package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
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
