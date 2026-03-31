package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type Tool interface {
	Definition() ToolDefinition
	Execute(ctx context.Context, input any) (string, error)
}

type ToolRegistry struct {
	tools map[string]Tool
}

func NewToolRegistry(items ...Tool) *ToolRegistry {
	byName := make(map[string]Tool, len(items))
	for _, item := range items {
		byName[item.Definition().Name] = item
	}
	return &ToolRegistry{tools: byName}
}

func (r *ToolRegistry) Definitions() []ToolDefinition {
	out := make([]ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		out = append(out, tool.Definition())
	}
	return out
}

func (r *ToolRegistry) Execute(ctx context.Context, name string, args string) (string, error) {
	tool, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	payload := map[string]any{}
	if strings.TrimSpace(args) != "" {
		if err := json.Unmarshal([]byte(args), &payload); err != nil {
			return "", fmt.Errorf("tool %s received invalid JSON: %w", name, err)
		}
	}
	return tool.Execute(ctx, payload)
}

type Workspace struct {
	BaseRoot         string
	Root             string
	Shell            string
	Perms            *PermissionManager
	PrepareEdit      func(string) error
	CurrentSelection func() *ViewerSelection
	PreviewEdit      func(EditPreview) (bool, error)
	UpdatePlan       func([]PlanItem)
	GetPlan          func() []PlanItem
}

type EditPreview struct {
	Title   string
	Preview string
}

func (w Workspace) Resolve(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs = filepath.Clean(filepath.Join(w.Root, path))
	}
	baseRoot := w.BaseRoot
	if strings.TrimSpace(baseRoot) == "" {
		baseRoot = w.Root
	}
	rootAbs, err := filepath.Abs(baseRoot)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(abs)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return targetAbs, nil
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path is outside the workspace root: %s", path)
	}
	return targetAbs, nil
}

func (w Workspace) EnsureWrite(path string) error {
	if w.Perms == nil {
		return nil
	}
	ok, err := w.Perms.Allow(ActionWrite, path)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: user denied write approval for %s", ErrWriteDenied, path)
	}
	return nil
}

func (w Workspace) EnsureShell(command string) error {
	if w.Perms == nil {
		return nil
	}
	ok, err := w.Perms.Allow(ActionShell, command)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("shell permission denied")
	}
	return nil
}

func (w Workspace) ConfirmEdit(preview EditPreview) error {
	if w.PreviewEdit == nil {
		return nil
	}
	ok, err := w.PreviewEdit(preview)
	if err != nil {
		return err
	}
	if !ok {
		return ErrEditCanceled
	}
	return nil
}

func (w Workspace) BeforeEdit(reason string) error {
	if w.PrepareEdit == nil {
		return nil
	}
	return w.PrepareEdit(reason)
}

func (w Workspace) Selection() *ViewerSelection {
	if w.CurrentSelection == nil {
		return nil
	}
	return w.CurrentSelection()
}

func shellInvocation(shell, command string) (string, []string) {
	base := strings.ToLower(strings.TrimSpace(shell))
	switch {
	case strings.Contains(base, "powershell"):
		wrapped := "[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new(); $OutputEncoding=[System.Text.UTF8Encoding]::new(); " + command
		return shell, []string{"-NoProfile", "-Command", wrapped}
	case base == "cmd":
		return "cmd", []string{"/C", command}
	case base == "bash":
		return "bash", []string{"-lc", command}
	case base == "sh":
		return "sh", []string{"-lc", command}
	default:
		if runtime.GOOS == "windows" {
			return "powershell", []string{"-NoProfile", "-Command", command}
		}
		return "sh", []string{"-lc", command}
	}
}

func relOrAbs(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	if rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}

func ensureParentDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func isText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	limit := len(data)
	if limit > 4096 {
		limit = 4096
	}
	for _, b := range data[:limit] {
		if b == 0 {
			return false
		}
	}
	return true
}

type ListFilesTool struct{ ws Workspace }

func NewListFilesTool(ws Workspace) ListFilesTool { return ListFilesTool{ws: ws} }

func (t ListFilesTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "list_files",
		Description: "List files and directories in the workspace.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string"},
				"recursive":   map[string]any{"type": "boolean"},
				"max_entries": map[string]any{"type": "integer"},
			},
		},
	}
}

func (t ListFilesTool) Execute(ctx context.Context, input any) (string, error) {
	args := input.(map[string]any)
	root, err := t.ws.Resolve(stringValue(args, "path"))
	if err != nil {
		return "", err
	}
	recursive := boolValue(args, "recursive", false)
	maxEntries := intValue(args, "max_entries", 200)
	var lines []string
	if recursive {
		stop := fmt.Errorf("max entries reached")
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if path == root {
				return nil
			}
			rel := relOrAbs(t.ws.Root, path)
			if d.IsDir() {
				rel += "/"
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
			}
			lines = append(lines, rel)
			if len(lines) >= maxEntries {
				return stop
			}
			return nil
		})
		if err != nil && err.Error() != "max entries reached" {
			return "", err
		}
	} else {
		entries, err := os.ReadDir(root)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			rel := relOrAbs(t.ws.Root, filepath.Join(root, entry.Name()))
			if entry.IsDir() {
				rel += "/"
			}
			lines = append(lines, rel)
			if len(lines) >= maxEntries {
				break
			}
		}
	}
	if len(lines) == 0 {
		return "(no files found)", nil
	}
	return strings.Join(lines, "\n"), nil
}

type ReadFileTool struct{ ws Workspace }

func NewReadFileTool(ws Workspace) ReadFileTool { return ReadFileTool{ws: ws} }

func (t ReadFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "read_file",
		Description: "Read a file from the workspace. Supports line ranges.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string"},
				"start_line": map[string]any{"type": "integer"},
				"end_line":   map[string]any{"type": "integer"},
			},
			"required": []string{"path"},
		},
	}
}

func (t ReadFileTool) Execute(ctx context.Context, input any) (string, error) {
	args := input.(map[string]any)
	path, err := t.ws.Resolve(stringValue(args, "path"))
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !isText(data) {
		return "", fmt.Errorf("refusing to read binary file: %s", path)
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	lines := strings.Split(string(data), "\n")
	start := intValue(args, "start_line", 1)
	end := intValue(args, "end_line", len(lines))
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return "", fmt.Errorf("invalid line range")
	}
	var out []string
	for i := start - 1; i < end; i++ {
		out = append(out, fmt.Sprintf("%4d | %s", i+1, lines[i]))
	}
	return strings.Join(out, "\n"), nil
}

type GrepTool struct{ ws Workspace }

func NewGrepTool(ws Workspace) GrepTool { return GrepTool{ws: ws} }

func (t GrepTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "grep",
		Description: "Search text across files in the workspace using a regular expression.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":     map[string]any{"type": "string"},
				"path":        map[string]any{"type": "string"},
				"glob":        map[string]any{"type": "string"},
				"max_results": map[string]any{"type": "integer"},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t GrepTool) Execute(ctx context.Context, input any) (string, error) {
	args := input.(map[string]any)
	root, err := t.ws.Resolve(stringValue(args, "path"))
	if err != nil {
		return "", err
	}
	re, err := regexp.Compile(stringValue(args, "pattern"))
	if err != nil {
		return "", err
	}
	glob := stringValue(args, "glob")
	maxResults := intValue(args, "max_results", 100)
	var matches []string
	stop := fmt.Errorf("max results reached")
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if glob != "" {
			ok, err := filepath.Match(glob, filepath.Base(path))
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil || !isText(data) {
			return nil
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if re.MatchString(line) {
				matches = append(matches, fmt.Sprintf("%s:%d: %s", relOrAbs(t.ws.Root, path), lineNo, line))
				if len(matches) >= maxResults {
					return stop
				}
			}
		}
		return nil
	})
	if err != nil && err != stop {
		return "", err
	}
	if len(matches) == 0 {
		return "(no matches)", nil
	}
	return strings.Join(matches, "\n"), nil
}

type WriteFileTool struct{ ws Workspace }

func NewWriteFileTool(ws Workspace) WriteFileTool { return WriteFileTool{ws: ws} }

func (t WriteFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "write_file",
		Description: "Write or append to a text file in the workspace.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
				"append":  map[string]any{"type": "boolean"},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (t WriteFileTool) Execute(ctx context.Context, input any) (string, error) {
	args := input.(map[string]any)
	path, err := t.ws.Resolve(stringValue(args, "path"))
	if err != nil {
		return "", err
	}
	content := stringValue(args, "content")
	before := ""
	if existing, err := os.ReadFile(path); err == nil {
		before = string(existing)
	}
	if suspiciousRewritePayload(path, before, content) {
		return "", fmt.Errorf("%w: write_file content looks like a malformed serialized payload instead of real file contents; use apply_patch or provide the final file text", ErrInvalidEditPayload)
	}
	if err := ensureParentDir(path); err != nil {
		return "", err
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	reason := "write " + relOrAbs(t.ws.Root, path)
	after := content
	if boolValue(args, "append", false) {
		after = before + content
		if err := t.ws.ConfirmEdit(EditPreview{
			Title:   "Append to " + relOrAbs(t.ws.Root, path),
			Preview: buildSelectionAwareEditPreview(t.ws, relOrAbs(t.ws.Root, path), before, after),
		}); err != nil {
			return "", err
		}
		if err := t.ws.EnsureWrite(path); err != nil {
			return "", err
		}
		if err := t.ws.BeforeEdit(reason); err != nil {
			return "", err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return "", err
		}
		defer f.Close()
		if _, err := f.WriteString(content); err != nil {
			return "", err
		}
	} else {
		if err := t.ws.ConfirmEdit(EditPreview{
			Title:   "Write " + relOrAbs(t.ws.Root, path),
			Preview: buildSelectionAwareEditPreview(t.ws, relOrAbs(t.ws.Root, path), before, after),
		}); err != nil {
			return "", err
		}
		if err := t.ws.EnsureWrite(path); err != nil {
			return "", err
		}
		if err := t.ws.BeforeEdit(reason); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	return joinNonEmpty(
		fmt.Sprintf("wrote %d bytes to %s", len(content), relOrAbs(t.ws.Root, path)),
		buildEditPreview(relOrAbs(t.ws.Root, path), before, after),
	), nil
}

func suspiciousRewritePayload(path, before, after string) bool {
	if strings.TrimSpace(before) == "" || strings.TrimSpace(after) == "" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".java", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs", ".rs", ".json", ".yaml", ".yml", ".toml", ".md":
	default:
		return false
	}

	beforeLines := strings.Count(strings.ReplaceAll(before, "\r\n", "\n"), "\n") + 1
	afterNormalized := strings.ReplaceAll(after, "\r\n", "\n")
	afterLines := strings.Count(afterNormalized, "\n") + 1
	if beforeLines < 5 || afterLines != 1 {
		return false
	}

	suspiciousBits := []string{
		"{'",
		"[[",
		"'lines':",
		"'line':",
		"\\\\n':",
		"\\n':",
	}
	hits := 0
	for _, bit := range suspiciousBits {
		if strings.Contains(after, bit) {
			hits++
		}
	}
	return hits >= 2
}

func suspiciousReplacePayload(path, search, replace, before, after string) bool {
	if strings.TrimSpace(search) == "" || strings.TrimSpace(replace) == "" {
		return false
	}
	if !suspiciousRewritePayload(path, before, after) {
		return false
	}

	suspiciousBits := []string{
		"{'",
		"'trimmed':",
		"'lines':",
		"'line':",
		"\\n",
		"\\t",
	}
	hits := 0
	for _, bit := range suspiciousBits {
		if strings.Contains(replace, bit) {
			hits++
		}
	}
	return hits >= 2
}

type ReplaceInFileTool struct{ ws Workspace }

func NewReplaceInFileTool(ws Workspace) ReplaceInFileTool { return ReplaceInFileTool{ws: ws} }

func (t ReplaceInFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "replace_in_file",
		Description: "Replace exact text inside a file. Safer than rewriting the entire file.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"search":  map[string]any{"type": "string"},
				"replace": map[string]any{"type": "string"},
				"all":     map[string]any{"type": "boolean"},
			},
			"required": []string{"path", "search", "replace"},
		},
	}
}

func (t ReplaceInFileTool) Execute(ctx context.Context, input any) (string, error) {
	args := input.(map[string]any)
	path, err := t.ws.Resolve(stringValue(args, "path"))
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	search := stringValue(args, "search")
	replace := stringValue(args, "replace")
	content := string(data)
	count := strings.Count(content, search)
	if count == 0 {
		return "", fmt.Errorf("search text not found in %s", path)
	}
	all := boolValue(args, "all", false)
	if !all && count > 1 {
		return "", fmt.Errorf("search text appears %d times; set all=true or use a more specific match", count)
	}
	var updated string
	if all {
		updated = strings.ReplaceAll(content, search, replace)
	} else {
		updated = strings.Replace(content, search, replace, 1)
	}
	if suspiciousReplacePayload(path, search, replace, content, updated) {
		return "", fmt.Errorf("%w: replace_in_file replacement looks like a malformed serialized payload instead of real code; use apply_patch or provide the exact replacement text", ErrInvalidEditPayload)
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	if err := t.ws.ConfirmEdit(EditPreview{
		Title:   "Update " + relOrAbs(t.ws.Root, path),
		Preview: buildSelectionAwareEditPreview(t.ws, relOrAbs(t.ws.Root, path), content, updated),
	}); err != nil {
		return "", err
	}
	if err := t.ws.EnsureWrite(path); err != nil {
		return "", err
	}
	if err := t.ws.BeforeEdit("replace in " + relOrAbs(t.ws.Root, path)); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return "", err
	}
	return joinNonEmpty(
		fmt.Sprintf("updated %s (%d replacement(s))", relOrAbs(t.ws.Root, path), count),
		buildEditPreview(relOrAbs(t.ws.Root, path), content, updated),
	), nil
}

type RunShellTool struct{ ws Workspace }

func NewRunShellTool(ws Workspace) RunShellTool { return RunShellTool{ws: ws} }

func (t RunShellTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "run_shell",
		Description: "Run a shell command in the workspace.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":    map[string]any{"type": "string"},
				"timeout_ms": map[string]any{"type": "integer"},
			},
			"required": []string{"command"},
		},
	}
}

func (t RunShellTool) Execute(ctx context.Context, input any) (string, error) {
	args := input.(map[string]any)
	command := stringValue(args, "command")
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("command is required")
	}
	if err := t.ws.EnsureShell(command); err != nil {
		return "", err
	}
	timeout := time.Duration(intValue(args, "timeout_ms", 30000)) * time.Millisecond
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	name, shellArgs := shellInvocation(t.ws.Shell, command)
	cmd := exec.CommandContext(runCtx, name, shellArgs...)
	cmd.Dir = t.ws.Root
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if runCtx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("command timed out after %s", timeout)
	}
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("command failed [%s]: %w", summarizeShellCommand(command), err)
	}
	if text == "" {
		text = "(no output)"
	}
	return text, nil
}

func summarizeShellCommand(command string) string {
	command = strings.TrimSpace(command)
	if len(command) <= 120 {
		return command
	}
	return command[:120] + "..."
}

type GitStatusTool struct{ ws Workspace }

func NewGitStatusTool(ws Workspace) GitStatusTool { return GitStatusTool{ws: ws} }

func (t GitStatusTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "git_status",
		Description: "Show git status for the current workspace.",
		InputSchema: emptyObjectSchema(),
	}
}

func (t GitStatusTool) Execute(ctx context.Context, input any) (string, error) {
	_ = input
	cmd := exec.CommandContext(ctx, "git", "status", "--short", "--branch")
	cmd.Dir = t.ws.Root
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("git status failed: %w", err)
	}
	if text == "" {
		return "(clean working tree)", nil
	}
	return text, nil
}

type GitDiffTool struct{ ws Workspace }

func NewGitDiffTool(ws Workspace) GitDiffTool { return GitDiffTool{ws: ws} }

func (t GitDiffTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "git_diff",
		Description: "Show git diff for the workspace or a specific path.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string"},
				"staged": map[string]any{"type": "boolean"},
			},
		},
	}
}

func (t GitDiffTool) Execute(ctx context.Context, input any) (string, error) {
	args := input.(map[string]any)
	cmdArgs := []string{"diff"}
	if boolValue(args, "staged", false) {
		cmdArgs = append(cmdArgs, "--staged")
	}
	if pathArg := stringValue(args, "path"); pathArg != "" {
		path, err := t.ws.Resolve(pathArg)
		if err != nil {
			return "", err
		}
		cmdArgs = append(cmdArgs, "--", path)
	}
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Dir = t.ws.Root
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("git diff failed: %w", err)
	}
	if text == "" {
		return "(no diff)", nil
	}
	return text, nil
}

type UpdatePlanTool struct{ ws Workspace }

func NewUpdatePlanTool(ws Workspace) UpdatePlanTool { return UpdatePlanTool{ws: ws} }

func (t UpdatePlanTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "update_plan",
		Description: "Update the shared task plan shown to the user.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"step":   map[string]any{"type": "string"},
							"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}},
						},
						"required": []string{"step", "status"},
					},
				},
			},
			"required": []string{"items"},
		},
	}
}

func (t UpdatePlanTool) Execute(ctx context.Context, input any) (string, error) {
	_ = ctx
	if t.ws.UpdatePlan == nil {
		return "", fmt.Errorf("plan updates are not configured")
	}
	args := input.(map[string]any)
	rawItems, ok := args["items"].([]any)
	if !ok {
		return "", fmt.Errorf("items must be an array")
	}
	items := make([]PlanItem, 0, len(rawItems))
	for _, raw := range rawItems {
		obj, ok := raw.(map[string]any)
		if !ok {
			return "", fmt.Errorf("each plan item must be an object")
		}
		items = append(items, PlanItem{
			Step:   stringValue(obj, "step"),
			Status: stringValue(obj, "status"),
		})
	}
	t.ws.UpdatePlan(items)
	if len(items) == 0 {
		return "cleared plan", nil
	}
	var lines []string
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("[%s] %s", item.Status, item.Step))
	}
	return strings.Join(lines, "\n"), nil
}

func stringValue(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case string:
			return x
		}
	}
	return ""
}

func boolValue(m map[string]any, key string, def bool) bool {
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case bool:
			return x
		}
	}
	return def
}

func intValue(m map[string]any, key string, def int) int {
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case float64:
			return int(x)
		case int:
			return x
		}
	}
	return def
}
