package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	openCodeDefaultModel   = "opencode/gpt-5.5"
	openCodeGoDefaultModel = "opencode-go/deepseek-v4-pro"
)

type OpenCodeClient struct {
	apiKey     string
	baseURL    string
	provider   string
	httpClient *http.Client
}

type OpenCodeModelInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Object  string `json:"object,omitempty"`
	Created int64  `json:"created,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

func NewOpenCodeClient(baseURL, apiKey string) *OpenCodeClient {
	return &OpenCodeClient{
		apiKey:     strings.TrimSpace(apiKey),
		baseURL:    normalizeOpenCodeBaseURL(baseURL),
		provider:   "opencode",
		httpClient: &http.Client{},
	}
}

func NewOpenCodeGoClient(baseURL, apiKey string) *OpenCodeClient {
	return &OpenCodeClient{
		apiKey:     strings.TrimSpace(apiKey),
		baseURL:    normalizeOpenCodeGoBaseURL(baseURL),
		provider:   "opencode-go",
		httpClient: &http.Client{},
	}
}

func (c *OpenCodeClient) Name() string {
	return c.providerName()
}

func (c *OpenCodeClient) providerName() string {
	provider := normalizeProviderName(c.provider)
	if provider == "" {
		return "opencode"
	}
	return provider
}

func (c *OpenCodeClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	provider := c.providerName()
	model := openCodeAPIModelID(req.Model)
	if model == "" {
		model = openCodeAPIModelID(defaultOpenCodeModelForProvider(provider))
	}
	routedReq := req
	routedReq.Model = model
	if routedReq.JSONMode && openCodeShouldOmitJSONMode(provider, model) {
		routedReq.JSONMode = false
	}

	switch openCodeEndpointKindForProvider(provider, model) {
	case "responses":
		return c.completeResponses(ctx, routedReq)
	case "messages":
		client := NewAnthropicClient(c.baseURL, c.apiKey)
		client.httpClient = c.httpClient
		client.headers = map[string]string{
			"authorization": "Bearer " + c.apiKey,
		}
		resp, err := client.Complete(ctx, routedReq)
		if err != nil {
			return ChatResponse{}, retagProviderError(err, provider)
		}
		return resp, nil
	case "chat":
		client := NewOpenAIClient(c.baseURL, c.apiKey)
		client.httpClient = c.httpClient
		resp, err := client.Complete(ctx, routedReq)
		if err != nil {
			if routedReq.JSONMode && openCodeShouldRetryWithoutJSONMode(err) {
				fallbackReq := routedReq
				fallbackReq.JSONMode = false
				resp, retryErr := client.Complete(ctx, fallbackReq)
				if retryErr == nil {
					return resp, nil
				}
			}
			return ChatResponse{}, retagProviderError(err, provider)
		}
		return resp, nil
	case "google":
		return ChatResponse{}, fmt.Errorf("%s model %q uses the Google model endpoint, which Kernforge does not support yet; choose a GPT, Claude, GLM, Kimi, MiniMax, Qwen, or other chat-completions model", provider, normalizeOpenCodeModelForProvider(provider, model))
	default:
		return ChatResponse{}, fmt.Errorf("unsupported %s model endpoint for %q", provider, normalizeOpenCodeModelForProvider(provider, model))
	}
}

func (c *OpenCodeClient) completeResponses(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	payload, err := buildOpenCodeResponsesPayload(req)
	if err != nil {
		return ChatResponse{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ChatResponse{}, err
	}

	provider := c.providerName()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, openCodeProviderAPIURL(provider, c.baseURL, "/v1/responses"), bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResponse{}, err
	}
	if resp.StatusCode >= 300 {
		return ChatResponse{}, newProviderHTTPError(provider, resp.StatusCode, resp.Status, data, summarizeOpenAIRequestBody(body))
	}

	decoded := openCodeResponsesPayload{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ChatResponse{}, err
	}
	if decoded.Error != nil {
		return ChatResponse{}, newProviderMessageError(provider, decoded.Error.Message, decoded.Error.Type, decoded.Error.Param, decoded.Error.Code, data)
	}

	out := Message{Role: "assistant"}
	if strings.TrimSpace(decoded.OutputText) != "" {
		out.Text = decoded.OutputText
	}
	for _, item := range decoded.Output {
		switch item.Type {
		case "message":
			if strings.TrimSpace(out.Text) == "" {
				out.Text = extractOpenAIMessageText(item.Content)
			}
		case "function_call":
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:        firstNonEmptyTrimmed(item.CallID, item.ID),
				Name:      item.Name,
				Arguments: normalizeOpenCodeResponsesArguments(item.Arguments),
			})
		}
	}

	if strings.TrimSpace(out.Text) == "" && len(out.ToolCalls) == 0 {
		return ChatResponse{}, newProviderMessageError(provider, "empty responses output", "", "", nil, data)
	}
	if req.OnTextDelta != nil && strings.TrimSpace(out.Text) != "" && len(out.ToolCalls) == 0 {
		req.OnTextDelta(out.Text)
	}

	return ChatResponse{
		Message:    out,
		StopReason: normalizeStopReason(decoded.Status),
	}, nil
}

type openCodeResponsesPayload struct {
	OutputText string `json:"output_text,omitempty"`
	Status     string `json:"status,omitempty"`
	Output     []struct {
		Type      string          `json:"type"`
		ID        string          `json:"id,omitempty"`
		CallID    string          `json:"call_id,omitempty"`
		Name      string          `json:"name,omitempty"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
		Content   json.RawMessage `json:"content,omitempty"`
	} `json:"output,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type,omitempty"`
		Param   string `json:"param,omitempty"`
		Code    any    `json:"code,omitempty"`
	} `json:"error,omitempty"`
}

func buildOpenCodeResponsesPayload(req ChatRequest) (map[string]any, error) {
	payload := map[string]any{
		"model": openCodeAPIModelID(req.Model),
	}
	if strings.TrimSpace(req.System) != "" {
		payload["instructions"] = strings.TrimSpace(req.System)
	}
	if req.MaxTokens > 0 {
		payload["max_output_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		payload["temperature"] = req.Temperature
	}

	input := make([]any, 0, len(req.Messages))
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			if strings.TrimSpace(msg.Text) != "" {
				if existing, ok := payload["instructions"].(string); ok && strings.TrimSpace(existing) != "" {
					payload["instructions"] = existing + "\n\n" + strings.TrimSpace(msg.Text)
				} else {
					payload["instructions"] = strings.TrimSpace(msg.Text)
				}
			}
		case "user":
			content, err := openCodeResponsesUserContent(req.WorkingDir, msg)
			if err != nil {
				return nil, err
			}
			if len(content) == 0 {
				continue
			}
			input = append(input, map[string]any{
				"role":    "user",
				"content": content,
			})
		case "assistant":
			if strings.TrimSpace(msg.Text) != "" {
				input = append(input, map[string]any{
					"role": "assistant",
					"content": []map[string]any{{
						"type": "output_text",
						"text": msg.Text,
					}},
				})
			}
			for _, tc := range msg.ToolCalls {
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.Name,
					"arguments": normalizeOpenAIToolCallArguments(tc.Arguments),
				})
			}
		case "tool":
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  msg.Text,
			})
		}
	}
	payload["input"] = input

	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.InputSchema,
			})
		}
		payload["tools"] = tools
	}

	return payload, nil
}

func openCodeResponsesUserContent(baseDir string, msg Message) ([]map[string]any, error) {
	content := make([]map[string]any, 0, len(msg.Images)+1)
	if strings.TrimSpace(msg.Text) != "" {
		content = append(content, map[string]any{
			"type": "input_text",
			"text": msg.Text,
		})
	}
	if len(msg.Images) == 0 {
		return content, nil
	}
	encodedImages, err := encodeMessageImages(baseDir, msg.Images)
	if err != nil {
		return nil, err
	}
	for _, image := range encodedImages {
		content = append(content, map[string]any{
			"type":      "input_image",
			"image_url": imageDataURI(image),
		})
	}
	return content, nil
}

func normalizeOpenCodeResponsesArguments(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "{}"
	}
	var direct string
	if err := json.Unmarshal(trimmed, &direct); err == nil {
		return normalizeOpenAIToolCallArguments(direct)
	}
	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err == nil {
		if data, marshalErr := json.Marshal(decoded); marshalErr == nil {
			return string(data)
		}
	}
	return normalizeOpenAIToolCallArguments(string(trimmed))
}

func FetchOpenCodeModels(ctx context.Context, baseURL, apiKey string) ([]OpenCodeModelInfo, string, error) {
	return fetchOpenCodeModelsForProvider(ctx, "opencode", baseURL, apiKey)
}

func FetchOpenCodeGoModels(ctx context.Context, baseURL, apiKey string) ([]OpenCodeModelInfo, string, error) {
	return fetchOpenCodeModelsForProvider(ctx, "opencode-go", baseURL, apiKey)
}

func fetchOpenCodeModelsForProvider(ctx context.Context, provider, baseURL, apiKey string) ([]OpenCodeModelInfo, string, error) {
	provider = normalizeProviderName(provider)
	normalized := normalizeOpenCodeProviderBaseURL(provider, baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, openCodeProviderAPIURL(provider, normalized, "/v1/models"), nil)
	if err != nil {
		return nil, normalized, err
	}
	if strings.TrimSpace(apiKey) != "" {
		httpReq.Header.Set("authorization", "Bearer "+strings.TrimSpace(apiKey))
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, normalized, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, normalized, err
	}
	if resp.StatusCode >= 300 {
		return nil, normalized, newProviderHTTPError(provider, resp.StatusCode, resp.Status, data, "")
	}

	var decoded struct {
		Data  []OpenCodeModelInfo `json:"data"`
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type,omitempty"`
			Param   string `json:"param,omitempty"`
			Code    any    `json:"code,omitempty"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, normalized, err
	}
	if decoded.Error != nil {
		return nil, normalized, newProviderMessageError(provider, decoded.Error.Message, decoded.Error.Type, decoded.Error.Param, decoded.Error.Code, data)
	}
	return decoded.Data, normalized, nil
}

func normalizeOpenCodeBaseURL(baseURL string) string {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = "https://opencode.ai/zen"
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	base = strings.TrimRight(base, "/")
	lower := strings.ToLower(base)
	for _, suffix := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/models"} {
		if strings.HasSuffix(lower, suffix) {
			base = base[:len(base)-len(suffix)]
			lower = strings.ToLower(base)
			break
		}
	}
	if strings.HasSuffix(lower, "/v1") {
		base = base[:len(base)-len("/v1")]
	}
	return strings.TrimRight(base, "/")
}

func normalizeOpenCodeGoBaseURL(baseURL string) string {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = "https://opencode.ai/zen/go"
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	base = strings.TrimRight(base, "/")
	lower := strings.ToLower(base)
	for _, suffix := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/models"} {
		if strings.HasSuffix(lower, suffix) {
			base = base[:len(base)-len(suffix)]
			lower = strings.ToLower(base)
			break
		}
	}
	if strings.HasSuffix(lower, "/v1") {
		base = base[:len(base)-len("/v1")]
	}
	return strings.TrimRight(base, "/")
}

func normalizeOpenCodeProviderBaseURL(provider, baseURL string) string {
	if normalizeProviderName(provider) == "opencode-go" {
		return normalizeOpenCodeGoBaseURL(baseURL)
	}
	return normalizeOpenCodeBaseURL(baseURL)
}

func openCodeAPIURL(baseURL, path string) string {
	return openCodeProviderAPIURL("opencode", baseURL, path)
}

func openCodeProviderAPIURL(provider, baseURL, path string) string {
	base := normalizeOpenCodeProviderBaseURL(provider, baseURL)
	normalizedPath := "/" + strings.TrimLeft(path, "/")
	lowerBase := strings.ToLower(base)
	lowerPath := strings.ToLower(normalizedPath)
	if strings.HasSuffix(lowerBase, lowerPath) {
		return base
	}
	if strings.HasSuffix(lowerBase, "/v1") && strings.HasPrefix(lowerPath, "/v1/") {
		return base + normalizedPath[len("/v1"):]
	}
	return base + normalizedPath
}

func normalizeOpenCodeModel(model string) string {
	return normalizeOpenCodeModelForProvider("opencode", model)
}

func normalizeOpenCodeGoModel(model string) string {
	return normalizeOpenCodeModelForProvider("opencode-go", model)
}

func normalizeOpenCodeModelForProvider(provider, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return defaultOpenCodeModelForProvider(provider)
	}
	prefix := openCodeModelPrefix(provider)
	if strings.Contains(model, "/") {
		parts := strings.SplitN(model, "/", 2)
		if strings.TrimSpace(parts[1]) == "" {
			return defaultOpenCodeModelForProvider(provider)
		}
		return prefix + "/" + strings.TrimSpace(parts[1])
	}
	return prefix + "/" + model
}

func normalizeOpenCodeModelAllowEmpty(model string) string {
	return normalizeOpenCodeModelAllowEmptyForProvider("opencode", model)
}

func normalizeOpenCodeModelAllowEmptyForProvider(provider, model string) string {
	if strings.TrimSpace(model) == "" {
		return ""
	}
	return normalizeOpenCodeModelForProvider(provider, model)
}

func openCodeModelPrefix(provider string) string {
	if normalizeProviderName(provider) == "opencode-go" {
		return "opencode-go"
	}
	return "opencode"
}

func defaultOpenCodeModelForProvider(provider string) string {
	if normalizeProviderName(provider) == "opencode-go" {
		return openCodeGoDefaultModel
	}
	return openCodeDefaultModel
}

func openCodeAPIModelID(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if strings.Contains(model, "/") {
		parts := strings.SplitN(model, "/", 2)
		return strings.TrimSpace(parts[1])
	}
	return model
}

func normalizeOpenCodeModelInfos(models []OpenCodeModelInfo) []OpenCodeModelInfo {
	out := make([]OpenCodeModelInfo, 0, len(models))
	seen := map[string]bool{}
	for _, model := range models {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" {
			continue
		}
		key := strings.ToLower(openCodeAPIModelID(model.ID))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, model)
	}
	return out
}

func openCodeModelInList(models []OpenCodeModelInfo, model string) bool {
	return openCodeModelInListForProvider("opencode", models, model)
}

func openCodeModelInListForProvider(provider string, models []OpenCodeModelInfo, model string) bool {
	target := strings.ToLower(openCodeAPIModelID(model))
	if target == "" {
		return false
	}
	for _, item := range normalizeOpenCodeModelInfos(models) {
		if strings.EqualFold(openCodeAPIModelID(item.ID), target) {
			return true
		}
	}
	return false
}

func preferredOpenCodeModel(models []OpenCodeModelInfo) string {
	return preferredOpenCodeModelForProvider("opencode", models)
}

func preferredOpenCodeModelForProvider(provider string, models []OpenCodeModelInfo) string {
	models = normalizeOpenCodeModelInfos(models)
	if len(models) == 0 {
		return defaultOpenCodeModelForProvider(provider)
	}
	preferred := preferredOpenCodeModelIDs(provider)
	for _, want := range preferred {
		for _, item := range models {
			if strings.EqualFold(openCodeAPIModelID(item.ID), want) {
				return normalizeOpenCodeModelForProvider(provider, item.ID)
			}
		}
	}
	return normalizeOpenCodeModelForProvider(provider, models[0].ID)
}

func preferredOpenCodeModelIDs(provider string) []string {
	if normalizeProviderName(provider) == "opencode-go" {
		return []string{
			"deepseek-v4-pro",
			"glm-5.1",
			"kimi-k2.6",
			"qwen3.6-plus",
			"minimax-m2.7",
			"kimi-k2.5",
			"qwen3.5-plus",
			"deepseek-v4-flash",
			"glm-5",
			"minimax-m2.5",
			"mimo-v2.5-pro",
			"mimo-v2.5",
			"mimo-v2-pro",
			"mimo-v2-omni",
		}
	}
	return []string{
		"gpt-5.5-pro",
		"gpt-5.5",
		"gpt-5.4-pro",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.3-codex",
		"gpt-5.3-codex-spark",
		"gpt-5.2",
		"gpt-5.1-codex",
		"gpt-5.1-codex-max",
		"claude-sonnet-4-6",
		"claude-sonnet-4-5",
		"glm-5.1",
		"minimax-m2.7",
		"kimi-k2.6",
		"qwen3.6-plus",
		"minimax-m2.5-free",
		"big-pickle",
		"nemotron-3-super-free",
	}
}

func openCodeEndpointKind(model string) string {
	return openCodeEndpointKindForProvider("opencode", model)
}

func openCodeEndpointKindForProvider(provider, model string) string {
	model = strings.ToLower(strings.TrimSpace(openCodeAPIModelID(model)))
	if normalizeProviderName(provider) == "opencode-go" {
		switch model {
		case "minimax-m2.7", "minimax-m2.5":
			return "messages"
		default:
			return "chat"
		}
	}
	switch {
	case strings.HasPrefix(model, "gpt-"):
		return "responses"
	case strings.HasPrefix(model, "claude-"):
		return "messages"
	case strings.HasPrefix(model, "gemini-"):
		return "google"
	default:
		return "chat"
	}
}

func openCodeShouldOmitJSONMode(provider, model string) bool {
	provider = normalizeProviderName(provider)
	model = strings.ToLower(strings.TrimSpace(openCodeAPIModelID(model)))
	if provider != "opencode" && provider != "opencode-go" {
		return false
	}
	if strings.HasPrefix(model, "kimi-") || strings.Contains(model, "/kimi-") {
		return true
	}
	return false
}

func openCodeShouldRetryWithoutJSONMode(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "response_format") || strings.Contains(msg, "json_object") {
		return true
	}
	if strings.Contains(msg, "unsupported parameter") || strings.Contains(msg, "unsupported param") {
		return true
	}
	return false
}

func retagProviderError(err error, provider string) error {
	if err == nil {
		return nil
	}
	apiErr, ok := err.(*ProviderAPIError)
	if !ok {
		return err
	}
	copied := *apiErr
	copied.Provider = provider
	return &copied
}
