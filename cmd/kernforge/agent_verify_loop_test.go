package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptedProviderClient struct {
	mu       sync.Mutex
	replies  []ChatResponse
	requests []ChatRequest
	index    int
}

func (s *scriptedProviderClient) Name() string { return "scripted" }

func (s *scriptedProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, cloneChatRequestForTest(req))
	if s.index >= len(s.replies) {
		return ChatResponse{Message: Message{Role: "assistant", Text: "done"}}, nil
	}
	resp := s.replies[s.index]
	s.index++
	return resp, nil
}

func TestAgentStopHookBlockContinuesSameTurn(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "scripted", "model", "", "default")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "draft final"}},
			{Message: Message{Role: "assistant", Text: "revised final"}},
		},
	}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
		Hooks: &HookRuntime{
			Engine: &HookEngine{
				Enabled: true,
				Rules: []HookRule{
					{
						ID:     "block-draft-final",
						Events: []HookEvent{HookStop},
						Match: HookMatch{
							ContainsText: []string{"draft final"},
						},
						Action: HookAction{
							Type:    "deny",
							Message: "write the revised final answer",
						},
					},
				},
			},
			Session: session,
		},
	}

	reply, err := agent.ReplyWithImages(context.Background(), "answer plainly", nil)
	if err != nil {
		t.Fatalf("ReplyWithImages: %v", err)
	}
	if reply != "revised final" {
		t.Fatalf("expected revised final answer, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected Stop hook continuation model turn, got %d request(s)", len(provider.requests))
	}
	secondMessages := provider.requests[1].Messages
	if len(secondMessages) == 0 || !strings.Contains(secondMessages[len(secondMessages)-1].Text, "write the revised final answer") {
		t.Fatalf("expected Stop hook continuation guidance in second request, got %#v", secondMessages)
	}
	for _, msg := range session.Messages {
		if msg.Role == "assistant" && strings.TrimSpace(msg.Text) == "draft final" {
			t.Fatalf("draft final candidate should have been discarded, messages=%#v", session.Messages)
		}
	}
}

type turnStateObservingProviderClient struct {
	mu       sync.Mutex
	replies  []ChatResponse
	states   []*ProviderTurnState
	values   []string
	requests []ChatRequest
	index    int
}

func (s *turnStateObservingProviderClient) Name() string { return "turn-state-observer" }

func (s *turnStateObservingProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, cloneChatRequestForTest(req))
	s.states = append(s.states, req.TurnState)
	value := ""
	if req.TurnState != nil {
		value = req.TurnState.Value()
	}
	s.values = append(s.values, value)
	if s.index == 0 && req.TurnState != nil {
		req.TurnState.Capture("sticky-turn")
	}
	if s.index >= len(s.replies) {
		return ChatResponse{Message: Message{Role: "assistant", Text: "done"}}, nil
	}
	resp := s.replies[s.index]
	s.index++
	return resp, nil
}

func chatRequestHasTool(req ChatRequest, name string) bool {
	for _, tool := range req.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

type streamingScriptedProviderClient struct {
	mu       sync.Mutex
	replies  []ChatResponse
	requests []ChatRequest
	index    int
}

func (s *streamingScriptedProviderClient) Name() string { return "streaming-scripted" }

func (s *streamingScriptedProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, cloneChatRequestForTest(req))
	if s.index >= len(s.replies) {
		return ChatResponse{Message: Message{Role: "assistant", Text: "done"}}, nil
	}
	resp := s.replies[s.index]
	s.index++
	if req.OnTextDelta != nil && strings.TrimSpace(resp.Message.Text) != "" {
		req.OnTextDelta(resp.Message.Text)
	}
	return resp, nil
}

func cloneChatRequestForTest(req ChatRequest) ChatRequest {
	out := req
	out.Messages = append([]Message(nil), req.Messages...)
	for i := range out.Messages {
		out.Messages[i].Images = append([]MessageImage(nil), out.Messages[i].Images...)
		out.Messages[i].ToolCalls = append([]ToolCall(nil), out.Messages[i].ToolCalls...)
		out.Messages[i].ToolContentItems = append([]ToolContentItem(nil), out.Messages[i].ToolContentItems...)
		if out.Messages[i].ToolMeta != nil {
			meta := make(map[string]any, len(out.Messages[i].ToolMeta))
			for key, value := range out.Messages[i].ToolMeta {
				meta[key] = value
			}
			out.Messages[i].ToolMeta = meta
		}
	}
	out.Tools = append([]ToolDefinition(nil), req.Tools...)
	out.TurnMetadata = cloneStringAnyMap(req.TurnMetadata)
	return out
}

type blockingProviderClient struct {
	calls   int
	started chan struct{}
}

func (b *blockingProviderClient) Name() string { return "blocking" }

func (b *blockingProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = req
	b.calls++
	if b.started != nil {
		select {
		case <-b.started:
		default:
			close(b.started)
		}
	}
	<-ctx.Done()
	return ChatResponse{}, ctx.Err()
}

type timeoutThenSuccessProviderClient struct {
	calls int
}

func (p *timeoutThenSuccessProviderClient) Name() string { return "timeout-then-success" }

func (p *timeoutThenSuccessProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = req
	p.calls++
	if p.calls == 1 {
		<-ctx.Done()
		return ChatResponse{}, ctx.Err()
	}
	return ChatResponse{
		Message: Message{
			Role: "assistant",
			Text: "recovered",
		},
		StopReason: "stop",
	}, nil
}

type timeoutProviderClient struct {
	calls int
}

func (p *timeoutProviderClient) Name() string { return "timeout" }

func (p *timeoutProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = req
	p.calls++
	<-ctx.Done()
	return ChatResponse{}, ctx.Err()
}

type transientErrorThenSuccessProviderClient struct {
	calls     int
	failCount int
	err       error
}

func (p *transientErrorThenSuccessProviderClient) Name() string {
	return "transient-error-then-success"
}

func (p *transientErrorThenSuccessProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	_ = req
	p.calls++
	if p.calls <= p.failCount {
		return ChatResponse{}, p.err
	}
	return ChatResponse{
		Message: Message{
			Role: "assistant",
			Text: "recovered",
		},
		StopReason: "stop",
	}, nil
}

type cancelDuringToolTool struct {
	cancel func()
	calls  int
}

func (t *cancelDuringToolTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "cancel_during_tool",
		Description: "Cancel the active request while simulating a completed tool call.",
		InputSchema: map[string]any{
			"type": "object",
		},
	}
}

func (t *cancelDuringToolTool) Execute(ctx context.Context, input any) (string, error) {
	_ = ctx
	_ = input
	t.calls++
	if t.cancel != nil {
		t.cancel()
	}
	return "tool finished after cancel", nil
}

type failingTool struct {
	name  string
	err   error
	calls int
}

type sequenceTool struct {
	name    string
	outputs []string
	errs    []error
	before  []func()
	calls   int
}

type observingSessionTool struct {
	name        string
	sessionPath string
	output      string
	err         error
	calls       int
	onExecute   func([]byte)
}

type staticTool struct {
	name   string
	output string
	calls  int
}

type parallelBarrierTool struct {
	name    string
	mu      sync.Mutex
	waiters []chan struct{}
	calls   int
}

type metadataEditTool struct {
	name   string
	output string
	meta   map[string]any
	calls  int
}

type toolUnsupportedThenSuccessClient struct {
	calls    int
	requests []ChatRequest
}

func (c *toolUnsupportedThenSuccessClient) Name() string { return "tool-unsupported-then-success" }

func (c *toolUnsupportedThenSuccessClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	c.calls++
	c.requests = append(c.requests, req)
	if c.calls == 1 {
		return ChatResponse{}, fmt.Errorf("openai API error (404 Not Found): No endpoints found that support tool use. Try disabling \"git_status\".")
	}
	return ChatResponse{
		Message: Message{
			Role: "assistant",
			Text: "첨부된 코드 기준으로 즉시 보이는 치명적 버그는 없지만 경계 조건 검토가 필요합니다.",
		},
		StopReason: "stop",
	}, nil
}

type fallbackReplayClient struct {
	calls int
}

func (c *fallbackReplayClient) Name() string { return "fallback-replay" }

func (c *fallbackReplayClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	c.calls++
	return ChatResponse{
		Message: Message{
			Role: "assistant",
			Text: "full fallback answer",
		},
		StopReason: "stop_after_stream_retry",
	}, nil
}

func (t *failingTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        t.name,
		Description: "Fail with a scripted error for retry-loop tests.",
		InputSchema: map[string]any{
			"type": "object",
		},
	}
}

func (t *failingTool) Execute(ctx context.Context, input any) (string, error) {
	_ = ctx
	_ = input
	t.calls++
	return "", t.err
}

func (t *sequenceTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        t.name,
		Description: "Return scripted outputs and errors for retry-loop tests.",
		InputSchema: map[string]any{
			"type": "object",
		},
	}
}

func (t *sequenceTool) Execute(ctx context.Context, input any) (string, error) {
	_ = ctx
	_ = input
	t.calls++
	index := t.calls - 1
	if index >= 0 && index < len(t.before) && t.before[index] != nil {
		t.before[index]()
	}
	var output string
	if index >= 0 && index < len(t.outputs) {
		output = t.outputs[index]
	}
	var err error
	if index >= 0 && index < len(t.errs) {
		err = t.errs[index]
	}
	return output, err
}

func (t *observingSessionTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        t.name,
		Description: "Observe the persisted session state during tool execution.",
		InputSchema: map[string]any{
			"type": "object",
		},
	}
}

func (t *observingSessionTool) Execute(ctx context.Context, input any) (string, error) {
	_ = ctx
	_ = input
	t.calls++
	if t.onExecute != nil && strings.TrimSpace(t.sessionPath) != "" {
		data, readErr := os.ReadFile(t.sessionPath)
		if readErr == nil {
			t.onExecute(data)
		}
	}
	return t.output, t.err
}

func (t *staticTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        t.name,
		Description: "Return a fixed output for agent loop tests.",
		InputSchema: map[string]any{
			"type": "object",
		},
	}
}

func (t *staticTool) Execute(ctx context.Context, input any) (string, error) {
	_ = ctx
	_ = input
	t.calls++
	return t.output, nil
}

func (t *parallelBarrierTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        t.name,
		Description: "Wait until two parallel calls arrive.",
		InputSchema: map[string]any{
			"type": "object",
		},
	}
}

func (t *parallelBarrierTool) ReadOnlyToolCall() bool {
	return true
}

func (t *parallelBarrierTool) SupportsParallelToolCalls() bool {
	return true
}

func (t *parallelBarrierTool) Execute(ctx context.Context, input any) (string, error) {
	_ = input
	t.mu.Lock()
	t.calls++
	ch := make(chan struct{})
	t.waiters = append(t.waiters, ch)
	if len(t.waiters) >= 2 {
		waiters := t.waiters
		t.waiters = nil
		for _, waiter := range waiters {
			close(waiter)
		}
		t.mu.Unlock()
		return "barrier ok", nil
	}
	t.mu.Unlock()
	select {
	case <-ch:
		return "barrier ok", nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(750 * time.Millisecond):
		return "", fmt.Errorf("parallel barrier timed out")
	}
}

func (t *metadataEditTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        t.name,
		Description: "Return a fixed edit-like result for agent loop tests.",
		InputSchema: map[string]any{
			"type": "object",
		},
	}
}

func (t *metadataEditTool) Execute(ctx context.Context, input any) (string, error) {
	_ = ctx
	_ = input
	t.calls++
	return t.output, nil
}

func (t *metadataEditTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	_ = ctx
	_ = input
	t.calls++
	return ToolExecutionResult{
		DisplayText: t.output,
		Meta:        t.meta,
	}, nil
}

func toolCallResponse(name string, args map[string]any) ChatResponse {
	data, _ := json.Marshal(args)
	return ChatResponse{
		Message: Message{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      name,
				Arguments: string(data),
			}},
		},
	}
}

func TestAgentRunsParallelSafeReadOnlyToolCallsConcurrently(t *testing.T) {
	root := t.TempDir()
	tool := &parallelBarrierTool{name: "readonly_parallel"}
	firstArgs, _ := json.Marshal(map[string]any{"id": 1})
	secondArgs, _ := json.Marshal(map[string]any{"id": 2})
	client := &scriptedProviderClient{replies: []ChatResponse{
		{
			Message: Message{
				Role: "assistant",
				ToolCalls: []ToolCall{
					{ID: "call-1", Name: "readonly_parallel", Arguments: string(firstArgs)},
					{ID: "call-2", Name: "readonly_parallel", Arguments: string(secondArgs)},
				},
			},
		},
		{
			Message:    Message{Role: "assistant", Text: "parallel tools completed"},
			StopReason: "stop",
		},
	}}
	session := NewSession(root, "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "run both read-only tools"})
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    client,
		Tools:     NewToolRegistry(tool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.completeLoop(context.Background(), true, false, false)
	if err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if !strings.Contains(reply, "parallel tools completed") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if tool.calls != 2 {
		t.Fatalf("expected both parallel tool calls to execute, got %d", tool.calls)
	}
	successes := 0
	for _, msg := range session.Messages {
		if msg.Role != "tool" || msg.ToolName != "readonly_parallel" {
			continue
		}
		if msg.IsError {
			t.Fatalf("parallel read-only tool call unexpectedly failed: %#v", msg)
		}
		if strings.Contains(msg.Text, "barrier ok") {
			successes++
		}
	}
	if successes != 2 {
		t.Fatalf("expected two successful parallel tool results, got %d", successes)
	}
}

func TestAgentTreatsBuiltInInspectionToolsAsParallelSafe(t *testing.T) {
	root := t.TempDir()
	agent := &Agent{
		Tools: NewToolRegistry(
			NewReadFileTool(Workspace{BaseRoot: root, Root: root}),
			NewListFilesTool(Workspace{BaseRoot: root, Root: root}),
			NewGrepTool(Workspace{BaseRoot: root, Root: root}),
			NewGitStatusTool(Workspace{BaseRoot: root, Root: root}),
			NewGitDiffTool(Workspace{BaseRoot: root, Root: root}),
			NewUpdatePlanTool(Workspace{BaseRoot: root, Root: root}),
		),
	}
	calls := []ToolCall{
		{ID: "call-read", Name: "read_file", Arguments: `{"path":"a.txt"}`},
		{ID: "call-list", Name: "list_files", Arguments: `{}`},
		{ID: "call-grep", Name: "grep", Arguments: `{"pattern":"needle"}`},
		{ID: "call-status", Name: "git_status", Arguments: `{}`},
		{ID: "call-diff", Name: "git_diff", Arguments: `{}`},
	}
	for _, call := range calls {
		if !agent.Tools.ToolCallReadOnly(call.Name) {
			t.Fatalf("expected %s to advertise read-only execution", call.Name)
		}
		if !agent.Tools.ToolCallSupportsParallel(call.Name) {
			t.Fatalf("expected %s to advertise parallel execution", call.Name)
		}
	}
	if !agent.shouldExecuteToolCallBatchInParallel(calls, true, nil) {
		t.Fatalf("expected built-in read-only inspection tools to use the parallel batch path")
	}
	mixedCalls := append([]ToolCall(nil), calls[:2]...)
	mixedCalls = append(mixedCalls, ToolCall{
		ID:        "call-plan",
		Name:      "update_plan",
		Arguments: `{"items":[{"step":"Inspect","status":"in_progress"}]}`,
	})
	if agent.Tools.ToolCallSupportsParallel("update_plan") {
		t.Fatalf("update_plan should remain write-locked in mixed parallel batches")
	}
	if !agent.shouldExecuteToolCallBatchInParallel(mixedCalls, true, nil) {
		t.Fatalf("expected update_plan to be allowed as the write-locked member of a mixed parallel batch")
	}
	viewImageRegistry := NewToolRegistry(NewViewImageTool(Workspace{BaseRoot: root, Root: root}))
	if !viewImageRegistry.ToolCallSupportsParallel("view_image") {
		t.Fatalf("expected view_image to advertise parallel execution")
	}
}

func scriptedRequestsContainText(requests []ChatRequest, needle string) bool {
	for _, req := range requests {
		for _, msg := range req.Messages {
			if strings.Contains(msg.Text, needle) {
				return true
			}
		}
	}
	return false
}

func sessionContainsText(session *Session, needle string) bool {
	if session == nil {
		return false
	}
	for _, msg := range session.Messages {
		if strings.Contains(msg.Text, needle) {
			return true
		}
	}
	return false
}

func sessionContainsToolResultText(session *Session, toolCallID string, needle string) bool {
	if session == nil {
		return false
	}
	for _, msg := range session.Messages {
		if msg.Role == "tool" && msg.ToolCallID == toolCallID && strings.Contains(msg.Text, needle) {
			return true
		}
	}
	return false
}

func sessionContainsInProgressToolResult(session *Session) bool {
	if session == nil {
		return false
	}
	for _, msg := range session.Messages {
		if msg.Role == "tool" && strings.HasPrefix(strings.TrimSpace(msg.Text), "IN_PROGRESS:") {
			return true
		}
	}
	return false
}

func approvedReviewResponse(summary string) ChatResponse {
	if strings.TrimSpace(summary) == "" {
		summary = "no actionable findings"
	}
	return ChatResponse{
		Message: Message{Role: "assistant", Text: strings.Join([]string{
			"REVIEW_RESULT",
			"verdict: approved",
			"summary: " + summary,
			"findings:",
		}, "\n")},
	}
}

func multiToolCallResponse(calls ...ToolCall) ChatResponse {
	return ChatResponse{
		Message: Message{
			Role:      "assistant",
			ToolCalls: calls,
		},
	}
}

func TestAgentAddsAllToolPlaceholdersBeforeNextModelTurn(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			multiToolCallResponse(
				ToolCall{ID: "call-list", Name: "list_files", Arguments: `{"path":"."}`},
				ToolCall{ID: "call-read", Name: "read_file", Arguments: `{"path":"sample.txt"}`},
			),
			{Message: Message{Role: "assistant", Text: "done"}},
		},
	}
	session := NewSession(root, "deepseek", "deepseek-v4-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws), NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "inspect"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected a second model request after tool execution, got %d", len(provider.requests))
	}
	messages := provider.requests[1].Messages
	assistantIndex := -1
	for i, msg := range messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) == 2 {
			assistantIndex = i
			break
		}
	}
	if assistantIndex < 0 {
		t.Fatalf("second request missing assistant multi-tool turn: %#v", messages)
	}
	if assistantIndex+2 >= len(messages) {
		t.Fatalf("assistant tool calls not followed by two tool messages: %#v", messages[assistantIndex:])
	}
	firstTool := messages[assistantIndex+1]
	secondTool := messages[assistantIndex+2]
	if firstTool.Role != "tool" || firstTool.ToolCallID != "call-list" {
		t.Fatalf("first tool response mismatch: %#v", firstTool)
	}
	if secondTool.Role != "tool" || secondTool.ToolCallID != "call-read" {
		t.Fatalf("second tool response mismatch: %#v", secondTool)
	}
}

func TestAgentInjectsBudgetLimitGoalContextAfterToolAccounting(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			multiToolCallResponse(
				ToolCall{ID: "call-grep", Name: "grep", Arguments: `{"pattern":"needle"}`},
			),
			{Message: Message{Role: "assistant", Text: "Budget reached; useful progress is summarized."}},
		},
	}
	session := NewSession(root, "openai-codex", "gpt-5.2", "", "default")
	goal := GoalState{
		ID:          "goal-budget-steer",
		Objective:   "keep improving the benchmark",
		Status:      goalStatusActive,
		TokenBudget: 1,
		CreatedAt:   time.Now().Add(-2 * time.Second),
		UpdatedAt:   time.Now().Add(-2 * time.Second),
	}
	goal.Normalize()
	session.UpsertGoal(goal)
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	grepTool := &staticTool{name: "grep", output: "needle found"}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(grepTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), strings.Repeat("budget ", 64))
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Budget reached") {
		t.Fatalf("expected final budget summary, got %q", reply)
	}
	if grepTool.calls != 1 {
		t.Fatalf("expected grep to run once, got %d", grepTool.calls)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected follow-up request after tool accounting, got %d", len(provider.requests))
	}
	for _, needle := range []string{
		"<goal_context>",
		"budget_limited",
		"Wrap up this turn soon",
		"keep improving the benchmark",
	} {
		if !scriptedRequestsContainText(provider.requests[1:], needle) {
			t.Fatalf("follow-up request missing budget steering text %q", needle)
		}
	}
	active, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if active.Status != goalStatusBudgetLimited {
		t.Fatalf("expected goal to be budget-limited, got %#v", active)
	}
}

func TestAgentHidesOriginalViewImageDetailForUnsupportedCodexModel(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "done"}},
		},
	}
	session := NewSession(root, "openai-codex", "gpt-5.2", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewViewImageTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "inspect image"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(provider.requests) < 1 {
		t.Fatalf("expected at least one request, got %d", len(provider.requests))
	}
	tool := findToolDefinitionForTest(provider.requests[0].Tools, "view_image")
	if tool == nil {
		t.Fatalf("request missing view_image tool: %#v", provider.requests[0].Tools)
	}
	props := tool.InputSchema["properties"].(map[string]any)
	if _, ok := props["detail"]; ok {
		t.Fatalf("unsupported Codex model should not receive detail property: %#v", props["detail"])
	}
}

func TestAgentDowngradesOriginalViewImageResultForUnsupportedCodexModel(t *testing.T) {
	root := t.TempDir()
	writeTestImage(t, root, "shot.png")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			multiToolCallResponse(ToolCall{
				ID:        "call-image",
				Name:      "view_image",
				Arguments: `{"path":"shot.png","detail":"original"}`,
			}),
			{Message: Message{Role: "assistant", Text: "done"}},
		},
	}
	session := NewSession(root, "openai-codex", "gpt-5.2", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewViewImageTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "inspect image"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected follow-up request after view_image, got %d", len(provider.requests))
	}
	toolMsg := findToolMessageForTest(provider.requests[1].Messages, "call-image")
	if toolMsg == nil {
		t.Fatalf("follow-up request missing tool message: %#v", provider.requests[1].Messages)
	}
	if len(toolMsg.ToolContentItems) != 1 {
		t.Fatalf("expected one tool content item, got %#v", toolMsg.ToolContentItems)
	}
	if toolMsg.ToolContentItems[0].Detail != imageDetailHigh {
		t.Fatalf("tool image detail = %q, want high", toolMsg.ToolContentItems[0].Detail)
	}
	if !strings.Contains(toolMsg.Text, `"detail":"high"`) {
		t.Fatalf("tool text should report high detail, got %q", toolMsg.Text)
	}
}

func TestAgentReturnsUnsupportedViewImageForTextOnlyCodexModel(t *testing.T) {
	root := t.TempDir()
	model := "text-only-agent-view-image-test"
	registerCodexModelImageInputSupport(model, false)
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			multiToolCallResponse(ToolCall{
				ID:        "call-image",
				Name:      "view_image",
				Arguments: `{"path":"missing.png"}`,
			}),
			{Message: Message{Role: "assistant", Text: "done"}},
		},
	}
	session := NewSession(root, "openai-codex", model, "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewViewImageTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "inspect image"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected follow-up request after view_image, got %d", len(provider.requests))
	}
	toolMsg := findToolMessageForTest(provider.requests[1].Messages, "call-image")
	if toolMsg == nil {
		t.Fatalf("follow-up request missing tool message: %#v", provider.requests[1].Messages)
	}
	if toolMsg.Text != viewImageUnsupportedMessage {
		t.Fatalf("expected unsupported view_image message, got %#v", toolMsg)
	}
	if len(toolMsg.ToolContentItems) != 0 {
		t.Fatalf("unsupported view_image result must not attach image content: %#v", toolMsg.ToolContentItems)
	}
}

func findToolDefinitionForTest(tools []ToolDefinition, name string) *ToolDefinition {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

func findToolMessageForTest(messages []Message, callID string) *Message {
	for i := range messages {
		if messages[i].Role == "tool" && messages[i].ToolCallID == callID {
			return &messages[i]
		}
	}
	return nil
}

func TestAgentDefersEditToolWhenMixedWithReadOnlyTool(t *testing.T) {
	root := t.TempDir()
	readTool := &staticTool{name: "read_file", output: "fresh source"}
	patchTool := &staticTool{name: "apply_patch", output: "patched"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			multiToolCallResponse(
				ToolCall{
					ID:        "call-patch-first",
					Name:      "apply_patch",
					Arguments: `{"patch":"*** Begin Patch\n*** End Patch\n"}`,
				},
				ToolCall{
					ID:        "call-read-second",
					Name:      "read_file",
					Arguments: `{"path":"sample.txt"}`,
				},
			),
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-patch-alone",
						Name:      "apply_patch",
						Arguments: `{"patch":"*** Begin Patch\n*** End Patch\n"}`,
					}},
				},
			},
			{Message: Message{Role: "assistant", Text: "done"}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(readTool, patchTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "inspect and fix"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if readTool.calls != 1 {
		t.Fatalf("expected read_file to execute once before the deferred edit retry, got %d", readTool.calls)
	}
	if patchTool.calls != 1 {
		t.Fatalf("expected mixed apply_patch to be deferred and only the standalone retry to execute, got %d", patchTool.calls)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected a second request after mixed tool-call deferral, got %d", len(provider.requests))
	}
	messages := provider.requests[1].Messages
	var deferredPatch Message
	var readResult Message
	for _, msg := range messages {
		if msg.Role == "tool" && msg.ToolCallID == "call-patch-first" {
			deferredPatch = msg
		}
		if msg.Role == "tool" && msg.ToolCallID == "call-read-second" {
			readResult = msg
		}
	}
	if !deferredPatch.IsError || !strings.Contains(deferredPatch.Text, "NOT_EXECUTED") {
		t.Fatalf("expected synthetic deferred edit tool result, got %#v", deferredPatch)
	}
	if !toolMetaBool(deferredPatch.ToolMeta, "deferred") || toolMetaBool(deferredPatch.ToolMeta, "changed_workspace") {
		t.Fatalf("expected deferred edit metadata without workspace change, got %#v", deferredPatch.ToolMeta)
	}
	if readResult.Text != "fresh source" {
		t.Fatalf("expected read_file result to be executed in the same turn, got %#v", readResult)
	}
	lastMessage := messages[len(messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "edit tool") ||
		!(strings.Contains(lastMessage.Text, "only tool call") || strings.Contains(lastMessage.Text, "단독")) {
		t.Fatalf("expected guidance to reissue edit tool by itself, got %#v", lastMessage)
	}
}

func TestAgentDefersNonReadOnlyToolWhenMixedWithEditTool(t *testing.T) {
	root := t.TempDir()
	patchTool := &staticTool{name: "apply_patch", output: "patched"}
	shellTool := &staticTool{name: "run_shell", output: "mutated"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			multiToolCallResponse(
				ToolCall{
					ID:        "call-patch",
					Name:      "apply_patch",
					Arguments: `{"patch":"*** Begin Patch\n*** End Patch\n"}`,
				},
				ToolCall{
					ID:        "call-shell",
					Name:      "run_shell",
					Arguments: `{"command":"Set-Content -Path sample.txt -Value changed"}`,
				},
			),
			{Message: Message{Role: "assistant", Text: "done"}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, shellTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "inspect and fix"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if patchTool.calls != 0 {
		t.Fatalf("expected mixed apply_patch to be deferred, got %d calls", patchTool.calls)
	}
	if shellTool.calls != 0 {
		t.Fatalf("expected non-read-only run_shell to be deferred with the edit, got %d calls", shellTool.calls)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected a second request after mixed tool-call deferral, got %d", len(provider.requests))
	}
	messages := provider.requests[1].Messages
	var deferredShell Message
	for _, msg := range messages {
		if msg.Role == "tool" && msg.ToolCallID == "call-shell" {
			deferredShell = msg
			break
		}
	}
	if !deferredShell.IsError || !strings.Contains(deferredShell.Text, "NOT_EXECUTED") {
		t.Fatalf("expected synthetic deferred shell tool result, got %#v", deferredShell)
	}
	if strings.Contains(deferredShell.Text, "skipped edit") {
		t.Fatalf("deferred non-read-only tool should not be described as a skipped edit: %q", deferredShell.Text)
	}
	if reason := toolMetaString(deferredShell.ToolMeta, "reason"); reason != "non_read_only_tool_deferred_until_edit_is_isolated" {
		t.Fatalf("expected non-read-only deferral reason, got %#v", deferredShell.ToolMeta)
	}
}

func TestAgentDefersCacheOnlyShellWhenMixedWithEditTool(t *testing.T) {
	root := t.TempDir()
	patchTool := &staticTool{name: "apply_patch", output: "patched"}
	shellTool := &staticTool{name: "run_shell", output: "go list output"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			multiToolCallResponse(
				ToolCall{
					ID:        "call-patch",
					Name:      "apply_patch",
					Arguments: `{"patch":"*** Begin Patch\n*** End Patch\n"}`,
				},
				ToolCall{
					ID:        "call-cache-shell",
					Name:      "run_shell",
					Arguments: `{"command":"go list ./..."}`,
				},
			),
			{Message: Message{Role: "assistant", Text: "done"}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, shellTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "inspect and fix"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if patchTool.calls != 0 {
		t.Fatalf("expected mixed apply_patch to be deferred, got %d calls", patchTool.calls)
	}
	if shellTool.calls != 0 {
		t.Fatalf("expected cache-only run_shell to be deferred with the edit, got %d calls", shellTool.calls)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected a second request after mixed tool-call deferral, got %d", len(provider.requests))
	}
	messages := provider.requests[1].Messages
	var deferredShell Message
	for _, msg := range messages {
		if msg.Role == "tool" && msg.ToolCallID == "call-cache-shell" {
			deferredShell = msg
			break
		}
	}
	if !deferredShell.IsError || !strings.Contains(deferredShell.Text, "NOT_EXECUTED") {
		t.Fatalf("expected synthetic deferred cache-only shell result, got %#v", deferredShell)
	}
	if reason := toolMetaString(deferredShell.ToolMeta, "reason"); reason != "non_read_only_tool_deferred_until_edit_is_isolated" {
		t.Fatalf("expected cache-only shell to use non-read-only deferral reason, got %#v", deferredShell.ToolMeta)
	}
}

func TestAgentClosesRemainingToolPlaceholdersAfterInvalidJSON(t *testing.T) {
	root := t.TempDir()
	readTool := &staticTool{name: "read_file", output: "read should not run"}
	listTool := &staticTool{name: "list_files", output: "list should not run"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			multiToolCallResponse(
				ToolCall{
					ID:        "call-bad-json",
					Name:      "read_file",
					Arguments: `{"path":`,
				},
				ToolCall{
					ID:        "call-list-after-bad-json",
					Name:      "list_files",
					Arguments: `{}`,
				},
			),
			{Message: Message{Role: "assistant", Text: "Stopped after invalid tool arguments."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(readTool, listTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect the workspace")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Stopped after invalid tool arguments." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if readTool.calls != 0 {
		t.Fatalf("invalid JSON should be rejected before executing read_file, got %d calls", readTool.calls)
	}
	if listTool.calls != 0 {
		t.Fatalf("remaining tool should be closed without execution after invalid JSON, got %d calls", listTool.calls)
	}
	if !sessionContainsToolResultText(session, "call-bad-json", "invalid JSON") {
		t.Fatalf("expected invalid JSON tool result, messages=%#v", session.Messages)
	}
	if !sessionContainsToolResultText(session, "call-list-after-bad-json", "NOT_EXECUTED") {
		t.Fatalf("expected later placeholder to be closed as NOT_EXECUTED, messages=%#v", session.Messages)
	}
	if sessionContainsInProgressToolResult(session) {
		t.Fatalf("tool placeholders must not remain IN_PROGRESS, messages=%#v", session.Messages)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected one retry turn after invalid JSON guidance, got %d requests", len(provider.requests))
	}
}

func TestAgentVerificationFailurePromptsAnotherTurnBeforeFinalAnswer(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{Message: Message{Role: "assistant", Text: "I made the change."}},
			{Message: Message{Role: "assistant", Text: "Verification is still failing because the tests are already broken upstream."}},
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
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			return VerificationReport{
				ChangedPaths: []string{"main.go"},
				Steps: []VerificationStep{{
					Label:  "go test ./...",
					Stage:  "targeted",
					Status: VerificationFailed,
					Output: "main.go: error: failing test",
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "fix the file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Verification is still failing because the tests are already broken upstream." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected 3 model turns due to verification gating, got %d", len(provider.requests))
	}
	if len(provider.requests[1].Messages) == 0 || !strings.Contains(provider.requests[1].Messages[len(provider.requests[1].Messages)-1].Text, "Likely failure summary") {
		t.Fatalf("expected failure hint to be included before retry, got %#v", provider.requests[1].Messages)
	}
	if !strings.Contains(provider.requests[1].Messages[len(provider.requests[1].Messages)-1].Text, "Suggested repair strategy") {
		t.Fatalf("expected repair strategy in retry prompt, got %#v", provider.requests[1].Messages)
	}
}

func TestAgentDoesNotAutoVerifyNoOpWriteFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{Message: Message{Role: "assistant", Text: "No source changes were needed."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	verifyCalls := 0
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			verifyCalls++
			return VerificationReport{
				ChangedPaths: []string{"main.go"},
				Steps: []VerificationStep{{
					Label:  "go test ./...",
					Stage:  "targeted",
					Status: VerificationFailed,
					Output: "verification must not run for a no-op write",
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "ensure main.go has this content")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "No source changes were needed." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if verifyCalls != 0 {
		t.Fatalf("no-op write_file must not trigger auto verification, got %d call(s)", verifyCalls)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected tool turn and final turn only, got %d requests", len(provider.requests))
	}
	var sawNoOpTool bool
	for _, msg := range session.Messages {
		if msg.Role != "tool" || msg.ToolName != "write_file" {
			continue
		}
		sawNoOpTool = true
		if toolMetaBool(msg.ToolMeta, "changed_workspace") {
			t.Fatalf("no-op tool result must not report changed_workspace=true: %#v", msg.ToolMeta)
		}
		if paths := toolMetaStringSlice(msg.ToolMeta, "changed_paths"); len(paths) != 0 {
			t.Fatalf("no-op tool result must not report changed_paths: %#v", msg.ToolMeta)
		}
	}
	if !sawNoOpTool {
		t.Fatalf("expected write_file tool result in session: %#v", session.Messages)
	}
}

func TestVerificationRepairScopeDoesNotTreatProjectBuildSiblingFailureAsPatchScoped(t *testing.T) {
	agent := &Agent{}
	report := VerificationReport{
		ChangedPaths: []string{"SampleApp/SampleWorker/PathConverter.cpp"},
		Steps: []VerificationStep{{
			Label:   "msbuild SampleApp/SampleWorker/SampleWorker.vcxproj",
			Command: `msbuild "SampleApp/SampleWorker/SampleWorker.vcxproj" /m`,
			Scope:   "SampleApp/SampleWorker/SampleWorker.vcxproj",
			Stage:   "targeted",
			Status:  VerificationFailed,
			Output: strings.Join([]string{
				`F:\repo\SampleApp\Common\Util.h(42,55): error C2039: 'string_view': is not a member of std`,
				`F:\repo\SampleApp\SampleWorker\HandleBreaker.cpp(135,20): error C2039: 'starts_with': is not a member`,
			}, "\n"),
		}},
	}

	decision := agent.verificationFailureRepairScope(report)
	if decision.ShouldRepair {
		t.Fatalf("project build sibling failures must not trigger current patch repair: %#v", decision)
	}
	if !strings.Contains(decision.Reason, "does not reference") {
		t.Fatalf("unexpected decision reason: %#v", decision)
	}
}

func TestVerificationRepairScopeIgnoresArchivedPatchFromPreviousTurn(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{
		{
			Role: "user",
			Text: "old.go를 수정해",
		},
		{
			Role:  "assistant",
			Phase: messagePhaseFinalAnswer,
			Text:  "old.go 수정 완료",
		},
		{
			Role: "user",
			Text: "new.go를 수정해",
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-old",
		Goal:   "old.go를 수정해",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-old-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "old.go",
				Operation: "modify",
			}},
		}},
	}}
	agent := &Agent{Session: session}
	report := VerificationReport{
		ChangedPaths: []string{"new.go"},
		Steps: []VerificationStep{{
			Label:   "go test ./...",
			Command: "go test ./...",
			Scope:   "new.go",
			Stage:   "targeted",
			Status:  VerificationFailed,
			Output:  "new.go: error: failing test",
		}},
	}

	decision := agent.verificationFailureRepairScope(report)
	if containsString(decision.ChangedPaths, "old.go") {
		t.Fatalf("stale archived patch path should not be part of current repair scope: %#v", decision)
	}
	if !containsString(decision.ChangedPaths, "new.go") || !decision.ShouldRepair {
		t.Fatalf("expected current verification path to drive repair scope, got %#v", decision)
	}
}

func TestAgentDoesNotBroadenRepairForOutOfScopeVerificationFailure(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{Message: Message{Role: "assistant", Text: "Updated main.go. Verification failed in an unrelated project, so that blocker remains disclosed."}},
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
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			return VerificationReport{
				GeneratedAt:  time.Now(),
				ChangedPaths: []string{"main.go"},
				Steps: []VerificationStep{{
					Label:  "go test ./...",
					Scope:  "workspace",
					Stage:  "workspace",
					Status: VerificationFailed,
					Output: "other/package_test.go: failing pre-existing test",
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "fix the file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "unrelated project") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected no repair turn beyond disclosure, got %d requests", len(provider.requests))
	}
	lastPrompt := provider.requests[1].Messages[len(provider.requests[1].Messages)-1].Text
	if !strings.Contains(lastPrompt, "outside the current patch scope") && !strings.Contains(lastPrompt, "patch scope") {
		t.Fatalf("expected out-of-scope verification guidance, got:\n%s", lastPrompt)
	}
	if strings.Contains(lastPrompt, "continue repairing only the current patch scope") {
		t.Fatalf("out-of-scope failure must not use in-scope repair prompt:\n%s", lastPrompt)
	}
}

func TestAgentBlocksVerificationRetryAfterOutOfScopeAutomaticFailure(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			toolCallResponse("run_shell", map[string]any{"command": "go test ./..."}),
			{Message: Message{Role: "assistant", Text: "Updated main.go. Verification failed outside the patch scope, so I am reporting that blocker instead of rerunning verification."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	confirmCount := 0
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
		ConfirmVerification: func(plan VerificationPlan) (bool, error) {
			confirmCount++
			return true, nil
		},
	}
	agent := &Agent{
		Config: Config{},
		Client: provider,
		Tools: NewToolRegistry(
			NewWriteFileTool(ws),
			NewRunShellTool(ws),
		),
		Workspace: ws,
		Session:   session,
		Store:     store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			return VerificationReport{
				GeneratedAt:  time.Now(),
				ChangedPaths: []string{"main.go"},
				Steps: []VerificationStep{{
					Label:  "go test ./...",
					Scope:  "workspace",
					Stage:  "workspace",
					Status: VerificationFailed,
					Output: "other/package_test.go: error: pre-existing failure",
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "fix the file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "outside the patch scope") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected a final-answer-only retry request")
	}
	if got := len(provider.requests[1].Tools); got != 0 {
		t.Fatalf("expected tools to be disabled after out-of-scope automatic verification failure, got %d tool definitions", got)
	}
	if confirmCount != 0 {
		t.Fatalf("out-of-scope verification retry must be blocked before shell confirmation, got %d prompts", confirmCount)
	}
	blocked := 0
	for _, msg := range session.Messages {
		if msg.Role == "tool" && msg.ToolName == "run_shell" && strings.Contains(msg.Text, "NOT_EXECUTED: automatic verification already failed outside the current patch scope") {
			blocked++
			if !msg.IsError {
				t.Fatalf("blocked verification retry must be an error tool result: %#v", msg)
			}
			if toolMetaBool(msg.ToolMeta, "success") {
				t.Fatalf("blocked verification retry must not be successful evidence: %#v", msg.ToolMeta)
			}
			if toolMetaBool(msg.ToolMeta, "verification_evidence") {
				t.Fatalf("blocked verification retry must not count as verification evidence: %#v", msg.ToolMeta)
			}
			if !toolMetaBool(msg.ToolMeta, "verification_out_scope") {
				t.Fatalf("blocked verification retry should carry out-of-scope metadata: %#v", msg.ToolMeta)
			}
		}
	}
	if blocked != 1 {
		t.Fatalf("expected one blocked out-of-scope verification retry, got %d", blocked)
	}
	if !sessionContainsText(session, "Do not call run_shell") &&
		!sessionContainsText(session, "final-answer-only") &&
		!sessionContainsText(session, "최종 답변 전용") {
		t.Fatalf("expected follow-up guidance to tell the model not to rerun verification")
	}
}

func TestAgentBlocksAllToolsAfterOutOfScopeAutomaticFailure(t *testing.T) {
	root := t.TempDir()
	readTool := &staticTool{name: "read_file", output: "source"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			{Message: Message{Role: "assistant", Text: "Updated main.go. Verification failed outside the patch scope, so no further tools were used."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{},
		Client: provider,
		Tools: NewToolRegistry(
			NewWriteFileTool(ws),
			readTool,
		),
		Workspace: ws,
		Session:   session,
		Store:     store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			return VerificationReport{
				ChangedPaths: []string{"main.go"},
				Steps: []VerificationStep{{
					Label:  "go test ./...",
					Scope:  "workspace",
					Stage:  "workspace",
					Status: VerificationFailed,
					Output: "other/package_test.go: error: pre-existing failure",
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "fix the file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "outside the patch scope") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if readTool.calls != 0 {
		t.Fatalf("post out-of-scope read_file must be blocked, got %d calls", readTool.calls)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected a retry request")
	}
	if got := len(provider.requests[1].Tools); got != 0 {
		t.Fatalf("expected all tools to be disabled in the post out-of-scope retry request, got %d", got)
	}
	var blockedRead Message
	for _, msg := range session.Messages {
		if msg.Role == "tool" && msg.ToolName == "read_file" {
			blockedRead = msg
			break
		}
	}
	if !blockedRead.IsError || !strings.Contains(blockedRead.Text, "final-answer-only") {
		t.Fatalf("expected read_file to be blocked as final-answer-only, got %#v", blockedRead)
	}
	if !toolMetaBool(blockedRead.ToolMeta, "verification_out_scope") {
		t.Fatalf("expected out-of-scope metadata on blocked read_file, got %#v", blockedRead.ToolMeta)
	}
}

func TestAgentReturnsFinalWithoutPostChangeReviewAfterOutOfScopeAutomaticFailure(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{Message: Message{Role: "assistant", Text: "Updated main.go."}},
			{
				Message: Message{
					Role: "assistant",
					Text: strings.Join([]string{
						"REVIEW_RESULT",
						"verdict: needs_revision",
						"summary: this post-change review should not run after terminal out-of-scope verification",
						"findings:",
						"- id: RF-POST",
						"  severity: high",
						"  category: correctness",
						"  title: should not be consumed",
						"  evidence: post-change review ran unexpectedly",
						"  required_fix: do not re-open the repair loop",
					}, "\n"),
				},
			},
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
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			return VerificationReport{
				ChangedPaths: []string{"main.go"},
				Steps: []VerificationStep{{
					Label:  "go test ./...",
					Scope:  "workspace",
					Stage:  "workspace",
					Status: VerificationFailed,
					Output: "other/package_test.go: error: pre-existing failure",
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "fix the file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Verification note") && !strings.Contains(reply, "검증 참고") {
		t.Fatalf("expected out-of-scope verification disclosure to be appended, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("terminal out-of-scope state must not run post-change review or final review turns, got %d requests", len(provider.requests))
	}
	if got := len(provider.requests[1].Tools); got != 0 {
		t.Fatalf("expected all tools to be disabled in terminal out-of-scope final request, got %d", got)
	}
	if session.LastReviewRun != nil && strings.EqualFold(session.LastReviewRun.Trigger, "post_change") {
		t.Fatalf("post-change review must not run after terminal out-of-scope verification, got %#v", session.LastReviewRun)
	}
}

func TestAgentOutOfScopeFinalDisclosureHandlesEmptyReply(t *testing.T) {
	agent := &Agent{
		Config: Config{},
		Session: &Session{
			LastVerification: &VerificationReport{
				Steps: []VerificationStep{{
					Label:  "go test ./...",
					Scope:  "workspace",
					Stage:  "workspace",
					Status: VerificationFailed,
					Output: "other/package_test.go: pre-existing failure",
				}},
			},
		},
	}

	reply := agent.ensureOutOfScopeVerificationFinalDisclosure("")
	if strings.TrimSpace(reply) == "" {
		t.Fatalf("expected non-empty verification disclosure for empty final reply")
	}
	if !strings.Contains(reply, "Verification note") && !strings.Contains(reply, "검증 참고") {
		t.Fatalf("expected verification disclosure, got %q", reply)
	}
}

func TestAgentAsksBeforeAutomaticVerificationAndSkipsOnNo(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{Message: Message{Role: "assistant", Text: "Implemented without running verification."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	history := &VerificationHistoryStore{Path: filepath.Join(root, "verification-history.json")}
	ws := Workspace{BaseRoot: root, Root: root}
	promptCount := 0
	verifyCount := 0
	var progress []string
	agent := &Agent{
		Config:        Config{},
		Client:        provider,
		Tools:         NewToolRegistry(NewWriteFileTool(ws)),
		Workspace:     ws,
		Session:       session,
		Store:         store,
		VerifyHistory: history,
		EmitProgress: func(line string) {
			progress = append(progress, line)
		},
		PromptConfirmAutoVerify: func(plan VerificationPlan) (bool, error) {
			promptCount++
			if len(plan.ChangedPaths) == 0 {
				t.Fatalf("expected changed paths in verification prompt plan")
			}
			return false, nil
		},
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			verifyCount++
			return VerificationReport{}, false
		},
	}

	reply, err := agent.Reply(context.Background(), "fix the file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Implemented without running verification." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if promptCount != 1 {
		t.Fatalf("expected one verification confirmation prompt, got %d", promptCount)
	}
	if verifyCount != 0 {
		t.Fatalf("verification must not run after user declines, got %d runs", verifyCount)
	}
	if session.LastVerification == nil {
		t.Fatalf("declined verification should record a skipped verification report")
	}
	if session.LastVerification.Steps[0].Status != VerificationSkipped {
		t.Fatalf("declined verification should be skipped, got %#v", session.LastVerification)
	}
	if !strings.Contains(session.LastVerification.Decision, "declined") {
		t.Fatalf("declined verification should preserve the decision, got %#v", session.LastVerification)
	}
	if session.TaskState == nil || !slices.Contains(session.TaskState.PendingChecks, verificationPendingCheck) {
		t.Fatalf("declined verification should leave verification pending, got %#v", session.TaskState)
	}
	if !containsStringMatching(progress, "skipped") && !containsStringMatching(progress, "생략") {
		t.Fatalf("declined verification should emit skipped progress, got %#v", progress)
	}
	if containsStringMatching(progress, "finished") {
		t.Fatalf("declined verification must not use completed progress, got %#v", progress)
	}
	latest, ok, err := history.Latest(root)
	if err != nil {
		t.Fatalf("load latest verification history: %v", err)
	}
	if !ok || latest.Report.Steps[0].Status != VerificationSkipped {
		t.Fatalf("verification history should record skipped report, got ok=%v latest=%#v", ok, latest)
	}
}

func TestAgentBlocksVerificationRetryAfterSkippedAutomaticVerification(t *testing.T) {
	root := t.TempDir()
	shellTool := &staticTool{name: "run_shell", output: "go test passed"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			toolCallResponse("run_shell", map[string]any{"command": "go test ./..."}),
			{Message: Message{Role: "assistant", Text: "Verification was not run because the user declined it, so I am reporting the unverified change."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	promptCount := 0
	verifyCount := 0
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), shellTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
		PromptConfirmAutoVerify: func(plan VerificationPlan) (bool, error) {
			promptCount++
			return false, nil
		},
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			verifyCount++
			return VerificationReport{}, false
		},
	}

	reply, err := agent.Reply(context.Background(), "fix the file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "not run") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if promptCount != 1 {
		t.Fatalf("declined automatic verification should prompt once, got %d", promptCount)
	}
	if verifyCount != 0 {
		t.Fatalf("verification callback must not run after decline, got %d runs", verifyCount)
	}
	if shellTool.calls != 0 {
		t.Fatalf("verification retry must be blocked before tool execution, got %d calls", shellTool.calls)
	}
	if !sessionContainsText(session, "NOT_EXECUTED: a build, test, or verification command was already skipped") {
		t.Fatalf("expected skipped-verification retry to be returned as NOT_EXECUTED")
	}
}

func TestAgentRecordsAutomaticVerificationPromptErrorAsSkipped(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{Message: Message{Role: "assistant", Text: "Implemented without verification."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	verifyCount := 0
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		PromptConfirmAutoVerify: func(plan VerificationPlan) (bool, error) {
			if len(plan.ChangedPaths) == 0 {
				t.Fatalf("expected changed paths in verification prompt plan")
			}
			return false, fmt.Errorf("prompt closed")
		},
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			verifyCount++
			return VerificationReport{}, false
		},
	}

	reply, err := agent.Reply(context.Background(), "fix the file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Implemented without verification." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if verifyCount != 0 {
		t.Fatalf("verification must not run after prompt error, got %d runs", verifyCount)
	}
	if session.LastVerification == nil || session.LastVerification.Steps[0].Status != VerificationSkipped {
		t.Fatalf("prompt error should record skipped verification, got %#v", session.LastVerification)
	}
	if !strings.Contains(session.LastVerification.Decision, "prompt closed") {
		t.Fatalf("prompt error should be visible in decision, got %#v", session.LastVerification)
	}
}

func TestAgentRunsAutomaticVerificationAfterConfirmation(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{Message: Message{Role: "assistant", Text: "Implemented and verified."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	promptCount := 0
	verifyCount := 0
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		PromptConfirmAutoVerify: func(plan VerificationPlan) (bool, error) {
			promptCount++
			if len(plan.ChangedPaths) == 0 {
				t.Fatalf("expected changed paths in verification prompt plan")
			}
			return true, nil
		},
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			verifyCount++
			return VerificationReport{
				Steps: []VerificationStep{{
					Label:  "go test ./...",
					Status: VerificationPassed,
					Output: "ok",
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "fix and verify")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Implemented and verified." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if promptCount != 1 || verifyCount != 1 {
		t.Fatalf("expected one prompt and one verification run, got prompts=%d verify=%d", promptCount, verifyCount)
	}
	if session.LastVerification == nil || session.LastVerification.HasFailures() {
		t.Fatalf("expected passing verification report, got %#v", session.LastVerification)
	}
}

func TestAgentCanRepairAfterFailedVerificationAndReturnAfterPass(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {}\n"}),
			{Message: Message{Role: "assistant", Text: "Implemented the fix and verification now passes."}},
		},
	}
	verifyCount := 0
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
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			verifyCount++
			if verifyCount == 1 {
				return VerificationReport{
					ChangedPaths: []string{"main.go"},
					Steps: []VerificationStep{{
						Label:  "go test ./...",
						Stage:  "targeted",
						Status: VerificationFailed,
						Output: "main.go: failing test",
					}},
				}, true
			}
			return VerificationReport{
				ChangedPaths: []string{"main.go"},
				Steps: []VerificationStep{{
					Label:  "go test ./...",
					Status: VerificationPassed,
					Output: "ok",
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "fix and verify")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Implemented the fix and verification now passes." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if verifyCount != 2 {
		t.Fatalf("expected verifier to run twice, got %d", verifyCount)
	}
	if session.LastVerification == nil || session.LastVerification.HasFailures() {
		t.Fatalf("expected final verification report to be passing, got %#v", session.LastVerification)
	}
	if session.ActiveEditLoop != nil {
		t.Fatalf("expected edit loop to be finalized, got %#v", session.ActiveEditLoop)
	}
	if len(session.EditLoops) == 0 {
		t.Fatalf("expected finalized edit-loop ledger")
	}
	loop := session.EditLoops[0]
	if loop.Status != editLoopStatusCompleted {
		t.Fatalf("expected completed edit-loop status, got %#v", loop)
	}
	if loop.VerificationStatus != "passed" {
		t.Fatalf("expected passing verification in edit loop, got %#v", loop)
	}
	if loop.RetryCount == 0 {
		t.Fatalf("expected failed verification retry to be recorded, got %#v", loop)
	}
	if !slices.Contains(loop.ChangedPaths, "main.go") {
		t.Fatalf("expected changed path in edit loop, got %#v", loop.ChangedPaths)
	}
	if len(loop.RemainingRisks) != 0 {
		t.Fatalf("expected passing retry to clear stale verification risks, got %#v", loop.RemainingRisks)
	}
}

func TestAgentDoesNotTreatGitStatusChangedPathsAsAnEdit(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "driver.cpp"), []byte("int old_value = 0;\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runTestGit(t, root, "add", "driver.cpp")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(root, "driver.cpp"), []byte("int old_value = 1;\n"), 0o644); err != nil {
		t.Fatalf("dirty file: %v", err)
	}

	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("git_status", map[string]any{}),
			{Message: Message{Role: "assistant", Text: "상태만 확인했고 수정은 하지 않았습니다."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	verifyCount := 0
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewGitStatusTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			verifyCount++
			return VerificationReport{}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "수정해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "수정은 하지 않았습니다") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if verifyCount != 0 {
		t.Fatalf("git_status changed_paths must not trigger automatic verification, got %d runs", verifyCount)
	}
}

func TestAgentAnalysisOnlyRequestHidesEditTools(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "원인은 ETW 세션 초기화 순서 문제입니다."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewApplyPatchTool(ws), NewWriteFileTool(ws), NewReplaceInFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@SampleApp/Common/ETWConsumer.cpp ETWConsumer가 제대로 동작할 수 없는 로그를 수집했는데 원인을 분석해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "원인은") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) == 0 {
		t.Fatalf("expected at least one model request")
	}
	var toolNames []string
	for _, def := range provider.requests[0].Tools {
		toolNames = append(toolNames, def.Name)
	}
	if containsString(toolNames, "apply_patch") || containsString(toolNames, "write_file") || containsString(toolNames, "replace_in_file") {
		t.Fatalf("expected edit tools to be hidden for analysis-only request, got %v", toolNames)
	}
	if !containsString(toolNames, "read_file") {
		t.Fatalf("expected read tools to remain available, got %v", toolNames)
	}
}

func TestAgentRetriesEmptyResponseForAnalysisRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VAllocAnalyzer.cpp"), []byte("int Check()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: ""}},
			{Message: Message{Role: "assistant", Text: "문제점은 아직 확인되지 않았지만 경계 조건 검토가 필요합니다."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewGrepTool(ws), NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@VAllocAnalyzer.cpp 에 버그가 있는지 검토해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "검토") && !strings.Contains(reply, "문제점") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected one retry after empty response, got %d requests", len(provider.requests))
	}
	lastMessages := provider.requests[1].Messages
	if len(lastMessages) == 0 {
		t.Fatalf("expected retry guidance message")
	}
	lastText := lastMessages[len(lastMessages)-1].Text
	if !strings.Contains(lastText, "read-only analysis or review request") {
		t.Fatalf("expected analysis-specific empty-response guidance, got %q", lastText)
	}
	if !strings.Contains(lastText, "read_file") {
		t.Fatalf("expected retry guidance to mention read_file, got %q", lastText)
	}
}

func TestAgentEmptyResponseErrorIncludesProviderModelAndAfterTool(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{"path": "."}),
			{Message: Message{Role: "assistant", Text: ""}, StopReason: "stream_empty_fallback_empty_after_stream_retry"},
			{Message: Message{Role: "assistant", Text: ""}, StopReason: "stream_empty_fallback_empty_after_stream_retry"},
		},
	}
	session := NewSession(root, "openrouter", "openai/gpt-oss-120b", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "리서치 문서를 파일로 작성해줘")
	if err == nil {
		t.Fatalf("expected empty response failure")
	}
	text := err.Error()
	if !strings.Contains(text, "provider=openrouter") {
		t.Fatalf("expected provider in error, got %q", text)
	}
	if !strings.Contains(text, "model=openai/gpt-oss-120b") {
		t.Fatalf("expected model in error, got %q", text)
	}
	if !strings.Contains(text, "stop_reason=stream_empty_fallback_empty_after_stream_retry") {
		t.Fatalf("expected detailed stop reason in error, got %q", text)
	}
	if !strings.Contains(text, "after_tool=true") {
		t.Fatalf("expected after_tool flag in error, got %q", text)
	}
}

func TestSummarizeToolInvocationReadFileIncludesPathAndRange(t *testing.T) {
	call := ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"SampleApp/Common/ETWConsumer.cpp","start_line":10,"end_line":42}`,
	}

	got := summarizeToolInvocation(Config{AutoLocale: boolPtr(false)}, call)
	want := "Using read_file on SampleApp/Common/ETWConsumer.cpp:10-42..."
	if got != want {
		t.Fatalf("unexpected summary: got %q want %q", got, want)
	}
}

func TestSummarizeToolInvocationRunShellIncludesCommand(t *testing.T) {
	call := ToolCall{
		Name:      "run_shell",
		Arguments: `{"command":"rg -n \"systemPrompt\" agent.go"}`,
	}

	got := summarizeToolInvocation(Config{AutoLocale: boolPtr(false)}, call)
	if !strings.Contains(got, "Running shell: rg -n") {
		t.Fatalf("unexpected shell summary: %q", got)
	}
}

func TestSummarizeToolInvocationVerificationRequestsApprovalNotExecution(t *testing.T) {
	shellCall := ToolCall{
		Name:      "run_shell",
		Arguments: `{"command":"go test ./cmd/kernforge"}`,
	}
	shellSummary := summarizeToolInvocation(Config{AutoLocale: boolPtr(false)}, shellCall)
	if !strings.Contains(shellSummary, "Requesting verification command approval") {
		t.Fatalf("expected verification approval summary, got %q", shellSummary)
	}
	if strings.Contains(shellSummary, "Running shell") {
		t.Fatalf("verification summary must not claim execution before approval: %q", shellSummary)
	}

	backgroundCall := ToolCall{
		Name:      "run_shell_background",
		Arguments: `{"command":"msbuild SampleApp.sln /m"}`,
	}
	backgroundSummary := summarizeToolInvocation(Config{AutoLocale: boolPtr(false)}, backgroundCall)
	if !strings.Contains(backgroundSummary, "Requesting background verification approval") {
		t.Fatalf("expected background verification approval summary, got %q", backgroundSummary)
	}
	if strings.Contains(backgroundSummary, "Starting background shell") {
		t.Fatalf("background verification summary must not claim job start before approval: %q", backgroundSummary)
	}

	bundleCall := ToolCall{
		Name:      "run_shell_bundle_background",
		Arguments: `{"commands":["go test ./cmd/kernforge","rg -n \"foo\" cmd/kernforge"]}`,
	}
	bundleSummary := summarizeToolInvocation(Config{AutoLocale: boolPtr(false)}, bundleCall)
	if !strings.Contains(bundleSummary, "Requesting background verification bundle approval") {
		t.Fatalf("expected bundle verification approval summary, got %q", bundleSummary)
	}
	if strings.Contains(bundleSummary, "Starting") {
		t.Fatalf("bundle verification summary must not claim job start before approval: %q", bundleSummary)
	}
}

func TestSummarizeToolCompletionVerificationDeclineIsSkippedNotStarted(t *testing.T) {
	out := "verification command skipped because the user declined to run it. Do not retry this verification command or poll a background job for it unless the user explicitly approves verification; disclose that verification was not run."
	call := ToolCall{
		Name:      "run_shell_background",
		Arguments: `{"command":"go test ./cmd/kernforge"}`,
	}
	got := summarizeToolCompletion(Config{AutoLocale: boolPtr(false)}, call, out)
	if !strings.Contains(got, "Background verification skipped") {
		t.Fatalf("expected skipped background verification completion, got %q", got)
	}
	if strings.Contains(got, "started") {
		t.Fatalf("declined background verification must not be summarized as started: %q", got)
	}
	if !strings.Contains(skippedVerificationCommandText(), "Do not relabel resolved code-review findings") {
		t.Fatalf("skipped verification guidance should separate verification gaps from code findings: %q", skippedVerificationCommandText())
	}
}

func TestSummarizeToolInvocationWebResearchIncludesIntent(t *testing.T) {
	searchCall := ToolCall{
		Name:      "mcp__web_research__search_web",
		Arguments: `{"query":"Microsoft Learn GetVolumePathNamesForVolumeName ERROR_MORE_DATA returnCch"}`,
	}
	searchSummary := summarizeToolInvocation(Config{AutoLocale: boolPtr(false)}, searchCall)
	if !strings.Contains(searchSummary, "Web research requested:") ||
		!strings.Contains(searchSummary, "GetVolumePathNamesForVolumeName") {
		t.Fatalf("expected web search intent in summary, got %q", searchSummary)
	}

	fetchCall := ToolCall{
		Name:      "mcp__web_research__fetch_url",
		Arguments: `{"url":"https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getvolumepathnamesforvolumenamew"}`,
	}
	fetchSummary := summarizeToolInvocation(Config{AutoLocale: boolPtr(false)}, fetchCall)
	if !strings.Contains(fetchSummary, "Web research requested:") ||
		!strings.Contains(fetchSummary, "learn.microsoft.com") {
		t.Fatalf("expected web fetch intent in summary, got %q", fetchSummary)
	}
}

func TestAgentInjectsLatestProjectAnalysisContextOnFirstTurn(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "used cached analysis"}},
		},
	}
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis dir: %v", err)
	}
	pack := KnowledgePack{
		RunID:          "run-1",
		Goal:           "map SampleWorker architecture",
		Root:           root,
		ProjectSummary: "SampleWorker owns telemetry collection and event triage.",
		Subsystems: []KnowledgeSubsystem{
			{
				Title:            "SampleWorker Runtime",
				Group:            "Forensic Analysis",
				Responsibilities: []string{"Collect telemetry", "Normalize suspicious events"},
				EntryPoints:      []string{"SampleWorker/main.cpp"},
				KeyFiles:         []string{"SampleWorker/main.cpp", "SampleWorker/collector.cpp"},
				Dependencies:     []string{"Common/ipc.hpp"},
				EvidenceFiles:    []string{"SampleWorker/main.cpp"},
			},
		},
	}
	data, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), data, 0o644); err != nil {
		t.Fatalf("write knowledge pack: %v", err)
	}
	corpus := VectorCorpus{
		RunID: "run-1",
		Documents: []VectorCorpusDocument{
			{
				ID:       "subsystem:sampleworker-runtime",
				Kind:     "subsystem",
				Title:    "Forensic Analysis: SampleWorker Runtime",
				Text:     "Startup path initializes telemetry collectors and triage workers.",
				PathHint: "SampleWorker/main.cpp",
			},
		},
	}
	corpusData, err := json.Marshal(corpus)
	if err != nil {
		t.Fatalf("marshal vector corpus: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "vector_corpus.json"), corpusData, 0o644); err != nil {
		t.Fatalf("write vector corpus: %v", err)
	}
	index := SemanticIndex{
		RunID: "run-1",
		Files: []SemanticIndexedFile{
			{Path: "SampleWorker/main.cpp", ImportanceScore: 95, Tags: []string{"entrypoint", "startup"}},
		},
		Symbols: []SemanticSymbol{
			{Name: "WorkerBootstrap", Kind: "function", File: "SampleWorker/main.cpp", Module: "SampleWorker"},
		},
	}
	indexData, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("marshal structural index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "structural_index.json"), indexData, 0o644); err != nil {
		t.Fatalf("write structural index: %v", err)
	}

	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "Explain SampleWorker startup flow.")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "used cached analysis") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) != 1 || len(provider.requests[0].Messages) == 0 {
		t.Fatalf("expected one provider request with messages, got %#v", provider.requests)
	}
	userText := ""
	for _, msg := range provider.requests[0].Messages {
		if strings.Contains(msg.Text, "Relevant project analysis from past analyze-project runs") {
			userText = msg.Text
			break
		}
	}
	if !strings.Contains(userText, "Relevant project analysis from past analyze-project runs") {
		t.Fatalf("expected cached analysis context in user text, got %q", userText)
	}
	if !strings.Contains(userText, "Forensic Analysis: SampleWorker Runtime") {
		t.Fatalf("expected matching subsystem in injected analysis context, got %q", userText)
	}
	if !strings.Contains(userText, "Relevant vector documents") {
		t.Fatalf("expected vector corpus context in injected analysis context, got %q", userText)
	}
	if !strings.Contains(userText, "Relevant structural index hits") {
		t.Fatalf("expected structural index hits in injected analysis context, got %q", userText)
	}
}

func TestAgentDoesNotRepeatLatestProjectAnalysisContextAfterFirstTurn(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "first"}},
			{Message: Message{Role: "assistant", Text: "second"}},
		},
	}
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis dir: %v", err)
	}
	pack := KnowledgePack{
		RunID:          "run-1",
		Goal:           "map worker architecture",
		Root:           root,
		ProjectSummary: "Worker summary.",
		Subsystems: []KnowledgeSubsystem{
			{
				Title:         "Worker Runtime",
				Group:         "Forensic Analysis",
				KeyFiles:      []string{"SampleWorker/main.cpp"},
				EvidenceFiles: []string{"SampleWorker/main.cpp"},
			},
		},
	}
	data, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
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
		Tools:     NewToolRegistry(),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "Explain SampleWorker."); err != nil {
		t.Fatalf("first Reply: %v", err)
	}
	if _, err := agent.Reply(context.Background(), "Now summarize risks only."); err != nil {
		t.Fatalf("second Reply: %v", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected two provider requests, got %d", len(provider.requests))
	}
	firstUserText := ""
	for _, msg := range provider.requests[0].Messages {
		if strings.Contains(msg.Text, "Relevant project analysis from past analyze-project runs") {
			firstUserText = msg.Text
			break
		}
	}
	secondUserText := provider.requests[1].Messages[len(provider.requests[1].Messages)-1].Text
	if !strings.Contains(firstUserText, "Relevant project analysis from past analyze-project runs") {
		t.Fatalf("expected first turn to include analysis context, got %q", firstUserText)
	}
	if strings.Contains(secondUserText, "Relevant project analysis from past analyze-project runs") {
		t.Fatalf("expected second turn not to repeat analysis context, got %q", secondUserText)
	}
}

func TestAgentReinjectsLatestProjectAnalysisContextWhenQueryChangesMaterially(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "first"}},
			{Message: Message{Role: "assistant", Text: "second"}},
		},
	}
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis dir: %v", err)
	}
	pack := KnowledgePack{
		RunID:          "run-2",
		Goal:           "map worker and common architecture",
		Root:           root,
		ProjectSummary: "Worker and Common cooperate over IPC.",
		Subsystems: []KnowledgeSubsystem{
			{
				Title:         "Worker Runtime",
				Group:         "Forensic Analysis",
				KeyFiles:      []string{"SampleWorker/main.cpp"},
				EvidenceFiles: []string{"SampleWorker/main.cpp"},
			},
			{
				Title:            "IPC Layer",
				Group:            "Shared Infrastructure",
				Responsibilities: []string{"Owns pipe framing"},
				KeyFiles:         []string{"Common/ipc.cpp"},
				EvidenceFiles:    []string{"Common/ipc.cpp"},
			},
		},
	}
	packData, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), packData, 0o644); err != nil {
		t.Fatalf("write knowledge pack: %v", err)
	}
	index := SemanticIndex{
		RunID: "run-2",
		Files: []SemanticIndexedFile{
			{Path: "Common/ipc.cpp", ImportanceScore: 80, Tags: []string{"ipc", "pipe"}},
		},
		Symbols: []SemanticSymbol{
			{Name: "PipeRouter", Kind: "class", File: "Common/ipc.cpp", Module: "Common"},
		},
	}
	indexData, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("marshal structural index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "structural_index.json"), indexData, 0o644); err != nil {
		t.Fatalf("write structural index: %v", err)
	}

	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "Explain SampleWorker startup flow."); err != nil {
		t.Fatalf("first Reply: %v", err)
	}
	if _, err := agent.Reply(context.Background(), "Explain Common IPC module architecture in detail."); err != nil {
		t.Fatalf("second Reply: %v", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected two provider requests, got %d", len(provider.requests))
	}
	secondUserText := ""
	for _, msg := range provider.requests[1].Messages {
		if strings.Contains(msg.Text, "Relevant project analysis from past analyze-project runs") {
			secondUserText = msg.Text
			break
		}
	}
	if !strings.Contains(secondUserText, "Relevant project analysis from past analyze-project runs") {
		t.Fatalf("expected materially changed query to reinject analysis context, got %q", secondUserText)
	}
	if !strings.Contains(secondUserText, "Common/ipc.cpp") {
		t.Fatalf("expected reinjected context to include Common IPC hits, got %q", secondUserText)
	}
}

func TestAgentUsesCachedProjectAnalysisFastPathWithoutTools(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "SampleWorker startup begins in main.cpp and then initializes telemetry collectors."}},
		},
	}
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis dir: %v", err)
	}
	pack := KnowledgePack{
		RunID:          "run-fastpath",
		Goal:           "map worker architecture",
		Root:           root,
		ProjectSummary: "SampleWorker owns startup and telemetry collection.",
		Subsystems: []KnowledgeSubsystem{
			{
				Title:         "Worker Runtime",
				Group:         "Forensic Analysis",
				KeyFiles:      []string{"SampleWorker/main.cpp"},
				EvidenceFiles: []string{"SampleWorker/main.cpp"},
			},
		},
	}
	packData, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), packData, 0o644); err != nil {
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

	reply, err := agent.Reply(context.Background(), "Explain SampleWorker startup flow.")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "SampleWorker startup begins") {
		t.Fatalf("unexpected fast-path reply: %q", reply)
	}
	if strings.Contains(reply, "Cached analysis fast-path") || strings.Contains(reply, "NEEDS_TOOLS") {
		t.Fatalf("expected internal fast-path markers to be suppressed, got %q", reply)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected only one provider request via fast-path, got %d", len(provider.requests))
	}
	if len(provider.requests[0].Tools) != 0 {
		t.Fatalf("expected fast-path request to expose no tools, got %#v", provider.requests[0].Tools)
	}
	lastMessage := provider.requests[0].Messages[len(provider.requests[0].Messages)-1].Text
	if !strings.Contains(lastMessage, "Fast-path check") {
		t.Fatalf("expected fast-path instruction in request, got %q", lastMessage)
	}
}

func TestAgentDeepStructureFastPathReceivesAnswerPackContract(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "GuardRuntime 구조는 Startup, IOCTL/RPC dispatch, validation anchors를 기준으로 설명할 수 있다."}},
		},
	}
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis dir: %v", err)
	}
	writeDeepStructureAnalysisLatestForTest(t, latestDir)

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

	reply, err := agent.Reply(context.Background(), "이 프로젝트 구조와 실행 흐름을 자세히 설명해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "GuardRuntime 구조") {
		t.Fatalf("unexpected fast-path reply: %q", reply)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected deep QA fast-path to answer in one provider request, got %d", len(provider.requests))
	}
	if len(provider.requests[0].Tools) != 0 {
		t.Fatalf("expected deep QA fast-path request to expose no tools, got %#v", provider.requests[0].Tools)
	}
	injectedUserText := ""
	for _, msg := range provider.requests[0].Messages {
		if strings.Contains(msg.Text, "Relevant project analysis from past analyze-project runs") {
			injectedUserText = msg.Text
			break
		}
	}
	if !strings.Contains(injectedUserText, "Project structure answer pack") {
		t.Fatalf("expected deep QA answer pack in injected context, got %q", injectedUserText)
	}
	for _, needle := range []string{"Answer contract", "Source anchors", "Graph views", "Relevant structural index v2 hits"} {
		if !strings.Contains(injectedUserText, needle) {
			t.Fatalf("expected injected context to contain %q, got %q", needle, injectedUserText)
		}
	}
	lastMessage := provider.requests[0].Messages[len(provider.requests[0].Messages)-1].Text
	if !strings.Contains(lastMessage, "Project structure answer pack") || !strings.Contains(lastMessage, "structure layers") {
		t.Fatalf("expected stricter deep QA fast-path instruction, got %q", lastMessage)
	}
	for _, needle := range []string{
		"Prefer the latest project analysis over persistent memory",
		"Windows kernel/WDM .sys driver, not a DLL",
		"Separate user-mode IOCTL/control-client wrappers from kernel-side IRP/IOCTL dispatch and validation",
	} {
		if !strings.Contains(lastMessage, needle) {
			t.Fatalf("expected deep QA fast-path instruction to contain %q, got %q", needle, lastMessage)
		}
	}
}

func TestAgentFallsBackToNormalToolLoopWhenCachedProjectAnalysisFastPathNeedsTools(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: projectAnalysisFastPathNeedsTools + "\n\nCached analysis is not enough."}},
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			{Message: Message{Role: "assistant", Text: "Read the file and answered with verified details."}},
		},
	}
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis dir: %v", err)
	}
	pack := KnowledgePack{
		RunID:          "run-fastpath-2",
		Goal:           "map worker architecture",
		Root:           root,
		ProjectSummary: "SampleWorker summary.",
		Subsystems: []KnowledgeSubsystem{
			{
				Title:         "Worker Runtime",
				Group:         "Forensic Analysis",
				KeyFiles:      []string{"main.go"},
				EvidenceFiles: []string{"main.go"},
			},
		},
	}
	packData, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), packData, 0o644); err != nil {
		t.Fatalf("write knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
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

	reply, err := agent.Reply(context.Background(), "Explain SampleWorker startup flow in verified detail.")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "verified details") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected preflight plus normal tool loop, got %d requests", len(provider.requests))
	}
	if len(provider.requests[0].Tools) != 0 {
		t.Fatalf("expected preflight request to expose no tools")
	}
	if len(provider.requests[1].Tools) == 0 {
		t.Fatalf("expected fallback request to expose tools")
	}
}

func writeDeepStructureAnalysisLatestForTest(t *testing.T, latestDir string) {
	t.Helper()
	run := sampleProjectStructureQARun()
	manifest := buildAnalysisDocsManifestForTest(run)
	corpus := VectorCorpus{
		RunID:     run.Summary.RunID,
		Goal:      run.Summary.Goal,
		Documents: buildAnalysisDocsVectorDocuments(run),
	}
	items := map[string]any{
		"knowledge_pack.json":      run.KnowledgePack,
		"snapshot.json":            run.Snapshot,
		"vector_corpus.json":       corpus,
		"structural_index_v2.json": run.SemanticIndexV2,
		"unreal_graph.json":        run.UnrealGraph,
		"docs_manifest.json":       manifest,
	}
	for name, value := range items {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(latestDir, name), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestAgentSkipsAutomaticVerificationWhenDisabled(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{Message: Message{Role: "assistant", Text: "Updated the file."}},
		},
	}
	verifyCount := 0
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	cfg := DefaultConfig(root)
	cfg.AutoVerify = boolPtr(false)
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			verifyCount++
			return VerificationReport{Steps: []VerificationStep{{Label: "verify", Status: VerificationPassed}}}, true
		},
	}

	if _, err := agent.Reply(context.Background(), "update the file"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if verifyCount != 0 {
		t.Fatalf("expected automatic verification to be disabled, got %d runs", verifyCount)
	}
}

func TestAgentPromptsToDisableAutoVerifyOnFirstMissingToolFailure(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{Message: Message{Role: "assistant", Text: "Implemented the change, but verification was disabled because the local build toolchain is unavailable."}},
		},
	}
	verifyCount := 0
	promptCount := 0
	cfg := DefaultConfig(root)
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
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			verifyCount++
			return VerificationReport{
				Steps: []VerificationStep{{
					Label:       "msbuild demo.sln",
					Command:     "msbuild demo.sln /m",
					Status:      VerificationFailed,
					FailureKind: "command_not_found",
					Hint:        "A required verification tool could not be started.",
					Output:      "msbuild : The term 'msbuild' is not recognized as the name of a cmdlet, function, script file, or executable program.",
				}},
			}, true
		},
	}
	agent.PromptResolveAutoVerifyFailure = func(report VerificationReport) (AutoVerifyFailureResolution, error) {
		promptCount++
		if !report.HasCommandMissingFailure() {
			t.Fatalf("expected command-missing failure report")
		}
		agent.Config.AutoVerify = boolPtr(false)
		return AutoVerifyFailureDisable, nil
	}

	reply, err := agent.Reply(context.Background(), "make the change")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "verification was disabled") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if verifyCount != 1 {
		t.Fatalf("expected first missing-tool verification failure to trigger disable prompt, got %d verification runs", verifyCount)
	}
	if promptCount != 1 {
		t.Fatalf("expected one disable prompt, got %d", promptCount)
	}
	if configAutoVerify(agent.Config) {
		t.Fatalf("expected auto_verify to be disabled after prompt")
	}
}

func TestAgentNudgesForFinalAnswerAfterMultipleSuccessfulEditTurns(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {}\n"}),
			{Message: Message{Role: "assistant", Text: "Implemented the requested change."}},
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

	reply, err := agent.Reply(context.Background(), "make the change")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Implemented the requested change." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected 3 model turns, got %d", len(provider.requests))
	}
	lastTurn := provider.requests[2]
	if len(lastTurn.Messages) == 0 {
		t.Fatalf("expected follow-up nudge before final answer")
	}
	lastMessage := lastTurn.Messages[len(lastTurn.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "multiple edit rounds") {
		t.Fatalf("expected final-answer nudge after repeated edits, got %#v", lastMessage)
	}
}

func TestAgentBlocksFurtherEditToolLoopAfterPostEditNudge(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {}\n"}),
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {}\n// extra\n"}),
			{Message: Message{Role: "assistant", Text: "Implemented the requested change."}},
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

	reply, err := agent.Reply(context.Background(), "make the change")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Implemented the requested change." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected 4 model turns, got %d", len(provider.requests))
	}
	thirdTurn := provider.requests[3]
	if len(thirdTurn.Messages) == 0 {
		t.Fatalf("expected stronger stop-editing nudge before final answer")
	}
	lastMessage := thirdTurn.Messages[len(thirdTurn.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Do not call more edit tools") {
		t.Fatalf("expected stop-editing nudge after repeated edit-tool attempt, got %#v", lastMessage)
	}
}

func TestAgentSuppressesDuplicateToolPreambleEmitsWithinATurn(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "Checking the workspace.",
					ToolCalls: []ToolCall{{
						ID:        "call-1",
						Name:      "list_files",
						Arguments: `{}`,
					}},
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Checking the workspace.",
					ToolCalls: []ToolCall{{
						ID:        "call-2",
						Name:      "list_files",
						Arguments: `{}`,
					}},
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Done.",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var emitted []string
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		EmitAssistant: func(text string) {
			emitted = append(emitted, text)
		},
	}

	reply, err := agent.Reply(context.Background(), "inspect the workspace")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Done." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if !reflect.DeepEqual(emitted, []string{"Checking the workspace."}) {
		t.Fatalf("expected duplicate preamble emit to be suppressed, got %#v", emitted)
	}
}

func TestAgentSuppressesFinalLookingToolPreambleEmit(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "작업 완료\n\n수정이 완료되었습니다. 더 이상 변경은 필요 없습니다.",
					ToolCalls: []ToolCall{{
						ID:        "call-1",
						Name:      "list_files",
						Arguments: `{}`,
					}},
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "list_files 결과를 확인했고 아직 최종 완료 전입니다.",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var emitted []string
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		EmitAssistant: func(text string) {
			emitted = append(emitted, text)
		},
	}

	reply, err := agent.Reply(context.Background(), "inspect the workspace")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "최종 완료 전") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	for _, text := range emitted {
		if strings.Contains(text, "수정이 완료되었습니다") || strings.Contains(text, "더 이상 변경") {
			t.Fatalf("final-looking tool preamble leaked to user-visible output: %#v", emitted)
		}
	}
	for _, msg := range session.Messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 && strings.Contains(msg.Text, "수정이 완료되었습니다") {
			t.Fatalf("final-looking tool preamble remained in transcript: %#v", msg)
		}
	}
}

func TestAgentPromotesFinalLookingToolPreambleAfterCodeEdit(t *testing.T) {
	root := t.TempDir()
	readTool := &staticTool{name: "read_file", output: "read should not run"}
	readArgs, _ := json.Marshal(map[string]any{"path": "main.txt"})
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "main.txt",
				"content": "fixed\n",
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Final Answer\n\nThe implementation is complete and ready for review.",
					ToolCalls: []ToolCall{{
						ID:        "call-read-after-final-looking-code-edit",
						Name:      "read_file",
						Arguments: string(readArgs),
					}},
				},
				StopReason: "tool_calls",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "This follow-up should not be requested.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), readTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			return VerificationReport{
				GeneratedAt:  time.Now(),
				ChangedPaths: []string{"main.txt"},
				Steps: []VerificationStep{{
					Label:  "targeted",
					Status: VerificationPassed,
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "fix main.txt")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Final Answer\n\nThe implementation is complete and ready for review." {
		t.Fatalf("expected final-looking preamble to become final answer, got %q", reply)
	}
	if readTool.calls != 0 {
		t.Fatalf("read_file should not execute after a final-looking code-edit summary, got %d calls", readTool.calls)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected runtime to finish without post-final inspection churn, got %d request(s)", len(provider.requests))
	}
	last := session.Messages[len(session.Messages)-1]
	if last.Phase != messagePhaseFinalAnswer || len(last.ToolCalls) != 0 {
		t.Fatalf("expected final-looking tool preamble to be stored as final answer without tool calls, got %#v", last)
	}
}

func TestAgentPromotesFinalLookingToolPreambleForReadOnlyAnalysis(t *testing.T) {
	root := t.TempDir()
	readTool := &staticTool{name: "read_file", output: "read should not run"}
	readArgs, _ := json.Marshal(map[string]any{"path": "main.go"})
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "Final Answer\n\nThe analysis is complete and ready for review.",
					ToolCalls: []ToolCall{{
						ID:        "call-read-after-final-looking-analysis",
						Name:      "read_file",
						Arguments: string(readArgs),
					}},
				},
				StopReason: "tool_calls",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "This follow-up should not be requested.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{Model: "model"},
		Client:    provider,
		Tools:     NewToolRegistry(readTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "현재 코드 구조를 분석해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Final Answer\n\nThe analysis is complete and ready for review." {
		t.Fatalf("expected final-looking analysis preamble to become final answer, got %q", reply)
	}
	if readTool.calls != 0 {
		t.Fatalf("read_file should not execute after a final-looking analysis summary, got %d calls", readTool.calls)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected final-looking analysis preamble to stop the turn, got %d requests", len(provider.requests))
	}
	last := session.Messages[len(session.Messages)-1]
	if last.Role != "assistant" || last.Phase != messagePhaseFinalAnswer || last.Text != reply || len(last.ToolCalls) != 0 {
		t.Fatalf("expected final-looking analysis preamble to be stored as final answer without tool calls, got %#v", last)
	}
}

func TestAgentDoesNotPromoteFinalLookingVerificationToolBeforeVerification(t *testing.T) {
	root := t.TempDir()
	shellTool := &staticTool{name: "run_shell", output: "tests passed"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "main.txt",
				"content": "fixed\n",
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Final Answer\n\nThe implementation is complete and ready for review.",
					ToolCalls: []ToolCall{{
						ID:        "call-test-after-edit",
						Name:      "run_shell",
						Arguments: `{"command":"go test ./cmd/kernforge"}`,
					}},
				},
				StopReason: "tool_calls",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Verification ran and the change is complete.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), shellTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "fix main.txt")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Verification ran and the change is complete." {
		t.Fatalf("expected verification follow-up final answer, got %q", reply)
	}
	if shellTool.calls != 1 {
		t.Fatalf("verification shell should execute when no current verification exists, got %d calls", shellTool.calls)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected verification tool turn before final answer, got %d request(s)", len(provider.requests))
	}
}

func TestAgentNudgesAfterRepeatedIdenticalToolCalls(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{}),
			toolCallResponse("list_files", map[string]any{}),
			toolCallResponse("list_files", map[string]any{}),
			{Message: Message{Role: "assistant", Text: "I kept seeing the same workspace state, so I am stopping here."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect the workspace")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "I kept seeing the same workspace state, so I am stopping here." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected 4 model turns, got %d", len(provider.requests))
	}
	lastTurn := provider.requests[3]
	if len(lastTurn.Messages) == 0 {
		t.Fatalf("expected repeated-tool warning before final answer")
	}
	lastMessage := lastTurn.Messages[len(lastTurn.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "repeating the same tool call sequence") {
		t.Fatalf("expected repeated-tool nudge, got %#v", lastMessage)
	}
}

func TestAgentStopsAfterRepeatedIdenticalToolCallsContinueAfterRecoveryTurn(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{}),
			toolCallResponse("list_files", map[string]any{}),
			toolCallResponse("list_files", map[string]any{}),
			toolCallResponse("list_files", map[string]any{}),
			toolCallResponse("list_files", map[string]any{}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "inspect the workspace")
	if err == nil {
		t.Fatalf("expected repeated identical tool calls to stop the loop")
	}
	if !strings.Contains(err.Error(), "repeated identical tool calls") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(provider.requests) != 5 {
		t.Fatalf("expected abort on fifth repeated tool-call turn, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[4]
	if len(lastRequest.Messages) == 0 {
		t.Fatalf("expected recovery guidance before aborting repeated tool calls")
	}
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Recovery mode") {
		t.Fatalf("expected recovery guidance before abort, got %#v", lastMessage)
	}
}

func TestShouldTrackRepeatedToolCallSignatureIgnoresReadFileOnlyTurns(t *testing.T) {
	calls := []ToolCall{
		{
			Name:      "read_file",
			Arguments: `{"path":"IMLauncherMainWindow.cpp","start_line":2170,"end_line":2200}`,
		},
	}

	if shouldTrackRepeatedToolCallSignature(calls) {
		t.Fatalf("expected read_file-only turns to use dedicated repeated-read handling")
	}
}

func TestShouldTrackRepeatedToolCallSignatureKeepsMixedToolTurns(t *testing.T) {
	calls := []ToolCall{
		{
			Name:      "read_file",
			Arguments: `{"path":"IMLauncherMainWindow.cpp","start_line":2170,"end_line":2200}`,
		},
		{
			Name:      "grep",
			Arguments: `{"pattern":"LoadBackgroundImage"}`,
		},
	}

	if !shouldTrackRepeatedToolCallSignature(calls) {
		t.Fatalf("expected mixed tool turns to keep repeated identical signature protection")
	}
}

func TestSanitizeAssistantMessageTextRemovesToolPreambleNarration(t *testing.T) {
	text := "Let me read AnthropicProvider and GeminiProvider:Now I have all the files. Let me apply all the changes."

	got := sanitizeAssistantMessageText(text, true)
	if got != "" {
		t.Fatalf("expected pure tool preamble narration to be dropped, got %q", got)
	}
}

func TestSanitizeAssistantMessageTextKeepsSubstantiveToolPlan(t *testing.T) {
	text := "Let me inspect the providers.\nThe approach:\n1. Update the interface\n2. Pass reasoning effort through all providers"

	got := sanitizeAssistantMessageText(text, true)
	if !strings.Contains(got, "The approach:") {
		t.Fatalf("expected substantive content to be kept, got %q", got)
	}
	if strings.Contains(strings.ToLower(got), "let me inspect") {
		t.Fatalf("expected narration preamble to be removed, got %q", got)
	}
}

func TestSanitizeAssistantMessageTextRemovesToolDeadlockNarration(t *testing.T) {
	text := "There's a tool execution deadlock: the system requires web research before local inspection. Let me try reading files directly."

	got := sanitizeAssistantMessageText(text, true)
	if got != "" {
		t.Fatalf("expected tool deadlock narration to be dropped, got %q", got)
	}
}

func TestSanitizeAssistantMessageTextRemovesKoreanToolPreambleNarration(t *testing.T) {
	text := "이제 템플릿 파일을 확인하겠습니다.\n먼저 main.py를 수정하겠습니다."

	got := sanitizeAssistantMessageText(text, true)
	if got != "" {
		t.Fatalf("expected korean tool preamble narration to be dropped, got %q", got)
	}
}

func TestSanitizeAssistantMessageTextKeepsSubstantiveKoreanToolPlan(t *testing.T) {
	text := "먼저 providers를 확인하겠습니다.\n구현 계획:\n1. 인터페이스 갱신\n2. reasoning effort 전달"

	got := sanitizeAssistantMessageText(text, true)
	if !strings.Contains(got, "구현 계획:") {
		t.Fatalf("expected substantive korean content to be kept, got %q", got)
	}
	if strings.Contains(got, "먼저 providers를 확인하겠습니다.") {
		t.Fatalf("expected korean narration preamble to be removed, got %q", got)
	}
}

func TestSanitizeAssistantMessageTextRemovesFinalLookingToolSummary(t *testing.T) {
	cases := []string{
		"작업 완료\n\nTavern/BugReport.md 문서가 완성되었습니다. 총 27개 버그를 기록했고 더 이상 변경은 필요 없습니다.",
		"Final Answer\n\nThe bug report has been completed and saved to Tavern/BugReport.md.",
	}

	for _, text := range cases {
		got := sanitizeAssistantMessageText(text, true)
		if got != "" {
			t.Fatalf("expected final-looking tool preamble to be dropped, got %q", got)
		}
	}
}

func TestSanitizeAssistantMessageTextKeepsToolCallRootCauseNote(t *testing.T) {
	text := "The likely root cause is stale review-gate state. I will inspect the runtime ledger next."

	got := sanitizeAssistantMessageText(text, true)
	if !strings.Contains(got, "root cause") {
		t.Fatalf("expected substantive non-final tool note to remain, got %q", got)
	}
}

func TestSanitizeAssistantMessageTextRemovesHiddenAssistantMarkup(t *testing.T) {
	text := strings.Join([]string{
		"완료했습니다.",
		"<oai-mem-citation><citation_entries>MEMORY.md:1-2|note=[x]</citation_entries></oai-mem-citation>",
		"<proposed_plan>",
		"- hidden step",
		"</proposed_plan>",
	}, "\n")

	got := sanitizeAssistantMessageText(text, false)
	if got != "완료했습니다." {
		t.Fatalf("expected hidden assistant markup to be stripped, got %q", got)
	}
}

func TestSanitizeAssistantMessageTextDropsHiddenOnlyMarkup(t *testing.T) {
	text := "<oai-mem-citation>hidden only</oai-mem-citation>\n<proposed_plan>\n- hidden\n</proposed_plan>"

	got := sanitizeAssistantMessageText(text, false)
	if got != "" {
		t.Fatalf("expected hidden-only assistant markup to be dropped, got %q", got)
	}
}

func TestSanitizeAssistantFinalTextRemovesHiddenAssistantMarkup(t *testing.T) {
	text := strings.Join([]string{
		"완료했습니다.",
		"<oai-mem-citation><citation_entries>MEMORY.md:1-2|note=[x]</citation_entries></oai-mem-citation>",
		"<proposed_plan>",
		"- hidden step",
		"</proposed_plan>",
	}, "\n")

	got := sanitizeAssistantFinalText(text)
	if got != "완료했습니다." {
		t.Fatalf("expected hidden assistant markup to be stripped from final text, got %q", got)
	}
}

func TestSanitizeAssistantFinalTextDropsHiddenOnlyMarkup(t *testing.T) {
	text := "<oai-mem-citation>hidden only</oai-mem-citation>\n<proposed_plan>\n- hidden\n</proposed_plan>"

	got := sanitizeAssistantFinalText(text)
	if got != "" {
		t.Fatalf("expected hidden-only final text to be dropped, got %q", got)
	}
}

func TestAgentContinuesSameTurnWhenProviderEndTurnFalse(t *testing.T) {
	root := t.TempDir()
	endTurnFalse := false
	endTurnTrue := true
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message:    Message{Role: "assistant", Text: "Still checking the generated artifact."},
				StopReason: "completed",
				EndTurn:    &endTurnFalse,
			},
			{
				Message:    Message{Role: "assistant", Text: "Final answer is ready."},
				StopReason: "completed",
				EndTurn:    &endTurnTrue,
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config:    Config{Model: "model"},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "do the work")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Final answer is ready." {
		t.Fatalf("expected second response to become final reply, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected end_turn=false to request one follow-up model turn, got %d", len(provider.requests))
	}
	if len(session.Messages) < 3 {
		t.Fatalf("expected user plus two assistant messages, got %#v", session.Messages)
	}
	if session.Messages[1].Role != "assistant" || session.Messages[1].Phase != messagePhaseCommentary {
		t.Fatalf("expected first end_turn=false assistant message to be stored as commentary, got %#v", session.Messages[1])
	}
	if strings.Contains(provider.requests[1].Messages[len(provider.requests[1].Messages)-1].Text, "Your last assistant message was commentary") {
		t.Fatalf("end_turn=false follow-up should continue without synthetic commentary warning")
	}
	if session.Messages[len(session.Messages)-1].Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected second assistant message to be accepted as final, got %#v", session.Messages[len(session.Messages)-1])
	}
}

func TestAgentEmitsCodexServerModelAndVerificationMetadata(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message:            Message{Role: "assistant", Text: "The request is complete."},
			ServerModel:        "gpt-5.2",
			ModelVerifications: []string{openAICodexTrustedAccessForCyber},
		}},
	}
	session := NewSession(root, "openai-codex", "gpt-5.3-codex", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	events := []ProgressEvent{}
	agent := &Agent{
		Config: Config{
			Model: "gpt-5.3-codex",
		},
		Client: provider,
		Tools:  NewToolRegistry(),
		EmitProgressEvent: func(event ProgressEvent) {
			events = append(events, event)
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "The request is complete." {
		t.Fatalf("expected reply, got %q", reply)
	}
	if !progressEventsContainKind(events, progressKindModelReroute) {
		t.Fatalf("expected model reroute progress event, got %#v", events)
	}
	if !progressEventsContainKind(events, progressKindModelVerification) {
		t.Fatalf("expected model verification progress event, got %#v", events)
	}
}

func progressEventsContainKind(events []ProgressEvent, kind string) bool {
	for _, event := range events {
		if strings.TrimSpace(event.Kind) == kind {
			return true
		}
	}
	return false
}

func TestAgentFinalizesFinalLookingReplyWhenProviderEndTurnFalse(t *testing.T) {
	root := t.TempDir()
	endTurnFalse := false
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message:    Message{Role: "assistant", Text: "Final Answer\n\nThe implementation is complete and ready for review."},
				StopReason: "completed",
				EndTurn:    &endTurnFalse,
			},
			{
				Message:    Message{Role: "assistant", Text: "This follow-up should not be requested."},
				StopReason: "completed",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config:    Config{Model: "model"},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "finish the work")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Final Answer\n\nThe implementation is complete and ready for review." {
		t.Fatalf("expected first final-looking response to become final reply, got %q", reply)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected final-looking end_turn=false response to stop the turn, got %d requests", len(provider.requests))
	}
	if session.Messages[len(session.Messages)-1].Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected final-looking response to be accepted as final, got %#v", session.Messages[len(session.Messages)-1])
	}
}

func TestAgentFinalizesSavedReportSummaryWhenProviderEndTurnFalse(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "Tavern", "BugReport.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	reportContent := strings.Join([]string{
		"# Tavern BugReport Report Status",
		"",
		"This Tavern BugReport report status document records the current saved report state.",
		"It includes detailed bug findings, impact analysis, and suggested fixes for each bug.",
		"BUG-001 documents a representative correctness issue with impact analysis and a fix recommendation.",
		"BUG-002 documents a representative stability issue with impact analysis and a fix recommendation.",
		"BUG-003 documents a representative resource issue with impact analysis and a fix recommendation.",
		strings.Repeat("The report status remains substantive and ready for final review. ", 20),
	}, "\n")
	if err := os.WriteFile(reportPath, []byte(reportContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	endTurnFalse := false
	finalReply := "The full report with detailed descriptions, impact analysis, and suggested fixes for each bug is saved in `Tavern/BugReport.md`."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message:    Message{Role: "assistant", Text: finalReply},
				StopReason: "completed",
				EndTurn:    &endTurnFalse,
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config:    Config{Model: "model"},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "Tavern/BugReport.md report status")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != finalReply {
		t.Fatalf("expected saved report summary to become final reply, got %q", reply)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected saved report summary with end_turn=false to stop the turn, got %d requests", len(provider.requests))
	}
	if session.Messages[len(session.Messages)-1].Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected saved report summary to be accepted as final, got %#v", session.Messages[len(session.Messages)-1])
	}
}

func TestAssistantTextLooksLikeCompletionSummaryForSavedReport(t *testing.T) {
	text := "The full report with detailed descriptions, impact analysis, and suggested fixes for each bug is saved in `Tavern/BugReport.md`."
	if !assistantTextLooksLikeCompletionSummary(text) {
		t.Fatalf("expected saved report wording to be treated as a completion summary")
	}
}

func TestAssistantTextLooksLikeCompletionSummaryDoesNotTreatReportMentionAsDone(t *testing.T) {
	text := "I still need to inspect the full report before I can provide the final result."
	if assistantTextLooksLikeCompletionSummary(text) {
		t.Fatalf("expected an in-progress report mention to remain a follow-up response")
	}
}

func TestShouldDeferEndTurnFollowUpForFinalLookingReplyKeepsInProgressText(t *testing.T) {
	endTurnFalse := false
	resp := ChatResponse{
		Message: Message{
			Role: "assistant",
			Text: "Still checking the edit before the final answer.",
		},
		EndTurn: &endTurnFalse,
	}
	if shouldDeferEndTurnFollowUpForFinalLookingReply(resp) {
		t.Fatalf("expected in-progress final-answer wording to request a follow-up")
	}
}

func TestShouldDeferEndTurnFollowUpForFinalLookingReplyAcceptsFinalCandidatePhase(t *testing.T) {
	endTurnFalse := false
	resp := ChatResponse{
		Message: Message{
			Role:  "assistant",
			Phase: messagePhaseFinalAnswerCandidate,
			Text:  "Done.",
		},
		EndTurn: &endTurnFalse,
	}
	if !shouldDeferEndTurnFollowUpForFinalLookingReply(resp) {
		t.Fatalf("expected final-answer candidate phase to defer follow-up even for short text")
	}
}

func TestShouldDeferEndTurnFollowUpForFinalLookingReplyDoesNotAcceptInProgressFinalPhase(t *testing.T) {
	endTurnFalse := false
	resp := ChatResponse{
		Message: Message{
			Role:  "assistant",
			Phase: messagePhaseFinalAnswerCandidate,
			Text:  "Checking the report now before the final answer.",
		},
		EndTurn: &endTurnFalse,
	}
	if shouldDeferEndTurnFollowUpForFinalLookingReply(resp) {
		t.Fatalf("expected in-progress text to request a follow-up even with final-answer candidate phase")
	}
}

func TestShouldDeferEndTurnFollowUpForFinalLookingReplyKeepsFutureVerification(t *testing.T) {
	endTurnFalse := false
	resp := ChatResponse{
		Message: Message{
			Role: "assistant",
			Text: "The file is saved to `main.go`; I need to verify it next.",
		},
		EndTurn: &endTurnFalse,
	}
	if shouldDeferEndTurnFollowUpForFinalLookingReply(resp) {
		t.Fatalf("expected future verification wording to request a follow-up")
	}
}

func TestAgentReusesProviderTurnStateOnlyWithinExternalTurn(t *testing.T) {
	root := t.TempDir()
	provider := &turnStateObservingProviderClient{
		replies: []ChatResponse{
			toolCallResponse("grep", map[string]any{"pattern": "BUG"}),
			{
				Message:    Message{Role: "assistant", Text: "첫 번째 턴 완료"},
				StopReason: "stop",
			},
			{
				Message:    Message{Role: "assistant", Text: "두 번째 턴 완료"},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(&staticTool{name: "grep", output: "found"}),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	if reply, err := agent.Reply(context.Background(), "첫 번째 작업"); err != nil || reply != "첫 번째 턴 완료" {
		t.Fatalf("first Reply = %q, %v", reply, err)
	}
	if len(provider.states) != 2 {
		t.Fatalf("expected first turn to make two model requests, got %d", len(provider.states))
	}
	if provider.requests[0].SessionID != session.ID || provider.requests[0].ThreadID != session.ID {
		t.Fatalf("expected agent to attach stable provider session metadata, got session=%q thread=%q want=%q", provider.requests[0].SessionID, provider.requests[0].ThreadID, session.ID)
	}
	if got := provider.requests[0].TurnMetadata["session_id"]; got != session.ID {
		t.Fatalf("expected first request to carry provider turn metadata session_id %q, got %#v", session.ID, provider.requests[0].TurnMetadata)
	}
	firstTurnID, ok := provider.requests[0].TurnMetadata["turn_id"].(string)
	if !ok || strings.TrimSpace(firstTurnID) == "" {
		t.Fatalf("expected first request to carry turn_id, got %#v", provider.requests[0].TurnMetadata)
	}
	firstTraceID, ok := provider.requests[0].TurnMetadata["trace_id"].(string)
	if !ok || strings.TrimSpace(firstTraceID) == "" {
		t.Fatalf("expected first request to carry trace_id, got %#v", provider.requests[0].TurnMetadata)
	}
	if provider.states[0] == nil || provider.states[1] == nil || provider.states[0] != provider.states[1] {
		t.Fatalf("expected first turn requests to share provider turn state, got %#v", provider.states[:2])
	}
	if secondTurnID, _ := provider.requests[1].TurnMetadata["turn_id"].(string); secondTurnID != firstTurnID {
		t.Fatalf("expected same external turn to reuse turn metadata, got %q then %q", firstTurnID, secondTurnID)
	}
	if secondTraceID, _ := provider.requests[1].TurnMetadata["trace_id"].(string); secondTraceID != firstTraceID {
		t.Fatalf("expected same external turn to reuse trace metadata, got %q then %q", firstTraceID, secondTraceID)
	}
	if provider.values[0] != "" || provider.values[1] != "sticky-turn" {
		t.Fatalf("expected sticky state to be replayed within turn, got %#v", provider.values[:2])
	}

	if reply, err := agent.Reply(context.Background(), "두 번째 작업"); err != nil || reply != "두 번째 턴 완료" {
		t.Fatalf("second Reply = %q, %v", reply, err)
	}
	if len(provider.states) != 3 {
		t.Fatalf("expected second turn to make one model request, got %d", len(provider.states))
	}
	if provider.states[2] == nil || provider.states[2] == provider.states[0] {
		t.Fatalf("expected second external turn to get fresh provider turn state")
	}
	if provider.values[2] != "" {
		t.Fatalf("expected sticky state not to leak into next turn, got %q", provider.values[2])
	}
	if thirdTurnID, _ := provider.requests[2].TurnMetadata["turn_id"].(string); thirdTurnID == "" || thirdTurnID == firstTurnID {
		t.Fatalf("expected second external turn to get fresh turn metadata, first=%q second=%q", firstTurnID, thirdTurnID)
	}
	if thirdTraceID, _ := provider.requests[2].TurnMetadata["trace_id"].(string); thirdTraceID == "" || thirdTraceID == firstTraceID {
		t.Fatalf("expected second external turn to get fresh trace metadata, first=%q second=%q", firstTraceID, thirdTraceID)
	}
}

func TestImmediateFinalReplyPathsSanitizeHiddenMarkup(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config:  Config{},
		Session: session,
		Store:   store,
	}
	reply, err := agent.finishPendingReviewRepairConfirmation(
		"n",
		nil,
		true,
		"<oai-mem-citation>hidden only</oai-mem-citation>\nVisible final answer.",
	)
	if err != nil {
		t.Fatalf("finishPendingReviewRepairConfirmation: %v", err)
	}
	if reply != "Visible final answer." {
		t.Fatalf("expected sanitized immediate final reply, got %q", reply)
	}
	if len(session.Messages) != 2 || session.Messages[1].Text != "Visible final answer." {
		t.Fatalf("expected session to store sanitized final reply, got %#v", session.Messages)
	}
}

func TestReplyLooksAbruptlyTruncatedDetectsCutoffTail(t *testing.T) {
	if !replyLooksAbruptlyTruncated("현재 코드는 `items` 하위 키가 있는 경로만 처리하고 있어, `items` 하위 키가 없는 구조에서는 MRU 항목을 가져오지 못합니다.\n\n이") {
		t.Fatalf("expected abrupt cutoff to be detected")
	}
	if !replyLooksAbruptlyTruncated("`HasMRUItemsSubKey` 함수에서 이 문제를 수정하겠습니다. 다른 함수들(`ParsePrivateRegistryFile`, `Parse") {
		t.Fatalf("expected unbalanced code-span cutoff to be detected")
	}
	if replyLooksAbruptlyTruncated("현재 코드는 `items` 하위 키가 없는 구조도 처리해야 합니다.") {
		t.Fatalf("did not expect complete sentence to be treated as truncated")
	}
}

func TestMergeAssistantContinuationHandlesKoreanAndEnglishBoundaries(t *testing.T) {
	if got := mergeAssistantContinuation("이", "로 인해 문제가 발생합니다."); got != "이로 인해 문제가 발생합니다." {
		t.Fatalf("unexpected korean continuation merge: %q", got)
	}
	if got := mergeAssistantContinuation("This affects", "some registry layouts."); got != "This affects some registry layouts." {
		t.Fatalf("unexpected english continuation merge: %q", got)
	}
	if got := mergeAssistantContinuation("Parse", "ItemsInInstance"); got != "ParseItemsInInstance" {
		t.Fatalf("unexpected camel-case continuation merge: %q", got)
	}
}

func TestAgentRetriesAbruptlyTruncatedFinalReply(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "현재 코드는 `items` 하위 키가 있는 경로만 처리하고 있어, `items` 하위 키가 없는 구조에서는 MRU 항목을 가져오지 못합니다.\n\n이",
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "로 인해 일부 Visual Studio 버전에서 최근 파일 목록이 누락됩니다.",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "review this file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	expected := "현재 코드는 `items` 하위 키가 있는 경로만 처리하고 있어, `items` 하위 키가 없는 구조에서는 MRU 항목을 가져오지 못합니다.\n\n이로 인해 일부 Visual Studio 버전에서 최근 파일 목록이 누락됩니다."
	if reply != expected {
		t.Fatalf("unexpected merged reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected continuation retry, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "cut off mid-sentence") {
		t.Fatalf("expected continuation guidance, got %#v", lastMessage)
	}
	if len(session.Messages) < 2 || session.Messages[1].Text != expected {
		t.Fatalf("expected merged answer to replace original assistant turn, got %#v", session.Messages)
	}
	if len(session.Messages) != 2 {
		t.Fatalf("expected continuation helper turns to be trimmed from history, got %#v", session.Messages)
	}
}

func TestAgentRetriesAbruptlyTruncatedFinalReplyWhileStreaming(t *testing.T) {
	root := t.TempDir()
	provider := &streamingScriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "현재 코드는 `items` 하위 키가 있는 경로만 처리하고 있어, `items` 하위 키가 없는 구조에서는 MRU 항목을 가져오지 못합니다.\n\n이",
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "로 인해 일부 Visual Studio 버전에서 최근 파일 목록이 누락됩니다.",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var emitted strings.Builder
	agent := &Agent{
		Config:             Config{},
		Client:             provider,
		Tools:              NewToolRegistry(NewReadFileTool(ws)),
		Workspace:          ws,
		Session:            session,
		Store:              store,
		EmitAssistantDelta: func(text string) { emitted.WriteString(text) },
	}

	reply, err := agent.Reply(context.Background(), "review this file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	expected := "현재 코드는 `items` 하위 키가 있는 경로만 처리하고 있어, `items` 하위 키가 없는 구조에서는 MRU 항목을 가져오지 못합니다.\n\n이로 인해 일부 Visual Studio 버전에서 최근 파일 목록이 누락됩니다."
	if reply != expected {
		t.Fatalf("unexpected merged reply: %q", reply)
	}
	if emitted.String() != expected {
		t.Fatalf("expected streamed text to continue seamlessly, got %q", emitted.String())
	}
}

func TestAgentRetriesCodeSpanTruncationWhileStreaming(t *testing.T) {
	root := t.TempDir()
	provider := &streamingScriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "`HasMRUItemsSubKey` 함수에서 이 문제를 수정하겠습니다. 다른 함수들(`ParsePrivateRegistryFile`, `Parse",
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "ItemsInInstance`)도 함께 점검해 안전하게 마무리하겠습니다.",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var emitted strings.Builder
	agent := &Agent{
		Config:             Config{},
		Client:             provider,
		Tools:              NewToolRegistry(NewReadFileTool(ws)),
		Workspace:          ws,
		Session:            session,
		Store:              store,
		EmitAssistantDelta: func(text string) { emitted.WriteString(text) },
	}

	reply, err := agent.Reply(context.Background(), "review and fix this file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	expected := "`HasMRUItemsSubKey` 함수에서 이 문제를 수정하겠습니다. 다른 함수들(`ParsePrivateRegistryFile`, `ParseItemsInInstance`)도 함께 점검해 안전하게 마무리하겠습니다."
	if reply != expected {
		t.Fatalf("unexpected merged reply: %q", reply)
	}
	if emitted.String() != expected {
		t.Fatalf("expected streamed text to continue seamlessly, got %q", emitted.String())
	}
}

func TestAgentStoresSanitizedToolPreambleInsteadOfNarration(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "Let me read AnthropicProvider and GeminiProvider:Now I have all the files. Let me apply all the changes.",
					ToolCalls: []ToolCall{{
						ID:        "call-1",
						Name:      "list_files",
						Arguments: `{}`,
					}},
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Done.",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect the workspace")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Done." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(session.Messages) < 3 {
		t.Fatalf("expected stored session messages, got %#v", session.Messages)
	}
	assistantTurn := session.Messages[1]
	if assistantTurn.Role != "assistant" {
		t.Fatalf("expected assistant tool turn, got %#v", assistantTurn)
	}
	if strings.TrimSpace(assistantTurn.Text) != "" {
		t.Fatalf("expected tool-turn narration to be stripped from stored message, got %q", assistantTurn.Text)
	}
}

func TestAgentRetriesEditToolWithoutPreFixReviewSummary(t *testing.T) {
	root := t.TempDir()
	patchTool := &staticTool{name: "apply_patch", output: "ok"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "이제 전체 파일을 확인했으니 세 가지 리뷰 발견사항을 모두 수정하겠습니다.",
					ToolCalls: []ToolCall{{
						ID:        "call-1",
						Name:      "apply_patch",
						Arguments: `{"patch":"*** Begin Patch\n*** Update File: Source/Sample.cpp\n@@\n-old\n+new\n*** End Patch"}`,
					}},
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "검토 결과:\n- RF-001: 단일 처리 실패가 전체 열거를 중단합니다. 현재 항목만 건너뛰도록 수정합니다.\n- RF-002: 고정 버퍼로 결과를 놓칠 수 있습니다. 필요한 크기로 재시도합니다.",
					ToolCalls: []ToolCall{{
						ID:        "call-2",
						Name:      "apply_patch",
						Arguments: `{"patch":"*** Begin Patch\n*** Update File: Source/Sample.cpp\n@@\n-old\n+new\n*** End Patch"}`,
					}},
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "수정했습니다.",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "@Source/Sample.cpp:10-40 검토하고 버그를 수정해"})
	session.LastReviewRun = &ReviewRun{
		Trigger:   reviewBeforeFixTrigger,
		Objective: "@Source/Sample.cpp:10-40 검토하고 버그를 수정해",
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{"RF-001", "RF-002"},
		},
		Findings: []ReviewFinding{
			{
				ID:          "RF-001",
				Source:      "model",
				Severity:    reviewSeverityMedium,
				Category:    "correctness",
				Title:       "단일 처리 실패가 전체 열거를 중단합니다",
				Path:        "Source/Sample.cpp",
				Evidence:    "A recoverable item failure exits the surrounding enumeration loop.",
				Impact:      "Later valid items are skipped.",
				RequiredFix: "현재 항목만 건너뛰고 다음 항목을 계속 처리하세요.",
			},
			{
				ID:          "RF-002",
				Source:      "model",
				Severity:    reviewSeverityMedium,
				Category:    "correctness",
				Title:       "고정 버퍼가 정상 결과를 누락할 수 있습니다",
				Path:        "Source/Sample.cpp",
				Evidence:    "The result is read once into a fixed-size buffer.",
				Impact:      "Long results can be dropped.",
				RequiredFix: "필요한 버퍼 크기를 확인한 뒤 재시도하세요.",
			},
		},
	}
	firstMessage := provider.replies[0].Message
	firstMessage.Text = sanitizeAssistantMessageText(firstMessage.Text, len(firstMessage.ToolCalls) > 0)
	if !shouldRetryPreFixEditWithoutVisibleReviewSummary(firstMessage, session) {
		t.Fatalf("expected missing visible RF summary to block the first edit call")
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.completeLoop(context.Background(), false, true, false)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "수정했습니다." && reply != "done" {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if patchTool.calls != 1 {
		t.Fatalf("expected only the summarized edit attempt to execute, got %d calls", patchTool.calls)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected retry request after missing review summary")
	}
	lastBeforeRetry := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastBeforeRetry.Role != "user" || !strings.Contains(lastBeforeRetry.Text, "검토 결과") || !strings.Contains(lastBeforeRetry.Text, "RF-001") {
		t.Fatalf("expected Korean review-summary retry guidance with RF IDs, got %#v", lastBeforeRetry)
	}
	for _, msg := range session.Messages {
		if msg.Role == "assistant" && strings.Contains(msg.Text, "세 가지 리뷰 발견사항") {
			t.Fatalf("unsummarized edit preamble should not be stored: %#v", msg)
		}
	}
}

func TestPreFixVisibleReviewSummaryRequiresStructuredFindingID(t *testing.T) {
	run := ReviewRun{
		Trigger:   reviewBeforeFixTrigger,
		Objective: "샘플 코드를 검토하고 버그를 수정해",
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{"RF-001", "RF-002"},
		},
		Findings: []ReviewFinding{
			{
				ID:          "RF-001",
				Source:      "model",
				Severity:    reviewSeverityMedium,
				Category:    "correctness",
				Title:       "경계 조건 누락",
				RequiredFix: "경계 조건을 검사하세요.",
			},
			{
				ID:          "RF-002",
				Source:      "model",
				Severity:    reviewSeverityMedium,
				Category:    "stability",
				Title:       "재시도 누락",
				RequiredFix: "재시도 경로를 검사하세요.",
			},
		},
	}

	if assistantTextIncludesPreFixReviewSummary("검토 결과를 바탕으로 수정하겠습니다.", run) {
		t.Fatalf("generic review wording should not satisfy visible RF summary")
	}
	if assistantTextIncludesPreFixReviewSummary("검토 결과:\n- RF-001: 경계 조건을 보강합니다.", run) {
		t.Fatalf("partial RF list should not satisfy visible review summary")
	}
	if !assistantTextIncludesPreFixReviewSummary("검토 결과:\n- RF-001: 경계 조건을 보강합니다.\n- RF-002: 재시도 경로를 보강합니다.", run) {
		t.Fatalf("expected explicit RF item to satisfy visible review summary")
	}
}

func TestPreFixVisibleReviewSummaryRequiresConcreteTextForIDLessFinding(t *testing.T) {
	run := ReviewRun{
		Trigger:   reviewBeforeFixTrigger,
		Objective: "샘플 코드를 검토하고 버그를 수정해",
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{""},
		},
		Findings: []ReviewFinding{{
			Source:      "model",
			Severity:    reviewSeverityMedium,
			Category:    "correctness",
			Title:       "Missing bounds check",
			RequiredFix: "Validate the buffer length before indexing.",
		}},
	}

	if assistantTextIncludesPreFixReviewSummary("검토 결과:\n- 경고 1개가 있습니다.", run) {
		t.Fatalf("generic review wording should not satisfy an id-less finding")
	}
	if !assistantTextIncludesPreFixReviewSummary("검토 결과:\n- Missing bounds check: Validate the buffer length before indexing.", run) {
		t.Fatalf("expected concrete title/fix text to satisfy an id-less finding")
	}
}

func TestPreFixVisibleReviewSummaryIgnoresStalePreviousTurnSummary(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "main-model", "", "default")
	run := ReviewRun{
		Trigger:   reviewBeforeFixTrigger,
		Objective: "@Source/Sample.cpp:10-40 검토하고 버그를 수정해",
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-001"},
		},
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Source:      "model",
			Severity:    reviewSeverityHigh,
			Category:    "correctness",
			Title:       "Old issue",
			RequiredFix: "Show this issue before editing.",
		}},
	}
	session.LastReviewRun = &run
	session.AddMessage(Message{Role: "user", Text: "@Source/Sample.cpp:10-40 검토하고 버그를 수정해"})
	session.AddMessage(Message{Role: "assistant", Text: "검토 결과:\n- RF-001: Show this issue before editing."})
	session.AddMessage(Message{Role: "user", Text: "이제 다른 질문이야"})
	message := Message{
		Role: "assistant",
		ToolCalls: []ToolCall{{
			ID:        "call-patch",
			Name:      "apply_patch",
			Arguments: `{"patch":"*** Begin Patch\n*** Update File: Source/Sample.cpp\n@@\n-old\n+new\n*** End Patch"}`,
		}},
	}

	if !shouldRetryPreFixEditWithoutVisibleReviewSummary(message, session) {
		t.Fatalf("stale previous-turn summary must not satisfy the current edit turn")
	}
}

func TestAgentDoesNotRetryEditAfterStoredPreFixVisibleReviewSummary(t *testing.T) {
	root := t.TempDir()
	updatePlanTool := &staticTool{name: "update_plan", output: "planned"}
	readFileTool := &staticTool{name: "read_file", output: "source excerpt"}
	patchTool := &staticTool{name: "apply_patch", output: "patched"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-plan",
						Name:      "update_plan",
						Arguments: `{"items":[{"status":"in_progress","step":"코드 확인"}]}`,
					}},
				},
			},
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-read",
						Name:      "read_file",
						Arguments: `{"path":"Source/Sample.cpp","start_line":1,"end_line":80}`,
					}},
				},
			},
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-patch",
						Name:      "apply_patch",
						Arguments: `{"patch":"*** Begin Patch\n*** Update File: Source/Sample.cpp\n@@\n-old\n+new\n*** End Patch"}`,
					}},
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "수정했습니다.",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "@Source/Sample.cpp:10-40 검토하고 버그를 수정해"})
	session.LastReviewRun = &ReviewRun{
		Trigger:   reviewBeforeFixTrigger,
		Objective: "@Source/Sample.cpp:10-40 검토하고 버그를 수정해",
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-001"},
		},
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Source:      "model",
			Severity:    reviewSeverityHigh,
			Category:    "correctness",
			Title:       "단일 처리 실패가 전체 열거를 중단합니다",
			RequiredFix: "현재 항목만 건너뛰고 다음 항목을 계속 처리하세요.",
		}},
	}
	summary := formatPreFixVisibleReviewSummary(Config{}, *session.LastReviewRun)
	if !strings.Contains(summary, "검토 결과:") || !strings.Contains(summary, "RF-001") {
		t.Fatalf("bad test summary: %q", summary)
	}
	session.AddMessage(Message{Role: "assistant", Text: summary})
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(updatePlanTool, readFileTool, patchTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.completeLoop(context.Background(), false, true, false)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "수정했습니다." && reply != "done" {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if updatePlanTool.calls != 1 || readFileTool.calls != 1 || patchTool.calls != 1 {
		t.Fatalf("expected plan/read/patch to execute once, got plan=%d read=%d patch=%d", updatePlanTool.calls, readFileTool.calls, patchTool.calls)
	}
	for _, req := range provider.requests {
		if len(req.Messages) == 0 {
			continue
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "user" && strings.Contains(last.Text, "파일 쓰기/패치 도구를 호출하기 전에") {
			t.Fatalf("stored visible pre-fix summary should avoid another summary retry, got %q", last.Text)
		}
	}
}

func TestAgentReportsTokenLimitWhenModelStopsWithEmptyResponse(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message:    Message{Role: "assistant"},
				StopReason: "length",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "inspect the workspace")
	if err == nil {
		t.Fatalf("expected token limit error")
	}
	if !strings.Contains(err.Error(), "token limit") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "stop_reason=length") {
		t.Fatalf("expected stop_reason in error, got %v", err)
	}
}

func TestAgentToolLoopLimitIncludesLastToolSummaryAndStopReason(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-1",
						Name:      "list_files",
						Arguments: `{}`,
					}},
				},
				StopReason: "tool_calls",
			},
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-2",
						Name:      "list_files",
						Arguments: `{"path":"."}`,
					}},
				},
				StopReason: "tool_calls",
			},
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-3",
						Name:      "list_files",
						Arguments: `{"path":"."}`,
					}},
				},
				StopReason: "tool_calls",
			},
		},
	}
	cfg := Config{MaxToolIterations: 2}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "inspect the workspace")
	if err == nil {
		t.Fatalf("expected tool loop limit error")
	}
	if !strings.Contains(err.Error(), "tool loop limit exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "last_tools=list_files") {
		t.Fatalf("expected last tool summary, got %v", err)
	}
	if !strings.Contains(err.Error(), "stop_reason=tool_calls") {
		t.Fatalf("expected stop reason, got %v", err)
	}
	if !strings.Contains(err.Error(), "iteration=3") {
		t.Fatalf("expected iteration count, got %v", err)
	}
	if !strings.Contains(err.Error(), "max_iterations=2") {
		t.Fatalf("expected max iteration count, got %v", err)
	}
	if !strings.Contains(err.Error(), "recent_turns=") {
		t.Fatalf("expected recent tool turns summary, got %v", err)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected one recovery turn beyond the normal tool budget, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[2]
	if len(lastRequest.Messages) == 0 {
		t.Fatalf("expected recovery guidance before final tool-loop-limit turn")
	}
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "tool budget has been exhausted") {
		t.Fatalf("expected tool-loop-limit recovery guidance, got %#v", lastMessage)
	}
}

func TestAgentUsesRecoveryTurnWhenToolBudgetIsExhausted(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{"path": "."}),
			{
				Message: Message{
					Role: "assistant",
					Text: "I already inspected the workspace listing and can answer from that evidence.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Updated main.go, go test ./... passed, and no remaining blockers were recorded.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Updated main.go, go test ./... passed, and no remaining blockers were recorded.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Updated main.go, go test ./... passed, and no remaining blockers were recorded.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			MaxToolIterations: 1,
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect the workspace")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "I already inspected the workspace listing and can answer from that evidence." {
		t.Fatalf("unexpected reply after recovery turn: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected one extra recovery turn, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[1]
	if len(lastRequest.Messages) == 0 {
		t.Fatalf("expected recovery guidance before final turn")
	}
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "tool budget has been exhausted") {
		t.Fatalf("expected tool-loop-limit recovery guidance, got %#v", lastMessage)
	}
}

func TestAgentExtendsToolBudgetWhenRecentTurnsShowProgress(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{"path": "."}),
			toolCallResponse("grep", map[string]any{"pattern": "alpha", "path": "."}),
			toolCallResponse("read_file", map[string]any{"path": "sample.txt", "start_line": 1, "end_line": 1}),
			{
				Message: Message{
					Role: "assistant",
					Text: "I gathered enough evidence after the extended tool budget.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			MaxToolIterations: 2,
		},
		Client: provider,
		Tools: NewToolRegistry(
			NewListFilesTool(ws),
			NewGrepTool(ws),
			NewReadFileTool(ws),
		),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect the workspace carefully")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "I gathered enough evidence after the extended tool budget." {
		t.Fatalf("unexpected reply after budget extension: %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected extra model turns after budget extension, got %d requests", len(provider.requests))
	}
	extendedRequest := provider.requests[2]
	if len(extendedRequest.Messages) == 0 {
		t.Fatalf("expected extension guidance before the extended turn")
	}
	lastMessage := extendedRequest.Messages[len(extendedRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "tool budget is extended") {
		t.Fatalf("expected tool budget extension guidance, got %#v", lastMessage)
	}
}

func TestCompactCutIndexPreservesRecentToolTurns(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "u1"},
		{Role: "assistant", Text: "a1"},
		{Role: "user", Text: "u2"},
		{Role: "assistant", Text: "a2"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "list_files", Arguments: `{}`}}},
		{Role: "tool", ToolName: "list_files", Text: "ok"},
		{Role: "user", Text: "g1"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "grep", Arguments: `{"pattern":"x"}`}}},
		{Role: "tool", ToolName: "grep", Text: "ok"},
		{Role: "user", Text: "g2"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "read_file", Arguments: `{"path":"a.cpp"}`}}},
		{Role: "tool", ToolName: "read_file", Text: "ok"},
		{Role: "user", Text: "g3"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "run_shell", Arguments: `{"command":"go test ./..."}`}}},
		{Role: "tool", ToolName: "run_shell", Text: "ok"},
		{Role: "user", Text: "g4"},
	}

	got := compactCutIndex(messages, 8, 4)
	if got != 4 {
		t.Fatalf("expected compact cut index 4 to preserve recent tool turns, got %d", got)
	}
}

func TestCompactCutIndexPreservesPinnedVerificationMessages(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "u1"},
		{Role: "assistant", Text: "a1"},
		{Role: "user", Text: "Automatic verification results:\n- step one failed\n- step two pending"},
		{Role: "assistant", Text: "a2"},
		{Role: "user", Text: "u2"},
		{Role: "assistant", Text: "a3"},
		{Role: "user", Text: "u3"},
		{Role: "assistant", Text: "a4"},
		{Role: "user", Text: "u4"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "list_files", Arguments: `{}`}}},
		{Role: "tool", ToolName: "list_files", Text: "ok"},
		{Role: "user", Text: "u5"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "grep", Arguments: `{"pattern":"x"}`}}},
		{Role: "tool", ToolName: "grep", Text: "ok"},
		{Role: "user", Text: "u6"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "read_file", Arguments: `{"path":"a.cpp"}`}}},
		{Role: "tool", ToolName: "read_file", Text: "ok"},
		{Role: "user", Text: "u7"},
	}

	got := compactCutIndex(messages, 8, 4)
	if got != 2 {
		t.Fatalf("expected compact cut index 2 to preserve pinned verification message, got %d", got)
	}
}

func TestCompactRetainedMessagesWithinBudgetKeepsNewestAndTruncatesBoundary(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "old context that should move into summary"},
		{Role: "user", Text: strings.Repeat("middle-", 20)},
		{Role: "user", Text: "new"},
	}

	retained, summarizePrefix := compactRetainedMessagesWithinBudget(messages, 24)
	if summarizePrefix != 2 {
		t.Fatalf("expected dropped and truncated boundary messages to be summarized, got prefix %d", summarizePrefix)
	}
	if len(retained) != 2 {
		t.Fatalf("expected truncated boundary plus newest message, got %#v", retained)
	}
	if retained[1].Text != "new" {
		t.Fatalf("expected newest message to survive intact, got %#v", retained)
	}
	if !strings.HasSuffix(retained[0].Text, "...") || strings.Contains(retained[0].Text, "middle-middle-middle-middle") {
		t.Fatalf("expected boundary message to be truncated, got %q", retained[0].Text)
	}
}

func TestCompactRetainedMessagesWithinBudgetPreservesImagesOnBoundary(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "old"},
		{
			Role: "user",
			Text: strings.Repeat("image-context-", 10),
			Images: []MessageImage{{
				Path:      "shot.png",
				MediaType: "image/png",
			}},
		},
		{Role: "user", Text: "new"},
	}

	retained, summarizePrefix := compactRetainedMessagesWithinBudget(messages, 20)
	if summarizePrefix != 2 {
		t.Fatalf("expected boundary image message to be summarized after truncation, got prefix %d", summarizePrefix)
	}
	if len(retained) != 2 || len(retained[0].Images) != 1 {
		t.Fatalf("expected truncated boundary to retain image payload, got %#v", retained)
	}
	if retained[1].Text != "new" {
		t.Fatalf("expected newest message to survive, got %#v", retained)
	}
}

func TestCompactRetainedMessagesWithinBudgetPreservesCodexStructuredHistoryItems(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "old"},
		{
			Role: "assistant",
			LocalShellCalls: []MessageLocalShellCall{{
				CallID: "call_shell",
				Status: "completed",
				Action: map[string]any{
					"type":    "exec",
					"command": []any{"echo", "hi"},
				},
			}},
			CodexCompactionItems: []MessageCodexCompactionItem{{
				Type:             "context_compaction",
				EncryptedContent: "sealed-context",
			}},
		},
		{Role: "user", Text: "new"},
	}

	retained, _ := compactRetainedMessagesWithinBudget(messages, 4)
	if len(retained) != 2 {
		t.Fatalf("expected newest message plus structured assistant history, got %#v", retained)
	}
	if len(retained[0].LocalShellCalls) != 1 || len(retained[0].CodexCompactionItems) != 1 {
		t.Fatalf("expected compact boundary to preserve Codex structured items, got %#v", retained[0])
	}
	if retained[1].Text != "new" {
		t.Fatalf("expected newest message to survive intact, got %#v", retained)
	}
}

func TestCompactRetainedMessagesWithinBudgetDropsOrphanToolOutputAtBoundary(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "old"},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:        "call_read",
				Name:      "read_file",
				Arguments: `{"path":"` + strings.Repeat("deep/", 20) + `main.go"}`,
			}},
		},
		{
			Role:       "tool",
			ToolCallID: "call_read",
			ToolName:   "read_file",
			Text:       "ok",
		},
		{Role: "user", Text: "new"},
	}

	retained, _ := compactRetainedMessagesWithinBudget(messages, 20)
	for _, msg := range retained {
		if msg.Role == "tool" && msg.ToolCallID == "call_read" {
			t.Fatalf("compaction must not retain orphan tool output without its assistant call, got %#v", retained)
		}
	}
	if len(retained) != 1 || retained[0].Text != "new" {
		t.Fatalf("expected only newest message after dropping orphan tool output, got %#v", retained)
	}
}

func TestCompactDropOrphanToolMessagesFiltersCodexToolOutputItems(t *testing.T) {
	messages := []Message{
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{ID: "call_read", Name: "read_file"},
				{ID: "call_patch", Name: "apply_patch"},
				{ID: "call_search", Name: "tool_search"},
			},
			LocalShellCalls: []MessageLocalShellCall{{
				CallID: "call_shell",
				Status: "completed",
			}},
		},
		{
			Role: "assistant",
			CodexToolOutputItems: []MessageCodexToolOutputItem{
				{Type: "function_call_output", CallID: "call_read", Text: "read ok"},
				{Type: "custom_tool_call_output", CallID: "call_patch", Text: "patch ok"},
				{Type: "tool_search_output", CallID: "call_search", Execution: "client"},
				{Type: "function_call_output", CallID: "call_shell", Text: "shell ok"},
				{Type: "function_call_output", CallID: "call_missing", Text: "orphan"},
				{Type: "tool_search_output", CallID: "call_missing_search", Execution: "client"},
				{Type: "tool_search_output", Execution: "server"},
			},
		},
	}

	filtered := compactDropOrphanToolMessages(messages)
	if len(filtered) != 2 {
		t.Fatalf("expected both assistant messages to remain, got %#v", filtered)
	}
	outputs := filtered[1].CodexToolOutputItems
	if len(outputs) != 5 {
		t.Fatalf("expected matching outputs plus server search output, got %#v", outputs)
	}
	for _, output := range outputs {
		if output.CallID == "call_missing" || output.CallID == "call_missing_search" {
			t.Fatalf("expected orphan Codex output item to be dropped, got %#v", outputs)
		}
	}
	if outputs[4].Type != "tool_search_output" || outputs[4].Execution != "server" {
		t.Fatalf("expected server tool search output without a call id to survive, got %#v", outputs)
	}
}

func TestMessageLooksLikeCompactContextOnlyKeepsStructuredHistoryItems(t *testing.T) {
	msg := Message{
		Role: "user",
		Text: "[Conversation Runtime Context]\nstate\n[/Conversation Runtime Context]",
		LocalShellCalls: []MessageLocalShellCall{{
			CallID: "call_shell",
			Status: "completed",
		}},
	}

	if messageLooksLikeCompactContextOnly(msg) {
		t.Fatalf("structured history item must prevent context-only compaction drop")
	}
}

func TestCompactRetainedMessagesWithinBudgetDropsContextOnlyMessagesBeforeBudget(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "old"},
		{Role: "user", Text: "[Conversation Runtime Context]\n" + strings.Repeat("runtime-context-", 20) + "\n[/Conversation Runtime Context]"},
		{Role: "user", Text: "new"},
	}

	retained, summarizePrefix := compactRetainedMessagesWithinBudget(messages, 6)
	if summarizePrefix != 0 {
		t.Fatalf("expected context-only message to be discarded before budget accounting, got prefix %d", summarizePrefix)
	}
	if len(retained) != 2 {
		t.Fatalf("expected old and newest user messages to survive, got %#v", retained)
	}
	if retained[0].Text != "old" || retained[1].Text != "new" {
		t.Fatalf("expected context-only message to be omitted from retained history, got %#v", retained)
	}
}

func TestSummarizeMessagesIncludesCodexStructuredHistoryItems(t *testing.T) {
	summary := summarizeMessages([]Message{
		{
			Role: "assistant",
			WebSearchCalls: []MessageWebSearchCall{{
				Status: "completed",
			}},
		},
		{
			Role: "assistant",
			LocalShellCalls: []MessageLocalShellCall{{
				CallID: "call_shell",
				Status: "completed",
			}},
		},
		{
			Role: "assistant",
			CodexCompactionItems: []MessageCodexCompactionItem{{
				Type:             "context_compaction",
				EncryptedContent: "sealed-context",
			}},
		},
	}, "compact")

	for _, want := range []string{"web search call(s): 1", "local shell call(s): 1", "codex compaction item(s): 1"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("expected compact summary to include %q, got %q", want, summary)
		}
	}
}

func TestSummarizeMessagesOmitsContextOnlyMessages(t *testing.T) {
	summary := summarizeMessages([]Message{
		{Role: "user", Text: "<environment_context>\n" + strings.Repeat("cwd=/tmp\n", 8) + "</environment_context>"},
		{Role: "user", Text: "real user request"},
	}, "compact")

	if strings.Contains(summary, "environment_context") || strings.Contains(summary, "cwd=/tmp") {
		t.Fatalf("expected context-only wrapper to be omitted from compact summary, got %q", summary)
	}
	if !strings.Contains(summary, "real user request") {
		t.Fatalf("expected real user request in compact summary, got %q", summary)
	}
}

func TestCompactWithTriggerSummarizesRetainedMessagesDroppedByBudget(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	for i := 0; i < 18; i++ {
		session.AddMessage(Message{Role: "user", Text: fmt.Sprintf("old message %02d", i)})
	}
	session.AddMessage(Message{Role: "user", Text: "recent before huge"})
	session.AddMessage(Message{Role: "user", Text: strings.Repeat("huge-boundary-", 50)})
	session.AddMessage(Message{Role: "user", Text: "latest"})
	agent := &Agent{
		Config: Config{
			AutoCompactChars: 40,
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	summary, err := agent.CompactWithTrigger(context.Background(), "test compact", "auto", "test")
	if err != nil {
		t.Fatalf("CompactWithTrigger: %v", err)
	}
	if !strings.Contains(summary, "huge-boundary") {
		t.Fatalf("expected truncated boundary to be summarized before dropping detail, got %q", summary)
	}
	if len(agent.Session.Messages) != 2 {
		t.Fatalf("expected retained messages to be budget-bound to boundary plus latest, got %#v", agent.Session.Messages)
	}
	if agent.Session.Messages[1].Text != "latest" {
		t.Fatalf("expected latest message to survive intact, got %#v", agent.Session.Messages)
	}
	if len(agent.Session.Messages[0].Text) > 40 {
		t.Fatalf("expected retained boundary to be truncated, got %q", agent.Session.Messages[0].Text)
	}
}

func TestSummarizeMessagesIncludesCompactToolErrorDetails(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "inspect the failure"},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				Name:      "failing_tool",
				Arguments: `{}`,
			}},
		},
		{
			Role:     "tool",
			ToolName: "failing_tool",
			Text:     "stderr line\n\nERROR: preview surface busy",
			IsError:  true,
		},
	}

	summary := summarizeMessages(messages, "Auto-compacted due to context growth.")
	if !strings.Contains(summary, "tool turn: failing_tool:error:preview surface busy") {
		t.Fatalf("expected compact summary to preserve tool error detail, got %q", summary)
	}
}

func TestSummarizeMessagesSkipsAssistantTextOnlyPhases(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "original user request"},
		{Role: "assistant", Phase: messagePhaseCommentary, Text: "progress update that should not be replayed"},
		{Role: "assistant", Phase: messagePhaseFinalAnswerCandidate, Text: "unaccepted final answer candidate"},
		{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: "accepted final answer"},
		{
			Role:  "assistant",
			Phase: messagePhaseCommentary,
			ToolCalls: []ToolCall{{
				Name:      "read_file",
				Arguments: `{"path":"a.cpp"}`,
			}},
		},
		{Role: "tool", ToolName: "read_file", Text: "ok"},
	}

	summary := summarizeMessages(messages, "Auto-compacted due to context growth.")
	if strings.Contains(summary, "progress update that should not be replayed") {
		t.Fatalf("assistant commentary leaked into compact summary: %q", summary)
	}
	if strings.Contains(summary, "unaccepted final answer candidate") {
		t.Fatalf("final-answer candidate leaked into compact summary: %q", summary)
	}
	if strings.Contains(summary, "accepted final answer") {
		t.Fatalf("accepted assistant final answer leaked into compact summary: %q", summary)
	}
	if !strings.Contains(summary, "original user request") {
		t.Fatalf("expected user request to remain in compact summary, got %q", summary)
	}
	if !strings.Contains(summary, "tool turn: read_file[a.cpp]:ok") {
		t.Fatalf("expected assistant tool evidence to remain in compact summary, got %q", summary)
	}
}

func TestCompactDropsStaleFinalAnswerCandidatesBeforeSummarizing(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "older user request"})
	session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswerCandidate, Text: "stale final candidate"})
	for i := 0; i < 20; i++ {
		session.AddMessage(Message{Role: "user", Text: fmt.Sprintf("message %02d", i)})
	}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	summary := agent.Compact("test compact")
	if strings.Contains(summary, "stale final candidate") {
		t.Fatalf("stale final-answer candidate leaked into compact summary: %q", summary)
	}
	for _, msg := range agent.Session.Messages {
		if msg.Role == "assistant" && msg.Phase == messagePhaseFinalAnswerCandidate {
			t.Fatalf("stale final-answer candidate remained after compact: %#v", msg)
		}
	}
}

func TestSummarizeMessagesKeepsPinnedVerificationSnippet(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "Automatic verification results:\n- msbuild failed\n- ctest skipped\n- rerun needed"},
	}

	summary := summarizeMessages(messages, "Auto-compacted due to context growth.")
	if !strings.Contains(summary, "Automatic verification results:") || !strings.Contains(summary, "msbuild failed") {
		t.Fatalf("expected compact summary to preserve pinned verification snippet, got %q", summary)
	}
}

func TestAgentPromptsRereadAfterEditTargetMismatch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "completion.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("replace_in_file", map[string]any{
				"path":    "completion.go",
				"search":  "missing",
				"replace": "found",
			}),
			{Message: Message{Role: "assistant", Text: "I need to re-read the file before editing it."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReplaceInFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update completion.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "I need to re-read the file before editing it." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected a follow-up turn after edit target mismatch, got %d", len(provider.requests))
	}
	lastTurn := provider.requests[1]
	if len(lastTurn.Messages) == 0 {
		t.Fatalf("expected reread guidance before second turn")
	}
	lastMessage := lastTurn.Messages[len(lastTurn.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "First read the exact file again from the same path") {
		t.Fatalf("expected reread guidance, got %#v", lastMessage)
	}
	if !strings.Contains(lastMessage.Text, "tool error's expected/current context diagnostics") {
		t.Fatalf("expected mismatch diagnostic guidance, got %#v", lastMessage)
	}
	if !strings.Contains(lastMessage.Text, "Do not repeat or lightly reformat the previous patch text") {
		t.Fatalf("expected previous patch reuse warning, got %#v", lastMessage)
	}
	if !strings.Contains(lastMessage.Text, "multiple related hunks or files") {
		t.Fatalf("expected cohesive root-repair guidance, got %#v", lastMessage)
	}
}

func TestAgentAllowsCohesiveApplyPatchAfterEditTargetMismatchReanchor(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc existing() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{
				"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+// stale attempt\n*** End Patch\n",
			}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			toolCallResponse("apply_patch", map[string]any{
				"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+// first broad hunk\n@@\n func existing() {}\n+// second broad hunk\n*** End Patch\n",
			}),
			{Message: Message{Role: "assistant", Text: "Applied the cohesive recovery patch."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	patchTool := &sequenceTool{
		name:    "apply_patch",
		outputs: []string{"", "patched"},
		errs:    []error{fmt.Errorf("%w: stale hunk", ErrEditTargetMismatch), nil},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Applied the cohesive recovery patch." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if patchTool.calls != 2 {
		t.Fatalf("expected stale attempt and cohesive retry to execute after reanchor; got %d calls", patchTool.calls)
	}
	if sessionContainsToolResultText(session, "call-1", "NOT_EXECUTED: this recovery apply_patch was not executed") {
		t.Fatalf("cohesive recovery patch should not be deferred after reanchor")
	}
	if scriptedRequestsContainText(provider.requests, "one file and one independent hunk") {
		t.Fatalf("recovery guidance should not force a one-file one-hunk repair, got %#v", provider.requests)
	}
}

func TestAgentBlocksImmediateApplyPatchAfterEditTargetMismatchUntilReanchor(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc existing() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	broadPatch := "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+// first broad hunk\n*** Add File: other.go\n+package main\n*** End Patch\n"
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{
				"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+// stale attempt\n*** End Patch\n",
			}),
			toolCallResponse("apply_patch", map[string]any{"patch": broadPatch}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			toolCallResponse("apply_patch", map[string]any{"patch": broadPatch}),
			{Message: Message{Role: "assistant", Text: "Applied after reanchor."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	patchTool := &sequenceTool{
		name:    "apply_patch",
		outputs: []string{"", "patched"},
		errs:    []error{fmt.Errorf("%w: stale hunk", ErrEditTargetMismatch), nil},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Applied after reanchor." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if patchTool.calls != 2 {
		t.Fatalf("expected stale attempt and post-reanchor patch to execute; got %d calls", patchTool.calls)
	}
	if !sessionContainsToolResultText(session, "call-1", "previous edit targeted stale or mismatched file contents") {
		t.Fatalf("expected immediate edit retry to be blocked until reanchor")
	}
	if !scriptedRequestsContainText(provider.requests, "re-anchor") {
		t.Fatalf("expected reanchor guidance before retry, got %#v", provider.requests)
	}
}

func TestAgentStopsRepeatedImmediateEditAfterMismatchWithoutReanchor(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc existing() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	patch := "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+// retry\n*** End Patch\n"
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{
				"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+// stale attempt\n*** End Patch\n",
			}),
			toolCallResponse("apply_patch", map[string]any{"patch": patch}),
			toolCallResponse("apply_patch", map[string]any{"patch": patch}),
			{Message: Message{Role: "assistant", Text: "this should not be requested"}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	patchTool := &sequenceTool{
		name: "apply_patch",
		errs: []error{fmt.Errorf("%w: stale hunk", ErrEditTargetMismatch)},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "fix it")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "without re-anchoring") {
		t.Fatalf("expected reanchor loop stop reply, got %q", reply)
	}
	if patchTool.calls != 1 {
		t.Fatalf("expected only the stale patch attempt to execute; got %d calls", patchTool.calls)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected stop after repeated immediate edit retry, got %d requests", len(provider.requests))
	}
	if !sessionContainsToolResultText(session, "call-1", "previous edit targeted stale or mismatched file contents") {
		t.Fatalf("expected repeated immediate edit retry to be recorded as NOT_EXECUTED")
	}
}

func TestAgentStopsBeforeNextModelTurnWhenContextCanceledDuringTool(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("cancel_during_tool", map[string]any{}),
			{Message: Message{Role: "assistant", Text: "this should never be requested"}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	ctx, cancel := context.WithCancel(context.Background())
	tool := &cancelDuringToolTool{
		cancel: cancel,
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(tool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(ctx, "cancel during tool execution")
	if err == nil {
		t.Fatalf("expected cancellation error")
	}
	if err != context.Canceled {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if tool.calls != 1 {
		t.Fatalf("expected tool to run once, got %d", tool.calls)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected no follow-up model turn after cancellation, got %d requests", len(provider.requests))
	}
	if sessionContainsInProgressToolResult(session) {
		t.Fatalf("tool placeholders must not remain IN_PROGRESS, messages=%#v", session.Messages)
	}
	if !sessionContainsToolResultText(session, "call-1", "tool finished after cancel") {
		t.Fatalf("expected completed tool result to be saved before cancellation")
	}
}

func TestAgentClosesRemainingToolPlaceholdersWhenContextCanceledDuringBatch(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			multiToolCallResponse(
				ToolCall{ID: "call-1", Name: "cancel_during_tool", Arguments: `{}`},
				ToolCall{ID: "call-2", Name: "second_tool", Arguments: `{}`},
			),
			{Message: Message{Role: "assistant", Text: "this should never be requested"}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	ctx, cancel := context.WithCancel(context.Background())
	cancelTool := &cancelDuringToolTool{
		cancel: cancel,
	}
	secondTool := &sequenceTool{
		name:    "second_tool",
		outputs: []string{"second should not run"},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(cancelTool, secondTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(ctx, "cancel during a multi-tool batch")
	if err == nil {
		t.Fatalf("expected cancellation error")
	}
	if err != context.Canceled {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if cancelTool.calls != 1 {
		t.Fatalf("expected first tool to run once, got %d calls", cancelTool.calls)
	}
	if secondTool.calls != 0 {
		t.Fatalf("expected second tool not to run after cancellation, got %d calls", secondTool.calls)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected no follow-up model turn after cancellation, got %d requests", len(provider.requests))
	}
	if sessionContainsInProgressToolResult(session) {
		t.Fatalf("tool placeholders must not remain IN_PROGRESS, messages=%#v", session.Messages)
	}
	if !sessionContainsToolResultText(session, "call-1", "tool finished after cancel") {
		t.Fatalf("expected completed first tool result to be saved before cancellation")
	}
	if !sessionContainsToolResultText(session, "call-2", "NOT_EXECUTED: context canceled before this tool could run") {
		t.Fatalf("expected remaining tool to be closed as NOT_EXECUTED, messages=%#v", session.Messages)
	}
}

func TestAgentReturnsPromptlyWhenContextCanceledDuringModelTurn(t *testing.T) {
	root := t.TempDir()
	provider := &blockingProviderClient{started: make(chan struct{})}
	session := NewSession(root, "blocking", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	ctx, cancel := context.WithCancel(context.Background())
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	done := make(chan error, 1)
	go func() {
		_, err := agent.Reply(ctx, "cancel during model execution")
		done <- err
	}()

	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("provider did not start model turn")
	}

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("agent did not return promptly after model cancellation")
	}

	if provider.calls != 1 {
		t.Fatalf("expected one provider call, got %d", provider.calls)
	}
}

func TestAgentPersistsInProgressToolStateBeforeExecution(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	sessionPath := filepath.Join(store.Root(), session.ID+".json")

	sawInProgress := false
	observer := &observingSessionTool{
		name:        "observe_session",
		sessionPath: sessionPath,
		output:      "observed",
	}
	observer.onExecute = func(data []byte) {
		if strings.Contains(string(data), `"text": "IN_PROGRESS: observe_session"`) {
			sawInProgress = true
		}
	}

	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("observe_session", map[string]any{}),
			{Message: Message{Role: "assistant", Text: "done"}},
		},
	}
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(observer),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect session persistence")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "done") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if !sawInProgress {
		t.Fatalf("expected observer to see an in-progress tool state before execution")
	}
}

func TestAgentPersistsCompletedToolResultsBetweenToolCalls(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	sessionPath := filepath.Join(store.Root(), session.ID+".json")

	firstTool := &observingSessionTool{
		name:   "first_tool",
		output: "first done",
	}
	secondSawFirst := false
	secondTool := &observingSessionTool{
		name:        "second_tool",
		sessionPath: sessionPath,
		output:      "second done",
		onExecute: func(data []byte) {
			secondSawFirst = strings.Contains(string(data), `"tool_name": "first_tool"`) &&
				strings.Contains(string(data), `"text": "first done"`)
		},
	}

	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{
						{ID: "call-1", Name: "first_tool", Arguments: `{}`},
						{ID: "call-2", Name: "second_tool", Arguments: `{}`},
					},
				},
				StopReason: "tool_calls",
			},
			{Message: Message{Role: "assistant", Text: "done"}},
		},
	}
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(firstTool, secondTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect persistence between tool calls")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "done") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if !secondSawFirst {
		t.Fatalf("expected the second tool call to observe the first persisted tool result")
	}
}

func TestAgentNudgesAfterMalformedWriteFileArguments(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-1",
						Name:      "write_file",
						Arguments: `{"path":"main.go","content":"package main`,
					}},
				},
			},
			{Message: Message{Role: "assistant", Text: "I retried with apply_patch after the malformed write_file call."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), NewApplyPatchTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update the file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "I retried with apply_patch after the malformed write_file call." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected follow-up turn after malformed tool arguments, got %d", len(provider.requests))
	}
	lastTurn := provider.requests[1]
	if len(lastTurn.Messages) == 0 {
		t.Fatalf("expected guidance before second turn")
	}
	lastMessage := lastTurn.Messages[len(lastTurn.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "use apply_patch instead of write_file") {
		t.Fatalf("expected apply_patch recovery guidance, got %#v", lastMessage)
	}
	for _, tool := range lastTurn.Tools {
		if tool.Name == "write_file" {
			t.Fatalf("expected write_file to be disabled after malformed arguments")
		}
	}
}

func TestAgentBlocksDisabledToolEvenIfModelCallsIt(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-1",
						Name:      "write_file",
						Arguments: `{"path":"main.go","content":"package main`,
					}},
				},
			},
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-2",
						Name:      "write_file",
						Arguments: `{"path":"main.go","content":"package main\n"}`,
					}},
				},
			},
			{Message: Message{Role: "assistant", Text: "done after disabled tool was blocked"}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), NewApplyPatchTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update the file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "done after disabled tool was blocked" {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected one retry and one follow-up after blocked disabled tool, got %d", len(provider.requests))
	}
	for _, tool := range provider.requests[1].Tools {
		if tool.Name == "write_file" {
			t.Fatalf("expected write_file to be hidden from the retry request")
		}
	}
	if _, statErr := os.Stat(filepath.Join(root, "main.go")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("disabled write_file should not create main.go, stat error: %v", statErr)
	}
	blocked := false
	for _, msg := range session.Messages {
		if msg.Role != "tool" || msg.ToolCallID != "call-2" {
			continue
		}
		blocked = strings.Contains(msg.Text, "NOT_EXECUTED") &&
			toolMetaString(msg.ToolMeta, "result_class") == "turn_tool_exposure_block" &&
			toolMetaBool(msg.ToolMeta, "turn_tool_disabled")
	}
	if !blocked {
		t.Fatalf("expected disabled tool result to be persisted, got %#v", session.Messages)
	}
	var blockedBegin *ConversationEvent
	var blockedEnd *ConversationEvent
	for i := range session.ConversationEvents {
		event := &session.ConversationEvents[i]
		if event.CorrelationID != "call-2" {
			continue
		}
		switch event.Kind {
		case conversationEventKindPatchApplyBegin:
			blockedBegin = event
		case conversationEventKindPatchApplyEnd:
			blockedEnd = event
		}
	}
	if blockedBegin == nil || blockedEnd == nil {
		t.Fatalf("expected blocked disabled tool to have Codex-style begin/end events, got %#v", session.ConversationEvents)
	}
	if blockedEnd.Entities["status"] != "declined" || blockedEnd.Severity != conversationSeverityWarn {
		t.Fatalf("expected blocked disabled tool to end as declined, got %#v", blockedEnd)
	}
}

func TestCompleteModelTurnRetriesOnceOnTimeout(t *testing.T) {
	provider := &timeoutThenSuccessProviderClient{}
	var progress []string
	agent := &Agent{
		Config: Config{
			MaxRequestRetries:   1,
			RequestRetryDelayMs: 1,
			RequestTimeoutSecs:  1,
		},
		Client: provider,
		EmitProgress: func(text string) {
			progress = append(progress, text)
		},
	}

	resp, err := agent.completeModelTurn(context.Background(), ChatRequest{
		Model: "test-model",
	})
	if err != nil {
		t.Fatalf("completeModelTurn: %v", err)
	}
	if resp.Message.Text != "recovered" {
		t.Fatalf("unexpected response text: %q", resp.Message.Text)
	}
	if provider.calls != 2 {
		t.Fatalf("expected two provider attempts, got %d", provider.calls)
	}
	if len(progress) == 0 || !strings.Contains(progress[0], "Retrying once") {
		t.Fatalf("expected retry progress message, got %#v", progress)
	}
}

func TestCompleteModelTurnReturnsTimeoutAfterRetryExhausted(t *testing.T) {
	provider := &timeoutProviderClient{}
	agent := &Agent{
		Config: Config{
			MaxRequestRetries:   1,
			RequestRetryDelayMs: 1,
			RequestTimeoutSecs:  1,
		},
		Client: provider,
	}

	_, err := agent.completeModelTurn(context.Background(), ChatRequest{
		Model: "test-model",
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded after retry exhaustion, got %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("expected two provider attempts, got %d", provider.calls)
	}
}

func TestCompleteModelTurnDoesNotRetryOnUserCancellation(t *testing.T) {
	provider := &blockingProviderClient{started: make(chan struct{})}
	agent := &Agent{
		Config: Config{
			MaxRequestRetries:   1,
			RequestRetryDelayMs: 1,
			RequestTimeoutSecs:  1,
		},
		Client: provider,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := agent.completeModelTurn(ctx, ChatRequest{
			Model: "test-model",
		})
		done <- err
	}()

	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("provider did not start model turn")
	}

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("completeModelTurn did not return after cancellation")
	}

	if provider.calls != 1 {
		t.Fatalf("expected one provider attempt on cancellation, got %d", provider.calls)
	}
}

func TestCompleteModelTurnRetriesTransientProviderErrors(t *testing.T) {
	provider := &transientErrorThenSuccessProviderClient{
		failCount: 2,
		err:       fmt.Errorf("openai API error (503 Service Unavailable): upstream overloaded"),
	}
	var progress []string
	agent := &Agent{
		Config: Config{
			MaxRequestRetries:   2,
			RequestRetryDelayMs: 1,
			RequestTimeoutSecs:  1,
		},
		Client: provider,
		EmitProgress: func(text string) {
			progress = append(progress, text)
		},
	}

	resp, err := agent.completeModelTurn(context.Background(), ChatRequest{
		Model: "test-model",
	})
	if err != nil {
		t.Fatalf("completeModelTurn: %v", err)
	}
	if resp.Message.Text != "recovered" {
		t.Fatalf("unexpected response text: %q", resp.Message.Text)
	}
	if provider.calls != 3 {
		t.Fatalf("expected three provider attempts, got %d", provider.calls)
	}
	if len(progress) == 0 || !strings.Contains(progress[0], "Transient provider error") {
		t.Fatalf("expected transient retry progress message, got %#v", progress)
	}
}

func TestCompleteModelTurnDoesNotRetryPermanentProviderErrors(t *testing.T) {
	provider := &transientErrorThenSuccessProviderClient{
		failCount: 1,
		err:       fmt.Errorf("openai API error (401 Unauthorized): invalid api key"),
	}
	agent := &Agent{
		Config: Config{
			MaxRequestRetries:   2,
			RequestRetryDelayMs: 1,
			RequestTimeoutSecs:  1,
		},
		Client: provider,
	}

	_, err := agent.completeModelTurn(context.Background(), ChatRequest{
		Model: "test-model",
	})
	if err == nil {
		t.Fatalf("expected permanent provider error")
	}
	if provider.calls != 1 {
		t.Fatalf("expected one provider attempt for permanent error, got %d", provider.calls)
	}
}

func TestAgentReadOnlyAnalysisFallsBackWhenModelDoesNotSupportToolUse(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VAllocAnalyzer.cpp"), []byte("int Check()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &toolUnsupportedThenSuccessClient{}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewGrepTool(ws), NewListFilesTool(ws), NewGitStatusTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@VAllocAnalyzer.cpp 코드에 버그가 있는지 검토해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "버그") && !strings.Contains(reply, "검토") && !strings.Contains(reply, "경계 조건") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if provider.calls != 2 {
		t.Fatalf("expected retry without tools, got %d calls", provider.calls)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected two recorded requests, got %d", len(provider.requests))
	}
	if len(provider.requests[0].Tools) == 0 {
		t.Fatalf("expected first request to include tools")
	}
	if len(provider.requests[1].Tools) != 0 {
		t.Fatalf("expected fallback request to omit tools, got %d", len(provider.requests[1].Tools))
	}
	if !strings.Contains(provider.requests[1].System, "does not support tool use") {
		t.Fatalf("expected fallback system guidance, got %q", provider.requests[1].System)
	}
}

func TestAgentReadOnlyAnalysisBlocksMutationCapableToolsAtExecution(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("source\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	marker := filepath.Join(root, "review-mode-marker.txt")
	runArgs, err := json.Marshal(map[string]any{
		"command": "Set-Content -Path review-mode-marker.txt -Value mutated",
	})
	if err != nil {
		t.Fatalf("marshal run args: %v", err)
	}
	readArgs, err := json.Marshal(map[string]any{
		"path": "target.txt",
	})
	if err != nil {
		t.Fatalf("marshal read args: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{
						{
							ID:        "call-shell",
							Name:      "run_shell",
							Arguments: string(runArgs),
						},
						{
							ID:        "call-read",
							Name:      "read_file",
							Arguments: string(readArgs),
						},
					},
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "done",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = append(session.Messages, Message{Role: "user", Text: "analysis only"})
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(NewRunShellTool(ws), NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.completeLoop(context.Background(), true, false, false)
	if err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if !strings.Contains(reply, "done") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected read-only mode to prevent marker creation, stat err=%v", err)
	}
	var shellBlocked bool
	var readSucceeded bool
	for _, msg := range session.Messages {
		if msg.Role != "tool" {
			continue
		}
		switch msg.ToolName {
		case "run_shell":
			if msg.IsError && strings.Contains(msg.Text, "NOT_EXECUTED") && toolMetaBool(msg.ToolMeta, "read_only_analysis") {
				shellBlocked = true
			}
		case "read_file":
			if !msg.IsError && strings.Contains(msg.Text, "source") {
				readSucceeded = true
			}
		}
	}
	if !shellBlocked {
		t.Fatalf("expected run_shell tool output to be a read-only block, messages=%#v", session.Messages)
	}
	if !readSucceeded {
		t.Fatalf("expected read_file to remain executable in read-only mode, messages=%#v", session.Messages)
	}
}

func TestAgentEditRequestReturnsFriendlyToolUseUnsupportedError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VAllocAnalyzer.cpp"), []byte("int Check()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &toolUnsupportedThenSuccessClient{}
	session := NewSession(root, "openrouter", "meta-llama/llama-3.2-11b-vision-instruct", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewApplyPatchTool(ws), NewGitStatusTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "@VAllocAnalyzer.cpp 코드에 버그가 있는지 검토하고 수정해줘")
	if err == nil {
		t.Fatalf("expected tool-use unsupported error")
	}
	text := err.Error()
	if !strings.Contains(text, "selected model does not support tool use") {
		t.Fatalf("expected friendly tool-use message, got %q", text)
	}
	if strings.Contains(text, "request={") {
		t.Fatalf("expected raw request dump to be hidden, got %q", text)
	}
	if provider.calls != 1 {
		t.Fatalf("expected edit request to fail without no-tools retry, got %d calls", provider.calls)
	}
}

func TestAgentRetriesWhenEditRequestHandsPatchBackWithoutUsingTools(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "VAllocAnalyzer.cpp")
	before := "int Check()\n{\n    return 0;\n}\n"
	after := "int Check()\n{\n    return 1;\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "도구 사용에 문제가 있어 직접 패치를 적용해드리지 못합니다. 위 설명에 따라 코드를 직접 수정해주시면 됩니다."}},
			toolCallResponse("read_file", map[string]any{"path": "VAllocAnalyzer.cpp"}),
			toolCallResponse("write_file", map[string]any{"path": "VAllocAnalyzer.cpp", "content": after}),
			{Message: Message{Role: "assistant", Text: "VAllocAnalyzer.cpp를 수정했고 반환값이 1이 되도록 반영했습니다."}},
		},
	}
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@VAllocAnalyzer.cpp 코드에 버그가 있는지 검토하고 수정해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "수정") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(contents) != after {
		t.Fatalf("expected file to be updated after retry, got %q", string(contents))
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected retry path to consume four requests, got %d", len(provider.requests))
	}
	secondRequest := provider.requests[1]
	if len(secondRequest.Messages) == 0 {
		t.Fatalf("expected retry guidance before second request")
	}
	lastMessage := secondRequest.Messages[len(secondRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Do not hand the patch back to the user") {
		t.Fatalf("expected manual-edit handoff guidance, got %#v", lastMessage)
	}
}

func TestAgentRetriesFinalReplyThatBlamesInternalTranscriptRecovery(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "VAllocAnalyzer.cpp")
	if err := os.WriteFile(path, []byte("int Check()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{"patch": "not a patch"}),
			{Message: Message{Role: "assistant", Text: "### 현재 차단 사항\n\n**도구 실행 파이프라인 오류**: 모든 도구가 동일한 \"ERROR: tool result was missing from the saved transcript\" 오류를 반환합니다. 세션을 다시 시작하거나 수동 패치를 직접 적용하세요."}},
			{Message: Message{Role: "assistant", Text: "내부 transcript recovery 문구를 실제 도구 장애로 보지 않고, 최신 apply_patch 오류 기준으로 다시 정리했습니다."}},
		},
	}
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewApplyPatchTool(ws), NewReadFileTool(ws), NewListFilesTool(ws), NewGrepTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@VAllocAnalyzer.cpp 코드에 버그가 있는지 검토하고 수정해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.Contains(reply, "missing from the saved transcript") || strings.Contains(reply, "수동 패치") {
		t.Fatalf("internal transcript failure leaked to final reply: %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected transcript-recovery blame retry to consume three requests, got %d", len(provider.requests))
	}
	lastMessage := provider.requests[2].Messages[len(provider.requests[2].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "not evidence that all tools are broken") {
		t.Fatalf("expected internal transcript recovery guidance, got %#v", lastMessage)
	}
}

func TestAgentBlocksToolCallsThatBlameInternalTranscriptRecovery(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "VAllocAnalyzer.cpp")
	before := "int Check()\n{\n    return 0;\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "모든 도구 호출에서 \"ERROR: tool result was missing from the saved transcript\" 오류가 반복적으로 발생하고 있습니다. 하지만 write_file로 전체 파일을 다시 작성하는 방법을 시도해보겠습니다.",
					ToolCalls: []ToolCall{{
						ID:        "call-1",
						Name:      "write_file",
						Arguments: `{"path":"VAllocAnalyzer.cpp","content":"bad"}`,
					}},
				},
			},
			{Message: Message{Role: "assistant", Text: "내부 transcript recovery 문구를 실제 도구 장애로 단정하지 않고 최신 로컬 증거 기준으로 계속하겠습니다."}},
		},
	}
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@VAllocAnalyzer.cpp 코드에 버그가 있는지 검토하고 수정해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.Contains(reply, "missing from the saved transcript") || strings.Contains(reply, "write_file") {
		t.Fatalf("internal transcript failure leaked to final reply: %q", reply)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(contents) != before {
		t.Fatalf("write_file should have been blocked before execution, got %q", string(contents))
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected transcript-recovery tool-call block to consume two requests, got %d", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "not evidence that all tools are broken") {
		t.Fatalf("expected internal transcript recovery guidance, got %#v", lastMessage)
	}
}

func TestAgentBlocksGitCommitWithoutExplicitUserRequest(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("git_commit", map[string]any{"message": "fix: unexpected commit"}),
			{Message: Message{Role: "assistant", Text: "코드 수정만 완료했고 커밋은 하지 않았습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewGitCommitTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@VAllocAnalyzer.cpp 코드에 버그가 있는지 검토하고 수정해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "커밋은 하지 않았습니다") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected git tool request to be blocked and retried, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Do not stage, commit, push, or open a PR") {
		t.Fatalf("expected git mutation guidance, got %#v", lastMessage)
	}
}

func TestAgentBlocksShellGitCommitWithoutExplicitUserRequest(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("run_shell", map[string]any{"command": `git commit -m "fix: unexpected commit"`}),
			{Message: Message{Role: "assistant", Text: "문서만 작성했고 git 작업은 하지 않았습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewRunShellTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "리서치 문서를 markdown 파일로 작성해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "git 작업은 하지 않았습니다") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected shell git mutation request to be blocked and retried, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Do not stage, commit, push, or open a PR") {
		t.Fatalf("expected git mutation guidance, got %#v", lastMessage)
	}
	var blockedEnd *ConversationEvent
	for i := range session.ConversationEvents {
		event := &session.ConversationEvents[i]
		if event.CorrelationID == "call-1" && event.Kind == conversationEventKindExecCommandEnd {
			blockedEnd = event
			break
		}
	}
	if blockedEnd == nil || blockedEnd.Entities["status"] != "declined" {
		t.Fatalf("expected blocked shell git mutation to have declined exec end event, got %#v", session.ConversationEvents)
	}
}

func TestAgentBlocksDocumentReadBeforeParentListing(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("read_file", map[string]any{"path": "anti-cheat-research/analysis/security-review.md"}),
			{Message: Message{Role: "assistant", Text: "먼저 부모 디렉터리를 확인한 뒤 진행하겠습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "리서치 문서를 markdown 파일로 작성해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "부모 디렉터리") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected document read request to be blocked and retried, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "First use list_files on the parent directory") {
		t.Fatalf("expected document read guidance, got %#v", lastMessage)
	}
}

func TestAgentBlocksDocumentReadWhenParentListedButTargetMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Tavern"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Tavern", "Other.md"), []byte("# Other\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{"path": "Tavern"}),
			toolCallResponse("read_file", map[string]any{"path": "Tavern/BugReport.md"}),
			toolCallResponse("write_file", map[string]any{
				"path": "Tavern/BugReport.md",
				"content": strings.Join([]string{
					"# Tavern Bug Report",
					"",
					"소스코드 검토 결과 총 1개 버그를 문서로 생성했습니다.",
					"",
					"| Severity | Count |",
					"|----------|-------|",
					"| Critical | 1 |",
					"| Total | 1 |",
					"",
					"## BUG-001",
					"- File: Tavern/Tavern/RuntimeManager.cpp",
					"- Impact: crash risk.",
				}, "\n"),
			}),
			{Message: Message{Role: "assistant", Text: "Tavern/BugReport.md 문서를 생성했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewListFilesTool(ws), NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected missing document read to be blocked after parent listing, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[2].Messages[len(provider.requests[2].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "If the parent directory is empty or the file is absent") {
		t.Fatalf("expected absent-file document guidance, got %#v", lastMessage)
	}
	for _, msg := range session.Messages {
		if msg.Role == "tool" && msg.ToolName == "read_file" && !msg.IsError {
			t.Fatalf("read_file must not execute successfully for a listed-but-absent generated document path: %#v", msg)
		}
	}
}

func TestAgentBlocksLocalInspectionBeforeWebResearchWhenCapabilityAvailable(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	webTool := &staticTool{
		name:   "mcp__web__search_web",
		output: "source 1\nsource 2",
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{"path": "."}),
			toolCallResponse("mcp__web__search_web", map[string]any{"query": "hypervisor anti-cheat latest"}),
			{Message: Message{Role: "assistant", Text: "웹 소스를 먼저 수집한 뒤 문서를 진행하겠습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws), webTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
		MCP: &MCPManager{
			servers: []*MCPClient{
				{
					config: MCPServerConfig{Name: "web"},
					tools: []MCPToolDescriptor{
						{Name: "search_web", Description: "Search the web for current articles and references"},
					},
				},
			},
		},
	}

	reply, err := agent.Reply(context.Background(), "Hypervisor를 이용한 게임핵 탐지 최신 기술들을 리서치하고 설계 문서를 파일로 작성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "웹 소스") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if webTool.calls != 1 {
		t.Fatalf("expected web research tool to run once, got %d", webTool.calls)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected local inspection request to be blocked and retried, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Before using local inspection or edit tools") {
		t.Fatalf("expected web research guidance, got %#v", lastMessage)
	}
}

func TestAgentBlocksWebResearchForLocalCodeRepair(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "SampleApp", "SampleWorker", "PathConverter.cpp")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("int ConvertPath()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	webTool := &staticTool{
		name:   "mcp__web__search_web",
		output: "external source",
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("mcp__web__search_web", map[string]any{"query": "std::mismatch path separator"}),
			toolCallResponse("mcp__web__search_web", map[string]any{"query": "FindFirstVolume buffer bug"}),
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/PathConverter.cpp"}),
			{Message: Message{Role: "assistant", Text: "로컬 소스 기준으로 검토를 계속했습니다."}},
		},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{approvedReviewResponse("no blocking code findings")},
	}
	agent := &Agent{
		Config:         Config{},
		Client:         provider,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Tools:          NewToolRegistry(webTool, NewReadFileTool(ws)),
		Workspace:      ws,
		Session:        session,
		Store:          store,
		MCP: &MCPManager{
			servers: []*MCPClient{
				{
					config: MCPServerConfig{Name: "web"},
					tools: []MCPToolDescriptor{
						{Name: "search_web", Description: "Search the web for current articles and references"},
					},
				},
			},
		},
	}

	reply, err := agent.Reply(context.Background(), "검증 명령을 실행해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "로컬 소스") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if webTool.calls != 0 {
		t.Fatalf("local code repair should block web research tool execution, got %d calls", webTool.calls)
	}
	if chatRequestHasTool(provider.requests[0], "mcp__web__search_web") {
		t.Fatalf("local code repair should not expose web research tools in the first model request")
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected web tool call to be blocked and retried, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "로컬 코드 리뷰/수정 작업") {
		t.Fatalf("expected local-code web block guidance, got %#v", lastMessage)
	}
	repeatedGuidance := provider.requests[2].Messages[len(provider.requests[2].Messages)-1]
	if repeatedGuidance.Role != "user" || !strings.Contains(repeatedGuidance.Text, "로컬 코드 리뷰/수정 작업") {
		t.Fatalf("expected repeated web call to be blocked again, got %#v", repeatedGuidance)
	}
}

func TestAgentBlocksNamespacedWebResearchForLocalCodeRepairWithoutMCPCatalog(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "SampleApp", "SampleWorker", "PathConverter.cpp")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("int ConvertPath()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	webTool := &staticTool{
		name:   "mcp__web_research__search_web",
		output: "external source",
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("mcp__web_research__search_web", map[string]any{"query": "Win32 volume API bug"}),
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/PathConverter.cpp"}),
			{Message: Message{Role: "assistant", Text: "로컬 소스 근거로만 계속했습니다."}},
		},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{approvedReviewResponse("no blocking code findings")},
	}
	agent := &Agent{
		Config:         Config{},
		Client:         provider,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Tools:          NewToolRegistry(webTool, NewReadFileTool(ws)),
		Workspace:      ws,
		Session:        session,
		Store:          store,
	}

	reply, err := agent.Reply(context.Background(), "검증 명령을 실행해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "로컬 소스") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if webTool.calls != 0 {
		t.Fatalf("namespaced web research should be blocked even without MCP catalog, got %d calls", webTool.calls)
	}
	if chatRequestHasTool(provider.requests[0], "mcp__web_research__search_web") {
		t.Fatalf("local code repair should hide namespaced web research tools even without MCP catalog")
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected blocked web call to be retried, got %d requests", len(provider.requests))
	}
	guidance := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if guidance.Role != "user" || !strings.Contains(guidance.Text, "로컬 코드 리뷰/수정 작업") {
		t.Fatalf("expected Korean local-code web block guidance, got %#v", guidance)
	}
}

func TestAgentBlocksWebResearchWhenLocalCodeContextComesFromToolTranscript(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "SampleApp", "SampleWorker", "PathConverter.cpp")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("int ConvertPath()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	session.Messages = append(session.Messages,
		Message{Role: "user", Text: "@SampleApp/SampleWorker/PathConverter.cpp:1-3 검토하고 버그를 수정해"},
		Message{
			Role:     "tool",
			ToolName: "read_file",
			Text:     "read_file loaded SampleApp/SampleWorker/PathConverter.cpp",
			ToolMeta: map[string]any{"path": "SampleApp/SampleWorker/PathConverter.cpp"},
		},
	)
	for i := 0; i < 16; i++ {
		session.Messages = append(session.Messages, Message{Role: "user", Text: fmt.Sprintf("계속 진행 %d", i)})
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	webTool := &staticTool{
		name:   "mcp__web_research__search_web",
		output: "external source",
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("mcp__web_research__search_web", map[string]any{"query": "Microsoft Learn FindFirstVolume"}),
			{Message: Message{Role: "assistant", Text: "로컬 도구 기록을 기준으로 계속했습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(webTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "계속 진행해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "로컬 도구") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if webTool.calls != 0 {
		t.Fatalf("web research should be blocked from local tool transcript context, got %d calls", webTool.calls)
	}
	if chatRequestHasTool(provider.requests[0], "mcp__web_research__search_web") {
		t.Fatalf("local code context from tool transcript should hide namespaced web research tools")
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected web tool call to be blocked and retried once, got %d requests", len(provider.requests))
	}
	guidance := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if guidance.Role != "user" || !strings.Contains(guidance.Text, "로컬 코드 리뷰/수정 작업") {
		t.Fatalf("expected Korean local-code web block guidance, got %#v", guidance)
	}
}

func TestAgentKeepsLocalCodeWebBlockStickyAcrossRepairLoop(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "SampleApp", "SampleWorker", "PathConverter.cpp")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("int ConvertPath()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	webTool := &staticTool{
		name:   "mcp__web_research__fetch_url",
		output: "external source",
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("update_plan", map[string]any{"items": []any{
				map[string]any{"step": "Gather external API behavior evidence", "status": "in_progress"},
				map[string]any{"step": "Repair local file", "status": "pending"},
			}}),
			toolCallResponse("mcp__web_research__fetch_url", map[string]any{"url": "https://learn.microsoft.com/windows/win32/api/fileapi/nf-fileapi-findfirstvolumew"}),
			{Message: Message{Role: "assistant", Text: "로컬 코드 수리 맥락을 유지했습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(webTool, NewUpdatePlanTool(ws), NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "검증 명령을 실행해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "로컬 코드") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if webTool.calls != 0 {
		t.Fatalf("web research should be blocked throughout a local repair loop, got %d calls", webTool.calls)
	}
	if chatRequestHasTool(provider.requests[1], "mcp__web_research__fetch_url") {
		t.Fatalf("local repair turn should keep namespaced web research hidden after prior model plan")
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected plan, blocked web call, final reply; got %d requests", len(provider.requests))
	}
	guidance := provider.requests[2].Messages[len(provider.requests[2].Messages)-1]
	if guidance.Role != "user" ||
		(!strings.Contains(guidance.Text, "local code review or repair request") &&
			!strings.Contains(guidance.Text, "로컬 코드 리뷰/수정 작업")) {
		t.Fatalf("expected local-code block guidance, got %#v", guidance)
	}
}

func TestAgentBlocksVerificationRetryAndPollAfterDeclineInSameTurn(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "scripted", "main-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	confirmCount := 0
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
		ConfirmVerification: func(plan VerificationPlan) (bool, error) {
			confirmCount++
			return false, nil
		},
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("run_shell_background", map[string]any{"command": "go test ./..."}),
			toolCallResponse("check_shell_job", map[string]any{"job_id": "latest"}),
			toolCallResponse("run_shell", map[string]any{"command": "Write-Output hello"}),
			toolCallResponse("run_shell", map[string]any{"command": "go test ./..."}),
			{Message: Message{Role: "assistant", Text: "검증은 사용자 거절로 실행하지 않았고 재시도하지 않았습니다."}},
		},
	}
	agent := &Agent{
		Config: Config{},
		Client: provider,
		Tools: NewToolRegistry(
			NewRunBackgroundShellTool(ws),
			NewCheckShellJobTool(ws),
			NewRunShellTool(ws),
		),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "검증 명령을 실행해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "재시도하지") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected follow-up request after declined verification, got %d", len(provider.requests))
	}
	for i, req := range provider.requests[1:] {
		for _, toolName := range []string{"run_shell", "run_shell_background"} {
			if !chatRequestHasTool(req, toolName) {
				t.Fatalf("request %d should keep %s available; verification-like calls are blocked by arguments at execution time", i+2, toolName)
			}
		}
		if !chatRequestHasTool(req, "check_shell_job") {
			t.Fatalf("request %d should keep check_shell_job available for already-running non-verification jobs", i+2)
		}
	}
	if confirmCount != 1 {
		t.Fatalf("declined verification should not prompt again for retry or poll, got %d prompts", confirmCount)
	}
	blockedCount := 0
	unavailablePollCount := 0
	readOnlyShellCount := 0
	for _, msg := range session.Messages {
		if msg.Role == "tool" && strings.Contains(msg.Text, "NOT_EXECUTED: a build, test, or verification command was already skipped") {
			blockedCount++
			if msg.IsError {
				t.Fatalf("blocked verification follow-up should be a skipped non-error status: %#v", msg)
			}
			if toolMetaBool(msg.ToolMeta, "success") {
				t.Fatalf("blocked verification follow-up must not be successful evidence: %#v", msg.ToolMeta)
			}
			if toolMetaBool(msg.ToolMeta, "verification_evidence") {
				t.Fatalf("blocked verification follow-up must not count as verification evidence: %#v", msg.ToolMeta)
			}
		}
		if msg.Role == "tool" && msg.ToolName == "check_shell_job" && strings.Contains(msg.Text, "no background shell job is available") {
			unavailablePollCount++
			if msg.IsError {
				t.Fatalf("checking for a missing background job after declined verification should be a non-error status: %#v", msg)
			}
			if toolMetaBool(msg.ToolMeta, "verification_evidence") {
				t.Fatalf("missing background job status must not count as verification evidence: %#v", msg.ToolMeta)
			}
		}
		if msg.Role == "tool" && msg.ToolName == "run_shell" && strings.Contains(msg.Text, "hello") {
			readOnlyShellCount++
			if msg.IsError {
				t.Fatalf("non-verification run_shell should be allowed after declined verification: %#v", msg)
			}
		}
	}
	if blockedCount != 2 {
		t.Fatalf("expected verification poll and run_shell retry to be blocked, got %d", blockedCount)
	}
	if unavailablePollCount != 0 {
		t.Fatalf("missing latest background poll after declined verification should be blocked before polling, got %d unavailable poll result(s)", unavailablePollCount)
	}
	if readOnlyShellCount != 1 {
		t.Fatalf("expected one allowed non-verification run_shell after declined verification, got %d", readOnlyShellCount)
	}
}

func TestAgentAllowsNonVerificationBackgroundPollAfterDeclinedVerification(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.UpsertBackgroundJob(BackgroundShellJob{
		ID:             "job-readonly",
		Command:        "Write-Output hello",
		CommandSummary: "Write-Output hello",
		Status:         "completed",
		MutationClass:  string(shellMutationReadOnly),
		StartedAt:      time.Now(),
	})
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	confirmCount := 0
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
		ConfirmVerification: func(plan VerificationPlan) (bool, error) {
			confirmCount++
			return false, nil
		},
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("run_shell_background", map[string]any{"command": "go test ./..."}),
			toolCallResponse("check_shell_job", map[string]any{"job_id": "job-readonly"}),
			{Message: Message{Role: "assistant", Text: "기존 비검증 작업 상태를 확인했고 검증은 실행하지 않았습니다."}},
		},
	}
	agent := &Agent{
		Config: Config{},
		Client: provider,
		Tools: NewToolRegistry(
			NewRunBackgroundShellTool(ws),
			NewCheckShellJobTool(ws),
		),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@SampleApp/SampleWorker/PathConverter.cpp:1-3 검토하고 버그를 수정해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "비검증 작업") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if confirmCount != 1 {
		t.Fatalf("declined verification should only prompt once, got %d", confirmCount)
	}
	foundPoll := false
	for _, msg := range session.Messages {
		if msg.Role == "tool" && msg.ToolName == "check_shell_job" && strings.Contains(msg.Text, "job: job-readonly") {
			foundPoll = true
			if msg.IsError {
				t.Fatalf("non-verification background poll should be allowed, got error message: %#v", msg)
			}
		}
	}
	if !foundPoll {
		t.Fatalf("expected non-verification background poll result in session")
	}
}

func TestAgentBlocksVerificationBackgroundPollAfterDeclinedVerification(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.UpsertBackgroundJob(BackgroundShellJob{
		ID:             "job-verification",
		Command:        "go test ./...",
		CommandSummary: "go test ./...",
		Status:         "running",
		MutationClass:  string(shellMutationVerificationArtifacts),
		StartedAt:      time.Now(),
	})
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	confirmCount := 0
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
		ConfirmVerification: func(plan VerificationPlan) (bool, error) {
			confirmCount++
			return false, nil
		},
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("run_shell_background", map[string]any{"command": "go test ./..."}),
			toolCallResponse("check_shell_job", map[string]any{"job_id": "job-verification"}),
			{Message: Message{Role: "assistant", Text: "검증 작업 조회는 재시도하지 않았습니다."}},
		},
	}
	agent := &Agent{
		Config: Config{},
		Client: provider,
		Tools: NewToolRegistry(
			NewRunBackgroundShellTool(ws),
			NewCheckShellJobTool(ws),
		),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@SampleApp/SampleWorker/PathConverter.cpp:1-3 검토하고 버그를 수정해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.Contains(reply, "tool connectivity") || strings.Contains(reply, "failing to return") {
		t.Fatalf("reply should not blame skipped verification on tool availability: %q", reply)
	}
	if confirmCount != 1 {
		t.Fatalf("declined verification should only prompt once, got %d", confirmCount)
	}
	blockedCount := 0
	for _, msg := range session.Messages {
		if msg.Role == "tool" && msg.ToolName == "check_shell_job" && strings.Contains(msg.Text, "NOT_EXECUTED: a build, test, or verification command was already skipped") {
			blockedCount++
			if msg.IsError {
				t.Fatalf("verification background poll should be a skipped non-error tool result: %#v", msg)
			}
			if got := fmt.Sprint(msg.ToolMeta["result_class"]); got != "verification_skipped" {
				t.Fatalf("expected verification_skipped result class, got %q in %#v", got, msg.ToolMeta)
			}
		}
	}
	if blockedCount != 1 {
		t.Fatalf("expected verification background poll to be blocked once, got %d", blockedCount)
	}
}

func TestToolCallIsVerificationRetryOrPollUsesBackgroundMetadata(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "main-model", "", "default")
	session.UpsertBackgroundJob(BackgroundShellJob{
		ID:             "job-readonly",
		Command:        "Write-Output hello",
		CommandSummary: "Write-Output hello",
		Status:         "completed",
		MutationClass:  string(shellMutationReadOnly),
		StartedAt:      time.Now(),
	})
	session.UpsertBackgroundJob(BackgroundShellJob{
		ID:             "job-verification",
		Command:        "go test ./...",
		CommandSummary: "go test ./...",
		Status:         "running",
		MutationClass:  string(shellMutationVerificationArtifacts),
		StartedAt:      time.Now(),
	})

	readOnlyArgs, _ := json.Marshal(map[string]any{"job_id": "job-readonly"})
	verificationArgs, _ := json.Marshal(map[string]any{"job_id": "job-verification"})
	if toolCallIsVerificationRetryOrPoll(ToolCall{Name: "check_shell_job", Arguments: string(readOnlyArgs)}, session) {
		t.Fatalf("read-only background job poll must not be treated as verification retry")
	}
	if !toolCallIsVerificationRetryOrPoll(ToolCall{Name: "check_shell_job", Arguments: string(verificationArgs)}, session) {
		t.Fatalf("verification background job poll should be treated as verification retry")
	}

	emptySession := NewSession(t.TempDir(), "scripted", "main-model", "", "default")
	latestArgs, _ := json.Marshal(map[string]any{"job_id": "latest"})
	if !toolCallIsVerificationRetryOrPoll(ToolCall{Name: "check_shell_job", Arguments: string(latestArgs)}, emptySession) {
		t.Fatalf("latest background poll with no job after declined verification should be blocked before emitting a misleading poll")
	}
}

func TestLatestEditProposalForUserDecisionSkipsIncludeOnlyWhenReviewNeedsCodePatch(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "main-model", "", "default")
	meaningfulPatch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: sample.cpp",
		"@@",
		"-\treturn false;",
		"+\treturn true;",
		"*** End Patch",
	}, "\n")
	includeOnlyPatch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: sample.cpp",
		"@@",
		"+#include <vector>",
		"*** End Patch",
	}, "\n")
	session.AddMessage(Message{Role: "assistant", Text: "최신 리뷰 결과:\n- RF-001: 함수 본문을 수정해야 합니다."})
	session.AddMessage(toolCallResponse("apply_patch", map[string]any{"patch": meaningfulPatch}).Message)
	session.AddMessage(toolCallResponse("apply_patch", map[string]any{"patch": includeOnlyPatch}).Message)
	session.LastReviewRun = &ReviewRun{
		RepairFindings: []ReviewFinding{{
			ID:          "RF-001",
			Severity:    reviewSeverityMedium,
			Category:    "correctness",
			Title:       "Loop body still exits early",
			Evidence:    "The function body still contains the old branch.",
			RequiredFix: "Patch the control flow in the function body.",
			BlocksGate:  true,
			Quality:     reviewFindingQualityComplete,
		}},
	}

	toolName, proposal := latestEditToolProposalForUserDecision(session)
	if toolName != "apply_patch" {
		t.Fatalf("expected apply_patch proposal, got %q", toolName)
	}
	if !strings.Contains(proposal, "return true") {
		t.Fatalf("expected previous meaningful patch, got %q", proposal)
	}
	if strings.Contains(proposal, "#include <vector>") {
		t.Fatalf("include-only follow-up should not hide the meaningful blocked patch: %q", proposal)
	}
}

func TestLatestEditProposalForUserDecisionScansWithoutReviewBoundary(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "main-model", "", "default")
	meaningfulPatch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: sample.cpp",
		"@@",
		"-\treturn false;",
		"+\treturn true;",
		"*** End Patch",
	}, "\n")
	includeOnlyPatch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: sample.cpp",
		"@@",
		"+#include <vector>",
		"*** End Patch",
	}, "\n")
	session.AddMessage(Message{Role: "user", Text: "Fix the pre-write review finding."})
	session.AddMessage(toolCallResponse("apply_patch", map[string]any{"patch": meaningfulPatch}).Message)
	session.AddMessage(toolCallResponse("apply_patch", map[string]any{"patch": includeOnlyPatch}).Message)
	session.LastReviewRun = &ReviewRun{
		RepairFindings: []ReviewFinding{{
			ID:          "RF-001",
			Severity:    reviewSeverityMedium,
			Category:    "correctness",
			Title:       "Loop body still exits early",
			Evidence:    "The function body still contains the old branch.",
			RequiredFix: "Patch the control flow in the function body.",
			BlocksGate:  true,
			Quality:     reviewFindingQualityComplete,
		}},
	}

	_, proposal := latestEditToolProposalForUserDecision(session)
	if !strings.Contains(proposal, "return true") {
		t.Fatalf("expected previous meaningful patch without a review boundary, got %q", proposal)
	}
	if strings.Contains(proposal, "#include <vector>") {
		t.Fatalf("include-only follow-up should not hide the meaningful blocked patch: %q", proposal)
	}
}

func TestProposalLooksIncludeOnlyIgnoresDiffContextLines(t *testing.T) {
	proposal := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: sample.cpp",
		"@@",
		" void Existing()",
		" {",
		" \treturn;",
		" }",
		"+#include <vector>",
		"*** End Patch",
	}, "\n")
	if !proposalLooksIncludeOnly(proposal) {
		t.Fatalf("diff context lines with leading whitespace must not be treated as changed code")
	}
}

func TestProposalLooksIncludeOnlyRejectsActualCodeChanges(t *testing.T) {
	proposal := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: sample.cpp",
		"@@",
		"+#include <vector>",
		"-\treturn false;",
		"+\treturn true;",
		"*** End Patch",
	}, "\n")
	if proposalLooksIncludeOnly(proposal) {
		t.Fatalf("actual +/- code lines at diff column zero must not be classified as include-only")
	}
}

func TestLatestEditProposalForUserDecisionDoesNotUseStaleProposalBeforeReviewBoundary(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "main-model", "", "default")
	stalePatch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: old.cpp",
		"@@",
		"-\treturn false;",
		"+\treturn true;",
		"*** End Patch",
	}, "\n")
	includeOnlyPatch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: sample.cpp",
		"@@",
		"+#include <vector>",
		"*** End Patch",
	}, "\n")
	session.AddMessage(toolCallResponse("apply_patch", map[string]any{"patch": stalePatch}).Message)
	session.AddMessage(Message{Role: "assistant", Text: "최신 리뷰 결과:\n- RF-001: 현재 함수 본문을 수정해야 합니다."})
	session.AddMessage(toolCallResponse("apply_patch", map[string]any{"patch": includeOnlyPatch}).Message)
	session.LastReviewRun = &ReviewRun{
		RepairFindings: []ReviewFinding{{
			ID:          "RF-001",
			Severity:    reviewSeverityMedium,
			Category:    "correctness",
			Title:       "Function body still exits early",
			Evidence:    "The current function body still contains the old branch.",
			RequiredFix: "Patch the control flow in the function body.",
			BlocksGate:  true,
			Quality:     reviewFindingQualityComplete,
		}},
	}

	_, proposal := latestEditToolProposalForUserDecision(session)
	if strings.Contains(proposal, "old.cpp") || strings.Contains(proposal, "return true") {
		t.Fatalf("stale proposal before the latest review boundary should not be reused: %q", proposal)
	}
	if !strings.Contains(proposal, "#include <vector>") {
		t.Fatalf("expected latest in-boundary proposal when no meaningful in-boundary patch exists, got %q", proposal)
	}
}

func TestLatestEditProposalForUserDecisionKeepsIncludeOnlyWhenReviewNeedsInclude(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "main-model", "", "default")
	meaningfulPatch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: sample.cpp",
		"@@",
		"-\treturn false;",
		"+\treturn true;",
		"*** End Patch",
	}, "\n")
	includeOnlyPatch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: sample.cpp",
		"@@",
		"+#include <memory>",
		"*** End Patch",
	}, "\n")
	session.AddMessage(Message{Role: "assistant", Text: "최신 리뷰 결과:\n- RF-include: include를 추가해야 합니다."})
	session.AddMessage(toolCallResponse("apply_patch", map[string]any{"patch": meaningfulPatch}).Message)
	session.AddMessage(toolCallResponse("apply_patch", map[string]any{"patch": includeOnlyPatch}).Message)
	session.LastReviewRun = &ReviewRun{
		RepairFindings: []ReviewFinding{{
			ID:          "RF-include",
			Title:       "std::unique_ptr include is missing",
			Evidence:    "The latest code uses std::unique_ptr.",
			RequiredFix: "Add #include <memory> and resubmit the patch.",
			BlocksGate:  true,
		}},
	}

	_, proposal := latestEditToolProposalForUserDecision(session)
	if !strings.Contains(proposal, "#include <memory>") {
		t.Fatalf("include-only blocker should keep the latest include proposal, got %q", proposal)
	}
	if strings.Contains(proposal, "return true") {
		t.Fatalf("include-only blocker should not restore an older function-body proposal: %q", proposal)
	}
}

func TestExplicitWebResearchRequestOverridesRecentLocalCodeContext(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "@SampleApp/SampleWorker/PathConverter.cpp:1-3 검토하고 버그를 수정해"})
	session.AddMessage(Message{Role: "assistant", Text: "로컬 코드 수정을 검토했습니다."})
	store := NewSessionStore(filepath.Join(root, "sessions"))
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	webTool := &staticTool{
		name:   "mcp__web_research__search_web",
		output: "fresh source",
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("mcp__web_research__search_web", map[string]any{"query": "latest Windows storage API docs"}),
			{Message: Message{Role: "assistant", Text: "웹 리서치 결과를 확인했습니다."}},
		},
	}
	agent := &Agent{
		Config: Config{},
		Client: provider,
		Tools:  NewToolRegistry(webTool),
		Workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
		Session: session,
		Store:   store,
		MCP: &MCPManager{
			servers: []*MCPClient{{
				config: MCPServerConfig{Name: "web_research"},
				tools: []MCPToolDescriptor{{
					Name:        "search_web",
					Description: "Search the web for current articles and references",
				}},
			}},
		},
	}

	reply, err := agent.Reply(context.Background(), "최신 Windows storage API 문서를 웹에서 검색해서 알려줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "웹 리서치") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if webTool.calls != 1 {
		t.Fatalf("explicit web research request should execute the web tool once, got %d", webTool.calls)
	}
	if len(provider.requests) == 0 || !chatRequestHasTool(provider.requests[0], "mcp__web_research__search_web") {
		t.Fatalf("explicit web research request should expose web tool despite earlier local-code context")
	}
}

func TestLocalCodeToolPolicyDoesNotStickToLaterNonCodeRequest(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "main-model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "@Source/Sample.cpp:1-20 검토하고 버그를 수정해"})
	session.AddMessage(Message{Role: "assistant", Text: "로컬 코드를 수정했습니다."})
	session.LastReviewRun = &ReviewRun{
		Trigger:   reviewBeforeFixTrigger,
		Objective: "@Source/Sample.cpp:1-20 검토하고 버그를 수정해",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"Source/Sample.cpp"},
		},
	}
	session.AddMessage(Message{Role: "user", Text: "방금 결과를 짧게 요약해줘"})

	if shouldUseLocalCodeToolPolicy(session) {
		t.Fatalf("previous local-code context should not force current non-code request into local-code policy")
	}
}

func TestLocalCodeToolPolicyAllowsCurrentRepairContinuation(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		Trigger:   reviewBeforeFixTrigger,
		Objective: "@Source/Sample.cpp:1-20 검토하고 버그를 수정해",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"Source/Sample.cpp"},
		},
	}
	session.AddMessage(Message{Role: "user", Text: "계속 수정해\n\nPending review repair confirmation:\n- Continue from the latest review findings."})

	if !shouldUseLocalCodeToolPolicy(session) {
		t.Fatalf("explicit current repair continuation should keep local-code policy")
	}
}

func TestLocalCodeToolPolicyCoversWorkspaceFileProblemReport(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "main-model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "각 파일들을 분석해서 문제점을 찾아서 별도 문서로 생성해"})

	if !shouldUseLocalCodeToolPolicy(session) {
		t.Fatalf("workspace file problem report should keep local-code policy")
	}
}

func TestAgentHidesWebResearchForWorkspaceFileProblemReport(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Sample.cpp"), []byte("int main()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	webTool := &staticTool{
		name:   "mcp__web_research__search_web",
		output: "external source",
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{"path": "."}),
			{Message: Message{Role: "assistant", Text: "로컬 파일 목록을 기준으로 문서 생성을 계속합니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(webTool, NewListFilesTool(ws), NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		MCP: &MCPManager{
			servers: []*MCPClient{{
				config: MCPServerConfig{Name: "web_research"},
				tools: []MCPToolDescriptor{{
					Name:        "search_web",
					Description: "Search the web for current articles and references",
				}},
			}},
		},
	}

	reply, err := agent.Reply(context.Background(), "각 파일들을 분석해서 문제점을 찾아서 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "로컬 파일") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if webTool.calls != 0 {
		t.Fatalf("workspace file problem report should not execute web research, got %d calls", webTool.calls)
	}
	if len(provider.requests) == 0 || chatRequestHasTool(provider.requests[0], "mcp__web_research__search_web") {
		t.Fatalf("workspace file problem report should hide web research tools")
	}
}

func TestAgentRetriesFinalReplyThatBlamesSkippedVerificationOnToolAvailability(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "scripted", "main-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	confirmCount := 0
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
		ConfirmVerification: func(plan VerificationPlan) (bool, error) {
			confirmCount++
			return false, nil
		},
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("run_shell_background", map[string]any{"command": "go test ./..."}),
			toolCallResponse("run_shell_background", map[string]any{"command": "go test ./..."}),
			{Message: Message{Role: "assistant", Text: "Blocked by tool availability/session errors.\n\n- The requested MCP tools (`mcp__web_research__search_web`) are not exposed in the callable tool namespace.\n- Local inspection/verification tools are currently returning transcript-recovery errors.\n\nBest next action: enable/expose the MCP web-research tools."}},
			{Message: Message{Role: "assistant", Text: "검증은 사용자 승인 없이 실행하지 않았고 재시도하지 않았습니다. 코드 변경 요약과 미검증 위험만 보고합니다."}},
		},
	}
	var progress []string
	agent := &Agent{
		Config: Config{},
		Client: provider,
		Tools: NewToolRegistry(
			NewRunBackgroundShellTool(ws),
		),
		Workspace: ws,
		Session:   session,
		Store:     store,
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
	}

	reply, err := agent.Reply(context.Background(), "@SampleApp/SampleWorker/PathConverter.cpp:1-3 검토하고 버그를 수정해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.Contains(strings.ToLower(reply), "mcp") || strings.Contains(strings.ToLower(reply), "web-research") {
		t.Fatalf("tool availability blame leaked to final reply: %q", reply)
	}
	if !strings.Contains(reply, "검증은") || !strings.Contains(reply, "실행하지") {
		t.Fatalf("expected final reply to disclose skipped verification, got %q", reply)
	}
	if confirmCount != 1 {
		t.Fatalf("declined verification should only prompt once, got %d", confirmCount)
	}
	approvalProgressCount := 0
	for _, message := range progress {
		if strings.Contains(message, "Requesting background verification approval") || strings.Contains(message, "백그라운드 검증 승인 확인") {
			approvalProgressCount++
		}
	}
	if approvalProgressCount != 1 {
		t.Fatalf("declined verification should only emit one approval progress message, got %d: %#v", approvalProgressCount, progress)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected retry after bad final reply, got %d requests", len(provider.requests))
	}
	foundGuidance := false
	for _, msg := range session.Messages {
		if msg.Role == "user" && strings.Contains(msg.Text, "도구 가용성/세션 장애로 잘못 해석") {
			foundGuidance = true
			break
		}
	}
	if !foundGuidance {
		t.Fatalf("expected skipped-verification tool-availability guidance in session")
	}
	if !scriptedRequestsContainText(provider.requests, "같은 검증을 위해 run_shell") {
		t.Fatalf("expected blocked verification retry guidance before final reply, got %#v", provider.requests)
	}
}

func TestDeclinedVerificationFollowupDoesNotRecordBackgroundStartOrFailure(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	call := ToolCall{
		Name:      "run_shell_background",
		Arguments: `{"command":"go test ./..."}`,
	}
	agent.noteToolExecutionResultDetailed(call, declinedVerificationFollowupBlockedResult(call), nil)

	if session.TaskState == nil {
		t.Fatalf("expected task state")
	}
	for _, failed := range session.TaskState.FailedAttempts {
		if strings.Contains(strings.ToLower(failed), "verification") || strings.Contains(strings.ToLower(failed), "go test") {
			t.Fatalf("declined verification follow-up must not be a failed attempt: %#v", session.TaskState.FailedAttempts)
		}
	}
	for _, event := range session.TaskState.Events {
		if event.Kind == "background_start" {
			t.Fatalf("declined verification follow-up must not be recorded as a background start: %#v", session.TaskState.Events)
		}
	}
	foundSkipped := false
	for _, event := range session.TaskState.Events {
		if event.Kind == "verification_skipped" && event.Status == "skipped" {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Fatalf("expected skipped verification event, got %#v", session.TaskState.Events)
	}
}

func TestAgentDoesNotRewriteToolAvailabilityReplyAsSkippedVerificationWithoutSkippedVerification(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "scripted", "main-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "Blocked by tool availability/session errors.\n\n- The requested MCP tools are not exposed in the callable tool namespace.\n- Local inspection tools are currently unavailable."}},
		},
	}
	agent := &Agent{
		Config: Config{},
		Client: provider,
		Tools:  NewToolRegistry(),
		Workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
		Session: session,
		Store:   store,
	}

	reply, err := agent.Reply(context.Background(), "@SampleApp/SampleWorker/PathConverter.cpp:1-3 검토하고 버그를 수정해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "tool availability") {
		t.Fatalf("expected original tool-availability reply to remain visible, got %q", reply)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("tool-availability reply should not be rewritten as skipped verification without a skipped verification event, got %d requests", len(provider.requests))
	}
	for _, msg := range session.Messages {
		if msg.Role == "user" && strings.Contains(msg.Text, "검증 생략 상태") {
			t.Fatalf("unexpected skipped-verification guidance without skipped verification: %#v", msg)
		}
	}
}

func TestAgentRetriesLocalCodeToolAvailabilityBlameWhenInspectionToolsExist(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "SampleApp", "SampleWorker", "PathConverter.cpp")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("int ConvertPath()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "scripted", "main-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ws := Workspace{BaseRoot: root, Root: root}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "현재 읽기 전용 분석 모드에서 외부 웹 연구 도구가 차단되어 있으며, 이로 인해 로컬 파일 분석 도구도 함께 차단된 상태입니다.\n\n해결 방안: MCP web-research 도구를 허용 목록에 추가해야 합니다."}},
			{Message: Message{Role: "assistant", Text: "로컬 검사 도구로 계속 진행할 수 있습니다. 웹 리서치 활성화는 필요하지 않습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewListFilesTool(ws), NewGrepTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@SampleApp/SampleWorker/PathConverter.cpp 코드가 왜 실패하는지 분석해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.Contains(reply, "허용 목록") || strings.Contains(strings.ToLower(reply), "web-research") {
		t.Fatalf("local-code tool availability blame leaked to final reply: %q", reply)
	}
	if !strings.Contains(reply, "로컬 검사 도구") {
		t.Fatalf("expected corrected local inspection reply, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected one retry after local-code tool availability blame, got %d requests", len(provider.requests))
	}
	guidance := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if guidance.Role != "user" ||
		!strings.Contains(guidance.Text, "전체 도구 장애") ||
		!strings.Contains(guidance.Text, "read_file") {
		t.Fatalf("expected local-code tool availability guidance, got %#v", guidance)
	}
}

func TestAgentBlocksWebResearchAfterPreWriteReviewFeedback(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "SampleApp", "SampleWorker", "PathConverter.cpp")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("int ConvertPath()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	webTool := &staticTool{
		name:   "mcp__web_research__search_web",
		output: "external source",
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("mcp__web_research__search_web", map[string]any{"query": "C++ loop bug fix"}),
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/PathConverter.cpp"}),
			{Message: Message{Role: "assistant", Text: "pre-write review 경고를 로컬 소스 기준으로 다시 수정했습니다."}},
		},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{approvedReviewResponse("no blocking code findings")},
	}
	agent := &Agent{
		Config:         Config{},
		Client:         provider,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Tools:          NewToolRegistry(webTool, NewReadFileTool(ws)),
		Workspace:      ws,
		Session:        session,
		Store:          store,
		MCP: &MCPManager{
			servers: []*MCPClient{
				{
					config: MCPServerConfig{Name: "web_research"},
					tools: []MCPToolDescriptor{
						{Name: "search_web", Description: "Search the web for current articles and references"},
					},
				},
			},
		},
	}

	reply, err := agent.Reply(context.Background(), "Automatic pre-write review found actionable warnings. Revise the proposed edit before writing files.\n\nImplementation rules:\n- This is local code review/repair work. Do not use MCP web/search/browser tools or external web research to satisfy this gate.")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "로컬 소스") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if webTool.calls != 0 {
		t.Fatalf("pre-write review repair should block web research tool execution, got %d calls", webTool.calls)
	}
	if chatRequestHasTool(provider.requests[0], "mcp__web_research__search_web") {
		t.Fatalf("pre-write local repair feedback should not expose web research tools")
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected web tool call to be blocked and retried, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" ||
		(!strings.Contains(lastMessage.Text, "local code review or repair request") &&
			!strings.Contains(lastMessage.Text, "로컬 코드 리뷰/수정 작업")) {
		t.Fatalf("expected local-code web block guidance after pre-write feedback, got %#v", lastMessage)
	}
}

func TestAgentKeepsWebResearchHiddenAfterEditTargetMismatch(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "SampleApp", "SampleWorker", "PathConverter.cpp")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("int ConvertPath()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	patchTool := &failingTool{
		name: "apply_patch",
		err:  fmt.Errorf("%w: exact_search text not found in SampleApp/SampleWorker/PathConverter.cpp", ErrEditTargetMismatch),
	}
	webTool := &staticTool{
		name:   "mcp__web_research__fetch_url",
		output: "external source",
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{"patch": "*** Begin Patch\n*** End Patch\n"}),
			toolCallResponse("mcp__web_research__fetch_url", map[string]any{"url": "https://learn.microsoft.com/windows/win32/api/fileapi/nf-fileapi-getvolumepathnamesforvolumenamew"}),
			{Message: Message{Role: "assistant", Text: "로컬 파일을 다시 기준으로 삼아 진행했습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, webTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}
	var progress []string
	agent.EmitProgress = func(message string) {
		progress = append(progress, message)
	}

	reply, err := agent.Reply(context.Background(), "@SampleApp/SampleWorker/PathConverter.cpp:1-3 검토하고 버그를 수정해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "로컬 파일") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if patchTool.calls != 1 {
		t.Fatalf("expected one failed patch attempt, got %d", patchTool.calls)
	}
	if webTool.calls != 0 {
		t.Fatalf("web research should stay blocked after edit target mismatch, got %d calls", webTool.calls)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected patch failure, web block guidance, then final reply; got %d requests", len(provider.requests))
	}
	if chatRequestHasTool(provider.requests[1], "mcp__web_research__fetch_url") {
		t.Fatalf("web research tool should stay hidden after edit target mismatch guidance")
	}
	if indexStringContaining(progress, "getvolumepathnamesforvolumenamew") < 0 {
		t.Fatalf("expected blocked web research progress to include requested lookup intent, got %#v", progress)
	}
	guidance := provider.requests[2].Messages[len(provider.requests[2].Messages)-1]
	if guidance.Role != "user" ||
		(!strings.Contains(guidance.Text, "local code review or repair request") &&
			!strings.Contains(guidance.Text, "로컬 코드 리뷰/수정 작업")) {
		t.Fatalf("expected local-code web block guidance after stale patch retry, got %#v", guidance)
	}
}

func TestAgentContinuesAfterWeakPreFixCrossReviewerAndStillBlocksWebResearch(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "SampleApp", "SampleWorker", "PathConverter.cpp")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("int ConvertPath()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := Config{AutoLocale: boolPtr(false)}
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	webTool := &staticTool{
		name:   "mcp__web_research__search_web",
		output: "external source",
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: needs_revision",
				"summary: main model found a local correctness issue",
				"findings:",
				"- severity: high",
				"  title: Missing path guard",
				"  category: correctness",
				"  path: SampleApp/SampleWorker/PathConverter.cpp",
				"  evidence: ConvertPath returns 0 without validating the input path",
				"  impact: invalid paths can be accepted",
				"  required_fix: validate the path before returning success",
				"  test_recommendation: add an invalid path test",
			}, "\n")}},
			toolCallResponse("mcp__web_research__search_web", map[string]any{"query": "Microsoft Learn FindFirstVolume"}),
			{Message: Message{Role: "assistant", Text: "로컬 소스 기준으로 다시 진행했습니다."}},
		},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: "I cannot produce a structured review from the supplied context."},
		}},
	}
	agent := &Agent{
		Config:         cfg,
		Client:         provider,
		ReviewerClient: reviewer,
		ReviewerModel:  "weak-reviewer",
		Tools:          NewToolRegistry(webTool, NewReadFileTool(ws)),
		Workspace:      ws,
		Session:        session,
		Store:          store,
	}

	reply, err := agent.Reply(context.Background(), "@SampleApp/SampleWorker/PathConverter.cpp:1-3 검토하고 버그를 수정해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.Contains(reply, "리뷰어 게이트") || strings.Contains(reply, "코드 수정은 적용하지 않았습니다") {
		t.Fatalf("expected implementation loop to continue after weak cross reviewer, got %q", reply)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("main model should run first-pass review and implementation, got %d requests", len(provider.requests))
	}
	if webTool.calls != 0 {
		t.Fatalf("web research should still be blocked in local repair, got %d calls", webTool.calls)
	}
	if session.LastReviewRun == nil || session.LastReviewRun.Gate.Verdict != reviewVerdictNeedsRevision {
		t.Fatalf("expected main first-pass review findings to drive repair, got %#v", session.LastReviewRun)
	}
	if reviewRunHasRequiredReviewerFailure(*session.LastReviewRun) {
		t.Fatalf("weak pre-fix cross reviewer should not be a required reviewer failure, got %#v", session.LastReviewRun.Findings)
	}
}

func TestAgentStopsAfterPreWriteReviewerFailureWithoutWebResearchRetry(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	path := filepath.Join(root, "main.cpp")
	before := "int main()\n{\n    return 0;\n}\n"
	after := "int main()\n{\n    return 1;\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runTestGit(t, root, "add", "main.cpp")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")
	cfg := Config{AutoLocale: boolPtr(false)}
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	webTool := &staticTool{
		name:   "mcp__web_research__search_web",
		output: "external source",
	}
	planResponse := toolCallResponse("update_plan", map[string]any{"items": []any{
		map[string]any{"step": "Inspect main.cpp", "status": "completed"},
		map[string]any{"step": "Edit main.cpp", "status": "in_progress"},
	}})
	planResponse.StopReason = "tool_calls"
	writeResponse := toolCallResponse("write_file", map[string]any{"path": "main.cpp", "content": after})
	writeResponse.Message.Text = "I will update main.cpp."
	writeResponse.StopReason = "tool_calls"
	webResponse := toolCallResponse("mcp__web_research__search_web", map[string]any{"query": "Microsoft Learn GetVolumePathNamesForVolumeName"})
	webResponse.StopReason = "tool_calls"
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "1. Inspect main.cpp\n2. Update main.cpp\n3. Report the blocked or completed result"}},
			planResponse,
			writeResponse,
			webResponse,
		},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "APPROVED\nThe execution plan is sound."}},
			{Message: Message{Role: "assistant", Text: "I cannot produce a structured review from the supplied edit proposal."}},
		},
	}
	agent := &Agent{
		Config:         cfg,
		Client:         provider,
		ReviewerClient: reviewer,
		ReviewerModel:  "weak-reviewer",
		Session:        session,
		Store:          store,
	}
	reviewCalls := 0
	ws.UpdatePlan = func(items []PlanItem) {
		_ = items
	}
	ws.ReviewEdit = func(ctx context.Context, preview EditPreview) error {
		reviewCalls++
		return agent.reviewProposedEdit(ctx, preview)
	}
	agent.Workspace = ws
	agent.Tools = NewToolRegistry(NewUpdatePlanTool(ws), NewWriteFileTool(ws), webTool)

	reply, err := agent.Reply(context.Background(), "update main.cpp")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reviewCalls == 0 {
		t.Fatalf("expected pre-write review hook to run")
	}
	if !strings.Contains(reply, "Pre-write reviewer gate: not approved") ||
		!strings.Contains(reply, "no code changes were applied") ||
		!strings.Contains(reply, "[3] Next step") ||
		!strings.Contains(reply, "/review models") {
		t.Fatalf("expected reviewer-gate stop reply, got %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("implementation model should not get a retry turn after pre-write reviewer failure, got %d requests", len(provider.requests))
	}
	if webTool.calls != 0 {
		t.Fatalf("web research should not run after pre-write reviewer failure, got %d calls", webTool.calls)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != before {
		t.Fatalf("write should be blocked before touching disk, got %q", string(data))
	}
	if session.LastReviewRun == nil || session.LastReviewRun.Trigger != "pre_write" {
		t.Fatalf("expected pre-write review run, got %#v", session.LastReviewRun)
	}
	if !reviewRunHasRequiredReviewerFailure(*session.LastReviewRun) {
		t.Fatalf("expected required reviewer failure marker, got %#v", session.LastReviewRun.Findings)
	}
}

func TestAgentReportsReviewAndProposalAfterRepeatedPreWriteBlock(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		Trigger: "pre_write",
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-001"},
		},
		Result: ReviewResult{Summary: "Patch still misses the dynamic mount point buffer retry."},
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Severity:    reviewSeverityMedium,
			Category:    "correctness",
			Title:       "GetVolumePathNamesForVolumeName still uses a fixed buffer",
			RequiredFix: "Retry with a returnCch-sized dynamic buffer.",
			Quality:     reviewFindingQualityComplete,
		}},
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	patchTool := &failingTool{
		name: "apply_patch",
		err: fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\n" +
			"Automatic pre-write review found blockers.\n\nReview gate: needs_revision"),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{"patch": "*** Begin Patch\n*** End Patch\n"}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			toolCallResponse("apply_patch", map[string]any{"patch": "*** Begin Patch\n*** End Patch\n"}),
			{Message: Message{Role: "assistant", Text: "this response must not be requested after repeated pre-write review blocks"}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	for _, want := range []string{
		"did not pass the pre-write review",
		"Latest review result: needs_revision",
		"RF-001",
		"Latest edit proposal",
		"*** Begin Patch",
		"Should I keep repairing",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("expected repeated pre-write block reply to contain %q, got %q", want, reply)
		}
	}
	if patchTool.calls != 2 {
		t.Fatalf("expected exactly two blocked patch attempts, got %d", patchTool.calls)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected agent to stop on the second pre-write block, got %d requests", len(provider.requests))
	}
}

func TestAgentContinuesAfterDistinctPreWriteCodeFinding(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "scripted", "main-model", "", "default")
	setReview := func(id string, title string, fix string) {
		session.LastReviewRun = &ReviewRun{
			Trigger: "pre_write",
			Gate: GateDecision{
				Verdict:          reviewVerdictNeedsRevision,
				BlockingFindings: []string{id},
			},
			Result: ReviewResult{Summary: title},
			Findings: []ReviewFinding{{
				ID:          id,
				Severity:    reviewSeverityMedium,
				Category:    "correctness",
				Title:       title,
				RequiredFix: fix,
				Quality:     reviewFindingQualityComplete,
			}},
		}
	}
	preWriteErr := fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\n" +
		"Automatic pre-write review found blockers.\n\nReview gate: needs_revision")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	patchTool := &sequenceTool{
		name:    "apply_patch",
		outputs: []string{"", "", "Patch applied successfully."},
		errs:    []error{preWriteErr, preWriteErr, nil},
		before: []func(){
			func() {
				setReview("RF-001", "First proposed edit still misses one guard", "Add the missing guard.")
			},
			func() {
				setReview("RF-002", "Second proposed edit now misses a different repair", "Apply the different required repair.")
			},
		},
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{"patch": "*** Begin Patch\n*** End Patch\n"}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			toolCallResponse("apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+// second proposal\n*** End Patch\n"}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			toolCallResponse("apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+// fixed\n*** End Patch\n"}),
			{Message: Message{Role: "assistant", Text: "Distinct pre-write findings were repaired."}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Distinct pre-write findings were repaired.") {
		t.Fatalf("expected repair loop to continue through distinct pre-write blockers, got %q", reply)
	}
	if patchTool.calls != 3 {
		t.Fatalf("expected two blocked patches and one successful repair, got %d", patchTool.calls)
	}
	if len(provider.requests) != 6 {
		t.Fatalf("expected final response after continuing through distinct blockers, got %d requests", len(provider.requests))
	}
}

func TestAgentBlocksImmediateEditAfterPreWriteBlockUntilReanchor(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	preWriteErr := fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\n" +
		"Automatic pre-write review found blockers.\n\nReview gate: needs_revision")
	patchTool := &sequenceTool{
		name:    "apply_patch",
		outputs: []string{"", "Patch applied successfully."},
		errs:    []error{preWriteErr, nil},
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			multiToolCallResponse(ToolCall{
				ID:        "call-first-patch",
				Name:      "apply_patch",
				Arguments: `{"patch":"*** Begin Patch\n*** End Patch\n"}`,
			}),
			multiToolCallResponse(ToolCall{
				ID:        "call-immediate-patch",
				Name:      "apply_patch",
				Arguments: `{"patch":"*** Begin Patch\n*** End Patch\n"}`,
			}),
			multiToolCallResponse(ToolCall{
				ID:        "call-reanchor",
				Name:      "read_file",
				Arguments: `{"path":"main.go"}`,
			}),
			multiToolCallResponse(ToolCall{
				ID:        "call-final-patch",
				Name:      "apply_patch",
				Arguments: `{"patch":"*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+// repaired\n*** End Patch\n"}`,
			}),
			{Message: Message{Role: "assistant", Text: "reanchored and repaired"}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "reanchored and repaired" {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if patchTool.calls != 2 {
		t.Fatalf("expected immediate stale patch to be blocked before execution, got %d patch calls", patchTool.calls)
	}
	if !sessionContainsToolResultText(session, "call-immediate-patch", "NOT_EXECUTED: the previous edit proposal was blocked before writing") {
		t.Fatalf("expected immediate patch to be returned as NOT_EXECUTED")
	}
	if !scriptedRequestsContainText(provider.requests, "Before another edit tool can run, re-anchor on committed workspace state") {
		t.Fatalf("expected reanchor guidance in model requests, got %#v", provider.requests)
	}
	if len(provider.requests) != 5 {
		t.Fatalf("expected reanchor turn before final patch, got %d requests", len(provider.requests))
	}
}

func TestAgentForcesEditAfterPreWriteRepairInspectionBudget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	for i := 1; i <= maxPreWriteReviewRepairInspectTools+1; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file%d.go", i)), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("WriteFile file%d.go: %v", i, err)
		}
	}
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	preWriteErr := fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\n" +
		"Automatic pre-write review found blockers.\n\nReview gate: needs_revision")
	patchTool := &sequenceTool{
		name:    "apply_patch",
		outputs: []string{"", "Patch applied successfully."},
		errs:    []error{preWriteErr, nil},
	}
	replies := []ChatResponse{
		toolCallResponse("apply_patch", map[string]any{"patch": "*** Begin Patch\n*** End Patch\n"}),
	}
	for i := 1; i <= maxPreWriteReviewRepairInspectTools+1; i++ {
		replies = append(replies, toolCallResponse("read_file", map[string]any{"path": fmt.Sprintf("file%d.go", i)}))
	}
	replies = append(replies,
		toolCallResponse("apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+// fixed\n*** End Patch\n"}),
		ChatResponse{Message: Message{Role: "assistant", Text: "Applied the narrow pre-write repair."}},
	)
	provider := &scriptedProviderClient{
		replies: replies,
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Applied the narrow pre-write repair.") {
		t.Fatalf("expected repair to continue after inspection budget nudge, got %q", reply)
	}
	if patchTool.calls != 2 {
		t.Fatalf("expected blocked patch plus forced repair patch, got %d", patchTool.calls)
	}
	if !scriptedRequestsContainText(provider.requests, "next response must be an edit-tool call") {
		t.Fatalf("expected force-edit guidance after inspection budget, got %#v", provider.requests)
	}
}

func TestAgentStopsCurrentBatchAfterPreWriteRepairInspectionBudget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	readCalls := make([]ToolCall, 0, maxPreWriteReviewRepairInspectTools+2)
	for i := 1; i <= maxPreWriteReviewRepairInspectTools+1; i++ {
		name := fmt.Sprintf("file%d.go", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
		readCalls = append(readCalls, ToolCall{
			ID:        fmt.Sprintf("call-read-%d", i),
			Name:      "read_file",
			Arguments: fmt.Sprintf(`{"path":"%s"}`, name),
		})
	}
	readCalls = append(readCalls, ToolCall{
		ID:        "call-same-batch-patch",
		Name:      "apply_patch",
		Arguments: `{"patch":"*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+// same batch should not run\n*** End Patch\n"}`,
	})
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	preWriteErr := fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\n" +
		"Automatic pre-write review found blockers.\n\nReview gate: needs_revision")
	patchTool := &sequenceTool{
		name:    "apply_patch",
		outputs: []string{"", "Patch applied successfully."},
		errs:    []error{preWriteErr, nil},
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{"patch": "*** Begin Patch\n*** End Patch\n"}),
			multiToolCallResponse(readCalls...),
			toolCallResponse("apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+// fixed\n*** End Patch\n"}),
			{Message: Message{Role: "assistant", Text: "Applied the narrow pre-write repair."}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Applied the narrow pre-write repair.") {
		t.Fatalf("expected next-turn repair after inspection budget nudge, got %q", reply)
	}
	if patchTool.calls != 2 {
		t.Fatalf("expected blocked patch plus next-turn repair patch only, got %d calls", patchTool.calls)
	}
	var skippedSameBatchPatch bool
	for _, msg := range session.Messages {
		if msg.Role == "tool" && msg.ToolCallID == "call-same-batch-patch" && strings.Contains(msg.Text, "NOT_EXECUTED") {
			skippedSameBatchPatch = true
			break
		}
	}
	if !skippedSameBatchPatch {
		t.Fatalf("expected same-batch patch after exhausted inspection budget to be marked NOT_EXECUTED")
	}
}

func TestAgentClosesBlockedReadOnlyEditToolBatchBeforeGuidance(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	patchTool := &sequenceTool{name: "apply_patch", outputs: []string{"patched"}}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{
				"patch": "*** Begin Patch\n*** End Patch\n",
			}),
			{Message: Message{Role: "assistant", Text: "분석만 수행했습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}
	session.AddMessage(Message{Role: "user", Text: "analysis-only"})

	reply, err := agent.completeLoop(context.Background(), true, false, false)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "분석") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if patchTool.calls != 0 {
		t.Fatalf("read-only blocked edit tool must not execute, got %d calls", patchTool.calls)
	}
	if !sessionContainsToolResultText(session, "call-1", "NOT_EXECUTED: this is a read-only analysis turn") {
		t.Fatalf("blocked edit tool call must be closed with a NOT_EXECUTED tool result")
	}
}

func TestAgentTreatsReviewIntentAsReadOnlyWhenModelAttemptsEdit(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	patchTool := &sequenceTool{name: "apply_patch", outputs: []string{"patched"}}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{
				"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n-before\n+after\n*** End Patch\n",
			}),
			{Message: Message{Role: "assistant", Text: "리뷰만 수행했습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "RuntimeManager.cpp 코드 리뷰해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "리뷰") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if patchTool.calls != 0 {
		t.Fatalf("review-only intent must not execute edit tools, got %d call(s)", patchTool.calls)
	}
	if len(provider.requests) == 0 {
		t.Fatalf("expected provider request")
	}
	if chatRequestHasTool(provider.requests[0], "apply_patch") {
		t.Fatalf("review-only intent must not expose apply_patch")
	}
	foundAnalysisOnlyGuidance := false
	for _, msg := range provider.requests[0].Messages {
		if msg.Internal && strings.Contains(msg.Text, "Request mode: analysis-only.") {
			foundAnalysisOnlyGuidance = true
			break
		}
	}
	if !foundAnalysisOnlyGuidance {
		t.Fatalf("expected review-only prompt to include internal analysis-only guidance, got %#v", provider.requests[0].Messages)
	}
	if !sessionContainsToolResultText(session, "call-1", "NOT_EXECUTED: this is a read-only analysis turn") {
		t.Fatalf("blocked review edit call must be closed with a NOT_EXECUTED tool result")
	}
}

func TestAgentAsksUserAfterPreWriteRepairInspectionNudgeIsExhausted(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	for i := 1; i <= maxPreWriteReviewRepairInspectTools+1; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file%d.go", i)), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("WriteFile file%d.go: %v", i, err)
		}
	}
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		Trigger: "pre_write",
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-002"},
		},
		Result: ReviewResult{Summary: "The proposal still leaves the pre-write blocker unresolved."},
		Findings: []ReviewFinding{{
			ID:          "RF-002",
			Severity:    reviewSeverityMedium,
			Category:    "correctness",
			Title:       "Mount point retry is still missing",
			RequiredFix: "Allocate returnCch-sized storage and retry.",
			Quality:     reviewFindingQualityComplete,
		}},
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	patchTool := &failingTool{
		name: "apply_patch",
		err: fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\n" +
			"Automatic pre-write review found blockers.\n\nReview gate: needs_revision"),
	}
	replies := []ChatResponse{
		toolCallResponse("apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+// attempt\n*** End Patch\n"}),
	}
	for cycle := 0; cycle < 2; cycle++ {
		for i := 1; i <= maxPreWriteReviewRepairInspectTools+1; i++ {
			replies = append(replies, toolCallResponse("read_file", map[string]any{"path": fmt.Sprintf("file%d.go", i)}))
		}
	}
	provider := &scriptedProviderClient{
		replies: replies,
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	for _, want := range []string{
		"did not pass",
		"Latest review result: needs_revision",
		"RF-002",
		"Latest edit proposal",
		"// attempt",
		"Should I keep repairing",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("expected inspection-limit reply to contain %q, got %q", want, reply)
		}
	}
	if patchTool.calls != 1 {
		t.Fatalf("expected one blocked patch attempt, got %d", patchTool.calls)
	}
	if !scriptedRequestsContainText(provider.requests, "next response must be an edit-tool call") {
		t.Fatalf("expected force-edit guidance before asking the user, got %#v", provider.requests)
	}
}

func TestAgentContinuesPendingReviewRepairOnlyOnY(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "review-continue",
		Trigger: "pre_write",
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-009"},
		},
		Result: ReviewResult{Summary: "Latest repair still needs one narrow fix."},
		Findings: []ReviewFinding{{
			ID:          "RF-009",
			Severity:    reviewSeverityMedium,
			Category:    "correctness",
			Title:       "Still needs the narrow repair",
			RequiredFix: "Apply the narrow repair from the latest review.",
			Quality:     reviewFindingQualityComplete,
		}},
	}
	session.PendingReviewRepairConfirm = &ReviewRepairConfirmationState{
		CreatedAt: time.Now(),
		ReviewID:  "review-continue",
		Verdict:   reviewVerdictNeedsRevision,
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "repair continued"}},
			{Message: Message{Role: "assistant", Text: "repair continued"}},
			{Message: Message{Role: "assistant", Text: "repair continued"}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "y")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "repair continued" {
		t.Fatalf("expected provider reply after y confirmation, got %q", reply)
	}
	if session.PendingReviewRepairConfirm != nil {
		t.Fatalf("expected pending confirmation to be cleared after y")
	}
	if len(provider.requests) == 0 {
		t.Fatalf("expected model to continue after y confirmation")
	}
	for _, want := range []string{
		"Pending review repair confirmation",
		"RF-009",
		"Apply the narrow repair",
	} {
		if !scriptedRequestsContainText(provider.requests, want) {
			t.Fatalf("expected continued request to contain %q, got %#v", want, provider.requests)
		}
	}
}

func TestAgentReviewerGateUnavailableShowsReviewProposalAndYN(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "reviewer-gate-unavailable",
		Trigger: "pre_write",
		ModelPlan: ReviewModelPlan{
			RequiredRoles: []string{"primary_reviewer"},
		},
		ReviewerRuns: []ReviewReviewerRun{{
			Role:         "primary_reviewer",
			Kind:         "main",
			Status:       "failed",
			ModelQuality: reviewModelQualityFailed,
			Error:        "review model returned empty response",
		}},
		Gate: GateDecision{
			Verdict:          reviewVerdictInsufficientEvidence,
			BlockingFindings: []string{requiredReviewerFailureFindingID, "RF-001"},
			WarningFindings:  []string{"RF-003"},
		},
		Result: ReviewResult{Summary: "Review has insufficient evidence for approval."},
		Findings: []ReviewFinding{
			{
				ID:          requiredReviewerFailureFindingID,
				Severity:    reviewSeverityBlocker,
				Category:    "evidence_gap",
				Title:       "Required review route failed or returned weak output",
				RequiredFix: "Fix the reviewer route before writing.",
				BlocksGate:  true,
			},
			{
				ID:          "RF-001",
				Severity:    reviewSeverityMedium,
				Category:    "maintainability",
				Title:       "Retry branch uses confusing continue in do-while(false)",
				RequiredFix: "Replace the confusing continue with break.",
				BlocksGate:  true,
			},
			{
				ID:          "RF-003",
				Severity:    reviewSeverityLow,
				Category:    "operational_risk",
				Title:       "Retry failure is not logged",
				RequiredFix: "Log the retry failure with the GLE.",
			},
		},
		ArtifactRefs: []string{filepath.Join(root, ".kernforge", "reviews", "review.md")},
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	patchTool := &failingTool{
		name: "apply_patch",
		err:  fmt.Errorf("%w: required review route failed", ErrReviewerGateUnavailable),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{
				"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n-continue;\n+break;\n*** End Patch\n",
			}),
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "fix main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	for _, want := range []string{
		"did not pass",
		"not write approval",
		"[1] Latest review",
		"Latest review result: insufficient_evidence",
		"RF-001",
		"Replace the confusing continue with break",
		"[2] Latest edit proposal",
		"Latest edit proposal",
		"*** Begin Patch",
		"[3] Next decision",
		"[y/N]",
		"exactly `y` or `n`",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("expected reviewer-gate reply to contain %q, got %q", want, reply)
		}
	}
	if session.PendingReviewRepairConfirm == nil {
		t.Fatalf("expected pending confirmation after reviewer gate unavailable")
	}
	if session.PendingReviewRepairConfirm.Mode != reviewRepairConfirmationModeReviewerGateUnavailable {
		t.Fatalf("expected reviewer-gate pending mode, got %q", session.PendingReviewRepairConfirm.Mode)
	}
	if patchTool.calls != 1 {
		t.Fatalf("expected one patch attempt, got %d", patchTool.calls)
	}
}

func TestAgentReviewerGateUnavailableUsesPromptAndContinuesOnY(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "reviewer-gate-prompt-y",
		Trigger: "pre_write",
		ModelPlan: ReviewModelPlan{
			RequiredRoles: []string{"primary_reviewer"},
		},
		ReviewerRuns: []ReviewReviewerRun{{
			Role:         "primary_reviewer",
			Kind:         "main",
			Status:       "failed",
			ModelQuality: reviewModelQualityFailed,
			Error:        "review model returned empty response",
		}},
		Gate: GateDecision{
			Verdict:          reviewVerdictInsufficientEvidence,
			BlockingFindings: []string{requiredReviewerFailureFindingID, "RF-021"},
		},
		Findings: []ReviewFinding{
			{
				ID:          requiredReviewerFailureFindingID,
				Severity:    reviewSeverityBlocker,
				Category:    "evidence_gap",
				Title:       "Required review route failed or returned weak output",
				RequiredFix: "Fix the reviewer route before writing.",
				BlocksGate:  true,
			},
			{
				ID:          "RF-021",
				Severity:    reviewSeverityMedium,
				Category:    "correctness",
				Title:       "Retry failure lacks a log",
				RequiredFix: "Add a narrow retry failure log.",
				BlocksGate:  true,
			},
		},
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	patchTool := &failingTool{
		name: "apply_patch",
		err:  fmt.Errorf("%w: required review route failed", ErrReviewerGateUnavailable),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{
				"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n-old\n+new\n*** End Patch\n",
			}),
			{Message: Message{Role: "assistant", Text: "repair continued"}},
			{Message: Message{Role: "assistant", Text: "repair continued"}},
			{Message: Message{Role: "assistant", Text: "repair continued"}},
		},
	}
	var promptText string
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
		PromptContinueReviewRepair: func(message string) (bool, error) {
			promptText = message
			return true, nil
		},
	}

	reply, err := agent.Reply(context.Background(), "fix main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "repair continued" {
		t.Fatalf("expected repair to continue in the same turn, got %q", reply)
	}
	if session.PendingReviewRepairConfirm != nil {
		t.Fatalf("expected prompt state to be cleared after y")
	}
	if !strings.Contains(promptText, "Pre-write reviewer gate: not approved") ||
		!strings.Contains(promptText, "[2] Latest edit proposal") {
		t.Fatalf("expected prompt text to include review and proposal, got %q", promptText)
	}
	if strings.Contains(promptText, "Reply with exactly") {
		t.Fatalf("runtime prompt text should not ask for natural-language y/n reply, got %q", promptText)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected same-turn continuation request after y, got %d requests", len(provider.requests))
	}
	for _, want := range []string{
		"The previous pre-write review was not approved",
		"RF-021",
		"Add a narrow retry failure log",
		"Latest edit proposal",
	} {
		if !scriptedRequestsContainText(provider.requests, want) {
			t.Fatalf("expected continued request to contain %q, got %#v", want, provider.requests)
		}
	}
}

func TestAgentReviewerGateUnavailableUsesPromptAndStopsOnN(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "reviewer-gate-prompt-n",
		Trigger: "pre_write",
		ModelPlan: ReviewModelPlan{
			RequiredRoles: []string{"primary_reviewer"},
		},
		ReviewerRuns: []ReviewReviewerRun{{
			Role:         "primary_reviewer",
			Kind:         "main",
			Status:       "failed",
			ModelQuality: reviewModelQualityFailed,
			Error:        "review model returned empty response",
		}},
		Gate: GateDecision{
			Verdict:          reviewVerdictInsufficientEvidence,
			BlockingFindings: []string{requiredReviewerFailureFindingID, "RF-021"},
		},
		Findings: []ReviewFinding{
			{
				ID:          requiredReviewerFailureFindingID,
				Severity:    reviewSeverityBlocker,
				Category:    "evidence_gap",
				Title:       "Required review route failed or returned weak output",
				RequiredFix: "Fix the reviewer route before writing.",
				BlocksGate:  true,
			},
			{
				ID:          "RF-021",
				Severity:    reviewSeverityMedium,
				Category:    "correctness",
				Title:       "Retry failure lacks a log",
				RequiredFix: "Add a narrow retry failure log.",
				BlocksGate:  true,
			},
		},
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	patchTool := &failingTool{
		name: "apply_patch",
		err:  fmt.Errorf("%w: required review route failed", ErrReviewerGateUnavailable),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{
				"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n-old\n+new\n*** End Patch\n",
			}),
			{Message: Message{Role: "assistant", Text: "this should not run"}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
		PromptContinueReviewRepair: func(message string) (bool, error) {
			if !strings.Contains(message, "Pre-write reviewer gate") {
				t.Fatalf("expected gate summary in prompt, got %q", message)
			}
			return false, nil
		},
	}

	reply, err := agent.Reply(context.Background(), "fix main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "will not continue repairing") {
		t.Fatalf("expected stop reply after n, got %q", reply)
	}
	if session.PendingReviewRepairConfirm != nil {
		t.Fatalf("expected prompt state to be cleared after n")
	}
	if len(provider.requests) != 1 {
		t.Fatalf("n should stop without another model request, got %d requests", len(provider.requests))
	}
}

func TestAgentReviewerGateUnavailableWithoutActionableFindingDoesNotPrompt(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "reviewer-gate-route-only",
		Trigger: "pre_write",
		ModelPlan: ReviewModelPlan{
			RequiredRoles: []string{"primary_reviewer"},
		},
		ReviewerRuns: []ReviewReviewerRun{{
			Role:         "primary_reviewer",
			Kind:         "main",
			Status:       "failed",
			ModelQuality: reviewModelQualityFailed,
			Error:        "review model returned empty response",
		}},
		Gate: GateDecision{
			Verdict:          reviewVerdictInsufficientEvidence,
			BlockingFindings: []string{requiredReviewerFailureFindingID},
		},
		Findings: []ReviewFinding{{
			ID:          requiredReviewerFailureFindingID,
			Severity:    reviewSeverityBlocker,
			Category:    "evidence_gap",
			Title:       "Required review route failed or returned weak output",
			RequiredFix: "Fix the reviewer route before writing.",
			BlocksGate:  true,
		}},
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	patchTool := &failingTool{
		name: "apply_patch",
		err:  fmt.Errorf("%w: required review route failed", ErrReviewerGateUnavailable),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{
				"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n-old\n+new\n*** End Patch\n",
			}),
			{Message: Message{Role: "assistant", Text: "this should not run"}},
		},
	}
	promptCalls := 0
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
		PromptContinueReviewRepair: func(message string) (bool, error) {
			promptCalls++
			return true, nil
		},
	}

	reply, err := agent.Reply(context.Background(), "fix main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if promptCalls != 0 {
		t.Fatalf("route-only reviewer failure should not ask y/N, got %d prompt calls", promptCalls)
	}
	for _, want := range []string{
		"not by a code finding",
		"/review models",
		"No `y/N` continuation is offered",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("expected route-repair instruction %q in reply, got %q", want, reply)
		}
	}
	if session.PendingReviewRepairConfirm != nil {
		t.Fatalf("expected pending confirmation to be cleared when no y/N is offered")
	}
	if len(provider.requests) != 1 {
		t.Fatalf("route-only reviewer failure should stop without retry, got %d requests", len(provider.requests))
	}
}

func TestReviewerGateUnavailableRouteOnlyReplyDoesNotRecordPendingConfirmation(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "reviewer-gate-route-only-format",
		Trigger: "pre_write",
		ModelPlan: ReviewModelPlan{
			RequiredRoles: []string{"primary_reviewer"},
		},
		ReviewerRuns: []ReviewReviewerRun{{
			Role:         "primary_reviewer",
			Kind:         "main",
			Status:       "failed",
			ModelQuality: reviewModelQualityFailed,
			Error:        "review model returned empty response",
		}},
		Gate: GateDecision{
			Verdict:          reviewVerdictInsufficientEvidence,
			BlockingFindings: []string{requiredReviewerFailureFindingID},
		},
		Findings: []ReviewFinding{{
			ID:          requiredReviewerFailureFindingID,
			Severity:    reviewSeverityBlocker,
			Category:    "evidence_gap",
			Title:       "Required review route failed or returned weak output",
			RequiredFix: "Fix the reviewer route before writing.",
			BlocksGate:  true,
		}},
	}

	reply := formatReviewerGateUnavailableUserDecisionReply(Config{AutoLocale: boolPtr(false)}, session)
	if !strings.Contains(reply, "No `y/N` continuation is offered") {
		t.Fatalf("expected route-only reply to avoid y/N, got %q", reply)
	}
	if session.PendingReviewRepairConfirm != nil {
		t.Fatalf("route-only reviewer failure must not record pending y/N state")
	}
}

func TestReviewerGateUnavailableUserDecisionReplyDoesNotMutateSession(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "reviewer-gate-format-pure",
		Trigger: "pre_write",
		Gate: GateDecision{
			Verdict:          reviewVerdictInsufficientEvidence,
			BlockingFindings: []string{requiredReviewerFailureFindingID, "RF-101"},
		},
		Findings: []ReviewFinding{
			{
				ID:          requiredReviewerFailureFindingID,
				Severity:    reviewSeverityBlocker,
				Category:    "evidence_gap",
				Title:       "Required review route failed or returned weak output",
				RequiredFix: "Fix the reviewer route before writing.",
				BlocksGate:  true,
			},
			{
				ID:          "RF-101",
				Severity:    reviewSeverityMedium,
				Category:    "correctness",
				Title:       "Retry path drops an item",
				RequiredFix: "Repair the item handling path.",
				BlocksGate:  true,
			},
		},
	}

	reply := formatReviewerGateUnavailableUserDecisionReply(Config{AutoLocale: boolPtr(false)}, session)
	if !strings.Contains(reply, "[y/N]") {
		t.Fatalf("expected actionable formatter content to include y/N guidance, got %q", reply)
	}
	if session.PendingReviewRepairConfirm != nil {
		t.Fatalf("formatter must not mutate pending confirmation state")
	}
}

func TestAgentContinuesReviewerGateRepairOnlyOnY(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "reviewer-gate-repair",
		Trigger: "pre_write",
		ModelPlan: ReviewModelPlan{
			RequiredRoles: []string{"primary_reviewer"},
		},
		ReviewerRuns: []ReviewReviewerRun{{
			Role:         "primary_reviewer",
			Kind:         "main",
			Status:       "failed",
			ModelQuality: reviewModelQualityFailed,
			Error:        "review model returned empty response",
		}},
		Gate: GateDecision{
			Verdict:          reviewVerdictInsufficientEvidence,
			BlockingFindings: []string{requiredReviewerFailureFindingID, "RF-021"},
		},
		Findings: []ReviewFinding{
			{
				ID:          requiredReviewerFailureFindingID,
				Severity:    reviewSeverityBlocker,
				Category:    "evidence_gap",
				Title:       "Required review route failed or returned weak output",
				RequiredFix: "Fix the reviewer route before writing.",
				BlocksGate:  true,
			},
			{
				ID:          "RF-021",
				Severity:    reviewSeverityMedium,
				Category:    "correctness",
				Title:       "Retry failure lacks a log",
				RequiredFix: "Add a narrow retry failure log.",
				BlocksGate:  true,
			},
		},
	}
	session.PendingReviewRepairConfirm = &ReviewRepairConfirmationState{
		CreatedAt: time.Now(),
		ReviewID:  "reviewer-gate-repair",
		Verdict:   reviewVerdictInsufficientEvidence,
		Mode:      reviewRepairConfirmationModeReviewerGateUnavailable,
	}
	proposalArgs, err := json.Marshal(map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n-old\n+new\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("marshal proposal: %v", err)
	}
	session.Messages = append(session.Messages, Message{
		Role: "assistant",
		ToolCalls: []ToolCall{{
			Name:      "apply_patch",
			Arguments: string(proposalArgs),
		}},
	})
	store := NewSessionStore(filepath.Join(root, "sessions"))
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "repair continued"}},
			{Message: Message{Role: "assistant", Text: "repair continued"}},
			{Message: Message{Role: "assistant", Text: "repair continued"}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "y")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "repair continued" {
		t.Fatalf("expected provider reply after y confirmation, got %q", reply)
	}
	if session.PendingReviewRepairConfirm != nil {
		t.Fatalf("expected pending confirmation to be cleared after y")
	}
	for _, want := range []string{
		"Pending reviewer-gate repair confirmation",
		"not approval to write without review",
		"Actionable code findings",
		"RF-021",
		"Add a narrow retry failure log",
		"Do not repair RF-REVIEWER-001",
		"Latest edit proposal",
		"*** Update File: main.go",
	} {
		if !scriptedRequestsContainText(provider.requests, want) {
			t.Fatalf("expected continued request to contain %q, got %#v", want, provider.requests)
		}
	}
}

func TestAgentReviewerGateRepairYWithoutActionableFindingStops(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "reviewer-gate-only",
		Trigger: "pre_write",
		ModelPlan: ReviewModelPlan{
			RequiredRoles: []string{"primary_reviewer"},
		},
		ReviewerRuns: []ReviewReviewerRun{{
			Role:         "primary_reviewer",
			Kind:         "main",
			Status:       "failed",
			ModelQuality: reviewModelQualityFailed,
			Error:        "review model returned empty response",
		}},
		Gate: GateDecision{
			Verdict:          reviewVerdictInsufficientEvidence,
			BlockingFindings: []string{requiredReviewerFailureFindingID},
			WarningFindings:  []string{"RF-TEST"},
		},
		Findings: []ReviewFinding{
			{
				ID:          requiredReviewerFailureFindingID,
				Severity:    reviewSeverityBlocker,
				Category:    "evidence_gap",
				Title:       "Required review route failed or returned weak output",
				RequiredFix: "Fix the reviewer route before writing.",
				BlocksGate:  true,
			},
			{
				ID:          "RF-TEST",
				Severity:    reviewSeverityMedium,
				Category:    "test_gap",
				Title:       "No test evidence was provided",
				RequiredFix: "Add a focused test if code changes are made.",
			},
		},
	}
	session.PendingReviewRepairConfirm = &ReviewRepairConfirmationState{
		CreatedAt: time.Now(),
		ReviewID:  "reviewer-gate-only",
		Verdict:   reviewVerdictInsufficientEvidence,
		Mode:      reviewRepairConfirmationModeReviewerGateUnavailable,
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	provider := &scriptedProviderClient{
		replies: []ChatResponse{{Message: Message{Role: "assistant", Text: "this should not run"}}},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "y")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "/review models") ||
		!strings.Contains(reply, "no code item to repair") {
		t.Fatalf("expected no-actionable reply, got %q", reply)
	}
	if session.PendingReviewRepairConfirm != nil {
		t.Fatalf("expected pending confirmation to be cleared")
	}
	if len(provider.requests) != 0 {
		t.Fatalf("y without actionable findings should not call the model, got %d requests", len(provider.requests))
	}
}

func TestAgentReviewerGateRepairUsesActionableFindingOutsideGateIDs(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "reviewer-gate-nongate-code-finding",
		Trigger: "pre_write",
		ModelPlan: ReviewModelPlan{
			RequiredRoles: []string{"primary_reviewer"},
		},
		ReviewerRuns: []ReviewReviewerRun{{
			Role:         "primary_reviewer",
			Kind:         "main",
			Status:       "failed",
			ModelQuality: reviewModelQualityFailed,
			Error:        "review model returned empty response",
		}},
		Gate: GateDecision{
			Verdict:          reviewVerdictInsufficientEvidence,
			BlockingFindings: []string{requiredReviewerFailureFindingID},
		},
		Findings: []ReviewFinding{
			{
				ID:          requiredReviewerFailureFindingID,
				Severity:    reviewSeverityBlocker,
				Category:    "evidence_gap",
				Title:       "Required review route failed or returned weak output",
				RequiredFix: "Fix the reviewer route before writing.",
				BlocksGate:  true,
			},
			{
				ID:          "RF-088",
				Severity:    reviewSeverityMedium,
				Category:    "correctness",
				Title:       "Retry path drops the volume",
				RequiredFix: "Keep the volume retry path observable.",
			},
		},
	}
	session.PendingReviewRepairConfirm = &ReviewRepairConfirmationState{
		CreatedAt: time.Now(),
		ReviewID:  "reviewer-gate-nongate-code-finding",
		Verdict:   reviewVerdictInsufficientEvidence,
		Mode:      reviewRepairConfirmationModeReviewerGateUnavailable,
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "repair continued"}},
			{Message: Message{Role: "assistant", Text: "repair continued"}},
			{Message: Message{Role: "assistant", Text: "repair continued"}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "y")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "repair continued" {
		t.Fatalf("expected provider reply after y confirmation, got %q", reply)
	}
	if len(provider.requests) == 0 {
		t.Fatalf("expected y to continue because a non-gate actionable code finding exists")
	}
	if !scriptedRequestsContainText(provider.requests, "RF-088") ||
		!scriptedRequestsContainText(provider.requests, "Keep the volume retry path observable") {
		t.Fatalf("expected non-gate actionable finding to be carried into repair prompt, got %#v", provider.requests)
	}
}

func TestAgentRejectsNaturalLanguagePendingReviewRepairAnswer(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "review-natural",
		Trigger: "pre_write",
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-010"},
		},
	}
	session.PendingReviewRepairConfirm = &ReviewRepairConfirmationState{
		CreatedAt: time.Now(),
		ReviewID:  "review-natural",
		Verdict:   reviewVerdictNeedsRevision,
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	provider := &scriptedProviderClient{
		replies: []ChatResponse{{Message: Message{Role: "assistant", Text: "this should not run"}}},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "yes")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "exactly `y` or `n`") {
		t.Fatalf("expected y/n-only prompt, got %q", reply)
	}
	if session.PendingReviewRepairConfirm == nil {
		t.Fatalf("expected pending confirmation to remain after natural-language answer")
	}
	if len(provider.requests) != 0 {
		t.Fatalf("natural-language confirmation should not call the model, got %d requests", len(provider.requests))
	}
}

func TestAgentStopsPendingReviewRepairOnN(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "review-stop",
		Trigger: "pre_write",
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-011"},
		},
	}
	session.PendingReviewRepairConfirm = &ReviewRepairConfirmationState{
		CreatedAt: time.Now(),
		ReviewID:  "review-stop",
		Verdict:   reviewVerdictNeedsRevision,
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	provider := &scriptedProviderClient{
		replies: []ChatResponse{{Message: Message{Role: "assistant", Text: "this should not run"}}},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "n")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "will not continue repairing") {
		t.Fatalf("expected stop reply, got %q", reply)
	}
	if session.PendingReviewRepairConfirm != nil {
		t.Fatalf("expected pending confirmation to be cleared after n")
	}
	if len(provider.requests) != 0 {
		t.Fatalf("n confirmation should not call the model, got %d requests", len(provider.requests))
	}
}

func TestAgentStopsAfterSecondEditTargetMismatchEvenWithInterleavedSuccess(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	replaceTool := &failingTool{
		name: "replace_in_file",
		err:  fmt.Errorf("%w: search text not found in main.go", ErrEditTargetMismatch),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing", "replace": "present"}),
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			toolCallResponse("replace_in_file", map[string]any{"path": "main.go", "search": "missing", "replace": "present"}),
			{Message: Message{Role: "assistant", Text: "this response must not be requested after repeated edit target mismatch"}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(replaceTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Edit target mismatches repeated") {
		t.Fatalf("expected bounded edit mismatch stop reply, got %q", reply)
	}
	if !strings.Contains(reply, "Result: no code changes were applied") || !strings.Contains(reply, "Latest edit proposal") {
		t.Fatalf("expected mismatch stop reply to show result and latest proposal, got %q", reply)
	}
	if replaceTool.calls != 2 {
		t.Fatalf("expected exactly two mismatched edit attempts, got %d", replaceTool.calls)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected agent to stop on the second edit mismatch, got %d requests", len(provider.requests))
	}
}

func TestEditTargetMismatchLoopLimitShowsPreWriteBlocker(t *testing.T) {
	session := &Session{
		LastReviewRun: &ReviewRun{
			Trigger: "pre_write",
			Gate: GateDecision{
				Verdict:          reviewVerdictInsufficientEvidence,
				BlockingFindings: []string{"RF-001"},
				WarningFindings:  []string{"RF-002"},
			},
			Findings: []ReviewFinding{
				{
					ID:          "RF-001",
					Severity:    reviewSeverityMedium,
					Category:    "evidence_gap",
					Title:       "Dynamic buffer repair evidence is missing",
					RequiredFix: "Resubmit the full after-change function body.",
					BlocksGate:  true,
				},
				{
					ID:       "RF-002",
					Severity: reviewSeverityLow,
					Category: "test_gap",
					Title:    "Build result is missing",
				},
			},
		},
	}
	reply := formatEditTargetMismatchLoopLimitReply(Config{AutoLocale: boolPtr(false)}, session)
	if !strings.Contains(reply, "Latest review result: insufficient_evidence") ||
		!strings.Contains(reply, "RF-001") ||
		!strings.Contains(reply, "Dynamic buffer repair evidence is missing") {
		t.Fatalf("expected mismatch stop reply to surface the pre-write blocker, got %q", reply)
	}
}

func TestEditTargetMismatchLoopLimitDistinguishesLookupOwnership(t *testing.T) {
	session := &Session{
		Messages: []Message{
			{
				Role:     "tool",
				ToolName: "list_files",
				Text:     "edit target mismatch: path . is outside editable ownership for specialist driver-build-fixer",
				IsError:  true,
			},
		},
	}
	reply := formatEditTargetMismatchLoopLimitReply(Config{AutoLocale: boolPtr(false)}, session)
	if !strings.Contains(reply, "Read-only inspection tools") {
		t.Fatalf("expected lookup ownership wording, got %q", reply)
	}
	if strings.Contains(reply, "stale patch problem") && strings.Contains(reply, "latest patch was not anchored") {
		t.Fatalf("lookup ownership mismatch should not be described as stale patch anchoring, got %q", reply)
	}
}

func TestPreWriteReviewBlockDisablesReplaceForNextRetry(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	patchTool := &failingTool{
		name: "apply_patch",
		err:  fmt.Errorf("automatic pre-write review blocked this edit before writing: missing evidence"),
	}
	replaceTool := &failingTool{name: "replace_in_file"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{"patch": "*** Begin Patch\n*** End Patch\n"}),
			{Message: Message{Role: "assistant", Text: "ready to retry with apply_patch only"}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, replaceTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}
	reply, err := agent.Reply(context.Background(), "fix main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "ready to retry") {
		t.Fatalf("expected second model reply, got %q", reply)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected a second model request after pre-write block, got %d", len(provider.requests))
	}
	if !chatRequestHasTool(provider.requests[0], "replace_in_file") {
		t.Fatalf("expected replace_in_file to be initially available")
	}
	if chatRequestHasTool(provider.requests[1], "replace_in_file") {
		t.Fatalf("expected replace_in_file to be disabled after pre-write block")
	}
	if !scriptedRequestsContainText(provider.requests, "replace_in_file is disabled for this recovery path") {
		t.Fatalf("expected retry guidance to explain disabled replace_in_file")
	}
}

func TestAgentRetriesEnglishToolNarrationForKoreanLocalCodeRepair(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "SampleApp", "SampleWorker", "PathConverter.cpp")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("int ConvertPath()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "I see the previous patches were blocked. Let me inspect the file again.",
					ToolCalls: []ToolCall{{
						ID:        "call-1",
						Name:      "read_file",
						Arguments: `{"path":"SampleApp/SampleWorker/PathConverter.cpp"}`,
					}},
				},
			},
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/PathConverter.cpp"}),
			{Message: Message{Role: "assistant", Text: "한국어 진행 설명으로 전환했고 로컬 파일을 확인했습니다."}},
		},
	}
	readFile := NewReadFileTool(ws)
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(readFile),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@SampleApp/SampleWorker/PathConverter.cpp:1-3 검토하고 버그를 수정해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "한국어") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected English narration to be retried before tool execution, got %d requests", len(provider.requests))
	}
	guidance := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if guidance.Role != "user" || !strings.Contains(guidance.Text, "응답 언어 정책 위반") {
		t.Fatalf("expected Korean language retry guidance, got %#v", guidance)
	}
}

func TestAgentAllowsLocalInspectionAfterWebResearchToolUsed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	webTool := &staticTool{
		name:   "mcp__web__search_web",
		output: "source 1\nsource 2",
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("mcp__web__search_web", map[string]any{"query": "hypervisor anti-cheat latest"}),
			toolCallResponse("list_files", map[string]any{"path": "."}),
			{Message: Message{Role: "assistant", Text: "웹 조사 후 로컬 문서 경로까지 확인했습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(webTool, NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		MCP: &MCPManager{
			servers: []*MCPClient{
				{
					config: MCPServerConfig{Name: "web"},
					tools: []MCPToolDescriptor{
						{Name: "search_web", Description: "Search the web for current articles and references"},
					},
				},
			},
		},
	}

	reply, err := agent.Reply(context.Background(), "Hypervisor를 이용한 게임핵 탐지 최신 기술들을 리서치하고 설계 문서를 파일로 작성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "로컬 문서 경로") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if webTool.calls != 1 {
		t.Fatalf("expected web research tool to run once, got %d", webTool.calls)
	}
	toolMessages := 0
	for _, msg := range session.Messages {
		if msg.Role == "tool" && !msg.IsError {
			toolMessages++
		}
	}
	if toolMessages != 2 {
		t.Fatalf("expected both web research and list_files tool results to be recorded, got %d", toolMessages)
	}
}

func TestAgentUsesLoadedWebResearchMCPToolsBeforeLocalInspection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("workspace\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: root, Root: root}, []MCPServerConfig{{
		Name:         "web-research",
		Command:      os.Args[0],
		Args:         []string{"-test.run=TestMCPWebResearchHelperProcess"},
		Capabilities: []string{"web_search", "web_fetch"},
		Env: map[string]string{
			"KERNFORGE_MCP_WEB_HELPER": "1",
		},
	}})
	defer manager.Close()
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{"path": "."}),
			toolCallResponse("mcp__web_research__search_web", map[string]any{"query": "hypervisor anti-cheat latest"}),
			toolCallResponse("mcp__web_research__fetch_url", map[string]any{"url": "https://example.test/hypervisor-detection"}),
			{Message: Message{Role: "assistant", Text: "웹 검색과 본문 fetch까지 마친 뒤 로컬 파일 작업으로 넘어갈 준비가 됐습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     buildRegistry(ws, manager),
		Workspace: ws,
		Session:   session,
		Store:     store,
		MCP:       manager,
	}

	reply, err := agent.Reply(context.Background(), "Hypervisor를 이용한 게임핵 탐지 최신 기술들을 리서치하고 설계 문서를 파일로 작성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "fetch") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected blocked local inspection plus two web tool turns, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Before using local inspection or edit tools") {
		t.Fatalf("expected web research guidance, got %#v", lastMessage)
	}
	toolMessages := 0
	for _, msg := range session.Messages {
		if msg.Role == "tool" && !msg.IsError {
			toolMessages++
		}
	}
	if toolMessages != 2 {
		t.Fatalf("expected search and fetch tool results to be recorded, got %d", toolMessages)
	}
}

func TestAgentDoesNotSuppressFinalReplyAfterStreamFallback(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:             Config{},
		Client:             &fallbackReplayClient{},
		Tools:              NewToolRegistry(NewReadFileTool(ws)),
		Workspace:          ws,
		Session:            session,
		Store:              store,
		EmitAssistantDelta: func(string) {},
	}
	agent.lastEmittedText = "full fallback answer"

	reply, err := agent.Reply(context.Background(), "inspect this file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "full fallback answer" {
		t.Fatalf("expected final reply replay, got %q", reply)
	}
}

func TestAgentReturnsFinalReplyEvenWhenAlreadyStreamed(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{},
		Client: &streamingScriptedProviderClient{
			replies: []ChatResponse{
				{Message: Message{Role: "assistant", Text: "final streamed answer"}},
			},
		},
		Tools:              NewToolRegistry(NewReadFileTool(ws)),
		Workspace:          ws,
		Session:            session,
		Store:              store,
		EmitAssistantDelta: func(string) {},
	}

	reply, err := agent.Reply(context.Background(), "inspect this file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "final streamed answer" {
		t.Fatalf("expected streamed final reply to be returned, got %q", reply)
	}
}

func TestReadFilePathKeyNormalizesCaseInsensitivePaths(t *testing.T) {
	upperArgs, err := json.Marshal(map[string]any{"path": "Src/Foo.cpp"})
	if err != nil {
		t.Fatalf("marshal upper args: %v", err)
	}
	lowerArgs, err := json.Marshal(map[string]any{"path": "Src/foo.cpp"})
	if err != nil {
		t.Fatalf("marshal lower args: %v", err)
	}
	upper := readFilePathKey(string(upperArgs))
	lower := readFilePathKey(string(lowerArgs))
	if upper == "" || lower == "" {
		t.Fatalf("expected non-empty path keys, got upper=%q lower=%q", upper, lower)
	}
	if runtime.GOOS == "windows" {
		if upper != lower {
			t.Fatalf("Windows read_file path keys should normalize case, got upper=%q lower=%q", upper, lower)
		}
	} else if upper == lower {
		t.Fatalf("POSIX-style read_file path keys should preserve case on case-sensitive workspaces, got %q", upper)
	}

	winUpperArgs, err := json.Marshal(map[string]any{"path": `C:\Repo\Src\Foo.cpp`})
	if err != nil {
		t.Fatalf("marshal windows upper args: %v", err)
	}
	winLowerArgs, err := json.Marshal(map[string]any{"path": `c:\repo\src\foo.cpp`})
	if err != nil {
		t.Fatalf("marshal windows lower args: %v", err)
	}
	if gotUpper, gotLower := readFilePathKey(string(winUpperArgs)), readFilePathKey(string(winLowerArgs)); gotUpper != gotLower {
		t.Fatalf("Windows-style read_file path keys should normalize case, got upper=%q lower=%q", gotUpper, gotLower)
	}

	backslashUpperArgs, err := json.Marshal(map[string]any{"path": `Dir\File.txt`})
	if err != nil {
		t.Fatalf("marshal backslash upper args: %v", err)
	}
	backslashLowerArgs, err := json.Marshal(map[string]any{"path": `dir\file.txt`})
	if err != nil {
		t.Fatalf("marshal backslash lower args: %v", err)
	}
	backslashUpper := readFilePathKey(string(backslashUpperArgs))
	backslashLower := readFilePathKey(string(backslashLowerArgs))
	if runtime.GOOS == "windows" {
		if backslashUpper != backslashLower {
			t.Fatalf("Windows backslash read_file path keys should normalize case, got upper=%q lower=%q", backslashUpper, backslashLower)
		}
	} else if backslashUpper == backslashLower {
		t.Fatalf("non-Windows ordinary backslash paths must preserve case, got upper=%q lower=%q", backslashUpper, backslashLower)
	}
}

func containsStringMatching(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func TestAgentNudgesAfterRepeatedReadFilePathAcrossRanges(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "SampleApp", "SampleWorker")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "SampleWorkerCore.cpp"), []byte("int WorkerMain()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/SampleWorkerCore.cpp", "start_line": 1, "end_line": 1}),
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/SampleWorkerCore.cpp", "start_line": 1, "end_line": 2}),
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/SampleWorkerCore.cpp", "start_line": 2, "end_line": 3}),
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/SampleWorkerCore.cpp", "start_line": 3, "end_line": 4}),
			{Message: Message{Role: "assistant", Text: "I have enough context now and can explain the issue."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect this file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "enough context") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 5 {
		t.Fatalf("expected fifth turn after repeated read_file nudge, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[4]
	if len(lastRequest.Messages) == 0 {
		t.Fatalf("expected repeated read_file guidance before final turn")
	}
	foundGuidance := false
	for _, msg := range lastRequest.Messages {
		if msg.Role != "user" {
			continue
		}
		if strings.Contains(msg.Text, "read the same file repeatedly") || strings.Contains(msg.Text, "came from cached previously-read content") {
			foundGuidance = true
			break
		}
	}
	if !foundGuidance {
		t.Fatalf("expected repeated read_file guidance in final request, got %#v", lastRequest.Messages)
	}
}

func TestAgentStopsAfterRepeatedReadFilePathAcrossRangesAfterRecoveryTurn(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "SampleApp", "SampleWorker")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "SampleWorkerCore.cpp"), []byte("int WorkerMain()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/SampleWorkerCore.cpp", "start_line": 1, "end_line": 1}),
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/SampleWorkerCore.cpp", "start_line": 1, "end_line": 2}),
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/SampleWorkerCore.cpp", "start_line": 2, "end_line": 3}),
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/SampleWorkerCore.cpp", "start_line": 3, "end_line": 4}),
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/SampleWorkerCore.cpp", "start_line": 1, "end_line": 3}),
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/SampleWorkerCore.cpp", "start_line": 2, "end_line": 4}),
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/SampleWorkerCore.cpp", "start_line": 1, "end_line": 4}),
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/SampleWorkerCore.cpp", "start_line": 2, "end_line": 4}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "inspect this file")
	if err == nil {
		t.Fatalf("expected repeated same-file reads to stop the loop")
	}
	if !strings.Contains(err.Error(), "repeatedly reading the same file") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(provider.requests) != 8 {
		t.Fatalf("expected abort on eighth repeated same-file turn, got %d requests", len(provider.requests))
	}
	recoveryRequest := provider.requests[6]
	if len(recoveryRequest.Messages) == 0 {
		t.Fatalf("expected recovery guidance before final repeated read")
	}
	foundRecovery := false
	for _, msg := range recoveryRequest.Messages {
		if msg.Role == "user" && strings.Contains(msg.Text, "Recovery mode") {
			foundRecovery = true
			break
		}
	}
	if !foundRecovery {
		t.Fatalf("expected recovery guidance in request %#v", recoveryRequest.Messages)
	}
}

func TestSummarizeToolTurnIncludesReadFileRangeDetails(t *testing.T) {
	messages := []Message{
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{
					ID:        "call-1",
					Name:      "read_file",
					Arguments: `{"path":"SampleApp/SampleWorker/SampleWorkerCore.cpp","start_line":29,"end_line":58}`,
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call-1",
			ToolName:   "read_file",
			Text:       "ok",
		},
	}

	got := summarizeToolTurn(messages, 0)
	if !strings.Contains(got, "read_file[SampleApp/SampleWorker/SampleWorkerCore.cpp:29-58]:ok") {
		t.Fatalf("expected read_file range details in diagnostic, got %q", got)
	}
}

func TestSummarizeToolTurnMarksCachedReadFileResults(t *testing.T) {
	messages := []Message{
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{
					ID:        "call-1",
					Name:      "read_file",
					Arguments: `{"path":"SampleApp/SampleWorker/SampleWorkerCore.cpp","start_line":29,"end_line":58}`,
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call-1",
			ToolName:   "read_file",
			Text:       "NOTE: returning cached content for an unchanged read_file range.\n  29 | cached",
		},
	}

	got := summarizeToolTurn(messages, 0)
	if !strings.Contains(got, "read_file[SampleApp/SampleWorker/SampleWorkerCore.cpp:29-58]:cached") {
		t.Fatalf("expected cached read_file diagnostic, got %q", got)
	}
}

func TestAgentNudgesSoonerAfterCachedReadFileResult(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "SampleApp", "SampleWorker")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "SampleWorkerCore.cpp"), []byte("int WorkerMain()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/SampleWorkerCore.cpp", "start_line": 1, "end_line": 2}),
			toolCallResponse("read_file", map[string]any{"path": "SampleApp/SampleWorker/SampleWorkerCore.cpp", "start_line": 1, "end_line": 2}),
			{Message: Message{Role: "assistant", Text: "I already have enough context."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect this file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "enough context") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected cached-read nudge before final turn, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[2]
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "came from cached previously-read content") {
		t.Fatalf("expected cached read guidance, got %#v", lastMessage)
	}
}

func TestAgentNudgesBeforeAbortingRepeatedToolFailure(t *testing.T) {
	root := t.TempDir()
	failTool := &failingTool{
		name: "failing_tool",
		err:  fmt.Errorf("preview surface busy"),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("failing_tool", map[string]any{}),
			toolCallResponse("failing_tool", map[string]any{}),
			{Message: Message{Role: "assistant", Text: "I could not use the preview surface, so I am stopping with guidance instead."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(failTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "try the preview flow")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "stopping with guidance") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected third turn after repeated failure nudge, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[2]
	if len(lastRequest.Messages) == 0 {
		t.Fatalf("expected guidance message before third turn")
	}
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "The same tool failure repeated") {
		t.Fatalf("expected repeated failure guidance, got %#v", lastMessage)
	}
}

func TestAgentAbortsAfterFourthRepeatedToolFailure(t *testing.T) {
	root := t.TempDir()
	failTool := &failingTool{
		name: "failing_tool",
		err:  fmt.Errorf("preview surface busy"),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("failing_tool", map[string]any{"attempt": 1}),
			toolCallResponse("failing_tool", map[string]any{"attempt": 2}),
			toolCallResponse("failing_tool", map[string]any{"attempt": 3}),
			toolCallResponse("failing_tool", map[string]any{"attempt": 4}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(failTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "try the preview flow")
	if err == nil || !strings.Contains(err.Error(), "stopped after repeated tool failure") {
		t.Fatalf("expected repeated tool failure error, got %v", err)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected abort on fourth failing turn, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[3]
	if len(lastRequest.Messages) == 0 {
		t.Fatalf("expected recovery guidance before final repeated failure turn")
	}
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Recovery mode") {
		t.Fatalf("expected recovery guidance before abort, got %#v", lastMessage)
	}
}

func TestAgentSingleToolFailureGetsRecoveryTurnInsteadOfRepeatedFailureLabel(t *testing.T) {
	root := t.TempDir()
	failTool := &failingTool{
		name: "failing_tool",
		err:  fmt.Errorf("preview surface busy"),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("failing_tool", map[string]any{}),
			{
				Message: Message{
					Role: "assistant",
					Text: "The preview surface stayed busy, so I am stopping instead of repeating the same failing tool call.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			MaxToolIterations: 1,
		},
		Client:    provider,
		Tools:     NewToolRegistry(failTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "try the preview flow")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "preview surface stayed busy") {
		t.Fatalf("unexpected recovery-turn reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected one recovery turn after the single tool failure, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[1]
	if len(lastRequest.Messages) == 0 {
		t.Fatalf("expected recovery guidance before final turn")
	}
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "tool budget has been exhausted") {
		t.Fatalf("expected tool-loop-limit recovery guidance, got %#v", lastMessage)
	}
}

func TestAgentStopsAfterDiffPreviewPromptCanceledWithoutModelRetry(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package main\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{
				"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+func main() {}\n*** End Patch\n",
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "this response must not be requested after preview cancel",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	previewCalls := 0
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			previewCalls++
			if !strings.Contains(preview.Preview, "Preview for main.go") {
				t.Fatalf("expected patch preview contents, got %q", preview.Preview)
			}
			return false, ErrPromptCanceled
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewApplyPatchTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "update main.go")
	if !errors.Is(err, ErrEditCanceled) {
		t.Fatalf("expected ErrEditCanceled after preview prompt cancel, got %v", err)
	}
	if previewCalls != 1 {
		t.Fatalf("expected one preview call, got %d", previewCalls)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("preview cancel must stop without a model retry, got %d requests", len(provider.requests))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Fatalf("preview cancel must not change files, got %q", string(data))
	}
}

func TestAgentStopsAfterDiffPreviewEOFWithoutModelRetry(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package main\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{
				"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+func main() {}\n*** End Patch\n",
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "this response must not be requested after preview EOF",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	previewCalls := 0
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			previewCalls++
			if !strings.Contains(preview.Preview, "Preview for main.go") {
				t.Fatalf("expected patch preview contents, got %q", preview.Preview)
			}
			return false, io.EOF
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewApplyPatchTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "update main.go")
	if !errors.Is(err, ErrEditCanceled) {
		t.Fatalf("expected ErrEditCanceled after preview EOF, got %v", err)
	}
	if previewCalls != 1 {
		t.Fatalf("expected one preview call, got %d", previewCalls)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("preview EOF must stop without a model retry, got %d requests", len(provider.requests))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Fatalf("preview EOF must not change files, got %q", string(data))
	}
}

func TestAgentStopsAfterWriteApprovalUnavailableWithoutModelRetry(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package main\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{
				"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+func main() {}\n*** End Patch\n",
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "this response must not be requested after write approval is unavailable",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		Perms: NewPermissionManager(ModeDefault, func(question string) (bool, error) {
			return false, fmt.Errorf("interactive confirmation unavailable")
		}),
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewApplyPatchTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "update main.go")
	if !errors.Is(err, ErrWriteDenied) {
		t.Fatalf("expected ErrWriteDenied after write approval unavailable, got %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("write approval failure must stop without a model retry, got %d requests", len(provider.requests))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Fatalf("write approval failure must not change files, got %q", string(data))
	}
}

func TestAgentFinalAnswerReviewerRequestsRevisionBeforeReturn(t *testing.T) {
	root := t.TempDir()
	mainProvider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "1. Inspect the issue\n2. Fix it\n3. Summarize the result",
				},
				StopReason: "stop",
			},
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {}\n"}),
			{
				Message: Message{
					Role: "assistant",
					Text: "The issue is fixed.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "I updated the fix, verified the result via go test ./..., and there are no remaining blockers.",
				},
				StopReason: "stop",
			},
		},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "APPROVED\nThe execution plan is sound.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: strings.Join([]string{
						"REVIEW_RESULT",
						"verdict: approved",
						"summary: post-change review approved the edit",
						"findings:",
					}, "\n"),
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "NEEDS_REVISION\nThe final answer does not mention verification or whether any blockers remain.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "APPROVED\nThe revised final answer is ready.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model: "model",
		},
		Client:         mainProvider,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Tools:          NewToolRegistry(NewWriteFileTool(ws)),
		Workspace:      ws,
		Session:        session,
		Store:          store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			return VerificationReport{
				ChangedPaths: []string{"main.go"},
				Steps: []VerificationStep{{
					Label:  "go test ./...",
					Status: VerificationPassed,
					Output: "ok",
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "fix the bug and summarize the result")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "verified the result") {
		t.Fatalf("expected revised final answer, got %q", reply)
	}
	if len(reviewer.requests) != 4 {
		t.Fatalf("expected plan review + post-change review + 2 final reviews, got %d requests", len(reviewer.requests))
	}
	foundRevisionPrompt := false
	for _, msg := range session.Messages {
		if msg.Role == "user" && strings.Contains(msg.Text, "Reviewer feedback: the proposed final answer is not ready yet") {
			foundRevisionPrompt = true
			break
		}
	}
	if !foundRevisionPrompt {
		t.Fatalf("expected reviewer revision prompt to be added to the session")
	}
	if session.TaskState == nil || session.TaskState.FinalReviewVerdict != "approved" {
		t.Fatalf("expected final review verdict to be approved, got %#v", session.TaskState)
	}
}

func TestAgentGeneratedDocumentArtifactFinalizesWithoutFinalReviewerOrShellValidation(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 2개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 2 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"- Impact: resource lifetime bug.",
	}, "\n")
	mainProvider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "1. Inspect source files\n2. Write Tavern/BugReport.md\n3. Summarize the document artifact",
				},
				StopReason: "stop",
			},
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 2개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
				},
				StopReason: "stop",
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    mainProvider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") {
		t.Fatalf("expected final document summary, got %q", reply)
	}
	if len(mainProvider.requests) != 3 {
		t.Fatalf("expected the shell-validation response to remain unrequested, got %d main requests", len(mainProvider.requests))
	}
	if session.ActivePatchTransaction != nil {
		t.Fatalf("expected document patch transaction to finalize, got %#v", session.ActivePatchTransaction)
	}
	if len(session.PatchTransactions) == 0 || session.PatchTransactions[0].ChangedPaths()[0] != "Tavern/BugReport.md" {
		t.Fatalf("expected archived document patch transaction, got %#v", session.PatchTransactions)
	}
}

func TestAgentGeneratedDocumentArtifactFinalizesWhenRequestOmitsOutputPath(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 2개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 2 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"- Impact: resource lifetime bug.",
	}, "\n")
	mainProvider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: strings.Join([]string{
						"The `Tavern/BugReport.md` document has been fully created and verified.",
						"",
						"27 documented bugs were found.",
						"",
						"| Severity | Count |",
						"|----------|-------|",
						"| Critical | 4 |",
						"| High | 7 |",
						"| Medium | 9 |",
						"| Low | 6 |",
						"| **Total** | **26** |",
						"",
						"No build/test verification was run because this is a documentation-only artifact.",
					}, "\n"),
				},
				StopReason: "stop",
			},
			toolCallResponse("read_file", map[string]any{"path": "Tavern/BugReport.md"}),
		},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{
				Role: "assistant",
				Text: "NEEDS_REVISION\nThis final-answer reviewer should not run for generated document artifact completion.",
			},
			StopReason: "stop",
		}},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
			Review: ReviewHarnessConfig{
				AutoAfterChange: boolPtr(true),
			},
		},
		Client:         mainProvider,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Tools:          NewToolRegistry(NewWriteFileTool(ws), &staticTool{name: "read_file", output: "read should not run"}),
		Workspace:      ws,
		Session:        session,
		Store:          store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") {
		t.Fatalf("expected final document summary, got %q", reply)
	}
	if strings.Contains(reply, "27 documented bugs") {
		t.Fatalf("expected inconsistent model summary to be replaced by synthesized final reply, got %q", reply)
	}
	if len(mainProvider.requests) != 2 {
		t.Fatalf("expected post-final inspection to remain unrequested, got %d main requests", len(mainProvider.requests))
	}
	if len(reviewer.requests) != 0 {
		t.Fatalf("expected generated document artifact to skip final reviewer, got %d reviewer requests", len(reviewer.requests))
	}
	if session.Messages[len(session.Messages)-1].Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected generated document reply to be accepted as final, got %#v", session.Messages[len(session.Messages)-1])
	}
}

func TestAgentGeneratedDocumentArtifactFinalizesDespiteProviderEndTurnFalse(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 1개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
	}, "\n")
	endTurnFalse := false
	mainProvider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 1개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
				},
				EndTurn:    &endTurnFalse,
				StopReason: "stop",
			},
			toolCallResponse("read_file", map[string]any{"path": "Tavern/BugReport.md"}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    mainProvider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), &staticTool{name: "read_file", output: "read should not run"}),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") || !strings.Contains(reply, "검증은 실행하지 않았습니다") {
		t.Fatalf("expected generated document final reply, got %q", reply)
	}
	if len(mainProvider.requests) != 2 {
		t.Fatalf("expected end_turn=false final reply to stop before post-final read_file, got %d requests", len(mainProvider.requests))
	}
	if session.Messages[len(session.Messages)-1].Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected generated document reply to be accepted as final, got %#v", session.Messages[len(session.Messages)-1])
	}
}

func TestAgentBlocksGeneratedDocumentPostWriteShellValidationWhenRequestOmitsOutputPath(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 2개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 2 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"- Impact: resource lifetime bug.",
	}, "\n")
	shellTool := &staticTool{name: "run_shell", output: "shell should not run"}
	mainProvider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			toolCallResponse("run_shell", map[string]any{"command": "echo Reviewing Tavern/BugReport.md for final validation"}),
			{
				Message: Message{
					Role: "assistant",
					Text: "This fallback final answer should not be requested.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
			Review: ReviewHarnessConfig{
				AutoAfterChange: boolPtr(true),
			},
		},
		Client:    mainProvider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), shellTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") || !strings.Contains(reply, "빌드/테스트 검증은 실행하지 않았습니다") {
		t.Fatalf("expected synthesized generated document final reply, got %q", reply)
	}
	if shellTool.calls != 0 {
		t.Fatalf("post-write shell validation should not execute, got %d call(s)", shellTool.calls)
	}
	if len(mainProvider.requests) != 2 {
		t.Fatalf("expected post-write shell validation to be blocked without a follow-up request, got %d main requests", len(mainProvider.requests))
	}
	if !sessionContainsToolResultText(session, "call-1", "generated document artifact turns do not run shell or review validation") {
		t.Fatalf("expected blocked shell validation tool result, messages=%#v", session.Messages)
	}
}

func TestAgentGeneratedDocumentArtifactFinalizesAfterSkippedAutoVerifyDisclosure(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 1개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
	}, "\n")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 1개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
				},
				StopReason: "stop",
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}), // must remain unused
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var progress []string
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
			Review: ReviewHarnessConfig{
				AutoAfterChange: boolPtr(true),
			},
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			return VerificationReport{
				Steps: []VerificationStep{{
					Label:   "configured verification",
					Command: "configured verification callback",
					Status:  VerificationSkipped,
				}},
			}, true
		},
		EmitProgress: func(text string) {
			progress = append(progress, text)
		},
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "검증은 실행하지 않았습니다") {
		t.Fatalf("expected skipped verification disclosure, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected generated document to finalize after skipped-verification disclosure, got %d requests", len(provider.requests))
	}
	for _, line := range progress {
		if strings.Contains(line, "automatic post-change review") || strings.Contains(line, "자동 변경 후 리뷰") {
			t.Fatalf("generated document finalization should not enter post-change review path, progress=%q", line)
		}
	}
}

func TestAgentGeneratedDocumentArtifactSynthesizesFinalBeforePostCompletionTools(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 2개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 2 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"- Impact: resource lifetime bug.",
	}, "\n")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "The bug report document has been created at `Tavern/BugReport.md` with detailed findings and suggested fixes.",
				},
				StopReason: "stop",
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var progress []string
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
			Review: ReviewHarnessConfig{
				AutoAfterChange: boolPtr(true),
			},
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		EmitProgress: func(text string) {
			progress = append(progress, text)
		},
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") {
		t.Fatalf("expected generated document final reply, got %q", reply)
	}
	if !replyMentionsVerificationNotRun(reply) {
		t.Fatalf("expected synthesized final reply to disclose skipped verification, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected post-completion shell response to remain unrequested, got %d requests", len(provider.requests))
	}
	for _, line := range progress {
		if strings.Contains(line, "자동 변경 후 리뷰") || strings.Contains(line, "Automatic post-change review") {
			t.Fatalf("generated document finalization should not enter post-change review, progress=%q", line)
		}
	}
}

func TestAgentFinalizesGeneratedDocumentAfterPostChangeQualitySkip(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 1개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
	}, "\n")
	reportDir := filepath.Join(root, "Tavern")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "BugReport.md"), []byte(reportContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	reply := "Tavern/BugReport.md 문서를 생성했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다."
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{
		{Role: "user", Text: request},
		{Role: "assistant", Phase: messagePhaseFinalAnswerCandidate, Text: reply},
	}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-doc",
		Goal:   request,
		Status: patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-doc-001",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}
	var progress []string
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
			Review: ReviewHarnessConfig{
				AutoAfterChange: boolPtr(true),
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
		EmitProgress: func(text string) {
			progress = append(progress, text)
		},
	}

	lastFingerprint := ""
	revisionCount := 0
	exhaustedNudge := false
	needsModelTurn, err := agent.runAutomaticPostChangeReviewGate(context.Background(), request, &lastFingerprint, &revisionCount, &exhaustedNudge)
	if err != nil {
		t.Fatalf("runAutomaticPostChangeReviewGate: %v", err)
	}
	if needsModelTurn {
		t.Fatalf("expected generated document quality skip to avoid a repair turn")
	}
	if session.LastCodingHarnessReport == nil || !session.LastCodingHarnessReport.Approved {
		t.Fatalf("expected post-change quality skip to seed an approved coding harness report, got %#v", session.LastCodingHarnessReport)
	}
	finalReply, finalized, err := agent.maybeFinalizeGeneratedDocumentArtifactFinalReply(request, reply, true, false)
	if err != nil {
		t.Fatalf("maybeFinalizeGeneratedDocumentArtifactFinalReply: %v", err)
	}
	if !finalized || finalReply != reply {
		t.Fatalf("expected post-change quality skip to converge into final acceptance, finalized=%t reply=%q", finalized, finalReply)
	}
	if session.ActivePatchTransaction != nil {
		t.Fatalf("expected final acceptance to close the active patch transaction, got %#v", session.ActivePatchTransaction)
	}
	if len(session.PatchTransactions) == 0 {
		t.Fatalf("expected final acceptance to archive the document patch transaction")
	}
	if got := session.Messages[len(session.Messages)-1].Phase; got != messagePhaseFinalAnswer {
		t.Fatalf("expected final answer candidate to be accepted, got phase %q", got)
	}
	foundSkip := false
	for _, line := range progress {
		if strings.Contains(line, "generated document artifacts") || strings.Contains(line, "생성 문서 산출물") {
			foundSkip = true
			break
		}
	}
	if !foundSkip {
		t.Fatalf("expected generated document post-change skip progress, got %#v", progress)
	}
}

func TestAgentFinalizesGeneratedDocumentAfterPostChangeQualitySkipWithUnscopedPatchTransaction(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	if _, err := runGitCommand(ctx, root, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := runGitCommand(ctx, root, "config", "user.email", "kernforge-test@example.com"); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if _, err := runGitCommand(ctx, root, "config", "user.name", "Kernforge Test"); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	reportDir := filepath.Join(root, "Tavern")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	reportPath := filepath.Join(reportDir, "BugReport.md")
	if err := os.WriteFile(reportPath, []byte("# Initial Report\n\nPlaceholder before generated artifact.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile initial report: %v", err)
	}
	if _, err := runGitCommand(ctx, root, "add", "Tavern/BugReport.md"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := runGitCommand(ctx, root, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 1개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
	}, "\n")
	if err := os.WriteFile(reportPath, []byte(reportContent), 0o644); err != nil {
		t.Fatalf("WriteFile report: %v", err)
	}

	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	reply := "Tavern/BugReport.md 문서를 생성했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다."
	session := NewSession(root, "scripted", "model", "", "default")
	contract := buildAcceptanceContract(request, TurnIntentEditCode, false, true, false)
	session.AcceptanceContract = &contract
	session.Messages = []Message{
		{Role: "user", Text: request},
		{Role: "assistant", Phase: messagePhaseFinalAnswerCandidate, Text: reply},
	}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:       "patch-doc",
		Goal:     request,
		Status:   patchTransactionStatusActive,
		Warnings: []string{"tool reported workspace changes without changed_paths metadata"},
	}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
			Review: ReviewHarnessConfig{
				AutoAfterChange: boolPtr(true),
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	lastFingerprint := ""
	reviewed, needsRevision, reviewFeedback, fingerprint, err := agent.maybeRunPostChangeReview(ctx, request, lastFingerprint)
	if err != nil {
		t.Fatalf("maybeRunPostChangeReview: %v", err)
	}
	if !reviewed || needsRevision {
		t.Fatalf("expected generated document quality skip to avoid a repair turn, reviewed=%t needsRevision=%t fingerprint=%q feedback=%q changed=%#v report=%#v", reviewed, needsRevision, fingerprint, reviewFeedback, autoReviewChangedPaths(session, root), session.LastCodingHarnessReport)
	}
	if !isGeneratedDocumentArtifactQualityFingerprint(fingerprint) {
		t.Fatalf("expected generated document quality fingerprint, got %q", fingerprint)
	}
	if session.LastCodingHarnessReport == nil || !sessionHasDocumentArtifactQualityAcceptedHarness(session) {
		t.Fatalf("expected post-change quality skip to seed an accepted artifact report, got %#v", session.LastCodingHarnessReport)
	}
	finalReply, finalized, err := agent.maybeFinalizeGeneratedDocumentArtifactFinalReply(request, reply, true, false)
	if err != nil {
		t.Fatalf("maybeFinalizeGeneratedDocumentArtifactFinalReply: %v", err)
	}
	if !finalized {
		t.Fatalf("expected accepted generated document artifact to finalize instead of reopening model turns, reply=%q report=%#v", finalReply, session.LastCodingHarnessReport)
	}
	if !strings.Contains(finalReply, "Tavern/BugReport.md") {
		t.Fatalf("expected generated document final reply, got %q", finalReply)
	}
	if session.ActivePatchTransaction != nil {
		t.Fatalf("expected final acceptance to close the active patch transaction, got %#v", session.ActivePatchTransaction)
	}
}

func TestAgentGeneratedDocumentArtifactSynthesisAllowsSkippedVerificationDisclosureRepair(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 1개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
	}, "\n")
	if err := os.MkdirAll(filepath.Join(root, "Tavern"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Tavern", "BugReport.md"), []byte(reportContent), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: request}}
	session.LastVerification = &VerificationReport{
		Trigger:   "automatic",
		Workspace: root,
		ChangedPaths: []string{
			"Tavern/BugReport.md",
		},
		Steps: []VerificationStep{{
			Label:   "configured verification",
			Command: "configured verification callback",
			Status:  VerificationSkipped,
		}},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Workspace: ws,
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	initialReply := "Tavern/BugReport.md 문서를 생성했고 총 1개 버그를 기록했습니다."
	report := agent.buildCodingHarnessReport(initialReply, false, true)
	if report.Approved {
		t.Fatalf("expected missing skipped-verification disclosure to block the initial report, got %#v", report)
	}
	if !agent.shouldSynthesizeGeneratedDocumentArtifactFinalReply(request, &report, true) {
		t.Fatalf("expected skipped verification disclosure gap to be answer-only for generated document artifacts, got %#v", report)
	}
	synthesized := agent.synthesizeGeneratedDocumentArtifactFinalReply(&report)
	if !strings.Contains(synthesized, "빌드/테스트 검증은 실행하지 않았습니다") {
		t.Fatalf("expected synthesized skipped verification disclosure, got %q", synthesized)
	}
	repaired := agent.buildCodingHarnessReport(synthesized, false, true)
	if !repaired.Approved {
		t.Fatalf("expected synthesized disclosure to satisfy the final harness, got %#v", repaired)
	}
}

func TestAgentGeneratedDocumentArtifactSynthesizesSkippedVerificationFinalWithoutReviewerLoop(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 1개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
	}, "\n")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 1개 버그를 기록했습니다.",
				},
				StopReason: "stop",
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}),
		},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{
				Role: "assistant",
				Text: "NEEDS_REVISION\nThis final-answer reviewer should not run for a generated document artifact final.",
			},
			StopReason: "stop",
		}},
	}
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: request,
	}}
	session.TaskState = &TaskState{
		Goal:        request,
		Phase:       "execution",
		PlanSummary: "1. Write the generated document artifact\n2. Summarize without extra validation",
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
			Review: ReviewHarnessConfig{
				AutoAfterChange: boolPtr(true),
			},
		},
		Client:         provider,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Tools:          NewToolRegistry(NewWriteFileTool(ws)),
		Workspace:      ws,
		Session:        session,
		Store:          store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			return VerificationReport{
				ChangedPaths: []string{"Tavern/BugReport.md"},
				Steps: []VerificationStep{{
					Label:   "configured verification",
					Command: "configured verification callback",
					Status:  VerificationSkipped,
				}},
			}, true
		},
	}

	reply, err := agent.completeLoop(context.Background(), false, true, false)
	if err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") || !strings.Contains(reply, "빌드/테스트 검증은 실행하지 않았습니다") {
		t.Fatalf("expected synthesized generated document final reply with skipped verification disclosure, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected no extra model turn after synthesized document final, got %d requests", len(provider.requests))
	}
	if len(reviewer.requests) != 0 {
		t.Fatalf("expected generated document finalization to skip interactive final reviewer, got %d reviewer requests", len(reviewer.requests))
	}
}

func TestAgentBuffersGeneratedDocumentFinalAnswerDeltaUntilAccepted(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 2개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 2 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"- Impact: resource lifetime bug.",
	}, "\n")
	provider := &streamingScriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 2개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
				},
				StopReason: "stop",
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var emitted strings.Builder
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:             provider,
		Tools:              NewToolRegistry(NewWriteFileTool(ws)),
		Workspace:          ws,
		Session:            session,
		Store:              store,
		EmitAssistantDelta: func(text string) { emitted.WriteString(text) },
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") {
		t.Fatalf("expected final document summary, got %q", reply)
	}
	if emitted.String() != "" {
		t.Fatalf("expected generated document final candidate to be buffered until accepted, got streamed %q", emitted.String())
	}
	if agent.lastEmittedText != "" {
		t.Fatalf("buffered final candidate must not be recorded as emitted text, got %q", agent.lastEmittedText)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected the shell-validation response to remain unrequested, got %d main requests", len(provider.requests))
	}
	if provider.requests[1].OnTextDelta != nil {
		t.Fatalf("expected final document summary request to buffer text deltas while final gates can still reject it")
	}
}

func TestAgentFinalizesGeneratedDocumentApplyPatchWithoutPostFinalLoop(t *testing.T) {
	root := t.TempDir()
	reportDir := filepath.Join(root, "Tavern")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	reportPath := filepath.Join(reportDir, "BugReport.md")
	if err := os.WriteFile(reportPath, []byte("# Draft\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: Tavern/BugReport.md",
		"@@",
		"-# Draft",
		"+# Tavern Bug Report",
		"+",
		"+소스코드 검토 결과 총 1개 버그를 문서로 생성했습니다.",
		"+",
		"+| Severity | Count |",
		"+|----------|-------|",
		"+| Critical | 1 |",
		"+| Total | 1 |",
		"+",
		"+## BUG-001",
		"+- File: Tavern/Tavern/RuntimeManager.cpp",
		"+- Impact: crash risk.",
		"*** End Patch",
	}, "\n")
	provider := &streamingScriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{"patch": patch}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 1개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
				},
				StopReason: "stop",
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var emittedDelta strings.Builder
	var emittedAssistant []string
	var progress []string
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
			Review: ReviewHarnessConfig{
				AutoAfterChange: boolPtr(true),
			},
		},
		Client:             provider,
		Tools:              NewToolRegistry(NewApplyPatchTool(ws), NewRunShellTool(ws)),
		Workspace:          ws,
		Session:            session,
		Store:              store,
		EmitAssistantDelta: func(text string) { emittedDelta.WriteString(text) },
		EmitAssistant: func(text string) {
			emittedAssistant = append(emittedAssistant, text)
		},
		EmitProgress: func(text string) {
			progress = append(progress, text)
		},
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") || !strings.Contains(reply, "검증은 실행하지 않았습니다") {
		t.Fatalf("expected generated document final answer with verification disclosure, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected the post-final shell response to remain unrequested, got %d requests", len(provider.requests))
	}
	if provider.requests[1].OnTextDelta != nil {
		t.Fatalf("expected final document summary request to buffer text deltas after apply_patch")
	}
	if emittedDelta.String() != "" {
		t.Fatalf("expected final candidate delta to stay hidden until accepted, got %q", emittedDelta.String())
	}
	for _, text := range emittedAssistant {
		if strings.Contains(text, "Tavern/BugReport.md 문서를 생성") {
			t.Fatalf("final candidate was emitted before acceptance: %q", text)
		}
	}
	for _, line := range progress {
		if strings.Contains(line, "automatic post-change review") || strings.Contains(line, "자동 변경 후 리뷰") {
			t.Fatalf("generated document apply_patch finalization should not enter post-change review path, progress=%q", line)
		}
	}
	if session.ActivePatchTransaction != nil {
		t.Fatalf("expected apply_patch document transaction to finalize, got %#v", session.ActivePatchTransaction)
	}
	if len(session.PatchTransactions) == 0 || session.PatchTransactions[0].ChangedPaths()[0] != "Tavern/BugReport.md" {
		t.Fatalf("expected archived document patch transaction, got %#v", session.PatchTransactions)
	}
}

func TestAgentSynthesizesGeneratedDocumentCountMismatchWithoutPostFinalLoop(t *testing.T) {
	root := t.TempDir()
	reportLines := []string{
		"# Tavern Client Bug Report",
		"",
		"각 소스코드 파일들을 검토해서 총 26개 버그를 문서화했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 4 |",
		"| High | 7 |",
		"| Medium | 9 |",
		"| Low | 6 |",
		"| Total | 26 |",
		"",
	}
	for i := 1; i <= 26; i++ {
		reportLines = append(reportLines,
			fmt.Sprintf("## BUG-%03d", i),
			"- File: Tavern/Tavern/RuntimeManager.cpp",
			"- Impact: documented issue.",
			"",
		)
	}
	provider := &streamingScriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": strings.Join(reportLines, "\n"),
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: strings.Join([]string{
						"The bug report document has been reviewed and is now in final form.",
						"",
						"27 documented bugs were found.",
						"",
						"| Severity | Count |",
						"|----------|-------|",
						"| Critical | 4 |",
						"| High | 7 |",
						"| Medium | 9 |",
						"| Low | 6 |",
						"| **Total** | **26** |",
						"",
						"Build/test verification was not run because this is a documentation-only artifact.",
					}, "\n"),
				},
				StopReason: "stop",
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo Reviewing Tavern/BugReport.md for final validation"}),
			toolCallResponse("read_file", map[string]any{"path": "Tavern/BugReport.md"}),
			{
				Message: Message{
					Role: "assistant",
					Text: "This fallback final answer should not be requested.",
				},
				StopReason: "stop",
			},
		},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{
				Role: "assistant",
				Text: "NEEDS_REVISION\nThis reviewer should not run after document content is accepted.",
			},
			StopReason: "stop",
		}},
	}
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	contract := buildAcceptanceContract(request, TurnIntentEditCode, false, true, false)
	session.AcceptanceContract = &contract
	session.Messages = []Message{{
		Role: "user",
		Text: request,
	}}
	session.TaskState = &TaskState{
		Goal:        request,
		Phase:       "execution",
		PlanSummary: "1. Write the generated document artifact\n2. Summarize without extra validation",
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var emittedDelta strings.Builder
	var progress []string
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
			Review: ReviewHarnessConfig{
				AutoAfterChange: boolPtr(true),
			},
		},
		Client:             provider,
		ReviewerClient:     reviewer,
		ReviewerModel:      "reviewer-model",
		Tools:              NewToolRegistry(NewWriteFileTool(ws), NewRunShellTool(ws), &staticTool{name: "read_file", output: "read should not run"}),
		Workspace:          ws,
		Session:            session,
		Store:              store,
		EmitAssistantDelta: func(text string) { emittedDelta.WriteString(text) },
		EmitProgress: func(text string) {
			progress = append(progress, text)
		},
	}

	reply, err := agent.completeLoop(context.Background(), false, true, false)
	if err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") || !strings.Contains(reply, "빌드/테스트 검증은 실행하지 않았습니다") {
		t.Fatalf("expected synthesized generated document final answer, got %q with %d provider requests and messages %#v", reply, len(provider.requests), session.Messages)
	}
	if strings.Contains(reply, "27") {
		t.Fatalf("expected synthesized final answer to omit inconsistent bug counts, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected post-final shell/read responses to remain unrequested, got %d request(s)", len(provider.requests))
	}
	if len(reviewer.requests) != 0 {
		t.Fatalf("expected generated document answer-only repair to skip final reviewer, got %d reviewer request(s)", len(reviewer.requests))
	}
	if emittedDelta.String() != "" {
		t.Fatalf("expected inconsistent final candidate to stay hidden until accepted, got %q", emittedDelta.String())
	}
	for _, line := range progress {
		if strings.Contains(line, "automatic post-change review") || strings.Contains(line, "자동 변경 후 리뷰") {
			t.Fatalf("generated document count repair should not enter post-change review path, progress=%q", line)
		}
	}
	if session.LastCodingHarnessReport == nil || !session.LastCodingHarnessReport.Approved {
		t.Fatalf("expected synthesized reply to refresh an approved harness report, got %#v", session.LastCodingHarnessReport)
	}
}

func TestAgentFinalizesGeneratedDocumentBeforeWebResearchChurn(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "Tavern", "BugReport.md")
	reportContent := strings.Join([]string{
		"# Tavern Client Bug Report",
		"",
		"각 소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성했습니다.",
		"총 2개 버그를 문서화했습니다.",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: documented issue.",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"- Impact: documented issue.",
	}, "\n")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(reportPath, []byte(reportContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		SourcePrompt: request,
	}
	session.TaskState = &TaskState{
		Goal: request,
	}
	session.Messages = []Message{{
		Role: "user",
		Text: request,
	}}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Size:         int64(len(reportContent)),
				ContentChars: len([]rune(reportContent)),
				Substantive:  true,
				Checks:       []string{"text readable", "content is substantive"},
			}},
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		Status:        patchTransactionStatusCommitted,
		WorkspaceRoot: root,
		Entries: []PatchTransactionEntry{{
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write",
			}},
		}},
	}}
	webTool := &staticTool{
		name:   "mcp__web_research__search_web",
		output: "external source that should not be used",
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("mcp__web_research__search_web", map[string]any{
				"query": "C++ static code analysis best practices",
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "This fallback final answer should not be requested.",
				},
				StopReason: "stop",
			},
		},
	}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(webTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	reply, err := agent.Reply(context.Background(), "Please provide the final answer now.")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") || !strings.Contains(reply, "빌드/테스트 검증은 실행하지 않았습니다") {
		t.Fatalf("expected synthesized generated document final answer, got %q", reply)
	}
	if strings.Contains(reply, "fallback") {
		t.Fatalf("expected post-completion web research churn to be finalized before fallback request, got %q", reply)
	}
	if webTool.calls != 0 {
		t.Fatalf("post-completion web research tool should not execute, got %d calls", webTool.calls)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected generated document finalization before web research retry, got %d provider request(s)", len(provider.requests))
	}
}

func TestAgentRepairsGeneratedDocumentThenSynthesizesBadFinalSummary(t *testing.T) {
	root := t.TempDir()
	badReport := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 3개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 3 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"- Impact: resource lifetime risk.",
	}, "\n")
	repairPatch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: Tavern/BugReport.md",
		"@@",
		"-소스코드 검토 결과 총 3개 버그를 문서로 생성했습니다.",
		"+소스코드 검토 결과 총 2개 버그를 문서로 생성했습니다.",
		"@@",
		"-| Total | 3 |",
		"+| Total | 2 |",
		"*** End Patch",
	}, "\n")
	provider := &streamingScriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": badReport,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 3개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
				},
				StopReason: "stop",
			},
			toolCallResponse("apply_patch", map[string]any{"patch": repairPatch}),
			{
				Message: Message{
					Role: "assistant",
					Text: strings.Join([]string{
						"The document is complete.",
						"",
						"3 documented bugs were found.",
						"",
						"| Severity | Count |",
						"|----------|-------|",
						"| Critical | 1 |",
						"| High | 1 |",
						"| **Total** | **2** |",
						"",
						"Build/test verification was not run because this is a documentation-only artifact.",
					}, "\n"),
				},
				StopReason: "stop",
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run after repaired document"}),
			{
				Message: Message{
					Role: "assistant",
					Text: "This fallback final answer should not be requested.",
				},
				StopReason: "stop",
			},
		},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{
				Role: "assistant",
				Text: "NEEDS_REVISION\nThis reviewer should not run after generated document repair.",
			},
			StopReason: "stop",
		}},
	}
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: request}}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var progress []string
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
			Review: ReviewHarnessConfig{
				AutoAfterChange: boolPtr(true),
			},
		},
		Client:         provider,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Tools:          NewToolRegistry(NewWriteFileTool(ws), NewApplyPatchTool(ws), NewRunShellTool(ws)),
		Workspace:      ws,
		Session:        session,
		Store:          store,
		EmitProgress: func(text string) {
			progress = append(progress, text)
		},
	}

	reply, err := agent.completeLoop(context.Background(), false, true, false)
	if err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") || !strings.Contains(reply, "빌드/테스트 검증은 실행하지 않았습니다") {
		t.Fatalf("expected synthesized generated document final answer, got %q", reply)
	}
	if strings.Contains(reply, "3 documented bugs") || strings.Contains(reply, "총 3개") {
		t.Fatalf("expected synthesized final answer to omit stale bug counts, got %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected post-repair shell response to remain unrequested, got %d request(s)", len(provider.requests))
	}
	if len(reviewer.requests) != 0 {
		t.Fatalf("expected generated document repair finalization to skip reviewer, got %d reviewer request(s)", len(reviewer.requests))
	}
	if session.ActivePatchTransaction != nil {
		t.Fatalf("expected repaired generated document transaction to finalize, got %#v", session.ActivePatchTransaction)
	}
	for _, line := range progress {
		if strings.Contains(line, "Running automatic post-change review") || strings.Contains(line, "자동 변경 후 리뷰를 실행") {
			t.Fatalf("generated document repair should not enter code review path, progress=%q", line)
		}
	}
}

func TestAgentGeneratedDocumentIgnoresEndTurnFalseAfterArtifactWrite(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 1개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
	}, "\n")
	endTurnFalse := false
	finalReply := "Tavern/BugReport.md 문서를 생성했고 총 1개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message:    Message{Role: "assistant", Text: finalReply},
				StopReason: "completed",
				EndTurn:    &endTurnFalse,
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var progress []string
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
			Review: ReviewHarnessConfig{
				AutoAfterChange: boolPtr(true),
			},
		},
		Client: provider,
		Tools:  NewToolRegistry(NewWriteFileTool(ws), NewRunShellTool(ws)),
		EmitProgress: func(text string) {
			progress = append(progress, text)
		},
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != finalReply {
		t.Fatalf("expected generated document final reply, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected generated document end_turn=false text to finalize without another request, got %d", len(provider.requests))
	}
	if session.Messages[len(session.Messages)-1].Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected generated document reply to be accepted as final, got %#v", session.Messages[len(session.Messages)-1])
	}
	for _, line := range progress {
		lowerLine := strings.ToLower(line)
		if strings.Contains(lowerLine, "automatic post-change review") || strings.Contains(line, "자동 변경 후 리뷰") {
			t.Fatalf("generated document end_turn=false finalization should not enter post-change review path, progress=%q", line)
		}
	}
}

func TestAgentGeneratedDocumentIgnoresEndTurnFalseCommentaryPhaseAfterArtifactWrite(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 1개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
	}, "\n")
	endTurnFalse := false
	finalReply := "Tavern/BugReport.md 문서를 생성했고 총 1개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role:  "assistant",
					Text:  finalReply,
					Phase: messagePhaseCommentary,
				},
				StopReason: "completed",
				EndTurn:    &endTurnFalse,
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	shellTool := &staticTool{name: "run_shell", output: "shell should not run"}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
			Review: ReviewHarnessConfig{
				AutoAfterChange: boolPtr(true),
			},
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), shellTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != finalReply {
		t.Fatalf("expected generated document final reply, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected generated document commentary-phase end_turn=false text to finalize without another request, got %d", len(provider.requests))
	}
	if shellTool.calls != 0 {
		t.Fatalf("post-final shell validation should not execute, got %d call(s)", shellTool.calls)
	}
	if session.Messages[len(session.Messages)-1].Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected commentary-phase generated document reply to be promoted to final, got %#v", session.Messages[len(session.Messages)-1])
	}
}

func TestAgentGeneratedDocumentSkippedVerificationCompletesSelfDrivingState(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 1개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
	}, "\n")
	finalReply := "Tavern/BugReport.md 문서를 생성했고 총 1개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message:    Message{Role: "assistant", Text: finalReply},
				StopReason: "stop",
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
			Review: ReviewHarnessConfig{
				AutoAfterChange: boolPtr(true),
			},
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), NewRunShellTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			return VerificationReport{
				Steps: []VerificationStep{{
					Label:   "document-only verification skipped",
					Command: "document-only verification skipped",
					Status:  VerificationSkipped,
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != finalReply {
		t.Fatalf("expected generated document final reply, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected generated document to finalize without post-final tool calls, got %d requests", len(provider.requests))
	}
	if session.TaskState == nil {
		t.Fatalf("expected self-driving task state to exist")
	}
	if session.TaskState.Phase != "done" {
		t.Fatalf("expected document-only skipped verification to complete task state, got %#v", session.TaskState)
	}
	if slices.Contains(session.TaskState.PendingChecks, verificationPendingCheck) {
		t.Fatalf("expected generated document finalization to clear verification pending check, got %#v", session.TaskState.PendingChecks)
	}
	for _, item := range session.Plan {
		if item.Status != "completed" {
			t.Fatalf("expected generated document finalization to complete shared plan, got %#v", session.Plan)
		}
	}
}

func TestAgentGeneratedDocumentExplicitVerificationRequirementKeepsPendingState(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		Goal:          "문서를 만들고 검증까지 실행해",
		Phase:         "execution",
		PendingChecks: []string{verificationPendingCheck},
	}
	session.AcceptanceContract = &AcceptanceContract{
		ID:                   "accept-doc-verify",
		SourcePrompt:         "문서를 만들고 검증까지 실행해",
		VerificationRequired: true,
	}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-doc",
		Status: patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-doc-001",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:        "Tavern/BugReport.md",
				Kind:        "document",
				Substantive: true,
			}},
		},
	}
	skipped := VerificationReport{
		Steps: []VerificationStep{{
			Label:   "required verification skipped",
			Command: "go test ./...",
			Status:  VerificationSkipped,
		}},
	}
	session.LastVerification = &skipped
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	agent.finalizeTaskStateOnAcceptedFinalAnswer("Tavern/BugReport.md 생성 완료. 검증은 실행하지 않았습니다.", true)
	if session.TaskState.Phase != "recovery" {
		t.Fatalf("expected explicit verification requirement to keep task in recovery, got %#v", session.TaskState)
	}
	if !slices.Contains(session.TaskState.PendingChecks, verificationPendingCheck) {
		t.Fatalf("expected required verification pending check to remain, got %#v", session.TaskState.PendingChecks)
	}
}

func TestAgentPreservesAcceptanceContextForInternalGoalPrompt(t *testing.T) {
	root := t.TempDir()
	original := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	contract := buildAcceptanceContract(original, TurnIntentEditCode, false, true, false)
	session.AcceptanceContract = &contract
	report := &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 2048,
			}},
		},
	}
	session.LastCodingHarnessReport = report
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	_, err := agent.Reply(context.Background(), "Autonomous goal iteration 2 for goal goal-1.\n\nObjective:\n"+original)
	if err == nil || !strings.Contains(err.Error(), "no model provider") {
		t.Fatalf("expected provider error after state preparation, got %v", err)
	}
	if session.TaskState == nil || session.TaskState.Goal != original {
		t.Fatalf("expected internal goal prompt to preserve task goal %q, got %#v", original, session.TaskState)
	}
	if session.AcceptanceContract == nil || session.AcceptanceContract.SourcePrompt != original {
		t.Fatalf("expected internal goal prompt to preserve acceptance contract, got %#v", session.AcceptanceContract)
	}
	if session.LastCodingHarnessReport != report {
		t.Fatalf("expected internal goal prompt to preserve latest artifact harness report")
	}
}

func TestAgentBlocksGeneratedDocumentPostWriteShellValidationAfterHarnessFeedback(t *testing.T) {
	root := t.TempDir()
	badReportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 3 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
	}, "\n")
	goodReportContent := strings.ReplaceAll(badReportContent, "| Total | 3 |", "| Total | 2 |")
	shellTool := &staticTool{name: "run_shell", output: "shell validation executed"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": badReportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 3개 버그를 기록했습니다.",
				},
				StopReason: "stop",
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo Reviewing Tavern/BugReport.md for final validation"}),
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": goodReportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 수정했고 총 2개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), shellTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "총 2개") {
		t.Fatalf("expected repaired generated document final reply, got %q", reply)
	}
	if shellTool.calls != 0 {
		t.Fatalf("generated document post-write shell validation should be blocked, got %d shell calls", shellTool.calls)
	}
	if !sessionContainsToolResultText(session, "call-1", "generated document artifact turns do not run shell or review validation") {
		t.Fatalf("expected blocked shell validation tool result, messages=%#v", session.Messages)
	}
	if session.LastCodingHarnessReport == nil || !session.LastCodingHarnessReport.Approved {
		t.Fatalf("expected repaired document harness report to be approved, got %#v", session.LastCodingHarnessReport)
	}
}

func TestAgentBlocksGeneratedDocumentPostApprovalToolChurn(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	session.Messages = []Message{{Role: "user", Text: request}}
	session.LastCodingHarnessReport = &CodingHarnessReport{Approved: true}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	for _, call := range []ToolCall{
		{Name: "read_file", Arguments: `{"path":"Tavern/BugReport.md"}`},
		{Name: "list_files", Arguments: `{"path":"Tavern"}`},
		{Name: "grep", Arguments: `{"path":"Tavern/BugReport.md","pattern":"BUG-"}`},
	} {
		if !agent.shouldBlockGeneratedDocumentArtifactValidationToolCalls(request, []ToolCall{call}) {
			t.Fatalf("expected approved generated document artifact to block post-completion tool call %#v", call)
		}
	}
	for _, call := range []ToolCall{
		{Name: "write_file", Arguments: `{"path":"Tavern/BugReport.md","content":"# report"}`},
		{Name: "replace_in_file", Arguments: `{"path":"Tavern/BugReport.md","old":"before","new":"after"}`},
		{Name: "apply_patch", Arguments: `{"patch":"*** Begin Patch\n*** Update File: Tavern/BugReport.md\n@@\n-before\n+after\n*** End Patch\n"}`},
	} {
		if !agent.shouldBlockGeneratedDocumentArtifactValidationToolCalls(request, []ToolCall{call}) {
			t.Fatalf("approved generated document artifact should block post-completion edit churn: %#v", call)
		}
	}
}

func TestAgentKeepsGeneratedDocumentFinalOnlyAfterGenericFollowup(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해",
	}}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 256,
			}},
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Tools: NewToolRegistry(
			&staticTool{name: "read_file", output: "read"},
			&staticTool{name: "run_shell", output: "shell"},
			&staticTool{name: "apply_patch", output: "patch"},
		),
	}

	genericFollowup := "Please provide the final answer now."
	if !agent.changesAreGeneratedDocumentArtifactsForTurn(genericFollowup) {
		t.Fatalf("accepted document-artifact harness should outlive generic final-answer follow-up prompts")
	}
	if !agent.shouldBlockGeneratedDocumentArtifactValidationToolCalls(genericFollowup, []ToolCall{{Name: "read_file", Arguments: `{"path":"Tavern/BugReport.md"}`}}) {
		t.Fatalf("accepted document-artifact harness should block post-completion inspection churn")
	}
	plan := agent.buildTurnToolExposurePlan(nil, genericFollowup, false, false, false, false, false)
	if !plan.GeneratedDocumentFinalOnly || !plan.SuppressInteractiveWorkers {
		t.Fatalf("accepted document-artifact harness should force final-only exposure, got %#v", plan)
	}
	for _, name := range []string{"read_file", "run_shell", "apply_patch"} {
		if !plan.DisabledTools[name] {
			t.Fatalf("generated document final-only exposure must disable %s, got %#v", name, plan.DisabledTools)
		}
	}
}

func TestAgentSynthesizesFinalForApprovedGeneratedDocumentToolChurn(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 2 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"- Impact: resource lifetime bug.",
	}, "\n")
	if err := os.MkdirAll(filepath.Join(root, "Tavern"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Tavern", "BugReport.md"), []byte(reportContent), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: request}}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: len(reportContent),
			}},
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	readTool := &staticTool{name: "read_file", output: "read should not run"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("read_file", map[string]any{"path": "Tavern/BugReport.md"}),
			{
				Message: Message{
					Role: "assistant",
					Text: "This follow-up should not be requested.",
				},
				StopReason: "stop",
			},
		},
	}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(readTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	reply, err := agent.completeLoop(context.Background(), false, false, false)
	if err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") || !strings.Contains(reply, "빌드/테스트 검증은 실행하지 않았습니다") {
		t.Fatalf("expected synthesized generated document final reply, got %q", reply)
	}
	if readTool.calls != 0 {
		t.Fatalf("read_file should not execute after generated document artifact approval, got %d call(s)", readTool.calls)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected runtime to finish without a follow-up model request, got %d request(s)", len(provider.requests))
	}
	if !sessionContainsToolResultText(session, "call-1", "generated document artifact turns do not run shell or review validation") {
		t.Fatalf("expected blocked tool result to be recorded, messages=%#v", session.Messages)
	}
	if session.Messages[len(session.Messages)-1].Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected synthesized reply to be accepted as final, got %#v", session.Messages[len(session.Messages)-1])
	}
}

func TestAgentHidesToolsForGeneratedDocumentFinalOnlyTurn(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
	}, "\n")
	reportPath := filepath.Join(root, "Tavern", "BugReport.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(reportPath, []byte(reportContent), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: request}}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: len(reportContent),
			}},
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	readTool := &staticTool{name: "read_file", output: "read should not run"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("read_file", map[string]any{"path": "Tavern/BugReport.md"}),
			{
				Message: Message{
					Role: "assistant",
					Text: "This follow-up should not be requested.",
				},
				StopReason: "stop",
			},
		},
	}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(readTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	reply, err := agent.completeLoop(context.Background(), false, false, false)
	if err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") || !strings.Contains(reply, "빌드/테스트 검증은 실행하지 않았습니다") {
		t.Fatalf("expected synthesized generated document final reply, got %q", reply)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected final-only turn to finish without another model request, got %d request(s)", len(provider.requests))
	}
	if len(provider.requests[0].Tools) != 0 {
		t.Fatalf("expected final-only generated document turn to hide tools, got %#v", provider.requests[0].Tools)
	}
	if readTool.calls != 0 {
		t.Fatalf("read_file should not execute in generated document final-only mode, got %d call(s)", readTool.calls)
	}
}

func TestAgentSuppressesInteractiveWorkersForGeneratedDocumentFinalOnlyTurn(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "worker_target.txt"), []byte("AntiTamperGuard evidence is present here.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: request}}
	session.TaskState = &TaskState{
		Goal:                  request,
		ExecutorFocusNode:     "plan-01",
		ExecutorParallelNodes: []string{"plan-02"},
	}
	session.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{ID: "plan-01", Title: "Finalize generated bug report", Kind: "report", Status: "in_progress", LastUpdated: time.Now()},
			{ID: "plan-02", Title: "Inspect AntiTamperGuard evidence", Kind: "inspection", Status: "ready", LastUpdated: time.Now()},
		},
		LastUpdated: time.Now(),
	}
	session.TaskGraph.Normalize()
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 4096,
			}},
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Goal:   request,
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{
				Role: "assistant",
				Text: "Tavern/BugReport.md 문서 산출물이 완료되었습니다. 빌드/테스트 검증은 실행하지 않았습니다.",
			},
			StopReason: "stop",
		}},
	}
	ws := Workspace{BaseRoot: root, Root: root, Shell: "powershell"}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     buildRegistry(ws, nil),
		Workspace: ws,
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	reply, err := agent.completeLoop(context.Background(), false, false, false)
	if err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") {
		t.Fatalf("expected generated document final reply, got %q", reply)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected exactly one model request, got %d", len(provider.requests))
	}
	node, ok := session.TaskGraph.Node("plan-02")
	if !ok {
		t.Fatalf("expected secondary node to remain in graph")
	}
	if node.ReadOnlyWorkerTool != "" || node.ReadOnlyWorkerSummary != "" {
		t.Fatalf("expected generated-document finalization to suppress read-only worker, got %#v", node)
	}
	for _, event := range session.TaskState.Events {
		if strings.Contains(event.Kind, "parallel_worker") {
			t.Fatalf("expected no parallel worker events for generated-document finalization, got %#v", session.TaskState.Events)
		}
	}
}

func TestAgentTurnToolExposurePlanSuppressesWorkersForAnswerOnlyStates(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Session: session,
		Tools: NewToolRegistry(
			&staticTool{name: "read_file", output: "read"},
			&staticTool{name: "apply_patch", output: "patch"},
			&staticTool{name: "mcp__web_research__search_web", output: "web"},
			&mutableRegistryTool{
				def: ToolDefinition{
					Name:        "dispatch_only",
					Description: "hidden dispatch-only tool",
					InputSchema: emptyObjectSchema(),
				},
				output: "hidden",
				hidden: true,
			},
		),
	}

	plan := agent.buildTurnToolExposurePlan(nil, "fix code", false, true, false, false, true)
	if !plan.SuppressInteractiveWorkers {
		t.Fatalf("final-answer-only correction must suppress interactive workers")
	}
	for _, name := range []string{"read_file", "apply_patch", "mcp__web_research__search_web", "dispatch_only"} {
		if !plan.DisabledTools[name] {
			t.Fatalf("final-answer-only correction must disable %s, got %#v", name, plan.DisabledTools)
		}
	}
	if plan.GeneratedDocumentFinalOnly {
		t.Fatalf("ordinary answer-only correction should not be classified as generated-document finalization")
	}

	plan = agent.buildTurnToolExposurePlan(nil, "fix code", false, false, false, false, true)
	if plan.SuppressInteractiveWorkers {
		t.Fatalf("ordinary local-code web policy should not suppress interactive workers")
	}
	if !plan.DisabledTools["mcp__web_research__search_web"] {
		t.Fatalf("ordinary local-code web policy should hide web research tools, got %#v", plan.DisabledTools)
	}
	for _, name := range []string{"read_file", "apply_patch"} {
		if plan.DisabledTools[name] {
			t.Fatalf("ordinary local-code web policy should keep local tool %s visible, got %#v", name, plan.DisabledTools)
		}
	}

	plan = agent.buildTurnToolExposurePlan(nil, "fix code", false, false, true, false, false)
	if !plan.SuppressInteractiveWorkers {
		t.Fatalf("out-of-scope verification final-only state must suppress interactive workers")
	}
	for _, name := range []string{"read_file", "apply_patch", "mcp__web_research__search_web", "dispatch_only"} {
		if !plan.DisabledTools[name] {
			t.Fatalf("out-of-scope verification final-only state must disable %s, got %#v", name, plan.DisabledTools)
		}
	}
}

func TestAgentBlocksModelRequestedHiddenDispatchOnlyTool(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	hiddenTool := &mutableRegistryTool{
		def: ToolDefinition{
			Name:        "dispatch_only",
			Description: "hidden dispatch-only tool",
			InputSchema: emptyObjectSchema(),
		},
		output: "hidden executed",
		hidden: true,
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("dispatch_only", map[string]any{"value": "from-model"}),
			{Message: Message{Role: "assistant", Text: "숨김 도구는 실행하지 않았습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(hiddenTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "숨김 dispatch 도구를 호출해봐")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "실행하지") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if hiddenTool.lastInput != nil {
		t.Fatalf("model-requested hidden dispatch-only tool must not execute, got input %#v", hiddenTool.lastInput)
	}
	if len(provider.requests) == 0 {
		t.Fatalf("expected provider request")
	}
	if chatRequestHasTool(provider.requests[0], "dispatch_only") {
		t.Fatalf("hidden dispatch-only tool must not be model-visible")
	}
	blocked := false
	for _, msg := range session.Messages {
		if msg.Role != "tool" || msg.ToolCallID != "call-1" {
			continue
		}
		blocked = strings.Contains(msg.Text, "NOT_EXECUTED") &&
			toolMetaString(msg.ToolMeta, "result_class") == "model_hidden_tool_block" &&
			toolMetaBool(msg.ToolMeta, "hidden_from_model")
	}
	if !blocked {
		t.Fatalf("expected hidden model tool call to be closed with a blocked result, got %#v", session.Messages)
	}
}

func TestAgentSuppressesInteractiveWorkersAfterGeneratedDocumentWriteBeforeHarnessApproval(t *testing.T) {
	root := t.TempDir()
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: request}}
	session.TaskState = &TaskState{
		Goal:                  request,
		ExecutorParallelNodes: []string{"plan-02"},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Goal:   request,
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	if !agent.shouldSuppressInteractiveWorkersForTurn(request) {
		t.Fatalf("expected generated document write to suppress automatic interactive workers before harness approval")
	}
	if agent.shouldSuppressInteractiveWorkersForTurn("Tavern/BugReport.md 검증해") {
		t.Fatalf("explicit local verification requests should remain allowed to run tools and workers")
	}
}

func TestAgentBlocksGeneratedDocumentInspectionAfterContentQualityAccepted(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 2 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"- Impact: resource lifetime bug.",
	}, "\n")
	readTool := &staticTool{name: "read_file", output: "read should not run"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 go test ./... 검증도 통과했습니다.",
				},
				StopReason: "stop",
			},
			toolCallResponse("read_file", map[string]any{"path": "Tavern/BugReport.md"}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), readTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "빌드/테스트 검증은 실행하지 않았습니다") {
		t.Fatalf("expected synthesized safe final answer, got %q", reply)
	}
	if readTool.calls != 0 {
		t.Fatalf("read_file should be blocked after artifact content quality is accepted, got %d calls", readTool.calls)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected runtime to synthesize the safe final answer without another model/tool turn, got %d requests", len(provider.requests))
	}
	if session.LastCodingHarnessReport == nil || !session.LastCodingHarnessReport.Approved {
		t.Fatalf("expected synthesized final answer to refresh an approved harness report, got %#v", session.LastCodingHarnessReport)
	}
}

func TestAgentFinalizesGeneratedDocumentPreambleWithToolCalls(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 2 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"- Impact: resource lifetime bug.",
	}, "\n")
	readTool := &staticTool{name: "read_file", output: "read should not run"}
	readArgs, _ := json.Marshal(map[string]any{"path": "Tavern/BugReport.md"})
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 2개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
					ToolCalls: []ToolCall{{
						ID:        "call-read-after-final",
						Name:      "read_file",
						Arguments: string(readArgs),
					}},
				},
				StopReason: "tool_calls",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "This follow-up should not be requested.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), readTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") || !strings.Contains(reply, "빌드/테스트 검증은 실행하지 않았습니다") {
		t.Fatalf("expected generated document final reply, got %q", reply)
	}
	if readTool.calls != 0 {
		t.Fatalf("read_file should not execute when final-looking document preamble has tool calls, got %d calls", readTool.calls)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected runtime to finish without post-final tool churn, got %d request(s)", len(provider.requests))
	}
	if !sessionContainsToolResultText(session, "call-read-after-final", "generated document artifact turns do not run shell or review validation") {
		t.Fatalf("expected blocked read tool result to be recorded, messages=%#v", session.Messages)
	}
	if session.LastCodingHarnessReport == nil || !session.LastCodingHarnessReport.Approved {
		t.Fatalf("expected final preamble harness report to approve, got %#v", session.LastCodingHarnessReport)
	}
}

func TestAgentSynthesizesGeneratedDocumentFinalWhenValidationToolArrivesBeforeHarnessReport(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 2 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"- Impact: resource lifetime bug.",
	}, "\n")
	shellTool := &staticTool{name: "run_shell", output: "should not run"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-shell-before-harness",
						Name:      "run_shell",
						Arguments: `{"command":"echo Reviewing Tavern/BugReport.md for final validation"}`,
					}},
				},
				StopReason: "tool_calls",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "This follow-up should not be requested.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), shellTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") || !strings.Contains(reply, "빌드/테스트 검증은 실행하지 않았습니다") {
		t.Fatalf("expected synthesized document-artifact final reply, got %q", reply)
	}
	if shellTool.calls != 0 {
		t.Fatalf("run_shell should be blocked and not executed for generated document validation churn, got %d calls", shellTool.calls)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected runtime to synthesize final answer without another model turn, got %d requests", len(provider.requests))
	}
	if session.LastCodingHarnessReport == nil || !session.LastCodingHarnessReport.Approved {
		t.Fatalf("expected blocked validation tool to trigger an approved artifact harness report, got %#v", session.LastCodingHarnessReport)
	}
	if sessionContainsToolResultText(session, "call-shell-before-harness", "should not run") {
		t.Fatalf("blocked run_shell unexpectedly executed, messages=%#v", session.Messages)
	}
}

func TestAgentSynthesizesGeneratedDocumentFinalWhenInspectionToolsArriveBeforeHarnessReport(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 2 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"- Impact: resource lifetime bug.",
	}, "\n")
	readTool := &staticTool{name: "read_file", output: "read should not run"}
	listTool := &staticTool{name: "list_files", output: "list should not run"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{
						{
							ID:        "call-read-before-harness",
							Name:      "read_file",
							Arguments: `{"path":"Tavern/BugReport.md"}`,
						},
						{
							ID:        "call-list-before-harness",
							Name:      "list_files",
							Arguments: `{"path":"Tavern"}`,
						},
					},
				},
				StopReason: "tool_calls",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "This follow-up should not be requested.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), readTool, listTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") || !strings.Contains(reply, "빌드/테스트 검증은 실행하지 않았습니다") {
		t.Fatalf("expected synthesized document-artifact final reply, got %q", reply)
	}
	if readTool.calls != 0 || listTool.calls != 0 {
		t.Fatalf("inspection tools should be blocked for generated document post-completion churn, read=%d list=%d", readTool.calls, listTool.calls)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected runtime to synthesize final answer without another model turn, got %d requests", len(provider.requests))
	}
	if session.LastCodingHarnessReport == nil || !session.LastCodingHarnessReport.Approved {
		t.Fatalf("expected blocked inspection tools to trigger an approved artifact harness report, got %#v", session.LastCodingHarnessReport)
	}
	if sessionContainsToolResultText(session, "call-read-before-harness", "read should not run") ||
		sessionContainsToolResultText(session, "call-list-before-harness", "list should not run") {
		t.Fatalf("blocked inspection tool unexpectedly executed, messages=%#v", session.Messages)
	}
}

func TestAgentSynthesizesGeneratedDocumentFinalWhenPlanToolArrivesBeforeHarnessReport(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
	}, "\n")
	planTool := &staticTool{name: "update_plan", output: "plan should not run"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-plan-before-harness",
						Name:      "update_plan",
						Arguments: `{"items":[{"step":"final validation","status":"completed"}]}`,
					}},
				},
				StopReason: "tool_calls",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "This follow-up should not be requested.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), planTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") || !strings.Contains(reply, "빌드/테스트 검증은 실행하지 않았습니다") {
		t.Fatalf("expected synthesized document-artifact final reply, got %q", reply)
	}
	if planTool.calls != 0 {
		t.Fatalf("update_plan should be blocked for generated document post-completion churn, got %d calls", planTool.calls)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected runtime to synthesize final answer without another model turn, got %d requests", len(provider.requests))
	}
	if !sessionContainsToolResultText(session, "call-plan-before-harness", "do not run shell or review validation, planning, or additional inspection") {
		t.Fatalf("expected blocked planning tool result, messages=%#v", session.Messages)
	}
}

func TestAgentDoesNotFinalizeGeneratedDocumentPreambleWithEditToolCalls(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 1개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
	}, "\n")
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: Tavern/BugReport.md",
		"@@",
		"-소스코드 검토 결과 총 1개 버그를 문서로 생성했습니다.",
		"+소스코드 검토 결과 총 2개 버그를 문서로 생성했습니다.",
		"@@",
		"-| Total | 1 |",
		"+| High | 1 |",
		"+| Total | 2 |",
		"@@",
		"-- Impact: crash risk.",
		"+- Impact: crash risk.",
		"+",
		"+## BUG-002",
		"+- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"+- Impact: resource lifetime bug.",
		"*** End Patch",
	}, "\n")
	patchArgs, _ := json.Marshal(map[string]any{"patch": patch})
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 2개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
					ToolCalls: []ToolCall{{
						ID:        "call-patch-after-final-looking-preamble",
						Name:      "apply_patch",
						Arguments: string(patchArgs),
					}},
				},
				StopReason: "tool_calls",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 2개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), NewApplyPatchTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "총 2개 버그") {
		t.Fatalf("expected final answer after the corrective edit, got %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected apply_patch preamble to execute before final answer, got %d requests", len(provider.requests))
	}
	content, err := os.ReadFile(filepath.Join(root, "Tavern", "BugReport.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(content), "## BUG-002") || !strings.Contains(string(content), "| High | 1 |") {
		t.Fatalf("expected corrective apply_patch to update the report, got:\n%s", string(content))
	}
	if sessionContainsToolResultText(session, "call-patch-after-final-looking-preamble", "generated document artifact turns do not run") {
		t.Fatalf("apply_patch was incorrectly treated as post-final tool churn, messages=%#v", session.Messages)
	}
}

func TestAgentExecutesGeneratedDocumentReplaceInFileAfterFinalLookingPreamble(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 2개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 2 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"- Impact: needs detail.",
	}, "\n")
	replaceArgs, _ := json.Marshal(map[string]any{
		"path":    "Tavern/BugReport.md",
		"search":  "- Impact: needs detail.",
		"replace": "- Impact: resource lifetime bug.",
	})
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 2개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
					ToolCalls: []ToolCall{{
						ID:        "call-replace-after-final-looking-preamble",
						Name:      "replace_in_file",
						Arguments: string(replaceArgs),
					}},
				},
				StopReason: "tool_calls",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 2개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), NewReplaceInFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "총 2개 버그") {
		t.Fatalf("expected final answer after replace_in_file edit, got %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected replace_in_file preamble to execute before final answer, got %d requests", len(provider.requests))
	}
	content, err := os.ReadFile(filepath.Join(root, "Tavern", "BugReport.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(content), "needs detail") || !strings.Contains(string(content), "resource lifetime bug") {
		t.Fatalf("expected corrective replace_in_file to update the report, got:\n%s", string(content))
	}
	if sessionContainsToolResultText(session, "call-replace-after-final-looking-preamble", "generated document artifact turns do not run") {
		t.Fatalf("replace_in_file was incorrectly treated as post-final tool churn, messages=%#v", session.Messages)
	}
}

func TestAgentBlocksApprovedDocumentArtifactToolChurnWithoutRequestContext(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	request := "Reviewer feedback: revise the final answer before concluding."
	session.Messages = []Message{{Role: "user", Text: request}}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 4096,
			}},
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	if !agent.shouldFinalizeGeneratedDocumentArtifactReply(request, "Tavern/BugReport.md 생성 완료", false) {
		t.Fatalf("expected approved document artifact harness to allow finalization without original request context")
	}
	for _, call := range []ToolCall{
		{Name: "run_shell", Arguments: `{"command":"echo validate"}`},
		{Name: "read_file", Arguments: `{"path":"Tavern/BugReport.md"}`},
		{Name: "list_files", Arguments: `{"path":"Tavern"}`},
	} {
		if !agent.shouldBlockGeneratedDocumentArtifactValidationToolCalls(request, []ToolCall{call}) {
			t.Fatalf("expected approved document artifact harness to block post-completion call %#v", call)
		}
	}
	if !agent.shouldBlockGeneratedDocumentArtifactValidationToolCalls(request, []ToolCall{{
		Name:      "apply_patch",
		Arguments: `{"patch":"*** Begin Patch\n*** End Patch\n"}`,
	}}) {
		t.Fatalf("document artifact finalization should classify post-completion edit tools as churn")
	}
	if !agent.shouldBlockGeneratedDocumentArtifactValidationToolCalls(request, []ToolCall{{
		Name:      "replace_in_file",
		Arguments: `{"path":"Tavern/BugReport.md","old":"before","new":"after"}`,
	}}) {
		t.Fatalf("document artifact finalization should classify post-completion replace_in_file as churn")
	}
	if agent.shouldReviewInteractiveFinalAnswer("Tavern/BugReport.md 생성 완료", true, false) {
		t.Fatalf("expected approved document artifact harness to skip interactive final-answer review")
	}
}

func TestAgentTreatsContentAcceptedDocumentHarnessAsDocumentArtifactTurn(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		SourcePrompt: "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해",
	}
	session.TaskState = &TaskState{
		Goal: "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해",
	}
	session.Messages = []Message{{
		Role: "user",
		Text: "Reviewer feedback: revise the final answer before concluding.",
	}}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 4096,
			}},
		},
		Acceptance: AcceptanceContractReport{
			Findings: []CodingHarnessFinding{{
				Severity: "blocker",
				Title:    "Final answer has inconsistent bug counts",
				Detail:   "The answer says 27 bugs but its severity rows add up to 26.",
			}},
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Goal:   "Reviewer feedback: revise the final answer before concluding.",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "apply_patch",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	if !agent.changesAreGeneratedDocumentArtifactsForTurn("") {
		t.Fatalf("expected content-accepted artifact harness to preserve document-artifact turn state")
	}
	if !agent.shouldSynthesizeGeneratedDocumentArtifactFinalReply("", session.LastCodingHarnessReport, false) {
		t.Fatalf("expected answer-only final blocker to synthesize a safe document-artifact final reply")
	}
	if agent.shouldReviewInteractiveFinalAnswer("The answer says 27 bugs but the report is complete.", true, false) {
		t.Fatalf("expected content-accepted document artifact to skip interactive final-answer review")
	}
	if !agent.shouldSkipInteractiveFinalAnswerReviewForGeneratedDocumentArtifact(session.AcceptanceContract.SourcePrompt, false) {
		t.Fatalf("expected generated document artifact finalization to bypass the optional final-answer reviewer")
	}
}

func TestAgentPreservesGeneratedDocumentArtifactStateWithoutPatchTransactionPaths(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		SourcePrompt: "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해",
	}
	session.TaskState = &TaskState{
		Goal: "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해",
	}
	session.Messages = []Message{{
		Role: "user",
		Text: "Please provide the final answer now.",
	}}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 4096,
			}},
		},
		Acceptance: AcceptanceContractReport{
			Findings: []CodingHarnessFinding{{
				Severity: "blocker",
				Title:    "Final answer has inconsistent bug counts",
				Detail:   "The answer says 27 bugs but its severity rows add up to 26.",
			}},
		},
	}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Tools: NewToolRegistry(
			&staticTool{name: "read_file", output: "read"},
			&staticTool{name: "run_shell", output: "shell"},
		),
	}

	if !agent.changesAreGeneratedDocumentArtifactsForTurn("") {
		t.Fatalf("expected accepted artifact-quality report to preserve document-artifact state without patch paths")
	}
	if !agent.shouldSynthesizeGeneratedDocumentArtifactFinalReply("", session.LastCodingHarnessReport, false) {
		t.Fatalf("expected answer-only final blocker to synthesize a safe document-artifact final reply without patch paths")
	}
	if agent.shouldReviewInteractiveFinalAnswer("The answer says 27 bugs but the report is complete.", true, false) {
		t.Fatalf("expected accepted document artifact quality to skip interactive final-answer review without patch paths")
	}
	if !agent.shouldBlockGeneratedDocumentArtifactValidationToolCalls("", []ToolCall{{
		Name:      "run_shell",
		Arguments: `{"command":"echo Reviewing Tavern/BugReport.md for final validation"}`,
	}}) {
		t.Fatalf("expected accepted document artifact quality to block post-completion validation without patch paths")
	}
	plan := agent.buildTurnToolExposurePlan(nil, "Please provide the final answer now.", false, false, false, false, false)
	if !plan.GeneratedDocumentFinalOnly {
		t.Fatalf("expected accepted document artifact quality to force final-only tools without patch paths")
	}
	for _, name := range []string{"read_file", "run_shell"} {
		if !plan.DisabledTools[name] {
			t.Fatalf("expected final-only document artifact turn to disable %s, got %#v", name, plan.DisabledTools)
		}
	}
}

func TestAgentRecoversGeneratedDocumentArtifactStateFromAcceptedHarnessWithoutRequestContext(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "Please provide the final answer now.",
	}}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 4096,
			}},
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Goal:   "Reviewer feedback: provide a final answer.",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "apply_patch",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Tools: NewToolRegistry(
			&staticTool{name: "read_file", output: "read"},
			&staticTool{name: "run_shell", output: "shell"},
		),
	}

	if !agent.changesAreGeneratedDocumentArtifactsForTurn("Please provide the final answer now.") {
		t.Fatalf("expected accepted artifact harness to recover document-artifact state for final-answer follow-up")
	}
	plan := agent.buildTurnToolExposurePlan(nil, "Please provide the final answer now.", false, false, false, false, false)
	if !plan.GeneratedDocumentFinalOnly {
		t.Fatalf("expected accepted document artifact state to force final-only tools")
	}
	for _, name := range []string{"read_file", "run_shell"} {
		if !plan.DisabledTools[name] {
			t.Fatalf("expected final-only document artifact turn to disable %s, got %#v", name, plan.DisabledTools)
		}
	}
	if !agent.shouldBlockGeneratedDocumentArtifactValidationToolCalls("Please provide the final answer now.", []ToolCall{{
		Name:      "read_file",
		Arguments: `{"path":"Tavern/BugReport.md"}`,
	}}) {
		t.Fatalf("expected accepted document artifact state to block post-completion inspection churn")
	}
}

func TestAgentSynthesizesGeneratedDocumentFinalWhenReplyOmitsArtifactPath(t *testing.T) {
	root := t.TempDir()
	reportLines := []string{
		"# Tavern Client Bug Report",
		"",
		"각 소스코드 파일들을 검토해서 총 26개 버그를 문서화했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 4 |",
		"| High | 7 |",
		"| Medium | 9 |",
		"| Low | 6 |",
		"| Total | 26 |",
		"",
	}
	for i := 1; i <= 26; i++ {
		reportLines = append(reportLines,
			fmt.Sprintf("## BUG-%03d", i),
			"- File: Tavern/Tavern/RuntimeManager.cpp",
			"- Impact: documented issue.",
			"",
		)
	}
	reportContent := strings.Join(reportLines, "\n")
	if err := os.MkdirAll(filepath.Join(root, "Tavern"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Tavern", "BugReport.md"), []byte(reportContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		SourcePrompt: request,
	}
	session.TaskState = &TaskState{
		Goal: request,
	}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				ContentChars: len([]rune(reportContent)),
				Substantive:  true,
				Checks:       []string{"text readable"},
			}},
		},
	}
	agent := &Agent{
		Config: Config{
			AutoLocale: boolPtr(false),
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	inconsistentReply := strings.Join([]string{
		"The bug report document has been reviewed and is now in final form.",
		"",
		"27 documented bugs were found.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 4 |",
		"| High | 7 |",
		"| Medium | 9 |",
		"| Low | 6 |",
		"| **Total** | **26** |",
		"",
		"Build/test verification was not run because this is a documentation-only artifact.",
	}, "\n")

	reply, finalized, err := agent.maybeFinalizeGeneratedDocumentArtifactFinalReply(request, inconsistentReply, true, false)
	if err != nil {
		t.Fatalf("maybeFinalizeGeneratedDocumentArtifactFinalReply: %v", err)
	}
	if !finalized {
		t.Fatalf("expected artifact finalizer to synthesize a final reply when the model omits the path")
	}
	if strings.Contains(reply, "27 documented bugs") {
		t.Fatalf("expected inconsistent bug-count reply to be replaced, got %q", reply)
	}
	if !strings.Contains(reply, "Tavern/BugReport.md") {
		t.Fatalf("expected synthesized reply to retain the artifact path, got %q", reply)
	}
	if session.LastCodingHarnessReport == nil || !session.LastCodingHarnessReport.Approved {
		t.Fatalf("expected synthesized document final to approve harness report, got %#v", session.LastCodingHarnessReport)
	}
	if len(session.LastCodingHarnessReport.ArtifactQuality.Artifacts) == 0 {
		t.Fatalf("expected artifact quality target to be preserved")
	}
	if agent.shouldReviewInteractiveFinalAnswer(reply, true, false) {
		t.Fatalf("expected synthesized generated document final to skip final-answer reviewer")
	}
}

func TestAgentDoesNotCarryGeneratedDocumentArtifactStateIntoUnrelatedTurn(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		SourcePrompt: "RuntimeManager.cpp 버그를 수정해",
	}
	session.TaskState = &TaskState{
		Goal:  "RuntimeManager.cpp 버그를 수정해",
		Phase: "planning",
	}
	session.Messages = []Message{
		{
			Role: "user",
			Text: "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해",
		},
		{
			Role:  "assistant",
			Phase: messagePhaseFinalAnswer,
			Text:  "Tavern/BugReport.md 생성 완료",
		},
		{
			Role: "user",
			Text: "RuntimeManager.cpp 버그를 수정해",
		},
	}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 4096,
			}},
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Goal:   "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	if agent.changesAreGeneratedDocumentArtifactsForTurn("RuntimeManager.cpp 버그를 수정해") {
		t.Fatalf("stale document artifact history should not classify an unrelated new turn as document-only")
	}
	if agent.changesAreGeneratedDocumentArtifactsForTurn("Fix RuntimeManager.cpp and provide the final answer now.") {
		t.Fatalf("final-answer wording on a new code request should not revive stale document artifact state")
	}
	if agent.shouldBlockGeneratedDocumentArtifactValidationToolCalls("RuntimeManager.cpp 버그를 수정해", []ToolCall{{Name: "read_file", Arguments: `{"path":"RuntimeManager.cpp"}`}}) {
		t.Fatalf("stale document artifact state should not block inspection tools for an unrelated new turn")
	}
	if agent.shouldReviewInteractiveFinalAnswer("RuntimeManager.cpp 수정 완료", true, false) {
		t.Fatalf("stale document patch history should not trigger final-answer review for an unrelated new turn")
	}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-code",
		Goal:   "RuntimeManager.cpp 버그를 수정해",
		Status: patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-code-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "cmd/kernforge/agent.go",
				Operation: "apply_patch",
			}},
		}},
	}
	if !agent.shouldReviewInteractiveFinalAnswer("RuntimeManager.cpp 수정 완료", true, false) {
		t.Fatalf("current-turn code patch evidence should remain eligible for final-answer review")
	}
}

func TestAgentDoesNotCarryGeneratedDocumentArtifactStateIntoFreshFollowupIntent(t *testing.T) {
	root := t.TempDir()
	originalRequest := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		SourcePrompt: originalRequest,
	}
	session.TaskState = &TaskState{
		Goal:  originalRequest,
		Phase: "done",
	}
	session.Messages = []Message{
		{
			Role: "user",
			Text: originalRequest,
		},
		{
			Role:  "assistant",
			Phase: messagePhaseFinalAnswer,
			Text:  "Tavern/BugReport.md 문서 산출물이 완료되었습니다.",
		},
	}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 4096,
			}},
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Goal:   originalRequest,
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Tools: NewToolRegistry(
			&staticTool{name: "read_file", output: "read"},
			&staticTool{name: "git_commit", output: "commit"},
			&staticTool{name: "run_shell", output: "shell"},
		),
	}

	requests := []string{
		"커밋하자",
		"지금 몇 % 정도 작업 완료된 것 같아?",
		"테스트 실행해",
		"RuntimeManager.cpp 코드 리뷰해줘",
		"수정하지 말고 RuntimeManager.cpp 리뷰만 해줘",
		"RuntimeManager.cpp 버그를 수정해",
	}
	if !looksLikeExplicitGitIntent("커밋하자") {
		t.Fatalf("expected Korean commit follow-up to be explicit git intent")
	}
	for _, request := range requests {
		session.Messages = append(session.Messages, Message{Role: "user", Text: request})
		if agent.changesAreGeneratedDocumentArtifactsForTurn(request) {
			t.Fatalf("fresh follow-up %q should not revive stale generated document final-only state", request)
		}
		plan := agent.buildTurnToolExposurePlan(nil, request, false, false, false, false, false)
		if plan.GeneratedDocumentFinalOnly || plan.SuppressInteractiveWorkers {
			t.Fatalf("fresh follow-up %q should keep normal orchestration, got %#v", request, plan)
		}
		if plan.DisabledTools["read_file"] || plan.DisabledTools["git_commit"] || plan.DisabledTools["run_shell"] {
			t.Fatalf("fresh follow-up %q should not inherit generated-document tool hiding, got %#v", request, plan.DisabledTools)
		}
	}
}

func TestAgentSkipsFinalReviewerForApprovedDocOnlyChangesWithoutArtifactList(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		SourcePrompt: "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해",
	}
	session.TaskState = &TaskState{
		Goal: "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해",
	}
	session.Messages = []Message{{
		Role: "user",
		Text: "Reviewer feedback: revise the final answer before concluding.",
	}}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "apply_patch",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	if !agent.changesAreGeneratedDocumentArtifactsForTurn("") {
		t.Fatalf("expected approved doc-only patch transaction to be treated as generated document artifact")
	}
	if agent.shouldReviewInteractiveFinalAnswer("Tavern/BugReport.md 생성 완료", true, false) {
		t.Fatalf("expected approved doc-only artifact to skip interactive final-answer reviewer")
	}
}

func TestAgentDoesNotTreatGenericDocOnlyChangeAsGeneratedReportWithoutArtifactList(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "README.md",
				Operation: "apply_patch",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	if agent.changesAreGeneratedDocumentArtifactsForTurn("") {
		t.Fatalf("generic README doc-only changes should not be inferred as generated report artifacts without request context")
	}
}

func TestAgentDoesNotTreatApprovedMixedArtifactHarnessAsGeneratedDocumentOnly(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	request := "Reviewer feedback: revise the final answer before concluding."
	session.Messages = []Message{{Role: "user", Text: request}}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 4096,
			}},
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-mixed",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-mixed-001",
			Status: "success",
			Paths: []PatchPathChange{
				{
					Path:      "Tavern/BugReport.md",
					Operation: "write_file",
				},
				{
					Path:      "cmd/kernforge/agent.go",
					Operation: "modify",
				},
			},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	if agent.shouldFinalizeGeneratedDocumentArtifactReply(request, "Tavern/BugReport.md 생성 완료", false) {
		t.Fatalf("expected mixed code/doc changes not to use document-only finalization")
	}
	if agent.shouldBlockGeneratedDocumentArtifactValidationToolCalls(request, []ToolCall{{Name: "read_file", Arguments: `{"path":"Tavern/BugReport.md"}`}}) {
		t.Fatalf("expected mixed code/doc changes not to block normal follow-up tools as document-only churn")
	}
}

func TestAgentAllowsGeneratedDocumentArtifactInspectionBeforeHarnessApproval(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해"
	session.Messages = []Message{{Role: "user", Text: request}}
	session.LastCodingHarnessReport = &CodingHarnessReport{Approved: false}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	if agent.shouldBlockGeneratedDocumentArtifactValidationToolCalls(request, []ToolCall{{Name: "read_file", Arguments: `{"path":"Tavern/BugReport.md"}`}}) {
		t.Fatalf("expected document inspection to remain available before artifact quality approval")
	}
	if !agent.shouldBlockGeneratedDocumentArtifactValidationToolCalls(request, []ToolCall{{Name: "run_shell", Arguments: `{"command":"echo validate"}`}}) {
		t.Fatalf("expected shell validation to remain blocked for generated document artifact turns")
	}
}

func TestAgentRoutesGeneratedDocumentCommentaryReplyThroughArtifactHarness(t *testing.T) {
	root := t.TempDir()
	badReportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 3 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
	}, "\n")
	goodReportContent := strings.ReplaceAll(badReportContent, "| Total | 3 |", "| Total | 2 |")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": badReportContent,
			}),
			{
				Message: Message{
					Role:  "assistant",
					Phase: messagePhaseCommentary,
					Text:  "Tavern/BugReport.md 문서를 생성했고 총 3개 버그를 기록했습니다.",
				},
				StopReason: "stop",
			},
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": goodReportContent,
			}),
			{
				Message: Message{
					Role:  "assistant",
					Phase: messagePhaseCommentary,
					Text:  "Tavern/BugReport.md 문서를 수정했고 총 2개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "총 2개") {
		t.Fatalf("expected commentary document reply to pass through artifact harness and repair, got %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected rejected commentary summary to trigger one repair turn, got %d requests", len(provider.requests))
	}
	if session.LastCodingHarnessReport == nil || !session.LastCodingHarnessReport.Approved {
		t.Fatalf("expected final commentary document harness report to be approved, got %#v", session.LastCodingHarnessReport)
	}
}

func TestAgentDoesNotRouteUnrelatedCommentaryThroughFinalGatesFromStalePatchHistory(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{
		{
			Role: "user",
			Text: "RuntimeManager.cpp 버그를 수정해",
		},
		{
			Role:  "assistant",
			Phase: messagePhaseFinalAnswer,
			Text:  "RuntimeManager.cpp 수정 완료",
		},
		{
			Role: "user",
			Text: "현재 상태만 알려줘",
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-code-old",
		Goal:   "RuntimeManager.cpp 버그를 수정해",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-code-old-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "cmd/kernforge/agent.go",
				Operation: "apply_patch",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	if agent.shouldRouteCommentaryReplyThroughFinalGates("현재 상태만 알려줘", "현재 상태를 확인했습니다.", false, false, false) {
		t.Fatalf("stale archived patch history should not route a read-only commentary reply into final gates")
	}
	if agent.shouldReviewInteractiveFinalAnswer("현재 상태를 확인했습니다.", false, false) {
		t.Fatalf("stale archived patch history should not trigger final-answer review for a read-only turn")
	}
	report := agent.buildDiffAwareSelfReviewReport("이번 턴에는 파일 변경이 없습니다.", false)
	if len(report.ChangedPaths) != 0 || len(report.Findings) != 0 {
		t.Fatalf("stale patch history should not affect current-turn diff review, got %#v", report)
	}
}

func TestAgentDoesNotRouteUnrelatedFinalGatesFromStaleActiveEditLoop(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.TaskState = &TaskState{Goal: "현재 상태만 알려줘"}
	session.Messages = []Message{
		{
			Role: "user",
			Text: "RuntimeManager.cpp 버그를 수정해",
		},
		{
			Role: "user",
			Text: "현재 상태만 알려줘",
		},
	}
	session.ActiveEditLoop = &EditLoopState{
		ID:              "edit-loop-old",
		Goal:            "RuntimeManager.cpp 버그를 수정해",
		Status:          editLoopStatusActive,
		ChangedPaths:    []string{"cmd/kernforge/agent.go"},
		WorkerSummaries: []string{"updated RuntimeManager handling"},
	}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	if sessionHasCurrentTurnFinalGateEvidence(session) {
		t.Fatalf("stale active edit loop should not count as current-turn final gate evidence")
	}
	if agent.shouldRouteCommentaryReplyThroughFinalGates("현재 상태만 알려줘", "현재 상태를 확인했습니다.", false, false, false) {
		t.Fatalf("stale active edit loop should not route a read-only commentary reply into final gates")
	}
	if agent.shouldReviewInteractiveFinalAnswer("현재 상태를 확인했습니다.", false, false) {
		t.Fatalf("stale active edit loop should not trigger final-answer review for a read-only turn")
	}
	if agent.shouldBufferAssistantDeltaForGatedTurn(false, false, false) {
		t.Fatalf("stale active edit loop should not buffer ordinary status replies")
	}
	report := agent.buildCodingHarnessReport("현재 상태를 확인했습니다.", false, false)
	for _, finding := range report.Findings {
		if strings.Contains(strings.ToLower(finding.Title), "edit loop") {
			t.Fatalf("stale active edit loop should not affect pre-final harness findings, got %#v", report.Findings)
		}
	}
}

func TestAgentRoutesCurrentActiveEditLoopThroughFinalGates(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.TaskState = &TaskState{Goal: "RuntimeManager.cpp 버그를 수정해"}
	session.Messages = []Message{{
		Role: "user",
		Text: "RuntimeManager.cpp 버그를 수정해",
	}}
	session.ActiveEditLoop = &EditLoopState{
		ID:              "edit-loop-current",
		Goal:            "RuntimeManager.cpp 버그를 수정해",
		Status:          editLoopStatusActive,
		ChangedPaths:    []string{"cmd/kernforge/agent.go"},
		WorkerSummaries: []string{"updated RuntimeManager handling"},
	}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	if !sessionHasCurrentTurnFinalGateEvidence(session) {
		t.Fatalf("current active edit loop should count as current-turn final gate evidence")
	}
	if !agent.shouldRouteCommentaryReplyThroughFinalGates("RuntimeManager.cpp 버그를 수정해", "수정 완료", false, false, false) {
		t.Fatalf("current active edit loop should route final-looking commentary through gates")
	}
	if !agent.shouldReviewInteractiveFinalAnswer("RuntimeManager.cpp 수정 완료", false, false) {
		t.Fatalf("current active edit loop should remain eligible for final-answer review")
	}
	if !agent.shouldBufferAssistantDeltaForGatedTurn(false, false, false) {
		t.Fatalf("current active edit loop should buffer final candidates until accepted")
	}
}

func TestAgentRoutesCurrentPatchCommentaryThroughFinalGates(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "RuntimeManager.cpp 버그를 수정해",
	}}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-code",
		Goal:   "RuntimeManager.cpp 버그를 수정해",
		Status: patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-code-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "cmd/kernforge/agent.go",
				Operation: "apply_patch",
			}},
		}},
	}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	if !agent.shouldRouteCommentaryReplyThroughFinalGates("RuntimeManager.cpp 버그를 수정해", "수정 완료", false, false, false) {
		t.Fatalf("current-turn patch evidence should route commentary replies through final gates")
	}
	report := agent.buildDiffAwareSelfReviewReport("수정 완료", false)
	if len(report.ChangedPaths) != 1 || report.ChangedPaths[0] != "cmd/kernforge/agent.go" {
		t.Fatalf("expected current-turn changed path in diff review, got %#v", report.ChangedPaths)
	}
}

func TestAgentBuffersFinalAnswerDeltaAfterMetadataOnlyEdit(t *testing.T) {
	root := t.TempDir()
	provider := &streamingScriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("external_edit", map[string]any{}),
			{
				Message: Message{
					Role: "assistant",
					Text: "변경을 적용했고 추가 검증은 실행하지 않았습니다.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var emitted strings.Builder
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client: provider,
		Tools: NewToolRegistry(&metadataEditTool{
			name:   "external_edit",
			output: "metadata-only edit complete",
			meta: map[string]any{
				"changed_workspace": true,
				"effect":            "edit",
				"changed_paths":     []string{"main.go"},
			},
		}),
		Workspace:          ws,
		Session:            session,
		Store:              store,
		EmitAssistantDelta: func(text string) { emitted.WriteString(text) },
	}

	reply, err := agent.Reply(context.Background(), "도구가 보고한 변경을 반영하고 요약해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "변경을 적용") {
		t.Fatalf("expected final answer, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected one tool turn and one final turn, got %d requests", len(provider.requests))
	}
	if provider.requests[1].OnTextDelta != nil {
		t.Fatalf("expected final answer deltas to be buffered after an edit-like tool result")
	}
	if emitted.String() != "" {
		t.Fatalf("expected edit final candidate to stay hidden until accepted, got streamed %q", emitted.String())
	}
	if agent.lastEmittedText != "" {
		t.Fatalf("buffered edit final candidate must not be recorded as emitted text, got %q", agent.lastEmittedText)
	}
}

func TestAgentRefreshesGeneratedDocumentHarnessAfterExhaustedFinalRevisions(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 2 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
		"- Impact: resource lifetime bug.",
	}, "\n")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 go test ./... 검증도 통과했습니다.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 go test ./... 검증도 통과했습니다.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
				},
				StopReason: "stop",
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "빌드/테스트 검증은 실행하지 않았습니다") {
		t.Fatalf("expected synthesized safe final answer, got %q with harness %#v", reply, session.LastCodingHarnessReport)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected synthesized final answer to finish without extra model turns, got %d requests", len(provider.requests))
	}
	if session.LastCodingHarnessReport == nil || !session.LastCodingHarnessReport.Approved {
		t.Fatalf("expected final harness report to be refreshed and approved, got %#v", session.LastCodingHarnessReport)
	}
}

func TestAgentResetsFinalGateRetriesAfterGeneratedDocumentRepair(t *testing.T) {
	root := t.TempDir()
	badReportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 3 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
	}, "\n")
	goodReportContent := strings.ReplaceAll(badReportContent, "| Total | 3 |", "| Total | 2 |")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": badReportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 3개 버그를 기록했습니다.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 검토했고 총 3개 버그를 기록했습니다.",
				},
				StopReason: "stop",
			},
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": goodReportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 수정했고 총 2개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다.",
				},
				StopReason: "stop",
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "총 2개") {
		t.Fatalf("expected repaired final answer, got %q with harness %#v", reply, session.LastCodingHarnessReport)
	}
	if len(provider.requests) != 5 {
		t.Fatalf("expected repaired document to finalize without an extra review/validation turn, got %d requests", len(provider.requests))
	}
	if session.LastCodingHarnessReport == nil || !session.LastCodingHarnessReport.Approved {
		t.Fatalf("expected repaired document harness report to be approved, got %#v", session.LastCodingHarnessReport)
	}
}

func TestAgentDropsRejectedFinalAnswerCandidateFromNextTurnHistory(t *testing.T) {
	root := t.TempDir()
	badReportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 3 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
	}, "\n")
	goodReportContent := strings.ReplaceAll(badReportContent, "| Total | 3 |", "| Total | 2 |")
	rejectedReply := "Tavern/BugReport.md 문서를 생성했고 총 3개 버그를 기록했습니다."
	acceptedReply := "Tavern/BugReport.md 문서를 수정했고 총 2개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": badReportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: rejectedReply,
				},
				StopReason: "stop",
			},
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": goodReportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: acceptedReply,
				},
				StopReason: "stop",
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != acceptedReply {
		t.Fatalf("expected accepted final reply, got %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected one rejected final answer, repair, then finalization, got %d requests", len(provider.requests))
	}
	for _, msg := range provider.requests[2].Messages {
		if msg.Role == "assistant" && strings.Contains(msg.Text, rejectedReply) {
			t.Fatalf("rejected final answer leaked into the next model request: %#v", msg)
		}
	}
	for _, msg := range session.Messages {
		if msg.Role == "assistant" && msg.Phase == messagePhaseFinalAnswerCandidate {
			t.Fatalf("final-answer candidate remained in session: %#v", msg)
		}
		if msg.Role == "assistant" && strings.Contains(msg.Text, rejectedReply) {
			t.Fatalf("rejected final answer remained in session: %#v", msg)
		}
	}
	foundFinal := false
	for _, msg := range session.Messages {
		if msg.Role == "assistant" && msg.Text == acceptedReply {
			if msg.Phase != messagePhaseFinalAnswer {
				t.Fatalf("accepted final answer was not promoted to final phase: %#v", msg)
			}
			foundFinal = true
		}
	}
	if !foundFinal {
		t.Fatalf("accepted final answer was not recorded in the session")
	}
}

func TestAgentDropsStaleFinalAnswerCandidateBeforeNewUserTurn(t *testing.T) {
	root := t.TempDir()
	staleReply := "이 답변은 게이트를 통과하지 못한 이전 최종 답변 후보입니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "새 질문에 대한 답변입니다.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "이전 요청"})
	session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswerCandidate, Text: staleReply})
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "새 질문에 답해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "새 질문에 대한 답변입니다." {
		t.Fatalf("expected fresh reply, got %q", reply)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected a single model request, got %d", len(provider.requests))
	}
	for _, msg := range provider.requests[0].Messages {
		if msg.Role == "assistant" && strings.Contains(msg.Text, staleReply) {
			t.Fatalf("stale final-answer candidate leaked into new model request: %#v", msg)
		}
	}
	for _, msg := range session.Messages {
		if msg.Role == "assistant" && strings.Contains(msg.Text, staleReply) {
			t.Fatalf("stale final-answer candidate remained in session: %#v", msg)
		}
	}
}

func TestAgentAcceptsCommentaryOnlyAssistantMessageLikeCodex(t *testing.T) {
	root := t.TempDir()
	commentary := "검토 결과를 정리하는 중입니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role:  "assistant",
					Phase: messagePhaseCommentary,
					Text:  commentary,
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "상태를 확인해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != commentary {
		t.Fatalf("expected commentary reply to complete the turn, got %q", reply)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected commentary-only reply to complete without another model turn, got %d requests", len(provider.requests))
	}
	if scriptedRequestsContainText(provider.requests, "commentary/progress") {
		t.Fatalf("commentary-only completion should not inject synthetic commentary guidance: %#v", provider.requests[0].Messages)
	}

	foundCommentary := false
	for _, msg := range session.Messages {
		if msg.Role == "assistant" && msg.Text == commentary {
			foundCommentary = true
			if msg.Phase != messagePhaseCommentary {
				t.Fatalf("commentary message phase was not preserved: %#v", msg)
			}
		}
	}
	if !foundCommentary {
		t.Fatalf("commentary-only assistant message was not retained")
	}
}

func TestAgentRoutesPostEditCommentaryReplyThroughFinalGates(t *testing.T) {
	root := t.TempDir()
	commentary := "main.go 파일을 수정했습니다. 검증은 실행하지 않았습니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{
				Message: Message{
					Role:  "assistant",
					Phase: messagePhaseCommentary,
					Text:  commentary,
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "main.go 파일을 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != commentary {
		t.Fatalf("expected commentary reply to complete after final gates, got %q", reply)
	}
	if session.LastCodingHarnessReport == nil {
		t.Fatalf("expected post-edit commentary reply to run final coding harness")
	}
	if !session.LastCodingHarnessReport.Approved {
		t.Fatalf("expected post-edit commentary harness to approve, got %#v", session.LastCodingHarnessReport)
	}
	if len(session.PatchTransactions) == 0 || session.PatchTransactions[0].Status != patchTransactionStatusCommitted {
		t.Fatalf("expected post-edit commentary reply to finalize patch transaction, got %#v", session.PatchTransactions)
	}
}

func TestAgentFinalizesPostEditReplyWhenProviderEndTurnFalse(t *testing.T) {
	root := t.TempDir()
	endTurnFalse := false
	replyText := "main.go 파일을 수정했습니다. 검증은 실행하지 않았습니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{
				Message: Message{
					Role: "assistant",
					Text: replyText,
				},
				StopReason: "completed",
				EndTurn:    &endTurnFalse,
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "This follow-up should not be requested.",
				},
				StopReason: "completed",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "main.go 파일을 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != replyText {
		t.Fatalf("expected post-edit end_turn=false reply to finalize, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected post-edit end_turn=false reply to stop after edit and final turns, got %d requests", len(provider.requests))
	}
	if session.Messages[len(session.Messages)-1].Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected post-edit end_turn=false reply to be accepted as final, got %#v", session.Messages[len(session.Messages)-1])
	}
	if session.LastCodingHarnessReport == nil || !session.LastCodingHarnessReport.Approved {
		t.Fatalf("expected final coding harness to approve post-edit reply, got %#v", session.LastCodingHarnessReport)
	}
}

func TestAgentContinuesPostEditInProgressEndTurnFalse(t *testing.T) {
	root := t.TempDir()
	endTurnFalse := false
	inProgress := "Still checking the edit before the final answer."
	finalReply := "main.go 파일을 수정했습니다. 검증은 실행하지 않았습니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{
				Message: Message{
					Role: "assistant",
					Text: inProgress,
				},
				StopReason: "completed",
				EndTurn:    &endTurnFalse,
			},
			{
				Message: Message{
					Role: "assistant",
					Text: finalReply,
				},
				StopReason: "completed",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "main.go 파일을 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != finalReply {
		t.Fatalf("expected final reply after in-progress continuation, got %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected in-progress post-edit end_turn=false reply to request a follow-up, got %d requests", len(provider.requests))
	}
	foundInProgress := false
	for _, msg := range session.Messages {
		if msg.Role == "assistant" && msg.Text == inProgress {
			foundInProgress = true
			if msg.Phase != messagePhaseCommentary {
				t.Fatalf("expected in-progress post-edit reply to remain commentary, got %#v", msg)
			}
		}
	}
	if !foundInProgress {
		t.Fatalf("expected in-progress assistant reply to be retained")
	}
}

func TestAgentContinuesPostEditFutureVerificationEndTurnFalse(t *testing.T) {
	root := t.TempDir()
	endTurnFalse := false
	inProgress := "main.go 파일을 수정했습니다. 이제 go test를 실행하겠습니다."
	finalReply := "main.go 파일을 수정했습니다. 검증은 실행하지 않았습니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{
				Message: Message{
					Role: "assistant",
					Text: inProgress,
				},
				StopReason: "completed",
				EndTurn:    &endTurnFalse,
			},
			{
				Message: Message{
					Role: "assistant",
					Text: finalReply,
				},
				StopReason: "completed",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "main.go 파일을 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != finalReply {
		t.Fatalf("expected final reply after future verification continuation, got %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected future verification reply to request a follow-up, got %d requests", len(provider.requests))
	}
	foundInProgress := false
	for _, msg := range session.Messages {
		if msg.Role == "assistant" && msg.Text == inProgress {
			foundInProgress = true
			if msg.Phase != messagePhaseCommentary {
				t.Fatalf("expected future verification reply to remain commentary, got %#v", msg)
			}
		}
	}
	if !foundInProgress {
		t.Fatalf("expected future verification assistant reply to be retained")
	}
}

func TestAgentContinuesGeneratedDocumentInProgressEndTurnFalseAfterArtifactWrite(t *testing.T) {
	root := t.TempDir()
	reportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 검토 결과 총 1개 버그를 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"- Impact: crash risk.",
	}, "\n")
	endTurnFalse := false
	inProgress := "Still checking the generated artifact."
	finalReply := "Tavern/BugReport.md 문서를 생성했고 총 1개 버그를 기록했습니다. 문서 산출물 작업이라 빌드/테스트 검증은 실행하지 않았습니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": reportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: inProgress,
				},
				StopReason: "completed",
				EndTurn:    &endTurnFalse,
			},
			{
				Message: Message{
					Role: "assistant",
					Text: finalReply,
				},
				StopReason: "completed",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != finalReply {
		t.Fatalf("expected generated document final reply after in-progress continuation, got %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected generated document in-progress end_turn=false reply to request a follow-up, got %d requests", len(provider.requests))
	}
	if session.Messages[len(session.Messages)-1].Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected final generated document reply to be accepted, got %#v", session.Messages[len(session.Messages)-1])
	}
}

func TestAgentContinuesAfterHiddenOnlyAssistantMessage(t *testing.T) {
	root := t.TempDir()
	hidden := "<oai-mem-citation>hidden only</oai-mem-citation>"
	finalReply := "검토 보고서를 생성했고 추가 작업은 없습니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: hidden,
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: finalReply,
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "상태를 확인해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != finalReply {
		t.Fatalf("expected final reply, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected hidden-only reply to trigger another model turn, got %d requests", len(provider.requests))
	}
	if !scriptedRequestsContainText(provider.requests[1:2], "Please provide the final answer") {
		t.Fatalf("expected second request to include empty-final guidance, got %#v", provider.requests[1].Messages)
	}
	for _, msg := range session.Messages {
		if msg.Role == "assistant" && strings.Contains(msg.Text, "oai-mem-citation") {
			t.Fatalf("hidden assistant markup leaked into session history: %#v", msg)
		}
	}
}

func TestAssistantMessagePhaseForModelResponsePreservesOnlyCommentary(t *testing.T) {
	commentary := assistantMessagePhaseForModelResponse(Message{
		Role:  "assistant",
		Phase: messagePhaseCommentary,
		Text:  "working",
	})
	if commentary != messagePhaseCommentary {
		t.Fatalf("expected commentary phase to be preserved, got %q", commentary)
	}

	final := assistantMessagePhaseForModelResponse(Message{
		Role:  "assistant",
		Phase: messagePhaseFinalAnswer,
		Text:  "done",
	})
	if final != messagePhaseFinalAnswerCandidate {
		t.Fatalf("expected provider final_answer to become gated candidate, got %q", final)
	}

	toolPhase := assistantMessagePhaseForModelResponse(Message{
		Role: "assistant",
		Text: "I will inspect this.",
		ToolCalls: []ToolCall{{
			ID:   "call-1",
			Name: "read_file",
		}},
	})
	if toolPhase != messagePhaseCommentary {
		t.Fatalf("expected tool-call message to be commentary, got %q", toolPhase)
	}
}

func TestAgentStopsGeneratedDocumentArtifactOnPersistentHarnessBlocker(t *testing.T) {
	root := t.TempDir()
	badReportContent := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 3 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern/RuntimeManager.cpp",
		"",
		"## BUG-002",
		"- File: Tavern/Tavern/TavernWorkerManager.cpp",
	}, "\n")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "Tavern/BugReport.md",
				"content": badReportContent,
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 생성했고 총 3개 버그를 기록했습니다.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 검토했고 총 3개 버그를 기록했습니다.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Tavern/BugReport.md 문서를 최종 확인했고 총 3개 버그를 기록했습니다.",
				},
				StopReason: "stop",
			},
			toolCallResponse("run_shell", map[string]any{"command": "echo should not run"}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), NewRunShellTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Generated document artifact quality checks are still blocking completion") {
		t.Fatalf("expected deterministic artifact blocker reply, got %q", reply)
	}
	if !strings.Contains(reply, "Artifact total does not match") {
		t.Fatalf("expected artifact count blocker details, got %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected no post-exhaustion shell validation or reviewer turn, got %d requests", len(provider.requests))
	}
	if session.LastCodingHarnessReport == nil || session.LastCodingHarnessReport.Approved {
		t.Fatalf("expected blocked harness report to remain recorded, got %#v", session.LastCodingHarnessReport)
	}
	if session.ActivePatchTransaction == nil {
		t.Fatalf("expected blocked document patch transaction to remain active for a follow-up repair")
	}
}

func TestAgentDoesNotBufferAssistantDeltaForArchivedPatchTransaction(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-old",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-old-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	agent := &Agent{Session: session}

	if agent.shouldBufferAssistantDeltaForGatedTurn(false, false, false) {
		t.Fatalf("expected archived patch transactions from a previous turn not to suppress assistant streaming")
	}
	if !agent.shouldBufferAssistantDeltaForGatedTurn(false, true, false) {
		t.Fatalf("expected attempted edit tools to suppress assistant streaming until final gates accept the answer")
	}
	if !agent.shouldBufferAssistantDeltaForGatedTurn(false, false, true) {
		t.Fatalf("expected successful edit-like tools to suppress assistant streaming until final gates accept the answer")
	}
}

func TestAgentRewritesRunShellApplyPatchHeredocToApplyPatchTool(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	command := strings.Join([]string{
		"apply_patch <<'PATCH'",
		"*** Begin Patch",
		"*** Update File: main.go",
		"@@",
		" package main",
		"+",
		"+func patched() {}",
		"*** End Patch",
		"PATCH",
	}, "\n")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("run_shell", map[string]any{"command": command}),
			{
				Message: Message{
					Role: "assistant",
					Text: "main.go patched.",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{Model: "model"},
		Client:    provider,
		Tools:     NewToolRegistry(NewRunShellTool(ws), NewApplyPatchTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "main.go를 수정해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "main.go patched." {
		t.Fatalf("unexpected reply: %q", reply)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(content), "func patched() {}") {
		t.Fatalf("expected apply_patch heredoc to be applied, got %q", string(content))
	}
	foundApplyPatch := false
	for _, msg := range session.Messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			if msg.ToolCalls[0].Name == "apply_patch" {
				foundApplyPatch = true
			}
			break
		}
	}
	if !foundApplyPatch {
		t.Fatalf("expected run_shell heredoc tool call to be rewritten to apply_patch, messages=%#v", session.Messages)
	}
}

func TestAgentRewritesRunShellApplyPatchHeredocWithCdWorkdir(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(subdir, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	command := strings.Join([]string{
		"cd subdir && apply_patch <<'PATCH'",
		"*** Begin Patch",
		"*** Update File: main.go",
		"@@",
		" package main",
		"+",
		"+func patchedFromSubdir() {}",
		"*** End Patch",
		"PATCH",
	}, "\n")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("run_shell", map[string]any{"command": command}),
			{Message: Message{Role: "assistant", Text: "subdir/main.go patched."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{Model: "model"},
		Client:    provider,
		Tools:     NewToolRegistry(NewRunShellTool(ws), NewApplyPatchTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "subdir/main.go를 수정해"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(content), "func patchedFromSubdir() {}") {
		t.Fatalf("expected cd workdir patch to update subdir target, got %q", string(content))
	}
	foundWorkdir := false
	for _, msg := range session.Messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 && msg.ToolCalls[0].Name == "apply_patch" {
			if got := stringValue(toolCallArgumentsMap(msg.ToolCalls[0]), "workdir"); got == "subdir" {
				foundWorkdir = true
			}
			break
		}
	}
	if !foundWorkdir {
		t.Fatalf("expected rewritten apply_patch call to preserve cd workdir, messages=%#v", session.Messages)
	}
}

func TestAgentRejectsImplicitRunShellPatchBodyLikeCodex(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: main.go",
		"@@",
		" package main",
		"+",
		"+func patchedAfterImplicitRetry() {}",
		"*** End Patch",
	}, "\n")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("run_shell", map[string]any{"command": patch}),
			toolCallResponse("apply_patch", map[string]any{"patch": patch}),
			{Message: Message{Role: "assistant", Text: "main.go patched after implicit invocation retry."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{Model: "model"},
		Client:    provider,
		Tools:     NewToolRegistry(NewRunShellTool(ws), NewApplyPatchTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "main.go를 수정해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "patched after implicit invocation retry") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(content), "func patchedAfterImplicitRetry() {}") {
		t.Fatalf("expected retry through apply_patch to update file, got %q", string(content))
	}
	foundImplicitRejection := false
	for _, msg := range session.Messages {
		if msg.Role == "tool" && msg.ToolName == "run_shell" && strings.Contains(msg.Text, "raw patch body was provided to run_shell") {
			foundImplicitRejection = true
			if !msg.IsError {
				t.Fatalf("expected implicit patch rejection to be an error tool result")
			}
			break
		}
	}
	if !foundImplicitRejection {
		t.Fatalf("expected raw run_shell patch body to be rejected before shell execution, messages=%#v", session.Messages)
	}
	var blockedEnd *ConversationEvent
	for i := range session.ConversationEvents {
		event := &session.ConversationEvents[i]
		if event.CorrelationID == "call-1" && event.Kind == conversationEventKindExecCommandEnd {
			blockedEnd = event
			break
		}
	}
	if blockedEnd == nil || blockedEnd.Entities["status"] != "declined" {
		t.Fatalf("expected implicit shell patch rejection to have declined exec end event, got %#v", session.ConversationEvents)
	}
}

func TestAgentNormalizesShellApplyPatchBeforeReadOnlyGate(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	command := strings.Join([]string{
		"apply_patch <<'PATCH'",
		"*** Begin Patch",
		"*** Update File: main.go",
		"@@",
		" package main",
		"+",
		"+func shouldNotPatch() {}",
		"*** End Patch",
		"PATCH",
	}, "\n")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("run_shell", map[string]any{"command": command}),
			{Message: Message{Role: "assistant", Text: "분석-only 요청에서 shell apply_patch 수정 시도는 read-only gate로 차단되었고, main.go 파일은 수정하지 않았습니다."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "main.go를 분석만 해. 파일은 수정하지 마",
	}}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{Model: "model"},
		Client:    provider,
		Tools:     NewToolRegistry(NewRunShellTool(ws), NewApplyPatchTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.completeLoop(context.Background(), true, false, false)
	if err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if !strings.Contains(reply, "수정하지 않았습니다") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(content), "shouldNotPatch") {
		t.Fatalf("read-only shell apply_patch heredoc should not modify file, got %q", string(content))
	}
	foundApplyPatchBlock := false
	for _, msg := range session.Messages {
		if msg.Role == "tool" && msg.ToolName == "apply_patch" && strings.Contains(msg.Text, "read-only analysis") {
			foundApplyPatchBlock = true
			if !msg.IsError {
				t.Fatalf("expected read-only apply_patch block to be an error tool result")
			}
			break
		}
	}
	if !foundApplyPatchBlock {
		t.Fatalf("expected shell heredoc to normalize to blocked apply_patch, messages=%#v", session.Messages)
	}
}

func TestRewriteShellApplyPatchToolCallsExtractsHeredocPayload(t *testing.T) {
	command := strings.Join([]string{
		"apply_patch <<'PATCH'",
		"*** Begin Patch",
		"*** Add File: main.go",
		"+package main",
		"*** End Patch",
		"PATCH",
	}, "\n")
	call := toolCallResponse("run_shell", map[string]any{
		"command":       command,
		"owner_node_id": "plan-02",
	}).Message.ToolCalls[0]
	rewritten := rewriteShellApplyPatchToolCalls([]ToolCall{call})
	if len(rewritten) != 1 || rewritten[0].Name != "apply_patch" {
		t.Fatalf("expected helper to recognize shell apply_patch when enabled, got %#v", rewritten)
	}
	args := toolCallArgumentsMap(rewritten[0])
	if !strings.Contains(stringValue(args, "patch"), "*** Add File: main.go") {
		t.Fatalf("expected patch payload to be preserved, args=%#v", args)
	}
	if got := stringValue(args, "owner_node_id"); got != "plan-02" {
		t.Fatalf("expected owner_node_id to be preserved, got %q", got)
	}
}

func TestShellCommandIsImplicitApplyPatchBodyMatchesCodexStrictness(t *testing.T) {
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Add File: main.go",
		"+package main",
		"*** End Patch",
	}, "\n")
	if !shellCommandIsImplicitApplyPatchBody(patch) {
		t.Fatalf("expected exact raw patch body to be detected as implicit apply_patch invocation")
	}
	for _, command := range []string{
		"echo before\n" + patch,
		patch + "\necho after",
		"```patch\n" + patch + "\n```",
		"*** Begin Patch\n*** End Patch\n*** Begin Patch\n*** End Patch",
		"*** Begin Patch\nnot a valid patch\n*** End Patch",
	} {
		if shellCommandIsImplicitApplyPatchBody(command) {
			t.Fatalf("expected non-exact or invalid raw patch shell command not to be treated as implicit invocation: %q", command)
		}
	}
}

func TestRewriteShellApplyPatchToolCallsMatchesCodexStrictShellForms(t *testing.T) {
	patchLines := []string{
		"*** Begin Patch",
		"*** Add File: main.go",
		"+package main",
		"*** End Patch",
		"PATCH",
	}
	valid := toolCallResponse("run_shell", map[string]any{
		"command": strings.Join(append([]string{"cd 'src dir' && applypatch <<'PATCH'"}, patchLines...), "\n"),
	}).Message.ToolCalls[0]
	rewritten := rewriteShellApplyPatchToolCalls([]ToolCall{valid})
	if rewritten[0].Name != "apply_patch" {
		t.Fatalf("expected cd/applypatch heredoc to rewrite, got %#v", rewritten[0])
	}
	if got := stringValue(toolCallArgumentsMap(rewritten[0]), "workdir"); got != "src dir" {
		t.Fatalf("expected quoted cd workdir to be preserved, got %q", got)
	}

	stripTabs := toolCallResponse("run_shell", map[string]any{
		"command": strings.Join(append([]string{"apply_patch <<-PATCH"}, patchLines[:len(patchLines)-1]...), "\n") + "\n\tPATCH",
	}).Message.ToolCalls[0]
	rewritten = rewriteShellApplyPatchToolCalls([]ToolCall{stripTabs})
	if rewritten[0].Name != "apply_patch" {
		t.Fatalf("expected tab-indented <<- heredoc marker to rewrite, got %#v", rewritten[0])
	}

	for _, command := range []string{
		strings.Join(append([]string{"echo before && apply_patch <<'PATCH'"}, patchLines...), "\n"),
		strings.Join(append([]string{"cd src; apply_patch <<'PATCH'"}, patchLines...), "\n"),
		strings.Join(append([]string{"cat <<'PATCH' | apply_patch"}, patchLines...), "\n"),
		strings.Join(append([]string{"cd src && apply_patch <<'PATCH'"}, append(patchLines, "&& echo done")...), "\n"),
		strings.Join(append([]string{"apply_patch foo <<'PATCH'"}, patchLines...), "\n"),
		strings.Join(append([]string{"cd foo && cd bar && apply_patch <<'PATCH'"}, patchLines...), "\n"),
		strings.Join(append([]string{"cd foo bar && apply_patch <<'PATCH'"}, patchLines...), "\n"),
		strings.Join(append([]string{"cd bar || apply_patch <<'PATCH'"}, patchLines...), "\n"),
		strings.Join(append([]string{"apply_patch <<'PATCH'"}, patchLines[:len(patchLines)-1]...), "\n") + "\n PATCH",
		strings.Join(append([]string{"apply_patch <<-PATCH"}, patchLines[:len(patchLines)-1]...), "\n") + "\n PATCH",
	} {
		call := toolCallResponse("run_shell", map[string]any{"command": command}).Message.ToolCalls[0]
		got := rewriteShellApplyPatchToolCalls([]ToolCall{call})
		if got[0].Name != "run_shell" {
			t.Fatalf("expected non-Codex shell form to remain run_shell, command=%q got=%#v", command, got[0])
		}
	}
}

func TestAgentSkipsInteractiveFinalAnswerReviewForGeneratedDocumentArtifact(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.TaskState = &TaskState{Goal: "create generated report"}
	session.AcceptanceContract = &AcceptanceContract{
		ID:           "accept-doc",
		SourcePrompt: "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 문서로 생성해",
		Mode:         "edit_code",
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	if agent.shouldReviewInteractiveFinalAnswer("Tavern/BugReport.md 생성 완료", true, false) {
		t.Fatalf("expected generated document artifact final answer to skip reviewer")
	}
}

func TestAgentFinalizesGeneratedDocumentArtifactFromGitChangedPathFallback(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	if err := os.MkdirAll(filepath.Join(root, "Tavern"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Tavern", "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runTestGit(t, root, "add", "Tavern/README.md")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "seed")
	if err := os.WriteFile(filepath.Join(root, "Tavern", "BugReport.md"), []byte("# Tavern Bug Report\n\n## BUG-001\n- File: Tavern/Tavern.cpp\n"), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}

	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해",
	}}
	session.LastCodingHarnessReport = &CodingHarnessReport{Approved: true}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	if !agent.shouldFinalizeGeneratedDocumentArtifactReply(session.Messages[0].Text, "Tavern/BugReport.md 생성 완료", false) {
		t.Fatalf("expected generated document finalization to use git changed-path fallback when patch transaction paths are unavailable")
	}
	if agent.shouldReviewInteractiveFinalAnswer("Tavern/BugReport.md 생성 완료", true, false) {
		t.Fatalf("expected generated document artifact final answer to skip reviewer from git changed-path fallback")
	}
}

func TestAgentFinalAnswerReviewerPromptIncludesEditLoopLedger(t *testing.T) {
	root := t.TempDir()
	mainProvider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "1. Update main.go\n2. Run focused verification\n3. Summarize changes and remaining risk",
				},
				StopReason: "stop",
			},
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {}\n"}),
			{
				Message: Message{
					Role: "assistant",
					Text: "Updated main.go, go test ./... passed, and no remaining blockers were recorded.",
				},
				StopReason: "stop",
			},
		},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "APPROVED\nThe execution plan is sound.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: strings.Join([]string{
						"REVIEW_RESULT",
						"verdict: approved",
						"summary: post-change review approved the edit",
						"findings:",
					}, "\n"),
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "APPROVED\nThe final answer ties the edit to verification and remaining risk.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			Model: "model",
		},
		Client:         mainProvider,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Tools:          NewToolRegistry(NewWriteFileTool(ws)),
		Workspace:      ws,
		Session:        session,
		Store:          store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			return VerificationReport{
				ChangedPaths: []string{"main.go"},
				Steps: []VerificationStep{{
					Label:  "go test ./...",
					Status: VerificationPassed,
					Output: "ok",
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "fix main.go and verify the result")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "go test ./... passed") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	var finalReviewPrompt string
	for _, req := range reviewer.requests {
		if len(req.Messages) == 0 {
			continue
		}
		text := req.Messages[len(req.Messages)-1].Text
		if strings.Contains(text, "Proposed final answer:") {
			finalReviewPrompt = text
		}
	}
	if finalReviewPrompt == "" {
		t.Fatalf("expected final-review prompt, got %#v", reviewer.requests)
	}
	for _, want := range []string{
		"Apply/verify/retry ledger:",
		"Expected final answer outcome contract:",
		"main.go",
		"Verification [passed]",
		"Worker/apply summary",
	} {
		if !strings.Contains(finalReviewPrompt, want) {
			t.Fatalf("expected final-review prompt to include %q, got:\n%s", want, finalReviewPrompt)
		}
	}
	if session.ActiveEditLoop != nil {
		t.Fatalf("expected edit loop to be finalized after return, got %#v", session.ActiveEditLoop)
	}
	if len(session.EditLoops) == 0 || session.EditLoops[0].FinalReviewVerdict != "approved" {
		t.Fatalf("expected approved final review in edit loop, got %#v", session.EditLoops)
	}
}

func TestBuildRecoveryGuidanceCanRefreshExecutionPlan(t *testing.T) {
	root := t.TempDir()
	mainProvider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "1. Poll the active shell job\n2. Inspect the failing output\n3. Only rerun verification if the job output is stale",
				},
				StopReason: "stop",
			},
		},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "- The loop is repeating a stale verification step.\n- Poll the running shell job before launching another build.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "APPROVED\nThe refreshed plan breaks the loop.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		Goal:           "Finish the long-running verification flow without rerunning the same build.",
		PlanSummary:    "1. Run the build\n2. Rerun the build again",
		FailedAttempts: []string{"Repeated the same build", "Repeated the same build again"},
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config: Config{
			Model: "model",
		},
		Client:         mainProvider,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Tools:          NewToolRegistry(),
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          store,
	}

	guidance := agent.buildRecoveryGuidance(context.Background(), string(recoveryTriggerRepeatedToolError), "fallback recovery", "recent tools", "same failure")
	if !strings.Contains(guidance, "Refreshed execution plan") {
		t.Fatalf("expected refreshed execution plan in guidance, got %q", guidance)
	}
	if session.TaskState == nil || session.TaskState.PlanRefreshCount == 0 {
		t.Fatalf("expected plan refresh count to increase, got %#v", session.TaskState)
	}
	if len(session.Plan) == 0 {
		t.Fatalf("expected refreshed session plan items")
	}
	if !strings.Contains(session.TaskState.PlanSummary, "Poll the active shell job") {
		t.Fatalf("expected refreshed plan summary, got %#v", session.TaskState)
	}
}

func TestNoteToolExecutionResultKeepsSharedPlanInProgressWhenBackgroundJobStarts(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.TaskState = &TaskState{}
	session.Plan = []PlanItem{
		{Step: "Start background verification", Status: "in_progress"},
		{Step: "Poll the background job", Status: "pending"},
	}
	now := time.Now()
	session.BackgroundJobs = []BackgroundShellJob{{
		ID:             "job-1",
		Command:        "go test ./...",
		CommandSummary: "go test ./...",
		Status:         "running",
		StartedAt:      now,
		UpdatedAt:      now,
	}}
	agent := &Agent{
		Session: session,
	}

	agent.noteToolExecutionResult(ToolCall{Name: "run_shell_background"}, "started background shell job job-1 [running]\ncommand: go test ./...\nstatus: running", nil)

	if session.Plan[0].Status != "in_progress" || session.Plan[1].Status != "pending" {
		t.Fatalf("expected shared plan to remain in progress while background job is only started, got %#v", session.Plan)
	}
	if session.TaskState.PlanCursor != 0 {
		t.Fatalf("expected plan cursor to stay at 0, got %d", session.TaskState.PlanCursor)
	}
	if !slices.Contains(session.TaskState.PendingChecks, backgroundShellJobPendingCheck) {
		t.Fatalf("expected pending background-job check, got %#v", session.TaskState.PendingChecks)
	}
}

func TestNoteToolExecutionResultAdvancesSharedPlanOnlyAfterBackgroundBundleCompletes(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		PendingChecks: []string{backgroundShellJobPendingCheck},
	}
	session.Plan = []PlanItem{
		{Step: "Poll the background jobs", Status: "in_progress"},
		{Step: "Summarize the result", Status: "pending"},
	}
	now := time.Now()
	session.BackgroundJobs = []BackgroundShellJob{
		{ID: "job-1", Status: "completed", StartedAt: now, UpdatedAt: now},
		{ID: "job-2", Status: "running", StartedAt: now, UpdatedAt: now},
	}
	agent := &Agent{
		Session: session,
	}

	agent.noteToolExecutionResult(ToolCall{Name: "check_shell_bundle"}, "summary: completed=1 running=1 failed=0 total=2\n- job-1 [completed]\n- job-2 [running]", nil)

	if session.Plan[0].Status != "in_progress" || session.TaskState.PlanCursor != 0 {
		t.Fatalf("expected plan to remain in progress while bundle still has running jobs, got %#v cursor=%d", session.Plan, session.TaskState.PlanCursor)
	}
	if !slices.Contains(session.TaskState.PendingChecks, backgroundShellJobPendingCheck) {
		t.Fatalf("expected pending background-job check while bundle is still running, got %#v", session.TaskState.PendingChecks)
	}

	session.BackgroundJobs[1].Status = "completed"
	agent.noteToolExecutionResult(ToolCall{Name: "check_shell_bundle"}, "summary: completed=2 running=0 failed=0 total=2\n- job-1 [completed]\n- job-2 [completed]", nil)

	if session.Plan[0].Status != "completed" || session.Plan[1].Status != "in_progress" {
		t.Fatalf("expected shared plan to advance after bundle completion, got %#v", session.Plan)
	}
	if session.TaskState.PlanCursor != 1 {
		t.Fatalf("expected plan cursor to advance to 1, got %d", session.TaskState.PlanCursor)
	}
	if slices.Contains(session.TaskState.PendingChecks, backgroundShellJobPendingCheck) {
		t.Fatalf("expected background-job pending check to clear after completion, got %#v", session.TaskState.PendingChecks)
	}
}

func TestCompleteLoopLeavesSharedPlanOpenWhenBackgroundWorkRemains(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "The fix is partially understood, but the background verification job is still running.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		Goal:        "Finish the long-running verification flow.",
		Phase:       "execution",
		PlanSummary: "1. Start background verification\n2. Poll the result\n3. Summarize the status",
		PendingChecks: []string{
			backgroundShellJobPendingCheck,
		},
	}
	session.Plan = []PlanItem{
		{Step: "Start background verification", Status: "completed"},
		{Step: "Poll the result", Status: "in_progress"},
		{Step: "Summarize the status", Status: "pending"},
	}
	session.syncTaskStatePlanCursor()
	now := time.Now()
	session.BackgroundJobs = []BackgroundShellJob{{
		ID:             "job-1",
		Command:        "go test ./...",
		CommandSummary: "go test ./...",
		Status:         "running",
		StartedAt:      now,
		UpdatedAt:      now,
	}}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.completeLoop(context.Background(), false, false, false)
	if err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if !strings.Contains(reply, "still running") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if session.TaskState == nil {
		t.Fatalf("expected task state to be preserved")
	}
	if session.TaskState.Phase == "done" {
		t.Fatalf("expected task state to remain open while background work remains, got %#v", session.TaskState)
	}
	if session.Plan[1].Status != "in_progress" || session.TaskState.PlanCursor != 1 {
		t.Fatalf("expected shared plan to remain open, got %#v cursor=%d", session.Plan, session.TaskState.PlanCursor)
	}

	reloaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.TaskState == nil || reloaded.TaskState.Phase == "done" {
		t.Fatalf("expected persisted task state to stay open, got %#v", reloaded.TaskState)
	}
	if len(reloaded.Plan) < 2 || reloaded.Plan[1].Status != "in_progress" {
		t.Fatalf("expected persisted shared plan to remain open, got %#v", reloaded.Plan)
	}
}

func TestNoteVerificationResultRemovesOnlyVerificationPendingCheck(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		PendingChecks: []string{
			verificationPendingCheck,
			backgroundShellJobPendingCheck,
		},
	}
	session.Plan = []PlanItem{
		{Step: "Verify the result", Status: "in_progress"},
		{Step: "Poll the background job", Status: "pending"},
	}
	now := time.Now()
	session.BackgroundJobs = []BackgroundShellJob{{
		ID:        "job-1",
		Status:    "running",
		StartedAt: now,
		UpdatedAt: now,
	}}
	agent := &Agent{
		Session: session,
	}

	agent.noteVerificationResult(VerificationReport{
		Steps: []VerificationStep{{
			Label:  "go test ./...",
			Status: VerificationPassed,
		}},
	})

	if slices.Contains(session.TaskState.PendingChecks, verificationPendingCheck) {
		t.Fatalf("expected verification pending check to be removed, got %#v", session.TaskState.PendingChecks)
	}
	if !slices.Contains(session.TaskState.PendingChecks, backgroundShellJobPendingCheck) {
		t.Fatalf("expected background pending check to remain, got %#v", session.TaskState.PendingChecks)
	}
	if session.TaskState.Phase != "execution" {
		t.Fatalf("expected task state to stay in execution while background work remains, got %#v", session.TaskState)
	}
	if session.TaskState.NextStep != backgroundShellJobPendingCheck {
		t.Fatalf("expected next step to point at the remaining background check, got %#v", session.TaskState)
	}
}

func TestAssignFocusedOwnerNodeToToolCalls(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		ExecutorFocusNode: "plan-02",
	}
	session.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{
				ID:                 "plan-02",
				EditableLeasePaths: []string{"main.go"},
			},
		},
	}

	calls := []ToolCall{
		{
			Name:      "apply_patch",
			Arguments: `{"patch":"*** Begin Patch\n*** End Patch\n"}`,
		},
		{
			Name:      "run_shell",
			Arguments: `{"command":"go test ./..."}`,
		},
		{
			Name:      "read_file",
			Arguments: `{"path":"main.go"}`,
		},
	}

	updated := assignFocusedOwnerNodeToToolCalls(calls, session)
	if got := stringValue(toolCallArgumentsMap(updated[0]), "owner_node_id"); got != "plan-02" {
		t.Fatalf("expected apply_patch owner_node_id to be injected, got %#v", updated[0])
	}
	if got := stringValue(toolCallArgumentsMap(updated[1]), "owner_node_id"); got != "plan-02" {
		t.Fatalf("expected run_shell owner_node_id to be injected, got %#v", updated[1])
	}
	if got := stringValue(toolCallArgumentsMap(updated[2]), "owner_node_id"); got != "" {
		t.Fatalf("expected read-only tool call to remain unchanged, got %#v", updated[2])
	}
}

func TestAssignFocusedOwnerNodeToToolCallsSkipsUnroutedFocusNode(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		ExecutorFocusNode: "plan-02",
	}
	session.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{
				ID:    "plan-02",
				Title: "Review focused code",
			},
		},
	}

	calls := []ToolCall{
		{
			Name:      "apply_patch",
			Arguments: `{"patch":"*** Begin Patch\n*** End Patch\n"}`,
		},
	}

	updated := assignFocusedOwnerNodeToToolCalls(calls, session)
	if got := stringValue(toolCallArgumentsMap(updated[0]), "owner_node_id"); got != "" {
		t.Fatalf("expected unrouted focus node not to be injected as owner_node_id, got %#v", updated[0])
	}
}

func TestAssignFocusedOwnerNodeToToolCallsPreservesExplicitOwner(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		ExecutorFocusNode: "plan-02",
	}

	calls := []ToolCall{
		{
			Name:      "run_shell",
			Arguments: `{"command":"go test ./...","owner_node_id":"plan-99"}`,
		},
	}

	updated := assignFocusedOwnerNodeToToolCalls(calls, session)
	if got := stringValue(toolCallArgumentsMap(updated[0]), "owner_node_id"); got != "plan-99" {
		t.Fatalf("expected explicit owner_node_id to be preserved, got %#v", updated[0])
	}
}

func TestStripUnsupportedOwnerNodeIDFromToolCalls(t *testing.T) {
	calls := []ToolCall{
		{
			Name:      "read_file",
			Arguments: `{"path":"main.go","owner_node_id":"plan-02"}`,
		},
		{
			Name:      "grep",
			Arguments: `{"pattern":"package","owner_node_id":"plan-02"}`,
		},
		{
			Name:      "list_files",
			Arguments: `{"path":".","owner_node_id":"plan-02"}`,
		},
		{
			Name:      "run_shell",
			Arguments: `{"command":"go test ./...","owner_node_id":"plan-02"}`,
		},
		{
			Name:      "mcp_custom_tool",
			Arguments: `{"owner_node_id":"domain-owned-value"}`,
		},
	}

	updated := stripUnsupportedOwnerNodeIDFromToolCalls(calls)
	if got := stringValue(toolCallArgumentsMap(updated[0]), "owner_node_id"); got != "" {
		t.Fatalf("expected read_file owner_node_id to be stripped, got %#v", updated[0])
	}
	if got := stringValue(toolCallArgumentsMap(updated[1]), "owner_node_id"); got != "" {
		t.Fatalf("expected grep owner_node_id to be stripped, got %#v", updated[1])
	}
	if got := stringValue(toolCallArgumentsMap(updated[2]), "owner_node_id"); got != "" {
		t.Fatalf("expected list_files owner_node_id to be stripped, got %#v", updated[2])
	}
	if got := stringValue(toolCallArgumentsMap(updated[3]), "owner_node_id"); got != "plan-02" {
		t.Fatalf("expected run_shell owner_node_id to be preserved, got %#v", updated[3])
	}
	if got := stringValue(toolCallArgumentsMap(updated[4]), "owner_node_id"); got != "domain-owned-value" {
		t.Fatalf("expected custom tool owner_node_id to be preserved, got %#v", updated[4])
	}
}

func TestAgentStripsUnsupportedOwnerNodeIDFromReadOnlyToolCalls(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	ws.ResolveEditTarget = func(req EditRoutingRequest) (EditRoutingResult, error) {
		req = req.normalized()
		if req.lookupIntent() && strings.TrimSpace(req.OwnerNodeID) != "" {
			return EditRoutingResult{}, fmt.Errorf("%w: read-only lookup should not carry owner_node_id", ErrEditTargetMismatch)
		}
		return ws.resolveEditFallback(req)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("read_file", map[string]any{
				"path":          "main.go",
				"owner_node_id": "plan-02",
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "main.go inspection completed.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "read main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "inspection completed") {
		t.Fatalf("expected final reply after read_file, got %q", reply)
	}
	if len(session.Messages) < 3 {
		t.Fatalf("expected user, assistant tool call, and tool result messages, got %#v", session.Messages)
	}
	assistantCall := session.Messages[1]
	if len(assistantCall.ToolCalls) != 1 {
		t.Fatalf("expected one assistant tool call, got %#v", assistantCall.ToolCalls)
	}
	if got := stringValue(toolCallArgumentsMap(assistantCall.ToolCalls[0]), "owner_node_id"); got != "" {
		t.Fatalf("expected assistant read_file call to be sanitized before execution, got %#v", assistantCall.ToolCalls[0])
	}
	toolResult := session.Messages[2]
	if toolResult.IsError {
		t.Fatalf("expected read_file to execute without ownership mismatch, got %#v", toolResult)
	}
	if !strings.Contains(toolResult.Text, "package main") {
		t.Fatalf("expected read_file result content, got %q", toolResult.Text)
	}
}

func TestAgentStripsUnsupportedOwnerNodeIDFromListFilesToolCall(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	ws.ResolveEditTarget = func(req EditRoutingRequest) (EditRoutingResult, error) {
		req = req.normalized()
		if req.lookupIntent() && strings.TrimSpace(req.OwnerNodeID) != "" {
			return EditRoutingResult{}, fmt.Errorf("%w: list_files should not carry owner_node_id", ErrEditTargetMismatch)
		}
		return ws.resolveEditFallback(req)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{
				"path":          ".",
				"owner_node_id": "plan-02",
			}),
			{
				Message: Message{
					Role: "assistant",
					Text: "listing completed.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "list files")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "listing completed") {
		t.Fatalf("expected final reply after list_files, got %q", reply)
	}
	if len(session.Messages) < 3 {
		t.Fatalf("expected user, assistant tool call, and tool result messages, got %#v", session.Messages)
	}
	assistantCall := session.Messages[1]
	if len(assistantCall.ToolCalls) != 1 {
		t.Fatalf("expected one assistant tool call, got %#v", assistantCall.ToolCalls)
	}
	if got := stringValue(toolCallArgumentsMap(assistantCall.ToolCalls[0]), "owner_node_id"); got != "" {
		t.Fatalf("expected assistant list_files call to be sanitized before execution, got %#v", assistantCall.ToolCalls[0])
	}
	toolResult := session.Messages[2]
	if toolResult.IsError {
		t.Fatalf("expected list_files to execute without ownership mismatch, got %#v", toolResult)
	}
	if !strings.Contains(toolResult.Text, "main.go") {
		t.Fatalf("expected list_files result content, got %q", toolResult.Text)
	}
}
