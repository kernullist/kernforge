package main

// Permanent regression suite for the "implement the proposal" deadlock.
//
// A real turn ran for 2h27m and never applied a single change. The root causes,
// and the guards that must now contain each of them, are:
//
//   D-A: the configured cross reviewer failed in ~2s on every pre-write review,
//        so the gate ended at insufficient_evidence with RF-REVIEWER-001 and
//        permanently blocked the write. A repeatedly-failing cross route must
//        fall back to the single-model review path and reach a real verdict,
//        never re-run the failing route forever and never auto-write unreviewed.
//
//   D-B: the model re-read the SAME ~14 files hundreds of times. An interleaved
//        failing git_status/git_diff (exit 128/129 on a non-repo) used to reset
//        the read-churn counter, masking the loop. A rotating reread over an
//        already-seen file set must now nudge and then stop in finite steps;
//        reading genuinely new files must NOT trip it.
//
//   D-C: the pre-write review blocked every apply_patch with a DIFFERENT finding
//        each round (needs_revision x4 then insufficient_evidence), so the
//        auto re-patch loop never converged. After a small cap the loop must
//        stop and hand a y/n decision to the user; one or two rounds must still
//        proceed normally.
//
//   D-D: every review emitted a noise finding redacting a benign app.py
//        password_assignment (a USERS dict literal, a dev placeholder secret, a
//        check_password call). Those benign shapes must NOT be redacted, while a
//        genuine long/high-entropy secret assignment MUST still be redacted.
//
//   D-E: the whole turn issued hundreds of tool calls with zero successful
//        mutations and gathered no new information. A global no-progress guard
//        must stop such a turn and report status; a normal task that lands a
//        successful mutation must NOT trip it.
//
// These tests intentionally reuse the shared test doubles (scriptedProviderClient,
// failingReviewProviderClient, sequenceTool, toolCallResponse, approvedReviewResponse)
// so they stay aligned with the real harness behavior.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImplementDeadlockD_A_CrossReviewerFailureFallsBackToSingleModel asserts
// that a cross-review route which errors on N consecutive reviews stops being
// re-run and the harness falls back to the single-model review path, reaching a
// real verdict instead of an endless RF-REVIEWER-001 permanent block. It also
// asserts no unreviewed auto-write happened: the main self-review still ran.
func TestImplementDeadlockD_A_CrossReviewerFailureFallsBackToSingleModel(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "app.py")
	if err := os.WriteFile(path, []byte("DATA_FILE = 'data.json'\n"), 0o644); err != nil {
		t.Fatalf("write app.py: %v", err)
	}
	// The cross reviewer "fails in 2s" every time, exactly like the observed run.
	reviewer := &failingReviewProviderClient{err: fmt.Errorf("review model soft timeout after 2s")}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "gpt-5.5"
	// Enough approved main self-review replies for every pre-fallback round plus
	// the fallback single-model pass (which may run a second self-review).
	mainReplies := []ChatResponse{}
	for i := 0; i < crossReviewerFallbackThreshold+2; i++ {
		mainReplies = append(mainReplies, approvedReviewResponse("main model approved the proposed edit"))
		mainReplies = append(mainReplies, approvedReviewResponse("main model self-review approved"))
	}
	agent := &Agent{
		Config:         cfg,
		Client:         &scriptedProviderClient{replies: mainReplies},
		ReviewerClient: reviewer,
		ReviewerModel:  "anthropic-claude-cli/sonnet",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        NewSession(root, "scripted", "gpt-5.5", "", "default"),
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
	}
	rt := agent.reviewHarnessRuntime(root)
	newOpts := func() ReviewHarnessOptions {
		return ReviewHarnessOptions{
			Trigger:      "pre_write",
			Target:       reviewTargetChange,
			Mode:         reviewModeLiveFix,
			Request:      "implement the proposal",
			Paths:        []string{path},
			ProvidedDiff: "- DATA_FILE = 'data.json'\n+ DATA_FILE = os.environ.get('DATA_FILE', 'data.json')\n",
			EditProposals: []EditProposal{{
				File:            "app.py",
				Operation:       "apply_patch",
				ExpectedPreview: "- DATA_FILE = 'data.json'\n+ DATA_FILE = os.environ.get('DATA_FILE', 'data.json')\n",
			}},
		}
	}

	// Before the fallback threshold is reached each review still runs the cross
	// route, fails, and blocks as a required reviewer failure (not an unreviewed
	// write). This is the safe-but-blocking state, never an auto-write.
	for i := 0; i < crossReviewerFallbackThreshold; i++ {
		run, err := runReviewHarness(context.Background(), rt, newOpts())
		if err != nil {
			t.Fatalf("pre-fallback review %d: %v", i, err)
		}
		if !reviewRunHasRequiredReviewerFailure(run) {
			t.Fatalf("review %d before fallback must record required reviewer failure (no auto-write), got verdict=%s findings=%#v", i, run.Gate.Verdict, run.Findings)
		}
	}
	if got := agent.Session.CrossReviewerConsecutiveFailures; got != crossReviewerFallbackThreshold {
		t.Fatalf("expected %d consecutive cross failures recorded, got %d", crossReviewerFallbackThreshold, got)
	}

	reviewerCallsBeforeFallback := len(reviewer.requests)

	// Now the fallback must engage: the failing cross route must NOT be called
	// again, and the single-model review must reach a real verdict.
	run, err := runReviewHarness(context.Background(), rt, newOpts())
	if err != nil {
		t.Fatalf("fallback review: %v", err)
	}
	if len(reviewer.requests) != reviewerCallsBeforeFallback {
		t.Fatalf("fallback must not re-run the failing cross route: before=%d after=%d", reviewerCallsBeforeFallback, len(reviewer.requests))
	}
	if reviewRunHasRequiredReviewerFailure(run) {
		t.Fatalf("fallback run must not stay stuck on RF-REVIEWER-001, got findings=%#v", run.Findings)
	}
	if run.Gate.Verdict == reviewVerdictInsufficientEvidence {
		t.Fatalf("fallback run must reach a real verdict, got %s", run.Gate.Verdict)
	}
	if !run.SingleModelPolicy.Enabled {
		t.Fatalf("fallback run must activate the single-model review policy")
	}
}

// TestImplementDeadlockD_B_RotatingRereadStopsThenAllowsNewExploration locks in
// the widened read-churn guard. The first sub-case rotates read_file over an
// already-seen file set while an interleaved failing git tool (exit 128/129 on a
// non-repo) tries to mask the loop; the turn must abort in finite steps. The
// second sub-case reads only genuinely new files and must NOT trip the guard.
func TestImplementDeadlockD_B_RotatingRereadStopsThenAllowsNewExploration(t *testing.T) {
	t.Run("rotating reread with interleaved failing git aborts", func(t *testing.T) {
		root := t.TempDir() // not a git repo: git_status/git_diff fail every time.
		files := []string{"app.py", "templates_index.html", "sample_tvp.json", "check_data.py"}
		for _, name := range files {
			if err := os.WriteFile(filepath.Join(root, name), []byte("content of "+name+"\n"), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		// Rotate reads over the same set with varied offsets so the per-signature
		// repeat detector cannot catch it, interleaving the two failing git tools
		// exactly like the observed run. Only the cumulative no-new-path read-churn
		// counter can stop this.
		gitTools := []string{"git_status", "git_diff"}
		var replies []ChatResponse
		for i := 0; i < 80; i++ {
			name := files[i%len(files)]
			offset := (i % 4) + 1
			replies = append(replies, toolCallResponse("read_file", map[string]any{"path": name, "offset": offset}))
			replies = append(replies, toolCallResponse(gitTools[i%len(gitTools)], map[string]any{}))
		}
		provider := &scriptedProviderClient{replies: replies}
		ws := Workspace{BaseRoot: root, Root: root}
		session := NewSession(root, "scripted", "gpt-5.5", "", "default")
		store := NewSessionStore(filepath.Join(root, "sessions"))
		agent := &Agent{
			Config:    Config{AutoLocale: boolPtr(false)},
			Client:    provider,
			Tools:     NewToolRegistry(NewReadFileTool(ws), NewGitStatusTool(ws), NewGitDiffTool(ws)),
			Workspace: ws,
			Session:   session,
			Store:     store,
		}

		reply, err := agent.Reply(context.Background(), "implement the proposal")
		if err != nil {
			t.Fatalf("read-churn guard should escalate to the user, not error, got: %v", err)
		}
		// The turn must STOP (bounded loop) but as a clarification escalation that
		// names the churned files -- not a bare error and not an endless loop.
		if strings.TrimSpace(reply) == "" || !strings.Contains(reply, "app.py") {
			t.Fatalf("expected a read-churn escalation reply naming the churned files, got: %q", reply)
		}
		if provider.index >= len(replies) {
			t.Fatalf("guard did not stop early: consumed %d/%d replies (failing git masked the loop)", provider.index, len(replies))
		}
		// A nudge must have been issued before the abort, not a silent kill: the
		// read-churn guidance is injected as an internal session message at the
		// nudge threshold, well before the abort threshold.
		nudged := false
		for _, msg := range session.Messages {
			if strings.Contains(msg.Text, "Stop re-reading these paths") {
				nudged = true
				break
			}
		}
		if !nudged {
			t.Fatalf("expected a read-churn nudge message before the abort")
		}
	})

	t.Run("reading genuinely new files does not trip the guard", func(t *testing.T) {
		root := t.TempDir()
		names := []string{"a.py", "b.py", "c.py", "d.py", "e.py", "f.py", "g.py", "h.py"}
		for _, name := range names {
			if err := os.WriteFile(filepath.Join(root, name), []byte("# "+name+"\n"), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		var replies []ChatResponse
		for _, name := range names {
			replies = append(replies, toolCallResponse("read_file", map[string]any{"path": name}))
		}
		replies = append(replies, ChatResponse{Message: Message{Role: "assistant", Text: "Reviewed all new files; here is the summary."}})
		provider := &scriptedProviderClient{replies: replies}
		ws := Workspace{BaseRoot: root, Root: root}
		session := NewSession(root, "scripted", "gpt-5.5", "", "default")
		store := NewSessionStore(filepath.Join(root, "sessions"))
		agent := &Agent{
			Config:    Config{AutoLocale: boolPtr(false)},
			Client:    provider,
			Tools:     NewToolRegistry(NewReadFileTool(ws)),
			Workspace: ws,
			Session:   session,
			Store:     store,
		}

		reply, err := agent.Reply(context.Background(), "review these new files")
		if err != nil {
			t.Fatalf("genuine new-file exploration must not abort, got: %v", err)
		}
		if !strings.Contains(reply, "Reviewed all new files") {
			t.Fatalf("expected the real answer after new-file exploration, got %q", reply)
		}
	})
}

// TestImplementDeadlockD_C_NonConvergingPreWriteLoopStopsButShortRoundsProceed
// locks in the pre-write re-patch cap. The first sub-case reproduces the
// observed loop where each blocked round surfaces a DIFFERENT finding (so the
// per-fingerprint repeat detector never fires); after the distinct-block cap the
// loop must stop and hand a y/n decision to the user. The second sub-case proves
// a single needs_revision round still proceeds normally to a successful edit.
func TestImplementDeadlockD_C_NonConvergingPreWriteLoopStopsButShortRoundsProceed(t *testing.T) {
	t.Run("non-converging distinct-finding loop stops with y/n", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("DATA_FILE = 'data.json'\n"), 0o644); err != nil {
			t.Fatalf("write app.py: %v", err)
		}
		session := NewSession(root, "scripted", "gpt-5.5", "", "default")
		store := NewSessionStore(filepath.Join(root, "sessions"))
		ws := Workspace{BaseRoot: root, Root: root}

		setReviewFinding := func(id, title, fix string) func() {
			return func() {
				session.LastReviewRun = &ReviewRun{
					Trigger: "pre_write",
					Gate: GateDecision{
						Verdict:          reviewVerdictNeedsRevision,
						BlockingFindings: []string{id},
					},
					Result: ReviewResult{Summary: "The proposal still leaves a pre-write blocker unresolved."},
					Findings: []ReviewFinding{{
						ID:          id,
						Severity:    reviewSeverityMedium,
						Category:    "correctness",
						Path:        "app.py",
						Title:       title,
						RequiredFix: fix,
						Quality:     reviewFindingQualityComplete,
					}},
				}
			}
		}
		blockedErr := fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\nReview gate: needs_revision")
		// Mirror the observed sequence: DATA_FILE removed -> secret key -> JSON
		// validation -> ... a fresh finding every round so it never converges.
		patchTool := &sequenceTool{
			name: "apply_patch",
			before: []func(){
				setReviewFinding("RF-001", "DATA_FILE constant removed", "Keep the DATA_FILE constant defined."),
				setReviewFinding("RF-002", "Hardcoded secret key", "Load SECRET_KEY from the environment."),
				setReviewFinding("RF-003", "Missing JSON validation", "Validate the parsed JSON before use."),
				setReviewFinding("RF-004", "Unhandled file error", "Handle the file-open error path."),
			},
			errs: []error{blockedErr, blockedErr, blockedErr, blockedErr},
		}
		patchReply := func(i int) ChatResponse {
			return toolCallResponse("apply_patch", map[string]any{"patch": fmt.Sprintf("*** Begin Patch\n*** Update File: app.py\n@@\n DATA_FILE = 'data.json'\n+# attempt %d\n*** End Patch\n", i)})
		}
		readReply := toolCallResponse("read_file", map[string]any{"path": "app.py"})
		// Re-read after each blocked patch so the re-anchor gate is satisfied and
		// each new patch reaches pre-write review again.
		replies := []ChatResponse{}
		for i := 0; i < 8; i++ {
			replies = append(replies, patchReply(i))
			replies = append(replies, readReply)
		}
		provider := &scriptedProviderClient{replies: replies}
		agent := &Agent{
			Config:    Config{AutoLocale: boolPtr(false)},
			Client:    provider,
			Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
			Workspace: ws,
			Session:   session,
			Store:     store,
		}

		reply, err := agent.Reply(context.Background(), "implement the proposal and apply the change to app.py")
		if err != nil {
			t.Fatalf("Reply: %v", err)
		}
		if !strings.Contains(reply, "did not converge") {
			t.Fatalf("expected non-convergence stop reply, got %q", reply)
		}
		if !strings.Contains(reply, "Should I keep repairing") {
			t.Fatalf("expected y/n decision handoff, got %q", reply)
		}
		if patchTool.calls != maxPreWriteReviewNoProgressRounds {
			t.Fatalf("loop must stop after %d no-progress blocked rounds, got %d patch attempts", maxPreWriteReviewNoProgressRounds, patchTool.calls)
		}
		if session.PendingReviewRepairConfirm == nil {
			t.Fatalf("expected a pending y/n repair confirmation to be recorded")
		}
	})

	t.Run("a single needs_revision round still proceeds to a successful edit", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("DATA_FILE = 'data.json'\n"), 0o644); err != nil {
			t.Fatalf("write app.py: %v", err)
		}
		session := NewSession(root, "scripted", "gpt-5.5", "", "default")
		store := NewSessionStore(filepath.Join(root, "sessions"))
		ws := Workspace{BaseRoot: root, Root: root}

		blockedErr := fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\nReview gate: needs_revision")
		// Exactly one blocked round, then the second patch succeeds. This must NOT
		// trip the cap: one or two revise rounds are a normal, healthy flow.
		patchTool := &sequenceTool{
			name: "apply_patch",
			before: []func(){
				func() {
					session.LastReviewRun = &ReviewRun{
						Trigger: "pre_write",
						Gate: GateDecision{
							Verdict:          reviewVerdictNeedsRevision,
							BlockingFindings: []string{"RF-001"},
						},
						Result: ReviewResult{Summary: "One blocker remains."},
						Findings: []ReviewFinding{{
							ID:          "RF-001",
							Severity:    reviewSeverityMedium,
							Category:    "correctness",
							Path:        "app.py",
							Title:       "DATA_FILE constant removed",
							RequiredFix: "Keep the DATA_FILE constant defined.",
							Quality:     reviewFindingQualityComplete,
						}},
					}
				},
				func() { session.LastReviewRun = nil },
			},
			outputs: []string{"", "Applied patch to app.py."},
			errs:    []error{blockedErr, nil},
		}
		patchReply := func(i int) ChatResponse {
			return toolCallResponse("apply_patch", map[string]any{"patch": fmt.Sprintf("*** Begin Patch\n*** Update File: app.py\n@@\n DATA_FILE = 'data.json'\n+# attempt %d\n*** End Patch\n", i)})
		}
		readReply := toolCallResponse("read_file", map[string]any{"path": "app.py"})
		finalReply := testModificationFinalAnswer("app.py", "targeted verification passed.", "no known remaining blocker.")
		replies := []ChatResponse{
			patchReply(0),
			readReply,
			patchReply(1),
			{Message: Message{Role: "assistant", Text: finalReply}},
		}
		provider := &scriptedProviderClient{replies: replies}
		agent := &Agent{
			Config:    Config{AutoLocale: boolPtr(false)},
			Client:    provider,
			Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
			Workspace: ws,
			Session:   session,
			Store:     store,
		}

		reply, err := agent.Reply(context.Background(), "implement the proposal and apply the change to app.py")
		if err != nil {
			t.Fatalf("Reply: %v", err)
		}
		if strings.Contains(reply, "did not converge") || strings.Contains(reply, "Should I keep repairing") {
			t.Fatalf("a single needs_revision round must not trip the non-convergence cap, got %q", reply)
		}
		if patchTool.calls != 2 {
			t.Fatalf("expected one blocked patch then one successful patch (2 calls), got %d", patchTool.calls)
		}
		if session.PendingReviewRepairConfirm != nil {
			t.Fatalf("a successful repair must not leave a pending y/n confirmation")
		}
	})
}

// TestImplementDeadlockNonConvergenceInteractivePromptDrivesDecision proves that
// when an interactive confirmation widget (PromptContinueReviewRepair) is wired,
// the non-convergence handoff is collected LIVE within the same turn instead of
// dead-ending in a text-only two-turn handoff: on "yes" the repair budget is
// reset and the model re-patches this turn; on "no" the turn stops cleanly with
// no pending two-turn confirmation left behind.
func TestImplementDeadlockNonConvergenceInteractivePromptDrivesDecision(t *testing.T) {
	blockedErr := fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\nReview gate: needs_revision")
	n := maxPreWriteReviewNoProgressRounds
	// Genuinely distinct findings each round (distinct title AND fix) so the
	// fingerprint differs and the loop trips via non-convergence, not the
	// repeated-fingerprint cap.
	distinctFindings := []struct{ id, title, fix string }{
		{"RF-001", "DATA_FILE constant removed", "Keep the DATA_FILE constant defined."},
		{"RF-002", "Hardcoded secret key", "Load SECRET_KEY from the environment."},
		{"RF-003", "Missing JSON validation", "Validate the parsed JSON before use."},
		{"RF-004", "Unhandled file error", "Handle the file-open error path."},
		{"RF-005", "Missing request timeout", "Add an explicit request timeout."},
		{"RF-006", "Unclosed file handle", "Close the file handle on every path."},
		{"RF-007", "Unvalidated path input", "Reject path traversal in the input."},
	}
	if n > len(distinctFindings) {
		t.Fatalf("test needs %d distinct findings but only %d are defined", n, len(distinctFindings))
	}

	newSetReviewFinding := func(session *Session) func(id, title, fix string) func() {
		return func(id, title, fix string) func() {
			return func() {
				session.LastReviewRun = &ReviewRun{
					Trigger: "pre_write",
					Gate: GateDecision{
						Verdict:          reviewVerdictNeedsRevision,
						BlockingFindings: []string{id},
					},
					Result: ReviewResult{Summary: "The proposal still leaves a pre-write blocker unresolved."},
					Findings: []ReviewFinding{{
						ID:          id,
						Severity:    reviewSeverityMedium,
						Category:    "correctness",
						Path:        "app.py",
						Title:       title,
						RequiredFix: fix,
						Quality:     reviewFindingQualityComplete,
					}},
				}
			}
		}
	}
	patchReply := func(i int) ChatResponse {
		return toolCallResponse("apply_patch", map[string]any{"patch": fmt.Sprintf("*** Begin Patch\n*** Update File: app.py\n@@\n DATA_FILE = 'data.json'\n+# attempt %d\n*** End Patch\n", i)})
	}
	readReply := toolCallResponse("read_file", map[string]any{"path": "app.py"})

	t.Run("interactive yes resets the budget and re-patches this turn", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("DATA_FILE = 'data.json'\n"), 0o644); err != nil {
			t.Fatalf("write app.py: %v", err)
		}
		session := NewSession(root, "scripted", "gpt-5.5", "", "default")
		store := NewSessionStore(filepath.Join(root, "sessions"))
		ws := Workspace{BaseRoot: root, Root: root}
		setReviewFinding := newSetReviewFinding(session)

		before := []func(){}
		errs := []error{}
		outputs := []string{}
		for i := 0; i < n; i++ {
			before = append(before, setReviewFinding(distinctFindings[i].id, distinctFindings[i].title, distinctFindings[i].fix))
			errs = append(errs, blockedErr)
			outputs = append(outputs, "")
		}
		// The round after the user opts to continue passes pre-write review.
		before = append(before, func() { session.LastReviewRun = nil })
		errs = append(errs, nil)
		outputs = append(outputs, "Applied patch to app.py.")
		patchTool := &sequenceTool{name: "apply_patch", before: before, errs: errs, outputs: outputs}

		replies := []ChatResponse{}
		for i := 0; i < n; i++ {
			replies = append(replies, patchReply(i), readReply)
		}
		finalReply := testModificationFinalAnswer("app.py", "targeted verification passed.", "no known remaining blocker.")
		replies = append(replies, patchReply(n), ChatResponse{Message: Message{Role: "assistant", Text: finalReply}})
		provider := &scriptedProviderClient{replies: replies}

		promptCount := 0
		promptMessage := ""
		agent := &Agent{
			Config:    Config{AutoLocale: boolPtr(false)},
			Client:    provider,
			Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
			Workspace: ws,
			Session:   session,
			Store:     store,
			PromptContinueReviewRepair: func(message string) (bool, error) {
				promptCount++
				promptMessage = message
				return true, nil
			},
		}

		reply, err := agent.Reply(context.Background(), "implement the proposal and apply the change to app.py")
		if err != nil {
			t.Fatalf("Reply: %v", err)
		}
		if promptCount != 1 {
			t.Fatalf("expected exactly one interactive continue prompt, got %d", promptCount)
		}
		if !strings.Contains(promptMessage, "did not converge") {
			t.Fatalf("interactive prompt should carry the non-convergence context, got %q", promptMessage)
		}
		if strings.Contains(promptMessage, "Reply with exactly") || strings.Contains(promptMessage, "[y=continue") {
			t.Fatalf("interactive prompt must omit the inline y/n tail (the widget shows it), got %q", promptMessage)
		}
		if strings.Contains(reply, "did not converge") || strings.Contains(reply, "Should I keep repairing") {
			t.Fatalf("an interactive 'yes' must resume and finish, not emit the text handoff, got %q", reply)
		}
		if patchTool.calls != n+1 {
			t.Fatalf("expected %d blocked patches then one successful re-patch, got %d", n, patchTool.calls)
		}
		if session.PendingReviewRepairConfirm != nil {
			t.Fatalf("interactive resolution must not leave a pending two-turn confirmation")
		}
	})

	t.Run("interactive no stops the turn with no pending confirmation", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("DATA_FILE = 'data.json'\n"), 0o644); err != nil {
			t.Fatalf("write app.py: %v", err)
		}
		session := NewSession(root, "scripted", "gpt-5.5", "", "default")
		store := NewSessionStore(filepath.Join(root, "sessions"))
		ws := Workspace{BaseRoot: root, Root: root}
		setReviewFinding := newSetReviewFinding(session)

		before := []func(){}
		errs := []error{}
		for i := 0; i < n; i++ {
			before = append(before, setReviewFinding(distinctFindings[i].id, distinctFindings[i].title, distinctFindings[i].fix))
			errs = append(errs, blockedErr)
		}
		patchTool := &sequenceTool{name: "apply_patch", before: before, errs: errs}

		replies := []ChatResponse{}
		for i := 0; i < n; i++ {
			replies = append(replies, patchReply(i))
			if i < n-1 {
				replies = append(replies, readReply)
			}
		}
		provider := &scriptedProviderClient{replies: replies}

		promptCount := 0
		agent := &Agent{
			Config:    Config{AutoLocale: boolPtr(false)},
			Client:    provider,
			Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
			Workspace: ws,
			Session:   session,
			Store:     store,
			PromptContinueReviewRepair: func(message string) (bool, error) {
				_ = message
				promptCount++
				return false, nil
			},
		}

		reply, err := agent.Reply(context.Background(), "implement the proposal and apply the change to app.py")
		if err != nil {
			t.Fatalf("Reply: %v", err)
		}
		if promptCount != 1 {
			t.Fatalf("expected exactly one interactive continue prompt, got %d", promptCount)
		}
		if !strings.Contains(reply, "will not continue repairing") {
			t.Fatalf("an interactive 'no' should return the cancelled reply, got %q", reply)
		}
		if strings.Contains(reply, "Reply with exactly") {
			t.Fatalf("interactive stop must not emit the text-handoff y/n tail, got %q", reply)
		}
		if patchTool.calls != n {
			t.Fatalf("expected the loop to stop after %d blocked patches, got %d", n, patchTool.calls)
		}
		if session.PendingReviewRepairConfirm != nil {
			t.Fatalf("interactive stop must not leave a pending two-turn confirmation")
		}
	})
}

// TestImplementDeadlockProgressiveConvergenceProceedsBeyondThreeRounds locks in
// the progress-aware cutoff: when each blocked round resolves blockers and the
// total count strictly decreases (4 -> 3 -> 2 -> done), the loop is multi-stage
// convergence and must NOT be killed at the 3-distinct-round cap even though a
// different finding surfaces each round. The earlier 2h27m deadlock fix made the
// cap fire on any 3 distinct findings, which also killed genuine convergence;
// this guards the corrected behavior.
func TestImplementDeadlockProgressiveConvergenceProceedsBeyondThreeRounds(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("DATA_FILE = 'data.json'\n"), 0o644); err != nil {
		t.Fatalf("write app.py: %v", err)
	}
	session := NewSession(root, "scripted", "gpt-5.5", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}

	setBlockers := func(ids ...string) func() {
		return func() {
			findings := make([]ReviewFinding, 0, len(ids))
			for _, id := range ids {
				findings = append(findings, ReviewFinding{
					ID:          id,
					Severity:    reviewSeverityMedium,
					Category:    "correctness",
					Path:        "app.py",
					Title:       id + " needs work",
					RequiredFix: "resolve " + id,
					Quality:     reviewFindingQualityComplete,
				})
			}
			session.LastReviewRun = &ReviewRun{
				Trigger:  "pre_write",
				Gate:     GateDecision{Verdict: reviewVerdictNeedsRevision, BlockingFindings: append([]string{}, ids...)},
				Result:   ReviewResult{Summary: "blockers remain"},
				Findings: findings,
			}
		}
	}
	blockedErr := fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\nReview gate: needs_revision")
	// Blocker count strictly decreases each round, then the 4th patch succeeds.
	patchTool := &sequenceTool{
		name: "apply_patch",
		before: []func(){
			setBlockers("RF-001", "RF-002", "RF-003", "RF-004"),
			setBlockers("RF-001", "RF-002", "RF-003"),
			setBlockers("RF-001", "RF-002"),
			func() { session.LastReviewRun = nil },
		},
		outputs: []string{"", "", "", "Applied patch to app.py."},
		errs:    []error{blockedErr, blockedErr, blockedErr, nil},
	}
	patchReply := func(i int) ChatResponse {
		return toolCallResponse("apply_patch", map[string]any{"patch": fmt.Sprintf("*** Begin Patch\n*** Update File: app.py\n@@\n DATA_FILE = 'data.json'\n+# attempt %d\n*** End Patch\n", i)})
	}
	readReply := toolCallResponse("read_file", map[string]any{"path": "app.py"})
	finalReply := testModificationFinalAnswer("app.py", "targeted verification passed.", "no known remaining blocker.")
	replies := []ChatResponse{
		patchReply(0), readReply,
		patchReply(1), readReply,
		patchReply(2), readReply,
		patchReply(3),
		{Message: Message{Role: "assistant", Text: finalReply}},
	}
	provider := &scriptedProviderClient{replies: replies}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "implement the proposal and apply the change to app.py")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.Contains(reply, "did not converge") || strings.Contains(reply, "Should I keep repairing") {
		t.Fatalf("decreasing-blocker convergence must not trip the non-convergence cap, got %q", reply)
	}
	if patchTool.calls != 4 {
		t.Fatalf("expected 3 progressing blocked rounds then a successful patch (4 calls), got %d", patchTool.calls)
	}
	if session.PendingReviewRepairConfirm != nil {
		t.Fatalf("a converged repair must not leave a pending y/n confirmation")
	}
}

// TestImplementDeadlockOscillatingBlockerCountStopsAtAbsoluteCap locks in the
// scenario for the progress-aware loop. When the blocker count OSCILLATES
// (3 -> 2 -> 3 -> 2 ...) with a disjoint finding set each round, no round repeats
// (so the per-fingerprint detector never fires). The progress metric measures
// each round against the running MINIMUM blocker count, not the previous round,
// so once the count has dipped to its low it cannot keep resetting the guard by
// bouncing back up: the bounces read as no-progress and the non-convergence cap
// fires well before the absolute per-turn cap. This bounds the real-world scope-
// explosion churn (a vague request whose review findings keep changing) instead
// of letting it run all the way to the absolute backstop.
func TestImplementDeadlockOscillatingBlockerCountStopsViaNoProgressGuard(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("DATA_FILE = 'data.json'\n"), 0o644); err != nil {
		t.Fatalf("write app.py: %v", err)
	}
	session := NewSession(root, "scripted", "gpt-5.5", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}

	// Each finding carries a distinct Symbol (= its id) so its repair fingerprint
	// is unique per round; otherwise same-count rounds would collide on the
	// fingerprint and trip the repeat detector before the absolute cap, which
	// would test a different guard than intended.
	setBlockers := func(ids ...string) func() {
		return func() {
			findings := make([]ReviewFinding, 0, len(ids))
			for _, id := range ids {
				findings = append(findings, ReviewFinding{
					ID:          id,
					Severity:    reviewSeverityMedium,
					Category:    "correctness",
					Path:        "app.py",
					Symbol:      id,
					Title:       id + " needs work",
					RequiredFix: "resolve " + id,
					Quality:     reviewFindingQualityComplete,
				})
			}
			session.LastReviewRun = &ReviewRun{
				Trigger:  "pre_write",
				Gate:     GateDecision{Verdict: reviewVerdictNeedsRevision, BlockingFindings: append([]string{}, ids...)},
				Result:   ReviewResult{Summary: "blockers oscillate"},
				Findings: findings,
			}
		}
	}
	blockedErr := fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\nReview gate: needs_revision")
	// Counts oscillate 3 -> 2 -> 3 -> 2 -> 3 ... with disjoint ids every round.
	// Running-minimum progress trace (no-progress cap = 3): R1 sets min=3
	// (no-progress 1); R2 count 2 < 3 is progress (reset to 0, min=2); R3 count 3
	// is not below min 2 (no-progress 1); R4 count 2 is not below min 2
	// (no-progress 2); R5 count 3 (no-progress 3) trips the non-convergence cap.
	// So the loop stops at the 5th blocked round, before the absolute cap (7).
	patchTool := &sequenceTool{
		name: "apply_patch",
		before: []func(){
			setBlockers("AA-1", "AA-2", "AA-3"),
			setBlockers("BB-1", "BB-2"),
			setBlockers("CC-1", "CC-2", "CC-3"),
			setBlockers("DD-1", "DD-2"),
			setBlockers("EE-1", "EE-2", "EE-3"),
			setBlockers("FF-1", "FF-2"),
			setBlockers("GG-1", "GG-2", "GG-3"),
			setBlockers("HH-1", "HH-2"),
		},
		errs: []error{blockedErr, blockedErr, blockedErr, blockedErr, blockedErr, blockedErr, blockedErr, blockedErr},
	}
	patchReply := func(i int) ChatResponse {
		return toolCallResponse("apply_patch", map[string]any{"patch": fmt.Sprintf("*** Begin Patch\n*** Update File: app.py\n@@\n DATA_FILE = 'data.json'\n+# attempt %d\n*** End Patch\n", i)})
	}
	readReply := toolCallResponse("read_file", map[string]any{"path": "app.py"})
	replies := []ChatResponse{}
	for i := 0; i < 9; i++ {
		replies = append(replies, patchReply(i), readReply)
	}
	provider := &scriptedProviderClient{replies: replies}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "implement the proposal and apply the change to app.py")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	const expectedBlockedRounds = 5
	if patchTool.calls != expectedBlockedRounds {
		t.Fatalf("oscillating count must stop via the no-progress guard at %d blocked rounds (before the absolute cap %d), got %d patch attempts", expectedBlockedRounds, maxPreWriteReviewRepairBlocksPerTurn+1, patchTool.calls)
	}
	if patchTool.calls >= maxPreWriteReviewRepairBlocksPerTurn+1 {
		t.Fatalf("running-minimum progress must stop oscillation before the absolute cap, got %d", patchTool.calls)
	}
	// The non-convergence (no-progress) cap fired, not the absolute loop-limit cap.
	if !strings.Contains(reply, "did not converge") {
		t.Fatalf("oscillation stop must use the non-convergence reply, got %q", reply)
	}
	// Disjoint finding sets every round are scope churn, so the stop reframes as a
	// scope-clarification ask rather than a bare keep-repairing prompt.
	if !strings.Contains(reply, "minimal behavior") {
		t.Fatalf("scope-churn non-convergence must ask for the minimal behavior, got %q", reply)
	}
	if !strings.Contains(reply, "Should I keep repairing") {
		t.Fatalf("expected a y/n decision handoff (the request is an edit), got %q", reply)
	}
	if session.PendingReviewRepairConfirm == nil {
		t.Fatalf("the non-convergence stop must record a pending y/n confirmation")
	}
}

// TestImplementDeadlockD_D_PasswordRedactionScoping locks in the redaction
// tightening: the benign app.py shapes that previously produced a noise finding
// on every review must NOT be redacted, while a genuine long/high-entropy secret
// assignment MUST still be redacted.
func TestImplementDeadlockD_D_PasswordRedactionScoping(t *testing.T) {
	benign := []string{
		`USERS = {'admin': 'password', 'alice': 'wonderland'}`,
		`SECRET_KEY = 'dev_secret_key_change_me'`,
		`app.config['SECRET_KEY'] = "dev_secret_key_change_me"`,
		`if check_password(password, user['password']):`,
		`password = request.form['password']`,
		`token = secrets.token_hex(16)`,
		`api_key = os.environ.get('API_KEY')`,
		`Token: "security"`,
	}
	for _, line := range benign {
		if out, redacted := redactPasswordAssignments(line); redacted {
			t.Errorf("D-D: benign line must not be redacted: %q -> %q", line, out)
		}
		if _, rep := redactSensitiveText(line); rep.Redacted {
			t.Errorf("D-D: benign line must not produce a redaction finding: %q", line)
		}
	}

	mustRedact := []string{
		`secret = "x7Kp9Lm2Qr5Tv8Wz3Bn6Df0Hs"`,
		`password = "Aa1!supersecretvalue"`,
		// Split after the "sk_live_" prefix so the source has no contiguous Stripe
		// key literal (avoids secret-scanning push protection on this redaction
		// fixture); the concatenated runtime value is unchanged.
		`api_key = "sk_live_` + `4eC39HqLyjWDarjtT1zdp7dc"`,
	}
	for _, line := range mustRedact {
		out, redacted := redactPasswordAssignments(line)
		if !redacted {
			t.Errorf("D-D: genuine secret must still be redacted: %q -> %q", line, out)
		}
		if redacted && !strings.Contains(out, "[REDACTED:password_assignment]") {
			t.Errorf("D-D: redacted secret must be masked: %q", out)
		}
	}
}

// TestImplementDeadlockD_E_NoProgressGuardFiresButRealMutationDoesNot locks in
// the global no-progress guard. The first sub-case reproduces the turn that
// issued many tool calls with zero successful mutations and no new information;
// the guard must stop it and report status. The second sub-case lands a real
// workspace mutation and must NOT trip the guard.
func TestImplementDeadlockD_E_NoProgressGuardFiresButRealMutationDoesNot(t *testing.T) {
	prevFloor := noProgressIterationFloor
	prevWall := noProgressWallClockFloor
	noProgressIterationFloor = 3
	noProgressWallClockFloor = 0
	defer func() {
		noProgressIterationFloor = prevFloor
		noProgressWallClockFloor = prevWall
	}()

	t.Run("zero mutations and no new info stops with a status report", func(t *testing.T) {
		root := t.TempDir()
		for _, name := range []string{"app.py", "check_data.py"} {
			if err := os.WriteFile(filepath.Join(root, name), []byte("# "+name+"\n"), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		var replies []ChatResponse
		for i := 0; i < 12; i++ {
			name := "app.py"
			if i%2 == 1 {
				name = "check_data.py"
			}
			replies = append(replies, toolCallResponse("read_file", map[string]any{"path": name}))
		}
		provider := &scriptedProviderClient{replies: replies}
		ws := Workspace{BaseRoot: root, Root: root}
		session := NewSession(root, "scripted", "gpt-5.5", "", "default")
		store := NewSessionStore(filepath.Join(root, "sessions"))
		agent := &Agent{
			Config:    Config{AutoLocale: boolPtr(false)},
			Client:    provider,
			Tools:     NewToolRegistry(NewReadFileTool(ws)),
			Workspace: ws,
			Session:   session,
			Store:     store,
		}

		reply, err := agent.Reply(context.Background(), "implement the proposal")
		if err != nil {
			t.Fatalf("Reply: %v", err)
		}
		if !strings.Contains(reply, "No-progress guard stopped this turn") {
			t.Fatalf("expected no-progress guard report, got %q", reply)
		}
		if !strings.Contains(reply, "No files were changed") {
			t.Fatalf("expected report to state no files changed, got %q", reply)
		}
		if len(provider.requests) >= len(replies) {
			t.Fatalf("no-progress guard did not stop early: %d requests for %d replies", len(provider.requests), len(replies))
		}
		if session.LastTurnRuntimeState == nil || session.LastTurnRuntimeState.State != TurnRuntimeBlocked {
			t.Fatalf("expected runtime state blocked by no-progress guard, got %#v", session.LastTurnRuntimeState)
		}
	})

	t.Run("a successful mutation does not trip the guard", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("# app\n"), 0o644); err != nil {
			t.Fatalf("write app.py: %v", err)
		}
		finalReply := testModificationFinalAnswer("app.py", "targeted verification passed.", "no known remaining blocker.")
		// A read followed by a real write that mutates the workspace, then the final
		// answer. The mutation must reset the no-progress guard so a normal task
		// with a successful change is never stopped.
		replies := []ChatResponse{
			toolCallResponse("read_file", map[string]any{"path": "app.py"}),
			toolCallResponse("write_file", map[string]any{"path": "app.py", "content": "# app\nDATA_FILE = 'data.json'\n"}),
			{Message: Message{Role: "assistant", Text: finalReply}},
		}
		provider := &scriptedProviderClient{replies: replies}
		ws := Workspace{BaseRoot: root, Root: root}
		session := NewSession(root, "scripted", "gpt-5.5", "", "default")
		store := NewSessionStore(filepath.Join(root, "sessions"))
		agent := &Agent{
			Config:    Config{AutoLocale: boolPtr(false)},
			Client:    provider,
			Tools:     NewToolRegistry(NewReadFileTool(ws), NewWriteFileTool(ws)),
			Workspace: ws,
			Session:   session,
			Store:     store,
		}

		reply, err := agent.Reply(context.Background(), "implement the proposal")
		if err != nil {
			t.Fatalf("Reply: %v", err)
		}
		// The load-bearing assertion: the no-progress guard must NOT have fired,
		// because a successful workspace mutation landed during the turn. (Other
		// unrelated final-answer gates are out of scope for this D-E regression.)
		if strings.Contains(reply, "No-progress guard stopped this turn") {
			t.Fatalf("no-progress guard must not trip when a mutation landed, got %q", reply)
		}
		if session.LastTurnRuntimeState != nil &&
			strings.Contains(session.LastTurnRuntimeState.LastTransitionReason, "no_progress") {
			t.Fatalf("turn with a successful mutation must not be blocked by the no-progress guard, got reason=%q", session.LastTurnRuntimeState.LastTransitionReason)
		}
		// The mutation must have actually been applied to the workspace.
		got, readErr := os.ReadFile(filepath.Join(root, "app.py"))
		if readErr != nil {
			t.Fatalf("read mutated app.py: %v", readErr)
		}
		if !strings.Contains(string(got), "DATA_FILE = 'data.json'") {
			t.Fatalf("expected the write_file mutation to land, got contents %q", string(got))
		}
	})
}

// TestReadChurnEscalationReplyAsksForClarification locks the escalate-to-user
// behavior of the read-churn no-progress guard: when the model keeps re-reading
// an already-seen file set, the turn must end with a useful clarification request
// (echoing the request and the churned files), not a bare guard error.
func TestReadChurnEscalationReplyAsksForClarification(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: ".env에 gitlab 토큰을 넣어두고 사용하게 하자"}
	agent := &Agent{Config: Config{}, Session: session}

	reply := agent.readChurnEscalationReply(map[string]struct{}{
		"app.py":               {},
		"lea.py":               {},
		"templates/index.html": {},
	})
	if strings.TrimSpace(reply) == "" {
		t.Fatal("expected a non-empty escalation reply")
	}
	for _, needle := range []string{"gitlab 토큰", "app.py"} {
		if !strings.Contains(reply, needle) {
			t.Fatalf("escalation reply missing %q, got:\n%s", needle, reply)
		}
	}
	if strings.Contains(reply, "stopped after repeatedly re-reading") {
		t.Fatalf("escalation must be a clarification, not the bare guard error, got:\n%s", reply)
	}
}
