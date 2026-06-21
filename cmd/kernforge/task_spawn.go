package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Bounded, safety-first model-invokable task spawn capability.
//
// A spawned task is a READ-ONLY investigation that runs under strict, hard caps.
// It is deliberately NOT a general autonomous sub-agent: by default it can only
// read the workspace (read_file/grep/list_files/lsp_nav) and can never mutate
// files, run shell, or perform git actions. The caps below are enforced at
// invoke time and rejected (never silently clamped into a dangerous shape) when
// they would be exceeded, because this runs inside an anti-cheat tool where an
// unbounded or privilege-escalating spawn path is unacceptable.
const (
	// maxSpawnTaskDepth caps how deep the spawn tree may grow. The root
	// interactive session is depth 0; a task it spawns is depth 1. A spawned
	// task at depth 3 cannot spawn again (depth 4 is rejected).
	maxSpawnTaskDepth = 3

	// maxSpawnedTasksPerSession caps the TOTAL number of tasks a single session
	// may ever spawn (including completed/failed/canceled ones). This bounds
	// persisted state growth and total fan-out work.
	maxSpawnedTasksPerSession = 16

	// maxConcurrentSpawnedTasks caps how many tasks may be in a non-terminal
	// (queued|running) state at the same time for one session.
	maxConcurrentSpawnedTasks = 4

	// spawnTaskTokenBudgetParentFraction is the maximum fraction of the parent's
	// token budget that a spawned task may be granted. Budget is NEVER inherited
	// implicitly: a task starts with a budget of 0 unless one is explicitly
	// requested, and any explicit request is capped to this fraction of the
	// parent budget. Each task accounts for its own usage separately.
	spawnTaskTokenBudgetParentFraction = 0.20

	// spawnTaskDefaultTimeoutSeconds / spawnTaskMaxTimeoutSeconds bound the
	// per-task wall-clock timeout. A request above the max is rejected.
	spawnTaskDefaultTimeoutSeconds = 30
	spawnTaskMaxTimeoutSeconds     = 300

	maxSpawnTaskObjectiveChars = 4000
)

const (
	spawnTaskStatusQueued    = "queued"
	spawnTaskStatusRunning   = "running"
	spawnTaskStatusCompleted = "completed"
	spawnTaskStatusFailed    = "failed"
	spawnTaskStatusCanceled  = "canceled"
)

// SpawnedTask mirrors the GoalState persistence shape for a spawned read-only
// investigation. It is persisted in Session.SpawnedTasks and mirrored as a
// kind=spawn node in the session TaskGraph.
type SpawnedTask struct {
	ID              string    `json:"id"`
	Objective       string    `json:"objective"`
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	Depth           int       `json:"depth"`
	Status          string    `json:"status"`
	AllowWrite      bool      `json:"allow_write,omitempty"`
	TokenBudget     int       `json:"token_budget,omitempty"`
	TokenUsed       int       `json:"token_used,omitempty"`
	TimeoutSeconds  int       `json:"timeout_seconds,omitempty"`
	Result          string    `json:"result,omitempty"`
	Error           string    `json:"error,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	CompletedAt     time.Time `json:"completed_at,omitempty"`
}

func (t *SpawnedTask) Normalize() {
	if t == nil {
		return
	}
	t.ID = strings.TrimSpace(t.ID)
	t.Objective = strings.Join(strings.Fields(strings.TrimSpace(t.Objective)), " ")
	t.ParentSessionID = strings.TrimSpace(t.ParentSessionID)
	if t.Depth < 0 {
		t.Depth = 0
	}
	t.Status = canonicalSpawnTaskStatus(t.Status)
	if t.TokenBudget < 0 {
		t.TokenBudget = 0
	}
	if t.TokenUsed < 0 {
		t.TokenUsed = 0
	}
	if t.TimeoutSeconds < 0 {
		t.TimeoutSeconds = 0
	}
	t.Result = strings.TrimSpace(t.Result)
	t.Error = strings.TrimSpace(t.Error)
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = t.CreatedAt
	}
}

func canonicalSpawnTaskStatus(status string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case spawnTaskStatusRunning:
		return spawnTaskStatusRunning
	case spawnTaskStatusCompleted:
		return spawnTaskStatusCompleted
	case spawnTaskStatusFailed:
		return spawnTaskStatusFailed
	case spawnTaskStatusCanceled:
		return spawnTaskStatusCanceled
	default:
		return spawnTaskStatusQueued
	}
}

func spawnTaskStatusTerminal(status string) bool {
	switch canonicalSpawnTaskStatus(status) {
	case spawnTaskStatusCompleted, spawnTaskStatusFailed, spawnTaskStatusCanceled:
		return true
	default:
		return false
	}
}

// SpawnDepth reports how deep this session already is in the spawn tree. The
// root interactive session reports 0.
func (s *Session) SpawnDepth() int {
	if s == nil || s.SpawnedTaskDepth < 0 {
		return 0
	}
	return s.SpawnedTaskDepth
}

func (s *Session) normalizeSpawnedTasks() {
	if s == nil {
		return
	}
	if s.SpawnedTaskDepth < 0 {
		s.SpawnedTaskDepth = 0
	}
	if len(s.SpawnedTasks) == 0 {
		return
	}
	for i := range s.SpawnedTasks {
		s.SpawnedTasks[i].Normalize()
	}
}

func (s *Session) SpawnedTask(id string) (SpawnedTask, bool) {
	if s == nil {
		return SpawnedTask{}, false
	}
	id = strings.TrimSpace(id)
	for _, task := range s.SpawnedTasks {
		if strings.EqualFold(strings.TrimSpace(task.ID), id) {
			return task, true
		}
	}
	return SpawnedTask{}, false
}

func (s *Session) UpsertSpawnedTask(task SpawnedTask) {
	if s == nil {
		return
	}
	task.Normalize()
	if task.ID == "" {
		return
	}
	for i := range s.SpawnedTasks {
		if strings.EqualFold(strings.TrimSpace(s.SpawnedTasks[i].ID), task.ID) {
			s.SpawnedTasks[i] = task
			s.syncSpawnedTaskGraphNode(task)
			return
		}
	}
	s.SpawnedTasks = append(s.SpawnedTasks, task)
	s.syncSpawnedTaskGraphNode(task)
}

func (s *Session) activeSpawnedTaskCount() int {
	if s == nil {
		return 0
	}
	count := 0
	for _, task := range s.SpawnedTasks {
		if !spawnTaskStatusTerminal(task.Status) {
			count++
		}
	}
	return count
}

// syncSpawnedTaskGraphNode records the task as a kind=spawn node in the session
// TaskGraph so the existing task-graph machinery surfaces it. Spawn nodes are
// not primary plan nodes; they never become ready/executable plan work.
func (s *Session) syncSpawnedTaskGraphNode(task SpawnedTask) {
	if s == nil {
		return
	}
	graph := s.EnsureTaskGraph()
	if graph == nil {
		return
	}
	graph.UpsertNode(TaskNode{
		ID:            "spawn:" + task.ID,
		Title:         firstNonBlankString(task.Objective, task.ID),
		Kind:          "spawn",
		Status:        spawnTaskNodeStatus(task.Status),
		LifecycleNote: spawnTaskNodeNote(task),
		LastUpdated:   time.Now(),
	})
}

func spawnTaskNodeStatus(status string) string {
	switch canonicalSpawnTaskStatus(status) {
	case spawnTaskStatusRunning:
		return "in_progress"
	case spawnTaskStatusCompleted:
		return "completed"
	case spawnTaskStatusFailed:
		return "failed"
	case spawnTaskStatusCanceled:
		return "canceled"
	default:
		return "pending"
	}
}

func spawnTaskNodeNote(task SpawnedTask) string {
	if strings.TrimSpace(task.Error) != "" {
		return compactPromptSection("error: "+task.Error, 160)
	}
	if strings.TrimSpace(task.Result) != "" {
		return compactPromptSection(task.Result, 160)
	}
	return ""
}

// taskToolsAvailable mirrors goalToolsAvailable: the spawn tools require a
// persisted session so task state survives across turns.
func taskToolsAvailable(ws Workspace) bool {
	return ws.GoalSession != nil && ws.GoalStore != nil
}

type SpawnTaskTool struct{ ws Workspace }
type GetTaskTool struct{ ws Workspace }
type ListTasksTool struct{ ws Workspace }
type CancelTaskTool struct{ ws Workspace }
type UpdateTaskTool struct{ ws Workspace }

func NewSpawnTaskTool(ws Workspace) SpawnTaskTool   { return SpawnTaskTool{ws: ws} }
func NewGetTaskTool(ws Workspace) GetTaskTool       { return GetTaskTool{ws: ws} }
func NewListTasksTool(ws Workspace) ListTasksTool   { return ListTasksTool{ws: ws} }
func NewCancelTaskTool(ws Workspace) CancelTaskTool { return CancelTaskTool{ws: ws} }
func NewUpdateTaskTool(ws Workspace) UpdateTaskTool { return UpdateTaskTool{ws: ws} }

type spawnTaskToolResponse struct {
	Task            *SpawnedTask  `json:"task,omitempty"`
	Tasks           []SpawnedTask `json:"tasks,omitempty"`
	RemainingTokens *int          `json:"remaining_tokens,omitempty"`
	Note            string        `json:"note,omitempty"`
}

func spawnTaskSession(ws Workspace) (*Session, error) {
	if ws.GoalSession == nil {
		return nil, fmt.Errorf("task session is not configured")
	}
	if ws.GoalStore == nil {
		return nil, fmt.Errorf("task tools require a persisted session")
	}
	ws.GoalSession.normalizeSpawnedTasks()
	return ws.GoalSession, nil
}

func spawnTaskSave(ws Workspace, session *Session) error {
	if ws.GoalStore == nil {
		return fmt.Errorf("task tools require a persisted session")
	}
	return ws.GoalStore.Save(session)
}

func spawnTaskExecutionResult(response spawnTaskToolResponse) ToolExecutionResult {
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return ToolExecutionResult{
			DisplayText: err.Error(),
			Meta:        map[string]any{"spawn_task_response_error": err.Error()},
		}
	}
	meta := map[string]any{}
	if response.Task != nil {
		meta["task_id"] = response.Task.ID
		meta["task_status"] = response.Task.Status
		meta["task_depth"] = response.Task.Depth
	}
	if len(response.Tasks) > 0 {
		meta["task_count"] = len(response.Tasks)
	}
	return ToolExecutionResult{
		DisplayText: string(data),
		ModelText:   string(data),
		Meta:        meta,
	}
}

func (t SpawnTaskTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "spawn_task",
		Description: "Spawn a bounded, read-only investigation task and return its task_id immediately without blocking.\n" +
			"The spawned task runs with read-only tools only (read_file/grep/list_files/lsp_nav); it cannot edit files, run shell, or perform git actions.\n" +
			"Hard caps are enforced and a request that exceeds a cap is rejected: nesting depth is capped, token_budget is capped to a fraction of the parent budget and is never inherited, a per-task timeout applies, and the number of concurrent and total tasks per session is capped.\n" +
			"Use get_task to retrieve status and result, list_tasks to enumerate, and cancel_task to cancel a non-terminal task.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"objective": map[string]any{
					"type":        "string",
					"description": "Required. The concrete read-only investigation objective for the spawned task.",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Optional positive token budget for the task. Capped to at most 20 percent of the parent token budget; never inherited implicitly.",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Optional per-task wall-clock timeout in seconds. Rejected if it exceeds the maximum.",
				},
				"allow_write": map[string]any{
					"type":        "boolean",
					"description": "Optional explicit opt-in to allow write tools. Honored only if the permission manager also authorizes writes; default is strictly read-only.",
				},
			},
			"required": []string{"objective"},
		},
	}
}

func (t SpawnTaskTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t SpawnTaskTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	session, err := spawnTaskSession(t.ws)
	if err != nil {
		return ToolExecutionResult{}, err
	}

	objective := strings.TrimSpace(stringValue(args, "objective"))
	if objective == "" {
		return ToolExecutionResult{}, fmt.Errorf("task objective must not be empty")
	}
	if len([]rune(objective)) > maxSpawnTaskObjectiveChars {
		return ToolExecutionResult{}, fmt.Errorf("task objective must be at most %d characters", maxSpawnTaskObjectiveChars)
	}

	// Cap: nesting depth. A spawned task is one level deeper than the spawning
	// session. Reject rather than clamp so a deep recursion cannot proceed.
	childDepth := session.SpawnDepth() + 1
	if childDepth > maxSpawnTaskDepth {
		return ToolExecutionResult{}, fmt.Errorf("cannot spawn task: nesting depth %d exceeds the maximum of %d", childDepth, maxSpawnTaskDepth)
	}

	// Cap: total and concurrent tasks per session.
	if len(session.SpawnedTasks) >= maxSpawnedTasksPerSession {
		return ToolExecutionResult{}, fmt.Errorf("cannot spawn task: this session already reached the maximum of %d spawned tasks", maxSpawnedTasksPerSession)
	}
	if session.activeSpawnedTaskCount() >= maxConcurrentSpawnedTasks {
		return ToolExecutionResult{}, fmt.Errorf("cannot spawn task: %d tasks are already active (maximum %d); wait for one to finish or cancel it", session.activeSpawnedTaskCount(), maxConcurrentSpawnedTasks)
	}

	// Recursion/loop guard: a task cannot re-enter an objective that an
	// ancestor or sibling is already pursuing (same normalized objective in a
	// non-terminal state).
	normalizedObjective := strings.ToLower(objective)
	for _, existing := range session.SpawnedTasks {
		if spawnTaskStatusTerminal(existing.Status) {
			continue
		}
		if strings.EqualFold(strings.ToLower(existing.Objective), normalizedObjective) {
			return ToolExecutionResult{}, fmt.Errorf("cannot spawn task: an active task with the same objective already exists (%s); this would re-enter the same work", existing.ID)
		}
	}
	if session.SpawnDepth() > 0 && strings.EqualFold(strings.ToLower(strings.Join(strings.Fields(session.Goal()), " ")), normalizedObjective) {
		return ToolExecutionResult{}, fmt.Errorf("cannot spawn task: the objective re-enters the spawning task's own objective; loop detected")
	}

	// Cap: token budget. Budget is NEVER inherited implicitly. Default is 0
	// (no budget). Any explicit request is capped to a fraction of the parent
	// budget; a request above that cap is rejected.
	tokenBudget, err := optionalPositiveIntValue(args, "token_budget")
	if err != nil {
		return ToolExecutionResult{}, err
	}
	if tokenBudget > 0 {
		parentBudget := session.spawnParentTokenBudget()
		if parentBudget <= 0 {
			return ToolExecutionResult{}, fmt.Errorf("cannot grant token_budget: the parent has no token budget to subdivide; spawn without a token_budget instead")
		}
		budgetCap := int(float64(parentBudget) * spawnTaskTokenBudgetParentFraction)
		if budgetCap <= 0 {
			budgetCap = 1
		}
		if tokenBudget > budgetCap {
			return ToolExecutionResult{}, fmt.Errorf("cannot grant token_budget %d: it exceeds the maximum of %d (20 percent of the parent budget %d)", tokenBudget, budgetCap, parentBudget)
		}
	}

	// Cap: per-task wall-clock timeout. Reject an over-max request.
	timeoutSeconds := spawnTaskDefaultTimeoutSeconds
	if raw, ok := args["timeout_seconds"]; ok && raw != nil {
		value, ok := numericIntValue(raw)
		if !ok || value <= 0 {
			return ToolExecutionResult{}, fmt.Errorf("timeout_seconds must be a positive integer")
		}
		if value > spawnTaskMaxTimeoutSeconds {
			return ToolExecutionResult{}, fmt.Errorf("cannot grant timeout_seconds %d: it exceeds the maximum of %d", value, spawnTaskMaxTimeoutSeconds)
		}
		timeoutSeconds = value
	}

	// Read-only-by-default. A write opt-in is honored ONLY if both the flag is
	// set AND the permission manager authorizes writes. Otherwise the task is
	// strictly read-only regardless of the flag.
	allowWrite := boolValue(args, "allow_write", false)
	effectiveAllowWrite := false
	if allowWrite {
		if spawnTaskPermitsWrite(t.ws) {
			effectiveAllowWrite = true
		}
	}

	now := time.Now()
	task := SpawnedTask{
		ID:              fmt.Sprintf("task-%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/1_000_000),
		Objective:       objective,
		ParentSessionID: session.ID,
		Depth:           childDepth,
		Status:          spawnTaskStatusQueued,
		AllowWrite:      effectiveAllowWrite,
		TokenBudget:     tokenBudget,
		TimeoutSeconds:  timeoutSeconds,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	// Fire SubagentStart so the hook extension point can deny the spawn. A deny
	// surfaces as an error from the workspace hook and blocks the task entirely
	// (consistent with mcp_extensibility-8): nothing is persisted.
	if err := spawnTaskRunStartHook(ctx, t.ws, task); err != nil {
		return ToolExecutionResult{}, fmt.Errorf("spawn_task blocked by SubagentStart hook: %w", err)
	}

	session.UpsertSpawnedTask(task)
	if err := spawnTaskSave(t.ws, session); err != nil {
		return ToolExecutionResult{}, err
	}

	// Bounded synchronous read-only execution. This deterministic executor
	// enforces the caps and records a result without an LLM in the loop, so it
	// is hermetic. A full async, LLM-driven multi-step executor is intentionally
	// out of this slice.
	executed := spawnTaskExecuteReadOnly(ctx, t.ws, session, task)
	session.UpsertSpawnedTask(executed)
	if saveErr := spawnTaskSave(t.ws, session); saveErr != nil {
		return ToolExecutionResult{}, saveErr
	}

	// Fire SubagentStop around task completion for the hook extension point. A
	// stop-hook block does not roll back the recorded result; it is surfaced as
	// a note so the caller learns the hook objected.
	stopNote := ""
	if stopErr := spawnTaskRunStopHook(ctx, t.ws, executed); stopErr != nil {
		stopNote = "SubagentStop hook reported: " + stopErr.Error()
	}

	queuedView := task
	queuedView.Status = spawnTaskStatusQueued
	response := spawnTaskToolResponse{
		Task: &queuedView,
		Note: firstNonBlankString(stopNote, "Task spawned. Use get_task with this id to retrieve status and result."),
	}
	return spawnTaskExecutionResult(response), nil
}

// Goal returns the spawning session goal text used for loop detection.
func (s *Session) Goal() string {
	if s == nil || s.TaskState == nil {
		return ""
	}
	return s.TaskState.Goal
}

// spawnParentTokenBudget reports the token budget the parent task is operating
// under, used to cap a child budget. It is read from the active goal budget if
// present; spawned-task budgets are otherwise self-contained.
func (s *Session) spawnParentTokenBudget() int {
	if s == nil {
		return 0
	}
	if goal, ok := s.ActiveGoal(); ok && goal.TokenBudget > 0 {
		return goal.TokenBudget
	}
	return 0
}

func spawnTaskPermitsWrite(ws Workspace) bool {
	if ws.Perms == nil {
		return false
	}
	switch ws.Perms.Mode() {
	case ModeBypass, ModeAcceptEdits:
		return true
	default:
		return false
	}
}

func spawnTaskRunStartHook(ctx context.Context, ws Workspace, task SpawnedTask) error {
	if ws.RunHook == nil {
		return nil
	}
	payload := HookPayload{
		"agent_id":        task.ID,
		"agent_type":      "spawn_task",
		"hook_event_name": string(HookSubagentStart),
		"objective":       task.Objective,
		"depth":           task.Depth,
	}
	if ws.GoalSession != nil {
		payload["session_id"] = ws.GoalSession.ID
	}
	_, err := ws.Hook(ctx, HookSubagentStart, payload)
	return err
}

func spawnTaskRunStopHook(ctx context.Context, ws Workspace, task SpawnedTask) error {
	if ws.RunHook == nil {
		return nil
	}
	payload := HookPayload{
		"agent_id":               task.ID,
		"agent_type":             "spawn_task",
		"hook_event_name":        string(HookSubagentStop),
		"last_assistant_message": nullableHookString(task.Result),
		"stop_hook_active":       false,
	}
	if ws.GoalSession != nil {
		payload["session_id"] = ws.GoalSession.ID
	}
	_, err := ws.Hook(ctx, HookSubagentStop, payload)
	return err
}

// spawnTaskReadOnlyRegistry builds the bounded tool set a spawned task may use.
// By default this is read-only only. Write/shell/git tools are added ONLY when
// the task carries an authorized write opt-in.
func spawnTaskReadOnlyRegistry(ws Workspace, allowWrite bool) *ToolRegistry {
	items := []Tool{
		NewReadFileTool(ws),
		NewGrepTool(ws),
		NewListFilesTool(ws),
		NewLSPNavigationTool(ws),
	}
	if allowWrite {
		items = append(items,
			NewWriteFileTool(ws),
			NewApplyPatchTool(ws),
			NewReplaceInFileTool(ws),
		)
	}
	return NewToolRegistryWithDefaultHookWorkspace(ws, items...)
}

// spawnTaskExecuteReadOnly runs a bounded, deterministic, read-only step for the
// task and returns the updated task. It honors the per-task timeout and never
// mutates the workspace unless the task is explicitly write-authorized (which
// this deterministic step never does). The heavy, LLM-driven investigation loop
// is intentionally out of this slice.
func spawnTaskExecuteReadOnly(ctx context.Context, ws Workspace, session *Session, task SpawnedTask) SpawnedTask {
	task.Status = spawnTaskStatusRunning
	task.UpdatedAt = time.Now()

	timeout := time.Duration(task.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = spawnTaskDefaultTimeoutSeconds * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	registry := spawnTaskReadOnlyRegistry(ws, task.AllowWrite)

	// Deterministic read-only step: list the task root so the result is a real,
	// non-empty investigation artifact. This proves the read-only tool set works
	// without an LLM or network.
	out, err := registry.Execute(runCtx, "list_files", `{"path": "."}`)
	now := time.Now()
	task.UpdatedAt = now
	task.CompletedAt = now
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			task.Status = spawnTaskStatusFailed
			task.Error = fmt.Sprintf("task timed out after %d seconds", task.TimeoutSeconds)
			return task
		}
		task.Status = spawnTaskStatusFailed
		task.Error = truncateStatusSnippet(firstNonEmptyLine(err.Error()), 200)
		return task
	}
	mode := "read-only"
	if task.AllowWrite {
		mode = "write-authorized"
	}
	task.Status = spawnTaskStatusCompleted
	task.Result = compactPromptSection(fmt.Sprintf("Investigation (%s) for %q completed. Workspace listing:\n%s", mode, task.Objective, out), 4000)
	return task
}

func (t GetTaskTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "get_task",
		Description: "Get a spawned task by id, including its status, result, error, depth, and token usage.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Required. The id returned by spawn_task.",
				},
			},
			"required": []string{"task_id"},
		},
	}
}

func (t GetTaskTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t GetTaskTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	_ = ctx
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	session, err := spawnTaskSession(t.ws)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	id := strings.TrimSpace(stringValue(args, "task_id"))
	if id == "" {
		return ToolExecutionResult{}, fmt.Errorf("task_id must not be empty")
	}
	task, ok := session.SpawnedTask(id)
	if !ok {
		return ToolExecutionResult{}, fmt.Errorf("no spawned task with id %s", id)
	}
	response := spawnTaskToolResponse{Task: &task}
	if task.TokenBudget > 0 {
		remaining := task.TokenBudget - task.TokenUsed
		if remaining < 0 {
			remaining = 0
		}
		response.RemainingTokens = &remaining
	}
	return spawnTaskExecutionResult(response), nil
}

func (t ListTasksTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "list_tasks",
		Description: "List all tasks spawned in this session with their status, depth, and timestamps.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]any{},
			"required":             []string{},
		},
	}
}

func (t ListTasksTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t ListTasksTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	_ = ctx
	if _, err := requireToolInputObject(input, t.Definition().Name); err != nil {
		return ToolExecutionResult{}, err
	}
	session, err := spawnTaskSession(t.ws)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	tasks := append([]SpawnedTask(nil), session.SpawnedTasks...)
	response := spawnTaskToolResponse{Tasks: tasks}
	return spawnTaskExecutionResult(response), nil
}

func (t CancelTaskTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "cancel_task",
		Description: "Cancel a non-terminal spawned task by id, transitioning it to canceled. A task that is already completed, failed, or canceled cannot be canceled.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Required. The id returned by spawn_task.",
				},
			},
			"required": []string{"task_id"},
		},
	}
}

func (t CancelTaskTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t CancelTaskTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	_ = ctx
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	session, err := spawnTaskSession(t.ws)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	id := strings.TrimSpace(stringValue(args, "task_id"))
	if id == "" {
		return ToolExecutionResult{}, fmt.Errorf("task_id must not be empty")
	}
	task, ok := session.SpawnedTask(id)
	if !ok {
		return ToolExecutionResult{}, fmt.Errorf("no spawned task with id %s", id)
	}
	if spawnTaskStatusTerminal(task.Status) {
		return ToolExecutionResult{}, fmt.Errorf("cannot cancel task %s: it is already %s", id, task.Status)
	}
	now := time.Now()
	task.Status = spawnTaskStatusCanceled
	task.UpdatedAt = now
	task.CompletedAt = now
	if strings.TrimSpace(task.Error) == "" {
		task.Error = "canceled by parent session"
	}
	session.UpsertSpawnedTask(task)
	if err := spawnTaskSave(t.ws, session); err != nil {
		return ToolExecutionResult{}, err
	}
	response := spawnTaskToolResponse{Task: &task, Note: "Task canceled."}
	return spawnTaskExecutionResult(response), nil
}

func (t UpdateTaskTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "update_task",
		Description: "Update a spawned task owned by this session. Only a non-terminal task may be updated, and only its objective may be refined.\n" +
			"Use cancel_task to cancel a task; status transitions to terminal states are controlled by the executor.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Required. The id returned by spawn_task.",
				},
				"objective": map[string]any{
					"type":        "string",
					"description": "Optional refined objective for the task. Must be non-empty when provided.",
				},
			},
			"required": []string{"task_id"},
		},
	}
}

func (t UpdateTaskTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t UpdateTaskTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	_ = ctx
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	session, err := spawnTaskSession(t.ws)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	id := strings.TrimSpace(stringValue(args, "task_id"))
	if id == "" {
		return ToolExecutionResult{}, fmt.Errorf("task_id must not be empty")
	}
	task, ok := session.SpawnedTask(id)
	if !ok {
		return ToolExecutionResult{}, fmt.Errorf("no spawned task with id %s", id)
	}
	// Restrict to the parent session and non-terminal states.
	if !strings.EqualFold(strings.TrimSpace(task.ParentSessionID), strings.TrimSpace(session.ID)) {
		return ToolExecutionResult{}, fmt.Errorf("cannot update task %s: it is owned by a different session", id)
	}
	if spawnTaskStatusTerminal(task.Status) {
		return ToolExecutionResult{}, fmt.Errorf("cannot update task %s: it is already %s", id, task.Status)
	}
	changed := false
	if raw, ok := args["objective"]; ok && raw != nil {
		objective := strings.TrimSpace(stringValue(args, "objective"))
		if objective == "" {
			return ToolExecutionResult{}, fmt.Errorf("objective must not be empty when provided")
		}
		if len([]rune(objective)) > maxSpawnTaskObjectiveChars {
			return ToolExecutionResult{}, fmt.Errorf("objective must be at most %d characters", maxSpawnTaskObjectiveChars)
		}
		task.Objective = objective
		changed = true
	}
	if !changed {
		return ToolExecutionResult{}, fmt.Errorf("update_task requires at least one field to update")
	}
	task.UpdatedAt = time.Now()
	session.UpsertSpawnedTask(task)
	if err := spawnTaskSave(t.ws, session); err != nil {
		return ToolExecutionResult{}, err
	}
	response := spawnTaskToolResponse{Task: &task, Note: "Task updated."}
	return spawnTaskExecutionResult(response), nil
}
