package main

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed .kernforge/mcp/web-research-mcp.js
var bundledWebResearchMCPScript []byte

//go:embed .kernforge/skills/goal-to-slice-planner/SKILL.md
var bundledGoalToSlicePlannerSkill []byte

// bundledBuiltinSkills carries the built-in workflow skills shipped inside the
// binary (visual-explainer, humanize-doc, goal-loop, parallel-agents) together
// with any supporting reference and template files each one references. They are
// seeded into the user skills directory on startup so LoadSkills discovers them
// without depending on an external Claude Code skills install. Using "all:" keeps
// files whose names start with "_" or "." (none today, but future-proof).
//
//go:embed all:.kernforge/skills/visual-explainer
//go:embed all:.kernforge/skills/humanize-doc
//go:embed all:.kernforge/skills/goal-loop
//go:embed all:.kernforge/skills/parallel-agents
var bundledBuiltinSkills embed.FS

// bundledBuiltinSkillsEmbedRoot is the path prefix inside bundledBuiltinSkills
// that maps onto the user skills directory. Everything under it is written to
// userConfigDir()/skills/<same-relative-path>.
const bundledBuiltinSkillsEmbedRoot = ".kernforge/skills"

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
	if err := ensureBundledBuiltinSkills(); err != nil {
		return err
	}
	return nil
}

// ensureBundledBuiltinSkills seeds each embedded built-in skill file into the
// user skills directory. It walks the embedded tree and mirrors the layout under
// userConfigDir()/skills so a skill's supporting files (references/, templates/)
// land next to its SKILL.md and stay resolvable by read_file. Seeding is
// non-destructive: ensureSeedUserFile writes a file only when it is absent, so a
// user's local edits to a previously seeded skill are preserved across restarts.
func ensureBundledBuiltinSkills() error {
	skillsRoot := filepath.Join(userConfigDir(), "skills")
	return fs.WalkDir(bundledBuiltinSkills, bundledBuiltinSkillsEmbedRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(bundledBuiltinSkillsEmbedRoot, path)
		if relErr != nil {
			return relErr
		}
		// The embed FS always uses forward slashes; normalize before joining onto
		// the host path so nested references/ and templates/ files map correctly.
		rel = filepath.FromSlash(strings.ReplaceAll(rel, "\\", "/"))
		data, readErr := bundledBuiltinSkills.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		return ensureSeedUserFile(filepath.Join(skillsRoot, rel), data, 0o644)
	})
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
