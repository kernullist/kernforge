package main

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed .kernforge/mcp/web-research-mcp.js
var bundledWebResearchMCPScript []byte

func deployedWebResearchMCPScriptPath() string {
	return filepath.Join(userConfigDir(), "mcp", "web-research-mcp.js")
}

func deployedWebResearchMCPScriptAvailable() bool {
	info, err := os.Stat(deployedWebResearchMCPScriptPath())
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func ensureBundledUserAssets() error {
	if len(bundledWebResearchMCPScript) == 0 {
		return nil
	}
	return ensureManagedUserFile(deployedWebResearchMCPScriptPath(), bundledWebResearchMCPScript, 0o644)
}

func ensureManagedUserFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, data, mode)
}
