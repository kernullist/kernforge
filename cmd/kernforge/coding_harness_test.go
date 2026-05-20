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
