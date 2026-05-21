package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

const maxThreadGoalObjectiveChars = 4000

type GetGoalTool struct{ ws Workspace }
type CreateGoalTool struct{ ws Workspace }
type UpdateGoalTool struct{ ws Workspace }

func NewGetGoalTool(ws Workspace) GetGoalTool       { return GetGoalTool{ws: ws} }
func NewCreateGoalTool(ws Workspace) CreateGoalTool { return CreateGoalTool{ws: ws} }
func NewUpdateGoalTool(ws Workspace) UpdateGoalTool { return UpdateGoalTool{ws: ws} }

type goalToolThreadGoal struct {
	ThreadID        string `json:"threadId"`
	Objective       string `json:"objective"`
	Status          string `json:"status"`
	TokenBudget     *int64 `json:"tokenBudget,omitempty"`
	TokensUsed      int64  `json:"tokensUsed"`
	TimeUsedSeconds int64  `json:"timeUsedSeconds"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
}

type goalToolResponse struct {
	Goal                   *goalToolThreadGoal `json:"goal"`
	RemainingTokens        *int64              `json:"remainingTokens"`
	CompletionBudgetReport *string             `json:"completionBudgetReport"`
}

func (t GetGoalTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "get_goal",
		Description: "Get the current goal for this thread, including status, budgets, token and elapsed-time usage, and remaining token budget.",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"required":             []string{},
			"additionalProperties": false,
		},
	}
}

func (t GetGoalTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t GetGoalTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	_ = ctx
	if _, err := requireToolInputObject(input, t.Definition().Name); err != nil {
		return ToolExecutionResult{}, err
	}
	session, err := t.goalSession()
	if err != nil {
		return ToolExecutionResult{}, err
	}
	response, err := goalToolCurrentResponse(session, false)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	return goalToolExecutionResult(response), nil
}

func (t CreateGoalTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "create_goal",
		Description: "Create a goal only when explicitly requested by the user or system/developer instructions; do not infer goals from ordinary tasks.\n" +
			"Set token_budget only when an explicit token budget is requested. Fails if a goal exists; use update_goal only for status.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"objective": map[string]any{
					"type":        "string",
					"description": "Required. The concrete objective to start pursuing. This starts a new active goal only when no goal is currently defined; if a goal already exists, this tool fails.",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Optional positive token budget for the new active goal.",
				},
			},
			"required": []string{"objective"},
		},
	}
}

func (t CreateGoalTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t CreateGoalTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	_ = ctx
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	session, err := t.goalSession()
	if err != nil {
		return ToolExecutionResult{}, err
	}
	if len(session.Goals) > 0 {
		return ToolExecutionResult{}, fmt.Errorf("cannot create a new goal because this thread already has a goal; use update_goal only when the existing goal is complete")
	}
	objective := strings.TrimSpace(stringValue(args, "objective"))
	if err := validateGoalToolObjective(objective); err != nil {
		return ToolExecutionResult{}, err
	}
	tokenBudget, err := optionalPositiveIntValue(args, "token_budget")
	if err != nil {
		return ToolExecutionResult{}, err
	}
	now := time.Now()
	goal := GoalState{
		ID:          fmt.Sprintf("goal-%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/1_000_000),
		Objective:   objective,
		Status:      goalStatusActive,
		TokenBudget: tokenBudget,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	goal.Normalize()
	primeGoalSessionState(session, &goal, "created", "")
	goal.updateUsageTelemetry(session)
	session.UpsertGoal(goal)
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindGoal,
		Severity: conversationSeverityInfo,
		Summary:  "goal created: " + compactPromptSection(goal.Objective, 120),
		Entities: map[string]string{
			"goal":   goal.ID,
			"status": goal.Status,
		},
	})
	if err := t.saveGoalSession(session); err != nil {
		return ToolExecutionResult{}, err
	}
	response, err := goalToolResponseFor(session, goal, false)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	return goalToolExecutionResult(response), nil
}

func (t UpdateGoalTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "update_goal",
		Description: "Update the existing goal.\n" +
			"Use this tool only to mark the goal achieved.\n" +
			"Set status to `complete` only when the objective has actually been achieved and no required work remains.\n" +
			"Do not mark a goal complete merely because its budget is nearly exhausted or because you are stopping work.\n" +
			"You cannot use this tool to pause, resume, or budget-limit a goal; those status changes are controlled by the user or system.\n" +
			"When marking a budgeted goal achieved with status `complete`, report the final token usage from the tool result to the user.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"complete"},
					"description": "Required. Set to complete only when the objective is achieved and no required work remains.",
				},
			},
			"required": []string{"status"},
		},
	}
}

func (t UpdateGoalTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t UpdateGoalTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	_ = ctx
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	if stringValue(args, "status") != goalStatusComplete {
		return ToolExecutionResult{}, fmt.Errorf("update_goal can only mark the existing goal complete; pause, resume, and budget-limited status changes are controlled by the user or system")
	}
	session, err := t.goalSession()
	if err != nil {
		return ToolExecutionResult{}, err
	}
	index, ok := session.GoalIndex("active")
	if !ok {
		return ToolExecutionResult{}, fmt.Errorf("cannot update goal for thread %s: no goal exists", session.ID)
	}
	goal := session.Goals[index]
	now := time.Now()
	goal.Status = goalStatusComplete
	goal.LastError = ""
	if goal.CompletedAt.IsZero() {
		goal.CompletedAt = now
	}
	goal.UpdatedAt = now
	goal.updateUsageTelemetry(session)
	goal.Normalize()
	session.UpsertGoal(goal)
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindGoal,
		Severity: conversationSeverityInfo,
		Summary:  "goal completed: " + compactPromptSection(goal.Objective, 120),
		Entities: map[string]string{
			"goal":   goal.ID,
			"status": goal.Status,
		},
	})
	if err := t.saveGoalSession(session); err != nil {
		return ToolExecutionResult{}, err
	}
	response, err := goalToolResponseFor(session, goal, true)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	return goalToolExecutionResult(response), nil
}

func (t GetGoalTool) goalSession() (*Session, error) {
	if t.ws.GoalSession == nil {
		return nil, fmt.Errorf("goal session is not configured")
	}
	t.ws.GoalSession.normalizeGoals()
	return t.ws.GoalSession, nil
}

func (t CreateGoalTool) goalSession() (*Session, error) {
	return GetGoalTool{ws: t.ws}.goalSession()
}

func (t UpdateGoalTool) goalSession() (*Session, error) {
	return GetGoalTool{ws: t.ws}.goalSession()
}

func (t CreateGoalTool) saveGoalSession(session *Session) error {
	if t.ws.GoalStore == nil {
		return nil
	}
	return t.ws.GoalStore.Save(session)
}

func (t UpdateGoalTool) saveGoalSession(session *Session) error {
	if t.ws.GoalStore == nil {
		return nil
	}
	return t.ws.GoalStore.Save(session)
}

func goalToolCurrentResponse(session *Session, includeCompletionBudgetReport bool) (goalToolResponse, error) {
	goal, ok := session.ActiveGoal()
	if !ok {
		return goalToolResponse{}, nil
	}
	goal.updateUsageTelemetry(session)
	return goalToolResponseFor(session, goal, includeCompletionBudgetReport)
}

func goalToolResponseFor(session *Session, goal GoalState, includeCompletionBudgetReport bool) (goalToolResponse, error) {
	threadGoal := goalToolThreadGoalFor(session, goal)
	response := goalToolResponse{
		Goal: &threadGoal,
	}
	if goal.TokenBudget > 0 {
		remaining := int64(goal.TokenBudget - goal.TokenUsedEstimate)
		if remaining < 0 {
			remaining = 0
		}
		response.RemainingTokens = &remaining
	}
	if includeCompletionBudgetReport && strings.EqualFold(goal.Status, goalStatusComplete) {
		if report := goalCompletionBudgetReport(goal); report != "" {
			response.CompletionBudgetReport = &report
		}
	}
	return response, nil
}

func goalToolThreadGoalFor(session *Session, goal GoalState) goalToolThreadGoal {
	var tokenBudget *int64
	if goal.TokenBudget > 0 {
		value := int64(goal.TokenBudget)
		tokenBudget = &value
	}
	threadID := ""
	if session != nil {
		threadID = session.ID
	}
	return goalToolThreadGoal{
		ThreadID:        threadID,
		Objective:       goal.Objective,
		Status:          canonicalGoalStatus(goal.Status),
		TokenBudget:     tokenBudget,
		TokensUsed:      int64(goal.TokenUsedEstimate),
		TimeUsedSeconds: int64(goal.TimeUsedSeconds),
		CreatedAt:       goal.CreatedAt.Unix(),
		UpdatedAt:       goal.UpdatedAt.Unix(),
	}
}

func goalToolExecutionResult(response goalToolResponse) ToolExecutionResult {
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return ToolExecutionResult{
			DisplayText: err.Error(),
			Meta: map[string]any{
				"goal_response_error": err.Error(),
			},
		}
	}
	meta := map[string]any{
		"goal_present": response.Goal != nil,
	}
	if response.Goal != nil {
		meta["goal_status"] = response.Goal.Status
		meta["thread_id"] = response.Goal.ThreadID
	}
	if response.RemainingTokens != nil {
		meta["remaining_tokens"] = *response.RemainingTokens
	}
	if response.CompletionBudgetReport != nil {
		meta["completion_budget_report"] = *response.CompletionBudgetReport
	}
	return ToolExecutionResult{
		DisplayText: string(data),
		ModelText:   string(data),
		Meta:        meta,
	}
}

func validateGoalToolObjective(objective string) error {
	if objective == "" {
		return fmt.Errorf("goal objective must not be empty")
	}
	if len([]rune(objective)) > maxThreadGoalObjectiveChars {
		return fmt.Errorf("goal objective must be at most %d characters", maxThreadGoalObjectiveChars)
	}
	return nil
}

func optionalPositiveIntValue(args map[string]any, key string) (int, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return 0, nil
	}
	value, ok := numericIntValue(raw)
	if !ok {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func numericIntValue(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		if v > maxPlatformIntValue() || v < minPlatformIntValue() {
			return 0, false
		}
		return int(v), true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v > float64(maxPlatformIntValue()) || v < float64(minPlatformIntValue()) {
			return 0, false
		}
		return int(v), true
	case json.Number:
		parsed, err := v.Int64()
		if err != nil || parsed > maxPlatformIntValue() || parsed < minPlatformIntValue() {
			return 0, false
		}
		return int(parsed), true
	default:
		return 0, false
	}
}

func maxPlatformIntValue() int64 {
	return int64(int(^uint(0) >> 1))
}

func minPlatformIntValue() int64 {
	return -maxPlatformIntValue() - 1
}
