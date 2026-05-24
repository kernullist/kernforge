package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultAgentsMDFilename  = "AGENTS.md"
	localAgentsMDFilename    = "AGENTS.override.md"
	defaultProjectRootMarker = ".git"
	agentsMDMaxBytes         = 32 * 1024
	agentsMDSeparator        = "\n\n--- project-doc ---\n\n"
)

func (a *Agent) agentsMDPromptSection() string {
	if a == nil || a.Session == nil {
		return ""
	}
	cwd := a.agentsMDPromptCWD()
	if cwd == "" {
		return ""
	}
	global := strings.TrimSpace(loadGlobalAgentsMD(userConfigDir()))
	project := a.projectAgentsMDContents()
	if global == "" && strings.TrimSpace(project) == "" {
		return ""
	}
	contents := global
	if strings.TrimSpace(project) != "" {
		if contents != "" {
			contents += agentsMDSeparator
		}
		contents += project
	}
	return fmt.Sprintf("# AGENTS.md instructions for %s\n\n<INSTRUCTIONS>\n%s\n</INSTRUCTIONS>", cwd, contents)
}

func (a *Agent) projectAgentsMDPromptSection() string {
	cwd := a.agentsMDPromptCWD()
	if cwd == "" {
		return ""
	}
	contents := a.projectAgentsMDContents()
	if strings.TrimSpace(contents) == "" {
		return ""
	}
	return fmt.Sprintf("# AGENTS.md instructions for %s\n\n<INSTRUCTIONS>\n%s\n</INSTRUCTIONS>", cwd, contents)
}

func (a *Agent) projectAgentsMDContents() string {
	if a == nil || a.Session == nil {
		return ""
	}
	cwd := a.agentsMDPromptCWD()
	if cwd == "" {
		return ""
	}
	root := projectRootFromMarkers(cwd, projectRootMarkers(a.Config))
	maxBytes := agentsMDMaxBytes
	if a.Config.ProjectDocMaxBytes != nil {
		maxBytes = *a.Config.ProjectDocMaxBytes
	}
	return loadProjectAgentsMD(root, cwd, maxBytes, a.Config.ProjectDocFallbackFilenames)
}

func (a *Agent) agentsMDPromptCWD() string {
	if a == nil || a.Session == nil {
		return ""
	}
	cwd := strings.TrimSpace(a.Session.WorkingDir)
	if cwd == "" {
		cwd = strings.TrimSpace(workspaceEffectiveActiveRoot(a.Workspace, a.Session))
	}
	return cwd
}

func loadGlobalAgentsMD(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	for _, name := range []string{localAgentsMDFilename, defaultAgentsMDFilename} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || !agentsMDFileIsRegular(info) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := strings.TrimSpace(strings.ToValidUTF8(string(data), "\uFFFD"))
		if text != "" {
			return text
		}
	}
	return ""
}

func loadProjectAgentsMD(root string, cwd string, maxBytes int, fallbackNames []string) string {
	if maxBytes <= 0 {
		return ""
	}
	searchDirs := agentsMDSearchDirs(root, cwd)
	if len(searchDirs) == 0 {
		return ""
	}
	remaining := maxBytes
	parts := make([]string, 0, len(searchDirs))
	for _, dir := range searchDirs {
		if remaining <= 0 {
			break
		}
		path := firstAgentsMDPath(dir, fallbackNames)
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if len(data) > remaining {
			data = data[:remaining]
		}
		text := strings.ToValidUTF8(string(data), "\uFFFD")
		if strings.TrimSpace(text) == "" {
			continue
		}
		parts = append(parts, text)
		remaining -= len(data)
	}
	return strings.Join(parts, "\n\n")
}

func projectRootMarkers(cfg Config) []string {
	if cfg.ProjectRootMarkers == nil {
		return []string{defaultProjectRootMarker}
	}
	return append([]string(nil), (*cfg.ProjectRootMarkers)...)
}

func agentsMDSearchDirs(root string, cwd string) []string {
	cwd = cleanAbsPath(cwd)
	root = cleanAbsPath(root)
	if cwd == "" {
		return nil
	}
	if root == "" {
		return []string{cwd}
	}
	if !pathContains(root, cwd) {
		return []string{cwd}
	}
	var reversed []string
	cursor := cwd
	for {
		reversed = append(reversed, cursor)
		if samePath(cursor, root) {
			break
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			break
		}
		cursor = parent
	}
	dirs := make([]string, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		dirs = append(dirs, reversed[i])
	}
	return dirs
}

func firstAgentsMDPath(dir string, fallbackNames []string) string {
	for _, name := range candidateAgentsMDFilenames(fallbackNames) {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || !agentsMDFileIsRegular(info) {
			continue
		}
		return path
	}
	return ""
}

func agentsMDFileIsRegular(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	return info.Mode().IsRegular()
}

func candidateAgentsMDFilenames(fallbackNames []string) []string {
	names := []string{localAgentsMDFilename, defaultAgentsMDFilename}
	seen := map[string]struct{}{
		strings.ToLower(localAgentsMDFilename):   {},
		strings.ToLower(defaultAgentsMDFilename): {},
	}
	for _, name := range normalizeProjectDocFallbackNames(fallbackNames) {
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	return names
}

func normalizeProjectDocFallbackNames(names []string) []string {
	normalized := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || filepath.IsAbs(name) || filepath.Base(name) != name {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, name)
	}
	return normalized
}

func cleanAbsPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func pathContains(root string, child string) bool {
	root = cleanAbsPath(root)
	child = cleanAbsPath(child)
	if root == "" || child == "" {
		return false
	}
	if samePath(root, child) {
		return true
	}
	rel, err := filepath.Rel(root, child)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}
