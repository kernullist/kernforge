package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type CheckpointMetadata struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Workspace  string    `json:"workspace"`
	CreatedAt  time.Time `json:"created_at"`
	FileCount  int       `json:"file_count"`
	TotalBytes int64     `json:"total_bytes"`
}

type CheckpointManager struct {
	Root string
}

type CheckpointDiffEntry struct {
	Path   string
	Before []byte
	After  []byte
}

func NewCheckpointManager() *CheckpointManager {
	return &CheckpointManager{
		Root: filepath.Join(userConfigDir(), "checkpoints"),
	}
}

func (m *CheckpointManager) Create(workspaceRoot, name string) (CheckpointMetadata, error) {
	root, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil {
		return CheckpointMetadata{}, err
	}
	now := time.Now()
	id := fmt.Sprintf("%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/1_000_000)
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		trimmedName = "Checkpoint " + id
	}

	dir := m.checkpointDir(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return CheckpointMetadata{}, err
	}
	meta := CheckpointMetadata{
		ID:        id,
		Name:      trimmedName,
		Workspace: root,
		CreatedAt: now,
	}

	snapshotPath := filepath.Join(dir, "snapshot.zip")
	fileCount, totalBytes, err := writeWorkspaceSnapshot(root, snapshotPath)
	if err != nil {
		return CheckpointMetadata{}, err
	}
	meta.FileCount = fileCount
	meta.TotalBytes = totalBytes

	if err := writeCheckpointMetadata(filepath.Join(dir, "metadata.json"), meta); err != nil {
		return CheckpointMetadata{}, err
	}
	return meta, nil
}

func (m *CheckpointManager) List(workspaceRoot string) ([]CheckpointMetadata, error) {
	root, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil {
		return nil, err
	}
	base := m.workspaceDir(root)
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	items := make([]CheckpointMetadata, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := readCheckpointMetadata(filepath.Join(base, entry.Name(), "metadata.json"))
		if err != nil {
			continue
		}
		items = append(items, meta)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (m *CheckpointManager) Resolve(workspaceRoot, target string) (CheckpointMetadata, string, error) {
	items, err := m.List(workspaceRoot)
	if err != nil {
		return CheckpointMetadata{}, "", err
	}
	if len(items) == 0 {
		return CheckpointMetadata{}, "", fmt.Errorf("no checkpoints found")
	}
	query := strings.TrimSpace(target)
	if query == "" || strings.EqualFold(query, "latest") {
		meta := items[0]
		return meta, m.checkpointDir(meta.Workspace, meta.ID), nil
	}
	for _, item := range items {
		if strings.EqualFold(item.ID, query) || strings.EqualFold(item.Name, query) {
			return item, m.checkpointDir(item.Workspace, item.ID), nil
		}
	}
	return CheckpointMetadata{}, "", fmt.Errorf("checkpoint not found: %s", query)
}

func (m *CheckpointManager) Rollback(workspaceRoot, target string) (CheckpointMetadata, error) {
	meta, dir, err := m.Resolve(workspaceRoot, target)
	if err != nil {
		return CheckpointMetadata{}, err
	}
	snapshotPath := filepath.Join(dir, "snapshot.zip")
	if err := restoreWorkspaceSnapshot(meta.Workspace, snapshotPath); err != nil {
		return CheckpointMetadata{}, err
	}
	return meta, nil
}

func (m *CheckpointManager) RollbackPaths(workspaceRoot, target string, paths []string) (CheckpointMetadata, error) {
	meta, dir, err := m.Resolve(workspaceRoot, target)
	if err != nil {
		return CheckpointMetadata{}, err
	}
	snapshotPath := filepath.Join(dir, "snapshot.zip")
	if err := restoreWorkspaceSnapshotPaths(meta.Workspace, snapshotPath, paths); err != nil {
		return CheckpointMetadata{}, err
	}
	return meta, nil
}

func (m *CheckpointManager) Diff(workspaceRoot, target string, paths []string) (CheckpointMetadata, []CheckpointDiffEntry, error) {
	meta, dir, err := m.Resolve(workspaceRoot, target)
	if err != nil {
		return CheckpointMetadata{}, nil, err
	}
	snapshotPath := filepath.Join(dir, "snapshot.zip")
	entries, err := diffWorkspaceSnapshot(meta.Workspace, snapshotPath, paths)
	if err != nil {
		return CheckpointMetadata{}, nil, err
	}
	return meta, entries, nil
}

func (m *CheckpointManager) workspaceDir(workspaceRoot string) string {
	return filepath.Join(m.Root, workspaceKey(workspaceRoot))
}

func (m *CheckpointManager) checkpointDir(workspaceRoot, id string) string {
	return filepath.Join(m.workspaceDir(workspaceRoot), id)
}

func workspaceKey(workspaceRoot string) string {
	clean := filepath.Clean(workspaceRoot)
	base := filepath.Base(clean)
	if strings.TrimSpace(base) == "" || base == "." || base == string(filepath.Separator) {
		base = "workspace"
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.ToLower(clean)))
	return fmt.Sprintf("%s-%x", sanitizeCheckpointName(base), h.Sum64())
}

func sanitizeCheckpointName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return "workspace"
	}
	var b strings.Builder
	for _, ch := range name {
		switch {
		case ch >= 'a' && ch <= 'z':
			b.WriteRune(ch)
		case ch >= '0' && ch <= '9':
			b.WriteRune(ch)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "workspace"
	}
	return out
}

func writeCheckpointMetadata(path string, meta CheckpointMetadata) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func readCheckpointMetadata(path string) (CheckpointMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CheckpointMetadata{}, err
	}
	var meta CheckpointMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return CheckpointMetadata{}, err
	}
	return meta, nil
}

func writeWorkspaceSnapshot(root, zipPath string) (int, int64, error) {
	if err := os.MkdirAll(filepath.Dir(zipPath), 0o755); err != nil {
		return 0, 0, err
	}
	file, err := os.Create(zipPath)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	defer writer.Close()

	count := 0
	var totalBytes int64
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if shouldSkipCheckpointDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = rel
		header.Method = zip.Deflate
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(entry, src)
		closeErr := src.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		count++
		totalBytes += n
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	if err := writer.Close(); err != nil {
		return 0, 0, err
	}
	return count, totalBytes, nil
}

func restoreWorkspaceSnapshot(root, zipPath string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	expected := map[string]*zip.File{}
	for _, file := range reader.File {
		cleanName := filepath.Clean(filepath.FromSlash(file.Name))
		if cleanName == "." || strings.HasPrefix(cleanName, "..") {
			return fmt.Errorf("invalid snapshot entry: %s", file.Name)
		}
		expected[filepath.ToSlash(cleanName)] = file
	}

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if shouldSkipCheckpointDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := expected[rel]; !ok {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	for rel, file := range expected {
		target := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		dst, err := os.Create(target)
		if err != nil {
			src.Close()
			return err
		}
		_, copyErr := io.Copy(dst, src)
		closeDstErr := dst.Close()
		closeSrcErr := src.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeDstErr != nil {
			return closeDstErr
		}
		if closeSrcErr != nil {
			return closeSrcErr
		}
	}

	cleanupEmptyDirs(root)
	return nil
}

func restoreWorkspaceSnapshotPaths(root, zipPath string, paths []string) error {
	entries, err := readSnapshotEntries(zipPath)
	if err != nil {
		return err
	}
	targets, err := normalizeCheckpointTargetPaths(root, paths)
	if err != nil {
		return err
	}
	for _, rel := range targets {
		target := filepath.Join(root, filepath.FromSlash(rel))
		data, ok := entries[rel]
		if ok {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(target, data, 0o644); err != nil {
				return err
			}
			continue
		}
		if _, err := os.Stat(target); err == nil {
			if err := os.Remove(target); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("path not found in checkpoint or workspace: %s", rel)
	}
	cleanupEmptyDirs(root)
	return nil
}

func diffWorkspaceSnapshot(root, zipPath string, paths []string) ([]CheckpointDiffEntry, error) {
	entries, err := readSnapshotEntries(zipPath)
	if err != nil {
		return nil, err
	}
	filtered, err := normalizeCheckpointTargetPaths(root, paths)
	if err != nil {
		return nil, err
	}
	filterSet := map[string]bool{}
	for _, item := range filtered {
		filterSet[item] = true
	}

	current, err := readWorkspaceEntries(root)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var keys []string
	for key := range entries {
		if len(filterSet) > 0 && !filterSet[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	for key := range current {
		if len(filterSet) > 0 && !filterSet[key] {
			continue
		}
		if seen[key] {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var diffs []CheckpointDiffEntry
	for _, key := range keys {
		before := entries[key]
		after := current[key]
		if bytes.Equal(before, after) {
			continue
		}
		diffs = append(diffs, CheckpointDiffEntry{
			Path:   key,
			Before: before,
			After:  after,
		})
	}
	return diffs, nil
}

func readSnapshotEntries(zipPath string) (map[string][]byte, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	out := map[string][]byte{}
	for _, file := range reader.File {
		cleanName := filepath.Clean(filepath.FromSlash(file.Name))
		if cleanName == "." || strings.HasPrefix(cleanName, "..") {
			return nil, fmt.Errorf("invalid snapshot entry: %s", file.Name)
		}
		src, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(src)
		closeErr := src.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		out[filepath.ToSlash(cleanName)] = data
	}
	return out, nil
}

func readWorkspaceEntries(root string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if d.IsDir() {
			if shouldSkipCheckpointDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeCheckpointTargetPaths(root string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(rootAbs, path)
		}
		abs, err = filepath.Abs(abs)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(rootAbs, abs)
		if err != nil {
			return nil, err
		}
		if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("path is outside the workspace root: %s", path)
		}
		normalized := filepath.ToSlash(rel)
		if !seen[normalized] {
			seen[normalized] = true
			out = append(out, normalized)
		}
	}
	sort.Strings(out)
	return out, nil
}

func renderCheckpointDiff(entries []CheckpointDiffEntry) string {
	if len(entries) == 0 {
		return "(no diff)"
	}
	var blocks []string
	for _, entry := range entries {
		before := entry.Before
		after := entry.After
		if (!isText(before) && len(before) > 0) || (!isText(after) && len(after) > 0) {
			blocks = append(blocks, fmt.Sprintf("Preview for %s\n[binary change omitted]", entry.Path))
			continue
		}
		blocks = append(blocks, buildEditPreview(entry.Path, string(before), string(after)))
	}
	return strings.Join(blocks, "\n\n")
}

func renderCheckpointDiffTerminal(ui UI, entries []CheckpointDiffEntry) string {
	var lines []string
	lines = append(lines, ui.outputHeader("diff", checkpointDiffSummary(entries), ui.accent2))
	if len(entries) == 0 {
		lines = append(lines, ui.progressLine(ui.dim("(no diff)")))
		return strings.Join(lines, "\n")
	}

	for _, entry := range entries {
		lines = append(lines, "")
		lines = append(lines, ui.subsection(fmt.Sprintf("%s [%s]", entry.Path, checkpointDiffKind(entry))))

		meta := checkpointDiffMeta(entry)
		if meta != "" {
			lines = append(lines, ui.progressLine(ui.dim(meta)))
		}

		before := entry.Before
		after := entry.After
		if (!isText(before) && len(before) > 0) || (!isText(after) && len(after) > 0) {
			lines = append(lines, indentBlock("[binary change omitted]", "    "))
			continue
		}

		preview := trimCheckpointDiffPreview(buildEditPreview(entry.Path, string(before), string(after)))
		lines = append(lines, indentBlock(preview, "    "))
	}
	return strings.Join(lines, "\n")
}

func checkpointDiffSummary(entries []CheckpointDiffEntry) string {
	if len(entries) == 0 {
		return "0 file(s)"
	}
	added := 0
	deleted := 0
	modified := 0
	binary := 0
	for _, entry := range entries {
		switch checkpointDiffKind(entry) {
		case "added":
			added++
		case "deleted":
			deleted++
		case "binary":
			binary++
		default:
			modified++
		}
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("%d file(s)", len(entries)))
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", modified))
	}
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d added", added))
	}
	if deleted > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", deleted))
	}
	if binary > 0 {
		parts = append(parts, fmt.Sprintf("%d binary", binary))
	}
	return strings.Join(parts, " | ")
}

func checkpointDiffKind(entry CheckpointDiffEntry) string {
	before := entry.Before
	after := entry.After
	if (!isText(before) && len(before) > 0) || (!isText(after) && len(after) > 0) {
		return "binary"
	}
	switch {
	case len(before) == 0 && len(after) > 0:
		return "added"
	case len(before) > 0 && len(after) == 0:
		return "deleted"
	default:
		return "modified"
	}
}

func checkpointDiffMeta(entry CheckpointDiffEntry) string {
	before := entry.Before
	after := entry.After
	if checkpointDiffKind(entry) == "binary" {
		return fmt.Sprintf("change=binary  before=%d byte(s)  after=%d byte(s)", len(before), len(after))
	}
	return fmt.Sprintf(
		"change=%s  before=%d line(s)  after=%d line(s)",
		checkpointDiffKind(entry),
		countBlockLines(string(before)),
		countBlockLines(string(after)),
	)
}

func trimCheckpointDiffPreview(preview string) string {
	lines := strings.Split(preview, "\n")
	if len(lines) == 0 {
		return preview
	}
	if strings.HasPrefix(lines[0], "Preview for ") {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

func shouldSkipCheckpointDir(name string) bool {
	return strings.EqualFold(name, ".git")
}

func cleanupEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root || !d.IsDir() {
			return nil
		}
		if shouldSkipCheckpointDir(d.Name()) {
			return filepath.SkipDir
		}
		dirs = append(dirs, path)
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			continue
		}
		_ = os.Remove(dir)
	}
}
