package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

	client, err = NewProviderClient(Config{Provider: "openai_codex", Model: "gpt-5.5"})
	if err != nil {
		t.Fatalf("NewProviderClient alias: %v", err)
	}
	if client.Name() != "openai-codex" {
		t.Fatalf("expected openai-codex client for alias, got %q", client.Name())
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
	encoded := string(body)
	for _, needle := range []string{`"stream":true`, `"type":"function_call"`, `"call_id":"call_1"`, `"type":"function_call_output"`, `"type":"json_object"`} {
		if !strings.Contains(encoded, needle) {
			t.Fatalf("expected %q in request body %s", needle, encoded)
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

func TestSyncClientFromConfigRefreshesOpenAICodexReviewerEffort(t *testing.T) {
	rt := &runtimeState{
		cfg: Config{
			Provider:        "openai-codex",
			Model:           "gpt-5.5",
			ReasoningEffort: "low",
			PlanReview: &PlanReviewConfig{
				Provider: "openai-codex",
				Model:    "gpt-5.5",
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
	if reviewerClient.reasoningEffort != "low" {
		t.Fatalf("reviewer reasoning effort = %q, want low", reviewerClient.reasoningEffort)
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
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5","supported_in_api":true,"visibility":"list"},{"slug":"hidden","display_name":"Hidden","supported_in_api":true,"visibility":"hidden"}]}`))
	}))
	defer server.Close()

	models, err := FetchOpenAICodexModels(context.Background(), server.URL, staticCodexTokenSource{token: "test-token"}, server.Client())
	if err != nil {
		t.Fatalf("FetchOpenAICodexModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gpt-5.5" || models[0].Name != "GPT-5.5" {
		t.Fatalf("unexpected models: %#v", models)
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

func TestReadOpenAICodexStreamUsesDoneMessageWhenNoDelta(t *testing.T) {
	stream := strings.NewReader("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"done text\"}]}}\n\n")
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.Text != "done text" {
		t.Fatalf("expected done message text, got %q", resp.Message.Text)
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
