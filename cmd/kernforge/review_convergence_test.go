package main

import (
	"path/filepath"
	"testing"
)

// Regression (GG-1): a real verification FAILURE is a repair obligation, not a
// missing-evidence gap, so the gate verdict must be needs_revision (routing to
// repair_required), not insufficient_evidence + user_decision.
func TestGateVerificationFailureRoutesToRepair(t *testing.T) {
	run := ReviewRun{
		Trigger:   "post_change",
		Target:    reviewTargetChange,
		ChangeSet: ReviewChangeSet{ChangedPaths: []string{"src/main.cpp"}},
		Evidence: ReviewEvidencePack{
			Sources:             []string{"git_diff", "verification"},
			Text:                "diff --git a/src/main.cpp b/src/main.cpp\n+return true;",
			VerificationFailed:  true,
			VerificationSummary: "[failed] go test ./... [1 failing test]",
		},
	}
	run.Findings = append(run.Findings, deterministicReviewFindings(&runtimeState{}, run)...)
	gate := evaluateReviewGate(run)
	if gate.Verdict == reviewVerdictInsufficientEvidence {
		t.Fatalf("a real verification failure must not be insufficient_evidence, got %q", gate.Verdict)
	}
	if gate.Verdict != reviewVerdictNeedsRevision {
		t.Fatalf("expected needs_revision for a verification failure, got %q", gate.Verdict)
	}
}

// Regression (G-4): the post-change repair loop escalates once the identical
// blocker set recurs for RepeatedFindingBlockThreshold rounds, instead of
// leaving RepeatedFindingBlockThreshold unwired and looping on dead config.
func TestPostChangeConvergenceGuardEscalatesOnRepeatedBlockers(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	// Threshold defaults to 2.
	agent := &Agent{
		Config:  cfg,
		Session: NewSession(root, "", "", "", "default"),
	}
	setBlockers := func(ids ...string) {
		findings := make([]ReviewFinding, 0, len(ids))
		for _, id := range ids {
			findings = append(findings, ReviewFinding{
				ID:       id,
				Category: "correctness",
				Path:     "RegGit.Core/DatabaseService.cs",
				Title:    "blocker " + id,
			})
		}
		agent.Session.LastReviewRun = &ReviewRun{
			Gate:     GateDecision{Verdict: reviewVerdictNeedsRevision, BlockingFindings: ids},
			Findings: findings,
		}
	}

	setBlockers("RF-002", "RF-004")
	if agent.postChangeBlockerSetRepeatedPastThreshold() {
		t.Fatalf("first occurrence of a blocker set must not trip the guard")
	}
	setBlockers("RF-004", "RF-002") // same set, different order
	if !agent.postChangeBlockerSetRepeatedPastThreshold() {
		t.Fatalf("the identical blocker set on the second round must trip the guard")
	}

	// A different blocker set resets the tracker and gets its own budget.
	setBlockers("RF-009")
	if agent.postChangeBlockerSetRepeatedPastThreshold() {
		t.Fatalf("a new blocker set must reset the repeat tracker")
	}
}

// A clean review round resets the repeat tracker so a later unrelated blocker
// gets its full repair budget.
func TestPostChangeConvergenceGuardResetsOnProgress(t *testing.T) {
	root := t.TempDir()
	agent := &Agent{
		Config:  DefaultConfig(root),
		Session: NewSession(root, "", "", "", "default"),
	}
	agent.Session.PostChangeRepeatBlockerSignature = "stale"
	agent.Session.PostChangeRepeatBlockerCount = 5
	agent.resetPostChangeRepeatBlockerTracker()
	if agent.Session.PostChangeRepeatBlockerCount != 0 || agent.Session.PostChangeRepeatBlockerSignature != "" {
		t.Fatalf("reset must clear the repeat tracker, got %d/%q", agent.Session.PostChangeRepeatBlockerCount, agent.Session.PostChangeRepeatBlockerSignature)
	}
	_ = filepath.Separator
}
