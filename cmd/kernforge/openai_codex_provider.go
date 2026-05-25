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
	"sort"
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
	openAICodexInstallationFile = "installation_id"
	openAICodexSSEMaxLineBytes  = 16 * 1024 * 1024
)

const (
	openAICodexServerModelHeader       = "OpenAI-Model"
	openAICodexServerModelsETagHeader  = "X-Models-Etag"
	openAICodexReasoningIncludedHeader = "x-reasoning-included"
	openAICodexTrustedAccessForCyber   = "trusted_access_for_cyber"
	openAICodexInstallationIDHeader    = "x-codex-installation-id"
	openAICodexParentThreadIDHeader    = "x-codex-parent-thread-id"
	openAICodexSubagentHeader          = "x-openai-subagent"
	openAICodexWindowIDHeader          = "x-codex-window-id"
)

const (
	openAICodexSubagentReview              = "review"
	openAICodexSubagentCompact             = "compact"
	openAICodexSubagentMemoryConsolidation = "memory_consolidation"
	openAICodexSubagentCollabSpawn         = "collab_spawn"
)

const openAICodexApplyPatchDescription = "Use the `apply_patch` tool to edit files. This is a FREEFORM tool, so do not wrap the patch in JSON."
const openAICodexImageContentOmittedPlaceholder = "image content omitted because you do not support image input"

const openAICodexApplyPatchLarkGrammar = `start: begin_patch hunk+ end_patch
begin_patch: "*** Begin Patch" LF
end_patch: "*** End Patch" LF?

hunk: add_hunk | delete_hunk | update_hunk
add_hunk: "*** Add File: " filename LF add_line+
delete_hunk: "*** Delete File: " filename LF
update_hunk: "*** Update File: " filename LF change_move? change?

filename: /(.+)/
add_line: "+" /(.*)/ LF -> line

change_move: "*** Move to: " filename LF
change: (change_context | change_line)+ eof_line?
change_context: ("@@" | "@@ " /(.+)/) LF
change_line: ("+" | "-" | " ") /(.*)/ LF
eof_line: "*** End of File" LF

%import common.LF
`

var (
	openAICodexOAuthTokenEndpoint  = openAICodexOAuthTokenURL
	openAICodexDeviceCodeEndpoint  = openAICodexDeviceCodeURL
	openAICodexDeviceTokenEndpoint = openAICodexDeviceTokenURL
)

var openAICodexVerbosityDefaults = struct {
	sync.RWMutex
	byModel map[string]string
}{
	byModel: map[string]string{
		openAICodexDefaultModel: "low",
		"gpt-5.4":               "low",
		"gpt-5.4-mini":          "medium",
		"gpt-5.3-codex":         "low",
		"gpt-5.2":               "low",
		"codex-auto-review":     "low",
	},
}

type openAICodexReasoningModelDefaults struct {
	SupportsSummaries bool
	DefaultEffort     string
	DefaultSummary    string
}

var openAICodexReasoningDefaults = struct {
	sync.RWMutex
	byModel map[string]openAICodexReasoningModelDefaults
}{
	byModel: map[string]openAICodexReasoningModelDefaults{
		openAICodexDefaultModel: {SupportsSummaries: true, DefaultEffort: "medium", DefaultSummary: "none"},
		"gpt-5.4":               {SupportsSummaries: true, DefaultEffort: "medium", DefaultSummary: "none"},
		"gpt-5.4-mini":          {SupportsSummaries: true, DefaultEffort: "medium", DefaultSummary: "none"},
		"gpt-5.3-codex":         {SupportsSummaries: true, DefaultEffort: "medium", DefaultSummary: "none"},
		"gpt-5.2":               {SupportsSummaries: true, DefaultEffort: "medium", DefaultSummary: "auto"},
		"codex-auto-review":     {SupportsSummaries: true, DefaultEffort: "medium", DefaultSummary: "none"},
	},
}

var openAICodexParallelToolCallSupport = struct {
	sync.RWMutex
	byModel map[string]bool
}{
	byModel: map[string]bool{
		openAICodexDefaultModel: true,
		"gpt-5.4":               true,
		"gpt-5.4-mini":          true,
		"gpt-5.3-codex":         true,
		"gpt-5.2":               true,
		"codex-auto-review":     true,
	},
}

var openAICodexServiceTierSupport = struct {
	sync.RWMutex
	byModel map[string]map[string]bool
}{
	byModel: map[string]map[string]bool{
		openAICodexDefaultModel: map[string]bool{"priority": true},
		"gpt-5.4":               map[string]bool{"priority": true},
	},
}

type codexOAuthAccessTokenSource interface {
	AccessToken(ctx context.Context) (string, error)
}

type codexOAuthUnauthorizedRecovery interface {
	RefreshAfterUnauthorized(ctx context.Context) (string, error)
}

var codexOAuthRefreshMu sync.Mutex

type OpenAICodexClient struct {
	baseURL             string
	reasoningEffort     string
	serviceTier         string
	httpClient          *http.Client
	tokenSource         codexOAuthAccessTokenSource
	allowedWorkspaceIDs []string
}

func NewOpenAICodexClient(baseURL string) *OpenAICodexClient {
	return NewOpenAICodexClientWithReasoningEffort(baseURL, "")
}

func NewOpenAICodexClientWithReasoningEffort(baseURL string, reasoningEffort string) *OpenAICodexClient {
	return NewOpenAICodexClientWithReasoningEffortAndWorkspaceIDs(baseURL, reasoningEffort, nil)
}

func NewOpenAICodexClientWithReasoningEffortAndWorkspaceIDs(baseURL string, reasoningEffort string, allowedWorkspaceIDs []string) *OpenAICodexClient {
	return NewOpenAICodexClientWithReasoningEffortServiceTierAndWorkspaceIDs(baseURL, reasoningEffort, "", allowedWorkspaceIDs)
}

func NewOpenAICodexClientWithReasoningEffortServiceTierAndWorkspaceIDs(baseURL string, reasoningEffort string, serviceTier string, allowedWorkspaceIDs []string) *OpenAICodexClient {
	httpClient := &http.Client{}
	normalizedWorkspaces := normalizeForcedChatGPTWorkspaceIDs(allowedWorkspaceIDs)
	return &OpenAICodexClient{
		baseURL:             normalizeOpenAICodexBaseURL(baseURL),
		reasoningEffort:     normalizeReasoningEffort(reasoningEffort),
		serviceTier:         normalizeServiceTier(serviceTier),
		httpClient:          httpClient,
		tokenSource:         NewCodexOAuthTokenSourceWithWorkspaceIDs("", httpClient, normalizedWorkspaces),
		allowedWorkspaceIDs: append([]string(nil), normalizedWorkspaces...),
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
		ServiceTier:     c.serviceTier,
	}
}

func (c *OpenAICodexClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if c == nil {
		return ChatResponse{}, fmt.Errorf("OpenAI Codex provider is not configured")
	}
	if strings.TrimSpace(req.ReasoningEffort) == "" {
		req.ReasoningEffort = c.reasoningEffort
	}
	if strings.TrimSpace(req.ServiceTier) == "" {
		req.ServiceTier = c.serviceTier
	}
	tokenSource := c.tokenSource
	if tokenSource == nil {
		tokenSource = NewCodexOAuthTokenSourceWithWorkspaceIDs("", c.httpClient, c.allowedWorkspaceIDs)
	}
	accessToken, err := tokenSource.AccessToken(ctx)
	if err != nil {
		return ChatResponse{}, err
	}

	requestID := newOpenAICodexRequestID()
	sessionID := firstNonBlankString(req.SessionID, requestID)
	threadID := firstNonBlankString(req.ThreadID, sessionID)
	req.SessionID = sessionID
	req.ThreadID = threadID
	clientMetadata := map[string]string{}
	if installationID := resolveOpenAICodexInstallationID(); installationID != "" {
		clientMetadata[openAICodexInstallationIDHeader] = installationID
	}
	windowID := openAICodexWindowID(threadID)
	if windowID != "" {
		clientMetadata[openAICodexWindowIDHeader] = windowID
	}
	subagent := openAICodexRequestHeaderValue(req.CodexSubagent)
	parentThreadID := openAICodexRequestHeaderValue(req.CodexParentThreadID)
	if subagent != "" {
		clientMetadata[openAICodexSubagentHeader] = subagent
	}
	if parentThreadID != "" {
		clientMetadata[openAICodexParentThreadIDHeader] = parentThreadID
	}

	body, err := buildOpenAICodexRequestBodyWithClientMetadataAndOptions(req, clientMetadata, openAICodexRequestBodyOptions{
		Store: openAICodexIsAzureResponsesEndpoint(c.baseURL),
	})
	if err != nil {
		return ChatResponse{}, err
	}
	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	newHTTPRequest := func(token string) (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, openAICodexAPIURL(c.baseURL, "/responses"), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("content-type", "application/json")
		httpReq.Header.Set("accept", "text/event-stream")
		applyOpenAICodexAuthHeaders(httpReq.Header, token)
		httpReq.Header.Set("originator", "codex_cli_rs")
		httpReq.Header.Set("user-agent", "kernforge/openai-codex")
		httpReq.Header.Set("session-id", sessionID)
		httpReq.Header.Set("thread-id", threadID)
		httpReq.Header.Set("x-client-request-id", threadID)
		if windowID != "" {
			httpReq.Header.Set(openAICodexWindowIDHeader, windowID)
		}
		if subagent != "" {
			httpReq.Header.Set(openAICodexSubagentHeader, subagent)
		}
		if parentThreadID != "" {
			httpReq.Header.Set(openAICodexParentThreadIDHeader, parentThreadID)
		}
		applyProviderTurnStateHeader(httpReq, req.TurnState)
		applyProviderTurnMetadataHeader(httpReq, req.TurnMetadata)
		return httpReq, nil
	}

	for attempt := 0; ; attempt++ {
		httpReq, err := newHTTPRequest(accessToken)
		if err != nil {
			return ChatResponse{}, err
		}
		resp, err := httpClient.Do(httpReq)
		if err != nil {
			return ChatResponse{}, err
		}
		captureProviderTurnStateHeader(resp, req.TurnState)

		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			if recovery, ok := openAICodexUnauthorizedRecovery(tokenSource); ok {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				refreshedToken, err := refreshOpenAICodexTokenAfterUnauthorized(ctx, recovery)
				if err != nil {
					return ChatResponse{}, err
				}
				accessToken = refreshedToken
				continue
			}
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 300 {
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return ChatResponse{}, err
			}
			return ChatResponse{}, newProviderHTTPErrorWithHeaders("openai-codex", resp.StatusCode, resp.Status, data, summarizeOpenAIRequestBody(body), resp.Header)
		}
		out, err := readOpenAICodexStreamWithOptions(ctx, resp.Body, openAICodexStreamOptions{
			OnProgressEvent: req.OnProgressEvent,
			SessionID:       sessionID,
			ImageOutputRoot: userConfigDir(),
		})
		if err != nil {
			return ChatResponse{}, err
		}
		if serverModel := strings.TrimSpace(resp.Header.Get(openAICodexServerModelHeader)); serverModel != "" {
			out.ServerModel = serverModel
		}
		out.ModelsETag = strings.TrimSpace(resp.Header.Get(openAICodexServerModelsETagHeader))
		out.ReasoningIncluded = resp.Header.Get(openAICodexReasoningIncludedHeader) != ""
		if summary := providerCodexRateLimitSummaryFromHeaders(resp.Header); summary != "" {
			out.RateLimitSummary = summary
		}
		return out, nil
	}
}

func openAICodexUnauthorizedRecovery(tokenSource codexOAuthAccessTokenSource) (codexOAuthUnauthorizedRecovery, bool) {
	recovery, ok := tokenSource.(codexOAuthUnauthorizedRecovery)
	if !ok || !codexOAuthCanRecoverAfterUnauthorized(tokenSource) {
		return nil, false
	}
	return recovery, true
}

func refreshOpenAICodexTokenAfterUnauthorized(ctx context.Context, recovery codexOAuthUnauthorizedRecovery) (string, error) {
	refreshedToken, err := recovery.RefreshAfterUnauthorized(ctx)
	if err != nil {
		return "", fmt.Errorf("OpenAI Codex OAuth recovery after 401 failed: %w", err)
	}
	refreshedToken = strings.TrimSpace(refreshedToken)
	if refreshedToken == "" {
		return "", fmt.Errorf("OpenAI Codex OAuth recovery after 401 returned no access token")
	}
	return refreshedToken, nil
}

func codexOAuthCanRecoverAfterUnauthorized(tokenSource codexOAuthAccessTokenSource) bool {
	if _, ok := tokenSource.(*CodexOAuthTokenSource); ok && strings.TrimSpace(os.Getenv(openAICodexAccessTokenEnv)) != "" {
		return false
	}
	return true
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

	newHTTPRequest := func(token string) (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("accept", "application/json")
		applyOpenAICodexAuthHeaders(httpReq.Header, token)
		httpReq.Header.Set("originator", "codex_cli_rs")
		httpReq.Header.Set("user-agent", "kernforge/openai-codex")
		return httpReq, nil
	}

	for attempt := 0; ; attempt++ {
		httpReq, err := newHTTPRequest(accessToken)
		if err != nil {
			return nil, err
		}
		resp, err := httpClient.Do(httpReq)
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			if recovery, ok := openAICodexUnauthorizedRecovery(tokenSource); ok {
				refreshedToken, err := refreshOpenAICodexTokenAfterUnauthorized(ctx, recovery)
				if err != nil {
					return nil, err
				}
				accessToken = refreshedToken
				continue
			}
		}
		if resp.StatusCode >= 300 {
			return nil, newProviderHTTPErrorWithHeaders("openai-codex", resp.StatusCode, resp.Status, data, "", resp.Header)
		}
		return parseCodexCLIModelsJSON(data)
	}
}

func applyOpenAICodexAuthHeaders(headers http.Header, accessToken string) {
	headers.Set("authorization", "Bearer "+accessToken)
	if accountID := codexOAuthJWTChatGPTAccountID(accessToken); accountID != "" {
		headers.Set("chatgpt-account-id", accountID)
	}
}

func openAICodexWindowID(threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	return threadID + ":0"
}

func openAICodexRequestHeaderValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

func buildOpenAICodexRequestBody(req ChatRequest) ([]byte, error) {
	return buildOpenAICodexRequestBodyWithClientMetadata(req, nil)
}

func buildOpenAICodexRequestBodyWithClientMetadata(req ChatRequest, clientMetadata map[string]string) ([]byte, error) {
	return buildOpenAICodexRequestBodyWithClientMetadataAndOptions(req, clientMetadata, openAICodexRequestBodyOptions{})
}

type openAICodexRequestBodyOptions struct {
	Store bool
}

func buildOpenAICodexRequestBodyWithClientMetadataAndOptions(req ChatRequest, clientMetadata map[string]string, options openAICodexRequestBodyOptions) ([]byte, error) {
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
		"store":               options.Store,
		"stream":              true,
		"include":             []string{},
		"tools":               []map[string]any{},
		"tool_choice":         "auto",
		"parallel_tool_calls": openAICodexSupportsParallelToolCalls(model),
		"reasoning":           nil,
	}
	if threadID := strings.TrimSpace(req.ThreadID); threadID != "" {
		payload["prompt_cache_key"] = threadID
	}
	if serviceTier := openAICodexServiceTierForRequest(model, req.ServiceTier); serviceTier != "" {
		payload["service_tier"] = serviceTier
	}
	if len(clientMetadata) > 0 {
		metadata := map[string]string{}
		for key, value := range clientMetadata {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key != "" && value != "" {
				metadata[key] = value
			}
		}
		if len(metadata) > 0 {
			payload["client_metadata"] = metadata
		}
	}
	if strings.TrimSpace(req.System) != "" {
		payload["instructions"] = req.System
	}
	if strings.TrimSpace(req.ReasoningEffort) != "" && !validReasoningEffort(req.ReasoningEffort) {
		return nil, fmt.Errorf("invalid reasoning effort %q; use undefined, minimal, low, medium, high, or xhigh", strings.TrimSpace(req.ReasoningEffort))
	}
	if reasoning, ok := openAICodexReasoningPayload(model, req.ReasoningEffort); ok {
		payload["reasoning"] = reasoning
		payload["include"] = []string{"reasoning.encrypted_content"}
	}
	if textControls := openAICodexTextControls(model, req.JSONMode); len(textControls) > 0 {
		payload["text"] = textControls
	}
	tools := make([]map[string]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		item := openAICodexToolPayload(tool)
		if len(item) == 0 {
			continue
		}
		tools = append(tools, item)
	}
	tools = mergeOpenAICodexNamespaceTools(tools)
	payload["tools"] = tools
	return json.Marshal(payload)
}

func openAICodexIsAzureResponsesEndpoint(baseURL string) bool {
	baseURL = strings.ToLower(strings.TrimSpace(baseURL))
	if baseURL == "" {
		return false
	}
	markers := []string{
		"openai.azure.",
		"cognitiveservices.azure.",
		"aoai.azure.",
		"azure-api.",
		"azurefd.",
		"windows.net/openai",
	}
	for _, marker := range markers {
		if strings.Contains(baseURL, marker) {
			return true
		}
	}
	return false
}

func openAICodexServiceTierForRequest(model string, serviceTier string) string {
	normalized := normalizeServiceTier(serviceTier)
	if normalized == "" {
		return ""
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || model == codexCLIDefaultModel {
		model = openAICodexDefaultModel
	}
	openAICodexServiceTierSupport.RLock()
	supported := openAICodexServiceTierSupport.byModel[model][normalized]
	openAICodexServiceTierSupport.RUnlock()
	if supported {
		return normalized
	}
	return ""
}

func openAICodexTextControls(model string, jsonMode bool) map[string]any {
	text := map[string]any{}
	if verbosity := openAICodexDefaultVerbosity(model); verbosity != "" {
		text["verbosity"] = verbosity
	}
	if jsonMode {
		text["format"] = openAICodexJSONModeTextFormat()
	}
	return text
}

func openAICodexJSONModeTextFormat() map[string]any {
	return map[string]any{
		"type":   "json_schema",
		"strict": false,
		"name":   "codex_json_object",
		"schema": map[string]any{
			"type":                 "object",
			"additionalProperties": true,
		},
	}
}

func openAICodexDefaultVerbosity(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || model == codexCLIDefaultModel {
		model = openAICodexDefaultModel
	}
	openAICodexVerbosityDefaults.RLock()
	verbosity := openAICodexVerbosityDefaults.byModel[model]
	openAICodexVerbosityDefaults.RUnlock()
	return normalizeOpenAICodexVerbosity(verbosity)
}

func registerOpenAICodexDefaultVerbosity(model string, verbosity string) {
	model = strings.ToLower(strings.TrimSpace(model))
	verbosity = normalizeOpenAICodexVerbosity(verbosity)
	if model == "" || verbosity == "" {
		return
	}
	openAICodexVerbosityDefaults.Lock()
	openAICodexVerbosityDefaults.byModel[model] = verbosity
	openAICodexVerbosityDefaults.Unlock()
}

func openAICodexReasoningPayload(model string, requestedEffort string) (map[string]any, bool) {
	defaults, ok := openAICodexReasoningDefaultsForModel(model)
	if !ok || !defaults.SupportsSummaries {
		return nil, false
	}
	reasoning := map[string]any{}
	effort := normalizeReasoningEffort(requestedEffort)
	if effort == "" {
		effort = defaults.DefaultEffort
	}
	if effort != "" {
		reasoning["effort"] = effort
	}
	if summary := normalizeOpenAICodexReasoningSummary(defaults.DefaultSummary); summary != "" && summary != "none" {
		reasoning["summary"] = summary
	}
	return reasoning, true
}

func openAICodexReasoningDefaultsForModel(model string) (openAICodexReasoningModelDefaults, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || model == codexCLIDefaultModel {
		model = openAICodexDefaultModel
	}
	openAICodexReasoningDefaults.RLock()
	defaults, ok := openAICodexReasoningDefaults.byModel[model]
	openAICodexReasoningDefaults.RUnlock()
	defaults.DefaultEffort = normalizeReasoningEffort(defaults.DefaultEffort)
	defaults.DefaultSummary = normalizeOpenAICodexReasoningSummary(defaults.DefaultSummary)
	return defaults, ok
}

func registerOpenAICodexReasoningDefaults(model string, supportsSummaries bool, defaultEffort string, defaultSummary string) {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return
	}
	defaults := openAICodexReasoningModelDefaults{
		SupportsSummaries: supportsSummaries,
		DefaultEffort:     normalizeReasoningEffort(defaultEffort),
		DefaultSummary:    normalizeOpenAICodexReasoningSummary(defaultSummary),
	}
	openAICodexReasoningDefaults.Lock()
	openAICodexReasoningDefaults.byModel[model] = defaults
	openAICodexReasoningDefaults.Unlock()
}

func openAICodexSupportsParallelToolCalls(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || model == codexCLIDefaultModel {
		model = openAICodexDefaultModel
	}
	openAICodexParallelToolCallSupport.RLock()
	supported, ok := openAICodexParallelToolCallSupport.byModel[model]
	openAICodexParallelToolCallSupport.RUnlock()
	return ok && supported
}

func registerOpenAICodexParallelToolCallSupport(model string, supported bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return
	}
	openAICodexParallelToolCallSupport.Lock()
	openAICodexParallelToolCallSupport.byModel[model] = supported
	openAICodexParallelToolCallSupport.Unlock()
}

func normalizeOpenAICodexVerbosity(verbosity string) string {
	switch strings.ToLower(strings.TrimSpace(verbosity)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(verbosity))
	default:
		return ""
	}
}

func normalizeOpenAICodexReasoningSummary(summary string) string {
	switch strings.ToLower(strings.TrimSpace(summary)) {
	case "auto", "concise", "detailed", "none":
		return strings.ToLower(strings.TrimSpace(summary))
	default:
		return ""
	}
}

func openAICodexToolPayload(tool ToolDefinition) map[string]any {
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		return nil
	}
	if name == "apply_patch" {
		return map[string]any{
			"type":        "custom",
			"name":        name,
			"description": openAICodexApplyPatchDescription,
			"format": map[string]any{
				"type":       "grammar",
				"syntax":     "lark",
				"definition": openAICodexApplyPatchLarkGrammar,
			},
		}
	}
	if name == "tool_search" {
		description := strings.TrimSpace(tool.Description)
		if description == "" {
			description = "Searches over deferred tool metadata and exposes matching tools for the next model call."
		}
		return map[string]any{
			"type":        "tool_search",
			"execution":   "client",
			"description": description,
			"parameters":  openAICodexToolParameters(tool.InputSchema),
		}
	}
	if name == "image_generation" {
		return map[string]any{
			"type":          "image_generation",
			"output_format": "png",
		}
	}
	if name == "web_search" {
		return openAICodexWebSearchToolPayload(tool)
	}
	if namespace, childName, ok := splitOpenAICodexMCPToolName(name); ok {
		description := strings.TrimSpace(tool.Description)
		item := map[string]any{
			"type":        "namespace",
			"name":        namespace,
			"description": openAICodexNamespaceDescription(namespace, ""),
			"tools": []any{
				map[string]any{
					"type":        "function",
					"name":        childName,
					"description": description,
					"strict":      false,
					"parameters":  openAICodexToolParameters(tool.InputSchema),
				},
			},
		}
		if tool.DeferLoading {
			children := item["tools"].([]any)
			child := children[0].(map[string]any)
			child["defer_loading"] = true
		}
		return item
	}
	item := map[string]any{
		"type":        "function",
		"name":        name,
		"description": strings.TrimSpace(tool.Description),
		"strict":      false,
		"parameters":  openAICodexToolParameters(tool.InputSchema),
	}
	if tool.DeferLoading {
		item["defer_loading"] = true
	}
	return item
}

func openAICodexWebSearchToolPayload(tool ToolDefinition) map[string]any {
	item := map[string]any{
		"type":                "web_search",
		"external_web_access": false,
	}
	options := tool.HostedOptions
	if len(options) == 0 {
		return item
	}
	if _, ok := options["external_web_access"]; ok {
		item["external_web_access"] = boolValue(options, "external_web_access", false)
	}
	for _, key := range []string{"filters", "user_location"} {
		if nested, ok := options[key].(map[string]any); ok && len(nested) > 0 {
			item[key] = cloneToolDefinitionMap(nested)
		}
	}
	if searchContextSize := strings.TrimSpace(stringValue(options, "search_context_size")); searchContextSize != "" {
		item["search_context_size"] = searchContextSize
	}
	if searchContentTypes := stringSliceValue(options, "search_content_types"); len(searchContentTypes) > 0 {
		item["search_content_types"] = searchContentTypes
	}
	return item
}

func splitOpenAICodexMCPToolName(name string) (string, string, bool) {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, "mcp__") {
		return "", "", false
	}
	index := strings.LastIndex(name, "__")
	if index <= len("mcp__") || index+len("__") >= len(name) {
		return "", "", false
	}
	namespace := name[:index+len("__")]
	childName := name[index+len("__"):]
	if namespace == "" || childName == "" {
		return "", "", false
	}
	return namespace, childName, true
}

func openAICodexNamespaceDescription(namespace string, description string) string {
	description = strings.TrimSpace(description)
	if description != "" {
		return description
	}
	return fmt.Sprintf("Tools in the %s namespace.", strings.TrimSpace(namespace))
}

func mergeOpenAICodexNamespaceTools(tools []map[string]any) []map[string]any {
	if len(tools) == 0 {
		return tools
	}
	merged := make([]map[string]any, 0, len(tools))
	namespaceIndices := map[string]int{}
	for _, tool := range tools {
		if strings.TrimSpace(stringsValueFromAny(tool["type"])) != "namespace" {
			merged = append(merged, tool)
			continue
		}
		namespace := strings.TrimSpace(stringsValueFromAny(tool["name"]))
		if namespace == "" {
			merged = append(merged, tool)
			continue
		}
		existingIndex, ok := namespaceIndices[namespace]
		if !ok {
			namespaceIndices[namespace] = len(merged)
			merged = append(merged, tool)
			continue
		}
		existing := merged[existingIndex]
		if strings.TrimSpace(stringsValueFromAny(existing["description"])) == "" {
			if description := strings.TrimSpace(stringsValueFromAny(tool["description"])); description != "" {
				existing["description"] = description
			}
		}
		existing["tools"] = appendOpenAICodexNamespaceToolItems(existing["tools"], tool["tools"])
	}
	for _, tool := range merged {
		if strings.TrimSpace(stringsValueFromAny(tool["type"])) != "namespace" {
			continue
		}
		if strings.TrimSpace(stringsValueFromAny(tool["description"])) == "" {
			if namespace := strings.TrimSpace(stringsValueFromAny(tool["name"])); namespace != "" {
				tool["description"] = openAICodexNamespaceDescription(namespace, "")
			}
		}
		sortOpenAICodexNamespaceTools(tool)
	}
	return merged
}

func appendOpenAICodexNamespaceToolItems(existing any, incoming any) []any {
	items := make([]any, 0)
	if current, ok := existing.([]any); ok {
		items = append(items, current...)
	}
	if next, ok := incoming.([]any); ok {
		items = append(items, next...)
	}
	return items
}

func sortOpenAICodexNamespaceTools(namespace map[string]any) {
	items, ok := namespace["tools"].([]any)
	if !ok || len(items) < 2 {
		return
	}
	sort.SliceStable(items, func(i int, j int) bool {
		left, _ := items[i].(map[string]any)
		right, _ := items[j].(map[string]any)
		return strings.TrimSpace(stringsValueFromAny(left["name"])) < strings.TrimSpace(stringsValueFromAny(right["name"]))
	})
	namespace["tools"] = items
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
	messages := ensureOpenAICodexToolCallResponses(req.Messages)
	supportsImages := canRequestImageInput("openai-codex", req.Model)
	items := make([]any, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			continue
		case "developer":
			if strings.TrimSpace(msg.Text) == "" {
				continue
			}
			items = append(items, map[string]any{
				"type": "message",
				"role": "developer",
				"content": []map[string]any{{
					"type": "input_text",
					"text": msg.Text,
				}},
			})
		case "user":
			content, err := openAICodexUserContent(req.WorkingDir, msg, supportsImages)
			if err != nil {
				return nil, err
			}
			if len(content) == 0 {
				continue
			}
			items = append(items, map[string]any{
				"type":    "message",
				"role":    "user",
				"content": content,
			})
		case "assistant":
			items = append(items, openAICodexAssistantCompactionInputItems(msg.CodexCompactionItems)...)
			if reasoningItem := openAICodexReasoningInputItem(msg); reasoningItem != nil {
				items = append(items, reasoningItem)
			}
			if strings.TrimSpace(msg.Text) != "" {
				item := map[string]any{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{{
						"type": "output_text",
						"text": msg.Text,
					}},
				}
				if phase := openAICodexInputMessagePhase(msg); phase != "" {
					item["phase"] = phase
				}
				items = append(items, item)
			}
			for _, tc := range msg.ToolCalls {
				callID := openAICodexToolCallHistoryCallID(tc)
				if callID == "" && strings.TrimSpace(tc.Name) != "tool_search" {
					continue
				}
				if strings.TrimSpace(tc.Name) == "" {
					continue
				}
				items = append(items, openAICodexToolCallInputItem(callID, tc))
			}
			items = append(items, openAICodexAssistantLocalShellInputItems(msg.LocalShellCalls)...)
			items = append(items, openAICodexAssistantToolOutputInputItems(msg.CodexToolOutputItems, supportsImages)...)
			items = append(items, openAICodexAssistantImageGenerationInputItems(req.WorkingDir, msg.Images, supportsImages)...)
			items = append(items, openAICodexAssistantWebSearchInputItems(msg.WebSearchCalls)...)
		case "tool":
			callID := firstNonEmptyTrimmed(msg.ToolCallID, msg.ToolName)
			if callID == "" {
				continue
			}
			items = append(items, openAICodexToolCallOutputItem(callID, msg, supportsImages))
		}
	}
	return items, nil
}

func openAICodexReasoningInputItem(msg Message) map[string]any {
	encryptedContent := strings.TrimSpace(msg.ReasoningEncryptedContent)
	summaryText := strings.TrimSpace(msg.ReasoningContent)
	if encryptedContent == "" && summaryText == "" {
		return nil
	}
	item := map[string]any{
		"type":    "reasoning",
		"summary": []map[string]any{},
	}
	if encryptedContent != "" {
		item["encrypted_content"] = encryptedContent
	} else {
		item["encrypted_content"] = nil
	}
	if summaryText != "" {
		item["summary"] = []map[string]any{{
			"type": "summary_text",
			"text": summaryText,
		}}
	}
	return item
}

func openAICodexAssistantImageGenerationInputItems(workingDir string, images []MessageImage, supportsImages bool) []any {
	if len(images) == 0 {
		return nil
	}
	items := make([]any, 0, len(images))
	for index, image := range images {
		item := map[string]any{
			"type":   "image_generation_call",
			"id":     openAICodexAssistantImageGenerationID(image, index),
			"status": firstNonEmptyTrimmed(image.Status, "completed"),
			"result": "",
		}
		if revisedPrompt := strings.TrimSpace(image.RevisedPrompt); revisedPrompt != "" {
			item["revised_prompt"] = revisedPrompt
		}
		if supportsImages {
			if encoded, err := encodeMessageImages(workingDir, []MessageImage{image}); err == nil && len(encoded) > 0 {
				item["result"] = encoded[0].Data
			}
		}
		items = append(items, item)
	}
	return items
}

func openAICodexAssistantImageGenerationID(image MessageImage, index int) string {
	if id := strings.TrimSpace(image.ID); id != "" {
		return id
	}
	base := strings.TrimSpace(filepath.Base(strings.TrimSpace(image.Path)))
	if base != "" {
		if ext := filepath.Ext(base); ext != "" {
			base = strings.TrimSuffix(base, ext)
		}
	}
	id := sanitizeOpenAICodexImageGenerationPathSegment(base)
	if id != "generated_image" {
		return id
	}
	return fmt.Sprintf("generated_image_%d", index+1)
}

func openAICodexAssistantWebSearchInputItems(calls []MessageWebSearchCall) []any {
	if len(calls) == 0 {
		return nil
	}
	items := make([]any, 0, len(calls))
	for _, call := range calls {
		item := map[string]any{
			"type": "web_search_call",
		}
		if status := strings.TrimSpace(call.Status); status != "" {
			item["status"] = status
		}
		if len(call.Action) > 0 {
			item["action"] = cloneToolDefinitionMap(call.Action)
		}
		items = append(items, item)
	}
	return items
}

func openAICodexAssistantLocalShellInputItems(calls []MessageLocalShellCall) []any {
	if len(calls) == 0 {
		return nil
	}
	items := make([]any, 0, len(calls))
	for _, call := range calls {
		callID := strings.TrimSpace(call.CallID)
		if len(call.Action) == 0 {
			continue
		}
		status := firstNonEmptyTrimmed(call.Status, "completed")
		items = append(items, map[string]any{
			"type":    "local_shell_call",
			"call_id": openAICodexNullableCallID(callID),
			"status":  status,
			"action":  cloneToolDefinitionMap(call.Action),
		})
	}
	return items
}

func openAICodexAssistantCompactionInputItems(compactions []MessageCodexCompactionItem) []any {
	if len(compactions) == 0 {
		return nil
	}
	items := make([]any, 0, len(compactions))
	for _, compaction := range compactions {
		itemType := strings.TrimSpace(compaction.Type)
		encryptedContent := strings.TrimSpace(compaction.EncryptedContent)
		switch itemType {
		case "compaction", "compaction_summary":
			if encryptedContent == "" {
				continue
			}
			items = append(items, map[string]any{
				"type":              itemType,
				"encrypted_content": encryptedContent,
			})
		case "context_compaction":
			item := map[string]any{
				"type": itemType,
			}
			if encryptedContent != "" {
				item["encrypted_content"] = encryptedContent
			}
			items = append(items, item)
		case "compaction_trigger":
			items = append(items, map[string]any{
				"type": itemType,
			})
		}
	}
	return items
}

func openAICodexAssistantToolOutputInputItems(outputs []MessageCodexToolOutputItem, supportsImages bool) []any {
	if len(outputs) == 0 {
		return nil
	}
	items := make([]any, 0, len(outputs))
	for _, output := range outputs {
		itemType := strings.TrimSpace(output.Type)
		callID := strings.TrimSpace(output.CallID)
		if itemType == "" {
			continue
		}
		switch itemType {
		case "function_call_output":
			if callID == "" {
				continue
			}
			items = append(items, map[string]any{
				"type":    itemType,
				"call_id": callID,
				"output":  openAICodexCodexToolOutputPayload(output, supportsImages),
			})
		case "custom_tool_call_output":
			if callID == "" {
				continue
			}
			item := map[string]any{
				"type":    itemType,
				"call_id": callID,
				"output":  openAICodexCodexToolOutputPayload(output, supportsImages),
			}
			if name := strings.TrimSpace(output.Name); name != "" {
				item["name"] = name
			}
			items = append(items, item)
		case "tool_search_output":
			execution := firstNonEmptyTrimmed(output.Execution, "client")
			status := firstNonEmptyTrimmed(output.Status, "completed")
			items = append(items, map[string]any{
				"type":      itemType,
				"call_id":   openAICodexNullableCallID(callID),
				"status":    status,
				"execution": execution,
				"tools":     cloneOpenAICodexToolMaps(output.Tools),
			})
		}
	}
	return items
}

func openAICodexNullableCallID(callID string) any {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil
	}
	return callID
}

func openAICodexCodexToolOutputPayload(output MessageCodexToolOutputItem, supportsImages bool) any {
	msg := Message{
		Text:             output.Text,
		ToolContentItems: append([]ToolContentItem(nil), output.ToolContentItems...),
	}
	return openAICodexToolOutputForResponses(msg, supportsImages)
}

func cloneOpenAICodexToolMaps(tools []map[string]any) []map[string]any {
	if len(tools) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, cloneToolDefinitionMap(tool))
	}
	return out
}

type openAICodexExpectedOutputSets struct {
	functionOrLocal map[string]bool
	custom          map[string]bool
	toolSearch      map[string]bool
}

func newOpenAICodexExpectedOutputSets() openAICodexExpectedOutputSets {
	return openAICodexExpectedOutputSets{
		functionOrLocal: map[string]bool{},
		custom:          map[string]bool{},
		toolSearch:      map[string]bool{},
	}
}

func (sets openAICodexExpectedOutputSets) hasAny() bool {
	return len(sets.functionOrLocal) > 0 || len(sets.custom) > 0 || len(sets.toolSearch) > 0
}

func (sets openAICodexExpectedOutputSets) markToolCall(callID string, call ToolCall) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	switch strings.TrimSpace(call.Name) {
	case "apply_patch":
		sets.custom[callID] = true
	case "tool_search":
		sets.toolSearch[callID] = true
	default:
		sets.functionOrLocal[callID] = true
	}
}

func (sets openAICodexExpectedOutputSets) markLocalShell(callID string) {
	callID = strings.TrimSpace(callID)
	if callID != "" {
		sets.functionOrLocal[callID] = true
	}
}

func (sets openAICodexExpectedOutputSets) matchesOutput(output MessageCodexToolOutputItem) bool {
	callID := strings.TrimSpace(output.CallID)
	switch strings.TrimSpace(output.Type) {
	case "function_call_output":
		return callID != "" && sets.functionOrLocal[callID]
	case "custom_tool_call_output":
		return callID != "" && sets.custom[callID]
	case "tool_search_output":
		if strings.TrimSpace(output.Execution) == "server" {
			return true
		}
		if callID == "" {
			return true
		}
		return callID != "" && sets.toolSearch[callID]
	default:
		return false
	}
}

func filterOpenAICodexAssistantToolOutputItems(outputs []MessageCodexToolOutputItem, expectedSets openAICodexExpectedOutputSets) []MessageCodexToolOutputItem {
	if len(outputs) == 0 {
		return nil
	}
	out := make([]MessageCodexToolOutputItem, 0, len(outputs))
	for _, output := range outputs {
		if expectedSets.matchesOutput(output) {
			out = append(out, output)
		}
	}
	return out
}

func ensureOpenAICodexToolCallResponses(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}
	expected := map[string]ToolCall{}
	expectedSets := newOpenAICodexExpectedOutputSets()
	outputs := map[string]bool{}
	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			for _, call := range msg.ToolCalls {
				callID := openAICodexToolCallHistoryCallID(call)
				if callID != "" {
					expected[callID] = call
					expectedSets.markToolCall(callID, call)
				}
			}
			for _, call := range msg.LocalShellCalls {
				callID := strings.TrimSpace(call.CallID)
				if callID != "" {
					expected[callID] = ToolCall{ID: callID}
					expectedSets.markLocalShell(callID)
				}
			}
		}
	}
	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			for _, output := range msg.CodexToolOutputItems {
				callID := strings.TrimSpace(output.CallID)
				if callID != "" && expectedSets.matchesOutput(output) {
					outputs[callID] = true
				}
			}
		case "tool":
			callID := firstNonEmptyTrimmed(msg.ToolCallID, msg.ToolName)
			if callID != "" {
				outputs[callID] = true
			}
		}
	}
	if len(expected) == 0 && !expectedSets.hasAny() {
		out := make([]Message, 0, len(messages))
		for _, msg := range messages {
			if msg.Role != "tool" {
				if msg.Role == "assistant" {
					msg.CodexToolOutputItems = filterOpenAICodexAssistantToolOutputItems(msg.CodexToolOutputItems, expectedSets)
				}
				out = append(out, msg)
			}
		}
		return out
	}

	out := make([]Message, 0, len(messages))
	for index, msg := range messages {
		if msg.Role == "tool" {
			callID := firstNonEmptyTrimmed(msg.ToolCallID, msg.ToolName)
			call, ok := expected[callID]
			if !ok {
				continue
			}
			if strings.TrimSpace(msg.ToolName) == "" {
				msg.ToolName = call.Name
			}
			out = append(out, msg)
			continue
		}

		if msg.Role == "assistant" {
			msg.CodexToolOutputItems = filterOpenAICodexAssistantToolOutputItems(msg.CodexToolOutputItems, expectedSets)
		}
		out = append(out, msg)
		if msg.Role != "assistant" || (len(msg.ToolCalls) == 0 && len(msg.LocalShellCalls) == 0) {
			continue
		}
		for _, call := range msg.ToolCalls {
			callID := openAICodexToolCallHistoryCallID(call)
			if callID == "" || outputs[callID] {
				continue
			}
			text := missingOpenAIToolResultText(messages, index+1)
			isError := strings.HasPrefix(text, "ERROR:")
			out = append(out, Message{
				Role:       "tool",
				ToolCallID: callID,
				ToolName:   call.Name,
				Text:       text,
				IsError:    isError,
			})
		}
		for _, call := range msg.LocalShellCalls {
			callID := strings.TrimSpace(call.CallID)
			if callID == "" || outputs[callID] {
				continue
			}
			text := missingOpenAIToolResultText(messages, index+1)
			isError := strings.HasPrefix(text, "ERROR:")
			out = append(out, Message{
				Role:       "tool",
				ToolCallID: callID,
				Text:       text,
				IsError:    isError,
			})
		}
	}
	return out
}

func openAICodexToolCallInputItem(callID string, call ToolCall) map[string]any {
	name := strings.TrimSpace(call.Name)
	if name == "apply_patch" {
		item := map[string]any{
			"type":    "custom_tool_call",
			"call_id": callID,
			"name":    name,
			"input":   openAICodexApplyPatchInputFromArguments(call.Arguments),
		}
		if status := strings.TrimSpace(call.Status); status != "" {
			item["status"] = status
		}
		return item
	}
	if name == "tool_search" {
		item := map[string]any{
			"type":      "tool_search_call",
			"call_id":   openAICodexNullableCallID(callID),
			"execution": "client",
			"arguments": openAICodexToolSearchArguments(call.Arguments),
		}
		if status := strings.TrimSpace(call.Status); status != "" {
			item["status"] = status
		}
		return item
	}
	item := map[string]any{
		"type":      "function_call",
		"call_id":   callID,
		"name":      openAICodexFunctionCallWireName(call),
		"arguments": normalizeOpenAIToolCallArguments(call.Arguments),
	}
	if namespace := strings.TrimSpace(call.Namespace); namespace != "" {
		item["namespace"] = namespace
	}
	return item
}

func openAICodexToolCallHistoryCallID(call ToolCall) string {
	if strings.TrimSpace(call.Name) == "tool_search" {
		return strings.TrimSpace(call.ID)
	}
	return firstNonEmptyTrimmed(call.ID, call.Name)
}

func openAICodexFunctionCallWireName(call ToolCall) string {
	name := strings.TrimSpace(call.Name)
	namespace := strings.TrimSpace(call.Namespace)
	if namespace == "" || name == "" {
		return name
	}
	if strings.HasPrefix(name, namespace) {
		stripped := strings.TrimSpace(strings.TrimPrefix(name, namespace))
		if stripped != "" {
			return stripped
		}
	}
	return name
}

func openAICodexToolCallOutputItem(callID string, msg Message, supportsImages bool) map[string]any {
	if strings.TrimSpace(msg.ToolName) == "tool_search" {
		return map[string]any{
			"type":      "tool_search_output",
			"call_id":   callID,
			"status":    "completed",
			"execution": "client",
			"tools":     openAICodexToolSearchOutputTools(msg),
		}
	}
	if strings.TrimSpace(msg.ToolName) == "apply_patch" {
		return map[string]any{
			"type":    "custom_tool_call_output",
			"call_id": callID,
			"output":  openAICodexToolOutputForResponses(msg, supportsImages),
		}
	}
	return map[string]any{
		"type":    "function_call_output",
		"call_id": callID,
		"output":  openAICodexToolOutputForResponses(msg, supportsImages),
	}
}

func openAICodexToolSearchOutputTools(msg Message) []any {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return []any{}
	}
	var array []any
	if err := json.Unmarshal([]byte(text), &array); err == nil {
		return openAICodexSanitizeToolSearchTools(array)
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(text), &object); err != nil {
		return []any{}
	}
	if rawTools, ok := object["tools"].([]any); ok {
		return openAICodexSanitizeToolSearchTools(rawTools)
	}
	if rawTools, ok := object["data"].([]any); ok {
		return openAICodexSanitizeToolSearchTools(rawTools)
	}
	return []any{}
}

func openAICodexSanitizeToolSearchTools(tools []any) []any {
	if len(tools) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		if sanitized, ok := openAICodexSanitizeToolSearchToolSpec(tool); ok {
			out = append(out, sanitized)
		}
	}
	return coalesceOpenAICodexToolSearchNamespaceTools(out)
}

func openAICodexSanitizeToolSearchToolSpec(value any) (any, bool) {
	tool, ok := openAICodexStripOutputSchemaFields(value).(map[string]any)
	if !ok {
		return nil, false
	}
	switch strings.TrimSpace(stringsValueFromAny(tool["type"])) {
	case "function":
		if strings.TrimSpace(stringsValueFromAny(tool["name"])) == "" {
			return nil, false
		}
		tool["defer_loading"] = true
		return tool, true
	case "namespace":
		namespace := strings.TrimSpace(stringsValueFromAny(tool["name"]))
		if namespace == "" {
			return nil, false
		}
		if strings.TrimSpace(stringsValueFromAny(tool["description"])) == "" {
			tool["description"] = openAICodexNamespaceDescription(namespace, "")
		}
		children, ok := tool["tools"].([]any)
		if !ok {
			return nil, false
		}
		filteredChildren := make([]any, 0, len(children))
		for _, child := range children {
			childTool, ok := child.(map[string]any)
			if !ok {
				continue
			}
			if strings.TrimSpace(stringsValueFromAny(childTool["type"])) != "function" {
				continue
			}
			if strings.TrimSpace(stringsValueFromAny(childTool["name"])) == "" {
				continue
			}
			childTool["defer_loading"] = true
			filteredChildren = append(filteredChildren, childTool)
		}
		if len(filteredChildren) == 0 {
			return nil, false
		}
		tool["tools"] = filteredChildren
		return tool, true
	default:
		return nil, false
	}
}

func openAICodexStripOutputSchemaFields(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if key == "output_schema" {
				continue
			}
			out[key] = openAICodexStripOutputSchemaFields(child)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, child := range typed {
			out = append(out, openAICodexStripOutputSchemaFields(child))
		}
		return out
	default:
		return value
	}
}

func coalesceOpenAICodexToolSearchNamespaceTools(tools []any) []any {
	if len(tools) == 0 {
		return []any{}
	}
	mapped := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		typed, ok := tool.(map[string]any)
		if !ok {
			return tools
		}
		mapped = append(mapped, typed)
	}
	merged := mergeOpenAICodexNamespaceTools(mapped)
	out := make([]any, 0, len(merged))
	for _, tool := range merged {
		out = append(out, tool)
	}
	return out
}

func openAICodexToolSearchArguments(arguments string) any {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
		if decoded != nil {
			return decoded
		}
	}
	return map[string]any{
		"query": trimmed,
	}
}

func openAICodexApplyPatchInputFromArguments(arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err == nil {
		if patch := strings.TrimSpace(stringValue(payload, "patch")); patch != "" {
			return patch
		}
		if raw := strings.TrimSpace(stringValue(payload, "raw")); raw != "" {
			return raw
		}
	}
	return trimmed
}

func openAICodexInputMessagePhase(msg Message) string {
	switch strings.TrimSpace(msg.Phase) {
	case messagePhaseCommentary:
		return messagePhaseCommentary
	case messagePhaseFinalAnswer:
		return messagePhaseFinalAnswer
	}
	if len(msg.ToolCalls) > 0 && strings.TrimSpace(msg.Text) != "" {
		return messagePhaseCommentary
	}
	return ""
}

func openAICodexUserContent(workingDir string, msg Message, supportsImages bool) ([]map[string]any, error) {
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
	if !supportsImages {
		for range msg.Images {
			content = append(content, map[string]any{
				"type": "input_text",
				"text": openAICodexImageContentOmittedPlaceholder,
			})
		}
		return content, nil
	}
	encodedImages, err := encodeMessageImages(workingDir, msg.Images)
	if err != nil {
		return nil, err
	}
	return appendCodexResponsesImages(content, encodedImages), nil
}

func openAICodexToolOutputForResponses(msg Message, supportsImages bool) any {
	if supportsImages {
		return toolOutputForResponses(msg)
	}
	msg.ToolContentItems = openAICodexStripUnsupportedImageContentItems(msg.ToolContentItems)
	return toolOutputForResponses(msg)
}

func openAICodexStripUnsupportedImageContentItems(items []ToolContentItem) []ToolContentItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]ToolContentItem, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Type) == "input_image" {
			out = append(out, ToolContentItem{
				Type: "input_text",
				Text: openAICodexImageContentOmittedPlaceholder,
			})
			continue
		}
		out = append(out, item)
	}
	return out
}

type openAICodexOutputItem struct {
	ID               string           `json:"id,omitempty"`
	Type             string           `json:"type"`
	Status           string           `json:"status,omitempty"`
	RevisedPrompt    string           `json:"revised_prompt,omitempty"`
	Result           string           `json:"result,omitempty"`
	Role             string           `json:"role,omitempty"`
	Phase            string           `json:"phase,omitempty"`
	CallID           string           `json:"call_id,omitempty"`
	Name             string           `json:"name,omitempty"`
	Namespace        string           `json:"namespace,omitempty"`
	Input            string           `json:"input,omitempty"`
	Execution        string           `json:"execution,omitempty"`
	Arguments        json.RawMessage  `json:"arguments,omitempty"`
	Output           json.RawMessage  `json:"output,omitempty"`
	Action           json.RawMessage  `json:"action,omitempty"`
	EncryptedContent string           `json:"encrypted_content,omitempty"`
	Tools            []map[string]any `json:"tools,omitempty"`
	Content          []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content,omitempty"`
	Summary []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"summary,omitempty"`
}

type openAICodexResponseErrorPayload struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Param   string `json:"param,omitempty"`
	Code    any    `json:"code,omitempty"`
}

func parseOpenAICodexResponse(data []byte) (ChatResponse, error) {
	var decoded struct {
		ID                string `json:"id,omitempty"`
		Status            string `json:"status"`
		OutputText        string `json:"output_text,omitempty"`
		EndTurn           *bool  `json:"end_turn,omitempty"`
		IncompleteDetails *struct {
			Reason string `json:"reason,omitempty"`
		} `json:"incomplete_details,omitempty"`
		Error    *openAICodexResponseErrorPayload `json:"error,omitempty"`
		Metadata json.RawMessage                  `json:"metadata,omitempty"`
		Output   []openAICodexOutputItem          `json:"output,omitempty"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ChatResponse{}, err
	}
	if decoded.Error != nil {
		return ChatResponse{}, newOpenAICodexResponseError(data, decoded.Error)
	}

	out := Message{Role: "assistant"}
	fallbackTexts := []string{}
	finalTexts := []string{}
	reasoningTexts := []string{}
	reasoningEncryptedContents := []string{}
	webSearchCalls := []MessageWebSearchCall{}
	localShellCalls := []MessageLocalShellCall{}
	compactionItems := []MessageCodexCompactionItem{}
	codexToolOutputItems := []MessageCodexToolOutputItem{}
	textMessageCount := 0
	commentaryTextMessageCount := 0
	messagePhase := ""
	if strings.TrimSpace(decoded.OutputText) != "" {
		fallbackTexts = append(fallbackTexts, decoded.OutputText)
	}
	for _, item := range decoded.Output {
		switch item.Type {
		case "message":
			itemTexts := []string{}
			for _, content := range item.Content {
				if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
					itemTexts = append(itemTexts, content.Text)
				}
			}
			if len(itemTexts) > 0 {
				textMessageCount++
				itemPhase := normalizeOpenAICodexMessagePhase(item.Phase)
				if itemPhase == messagePhaseCommentary {
					commentaryTextMessageCount++
				}
				if itemPhase == messagePhaseFinalAnswer {
					finalTexts = append(finalTexts, itemTexts...)
					messagePhase = messagePhaseFinalAnswer
				}
				fallbackTexts = append(fallbackTexts, itemTexts...)
			}
		case "function_call", "custom_tool_call", "tool_search_call":
			if call, ok := openAICodexToolCallFromOutputItem(item); ok {
				out.ToolCalls = append(out.ToolCalls, call)
			}
		case "local_shell_call":
			if call, ok := openAICodexLocalShellCallFromOutputItem(item); ok {
				localShellCalls = append(localShellCalls, call)
			}
		case "compaction", "compaction_summary", "context_compaction", "compaction_trigger":
			if compaction, ok := openAICodexCompactionItemFromOutputItem(item); ok {
				compactionItems = append(compactionItems, compaction)
			}
		case "function_call_output", "custom_tool_call_output", "tool_search_output":
			if output, ok := openAICodexToolOutputFromOutputItem(item); ok {
				codexToolOutputItems = append(codexToolOutputItems, output)
			}
		case "reasoning":
			if reasoningText := openAICodexReasoningItemText(item); strings.TrimSpace(reasoningText) != "" {
				reasoningTexts = append(reasoningTexts, reasoningText)
			}
			if encryptedContent := openAICodexReasoningEncryptedContent(item); encryptedContent != "" {
				reasoningEncryptedContents = append(reasoningEncryptedContents, encryptedContent)
			}
		case "image_generation_call", "web_search_call":
			if itemText := openAICodexHostedOutputItemText(item, ""); strings.TrimSpace(itemText) != "" {
				fallbackTexts = append(fallbackTexts, itemText)
			}
			if call, ok := openAICodexWebSearchCallFromOutputItem(item); ok {
				webSearchCalls = append(webSearchCalls, call)
			}
		}
	}
	texts := fallbackTexts
	if len(finalTexts) > 0 {
		texts = finalTexts
	} else if textMessageCount > 0 && textMessageCount == commentaryTextMessageCount {
		messagePhase = messagePhaseCommentary
	} else if messagePhase != messagePhaseFinalAnswer {
		messagePhase = ""
	}
	out.Text = strings.TrimSpace(strings.Join(deduplicateOpenAICodexTexts(texts), "\n"))
	out.ReasoningContent = strings.TrimSpace(strings.Join(deduplicateOpenAICodexTexts(reasoningTexts), "\n"))
	out.ReasoningEncryptedContent = firstNonEmptyTrimmed(reasoningEncryptedContents...)
	out.WebSearchCalls = append(out.WebSearchCalls, webSearchCalls...)
	out.LocalShellCalls = append(out.LocalShellCalls, localShellCalls...)
	out.CodexCompactionItems = append(out.CodexCompactionItems, compactionItems...)
	out.CodexToolOutputItems = append(out.CodexToolOutputItems, codexToolOutputItems...)
	out.Phase = messagePhase
	stopReason := strings.TrimSpace(decoded.Status)
	if decoded.IncompleteDetails != nil && strings.TrimSpace(decoded.IncompleteDetails.Reason) != "" {
		stopReason = strings.TrimSpace(decoded.IncompleteDetails.Reason)
	}
	if !openAICodexMessageHasOutput(out) {
		return ChatResponse{}, newProviderMessageError("openai-codex", "empty Responses output", "", "", nil, data)
	}
	return ChatResponse{
		Message:            out,
		ResponseID:         strings.TrimSpace(decoded.ID),
		StopReason:         stopReason,
		EndTurn:            decoded.EndTurn,
		ServerModel:        openAICodexServerModelFromResponsePayload(data),
		ModelVerifications: openAICodexModelVerificationsFromMetadata(decoded.Metadata),
	}, nil
}

type openAICodexStreamToolCall struct {
	ID           string
	Name         string
	Namespace    string
	Status       string
	Type         string
	Arguments    strings.Builder
	ArgsStarted  bool
	ReadyEmitted bool
}

type openAICodexStreamOptions struct {
	OnProgressEvent func(ProgressEvent)
	SessionID       string
	ImageOutputRoot string
}

func openAICodexStreamToolCallIndex(toolCalls map[int]*openAICodexStreamToolCall, target *openAICodexStreamToolCall) (int, bool) {
	for index, item := range toolCalls {
		if item == target {
			return index, true
		}
	}
	return 0, false
}

func readOpenAICodexStream(ctx context.Context, body io.Reader, onProgressEvent ...func(ProgressEvent)) (ChatResponse, error) {
	opts := openAICodexStreamOptions{}
	if len(onProgressEvent) > 0 {
		opts.OnProgressEvent = onProgressEvent[0]
	}
	return readOpenAICodexStreamWithOptions(ctx, body, opts)
}

func readOpenAICodexStreamWithOptions(ctx context.Context, body io.Reader, opts openAICodexStreamOptions) (ChatResponse, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), openAICodexSSEMaxLineBytes)

	var text strings.Builder
	var reasoning strings.Builder
	reasoningEncryptedContent := ""
	deltaPrefixTexts := []string{}
	itemTexts := []string{}
	finalItemTexts := []string{}
	hostedItemTexts := []string{}
	hostedImages := []MessageImage{}
	hostedWebSearchCalls := []MessageWebSearchCall{}
	localShellCalls := []MessageLocalShellCall{}
	compactionItems := []MessageCodexCompactionItem{}
	codexToolOutputItems := []MessageCodexToolOutputItem{}
	var completedResponse json.RawMessage
	toolCalls := map[int]*openAICodexStreamToolCall{}
	toolOrder := []int{}
	stopReason := ""
	messagePhase := ""
	completedSeen := false
	var endTurn *bool
	responseID := ""
	serverModel := ""
	rateLimitSummary := ""
	modelVerifications := []string{}
	var progress func(ProgressEvent)
	progress = opts.OnProgressEvent

	ensureToolCall := func(index int) *openAICodexStreamToolCall {
		item := toolCalls[index]
		if item == nil {
			item = &openAICodexStreamToolCall{}
			toolCalls[index] = item
			toolOrder = append(toolOrder, index)
		}
		return item
	}
	toolCallIndexByRef := map[string]int{}
	nextSyntheticToolIndex := -1
	allocateSyntheticToolIndex := func() int {
		index := nextSyntheticToolIndex
		nextSyntheticToolIndex--
		return index
	}
	rememberToolCallRef := func(index int, ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		toolCallIndexByRef[ref] = index
	}
	rememberToolCallRefs := func(index int, item *openAICodexStreamToolCall, itemID string) {
		if item == nil {
			return
		}
		rememberToolCallRef(index, itemID)
		rememberToolCallRef(index, item.ID)
	}
	ensureToolCallForRefs := func(itemID string, callID string, fallbackIndex *int) *openAICodexStreamToolCall {
		itemID = strings.TrimSpace(itemID)
		callID = strings.TrimSpace(callID)
		for _, ref := range []string{itemID, callID} {
			if ref == "" {
				continue
			}
			if index, ok := toolCallIndexByRef[ref]; ok {
				return ensureToolCall(index)
			}
		}
		index := 0
		if fallbackIndex != nil {
			index = *fallbackIndex
		} else if itemID != "" || callID != "" {
			index = allocateSyntheticToolIndex()
		}
		item := ensureToolCall(index)
		if callID != "" && strings.TrimSpace(item.ID) == "" {
			item.ID = callID
		}
		rememberToolCallRefs(index, item, itemID)
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
	collectMessageItemText := func(item openAICodexOutputItem) {
		itemText := openAICodexOutputItemText(item)
		if strings.TrimSpace(itemText) == "" {
			return
		}
		itemTexts = append(itemTexts, itemText)
		if normalizeOpenAICodexMessagePhase(item.Phase) == messagePhaseFinalAnswer {
			finalItemTexts = append(finalItemTexts, itemText)
		}
	}
	collectMessageDeltaPrefixText := func(item openAICodexOutputItem) {
		itemText := openAICodexOutputItemText(item)
		if strings.TrimSpace(itemText) == "" {
			return
		}
		deltaPrefixTexts = append(deltaPrefixTexts, itemText)
	}
	collectHostedOutputItem := func(item openAICodexOutputItem) {
		savedPath := ""
		if strings.TrimSpace(item.Type) == "image_generation_call" {
			if path, err := saveOpenAICodexImageGenerationOutputItem(opts.ImageOutputRoot, opts.SessionID, item); err == nil && strings.TrimSpace(path) != "" {
				savedPath = path
				hostedImages = appendUniqueImages(hostedImages, MessageImage{
					Path:          path,
					MediaType:     "image/png",
					ID:            strings.TrimSpace(item.ID),
					Status:        strings.TrimSpace(item.Status),
					RevisedPrompt: strings.TrimSpace(item.RevisedPrompt),
				})
			}
		}
		if itemText := openAICodexHostedOutputItemText(item, savedPath); strings.TrimSpace(itemText) != "" {
			hostedItemTexts = append(hostedItemTexts, itemText)
		}
		if call, ok := openAICodexWebSearchCallFromOutputItem(item); ok {
			hostedWebSearchCalls = append(hostedWebSearchCalls, call)
		}
	}
	collectLocalShellCall := func(item openAICodexOutputItem) {
		call, ok := openAICodexLocalShellCallFromOutputItem(item)
		if !ok {
			return
		}
		localShellCalls = append(localShellCalls, call)
	}
	collectCompactionItem := func(item openAICodexOutputItem) {
		compaction, ok := openAICodexCompactionItemFromOutputItem(item)
		if !ok {
			return
		}
		compactionItems = append(compactionItems, compaction)
	}
	collectCodexToolOutputItem := func(item openAICodexOutputItem) {
		output, ok := openAICodexToolOutputFromOutputItem(item)
		if !ok {
			return
		}
		codexToolOutputItems = append(codexToolOutputItems, output)
	}

	processPayload := func(payload string) (ChatResponse, bool, error) {
		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			return ChatResponse{}, false, nil
		}
		var event struct {
			Type        string                `json:"type"`
			Delta       string                `json:"delta,omitempty"`
			Text        string                `json:"text,omitempty"`
			Arguments   string                `json:"arguments,omitempty"`
			Input       string                `json:"input,omitempty"`
			ItemID      string                `json:"item_id,omitempty"`
			CallID      string                `json:"call_id,omitempty"`
			OutputIndex *int                  `json:"output_index,omitempty"`
			EndTurn     *bool                 `json:"end_turn,omitempty"`
			Response    json.RawMessage       `json:"response,omitempty"`
			Headers     json.RawMessage       `json:"headers,omitempty"`
			Metadata    json.RawMessage       `json:"metadata,omitempty"`
			Item        openAICodexOutputItem `json:"item,omitempty"`
			Error       *struct {
				Message string `json:"message"`
				Type    string `json:"type,omitempty"`
				Param   string `json:"param,omitempty"`
				Code    any    `json:"code,omitempty"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return ChatResponse{}, false, nil
		}
		if event.Error != nil {
			return ChatResponse{}, false, newProviderMessageError("openai-codex", event.Error.Message, event.Error.Type, event.Error.Param, event.Error.Code, []byte(payload))
		}
		if model := openAICodexServerModelFromEventPayload(event.Response, event.Headers); model != "" {
			serverModel = model
		}
		if event.Type == "codex.rate_limits" {
			if summary := openAICodexRateLimitSummaryFromEventPayload([]byte(payload)); summary != "" {
				rateLimitSummary = summary
			}
			return ChatResponse{}, false, nil
		}
		if event.Type == "response.metadata" {
			modelVerifications = mergeOpenAICodexModelVerifications(modelVerifications, openAICodexModelVerificationsFromMetadata(event.Metadata))
			return ChatResponse{}, false, nil
		}
		switch event.Type {
		case "response.output_text.delta":
			text.WriteString(event.Delta)
		case "response.output_text.done":
			if text.Len() == 0 && strings.TrimSpace(event.Text) != "" {
				text.WriteString(event.Text)
			}
		case "response.output_item.added":
			if event.Item.Type == "message" {
				messagePhase = mergeOpenAICodexMessagePhase(messagePhase, event.Item.Phase)
				collectMessageItemText(event.Item)
				collectMessageDeltaPrefixText(event.Item)
			} else if event.Item.Type == "reasoning" {
				if reasoningText := openAICodexReasoningItemText(event.Item); strings.TrimSpace(reasoningText) != "" {
					reasoning.WriteString(reasoningText)
				}
				if reasoningEncryptedContent == "" {
					reasoningEncryptedContent = openAICodexReasoningEncryptedContent(event.Item)
				}
			} else if openAICodexOutputItemIsToolCall(event.Item) {
				itemID := firstNonEmptyTrimmed(event.ItemID, event.Item.ID)
				item := ensureToolCallForRefs(itemID, event.Item.CallID, event.OutputIndex)
				if openAICodexPopulateStreamToolCall(item, event.Item, false) {
					if index, ok := openAICodexStreamToolCallIndex(toolCalls, item); ok {
						rememberToolCallRefs(index, item, itemID)
					}
					emitProgressEvent(progress, ProgressEvent{
						Kind:       progressKindModelStreamToolCall,
						Provider:   "openai-codex",
						ToolName:   item.Name,
						ToolCallID: item.ID,
					})
				}
			}
		case "response.function_call_arguments.delta":
			item := ensureToolCallForRefs(event.ItemID, event.CallID, event.OutputIndex)
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
			item := ensureToolCallForRefs(event.ItemID, event.CallID, event.OutputIndex)
			arguments := firstNonEmptyTrimmed(event.Arguments, event.Text)
			if strings.TrimSpace(arguments) != "" {
				item.Arguments.Reset()
				item.Arguments.WriteString(arguments)
			}
			emitToolReady(item)
		case "response.custom_tool_call_input.delta":
			item := ensureToolCallForRefs(event.ItemID, event.CallID, event.OutputIndex)
			item.Type = firstNonEmptyTrimmed(item.Type, "custom_tool_call")
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
		case "response.custom_tool_call_input.done":
			item := ensureToolCallForRefs(event.ItemID, event.CallID, event.OutputIndex)
			item.Type = firstNonEmptyTrimmed(item.Type, "custom_tool_call")
			input := firstNonEmptyTrimmed(event.Input, event.Text, event.Arguments)
			if input != "" {
				item.Arguments.Reset()
				item.Arguments.WriteString(input)
			}
			emitToolReady(item)
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			reasoning.WriteString(event.Delta)
		case "response.reasoning_summary_part.added":
			if reasoning.Len() > 0 && !strings.HasSuffix(reasoning.String(), "\n") {
				reasoning.WriteString("\n")
			}
		case "response.output_item.done":
			if event.Item.Type == "message" {
				messagePhase = mergeOpenAICodexMessagePhase(messagePhase, event.Item.Phase)
				collectMessageItemText(event.Item)
			} else if event.Item.Type == "reasoning" {
				if reasoning.Len() == 0 {
					if reasoningText := openAICodexReasoningItemText(event.Item); strings.TrimSpace(reasoningText) != "" {
						reasoning.WriteString(reasoningText)
					}
				}
				if reasoningEncryptedContent == "" {
					reasoningEncryptedContent = openAICodexReasoningEncryptedContent(event.Item)
				}
			} else if event.Item.Type == "image_generation_call" || event.Item.Type == "web_search_call" {
				collectHostedOutputItem(event.Item)
			} else if event.Item.Type == "local_shell_call" {
				collectLocalShellCall(event.Item)
			} else if event.Item.Type == "compaction" || event.Item.Type == "compaction_summary" || event.Item.Type == "context_compaction" || event.Item.Type == "compaction_trigger" {
				collectCompactionItem(event.Item)
			} else if event.Item.Type == "function_call_output" || event.Item.Type == "custom_tool_call_output" || event.Item.Type == "tool_search_output" {
				collectCodexToolOutputItem(event.Item)
			} else if openAICodexOutputItemIsToolCall(event.Item) {
				itemID := firstNonEmptyTrimmed(event.ItemID, event.Item.ID)
				item := ensureToolCallForRefs(itemID, event.Item.CallID, event.OutputIndex)
				if openAICodexPopulateStreamToolCall(item, event.Item, true) {
					if index, ok := openAICodexStreamToolCallIndex(toolCalls, item); ok {
						rememberToolCallRefs(index, item, itemID)
					}
					emitToolReady(item)
				}
			}
		case "response.completed":
			if len(event.Response) == 0 {
				return ChatResponse{}, false, nil
			}
			completedID := openAICodexResponseIDFromResponsePayload(event.Response)
			if completedID == "" {
				return ChatResponse{}, false, fmt.Errorf("failed to parse ResponseCompleted: missing response id")
			}
			completedSeen = true
			completedResponse = append(completedResponse[:0], event.Response...)
			responseID = completedID
			if nestedEndTurn := openAICodexEndTurnFromResponsePayload(event.Response); nestedEndTurn != nil {
				endTurn = nestedEndTurn
			} else if event.EndTurn != nil {
				endTurn = event.EndTurn
			}
			stopReason = "completed"
		case "response.failed":
			if len(event.Response) > 0 {
				resp, err := parseOpenAICodexResponse(event.Response)
				if err != nil {
					return ChatResponse{}, false, err
				}
				return resp, true, nil
			}
			return ChatResponse{}, false, newProviderMessageError("openai-codex", "response.failed event received", "", "", nil, []byte(payload))
		case "response.incomplete":
			reason := openAICodexIncompleteReason(event.Response)
			message := "Incomplete response returned"
			if reason != "" {
				message += ", reason: " + reason
			}
			return ChatResponse{}, false, newProviderMessageError("openai-codex", message, "incomplete", "", reason, []byte(payload))
		}
		return ChatResponse{}, false, nil
	}

	pendingData := []string{}
	flushPendingData := func() (ChatResponse, bool, error) {
		if len(pendingData) == 0 {
			return ChatResponse{}, false, nil
		}
		payload := strings.Join(pendingData, "\n")
		pendingData = pendingData[:0]
		return processPayload(payload)
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			resp, done, err := flushPendingData()
			if err != nil {
				return ChatResponse{}, err
			}
			if done {
				return resp, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		pendingData = append(pendingData, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
	}
	if err := scanner.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ChatResponse{}, ctxErr
		}
		return ChatResponse{}, err
	}
	resp, done, err := flushPendingData()
	if err != nil {
		return ChatResponse{}, err
	}
	if done {
		return resp, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ChatResponse{}, ctxErr
	}
	if !completedSeen {
		return ChatResponse{}, newProviderMessageError("openai-codex", "stream closed before response.completed", "", "", nil, nil)
	}
	var completedParsed *ChatResponse
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
			if resp.ServerModel == "" {
				resp.ServerModel = serverModel
			}
			if resp.ResponseID == "" {
				resp.ResponseID = responseID
			}
			if resp.RateLimitSummary == "" {
				resp.RateLimitSummary = rateLimitSummary
			}
			if len(resp.ModelVerifications) == 0 {
				resp.ModelVerifications = append([]string(nil), modelVerifications...)
			}
			if len(resp.Message.Images) == 0 && len(hostedImages) > 0 {
				resp.Message.Images = appendUniqueImages(resp.Message.Images, hostedImages...)
			}
			if len(hostedItemTexts) > 0 && (strings.TrimSpace(resp.Message.Text) == "" || !openAICodexResponsePayloadHasMessageText(completedResponse)) {
				resp.Message.Text = strings.TrimSpace(strings.Join(deduplicateOpenAICodexTexts(hostedItemTexts), "\n"))
			}
			completedParsed = &resp
			if !openAICodexStreamHasOutput(text.String(), itemTexts, finalItemTexts, hostedItemTexts, hostedImages, hostedWebSearchCalls, localShellCalls, compactionItems, codexToolOutputItems, reasoning.String(), reasoningEncryptedContent, toolCalls, toolOrder) {
				return resp, nil
			}
		}
	}

	streamText := text.String()
	if len(finalItemTexts) > 0 {
		streamText = strings.Join(deduplicateOpenAICodexTexts(finalItemTexts), "\n")
	} else if strings.TrimSpace(streamText) != "" {
		prefix := openAICodexDeltaPrefixText(deltaPrefixTexts)
		if strings.TrimSpace(prefix) != "" && !strings.HasPrefix(streamText, prefix) {
			streamText = prefix + streamText
		}
	} else {
		texts := itemTexts
		if len(texts) == 0 && len(hostedItemTexts) > 0 {
			texts = hostedItemTexts
		}
		streamText = strings.Join(deduplicateOpenAICodexTexts(texts), "\n")
	}
	out := Message{Role: "assistant", Phase: messagePhase, Text: strings.TrimSpace(streamText), ReasoningContent: strings.TrimSpace(reasoning.String()), ReasoningEncryptedContent: strings.TrimSpace(reasoningEncryptedContent), Images: appendUniqueImages(nil, hostedImages...), WebSearchCalls: append([]MessageWebSearchCall(nil), hostedWebSearchCalls...), LocalShellCalls: append([]MessageLocalShellCall(nil), localShellCalls...), CodexCompactionItems: append([]MessageCodexCompactionItem(nil), compactionItems...), CodexToolOutputItems: append([]MessageCodexToolOutputItem(nil), codexToolOutputItems...)}
	for _, index := range toolOrder {
		item := toolCalls[index]
		if item == nil || strings.TrimSpace(item.Name) == "" {
			continue
		}
		if strings.TrimSpace(item.ID) == "" && strings.TrimSpace(item.Name) != "tool_search" {
			continue
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        strings.TrimSpace(item.ID),
			Name:      strings.TrimSpace(item.Name),
			Namespace: strings.TrimSpace(item.Namespace),
			Status:    strings.TrimSpace(item.Status),
			Arguments: normalizeOpenAICodexStreamToolCallArguments(item),
		})
	}
	if completedParsed != nil {
		if responseID == "" {
			responseID = completedParsed.ResponseID
		}
		if out.Text == "" {
			out.Text = completedParsed.Message.Text
			out.Phase = completedParsed.Message.Phase
		}
		if out.ReasoningContent == "" {
			out.ReasoningContent = completedParsed.Message.ReasoningContent
		}
		if out.ReasoningEncryptedContent == "" {
			out.ReasoningEncryptedContent = completedParsed.Message.ReasoningEncryptedContent
		}
		if len(out.ToolCalls) == 0 && len(completedParsed.Message.ToolCalls) > 0 {
			out.ToolCalls = append(out.ToolCalls, completedParsed.Message.ToolCalls...)
		}
		if len(out.Images) == 0 && len(completedParsed.Message.Images) > 0 {
			out.Images = appendUniqueImages(out.Images, completedParsed.Message.Images...)
		}
		if len(out.WebSearchCalls) == 0 && len(completedParsed.Message.WebSearchCalls) > 0 {
			out.WebSearchCalls = append(out.WebSearchCalls, completedParsed.Message.WebSearchCalls...)
		}
		if len(out.LocalShellCalls) == 0 && len(completedParsed.Message.LocalShellCalls) > 0 {
			out.LocalShellCalls = append(out.LocalShellCalls, completedParsed.Message.LocalShellCalls...)
		}
		if len(out.CodexCompactionItems) == 0 && len(completedParsed.Message.CodexCompactionItems) > 0 {
			out.CodexCompactionItems = append(out.CodexCompactionItems, completedParsed.Message.CodexCompactionItems...)
		}
		if len(out.CodexToolOutputItems) == 0 && len(completedParsed.Message.CodexToolOutputItems) > 0 {
			out.CodexToolOutputItems = append(out.CodexToolOutputItems, completedParsed.Message.CodexToolOutputItems...)
		}
	}
	if !openAICodexMessageHasOutput(out) {
		return ChatResponse{}, newProviderMessageError("openai-codex", "empty Responses stream", "", "", nil, nil)
	}
	return ChatResponse{
		Message:            out,
		ResponseID:         responseID,
		StopReason:         stopReason,
		EndTurn:            endTurn,
		ServerModel:        serverModel,
		RateLimitSummary:   rateLimitSummary,
		ModelVerifications: append([]string(nil), modelVerifications...),
	}, nil
}

func newOpenAICodexResponseError(data []byte, responseError *openAICodexResponseErrorPayload) error {
	if responseError == nil {
		return newProviderMessageError("openai-codex", "response.failed event received", "", "", nil, data)
	}
	code := normalizeProviderErrorCode(responseError.Code)
	message := strings.TrimSpace(responseError.Message)
	switch code {
	case "context_length_exceeded":
		message = "context window exceeded"
	case "insufficient_quota":
		message = "quota exceeded"
	case "usage_not_included":
		message = "usage not included"
	case "cyber_policy":
		if message == "" {
			message = "This request has been flagged for possible cybersecurity risk."
		}
	case "invalid_prompt":
		if message == "" {
			message = "Invalid request."
		}
	case "server_is_overloaded", "slow_down":
		message = "server overloaded"
	default:
		if message == "" {
			message = "response.failed event received"
		}
	}
	return newProviderMessageError("openai-codex", message, responseError.Type, responseError.Param, code, data)
}

func openAICodexMessageHasOutput(msg Message) bool {
	return strings.TrimSpace(msg.Text) != "" ||
		strings.TrimSpace(msg.ReasoningContent) != "" ||
		strings.TrimSpace(msg.ReasoningEncryptedContent) != "" ||
		len(msg.Images) > 0 ||
		len(msg.WebSearchCalls) > 0 ||
		len(msg.LocalShellCalls) > 0 ||
		len(msg.CodexCompactionItems) > 0 ||
		len(msg.CodexToolOutputItems) > 0 ||
		len(msg.ToolCalls) > 0
}

func openAICodexStreamHasOutput(streamText string, itemTexts []string, finalItemTexts []string, hostedItemTexts []string, hostedImages []MessageImage, hostedWebSearchCalls []MessageWebSearchCall, localShellCalls []MessageLocalShellCall, compactionItems []MessageCodexCompactionItem, codexToolOutputItems []MessageCodexToolOutputItem, reasoningText string, reasoningEncryptedContent string, toolCalls map[int]*openAICodexStreamToolCall, toolOrder []int) bool {
	if strings.TrimSpace(streamText) != "" || strings.TrimSpace(reasoningText) != "" || strings.TrimSpace(reasoningEncryptedContent) != "" {
		return true
	}
	if len(itemTexts) > 0 || len(finalItemTexts) > 0 || len(hostedItemTexts) > 0 || len(hostedImages) > 0 {
		return true
	}
	if len(hostedWebSearchCalls) > 0 {
		return true
	}
	if len(localShellCalls) > 0 {
		return true
	}
	if len(compactionItems) > 0 {
		return true
	}
	if len(codexToolOutputItems) > 0 {
		return true
	}
	for _, index := range toolOrder {
		item := toolCalls[index]
		if item == nil || strings.TrimSpace(item.Name) == "" {
			continue
		}
		if strings.TrimSpace(item.ID) != "" || strings.TrimSpace(item.Name) == "tool_search" {
			return true
		}
	}
	return false
}

func openAICodexResponseIDFromResponsePayload(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var decoded struct {
		ID string `json:"id,omitempty"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ""
	}
	return strings.TrimSpace(decoded.ID)
}

func openAICodexServerModelFromResponsePayload(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var decoded struct {
		Headers map[string]any `json:"headers,omitempty"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ""
	}
	return openAICodexServerModelFromHeaderMap(decoded.Headers)
}

func openAICodexServerModelFromEventPayload(response json.RawMessage, headers json.RawMessage) string {
	if model := openAICodexServerModelFromResponsePayload(response); model != "" {
		return model
	}
	return openAICodexServerModelFromHeadersPayload(headers)
}

func openAICodexServerModelFromHeadersPayload(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ""
	}
	return openAICodexServerModelFromHeaderMap(decoded)
}

func openAICodexServerModelFromHeaderMap(headers map[string]any) string {
	for _, key := range []string{"OpenAI-Model", "openai-model", "x-openai-model", "X-OpenAI-Model"} {
		if value := openAICodexHeaderString(headers[key]); value != "" {
			return value
		}
	}
	return ""
}

func openAICodexHeaderString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		for _, item := range typed {
			if text := openAICodexHeaderString(item); text != "" {
				return text
			}
		}
	}
	return ""
}

func openAICodexResponsePayloadHasMessageText(data json.RawMessage) bool {
	if len(data) == 0 {
		return false
	}
	var decoded struct {
		OutputText string                  `json:"output_text,omitempty"`
		Output     []openAICodexOutputItem `json:"output,omitempty"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return false
	}
	if strings.TrimSpace(decoded.OutputText) != "" {
		return true
	}
	for _, item := range decoded.Output {
		if strings.TrimSpace(item.Type) != "message" {
			continue
		}
		if strings.TrimSpace(openAICodexOutputItemText(item)) != "" {
			return true
		}
	}
	return false
}

func openAICodexDeltaPrefixText(items []string) string {
	if len(items) == 0 {
		return ""
	}
	var out strings.Builder
	seen := map[string]bool{}
	for _, item := range items {
		if strings.TrimSpace(item) == "" || seen[item] {
			continue
		}
		out.WriteString(item)
		seen[item] = true
	}
	return out.String()
}

func openAICodexEndTurnFromResponsePayload(data json.RawMessage) *bool {
	if len(data) == 0 {
		return nil
	}
	var decoded struct {
		EndTurn *bool `json:"end_turn,omitempty"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil
	}
	if decoded.EndTurn == nil {
		return nil
	}
	endTurn := *decoded.EndTurn
	return &endTurn
}

func openAICodexModelVerificationsFromMetadata(data json.RawMessage) []string {
	if len(data) == 0 {
		return nil
	}
	var decoded struct {
		Recommendation any `json:"openai_verification_recommendation,omitempty"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil
	}
	return openAICodexNormalizeModelVerifications(decoded.Recommendation)
}

func openAICodexNormalizeModelVerifications(value any) []string {
	switch typed := value.(type) {
	case string:
		return openAICodexAppendModelVerification(nil, typed)
	case []any:
		out := []string{}
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				continue
			}
			out = openAICodexAppendModelVerification(out, text)
		}
		return out
	default:
		return nil
	}
}

func openAICodexAppendModelVerification(existing []string, value string) []string {
	normalized := strings.TrimSpace(value)
	switch normalized {
	case openAICodexTrustedAccessForCyber:
	default:
		return existing
	}
	for _, item := range existing {
		if item == normalized {
			return existing
		}
	}
	return append(existing, normalized)
}

func mergeOpenAICodexModelVerifications(existing []string, incoming []string) []string {
	out := append([]string(nil), existing...)
	for _, item := range incoming {
		out = openAICodexAppendModelVerification(out, item)
	}
	return out
}

func openAICodexRateLimitSummaryFromEventPayload(data []byte) string {
	var decoded struct {
		Type       string `json:"type"`
		RateLimits *struct {
			Primary   *openAICodexRateLimitEventWindow `json:"primary,omitempty"`
			Secondary *openAICodexRateLimitEventWindow `json:"secondary,omitempty"`
		} `json:"rate_limits,omitempty"`
		Credits *struct {
			HasCredits bool   `json:"has_credits"`
			Unlimited  bool   `json:"unlimited"`
			Balance    string `json:"balance,omitempty"`
		} `json:"credits,omitempty"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ""
	}
	if decoded.Type != "codex.rate_limits" {
		return ""
	}
	parts := []string{}
	if decoded.RateLimits != nil {
		if primary := openAICodexRateLimitEventWindowSummary(decoded.RateLimits.Primary, "primary"); primary != "" {
			parts = append(parts, primary)
		}
		if secondary := openAICodexRateLimitEventWindowSummary(decoded.RateLimits.Secondary, "secondary"); secondary != "" {
			parts = append(parts, secondary)
		}
	}
	if decoded.Credits != nil {
		credits := []string{
			fmt.Sprintf("has_credits=%t", decoded.Credits.HasCredits),
			fmt.Sprintf("unlimited=%t", decoded.Credits.Unlimited),
		}
		if balance := strings.TrimSpace(decoded.Credits.Balance); balance != "" {
			credits = append(credits, "balance="+balance)
		}
		parts = append(parts, "credits("+strings.Join(credits, " ")+")")
	}
	return strings.Join(parts, ", ")
}

type openAICodexRateLimitEventWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes *int64  `json:"window_minutes,omitempty"`
	ResetAt       *int64  `json:"reset_at,omitempty"`
}

func openAICodexRateLimitEventWindowSummary(window *openAICodexRateLimitEventWindow, label string) string {
	if window == nil {
		return ""
	}
	parts := []string{label + "=" + fmt.Sprintf("%g", window.UsedPercent) + "%"}
	if window.WindowMinutes != nil {
		parts = append(parts, fmt.Sprintf("window=%dm", *window.WindowMinutes))
	}
	if window.ResetAt != nil {
		parts = append(parts, fmt.Sprintf("reset_at=%d", *window.ResetAt))
	}
	return strings.Join(parts, " ")
}

func openAICodexIncompleteReason(response json.RawMessage) string {
	if len(response) == 0 {
		return ""
	}
	var decoded struct {
		IncompleteDetails *struct {
			Reason string `json:"reason,omitempty"`
		} `json:"incomplete_details,omitempty"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil {
		return ""
	}
	if decoded.IncompleteDetails == nil {
		return ""
	}
	return strings.TrimSpace(decoded.IncompleteDetails.Reason)
}

func openAICodexOutputItemText(item openAICodexOutputItem) string {
	var text strings.Builder
	for _, content := range item.Content {
		if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
			text.WriteString(content.Text)
		}
	}
	return text.String()
}

func openAICodexReasoningItemText(item openAICodexOutputItem) string {
	parts := []string{}
	for _, summary := range item.Summary {
		if strings.TrimSpace(summary.Text) == "" {
			continue
		}
		switch strings.TrimSpace(summary.Type) {
		case "", "summary_text":
			parts = append(parts, summary.Text)
		}
	}
	for _, content := range item.Content {
		if strings.TrimSpace(content.Text) == "" {
			continue
		}
		switch strings.TrimSpace(content.Type) {
		case "reasoning_text", "text", "summary_text":
			parts = append(parts, content.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func openAICodexReasoningEncryptedContent(item openAICodexOutputItem) string {
	if strings.TrimSpace(item.Type) != "reasoning" {
		return ""
	}
	return strings.TrimSpace(item.EncryptedContent)
}

func openAICodexHostedOutputItemText(item openAICodexOutputItem, savedPath string) string {
	switch strings.TrimSpace(item.Type) {
	case "image_generation_call":
		return openAICodexImageGenerationOutputItemText(item, savedPath)
	case "web_search_call":
		return openAICodexWebSearchOutputItemText(item)
	default:
		return ""
	}
}

func openAICodexImageGenerationOutputItemText(item openAICodexOutputItem, savedPath string) string {
	id := firstNonEmptyTrimmed(item.ID, item.CallID, "image_generation")
	status := firstNonEmptyTrimmed(item.Status, "completed")
	parts := []string{fmt.Sprintf("Image generation %s: %s", status, id)}
	if strings.TrimSpace(savedPath) != "" {
		parts = append(parts, "Saved image: "+strings.TrimSpace(savedPath))
	}
	if strings.TrimSpace(item.RevisedPrompt) != "" {
		parts = append(parts, "Revised prompt: "+strings.TrimSpace(item.RevisedPrompt))
	}
	return strings.Join(parts, "\n")
}

func openAICodexWebSearchOutputItemText(item openAICodexOutputItem) string {
	id := firstNonEmptyTrimmed(item.ID, item.CallID, "web_search")
	status := firstNonEmptyTrimmed(item.Status, "completed")
	query := openAICodexWebSearchOutputItemQuery(item)
	if query != "" {
		return fmt.Sprintf("Web search %s: %s (%s)", status, query, id)
	}
	return fmt.Sprintf("Web search %s: %s", status, id)
}

func openAICodexWebSearchCallFromOutputItem(item openAICodexOutputItem) (MessageWebSearchCall, bool) {
	if strings.TrimSpace(item.Type) != "web_search_call" {
		return MessageWebSearchCall{}, false
	}
	call := MessageWebSearchCall{
		ID:     strings.TrimSpace(item.ID),
		Status: strings.TrimSpace(item.Status),
	}
	if len(item.Action) > 0 {
		var action map[string]any
		if err := json.Unmarshal(item.Action, &action); err == nil && len(action) > 0 {
			call.Action = cloneToolDefinitionMap(action)
		}
	}
	return call, true
}

func openAICodexLocalShellCallFromOutputItem(item openAICodexOutputItem) (MessageLocalShellCall, bool) {
	if strings.TrimSpace(item.Type) != "local_shell_call" {
		return MessageLocalShellCall{}, false
	}
	var action map[string]any
	if len(item.Action) > 0 {
		if err := json.Unmarshal(item.Action, &action); err != nil {
			return MessageLocalShellCall{}, false
		}
	}
	if len(action) == 0 {
		return MessageLocalShellCall{}, false
	}
	return MessageLocalShellCall{
		ID:     strings.TrimSpace(item.ID),
		CallID: strings.TrimSpace(item.CallID),
		Status: strings.TrimSpace(item.Status),
		Action: cloneToolDefinitionMap(action),
	}, true
}

func openAICodexCompactionItemFromOutputItem(item openAICodexOutputItem) (MessageCodexCompactionItem, bool) {
	itemType := strings.TrimSpace(item.Type)
	encryptedContent := strings.TrimSpace(item.EncryptedContent)
	switch itemType {
	case "compaction", "compaction_summary":
		if encryptedContent == "" {
			return MessageCodexCompactionItem{}, false
		}
		return MessageCodexCompactionItem{
			Type:             itemType,
			EncryptedContent: encryptedContent,
		}, true
	case "context_compaction":
		return MessageCodexCompactionItem{
			Type:             itemType,
			EncryptedContent: encryptedContent,
		}, true
	case "compaction_trigger":
		return MessageCodexCompactionItem{
			Type: itemType,
		}, true
	default:
		return MessageCodexCompactionItem{}, false
	}
}

func openAICodexToolOutputFromOutputItem(item openAICodexOutputItem) (MessageCodexToolOutputItem, bool) {
	itemType := strings.TrimSpace(item.Type)
	switch itemType {
	case "function_call_output", "custom_tool_call_output", "tool_search_output":
	default:
		return MessageCodexToolOutputItem{}, false
	}
	callID := strings.TrimSpace(item.CallID)
	if callID == "" && itemType != "tool_search_output" {
		return MessageCodexToolOutputItem{}, false
	}
	output := MessageCodexToolOutputItem{
		Type:      itemType,
		CallID:    callID,
		Name:      strings.TrimSpace(item.Name),
		Status:    strings.TrimSpace(item.Status),
		Execution: strings.TrimSpace(item.Execution),
	}
	if itemType == "tool_search_output" {
		output.Tools = cloneOpenAICodexToolMaps(item.Tools)
		return output, true
	}
	output.Text, output.ToolContentItems = openAICodexToolOutputPayloadFromRaw(item.Output)
	return output, true
}

func openAICodexToolOutputPayloadFromRaw(raw json.RawMessage) (string, []ToolContentItem) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var items []ToolContentItem
	if err := json.Unmarshal(raw, &items); err == nil {
		return "", normalizeToolContentItems(items)
	}
	return string(raw), nil
}

func openAICodexWebSearchOutputItemQuery(item openAICodexOutputItem) string {
	if len(item.Action) == 0 {
		return ""
	}
	var action struct {
		Type    string   `json:"type,omitempty"`
		Query   string   `json:"query,omitempty"`
		Queries []string `json:"queries,omitempty"`
		URL     string   `json:"url,omitempty"`
		Pattern string   `json:"pattern,omitempty"`
	}
	if err := json.Unmarshal(item.Action, &action); err != nil {
		return ""
	}
	if query := strings.TrimSpace(action.Query); query != "" {
		return query
	}
	queries := make([]string, 0, len(action.Queries))
	for _, query := range action.Queries {
		if trimmed := strings.TrimSpace(query); trimmed != "" {
			queries = append(queries, trimmed)
		}
	}
	if len(queries) > 0 {
		return strings.Join(queries, ", ")
	}
	url := strings.TrimSpace(action.URL)
	pattern := strings.TrimSpace(action.Pattern)
	if url != "" && pattern != "" {
		return fmt.Sprintf("%s find %q", url, pattern)
	}
	if url != "" {
		return url
	}
	return strings.TrimSpace(action.Type)
}

func saveOpenAICodexImageGenerationOutputItem(root string, sessionID string, item openAICodexOutputItem) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" || strings.TrimSpace(item.Result) == "" {
		return "", nil
	}
	callID := firstNonEmptyTrimmed(item.ID, item.CallID, "generated_image")
	bytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(item.Result))
	if err != nil {
		return "", err
	}
	path := openAICodexImageGenerationArtifactPath(root, sessionID, callID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, bytes, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func openAICodexImageGenerationArtifactPath(root string, sessionID string, callID string) string {
	return filepath.Join(
		root,
		"generated_images",
		sanitizeOpenAICodexImageGenerationPathSegment(sessionID),
		sanitizeOpenAICodexImageGenerationPathSegment(callID)+".png",
	)
}

func sanitizeOpenAICodexImageGenerationPathSegment(value string) string {
	var b strings.Builder
	for _, ch := range strings.TrimSpace(value) {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			b.WriteRune(ch)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "generated_image"
	}
	return out
}

func normalizeOpenAICodexMessagePhase(phase string) string {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case messagePhaseCommentary:
		return messagePhaseCommentary
	case messagePhaseFinalAnswer, "final-answer", "final":
		return messagePhaseFinalAnswer
	default:
		return ""
	}
}

func mergeOpenAICodexMessagePhase(current string, next string) string {
	current = normalizeOpenAICodexMessagePhase(current)
	next = normalizeOpenAICodexMessagePhase(next)
	if current == messagePhaseFinalAnswer || next == "" {
		return current
	}
	if next == messagePhaseFinalAnswer {
		return messagePhaseFinalAnswer
	}
	if current == "" {
		return next
	}
	return current
}

func openAICodexOutputItemIsToolCall(item openAICodexOutputItem) bool {
	switch strings.TrimSpace(item.Type) {
	case "function_call", "custom_tool_call":
		return true
	case "tool_search_call":
		return strings.TrimSpace(item.Execution) == "client"
	default:
		return false
	}
}

func openAICodexToolCallFromOutputItem(item openAICodexOutputItem) (ToolCall, bool) {
	if !openAICodexOutputItemIsToolCall(item) {
		return ToolCall{}, false
	}
	name := openAICodexOutputItemToolName(item)
	if name == "" {
		return ToolCall{}, false
	}
	callID := strings.TrimSpace(item.CallID)
	if name != "tool_search" {
		callID = firstNonEmptyTrimmed(item.CallID, item.ID)
		if callID == "" {
			return ToolCall{}, false
		}
	}
	return ToolCall{
		ID:        callID,
		Name:      name,
		Namespace: strings.TrimSpace(item.Namespace),
		Status:    strings.TrimSpace(item.Status),
		Arguments: normalizeOpenAICodexOutputItemArguments(item, name),
	}, true
}

func openAICodexPopulateStreamToolCall(target *openAICodexStreamToolCall, item openAICodexOutputItem, replaceArguments bool) bool {
	if target == nil || !openAICodexOutputItemIsToolCall(item) {
		return false
	}
	name := openAICodexOutputItemToolName(item)
	target.ID = firstNonEmptyTrimmed(item.CallID, item.ID, target.ID)
	target.Name = firstNonEmptyTrimmed(name, target.Name)
	target.Namespace = firstNonEmptyTrimmed(item.Namespace, target.Namespace)
	target.Status = firstNonEmptyTrimmed(item.Status, target.Status)
	target.Type = firstNonEmptyTrimmed(item.Type, target.Type)
	if strings.TrimSpace(target.Name) == "" {
		return false
	}
	if strings.TrimSpace(target.ID) == "" && strings.TrimSpace(target.Name) != "tool_search" {
		return false
	}
	if openAICodexOutputItemHasArguments(item) {
		arguments := normalizeOpenAICodexOutputItemArguments(item, target.Name)
		if strings.TrimSpace(arguments) != "" && (replaceArguments || target.Arguments.Len() == 0) {
			target.Arguments.Reset()
			target.Arguments.WriteString(arguments)
		}
	}
	return true
}

func openAICodexOutputItemToolName(item openAICodexOutputItem) string {
	if strings.TrimSpace(item.Type) == "tool_search_call" {
		return "tool_search"
	}
	name := strings.TrimSpace(item.Name)
	namespace := strings.TrimSpace(item.Namespace)
	if namespace == "" || name == "" {
		return name
	}
	return namespace + name
}

func openAICodexOutputItemHasArguments(item openAICodexOutputItem) bool {
	switch strings.TrimSpace(item.Type) {
	case "custom_tool_call":
		return firstNonEmptyTrimmed(item.Input, openAICodexRawArgumentsText(item.Arguments)) != ""
	default:
		raw := strings.TrimSpace(string(item.Arguments))
		return raw != "" && raw != "null"
	}
}

func normalizeOpenAICodexStreamToolCallArguments(item *openAICodexStreamToolCall) string {
	if item == nil {
		return "{}"
	}
	raw := strings.TrimSpace(item.Arguments.String())
	if strings.TrimSpace(item.Type) == "custom_tool_call" {
		if raw == "" {
			return normalizeOpenAICodexToolCallArguments(item.Type, item.Name, raw)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err == nil {
			if _, ok := payload["patch"]; ok {
				return normalizeOpenAIToolCallArguments(raw)
			}
			if _, ok := payload["raw"]; ok {
				return normalizeOpenAIToolCallArguments(raw)
			}
		}
		return normalizeOpenAICodexToolCallArguments(item.Type, item.Name, raw)
	}
	return normalizeOpenAIToolCallArguments(raw)
}

func normalizeOpenAICodexOutputItemArguments(item openAICodexOutputItem, name string) string {
	switch strings.TrimSpace(item.Type) {
	case "custom_tool_call":
		input := firstNonEmptyTrimmed(item.Input, openAICodexRawArgumentsText(item.Arguments))
		return normalizeOpenAICodexToolCallArguments(item.Type, name, input)
	case "tool_search_call":
		return normalizeOpenAIToolCallArguments(openAICodexRawArgumentsJSON(item.Arguments))
	default:
		return normalizeOpenAIToolCallArguments(openAICodexRawArgumentsText(item.Arguments))
	}
}

func normalizeOpenAICodexToolCallArguments(itemType string, name string, raw string) string {
	if strings.TrimSpace(itemType) == "custom_tool_call" && strings.TrimSpace(name) == "apply_patch" {
		encoded, err := json.Marshal(map[string]any{
			"patch": strings.TrimSpace(raw),
		})
		if err == nil {
			return string(encoded)
		}
	}
	return normalizeOpenAIToolCallArguments(raw)
}

func openAICodexRawArgumentsText(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return trimmed
}

func openAICodexRawArgumentsJSON(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "{}"
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err == nil {
		return compact.String()
	}
	return trimmed
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
	authPath            string
	httpClient          *http.Client
	allowedWorkspaceIDs []string
}

func NewCodexOAuthTokenSource(authPath string, httpClient *http.Client) *CodexOAuthTokenSource {
	return NewCodexOAuthTokenSourceWithWorkspaceIDs(authPath, httpClient, nil)
}

func NewCodexOAuthTokenSourceWithWorkspaceIDs(authPath string, httpClient *http.Client, allowedWorkspaceIDs []string) *CodexOAuthTokenSource {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	normalizedWorkspaces := normalizeForcedChatGPTWorkspaceIDs(allowedWorkspaceIDs)
	return &CodexOAuthTokenSource{
		authPath:            strings.TrimSpace(authPath),
		httpClient:          httpClient,
		allowedWorkspaceIDs: append([]string(nil), normalizedWorkspaces...),
	}
}

func (s *CodexOAuthTokenSource) AccessToken(ctx context.Context) (string, error) {
	if token := strings.TrimSpace(os.Getenv(openAICodexAccessTokenEnv)); token != "" {
		if err := validateCodexOAuthTokenWorkspaceID(token, s.allowedWorkspaceIDs); err != nil {
			return "", err
		}
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
		if err := validateCodexOAuthWorkspace(auth.Tokens, s.allowedWorkspaceIDs); err != nil {
			return "", err
		}
		return auth.Tokens.AccessToken, nil
	}

	codexOAuthRefreshMu.Lock()
	defer codexOAuthRefreshMu.Unlock()

	data, auth, err = readCodexOAuthAuthFile(authPath)
	if err != nil {
		return "", err
	}
	if tokenUsable(auth.Tokens.AccessToken, openAICodexTokenRefreshSkew) {
		if err := validateCodexOAuthWorkspace(auth.Tokens, s.allowedWorkspaceIDs); err != nil {
			return "", err
		}
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
	if err := validateCodexOAuthWorkspace(refreshed, s.allowedWorkspaceIDs); err != nil {
		return "", err
	}
	if err := updateCodexOAuthAuthFile(authPath, data, refreshed); err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

func (s *CodexOAuthTokenSource) RefreshAfterUnauthorized(ctx context.Context) (string, error) {
	if token := strings.TrimSpace(os.Getenv(openAICodexAccessTokenEnv)); token != "" {
		return "", fmt.Errorf("%s is set, so OpenAI Codex OAuth refresh is unavailable", openAICodexAccessTokenEnv)
	}
	authPath := s.authPath
	if authPath == "" {
		authPath = codexOAuthAuthFilePath()
	}

	codexOAuthRefreshMu.Lock()
	defer codexOAuthRefreshMu.Unlock()

	data, auth, err := readCodexOAuthAuthFile(authPath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(auth.Tokens.RefreshToken) == "" {
		return "", fmt.Errorf("OpenAI Codex OAuth access token was rejected and no refresh token is available; run /codex-auth login")
	}
	refreshed, err := s.refresh(ctx, auth.Tokens.RefreshToken)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(refreshed.AccessToken) == "" {
		return "", fmt.Errorf("OpenAI Codex OAuth refresh returned no access token")
	}
	if err := validateCodexOAuthWorkspace(refreshed, s.allowedWorkspaceIDs); err != nil {
		return "", err
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
		return codexOAuthTokens{}, newProviderHTTPErrorWithHeaders("openai-codex", resp.StatusCode, resp.Status, data, "", resp.Header)
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
	return runCodexOAuthDeviceLoginWithWorkspaces(ctx, writer, authPath, httpClient, nil)
}

func runCodexOAuthDeviceLoginWithWorkspaces(ctx context.Context, writer io.Writer, authPath string, httpClient *http.Client, allowedWorkspaceIDs []string) (codexOAuthTokens, error) {
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
	if err := validateCodexOAuthWorkspace(tokens, allowedWorkspaceIDs); err != nil {
		return codexOAuthTokens{}, err
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
	return importCodexCLIOAuthAuthFileWithWorkspaces(destPath, nil)
}

func importCodexCLIOAuthAuthFileWithWorkspaces(destPath string, allowedWorkspaceIDs []string) error {
	sourcePath := codexCLIOAuthAuthFilePath()
	if strings.TrimSpace(sourcePath) == "" {
		return fmt.Errorf("Codex CLI OAuth auth file path is unavailable")
	}
	_, auth, err := readCodexOAuthAuthFile(sourcePath)
	if err != nil {
		return err
	}
	if err := validateCodexOAuthWorkspace(auth.Tokens, allowedWorkspaceIDs); err != nil {
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
			err = newProviderHTTPErrorWithHeaders("openai-codex", resp.StatusCode, resp.Status, data, "", resp.Header)
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
	return codexOAuthAuthFileUsableForWorkspaces(path, nil)
}

func codexOAuthAuthFileUsableForWorkspaces(path string, allowedWorkspaceIDs []string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		path = codexOAuthAuthFilePath()
	}
	_, auth, err := readCodexOAuthAuthFile(path)
	if err != nil {
		return false
	}
	if err := validateCodexOAuthWorkspace(auth.Tokens, allowedWorkspaceIDs); err != nil {
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

func validateCodexOAuthWorkspace(tokens codexOAuthTokens, allowedWorkspaceIDs []string) error {
	allowed := normalizeForcedChatGPTWorkspaceIDs(allowedWorkspaceIDs)
	if len(allowed) == 0 {
		return nil
	}
	actual := firstNonEmptyTrimmed(
		tokens.AccountID,
		codexOAuthJWTChatGPTAccountID(tokens.IDToken),
		codexOAuthJWTChatGPTAccountID(tokens.AccessToken),
	)
	if actual == "" {
		return fmt.Errorf("OpenAI Codex OAuth login is restricted to workspace id(s) %s, but the token did not include a chatgpt_account_id claim", forcedChatGPTWorkspaceIDsDisplay(allowed))
	}
	for _, workspaceID := range allowed {
		if actual == workspaceID {
			return nil
		}
	}
	return fmt.Errorf("OpenAI Codex OAuth login is restricted to workspace id(s) %s, but token workspace is %q", forcedChatGPTWorkspaceIDsDisplay(allowed), actual)
}

func validateCodexOAuthTokenWorkspaceID(token string, allowedWorkspaceIDs []string) error {
	allowed := normalizeForcedChatGPTWorkspaceIDs(allowedWorkspaceIDs)
	if len(allowed) == 0 {
		return nil
	}
	actual := codexOAuthJWTChatGPTAccountID(token)
	if actual == "" {
		return fmt.Errorf("OpenAI Codex OAuth access token is restricted to workspace id(s) %s, but the token did not include a chatgpt_account_id claim", forcedChatGPTWorkspaceIDsDisplay(allowed))
	}
	for _, workspaceID := range allowed {
		if actual == workspaceID {
			return nil
		}
	}
	return fmt.Errorf("OpenAI Codex OAuth access token is restricted to workspace id(s) %s, but token workspace is %q", forcedChatGPTWorkspaceIDsDisplay(allowed), actual)
}

func codexOAuthJWTChatGPTAccountID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return ""
	}
	if auth, ok := raw["https://api.openai.com/auth"].(map[string]any); ok {
		if accountID := strings.TrimSpace(codexOAuthStringField(auth, "chatgpt_account_id", "account_id", "organization_id")); accountID != "" {
			return accountID
		}
	}
	return strings.TrimSpace(codexOAuthStringField(raw, "chatgpt_account_id", "account_id", "organization_id"))
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

func resolveOpenAICodexInstallationID() string {
	id, _ := resolveOpenAICodexInstallationIDAtPath(filepath.Join(userConfigDir(), openAICodexInstallationFile))
	return id
}

func resolveOpenAICodexInstallationIDAtPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("installation id path is empty")
	}
	if data, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return id, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	id := newOpenAICodexInstallationID()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

func newOpenAICodexInstallationID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		raw[6] = (raw[6] & 0x0f) | 0x40
		raw[8] = (raw[8] & 0x3f) | 0x80
		hexed := hex.EncodeToString(raw[:])
		return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:32]
	}
	return newOpenAICodexRequestID()
}
