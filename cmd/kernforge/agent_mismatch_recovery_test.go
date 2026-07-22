package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin the mismatch-recovery contract: the loop-limit stop reports
// honestly which edits already landed this turn, the model is steered to the
// write_file escape hatch before the final mismatch, and a successful edit
// resets the per-turn mismatch budget so productive turns are not aborted by
// scattered failures.

func TestEditTargetMismatchAppliedResultLineHonesty(t *testing.T) {
	line := formatEditTargetMismatchAppliedResultLine(nil, false)
	if !strings.Contains(line, "no code changes were applied this turn") {
		t.Fatalf("expected scoped no-change wording, got %q", line)
	}
	line = formatEditTargetMismatchAppliedResultLine([]string{"a/main.go", "b/util.go"}, false)
	if !strings.Contains(line, "2 edit(s) from this turn were already applied") ||
		!strings.Contains(line, "a/main.go") || !strings.Contains(line, "b/util.go") {
		t.Fatalf("expected applied edits to be listed, got %q", line)
	}
	if strings.Contains(line, "no code changes were applied") {
		t.Fatalf("applied edits must not be denied, got %q", line)
	}
	korean := formatEditTargetMismatchAppliedResultLine([]string{"a/main.go"}, true)
	if !strings.Contains(korean, "이미 적용되었습니다") || !strings.Contains(korean, "a/main.go") {
		t.Fatalf("expected Korean applied wording, got %q", korean)
	}
}

func TestEditTargetMismatchLoopLimitReplyListsAppliedEditsAndWriteFileHint(t *testing.T) {
	reply := formatEditTargetMismatchLoopLimitReply(Config{AutoLocale: boolPtr(false)}, &Session{}, []string{"RegGit.Core/DatabaseService.cs"})
	if !strings.Contains(reply, "RegGit.Core/DatabaseService.cs") ||
		!strings.Contains(reply, "already applied") {
		t.Fatalf("expected loop-limit reply to list applied edits, got %q", reply)
	}
	if strings.Contains(reply, "no code changes were applied") {
		t.Fatalf("loop-limit reply must not deny applied edits, got %q", reply)
	}
	if !strings.Contains(reply, "write_file") {
		t.Fatalf("expected write_file escalation hint in the next condition, got %q", reply)
	}
	reanchor := formatEditTargetMismatchReanchorLoopLimitReply(Config{AutoLocale: boolPtr(false)}, &Session{}, []string{"RegGit.Core/DatabaseService.cs"})
	if !strings.Contains(reanchor, "already applied") || !strings.Contains(reanchor, "write_file") {
		t.Fatalf("expected reanchor loop-limit reply to report applied edits and the write_file hint, got %q", reanchor)
	}
}

func TestEditTargetMismatchLoopLimitReportsEarlierAppliedEdits(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	editOK := &metadataEditTool{
		name:   "external_edit",
		output: "edit complete",
		meta: map[string]any{
			"changed_workspace": true,
			"effect":            "edit",
			"changed_paths":     []string{"main.go"},
		},
	}
	replaceTool := &failingTool{
		name: "replace_in_file",
		err:  fmt.Errorf("%w: search text not found in main.go", ErrEditTargetMismatch),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			turnPlanAnnounceReply(),
			toolCallResponse("external_edit", map[string]any{}),
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing-one", "replace": "present"}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing-two", "replace": "present"}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing-three", "replace": "present"}),
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(editOK, replaceTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Edit target mismatches repeated") {
		t.Fatalf("expected the mismatch loop-limit stop, got %q", reply)
	}
	if !strings.Contains(reply, "already applied") || !strings.Contains(reply, "main.go") {
		t.Fatalf("expected the stop reply to report the earlier applied edit, got %q", reply)
	}
	if strings.Contains(reply, "no code changes were applied") {
		t.Fatalf("stop reply must not deny the applied edit, got %q", reply)
	}
	escalationSeen := false
	for _, msg := range session.Messages {
		if msg.Role == "user" && strings.Contains(msg.Text, "single write_file call") {
			escalationSeen = true
			break
		}
	}
	if !escalationSeen {
		t.Fatalf("expected the write_file escalation guidance after the second mismatch")
	}
}

func TestEditTargetMismatchBudgetResetsAfterSuccessfulEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	editOK := &metadataEditTool{
		name:   "external_edit",
		output: "edit complete",
		meta: map[string]any{
			"changed_workspace": true,
			"effect":            "edit",
			"changed_paths":     []string{"main.go"},
		},
	}
	replaceTool := &failingTool{
		name: "replace_in_file",
		err:  fmt.Errorf("%w: search text not found in main.go", ErrEditTargetMismatch),
	}
	// Two mismatches (each followed by a reanchor), then a successful edit, then
	// a third mismatch: without the on-success reset the third mismatch would
	// exceed the per-turn budget and hard-stop the turn even though the turn is
	// making real progress. Every mismatch re-arms the reanchor latch, so each
	// retry must re-read before the next context edit.
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			turnPlanAnnounceReply(),
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing-one", "replace": "present"}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing-two", "replace": "present"}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			toolCallResponse("external_edit", map[string]any{}),
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing-three", "replace": "present"}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Changed files: main.go. Self-review: no code blocker found after 변경을 적용. Validation: verification was not run. Remaining risk: no successful verification evidence was recorded.",
				},
				StopReason: "stop",
			},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(editOK, replaceTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.Contains(reply, "Edit target mismatches repeated") {
		t.Fatalf("successful edit must reset the mismatch budget, but the turn hard-stopped: %q", reply)
	}
	if replaceTool.calls != 3 {
		t.Fatalf("expected all three mismatching edits to execute, got %d", replaceTool.calls)
	}
	if editOK.calls == 0 {
		t.Fatalf("expected the successful edit to run")
	}
}

func TestDocumentTurnKeepsContextEditsDisabledAfterMismatchReanchor(t *testing.T) {
	root := t.TempDir()
	docPath := filepath.Join(root, "overview.md")
	if err := os.WriteFile(docPath, []byte("# Overview\n\nold line\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	replaceTool := &failingTool{
		name: "replace_in_file",
		err:  fmt.Errorf("%w: search text not found in overview.md", ErrEditTargetMismatch),
	}
	writeTool := &metadataEditTool{
		name:   "write_file",
		output: "wrote overview.md",
		meta: map[string]any{
			"changed_workspace": true,
			"effect":            "edit",
			"changed_paths":     []string{"overview.md"},
		},
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			turnPlanAnnounceReply(),
			toolCallResponse("replace_in_file", map[string]any{
				"path": "overview.md", "search": "missing", "replace": "present",
			}),
			toolCallResponse("read_file", map[string]any{"path": "overview.md"}),
			// After reanchor, document turns must NOT reopen replace_in_file.
			toolCallResponse("replace_in_file", map[string]any{
				"path": "overview.md", "search": "still-missing", "replace": "present",
			}),
			toolCallResponse("write_file", map[string]any{
				"path": "overview.md", "content": "# Overview\n\nfixed\n",
			}),
			{Message: Message{Role: "assistant", Text: "Changed files: overview.md. Self-review: no code blocker found after 변경을 적용. Validation: verification was not run. Remaining risk: no known remaining blocker remains."}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(replaceTool, writeTool, NewReadFileTool(ws), NewApplyPatchTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	userText := "발견한 문제점들을 모두 수정해서 문서를 보강하자. 한국어 문서도 함께 보강해줘"
	if !requestLooksLikeInspectThenDocumentTurn(userText) {
		t.Fatalf("fixture request must classify as document deliverable turn")
	}
	reply, err := agent.Reply(context.Background(), userText)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "overview.md") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if replaceTool.calls != 1 {
		t.Fatalf("document turn must not re-enable replace_in_file after reanchor; calls=%d", replaceTool.calls)
	}
	if writeTool.calls != 1 {
		t.Fatalf("expected write_file escape hatch to run once, got %d", writeTool.calls)
	}
	if !scriptedRequestsContainText(provider.requests, "write_file") {
		t.Fatalf("expected document write_file guidance after mismatch")
	}
	// Post-reanchor request must keep context edit tools disabled.
	postReanchorIdx := -1
	for i, req := range provider.requests {
		if chatRequestHasTool(req, "write_file") && !chatRequestHasTool(req, "replace_in_file") {
			postReanchorIdx = i
			break
		}
	}
	if postReanchorIdx < 0 {
		t.Fatalf("expected a post-mismatch request with write_file and without replace_in_file, got %d requests", len(provider.requests))
	}
	postReanchor := provider.requests[postReanchorIdx]
	if chatRequestHasTool(postReanchor, "apply_patch") {
		t.Fatalf("apply_patch must stay disabled after document-turn reanchor")
	}
}

func TestMismatchRearmsReanchorBeforeAnotherContextEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	replaceTool := &failingTool{
		name: "replace_in_file",
		err:  fmt.Errorf("%w: search text not found in main.go", ErrEditTargetMismatch),
	}
	// Mismatch → reanchor → mismatch → immediate replace without re-read must be
	// NOT_EXECUTED (reanchor re-armed), not counted as another executed mismatch.
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			turnPlanAnnounceReply(),
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing-one", "replace": "present"}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing-two", "replace": "present"}),
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing-three", "replace": "present"}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			{Message: Message{Role: "assistant", Text: "stopped guessing without a fresh read"}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(replaceTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.Contains(reply, "Edit target mismatches repeated") {
		t.Fatalf("unguessed third replace must be deferred by reanchor, not hard-stop the turn: %q", reply)
	}
	if replaceTool.calls != 2 {
		t.Fatalf("expected only two executed mismatches; third must be blocked until reanchor, got %d", replaceTool.calls)
	}
	foundDeferred := false
	for _, msg := range session.Messages {
		if msg.Role == "tool" && strings.Contains(msg.Text, "NOT_EXECUTED") &&
			(strings.Contains(msg.Text, "re-anchor") || strings.Contains(msg.Text, "mismatched file contents")) {
			foundDeferred = true
			break
		}
	}
	if !foundDeferred {
		t.Fatalf("expected the immediate post-mismatch edit to be NOT_EXECUTED pending reanchor")
	}
}

// Document reinforce turns that hammer the same non-read signature must get one
// write_file push at abort threshold instead of an immediate stall card.
func TestDocumentReinforceRepeatedToolAbortPushesWriteOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "overview.md"), []byte("# Overview\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	writeTool := &metadataEditTool{
		name:   "write_file",
		output: "wrote overview.md",
		meta: map[string]any{
			"changed_workspace": true,
			"effect":            "edit",
			"changed_paths":     []string{"overview.md"},
		},
	}
	listCall := toolCallResponse("list_files", map[string]any{})
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			turnPlanAnnounceReply(),
			listCall, listCall, listCall, listCall, listCall, // abort → write push
			toolCallResponse("write_file", map[string]any{
				"path": "overview.md", "content": "# Overview\n\nreinforced\n",
			}),
			{Message: Message{Role: "assistant", Text: "Changed files: overview.md. Self-review: document reinforced. Validation: verification was not run. Remaining risk: no known remaining blocker remains."}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws), writeTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}
	userText := "발견한 문제점들을 모두 수정해서 문서를 보강하자. 한국어 문서도 함께 보강해줘"
	reply, err := agent.Reply(context.Background(), userText)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if session.PendingHarnessBlockedRecovery != nil {
		t.Fatalf("first repeated-tool abort on document turn must push write, not open stall card: %#v", session.PendingHarnessBlockedRecovery)
	}
	if !scriptedRequestsContainText(provider.requests, "write_file") {
		t.Fatalf("expected write_file deliverable guidance after repeated-tool abort")
	}
	if writeTool.calls != 1 {
		t.Fatalf("expected write_file to run once after the push, got %d", writeTool.calls)
	}
	if !strings.Contains(reply, "overview.md") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
}

// A second identical-signature abort (after the one-shot write push) still
// escalates to the stall recovery card.
func TestDocumentReinforceSecondRepeatedToolAbortOpensStallCard(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	listCall := toolCallResponse("list_files", map[string]any{})
	replies := []ChatResponse{turnPlanAnnounceReply()}
	for i := 0; i < 10; i++ {
		replies = append(replies, listCall)
	}
	provider := &scriptedProviderClient{replies: replies}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}
	userText := "발견한 문제점들을 모두 수정해서 문서를 보강하자. 한국어 문서도 함께 보강해줘"
	reply, err := agent.Reply(context.Background(), userText)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "repeating the same tool calls") &&
		!strings.Contains(reply, "같은 도구 호출") &&
		!strings.Contains(reply, "Choose") &&
		!strings.Contains(reply, "다음 단계") {
		t.Fatalf("expected stall escalation on second abort, got %q", reply)
	}
	if session.PendingHarnessBlockedRecovery == nil ||
		session.PendingHarnessBlockedRecovery.Cause != harnessRecoveryCauseRepeatedToolCalls {
		t.Fatalf("expected pending repeated-tool recovery after second abort, got %#v", session.PendingHarnessBlockedRecovery)
	}
	if !scriptedRequestsContainText(provider.requests, "write_file") {
		t.Fatalf("first abort must still inject write_file guidance before the stall")
	}
}

