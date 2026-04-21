package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
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
			out, err := tool.Execute(context.Background(), map[string]any{"message": "hello"})
			if err != nil {
				t.Fatalf("Execute echo: %v", err)
			}
			if out != "echo: hello" {
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
