package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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

type refreshingCodexTokenSource struct {
	token          string
	refreshedToken string
	accessCalls    int
	refreshCalls   int
}

func (s *refreshingCodexTokenSource) AccessToken(ctx context.Context) (string, error) {
	s.accessCalls++
	return s.token, nil
}

func (s *refreshingCodexTokenSource) RefreshAfterUnauthorized(ctx context.Context) (string, error) {
	s.refreshCalls++
	s.token = s.refreshedToken
	return s.refreshedToken, nil
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
	if _, ok := reasoning["summary"]; ok {
		t.Fatalf("expected default reasoning summary to be omitted, got %#v", payload["reasoning"])
	}
	include, ok := payload["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("expected reasoning encrypted content include, got %#v", payload["include"])
	}
	textControls, ok := payload["text"].(map[string]any)
	if !ok || textControls["verbosity"] != "low" {
		t.Fatalf("expected default verbosity low, got %#v", payload["text"])
	}
	format, ok := textControls["format"].(map[string]any)
	if !ok {
		t.Fatalf("expected JSON mode format to be preserved, got %#v", payload["text"])
	}
	if format["type"] != "json_schema" || format["name"] != "codex_json_object" || format["strict"] != false {
		t.Fatalf("expected Codex JSON schema text format, got %#v", format)
	}
	schema, ok := format["schema"].(map[string]any)
	if !ok || schema["type"] != "object" || schema["additionalProperties"] != true {
		t.Fatalf("expected permissive JSON object schema, got %#v", format["schema"])
	}
	if _, ok := payload["tools"].([]any); !ok {
		t.Fatalf("expected responses tools array, got %#v", payload["tools"])
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) != 4 {
		t.Fatalf("expected four input items, got %#v", payload["input"])
	}
	user, ok := input[0].(map[string]any)
	if !ok || user["type"] != "message" || user["role"] != "user" {
		t.Fatalf("expected user input to be a tagged message item, got %#v", input[0])
	}
	assistant, ok := input[1].(map[string]any)
	if !ok || assistant["type"] != "message" || assistant["phase"] != messagePhaseCommentary {
		t.Fatalf("expected assistant tool preamble to carry commentary phase, got %#v", input[1])
	}
	encoded := string(body)
	for _, needle := range []string{`"stream":true`, `"type":"function_call"`, `"call_id":"call_1"`, `"type":"function_call_output"`, `"type":"json_schema"`} {
		if !strings.Contains(encoded, needle) {
			t.Fatalf("expected %q in request body %s", needle, encoded)
		}
	}
}

func TestBuildOpenAICodexRequestBodyPreservesPromptCacheKeyAndMetadata(t *testing.T) {
	body, err := buildOpenAICodexRequestBodyWithClientMetadata(ChatRequest{
		Model:    "gpt-5.5",
		ThreadID: "thread-456",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	}, map[string]string{
		"x-codex-installation-id": "install-123",
		"empty":                   "  ",
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBodyWithClientMetadata: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if payload["prompt_cache_key"] != "thread-456" {
		t.Fatalf("expected prompt_cache_key to use thread id, got %#v", payload["prompt_cache_key"])
	}
	metadata, ok := payload["client_metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected client_metadata object, got %#v", payload["client_metadata"])
	}
	if metadata["x-codex-installation-id"] != "install-123" {
		t.Fatalf("expected installation id metadata, got %#v", metadata)
	}
	if _, ok := metadata["empty"]; ok {
		t.Fatalf("empty metadata values should be dropped: %#v", metadata)
	}
	if payload["tool_choice"] != "auto" {
		t.Fatalf("expected tool_choice auto even without tools, got %#v", payload["tool_choice"])
	}
	if payload["parallel_tool_calls"] != true {
		t.Fatalf("expected gpt-5.5 to enable parallel tool calls, got %#v", payload["parallel_tool_calls"])
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 0 {
		t.Fatalf("expected empty tools array without tools, got %#v", payload["tools"])
	}
	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "medium" {
		t.Fatalf("expected default Codex reasoning effort medium, got %#v", payload["reasoning"])
	}
	include, ok := payload["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("expected reasoning encrypted content include, got %#v", payload["include"])
	}
	textControls, ok := payload["text"].(map[string]any)
	if !ok || textControls["verbosity"] != "low" {
		t.Fatalf("expected default verbosity low, got %#v", payload["text"])
	}
}

func TestBuildOpenAICodexRequestBodyUsesCatalogReasoningDefaults(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.2",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "medium" || reasoning["summary"] != "auto" {
		t.Fatalf("expected gpt-5.2 reasoning defaults, got %#v", payload["reasoning"])
	}
	include, ok := payload["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("expected reasoning encrypted content include, got %#v", payload["include"])
	}
}

func TestBuildOpenAICodexRequestBodyPreservesDeveloperMessages(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "developer", Text: "follow AGENTS.md and workspace policy"},
			{Role: "user", Text: "hello"},
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
	if !ok || len(input) != 2 {
		t.Fatalf("expected developer and user input items, got %#v", payload["input"])
	}
	developer, ok := input[0].(map[string]any)
	if !ok || developer["type"] != "message" || developer["role"] != "developer" {
		t.Fatalf("expected first item to be developer, got %#v", input[0])
	}
	user, ok := input[1].(map[string]any)
	if !ok || user["type"] != "message" || user["role"] != "user" {
		t.Fatalf("expected second item to be user message, got %#v", input[1])
	}
	content, ok := developer["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected developer text content, got %#v", developer["content"])
	}
	text, ok := content[0].(map[string]any)
	if !ok || text["text"] != "follow AGENTS.md and workspace policy" {
		t.Fatalf("expected developer text to be preserved, got %#v", content[0])
	}
}

func TestBuildOpenAICodexRequestBodyOmitsDefaultVerbosityForUnknownModel(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "test-no-verbosity",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := payload["text"]; ok {
		t.Fatalf("expected text controls to be omitted for unknown model, got %#v", payload["text"])
	}
	if reasoning, ok := payload["reasoning"]; !ok || reasoning != nil {
		t.Fatalf("expected reasoning null for unknown model, got %#v ok=%v", payload["reasoning"], ok)
	}
	if payload["parallel_tool_calls"] != false {
		t.Fatalf("expected unknown model to disable parallel tool calls, got %#v", payload["parallel_tool_calls"])
	}
	include, ok := payload["include"].([]any)
	if !ok || len(include) != 0 {
		t.Fatalf("expected empty include for unknown model, got %#v", payload["include"])
	}
}

func TestBuildOpenAICodexRequestBodyUsesCatalogVerbosityDefaults(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.4-mini",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	textControls, ok := payload["text"].(map[string]any)
	if !ok || textControls["verbosity"] != "medium" {
		t.Fatalf("expected gpt-5.4-mini default verbosity medium, got %#v", payload["text"])
	}
}

func TestBuildOpenAICodexRequestBodyUsesCustomApplyPatchTool(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "edit the file"},
		},
		Tools: []ToolDefinition{
			NewApplyPatchTool(Workspace{}).Definition(),
			{
				Name:        "read_file",
				Description: "Read file",
				InputSchema: map[string]any{
					"type": "object",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("expected two tools, got %#v in %s", payload["tools"], body)
	}
	applyPatch := tools[0].(map[string]any)
	if applyPatch["type"] != "custom" || applyPatch["name"] != "apply_patch" {
		t.Fatalf("expected apply_patch to use Responses custom tool shape, got %#v", applyPatch)
	}
	if _, exists := applyPatch["parameters"]; exists {
		t.Fatalf("custom apply_patch must not be exposed as JSON parameters, got %#v", applyPatch)
	}
	format, ok := applyPatch["format"].(map[string]any)
	if !ok || format["type"] != "grammar" || format["syntax"] != "lark" {
		t.Fatalf("expected lark grammar format, got %#v", applyPatch["format"])
	}
	definition, _ := format["definition"].(string)
	for _, want := range []string{"start: begin_patch hunk+ end_patch", "*** Begin Patch", "*** End Patch"} {
		if !strings.Contains(definition, want) {
			t.Fatalf("expected apply_patch grammar to contain %q, got %q", want, definition)
		}
	}
	readFile := tools[1].(map[string]any)
	if readFile["type"] != "function" || readFile["name"] != "read_file" {
		t.Fatalf("expected non-apply_patch tools to remain functions, got %#v", readFile)
	}
	if strict, ok := readFile["strict"].(bool); !ok || strict {
		t.Fatalf("expected function tool strict=false to match Codex Responses shape, got %#v", readFile["strict"])
	}
}

func TestBuildOpenAICodexRequestBodyUsesNamespaceForMCPTools(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "use mcp"},
		},
		Tools: []ToolDefinition{
			{
				Name:        "mcp__web_research__search_web",
				Description: "[MCP:web_research] Search web",
				InputSchema: map[string]any{
					"type": "object",
				},
			},
			{
				Name:        "read_file",
				Description: "Read file",
				InputSchema: map[string]any{
					"type": "object",
				},
			},
			{
				Name:        "mcp__web_research__fetch_url",
				Description: "[MCP:web_research] Fetch URL",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"url": map[string]any{"type": "string"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("expected merged namespace plus ordinary tool, got %#v in %s", payload["tools"], body)
	}
	namespace := tools[0].(map[string]any)
	if namespace["type"] != "namespace" || namespace["name"] != "mcp__web_research__" {
		t.Fatalf("expected MCP tools to use Responses namespace shape, got %#v", namespace)
	}
	if namespace["description"] != "Tools in the mcp__web_research__ namespace." {
		t.Fatalf("expected default namespace description, got %#v", namespace["description"])
	}
	namespaceTools, ok := namespace["tools"].([]any)
	if !ok || len(namespaceTools) != 2 {
		t.Fatalf("expected two child namespace tools, got %#v", namespace["tools"])
	}
	fetchURL := namespaceTools[0].(map[string]any)
	searchWeb := namespaceTools[1].(map[string]any)
	if fetchURL["type"] != "function" || fetchURL["name"] != "fetch_url" {
		t.Fatalf("expected namespace child tools to be sorted by child name, got %#v", namespaceTools)
	}
	if searchWeb["type"] != "function" || searchWeb["name"] != "search_web" {
		t.Fatalf("expected namespace child name without namespace prefix, got %#v", searchWeb)
	}
	if strict, ok := searchWeb["strict"].(bool); !ok || strict {
		t.Fatalf("expected namespace function strict=false to match Codex Responses shape, got %#v", searchWeb["strict"])
	}
	if _, exists := namespace["parameters"]; exists {
		t.Fatalf("namespace wrapper must not expose function parameters, got %#v", namespace)
	}
	readFile := tools[1].(map[string]any)
	if readFile["type"] != "function" || readFile["name"] != "read_file" {
		t.Fatalf("expected ordinary tools to remain functions, got %#v", readFile)
	}
}

func TestBuildOpenAICodexRequestBodyPreservesDeferredToolDefinitions(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "use deferred tools"},
		},
		Tools: []ToolDefinition{
			{
				Name:         "lookup_order",
				Description:  "Look up an order",
				InputSchema:  map[string]any{"type": "object"},
				DeferLoading: true,
			},
			{
				Name:         "mcp__orders__lookup_order",
				Description:  "[MCP:orders] Look up an order",
				InputSchema:  map[string]any{"type": "object"},
				DeferLoading: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("expected deferred function and namespace tools, got %#v in %s", payload["tools"], body)
	}
	functionTool := tools[0].(map[string]any)
	if functionTool["type"] != "function" || functionTool["defer_loading"] != true {
		t.Fatalf("expected function defer_loading=true, got %#v", functionTool)
	}
	namespace := tools[1].(map[string]any)
	children := namespace["tools"].([]any)
	child := children[0].(map[string]any)
	if namespace["type"] != "namespace" || child["name"] != "lookup_order" || child["defer_loading"] != true {
		t.Fatalf("expected namespace child defer_loading=true, got %#v", namespace)
	}
}

func TestBuildOpenAICodexRequestBodyUsesNativeToolSearchTool(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "find a deferred tool"},
		},
		Tools: []ToolDefinition{
			{
				Name:        "tool_search",
				Description: "Search app tools",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Search query for deferred tools.",
						},
						"limit": map[string]any{
							"type":        "number",
							"description": "Maximum number of tools to return.",
						},
					},
					"required":             []any{"query"},
					"additionalProperties": false,
				},
			},
			{
				Name:        "read_file",
				Description: "Read file",
				InputSchema: map[string]any{
					"type": "object",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("expected two tools, got %#v in %s", payload["tools"], body)
	}
	toolSearch := tools[0].(map[string]any)
	if toolSearch["type"] != "tool_search" || toolSearch["execution"] != "client" {
		t.Fatalf("expected native client tool_search tool, got %#v", toolSearch)
	}
	if _, exists := toolSearch["name"]; exists {
		t.Fatalf("native tool_search must not include function name, got %#v", toolSearch)
	}
	if _, exists := toolSearch["strict"]; exists {
		t.Fatalf("native tool_search must not include function strict flag, got %#v", toolSearch)
	}
	params, ok := toolSearch["parameters"].(map[string]any)
	if !ok || params["type"] != "object" || params["additionalProperties"] != false {
		t.Fatalf("expected tool_search parameters to be preserved, got %#v", toolSearch["parameters"])
	}
	readFile := tools[1].(map[string]any)
	if readFile["type"] != "function" || readFile["name"] != "read_file" {
		t.Fatalf("expected non-native tools to remain functions, got %#v", readFile)
	}
}

func TestBuildOpenAICodexRequestBodyUsesNativeHostedTools(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "use hosted tools"},
		},
		Tools: []ToolDefinition{
			{
				Name:        "image_generation",
				Description: "Generate an image",
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "web_search",
				Description: "Search the web",
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "read_file",
				Description: "Read file",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 3 {
		t.Fatalf("expected three tools, got %#v in %s", payload["tools"], body)
	}
	imageGeneration := tools[0].(map[string]any)
	if imageGeneration["type"] != "image_generation" || imageGeneration["output_format"] != "png" {
		t.Fatalf("expected native image_generation tool, got %#v", imageGeneration)
	}
	if _, exists := imageGeneration["name"]; exists {
		t.Fatalf("native image_generation must not include function name, got %#v", imageGeneration)
	}
	webSearch := tools[1].(map[string]any)
	if webSearch["type"] != "web_search" {
		t.Fatalf("expected native web_search tool, got %#v", webSearch)
	}
	if webSearch["external_web_access"] != false {
		t.Fatalf("expected native web_search to default to cached access, got %#v", webSearch)
	}
	if _, exists := webSearch["name"]; exists {
		t.Fatalf("native web_search must not include function name, got %#v", webSearch)
	}
	readFile := tools[2].(map[string]any)
	if readFile["type"] != "function" || readFile["name"] != "read_file" {
		t.Fatalf("expected ordinary tool to remain a function, got %#v", readFile)
	}
}

func TestBuildOpenAICodexRequestBodyPreservesHostedWebSearchOptions(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "use hosted web search"},
		},
		Tools: []ToolDefinition{
			{
				Name:        "web_search",
				Description: "Search the web",
				InputSchema: map[string]any{"type": "object"},
				HostedOptions: map[string]any{
					"external_web_access": true,
					"filters": map[string]any{
						"allowed_domains": []any{"example.com"},
					},
					"user_location": map[string]any{
						"type":     "approximate",
						"country":  "US",
						"timezone": "America/Los_Angeles",
					},
					"search_context_size":  "low",
					"search_content_types": []any{"text", "image"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one hosted tool, got %#v in %s", payload["tools"], body)
	}
	webSearch := tools[0].(map[string]any)
	if webSearch["type"] != "web_search" || webSearch["external_web_access"] != true {
		t.Fatalf("expected live native web_search tool, got %#v", webSearch)
	}
	filters := webSearch["filters"].(map[string]any)
	if domains := filters["allowed_domains"].([]any); len(domains) != 1 || domains[0] != "example.com" {
		t.Fatalf("expected allowed domain filter, got %#v", filters)
	}
	location := webSearch["user_location"].(map[string]any)
	if location["type"] != "approximate" || location["country"] != "US" || location["timezone"] != "America/Los_Angeles" {
		t.Fatalf("expected approximate user location, got %#v", location)
	}
	if webSearch["search_context_size"] != "low" {
		t.Fatalf("expected search_context_size=low, got %#v", webSearch)
	}
	contentTypes := webSearch["search_content_types"].([]any)
	if len(contentTypes) != 2 || contentTypes[0] != "text" || contentTypes[1] != "image" {
		t.Fatalf("expected text/image content types, got %#v", contentTypes)
	}
}

func TestBuildOpenAICodexRequestBodyRoundTripsApplyPatchAsCustomItems(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: main.go\n+package main\n*** End Patch\n"
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "edit"},
			{Role: "assistant", ToolCalls: []ToolCall{{
				ID:        "call_patch",
				Name:      "apply_patch",
				Arguments: mustJSON(map[string]any{"patch": patch}),
			}}},
			{Role: "tool", ToolCallID: "call_patch", ToolName: "apply_patch", Text: "Patch applied."},
		},
		Tools: []ToolDefinition{NewApplyPatchTool(Workspace{}).Definition()},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) != 3 {
		t.Fatalf("expected three input items, got %#v in %s", payload["input"], body)
	}
	call := input[1].(map[string]any)
	if call["type"] != "custom_tool_call" || call["name"] != "apply_patch" || call["call_id"] != "call_patch" {
		t.Fatalf("expected custom apply_patch call item, got %#v", call)
	}
	if call["input"] != strings.TrimSpace(patch) {
		t.Fatalf("expected raw patch input, got %#v", call["input"])
	}
	if _, exists := call["arguments"]; exists {
		t.Fatalf("custom apply_patch call must not use JSON arguments, got %#v", call)
	}
	output := input[2].(map[string]any)
	if output["type"] != "custom_tool_call_output" || output["call_id"] != "call_patch" {
		t.Fatalf("expected custom apply_patch output item, got %#v", output)
	}
	if _, exists := output["name"]; exists {
		t.Fatalf("custom apply_patch output should omit name to match Codex, got %#v", output)
	}
}

func TestBuildOpenAICodexRequestBodyPreservesToolSearchOutput(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "discover tool"},
			{Role: "assistant", ToolCalls: []ToolCall{{
				ID:        "call_search",
				Name:      "tool_search",
				Arguments: mustJSON(map[string]any{"query": "apply_patch"}),
			}}},
			{
				Role:       "tool",
				ToolCallID: "call_search",
				ToolName:   "tool_search",
				Text: mustJSON(map[string]any{
					"tools": []any{
						map[string]any{
							"type":        "function",
							"name":        "apply_patch",
							"description": "Apply patch",
							"output_schema": map[string]any{
								"type": "object",
							},
						},
					},
				}),
			},
		},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	input := payload["input"].([]any)
	call := input[1].(map[string]any)
	if call["type"] != "tool_search_call" || call["call_id"] != "call_search" || call["execution"] != "client" {
		t.Fatalf("expected Codex tool_search_call item, got %#v", call)
	}
	arguments, ok := call["arguments"].(map[string]any)
	if !ok || arguments["query"] != "apply_patch" {
		t.Fatalf("expected native tool_search arguments object, got %#v", call["arguments"])
	}
	if _, exists := call["name"]; exists {
		t.Fatalf("native tool_search_call must not include function name, got %#v", call)
	}
	output := input[2].(map[string]any)
	if output["type"] != "tool_search_output" || output["call_id"] != "call_search" || output["status"] != "completed" || output["execution"] != "client" {
		t.Fatalf("expected Codex tool_search_output item, got %#v", output)
	}
	tools, ok := output["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one discovered tool, got %#v in %s", output["tools"], body)
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "apply_patch" {
		t.Fatalf("expected apply_patch tool payload, got %#v", tool)
	}
	if tool["defer_loading"] != true {
		t.Fatalf("expected tool_search function result to set defer_loading=true, got %#v", tool)
	}
	if _, exists := tool["output_schema"]; exists {
		t.Fatalf("Codex tool_search output tool specs must omit output_schema, got %#v", tool)
	}
	if encoded := string(body); strings.Contains(encoded, `"type":"function_call_output"`) {
		t.Fatalf("native tool_search result must not be serialized as function_call_output: %s", encoded)
	}
}

func TestBuildOpenAICodexRequestBodyMarksToolSearchNamespaceOutputDeferred(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "discover mcp tool"},
			{Role: "assistant", ToolCalls: []ToolCall{{
				ID:        "call_search",
				Name:      "tool_search",
				Arguments: mustJSON(map[string]any{"query": "mcp"}),
			}}},
			{
				Role:       "tool",
				ToolCallID: "call_search",
				ToolName:   "tool_search",
				Text: mustJSON(map[string]any{
					"tools": []any{
						map[string]any{
							"type": "namespace",
							"name": "mcp__web_research__",
							"tools": []any{
								map[string]any{
									"type":        "function",
									"name":        "search_web",
									"description": "Search web",
									"output_schema": map[string]any{
										"type": "object",
									},
								},
							},
						},
						map[string]any{
							"type":        "namespace",
							"name":        "mcp__web_research__",
							"description": "Tools for web research.",
							"tools": []any{
								map[string]any{
									"type":        "function",
									"name":        "fetch_url",
									"description": "Fetch URL",
								},
							},
						},
					},
				}),
			},
		},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	input := payload["input"].([]any)
	output := input[2].(map[string]any)
	tools, ok := output["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected coalesced namespace tool_search output, got %#v in %s", output["tools"], body)
	}
	namespace := tools[0].(map[string]any)
	if namespace["type"] != "namespace" || namespace["name"] != "mcp__web_research__" {
		t.Fatalf("expected namespace loadable spec, got %#v", namespace)
	}
	if namespace["description"] != "Tools in the mcp__web_research__ namespace." {
		t.Fatalf("expected first non-empty/default namespace description to be preserved, got %#v", namespace["description"])
	}
	children, ok := namespace["tools"].([]any)
	if !ok || len(children) != 2 {
		t.Fatalf("expected merged namespace children, got %#v", namespace["tools"])
	}
	fetchURL := children[0].(map[string]any)
	searchWeb := children[1].(map[string]any)
	if fetchURL["name"] != "fetch_url" || searchWeb["name"] != "search_web" {
		t.Fatalf("expected namespace children sorted by name, got %#v", children)
	}
	for _, child := range []map[string]any{fetchURL, searchWeb} {
		if child["defer_loading"] != true {
			t.Fatalf("expected namespace child to set defer_loading=true, got %#v", child)
		}
		if _, exists := child["output_schema"]; exists {
			t.Fatalf("expected namespace child output_schema to be stripped, got %#v", child)
		}
	}
}

func TestBuildOpenAICodexRequestBodyFiltersToolSearchOutputToLoadableSpecs(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "discover tools"},
			{Role: "assistant", ToolCalls: []ToolCall{{
				ID:        "call_search",
				Name:      "tool_search",
				Arguments: mustJSON(map[string]any{"query": "tools"}),
			}}},
			{
				Role:       "tool",
				ToolCallID: "call_search",
				ToolName:   "tool_search",
				Text: mustJSON(map[string]any{
					"tools": []any{
						map[string]any{
							"type":        "tool_search",
							"execution":   "client",
							"description": "Recursive search should not be re-exposed",
							"parameters":  map[string]any{"type": "object"},
						},
						map[string]any{
							"type":          "image_generation",
							"output_format": "png",
						},
						map[string]any{
							"type": "web_search",
						},
						map[string]any{
							"type":        "custom",
							"name":        "apply_patch",
							"description": "Freeform tools are not loadable tool_search specs",
						},
						map[string]any{
							"type":        "function",
							"name":        "read_file",
							"description": "Read file",
						},
						map[string]any{
							"type": "namespace",
							"name": "mcp__repo__",
							"tools": []any{
								map[string]any{
									"type":        "function",
									"name":        "inspect",
									"description": "Inspect repo",
								},
								map[string]any{
									"type": "web_search",
								},
							},
						},
					},
				}),
			},
		},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	input := payload["input"].([]any)
	output := input[2].(map[string]any)
	tools, ok := output["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("expected only function and namespace loadable specs, got %#v in %s", output["tools"], body)
	}
	functionTool := tools[0].(map[string]any)
	if functionTool["type"] != "function" || functionTool["name"] != "read_file" || functionTool["defer_loading"] != true {
		t.Fatalf("expected deferred read_file function, got %#v", functionTool)
	}
	namespace := tools[1].(map[string]any)
	if namespace["type"] != "namespace" || namespace["name"] != "mcp__repo__" {
		t.Fatalf("expected namespace loadable spec, got %#v", namespace)
	}
	if namespace["description"] != "Tools in the mcp__repo__ namespace." {
		t.Fatalf("expected default namespace description, got %#v", namespace["description"])
	}
	children, ok := namespace["tools"].([]any)
	if !ok || len(children) != 1 {
		t.Fatalf("expected unsupported namespace children to be filtered, got %#v", namespace["tools"])
	}
	child := children[0].(map[string]any)
	if child["type"] != "function" || child["name"] != "inspect" || child["defer_loading"] != true {
		t.Fatalf("expected deferred namespace child function, got %#v", child)
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
	if _, ok := tool["output_schema"]; ok {
		t.Fatalf("Codex Responses tool schema must omit output_schema, got %#v", tool)
	}
}

func TestBuildOpenAICodexRequestBodyCollapsesSingleTextToolContentItem(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "run tool"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_text", Name: "read_file", Arguments: `{"path":"main.go"}`}}},
			{
				Role:       "tool",
				ToolCallID: "call_text",
				ToolName:   "read_file",
				Text:       "fallback text",
				ToolContentItems: []ToolContentItem{{
					Type: "input_text",
					Text: "file contents",
				}},
			},
		},
		Tools: []ToolDefinition{{
			Name:        "read_file",
			Description: "Read file",
			InputSchema: map[string]any{
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
	for _, raw := range input {
		item := raw.(map[string]any)
		if item["type"] != "function_call_output" {
			continue
		}
		if item["output"] != "file contents" {
			t.Fatalf("expected single text content item to collapse to string output, got %#v in %s", item["output"], body)
		}
		return
	}
	t.Fatalf("missing function_call_output in %s", body)
}

func TestBuildOpenAICodexRequestBodyPreservesEncryptedToolContentItems(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "run tool"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_secret", Name: "secure_tool", Arguments: `{}`}}},
			{
				Role:       "tool",
				ToolCallID: "call_secret",
				ToolName:   "secure_tool",
				Text:       "fallback text",
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
			},
		},
		Tools: []ToolDefinition{{
			Name:        "secure_tool",
			Description: "Secure tool",
			InputSchema: map[string]any{
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
	for _, raw := range input {
		item := raw.(map[string]any)
		if item["type"] != "function_call_output" {
			continue
		}
		output := item["output"].([]any)
		if len(output) != 2 {
			t.Fatalf("expected two output content items, got %#v", output)
		}
		encrypted := output[1].(map[string]any)
		if encrypted["type"] != "encrypted_content" || encrypted["encrypted_content"] != "sealed-payload" {
			t.Fatalf("expected encrypted content item to survive request encoding, got %#v in %s", encrypted, body)
		}
		return
	}
	t.Fatalf("missing function_call_output in %s", body)
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

func TestBuildOpenAICodexRequestBodyPreservesAssistantGeneratedImagesLikeCodex(t *testing.T) {
	dir := t.TempDir()
	writeTestImage(t, dir, "shot.png")

	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model:      "gpt-5.5",
		WorkingDir: dir,
		Messages: []Message{
			{Role: "user", Text: "generate an image"},
			{
				Role: "assistant",
				Text: "Image generation completed",
				Images: []MessageImage{{
					Path:      "shot.png",
					MediaType: "image/png",
				}},
			},
			{Role: "user", Text: "use the prior image"},
		},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	input := payload["input"].([]any)
	var imageItem map[string]any
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if ok && item["type"] == "image_generation_call" {
			imageItem = item
			break
		}
	}
	if imageItem == nil {
		t.Fatalf("expected assistant image to be replayed as image_generation_call, got %s", body)
	}
	if imageItem["id"] != "shot" || imageItem["status"] != "completed" {
		t.Fatalf("unexpected generated image item metadata: %#v", imageItem)
	}
	if imageItem["result"] != base64.StdEncoding.EncodeToString(onePixelPNG) {
		t.Fatalf("expected generated image result to preserve image bytes, got %#v", imageItem["result"])
	}
}

func TestBuildOpenAICodexRequestBodyClearsAssistantGeneratedImageResultForTextOnlyModel(t *testing.T) {
	dir := t.TempDir()
	writeTestImage(t, dir, "shot.png")
	model := "text-only-generated-image-history-test"
	registerCodexModelImageInputSupport(model, false)
	t.Cleanup(func() {
		registerCodexModelImageInputSupport(model, true)
	})

	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model:      model,
		WorkingDir: dir,
		Messages: []Message{
			{
				Role: "assistant",
				Text: "Image generation completed",
				Images: []MessageImage{{
					Path:      "shot.png",
					MediaType: "image/png",
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	input := payload["input"].([]any)
	if len(input) != 2 {
		t.Fatalf("expected assistant text plus image_generation_call, got %#v in %s", input, body)
	}
	imageItem, ok := input[1].(map[string]any)
	if !ok || imageItem["type"] != "image_generation_call" {
		t.Fatalf("expected image_generation_call, got %#v in %s", input[1], body)
	}
	if imageItem["result"] != "" {
		t.Fatalf("text-only model should keep the image_generation_call but clear result, got %#v", imageItem)
	}
}

func TestBuildOpenAICodexRequestBodySerializesServiceTier(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model:       "gpt-5.5",
		ServiceTier: "priority",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if payload["service_tier"] != "priority" {
		t.Fatalf("expected priority service_tier, got %#v in body %s", payload["service_tier"], body)
	}
}

func TestBuildOpenAICodexRequestBodyMapsLegacyFastServiceTier(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model:       "gpt-5.5",
		ServiceTier: "fast",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if payload["service_tier"] != "priority" {
		t.Fatalf("expected priority service_tier, got %#v in body %s", payload["service_tier"], body)
	}
}

func TestBuildOpenAICodexRequestBodyOmitsDefaultServiceTier(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model:       "gpt-5.5",
		ServiceTier: "default",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, exists := payload["service_tier"]; exists {
		t.Fatalf("default service_tier should be omitted, got body %s", body)
	}
}

func TestBuildOpenAICodexRequestBodyOmitsUnsupportedServiceTier(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model:       "gpt-5.5",
		ServiceTier: "flex",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, exists := payload["service_tier"]; exists {
		t.Fatalf("unsupported service_tier should be omitted to match Codex catalog filtering, got body %s", body)
	}
}

func TestBuildOpenAICodexRequestBodyOmitsModelUnsupportedServiceTier(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model:       "gpt-5.2",
		ServiceTier: "priority",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, exists := payload["service_tier"]; exists {
		t.Fatalf("model-unsupported service_tier should be omitted to match Codex catalog filtering, got body %s", body)
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

func TestBuildOpenAICodexRequestBodyStripsImagesForTextOnlyModel(t *testing.T) {
	model := "text-only-codex-request-test"
	registerCodexModelImageInputSupport(model, false)
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model:      model,
		WorkingDir: t.TempDir(),
		Messages: []Message{
			{
				Role: "user",
				Text: "inspect",
				Images: []MessageImage{{
					Path:      "missing.png",
					MediaType: "image/png",
				}},
			},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_img", Name: "view_image", Arguments: `{"path":"missing.png"}`}}},
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
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}
	encoded := string(body)
	if strings.Contains(encoded, "input_image") || strings.Contains(encoded, "data:image/png;base64") {
		t.Fatalf("text-only Codex request must not include image payloads: %s", body)
	}
	if count := strings.Count(encoded, openAICodexImageContentOmittedPlaceholder); count != 2 {
		t.Fatalf("expected user and tool image placeholders, count=%d body=%s", count, body)
	}
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

func TestBuildOpenAICodexRequestBodyKeepsNonAdjacentToolOutput(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "inspect"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_late", Name: "read_file", Arguments: `{"path":"main.go"}`}}},
			{Role: "user", Text: "Runtime note before the tool result."},
			{Role: "tool", ToolCallID: "call_late", ToolName: "read_file", Text: "package main"},
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
	outputs := 0
	for _, item := range payload.Input {
		if item["type"] != "function_call_output" || item["call_id"] != "call_late" {
			continue
		}
		outputs++
		if item["output"] != "package main" {
			t.Fatalf("expected late real tool output to be preserved, got %#v in %s", item, body)
		}
	}
	if outputs != 1 {
		t.Fatalf("expected exactly one real late tool output, got %d in %s", outputs, body)
	}
	if strings.Contains(string(body), "aborted") {
		t.Fatalf("late matching tool output must not be replaced by synthetic aborted output: %s", body)
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

func TestBuildOpenAICodexRequestBodyPreservesFunctionCallNamespace(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "inspect"},
			{Role: "assistant", ToolCalls: []ToolCall{{
				ID:        "call_mcp",
				Name:      "mcp__filesystem__read_file",
				Namespace: "mcp__filesystem__",
				Arguments: `{"path":"main.go"}`,
			}}},
			{Role: "tool", ToolCallID: "call_mcp", ToolName: "mcp__filesystem__read_file", Text: "package main"},
		},
		Tools: []ToolDefinition{{
			Name:        "mcp__filesystem__read_file",
			Description: "Read a file",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("buildOpenAICodexRequestBody: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	input := payload["input"].([]any)
	call := input[1].(map[string]any)
	if call["type"] != "function_call" || call["call_id"] != "call_mcp" {
		t.Fatalf("unexpected function call item: %#v", call)
	}
	if call["namespace"] != "mcp__filesystem__" {
		t.Fatalf("expected namespace to be preserved, got %#v in %s", call, body)
	}
	if call["name"] != "read_file" {
		t.Fatalf("expected namespaced call to serialize short function name, got %#v in %s", call, body)
	}
}

func TestBuildOpenAICodexRequestBodySynthesizesMissingToolOutputWithNameFallbackCallID(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "inspect"},
			{Role: "assistant", ToolCalls: []ToolCall{{Name: "read_file", Arguments: `{"path":"main.go"}`}}},
			{Role: "user", Text: "continue"},
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
		`"call_id":"read_file"`,
		`"type":"function_call_output"`,
		`"output":"aborted"`,
	} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("expected %q in request body %s", want, encoded)
		}
	}
}

func TestBuildOpenAICodexRequestBodyMatchesToolOutputWithNameFallbackCallID(t *testing.T) {
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "inspect"},
			{Role: "assistant", ToolCalls: []ToolCall{{Name: "read_file", Arguments: `{"path":"main.go"}`}}},
			{Role: "tool", ToolName: "read_file", Text: "package main"},
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
		`"call_id":"read_file"`,
		`"type":"function_call_output"`,
		`"output":"package main"`,
	} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("expected %q in request body %s", want, encoded)
		}
	}
	if strings.Contains(encoded, `"output":"aborted"`) {
		t.Fatalf("did not expect synthesized aborted output when matching tool output exists: %s", encoded)
	}
}

func TestBuildOpenAICodexRequestBodySynthesizesMissingApplyPatchOutputAsCustom(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: main.go\n+package main\n*** End Patch\n"
	body, err := buildOpenAICodexRequestBody(ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{
			{Role: "user", Text: "edit"},
			{Role: "assistant", ToolCalls: []ToolCall{{
				ID:        "call_patch",
				Name:      "apply_patch",
				Arguments: mustJSON(map[string]any{"patch": patch}),
			}}},
			{Role: "user", Text: "Continue after the interrupted edit."},
		},
		Tools: []ToolDefinition{NewApplyPatchTool(Workspace{}).Definition()},
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
		t.Fatalf("expected four input items, got %#v in %s", payload["input"], body)
	}
	call := input[1].(map[string]any)
	if call["type"] != "custom_tool_call" || call["call_id"] != "call_patch" || call["name"] != "apply_patch" {
		t.Fatalf("expected apply_patch custom tool call, got %#v", call)
	}
	output := input[2].(map[string]any)
	if output["type"] != "custom_tool_call_output" || output["call_id"] != "call_patch" {
		t.Fatalf("expected synthesized apply_patch custom output, got %#v", output)
	}
	if _, exists := output["name"]; exists {
		t.Fatalf("synthesized apply_patch custom output should omit name to match Codex, got %#v", output)
	}
	if encoded := string(body); strings.Contains(encoded, `"type":"function_call_output"`) {
		t.Fatalf("apply_patch missing result must not be synthesized as function_call_output: %s", encoded)
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

func TestOpenAICodexClientAppliesConfiguredServiceTier(t *testing.T) {
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

	client := NewOpenAICodexClientWithReasoningEffortServiceTierAndWorkspaceIDs(server.URL, "", "fast", nil)
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
	if payload["service_tier"] != "priority" {
		t.Fatalf("expected configured priority service tier, got %#v", payload["service_tier"])
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
	accessToken := testCodexOAuthWorkspaceJWT(time.Now().Add(time.Hour), "account-123")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("client_version"); got != "1.0.0" {
			t.Fatalf("unexpected client_version: %q", got)
		}
		if got := r.Header.Get("authorization"); got != "Bearer "+accessToken {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if got := r.Header.Get("chatgpt-account-id"); got != "account-123" {
			t.Fatalf("unexpected chatgpt-account-id header: %q", got)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5","supported_in_api":true,"visibility":"list","supports_image_detail_original":true},{"slug":"hidden","display_name":"Hidden","supported_in_api":true,"visibility":"hidden"}]}`))
	}))
	defer server.Close()

	models, err := FetchOpenAICodexModels(context.Background(), server.URL, staticCodexTokenSource{token: accessToken}, server.Client())
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

func TestFetchOpenAICodexModelsRefreshesAfterUnauthorized(t *testing.T) {
	tokenSource := &refreshingCodexTokenSource{
		token:          "old-token",
		refreshedToken: "new-token",
	}
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		requestCount++
		switch requestCount {
		case 1:
			if got := r.Header.Get("authorization"); got != "Bearer old-token" {
				t.Fatalf("unexpected first authorization header: %q", got)
			}
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
		case 2:
			if got := r.Header.Get("authorization"); got != "Bearer new-token" {
				t.Fatalf("unexpected retry authorization header: %q", got)
			}
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5","supported_in_api":true,"visibility":"list"}]}`))
		default:
			t.Fatalf("unexpected request count: %d", requestCount)
		}
	}))
	defer server.Close()

	models, err := FetchOpenAICodexModels(context.Background(), server.URL, tokenSource, server.Client())
	if err != nil {
		t.Fatalf("FetchOpenAICodexModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gpt-5.5" {
		t.Fatalf("unexpected models: %#v", models)
	}
	if requestCount != 2 {
		t.Fatalf("expected one retry after 401, got %d requests", requestCount)
	}
	if tokenSource.accessCalls != 1 || tokenSource.refreshCalls != 1 {
		t.Fatalf("expected one access and one refresh, got access=%d refresh=%d", tokenSource.accessCalls, tokenSource.refreshCalls)
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
		w.Header().Set("OpenAI-Model", "gpt-5.2")
		w.Header().Set("X-Models-Etag", "etag-123")
		w.Header().Set("x-reasoning-included", "true")
		w.Header().Set("X-Codex-Primary-Used-Percent", "12.5")
		w.Header().Set("X-Codex-Primary-Window-Minutes", "10")
		w.Header().Set("X-Codex-Primary-Reset-At", "1704069000")
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
	if resp.ServerModel != "gpt-5.2" || resp.ModelsETag != "etag-123" || !resp.ReasoningIncluded {
		t.Fatalf("expected Codex response metadata to be captured, got %#v", resp)
	}
	if resp.RateLimitSummary != "primary=12.5% window=10m reset_at=1704069000" {
		t.Fatalf("expected Codex rate limit summary to be captured, got %q", resp.RateLimitSummary)
	}
}

func TestOpenAICodexClientCompleteRefreshesAfterUnauthorized(t *testing.T) {
	tokenSource := &refreshingCodexTokenSource{
		token:          "old-token",
		refreshedToken: "new-token",
	}
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		requestCount++
		switch requestCount {
		case 1:
			if got := r.Header.Get("authorization"); got != "Bearer old-token" {
				t.Fatalf("unexpected first authorization header: %q", got)
			}
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
		case 2:
			if got := r.Header.Get("authorization"); got != "Bearer new-token" {
				t.Fatalf("unexpected retry authorization header: %q", got)
			}
			w.Header().Set("content-type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ready\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
		default:
			t.Fatalf("unexpected request count: %d", requestCount)
		}
	}))
	defer server.Close()

	client := NewOpenAICodexClient(server.URL)
	client.tokenSource = tokenSource
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "gpt-5.5",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text != "ready" {
		t.Fatalf("expected retry response text, got %q", resp.Message.Text)
	}
	if requestCount != 2 {
		t.Fatalf("expected one retry after 401, got %d requests", requestCount)
	}
	if tokenSource.accessCalls != 1 || tokenSource.refreshCalls != 1 {
		t.Fatalf("expected one access and one refresh, got access=%d refresh=%d", tokenSource.accessCalls, tokenSource.refreshCalls)
	}
}

func TestOpenAICodexClientCompletePreservesRateLimitReachedType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set(providerRateLimitReachedTypeHeader, "workspace_member_usage_limit_reached")
		w.Header().Set("X-Codex-Primary-Used-Percent", "100.0")
		w.Header().Set("X-Codex-Primary-Window-Minutes", "15")
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"usage_limit_reached","type":"usage_limit_exceeded","code":"usage_limit_reached"}}`))
	}))
	defer server.Close()

	client := NewOpenAICodexClient(server.URL)
	client.tokenSource = staticCodexTokenSource{token: "test-token"}
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "gpt-5.5",
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
	if providerErr.RateLimitReachedType != "workspace_member_usage_limit_reached" {
		t.Fatalf("expected rate limit reached type, got %q", providerErr.RateLimitReachedType)
	}
	if providerErr.RateLimitSummary != "primary=100.0% window=15m" {
		t.Fatalf("expected rate limit summary, got %q", providerErr.RateLimitSummary)
	}
	if !strings.Contains(err.Error(), "Ask an owner to increase your spend cap to continue.") {
		t.Fatalf("expected workspace usage-limit copy, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "rate_limits=primary=100.0% window=15m") {
		t.Fatalf("expected rate limit details in error, got %q", err.Error())
	}
}

func TestOpenAICodexClientGenerateImagePostsTypedRequest(t *testing.T) {
	accessToken := testCodexOAuthWorkspaceJWT(time.Now().Add(time.Hour), "account-123")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Method; got != http.MethodPost {
			t.Fatalf("unexpected method: %s", got)
		}
		if got := r.Header.Get("authorization"); got != "Bearer "+accessToken {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if got := r.Header.Get("chatgpt-account-id"); got != "account-123" {
			t.Fatalf("unexpected chatgpt-account-id header: %q", got)
		}
		if got := r.Header.Get("x-extra"); got != "present" {
			t.Fatalf("missing extra header, got %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode body: %v", err)
		}
		want := map[string]any{
			"prompt":     "a red fox in a field",
			"background": "opaque",
			"model":      "gpt-image-1.5",
			"quality":    "medium",
			"size":       "1024x1536",
		}
		if !mapsEqualForTest(body, want) {
			t.Fatalf("unexpected request body: %#v", body)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(openAICodexImageResponseFixture())
	}))
	defer server.Close()

	client := NewOpenAICodexClient(server.URL)
	client.tokenSource = staticCodexTokenSource{token: accessToken}
	headers := http.Header{"x-extra": []string{"present"}}
	resp, err := client.GenerateImage(context.Background(), OpenAICodexImageGenerationRequest{
		Prompt:     "a red fox in a field",
		Background: OpenAICodexImageBackgroundOpaque,
		Model:      "gpt-image-1.5",
		Quality:    OpenAICodexImageQualityMedium,
		Size:       "1024x1536",
	}, headers)
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	assertOpenAICodexImageResponseFixture(t, resp)
}

func TestOpenAICodexClientGenerateImageRefreshesAfterUnauthorized(t *testing.T) {
	tokenSource := &refreshingCodexTokenSource{
		token:          "old-token",
		refreshedToken: "new-token",
	}
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		requestCount++
		switch requestCount {
		case 1:
			if got := r.Header.Get("authorization"); got != "Bearer old-token" {
				t.Fatalf("unexpected first authorization header: %q", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode first body: %v", err)
			}
			if body["prompt"] != "a red fox in a field" {
				t.Fatalf("unexpected first request body: %#v", body)
			}
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
		case 2:
			if got := r.Header.Get("authorization"); got != "Bearer new-token" {
				t.Fatalf("unexpected retry authorization header: %q", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode retry body: %v", err)
			}
			if body["prompt"] != "a red fox in a field" {
				t.Fatalf("unexpected retry request body: %#v", body)
			}
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write(openAICodexImageResponseFixture())
		default:
			t.Fatalf("unexpected request count: %d", requestCount)
		}
	}))
	defer server.Close()

	client := NewOpenAICodexClient(server.URL)
	client.tokenSource = tokenSource
	resp, err := client.GenerateImage(context.Background(), OpenAICodexImageGenerationRequest{
		Prompt: "a red fox in a field",
		Model:  "gpt-image-1.5",
	}, nil)
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	assertOpenAICodexImageResponseFixture(t, resp)
	if requestCount != 2 {
		t.Fatalf("expected one retry after 401, got %d requests", requestCount)
	}
	if tokenSource.accessCalls != 1 || tokenSource.refreshCalls != 1 {
		t.Fatalf("expected one access and one refresh, got access=%d refresh=%d", tokenSource.accessCalls, tokenSource.refreshCalls)
	}
}

func TestOpenAICodexClientGenerateImageRetriesServerError(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		requestCount++
		if requestCount == 1 {
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"temporary outage"}}`))
			return
		}
		if requestCount != 2 {
			t.Fatalf("unexpected request count: %d", requestCount)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode retry body: %v", err)
		}
		if body["prompt"] != "a red fox in a field" {
			t.Fatalf("unexpected retry request body: %#v", body)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(openAICodexImageResponseFixture())
	}))
	defer server.Close()

	client := NewOpenAICodexClient(server.URL)
	client.tokenSource = staticCodexTokenSource{token: "test-token"}
	resp, err := client.GenerateImage(context.Background(), OpenAICodexImageGenerationRequest{
		Prompt: "a red fox in a field",
		Model:  "gpt-image-1.5",
	}, nil)
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	assertOpenAICodexImageResponseFixture(t, resp)
	if requestCount != 2 {
		t.Fatalf("expected one retry after 503, got %d requests", requestCount)
	}
}

func TestOpenAICodexClientEditImagePostsTypedRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/edits" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode body: %v", err)
		}
		images, ok := body["images"].([]any)
		if !ok || len(images) != 1 {
			t.Fatalf("expected one image URL, got %#v", body["images"])
		}
		image, ok := images[0].(map[string]any)
		if !ok || image["image_url"] != "data:image/png;base64,Zm9v" {
			t.Fatalf("unexpected image URL payload: %#v", images[0])
		}
		if body["prompt"] != "add a red hat" || body["model"] != "gpt-image-1.5" {
			t.Fatalf("unexpected edit request body: %#v", body)
		}
		if _, ok := body["quality"]; ok {
			t.Fatalf("quality should be omitted when unset: %#v", body)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(openAICodexImageResponseFixture())
	}))
	defer server.Close()

	client := NewOpenAICodexClient(server.URL)
	client.tokenSource = staticCodexTokenSource{token: "test-token"}
	resp, err := client.EditImage(context.Background(), OpenAICodexImageEditRequest{
		Images: []OpenAICodexImageURL{{
			ImageURL: "data:image/png;base64,Zm9v",
		}},
		Prompt: "add a red hat",
		Model:  "gpt-image-1.5",
	}, nil)
	if err != nil {
		t.Fatalf("EditImage: %v", err)
	}
	assertOpenAICodexImageResponseFixture(t, resp)
}

func TestOpenAICodexClientGenerateImageRequiresDataField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"created":1778832973}`))
	}))
	defer server.Close()

	client := NewOpenAICodexClient(server.URL)
	client.tokenSource = staticCodexTokenSource{token: "test-token"}
	_, err := client.GenerateImage(context.Background(), OpenAICodexImageGenerationRequest{
		Prompt: "a red fox in a field",
		Model:  "gpt-image-1.5",
	}, nil)
	if err == nil {
		t.Fatalf("expected missing data error")
	}
	if !strings.Contains(err.Error(), "failed to decode image generation response: missing field `data`") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAICodexClientGenerateImageRejectsInvalidTypedEnum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("request should not be sent for invalid typed enum")
	}))
	defer server.Close()

	client := NewOpenAICodexClient(server.URL)
	client.tokenSource = staticCodexTokenSource{token: "test-token"}
	_, err := client.GenerateImage(context.Background(), OpenAICodexImageGenerationRequest{
		Prompt:  "a red fox in a field",
		Model:   "gpt-image-1.5",
		Quality: OpenAICodexImageQuality("ultra"),
	}, nil)
	if err == nil {
		t.Fatalf("expected invalid quality error")
	}
	if !strings.Contains(err.Error(), `failed to encode image generation request: invalid quality "ultra"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAICodexClientGenerateImagePreservesRateLimitReachedType(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		requestCount++
		w.Header().Set(providerRateLimitReachedTypeHeader, "workspace_owner_credits_depleted")
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota","type":"usage_limit_exceeded","code":"usage_limit_reached"}}`))
	}))
	defer server.Close()

	client := NewOpenAICodexClient(server.URL)
	client.tokenSource = staticCodexTokenSource{token: "test-token"}
	_, err := client.GenerateImage(context.Background(), OpenAICodexImageGenerationRequest{
		Prompt: "a red fox in a field",
		Model:  "gpt-image-1.5",
	}, nil)
	if err == nil {
		t.Fatalf("expected rate-limit error")
	}
	var providerErr *ProviderAPIError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderAPIError, got %T", err)
	}
	if providerErr.RateLimitReachedType != "workspace_owner_credits_depleted" {
		t.Fatalf("expected rate limit reached type, got %q", providerErr.RateLimitReachedType)
	}
	if !strings.Contains(err.Error(), "Your workspace is out of credits. Add credits to continue.") {
		t.Fatalf("expected workspace credit copy, got %q", err.Error())
	}
	if requestCount != 1 {
		t.Fatalf("429 should not be retried, got %d requests", requestCount)
	}
}

func openAICodexImageResponseFixture() []byte {
	return []byte(`{"created":1778832973,"background":"opaque","data":[{"b64_json":"REDACT"}],"output_format":"png","quality":"medium","size":"1024x1536","usage":{"input_tokens":1}}`)
}

func assertOpenAICodexImageResponseFixture(t *testing.T, resp OpenAICodexImageResponse) {
	t.Helper()
	if resp.Created != 1778832973 {
		t.Fatalf("unexpected created value: %d", resp.Created)
	}
	if resp.Background != OpenAICodexImageBackgroundOpaque {
		t.Fatalf("unexpected background: %q", resp.Background)
	}
	if resp.Quality != OpenAICodexImageQualityMedium {
		t.Fatalf("unexpected quality: %q", resp.Quality)
	}
	if resp.Size != "1024x1536" {
		t.Fatalf("unexpected size: %q", resp.Size)
	}
	if len(resp.Data) != 1 || resp.Data[0].B64JSON != "REDACT" {
		t.Fatalf("unexpected image data: %#v", resp.Data)
	}
}

func mapsEqualForTest(got map[string]any, want map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			return false
		}
	}
	return true
}

func TestResolveOpenAICodexInstallationIDPersistsValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installation_id")
	first, err := resolveOpenAICodexInstallationIDAtPath(path)
	if err != nil {
		t.Fatalf("resolveOpenAICodexInstallationIDAtPath first: %v", err)
	}
	if strings.TrimSpace(first) == "" {
		t.Fatalf("expected generated installation id")
	}
	second, err := resolveOpenAICodexInstallationIDAtPath(path)
	if err != nil {
		t.Fatalf("resolveOpenAICodexInstallationIDAtPath second: %v", err)
	}
	if second != first {
		t.Fatalf("expected persisted installation id %q, got %q", first, second)
	}
}

func TestOpenAICodexClientReplaysTurnState(t *testing.T) {
	accessToken := testCodexOAuthWorkspaceJWT(time.Now().Add(time.Hour), "account-123")
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
			if got := r.Header.Get("chatgpt-account-id"); got != "account-123" {
				t.Fatalf("unexpected chatgpt-account-id: %q", got)
			}
			assertTurnMetadataHeader(t, r, "turn-abc")
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if payload["prompt_cache_key"] != "thread-456" {
				t.Fatalf("expected prompt_cache_key to use thread id, got %#v", payload["prompt_cache_key"])
			}
			metadata, ok := payload["client_metadata"].(map[string]any)
			if !ok || strings.TrimSpace(fmt.Sprint(metadata["x-codex-installation-id"])) == "" {
				t.Fatalf("expected x-codex-installation-id metadata, got %#v", payload["client_metadata"])
			}
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
	client.tokenSource = staticCodexTokenSource{token: accessToken}
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
		"id":"resp_parse_1",
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
	if resp.ResponseID != "resp_parse_1" {
		t.Fatalf("expected response id to be preserved, got %q", resp.ResponseID)
	}
}

func TestParseOpenAICodexResponsePreservesReasoningContent(t *testing.T) {
	resp, err := parseOpenAICodexResponse([]byte(`{
		"status":"completed",
		"output":[{
			"type":"reasoning",
			"id":"reasoning-1",
			"summary":[{"type":"summary_text","text":"Consider inputs"}],
			"content":[{"type":"reasoning_text","text":"Detailed trace"}]
		},{
			"type":"message",
			"role":"assistant",
			"content":[{"type":"output_text","text":"done"}]
		}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAICodexResponse: %v", err)
	}
	if resp.Message.Text != "done" {
		t.Fatalf("expected visible message text, got %q", resp.Message.Text)
	}
	if resp.Message.ReasoningContent != "Consider inputs\nDetailed trace" {
		t.Fatalf("expected reasoning content to be preserved, got %q", resp.Message.ReasoningContent)
	}
}

func TestParseOpenAICodexResponseCapturesServerModel(t *testing.T) {
	resp, err := parseOpenAICodexResponse([]byte(`{
		"status":"completed",
		"headers":{"OpenAI-Model":"gpt-5.2"},
		"output":[{
			"type":"message",
			"role":"assistant",
			"content":[{"type":"output_text","text":"done"}]
		}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAICodexResponse: %v", err)
	}
	if resp.ServerModel != "gpt-5.2" {
		t.Fatalf("expected server model to be captured, got %q", resp.ServerModel)
	}
}

func TestParseOpenAICodexResponseIgnoresModelFieldForServerModel(t *testing.T) {
	resp, err := parseOpenAICodexResponse([]byte(`{
		"status":"completed",
		"model":"gpt-5.2",
		"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAICodexResponse: %v", err)
	}
	if resp.ServerModel != "" {
		t.Fatalf("expected server model to come only from headers, got %q", resp.ServerModel)
	}
}

func TestParseOpenAICodexResponseCapturesModelVerifications(t *testing.T) {
	resp, err := parseOpenAICodexResponse([]byte(`{
		"status":"completed",
		"metadata":{"openai_verification_recommendation":["trusted_access_for_cyber","unknown","trusted_access_for_cyber"]},
		"output":[{
			"type":"message",
			"role":"assistant",
			"content":[{"type":"output_text","text":"done"}]
		}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAICodexResponse: %v", err)
	}
	if len(resp.ModelVerifications) != 1 || resp.ModelVerifications[0] != openAICodexTrustedAccessForCyber {
		t.Fatalf("expected trusted access verification to be captured once, got %#v", resp.ModelVerifications)
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

func TestParseOpenAICodexResponsePreservesFunctionCallNamespace(t *testing.T) {
	resp, err := parseOpenAICodexResponse([]byte(`{
		"status":"completed",
		"output":[{
			"type":"function_call",
			"call_id":"call_mcp",
			"namespace":"mcp__filesystem__",
			"name":"read_file",
			"arguments":"{\"path\":\"main.go\"}"
		}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAICodexResponse: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one namespaced function call, got %#v", resp.Message.ToolCalls)
	}
	call := resp.Message.ToolCalls[0]
	if call.ID != "call_mcp" || call.Name != "mcp__filesystem__read_file" || call.Namespace != "mcp__filesystem__" {
		t.Fatalf("expected namespace and name to be flattened like Codex, got %#v", call)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		t.Fatalf("function arguments are not JSON: %v", err)
	}
	if args["path"] != "main.go" {
		t.Fatalf("unexpected namespaced function arguments: %#v", args)
	}
}

func TestParseOpenAICodexResponseMirrorsCodexNamespacedDisplayName(t *testing.T) {
	resp, err := parseOpenAICodexResponse([]byte(`{
		"status":"completed",
		"output":[{
			"type":"function_call",
			"call_id":"call_mcp",
			"namespace":"mcp__filesystem__",
			"name":"mcp__filesystem__read_file",
			"arguments":"{\"path\":\"main.go\"}"
		}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAICodexResponse: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one namespaced function call, got %#v", resp.Message.ToolCalls)
	}
	call := resp.Message.ToolCalls[0]
	if call.Name != "mcp__filesystem__mcp__filesystem__read_file" {
		t.Fatalf("expected Codex ToolName display semantics, got %#v", call)
	}
}

func TestParseOpenAICodexResponseAcceptsHostedOutputItems(t *testing.T) {
	resp, err := parseOpenAICodexResponse([]byte(`{
		"status":"completed",
		"output":[{
			"type":"image_generation_call",
			"id":"ig_123",
			"status":"completed",
			"revised_prompt":"A clean diagram",
			"result":"Zm9v"
		},{
			"type":"web_search_call",
			"id":"ws_123",
			"status":"completed",
			"action":{"type":"search","query":"codex hosted tools"}
		}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAICodexResponse: %v", err)
	}
	if !strings.Contains(resp.Message.Text, "Image generation completed: ig_123") {
		t.Fatalf("expected image generation hosted output text, got %q", resp.Message.Text)
	}
	if strings.Contains(resp.Message.Text, "Zm9v") {
		t.Fatalf("hosted image text must not expose raw base64 payload: %q", resp.Message.Text)
	}
	if !strings.Contains(resp.Message.Text, "Web search completed: codex hosted tools (ws_123)") {
		t.Fatalf("expected web search hosted output text, got %q", resp.Message.Text)
	}
}

func TestParseOpenAICodexResponseSummarizesHostedWebSearchActionVariants(t *testing.T) {
	resp, err := parseOpenAICodexResponse([]byte(`{
		"status":"completed",
		"output":[{
			"type":"web_search_call",
			"id":"ws_queries",
			"status":"completed",
			"action":{"type":"search","queries":["codex hosted tools","responses web search"]}
		},{
			"type":"web_search_call",
			"id":"ws_open",
			"status":"completed",
			"action":{"type":"open_page","url":"https://example.test/page"}
		},{
			"type":"web_search_call",
			"id":"ws_find",
			"status":"completed",
			"action":{"type":"find_in_page","url":"https://example.test/page","pattern":"needle"}
		}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAICodexResponse: %v", err)
	}
	for _, want := range []string{
		"Web search completed: codex hosted tools, responses web search (ws_queries)",
		"Web search completed: https://example.test/page (ws_open)",
		`Web search completed: https://example.test/page find "needle" (ws_find)`,
	} {
		if !strings.Contains(resp.Message.Text, want) {
			t.Fatalf("expected hosted web search summary %q, got %q", want, resp.Message.Text)
		}
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

func TestReadOpenAICodexStreamSavesImageGenerationOutput(t *testing.T) {
	imageData := []byte("png-bytes")
	encoded := base64.StdEncoding.EncodeToString(imageData)
	stream := strings.NewReader(strings.Join([]string{
		fmt.Sprintf(`data: {"type":"response.output_item.done","item":{"type":"image_generation_call","id":"ig/save:1","status":"completed","revised_prompt":"A saved image","result":%q}}`, encoded),
		`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"image_generation_call","id":"ig/save:1","status":"completed","revised_prompt":"A saved image","result":"` + encoded + `"}]}}`,
		"",
	}, "\n\n"))
	root := t.TempDir()
	resp, err := readOpenAICodexStreamWithOptions(context.Background(), stream, openAICodexStreamOptions{
		SessionID:       "session/1",
		ImageOutputRoot: root,
	})
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	wantPath := openAICodexImageGenerationArtifactPath(root, "session/1", "ig/save:1")
	if len(resp.Message.Images) != 1 || resp.Message.Images[0].Path != wantPath || resp.Message.Images[0].MediaType != "image/png" {
		t.Fatalf("expected saved image metadata %q, got %#v", wantPath, resp.Message.Images)
	}
	if got, err := os.ReadFile(wantPath); err != nil || string(got) != string(imageData) {
		t.Fatalf("expected saved image bytes %q at %s, got %q err=%v", string(imageData), wantPath, string(got), err)
	}
	if !strings.Contains(resp.Message.Text, "Saved image: "+wantPath) {
		t.Fatalf("expected saved image path in response text, got %q", resp.Message.Text)
	}
	if strings.Contains(resp.Message.Text, encoded) {
		t.Fatalf("stream response text must not expose raw base64 payload: %q", resp.Message.Text)
	}
}

func TestReadOpenAICodexStreamUsesAddedMessageTextAsPrefix(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Intro "}]}}`,
		`data: {"type":"response.output_text.delta","delta":"body"}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.Text != "Intro body" {
		t.Fatalf("expected added message prefix plus delta, got %q", resp.Message.Text)
	}
}

func TestReadOpenAICodexStreamAcceptsLargeSSEDataLine(t *testing.T) {
	large := strings.Repeat("x", 1024*1024+4096)
	event, err := json.Marshal(map[string]any{
		"type":  "response.output_text.delta",
		"delta": large,
	})
	if err != nil {
		t.Fatalf("Marshal event: %v", err)
	}
	stream := strings.NewReader("data: " + string(event) + "\n\n" + `data: {"type":"response.completed"}` + "\n\n")
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.Text != large {
		t.Fatalf("expected large streamed text length %d, got %d", len(large), len(resp.Message.Text))
	}
}

func TestReadOpenAICodexStreamParsesMultilineSSEDataEvent(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta",`,
		`data: "delta":"multi line"}`,
		"",
		`data: {"type":"response.completed"}`,
		"",
	}, "\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.Text != "multi line" {
		t.Fatalf("expected multiline SSE event text, got %q", resp.Message.Text)
	}
}

func TestReadOpenAICodexStreamDoesNotDuplicateAddedMessageTextOnDone(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Intro "}]}}`,
		`data: {"type":"response.output_text.delta","delta":"body"}`,
		`data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Intro body"}]}}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.Text != "Intro body" {
		t.Fatalf("expected added message prefix to stay single, got %q", resp.Message.Text)
	}
}

func TestReadOpenAICodexStreamCapturesCreatedResponseModelHeader(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp-1","headers":{"OpenAI-Model":"gpt-5.2"}}}`,
		`data: {"type":"response.output_text.delta","delta":"done"}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.ServerModel != "gpt-5.2" {
		t.Fatalf("expected response.created server model to be captured, got %q", resp.ServerModel)
	}
}

func TestReadOpenAICodexStreamCapturesTopLevelModelHeader(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.created","headers":{"x-openai-model":["gpt-5.4"]},"response":{"id":"resp-1"}}`,
		`data: {"type":"response.output_text.delta","delta":"done"}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.ServerModel != "gpt-5.4" {
		t.Fatalf("expected top-level model header to be captured, got %q", resp.ServerModel)
	}
}

func TestReadOpenAICodexStreamPrefersResponseModelHeader(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.created","headers":{"OpenAI-Model":"outer-model"},"response":{"id":"resp-1","headers":{"OpenAI-Model":"inner-model"}}}`,
		`data: {"type":"response.output_text.delta","delta":"done"}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.ServerModel != "inner-model" {
		t.Fatalf("expected response header model to win, got %q", resp.ServerModel)
	}
}

func TestReadOpenAICodexStreamCapturesRateLimitEvent(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":12.5,"window_minutes":10,"reset_at":1704069000},"secondary":{"used_percent":40,"window_minutes":60}},"credits":{"has_credits":true,"unlimited":false,"balance":"42"}}`,
		`data: {"type":"response.output_text.delta","delta":"done"}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	want := "primary=12.5% window=10m reset_at=1704069000, secondary=40% window=60m, credits(has_credits=true unlimited=false balance=42)"
	if resp.RateLimitSummary != want {
		t.Fatalf("expected rate limit event summary %q, got %q", want, resp.RateLimitSummary)
	}
}

func TestReadOpenAICodexStreamCapturesModelVerificationEvent(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.metadata","metadata":{"openai_verification_recommendation":["trusted_access_for_cyber","ignored"]}}`,
		`data: {"type":"response.output_text.delta","delta":"done"}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if len(resp.ModelVerifications) != 1 || resp.ModelVerifications[0] != openAICodexTrustedAccessForCyber {
		t.Fatalf("expected trusted access verification to be captured once, got %#v", resp.ModelVerifications)
	}
}

func TestReadOpenAICodexStreamAccumulatesReasoningDeltas(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"type":"reasoning","id":"reasoning-1","summary":[{"type":"summary_text","text":""}]}}`,
		`data: {"type":"response.reasoning_summary_text.delta","delta":"step one"}`,
		`data: {"type":"response.reasoning_text.delta","delta":" raw detail"}`,
		`data: {"type":"response.output_item.added","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.Text != "done" {
		t.Fatalf("expected visible message text, got %q", resp.Message.Text)
	}
	if resp.Message.ReasoningContent != "step one raw detail" {
		t.Fatalf("expected reasoning deltas to be preserved, got %q", resp.Message.ReasoningContent)
	}
}

func TestReadOpenAICodexStreamPreservesReasoningSummarySectionBreaks(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"type":"reasoning","id":"reasoning-1","summary":[{"type":"summary_text","text":""}]}}`,
		`data: {"type":"response.reasoning_summary_text.delta","delta":"first section"}`,
		`data: {"type":"response.reasoning_summary_part.added","summary_index":1}`,
		`data: {"type":"response.reasoning_summary_text.delta","delta":"second section"}`,
		`data: {"type":"response.output_item.added","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.ReasoningContent != "first section\nsecond section" {
		t.Fatalf("expected reasoning section break to be preserved, got %q", resp.Message.ReasoningContent)
	}
}

func TestReadOpenAICodexStreamUsesDoneReasoningWhenNoDeltas(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.done","item":{"type":"reasoning","id":"reasoning-1","summary":[{"type":"summary_text","text":"Consider inputs"}],"content":[{"type":"reasoning_text","text":"Detailed trace"}]}}`,
		`data: {"type":"response.output_item.added","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.ReasoningContent != "Consider inputs\nDetailed trace" {
		t.Fatalf("expected done reasoning content, got %q", resp.Message.ReasoningContent)
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

func TestReadOpenAICodexStreamReturnsIncompleteReason(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}}`,
		`data: {"type":"response.output_text.delta","delta":" content"}`,
		`data: {"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"content_filter"}}}`,
		"",
	}, "\n\n"))
	_, err := readOpenAICodexStream(context.Background(), stream)
	if err == nil {
		t.Fatalf("expected incomplete stream error")
	}
	text := err.Error()
	if !strings.Contains(text, "Incomplete response returned, reason: content_filter") {
		t.Fatalf("expected incomplete reason in error, got %v", err)
	}
	if strings.Contains(text, "stream closed before response.completed") {
		t.Fatalf("incomplete stream error should not be reported as missing completed event: %v", err)
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

func TestReadOpenAICodexStreamKeepsFinalPhaseAfterLaterCommentaryItem(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","phase":"final_answer"}}`,
		`data: {"type":"response.output_text.delta","delta":"done"}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"ignored late commentary"}]}}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.Text != "done" {
		t.Fatalf("expected streamed text, got %q", resp.Message.Text)
	}
	if resp.Message.Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected final-answer phase to win over later commentary, got %q", resp.Message.Phase)
	}
}

func TestReadOpenAICodexStreamPrefersFinalAnswerItemOverCommentaryDeltas(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","phase":"commentary"}}`,
		`data: {"type":"response.output_text.delta","delta":"checking report"}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"report complete"}]}}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.Text != "report complete" {
		t.Fatalf("expected final-answer item text, got %q", resp.Message.Text)
	}
	if resp.Message.Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected final-answer phase, got %q", resp.Message.Phase)
	}
}

func TestReadOpenAICodexStreamDoesNotLetCompletedSnapshotOverrideFinalItem(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","phase":"commentary"}}`,
		`data: {"type":"response.output_text.delta","delta":"checking report"}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"report complete"}]}}`,
		`data: {"type":"response.completed","response":{"status":"completed","end_turn":true,"output":[{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"stale commentary snapshot"}]}]}}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.Text != "report complete" {
		t.Fatalf("expected streamed final-answer item to win, got %q", resp.Message.Text)
	}
	if resp.Message.Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected final-answer phase, got %q", resp.Message.Phase)
	}
	if resp.EndTurn == nil || !*resp.EndTurn {
		t.Fatalf("expected completed response end_turn=true to be preserved, got %#v", resp.EndTurn)
	}
}

func TestReadOpenAICodexStreamUsesFinalMessageItemWhenCompletedResponseHasNoOutput(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"still working"}]}}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"done"}]}}`,
		`data: {"type":"response.completed","response":{"id":"resp_stream_1","status":"completed","end_turn":true}}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.Text != "done" {
		t.Fatalf("expected final message item text, got %q", resp.Message.Text)
	}
	if resp.Message.Phase != messagePhaseFinalAnswer {
		t.Fatalf("expected final-answer phase, got %q", resp.Message.Phase)
	}
	if resp.EndTurn == nil || !*resp.EndTurn {
		t.Fatalf("expected nested stream end_turn=true to survive fallback, got %#v", resp.EndTurn)
	}
	if resp.ResponseID != "resp_stream_1" {
		t.Fatalf("expected stream response id to be preserved, got %q", resp.ResponseID)
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

func TestReadOpenAICodexStreamPreservesEndTurnFromCompletedResponseWithoutOutput(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"continuing"}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","end_turn":false}}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if resp.Message.Text != "continuing" {
		t.Fatalf("expected streamed text fallback, got %q", resp.Message.Text)
	}
	if resp.EndTurn == nil || *resp.EndTurn {
		t.Fatalf("expected nested stream end_turn=false to survive fallback, got %#v", resp.EndTurn)
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

func TestReadOpenAICodexStreamRoutesToolArgumentsByItemIDWithoutOutputIndex(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"id":"fc_one","type":"function_call","call_id":"call_one","name":"read_file"}}`,
		`data: {"type":"response.output_item.added","item":{"id":"fc_two","type":"function_call","call_id":"call_two","name":"grep"}}`,
		`data: {"type":"response.function_call_arguments.done","item_id":"fc_one","arguments":"{\"path\":\"a.txt\"}"}`,
		`data: {"type":"response.function_call_arguments.done","item_id":"fc_two","arguments":"{\"pattern\":\"needle\"}"}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n\n"))
	resp, err := readOpenAICodexStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("readOpenAICodexStream: %v", err)
	}
	if len(resp.Message.ToolCalls) != 2 {
		t.Fatalf("expected two tool calls, got %#v", resp.Message.ToolCalls)
	}
	first := resp.Message.ToolCalls[0]
	if first.ID != "call_one" || first.Name != "read_file" || first.Arguments != `{"path":"a.txt"}` {
		t.Fatalf("unexpected first tool call: %#v", first)
	}
	second := resp.Message.ToolCalls[1]
	if second.ID != "call_two" || second.Name != "grep" || second.Arguments != `{"pattern":"needle"}` {
		t.Fatalf("unexpected second tool call: %#v", second)
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

func TestReadOpenAICodexStreamPreservesFunctionCallNamespace(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_mcp","namespace":"mcp__filesystem__","name":"read_file"}}`,
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
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one namespaced function call, got %#v", resp.Message.ToolCalls)
	}
	call := resp.Message.ToolCalls[0]
	if call.ID != "call_mcp" || call.Name != "mcp__filesystem__read_file" || call.Namespace != "mcp__filesystem__" {
		t.Fatalf("expected namespace and name to be flattened like Codex, got %#v", call)
	}
	if !progressEventsContain(events, progressKindModelStreamToolCall, "mcp__filesystem__read_file") ||
		!progressEventsContain(events, progressKindModelStreamToolReady, "mcp__filesystem__read_file") {
		t.Fatalf("expected namespaced tool progress events, got %#v", events)
	}
}

func TestReadOpenAICodexStreamAccumulatesCustomToolCallInputDeltas(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: streamed.txt\n+hello\n+world\n*** End Patch"
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"type":"custom_tool_call","call_id":"call_patch_stream","name":"apply_patch","input":""}}`,
		`data: {"type":"response.custom_tool_call_input.delta","call_id":"call_patch_stream","delta":"*** Begin Patch\n"}`,
		`data: {"type":"response.custom_tool_call_input.delta","call_id":"call_patch_stream","delta":"*** Add File: streamed.txt\n+hello"}`,
		`data: {"type":"response.custom_tool_call_input.delta","call_id":"call_patch_stream","delta":"\n+world\n*** End Patch"}`,
		`data: {"type":"response.output_item.done","item":{"type":"custom_tool_call","call_id":"call_patch_stream","name":"apply_patch","input":""}}`,
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
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one custom tool call, got %#v", resp.Message.ToolCalls)
	}
	call := resp.Message.ToolCalls[0]
	if call.ID != "call_patch_stream" || call.Name != "apply_patch" {
		t.Fatalf("unexpected custom tool call: %#v", call)
	}
	var patchArgs map[string]string
	if err := json.Unmarshal([]byte(call.Arguments), &patchArgs); err != nil {
		t.Fatalf("custom tool arguments are not JSON: %v", err)
	}
	if patchArgs["patch"] != patch {
		t.Fatalf("expected accumulated custom input patch, got %#v", patchArgs)
	}
	if !progressEventsContain(events, progressKindModelStreamToolArgs, "apply_patch") ||
		!progressEventsContain(events, progressKindModelStreamToolReady, "apply_patch") {
		t.Fatalf("expected custom tool arg and ready progress events, got %#v", events)
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

func TestCodexOAuthTokenSourceRefreshAfterUnauthorizedForcesRefresh(t *testing.T) {
	t.Setenv(openAICodexAccessTokenEnv, "")
	path := filepath.Join(t.TempDir(), "codex_auth.json")
	newAccessToken := testCodexOAuthWorkspaceJWT(time.Now().Add(time.Hour), "workspace-1")
	if err := saveCodexOAuthAuthFile(path, codexOAuthTokens{
		AccessToken:  testCodexOAuthWorkspaceJWT(time.Now().Add(time.Hour), "workspace-1"),
		RefreshToken: "refresh-old",
	}); err != nil {
		t.Fatalf("save auth file: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.FormValue("grant_type"); got != "refresh_token" {
			t.Fatalf("unexpected grant_type: %q", got)
		}
		if got := r.FormValue("refresh_token"); got != "refresh-old" {
			t.Fatalf("unexpected refresh_token: %q", got)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"access_token":%q}`, newAccessToken)))
	}))
	defer server.Close()
	oldEndpoint := openAICodexOAuthTokenEndpoint
	openAICodexOAuthTokenEndpoint = server.URL
	t.Cleanup(func() {
		openAICodexOAuthTokenEndpoint = oldEndpoint
	})

	source := NewCodexOAuthTokenSourceWithWorkspaceIDs(path, server.Client(), []string{"workspace-1"})
	token, err := source.RefreshAfterUnauthorized(context.Background())
	if err != nil {
		t.Fatalf("RefreshAfterUnauthorized: %v", err)
	}
	if token != newAccessToken {
		t.Fatalf("expected refreshed access token, got %q", token)
	}
	_, auth, err := readCodexOAuthAuthFile(path)
	if err != nil {
		t.Fatalf("read updated auth file: %v", err)
	}
	if auth.Tokens.AccessToken != newAccessToken {
		t.Fatalf("expected saved refreshed access token, got %#v", auth.Tokens)
	}
	if auth.Tokens.RefreshToken != "refresh-old" {
		t.Fatalf("expected saved refresh token to be preserved, got %#v", auth.Tokens)
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
