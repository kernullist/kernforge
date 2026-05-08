package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveInteractiveShellPathAllowsParentWithinBaseRoot(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "TavernKernel", "TavernKernel", "TavernKernel")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	parent := filepath.Dir(nested)

	got, err := resolveInteractiveShellPath(Workspace{
		BaseRoot: base,
		Root:     nested,
	}, &Session{
		BaseWorkingDir: base,
		WorkingDir:     nested,
	}, "..")
	if err != nil {
		t.Fatalf("resolveInteractiveShellPath: %v", err)
	}
	if !strings.EqualFold(filepath.Clean(got), filepath.Clean(parent)) {
		t.Fatalf("resolved path = %q, want %q", got, parent)
	}
}

func TestResolveInteractiveShellPathRejectsParentOutsideBaseRoot(t *testing.T) {
	base := t.TempDir()

	_, err := resolveInteractiveShellPath(Workspace{
		BaseRoot: base,
		Root:     base,
	}, &Session{
		BaseWorkingDir: base,
		WorkingDir:     base,
	}, "..")
	if err == nil {
		t.Fatalf("expected parent outside base root to be rejected")
	}
	if !strings.Contains(err.Error(), "outside the active workspace root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveInteractiveShellPathAllowsChildNameStartingWithDots(t *testing.T) {
	base := t.TempDir()
	child := filepath.Join(base, "..safe-child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	got, err := resolveInteractiveShellPath(Workspace{
		BaseRoot: base,
		Root:     base,
	}, &Session{
		BaseWorkingDir: base,
		WorkingDir:     base,
	}, "..safe-child")
	if err != nil {
		t.Fatalf("resolveInteractiveShellPath: %v", err)
	}
	if !strings.EqualFold(filepath.Clean(got), filepath.Clean(child)) {
		t.Fatalf("resolved path = %q, want %q", got, child)
	}
}

func TestResolveInteractiveShellPathUsesActiveWorktreeBoundary(t *testing.T) {
	parent := t.TempDir()
	base := filepath.Join(parent, "repo")
	worktree := filepath.Join(parent, "worktree")
	nested := filepath.Join(worktree, "src", "driver")
	for _, dir := range []string{base, nested} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
	}

	got, err := resolveInteractiveShellPath(Workspace{
		BaseRoot: base,
		Root:     nested,
	}, &Session{
		BaseWorkingDir: base,
		WorkingDir:     nested,
		Worktree: &SessionWorktree{
			Root:   worktree,
			Active: true,
		},
	}, "..")
	if err != nil {
		t.Fatalf("resolveInteractiveShellPath: %v", err)
	}
	if want := filepath.Join(worktree, "src"); !strings.EqualFold(filepath.Clean(got), filepath.Clean(want)) {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}

	_, err = resolveInteractiveShellPath(Workspace{
		BaseRoot: base,
		Root:     worktree,
	}, &Session{
		BaseWorkingDir: base,
		WorkingDir:     worktree,
		Worktree: &SessionWorktree{
			Root:   worktree,
			Active: true,
		},
	}, "..")
	if err == nil {
		t.Fatalf("expected parent outside active worktree root to be rejected")
	}
}
