package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSnapshotWorkspaceFilesSkipsContentHashByDefault(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "big.bin")
	payload := bytes.Repeat([]byte("x"), 256*1024)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	started := time.Now()
	snapshot, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshotWorkspaceFiles: %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("metadata snapshot took too long: %s", time.Since(started))
	}
	sig, ok := snapshot["big.bin"]
	if !ok {
		t.Fatalf("expected big.bin in snapshot, got %#v", snapshot)
	}
	if sig.ContentSHA != "" {
		t.Fatalf("default snapshot must not hash file contents, got %q", sig.ContentSHA)
	}
	if sig.Size != int64(len(payload)) {
		t.Fatalf("expected size %d, got %d", len(payload), sig.Size)
	}
}

func TestSnapshotWorkspaceFilesSkipsHeavyDirs(t *testing.T) {
	root := t.TempDir()
	hidden := filepath.Join(root, "node_modules", "pkg")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "index.js"), []byte("module.exports = 1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.js"), []byte("console.log(1)\n"), 0o644); err != nil {
		t.Fatalf("WriteFile app: %v", err)
	}
	snapshot, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshotWorkspaceFiles: %v", err)
	}
	if _, ok := snapshot[filepath.Join("node_modules", "pkg", "index.js")]; ok {
		t.Fatalf("node_modules files must be skipped, got %#v", snapshot)
	}
	if _, ok := snapshot["app.js"]; !ok {
		t.Fatalf("expected app.js in snapshot, got %#v", snapshot)
	}
}

func TestUserChangeIsolationBlocksExternalTargetChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}
	agent.startUserChangeIsolation()
	if err := os.WriteFile(path, []byte("package main\n\n// user edit\n"), 0o644); err != nil {
		t.Fatalf("WriteFile user edit: %v", err)
	}

	err := agent.checkUserChangeIsolationBeforeTool(ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"main.go","content":"package main\n\n// agent edit\n"}`,
	})
	if err == nil {
		t.Fatalf("expected user-change conflict")
	}
	if !strings.Contains(err.Error(), "main.go") {
		t.Fatalf("expected conflicted path in error, got %v", err)
	}
	if session.LastUserChangeIsolationReport == nil || len(session.LastUserChangeIsolationReport.ConflictedPaths) != 1 {
		t.Fatalf("expected persisted isolation report, got %#v", session.LastUserChangeIsolationReport)
	}
}

func TestUserChangeIsolationRebaselineAfterConflictedRead(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}
	agent.startUserChangeIsolation()
	if err := os.WriteFile(path, []byte("package main\n\n// user edit\n"), 0o644); err != nil {
		t.Fatalf("WriteFile user edit: %v", err)
	}

	err := agent.checkUserChangeIsolationBeforeTool(ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"main.go","content":"package main\n\n// agent edit\n"}`,
	})
	if err == nil {
		t.Fatalf("expected initial user-change conflict")
	}
	if session.LastUserChangeIsolationReport == nil || len(session.LastUserChangeIsolationReport.ConflictedPaths) != 1 {
		t.Fatalf("expected persisted isolation report, got %#v", session.LastUserChangeIsolationReport)
	}

	agent.rebaselineUserChangeIsolationFromRead(ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"main.go"}`,
	}, nil)
	if session.LastUserChangeIsolationReport == nil {
		t.Fatalf("expected isolation report to remain available")
	}
	if len(session.LastUserChangeIsolationReport.ConflictedPaths) != 0 {
		t.Fatalf("expected conflicted path to be cleared after successful read, got %#v", session.LastUserChangeIsolationReport)
	}

	err = agent.checkUserChangeIsolationBeforeTool(ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"main.go","content":"package main\n\n// user edit\n// agent edit\n"}`,
	})
	if err != nil {
		t.Fatalf("expected merge-aware retry after rebaseline to be allowed, got %v", err)
	}
}

func TestUserChangeIsolationAllowsAgentTouchedTarget(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}
	agent.startUserChangeIsolation()
	agent.markAgentTouchedPaths([]string{"main.go"})
	if err := os.WriteFile(path, []byte("package main\n\n// agent edit\n"), 0o644); err != nil {
		t.Fatalf("WriteFile agent edit: %v", err)
	}

	err := agent.checkUserChangeIsolationBeforeTool(ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"main.go","content":"package main\n\n// agent second edit\n"}`,
	})
	if err != nil {
		t.Fatalf("expected agent-touched file to be allowed, got %v", err)
	}
}

func TestAgentUserChangeIsolationPreservesExternalEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &sideEffectProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "main.go",
				"content": "package main\n\n// agent edit\n",
			}),
			{Message: Message{Role: "assistant", Text: "Changed files: main.go. Self-review: stopped because main.go changed outside the agent. Validation: verification was not run. Remaining risk: user edit was preserved and the agent change is incomplete."}},
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
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "changed outside") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "package main\n\n// user edit\n" {
		t.Fatalf("expected user edit to be preserved, got %q", string(data))
	}
	if session.LastUserChangeIsolationReport == nil || len(session.LastUserChangeIsolationReport.ConflictedPaths) == 0 {
		t.Fatalf("expected isolation report, got %#v", session.LastUserChangeIsolationReport)
	}
}

type sideEffectProviderClient struct {
	mu           sync.Mutex
	replies      []ChatResponse
	beforeReturn []func()
	requests     []ChatRequest
	index        int
}

func (s *sideEffectProviderClient) Name() string {
	return "side-effect-scripted"
}

func (s *sideEffectProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req)
	if s.index < len(s.beforeReturn) && s.beforeReturn[s.index] != nil {
		s.beforeReturn[s.index]()
	}
	if s.index >= len(s.replies) {
		return ChatResponse{Message: Message{Role: "assistant", Text: "done"}}, nil
	}
	resp := s.replies[s.index]
	s.index++
	return resp, nil
}
