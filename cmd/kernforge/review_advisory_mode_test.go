package main

import "testing"

func TestReviewBlockingIsAdvisory(t *testing.T) {
	if !reviewBlockingIsAdvisory(Config{Review: ReviewHarnessConfig{Blocking: "advisory"}}) {
		t.Fatal("advisory should be advisory")
	}
	if !reviewBlockingIsAdvisory(Config{Review: ReviewHarnessConfig{Blocking: "  ADVISORY "}}) {
		t.Fatal("advisory detection should be case- and space-insensitive")
	}
	if reviewBlockingIsAdvisory(Config{Review: ReviewHarnessConfig{Blocking: "blocking"}}) {
		t.Fatal("blocking must not be advisory")
	}
	if reviewBlockingIsAdvisory(Config{Review: ReviewHarnessConfig{Blocking: ""}}) {
		t.Fatal("empty must default to blocking, not advisory")
	}
}

func TestNormalizeReviewBlockingDefault(t *testing.T) {
	c := ReviewHarnessConfig{}
	normalizeReviewHarnessConfig(&c)
	if c.Blocking != "blocking" {
		t.Fatalf("empty blocking should normalize to blocking, got %q", c.Blocking)
	}
	c2 := ReviewHarnessConfig{Blocking: "  Advisory "}
	normalizeReviewHarnessConfig(&c2)
	if c2.Blocking != "advisory" {
		t.Fatalf("Advisory should normalize to advisory, got %q", c2.Blocking)
	}
}

// TestAdvisoryReviewSurfacesBlockersAsWarnings locks the advisory escape hatch:
// with advisory review on, a would-be blocker is surfaced as a warning and the
// gate approves-with-warnings instead of blocking the write.
func TestAdvisoryReviewSurfacesBlockersAsWarnings(t *testing.T) {
	finding := ReviewFinding{
		ID:          "RF-1",
		Source:      "deterministic",
		Severity:    reviewSeverityBlocker,
		BlocksGate:  true,
		Confidence:  "high",
		Quality:     reviewFindingQualityComplete,
		Path:        "a.go",
		Title:       "blocking issue",
		Evidence:    "e",
		Impact:      "i",
		RequiredFix: "f",
	}
	mkRun := func(advisory bool) ReviewRun {
		return ReviewRun{
			Trigger:        "pre_write",
			AdvisoryReview: advisory,
			ChangeSet:      ReviewChangeSet{ChangedPaths: []string{"a.go"}},
			Findings:       []ReviewFinding{finding},
		}
	}

	if g := evaluateReviewGate(mkRun(false)); len(g.BlockingFindings) == 0 {
		t.Fatal("non-advisory review should still block")
	}

	g := evaluateReviewGate(mkRun(true))
	if len(g.BlockingFindings) != 0 {
		t.Fatalf("advisory review must not block, got blockers %v", g.BlockingFindings)
	}
	if g.Verdict != reviewVerdictApprovedWithWarnings {
		t.Fatalf("advisory review with a finding should approve-with-warnings, got %q", g.Verdict)
	}
	surfaced := false
	for _, id := range g.WarningFindings {
		if id == "RF-1" {
			surfaced = true
		}
	}
	if !surfaced {
		t.Fatal("advisory review should surface the would-be blocker as a warning")
	}
}
