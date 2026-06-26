package main

import "testing"

// A user-connected MCP tool must bypass both intent-inference gates (web-research
// deferral and read-only analysis), while the guards for plain local tools and
// local edits stay intact.
func TestMCPToolsBypassIntentGates(t *testing.T) {
	mcpCall := ToolCall{Name: "mcp__knlivedbg__ti_subscribe"}

	if !toolCallAllowedBeforeWebResearch(mcpCall, nil) {
		t.Fatalf("an MCP tool must not be deferred behind web research")
	}
	if !toolCallAllowedInReadOnlyAnalysis(mcpCall) {
		t.Fatalf("an MCP tool must be allowed in a read-only analysis turn")
	}

	// Guards intact: a plain local tool still defers to web research...
	if toolCallAllowedBeforeWebResearch(ToolCall{Name: "read_file"}, nil) {
		t.Fatalf("a plain local tool must still defer to web research")
	}
	// ...a local edit tool stays blocked in read-only analysis...
	if toolCallAllowedInReadOnlyAnalysis(ToolCall{Name: "write_file"}) {
		t.Fatalf("a local edit tool must stay blocked in read-only analysis")
	}
	// ...and read-only inspection stays allowed.
	if !toolCallAllowedInReadOnlyAnalysis(ToolCall{Name: "read_file"}) {
		t.Fatalf("read_file must remain allowed in read-only analysis")
	}
}

func TestIsMCPToolName(t *testing.T) {
	if !isMCPToolName("mcp__web_research__search_web") {
		t.Fatalf("expected mcp__ prefix to be detected")
	}
	if isMCPToolName("read_file") || isMCPToolName("") {
		t.Fatalf("non-MCP names must not be detected as MCP tools")
	}
}
