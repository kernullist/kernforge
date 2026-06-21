package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMCPWorkspaceHintAcceptsIDEActiveSelection verifies that an IDE-supplied
// clientContext carrying a directory-bearing workspace field nested under
// activeSelection still resolves a workspace. This is the VS Code extension
// scaffold shape: the client sends its active selection and a workspace root in
// the same client-context envelope.
func TestMCPWorkspaceHintAcceptsIDEActiveSelection(t *testing.T) {
	dir := t.TempDir()
	uri := testMCPFileURI(dir)

	hint, source := mcpWorkspaceHintFromMessage(map[string]any{
		"method": "tools/call",
		"params": map[string]any{
			"name": "kernforge_status",
			"clientContext": map[string]any{
				"activeSelection": map[string]any{
					"workspaceRoot": uri,
					"range": map[string]any{
						"start": map[string]any{"line": float64(1), "character": float64(0)},
						"end":   map[string]any{"line": float64(3), "character": float64(5)},
					},
				},
			},
		},
	})
	if hint == "" {
		t.Fatalf("expected workspace hint from nested activeSelection, source=%s", source)
	}
	resolved, err := resolveMCPWorkspacePath(hint)
	if err != nil {
		t.Fatalf("resolve hint: %v", err)
	}
	if !samePath(resolved, filepath.Clean(dir)) {
		t.Fatalf("expected hint to resolve to %q, got %q", dir, resolved)
	}
}

// TestMCPWorkspaceHintIgnoresSelectionFileURI verifies that a bare file uri in an
// active selection does NOT become a workspace hint. A selection points at a
// file, not a directory, so it must fail the directory check and be ignored
// rather than poisoning workspace resolution.
func TestMCPWorkspaceHintIgnoresSelectionFileURI(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "selected.go")
	if err := os.WriteFile(filePath, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write selection file: %v", err)
	}
	fileURI := testMCPFileURI(filePath)

	hint, source := mcpWorkspaceHintFromMessage(map[string]any{
		"method": "tools/call",
		"params": map[string]any{
			"name": "kernforge_status",
			"clientContext": map[string]any{
				"activeSelection": map[string]any{
					"uri": fileURI,
					"range": map[string]any{
						"start": map[string]any{"line": float64(0), "character": float64(0)},
						"end":   map[string]any{"line": float64(0), "character": float64(4)},
					},
				},
			},
		},
	})
	if hint != "" {
		t.Fatalf("expected a selection file uri to be ignored, got hint=%q source=%q", hint, source)
	}
}

// TestMCPWorkspaceHintAbsentIDEFieldsUnchanged verifies that when no IDE hint
// fields are present, an existing rootUri-based initialize still resolves exactly
// as before. The additive recursion must not change existing resolution.
func TestMCPWorkspaceHintAbsentIDEFieldsUnchanged(t *testing.T) {
	dir := t.TempDir()
	uri := testMCPFileURI(dir)

	hint, source := mcpWorkspaceHintFromMessage(map[string]any{
		"method": "initialize",
		"params": map[string]any{
			"rootUri": uri,
		},
	})
	if hint == "" {
		t.Fatalf("expected rootUri hint to still resolve, source=%s", source)
	}
	if source != "initialize.params.rootUri" {
		t.Fatalf("expected existing rootUri source, got %q", source)
	}

	// A message with no params and no hint must resolve to nothing.
	if h, _ := mcpWorkspaceHintFromMessage(map[string]any{"method": "ping"}); h != "" {
		t.Fatalf("expected no hint for hintless ping, got %q", h)
	}
}
