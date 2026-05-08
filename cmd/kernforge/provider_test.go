package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAIClientUsesVersionedBaseURLWithoutDuplicatingV1(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL+"/api/v1", "test-key")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestOpenAIClientOmitsToolChoiceWithoutTools(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "openrouter:z-ai/glm-5v-turbo",
		Messages: []Message{
			{
				Role: "user",
				Text: "hello",
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, ok := body["tool_choice"]; ok {
		t.Fatalf("tool_choice should be omitted when no tools are provided")
	}
}

func TestLocalOpenAICompatibleClientOmitsAuthorizationWithoutAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("authorization"); got != "" {
			t.Fatalf("authorization header should be omitted, got %q", got)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client, err := NewProviderClient(Config{
		Provider: "lmstudio",
		Model:    "local-model",
		BaseURL:  server.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	if client.Name() != "lmstudio" {
		t.Fatalf("expected lmstudio client name, got %q", client.Name())
	}
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "local-model",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text != "ok" {
		t.Fatalf("expected ok response, got %#v", resp)
	}
}

func TestOpenAIClientRetriesJSONModeWithoutResponseFormatWhenUnsupported(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		defer r.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if attempts == 1 {
			if _, ok := body["response_format"]; !ok {
				t.Fatalf("expected first request to include response_format")
			}
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unsupported parameter: response_format","type":"invalid_request_error","param":"response_format"}}`))
			return
		}
		if _, ok := body["response_format"]; ok {
			t.Fatalf("expected fallback request to omit response_format")
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "local-model",
		Messages: []Message{{
			Role: "user",
			Text: "return json",
		}},
		JSONMode: true,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected two attempts, got %d", attempts)
	}
	if !strings.Contains(resp.Message.Text, `"ok":true`) {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestProviderErrorSuggestsJSONModeUnsupportedIgnoresRequestSummaryOnly(t *testing.T) {
	err := &ProviderAPIError{
		Provider:       "openai",
		StatusCode:     http.StatusBadRequest,
		Message:        "model not found",
		RequestSummary: `{"response_format":{"type":"json_object"}}`,
	}
	if providerErrorSuggestsJSONModeUnsupported(err) {
		t.Fatalf("expected request summary alone not to trigger JSON-mode fallback")
	}
}

func TestProviderErrorSuggestsJSONModeUnsupportedFromStructuredParam(t *testing.T) {
	err := &ProviderAPIError{
		Provider:   "openai",
		StatusCode: http.StatusBadRequest,
		Message:    "unsupported parameter",
		Param:      "response_format",
	}
	if !providerErrorSuggestsJSONModeUnsupported(err) {
		t.Fatalf("expected response_format param to trigger JSON-mode fallback")
	}
}

func TestDeepSeekClientUsesRootChatCompletionsEndpoint(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("authorization"); got != "Bearer deepseek-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client, err := NewProviderClient(Config{
		Provider:        "deepseek-api",
		Model:           "deepseek-v4-pro",
		BaseURL:         server.URL,
		APIKey:          "deepseek-key",
		ReasoningEffort: "xhigh",
	})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	if got := client.Name(); got != "deepseek" {
		t.Fatalf("expected deepseek client, got %q", got)
	}
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "deepseek-v4-pro",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text != "ok" {
		t.Fatalf("expected ok response, got %#v", resp)
	}
	if body["reasoning_effort"] != "max" {
		t.Fatalf("expected DeepSeek reasoning_effort=max, got %#v", body["reasoning_effort"])
	}
	if thinking, ok := body["thinking"].(map[string]any); !ok || thinking["type"] != "enabled" {
		t.Fatalf("expected DeepSeek thinking enabled, got %#v", body["thinking"])
	}
}

func TestDeepSeekClientPreservesVersionedBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client, err := NewProviderClient(Config{
		Provider: "deepseek",
		Model:    "deepseek-v4-flash",
		BaseURL:  server.URL + "/v1",
		APIKey:   "deepseek-key",
	})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	if _, err := client.Complete(context.Background(), ChatRequest{
		Model: "deepseek-v4-flash",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestDeepSeekClientPreservesReasoningContentForToolLoop(t *testing.T) {
	requests := 0
	var secondBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		defer r.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","reasoning_content":"need the file first","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"main.go\"}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		secondBody = body
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient(server.URL, "deepseek-key", "high")
	first, err := client.Complete(context.Background(), ChatRequest{
		Model: "deepseek-v4-pro",
		Messages: []Message{{
			Role: "user",
			Text: "inspect",
		}},
		Tools: []ToolDefinition{{Name: "read_file"}},
	})
	if err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if first.Message.ReasoningContent != "need the file first" {
		t.Fatalf("expected reasoning_content to be preserved, got %q", first.Message.ReasoningContent)
	}
	_, err = client.Complete(context.Background(), ChatRequest{
		Model: "deepseek-v4-pro",
		Messages: []Message{
			{Role: "user", Text: "inspect"},
			first.Message,
			{Role: "tool", ToolCallID: "call_1", ToolName: "read_file", Text: "package main"},
		},
		Tools: []ToolDefinition{{Name: "read_file"}},
	})
	if err != nil {
		t.Fatalf("second Complete: %v", err)
	}
	messages, ok := secondBody["messages"].([]any)
	if !ok || len(messages) < 2 {
		t.Fatalf("expected second request messages, got %#v", secondBody["messages"])
	}
	assistant, ok := messages[1].(map[string]any)
	if !ok {
		t.Fatalf("expected assistant message map, got %#v", messages[1])
	}
	if assistant["reasoning_content"] != "need the file first" {
		t.Fatalf("expected reasoning_content in follow-up assistant message, got %#v", assistant)
	}
	if content, ok := assistant["content"].(string); !ok || content != "" {
		t.Fatalf("expected DeepSeek assistant tool-call content to be explicit empty string, got %#v", assistant["content"])
	}
}

func TestOpenAICompatibleClientSynthesizesMissingToolResponses(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient(server.URL, "deepseek-key", "high")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "deepseek-v4-pro",
		Messages: []Message{
			{Role: "user", Text: "inspect"},
			{Role: "assistant", ToolCalls: []ToolCall{
				{ID: "call_list", Name: "list_files", Arguments: `{"path":"."}`},
				{ID: "call_read", Name: "read_file", Arguments: `{"path":"goal.json"}`},
			}},
			{Role: "tool", ToolCallID: "call_list", ToolName: "list_files", Text: "goal.json"},
			{Role: "user", Text: "continue"},
		},
		Tools: []ToolDefinition{{Name: "list_files"}, {Name: "read_file"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	messages, ok := captured["messages"].([]any)
	if !ok {
		t.Fatalf("expected messages array, got %#v", captured["messages"])
	}
	if len(messages) < 5 {
		t.Fatalf("expected synthesized tool message before follow-up user, got %#v", messages)
	}
	tool, ok := messages[3].(map[string]any)
	if !ok {
		t.Fatalf("expected synthesized tool message, got %#v", messages[3])
	}
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_read" {
		t.Fatalf("unexpected synthesized tool message: %#v", tool)
	}
	if !strings.Contains(fmt.Sprint(tool["content"]), "tool result was missing") {
		t.Fatalf("expected synthetic missing-result content, got %#v", tool["content"])
	}
	user, ok := messages[4].(map[string]any)
	if !ok || user["role"] != "user" {
		t.Fatalf("expected follow-up user after synthesized tool message, got %#v", messages[4])
	}
}

func TestOpenAICompatibleClientConvertsOrphanToolMessagesToUserContext(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient(server.URL, "deepseek-key", "high")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "deepseek-v4-pro",
		Messages: []Message{
			{Role: "user", Text: "inspect"},
			{Role: "tool", ToolCallID: "call_orphan", ToolName: "write_file", Text: "wrote TavernKernel/ANTICHEAT_GAP_ANALYSIS.md"},
			{Role: "user", Text: "continue"},
		},
		Tools: []ToolDefinition{{Name: "write_file"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	messages, ok := captured["messages"].([]any)
	if !ok {
		t.Fatalf("expected messages array, got %#v", captured["messages"])
	}
	for _, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("expected message map, got %#v", item)
		}
		if message["role"] == "tool" {
			t.Fatalf("expected orphan tool message to be converted, got %#v", messages)
		}
	}
	recovered, ok := messages[1].(map[string]any)
	if !ok || recovered["role"] != "user" {
		t.Fatalf("expected recovered orphan tool context as user message, got %#v", messages[1])
	}
	if !strings.Contains(fmt.Sprint(recovered["content"]), "saved tool result appeared without a matching preceding assistant tool_call") {
		t.Fatalf("expected recovered transcript note, got %#v", recovered["content"])
	}
}

func TestOpenAICompatibleClientConvertsUnexpectedToolResponseAfterAssistant(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient(server.URL, "deepseek-key", "high")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "deepseek-v4-pro",
		Messages: []Message{
			{Role: "user", Text: "inspect"},
			{Role: "assistant", ToolCalls: []ToolCall{
				{ID: "call_expected", Name: "read_file", Arguments: `{"path":"main.go"}`},
			}},
			{Role: "tool", ToolCallID: "call_other", ToolName: "write_file", Text: "wrote other.md"},
		},
		Tools: []ToolDefinition{{Name: "read_file"}, {Name: "write_file"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	messages, ok := captured["messages"].([]any)
	if !ok {
		t.Fatalf("expected messages array, got %#v", captured["messages"])
	}
	if len(messages) < 4 {
		t.Fatalf("expected assistant, recovered unexpected tool, and synthesized missing tool, got %#v", messages)
	}
	synthetic, ok := messages[2].(map[string]any)
	if !ok || synthetic["role"] != "tool" || synthetic["tool_call_id"] != "call_expected" {
		t.Fatalf("expected missing expected tool response to be synthesized before other messages, got %#v", messages[2])
	}
	recovered, ok := messages[3].(map[string]any)
	if !ok || recovered["role"] != "user" {
		t.Fatalf("expected unexpected tool response to be converted to user context after required tool responses, got %#v", messages[3])
	}
}

func TestDeepSeekClientPreservesStreamedReasoningContentForToolLoop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"need \"},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"context\"},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"main.go\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewDeepSeekClient(server.URL, "deepseek-key", "high")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "deepseek-v4-pro",
		Messages: []Message{{
			Role: "user",
			Text: "inspect",
		}},
		Tools:           []ToolDefinition{{Name: "read_file"}},
		OnProgressEvent: func(ProgressEvent) {},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", resp.Message.ToolCalls)
	}
	if resp.Message.ReasoningContent != "need context" {
		t.Fatalf("expected streamed reasoning_content, got %q", resp.Message.ReasoningContent)
	}
}

func TestOpenAICompatibleClientsReportProviderName(t *testing.T) {
	cases := []struct {
		provider string
		want     string
	}{
		{provider: "openrouter", want: "openrouter"},
		{provider: "deepseek", want: "deepseek"},
		{provider: "openai-compatible", want: "openai-compatible"},
	}
	for _, tc := range cases {
		client, err := NewProviderClient(Config{
			Provider: tc.provider,
			Model:    "test-model",
			APIKey:   "test-key",
		})
		if err != nil {
			t.Fatalf("NewProviderClient(%q): %v", tc.provider, err)
		}
		if got := client.Name(); got != tc.want {
			t.Fatalf("NewProviderClient(%q).Name() = %q, want %q", tc.provider, got, tc.want)
		}
	}
}

func TestOpenAIClientSetsToolChoiceWhenToolsExist(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{
			{
				Role: "user",
				Text: "hello",
			},
		},
		Tools: []ToolDefinition{
			{
				Name:        "read_file",
				Description: "Read a file",
				InputSchema: map[string]any{
					"type": "object",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	value, ok := body["tool_choice"].(string)
	if !ok {
		t.Fatalf("tool_choice should be present when tools are provided")
	}
	if value != "auto" {
		t.Fatalf("unexpected tool_choice: %v", value)
	}
}

func TestOpenAIClientIncludesStructuredErrorDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"Provider returned error","type":"server_error","param":"model","code":"backend_failure"}}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	text := err.Error()
	for _, part := range []string{"Provider returned error", "type=server_error", "param=model", "code=backend_failure"} {
		if !strings.Contains(text, part) {
			t.Fatalf("expected %q in error, got %q", part, text)
		}
	}
}

func TestOpenAIClientReturnsStructuredProviderAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"Provider returned error","type":"server_error","code":"backend_failure"}}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	var providerErr *ProviderAPIError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderAPIError, got %T", err)
	}
	if providerErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected HTTP 503, got %d", providerErr.StatusCode)
	}
	if !providerErr.Retryable() {
		t.Fatalf("expected HTTP 503 provider error to be retryable")
	}
}

func TestShouldRetryProviderErrorUsesStructuredProviderAPIError(t *testing.T) {
	if !shouldRetryProviderError(&ProviderAPIError{
		Provider:   "openai",
		StatusCode: http.StatusBadGateway,
		Status:     "502 Bad Gateway",
		Message:    "upstream overloaded",
	}) {
		t.Fatalf("expected structured 502 provider error to be retryable")
	}
	if shouldRetryProviderError(&ProviderAPIError{
		Provider:   "openai",
		StatusCode: http.StatusUnauthorized,
		Status:     "401 Unauthorized",
		Message:    "invalid api key",
	}) {
		t.Fatalf("expected structured 401 provider error to remain non-retryable")
	}
}

func TestOpenAIClientNormalizesAssistantToolCallArguments(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{
			{
				Role: "assistant",
				ToolCalls: []ToolCall{
					{
						ID:        "call_1",
						Name:      "read_file",
						Arguments: "path=C:\\\\temp\\\\note.txt",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	messages, ok := body["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("messages missing from request body: %#v", body["messages"])
	}
	assistant, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("assistant message has unexpected type: %#v", messages[0])
	}
	if _, ok := assistant["content"]; ok {
		t.Fatalf("assistant content should be omitted when only tool calls are present")
	}
	toolCalls, ok := assistant["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("tool_calls missing from assistant message: %#v", assistant["tool_calls"])
	}
	call, ok := toolCalls[0].(map[string]any)
	if !ok {
		t.Fatalf("tool call has unexpected type: %#v", toolCalls[0])
	}
	function, ok := call["function"].(map[string]any)
	if !ok {
		t.Fatalf("function payload has unexpected type: %#v", call["function"])
	}
	args, ok := function["arguments"].(string)
	if !ok {
		t.Fatalf("arguments should be encoded as a string: %#v", function["arguments"])
	}
	expected := `{"raw":"path=C:\\\\temp\\\\note.txt"}`
	if args != expected {
		t.Fatalf("unexpected normalized arguments: got %q want %q", args, expected)
	}
}

func TestOpenAIClientIncludesRequestPreviewOnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"Provider returned error","code":"400"}}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{
			{
				Role: "user",
				Text: "hello",
			},
		},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), `request={"model":"openai/gpt-4.1"`) {
		t.Fatalf("expected request preview in error, got %q", err.Error())
	}
}

func TestOpenAIClientDoesNotRetryStreamingHTTPErrorAsFallback(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusUnauthorized)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"bad token","type":"authentication_error"}}`))
	}))
	defer server.Close()

	client := NewOpenAICompatibleClient("openai-compatible", server.URL, "bad-key")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "test-model",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
		OnTextDelta: func(string) {},
	})
	if err == nil {
		t.Fatalf("expected streaming HTTP error")
	}
	var providerErr *ProviderAPIError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderAPIError, got %T", err)
	}
	if providerErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected HTTP 401, got %d", providerErr.StatusCode)
	}
	if providerErr.Provider != "openai-compatible" {
		t.Fatalf("expected provider name openai-compatible, got %q", providerErr.Provider)
	}
	if requests != 1 {
		t.Fatalf("expected streaming HTTP error to be returned without fallback retry, got %d requests", requests)
	}
}

func TestOpenAIClientStreamsTextDeltas(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hel\"},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	var deltas []string
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
		OnTextDelta: func(text string) {
			deltas = append(deltas, text)
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if body["stream"] != true {
		t.Fatalf("expected stream=true in request body, got %#v", body["stream"])
	}
	if strings.Join(deltas, "") != "hello" {
		t.Fatalf("unexpected deltas: %#v", deltas)
	}
	if resp.Message.Text != "hello" {
		t.Fatalf("unexpected streamed text: %q", resp.Message.Text)
	}
	if resp.StopReason != "stop" {
		t.Fatalf("unexpected stop reason: %q", resp.StopReason)
	}
}

func TestOpenAIClientStreamsToolCallArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\"\"}}]},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":\\\"main.go\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	var events []ProgressEvent
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "inspect",
		}},
		OnTextDelta: func(text string) {},
		OnProgressEvent: func(event ProgressEvent) {
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one streamed tool call, got %#v", resp.Message.ToolCalls)
	}
	call := resp.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "read_file" {
		t.Fatalf("unexpected tool call identity: %#v", call)
	}
	if call.Arguments != "{\"path\":\"main.go\"}" {
		t.Fatalf("unexpected tool call arguments: %q", call.Arguments)
	}
	if !progressEventsContain(events, progressKindModelStreamToolCall, "read_file") {
		t.Fatalf("expected streamed tool-call progress event, got %#v", events)
	}
	if !progressEventsContain(events, progressKindModelStreamToolReady, "read_file") {
		t.Fatalf("expected streamed tool-ready progress event, got %#v", events)
	}
	if resp.StopReason != "tool_calls" {
		t.Fatalf("unexpected stop reason: %q", resp.StopReason)
	}
}

func progressEventsContain(events []ProgressEvent, kind string, toolName string) bool {
	for _, event := range events {
		if strings.TrimSpace(event.Kind) == kind && strings.TrimSpace(event.ToolName) == toolName {
			return true
		}
	}
	return false
}

func TestOpenAIClientSuppressesBufferedToolPreambleTextWhenToolCallsAppear(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Let me inspect the file first.\"},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"main.go\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	var deltas []string
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "inspect",
		}},
		Tools: []ToolDefinition{{
			Name: "read_file",
		}},
		OnTextDelta: func(text string) {
			deltas = append(deltas, text)
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(deltas) != 0 {
		t.Fatalf("expected buffered tool preamble deltas to stay hidden, got %#v", deltas)
	}
	if resp.Message.Text != "Let me inspect the file first." {
		t.Fatalf("unexpected response text: %q", resp.Message.Text)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one streamed tool call, got %#v", resp.Message.ToolCalls)
	}
}

func TestOpenAIClientFlushesBufferedShortTextAtEndWhenNoToolCallArrives(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"short final answer\"},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	var deltas []string
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "inspect",
		}},
		Tools: []ToolDefinition{{
			Name: "read_file",
		}},
		OnTextDelta: func(text string) {
			deltas = append(deltas, text)
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if strings.Join(deltas, "") != "short final answer" {
		t.Fatalf("expected buffered text to flush at end, got %#v", deltas)
	}
	if resp.Message.Text != "short final answer" {
		t.Fatalf("unexpected response text: %q", resp.Message.Text)
	}
}

func TestOpenAIClientStreamHonorsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		_, err := client.Complete(ctx, ChatRequest{
			Model: "openai/gpt-4.1",
			Messages: []Message{{
				Role: "user",
				Text: "hello",
			}},
			OnTextDelta: func(string) {},
		})
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("stream request did not stop after context cancellation")
	}
}

func TestOpenAIClientReturnsPartialTextOnStreamDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial answer\"},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	resp, err := client.Complete(ctx, ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
		OnTextDelta: func(string) {},
	})
	if err != nil {
		t.Fatalf("expected partial response instead of timeout, got %v", err)
	}
	if resp.Message.Text != "partial answer" {
		t.Fatalf("unexpected partial text: %q", resp.Message.Text)
	}
	if resp.StopReason != "partial" {
		t.Fatalf("unexpected stop reason: %q", resp.StopReason)
	}
}

func TestOpenAIClientDoesNotReturnPartialToolCallOnStreamDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\"\"}}]},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.Complete(ctx, ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "inspect",
		}},
		OnTextDelta: func(string) {},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout for partial tool call stream, got %v", err)
	}
}

func TestOpenAIClientFallsBackToNonStreamWhenStreamReturnsEmpty(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		defer r.Body.Close()

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode: %v", err)
		}

		if requests == 1 {
			if body["stream"] != true {
				t.Fatalf("expected first request to stream, got %#v", body["stream"])
			}
			w.Header().Set("content-type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatalf("expected flusher")
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			flusher.Flush()
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}

		if _, ok := body["stream"]; ok {
			t.Fatalf("expected fallback request to omit stream flag, got %#v", body["stream"])
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"fallback answer"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "openrouter:google/gemma-4-31b-it:free",
		Messages: []Message{{
			Role: "user",
			Text: "review this file",
		}},
		OnTextDelta: func(string) {},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected stream + fallback requests, got %d", requests)
	}
	if resp.Message.Text != "fallback answer" {
		t.Fatalf("unexpected fallback text: %q", resp.Message.Text)
	}
	if resp.StopReason != "stop_after_stream_retry" {
		t.Fatalf("unexpected fallback stop reason: %q", resp.StopReason)
	}
}

func TestOpenAIClientProgressOnlyFallbackUsesNonStreamRetry(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		defer r.Body.Close()

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode: %v", err)
		}

		if requests == 1 {
			if body["stream"] != true {
				t.Fatalf("expected progress-only request to stream, got %#v", body["stream"])
			}
			w.Header().Set("content-type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}

		if _, ok := body["stream"]; ok {
			t.Fatalf("expected progress-only fallback request to omit stream flag, got %#v", body["stream"])
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"fallback after progress stream"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "inspect",
		}},
		OnProgressEvent: func(ProgressEvent) {},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected stream + non-stream fallback requests, got %d", requests)
	}
	if resp.Message.Text != "fallback after progress stream" {
		t.Fatalf("unexpected fallback text: %q", resp.Message.Text)
	}
}

func TestOpenAIClientExtractsTextFromContentPartArray(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":[{"type":"output_text","text":"array "},{"type":"text","text":"answer"}]},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "openrouter:openai/gpt-oss-120b",
		Messages: []Message{{
			Role: "user",
			Text: "summarize",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text != "array answer" {
		t.Fatalf("expected array content text, got %q", resp.Message.Text)
	}
}

func TestOpenAIClientDeduplicatesEquivalentTypedContentPartsInStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":[{\"type\":\"output_text\",\"text\":\"좋\"},{\"type\":\"text\",\"text\":\"좋\"}]},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":[{\"type\":\"output_text_delta\",\"text\":\"습\"},{\"type\":\"text\",\"text\":\"습\"}]},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":[{\"type\":\"output_text\",\"text\":\"니다.\"},{\"type\":\"text\",\"text\":\"니다.\"}]},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	var deltas []string
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "say hello",
		}},
		OnTextDelta: func(text string) {
			deltas = append(deltas, text)
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if strings.Join(deltas, "") != "좋습니다." {
		t.Fatalf("unexpected streamed deltas: %#v", deltas)
	}
	if resp.Message.Text != "좋습니다." {
		t.Fatalf("unexpected streamed text: %q", resp.Message.Text)
	}
}

func TestOpenAIClientPreservesRepeatedContentPartsWhenTypesMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":[{"type":"text","text":"ha"},{"type":"text","text":"ha"}]},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "repeat",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text != "haha" {
		t.Fatalf("expected repeated same-type parts to be preserved, got %q", resp.Message.Text)
	}
}

func TestOpenAIClientMarksEmptyFallbackAfterEmptyStream(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		defer r.Body.Close()

		if requests == 1 {
			w.Header().Set("content-type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatalf("expected flusher")
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			flusher.Flush()
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}

		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":""},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "openrouter:openai/gpt-oss-120b",
		Messages: []Message{{
			Role: "user",
			Text: "review this file",
		}},
		OnTextDelta: func(string) {},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected stream + fallback requests, got %d", requests)
	}
	if resp.StopReason != "stream_empty_fallback_empty_after_stream_retry" {
		t.Fatalf("unexpected fallback stop reason: %q", resp.StopReason)
	}
	if resp.Message.Text != "" {
		t.Fatalf("expected empty fallback text, got %q", resp.Message.Text)
	}
}

func TestOpenAIClientFallsBackToNonStreamWhenStreamEndsWithoutDoneOrFinishReason(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		defer r.Body.Close()

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode: %v", err)
		}

		if requests == 1 {
			w.Header().Set("content-type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatalf("expected flusher")
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial intro\"},\"finish_reason\":\"\"}]}\n\n")
			flusher.Flush()
			return
		}

		if _, ok := body["stream"]; ok {
			t.Fatalf("expected fallback request to omit stream flag, got %#v", body["stream"])
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"full fallback answer"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "openrouter:google/gemini-2.5-pro",
		Messages: []Message{{
			Role: "user",
			Text: "review and fix this file",
		}},
		OnTextDelta: func(string) {},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected stream + fallback requests, got %d", requests)
	}
	if resp.Message.Text != "full fallback answer" {
		t.Fatalf("unexpected fallback text: %q", resp.Message.Text)
	}
	if resp.StopReason != "stop_after_stream_retry" {
		t.Fatalf("unexpected fallback stop reason: %q", resp.StopReason)
	}
}
