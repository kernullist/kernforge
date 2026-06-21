package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ToolSearchTool is a built-in tool the model can call to discover MCP tools
// whose full input schema was not loaded up front (deferred tools). It runs a
// bounded live tools/list against the matching servers, caches the returned
// schemas on the owning client, and reports each tool's namespaced name,
// description, and full input schema so the model can then call the tool with
// valid arguments. It is registered only when at least one MCP server is loaded.
type ToolSearchTool struct {
	mcp *MCPManager
}

func NewToolSearchTool(mcp *MCPManager) ToolSearchTool {
	return ToolSearchTool{mcp: mcp}
}

// toolSearchToolAvailable reports whether the built-in tool_search tool should
// be registered. It is only useful when there is at least one MCP server to
// search; without one the tool would always return an empty result.
func toolSearchToolAvailable(mcp *MCPManager) bool {
	return mcp != nil && len(mcp.servers) > 0
}

func (t ToolSearchTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "tool_search",
		Description: "Searches over deferred MCP tool metadata and exposes matching tools for the next model call. " +
			"Use this to discover tools whose argument schema was not loaded up front: it fetches and returns the full input schema for matching tools so you can call them. " +
			"Filter by server_name and/or tool_name_filter (case-insensitive substring of the remote tool name).",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"server_name": map[string]any{
					"type":        "string",
					"description": "Optional MCP server name to restrict the search to one server.",
				},
				"tool_name_filter": map[string]any{
					"type":        "string",
					"description": "Optional case-insensitive substring matched against the remote tool name. Empty returns all tools.",
				},
			},
			"required": []string{},
		},
	}
}

// ReadOnlyToolCall reports that searching tool metadata performs no mutation, so
// the agent may run it without a write gate and in parallel with other reads.
func (t ToolSearchTool) ReadOnlyToolCall() bool {
	return true
}

func (t ToolSearchTool) SupportsParallelToolCalls() bool {
	return true
}

func (t ToolSearchTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	serverName := strings.TrimSpace(stringValue(args, "server_name"))
	toolFilter := strings.TrimSpace(stringValue(args, "tool_name_filter"))
	hits, searchErr := t.mcp.ToolSearch(ctx, serverName, toolFilter)
	payload := map[string]any{
		"tools": toolSearchHitPayloads(hits),
		"count": len(hits),
	}
	if searchErr != nil {
		payload["error"] = searchErr.Error()
	}
	data, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return "", marshalErr
	}
	if len(hits) == 0 && searchErr != nil {
		return string(data), fmt.Errorf("tool_search failed: %w", searchErr)
	}
	return string(data), nil
}

func toolSearchHitPayloads(hits []MCPToolSearchHit) []map[string]any {
	out := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		item := map[string]any{
			"name":        hit.Name,
			"server":      hit.Server,
			"description": hit.Description,
			"deferred":    hit.Deferred,
		}
		if len(hit.InputSchema) > 0 {
			item["input_schema"] = hit.InputSchema
		}
		out = append(out, item)
	}
	return out
}
