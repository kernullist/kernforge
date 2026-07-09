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
			toolCallResponse("external_edit", map[string]any{}),
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing-one", "replace": "present"}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing-two", "replace": "present"}),
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
	// Two mismatches, then a successful edit, then a third mismatch: without
	// the on-success reset the third mismatch would exceed the per-turn budget
	// and hard-stop the turn even though the turn is making real progress.
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing-one", "replace": "present"}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing-two", "replace": "present"}),
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
