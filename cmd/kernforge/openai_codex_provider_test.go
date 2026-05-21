package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type staticCodexTokenSource struct {
	token string
}

func (s staticCodexTokenSource) AccessToken(ctx context.Context) (string, error) {
	return s.token, nil
}

func TestNewProviderClientSupportsOpenAICodexWithoutAPIKey(t *testing.T) {
	client, err := NewProviderClient(Config{Provider: "openai-codex", Model: "gpt-5.5"})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	if client.Name() != "openai-codex" {
		t.Fatalf("expected openai-codex client, got %q", client.Name())
	}

	for _, provider := range []string{"openai_codex", "openai-codex-subscription", "openai_codex_subscription"} {
		client, err = NewProviderClient(Config{Provider: provider, Model: "gpt-5.5"})
		if err != nil {
			t.Fatalf("NewProviderClient alias %q: %v", provider, err)
		}
		if client.Name() != "openai-codex" {
			t.Fatalf("expected openai-codex client for alias %q, got %q", provider, client.Name())
		}
	}
}

func TestBuildOpenAICodexRequestBodyPreservesToolContext(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model:  "",
		System: "system prompt",
		Messages: []Message{
			{Role: "user", Text: "inspect"},
			{Role: "assistant", Text: "calling", ToolCalls: []ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"main.go"}`}}},
			{Role: "tool", ToolCallID: "call_1", ToolName: "read_file", Text: "file body"},
		},
		Tools: []ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: map[string]any{
				"type": "object",
			},
		}},
		JSONMode:        true,
		MaxTokens:       123,
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if payload["model"] != openAICodexDefaultModel {
		t.Fatalf("expected default model %q, got %#v", openAICodexDefaultModel, payload["model"])
	}
	if payload["instructions"] != "system prompt" {
		t.Fatalf("expected instructions to be preserved, got %#v", payload["instructions"])
	}
	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("expected reasoning effort high, got %#v", payload["reasoning"])
	}
	if _, ok := payload["tools"].([]any); !ok {
		t.Fatalf("expected responses tools array, got %#v", payload["tools"])
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) != 4 {
		t.Fatalf("expected four input items, got %#v", payload["input"])
	}
	assistant, ok := input[1].(map[string]any)
	if !ok || assistant["phase"] != messagePhaseCommentary {
		t.Fatalf("expected assistant tool preamble to carry commentary phase, got %#v", input[1])
	}
	encoded := string(body)
	for _, needle := range []string{`"stream":true`, `"type":"function_call"`, `"call_id":"call_1"`, `"type":"function_call_output"`, `"type":"json_object"`} {
		if !strings.Contains(encoded, needle) {
			t.Fatalf("expected %q in request body %s", needle, encoded)
		}
	}
}

func TestBuildOpenAICodexRequestBodyPreservesAssistantPhase(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "continue"},
			{Role: "assistant", Phase: messagePhaseCommentary, Text: "checking"},
			{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: "done"},
			{Role: "assistant", Phase: messagePhaseFinalAnswerCandidate, Text: "candidate"},
		},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) != 4 {
		t.Fatalf("expected four input items, got %#v", payload["input"])
	}
	if got := input[1].(map[string]any)["phase"]; got != messagePhaseCommentary {
		t.Fatalf("expected commentary phase, got %#v", got)
	}
	if got := input[2].(map[string]any)["phase"]; got != messagePhaseFinalAnswer {
		t.Fatalf("expected final-answer phase, got %#v", got)
	}
	if _, ok := input[3].(map[string]any)["phase"]; ok {
		t.Fatalf("did not expect internal candidate phase to be sent, got %#v", input[3])
	}
}

func TestBuildOpenAICodexRequestBodyPreservesToolContentItems(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "inspect image"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_img", Name: "view_image", Arguments: `{"path":"shot.png"}`}}},
			{
				Role:       "tool",
				ToolCallID: "call_img",
				ToolName:   "view_image",
				Text:       `{"image_url":"data:image/png;base64,AAA","detail":"high"}`,
				ToolContentItems: []ToolContentItem{{
					Type:     "input_image",
					ImageURL: "data:image/png;base64,AAA",
					Detail:   imageDetailHigh,
				}},
			},
		},
		Tools: []ToolDefinition{{
			Name:        "view_image",
			Description: "View image",
			InputSchema: map[string]any{
				"type": "object",
			},
			OutputSchema: map[string]any{
				"type": "object",
			},
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	input := payload["input"].([]any)
	var output []any
	for _, raw := range input {
		item := raw.(map[string]any)
		if item["type"] == "function_call_output" {
			output = item["output"].([]any)
			break
		}
	}
	if len(output) != 1 {
		t.Fatalf("expected one output content item, got %#v in %s", output, body)
	}
	image := output[0].(map[string]any)
	if image["type"] != "input_image" || image["image_url"] != "data:image/png;base64,AAA" || image["detail"] != imageDetailHigh {
		t.Fatalf("unexpected tool output image item: %#v", image)
	}
	tools := payload["tools"].([]any)
	tool := tools[0].(map[string]any)
	if _, ok := tool["output_schema"].(map[string]any); !ok {
		t.Fatalf("expected output_schema to be preserved, got %#v", tool)
	}
}

func assertCodexLocalImageContent(t *testing.T, content []any, openIndex int) map[string]any {
	t.Helper()
	if openIndex < 0 || openIndex+2 >= len(content) {
		t.Fatalf("image wrapper indexes out of range: open=%d len=%d content=%#v", openIndex, len(content), content)
	}
	openTag, ok := content[openIndex].(map[string]any)
	if !ok || openTag["type"] != "input_text" || openTag["text"] != "<image name=[Image #1]>" {
		t.Fatalf("expected Codex local image open tag, got %#v", content[openIndex])
	}
	image, ok := content[openIndex+1].(map[string]any)
	if !ok || image["type"] != "input_image" {
		t.Fatalf("expected input_image, got %#v", content[openIndex+1])
	}
	closeTag, ok := content[openIndex+2].(map[string]any)
	if !ok || closeTag["type"] != "input_text" || closeTag["text"] != codexImageCloseTag {
		t.Fatalf("expected Codex image close tag, got %#v", content[openIndex+2])
	}
	return image
}

func TestBuildOpenAICodexRequestBodyPreservesImageDetail(t *testing.T) {
	dir := t.TempDir()
	writeTestImage(t, dir, "shot.png")

	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model:      "gpt-5.5",
		WorkingDir: dir,
		Messages: []Message{{
			Role: "user",
			Text: "inspect",
			Images: []MessageImage{{
				Path:      "shot.png",
				MediaType: "image/png",
				Detail:    imageDetailOriginal,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	input := payload["input"].([]any)
	message := input[0].(map[string]any)
	content := message["content"].([]any)
	if len(content) != 4 {
		t.Fatalf("expected text plus wrapped image content, got %#v", content)
	}
	image := assertCodexLocalImageContent(t, content, 1)
	if image["detail"] != imageDetailOriginal {
		t.Fatalf("expected original detail, got %#v in body %s", image["detail"], body)
	}
	if strings.Contains(string(body), `"detail":"auto"`) {
		t.Fatalf("request body must not use removed auto detail: %s", body)
	}
}

func TestBuildOpenAICodexRequestBodyDefaultsImageDetailHigh(t *testing.T) {
	dir := t.TempDir()
	writeTestImage(t, dir, "shot.png")

	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model:      "gpt-5.5",
		WorkingDir: dir,
		Messages: []Message{{
			Role: "user",
			Text: "inspect",
			Images: []MessageImage{{
				Path:      "shot.png",
				MediaType: "image/png",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	input := payload["input"].([]any)
	message := input[0].(map[string]any)
	content := message["content"].([]any)
	if len(content) != 4 {
		t.Fatalf("expected text plus wrapped image content, got %#v", content)
	}
	image := assertCodexLocalImageContent(t, content, 1)
	if image["detail"] != imageDetailHigh {
		t.Fatalf("expected high detail, got %#v in body %s", image["detail"], body)
	}
}

func TestBuildOpenAICodexRequestBodyWrapsImageOnlyMessageLikeCodex(t *testing.T) {
	dir := t.TempDir()
	writeTestImage(t, dir, "shot.png")

	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model:      "gpt-5.5",
		WorkingDir: dir,
		Messages: []Message{{
			Role: "user",
			Images: []MessageImage{{
				Path:      "shot.png",
				MediaType: "image/png",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	input := payload["input"].([]any)
	message := input[0].(map[string]any)
	content := message["content"].([]any)
	if len(content) != 3 {
		t.Fatalf("expected wrapped image-only content, got %#v", content)
	}
	assertCodexLocalImageContent(t, content, 0)
}

func TestBuildOpenAICodexRequestBodyDropsOrphanToolOutput(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "continue"},
			{Role: "tool", ToolCallID: "call_orphan", ToolName: "update_plan", Text: "[in_progress] inspect"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_read", Name: "read_file", Arguments: `{"path":"main.go"}`}}},
			{Role: "tool", ToolCallID: "call_read", ToolName: "read_file", Text: "package main"},
		},
		Tools: []ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(payload.Input) == 0 {
		t.Fatalf("expected input items, got %s", body)
	}
	for _, item := range payload.Input {
		if item["type"] == "function_call_output" && item["call_id"] == "call_orphan" {
			t.Fatalf("orphan tool result must not be sent as function_call_output: %s", body)
		}
	}
	encoded := string(body)
	for _, forbidden := range []string{
		"Recovered transcript note",
		"tool_call_id=call_orphan",
		"call_orphan",
	} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("orphan tool result must be dropped, found %q in request body %s", forbidden, encoded)
		}
	}
	for _, want := range []string{
		`"type":"function_call"`,
		`"call_id":"call_read"`,
		`"type":"function_call_output"`,
	} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("expected %q in request body %s", want, encoded)
		}
	}
}

func TestBuildOpenAICodexRequestBodySynthesizesMissingToolOutput(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "inspect"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_expected", Name: "read_file", Arguments: `{"path":"main.go"}`}}},
			{Role: "user", Text: "Do not repeat the same tool call; continue from local context."},
		},
		Tools: []ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}
	encoded := string(body)
	for _, want := range []string{
		`"type":"function_call"`,
		`"call_id":"call_expected"`,
		`"type":"function_call_output"`,
		"NOTICE: tool call was superseded before execution",
	} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("expected %q in request body %s", want, encoded)
		}
	}
}

func TestOpenAICodexClientAppliesConfiguredReasoningEffort(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer server.Close()

	client := NewOpenAICodexClientWithReasoningEffort(server.URL, "x-high")
	client.tokenSource = staticCodexTokenSource{token: "test-token"}
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "xhigh" {
		t.Fatalf("expected configured xhigh reasoning effort, got %#v", payload["reasoning"])
	}
}

func TestBuildOpenAICodexRequestBodyRejectsInvalidReasoningEffort(t *testing.T) {
	_, err := buildOpenAICodexRequestBody(ChatRequest{
		Model:           "gpt-5.5",
		ReasoningEffort: "turbo",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid reasoning effort") {
		t.Fatalf("expected invalid reasoning effort error, got %v", err)
	}
}

func TestSyncClientFromConfigKeepsOpenAICodexReviewerEffortPerTarget(t *testing.T) {
	rt := &runtimeState{
		cfg: Config{
			Provider:        "openai-codex",
			Model:           "gpt-5.5",
			ReasoningEffort: "low",
			Review: ReviewHarnessConfig{
				RoleModels: map[string]ReviewModelConfig{
					"primary_reviewer": {
						Provider:        "openai-codex",
						Model:           "gpt-5.5",
						ReasoningEffort: "medium",
					},
				},
			},
		},
		agent: &Agent{
			ReviewerClient:    NewOpenAICodexClientWithReasoningEffort("", "high"),
			ReviewerModel:     "gpt-5.5",
			AuxReviewerClient: NewOpenAICodexClientWithReasoningEffort("", "high"),
			AuxReviewerModel:  "gpt-5.5",
		},
	}

	rt.syncClientFromConfig()

	mainClient, ok := rt.agent.Client.(*OpenAICodexClient)
	if !ok {
		t.Fatalf("expected OpenAI Codex main client, got %T", rt.agent.Client)
	}
	if mainClient.reasoningEffort != "low" {
		t.Fatalf("main reasoning effort = %q, want low", mainClient.reasoningEffort)
	}
	reviewerClient, ok := rt.agent.ReviewerClient.(*OpenAICodexClient)
	if !ok {
		t.Fatalf("expected OpenAI Codex reviewer client, got %T", rt.agent.ReviewerClient)
	}
	if reviewerClient.reasoningEffort != "high" {
		t.Fatalf("reviewer reasoning effort = %q, want high", reviewerClient.reasoningEffort)
	}
	if rt.agent.AuxReviewerClient != nil || rt.agent.AuxReviewerModel != "" {
		t.Fatalf("expected auxiliary reviewer cache to be cleared")
	}

	rt.cfg.ReasoningEffort = "x-high"
	rt.syncClientFromConfig()

	reviewerClient, ok = rt.agent.ReviewerClient.(*OpenAICodexClient)
	if !ok {
		t.Fatalf("expected refreshed OpenAI Codex reviewer client, got %T", rt.agent.ReviewerClient)
	}
	if reviewerClient.reasoningEffort != "high" {
		t.Fatalf("reviewer reasoning effort after main change = %q, want high", reviewerClient.reasoningEffort)
	}

	rt.cfg.Review.RoleModels["primary_reviewer"] = ReviewModelConfig{
		Provider:        "openai-codex",
		Model:           "gpt-5.5",
		ReasoningEffort: "x-high",
	}
	rt.syncClientFromConfig()

	reviewerClient, ok = rt.agent.ReviewerClient.(*OpenAICodexClient)
	if !ok {
		t.Fatalf("expected refreshed OpenAI Codex reviewer client, got %T", rt.agent.ReviewerClient)
	}
	if reviewerClient.reasoningEffort != "xhigh" {
		t.Fatalf("reviewer reasoning effort = %q, want xhigh", reviewerClient.reasoningEffort)
	}
}

func TestFetchOpenAICodexModelsUsesOAuthBackend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("client_version"); got != "1.0.0" {
			t.Fatalf("unexpected client_version: %q", got)
		}
		if got := r.Header.Get("authorization"); got != "Bearer test-token" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5","supported_in_api":true,"visibility":"list","supports_image_detail_original":true},{"slug":"hidden","display_name":"Hidden","supported_in_api":true,"visibility":"hidden"}]}`))
	}))
	defer server.Close()

	models, err := FetchOpenAICodexModels(context.Background(), server.URL, staticCodexTokenSource{token: "test-token"}, server.Client())
	if err != nil {
		t.Fatalf("FetchOpenAICodexModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gpt-5.5" || models[0].Name != "GPT-5.5" {
		t.Fatalf("unexpected models: %#v", models)
	}
	if !models[0].SupportsImageDetailOriginal {
		t.Fatalf("expected supports_image_detail_original to be preserved: %#v", models[0])
	}
}

func TestOpenAICodexModelChoicesUseRemoteCatalogAsSourceOfTruth(t *testing.T) {
	home := t.TempDir()
	authPath := filepath.Join(home, "codex_auth.json")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(openAICodexAuthFileEnv, authPath)
	t.Setenv(openAICodexAccessTokenEnv, "")
	if err := saveCodexOAuthAuthFile(authPath, codexOAuthTokens{AccessToken: "test-token"}); err != nil {
		t.Fatalf("save auth file: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"slug":"chatgpt-remote-only","display_name":"ChatGPT Remote Only","supported_in_api":true,"visibility":"list"}]}`))
	}))
	defer server.Close()

	rt := &runtimeState{
		cfg: Config{
			Provider: "openai-codex",
			BaseURL:  server.URL,
		},
	}
	models, authoritative := rt.openAICodexModelChoicesWithSource("legacy-configured-model")
	if !authoritative {
		t.Fatalf("expected remote model catalog to be authoritative")
	}
	if len(models) != 1 || models[0].ID != "chatgpt-remote-only" {
		t.Fatalf("expected remote-only model list, got %#v", models)
	}
	for _, model := range models {
		if model.ID == "legacy-configured-model" {
			t.Fatalf("remote authoritative catalog must not append unavailable current model: %#v", models)
		}
	}
}

func TestOpenAICodexModelChoicesAppendCurrentWhenRemoteUnavailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(openAICodexAuthFileEnv, filepath.Join(home, "missing_auth.json"))
	t.Setenv(openAICodexAccessTokenEnv, "")

	rt := &runtimeState{
		cfg: Config{
			Provider: "openai-codex",
			BaseURL:  "http://127.0.0.1:1",
		},
	}
	models, authoritative := rt.openAICodexModelChoicesWithSource("legacy-configured-model")
	if authoritative {
		t.Fatalf("expected missing remote catalog to fall back")
	}
	foundCurrent := false
	for _, model := range models {
		if model.ID == "legacy-configured-model" {
			foundCurrent = true
			break
		}
	}
	if !foundCurrent {
		t.Fatalf("fallback catalog should keep current model available, got %#v", models)
	}
}

func TestOpenAICodexClientCompleteParsesResponsesOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("authorization"); got != "Bearer test-token" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ready\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"call_id\":\"call_2\",\"name\":\"grep\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.function_call_arguments.done\",\"output_index\":1,\"arguments\":\"{\\\"pattern\\\":\\\"x\\\"}\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer server.Close()

	client := NewOpenAICodexClient(server.URL)
	client.tokenSource = staticCodexTokenSource{token: "test-token"}
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
		Tools: []ToolDefinition{{
			Name:        "grep",
			Description: "Search",
			InputSchema: map[string]any{
				"type": "object",
			},
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text != "ready" {
		t.Fatalf("expected text, got %q", resp.Message.Text)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID != "call_2" || resp.Message.ToolCalls[0].Name != "grep" {
		t.Fatalf("unexpected tool calls: %#v", resp.Message.ToolCalls)
	}
}

func TestOpenAICodexClientReplaysTurnState(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		switch requestCount {
		case 1:
			if got := r.Header.Get(codexTurnStateHeader); got != "" {
				t.Fatalf("first request should not send turn state, got %q", got)
			}
			if got := r.Header.Get("session-id"); got != "session-123" {
				t.Fatalf("unexpected session-id: %q", got)
			}
			if got := r.Header.Get("thread-id"); got != "thread-456" {
				t.Fatalf("unexpected thread-id: %q", got)
			}
			if got := r.Header.Get("x-client-request-id"); got != "thread-456" {
				t.Fatalf("unexpected x-client-request-id: %q", got)
			}
			assertTurnMetadataHeader(t, r, "turn-abc")
			w.Header().Set(codexTurnStateHeader, "codex-sticky")
		case 2:
			if got := r.Header.Get(codexTurnStateHeader); got != "codex-sticky" {
				t.Fatalf("second request should replay turn state, got %q", got)
			}
			assertTurnMetadataHeader(t, r, "turn-abc")
		default:
			t.Fatalf("unexpected request %d", requestCount)
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer server.Close()

	client := NewOpenAICodexClient(server.URL)
	client.tokenSource = staticCodexTokenSource{token: "test-token"}
	state := &ProviderTurnState{}
	for i := 0; i < 2; i++ {
		_, err := client.Complete(context.Background(), ChatRequest{
			Model:     "gpt-5.5",
			TurnState: state,
			TurnMetadata: map[string]any{
				"session_id": "session-123",
				"thread_id":  "thread-456",
				"turn_id":    "turn-abc",
			},
			SessionID: "session-123",
			ThreadID:  "thread-456",
			Messages: []Message{{
				Role: "user",
				Text: "hello",
			}},
		})
		if err != nil {
			t.Fatalf("Complete %d: %v", i+1, err)
		}
	}
	if state.Value() != "codex-sticky" {
		t.Fatalf("expected captured turn state, got %q", state.Value())
	}
}

func assertTurnMetadataHeader(t *testing.T, r *http.Request, wantTurnID string) {
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

func TestParseOpenAICodexResponsePreservesMessagePhase(t *testing.T) {
	resp, err := parseOpenAICodexResponse([]byte(`{
		"status":"completed",
		"output":[{
			"type":"message",
			"role":"assistant",
			"phase":"commentary",
			"content":[{"type":"output_text","text":"still working"}]
		}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAICodexResponse: %v", err)
	}
	if resp.Message.Text != "still working" {
		t.Fatalf("expected text, got %q", resp.Message.Text)
	}
	if resp.Message.Phase != messagePhaseCommentary {
		t.Fatalf("expected commentary phase, got %q", resp.Message.Phase)
	}

	resp, err = parseOpenAICodexResponse([]byte(`{
		"status":"completed",
		"output":[{
			"type":"message",
			"role":"assistant",
			"phase":"final_answer",
			"content":[{"type":"output_text","text":"done"}]
		}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAICodexResponse final: %v", err)
	}
	if resp.Message.Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected final-answer phase, got %q", resp.Message.Phase)
	}
}

func TestParseOpenAICodexResponsePreservesEndTurn(t *testing.T) {
	resp, err := parseOpenAICodexResponse([]byte(`{
		"status":"completed",
		"end_turn":false,
		"output":[{
			"type":"message",
			"role":"assistant",
			"content":[{"type":"output_text","text":"continuing"}]
		}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAICodexResponse: %v", err)
	}
	if resp.EndTurn == nil || *resp.EndTurn {
		t.Fatalf("expected end_turn=false to be preserved, got %#v", resp.EndTurn)
	}
}

func TestParseOpenAICodexResponseUsesFinalTextOverCommentary(t *testing.T) {
	resp, err := parseOpenAICodexResponse([]byte(`{
		"status":"completed",
		"output_text":"still working\ndone",
		"output":[{
			"type":"message",
			"role":"assistant",
			"phase":"commentary",
			"content":[{"type":"output_text","text":"still working"}]
		},{
			"type":"message",
			"role":"assistant",
			"phase":"final_answer",
			"content":[{"type":"output_text","text":"done"}]
		}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAICodexResponse: %v", err)
	}
	if resp.Message.Text != "done" {
		t.Fatalf("expected only final-answer text, got %q", resp.Message.Text)
	}
	if strings.Contains(resp.Message.Text, "still working") {
		t.Fatalf("commentary text leaked into final text: %q", resp.Message.Text)
	}
	if resp.Message.Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected final-answer phase, got %q", resp.Message.Phase)
	}
}

func TestParseOpenAICodexResponsePreservesCommentaryWithAggregateOutputText(t *testing.T) {
	resp, err := parseOpenAICodexResponse([]byte(`{
		"status":"completed",
		"output_text":"still working",
		"output":[{
			"type":"message",
			"role":"assistant",
			"phase":"commentary",
			"content":[{"type":"output_text","text":"still working"}]
		}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAICodexResponse: %v", err)
	}
	if resp.Message.Text != "still working" {
		t.Fatalf("expected commentary text, got %q", resp.Message.Text)
	}
	if resp.Message.Phase != messagePhaseCommentary {
		t.Fatalf("expected commentary phase, got %q", resp.Message.Phase)
	}
}

func TestParseOpenAICodexResponseParsesCodexToolCallVariants(t *testing.T) {
	resp, err := parseOpenAICodexResponse([]byte(`{
		"status":"completed",
		"output":[{
			"type":"custom_tool_call",
			"call_id":"call_patch",
			"name":"apply_patch",
			"input":"*** Begin Patch\n*** End Patch"
		},{
			"type":"tool_search_call",
			"call_id":"call_search",
			"execution":"client",
			"arguments":{"query":"apply_patch","limit":5}
		},{
			"type":"tool_search_call",
			"call_id":"call_server",
			"execution":"server",
			"arguments":{"query":"ignored"}
		}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAICodexResponse: %v", err)
	}
	if len(resp.Message.ToolCalls) != 2 {
		t.Fatalf("expected custom and client tool_search calls, got %#v", resp.Message.ToolCalls)
	}
	if resp.Message.ToolCalls[0].ID != "call_patch" || resp.Message.ToolCalls[0].Name != "apply_patch" {
		t.Fatalf("unexpected custom tool call: %#v", resp.Message.ToolCalls[0])
	}
	var patchArgs map[string]string
	if err := json.Unmarshal([]byte(resp.Message.ToolCalls[0].Arguments), &patchArgs); err != nil {
		t.Fatalf("custom tool arguments are not JSON: %v", err)
	}
	if patchArgs["patch"] != "*** Begin Patch\n*** End Patch" {
		t.Fatalf("expected custom apply_patch input to map to patch argument, got %#v", patchArgs)
	}
	if resp.Message.ToolCalls[1].ID != "call_search" || resp.Message.ToolCalls[1].Name != "tool_search" {
		t.Fatalf("unexpected tool_search call: %#v", resp.Message.ToolCalls[1])
	}
	var searchArgs map[string]any
	if err := json.Unmarshal([]byte(resp.Message.ToolCalls[1].Arguments), &searchArgs); err != nil {
		t.Fatalf("tool_search arguments are not JSON: %v", err)
	}
	if searchArgs["query"] != "apply_patch" || searchArgs["limit"].(float64) != 5 {
		t.Fatalf("unexpected tool_search arguments: %#v", searchArgs)
	}
}

func TestReadOpenAICodexStreamUsesDoneMessageWhenNoDelta(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"done text"}]}}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.Text != "done text" {
		t.Fatalf("expected done message text, got %q", resp.Message.Text)
	}
}

func TestReadOpenAICodexStreamRejectsMissingCompletedEvent(t *testing.T) {
	stream := strings.NewReader(`data: {"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"partial text"}]}}` + "\n\n")
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err == nil {
		t.Fatalf("expected missing response.completed to fail, got %#v", resp)
	}
	if !strings.Contains(err.Error(), "stream closed before response.completed") {
		t.Fatalf("expected response.completed error, got %v", err)
	}
}

func TestReadOpenAICodexStreamPreservesMessagePhaseWithoutCompletedResponseBody(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","phase":"commentary"}}`,
		`data: {"type":"response.output_text.delta","delta":"checking"}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.Text != "checking" {
		t.Fatalf("expected stream text, got %q", resp.Message.Text)
	}
	if resp.Message.Phase != messagePhaseCommentary {
		t.Fatalf("expected commentary phase, got %q", resp.Message.Phase)
	}
}

func TestReadOpenAICodexStreamPreservesEndTurnFromCompletedResponse(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.completed","response":{"status":"completed","end_turn":false,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"continuing"}]}]}}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.Text != "continuing" {
		t.Fatalf("expected completed response text, got %q", resp.Message.Text)
	}
	if resp.EndTurn == nil || *resp.EndTurn {
		t.Fatalf("expected stream end_turn=false to be preserved, got %#v", resp.EndTurn)
	}
}

func TestReadOpenAICodexStreamEmitsToolProgressEvents(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"read_file"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\""}`,
		`data: {"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"path\":\"main.go\"}"}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	var events []ProgressEvent
	resp, err := readOpenAICodexStream(context.Background(), stream, func(event ProgressEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "read_file" {
		t.Fatalf("expected read_file tool call, got %#v", resp.Message.ToolCalls)
	}
	if !progressEventsContain(events, progressKindModelStreamToolCall, "read_file") {
		t.Fatalf("expected tool-call progress event, got %#v", events)
	}
	if !progressEventsContain(events, progressKindModelStreamToolReady, "read_file") {
		t.Fatalf("expected tool-ready progress event, got %#v", events)
	}
}

func TestReadOpenAICodexStreamParsesCodexToolCallVariants(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"custom_tool_call","call_id":"call_patch","name":"apply_patch","input":"*** Begin Patch\n*** End Patch"}}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"tool_search_call","call_id":"call_search","execution":"client","arguments":{"query":"apply_patch","limit":5}}}`,
		`data: {"type":"response.output_item.done","output_index":2,"item":{"type":"tool_search_call","call_id":"call_server","execution":"server","arguments":{"query":"ignored"}}}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	var events []ProgressEvent
	resp, err := readOpenAICodexStream(context.Background(), stream, func(event ProgressEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if len(resp.Message.ToolCalls) != 2 {
		t.Fatalf("expected custom and client tool_search calls, got %#v", resp.Message.ToolCalls)
	}
	if resp.Message.ToolCalls[0].ID != "call_patch" || resp.Message.ToolCalls[0].Name != "apply_patch" {
		t.Fatalf("unexpected custom tool call: %#v", resp.Message.ToolCalls[0])
	}
	var patchArgs map[string]string
	if err := json.Unmarshal([]byte(resp.Message.ToolCalls[0].Arguments), &patchArgs); err != nil {
		t.Fatalf("custom tool arguments are not JSON: %v", err)
	}
	if patchArgs["patch"] != "*** Begin Patch\n*** End Patch" {
		t.Fatalf("expected custom apply_patch input to map to patch argument, got %#v", patchArgs)
	}
	if resp.Message.ToolCalls[1].ID != "call_search" || resp.Message.ToolCalls[1].Name != "tool_search" {
		t.Fatalf("unexpected tool_search call: %#v", resp.Message.ToolCalls[1])
	}
	if progressEventsContain(events, progressKindModelStreamToolReady, "call_server") {
		t.Fatalf("server-side tool_search call should not be emitted as a client tool call: %#v", events)
	}
	if !progressEventsContain(events, progressKindModelStreamToolReady, "apply_patch") ||
		!progressEventsContain(events, progressKindModelStreamToolReady, "tool_search") {
		t.Fatalf("expected ready events for client tool calls, got %#v", events)
	}
}

func TestReadOpenAICodexStreamEmitsToolReadyFromCompletedResponse(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","call_id":"call_9","name":"read_file","arguments":"{\"path\":\"main.go\"}"}]}}`,
		"",
	}, "\n\n"))
	var events []ProgressEvent
	resp, err := readOpenAICodexStream(context.Background(), stream, func(event ProgressEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID != "call_9" {
		t.Fatalf("expected completed response tool call, got %#v", resp.Message.ToolCalls)
	}
	if !progressEventsContain(events, progressKindModelStreamToolReady, "read_file") {
		t.Fatalf("expected completed-response tool-ready progress event, got %#v", events)
	}
}

func TestCodexOAuthAuthFilePathDefaultsToKernforgeConfig(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv(openAICodexAuthFileEnv, "")
	t.Setenv("CODEX_HOME", filepath.Join(tempDir, "codex-home"))
	t.Setenv("USERPROFILE", tempDir)
	t.Setenv("HOME", tempDir)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	got := codexOAuthAuthFilePath()
	want := filepath.Join(userConfigDir(), openAICodexDefaultAuthFile)
	if got != want {
		t.Fatalf("expected dedicated auth file %q, got %q", want, got)
	}
	if strings.Contains(strings.ToLower(got), filepath.Join(".codex", "auth.json")) {
		t.Fatalf("expected Kernforge auth file, got Codex CLI path %q", got)
	}
}

func TestCodexOAuthAuthFilePathEnvOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom-auth.json")
	t.Setenv(openAICodexAuthFileEnv, path)
	if got := codexOAuthAuthFilePath(); got != path {
		t.Fatalf("expected env override %q, got %q", path, got)
	}
}

func TestSaveAndUpdateCodexOAuthAuthFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex_auth.json")
	if err := saveCodexOAuthAuthFile(path, codexOAuthTokens{
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		IDToken:      "id-old",
	}); err != nil {
		t.Fatalf("saveCodexOAuthAuthFile: %v", err)
	}
	original, auth, err := readCodexOAuthAuthFile(path)
	if err != nil {
		t.Fatalf("readCodexOAuthAuthFile: %v", err)
	}
	if auth.AuthMode != "chatgpt" || auth.Tokens.AccessToken != "access-old" || auth.Tokens.RefreshToken != "refresh-old" {
		t.Fatalf("unexpected saved auth: %#v", auth)
	}
	if !codexOAuthAuthFileUsable(path) {
		t.Fatalf("expected saved auth file to be usable")
	}

	if err := updateCodexOAuthAuthFile(path, original, codexOAuthTokens{AccessToken: "access-new"}); err != nil {
		t.Fatalf("updateCodexOAuthAuthFile: %v", err)
	}
	_, auth, err = readCodexOAuthAuthFile(path)
	if err != nil {
		t.Fatalf("read updated auth: %v", err)
	}
	if auth.Tokens.AccessToken != "access-new" {
		t.Fatalf("expected updated access token, got %#v", auth.Tokens)
	}
	if auth.Tokens.RefreshToken != "refresh-old" {
		t.Fatalf("expected refresh token to be preserved, got %#v", auth.Tokens)
	}

	expiredPath := filepath.Join(t.TempDir(), "expired_codex_auth.json")
	if err := saveCodexOAuthAuthFile(expiredPath, codexOAuthTokens{
		AccessToken: testCodexOAuthJWT(time.Now().Add(-time.Hour)),
	}); err != nil {
		t.Fatalf("save expired auth file: %v", err)
	}
	if codexOAuthAuthFileUsable(expiredPath) {
		t.Fatalf("expired access token without refresh token should not be usable")
	}
	if err := saveCodexOAuthAuthFile(expiredPath, codexOAuthTokens{
		AccessToken:  testCodexOAuthJWT(time.Now().Add(-time.Hour)),
		RefreshToken: "refresh-old",
	}); err != nil {
		t.Fatalf("save refreshable expired auth file: %v", err)
	}
	if !codexOAuthAuthFileUsable(expiredPath) {
		t.Fatalf("expired access token with refresh token should be usable")
	}
}

func TestRunCodexOAuthDeviceLoginSavesDedicatedAuthFile(t *testing.T) {
	var sawUserCode bool
	var sawPoll bool
	var sawExchange bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			sawUserCode = true
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode usercode body: %v", err)
			}
			if body["client_id"] != openAICodexOAuthClientID {
				t.Fatalf("unexpected client_id: %q", body["client_id"])
			}
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"device_auth_id":"device-1","user_code":"ABCD-EFGH","verification_uri":"https://auth.example/device","interval":1,"expires_in":60}`))
		case "/api/accounts/deviceauth/token":
			sawPoll = true
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode poll body: %v", err)
			}
			if body["device_auth_id"] != "device-1" || body["user_code"] != "ABCD-EFGH" {
				t.Fatalf("unexpected poll body: %#v", body)
			}
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"authorization_code":"auth-code-1","code_challenge":"challenge-1","code_verifier":"verifier-1"}`))
		case "/oauth/token":
			sawExchange = true
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			expected := map[string]string{
				"grant_type":    "authorization_code",
				"code":          "auth-code-1",
				"redirect_uri":  openAICodexDeviceRedirect,
				"client_id":     openAICodexOAuthClientID,
				"code_verifier": "verifier-1",
			}
			for key, want := range expected {
				if got := r.Form.Get(key); got != want {
					t.Fatalf("form %s: expected %q, got %q", key, want, got)
				}
			}
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"access-live","refresh_token":"refresh-live","id_token":"id-live"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldUserCodeEndpoint := openAICodexDeviceCodeEndpoint
	oldDeviceTokenEndpoint := openAICodexDeviceTokenEndpoint
	oldOAuthTokenEndpoint := openAICodexOAuthTokenEndpoint
	openAICodexDeviceCodeEndpoint = server.URL + "/api/accounts/deviceauth/usercode"
	openAICodexDeviceTokenEndpoint = server.URL + "/api/accounts/deviceauth/token"
	openAICodexOAuthTokenEndpoint = server.URL + "/oauth/token"
	defer func() {
		openAICodexDeviceCodeEndpoint = oldUserCodeEndpoint
		openAICodexDeviceTokenEndpoint = oldDeviceTokenEndpoint
		openAICodexOAuthTokenEndpoint = oldOAuthTokenEndpoint
	}()

	path := filepath.Join(t.TempDir(), "codex_auth.json")
	tokens, err := runCodexOAuthDeviceLogin(context.Background(), io.Discard, path, server.Client())
	if err != nil {
		t.Fatalf("runCodexOAuthDeviceLogin: %v", err)
	}
	if !sawUserCode || !sawPoll || !sawExchange {
		t.Fatalf("expected all OAuth endpoints to be called: usercode=%t poll=%t exchange=%t", sawUserCode, sawPoll, sawExchange)
	}
	if tokens.AccessToken != "access-live" || tokens.RefreshToken != "refresh-live" {
		t.Fatalf("unexpected tokens: %#v", tokens)
	}
	_, auth, err := readCodexOAuthAuthFile(path)
	if err != nil {
		t.Fatalf("read saved auth file: %v", err)
	}
	if auth.Tokens.AccessToken != "access-live" || auth.Tokens.RefreshToken != "refresh-live" || auth.Tokens.IDToken != "id-live" {
		t.Fatalf("unexpected saved auth: %#v", auth)
	}
}

func TestRunCodexOAuthDeviceLoginRejectsWorkspaceMismatch(t *testing.T) {
	var sawExchange bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"device_auth_id":"device-1","user_code":"ABCD-EFGH","verification_uri":"https://auth.example/device","interval":1,"expires_in":60}`))
		case "/api/accounts/deviceauth/token":
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"authorization_code":"auth-code-1","code_verifier":"verifier-1"}`))
		case "/oauth/token":
			sawExchange = true
			w.Header().Set("content-type", "application/json")
			idToken := testCodexOAuthWorkspaceJWT(time.Now().Add(time.Hour), "workspace-denied")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"access_token":%q,"refresh_token":"refresh-live","id_token":%q}`, idToken, idToken)))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldUserCodeEndpoint := openAICodexDeviceCodeEndpoint
	oldDeviceTokenEndpoint := openAICodexDeviceTokenEndpoint
	oldOAuthTokenEndpoint := openAICodexOAuthTokenEndpoint
	openAICodexDeviceCodeEndpoint = server.URL + "/api/accounts/deviceauth/usercode"
	openAICodexDeviceTokenEndpoint = server.URL + "/api/accounts/deviceauth/token"
	openAICodexOAuthTokenEndpoint = server.URL + "/oauth/token"
	defer func() {
		openAICodexDeviceCodeEndpoint = oldUserCodeEndpoint
		openAICodexDeviceTokenEndpoint = oldDeviceTokenEndpoint
		openAICodexOAuthTokenEndpoint = oldOAuthTokenEndpoint
	}()

	path := filepath.Join(t.TempDir(), "codex_auth.json")
	_, err := runCodexOAuthDeviceLoginWithWorkspaces(context.Background(), io.Discard, path, server.Client(), []string{"workspace-allowed"})
	if err == nil || !strings.Contains(err.Error(), "workspace-allowed") {
		t.Fatalf("expected workspace mismatch, got %v", err)
	}
	if !sawExchange {
		t.Fatalf("expected token exchange before local workspace validation")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("auth file should not be written on workspace mismatch, stat=%v", statErr)
	}
}

func TestCodexOAuthTokenSourceRejectsWorkspaceMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex_auth.json")
	if err := saveCodexOAuthAuthFile(path, codexOAuthTokens{
		AccessToken:  testCodexOAuthWorkspaceJWT(time.Now().Add(time.Hour), "workspace-denied"),
		RefreshToken: "refresh-live",
	}); err != nil {
		t.Fatalf("save auth file: %v", err)
	}
	source := NewCodexOAuthTokenSourceWithWorkspaceIDs(path, nil, []string{"workspace-allowed", "workspace-other"})
	_, err := source.AccessToken(context.Background())
	if err == nil || !strings.Contains(err.Error(), "workspace-allowed") {
		t.Fatalf("expected workspace mismatch, got %v", err)
	}
}

func TestPollCodexOAuthDeviceCodeHandlesTwoHundredError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"error":"access_denied","error_description":"denied"}`))
	}))
	defer server.Close()

	oldEndpoint := openAICodexDeviceTokenEndpoint
	openAICodexDeviceTokenEndpoint = server.URL
	defer func() {
		openAICodexDeviceTokenEndpoint = oldEndpoint
	}()

	_, err := pollCodexOAuthDeviceCode(context.Background(), server.Client(), codexOAuthDeviceCode{
		DeviceAuthID: "device-1",
		UserCode:     "ABCD",
		Interval:     time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected access_denied error, got %v", err)
	}
}

func TestPollCodexOAuthDeviceCodeRejectsMalformedHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	oldEndpoint := openAICodexDeviceTokenEndpoint
	openAICodexDeviceTokenEndpoint = server.URL
	defer func() {
		openAICodexDeviceTokenEndpoint = oldEndpoint
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := pollCodexOAuthDeviceCode(ctx, server.Client(), codexOAuthDeviceCode{
		DeviceAuthID: "device-1",
		UserCode:     "ABCD",
		Interval:     time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "Bad Request") {
		t.Fatalf("expected immediate HTTP error, got %v", err)
	}
}

func TestPollCodexOAuthDeviceCodeRetriesRequestTimeout(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("content-type", "application/json")
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"detail":"Request timeout"}`))
			return
		}
		_, _ = w.Write([]byte(`{"authorization_code":"auth-code-1","code_verifier":"verifier-1"}`))
	}))
	defer server.Close()

	oldEndpoint := openAICodexDeviceTokenEndpoint
	openAICodexDeviceTokenEndpoint = server.URL
	defer func() {
		openAICodexDeviceTokenEndpoint = oldEndpoint
	}()

	token, err := pollCodexOAuthDeviceCode(context.Background(), server.Client(), codexOAuthDeviceCode{
		DeviceAuthID: "device-1",
		UserCode:     "ABCD",
		Interval:     time.Millisecond,
	})
	if err != nil {
		t.Fatalf("pollCodexOAuthDeviceCode: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected retry after request timeout, got %d attempts", attempts)
	}
	if token.AuthorizationCode != "auth-code-1" || token.CodeVerifier != "verifier-1" {
		t.Fatalf("unexpected device token: %#v", token)
	}
}

func TestPollCodexOAuthDeviceCodeRetriesDeviceAuthorizationUnknown(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("content-type", "application/json")
		if attempts == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"message":"Device authorization is unknown. Please try again.","type":"invalid_request_error","code":"deviceauth_authorization_unknown"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"authorization_code":"auth-code-1","code_verifier":"verifier-1"}`))
	}))
	defer server.Close()

	oldEndpoint := openAICodexDeviceTokenEndpoint
	openAICodexDeviceTokenEndpoint = server.URL
	defer func() {
		openAICodexDeviceTokenEndpoint = oldEndpoint
	}()

	token, err := pollCodexOAuthDeviceCode(context.Background(), server.Client(), codexOAuthDeviceCode{
		DeviceAuthID: "device-1",
		UserCode:     "ABCD",
		Interval:     time.Millisecond,
	})
	if err != nil {
		t.Fatalf("pollCodexOAuthDeviceCode: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected retry after device authorization unknown, got %d attempts", attempts)
	}
	if token.AuthorizationCode != "auth-code-1" || token.CodeVerifier != "verifier-1" {
		t.Fatalf("unexpected device token: %#v", token)
	}
}

func TestImportCodexCLIOAuthAuthFileCopiesUsableTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	source := codexCLIOAuthAuthFilePath()
	dest := filepath.Join(home, "kernforge", "codex_auth.json")
	if err := saveCodexOAuthAuthFile(source, codexOAuthTokens{
		AccessToken:  testCodexOAuthJWT(time.Now().Add(time.Hour)),
		RefreshToken: "refresh-1",
		AccountID:    "account-1",
	}); err != nil {
		t.Fatalf("save source auth: %v", err)
	}

	if err := importCodexCLIOAuthAuthFile(dest); err != nil {
		t.Fatalf("importCodexCLIOAuthAuthFile: %v", err)
	}
	if !codexOAuthAuthFileUsable(dest) {
		t.Fatalf("expected imported auth file to be usable")
	}
	_, auth, err := readCodexOAuthAuthFile(dest)
	if err != nil {
		t.Fatalf("read imported auth file: %v", err)
	}
	if auth.Tokens.RefreshToken != "refresh-1" || auth.Tokens.AccountID != "account-1" {
		t.Fatalf("expected imported tokens to preserve refresh/account metadata, got %#v", auth.Tokens)
	}
}

func TestExchangeCodexOAuthAuthorizationCodeRetriesRequestTimeout(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("content-type", "application/json")
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"detail":"Request timeout"}`))
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("code_verifier"); got != "verifier-1" {
			t.Fatalf("unexpected verifier: %q", got)
		}
		_, _ = w.Write([]byte(`{"access_token":"access-live","refresh_token":"refresh-live"}`))
	}))
	defer server.Close()

	oldEndpoint := openAICodexOAuthTokenEndpoint
	openAICodexOAuthTokenEndpoint = server.URL
	defer func() {
		openAICodexOAuthTokenEndpoint = oldEndpoint
	}()

	tokens, err := exchangeCodexOAuthAuthorizationCode(context.Background(), server.Client(), "auth-code-1", "verifier-1")
	if err != nil {
		t.Fatalf("exchangeCodexOAuthAuthorizationCode: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected retry after request timeout, got %d attempts", attempts)
	}
	if tokens.AccessToken != "access-live" || tokens.RefreshToken != "refresh-live" {
		t.Fatalf("unexpected tokens: %#v", tokens)
	}
}

func testCodexOAuthJWT(expiresAt time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, expiresAt.Unix())))
	return "header." + payload + ".signature"
}

func testCodexOAuthWorkspaceJWT(expiresAt time.Time, workspaceID string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d,"https://api.openai.com/auth":{"chatgpt_account_id":%q}}`, expiresAt.Unix(), workspaceID)))
	return "header." + payload + ".signature"
}
