package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceResolveForLookupFallsBackToBaseRoot(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(base, "root_only.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ws := Workspace{
		BaseRoot: base,
		Root:     current,
	}

	resolved, err := ws.ResolveForLookup("root_only.txt")
	if err != nil {
		t.Fatalf("ResolveForLookup: %v", err)
	}
	if resolved != target {
		t.Fatalf("expected fallback path %q, got %q", target, resolved)
	}
}

func TestReadFileToolPrefersCurrentDirectoryBeforeBaseRoot(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "shared.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("WriteFile base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(current, "shared.txt"), []byte("current\n"), 0o644); err != nil {
		t.Fatalf("WriteFile current: %v", err)
	}

	tool := NewReadFileTool(Workspace{
		BaseRoot: base,
		Root:     current,
	})

	out, err := tool.Execute(context.Background(), map[string]any{"path": "shared.txt"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "current") {
		t.Fatalf("expected current directory file contents, got %q", out)
	}
	if strings.Contains(out, "base") {
		t.Fatalf("expected current directory file to win over base root, got %q", out)
	}
}

func TestListFilesToolFallsBackToBaseRootWhenRelativePathMissingInCurrentDirectory(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	targetDir := filepath.Join(base, "docs")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll current: %v", err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "guide.md"), []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewListFilesTool(Workspace{
		BaseRoot: base,
		Root:     current,
	})

	out, err := tool.Execute(context.Background(), map[string]any{"path": "docs"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "../docs/guide.md") {
		t.Fatalf("expected fallback listing to include root-level file, got %q", out)
	}
}

func TestWriteFileToolUpdatesFallbackTargetInsteadOfCreatingSiblingFile(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(base, "config.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}

	tool := NewWriteFileTool(Workspace{
		BaseRoot: base,
		Root:     current,
	})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":    "config.txt",
		"content": "new",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("expected fallback target update, got %q", string(data))
	}
	if _, err := os.Stat(filepath.Join(current, "config.txt")); !os.IsNotExist(err) {
		t.Fatalf("did not expect a new file in the current directory, err=%v", err)
	}
}

func TestWriteFileToolCreatesNewFileInCurrentDirectoryWhenNoFallbackExists(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	tool := NewWriteFileTool(Workspace{
		BaseRoot: base,
		Root:     current,
	})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":    "new.txt",
		"content": "hello",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(current, "new.txt"))
	if err != nil {
		t.Fatalf("ReadFile current: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("expected current-directory create, got %q", string(data))
	}
}

func TestReplaceInFileToolUpdatesFallbackTarget(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(base, "sample.txt")
	if err := os.WriteFile(target, []byte("alpha beta"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}

	tool := NewReplaceInFileTool(Workspace{
		BaseRoot: base,
		Root:     current,
	})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":    "sample.txt",
		"search":  "beta",
		"replace": "gamma",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if string(data) != "alpha gamma" {
		t.Fatalf("expected fallback replace target update, got %q", string(data))
	}
}
