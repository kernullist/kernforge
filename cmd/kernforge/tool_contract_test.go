package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

type toolContractRecordingTool struct {
	name     string
	readOnly bool
	calls    int
}

func (t *toolContractRecordingTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        t.name,
		Description: "recording tool for tool contract tests",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t *toolContractRecordingTool) Execute(ctx context.Context, input any) (string, error) {
	_ = ctx
	_ = input
	t.calls++
	return "executed " + t.name, nil
}

func (t *toolContractRecordingTool) ReadOnlyToolCall() bool {
	return t.readOnly
}

type toolContractCaptureClient struct {
	req ChatRequest
}

func (c *toolContractCaptureClient) Name() string {
	return "capture"
}

func (c *toolContractCaptureClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	c.req = req
	return ChatResponse{Message: Message{Role: "assistant", Text: "ok"}}, nil
}

func TestToolContractMalformedArgumentsCreatesInvalidSyntheticResult(t *testing.T) {
	root := t.TempDir()
	readTool := &toolContractRecordingTool{name: "read_file", readOnly: true}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
				ID:        "call_bad_args",
				Name:      "read_file",
				Arguments: `{"path":`,
			}}}},
			{Message: Message{Role: "assistant", Text: "invalid args handled"}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: "main.go를 읽어줘"}}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Client:    provider,
		Tools:     NewToolRegistry(readTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	if _, err := agent.completeLoop(context.Background(), false, false, false); err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if readTool.calls != 0 {
		t.Fatalf("malformed tool arguments must not execute the tool, calls=%d", readTool.calls)
	}
	msg := toolContractFindToolMessage(t, session, "call_bad_args")
	if !msg.IsError || !strings.Contains(msg.Text, "INVALID:") {
		t.Fatalf("expected invalid synthetic result, got %#v", msg)
	}
	if got := strings.TrimSpace(stringValue(msg.ToolMeta, "tool_contract_result")); got != string(ToolContractSyntheticInvalid) {
		t.Fatalf("expected invalid tool_contract_result, got %#v", msg.ToolMeta)
	}
}

func TestToolContractNormalizesMissingDuplicateAndUnknownToolCalls(t *testing.T) {
	registry := NewToolRegistry(&toolContractRecordingTool{name: "read_file", readOnly: true})
	normalized := NormalizeAssistantToolCalls([]ToolCall{
		{Name: "read_file", Arguments: `{}`},
		{ID: "dup", Name: "read_file", Arguments: `{}`},
		{ID: "dup", Name: "read_file", Arguments: `{}`},
		{ID: "unknown", Name: "missing_tool", Arguments: `{}`},
	}, ToolContractNormalizationOptions{Registry: registry})
	if len(normalized.Calls) != 4 {
		t.Fatalf("expected four normalized calls, got %#v", normalized)
	}
	if normalized.Calls[0].ID == "" {
		t.Fatalf("missing call id should be generated, got %#v", normalized.Calls[0])
	}
	if normalized.Calls[1].ID == normalized.Calls[2].ID {
		t.Fatalf("duplicate call ids should be made unique, got %#v", normalized.Calls)
	}
	if len(normalized.Issues) < 2 {
		t.Fatalf("expected missing-id and duplicate-id issues, got %#v", normalized.Issues)
	}
	var unsupported bool
	for _, item := range normalized.SyntheticResults {
		if item.Call.ID == "unknown" && item.Kind == ToolContractSyntheticUnsupported {
			unsupported = true
		}
	}
	if !unsupported {
		t.Fatalf("unknown tool should produce unsupported synthetic result, got %#v", normalized.SyntheticResults)
	}
}

func TestToolContractReadOnlyBlocksMutatingTool(t *testing.T) {
	root := t.TempDir()
	patchTool := &toolContractRecordingTool{name: "apply_patch"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
				ID:        "call_patch",
				Name:      "apply_patch",
				Arguments: `{"patch":"*** Begin Patch\n*** Add File: main.go\n+package main\n*** End Patch"}`,
			}}}},
			{Message: Message{Role: "assistant", Text: "수정하지 않았습니다."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: "main.go를 분석만 해. 파일은 수정하지 마"}}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Client:    provider,
		Tools:     NewToolRegistry(patchTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	if _, err := agent.completeLoop(context.Background(), true, false, false); err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if patchTool.calls != 0 {
		t.Fatalf("read-only boundary must block mutating tool execution, calls=%d", patchTool.calls)
	}
	msg := toolContractFindToolMessage(t, session, "call_patch")
	if !msg.IsError || !strings.Contains(msg.Text, "read-only analysis") {
		t.Fatalf("expected read-only blocked synthetic result, got %#v", msg)
	}
	if got := strings.TrimSpace(stringValue(msg.ToolMeta, "tool_contract_result")); got != string(ToolContractSyntheticBlocked) {
		t.Fatalf("expected blocked tool_contract_result, got %#v", msg.ToolMeta)
	}
}

func TestToolContractValidateToolCallsAgainstEnvelopeBoundaries(t *testing.T) {
	registry := NewToolRegistry(
		&toolContractRecordingTool{name: "apply_patch"},
		&toolContractRecordingTool{name: "read_file", readOnly: true},
		&toolContractRecordingTool{name: "git_commit"},
		&toolContractRecordingTool{name: "web_search", readOnly: true},
	)
	readOnly := buildRequestEnvelope("main.go를 분석만 해. 파일은 수정하지 마")
	readOnlyResults := ValidateToolCallsAgainstEnvelope([]ToolCall{{
		ID:        "call_patch",
		Name:      "apply_patch",
		Arguments: `{}`,
	}}, readOnly, ToolContractValidationOptions{Registry: registry})
	if len(readOnlyResults) != 1 || readOnlyResults[0].Kind != ToolContractSyntheticBlocked {
		t.Fatalf("read-only boundary should block mutating tool, got %#v", readOnlyResults)
	}

	noGit := buildRequestEnvelope("작업 끝내줘")
	gitResults := ValidateToolCallsAgainstEnvelope([]ToolCall{{
		ID:        "call_git",
		Name:      "git_commit",
		Arguments: `{}`,
	}}, noGit, ToolContractValidationOptions{Registry: registry})
	if len(gitResults) != 1 || !strings.Contains(gitResults[0].Reason, "git write actions require an explicit user request") {
		t.Fatalf("no-git boundary should block git mutation, got %#v", gitResults)
	}

	noWeb := buildRequestEnvelope("main.go 코드 구조를 설명해줘")
	webResults := ValidateToolCallsAgainstEnvelope([]ToolCall{{
		ID:        "call_web",
		Name:      "web_search",
		Arguments: `{}`,
	}}, noWeb, ToolContractValidationOptions{Registry: registry})
	if len(webResults) != 1 || webResults[0].Kind != ToolContractSyntheticBlocked {
		t.Fatalf("no-web boundary should block web research tool, got %#v", webResults)
	}

	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: "분석 보고서를 docs/report.md로 작성해줘"}}
	document := buildRequestEnvelope("분석 보고서를 docs/report.md로 작성해줘")
	documentResults := ValidateToolCallsAgainstEnvelope([]ToolCall{{
		ID:        "call_read_doc",
		Name:      "read_file",
		Arguments: `{"path":"docs/report.md"}`,
	}}, document, ToolContractValidationOptions{Registry: registry, Session: session})
	if len(documentResults) != 1 || !strings.Contains(documentResults[0].Reason, "document read was deferred") {
		t.Fatalf("document boundary should block read-before-create loop, got %#v", documentResults)
	}
}

func TestToolContractBuildSyntheticToolResultKinds(t *testing.T) {
	call := ToolCall{ID: "call", Name: "run_shell", Arguments: `{"command":"echo hi"}`}
	for _, kind := range []ToolContractSyntheticKind{
		ToolContractSyntheticBlocked,
		ToolContractSyntheticSkipped,
		ToolContractSyntheticAborted,
		ToolContractSyntheticInvalid,
		ToolContractSyntheticIncomplete,
		ToolContractSyntheticUnsupported,
	} {
		result := BuildSyntheticToolResult(call, kind, "")
		if strings.TrimSpace(result.DisplayText) == "" {
			t.Fatalf("synthetic result %s should include display text", kind)
		}
		if got := strings.TrimSpace(stringValue(result.Meta, "tool_contract_result")); got != string(kind) {
			t.Fatalf("synthetic result %s should record kind, got %#v", kind, result.Meta)
		}
		if toolMetaBool(result.Meta, "success") {
			t.Fatalf("synthetic result %s should not report success, got %#v", kind, result.Meta)
		}
	}
}

func TestToolContractPartialToolResultsCompleteAllCallIDs(t *testing.T) {
	messages := ValidateConversationToolPairs([]Message{
		{Role: "user", Text: "inspect"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_list", Name: "list_files", Arguments: `{}`},
			{ID: "call_read", Name: "read_file", Arguments: `{"path":"main.go"}`},
		}},
		{Role: "tool", ToolCallID: "call_list", ToolName: "list_files", Text: "main.go"},
		{Role: "user", Text: "continue"},
	})
	first := toolContractFindToolMessageInMessages(t, messages, "call_list")
	second := toolContractFindToolMessageInMessages(t, messages, "call_read")
	if first.Text != "main.go" {
		t.Fatalf("expected existing first tool result to remain, got %#v", first)
	}
	if second.Text != "aborted" {
		t.Fatalf("expected missing second tool result to be synthesized as aborted, got %#v", second)
	}
}

func TestToolContractDropsOrphanToolResultBeforeProviderRequest(t *testing.T) {
	client := &toolContractCaptureClient{}
	_, err := completeModelTurnOnceWithModelRoutes(context.Background(), NewModelRouteScheduler(), modelRoutePolicyFromConfig(Config{}), Config{}, client, ChatRequest{
		Model: "model",
		Messages: []Message{
			{Role: "user", Text: "inspect"},
			{Role: "tool", ToolCallID: "call_orphan", ToolName: "write_file", Text: "wrote report.md"},
			{Role: "user", Text: "continue"},
		},
	})
	if err != nil {
		t.Fatalf("completeModelTurnOnceWithModelRoutes: %v", err)
	}
	for _, msg := range client.req.Messages {
		if msg.Role == "tool" {
			t.Fatalf("orphan tool result must not reach provider request, got %#v", client.req.Messages)
		}
	}
}

func TestToolContractIncompleteStopDoesNotExecutePartialToolCall(t *testing.T) {
	root := t.TempDir()
	readTool := &toolContractRecordingTool{name: "read_file", readOnly: true}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
					ID:        "call_partial",
					Name:      "read_file",
					Arguments: `{"path":"main.go"}`,
				}}},
				StopReason: "length",
			},
			{Message: Message{Role: "assistant", Text: "partial call skipped"}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: "main.go를 읽어줘"}}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Client:    provider,
		Tools:     NewToolRegistry(readTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	if _, err := agent.completeLoop(context.Background(), false, false, false); err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if readTool.calls != 0 {
		t.Fatalf("partial length-stop tool call must not execute, calls=%d", readTool.calls)
	}
	msg := toolContractFindToolMessage(t, session, "call_partial")
	if !msg.IsError || !strings.Contains(msg.Text, "INCOMPLETE:") {
		t.Fatalf("expected incomplete synthetic result, got %#v", msg)
	}
}

func TestToolContractOpenAICompatibleReplayKeepsToolResultOrdering(t *testing.T) {
	messages := ValidateConversationToolPairs([]Message{
		{Role: "user", Text: "inspect"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_list", Name: "list_files", Arguments: `{}`},
			{ID: "call_read", Name: "read_file", Arguments: `{"path":"main.go"}`},
		}},
		{Role: "tool", ToolCallID: "call_read", ToolName: "read_file", Text: "package main"},
		{Role: "user", Text: "continue"},
	})
	assistantIndex := -1
	for i, msg := range messages {
		if msg.Role == "assistant" {
			assistantIndex = i
			break
		}
	}
	if assistantIndex < 0 || assistantIndex+2 >= len(messages) {
		t.Fatalf("expected assistant followed by two tool results, got %#v", messages)
	}
	if messages[assistantIndex+1].ToolCallID != "call_list" || messages[assistantIndex+1].Text != "aborted" {
		t.Fatalf("expected missing first result to be synthesized in call order, got %#v", messages)
	}
	if messages[assistantIndex+2].ToolCallID != "call_read" || messages[assistantIndex+2].Text != "package main" {
		t.Fatalf("expected existing second result to remain in call order, got %#v", messages)
	}
}

func TestToolContractNoGitEnvelopeBlocksGitMutation(t *testing.T) {
	root := t.TempDir()
	gitTool := &toolContractRecordingTool{name: "git_commit"}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
				ID:        "call_git",
				Name:      "git_commit",
				Arguments: `{"message":"unexpected"}`,
			}}}},
			{Message: Message{Role: "assistant", Text: "커밋하지 않았습니다."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: "작업 끝내줘"}}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Client:    provider,
		Tools:     NewToolRegistry(gitTool),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	if _, err := agent.completeLoop(context.Background(), false, false, false); err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if gitTool.calls != 0 {
		t.Fatalf("no-git envelope must block git mutation execution, calls=%d", gitTool.calls)
	}
	msg := toolContractFindToolMessage(t, session, "call_git")
	if !msg.IsError || !strings.Contains(msg.Text, "git write actions require an explicit user request") {
		t.Fatalf("expected no-git blocked synthetic result, got %#v", msg)
	}
}

func toolContractFindToolMessage(t *testing.T, session *Session, callID string) Message {
	t.Helper()
	return toolContractFindToolMessageInMessages(t, session.Messages, callID)
}

func toolContractFindToolMessageInMessages(t *testing.T, messages []Message, callID string) Message {
	t.Helper()
	for _, msg := range messages {
		if msg.Role == "tool" && msg.ToolCallID == callID {
			return msg
		}
	}
	t.Fatalf("tool message %q not found in %#v", callID, messages)
	return Message{}
}
