package main

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed .kernforge/mcp/web-research-mcp.js
var bundledWebResearchMCPScript []byte

//go:embed .kernforge/skills/goal-to-slice-planner/SKILL.md
var bundledGoalToSlicePlannerSkill []byte

func deployedWebResearchMCPScriptPath() string {
	return filepath.Join(userConfigDir(), "mcp", "web-research-mcp.js")
}

func deployedGoalToSlicePlannerSkillPath() string {
	return filepath.Join(userConfigDir(), "skills", "goal-to-slice-planner", "SKILL.md")
}

func deployedWebResearchMCPScriptAvailable() bool {
	info, err := os.Stat(deployedWebResearchMCPScriptPath())
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func ensureBundledUserAssets() error {
	if len(bundledWebResearchMCPScript) > 0 {
		if err := ensureManagedUserFile(deployedWebResearchMCPScriptPath(), bundledWebResearchMCPScript, 0o644); err != nil {
			return err
		}
	}
	if len(bundledGoalToSlicePlannerSkill) > 0 {
		if err := ensureSeedUserFile(deployedGoalToSlicePlannerSkillPath(), bundledGoalToSlicePlannerSkill, 0o644); err != nil {
			return err
		}
	}
	return nil
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

func ensureSeedUserFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return os.ErrExist
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, data, mode)
}
