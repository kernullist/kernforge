package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestViewerSelectionRelativePromptUsesStoredAbsolutePath(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "main.go")
	selection := ViewerSelection{
		FilePath:  file,
		StartLine: 10,
		EndLine:   20,
	}
	got := selection.RelativePrompt(root)
	if got != "@main.go:10-20" {
		t.Fatalf("unexpected selection prompt: %q", got)
	}
}

func TestLoadSelectionPreviewSlicesSelectedLines(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "main.go")
	content := "line1\nline2\nline3\nline4\n"
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	selection := ViewerSelection{
		FilePath:  file,
		StartLine: 2,
		EndLine:   3,
	}
	preview, err := loadSelectionPreview(root, selection)
	if err != nil {
		t.Fatalf("loadSelectionPreview: %v", err)
	}
	if !strings.Contains(preview, "2 | line2") || !strings.Contains(preview, "3 | line3") {
		t.Fatalf("unexpected preview: %q", preview)
	}
}

func TestBuildSelectionAwareEditPreviewAddsFocusedSection(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "main.go")
	ws := Workspace{
		Root: root,
		CurrentSelection: func() *ViewerSelection {
			return &ViewerSelection{
				FilePath:  file,
				StartLine: 2,
				EndLine:   3,
			}
		},
	}
	before := "line1\nline2\nline3\nline4\n"
	after := "line1\nline2 changed\nline3 changed\nline4\n"
	preview := buildSelectionAwareEditPreview(ws, file, before, after)
	if !strings.Contains(preview, "Selection-focused preview") {
		t.Fatalf("expected selection-focused header, got %q", preview)
	}
	if !strings.Contains(preview, "main.go:2-3") {
		t.Fatalf("expected selected range in preview, got %q", preview)
	}
}

func TestFilterUnifiedDiffBySelectionKeepsOnlyIntersectingHunks(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/main.go b/main.go",
		"@@ -1,2 +1,2 @@",
		"-line1",
		"+line1 changed",
		"@@ -10,2 +10,2 @@",
		"-line10",
		"+line10 changed",
	}, "\n")
	filtered := filterUnifiedDiffBySelection(diff, 10, 12)
	if strings.Contains(filtered, "@@ -1,2 +1,2 @@") {
		t.Fatalf("expected first hunk to be filtered out, got %q", filtered)
	}
	if !strings.Contains(filtered, "@@ -10,2 +10,2 @@") {
		t.Fatalf("expected intersecting hunk to remain, got %q", filtered)
	}
}

func TestBuildSelectionContextPromptIncludesNotesAndTags(t *testing.T) {
	root := t.TempDir()
	selections := []ViewerSelection{
		{FilePath: filepath.Join(root, "main.go"), StartLine: 2, EndLine: 4, Note: "auth edge case", Tags: []string{"auth", "critical"}},
		{FilePath: filepath.Join(root, "provider.go"), StartLine: 10, EndLine: 12},
	}
	prompt := buildSelectionContextPrompt(root, selections)
	if !strings.Contains(prompt, "@main.go:2-4") {
		t.Fatalf("expected first selection reference, got %q", prompt)
	}
	if !strings.Contains(prompt, `note="auth edge case"`) || !strings.Contains(prompt, "tags=auth,critical") {
		t.Fatalf("expected note/tags in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "@provider.go:10-12") {
		t.Fatalf("expected second selection reference, got %q", prompt)
	}
}

func TestParseSelectionReviewArgsSupportsSubsetAndExtraInstructions(t *testing.T) {
	sess := NewSession("F:/repo", "openai", "gpt", "", "default")
	sess.AddSelection(ViewerSelection{FilePath: "main.go", StartLine: 1, EndLine: 2})
	sess.AddSelection(ViewerSelection{FilePath: "provider.go", StartLine: 3, EndLine: 4})
	selected, extra, err := parseSelectionReviewArgs(sess, "1,2 -- focus on duplication")
	if err != nil {
		t.Fatalf("parseSelectionReviewArgs: %v", err)
	}
	if len(selected) != 2 {
		t.Fatalf("expected two selections, got %#v", selected)
	}
	if extra != "focus on duplication" {
		t.Fatalf("unexpected extra instructions: %q", extra)
	}
}
