package main

import (
	"context"
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
