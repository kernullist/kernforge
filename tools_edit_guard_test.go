package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceEnsureWriteRejectsNestedClaudeWorktreeOutsideActiveRoot(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, ".claude", "worktrees", "compassionate-goldberg", "completion.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	ws := Workspace{
		BaseRoot: base,
		Root:     base,
	}

	err := ws.EnsureWrite(target)
	if err == nil {
		t.Fatalf("expected nested worktree edit to be rejected")
	}
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch, got %v", err)
	}
}

func TestWorkspaceEnsureWriteAllowsActiveNestedClaudeWorktree(t *testing.T) {
	base := t.TempDir()
	activeRoot := filepath.Join(base, ".claude", "worktrees", "compassionate-goldberg")
	target := filepath.Join(activeRoot, "completion.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	ws := Workspace{
		BaseRoot: base,
		Root:     activeRoot,
	}

	if err := ws.EnsureWrite(target); err != nil {
		t.Fatalf("expected active worktree edit to be allowed, got %v", err)
	}
}

func TestReplaceInFileReturnsEditTargetMismatchWhenSearchTextMissing(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "completion.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewReplaceInFileTool(Workspace{BaseRoot: root, Root: root})

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "completion.go",
		"search":  "missing",
		"replace": "found",
	})
	if err == nil {
		t.Fatalf("expected replace failure")
	}
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch, got %v", err)
	}
}

func TestEditToolDescriptionsBiasTowardApplyPatch(t *testing.T) {
	ws := Workspace{}

	patchDesc := NewApplyPatchTool(ws).Definition().Description
	if !strings.Contains(patchDesc, "default edit tool") {
		t.Fatalf("expected apply_patch description to emphasize default usage, got %q", patchDesc)
	}

	replaceDesc := NewReplaceInFileTool(ws).Definition().Description
	if !strings.Contains(replaceDesc, "only for very small single-location substitutions") {
		t.Fatalf("expected replace_in_file description to emphasize narrow usage, got %q", replaceDesc)
	}
}
