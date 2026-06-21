package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSkillsFindsWorkspaceSkillsAndEnabledDefaults(t *testing.T) {
	dir := t.TempDir()
	isolateUserConfigDir(t)
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
	skill, ok := catalog.Lookup("checks")
	if !ok {
		t.Fatalf("expected checks skill in catalog, got %#v", catalog.Items())
	}
	if !strings.EqualFold(skill.Path, filepath.Join(skillDir, "SKILL.md")) {
		t.Fatalf("expected workspace checks skill path, got %q", skill.Path)
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
	isolateUserConfigDir(t)
	skillDir := filepath.Join(dir, "skills", "unit-planner")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "# unit-planner\n\nBreak the work into ordered steps.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	catalog, warnings := LoadSkills(dir, nil, nil)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	enriched := catalog.InjectPromptContext("please use $unit-planner for this task")

	if strings.Contains(enriched, "$unit-planner") {
		t.Fatalf("expected explicit skill token to be normalized, got %q", enriched)
	}
	if !strings.Contains(enriched, "Activated skills for this request:") {
		t.Fatalf("expected activated skills section, got %q", enriched)
	}
	if !strings.Contains(enriched, "Break the work into ordered steps.") {
		t.Fatalf("expected injected skill body, got %q", enriched)
	}
}

func TestLoadSkillParsesYAMLFrontmatter(t *testing.T) {
	dir := t.TempDir()
	isolateUserConfigDir(t)
	skillDir := filepath.Join(dir, ".kernforge", "skills", "raw-folder")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	// Frontmatter name/description must win over the folder name and the first
	// prose line. allowed-tools must parse into a list. Body must drop the
	// frontmatter block.
	content := "---\n" +
		"name: memory-auditor\n" +
		"description: \"Audit persistent memory entries for stale claims before reuse.\"\n" +
		"allowed-tools: Read, Grep\n" +
		"---\n" +
		"# Heading That Should Not Win\n\n" +
		"This prose line should not become the summary.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	catalog, warnings := LoadSkills(dir, nil, nil)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	skill, ok := catalog.Lookup("memory-auditor")
	if !ok {
		t.Fatalf("expected frontmatter name to be used, items=%#v", catalog.Items())
	}
	if skill.Summary != "Audit persistent memory entries for stale claims before reuse." {
		t.Fatalf("expected description as summary, got %q", skill.Summary)
	}
	if len(skill.AllowedTools) != 2 || skill.AllowedTools[0] != "Read" || skill.AllowedTools[1] != "Grep" {
		t.Fatalf("expected allowed-tools [Read Grep], got %#v", skill.AllowedTools)
	}
	if strings.Contains(skill.Content, "name: memory-auditor") {
		t.Fatalf("expected frontmatter to be stripped from content, got %q", skill.Content)
	}
	if !strings.Contains(skill.Content, "# Heading That Should Not Win") {
		t.Fatalf("expected markdown body to be retained, got %q", skill.Content)
	}
}

func TestLoadSkillWithoutFrontmatterFallsBackToHeading(t *testing.T) {
	// Files without a frontmatter block must keep the legacy behavior: name
	// from the first heading and summary from the first prose line.
	dir := t.TempDir()
	isolateUserConfigDir(t)
	skillDir := filepath.Join(dir, ".kernforge", "skills", "legacy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "# legacy-skill\n\nDo the legacy thing carefully.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	catalog, warnings := LoadSkills(dir, nil, nil)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	skill, ok := catalog.Lookup("legacy-skill")
	if !ok {
		t.Fatalf("expected heading name fallback, items=%#v", catalog.Items())
	}
	if skill.Summary != "Do the legacy thing carefully." {
		t.Fatalf("expected first prose line summary, got %q", skill.Summary)
	}
	if len(skill.AllowedTools) != 0 {
		t.Fatalf("expected no allowed-tools, got %#v", skill.AllowedTools)
	}
}

func TestParseSkillFrontmatterUnterminatedTreatedAsBody(t *testing.T) {
	// An opening "---" with no closing fence is not valid frontmatter and must
	// be returned as body so a horizontal-rule style document is not mangled.
	front, body := parseSkillFrontmatter("---\nname: broken\nstill body\n")
	if len(front) != 0 {
		t.Fatalf("expected no frontmatter keys, got %#v", front)
	}
	if !strings.Contains(body, "name: broken") {
		t.Fatalf("expected full text as body, got %q", body)
	}
}

func TestSkillCatalogSelectableCount(t *testing.T) {
	dir := t.TempDir()
	isolateUserConfigDir(t)
	for _, name := range []string{"alpha", "beta"} {
		skillDir := filepath.Join(dir, ".kernforge", "skills", name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatalf("mkdir skill dir: %v", err)
		}
		content := "# " + name + "\n\nSummary for " + name + ".\n"
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatalf("write SKILL.md: %v", err)
		}
	}
	// Only "alpha" is enabled by default; "beta" stays selectable on demand.
	// (Ancestor traversal of the temp dir can pick up unrelated global skills,
	// so assert relative selectability of our two skills, not an absolute count.)
	catalog, _ := LoadSkills(dir, nil, []string{"alpha"})
	alpha, ok := catalog.Lookup("alpha")
	if !ok || !alpha.Enabled {
		t.Fatalf("expected alpha to be enabled, got %#v ok=%v", alpha, ok)
	}
	beta, ok := catalog.Lookup("beta")
	if !ok || beta.Enabled {
		t.Fatalf("expected beta to be selectable (not enabled), got %#v ok=%v", beta, ok)
	}
	if catalog.SelectableCount() < 1 {
		t.Fatalf("expected at least 1 selectable skill, got %d", catalog.SelectableCount())
	}
}

func TestShouldIncludeSkillCatalogAutoAvailable(t *testing.T) {
	dir := t.TempDir()
	isolateUserConfigDir(t)
	skillDir := filepath.Join(dir, ".kernforge", "skills", "auto")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "---\nname: auto\ndescription: Auto available skill.\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	catalog, _ := LoadSkills(dir, nil, nil)
	// Auto-availability: catalog is offered even when the prompt never mentions
	// "skill" and uses no literal "$name" token.
	if !shouldIncludeSkillCatalogInSystemPrompt("please refactor this parser", catalog) {
		t.Fatalf("expected catalog to be auto-available when selectable skills exist")
	}
	// With no skills at all, keep the keyword/token gate behavior.
	empty := SkillCatalog{}
	if shouldIncludeSkillCatalogInSystemPrompt("please refactor this parser", empty) {
		t.Fatalf("expected no catalog injection without skills and without keyword")
	}
	if !shouldIncludeSkillCatalogInSystemPrompt("show me the skill list", empty) {
		t.Fatalf("expected keyword gate to still trigger injection")
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

func isolateUserConfigDir(t *testing.T) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}
