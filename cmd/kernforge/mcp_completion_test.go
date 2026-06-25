package main

import (
	"slices"
	"strings"
	"testing"
)

// TestMCPSlashCompletionSubcommands locks tab-completion for the /mcp management
// commands: "/mcp " offers the subcommands, and the <name> argument of
// remove/enable/disable/auth completes against configured server names, while add
// (which takes a new name) does not.
func TestMCPSlashCompletionSubcommands(t *testing.T) {
	rt := &runtimeState{cfg: Config{MCPServers: []MCPServerConfig{
		{Name: "web-research", Command: "node", Args: []string{"x.js"}},
		{Name: "docs", URL: "https://docs.example.com/sse"},
	}}}

	subs, idx, ok := rt.slashArgumentSuggestions("mcp", nil, false)
	if !ok || idx != 0 {
		t.Fatalf("mcp first-level: ok=%v idx=%d", ok, idx)
	}
	for _, want := range []string{"add", "remove", "enable", "disable", "auth"} {
		if !slices.Contains(subs, want) {
			t.Fatalf("mcp first-level missing %q in %#v", want, subs)
		}
	}

	names, idx, ok := rt.slashArgumentSuggestions("mcp", []string{"remove"}, true)
	if !ok || idx != 1 {
		t.Fatalf("mcp remove names: ok=%v idx=%d", ok, idx)
	}
	if !slices.Contains(names, "web-research") || !slices.Contains(names, "docs") {
		t.Fatalf("mcp remove must suggest configured server names, got %#v", names)
	}

	for _, verb := range []string{"enable", "disable", "auth"} {
		got, _, ok := rt.slashArgumentSuggestions("mcp", []string{verb}, true)
		if !ok || !slices.Contains(got, "docs") {
			t.Fatalf("mcp %s must suggest server names, got ok=%v %#v", verb, ok, got)
		}
	}

	if _, _, ok := rt.slashArgumentSuggestions("mcp", []string{"add"}, true); ok {
		t.Fatalf("mcp add must not suggest existing server names")
	}
}

func TestConfiguredMCPServerNamesDedup(t *testing.T) {
	rt := &runtimeState{cfg: Config{MCPServers: []MCPServerConfig{
		{Name: "a"}, {Name: "A"}, {Command: "node"}, {URL: "https://h.example.com/x"},
	}}}
	names := rt.configuredMCPServerNames()
	count := map[string]int{}
	for _, n := range names {
		count[strings.ToLower(n)]++
	}
	if count["a"] != 1 {
		t.Fatalf("expected case-insensitive 'a' deduped to 1, got %d in %#v", count["a"], names)
	}
	if !slices.Contains(names, "node") {
		t.Fatalf("command-only server should derive name 'node', got %#v", names)
	}
	if !slices.Contains(names, "h.example.com") {
		t.Fatalf("url-only server should derive host name, got %#v", names)
	}
}
