package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckpointCreateListAndRollback(t *testing.T) {
	workspace := t.TempDir()
	manager := &CheckpointManager{
		Root: filepath.Join(t.TempDir(), "checkpoints"),
	}

	if err := os.WriteFile(filepath.Join(workspace, "main.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatalf("write main.txt: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "dir"), 0o755); err != nil {
		t.Fatalf("mkdir dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "dir", "nested.txt"), []byte("nested\n"), 0o644); err != nil {
		t.Fatalf("write nested.txt: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".git", "HEAD"), []byte("git-state\n"), 0o644); err != nil {
		t.Fatalf("write .git/HEAD: %v", err)
	}

	meta, err := manager.Create(workspace, "before-change")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if meta.FileCount != 2 {
		t.Fatalf("expected 2 workspace files in checkpoint, got %d", meta.FileCount)
	}

	if err := os.WriteFile(filepath.Join(workspace, "main.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("rewrite main.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write new.txt: %v", err)
	}

	items, err := manager.List(workspace)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != meta.ID {
		t.Fatalf("unexpected checkpoints: %#v", items)
	}

	restored, err := manager.Rollback(workspace, meta.ID)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if restored.ID != meta.ID {
		t.Fatalf("unexpected restored checkpoint: %#v", restored)
	}

	data, err := os.ReadFile(filepath.Join(workspace, "main.txt"))
	if err != nil {
		t.Fatalf("read restored main.txt: %v", err)
	}
	if string(data) != "v1\n" {
		t.Fatalf("expected main.txt to be restored, got %q", string(data))
	}
	if _, err := os.Stat(filepath.Join(workspace, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected new.txt to be removed after rollback, err=%v", err)
	}
	gitData, err := os.ReadFile(filepath.Join(workspace, ".git", "HEAD"))
	if err != nil {
		t.Fatalf("read .git/HEAD: %v", err)
	}
	if string(gitData) != "git-state\n" {
		t.Fatalf("expected .git to remain untouched, got %q", string(gitData))
	}
}

func TestCheckpointResolveLatestAndByName(t *testing.T) {
	workspace := t.TempDir()
	manager := &CheckpointManager{
		Root: filepath.Join(t.TempDir(), "checkpoints"),
	}
	if err := os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("one"), 0o644); err != nil {
		t.Fatalf("write file.txt: %v", err)
	}
	first, err := manager.Create(workspace, "alpha")
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("two"), 0o644); err != nil {
		t.Fatalf("rewrite file.txt: %v", err)
	}
	second, err := manager.Create(workspace, "beta")
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}

	latest, _, err := manager.Resolve(workspace, "latest")
	if err != nil {
		t.Fatalf("Resolve latest: %v", err)
	}
	if latest.ID != second.ID {
		t.Fatalf("expected latest checkpoint %s, got %s", second.ID, latest.ID)
	}
	byName, _, err := manager.Resolve(workspace, "alpha")
	if err != nil {
		t.Fatalf("Resolve by name: %v", err)
	}
	if byName.ID != first.ID {
		t.Fatalf("expected alpha checkpoint %s, got %s", first.ID, byName.ID)
	}
}

func TestCheckpointDiffAndPartialRollback(t *testing.T) {
	workspace := t.TempDir()
	manager := &CheckpointManager{
		Root: filepath.Join(t.TempDir(), "checkpoints"),
	}
	if err := os.WriteFile(filepath.Join(workspace, "main.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatalf("write main.txt: %v", err)
	}
	meta, err := manager.Create(workspace, "base")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "main.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("rewrite main.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write new.txt: %v", err)
	}

	_, diffs, err := manager.Diff(workspace, meta.ID, []string{"main.txt"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diffs) != 1 || diffs[0].Path != "main.txt" {
		t.Fatalf("unexpected diff set: %#v", diffs)
	}
	if !strings.Contains(renderCheckpointDiff(diffs), "+   1 | v2") {
		t.Fatalf("expected rendered diff to include updated content, got %q", renderCheckpointDiff(diffs))
	}

	_, err = manager.RollbackPaths(workspace, meta.ID, []string{"main.txt"})
	if err != nil {
		t.Fatalf("RollbackPaths main.txt: %v", err)
	}
	mainData, err := os.ReadFile(filepath.Join(workspace, "main.txt"))
	if err != nil {
		t.Fatalf("read main.txt: %v", err)
	}
	if string(mainData) != "v1\n" {
		t.Fatalf("expected main.txt restored, got %q", string(mainData))
	}
	if _, err := os.Stat(filepath.Join(workspace, "new.txt")); err != nil {
		t.Fatalf("expected unrelated file to remain after partial restore, err=%v", err)
	}

	_, err = manager.RollbackPaths(workspace, meta.ID, []string{"new.txt"})
	if err != nil {
		t.Fatalf("RollbackPaths new.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected new.txt to be removed by partial rollback, err=%v", err)
	}
}

func TestHelpTextIncludesCheckpointCommands(t *testing.T) {
	help := HelpText()
	for _, needle := range []string{"/checkpoint [note]", "/checkpoint-auto [on|off]", "/checkpoint-diff [target] [-- path[,path2]]", "/checkpoints", "/rollback [target]"} {
		if !strings.Contains(help, needle) {
			t.Fatalf("expected help to include %q", needle)
		}
	}
}
