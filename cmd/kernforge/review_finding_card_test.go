package main

import (
	"strings"
	"testing"
)

// TestReviewFindingCardShowsBlockerWhenGateBlocking locks Fix C: when a finding
// blocks the gate (e.g. a pre-write warning promoted to a blocker), its card
// label reads as a blocker so the per-item label agrees with the "blockers N"
// count in the summary header, instead of showing its raw lower severity.
func TestReviewFindingCardShowsBlockerWhenGateBlocking(t *testing.T) {
	blockLabel := humanizeReviewSeverity(reviewSeverityBlocker, true)
	medLabel := humanizeReviewSeverity(reviewSeverityMedium, true)
	if blockLabel == medLabel {
		t.Fatalf("test precondition: blocker and medium labels must differ (%q vs %q)", blockLabel, medLabel)
	}
	finding := ReviewFinding{ID: "RF-001", Severity: reviewSeverityMedium, Category: "security", Title: "described inconsistency"}

	var blocking strings.Builder
	writeReviewFindingCard(&blocking, finding, true, false, true)
	if !strings.Contains(blocking.String(), "["+blockLabel+"·") {
		t.Fatalf("gate-blocking card should show the blocker label %q, got:\n%s", blockLabel, blocking.String())
	}
	if strings.Contains(blocking.String(), "["+medLabel+"·") {
		t.Fatalf("gate-blocking card must not show the raw %q label, got:\n%s", medLabel, blocking.String())
	}

	var plain strings.Builder
	writeReviewFindingCard(&plain, finding, true, false, false)
	if !strings.Contains(plain.String(), "["+medLabel+"·") {
		t.Fatalf("a non-blocking card should keep the raw severity label %q, got:\n%s", medLabel, plain.String())
	}
}
