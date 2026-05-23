package main

import (
	"encoding/json"
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

func TestLoadWorkspaceSelectionsRecoversCorruptPrimaryFromBackup(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kernforge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, "selections.json")
	backup := workspaceSelectionsBackupPath(path)
	backupData := []byte(`[{"file_path":"main.go","start_line":2,"end_line":4,"note":"keep"}]`)
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatalf("write corrupt primary: %v", err)
	}
	if err := os.WriteFile(backup, backupData, 0o644); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	selections, err := LoadWorkspaceSelections(root)
	if err != nil {
		t.Fatalf("LoadWorkspaceSelections: %v", err)
	}
	if len(selections) != 1 {
		t.Fatalf("expected one recovered selection, got %#v", selections)
	}
	if selections[0].FilePath != filepath.Join(root, "main.go") {
		t.Fatalf("expected absolute recovered file path, got %q", selections[0].FilePath)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored primary: %v", err)
	}
	if !json.Valid(restored) {
		t.Fatalf("expected restored primary to be valid JSON: %q", restored)
	}
}

func TestSyncWorkspaceSelectionsUsesBackupWhenPrimaryIsCorrupt(t *testing.T) {
	root := t.TempDir()
	first := ViewerSelection{
		FilePath:  filepath.Join(root, "first.go"),
		StartLine: 1,
		EndLine:   3,
		Note:      "first",
	}
	if err := SyncWorkspaceSelections(root, []ViewerSelection{first}); err != nil {
		t.Fatalf("initial SyncWorkspaceSelections: %v", err)
	}

	path := filepath.Join(root, ".kernforge", "selections.json")
	backup := workspaceSelectionsBackupPath(path)
	if data, err := os.ReadFile(backup); err != nil {
		t.Fatalf("expected backup after sync: %v", err)
	} else if !json.Valid(data) {
		t.Fatalf("expected valid backup JSON")
	}
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatalf("corrupt primary: %v", err)
	}

	second := ViewerSelection{
		FilePath:  filepath.Join(root, "second.go"),
		StartLine: 4,
		EndLine:   5,
		Note:      "second",
	}
	if err := SyncWorkspaceSelections(root, []ViewerSelection{second}); err != nil {
		t.Fatalf("recovering SyncWorkspaceSelections: %v", err)
	}
	selections, err := LoadWorkspaceSelections(root)
	if err != nil {
		t.Fatalf("LoadWorkspaceSelections: %v", err)
	}
	if len(selections) != 2 {
		t.Fatalf("expected recovered first selection plus new second selection, got %#v", selections)
	}
	var foundFirst bool
	var foundSecond bool
	for _, selection := range selections {
		if selection.FilePath == filepath.Join(root, "first.go") && selection.Note == "first" {
			foundFirst = true
		}
		if selection.FilePath == filepath.Join(root, "second.go") && selection.Note == "second" {
			foundSecond = true
		}
	}
	if !foundFirst || !foundSecond {
		t.Fatalf("expected both recovered and new selections, got %#v", selections)
	}
}

func TestSyncWorkspaceSelectionsMergesAnnotatedWritesByKey(t *testing.T) {
	root := t.TempDir()
	first := ViewerSelection{
		FilePath:  filepath.Join(root, "first.go"),
		StartLine: 1,
		EndLine:   3,
		Note:      "written by another session",
	}
	if err := SyncWorkspaceSelections(root, []ViewerSelection{first}); err != nil {
		t.Fatalf("initial SyncWorkspaceSelections: %v", err)
	}

	staleUnannotated := ViewerSelection{
		FilePath:  filepath.Join(root, "first.go"),
		StartLine: 1,
		EndLine:   3,
	}
	second := ViewerSelection{
		FilePath:  filepath.Join(root, "second.go"),
		StartLine: 4,
		EndLine:   5,
		Tags:      []string{"fresh"},
	}
	if err := SyncWorkspaceSelections(root, []ViewerSelection{staleUnannotated, second}); err != nil {
		t.Fatalf("merge SyncWorkspaceSelections: %v", err)
	}

	selections, err := LoadWorkspaceSelections(root)
	if err != nil {
		t.Fatalf("LoadWorkspaceSelections: %v", err)
	}
	if len(selections) != 2 {
		t.Fatalf("expected two merged selections, got %#v", selections)
	}
	var foundFirst bool
	var foundSecond bool
	for _, selection := range selections {
		switch {
		case selection.FilePath == filepath.Join(root, "first.go"):
			foundFirst = selection.Note == "written by another session"
		case selection.FilePath == filepath.Join(root, "second.go"):
			foundSecond = len(selection.Tags) == 1 && selection.Tags[0] == "fresh"
		}
	}
	if !foundFirst || !foundSecond {
		t.Fatalf("expected stale unannotated selection not to delete annotated writes, got %#v", selections)
	}
}

func TestSyncWorkspaceSelectionsIgnoresUnannotatedOnlySnapshot(t *testing.T) {
	root := t.TempDir()
	persisted := ViewerSelection{
		FilePath:  filepath.Join(root, "main.go"),
		StartLine: 8,
		EndLine:   12,
		Tags:      []string{"keep"},
	}
	if err := SyncWorkspaceSelections(root, []ViewerSelection{persisted}); err != nil {
		t.Fatalf("initial SyncWorkspaceSelections: %v", err)
	}

	if err := SyncWorkspaceSelections(root, []ViewerSelection{
		{FilePath: filepath.Join(root, "main.go"), StartLine: 8, EndLine: 12},
	}); err != nil {
		t.Fatalf("unannotated SyncWorkspaceSelections: %v", err)
	}

	selections, err := LoadWorkspaceSelections(root)
	if err != nil {
		t.Fatalf("LoadWorkspaceSelections: %v", err)
	}
	if len(selections) != 1 || selections[0].FilePath != filepath.Join(root, "main.go") || len(selections[0].Tags) != 1 || selections[0].Tags[0] != "keep" {
		t.Fatalf("expected unannotated snapshot to preserve existing workspace metadata, got %#v", selections)
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
