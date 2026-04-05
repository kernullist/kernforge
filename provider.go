package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Message struct {
	Role       string         `json:"role"`
	Text       string         `json:"text,omitempty"`
	Images     []MessageImage `json:"images,omitempty"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	IsError    bool           `json:"is_error,omitempty"`
}

type ChatRequest struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolDefinition
	MaxTokens   int
	Temperature float64
	WorkingDir  string
	OnTextDelta func(string)
}

type ChatResponse struct {
	Message    Message
	StopReason string
}

type providerErrorBody struct {
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type,omitempty"`
		Param   string `json:"param,omitempty"`
		Code    any    `json:"code,omitempty"`
	} `json:"error,omitempty"`
}

type ProviderClient interface {
	Name() string
	Complete(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

func NewProviderClient(cfg Config) (ProviderClient, error) {
	switch strings.ToLower(cfg.Provider) {
	case "anthropic":
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("Anthropic provider selected but no API key was found")
		}
		return NewAnthropicClient(cfg.BaseURL, cfg.APIKey), nil
	case "openrouter":
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("OpenRouter provider selected but no API key was found")
		}
		return NewOpenAIClient(normalizeOpenRouterBaseURL(cfg.BaseURL), cfg.APIKey), nil
	case "ollama":
		return NewOllamaClient(cfg.BaseURL, cfg.APIKey), nil
	case "openai", "openai-compatible":
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("OpenAI-compatible provider selected but no API key was found")
		}
		return NewOpenAIClient(cfg.BaseURL, cfg.APIKey), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", cfg.Provider)
	}
}

type AnthropicClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewAnthropicClient(baseURL, apiKey string) *AnthropicClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &AnthropicClient{
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{},
	}
}

func (c *AnthropicClient) Name() string {
	return "anthropic"
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
		return ChatResponse{}, fmt.Errorf("anthropic API error (%s): %s", resp.Status, strings.TrimSpace(string(data)))
	}

	var decoded anthropicResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ChatResponse{}, err
	}
	if decoded.Error != nil {
		return ChatResponse{}, fmt.Errorf("anthropic API error: %s", decoded.Error.Message)
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
	httpClient *http.Client
}

func NewOpenAIClient(baseURL, apiKey string) *OpenAIClient {
	return &OpenAIClient{
		apiKey:     apiKey,
		baseURL:    normalizeOpenAIBaseURL(baseURL),
		httpClient: &http.Client{},
	}
}

func (c *OpenAIClient) Name() string {
	return "openai"
}

func (c *OpenAIClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	type openAIToolCall struct {
		ID       string `json:"id,omitempty"`
		Type     string `json:"type,omitempty"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	type openAIMessage struct {
		Role       string           `json:"role"`
		Content    any              `json:"content,omitempty"`
		Name       string           `json:"name,omitempty"`
		ToolCallID string           `json:"tool_call_id,omitempty"`
		ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	}
	type openAIRequest struct {
		Model       string          `json:"model"`
		Messages    []openAIMessage `json:"messages"`
		Temperature float64         `json:"temperature,omitempty"`
		MaxTokens   int             `json:"max_tokens,omitempty"`
		Tools       []struct {
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
				Content   string           `json:"content"`
				ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
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

	if strings.TrimSpace(req.System) != "" {
		payload.Messages = append(payload.Messages, openAIMessage{
			Role:    "system",
			Content: req.System,
		})
	}

	for _, msg := range req.Messages {
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
			item := openAIMessage{Role: "assistant", Content: assistantMessageContent(msg.Text, len(msg.ToolCalls) > 0)}
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
	if req.OnTextDelta != nil {
		payload.Stream = true
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ChatResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIAPIURL(c.baseURL, "/v1/chat/completions"), bytes.NewReader(body))
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

	if payload.Stream {
		return readOpenAIStream(ctx, resp.Body, req.OnTextDelta)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResponse{}, err
	}
	if resp.StatusCode >= 300 {
		return ChatResponse{}, fmt.Errorf("openai API error (%s): %s | request=%s", resp.Status, formatProviderErrorDetails(data), summarizeOpenAIRequestBody(body))
	}

	var decoded openAIResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ChatResponse{}, err
	}
	if decoded.Error != nil {
		return ChatResponse{}, fmt.Errorf("openai API error: %s", formatProviderErrorFields(decoded.Error.Message, decoded.Error.Type, decoded.Error.Param, decoded.Error.Code, data))
	}
	if len(decoded.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("openai API error: empty choices")
	}

	choice := decoded.Choices[0]
	out := Message{Role: "assistant", Text: choice.Message.Content}
	for _, tc := range choice.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	return ChatResponse{
		Message:    out,
		StopReason: choice.FinishReason,
	}, nil
}

func readOpenAIStream(ctx context.Context, body io.ReadCloser, onTextDelta func(string)) (ChatResponse, error) {
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
				Content   string                `json:"content,omitempty"`
				ToolCalls []streamToolCallDelta `json:"tool_calls,omitempty"`
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
	toolCalls := map[int]*toolCallAccumulator{}
	stopReason := ""

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
			break
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return ChatResponse{}, err
		}
		if chunk.Error != nil {
			return ChatResponse{}, fmt.Errorf("openai API error: %s", formatProviderErrorFields(chunk.Error.Message, chunk.Error.Type, chunk.Error.Param, chunk.Error.Code, []byte(payload)))
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				stopReason = choice.FinishReason
			}
			if choice.Delta.Content != "" {
				textBuilder.WriteString(choice.Delta.Content)
				if onTextDelta != nil {
					onTextDelta(choice.Delta.Content)
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
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
				}
				if tc.Function.Arguments != "" {
					entry.Arguments.WriteString(tc.Function.Arguments)
					entry.SeenArgument = true
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ChatResponse{}, ctxErr
		}
		return ChatResponse{}, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ChatResponse{}, ctxErr
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
	}

	return ChatResponse{
		Message:    out,
		StopReason: stopReason,
	}, nil
}

func assistantMessageContent(text string, hasToolCalls bool) any {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" && hasToolCalls {
		return nil
	}
	return text
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
		return ChatResponse{}, fmt.Errorf("ollama API error (%s): %s", resp.Status, strings.TrimSpace(string(data)))
	}

	var decoded ollamaResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ChatResponse{}, err
	}
	if strings.TrimSpace(decoded.Error) != "" {
		return ChatResponse{}, fmt.Errorf("ollama API error: %s", decoded.Error)
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
		return nil, normalized, fmt.Errorf("ollama API error (%s): %s", resp.Status, strings.TrimSpace(string(data)))
	}

	var decoded ollamaTagsResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, normalized, err
	}
	if strings.TrimSpace(decoded.Error) != "" {
		return nil, normalized, fmt.Errorf("ollama API error: %s", decoded.Error)
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
		return nil, normalized, fmt.Errorf("openrouter API error (%s): %s", resp.Status, strings.TrimSpace(string(data)))
	}

	var decoded openRouterModelsResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, normalized, err
	}
	if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
		return nil, normalized, fmt.Errorf("openrouter API error: %s", decoded.Error.Message)
	}
	return decoded.Data, normalized, nil
}
