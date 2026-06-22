package main

import "testing"

func TestReviewModelFindingMeetsBlockingFloor(t *testing.T) {
	complete := func(conf, qual string) ReviewFinding {
		return ReviewFinding{
			Source: "model", Confidence: conf, Quality: qual,
			Path: "a.go", Evidence: "e", Impact: "i", RequiredFix: "f", Title: "t",
		}
	}
	if !reviewModelFindingMeetsBlockingFloor(complete("medium", reviewFindingQualityComplete)) {
		t.Fatal("medium-confidence complete finding should meet the floor")
	}
	if !reviewModelFindingMeetsBlockingFloor(complete("high", reviewFindingQualityComplete)) {
		t.Fatal("high-confidence complete finding should meet the floor")
	}
	if reviewModelFindingMeetsBlockingFloor(complete("low", reviewFindingQualityComplete)) {
		t.Fatal("low-confidence finding must NOT meet the floor")
	}
	if reviewModelFindingMeetsBlockingFloor(complete("medium", reviewFindingQualityWeak)) {
		t.Fatal("weak-quality finding must NOT meet the floor")
	}
}

func trustFloorSecurityFinding(confidence string) ReviewFinding {
	return ReviewFinding{
		ID:           "RF-001",
		Source:       "model",
		ReviewerRole: "primary_reviewer",
		Severity:     reviewSeverityHigh,
		Category:     "security",
		Confidence:   confidence,
		Quality:      reviewFindingQualityComplete,
		Path:         "driver.c",
		Symbol:       "Handler",
		Title:        "possible bounds issue",
		Evidence:     "len used without an upper bound",
		Impact:       "out-of-bounds access",
		RequiredFix:  "clamp len to the buffer capacity",
	}
}

// TestTrustFloorLowConfidenceModelFindingDoesNotBlock locks the trust floor: a
// low-confidence model finding surfaces as a warning instead of hard-blocking a
// write, while the same finding at medium confidence still blocks.
func TestTrustFloorLowConfidenceModelFindingDoesNotBlock(t *testing.T) {
	low := ReviewRun{
		Trigger:   "pre_write",
		ChangeSet: ReviewChangeSet{ChangedPaths: []string{"driver.c"}},
		Findings:  []ReviewFinding{trustFloorSecurityFinding("low")},
	}
	if gate := evaluateReviewGate(low); len(gate.BlockingFindings) != 0 {
		t.Fatalf("low-confidence model finding must not hard-block, got blockers %v", gate.BlockingFindings)
	}

	mid := ReviewRun{
		Trigger:   "pre_write",
		ChangeSet: ReviewChangeSet{ChangedPaths: []string{"driver.c"}},
		Findings:  []ReviewFinding{trustFloorSecurityFinding("medium")},
	}
	if gate := evaluateReviewGate(mid); len(gate.BlockingFindings) == 0 {
		t.Fatalf("a confident complete security finding should still block")
	}
}

// TestTrustFloorDoesNotApplyToDeterministicFindings ensures KernForge's own
// deterministic checks still block regardless of confidence.
func TestTrustFloorDoesNotApplyToDeterministicFindings(t *testing.T) {
	det := ReviewFinding{
		ID:          "RF-D",
		Source:      "deterministic",
		Severity:    reviewSeverityBlocker,
		BlocksGate:  true,
		Confidence:  "low",
		Quality:     reviewFindingQualityComplete,
		Title:       "Verification is required but missing",
		Evidence:    "no verification recorded",
		Impact:      "regression risk unknown",
		RequiredFix: "run verification",
	}
	run := ReviewRun{
		Trigger:   "pre_write",
		ChangeSet: ReviewChangeSet{ChangedPaths: []string{"driver.c"}},
		Findings:  []ReviewFinding{det},
	}
	if gate := evaluateReviewGate(run); len(gate.BlockingFindings) == 0 {
		t.Fatal("a deterministic low-confidence blocker must still block")
	}
}
