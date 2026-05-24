package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWorkspaceResolveForLookupFallsBackToBaseRoot(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(base, "root_only.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ws := Workspace{
		BaseRoot: base,
		Root:     current,
	}

	resolved, err := ws.ResolveForLookup("root_only.txt")
	if err != nil {
		t.Fatalf("ResolveForLookup: %v", err)
	}
	if resolved != target {
		t.Fatalf("expected fallback path %q, got %q", target, resolved)
	}
}

func TestWorkspaceResolveForLookupRejectsSymlinkOutsideRoot(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("secret"), 0o644); err != nil {
		t.Fatalf("WriteFile external: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}

	_, err := ws.ResolveForLookup("link.txt")
	if err == nil {
		t.Fatalf("expected symlink lookup outside root to be rejected")
	}
	if !strings.Contains(err.Error(), "resolves outside the active workspace root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadFileRejectsSymlinkOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("secret"), 0o644); err != nil {
		t.Fatalf("WriteFile external: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	tool := NewReadFileTool(Workspace{
		BaseRoot: root,
		Root:     root,
	})
	_, err := tool.Execute(context.Background(), map[string]any{
		"path": "link.txt",
	})
	if err == nil {
		t.Fatalf("expected read_file through outside symlink to be rejected")
	}
	if !strings.Contains(err.Error(), "resolves outside the active workspace root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLookupToolsRecoverFromOwnerEditMismatch(t *testing.T) {
	assertLookupToolsRecoverFromOwnerEditMismatch(t, true)
}

func TestLookupToolsRecoverFromRawOwnerEditMismatch(t *testing.T) {
	assertLookupToolsRecoverFromOwnerEditMismatch(t, false)
}

func TestLookupToolsRecoverFromOwnerEditMismatchWithAbsoluteWorkspacePath(t *testing.T) {
	for _, wrapSentinel := range []bool{true, false} {
		wrapSentinel := wrapSentinel
		t.Run(fmt.Sprintf("wrapSentinel=%v", wrapSentinel), func(t *testing.T) {
			root := t.TempDir()
			sourceDir := filepath.Join(root, "Tavern", "Tavern")
			if err := os.MkdirAll(sourceDir, 0o755); err != nil {
				t.Fatalf("MkdirAll sourceDir: %v", err)
			}
			sourceFile := filepath.Join(sourceDir, "RuntimeManager.cpp")
			if err := os.WriteFile(sourceFile, []byte("int runtime_manager_marker = 1;\n"), 0o644); err != nil {
				t.Fatalf("WriteFile sourceFile: %v", err)
			}

			ws := Workspace{
				BaseRoot: root,
				Root:     root,
			}
			ws.ResolveEditTarget = func(req EditRoutingRequest) (EditRoutingResult, error) {
				req = req.normalized()
				if req.lookupIntent() && strings.TrimSpace(req.OwnerNodeID) != "" {
					if !wrapSentinel {
						return EditRoutingResult{}, fmt.Errorf("edit target mismatch: path %s is outside editable ownership for specialist driver-build-fixer", req.Path)
					}
					return EditRoutingResult{}, fmt.Errorf("%w: path %s is outside editable ownership for specialist driver-build-fixer", ErrEditTargetMismatch, req.Path)
				}
				return ws.resolveEditFallback(req)
			}

			readTool := NewReadFileTool(ws)
			readResult, err := readTool.ExecuteDetailed(context.Background(), map[string]any{
				"path":          sourceFile,
				"owner_node_id": "plan-01",
			})
			if err != nil {
				t.Fatalf("read_file should fall back for absolute workspace path after owner mismatch: %v", err)
			}
			if !strings.Contains(readResult.DisplayText, "runtime_manager_marker") {
				t.Fatalf("expected read_file to return absolute workspace file content, got %q", readResult.DisplayText)
			}

			listTool := NewListFilesTool(ws)
			listResult, err := listTool.ExecuteDetailed(context.Background(), map[string]any{
				"path":          sourceDir,
				"owner_node_id": "plan-01",
			})
			if err != nil {
				t.Fatalf("list_files should fall back for absolute workspace path after owner mismatch: %v", err)
			}
			if !strings.Contains(listResult.DisplayText, "RuntimeManager.cpp") {
				t.Fatalf("expected list_files to include absolute workspace file, got %q", listResult.DisplayText)
			}

			grepTool := NewGrepTool(ws)
			grepResult, err := grepTool.ExecuteDetailed(context.Background(), map[string]any{
				"pattern":       "runtime_manager_marker",
				"path":          sourceDir,
				"owner_node_id": "plan-01",
			})
			if err != nil {
				t.Fatalf("grep should fall back for absolute workspace path after owner mismatch: %v", err)
			}
			if !strings.Contains(grepResult.DisplayText, "RuntimeManager.cpp") {
				t.Fatalf("expected grep to find absolute workspace file content, got %q", grepResult.DisplayText)
			}
		})
	}
}

func TestLookupToolsRecoverWhenOwnedWorktreeRouteIsMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("MkdirAll src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile main.go: %v", err)
	}
	worktreeRoot := filepath.Join(t.TempDir(), "specialist")
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll specialist worktree: %v", err)
	}

	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	ws.ResolveEditTarget = func(req EditRoutingRequest) (EditRoutingResult, error) {
		req = req.normalized()
		if req.lookupIntent() && strings.TrimSpace(req.OwnerNodeID) != "" {
			abs, err := ws.resolveAgainstRoot(worktreeRoot, req.Path)
			if err != nil {
				return EditRoutingResult{}, err
			}
			return EditRoutingResult{
				AbsolutePath: abs,
				DisplayRoot:  worktreeRoot,
				OwnerNodeID:  req.OwnerNodeID,
				Specialist:   "driver-build-fixer",
				WorktreeRoot: worktreeRoot,
			}, nil
		}
		return ws.resolveEditFallback(req)
	}

	readTool := NewReadFileTool(ws)
	readResult, err := readTool.ExecuteDetailed(context.Background(), map[string]any{
		"path":          "src/main.go",
		"owner_node_id": "plan-01",
	})
	if err != nil {
		t.Fatalf("read_file should fall back when owned worktree route is missing: %v", err)
	}
	if !strings.Contains(readResult.DisplayText, "package main") {
		t.Fatalf("expected read_file to return base workspace content, got %q", readResult.DisplayText)
	}

	listTool := NewListFilesTool(ws)
	listResult, err := listTool.ExecuteDetailed(context.Background(), map[string]any{
		"path":          "src",
		"owner_node_id": "plan-01",
	})
	if err != nil {
		t.Fatalf("list_files should fall back when owned worktree route is missing: %v", err)
	}
	if !strings.Contains(listResult.DisplayText, "src/main.go") {
		t.Fatalf("expected list_files to include base workspace file, got %q", listResult.DisplayText)
	}

	grepTool := NewGrepTool(ws)
	grepResult, err := grepTool.ExecuteDetailed(context.Background(), map[string]any{
		"pattern":       "package main",
		"path":          "src",
		"owner_node_id": "plan-01",
	})
	if err != nil {
		t.Fatalf("grep should fall back when owned worktree route is missing: %v", err)
	}
	if !strings.Contains(grepResult.DisplayText, "src/main.go") {
		t.Fatalf("expected grep to find base workspace content, got %q", grepResult.DisplayText)
	}
}

func TestLookupToolsRecoverWhenOwnedLookupReturnsMissingPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "sentinel", err: os.ErrNotExist},
		{name: "windows-text", err: fmt.Errorf("The system cannot find the path specified")},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			sourceDir := filepath.Join(root, "src")
			if err := os.MkdirAll(sourceDir, 0o755); err != nil {
				t.Fatalf("MkdirAll sourceDir: %v", err)
			}
			sourceFile := filepath.Join(sourceDir, "main.go")
			if err := os.WriteFile(sourceFile, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
				t.Fatalf("WriteFile sourceFile: %v", err)
			}

			ws := Workspace{
				BaseRoot: root,
				Root:     root,
			}
			ws.ResolveEditTarget = func(req EditRoutingRequest) (EditRoutingResult, error) {
				req = req.normalized()
				if req.lookupIntent() && strings.TrimSpace(req.OwnerNodeID) != "" {
					return EditRoutingResult{}, tc.err
				}
				return ws.resolveEditFallback(req)
			}

			readTool := NewReadFileTool(ws)
			readResult, err := readTool.ExecuteDetailed(context.Background(), map[string]any{
				"path":          "src/main.go",
				"owner_node_id": "plan-01",
			})
			if err != nil {
				t.Fatalf("read_file should fall back after owned lookup missing path: %v", err)
			}
			if !strings.Contains(readResult.DisplayText, "package main") {
				t.Fatalf("expected read_file to return main workspace file, got %q", readResult.DisplayText)
			}

			listTool := NewListFilesTool(ws)
			listResult, err := listTool.ExecuteDetailed(context.Background(), map[string]any{
				"path":          "src/main.go",
				"owner_node_id": "plan-01",
			})
			if err != nil {
				t.Fatalf("list_files should fall back after owned lookup missing path: %v", err)
			}
			if toolMetaString(listResult.Meta, "path_type") != "file" {
				t.Fatalf("expected list_files fallback to return file metadata, got %#v", listResult.Meta)
			}

			grepTool := NewGrepTool(ws)
			grepResult, err := grepTool.ExecuteDetailed(context.Background(), map[string]any{
				"pattern":       "package main",
				"path":          "src",
				"owner_node_id": "plan-01",
			})
			if err != nil {
				t.Fatalf("grep should fall back after owned lookup missing path: %v", err)
			}
			if !strings.Contains(grepResult.DisplayText, "src/main.go") {
				t.Fatalf("expected grep to find main workspace file, got %q", grepResult.DisplayText)
			}
		})
	}
}

func assertLookupToolsRecoverFromOwnerEditMismatch(t *testing.T, wrapSentinel bool) {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	ws.ResolveEditTarget = func(req EditRoutingRequest) (EditRoutingResult, error) {
		req = req.normalized()
		if req.lookupIntent() && strings.TrimSpace(req.OwnerNodeID) != "" {
			if !wrapSentinel {
				return EditRoutingResult{}, fmt.Errorf("edit target mismatch: path %s is outside editable ownership for specialist driver-build-fixer", req.Path)
			}
			return EditRoutingResult{}, fmt.Errorf("%w: path %s is outside editable ownership for specialist driver-build-fixer", ErrEditTargetMismatch, req.Path)
		}
		return ws.resolveEditFallback(req)
	}

	readTool := NewReadFileTool(ws)
	readResult, err := readTool.ExecuteDetailed(context.Background(), map[string]any{
		"path":          "main.go",
		"owner_node_id": "plan-01",
	})
	if err != nil {
		t.Fatalf("read_file should fall back to normal lookup after owner mismatch: %v", err)
	}
	if !strings.Contains(readResult.DisplayText, "package main") {
		t.Fatalf("expected read_file to return base workspace content, got %q", readResult.DisplayText)
	}

	listTool := NewListFilesTool(ws)
	listResult, err := listTool.ExecuteDetailed(context.Background(), map[string]any{
		"path":          ".",
		"owner_node_id": "plan-01",
	})
	if err != nil {
		t.Fatalf("list_files should fall back to normal lookup after owner mismatch: %v", err)
	}
	if !strings.Contains(listResult.DisplayText, "main.go") {
		t.Fatalf("expected list_files to include base workspace files, got %q", listResult.DisplayText)
	}

	grepTool := NewGrepTool(ws)
	grepResult, err := grepTool.ExecuteDetailed(context.Background(), map[string]any{
		"pattern":       "package main",
		"path":          ".",
		"owner_node_id": "plan-01",
	})
	if err != nil {
		t.Fatalf("grep should fall back to normal lookup after owner mismatch: %v", err)
	}
	if !strings.Contains(grepResult.DisplayText, "main.go") {
		t.Fatalf("expected grep to find base workspace content, got %q", grepResult.DisplayText)
	}
}

func TestWorkspaceResolveForLookupAcceptsWindowsSlashAbsolutePath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("slash-rooted lookup aliases are Windows-specific")
	}

	base := t.TempDir()
	target := filepath.Join(base, "guide.md")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ws := Workspace{
		BaseRoot: base,
		Root:     base,
	}

	volume := filepath.VolumeName(base)
	if volume == "" {
		t.Fatalf("expected volume for %q", base)
	}
	slashPath := strings.TrimPrefix(filepath.ToSlash(target), filepath.ToSlash(volume))
	resolved, err := ws.ResolveForLookup(slashPath)
	if err != nil {
		t.Fatalf("ResolveForLookup: %v", err)
	}
	if resolved != target {
		t.Fatalf("expected slash-rooted alias %q to resolve to %q, got %q", slashPath, target, resolved)
	}
}

func TestReadFileToolAcceptsWindowsSlashAbsolutePath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("slash-rooted lookup aliases are Windows-specific")
	}

	base := t.TempDir()
	target := filepath.Join(base, "guide.md")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	volume := filepath.VolumeName(base)
	if volume == "" {
		t.Fatalf("expected volume for %q", base)
	}
	slashPath := strings.TrimPrefix(filepath.ToSlash(target), filepath.ToSlash(volume))
	tool := NewReadFileTool(Workspace{
		BaseRoot: base,
		Root:     base,
	})

	out, err := tool.Execute(context.Background(), map[string]any{"path": slashPath})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected slash-rooted read_file lookup to return content, got %q", out)
	}
}

func TestReadFileToolPrefersCurrentDirectoryBeforeBaseRoot(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "shared.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("WriteFile base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(current, "shared.txt"), []byte("current\n"), 0o644); err != nil {
		t.Fatalf("WriteFile current: %v", err)
	}

	tool := NewReadFileTool(Workspace{
		BaseRoot: base,
		Root:     current,
	})

	out, err := tool.Execute(context.Background(), map[string]any{"path": "shared.txt"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "current") {
		t.Fatalf("expected current directory file contents, got %q", out)
	}
	if strings.Contains(out, "base") {
		t.Fatalf("expected current directory file to win over base root, got %q", out)
	}
}

func TestReadFileToolReturnsCachedRangeHintForUnchangedFile(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "cached.txt")
	if err := os.WriteFile(target, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewReadFileTool(Workspace{
		BaseRoot: base,
		Root:     base,
	})

	first, err := tool.Execute(context.Background(), map[string]any{
		"path":       "cached.txt",
		"start_line": 1,
		"end_line":   1,
	})
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if strings.Contains(first, "NOTE: returning cached content") {
		t.Fatalf("did not expect cache hint on first read, got %q", first)
	}

	second, err := tool.Execute(context.Background(), map[string]any{
		"path":       "cached.txt",
		"start_line": 1,
		"end_line":   1,
	})
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if !strings.Contains(second, "NOTE: returning cached content for an unchanged read_file range.") {
		t.Fatalf("expected cache hint on repeated identical read, got %q", second)
	}
	if !strings.Contains(second, "   1 | alpha") {
		t.Fatalf("expected cached line content, got %q", second)
	}
}

func TestReadFileToolReturnsOutOfRangeDiagnosticWithoutError(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "short.txt")
	if err := os.WriteFile(target, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewReadFileTool(Workspace{
		BaseRoot: base,
		Root:     base,
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path":       "short.txt",
		"start_line": 10,
		"end_line":   20,
	})
	if err != nil {
		t.Fatalf("expected out-of-range read to return diagnostic without error: %v", err)
	}
	if !strings.Contains(result.DisplayText, "read_file range is outside file") ||
		!strings.Contains(result.DisplayText, "File line count: 2") {
		t.Fatalf("expected range diagnostic with file line count, got %q", result.DisplayText)
	}
	if got := toolMetaString(result.Meta, "error_kind"); got != "range_out_of_bounds" {
		t.Fatalf("expected range_out_of_bounds metadata, got %#v", result.Meta)
	}
	if got := intValue(result.Meta, "file_line_count", 0); got != 2 {
		t.Fatalf("expected file_line_count=2 metadata, got %#v", result.Meta)
	}
}

func TestReadFileToolInvalidatesCacheAfterFileChange(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "cached.txt")
	if err := os.WriteFile(target, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewReadFileTool(Workspace{
		BaseRoot: base,
		Root:     base,
	})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":       "cached.txt",
		"start_line": 1,
		"end_line":   1,
	}); err != nil {
		t.Fatalf("first Execute: %v", err)
	}

	if err := os.WriteFile(target, []byte("updated\nbeta\n"), 0o644); err != nil {
		t.Fatalf("WriteFile update: %v", err)
	}

	out, err := tool.Execute(context.Background(), map[string]any{
		"path":       "cached.txt",
		"start_line": 1,
		"end_line":   1,
	})
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if strings.Contains(out, "NOTE: returning cached content") {
		t.Fatalf("did not expect stale cache hint after file change, got %q", out)
	}
	if !strings.Contains(out, "   1 | updated") {
		t.Fatalf("expected fresh file contents after cache invalidation, got %q", out)
	}
}

func TestReadFileToolReturnsCoveredSubrangeFromCachedRange(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "cached.txt")
	if err := os.WriteFile(target, []byte("alpha\nbeta\ngamma\ndelta\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewReadFileTool(Workspace{
		BaseRoot: base,
		Root:     base,
	})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":       "cached.txt",
		"start_line": 1,
		"end_line":   4,
	}); err != nil {
		t.Fatalf("seed Execute: %v", err)
	}

	out, err := tool.Execute(context.Background(), map[string]any{
		"path":       "cached.txt",
		"start_line": 2,
		"end_line":   3,
	})
	if err != nil {
		t.Fatalf("subset Execute: %v", err)
	}
	if !strings.Contains(out, "NOTE: returning content from a cached overlapping read_file range.") {
		t.Fatalf("expected overlapping cache hint, got %q", out)
	}
	if !strings.Contains(out, "   2 | beta") || !strings.Contains(out, "   3 | gamma") {
		t.Fatalf("expected covered subrange content, got %q", out)
	}
	if strings.Contains(out, "   1 | alpha") || strings.Contains(out, "   4 | delta") {
		t.Fatalf("expected only requested subrange lines, got %q", out)
	}
}

func TestReadFileToolReturnsPartialOverlapPlusFreshTail(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "cached.txt")
	if err := os.WriteFile(target, []byte("alpha\nbeta\ngamma\ndelta\nepsilon\nzeta\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewReadFileTool(Workspace{
		BaseRoot: base,
		Root:     base,
	})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":       "cached.txt",
		"start_line": 2,
		"end_line":   4,
	}); err != nil {
		t.Fatalf("seed Execute: %v", err)
	}

	out, err := tool.Execute(context.Background(), map[string]any{
		"path":       "cached.txt",
		"start_line": 3,
		"end_line":   6,
	})
	if err != nil {
		t.Fatalf("partial overlap Execute: %v", err)
	}
	if !strings.Contains(out, "NOTE: returning content assembled from a cached partial overlap plus newly read lines.") {
		t.Fatalf("expected partial overlap hint, got %q", out)
	}
	if !strings.Contains(out, "   3 | gamma") || !strings.Contains(out, "   4 | delta") {
		t.Fatalf("expected cached overlap lines, got %q", out)
	}
	if !strings.Contains(out, "   5 | epsilon") || !strings.Contains(out, "   6 | zeta") {
		t.Fatalf("expected freshly read tail lines, got %q", out)
	}
	if strings.Contains(out, "   2 | beta") {
		t.Fatalf("did not expect extra pre-overlap line, got %q", out)
	}
}

func TestGrepToolAnnotatesMatchesInsideCachedReadSpan(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "sample.cpp")
	if err := os.WriteFile(target, []byte("alpha\nbeta target\ngamma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hints := &ToolHints{}
	ws := Workspace{
		BaseRoot:  base,
		Root:      base,
		ToolHints: hints,
	}
	readTool := NewReadFileTool(ws)
	grepTool := NewGrepTool(ws)

	if _, err := readTool.Execute(context.Background(), map[string]any{
		"path":       "sample.cpp",
		"start_line": 2,
		"end_line":   2,
	}); err != nil {
		t.Fatalf("read Execute: %v", err)
	}

	out, err := grepTool.Execute(context.Background(), map[string]any{
		"pattern": "target",
		"path":    ".",
	})
	if err != nil {
		t.Fatalf("grep Execute: %v", err)
	}
	if !strings.Contains(out, "[cached-nearby:inside]") {
		t.Fatalf("expected cached-nearby inside hint, got %q", out)
	}
}

func TestGrepToolAnnotatesMatchesNearCachedReadSpan(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "sample.cpp")
	if err := os.WriteFile(target, []byte("one\ntwo\nthree\nfour\nfive target\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hints := &ToolHints{}
	ws := Workspace{
		BaseRoot:  base,
		Root:      base,
		ToolHints: hints,
	}
	readTool := NewReadFileTool(ws)
	grepTool := NewGrepTool(ws)

	if _, err := readTool.Execute(context.Background(), map[string]any{
		"path":       "sample.cpp",
		"start_line": 2,
		"end_line":   3,
	}); err != nil {
		t.Fatalf("read Execute: %v", err)
	}

	out, err := grepTool.Execute(context.Background(), map[string]any{
		"pattern": "target",
		"path":    ".",
	})
	if err != nil {
		t.Fatalf("grep Execute: %v", err)
	}
	if !strings.Contains(out, "[cached-nearby:2]") {
		t.Fatalf("expected cached-nearby distance hint, got %q", out)
	}
}

func TestGrepToolSkipsStaleCachedReadHintsAfterFileChange(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "sample.cpp")
	if err := os.WriteFile(target, []byte("alpha\nbeta target\ngamma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hints := &ToolHints{}
	ws := Workspace{
		BaseRoot:  base,
		Root:      base,
		ToolHints: hints,
	}
	readTool := NewReadFileTool(ws)
	grepTool := NewGrepTool(ws)

	if _, err := readTool.Execute(context.Background(), map[string]any{
		"path":       "sample.cpp",
		"start_line": 2,
		"end_line":   2,
	}); err != nil {
		t.Fatalf("read Execute: %v", err)
	}
	if err := os.WriteFile(target, []byte("alpha\nbeta target updated\ngamma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile update: %v", err)
	}

	out, err := grepTool.Execute(context.Background(), map[string]any{
		"pattern": "target",
		"path":    ".",
	})
	if err != nil {
		t.Fatalf("grep Execute: %v", err)
	}
	if strings.Contains(out, "[cached-nearby:") {
		t.Fatalf("did not expect stale cached-nearby hint, got %q", out)
	}
}

func TestToolRegistrySharesReadHintsBetweenReadFileAndGrep(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "sample.cpp")
	if err := os.WriteFile(target, []byte("alpha\nbeta target\ngamma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ws := Workspace{
		BaseRoot: base,
		Root:     base,
	}
	registry := NewToolRegistry(NewReadFileTool(ws), NewGrepTool(ws))

	if _, err := registry.Execute(context.Background(), "read_file", `{"path":"sample.cpp","start_line":2,"end_line":2}`); err != nil {
		t.Fatalf("read_file Execute: %v", err)
	}
	out, err := registry.Execute(context.Background(), "grep", `{"pattern":"target","path":"."}`)
	if err != nil {
		t.Fatalf("grep Execute: %v", err)
	}
	if !strings.Contains(out, "[cached-nearby:inside]") {
		t.Fatalf("expected registry-shared cached hint, got %q", out)
	}
}

func TestReadToolsUseWorkspaceConfiguredHintAndCacheLimits(t *testing.T) {
	base := t.TempDir()
	readTool := NewReadFileTool(Workspace{
		BaseRoot:         base,
		Root:             base,
		ReadHintSpans:    5,
		ReadCacheEntries: 3,
	})
	grepTool := NewGrepTool(Workspace{
		BaseRoot:      base,
		Root:          base,
		ReadHintSpans: 5,
	})
	_ = NewToolRegistry(readTool, grepTool)

	if readTool.maxCache != 3 {
		t.Fatalf("expected read cache size 3, got %d", readTool.maxCache)
	}
	if readTool.ws.ToolHints == nil {
		t.Fatalf("expected shared tool hints to be initialized")
	}
	if readTool.ws.ToolHints.maxReadSpans != 5 {
		t.Fatalf("expected shared read hint limit 5, got %d", readTool.ws.ToolHints.maxReadSpans)
	}
}

func TestListFilesToolFallsBackToBaseRootWhenRelativePathMissingInCurrentDirectory(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	targetDir := filepath.Join(base, "docs")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll current: %v", err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "guide.md"), []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewListFilesTool(Workspace{
		BaseRoot: base,
		Root:     current,
	})

	out, err := tool.Execute(context.Background(), map[string]any{"path": "docs"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "../docs/guide.md") {
		t.Fatalf("expected fallback listing to include root-level file, got %q", out)
	}
}

func TestListFilesToolReturnsFilePathWhenTargetIsFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Tavern.cpp")
	if err := os.WriteFile(target, []byte("int main() { return 0; }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewListFilesTool(Workspace{
		BaseRoot: root,
		Root:     root,
	})
	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path": "Tavern.cpp",
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if strings.TrimSpace(result.DisplayText) != "Tavern.cpp" {
		t.Fatalf("expected single file listing, got %q", result.DisplayText)
	}
	if toolMetaString(result.Meta, "path_type") != "file" {
		t.Fatalf("expected path_type=file metadata, got %#v", result.Meta)
	}
	if toolMetaInt(result.Meta, "entry_count") != 1 {
		t.Fatalf("expected entry_count=1, got %#v", result.Meta)
	}
}

func TestWriteFileToolUpdatesFallbackTargetInsteadOfCreatingSiblingFile(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(base, "config.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}

	tool := NewWriteFileTool(Workspace{
		BaseRoot: base,
		Root:     current,
	})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":    "config.txt",
		"content": "new",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("expected fallback target update, got %q", string(data))
	}
	if _, err := os.Stat(filepath.Join(current, "config.txt")); !os.IsNotExist(err) {
		t.Fatalf("did not expect a new file in the current directory, err=%v", err)
	}
}

func TestWriteFileToolCreatesNewFileInCurrentDirectoryWhenNoFallbackExists(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	tool := NewWriteFileTool(Workspace{
		BaseRoot: base,
		Root:     current,
	})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":    "new.txt",
		"content": "hello",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(current, "new.txt"))
	if err != nil {
		t.Fatalf("ReadFile current: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("expected current-directory create, got %q", string(data))
	}
}

func TestReplaceInFileToolUpdatesFallbackTarget(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(base, "sample.txt")
	if err := os.WriteFile(target, []byte("alpha beta"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}

	tool := NewReplaceInFileTool(Workspace{
		BaseRoot: base,
		Root:     current,
	})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":    "sample.txt",
		"search":  "beta",
		"replace": "gamma",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if string(data) != "alpha gamma" {
		t.Fatalf("expected fallback replace target update, got %q", string(data))
	}
}
