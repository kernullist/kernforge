package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFinishHarnessBlockedWithDisclosureCompletesWhenOnlyOverclaimRemains(t *testing.T) {
	root := t.TempDir()
	candidate := strings.TrimSpace(`
Changed files: main.go
Review result: approved for the current change.
Validation: build and tests passed.
Remaining risk: none known.
`)

	session := NewSession(root, "scripted", "model", "", "default")
	session.PendingHarnessBlockedRecovery = &HarnessBlockedRecovery{
		RecordedAt:        time.Now(),
		CandidateReply:    candidate,
		AttemptedEditTool: true,
		PrimaryCommand:    "/finish --disclose",
		Actions: []HarnessRecoveryAction{{
			ID:      "finish-disclose",
			Command: "/finish --disclose",
			Kind:    harnessRecoveryActionDisclose,
		}},
	}
	agent := &Agent{
		Config:  DefaultConfig(root),
		Session: session,
		Store:   NewSessionStore(root),
		Workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	healed := retractVerificationOverclaimForDisclosure(candidate)
	if !strings.Contains(healed, "Validation: verification was not run.") {
		t.Fatalf("expected overclaim retraction helper to append disclosure, got %q", healed)
	}

	reply, err := agent.finishHarnessBlockedWithDisclosure()
	if err != nil {
		if !strings.Contains(reply, "Validation: verification was not run.") &&
			!strings.Contains(reply, "Choose a next step:") &&
			!strings.Contains(reply, "다음 단계를 고르세요:") &&
			!strings.Contains(reply, "Completion is blocked") {
			t.Fatalf("finish disclosure failed unexpectedly: err=%v reply=%q", err, reply)
		}
		return
	}
	if !strings.Contains(reply, "Validation: verification was not run.") {
		t.Fatalf("expected disclosure finish to retract verification overclaim, got %q", reply)
	}
	if session.PendingHarnessBlockedRecovery != nil {
		t.Fatalf("successful finish should clear pending recovery")
	}
}

func TestLooksLikeHarnessRecoveryIntents(t *testing.T) {
	if !looksLikeHarnessDiscloseFinishIntent("검증 미실행으로 끝내줘") {
		t.Fatal("expected Korean disclose intent")
	}
	if !looksLikeHarnessRetryVerifyIntent("retry verification and finish") {
		t.Fatal("expected retry verify intent")
	}
	if !looksLikeHarnessRepairContinueIntent("남은 blocker를 수정해") {
		t.Fatal("expected repair continue intent")
	}
	if looksLikeHarnessRepairContinueIntent("retry verification") {
		t.Fatal("retry verify must not classify as repair continue")
	}
}

func TestMatchHarnessRecoveryInputSelectsByNumber(t *testing.T) {
	recovery := HarnessBlockedRecovery{
		Actions: []HarnessRecoveryAction{
			{ID: "continue-repair", Command: "/continue", Kind: harnessRecoveryActionRepair},
			{ID: "switch-model", Command: "/model", Kind: harnessRecoveryActionModel},
			{ID: "status-detail", Command: "/status detail", Kind: harnessRecoveryActionStatus},
		},
	}
	action, ok := matchHarnessRecoveryInput(Config{}, recovery, "2")
	if !ok || action.Kind != harnessRecoveryActionModel {
		t.Fatalf("expected model action for input 2, got ok=%v action=%#v", ok, action)
	}
	action, ok = matchHarnessRecoveryInput(Config{}, recovery, "/continue")
	if !ok || action.Kind != harnessRecoveryActionRepair {
		t.Fatalf("expected repair action for /continue, got ok=%v action=%#v", ok, action)
	}
}

func TestBuildStallBlockedRecoveryIncludesContinueModelStatus(t *testing.T) {
	recovery := buildStallBlockedRecovery(Config{}, harnessRecoveryCauseReadChurn, "stopped", []string{"merge_mp4_gui.py"})
	if recovery.Cause != harnessRecoveryCauseReadChurn {
		t.Fatalf("cause=%q", recovery.Cause)
	}
	if recovery.PrimaryCommand != "/continue" {
		t.Fatalf("primary=%q", recovery.PrimaryCommand)
	}
	if len(recovery.Actions) < 3 {
		t.Fatalf("expected continue/model/status actions, got %#v", recovery.Actions)
	}
}

func TestStallContinuePromptDoesNotRouteToRecentErrorExplainer(t *testing.T) {
	legacy := "이전 턴은 같은 파일을 진전 없이 반복해서 읽어 중단되었습니다. 지금 사용자 작업을 이어서 진행하세요. 마지막 shell/compile 출력에 문법/실행 오류가 있으면 그 오류부터 focused edit로 고치세요."
	if classifyTurnIntent(legacy) != TurnIntentContinueLastTask {
		t.Fatalf("legacy stall prompt intent=%q", classifyTurnIntent(legacy))
	}
	currentKO := "이전 턴은 같은 파일을 진전 없이 반복해서 읽어 중단되었습니다. 지금 사용자의 원래 작업을 이어서 진행하세요. 이미 읽은 파일을 다시 읽기보다 write_file, replace_in_file, apply_patch로 수정하세요. 마지막 shell/compile 출력에 문법 문제가 있으면 그 위치부터 focused 수정으로 고치고, 검증 상태는 정직하게 밝히며 마무리하세요."
	if classifyTurnIntent(currentKO) != TurnIntentContinueLastTask {
		t.Fatalf("stall prompt intent=%q", classifyTurnIntent(currentKO))
	}
	currentEN := stallContinueRecoveryPrompt(Config{}, harnessRecoveryCauseReadChurn)
	if classifyTurnIntent(currentEN) != TurnIntentContinueLastTask {
		t.Fatalf("english stall prompt intent=%q", classifyTurnIntent(currentEN))
	}
	agent := &Agent{Session: &Session{}}
	for _, prompt := range []string{legacy, currentKO, currentEN} {
		if _, ok := agent.maybeAnswerRecentErrorQuestion(prompt); ok {
			t.Fatalf("stall continue must not be answered by the recent-error explainer: %q", prompt)
		}
	}
	for _, cause := range []string{
		harnessRecoveryCauseRepeatedToolCalls,
		harnessRecoveryCauseRepeatedToolFailure,
		harnessRecoveryCauseToolLoopLimit,
		harnessRecoveryCauseEmptyStop,
		harnessRecoveryCauseFinalGate,
	} {
		prompt := stallContinueRecoveryPrompt(Config{}, cause)
		if classifyTurnIntent(prompt) != TurnIntentContinueLastTask {
			t.Fatalf("stall continue for %s intent=%q prompt=%q", cause, classifyTurnIntent(prompt), prompt)
		}
		if _, ok := agent.maybeAnswerRecentErrorQuestion(prompt); ok {
			t.Fatalf("stall continue for %s must not hit recent-error explainer", cause)
		}
	}
}

func TestCollectRecentRepairEvidenceFindsSyntaxFailure(t *testing.T) {
	session := &Session{
		Messages: []Message{
			{Role: "tool", ToolName: "run_shell", IsError: true, Text: `File "merge_mp4_gui.py", line 747\n    if line and\nSyntaxError: invalid syntax`},
		},
		PendingHarnessBlockedRecovery: &HarnessBlockedRecovery{
			BlockerTitles: []string{`c:/git/make-one-video/merge_mp4_gui.py`},
		},
	}
	evidence := collectRecentRepairEvidence(session, 12)
	if evidence.Kind != "syntax" {
		t.Fatalf("expected syntax kind, got %#v", evidence)
	}
	if !strings.Contains(evidence.Detail, "SyntaxError") {
		t.Fatalf("expected SyntaxError detail, got %q", evidence.Detail)
	}
	if len(evidence.Files) == 0 {
		t.Fatalf("expected files from error/pending recovery, got %#v", evidence.Files)
	}
}

func TestBuildStallContinueRecoveryPromptIncludesEvidenceAndEditBias(t *testing.T) {
	session := &Session{
		Messages: []Message{
			{Role: "tool", ToolName: "run_shell", IsError: true, Text: "python -m py_compile merge_mp4_gui.py\nSyntaxError: invalid syntax"},
		},
		PendingHarnessBlockedRecovery: &HarnessBlockedRecovery{
			Cause:         harnessRecoveryCauseReadChurn,
			BlockerTitles: []string{"merge_mp4_gui.py"},
		},
		AcceptanceContract: &AcceptanceContract{SourcePrompt: "UX를 만들고 개선하자"},
	}
	prompt := buildStallContinueRecoveryPrompt(Config{}, session, harnessRecoveryCauseReadChurn)
	if !strings.Contains(prompt, "Operator continue is active") && !strings.Contains(prompt, "운영자 continue") {
		t.Fatalf("expected edit-bias guidance, got %q", prompt)
	}
	if !strings.Contains(prompt, "SyntaxError") {
		t.Fatalf("expected recorded syntax failure, got %q", prompt)
	}
	if !strings.Contains(prompt, "merge_mp4_gui.py") {
		t.Fatalf("expected stall file in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "UX를 만들고 개선하자") {
		t.Fatalf("expected original request, got %q", prompt)
	}
	if classifyTurnIntent(prompt) != TurnIntentContinueLastTask {
		t.Fatalf("enriched continue prompt intent=%q", classifyTurnIntent(prompt))
	}
	agent := &Agent{Session: session}
	if _, ok := agent.maybeAnswerRecentErrorQuestion(prompt); ok {
		t.Fatal("enriched continue must not hit recent-error explainer")
	}
}

func TestReadChurnEscalationPrefersDefectOverBroadClarify(t *testing.T) {
	session := &Session{
		Messages: []Message{
			{Role: "user", Text: "UX를 만들고 개선하자"},
			{Role: "tool", ToolName: "run_shell", IsError: true, Text: "SyntaxError: invalid syntax"},
		},
	}
	agent := &Agent{Config: Config{}, Session: session}
	reply := agent.readChurnEscalationReply(map[string]struct{}{"merge_mp4_gui.py": {}})
	if strings.Contains(reply, "원하시는 바를 명확히") || strings.Contains(reply, "Please clarify what you want") {
		t.Fatalf("must not ask broad clarification when syntax defect exists: %q", reply)
	}
	if !strings.Contains(reply, "SyntaxError") && !strings.Contains(reply, "구체적 결함") && !strings.Contains(reply, "Concrete defect") {
		t.Fatalf("expected defect-aware escalation, got %q", reply)
	}
}

func TestClassifyAssistantStallError(t *testing.T) {
	cases := []struct {
		err   error
		cause string
	}{
		{fmt.Errorf("stopped after repeated identical tool calls"), harnessRecoveryCauseRepeatedToolCalls},
		{fmt.Errorf("stopped after repeatedly reading the same file without making progress: foo.go"), harnessRecoveryCauseReadChurn},
		{fmt.Errorf("stopped after repeatedly cycling read_file across paths"), harnessRecoveryCauseReadChurn},
		{fmt.Errorf("stopped after repeated tool failure: boom"), harnessRecoveryCauseRepeatedToolFailure},
		{fmt.Errorf("tool loop limit exceeded (last_tools=list_files)"), harnessRecoveryCauseToolLoopLimit},
		{fmt.Errorf("model produced commentary-only assistant messages without tool calls or final answer"), harnessRecoveryCauseCommentaryOnly},
		{fmt.Errorf("model stopped before producing a usable response due to token limit (stop_reason=length)"), harnessRecoveryCauseLengthStop},
		{fmt.Errorf("provider content filter blocked this response (stop_reason=content_filter)"), harnessRecoveryCauseContentFilter},
		{fmt.Errorf("model returned an empty response (provider=x model=y)"), harnessRecoveryCauseEmptyStop},
		{fmt.Errorf("Held the final answer - fix blockers [final_gate intervention: unresolved]"), harnessRecoveryCauseFinalGate},
		{fmt.Errorf("Stop hook kept blocking final answer after 3 continuation attempt(s): no"), harnessRecoveryCauseFinalGate},
		{fmt.Errorf("network dial timeout"), ""},
	}
	for _, tc := range cases {
		cause, _ := classifyAssistantStallError(tc.err)
		if cause != tc.cause {
			t.Fatalf("err=%v cause=%q want %q", tc.err, cause, tc.cause)
		}
	}
}

func TestMaybeOfferStallRecoveryFromAssistantErrorSynthesizesPending(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	var out strings.Builder
	rt := &runtimeState{
		cfg:         DefaultConfig(root),
		interactive: true,
		writer:      &out,
		ui:          NewUI(),
		session:     session,
		store:       NewSessionStore(root),
		// Non-interactive choice path: offeringHarnessRecovery stays false but
		// promptUserChoice is skipped when stdin is unavailable; we only assert
		// pending recovery synthesis here.
		offeringHarnessRecovery: true,
	}
	rt.maybeOfferStallRecoveryFromAssistantError(context.Background(), fmt.Errorf("tool loop limit exceeded (last_tools=list_files)"))
	if session.PendingHarnessBlockedRecovery == nil || session.PendingHarnessBlockedRecovery.Cause != harnessRecoveryCauseToolLoopLimit {
		t.Fatalf("expected pending tool-loop recovery, got %#v", session.PendingHarnessBlockedRecovery)
	}
	if !strings.Contains(out.String(), "Choose a next step:") && !strings.Contains(out.String(), "다음 단계를 고르세요:") {
		t.Fatalf("expected recovery card printed, got %q", out.String())
	}
}

func TestOfferHarnessBlockedRecoveryChoiceExecutesSelection(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	candidate := "Changed files: main.go\nReview result: approved.\nValidation: build and tests passed.\nRemaining risk: none known."
	session.PendingHarnessBlockedRecovery = &HarnessBlockedRecovery{
		CandidateReply: candidate,
		PrimaryCommand: "/finish --disclose",
		Actions: []HarnessRecoveryAction{{
			ID:      "finish-disclose",
			Command: "/finish --disclose",
			Kind:    harnessRecoveryActionDisclose,
			TitleEN: "Finish with honest disclosure",
			TitleKO: "정직한 공개로 끝내기",
		}},
	}
	rt := &runtimeState{
		cfg:         DefaultConfig(root),
		interactive: true,
		writer:      &strings.Builder{},
		ui:          NewUI(),
		session:     session,
		store:       NewSessionStore(root),
		agent: &Agent{
			Config:    DefaultConfig(root),
			Session:   session,
			Store:     NewSessionStore(root),
			Workspace: Workspace{BaseRoot: root, Root: root},
		},
	}
	recovery := *session.PendingHarnessBlockedRecovery
	q := harnessBlockedRecoveryUserQuestion(rt.cfg, recovery)
	if len(q.Options) != 1 || q.Options[0].Label != "finish-disclose" {
		t.Fatalf("unexpected question options: %#v", q.Options)
	}
	action, ok := resolveHarnessRecoveryAction(recovery, UserQuestionResult{Selected: []string{"finish-disclose"}})
	if !ok || action.Kind != harnessRecoveryActionDisclose {
		t.Fatalf("resolve failed: ok=%v action=%#v", ok, action)
	}
	reply, err := rt.executeHarnessRecoveryAction(context.Background(), action)
	if err != nil && !strings.Contains(reply, "Validation: verification was not run.") &&
		!strings.Contains(reply, "Completion is blocked") &&
		!strings.Contains(reply, "Choose a next step") {
		t.Fatalf("execute disclose: err=%v reply=%q", err, reply)
	}
	if err == nil && !strings.Contains(reply, "verification was not run") {
		t.Fatalf("expected disclosure finish text, got %q", reply)
	}
}

func TestAgentForcesEditGuidanceAfterSyntaxFailureAndBlocksReread(t *testing.T) {
	root := t.TempDir()
	broken := "def broken():\n    if line and\n"
	if err := os.WriteFile(filepath.Join(root, "merge_mp4_gui.py"), []byte(broken), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("run_shell", map[string]any{"command": "python -m py_compile merge_mp4_gui.py"}),
			toolCallResponse("read_file", map[string]any{"path": "merge_mp4_gui.py", "start_line": 1, "end_line": 20}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Fixed the syntax error with a focused edit. Validation: verification was not run. Remaining risk: incomplete UX polish.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "full")
	session.StallContinueEditBias = true
	session.PendingHarnessBlockedRecovery = &HarnessBlockedRecovery{
		Cause:         harnessRecoveryCauseReadChurn,
		BlockerTitles: []string{"merge_mp4_gui.py"},
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(NewRunShellTool(ws), NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "UX를 만들고 개선하자")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected at least two model requests, got %d", len(provider.requests))
	}
	foundForceEdit := false
	foundRedirect := false
	for _, req := range provider.requests[1:] {
		for _, msg := range req.Messages {
			text := strings.TrimSpace(msg.Text)
			if strings.Contains(text, "compile/syntax failure") || strings.Contains(text, "컴파일/문법 실패") {
				foundForceEdit = true
			}
			if strings.Contains(text, "NOT_EXECUTED") && strings.Contains(strings.ToLower(text), "edit") {
				foundRedirect = true
			}
			if msg.Role == "tool" && strings.Contains(text, "NOT_EXECUTED") {
				foundRedirect = true
			}
		}
	}
	if !foundForceEdit {
		t.Fatalf("expected syntax force-edit guidance after py_compile failure, requests=%d reply=%q", len(provider.requests), reply)
	}
	if !foundRedirect {
		t.Fatalf("expected read_file to be redirected after syntax failure, reply=%q", reply)
	}
}
