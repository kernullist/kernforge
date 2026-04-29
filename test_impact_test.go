package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildTestImpactReportRecommendsGoVerification(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("WriteFile go.mod: %v", err)
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.ActivePatchTransaction = &PatchTransaction{
		ID:            "patch-tx-test",
		WorkspaceRoot: root,
		Status:        patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-tx-test-001",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "cmd/app/main.go",
				Operation: "update",
				After: HarnessFileFingerprint{
					Path:   "cmd/app/main.go",
					Kind:   "file",
					Exists: true,
				},
			}},
		}},
	}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildTestImpactReport()
	if report.Confidence == "low" || report.Confidence == "not_applicable" {
		t.Fatalf("expected medium/high confidence, got %#v", report)
	}
	joined := strings.Join(report.RecommendedCommands, " | ")
	if !strings.Contains(joined, "go test") || !strings.Contains(joined, "go vet ./...") {
		t.Fatalf("expected go verification commands, got %#v", report.RecommendedCommands)
	}
	if len(report.Findings) == 0 || !strings.Contains(report.Findings[0].Title, "Recommended verification") {
		t.Fatalf("expected unrecorded verification warning, got %#v", report.Findings)
	}
}

func TestPreFinalHarnessStoresTestImpactReport(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("WriteFile go.mod: %v", err)
	}
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
	if !strings.Contains(reply, "Verification not run") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if session.LastTestImpactReport == nil {
		t.Fatalf("expected test impact report")
	}
	if len(session.LastTestImpactReport.RecommendedCommands) == 0 {
		t.Fatalf("expected recommended commands, got %#v", session.LastTestImpactReport)
	}
}
