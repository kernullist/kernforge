package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

type Tool interface {
	Definition() ToolDefinition
	Execute(ctx context.Context, input any) (string, error)
}

type ToolExecutionResult struct {
	DisplayText       string            `json:"display_text,omitempty"`
	ContentItems      []ToolContentItem `json:"content_items,omitempty"`
	ModelText         string            `json:"model_text,omitempty"`
	ModelContentItems []ToolContentItem `json:"model_content_items,omitempty"`
	Meta              map[string]any    `json:"meta,omitempty"`
}

type detailedTool interface {
	ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error)
}

func requireToolInputObject(input any, toolName string) (map[string]any, error) {
	args, ok := input.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s input must be an object", strings.TrimSpace(toolName))
	}
	return args, nil
}

type sharedToolHintsAware interface {
	setSharedToolHints(*ToolHints)
}

type sharedToolHintsCapacityAware interface {
	sharedToolHintsMaxReadSpans() int
}

type ToolRegistry struct {
	tools map[string]Tool
}

func NewToolRegistry(items ...Tool) *ToolRegistry {
	sharedHints := &ToolHints{maxReadSpans: sharedToolHintsLimit(items)}
	byName := make(map[string]Tool, len(items))
	for _, item := range items {
		if aware, ok := item.(sharedToolHintsAware); ok {
			aware.setSharedToolHints(sharedHints)
		}
		byName[item.Definition().Name] = item
	}
	return &ToolRegistry{tools: byName}
}

func sharedToolHintsLimit(items []Tool) int {
	for _, item := range items {
		aware, ok := item.(sharedToolHintsCapacityAware)
		if !ok {
			continue
		}
		if maxReadSpans := aware.sharedToolHintsMaxReadSpans(); maxReadSpans > 0 {
			return maxReadSpans
		}
	}
	return defaultReadHintSpans
}

func (r *ToolRegistry) Definitions() []ToolDefinition {
	out := make([]ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		out = append(out, tool.Definition())
	}
	return out
}

func (r *ToolRegistry) DefinitionsExcluding(disabled map[string]bool) []ToolDefinition {
	if len(disabled) == 0 {
		return r.Definitions()
	}
	out := make([]ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		def := tool.Definition()
		if disabled[strings.TrimSpace(def.Name)] {
			continue
		}
		out = append(out, def)
	}
	return out
}

func (r *ToolRegistry) Execute(ctx context.Context, name string, args string) (string, error) {
	result, err := r.ExecuteDetailed(ctx, name, args)
	if err != nil {
		return "", err
	}
	return result.DisplayText, nil
}

func (r *ToolRegistry) ExecuteDetailed(ctx context.Context, name string, args string) (ToolExecutionResult, error) {
	tool, ok := r.tools[name]
	if !ok {
		return ToolExecutionResult{}, fmt.Errorf("unknown tool: %s", name)
	}
	payload := map[string]any{}
	if strings.TrimSpace(args) != "" {
		if err := json.Unmarshal([]byte(args), &payload); err != nil {
			return ToolExecutionResult{}, fmt.Errorf("%w: tool %s received invalid JSON: %v", ErrInvalidToolArgumentsJSON, name, err)
		}
	}
	if detailed, ok := tool.(detailedTool); ok {
		result, err := detailed.ExecuteDetailed(ctx, payload)
		result.DisplayText = strings.TrimSpace(result.DisplayText)
		result.ModelText = strings.TrimSpace(result.ModelText)
		result.ContentItems = normalizeToolContentItems(result.ContentItems)
		result.ModelContentItems = normalizeToolContentItems(result.ModelContentItems)
		result.Meta = mergeToolMetaMaps(defaultToolExecutionMeta(name, payload), result.Meta)
		result.Meta["success"] = err == nil
		if err != nil {
			result.Meta["error"] = err.Error()
		}
		return result, err
	}
	out, err := tool.Execute(ctx, payload)
	meta := defaultToolExecutionMeta(name, payload)
	meta["success"] = err == nil
	if err != nil {
		meta["error"] = err.Error()
	}
	return ToolExecutionResult{
		DisplayText: strings.TrimSpace(out),
		Meta:        meta,
	}, err
}

func toolExecutionModelText(result ToolExecutionResult) string {
	if text := strings.TrimSpace(result.ModelText); text != "" {
		return text
	}
	return strings.TrimSpace(result.DisplayText)
}

func toolExecutionModelContentItems(result ToolExecutionResult) []ToolContentItem {
	if len(result.ModelContentItems) > 0 {
		return normalizeToolContentItems(result.ModelContentItems)
	}
	return normalizeToolContentItems(result.ContentItems)
}

func toolExecutionModelTextWithError(result ToolExecutionResult, err error) string {
	if err == nil {
		return toolExecutionModelText(result)
	}
	text := toolExecutionModelText(result)
	if text == "" {
		return err.Error()
	}
	return text + "\n\nERROR: " + err.Error()
}

func mergeToolMetaMaps(base map[string]any, extra map[string]any) map[string]any {
	merged := cloneMetaMap(base)
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

func defaultToolExecutionMeta(name string, payload map[string]any) map[string]any {
	meta := map[string]any{
		"tool_name": strings.TrimSpace(name),
		"effect":    inferToolExecutionEffect(name),
	}
	if path := strings.TrimSpace(stringValue(payload, "path")); path != "" {
		meta["path"] = path
	}
	if paths := normalizeTaskStateList(stringSliceValue(payload, "paths"), 16); len(paths) > 0 {
		meta["paths"] = paths
	}
	if command := strings.TrimSpace(stringValue(payload, "command")); command != "" {
		meta["command"] = command
	}
	if ownerNodeID := strings.TrimSpace(stringValue(payload, "owner_node_id")); ownerNodeID != "" {
		meta["owner_node_id"] = ownerNodeID
	}
	if pattern := strings.TrimSpace(stringValue(payload, "pattern")); pattern != "" {
		meta["pattern"] = pattern
	}
	if len(normalizeTaskStateList(stringSliceValue(payload, "commands"), 8)) > 0 {
		meta["commands"] = normalizeTaskStateList(stringSliceValue(payload, "commands"), 8)
	}
	if glob := strings.TrimSpace(stringValue(payload, "glob")); glob != "" {
		meta["glob"] = glob
	}
	return meta
}

func inferToolExecutionEffect(name string) string {
	switch strings.TrimSpace(name) {
	case "list_files", "read_file", "grep", "git_status", "git_diff":
		return "inspect"
	case "apply_edit_proposal", "write_file", "replace_in_file", "apply_patch":
		return "edit"
	case "update_plan":
		return "plan"
	case "run_shell", "check_shell_job", "check_shell_bundle", "run_shell_background", "run_shell_bundle_background", "cancel_shell_job", "cancel_shell_bundle":
		return "execute"
	case "git_add", "git_commit", "git_push", "git_create_pr":
		return "git_mutation"
	default:
		return "task"
	}
}

func textLineCount(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n"))
}

func countListedEntries(text string) int {
	normalized := strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n")
	if normalized == "" || normalized == "(no files found)" {
		return 0
	}
	count := 0
	for _, line := range strings.Split(normalized, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

type Workspace struct {
	BaseRoot              string
	Root                  string
	Shell                 string
	ShellTimeout          time.Duration
	ReadHintSpans         int
	ReadCacheEntries      int
	VerificationToolPaths map[string]string
	ToolHints             *ToolHints
	Perms                 *PermissionManager
	PrepareEdit           func(string) error
	PrepareEditAtRoot     func(string, string) error
	ReviewEdit            func(context.Context, EditPreview) error
	ReportProgress        func(string)
	CurrentSelection      func() *ViewerSelection
	PreviewEdit           func(EditPreview) (bool, error)
	ConfirmVerification   func(VerificationPlan) (bool, error)
	UpdatePlan            func([]PlanItem)
	GetPlan               func() []PlanItem
	RunHook               func(context.Context, HookEvent, HookPayload) (HookVerdict, error)
	BackgroundJobs        *BackgroundJobManager
	ResolveEditTarget     func(EditRoutingRequest) (EditRoutingResult, error)
	ResolveShellRoot      func(string) (ShellRoutingResult, error)
}

type EditPreview struct {
	Title     string
	Preview   string
	Paths     []string
	Operation string
	Proposals []EditProposal
}

type ToolHints struct {
	mu              sync.Mutex
	recentReadSpans []readSpanHint
	maxReadSpans    int
}

type readSpanHint struct {
	path            string
	startLine       int
	endLine         int
	modTimeUnixNano int64
	size            int64
}

func (w Workspace) Resolve(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	abs, err := w.resolveAgainstRoot(w.Root, path)
	if err != nil {
		return "", err
	}
	return w.ensureWithinBaseRoot(path, abs)
}

func (w Workspace) resolveEditFallback(req EditRoutingRequest) (EditRoutingResult, error) {
	req = req.normalized()
	path := strings.TrimSpace(req.Path)
	if path == "" {
		path = "."
	}
	if !req.lookupIntent() {
		abs, err := w.resolveAgainstRoot(w.Root, path)
		if err != nil {
			return EditRoutingResult{}, err
		}
		abs, err = w.ensureWithinBaseRoot(path, abs)
		if err != nil {
			return EditRoutingResult{}, err
		}
		if req.AllowBaseFallback && !w.pathLooksAbsoluteForLookup(path) && !sameFilePath(w.Root, w.BaseRoot) {
			if _, err := os.Stat(abs); err == nil {
				return EditRoutingResult{
					AbsolutePath: abs,
					DisplayRoot:  w.Root,
					OwnerNodeID:  strings.TrimSpace(req.OwnerNodeID),
				}, nil
			} else if err != nil && !os.IsNotExist(err) {
				return EditRoutingResult{}, err
			}
			fallback, err := w.resolveAgainstRoot(w.BaseRoot, path)
			if err != nil {
				return EditRoutingResult{}, err
			}
			fallback, err = ensurePathWithinRoot(path, w.BaseRoot, fallback)
			if err != nil {
				return EditRoutingResult{}, err
			}
			if _, err := os.Stat(fallback); err == nil {
				return EditRoutingResult{
					AbsolutePath: fallback,
					DisplayRoot:  w.BaseRoot,
					OwnerNodeID:  strings.TrimSpace(req.OwnerNodeID),
				}, nil
			} else if err != nil && !os.IsNotExist(err) {
				return EditRoutingResult{}, err
			}
		}
		return EditRoutingResult{
			AbsolutePath: abs,
			DisplayRoot:  w.Root,
			OwnerNodeID:  strings.TrimSpace(req.OwnerNodeID),
		}, nil
	}
	primary, err := w.Resolve(path)
	if err != nil {
		return EditRoutingResult{}, err
	}
	if w.pathLooksAbsoluteForLookup(path) || sameFilePath(w.Root, w.BaseRoot) {
		return EditRoutingResult{
			AbsolutePath: primary,
			DisplayRoot:  w.Root,
			OwnerNodeID:  strings.TrimSpace(req.OwnerNodeID),
		}, nil
	}
	if _, err := os.Stat(primary); err == nil {
		return EditRoutingResult{
			AbsolutePath: primary,
			DisplayRoot:  w.Root,
			OwnerNodeID:  strings.TrimSpace(req.OwnerNodeID),
		}, nil
	} else if !os.IsNotExist(err) {
		return EditRoutingResult{}, err
	}
	fallback, err := w.resolveAgainstRoot(w.BaseRoot, path)
	if err != nil {
		return EditRoutingResult{}, err
	}
	if _, err := os.Stat(fallback); err == nil {
		return EditRoutingResult{
			AbsolutePath: fallback,
			DisplayRoot:  w.BaseRoot,
			OwnerNodeID:  strings.TrimSpace(req.OwnerNodeID),
		}, nil
	} else if err != nil && !os.IsNotExist(err) {
		return EditRoutingResult{}, err
	}
	return EditRoutingResult{
		AbsolutePath: primary,
		DisplayRoot:  w.Root,
		OwnerNodeID:  strings.TrimSpace(req.OwnerNodeID),
	}, nil
}

func (w Workspace) ResolveEditPath(path string, ownerNodeID string, forLookup bool) (EditRoutingResult, error) {
	req := EditRoutingRequest{
		Path:        path,
		OwnerNodeID: ownerNodeID,
		ForLookup:   forLookup,
		Intent:      editRoutingIntentForLookup(forLookup),
	}
	if w.ResolveEditTarget != nil {
		return w.ResolveEditTarget(req)
	}
	return w.resolveEditFallback(req)
}

func (w Workspace) ResolveLookupPath(path string, ownerNodeID string) (EditRoutingResult, error) {
	return w.ResolveEditPathWithOptions(EditRoutingRequest{
		Path:        path,
		OwnerNodeID: ownerNodeID,
		ForLookup:   true,
		Intent:      editRoutingIntentLookup,
	})
}

func (w Workspace) ResolveEditPathWithOptions(req EditRoutingRequest) (EditRoutingResult, error) {
	req = req.normalized()
	if w.ResolveEditTarget != nil {
		return w.ResolveEditTarget(req)
	}
	return w.resolveEditFallback(req)
}

func (w Workspace) ResolveShellWorkingDir(ownerNodeID string) (ShellRoutingResult, error) {
	if w.ResolveShellRoot != nil {
		return w.ResolveShellRoot(ownerNodeID)
	}
	return ShellRoutingResult{
		Root:        firstNonBlankString(w.Root, w.BaseRoot),
		OwnerNodeID: strings.TrimSpace(ownerNodeID),
	}, nil
}

func (w Workspace) ResolveShellWorkDir(ownerNodeID string, workdir string) (ShellRoutingResult, string, error) {
	route, err := w.ResolveShellWorkingDir(ownerNodeID)
	if err != nil {
		return ShellRoutingResult{}, "", err
	}
	root := firstNonBlankString(route.Root, w.Root, w.BaseRoot)
	if strings.TrimSpace(workdir) == "" {
		return route, root, nil
	}
	resolved, err := w.resolveAgainstRoot(root, filepath.FromSlash(strings.TrimSpace(workdir)))
	if err != nil {
		return ShellRoutingResult{}, "", err
	}
	resolved, err = ensurePathWithinRoot(workdir, root, resolved)
	if err != nil {
		return ShellRoutingResult{}, "", err
	}
	return route, resolved, nil
}

func (w Workspace) ConfirmVerificationPlan(plan VerificationPlan) (bool, error) {
	if w.ConfirmVerification == nil {
		return true, nil
	}
	return w.ConfirmVerification(plan)
}

func (w Workspace) toolHints() *ToolHints {
	if w.ToolHints != nil {
		return w.ToolHints
	}
	return nil
}

func (w Workspace) defaultReadHintSpans() int {
	if w.ReadHintSpans > 0 {
		return w.ReadHintSpans
	}
	return defaultReadHintSpans
}

func (w Workspace) defaultReadCacheEntries() int {
	if w.ReadCacheEntries > 0 {
		return w.ReadCacheEntries
	}
	return defaultReadCacheEntries
}

func (h *ToolHints) rememberReadSpan(span readSpanHint) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.maxReadSpans <= 0 {
		h.maxReadSpans = defaultReadHintSpans
	}
	filtered := h.recentReadSpans[:0]
	for _, existing := range h.recentReadSpans {
		if existing.path == span.path && existing.startLine == span.startLine && existing.endLine == span.endLine &&
			existing.modTimeUnixNano == span.modTimeUnixNano && existing.size == span.size {
			continue
		}
		filtered = append(filtered, existing)
	}
	h.recentReadSpans = append(filtered, span)
	if len(h.recentReadSpans) > h.maxReadSpans {
		h.recentReadSpans = h.recentReadSpans[len(h.recentReadSpans)-h.maxReadSpans:]
	}
}

func (h *ToolHints) readCacheHint(path string, lineNo int, info fs.FileInfo) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	best := ""
	bestDistance := 0
	for i := len(h.recentReadSpans) - 1; i >= 0; i-- {
		span := h.recentReadSpans[i]
		if span.path != path {
			continue
		}
		if span.modTimeUnixNano != info.ModTime().UnixNano() || span.size != info.Size() {
			continue
		}
		if lineNo >= span.startLine && lineNo <= span.endLine {
			return "[cached-nearby:inside]"
		}
		distance := 0
		if lineNo < span.startLine {
			distance = span.startLine - lineNo
		} else {
			distance = lineNo - span.endLine
		}
		if distance > 12 {
			continue
		}
		if best == "" || distance < bestDistance {
			bestDistance = distance
			best = fmt.Sprintf("[cached-nearby:%d]", distance)
		}
	}
	return best
}

func (w Workspace) ResolveForLookup(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	primary, err := w.Resolve(path)
	if err != nil {
		return "", err
	}
	if w.pathLooksAbsoluteForLookup(path) || sameFilePath(w.Root, w.BaseRoot) {
		return primary, nil
	}
	if _, err := os.Stat(primary); err == nil {
		return primary, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	fallback, err := w.resolveAgainstRoot(w.BaseRoot, path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(fallback); err == nil {
		return fallback, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return primary, nil
}

func (w Workspace) resolveAgainstRoot(root, path string) (string, error) {
	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else if resolved, ok := w.resolveWindowsVolumeRootedPath(path); ok {
		abs = resolved
	} else {
		base := root
		if strings.TrimSpace(base) == "" {
			base = w.Root
		}
		abs = filepath.Clean(filepath.Join(base, path))
	}
	return abs, nil
}

func (w Workspace) pathLooksAbsoluteForLookup(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	_, ok := w.resolveWindowsVolumeRootedPath(path)
	return ok
}

func (w Workspace) resolveWindowsVolumeRootedPath(path string) (string, bool) {
	if runtime.GOOS != "windows" {
		return "", false
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" || filepath.IsAbs(trimmed) {
		return "", false
	}
	if (!strings.HasPrefix(trimmed, "/") && !strings.HasPrefix(trimmed, `\`)) ||
		strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, `\\`) {
		return "", false
	}
	volume := workspacePathVolume(w.Root)
	if volume == "" {
		volume = workspacePathVolume(w.BaseRoot)
	}
	if volume == "" {
		return "", false
	}
	relative := strings.TrimLeft(trimmed, `/\`)
	if relative == "" {
		return filepath.Clean(volume + string(filepath.Separator)), true
	}
	return filepath.Clean(volume + string(filepath.Separator) + filepath.FromSlash(relative)), true
}

func workspacePathVolume(path string) string {
	candidate := strings.TrimSpace(path)
	if candidate == "" {
		return ""
	}
	if volume := filepath.VolumeName(candidate); volume != "" {
		return volume
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return ""
	}
	return filepath.VolumeName(abs)
}

func (w Workspace) ensureWithinBaseRoot(originalPath, abs string) (string, error) {
	activeRoot := w.Root
	if strings.TrimSpace(activeRoot) == "" {
		activeRoot = w.BaseRoot
	}
	return ensurePathWithinRoot(originalPath, activeRoot, abs)
}

func sameFilePath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	left, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	right, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	leftInfo, leftStatErr := os.Stat(left)
	rightInfo, rightStatErr := os.Stat(right)
	if leftStatErr == nil && rightStatErr == nil {
		return os.SameFile(leftInfo, rightInfo)
	}
	return sameCleanPathForOS(left, right)
}

func sameCleanPathForOS(left string, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if workspacePathsAreCaseInsensitiveByDefault() {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func workspacePathsAreCaseInsensitiveByDefault() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

func (w Workspace) CheckEditBoundary(path string) error {
	if err := w.ensureProtectedEditPath(path); err != nil {
		return err
	}
	if err := w.ensureResolvedWritePathWithinRoot(path); err != nil {
		return err
	}
	return nil
}

func (w Workspace) EnsureWrite(path string) error {
	if err := w.CheckEditBoundary(path); err != nil {
		return err
	}
	if w.Perms == nil {
		return nil
	}
	ok, err := w.Perms.Allow(ActionWrite, path)
	if err != nil {
		return fmt.Errorf("%w: write approval unavailable for %s: %v", ErrWriteDenied, path, err)
	}
	if !ok {
		return fmt.Errorf("%w: user denied write approval for %s", ErrWriteDenied, path)
	}
	return nil
}

func (w Workspace) ensureResolvedWritePathWithinRoot(path string) error {
	roots := normalizeTaskStateList([]string{w.Root, w.BaseRoot}, 2)
	if len(roots) == 0 {
		return nil
	}
	var rootAbsList []string
	for _, root := range roots {
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		if resolvedRoot, err := filepath.EvalSymlinks(rootAbs); err == nil {
			rootAbs = resolvedRoot
		}
		rootAbsList = append(rootAbsList, rootAbs)
	}
	targetAbs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	checkPath := targetAbs
	for {
		resolved, err := filepath.EvalSymlinks(checkPath)
		if err == nil {
			resolvedAbs, absErr := filepath.Abs(resolved)
			if absErr != nil {
				return absErr
			}
			if !pathWithinAnyRoot(rootAbsList, resolvedAbs) {
				return fmt.Errorf("%w: refusing to write through a path that resolves outside the active workspace root: %s -> %s", ErrEditTargetMismatch, targetAbs, resolvedAbs)
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(checkPath)
		if parent == checkPath || strings.TrimSpace(parent) == "" {
			return nil
		}
		checkPath = parent
	}
}

func pathWithinAnyRoot(roots []string, path string) bool {
	for _, root := range roots {
		if pathWithinRoot(root, path) {
			return true
		}
	}
	return false
}

func pathWithinRoot(root string, path string) bool {
	rootClean := filepath.Clean(root)
	pathClean := filepath.Clean(path)
	rel, err := filepath.Rel(rootClean, pathClean)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}

func (w Workspace) EnsureGit(detail string) error {
	if w.Perms == nil {
		return nil
	}
	ok, err := w.Perms.Allow(ActionGit, detail)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("git permission denied")
	}
	return nil
}

func (w Workspace) ensureProtectedEditPath(path string) error {
	targetAbs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(w.Root)
	if err != nil {
		return err
	}
	targetScope, targetProtected := protectedWorktreeScope(targetAbs)
	if !targetProtected {
		return nil
	}
	rootScope, rootProtected := protectedWorktreeScope(rootAbs)
	if rootProtected && sameCleanPathForOS(rootScope, targetScope) {
		return nil
	}
	return fmt.Errorf("%w: refusing to edit nested worktree-managed path outside the active workspace root: %s", ErrEditTargetMismatch, targetAbs)
}

func protectedWorktreeScope(path string) (string, bool) {
	cleaned := filepath.Clean(path)
	volume := filepath.VolumeName(cleaned)
	trimmed := strings.TrimPrefix(cleaned, volume)
	trimmed = strings.TrimLeft(trimmed, string(filepath.Separator))
	parts := strings.Split(trimmed, string(filepath.Separator))
	for i := 0; i+1 < len(parts); i++ {
		first := strings.ToLower(strings.TrimSpace(parts[i]))
		second := strings.ToLower(strings.TrimSpace(parts[i+1]))
		if (first == ".claude" || first == ".git") && second == "worktrees" {
			scopeEnd := i + 2
			if i+2 < len(parts) {
				scopeEnd = i + 3
			}
			scopeParts := parts[:scopeEnd]
			prefix := filepath.Join(scopeParts...)
			if volume != "" {
				prefix = volume + string(filepath.Separator) + prefix
			} else {
				prefix = string(filepath.Separator) + prefix
			}
			return filepath.Clean(prefix), true
		}
	}
	return "", false
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

func (w Workspace) defaultShellTimeout() time.Duration {
	if w.ShellTimeout > 0 {
		return w.ShellTimeout
	}
	return time.Duration(currentDefaultShellTimeoutSecs) * time.Second
}

func (w Workspace) ConfirmEdit(preview EditPreview) error {
	if w.PreviewEdit == nil {
		return nil
	}
	ok, err := w.PreviewEdit(preview)
	if err != nil {
		if errors.Is(err, ErrPromptCanceled) || errors.Is(err, io.EOF) {
			return ErrEditCanceled
		}
		return err
	}
	if !ok {
		return ErrEditCanceled
	}
	return nil
}

func (w Workspace) ReviewProposedEdit(ctx context.Context, preview EditPreview) error {
	if w.ReviewEdit == nil {
		return nil
	}
	return w.ReviewEdit(ctx, preview)
}

func (w Workspace) BeforeEdit(reason string) error {
	if w.PrepareEdit == nil {
		return nil
	}
	return w.PrepareEdit(reason)
}

func (w Workspace) BeforeEditForRoot(reason string, root string) error {
	if w.PrepareEditAtRoot != nil {
		return w.PrepareEditAtRoot(reason, root)
	}
	return w.BeforeEdit(reason)
}

func (w Workspace) Progress(message string) {
	if w.ReportProgress == nil {
		return
	}
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return
	}
	w.ReportProgress(trimmed)
}

func (w Workspace) Selection() *ViewerSelection {
	if w.CurrentSelection == nil {
		return nil
	}
	return w.CurrentSelection()
}

func (w Workspace) Hook(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
	if w.RunHook == nil {
		return HookVerdict{Allow: true}, nil
	}
	return w.RunHook(ctx, event, payload)
}

func firstLine(text string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if trimmed == "" {
		return ""
	}
	if idx := strings.IndexByte(trimmed, '\n'); idx >= 0 {
		return strings.TrimSpace(trimmed[:idx])
	}
	return trimmed
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
				"path":          map[string]any{"type": "string"},
				"recursive":     map[string]any{"type": "boolean"},
				"max_entries":   map[string]any{"type": "integer"},
				"owner_node_id": map[string]any{"type": "string"},
			},
		},
	}
}

func (t ListFilesTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t ListFilesTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	ownerNodeID := stringValue(args, "owner_node_id")
	route, err := t.ws.ResolveLookupPath(stringValue(args, "path"), ownerNodeID)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	root := route.AbsolutePath
	displayRoot := t.ws.Root
	if strings.TrimSpace(ownerNodeID) != "" {
		displayRoot = firstNonBlankString(route.DisplayRoot, route.WorktreeRoot, t.ws.Root)
	}
	recursive := boolValue(args, "recursive", false)
	maxEntries := intValue(args, "max_entries", 200)
	if info, err := os.Stat(root); err != nil {
		return ToolExecutionResult{}, err
	} else if !info.IsDir() {
		displayPath := relOrAbs(displayRoot, root)
		return ToolExecutionResult{
			DisplayText: displayPath,
			Meta: map[string]any{
				"path":        displayPath,
				"path_type":   "file",
				"recursive":   recursive,
				"max_entries": maxEntries,
				"entry_count": 1,
				"truncated":   false,
			},
		}, nil
	}
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
			rel := relOrAbs(displayRoot, path)
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
			return ToolExecutionResult{}, err
		}
	} else {
		entries, err := os.ReadDir(root)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		for _, entry := range entries {
			rel := relOrAbs(displayRoot, filepath.Join(root, entry.Name()))
			if entry.IsDir() {
				rel += "/"
			}
			lines = append(lines, rel)
			if len(lines) >= maxEntries {
				break
			}
		}
	}
	text := "(no files found)"
	if len(lines) == 0 {
		return ToolExecutionResult{
			DisplayText: text,
			Meta: map[string]any{
				"path":        relOrAbs(displayRoot, root),
				"recursive":   recursive,
				"max_entries": maxEntries,
				"entry_count": 0,
			},
		}, nil
	}
	text = strings.Join(lines, "\n")
	return ToolExecutionResult{
		DisplayText: text,
		Meta: map[string]any{
			"path":        relOrAbs(displayRoot, root),
			"recursive":   recursive,
			"max_entries": maxEntries,
			"entry_count": len(lines),
			"truncated":   len(lines) >= maxEntries,
		},
	}, nil
}

type readFileCacheEntry struct {
	path            string
	startLine       int
	endLine         int
	modTimeUnixNano int64
	size            int64
	renderedLines   []string
	output          string
}

type ReadFileTool struct {
	ws        Workspace
	mu        sync.Mutex
	cache     map[string]readFileCacheEntry
	cacheKeys []string
	maxCache  int
}

func NewReadFileTool(ws Workspace) *ReadFileTool {
	if ws.ToolHints == nil {
		ws.ToolHints = &ToolHints{maxReadSpans: ws.defaultReadHintSpans()}
	}
	return &ReadFileTool{
		ws:       ws,
		cache:    make(map[string]readFileCacheEntry),
		maxCache: ws.defaultReadCacheEntries(),
	}
}

func (t *ReadFileTool) sharedToolHintsMaxReadSpans() int {
	return t.ws.defaultReadHintSpans()
}

func (t *ReadFileTool) setSharedToolHints(hints *ToolHints) {
	if hints == nil {
		return
	}
	t.ws.ToolHints = hints
}

func (t *ReadFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "read_file",
		Description: "Read a file from the workspace. Supports line ranges.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":          map[string]any{"type": "string"},
				"start_line":    map[string]any{"type": "integer"},
				"end_line":      map[string]any{"type": "integer"},
				"owner_node_id": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t *ReadFileTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	ownerNodeID := stringValue(args, "owner_node_id")
	route, err := t.ws.ResolveLookupPath(stringValue(args, "path"), ownerNodeID)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	path := route.AbsolutePath
	displayRoot := t.ws.Root
	if strings.TrimSpace(ownerNodeID) != "" {
		displayRoot = firstNonBlankString(route.DisplayRoot, route.WorktreeRoot, t.ws.Root)
	}
	startArg := intValue(args, "start_line", 1)
	endArg := intValue(args, "end_line", 0)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			display, meta := buildMissingReadFileResult(displayRoot, path, startArg, endArg)
			return ToolExecutionResult{
				DisplayText: display,
				Meta:        meta,
			}, fmt.Errorf("read_file target does not exist: %s", relOrAbs(displayRoot, path))
		}
		return ToolExecutionResult{}, err
	}
	cacheKey := readFileCacheKey(path, startArg, endArg)
	if cached, ok := t.lookupCachedRead(cacheKey, info); ok {
		normalizedStart, normalizedEnd := normalizeRenderedRangeBounds(cached, startArg, endArg)
		return ToolExecutionResult{
			DisplayText: "NOTE: returning cached content for an unchanged read_file range.\n" + cached,
			Meta:        buildReadFileMeta(displayRoot, path, startArg, endArg, normalizedStart, normalizedEnd, cached, "exact"),
		}, nil
	}
	if covered, ok := t.lookupCoveredCachedRead(path, startArg, endArg, info); ok {
		normalizedStart, normalizedEnd := normalizeRenderedRangeBounds(covered, startArg, endArg)
		return ToolExecutionResult{
			DisplayText: "NOTE: returning content from a cached overlapping read_file range.\n" + covered,
			Meta:        buildReadFileMeta(displayRoot, path, startArg, endArg, normalizedStart, normalizedEnd, covered, "covered"),
		}, nil
	}
	start := startArg
	end := endArg
	if start < 1 {
		start = 1
	}
	if overlap, ok := t.lookupPartialOverlap(path, start, end, info); ok {
		renderedLines, normalizedEnd, readErr := readRenderedRangeWithCachedOverlap(ctx, path, start, end, overlap)
		if readErr != nil {
			return ToolExecutionResult{}, readErr
		}
		if start > normalizedEnd {
			return ToolExecutionResult{}, fmt.Errorf("invalid line range")
		}
		result := strings.Join(renderedLines, "\n")
		t.storeCachedRead(cacheKey, path, start, normalizedEnd, info, renderedLines, result)
		t.recordReadSpanHint(path, start, normalizedEnd, info)
		return ToolExecutionResult{
			DisplayText: "NOTE: returning content assembled from a cached partial overlap plus newly read lines.\n" + result,
			Meta:        buildReadFileMeta(displayRoot, path, startArg, endArg, start, normalizedEnd, result, "partial_overlap"),
		}, nil
	}
	renderedLines, normalizedEnd, err := readRenderedFileRange(ctx, path, start, end)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	if start > normalizedEnd {
		return ToolExecutionResult{}, fmt.Errorf("invalid line range")
	}
	result := strings.Join(renderedLines, "\n")
	t.storeCachedRead(cacheKey, path, start, normalizedEnd, info, renderedLines, result)
	t.recordReadSpanHint(path, start, normalizedEnd, info)
	return ToolExecutionResult{
		DisplayText: result,
		Meta:        buildReadFileMeta(displayRoot, path, startArg, endArg, start, normalizedEnd, result, "fresh"),
	}, nil
}

func buildReadFileMeta(root string, path string, requestedStart int, requestedEnd int, actualStart int, actualEnd int, output string, cacheMode string) map[string]any {
	lineCount := 0
	if actualEnd >= actualStart && actualStart > 0 {
		lineCount = actualEnd - actualStart + 1
	}
	resolvedPath := relOrAbs(root, path)
	return map[string]any{
		"path":           resolvedPath,
		"requested_path": resolvedPath,
		"start_line":     requestedStart,
		"end_line":       requestedEnd,
		"actual_start":   actualStart,
		"actual_end":     actualEnd,
		"line_count":     lineCount,
		"cache_mode":     cacheMode,
		"output_lines":   textLineCount(output),
	}
}

func buildMissingReadFileResult(root string, path string, requestedStart int, requestedEnd int) (string, map[string]any) {
	resolvedPath := relOrAbs(root, path)
	parent := filepath.Dir(path)
	parentPath := relOrAbs(root, parent)
	lines := []string{
		"read_file target does not exist: " + resolvedPath,
		"Parent directory: " + parentPath,
	}
	parentExists := false
	parentEntryCount := 0

	entries, err := os.ReadDir(parent)
	switch {
	case err == nil:
		parentExists = true
		parentEntryCount = len(entries)
		if len(entries) == 0 {
			lines = append(lines,
				"Parent directory exists but is empty.",
				"For document or report authoring tasks, treat this as document not created yet. Use list_files on the parent directory before retrying read_file, or create/update the file with edit tools.",
			)
		} else {
			lines = append(lines, "Known entries in parent:")
			for _, entry := range entries[:minInt(len(entries), 12)] {
				item := relOrAbs(root, filepath.Join(parent, entry.Name()))
				if entry.IsDir() {
					item += "/"
				}
				lines = append(lines, "- "+item)
			}
			if len(entries) > 12 {
				lines = append(lines, fmt.Sprintf("- ... (%d more)", len(entries)-12))
			}
			lines = append(lines, "If this path is a generated document, confirm the actual filename with list_files before retrying read_file.")
		}
	case os.IsNotExist(err):
		lines = append(lines,
			"Parent directory does not exist.",
			"Use list_files on the nearest existing ancestor before retrying read_file. For document or report authoring tasks, treat this as document not created yet.",
		)
	default:
		lines = append(lines, "Could not inspect parent directory: "+strings.TrimSpace(err.Error()))
	}

	meta := map[string]any{
		"path":               resolvedPath,
		"requested_path":     resolvedPath,
		"start_line":         requestedStart,
		"end_line":           requestedEnd,
		"cache_mode":         "missing",
		"error_kind":         "not_found",
		"parent_path":        parentPath,
		"parent_exists":      parentExists,
		"parent_entry_count": parentEntryCount,
	}
	return strings.Join(lines, "\n"), meta
}

func normalizeRenderedRangeBounds(output string, requestedStart int, requestedEnd int) (int, int) {
	normalized := strings.ReplaceAll(strings.TrimSpace(output), "\r\n", "\n")
	if normalized == "" {
		return requestedStart, requestedEnd
	}
	lines := strings.Split(normalized, "\n")
	firstLine := strings.TrimSpace(lines[0])
	lastLine := strings.TrimSpace(lines[len(lines)-1])
	parseLineNo := func(text string) int {
		if strings.TrimSpace(text) == "" {
			return 0
		}
		prefix := text
		if divider := strings.Index(prefix, "|"); divider >= 0 {
			prefix = prefix[:divider]
		}
		prefix = strings.TrimSpace(prefix)
		value, _ := strconv.Atoi(prefix)
		return value
	}
	start := parseLineNo(firstLine)
	end := parseLineNo(lastLine)
	if start == 0 {
		start = requestedStart
	}
	if end == 0 {
		end = requestedEnd
	}
	return start, end
}

func readFileCacheKey(path string, start, end int) string {
	return fmt.Sprintf("%s:%d:%d", normalizeReadFileCachePath(path), start, end)
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func (t *ReadFileTool) lookupCachedRead(cacheKey string, info fs.FileInfo) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	entry, ok := t.cache[cacheKey]
	if !ok {
		return "", false
	}
	if entry.size != info.Size() {
		return "", false
	}
	if entry.modTimeUnixNano != info.ModTime().UnixNano() {
		return "", false
	}
	return entry.output, true
}

func (t *ReadFileTool) lookupCoveredCachedRead(path string, start, end int, info fs.FileInfo) (string, bool) {
	if end <= 0 {
		return "", false
	}

	normalizedPath := normalizeReadFileCachePath(path)

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, cacheKey := range t.cacheKeys {
		entry, ok := t.cache[cacheKey]
		if !ok {
			continue
		}
		if entry.path != normalizedPath {
			continue
		}
		if entry.size != info.Size() {
			continue
		}
		if entry.modTimeUnixNano != info.ModTime().UnixNano() {
			continue
		}
		if start < entry.startLine || end > entry.endLine {
			continue
		}
		offsetStart := start - entry.startLine
		offsetEnd := end - entry.startLine + 1
		if offsetStart < 0 || offsetEnd > len(entry.renderedLines) || offsetStart >= offsetEnd {
			continue
		}
		return strings.Join(entry.renderedLines[offsetStart:offsetEnd], "\n"), true
	}

	return "", false
}

func (t *ReadFileTool) lookupPartialOverlap(path string, start, end int, info fs.FileInfo) (readFileCacheEntry, bool) {
	if end <= 0 {
		return readFileCacheEntry{}, false
	}

	normalizedPath := normalizeReadFileCachePath(path)

	t.mu.Lock()
	defer t.mu.Unlock()

	best := readFileCacheEntry{}
	bestOverlap := 0
	for i := len(t.cacheKeys) - 1; i >= 0; i-- {
		cacheKey := t.cacheKeys[i]
		entry, ok := t.cache[cacheKey]
		if !ok {
			continue
		}
		if entry.path != normalizedPath {
			continue
		}
		if entry.size != info.Size() {
			continue
		}
		if entry.modTimeUnixNano != info.ModTime().UnixNano() {
			continue
		}
		overlapStart := readFileMaxInt(start, entry.startLine)
		overlapEnd := readFileMinInt(end, entry.endLine)
		if overlapStart > overlapEnd {
			continue
		}
		overlapLen := overlapEnd - overlapStart + 1
		requestLen := end - start + 1
		if overlapLen <= 0 || overlapLen >= requestLen {
			continue
		}
		if overlapLen > bestOverlap {
			best = cloneReadFileCacheEntry(entry)
			bestOverlap = overlapLen
		}
	}

	if bestOverlap == 0 {
		return readFileCacheEntry{}, false
	}
	return best, true
}

func (t *ReadFileTool) storeCachedRead(cacheKey, path string, start, end int, info fs.FileInfo, renderedLines []string, output string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cache == nil {
		t.cache = make(map[string]readFileCacheEntry)
	}
	if existing, ok := t.cache[cacheKey]; ok {
		existing.path = normalizeReadFileCachePath(path)
		existing.startLine = start
		existing.endLine = end
		existing.modTimeUnixNano = info.ModTime().UnixNano()
		existing.size = info.Size()
		existing.renderedLines = append([]string(nil), renderedLines...)
		existing.output = output
		t.cache[cacheKey] = existing
		return
	}

	t.cache[cacheKey] = readFileCacheEntry{
		path:            normalizeReadFileCachePath(path),
		startLine:       start,
		endLine:         end,
		modTimeUnixNano: info.ModTime().UnixNano(),
		size:            info.Size(),
		renderedLines:   append([]string(nil), renderedLines...),
		output:          output,
	}
	t.cacheKeys = append(t.cacheKeys, cacheKey)
	if t.maxCache <= 0 {
		t.maxCache = t.ws.defaultReadCacheEntries()
	}
	if len(t.cacheKeys) > t.maxCache {
		evictKey := t.cacheKeys[0]
		t.cacheKeys = t.cacheKeys[1:]
		delete(t.cache, evictKey)
	}
}

func normalizeReadFileCachePath(path string) string {
	return strings.ToLower(filepath.Clean(path))
}

func (t *ReadFileTool) recordReadSpanHint(path string, start, end int, info fs.FileInfo) {
	hints := t.ws.toolHints()
	if hints == nil {
		return
	}
	hints.rememberReadSpan(readSpanHint{
		path:            normalizeReadFileCachePath(path),
		startLine:       start,
		endLine:         end,
		modTimeUnixNano: info.ModTime().UnixNano(),
		size:            info.Size(),
	})
}

func cloneReadFileCacheEntry(entry readFileCacheEntry) readFileCacheEntry {
	cloned := entry
	cloned.renderedLines = append([]string(nil), entry.renderedLines...)
	return cloned
}

func readRenderedFileRange(ctx context.Context, path string, start, end int) ([]string, int, error) {
	if start < 1 {
		start = 1
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	if err := rejectBinaryFile(file); err != nil {
		return nil, 0, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return nil, 0, err
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	renderedLines := make([]string, 0)
	lineNumber := 0
	lastLine := 0
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		default:
		}

		lineNumber++
		lastLine = lineNumber
		if lineNumber < start {
			continue
		}
		if end > 0 && lineNumber > end {
			break
		}
		renderedLines = append(renderedLines, fmt.Sprintf("%4d | %s", lineNumber, scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	if end == 0 || end > lastLine {
		end = lastLine
	}
	return renderedLines, end, nil
}

func readRenderedRangeWithCachedOverlap(ctx context.Context, path string, start, end int, overlap readFileCacheEntry) ([]string, int, error) {
	headLines := make([]string, 0)
	tailLines := make([]string, 0)
	normalizedEnd := end
	var err error

	if start < overlap.startLine {
		headLines, _, err = readRenderedFileRange(ctx, path, start, overlap.startLine-1)
		if err != nil {
			return nil, 0, err
		}
	}

	overlapStart := readFileMaxInt(start, overlap.startLine)
	overlapEnd := readFileMinInt(end, overlap.endLine)
	offsetStart := overlapStart - overlap.startLine
	offsetEnd := overlapEnd - overlap.startLine + 1
	middleLines := append([]string(nil), overlap.renderedLines[offsetStart:offsetEnd]...)

	if end > overlap.endLine {
		tailLines, normalizedEnd, err = readRenderedFileRange(ctx, path, overlap.endLine+1, end)
		if err != nil {
			return nil, 0, err
		}
	}

	combined := make([]string, 0, len(headLines)+len(middleLines)+len(tailLines))
	combined = append(combined, headLines...)
	combined = append(combined, middleLines...)
	combined = append(combined, tailLines...)
	if normalizedEnd == 0 {
		normalizedEnd = overlapEnd
	}
	return combined, normalizedEnd, nil
}

func rejectBinaryFile(file *os.File) error {
	preview := make([]byte, 8192)
	n, err := file.Read(preview)
	if err != nil && err != io.EOF {
		return err
	}
	if !isText(preview[:n]) {
		return fmt.Errorf("refusing to read binary file: %s", file.Name())
	}
	return nil
}

func readFileMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func readFileMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type GrepTool struct{ ws Workspace }

func NewGrepTool(ws Workspace) *GrepTool { return &GrepTool{ws: ws} }

func (t *GrepTool) sharedToolHintsMaxReadSpans() int {
	return t.ws.defaultReadHintSpans()
}

func (t *GrepTool) setSharedToolHints(hints *ToolHints) {
	if hints == nil {
		return
	}
	t.ws.ToolHints = hints
}

func (t GrepTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "grep",
		Description: "Search text across files in the workspace using a regular expression.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":       map[string]any{"type": "string"},
				"path":          map[string]any{"type": "string"},
				"glob":          map[string]any{"type": "string"},
				"max_results":   map[string]any{"type": "integer"},
				"owner_node_id": map[string]any{"type": "string"},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t GrepTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t GrepTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	ownerNodeID := stringValue(args, "owner_node_id")
	route, err := t.ws.ResolveLookupPath(stringValue(args, "path"), ownerNodeID)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	root := route.AbsolutePath
	displayRoot := t.ws.Root
	if strings.TrimSpace(ownerNodeID) != "" {
		displayRoot = firstNonBlankString(route.DisplayRoot, route.WorktreeRoot, t.ws.Root)
	}
	re, err := regexp.Compile(stringValue(args, "pattern"))
	if err != nil {
		return ToolExecutionResult{}, err
	}
	glob := stringValue(args, "glob")
	maxResults := intValue(args, "max_results", 100)
	var matches []string
	matchedFiles := map[string]struct{}{}
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
		fileInfo, statErr := os.Stat(path)
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if re.MatchString(line) {
				matchPrefix := fmt.Sprintf("%s:%d: %s", relOrAbs(displayRoot, path), lineNo, line)
				if statErr == nil {
					if hint := t.grepReadCacheHint(path, lineNo, fileInfo); hint != "" {
						matchPrefix += " " + hint
					}
				}
				matches = append(matches, matchPrefix)
				matchedFiles[relOrAbs(displayRoot, path)] = struct{}{}
				if len(matches) >= maxResults {
					return stop
				}
			}
		}
		return nil
	})
	if err != nil && err != stop {
		return ToolExecutionResult{}, err
	}
	if len(matches) == 0 {
		return ToolExecutionResult{
			DisplayText: "(no matches)",
			Meta: map[string]any{
				"path":          relOrAbs(displayRoot, root),
				"pattern":       re.String(),
				"glob":          glob,
				"match_count":   0,
				"file_count":    0,
				"max_results":   maxResults,
				"truncated":     false,
				"matched_paths": []string{},
			},
		}, nil
	}
	paths := make([]string, 0, len(matchedFiles))
	for path := range matchedFiles {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return ToolExecutionResult{
		DisplayText: strings.Join(matches, "\n"),
		Meta: map[string]any{
			"path":          relOrAbs(displayRoot, root),
			"pattern":       re.String(),
			"glob":          glob,
			"match_count":   len(matches),
			"file_count":    len(paths),
			"max_results":   maxResults,
			"truncated":     len(matches) >= maxResults,
			"matched_paths": paths,
		},
	}, nil
}

func (t GrepTool) grepReadCacheHint(path string, lineNo int, info fs.FileInfo) string {
	hints := t.ws.toolHints()
	if hints == nil {
		return ""
	}
	return hints.readCacheHint(normalizeReadFileCachePath(path), lineNo, info)
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
				"path":          map[string]any{"type": "string"},
				"content":       map[string]any{"type": "string"},
				"append":        map[string]any{"type": "boolean"},
				"owner_node_id": map[string]any{"type": "string"},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (t WriteFileTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	route, err := t.ws.ResolveEditPathWithOptions(EditRoutingRequest{
		Path:              stringValue(args, "path"),
		OwnerNodeID:       stringValue(args, "owner_node_id"),
		ForLookup:         false,
		AllowBaseFallback: true,
	})
	if err != nil {
		return "", err
	}
	path := route.AbsolutePath
	displayPath := route.DisplayPath()
	editRoot := firstNonBlankString(route.WorktreeRoot, route.DisplayRoot, t.ws.Root)
	content := stringValue(args, "content")
	before := ""
	if err := t.ws.CheckEditBoundary(path); err != nil {
		return "", err
	}
	if existing, err := os.ReadFile(path); err == nil {
		before = string(existing)
	}
	if suspiciousRewritePayload(path, before, content) {
		return "", fmt.Errorf("%w: write_file content looks like a malformed serialized payload instead of real file contents; use apply_patch or provide the final file text", ErrInvalidEditPayload)
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	reason := "write " + displayPath
	after := content
	if boolValue(args, "append", false) {
		after = before + content
		if _, err := t.ws.Hook(ctx, HookPreEdit, HookPayload{
			"path":          displayPath,
			"absolute_path": path,
			"operation":     "write_file",
			"reason":        reason,
			"file_tags":     hookFileTags(path),
			"owner_node_id": route.OwnerNodeID,
			"worktree_root": route.WorktreeRoot,
			"specialist":    route.Specialist,
		}); err != nil {
			return "", err
		}
		preview := EditPreview{
			Title:     "Append to " + displayPath,
			Preview:   buildSelectionAwareEditPreview(t.ws, displayPath, before, after),
			Paths:     []string{displayPath},
			Operation: "write_file",
		}
		if err := t.ws.ReviewProposedEdit(ctx, preview); err != nil {
			return "", err
		}
		if err := t.ws.ConfirmEdit(preview); err != nil {
			return "", err
		}
		if err := t.ws.EnsureWrite(path); err != nil {
			return "", err
		}
		if err := t.ws.BeforeEditForRoot(reason, editRoot); err != nil {
			return "", err
		}
		if err := ensureParentDir(path); err != nil {
			return "", err
		}
		t.ws.Progress("Writing " + displayPath + "...")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return "", err
		}
		defer f.Close()
		if _, err := f.WriteString(content); err != nil {
			return "", err
		}
		t.ws.Progress("Saved " + displayPath + ".")
	} else {
		if _, err := t.ws.Hook(ctx, HookPreEdit, HookPayload{
			"path":          displayPath,
			"absolute_path": path,
			"operation":     "write_file",
			"reason":        reason,
			"file_tags":     hookFileTags(path),
			"owner_node_id": route.OwnerNodeID,
			"worktree_root": route.WorktreeRoot,
			"specialist":    route.Specialist,
		}); err != nil {
			return "", err
		}
		preview := EditPreview{
			Title:     "Write " + displayPath,
			Preview:   buildSelectionAwareEditPreview(t.ws, displayPath, before, after),
			Paths:     []string{displayPath},
			Operation: "write_file",
		}
		if err := t.ws.ReviewProposedEdit(ctx, preview); err != nil {
			return "", err
		}
		if err := t.ws.ConfirmEdit(preview); err != nil {
			return "", err
		}
		if err := t.ws.EnsureWrite(path); err != nil {
			return "", err
		}
		if err := t.ws.BeforeEditForRoot(reason, editRoot); err != nil {
			return "", err
		}
		if err := ensureParentDir(path); err != nil {
			return "", err
		}
		t.ws.Progress("Writing " + displayPath + "...")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", err
		}
		t.ws.Progress("Saved " + displayPath + ".")
	}
	t.ws.Progress("Running post-edit hooks for " + displayPath + "...")
	if _, err := t.ws.Hook(ctx, HookPostEdit, HookPayload{
		"path":          displayPath,
		"absolute_path": path,
		"operation":     "write_file",
		"reason":        reason,
		"file_tags":     hookFileTags(path),
		"owner_node_id": route.OwnerNodeID,
		"worktree_root": route.WorktreeRoot,
		"specialist":    route.Specialist,
	}); err != nil {
		return "", err
	}
	t.ws.Progress("Post-edit hooks finished for " + displayPath + ".")
	return joinNonEmpty(
		fmt.Sprintf("wrote %d bytes to %s", len(content), displayPath),
		buildEditPreview(displayPath, before, after),
	), nil
}

func (t WriteFileTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	text, err := t.Execute(ctx, input)
	path := strings.TrimSpace(stringValue(args, "path"))
	meta := map[string]any{
		"path":                  path,
		"changed_paths":         normalizeTaskStateList([]string{path}, 8),
		"changed_count":         1,
		"append":                boolValue(args, "append", false),
		"owner_node_id":         strings.TrimSpace(stringValue(args, "owner_node_id")),
		"bytes_written":         len(stringValue(args, "content")),
		"changed_workspace":     err == nil,
		"requires_verification": err == nil,
		"effect":                "edit",
	}
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
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
		Description: "Replace an exact text match in a file. Use this only for very small single-location substitutions when you have just read the same file path and confirmed the exact search text.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":          map[string]any{"type": "string"},
				"search":        map[string]any{"type": "string"},
				"replace":       map[string]any{"type": "string"},
				"all":           map[string]any{"type": "boolean"},
				"owner_node_id": map[string]any{"type": "string"},
			},
			"required": []string{"path", "search", "replace"},
		},
	}
}

func (t ReplaceInFileTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	route, err := t.ws.ResolveEditPathWithOptions(EditRoutingRequest{
		Path:              stringValue(args, "path"),
		OwnerNodeID:       stringValue(args, "owner_node_id"),
		ForLookup:         false,
		AllowBaseFallback: true,
	})
	if err != nil {
		return "", err
	}
	path := route.AbsolutePath
	displayPath := route.DisplayPath()
	editRoot := firstNonBlankString(route.WorktreeRoot, route.DisplayRoot, t.ws.Root)
	if err := t.ws.CheckEditBoundary(path); err != nil {
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
		return "", fmt.Errorf("%w: search text not found in %s", ErrEditTargetMismatch, path)
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
	if _, err := t.ws.Hook(ctx, HookPreEdit, HookPayload{
		"path":          displayPath,
		"absolute_path": path,
		"operation":     "replace_in_file",
		"reason":        "replace in " + displayPath,
		"file_tags":     hookFileTags(path),
		"owner_node_id": route.OwnerNodeID,
		"worktree_root": route.WorktreeRoot,
		"specialist":    route.Specialist,
	}); err != nil {
		return "", err
	}
	preview := EditPreview{
		Title:     "Update " + displayPath,
		Preview:   buildSelectionAwareEditPreview(t.ws, displayPath, content, updated),
		Paths:     []string{displayPath},
		Operation: "replace_in_file",
	}
	if err := t.ws.ReviewProposedEdit(ctx, preview); err != nil {
		return "", err
	}
	if err := t.ws.ConfirmEdit(preview); err != nil {
		return "", err
	}
	if err := t.ws.EnsureWrite(path); err != nil {
		return "", err
	}
	if err := t.ws.BeforeEditForRoot("replace in "+displayPath, editRoot); err != nil {
		return "", err
	}
	t.ws.Progress("Writing " + displayPath + "...")
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return "", err
	}
	t.ws.Progress("Saved " + displayPath + ".")
	t.ws.Progress("Running post-edit hooks for " + displayPath + "...")
	if _, err := t.ws.Hook(ctx, HookPostEdit, HookPayload{
		"path":          displayPath,
		"absolute_path": path,
		"operation":     "replace_in_file",
		"reason":        "replace in " + displayPath,
		"file_tags":     hookFileTags(path),
		"owner_node_id": route.OwnerNodeID,
		"worktree_root": route.WorktreeRoot,
		"specialist":    route.Specialist,
	}); err != nil {
		return "", err
	}
	t.ws.Progress("Post-edit hooks finished for " + displayPath + ".")
	return joinNonEmpty(
		fmt.Sprintf("updated %s (%d replacement(s))", displayPath, count),
		buildEditPreview(displayPath, content, updated),
	), nil
}

func (t ReplaceInFileTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	text, err := t.Execute(ctx, input)
	path := strings.TrimSpace(stringValue(args, "path"))
	all := boolValue(args, "all", false)
	replacements := 0
	if err == nil {
		if all {
			if parsed, parseErr := parseReplacementCountFromOutput(text); parseErr == nil {
				replacements = parsed
			}
		} else {
			replacements = 1
		}
	}
	meta := map[string]any{
		"path":                  path,
		"changed_paths":         normalizeTaskStateList([]string{path}, 8),
		"changed_count":         1,
		"all":                   all,
		"owner_node_id":         strings.TrimSpace(stringValue(args, "owner_node_id")),
		"applied_replacements":  replacements,
		"changed_workspace":     err == nil,
		"requires_verification": err == nil,
		"effect":                "edit",
	}
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

func parseReplacementCountFromOutput(text string) (int, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		start := strings.Index(line, "(")
		end := strings.Index(line, " replacement")
		if start < 0 || end <= start {
			continue
		}
		return strconv.Atoi(strings.TrimSpace(line[start+1 : end]))
	}
	return 0, fmt.Errorf("replacement count not found")
}

type RunShellTool struct{ ws Workspace }

func NewRunShellTool(ws Workspace) RunShellTool { return RunShellTool{ws: ws} }

type shellMutationClass string

const (
	shellMutationReadOnly              shellMutationClass = "read_only"
	shellMutationCacheOnly             shellMutationClass = "cache_only"
	shellMutationExternalInstall       shellMutationClass = "external_install"
	shellMutationGitMutation           shellMutationClass = "git_mutation"
	shellMutationVerificationArtifacts shellMutationClass = "verification_artifacts"
	shellMutationWorkspaceWrite        shellMutationClass = "workspace_write"
)

const (
	shellOutputTailLimit      = 64 * 1024
	shellOutputHeartbeatEvery = 15 * time.Second
	shellOutputProgressEvery  = 2 * time.Second
)

var shellFileWriteRedirectionTargetPattern = regexp.MustCompile(`(?i)(^|[\s;(|&])(?:\*|\d+)?>>?\s*([^\s;|&)]+)`)
var shellGitMutationPattern = regexp.MustCompile(`(?i)(^|[;&|()])\s*git\s+(add|am|apply|branch|checkout|cherry-pick|clean|clone|commit|config|init|merge|mv|pull|push|rebase|reset|restore|revert|rm|stash|switch|tag)\b`)
var shellManualWorkspaceWriteCommandPattern = regexp.MustCompile(`(?i)(^|[;|&(){}])\s*(?:set-content|add-content|clear-content|out-file|tee-object|new-item|remove-item|rename-item|move-item|copy-item|set-acl|export-csv|export-clixml|start-transcript|stop-transcript|mkdir|md|del|erase|copy|move|ren|rename|rm|mv|cp|touch)\b`)
var shellNestedManualWorkspaceWriteCommandPattern = regexp.MustCompile(`(?i)(^|[\s;|&(){}"'` + "`" + `])(?:set-content|add-content|clear-content|out-file|tee-object|new-item|remove-item|rename-item|move-item|copy-item|set-acl|export-csv|export-clixml|start-transcript|stop-transcript|mkdir|md|del|erase|copy|move|ren|rename|rm|mv|cp|touch)\b`)

type shellCommandAssessment struct {
	Class  shellMutationClass
	Reason string
}

type shellOutputEvent struct {
	data []byte
}

type shellOutputCollector struct {
	ws             Workspace
	commandSummary string
	startedAt      time.Time
	tailLimit      int

	mu             sync.Mutex
	tail           []byte
	totalBytes     int
	lastOutputLine string
	lastProgressAt time.Time
	lineBuffer     []byte
}

func (t RunShellTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "run_shell",
		Description: "Run a shell command in the workspace.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":       map[string]any{"type": "string"},
				"workdir":       map[string]any{"type": "string"},
				"timeout_ms":    map[string]any{"type": "integer"},
				"owner_node_id": map[string]any{"type": "string"},
			},
			"required": []string{"command"},
		},
	}
}

func (t RunShellTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	command := stringValue(args, "command")
	ownerNodeID := strings.TrimSpace(stringValue(args, "owner_node_id"))
	workdir := strings.TrimSpace(stringValue(args, "workdir"))
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("command is required")
	}
	if guidance := runShellCompatibilityGuidance(t.ws.Shell, command); guidance != "" {
		return guidance, fmt.Errorf("shell command is incompatible with the active shell")
	}
	assessment := assessShellCommandMutation(command)
	if assessment.Class == shellMutationReadOnly || assessment.Class == shellMutationCacheOnly {
		if guidance := runShellDedicatedToolGuidance(command); guidance != "" {
			return guidance, fmt.Errorf("run_shell command should use a dedicated workspace tool")
		}
	}
	shellRoute, workDir, err := t.ws.ResolveShellWorkDir(ownerNodeID, workdir)
	if err != nil {
		return "", err
	}
	effectiveOwnerNodeID := firstNonBlankString(shellRoute.OwnerNodeID, ownerNodeID)
	if assessment.Class == shellMutationWorkspaceWrite {
		if reason := shellCommandManualWorkspaceWriteReason(command); reason != "" {
			return "", fmt.Errorf("run_shell cannot perform manual workspace file writes; use apply_patch or apply_edit_proposal so edits stay reviewable (%s)", reason)
		}
		return "", fmt.Errorf("run_shell cannot modify workspace files because shell writes bypass the diff preview and review gate; use apply_patch or apply_edit_proposal instead (%s)", assessment.Reason)
	}
	if assessment.Class == shellMutationVerificationArtifacts {
		t.ws.Progress("run_shell recognized a verification/build command that may write workspace build artifacts. Source edits are still blocked.")
		ok, confirmErr := t.ws.ConfirmVerificationPlan(VerificationPlan{
			Mode:         VerificationAdaptive,
			ChangedPaths: collectVerificationChangedPaths(workDir, nil),
			Steps: []VerificationStep{{
				Label:   "shell verification",
				Command: command,
				Status:  VerificationPending,
			}},
		})
		if confirmErr != nil {
			return "", confirmErr
		}
		if !ok {
			return skippedVerificationCommandText(), nil
		}
	}
	var workspaceBeforeShell map[string]workspaceFileSignature
	if assessment.Class == shellMutationVerificationArtifacts {
		snapshot, snapshotErr := snapshotWorkspaceFiles(workDir)
		if snapshotErr != nil {
			return "", snapshotErr
		}
		if externalLinks := workspaceSnapshotExternalSymlinkPaths(snapshot); len(externalLinks) > 0 {
			return "", fmt.Errorf("run_shell verification command is blocked because the workspace contains symlinks that resolve outside the active root: %s", strings.Join(externalLinks, ", "))
		}
		workspaceBeforeShell = snapshot
	}
	if _, err := t.ws.Hook(ctx, HookPreToolUse, HookPayload{
		"tool_name":     "run_shell",
		"tool_kind":     "shell",
		"command":       command,
		"risk_tags":     hookCommandRiskTags(command),
		"file_tags":     []string{},
		"owner_node_id": effectiveOwnerNodeID,
		"work_dir":      workDir,
	}); err != nil {
		return "", err
	}
	if err := t.ws.EnsureShell(command); err != nil {
		return "", err
	}
	timeout := t.ws.defaultShellTimeout()
	if timeoutMs := intValue(args, "timeout_ms", 0); timeoutMs > 0 {
		timeout = time.Duration(timeoutMs) * time.Millisecond
	}
	text, err := t.runShellCommand(ctx, workDir, command, timeout)
	if err != nil {
		if workspaceErr := detectUnexpectedShellWorkspaceChanges(workDir, workspaceBeforeShell); workspaceErr != nil {
			err = fmt.Errorf("%w; %v", err, workspaceErr)
		}
		text = appendRunShellGuidance(text, runShellFailureGuidance(t.ws.Shell, command, text, err))
		_, _ = t.ws.Hook(ctx, HookPostToolUse, HookPayload{
			"tool_name":     "run_shell",
			"tool_kind":     "shell",
			"command":       command,
			"risk_tags":     hookCommandRiskTags(command),
			"output":        text,
			"error":         err.Error(),
			"owner_node_id": effectiveOwnerNodeID,
			"work_dir":      workDir,
		})
		return text, err
	}
	if text == "" {
		text = "(no output)"
	}
	if workspaceErr := detectUnexpectedShellWorkspaceChanges(workDir, workspaceBeforeShell); workspaceErr != nil {
		_, _ = t.ws.Hook(ctx, HookPostToolUse, HookPayload{
			"tool_name":     "run_shell",
			"tool_kind":     "shell",
			"command":       command,
			"risk_tags":     hookCommandRiskTags(command),
			"output":        text,
			"error":         workspaceErr.Error(),
			"owner_node_id": effectiveOwnerNodeID,
			"work_dir":      workDir,
		})
		return text, workspaceErr
	}
	if _, err := t.ws.Hook(ctx, HookPostToolUse, HookPayload{
		"tool_name":     "run_shell",
		"tool_kind":     "shell",
		"command":       command,
		"risk_tags":     hookCommandRiskTags(command),
		"output":        text,
		"owner_node_id": effectiveOwnerNodeID,
		"work_dir":      workDir,
	}); err != nil {
		return "", err
	}
	return text, nil
}

func skippedVerificationCommandText() string {
	return "verification command skipped because the user declined to run it. Do not retry this verification command or poll a background job for it unless the user explicitly approves verification; disclose that verification was not run. Do not relabel resolved code-review findings as remaining bugs only because verification is missing."
}

func detectUnexpectedShellWorkspaceChanges(workDir string, before map[string]workspaceFileSignature) error {
	if len(before) == 0 || strings.TrimSpace(workDir) == "" {
		return nil
	}
	current, err := snapshotWorkspaceFiles(workDir)
	if err != nil {
		return err
	}
	changed := changedWorkspaceSignaturePaths(before, current)
	if len(changed) == 0 {
		return nil
	}
	unexpected := verificationWorkspaceSourceOrConfigChanges(changed)
	if len(unexpected) == 0 {
		return nil
	}
	return fmt.Errorf("run_shell verification command modified workspace source/config files outside the edit review gate: %s", strings.Join(unexpected, ", "))
}

func (t RunShellTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	command := stringValue(args, "command")
	ownerNodeID := strings.TrimSpace(stringValue(args, "owner_node_id"))
	workdir := strings.TrimSpace(stringValue(args, "workdir"))
	_, workDir, _ := t.ws.ResolveShellWorkDir(ownerNodeID, workdir)
	assessment := assessShellCommandMutation(command)
	text, err := t.Execute(ctx, input)
	verificationLike := assessment.Class == shellMutationVerificationArtifacts || runShellOutputLooksLikeVerification(text) || runShellOutputLooksLikeSkippedVerification(text)
	meta := map[string]any{
		"command":           command,
		"mutation_class":    string(assessment.Class),
		"verification_like": verificationLike,
		"owner_node_id":     ownerNodeID,
		"work_dir":          workDir,
		"changed_workspace": false,
		"effect":            "execute",
	}
	if verificationLike {
		status := VerificationPassed
		commandStatus := "completed"
		if runShellOutputLooksLikeSkippedVerification(text) {
			status = VerificationSkipped
			commandStatus = "declined"
		} else if err != nil {
			status = VerificationFailed
			commandStatus = "failed"
		}
		meta["verification_status"] = string(status)
		meta["verification_evidence"] = status == VerificationPassed
		meta["verification_approved"] = status != VerificationSkipped
		meta["command_execution_status"] = commandStatus
		if status == VerificationSkipped {
			meta["verification_declined"] = true
		}
	}
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

func runShellDedicatedToolGuidance(command string) string {
	if runShellLooksLikeFileReadInspection(command) {
		return "Use the read_file tool instead of run_shell for source file inspection. This avoids shell approval prompts and keeps file evidence structured for review."
	}
	args := shellLikeFields(command)
	if len(args) < 2 {
		return ""
	}
	program := strings.ToLower(strings.TrimSuffix(filepath.Base(args[0]), ".exe"))
	if program != "git" {
		return ""
	}
	switch strings.ToLower(args[1]) {
	case "status":
		return "Use the git_status tool instead of run_shell for git status inspection. This avoids interactive shell approval and keeps the review/repair loop deterministic."
	case "diff":
		return "Use the git_diff tool instead of run_shell for git diff inspection. Pass a path to git_diff when you need a focused diff."
	default:
	}
	return ""
}

func shellLikeFields(command string) []string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return nil
	}
	for i, field := range fields {
		fields[i] = strings.Trim(field, "\"'")
	}
	return fields
}

func runShellLooksLikeFileReadInspection(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "get-content") {
		return true
	}
	fields := shellLikeFields(command)
	if len(fields) == 0 {
		return false
	}
	first := strings.ToLower(strings.TrimSuffix(filepath.Base(fields[0]), ".exe"))
	switch first {
	case "cat", "type", "gc":
		return len(fields) >= 2
	default:
		return false
	}
}

func (t RunShellTool) runShellCommand(ctx context.Context, workDir string, command string, timeout time.Duration) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	name, shellArgs := shellInvocation(t.ws.Shell, command)
	cmd := exec.CommandContext(runCtx, name, shellArgs...)
	cmd.Dir = workDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	collector := newShellOutputCollector(t.ws, command)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan struct{})
	defer close(done)
	go t.emitShellHeartbeats(done, collector)

	streamErrs := make(chan error, 2)
	events := make(chan shellOutputEvent, 32)
	var streamWG sync.WaitGroup
	streamWG.Add(2)
	go func() {
		defer streamWG.Done()
		if readErr := streamShellOutput(runCtx, stdout, events); readErr != nil {
			streamErrs <- readErr
		}
	}()
	go func() {
		defer streamWG.Done()
		if readErr := streamShellOutput(runCtx, stderr, events); readErr != nil {
			streamErrs <- readErr
		}
	}()
	waitErrs := make(chan error, 1)
	go func() {
		waitErrs <- cmd.Wait()
	}()
	go func() {
		streamWG.Wait()
		close(events)
		close(streamErrs)
	}()
	var waitErr error
	waitDone := false
	eventsClosed := false
	for !waitDone || !eventsClosed {
		select {
		case event, ok := <-events:
			if !ok {
				eventsClosed = true
				continue
			}
			collector.AppendBytes(event.data)
		case err := <-waitErrs:
			waitErr = err
			waitDone = true
			if !eventsClosed {
				_ = drainShellOutputEvents(collector, events, 200*time.Millisecond)
				eventsClosed = true
			}
		case <-runCtx.Done():
			if cmd.Process != nil {
				_ = terminateBackgroundProcess(cmd.Process.Pid)
			}
			_ = drainShellOutputEvents(collector, events, 500*time.Millisecond)
			eventsClosed = true
			select {
			case err := <-waitErrs:
				waitErr = err
				waitDone = true
			case <-time.After(1500 * time.Millisecond):
				return collector.Text(), runCtx.Err()
			}
		}
	}
	err = waitErr
	err = mergeShellStreamErrors(err, streamErrs, 100*time.Millisecond)
	text := collector.Text()
	if runCtx.Err() == context.Canceled {
		if text == "" {
			text = "command canceled"
		}
		return text, runCtx.Err()
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("command timed out after %s", timeout)
	}
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("command failed [%s]: %w", summarizeShellCommand(command), err)
	}
	return text, nil
}

func mergeShellStreamErrors(err error, streamErrs <-chan error, limit time.Duration) error {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	for {
		select {
		case readErr, ok := <-streamErrs:
			if !ok {
				return err
			}
			if err == nil {
				err = readErr
			}
		case <-timer.C:
			return err
		}
	}
}

func drainShellOutputEvents(collector *shellOutputCollector, events <-chan shellOutputEvent, limit time.Duration) bool {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return true
			}
			collector.AppendBytes(event.data)
		case <-timer.C:
			return false
		}
	}
}

func summarizeShellCommand(command string) string {
	command = strings.TrimSpace(command)
	if len(command) <= 120 {
		return command
	}
	return command[:120] + "..."
}

func shellCommandLikelyMutatesWorkspace(command string) bool {
	return assessShellCommandMutation(command).Class == shellMutationWorkspaceWrite
}

func newShellOutputCollector(ws Workspace, command string) *shellOutputCollector {
	return &shellOutputCollector{
		ws:             ws,
		commandSummary: summarizeShellCommand(command),
		startedAt:      time.Now(),
		tailLimit:      shellOutputTailLimit,
	}
}

func (t RunShellTool) emitShellHeartbeats(done <-chan struct{}, collector *shellOutputCollector) {
	ticker := time.NewTicker(shellOutputHeartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if heartbeat := collector.Heartbeat(); heartbeat != "" {
				t.ws.Progress(heartbeat)
			}
		}
	}
}

func streamShellOutput(ctx context.Context, reader io.Reader, events chan<- shellOutputEvent) error {
	buffer := make([]byte, 4096)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			chunk := append([]byte(nil), buffer[:n]...)
			select {
			case events <- shellOutputEvent{data: chunk}:
			case <-ctx.Done():
				return nil
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
}

func (c *shellOutputCollector) AppendBytes(chunk []byte) {
	trimmedLine, sawDelimiter := c.consumeProgressChunk(chunk)
	now := time.Now()

	c.mu.Lock()
	c.totalBytes += len(chunk)
	c.tail = appendShellOutputTail(c.tail, chunk, c.tailLimit)
	emitProgress := false
	if trimmedLine != "" {
		c.lastOutputLine = trimmedLine
		if sawDelimiter || now.Sub(c.lastProgressAt) >= shellOutputProgressEvery {
			c.lastProgressAt = now
			emitProgress = true
		}
	}
	c.mu.Unlock()

	if emitProgress {
		c.ws.Progress("run_shell output: " + trimmedLine)
	}
}

func (c *shellOutputCollector) Text() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	text := strings.TrimSpace(normalizeShellOutputForDisplay(c.tail))
	if text == "" {
		return ""
	}
	if c.totalBytes <= len(c.tail) {
		return text
	}
	return fmt.Sprintf("[run_shell output truncated to last %s of %s]\n%s", formatShellByteCount(len(c.tail)), formatShellByteCount(c.totalBytes), text)
}

func (c *shellOutputCollector) Heartbeat() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	elapsed := time.Since(c.startedAt).Round(time.Second)
	if elapsed <= 0 {
		elapsed = time.Second
	}
	message := fmt.Sprintf("run_shell still running after %s: %s", elapsed, c.commandSummary)
	current := c.lastOutputLine
	if current == "" && len(c.lineBuffer) > 0 {
		current = summarizeShellProgressLine(string(c.lineBuffer))
	}
	if current != "" {
		message += " | last output: " + current
	}
	if c.totalBytes > 0 {
		message += " | buffered " + formatShellByteCount(c.totalBytes)
	}
	return message
}

func appendShellOutputTail(current []byte, chunk []byte, limit int) []byte {
	if limit <= 0 {
		limit = shellOutputTailLimit
	}
	if len(chunk) >= limit {
		return append([]byte(nil), chunk[len(chunk)-limit:]...)
	}
	if len(current)+len(chunk) <= limit {
		return append(current, chunk...)
	}
	trim := len(current) + len(chunk) - limit
	if trim > len(current) {
		trim = len(current)
	}
	next := append([]byte(nil), current[trim:]...)
	return append(next, chunk...)
}

func normalizeShellOutputForDisplay(raw []byte) string {
	text := decodePossiblyUTF16(raw)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

func decodePossiblyUTF16(raw []byte) string {
	if len(raw) >= 2 {
		if raw[0] == 0xFF && raw[1] == 0xFE {
			return string(utf16.Decode(bytesToUint16s(raw[2:], true)))
		}
		if raw[0] == 0xFE && raw[1] == 0xFF {
			return string(utf16.Decode(bytesToUint16s(raw[2:], false)))
		}
	}
	zeroBytes := 0
	sample := raw
	if len(sample) > 128 {
		sample = sample[:128]
	}
	for _, b := range sample {
		if b == 0 {
			zeroBytes++
		}
	}
	if len(sample) > 0 && zeroBytes >= len(sample)/4 {
		return string(utf16.Decode(bytesToUint16s(raw, true)))
	}
	return string(raw)
}

func bytesToUint16s(raw []byte, littleEndian bool) []uint16 {
	if len(raw)%2 == 1 {
		raw = raw[:len(raw)-1]
	}
	words := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		if littleEndian {
			words = append(words, uint16(raw[i])|uint16(raw[i+1])<<8)
		} else {
			words = append(words, uint16(raw[i])<<8|uint16(raw[i+1]))
		}
	}
	return words
}

func summarizeShellProgressLine(chunk string) string {
	lines := strings.Split(strings.ReplaceAll(chunk, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if len(line) > 160 {
			return line[:160] + "..."
		}
		return line
	}
	return ""
}

func (c *shellOutputCollector) consumeProgressChunk(chunk []byte) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	lastLine := ""
	sawDelimiter := false
	for _, b := range chunk {
		switch b {
		case '\r', '\n':
			sawDelimiter = true
			line := summarizeShellProgressLine(string(c.lineBuffer))
			if line != "" {
				lastLine = line
			}
			c.lineBuffer = c.lineBuffer[:0]
		default:
			c.lineBuffer = append(c.lineBuffer, b)
			if len(c.lineBuffer) > 2048 {
				c.lineBuffer = append([]byte(nil), c.lineBuffer[len(c.lineBuffer)-2048:]...)
			}
		}
	}
	if lastLine == "" && len(c.lineBuffer) > 0 {
		lastLine = summarizeShellProgressLine(string(c.lineBuffer))
	}
	return lastLine, sawDelimiter
}

func formatShellByteCount(size int) string {
	if size >= 1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(size)/float64(1024*1024))
	}
	if size >= 1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	return fmt.Sprintf("%d B", size)
}

func assessShellCommandMutation(command string) shellCommandAssessment {
	unquoted := shellCommandWithoutQuotedLiterals(command)
	lower := strings.ToLower(strings.TrimSpace(unquoted))
	if lower == "" {
		return shellCommandAssessment{Class: shellMutationReadOnly, Reason: "empty command"}
	}
	if reason := shellCommandManualWorkspaceWriteReason(command); reason != "" {
		return shellCommandAssessment{Class: shellMutationWorkspaceWrite, Reason: reason}
	}

	tokens := shellCommandAssessmentTokens(command)
	if len(tokens) == 0 {
		return shellCommandAssessment{Class: shellMutationReadOnly, Reason: "no workspace write markers detected"}
	}
	if shellCommandMutatesGitState(lower) {
		return shellCommandAssessment{Class: shellMutationGitMutation, Reason: "command mutates git state"}
	}

	if shellCommandHasPrefixTokens(tokens,
		[]string{"set-content"},
		[]string{"add-content"},
		[]string{"out-file"},
		[]string{"move-item"},
		[]string{"copy-item"},
		[]string{"remove-item"},
		[]string{"rename-item"},
		[]string{"new-item"},
		[]string{"mkdir"},
		[]string{"md"},
		[]string{"del"},
		[]string{"erase"},
		[]string{"copy"},
		[]string{"move"},
		[]string{"ren"},
		[]string{"rename"},
		[]string{"rm"},
		[]string{"mv"},
		[]string{"cp"},
		[]string{"touch"},
		[]string{"black"},
		[]string{"go", "generate"},
		[]string{"go", "mod", "tidy"},
		[]string{"go", "mod", "vendor"},
		[]string{"go", "get"},
		[]string{"cargo", "add"},
		[]string{"cargo", "vendor"},
		[]string{"dotnet", "add"},
		[]string{"npm", "install"},
		[]string{"npm", "add"},
		[]string{"pnpm", "install"},
		[]string{"pnpm", "add"},
		[]string{"yarn", "install"},
		[]string{"yarn", "add"},
		[]string{"bun", "install"},
	) {
		return shellCommandAssessment{Class: shellMutationWorkspaceWrite, Reason: "command commonly writes build outputs or workspace-managed files"}
	}
	if tokens[0] == "sed" && shellCommandContainsToken(tokens, "-i") {
		return shellCommandAssessment{Class: shellMutationWorkspaceWrite, Reason: "sed -i edits files in place"}
	}
	if tokens[0] == "perl" && shellCommandContainsTokenPrefix(tokens, "-pi") {
		return shellCommandAssessment{Class: shellMutationWorkspaceWrite, Reason: "perl -pi edits files in place"}
	}
	if shellCommandHasPrefixTokens(tokens,
		[]string{"gofmt"},
		[]string{"goimports"},
		[]string{"clang-format"},
	) && (shellCommandContainsToken(tokens, "-w") || shellCommandContainsTokenPrefix(tokens, "-w=") || shellCommandContainsToken(tokens, "-i")) {
		return shellCommandAssessment{Class: shellMutationWorkspaceWrite, Reason: "formatter command edits workspace files in place"}
	}
	if shellCommandHasPrefixTokens(tokens,
		[]string{"prettier"},
	) && shellCommandContainsToken(tokens, "--write") {
		return shellCommandAssessment{Class: shellMutationWorkspaceWrite, Reason: "formatter command edits workspace files in place"}
	}
	if shellCommandHasPrefixTokens(tokens,
		[]string{"ruff", "format"},
	) {
		return shellCommandAssessment{Class: shellMutationWorkspaceWrite, Reason: "formatter command edits workspace files in place"}
	}

	if shellCommandHasPrefixTokens(tokens,
		[]string{"go", "list"},
		[]string{"go", "mod", "download"},
		[]string{"git", "status"},
		[]string{"git", "diff"},
		[]string{"npm", "view"},
		[]string{"pip", "show"},
		[]string{"pip", "list"},
	) {
		return shellCommandAssessment{Class: shellMutationCacheOnly, Reason: "command is read-only or writes only to external caches"}
	}

	verificationPrefixes := [][]string{
		{"go", "test"},
		{"go", "build"},
		{"cargo", "test"},
		{"cargo", "check"},
		{"cargo", "build"},
		{"pytest"},
		{"ctest"},
		{"cmake", "--build"},
		{"msbuild"},
		{"ninja"},
		{"dotnet", "test"},
		{"dotnet", "build"},
		{"npm", "test"},
		{"pnpm", "test"},
		{"yarn", "test"},
		{"bun", "test"},
	}
	if shellCommandInvokesVerificationCommand(tokens, verificationPrefixes...) {
		return shellCommandAssessment{Class: shellMutationVerificationArtifacts, Reason: "command may write build or test artifacts under the workspace"}
	}

	if shellCommandHasPrefixTokens(tokens,
		[]string{"go", "install"},
		[]string{"pip", "install"},
		[]string{"winget", "install"},
		[]string{"choco", "install"},
		[]string{"apt", "install"},
		[]string{"brew", "install"},
		[]string{"uv", "tool", "install"},
	) {
		return shellCommandAssessment{Class: shellMutationExternalInstall, Reason: "command installs tools outside the workspace"}
	}

	return shellCommandAssessment{Class: shellMutationReadOnly, Reason: "no workspace write markers detected"}
}

func shellCommandManualWorkspaceWriteReason(command string) string {
	unquotedLower := strings.ToLower(shellCommandWithoutQuotedLiterals(command))
	if shellCommandHasWorkspaceWriteRedirection(unquotedLower) || strings.Contains(unquotedLower, "| tee ") || strings.HasPrefix(unquotedLower, "tee ") {
		return "output redirection or tee can create workspace files"
	}
	if shellManualWorkspaceWriteCommandPattern.MatchString(unquotedLower) {
		return "manual shell file-write primitive can modify workspace files"
	}
	if shellCommandContainsOutFileArgument(unquotedLower) {
		return "PowerShell -OutFile argument can create workspace files"
	}
	compactUnquoted := compactShellMutationText(unquotedLower)
	compactRaw := compactShellMutationText(command)
	dotNetMutators := []string{
		"[system.io.file]::writealltext",
		"[system.io.file]::writealllines",
		"[system.io.file]::writeallbytes",
		"[system.io.file]::appendalltext",
		"[system.io.file]::appendalllines",
		"[system.io.file]::create",
		"[system.io.file]::delete",
		"[system.io.file]::move",
		"[system.io.file]::copy",
		"[system.io.file]::replace",
		"[io.file]::writealltext",
		"[io.file]::writealllines",
		"[io.file]::writeallbytes",
		"[io.file]::appendalltext",
		"[io.file]::appendalllines",
		"[io.file]::create",
		"[io.file]::delete",
		"[io.file]::move",
		"[io.file]::copy",
		"[io.file]::replace",
	}
	for _, marker := range dotNetMutators {
		if strings.Contains(compactUnquoted, marker) {
			return ".NET file mutation API can modify workspace files"
		}
	}
	if shellCommandInvokesNestedShell(unquotedLower) {
		rawLower := strings.ToLower(command)
		if shellNestedManualWorkspaceWriteCommandPattern.MatchString(rawLower) {
			return "nested shell command contains a file-write primitive"
		}
		for _, marker := range dotNetMutators {
			if strings.Contains(compactRaw, marker) {
				return "nested shell command contains a .NET file mutation API"
			}
		}
	}
	return ""
}

func shellCommandContainsOutFileArgument(command string) bool {
	tokens := splitShellCommandWords(command)
	for _, token := range tokens {
		if token == "-outfile" || strings.HasPrefix(token, "-outfile:") {
			return true
		}
	}
	return false
}

func shellCommandInvokesNestedShell(command string) bool {
	tokens := splitShellCommandWords(command)
	for _, token := range tokens {
		switch strings.TrimSuffix(token, ".exe") {
		case "powershell", "pwsh", "cmd":
			return true
		}
	}
	return false
}

func shellCommandAssessmentTokens(command string) []string {
	unquoted := strings.ToLower(strings.TrimSpace(shellCommandWithoutQuotedLiterals(command)))
	tokens := splitShellCommandWords(unquoted)
	rawTokens := splitShellCommandWords(shellCommandSeparatorsForTokenizing(strings.ToLower(strings.TrimSpace(command))))
	rawTokens = unwrapShellCommandWrapperTokens(rawTokens)
	rawTokens = retokenizeNestedShellPayload(rawTokens)
	unwrappedTokens := unwrapShellCommandWrapperTokens(tokens)
	if len(rawTokens) > 0 && shellCommandShouldPreferRawTokens(unwrappedTokens) {
		return rawTokens
	}
	return unwrappedTokens
}

func shellCommandShouldPreferRawTokens(tokens []string) bool {
	first := ""
	for _, token := range tokens {
		if shellCommandTokenIsSegmentDelimiter(token) {
			continue
		}
		first = strings.TrimSpace(token)
		break
	}
	if first == "" {
		return true
	}
	if strings.HasPrefix(first, "-") {
		return true
	}
	if strings.Contains(first, ".") || strings.ContainsAny(first, `/\`) {
		return true
	}
	return false
}

func unwrapShellCommandWrapperTokens(tokens []string) []string {
unwrap:
	for {
		if len(tokens) >= 3 && shellTokenBaseName(tokens[0]) == "cmd" && tokens[1] == "/s" && tokens[2] == "/c" {
			tokens = tokens[3:]
			continue
		}
		if len(tokens) >= 2 && shellTokenBaseName(tokens[0]) == "cmd" && tokens[1] == "/c" {
			tokens = tokens[2:]
			continue
		}
		if len(tokens) >= 2 {
			base := shellTokenBaseName(tokens[0])
			if base == "powershell" || base == "pwsh" {
				for i := 1; i < len(tokens); i++ {
					switch tokens[i] {
					case "-command", "-c", "-encodedcommand", "-enc":
						if i+1 < len(tokens) {
							tokens = tokens[i+1:]
						} else {
							tokens = nil
						}
						continue unwrap
					}
				}
			}
		}
		return tokens
	}
}

func retokenizeNestedShellPayload(tokens []string) []string {
	if len(tokens) != 1 {
		return tokens
	}
	payload := strings.TrimSpace(tokens[0])
	if !strings.ContainsAny(payload, " \t;&|()") {
		return tokens
	}
	return splitShellCommandWords(shellCommandSeparatorsForTokenizing(payload))
}

func shellCommandSeparatorsForTokenizing(command string) string {
	replacer := strings.NewReplacer(
		"&&", " && ",
		"||", " || ",
		"&", " & ",
		";", " ; ",
		"|", " | ",
		"(", " ( ",
		")", " ) ",
	)
	return replacer.Replace(command)
}

func shellTokenBaseName(token string) string {
	base := strings.TrimSpace(strings.TrimSuffix(token, ".exe"))
	base = strings.Trim(base, `"'`)
	if idx := strings.LastIndexAny(base, `/\`); idx >= 0 && idx+1 < len(base) {
		base = base[idx+1:]
	}
	return base
}

func compactShellMutationText(command string) string {
	lower := strings.ToLower(command)
	var b strings.Builder
	b.Grow(len(lower))
	for _, ch := range lower {
		switch ch {
		case ' ', '\t', '\r', '\n', '`':
			continue
		default:
			b.WriteRune(ch)
		}
	}
	return b.String()
}

func shellCommandWithoutQuotedLiterals(command string) string {
	var b strings.Builder
	b.Grow(len(command))
	quote := rune(0)
	for _, ch := range command {
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			switch ch {
			case '\r', '\n', ';', '|', '&', '(', ')', '{', '}':
				b.WriteRune(ch)
			default:
				b.WriteRune(' ')
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
			b.WriteRune(' ')
		default:
			b.WriteRune(ch)
		}
	}
	return b.String()
}

func shellCommandMutatesGitState(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return false
	}
	return shellGitMutationPattern.MatchString(lower)
}

func shellCommandHasWorkspaceWriteRedirection(command string) bool {
	cleaned := strings.ToLower(strings.TrimSpace(command))
	if cleaned == "" {
		return false
	}
	matches := shellFileWriteRedirectionTargetPattern.FindAllStringSubmatch(cleaned, -1)
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		target := strings.TrimSpace(match[2])
		switch target {
		case "/dev/null", "$null", "nul":
			continue
		}
		if strings.HasPrefix(target, "&") {
			continue
		}
		return true
	}
	return false
}

func splitShellCommandWords(command string) []string {
	var tokens []string
	var current strings.Builder
	quote := byte(0)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, strings.ToLower(current.String()))
		current.Reset()
	}
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
				continue
			}
			current.WriteByte(ch)
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
		case ' ', '\t', '\r', '\n':
			flush()
		default:
			current.WriteByte(ch)
		}
	}
	flush()
	return tokens
}

func shellCommandHasPrefixTokens(tokens []string, prefixes ...[]string) bool {
	for _, prefix := range prefixes {
		if len(tokens) < len(prefix) {
			continue
		}
		matched := true
		for i := 0; i < len(prefix); i++ {
			token := tokens[i]
			if i == 0 {
				token = shellTokenBaseName(token)
			}
			if token != prefix[i] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func shellCommandHasSegmentPrefixTokens(tokens []string, prefixes ...[]string) bool {
	start := 0
	for start < len(tokens) {
		for start < len(tokens) && shellCommandTokenIsSegmentDelimiter(tokens[start]) {
			start++
		}
		end := start
		for end < len(tokens) && !shellCommandTokenIsSegmentDelimiter(tokens[end]) {
			end++
		}
		if start < end && shellCommandHasPrefixTokens(tokens[start:end], prefixes...) {
			return true
		}
		start = end + 1
	}
	return false
}

func shellCommandInvokesVerificationCommand(tokens []string, prefixes ...[]string) bool {
	if shellCommandHasPrefixTokens(tokens, prefixes...) || shellCommandHasSegmentPrefixTokens(tokens, prefixes...) {
		return true
	}
	aliases := shellCommandVerificationAliases(tokens, prefixes...)
	if len(aliases) == 0 {
		return false
	}
	for i, token := range tokens {
		if token != "&" || i+1 >= len(tokens) {
			continue
		}
		if shellCommandTokenMatchesAlias(tokens[i+1], aliases) {
			return true
		}
	}
	return false
}

func shellCommandVerificationAliases(tokens []string, prefixes ...[]string) map[string]bool {
	aliases := map[string]bool{}
	for i := 0; i+2 < len(tokens); i++ {
		name := strings.TrimSpace(tokens[i])
		if !strings.HasPrefix(name, "$") || tokens[i+1] != "=" || shellTokenBaseName(tokens[i+2]) != "get-command" {
			continue
		}
		if shellCommandSegmentContainsVerificationExecutable(tokens[i+3:], prefixes...) {
			aliases[name] = true
		}
	}
	return aliases
}

func shellCommandSegmentContainsVerificationExecutable(tokens []string, prefixes ...[]string) bool {
	for _, token := range tokens {
		if shellCommandTokenIsSegmentDelimiter(token) {
			return false
		}
		if shellCommandTokenMatchesVerificationExecutable(token, prefixes...) {
			return true
		}
	}
	return false
}

func shellCommandTokenMatchesVerificationExecutable(token string, prefixes ...[]string) bool {
	base := shellTokenBaseName(token)
	for _, prefix := range prefixes {
		if len(prefix) > 0 && base == prefix[0] {
			return true
		}
	}
	return false
}

func shellCommandTokenMatchesAlias(token string, aliases map[string]bool) bool {
	token = strings.TrimSpace(strings.Trim(token, `"'`))
	if idx := strings.IndexAny(token, ".["); idx >= 0 {
		token = token[:idx]
	}
	return aliases[token]
}

func shellCommandTokenIsSegmentDelimiter(token string) bool {
	switch strings.TrimSpace(token) {
	case ";", "&", "&&", "||", "|", "(", ")":
		return true
	default:
		return false
	}
}

func shellCommandTokensHaveCommandWord(tokens []string) bool {
	for _, token := range tokens {
		if !shellCommandTokenIsSegmentDelimiter(token) {
			return true
		}
	}
	return false
}

func shellCommandContainsToken(tokens []string, want string) bool {
	for _, token := range tokens {
		if token == want {
			return true
		}
	}
	return false
}

func shellCommandContainsTokenPrefix(tokens []string, prefix string) bool {
	for _, token := range tokens {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}

func hookCommandRiskTags(command string) []string {
	lower := strings.ToLower(strings.TrimSpace(command))
	var tags []string
	if strings.Contains(lower, "bcdedit") || strings.Contains(lower, "verifier") {
		tags = append(tags, "windows")
	}
	if strings.Contains(lower, "signtool") || strings.Contains(lower, "symchk") {
		tags = append(tags, "signing")
	}
	if strings.Contains(lower, "fltmc") || strings.Contains(lower, ".sys") {
		tags = append(tags, "driver")
	}
	return uniqueStrings(tags)
}

func hookFileTags(path string) []string {
	lower := strings.ToLower(filepath.ToSlash(path))
	var tags []string
	switch filepath.Ext(lower) {
	case ".c", ".cc", ".cpp", ".h", ".hpp":
		tags = append(tags, "cpp")
	case ".go":
		tags = append(tags, "go")
	case ".sys", ".inf", ".cat":
		tags = append(tags, "driver")
	}
	if strings.Contains(lower, "/driver/") || strings.HasSuffix(lower, ".sys") || strings.HasSuffix(lower, ".inf") || strings.HasSuffix(lower, ".cat") {
		tags = append(tags, "driver")
	}
	if strings.Contains(lower, "kernel") || strings.Contains(lower, "/driver/") || strings.HasSuffix(lower, ".sys") {
		tags = append(tags, "kernel")
	}
	return uniqueStrings(tags)
}

func normalizedHookFileTagsForPaths(paths []string) []string {
	if len(paths) == 0 {
		return []string{}
	}
	var tags []string
	for _, path := range paths {
		tags = append(tags, hookFileTags(path)...)
	}
	return uniqueStrings(tags)
}

type GitStatusTool struct{ ws Workspace }

func NewGitStatusTool(ws Workspace) GitStatusTool { return GitStatusTool{ws: ws} }

type GitAddTool struct{ ws Workspace }

func NewGitAddTool(ws Workspace) GitAddTool { return GitAddTool{ws: ws} }

func (t GitAddTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "git_add",
		Description: "Stage specific paths or all tracked and untracked changes in the current workspace.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"paths": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"all": map[string]any{"type": "boolean"},
			},
		},
	}
}

func (t GitAddTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	all := boolValue(args, "all", false)
	paths := stringSliceValue(args, "paths")
	if all && len(paths) > 0 {
		return "", fmt.Errorf("provide either all=true or paths, not both")
	}
	if !all && len(paths) == 0 {
		return "", fmt.Errorf("paths are required unless all=true")
	}
	if err := t.ws.EnsureGit("stage changes with git_add"); err != nil {
		return "", err
	}
	cmdArgs := []string{"add"}
	if all {
		cmdArgs = append(cmdArgs, "--all")
	} else {
		for _, rawPath := range paths {
			resolved, err := t.ws.Resolve(rawPath)
			if err != nil {
				return "", err
			}
			rel, err := filepath.Rel(t.ws.Root, resolved)
			if err != nil {
				return "", err
			}
			cmdArgs = append(cmdArgs, rel)
		}
	}
	if _, err := runGitCommand(ctx, t.ws.Root, cmdArgs...); err != nil {
		return "", err
	}
	status, err := runGitCommand(ctx, t.ws.Root, "status", "--short")
	if err != nil {
		return "", err
	}
	summary := "staged changes"
	if all {
		summary = "staged all changes"
	} else {
		summary = fmt.Sprintf("staged %d path(s)", len(paths))
	}
	if status == "(no output)" {
		status = "(no staged or unstaged changes remain)"
	}
	return joinNonEmpty(summary, status), nil
}

func (t GitAddTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	all := boolValue(args, "all", false)
	paths := normalizeTaskStateList(stringSliceValue(args, "paths"), 32)
	text, err := t.Execute(ctx, input)
	meta := map[string]any{
		"effect":       "git_mutation",
		"all":          all,
		"paths":        paths,
		"stage_scope":  "paths",
		"staged":       err == nil,
		"staged_count": len(paths),
	}
	if all {
		meta["stage_scope"] = "all"
	}
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

type GitCommitTool struct{ ws Workspace }

func NewGitCommitTool(ws Workspace) GitCommitTool { return GitCommitTool{ws: ws} }

func (t GitCommitTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "git_commit",
		Description: "Create a git commit from currently staged changes.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message":     map[string]any{"type": "string"},
				"allow_empty": map[string]any{"type": "boolean"},
			},
			"required": []string{"message"},
		},
	}
}

func (t GitCommitTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	message := stringValue(args, "message")
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("message is required")
	}
	if err := t.ws.EnsureGit("create commit: " + firstLine(message)); err != nil {
		return "", err
	}
	cmdArgs := []string{"commit", "-m", message}
	if boolValue(args, "allow_empty", false) {
		cmdArgs = append(cmdArgs, "--allow-empty")
	}
	out, err := runGitCommand(ctx, t.ws.Root, cmdArgs...)
	if err != nil {
		return out, err
	}
	shortSHA, err := runGitCommand(ctx, t.ws.Root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return out, err
	}
	subject, err := runGitCommand(ctx, t.ws.Root, "log", "-1", "--pretty=%s")
	if err != nil {
		return out, err
	}
	return joinNonEmpty(
		fmt.Sprintf("created commit %s: %s", shortSHA, subject),
		out,
	), nil
}

func (t GitCommitTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	message := stringValue(args, "message")
	allowEmpty := boolValue(args, "allow_empty", false)
	text, err := t.Execute(ctx, input)
	commitSHA := ""
	commitSubject := strings.TrimSpace(firstLine(message))
	branch := ""
	if err == nil {
		commitSHA, _ = runGitCommand(ctx, t.ws.Root, "rev-parse", "--short", "HEAD")
		if subject, subjectErr := runGitCommand(ctx, t.ws.Root, "log", "-1", "--pretty=%s"); subjectErr == nil {
			commitSubject = subject
		}
		branch, _ = gitCurrentBranch(ctx, t.ws.Root)
	}
	meta := map[string]any{
		"effect":         "git_mutation",
		"message":        message,
		"allow_empty":    allowEmpty,
		"created_commit": err == nil,
		"commit_sha":     commitSHA,
		"commit_subject": commitSubject,
		"branch":         branch,
	}
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

type GitPushTool struct{ ws Workspace }

func NewGitPushTool(ws Workspace) GitPushTool { return GitPushTool{ws: ws} }

func (t GitPushTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "git_push",
		Description: "Push the current or specified branch to a remote and optionally set upstream.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"remote":       map[string]any{"type": "string"},
				"branch":       map[string]any{"type": "string"},
				"set_upstream": map[string]any{"type": "boolean"},
			},
		},
	}
}

func (t GitPushTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	remote := stringValue(args, "remote")
	if strings.TrimSpace(remote) == "" {
		remote = "origin"
	}
	branch := stringValue(args, "branch")
	if strings.TrimSpace(branch) == "" {
		currentBranch, err := gitCurrentBranch(ctx, t.ws.Root)
		if err != nil {
			return "", err
		}
		branch = currentBranch
	}
	changedFiles, _ := gitChangedFiles(ctx, t.ws.Root)
	if _, err := t.ws.Hook(ctx, HookPreGitPush, HookPayload{
		"remote":        remote,
		"branch":        branch,
		"changed_files": changedFiles,
	}); err != nil {
		return "", err
	}
	if _, err := t.ws.Hook(ctx, HookPreToolUse, HookPayload{
		"tool_name":     "git_push",
		"tool_kind":     "git",
		"command":       fmt.Sprintf("git push %s %s", remote, branch),
		"branch":        branch,
		"changed_files": changedFiles,
	}); err != nil {
		return "", err
	}
	if err := t.ws.EnsureGit(fmt.Sprintf("push branch %s to %s", branch, remote)); err != nil {
		return "", err
	}
	cmdArgs := []string{"push"}
	if boolValue(args, "set_upstream", true) {
		hasUpstream, err := gitHasUpstream(ctx, t.ws.Root)
		if err != nil {
			return "", err
		}
		if !hasUpstream {
			cmdArgs = append(cmdArgs, "-u")
		}
	}
	cmdArgs = append(cmdArgs, remote, branch)
	out, err := runGitCommand(ctx, t.ws.Root, cmdArgs...)
	if err != nil {
		return out, err
	}
	if _, err := t.ws.Hook(ctx, HookPostToolUse, HookPayload{
		"tool_name": "git_push",
		"tool_kind": "git",
		"command":   strings.Join(append([]string{"git"}, cmdArgs...), " "),
		"branch":    branch,
		"output":    out,
	}); err != nil {
		return "", err
	}
	return joinNonEmpty(
		fmt.Sprintf("pushed %s to %s", branch, remote),
		out,
	), nil
}

func (t GitPushTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	remote := firstNonBlankString(stringValue(args, "remote"), "origin")
	branch := stringValue(args, "branch")
	setUpstream := boolValue(args, "set_upstream", true)
	text, err := t.Execute(ctx, input)
	if strings.TrimSpace(branch) == "" && err == nil {
		branch, _ = gitCurrentBranch(ctx, t.ws.Root)
	}
	meta := map[string]any{
		"effect":       "git_mutation",
		"remote":       remote,
		"branch":       strings.TrimSpace(branch),
		"set_upstream": setUpstream,
		"pushed":       err == nil,
	}
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

type GitCreatePRTool struct{ ws Workspace }

func NewGitCreatePRTool(ws Workspace) GitCreatePRTool { return GitCreatePRTool{ws: ws} }

func (t GitCreatePRTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "git_create_pr",
		Description: "Create a GitHub pull request for the current branch using the gh CLI. By default this pushes the branch first.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":       map[string]any{"type": "string"},
				"body":        map[string]any{"type": "string"},
				"base_branch": map[string]any{"type": "string"},
				"remote":      map[string]any{"type": "string"},
				"branch":      map[string]any{"type": "string"},
				"draft":       map[string]any{"type": "boolean"},
				"fill":        map[string]any{"type": "boolean"},
				"push":        map[string]any{"type": "boolean"},
			},
		},
	}
}

func (t GitCreatePRTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return "", fmt.Errorf("gh CLI is required to create a pull request: %w", err)
	}
	branch := stringValue(args, "branch")
	if strings.TrimSpace(branch) == "" {
		currentBranch, err := gitCurrentBranch(ctx, t.ws.Root)
		if err != nil {
			return "", err
		}
		branch = currentBranch
	}
	remote := stringValue(args, "remote")
	if strings.TrimSpace(remote) == "" {
		remote = "origin"
	}
	fill := boolValue(args, "fill", false)
	title := stringValue(args, "title")
	if !fill && strings.TrimSpace(title) == "" {
		return "", fmt.Errorf("title is required unless fill=true")
	}
	if boolValue(args, "push", true) {
		pushTool := NewGitPushTool(t.ws)
		if _, err := pushTool.Execute(ctx, map[string]any{
			"remote":       remote,
			"branch":       branch,
			"set_upstream": true,
		}); err != nil {
			return "", err
		}
	}
	changedFiles, _ := gitChangedFiles(ctx, t.ws.Root)
	if _, err := t.ws.Hook(ctx, HookPreCreatePR, HookPayload{
		"remote":        remote,
		"branch":        branch,
		"changed_files": changedFiles,
		"title":         title,
	}); err != nil {
		return "", err
	}
	if _, err := t.ws.Hook(ctx, HookPreToolUse, HookPayload{
		"tool_name":     "git_create_pr",
		"tool_kind":     "git",
		"command":       "gh pr create",
		"branch":        branch,
		"changed_files": changedFiles,
	}); err != nil {
		return "", err
	}
	if err := t.ws.EnsureGit("create pull request for " + branch); err != nil {
		return "", err
	}
	cmdArgs := []string{"pr", "create", "--head", branch}
	if base := stringValue(args, "base_branch"); strings.TrimSpace(base) != "" {
		cmdArgs = append(cmdArgs, "--base", base)
	}
	if boolValue(args, "draft", false) {
		cmdArgs = append(cmdArgs, "--draft")
	}
	if fill {
		cmdArgs = append(cmdArgs, "--fill")
	} else {
		cmdArgs = append(cmdArgs, "--title", title, "--body", stringValue(args, "body"))
	}
	out, err := runCommand(ctx, t.ws.Root, "gh", cmdArgs...)
	if err != nil {
		return out, err
	}
	if _, err := t.ws.Hook(ctx, HookPostToolUse, HookPayload{
		"tool_name": "git_create_pr",
		"tool_kind": "git",
		"command":   strings.Join(append([]string{"gh"}, cmdArgs...), " "),
		"branch":    branch,
		"output":    out,
	}); err != nil {
		return "", err
	}
	return joinNonEmpty(
		fmt.Sprintf("created pull request for %s", branch),
		out,
	), nil
}

func (t GitCreatePRTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	branch := strings.TrimSpace(stringValue(args, "branch"))
	remote := firstNonBlankString(stringValue(args, "remote"), "origin")
	fill := boolValue(args, "fill", false)
	draft := boolValue(args, "draft", false)
	push := boolValue(args, "push", true)
	baseBranch := strings.TrimSpace(stringValue(args, "base_branch"))
	title := stringValue(args, "title")
	text, err := t.Execute(ctx, input)
	if branch == "" && err == nil {
		branch, _ = gitCurrentBranch(ctx, t.ws.Root)
	}
	meta := map[string]any{
		"effect":      "git_mutation",
		"remote":      remote,
		"branch":      branch,
		"base_branch": baseBranch,
		"draft":       draft,
		"fill":        fill,
		"push":        push,
		"title":       title,
		"pr_created":  err == nil,
		"pr_url":      firstHTTPURL(text),
	}
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

func (t GitStatusTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "git_status",
		Description: "Show git status for the current workspace.",
		InputSchema: emptyObjectSchema(),
	}
}

func (t GitStatusTool) Execute(ctx context.Context, input any) (string, error) {
	if _, err := requireToolInputObject(input, t.Definition().Name); err != nil {
		return "", err
	}
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

func (t GitStatusTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	text, err := t.Execute(ctx, input)
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	branch := ""
	changedPaths := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			branch = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			continue
		}
		if len(trimmed) > 3 {
			changedPaths = append(changedPaths, strings.TrimSpace(trimmed[3:]))
		}
	}
	meta := map[string]any{
		"branch":        branch,
		"changed_paths": normalizeTaskStateList(changedPaths, 32),
		"changed_count": len(changedPaths),
		"clean":         len(changedPaths) == 0 && err == nil,
		"effect":        "inspect",
	}
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
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
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
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

func (t GitDiffTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	text, err := t.Execute(ctx, input)
	fileCount := 0
	for _, line := range strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "diff --git ") {
			fileCount++
		}
	}
	meta := map[string]any{
		"path":       strings.TrimSpace(stringValue(args, "path")),
		"staged":     boolValue(args, "staged", false),
		"has_diff":   strings.TrimSpace(text) != "" && strings.TrimSpace(text) != "(no diff)",
		"file_count": fileCount,
		"line_count": textLineCount(text),
		"effect":     "inspect",
	}
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

func runCommand(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("%s failed: %w", summarizeExec(name, args...), err)
	}
	if text == "" {
		return "(no output)", nil
	}
	return text, nil
}

func runGitCommand(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := runCommand(ctx, dir, "git", args...)
	if err != nil {
		return out, fmt.Errorf("git command failed: %w", err)
	}
	return out, nil
}

func summarizeExec(name string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	parts = append(parts, args...)
	return summarizeShellCommand(strings.Join(parts, " "))
}

func gitCurrentBranch(ctx context.Context, dir string) (string, error) {
	branch, err := runGitCommand(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if branch == "HEAD" {
		return "", fmt.Errorf("git repository is in detached HEAD state")
	}
	return branch, nil
}

func gitHasUpstream(ctx context.Context, dir string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if strings.Contains(text, "no upstream configured") || strings.Contains(text, "HEAD branch has no upstream branch") {
			return false, nil
		}
		if text == "" {
			text = err.Error()
		}
		return false, fmt.Errorf("failed to inspect upstream branch: %s", text)
	}
	return true, nil
}

func gitChangedFiles(ctx context.Context, dir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-c", "core.quotePath=false", "status", "--short")
	cmd.Dir = dir
	data, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(data))
		if text == "" {
			text = err.Error()
		}
		return nil, fmt.Errorf("git status --short failed: %s", text)
	}
	out := strings.TrimRight(string(data), "\r\n")
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		path, ok := parseGitStatusShortPath(line)
		if ok {
			files = append(files, path)
		}
	}
	return uniqueStrings(files), nil
}

func firstHTTPURL(text string) string {
	matches := regexp.MustCompile(`https?://[^\s]+`).FindString(strings.TrimSpace(text))
	return strings.TrimSpace(matches)
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
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
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

func (t UpdatePlanTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	text, err := t.Execute(ctx, input)
	rawItems, _ := args["items"].([]any)
	pendingCount := 0
	inProgressCount := 0
	completedCount := 0
	for _, raw := range rawItems {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(stringValue(obj, "status"))) {
		case "completed":
			completedCount++
		case "in_progress":
			inProgressCount++
		default:
			pendingCount++
		}
	}
	meta := map[string]any{
		"plan_item_count":   len(rawItems),
		"pending_count":     pendingCount,
		"in_progress_count": inProgressCount,
		"completed_count":   completedCount,
		"effect":            "plan",
	}
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
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

func stringSliceValue(m map[string]any, key string) []string {
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case []string:
			return append([]string(nil), x...)
		case []any:
			out := make([]string, 0, len(x))
			for _, item := range x {
				s, ok := item.(string)
				if !ok {
					continue
				}
				if strings.TrimSpace(s) == "" {
					continue
				}
				out = append(out, s)
			}
			return out
		}
	}
	return nil
}
