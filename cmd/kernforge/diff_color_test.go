package main

import (
	"strings"
	"testing"
)

func TestPaintDiffPreviewColorsLines(t *testing.T) {
	ui := UI{color: true}
	out := ui.paintDiffPreview("Preview for x.go\n--- before/x.go\n+++ after/x.go\n+  10 | added\n-  10 | removed\n    9 | ctx")
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("colored diff output expected, got %q", out)
	}
	// no-color mode is a no-op
	plain := UI{color: false}
	in := "Preview for x\n+ 1 | a\n- 1 | b"
	if got := plain.paintDiffPreview(in); got != in {
		t.Fatalf("no-color must be a no-op, got %q", got)
	}
}

func TestPaintDiffLineClassifies(t *testing.T) {
	ui := UI{color: true}
	add := ui.paintDiffLine("+  1 | x")
	rem := ui.paintDiffLine("-  1 | x")
	ctx := ui.paintDiffLine("    1 | x")
	if add == rem || add == ctx {
		t.Fatalf("add/remove/context must render differently: add=%q rem=%q ctx=%q", add, rem, ctx)
	}
	if ctx != "    1 | x" {
		t.Fatalf("a context line must stay unstyled, got %q", ctx)
	}
}

// The pre-write review consent preview embeds the main model's proposed diff; its
// +/- lines should be colored too (this is the diff a user most often sees).
func TestModelReviewConsentPreviewColorsDiff(t *testing.T) {
	ui := UI{color: true}
	req := ModelReviewConsentRequest{OriginalMainProposal: "Proposed diff:\n+  10 | added\n-  10 | removed"}
	got := formatModelReviewConsentOriginalProposal(ui, Config{AutoLocale: boolPtr(false)}, req)
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("consent preview diff lines should be colored, got %q", got)
	}
}
