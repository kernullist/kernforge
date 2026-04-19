package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDurabilityEvalTaskGraphPersistsBundleNodesAcrossSaveLoad(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "provider", "model", "", "default")
	session.TaskState = &TaskState{}
	session.SetSharedPlan([]PlanItem{
		{Step: "Inspect the code", Status: "in_progress"},
		{Step: "Run background verification", Status: "pending"},
	})

	bundle := BackgroundShellBundle{
		ID:               "bundle-1",
		Summary:          "go test ./...",
		CommandSummaries: []string{"go test ./..."},
		JobIDs:           []string{"job-1"},
		Status:           "running",
		LastSummary:      "completed=0 running=1 failed=0 total=1",
		StartedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	job := BackgroundShellJob{
		ID:             "job-1",
		Command:        "go test ./...",
		CommandSummary: "go test ./...",
		Status:         "running",
		StartedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	session.UpsertBackgroundBundle(bundle)
	session.AttachBackgroundBundle(bundle, []BackgroundShellJob{job})

	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.TaskGraph == nil {
		t.Fatalf("expected task graph to persist")
	}
	if _, ok := reloaded.TaskGraph.Node("bundle:bundle-1"); !ok {
		t.Fatalf("expected bundle node to persist, got %#v", reloaded.TaskGraph.Nodes)
	}
	if len(reloaded.TaskGraph.Nodes) < 3 {
		t.Fatalf("expected plan nodes plus bundle node, got %#v", reloaded.TaskGraph.Nodes)
	}
}

func TestDurabilityEvalToolMetaPersistsAcrossSessionStore(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{
		Role:       "tool",
		ToolCallID: "call-1",
		ToolName:   "check_shell_bundle",
		Text:       "bundle: bundle-1\nbundle_status: running",
		ToolMeta: map[string]any{
			"bundle_id":     "bundle-1",
			"bundle_status": "running",
			"running":       1,
			"failed":        0,
		},
	}}
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reloaded.Messages) != 1 {
		t.Fatalf("expected one message, got %d", len(reloaded.Messages))
	}
	if got := toolMetaString(reloaded.Messages[0].ToolMeta, "bundle_id"); got != "bundle-1" {
		t.Fatalf("expected persisted bundle_id, got %#v", reloaded.Messages[0].ToolMeta)
	}
	if got := toolMetaInt(reloaded.Messages[0].ToolMeta, "running"); got != 1 {
		t.Fatalf("expected persisted running count 1, got %#v", reloaded.Messages[0].ToolMeta)
	}
}

func TestDurabilityEvalEditMarksBackgroundBundlesStale(t *testing.T) {
	session := NewSession("C:\\workspace", "provider", "model", "", "default")
	session.TaskState = &TaskState{}
	session.SetSharedPlan([]PlanItem{
		{Step: "Apply the fix", Status: "in_progress"},
		{Step: "Verify the result", Status: "pending"},
	})

	bundle := BackgroundShellBundle{
		ID:               "bundle-1",
		Summary:          "go test ./...",
		CommandSummaries: []string{"go test ./..."},
		JobIDs:           []string{"job-1"},
		Status:           "running",
		LastSummary:      "completed=0 running=1 failed=0 total=1",
	}
	job := BackgroundShellJob{
		ID:             "job-1",
		CommandSummary: "go test ./...",
		Status:         "running",
	}
	session.UpsertBackgroundBundle(bundle)
	session.AttachBackgroundBundle(bundle, []BackgroundShellJob{job})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	agent.noteToolExecutionResultDetailed(ToolCall{Name: "apply_patch"}, ToolExecutionResult{
		DisplayText: "Updated 1 file.",
	}, nil)

	updatedBundle, ok := session.BackgroundBundle("bundle-1")
	if !ok {
		t.Fatalf("expected bundle to remain in session")
	}
	if updatedBundle.Status != "stale" {
		t.Fatalf("expected bundle to become stale after edit, got %#v", updatedBundle)
	}
	node, ok := session.TaskGraph.Node("bundle:bundle-1")
	if !ok {
		t.Fatalf("expected bundle node in task graph")
	}
	if node.Status != "stale" {
		t.Fatalf("expected task-graph bundle node to become stale, got %#v", node)
	}
	if !strings.Contains(updatedBundle.LifecycleNote, "newer edit") {
		t.Fatalf("expected lifecycle note to mention newer edit, got %#v", updatedBundle)
	}
}

func TestDurabilityEvalMicroWorkerBriefAttachesToTaskGraphNode(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "provider", "model", "", "default")
	session.TaskState = &TaskState{
		Goal:        "Keep the long verification workflow recoverable.",
		PlanSummary: "1. Poll the running verification bundle\n2. Summarize the result",
		Phase:       "execution",
	}
	session.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{ID: "plan-01", Title: "Poll the running verification bundle", Kind: "verification", Status: "in_progress"},
			{ID: "plan-02", Title: "Inspect the failing output", Kind: "inspection", Status: "ready"},
			{ID: "bundle:bundle-1", Title: "go test ./...", Kind: "background_bundle", Status: "in_progress"},
		},
		LastUpdated: time.Now(),
	}
	session.TaskGraph.Normalize()
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "- Watch for stale verification output\n- Next: poll the running bundle once before concluding\n- This node gates whether the final answer is trustworthy",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "- Watch for the first failing line\n- Next: inspect the latest failing output before editing\n- This node narrows the repair scope",
				},
				StopReason: "stop",
			},
		},
	}
	agent := &Agent{
		Config: Config{
			Model:     "model",
			MaxTokens: 512,
		},
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Session:        session,
		Store:          store,
	}

	if err := agent.maybeRunInteractiveMicroWorkers(context.Background(), "replan"); err != nil {
		t.Fatalf("maybeRunInteractiveMicroWorkers: %v", err)
	}
	for _, nodeID := range []string{"plan-01", "plan-02"} {
		node, ok := session.TaskGraph.Node(nodeID)
		if !ok {
			t.Fatalf("expected task graph node %s to exist", nodeID)
		}
		if strings.TrimSpace(node.MicroWorkerBrief) == "" {
			t.Fatalf("expected micro-worker brief for %s, got %#v", nodeID, node)
		}
	}
	if len(reviewer.requests) != 2 {
		t.Fatalf("expected two reviewer micro-worker requests, got %d", len(reviewer.requests))
	}
}

func TestDurabilityEvalExecutorFocusPersistsAcrossSessionStore(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "provider", "model", "", "default")
	session.TaskState = &TaskState{
		Goal: "Keep the verification workflow recoverable.",
	}
	session.SetSharedPlan([]PlanItem{
		{Step: "Inspect the failing output", Status: "completed"},
		{Step: "Run verification bundle", Status: "pending"},
		{Step: "Summarize", Status: "pending"},
	})
	agent := &Agent{
		Session: session,
		Store:   store,
	}

	if err := agent.syncTaskExecutorFocus(); err != nil {
		t.Fatalf("syncTaskExecutorFocus: %v", err)
	}
	if strings.TrimSpace(session.TaskState.ExecutorFocusNode) == "" {
		t.Fatalf("expected executor focus to be recorded")
	}
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.TaskState == nil || strings.TrimSpace(reloaded.TaskState.ExecutorFocusNode) == "" {
		t.Fatalf("expected executor focus to persist, got %#v", reloaded.TaskState)
	}
	if strings.TrimSpace(reloaded.TaskState.ExecutorGuidance) == "" {
		t.Fatalf("expected executor guidance to persist, got %#v", reloaded.TaskState)
	}
}

func TestDurabilityEvalPlanNodeRuntimeStatePersistsAcrossPlanSync(t *testing.T) {
	session := NewSession("C:\\workspace", "provider", "model", "", "default")
	session.SetSharedPlan([]PlanItem{
		{Step: "Run verification", Status: "in_progress"},
		{Step: "Summarize", Status: "pending"},
	})
	graph := session.EnsureTaskGraph()
	if graph == nil {
		t.Fatalf("expected task graph")
	}
	node, ok := graph.Node("plan-01")
	if !ok {
		t.Fatalf("expected first plan node")
	}
	node.MicroWorkerBrief = "Poll the verification bundle before concluding."
	node.LifecycleNote = "bundle completed=0 running=1 failed=0 total=1"
	node.LinkedBundleIDs = []string{"bundle-1"}
	node.LinkedJobIDs = []string{"job-1"}
	graph.UpsertNode(node)

	session.ensureSharedPlanInProgress()

	updated, ok := session.TaskGraph.Node("plan-01")
	if !ok {
		t.Fatalf("expected synced plan node")
	}
	if updated.MicroWorkerBrief == "" || updated.LifecycleNote == "" {
		t.Fatalf("expected runtime state to survive plan sync, got %#v", updated)
	}
	if len(updated.LinkedBundleIDs) != 1 || updated.LinkedBundleIDs[0] != "bundle-1" {
		t.Fatalf("expected linked bundle ids to persist, got %#v", updated)
	}
}

func TestDurabilityEvalFailedBundleBlocksLinkedVerificationPlanNode(t *testing.T) {
	session := NewSession("C:\\workspace", "provider", "model", "", "default")
	session.TaskState = &TaskState{}
	session.SetSharedPlan([]PlanItem{
		{Step: "Run verification bundle", Status: "in_progress"},
		{Step: "Summarize", Status: "pending"},
	})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	agent.noteToolExecutionResultDetailed(ToolCall{Name: "check_shell_bundle"}, ToolExecutionResult{
		DisplayText: "bundle: bundle-1\nbundle_status: failed\nsummary: completed=0 running=0 failed=1 total=1",
		Meta: map[string]any{
			"bundle_id":                "bundle-1",
			"bundle_status":            "failed",
			"bundle_summary":           "completed=0 running=0 failed=1 total=1",
			"bundle_command_summaries": []string{"go test ./..."},
			"bundle_job_ids":           []string{"job-1"},
			"job_status": []map[string]any{{
				"id":              "job-1",
				"status":          "failed",
				"command_summary": "go test ./...",
			}},
			"failed":  1,
			"running": 0,
		},
	}, nil)

	node, ok := session.TaskGraph.Node("plan-01")
	if !ok {
		t.Fatalf("expected linked verification plan node")
	}
	if node.Status != "blocked" {
		t.Fatalf("expected linked plan node to become blocked, got %#v", node)
	}
	if !strings.Contains(node.LifecycleNote, "failed=1") {
		t.Fatalf("expected lifecycle note to include bundle summary, got %#v", node)
	}
}

func TestDurabilityEvalFocusedBlockedNodeReturnsToInProgressAfterSuccessfulInspect(t *testing.T) {
	session := NewSession("C:\\workspace", "provider", "model", "", "default")
	session.TaskState = &TaskState{
		ExecutorFocusNode: "plan-02",
		ExecutorAction:    "recover",
	}
	session.SetSharedPlan([]PlanItem{
		{Step: "Inspect the failure summary", Status: "completed"},
		{Step: "Recover the blocked verification step", Status: "pending"},
		{Step: "Summarize", Status: "pending"},
	})
	session.TaskGraph.SetNodeLifecycle("plan-02", "blocked", "Verification output was stale.")
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	agent.noteToolExecutionResultDetailed(ToolCall{Name: "read_file"}, ToolExecutionResult{
		DisplayText: "latest failing line",
		Meta: map[string]any{
			"effect": "inspect",
			"path":   "logs/verify.txt",
		},
	}, nil)

	node, ok := session.TaskGraph.Node("plan-02")
	if !ok {
		t.Fatalf("expected focused plan node to exist")
	}
	if node.Status != "in_progress" {
		t.Fatalf("expected focused blocked node to return to in_progress, got %#v", node)
	}
	if !strings.Contains(node.LifecycleNote, "Recovered after") {
		t.Fatalf("expected recovery lifecycle note, got %#v", node)
	}
	if len(session.Plan) < 2 || session.Plan[1].Status != "in_progress" {
		t.Fatalf("expected shared plan to resume on recovered node, got %#v", session.Plan)
	}
}

func TestDurabilityEvalBackgroundBundleAttachesToExactOwnerNodeFromMeta(t *testing.T) {
	session := NewSession("C:\\workspace", "provider", "model", "", "default")
	session.TaskState = &TaskState{}
	session.SetSharedPlan([]PlanItem{
		{Step: "Run generic verification", Status: "in_progress"},
		{Step: "Run focused verification", Status: "pending"},
		{Step: "Summarize", Status: "pending"},
	})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	agent.noteToolExecutionResultDetailed(ToolCall{Name: "run_shell_background"}, ToolExecutionResult{
		DisplayText: "started background shell job job-1 [running]\nbundle: bundle-1",
		Meta: map[string]any{
			"owner_node_id":            "plan-02",
			"job_id":                   "job-1",
			"job_status":               "running",
			"command_summary":          "go test ./focused",
			"bundle_id":                "bundle-1",
			"bundle_status":            "running",
			"bundle_summary":           "completed=0 running=1 failed=0 canceled=0 total=1",
			"bundle_job_ids":           []string{"job-1"},
			"bundle_command_summaries": []string{"go test ./focused"},
			"result_class":             "background_start",
			"plan_effect":              "progress",
		},
	}, nil)

	first, ok := session.TaskGraph.Node("plan-01")
	if !ok {
		t.Fatalf("expected first plan node")
	}
	second, ok := session.TaskGraph.Node("plan-02")
	if !ok {
		t.Fatalf("expected second plan node")
	}
	if slices.Contains(first.LinkedBundleIDs, "bundle-1") {
		t.Fatalf("expected bundle to avoid heuristic attachment to plan-01, got %#v", first)
	}
	if !slices.Contains(second.LinkedBundleIDs, "bundle-1") {
		t.Fatalf("expected exact owner node attachment on plan-02, got %#v", second)
	}
}

func TestDurabilityEvalNodeAwarePlanAdvancementUsesOwnerNodeID(t *testing.T) {
	session := NewSession("C:\\workspace", "provider", "model", "", "default")
	session.TaskState = &TaskState{}
	session.SetSharedPlan([]PlanItem{
		{Step: "Inspect", Status: "completed"},
		{Step: "Apply fix", Status: "in_progress"},
		{Step: "Verify", Status: "pending"},
	})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	agent.noteToolExecutionResultDetailed(ToolCall{Name: "apply_patch"}, ToolExecutionResult{
		DisplayText: "Updated 1 file.",
		Meta: map[string]any{
			"effect":                "edit",
			"owner_node_id":         "plan-02",
			"changed_paths":         []string{"main.go"},
			"requires_verification": true,
		},
	}, nil)

	if session.Plan[1].Status != "completed" || session.Plan[2].Status != "in_progress" {
		t.Fatalf("expected owner-node-aware advancement, got %#v", session.Plan)
	}
	node, ok := session.TaskGraph.Node("plan-02")
	if !ok || node.Status != "completed" {
		t.Fatalf("expected task graph node completion, got %#v", node)
	}
}

func TestDurabilityEvalParallelReadOnlyWorkerExecutesSecondaryNodeInspection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "worker_target.txt"), []byte("AntiTamperGuard evidence is present here.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "provider", "model", "", "default")
	session.TaskState = &TaskState{}
	session.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{ID: "plan-01", Title: "Run focused verification", Kind: "verification", Status: "in_progress"},
			{ID: "plan-02", Title: "Inspect AntiTamperGuard evidence", Kind: "inspection", Status: "ready"},
		},
		LastUpdated: time.Now(),
	}
	session.TaskGraph.Normalize()
	session.TaskState.SetExecutorFocus("plan-01", "continue", "primary verification is still active", "Continue the primary node.")
	session.TaskState.SetExecutorParallelAssignments([]string{"plan-02"}, []string{"plan-02: gather read-only evidence"})

	ws := Workspace{
		BaseRoot:      root,
		Root:          root,
		Shell:         "powershell",
		ReadHintSpans: 16,
		ToolHints:     &ToolHints{maxReadSpans: 16},
	}
	agent := &Agent{
		Config:    Config{},
		Tools:     buildRegistry(ws, nil),
		Workspace: ws,
		Session:   session,
	}

	if err := agent.maybeRunInteractiveParallelReadOnlyWorkers(context.Background(), "executor"); err != nil {
		t.Fatalf("maybeRunInteractiveParallelReadOnlyWorkers: %v", err)
	}

	node, ok := session.TaskGraph.Node("plan-02")
	if !ok {
		t.Fatalf("expected secondary node to exist")
	}
	if node.ReadOnlyWorkerTool != "grep" {
		t.Fatalf("expected grep evidence for secondary node, got %#v", node)
	}
	if !strings.Contains(strings.ToLower(node.ReadOnlyWorkerSummary), "matches") {
		t.Fatalf("expected worker summary to mention matches, got %#v", node)
	}
	if canonicalTaskNodeStatus(node.Status) != "in_progress" {
		t.Fatalf("expected inspection node to move to in_progress after evidence gathering, got %#v", node)
	}
	foundEvent := false
	for _, event := range session.TaskState.Events {
		if event.Kind == "parallel_worker_result" && event.NodeID == "plan-02" {
			foundEvent = true
			break
		}
	}
	if !foundEvent {
		t.Fatalf("expected parallel_worker_result event, got %#v", session.TaskState.Events)
	}
}

func TestDurabilityEvalParallelReadOnlyWorkerAvoidsGitStatusForGenericStatusNode(t *testing.T) {
	session := NewSession("C:\\workspace", "provider", "model", "", "default")
	node := TaskNode{
		ID:     "plan-02",
		Title:  "Inspect verification status for AntiTamperGuard",
		Kind:   "inspection",
		Status: "ready",
	}

	plan, ok := buildParallelReadOnlyWorkerPlan(session, node)
	if !ok {
		t.Fatalf("expected a read-only worker plan")
	}
	if plan.Call.Name == "git_status" {
		t.Fatalf("expected generic status wording to avoid git_status, got %#v", plan)
	}
}
