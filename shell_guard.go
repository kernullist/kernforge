package main

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type workspaceFileSignature struct {
	Size    int64
	ModTime int64
}

func (w Workspace) EnsureScopedShellWrite(command string, writePaths []string) ([]string, error) {
	if len(writePaths) == 0 {
		return nil, fmt.Errorf("scoped shell writes require write_paths")
	}
	resolved := make([]string, 0, len(writePaths))
	for _, raw := range writePaths {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		path, err := w.Resolve(trimmed)
		if err != nil {
			return nil, err
		}
		if err := w.ensureProtectedEditPath(path); err != nil {
			return nil, err
		}
		resolved = append(resolved, path)
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("scoped shell writes require at least one valid write path")
	}
	if err := w.BeforeEdit("scoped mutating shell: " + summarizeShellCommand(command)); err != nil {
		return nil, err
	}
	if w.Perms == nil {
		return resolved, nil
	}
	relPaths := make([]string, 0, len(resolved))
	for _, path := range resolved {
		relPaths = append(relPaths, relOrAbs(w.Root, path))
	}
	ok, err := w.Perms.Allow(ActionShellWrite, fmt.Sprintf("%s (scoped to %s)", summarizeShellCommand(command), strings.Join(relPaths, ", ")))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("shell write permission denied")
	}
	return resolved, nil
}

func snapshotWorkspaceFiles(root string) (map[string]workspaceFileSignature, error) {
	snapshot := map[string]workspaceFileSignature{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := strings.ToLower(strings.TrimSpace(d.Name()))
			if name == ".git" || name == ".claude" {
				return filepath.SkipDir
			}
			if name == userConfigDirName {
				return filepath.SkipDir
			}
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.Clean(rel)
		snapshot[rel] = workspaceFileSignature{
			Size:    info.Size(),
			ModTime: info.ModTime().UnixNano(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func diffWorkspaceSnapshots(before map[string]workspaceFileSignature, after map[string]workspaceFileSignature) []string {
	changes := make([]string, 0)
	seen := map[string]struct{}{}
	for path, left := range before {
		right, ok := after[path]
		if !ok || left != right {
			changes = append(changes, filepath.Clean(path))
		}
		seen[path] = struct{}{}
	}
	for path := range after {
		if _, ok := seen[path]; ok {
			continue
		}
		changes = append(changes, filepath.Clean(path))
	}
	sort.Strings(changes)
	return changes
}

func verifyScopedShellWriteChanges(root string, before map[string]workspaceFileSignature, allowed []string) ([]string, error) {
	after, err := snapshotWorkspaceFiles(root)
	if err != nil {
		return nil, err
	}
	changes := diffWorkspaceSnapshots(before, after)
	if len(changes) == 0 {
		return nil, nil
	}
	allowedSet := normalizeAllowedWriteScopes(root, allowed)
	outside := make([]string, 0)
	for _, changed := range changes {
		if !pathMatchesAnyAllowedScope(changed, allowedSet) {
			outside = append(outside, changed)
		}
	}
	if len(outside) > 0 {
		return nil, fmt.Errorf("scoped shell write modified paths outside write_paths: %s", strings.Join(outside, ", "))
	}
	return changes, nil
}

func normalizeAllowedWriteScopes(root string, allowed []string) []string {
	scopes := make([]string, 0, len(allowed))
	for _, raw := range allowed {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		abs := trimmed
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, trimmed)
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		rel = filepath.Clean(rel)
		if rel == "." {
			scopes = append(scopes, rel)
			continue
		}
		scopes = append(scopes, rel)
	}
	return normalizeTaskStateList(scopes, 32)
}

func pathMatchesAnyAllowedScope(relPath string, allowed []string) bool {
	cleanRel := filepath.Clean(relPath)
	for _, scope := range allowed {
		cleanScope := filepath.Clean(scope)
		if cleanScope == "." {
			return true
		}
		if strings.EqualFold(cleanRel, cleanScope) {
			return true
		}
		prefix := cleanScope + string(filepath.Separator)
		if strings.HasPrefix(strings.ToLower(cleanRel), strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}

func formatScopedShellWriteSummary(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	trimmed := make([]string, 0, len(paths))
	for _, path := range paths {
		trimmed = append(trimmed, filepath.ToSlash(strings.TrimSpace(path)))
	}
	return strings.Join(trimmed, ", ")
}

func shellJobNow() time.Time {
	return time.Now()
}
