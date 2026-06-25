package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestLoadSkillFileMergesWhenToUseAndVisibility locks the Claude Code frontmatter
// compatibility additions: when_to_use folds into the trigger summary,
// disable-model-invocation / user-invocable parse, and unsupported fields warn
// instead of being silently dropped.
func TestLoadSkillFileMergesWhenToUseAndVisibility(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	content := "---\n" +
		"name: probe\n" +
		"description: Base description.\n" +
		"when_to_use: trigger on X and Y.\n" +
		"disable-model-invocation: true\n" +
		"user-invocable: false\n" +
		"allowed-tools: read_file, grep\n" +
		"model: claude-opus-4-1\n" +
		"paths: \"*.go\"\n" +
		"---\n" +
		"Body.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	skill, warns, err := loadSkillFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(skill.Summary, "Base description.") || !strings.Contains(skill.Summary, "trigger on X and Y.") {
		t.Fatalf("when_to_use must fold into the summary, got %q", skill.Summary)
	}
	if !skill.DisableModelInvocation {
		t.Fatalf("disable-model-invocation:true must parse")
	}
	if skill.UserInvocable {
		t.Fatalf("user-invocable:false must parse")
	}
	joined := strings.Join(warns, "\n")
	if !strings.Contains(joined, "model") || !strings.Contains(joined, "paths") {
		t.Fatalf("unsupported fields (model, paths) must warn, got %#v", warns)
	}
}

func TestClampSkillSummaryLongAndRuneSafe(t *testing.T) {
	// Anti-cheat skills carry long Korean trigger descriptions; the old 280-byte
	// cap truncated them. 600 runes must survive the 1536 cap intact.
	long := strings.Repeat("가", 600)
	got := clampSkillSummary(long)
	if len([]rune(got)) != 600 {
		t.Fatalf("a 600-rune description must survive the 1536 cap, got %d runes", len([]rune(got)))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("clamp must keep valid UTF-8")
	}
	// Over the cap: truncated, still valid UTF-8, and marked.
	got = clampSkillSummary(strings.Repeat("나", 2000))
	if !utf8.ValidString(got) {
		t.Fatalf("over-cap clamp must not split a multibyte rune")
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("over-cap clamp must mark truncation")
	}
}

func TestCatalogPromptExcludesDisableModelInvocation(t *testing.T) {
	catalog := newTestSkillCatalog(
		Skill{Name: "visible", Summary: "shows up", UserInvocable: true},
		Skill{Name: "hidden", Summary: "model-hidden", DisableModelInvocation: true, UserInvocable: true},
	)
	cat := catalog.CatalogPrompt()
	if !strings.Contains(cat, "visible") {
		t.Fatalf("a normal skill must appear in the model catalog, got %q", cat)
	}
	if strings.Contains(cat, "hidden") {
		t.Fatalf("disable-model-invocation skill must be hidden from the model catalog, got %q", cat)
	}
}

func TestInjectPromptContextRespectsUserInvocable(t *testing.T) {
	catalog := newTestSkillCatalog(
		Skill{Name: "open", Path: "/s/open/SKILL.md", Content: "open-body", UserInvocable: true},
		Skill{Name: "modelonly", Path: "/s/mo/SKILL.md", Content: "mo-body", UserInvocable: false},
	)
	out := catalog.InjectPromptContext("use $open and $modelonly")
	if !strings.Contains(out, "open-body") {
		t.Fatalf("a user-invocable skill must inject via $name, got %q", out)
	}
	if strings.Contains(out, "mo-body") {
		t.Fatalf("user-invocable:false skill must not inject via $name, got %q", out)
	}
}

func TestLoadSkillToolRejectsDisableModelInvocation(t *testing.T) {
	catalog := newTestSkillCatalog(Skill{
		Name:                   "userOnly",
		Path:                   "/s/useronly/SKILL.md",
		Content:                "body",
		DisableModelInvocation: true,
		UserInvocable:          true,
	})
	tool := NewLoadSkillTool(catalog)
	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{"name": "userOnly"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok, _ := result.Meta["success"].(bool); ok {
		t.Fatalf("the model must not load a disable-model-invocation skill")
	}
	if !strings.Contains(result.ModelText, "user-invocable only") {
		t.Fatalf("expected a user-invocable-only message, got %q", result.ModelText)
	}
}

// TestSelectableCountExcludesDisableModelInvocation locks the catalog-consistency
// fix: a disable-model-invocation skill is hidden from CatalogPrompt, so it must not
// count toward SelectableCount (which gates whether the catalog is built at all).
func TestSelectableCountExcludesDisableModelInvocation(t *testing.T) {
	catalog := newTestSkillCatalog(
		Skill{Name: "a", UserInvocable: true},
		Skill{Name: "b", Enabled: true},
		Skill{Name: "c", DisableModelInvocation: true, UserInvocable: true},
	)
	if got := catalog.SelectableCount(); got != 1 {
		t.Fatalf("SelectableCount must exclude enabled and disable-model-invocation skills (only 'a' is model-selectable), got %d", got)
	}
}

// TestInjectPromptContextExactNameNoPrefixCollision locks the $name prefix-collision
// fix: a $foo skill must not rewrite or activate from an unrelated $foobar token.
func TestInjectPromptContextExactNameNoPrefixCollision(t *testing.T) {
	catalog := newTestSkillCatalog(
		Skill{Name: "foo", Path: "/s/foo/SKILL.md", Content: "foo-body", UserInvocable: true},
	)
	// Only skill "foo" exists; "$foobar" must be left intact and not activate "foo".
	out := catalog.InjectPromptContext("run $foobar now")
	if !strings.Contains(out, "$foobar") {
		t.Fatalf("$foobar must be left intact when only skill 'foo' exists, got %q", out)
	}
	if strings.Contains(out, "foo-body") {
		t.Fatalf("unknown $foobar must not activate skill 'foo', got %q", out)
	}
	// "$foo" alone still resolves: token replaced with the skill name, body injected.
	out = catalog.InjectPromptContext("run $foo now")
	if strings.Contains(out, "$foo") {
		t.Fatalf("$foo token must be replaced with the skill name, got %q", out)
	}
	if !strings.Contains(out, "foo-body") {
		t.Fatalf("$foo must activate skill 'foo', got %q", out)
	}
}
