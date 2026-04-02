package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var slashCommands = []string{
	"help",
	"status",
	"provider",
	"profile",
	"version",
	"model",
	"permissions",
	"verify",
	"verify-dashboard",
	"verify-dashboard-html",
	"clear",
	"compact",
	"context",
	"memory",
	"mem",
	"mem-search",
	"mem-show",
	"mem-promote",
	"mem-demote",
	"mem-confirm",
	"mem-tentative",
	"mem-dashboard",
	"mem-dashboard-html",
	"mem-prune",
	"mem-stats",
	"checkpoint",
	"checkpoint-auto",
	"checkpoint-diff",
	"locale-auto",
	"checkpoints",
	"rollback",
	"skills",
	"mcp",
	"resources",
	"resource",
	"prompts",
	"prompt",
	"reload",
	"init",
	"open",
	"selection",
	"selections",
	"use-selection",
	"drop-selection",
	"note-selection",
	"tag-selection",
	"clear-selection",
	"clear-selections",
	"diff-selection",
	"review-selection",
	"review-selections",
	"edit-selection",
	"resume",
	"rename",
	"session",
	"sessions",
	"tasks",
	"diff",
	"export",
	"config",
	"set-plan-review",
	"do-plan-review",
	"profile-review",
	"set_max_tool_iterations",
	"exit",
}

func (rt *runtimeState) completeLine(buffer string) (string, []string, bool) {
	if completed, suggestions, ok := rt.completeSlashCommand(buffer); ok {
		return completed, suggestions, true
	}
	if completed, suggestions, ok := rt.completeMCPCommandTarget(buffer); ok {
		return completed, suggestions, true
	}
	if completed, suggestions, ok := rt.completeOpenPath(buffer); ok {
		return completed, suggestions, true
	}
	if completed, suggestions, ok := rt.completeShellPath(buffer); ok {
		return completed, suggestions, true
	}
	if completed, suggestions, ok := rt.completeMCPMention(buffer); ok {
		return completed, suggestions, true
	}
	if completed, suggestions, ok := rt.completeMentionPath(buffer); ok {
		return completed, suggestions, true
	}
	return buffer, nil, false
}

func (rt *runtimeState) completeSlashCommand(buffer string) (string, []string, bool) {
	trimmedLeft := strings.TrimLeft(buffer, " \t")
	if !strings.HasPrefix(trimmedLeft, "/") {
		return buffer, nil, false
	}
	commandText := strings.TrimPrefix(trimmedLeft, "/")
	if strings.Contains(commandText, " ") {
		return buffer, nil, false
	}
	leading := buffer[:len(buffer)-len(trimmedLeft)]
	partial := strings.ToLower(commandText)
	var matches []string
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd, partial) {
			matches = append(matches, cmd)
		}
	}
	if len(matches) == 0 {
		return buffer, nil, true
	}
	if len(matches) == 1 {
		return leading + "/" + matches[0] + " ", nil, true
	}
	prefix := longestCommonPrefix(matches)
	if len(prefix) > len(partial) {
		return leading + "/" + prefix, nil, true
	}
	suggestions := make([]string, 0, len(matches))
	for _, match := range matches {
		suggestions = append(suggestions, "/"+match)
	}
	return buffer, suggestions, true
}

func (rt *runtimeState) completeMentionPath(buffer string) (string, []string, bool) {
	atIndex := lastMentionStart(buffer)
	if atIndex < 0 {
		return buffer, nil, false
	}
	token := buffer[atIndex+1:]
	searchToken := normalizeTypedPath(token)
	dirPart, partial := splitTypedPath(searchToken)

	baseDir := "."
	if dirPart != "" {
		baseDir = dirPart
	}
	resolvedBase, err := rt.workspace.Resolve(baseDir)
	if err != nil {
		return buffer, nil, true
	}
	entries, err := os.ReadDir(resolvedBase)
	if err != nil {
		return buffer, nil, true
	}

	lowerPartial := strings.ToLower(partial)
	type candidate struct {
		display string
		dir     bool
	}
	var matches []candidate
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(strings.ToLower(name), lowerPartial) {
			continue
		}
		display := name
		if dirPart != "" {
			display = filepath.ToSlash(filepath.Join(dirPart, name))
		}
		if entry.IsDir() {
			display += "/"
		}
		matches = append(matches, candidate{
			display: display,
			dir:     entry.IsDir(),
		})
	}
	if len(matches) == 0 {
		return buffer, nil, true
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].display < matches[j].display })
	if len(matches) == 1 {
		replacement := "@" + matches[0].display
		if !matches[0].dir {
			replacement += " "
		}
		return buffer[:atIndex] + replacement, nil, true
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match.display)
	}
	common := longestCommonPrefixInsensitive(names)
	if len(common) > len(searchToken) {
		return buffer[:atIndex] + "@" + common, nil, true
	}
	suggestions := make([]string, 0, len(matches))
	for _, match := range matches {
		suggestions = append(suggestions, "@"+match.display)
	}
	return buffer, suggestions, true
}

func (rt *runtimeState) completeMCPMention(buffer string) (string, []string, bool) {
	atIndex := lastMentionStart(buffer)
	if atIndex < 0 {
		return buffer, nil, false
	}
	token := buffer[atIndex+1:]
	if !strings.HasPrefix(strings.ToLower(token), "mcp:") {
		return buffer, nil, false
	}
	replacement, suggestions, ok := rt.completeMCPQualifiedTarget(token)
	if !ok {
		return buffer, nil, true
	}
	if len(suggestions) > 0 {
		out := make([]string, 0, len(suggestions))
		for _, suggestion := range suggestions {
			out = append(out, "@"+suggestion)
		}
		return buffer, out, true
	}
	if replacement != token {
		if !strings.HasSuffix(replacement, ":") {
			replacement += " "
		}
		return buffer[:atIndex] + "@" + replacement, nil, true
	}
	return buffer, nil, true
}

func (rt *runtimeState) completeShellPath(buffer string) (string, []string, bool) {
	trimmedLeft := strings.TrimLeft(buffer, " \t")
	lower := strings.ToLower(trimmedLeft)
	command := ""
	dirsOnly := false
	switch {
	case strings.HasPrefix(lower, "!cd "):
		command = trimmedLeft[:4]
		dirsOnly = true
	case strings.HasPrefix(lower, "!ls "):
		command = trimmedLeft[:4]
	default:
		return buffer, nil, false
	}
	leading := buffer[:len(buffer)-len(trimmedLeft)]
	pathPart := trimmedLeft[len(command):]
	completed, suggestions, ok := rt.completeWorkspacePathFiltered(pathPart, dirsOnly)
	if !ok {
		return buffer, nil, true
	}
	if len(suggestions) > 0 {
		prefixed := make([]string, 0, len(suggestions))
		for _, s := range suggestions {
			prefixed = append(prefixed, command+s)
		}
		return buffer, prefixed, true
	}
	return leading + command + completed, nil, true
}

func (rt *runtimeState) completeWorkspacePathFiltered(typed string, dirsOnly bool) (string, []string, bool) {
	searchToken := normalizeTypedPath(typed)
	dirPart, partial := splitTypedPath(searchToken)

	baseDir := "."
	if dirPart != "" {
		baseDir = dirPart
	}
	resolvedBase, err := rt.workspace.Resolve(baseDir)
	if err != nil {
		return typed, nil, false
	}
	entries, err := os.ReadDir(resolvedBase)
	if err != nil {
		return typed, nil, false
	}

	lowerPartial := strings.ToLower(partial)
	type candidate struct {
		display string
		dir     bool
	}
	var matches []candidate
	for _, entry := range entries {
		if dirsOnly && !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(strings.ToLower(name), lowerPartial) {
			continue
		}
		display := name
		if dirPart != "" {
			display = filepath.ToSlash(filepath.Join(dirPart, name))
		}
		if entry.IsDir() {
			display += "/"
		}
		matches = append(matches, candidate{display: display, dir: entry.IsDir()})
	}
	if len(matches) == 0 {
		return typed, nil, false
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].display < matches[j].display })
	if len(matches) == 1 {
		one := matches[0]
		if one.dir {
			return one.display, nil, true
		}
		return one.display + " ", nil, true
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m.display)
	}
	common := longestCommonPrefixInsensitive(names)
	if len(common) > len(searchToken) {
		return common, nil, true
	}
	return typed, names, true
}

func (rt *runtimeState) completeOpenPath(buffer string) (string, []string, bool) {
	trimmedLeft := strings.TrimLeft(buffer, " \t")
	if !strings.HasPrefix(strings.ToLower(trimmedLeft), "/open ") {
		return buffer, nil, false
	}

	leading := buffer[:len(buffer)-len(trimmedLeft)]
	pathPart := trimmedLeft[len("/open "):]
	completed, suggestions, ok := rt.completeWorkspacePathValue(pathPart, true)
	if !ok {
		return buffer, nil, true
	}
	if len(suggestions) > 0 {
		prefixed := make([]string, 0, len(suggestions))
		for _, suggestion := range suggestions {
			prefixed = append(prefixed, "/open "+suggestion)
		}
		return buffer, prefixed, true
	}
	return leading + "/open " + completed, nil, true
}

func (rt *runtimeState) completeMCPCommandTarget(buffer string) (string, []string, bool) {
	trimmedLeft := strings.TrimLeft(buffer, " \t")
	lower := strings.ToLower(trimmedLeft)
	command := ""
	kind := ""
	switch {
	case strings.HasPrefix(lower, "/resource "):
		command = "/resource "
		kind = "resource"
	case strings.HasPrefix(lower, "/prompt "):
		command = "/prompt "
		kind = "prompt"
	default:
		return buffer, nil, false
	}
	leading := buffer[:len(buffer)-len(trimmedLeft)]
	targetPart := trimmedLeft[len(command):]
	if strings.ContainsAny(targetPart, " \t") {
		return buffer, nil, false
	}
	var (
		completed   string
		suggestions []string
		ok          bool
	)
	switch kind {
	case "resource":
		completed, suggestions, ok = rt.completeMCPQualifiedResource("mcp:" + targetPart)
	case "prompt":
		completed, suggestions, ok = rt.completeMCPQualifiedPrompt("mcp:" + targetPart)
	}
	if !ok {
		return buffer, nil, true
	}
	if len(suggestions) > 0 {
		out := make([]string, 0, len(suggestions))
		for _, suggestion := range suggestions {
			out = append(out, command+strings.TrimPrefix(suggestion, "mcp:"))
		}
		return buffer, out, true
	}
	return leading + command + strings.TrimPrefix(completed, "mcp:"), nil, true
}

func (rt *runtimeState) completeMCPQualifiedTarget(token string) (string, []string, bool) {
	return rt.completeMCPQualifiedResource(token)
}

func (rt *runtimeState) completeMCPQualifiedResource(token string) (string, []string, bool) {
	if rt.mcp == nil {
		return token, nil, false
	}
	trimmed := strings.TrimSpace(token)
	if !strings.HasPrefix(strings.ToLower(trimmed), "mcp:") {
		return token, nil, false
	}
	rest := trimmed[len("mcp:"):]
	if rest == "" {
		var suggestions []string
		for _, status := range rt.mcpStatus() {
			suggestions = append(suggestions, "mcp:"+status.Name+":")
		}
		return token, suggestions, true
	}
	if !strings.Contains(rest, ":") {
		partial := strings.ToLower(rest)
		var matches []string
		for _, status := range rt.mcpStatus() {
			if strings.HasPrefix(strings.ToLower(status.Name), partial) {
				matches = append(matches, "mcp:"+status.Name+":")
			}
		}
		if len(matches) == 1 {
			return matches[0], nil, true
		}
		return token, matches, true
	}
	parts := strings.SplitN(rest, ":", 2)
	server := parts[0]
	partial := strings.ToLower(parts[1])
	var matches []string
	for _, item := range rt.mcpResources() {
		if !strings.EqualFold(item.Server, server) {
			continue
		}
		target := item.Resource.URI
		if target == "" {
			target = item.Resource.Name
		}
		if target == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(target), partial) || strings.HasPrefix(strings.ToLower(item.Resource.Name), partial) {
			matches = append(matches, "mcp:"+item.Server+":"+target)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil, true
	}
	return token, matches, true
}

func (rt *runtimeState) completeMCPQualifiedPrompt(token string) (string, []string, bool) {
	if rt.mcp == nil {
		return token, nil, false
	}
	trimmed := strings.TrimSpace(token)
	if !strings.HasPrefix(strings.ToLower(trimmed), "mcp:") {
		return token, nil, false
	}
	rest := trimmed[len("mcp:"):]
	if rest == "" {
		var suggestions []string
		for _, status := range rt.mcpStatus() {
			suggestions = append(suggestions, "mcp:"+status.Name+":")
		}
		return token, suggestions, true
	}
	if !strings.Contains(rest, ":") {
		partial := strings.ToLower(rest)
		var matches []string
		for _, status := range rt.mcpStatus() {
			if strings.HasPrefix(strings.ToLower(status.Name), partial) {
				matches = append(matches, "mcp:"+status.Name+":")
			}
		}
		if len(matches) == 1 {
			return matches[0], nil, true
		}
		return token, matches, true
	}
	parts := strings.SplitN(rest, ":", 2)
	server := parts[0]
	partial := strings.ToLower(parts[1])
	var matches []string
	for _, item := range rt.mcpPrompts() {
		if !strings.EqualFold(item.Server, server) {
			continue
		}
		if strings.HasPrefix(strings.ToLower(item.Prompt.Name), partial) {
			matches = append(matches, "mcp:"+item.Server+":"+item.Prompt.Name)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil, true
	}
	return token, matches, true
}

func (rt *runtimeState) completeWorkspacePathValue(typed string, preferFiles bool) (string, []string, bool) {
	searchToken := normalizeTypedPath(typed)
	dirPart, partial := splitTypedPath(searchToken)

	baseDir := "."
	if dirPart != "" {
		baseDir = dirPart
	}
	resolvedBase, err := rt.workspace.Resolve(baseDir)
	if err != nil {
		return typed, nil, false
	}
	entries, err := os.ReadDir(resolvedBase)
	if err != nil {
		return typed, nil, false
	}

	lowerPartial := strings.ToLower(partial)
	type candidate struct {
		display string
		dir     bool
	}
	var matches []candidate
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(strings.ToLower(name), lowerPartial) {
			continue
		}
		display := name
		if dirPart != "" {
			display = filepath.ToSlash(filepath.Join(dirPart, name))
		}
		if entry.IsDir() {
			display += "/"
		}
		matches = append(matches, candidate{display: display, dir: entry.IsDir()})
	}
	if len(matches) == 0 {
		return typed, nil, false
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].display < matches[j].display })
	if len(matches) == 1 {
		one := matches[0]
		if one.dir {
			return one.display, nil, true
		}
		if preferFiles {
			return one.display + " ", nil, true
		}
		return one.display, nil, true
	}

	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match.display)
	}
	common := longestCommonPrefixInsensitive(names)
	if len(common) > len(searchToken) {
		return common, nil, true
	}
	return typed, names, true
}

func lastMentionStart(buffer string) int {
	for i := len(buffer) - 1; i >= 0; i-- {
		if buffer[i] != '@' {
			continue
		}
		if i > 0 {
			prev := buffer[i-1]
			if prev != ' ' && prev != '\t' && prev != '\n' {
				continue
			}
		}
		if strings.ContainsAny(buffer[i+1:], " \t\n") {
			continue
		}
		return i
	}
	return -1
}

func splitTypedPath(path string) (dirPart string, partial string) {
	if path == "" {
		return "", ""
	}
	if strings.HasSuffix(path, "/") {
		return strings.TrimSuffix(path, "/"), ""
	}
	last := strings.LastIndex(path, "/")
	if last < 0 {
		return "", path
	}
	return path[:last], path[last+1:]
}

func normalizeTypedPath(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}

func longestCommonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for _, value := range values[1:] {
		for !strings.HasPrefix(value, prefix) && prefix != "" {
			prefix = prefix[:len(prefix)-1]
		}
		if prefix == "" {
			return ""
		}
	}
	return prefix
}

func longestCommonPrefixInsensitive(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := []rune(values[0])
	for _, value := range values[1:] {
		runes := []rune(value)
		limit := len(prefix)
		if len(runes) < limit {
			limit = len(runes)
		}
		idx := 0
		for idx < limit && strings.EqualFold(string(prefix[idx]), string(runes[idx])) {
			idx++
		}
		prefix = prefix[:idx]
		if len(prefix) == 0 {
			return ""
		}
	}
	return string(prefix)
}
