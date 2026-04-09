package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShouldRunAutoScoutOnlyForLookupStyleQueries(t *testing.T) {
	if shouldRunAutoScout("Explain TavernWorker startup flow.") {
		t.Fatalf("expected broad explanation query not to trigger auto scout")
	}
	if !shouldRunAutoScout("Where is WorkerBootstrap defined?") {
		t.Fatalf("expected definition lookup query to trigger auto scout")
	}
	if !shouldRunAutoScout("WorkerBootstrap 사용처 찾아줘") {
		t.Fatalf("expected Korean lookup query to trigger auto scout")
	}
}

func TestReplySkipsAutoScoutWhenAnalysisContextExists(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "worker.cpp"), []byte("int WorkerBootstrap()\n{\n    return 1;\n}\n"), 0o644); err != nil {
		t.Fatalf("write worker.cpp: %v", err)
	}
	cfg := DefaultConfig(root)
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: projectAnalysisFastPathNeedsTools}},
			{Message: Message{Role: "assistant", Text: "done"}},
		},
	}
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest: %v", err)
	}
	pack := KnowledgePack{
		RunID:          "run-scout-skip",
		Goal:           "map worker bootstrap",
		Root:           root,
		ProjectSummary: "WorkerBootstrap is owned by the worker runtime.",
		Subsystems: []KnowledgeSubsystem{
			{
				Title:         "Worker Runtime",
				Group:         "Forensic Analysis",
				KeyFiles:      []string{"worker.cpp"},
				EvidenceFiles: []string{"worker.cpp"},
			},
		},
	}
	data, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), data, 0o644); err != nil {
		t.Fatalf("write knowledge pack: %v", err)
	}

	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "Where is WorkerBootstrap defined?"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(provider.requests) == 0 {
		t.Fatalf("expected provider requests")
	}
	var foundAnalysis bool
	for _, msg := range provider.requests[0].Messages {
		if strings.Contains(msg.Text, "Relevant project analysis from past analyze-project runs") {
			foundAnalysis = true
			if strings.Contains(msg.Text, "Auto-discovered code context") {
				t.Fatalf("expected auto scout context to be skipped when analysis context exists, got %q", msg.Text)
			}
		}
	}
	if !foundAnalysis {
		t.Fatalf("expected cached analysis context to be present in request")
	}
}
