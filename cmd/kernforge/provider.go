package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ToolDefinition struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
}

type ToolContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

const (
	messagePhaseCommentary           = "commentary"
	messagePhaseFinalAnswer          = "final_answer"
	messagePhaseFinalAnswerCandidate = "final_answer_candidate"
)

type Message struct {
	Role             string            `json:"role"`
	Phase            string            `json:"phase,omitempty"`
	Text             string            `json:"text,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	Images           []MessageImage    `json:"images,omitempty"`
	ToolCalls        []ToolCall        `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	ToolName         string            `json:"tool_name,omitempty"`
	ToolContentItems []ToolContentItem `json:"tool_content_items,omitempty"`
	ToolMeta         map[string]any    `json:"tool_meta,omitempty"`
	IsError          bool              `json:"is_error,omitempty"`
}

type ChatRequest struct {
	Model           string
	System          string
	Messages        []Message
	Tools           []ToolDefinition
	MaxTokens       int
	Temperature     float64
	ReasoningEffort string
	WorkingDir      string
	JSONMode        bool
	OnTextDelta     func(string)
	OnProgressEvent func(ProgressEvent)
}

type ChatResponse struct {
	Message    Message
	StopReason string
	EndTurn    *bool
	RawBody    string
}

type ProgressEvent struct {
	Kind             string
	Message          string
	Provider         string
	Model            string
	ToolName         string
	ToolCallID       string
	ArgumentsPreview string
	RouteLabel       string
	Stage            string
	Shard            string
	Status           string
	Elapsed          time.Duration
}

type providerErrorBody struct {
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type,omitempty"`
		Param   string `json:"param,omitempty"`
		Code    any    `json:"code,omitempty"`
	} `json:"error,omitempty"`
}

type ProviderAPIError struct {
	Provider       string
	StatusCode     int
	Status         string
	Message        string
	ErrorType      string
	Param          string
	Code           string
	RequestSummary string
	RawBody        string
}

func (e *ProviderAPIError) Error() string {
	provider := strings.TrimSpace(e.Provider)
	if provider == "" {
		provider = "provider"
	}
	if e.StatusCode > 0 || strings.TrimSpace(e.Status) != "" {
		statusText := strings.TrimSpace(e.Status)
		if statusText == "" {
			statusText = strconv.Itoa(e.StatusCode)
		}
		text := fmt.Sprintf("%s API error (%s): %s", provider, statusText, e.Details())
		if strings.TrimSpace(e.RequestSummary) != "" {
			text += " | request=" + strings.TrimSpace(e.RequestSummary)
		}
		return text
	}
	return fmt.Sprintf("%s API error: %s", provider, e.Details())
}

func (e *ProviderAPIError) Details() string {
	parts := []string{}
	if strings.TrimSpace(e.Message) != "" {
		parts = append(parts, strings.TrimSpace(e.Message))
	} else {
		parts = append(parts, "provider returned error")
	}
	if strings.TrimSpace(e.ErrorType) != "" {
		parts = append(parts, "type="+strings.TrimSpace(e.ErrorType))
	}
	if strings.TrimSpace(e.Param) != "" {
		parts = append(parts, "param="+strings.TrimSpace(e.Param))
	}
	if strings.TrimSpace(e.Code) != "" {
		parts = append(parts, "code="+strings.TrimSpace(e.Code))
	}
	detail := strings.Join(parts, " | ")
	rawText := strings.TrimSpace(e.RawBody)
	if strings.EqualFold(strings.TrimSpace(e.Message), "Provider returned error") && rawText != "" && !strings.Contains(rawText, detail) {
		detail += " | raw=" + rawText
	}
	return detail
}

func (e *ProviderAPIError) Retryable() bool {
	return providerErrorLooksRetryable(e.StatusCode, e.ErrorType, e.Message, e.Code, e.RawBody)
}

func newProviderHTTPError(provider string, statusCode int, status string, data []byte, requestSummary string) error {
	message, errorType, param, code, rawBody := decodeProviderErrorPayload(data)
	return &ProviderAPIError{
		Provider:       provider,
		StatusCode:     statusCode,
		Status:         status,
		Message:        message,
		ErrorType:      errorType,
		Param:          param,
		Code:           code,
		RequestSummary: requestSummary,
		RawBody:        rawBody,
	}
}

func newProviderMessageError(provider, message, errorType, param string, code any, raw []byte) error {
	return &ProviderAPIError{
		Provider:  provider,
		Message:   strings.TrimSpace(message),
		ErrorType: strings.TrimSpace(errorType),
		Param:     strings.TrimSpace(param),
		Code:      normalizeProviderErrorCode(code),
		RawBody:   strings.TrimSpace(string(raw)),
	}
}

func decodeProviderErrorPayload(data []byte) (string, string, string, string, string) {
	rawText := strings.TrimSpace(string(data))
	decoded := providerErrorBody{}
	if err := json.Unmarshal(data, &decoded); err == nil && decoded.Error != nil {
		return strings.TrimSpace(decoded.Error.Message), strings.TrimSpace(decoded.Error.Type), strings.TrimSpace(decoded.Error.Param), normalizeProviderErrorCode(decoded.Error.Code), rawText
	}
	if rawText == "" {
		return "empty error response body", "", "", "", rawText
	}
	return rawText, "", "", "", rawText
}

func normalizeProviderErrorCode(code any) string {
	if code == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", code))
}

func providerErrorLooksRetryable(statusCode int, errorType, message, code, raw string) bool {
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	if statusCode >= 500 && statusCode <= 504 {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(strings.Join([]string{errorType, message, code, raw}, " ")))
	retryHints := []string{
		"rate limit",
		"timeout",
		"temporarily unavailable",
		"server error",
		"bad gateway",
		"gateway timeout",
		"service unavailable",
		"overloaded",
		"server_overloaded",
		"server_error",
		"timeout_error",
		"429",
		"500",
		"502",
		"503",
		"504",
	}
	for _, hint := range retryHints {
		if strings.Contains(text, hint) {
			return true
		}
	}
	return false
}

func providerErrorSuggestsJSONModeUnsupported(err error) bool {
	if err == nil {
		return false
	}
	apiErr := &ProviderAPIError{}
	if errors.As(err, &apiErr) {
		msg := strings.ToLower(strings.Join([]string{apiErr.Message, apiErr.ErrorType, apiErr.Param, apiErr.Code, apiErr.RawBody}, " "))
		return providerErrorTextSuggestsJSONModeUnsupported(msg)
	}
	return providerErrorTextSuggestsJSONModeUnsupported(strings.ToLower(err.Error()))
}

func toolOutputForResponses(msg Message) any {
	items := normalizeToolContentItems(msg.ToolContentItems)
	if len(items) == 0 {
		return msg.Text
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		switch strings.TrimSpace(item.Type) {
		case "input_text":
			out = append(out, map[string]any{
				"type": "input_text",
				"text": item.Text,
			})
		case "input_image":
			image := map[string]any{
				"type":      "input_image",
				"image_url": item.ImageURL,
			}
			if strings.TrimSpace(item.Detail) != "" {
				image["detail"] = strings.TrimSpace(item.Detail)
			}
			out = append(out, image)
		}
	}
	if len(out) == 0 {
		return msg.Text
	}
	return out
}

func normalizeToolContentItems(items []ToolContentItem) []ToolContentItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]ToolContentItem, 0, len(items))
	for _, item := range items {
		item.Type = strings.TrimSpace(item.Type)
		switch item.Type {
		case "input_text":
			if strings.TrimSpace(item.Text) == "" {
				continue
			}
			out = append(out, ToolContentItem{
				Type: "input_text",
				Text: item.Text,
			})
		case "input_image":
			if strings.TrimSpace(item.ImageURL) == "" {
				continue
			}
			detail, err := normalizeImageDetail(item.Detail)
			if err != nil {
				detail = ""
			}
			out = append(out, ToolContentItem{
				Type:     "input_image",
				ImageURL: strings.TrimSpace(item.ImageURL),
				Detail:   detail,
			})
		}
	}
	return out
}

func providerErrorTextSuggestsJSONModeUnsupported(msg string) bool {
	msg = strings.ToLower(strings.TrimSpace(msg))
	if strings.Contains(msg, "response_format") || strings.Contains(msg, "json_object") {
		return true
	}
	if strings.Contains(msg, "json") &&
		(strings.Contains(msg, "unsupported parameter") || strings.Contains(msg, "unsupported param")) {
		return true
	}
	return false
}

type ProviderClient interface {
	Name() string
	Complete(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

func NewProviderClient(cfg Config) (ProviderClient, error) {
	switch normalizeProviderName(cfg.Provider) {
	case "anthropic":
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("Anthropic provider selected but no API key was found")
		}
		return NewAnthropicClient(cfg.BaseURL, cfg.APIKey), nil
	case "deepseek":
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("DeepSeek provider selected but no API key was found")
		}
		return NewDeepSeekClient(cfg.BaseURL, cfg.APIKey, cfg.ReasoningEffort), nil
	case "openrouter":
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("OpenRouter provider selected but no API key was found")
		}
		return NewOpenAICompatibleClient("openrouter", cfg.BaseURL, cfg.APIKey), nil
	case "opencode":
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("OpenCode Zen provider selected but no API key was found")
		}
		return NewOpenCodeClient(cfg.BaseURL, cfg.APIKey), nil
	case "opencode-go":
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("OpenCode Go provider selected but no API key was found")
		}
		return NewOpenCodeGoClient(cfg.BaseURL, cfg.APIKey), nil
	case "ollama":
		return NewOllamaClient(cfg.BaseURL, cfg.APIKey), nil
	case "lmstudio", "vllm", "llama.cpp":
		return NewOpenAICompatibleClient(cfg.Provider, cfg.BaseURL, cfg.APIKey), nil
	case "openai-codex":
		return NewOpenAICodexClientWithReasoningEffort(cfg.BaseURL, cfg.ReasoningEffort), nil
	case "codex-cli":
		return NewCodexCLIClient(cfg.CodexCLIPath, cfg.CodexCLIArgs), nil
	case "anthropic-claude-cli":
		return NewClaudeCLIClient(cfg.ClaudeCLIPath, cfg.ClaudeCLIArgs), nil
	case "openai":
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("OpenAI-compatible provider selected but no API key was found")
		}
		return NewOpenAIClient(cfg.BaseURL, cfg.APIKey), nil
	case "openai-compatible":
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("OpenAI-compatible provider selected but no API key was found")
		}
		return NewOpenAICompatibleClient("openai-compatible", cfg.BaseURL, cfg.APIKey), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", cfg.Provider)
	}
}

func normalizeProviderName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex", "codex-cli", "codex_cli", "openai-codex-cli", "openai_codex_cli", "openai codex cli":
		return "codex-cli"
	case "openai-codex", "openai_codex", "openai-codex-subscription", "openai_codex_subscription", "openai codex subscription", "codex-subscription", "codex_subscription":
		return "openai-codex"
	case "openai-api", "openai_api", "openai api":
		return "openai"
	case "anthropic-api", "anthropic_api", "anthropic api":
		return "anthropic"
	case "anthropic-claude-cli", "anthropic_claude_cli", "anthropic claude cli", "claude-cli", "claude_cli", "claude cli", "claude-code-cli", "claude_code_cli", "claude code cli":
		return "anthropic-claude-cli"
	case "deepseek", "deepseek-api", "deepseek_api", "deepseek api":
		return "deepseek"
	case "opencode", "open-code", "open_code", "opencode-zen", "opencode_zen", "opencode zen", "open-code-zen", "open_code_zen", "open code zen":
		return "opencode"
	case "opencode-go", "opencode_go", "opencode go", "open-code-go", "open_code_go", "open code go", "opencodego":
		return "opencode-go"
	case "lmstudio", "lm-studio", "lm_studio", "lm studio":
		return "lmstudio"
	case "vllm", "v-llm", "v_llm":
		return "vllm"
	case "llama.cpp", "llamacpp", "llama-cpp", "llama_cpp", "llama cpp":
		return "llama.cpp"
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

type AnthropicClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	headers    map[string]string
}

func NewAnthropicClient(baseURL, apiKey string) *AnthropicClient {
	return &AnthropicClient{
		apiKey:     apiKey,
		baseURL:    normalizeAnthropicBaseURL(baseURL),
		httpClient: &http.Client{},
	}
}

func (c *AnthropicClient) Name() string {
	return "anthropic"
}

func (c *AnthropicClient) ModelRouteMetadata() ModelRouteMetadata {
	if c == nil {
		return ModelRouteMetadata{Provider: "anthropic"}
	}
	return ModelRouteMetadata{Provider: "anthropic", BaseURL: c.baseURL}
}

func (c *AnthropicClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	type anthropicTool struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"input_schema"`
	}
	type anthropicContentBlock struct {
		Type      string `json:"type"`
		Text      string `json:"text,omitempty"`
		ID        string `json:"id,omitempty"`
		Name      string `json:"name,omitempty"`
		ToolUseID string `json:"tool_use_id,omitempty"`
		Content   string `json:"content,omitempty"`
		IsError   bool   `json:"is_error,omitempty"`
		Input     any    `json:"input,omitempty"`
		Source    any    `json:"source,omitempty"`
	}
	type anthropicMessage struct {
		Role    string                  `json:"role"`
		Content []anthropicContentBlock `json:"content"`
	}
	type anthropicRequest struct {
		Model       string             `json:"model"`
		System      string             `json:"system,omitempty"`
		MaxTokens   int                `json:"max_tokens"`
		Temperature float64            `json:"temperature,omitempty"`
		Messages    []anthropicMessage `json:"messages"`
		Tools       []anthropicTool    `json:"tools,omitempty"`
	}
	type anthropicResponse struct {
		Content    []anthropicContentBlock `json:"content"`
		StopReason string                  `json:"stop_reason"`
		Error      *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	payload := anthropicRequest{
		Model:       req.Model,
		System:      req.System,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Messages:    make([]anthropicMessage, 0, len(req.Messages)),
		Tools:       make([]anthropicTool, 0, len(req.Tools)),
	}

	for _, tool := range req.Tools {
		payload.Tools = append(payload.Tools, anthropicTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}

	for _, msg := range req.Messages {
		blocked := anthropicMessage{Role: msg.Role}
		switch msg.Role {
		case "system":
			continue
		case "user":
			encodedImages, err := encodeMessageImages(req.WorkingDir, msg.Images)
			if err != nil {
				return ChatResponse{}, err
			}
			for _, image := range encodedImages {
				blocked.Content = append(blocked.Content, anthropicContentBlock{
					Type: "image",
					Source: map[string]any{
						"type":       "base64",
						"media_type": image.MediaType,
						"data":       image.Data,
					},
				})
			}
			if strings.TrimSpace(msg.Text) != "" {
				blocked.Content = append(blocked.Content, anthropicContentBlock{Type: "text", Text: msg.Text})
			}
		case "assistant":
			if strings.TrimSpace(msg.Text) != "" {
				blocked.Content = append(blocked.Content, anthropicContentBlock{Type: "text", Text: msg.Text})
			}
			for _, tc := range msg.ToolCalls {
				var input any
				if tc.Arguments != "" {
					if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
						input = map[string]any{"raw": tc.Arguments}
					}
				} else {
					input = map[string]any{}
				}
				blocked.Content = append(blocked.Content, anthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
			}
		case "tool":
			blocked.Role = "user"
			blocked.Content = append(blocked.Content, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   msg.Text,
				IsError:   msg.IsError,
			})
		default:
			continue
		}
		if len(blocked.Content) == 0 {
			continue
		}
		payload.Messages = append(payload.Messages, blocked)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ChatResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	for key, value := range c.headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			httpReq.Header.Set(key, value)
		}
	}

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
		return ChatResponse{}, newProviderHTTPError("anthropic", resp.StatusCode, resp.Status, data, "")
	}

	var decoded anthropicResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ChatResponse{}, err
	}
	if decoded.Error != nil {
		return ChatResponse{}, newProviderMessageError("anthropic", decoded.Error.Message, "", "", nil, data)
	}

	out := Message{Role: "assistant"}
	var texts []string
	for _, block := range decoded.Content {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				texts = append(texts, block.Text)
			}
		case "tool_use":
			args, _ := json.Marshal(block.Input)
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(args),
			})
		}
	}
	out.Text = strings.Join(texts, "\n")

	return ChatResponse{
		Message:    out,
		StopReason: decoded.StopReason,
	}, nil
}

type OpenAIClient struct {
	apiKey     string
	baseURL    string
	name       string
	reasoning  string
	httpClient *http.Client
}

func NewOpenAIClient(baseURL, apiKey string) *OpenAIClient {
	return &OpenAIClient{
		apiKey:     apiKey,
		baseURL:    normalizeOpenAIBaseURL(baseURL),
		name:       "openai",
		httpClient: &http.Client{},
	}
}

func NewOpenAICompatibleClient(provider, baseURL, apiKey string) *OpenAIClient {
	provider = normalizeProviderName(provider)
	if provider == "" {
		provider = "openai-compatible"
	}
	return &OpenAIClient{
		apiKey:     strings.TrimSpace(apiKey),
		baseURL:    normalizeProviderBaseURL(provider, baseURL),
		name:       provider,
		httpClient: &http.Client{},
	}
}

func NewDeepSeekClient(baseURL, apiKey string, reasoningEffort string) *OpenAIClient {
	return &OpenAIClient{
		apiKey:     strings.TrimSpace(apiKey),
		baseURL:    normalizeDeepSeekBaseURL(baseURL),
		name:       "deepseek",
		reasoning:  normalizeReasoningEffort(reasoningEffort),
		httpClient: &http.Client{},
	}
}

func (c *OpenAIClient) Name() string {
	if c != nil && strings.TrimSpace(c.name) != "" {
		return strings.TrimSpace(c.name)
	}
	return "openai"
}

func (c *OpenAIClient) ModelRouteMetadata() ModelRouteMetadata {
	if c == nil {
		return ModelRouteMetadata{Provider: "openai"}
	}
	meta := ModelRouteMetadata{Provider: c.Name(), BaseURL: c.baseURL}
	if strings.EqualFold(meta.Provider, "deepseek") {
		meta.ReasoningEffort = c.reasoning
	}
	return meta
}

func (c *OpenAIClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	providerName := c.Name()
	type openAIToolCall struct {
		ID       string `json:"id,omitempty"`
		Type     string `json:"type,omitempty"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	type openAIMessage struct {
		Role             string           `json:"role"`
		Content          any              `json:"content,omitempty"`
		ReasoningContent string           `json:"reasoning_content,omitempty"`
		Name             string           `json:"name,omitempty"`
		ToolCallID       string           `json:"tool_call_id,omitempty"`
		ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
	}
	type openAIRequest struct {
		Model           string          `json:"model"`
		Messages        []openAIMessage `json:"messages"`
		Temperature     float64         `json:"temperature,omitempty"`
		MaxTokens       int             `json:"max_tokens,omitempty"`
		ReasoningEffort string          `json:"reasoning_effort,omitempty"`
		Thinking        any             `json:"thinking,omitempty"`
		ResponseFormat  any             `json:"response_format,omitempty"`
		Tools           []struct {
			Type     string `json:"type"`
			Function struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools,omitempty"`
		ToolChoice string `json:"tool_choice,omitempty"`
		Stream     bool   `json:"stream,omitempty"`
	}
	type openAIResponse struct {
		Choices []struct {
			Message struct {
				Content          json.RawMessage  `json:"content"`
				ReasoningContent string           `json:"reasoning_content,omitempty"`
				ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type,omitempty"`
			Param   string `json:"param,omitempty"`
			Code    any    `json:"code,omitempty"`
		} `json:"error,omitempty"`
	}

	payload := openAIRequest{
		Model:       req.Model,
		Messages:    make([]openAIMessage, 0, len(req.Messages)+1),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	if req.JSONMode {
		payload.ResponseFormat = map[string]string{"type": "json_object"}
	}
	if strings.EqualFold(providerName, "deepseek") {
		effort, err := normalizeDeepSeekReasoningEffort(firstNonBlankString(req.ReasoningEffort, c.reasoning))
		if err != nil {
			return ChatResponse{}, err
		}
		if effort != "" {
			payload.ReasoningEffort = effort
			payload.Thinking = map[string]string{"type": "enabled"}
		}
	}

	if strings.TrimSpace(req.System) != "" {
		payload.Messages = append(payload.Messages, openAIMessage{
			Role:    "system",
			Content: req.System,
		})
	}

	for _, msg := range ensureOpenAIToolCallResponses(req.Messages) {
		switch msg.Role {
		case "system":
			continue
		case "user":
			content := any(msg.Text)
			if len(msg.Images) > 0 {
				encodedImages, err := encodeMessageImages(req.WorkingDir, msg.Images)
				if err != nil {
					return ChatResponse{}, err
				}
				parts := make([]map[string]any, 0, len(encodedImages)+1)
				if strings.TrimSpace(msg.Text) != "" {
					parts = append(parts, map[string]any{
						"type": "text",
						"text": msg.Text,
					})
				}
				for _, image := range encodedImages {
					parts = append(parts, map[string]any{
						"type": "image_url",
						"image_url": map[string]any{
							"url": imageDataURI(image),
						},
					})
				}
				content = parts
			}
			payload.Messages = append(payload.Messages, openAIMessage{Role: "user", Content: content})
		case "assistant":
			assistantContent := assistantMessageContent(msg.Text, len(msg.ToolCalls) > 0)
			if strings.EqualFold(providerName, "deepseek") && assistantContent == nil {
				assistantContent = ""
			}
			item := openAIMessage{Role: "assistant", Content: assistantContent}
			if strings.EqualFold(providerName, "deepseek") && strings.TrimSpace(msg.ReasoningContent) != "" {
				item.ReasoningContent = strings.TrimSpace(msg.ReasoningContent)
			}
			for _, tc := range msg.ToolCalls {
				call := openAIToolCall{ID: tc.ID, Type: "function"}
				call.Function.Name = tc.Name
				call.Function.Arguments = normalizeOpenAIToolCallArguments(tc.Arguments)
				item.ToolCalls = append(item.ToolCalls, call)
			}
			payload.Messages = append(payload.Messages, item)
		case "tool":
			payload.Messages = append(payload.Messages, openAIMessage{
				Role:       "tool",
				Content:    msg.Text,
				Name:       msg.ToolName,
				ToolCallID: msg.ToolCallID,
			})
		}
	}

	for _, tool := range req.Tools {
		item := struct {
			Type     string `json:"type"`
			Function struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
			} `json:"function"`
		}{Type: "function"}
		item.Function.Name = tool.Name
		item.Function.Description = tool.Description
		item.Function.Parameters = tool.InputSchema
		payload.Tools = append(payload.Tools, item)
	}
	if len(payload.Tools) > 0 {
		payload.ToolChoice = "auto"
	}
	if req.OnTextDelta != nil || req.OnProgressEvent != nil {
		payload.Stream = true
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ChatResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, openAICompatibleAPIURL(providerName, c.baseURL, "/v1/chat/completions"), bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("content-type", "application/json")
	if strings.TrimSpace(c.apiKey) != "" {
		httpReq.Header.Set("authorization", "Bearer "+strings.TrimSpace(c.apiKey))
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return ChatResponse{}, err
		}
		apiErr := newProviderHTTPError(providerName, resp.StatusCode, resp.Status, data, summarizeOpenAIRequestBody(body))
		if req.JSONMode && providerErrorSuggestsJSONModeUnsupported(apiErr) {
			fallbackReq := req
			fallbackReq.JSONMode = false
			return c.Complete(ctx, fallbackReq)
		}
		return ChatResponse{}, apiErr
	}

	if payload.Stream {
		streamResp, err := readOpenAIStream(ctx, providerName, resp.Body, req.OnTextDelta, req.OnProgressEvent, len(req.Tools) > 0)
		if err != nil {
			return ChatResponse{}, err
		}
		if shouldFallbackAfterOpenAIStream(streamResp) {
			fallbackReq := req
			fallbackReq.OnTextDelta = nil
			fallbackReq.OnProgressEvent = nil
			fallbackResp, err := c.Complete(ctx, fallbackReq)
			if err != nil {
				return ChatResponse{}, err
			}
			fallbackResp.StopReason = markStopReasonAsStreamFallback(streamResp, fallbackResp)
			return fallbackResp, nil
		}
		return streamResp, nil
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResponse{}, err
	}

	var decoded openAIResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ChatResponse{}, err
	}
	if decoded.Error != nil {
		apiErr := newProviderMessageError(providerName, decoded.Error.Message, decoded.Error.Type, decoded.Error.Param, decoded.Error.Code, data)
		if req.JSONMode && providerErrorSuggestsJSONModeUnsupported(apiErr) {
			fallbackReq := req
			fallbackReq.JSONMode = false
			return c.Complete(ctx, fallbackReq)
		}
		return ChatResponse{}, apiErr
	}
	if len(decoded.Choices) == 0 {
		return ChatResponse{}, newProviderMessageError(providerName, "empty choices", "", "", nil, nil)
	}

	choice := decoded.Choices[0]
	out := Message{Role: "assistant", Text: extractOpenAIMessageText(choice.Message.Content)}
	for _, tc := range choice.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	if shouldPreserveOpenAIReasoningContent(providerName, out.Text, out.ToolCalls) {
		out.ReasoningContent = strings.TrimSpace(choice.Message.ReasoningContent)
	}

	return ChatResponse{
		Message:    out,
		StopReason: choice.FinishReason,
		RawBody:    string(data),
	}, nil
}

func readOpenAIStream(ctx context.Context, providerName string, body io.ReadCloser, onTextDelta func(string), onProgressEvent func(ProgressEvent), bufferLeadingText bool) (ChatResponse, error) {
	providerName = normalizeProviderName(providerName)
	if providerName == "" {
		providerName = "openai"
	}
	type streamToolCallDelta struct {
		Index    int    `json:"index"`
		ID       string `json:"id,omitempty"`
		Type     string `json:"type,omitempty"`
		Function struct {
			Name      string `json:"name,omitempty"`
			Arguments string `json:"arguments,omitempty"`
		} `json:"function,omitempty"`
	}
	type streamChunk struct {
		Choices []struct {
			Delta struct {
				Content          json.RawMessage       `json:"content,omitempty"`
				ReasoningContent json.RawMessage       `json:"reasoning_content,omitempty"`
				ToolCalls        []streamToolCallDelta `json:"tool_calls,omitempty"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type,omitempty"`
			Param   string `json:"param,omitempty"`
			Code    any    `json:"code,omitempty"`
		} `json:"error,omitempty"`
	}
	type toolCallAccumulator struct {
		ID           string
		Name         string
		Arguments    strings.Builder
		SeenArgument bool
		Started      bool
		ArgsStarted  bool
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = body.Close()
		case <-done:
		}
	}()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var textBuilder strings.Builder
	var reasoningBuilder strings.Builder
	var pendingText strings.Builder
	toolCalls := map[int]*toolCallAccumulator{}
	stopReason := ""
	sawDone := false
	streamUnlocked := !bufferLeadingText
	sawToolCalls := false
	deltaFilter := hiddenAssistantMarkupDeltaFilter{}
	emitVisibleTextDelta := func(delta string) {
		if onTextDelta == nil || delta == "" {
			return
		}
		if visible := deltaFilter.Push(delta); visible != "" {
			onTextDelta(visible)
		}
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			sawDone = true
			break
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return ChatResponse{}, err
		}
		if chunk.Error != nil {
			return ChatResponse{}, newProviderMessageError(providerName, chunk.Error.Message, chunk.Error.Type, chunk.Error.Param, chunk.Error.Code, []byte(payload))
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				stopReason = choice.FinishReason
			}
			if deltaText := extractOpenAIMessageText(choice.Delta.Content); deltaText != "" {
				textBuilder.WriteString(deltaText)
				if onTextDelta != nil {
					if streamUnlocked && !sawToolCalls {
						emitVisibleTextDelta(deltaText)
					} else if !sawToolCalls {
						pendingText.WriteString(deltaText)
					}
				}
			}
			if shouldCollectOpenAIReasoningContent(providerName) {
				if deltaReasoning := extractOpenAIMessageText(choice.Delta.ReasoningContent); deltaReasoning != "" {
					reasoningBuilder.WriteString(deltaReasoning)
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				sawToolCalls = true
				entry := toolCalls[tc.Index]
				if entry == nil {
					entry = &toolCallAccumulator{}
					toolCalls[tc.Index] = entry
				}
				if tc.ID != "" {
					entry.ID = tc.ID
				}
				if tc.Function.Name != "" {
					entry.Name = tc.Function.Name
					if !entry.Started {
						entry.Started = true
						emitProgressEvent(onProgressEvent, ProgressEvent{
							Kind:       progressKindModelStreamToolCall,
							Provider:   providerName,
							ToolName:   entry.Name,
							ToolCallID: entry.ID,
						})
					}
				}
				if tc.Function.Arguments != "" {
					if !entry.ArgsStarted {
						entry.ArgsStarted = true
						emitProgressEvent(onProgressEvent, ProgressEvent{
							Kind:       progressKindModelStreamToolArgs,
							Provider:   providerName,
							ToolName:   entry.Name,
							ToolCallID: entry.ID,
						})
					}
					entry.Arguments.WriteString(tc.Function.Arguments)
					entry.SeenArgument = true
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				if partial, ok := buildPartialStreamResponse(textBuilder.String(), len(toolCalls) > 0, stopReason); ok {
					return partial, nil
				}
			}
			return ChatResponse{}, ctxErr
		}
		return ChatResponse{}, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			if partial, ok := buildPartialStreamResponse(textBuilder.String(), len(toolCalls) > 0, stopReason); ok {
				return partial, nil
			}
		}
		return ChatResponse{}, ctxErr
	}
	if onTextDelta != nil && !sawToolCalls && streamUnlocked {
		if visible := deltaFilter.Flush(); visible != "" {
			onTextDelta(visible)
		}
	}
	if onTextDelta != nil && !sawToolCalls && pendingText.Len() > 0 {
		if visible := stripHiddenAssistantMarkup(pendingText.String()); visible != "" {
			onTextDelta(visible)
		}
	}

	out := Message{
		Role: "assistant",
		Text: textBuilder.String(),
	}
	for index := 0; ; index++ {
		entry, ok := toolCalls[index]
		if !ok {
			break
		}
		arguments := "{}"
		if entry.SeenArgument {
			arguments = entry.Arguments.String()
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        entry.ID,
			Name:      entry.Name,
			Arguments: arguments,
		})
		emitProgressEvent(onProgressEvent, ProgressEvent{
			Kind:             progressKindModelStreamToolReady,
			Provider:         providerName,
			ToolName:         entry.Name,
			ToolCallID:       entry.ID,
			ArgumentsPreview: summarizeToolArgumentsPreview(arguments),
		})
	}
	if shouldPreserveOpenAIReasoningContent(providerName, out.Text, out.ToolCalls) {
		out.ReasoningContent = strings.TrimSpace(reasoningBuilder.String())
	}

	result := ChatResponse{
		Message:    out,
		StopReason: stopReason,
	}
	if !sawDone && strings.TrimSpace(result.StopReason) == "" && strings.TrimSpace(result.Message.Text) != "" && len(result.Message.ToolCalls) == 0 {
		result.StopReason = "stream_incomplete"
	}
	return result, nil
}

func shouldCollectOpenAIReasoningContent(providerName string) bool {
	providerName = normalizeProviderName(providerName)
	return strings.EqualFold(providerName, "deepseek") || isLocalOpenAICompatibleProvider(providerName)
}

func shouldPreserveOpenAIReasoningContent(providerName string, text string, toolCalls []ToolCall) bool {
	providerName = normalizeProviderName(providerName)
	if strings.EqualFold(providerName, "deepseek") {
		return len(toolCalls) > 0
	}
	if isLocalOpenAICompatibleProvider(providerName) {
		return strings.TrimSpace(text) == ""
	}
	return false
}

func shouldReleaseBufferedStreamText(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "\n\n") || strings.Contains(trimmed, "```") {
		return true
	}
	if visibleLen(trimmed) >= 120 {
		return true
	}
	return len(strings.Fields(trimmed)) >= 24
}

func buildPartialStreamResponse(text string, hasToolCalls bool, stopReason string) (ChatResponse, bool) {
	if strings.TrimSpace(text) == "" {
		return ChatResponse{}, false
	}
	if hasToolCalls {
		return ChatResponse{}, false
	}
	partialStopReason := "partial"
	if strings.TrimSpace(stopReason) != "" {
		partialStopReason = strings.TrimSpace(stopReason) + "_partial"
	}
	return ChatResponse{
		Message: Message{
			Role: "assistant",
			Text: text,
		},
		StopReason: normalizeStopReason(partialStopReason),
	}, true
}

func shouldFallbackAfterOpenAIStream(resp ChatResponse) bool {
	if strings.TrimSpace(resp.Message.Text) == "" && len(resp.Message.ToolCalls) == 0 {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(resp.StopReason), "stream_incomplete")
}

func markStopReasonAsStreamFallback(streamResp, fallbackResp ChatResponse) string {
	streamWasEmpty := responseLooksEmpty(streamResp)
	fallbackWasEmpty := responseLooksEmpty(fallbackResp)
	if fallbackWasEmpty {
		switch {
		case streamWasEmpty:
			return normalizeStopReason("stream_empty_fallback_empty_after_stream_retry")
		case strings.EqualFold(strings.TrimSpace(streamResp.StopReason), "stream_incomplete"):
			return normalizeStopReason("stream_incomplete_fallback_empty_after_stream_retry")
		}
	}
	base := strings.TrimSpace(fallbackResp.StopReason)
	if base == "" {
		base = "fallback"
	}
	return normalizeStopReason(base + "_after_stream_retry")
}

func responseLooksEmpty(resp ChatResponse) bool {
	return strings.TrimSpace(resp.Message.Text) == "" && len(resp.Message.ToolCalls) == 0
}

func extractOpenAIMessageText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}

	var direct string
	if err := json.Unmarshal(trimmed, &direct); err == nil {
		return direct
	}

	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return ""
	}
	return joinOpenAITextSegments(flattenOpenAIContentSegments(decoded, ""))
}

type openAITextSegment struct {
	Text string
	Kind string
}

func flattenOpenAIContentSegments(value any, kind string) []openAITextSegment {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []openAITextSegment{{
			Text: typed,
			Kind: kind,
		}}
	case []any:
		segments := make([]openAITextSegment, 0, len(typed))
		for _, item := range typed {
			segments = append(segments, flattenOpenAIContentSegments(item, kind)...)
		}
		return segments
	case map[string]any:
		if rawKind, ok := typed["type"].(string); ok && strings.TrimSpace(rawKind) != "" {
			kind = strings.TrimSpace(rawKind)
		}
		for _, key := range []string{"text", "output_text", "content", "value"} {
			if candidate, ok := typed[key]; ok {
				if segments := flattenOpenAIContentSegments(candidate, kind); len(segments) > 0 {
					return segments
				}
			}
		}
	}
	return nil
}

func joinOpenAITextSegments(segments []openAITextSegment) string {
	if len(segments) == 0 {
		return ""
	}

	parts := make([]string, 0, len(segments))
	var previous openAITextSegment
	hasPrevious := false
	for _, segment := range segments {
		if strings.TrimSpace(segment.Text) == "" {
			continue
		}
		if hasPrevious && shouldSkipEquivalentOpenAITextSegment(previous, segment) {
			continue
		}
		parts = append(parts, segment.Text)
		previous = segment
		hasPrevious = true
	}
	return strings.Join(parts, "")
}

func shouldSkipEquivalentOpenAITextSegment(previous, current openAITextSegment) bool {
	if previous.Text != current.Text {
		return false
	}

	previousKind := canonicalOpenAITextSegmentKind(previous.Kind)
	currentKind := canonicalOpenAITextSegmentKind(current.Kind)
	if previousKind == "" || currentKind == "" {
		return false
	}
	if previousKind != currentKind {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(previous.Kind), strings.TrimSpace(current.Kind))
}

func canonicalOpenAITextSegmentKind(kind string) string {
	lower := strings.ToLower(strings.TrimSpace(kind))
	if lower == "" {
		return ""
	}
	if strings.Contains(lower, "text") {
		return "text"
	}
	return lower
}

func assistantMessageContent(text string, hasToolCalls bool) any {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" && hasToolCalls {
		return nil
	}
	return text
}

func ensureOpenAIToolCallResponses(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}
	out := make([]Message, 0, len(messages))
	for index := 0; index < len(messages); index++ {
		msg := messages[index]
		if msg.Role == "tool" {
			continue
		}
		out = append(out, msg)
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}

		expected := map[string]ToolCall{}
		expectedOrder := make([]string, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			callID := strings.TrimSpace(call.ID)
			if callID != "" {
				expected[callID] = call
				expectedOrder = append(expectedOrder, callID)
			}
		}
		if len(expectedOrder) == 0 {
			continue
		}

		seen := map[string]bool{}
		orphanedToolMessages := make([]Message, 0)
		next := index + 1
		for next < len(messages) && messages[next].Role == "tool" {
			toolMsg := messages[next]
			toolCallID := strings.TrimSpace(toolMsg.ToolCallID)
			if _, ok := expected[toolCallID]; ok {
				seen[toolCallID] = true
				out = append(out, toolMsg)
			} else {
				orphanedToolMessages = append(orphanedToolMessages, toolMsg)
			}
			next++
		}
		for _, callID := range expectedOrder {
			if seen[callID] {
				continue
			}
			call := expected[callID]
			text := missingOpenAIToolResultText(messages, next)
			isError := strings.HasPrefix(text, "ERROR:")
			out = append(out, Message{
				Role:       "tool",
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Text:       text,
				IsError:    isError,
			})
		}
		index = next - 1
	}
	return out
}

func missingOpenAIToolResultText(messages []Message, next int) string {
	if next >= 0 && next < len(messages) && messages[next].Role == "user" && userMessageLooksLikeRuntimeToolRedirect(messages[next].Text) {
		return "NOTICE: tool call was superseded before execution by runtime guidance in the next user message. Do not treat this as a tool infrastructure failure; follow the next user message instead."
	}
	return "aborted"
}

func userMessageLooksLikeRuntimeToolRedirect(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"do not repeat",
		"do not call",
		"your last",
		"runtime gate",
		"recovery mode:",
		"tool budget",
		"tool loop",
		"reviewer feedback:",
		"automatic verification",
		"latest verification failed",
		"the latest verification failed",
		"pre-write review",
		"edit targeted stale",
		"stale or mismatched",
		"malformed or truncated json",
		"wrong patch format",
		"같은 도구",
		"반복하지",
		"자동 검증",
		"리뷰어 피드백",
		"런타임 게이트",
	)
}

func normalizeOpenAIToolCallArguments(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "{}"
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
		if _, ok := decoded.(map[string]any); ok {
			normalized, marshalErr := json.Marshal(decoded)
			if marshalErr == nil {
				return string(normalized)
			}
		}
	}

	fallback, err := json.Marshal(map[string]any{
		"raw": trimmed,
	})
	if err != nil {
		return `{}`
	}
	return string(fallback)
}

type OllamaModelInfo struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	ModifiedAt string `json:"modified_at,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Details    struct {
		Format            string   `json:"format,omitempty"`
		Family            string   `json:"family,omitempty"`
		Families          []string `json:"families,omitempty"`
		ParameterSize     string   `json:"parameter_size,omitempty"`
		QuantizationLevel string   `json:"quantization_level,omitempty"`
	} `json:"details,omitempty"`
}

type OpenRouterModelInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	ContextLength int    `json:"context_length,omitempty"`
	Architecture  struct {
		Modality     string `json:"modality,omitempty"`
		InstructType string `json:"instruct_type,omitempty"`
	} `json:"architecture,omitempty"`
	Pricing struct {
		Prompt     string `json:"prompt,omitempty"`
		Completion string `json:"completion,omitempty"`
	} `json:"pricing,omitempty"`
	TopProvider struct {
		IsModerated         bool `json:"is_moderated,omitempty"`
		ContextLength       int  `json:"context_length,omitempty"`
		MaxCompletionTokens int  `json:"max_completion_tokens,omitempty"`
	} `json:"top_provider,omitempty"`
	SupportedParameters []string `json:"supported_parameters,omitempty"`
}

type OpenAICompatibleModelInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Object  string `json:"object,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

type OllamaClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewOllamaClient(baseURL, apiKey string) *OllamaClient {
	return &OllamaClient{
		apiKey:     apiKey,
		baseURL:    normalizeOllamaBaseURL(baseURL),
		httpClient: &http.Client{},
	}
}

func (c *OllamaClient) Name() string {
	return "ollama"
}

func (c *OllamaClient) ModelRouteMetadata() ModelRouteMetadata {
	if c == nil {
		return ModelRouteMetadata{Provider: "ollama"}
	}
	return ModelRouteMetadata{Provider: "ollama", BaseURL: c.baseURL}
}

func (c *OllamaClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	type ollamaToolCall struct {
		Function struct {
			Name      string `json:"name"`
			Arguments any    `json:"arguments"`
		} `json:"function"`
	}
	type ollamaMessage struct {
		Role      string           `json:"role"`
		Content   string           `json:"content,omitempty"`
		ToolName  string           `json:"tool_name,omitempty"`
		ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
		Images    []string         `json:"images,omitempty"`
	}
	type ollamaRequest struct {
		Model    string          `json:"model"`
		Messages []ollamaMessage `json:"messages"`
		Tools    []struct {
			Type     string `json:"type"`
			Function struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools,omitempty"`
		Options map[string]any `json:"options,omitempty"`
		Stream  bool           `json:"stream"`
	}
	type ollamaResponse struct {
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		DoneReason string `json:"done_reason"`
		Error      string `json:"error,omitempty"`
	}

	payload := ollamaRequest{
		Model:    req.Model,
		Messages: make([]ollamaMessage, 0, len(req.Messages)+1),
		Stream:   false,
	}
	if req.Temperature > 0 || req.MaxTokens > 0 {
		payload.Options = map[string]any{}
		if req.Temperature > 0 {
			payload.Options["temperature"] = req.Temperature
		}
		if req.MaxTokens > 0 {
			payload.Options["num_predict"] = req.MaxTokens
		}
	}
	if strings.TrimSpace(req.System) != "" {
		payload.Messages = append(payload.Messages, ollamaMessage{
			Role:    "system",
			Content: req.System,
		})
	}
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			continue
		case "user":
			item := ollamaMessage{Role: "user", Content: msg.Text}
			if len(msg.Images) > 0 {
				encodedImages, err := encodeMessageImages(req.WorkingDir, msg.Images)
				if err != nil {
					return ChatResponse{}, err
				}
				for _, image := range encodedImages {
					item.Images = append(item.Images, image.Data)
				}
			}
			payload.Messages = append(payload.Messages, item)
		case "assistant":
			item := ollamaMessage{Role: "assistant", Content: msg.Text}
			for _, tc := range msg.ToolCalls {
				call := ollamaToolCall{}
				call.Function.Name = tc.Name
				var args any = map[string]any{}
				if strings.TrimSpace(tc.Arguments) != "" {
					if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
						args = map[string]any{"raw": tc.Arguments}
					}
				}
				call.Function.Arguments = args
				item.ToolCalls = append(item.ToolCalls, call)
			}
			payload.Messages = append(payload.Messages, item)
		case "tool":
			payload.Messages = append(payload.Messages, ollamaMessage{
				Role:     "tool",
				Content:  msg.Text,
				ToolName: msg.ToolName,
			})
		}
	}
	for _, tool := range req.Tools {
		item := struct {
			Type     string `json:"type"`
			Function struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
			} `json:"function"`
		}{Type: "function"}
		item.Function.Name = tool.Name
		item.Function.Description = tool.Description
		item.Function.Parameters = tool.InputSchema
		payload.Tools = append(payload.Tools, item)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, ollamaAPIURL(c.baseURL, "/chat"), bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("content-type", "application/json")
	if strings.TrimSpace(c.apiKey) != "" {
		httpReq.Header.Set("authorization", "Bearer "+c.apiKey)
	}

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
		return ChatResponse{}, newProviderHTTPError("ollama", resp.StatusCode, resp.Status, data, "")
	}

	var decoded ollamaResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ChatResponse{}, err
	}
	if strings.TrimSpace(decoded.Error) != "" {
		return ChatResponse{}, newProviderMessageError("ollama", decoded.Error, "", "", nil, data)
	}

	out := Message{Role: "assistant", Text: decoded.Message.Content}
	for i, tc := range decoded.Message.ToolCalls {
		args, _ := json.Marshal(tc.Function.Arguments)
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        fmt.Sprintf("ollama-tool-%d", i+1),
			Name:      tc.Function.Name,
			Arguments: string(args),
		})
	}
	return ChatResponse{
		Message:    out,
		StopReason: decoded.DoneReason,
	}, nil
}

func FetchOllamaModels(ctx context.Context, baseURL, apiKey string) ([]OllamaModelInfo, string, error) {
	type ollamaTagsResponse struct {
		Models []OllamaModelInfo `json:"models"`
		Error  string            `json:"error,omitempty"`
	}

	normalized := normalizeOllamaBaseURL(baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaAPIURL(normalized, "/tags"), nil)
	if err != nil {
		return nil, normalized, err
	}
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("authorization", "Bearer "+apiKey)
	}

	// 모델 목록 조회에도 충분한 타임아웃 설정
	client := &http.Client{
		Timeout: 60 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, normalized, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, normalized, err
	}
	if resp.StatusCode >= 300 {
		return nil, normalized, newProviderHTTPError("ollama", resp.StatusCode, resp.Status, data, "")
	}

	var decoded ollamaTagsResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, normalized, err
	}
	if strings.TrimSpace(decoded.Error) != "" {
		return nil, normalized, newProviderMessageError("ollama", decoded.Error, "", "", nil, data)
	}
	return decoded.Models, normalized, nil
}

func normalizeOllamaBaseURL(baseURL string) string {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = "http://localhost:11434"
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	base = strings.TrimRight(base, "/")
	base = strings.TrimSuffix(base, "/api")
	return base
}

func normalizeAnthropicBaseURL(baseURL string) string {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = "https://api.anthropic.com"
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return strings.TrimRight(base, "/")
}

func ollamaAPIURL(baseURL, path string) string {
	base := normalizeOllamaBaseURL(baseURL)
	return base + "/api" + path
}

func normalizeOpenRouterBaseURL(baseURL string) string {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = "https://openrouter.ai/api/v1"
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return strings.TrimRight(base, "/")
}

func normalizeDeepSeekBaseURL(baseURL string) string {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = "https://api.deepseek.com"
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return strings.TrimRight(base, "/")
}

func normalizeOpenAIBaseURL(baseURL string) string {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = "https://api.openai.com"
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return strings.TrimRight(base, "/")
}

func isLocalOpenAICompatibleProvider(provider string) bool {
	switch normalizeProviderName(provider) {
	case "lmstudio", "vllm", "llama.cpp":
		return true
	default:
		return false
	}
}

func defaultLocalOpenAICompatibleBaseURL(provider string) string {
	switch normalizeProviderName(provider) {
	case "lmstudio":
		return "http://localhost:1234/v1"
	case "vllm":
		return "http://localhost:8000/v1"
	case "llama.cpp":
		return "http://localhost:8080/v1"
	default:
		return "http://localhost:8000/v1"
	}
}

func normalizeLocalOpenAICompatibleBaseURL(provider, baseURL string) string {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = defaultLocalOpenAICompatibleBaseURL(provider)
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	return strings.TrimRight(base, "/")
}

func normalizeProviderBaseURL(provider, baseURL string) string {
	switch normalizeProviderName(provider) {
	case "anthropic":
		return normalizeAnthropicBaseURL(baseURL)
	case "openai", "openai-compatible":
		return normalizeOpenAIBaseURL(baseURL)
	case "deepseek":
		return normalizeDeepSeekBaseURL(baseURL)
	case "openrouter":
		return normalizeOpenRouterBaseURL(baseURL)
	case "opencode":
		return normalizeOpenCodeBaseURL(baseURL)
	case "opencode-go":
		return normalizeOpenCodeGoBaseURL(baseURL)
	case "ollama":
		return normalizeOllamaBaseURL(baseURL)
	case "lmstudio", "vllm", "llama.cpp":
		return normalizeLocalOpenAICompatibleBaseURL(provider, baseURL)
	case "openai-codex":
		return normalizeOpenAICodexBaseURL(baseURL)
	case "codex-cli", "anthropic-claude-cli":
		return strings.TrimSpace(baseURL)
	default:
		return strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
}

func formatProviderErrorDetails(data []byte) string {
	trimmed := strings.TrimSpace(string(data))
	decoded := providerErrorBody{}
	if err := json.Unmarshal(data, &decoded); err == nil && decoded.Error != nil {
		return formatProviderErrorFields(decoded.Error.Message, decoded.Error.Type, decoded.Error.Param, decoded.Error.Code, data)
	}
	if trimmed == "" {
		return "empty error response body"
	}
	return trimmed
}

func formatProviderErrorFields(message string, errorType string, param string, code any, raw []byte) string {
	parts := []string{}
	if strings.TrimSpace(message) != "" {
		parts = append(parts, strings.TrimSpace(message))
	} else {
		parts = append(parts, "provider returned error")
	}
	if strings.TrimSpace(errorType) != "" {
		parts = append(parts, "type="+strings.TrimSpace(errorType))
	}
	if strings.TrimSpace(param) != "" {
		parts = append(parts, "param="+strings.TrimSpace(param))
	}
	if code != nil {
		parts = append(parts, "code="+fmt.Sprintf("%v", code))
	}

	detail := strings.Join(parts, " | ")
	rawText := strings.TrimSpace(string(raw))
	if strings.EqualFold(strings.TrimSpace(message), "Provider returned error") && rawText != "" && !strings.Contains(rawText, detail) {
		detail += " | raw=" + rawText
	}
	return detail
}

func summarizeOpenAIRequestBody(body []byte) string {
	const maxLen = 600

	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "empty"
	}
	if len(trimmed) <= maxLen {
		return trimmed
	}
	return trimmed[:maxLen] + "...(truncated)"
}

func openAIAPIURL(baseURL, path string) string {
	base := normalizeOpenAIBaseURL(baseURL)
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

func openAICompatibleAPIURL(provider, baseURL, path string) string {
	if normalizeProviderName(provider) == "deepseek" {
		return deepSeekAPIURL(baseURL, path)
	}
	return openAIAPIURL(baseURL, path)
}

func deepSeekAPIURL(baseURL, path string) string {
	base := normalizeDeepSeekBaseURL(baseURL)
	normalizedPath := "/" + strings.TrimLeft(path, "/")
	lowerBase := strings.ToLower(base)
	lowerPath := strings.ToLower(normalizedPath)
	if strings.HasSuffix(lowerBase, lowerPath) {
		return base
	}
	if strings.HasSuffix(lowerBase, "/v1") && strings.HasPrefix(lowerPath, "/v1/") {
		return base + normalizedPath[len("/v1"):]
	}
	if strings.HasPrefix(lowerPath, "/v1/") {
		withoutVersion := normalizedPath[len("/v1"):]
		if strings.HasSuffix(lowerBase, strings.ToLower(withoutVersion)) {
			return base
		}
		normalizedPath = normalizedPath[len("/v1"):]
	}
	return base + normalizedPath
}

func deepSeekAccountAPIURL(baseURL, path string) string {
	base := normalizeDeepSeekBaseURL(baseURL)
	lower := strings.ToLower(base)
	for _, suffix := range []string{"/v1", "/beta"} {
		if strings.HasSuffix(lower, suffix) {
			base = base[:len(base)-len(suffix)]
			break
		}
	}
	return deepSeekAPIURL(base, path)
}

func normalizeDeepSeekReasoningEffort(effort string) (string, error) {
	normalized := normalizeReasoningEffort(effort)
	switch normalized {
	case "":
		return "", nil
	case "minimal", "low", "medium", "high":
		return "high", nil
	case "xhigh":
		return "max", nil
	default:
		return "", fmt.Errorf("invalid reasoning effort %q; use undefined, minimal, low, medium, high, or xhigh", strings.TrimSpace(effort))
	}
}

type OpenRouterKeyStatus struct {
	Label              string   `json:"label,omitempty"`
	Limit              *float64 `json:"limit,omitempty"`
	LimitRemaining     *float64 `json:"limit_remaining,omitempty"`
	LimitReset         string   `json:"limit_reset,omitempty"`
	Usage              *float64 `json:"usage,omitempty"`
	UsageDaily         *float64 `json:"usage_daily,omitempty"`
	UsageWeekly        *float64 `json:"usage_weekly,omitempty"`
	UsageMonthly       *float64 `json:"usage_monthly,omitempty"`
	BYOKUsage          *float64 `json:"byok_usage,omitempty"`
	BYOKUsageDaily     *float64 `json:"byok_usage_daily,omitempty"`
	BYOKUsageWeekly    *float64 `json:"byok_usage_weekly,omitempty"`
	BYOKUsageMonthly   *float64 `json:"byok_usage_monthly,omitempty"`
	IncludeBYOKInLimit bool     `json:"include_byok_in_limit,omitempty"`
	IsFreeTier         bool     `json:"is_free_tier,omitempty"`
	IsManagementKey    bool     `json:"is_management_key,omitempty"`
	IsProvisioningKey  bool     `json:"is_provisioning_key,omitempty"`
	ExpiresAt          string   `json:"expires_at,omitempty"`
}

type OpenRouterCredits struct {
	TotalCredits *float64 `json:"total_credits,omitempty"`
	TotalUsage   *float64 `json:"total_usage,omitempty"`
}

func FetchOpenRouterKeyStatus(ctx context.Context, baseURL, apiKey string) (OpenRouterKeyStatus, string, error) {
	type openRouterKeyResponse struct {
		Data  OpenRouterKeyStatus `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	normalized := normalizeOpenRouterBaseURL(baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized+"/key", nil)
	if err != nil {
		return OpenRouterKeyStatus{}, normalized, err
	}
	req.Header.Set("content-type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return OpenRouterKeyStatus{}, normalized, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return OpenRouterKeyStatus{}, normalized, err
	}
	if resp.StatusCode >= 300 {
		return OpenRouterKeyStatus{}, normalized, newProviderHTTPError("openrouter", resp.StatusCode, resp.Status, data, "")
	}

	var decoded openRouterKeyResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return OpenRouterKeyStatus{}, normalized, err
	}
	if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
		return OpenRouterKeyStatus{}, normalized, newProviderMessageError("openrouter", decoded.Error.Message, "", "", nil, data)
	}
	return decoded.Data, normalized, nil
}

func FetchOpenRouterCredits(ctx context.Context, baseURL, apiKey string) (OpenRouterCredits, string, error) {
	type openRouterCreditsResponse struct {
		Data  OpenRouterCredits `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	normalized := normalizeOpenRouterBaseURL(baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized+"/credits", nil)
	if err != nil {
		return OpenRouterCredits{}, normalized, err
	}
	req.Header.Set("content-type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return OpenRouterCredits{}, normalized, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return OpenRouterCredits{}, normalized, err
	}
	if resp.StatusCode >= 300 {
		return OpenRouterCredits{}, normalized, newProviderHTTPError("openrouter", resp.StatusCode, resp.Status, data, "")
	}

	var decoded openRouterCreditsResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return OpenRouterCredits{}, normalized, err
	}
	if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
		return OpenRouterCredits{}, normalized, newProviderMessageError("openrouter", decoded.Error.Message, "", "", nil, data)
	}
	return decoded.Data, normalized, nil
}

type DeepSeekBalance struct {
	IsAvailable  bool                  `json:"is_available"`
	BalanceInfos []DeepSeekBalanceInfo `json:"balance_infos,omitempty"`
}

type DeepSeekBalanceInfo struct {
	Currency        string `json:"currency,omitempty"`
	TotalBalance    string `json:"total_balance,omitempty"`
	GrantedBalance  string `json:"granted_balance,omitempty"`
	ToppedUpBalance string `json:"topped_up_balance,omitempty"`
}

func FetchDeepSeekBalance(ctx context.Context, baseURL, apiKey string) (DeepSeekBalance, string, error) {
	normalized := normalizeDeepSeekBaseURL(baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, deepSeekAccountAPIURL(normalized, "/user/balance"), nil)
	if err != nil {
		return DeepSeekBalance{}, normalized, err
	}
	req.Header.Set("content-type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("authorization", "Bearer "+strings.TrimSpace(apiKey))
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return DeepSeekBalance{}, normalized, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return DeepSeekBalance{}, normalized, err
	}
	if resp.StatusCode >= 300 {
		return DeepSeekBalance{}, normalized, newProviderHTTPError("deepseek", resp.StatusCode, resp.Status, data, "")
	}

	var decoded DeepSeekBalance
	if err := json.Unmarshal(data, &decoded); err != nil {
		return DeepSeekBalance{}, normalized, err
	}
	return decoded, normalized, nil
}

func FetchOpenRouterModels(ctx context.Context, baseURL, apiKey string) ([]OpenRouterModelInfo, string, error) {
	type openRouterModelsResponse struct {
		Data  []OpenRouterModelInfo `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	normalized := normalizeOpenRouterBaseURL(baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized+"/models", nil)
	if err != nil {
		return nil, normalized, err
	}
	req.Header.Set("content-type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, normalized, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, normalized, err
	}
	if resp.StatusCode >= 300 {
		return nil, normalized, newProviderHTTPError("openrouter", resp.StatusCode, resp.Status, data, "")
	}

	var decoded openRouterModelsResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, normalized, err
	}
	if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
		return nil, normalized, newProviderMessageError("openrouter", decoded.Error.Message, "", "", nil, data)
	}
	return decoded.Data, normalized, nil
}

func FetchOpenAICompatibleModels(ctx context.Context, provider, baseURL, apiKey string) ([]OpenAICompatibleModelInfo, string, error) {
	type modelsResponse struct {
		Data   []OpenAICompatibleModelInfo `json:"data"`
		Models []OpenAICompatibleModelInfo `json:"models"`
		Error  *struct {
			Message string `json:"message"`
			Type    string `json:"type,omitempty"`
			Param   string `json:"param,omitempty"`
			Code    any    `json:"code,omitempty"`
		} `json:"error,omitempty"`
	}

	provider = normalizeProviderName(provider)
	if provider == "" {
		provider = "openai-compatible"
	}
	normalized := normalizeProviderBaseURL(provider, baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAICompatibleAPIURL(provider, normalized, "/v1/models"), nil)
	if err != nil {
		return nil, normalized, err
	}
	req.Header.Set("content-type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("authorization", "Bearer "+strings.TrimSpace(apiKey))
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
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

	var decoded modelsResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, normalized, err
	}
	if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
		return nil, normalized, newProviderMessageError(provider, decoded.Error.Message, decoded.Error.Type, decoded.Error.Param, decoded.Error.Code, data)
	}
	models := decoded.Data
	if len(models) == 0 {
		models = decoded.Models
	}
	return dedupeOpenAICompatibleModels(models), normalized, nil
}

func dedupeOpenAICompatibleModels(models []OpenAICompatibleModelInfo) []OpenAICompatibleModelInfo {
	out := make([]OpenAICompatibleModelInfo, 0, len(models))
	seen := map[string]bool{}
	for _, model := range models {
		model.ID = strings.TrimSpace(model.ID)
		model.Name = strings.TrimSpace(model.Name)
		if model.ID == "" || seen[strings.ToLower(model.ID)] {
			continue
		}
		if model.Name == "" {
			model.Name = model.ID
		}
		out = append(out, model)
		seen[strings.ToLower(model.ID)] = true
	}
	return out
}
