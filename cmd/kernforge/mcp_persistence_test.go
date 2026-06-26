package main

import (
	"path/filepath"
	"testing"
)

// A full-config save must not rewrite mcp_servers from the in-memory Config; the
// file's own servers (owned by /mcp add|remove) survive a settings change.
func TestSaveUserConfigPreservesFileMCPServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := saveConfigFileMCPServers(path, []MCPServerConfig{{Name: "knlivedbg", URL: "http://x"}}); err != nil {
		t.Fatalf("seed mcp_servers: %v", err)
	}

	// In-memory MCPServers differ (e.g. a merged/backfilled value); the save must
	// keep the file's mcp_servers untouched.
	cfg := Config{Provider: "anthropic", MCPServers: []MCPServerConfig{{Name: "other", Command: "z"}}}
	if err := saveConfigWithOptions(cfg, saveUserConfigOptions{}, path); err != nil {
		t.Fatalf("save full config: %v", err)
	}

	servers, err := loadConfigFileMCPServers(path)
	if err != nil {
		t.Fatalf("reload mcp_servers: %v", err)
	}
	if len(servers) != 1 || servers[0].Name != "knlivedbg" {
		t.Fatalf("full-config save must preserve the file's mcp_servers, got %#v", servers)
	}
}
