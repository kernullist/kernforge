package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveDirWithRetry locks the worktree-teardown helper: it deletes a
// populated directory and treats an already-missing directory as success (the
// retry loop's terminal state on Windows where a lingering handle is released).
func TestRemoveDirWithRetry(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "wt")
	if err := os.MkdirAll(filepath.Join(sub, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeDirWithRetry(sub); err != nil {
		t.Fatalf("removeDirWithRetry: %v", err)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Fatalf("directory should be gone, stat err=%v", err)
	}
	// A missing directory must be reported as success, not an error.
	if err := removeDirWithRetry(sub); err != nil {
		t.Fatalf("removing a missing directory must succeed, got %v", err)
	}
}
