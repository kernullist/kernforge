package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAgentCatalogProfilesParsesFrontmatterAndBody(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kernforge", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	content := "---\n" +
		"name: kernel-auditor\n" +
		"description: Audits kernel driver code\n" +
		"model: gpt-5\n" +
		"provider: openai\n" +
		"tools: read_file, grep, apply_patch\n" +
		"editable: true\n" +
		"---\n" +
		"You are a kernel driver auditor. Look for IRQL and pool leaks.\n"
	if err := os.WriteFile(filepath.Join(dir, "kernel-auditor.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}

	profiles := loadAgentCatalogProfiles(root)
	if len(profiles) != 1 {
		t.Fatalf("expected 1 discovered profile, got %d", len(profiles))
	}
	got := profiles[0]
	if got.Name != "kernel-auditor" {
		t.Fatalf("name = %q, want kernel-auditor", got.Name)
	}
	if got.Description != "Audits kernel driver code" {
		t.Fatalf("description = %q", got.Description)
	}
	if got.Model != "gpt-5" {
		t.Fatalf("model = %q, want gpt-5", got.Model)
	}
	if got.Provider != "openai" {
		t.Fatalf("provider = %q, want openai", got.Provider)
	}
	if len(got.Tools) != 3 || got.Tools[0] != "read_file" || got.Tools[2] != "apply_patch" {
		t.Fatalf("tools = %v, want [read_file grep apply_patch]", got.Tools)
	}
	if got.Editable == nil || !*got.Editable {
		t.Fatalf("editable = %v, want true", got.Editable)
	}
	if got.Prompt == "" {
		t.Fatalf("prompt should fall back to the body, got empty")
	}
}

func TestLoadAgentCatalogProfilesPromptFallsBackToFileNameAndBody(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kernforge", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	// No frontmatter at all: file name is the name, whole file is the prompt.
	body := "Investigate memory inspection bypasses.\n"
	if err := os.WriteFile(filepath.Join(dir, "memory-inspector.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	profiles := loadAgentCatalogProfiles(root)
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	if profiles[0].Name != "memory-inspector" {
		t.Fatalf("name = %q, want memory-inspector", profiles[0].Name)
	}
	if profiles[0].Prompt == "" {
		t.Fatalf("prompt should be the body, got empty")
	}
}

func TestLoadAgentCatalogProfilesMissingDirReturnsNil(t *testing.T) {
	root := t.TempDir()
	if profiles := loadAgentCatalogProfiles(root); profiles != nil {
		t.Fatalf("expected nil for missing dir, got %v", profiles)
	}
	if profiles := loadAgentCatalogProfiles(""); profiles != nil {
		t.Fatalf("expected nil for empty root, got %v", profiles)
	}
}

func TestMergeAgentCatalogProfilesOverridesAndAppends(t *testing.T) {
	configProfiles := []SpecialistSubagentProfile{
		{Name: "implementation-owner", Description: "config base", Model: "config-model"},
	}
	diskProfiles := []SpecialistSubagentProfile{
		// Same name: override only the set fields, keep config as base.
		{Name: "implementation-owner", Model: "disk-model"},
		// New name: appended.
		{Name: "disk-only-agent", Prompt: "disk prompt"},
	}
	merged := mergeAgentCatalogProfiles(configProfiles, diskProfiles)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged profiles, got %d", len(merged))
	}
	byName := map[string]SpecialistSubagentProfile{}
	for _, p := range merged {
		byName[p.Name] = p
	}
	owner, ok := byName["implementation-owner"]
	if !ok {
		t.Fatalf("implementation-owner missing after merge")
	}
	if owner.Model != "disk-model" {
		t.Fatalf("model = %q, want disk-model (disk overrides)", owner.Model)
	}
	if owner.Description != "config base" {
		t.Fatalf("description = %q, want config base preserved", owner.Description)
	}
	if _, ok := byName["disk-only-agent"]; !ok {
		t.Fatalf("disk-only-agent should be appended")
	}
}

func TestMergeAgentCatalogProfilesEmptyDiskKeepsConfig(t *testing.T) {
	configProfiles := []SpecialistSubagentProfile{{Name: "planner"}}
	merged := mergeAgentCatalogProfiles(configProfiles, nil)
	if len(merged) != 1 || merged[0].Name != "planner" {
		t.Fatalf("expected config profiles unchanged, got %v", merged)
	}
}
