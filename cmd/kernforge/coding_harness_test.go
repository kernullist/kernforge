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
			{Message: Message{Role: "assistant", Text: "Implemented the change. Verification not run."}},
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
	if !strings.Contains(reply, "Implemented") {
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
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
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
	stale := "Tavern/BugReport.md 보고서를 생성해"
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
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
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
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
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
	original := "검토해서 @Tavern.cpp 보고서를 작성해"
	enriched := strings.Join([]string{
		"검토해서 Tavern/Tavern.cpp 보고서를 작성해",
		"",
		"Attached context:",
		"Referenced file: F:\\repo\\Tavern\\Tavern.cpp",
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
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
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
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
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
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
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
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
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
	stale := "Tavern/BugReport.md 보고서를 생성해"
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

func TestPatchTransactionGoalSkipsInternalReviewerFeedback(t *testing.T) {
	root := t.TempDir()
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
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
		ToolCall{Name: "write_file", Arguments: `{"path":"Tavern/BugReport.md"}`},
		ToolExecutionResult{Meta: map[string]any{
			"effect":            "edit",
			"changed_workspace": true,
			"changed_paths":     []string{"Tavern/BugReport.md"},
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

func TestAcceptanceContractDrivesMissingRequiredArtifactRepair(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "Done."}},
			toolCallResponse("write_file", map[string]any{
				"path":    "docs/required.md",
				"content": "# Required\n",
			}),
			{Message: Message{Role: "assistant", Text: "Created docs/required.md. Verification not run."}},
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
			{Message: Message{Role: "assistant", Text: "Implemented the change. Tests not run because no test command is configured."}},
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
	if !strings.Contains(reply, "Tests not run") {
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
			{Message: Message{Role: "assistant", Text: "I did not create the requested file."}},
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
			{Message: Message{Role: "assistant", Text: "Implemented the change. Verification not run."}},
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
	if !strings.Contains(reply, "Verification not run") {
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
	goodReply := strings.ReplaceAll(badReply, "| Total | 27 |", "| Total | 26 |") + "\nVerification not run."
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

func TestClaimedArtifactExtractionPreservesDotDirectory(t *testing.T) {
	paths := extractClaimedArtifactPaths("Saved `.kernforge/reviews/bug-analysis-report.md`.")
	if len(paths) != 1 || paths[0] != ".kernforge/reviews/bug-analysis-report.md" {
		t.Fatalf("expected dot-prefixed artifact path to be preserved, got %#v", paths)
	}
}
