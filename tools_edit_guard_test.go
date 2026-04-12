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

func TestApplyPatchRequiresPreviewApprovalBeforeWriting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package main\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	previewCalls := 0
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			previewCalls++
			if !strings.Contains(preview.Title, "Apply patch") {
				t.Fatalf("unexpected preview title: %q", preview.Title)
			}
			if !strings.Contains(preview.Preview, "Preview for main.go") {
				t.Fatalf("expected patch preview contents, got %q", preview.Preview)
			}
			return false, nil
		},
	}
	tool := NewApplyPatchTool(ws)

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+func main() {}\n*** End Patch\n",
	})
	if !errors.Is(err, ErrEditCanceled) {
		t.Fatalf("expected ErrEditCanceled, got %v", err)
	}
	if previewCalls != 1 {
		t.Fatalf("expected one preview confirmation, got %d", previewCalls)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Fatalf("expected file to remain unchanged after preview rejection, got %q", string(data))
	}
}

func TestToolRegistryExecuteWrapsMalformedJSONAsInvalidToolArguments(t *testing.T) {
	ws := Workspace{}
	registry := NewToolRegistry(NewWriteFileTool(ws))

	_, err := registry.Execute(context.Background(), "write_file", `{"path":"main.go","content":"package main`)
	if !errors.Is(err, ErrInvalidToolArgumentsJSON) {
		t.Fatalf("expected ErrInvalidToolArgumentsJSON, got %v", err)
	}
}

func TestRunShellRejectsWorkspaceMutatingCommands(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	_, err := tool.Execute(context.Background(), map[string]any{
		"command": "Set-Content test.txt 'hello'",
	})
	if err == nil {
		t.Fatalf("expected mutating shell command to be rejected")
	}
	if !strings.Contains(err.Error(), "run_shell cannot modify workspace files") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunShellAllowsReadOnlyCommands(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo hello",
	}); err != nil {
		t.Fatalf("expected read-only shell command to succeed, got %v", err)
	}
}

func TestApplyPatchUpdatesFallbackTargetWhenSourceExistsAtBaseRoot(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(base, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewApplyPatchTool(Workspace{
		BaseRoot: base,
		Root:     current,
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+func main() {}\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "func main() {}") {
		t.Fatalf("expected fallback patch target update, got %q", string(data))
	}
}
