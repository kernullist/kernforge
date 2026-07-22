package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGateClearDismissesPreviousReviewFromRuntimeGate(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"UserCommon.h"})

	// Persist a prior-session review on disk (this is what new sessions inherit).
	run := ReviewRun{
		ID:                "review-old-session",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Trigger:           "pre_write",
		Branch:            "main",
		CreatedAt:         time.Now().Add(-2 * time.Hour),
		ReviewFingerprint: "fp-old",
		ChangeSet:         ReviewChangeSet{ChangedPaths: []string{"other.go"}},
		Freshness:         ReviewFreshness{ReviewFingerprint: "fp-old"},
		Gate:              GateDecision{Verdict: reviewVerdictApproved},
	}
	if err := os.MkdirAll(reviewArtifactRoot(root), 0o755); err != nil {
		t.Fatalf("mkdir reviews: %v", err)
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reviewArtifactRoot(root), "latest.json"), data, 0o644); err != nil {
		t.Fatalf("write latest review: %v", err)
	}

	session := NewSession(root, "provider", "model", "", "default")
	// The final-answer gate uses the tracked patch scope only (2026-07-21), so
	// the fixture records the dirty file in a current-turn patch transaction.
	session.Messages = []Message{{
		Role: "user",
		Text: "UserCommon.h를 수정해",
	}}
	session.PatchTransactions = []PatchTransaction{{
		ID:        "patch-tx-dismiss",
		Goal:      "UserCommon.h를 수정해",
		Status:    patchTransactionStatusCommitted,
		StartedAt: time.Now(),
		UpdatedAt: time.Now(),
		Entries: []PatchTransactionEntry{{
			ID:     "patch-tx-dismiss-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "UserCommon.h",
				Operation: "modify",
			}},
		}},
	}}
	session.LastReviewRun = &run
	store := NewSessionStore(filepath.Join(root, "sessions"))

	// Before clear: stale review must block final answer.
	before := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)
	if before.Status != runtimeGateStatusBlocked {
		t.Fatalf("expected stale prior review to block, got %#v", before)
	}

	var out bytes.Buffer
	rt := &runtimeState{
		writer:    &out,
		ui:        UI{color: false},
		cfg:       Config{AutoLocale: boolPtr(false)},
		session:   session,
		store:     store,
		workspace: Workspace{Root: root, BaseRoot: root},
	}
	if err := rt.handleGateCommand("clear --reason continue editing"); err != nil {
		t.Fatalf("gate clear: %v", err)
	}
	if session.LastReviewRun != nil {
		t.Fatalf("expected session LastReviewRun cleared, got %#v", session.LastReviewRun)
	}
	dismissal, err := loadWorkspaceRuntimeGateDismissal(root)
	if err != nil || dismissal == nil || !dismissal.Active() {
		t.Fatalf("expected workspace dismissal, got %#v err=%v", dismissal, err)
	}
	if dismissal.ReviewRunID != "review-old-session" {
		t.Fatalf("dismissal review id = %q", dismissal.ReviewRunID)
	}

	after := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)
	if after.Status == runtimeGateStatusBlocked && runtimeGateBlockersAreReviewStalenessOnly(after) {
		t.Fatalf("stale-only block must not remain after /gate clear, got %#v", after)
	}
	if strings.Contains(strings.Join(after.Blockers, "\n"), runtimeGateBlockerStaleReviewPrefix) {
		t.Fatalf("stale blocker must be gone after clear, got %#v", after.Blockers)
	}

	// New session in same workspace must also honor the workspace dismissal.
	session2 := NewSession(root, "provider", "model", "", "default")
	fresh := buildRuntimeGateLedger(root, session2, runtimeGateActionFinalAnswer)
	if strings.Contains(strings.Join(fresh.Blockers, "\n"), runtimeGateBlockerStaleReviewPrefix) {
		t.Fatalf("new session still blocked by dismissed review: %#v", fresh)
	}

	// Restore brings the gate baggage back.
	out.Reset()
	if err := rt.handleGateCommand("restore"); err != nil {
		t.Fatalf("gate restore: %v", err)
	}
	// Re-attach the review as a new session would from disk. The final-answer
	// gate only sees the tracked patch scope, so the same current-turn edit
	// fixture is required for the stale review to matter again.
	session2b := NewSession(root, "provider", "model", "", "default")
	session2b.Messages = []Message{{
		Role: "user",
		Text: "UserCommon.h를 수정해",
	}}
	session2b.PatchTransactions = []PatchTransaction{{
		ID:        "patch-tx-dismiss-restore",
		Goal:      "UserCommon.h를 수정해",
		Status:    patchTransactionStatusCommitted,
		StartedAt: time.Now(),
		UpdatedAt: time.Now(),
		Entries: []PatchTransactionEntry{{
			ID:     "patch-tx-dismiss-restore-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "UserCommon.h",
				Operation: "modify",
			}},
		}},
	}}
	restored := buildRuntimeGateLedger(root, session2b, runtimeGateActionFinalAnswer)
	if restored.Status != runtimeGateStatusBlocked {
		t.Fatalf("expected restore to re-enable stale block, got %#v", restored)
	}
}

func TestGateClearSessionScopeDoesNotWriteWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"a.go"})
	run := ReviewRun{
		ID:                "review-session-only",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Branch:            "main",
		CreatedAt:         time.Now().Add(-time.Hour),
		ReviewFingerprint: "fp",
		ChangeSet:         ReviewChangeSet{ChangedPaths: []string{"b.go"}},
		Freshness:         ReviewFreshness{ReviewFingerprint: "fp"},
		Gate:              GateDecision{Verdict: reviewVerdictApproved},
	}
	session := NewSession(root, "provider", "model", "", "default")
	session.LastReviewRun = &run
	rt := &runtimeState{
		writer:    &bytes.Buffer{},
		ui:        UI{},
		cfg:       Config{AutoLocale: boolPtr(false)},
		session:   session,
		store:     NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{Root: root, BaseRoot: root},
	}
	if err := rt.handleGateCommand("clear --session"); err != nil {
		t.Fatalf("gate clear --session: %v", err)
	}
	if session.RuntimeGateDismissal == nil || session.RuntimeGateDismissal.Scope != runtimeGateDismissalScopeSession {
		t.Fatalf("expected session dismissal, got %#v", session.RuntimeGateDismissal)
	}
	if disk, err := loadWorkspaceRuntimeGateDismissal(root); err != nil {
		t.Fatalf("load workspace dismissal: %v", err)
	} else if disk != nil {
		t.Fatalf("session-scope clear must not write workspace file, got %#v", disk)
	}
	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)
	if strings.Contains(strings.Join(ledger.Blockers, "\n"), runtimeGateBlockerStaleReviewPrefix) {
		t.Fatalf("session clear should drop stale blockers, got %#v", ledger)
	}
}

func TestRecordReviewRunClearsDismissalForNewReview(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.WorkingDir = root
	dismissal := &RuntimeGateDismissal{
		ClearedAt:              time.Now().Add(-time.Minute),
		ReviewRunID:            "review-old",
		Scope:                  runtimeGateDismissalScopeWorkspace,
		IgnoreReviewUntilNewer: true,
	}
	if err := saveWorkspaceRuntimeGateDismissal(root, dismissal); err != nil {
		t.Fatalf("save dismissal: %v", err)
	}
	session.recordReviewRun(ReviewRun{
		ID:     "review-new",
		Status: "completed",
		Gate:   GateDecision{Verdict: reviewVerdictApproved},
	})
	if disk, err := loadWorkspaceRuntimeGateDismissal(root); err != nil {
		t.Fatalf("load dismissal: %v", err)
	} else if disk != nil {
		t.Fatalf("new review must clear workspace dismissal, got %#v", disk)
	}
}

func TestRuntimeGateRecoveryCTAOmitsSlashCommands(t *testing.T) {
	ledger := RuntimeGateLedger{
		ID:       "lg",
		Action:   runtimeGateActionFinalAnswer,
		Blockers: []string{runtimeGateBlockerStaleReviewPrefix + " reviewed files changed since review: x.h"},
		NextCommands: []ReviewNextCommand{
			{Command: "/review", Reason: "stale"},
		},
	}
	ledger.Normalize()
	lines := runtimeGateRecoveryGuidanceLines(Config{AutoLocale: boolPtr(false)}, nil, ledger)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "numbered option") ||
		strings.Contains(joined, "/gate clear") ||
		strings.Contains(joined, "/review") {
		t.Fatalf("expected command-free CTA, got:\n%s", joined)
	}

	// Active dismissal does not change the Everyday CTA shape (still no slash ads).
	session := &Session{
		RuntimeGateDismissal: &RuntimeGateDismissal{
			ClearedAt:              time.Now(),
			ReviewRunID:            "review-old",
			IgnoreReviewUntilNewer: true,
			Scope:                  runtimeGateDismissalScopeSession,
		},
	}
	lines = runtimeGateRecoveryGuidanceLines(Config{AutoLocale: boolPtr(false)}, session, ledger)
	joined = strings.Join(lines, "\n")
	if strings.Contains(joined, "/gate clear") {
		t.Fatalf("CTA must never advertise /gate clear, got:\n%s", joined)
	}
}
