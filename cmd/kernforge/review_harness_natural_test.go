package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNaturalLanguageReviewRoutesMentionToSelection(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	opts, selection, ok := rt.naturalLanguageReviewOptions("@main.go:2-3 리뷰해줘", nil)
	if !ok {
		t.Fatalf("expected natural review route")
	}
	if selection == nil {
		t.Fatalf("expected mention selection")
	}
	wantPath := filepath.Join(root, "main.go")
	if selection.FilePath != wantPath || selection.StartLine != 2 || selection.EndLine != 3 {
		t.Fatalf("unexpected selection: %#v want path=%q 2-3", selection, wantPath)
	}
	if opts.Trigger != naturalReviewTrigger || opts.Target != reviewTargetSelection {
		t.Fatalf("unexpected opts: %#v", opts)
	}
	if len(opts.Paths) != 1 || opts.Paths[0] != wantPath {
		t.Fatalf("expected selection path in opts, got %#v", opts.Paths)
	}
}

func TestNaturalLanguageReviewRoutesFileMentionToFileContents(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	opts, selection, ok := rt.naturalLanguageReviewOptions("@main.cpp 검토해줘", nil)
	if !ok {
		t.Fatalf("expected file mention review route")
	}
	if selection != nil {
		t.Fatalf("did not expect line selection for file mention: %#v", selection)
	}
	wantPath := filepath.Join(root, "main.cpp")
	if opts.Target != reviewTargetChange || !opts.IncludeFileContents || opts.IncludeGitDiff {
		t.Fatalf("unexpected file mention opts: %#v", opts)
	}
	if len(opts.Paths) != 1 || opts.Paths[0] != wantPath {
		t.Fatalf("expected file mention path, got %#v want %q", opts.Paths, wantPath)
	}
}

func TestNaturalLanguageReviewRoutesActiveSelection(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "", "", "", "default")
	session.AddSelection(ViewerSelection{
		FilePath:  filepath.Join(root, "main.cpp"),
		StartLine: 5,
		EndLine:   9,
	})
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   session,
	}
	opts, selection, ok := rt.naturalLanguageReviewOptions("이 코드 리뷰해줘", nil)
	if !ok {
		t.Fatalf("expected active selection review route")
	}
	if selection != nil {
		t.Fatalf("did not expect a new mention selection: %#v", selection)
	}
	if opts.Trigger != naturalReviewTrigger || opts.Target != reviewTargetSelection {
		t.Fatalf("unexpected opts: %#v", opts)
	}
}

func TestNaturalLanguageReviewKeepsBugFindingWithoutFixAsReview(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "", "", "", "default")
	session.AddSelection(ViewerSelection{
		FilePath:  filepath.Join(root, "main.cpp"),
		StartLine: 5,
		EndLine:   9,
	})
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   session,
	}
	opts, _, ok := rt.naturalLanguageReviewOptions("이 코드 검토하고 버그 찾아줘", nil)
	if !ok {
		t.Fatalf("expected review-only bug finding request to route through natural review")
	}
	if opts.Trigger != naturalReviewTrigger || opts.Target != reviewTargetSelection {
		t.Fatalf("unexpected natural review opts: %#v", opts)
	}
}

func TestNaturalLanguageReviewSkipsNegatedReviewIntent(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "", "", "", "default")
	session.AddSelection(ViewerSelection{
		FilePath:  filepath.Join(root, "main.cpp"),
		StartLine: 1,
		EndLine:   1,
	})
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   session,
	}
	if _, _, ok := rt.naturalLanguageReviewOptions("리뷰 없이 바로 수정해", nil); ok {
		t.Fatalf("expected negated review intent to stay on normal agent path")
	}
}

func TestNaturalLanguageReviewRoutesGenericReviewToAuto(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	opts, selection, ok := rt.naturalLanguageReviewOptions("리뷰해줘", nil)
	if !ok {
		t.Fatalf("expected generic review request to route")
	}
	if selection != nil {
		t.Fatalf("did not expect a selection for generic review: %#v", selection)
	}
	if opts.Target != reviewTargetAuto {
		t.Fatalf("expected auto target, got %#v", opts)
	}
}

func TestNaturalLanguageReviewSkipsDesignDiscussion(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "", "", "", "default")
	session.AddSelection(ViewerSelection{
		FilePath:  filepath.Join(root, "main.cpp"),
		StartLine: 1,
		EndLine:   1,
	})
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   session,
	}
	if _, _, ok := rt.naturalLanguageReviewOptions("review harness 구조는 어때?", nil); ok {
		t.Fatalf("expected design discussion to stay on normal agent path")
	}
}

func TestNaturalLanguageReviewDoesNotSwallowReviewThenFix(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "", "", "", "default")
	session.AddSelection(ViewerSelection{
		FilePath:  filepath.Join(root, "main.cpp"),
		StartLine: 1,
		EndLine:   2,
	})
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   session,
	}
	if _, _, ok := rt.naturalLanguageReviewOptions("이 코드 리뷰하고 버그 수정해줘", nil); ok {
		t.Fatalf("expected review-and-fix request to continue to the agent pre-fix flow")
	}
}

func TestReviewBeforeFixRoutesBugFindAndFixWithoutReviewWord(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	opts, selection, ok := rt.reviewBeforeFixOptions("@Tavern/TavernWorker/TavernUpdManager.cpp:250-322 버그를 찾아서 수정해", nil)
	if !ok {
		t.Fatalf("expected focused bug-fix request to run pre-fix review")
	}
	if selection == nil {
		t.Fatalf("expected selection mention to be captured")
	}
	if opts.Trigger != reviewBeforeFixTrigger || opts.Target != reviewTargetSelection {
		t.Fatalf("unexpected pre-fix opts: %#v", opts)
	}
	if opts.IncludeGitDiff {
		t.Fatalf("selection-scoped bug-fix review should not include unrelated git diff: %#v", opts)
	}
}

func TestReviewBeforeFixSkipsGenericBugFixWithoutTarget(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	if _, _, ok := rt.reviewBeforeFixOptions("fix the bug and summarize the result", nil); ok {
		t.Fatalf("expected generic bug-fix request without a target to stay on the normal planning loop")
	}
}

func TestReviewBeforeFixAddsReviewFeedbackBeforeImplementation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("int value()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.AddSelection(ViewerSelection{
		FilePath:  filepath.Join(root, "main.cpp"),
		StartLine: 1,
		EndLine:   4,
	})
	request := "이 코드 검토하고 버그 수정해줘"
	session.AddMessage(Message{Role: "user", Text: request})
	provider := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: needs_revision",
				"summary: selected function returns the wrong value",
				"findings:",
				"- severity: high",
				"  title: Wrong return value",
				"  category: correctness",
				"  path: main.cpp",
				"  evidence: value returns 0",
				"  impact: callers observe the wrong result",
				"  required_fix: return 1 instead",
				"  test_recommendation: add a focused value test",
			}, "\n")}},
		},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "model"
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	ran, err := agent.maybeRunReviewBeforeFix(context.Background(), request, nil, false, true)
	if err != nil {
		t.Fatalf("review before fix: %v", err)
	}
	if !ran {
		t.Fatalf("expected review before fix to run")
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected one lightweight review model call, got %d", len(provider.requests))
	}
	if !strings.Contains(provider.requests[0].System, "structured review model") {
		t.Fatalf("expected first model call to be the review harness, got system=%q", provider.requests[0].System)
	}
	if provider.requests[0].ReasoningEffort != "low" {
		t.Fatalf("expected pre-fix fallback review effort to be low, got %q", provider.requests[0].ReasoningEffort)
	}
	if provider.requests[0].MaxTokens != 2048 {
		t.Fatalf("expected pre-fix review token budget to be capped, got %d", provider.requests[0].MaxTokens)
	}
	latest := latestUserMessageText(session.Messages)
	if !strings.Contains(latest, "수정 전에 리뷰를 완료") && !strings.Contains(latest, "Review-first pass completed") {
		t.Fatalf("expected review feedback as latest user guidance, got %q", latest)
	}
	for _, banned := range []string{".kernforge", "\nReport:", "review.md", "review.json", "evidence.md", "Review ID", "Next commands"} {
		if strings.Contains(latest, banned) {
			t.Fatalf("pre-fix feedback should not make the implementation model read review artifacts; found %q in %q", banned, latest)
		}
	}
	for _, required := range []string{"Wrong return value", "return 1 instead", "응답 언어 정책"} {
		if !strings.Contains(latest, required) {
			t.Fatalf("expected pre-fix feedback to contain %q, got %q", required, latest)
		}
	}
	if strings.Contains(latest, "Fixed by") {
		t.Fatalf("pre-fix feedback should not describe required fixes as already applied: %q", latest)
	}
	if session.LastReviewRun == nil || session.LastReviewRun.Trigger != reviewBeforeFixTrigger {
		t.Fatalf("expected pre-fix review to be recorded, got %#v", session.LastReviewRun)
	}
	if session.TaskState == nil || !strings.Contains(session.TaskState.PlanSummary, "pre-fix review findings") {
		t.Fatalf("expected pre-fix review to prime task state, got %#v", session.TaskState)
	}
}

func TestReviewBeforeFixDoesNotAlsoInjectLatestReviewFollowUp(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("int value()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: needs_revision",
				"summary: selected function returns the wrong value",
				"findings:",
				"- severity: high",
				"  category: correctness",
				"  path: main.cpp",
				"  evidence: value returns 0",
				"  impact: callers observe the wrong result",
				"  required_fix: return 1 instead",
				"  test_recommendation: add a focused value test",
			}, "\n")}},
		},
	}
	var progress []string
	agent := &Agent{
		Config:         DefaultConfig(root),
		Client:         &scriptedProviderClient{replies: []ChatResponse{{Message: Message{Role: "assistant", Text: "수정 지침을 확인했습니다."}}}},
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Tools:          NewToolRegistry(),
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          store,
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
	}

	if _, err := agent.Reply(context.Background(), "@main.cpp:1-4 버그를 찾아서 수정해"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	combined := strings.Join(sessionMessageTexts(session.Messages), "\n")
	if count := strings.Count(combined, "수정 전에 리뷰를 완료"); count != 1 {
		t.Fatalf("expected one pre-fix review guidance injection, got %d in %q", count, combined)
	}
	if strings.Contains(combined, "직전 리뷰의 차단 finding") {
		t.Fatalf("same turn pre-fix review should not also inject latest-review follow-up guidance: %q", combined)
	}
	for _, message := range progress {
		if strings.Contains(message, "최신 리뷰 결과") {
			t.Fatalf("same turn pre-fix review should not emit latest-review follow-up progress, got %#v", progress)
		}
	}
}

func TestReviewBeforeFixApprovedWithWarningsStopsBeforeImplementation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("int value()\n{\n    return 1;\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	mainProvider := &scriptedProviderClient{
		replies: []ChatResponse{{Message: Message{Role: "assistant", Text: "should not be called"}}},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: approved_with_warnings",
				"summary: 차단 수준 버그는 없고 경계 조건 테스트만 권장됩니다.",
				"findings:",
				"- severity: medium",
				"  category: maintainability",
				"  path: main.cpp",
				"  evidence: value returns a literal",
				"  impact: future behavior changes may need tests",
				"  required_fix: Add a focused regression test if this function becomes externally visible.",
				"  test_recommendation: Add a focused value test.",
			}, "\n")}},
		},
	}
	agent := &Agent{
		Config:         DefaultConfig(root),
		Client:         mainProvider,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Tools:          NewToolRegistry(),
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          store,
	}

	reply, err := agent.Reply(context.Background(), "@main.cpp 버그를 찾고 수정해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "차단 수준의 버그를 찾지 못해서") {
		t.Fatalf("expected no-repair summary, got %q", reply)
	}
	for _, needle := range []string{
		"참고 경고:",
		"근거: value returns a literal",
		"영향: future behavior changes may need tests",
		"권장 조치: Add a focused regression test if this function becomes externally visible.",
		"테스트: Add a focused value test.",
	} {
		if !strings.Contains(reply, needle) {
			t.Fatalf("expected non-blocking reply to include %q, got %q", needle, reply)
		}
	}
	if len(mainProvider.requests) != 0 {
		t.Fatalf("implementation model should not run after non-blocking pre-fix review, got %d requests", len(mainProvider.requests))
	}
	if session.LastReviewRun == nil || session.LastReviewRun.Gate.Verdict != reviewVerdictApprovedWithWarnings {
		t.Fatalf("expected latest review to be recorded, got %#v", session.LastReviewRun)
	}
}

func sessionMessageTexts(messages []Message) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		out = append(out, message.Text)
	}
	return out
}

func TestReviewBeforeFixInlineFeedbackStripsArtifactRefs(t *testing.T) {
	run := ReviewRun{
		ID:            "review-1",
		Trigger:       reviewBeforeFixTrigger,
		Target:        reviewTargetSelection,
		Objective:     "코드를 검토하고 버그를 수정해",
		MachineStatus: "warning",
		Result: ReviewResult{
			Verdict: "approved_with_warnings",
			Summary: "review summary",
		},
		Gate: GateDecision{
			Verdict: "approved_with_warnings",
		},
		ArtifactRefs: []string{
			".kernforge/reviews/review-1/review.md",
			".kernforge/reviews/review-1/review.json",
		},
		Findings: []ReviewFinding{
			{
				ID:          "RF-001",
				Severity:    "medium",
				Category:    "test_gap",
				Title:       "verification gap",
				RequiredFix: "Run tests.",
			},
			{
				ID:          "RF-001",
				Severity:    "medium",
				Category:    "correctness",
				Path:        "main.cpp",
				Title:       "Counter can overflow",
				RequiredFix: "Fixed by changing int to size_t.",
			},
		},
	}
	assignReviewFindingIDs(run.Findings)
	feedback := formatReviewBeforeFixFeedback(run)

	for _, banned := range []string{".kernforge", "review.md", "review.json", "evidence.md", "Next commands", "verification gap"} {
		if strings.Contains(feedback, banned) {
			t.Fatalf("pre-fix inline feedback leaked non-actionable artifact/report text %q in %q", banned, feedback)
		}
	}
	for _, required := range []string{"Counter can overflow", "Apply this fix if it is not already present: changing int to size_t.", "응답 언어 정책"} {
		if !strings.Contains(feedback, required) {
			t.Fatalf("expected inline feedback to contain %q, got %q", required, feedback)
		}
	}
	if run.Findings[0].ID == run.Findings[1].ID {
		t.Fatalf("expected duplicate finding IDs to be reassigned, got %#v", run.Findings)
	}
}

func TestReviewRepairFollowUpUsesLatestBlockingReview(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:        "review-1",
		Objective: "@Tavern/TavernWorker/Txr.cpp:20-57 리뷰해줘",
		Target:    reviewTargetSelection,
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-001"},
		},
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Severity:    reviewSeverityHigh,
			Category:    "correctness",
			Title:       "짧은 버퍼 검증 누락",
			RequiredFix: "헤더 캐스팅 전에 버퍼 크기를 검증하세요.",
			BlocksGate:  true,
		}},
	}
	agent := &Agent{
		Config:  DefaultConfig(root),
		Session: session,
		Store:   NewSessionStore(filepath.Join(root, "sessions")),
	}

	if !agent.maybePrimeRepairFromLastReview("수정해줘", nil, false, true) {
		t.Fatalf("expected latest review to be injected as repair guidance")
	}
	latest := latestUserMessageText(session.Messages)
	if !strings.Contains(latest, "직전 리뷰의 차단 finding") || !strings.Contains(latest, "짧은 버퍼 검증 누락") {
		t.Fatalf("expected latest review guidance in session, got %q", latest)
	}
	if session.TaskState == nil || !strings.Contains(session.TaskState.PlanSummary, "pre-fix review findings") {
		t.Fatalf("expected review repair follow-up to prime task state, got %#v", session.TaskState)
	}
}

func TestReviewBeforeFixUsesFileMentionAsFileEvidence(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	opts, selection, ok := rt.reviewBeforeFixOptions("@Tavern/TavernWorker/UnmountedProcessScanner.cpp 코드를 검토하고 버그를 수정해", nil)
	if !ok {
		t.Fatalf("expected review-before-fix route for file mention")
	}
	if selection != nil {
		t.Fatalf("did not expect line selection for file mention: %#v", selection)
	}
	wantPath := filepath.Join(root, "Tavern", "TavernWorker", "UnmountedProcessScanner.cpp")
	if opts.Target != reviewTargetChange || !opts.IncludeFileContents || opts.IncludeGitDiff {
		t.Fatalf("unexpected pre-fix opts: %#v", opts)
	}
	if len(opts.Paths) != 1 || opts.Paths[0] != wantPath {
		t.Fatalf("expected file mention path, got %#v want %q", opts.Paths, wantPath)
	}
}

func TestReviewBeforeFixSelectionMentionDoesNotIncludeGitDiff(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	opts, selection, ok := rt.reviewBeforeFixOptions("@Tavern/TavernWorker/SpoofChecker.cpp:332-351 코드를 검토하고 버그를 수정해", nil)
	if !ok {
		t.Fatalf("expected review-before-fix route for selection mention")
	}
	if selection == nil {
		t.Fatalf("expected line selection for mention")
	}
	wantPath := filepath.Join(root, "Tavern", "TavernWorker", "SpoofChecker.cpp")
	if opts.Target != reviewTargetSelection || opts.IncludeGitDiff || opts.IncludeFileContents {
		t.Fatalf("unexpected selection-scoped pre-fix opts: %#v", opts)
	}
	if len(opts.Paths) != 1 || opts.Paths[0] != wantPath {
		t.Fatalf("expected selection path, got %#v want %q", opts.Paths, wantPath)
	}
}

func TestPreFixSecurityReviewUsesSingleFallbackRole(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-v4-pro"
	cfg.ReasoningEffort = "xhigh"
	run := ReviewRun{
		Trigger:     reviewBeforeFixTrigger,
		Mode:        reviewModeSecurityHardening,
		Flow:        "security_review",
		Objective:   "anti-cheat kernel code review and bug fix",
		PolicyPacks: []string{"windows_kernel_driver", "anti_cheat_telemetry"},
	}
	plan := planReviewModels(cfg, run)
	if len(plan.RequiredRoles) != 1 || plan.RequiredRoles[0] != "primary_reviewer" {
		t.Fatalf("expected one primary fallback role for pre-fix review, got %#v", plan.RequiredRoles)
	}
	if plan.Strategy != "single" {
		t.Fatalf("expected single strategy, got %#v", plan)
	}
	if got := reviewRoleReasoningEffortForRun(cfg, "primary_reviewer", run); got != "low" {
		t.Fatalf("expected pre-fix fallback effort low, got %q", got)
	}
	if got := reviewRoleMaxTokensForRun(cfg, run); got != 2048 {
		t.Fatalf("expected pre-fix max tokens cap, got %d", got)
	}
}
