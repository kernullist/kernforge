package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recordingBackgroundBundleTool struct {
	calls []map[string]any
	meta  map[string]any
	err   error
}

func (t *recordingBackgroundBundleTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "run_shell_bundle_background",
		Description: "Record verification bundle starts during tests.",
		InputSchema: map[string]any{
			"type": "object",
		},
	}
}

func (t *recordingBackgroundBundleTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t *recordingBackgroundBundleTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	_ = ctx
	args := cloneMetaMap(input.(map[string]any))
	t.calls = append(t.calls, args)
	return ToolExecutionResult{
		DisplayText: "started test verification bundle",
		Meta:        cloneMetaMap(t.meta),
	}, t.err
}

func TestReadFileToolRoutesOwnedLookupIntoSpecialistWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	repoRoot := t.TempDir()
	ctx := context.Background()

	if _, err := runGitCommand(ctx, repoRoot, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "config", "user.email", "kernforge-test@example.com"); err != nil {
		t.Fatalf("git config user.email: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "config", "user.name", "Kernforge Test"); err != nil {
		t.Fatalf("git config user.name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile main.go: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "add", "main.go"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	cfg := DefaultConfig(repoRoot)
	cfg.WorktreeIsolation.Enabled = boolPtr(true)
	cfg.WorktreeIsolation.RootDir = filepath.Join(t.TempDir(), "managed")
	sess := NewSession(repoRoot, "openai", "gpt-5.4", "", "default")
	sess.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{
				ID:                 "plan-01",
				Title:              "Patch main.go",
				Kind:               "edit",
				Status:             "ready",
				EditableLeasePaths: []string{"main.go"},
				LastUpdated:        time.Now(),
			},
		},
		LastUpdated: time.Now(),
	}
	rt := &runtimeState{
		cfg:     cfg,
		session: sess,
		workspace: Workspace{
			BaseRoot: repoRoot,
			Root:     repoRoot,
		},
	}
	rt.syncWorkspaceFromSession()

	route, err := rt.resolveEditTarget(EditRoutingRequest{
		Path:        "main.go",
		OwnerNodeID: "plan-01",
		ForLookup:   true,
	})
	if err != nil {
		t.Fatalf("resolveEditTarget: %v", err)
	}
	if err := os.WriteFile(route.AbsolutePath, []byte("package worker\n"), 0o644); err != nil {
		t.Fatalf("WriteFile routed file: %v", err)
	}

	tool := NewReadFileTool(rt.workspace)
	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path":          "main.go",
		"owner_node_id": "plan-01",
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if !strings.Contains(result.DisplayText, "package worker") {
		t.Fatalf("expected read_file to read from the specialist worktree, got %q", result.DisplayText)
	}
	if toolMetaString(result.Meta, "path") != "main.go" {
		t.Fatalf("expected worktree-relative path metadata, got %#v", result.Meta)
	}

	manager := newWorktreeManager(cfg)
	t.Cleanup(func() {
		for _, lease := range rt.session.SpecialistWorktrees {
			_ = manager.Remove(context.Background(), repoRoot, SessionWorktree{
				Root:    lease.Root,
				Branch:  lease.Branch,
				Managed: true,
			})
		}
	})
}

func TestMaybeRunInteractiveParallelEditableWorkersDefersOverlappingSecondaryEditNode(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	session.TaskState = &TaskState{
		ExecutorFocusNode:        "plan-01",
		ExecutorParallelNodes:    []string{"plan-02"},
		ExecutorParallelGuidance: []string{"plan-02: keep this secondary edit lane scoped to telemetry/provider.man"},
	}
	session.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{
				ID:                 "plan-01",
				Title:              "Patch telemetry/provider.man",
				Kind:               "edit",
				Status:             "in_progress",
				EditableLeasePaths: []string{"telemetry/provider.man"},
				LastUpdated:        time.Now(),
			},
			{
				ID:                 "plan-02",
				Title:              "Update telemetry/provider.man and refresh schema",
				Kind:               "edit",
				Status:             "ready",
				EditableLeasePaths: []string{"telemetry/provider.man"},
				LastUpdated:        time.Now(),
			},
		},
		LastUpdated: time.Now(),
	}
	session.TaskGraph.Normalize()

	agent := &Agent{
		Config: Config{
			Model:     "gpt-test",
			MaxTokens: 1024,
		},
		Session: session,
		Tools: NewToolRegistry(
			NewReadFileTool(Workspace{BaseRoot: "C:\\workspace", Root: "C:\\workspace"}),
			NewListFilesTool(Workspace{BaseRoot: "C:\\workspace", Root: "C:\\workspace"}),
			NewGrepTool(Workspace{BaseRoot: "C:\\workspace", Root: "C:\\workspace"}),
			NewApplyPatchTool(Workspace{BaseRoot: "C:\\workspace", Root: "C:\\workspace"}),
		),
	}

	if err := agent.maybeRunInteractiveParallelEditableWorkers(context.Background(), "executor"); err != nil {
		t.Fatalf("maybeRunInteractiveParallelEditableWorkers: %v", err)
	}

	node, ok := session.TaskGraph.Node("plan-02")
	if !ok {
		t.Fatalf("expected deferred secondary node to remain available")
	}
	if canonicalTaskNodeStatus(node.Status) != "ready" {
		t.Fatalf("expected overlapping node to stay ready but deferred, got %#v", node)
	}
	if !strings.Contains(strings.ToLower(node.EditableWorkerSummary), "overlaps with plan-01") {
		t.Fatalf("expected overlap reason to be recorded, got %#v", node)
	}
	if slicesContains(session.TaskState.ExecutorParallelNodes, "plan-02") {
		t.Fatalf("expected deferred node to be removed from executor parallel assignments, got %#v", session.TaskState)
	}
}

func TestMaybeRunInteractiveParallelEditableWorkersAppliesPatchForSecondaryEditNodeAndStartsVerificationBundle(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "driver"), 0o755); err != nil {
		t.Fatalf("MkdirAll driver: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "driver", "monitor.inf"), []byte("Version=1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile monitor.inf: %v", err)
	}

	session := NewSession(root, "openai", "gpt-test", "", "default")
	session.TaskState = &TaskState{
		ExecutorFocusNode:     "plan-01",
		ExecutorParallelNodes: []string{"plan-02"},
	}
	session.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{
				ID:                 "plan-01",
				Title:              "Patch telemetry/provider.man",
				Kind:               "edit",
				Status:             "in_progress",
				EditableLeasePaths: []string{"telemetry/provider.man"},
				LastUpdated:        time.Now(),
			},
			{
				ID:                 "plan-02",
				Title:              "Patch driver/monitor.inf",
				Kind:               "edit",
				Status:             "ready",
				EditableLeasePaths: []string{"driver/monitor.inf"},
				LastUpdated:        time.Now(),
			},
		},
		LastUpdated: time.Now(),
	}
	session.TaskGraph.Normalize()

	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	bgJob := BackgroundShellJob{
		ID:             "job-verify-1",
		CommandSummary: "echo Driver-related changes detected. Review signing, symbols, verifier settings, and deployment readiness.",
		Status:         "running",
		OwnerNodeID:    "plan-02",
	}
	bgBundle := BackgroundShellBundle{
		ID:               "bundle-verify-1",
		Summary:          bgJob.CommandSummary,
		CommandSummaries: []string{bgJob.CommandSummary},
		JobIDs:           []string{bgJob.ID},
		OwnerNodeID:      "plan-02",
		OwnerLeasePaths:  []string{"driver/monitor.inf"},
		Status:           "running",
		LastSummary:      "completed=0 running=1 failed=0 canceled=0 total=1",
		VerificationLike: true,
	}
	bgTool := &recordingBackgroundBundleTool{
		meta: buildBackgroundBundleMeta(bgBundle, []BackgroundShellJob{bgJob}, map[string]any{
			"tool_name":    "run_shell_bundle_background",
			"plan_effect":  "progress",
			"result_class": "background_start",
		}),
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-1",
						Name:      "read_file",
						Arguments: `{"path":"driver/monitor.inf"}`,
					}},
				},
				StopReason: "tool_use",
			},
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-2",
						Name:      "apply_patch",
						Arguments: `{"patch":"*** Begin Patch\n*** Update File: driver/monitor.inf\n@@\n-Version=1\n+Version=2\n*** End Patch\n"}`,
					}},
				},
				StopReason: "tool_use",
			},
		},
	}
	agent := &Agent{
		Config: Config{
			Model:     "gpt-test",
			MaxTokens: 1024,
		},
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Session:        session,
		Tools: NewToolRegistry(
			NewReadFileTool(ws),
			NewListFilesTool(ws),
			NewGrepTool(ws),
			NewApplyPatchTool(ws),
			bgTool,
		),
	}

	if err := agent.maybeRunInteractiveParallelEditableWorkers(context.Background(), "executor"); err != nil {
		t.Fatalf("maybeRunInteractiveParallelEditableWorkers: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "driver", "monitor.inf"))
	if err != nil {
		t.Fatalf("ReadFile monitor.inf: %v", err)
	}
	if !strings.Contains(string(data), "Version=2") {
		t.Fatalf("expected worker patch to apply, got %q", string(data))
	}
	node, ok := session.TaskGraph.Node("plan-02")
	if !ok {
		t.Fatalf("expected secondary edit node to remain available")
	}
	if canonicalTaskNodeStatus(node.Status) != "in_progress" {
		t.Fatalf("expected secondary edit node to reopen as in_progress once verification starts, got %#v", node)
	}
	if !strings.Contains(strings.ToLower(node.EditableWorkerSummary), "driver/monitor.inf") {
		t.Fatalf("expected editable worker evidence on node, got %#v", node)
	}
	if !containsTaskStateEntry(session.TaskState.PendingChecks, verificationPendingCheck) {
		t.Fatalf("expected verification pending check after worker edit, got %#v", session.TaskState)
	}
	if len(bgTool.calls) != 1 {
		t.Fatalf("expected one automatic verification bundle start, got %d", len(bgTool.calls))
	}
	if got := strings.TrimSpace(stringValue(bgTool.calls[0], "owner_node_id")); got != "plan-02" {
		t.Fatalf("expected verification bundle to stay attached to plan-02, got %#v", bgTool.calls[0])
	}
	if !boolValue(bgTool.calls[0], "verification_like", false) {
		t.Fatalf("expected parallel editable verification bundle to be marked verification_like, got %#v", bgTool.calls[0])
	}
	rawCommands, err := json.Marshal(bgTool.calls[0]["commands"])
	if err != nil {
		t.Fatalf("Marshal commands: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(rawCommands)), "driver") {
		t.Fatalf("expected security-aware driver verification commands, got %s", string(rawCommands))
	}
	bundle, ok := session.BackgroundBundle("bundle-verify-1")
	if !ok {
		t.Fatalf("expected verification bundle metadata to attach to the session")
	}
	if bundle.OwnerNodeID != "plan-02" || bundle.Status != "running" {
		t.Fatalf("expected running verification bundle on plan-02, got %#v", bundle)
	}
	if !bundle.VerificationLike {
		t.Fatalf("expected attached verification bundle metadata to keep verification_like, got %#v", bundle)
	}
	node, ok = session.TaskGraph.Node("bundle:bundle-verify-1")
	if !ok {
		t.Fatalf("expected task graph bundle node to be attached")
	}
	if canonicalTaskNodeStatus(node.Status) != "running" {
		t.Fatalf("expected bundle node to stay running, got %#v", node)
	}
	if len(reviewer.requests) != 2 {
		t.Fatalf("expected two specialist worker model turns, got %d", len(reviewer.requests))
	}
	for _, def := range reviewer.requests[0].Tools {
		if !parallelEditableWorkerAllowsTool(def.Name) {
			t.Fatalf("expected only editable-worker tools, got %#v", reviewer.requests[0].Tools)
		}
	}
}

func containsTaskStateEntry(items []string, target string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func slicesContains(items []string, target string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}
