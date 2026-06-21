package main

// LSPNavigationTool exposes opt-in, read-only code navigation backed by a
// Language Server Protocol server pool. It supports three actions at a
// file:line:col position: definition, references, and hover. The tool never
// edits or executes project code; it only queries the server for locations and
// hover text.
//
// Safety properties:
//   - Read-only: ReadOnlyToolCall reports true and the tool only issues
//     navigation requests.
//   - Path containment: model-supplied paths are resolved against the workspace
//     root and rejected if they escape it (reusing ensureResolvedPathWithinRoot).
//   - Bounded: every request goes through the pool's per-request timeout, so a
//     stuck or missing server degrades to a clear message rather than hanging.
//   - Opt-in: when the pool is absent (lsp.enabled = false) the tool returns a
//     "not enabled" message so the model falls back to grep + read_file.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LSPNavigationTool implements the lsp_nav tool. The pool may be nil when LSP
// navigation is disabled; the tool handles that as a graceful no-op.
type LSPNavigationTool struct {
	ws Workspace
}

func NewLSPNavigationTool(ws Workspace) *LSPNavigationTool {
	return &LSPNavigationTool{ws: ws}
}

// ReadOnlyToolCall marks lsp_nav as non-mutating so it can run without an edit
// gate. The tool only performs navigation queries.
func (t *LSPNavigationTool) ReadOnlyToolCall() bool {
	return true
}

// SupportsParallelToolCalls allows the model to issue several navigation lookups
// at once; each request is independently bounded by the pool timeout.
func (t *LSPNavigationTool) SupportsParallelToolCalls() bool {
	return true
}

func (t *LSPNavigationTool) hookWorkspace() Workspace {
	return t.ws
}

func (t *LSPNavigationTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "lsp_nav",
		Description: "Read-only code navigation via a Language Server (gopls for Go, clangd for C/C++). Resolve a symbol's definition, find references, or read hover documentation at a file:line:col position. Opt-in (config lsp.enabled); when unavailable it returns a clear message so you can fall back to grep + read_file.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"definition", "references", "hover"},
					"description": "Navigation action: definition (jump to declaration), references (find usages), or hover (type/doc text).",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Workspace-relative path to the source file containing the symbol.",
				},
				"line": map[string]any{
					"type":        "integer",
					"description": "1-based line number of the symbol.",
				},
				"column": map[string]any{
					"type":        "integer",
					"description": "1-based column number of the symbol within the line.",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": "Optional cap on locations returned for references/definition. Defaults to a safe limit.",
				},
			},
			"required": []string{"action", "path", "line", "column"},
		},
	}
}

func (t *LSPNavigationTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t *LSPNavigationTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	args, err := requireToolInputObject(input, "lsp_nav")
	if err != nil {
		return ToolExecutionResult{}, err
	}
	action := strings.ToLower(strings.TrimSpace(stringValue(args, "action")))
	switch action {
	case "definition", "references", "hover":
	default:
		return ToolExecutionResult{}, fmt.Errorf("lsp_nav action must be one of definition, references, hover")
	}
	rawPath := strings.TrimSpace(stringValue(args, "path"))
	if rawPath == "" {
		return ToolExecutionResult{}, fmt.Errorf("lsp_nav requires a non-empty path")
	}
	line := intValue(args, "line", 0)
	col := intValue(args, "column", 0)
	if line <= 0 {
		return ToolExecutionResult{}, fmt.Errorf("lsp_nav requires a positive 1-based line")
	}
	if col <= 0 {
		return ToolExecutionResult{}, fmt.Errorf("lsp_nav requires a positive 1-based column")
	}
	maxResults := intValue(args, "max_results", 50)
	if maxResults <= 0 {
		maxResults = 50
	}

	// Resolve the path against the workspace root and reject anything that
	// escapes it. ResolveLookupPath applies the same routing as read_file/grep;
	// ensureResolvedPathWithinRoot is the final containment check against the
	// active root (symlink-aware).
	ownerNodeID := stringValue(args, "owner_node_id")
	route, err := t.ws.ResolveLookupPath(rawPath, ownerNodeID)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	absPath := route.AbsolutePath
	displayRoot := firstNonBlankString(route.DisplayRoot, t.ws.Root, t.ws.BaseRoot)
	activeRoot := strings.TrimSpace(displayRoot)
	if activeRoot == "" {
		activeRoot = strings.TrimSpace(t.ws.Root)
	}
	if activeRoot != "" {
		safe, err := ensureResolvedPathWithinRoot(activeRoot, absPath)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		absPath = safe
	}
	if info, err := os.Stat(absPath); err != nil {
		if os.IsNotExist(err) {
			return ToolExecutionResult{}, fmt.Errorf("lsp_nav target does not exist: %s", relOrAbs(displayRoot, absPath))
		}
		return ToolExecutionResult{}, err
	} else if info.IsDir() {
		return ToolExecutionResult{}, fmt.Errorf("lsp_nav target is a directory: %s", relOrAbs(displayRoot, absPath))
	}

	language, ok := lspLanguageForPath(absPath)
	if !ok {
		return t.degrade(action, displayRoot, absPath,
			fmt.Sprintf("no language server is mapped for files with extension %q", strings.ToLower(filepath.Ext(absPath)))), nil
	}

	pool := t.ws.LSP
	if pool == nil {
		return t.degrade(action, displayRoot, absPath,
			"lsp navigation is not enabled (set lsp.enabled true in config to launch a language server); fall back to grep + read_file"), nil
	}

	switch action {
	case "definition":
		locations, err := pool.Definition(ctx, language, activeRoot, absPath, line, col)
		if err != nil {
			return t.degradeOnError(action, displayRoot, absPath, err), nil
		}
		return t.renderLocations(action, displayRoot, absPath, line, col, locations, maxResults), nil
	case "references":
		locations, err := pool.References(ctx, language, activeRoot, absPath, line, col)
		if err != nil {
			return t.degradeOnError(action, displayRoot, absPath, err), nil
		}
		return t.renderLocations(action, displayRoot, absPath, line, col, locations, maxResults), nil
	case "hover":
		text, err := pool.Hover(ctx, language, activeRoot, absPath, line, col)
		if err != nil {
			return t.degradeOnError(action, displayRoot, absPath, err), nil
		}
		return t.renderHover(displayRoot, absPath, line, col, text), nil
	}
	return ToolExecutionResult{}, fmt.Errorf("lsp_nav action must be one of definition, references, hover")
}

// degrade renders a clear, non-error message for a graceful fallback (server
// not available / language unmapped). It returns no Go error so the turn
// continues and the model can choose grep instead.
func (t *LSPNavigationTool) degrade(action string, displayRoot string, absPath string, reason string) ToolExecutionResult {
	display := fmt.Sprintf("lsp_nav %s unavailable for %s: %s", action, relOrAbs(displayRoot, absPath), reason)
	return ToolExecutionResult{
		DisplayText: display,
		Meta: map[string]any{
			"action":    action,
			"available": false,
			"reason":    reason,
		},
	}
}

// degradeOnError maps a pool error to a graceful fallback message. Timeouts,
// missing servers, and rejected binaries are all non-fatal so the model can
// fall back to grep without the turn failing.
func (t *LSPNavigationTool) degradeOnError(action string, displayRoot string, absPath string, err error) ToolExecutionResult {
	reason := lspDegradeReason(err)
	result := t.degrade(action, displayRoot, absPath, reason)
	result.Meta["error"] = err.Error()
	return result
}

func lspDegradeReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "the language server did not respond within the request timeout; fall back to grep + read_file"
	case errors.Is(err, context.Canceled):
		return "the request was canceled"
	case errors.Is(err, errLSPDisabled):
		return "lsp navigation is not enabled; fall back to grep + read_file"
	case errors.Is(err, errLSPServerNotFound):
		return "the language server binary could not be resolved; fall back to grep + read_file"
	case errors.Is(err, errLSPBinaryRejected):
		return "the resolved language server binary is not permitted by the configured allowlist"
	case errors.Is(err, errLSPUnsupportedLang):
		return "this language is not supported by lsp navigation; fall back to grep + read_file"
	default:
		return "the language server request failed; fall back to grep + read_file"
	}
}

func (t *LSPNavigationTool) renderLocations(action string, displayRoot string, absPath string, line int, col int, locations []LSPLocation, maxResults int) ToolExecutionResult {
	header := fmt.Sprintf("lsp_nav %s at %s:%d:%d", action, relOrAbs(displayRoot, absPath), line, col)
	if len(locations) == 0 {
		return ToolExecutionResult{
			DisplayText: header + "\n(no results)",
			Meta: map[string]any{
				"action":    action,
				"available": true,
				"count":     0,
			},
		}
	}
	truncated := false
	if len(locations) > maxResults {
		locations = locations[:maxResults]
		truncated = true
	}
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	for _, loc := range locations {
		b.WriteString("- ")
		b.WriteString(t.formatLocation(displayRoot, loc))
		b.WriteString("\n")
	}
	if truncated {
		b.WriteString(fmt.Sprintf("(results truncated to %d)\n", maxResults))
	}
	return ToolExecutionResult{
		DisplayText: strings.TrimRight(b.String(), "\n"),
		Meta: map[string]any{
			"action":    action,
			"available": true,
			"count":     len(locations),
			"truncated": truncated,
		},
	}
}

func (t *LSPNavigationTool) formatLocation(displayRoot string, loc LSPLocation) string {
	display := loc.Path
	if rooted := relOrAbs(displayRoot, loc.Path); rooted != "" {
		display = rooted
	}
	if loc.StartLine > 0 && loc.StartCol > 0 {
		return fmt.Sprintf("%s:%d:%d", display, loc.StartLine, loc.StartCol)
	}
	if loc.StartLine > 0 {
		return fmt.Sprintf("%s:%d", display, loc.StartLine)
	}
	return display
}

func (t *LSPNavigationTool) renderHover(displayRoot string, absPath string, line int, col int, text string) ToolExecutionResult {
	header := fmt.Sprintf("lsp_nav hover at %s:%d:%d", relOrAbs(displayRoot, absPath), line, col)
	text = strings.TrimSpace(text)
	if text == "" {
		return ToolExecutionResult{
			DisplayText: header + "\n(no hover information)",
			Meta: map[string]any{
				"action":    "hover",
				"available": true,
				"empty":     true,
			},
		}
	}
	return ToolExecutionResult{
		DisplayText: header + "\n" + text,
		Meta: map[string]any{
			"action":    "hover",
			"available": true,
			"empty":     false,
		},
	}
}
