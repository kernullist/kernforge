package main

import (
	"strings"
	"testing"
)

func TestSummarizeEditToolCompletionShowsChange(t *testing.T) {
	cfg := Config{AutoLocale: boolPtr(false)}
	meta := map[string]any{
		"changed_paths": []string{"auth.go"},
		"unified_diff":  "--- a/auth.go\n+++ b/auth.go\n@@ -1,2 +1,3 @@\n line\n+added1\n+added2\n-removed1\n",
	}
	got := summarizeEditToolCompletion(cfg, meta)
	if !strings.Contains(got, "auth.go") || !strings.Contains(got, "+2") || !strings.Contains(got, "-1") {
		t.Fatalf("edit completion must show file and +/- counts, got %q", got)
	}
}

func TestSummarizeEditToolCompletionEmptyWhenNoChange(t *testing.T) {
	if got := summarizeEditToolCompletion(Config{}, nil); got != "" {
		t.Fatalf("no changed paths must yield an empty summary, got %q", got)
	}
}

func TestSummarizeToolCompletionWithMetaRoutesEdit(t *testing.T) {
	cfg := Config{AutoLocale: boolPtr(false)}
	meta := map[string]any{"changed_paths": []string{"x.go"}, "unified_diff": "+++ b/x.go\n+a\n"}
	if got := summarizeToolCompletionWithMeta(cfg, ToolCall{Name: "replace_in_file"}, "updated x.go", meta); !strings.Contains(got, "x.go") {
		t.Fatalf("edit tool must route to the edit summary, got %q", got)
	}
	// A non-edit tool falls through to the existing output-based summarizer.
	got := summarizeToolCompletionWithMeta(cfg, ToolCall{Name: "read_file", Arguments: `{"path":"y.go"}`}, "l1\nl2\n", nil)
	if !strings.Contains(got, "read_file") {
		t.Fatalf("non-edit tool must use the existing summary, got %q", got)
	}
}

func TestCountUnifiedDiffLines(t *testing.T) {
	added, removed := countUnifiedDiffLines("--- a\n+++ b\n+one\n+two\n-three\n unchanged\n")
	if added != 2 || removed != 1 {
		t.Fatalf("expected +2 -1 (headers ignored), got +%d -%d", added, removed)
	}
}

// Content lines whose own text starts with "++"/"--" arrive as "+++.."/"---.."
// after the single +/- gutter (e.g. C++ "++i", a CLI "--flag", a SQL "-- note").
// They must be counted, not mistaken for the "+++ "/"--- " file headers.
func TestCountUnifiedDiffLinesContentStartingWithDoubleSign(t *testing.T) {
	diff := "diff --git a/x.c b/x.c\n--- a/x.c\n+++ b/x.c\n@@ -1,2 +1,2 @@\n ctx\n+++i\n---flag\n"
	added, removed := countUnifiedDiffLines(diff)
	if added != 1 || removed != 1 {
		t.Fatalf("content lines starting with ++/-- must count: expected +1 -1, got +%d -%d", added, removed)
	}
}
