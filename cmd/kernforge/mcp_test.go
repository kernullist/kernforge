package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var mcpHelperBarrierMu sync.Mutex
var mcpHelperBarrierWaiters []chan struct{}
var mcpHelperStdoutMu sync.Mutex

func mcpHelperBarrierArrive(participants int, timeout time.Duration) bool {
	if participants <= 1 {
		return true
	}
	ch := make(chan struct{})
	mcpHelperBarrierMu.Lock()
	mcpHelperBarrierWaiters = append(mcpHelperBarrierWaiters, ch)
	if len(mcpHelperBarrierWaiters) >= participants {
		waiters := mcpHelperBarrierWaiters
		mcpHelperBarrierWaiters = nil
		for _, waiter := range waiters {
			close(waiter)
		}
		mcpHelperBarrierMu.Unlock()
		return true
	}
	mcpHelperBarrierMu.Unlock()
	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		mcpHelperBarrierMu.Lock()
		for i, waiter := range mcpHelperBarrierWaiters {
			if waiter == ch {
				mcpHelperBarrierWaiters = append(mcpHelperBarrierWaiters[:i], mcpHelperBarrierWaiters[i+1:]...)
				break
			}
		}
		mcpHelperBarrierMu.Unlock()
		return false
	}
}

func writeMCPHelperMessage(payload map[string]any) {
	mcpHelperStdoutMu.Lock()
	defer mcpHelperStdoutMu.Unlock()
	_ = writeRPCMessage(os.Stdout, payload)
}

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
			writeMCPHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"tools": []map[string]any{
						{
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
						},
						{
							"name":        "echo_readonly",
							"description": "Echo a message without mutating server state",
							"annotations": map[string]any{
								"readOnlyHint": true,
							},
							"inputSchema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"message": map[string]any{"type": "string"},
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
			message, _ := args["message"].(string)
			if name == "echo_readonly" && message == "barrier" {
				id := msg["id"]
				go func() {
					if !mcpHelperBarrierArrive(2, 2*time.Second) {
						writeMCPHelperMessage(map[string]any{
							"jsonrpc": "2.0",
							"id":      id,
							"error": map[string]any{
								"message": "barrier timeout",
							},
						})
						return
					}
					writeMCPHelperMessage(map[string]any{
						"jsonrpc": "2.0",
						"id":      id,
						"result": map[string]any{
							"structuredContent": map[string]any{
								"echoed": "barrier",
							},
						},
					})
				}()
				continue
			}
			structured := map[string]any{
				"echoed": message,
			}
			if message == "meta" {
				if requestMeta, ok := params["_meta"].(map[string]any); ok {
					structured["request_meta"] = requestMeta
				}
			}
			writeMCPHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"content": []map[string]any{{
						"type": "text",
						"text": fmt.Sprintf("echo: %s", message),
					}},
					"structuredContent": structured,
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

func TestMCPInvalidToolHelperProcess(t *testing.T) {
	if os.Getenv("KERNFORGE_MCP_INVALID_HELPER") != "1" {
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
						"name":    "invalid",
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
							"description": "Search the web with a valid schema",
							"inputSchema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"query": map[string]any{"type": "string"},
								},
							},
						},
						{
							"name":        "bad_search",
							"description": "Search web with an invalid schema",
							"inputSchema": map[string]any{
								"type": "string",
							},
						},
					},
				},
			})
		case "tools/call":
			params, _ := msg["params"].(map[string]any)
			name, _ := params["name"].(string)
			args, _ := params["arguments"].(map[string]any)
			query, _ := args["query"].(string)
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"content": []map[string]any{{
						"type": "text",
						"text": fmt.Sprintf("%s: %s", name, query),
					}},
				},
			})
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
			value, ok := os.LookupEnv(name)
			if !ok {
				value = "__MISSING__"
			}
			_ = writeRPCMessage(os.Stdout, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"content": []map[string]any{{
						"type": "text",
						"text": value,
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
	if statuses[0].ToolCount != 2 {
		t.Fatalf("expected 2 remote tools, got %d", statuses[0].ToolCount)
	}
	if statuses[0].EnvironmentID != defaultMCPServerEnvironmentID {
		t.Fatalf("expected default MCP environment %q, got %q", defaultMCPServerEnvironmentID, statuses[0].EnvironmentID)
	}
	if statuses[0].ResourceCount != 1 {
		t.Fatalf("expected 1 remote resource, got %d", statuses[0].ResourceCount)
	}
	if statuses[0].PromptCount != 1 {
		t.Fatalf("expected 1 remote prompt, got %d", statuses[0].PromptCount)
	}

	tools := manager.Tools()
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools))
	}
	foundEcho := false
	foundReadOnlyEcho := false
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
		case "mcp__fake__echo_readonly":
			foundReadOnlyEcho = true
			if !tool.(readOnlyToolCallSupport).ReadOnlyToolCall() {
				t.Fatalf("expected echo_readonly to be read-only")
			}
			if !tool.(parallelToolCallSupport).SupportsParallelToolCalls() {
				t.Fatalf("expected echo_readonly to support parallel tool calls")
			}
		case "mcp__resource__fake":
			foundResource = true
			if !tool.(readOnlyToolCallSupport).ReadOnlyToolCall() {
				t.Fatalf("expected synthetic resource tool to be read-only")
			}
			if !tool.(parallelToolCallSupport).SupportsParallelToolCalls() {
				t.Fatalf("expected synthetic resource tool to support parallel tool calls")
			}
			out, err := tool.Execute(context.Background(), map[string]any{"uri": "project"})
			if err != nil {
				t.Fatalf("Execute resource: %v", err)
			}
			if !strings.Contains(out, "resource body") {
				t.Fatalf("unexpected resource output: %q", out)
			}
		case "mcp__prompt__fake":
			foundPrompt = true
			if !tool.(readOnlyToolCallSupport).ReadOnlyToolCall() {
				t.Fatalf("expected synthetic prompt tool to be read-only")
			}
			if !tool.(parallelToolCallSupport).SupportsParallelToolCalls() {
				t.Fatalf("expected synthetic prompt tool to support parallel tool calls")
			}
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
	if !foundEcho || !foundReadOnlyEcho || !foundResource || !foundPrompt {
		t.Fatalf("missing expected synthetic tools: echo=%v readonly=%v resource=%v prompt=%v", foundEcho, foundReadOnlyEcho, foundResource, foundPrompt)
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

func TestLoadMCPManagerStartsStreamableHTTPServerAndCallsTools(t *testing.T) {
	t.Setenv("MCP_HTTP_TOKEN", "test-token")
	t.Setenv("MCP_HTTP_ENV_HEADER", "env-value")
	sessionID := "session-123"
	var sawSession bool
	var sawDelete bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			if r.Header.Get("mcp-session-id") == sessionID {
				sawDelete = true
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		if got := r.Header.Get("X-Static"); got != "static-value" {
			t.Fatalf("unexpected static header %q", got)
		}
		if got := r.Header.Get("X-Env"); got != "env-value" {
			t.Fatalf("unexpected env header %q", got)
		}
		if r.Header.Get("mcp-session-id") == sessionID {
			sawSession = true
		}
		var msg map[string]any
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		method, _ := msg["method"].(string)
		if strings.HasPrefix(method, "notifications/") {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("mcp-session-id", sessionID)
		switch method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]any{
						"tools": map[string]any{},
					},
					"serverInfo": map[string]any{
						"name":    "http",
						"version": "1.0.0",
					},
				},
			})
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
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
		case "resources/list", "prompts/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result":  map[string]any{},
			})
		case "tools/call":
			params, _ := msg["params"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			message, _ := args["message"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"structuredContent": map[string]any{
						"echoed": message,
					},
				},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"error": map[string]any{
					"message": "unsupported method",
				},
			})
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name:              "http",
		URL:               server.URL,
		BearerTokenEnvVar: "MCP_HTTP_TOKEN",
		HTTPHeaders: map[string]string{
			"X-Static": "static-value",
		},
		EnvHTTPHeaders: map[string]string{
			"X-Env": "MCP_HTTP_ENV_HEADER",
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
	if statuses[0].Transport != "streamable_http" || statuses[0].URL != server.URL {
		t.Fatalf("unexpected HTTP MCP status: %#v", statuses[0])
	}
	if statuses[0].AuthStatus != "bearer_token" {
		t.Fatalf("expected bearer token auth status, got %#v", statuses[0])
	}
	tools := manager.Tools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	out, err := tools[0].Execute(context.Background(), map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("Execute echo: %v", err)
	}
	if out != `{"echoed":"hello"}` {
		t.Fatalf("unexpected echo output: %q", out)
	}
	if !sawSession {
		t.Fatalf("expected subsequent HTTP requests to include mcp-session-id")
	}
	manager.Close()
	if !sawDelete {
		t.Fatalf("expected HTTP MCP session delete on close")
	}
}

func TestMCPHTTPAuthStatusReportsConfiguredModes(t *testing.T) {
	if got := mcpHTTPAuthStatus(MCPServerConfig{URL: "https://example.com/mcp"}, nil, nil); got != "none" {
		t.Fatalf("expected no auth status, got %q", got)
	}
	if got := mcpHTTPAuthStatus(MCPServerConfig{
		URL:               "https://example.com/mcp",
		BearerTokenEnvVar: "MCP_TOKEN",
	}, nil, func(string) string { return "" }); got != "bearer_token_missing" {
		t.Fatalf("expected missing bearer token status, got %q", got)
	}
	if got := mcpHTTPAuthStatus(MCPServerConfig{
		URL:               "https://example.com/mcp",
		BearerTokenEnvVar: "MCP_TOKEN",
	}, nil, func(string) string { return "token" }); got != "bearer_token" {
		t.Fatalf("expected bearer token status, got %q", got)
	}
	if got := mcpHTTPAuthStatus(MCPServerConfig{URL: "https://example.com/mcp"}, map[string]string{
		"Authorization": "Bearer token",
	}, nil); got != "bearer_token" {
		t.Fatalf("expected authorization header bearer status, got %q", got)
	}
	if got := mcpHTTPAuthStatus(MCPServerConfig{
		URL: "https://example.com/mcp",
		EnvHTTPHeaders: map[string]string{
			"Authorization": "MCP_AUTH_HEADER",
		},
	}, nil, func(string) string { return "" }); got != "bearer_token_missing" {
		t.Fatalf("expected missing authorization env header status, got %q", got)
	}
	if got := mcpHTTPAuthStatus(MCPServerConfig{
		URL: "https://example.com/mcp",
		OAuth: &MCPServerOAuthConfig{
			ClientID: "client",
		},
	}, nil, nil); got != "oauth_configured" {
		t.Fatalf("expected oauth status, got %q", got)
	}
	if got := mcpHTTPAuthStatus(MCPServerConfig{
		Command: "node",
	}, nil, nil); got != "" {
		t.Fatalf("expected stdio auth status to be empty, got %q", got)
	}
}

func TestReadMCPHTTPSSEMessageParsesFirstDataEvent(t *testing.T) {
	msg, err := readMCPHTTPSSEMessage(strings.NewReader("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\n"))
	if err != nil {
		t.Fatalf("readMCPHTTPSSEMessage: %v", err)
	}
	result, ok := msg["result"].(map[string]any)
	if !ok || result["ok"] != true {
		t.Fatalf("unexpected SSE JSON-RPC message: %#v", msg)
	}
}

func TestLoadMCPManagerSkipsInvalidToolSpecsBeforeRegistration(t *testing.T) {
	dir := t.TempDir()
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name:         "invalid",
		Command:      os.Args[0],
		Args:         []string{"-test.run=TestMCPInvalidToolHelperProcess"},
		Capabilities: []string{"web_search"},
		Env: map[string]string{
			"KERNFORGE_MCP_INVALID_HELPER": "1",
		},
	}})
	defer manager.Close()

	if len(warnings) != 1 {
		t.Fatalf("expected one invalid tool warning, got %v", warnings)
	}
	if !strings.Contains(warnings[0], "skipped tool bad_search") {
		t.Fatalf("expected invalid tool warning to name bad_search, got %q", warnings[0])
	}

	statuses := manager.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].ToolCount != 1 {
		t.Fatalf("expected only the valid remote tool in status, got %d", statuses[0].ToolCount)
	}

	tools := manager.Tools()
	if len(tools) != 1 {
		t.Fatalf("expected only the valid MCP tool to be registered, got %d", len(tools))
	}
	def := tools[0].Definition()
	if def.Name != "mcp__invalid__search_web" {
		t.Fatalf("expected valid tool definition, got %q", def.Name)
	}
	if len(NewToolRegistry(tools...).Definitions()) != 1 {
		t.Fatalf("expected registry to expose exactly one valid MCP definition")
	}

	catalog := manager.WebResearchCatalogPrompt()
	if !strings.Contains(catalog, "mcp__invalid__search_web") {
		t.Fatalf("expected valid web tool in catalog, got %q", catalog)
	}
	if strings.Contains(catalog, "bad_search") || strings.Contains(catalog, "mcp__invalid__bad_search") {
		t.Fatalf("invalid tool leaked into web research catalog: %q", catalog)
	}
}

func TestMCPServerParallelOptInMarksMutableToolsParallelSafe(t *testing.T) {
	dir := t.TempDir()
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name:                      "fake",
		Command:                   os.Args[0],
		Args:                      []string{"-test.run=TestMCPHelperProcess"},
		SupportsParallelToolCalls: true,
		Env: map[string]string{
			"KERNFORGE_MCP_HELPER": "1",
		},
	}})
	defer manager.Close()

	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	registry := NewToolRegistry(manager.Tools()...)
	if registry.ToolCallReadOnly("mcp__fake__echo") {
		t.Fatalf("server opt-in must not make mutable tools read-only")
	}
	if !registry.ToolCallSupportsParallel("mcp__fake__echo") {
		t.Fatalf("expected server opt-in to make mutable MCP tool parallel-safe")
	}
}

func TestMCPClientMultiplexesParallelReadOnlyToolCalls(t *testing.T) {
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
	if !registry.ToolCallReadOnly("mcp__fake__echo_readonly") {
		t.Fatalf("expected readOnlyHint tool to be read-only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := registry.ExecuteDetailed(ctx, "mcp__fake__echo_readonly", `{"message":"barrier"}`)
			if err != nil {
				errs <- err
				return
			}
			if result.DisplayText != `{"echoed":"barrier"}` {
				errs <- fmt.Errorf("unexpected barrier output: %q", result.DisplayText)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadMCPManagerAcceptsExplicitLocalEnvironment(t *testing.T) {
	dir := t.TempDir()
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name:          "fake",
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestMCPHelperProcess"},
		EnvironmentID: " local ",
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
	if statuses[0].EnvironmentID != defaultMCPServerEnvironmentID {
		t.Fatalf("expected explicit local MCP environment, got %#v", statuses[0])
	}
	if statuses[0].ToolCount != 2 {
		t.Fatalf("expected local MCP server to start, got %#v", statuses[0])
	}
}

func TestLoadMCPManagerRejectsUnsupportedExplicitEnvironmentWithoutStartingServer(t *testing.T) {
	dir := t.TempDir()
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name:          "remote",
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestMCPHelperProcess"},
		Cwd:           dir,
		EnvironmentID: "remote",
		Env: map[string]string{
			"KERNFORGE_MCP_HELPER": "1",
		},
	}})
	defer manager.Close()

	if len(warnings) != 1 || !strings.Contains(warnings[0], `unsupported environment_id "remote"`) {
		t.Fatalf("expected unsupported environment warning, got %#v", warnings)
	}
	statuses := manager.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 failed status, got %d", len(statuses))
	}
	if statuses[0].ToolCount != 0 || statuses[0].EnvironmentID != "remote" {
		t.Fatalf("expected failed remote status without tools, got %#v", statuses[0])
	}
	if !strings.Contains(statuses[0].Error, `unsupported environment_id "remote"`) {
		t.Fatalf("expected unsupported environment status error, got %#v", statuses[0])
	}
}

func TestLoadMCPManagerRejectsExplicitEnvironmentRelativeCwd(t *testing.T) {
	dir := t.TempDir()
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name:          "remote",
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestMCPHelperProcess"},
		Cwd:           "relative",
		EnvironmentID: "remote",
		Env: map[string]string{
			"KERNFORGE_MCP_HELPER": "1",
		},
	}})
	defer manager.Close()

	if len(warnings) != 1 || !strings.Contains(warnings[0], "requires an absolute cwd") {
		t.Fatalf("expected absolute cwd warning, got %#v", warnings)
	}
	statuses := manager.Status()
	if len(statuses) != 1 || !strings.Contains(statuses[0].Error, "requires an absolute cwd") {
		t.Fatalf("expected failed absolute cwd status, got %#v", statuses)
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

func TestMCPToolPreToolUseRewriteExecutesUpdatedArguments(t *testing.T) {
	dir := t.TempDir()
	var observed string
	manager, warnings := LoadMCPManager(Workspace{
		BaseRoot: dir,
		Root:     dir,
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			if event != HookPreToolUse {
				return HookVerdict{Allow: true}, nil
			}
			if input, ok := payload["tool_input"].(map[string]any); ok {
				observed = stringsValueFromAny(input["message"])
			}
			return HookVerdict{
				Allow:        true,
				UpdatedInput: HookPayload{"message": "rewritten"},
			}, nil
		},
	}, []MCPServerConfig{{
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
	result, err := registry.ExecuteDetailed(context.Background(), "mcp__fake__echo", `{"message":"original"}`)
	if err != nil {
		t.Fatalf("ExecuteDetailed echo: %v", err)
	}
	if observed != "original" {
		t.Fatalf("expected hook to observe original MCP arguments, got %q", observed)
	}
	if result.DisplayText != `{"echoed":"rewritten"}` {
		t.Fatalf("expected rewritten MCP arguments to execute, got %q", result.DisplayText)
	}
	if rewritten, _ := result.Meta["hook_rewritten"].(bool); !rewritten {
		t.Fatalf("expected hook_rewritten metadata, got %#v", result.Meta)
	}
	originalInput, ok := result.Meta["original_input"].(map[string]any)
	if !ok || stringsValueFromAny(originalInput["message"]) != "original" {
		t.Fatalf("expected original MCP input metadata, got %#v", result.Meta)
	}
}

func TestMCPToolPreToolUseDenialBlocksBeforeExecution(t *testing.T) {
	dir := t.TempDir()
	var observed string
	manager, warnings := LoadMCPManager(Workspace{
		BaseRoot: dir,
		Root:     dir,
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			if event != HookPreToolUse {
				return HookVerdict{Allow: true}, nil
			}
			if input, ok := payload["tool_input"].(map[string]any); ok {
				observed = stringsValueFromAny(input["message"])
			}
			return HookVerdict{
				Allow:      false,
				DenyReason: "blocked mcp echo",
			}, nil
		},
	}, []MCPServerConfig{{
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
	_, err := registry.ExecuteDetailed(context.Background(), "mcp__fake__echo", `{"message":"original"}`)
	if err == nil || !strings.Contains(err.Error(), "blocked mcp echo") {
		t.Fatalf("expected MCP pre hook denial, got %v", err)
	}
	if observed != "original" {
		t.Fatalf("expected hook to observe original MCP arguments, got %q", observed)
	}
}

func TestMCPToolPostToolUseHookRunsWithResponseAndFeedback(t *testing.T) {
	dir := t.TempDir()
	var postPayload HookPayload
	manager, warnings := LoadMCPManager(Workspace{
		BaseRoot: dir,
		Root:     dir,
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			switch event {
			case HookPreToolUse:
				return HookVerdict{Allow: true}, nil
			case HookPostToolUse:
				postPayload = payload
				return HookVerdict{
					Allow:      false,
					DenyReason: "redacted mcp output",
				}, nil
			default:
				return HookVerdict{Allow: true}, nil
			}
		},
	}, []MCPServerConfig{{
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
	ctx := contextWithToolCallHookMetadata(context.Background(), ToolCall{
		ID:   "call-mcp-echo",
		Name: "mcp__fake__echo",
	})
	result, err := registry.ExecuteDetailed(ctx, "mcp__fake__echo", `{"message":"hello"}`)
	if err != nil {
		t.Fatalf("ExecuteDetailed echo: %v", err)
	}
	if result.DisplayText != "redacted mcp output" {
		t.Fatalf("expected post hook feedback to replace MCP display text, got %q", result.DisplayText)
	}
	if got := toolExecutionModelText(result); got != "redacted mcp output" {
		t.Fatalf("expected post hook feedback as model text, got %q", got)
	}
	if got := stringsValueFromAny(postPayload["tool_name"]); got != "mcp__fake__echo" {
		t.Fatalf("expected post hook MCP tool name, got %#v", postPayload)
	}
	if got := stringsValueFromAny(postPayload["tool_use_id"]); got != "call-mcp-echo" {
		t.Fatalf("expected post hook tool_use_id, got %#v", postPayload)
	}
	if got := stringsValueFromAny(postPayload["tool_kind"]); got != "mcp" {
		t.Fatalf("expected MCP tool_kind, got %#v", postPayload)
	}
	if got := stringsValueFromAny(postPayload["mcp_server"]); got != "fake" {
		t.Fatalf("expected MCP server metadata, got %#v", postPayload)
	}
	if response := stringsValueFromAny(postPayload["tool_response"]); !strings.Contains(response, `"echoed":"hello"`) {
		t.Fatalf("expected post hook response to include MCP output, got %#v", postPayload)
	}
	if got := stringsValueFromAny(result.Meta["post_tool_use_hook_feedback"]); got != "redacted mcp output" {
		t.Fatalf("expected post hook feedback metadata, got %#v", result.Meta)
	}
	if stopped, _ := result.Meta["post_tool_use_hook_stopped"].(bool); !stopped {
		t.Fatalf("expected post hook stopped metadata, got %#v", result.Meta)
	}
}

func TestMCPResourceToolRunsDefaultFunctionHooks(t *testing.T) {
	dir := t.TempDir()
	var events []HookEvent
	var prePayload HookPayload
	var postPayload HookPayload
	manager, warnings := LoadMCPManager(Workspace{
		BaseRoot: dir,
		Root:     dir,
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			events = append(events, event)
			switch event {
			case HookPreToolUse:
				prePayload = payload
			case HookPostToolUse:
				postPayload = payload
			}
			return HookVerdict{Allow: true}, nil
		},
	}, []MCPServerConfig{{
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
	ctx := contextWithToolCallHookMetadata(context.Background(), ToolCall{
		ID:   "call-mcp-resource",
		Name: "mcp__resource__fake",
	})
	result, err := registry.ExecuteDetailed(ctx, "mcp__resource__fake", `{"uri":"memo://project"}`)
	if err != nil {
		t.Fatalf("ExecuteDetailed resource: %v", err)
	}
	if !strings.Contains(result.DisplayText, "resource body") {
		t.Fatalf("unexpected resource output: %q", result.DisplayText)
	}
	eventNames := make([]string, len(events))
	for index, event := range events {
		eventNames[index] = string(event)
	}
	if strings.Join(eventNames, ",") != "PreToolUse,PostToolUse" {
		t.Fatalf("expected pre and post hooks for MCP resource tool, got %#v", events)
	}
	if got := stringsValueFromAny(prePayload["tool_name"]); got != "mcp__resource__fake" {
		t.Fatalf("expected resource pre hook tool name, got %#v", prePayload)
	}
	if got := stringsValueFromAny(prePayload["tool_use_id"]); got != "call-mcp-resource" {
		t.Fatalf("expected resource pre hook tool_use_id, got %#v", prePayload)
	}
	if response := stringsValueFromAny(postPayload["tool_response"]); !strings.Contains(response, "resource body") {
		t.Fatalf("expected resource post hook response, got %#v", postPayload)
	}
}

func TestMCPToolCallIncludesTurnMetadataRequestMeta(t *testing.T) {
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

	ctx := contextWithMCPTurnMetadata(context.Background(), map[string]any{
		"user_input_requested_during_turn": true,
	})
	registry := NewToolRegistry(manager.Tools()...)
	result, err := registry.ExecuteDetailed(ctx, "mcp__fake__echo", `{"message":"meta"}`)
	if err != nil {
		t.Fatalf("ExecuteDetailed echo: %v", err)
	}
	structured, ok := result.Meta["mcp_result_structured_content"].(map[string]any)
	if !ok {
		t.Fatalf("expected structuredContent metadata, got %#v", result.Meta["mcp_result_structured_content"])
	}
	requestMeta, ok := structured["request_meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected helper to receive MCP request _meta, got %#v", structured)
	}
	callID, ok := requestMeta[mcpBridgeCallIDMetaKey].(string)
	if !ok || !strings.HasPrefix(callID, "mcp-") {
		t.Fatalf("expected MCP bridge call id metadata, got %#v in %#v", requestMeta[mcpBridgeCallIDMetaKey], requestMeta)
	}
	if got := result.Meta["mcp_call_id"]; got != callID {
		t.Fatalf("expected result mcp_call_id %q, got %#v in %#v", callID, got, result.Meta)
	}
	turnMeta, ok := requestMeta[mcpTurnMetadataMetaKey].(map[string]any)
	if !ok {
		t.Fatalf("expected %s metadata, got %#v", mcpTurnMetadataMetaKey, requestMeta)
	}
	if got := turnMeta["user_input_requested_during_turn"]; got != true {
		t.Fatalf("expected user input turn marker to be true, got %#v in %#v", got, turnMeta)
	}
}

func TestMCPConversationHistorySnapshotOmitsToolMeta(t *testing.T) {
	history := mcpConversationHistorySnapshot([]Message{
		{
			Role:       "tool",
			ToolCallID: "call-1",
			ToolName:   "mcp__fake__echo",
			Text:       "echo",
			ToolMeta: map[string]any{
				"mcp_result_structured_content": map[string]any{
					"request_meta": map[string]any{
						mcpConversationHistoryMetaKey: "recursive",
					},
				},
			},
		},
	})
	items, ok := history["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected one history item, got %#v", history)
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected history item object, got %#v", items[0])
	}
	if _, ok := item["tool_meta"]; ok {
		t.Fatalf("conversation history must not include local tool metadata: %#v", item)
	}
	if item["role"] != "tool" || item["tool_call_id"] != "call-1" {
		t.Fatalf("expected tool identity to remain, got %#v", item)
	}
}

func TestAgentMCPToolCallCarriesTurnMetadata(t *testing.T) {
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

	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("mcp__fake__echo", map[string]any{"message": "meta"}),
			{Message: Message{Role: "assistant", Text: "done"}},
		},
	}
	session := NewSession(dir, "scripted", "model-a", "high", "default")
	agent := &Agent{
		Config: Config{
			Model:           "model-a",
			ReasoningEffort: "high",
			ServiceTier:     "flex",
			AutoLocale:      boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(manager.Tools()...),
		Workspace: Workspace{BaseRoot: dir, Root: dir},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(dir, "sessions")),
	}

	if _, err := agent.Reply(context.Background(), "call the MCP echo tool"); err != nil {
		t.Fatalf("Reply: %v", err)
	}

	var toolMsg *Message
	for i := range session.Messages {
		if session.Messages[i].Role == "tool" && session.Messages[i].ToolName == "mcp__fake__echo" {
			toolMsg = &session.Messages[i]
			break
		}
	}
	if toolMsg == nil {
		t.Fatalf("expected MCP tool result in session messages: %#v", session.Messages)
	}
	structured, ok := toolMsg.ToolMeta["mcp_result_structured_content"].(map[string]any)
	if !ok {
		t.Fatalf("expected MCP structuredContent metadata, got %#v", toolMsg.ToolMeta)
	}
	requestMeta, ok := structured["request_meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected MCP request _meta, got %#v", structured)
	}
	turnMeta, ok := requestMeta[mcpTurnMetadataMetaKey].(map[string]any)
	if !ok {
		t.Fatalf("expected %s metadata, got %#v", mcpTurnMetadataMetaKey, requestMeta)
	}
	if got := turnMeta["model"]; got != "model-a" {
		t.Fatalf("expected model metadata, got %#v in %#v", got, turnMeta)
	}
	if got := turnMeta["reasoning_effort"]; got != "high" {
		t.Fatalf("expected reasoning effort metadata, got %#v in %#v", got, turnMeta)
	}
	if got := turnMeta["service_tier"]; got != "flex" {
		t.Fatalf("expected service tier metadata, got %#v in %#v", got, turnMeta)
	}
	if got := turnMeta["provider"]; got != "scripted" {
		t.Fatalf("expected provider metadata, got %#v in %#v", got, turnMeta)
	}
	if got := turnMeta["session_id"]; got != session.ID {
		t.Fatalf("expected session id metadata %q, got %#v in %#v", session.ID, got, turnMeta)
	}
	if got := turnMeta["thread_id"]; got != session.ID {
		t.Fatalf("expected thread id metadata %q, got %#v in %#v", session.ID, got, turnMeta)
	}
	if got := turnMeta["thread_source"]; got != "user" {
		t.Fatalf("expected user thread source metadata, got %#v in %#v", got, turnMeta)
	}
	if got, ok := turnMeta["turn_id"].(string); !ok || !strings.HasPrefix(got, session.ID+":") {
		t.Fatalf("expected turn id to be scoped to session %q, got %#v in %#v", session.ID, turnMeta["turn_id"], turnMeta)
	}
	if got, ok := turnMeta["trace_id"].(string); !ok || !isLowerHex32(got) {
		t.Fatalf("expected trace id to be scoped to session %q, got %#v in %#v", session.ID, turnMeta["trace_id"], turnMeta)
	}
	if got, ok := turnMeta["turn_started_at_unix_ms"].(float64); !ok || got <= 0 {
		t.Fatalf("expected positive turn start metadata, got %#v in %#v", turnMeta["turn_started_at_unix_ms"], turnMeta)
	}
	if got := turnMeta["permission_mode"]; got != "default" {
		t.Fatalf("expected permission mode metadata, got %#v in %#v", got, turnMeta)
	}
	if got := turnMeta["active_permission_profile_id"]; got != builtInPermissionProfileWorkspace {
		t.Fatalf("expected active permission profile id metadata, got %#v in %#v", got, turnMeta)
	}
	activeProfile, ok := turnMeta["active_permission_profile"].(map[string]any)
	if !ok || activeProfile["id"] != builtInPermissionProfileWorkspace {
		t.Fatalf("expected active permission profile snapshot metadata, got %#v in %#v", turnMeta["active_permission_profile"], turnMeta)
	}
	if got := turnMeta["sandbox"]; got != "none" {
		t.Fatalf("expected sandbox metadata to mirror Codex no-platform-sandbox tag, got %#v in %#v", got, turnMeta)
	}
	if got := turnMeta["cwd"]; got != dir {
		t.Fatalf("expected cwd metadata %q, got %#v in %#v", dir, got, turnMeta)
	}
	if got := turnMeta["workspace_root"]; got != dir {
		t.Fatalf("expected workspace root metadata %q, got %#v in %#v", dir, got, turnMeta)
	}
	roots, ok := turnMeta["workspace_roots"].([]any)
	if !ok || len(roots) != 1 || roots[0] != dir {
		t.Fatalf("expected workspace_roots metadata [%q], got %#v in %#v", dir, turnMeta["workspace_roots"], turnMeta)
	}
	if _, ok := turnMeta["active_workspace_root"]; ok {
		t.Fatalf("did not expect active_workspace_root when cwd equals workspace root: %#v", turnMeta)
	}
	history, ok := requestMeta[mcpConversationHistoryMetaKey].(map[string]any)
	if !ok {
		t.Fatalf("expected %s metadata, got %#v", mcpConversationHistoryMetaKey, requestMeta)
	}
	items, ok := history["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected non-empty conversation history items, got %#v", history)
	}
	sawUserRequest := false
	sawAssistantToolCall := false
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if item["role"] == "user" && strings.Contains(fmt.Sprint(item["text"]), "call the MCP echo tool") {
			sawUserRequest = true
		}
		if item["role"] == "assistant" {
			if calls, ok := item["tool_calls"].([]any); ok && len(calls) > 0 {
				sawAssistantToolCall = true
			}
		}
	}
	if !sawUserRequest {
		t.Fatalf("expected conversation history to include current user request, got %#v", history)
	}
	if !sawAssistantToolCall {
		t.Fatalf("expected conversation history to include assistant tool call, got %#v", history)
	}
}

func TestProviderTurnMetadataFromMCPStripsModelRoutingFields(t *testing.T) {
	metadata := map[string]any{
		"provider":         "openai-codex",
		"model":            "gpt-5.5",
		"reasoning_effort": "high",
		"service_tier":     "flex",
		"turn_id":          "turn-123",
	}
	got := providerTurnMetadataFromMCP(metadata)
	if got["turn_id"] != "turn-123" {
		t.Fatalf("expected turn metadata to keep non-routing keys, got %#v", got)
	}
	for _, key := range []string{"provider", "model", "reasoning_effort", "service_tier"} {
		if _, exists := got[key]; exists {
			t.Fatalf("provider turn metadata should strip %s, got %#v", key, got)
		}
	}
}

func TestAgentMCPTurnMetadataDistinguishesActiveWorkspaceRoot(t *testing.T) {
	baseRoot := t.TempDir()
	activeRoot := filepath.Join(baseRoot, "worktrees", "feature")
	agent := &Agent{
		Config: Config{
			ReasoningEffort: "medium",
		},
		Workspace: Workspace{BaseRoot: baseRoot, Root: activeRoot},
		Session:   NewSession(baseRoot, "scripted", "model-a", "", "full-access"),
	}

	startedAt := time.UnixMilli(1_700_000_000_123)
	turnMeta := agent.mcpTurnMetadataForToolCall(startedAt)
	if got := turnMeta["turn_started_at_unix_ms"]; got != int64(1_700_000_000_123) {
		t.Fatalf("expected deterministic turn start metadata, got %#v in %#v", got, turnMeta)
	}
	if got := turnMeta["turn_id"]; got != agent.Session.ID+":1700000000123000000" {
		t.Fatalf("expected deterministic turn id metadata, got %#v in %#v", got, turnMeta)
	}
	if got := turnMeta["trace_id"]; got != mcpTurnMetadataTraceID(agent.Session.ID, startedAt) {
		t.Fatalf("expected deterministic trace id metadata, got %#v in %#v", got, turnMeta)
	}
	if got := turnMeta["cwd"]; got != activeRoot {
		t.Fatalf("expected cwd to use active workspace root %q, got %#v in %#v", activeRoot, got, turnMeta)
	}
	if got := turnMeta["workspace_root"]; got != baseRoot {
		t.Fatalf("expected workspace root metadata %q, got %#v in %#v", baseRoot, got, turnMeta)
	}
	if got := turnMeta["active_workspace_root"]; got != activeRoot {
		t.Fatalf("expected active workspace root metadata %q, got %#v in %#v", activeRoot, got, turnMeta)
	}
	roots, ok := turnMeta["workspace_roots"].([]string)
	if !ok || len(roots) != 2 || roots[0] != baseRoot || roots[1] != activeRoot {
		t.Fatalf("expected workspace_roots to preserve base and active roots, got %#v in %#v", turnMeta["workspace_roots"], turnMeta)
	}
}

func isLowerHex32(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, ch := range value {
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch >= 'a' && ch <= 'f' {
			continue
		}
		return false
	}
	return true
}

func TestWorkspaceEffectiveRootsCanonicalizesAndDeduplicates(t *testing.T) {
	tempRoot := t.TempDir()
	baseRoot := filepath.Join(tempRoot, "repo")
	if err := os.MkdirAll(baseRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	dirtyBaseRoot := filepath.Join(tempRoot, ".", "repo")
	dirtyActiveRoot := filepath.Join(tempRoot, "repo", "worktrees", "..")

	roots := workspaceEffectiveRoots(Workspace{BaseRoot: dirtyBaseRoot, Root: dirtyActiveRoot}, nil)
	if len(roots) != 1 || roots[0] != filepath.Clean(baseRoot) {
		t.Fatalf("expected one canonical workspace root %q, got %#v", filepath.Clean(baseRoot), roots)
	}
}

func TestAgentMCPTurnMetadataUsesLivePermissionSnapshot(t *testing.T) {
	root := t.TempDir()
	agent := &Agent{
		Workspace: Workspace{
			BaseRoot: root,
			Root:     root,
			Perms:    NewPermissionManager(ModePlan, nil),
		},
		Session: NewSession(root, "scripted", "model-a", "", string(ModeBypass)),
	}

	turnMeta := agent.mcpTurnMetadataForToolCall(time.UnixMilli(1_700_000_000_123))
	if got := turnMeta["permission_mode"]; got != string(ModePlan) {
		t.Fatalf("expected live permission mode metadata %q, got %#v in %#v", ModePlan, got, turnMeta)
	}
	if got := turnMeta["active_permission_profile_id"]; got != builtInPermissionProfileReadOnly {
		t.Fatalf("expected live permission profile id metadata %q, got %#v in %#v", builtInPermissionProfileReadOnly, got, turnMeta)
	}
	if got := turnMeta["sandbox"]; got != "none" {
		t.Fatalf("expected sandbox metadata from live permission mode, got %#v in %#v", got, turnMeta)
	}
}

func TestAgentMCPTurnMetadataIncludesWorkspaceGitMetadata(t *testing.T) {
	repo := t.TempDir()
	mustRunGit(t, repo, "init")
	mustRunGit(t, repo, "config", "user.email", "test@example.com")
	mustRunGit(t, repo, "config", "user.name", "Test User")
	mustRunGit(t, repo, "config", "core.autocrlf", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustRunGit(t, repo, "add", "README.md")
	mustRunGit(t, repo, "commit", "-m", "initial")
	head := strings.TrimSpace(mustRunGit(t, repo, "rev-parse", "HEAD"))
	repoRoot := strings.TrimSpace(mustRunGit(t, repo, "rev-parse", "--show-toplevel"))

	agent := &Agent{
		Config:    Config{ReasoningEffort: "medium"},
		Workspace: Workspace{BaseRoot: repo, Root: repo},
		Session:   NewSession(repo, "scripted", "model-a", "", "default"),
	}

	turnMeta := agent.mcpTurnMetadataForToolCall(time.UnixMilli(1_700_000_000_123))
	workspace := metadataWorkspaceForPath(t, turnMeta, repoRoot)
	if got := workspace["latest_git_commit_hash"]; got != head {
		t.Fatalf("expected HEAD metadata %q, got %#v in %#v", head, got, workspace)
	}
	if got := workspace["has_changes"]; got != false {
		t.Fatalf("expected clean repo has_changes=false, got %#v in %#v", got, workspace)
	}

	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	dirtyMeta := agent.mcpTurnMetadataForToolCall(time.UnixMilli(1_700_000_000_124))
	dirtyWorkspace := metadataWorkspaceForPath(t, dirtyMeta, repoRoot)
	if got := dirtyWorkspace["has_changes"]; got != true {
		t.Fatalf("expected dirty repo has_changes=true, got %#v in %#v", got, dirtyWorkspace)
	}
}

func TestGitRepositoryRootFromFilesystemFindsAncestor(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	nested := filepath.Join(repo, "src", "pkg")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	filePath := filepath.Join(nested, "main.go")
	if err := os.WriteFile(filePath, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write nested file: %v", err)
	}
	if got := gitRepositoryRootFromFilesystem(filePath); !samePath(got, repo) {
		t.Fatalf("expected repo root %q, got %q", repo, got)
	}
}

func metadataWorkspaceForPath(t *testing.T, turnMeta map[string]any, path string) map[string]any {
	t.Helper()
	workspaces, ok := turnMeta["workspaces"].(map[string]any)
	if !ok || len(workspaces) == 0 {
		t.Fatalf("expected workspaces metadata, got %#v in %#v", turnMeta["workspaces"], turnMeta)
	}
	for root, raw := range workspaces {
		if !samePath(root, path) {
			continue
		}
		workspace, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("expected workspace metadata map for %q, got %#v", root, raw)
		}
		return workspace
	}
	t.Fatalf("expected workspace metadata for %q, got %#v", path, workspaces)
	return nil
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

func TestMCPToolModelOutputTruncatesLargeStructuredContentLikeCodex(t *testing.T) {
	result := map[string]any{
		"structuredContent": map[string]any{
			"items": strings.Repeat("large structured value ", 1000),
		},
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "ignored when structured content is present",
			},
		},
	}

	modelText, items := mcpToolModelOutput(result, 1250*time.Millisecond)
	if len(items) != 0 {
		t.Fatalf("large structured MCP result should remain text output, got %#v", items)
	}
	if !strings.HasPrefix(modelText, "Wall time: 1.2500 seconds\nOutput:\n") {
		t.Fatalf("missing wall-time prefix: %q", modelText)
	}
	if !strings.Contains(modelText, "chars truncated") {
		t.Fatalf("expected large MCP output to be truncated, got length=%d", len(modelText))
	}
	if strings.Contains(modelText, "ignored when structured content is present") {
		t.Fatalf("structured content should take precedence over content array: %q", modelText)
	}
	if len(modelText) > mcpToolModelOutputMaxBytes+128 {
		t.Fatalf("truncated MCP output still too large: %d", len(modelText))
	}
}

func TestMCPToolModelOutputTruncatesTextItemsAndPreservesImagesLikeCodex(t *testing.T) {
	result := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": strings.Repeat("text payload ", 1500),
			},
			map[string]any{
				"type":     "image",
				"data":     "AAA",
				"mimeType": "image/png",
			},
		},
	}

	modelText, items := mcpToolModelOutput(result, 500*time.Millisecond)
	if modelText != "Wall time: 0.5000 seconds\nOutput:" {
		t.Fatalf("unexpected model text: %q", modelText)
	}
	if len(items) != 3 {
		t.Fatalf("expected header, truncated text, and image items, got %#v", items)
	}
	if !strings.Contains(items[1].Text, "chars truncated") {
		t.Fatalf("expected text content item to be truncated, got length=%d", len(items[1].Text))
	}
	if items[2].Type != "input_image" || items[2].ImageURL != "data:image/png;base64,AAA" {
		t.Fatalf("expected image item to be preserved, got %#v", items[2])
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

	echoTool := requireMCPToolByName(t, manager, "mcp__env__echo_env")

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

func TestBuildMCPProcessEnvForwardsManagedProxyEnvAndEnablesNodeProxyOptIn(t *testing.T) {
	t.Setenv("CODEX_NETWORK_PROXY_ACTIVE", "1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:3128")
	t.Setenv("NO_PROXY", "localhost,127.0.0.1")

	envList, err := buildMCPProcessEnv(MCPServerConfig{})
	if err != nil {
		t.Fatalf("build MCP process env: %v", err)
	}
	env := splitProcessEnv(envList)
	if env["HTTP_PROXY"] != "http://127.0.0.1:3128" {
		t.Fatalf("expected HTTP_PROXY to be forwarded, got %#v", env)
	}
	if env["CODEX_NETWORK_PROXY_ACTIVE"] != "1" {
		t.Fatalf("expected managed proxy marker to be forwarded, got %#v", env)
	}
	if env["NO_PROXY"] != "localhost,127.0.0.1" {
		t.Fatalf("expected NO_PROXY to be forwarded, got %#v", env)
	}
	if env["NODE_USE_ENV_PROXY"] != "1" {
		t.Fatalf("expected NODE_USE_ENV_PROXY opt-in for proxied MCP child, got %#v", env)
	}
}

func TestBuildMCPProcessEnvPreservesExplicitNodeProxyOptIn(t *testing.T) {
	t.Setenv("CODEX_NETWORK_PROXY_ACTIVE", "1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:3128")
	t.Setenv("NODE_USE_ENV_PROXY", "0")

	envList, err := buildMCPProcessEnv(MCPServerConfig{})
	if err != nil {
		t.Fatalf("build MCP process env: %v", err)
	}
	env := splitProcessEnv(envList)
	if env["NODE_USE_ENV_PROXY"] != "0" {
		t.Fatalf("expected explicit NODE_USE_ENV_PROXY to be preserved, got %#v", env)
	}
}

func TestBuildMCPProcessEnvDoesNotForwardAmbientProxyWithoutManagedProxy(t *testing.T) {
	t.Setenv("CODEX_NETWORK_PROXY_ACTIVE", "")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:3128")

	envList, err := buildMCPProcessEnv(MCPServerConfig{})
	if err != nil {
		t.Fatalf("build MCP process env: %v", err)
	}
	env := splitProcessEnv(envList)
	if _, ok := env["HTTP_PROXY"]; ok {
		t.Fatalf("ambient HTTP_PROXY should not be forwarded without managed proxy marker, got %#v", env)
	}
	if _, ok := env["NODE_USE_ENV_PROXY"]; ok {
		t.Fatalf("NODE_USE_ENV_PROXY should not be added without forwarded proxy, got %#v", env)
	}
}

func TestBuildMCPProcessEnvEnablesNodeProxyOptInForConfiguredProxy(t *testing.T) {
	t.Setenv("CODEX_NETWORK_PROXY_ACTIVE", "")

	envList, err := buildMCPProcessEnv(MCPServerConfig{
		Env: map[string]string{
			"HTTPS_PROXY": "http://127.0.0.1:3128",
		},
	})
	if err != nil {
		t.Fatalf("build MCP process env: %v", err)
	}
	env := splitProcessEnv(envList)
	if env["HTTPS_PROXY"] != "http://127.0.0.1:3128" {
		t.Fatalf("expected configured HTTPS_PROXY, got %#v", env)
	}
	if env["NODE_USE_ENV_PROXY"] != "1" {
		t.Fatalf("expected NODE_USE_ENV_PROXY opt-in for configured proxy, got %#v", env)
	}
}

func TestLoadMCPManagerUsesCoreEnvAndRequestedEnvVars(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KERNFORGE_MCP_REQUESTED_ENV", "requested")
	t.Setenv("KERNFORGE_MCP_UNREQUESTED_ENV", "hidden")

	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name:    "env",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPEnvHelperProcess"},
		Env: map[string]string{
			"KERNFORGE_MCP_ENV_HELPER": "1",
		},
		EnvVars: []MCPServerEnvVar{
			{Name: "KERNFORGE_MCP_REQUESTED_ENV"},
		},
	}})
	defer manager.Close()

	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	echoTool := requireMCPToolByName(t, manager, "mcp__env__echo_env")

	requestedValue, err := echoTool.Execute(context.Background(), map[string]any{"name": "KERNFORGE_MCP_REQUESTED_ENV"})
	if err != nil {
		t.Fatalf("Execute requested env: %v", err)
	}
	if requestedValue != "requested" {
		t.Fatalf("expected requested env var to be forwarded, got %q", requestedValue)
	}

	unrequestedValue, err := echoTool.Execute(context.Background(), map[string]any{"name": "KERNFORGE_MCP_UNREQUESTED_ENV"})
	if err != nil {
		t.Fatalf("Execute unrequested env: %v", err)
	}
	if unrequestedValue != "__MISSING__" {
		t.Fatalf("expected unrequested env var to be withheld, got %q", unrequestedValue)
	}
}

func TestLoadMCPManagerRejectsRemoteSourceEnvVarsForLocalStdio(t *testing.T) {
	dir := t.TempDir()
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name:    "env",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPEnvHelperProcess"},
		Env: map[string]string{
			"KERNFORGE_MCP_ENV_HELPER": "1",
		},
		EnvVars: []MCPServerEnvVar{
			{Name: "REMOTE_TOKEN", Source: "remote"},
		},
	}})
	defer manager.Close()

	if len(warnings) != 1 || !strings.Contains(warnings[0], `source "remote"`) {
		t.Fatalf("expected remote env_vars warning, got %#v", warnings)
	}
	statuses := manager.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 failed status, got %d", len(statuses))
	}
	if statuses[0].ToolCount != 0 {
		t.Fatalf("expected failed MCP server without tools, got %#v", statuses[0])
	}
	if !strings.Contains(statuses[0].Error, `source "remote"`) {
		t.Fatalf("expected remote env_vars status error, got %#v", statuses[0])
	}
}

func requireMCPToolByName(t *testing.T, manager *MCPManager, name string) Tool {
	t.Helper()
	for _, tool := range manager.Tools() {
		if tool.Definition().Name == name {
			return tool
		}
	}
	t.Fatalf("expected MCP tool %q", name)
	return nil
}

func splitProcessEnv(values []string) map[string]string {
	out := map[string]string{}
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		out[key] = val
	}
	return out
}
