package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	s.requests = append(s.requests, req)
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
	s.requests = append(s.requests, req)
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
				Steps: []VerificationStep{{
					Label:  "go test ./...",
					Status: VerificationFailed,
					Output: "failing test",
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
					Steps: []VerificationStep{{
						Label:  "go test ./...",
						Status: VerificationFailed,
						Output: "failing test",
					}},
				}, true
			}
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
				RequiredFix: "현재 항목만 건너뛰고 다음 항목을 계속 처리하세요.",
			},
			{
				ID:          "RF-002",
				Source:      "model",
				Severity:    reviewSeverityMedium,
				Category:    "correctness",
				Title:       "고정 버퍼가 정상 결과를 누락할 수 있습니다",
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
			WarningFindings: []string{"RF-001"},
		},
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Source:      "model",
			Severity:    reviewSeverityMedium,
			Category:    "correctness",
			Title:       "경계 조건 누락",
			RequiredFix: "경계 조건을 검사하세요.",
		}},
	}

	if assistantTextIncludesPreFixReviewSummary("검토 결과를 바탕으로 수정하겠습니다.", run) {
		t.Fatalf("generic review wording should not satisfy visible RF summary")
	}
	if !assistantTextIncludesPreFixReviewSummary("검토 결과:\n- RF-001: 경계 조건을 보강합니다.", run) {
		t.Fatalf("expected explicit RF item to satisfy visible review summary")
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

	reply, err := agent.Reply(context.Background(), "@SampleApp/SampleWorker/PathConverter.cpp:1-3 검토하고 버그를 수정해")
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

	reply, err := agent.Reply(context.Background(), "@SampleApp/SampleWorker/PathConverter.cpp:1-3 검토하고 버그를 수정해")
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
	if replaceTool.calls != 2 {
		t.Fatalf("expected exactly two mismatched edit attempts, got %d", replaceTool.calls)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected agent to stop on the second edit mismatch, got %d requests", len(provider.requests))
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
	if len(reviewer.requests) != 3 {
		t.Fatalf("expected plan review + 2 final reviews, got %d requests", len(reviewer.requests))
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
