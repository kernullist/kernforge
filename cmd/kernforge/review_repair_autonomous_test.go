package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeRequiredReviewerFailureRun builds a pre-write review run that recorded a
// required-reviewer failure while the main model still produced a usable
// self-review -- the exact shape that engages the advisory main-only fallback.
func makeRequiredReviewerFailureRun() *ReviewRun {
	return &ReviewRun{
		Trigger:   "pre_write",
		ModelPlan: ReviewModelPlan{RequiredRoles: []string{"cross_reviewer"}},
		ReviewerRuns: []ReviewReviewerRun{
			{Role: "cross_reviewer", Kind: "cross", Status: "failed", ModelQuality: reviewModelQualityFailed, Error: "reviewer returned empty response"},
			{Role: "main_reviewer", Kind: "main", Status: "completed", ModelQuality: reviewModelQualityUsable},
		},
		Gate: GateDecision{
			Verdict:          reviewVerdictInsufficientEvidence,
			BlockingFindings: []string{requiredReviewerFailureFindingID},
		},
		Findings: []ReviewFinding{
			{ID: requiredReviewerFailureFindingID, Severity: reviewSeverityBlocker, Category: "evidence_gap", Title: "Required review route failed", RequiredFix: "Fix the reviewer route.", BlocksGate: true},
		},
	}
}

// Defect B: a flaky independent reviewer that merely failed to return usable
// output must not permanently hard-block every edit in full mode. Full mode
// auto-approves the same advisory main-only fallback the user would otherwise
// opt into by text; non-full modes still require the explicit approval.
func TestPreWriteMainOnlyReviewerFallbackAutoApprovedInFullMode(t *testing.T) {
	root := t.TempDir()

	full := NewSession(root, "scripted", "m", "", "full")
	full.LastReviewRun = makeRequiredReviewerFailureRun()
	if !preWriteMainOnlyReviewerFallbackApproved(full) {
		t.Fatalf("full mode must auto-approve the main-only reviewer fallback after a required reviewer failure with a usable main self-review")
	}

	for _, mode := range []string{"edit", "plan", "default"} {
		s := NewSession(root, "scripted", "m", "", mode)
		s.LastReviewRun = makeRequiredReviewerFailureRun()
		if preWriteMainOnlyReviewerFallbackApproved(s) {
			t.Fatalf("mode %q must NOT auto-approve the fallback without explicit user approval text", mode)
		}
	}

	// Full mode still does not engage the fallback when there is no usable main
	// self-review (both routes are unusable): there is nothing to fall back to.
	noMain := NewSession(root, "scripted", "m", "", "full")
	run := makeRequiredReviewerFailureRun()
	run.ReviewerRuns = []ReviewReviewerRun{
		{Role: "cross_reviewer", Kind: "cross", Status: "failed", ModelQuality: reviewModelQualityFailed, Error: "reviewer returned empty response"},
	}
	noMain.LastReviewRun = run
	if preWriteMainOnlyReviewerFallbackApproved(noMain) {
		t.Fatalf("full mode must NOT auto-approve the fallback when no usable main self-review exists")
	}
}

// Defect A (builder level): the autonomous-stop reply must never ask an absent
// user to type y/n and must never record a pending two-turn confirmation, even
// when the permission mode would otherwise allow repair continuation.
func TestPreWriteReviewRepairAutonomousStopReplyOmitsYNAndPending(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "m", "", "full")
	session.LastReviewRun = &ReviewRun{
		Trigger: "pre_write",
		Gate:    GateDecision{Verdict: reviewVerdictNeedsRevision, BlockingFindings: []string{"RF-001"}},
		Findings: []ReviewFinding{
			{ID: "RF-001", Severity: reviewSeverityMedium, Category: "correctness", Path: "app.go", Title: "Needs a fix", RequiredFix: "Fix it.", BlocksGate: true},
		},
	}
	// A stale pending confirmation from an earlier round must be cleared.
	session.PendingReviewRepairConfirm = &ReviewRepairConfirmationState{ReviewID: "stale"}

	if !sessionAllowsReviewRepairContinuation(session) {
		t.Fatalf("full mode session should allow repair continuation (guard precondition for this test)")
	}

	reply := formatPreWriteReviewRepairAutonomousStopReply(Config{AutoLocale: boolPtr(false)}, session,
		"Pre-write review did not converge.", "쓰기 전 리뷰가 수렴하지 못했습니다.")

	for _, banned := range []string{"Reply with exactly", "Should I keep repairing", "[y=continue", "[y=계속"} {
		if strings.Contains(reply, banned) {
			t.Fatalf("autonomous-stop reply must not contain %q, got:\n%s", banned, reply)
		}
	}
	if !strings.Contains(reply, "review.blocking") || !strings.Contains(reply, "cross-review") {
		t.Fatalf("autonomous-stop reply must name the concrete remedy (review.blocking / cross-review), got:\n%s", reply)
	}
	if session.PendingReviewRepairConfirm != nil {
		t.Fatalf("autonomous-stop reply must clear any pending two-turn confirmation")
	}

	// Contrast: the interactive inline reply still records a pending confirmation
	// and appends the y/n tail for a mode that allows continuation.
	session.PendingReviewRepairConfirm = nil
	inline := formatPreWriteReviewRepairNonConvergenceReply(Config{AutoLocale: boolPtr(false)}, session, 3, 3)
	if !strings.Contains(inline, "Reply with exactly") {
		t.Fatalf("inline reply should keep the y/n tail, got:\n%s", inline)
	}
	if session.PendingReviewRepairConfirm == nil {
		t.Fatalf("inline reply should record a pending two-turn confirmation")
	}
}

// Defect A (loop level): an autonomous full-mode run whose pre-write review
// never converges must NOT dead-end on the first cap. It auto-continues a
// bounded number of times, then stops honestly without a phantom y/n prompt and
// without a pending confirmation no follow-up turn can consume.
func TestAutonomousFullModePreWriteNonConvergenceAutoContinuesThenStopsHonestly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("DATA_FILE = 'data.json'\n"), 0o644); err != nil {
		t.Fatalf("write app.py: %v", err)
	}
	session := NewSession(root, "scripted", "gpt-5.5", "", "full")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root, Perms: NewPermissionManager(ModeBypass, nil)}

	blockedErr := fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\nReview gate: needs_revision")

	// Every attempt blocks with a DISTINCT finding so the loop trips via
	// non-convergence (not the repeated-fingerprint cap) and never converges.
	const attempts = 40
	before := make([]func(), attempts)
	errs := make([]error, attempts)
	outputs := make([]string, attempts)
	for i := 0; i < attempts; i++ {
		id := fmt.Sprintf("RF-%03d", i)
		title := fmt.Sprintf("Distinct finding %d", i)
		fix := fmt.Sprintf("Apply the distinct fix number %d.", i)
		before[i] = func() {
			session.LastReviewRun = &ReviewRun{
				Trigger: "pre_write",
				Gate:    GateDecision{Verdict: reviewVerdictNeedsRevision, BlockingFindings: []string{id}},
				Result:  ReviewResult{Summary: "The proposal still leaves a distinct pre-write blocker unresolved."},
				Findings: []ReviewFinding{{
					ID: id, Severity: reviewSeverityMedium, Category: "correctness", Path: "app.py",
					Title: title, RequiredFix: fix, Quality: reviewFindingQualityComplete,
				}},
			}
		}
		errs[i] = blockedErr
	}
	patchTool := &sequenceTool{name: "apply_patch", before: before, errs: errs, outputs: outputs}

	// Interleave a re-read (read_file) after every blocked patch: a blocked
	// pre-write review requires the model to re-anchor before its next edit, so a
	// realistic loop reads between patches. Without this the re-anchor gate would
	// intercept the second patch and the loop could never reach the cap.
	readReply := toolCallResponse("read_file", map[string]any{"path": "app.py"})
	replies := make([]ChatResponse, 0, attempts*2)
	for i := 0; i < attempts; i++ {
		replies = append(replies, toolCallResponse("apply_patch", map[string]any{
			"patch": fmt.Sprintf("*** Begin Patch\n*** Update File: app.py\n@@\n DATA_FILE = 'data.json'\n+# attempt %d\n*** End Patch\n", i),
		}), readReply)
	}
	provider := &scriptedProviderClient{replies: replies}

	promptCount := 0
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false), PermissionMode: string(ModeBypass)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		// Interactive widget is present, but full mode must never call it.
		PromptContinueReviewRepair: func(string) (bool, error) {
			promptCount++
			return true, nil
		},
	}

	reply, err := agent.Reply(context.Background(), "implement the proposal and apply the change to app.py")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if promptCount != 0 {
		t.Fatalf("full mode must not invoke the interactive continue prompt, called %d times", promptCount)
	}
	// Auto-continued past the first cap: strictly more than one no-progress cap's
	// worth of blocked attempts landed before stopping.
	if patchTool.calls <= maxPreWriteReviewNoProgressRounds {
		t.Fatalf("expected the loop to auto-continue past the first cap, only %d patch attempts ran", patchTool.calls)
	}
	// Bounded: it did NOT run away.
	if patchTool.calls >= attempts {
		t.Fatalf("auto-continue must be bounded; it consumed all %d scripted attempts", patchTool.calls)
	}
	for _, banned := range []string{"Reply with exactly", "Should I keep repairing", "[y=continue"} {
		if strings.Contains(reply, banned) {
			t.Fatalf("autonomous full-mode stop must not ask an absent user y/n; reply contained %q:\n%s", banned, reply)
		}
	}
	if session.PendingReviewRepairConfirm != nil {
		t.Fatalf("autonomous full-mode stop must not record a pending two-turn confirmation")
	}
}
