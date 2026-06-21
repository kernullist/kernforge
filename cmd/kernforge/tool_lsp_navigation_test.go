package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// injectMockPool builds an LSPServerPool whose servers are in-process mocks, so
// the tool-level tests never depend on a real gopls/clangd.
func injectMockPool(t *testing.T, defURI string) *LSPServerPool {
	t.Helper()
	pool := NewLSPServerPool(LSPPoolConfig{RequestTimeout: time.Second})
	pool.startFn = func(spec lspLanguageSpec, binary string, args []string, root string) (*lspClient, error) {
		server := &mockLSPServer{handler: defaultMockHandler(defURI)}
		client, _ := newMockLSPClient(t, server, spec, root)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.initialize(ctx, root); err != nil {
			return nil, err
		}
		return client, nil
	}
	return pool
}

func TestLSPNavToolDefinitionSucceeds(t *testing.T) {
	root := t.TempDir()
	srcPath := filepath.Join(root, "main.go")
	writeTempFile(t, srcPath, "package main\n\nfunc Foo() error { return nil }\n")
	defURI := lspPathToURI(srcPath)

	pool := injectMockPool(t, defURI)
	defer pool.Close()

	ws := Workspace{BaseRoot: root, Root: root, LSP: pool}
	tool := NewLSPNavigationTool(ws)
	out, err := tool.Execute(context.Background(), map[string]any{
		"action": "definition",
		"path":   "main.go",
		"line":   3,
		"column": 6,
	})
	if err != nil {
		t.Fatalf("definition execute: %v", err)
	}
	if !strings.Contains(out, "main.go:10:6") {
		t.Fatalf("expected resolved definition location in output, got:\n%s", out)
	}
}

func TestLSPNavToolHoverSucceeds(t *testing.T) {
	root := t.TempDir()
	srcPath := filepath.Join(root, "main.go")
	writeTempFile(t, srcPath, "package main\n")
	defURI := lspPathToURI(srcPath)

	pool := injectMockPool(t, defURI)
	defer pool.Close()

	ws := Workspace{BaseRoot: root, Root: root, LSP: pool}
	tool := NewLSPNavigationTool(ws)
	out, err := tool.Execute(context.Background(), map[string]any{
		"action": "hover",
		"path":   "main.go",
		"line":   1,
		"column": 1,
	})
	if err != nil {
		t.Fatalf("hover execute: %v", err)
	}
	if !strings.Contains(out, "func Foo() error") {
		t.Fatalf("expected hover text in output, got:\n%s", out)
	}
}

func TestLSPNavToolDisabledDegradesGracefully(t *testing.T) {
	root := t.TempDir()
	writeTempFile(t, filepath.Join(root, "main.go"), "package main\n")

	// No pool attached -> LSP disabled. The tool must NOT error; it returns a
	// clear "not enabled" message so the model falls back to grep.
	ws := Workspace{BaseRoot: root, Root: root}
	tool := NewLSPNavigationTool(ws)
	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"action": "definition",
		"path":   "main.go",
		"line":   1,
		"column": 1,
	})
	if err != nil {
		t.Fatalf("expected graceful degrade, got error: %v", err)
	}
	if !strings.Contains(result.DisplayText, "not enabled") {
		t.Fatalf("expected 'not enabled' message, got:\n%s", result.DisplayText)
	}
	if available, _ := result.Meta["available"].(bool); available {
		t.Fatalf("expected available=false in meta, got %v", result.Meta["available"])
	}
}

func TestLSPNavToolRejectsPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	pool := injectMockPool(t, lspPathToURI(filepath.Join(root, "main.go")))
	defer pool.Close()

	ws := Workspace{BaseRoot: root, Root: root, LSP: pool}
	tool := NewLSPNavigationTool(ws)
	// An absolute path pointing outside the workspace root must be rejected.
	outside := filepath.Join(t.TempDir(), "secret.go")
	writeTempFile(t, outside, "package secret\n")
	_, err := tool.Execute(context.Background(), map[string]any{
		"action": "definition",
		"path":   outside,
		"line":   1,
		"column": 1,
	})
	if err == nil {
		t.Fatal("expected path-outside-root rejection")
	}
	// Either the lookup router or the final containment check may reject it; both
	// report an out-of-root condition.
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "outside the active workspace root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLSPNavToolUnmappedExtensionDegrades(t *testing.T) {
	root := t.TempDir()
	writeTempFile(t, filepath.Join(root, "notes.txt"), "hello\n")
	pool := injectMockPool(t, lspPathToURI(filepath.Join(root, "notes.txt")))
	defer pool.Close()

	ws := Workspace{BaseRoot: root, Root: root, LSP: pool}
	tool := NewLSPNavigationTool(ws)
	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"action": "hover",
		"path":   "notes.txt",
		"line":   1,
		"column": 1,
	})
	if err != nil {
		t.Fatalf("expected graceful degrade for unmapped extension, got: %v", err)
	}
	if !strings.Contains(result.DisplayText, "no language server is mapped") {
		t.Fatalf("expected unmapped-language message, got:\n%s", result.DisplayText)
	}
}

func TestLSPNavToolValidatesInput(t *testing.T) {
	root := t.TempDir()
	writeTempFile(t, filepath.Join(root, "main.go"), "package main\n")
	ws := Workspace{BaseRoot: root, Root: root}
	tool := NewLSPNavigationTool(ws)

	cases := []map[string]any{
		{"action": "bogus", "path": "main.go", "line": 1, "column": 1},
		{"action": "definition", "path": "", "line": 1, "column": 1},
		{"action": "definition", "path": "main.go", "line": 0, "column": 1},
		{"action": "definition", "path": "main.go", "line": 1, "column": 0},
	}
	for i, args := range cases {
		if _, err := tool.Execute(context.Background(), args); err == nil {
			t.Fatalf("case %d: expected validation error for %v", i, args)
		}
	}
}

func TestLSPNavToolReadOnly(t *testing.T) {
	tool := NewLSPNavigationTool(Workspace{})
	if !tool.ReadOnlyToolCall() {
		t.Fatal("lsp_nav must report read-only")
	}
	if def := tool.Definition(); def.Name != "lsp_nav" {
		t.Fatalf("unexpected tool name %q", def.Name)
	}
}
