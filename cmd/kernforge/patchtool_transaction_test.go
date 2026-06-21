package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tooling-3: applyPatchDocument must be transactional. A multi-file apply that
// fails partway must leave the tree exactly as it was found: no partial writes,
// no half-finished move, no duplicated file. These tests drive the real apply
// path (plan + execute) through a bare Workspace whose review/permission gates
// no-op, then assert the on-disk result.

// readFileString reads a file and fails the test if it cannot be read.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	return string(data)
}

// TestApplyPatchDocumentRollsBackFailedMoveLeavesNoDuplicate simulates the
// exact data-integrity hazard from the audit: a Move (Update + Move to) whose
// source removal fails after the destination has been written. Without the
// transaction this leaves a duplicated file (both src and dest present); with
// it, the destination write is rolled back so only the original source remains.
func TestApplyPatchDocumentRollsBackFailedMoveLeavesNoDuplicate(t *testing.T) {
	base := t.TempDir()
	srcPath := filepath.Join(base, "src.txt")
	destPath := filepath.Join(base, "dest.txt")
	const srcContent = "alpha\nbeta\n"
	if err := os.WriteFile(srcPath, []byte(srcContent), 0o644); err != nil {
		t.Fatalf("WriteFile src: %v", err)
	}

	// Inject a deterministic failure when the apply tries to remove the move
	// source, then restore the real remover so other tests are unaffected.
	wantErr := errors.New("injected remove failure")
	prev := patchFileRemove
	patchFileRemove = func(path string) error {
		if path == srcPath {
			return wantErr
		}
		return prev(path)
	}
	t.Cleanup(func() { patchFileRemove = prev })

	patch := "*** Begin Patch\n" +
		"*** Update File: src.txt\n" +
		"*** Move to: dest.txt\n" +
		"@@\n" +
		"-alpha\n" +
		"+gamma\n" +
		" beta\n" +
		"*** End Patch\n"
	doc, err := parsePatchDocument(patch)
	if err != nil {
		t.Fatalf("parsePatchDocument: %v", err)
	}

	ws := Workspace{BaseRoot: base, Root: base}
	_, mutated, changedPaths, _, applyErr := applyPatchDocument(context.Background(), ws, doc, "")
	if applyErr == nil {
		t.Fatalf("expected the failed move to return an error")
	}
	if !errors.Is(applyErr, wantErr) {
		t.Fatalf("expected the injected remove error, got %v", applyErr)
	}
	// A rolled-back apply must report it did not mutate the workspace.
	if mutated {
		t.Fatalf("expected mutated=false after rollback, got true (changedPaths=%v)", changedPaths)
	}
	// The source must still hold its original content (the move was undone).
	if got := readFileString(t, srcPath); got != srcContent {
		t.Fatalf("source content changed after rollback.\n got: %q\nwant: %q", got, srcContent)
	}
	// The destination must not exist: the rolled-back write was removed, so there
	// is no duplicated file.
	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Fatalf("expected destination to be absent after rollback, stat err=%v", err)
	}
}

// TestApplyPatchDocumentRollsBackEarlierFileOnLaterFailure covers the second
// hazard: an earlier op committed to disk while a later op fails. The first
// file's add and the second file's update must both be reverted, restoring the
// pre-apply tree exactly.
func TestApplyPatchDocumentRollsBackEarlierFileOnLaterFailure(t *testing.T) {
	base := t.TempDir()
	addedPath := filepath.Join(base, "added.txt")
	updatedPath := filepath.Join(base, "existing.txt")
	const updatedOriginal = "one\ntwo\n"
	if err := os.WriteFile(updatedPath, []byte(updatedOriginal), 0o644); err != nil {
		t.Fatalf("WriteFile existing: %v", err)
	}

	// The third op is a delete whose removal is forced to fail, after the add
	// (op 1) and the update (op 2) have already been written to disk.
	deletedPath := filepath.Join(base, "doomed.txt")
	if err := os.WriteFile(deletedPath, []byte("remove me\n"), 0o644); err != nil {
		t.Fatalf("WriteFile doomed: %v", err)
	}
	wantErr := errors.New("injected delete failure")
	prev := patchFileRemove
	patchFileRemove = func(path string) error {
		if path == deletedPath {
			return wantErr
		}
		return prev(path)
	}
	t.Cleanup(func() { patchFileRemove = prev })

	patch := "*** Begin Patch\n" +
		"*** Add File: added.txt\n" +
		"+new file line\n" +
		"*** Update File: existing.txt\n" +
		"@@\n" +
		"-one\n" +
		"+ONE\n" +
		" two\n" +
		"*** Delete File: doomed.txt\n" +
		"*** End Patch\n"
	doc, err := parsePatchDocument(patch)
	if err != nil {
		t.Fatalf("parsePatchDocument: %v", err)
	}

	ws := Workspace{BaseRoot: base, Root: base}
	_, mutated, _, _, applyErr := applyPatchDocument(context.Background(), ws, doc, "")
	if applyErr == nil {
		t.Fatalf("expected the failed delete to return an error")
	}
	if !errors.Is(applyErr, wantErr) {
		t.Fatalf("expected the injected delete error, got %v", applyErr)
	}
	if mutated {
		t.Fatalf("expected mutated=false after rollback")
	}
	// Op 1's added file must have been removed by rollback.
	if _, err := os.Stat(addedPath); !os.IsNotExist(err) {
		t.Fatalf("expected added file to be removed by rollback, stat err=%v", err)
	}
	// Op 2's updated file must hold its original (pre-apply) content again.
	if got := readFileString(t, updatedPath); got != updatedOriginal {
		t.Fatalf("updated file not restored on rollback.\n got: %q\nwant: %q", got, updatedOriginal)
	}
	// Op 3's target was never removed (its removal is what failed), so it stays.
	if got := readFileString(t, deletedPath); got != "remove me\n" {
		t.Fatalf("delete target unexpectedly changed: %q", got)
	}
}

// TestApplyPatchDocumentRejectsForbiddenWriteWithoutMutating proves the
// pre-validation phase: when one op cannot land (its parent path component is
// an existing regular file), the whole apply is rejected before any file is
// written, so an earlier op in the same patch is never committed.
func TestApplyPatchDocumentRejectsForbiddenWriteWithoutMutating(t *testing.T) {
	base := t.TempDir()
	updatedPath := filepath.Join(base, "keep.txt")
	const keepOriginal = "keep one\nkeep two\n"
	if err := os.WriteFile(updatedPath, []byte(keepOriginal), 0o644); err != nil {
		t.Fatalf("WriteFile keep: %v", err)
	}
	// "blocker" is a regular file, so an add under "blocker/child.txt" cannot
	// create its parent directory.
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("i am a file\n"), 0o644); err != nil {
		t.Fatalf("WriteFile blocker: %v", err)
	}

	patch := "*** Begin Patch\n" +
		"*** Update File: keep.txt\n" +
		"@@\n" +
		"-keep one\n" +
		"+KEEP ONE\n" +
		" keep two\n" +
		"*** Add File: blocker/child.txt\n" +
		"+nested add\n" +
		"*** End Patch\n"
	doc, err := parsePatchDocument(patch)
	if err != nil {
		t.Fatalf("parsePatchDocument: %v", err)
	}

	ws := Workspace{BaseRoot: base, Root: base}
	_, mutated, _, _, applyErr := applyPatchDocument(context.Background(), ws, doc, "")
	if applyErr == nil {
		t.Fatalf("expected the forbidden write to be rejected")
	}
	if mutated {
		t.Fatalf("expected mutated=false when pre-validation rejects the apply")
	}
	// The update that was planned first must not have been applied.
	if got := readFileString(t, updatedPath); got != keepOriginal {
		t.Fatalf("earlier file was mutated despite a rejected apply.\n got: %q\nwant: %q", got, keepOriginal)
	}
	// No child file was created under the blocker.
	if _, err := os.Stat(filepath.Join(base, "blocker", "child.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected no nested child file, stat err=%v", err)
	}
}

// TestApplyPatchDocumentTransactionalSuccessCommitsAll confirms the happy path
// still fully applies a multi-op patch (add + update + move) so the transaction
// does not regress normal behavior.
func TestApplyPatchDocumentTransactionalSuccessCommitsAll(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "edit.txt"), []byte("x := 1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile edit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "mover.txt"), []byte("payload\n"), 0o644); err != nil {
		t.Fatalf("WriteFile mover: %v", err)
	}

	patch := "*** Begin Patch\n" +
		"*** Add File: fresh.txt\n" +
		"+hello\n" +
		"*** Update File: edit.txt\n" +
		"@@\n" +
		"-x := 1\n" +
		"+x := 2\n" +
		"*** Update File: mover.txt\n" +
		"*** Move to: moved.txt\n" +
		"@@\n" +
		"-payload\n" +
		"+payload v2\n" +
		"*** End Patch\n"
	doc, err := parsePatchDocument(patch)
	if err != nil {
		t.Fatalf("parsePatchDocument: %v", err)
	}

	ws := Workspace{BaseRoot: base, Root: base}
	_, mutated, _, _, applyErr := applyPatchDocument(context.Background(), ws, doc, "")
	if applyErr != nil {
		t.Fatalf("expected success, got %v", applyErr)
	}
	if !mutated {
		t.Fatalf("expected mutated=true on a successful apply")
	}
	if got := readFileString(t, filepath.Join(base, "fresh.txt")); got != "hello\n" {
		t.Fatalf("added file content wrong: %q", got)
	}
	if got := readFileString(t, filepath.Join(base, "edit.txt")); got != "x := 2\n" {
		t.Fatalf("updated file content wrong: %q", got)
	}
	if got := readFileString(t, filepath.Join(base, "moved.txt")); got != "payload v2\n" {
		t.Fatalf("moved file content wrong: %q", got)
	}
	if _, err := os.Stat(filepath.Join(base, "mover.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected move source to be gone, stat err=%v", err)
	}
}

// TestAtomicWriteFileReplacesExistingContent guards the write primitive the
// transaction relies on: an existing file is replaced wholesale (no leftover
// tail bytes from longer prior content).
func TestAtomicWriteFileReplacesExistingContent(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "atomic.txt")
	if err := atomicWriteFile(target, []byte("a long original line of content\n"), 0o644); err != nil {
		t.Fatalf("first atomicWriteFile: %v", err)
	}
	if err := atomicWriteFile(target, []byte("short\n"), 0o644); err != nil {
		t.Fatalf("second atomicWriteFile: %v", err)
	}
	if got := readFileString(t, target); got != "short\n" {
		t.Fatalf("expected wholesale replacement, got %q", got)
	}
	// No stray temp files left behind in the directory.
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("leftover temp file after atomic write: %s", e.Name())
		}
	}
}
