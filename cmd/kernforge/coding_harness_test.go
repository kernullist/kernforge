package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchTransactionRecordsWriteFileAndFinalizes(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "main.go",
				"content": "package main\n",
			}),
			{Message: Message{Role: "assistant", Text: "Changed files: main.go. Self-review: no code blocker found. Validation: verification not run. Remaining risk: no known remaining blocker."}},
		},
	}
	cfg := DefaultConfig(root)
	cfg.AutoVerify = boolPtr(false)
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "make the change")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Changed files: main.go") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if session.ActivePatchTransaction != nil {
		t.Fatalf("expected active patch transaction to be archived after final answer")
	}
	if len(session.PatchTransactions) != 1 {
		t.Fatalf("expected one archived patch transaction, got %#v", session.PatchTransactions)
	}
	tx := session.PatchTransactions[0]
	if tx.Status != patchTransactionStatusCommitted {
		t.Fatalf("expected committed transaction, got %#v", tx)
	}
	if len(tx.Entries) != 1 || tx.Entries[0].ToolName != "write_file" {
		t.Fatalf("expected write_file entry, got %#v", tx.Entries)
	}
	if len(tx.Entries[0].Paths) != 1 {
		t.Fatalf("expected one path change, got %#v", tx.Entries[0].Paths)
	}
	change := tx.Entries[0].Paths[0]
	if change.Path != "main.go" || change.Operation != "create" {
		t.Fatalf("unexpected path change: %#v", change)
	}
	if change.Before.Exists || !change.After.Exists || change.After.SHA256 == "" {
		t.Fatalf("expected missing-before and hashed-after fingerprints, got %#v", change)
	}
}

func TestCodingHarnessSourcePromptSkipsInternalReviewerFeedback(t *testing.T) {
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 별도 문서로 생성해"
	session := &Session{
		Messages: []Message{
			{Role: "user", Text: original},
			{Role: "user", Text: "Reviewer feedback: the proposed final answer is not ready yet. Revise before concluding."},
		},
	}

	if got := codingHarnessSourcePrompt(session); got != original {
		t.Fatalf("expected source prompt to preserve original user request, got %q", got)
	}
}

func TestCodingHarnessSourcePromptUsesFreshExternalTaskOverStaleContract(t *testing.T) {
	stale := "SampleGame/BugReport.md 보고서를 생성해"
	fresh := "RuntimeManager.cpp 버그를 수정해"
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: stale}
	session.TaskState = &TaskState{Goal: stale}
	session.Messages = []Message{
		{Role: "user", Text: stale},
		{Role: "assistant", Text: "보고서를 생성했습니다."},
		{Role: "user", Text: fresh},
	}

	if got := codingHarnessSourcePrompt(session); got != fresh {
		t.Fatalf("expected fresh external request to override stale contract, got %q", got)
	}
}

func TestCodingHarnessSourcePromptPreservesContractForControlFollowup(t *testing.T) {
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 별도 문서로 생성해"
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: original}
	session.TaskState = &TaskState{Goal: original}
	session.Messages = []Message{
		{Role: "user", Text: original},
		{Role: "assistant", Text: "보고서를 생성했습니다."},
		{Role: "user", Text: "최종 답변 줘"},
	}

	if got := codingHarnessSourcePrompt(session); got != original {
		t.Fatalf("expected control follow-up to preserve contract prompt, got %q", got)
	}
}

func TestLatestExternalOrUserMessageTextSkipsInternalSteering(t *testing.T) {
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 별도 문서로 생성해"
	messages := []Message{
		{Role: "user", Text: original},
		{Role: "user", Text: "This is a generated document artifact turn. Do not run shell validation."},
		{Role: "user", Text: "Reviewer feedback: revise the final answer before concluding."},
	}

	if got := latestExternalOrUserMessageText(messages); got != original {
		t.Fatalf("expected latest external user message, got %q", got)
	}
}

func TestLatestExternalUserMessageTextPrefersSourceText(t *testing.T) {
	original := "검토해서 @SampleGame.cpp 보고서를 작성해"
	enriched := strings.Join([]string{
		"검토해서 SampleGame/SampleGame.cpp 보고서를 작성해",
		"",
		"Attached context:",
		"Referenced file: F:\\repo\\SampleGame\\SampleGame.cpp",
		"```",
		"int main() { return 0; }",
		"```",
		"",
		"Request mode: analysis-only.",
	}, "\n")
	externalText, injectedContext := splitInjectedPromptContext(enriched)
	messages := []Message{
		{Role: "user", Text: externalText, SourceText: original},
		internalUserMessage("Additional turn context for the preceding user request:\n" + injectedContext),
	}

	if got := latestExternalOrUserMessageText(messages); got != original {
		t.Fatalf("expected source text to preserve the original user request, got %q", got)
	}
	if strings.Contains(externalText, "Attached context") || strings.Contains(externalText, "Request mode") {
		t.Fatalf("expected external text to exclude injected context, got %q", externalText)
	}
	if !strings.Contains(injectedContext, "Attached context:") || !strings.Contains(injectedContext, "Request mode: analysis-only.") {
		t.Fatalf("expected injected context to be separated, got %q", injectedContext)
	}
}

func TestLatestExternalOrUserMessageTextSkipsAgentLoopInternalGuidance(t *testing.T) {
	original := "Fix the runtime gate loop"
	internalMessages := []string{
		"Pre-final coding harness found issues that require revising only the final answer.\nDo not call tools.",
		"Runtime gate ledger blocked final_answer. Resolve the blockers before continuing.",
		"Generated document artifact finalization is answer-only now. The artifact content has already passed deterministic content checks.",
		"The last read-only inspection tool was blocked by editable ownership routing. This is not a stale patch problem.",
		"You have already made multiple rounds of edits. Do not call more edit tools unless the previous changes are clearly insufficient.",
		"Your last response was a raw internal REVIEW_RESULT block. Do not expose review harness result syntax to the user as the final answer.",
		"Please provide the final answer now.",
	}
	for _, internal := range internalMessages {
		messages := []Message{
			{Role: "user", Text: original},
			{Role: "user", Text: internal},
		}
		if got := latestExternalOrUserMessageText(messages); got != original {
			t.Fatalf("expected latest external request to skip internal guidance %q, got %q", internal, got)
		}
	}
}

func TestLatestExternalOrUserMessageTextSkipsStructuredInternalGuidance(t *testing.T) {
	original := "Fix the runtime gate loop"
	messages := []Message{
		{Role: "user", Text: original},
		internalUserMessage("Do not stage, commit, push, or open a PR unless the user explicitly asks for a git action first."),
		internalUserMessage("This internal recovery note intentionally looks like a normal user instruction."),
	}

	if got := latestExternalOrUserMessageText(messages); got != original {
		t.Fatalf("expected latest external request to skip structured internal guidance, got %q", got)
	}
}

func TestLatestExternalOrUserMessageTextDoesNotFallbackToInternalOnly(t *testing.T) {
	messages := []Message{
		internalUserMessage("Additional turn context for the preceding user request:\nRequest mode: inspect-and-fix."),
		internalUserMessage("Reviewer feedback: revise the final answer before concluding."),
	}

	if got := latestExternalOrUserMessageText(messages); got != "" {
		t.Fatalf("expected internal-only user messages to stay invisible as user requests, got %q", got)
	}
}

func TestSessionEffectiveUserRequestTextFallsBackToAcceptanceContext(t *testing.T) {
	original := "RuntimeManager.cpp 버그를 수정해"
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: original}
	session.TaskState = &TaskState{Goal: original}
	session.AddMessage(internalUserMessage("Additional turn context for the preceding user request:\nRequest mode: inspect-and-fix."))

	if got := sessionEffectiveUserRequestText(session); got != original {
		t.Fatalf("expected acceptance context fallback for internal-only continuation, got %q", got)
	}
}

func TestSessionEffectiveUserRequestTextPreservesActionContextForContinuation(t *testing.T) {
	original := "Codex upstream과 kernforge를 비교해서 turn orchestration을 수정해"
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: original}
	session.TaskState = &TaskState{Goal: original}
	session.Messages = []Message{
		{Role: "user", Text: original},
		{Role: "assistant", Text: "1차 provider role 분리를 적용했습니다."},
		{Role: "user", Text: "좋아 너무 작은 기능까지 먼저 확인하지 말고 전체적인 큰 흐름과 관련된 것들 위주로 먼저 확인하자"},
	}

	if got := sessionEffectiveUserRequestText(session); got != original {
		t.Fatalf("expected continuation steering to preserve effective action request %q, got %q", original, got)
	}
}

func TestSessionEffectiveUserRequestTextKeepsStatusQuestionAsLatestRequest(t *testing.T) {
	original := "Codex upstream과 kernforge를 비교해서 turn orchestration을 수정해"
	status := "지금 몇 % 정도 작업 완료된 것 같아?"
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: original}
	session.TaskState = &TaskState{Goal: original}
	session.Messages = []Message{
		{Role: "user", Text: original},
		{Role: "assistant", Text: "1차 provider role 분리를 적용했습니다."},
		{Role: "user", Text: status},
	}

	if got := sessionEffectiveUserRequestText(session); got != status {
		t.Fatalf("expected status question to remain the effective latest request, got %q", got)
	}
	if got := codingHarnessSourcePrompt(session); got != original {
		t.Fatalf("expected coding harness source prompt to keep original task for status follow-up, got %q", got)
	}
}

func TestSourcePromptAndPatchGoalDoNotFallbackToInternalOnly(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.AddMessage(internalUserMessage("Additional turn context for the preceding user request:\nRequest mode: inspect-and-fix."))
	session.AddMessage(internalUserMessage("Reviewer feedback: revise the final answer before concluding."))

	if got := codingHarnessSourcePrompt(session); got != "" {
		t.Fatalf("expected internal-only context not to become source prompt, got %q", got)
	}
	if got := patchTransactionGoalFromSession(session); got != "" {
		t.Fatalf("expected internal-only context not to become patch transaction goal, got %q", got)
	}
}

func TestSessionAddMessageMarksKnownInternalGuidance(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "Fix the runtime gate loop"})
	session.AddMessage(Message{Role: "user", Text: "Reviewer feedback: revise the final answer before concluding."})

	if len(session.Messages) != 2 {
		t.Fatalf("expected two messages, got %d", len(session.Messages))
	}
	if !session.Messages[1].Internal {
		t.Fatalf("expected known internal feedback to be marked internal: %#v", session.Messages[1])
	}
	if got := latestExternalOrUserMessageText(session.Messages); got != "Fix the runtime gate loop" {
		t.Fatalf("expected latest external request, got %q", got)
	}
}

func TestSessionAddMessageMarksAdditionalTurnContextInternal(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "Fix the runtime gate loop"})
	session.AddMessage(Message{Role: "user", Text: "Additional turn context for the preceding user request:\nRequest mode: inspect-and-fix."})

	if len(session.Messages) != 2 {
		t.Fatalf("expected two messages, got %d", len(session.Messages))
	}
	if !session.Messages[1].Internal {
		t.Fatalf("expected additional turn context to be marked internal: %#v", session.Messages[1])
	}
	if got := latestExternalOrUserMessageText(session.Messages); got != "Fix the runtime gate loop" {
		t.Fatalf("expected latest external request, got %q", got)
	}
}

func TestGenericFinalAnswerPromptPreservesAcceptanceContext(t *testing.T) {
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 별도 문서로 생성해"
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: original}
	session.TaskState = &TaskState{Goal: original}
	session.AddMessage(Message{Role: "user", Text: original})

	agent := &Agent{Session: session}
	if agent.shouldStartNewExternalAcceptanceContext("Please provide the final answer now.") {
		t.Fatalf("generic finalization prompt should preserve existing acceptance context")
	}

	session.AddMessage(Message{Role: "user", Text: "Please provide the final answer now."})
	if !session.Messages[len(session.Messages)-1].Internal {
		t.Fatalf("expected generic finalization prompt to be stored as internal guidance")
	}
	if got := latestExternalOrUserMessageText(session.Messages); got != original {
		t.Fatalf("expected latest external request to remain original, got %q", got)
	}
}

func TestKoreanFinalAnswerPromptPreservesAcceptanceContext(t *testing.T) {
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 별도 문서로 생성해"
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: original}
	session.TaskState = &TaskState{Goal: original}
	session.AddMessage(Message{Role: "user", Text: original})

	agent := &Agent{Session: session}
	if agent.shouldStartNewExternalAcceptanceContext("최종 답변 줘") {
		t.Fatalf("korean finalization prompt should preserve existing acceptance context")
	}

	session.AddMessage(Message{Role: "user", Text: "최종 답변 줘"})
	if !session.Messages[len(session.Messages)-1].Internal {
		t.Fatalf("expected korean finalization prompt to be stored as internal guidance")
	}
	if got := latestExternalOrUserMessageText(session.Messages); got != original {
		t.Fatalf("expected latest external request to remain original, got %q", got)
	}
}

func TestControlFollowupsPreservePatchTransactionGoal(t *testing.T) {
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 별도 문서로 생성해"
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: original}
	session.TaskState = &TaskState{Goal: original}
	session.Messages = []Message{
		{Role: "user", Text: original},
		{Role: "assistant", Text: "보고서를 생성했습니다."},
	}
	agent := &Agent{Session: session}

	for _, followup := range []string{
		"최종 답변 줘",
		"지금 몇 % 정도 작업 완료된 것 같아?",
		"계속 진행해",
	} {
		if agent.shouldStartNewExternalAcceptanceContext(followup) {
			t.Fatalf("control follow-up %q should preserve existing acceptance context", followup)
		}
		session.Messages = append(session.Messages, Message{Role: "user", Text: followup})
		if got := patchTransactionGoalFromSession(session); got != original {
			t.Fatalf("expected control follow-up %q to preserve patch transaction goal %q, got %q", followup, original, got)
		}
		session.Messages = session.Messages[:len(session.Messages)-1]
	}
}

func TestControlFollowupsPreservePatchTransactionGoalWithoutContract(t *testing.T) {
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 별도 문서로 생성해"
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.Messages = []Message{
		{Role: "user", Text: original},
		{Role: "assistant", Text: "보고서를 생성했습니다."},
	}
	if got := preservableSessionAcceptancePrompt(session); got != original {
		t.Fatalf("expected prior user request to be preservable before follow-up, got %q", got)
	}

	for _, followup := range []string{
		"지금 몇 % 정도 작업 완료된 것 같아?",
		"계속 진행해",
		"좋아 너무 작은 기능까지 먼저 확인하지 말고 전체적인 큰 흐름과 관련된 것들 위주로 먼저 확인하자",
		"문서 산출에 관해서만 검토하지 말고 모든 영역을 검토해야 해. 잊지마",
		"좁게만 수정하려고 하지 말고 근본적으로 개선해야 해",
	} {
		session.Messages = append(session.Messages, Message{Role: "user", Text: followup})
		if got := preservableSessionAcceptancePrompt(session); got != original {
			t.Fatalf("expected prior user request to be preservable after follow-up %q, got %q", followup, got)
		}
		if got := patchTransactionGoalFromSession(session); got != original {
			t.Fatalf("expected control follow-up %q to recover prior patch transaction goal %q without contract, got %q", followup, original, got)
		}
		session.Messages = session.Messages[:len(session.Messages)-1]
	}
}

func TestAcceptanceContextPreservingControlRequestRecognizesKoreanFollowups(t *testing.T) {
	for _, followup := range []string{
		"최종 답변 줘",
		"지금 몇 % 정도 작업 완료된 것 같아?",
		"계속 진행해",
		"좋아 너무 작은 기능까지 먼저 확인하지 말고 전체적인 큰 흐름과 관련된 것들 위주로 먼저 확인하자",
		"문서 산출에 관해서만 검토하지 말고 모든 영역을 검토해야 해. 잊지마",
		"좁게만 수정하려고 하지 말고 근본적으로 개선해야 해",
	} {
		if !acceptanceContextPreservingControlRequest(followup) {
			t.Fatalf("expected %q to preserve acceptance context", followup)
		}
	}
}

func TestPatchTransactionGoalUsesFreshExternalTaskOverStaleContext(t *testing.T) {
	stale := "SampleGame/BugReport.md 보고서를 생성해"
	fresh := "RuntimeManager.cpp 버그를 수정해"
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: stale}
	session.TaskState = &TaskState{Goal: stale}
	session.Messages = []Message{
		{Role: "user", Text: stale},
		{Role: "assistant", Text: "보고서를 생성했습니다."},
		{Role: "user", Text: fresh},
	}

	if got := patchTransactionGoalFromSession(session); got != fresh {
		t.Fatalf("expected fresh external edit task to become patch transaction goal, got %q", got)
	}
}

func TestPreWriteReviewUserRequestSkipsStructuredInternalGuidance(t *testing.T) {
	original := "Fix the runtime gate loop"
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.Messages = []Message{
		{Role: "user", Text: original},
		internalUserMessage("Do not stage, commit, push, or open a PR unless the user explicitly asks for a git action first."),
		{Role: "assistant", Text: "This assistant text should never become the review request."},
	}

	if got := preWriteReviewUserRequest(session); got != original {
		t.Fatalf("expected pre-write request to skip structured internal guidance, got %q", got)
	}
}

func TestGeneratedDocumentCandidatesSkipStructuredInternalAndNonUserMessages(t *testing.T) {
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성해"
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	session.Messages = []Message{
		{Role: "user", Text: original},
		internalUserMessage("This internal note intentionally looks like a generated document request."),
		{Role: "assistant", Text: "assistant generated document summary"},
	}

	candidates := generatedDocumentArtifactRequestCandidates(session, "")
	if len(candidates) != 1 || candidates[0] != original {
		t.Fatalf("expected only the external document request, got %#v", candidates)
	}
}

func TestCurrentTurnPatchTransactionSurvivesAgentLoopInternalGuidance(t *testing.T) {
	root := t.TempDir()
	original := "Fix the runtime gate loop"
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{
		{Role: "user", Text: original},
		{Role: "user", Text: "Pre-final coding harness found issues that require revising only the final answer.\nDo not call tools."},
	}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-001",
		Goal:   original,
		Status: patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ToolName: "apply_patch",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "cmd/kernforge/agent.go",
				Operation: "apply_patch",
			}},
		}},
	}

	changed := currentTurnPatchTransactionChangedPaths(session)
	if len(changed) != 1 || changed[0] != "cmd/kernforge/agent.go" {
		t.Fatalf("expected current-turn patch transaction to survive internal guidance, got %#v", changed)
	}
}

func TestCurrentTurnPatchTransactionSurvivesStructuredInternalGuidance(t *testing.T) {
	root := t.TempDir()
	original := "Fix the runtime gate loop"
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{
		{Role: "user", Text: original},
		internalUserMessage("Do not stage, commit, push, or open a PR unless the user explicitly asks for a git action first."),
	}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-001",
		Goal:   original,
		Status: patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ToolName: "apply_patch",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "cmd/kernforge/agent.go",
				Operation: "apply_patch",
			}},
		}},
	}

	changed := currentTurnPatchTransactionChangedPaths(session)
	if len(changed) != 1 || changed[0] != "cmd/kernforge/agent.go" {
		t.Fatalf("expected current-turn patch transaction to survive structured internal guidance, got %#v", changed)
	}
}

func TestCurrentTurnPatchTransactionSurvivesContinuationWithoutContract(t *testing.T) {
	root := t.TempDir()
	original := "Codex repo와 비교해서 turn orchestration의 큰 흐름을 수정해"
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{
		{Role: "user", Text: original},
		{Role: "assistant", Text: "핵심 흐름을 확인하고 있습니다."},
		{Role: "user", Text: "좋아 너무 작은 기능까지 먼저 확인하지 말고 전체적인 큰 흐름과 관련된 것들 위주로 먼저 확인하자"},
	}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-001",
		Goal:   original,
		Status: patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ToolName: "apply_patch",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "cmd/kernforge/agent.go",
				Operation: "apply_patch",
			}},
		}},
	}

	changed := currentTurnPatchTransactionChangedPaths(session)
	if len(changed) != 1 || changed[0] != "cmd/kernforge/agent.go" {
		t.Fatalf("expected continuation steering to preserve current patch transaction without contract, got %#v", changed)
	}
}

func TestCurrentTurnPatchTransactionDoesNotTreatBroaderDocumentSteeringAsCurrent(t *testing.T) {
	root := t.TempDir()
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: original}
	session.TaskState = &TaskState{Goal: original}
	session.Messages = []Message{
		{Role: "user", Text: original},
		{Role: "assistant", Text: "SampleGame/BugReport.md 문서를 생성했습니다."},
		{Role: "user", Text: "문서 산출에 관해서만 검토하지 말고 모든 영역을 검토해야 해"},
	}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-doc",
		Goal:   original,
		Status: patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "SampleGame/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}

	if tx := currentTurnPatchTransaction(session); tx != nil {
		t.Fatalf("broader document steering must not keep document artifact patch as current-turn evidence, got %#v", tx)
	}
}

func TestPatchTransactionGoalSkipsInternalReviewerFeedback(t *testing.T) {
	root := t.TempDir()
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 별도 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{
		{Role: "user", Text: original},
		{Role: "user", Text: "Reviewer feedback: the proposed final answer is not ready yet. Revise before concluding."},
	}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	agent.recordPatchTransactionFromToolMetaIfNeeded(
		ToolCall{Name: "write_file", Arguments: `{"path":"SampleGame/BugReport.md"}`},
		ToolExecutionResult{Meta: map[string]any{
			"effect":            "edit",
			"changed_workspace": true,
			"changed_paths":     []string{"SampleGame/BugReport.md"},
		}},
		nil,
	)

	if session.ActivePatchTransaction == nil {
		t.Fatalf("expected patch transaction")
	}
	if got := session.ActivePatchTransaction.Goal; got != original {
		t.Fatalf("expected patch transaction goal to preserve original user request, got %q", got)
	}
}

func TestPatchTransactionWarnsOnUnscopedWorkspaceMutation(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: "Fix the runtime bug"}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}
	result := ToolExecutionResult{Meta: map[string]any{
		"effect":            "edit",
		"changed_workspace": true,
	}}

	agent.recordPatchTransactionFromToolMetaIfNeeded(
		ToolCall{Name: "external_edit", Arguments: `{}`},
		result,
		nil,
	)

	if session.ActivePatchTransaction == nil {
		t.Fatalf("expected patch transaction for unscoped workspace mutation")
	}
	if len(session.ActivePatchTransaction.Warnings) == 0 ||
		!strings.Contains(session.ActivePatchTransaction.Warnings[0], "without changed_paths") {
		t.Fatalf("expected changed_paths warning, got %#v", session.ActivePatchTransaction.Warnings)
	}
	if !toolMetaBool(result.Meta, "patch_transaction_scope_unknown") {
		t.Fatalf("expected metadata to mark unknown patch scope, got %#v", result.Meta)
	}
	report := agent.buildDiffAwareSelfReviewReport("수정 완료", true)
	if !codingHarnessReportHasFinding(report.Findings, "Workspace mutation has unknown review scope") {
		t.Fatalf("expected unknown scope blocker, got %#v", report.Findings)
	}
}

func TestPatchTransactionWarnsWhenTurnDiffInvalidated(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: "Fix the runtime bug"}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}
	result := ToolExecutionResult{Meta: map[string]any{
		"effect":                          "edit",
		"changed_workspace":               true,
		"changed_paths":                   []string{"main.go"},
		"turn_diff_invalidated":           true,
		"unified_diff_unavailable_reason": "workspace changed but final contents did not match the planned edit after tool failure",
	}}

	agent.recordPatchTransactionFromToolMetaIfNeeded(
		ToolCall{Name: "external_edit", Arguments: `{}`},
		result,
		nil,
	)

	if session.ActivePatchTransaction == nil {
		t.Fatalf("expected patch transaction for invalidated diff mutation")
	}
	warnings := strings.Join(session.ActivePatchTransaction.Warnings, "\n")
	if !strings.Contains(warnings, "invalidated turn diff evidence") {
		t.Fatalf("expected invalidated turn diff warning, got %#v", session.ActivePatchTransaction.Warnings)
	}
}

func TestNoopMetadataEditDoesNotCreateUnknownScopeWarning(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: "Try a metadata edit"}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	agent.recordPatchTransactionFromToolMetaIfNeeded(
		ToolCall{Name: "external_edit", Arguments: `{}`},
		ToolExecutionResult{Meta: map[string]any{
			"effect":            "edit",
			"changed_workspace": false,
		}},
		nil,
	)

	if session.ActivePatchTransaction != nil {
		t.Fatalf("no-op metadata edit must not create patch transaction, got %#v", session.ActivePatchTransaction)
	}
}

func TestPatchTransactionChangedPathsIncludesFailedPartialMutation(t *testing.T) {
	tx := PatchTransaction{
		ID:     "patch-tx-test",
		Status: patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ID:          "patch-tx-test-001",
			ToolName:    "apply_patch",
			Status:      "failed",
			UnifiedDiff: "diff --git a/dir b/dir\n+++ b/dir\n@@ -0,0 +1 @@\n+not a directory\n",
			Paths: []PatchPathChange{{
				Path:      "dir",
				Operation: "create",
				After: HarnessFileFingerprint{
					Path:   "dir",
					Kind:   "file",
					Exists: true,
				},
			}},
		}},
	}

	paths := tx.ChangedPaths()
	if len(paths) != 1 || paths[0] != "dir" {
		t.Fatalf("failed entries with real mutations must still expose changed paths, got %#v", paths)
	}
	if diff := tx.UnifiedDiff(); !strings.Contains(diff, "diff --git a/dir b/dir") {
		t.Fatalf("expected partial mutation unified diff, got %q", diff)
	}
	rendered := tx.RenderPromptSection()
	if !strings.Contains(rendered, "Changed paths: dir") || !strings.Contains(rendered, "Unified diff excerpt") {
		t.Fatalf("expected rendered transaction to include failed mutation evidence, got %q", rendered)
	}
}

func TestAcceptanceContractExtractsArtifactsAndVerificationIntent(t *testing.T) {
	contract := buildAcceptanceContract(
		"docs/result.md 파일을 생성하고 테스트까지 실행해줘",
		TurnIntentEditCode,
		false,
		true,
		false,
	)

	if contract.Mode != "inspect_and_fix" {
		t.Fatalf("expected inspect_and_fix mode, got %#v", contract)
	}
	if len(contract.RequiredArtifacts) != 1 || contract.RequiredArtifacts[0] != "docs/result.md" {
		t.Fatalf("expected docs/result.md artifact, got %#v", contract.RequiredArtifacts)
	}
	if !contract.VerificationRequired {
		t.Fatalf("expected verification to be required, got %#v", contract)
	}
	if len(contract.NonGoals) == 0 || !strings.Contains(contract.NonGoals[0], "Do not stage") {
		t.Fatalf("expected git non-goal, got %#v", contract.NonGoals)
	}
}

func TestAcceptanceContractExtractsKoreanDocumentArtifactRequest(t *testing.T) {
	contract := buildAcceptanceContract(
		"각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 별도 문서로 생성해",
		TurnIntentEditCode,
		false,
		true,
		false,
	)

	if len(contract.RequiredArtifacts) != 1 || contract.RequiredArtifacts[0] != "SampleGame/BugReport.md" {
		t.Fatalf("expected Korean document artifact path, got %#v", contract.RequiredArtifacts)
	}
}

func TestAcceptanceContractDoesNotTreatKoreanCodeFixPathAsArtifact(t *testing.T) {
	contract := buildAcceptanceContract(
		"@VAllocAnalyzer.cpp 코드에 버그가 있는지 검토하고 수정해줘",
		TurnIntentEditCode,
		false,
		true,
		false,
	)

	if len(contract.RequiredArtifacts) != 0 {
		t.Fatalf("code fix target should not become a required artifact, got %#v", contract.RequiredArtifacts)
	}
}

func TestAcceptanceContractDrivesMissingRequiredArtifactRepair(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "Done."}},
			toolCallResponse("write_file", map[string]any{
				"path":    "docs/required.md",
				"content": "# Required\n",
			}),
			{Message: Message{Role: "assistant", Text: "Created docs/required.md. Deterministic artifact-quality checks found no blocking document-content issues. Verification not run. Remaining limitations: no known remaining artifact limitation is recorded."}},
		},
	}
	cfg := DefaultConfig(root)
	cfg.AutoVerify = boolPtr(false)
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "create docs/required.md")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Created docs/required.md") {
		t.Fatalf("expected final artifact summary, got %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected one contract repair turn, got %d requests", len(provider.requests))
	}
	secondRequest := provider.requests[1]
	last := secondRequest.Messages[len(secondRequest.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Text, "Required artifact is missing") {
		t.Fatalf("expected required artifact feedback, got %#v", last)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "required.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "# Required\n" {
		t.Fatalf("unexpected artifact contents: %q", string(data))
	}
}

func TestAcceptanceContractBlocksExplicitVerificationWithoutOutcome(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "main.go",
				"content": "package main\n",
			}),
			{Message: Message{Role: "assistant", Text: "Implemented the change."}},
			{Message: Message{Role: "assistant", Text: "Changed files: main.go. Self-review: no code blocker found. Validation: tests not run because no test command is configured. Remaining risk: no successful test evidence was recorded."}},
		},
	}
	cfg := DefaultConfig(root)
	cfg.AutoVerify = boolPtr(false)
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "make the change and run tests")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "tests not run") {
		t.Fatalf("expected revised verification status, got %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected contract verification repair turn, got %d requests", len(provider.requests))
	}
	last := provider.requests[2].Messages[len(provider.requests[2].Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Text, "Required verification has no outcome") {
		t.Fatalf("expected verification contract feedback, got %#v", last)
	}
}

func TestPreFinalHarnessBlocksMissingClaimedArtifact(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "Created docs/missing.md."}},
			{Message: Message{Role: "assistant", Text: "I did not create the requested file. No files were changed. Self-review: artifact claim corrected. Validation: verification not run. Remaining risk: requested artifact is still missing."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "create a report")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "did not create") {
		t.Fatalf("expected revised final answer, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected one harness revision turn, got %d requests", len(provider.requests))
	}
	last := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Text, "Claimed artifact is missing") {
		t.Fatalf("expected missing artifact harness feedback, got %#v", last)
	}
}

func TestPreFinalHarnessBlocksVerificationClaimWithoutEvidence(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "main.go",
				"content": "package main\n",
			}),
			{Message: Message{Role: "assistant", Text: "Implemented and verified the change."}},
			{Message: Message{Role: "assistant", Text: "Changed files: main.go. Self-review: no code blocker found. Validation: verification not run. Remaining risk: no known remaining blocker."}},
		},
	}
	cfg := DefaultConfig(root)
	cfg.AutoVerify = boolPtr(false)
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "make the change")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "verification not run") {
		t.Fatalf("expected revised verification wording, got %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected harness to request one revision, got %d requests", len(provider.requests))
	}
	last := provider.requests[2].Messages[len(provider.requests[2].Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Text, "Verification claim has no recorded evidence") {
		t.Fatalf("expected verification-claim harness feedback, got %#v", last)
	}
}

func TestPreFinalHarnessCorrectsMissingChangedFileSummary(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "main.go",
				"content": "package main\n",
			}),
			{Message: Message{Role: "assistant", Text: "Implemented. Self-review: no blocker found. Validation: verification not run. Remaining risk: no known remaining blocker."}},
			{Message: Message{Role: "assistant", Text: "Changed files: main.go. Self-review: no blocker found. Validation: verification not run. Remaining risk: no known remaining blocker."}},
		},
	}
	cfg := DefaultConfig(root)
	cfg.AutoVerify = boolPtr(false)
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "make the change")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "main.go") {
		t.Fatalf("expected corrected changed-file summary, got %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected one final-answer correction, got %d requests", len(provider.requests))
	}
	last := provider.requests[2].Messages[len(provider.requests[2].Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Text, "Changed-file summary is missing") {
		t.Fatalf("expected changed-file completeness feedback, got %#v", last)
	}
}

func TestPreFinalHarnessCorrectsMissingValidationDisclosure(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "main.go",
				"content": "package main\n",
			}),
			{Message: Message{Role: "assistant", Text: "Changed files: main.go. Self-review: no blocker found. Remaining risk: no known remaining blocker."}},
			{Message: Message{Role: "assistant", Text: "Changed files: main.go. Self-review: no blocker found. Validation: verification not run. Remaining risk: no known remaining blocker."}},
		},
	}
	cfg := DefaultConfig(root)
	cfg.AutoVerify = boolPtr(false)
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "make the change")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "verification not run") {
		t.Fatalf("expected corrected validation disclosure, got %q", reply)
	}
	last := provider.requests[2].Messages[len(provider.requests[2].Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Text, "Validation result is missing") {
		t.Fatalf("expected validation completeness feedback, got %#v", last)
	}
}

func TestReviewOnlyFinalAnswerRemainsReadOnlyAndFindingsFirst(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	contract := buildAcceptanceContract("review main.go", TurnIntentReviewCode, true, false, false)
	session.AcceptanceContract = &contract
	agent := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}

	report := agent.buildCodingHarnessReport("Summary: looks okay.", false, false)
	if report.Approved {
		t.Fatalf("expected review-only completeness blockers")
	}
	if !codingHarnessReportHasFinding(report.Outcome.Findings, "Review-only answer is not findings-first") ||
		!codingHarnessReportHasFinding(report.Outcome.Findings, "Review-only no-edit statement is missing") {
		t.Fatalf("expected review-only final answer blockers, got %#v", report.Outcome.Findings)
	}
	report = agent.buildCodingHarnessReport("Findings: no actionable findings. No files were changed. Residual risk: tests were not run.", false, false)
	if !report.Approved {
		t.Fatalf("expected findings-first no-edit review answer to pass, got %#v", report.Outcome.Findings)
	}
}

func TestPreFinalHarnessAnswerOnlyRevisionDisablesTools(t *testing.T) {
	root := t.TempDir()
	badReply := strings.Join([]string{
		"Bug report summary:",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 4 |",
		"| High | 7 |",
		"| Medium | 9 |",
		"| Low | 6 |",
		"| Total | 27 |",
	}, "\n")
	goodReply := strings.ReplaceAll(badReply, "| Total | 27 |", "| Total | 26 |") + "\nChanged files: main.go. Self-review: no code blocker found. Validation: verification not run. Remaining risk: no known remaining blocker."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "main.go",
				"content": "package main\n",
			}),
			{Message: Message{Role: "assistant", Text: badReply}},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}),
			{Message: Message{Role: "assistant", Text: goodReply}, EndTurn: boolPtr(false)},
		},
	}
	cfg := DefaultConfig(root)
	cfg.AutoVerify = boolPtr(false)
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "make the change")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "| Total | 26 |") {
		t.Fatalf("expected corrected final answer, got %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected answer-only correction, blocked tool attempt, and final answer, got %d requests", len(provider.requests))
	}
	if len(provider.requests[2].Tools) != 0 {
		t.Fatalf("expected final-answer-only correction request to disable tools, got %d tools", len(provider.requests[2].Tools))
	}
	if len(provider.requests[3].Tools) != 0 {
		t.Fatalf("expected redirected correction retry to keep tools disabled, got %d tools", len(provider.requests[3].Tools))
	}
	if !scriptedRequestsContainText(provider.requests, "final-answer-only correction") {
		t.Fatalf("expected final-answer-only guidance in model history")
	}
	if !sessionContainsToolResultText(session, "call-1", "NOT_EXECUTED: pre-final coding harness requires a final-answer-only correction") {
		t.Fatalf("expected hallucinated tool call to be blocked, messages=%#v", session.Messages)
	}
	if session.LastFinalAnswerCorrection == nil || !session.LastFinalAnswerCorrection.Corrected {
		t.Fatalf("expected final-answer correction lifecycle to be recorded, got %#v", session.LastFinalAnswerCorrection)
	}
	if session.LastFinalAnswerCorrection.Status != finalAnswerCorrectionStatusAccepted ||
		session.LastFinalAnswerCorrection.AttemptCount == 0 ||
		session.LastFinalAnswerCorrection.MaxAttempts != finalAnswerCorrectionDefaultMaxAttempts {
		t.Fatalf("expected accepted correction attempt state, got %#v", session.LastFinalAnswerCorrection)
	}
	for _, want := range []string{"changed_file_disclosure", "validation_disclosure", "remaining_risk_disclosure"} {
		if !containsString(session.LastFinalAnswerCorrection.Reasons, want) {
			t.Fatalf("expected correction reason %q, got %#v", want, session.LastFinalAnswerCorrection)
		}
	}
	if strings.Contains(strings.Join(session.LastFinalAnswerCorrection.Reasons, " "), "Do not call tools") ||
		strings.Contains(strings.Join(session.LastFinalAnswerCorrection.FindingTitles, " "), "Pre-final coding harness found issues") {
		t.Fatalf("correction visibility should not expose prompt noise: %#v", session.LastFinalAnswerCorrection)
	}
	if session.RuntimeGateLedger == nil || session.RuntimeGateLedger.FinalAnswerCorrection == nil {
		t.Fatalf("expected runtime gate to expose final-answer correction, got %#v", session.RuntimeGateLedger)
	}
}

func TestFinalAnswerCorrectionVisibilityClassifiesReviewOnlyFormatting(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	contract := buildAcceptanceContract("review main.go", TurnIntentReviewCode, true, false, false)
	session.AcceptanceContract = &contract
	agent := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}

	report := agent.buildCodingHarnessReport("Summary: no issues.", false, false)

	if report.FinalAnswerCorrection == nil || !report.FinalAnswerCorrection.Required {
		t.Fatalf("expected correction visibility for review-only formatting, got %#v", report.FinalAnswerCorrection)
	}
	if !containsString(report.FinalAnswerCorrection.Reasons, "review_only_findings_first_no_edit") {
		t.Fatalf("expected review-only correction reason, got %#v", report.FinalAnswerCorrection)
	}
}

func TestFinalAnswerCorrectionAcceptedDoesNotCrossExternalTurn(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{Session: session}
	report := CodingHarnessReport{
		Findings: []CodingHarnessFinding{{
			Severity: "blocker",
			Title:    "Changed-file summary is missing",
			Detail:   "Changed paths are recorded but not disclosed.",
		}},
	}
	report.Normalize()

	agent.recordFinalAnswerCorrectionRequired(&report)
	session.Messages = append(session.Messages, Message{Role: "user", Text: "새 작업을 하자"})
	session.LastCodingHarnessReport = &CodingHarnessReport{Approved: true}

	agent.markFinalAnswerCorrectionAccepted()

	if session.LastFinalAnswerCorrection == nil {
		t.Fatalf("expected pending correction visibility")
	}
	if session.LastFinalAnswerCorrection.Corrected {
		t.Fatalf("external user turn must not mark stale correction as corrected: %#v", session.LastFinalAnswerCorrection)
	}
}

func TestPreFinalHarnessExhaustionReturnsBlockedReply(t *testing.T) {
	root := t.TempDir()
	badReply := strings.Join([]string{
		"Bug report summary:",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 4 |",
		"| High | 7 |",
		"| Medium | 9 |",
		"| Low | 6 |",
		"| Total | 27 |",
	}, "\n")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "main.go",
				"content": "package main\n",
			}),
			{Message: Message{Role: "assistant", Text: badReply}},
			{Message: Message{Role: "assistant", Text: badReply}},
			{Message: Message{Role: "assistant", Text: badReply}},
		},
	}
	cfg := DefaultConfig(root)
	cfg.AutoVerify = boolPtr(false)
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "make the change")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Pre-final coding harness is still blocking completion") {
		t.Fatalf("expected blocked harness reply, got %q", reply)
	}
	if !strings.Contains(reply, "Final answer has inconsistent bug counts") {
		t.Fatalf("expected blocker details in blocked reply, got %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected two correction attempts before blocked final, got %d requests", len(provider.requests))
	}
	if len(provider.requests[2].Tools) != 0 || len(provider.requests[3].Tools) != 0 {
		t.Fatalf("expected exhausted answer-only correction turns to keep tools disabled, got %d and %d tools", len(provider.requests[2].Tools), len(provider.requests[3].Tools))
	}
	if session.LastFinalAnswerCorrection == nil ||
		!session.LastFinalAnswerCorrection.Rejected ||
		session.LastFinalAnswerCorrection.Status != finalAnswerCorrectionStatusRejected ||
		session.LastFinalAnswerCorrection.AttemptCount < 3 {
		t.Fatalf("expected exhausted correction to be recorded as rejected with attempts, got %#v", session.LastFinalAnswerCorrection)
	}
}

func TestReportOnlyBlockedBySkippedVerificationDisclosure(t *testing.T) {
	disclosure := CodingHarnessFinding{
		Severity: "blocker",
		Title:    "Verification was not run disclosure missing",
		Detail:   "The latest verification was skipped, but the final answer does not say so.",
	}

	only := &CodingHarnessReport{Outcome: OutcomeInvariantReport{Findings: []CodingHarnessFinding{disclosure}}}
	if !reportOnlyBlockedBySkippedVerificationDisclosure(only) {
		t.Fatalf("expected the sole disclosure blocker to qualify for self-heal")
	}

	withFailure := &CodingHarnessReport{Outcome: OutcomeInvariantReport{Findings: []CodingHarnessFinding{
		disclosure,
		{Severity: "blocker", Title: "Unresolved verification failure", Detail: "verification still failing"},
	}}}
	if reportOnlyBlockedBySkippedVerificationDisclosure(withFailure) {
		t.Fatalf("a real verification failure blocker must disqualify the self-heal")
	}

	withOther := &CodingHarnessReport{
		Findings: []CodingHarnessFinding{{Severity: "blocker", Title: "Final answer has inconsistent bug counts"}},
		Outcome:  OutcomeInvariantReport{Findings: []CodingHarnessFinding{disclosure}},
	}
	if reportOnlyBlockedBySkippedVerificationDisclosure(withOther) {
		t.Fatalf("a second unrelated blocker must disqualify the self-heal")
	}

	if reportOnlyBlockedBySkippedVerificationDisclosure(&CodingHarnessReport{}) {
		t.Fatalf("an approved report has nothing to heal")
	}
	if reportOnlyBlockedBySkippedVerificationDisclosure(nil) {
		t.Fatalf("a nil report must not qualify")
	}
}

func TestAppendSkippedVerificationNotRunDisclosure(t *testing.T) {
	blockerOnly := "Changed files: main.go. Validation: the test runner is currently blocked. Remaining risk: behavior is unconfirmed."
	if replyMentionsVerificationNotRun(blockerOnly) {
		t.Fatalf("fixture should not already disclose not-run, got %q", blockerOnly)
	}
	healed := appendSkippedVerificationNotRunDisclosure(blockerOnly)
	if !strings.Contains(healed, "verification was not run") {
		t.Fatalf("expected the not-run disclosure to be appended even when only a blocker is mentioned, got %q", healed)
	}
	if !replyMentionsVerificationNotRun(healed) {
		t.Fatalf("healed reply must satisfy the harness not-run detector, got %q", healed)
	}

	alreadyDisclosed := "Validation: verification was not run."
	if got := appendSkippedVerificationNotRunDisclosure(alreadyDisclosed); got != alreadyDisclosed {
		t.Fatalf("must not double-append when the disclosure is already present, got %q", got)
	}
	if got := appendSkippedVerificationNotRunDisclosure("   "); got != "" {
		t.Fatalf("empty reply should stay empty, got %q", got)
	}
}

func TestPreFinalHarnessSelfHealsSkippedVerificationDisclosure(t *testing.T) {
	root := t.TempDir()
	// A divergent final answer: it mentions a "blocker" (so the runtime gate treats
	// the verification intervention as resolved) but never states that verification
	// was not run (so the harness keeps blocking). This is the exact reply class that
	// used to dead-end in a blocked turn after the correction loop exhausted.
	divergent := testModificationFinalAnswer("main.go",
		"the local test runner is currently blocked, so no result is available",
		"behavior has no successful test evidence")
	if replyMentionsVerificationNotRun(divergent) {
		t.Fatalf("fixture must NOT disclose not-run, got %q", divergent)
	}
	if !replyMentionsVerificationBlocker(divergent) {
		t.Fatalf("fixture must mention a blocker to reproduce the divergence, got %q", divergent)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{Message: Message{Role: "assistant", Text: divergent}},
			{Message: Message{Role: "assistant", Text: divergent}},
			{Message: Message{Role: "assistant", Text: divergent}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	history := &VerificationHistoryStore{Path: filepath.Join(root, "verification-history.json")}
	ws := Workspace{BaseRoot: root, Root: root}
	verifyCount := 0
	agent := &Agent{
		Config:        Config{},
		Client:        provider,
		Tools:         NewToolRegistry(NewWriteFileTool(ws)),
		Workspace:     ws,
		Session:       session,
		Store:         store,
		VerifyHistory: history,
		PromptConfirmAutoVerify: func(plan VerificationPlan) (bool, error) {
			_ = plan
			return false, nil
		},
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			verifyCount++
			return VerificationReport{}, false
		},
	}

	reply, err := agent.Reply(context.Background(), "fix the file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if verifyCount != 0 {
		t.Fatalf("verification must not run after the user declines, got %d", verifyCount)
	}
	if strings.Contains(reply, "Pre-final coding harness is still blocking completion") {
		t.Fatalf("self-heal should replace the blocked reply, got %q", reply)
	}
	if !strings.Contains(reply, "Validation: verification was not run.") {
		t.Fatalf("self-heal should append the not-run disclosure, got %q", reply)
	}
	if !strings.Contains(reply, "Changed files: main.go") {
		t.Fatalf("self-heal must preserve the model's original answer, got %q", reply)
	}
	if session.LastCodingHarnessReport == nil || !session.LastCodingHarnessReport.Approved {
		t.Fatalf("re-checked harness report should be approved after the heal, got %#v", session.LastCodingHarnessReport)
	}
	if session.LastVerification == nil || !session.LastVerification.WasSkipped() {
		t.Fatalf("declined verification should remain recorded as skipped, got %#v", session.LastVerification)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected exactly 4 model requests (1 tool + 3 finals, then deterministic heal), got %d", len(provider.requests))
	}
}

func TestReviewObservabilityPreservesRejectedFinalAnswerCorrection(t *testing.T) {
	report := &CodingHarnessReport{
		Findings: []CodingHarnessFinding{{
			Severity: "blocker",
			Title:    "Changed-file summary is missing",
			Detail:   "Changed paths are recorded but not disclosed.",
		}},
		FinalAnswerCorrection: &FinalAnswerCorrectionVisibility{
			Required:     true,
			Rejected:     true,
			Status:       finalAnswerCorrectionStatusRejected,
			AttemptCount: 3,
			MaxAttempts:  finalAnswerCorrectionDefaultMaxAttempts,
			Reasons:      []string{"changed_file_disclosure", "correction_attempts_exhausted"},
		},
	}
	report.Normalize()
	run := &ReviewRun{
		ID:           "review-test",
		RequestClass: reviewRequestClassModifyThenReview,
	}

	observability := buildReviewDecisionObservability(run, nil, report)

	if observability == nil || observability.FinalAnswerCorrection == nil {
		t.Fatalf("expected final-answer correction in observability, got %#v", observability)
	}
	if !observability.FinalAnswerCorrection.Rejected ||
		observability.FinalAnswerCorrection.Status != finalAnswerCorrectionStatusRejected ||
		observability.FinalAnswerCorrection.AttemptCount != 3 {
		t.Fatalf("expected rejected correction state to be preserved, got %#v", observability.FinalAnswerCorrection)
	}
}

func TestDiffAwareHarnessBlocksKoreanBuildPassClaimWithoutEvidence(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.PatchTransactions = []PatchTransaction{{
		ID:            "patch-tx-test",
		WorkspaceRoot: root,
		Status:        patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-tx-test-001",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "main.go",
				Operation: "create",
				After: HarnessFileFingerprint{
					Path:   "main.go",
					Kind:   "file",
					Exists: true,
				},
			}},
		}},
	}}
	agent := &Agent{
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}

	report := agent.buildDiffAwareSelfReviewReport("검증:\n- `msbuild \"SampleApp/SampleApp.sln\" /m` 실행 및 통과 확인했습니다.", false)
	if !codingHarnessReportHasFinding(report.Findings, "Verification claim has no recorded evidence") {
		t.Fatalf("expected Korean verification success claim to be blocked, got %#v", report.Findings)
	}
}

func TestDiffAwareHarnessAllowsKoreanBuildSkippedDisclosure(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.PatchTransactions = []PatchTransaction{{
		ID:            "patch-tx-test",
		WorkspaceRoot: root,
		Status:        patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-tx-test-001",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "main.go",
				Operation: "create",
				After: HarnessFileFingerprint{
					Path:   "main.go",
					Kind:   "file",
					Exists: true,
				},
			}},
		}},
	}}
	agent := &Agent{
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}

	report := agent.buildDiffAwareSelfReviewReport("검증:\n- `msbuild \"SampleApp/SampleApp.sln\" /m` 빌드는 실행하지 않았습니다.", false)
	if codingHarnessReportHasFinding(report.Findings, "Verification claim has no recorded evidence") {
		t.Fatalf("expected skipped verification disclosure to be allowed, got %#v", report.Findings)
	}
}

func codingHarnessReportHasFinding(findings []CodingHarnessFinding, title string) bool {
	for _, finding := range findings {
		if finding.Title == title {
			return true
		}
	}
	return false
}

func TestManualVerificationClearsPendingCheck(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		PendingChecks: []string{verificationPendingCheck},
	}
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	agent.noteToolExecutionResultDetailed(ToolCall{Name: "run_shell"}, ToolExecutionResult{
		DisplayText: "ok  \t./...",
		Meta: map[string]any{
			"effect":            "execute",
			"verification_like": true,
			"success":           true,
		},
	}, nil)

	if hasPendingVerificationCheck(session) {
		t.Fatalf("expected manual verification-like shell result to clear pending verification check, got %#v", session.TaskState.PendingChecks)
	}
}

func TestDeclinedVerificationDoesNotClearPendingCheckOrCountAsEvidence(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		PendingChecks: []string{verificationPendingCheck},
	}
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	agent.noteToolExecutionResultDetailed(ToolCall{Name: "run_shell"}, ToolExecutionResult{
		DisplayText: "verification command skipped because the user declined to run it",
		Meta: map[string]any{
			"effect":                   "execute",
			"verification_like":        true,
			"verification_status":      string(VerificationSkipped),
			"verification_evidence":    false,
			"verification_declined":    true,
			"command_execution_status": "declined",
			"success":                  true,
		},
	}, nil)

	if !hasPendingVerificationCheck(session) {
		t.Fatalf("declined verification must leave the pending verification check in place")
	}
	if sessionHasSuccessfulVerificationEvidence(session) {
		t.Fatalf("declined verification must not count as successful evidence")
	}
}

func TestSkippedVerificationReportIsNotSuccessfulEvidence(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.LastVerification = &VerificationReport{
		Steps: []VerificationStep{{
			Label:  "build",
			Status: VerificationSkipped,
			Output: "verification skipped because the user declined",
		}},
	}

	if sessionHasSuccessfulVerificationEvidence(session) {
		t.Fatalf("skipped-only verification report must not count as successful evidence")
	}
	session.LastVerification.Steps[0].Status = VerificationPassed
	if !sessionHasSuccessfulVerificationEvidence(session) {
		t.Fatalf("passed verification report should count as successful evidence")
	}
}

func TestFinalReviewerPromptIncludesCodingHarnessContext(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	contract := buildAcceptanceContract("create docs/missing.md and run tests", TurnIntentEditCode, false, true, false)
	session.AcceptanceContract = &contract
	session.ActivePatchTransaction = &PatchTransaction{
		ID:            "patch-tx-test",
		Goal:          "create docs/missing.md and run tests",
		WorkspaceRoot: root,
		Status:        patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-tx-test-001",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "main.go",
				Operation: "create",
				After: HarnessFileFingerprint{
					Path:   "main.go",
					Kind:   "file",
					Exists: true,
				},
			}},
		}},
	}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: false,
		Findings: []CodingHarnessFinding{{
			Severity: "blocker",
			Title:    "Claimed artifact is missing",
			Detail:   "docs/missing.md does not exist.",
		}},
	}

	prompt := buildInteractiveFinalAnswerReviewerPrompt(session, "done", false)
	if !strings.Contains(prompt, "patch-tx-test") {
		t.Fatalf("expected patch transaction in reviewer prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Acceptance contract") || !strings.Contains(prompt, "docs/missing.md") {
		t.Fatalf("expected acceptance contract in reviewer prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Claimed artifact is missing") {
		t.Fatalf("expected coding harness finding in reviewer prompt, got %q", prompt)
	}
}

func TestFinalReviewerPromptOmitsStaleActiveEditLoop(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.TaskState = &TaskState{Goal: "현재 상태만 알려줘"}
	session.Messages = []Message{
		{
			Role: "user",
			Text: "RuntimeManager.cpp 버그를 수정해",
		},
		{
			Role: "user",
			Text: "현재 상태만 알려줘",
		},
	}
	session.ActiveEditLoop = &EditLoopState{
		ID:              "edit-loop-old",
		Goal:            "RuntimeManager.cpp 버그를 수정해",
		Status:          editLoopStatusActive,
		ChangedPaths:    []string{"cmd/kernforge/agent.go"},
		WorkerSummaries: []string{"updated RuntimeManager handling"},
	}

	prompt := buildInteractiveFinalAnswerReviewerPrompt(session, "현재 상태를 확인했습니다.", false)
	if strings.Contains(prompt, "Apply/verify/retry ledger") {
		t.Fatalf("stale active edit loop should not be included in final reviewer prompt, got %q", prompt)
	}
	if strings.Contains(prompt, "cmd/kernforge/agent.go") {
		t.Fatalf("stale edit-loop changed path should not be included in final reviewer prompt, got %q", prompt)
	}
}

func TestFinalReviewerPromptIncludesCurrentActiveEditLoop(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.TaskState = &TaskState{Goal: "RuntimeManager.cpp 버그를 수정해"}
	session.Messages = []Message{{
		Role: "user",
		Text: "RuntimeManager.cpp 버그를 수정해",
	}}
	session.ActiveEditLoop = &EditLoopState{
		ID:              "edit-loop-current",
		Goal:            "RuntimeManager.cpp 버그를 수정해",
		Status:          editLoopStatusActive,
		ChangedPaths:    []string{"cmd/kernforge/agent.go"},
		WorkerSummaries: []string{"updated RuntimeManager handling"},
	}

	prompt := buildInteractiveFinalAnswerReviewerPrompt(session, "RuntimeManager.cpp 수정 완료", false)
	if !strings.Contains(prompt, "Apply/verify/retry ledger") {
		t.Fatalf("current active edit loop should be included in final reviewer prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "cmd/kernforge/agent.go") {
		t.Fatalf("current edit-loop changed path should be included in final reviewer prompt, got %q", prompt)
	}
}

func TestCurrentTurnPatchTransactionIgnoresStaleActiveGoal(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.TaskState = &TaskState{Goal: "현재 상태만 알려줘"}
	session.Messages = []Message{
		{
			Role: "user",
			Text: "RuntimeManager.cpp 버그를 수정해",
		},
		{
			Role: "user",
			Text: "현재 상태만 알려줘",
		},
	}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-old",
		Goal:   "RuntimeManager.cpp 버그를 수정해",
		Status: patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-old-001",
			ToolName: "apply_patch",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "cmd/kernforge/agent.go",
				Operation: "modify",
			}},
		}},
	}

	if tx := currentTurnPatchTransaction(session); tx != nil {
		t.Fatalf("stale active patch transaction should not be current-turn evidence, got %#v", tx)
	}
	if changed := currentTurnPatchTransactionChangedPaths(session); len(changed) != 0 {
		t.Fatalf("stale active patch changed paths should be ignored, got %#v", changed)
	}
	prompt := buildInteractiveFinalAnswerReviewerPrompt(session, "현재 상태를 확인했습니다.", false)
	if strings.Contains(prompt, "Patch transaction") {
		t.Fatalf("stale active patch should not be included in final reviewer prompt, got %q", prompt)
	}
	if strings.Contains(prompt, "cmd/kernforge/agent.go") {
		t.Fatalf("stale active patch path should not be included in final reviewer prompt, got %q", prompt)
	}
}

func TestEnsurePatchTransactionStartsFreshWhenGoalChanges(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-old",
		Goal:   "RuntimeManager.cpp 버그를 수정해",
		Status: patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-old-001",
			ToolName: "apply_patch",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "cmd/kernforge/agent.go",
				Operation: "modify",
			}},
		}},
	}

	tx := session.ensurePatchTransaction("KnIocpPipeServer.cpp 버그를 수정해", root)
	if tx == nil {
		t.Fatalf("expected fresh patch transaction")
	}
	if tx.ID == "patch-old" {
		t.Fatalf("expected old patch transaction to be archived instead of reused")
	}
	if tx.Goal != "KnIocpPipeServer.cpp 버그를 수정해" {
		t.Fatalf("expected new goal, got %q", tx.Goal)
	}
	if len(session.PatchTransactions) != 1 || session.PatchTransactions[0].ID != "patch-old" {
		t.Fatalf("expected stale patch transaction to be archived, got %#v", session.PatchTransactions)
	}
}

func TestSessionPatchTransactionChangedPathsSkipsStaleActivePatch(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "README.md를 업데이트해",
	}}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-old-active",
		Goal:   "RuntimeManager.cpp 버그를 수정해",
		Status: patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-old-active-001",
			ToolName: "apply_patch",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "cmd/kernforge/agent.go",
				Operation: "modify",
			}},
		}},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-readme",
		Goal:   "README.md를 업데이트해",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-readme-001",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "README.md",
				Operation: "modify",
			}},
		}},
	}}

	paths := sessionPatchTransactionChangedPaths(session)
	if len(paths) != 1 || paths[0] != "README.md" {
		t.Fatalf("expected stale active patch to be skipped in session patch scope, got %#v", paths)
	}
}

func TestClaimedArtifactExtractionIgnoresNonClaimLines(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# ok\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	paths := extractClaimedArtifactPaths("Read README.md before changing code.\nREADME.md was not created.\nCreated docs/result.md.")
	if len(paths) != 1 || paths[0] != "docs/result.md" {
		t.Fatalf("expected only claimed artifact path, got %#v", paths)
	}
}

func TestClaimedArtifactExtractionIgnoresDraftPlanCandidatePaths(t *testing.T) {
	reply := strings.Join([]string{
		"Draft plan only. No files were changed. No artifacts were created.",
		"",
		"## Background",
		"- `Claimed artifact is missing: The final answer appears to claim SKILL.md was created or updated, but it does not exist in the workspace.`",
		"- Similar false positives for `skill.go`, `completion.go`, and `config.go`.",
		"",
		"## Likely files",
		"- SKILL.md",
		"- cmd/kernforge/skill.go",
		"- cmd/kernforge/completion.go",
		"- cmd/kernforge/config.go",
		"",
		"## Validation",
		"- go test ./cmd/kernforge -run \"ClaimedArtifact|DraftOnly\" -count=1",
	}, "\n")

	if paths := extractClaimedArtifactPaths(reply); len(paths) != 0 {
		t.Fatalf("draft-only candidate paths should not be claimed artifacts, got %#v", paths)
	}
}

func TestDraftOnlyPlanAnswerDoesNotTriggerMissingArtifactBlocker(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}
	reply := strings.Join([]string{
		"Draft plan only. No files were changed. No artifacts were created. Verification was not run.",
		"",
		"## Background",
		"- `Claimed artifact is missing: The final answer appears to claim SKILL.md was created or updated, but it does not exist in the workspace.`",
		"",
		"## Likely files",
		"- SKILL.md",
		"- cmd/kernforge/skill.go",
		"- cmd/kernforge/completion.go",
		"- cmd/kernforge/config.go",
	}, "\n")

	report := agent.buildCodingHarnessReport(reply, false, false)
	if codingHarnessReportHasFinding(report.Outcome.Findings, "Claimed artifact is missing") {
		t.Fatalf("draft-only plan answer should not trigger missing artifact blocker, got %#v", report.Outcome.Findings)
	}
	if codingHarnessReportHasFinding(report.ArtifactQuality.Findings, "Artifact contains placeholder content") {
		t.Fatalf("draft-only plan answer should not run artifact quality on candidate paths, got %#v", report.ArtifactQuality)
	}
}

func TestClaimedArtifactExtractionPreservesDotDirectory(t *testing.T) {
	paths := extractClaimedArtifactPaths("Saved `.kernforge/reviews/bug-analysis-report.md`.")
	if len(paths) != 1 || paths[0] != ".kernforge/reviews/bug-analysis-report.md" {
		t.Fatalf("expected dot-prefixed artifact path to be preserved, got %#v", paths)
	}
}

func TestClaimedArtifactExtractionRecognizesKoreanDocumentCreationClaim(t *testing.T) {
	paths := extractClaimedArtifactPaths("SampleGame/BugReport.md 문서를 생성했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.")
	if len(paths) != 1 || paths[0] != "SampleGame/BugReport.md" {
		t.Fatalf("expected Korean document creation claim path, got %#v", paths)
	}
}

func TestAcceptanceContractIgnoresGoalPromptPlanningExamplesAsArtifacts(t *testing.T) {
	request := strings.Join([]string{
		"# Goal Prompt: Fix Draft-Only Plan Artifact Claim Misclassification",
		"",
		"## Objective",
		"Fix KernForge so draft-only planning answers that list candidate files are not misclassified as created artifacts.",
		"",
		"## Background",
		"- `Claimed artifact is missing: The final answer appears to claim SKILL.md was created or updated, but it does not exist in the workspace.`",
		"",
		"## Likely files",
		"- SKILL.md",
		"- cmd/kernforge/skill.go",
		"- cmd/kernforge/completion.go",
		"- cmd/kernforge/config.go",
		"",
		"## Required Behavior",
		"3. Real artifact claims must still be blocked when unsupported. Examples:",
		"- `Created docs/plan.md`",
		"- `Updated README.md`",
		"- `Generated report.html`",
		"4. The runtime should prefer explicit action verbs over bare path mentions.",
	}, "\n")
	mode := resolveAgentRequestMode(request, classifyTurnIntent(request))
	contract := buildAcceptanceContract(request, classifyTurnIntent(request), mode.ReadOnlyAnalysis, mode.ExplicitEditRequest, false)

	if len(contract.RequiredArtifacts) != 0 {
		t.Fatalf("goal prompt planning examples should not become required artifacts, got %#v", contract.RequiredArtifacts)
	}
}
