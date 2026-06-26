package main

import (
	"path/filepath"
	"testing"
)

// /perm <mode> sets cfg.PermissionMode and calls SaveUserConfig; verify that path
// actually persists the mode into the user config file, overwriting a prior value.
func TestSaveUserConfigPersistsPermissionMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := saveConfigFileOverrides(path, map[string]any{"permission_mode": "plan", "provider": "anthropic"}); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	cfg := Config{Provider: "anthropic", PermissionMode: "full"}
	if err := saveConfigWithOptions(cfg, saveUserConfigOptions{PreserveActiveProfileKey: true, PreserveReviewRoleModels: true}, path); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := loadRawConfigFile(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded.PermissionMode != "full" {
		t.Fatalf("permission_mode must persist as full, got %q", loaded.PermissionMode)
	}
}
