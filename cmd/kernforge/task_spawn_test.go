package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func newSpawnTaskTestWorkspace(t *testing.T) (Workspace, *Session, *SessionStore) {
	t.Helper()
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{
		BaseRoot:    root,
		Root:        root,
		GoalSession: session,
		GoalStore:   store,
	}
	return ws, session, store
}

func decodeSpawnTaskResponse(t *testing.T, out string) spawnTaskToolResponse {
	t.Helper()
	var resp spawnTaskToolResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode spawn task response: %v\n%s", err, out)
	}
	return resp
}

func TestSpawnTaskReturnsIDImmediatelyAndGetTaskSurfacesResult(t *testing.T) {
	ws, session, store := newSpawnTaskTestWorkspace(t)
	ctx := context.Background()

	out, err := NewSpawnTaskTool(ws).Execute(ctx, map[string]any{
		"objective": "investigate the read-only tool surface",
	})
	if err != nil {
		t.Fatalf("spawn_task: %v", err)
	}
	spawned := decodeSpawnTaskResponse(t, out)
	if spawned.Task == nil || strings.TrimSpace(spawned.Task.ID) == "" {
		t.Fatalf("spawn_task must return a task id, got %#v", spawned)
	}
	// spawn_task returns the queued view immediately (non-blocking contract).
	if spawned.Task.Status != spawnTaskStatusQueued {
		t.Fatalf("spawn_task should return queued status, got %q", spawned.Task.Status)
	}
	if spawned.Task.Depth != 1 {
		t.Fatalf("expected depth 1 for a task spawned from the root session, got %d", spawned.Task.Depth)
	}
	taskID := spawned.Task.ID

	// The session must persist the task.
	if _, err := store.Load(session.ID); err != nil {
		t.Fatalf("spawn_task should persist session: %v", err)
	}

	getOut, err := NewGetTaskTool(ws).Execute(ctx, map[string]any{"task_id": taskID})
	if err != nil {
		t.Fatalf("get_task: %v", err)
	}
	got := decodeSpawnTaskResponse(t, getOut)
	if got.Task == nil {
		t.Fatalf("get_task must return a task, got %#v", got)
	}
	if got.Task.Status != spawnTaskStatusCompleted {
		t.Fatalf("expected completed status after bounded execution, got %q (err=%q)", got.Task.Status, got.Task.Error)
	}
	if !strings.Contains(got.Task.Result, "read-only") {
		t.Fatalf("expected a read-only investigation result, got %q", got.Task.Result)
	}

	// A spawn node must appear in the task graph.
	if node, ok := session.TaskGraph.Node("spawn:" + taskID); !ok || node.Kind != "spawn" {
		t.Fatalf("expected a kind=spawn task-graph node, got ok=%t node=%#v", ok, node)
	}
}

func TestSpawnTaskDepthCapRejectsFourthLevel(t *testing.T) {
	ws, session, _ := newSpawnTaskTestWorkspace(t)
	// Simulate a session already at the maximum depth so the next spawn would be
	// depth 4.
	session.SpawnedTaskDepth = maxSpawnTaskDepth

	_, err := NewSpawnTaskTool(ws).Execute(context.Background(), map[string]any{
		"objective": "go one level too deep",
	})
	if err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("expected depth-cap rejection, got %v", err)
	}
	if len(session.SpawnedTasks) != 0 {
		t.Fatalf("a rejected spawn must not persist a task, got %#v", session.SpawnedTasks)
	}
}

func TestSpawnTaskTokenBudgetCappedAndNotInherited(t *testing.T) {
	ws, _, _ := newSpawnTaskTestWorkspace(t)
	ctx := context.Background()

	// No parent budget -> requesting a budget is rejected (never inherited).
	if _, err := NewSpawnTaskTool(ws).Execute(ctx, map[string]any{
		"objective":    "budget without a parent budget",
		"token_budget": 100,
	}); err == nil || !strings.Contains(err.Error(), "no token budget to subdivide") {
		t.Fatalf("expected rejection when parent has no budget, got %v", err)
	}

	// Give the parent an active goal budget so the cap is 20 percent of it.
	if _, err := NewCreateGoalTool(ws).Execute(ctx, map[string]any{
		"objective":    "parent goal with a budget",
		"token_budget": 1000,
	}); err != nil {
		t.Fatalf("create_goal: %v", err)
	}

	// 201 exceeds the 20 percent cap (200) and must be rejected, not clamped.
	if _, err := NewSpawnTaskTool(ws).Execute(ctx, map[string]any{
		"objective":    "over budget request",
		"token_budget": 201,
	}); err == nil || !strings.Contains(err.Error(), "exceeds the maximum") {
		t.Fatalf("expected over-cap token_budget rejection, got %v", err)
	}

	// A within-cap budget is accepted and the task carries exactly that budget
	// (no implicit inheritance of the parent's 1000).
	out, err := NewSpawnTaskTool(ws).Execute(ctx, map[string]any{
		"objective":    "within budget request",
		"token_budget": 150,
	})
	if err != nil {
		t.Fatalf("spawn_task within budget: %v", err)
	}
	spawned := decodeSpawnTaskResponse(t, out)
	if spawned.Task == nil || spawned.Task.TokenBudget != 150 {
		t.Fatalf("expected the task to carry the requested 150 budget, got %#v", spawned.Task)
	}
	if spawned.Task.TokenBudget == 1000 {
		t.Fatalf("task budget must not inherit the parent budget")
	}
}

func TestSpawnTaskReadOnlyRegistryExcludesWriteShellGitByDefault(t *testing.T) {
	ws, _, _ := newSpawnTaskTestWorkspace(t)
	registry := spawnTaskReadOnlyRegistry(ws, false)

	for _, allowed := range []string{"read_file", "grep", "list_files", "lsp_nav"} {
		if !toolContractRegistryHasTool(registry, allowed) {
			t.Fatalf("read-only registry must expose %q", allowed)
		}
	}
	for _, forbidden := range []string{
		"write_file", "apply_patch", "replace_in_file", "run_shell",
		"run_shell_background", "git_add", "git_commit", "git_push",
	} {
		if toolContractRegistryHasTool(registry, forbidden) {
			t.Fatalf("read-only registry must NOT expose %q by default", forbidden)
		}
	}

	// Even with the opt-in flag set, a non-write permission mode keeps the task
	// read-only.
	if spawnTaskPermitsWrite(Workspace{Perms: NewPermissionManager(ModeDefault, nil)}) {
		t.Fatalf("default permission mode must not authorize spawned-task writes")
	}
	if spawnTaskPermitsWrite(Workspace{Perms: NewPermissionManager(ModePlan, nil)}) {
		t.Fatalf("plan permission mode must not authorize spawned-task writes")
	}
	if !spawnTaskPermitsWrite(Workspace{Perms: NewPermissionManager(ModeAcceptEdits, nil)}) {
		t.Fatalf("acceptEdits mode should authorize spawned-task writes")
	}
}

func TestSpawnTaskWriteOptInIgnoredWithoutPermission(t *testing.T) {
	ws, _, _ := newSpawnTaskTestWorkspace(t)
	ws.Perms = NewPermissionManager(ModeDefault, nil)

	out, err := NewSpawnTaskTool(ws).Execute(context.Background(), map[string]any{
		"objective":   "request writes without authorization",
		"allow_write": true,
	})
	if err != nil {
		t.Fatalf("spawn_task: %v", err)
	}
	spawned := decodeSpawnTaskResponse(t, out)
	if spawned.Task == nil || spawned.Task.AllowWrite {
		t.Fatalf("write opt-in must be dropped without permission authorization, got %#v", spawned.Task)
	}
}

func TestCancelTaskTransitionsToCanceled(t *testing.T) {
	ws, session, _ := newSpawnTaskTestWorkspace(t)
	ctx := context.Background()

	// Pre-seed a non-terminal task directly so cancel has something to act on
	// regardless of the synchronous executor.
	session.UpsertSpawnedTask(SpawnedTask{
		ID:              "task-cancel-001",
		Objective:       "cancel me",
		ParentSessionID: session.ID,
		Depth:           1,
		Status:          spawnTaskStatusQueued,
	})

	out, err := NewCancelTaskTool(ws).Execute(ctx, map[string]any{"task_id": "task-cancel-001"})
	if err != nil {
		t.Fatalf("cancel_task: %v", err)
	}
	canceled := decodeSpawnTaskResponse(t, out)
	if canceled.Task == nil || canceled.Task.Status != spawnTaskStatusCanceled {
		t.Fatalf("expected canceled status, got %#v", canceled.Task)
	}

	// Cancel again must fail because it is terminal.
	if _, err := NewCancelTaskTool(ws).Execute(ctx, map[string]any{"task_id": "task-cancel-001"}); err == nil || !strings.Contains(err.Error(), "already canceled") {
		t.Fatalf("expected re-cancel rejection, got %v", err)
	}
}

func TestSpawnTaskSubagentStartHookDenyBlocksSpawn(t *testing.T) {
	ws, session, _ := newSpawnTaskTestWorkspace(t)
	hooks := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "deny-spawn",
					Events: []HookEvent{HookSubagentStart},
					Action: HookAction{Type: "deny", Message: "spawn blocked by policy"},
				},
			},
		},
		Workspace: ws,
	}
	ws.RunHook = hooks.Run

	_, err := NewSpawnTaskTool(ws).Execute(context.Background(), map[string]any{
		"objective": "should be blocked at start",
	})
	if err == nil || !strings.Contains(err.Error(), "SubagentStart hook") {
		t.Fatalf("expected SubagentStart deny to block the spawn, got %v", err)
	}
	if len(session.SpawnedTasks) != 0 {
		t.Fatalf("a denied spawn must not persist a task, got %#v", session.SpawnedTasks)
	}
}

func TestSpawnTaskLoopDetectionRejectsDuplicateObjective(t *testing.T) {
	ws, session, _ := newSpawnTaskTestWorkspace(t)

	// An active (non-terminal) task with the same objective blocks a re-entry.
	session.UpsertSpawnedTask(SpawnedTask{
		ID:              "task-active-001",
		Objective:       "scan the driver entry points",
		ParentSessionID: session.ID,
		Depth:           1,
		Status:          spawnTaskStatusRunning,
	})

	_, err := NewSpawnTaskTool(ws).Execute(context.Background(), map[string]any{
		"objective": "Scan the driver entry points",
	})
	if err == nil || !strings.Contains(err.Error(), "same objective already exists") {
		t.Fatalf("expected loop-detection rejection, got %v", err)
	}
}

func TestSpawnTaskConcurrencyCapRejectsExcess(t *testing.T) {
	ws, session, _ := newSpawnTaskTestWorkspace(t)
	for i := 0; i < maxConcurrentSpawnedTasks; i++ {
		session.UpsertSpawnedTask(SpawnedTask{
			ID:              "task-active-" + string(rune('a'+i)),
			Objective:       "active objective " + string(rune('a'+i)),
			ParentSessionID: session.ID,
			Depth:           1,
			Status:          spawnTaskStatusRunning,
		})
	}
	_, err := NewSpawnTaskTool(ws).Execute(context.Background(), map[string]any{
		"objective": "one too many concurrent tasks",
	})
	if err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("expected concurrency-cap rejection, got %v", err)
	}
}

func TestListAndUpdateTask(t *testing.T) {
	ws, _, _ := newSpawnTaskTestWorkspace(t)
	ctx := context.Background()

	spawnOut, err := NewSpawnTaskTool(ws).Execute(ctx, map[string]any{"objective": "first objective"})
	if err != nil {
		t.Fatalf("spawn_task: %v", err)
	}
	taskID := decodeSpawnTaskResponse(t, spawnOut).Task.ID

	listOut, err := NewListTasksTool(ws).Execute(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("list_tasks: %v", err)
	}
	listed := decodeSpawnTaskResponse(t, listOut)
	if len(listed.Tasks) != 1 {
		t.Fatalf("expected exactly one task, got %#v", listed.Tasks)
	}

	// The synchronous executor completes the task, so update_task (non-terminal
	// only) must reject it. Verify the terminal guard.
	if _, err := NewUpdateTaskTool(ws).Execute(ctx, map[string]any{
		"task_id":   taskID,
		"objective": "refined objective",
	}); err == nil || !strings.Contains(err.Error(), "already completed") {
		t.Fatalf("expected update_task to reject a completed task, got %v", err)
	}
}
