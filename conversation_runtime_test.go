package main

import (
	"context"
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
		Summary:  "provider error | provider=openrouter | upstream=DeepInfra | model=deepseek/deepseek-v4-flash | shard=TavernKernel/TavernKernel/BuildCab_refined_03 | code=429 | category=rate_limit",
		Raw:      `openai API error (429 Too Many Requests): Provider returned error | raw={"error":{"message":"deepseek/deepseek-v4-flash is temporarily rate-limited upstream","metadata":{"provider_name":"DeepInfra","is_byok":false}}}`,
		Entities: map[string]string{
			"provider":  "openrouter",
			"upstream":  "DeepInfra",
			"model":     "deepseek/deepseek-v4-flash",
			"shard":     "TavernKernel/TavernKernel/BuildCab_refined_03",
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
	for _, want := range []string{"rate limit", "DeepInfra", "deepseek/deepseek-v4-flash", "TavernKernel/TavernKernel/BuildCab_refined_03", "429"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("expected reply to contain %q, got %q", want, reply)
		}
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
