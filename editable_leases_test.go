package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDeriveEditableLeasePathsPrefersExplicitPathHints(t *testing.T) {
	cfg := DefaultConfig("C:\\workspace")
	profile, ok := configuredSpecialistProfileByName(cfg, "telemetry-analyst")
	if !ok {
		t.Fatalf("expected telemetry-analyst profile")
	}
	node := TaskNode{
		ID:                     "plan-01",
		Title:                  "Patch telemetry/provider.man and validate manifest drift",
		Kind:                   "edit",
		Status:                 "ready",
		EditableOwnershipPaths: []string{"telemetry/**", "*.man"},
		LastUpdated:            time.Now(),
	}

	leasePaths, reason := deriveEditableLeasePaths(nil, node, profile)
	if !slices.Contains(leasePaths, "telemetry/provider.man") {
		t.Fatalf("expected explicit path hint lease, got %#v", leasePaths)
	}
	if reason != "path-hints" {
		t.Fatalf("expected path-hints reason, got %q", reason)
	}
}

func TestDeriveEditableLeasePathsFiltersClaimedPatternsBySpecialist(t *testing.T) {
	cfg := DefaultConfig("C:\\workspace")
	profile, ok := configuredSpecialistProfileByName(cfg, "telemetry-analyst")
	if !ok {
		t.Fatalf("expected telemetry-analyst profile")
	}
	graph := &TaskGraph{
		Nodes: []TaskNode{
			{
				ID:                 "plan-01",
				Title:              "Update telemetry tree",
				Kind:               "edit",
				Status:             "in_progress",
				EditableSpecialist: "telemetry-analyst",
				EditableLeasePaths: []string{"telemetry/**"},
				LastUpdated:        time.Now(),
			},
			{
				ID:                     "plan-02",
				Title:                  "Refresh provider.man manifest",
				Kind:                   "edit",
				Status:                 "ready",
				EditableSpecialist:     "telemetry-analyst",
				EditableOwnershipPaths: []string{"telemetry/**", "*.man"},
				LastUpdated:            time.Now(),
			},
		},
		LastUpdated: time.Now(),
	}

	leasePaths, _ := deriveEditableLeasePaths(graph, graph.Nodes[1], profile)
	if slices.Contains(leasePaths, "telemetry/**") {
		t.Fatalf("expected claimed telemetry tree lease to be filtered, got %#v", leasePaths)
	}
	if !slices.Contains(leasePaths, "provider.man") {
		t.Fatalf("expected the file-specific manifest lease to survive, got %#v", leasePaths)
	}
}

func TestResolveEditTargetUsesEditableLeaseOverBroadOwnership(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "telemetry"), 0o755); err != nil {
		t.Fatalf("MkdirAll telemetry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "telemetry", "provider.man"), []byte("<manifest/>\n"), 0o644); err != nil {
		t.Fatalf("WriteFile provider.man: %v", err)
	}

	cfg := DefaultConfig(root)
	sess := NewSession(root, "openai", "gpt-5.4", "", "default")
	sess.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{
				ID:                     "plan-01",
				Title:                  "Patch telemetry/provider.man",
				Kind:                   "edit",
				Status:                 "ready",
				EditableSpecialist:     "implementation-owner",
				EditableOwnershipPaths: []string{"**"},
				EditableLeasePaths:     []string{"telemetry/provider.man"},
				LastUpdated:            time.Now(),
			},
		},
		LastUpdated: time.Now(),
	}
	rt := &runtimeState{
		cfg:     cfg,
		session: sess,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}
	rt.syncWorkspaceFromSession()

	allowed, err := rt.resolveEditTarget(EditRoutingRequest{
		Path:        "telemetry/provider.man",
		OwnerNodeID: "plan-01",
		ForLookup:   true,
	})
	if err != nil {
		t.Fatalf("expected leased path to resolve, got %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(allowed.AbsolutePath), "telemetry/provider.man") {
		t.Fatalf("unexpected leased target: %#v", allowed)
	}

	_, err = rt.resolveEditTarget(EditRoutingRequest{
		Path:        "main.go",
		OwnerNodeID: "plan-01",
		ForLookup:   true,
	})
	if err == nil {
		t.Fatalf("expected lease enforcement to reject main.go")
	}
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch, got %v", err)
	}
}

func TestSyncTaskExecutorFocusRecordsEditableLeaseFromPathHints(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	session.TaskState = &TaskState{}
	session.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{
				ID:          "plan-01",
				Title:       "Patch telemetry/provider.man and re-run manifest validation",
				Kind:        "edit",
				Status:      "ready",
				LastUpdated: time.Now(),
			},
		},
		LastUpdated: time.Now(),
	}
	session.TaskGraph.Normalize()

	agent := &Agent{
		Config:  Config{},
		Session: session,
	}
	if err := agent.syncTaskExecutorFocus(); err != nil {
		t.Fatalf("syncTaskExecutorFocus: %v", err)
	}

	node, ok := session.TaskGraph.Node("plan-01")
	if !ok {
		t.Fatalf("expected focused task node to remain available")
	}
	if strings.TrimSpace(node.EditableSpecialist) == "" {
		t.Fatalf("expected editable specialist assignment, got %#v", node)
	}
	if !slices.Contains(node.EditableLeasePaths, "telemetry/provider.man") {
		t.Fatalf("expected editable lease path to be recorded, got %#v", node)
	}
}
