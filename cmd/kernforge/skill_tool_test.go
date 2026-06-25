package main

import (
	"context"
	"strings"
	"testing"
)

func newTestSkillCatalog(skills ...Skill) SkillCatalog {
	catalog := SkillCatalog{byName: map[string]Skill{}}
	for _, skill := range skills {
		catalog.items = append(catalog.items, skill)
		catalog.byName[normalizeSkillName(skill.Name)] = skill
		if skill.Enabled {
			catalog.enabled = append(catalog.enabled, skill)
		}
	}
	return catalog
}

// TestLoadSkillToolReturnsFullBody locks F2: the model can pull a non-enabled
// skill's full instructions on demand via the load_skill tool.
func TestLoadSkillToolReturnsFullBody(t *testing.T) {
	catalog := newTestSkillCatalog(Skill{
		Name:    "BypassSurface",
		Path:    "/skills/bypass/SKILL.md",
		Summary: "Analyze the bypass surface.",
		Content: "Step 1: enumerate detections. Step 2: map evasions.",
	})
	tool := NewLoadSkillTool(catalog)

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{"name": "bypasssurface"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, _ := result.Meta["success"].(bool); !got {
		t.Fatalf("expected success, got meta %#v", result.Meta)
	}
	if !strings.Contains(result.ModelText, "Step 1: enumerate detections.") {
		t.Fatalf("expected full skill body in model text, got %q", result.ModelText)
	}
	if !strings.Contains(result.ModelText, "### BypassSurface") {
		t.Fatalf("expected skill name header, got %q", result.ModelText)
	}
}

// TestLoadSkillToolSurfacesAllowedTools locks F3: a skill that declares
// allowed-tools surfaces that scope in the rendered body so the field is no
// longer inert.
func TestLoadSkillToolSurfacesAllowedTools(t *testing.T) {
	catalog := newTestSkillCatalog(Skill{
		Name:         "ReadOnlyAudit",
		Path:         "/skills/audit/SKILL.md",
		Content:      "Audit without mutating the tree.",
		AllowedTools: []string{"read_file", "grep"},
	})
	tool := NewLoadSkillTool(catalog)

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{"name": "ReadOnlyAudit"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.ModelText, "Preferred tools (allowed-tools):") {
		t.Fatalf("expected allowed-tools preference note, got %q", result.ModelText)
	}
	if !strings.Contains(result.ModelText, "read_file, grep") {
		t.Fatalf("expected the declared tools listed, got %q", result.ModelText)
	}
	if !strings.Contains(result.ModelText, "not a hard restriction") {
		t.Fatalf("allowed-tools must read as a soft preference (Claude Code semantics), got %q", result.ModelText)
	}
}

// TestLoadSkillToolUnknownNameListsAvailable locks the failure path: an unknown
// name returns a non-success result that names the available skills.
func TestLoadSkillToolUnknownNameListsAvailable(t *testing.T) {
	catalog := newTestSkillCatalog(
		Skill{Name: "Alpha", Content: "a"},
		Skill{Name: "Beta", Content: "b"},
	)
	tool := NewLoadSkillTool(catalog)

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{"name": "Gamma"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, _ := result.Meta["success"].(bool); got {
		t.Fatalf("expected failure for unknown skill, got meta %#v", result.Meta)
	}
	if !strings.Contains(result.ModelText, "Alpha") || !strings.Contains(result.ModelText, "Beta") {
		t.Fatalf("expected available skills listed, got %q", result.ModelText)
	}

	empty, err := tool.ExecuteDetailed(context.Background(), map[string]any{"name": "   "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, _ := empty.Meta["success"].(bool); got {
		t.Fatalf("expected failure for blank name, got meta %#v", empty.Meta)
	}
}

// TestBuildRegistryRegistersLoadSkillOnlyWhenSkillsExist locks the registration
// gate: the load_skill tool appears only when at least one skill is discovered.
func TestBuildRegistryRegistersLoadSkillOnlyWhenSkillsExist(t *testing.T) {
	ws := Workspace{BaseRoot: t.TempDir(), Root: t.TempDir()}

	without := buildRegistry(ws, nil, SkillCatalog{})
	if _, ok := without.tools["load_skill"]; ok {
		t.Fatalf("load_skill must not be registered when no skills exist")
	}

	with := buildRegistry(ws, nil, newTestSkillCatalog(Skill{Name: "Alpha", Content: "a"}))
	if _, ok := with.tools["load_skill"]; !ok {
		t.Fatalf("load_skill must be registered when skills exist")
	}
}
