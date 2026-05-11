package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewProviderClientSupportsOpenCodeAPIAliases(t *testing.T) {
	for _, provider := range []string{"opencode", "open-code", "open_code", "OpenCode Zen", "opencode-zen", "open-code-zen"} {
		client, err := NewProviderClient(Config{Provider: provider, Model: openCodeDefaultModel, APIKey: "test-key"})
		if err != nil {
			t.Fatalf("NewProviderClient(%q): %v", provider, err)
		}
		if client.Name() != "opencode" {
			t.Fatalf("expected opencode client for %q, got %q", provider, client.Name())
		}
	}
}

func TestNewProviderClientSupportsOpenCodeGoAliases(t *testing.T) {
	for _, provider := range []string{"opencode-go", "opencode_go", "OpenCode Go", "opencode go", "open-code-go", "open_code_go"} {
		client, err := NewProviderClient(Config{Provider: provider, Model: openCodeGoDefaultModel, APIKey: "test-key"})
		if err != nil {
			t.Fatalf("NewProviderClient(%q): %v", provider, err)
		}
		if client.Name() != "opencode-go" {
			t.Fatalf("expected opencode-go client for %q, got %q", provider, client.Name())
		}
	}
}

func TestNewProviderClientRequiresOpenCodeAPIKey(t *testing.T) {
	_, err := NewProviderClient(Config{Provider: "opencode", Model: openCodeDefaultModel})
	if err == nil {
		t.Fatalf("expected missing API key error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "api key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenCodeClientUsesResponsesForGPTModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := body["model"]; got != "gpt-5.3-codex" {
			t.Fatalf("model = %#v", got)
		}
		if _, ok := body["tools"].([]any); !ok {
			t.Fatalf("expected responses tools in request: %#v", body["tools"])
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"go.mod\"}"}]}`))
	}))
	defer server.Close()

	client := NewOpenCodeClient(server.URL+"/v1/responses", "test-key")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model:  "opencode/gpt-5.3-codex",
		System: "system",
		Messages: []Message{{
			Role: "user",
			Text: "inspect",
		}},
		Tools: []ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: map[string]any{
				"type": "object",
			},
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "read_file" {
		t.Fatalf("unexpected tool calls: %#v", resp.Message.ToolCalls)
	}
	if resp.Message.ToolCalls[0].Arguments != `{"path":"go.mod"}` {
		t.Fatalf("unexpected tool arguments: %q", resp.Message.ToolCalls[0].Arguments)
	}
}

func TestOpenCodeClientUsesChatCompletionsForCompatibleModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := body["model"]; got != "glm-5.1" {
			t.Fatalf("model = %#v", got)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenCodeClient(server.URL, "test-key")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "opencode/glm-5.1",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text != "ok" {
		t.Fatalf("text = %q", resp.Message.Text)
	}
}

func TestOpenCodeClientUsesMessagesForClaudeModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("unexpected x-api-key header: %q", got)
		}
		if got := r.Header.Get("authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := body["model"]; got != "claude-sonnet-4-7" {
			t.Fatalf("model = %#v", got)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer server.Close()

	client := NewOpenCodeClient(server.URL, "test-key")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "opencode/claude-sonnet-4-7",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text != "ok" {
		t.Fatalf("text = %q", resp.Message.Text)
	}
}

func TestOpenCodeGoClientUsesMessagesForMiniMaxModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := body["model"]; got != "minimax-m2.7" {
			t.Fatalf("model = %#v", got)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer server.Close()

	client := NewOpenCodeGoClient(server.URL+"/v1/messages", "test-key")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "opencode-go/minimax-m2.7",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text != "ok" {
		t.Fatalf("text = %q", resp.Message.Text)
	}
}

func TestOpenCodeGoClientUsesChatCompletionsForGoModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := body["model"]; got != "deepseek-v4-pro" {
			t.Fatalf("model = %#v", got)
		}
		responseFormat, ok := body["response_format"].(map[string]any)
		if !ok || responseFormat["type"] != "json_object" {
			t.Fatalf("response_format = %#v", body["response_format"])
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenCodeGoClient(server.URL, "test-key")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model:    "opencode-go/deepseek-v4-pro",
		JSONMode: true,
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text != "ok" {
		t.Fatalf("text = %q", resp.Message.Text)
	}
}

func TestOpenCodeKimiOmitsJSONMode(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		client   func(string) *OpenCodeClient
	}{
		{
			name:     "zen",
			provider: "opencode",
			model:    "opencode/kimi-k2.6",
			client: func(url string) *OpenCodeClient {
				return NewOpenCodeClient(url, "test-key")
			},
		},
		{
			name:     "go",
			provider: "opencode-go",
			model:    "opencode-go/kimi-k2.6",
			client: func(url string) *OpenCodeClient {
				return NewOpenCodeGoClient(url, "test-key")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if got := body["model"]; got != "kimi-k2.6" {
					t.Fatalf("model = %#v", got)
				}
				if _, ok := body["response_format"]; ok {
					t.Fatalf("expected %s kimi request to omit response_format: %#v", tc.provider, body["response_format"])
				}
				w.Header().Set("content-type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"},"finish_reason":"stop"}]}`))
			}))
			defer server.Close()

			resp, err := tc.client(server.URL).Complete(context.Background(), ChatRequest{
				Model:    tc.model,
				JSONMode: true,
				Messages: []Message{{
					Role: "user",
					Text: "return json",
				}},
			})
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if resp.Message.Text != `{"ok":true}` {
				t.Fatalf("text = %q", resp.Message.Text)
			}
		})
	}
}

func TestOpenCodeGoClientRetriesWithoutJSONModeWhenUnsupported(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if requests == 1 {
			if _, ok := body["response_format"]; !ok {
				t.Fatalf("expected first request to include response_format")
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported parameter: response_format","type":"invalid_request_error","param":"response_format"}}`))
			return
		}
		if _, ok := body["response_format"]; ok {
			t.Fatalf("expected retry to omit response_format: %#v", body["response_format"])
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenCodeGoClient(server.URL, "test-key")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model:    "opencode-go/deepseek-v4-pro",
		JSONMode: true,
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
	if resp.Message.Text != "ok" {
		t.Fatalf("text = %q", resp.Message.Text)
	}
}

func TestFetchOpenCodeModelsParsesModelList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-5.5","object":"model","owned_by":"opencode"}]}`))
	}))
	defer server.Close()

	models, normalized, err := FetchOpenCodeModels(context.Background(), server.URL+"/v1", "")
	if err != nil {
		t.Fatalf("FetchOpenCodeModels: %v", err)
	}
	if normalized != server.URL {
		t.Fatalf("normalized = %q, want %q", normalized, server.URL)
	}
	if len(models) != 1 || models[0].ID != "gpt-5.5" {
		t.Fatalf("models = %#v", models)
	}
}

func TestFetchOpenCodeGoModelsParsesModelList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"deepseek-v4-pro","object":"model","owned_by":"opencode"}]}`))
	}))
	defer server.Close()

	models, normalized, err := FetchOpenCodeGoModels(context.Background(), server.URL+"/v1", "")
	if err != nil {
		t.Fatalf("FetchOpenCodeGoModels: %v", err)
	}
	if normalized != server.URL {
		t.Fatalf("normalized = %q, want %q", normalized, server.URL)
	}
	if len(models) != 1 || models[0].ID != "deepseek-v4-pro" {
		t.Fatalf("models = %#v", models)
	}
}

func TestChooseOpenCodeModelUsesAPIKeyModelListNonInteractive(t *testing.T) {
	rt := &runtimeState{}
	models := []OpenCodeModelInfo{
		{ID: "minimax-m2.5-free"},
	}

	selected, err := rt.chooseOpenCodeModel(models, "")
	if err != nil {
		t.Fatalf("chooseOpenCodeModel empty current: %v", err)
	}
	if selected != "opencode/minimax-m2.5-free" {
		t.Fatalf("selected = %q", selected)
	}

	selected, err = rt.chooseOpenCodeModel(models, "opencode/gpt-5.4-mini")
	if err != nil {
		t.Fatalf("chooseOpenCodeModel unavailable current: %v", err)
	}
	if selected != "opencode/minimax-m2.5-free" {
		t.Fatalf("selected unavailable = %q", selected)
	}

	selected, err = rt.chooseOpenCodeModel(models, "minimax-m2.5-free")
	if err != nil {
		t.Fatalf("chooseOpenCodeModel available current: %v", err)
	}
	if selected != "opencode/minimax-m2.5-free" {
		t.Fatalf("selected available = %q", selected)
	}
}

func TestChooseOpenCodeGoModelUsesAPIKeyModelListNonInteractive(t *testing.T) {
	rt := &runtimeState{}
	models := []OpenCodeModelInfo{
		{ID: "deepseek-v4-pro"},
		{ID: "qwen3.6-plus"},
	}

	selected, err := rt.chooseOpenCodeModelForProvider("opencode-go", models, "")
	if err != nil {
		t.Fatalf("chooseOpenCodeModelForProvider empty current: %v", err)
	}
	if selected != "opencode-go/deepseek-v4-pro" {
		t.Fatalf("selected = %q", selected)
	}

	selected, err = rt.chooseOpenCodeModelForProvider("opencode-go", models, "opencode/gpt-5.4-mini")
	if err != nil {
		t.Fatalf("chooseOpenCodeModelForProvider unavailable current: %v", err)
	}
	if selected != "opencode-go/deepseek-v4-pro" {
		t.Fatalf("selected unavailable = %q", selected)
	}
}

func TestResolveOpenCodeModelForAPIKeyRejectsUnavailableModel(t *testing.T) {
	sawAuth := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		sawAuth = r.Header.Get("authorization") == "Bearer test-key"
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"minimax-m2.5-free"}]}`))
	}))
	defer server.Close()

	rt := &runtimeState{}
	_, _, err := rt.resolveOpenCodeModelForAPIKey("opencode/gpt-5.4-mini", server.URL, "test-key", "analysis worker")
	if err == nil {
		t.Fatalf("expected unavailable model error")
	}
	if !sawAuth {
		t.Fatalf("expected OpenCode model lookup to use configured API key")
	}
	if !strings.Contains(err.Error(), "not available") || !strings.Contains(err.Error(), "opencode/minimax-m2.5-free") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveOpenCodeGoModelForAPIKeyUsesGoPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-v4-pro"}]}`))
	}))
	defer server.Close()

	rt := &runtimeState{}
	model, normalized, err := rt.resolveOpenCodeModelForProviderAPIKey("opencode-go", "", server.URL, "test-key", "analysis worker")
	if err != nil {
		t.Fatalf("resolveOpenCodeModelForProviderAPIKey: %v", err)
	}
	if normalized != server.URL {
		t.Fatalf("normalized = %q", normalized)
	}
	if model != "opencode-go/deepseek-v4-pro" {
		t.Fatalf("model = %q", model)
	}
}
