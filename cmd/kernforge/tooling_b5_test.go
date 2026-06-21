package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

// encodeUTF16LEWithBOM produces UTF-16 little-endian bytes with a leading BOM,
// matching the on-disk form Windows tools commonly emit for CJK text files.
func encodeUTF16LEWithBOM(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := []byte{0xFF, 0xFE}
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func encodeUTF16BEWithBOM(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := []byte{0xFE, 0xFF}
	for _, u := range units {
		out = append(out, byte(u>>8), byte(u))
	}
	return out
}

// tooling-4: a UTF-16LE file with a BOM (and CJK content) must read as text
// instead of being rejected as binary, because isText's raw NUL check would
// otherwise misclassify the interleaved NUL bytes.
func TestReadFileToolDecodesUTF16LEWithBOM(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "kr.txt")
	content := "first line\n\xed\x95\x9c\xea\xb8\x80 line\nlast line\n" // includes Korean text
	if err := os.WriteFile(target, encodeUTF16LEWithBOM(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewReadFileTool(Workspace{BaseRoot: base, Root: base})
	out, err := tool.Execute(context.Background(), map[string]any{"path": "kr.txt"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out, "refusing to read binary file") {
		t.Fatalf("UTF-16LE file was rejected as binary: %q", out)
	}
	if !strings.Contains(out, "first line") || !strings.Contains(out, "last line") {
		t.Fatalf("expected decoded UTF-16 lines, got %q", out)
	}
	if !strings.Contains(out, "\xed\x95\x9c\xea\xb8\x80") {
		t.Fatalf("expected Korean text decoded to UTF-8, got %q", out)
	}
}

func TestReadFileToolDecodesUTF16BEWithBOM(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "be.txt")
	if err := os.WriteFile(target, encodeUTF16BEWithBOM("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewReadFileTool(Workspace{BaseRoot: base, Root: base})
	out, err := tool.Execute(context.Background(), map[string]any{"path": "be.txt"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("expected decoded UTF-16BE lines, got %q", out)
	}
}

// A genuine binary file (NUL bytes that are not UTF-16 text) must still be
// rejected so the decoder does not relax the binary guard.
func TestReadFileToolStillRejectsRealBinary(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "blob.bin")
	blob := []byte{0x00, 0x01, 0x02, 0xFF, 0x00, 0x7A, 0x00, 0x13, 0x37, 0x00, 0x00, 0xAB}
	if err := os.WriteFile(target, blob, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewReadFileTool(Workspace{BaseRoot: base, Root: base})
	_, err := tool.Execute(context.Background(), map[string]any{"path": "blob.bin"})
	if err == nil {
		t.Fatalf("expected binary file to be rejected")
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Fatalf("expected binary rejection error, got %v", err)
	}
}

func TestDecodeUTF16TextRejectsPlainUTF8(t *testing.T) {
	if _, ok := decodeUTF16Text([]byte("plain ascii and \xed\x95\x9c utf8")); ok {
		t.Fatalf("plain UTF-8 should not be treated as UTF-16")
	}
}

// tooling-6: the grep schema must expose the new search controls and
// buildRipgrepArgs must translate them into the matching ripgrep flags while
// leaving the defaults identical when the params are omitted.
func TestGrepSchemaExposesSearchControls(t *testing.T) {
	def := NewGrepTool(Workspace{}).Definition()
	props, ok := def.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected object properties, got %#v", def.InputSchema)
	}
	for _, key := range []string{"ignore_case", "before_context", "after_context", "context", "fixed_string", "multiline", "type"} {
		if _, exists := props[key]; !exists {
			t.Fatalf("grep schema missing %q parameter", key)
		}
	}
}

func TestBuildRipgrepArgsDefaultsUnchanged(t *testing.T) {
	args, err := buildRipgrepArgs(grepSearchRequest{Root: ".", Pattern: "needle", MaxResults: 10})
	if err != nil {
		t.Fatalf("buildRipgrepArgs: %v", err)
	}
	for _, flag := range []string{"-i", "-F", "-U", "-A", "-B", "-t"} {
		if slices.Contains(args, flag) {
			t.Fatalf("default args unexpectedly contain %q: %#v", flag, args)
		}
	}
	sep := slices.Index(args, "--")
	if sep < 0 || sep+1 >= len(args) || args[sep+1] != "needle" {
		t.Fatalf("expected pattern after -- separator, got %#v", args)
	}
}

func TestBuildRipgrepArgsTranslatesSearchControls(t *testing.T) {
	args, err := buildRipgrepArgs(grepSearchRequest{
		Root:          ".",
		Pattern:       "needle",
		MaxResults:    10,
		IgnoreCase:    true,
		FixedString:   true,
		Multiline:     true,
		BeforeContext: 2,
		AfterContext:  3,
		FileType:      "go",
	})
	if err != nil {
		t.Fatalf("buildRipgrepArgs: %v", err)
	}
	sep := slices.Index(args, "--")
	if sep < 0 {
		t.Fatalf("missing -- separator: %#v", args)
	}
	pre := args[:sep]
	if !slices.Contains(pre, "-i") {
		t.Fatalf("expected -i, got %#v", args)
	}
	if !slices.Contains(pre, "-F") {
		t.Fatalf("expected -F, got %#v", args)
	}
	if !slices.Contains(pre, "-U") || !slices.Contains(pre, "--multiline-dotall") {
		t.Fatalf("expected multiline flags, got %#v", args)
	}
	if idx := slices.Index(pre, "-B"); idx < 0 || pre[idx+1] != "2" {
		t.Fatalf("expected -B 2, got %#v", args)
	}
	if idx := slices.Index(pre, "-A"); idx < 0 || pre[idx+1] != "3" {
		t.Fatalf("expected -A 3, got %#v", args)
	}
	if idx := slices.Index(pre, "-t"); idx < 0 || pre[idx+1] != "go" {
		t.Fatalf("expected -t go, got %#v", args)
	}
}

func TestBuildRipgrepArgsRejectsUnsafeFileType(t *testing.T) {
	if _, err := buildRipgrepArgs(grepSearchRequest{
		Root:       ".",
		Pattern:    "needle",
		MaxResults: 10,
		FileType:   "go --search-zip",
	}); err == nil {
		t.Fatalf("expected unsafe file type to be rejected")
	}
}

func TestGrepExecuteDetailedMapsContextOption(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "sample.txt")
	if err := os.WriteFile(target, []byte("a\nb\nNEEDLE\nc\nd\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var captured grepSearchRequest
	tool := &GrepTool{
		ws: Workspace{BaseRoot: base, Root: base},
		ripgrepSearch: func(_ context.Context, req grepSearchRequest) (grepSearchResult, error) {
			captured = req
			return grepSearchResult{
				Matches: []grepSearchMatch{
					{Path: target, LineNo: 2, Line: "b\n", Context: true},
					{Path: target, LineNo: 3, Line: "NEEDLE\n"},
					{Path: target, LineNo: 4, Line: "c\n", Context: true},
				},
			}, nil
		},
	}

	res, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"pattern": "NEEDLE",
		"path":    ".",
		"context": 1,
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if captured.BeforeContext != 1 || captured.AfterContext != 1 {
		t.Fatalf("expected context=1 to set before/after to 1, got %+v", captured)
	}
	// match_count must count only true matches, not the surrounding context.
	if got := res.Meta["match_count"]; got != 1 {
		t.Fatalf("expected match_count 1, got %v", got)
	}
	if !strings.Contains(res.DisplayText, "NEEDLE") {
		t.Fatalf("expected match line in output, got %q", res.DisplayText)
	}
	if !strings.Contains(res.DisplayText, "-2- b") {
		t.Fatalf("expected context line rendered with - separator, got %q", res.DisplayText)
	}
}

// tooling-7: view_image must advertise itself as a read-only tool call so it
// routes through the read permission path instead of looking like a mutation.
func TestViewImageToolIsReadOnly(t *testing.T) {
	registry := NewToolRegistry(NewViewImageTool(Workspace{}))
	if !registry.ToolCallReadOnly("view_image") {
		t.Fatalf("expected view_image to be treated as a read-only tool call")
	}
}

// tooling-2: notebook_edit must read a cell, replace its source preserving the
// notebook structure, and round-trip as valid JSON.
func TestNotebookEditReadAndReplace(t *testing.T) {
	base := t.TempDir()
	nbPath := filepath.Join(base, "demo.ipynb")
	original := `{
 "cells": [
  {"cell_type": "markdown", "metadata": {}, "source": ["# Title\n"]},
  {"cell_type": "code", "execution_count": 1, "metadata": {}, "outputs": [], "source": ["print('old')\n"]}
 ],
 "metadata": {"kernelspec": {"name": "python3"}},
 "nbformat": 4,
 "nbformat_minor": 5
}`
	if err := os.WriteFile(nbPath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewNotebookEditTool(Workspace{BaseRoot: base, Root: base})

	readOut, err := tool.Execute(context.Background(), map[string]any{
		"path":       "demo.ipynb",
		"operation":  "read",
		"cell_index": 1,
	})
	if err != nil {
		t.Fatalf("read Execute: %v", err)
	}
	if !strings.Contains(readOut, "print('old')") {
		t.Fatalf("expected to read code cell source, got %q", readOut)
	}

	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":       "demo.ipynb",
		"operation":  "replace",
		"cell_index": 1,
		"source":     "print('new')\nprint('done')\n",
	}); err != nil {
		t.Fatalf("replace Execute: %v", err)
	}

	data, err := os.ReadFile(nbPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("rewritten notebook is not valid JSON: %v", err)
	}
	// Structure preserved: metadata and nbformat fields survive.
	if _, ok := parsed["metadata"]; !ok {
		t.Fatalf("expected metadata to be preserved: %v", parsed)
	}
	if parsed["nbformat"] != float64(4) {
		t.Fatalf("expected nbformat preserved, got %v", parsed["nbformat"])
	}
	cells, ok := parsed["cells"].([]any)
	if !ok || len(cells) != 2 {
		t.Fatalf("expected 2 cells, got %#v", parsed["cells"])
	}
	cell1 := cells[1].(map[string]any)
	srcLines, ok := cell1["source"].([]any)
	if !ok || len(srcLines) != 2 {
		t.Fatalf("expected 2 source lines, got %#v", cell1["source"])
	}
	if srcLines[0] != "print('new')\n" || srcLines[1] != "print('done')\n" {
		t.Fatalf("unexpected replaced source: %#v", srcLines)
	}
}

func TestNotebookEditInsertAndDelete(t *testing.T) {
	base := t.TempDir()
	nbPath := filepath.Join(base, "ops.ipynb")
	original := `{
 "cells": [
  {"cell_type": "code", "execution_count": null, "metadata": {}, "outputs": [], "source": ["a = 1\n"]}
 ],
 "metadata": {},
 "nbformat": 4,
 "nbformat_minor": 5
}`
	if err := os.WriteFile(nbPath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewNotebookEditTool(Workspace{BaseRoot: base, Root: base})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":       "ops.ipynb",
		"operation":  "insert",
		"cell_index": 0,
		"cell_type":  "markdown",
		"source":     "# Heading\n",
	}); err != nil {
		t.Fatalf("insert Execute: %v", err)
	}

	data, _ := os.ReadFile(nbPath)
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("after insert, invalid JSON: %v", err)
	}
	cells := parsed["cells"].([]any)
	if len(cells) != 2 {
		t.Fatalf("expected 2 cells after insert, got %d", len(cells))
	}
	if cells[0].(map[string]any)["cell_type"] != "markdown" {
		t.Fatalf("expected inserted markdown cell at index 0, got %#v", cells[0])
	}

	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":       "ops.ipynb",
		"operation":  "delete",
		"cell_index": 0,
	}); err != nil {
		t.Fatalf("delete Execute: %v", err)
	}
	data, _ = os.ReadFile(nbPath)
	parsed = map[string]any{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("after delete, invalid JSON: %v", err)
	}
	cells = parsed["cells"].([]any)
	if len(cells) != 1 {
		t.Fatalf("expected 1 cell after delete, got %d", len(cells))
	}
	if cells[0].(map[string]any)["cell_type"] != "code" {
		t.Fatalf("expected remaining code cell, got %#v", cells[0])
	}
}

func TestNotebookEditRejectsOutOfRange(t *testing.T) {
	base := t.TempDir()
	nbPath := filepath.Join(base, "small.ipynb")
	if err := os.WriteFile(nbPath, []byte(`{"cells": [], "metadata": {}, "nbformat": 4, "nbformat_minor": 5}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewNotebookEditTool(Workspace{BaseRoot: base, Root: base})
	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":       "small.ipynb",
		"operation":  "replace",
		"cell_index": 0,
		"source":     "x\n",
	}); err == nil {
		t.Fatalf("expected out-of-range replace to fail")
	}
}
