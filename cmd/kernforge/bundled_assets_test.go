package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureBundledUserAssetsSeedsGoalToSlicePlannerSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if err := ensureBundledUserAssets(); err != nil {
		t.Fatalf("ensureBundledUserAssets: %v", err)
	}

	path := deployedGoalToSlicePlannerSkillPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read deployed skill: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"# goal-to-slice-planner",
		"draft-only planning skill",
		"Likely files: candidate files or directories to inspect or touch; this is not a change claim",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("deployed skill missing %q:\n%s", want, text)
		}
	}
}

func TestEnsureBundledUserAssetsSeedsBuiltinWorkflowSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if err := ensureBundledUserAssets(); err != nil {
		t.Fatalf("ensureBundledUserAssets: %v", err)
	}

	skillsRoot := filepath.Join(userConfigDir(), "skills")

	// Each built-in skill must land as a SKILL.md, and multi-file skills must
	// keep their supporting reference/template files next to it so read_file can
	// resolve the relative paths the SKILL.md instructions use.
	cases := []struct {
		name          string
		supportFiles  []string
		mustReference []string
	}{
		{
			name:          "goal-loop",
			supportFiles:  nil,
			mustReference: []string{"web_search", "spawn_task", "web_fetch"},
		},
		{
			name:          "humanize-doc",
			supportFiles:  []string{filepath.Join("references", "ai-tells.md")},
			mustReference: []string{"web_search", "spawn_task", "replace_in_file"},
		},
		{
			name:          "parallel-agents",
			supportFiles:  []string{filepath.Join("references", "decomposition-patterns.md"), filepath.Join("references", "prompt-library.md")},
			mustReference: []string{"spawn_task", "get_task"},
		},
		{
			name: "visual-explainer",
			supportFiles: []string{
				filepath.Join("references", "css-patterns.md"),
				filepath.Join("references", "libraries.md"),
				filepath.Join("references", "responsive-nav.md"),
				filepath.Join("templates", "architecture.html"),
				filepath.Join("templates", "data-table.html"),
				filepath.Join("templates", "mermaid-flowchart.html"),
			},
			mustReference: []string{"write_file", ".kernforge/diagrams"},
		},
	}

	for _, tc := range cases {
		skillPath := filepath.Join(skillsRoot, tc.name, "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			t.Fatalf("read seeded skill %s: %v", tc.name, err)
		}
		text := string(data)
		for _, want := range tc.mustReference {
			if !strings.Contains(text, want) {
				t.Fatalf("skill %s should reference native token %q but does not", tc.name, want)
			}
		}
		// The adapted skills must not tell the model to call Claude Code tool
		// names that do not exist in kernforge.
		for _, forbidden := range []string{"WebSearch", "WebFetch"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("skill %s still references Claude Code tool %q; adapt it to the kernforge tool name", tc.name, forbidden)
			}
		}
		for _, support := range tc.supportFiles {
			if _, err := os.Stat(filepath.Join(skillsRoot, tc.name, support)); err != nil {
				t.Fatalf("skill %s missing seeded support file %s: %v", tc.name, support, err)
			}
		}
	}

	// After seeding, LoadSkills must discover every built-in skill by name so it
	// is model-selectable from the catalog and reachable with $name.
	catalog, warnings := LoadSkills(t.TempDir(), nil, nil)
	for _, warn := range warnings {
		if strings.Contains(warn, "goal-loop") || strings.Contains(warn, "humanize-doc") ||
			strings.Contains(warn, "parallel-agents") || strings.Contains(warn, "visual-explainer") {
			t.Fatalf("unexpected load warning for built-in skill: %s", warn)
		}
	}
	for _, tc := range cases {
		if _, ok := catalog.Lookup(tc.name); !ok {
			t.Fatalf("LoadSkills did not discover built-in skill %q", tc.name)
		}
	}
}

func TestEnsureBundledUserAssetsDoesNotOverwriteUserEditedSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	path := deployedGoalToSlicePlannerSkillPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	custom := "# goal-to-slice-planner\n\nUser customized copy.\n"
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatalf("write custom skill: %v", err)
	}

	if err := ensureBundledUserAssets(); err != nil {
		t.Fatalf("ensureBundledUserAssets: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read deployed skill: %v", err)
	}
	if string(data) != custom {
		t.Fatalf("expected custom skill to be preserved, got:\n%s", string(data))
	}
}
