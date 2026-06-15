package main

import (
	"strings"
	"testing"
)

// TestPreWriteReviewBlockedRetryGuidanceDemandsConsistentReferences locks in the
// convergence-nudge fix: a real failure mode is the main model changing a
// signature/constant but leaving call sites stale, so the review keeps blocking
// with a different finding each round. The retry guidance must explicitly require
// updating every reference in the same patch, and escalate once the same edit has
// been blocked repeatedly.
func TestPreWriteReviewBlockedRetryGuidanceDemandsConsistentReferences(t *testing.T) {
	cfg := Config{AutoLocale: boolPtr(false)}

	first := formatPreWriteReviewBlockedRetryGuidance(cfg, nil, 1)
	for _, want := range []string{
		"complete standalone apply_patch",
		"grep for each name",
		"call sites",
		"blocked again by the next review",
	} {
		if !strings.Contains(first, want) {
			t.Fatalf("round-1 guidance missing %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, "Repeated-block warning") {
		t.Fatalf("round-1 guidance must not escalate yet:\n%s", first)
	}

	repeated := formatPreWriteReviewBlockedRetryGuidance(cfg, nil, 2)
	if !strings.Contains(repeated, "Repeated-block warning") {
		t.Fatalf("round-2 guidance must escalate to the repeated-block warning:\n%s", repeated)
	}
	if !strings.Contains(repeated, "single apply_patch") {
		t.Fatalf("round-2 guidance should suggest one consistent rewrite:\n%s", repeated)
	}

	// Korean locale carries the same reference-consistency rule and escalation.
	ko := formatPreWriteReviewBlockedRetryGuidance(Config{AutoLocale: boolPtr(true)}, nil, 2)
	if !strings.Contains(ko, "호출부") || !strings.Contains(ko, "grep") {
		t.Fatalf("korean guidance must demand call-site consistency via grep:\n%s", ko)
	}
	if !strings.Contains(ko, "반복 차단 경고") {
		t.Fatalf("korean round-2 guidance must escalate:\n%s", ko)
	}
}
