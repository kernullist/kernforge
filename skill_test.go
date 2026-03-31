package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSkillsFindsWorkspaceSkillsAndEnabledDefaults(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".kernforge", "skills", "checks")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "# Checks\n\nRun tests and report failures before editing.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	catalog, warnings := LoadSkills(dir, nil, []string{"checks"})

	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if catalog.Count() != 1 {
		t.Fatalf("expected 1 skill, got %d", catalog.Count())
	}
	if catalog.EnabledCount() != 1 {
		t.Fatalf("expected 1 enabled skill, got %d", catalog.EnabledCount())
	}
	prompt := catalog.DefaultPrompt()
	if !strings.Contains(prompt, "Run tests and report failures") {
		t.Fatalf("expected enabled skill content in default prompt, got %q", prompt)
	}
}

func TestSkillCatalogInjectsExplicitSkillContext(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "planner")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "# Planner\n\nBreak the work into ordered steps.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	catalog, warnings := LoadSkills(dir, nil, nil)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	enriched := catalog.InjectPromptContext("please use $planner for this task")

	if strings.Contains(enriched, "$planner") {
		t.Fatalf("expected explicit skill token to be normalized, got %q", enriched)
	}
	if !strings.Contains(enriched, "Activated skills for this request:") {
		t.Fatalf("expected activated skills section, got %q", enriched)
	}
	if !strings.Contains(enriched, "Break the work into ordered steps.") {
		t.Fatalf("expected injected skill body, got %q", enriched)
	}
}

func TestInitSkillTemplateIncludesSkillName(t *testing.T) {
	text := InitSkillTemplate("planner")
	if !strings.Contains(text, "# planner") {
		t.Fatalf("expected heading to include skill name, got %q", text)
	}
	if !strings.Contains(text, "## Workflow") {
		t.Fatalf("expected workflow section, got %q", text)
	}
}

func TestDefaultSkillSearchPathsExcludeLegacyLocations(t *testing.T) {
	paths := defaultSkillSearchPaths(filepath.Join("workspace", "repo"))
	for _, path := range paths {
		lower := strings.ToLower(filepath.ToSlash(path))
		if strings.Contains(lower, ".imcli") {
			t.Fatalf("unexpected legacy skill path: %s", path)
		}
	}
}
