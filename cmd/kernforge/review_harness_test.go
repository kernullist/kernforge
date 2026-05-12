package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func indexStringContaining(items []string, needle string) int {
	for i, item := range items {
		if strings.Contains(item, needle) {
			return i
		}
	}
	return -1
}

func reviewRunHasFindingTitle(run ReviewRun, title string) bool {
	for _, finding := range run.Findings {
		if finding.Title == title {
			return true
		}
	}
	return false
}

type reviewRetryFailureProviderClient struct {
	first    ChatResponse
	err      error
	index    int
	requests []ChatRequest
}

func (r *reviewRetryFailureProviderClient) Name() string {
	return "review-retry-failure"
}

func (r *reviewRetryFailureProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	r.requests = append(r.requests, req)
	if r.index == 0 {
		r.index++
		return r.first, nil
	}
	r.index++
	return ChatResponse{}, r.err
}

type failingReviewProviderClient struct {
	err      error
	requests []ChatRequest
}

func (f *failingReviewProviderClient) Name() string {
	return "failing-reviewer"
}

func (f *failingReviewProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	f.requests = append(f.requests, req)
	if f.err != nil {
		return ChatResponse{}, f.err
	}
	return ChatResponse{}, fmt.Errorf("synthetic reviewer route failure")
}

func TestReviewHarnessNoModelWritesTypedArtifact(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("int main(){return 0;}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runTestGit(t, root, "add", "main.cpp")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("int main(){return 1;}\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}
	rt := &runtimeState{
		cfg:       DefaultConfig(root),
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:        "explicit_command",
		Target:         "change",
		Request:        "review current change",
		NoModel:        true,
		IncludeGitDiff: true,
	})
	if err != nil {
		t.Fatalf("run review: %v", err)
	}
	if run.SchemaVersion != reviewSchemaVersion {
		t.Fatalf("unexpected schema version: %s", run.SchemaVersion)
	}
	if run.Target != reviewTargetChange || run.ChangeSet.Fingerprint == "" {
		t.Fatalf("review did not collect change set: %#v", run.ChangeSet)
	}
	if run.ArtifactRefs == nil || len(run.ArtifactRefs) < 2 {
		t.Fatalf("expected artifacts, got %#v", run.ArtifactRefs)
	}
	if _, err := os.Stat(filepath.Join(root, ".kernforge", "reviews", "latest.json")); err != nil {
		t.Fatalf("latest review json missing: %v", err)
	}
}

func TestReviewHarnessUsesVerificationHistoryForFreshEvidence(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runTestGit(t, root, "add", "main.go")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() { println(\"ok\") }\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	history := &VerificationHistoryStore{
		Path:       filepath.Join(root, ".kernforge", "verification-history.json"),
		MaxEntries: 10,
	}
	report := VerificationReport{
		Workspace:    root,
		ChangedPaths: []string{"main.go"},
		Steps: []VerificationStep{{
			Label:   "go test ./...",
			Command: "go test ./...",
			Status:  VerificationPassed,
			Output:  "ok example 0.001s",
		}},
	}
	if err := history.Append("session-test", root, report); err != nil {
		t.Fatalf("append verification history: %v", err)
	}

	rt := &runtimeState{
		cfg:           DefaultConfig(root),
		workspace:     Workspace{BaseRoot: root, Root: root},
		session:       NewSession(root, "", "", "", "default"),
		verifyHistory: history,
	}
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:        "explicit_command",
		Target:         "change",
		Request:        "review current change",
		NoModel:        true,
		IncludeGitDiff: true,
	})
	if err != nil {
		t.Fatalf("run review: %v", err)
	}
	if !strings.Contains(run.Evidence.VerificationSummary, "passed=1") {
		t.Fatalf("expected review evidence to include verification history, got %#v", run.Evidence)
	}
	for _, finding := range run.Findings {
		if strings.Contains(finding.Title, "Changed files have no latest verification evidence") {
			t.Fatalf("fresh verification history should satisfy review evidence, got %#v", finding)
		}
	}
}

func TestSelectionReviewEvidenceUsesFullRangeNotPreviewTruncation(t *testing.T) {
	root := t.TempDir()
	var lines []string
	for i := 1; i <= 120; i++ {
		lines = append(lines, fmt.Sprintf("// line %03d %s", i, strings.Repeat("x", 48)))
	}
	lines[119] = "// line 120 tail sentinel review must keep this line"
	path := filepath.Join(root, "main.cpp")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	session := NewSession(root, "", "", "", "default")
	session.AddSelection(ViewerSelection{
		FilePath:  path,
		StartLine: 1,
		EndLine:   120,
	})
	rt := &runtimeState{
		cfg:       DefaultConfig(root),
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   session,
	}
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:         naturalReviewTrigger,
		Target:          reviewTargetSelection,
		Request:         "@main.cpp:1-120 리뷰해줘",
		NoModel:         true,
		IncludeGitDiff:  false,
		MaxContextChars: 60000,
	})
	if err != nil {
		t.Fatalf("run review: %v", err)
	}
	if strings.Contains(run.Evidence.Text, "... (truncated)") {
		t.Fatalf("selection review evidence should not use UI preview truncation: %q", run.Evidence.Text)
	}
	if !strings.Contains(run.Evidence.Text, "line 120 tail sentinel") {
		t.Fatalf("selection review evidence lost the selected tail line: %q", run.Evidence.Text)
	}
	for _, finding := range run.Findings {
		if strings.Contains(finding.Title, "Changed files have no latest verification evidence") {
			t.Fatalf("read-only selection review should not be treated as a changed-file verification gap: %#v", finding)
		}
	}
	for _, cmd := range run.Gate.NextCommands {
		if cmd.ID == "verify" {
			t.Fatalf("read-only selection review should not recommend /verify --full: %#v", run.Gate.NextCommands)
		}
	}
}

func TestPostChangeReviewRunsAfterEditWhenEnabled(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("int main()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runTestGit(t, root, "add", "main.cpp")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("int main()\n{\n    return 1;\n}\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}
	session := NewSession(root, "", "", "", "default")
	agent := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}
	reviewed, needsRevision, feedback, fingerprint, err := agent.maybeRunPostChangeReview(context.Background(), "implement the change", "")
	if err != nil {
		t.Fatalf("post-change review: %v", err)
	}
	if !reviewed || needsRevision || fingerprint == "" {
		t.Fatalf("expected non-blocking automatic review, reviewed=%t needs=%t fingerprint=%q feedback=%q", reviewed, needsRevision, fingerprint, feedback)
	}
	if session.LastReviewRun == nil {
		t.Fatalf("expected session last review run")
	}
	if !session.LastReviewRun.AutoTriggered || session.LastReviewRun.Trigger != "post_change" {
		t.Fatalf("expected post_change auto review, got %#v", session.LastReviewRun)
	}
	if session.LastReviewRun.Target != reviewTargetChange {
		t.Fatalf("expected change target, got %#v", session.LastReviewRun)
	}
	if !strings.Contains(feedback, "Automatic post-change review completed") {
		t.Fatalf("expected user-visible review summary, got %q", feedback)
	}
	reviewedAgain, _, _, againFingerprint, err := agent.maybeRunPostChangeReview(context.Background(), "implement the change", fingerprint)
	if err != nil {
		t.Fatalf("repeat post-change review: %v", err)
	}
	if reviewedAgain || againFingerprint != fingerprint {
		t.Fatalf("expected same fingerprint to skip repeat review, reviewed=%t fingerprint=%q", reviewedAgain, againFingerprint)
	}
}

func TestPostChangeReviewSkipsAfterFailedEditAttemptWithoutSuccessfulChange(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runTestGit(t, root, "add", "main.go")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")

	provider := &sideEffectProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "main.go",
				"content": "package main\n\n// agent edit\n",
			}),
			{Message: Message{Role: "assistant", Text: "I stopped because main.go changed outside the agent."}},
		},
		beforeReturn: []func(){
			func() {
				if err := os.WriteFile(path, []byte("package main\n\n// user edit\n"), 0o644); err != nil {
					panic(err)
				}
			},
			nil,
		},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "model"
	cfg.Review.AutoAfterChange = boolPtr(true)
	session := NewSession(root, "scripted", "model", "", "default")
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-tx-prior",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:       "entry-1",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "main.go",
				Operation: "update",
				After: HarnessFileFingerprint{
					Path:   "main.go",
					Kind:   "file",
					Exists: true,
				},
			}},
		}},
	}}
	ws := Workspace{BaseRoot: root, Root: root}
	var progress []string
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "changed outside") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	for _, item := range progress {
		if strings.Contains(item, "automatic post-change review") || strings.Contains(item, "Automatic post-change review") {
			t.Fatalf("post-change review should not run after a failed edit attempt with no successful current change, progress=%#v", progress)
		}
	}
	if session.LastReviewRun != nil && strings.EqualFold(session.LastReviewRun.Trigger, "post_change") {
		t.Fatalf("post-change review should not be recorded after failed edit attempt, got %#v", session.LastReviewRun)
	}
}

func TestAutomaticReviewFeedbackKeepsModelOnInlineFindings(t *testing.T) {
	run := ReviewRun{
		ID:            "review-blocker",
		MachineStatus: "blocked",
		Result: ReviewResult{
			Verdict: "needs_revision",
			Summary: "review found blockers",
		},
		Gate: GateDecision{
			Verdict: reviewVerdictNeedsRevision,
		},
		ArtifactRefs: []string{
			".kernforge/reviews/review-blocker/review.md",
			".kernforge/reviews/review-blocker/review.json",
		},
		Findings: []ReviewFinding{{
			ID:                 "RF-001",
			Severity:           reviewSeverityHigh,
			Category:           "correctness",
			Title:              "Bad bounds check",
			Path:               "main.cpp",
			Evidence:           "index is used before validation",
			RequiredFix:        "validate index before use",
			TestRecommendation: "add an out-of-range index test",
			BlocksGate:         true,
		}},
	}
	post := formatPostChangeReviewFeedback(run, true)
	preWrite := formatPreWriteReviewFeedback(run)
	for _, feedback := range []string{post, preWrite} {
		for _, banned := range []string{".kernforge", "review.md", "review.json", "\nReport:", "Review ID", "Next Commands"} {
			if strings.Contains(feedback, banned) {
				t.Fatalf("automatic review feedback should not leak artifact/report text %q in %q", banned, feedback)
			}
		}
		for _, required := range []string{"Bad bounds check", "validate index before use", "add an out-of-range index test", "Do not read review artifact files"} {
			if !strings.Contains(feedback, required) {
				t.Fatalf("expected automatic review feedback to contain %q, got %q", required, feedback)
			}
		}
	}
}

func TestDistinctReviewModelProgressIsExplicit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	mainReviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: approved",
				"summary: main first pass completed",
				"findings:",
			}, "\n")},
		}},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: approved_with_warnings",
				"summary: review found a minor issue",
				"findings:",
				"- severity: medium",
				"  title: Missing focused test",
				"  category: test_gap",
				"  evidence: no test evidence was supplied",
				"  required_fix: run a focused test",
			}, "\n")},
		}},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(false)
	session := NewSession(root, "scripted", "main-model", "", "default")
	var progress []string
	agent := &Agent{
		Config:         cfg,
		Client:         mainReviewer,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
	}
	rt := agent.reviewHarnessRuntime(root)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             "explicit_command",
		Target:              reviewTargetChange,
		Request:             "review main.go",
		Paths:               []string{path},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if run.Result.WarningCount == 0 {
		t.Fatalf("expected warning finding from reviewer, got %#v", run.Result)
	}
	if len(reviewer.requests) != 1 {
		t.Fatalf("expected one cross reviewer request, got %d", len(reviewer.requests))
	}
	crossPrompt := reviewer.requests[0].Messages[0].Text
	if !strings.Contains(crossPrompt, "Primary model raw draft") || !strings.Contains(crossPrompt, "main first pass completed") {
		t.Fatalf("expected cross reviewer prompt to include primary draft, got %q", crossPrompt)
	}
	for _, needle := range []string{
		"Review phase 1/2: main first-pass review",
		"Main model is preparing the first-pass review from the collected local evidence.",
		"Main model first-pass review request: scripted / main-model.",
		"Model-call budget: main first-pass review",
		"Main model first-pass review result: completed",
		"Main model first-pass review completed. Sending its draft and the same evidence to the review model.",
		"Review phase 2/2: review model cross-check",
		"Review model is cross-checking the main model draft before the final gate is decided.",
		"Review model cross-check request: cross -> scripted / reviewer-model (main: scripted / main-model).",
		"Model-call budget: review model cross-check",
		"Review model cross-check result: cross completed",
		"Review model returned its cross-check. Kernforge is merging both reviews for the final gate.",
		"Review gate result:",
	} {
		if indexStringContaining(progress, needle) < 0 {
			t.Fatalf("expected progress to contain %q, got %#v", needle, progress)
		}
	}
}

func TestFocusedLineRangeReviewUsesFastContextBudget(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "PathConverter.cpp")
	var source strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&source, "int line_%04d = %d; // UNIQUE_REVIEW_CONTEXT_%04d\n", i, i, i)
	}
	if err := os.WriteFile(path, []byte(source.String()), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg := DefaultConfig(root)
	cfg.AutoLocale = boolPtr(false)
	var progress []string
	rt := &runtimeState{
		cfg:       cfg,
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "scripted", "model", "", "default"),
		agent: &Agent{
			Config: cfg,
			EmitProgress: func(message string) {
				progress = append(progress, message)
			},
		},
	}
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             reviewBeforeFixTrigger,
		Target:              reviewTargetAuto,
		Request:             "@PathConverter.cpp:132-221 review and fix bugs",
		Paths:               []string{path},
		IncludeFileContents: true,
		NoModel:             true,
		MaxContextChars:     reviewDefaultMaxContextChars,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(run.Evidence.Text) > reviewFocusedMaxContextChars {
		t.Fatalf("focused evidence should be capped, got %d > %d", len(run.Evidence.Text), reviewFocusedMaxContextChars)
	}
	if indexStringContaining(progress, "max_context=20000") < 0 {
		t.Fatalf("expected focused max_context progress, got %#v", progress)
	}
}

func TestPreWriteReviewUsesDiffFirstContextBudget(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	rt := &runtimeState{
		cfg:       cfg,
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "scripted", "model", "", "default"),
		agent:     &Agent{Config: cfg},
	}
	diff := "diff --git a/driver.cpp b/driver.cpp\n--- a/driver.cpp\n+++ b/driver.cpp\n"
	diff += strings.Repeat("+int very_long_changed_line = 42; // DIFF_FIRST_CONTEXT\n", 2000)
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:         "pre_write",
		Target:          reviewTargetChange,
		Request:         "review proposed edit",
		Paths:           []string{"driver.cpp"},
		ProvidedDiff:    diff,
		IncludeGitDiff:  false,
		NoModel:         true,
		MaxContextChars: reviewDefaultMaxContextChars,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(run.Evidence.Text) > reviewPreWriteMaxContextChars {
		t.Fatalf("pre-write evidence should be capped, got %d > %d", len(run.Evidence.Text), reviewPreWriteMaxContextChars)
	}
	if len(run.Evidence.Sources) == 0 || run.Evidence.Sources[0] != "provided_diff" {
		t.Fatalf("pre-write evidence should remain diff-first, sources=%#v", run.Evidence.Sources)
	}
}

func TestFocusedCrossReviewerUsesSoftTimeoutBudget(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex-subscription"
	cfg.Review.RoleModels = map[string]ReviewModelConfig{
		"primary_reviewer": {
			Provider: "deepseek",
			Model:    "deepseek-v4-pro",
		},
	}
	run := ReviewRun{
		Trigger:   reviewBeforeFixTrigger,
		Target:    reviewTargetChange,
		Objective: "@PathConverter.cpp:132-221 review and fix bugs",
		RequestAnalysis: ReviewRequestAnalysis{
			ScopeDiscovery: ReviewScopeDiscovery{
				ScopeWidth:     "focused",
				CandidateFiles: []string{"PathConverter.cpp"},
			},
		},
	}
	if got := reviewModelSoftTimeoutForRun(cfg, run, ReviewReviewerRun{Role: "cross_reviewer", Kind: "cross"}); got != reviewFocusedCrossSoftTimeout {
		t.Fatalf("expected focused cross soft timeout, got %s", got)
	}
	if got := reviewModelSoftTimeoutForRun(cfg, run, ReviewReviewerRun{Role: "primary_reviewer", Kind: "main"}); got != 0 {
		t.Fatalf("main review should not use a soft timeout, got %s", got)
	}
}

func TestReviewModelLongWaitProgressExplainsCrossHandoff(t *testing.T) {
	cfg := Config{AutoLocale: boolPtr(false)}
	message := formatReviewModelLongWaitProgress(cfg, ReviewReviewerRun{
		Role: "cross_reviewer",
		Kind: "cross",
	}, 2*time.Minute+5*time.Second)

	for _, want := range []string{
		"Review model cross-check is still running",
		"merge it with the main model review",
		"final gate",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected %q in long-wait progress, got %q", want, message)
		}
	}
}

func TestReviewModelRetryProgressIncludesAttemptBudget(t *testing.T) {
	cfg := Config{AutoLocale: boolPtr(false)}
	var progress []string
	rt := &runtimeState{
		cfg: cfg,
		agent: &Agent{
			Config: cfg,
			EmitProgress: func(message string) {
				progress = append(progress, message)
			},
		},
	}

	emitReviewModelRetryProgress(rt, "cross_reviewer", "DeepSeek / deepseek-v4-pro", 2, 3)

	if len(progress) != 1 {
		t.Fatalf("expected one progress line, got %#v", progress)
	}
	for _, want := range []string{"strict review (2/3)", "cross", "DeepSeek / deepseek-v4-pro"} {
		if !strings.Contains(progress[0], want) {
			t.Fatalf("expected %q in retry progress, got %q", want, progress[0])
		}
	}
}

func TestReviewMCPRunUsesMainFirstAndResponseIncludesReviewerRuns(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	mainReviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: approved",
				"summary: main mcp review completed",
				"findings:",
			}, "\n")},
		}},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: approved_with_warnings",
				"summary: cross reviewer found a test gap",
				"findings:",
				"- severity: medium",
				"  title: Missing MCP response contract test",
				"  category: test_gap",
				"  evidence: MCP clients need reviewer run status in the response",
				"  required_fix: include reviewer_runs in the MCP response",
			}, "\n")},
		}},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(false)
	agent := &Agent{
		Config:         cfg,
		Client:         mainReviewer,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        NewSession(root, "scripted", "main-model", "", "default"),
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
	}
	rt := agent.reviewHarnessRuntime(root)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             "explicit_mcp",
		Target:              reviewTargetChange,
		Request:             "review main.go through MCP",
		Paths:               []string{path},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(mainReviewer.requests) != 1 {
		t.Fatalf("expected one main reviewer request, got %d", len(mainReviewer.requests))
	}
	if len(reviewer.requests) != 1 {
		t.Fatalf("expected one cross reviewer request, got %d", len(reviewer.requests))
	}
	if len(run.ReviewerRuns) != 2 {
		t.Fatalf("expected main and cross reviewer runs, got %#v", run.ReviewerRuns)
	}
	if run.ReviewerRuns[0].Kind != "main" || run.ReviewerRuns[0].Role != "primary_reviewer" {
		t.Fatalf("expected primary main run first, got %#v", run.ReviewerRuns[0])
	}
	if run.ReviewerRuns[1].Kind != "cross" || run.ReviewerRuns[1].Role != "cross_reviewer" {
		t.Fatalf("expected cross reviewer run second, got %#v", run.ReviewerRuns[1])
	}
	if !stringSliceContainsCI(run.ModelPlan.OptionalRoles, "cross_reviewer") {
		t.Fatalf("expected MCP cross reviewer to be optional in model plan, got %#v", run.ModelPlan)
	}
	rendered := renderReviewMCPResponse(run, 40000)
	for _, needle := range []string{
		`"model_plan"`,
		`"reviewer_runs"`,
		`"role": "primary_reviewer"`,
		`"kind": "main"`,
		`"role": "cross_reviewer"`,
		`"kind": "cross"`,
		`"Missing MCP response contract test"`,
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected MCP review response to contain %q, got %s", needle, rendered)
		}
	}
}

func TestMainFirstCrossReviewerSatisfiesDedicatedSecurityRole(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "driver.cpp")
	if err := os.WriteFile(path, []byte("void DriverEntry() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	mainReviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: approved",
				"summary: main security pass completed",
				"findings:",
			}, "\n")},
		}},
	}
	securityReviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: approved",
				"summary: security cross pass completed",
				"findings:",
			}, "\n")},
		}},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(false)
	agent := &Agent{
		Config:         cfg,
		Client:         mainReviewer,
		ReviewerClient: securityReviewer,
		ReviewerModel:  "security-reviewer-model",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        NewSession(root, "scripted", "main-model", "", "default"),
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
	}
	rt := agent.reviewHarnessRuntime(root)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             "explicit_command",
		Target:              reviewTargetChange,
		Mode:                reviewModeSecurityHardening,
		Request:             "review kernel driver security boundary",
		Paths:               []string{path},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(run.ReviewerRuns) != 2 {
		t.Fatalf("expected main and security cross reviewer runs, got %#v", run.ReviewerRuns)
	}
	if run.ReviewerRuns[1].Role != "security_reviewer" || run.ReviewerRuns[1].Kind != "cross" {
		t.Fatalf("expected second pass to satisfy security reviewer role, got %#v", run.ReviewerRuns[1])
	}
	if stringSliceContainsCI(run.ModelPlan.MissingRoles, "security_reviewer") ||
		stringSliceContainsCI(run.ModelPlan.DegradedRoles, "security_reviewer") {
		t.Fatalf("satisfied security reviewer role should not remain missing or degraded: %#v", run.ModelPlan)
	}
	for _, command := range run.Gate.NextCommands {
		if command.ID == "set-security-model" {
			t.Fatalf("satisfied security reviewer should not recommend setup command: %#v", run.Gate.NextCommands)
		}
	}
}

func TestPreFixReviewModelFailureDegradesButKeepsMainFirstRepairGate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "PathConverter.cpp")
	if err := os.WriteFile(path, []byte("bool Fix(){return true;}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	reviewer := &failingReviewProviderClient{err: fmt.Errorf("Claude Code CLI command failed: exit status 1")}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	agent := &Agent{
		Config: cfg,
		Client: &scriptedProviderClient{
			replies: []ChatResponse{{
				Message: Message{Role: "assistant", Text: strings.Join([]string{
					"REVIEW_RESULT",
					"verdict: needs_revision",
					"summary: main model found a concrete pre-fix bug",
					"findings:",
					"- severity: high",
					"  title: Missing guard",
					"  category: correctness",
					"  path: PathConverter.cpp",
					"  evidence: Fix returns true without checking input",
					"  impact: invalid inputs can be accepted",
					"  required_fix: validate the input before returning success",
					"  test_recommendation: add an invalid input test",
				}, "\n")}},
			},
		},
		ReviewerClient: reviewer,
		ReviewerModel:  "claude-sonnet-4-7",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        NewSession(root, "scripted", "main-model", "", "default"),
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
	}
	rt := agent.reviewHarnessRuntime(root)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             reviewBeforeFixTrigger,
		Target:              reviewTargetSelection,
		Mode:                reviewModeLiveFix,
		Request:             "@PathConverter.cpp:1-1 검토하고 버그를 수정해",
		Paths:               []string{path},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(reviewer.requests) == 0 {
		t.Fatalf("expected reviewer request")
	}
	if run.Gate.Verdict != reviewVerdictNeedsRevision {
		t.Fatalf("pre-fix reviewer failure should not hide main review findings, got %#v findings=%#v", run.Gate, run.Findings)
	}
	if !run.Result.Degraded {
		t.Fatalf("expected degraded result after reviewer failure")
	}
	if reviewRunHasRequiredReviewerFailure(run) {
		t.Fatalf("pre-fix cross reviewer failure should not be a required reviewer failure, got %#v", run.Findings)
	}
	if !reviewRunHasFindingTitle(run, "Missing guard") {
		t.Fatalf("expected main review finding to drive repair, got %#v", run.Findings)
	}
}

func TestPreFixWeakReviewModelQualityDegradesWithoutBlockingMainFirstRepair(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "PathConverter.cpp")
	if err := os.WriteFile(path, []byte("bool Fix(){return true;}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: "I cannot produce a structured review from the supplied context."},
		}},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	agent := &Agent{
		Config: cfg,
		Client: &scriptedProviderClient{
			replies: []ChatResponse{{
				Message: Message{Role: "assistant", Text: strings.Join([]string{
					"REVIEW_RESULT",
					"verdict: approved",
					"summary: main model found no concrete blocker",
					"findings:",
				}, "\n")}},
			},
		},
		ReviewerClient: reviewer,
		ReviewerModel:  "deepseek-v4-pro",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        NewSession(root, "scripted", "main-model", "", "default"),
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
	}
	rt := agent.reviewHarnessRuntime(root)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             reviewBeforeFixTrigger,
		Target:              reviewTargetSelection,
		Mode:                reviewModeLiveFix,
		Request:             "@PathConverter.cpp:1-1 검토하고 버그를 수정해",
		Paths:               []string{path},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(reviewer.requests) == 0 {
		t.Fatalf("expected reviewer request")
	}
	if run.Result.ModelQuality != reviewModelQualityWeak {
		t.Fatalf("expected weak reviewer quality, got %q", run.Result.ModelQuality)
	}
	if run.Gate.Verdict == reviewVerdictInsufficientEvidence {
		t.Fatalf("weak cross reviewer should not block pre-fix as insufficient evidence, got %#v findings=%#v", run.Gate, run.Findings)
	}
	if reviewRunHasRequiredReviewerFailure(run) {
		t.Fatalf("weak cross reviewer should not be a required reviewer failure for pre-fix, got %#v", run.Findings)
	}
}

func TestPreFixEmptyReviewModelResponseDegradesWithoutBlockingMainFirstRepair(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "PathConverter.cpp")
	if err := os.WriteFile(path, []byte("bool Fix(){return true;}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: "   "},
		}},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	agent := &Agent{
		Config: cfg,
		Client: &scriptedProviderClient{
			replies: []ChatResponse{{
				Message: Message{Role: "assistant", Text: strings.Join([]string{
					"REVIEW_RESULT",
					"verdict: approved",
					"summary: main model completed the first-pass review",
					"findings:",
				}, "\n")}},
			},
		},
		ReviewerClient: reviewer,
		ReviewerModel:  "deepseek-v4-pro",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        NewSession(root, "scripted", "main-model", "", "default"),
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
	}
	rt := agent.reviewHarnessRuntime(root)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             reviewBeforeFixTrigger,
		Target:              reviewTargetSelection,
		Mode:                reviewModeLiveFix,
		Request:             "@PathConverter.cpp:1-1 검토하고 버그를 수정해",
		Paths:               []string{path},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(run.ReviewerRuns) != 2 {
		t.Fatalf("expected main and cross reviewer runs, got %#v", run.ReviewerRuns)
	}
	reviewerRun := run.ReviewerRuns[1]
	if reviewerRun.Status != "failed" || reviewerRun.ModelQuality != reviewModelQualityFailed {
		t.Fatalf("empty reviewer response should be a failed run, got %#v", reviewerRun)
	}
	if !strings.Contains(reviewerRun.Error, "empty response") {
		t.Fatalf("expected empty response error, got %#v", reviewerRun)
	}
	if run.Gate.Verdict == reviewVerdictInsufficientEvidence {
		t.Fatalf("empty cross reviewer should not block pre-fix as insufficient evidence, got %#v findings=%#v", run.Gate, run.Findings)
	}
	if reviewRunHasRequiredReviewerFailure(run) {
		t.Fatalf("empty cross reviewer should not be a required reviewer failure for pre-fix, got %#v", run.Findings)
	}
}

func TestPreWriteReviewModelFailureBlocksEditGate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "PathConverter.cpp")
	if err := os.WriteFile(path, []byte("bool Fix(){return true;}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	reviewer := &failingReviewProviderClient{err: fmt.Errorf("Claude Code CLI command failed: exit status 1")}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	agent := &Agent{
		Config:         cfg,
		Client:         &scriptedProviderClient{},
		ReviewerClient: reviewer,
		ReviewerModel:  "claude-sonnet-4-7",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        NewSession(root, "scripted", "main-model", "", "default"),
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
	}
	rt := agent.reviewHarnessRuntime(root)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:      "pre_write",
		Target:       reviewTargetChange,
		Mode:         reviewModeLiveFix,
		Request:      "automatic pre-write review",
		Paths:        []string{path},
		ProvidedDiff: "- break;\n+ continue;\n",
		EditProposals: []EditProposal{{
			File:            "PathConverter.cpp",
			Operation:       "apply_patch",
			ExpectedPreview: "- break;\n+ continue;\n",
		}},
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(reviewer.requests) == 0 {
		t.Fatalf("expected reviewer request")
	}
	if run.Gate.Verdict != reviewVerdictInsufficientEvidence {
		t.Fatalf("pre-write reviewer failure should block as insufficient evidence, got %#v findings=%#v", run.Gate, run.Findings)
	}
	if !reviewRunHasFindingTitle(run, "Required reviewer model failed or returned weak output") {
		t.Fatalf("expected required reviewer failure finding, got %#v", run.Findings)
	}
}

func TestPreWriteWeakReviewModelQualityBlocksEditGate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "PathConverter.cpp")
	if err := os.WriteFile(path, []byte("bool Fix(){return true;}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: "Unable to provide structured findings."},
		}},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	agent := &Agent{
		Config:         cfg,
		Client:         &scriptedProviderClient{},
		ReviewerClient: reviewer,
		ReviewerModel:  "deepseek-v4-pro",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        NewSession(root, "scripted", "main-model", "", "default"),
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
	}
	rt := agent.reviewHarnessRuntime(root)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:      "pre_write",
		Target:       reviewTargetChange,
		Mode:         reviewModeLiveFix,
		Request:      "automatic pre-write review",
		Paths:        []string{path},
		ProvidedDiff: "- break;\n+ continue;\n",
		EditProposals: []EditProposal{{
			File:            "PathConverter.cpp",
			Operation:       "apply_patch",
			ExpectedPreview: "- break;\n+ continue;\n",
		}},
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(reviewer.requests) == 0 {
		t.Fatalf("expected reviewer request")
	}
	if run.Result.ModelQuality != reviewModelQualityWeak {
		t.Fatalf("expected weak reviewer quality, got %q", run.Result.ModelQuality)
	}
	if run.Gate.Verdict != reviewVerdictInsufficientEvidence {
		t.Fatalf("weak pre-write reviewer should block as insufficient evidence, got %#v findings=%#v", run.Gate, run.Findings)
	}
	if !reviewRunHasFindingTitle(run, "Required reviewer model failed or returned weak output") {
		t.Fatalf("expected required reviewer quality finding, got %#v", run.Findings)
	}
}

func TestSameModelReviewProgressShowsScopeEvidenceAndRequest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Source", "FocusedServerRuntime.cpp")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("class FocusedServerRuntime\n{\npublic:\n    void Tick();\n};\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	client := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: approved",
				"summary: no blocking issue found",
			}, "\n")},
		}},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(false)
	session := NewSession(root, "scripted", "main-model", "", "default")
	var progress []string
	agent := &Agent{
		Config:    cfg,
		Client:    client,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
	}
	rt := agent.reviewHarnessRuntime(root)

	if _, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger: "explicit_command",
		Target:  reviewTargetAuto,
		Request: "FocusedServerRuntime 코드를 분석해서 서버 성능이나 히칭에 영향을 줄 수 있는 부분을 검토해줘",
	}); err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}

	for _, needle := range []string{
		"Review scope discovery:",
		"Review evidence prepared:",
		"max_context=180000",
		"Main model first-pass review request: scripted / main-model.",
		"Main model first-pass review result: completed",
	} {
		if indexStringContaining(progress, needle) < 0 {
			t.Fatalf("expected progress to contain %q, got %#v", needle, progress)
		}
	}
}

func TestReviewModelRetriesOmittedFindingOutput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() { println(\"ok\") }\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: needs_revision",
				"summary: 버퍼 검증 문제",
				"findings:",
				"- severity: high",
				"  category: correctness",
				"  title: 입력 검증이 누락됨...",
				"  evidence: main.go의 호출 흐름 일부가 생략되었습니다",
				"  impact: 잘못된 입력에서 크래시가 날 수 있습니다",
				"  required_fix: 검증을 추가하세요",
			}, "\n")}},
			{Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: needs_revision",
				"summary: 구체적인 수정 필요 finding이 있습니다",
				"findings:",
				"- severity: high",
				"  category: correctness",
				"  title: 리뷰 범위가 실제 입력 경계를 포함하지 않음",
				"  path: main.go",
				"  symbol: main",
				"  evidence: 제공된 파일은 외부 입력 없이 동작하지만 리뷰 요청은 버그 탐지를 요구하므로 실제 입력 검증 대상이 증거에 포함되지 않았습니다.",
				"  impact: 증거가 좁으면 잘못된 수정을 적용할 수 있습니다.",
				"  required_fix: 버그가 의심되는 함수의 실제 입력 경계 코드까지 선택해서 다시 리뷰하십시오.",
				"  test_recommendation: 문제 함수의 정상 입력과 비정상 입력을 포함하는 focused test를 추가하십시오.",
			}, "\n")}},
		},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(true)
	session := NewSession(root, "scripted", "main-model", "", "default")
	var progress []string
	agent := &Agent{
		Config:         cfg,
		Client:         &scriptedProviderClient{replies: []ChatResponse{approvedReviewResponse("main first-pass review found no actionable issues")}},
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
	}
	rt := agent.reviewHarnessRuntime(root)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             naturalReviewTrigger,
		Target:              reviewTargetChange,
		Request:             "@main.go 리뷰해줘",
		Paths:               []string{path},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(reviewer.requests) != 2 {
		t.Fatalf("expected strict retry request, got %d requests", len(reviewer.requests))
	}
	var modelFindings []ReviewFinding
	for _, finding := range run.Findings {
		if finding.Source == "model" && finding.ReviewerRole == "cross_reviewer" {
			modelFindings = append(modelFindings, finding)
		}
	}
	if len(modelFindings) != 1 {
		t.Fatalf("expected retry model finding only, got %#v", run.Findings)
	}
	if reviewFindingIsOmittedOutputPlaceholder(modelFindings[0]) {
		t.Fatalf("omitted placeholder should be replaced by retry finding: %#v", modelFindings[0])
	}
	if run.Result.ModelQuality != reviewModelQualityUsable {
		t.Fatalf("expected usable retry quality, got %#v", run.Result)
	}
	if !strings.Contains(reviewer.requests[1].Messages[0].Text, "이전 리뷰 출력은") {
		t.Fatalf("retry prompt should explain the rejected output, got %q", reviewer.requests[1].Messages[0].Text)
	}
	if indexStringContaining(progress, "엄격 리뷰로 재시도합니다") < 0 {
		t.Fatalf("expected retry progress message, got %#v", progress)
	}
}

func TestReviewModelDoesNotRetryUsableFindingsForRawOmissionMarker(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() { println(\"ok\") }\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: needs_revision",
				"summary: details omitted from the prose summary, but the structured finding is complete",
				"findings:",
				"- severity: high",
				"  category: correctness",
				"  title: Input validation is missing",
				"  path: main.go",
				"  symbol: main",
				"  evidence: main accepts an unchecked value before using it.",
				"  impact: Invalid input can trigger incorrect behavior.",
				"  required_fix: Validate the value before use.",
				"  test_recommendation: Add a focused invalid-input test.",
			}, "\n")},
		}},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(false)
	session := NewSession(root, "scripted", "main-model", "", "default")
	var progress []string
	agent := &Agent{
		Config:         cfg,
		Client:         &scriptedProviderClient{replies: []ChatResponse{approvedReviewResponse("main first-pass review found no actionable issues")}},
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
	}
	rt := agent.reviewHarnessRuntime(root)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             reviewBeforeFixTrigger,
		Target:              reviewTargetChange,
		Mode:                reviewModeLiveFix,
		Request:             "@main.go 검토하고 버그를 수정해",
		Paths:               []string{path},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(reviewer.requests) != 1 {
		t.Fatalf("usable structured findings should not strict-retry for prose omission markers, got %d requests result=%#v findings=%#v progress=%#v", len(reviewer.requests), run.Result, run.Findings, progress)
	}
	for _, req := range reviewer.requests {
		for _, msg := range req.Messages {
			if strings.Contains(msg.Text, "previous review output was rejected") ||
				strings.Contains(msg.Text, "이전 리뷰 출력은 구조화된 필드") {
				t.Fatalf("did not expect omission retry prompt, got %q", msg.Text)
			}
		}
	}
	if run.Result.ModelQuality != reviewModelQualityUsable {
		t.Fatalf("expected usable quality, got %#v", run.Result)
	}
	if indexStringContaining(progress, "strict review") >= 0 || indexStringContaining(progress, "엄격 리뷰") >= 0 {
		t.Fatalf("did not expect strict retry progress, got %#v", progress)
	}
	var modelFindings []ReviewFinding
	for _, finding := range run.Findings {
		if finding.Source == "model" && finding.ReviewerRole == "cross_reviewer" {
			modelFindings = append(modelFindings, finding)
		}
	}
	if len(modelFindings) != 1 || modelFindings[0].Title != "Input validation is missing" {
		t.Fatalf("expected one complete model finding, got %#v", modelFindings)
	}
}

func TestReviewModelRetriesCutOffFindingOutput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() { println(\"ok\") }\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: needs_revision",
				"summary: 워치독 처리 문제",
				"findings:",
				"- severity: high",
				"  category: stability",
				"  title: Watchdog failure path exits abruptly",
				"  path: main.go",
				"  symbol: main",
				"  evidence: watchdog timeout path invokes an abrupt shutdown helper",
				"  impact: graceful shutdown and final logging can be skipped",
				"  required_fix: route timeout handling through the normal shutdown path",
				"  test_recommendation: 워치독 타임",
			}, "\n")}},
			{Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: needs_revision",
				"summary: 완성된 finding입니다",
				"findings:",
				"- severity: high",
				"  category: stability",
				"  title: Watchdog failure path exits abruptly",
				"  path: main.go",
				"  symbol: main",
				"  evidence: watchdog timeout path invokes an abrupt shutdown helper",
				"  impact: graceful shutdown and final logging can be skipped",
				"  required_fix: route timeout handling through the normal shutdown path",
				"  test_recommendation: watchdog timeout path should request normal shutdown and preserve final logs.",
			}, "\n")}},
		},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(true)
	session := NewSession(root, "scripted", "main-model", "", "default")
	var progress []string
	agent := &Agent{
		Config:         cfg,
		Client:         &scriptedProviderClient{replies: []ChatResponse{approvedReviewResponse("main first-pass review found no actionable issues")}},
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
	}
	rt := agent.reviewHarnessRuntime(root)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             naturalReviewTrigger,
		Target:              reviewTargetChange,
		Request:             "@main.go 리뷰해줘",
		Paths:               []string{path},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(reviewer.requests) != 2 {
		t.Fatalf("expected strict retry request for cut-off output, got %d requests", len(reviewer.requests))
	}
	if run.Result.ModelQuality != reviewModelQualityUsable {
		t.Fatalf("expected usable retry quality, got %#v", run.Result)
	}
	var crossFindings []ReviewFinding
	for _, finding := range run.Findings {
		if finding.Source == "model" && finding.ReviewerRole == "cross_reviewer" {
			crossFindings = append(crossFindings, finding)
		}
	}
	if len(crossFindings) != 1 || !strings.Contains(crossFindings[0].TestRecommendation, "preserve final logs") {
		t.Fatalf("expected retry cross-reviewer finding to replace cut-off test recommendation, got %#v", run.Findings)
	}
	if indexStringContaining(progress, "생략/잘림 징후") < 0 {
		t.Fatalf("expected cut-off retry progress message, got %#v", progress)
	}
}

func TestReviewModelOmissionRetryFailureMarksRunDegraded(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() { println(\"ok\") }\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	reviewer := &reviewRetryFailureProviderClient{
		first: ChatResponse{Message: Message{Role: "assistant", Text: strings.Join([]string{
			"REVIEW_RESULT",
			"verdict: needs_revision",
			"summary: omitted review output",
			"findings:",
			"- severity: high",
			"  category: correctness",
			"  title: incomplete finding...",
			"  evidence: key path omitted...",
			"  impact: missing middle",
			"  required_fix: rerun with full context",
		}, "\n")}},
		err: fmt.Errorf("synthetic review retry failure"),
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(false)
	session := NewSession(root, "scripted", "main-model", "", "default")
	agent := &Agent{
		Config:         cfg,
		Client:         &scriptedProviderClient{},
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
		EmitProgress:   func(string) {},
	}
	rt := agent.reviewHarnessRuntime(root)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             naturalReviewTrigger,
		Target:              reviewTargetChange,
		Request:             "@main.go 리뷰해줘",
		Paths:               []string{path},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(reviewer.requests) != 2 {
		t.Fatalf("expected initial review and strict retry request, got %d", len(reviewer.requests))
	}
	if !run.Result.Degraded || !strings.Contains(run.Result.DegradedReason, "omission retry failed") {
		t.Fatalf("expected degraded result from retry failure, got %#v", run.Result)
	}
	foundRetryFailure := false
	for _, reviewerRun := range run.ReviewerRuns {
		if strings.Contains(reviewerRun.Error, "synthetic review retry failure") {
			foundRetryFailure = true
			break
		}
	}
	if !foundRetryFailure {
		t.Fatalf("expected reviewer run to record retry failure, got %#v", run.ReviewerRuns)
	}
}

func TestAgentRunsPreWriteReviewBeforePreviewAndWrite(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	path := filepath.Join(root, "main.go")
	before := "package main\n\nfunc main() {}\n"
	after := "package main\n\nfunc main() { println(\"ok\") }\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runTestGit(t, root, "add", "main.go")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": after}),
			{Message: Message{Role: "assistant", Text: "main.go updated. Review gate is stale because verification was not run."}},
			{Message: Message{Role: "assistant", Text: "APPROVED\nThe final answer matches the edit."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var progress []string
	var finalReviewSummaries []string
	agent := &Agent{
		Config:  DefaultConfig(root),
		Client:  provider,
		Session: session,
		Store:   store,
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
		EmitAssistantPersistent: func(message string) {
			finalReviewSummaries = append(finalReviewSummaries, message)
		},
	}
	var events []string
	ws.ReviewEdit = func(ctx context.Context, preview EditPreview) error {
		events = append(events, "review")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read before pre-write review: %v", err)
		}
		if string(data) != before {
			t.Fatalf("expected file to remain unchanged during pre-write review, got %q", string(data))
		}
		return agent.reviewProposedEdit(ctx, preview)
	}
	ws.PreviewEdit = func(preview EditPreview) (bool, error) {
		events = append(events, "preview")
		if session.LastReviewRun == nil || session.LastReviewRun.Trigger != "pre_write" {
			t.Fatalf("expected pre-write review before diff preview, got %#v", session.LastReviewRun)
		}
		if len(session.LastReviewRun.EditProposals) != 1 {
			t.Fatalf("expected pre-write review to record edit proposal, got %#v", session.LastReviewRun.EditProposals)
		}
		proposal := session.LastReviewRun.EditProposals[0]
		if proposal.File != "main.go" || proposal.Operation != "write_file" || proposal.PreviewFingerprint == "" {
			t.Fatalf("unexpected edit proposal: %#v", proposal)
		}
		if !strings.Contains(session.LastReviewRun.Evidence.Text, "Edit proposal") {
			t.Fatalf("pre-write review evidence should include edit proposal, got %q", session.LastReviewRun.Evidence.Text)
		}
		if len(finalReviewSummaries) == 0 {
			t.Fatalf("expected detailed final review summary before diff preview")
		}
		if !strings.Contains(finalReviewSummaries[len(finalReviewSummaries)-1], "Final review result:") &&
			!strings.Contains(finalReviewSummaries[len(finalReviewSummaries)-1], "최종 검토 결과:") {
			t.Fatalf("expected final review summary content before diff preview, got %#v", finalReviewSummaries)
		}
		return true, nil
	}
	agent.Workspace = ws
	agent.Tools = NewToolRegistry(NewWriteFileTool(ws))
	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "updated") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if strings.Join(events, ",") != "review,preview" {
		t.Fatalf("expected pre-write review before preview, got %#v", events)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after write: %v", err)
	}
	if string(data) != after {
		t.Fatalf("expected write after pre-write review, got %q", string(data))
	}
	reviewProgress := indexStringContaining(progress, "자동 쓰기 전 리뷰가 완료")
	if reviewProgress < 0 {
		reviewProgress = indexStringContaining(progress, "Automatic pre-write review completed")
	}
	finalReviewProgress := indexStringContaining(progress, "최종 검토 결과")
	if finalReviewProgress < 0 {
		finalReviewProgress = indexStringContaining(progress, "Final review result")
	}
	previewProgress := indexStringContaining(progress, "Edit applied. Checking follow-up steps")
	if reviewProgress < 0 {
		t.Fatalf("expected user-visible pre-write review progress, got %#v", progress)
	}
	if finalReviewProgress < 0 {
		t.Fatalf("expected final pre-write review result before diff preview, got %#v", progress)
	}
	if previewProgress < 0 {
		t.Fatalf("expected post-review follow-up progress, got %#v", progress)
	}
	if reviewProgress > previewProgress {
		t.Fatalf("expected review progress before follow-up checks, got %#v", progress)
	}
	if finalReviewProgress > previewProgress {
		t.Fatalf("expected final review result before follow-up checks, got %#v", progress)
	}
}

func TestPreWriteFinalReviewProgressMentionsDiffPreview(t *testing.T) {
	run := ReviewRun{
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{"RF-001"},
		},
		Result: ReviewResult{
			Summary: "final patch has only a verification warning",
		},
		RepairFindings: []ReviewFinding{{
			ID:                 "RF-101",
			Severity:           reviewSeverityHigh,
			Category:           "correctness",
			Path:               "Tavern/TavernWorker/PathConverter.cpp",
			Symbol:             "_InitiateVolumePath",
			Title:              "QueryDosDevice failure stops volume enumeration",
			Evidence:           "The old code used break inside the per-volume processing block.",
			Impact:             "One bad volume can stop later valid volumes from being mapped.",
			RequiredFix:        "Skip only the failed volume and continue enumerating.",
			TestRecommendation: "Exercise a failing volume followed by a valid volume.",
		}},
		Findings: []ReviewFinding{{
			ID:                 "RF-001",
			Severity:           reviewSeverityLow,
			Category:           "test_gap",
			Title:              "Build verification was not run",
			Evidence:           "No focused build output was supplied for the proposed patch.",
			RequiredFix:        "Run a focused build before merging.",
			TestRecommendation: "Run the touched package tests.",
		}},
		ArtifactRefs: []string{"C:/tmp/review.md"},
	}
	rendered := formatPreWriteFinalReviewProgress(Config{AutoLocale: boolPtr(false)}, run, true)
	for _, want := range []string{
		"Automatic pre-write review completed.",
		"Final review result: approved_with_warnings",
		"Review content:",
		"summary: final patch has only a verification warning",
		"key findings:",
		"Proceeding to diff preview.",
		"RF-001 low: Build verification was not run",
		"report: C:/tmp/review.md",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected final review progress to contain %q, got %q", want, rendered)
		}
	}
	visible := formatPreWriteFinalVisibleReviewSummary(Config{AutoLocale: boolPtr(false)}, run, true)
	for _, want := range []string{
		"Final review result:",
		"- Verdict: approved_with_warnings",
		"- Next: proceed to diff preview.",
		"Repair targets checked:",
		"- RF-101 [high/correctness]: QueryDosDevice failure stops volume enumeration",
		"Code location: Tavern/TavernWorker/PathConverter.cpp",
		"Symbol: _InitiateVolumePath",
		"Problem: The old code used break inside the per-volume processing block.",
		"Required fix: Skip only the failed volume and continue enumerating.",
		"Verification: Exercise a failing volume followed by a valid volume.",
		"Remaining review items:",
		"- RF-001 [low/test_gap]: Build verification was not run",
		"Evidence: No focused build output was supplied",
		"Fix: Run a focused build before merging.",
		"Test: Run the touched package tests.",
		"Review report: C:/tmp/review.md",
	} {
		if !strings.Contains(visible, want) {
			t.Fatalf("expected visible final review summary to contain %q, got %q", want, visible)
		}
	}
}

func TestPreWriteRunStoresRepairFindingsForFinalSummary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.cpp")
	if err := os.WriteFile(path, []byte("int main(){return 0;}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(false)
	agent := &Agent{
		Config: cfg,
		Client: &scriptedProviderClient{replies: []ChatResponse{{
			Message: approvedReviewResponse("patch fixes the repair target").Message,
		}}},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   NewSession(root, "scripted", "main-model", "", "default"),
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	rt := agent.reviewHarnessRuntime(root)
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:      "pre_write",
		Target:       reviewTargetChange,
		Request:      "fix main.cpp",
		Paths:        []string{path},
		ProvidedDiff: "- break;\n+ continue;\n",
		RepairFindings: []ReviewFinding{{
			ID:          "RF-200",
			Severity:    reviewSeverityMedium,
			Category:    "correctness",
			Path:        "main.cpp",
			Title:       "break stops enumeration",
			RequiredFix: "continue after the failed item",
		}},
		MaxContextChars: 20000,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(run.RepairFindings) != 1 || run.RepairFindings[0].ID != "RF-200" {
		t.Fatalf("expected repair finding to be stored on review run, got %#v", run.RepairFindings)
	}
	visible := formatPreWriteFinalVisibleReviewSummary(cfg, run, true)
	if !strings.Contains(visible, "Repair targets checked:") ||
		!strings.Contains(visible, "Remaining review items:") ||
		!strings.Contains(visible, "RF-200") ||
		!strings.Contains(visible, "continue after the failed item") {
		t.Fatalf("expected final visible summary to include stored repair finding, got %q", visible)
	}
}

func TestPreWriteReviewWarningProgressIncludesFindingTitles(t *testing.T) {
	run := ReviewRun{
		ID:        "review-prewrite-warn",
		Objective: "코드를 검토하고 버그를 수정해",
		Target:    reviewTargetChange,
		Mode:      reviewModeGeneralChange,
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{"RF-001", "RF-002"},
		},
		Findings: []ReviewFinding{
			{
				ID:          "RF-001",
				Severity:    reviewSeverityMedium,
				Category:    "regression",
				Title:       "Comparator change needs duplicate-key coverage",
				RequiredFix: "Add a focused comparator regression test.",
			},
			{
				ID:       "RF-002",
				Severity: reviewSeverityLow,
				Category: "test_gap",
				Title:    "Verification was not run",
			},
		},
		ArtifactRefs: []string{"C:/tmp/review.md"},
	}
	rendered := formatPreWriteReviewWarningProgress(Config{AutoLocale: boolPtr(true)}, run)
	for _, want := range []string{
		"자동 쓰기 전 리뷰가 경고와 함께 완료되었습니다.",
		"경고 2개",
		"RF-001 medium: Comparator change needs duplicate-key coverage",
		"RF-002 low: Verification was not run",
		"보고서: C:/tmp/review.md",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected warning progress to contain %q, got %q", want, rendered)
		}
	}
}

func TestPreWriteReviewBlocksActionableModelWarnings(t *testing.T) {
	run := ReviewRun{
		ID:        "review-prewrite-actionable",
		Objective: "정책 다운로드와 조회 기능을 구현해",
		Target:    reviewTargetChange,
		Mode:      reviewModeGeneralChange,
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{"RF-001", "RF-002", "RF-003"},
		},
		Findings: []ReviewFinding{
			{
				ID:           "RF-001",
				Source:       "deterministic",
				ReviewerRole: "scope_discovery",
				Severity:     reviewSeverityMedium,
				Category:     "evidence_gap",
				Title:        "Review scope needs narrowing",
			},
			{
				ID:          "RF-002",
				Source:      "model",
				Severity:    reviewSeverityMedium,
				Category:    "evidence_gap",
				Title:       "멤버 선언과 초기값 변경 증거가 없어 빌드 가능성을 확인할 수 없습니다",
				Evidence:    "The proposed patch only changes the implementation file.",
				Impact:      "The edit may not compile because declarations and state are missing.",
				RequiredFix: "Add the member declarations and getters to the header.",
			},
			{
				ID:          "RF-003",
				Source:      "model",
				Severity:    reviewSeverityMedium,
				Category:    "design",
				Title:       "조회 기능 구현 증거가 없어 요청 범위를 충족하지 못합니다",
				Evidence:    "No getter or public accessor is present in the proposed edit.",
				Impact:      "Callers cannot query the downloaded policy value.",
				RequiredFix: "Add accessors and parse/storage code together.",
			},
		},
	}
	blockingWarnings := preWriteReviewBlockingWarningFindings(run)
	if len(blockingWarnings) != 2 {
		t.Fatalf("expected two actionable model warnings to block pre-write, got %#v", blockingWarnings)
	}
	feedback := formatPreWriteReviewWarningBlockFeedback(run, blockingWarnings)
	for _, want := range []string{
		"Automatic pre-write review found actionable warnings",
		"RF-002",
		"멤버 선언과 초기값 변경 증거",
		"RF-003",
		"조회 기능 구현 증거",
		"Do not write the previous incomplete patch.",
	} {
		if !strings.Contains(feedback, want) {
			t.Fatalf("expected warning block feedback to contain %q, got %q", want, feedback)
		}
	}
}

func TestPreWriteReviewDoesNotBlockPureVerificationWarning(t *testing.T) {
	run := ReviewRun{
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{"RF-001"},
		},
		Findings: []ReviewFinding{{
			ID:                 "RF-001",
			Source:             "model",
			Severity:           reviewSeverityMedium,
			Category:           "evidence_gap",
			Title:              "Verification was not run",
			RequiredFix:        "Run /verify --full.",
			TestRecommendation: "/verify --full",
		}},
	}
	if got := preWriteReviewBlockingWarningFindings(run); len(got) != 0 {
		t.Fatalf("pure verification warning should remain non-blocking for pre-write, got %#v", got)
	}
}

func TestPreWriteReviewDoesNotBlockBuildVerificationWarningWithWrongCategory(t *testing.T) {
	run := ReviewRun{
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{"RF-001"},
		},
		Findings: []ReviewFinding{{
			ID:                 "RF-001",
			Source:             "model",
			Severity:           reviewSeverityLow,
			Category:           "correctness",
			Title:              "빌드 검증이 생략되었습니다",
			Evidence:           "No build verification was supplied for the proposed patch.",
			RequiredFix:        "Run a focused build after the edit is applied.",
			TestRecommendation: "/verify --full",
		}},
	}
	if got := preWriteReviewBlockingWarningFindings(run); len(got) != 0 {
		t.Fatalf("build-verification-only warning should not block pre-write, got %#v", got)
	}
}

func TestPreWriteReviewBlocksImplementationEvidenceGapEvenWhenVerificationMentioned(t *testing.T) {
	run := ReviewRun{
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{"RF-001"},
		},
		Findings: []ReviewFinding{{
			ID:                 "RF-001",
			Source:             "model",
			Severity:           reviewSeverityMedium,
			Category:           "evidence_gap",
			Title:              "멤버 선언과 초기값 변경 증거가 없어 빌드 검증 가능성을 확인할 수 없습니다",
			Evidence:           "The proposed patch only changes the implementation file.",
			Impact:             "The requested API surface is still missing.",
			RequiredFix:        "Add member declarations, storage, and accessors.",
			TestRecommendation: "Run build verification after the implementation is complete.",
		}},
	}
	if got := preWriteReviewBlockingWarningFindings(run); len(got) != 1 {
		t.Fatalf("implementation evidence gap should block even when verification is mentioned, got %#v", got)
	}
}

func TestPreWriteReviewBlocksLowStyleWarning(t *testing.T) {
	run := ReviewRun{
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{"RF-001"},
		},
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Source:      "model",
			Severity:    reviewSeverityLow,
			Category:    "style",
			Title:       "drivePath block opening brace violates Allman style",
			Evidence:    "The opening brace for the drivePath block is indented as part of the previous statement.",
			RequiredFix: "Move the opening brace to its own line and align indentation.",
		}},
	}
	if got := preWriteReviewBlockingWarningFindings(run); len(got) != 1 || got[0].ID != "RF-001" {
		t.Fatalf("style warning should block pre-write so the patch can be corrected, got %#v", got)
	}
}

func TestHighModelFindingBlocksWhenUserAskedToFix(t *testing.T) {
	run := ReviewRun{
		Trigger:   reviewBeforeFixTrigger,
		Mode:      reviewModeLiveFix,
		Objective: "@Sample/PathConverter.cpp:132-221 검토하고 버그를 수정해",
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Source:      "model",
			Severity:    reviewSeverityHigh,
			Category:    "stability",
			Title:       "std::mismatch can read past a short input",
			Evidence:    "The comparison advances through the longer prefix without checking input length.",
			RequiredFix: "Check input length before std::mismatch.",
			Quality:     reviewFindingQualityComplete,
		}},
	}
	gate := evaluateReviewGate(run)
	if gate.Verdict != reviewVerdictNeedsRevision {
		t.Fatalf("expected high complete finding to block explicit fix flow, got %#v", gate)
	}
}

func TestMediumModelFindingBlocksWhenUserAskedToFix(t *testing.T) {
	run := ReviewRun{
		Trigger:   reviewBeforeFixTrigger,
		Mode:      reviewModeLiveFix,
		Objective: "@Sample/PathConverter.cpp:132-221 검토하고 버그를 수정해",
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Source:      "model",
			Severity:    reviewSeverityMedium,
			Category:    "stability",
			Title:       "wcslen underflows on empty input",
			Evidence:    "The code subtracts one from wcslen(volumeName) before validating the length.",
			Impact:      "A malformed or empty volume name can wrap size_t and index outside the buffer.",
			RequiredFix: "Validate the volumeName length before computing lastIndex.",
			Quality:     reviewFindingQualityComplete,
		}},
	}
	gate := evaluateReviewGate(run)
	if gate.Verdict != reviewVerdictNeedsRevision {
		t.Fatalf("expected medium actionable finding to block explicit fix flow, got %#v", gate)
	}
}

func TestLowStyleFindingDoesNotBlockPreFixGate(t *testing.T) {
	run := ReviewRun{
		Trigger:   reviewBeforeFixTrigger,
		Mode:      reviewModeLiveFix,
		Objective: "@Sample/PathConverter.cpp:132-221 검토하고 버그를 수정해",
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Source:      "model",
			Severity:    reviewSeverityLow,
			Category:    "style",
			Title:       "Opening brace should use Allman style",
			Evidence:    "The existing function uses Allman style elsewhere.",
			RequiredFix: "Move the opening brace to its own line.",
			Quality:     reviewFindingQualityComplete,
		}},
	}
	gate := evaluateReviewGate(run)
	if gate.Verdict != reviewVerdictApprovedWithWarnings {
		t.Fatalf("expected pre-fix low style finding to remain a warning, got %#v", gate)
	}
}

func TestHighStyleFindingDoesNotBlockPreFixGateUnlessExplicitBlocker(t *testing.T) {
	run := ReviewRun{
		Trigger:   reviewBeforeFixTrigger,
		Mode:      reviewModeLiveFix,
		Objective: "@Sample/PathConverter.cpp:132-221 검토하고 버그를 수정해",
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Source:      "model",
			Severity:    reviewSeverityHigh,
			Category:    "style",
			Title:       "Opening brace should use Allman style",
			Evidence:    "The patch does not follow the local brace style.",
			RequiredFix: "Move the opening brace to its own line.",
			Quality:     reviewFindingQualityComplete,
		}},
	}
	gate := evaluateReviewGate(run)
	if gate.Verdict != reviewVerdictApprovedWithWarnings {
		t.Fatalf("expected pre-fix high style finding to remain a warning without BlocksGate, got %#v", gate)
	}
}

func TestHighModelFindingDoesNotBlockReadOnlyAnalysis(t *testing.T) {
	run := ReviewRun{
		Mode:      reviewModeGeneralChange,
		Objective: "SampleServer 코드를 분석해서 성능 문제를 검토해줘",
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Source:      "model",
			Severity:    reviewSeverityHigh,
			Category:    "performance",
			Title:       "Single lock can serialize hot-path reads",
			Evidence:    "All player lookups use one lock.",
			RequiredFix: "Consider sharding the lock.",
			Quality:     reviewFindingQualityComplete,
		}},
	}
	gate := evaluateReviewGate(run)
	if gate.Verdict != reviewVerdictApprovedWithWarnings {
		t.Fatalf("expected read-only analysis to report high finding without repair gate, got %#v", gate)
	}
}

func TestPreWriteRepairObligationsIncludeBlockingAndActionableWarnings(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "review-prefix",
		Trigger: reviewBeforeFixTrigger,
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-001"},
			WarningFindings:  []string{"RF-002", "RF-003"},
		},
		Findings: []ReviewFinding{
			{
				ID:          "RF-001",
				Severity:    reviewSeverityHigh,
				Category:    "correctness",
				Title:       "Break stops the volume enumeration",
				RequiredFix: "Use continue for this volume only.",
				BlocksGate:  true,
			},
			{
				ID:          "RF-002",
				Severity:    reviewSeverityMedium,
				Category:    "stability",
				Title:       "Mount point buffer is fixed to MAX_PATH",
				RequiredFix: "Retry with the required dynamic buffer size.",
			},
			{
				ID:          "RF-003",
				Severity:    reviewSeverityLow,
				Category:    "test_gap",
				Title:       "Verification was not run",
				RequiredFix: "Run focused verification.",
			},
		},
	}
	got := preWriteRepairObligationsFromLastReview(session)
	if len(got) != 2 {
		t.Fatalf("expected blocker plus actionable medium warning obligations, got %#v", got)
	}
	if got[0].ID != "RF-001" || got[1].ID != "RF-002" {
		t.Fatalf("unexpected repair obligations: %#v", got)
	}
}

func TestReviewRepairPlanIncludesActionableWarningsWithBlockers(t *testing.T) {
	run := ReviewRun{
		ID:        "review-prefix",
		Trigger:   reviewBeforeFixTrigger,
		Objective: "@SampleApp/SampleWorker/PathConverter.cpp:132-221 검토하고 버그를 수정해",
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-001"},
			WarningFindings:  []string{"RF-002", "RF-003"},
		},
		Findings: []ReviewFinding{
			{
				ID:          "RF-001",
				Severity:    reviewSeverityHigh,
				Category:    "correctness",
				Title:       "Break stops the volume enumeration",
				RequiredFix: "Use continue for this volume only.",
				BlocksGate:  true,
				Quality:     reviewFindingQualityComplete,
			},
			{
				ID:          "RF-002",
				Severity:    reviewSeverityMedium,
				Category:    "stability",
				Title:       "Mount point buffer is fixed to MAX_PATH",
				RequiredFix: "Retry with the required dynamic buffer size.",
				Quality:     reviewFindingQualityComplete,
			},
			{
				ID:          "RF-003",
				Severity:    reviewSeverityMedium,
				Category:    "test_gap",
				Title:       "Verification was not run",
				RequiredFix: "Run focused verification.",
				Quality:     reviewFindingQualityComplete,
			},
		},
	}
	run.RepairPlan = buildReviewRepairPlan(run)
	if !run.RepairPlan.Required {
		t.Fatalf("expected repair plan to be required")
	}
	for _, want := range []string{
		"Blocking findings:",
		"RF-001",
		"Medium-or-higher actionable warnings that must also be handled:",
		"RF-002",
		"Retry with the required dynamic buffer size.",
	} {
		if !strings.Contains(run.RepairPlan.Prompt, want) {
			t.Fatalf("expected repair plan to contain %q, got:\n%s", want, run.RepairPlan.Prompt)
		}
	}
	if strings.Contains(run.RepairPlan.Prompt, "RF-003") {
		t.Fatalf("test_gap warning should not be included as a repair obligation, got:\n%s", run.RepairPlan.Prompt)
	}
	if len(run.RepairPlan.Findings) != 2 || run.RepairPlan.Findings[0] != "RF-001" || run.RepairPlan.Findings[1] != "RF-002" {
		t.Fatalf("expected blocker plus actionable warning IDs, got %#v", run.RepairPlan.Findings)
	}
}

func TestPreWriteRepairObligationsIncludeApprovedActionableWarnings(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "review-prefix",
		Trigger: reviewBeforeFixTrigger,
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{"RF-001", "RF-002"},
		},
		Findings: []ReviewFinding{
			{
				ID:          "RF-001",
				Severity:    reviewSeverityMedium,
				Category:    "correctness",
				Title:       "Single volume failure stops enumeration",
				RequiredFix: "Skip only the failed volume and continue enumerating.",
			},
			{
				ID:          "RF-002",
				Severity:    reviewSeverityLow,
				Category:    "maintainability",
				Title:       "Loop body is duplicated",
				RequiredFix: "Consider deduplicating the loop later.",
			},
		},
	}
	got := preWriteRepairObligationsFromLastReview(session)
	if len(got) != 1 || got[0].ID != "RF-001" {
		t.Fatalf("expected only medium actionable warning obligation from approved_with_warnings pre-fix review, got %#v", got)
	}
}

func TestPreWriteEvidenceIncludesPreFixRepairObligations(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "scripted", "model", "", "default"),
	}
	run := ReviewRun{
		ID:      "review-prewrite",
		Trigger: "pre_write",
		Target:  reviewTargetChange,
		Mode:    reviewModeLiveFix,
		EditProposals: []EditProposal{{
			File:            "main.cpp",
			Operation:       "patch",
			ExpectedPreview: "- break;\n+ continue;\n",
		}},
	}
	_, evidence := collectReviewEvidence(context.Background(), rt, root, run, ReviewHarnessOptions{
		ProvidedDiff: "- break;\n+ continue;\n",
		RepairFindings: []ReviewFinding{
			{
				ID:          "RF-002",
				Severity:    reviewSeverityMedium,
				Category:    "stability",
				Path:        "main.cpp",
				Title:       "Mount point buffer is fixed to MAX_PATH",
				RequiredFix: "Retry with the required dynamic buffer size.",
			},
		},
		MaxContextChars: 20000,
	})
	for _, want := range []string{
		"Required repair findings from pre-fix review",
		"RF-002",
		"Mount point buffer is fixed to MAX_PATH",
		"Retry with the required dynamic buffer size.",
	} {
		if !strings.Contains(evidence.Text, want) {
			t.Fatalf("expected pre-write evidence to contain %q, got:\n%s", want, evidence.Text)
		}
	}
}

func TestEditProposalsFromPreviewAvoidsDuplicatingMultiFilePreview(t *testing.T) {
	proposals := editProposalsFromPreview(EditPreview{
		Title:     "Apply patch",
		Preview:   strings.Repeat("diff line\n", 200),
		Paths:     []string{"a.go", "b.go"},
		Operation: "apply_patch",
	})
	if len(proposals) != 2 {
		t.Fatalf("expected two proposals, got %#v", proposals)
	}
	if proposals[0].ExpectedPreview == "" {
		t.Fatalf("first proposal should keep the shared preview body")
	}
	if proposals[1].ExpectedPreview != "" {
		t.Fatalf("subsequent proposals should not duplicate preview body: %#v", proposals[1])
	}
	if proposals[0].PreviewFingerprint == "" || proposals[0].PreviewFingerprint != proposals[1].PreviewFingerprint {
		t.Fatalf("expected shared preview fingerprint, got %#v", proposals)
	}
	rendered := renderEditProposalsForEvidence(proposals)
	if !strings.Contains(rendered, "shared by preview_fingerprint") {
		t.Fatalf("expected shared preview marker, got %q", rendered)
	}
}

func TestPostChangeReviewCanBeDisabled(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Review.AutoAfterChange = boolPtr(false)
	agent := &Agent{
		Config:    cfg,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   NewSession(root, "", "", "", "default"),
	}
	reviewed, _, _, _, err := agent.maybeRunPostChangeReview(context.Background(), "implement", "")
	if err != nil {
		t.Fatalf("disabled post-change review: %v", err)
	}
	if reviewed {
		t.Fatalf("expected disabled auto review to skip")
	}
}

func TestReviewHarnessRedactsSensitiveEvidence(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		cfg:       DefaultConfig(root),
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:      "explicit_mcp",
		Target:       "change",
		Request:      "review supplied diff with token sk-abcdefghijklmnopqrstuvwxyz123456",
		ProvidedDiff: "diff --git a/.env b/.env\n+OPENAI_API_KEY=sk-abcdefghijklmnopqrstuvwxyz123456",
		NoModel:      true,
	})
	if err != nil {
		t.Fatalf("run review: %v", err)
	}
	if !run.Redaction.Redacted {
		t.Fatalf("expected redaction report: %#v", run.Redaction)
	}
	data, err := os.ReadFile(filepath.Join(root, ".kernforge", "reviews", "latest.json"))
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if strings.Contains(string(data), "sk-abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("secret leaked into review artifact: %s", string(data))
	}
	if !strings.Contains(string(data), "[REDACTED:") {
		t.Fatalf("redaction marker missing from artifact: %s", string(data))
	}
}

func TestReviewModelParserKeepsMultipleStructuredFindings(t *testing.T) {
	raw := strings.Join([]string{
		"REVIEW_RESULT",
		"findings:",
		"- severity: high",
		"  title: Missing bounds check",
		"  category: security",
		"  path: driver/ioctl.cpp",
		"  evidence: IOCTL length is trusted",
		"  impact: kernel read may exceed the user buffer",
		"  required_fix: validate the length before copy",
		"  test_recommendation: add short-buffer IOCTL test",
		"- severity: medium",
		"  title: Missing regression test",
		"  category: test_gap",
		"  evidence: no verification evidence was supplied",
		"  required_fix: run /verify --full",
	}, "\n")
	findings, quality := parseModelReviewFindings(raw, "security_reviewer")
	if quality != reviewModelQualityUsable {
		t.Fatalf("unexpected quality: %s", quality)
	}
	if len(findings) != 2 {
		t.Fatalf("expected two findings, got %#v", findings)
	}
	if findings[0].Title != "Missing bounds check" || findings[1].Title != "Missing regression test" {
		t.Fatalf("parser merged or lost findings: %#v", findings)
	}
	if findings[0].TestRecommendation != "add short-buffer IOCTL test" {
		t.Fatalf("test recommendation was not parsed: %#v", findings[0])
	}
}

func TestReviewModelParserFlagsCutOffKoreanTestRecommendation(t *testing.T) {
	raw := strings.Join([]string{
		"REVIEW_RESULT",
		"verdict: needs_revision",
		"summary: 워치독 처리 문제",
		"findings:",
		"- severity: high",
		"  category: stability",
		"  title: Watchdog failure path exits abruptly",
		"  path: main.go",
		"  symbol: main",
		"  evidence: watchdog timeout path invokes an abrupt shutdown helper",
		"  impact: graceful shutdown and final logging can be skipped",
		"  required_fix: route timeout handling through the normal shutdown path",
		"  test_recommendation: 워치독 타임",
	}, "\n")

	findings, quality := parseModelReviewFindingsForLanguage(raw, "primary_reviewer", true)
	if quality != reviewModelQualityWeak {
		t.Fatalf("expected weak quality for cut-off tail, got quality=%s findings=%#v", quality, findings)
	}
	if len(findings) != 2 {
		t.Fatalf("expected original finding plus cut-off placeholder, got %#v", findings)
	}
	if !reviewFindingIsOmittedOutputPlaceholder(findings[1]) {
		t.Fatalf("expected cut-off placeholder, got %#v", findings[1])
	}
}

func TestReviewFindingSynthesizesStableTitleWhenModelOmitsTitle(t *testing.T) {
	finding := ReviewFinding{
		Severity: reviewSeverityMedium,
		Category: "security",
		Path:     "SampleApp/SampleWorker/ServiceInstaller.cpp",
		Evidence: "CreateServiceW installs SampleWorker with a quoted binary path.",
		Impact:   "SampleWorker 서비스가 어떤 권한으로 설치되는지, 어떤 바이너리를 실행하는지, 실패 시 어떤 롤백이 수행되는지 확인할 수 없습니다.",
	}
	finding.Normalize()
	if finding.Title != "security finding in ServiceInstaller.cpp" {
		t.Fatalf("expected stable synthesized title, got %q", finding.Title)
	}
	if strings.Contains(finding.Title, "확인할 수") || strings.Contains(finding.Title, "서명 검증") {
		t.Fatalf("title should not be a hard-truncated evidence or impact sentence: %q", finding.Title)
	}
}

func TestReviewModelParserKeepsLongKoreanFallbackFindingText(t *testing.T) {
	raw := `- high: "JPEG 데이터가 잘린 상태로 호출자 버퍼에 기록될 수 있습니다. 이후 성공 경로에서 이 값을 정상 JPEG로 취급하면 호출자는 손상된 데이터를 저장하거나 전송하게 됩니다. 성공 처리는 전체 JPEG가 복사된 경우에만 허용해야 합니다."`
	findings, quality := parseModelReviewFindings(raw, "primary_reviewer")
	if quality != reviewModelQualityUsable || len(findings) != 1 {
		t.Fatalf("expected one usable finding, quality=%s findings=%#v", quality, findings)
	}
	title := findings[0].Title
	if strings.Contains(title, "...") || strings.ContainsRune(title, '\uFFFD') || !utf8.ValidString(title) {
		t.Fatalf("fallback finding title should not be truncated or split: %q", title)
	}
	if strings.HasPrefix(title, "\"") || strings.HasSuffix(title, "\"") {
		t.Fatalf("fallback finding title should trim wrapper quotes: %q", title)
	}
	if !strings.Contains(title, "성공 처리는 전체 JPEG가 복사된 경우에만 허용해야 합니다") {
		t.Fatalf("fallback finding title lost the important tail text: %q", title)
	}
}

func TestReviewModelParserRejectsOmittedFindingText(t *testing.T) {
	raw := strings.Join([]string{
		"REVIEW_RESULT",
		"verdict: needs_revision",
		"summary: omitted output should be rejected",
		"findings:",
		"- severity: high",
		"  category: correctness",
		"  title: ...",
		"  evidence: omitted for brevity",
		"  impact: ...",
		"  required_fix: ...",
	}, "\n")

	findings, quality := parseModelReviewFindings(raw, "primary_reviewer")
	if quality != reviewModelQualityWeak || len(findings) != 1 {
		t.Fatalf("expected one weak placeholder finding, quality=%s findings=%#v", quality, findings)
	}
	finding := findings[0]
	if finding.Title != "Reviewer output omitted part of a finding" ||
		finding.Category != "evidence_gap" ||
		finding.Severity != reviewSeverityMedium ||
		finding.BlocksGate {
		t.Fatalf("omitted finding should become non-blocking quality finding, got %#v", finding)
	}
	for _, value := range []string{finding.Title, finding.Evidence, finding.Impact, finding.RequiredFix} {
		if strings.Contains(value, "...") || strings.Contains(value, "…") || strings.ContainsRune(value, '\uFFFD') {
			t.Fatalf("placeholder should not render omitted text markers, got %#v", finding)
		}
	}
}

func TestReviewModelParserSalvagesOmittedFindingText(t *testing.T) {
	raw := strings.Join([]string{
		"REVIEW_RESULT",
		"verdict: needs_revision",
		"summary: one field contains an omission marker",
		"findings:",
		"- severity: high",
		"  category: correctness",
		"  title: Buffer validation is missing...",
		"  evidence: DataCount is trusted before indexing",
		"  impact: out-of-bounds read",
		"  required_fix: validate DataCount",
	}, "\n")

	findings, quality := parseModelReviewFindings(raw, "primary_reviewer")
	if quality != reviewModelQualityUsable || len(findings) != 1 {
		t.Fatalf("expected one salvaged finding, quality=%s findings=%#v", quality, findings)
	}
	finding := findings[0]
	if reviewFindingIsOmittedOutputPlaceholder(finding) ||
		finding.Title != "Buffer validation is missing" ||
		finding.Quality != reviewFindingQualityPartial ||
		finding.Severity != reviewSeverityMedium ||
		finding.BlocksGate {
		t.Fatalf("expected non-blocking salvaged finding, got %#v", finding)
	}
	for _, value := range []string{finding.Title, finding.Evidence, finding.Impact, finding.RequiredFix} {
		if strings.Contains(value, "...") || strings.Contains(value, "…") || strings.ContainsRune(value, '\uFFFD') {
			t.Fatalf("salvaged finding should not render omission markers, got %#v", finding)
		}
	}
}

func TestReviewModelParserKeepsUnstructuredNoBlockingSummary(t *testing.T) {
	raw := "No blocking findings. Add a regression test for the new branch."
	findings, quality := parseModelReviewFindings(raw, "primary_reviewer")
	if quality != reviewModelQualityUsable || len(findings) != 1 {
		t.Fatalf("expected one usable info finding, quality=%s findings=%#v", quality, findings)
	}
	finding := findings[0]
	if finding.Severity != reviewSeverityInfo ||
		finding.Title != raw ||
		finding.BlocksGate {
		t.Fatalf("unexpected unstructured approval finding: %#v", finding)
	}
}

func TestReviewModelParserLocalizesAndDeduplicatesOmittedFindings(t *testing.T) {
	raw := strings.Join([]string{
		"REVIEW_RESULT",
		"verdict: needs_revision",
		"summary: omitted output should be rejected",
		"findings:",
		"- severity: high",
		"  category: correctness",
		"  title: ...",
		"  evidence: omitted for brevity",
		"  impact: ...",
		"  required_fix: ...",
		"- severity: high",
		"  category: security",
		"  title: ...",
		"  evidence: omitted for brevity",
		"  impact: ...",
		"  required_fix: ...",
	}, "\n")

	findings, quality := parseModelReviewFindingsForLanguage(raw, "primary_reviewer", true)
	if quality != reviewModelQualityWeak || len(findings) != 1 {
		t.Fatalf("expected one localized weak placeholder finding, quality=%s findings=%#v", quality, findings)
	}
	finding := findings[0]
	for _, value := range []string{finding.Title, finding.Evidence, finding.Impact, finding.RequiredFix} {
		if !textContainsHangul(value) {
			t.Fatalf("expected Korean placeholder fields, got %#v", finding)
		}
		if strings.Contains(value, "...") || strings.Contains(value, "…") {
			t.Fatalf("placeholder should not include omitted markers, got %#v", finding)
		}
	}
	if finding.Title != "리뷰어 출력이 finding 일부를 생략함" {
		t.Fatalf("unexpected localized placeholder title: %#v", finding)
	}
}

func TestCompactPromptSectionDoesNotSplitKoreanRune(t *testing.T) {
	got := compactPromptSection("가나다라마바사아자차카타파하", 19)
	if !utf8.ValidString(got) || strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("compactPromptSection split a multibyte rune: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected truncated string to keep ellipsis, got %q", got)
	}
}

func TestSecurityHighFindingBlocksGate(t *testing.T) {
	run := ReviewRun{
		PolicyPacks: []string{"windows_kernel_driver"},
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Severity:    reviewSeverityHigh,
			Category:    "security",
			Title:       "Bypassable trust boundary",
			Evidence:    "security-sensitive path",
			RequiredFix: "tighten the boundary",
		}},
	}
	gate := evaluateReviewGate(run)
	if gate.Verdict != reviewVerdictNeedsRevision {
		t.Fatalf("high security finding should block, got %#v", gate)
	}
	if len(gate.BlockingFindings) != 1 || gate.BlockingFindings[0] != "RF-001" {
		t.Fatalf("expected high security finding as blocker, got %#v", gate.BlockingFindings)
	}
	run.Gate = gate
	rendered := renderReviewRunMarkdown(run)
	if strings.Contains(rendered, "## Warnings") {
		t.Fatalf("policy-blocking security finding should not be duplicated as warning: %s", rendered)
	}
}

func TestWeakModelHighFindingDoesNotBlockGate(t *testing.T) {
	raw := strings.Join([]string{
		"REVIEW_RESULT",
		"findings:",
		"- severity: high",
		"  category: security",
		"  title: Suspicious service boundary",
		"  evidence: service code may be risky",
		"  required_fix: inspect the service path",
	}, "\n")

	findings, quality := parseModelReviewFindings(raw, "security_reviewer")
	if quality != reviewModelQualityUsable || len(findings) != 1 {
		t.Fatalf("unexpected parsed findings: quality=%s findings=%#v", quality, findings)
	}
	finding := findings[0]
	if finding.Severity != reviewSeverityMedium ||
		finding.Category != "evidence_gap" ||
		finding.Quality != reviewFindingQualityPartial ||
		finding.BlocksGate {
		t.Fatalf("underspecified model high finding should be downgraded, got %#v", finding)
	}
	gate := evaluateReviewGate(ReviewRun{
		PolicyPacks: []string{"windows_kernel_driver"},
		Findings:    findings,
	})
	if gate.Verdict != reviewVerdictApprovedWithWarnings ||
		len(gate.BlockingFindings) != 0 ||
		len(gate.WarningFindings) != 1 {
		t.Fatalf("weak model finding should warn without blocking, got %#v", gate)
	}
}

func TestCompleteModelHighSecurityFindingBlocksGate(t *testing.T) {
	findings, quality := parseModelReviewFindings(strings.Join([]string{
		"REVIEW_RESULT",
		"findings:",
		"- severity: high",
		"  category: security",
		"  path: driver/ioctl.cpp",
		"  symbol: DispatchIoctl",
		"  title: Missing IOCTL length validation",
		"  evidence: IOCTL length is trusted before copy",
		"  impact: kernel copy can read past the user buffer",
		"  required_fix: validate the length before copy",
		"  test_recommendation: add short-buffer IOCTL test",
	}, "\n"), "security_reviewer")
	if quality != reviewModelQualityUsable || len(findings) != 1 {
		t.Fatalf("unexpected parsed findings: quality=%s findings=%#v", quality, findings)
	}
	if findings[0].Quality != reviewFindingQualityComplete {
		t.Fatalf("expected complete finding, got %#v", findings[0])
	}
	gate := evaluateReviewGate(ReviewRun{
		PolicyPacks: []string{"windows_kernel_driver"},
		Findings:    findings,
	})
	if gate.Verdict != reviewVerdictNeedsRevision ||
		len(gate.BlockingFindings) != 1 {
		t.Fatalf("complete model high security finding should block, got %#v", gate)
	}
}

func TestSecuritySensitiveFallbackModelsProduceGuidance(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	run := ReviewRun{
		Mode:        reviewModeSecurityHardening,
		Flow:        "security_review",
		PolicyPacks: []string{"windows_kernel_driver", "anti_cheat_telemetry"},
		Objective:   "kernel anti-cheat false positive review",
	}
	plan := planReviewModels(cfg, run)
	if !stringSliceContainsCI(plan.MissingRoles, "security_reviewer") {
		t.Fatalf("expected missing dedicated security role, got %#v", plan)
	}
	if !stringSliceContainsCI(plan.MissingRoles, "false_positive_reviewer") {
		t.Fatalf("expected missing dedicated false-positive role, got %#v", plan)
	}
	if len(plan.AssignedModels) == 0 {
		t.Fatalf("fallback model should still be assigned for execution: %#v", plan)
	}
	gate := evaluateReviewGate(ReviewRun{ModelPlan: plan})
	if len(gate.NextCommands) < 2 {
		t.Fatalf("expected model setup next commands, got %#v", gate.NextCommands)
	}
}

func TestSecurityServiceReviewDoesNotRequireFalsePositiveReviewer(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	run := ReviewRun{
		Mode:        reviewModeSecurityHardening,
		Flow:        "security_review",
		PolicyPacks: []string{"windows_kernel_driver"},
		Objective:   "SampleWorker service install/start bug fix",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"SampleApp/SampleWorker/SampleKernelManager.cpp"},
		},
	}
	plan := planReviewModels(cfg, run)
	if !stringSliceContainsCI(plan.RequiredRoles, "security_reviewer") {
		t.Fatalf("expected security reviewer, got %#v", plan)
	}
	if stringSliceContainsCI(plan.RequiredRoles, "false_positive_reviewer") ||
		stringSliceContainsCI(plan.MissingRoles, "false_positive_reviewer") {
		t.Fatalf("service install review should not require false-positive reviewer, got %#v", plan)
	}
}

func TestReviewScopeDiscoveryClassifiesServiceControlWithoutFalsePositive(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	analysis := analyzeReviewRequest(rt, root, ReviewHarnessOptions{
		Target:  reviewTargetChange,
		Request: "SampleWorker CreateService StartService bug fix",
		Paths:   []string{"SampleApp/SampleWorker/SampleKernelManager.cpp"},
	})
	if analysis.InferredMode != reviewModeSecurityHardening {
		t.Fatalf("expected security hardening mode, got %#v", analysis)
	}
	if !reviewDomainSignalsContain(analysis.DomainSignals, "windows_service_control") {
		t.Fatalf("expected service control domain signal, got %#v", analysis.DomainSignals)
	}
	if !reviewRiskSignalsContain(analysis.RiskSignals, "privileged_service_control") {
		t.Fatalf("expected privileged service risk signal, got %#v", analysis.RiskSignals)
	}
	if !stringSliceContainsCI(analysis.PolicyPacks, "windows_service_control") {
		t.Fatalf("expected windows service policy pack, got %#v", analysis.PolicyPacks)
	}
	if stringSliceContainsCI(analysis.PolicyPacks, "anti_cheat_telemetry") {
		t.Fatalf("service control alone should not imply anti-cheat false-positive review, got %#v", analysis.PolicyPacks)
	}
}

func TestReviewScopeDiscoverySearchesServiceFilesBeforeDirtyGit(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	servicePath := filepath.Join(root, "SampleApp", "SampleWorker", "ServiceInstaller.cpp")
	if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
		t.Fatalf("mkdir service dir: %v", err)
	}
	serviceSource := strings.Join([]string{
		"#include <windows.h>",
		"bool InstallSampleWorkerService()",
		"{",
		"    SC_HANDLE scm = OpenSCManagerW(nullptr, nullptr, SC_MANAGER_CREATE_SERVICE);",
		"    SC_HANDLE service = CreateServiceW(scm, L\"SampleWorker\", L\"SampleWorker\", SERVICE_START, SERVICE_WIN32_OWN_PROCESS, SERVICE_AUTO_START, SERVICE_ERROR_NORMAL, L\"SampleWorker.exe\", nullptr, nullptr, nullptr, nullptr, nullptr);",
		"    return StartServiceW(service, 0, nullptr) != FALSE;",
		"}",
	}, "\n")
	if err := os.WriteFile(servicePath, []byte(serviceSource), 0o644); err != nil {
		t.Fatalf("write service source: %v", err)
	}
	unrelatedPath := filepath.Join(root, "SampleApp", "SampleMaster", "DirectCapture.cpp")
	if err := os.MkdirAll(filepath.Dir(unrelatedPath), 0o755); err != nil {
		t.Fatalf("mkdir unrelated dir: %v", err)
	}
	if err := os.WriteFile(unrelatedPath, []byte("int DirtyCapture() { return 1; }\n"), 0o644); err != nil {
		t.Fatalf("write unrelated source: %v", err)
	}
	runTestGit(t, root, "add", ".")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")
	if err := os.WriteFile(unrelatedPath, []byte("int DirtyCapture() { return 2; }\n"), 0o644); err != nil {
		t.Fatalf("dirty unrelated source: %v", err)
	}

	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	request := "SampleWorker 서비스를 설치하고 실행하는 과정을 리뷰해줘"
	analysis := analyzeReviewRequest(rt, root, ReviewHarnessOptions{
		Target:  reviewTargetChange,
		Request: request,
	})
	if !stringSliceContainsCI(analysis.ScopeDiscovery.CandidateFiles, "SampleApp/SampleWorker/ServiceInstaller.cpp") {
		t.Fatalf("expected request-matched service source candidate, got %#v", analysis.ScopeDiscovery.CandidateFiles)
	}
	if stringSliceContainsCI(analysis.ScopeDiscovery.CandidateFiles, "SampleApp/SampleMaster/DirectCapture.cpp") {
		t.Fatalf("dirty unrelated file should not outrank request-matched service source: %#v", analysis.ScopeDiscovery.CandidateFiles)
	}

	run := ReviewRun{
		Target:          analysis.InferredTarget,
		Mode:            analysis.InferredMode,
		Flow:            analysis.SelectedFlow,
		Objective:       request,
		RequestAnalysis: analysis,
	}
	changeSet, evidence := collectReviewEvidence(context.Background(), rt, root, run, ReviewHarnessOptions{
		Target:          reviewTargetChange,
		Request:         request,
		IncludeGitDiff:  true,
		MaxContextChars: 60000,
	})
	if !strings.Contains(evidence.Text, "CreateServiceW") || !strings.Contains(evidence.Text, "StartServiceW") {
		t.Fatalf("review evidence should include request-matched service source, sources=%#v text=%s", evidence.Sources, evidence.Text)
	}
	if strings.Contains(evidence.Text, "DirtyCapture() { return 2; }") {
		t.Fatalf("review evidence should not be dominated by unrelated dirty git diff: %s", evidence.Text)
	}
	if !stringSliceContainsCI(changeSet.ChangedPaths, "SampleApp/SampleWorker/ServiceInstaller.cpp") {
		t.Fatalf("focused service file should be part of reviewed scope, got %#v", changeSet.ChangedPaths)
	}
}

func TestCodeAnalysisRequestPrefersSourceEvidenceBeforeAnalysisReport(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "Source", "ServerRuntime.cpp")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	source := strings.Join([]string{
		"#include <mutex>",
		"class SampleServer",
		"{",
		"public:",
		"    void Tick();",
		"private:",
		"    std::mutex Mutex;",
		"};",
		"void SampleServer::Tick()",
		"{",
		"    std::lock_guard<std::mutex> lock(Mutex);",
		"    FlushTelemetryToDisk();",
		"}",
		strings.Repeat("// SampleServer performance filler\n", 24000),
	}, "\n")
	if len(source) <= 512*1024 {
		t.Fatalf("test fixture must exceed workspace content-search threshold, got %d bytes", len(source))
	}
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	rt.session.LastAnalysis = &ProjectAnalysisSummary{
		RunID: "stale-analysis",
		Goal:  "old report without source excerpts",
	}
	request := "SampleServer 코드를 분석해서 SampleServer가 서버에서 동작할 때 서버의 성능이나 히칭에 영향을 줄 수 있는 부분을 검토해줘"
	analysis := analyzeReviewRequest(rt, root, ReviewHarnessOptions{
		Target:  reviewTargetAuto,
		Request: request,
	})
	if analysis.InferredTarget != reviewTargetSourceAnalysis {
		t.Fatalf("code analysis request should prefer source analysis target, got %#v", analysis)
	}
	if analysis.InferredMode != reviewModePerformanceAnalysis {
		t.Fatalf("server performance analysis request should use performance analysis mode, got %#v", analysis)
	}
	if !stringSliceContainsCI(analysis.ScopeDiscovery.CandidateFiles, "Source/ServerRuntime.cpp") {
		t.Fatalf("expected large symbol-matched source candidate, got %#v", analysis.ScopeDiscovery.CandidateFiles)
	}

	run := ReviewRun{
		Target:          analysis.InferredTarget,
		Mode:            analysis.InferredMode,
		Flow:            analysis.SelectedFlow,
		Objective:       request,
		RequestAnalysis: analysis,
	}
	_, evidence := collectReviewEvidence(context.Background(), rt, root, run, ReviewHarnessOptions{
		Target:          reviewTargetAuto,
		Request:         request,
		MaxContextChars: 60000,
	})
	if len(evidence.Sources) == 0 || evidence.Sources[0] != "file_excerpt" {
		t.Fatalf("source evidence should be collected before git or analysis report evidence, sources=%#v text=%s", evidence.Sources, evidence.Text)
	}
	if !strings.Contains(evidence.Text, "SampleServer::Tick") || !strings.Contains(evidence.Text, "FlushTelemetryToDisk") {
		t.Fatalf("expected SampleServer source evidence, sources=%#v text=%s", evidence.Sources, evidence.Text)
	}
	if strings.Contains(evidence.Text, "old report without source excerpts") || stringSliceContainsCI(evidence.Sources, "analysis_summary") {
		t.Fatalf("source code analysis request must not be routed as analysis_report first, sources=%#v text=%s", evidence.Sources, evidence.Text)
	}
}

func TestSourceSymbolRequestDoesNotFallbackToUnrelatedGitDirectory(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	if err := os.MkdirAll(filepath.Join(root, "kernforge"), 0o755); err != nil {
		t.Fatalf("mkdir unrelated dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "kernforge", "notes.md"), []byte("tool artifact\n"), 0o644); err != nil {
		t.Fatalf("write unrelated artifact: %v", err)
	}
	request := "MissingServer 코드를 분석해서 MissingServer가 서버에서 동작할 때 성능에 영향을 줄 수 있는 부분을 검토해줘"
	discovery := discoverReviewScope(root, request, nil)
	if stringSliceContainsCI(discovery.CandidateFiles, "kernforge/") ||
		stringSliceContainsCI(discovery.CandidateFiles, "kernforge/notes.md") {
		t.Fatalf("source-symbol request should not fall back to unrelated git artifacts, got %#v", discovery.CandidateFiles)
	}
	if len(discovery.CandidateFiles) != 0 {
		t.Fatalf("expected no source candidate for missing symbol, got %#v", discovery.CandidateFiles)
	}
}

func TestAnalysisReportRequestWithoutSourceIntentKeepsAnalysisTarget(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	analysis := analyzeReviewRequest(rt, root, ReviewHarnessOptions{
		Target:  reviewTargetAuto,
		Request: "최근 analysis report를 검토해줘",
	})
	if analysis.InferredTarget != reviewTargetAnalysis {
		t.Fatalf("report-only request should stay analysis_report, got %#v", analysis)
	}
}

func TestSourceSymbolRequestRequiresSymbolMatchBeforeDomainTerms(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"Plugins/Aura/INDEX_IGNORE.txt":                         "DriverEntry IOCTL ProbeForWrite\n",
		"Policy/HackPolicy.json":                                `{"terms":["IRP_MJ_DEVICE_CONTROL","DeviceIoControl"]}`,
		"Source/DungeonCrawler/Core/Game/DCDungeonGameMode.cpp": "void DCDungeonGameMode::Tick(float DeltaSeconds) {}\n",
		"Source/DungeonCrawler/FocusedServerRuntime.cpp": strings.Join([]string{
			"class FocusedServerRuntime",
			"{",
			"public:",
			"    void Tick(float DeltaSeconds);",
			"};",
			"",
		}, "\n"),
	}
	for rel, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	analysis := analyzeReviewRequest(nil, root, ReviewHarnessOptions{
		Target:  reviewTargetAuto,
		Request: "FocusedServerRuntime 코드를 분석해서 FocusedServerRuntime이 서버에서 동작할 때 서버의 성능이나 히칭에 영향을 줄 수 있는 부분을 검토해줘",
	})
	filesFound := analysis.ScopeDiscovery.CandidateFiles
	if len(filesFound) == 0 || filesFound[0] != "Source/DungeonCrawler/FocusedServerRuntime.cpp" {
		t.Fatalf("expected symbol source file first, got %#v", filesFound)
	}
	for _, rel := range filesFound {
		if rel == "Plugins/Aura/INDEX_IGNORE.txt" || rel == "Policy/HackPolicy.json" {
			t.Fatalf("unrelated domain-term file should not be a symbol-scoped candidate: %#v", filesFound)
		}
	}
	if analysis.InferredMode == reviewModeSecurityHardening {
		t.Fatalf("expected UE server performance source request not to become security_hardening, got %#v", analysis)
	}
	if analysis.InferredTarget != reviewTargetSourceAnalysis || analysis.InferredMode != reviewModePerformanceAnalysis {
		t.Fatalf("expected source performance analysis routing, got %#v", analysis)
	}
	if strings.Contains(strings.Join(analysis.ScopeDiscovery.SearchTerms, ","), "DriverEntry") {
		t.Fatalf("expected no kernel search terms from unrelated files, got %#v", analysis.ScopeDiscovery.SearchTerms)
	}
}

func TestSourceSymbolRequestPrefersDefinitionOverReferences(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"Source/DungeonCrawler/Core/Arena/DCArenaGameMode.cpp": strings.Join([]string{
			"void DCArenaGameMode::BeginPlay()",
			"{",
			"    FocusedServerRuntime->StartArena();",
			"}",
		}, "\n"),
		"Source/DungeonCrawler/Core/Game/DCDungeonGameMode.cpp": strings.Join([]string{
			"void DCDungeonGameMode::Tick(float DeltaSeconds)",
			"{",
			"    FocusedServerRuntime->Tick(DeltaSeconds);",
			"}",
		}, "\n"),
		"Source/DungeonCrawler/Runtime/FocusedServerRuntime.cpp": strings.Join([]string{
			"void FocusedServerRuntime::Tick(float DeltaSeconds)",
			"{",
			"    FlushReplicationQueue();",
			"}",
		}, "\n"),
	}
	for rel, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	discovery := discoverReviewScope(root, "FocusedServerRuntime 코드를 분석해서 서버 성능이나 히칭을 검토해줘", nil)
	if len(discovery.CandidateFiles) == 0 || discovery.CandidateFiles[0] != "Source/DungeonCrawler/Runtime/FocusedServerRuntime.cpp" {
		t.Fatalf("expected implementation file before reference files, got %#v", discovery.CandidateFiles)
	}
	for _, rel := range discovery.CandidateFiles {
		if strings.Contains(rel, "GameMode") {
			t.Fatalf("reference-only files should not remain when a symbol implementation file is available: %#v", discovery.CandidateFiles)
		}
	}
	if discovery.ScopeWidth != "focused" {
		t.Fatalf("expected focused scope after definition filtering, got %#v", discovery)
	}
}

func TestSourceSymbolRequestFindsUnrealPrefixedDefinition(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"Source/DungeonCrawler/Core/Game/DCDungeonGameMode.cpp": strings.Join([]string{
			"void DCDungeonGameMode::Tick(float DeltaSeconds)",
			"{",
			"    FocusedServerRuntime->Tick(DeltaSeconds);",
			"}",
		}, "\n"),
		"Source/DungeonCrawler/Runtime/FocusedRuntimeSubsystem.h": strings.Join([]string{
			"class UFocusedServerRuntime : public UGameInstanceSubsystem",
			"{",
			"    GENERATED_BODY()",
			"};",
		}, "\n"),
	}
	for rel, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	discovery := discoverReviewScope(root, "FocusedServerRuntime 코드를 분석해서 서버 성능이나 히칭을 검토해줘", nil)
	if len(discovery.CandidateFiles) != 1 || discovery.CandidateFiles[0] != "Source/DungeonCrawler/Runtime/FocusedRuntimeSubsystem.h" {
		t.Fatalf("expected Unreal-prefixed definition file only, got %#v", discovery.CandidateFiles)
	}
	if discovery.ScopeWidth != "focused" {
		t.Fatalf("expected focused scope after Unreal-prefixed definition match, got %#v", discovery)
	}
}

func TestSourceSymbolRequestRejectsBroadReferenceFanoutWithoutDefinition(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{}
	for i := 0; i < 12; i++ {
		files[fmt.Sprintf("Source/DungeonCrawler/Core/Game/ReferenceOnly%02d.cpp", i)] = strings.Join([]string{
			fmt.Sprintf("void FReferenceOnly%02d::Tick(float DeltaSeconds)", i),
			"{",
			"    FocusedServerRuntime->Tick(DeltaSeconds);",
			"}",
		}, "\n")
	}
	for rel, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	discovery := discoverReviewScope(root, "FocusedServerRuntime 코드를 분석해서 서버 성능이나 히칭을 검토해줘", nil)
	if len(discovery.CandidateFiles) != 0 {
		t.Fatalf("expected broad reference fan-out without definition to be rejected, got %#v", discovery.CandidateFiles)
	}
	if discovery.ScopeWidth != "unknown" {
		t.Fatalf("expected unknown scope with symbol-search narrowing, got %#v", discovery)
	}
	commandText := strings.Join(discovery.NarrowingCommands, "\n")
	if !strings.Contains(commandText, `rg -n "FocusedServerRuntime" .`) {
		t.Fatalf("expected symbol search narrowing command, got %#v", discovery.NarrowingCommands)
	}
}

func TestSourceSymbolRequestWithoutMatchSuggestsSymbolSearchNotUnrelatedPath(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"Plugins/Aura/INDEX_IGNORE.txt":                         "DriverEntry IOCTL ProbeForWrite\n",
		"Policy/HackPolicy.json":                                `{"terms":["IRP_MJ_DEVICE_CONTROL","DeviceIoControl"]}`,
		"Source/DungeonCrawler/Core/Game/DCDungeonGameMode.cpp": "void DCDungeonGameMode::Tick(float DeltaSeconds) {}\n",
	}
	for rel, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	discovery := discoverReviewScope(root, "FocusedServerRuntime 코드를 분석해서 서버 성능이나 히칭을 검토해줘", nil)
	if len(discovery.CandidateFiles) != 0 {
		t.Fatalf("expected no unrelated candidates when symbol is absent, got %#v", discovery.CandidateFiles)
	}
	commandText := strings.Join(discovery.NarrowingCommands, "\n")
	if !strings.Contains(commandText, `rg -n "FocusedServerRuntime" .`) {
		t.Fatalf("expected symbol search narrowing command, got %#v", discovery.NarrowingCommands)
	}
	if strings.Contains(commandText, "INDEX_IGNORE.txt") || strings.Contains(commandText, "HackPolicy.json") {
		t.Fatalf("expected no unrelated path narrowing command, got %#v", discovery.NarrowingCommands)
	}
}

func TestAnalysisTargetWithSourceScopeCollectsSourceBeforeReport(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "Source", "SampleServer.cpp")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	source := strings.Join([]string{
		"void SampleServer::Tick()",
		"{",
		"    FlushTelemetryToDisk();",
		"}",
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	rt.session.LastAnalysis = &ProjectAnalysisSummary{
		RunID: "analysis-after-source",
		Goal:  "stale report should be secondary",
	}
	request := "SampleServer 코드와 latest analysis report를 함께 검토해줘"
	analysis := analyzeReviewRequest(rt, root, ReviewHarnessOptions{
		Target:  reviewTargetAnalysis,
		Request: request,
		Paths:   []string{"Source/SampleServer.cpp"},
	})
	run := ReviewRun{
		Target:          analysis.InferredTarget,
		Mode:            analysis.InferredMode,
		Flow:            analysis.SelectedFlow,
		Objective:       request,
		RequestAnalysis: analysis,
	}
	_, evidence := collectReviewEvidence(context.Background(), rt, root, run, ReviewHarnessOptions{
		Target:          reviewTargetAnalysis,
		Request:         request,
		Paths:           []string{"Source/SampleServer.cpp"},
		MaxContextChars: 60000,
	})
	fileIndex := indexStringContaining(evidence.Sources, "file_excerpt")
	analysisIndex := indexStringContaining(evidence.Sources, "analysis_summary")
	if fileIndex < 0 || analysisIndex < 0 || fileIndex > analysisIndex {
		t.Fatalf("source evidence should precede analysis summary, sources=%#v text=%s", evidence.Sources, evidence.Text)
	}
	if !strings.Contains(evidence.Text, "SampleServer::Tick") || !strings.Contains(evidence.Text, "stale report should be secondary") {
		t.Fatalf("expected source evidence and secondary analysis summary, sources=%#v text=%s", evidence.Sources, evidence.Text)
	}
}

func TestSourceAnalysisDistributesEvidenceAcrossBoundedFiles(t *testing.T) {
	root := t.TempDir()
	var paths []string
	for i := 0; i < 12; i++ {
		rel := filepath.ToSlash(filepath.Join("Source", "SampleServer", fmt.Sprintf("RuntimePart%02d.cpp", i)))
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir source dir: %v", err)
		}
		source := strings.Join([]string{
			fmt.Sprintf("void SampleServerRuntimePart%02d::Tick()", i),
			"{",
			fmt.Sprintf("    // UNIQUE_SOURCE_ANALYSIS_MARKER_%02d", i),
			strings.Repeat("    UpdateHotPathState();\n", 900),
			"}",
		}, "\n")
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		paths = append(paths, rel)
	}
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	request := "SampleServer 코드를 분석해서 서버 성능이나 히칭에 영향을 줄 수 있는 부분을 검토해줘"
	analysis := analyzeReviewRequest(rt, root, ReviewHarnessOptions{
		Target:  reviewTargetSourceAnalysis,
		Mode:    reviewModePerformanceAnalysis,
		Request: request,
		Paths:   paths,
	})
	run := ReviewRun{
		Target:          analysis.InferredTarget,
		Mode:            analysis.InferredMode,
		Flow:            analysis.SelectedFlow,
		Objective:       request,
		RequestAnalysis: analysis,
	}
	changeSet, evidence := collectReviewEvidence(context.Background(), rt, root, run, ReviewHarnessOptions{
		Target:              reviewTargetSourceAnalysis,
		Mode:                reviewModePerformanceAnalysis,
		Request:             request,
		Paths:               paths,
		IncludeFileContents: true,
		MaxContextChars:     reviewSourceAnalysisMaxContextChars,
	})
	if len(changeSet.ChangedPaths) < 8 {
		t.Fatalf("expected bounded source analysis to sample many candidate files, got %d paths: %#v", len(changeSet.ChangedPaths), changeSet.ChangedPaths)
	}
	if len(evidence.Text) > reviewSourceAnalysisMaxContextChars {
		t.Fatalf("evidence text should be clamped to max context, got %d > %d", len(evidence.Text), reviewSourceAnalysisMaxContextChars)
	}
	if !strings.Contains(evidence.Text, "UNIQUE_SOURCE_ANALYSIS_MARKER_00") ||
		!strings.Contains(evidence.Text, "UNIQUE_SOURCE_ANALYSIS_MARKER_07") {
		t.Fatalf("expected evidence to include multiple file markers, paths=%#v text_len=%d", changeSet.ChangedPaths, len(evidence.Text))
	}
}

func TestReviewEvidenceOmitsGitDiffWhenContextBudgetIsExhausted(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	runTestGit(t, root, "config", "user.email", "test@example.com")
	runTestGit(t, root, "config", "user.name", "Test User")
	sourcePath := filepath.Join(root, "Source", "SampleServer.cpp")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	initial := strings.Repeat("void SampleServer::Tick() { }\n", 200)
	if err := os.WriteFile(sourcePath, []byte(initial), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	runTestGit(t, root, "add", "Source/SampleServer.cpp")
	runTestGit(t, root, "commit", "-m", "initial")
	if err := os.WriteFile(sourcePath, []byte(initial+"\n// DIFF_ONLY_MARKER_SHOULD_NOT_LEAK\n"), 0o644); err != nil {
		t.Fatalf("update source: %v", err)
	}
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	request := "@Source/SampleServer.cpp SampleServer 성능을 분석해줘"
	analysis := analyzeReviewRequest(rt, root, ReviewHarnessOptions{
		Target: reviewTargetSourceAnalysis,
		Mode:   reviewModePerformanceAnalysis,
		Paths:  []string{"Source/SampleServer.cpp"},
	})
	run := ReviewRun{
		Target:          analysis.InferredTarget,
		Mode:            analysis.InferredMode,
		Flow:            analysis.SelectedFlow,
		Objective:       request,
		RequestAnalysis: analysis,
	}
	_, evidence := collectReviewEvidence(context.Background(), rt, root, run, ReviewHarnessOptions{
		Target:              reviewTargetSourceAnalysis,
		Mode:                reviewModePerformanceAnalysis,
		Request:             request,
		Paths:               []string{"Source/SampleServer.cpp"},
		IncludeFileContents: true,
		IncludeGitDiff:      true,
		MaxContextChars:     1000,
	})
	if strings.Contains(evidence.Text, "DIFF_ONLY_MARKER_SHOULD_NOT_LEAK") {
		t.Fatalf("git diff should not be appended after context budget is exhausted: %s", evidence.Text)
	}
	if !strings.Contains(strings.Join(evidence.Warnings, "\n"), "git diff excerpt omitted because review context budget is exhausted") {
		t.Fatalf("expected budget exhaustion warning, got %#v", evidence.Warnings)
	}
}

func TestNaturalReviewIncludesSymbolExcerptFromLargeMentionedFile(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "Product", "Master", "RuntimeStatus.cpp")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	var lines []string
	for i := 0; i < 9000; i++ {
		lines = append(lines, fmt.Sprintf("int filler_%04d = %d;", i, i))
	}
	lines = append(lines,
		"FocusedStatus GetFocusedStatus(const FocusedState& state)",
		"{",
		"    FocusedStatus status = {};",
		"    status.enabled = state.enabled;",
		"    return status;",
		"}",
	)
	for i := 9000; i < 18000; i++ {
		lines = append(lines, fmt.Sprintf("int filler_%04d = %d;", i, i))
	}
	if err := os.WriteFile(sourcePath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	request := "@Product/Master/RuntimeStatus.cpp GetFocusedStatus 관련 코드를 검토해줘"
	opts, _, ok := rt.naturalLanguageReviewOptions(request, nil)
	if !ok {
		t.Fatalf("expected natural review options")
	}
	if !opts.IncludeFileContents || len(opts.Paths) != 1 {
		t.Fatalf("expected path mention to force file contents, got %#v", opts)
	}
	analysis := analyzeReviewRequest(rt, root, opts)
	run := ReviewRun{
		Target:          analysis.InferredTarget,
		Mode:            analysis.InferredMode,
		Flow:            analysis.SelectedFlow,
		Objective:       request,
		RequestAnalysis: analysis,
	}
	_, evidence := collectReviewEvidence(context.Background(), rt, root, run, opts)
	if !strings.Contains(evidence.Text, "Symbol excerpt: Product/Master/RuntimeStatus.cpp :: GetFocusedStatus") ||
		!strings.Contains(evidence.Text, "GetFocusedStatus") ||
		!strings.Contains(evidence.Text, "return status;") {
		t.Fatalf("expected symbol-focused evidence for large mentioned file, got sources=%#v warnings=%#v text=%s", evidence.Sources, evidence.Warnings, evidence.Text)
	}
	if strings.Contains(evidence.Text, "filler_0000") {
		t.Fatalf("symbol-focused excerpt should not dump the start of the large file: %s", evidence.Text)
	}
	for _, warning := range evidence.Warnings {
		if strings.Contains(warning, "too large") || strings.Contains(warning, "skipped Product/Master/RuntimeStatus.cpp") {
			t.Fatalf("large focused file should not be skipped when symbol is present, warnings=%#v", evidence.Warnings)
		}
	}
}

func TestNaturalReviewDoesNotApproveLargeFileWhenRequestedSymbolMissing(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "Product", "Master", "RuntimeStatus.cpp")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	var lines []string
	for i := 0; i < 18000; i++ {
		lines = append(lines, fmt.Sprintf("int filler_%04d = %d;", i, i))
	}
	if err := os.WriteFile(sourcePath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	request := "@Product/Master/RuntimeStatus.cpp GetFocusedStatus 관련 코드를 검토해줘"
	opts, _, ok := rt.naturalLanguageReviewOptions(request, nil)
	if !ok {
		t.Fatalf("expected natural review options")
	}
	analysis := analyzeReviewRequest(rt, root, opts)
	run := ReviewRun{
		Target:          analysis.InferredTarget,
		Mode:            analysis.InferredMode,
		Flow:            analysis.SelectedFlow,
		Objective:       request,
		RequestAnalysis: analysis,
	}
	changeSet, evidence := collectReviewEvidence(context.Background(), rt, root, run, opts)
	run.ChangeSet = changeSet
	run.Evidence = evidence
	run.Findings = deterministicReviewFindings(rt, run)
	run.Gate = evaluateReviewGate(run)
	if !strings.Contains(strings.Join(evidence.Warnings, "\n"), "requested symbols not found: GetFocusedStatus") {
		t.Fatalf("expected missing symbol warning, got %#v", evidence.Warnings)
	}
	if strings.Contains(evidence.Text, "filler_0000") {
		t.Fatalf("large missing-symbol review should not fall back to unrelated file start: %s", evidence.Text)
	}
	if run.Gate.Verdict != reviewVerdictInsufficientEvidence {
		t.Fatalf("missing requested symbol in large file should block as insufficient evidence, got %#v findings=%#v", run.Gate, run.Findings)
	}
}

func TestNaturalReviewIncludesSymbolExcerptFromHugeMentionedFile(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "Source", "HugeRuntime.cpp")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	source := strings.Join([]string{
		strings.Repeat("// filler filler filler filler\n", 350000),
		"void HugeServer::Tick()",
		"{",
		"    FlushTelemetryToDisk();",
		"}",
	}, "\n")
	if len(source) <= 8*1024*1024 {
		t.Fatalf("test fixture must exceed huge file threshold, got %d bytes", len(source))
	}
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	request := "@Source/HugeRuntime.cpp HugeServer 관련 코드를 검토해줘"
	opts, _, ok := rt.naturalLanguageReviewOptions(request, nil)
	if !ok {
		t.Fatalf("expected natural review options")
	}
	analysis := analyzeReviewRequest(rt, root, opts)
	run := ReviewRun{
		Target:          analysis.InferredTarget,
		Mode:            analysis.InferredMode,
		Flow:            analysis.SelectedFlow,
		Objective:       request,
		RequestAnalysis: analysis,
	}
	_, evidence := collectReviewEvidence(context.Background(), rt, root, run, opts)
	if !strings.Contains(evidence.Text, "HugeServer::Tick") || !strings.Contains(evidence.Text, "FlushTelemetryToDisk") {
		t.Fatalf("expected streaming symbol excerpt from huge file, warnings=%#v text=%s", evidence.Warnings, evidence.Text)
	}
	if strings.Contains(strings.Join(evidence.Warnings, "\n"), "file is too large") {
		t.Fatalf("huge symbol-focused source should not be skipped only because of size, warnings=%#v", evidence.Warnings)
	}
}

func TestReviewScopeDiscoveryIgnoresGitHelpInNonRepository(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	discovery := discoverReviewScope(root, "@main.go 리뷰해줘", []string{path})
	if discovery.ScopeWidth != "focused" {
		t.Fatalf("expected focused scope in non-git temp dir, got %#v", discovery)
	}
	if len(discovery.CandidateFiles) != 1 || discovery.CandidateFiles[0] != "main.go" {
		t.Fatalf("expected only the explicit path, got %#v", discovery.CandidateFiles)
	}
	for _, file := range discovery.CandidateFiles {
		if strings.HasPrefix(file, "-") || strings.Contains(file, "usage:") {
			t.Fatalf("git help output must not become a candidate file: %#v", discovery.CandidateFiles)
		}
	}
}

func TestReviewScopeDiscoveryUsesProvidedDiffPaths(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	analysis := analyzeReviewRequest(rt, root, ReviewHarnessOptions{
		Target:  reviewTargetChange,
		Request: "KernForge로 방금 수정한 코드를 리뷰해줘",
		ProvidedDiff: strings.Join([]string{
			"diff --git a/driver.cpp b/driver.cpp",
			"--- a/driver.cpp",
			"+++ b/driver.cpp",
			"@@ -1 +1 @@",
			"+return true;",
		}, "\n"),
	})
	if analysis.ScopeDiscovery.ScopeWidth != "focused" {
		t.Fatalf("provided diff should be focused, got %#v", analysis.ScopeDiscovery)
	}
	if len(analysis.ScopeDiscovery.CandidateFiles) != 1 || analysis.ScopeDiscovery.CandidateFiles[0] != "driver.cpp" {
		t.Fatalf("expected diff path candidate, got %#v", analysis.ScopeDiscovery.CandidateFiles)
	}
	if analysis.InferredMode == reviewModeLiveFix {
		t.Fatalf("provided diff review should not become live_fix only because no explicit path arg was supplied: %#v", analysis)
	}
	run := ReviewRun{RequestAnalysis: analysis, Target: analysis.InferredTarget, Mode: analysis.InferredMode}
	changeSet, evidence := collectReviewEvidence(context.Background(), rt, root, run, ReviewHarnessOptions{
		Target:  reviewTargetChange,
		Request: "KernForge로 방금 수정한 코드를 리뷰해줘",
		ProvidedDiff: strings.Join([]string{
			"diff --git a/driver.cpp b/driver.cpp",
			"--- a/driver.cpp",
			"+++ b/driver.cpp",
			"@@ -1 +1 @@",
			"+return true;",
		}, "\n"),
		MaxContextChars: 60000,
	})
	if !stringSliceContainsCI(changeSet.ChangedPaths, "driver.cpp") || !stringSliceContainsCI(evidence.ChangedPaths, "driver.cpp") {
		t.Fatalf("provided diff path should become changed path, changeSet=%#v evidence=%#v", changeSet.ChangedPaths, evidence.ChangedPaths)
	}
}

func TestReviewScopeDiscoveryNormalizesBeforeAfterDiffPaths(t *testing.T) {
	diff := strings.Join([]string{
		"--- before/Source/PathConverter.cpp",
		"+++ after/Source/PathConverter.cpp",
		"@@ -1 +1 @@",
		"-break;",
		"+continue;",
	}, "\n")
	paths := reviewScopeCandidateFilesFromDiff(diff)
	if len(paths) != 1 || paths[0] != "Source/PathConverter.cpp" {
		t.Fatalf("expected before/after diff paths to normalize to one source path, got %#v", paths)
	}
	for _, path := range paths {
		if strings.HasPrefix(path, "before/") || strings.HasPrefix(path, "after/") {
			t.Fatalf("synthetic diff prefix leaked into candidate path: %#v", paths)
		}
	}
}

func TestReviewScopeDiscoveryRejectsSyntheticToolPaths(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "Source", "PathConverter.cpp")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("int ConvertPath() { return 0; }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	discovery := discoverReviewScope(
		root,
		"@Source/PathConverter.cpp:1-1 ConvertPath 검토하고 버그를 수정해 web/research web/search/browser code/change C:/Win C:// low/correctness medium/stability +/- L'//",
		nil,
	)
	if len(discovery.CandidateFiles) != 1 || discovery.CandidateFiles[0] != "Source/PathConverter.cpp" {
		t.Fatalf("expected only the real source path, got %#v", discovery.CandidateFiles)
	}
	for _, rel := range discovery.CandidateFiles {
		lower := strings.ToLower(rel)
		if strings.HasPrefix(lower, "web/") ||
			strings.HasPrefix(lower, "code/") ||
			strings.HasPrefix(lower, "c:/") ||
			lower == "c:" ||
			lower == "+/-" ||
			strings.Contains(lower, "l'") ||
			strings.Contains(lower, "/correctness") ||
			strings.Contains(lower, "/stability") {
			t.Fatalf("synthetic tool path leaked into candidate files: %#v", discovery.CandidateFiles)
		}
	}
}

func TestReviewScopeDiscoveryKeepsRealWebDirectoryPaths(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "web", "src", "App.tsx")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("export function App() { return null; }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	discovery := discoverReviewScope(root, "@web/src/App.tsx App 컴포넌트를 검토해줘", nil)
	if len(discovery.CandidateFiles) != 1 || discovery.CandidateFiles[0] != "web/src/App.tsx" {
		t.Fatalf("expected real web source path to remain reviewable, got %#v", discovery.CandidateFiles)
	}
}

func TestBroadReviewScopeAddsNarrowingFindingAndCommand(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	analysis := analyzeReviewRequest(rt, root, ReviewHarnessOptions{
		Target:  reviewTargetChange,
		Request: "find bugs and fix",
	})
	if analysis.ScopeDiscovery.ScopeWidth != "broad" {
		t.Fatalf("expected broad scope discovery, got %#v", analysis.ScopeDiscovery)
	}
	run := ReviewRun{
		Target:          reviewTargetChange,
		Mode:            analysis.InferredMode,
		RequestAnalysis: analysis,
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"main.go"},
		},
		Evidence: ReviewEvidencePack{
			Sources: []string{"git_diff"},
			Text:    "diff --git a/main.go b/main.go",
		},
	}
	run.Findings = deterministicReviewFindings(rt, run)
	if !reviewFindingsContainTitle(run.Findings, "Review scope needs narrowing") {
		t.Fatalf("expected scope narrowing finding, got %#v", run.Findings)
	}
	gate := evaluateReviewGate(run)
	if !reviewNextCommandsContainID(gate.NextCommands, "narrow-review") {
		t.Fatalf("expected narrow-review next command, got %#v", gate.NextCommands)
	}
}

func TestExplicitReviewScopeDoesNotAbsorbUnrelatedGitChanges(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write other.go: %v", err)
	}
	runTestGit(t, root, "add", ".")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(root, "other.go"), []byte("package main\n\nfunc other() {}\n"), 0o644); err != nil {
		t.Fatalf("modify other.go: %v", err)
	}

	discovery := discoverReviewScope(root, "@main.go 리뷰해줘", []string{filepath.Join(root, "main.go")})
	if len(discovery.CandidateFiles) != 1 || discovery.CandidateFiles[0] != "main.go" {
		t.Fatalf("explicit review scope should not absorb unrelated git changes, got %#v", discovery)
	}
	if discovery.ScopeWidth != "focused" {
		t.Fatalf("expected focused scope, got %#v", discovery)
	}
}

func TestBaseSecurityPolicyDoesNotForceSecurityReviewer(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	run := ReviewRun{
		Mode:        reviewModeGeneralChange,
		Flow:        "selection_review",
		PolicyPacks: []string{"base_correctness", "base_security", "base_stability"},
		Objective:   "@src/foo.cpp 리뷰해줘",
	}
	if reviewRunSecuritySensitive(run) {
		t.Fatalf("base_security alone should not make a generic review security-sensitive")
	}
	plan := planReviewModels(cfg, run)
	if stringSliceContainsCI(plan.OptionalRoles, "security_reviewer") ||
		stringSliceContainsCI(plan.MissingRoles, "security_reviewer") {
		t.Fatalf("generic review should not recommend security reviewer from base_security alone: %#v", plan)
	}
}

func TestApprovedWarningsRecommendRepairWhenActionable(t *testing.T) {
	run := ReviewRun{
		Mode:        reviewModeGeneralChange,
		Flow:        "selection_review",
		PolicyPacks: []string{"base_correctness", "base_security"},
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Source:      "model",
			Severity:    reviewSeverityMedium,
			Category:    "stability",
			Title:       "Handle leak on failure path",
			Path:        "TaverDartManager.cpp",
			Evidence:    "CreateEvent can succeed before a later failure.",
			Impact:      "Repeated failures can leak handles.",
			RequiredFix: "Close acquired handles on every failure path.",
			Quality:     reviewFindingQualityComplete,
		}},
	}
	gate := evaluateReviewGate(run)
	if gate.Verdict != reviewVerdictApprovedWithWarnings {
		t.Fatalf("expected approved_with_warnings, got %#v", gate)
	}
	if !reviewNextCommandsContainID(gate.NextCommands, "repair-warnings") {
		t.Fatalf("expected actionable warning repair command, got %#v", gate.NextCommands)
	}
	if !reviewNextCommandsContainID(gate.NextCommands, "completion-audit") {
		t.Fatalf("expected completion audit to remain available, got %#v", gate.NextCommands)
	}
}

func reviewNextCommandsContainID(commands []ReviewNextCommand, id string) bool {
	for _, command := range commands {
		if strings.EqualFold(command.ID, id) {
			return true
		}
	}
	return false
}

func reviewDomainSignalsContain(signals []ReviewDomainSignal, domain string) bool {
	for _, signal := range signals {
		if strings.EqualFold(signal.Domain, domain) {
			return true
		}
	}
	return false
}

func reviewRiskSignalsContain(signals []ReviewRiskSignal, risk string) bool {
	for _, signal := range signals {
		if strings.EqualFold(signal.Risk, risk) {
			return true
		}
	}
	return false
}

func reviewFindingsContainTitle(findings []ReviewFinding, title string) bool {
	for _, finding := range findings {
		if strings.EqualFold(finding.Title, title) {
			return true
		}
	}
	return false
}

func TestReviewModelsCommandShortFormConfiguresRole(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var out bytes.Buffer
	cfg := DefaultConfig(workspace)
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	rt := &runtimeState{
		writer:  &out,
		ui:      UI{},
		cfg:     cfg,
		session: &Session{Provider: "openai", Model: "gpt-main", PermissionMode: "default"},
	}

	if err := rt.handleReviewModelsCommand("security openai gpt-5.4"); err != nil {
		t.Fatalf("handleReviewModelsCommand: %v", err)
	}
	roleCfg := rt.cfg.Review.RoleModels["security_reviewer"]
	if roleCfg.Provider != "openai" || roleCfg.Model != "gpt-5.4" {
		t.Fatalf("unexpected security reviewer config: %#v", roleCfg)
	}
	if !strings.Contains(out.String(), "Review security set") {
		t.Fatalf("expected success output, got %q", out.String())
	}
}

func TestReviewModelsCommandShortFormPersistsRole(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var out bytes.Buffer
	cfg := DefaultConfig(workspace)
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-v4-pro"
	rt := &runtimeState{
		writer:  &out,
		ui:      UI{},
		cfg:     cfg,
		session: &Session{Provider: "deepseek", Model: "deepseek-v4-pro", PermissionMode: "default"},
	}

	if err := rt.handleReviewModelsCommand("primary deepseek deepseek-v4-pro low"); err != nil {
		t.Fatalf("handleReviewModelsCommand: %v", err)
	}
	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	roleCfg := loaded.Review.RoleModels["primary_reviewer"]
	if roleCfg.Provider != "deepseek" || roleCfg.Model != "deepseek-v4-pro" || roleCfg.ReasoningEffort != "high" {
		t.Fatalf("expected review primary model to persist, got %#v", loaded.Review.RoleModels)
	}
}

func TestReviewModelsCommandDefaultsRoleEffortToHigh(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var out bytes.Buffer
	cfg := DefaultConfig(workspace)
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-v4-pro"
	rt := &runtimeState{
		writer:  &out,
		ui:      UI{},
		cfg:     cfg,
		session: &Session{Provider: "deepseek", Model: "deepseek-v4-pro", PermissionMode: "default"},
	}

	if err := rt.handleReviewModelsCommand("primary deepseek deepseek-v4-pro"); err != nil {
		t.Fatalf("handleReviewModelsCommand: %v", err)
	}
	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	roleCfg := loaded.Review.RoleModels["primary_reviewer"]
	if roleCfg.Provider != "deepseek" || roleCfg.Model != "deepseek-v4-pro" || roleCfg.ReasoningEffort != "high" {
		t.Fatalf("expected review primary model to default to high, got %#v", loaded.Review.RoleModels)
	}
	if !strings.Contains(out.String(), "defaulted to high") {
		t.Fatalf("expected high default notice, got %q", out.String())
	}
}

func TestReviewModelsClearPersistsLastRoleRemoval(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var out bytes.Buffer
	cfg := DefaultConfig(workspace)
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	cfg.Review.RoleModels = map[string]ReviewModelConfig{
		"primary_reviewer": {
			Provider:        "codex-cli",
			Model:           "gpt-5.5",
			ReasoningEffort: "low",
		},
	}
	if err := SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}
	rt := &runtimeState{
		writer:  &out,
		ui:      UI{},
		cfg:     cfg,
		session: &Session{Provider: "openai", Model: "gpt-main", PermissionMode: "default"},
	}

	if err := rt.handleReviewModelsCommand("clear primary"); err != nil {
		t.Fatalf("handleReviewModelsCommand: %v", err)
	}
	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(loaded.Review.RoleModels) != 0 {
		t.Fatalf("expected /review models clear to persist last role removal, got %#v", loaded.Review.RoleModels)
	}
}

func TestReviewModelsStatusExplainsRolesAndSettings(t *testing.T) {
	var out bytes.Buffer
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
		cfg:    cfg,
	}

	if err := rt.handleReviewModelsCommand("status"); err != nil {
		t.Fatalf("handleReviewModelsCommand: %v", err)
	}
	rendered := out.String()
	for _, needle := range []string{
		"Automatic Review",
		"after_change",
		"review code-changing agent edits by default",
		"Reviewer Roles",
		"security",
		"security boundary",
		"follows main: openai-api / gpt-main",
		"Direct form: /review models security openai-api gpt-5.4",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected status output to contain %q, got %q", needle, rendered)
		}
	}
	for _, internal := range []string{"auto_after_change", "security_reviewer"} {
		if strings.Contains(rendered, internal) {
			t.Fatalf("status output should hide internal key %q, got %q", internal, rendered)
		}
	}
}

func TestReviewModelsCommandInteractiveUsesNumberedChoices(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var out bytes.Buffer
	cfg := DefaultConfig(workspace)
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	cfg.APIKey = "test-key"
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("2\n3\n1\n")),
		writer:      &out,
		ui:          UI{},
		interactive: true,
		cfg:         cfg,
		session:     &Session{Provider: "openai", Model: "gpt-main", PermissionMode: "default"},
	}

	if err := rt.handleReviewModelsCommand(""); err != nil {
		t.Fatalf("handleReviewModelsCommand: %v", err)
	}
	roleCfg := rt.cfg.Review.RoleModels["security_reviewer"]
	if roleCfg.Provider != "openai" || roleCfg.Model != "gpt-5.4" {
		t.Fatalf("unexpected interactive security reviewer config: %#v", roleCfg)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Review Model Role") || !strings.Contains(rendered, "2. security") {
		t.Fatalf("expected numbered role choices, got %q", rendered)
	}
}

func TestReviewModelPromptFollowsKoreanObjectiveLanguage(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.AutoLocale = boolPtr(false)
	run := ReviewRun{
		ID:        "review-1",
		Objective: "@SampleApp/SampleWorker/Txr.cpp:20-57 리뷰해줘",
	}

	system := reviewModelSystemPrompt(cfg, run, "primary_reviewer")
	if !strings.Contains(system, "human-readable narrative fields in Korean") {
		t.Fatalf("expected Korean narrative guidance in system prompt, got %q", system)
	}
	if !strings.Contains(system, "title: <complete short finding title") {
		t.Fatalf("expected structured title field in system prompt, got %q", system)
	}
	if !strings.Contains(system, "Never use ellipses") {
		t.Fatalf("expected no-ellipsis guidance in system prompt, got %q", system)
	}
	prompt := buildReviewModelPrompt(cfg, run, "primary_reviewer")
	if !strings.Contains(prompt, "Write narrative field values in Korean") {
		t.Fatalf("expected Korean narrative guidance in review prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Do not use ellipses") {
		t.Fatalf("expected no-ellipsis guidance in review prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Every finding must include a title field") {
		t.Fatalf("expected title-field guidance in review prompt, got %q", prompt)
	}
}

func TestReviewModelPromptCalibratesSourcePerformanceAnalysisSeverity(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	run := ReviewRun{
		ID:        "review-1",
		Target:    reviewTargetSourceAnalysis,
		Mode:      reviewModePerformanceAnalysis,
		Objective: "SampleServer 코드를 분석해서 서버 성능과 히칭에 영향을 줄 수 있는 부분을 검토해줘",
	}

	prompt := buildReviewModelPrompt(cfg, run, "primary_reviewer")
	for _, needle := range []string{
		"source analysis review",
		"not a proposed code-change review",
		"calibrate severity carefully",
		"Use medium for plausible lock contention",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("expected performance severity calibration %q in prompt, got %q", needle, prompt)
		}
	}

	retry := buildReviewModelOmissionRetryPrompt(cfg, run, "primary_reviewer")
	if !strings.Contains(retry, "source analysis review") || !strings.Contains(retry, "Severity rule") {
		t.Fatalf("expected retry prompt to preserve source performance calibration, got %q", retry)
	}
}

func TestPrintReviewRunNeedsRevisionUsesWarnAndKoreanLabels(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
		cfg:    Config{AutoLocale: boolPtr(false)},
	}
	run := ReviewRun{
		ID:        "review-1",
		Objective: "@SampleApp/SampleWorker/Txr.cpp:20-57 리뷰해줘",
		Target:    reviewTargetSelection,
		Mode:      reviewModeGeneralChange,
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-001"},
		},
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Severity:    reviewSeverityHigh,
			Title:       "범위 검사가 누락됨",
			RequiredFix: "읽기 전에 버퍼 크기를 검증하세요.",
			BlocksGate:  true,
		}},
	}

	rt.printReviewRun(run)
	rendered := out.String()
	if strings.Contains(rendered, "ERROR") {
		t.Fatalf("needs_revision review should not be rendered as ERROR, got %q", rendered)
	}
	for _, needle := range []string{"WARN", "리뷰 review-1: needs_revision", "- 대상: selection", "수정: 읽기 전에 버퍼 크기를 검증하세요."} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected review output to contain %q, got %q", needle, rendered)
		}
	}
}

func TestPrintReviewRunApprovedWithWarningsShowsWarningFindings(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
		cfg:    Config{AutoLocale: boolPtr(false)},
	}
	run := ReviewRun{
		ID:        "review-1",
		Objective: "@SampleApp/SampleMaster/SampleMaster.cpp:869-996 리뷰해줘",
		Target:    reviewTargetSelection,
		Mode:      reviewModeGeneralChange,
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{"RF-001", "RF-002"},
		},
		Findings: []ReviewFinding{
			{
				ID:                 "RF-001",
				Severity:           reviewSeverityMedium,
				Title:              "상태 복구 경로가 불명확함",
				Evidence:           "실패 분기에서 복구 상태가 기록되지 않습니다.",
				Impact:             "호출자가 성공/실패 상태를 잘못 판단할 수 있습니다.",
				RequiredFix:        "실패 시 상태를 명시적으로 복구하세요.",
				TestRecommendation: "복구 경로 회귀 테스트",
			},
			{
				ID:          "RF-002",
				Severity:    reviewSeverityHigh,
				Title:       "동시 실행 경계가 검증되지 않음",
				RequiredFix: "공유 상태 접근 전후의 동기화 경계를 검증하세요.",
			},
		},
	}

	rt.printReviewRun(run)
	rendered := out.String()
	if strings.Contains(rendered, "ERROR") {
		t.Fatalf("approved_with_warnings review should not be rendered as ERROR, got %q", rendered)
	}
	for _, needle := range []string{
		"WARN",
		"리뷰 review-1: approved_with_warnings",
		"경고:",
		"[RF-001] medium: 상태 복구 경로가 불명확함",
		"근거: 실패 분기에서 복구 상태가 기록되지 않습니다.",
		"영향: 호출자가 성공/실패 상태를 잘못 판단할 수 있습니다.",
		"권장 조치: 실패 시 상태를 명시적으로 복구하세요.",
		"테스트: 복구 경로 회귀 테스트",
		"[RF-002] high: 동시 실행 경계가 검증되지 않음",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected review output to contain %q, got %q", needle, rendered)
		}
	}
}

func TestPrintReviewRunExplainsNextCommands(t *testing.T) {
	run := ReviewRun{
		ID:        "review-next",
		Objective: "@SampleApp/SampleMaster/CaptureHelper.cpp:45-145 리뷰해줘",
		Target:    reviewTargetSelection,
		Mode:      reviewModeGeneralChange,
		Gate: GateDecision{
			Verdict: reviewVerdictNeedsRevision,
			NextCommands: []ReviewNextCommand{
				{
					ID:         "verify",
					Command:    "/verify --full",
					Reason:     "changed files have no latest verification evidence",
					Safety:     "safe_local",
					When:       "before completion or git write",
					ClientHint: "Run verification, then repeat /review.",
				},
				{
					ID:         "repair",
					Command:    "/continuity continue from review",
					Reason:     "blocking findings need a focused repair pass",
					Safety:     "safe_local",
					When:       "after reading review findings",
					ClientHint: "Use the repair prompt in the review artifact.",
				},
			},
		},
	}

	rendered := renderReviewCLIResult(Config{AutoLocale: boolPtr(false)}, run)
	for _, needle := range []string{
		"다음 명령:",
		"- /verify --full\n  이유: 변경된 파일에 대한 최신 빌드/테스트 근거가 없습니다.",
		"  시점: 완료 선언 또는 git write 전에",
		"  안전성: safe_local",
		"  자동 실행: false",
		"  확인 필요: false",
		"  실행 방법: `/verify --full`로 검증을 실행한 뒤 `/review`를 다시 실행해 최신 근거를 붙이세요.",
		"  예상 결과: 변경된 파일에 대한 최신 verification report가 기록됩니다.",
		"- /continuity continue from review\n  이유: 차단 finding이 있어서 위 RF 항목을 기준으로 수정 작업을 이어가야 합니다.",
		"  실행 방법: 이 명령을 실행하거나 자연어로 `수정해줘`라고 이어가면 최신 리뷰 finding을 기준으로 repair 흐름을 시작합니다.",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected next-command output to contain %q, got %q", needle, rendered)
		}
	}
}

func TestReviewProviderBehaviorNormalizesDisplayLabels(t *testing.T) {
	cases := map[string]string{
		"openai-codex-subscription": "openai-codex-subscription",
		"openai-codex-cli":          "openai-codex-cli",
		"deepseek-api":              "DeepSeek",
		"lm-studio":                 "LM Studio",
	}
	for input, want := range cases {
		if got := providerUserLabel(input); got != want {
			t.Fatalf("providerUserLabel(%q): want %q, got %q", input, want, got)
		}
	}
}

func TestReviewProviderBehaviorCapsReviewTokens(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "deepseek"
	cfg.MaxTokens = 8192
	run := ReviewRun{Trigger: "explicit_command"}

	if got := reviewRoleMaxTokensForRun(cfg, run); got != 4096 {
		t.Fatalf("expected DeepSeek review token cap, got %d", got)
	}

	cfg.Review.RoleModels = map[string]ReviewModelConfig{
		"security_reviewer": {
			Provider: "lmstudio",
			Model:    "local-reviewer",
		},
	}
	if got := reviewRoleMaxTokensForRoleRun(cfg, "security", run); got != 6000 {
		t.Fatalf("expected role provider token cap, got %d", got)
	}
}

func TestReviewProviderBehaviorControlsOmissionRetryBudget(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "deepseek"
	if got := reviewRoleOmissionRetryBudgetForRun(cfg, "primary"); got != 1 {
		t.Fatalf("expected DeepSeek omission retry budget, got %d", got)
	}

	cfg.Review.RoleModels = map[string]ReviewModelConfig{
		"security_reviewer": {
			Provider: "ollama",
			Model:    "local-reviewer",
		},
	}
	if got := reviewRoleOmissionRetryBudgetForRun(cfg, "security"); got != 1 {
		t.Fatalf("expected local reviewer omission retry budget, got %d", got)
	}
}

func TestReviewMCPResponseIncludesActionContractBooleans(t *testing.T) {
	run := ReviewRun{
		ID:            "review-mcp-contract",
		MachineStatus: reviewMachineStatusNeedsRevision,
		Gate: GateDecision{
			NextCommands: []ReviewNextCommand{{
				ID:             "repair",
				Command:        "/continuity continue from review",
				Reason:         "blocking findings need a focused repair pass",
				When:           "after reading review findings",
				Safety:         "safe_local",
				AutoRun:        false,
				ClientHint:     "Use the repair prompt in the review artifact.",
				ExpectedResult: "The latest review blockers are converted into a focused repair turn.",
			}},
		},
	}

	rendered := renderReviewMCPResponse(run, 20000)
	for _, needle := range []string{
		`"auto_run": false`,
		`"requires_confirmation": false`,
		`"expected_result": "The latest review blockers are converted into a focused repair turn."`,
		`"next_commands"`,
		`"recommended_command"`,
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected MCP review response to contain %q, got %s", needle, rendered)
		}
	}
}

func TestReviewMCPResponseIncludesLatestFreshness(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	runTestGit(t, root, "add", "main.go")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("modify main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.go"), []byte("package main\n\nfunc other() {}\n"), 0o644); err != nil {
		t.Fatalf("write other.go: %v", err)
	}
	run := ReviewRun{
		ID:                "review-freshness",
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Flow:              "change_review",
		Branch:            delegationGitBranch(root),
		ReviewFingerprint: "fp-1",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"main.go"},
		},
		Freshness: ReviewFreshness{
			ReviewFingerprint: "fp-1",
		},
	}
	latest := reviewLatestFreshnessForRoot(root, run)
	if !latest.Stale || !stringSliceContainsCI(latest.InvalidatedBy, "changed_paths") || !strings.Contains(latest.StaleReason, "other.go") {
		t.Fatalf("expected latest freshness to detect unreviewed changed path, got %#v", latest)
	}
	rendered := renderReviewMCPResponseWithLatestFreshness(run, latest, 20000)
	for _, needle := range []string{
		`"latest_review_freshness"`,
		`"invalidated_by"`,
		`"changed_paths"`,
		`other.go`,
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected rendered MCP response to contain %q, got %s", needle, rendered)
		}
	}
}

func TestReviewMarkdownKeepsLongFindingTextAndExplainsNextCommands(t *testing.T) {
	longTitle := "JPEG 데이터가 잘린 상태로 호출자 버퍼에 기록될 수 있습니다. 이후 성공 경로에서 이 값을 정상 JPEG로 취급하면 호출자는 손상된 데이터를 저장하거나 전송하게 됩니다. 성공 처리는 전체 JPEG가 복사된 경우에만 허용해야 합니다."
	run := ReviewRun{
		ID:            "review-report",
		SchemaVersion: reviewSchemaVersion,
		Objective:     "@SampleApp/SampleMaster/DirectCapture.cpp:252-413 리뷰해줘",
		Target:        reviewTargetSelection,
		Mode:          reviewModeGeneralChange,
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{"RF-001"},
			NextCommands: []ReviewNextCommand{{
				ID:         "verify",
				Command:    "/verify --full",
				Reason:     "changed files have no latest verification evidence",
				When:       "before completion or git write",
				Safety:     "safe_local",
				ClientHint: "Run verification, then repeat /review.",
			}},
		},
		Findings: []ReviewFinding{{
			ID:                 "RF-001",
			Severity:           reviewSeverityMedium,
			Category:           "correctness",
			Title:              longTitle,
			Evidence:           longTitle,
			Impact:             "Caller can persist or transmit corrupt image data.",
			RequiredFix:        "Return failure when the caller buffer is too small, or define an explicit size-query contract.",
			TestRecommendation: "Cover exact-size, one-byte-short, and zero-size output buffers.",
		}},
	}

	rendered := renderReviewRunMarkdown(run)
	if strings.Contains(rendered, "호출자는 손상된 데이터를 저장하거나 전...") ||
		strings.ContainsRune(rendered, '\uFFFD') {
		t.Fatalf("review markdown should not contain truncated or split finding text: %q", rendered)
	}
	for _, needle := range []string{
		longTitle,
		"- Required fix: Return failure when the caller buffer is too small",
		"## Next Commands",
		"- `/verify --full`",
		"  - Why: changed files have no latest verification evidence",
		"  - Action: Run verification, then repeat /review.",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected markdown report to contain %q, got %q", needle, rendered)
		}
	}
}

func TestLowSeverityFindingCountsAsGateWarningAndIsRendered(t *testing.T) {
	run := ReviewRun{
		ID:        "review-low",
		Objective: "@SampleApp/SampleMaster/CaptureHelper.cpp:45-145 리뷰해줘",
		Target:    reviewTargetSelection,
		Mode:      reviewModeGeneralChange,
		Findings: []ReviewFinding{
			{
				ID:          "RF-001",
				Severity:    reviewSeverityLow,
				Title:       "정리 가능한 중복 검증 경로",
				RequiredFix: "중복 검증 경로를 하나로 합치십시오.",
			},
			{
				ID:       "RF-002",
				Severity: reviewSeverityInfo,
				Title:    "참고용 기록",
			},
		},
	}
	run.Gate = evaluateReviewGate(run)
	run.finalizeStatus(false)

	if run.Gate.Verdict != reviewVerdictApprovedWithWarnings {
		t.Fatalf("low finding should keep the gate in approved_with_warnings, got %#v", run.Gate)
	}
	if len(run.Gate.WarningFindings) != 1 || run.Gate.WarningFindings[0] != "RF-001" {
		t.Fatalf("expected exactly one warning finding, got %#v", run.Gate.WarningFindings)
	}
	if run.Result.WarningCount != len(run.Gate.WarningFindings) {
		t.Fatalf("result and gate warning counts diverged: result=%d gate=%d", run.Result.WarningCount, len(run.Gate.WarningFindings))
	}
	rendered := renderReviewCLIResult(Config{AutoLocale: boolPtr(false)}, run)
	for _, needle := range []string{
		"발견: 2 blocker=0 warning=1",
		"경고:",
		"[RF-001] low: 정리 가능한 중복 검증 경로",
		"권장 조치: 중복 검증 경로를 하나로 합치십시오.",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected low warning output to contain %q, got %q", needle, rendered)
		}
	}
	if strings.Contains(rendered, "참고용 기록") {
		t.Fatalf("info findings should stay out of concise warning output, got %q", rendered)
	}
	if !strings.Contains(rendered, "note=1") {
		t.Fatalf("hidden info finding should be counted in the header, got %q", rendered)
	}
}

func TestNeedsRevisionReviewHeaderShowsNoteCount(t *testing.T) {
	run := ReviewRun{
		ID:     "review-note-count",
		Target: reviewTargetChange,
		Mode:   reviewModeGeneralChange,
		Findings: []ReviewFinding{
			{
				ID:          "RF-001",
				Severity:    reviewSeverityHigh,
				Title:       "Blocking correctness issue",
				Evidence:    "A concrete code path writes past the buffer.",
				Impact:      "The process can crash.",
				RequiredFix: "Validate the buffer size before writing.",
				BlocksGate:  true,
				Quality:     reviewFindingQualityComplete,
			},
			{
				ID:          "RF-002",
				Severity:    reviewSeverityMedium,
				Title:       "Actionable warning",
				Evidence:    "The same code path lacks a regression test.",
				Impact:      "Regression risk remains.",
				RequiredFix: "Add a focused test.",
				Quality:     reviewFindingQualityComplete,
			},
			{
				ID:       "RF-003",
				Severity: reviewSeverityInfo,
				Title:    "Non-blocking reviewer note",
			},
			{
				ID:       "RF-004",
				Severity: reviewSeverityInfo,
				Title:    "Second non-blocking reviewer note",
			},
		},
	}
	run.Gate = evaluateReviewGate(run)
	run.finalizeStatus(false)
	rendered := renderReviewCLIResult(Config{AutoLocale: boolPtr(false)}, run)
	if !strings.Contains(rendered, "Findings: 4 blocker=1 warning=1 note=2") {
		t.Fatalf("expected header to explain hidden note findings, got %q", rendered)
	}
	if strings.Contains(rendered, "Non-blocking reviewer note") {
		t.Fatalf("needs_revision output should keep notes concise and rely on note count/report, got %q", rendered)
	}
	if run.Result.NoteCount != 2 {
		t.Fatalf("expected finalizeStatus to record note count, got %#v", run.Result)
	}
}

func TestReadOnlyAnalysisRepairNextCommandIsOptionalInKoreanAndEnglish(t *testing.T) {
	run := ReviewRun{
		ID:        "review-analysis-repair",
		Objective: "SampleServer 코드를 분석해서 서버에서 동작할 때 성능이나 히칭에 영향을 줄 수 있는 부분을 검토해줘",
		Target:    reviewTargetChange,
		Mode:      reviewModeGeneralChange,
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Source:      "model",
			Severity:    reviewSeverityHigh,
			Category:    "correctness",
			Title:       "Shared lock serializes all player data access",
			Evidence:    "All players go through the same lock.",
			Impact:      "Server tick work can stall under load.",
			RequiredFix: "Shard locks by player id.",
			BlocksGate:  true,
			Quality:     reviewFindingQualityComplete,
		}},
	}
	run.Gate = evaluateReviewGate(run)
	run.finalizeStatus(false)
	if !reviewNextCommandsContainID(run.Gate.NextCommands, "repair") {
		t.Fatalf("expected optional repair command to remain available, got %#v", run.Gate.NextCommands)
	}

	korean := renderReviewCLIResult(Config{AutoLocale: boolPtr(false)}, run)
	if !strings.Contains(korean, "수정은 사용자가 원할 때만 이어갑니다") {
		t.Fatalf("expected Korean optional repair wording, got %q", korean)
	}
	if strings.Contains(korean, "수정 작업을 이어가야 합니다") {
		t.Fatalf("read-only analysis review should not force repair wording, got %q", korean)
	}

	run.Objective = "Analyze the SampleServer code and review parts that can affect server performance or hitching."
	english := renderReviewCLIResult(Config{AutoLocale: boolPtr(false)}, run)
	for _, want := range []string{
		"blocking findings were found; repair is optional unless the user asks to fix them",
		"if you decide to turn the analysis into a repair pass",
		"only if you want Kernforge to fix the findings",
		"only after an explicit repair request",
	} {
		if !strings.Contains(english, want) {
			t.Fatalf("expected English optional repair wording %q, got %q", want, english)
		}
	}
	if strings.Contains(english, "blocking findings need a focused repair pass") {
		t.Fatalf("read-only analysis review should not force English repair wording, got %q", english)
	}
}

func TestReadOnlySourceAnalysisHighPerformanceFindingWarnsWithoutBlocking(t *testing.T) {
	run := ReviewRun{
		ID:          "review-source-performance",
		Objective:   "SampleServer 코드를 분석해서 서버에서 동작할 때 성능이나 히칭에 영향을 줄 수 있는 부분을 검토해줘",
		Target:      reviewTargetSourceAnalysis,
		Mode:        reviewModePerformanceAnalysis,
		PolicyPacks: []string{"base_security", "source_analysis", "performance_hot_path"},
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Source:      "model",
			Severity:    reviewSeverityHigh,
			Category:    "performance",
			Title:       "Archive data can grow without bounds",
			Evidence:    "Archive calls create new keys without cleanup.",
			Impact:      "Long-running servers can accumulate memory pressure.",
			RequiredFix: "Add retention or cleanup for archived data.",
			Quality:     reviewFindingQualityComplete,
		}},
	}
	run.Gate = evaluateReviewGate(run)
	run.finalizeStatus(false)
	if run.Gate.Verdict != reviewVerdictApprovedWithWarnings {
		t.Fatalf("read-only performance analysis should warn without blocking, got %#v", run.Gate)
	}
	if len(run.Gate.BlockingFindings) != 0 || len(run.Gate.WarningFindings) != 1 {
		t.Fatalf("expected one warning and no blockers, got %#v", run.Gate)
	}
	rendered := renderReviewCLIResult(Config{AutoLocale: boolPtr(false)}, run)
	if strings.Contains(rendered, "needs_revision") || strings.Contains(rendered, "blocker=1") {
		t.Fatalf("read-only source analysis should not render as repair-gated needs_revision, got %q", rendered)
	}
	if !strings.Contains(rendered, "수정은 사용자가 원할 때만") {
		t.Fatalf("expected optional repair wording for read-only analysis warning, got %q", rendered)
	}
}

func TestApprovedReviewRendersInfoFindingWhenNoWarnings(t *testing.T) {
	run := ReviewRun{
		ID:     "review-info",
		Target: reviewTargetChange,
		Mode:   reviewModeGeneralChange,
		Findings: []ReviewFinding{{
			ID:           "RF-001",
			Source:       "model",
			Severity:     reviewSeverityInfo,
			Category:     "maintainability",
			Title:        "No blocking findings. Add a regression test for the new branch.",
			Evidence:     "Reviewer returned an unstructured non-blocking approval summary.",
			Impact:       "No blocking issue was reported, but the summary is weaker than structured findings.",
			BlocksGate:   false,
			Quality:      reviewFindingQualityPartial,
			Confidence:   "medium",
			ReviewerRole: "primary",
		}},
	}
	run.Gate = evaluateReviewGate(run)
	run.finalizeStatus(false)
	rendered := renderReviewCLIResult(Config{AutoLocale: boolPtr(false)}, run)
	if !strings.Contains(rendered, "Notes:") ||
		!strings.Contains(rendered, "[RF-001] info: No blocking findings. Add a regression test for the new branch.") {
		t.Fatalf("approved review should render non-actionable model summary when no warnings exist, got %q", rendered)
	}
}

func TestReviewFinalizeStatusResetsCounts(t *testing.T) {
	run := ReviewRun{
		Gate: GateDecision{Verdict: reviewVerdictNeedsRevision},
		Findings: []ReviewFinding{{
			ID:         "RF-001",
			Severity:   reviewSeverityBlocker,
			Title:      "blocker",
			BlocksGate: true,
		}},
	}
	run.finalizeStatus(false)
	run.finalizeStatus(false)
	if run.Result.BlockingCount != 1 || run.Result.WarningCount != 0 {
		t.Fatalf("finalizeStatus should not accumulate counts, got %#v", run.Result)
	}
}

func TestCompletionAuditFlagsUnreviewedChangedFilesAsStale(t *testing.T) {
	artifact := &CompletionAuditArtifact{ChangedFiles: []string{"cmd/kernforge/review_harness.go", "cmd/kernforge/new_file.go"}}
	review := ReviewRun{
		SchemaVersion: reviewSchemaVersion,
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"cmd/kernforge/review_harness.go"},
		},
	}
	reason := completionAuditReviewFreshnessIssue(t.TempDir(), review, artifact)
	if !strings.Contains(reason, "cmd/kernforge/new_file.go") {
		t.Fatalf("expected stale reason for unreviewed file, got %q", reason)
	}
}

func stringSliceContainsCI(items []string, want string) bool {
	for _, item := range items {
		if strings.EqualFold(item, want) {
			return true
		}
	}
	return false
}
