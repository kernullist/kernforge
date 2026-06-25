package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseMCPAddCommandStdio(t *testing.T) {
	req, err := parseMCPAddCommand([]string{"fs", "--", "npx", "-y", "@modelcontextprotocol/server-filesystem", "F:/kernullist"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Server.Name != "fs" {
		t.Fatalf("name=%q", req.Server.Name)
	}
	if req.Server.Command != "npx" {
		t.Fatalf("command=%q", req.Server.Command)
	}
	wantArgs := []string{"-y", "@modelcontextprotocol/server-filesystem", "F:/kernullist"}
	if !reflect.DeepEqual(req.Server.Args, wantArgs) {
		t.Fatalf("args=%#v", req.Server.Args)
	}
	if req.Server.URL != "" {
		t.Fatalf("stdio server must not set url, got %q", req.Server.URL)
	}
	if !req.ScopeUser {
		t.Fatalf("default scope must be user")
	}
	if mcpServerTransport(req.Server) != "stdio" {
		t.Fatalf("transport=%q", mcpServerTransport(req.Server))
	}
}

func TestParseMCPAddCommandRemote(t *testing.T) {
	req, err := parseMCPAddCommand([]string{"api", "--url", "https://mcp.example.com/sse", "--bearer-env", "API_TOKEN", "--header", "X-Org=ironmace", "--workspace"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Server.URL != "https://mcp.example.com/sse" {
		t.Fatalf("url=%q", req.Server.URL)
	}
	if req.Server.BearerTokenEnvVar != "API_TOKEN" {
		t.Fatalf("bearer=%q", req.Server.BearerTokenEnvVar)
	}
	if req.Server.HTTPHeaders["X-Org"] != "ironmace" {
		t.Fatalf("headers=%#v", req.Server.HTTPHeaders)
	}
	if req.ScopeUser {
		t.Fatalf("--workspace must set scope to workspace")
	}
	if mcpServerTransport(req.Server) != "streamable_http" {
		t.Fatalf("transport=%q", mcpServerTransport(req.Server))
	}
}

func TestParseMCPAddCommandFlags(t *testing.T) {
	req, err := parseMCPAddCommand([]string{"fs", "--env", "TOK=abc", "--cwd", ".", "--cap", "read_file", "--force", "--disabled", "--", "node", "server.js"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Server.Env["TOK"] != "abc" {
		t.Fatalf("env=%#v", req.Server.Env)
	}
	if req.Server.Cwd != "." {
		t.Fatalf("cwd=%q", req.Server.Cwd)
	}
	if len(req.Server.Capabilities) != 1 || req.Server.Capabilities[0] != "read_file" {
		t.Fatalf("caps=%#v", req.Server.Capabilities)
	}
	if !req.Force {
		t.Fatalf("--force must set Force")
	}
	if !req.Server.Disabled {
		t.Fatalf("--disabled must set Disabled")
	}
	if req.Server.Command != "node" || len(req.Server.Args) != 1 || req.Server.Args[0] != "server.js" {
		t.Fatalf("command/args wrong: %q %#v", req.Server.Command, req.Server.Args)
	}
}

func TestParseMCPAddCommandErrors(t *testing.T) {
	cases := map[string][]string{
		"no transport":      {"fs"},
		"both transports":   {"fs", "--url", "https://x/sse", "--", "node", "s.js"},
		"missing name":      {"--url", "https://x/sse"},
		"unknown option":    {"fs", "--bogus", "--", "node"},
		"remote with env":   {"api", "--url", "https://x/sse", "--env", "K=V"},
		"remote with cwd":   {"api", "--url", "https://x/sse", "--cwd", "."},
		"stdio with header": {"fs", "--header", "K=V", "--", "node"},
		"bad header":        {"api", "--url", "https://x/sse", "--header", "novalue"},
		"dangling dashdash": {"fs", "--"},
		"flag needs value":  {"fs", "--url"},
	}
	for label, args := range cases {
		if _, err := parseMCPAddCommand(args); err == nil {
			t.Fatalf("%s: expected error, got nil for args=%#v", label, args)
		}
	}
}

func TestReplaceOrAppendMCPServer(t *testing.T) {
	base := []MCPServerConfig{{Name: "a", Command: "x"}, {Name: "b", Command: "y"}}
	out := replaceOrAppendMCPServer(base, MCPServerConfig{Name: "c", Command: "z"})
	if len(out) != 3 || out[2].Name != "c" {
		t.Fatalf("append failed: %#v", out)
	}
	out = replaceOrAppendMCPServer(base, MCPServerConfig{Name: "B", Command: "new"})
	if len(out) != 2 {
		t.Fatalf("replace must not grow: %#v", out)
	}
	for _, s := range out {
		if strings.EqualFold(s.Name, "b") && s.Command != "new" {
			t.Fatalf("replace did not update matched server: %#v", out)
		}
	}
}

func TestRemoveMCPServerByName(t *testing.T) {
	base := []MCPServerConfig{{Name: "a"}, {Name: "b"}}
	out, removed := removeMCPServerByName(base, "A")
	if !removed || len(out) != 1 || out[0].Name != "b" {
		t.Fatalf("remove failed: removed=%v out=%#v", removed, out)
	}
	if _, removed := removeMCPServerByName(base, "zzz"); removed {
		t.Fatalf("removing a missing server must report false")
	}
}

func TestSetMCPServerDisabledByName(t *testing.T) {
	base := []MCPServerConfig{{Name: "a"}, {Name: "b"}}
	out, found := setMCPServerDisabledByName(base, "a", true)
	if !found {
		t.Fatalf("expected found")
	}
	if !out[0].Disabled || !out[0].DisabledSet {
		t.Fatalf("disabled not set: %#v", out[0])
	}
	if base[0].Disabled {
		t.Fatalf("input slice must not be mutated")
	}
}

// TestConfigFileMCPServersRoundTripPreservesOtherKeys locks the merge-leak guard:
// saving servers touches only the mcp_servers key of the targeted file and never
// rewrites the in-memory merged Config, so unrelated keys survive and no server
// from another layer is introduced.
func TestConfigFileMCPServersRoundTripPreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"provider":"openrouter","mcp_servers":[{"name":"old","command":"node","args":["old.js"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	existing, err := loadConfigFileMCPServers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(existing) != 1 || existing[0].Name != "old" {
		t.Fatalf("load failed: %#v", existing)
	}
	merged := replaceOrAppendMCPServer(existing, MCPServerConfig{Name: "fs", Command: "npx", Args: []string{"-y", "srv"}})
	if err := saveConfigFileMCPServers(path, merged); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["provider"]; !ok {
		t.Fatalf("unrelated key 'provider' was dropped: %s", data)
	}
	servers, err := loadConfigFileMCPServers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers after add, got %d: %#v", len(servers), servers)
	}
}

func TestLoadConfigFileMCPServersMissingFile(t *testing.T) {
	servers, err := loadConfigFileMCPServers(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("missing file must yield no servers, got %#v", servers)
	}
}
