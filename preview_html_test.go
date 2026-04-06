package main

import (
	"strings"
	"testing"
)

func TestRenderDiffPreviewHTMLContainsStructuredRowsAndActions(t *testing.T) {
	preview := EditPreview{
		Title: "Patch review",
		Preview: strings.Join([]string{
			"Preview for main.go",
			"--- before/main.go",
			"+++ after/main.go",
			"   8 | keep",
			"-  9 | old value",
			"+  9 | new value",
		}, "\n"),
	}

	html := renderDiffPreviewHTML(preview, "tok123")
	for _, needle := range []string{
		"Patch review",
		"Diff Review",
		"Apply Patch",
		"submitDecision('apply')",
		"/decision?token=tok123",
		"diff-row diff-add",
		"diff-row diff-remove",
		"old value",
		"new value",
	} {
		if !strings.Contains(html, needle) {
			t.Fatalf("expected html to contain %q, got:\n%s", needle, html)
		}
	}
}

func TestRenderDiffPreviewWebViewHTMLContainsInternalBindings(t *testing.T) {
	preview := EditPreview{
		Title: "Embedded review",
		Preview: strings.Join([]string{
			"Preview for main.go",
			"--- before/main.go",
			"+++ after/main.go",
			"-  9 | old value",
			"+  9 | new value",
		}, "\n"),
	}

	html := renderDiffPreviewWebViewHTML(preview)
	for _, needle := range []string{
		"Embedded review",
		"resolveDecisionBridge",
		"kfDecision bridge is unavailable",
		"submitDecision('apply')",
		"Diff Review",
	} {
		if !strings.Contains(html, needle) {
			t.Fatalf("expected embedded html to contain %q, got:\n%s", needle, html)
		}
	}
}

func TestRenderReadOnlyDiffWebViewHTMLContainsCloseBinding(t *testing.T) {
	html := renderReadOnlyDiffWebViewHTML("Workspace Diff", "repo root", strings.Join([]string{
		"diff --git a/main.go b/main.go",
		"--- a/main.go",
		"+++ b/main.go",
		"@@ -1,1 +1,1 @@",
		"-func oldValue() string { return \"a\" }",
		"+func newValue() string { return \"b\" }",
	}, "\n"))

	for _, needle := range []string{
		"Workspace Diff",
		"Read-only diff view",
		"Close the window when you are done reviewing.",
		"Use the window close button to dismiss this view",
		"file-nav",
		"file-nav-item",
		"href=\"#diff-main-go\"",
		"file-card",
		"gh-hunk-header",
		"diff-mode-switch",
		"data-mode=\"split\"",
		"split-row",
		"word-add",
		"word-remove",
		"tok-keyword",
		"tok-string",
		"main.go",
	} {
		if !strings.Contains(html, needle) {
			t.Fatalf("expected read-only html to contain %q, got:\n%s", needle, html)
		}
	}
	if strings.Contains(html, `onclick="closeViewer()"`) {
		t.Fatalf("expected read-only diff viewer to remove the close action button, got:\n%s", html)
	}
	if strings.Contains(html, "Escape") {
		t.Fatalf("expected read-only diff viewer to stop advertising Escape close, got:\n%s", html)
	}
}

func TestHighlightDiffPairWrapsChangedSegments(t *testing.T) {
	oldHTML, newHTML := highlightDiffPair("prefixOldSuffix", "prefixNewSuffix", "")
	if !strings.Contains(oldHTML, "word-remove") || !strings.Contains(newHTML, "word-add") {
		t.Fatalf("expected intraline highlight spans, got old=%q new=%q", oldHTML, newHTML)
	}
	if !strings.Contains(oldHTML, "prefix") || !strings.Contains(newHTML, "Suffix") {
		t.Fatalf("expected unchanged context to remain visible, got old=%q new=%q", oldHTML, newHTML)
	}
}

func TestRenderSyntaxHighlightedHTMLHighlightsGoTokens(t *testing.T) {
	html := renderSyntaxHighlightedHTML(`func main() { return "x" // note }`, "go")
	for _, needle := range []string{"tok-keyword", "tok-string", "tok-comment"} {
		if !strings.Contains(html, needle) {
			t.Fatalf("expected syntax html to contain %q, got %q", needle, html)
		}
	}
}

func TestParseUnifiedDiffBuildsFileAndHunkStructure(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/main.go b/main.go",
		"--- a/main.go",
		"+++ b/main.go",
		"@@ -1,2 +1,2 @@",
		"-old",
		"+new",
		" keep",
	}, "\n")

	files := parseUnifiedDiff(diff)
	if len(files) != 1 {
		t.Fatalf("expected one file, got %#v", files)
	}
	if files[0].ID != "diff-main-go" || files[0].NewPath != "main.go" || files[0].Adds != 1 || files[0].Removes != 1 {
		t.Fatalf("unexpected file parse: %#v", files[0])
	}
	if len(files[0].Hunks) != 1 || len(files[0].Hunks[0].Lines) != 3 {
		t.Fatalf("unexpected hunk parse: %#v", files[0].Hunks)
	}
}

func TestBuildDiffAnchorIDSanitizesPath(t *testing.T) {
	got := buildDiffAnchorID(`Source/My Module/main.go`)
	if got != "diff-source-my-module-main-go" {
		t.Fatalf("unexpected anchor id: %q", got)
	}
}

func TestBuildSplitDiffRowsPairsRemovesAndAdds(t *testing.T) {
	rows := buildSplitDiffRows([]unifiedDiffLine{
		{Kind: "remove", OldNo: 10, Text: "old a"},
		{Kind: "remove", OldNo: 11, Text: "old b"},
		{Kind: "add", NewNo: 10, Text: "new a"},
		{Kind: "context", OldNo: 12, NewNo: 11, Text: "keep"},
	})

	if len(rows) != 3 {
		t.Fatalf("expected three split rows, got %#v", rows)
	}
	if rows[0][0] == nil || rows[0][1] == nil || rows[0][0].Text != "old a" || rows[0][1].Text != "new a" {
		t.Fatalf("unexpected first paired row: %#v", rows[0])
	}
	if rows[1][0] == nil || rows[1][1] != nil || rows[1][0].Text != "old b" {
		t.Fatalf("unexpected second row: %#v", rows[1])
	}
	if rows[2][0] == nil || rows[2][1] == nil || rows[2][0].Text != "keep" || rows[2][1].Text != "keep" {
		t.Fatalf("unexpected context row: %#v", rows[2])
	}
}

func TestClassifyDiffPreviewLineRecognizesPreviewShapes(t *testing.T) {
	cases := []struct {
		line      string
		wantClass string
		wantNo    string
	}{
		{"Preview for main.go", "diff-title", ""},
		{"--- before/main.go", "diff-meta", ""},
		{"+++ after/main.go", "diff-meta", ""},
		{"-  12 | old", "diff-remove", "12"},
		{"+  12 | new", "diff-add", "12"},
		{"   7 | keep", "diff-context", "7"},
	}

	for _, tc := range cases {
		got := classifyDiffPreviewLine(tc.line)
		if got.Class != tc.wantClass || got.LineNo != tc.wantNo {
			t.Fatalf("classifyDiffPreviewLine(%q) = %+v, want class=%q line=%q", tc.line, got, tc.wantClass, tc.wantNo)
		}
	}
}
