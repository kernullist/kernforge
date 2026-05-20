package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecentErrorQuestionAnswersFromConversationEventWithoutModelCall(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openrouter", "deepseek/deepseek-v4-pro", "", "default")
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindProviderError,
		Severity: conversationSeverityError,
		Summary:  "provider error | provider=openrouter | upstream=DeepInfra | model=deepseek/deepseek-v4-flash | shard=SampleKernel/SampleKernel/BuildCab_refined_03 | code=429 | category=rate_limit",
		Raw:      `openai API error (429 Too Many Requests): Provider returned error | raw={"error":{"message":"deepseek/deepseek-v4-flash is temporarily rate-limited upstream","metadata":{"provider_name":"DeepInfra","is_byok":false}}}`,
		Entities: map[string]string{
			"provider":  "openrouter",
			"upstream":  "DeepInfra",
			"model":     "deepseek/deepseek-v4-flash",
			"shard":     "SampleKernel/SampleKernel/BuildCab_refined_03",
			"code":      "429",
			"category":  "rate_limit",
			"retryable": "true",
			"byok_hint": "true",
		},
	})
	provider := &scriptedProviderClient{}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	reply, err := agent.Reply(context.Background(), "방금 에러는 왜 난거야?")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("expected no provider call for recent error resolver, got %d", len(provider.requests))
	}
	for _, want := range []string{"rate limit", "DeepInfra", "deepseek/deepseek-v4-flash", "SampleKernel/SampleKernel/BuildCab_refined_03", "429"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("expected reply to contain %q, got %q", want, reply)
		}
	}
	if len(session.Messages) == 0 || session.Messages[len(session.Messages)-1].Role != "assistant" {
		t.Fatalf("expected recent error resolver to persist assistant reply, got %#v", session.Messages)
	}
	if session.Messages[len(session.Messages)-1].Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected recent error resolver reply to be final-answer phase, got %#v", session.Messages[len(session.Messages)-1])
	}
	if session.ConversationState == nil {
		t.Fatalf("expected recent error resolver to refresh conversation state")
	}
	if len(session.ConversationEvents) == 0 || session.ConversationEvents[len(session.ConversationEvents)-1].Kind != conversationEventKindAssistantReply {
		t.Fatalf("expected recent error resolver to record assistant event, got %#v", session.ConversationEvents)
	}
}

func TestReplyWithNilLongMemDoesNotPanic(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: "ok"},
		}},
	}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	reply, err := agent.Reply(context.Background(), "간단히 답해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "ok") {
		t.Fatalf("expected scripted reply, got %q", reply)
	}
}

func TestRecentErrorAnswerListsAlternateCandidates(t *testing.T) {
	primary := ConversationEvent{
		Kind:     conversationEventKindProviderError,
		Severity: conversationSeverityError,
		Summary:  "provider error | code=429 | category=rate_limit",
		Raw:      "429 Too Many Requests",
		Entities: map[string]string{
			"provider": "openrouter",
			"model":    "deepseek/deepseek-v4-flash",
			"code":     "429",
			"category": "rate_limit",
		},
	}
	alternates := []ConversationEvent{
		{
			Kind:     conversationEventKindToolError,
			Severity: conversationSeverityError,
			Summary:  "tool error | tool=go_test | code=exit1",
			Entities: map[string]string{
				"tool":     "go_test",
				"category": "command_failure",
			},
		},
	}
	reply := renderRecentErrorAnswer(primary, alternates)
	if !strings.Contains(reply, "다른 최근 오류 후보") || !strings.Contains(reply, "go_test") || !strings.Contains(reply, "command_failure") {
		t.Fatalf("expected alternate error details, got %q", reply)
	}
}

func TestConversationRuntimeContextIsInjectedIntoNormalTurn(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindToolError,
		Severity: conversationSeverityError,
		Summary:  "tool error | tool=go_test | code=exit1 | package failed",
		Raw:      "go test ./... failed",
		Entities: map[string]string{
			"tool":     "run_shell",
			"category": "command_failure",
		},
	})
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "상태를 설명했습니다."}},
		},
	}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	if _, err := agent.Reply(context.Background(), "현재 상태 알려줘"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected one provider request, got %d", len(provider.requests))
	}
	userText := latestUserMessageText(provider.requests[0].Messages)
	if !strings.Contains(userText, "[Conversation Runtime Context]") {
		t.Fatalf("expected conversation runtime context in prompt, got %q", userText)
	}
	if !strings.Contains(userText, "go test ./... failed") {
		t.Fatalf("expected recent error raw text in prompt, got %q", userText)
	}
}

func TestProviderErrorIsRecordedInConversationEvents(t *testing.T) {
	root := t.TempDir()
	provider := &providerErrorClient{
		err: &ProviderAPIError{
			Provider:       "openrouter",
			StatusCode:     429,
			Status:         "429 Too Many Requests",
			Message:        "Provider returned error",
			Code:           "429",
			RawBody:        `{"error":{"message":"temporarily rate-limited upstream","metadata":{"provider_name":"DeepInfra","is_byok":false}}}`,
			RequestSummary: `{"model":"deepseek/deepseek-v4-flash"}`,
		},
	}
	session := NewSession(root, "openrouter", "deepseek/deepseek-v4-flash", "", "default")
	cfg := DefaultConfig(root)
	cfg.MaxRequestRetries = 0
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	_, err := agent.Reply(context.Background(), "분석해줘")
	if err == nil {
		t.Fatalf("expected provider error")
	}
	events := recentErrorEvents(session, 1)
	if len(events) != 1 {
		t.Fatalf("expected one recent error event, got %d", len(events))
	}
	if events[0].Kind != conversationEventKindProviderError {
		t.Fatalf("expected provider error event, got %#v", events[0])
	}
	if events[0].Entities["code"] != "429" || events[0].Entities["category"] != "rate_limit" {
		t.Fatalf("expected normalized 429 rate limit event, got %#v", events[0].Entities)
	}
	data, err := os.ReadFile(runtimeErrorLogPath(root))
	if err != nil {
		t.Fatalf("read runtime error log: %v", err)
	}
	logText := string(data)
	for _, want := range []string{"provider_error", "openrouter", "deepseek/deepseek-v4-flash", "DeepInfra", `"final":"true"`} {
		if !strings.Contains(logText, want) {
			t.Fatalf("expected runtime error log to contain %q, got %q", want, logText)
		}
	}
}

func TestToolSuccessIsRecordedInConversationEvents(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			{Message: Message{Role: "assistant", Text: "읽었습니다."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	if _, err := agent.Reply(context.Background(), "main.go 읽어줘"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	starts := latestEventsByKind(session.ConversationEvents, conversationEventKindToolCall)
	results := latestEventsByKind(session.ConversationEvents, conversationEventKindToolResult)
	if len(starts) == 0 || starts[0].Entities["tool"] != "read_file" || starts[0].Entities["path"] != "main.go" {
		t.Fatalf("expected read_file start event, got %#v", starts)
	}
	if len(results) == 0 || results[0].Entities["tool"] != "read_file" || !strings.Contains(results[0].Raw, "package main") {
		t.Fatalf("expected read_file result event with output summary, got %#v", results)
	}
	if session.ConversationState == nil || !strings.Contains(session.ConversationState.LastCommand, "read_file") {
		t.Fatalf("expected active state to remember last tool command, got %#v", session.ConversationState)
	}
}

func TestShellToolRecordsCodexStyleLifecycleEvents(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	call := ToolCall{
		ID:        "call-shell",
		Name:      "run_shell",
		Arguments: `{"command":"go test ./...","workdir":"pkg"}`,
	}
	agent.noteToolConversationStart(call)
	agent.noteToolConversationResult(call, ToolExecutionResult{
		DisplayText: "ok",
		Meta: map[string]any{
			"command":                  "go test ./...",
			"work_dir":                 filepath.Join(root, "pkg"),
			"command_execution_status": "completed",
			"success":                  true,
		},
	})

	begins := latestEventsByKind(session.ConversationEvents, conversationEventKindExecCommandBegin)
	ends := latestEventsByKind(session.ConversationEvents, conversationEventKindExecCommandEnd)
	if len(begins) != 1 || begins[0].CorrelationID != "call-shell" {
		t.Fatalf("expected one exec begin event paired to call id, got %#v", begins)
	}
	if begins[0].Entities["command"] != "go test ./..." || begins[0].Entities["workdir"] != "pkg" {
		t.Fatalf("expected exec begin command/workdir entities, got %#v", begins[0].Entities)
	}
	if len(ends) != 1 || ends[0].CorrelationID != "call-shell" {
		t.Fatalf("expected one exec end event paired to call id, got %#v", ends)
	}
	if ends[0].Entities["status"] != "completed" || ends[0].Entities["work_dir"] != filepath.Join(root, "pkg") {
		t.Fatalf("expected exec end status/work_dir entities, got %#v", ends[0].Entities)
	}
}

func TestMCPToolRecordsCodexStyleLifecycleEvents(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	call := ToolCall{
		ID:        "call-mcp",
		Name:      "mcp__fake__echo",
		Arguments: `{"message":"hello"}`,
	}
	agent.noteToolConversationStart(call)
	agent.noteToolConversationResult(call, ToolExecutionResult{
		DisplayText: "echo: hello",
		Meta: map[string]any{
			"mcp_server": "fake",
			"mcp_tool":   "echo",
			"mcp_result_content": []map[string]any{{
				"type": "text",
				"text": "echo: hello",
			}},
			"mcp_result_structured_content": map[string]any{
				"echoed": "hello",
			},
			"mcp_is_error": false,
			"_meta": map[string]any{
				"trace_id": "trace-hello",
			},
		},
	})

	begins := latestEventsByKind(session.ConversationEvents, conversationEventKindMCPToolCallBegin)
	ends := latestEventsByKind(session.ConversationEvents, conversationEventKindMCPToolCallEnd)
	if len(begins) != 1 || begins[0].CorrelationID != "call-mcp" {
		t.Fatalf("expected one MCP begin event paired to call id, got %#v", begins)
	}
	if begins[0].Entities["mcp_server"] != "fake" || begins[0].Entities["mcp_tool"] != "echo" {
		t.Fatalf("expected MCP begin invocation entities, got %#v", begins[0].Entities)
	}
	if len(ends) != 1 || ends[0].CorrelationID != "call-mcp" {
		t.Fatalf("expected one MCP end event paired to call id, got %#v", ends)
	}
	if ends[0].Entities["status"] != "completed" || ends[0].Entities["mcp_namespaced_tool"] != "mcp__fake__echo" {
		t.Fatalf("expected MCP end status/namespaced tool entities, got %#v", ends[0].Entities)
	}
	mcpResult, ok := ends[0].Metadata["mcp_result"].(map[string]any)
	if !ok {
		t.Fatalf("expected MCP result metadata on lifecycle end, got %#v", ends[0].Metadata)
	}
	if _, ok := mcpResult["structuredContent"]; !ok {
		t.Fatalf("expected Codex-style structuredContent metadata, got %#v", mcpResult)
	}
	if mcpResult["isError"] != false {
		t.Fatalf("expected Codex-style isError metadata, got %#v", mcpResult)
	}
	if session.ConversationState == nil || session.ConversationState.CurrentWorkflow != "mcp__fake__echo" {
		t.Fatalf("expected MCP lifecycle event to refresh workflow state, got %#v", session.ConversationState)
	}
}

func TestApplyPatchRecordsCodexStyleLifecycleEvents(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Add File: main.go",
		"+package main",
		"*** End Patch",
		"",
	}, "\n")
	call := ToolCall{
		ID:        "call-patch",
		Name:      "apply_patch",
		Arguments: mustJSON(map[string]any{"patch": patch}),
	}
	agent.noteToolConversationStart(call)
	agent.noteToolConversationResult(call, ToolExecutionResult{
		DisplayText: "Patch applied: added main.go",
		Meta: map[string]any{
			"changed_paths":         []string{"main.go"},
			"changed_count":         1,
			"patch_operation_count": 1,
			"unified_diff": strings.Join([]string{
				"diff --git a/main.go b/main.go",
				"new file mode 100644",
				"--- /dev/null",
				"+++ b/main.go",
				"@@ -0,0 +1,1 @@",
				"+package main",
			}, "\n"),
			"success": true,
		},
	})

	begins := latestEventsByKind(session.ConversationEvents, conversationEventKindPatchApplyBegin)
	ends := latestEventsByKind(session.ConversationEvents, conversationEventKindPatchApplyEnd)
	turnDiffs := latestEventsByKind(session.ConversationEvents, conversationEventKindTurnDiff)
	if len(begins) != 1 || begins[0].CorrelationID != "call-patch" {
		t.Fatalf("expected one patch begin event paired to call id, got %#v", begins)
	}
	if begins[0].Entities["changed_paths"] != "main.go" || begins[0].Entities["patch_operation_count"] != "1" {
		t.Fatalf("expected parsed patch begin entities, got %#v", begins[0].Entities)
	}
	if len(ends) != 1 || ends[0].Entities["status"] != "completed" {
		t.Fatalf("expected one completed patch end event, got %#v", ends)
	}
	if ends[0].Entities["changed_paths"] != "main.go" || ends[0].Entities["changed_count"] != "1" {
		t.Fatalf("expected patch end changed path/count entities, got %#v", ends[0].Entities)
	}
	if len(turnDiffs) != 1 || !strings.Contains(turnDiffs[0].Raw, "diff --git a/main.go b/main.go") {
		t.Fatalf("expected turn diff event with unified diff, got %#v", turnDiffs)
	}
	if turnDiffs[0].Entities["file_count"] != "1" || turnDiffs[0].Entities["line_count"] == "" {
		t.Fatalf("expected turn diff file/line counts, got %#v", turnDiffs[0].Entities)
	}
}

func TestToolFailureRecordsCodexStyleEndEvent(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	call := ToolCall{
		ID:        "call-shell-failed",
		Name:      "run_shell",
		Arguments: `{"command":"go test ./..."}`,
	}
	agent.noteToolConversationStart(call)
	err := errors.New("exit status 1")
	agent.noteToolConversationFailureResult(call, ToolExecutionResult{DisplayText: "FAIL"}, err, false)
	agent.noteToolConversationError(call, err, "FAIL")

	ends := latestEventsByKind(session.ConversationEvents, conversationEventKindExecCommandEnd)
	errors := latestEventsByKind(session.ConversationEvents, conversationEventKindCommandError)
	if len(ends) != 1 || ends[0].Entities["status"] != "failed" || ends[0].Severity != conversationSeverityError {
		t.Fatalf("expected failed exec end event, got %#v", ends)
	}
	if len(errors) != 1 || errors[0].CorrelationID != "call-shell-failed" {
		t.Fatalf("expected paired command error event, got %#v", errors)
	}
	if session.ConversationState == nil || !strings.Contains(session.ConversationState.LastError, "exec_command end: failed") {
		t.Fatalf("expected failed lifecycle event to refresh last error state, got %#v", session.ConversationState)
	}
}

func TestCompactPreservesConversationWorkingMemoryForResume(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "scripted", "model", "", "default")
	for i := 0; i < 24; i++ {
		session.AddMessage(Message{Role: "user", Text: "older message"})
	}
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindProviderError,
		Severity: conversationSeverityError,
		Summary:  "provider error | provider=openrouter | model=deepseek/deepseek-v4-flash | code=429 | category=rate_limit",
		Raw:      "429 Too Many Requests",
		Entities: map[string]string{
			"provider":  "openrouter",
			"model":     "deepseek/deepseek-v4-flash",
			"code":      "429",
			"category":  "rate_limit",
			"retryable": "true",
		},
	})
	agent := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	summary := agent.Compact("test compact")
	if !strings.Contains(summary, "[Conversation Working Memory]") || !strings.Contains(summary, "429") {
		t.Fatalf("expected compact summary to preserve working memory, got %q", summary)
	}
	loaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	resumed := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   loaded,
		Store:     store,
	}
	reply, ok := resumed.maybeAnswerRecentErrorQuestion("아까 오류 왜 난거야?")
	if !ok {
		t.Fatalf("expected resumed recent error resolver to answer")
	}
	if !strings.Contains(reply, "429") || !strings.Contains(reply, "rate limit") {
		t.Fatalf("expected resumed reply to explain 429 rate limit, got %q", reply)
	}
}

type providerErrorClient struct {
	err error
}

func (p *providerErrorClient) Name() string {
	return "provider-error"
}

func (p *providerErrorClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	_ = req
	return ChatResponse{}, p.err
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
