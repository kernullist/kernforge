package main

import (
	"strings"
	"testing"
)

// Compact mode shows only the actionable essentials (id/severity/title + trimmed
// evidence + fix); impact/test stay in the report artifact.
func TestReviewCLIFindingCompactTrimsToEssentials(t *testing.T) {
	var b strings.Builder
	cfg := Config{AutoLocale: boolPtr(false)} // default progress = compact (not "stream")
	f := ReviewFinding{
		ID: "RF-001", Severity: "high", Title: "title",
		Evidence:           strings.Repeat("e", 300),
		Impact:             "IMPACTTEXT",
		RequiredFix:        "do the fix",
		TestRecommendation: "TESTTEXT",
	}
	renderReviewCLIFinding(&b, cfg, ReviewRun{}, f, "Fix")
	out := b.String()
	if strings.Contains(out, "IMPACTTEXT") || strings.Contains(out, "TESTTEXT") {
		t.Fatalf("compact finding must omit impact/test (kept in the report), got %q", out)
	}
	if !strings.Contains(out, "RF-001") || !strings.Contains(out, "do the fix") {
		t.Fatalf("compact finding must keep id and fix, got %q", out)
	}
}

// The verbose "stream" display keeps the complete finding (impact + test).
func TestReviewCLIFindingStreamKeepsFullDetail(t *testing.T) {
	var b strings.Builder
	cfg := Config{AutoLocale: boolPtr(false), ProgressDisplay: "stream"}
	f := ReviewFinding{ID: "RF-1", Severity: "high", Title: "t", Impact: "IMPACTTEXT", RequiredFix: "fix", TestRecommendation: "TESTTEXT"}
	renderReviewCLIFinding(&b, cfg, ReviewRun{}, f, "Fix")
	out := b.String()
	if !strings.Contains(out, "IMPACTTEXT") || !strings.Contains(out, "TESTTEXT") {
		t.Fatalf("stream finding must keep impact and test, got %q", out)
	}
}
