package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInvalidEditPayloadRetriesInsteadOfHardFailing verifies that when an edit
// tool rejects a malformed serialized payload (ErrInvalidEditPayload), the turn
// retries with guidance and can complete, rather than hard-failing the whole
// request on the first bad payload.
func TestInvalidEditPayloadRetriesInsteadOfHardFailing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatalf("write app.py: %v", err)
	}
	session := NewSession(root, "scripted", "main-model", "", "full")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root, Perms: NewPermissionManager(ModeBypass, nil)}

	// First write_file rejects a malformed payload; the second succeeds.
	writeTool := &sequenceTool{
		name:    "write_file",
		errs:    []error{ErrInvalidEditPayload, nil},
		outputs: []string{"", "Wrote app.py."},
	}
	writeCall := toolCallResponse("write_file", map[string]any{"path": "app.py", "content": "print('hello')\n"})
	provider := &scriptedProviderClient{replies: []ChatResponse{
		writeCall,
		writeCall,
		{Message: Message{Role: "assistant", Text: testModificationFinalAnswer("app.py", "targeted check passed.", "no known remaining blocker.")}},
	}}

	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false), PermissionMode: string(ModeBypass)},
		Client:    provider,
		Tools:     NewToolRegistry(writeTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update app.py to print hello")
	if err != nil {
		t.Fatalf("Reply hard-failed on ErrInvalidEditPayload instead of retrying: %v", err)
	}
	if writeTool.calls < 2 {
		t.Fatalf("expected the write tool to be retried after ErrInvalidEditPayload, got %d call(s)", writeTool.calls)
	}
	if strings.Contains(strings.ToLower(reply), "invalid edit payload") {
		t.Fatalf("final reply should not surface the invalid-edit-payload error, got %q", reply)
	}
}
