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

func TestToolOutputForResponsesPreservesEncryptedContentItemLikeCodex(t *testing.T) {
	output := toolOutputForResponses(Message{
		Text: "fallback",
		ToolContentItems: []ToolContentItem{
			{
				Type: "input_text",
				Text: "visible",
			},
			{
				Type:             "encrypted_content",
				EncryptedContent: "sealed-payload",
			},
		},
	})

	items, ok := output.([]map[string]any)
	if !ok {
		t.Fatalf("expected content item array, got %#v", output)
	}
	if len(items) != 2 {
		t.Fatalf("expected two content items, got %#v", items)
	}
	if items[0]["type"] != "input_text" || items[0]["text"] != "visible" {
		t.Fatalf("unexpected text content item: %#v", items[0])
	}
	if items[1]["type"] != "encrypted_content" || items[1]["encrypted_content"] != "sealed-payload" {
		t.Fatalf("unexpected encrypted content item: %#v", items[1])
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

func TestOpenAIClientMapsImageDetailInChatCompletions(t *testing.T) {
	dir := t.TempDir()
	originalImage := writeTestImage(t, dir, "original.png")
	defaultImage := writeTestImage(t, dir, "default.png")

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
				Text: "inspect",
				Images: []MessageImage{
					{Path: originalImage, MediaType: "image/png", Detail: imageDetailOriginal},
					{Path: defaultImage, MediaType: "image/png"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("unexpected messages payload: %#v", body["messages"])
	}
	message, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected message payload: %#v", messages[0])
	}
	content, ok := message["content"].([]any)
	if !ok || len(content) != 3 {
		t.Fatalf("unexpected content payload: %#v", message["content"])
	}

	originalPayload := openAIChatImagePayloadFromContent(t, content[1])
	if originalPayload["detail"] != imageDetailHigh {
		t.Fatalf("original image detail = %#v, want %q; payload=%#v", originalPayload["detail"], imageDetailHigh, originalPayload)
	}
	if url, _ := originalPayload["url"].(string); !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("original image url should be data URI, got %#v", originalPayload["url"])
	}

	defaultPayload := openAIChatImagePayloadFromContent(t, content[2])
	if defaultPayload["detail"] != imageDetailHigh {
		t.Fatalf("default image detail = %#v, want %q; payload=%#v", defaultPayload["detail"], imageDetailHigh, defaultPayload)
	}
	if url, _ := defaultPayload["url"].(string); !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("default image url should be data URI, got %#v", defaultPayload["url"])
	}
}

func openAIChatImagePayloadFromContent(t *testing.T, part any) map[string]any {
	t.Helper()
	partMap, ok := part.(map[string]any)
	if !ok {
		t.Fatalf("unexpected content part: %#v", part)
	}
	if partMap["type"] != "image_url" {
		t.Fatalf("content part type = %#v, want image_url; part=%#v", partMap["type"], partMap)
	}
	payload, ok := partMap["image_url"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected image_url payload: %#v", partMap["image_url"])
	}
	return payload
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

func TestOpenAIClientReplaysCodexTurnStateWithinRequestState(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		switch requestCount {
		case 1:
			if got := r.Header.Get(codexTurnStateHeader); got != "" {
				t.Fatalf("first request should not send turn state, got %q", got)
			}
			assertOpenAICompatibleTurnMetadataHeader(t, r, "turn-123")
			w.Header().Set(codexTurnStateHeader, "sticky-turn")
		case 2:
			if got := r.Header.Get(codexTurnStateHeader); got != "sticky-turn" {
				t.Fatalf("second request should replay turn state, got %q", got)
			}
			assertOpenAICompatibleTurnMetadataHeader(t, r, "turn-123")
		default:
			t.Fatalf("unexpected request %d", requestCount)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	state := &ProviderTurnState{}
	for i := 0; i < 2; i++ {
		_, err := client.Complete(context.Background(), ChatRequest{
			Model:     "openai/gpt-4.1",
			TurnState: state,
			TurnMetadata: map[string]any{
				"turn_id": "turn-123",
			},
			Messages: []Message{{
				Role: "user",
				Text: "hello",
			}},
		})
		if err != nil {
			t.Fatalf("Complete %d: %v", i+1, err)
		}
	}
	if state.Value() != "sticky-turn" {
		t.Fatalf("expected captured turn state, got %q", state.Value())
	}
}

func assertOpenAICompatibleTurnMetadataHeader(t *testing.T, r *http.Request, wantTurnID string) {
	t.Helper()
	raw := strings.TrimSpace(r.Header.Get(codexTurnMetadataHeader))
	if raw == "" {
		t.Fatalf("missing %s header", codexTurnMetadataHeader)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		t.Fatalf("%s is not valid JSON: %v; raw=%q", codexTurnMetadataHeader, err, raw)
	}
	if got := metadata["turn_id"]; got != wantTurnID {
		t.Fatalf("unexpected turn_id in %s: got %#v want %q; metadata=%#v", codexTurnMetadataHeader, got, wantTurnID, metadata)
	}
}

func TestProviderTurnMetadataHeaderValueEscapesNonASCII(t *testing.T) {
	value := providerTurnMetadataHeaderValue(map[string]any{
		"cwd":     `F:\kernullist\꿀보`,
		"turn_id": "turn-123",
	})
	if strings.TrimSpace(value) == "" {
		t.Fatalf("expected metadata header value")
	}
	for _, r := range value {
		if r > 0x7f {
			t.Fatalf("metadata header should be ASCII-only, got %q", value)
		}
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(value), &metadata); err != nil {
		t.Fatalf("metadata header should remain valid JSON: %v", err)
	}
	if got := metadata["cwd"]; got != `F:\kernullist\꿀보` {
		t.Fatalf("metadata did not round-trip non-ASCII path: %#v", metadata)
	}
}

func TestLocalOpenAICompatibleClientPreservesReasoningContentWhenContentEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","reasoning_content":"REVIEW_RESULT\nverdict: approved\nsummary: ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client, err := NewProviderClient(Config{
		Provider: "lmstudio",
		Model:    "qwen-local",
		BaseURL:  server.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "qwen-local",
		Messages: []Message{{
			Role: "user",
			Text: "review",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text != "" {
		t.Fatalf("expected empty content to stay empty, got %#v", resp.Message)
	}
	if !strings.Contains(resp.Message.ReasoningContent, "REVIEW_RESULT") {
		t.Fatalf("expected reasoning_content to be preserved for local empty content, got %#v", resp.Message)
	}
	if !strings.Contains(resp.RawBody, "reasoning_content") {
		t.Fatalf("expected raw provider body to be retained, got %q", resp.RawBody)
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

func TestProviderHTTPErrorFormatsCodexRateLimitReachedTypes(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		wantDetail string
	}{
		{
			name:       "owner credits",
			header:     "workspace_owner_credits_depleted",
			wantDetail: "Your workspace is out of credits. Add credits to continue.",
		},
		{
			name:       "member credits",
			header:     "workspace_member_credits_depleted",
			wantDetail: "Your workspace is out of credits. Ask your workspace owner to refill in order to continue.",
		},
		{
			name:       "owner usage limit",
			header:     "workspace_owner_usage_limit_reached",
			wantDetail: "You hit your spend cap set in your workspace. Increase your spend cap to continue.",
		},
		{
			name:       "member usage limit",
			header:     "workspace_member_usage_limit_reached",
			wantDetail: "You hit your spend cap set by the owner of your workspace. Ask an owner to increase your spend cap to continue.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			headers.Set(providerRateLimitReachedTypeHeader, tt.header)
			err := newProviderHTTPErrorWithHeaders(
				"openai-codex",
				http.StatusTooManyRequests,
				"429 Too Many Requests",
				[]byte(`{"error":{"message":"usage_limit_reached","type":"usage_limit_exceeded","code":"usage_limit_reached"}}`),
				"",
				headers,
			)
			var providerErr *ProviderAPIError
			if !errors.As(err, &providerErr) {
				t.Fatalf("expected ProviderAPIError, got %T", err)
			}
			if providerErr.RateLimitReachedType != tt.header {
				t.Fatalf("expected header %q, got %q", tt.header, providerErr.RateLimitReachedType)
			}
			text := err.Error()
			for _, part := range []string{tt.wantDetail, "type=usage_limit_exceeded", "code=usage_limit_reached"} {
				if !strings.Contains(text, part) {
					t.Fatalf("expected %q in error, got %q", part, text)
				}
			}
			if !providerErr.Retryable() {
				t.Fatalf("expected 429 provider error to remain retryable")
			}
		})
	}
}

func TestProviderHTTPErrorFallsBackForGenericCodexRateLimitReachedType(t *testing.T) {
	for _, header := range []string{"rate_limit_reached", "future_rate_limit_reached_type"} {
		headers := http.Header{}
		headers.Set(providerRateLimitReachedTypeHeader, header)
		err := newProviderHTTPErrorWithHeaders(
			"openai-codex",
			http.StatusTooManyRequests,
			"429 Too Many Requests",
			[]byte(`{"error":{"message":"usage_limit_reached","type":"usage_limit_exceeded","code":"usage_limit_reached"}}`),
			"",
			headers,
		)
		text := err.Error()
		if !strings.Contains(text, "usage_limit_reached") {
			t.Fatalf("expected body message fallback for %q, got %q", header, text)
		}
		if strings.Contains(text, "workspace") {
			t.Fatalf("expected %q not to use workspace-specific copy, got %q", header, text)
		}
		var providerErr *ProviderAPIError
		if !errors.As(err, &providerErr) {
			t.Fatalf("expected ProviderAPIError, got %T", err)
		}
		if header == "future_rate_limit_reached_type" && providerErr.RateLimitReachedType != "" {
			t.Fatalf("expected unknown header to be ignored, got %q", providerErr.RateLimitReachedType)
		}
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
	if fmt.Sprint(tool["content"]) != "aborted" {
		t.Fatalf("expected Codex-style aborted synthetic missing-result content, got %#v", tool["content"])
	}
	user, ok := messages[4].(map[string]any)
	if !ok || user["role"] != "user" {
		t.Fatalf("expected follow-up user after synthesized tool message, got %#v", messages[4])
	}
}

func TestOpenAICompatibleClientMarksRuntimeSupersededToolResponses(t *testing.T) {
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
				{ID: "call_patch", Name: "apply_patch", Arguments: `{"patch":"..."}`},
			}},
			{Role: "user", Text: "Your last edit targeted stale or mismatched file contents. Read the file again before editing."},
		},
		Tools: []ToolDefinition{{Name: "apply_patch"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	messages, ok := captured["messages"].([]any)
	if !ok {
		t.Fatalf("expected messages array, got %#v", captured["messages"])
	}
	if len(messages) < 4 {
		t.Fatalf("expected synthesized superseded tool message, got %#v", messages)
	}
	tool, ok := messages[2].(map[string]any)
	if !ok {
		t.Fatalf("expected synthesized tool message, got %#v", messages[2])
	}
	content := fmt.Sprint(tool["content"])
	if !strings.Contains(content, "superseded before execution") {
		t.Fatalf("expected superseded tool content, got %#v", tool["content"])
	}
	if strings.Contains(content, "tool result was missing") {
		t.Fatalf("expected no missing-transcript error for runtime superseded tool call, got %#v", tool["content"])
	}
}

func TestOpenAICompatibleClientDropsOrphanToolMessages(t *testing.T) {
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
			{Role: "tool", ToolCallID: "call_orphan", ToolName: "write_file", Text: "wrote SampleKernel/ANTICHEAT_GAP_ANALYSIS.md"},
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
			t.Fatalf("expected orphan tool message to be dropped, got %#v", messages)
		}
		if strings.Contains(fmt.Sprint(message["content"]), "call_orphan") ||
			strings.Contains(fmt.Sprint(message["content"]), "saved tool result appeared without a matching preceding assistant tool_call") {
			t.Fatalf("expected orphan tool context to be dropped, got %#v", messages)
		}
	}
	if len(messages) != 2 {
		t.Fatalf("expected only user messages after dropping orphan tool result, got %#v", messages)
	}
}

func TestOpenAICompatibleClientDropsUnexpectedToolResponseAfterAssistant(t *testing.T) {
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
	if len(messages) != 3 {
		t.Fatalf("expected assistant and synthesized missing tool only, got %#v", messages)
	}
	synthetic, ok := messages[2].(map[string]any)
	if !ok || synthetic["role"] != "tool" || synthetic["tool_call_id"] != "call_expected" {
		t.Fatalf("expected missing expected tool response to be synthesized before other messages, got %#v", messages[2])
	}
	for _, item := range messages {
		if strings.Contains(fmt.Sprint(item), "call_other") || strings.Contains(fmt.Sprint(item), "wrote other.md") {
			t.Fatalf("unexpected orphan tool response should be dropped, got %#v", messages)
		}
	}
}

func TestOpenAICompatibleClientUsesNameFallbackForMissingToolCallID(t *testing.T) {
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
				{Name: "read_file", Arguments: `{"path":"main.go"}`},
			}},
			{Role: "tool", ToolName: "read_file", Text: "package main"},
		},
		Tools: []ToolDefinition{{Name: "read_file"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	messages, ok := captured["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("expected three messages, got %#v", captured["messages"])
	}
	assistant, ok := messages[1].(map[string]any)
	if !ok {
		t.Fatalf("expected assistant message, got %#v", messages[1])
	}
	toolCalls, ok := assistant["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", assistant["tool_calls"])
	}
	toolCall, ok := toolCalls[0].(map[string]any)
	if !ok || toolCall["id"] != "read_file" {
		t.Fatalf("expected tool call id fallback to name, got %#v", toolCalls[0])
	}
	tool, ok := messages[2].(map[string]any)
	if !ok || tool["tool_call_id"] != "read_file" || tool["content"] != "package main" {
		t.Fatalf("expected matching tool output with fallback id, got %#v", messages[2])
	}
	if strings.Contains(fmt.Sprint(messages), "aborted") {
		t.Fatalf("did not expect synthesized aborted output when matching tool output exists: %#v", messages)
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
	if !shouldRetryProviderError(errors.New("stream error: stream ID 27; INTERNAL_ERROR; received from peer")) {
		t.Fatalf("expected transient stream INTERNAL_ERROR to be retryable")
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

func TestOpenAIClientStreamsTextDeltasWithoutHiddenAssistantMarkup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello \"},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"<oai\"},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"-mem-citation>hidden</oai-mem-citation>\"},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"world\"},\"finish_reason\":\"stop\"}]}\n\n")
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
	if strings.Join(deltas, "") != "hello world" {
		t.Fatalf("unexpected visible deltas: %#v", deltas)
	}
	if strings.Contains(strings.Join(deltas, ""), "oai-mem-citation") {
		t.Fatalf("hidden assistant markup leaked through deltas: %#v", deltas)
	}
	if resp.Message.Text != "hello <oai-mem-citation>hidden</oai-mem-citation>world" {
		t.Fatalf("unexpected raw streamed text: %q", resp.Message.Text)
	}
}

func TestOpenAIClientPreservesIncompleteHiddenTagPrefixAtStreamEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello <oai-mem-\"},\"finish_reason\":\"stop\"}]}\n\n")
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
	if strings.Join(deltas, "") != "hello <oai-mem-" {
		t.Fatalf("expected incomplete hidden tag prefix to remain visible, got %#v", deltas)
	}
	if resp.Message.Text != "hello <oai-mem-" {
		t.Fatalf("unexpected raw streamed text: %q", resp.Message.Text)
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

func TestOpenAIClientSuppressesLongBufferedToolPreambleTextWhenToolCallsAppear(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"I understand the system is deferring local tools, but this task is a local code review, so no external research is needed. Let me proceed with local inspection.\"},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"list_files\",\"arguments\":\"{\\\"path\\\":\\\".\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	var deltas []string
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "lm-studio/qwen",
		Messages: []Message{{
			Role: "user",
			Text: "각 파일들을 분석해서 문제점을 찾아서 별도 문서로 생성해",
		}},
		Tools: []ToolDefinition{{
			Name: "list_files",
		}},
		OnTextDelta: func(text string) {
			deltas = append(deltas, text)
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(deltas) != 0 {
		t.Fatalf("expected long buffered tool preamble deltas to stay hidden, got %#v", deltas)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "list_files" {
		t.Fatalf("expected streamed list_files tool call, got %#v", resp.Message.ToolCalls)
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

func TestOpenAIClientFlushesBufferedShortTextWithoutHiddenAssistantMarkup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"short <proposed_plan>hidden</proposed_plan>final\"},\"finish_reason\":\"stop\"}]}\n\n")
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
	if strings.Join(deltas, "") != "short final" {
		t.Fatalf("expected buffered hidden markup to stay hidden, got %#v", deltas)
	}
	if resp.Message.Text != "short <proposed_plan>hidden</proposed_plan>final" {
		t.Fatalf("unexpected raw response text: %q", resp.Message.Text)
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
