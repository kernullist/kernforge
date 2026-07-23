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

func TestEnsureUpgradableBundledSkillFileUpgradesUncustomizedAndPreservesCustom(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "humanize-doc", "SKILL.md")

	// First seed.
	v1 := []byte("version-one skill body\n")
	if err := ensureUpgradableBundledSkillFile(path, v1, 0o644); err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after v1: %v", err)
	}
	if string(got) != string(v1) {
		t.Fatalf("expected v1 body, got %q", got)
	}
	marker, err := os.ReadFile(bundledSkillHashPath(path))
	if err != nil {
		t.Fatalf("expected hash marker after seed: %v", err)
	}
	if strings.TrimSpace(string(marker)) != hashBundledSkillContent(v1) {
		t.Fatalf("marker hash mismatch: got %q want %q", marker, hashBundledSkillContent(v1))
	}

	// Upgrade when still matching last bundled hash.
	v2 := []byte("version-two skill body with no-ai-slop\n")
	if err := ensureUpgradableBundledSkillFile(path, v2, 0o644); err != nil {
		t.Fatalf("upgrade v2: %v", err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after v2: %v", err)
	}
	if string(got) != string(v2) {
		t.Fatalf("expected upgraded v2 body, got %q", got)
	}

	// User customization must not be overwritten.
	custom := []byte("user customized skill body\n")
	if err := os.WriteFile(path, custom, 0o644); err != nil {
		t.Fatalf("write custom: %v", err)
	}
	v3 := []byte("version-three skill body\n")
	if err := ensureUpgradableBundledSkillFile(path, v3, 0o644); err != nil {
		t.Fatalf("upgrade after custom: %v", err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after custom guard: %v", err)
	}
	if string(got) != string(custom) {
		t.Fatalf("customized skill must be preserved, got %q", got)
	}
}

func TestEnsureUpgradableBundledSkillFileMigratesLegacySeedWithoutMarker(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "humanize-doc", "SKILL.md")
	legacy := []byte("old seeded humanize body\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	// No marker file: one-time migrate to current bundled content.
	current := []byte("new bundled humanize body with no-ai-slop\n")
	if err := ensureUpgradableBundledSkillFile(path, current, 0o644); err != nil {
		t.Fatalf("legacy migrate: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after migrate: %v", err)
	}
	if string(got) != string(current) {
		t.Fatalf("legacy seed without marker should migrate once, got %q", got)
	}
	if _, err := os.Stat(bundledSkillHashPath(path)); err != nil {
		t.Fatalf("migrate must write marker: %v", err)
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
			name:         "humanize-doc",
			supportFiles: []string{filepath.Join("references", "ai-tells.md")},
			mustReference: []string{
				"web_search",
				"spawn_task",
				"replace_in_file",
				"no-ai-slop",
				"eval 자기 검수",
				"Binary contrasts",
			},
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
		// humanize-doc also ships a reference file that holds pattern catalogs.
		if tc.name == "humanize-doc" {
			refData, refErr := os.ReadFile(filepath.Join(skillsRoot, tc.name, "references", "ai-tells.md"))
			if refErr != nil {
				t.Fatalf("read humanize-doc ai-tells: %v", refErr)
			}
			text = text + "\n" + string(refData)
		}
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
