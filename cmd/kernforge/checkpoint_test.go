package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestCheckpointCreateRetriesWhenTimestampIDCollides(t *testing.T) {
	workspace := t.TempDir()
	manager := &CheckpointManager{
		Root: filepath.Join(t.TempDir(), "checkpoints"),
	}
	if err := os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("one"), 0o644); err != nil {
		t.Fatalf("write file.txt: %v", err)
	}

	originalNow := checkpointTimeNow
	t.Cleanup(func() {
		checkpointTimeNow = originalNow
	})

	baseTime := time.Date(2026, 4, 20, 12, 0, 0, 123456700, time.UTC)
	callCount := 0
	checkpointTimeNow = func() time.Time {
		callCount++
		if callCount <= 2 {
			return baseTime
		}
		return baseTime.Add(time.Nanosecond)
	}

	first, err := manager.Create(workspace, "alpha")
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	second, err := manager.Create(workspace, "beta")
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("expected unique checkpoint IDs after collision retry, got %s", first.ID)
	}

	items, err := manager.List(workspace)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected two checkpoints after collision retry, got %#v", items)
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

func TestCheckpointRejectsSnapshotEntryTraversal(t *testing.T) {
	workspace := t.TempDir()
	zipPath := filepath.Join(t.TempDir(), "snapshot.zip")
	if err := writeTestSnapshotZip(zipPath, []testSnapshotZipEntry{
		{Name: "../escape.txt", Body: "escape"},
	}); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	if err := restoreWorkspaceSnapshot(workspace, zipPath); err == nil || !strings.Contains(err.Error(), "invalid snapshot entry") {
		t.Fatalf("expected traversal snapshot entry to be rejected, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(workspace), "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("snapshot traversal wrote outside workspace, err=%v", err)
	}
}

func TestCheckpointRejectsSnapshotAbsoluteEntry(t *testing.T) {
	for _, name := range []string{"/abs.txt", `C:\abs.txt`} {
		t.Run(name, func(t *testing.T) {
			if _, err := cleanCheckpointSnapshotEntryName(name); err == nil {
				t.Fatalf("expected absolute snapshot entry %q to be rejected", name)
			}
		})
	}
}

func TestCheckpointRejectsSnapshotSymlinkEntry(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "snapshot.zip")
	if err := writeTestSnapshotZip(zipPath, []testSnapshotZipEntry{
		{Name: "link.txt", Body: "target.txt", Mode: os.ModeSymlink | 0o777},
	}); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	if _, err := readSnapshotEntries(zipPath); err == nil || !strings.Contains(err.Error(), "invalid snapshot entry type") {
		t.Fatalf("expected symlink snapshot entry to be rejected, got %v", err)
	}
}

func TestCheckpointRejectsDuplicateSnapshotEntry(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "snapshot.zip")
	if err := writeTestSnapshotZip(zipPath, []testSnapshotZipEntry{
		{Name: "dup.txt", Body: "one"},
		{Name: "dup.txt", Body: "two"},
	}); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	if _, err := readSnapshotEntries(zipPath); err == nil || !strings.Contains(err.Error(), "duplicate snapshot entry") {
		t.Fatalf("expected duplicate snapshot entry to be rejected, got %v", err)
	}
}

func TestCheckpointRejectsOversizedSnapshotEntry(t *testing.T) {
	header := zip.FileHeader{
		Name:               "huge.bin",
		UncompressedSize64: uint64(checkpointSnapshotMaxExtractedBytes) + 1,
	}
	header.SetMode(0o644)
	file := &zip.File{FileHeader: header}
	totalBytes := int64(0)
	if _, _, err := validateCheckpointSnapshotFile(file, &totalBytes); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected oversized snapshot entry to be rejected, got %v", err)
	}
}

func TestCheckpointRejectsEntryThatExceedsDeclaredSize(t *testing.T) {
	header := zip.FileHeader{
		Name:               "short.bin",
		UncompressedSize64: 1,
	}
	header.SetMode(0o644)
	file := &zip.File{FileHeader: header}
	if _, err := readCheckpointSnapshotFile(strings.NewReader("toolong"), file); err == nil || !strings.Contains(err.Error(), "exceeded declared size") {
		t.Fatalf("expected declared-size overflow to be rejected, got %v", err)
	}
}

type testSnapshotZipEntry struct {
	Name string
	Body string
	Mode os.FileMode
}

func writeTestSnapshotZip(path string, entries []testSnapshotZipEntry) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{
			Name:   entry.Name,
			Method: zip.Deflate,
		}
		mode := entry.Mode
		if mode == 0 {
			mode = 0o644
		}
		header.SetMode(mode)
		w, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			_ = file.Close()
			return err
		}
		if _, err := w.Write([]byte(entry.Body)); err != nil {
			_ = writer.Close()
			_ = file.Close()
			return err
		}
	}
	closeWriterErr := writer.Close()
	closeFileErr := file.Close()
	if closeWriterErr != nil {
		return closeWriterErr
	}
	return closeFileErr
}

func TestHelpTextIncludesCheckpointCommands(t *testing.T) {
	help := HelpTextAll()
	for _, needle := range []string{"/checkpoint [note]", "/checkpoint auto [on|off]", "/checkpoint diff [target] [-- path[,path2]]", "/checkpoint list", "/checkpoint rollback [target]"} {
		if !strings.Contains(help, needle) {
			t.Fatalf("expected help to include %q", needle)
		}
	}
}

func TestRenderCheckpointDiffTerminalUsesGroupedCards(t *testing.T) {
	ui := UI{color: false}
	rendered := renderCheckpointDiffTerminal(ui, []CheckpointDiffEntry{
		{
			Path:   "main.txt",
			Before: []byte("v1\n"),
			After:  []byte("v2\n"),
		},
		{
			Path:   "new.txt",
			Before: nil,
			After:  []byte("hello\n"),
		},
	})

	for _, needle := range []string{
		">> diff [2 file(s) | 1 modified | 1 added] ",
		"-- main.txt [modified] ",
		"change=modified  before=1 line(s)  after=1 line(s)",
		"--- before/main.txt",
		"-- new.txt [added] ",
		"change=added  before=0 line(s)  after=1 line(s)",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected terminal diff to contain %q, got %q", needle, rendered)
		}
	}
}
