package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	openAICodexDefaultBaseURL   = "https://chatgpt.com/backend-api/codex"
	openAICodexDefaultModel     = "gpt-5.5"
	openAICodexOAuthClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAICodexOAuthTokenURL    = "https://auth.openai.com/oauth/token"
	openAICodexDeviceCodeURL    = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	openAICodexDeviceTokenURL   = "https://auth.openai.com/api/accounts/deviceauth/token"
	openAICodexDeviceURL        = "https://auth.openai.com/codex/device"
	openAICodexDeviceRedirect   = "https://auth.openai.com/deviceauth/callback"
	openAICodexAccessTokenEnv   = "KERNFORGE_CODEX_ACCESS_TOKEN"
	openAICodexAuthFileEnv      = "KERNFORGE_CODEX_AUTH_FILE"
	openAICodexDefaultAuthFile  = "codex_auth.json"
	openAICodexTokenRefreshSkew = 2 * time.Minute
)

var (
	openAICodexOAuthTokenEndpoint  = openAICodexOAuthTokenURL
	openAICodexDeviceCodeEndpoint  = openAICodexDeviceCodeURL
	openAICodexDeviceTokenEndpoint = openAICodexDeviceTokenURL
)

type codexOAuthAccessTokenSource interface {
	AccessToken(ctx context.Context) (string, error)
}

var codexOAuthRefreshMu sync.Mutex

type OpenAICodexClient struct {
	baseURL         string
	reasoningEffort string
	httpClient      *http.Client
	tokenSource     codexOAuthAccessTokenSource
}

func NewOpenAICodexClient(baseURL string) *OpenAICodexClient {
	return NewOpenAICodexClientWithReasoningEffort(baseURL, "")
}

func NewOpenAICodexClientWithReasoningEffort(baseURL string, reasoningEffort string) *OpenAICodexClient {
	httpClient := &http.Client{}
	return &OpenAICodexClient{
		baseURL:         normalizeOpenAICodexBaseURL(baseURL),
		reasoningEffort: normalizeReasoningEffort(reasoningEffort),
		httpClient:      httpClient,
		tokenSource:     NewCodexOAuthTokenSource("", httpClient),
	}
}

func (c *OpenAICodexClient) Name() string {
	return "openai-codex"
}

func (c *OpenAICodexClient) ModelRouteMetadata() ModelRouteMetadata {
	if c == nil {
		return ModelRouteMetadata{Provider: "openai-codex"}
	}
	return ModelRouteMetadata{
		Provider:        "openai-codex",
		BaseURL:         c.baseURL,
		ReasoningEffort: c.reasoningEffort,
	}
}

func (c *OpenAICodexClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if c == nil {
		return ChatResponse{}, fmt.Errorf("OpenAI Codex provider is not configured")
	}
	if strings.TrimSpace(req.ReasoningEffort) == "" {
		req.ReasoningEffort = c.reasoningEffort
	}
	tokenSource := c.tokenSource
	if tokenSource == nil {
		tokenSource = NewCodexOAuthTokenSource("", c.httpClient)
	}
	accessToken, err := tokenSource.AccessToken(ctx)
	if err != nil {
		return ChatResponse{}, err
	}

	body, err := buildOpenAICodexRequestBody(req)
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, openAICodexAPIURL(c.baseURL, "/responses"), bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")
	httpReq.Header.Set("authorization", "Bearer "+accessToken)
	httpReq.Header.Set("originator", "codex_cli_rs")
	httpReq.Header.Set("user-agent", "kernforge/openai-codex")
	requestID := newOpenAICodexRequestID()
	httpReq.Header.Set("session_id", requestID)
	httpReq.Header.Set("x-client-request-id", requestID)

	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return ChatResponse{}, err
		}
		return ChatResponse{}, newProviderHTTPError("openai-codex", resp.StatusCode, resp.Status, data, summarizeOpenAIRequestBody(body))
	}
	out, err := readOpenAICodexStream(ctx, resp.Body, req.OnProgressEvent)
	if err != nil {
		return ChatResponse{}, err
	}
	return out, nil
}

func FetchOpenAICodexModels(ctx context.Context, baseURL string, tokenSource codexOAuthAccessTokenSource, httpClient *http.Client) ([]CodexCLIModelInfo, error) {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	if tokenSource == nil {
		tokenSource = NewCodexOAuthTokenSource("", httpClient)
	}
	accessToken, err := tokenSource.AccessToken(ctx)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(openAICodexAPIURL(baseURL, "/models"))
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("client_version", "1.0.0")
	endpoint.RawQuery = query.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("accept", "application/json")
	httpReq.Header.Set("authorization", "Bearer "+accessToken)
	httpReq.Header.Set("originator", "codex_cli_rs")
	httpReq.Header.Set("user-agent", "kernforge/openai-codex")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, newProviderHTTPError("openai-codex", resp.StatusCode, resp.Status, data, "")
	}
	return parseCodexCLIModelsJSON(data)
}

func buildOpenAICodexRequestBody(req ChatRequest) ([]byte, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" || strings.EqualFold(model, codexCLIDefaultModel) {
		model = openAICodexDefaultModel
	}
	input, err := buildOpenAICodexInput(req)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"model":               model,
		"input":               input,
		"store":               false,
		"stream":              true,
		"include":             []string{"reasoning.encrypted_content"},
		"parallel_tool_calls": true,
	}
	if strings.TrimSpace(req.System) != "" {
		payload["instructions"] = req.System
	}
	if strings.TrimSpace(req.ReasoningEffort) != "" && !validReasoningEffort(req.ReasoningEffort) {
		return nil, fmt.Errorf("invalid reasoning effort %q; use undefined, minimal, low, medium, high, or xhigh", strings.TrimSpace(req.ReasoningEffort))
	}
	if effort := normalizeReasoningEffort(req.ReasoningEffort); effort != "" {
		payload["reasoning"] = map[string]any{
			"effort": effort,
		}
	}
	if req.JSONMode {
		payload["text"] = map[string]any{
			"format": map[string]any{
				"type": "json_object",
			},
		}
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			name := strings.TrimSpace(tool.Name)
			if name == "" {
				continue
			}
			item := map[string]any{
				"type":        "function",
				"name":        name,
				"description": strings.TrimSpace(tool.Description),
				"parameters":  openAICodexToolParameters(tool.InputSchema),
			}
			tools = append(tools, item)
		}
		if len(tools) > 0 {
			payload["tools"] = tools
			payload["tool_choice"] = "auto"
		}
	}
	return json.Marshal(payload)
}

func openAICodexToolParameters(schema map[string]any) map[string]any {
	if len(schema) > 0 {
		return schema
	}
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func buildOpenAICodexInput(req ChatRequest) ([]any, error) {
	messages := ensureOpenAIToolCallResponses(req.Messages)
	items := make([]any, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			continue
		case "user":
			content, err := openAICodexUserContent(req.WorkingDir, msg)
			if err != nil {
				return nil, err
			}
			if len(content) == 0 {
				continue
			}
			items = append(items, map[string]any{
				"role":    "user",
				"content": content,
			})
		case "assistant":
			if strings.TrimSpace(msg.Text) != "" {
				items = append(items, map[string]any{
					"role": "assistant",
					"content": []map[string]any{{
						"type": "output_text",
						"text": msg.Text,
					}},
				})
			}
			for _, tc := range msg.ToolCalls {
				callID := firstNonEmptyTrimmed(tc.ID, tc.Name)
				if callID == "" || strings.TrimSpace(tc.Name) == "" {
					continue
				}
				items = append(items, map[string]any{
					"type":      "function_call",
					"call_id":   callID,
					"name":      strings.TrimSpace(tc.Name),
					"arguments": normalizeOpenAIToolCallArguments(tc.Arguments),
				})
			}
		case "tool":
			callID := firstNonEmptyTrimmed(msg.ToolCallID, msg.ToolName)
			if callID == "" {
				continue
			}
			items = append(items, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  msg.Text,
			})
		}
	}
	return items, nil
}

func openAICodexUserContent(workingDir string, msg Message) ([]map[string]any, error) {
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
	encodedImages, err := encodeMessageImages(workingDir, msg.Images)
	if err != nil {
		return nil, err
	}
	for _, image := range encodedImages {
		content = append(content, map[string]any{
			"type":      "input_image",
			"image_url": imageDataURI(image),
			"detail":    encodedImageDetail(image),
		})
	}
	return content, nil
}

func parseOpenAICodexResponse(data []byte) (ChatResponse, error) {
	var decoded struct {
		Status            string `json:"status"`
		OutputText        string `json:"output_text,omitempty"`
		IncompleteDetails *struct {
			Reason string `json:"reason,omitempty"`
		} `json:"incomplete_details,omitempty"`
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type,omitempty"`
			Param   string `json:"param,omitempty"`
			Code    any    `json:"code,omitempty"`
		} `json:"error,omitempty"`
		Output []struct {
			ID        string `json:"id,omitempty"`
			Type      string `json:"type"`
			Status    string `json:"status,omitempty"`
			Role      string `json:"role,omitempty"`
			CallID    string `json:"call_id,omitempty"`
			Name      string `json:"name,omitempty"`
			Arguments string `json:"arguments,omitempty"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text,omitempty"`
			} `json:"content,omitempty"`
		} `json:"output,omitempty"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ChatResponse{}, err
	}
	if decoded.Error != nil {
		return ChatResponse{}, newProviderMessageError("openai-codex", decoded.Error.Message, decoded.Error.Type, decoded.Error.Param, decoded.Error.Code, data)
	}

	out := Message{Role: "assistant"}
	texts := []string{}
	if strings.TrimSpace(decoded.OutputText) != "" {
		texts = append(texts, decoded.OutputText)
	}
	for _, item := range decoded.Output {
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
					texts = append(texts, content.Text)
				}
			}
		case "function_call":
			callID := firstNonEmptyTrimmed(item.CallID, item.ID)
			if callID != "" && strings.TrimSpace(item.Name) != "" {
				out.ToolCalls = append(out.ToolCalls, ToolCall{
					ID:        callID,
					Name:      strings.TrimSpace(item.Name),
					Arguments: normalizeOpenAIToolCallArguments(item.Arguments),
				})
			}
		}
	}
	out.Text = strings.TrimSpace(strings.Join(deduplicateOpenAICodexTexts(texts), "\n"))
	stopReason := strings.TrimSpace(decoded.Status)
	if decoded.IncompleteDetails != nil && strings.TrimSpace(decoded.IncompleteDetails.Reason) != "" {
		stopReason = strings.TrimSpace(decoded.IncompleteDetails.Reason)
	}
	if out.Text == "" && len(out.ToolCalls) == 0 {
		return ChatResponse{}, newProviderMessageError("openai-codex", "empty Responses output", "", "", nil, data)
	}
	return ChatResponse{
		Message:    out,
		StopReason: stopReason,
	}, nil
}

type openAICodexStreamToolCall struct {
	ID           string
	Name         string
	Arguments    strings.Builder
	ArgsStarted  bool
	ReadyEmitted bool
}

func readOpenAICodexStream(ctx context.Context, body io.Reader, onProgressEvent ...func(ProgressEvent)) (ChatResponse, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var text strings.Builder
	var completedResponse json.RawMessage
	toolCalls := map[int]*openAICodexStreamToolCall{}
	toolOrder := []int{}
	stopReason := ""
	var progress func(ProgressEvent)
	if len(onProgressEvent) > 0 {
		progress = onProgressEvent[0]
	}

	ensureToolCall := func(index int) *openAICodexStreamToolCall {
		item := toolCalls[index]
		if item == nil {
			item = &openAICodexStreamToolCall{}
			toolCalls[index] = item
			toolOrder = append(toolOrder, index)
		}
		return item
	}
	emitToolReady := func(item *openAICodexStreamToolCall) {
		if item == nil || item.ReadyEmitted {
			return
		}
		item.ReadyEmitted = true
		emitProgressEvent(progress, ProgressEvent{
			Kind:             progressKindModelStreamToolReady,
			Provider:         "openai-codex",
			ToolName:         item.Name,
			ToolCallID:       item.ID,
			ArgumentsPreview: summarizeToolArgumentsPreview(item.Arguments.String()),
		})
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event struct {
			Type        string          `json:"type"`
			Delta       string          `json:"delta,omitempty"`
			Text        string          `json:"text,omitempty"`
			Arguments   string          `json:"arguments,omitempty"`
			OutputIndex int             `json:"output_index,omitempty"`
			Response    json.RawMessage `json:"response,omitempty"`
			Item        struct {
				ID        string `json:"id,omitempty"`
				Type      string `json:"type,omitempty"`
				CallID    string `json:"call_id,omitempty"`
				Name      string `json:"name,omitempty"`
				Arguments string `json:"arguments,omitempty"`
				Content   []struct {
					Type string `json:"type,omitempty"`
					Text string `json:"text,omitempty"`
				} `json:"content,omitempty"`
			} `json:"item,omitempty"`
			Error *struct {
				Message string `json:"message"`
				Type    string `json:"type,omitempty"`
				Param   string `json:"param,omitempty"`
				Code    any    `json:"code,omitempty"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return ChatResponse{}, err
		}
		if event.Error != nil {
			return ChatResponse{}, newProviderMessageError("openai-codex", event.Error.Message, event.Error.Type, event.Error.Param, event.Error.Code, []byte(payload))
		}
		switch event.Type {
		case "response.output_text.delta":
			text.WriteString(event.Delta)
		case "response.output_text.done":
			if text.Len() == 0 && strings.TrimSpace(event.Text) != "" {
				text.WriteString(event.Text)
			}
		case "response.output_item.added":
			if event.Item.Type == "function_call" {
				item := ensureToolCall(event.OutputIndex)
				item.ID = firstNonEmptyTrimmed(event.Item.CallID, event.Item.ID, item.ID)
				item.Name = firstNonEmptyTrimmed(event.Item.Name, item.Name)
				if strings.TrimSpace(event.Item.Arguments) != "" && item.Arguments.Len() == 0 {
					item.Arguments.WriteString(event.Item.Arguments)
				}
				emitProgressEvent(progress, ProgressEvent{
					Kind:       progressKindModelStreamToolCall,
					Provider:   "openai-codex",
					ToolName:   item.Name,
					ToolCallID: item.ID,
				})
			}
		case "response.function_call_arguments.delta":
			item := ensureToolCall(event.OutputIndex)
			if !item.ArgsStarted {
				item.ArgsStarted = true
				emitProgressEvent(progress, ProgressEvent{
					Kind:       progressKindModelStreamToolArgs,
					Provider:   "openai-codex",
					ToolName:   item.Name,
					ToolCallID: item.ID,
				})
			}
			item.Arguments.WriteString(event.Delta)
		case "response.function_call_arguments.done":
			item := ensureToolCall(event.OutputIndex)
			arguments := firstNonEmptyTrimmed(event.Arguments, event.Text)
			if strings.TrimSpace(arguments) != "" {
				item.Arguments.Reset()
				item.Arguments.WriteString(arguments)
			}
			emitToolReady(item)
		case "response.output_item.done":
			if event.Item.Type == "message" && text.Len() == 0 {
				for _, content := range event.Item.Content {
					if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
						text.WriteString(content.Text)
					}
				}
			} else if event.Item.Type == "function_call" {
				item := ensureToolCall(event.OutputIndex)
				item.ID = firstNonEmptyTrimmed(event.Item.CallID, event.Item.ID, item.ID)
				item.Name = firstNonEmptyTrimmed(event.Item.Name, item.Name)
				if strings.TrimSpace(event.Item.Arguments) != "" {
					item.Arguments.Reset()
					item.Arguments.WriteString(event.Item.Arguments)
				}
				emitToolReady(item)
			}
		case "response.completed":
			if len(event.Response) > 0 {
				completedResponse = append(completedResponse[:0], event.Response...)
			}
			stopReason = "completed"
		case "response.failed":
			if len(event.Response) > 0 {
				resp, err := parseOpenAICodexResponse(event.Response)
				if err != nil {
					return ChatResponse{}, err
				}
				return resp, nil
			}
			return ChatResponse{}, newProviderMessageError("openai-codex", "stream failed", "", "", nil, []byte(payload))
		case "response.incomplete":
			stopReason = "incomplete"
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
	if len(completedResponse) > 0 {
		resp, err := parseOpenAICodexResponse(completedResponse)
		if err == nil {
			emittedReady := map[string]bool{}
			for _, item := range toolCalls {
				if item == nil || !item.ReadyEmitted {
					continue
				}
				emittedReady[strings.TrimSpace(item.ID)] = true
			}
			for _, call := range resp.Message.ToolCalls {
				callID := strings.TrimSpace(call.ID)
				if emittedReady[callID] {
					continue
				}
				emitProgressEvent(progress, ProgressEvent{
					Kind:             progressKindModelStreamToolReady,
					Provider:         "openai-codex",
					ToolName:         call.Name,
					ToolCallID:       callID,
					ArgumentsPreview: summarizeToolArgumentsPreview(call.Arguments),
				})
			}
			return resp, nil
		}
	}

	out := Message{Role: "assistant", Text: strings.TrimSpace(text.String())}
	for _, index := range toolOrder {
		item := toolCalls[index]
		if item == nil || strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Name) == "" {
			continue
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        strings.TrimSpace(item.ID),
			Name:      strings.TrimSpace(item.Name),
			Arguments: normalizeOpenAIToolCallArguments(item.Arguments.String()),
		})
	}
	if out.Text == "" && len(out.ToolCalls) == 0 {
		return ChatResponse{}, newProviderMessageError("openai-codex", "empty Responses stream", "", "", nil, nil)
	}
	return ChatResponse{Message: out, StopReason: stopReason}, nil
}

func deduplicateOpenAICodexTexts(items []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		text := strings.TrimSpace(item)
		if text == "" || seen[text] {
			continue
		}
		out = append(out, text)
		seen[text] = true
	}
	return out
}

type CodexOAuthTokenSource struct {
	authPath   string
	httpClient *http.Client
}

func NewCodexOAuthTokenSource(authPath string, httpClient *http.Client) *CodexOAuthTokenSource {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &CodexOAuthTokenSource{
		authPath:   strings.TrimSpace(authPath),
		httpClient: httpClient,
	}
}

func (s *CodexOAuthTokenSource) AccessToken(ctx context.Context) (string, error) {
	if token := strings.TrimSpace(os.Getenv(openAICodexAccessTokenEnv)); token != "" {
		return token, nil
	}
	authPath := s.authPath
	if authPath == "" {
		authPath = codexOAuthAuthFilePath()
	}
	data, auth, err := readCodexOAuthAuthFile(authPath)
	if err != nil {
		return "", err
	}
	if tokenUsable(auth.Tokens.AccessToken, openAICodexTokenRefreshSkew) {
		return auth.Tokens.AccessToken, nil
	}

	codexOAuthRefreshMu.Lock()
	defer codexOAuthRefreshMu.Unlock()

	data, auth, err = readCodexOAuthAuthFile(authPath)
	if err != nil {
		return "", err
	}
	if tokenUsable(auth.Tokens.AccessToken, openAICodexTokenRefreshSkew) {
		return auth.Tokens.AccessToken, nil
	}
	if strings.TrimSpace(auth.Tokens.RefreshToken) == "" {
		return "", fmt.Errorf("OpenAI Codex OAuth access token is expired and no refresh token is available; run /codex-auth login")
	}
	refreshed, err := s.refresh(ctx, auth.Tokens.RefreshToken)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(refreshed.AccessToken) == "" {
		return "", fmt.Errorf("OpenAI Codex OAuth refresh returned no access token")
	}
	if err := updateCodexOAuthAuthFile(authPath, data, refreshed); err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

func readCodexOAuthAuthFile(path string) ([]byte, codexOAuthAuthFile, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = codexOAuthAuthFilePath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, codexOAuthAuthFile{}, fmt.Errorf("OpenAI Codex OAuth auth file not found: %s; run /codex-auth login or set %s", path, openAICodexAccessTokenEnv)
		}
		return nil, codexOAuthAuthFile{}, err
	}
	auth, err := parseCodexOAuthAuthFile(data)
	if err != nil {
		return nil, codexOAuthAuthFile{}, err
	}
	return data, auth, nil
}

func (s *CodexOAuthTokenSource) refresh(ctx context.Context, refreshToken string) (codexOAuthTokens, error) {
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", refreshToken)
	values.Set("client_id", openAICodexOAuthClientID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, openAICodexOAuthTokenEndpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return codexOAuthTokens{}, err
	}
	httpReq.Header.Set("content-type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("accept", "application/json")
	httpReq.Header.Set("user-agent", "kernforge/openai-codex")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return codexOAuthTokens{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return codexOAuthTokens{}, err
	}
	if resp.StatusCode >= 300 {
		return codexOAuthTokens{}, newProviderHTTPError("openai-codex", resp.StatusCode, resp.Status, data, "")
	}
	var decoded struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		IDToken          string `json:"id_token"`
		AccountID        string `json:"account_id"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return codexOAuthTokens{}, err
	}
	if strings.TrimSpace(decoded.Error) != "" {
		message := strings.TrimSpace(decoded.ErrorDescription)
		if message == "" {
			message = decoded.Error
		}
		return codexOAuthTokens{}, newProviderMessageError("openai-codex", message, decoded.Error, "", nil, data)
	}
	return codexOAuthTokens{
		AccessToken:  strings.TrimSpace(decoded.AccessToken),
		RefreshToken: strings.TrimSpace(decoded.RefreshToken),
		IDToken:      strings.TrimSpace(decoded.IDToken),
		AccountID:    strings.TrimSpace(decoded.AccountID),
	}, nil
}

type codexOAuthDeviceCode struct {
	DeviceAuthID string
	UserCode     string
	Verification string
	CodeVerifier string
	Interval     time.Duration
	ExpiresIn    time.Duration
	Raw          map[string]any
}

type codexOAuthDeviceToken struct {
	AuthorizationCode string
	CodeChallenge     string
	CodeVerifier      string
}

type codexOAuthAuthFile struct {
	AuthMode string           `json:"auth_mode"`
	Tokens   codexOAuthTokens `json:"tokens"`
}

type codexOAuthTokens struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
}

func parseCodexOAuthAuthFile(data []byte) (codexOAuthAuthFile, error) {
	var auth codexOAuthAuthFile
	if err := json.Unmarshal(data, &auth); err != nil {
		return codexOAuthAuthFile{}, err
	}
	if strings.TrimSpace(auth.Tokens.AccessToken) == "" && strings.TrimSpace(auth.Tokens.RefreshToken) == "" {
		return codexOAuthAuthFile{}, fmt.Errorf("OpenAI Codex OAuth auth file does not contain ChatGPT tokens")
	}
	return auth, nil
}

func runCodexOAuthDeviceLogin(ctx context.Context, writer io.Writer, authPath string, httpClient *http.Client) (codexOAuthTokens, error) {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	authPath = strings.TrimSpace(authPath)
	if authPath == "" {
		authPath = codexOAuthAuthFilePath()
	}
	device, err := requestCodexOAuthDeviceCode(ctx, httpClient)
	if err != nil {
		return codexOAuthTokens{}, err
	}
	if writer != nil {
		fmt.Fprintln(writer, "OpenAI Codex OAuth login")
		fmt.Fprintln(writer, "Open this URL and enter the code:")
		fmt.Fprintln(writer, "  "+valueOrFallback(device.Verification, openAICodexDeviceURL))
		fmt.Fprintln(writer, "  code: "+device.UserCode)
		fmt.Fprintln(writer, "Waiting for browser approval...")
	}
	pollCtx := ctx
	if device.ExpiresIn > 0 {
		var cancel context.CancelFunc
		pollCtx, cancel = context.WithTimeout(ctx, device.ExpiresIn)
		defer cancel()
	}
	deviceToken, err := pollCodexOAuthDeviceCode(pollCtx, httpClient, device)
	if err != nil {
		return codexOAuthTokens{}, err
	}
	codeVerifier := firstNonEmptyTrimmed(deviceToken.CodeVerifier, device.CodeVerifier)
	if codeVerifier == "" {
		return codexOAuthTokens{}, fmt.Errorf("OpenAI Codex OAuth device login returned no code verifier")
	}
	tokens, err := exchangeCodexOAuthAuthorizationCode(ctx, httpClient, deviceToken.AuthorizationCode, codeVerifier)
	if err != nil {
		return codexOAuthTokens{}, err
	}
	if strings.TrimSpace(tokens.AccessToken) == "" {
		return codexOAuthTokens{}, fmt.Errorf("OpenAI Codex OAuth login returned no access token")
	}
	if err := saveCodexOAuthAuthFile(authPath, tokens); err != nil {
		return codexOAuthTokens{}, err
	}
	if writer != nil {
		fmt.Fprintln(writer, "OpenAI Codex OAuth token saved to "+authPath)
	}
	return tokens, nil
}

func importCodexCLIOAuthAuthFile(destPath string) error {
	sourcePath := codexCLIOAuthAuthFilePath()
	if strings.TrimSpace(sourcePath) == "" {
		return fmt.Errorf("Codex CLI OAuth auth file path is unavailable")
	}
	_, auth, err := readCodexOAuthAuthFile(sourcePath)
	if err != nil {
		return err
	}
	return saveCodexOAuthAuthFile(destPath, auth.Tokens)
}

func requestCodexOAuthDeviceCode(ctx context.Context, httpClient *http.Client) (codexOAuthDeviceCode, error) {
	var data []byte
	var raw map[string]any
	var status int
	for attempt := 0; attempt < 3; attempt++ {
		var err error
		data, status, err = postCodexOAuthJSON(ctx, httpClient, openAICodexDeviceCodeEndpoint, map[string]string{
			"client_id": openAICodexOAuthClientID,
		})
		if err != nil {
			return codexOAuthDeviceCode{}, err
		}
		raw = map[string]any{}
		if len(data) > 0 {
			_ = json.Unmarshal(data, &raw)
		}
		if status < 300 {
			break
		}
		if codexOAuthShouldRetryTransient(status, raw, data) && attempt < 2 {
			if err := sleepCodexOAuthPoll(ctx, time.Duration(attempt+1)*time.Second); err != nil {
				return codexOAuthDeviceCode{}, err
			}
			continue
		}
		return codexOAuthDeviceCode{}, newProviderHTTPError("openai-codex", status, http.StatusText(status), data, "")
	}
	device := codexOAuthDeviceCode{
		DeviceAuthID: strings.TrimSpace(codexOAuthStringField(raw, "device_auth_id", "device_code", "device_id")),
		UserCode:     strings.TrimSpace(codexOAuthStringField(raw, "user_code", "code")),
		Verification: strings.TrimSpace(codexOAuthStringField(raw, "verification_uri_complete", "verification_url_complete", "verification_url", "verification_uri")),
		CodeVerifier: strings.TrimSpace(codexOAuthStringField(raw, "code_verifier")),
		Interval:     time.Duration(codexOAuthIntField(raw, "interval", "poll_interval", "polling_interval")) * time.Second,
		ExpiresIn:    time.Duration(codexOAuthIntField(raw, "expires_in", "expires")) * time.Second,
		Raw:          raw,
	}
	if device.Interval <= 0 {
		device.Interval = 5 * time.Second
	}
	if device.Verification == "" {
		device.Verification = openAICodexDeviceURL
	}
	if device.DeviceAuthID == "" {
		return codexOAuthDeviceCode{}, fmt.Errorf("OpenAI Codex OAuth device login returned no device auth id")
	}
	if device.UserCode == "" {
		return codexOAuthDeviceCode{}, fmt.Errorf("OpenAI Codex OAuth device login returned no user code")
	}
	return device, nil
}

func pollCodexOAuthDeviceCode(ctx context.Context, httpClient *http.Client, device codexOAuthDeviceCode) (codexOAuthDeviceToken, error) {
	interval := device.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	unknownAttempts := 0
	for {
		data, status, err := postCodexOAuthJSON(ctx, httpClient, openAICodexDeviceTokenEndpoint, map[string]string{
			"device_auth_id": device.DeviceAuthID,
			"user_code":      device.UserCode,
		})
		if err != nil {
			return codexOAuthDeviceToken{}, err
		}
		var raw map[string]any
		if len(data) > 0 {
			_ = json.Unmarshal(data, &raw)
		}
		if status < 300 {
			code := strings.TrimSpace(codexOAuthStringField(raw, "authorization_code", "code"))
			if code != "" {
				return codexOAuthDeviceToken{
					AuthorizationCode: code,
					CodeChallenge:     strings.TrimSpace(codexOAuthStringField(raw, "code_challenge")),
					CodeVerifier:      strings.TrimSpace(codexOAuthStringField(raw, "code_verifier")),
				}, nil
			}
			errCode := codexOAuthDeviceErrorCode(raw, data, "error", "error_code")
			switch errCode {
			case "":
			case "authorization_pending", "pending":
				if err := sleepCodexOAuthPoll(ctx, interval); err != nil {
					return codexOAuthDeviceToken{}, err
				}
				continue
			case "slow_down":
				interval += 5 * time.Second
				if err := sleepCodexOAuthPoll(ctx, interval); err != nil {
					return codexOAuthDeviceToken{}, err
				}
				continue
			case "deviceauth_authorization_unknown", "device_authorization_unknown", "authorization_unknown":
				unknownAttempts++
				if unknownAttempts <= 3 {
					if err := sleepCodexOAuthPoll(ctx, interval); err != nil {
						return codexOAuthDeviceToken{}, err
					}
					continue
				}
				return codexOAuthDeviceToken{}, fmt.Errorf("OpenAI Codex OAuth device authorization was not recognized; start /codex-auth login again")
			case "expired_token", "access_denied":
				message := strings.TrimSpace(codexOAuthStringField(raw, "error_description", "message", "detail"))
				if message == "" {
					message = errCode
				}
				return codexOAuthDeviceToken{}, newProviderMessageError("openai-codex", message, errCode, "", nil, data)
			default:
				return codexOAuthDeviceToken{}, newProviderMessageError("openai-codex", errCode, errCode, "", nil, data)
			}
			state := strings.ToLower(strings.TrimSpace(codexOAuthStringField(raw, "status", "state")))
			if state == "" || state == "pending" || state == "authorization_pending" {
				if err := sleepCodexOAuthPoll(ctx, interval); err != nil {
					return codexOAuthDeviceToken{}, err
				}
				continue
			}
			return codexOAuthDeviceToken{}, newProviderMessageError("openai-codex", "OpenAI Codex OAuth device login did not return an authorization code", state, "", nil, data)
		}
		errCode := codexOAuthDeviceErrorCode(raw, data, "error", "error_code", "code", "status", "state", "detail", "message")
		switch errCode {
		case "authorization_pending", "pending":
			if err := sleepCodexOAuthPoll(ctx, interval); err != nil {
				return codexOAuthDeviceToken{}, err
			}
		case "":
			if codexOAuthShouldRetryTransient(status, raw, data) {
				if err := sleepCodexOAuthPoll(ctx, interval); err != nil {
					return codexOAuthDeviceToken{}, err
				}
				continue
			}
			return codexOAuthDeviceToken{}, newProviderHTTPError("openai-codex", status, http.StatusText(status), data, "")
		case "slow_down":
			interval += 5 * time.Second
			if err := sleepCodexOAuthPoll(ctx, interval); err != nil {
				return codexOAuthDeviceToken{}, err
			}
		case "deviceauth_authorization_unknown", "device_authorization_unknown", "authorization_unknown":
			unknownAttempts++
			if unknownAttempts <= 3 {
				if err := sleepCodexOAuthPoll(ctx, interval); err != nil {
					return codexOAuthDeviceToken{}, err
				}
				continue
			}
			return codexOAuthDeviceToken{}, fmt.Errorf("OpenAI Codex OAuth device authorization was not recognized; start /codex-auth login again")
		case "expired_token", "access_denied":
			message := strings.TrimSpace(codexOAuthStringField(raw, "error_description", "message", "detail"))
			if message == "" {
				message = errCode
			}
			return codexOAuthDeviceToken{}, newProviderMessageError("openai-codex", message, errCode, "", nil, data)
		default:
			if codexOAuthShouldRetryTransient(status, raw, data) {
				if err := sleepCodexOAuthPoll(ctx, interval); err != nil {
					return codexOAuthDeviceToken{}, err
				}
				continue
			}
			return codexOAuthDeviceToken{}, newProviderHTTPError("openai-codex", status, http.StatusText(status), data, "")
		}
	}
}

func exchangeCodexOAuthAuthorizationCode(ctx context.Context, httpClient *http.Client, authorizationCode string, codeVerifier string) (codexOAuthTokens, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", strings.TrimSpace(authorizationCode))
	values.Set("redirect_uri", openAICodexDeviceRedirect)
	values.Set("client_id", openAICodexOAuthClientID)
	values.Set("code_verifier", strings.TrimSpace(codeVerifier))

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, openAICodexOAuthTokenEndpoint, strings.NewReader(values.Encode()))
		if err != nil {
			return codexOAuthTokens{}, err
		}
		httpReq.Header.Set("content-type", "application/x-www-form-urlencoded")
		httpReq.Header.Set("accept", "application/json")
		httpReq.Header.Set("user-agent", "kernforge/openai-codex")

		resp, err := httpClient.Do(httpReq)
		if err != nil {
			lastErr = err
			if attempt < 2 {
				if sleepErr := sleepCodexOAuthPoll(ctx, time.Duration(attempt+1)*time.Second); sleepErr != nil {
					return codexOAuthTokens{}, sleepErr
				}
				continue
			}
			return codexOAuthTokens{}, err
		}
		data, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return codexOAuthTokens{}, readErr
		}
		if closeErr != nil {
			return codexOAuthTokens{}, closeErr
		}
		var raw map[string]any
		if len(data) > 0 {
			_ = json.Unmarshal(data, &raw)
		}
		if resp.StatusCode >= 300 {
			err = newProviderHTTPError("openai-codex", resp.StatusCode, resp.Status, data, "")
			lastErr = err
			if codexOAuthShouldRetryTransient(resp.StatusCode, raw, data) && attempt < 2 {
				if sleepErr := sleepCodexOAuthPoll(ctx, time.Duration(attempt+1)*time.Second); sleepErr != nil {
					return codexOAuthTokens{}, sleepErr
				}
				continue
			}
			return codexOAuthTokens{}, err
		}
		var decoded struct {
			AccessToken      string `json:"access_token"`
			RefreshToken     string `json:"refresh_token"`
			IDToken          string `json:"id_token"`
			AccountID        string `json:"account_id"`
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if err := json.Unmarshal(data, &decoded); err != nil {
			return codexOAuthTokens{}, err
		}
		if strings.TrimSpace(decoded.Error) != "" {
			message := strings.TrimSpace(decoded.ErrorDescription)
			if message == "" {
				message = decoded.Error
			}
			err = newProviderMessageError("openai-codex", message, decoded.Error, "", nil, data)
			lastErr = err
			if codexOAuthShouldRetryTransient(resp.StatusCode, raw, data) && attempt < 2 {
				if sleepErr := sleepCodexOAuthPoll(ctx, time.Duration(attempt+1)*time.Second); sleepErr != nil {
					return codexOAuthTokens{}, sleepErr
				}
				continue
			}
			return codexOAuthTokens{}, err
		}
		return codexOAuthTokens{
			AccessToken:  strings.TrimSpace(decoded.AccessToken),
			RefreshToken: strings.TrimSpace(decoded.RefreshToken),
			IDToken:      strings.TrimSpace(decoded.IDToken),
			AccountID:    strings.TrimSpace(decoded.AccountID),
		}, nil
	}
	if lastErr != nil {
		return codexOAuthTokens{}, lastErr
	}
	return codexOAuthTokens{}, fmt.Errorf("OpenAI Codex OAuth token exchange failed")
}

func postCodexOAuthJSON(ctx context.Context, httpClient *http.Client, endpoint string, payload any) ([]byte, int, error) {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "application/json")
	httpReq.Header.Set("user-agent", "kernforge/openai-codex")
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

func sleepCodexOAuthPoll(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		delay = 5 * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func codexOAuthShouldRetryTransient(status int, raw map[string]any, data []byte) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	case http.StatusInternalServerError:
	default:
		return false
	}
	text := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		codexOAuthStringField(raw, "detail"),
		codexOAuthStringField(raw, "message"),
		codexOAuthStringField(raw, "error_description"),
		codexOAuthStringField(raw, "error"),
		string(data),
	}, " ")))
	return strings.Contains(text, "timeout") || strings.Contains(text, "temporar") || strings.Contains(text, "try again")
}

func saveCodexOAuthAuthFile(path string, tokens codexOAuthTokens) error {
	path = strings.TrimSpace(path)
	if path == "" {
		path = codexOAuthAuthFilePath()
	}
	root := map[string]any{
		"auth_mode":    "chatgpt",
		"source":       "kernforge",
		"created_at":   time.Now().UTC().Format(time.RFC3339),
		"last_refresh": time.Now().UTC().Format(time.RFC3339),
		"tokens": map[string]any{
			"access_token": strings.TrimSpace(tokens.AccessToken),
		},
	}
	tokenMap := root["tokens"].(map[string]any)
	if strings.TrimSpace(tokens.RefreshToken) != "" {
		tokenMap["refresh_token"] = strings.TrimSpace(tokens.RefreshToken)
	}
	if strings.TrimSpace(tokens.IDToken) != "" {
		tokenMap["id_token"] = strings.TrimSpace(tokens.IDToken)
	}
	if strings.TrimSpace(tokens.AccountID) != "" {
		tokenMap["account_id"] = strings.TrimSpace(tokens.AccountID)
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	next = append(next, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	unlock := lockFilePath(path)
	defer unlock()
	return atomicWriteFile(path, next, 0o600)
}

func updateCodexOAuthAuthFile(path string, original []byte, tokens codexOAuthTokens) error {
	root := map[string]any{}
	if err := json.Unmarshal(original, &root); err != nil {
		return err
	}
	tokenMap, _ := root["tokens"].(map[string]any)
	if tokenMap == nil {
		tokenMap = map[string]any{}
	}
	if strings.TrimSpace(tokens.AccessToken) != "" {
		tokenMap["access_token"] = strings.TrimSpace(tokens.AccessToken)
	}
	if strings.TrimSpace(tokens.RefreshToken) != "" {
		tokenMap["refresh_token"] = strings.TrimSpace(tokens.RefreshToken)
	}
	if strings.TrimSpace(tokens.IDToken) != "" {
		tokenMap["id_token"] = strings.TrimSpace(tokens.IDToken)
	}
	if strings.TrimSpace(tokens.AccountID) != "" {
		tokenMap["account_id"] = strings.TrimSpace(tokens.AccountID)
	}
	root["tokens"] = tokenMap
	root["auth_mode"] = "chatgpt"
	root["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	next = append(next, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	unlock := lockFilePath(path)
	defer unlock()
	return atomicWriteFile(path, next, 0o600)
}

func codexOAuthAuthFilePath() string {
	if path := strings.TrimSpace(os.Getenv(openAICodexAuthFileEnv)); path != "" {
		return expandHome(path)
	}
	return filepath.Join(userConfigDir(), openAICodexDefaultAuthFile)
}

func codexCLIOAuthAuthFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "auth.json")
}

func codexOAuthAuthFileUsable(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		path = codexOAuthAuthFilePath()
	}
	_, auth, err := readCodexOAuthAuthFile(path)
	if err != nil {
		return false
	}
	return tokenUsable(auth.Tokens.AccessToken, openAICodexTokenRefreshSkew) || strings.TrimSpace(auth.Tokens.RefreshToken) != ""
}

func codexOAuthStringField(raw map[string]any, keys ...string) string {
	if raw == nil {
		return ""
	}
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return typed
			}
		case fmt.Stringer:
			if strings.TrimSpace(typed.String()) != "" {
				return typed.String()
			}
		}
	}
	return ""
}

func codexOAuthDeviceErrorCode(raw map[string]any, data []byte, keys ...string) string {
	code := strings.ToLower(strings.TrimSpace(codexOAuthStringField(raw, keys...)))
	if code == "" {
		if nested, ok := raw["error"].(map[string]any); ok {
			code = strings.ToLower(strings.TrimSpace(codexOAuthStringField(nested, "code", "error_code", "error", "status", "state", "type", "message", "detail")))
		}
	}
	text := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		code,
		codexOAuthStringField(raw, "detail"),
		codexOAuthStringField(raw, "message"),
		codexOAuthStringField(raw, "error_description"),
		string(data),
	}, " ")))
	if strings.Contains(text, "deviceauth_authorization_unknown") ||
		strings.Contains(text, "device authorization is unknown") ||
		strings.Contains(text, "authorization is unknown") {
		return "deviceauth_authorization_unknown"
	}
	return code
}

func codexOAuthIntField(raw map[string]any, keys ...string) int {
	if raw == nil {
		return 0
	}
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return int(typed)
		case int:
			return typed
		case json.Number:
			intValue, err := typed.Int64()
			if err == nil {
				return int(intValue)
			}
		case string:
			var intValue int
			if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &intValue); err == nil {
				return intValue
			}
		}
	}
	return 0
}

func valueOrFallback(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}

func tokenUsable(token string, skew time.Duration) bool {
	expiresAt, ok := jwtExpiresAt(token)
	if !ok {
		return strings.TrimSpace(token) != ""
	}
	return time.Now().Add(skew).Before(expiresAt)
}

func jwtExpiresAt(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

func normalizeOpenAICodexBaseURL(baseURL string) string {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = openAICodexDefaultBaseURL
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return strings.TrimRight(base, "/")
}

func openAICodexAPIURL(baseURL string, path string) string {
	base := normalizeOpenAICodexBaseURL(baseURL)
	normalizedPath := "/" + strings.TrimLeft(path, "/")
	if strings.HasSuffix(strings.ToLower(base), strings.ToLower(normalizedPath)) {
		return base
	}
	return base + normalizedPath
}

func newOpenAICodexRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}
