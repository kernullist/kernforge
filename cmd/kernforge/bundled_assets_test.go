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
