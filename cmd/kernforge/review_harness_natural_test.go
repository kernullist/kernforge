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

func TestMaybeHandleNaturalLanguageReviewAllowsNilRuntime(t *testing.T) {
	var rt *runtimeState
	handled, err := rt.maybeHandleNaturalLanguageReview(context.Background(), "리뷰해줘", nil)
	if err != nil {
		t.Fatalf("maybeHandleNaturalLanguageReview nil runtime returned error: %v", err)
	}
	if handled {
		t.Fatalf("nil runtime should not handle natural review")
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

func TestNaturalLanguageReviewRejectsMentionsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "secret.cpp")
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	for _, request := range []string{
		"@../secret.cpp 리뷰해줘",
		"@../../secret.cpp:1-2 리뷰해줘",
		"@" + outside + " 검토해줘",
		"@" + outside + ":1-2 검토해줘",
	} {
		if opts, selection, ok := rt.naturalLanguageReviewOptions(request, nil); ok {
			t.Fatalf("expected outside-workspace mention %q to be rejected, opts=%#v selection=%#v", request, opts, selection)
		}
	}
}

func TestNaturalLanguageReviewAllowsCleanedMentionInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	opts, _, ok := rt.naturalLanguageReviewOptions("@src/../main.cpp 검토해줘", nil)
	if !ok {
		t.Fatalf("expected cleaned inside-workspace mention to route")
	}
	wantPath := filepath.Join(root, "main.cpp")
	if len(opts.Paths) != 1 || opts.Paths[0] != wantPath {
		t.Fatalf("expected cleaned path %q, got %#v", wantPath, opts.Paths)
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

func TestNaturalLanguageReviewSkipsSourceBugDocumentGeneration(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성해"
	if _, _, ok := rt.naturalLanguageReviewOptions(request, nil); ok {
		t.Fatalf("source bug document generation should stay on normal agent path")
	}
	if looksLikeReviewBeforeFixIntent(request) {
		t.Fatalf("source bug document generation should not run pre-fix review")
	}
	if looksLikeReviewOnlyModeIntent(request) {
		t.Fatalf("source bug document generation should not enter review-only mode")
	}
	mode := resolveAgentRequestMode(request, classifyTurnIntent(request))
	if mode.ReadOnlyAnalysis || !mode.ExplicitEditRequest || mode.Intent != TurnIntentEditCode {
		t.Fatalf("source bug document generation should be an editable artifact request, got %#v", mode)
	}
}

func TestReviewOnlyModeDoesNotSwallowNoSourceEditDocumentGeneration(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	request := "소스코드는 수정하지 말고 각 파일을 검토해서 버그를 찾아 보고서로 작성해"
	if _, _, ok := rt.naturalLanguageReviewOptions(request, nil); ok {
		t.Fatalf("no-source-edit document generation should stay on normal agent path")
	}
	if looksLikeReviewOnlyModeIntent(request) {
		t.Fatalf("no-source-edit document generation should not be stolen by review-only mode")
	}
	mode := resolveAgentRequestMode(request, classifyTurnIntent(request))
	if mode.ReadOnlyAnalysis || mode.ReviewOnlyModeRequest || !mode.ExplicitEditRequest || mode.Intent != TurnIntentEditCode {
		t.Fatalf("no-source-edit document generation should remain an editable artifact request, got %#v", mode)
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

func TestNaturalLanguageReviewRoutesReviewModePhraseToAuto(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	opts, selection, ok := rt.naturalLanguageReviewOptions("리뷰 모드로 검토해", nil)
	if !ok {
		t.Fatalf("expected review mode phrase to route")
	}
	if selection != nil {
		t.Fatalf("did not expect selection for generic review mode request: %#v", selection)
	}
	if opts.Trigger != naturalReviewTrigger || opts.Target != reviewTargetAuto {
		t.Fatalf("unexpected review mode opts: %#v", opts)
	}
}

func TestNaturalLanguageReviewModeDoesNotSwallowReviewThenFix(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	if _, _, ok := rt.naturalLanguageReviewOptions("@main.cpp 리뷰 모드로 검토하고 수정해", nil); ok {
		t.Fatalf("expected review mode plus fix request to continue to pre-fix repair flow")
	}
	if !looksLikeReviewBeforeFixIntent("@main.cpp 리뷰 모드로 검토하고 수정해") {
		t.Fatalf("expected review mode plus fix request to be recognized as review-before-fix")
	}
	mode := resolveAgentRequestMode("@main.cpp 리뷰 모드로 검토하고 수정해", TurnIntentEditCode)
	if mode.ReadOnlyAnalysis || mode.ReviewOnlyModeRequest || !mode.ExplicitEditRequest || mode.Intent != TurnIntentEditCode {
		t.Fatalf("mixed review-mode and fix prompt must keep edit intent, got %#v", mode)
	}
	englishMode := resolveAgentRequestMode("@main.cpp reviewer stance로 보고 fix it", TurnIntentEditCode)
	if englishMode.ReadOnlyAnalysis || englishMode.ReviewOnlyModeRequest || !englishMode.ExplicitEditRequest || englishMode.Intent != TurnIntentEditCode {
		t.Fatalf("English mixed review/fix prompt must keep edit intent, got %#v", englishMode)
	}
}

func TestNaturalLanguageReviewModeHonorsNoEditWording(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	opts, _, ok := rt.naturalLanguageReviewOptions("@main.cpp 수정은 하지 말고 리뷰 모드로 검토해", nil)
	if !ok {
		t.Fatalf("expected no-edit review mode request to route as review-only")
	}
	if opts.Target != reviewTargetChange || !opts.IncludeFileContents {
		t.Fatalf("expected file-scoped review-only opts, got %#v", opts)
	}
	if looksLikeReviewBeforeFixIntent("@main.cpp 수정은 하지 말고 리뷰 모드로 검토해") {
		t.Fatalf("no-edit wording must not be treated as review-before-fix")
	}
	mode := resolveAgentRequestMode("@main.cpp 수정은 하지 말고 리뷰 모드로 검토해", TurnIntentEditCode)
	if !mode.ReadOnlyAnalysis || !mode.ReviewOnlyModeRequest || mode.ExplicitEditRequest || mode.Intent == TurnIntentEditCode {
		t.Fatalf("no-edit review mode should remain read-only, got %#v", mode)
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

func TestAgentReplyReviewModeRunsReviewOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("int value()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	session := NewSession(root, "scripted", "model", "", "default")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: needs_revision",
				"summary: selected function returns the wrong value",
				"findings:",
				"- severity: high",
				"  category: correctness",
				"  path: main.cpp",
				"  symbol: value",
				"  title: Wrong return value",
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

	request := "@main.cpp 리뷰 모드로 검토해"
	reply, err := agent.Reply(context.Background(), request)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("review mode should stop after one review model call, got %d", len(provider.requests))
	}
	if !strings.Contains(provider.requests[0].System, "structured review model") {
		t.Fatalf("expected review harness request, got system=%q", provider.requests[0].System)
	}
	if !strings.Contains(provider.requests[0].Messages[0].Text, "read-only code review") {
		t.Fatalf("expected review-mode prompt rule, got %q", provider.requests[0].Messages[0].Text)
	}
	for _, internalNeedle := range []string{
		"Request mode:",
		"Relevant persistent memory",
		"Relevant project analysis",
		"Auto-discovered code context",
	} {
		if strings.Contains(provider.requests[0].Messages[0].Text, internalNeedle) {
			t.Fatalf("review-mode request should not be overwritten by injected context %q, got %q", internalNeedle, provider.requests[0].Messages[0].Text)
		}
	}
	if session.LastReviewRun == nil || session.LastReviewRun.Trigger != naturalReviewTrigger {
		t.Fatalf("expected natural review run, got %#v", session.LastReviewRun)
	}
	if session.LastReviewRun.Objective != request {
		t.Fatalf("review-mode objective should preserve the external user request, got %q want %q", session.LastReviewRun.Objective, request)
	}
	if session.LastReviewRun.RequestAnalysis.OriginalRequest != request {
		t.Fatalf("review-mode analysis should preserve the external user request, got %q want %q", session.LastReviewRun.RequestAnalysis.OriginalRequest, request)
	}
	for _, needle := range []string{"검토 결과:", "Wrong return value", "위치: main.cpp, 심볼 value", "요약:", "판정:"} {
		if !strings.Contains(reply, needle) {
			t.Fatalf("expected review-only reply to contain %q, got %q", needle, reply)
		}
	}
	if strings.Contains(strings.Join(sessionMessageTexts(session.Messages), "\n"), "수정 전에 리뷰를 완료") {
		t.Fatalf("review-only mode must not inject implementation repair guidance, got %#v", session.Messages)
	}
	if session.ConversationState == nil || !strings.Contains(session.ConversationState.LastResult, "검토 결과") {
		t.Fatalf("expected review-only reply to refresh conversation state, got %#v", session.ConversationState)
	}
	assistantEvents := 0
	for _, event := range session.ConversationEvents {
		if event.Kind == conversationEventKindAssistantReply {
			assistantEvents++
		}
	}
	if assistantEvents == 0 {
		t.Fatalf("expected review-only reply to record assistant conversation event")
	}
}

func TestAgentReplyReviewModeCanUseReviewerWithoutMainChatClient(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("int value()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	session := NewSession(root, "scripted", "model", "", "default")
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: approved",
				"summary: no issues found",
				"findings: []",
			}, "\n")}},
		},
	}
	cfg := DefaultConfig(root)
	agent := &Agent{
		Config:         cfg,
		ReviewerClient: reviewer,
		ReviewerModel:  "model",
		Tools:          NewToolRegistry(),
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
	}

	reply, err := agent.Reply(context.Background(), "@main.cpp 리뷰 모드로 검토해")
	if err != nil {
		t.Fatalf("Reply review mode with reviewer-only client: %v", err)
	}
	if len(reviewer.requests) != 1 {
		t.Fatalf("expected reviewer-only review mode to make one review request, got %d", len(reviewer.requests))
	}
	if !strings.Contains(reply, "검토 결과") || !strings.Contains(reply, "판정: approved") {
		t.Fatalf("expected review-mode reply from reviewer-only route, got %q", reply)
	}

	plainSession := NewSession(root, "scripted", "model", "", "default")
	plainAgent := &Agent{
		Config:    cfg,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   plainSession,
		Store:     NewSessionStore(filepath.Join(root, "sessions-plain")),
	}
	if _, err := plainAgent.Reply(context.Background(), "일반 질문이야"); err == nil || !strings.Contains(err.Error(), "no model provider") {
		t.Fatalf("expected non-review request without main client to keep provider error, got %v", err)
	}
}

func TestReviewBeforeFixRoutesBugFindAndFixWithoutReviewWord(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	opts, selection, ok := rt.reviewBeforeFixOptions("@SampleApp/SampleWorker/SampleUpdManager.cpp:250-322 버그를 찾아서 수정해", nil)
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
				"- severity: low",
				"  title: Build verification was not run",
				"  category: test_gap",
				"  path: main.cpp",
				"  evidence: no latest build output was supplied",
				"  impact: compile risk remains unknown",
				"  required_fix: run focused build verification before final approval",
			}, "\n")}},
		},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "model"
	var emittedAssistant []string
	var emittedPersistentAssistant []string
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
		EmitAssistant: func(text string) {
			emittedAssistant = append(emittedAssistant, text)
		},
		EmitAssistantPersistent: func(text string) {
			emittedPersistentAssistant = append(emittedPersistentAssistant, text)
		},
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
	if provider.requests[0].ReasoningEffort != "high" {
		t.Fatalf("expected focused pre-fix bug hunt review effort to be high, got %q", provider.requests[0].ReasoningEffort)
	}
	if provider.requests[0].MaxTokens != 6000 {
		t.Fatalf("expected focused pre-fix bug hunt review token budget to expand, got %d", provider.requests[0].MaxTokens)
	}
	if !strings.Contains(provider.requests[0].Messages[0].Text, "Review the supplied source line by line") {
		t.Fatalf("expected focused pre-fix bug hunt prompt, got %q", provider.requests[0].Messages[0].Text)
	}
	if session.LastReviewRun == nil || session.LastReviewRun.Trigger != reviewBeforeFixTrigger {
		t.Fatalf("expected pre-fix review to be recorded, got %#v", session.LastReviewRun)
	}
	if len(emittedAssistant) != 0 {
		t.Fatalf("expected pre-fix summary to use the persistent assistant emitter, got fallback emissions %#v", emittedAssistant)
	}
	combinedAssistant := strings.Join(emittedPersistentAssistant, "\n")
	if !strings.Contains(combinedAssistant, "검토 결과:") || !strings.Contains(combinedAssistant, "Wrong return value") {
		t.Fatalf("expected deterministic visible pre-fix summary before implementation, got %#v", emittedPersistentAssistant)
	}
	if !strings.Contains(combinedAssistant, "Build verification was not run") {
		t.Fatalf("expected visible pre-fix summary to include non-blocking warnings, got %#v", emittedPersistentAssistant)
	}
	if !sessionHasVisiblePreFixReviewSummary(session, *session.LastReviewRun) {
		t.Fatalf("expected visible pre-fix review summary to be stored in the session, got %#v", session.Messages)
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
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "model"
	agent := &Agent{
		Config:         cfg,
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
	var progress []string
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "model"
	agent := &Agent{
		Config:    cfg,
		Client:    mainProvider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
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
	if len(mainProvider.requests) != 1 {
		t.Fatalf("only the main first-pass review should run after non-blocking pre-fix review, got %d requests", len(mainProvider.requests))
	}
	if !strings.Contains(mainProvider.requests[0].System, "structured review model") {
		t.Fatalf("expected the single model call to be the main first-pass review, got %q", mainProvider.requests[0].System)
	}
	progressText := strings.Join(progress, "\n")
	for _, want := range []string{
		"수정 전 리뷰가 경고와 함께 완료되었습니다.",
		"경고 1개",
		"value returns a literal",
	} {
		if !strings.Contains(progressText, want) {
			t.Fatalf("expected pre-fix warning progress to contain %q, got %#v", want, progress)
		}
	}
	if session.LastReviewRun == nil || session.LastReviewRun.Gate.Verdict != reviewVerdictApprovedWithWarnings {
		t.Fatalf("expected latest review to be recorded, got %#v", session.LastReviewRun)
	}
}

func TestReviewBeforeFixApprovedBugHuntAddsNonConclusiveWarning(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("int value()\n{\n    return 1;\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.AddSelection(ViewerSelection{
		FilePath:  filepath.Join(root, "main.cpp"),
		StartLine: 1,
		EndLine:   4,
	})
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: approved",
				"summary: 차단 수준 버그를 찾지 못했습니다.",
				"findings:",
			}, "\n")}},
		},
	}
	var progress []string
	agent := &Agent{
		Config:         DefaultConfig(root),
		Client:         reviewer,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Tools:          NewToolRegistry(),
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
	}

	ran, err := agent.maybeRunReviewBeforeFix(context.Background(), "이 코드 검토하고 버그 수정해줘", nil, false, true)
	if err != nil {
		t.Fatalf("review before fix: %v", err)
	}
	if !ran {
		t.Fatalf("expected review before fix to run")
	}
	if session.LastReviewRun == nil {
		t.Fatalf("expected latest review run")
	}
	run := *session.LastReviewRun
	if run.Gate.Verdict != reviewVerdictApprovedWithWarnings {
		t.Fatalf("empty approved pre-fix bug hunt should become warning, got %#v", run.Gate)
	}
	if !reviewFindingsContainTitle(run.Findings, "수정 전 리뷰가 실행 가능한 버그 finding을 반환하지 않았습니다") {
		t.Fatalf("expected non-conclusive pre-fix warning, got %#v", run.Findings)
	}
	progressText := strings.Join(progress, "\n")
	if !strings.Contains(progressText, "경고") ||
		!strings.Contains(progressText, "수정 전 리뷰가 실행 가능한 버그 finding을 반환하지 않았습니다") {
		t.Fatalf("expected warning progress for non-conclusive bug hunt, got %#v", progress)
	}
	latest := latestUserMessageText(session.Messages)
	if !strings.Contains(latest, "요청된 코드를 독립적으로 확인") &&
		!strings.Contains(latest, "리뷰가 검증 또는 근거 gap만 보고했습니다") {
		t.Fatalf("expected implementation guidance to require independent inspection, got %q", latest)
	}
	if strings.Contains(latest, "Review approved with warnings") || strings.Contains(latest, "Inspect the requested code") {
		t.Fatalf("expected localized implementation guidance, got %q", latest)
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
	for _, required := range []string{"Counter can overflow", "아직 반영되지 않았다면 이 수정을 적용하세요: changing int to size_t.", "응답 언어 정책", "검토 게이트:", "구현 규칙:"} {
		if !strings.Contains(feedback, required) {
			t.Fatalf("expected inline feedback to contain %q, got %q", required, feedback)
		}
	}
	for _, banned := range []string{"Review gate:", "Implementation rules:", "Inline review findings:", "Required fix:"} {
		if strings.Contains(feedback, banned) {
			t.Fatalf("expected localized pre-fix feedback not to contain %q, got %q", banned, feedback)
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
		Objective: "@SampleApp/SampleWorker/Txr.cpp:20-57 리뷰해줘",
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

func TestReviewRepairFollowUpWithExplicitRFIDScopesRepairGuidance(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:        "review-1",
		Trigger:   reviewBeforeFixTrigger,
		Objective: "@Tavern/TavernMaster/TaverDartManager.cpp CreateDartProcess 함수에 버그가 있는지 검토해줘",
		Target:    reviewTargetChange,
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-001", "RF-004"},
		},
		Findings: []ReviewFinding{
			{
				ID:          "RF-001",
				Severity:    reviewSeverityHigh,
				Category:    "correctness",
				Title:       "CreateProcessW lpCommandLine const pointer",
				RequiredFix: "수정 가능한 command line 버퍼를 전달하세요.",
				BlocksGate:  true,
			},
			{
				ID:          "RF-004",
				Severity:    reviewSeverityHigh,
				Category:    "correctness",
				Title:       "따옴표 없는 경로와 nullptr lpApplicationName",
				RequiredFix: "lpApplicationName에 실행 파일 경로를 명시하세요.",
				BlocksGate:  true,
			},
		},
	}
	agent := &Agent{
		Config:  DefaultConfig(root),
		Session: session,
		Store:   NewSessionStore(filepath.Join(root, "sessions")),
	}

	if !agent.maybePrimeRepairFromLastReview("RF-004 수정해줘", nil, false, true) {
		t.Fatalf("expected latest review to be injected as scoped repair guidance")
	}
	latest := latestUserMessageText(session.Messages)
	if !strings.Contains(latest, "RF-004") || !strings.Contains(latest, "따옴표 없는 경로") {
		t.Fatalf("expected requested RF in scoped repair guidance, got %q", latest)
	}
	if strings.Contains(latest, "RF-001") || strings.Contains(latest, "const pointer") {
		t.Fatalf("did not expect unrequested RF in scoped repair guidance, got %q", latest)
	}
	if !strings.Contains(latest, "나열되지 않은 RF") {
		t.Fatalf("expected explicit no-broaden guidance, got %q", latest)
	}
	if len(session.LastReviewRun.RepairFindings) != 1 || session.LastReviewRun.RepairFindings[0].ID != "RF-004" {
		t.Fatalf("expected carried repair findings to be scoped to RF-004, got %#v", session.LastReviewRun.RepairFindings)
	}
	obligations := preFixRepairObligationFindings(*session.LastReviewRun)
	if len(obligations) != 1 || obligations[0].ID != "RF-004" {
		t.Fatalf("expected pre-fix obligations to stay scoped to RF-004, got %#v", obligations)
	}
	if session.TaskState == nil || strings.Contains(session.TaskState.ReviewerGuidance, "RF-001") {
		t.Fatalf("expected task state reviewer guidance to stay scoped, got %#v", session.TaskState)
	}
}

func TestReviewBeforeFixUsesFileMentionAsFileEvidence(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	opts, selection, ok := rt.reviewBeforeFixOptions("@SampleApp/SampleWorker/UnmountedProcessScanner.cpp 코드를 검토하고 버그를 수정해", nil)
	if !ok {
		t.Fatalf("expected review-before-fix route for file mention")
	}
	if selection != nil {
		t.Fatalf("did not expect line selection for file mention: %#v", selection)
	}
	wantPath := filepath.Join(root, "SampleApp", "SampleWorker", "UnmountedProcessScanner.cpp")
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
	opts, selection, ok := rt.reviewBeforeFixOptions("@SampleApp/SampleWorker/SpoofChecker.cpp:332-351 코드를 검토하고 버그를 수정해", nil)
	if !ok {
		t.Fatalf("expected review-before-fix route for selection mention")
	}
	if selection == nil {
		t.Fatalf("expected line selection for mention")
	}
	wantPath := filepath.Join(root, "SampleApp", "SampleWorker", "SpoofChecker.cpp")
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
	if got := reviewRoleReasoningEffortForRun(cfg, "primary_reviewer", run); got != "xhigh" {
		t.Fatalf("expected pre-fix fallback effort to preserve xhigh main effort, got %q", got)
	}
	if got := reviewRoleMaxTokensForRun(cfg, run); got != 2048 {
		t.Fatalf("expected pre-fix max tokens cap, got %d", got)
	}
}

func TestUIPolishReviewRequiresPrimaryForCorePaths(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	run := ReviewRun{
		Mode: reviewModeUIPolish,
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"cmd/kernforge/verify.go", "cmd/kernforge/ui.go"},
		},
	}
	plan := planReviewModels(cfg, run)
	if !stringSliceContainsCI(plan.RequiredRoles, "primary_reviewer") {
		t.Fatalf("core-path UI polish review should require primary reviewer, got %#v", plan.RequiredRoles)
	}
	if !stringSliceContainsCI(plan.RequiredLenses, "design") {
		t.Fatalf("UI polish review should keep design lens, got %#v", plan.RequiredLenses)
	}
}

func TestUIPolishReviewRequiresPrimaryForEmptyPathSet(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	run := ReviewRun{
		Mode: reviewModeUIPolish,
	}
	plan := planReviewModels(cfg, run)
	if !stringSliceContainsCI(plan.RequiredRoles, "primary_reviewer") {
		t.Fatalf("empty-path UI polish review should require primary reviewer, got %#v", plan.RequiredRoles)
	}
	if !stringSliceContainsCI(plan.RequiredLenses, "design") {
		t.Fatalf("empty-path UI polish review should keep design lens, got %#v", plan.RequiredLenses)
	}
}

func TestUIPolishReviewAllowsDesignOnlyForUIPaths(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	run := ReviewRun{
		Mode: reviewModeUIPolish,
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"docs/assets/review-panel.css", "docs/styles/theme.css"},
		},
	}
	plan := planReviewModels(cfg, run)
	if !stringSliceContainsCI(plan.RequiredRoles, "primary_reviewer") {
		t.Fatalf("UI-only polish review should still use primary route, got %#v", plan.RequiredRoles)
	}
	if !stringSliceContainsCI(plan.RequiredLenses, "design") {
		t.Fatalf("UI-only polish review should require design lens, got %#v", plan.RequiredLenses)
	}
}

func TestExecuteReviewModelRunsUsesPrimaryRouteWithDesignLens(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "model"
	provider := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: approved",
				"summary: design-only review approved",
				"findings:",
			}, "\n")},
		}},
	}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   NewSession(root, "scripted", "model", "", "default"),
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	rt := &runtimeState{
		cfg:       cfg,
		agent:     agent,
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   agent.Session,
	}
	run := ReviewRun{
		ID:     "review-role-test",
		Mode:   reviewModeUIPolish,
		Target: reviewTargetChange,
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"docs/assets/review-panel.css"},
		},
		Evidence: ReviewEvidencePack{
			Sources: []string{"provided_diff"},
			Text:    "diff evidence",
		},
	}
	run.ModelPlan = planReviewModels(cfg, run)
	if role := reviewMainExecutionRole(run.ModelPlan); role != "primary_reviewer" {
		t.Fatalf("expected primary reviewer main role before execution, got %q plan=%#v", role, run.ModelPlan)
	}

	_, reviewerRuns := executeReviewModelRuns(context.Background(), rt, root, &run)
	if len(reviewerRuns) != 1 {
		t.Fatalf("expected one reviewer run, got %#v", reviewerRuns)
	}
	if reviewerRuns[0].Role != "primary_reviewer" {
		t.Fatalf("expected main review run to use primary route, got %#v", reviewerRuns[0])
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected one model request, got %d", len(provider.requests))
	}
	prompt := provider.requests[0].Messages[0].Text
	if !strings.Contains(prompt, "Role: primary_reviewer") || !strings.Contains(prompt, "required: correctness, design") {
		t.Fatalf("expected primary route with design lens in prompt, got %q", prompt)
	}
	if !stringSliceContainsCI(run.ModelPlan.RequiredRoles, "primary_reviewer") {
		t.Fatalf("main execution must preserve primary route, got %#v", run.ModelPlan.RequiredRoles)
	}
	if _, ok := run.ModelPlan.AssignedModels["primary_reviewer"]; !ok {
		t.Fatalf("expected main model assignment on primary route, got %#v", run.ModelPlan.AssignedModels)
	}
}

func TestUIPolishReviewRequiresPrimaryForExecutableUIPaths(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	for _, changedPath := range []string{"cmd/kernforge/ui.go", "ui.ts", "components/Button.tsx"} {
		run := ReviewRun{
			Mode: reviewModeUIPolish,
			ChangeSet: ReviewChangeSet{
				ChangedPaths: []string{changedPath},
			},
		}
		plan := planReviewModels(cfg, run)
		if !stringSliceContainsCI(plan.RequiredRoles, "primary_reviewer") {
			t.Fatalf("executable UI polish path %q should require primary reviewer, got %#v", changedPath, plan.RequiredRoles)
		}
		if !stringSliceContainsCI(plan.RequiredLenses, "design") {
			t.Fatalf("executable UI polish path %q should keep design lens, got %#v", changedPath, plan.RequiredLenses)
		}
	}
}

func TestReviewRoleReasoningEffortDefaultsToAtLeastHigh(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex-subscription"
	cfg.Model = "gpt-5.5"
	cfg.ReasoningEffort = "low"

	if got := reviewRoleReasoningEffort(cfg, "primary_reviewer"); got != "high" {
		t.Fatalf("review role should not inherit low main effort, got %q", got)
	}

	cfg.Review.RoleModels = map[string]ReviewModelConfig{
		"primary_reviewer": {
			Provider:        "deepseek",
			Model:           "deepseek-v4-pro",
			ReasoningEffort: "medium",
		},
	}
	if got := reviewRoleReasoningEffort(cfg, "primary_reviewer"); got != "high" {
		t.Fatalf("review role should raise medium configured effort to high, got %q", got)
	}
	if label, _ := reviewRoleModelLabelAndSource(cfg, configReviewHarness(cfg), "primary_reviewer"); !strings.Contains(label, "effort=high") {
		t.Fatalf("review role label should show effective high effort, got %q", label)
	}

	cfg.Review.RoleModels["primary_reviewer"] = ReviewModelConfig{
		Provider:        "deepseek",
		Model:           "deepseek-v4-pro",
		ReasoningEffort: "xhigh",
	}
	if got := reviewRoleReasoningEffort(cfg, "cross_reviewer"); got != "xhigh" {
		t.Fatalf("legacy primary review route should preserve xhigh configured effort for cross reviewer, got %q", got)
	}

	cfg.Provider = "openai-codex-subscription"
	cfg.Model = "gpt-5.5"
	cfg.ReasoningEffort = "xhigh"
	cfg.Review.RoleModels["primary_reviewer"] = ReviewModelConfig{
		Provider:        "openai-codex-subscription",
		Model:           "gpt-5.5",
		ReasoningEffort: "high",
	}
	if got := reviewRoleReasoningEffort(cfg, "primary_reviewer"); got != "xhigh" {
		t.Fatalf("primary review route should preserve higher active main effort, got %q", got)
	}

	cfg.Review.RoleModels["primary_reviewer"] = ReviewModelConfig{
		Provider:        "deepseek",
		Model:           "deepseek-v4-pro",
		ReasoningEffort: "high",
	}
	if got := reviewRoleReasoningEffort(cfg, "cross_reviewer"); got != "high" {
		t.Fatalf("legacy primary review route should keep configured cross effort, got %q", got)
	}
}

func TestFocusedPreFixBugHuntRaisesRoleEffortToHigh(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex-subscription"
	cfg.Model = "gpt-5.5"
	cfg.Review.RoleModels = map[string]ReviewModelConfig{
		"primary_reviewer": {
			Provider:        "deepseek",
			Model:           "deepseek-v4-pro",
			ReasoningEffort: "low",
		},
	}
	run := ReviewRun{
		Trigger:   reviewBeforeFixTrigger,
		Target:    reviewTargetSelection,
		Mode:      reviewModeLiveFix,
		Objective: "@SampleApp/SampleWorker/SampleReview.cpp:132-221 검토하고 버그를 수정해",
	}

	if got := reviewRoleReasoningEffortForRun(cfg, "primary_reviewer", run); got != "high" {
		t.Fatalf("focused pre-fix bug hunt should raise low reviewer effort to high, got %q", got)
	}
	cfg.Review.RoleModels = nil
	cfg.ReasoningEffort = "xhigh"
	if got := reviewRoleReasoningEffortForRun(cfg, "primary_reviewer", run); got != "xhigh" {
		t.Fatalf("focused pre-fix bug hunt should preserve explicit xhigh main effort, got %q", got)
	}
	cfg.Review.RoleModels = map[string]ReviewModelConfig{
		"primary_reviewer": {
			Provider:        "deepseek",
			Model:           "deepseek-v4-pro",
			ReasoningEffort: "low",
		},
	}
	cfg.Review.RoleModels["primary_reviewer"] = ReviewModelConfig{
		Provider:        "deepseek",
		Model:           "deepseek-v4-pro",
		ReasoningEffort: "xhigh",
	}
	if got := reviewRoleReasoningEffortForRun(cfg, "primary_reviewer", run); got != "xhigh" {
		t.Fatalf("focused pre-fix bug hunt should preserve xhigh reviewer effort, got %q", got)
	}
	cfg.Provider = "openai-codex-subscription"
	cfg.Model = "gpt-5.5"
	cfg.ReasoningEffort = "xhigh"
	cfg.Review.RoleModels["primary_reviewer"] = ReviewModelConfig{
		Provider:        "openai-codex-subscription",
		Model:           "gpt-5.5",
		ReasoningEffort: "high",
	}
	if got := reviewRoleReasoningEffortForRun(cfg, "primary_reviewer", run); got != "xhigh" {
		t.Fatalf("focused pre-fix bug hunt should not downgrade same-route xhigh main effort, got %q", got)
	}
}
