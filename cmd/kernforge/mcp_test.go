package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("KERNFORGE_MCP_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		msg, err := readRPCMessage(reader)
		if err != nil {
			os.Exit(0)
		}
		method, _ := msg["method"].(string)
		switch method {
		case "initialize":
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]any{
						"tools":     map[string]any{},
						"resources": map[string]any{},
						"prompts":   map[string]any{},
					},
					"serverInfo": map[string]any{
						"name":    "fake",
						"version": "1.0.0",
					},
				},
			})
		case "notifications/initialized":
			continue
		case "tools/list":
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "echo",
						"description": "Echo a message",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"message": map[string]any{"type": "string"},
							},
						},
						"outputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"echoed": map[string]any{"type": "string"},
							},
						},
					}},
				},
			})
		case "tools/call":
			params, _ := msg["params"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			message, _ := args["message"].(string)
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"content": []map[string]any{{
						"type": "text",
						"text": fmt.Sprintf("echo: %s", message),
					}},
					"structuredContent": map[string]any{
						"echoed": message,
					},
					"_meta": map[string]any{
						"trace_id": "trace-" + message,
					},
				},
			})
		case "resources/list":
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"resources": []map[string]any{{
						"uri":         "memo://project",
						"name":        "project",
						"description": "Project memo",
						"mimeType":    "text/plain",
					}},
				},
			})
		case "resources/read":
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"contents": []map[string]any{{
						"uri":      "memo://project",
						"mimeType": "text/plain",
						"text":     "resource body",
					}},
				},
			})
		case "prompts/list":
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"prompts": []map[string]any{{
						"name":        "summarize",
						"description": "Summarize a topic",
						"arguments": []map[string]any{{
							"name":        "topic",
							"description": "Topic to summarize",
							"required":    true,
						}},
					}},
				},
			})
		case "prompts/get":
			params, _ := msg["params"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			topic, _ := args["topic"].(string)
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"description": "Prompt body",
					"messages": []map[string]any{{
						"role": "user",
						"content": []map[string]any{{
							"type": "text",
							"text": "Summarize " + topic,
						}},
					}},
				},
			})
		default:
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"error": map[string]any{
					"message": "unsupported method",
				},
			})
		}
	}
}

func TestMCPWebResearchHelperProcess(t *testing.T) {
	if os.Getenv("KERNFORGE_MCP_WEB_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		msg, err := readRPCMessage(reader)
		if err != nil {
			os.Exit(0)
		}
		method, _ := msg["method"].(string)
		switch method {
		case "initialize":
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]any{
						"tools":     map[string]any{},
						"resources": map[string]any{},
						"prompts":   map[string]any{},
					},
					"serverInfo": map[string]any{
						"name":    "web-research",
						"version": "1.0.0",
					},
				},
			})
		case "notifications/initialized":
			continue
		case "tools/list":
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"tools": []map[string]any{
						{
							"name":        "search_web",
							"description": "Search the web for current articles, papers, and vendor references",
							"inputSchema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"query": map[string]any{"type": "string"},
								},
							},
						},
						{
							"name":        "fetch_url",
							"description": "Fetch a URL and return the page text for later synthesis",
							"inputSchema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"url": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
			})
		case "tools/call":
			params, _ := msg["params"].(map[string]any)
			name, _ := params["name"].(string)
			args, _ := params["arguments"].(map[string]any)
			switch name {
			case "search_web":
				query, _ := args["query"].(string)
				_ = writeRPCMessage(os.Stdout, map[string]any{
					"jsonrpc": "2.0",
					"id":      msg["id"],
					"result": map[string]any{
						"content": []map[string]any{{
							"type": "text",
							"text": fmt.Sprintf("Search results for: %s\n1. https://example.test/hypervisor-detection (2026-04-01)\n2. https://example.test/anti-cheat-telemetry (2026-03-20)", query),
						}},
					},
				})
			case "fetch_url":
				url, _ := args["url"].(string)
				_ = writeRPCMessage(os.Stdout, map[string]any{
					"jsonrpc": "2.0",
					"id":      msg["id"],
					"result": map[string]any{
						"content": []map[string]any{{
							"type": "text",
							"text": fmt.Sprintf("Fetched %s\nKey findings:\n- Hypervisor-aware anti-cheat relies on timing, CPUID, and telemetry cross-checks.\n- Kernel and user telemetry should be correlated before enforcement.", url),
						}},
					},
				})
			default:
				_ = writeRPCMessage(os.Stdout, map[string]any{
					"jsonrpc": "2.0",
					"id":      msg["id"],
					"error": map[string]any{
						"message": "unsupported method",
					},
				})
			}
		case "resources/list":
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"resources": []map[string]any{},
				},
			})
		case "prompts/list":
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"prompts": []map[string]any{},
				},
			})
		default:
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"error": map[string]any{
					"message": "unsupported method",
				},
			})
		}
	}
}

func TestMCPEnvHelperProcess(t *testing.T) {
	if os.Getenv("KERNFORGE_MCP_ENV_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		msg, err := readRPCMessage(reader)
		if err != nil {
			os.Exit(0)
		}
		method, _ := msg["method"].(string)
		switch method {
		case "initialize":
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]any{
						"tools": map[string]any{},
					},
					"serverInfo": map[string]any{
						"name":    "env",
						"version": "1.0.0",
					},
				},
			})
		case "notifications/initialized":
			continue
		case "tools/list":
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "echo_env",
						"description": "Return one environment variable",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"name": map[string]any{"type": "string"},
							},
						},
					}},
				},
			})
		case "tools/call":
			params, _ := msg["params"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			name, _ := args["name"].(string)
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"content": []map[string]any{{
						"type": "text",
						"text": os.Getenv(name),
					}},
				},
			})
		default:
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"error": map[string]any{
					"message": "unsupported method",
				},
			})
		}
	}
}

func TestLoadMCPManagerStartsServerAndCallsTools(t *testing.T) {
	dir := t.TempDir()
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name:    "fake",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPHelperProcess"},
		Env: map[string]string{
			"KERNFORGE_MCP_HELPER": "1",
		},
	}})
	defer manager.Close()

	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	statuses := manager.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].ToolCount != 1 {
		t.Fatalf("expected 1 remote tool, got %d", statuses[0].ToolCount)
	}
	if statuses[0].ResourceCount != 1 {
		t.Fatalf("expected 1 remote resource, got %d", statuses[0].ResourceCount)
	}
	if statuses[0].PromptCount != 1 {
		t.Fatalf("expected 1 remote prompt, got %d", statuses[0].PromptCount)
	}

	tools := manager.Tools()
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}
	foundEcho := false
	foundResource := false
	foundPrompt := false
	for _, tool := range tools {
		switch tool.Definition().Name {
		case "mcp__fake__echo":
			foundEcho = true
			def := tool.Definition()
			if got := stringValue(def.OutputSchema, "type"); got != "object" {
				t.Fatalf("expected MCP outputSchema to be exposed, got %#v", def.OutputSchema)
			}
			out, err := tool.Execute(context.Background(), map[string]any{"message": "hello"})
			if err != nil {
				t.Fatalf("Execute echo: %v", err)
			}
			if out != `{"echoed":"hello"}` {
				t.Fatalf("unexpected echo output: %q", out)
			}
		case "mcp__resource__fake":
			foundResource = true
			out, err := tool.Execute(context.Background(), map[string]any{"uri": "project"})
			if err != nil {
				t.Fatalf("Execute resource: %v", err)
			}
			if !strings.Contains(out, "resource body") {
				t.Fatalf("unexpected resource output: %q", out)
			}
		case "mcp__prompt__fake":
			foundPrompt = true
			out, err := tool.Execute(context.Background(), map[string]any{
				"name": "summarize",
				"arguments": map[string]any{
					"topic": "tests",
				},
			})
			if err != nil {
				t.Fatalf("Execute prompt: %v", err)
			}
			if !strings.Contains(out, "Summarize tests") {
				t.Fatalf("unexpected prompt output: %q", out)
			}
		}
	}
	if !foundEcho || !foundResource || !foundPrompt {
		t.Fatalf("missing expected synthetic tools: echo=%v resource=%v prompt=%v", foundEcho, foundResource, foundPrompt)
	}

	resources := manager.Resources()
	if len(resources) != 1 || resources[0].Resource.URI != "memo://project" {
		t.Fatalf("unexpected resources: %#v", resources)
	}
	prompts := manager.Prompts()
	if len(prompts) != 1 || prompts[0].Prompt.Name != "summarize" {
		t.Fatalf("unexpected prompts: %#v", prompts)
	}
}

func TestMCPToolExecuteDetailedPreservesResultMeta(t *testing.T) {
	dir := t.TempDir()
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name:    "fake",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPHelperProcess"},
		Env: map[string]string{
			"KERNFORGE_MCP_HELPER": "1",
		},
	}})
	defer manager.Close()

	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	registry := NewToolRegistry(manager.Tools()...)
	result, err := registry.ExecuteDetailed(context.Background(), "mcp__fake__echo", `{"message":"hello"}`)
	if err != nil {
		t.Fatalf("ExecuteDetailed echo: %v", err)
	}
	if result.DisplayText != `{"echoed":"hello"}` {
		t.Fatalf("unexpected display text: %q", result.DisplayText)
	}
	if !strings.HasPrefix(result.ModelText, "Wall time: ") || !strings.HasSuffix(result.ModelText, "\nOutput:\n"+`{"echoed":"hello"}`) {
		t.Fatalf("unexpected model text: %q", result.ModelText)
	}
	if got := toolMetaString(result.Meta, "mcp_server"); got != "fake" {
		t.Fatalf("expected MCP server metadata, got %#v", result.Meta)
	}
	if got := toolMetaString(result.Meta, "mcp_tool"); got != "echo" {
		t.Fatalf("expected MCP tool metadata, got %#v", result.Meta)
	}
	if got := toolMetaString(result.Meta, "mcp_namespaced_tool"); got != "mcp__fake__echo" {
		t.Fatalf("expected namespaced MCP tool metadata, got %#v", result.Meta)
	}
	if !toolMetaBool(result.Meta, "mcp_has_meta") {
		t.Fatalf("expected MCP _meta presence marker, got %#v", result.Meta)
	}
	rawMeta, ok := result.Meta["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected MCP _meta payload, got %#v", result.Meta["_meta"])
	}
	if got := rawMeta["trace_id"]; got != "trace-hello" {
		t.Fatalf("unexpected MCP _meta trace: %#v", rawMeta)
	}
	structured, ok := result.Meta["mcp_result_structured_content"].(map[string]any)
	if !ok {
		t.Fatalf("expected structuredContent metadata, got %#v", result.Meta["mcp_result_structured_content"])
	}
	if got := structured["echoed"]; got != "hello" {
		t.Fatalf("unexpected structuredContent payload: %#v", structured)
	}
	if len(result.ContentItems) != 0 {
		t.Fatalf("structuredContent MCP result should remain text-only for model replay, got %#v", result.ContentItems)
	}
	if len(result.ModelContentItems) != 0 {
		t.Fatalf("structuredContent MCP model result should not include content items, got %#v", result.ModelContentItems)
	}

	encoded, err := json.Marshal(result.Meta)
	if err != nil {
		t.Fatalf("marshal result metadata: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal result metadata: %v", err)
	}
	if _, ok := decoded["_meta"]; !ok {
		t.Fatalf("expected serialized metadata to preserve _meta spelling: %s", string(encoded))
	}
	if _, ok := decoded["meta"]; ok {
		t.Fatalf("MCP result metadata must not be serialized as meta: %s", string(encoded))
	}

	session := NewSession(dir, "provider", "model", "", "default")
	agent := &Agent{Session: session}
	call := ToolCall{
		ID:        "call-1",
		Name:      "mcp__fake__echo",
		Arguments: `{"message":"hello"}`,
	}
	agent.noteToolConversationResult(call, result)
	if len(session.ConversationEvents) == 0 {
		t.Fatalf("expected conversation event")
	}
	jsonl, err := renderSessionEventsJSONL(session, session.ConversationEvents)
	if err != nil {
		t.Fatalf("render session events: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(jsonl), "\n")
	record := SessionEventStreamRecord{}
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("unmarshal session event: %v", err)
	}
	mcpResult, ok := record.Event.Metadata["mcp_result"].(map[string]any)
	if !ok {
		t.Fatalf("expected MCP result metadata in event JSONL, got %#v", record.Event.Metadata)
	}
	if _, ok := mcpResult["_meta"]; !ok {
		t.Fatalf("expected event JSONL to preserve MCP result _meta: %#v", mcpResult)
	}
	if _, ok := mcpResult["meta"]; ok {
		t.Fatalf("event JSONL must not rename MCP result _meta to meta: %#v", mcpResult)
	}
	if _, ok := mcpResult["structuredContent"]; !ok {
		t.Fatalf("expected event JSONL to preserve MCP structuredContent spelling: %#v", mcpResult)
	}
	if _, ok := mcpResult["structured_content"]; ok {
		t.Fatalf("event JSONL must not snake-case MCP structuredContent: %#v", mcpResult)
	}
}

func TestMCPToolContentItemsPreserveImageContentForResponses(t *testing.T) {
	result := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "screenshot follows",
			},
			map[string]any{
				"type":     "image",
				"data":     "AAA",
				"mimeType": "image/png",
				"_meta": map[string]any{
					"codex/imageDetail": "original",
				},
			},
		},
	}

	items := mcpToolContentItems(result)
	if len(items) != 2 {
		t.Fatalf("expected text and image content items, got %#v", items)
	}
	if items[0].Type != "input_text" || items[0].Text != "screenshot follows" {
		t.Fatalf("unexpected text item: %#v", items[0])
	}
	if items[1].Type != "input_image" || items[1].ImageURL != "data:image/png;base64,AAA" || items[1].Detail != imageDetailOriginal {
		t.Fatalf("unexpected image item: %#v", items[1])
	}
	modelText, modelItems := mcpToolModelOutput(result, 500*time.Millisecond)
	if modelText != "Wall time: 0.5000 seconds\nOutput:" {
		t.Fatalf("unexpected model text: %q", modelText)
	}
	if len(modelItems) != 3 || modelItems[0].Type != "input_text" || modelItems[0].Text != "Wall time: 0.5000 seconds\nOutput:" {
		t.Fatalf("expected wall-time header as first model content item, got %#v", modelItems)
	}
	msg := Message{
		Role:             "tool",
		ToolCallID:       "call-mcp-image",
		ToolName:         "mcp__fake__screenshot",
		Text:             modelText,
		ToolContentItems: modelItems,
	}
	output, ok := toolOutputForResponses(msg).([]map[string]any)
	if !ok || len(output) != 3 {
		t.Fatalf("expected Responses content-item output, got %#v", toolOutputForResponses(msg))
	}
	if output[0]["type"] != "input_text" || output[0]["text"] != "Wall time: 0.5000 seconds\nOutput:" {
		t.Fatalf("unexpected Responses header output: %#v", output[0])
	}
	if output[2]["type"] != "input_image" || output[2]["image_url"] != "data:image/png;base64,AAA" || output[2]["detail"] != imageDetailOriginal {
		t.Fatalf("unexpected Responses image output: %#v", output[2])
	}
}

func TestMCPToolModelOutputSerializesTextContentLikeCodex(t *testing.T) {
	result := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "done",
			},
		},
	}

	modelText, items := mcpToolModelOutput(result, 1250*time.Millisecond)
	if len(items) != 0 {
		t.Fatalf("text-only MCP result should remain model text, got %#v", items)
	}
	const prefix = "Wall time: 1.2500 seconds\nOutput:\n"
	if !strings.HasPrefix(modelText, prefix) {
		t.Fatalf("missing wall-time prefix: %q", modelText)
	}
	var payload []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(modelText, prefix)), &payload); err != nil {
		t.Fatalf("model output should serialize raw MCP content JSON: %v text=%q", err, modelText)
	}
	if len(payload) != 1 || payload[0]["type"] != "text" || payload[0]["text"] != "done" {
		t.Fatalf("unexpected serialized content payload: %#v", payload)
	}
}

func TestAgentExpandMentionsInjectsMCPResource(t *testing.T) {
	dir := t.TempDir()
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name:    "fake",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPHelperProcess"},
		Env: map[string]string{
			"KERNFORGE_MCP_HELPER": "1",
		},
	}})
	defer manager.Close()
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	agent := &Agent{
		Session: &Session{WorkingDir: dir},
		MCP:     manager,
	}

	text, _ := agent.expandMentions(context.Background(), "look at @mcp:fake:project")
	if !strings.Contains(text, "Referenced MCP resource: mcp:fake:memo://project") {
		t.Fatalf("expected MCP resource context, got %q", text)
	}
	if !strings.Contains(text, "resource body") {
		t.Fatalf("expected resource body, got %q", text)
	}
}

func TestMCPManagerTreatsCapabilityTaggedServerAsWebResearch(t *testing.T) {
	manager := &MCPManager{
		servers: []*MCPClient{
			{
				config: MCPServerConfig{
					Name:         "research",
					Capabilities: []string{"web_search", "web_fetch"},
				},
				tools: []MCPToolDescriptor{
					{Name: "lookup", Description: "General lookup"},
				},
				prompts: []MCPPromptDescriptor{
					{Name: "summarize", Description: "Summarize the fetched page"},
				},
			},
		},
	}

	if !manager.HasWebResearchCapability() {
		t.Fatalf("expected capability-tagged server to be treated as web research")
	}
	catalog := manager.WebResearchCatalogPrompt()
	if !strings.Contains(catalog, "mcp__research__lookup") {
		t.Fatalf("expected capability-tagged tool in web catalog, got %q", catalog)
	}
	if !manager.IsWebResearchToolName("mcp__research__lookup") {
		t.Fatalf("expected namespaced tool to be recognized as web research")
	}
	if !manager.IsWebResearchToolCall(ToolCall{Name: "mcp__research__lookup", Arguments: `{}`}) {
		t.Fatalf("expected tool call to be recognized as web research")
	}
}

func TestLoadMCPManagerStartsCapabilityTaggedWebResearchServerAndCallsTools(t *testing.T) {
	dir := t.TempDir()
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name:         "web-research",
		Command:      os.Args[0],
		Args:         []string{"-test.run=TestMCPWebResearchHelperProcess"},
		Capabilities: []string{"web_search", "web_fetch"},
		Env: map[string]string{
			"KERNFORGE_MCP_WEB_HELPER": "1",
		},
	}})
	defer manager.Close()

	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if !manager.HasWebResearchCapability() {
		t.Fatalf("expected loaded server to expose web research capability")
	}
	statuses := manager.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].ToolCount != 2 {
		t.Fatalf("expected 2 remote tools, got %d", statuses[0].ToolCount)
	}
	catalog := manager.WebResearchCatalogPrompt()
	if !strings.Contains(catalog, "mcp__web_research__search_web") || !strings.Contains(catalog, "mcp__web_research__fetch_url") {
		t.Fatalf("expected web tools in catalog, got %q", catalog)
	}

	var searchTool Tool
	var fetchTool Tool
	for _, tool := range manager.Tools() {
		switch tool.Definition().Name {
		case "mcp__web_research__search_web":
			searchTool = tool
		case "mcp__web_research__fetch_url":
			fetchTool = tool
		}
	}
	if searchTool == nil || fetchTool == nil {
		t.Fatalf("expected both web tools, got search=%v fetch=%v", searchTool != nil, fetchTool != nil)
	}

	searchOut, err := searchTool.Execute(context.Background(), map[string]any{"query": "hypervisor anti-cheat latest"})
	if err != nil {
		t.Fatalf("Execute search_web: %v", err)
	}
	if !strings.Contains(searchOut, "https://example.test/hypervisor-detection") {
		t.Fatalf("unexpected search output: %q", searchOut)
	}

	fetchOut, err := fetchTool.Execute(context.Background(), map[string]any{"url": "https://example.test/hypervisor-detection"})
	if err != nil {
		t.Fatalf("Execute fetch_url: %v", err)
	}
	if !strings.Contains(fetchOut, "Key findings:") {
		t.Fatalf("unexpected fetch output: %q", fetchOut)
	}
}

func TestWorkspaceWebResearchMCPScriptLoads(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v", err)
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: root, Root: root}, []MCPServerConfig{{
		Name:         "web-research",
		Command:      "node",
		Args:         []string{".kernforge/mcp/web-research-mcp.js"},
		Cwd:          ".",
		Capabilities: []string{"web_search", "web_fetch"},
	}})
	defer manager.Close()

	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if !manager.HasWebResearchCapability() {
		t.Fatalf("expected workspace script to expose web research capability")
	}
	statuses := manager.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].ToolCount != 2 {
		t.Fatalf("expected 2 remote tools, got %d", statuses[0].ToolCount)
	}

	foundSearch := false
	foundFetch := false
	for _, tool := range manager.Tools() {
		switch tool.Definition().Name {
		case "mcp__web_research__search_web":
			foundSearch = true
		case "mcp__web_research__fetch_url":
			foundFetch = true
		}
	}
	if !foundSearch || !foundFetch {
		t.Fatalf("expected workspace script tools, got search=%v fetch=%v", foundSearch, foundFetch)
	}
}

func TestLoadMCPManagerSkipsEmptyEnvOverridesAndKeepsConfiguredValues(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TAVILY_API_KEY", "parent-value")

	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name:    "env",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPEnvHelperProcess"},
		Env: map[string]string{
			"KERNFORGE_MCP_ENV_HELPER": "1",
			"TAVILY_API_KEY":           "",
			"SERPAPI_API_KEY":          "config-value",
		},
	}})
	defer manager.Close()

	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	var echoTool Tool
	for _, tool := range manager.Tools() {
		if tool.Definition().Name == "mcp__env__echo_env" {
			echoTool = tool
			break
		}
	}
	if echoTool == nil {
		t.Fatalf("expected echo_env tool")
	}

	tavilyValue, err := echoTool.Execute(context.Background(), map[string]any{"name": "TAVILY_API_KEY"})
	if err != nil {
		t.Fatalf("Execute TAVILY_API_KEY: %v", err)
	}
	if tavilyValue != "parent-value" {
		t.Fatalf("expected parent env to survive empty override, got %q", tavilyValue)
	}

	serpValue, err := echoTool.Execute(context.Background(), map[string]any{"name": "SERPAPI_API_KEY"})
	if err != nil {
		t.Fatalf("Execute SERPAPI_API_KEY: %v", err)
	}
	if serpValue != "config-value" {
		t.Fatalf("expected non-empty config env override, got %q", serpValue)
	}
}
