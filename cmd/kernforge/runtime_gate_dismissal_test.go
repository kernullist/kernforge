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

func TestGateClearDismissesSessionReviewFromRuntimeGate(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"UserCommon.h"})

	run := ReviewRun{
		ID:                "review-this-session",
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
	// Disk history may exist; gate must not auto-attach it for a foreign session.
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
	if err := os.MkdirAll(reviewRunDir(root, run.ID), 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reviewRunDir(root, run.ID), "review.json"), data, 0o644); err != nil {
		t.Fatalf("write run review: %v", err)
	}

	session := NewSession(root, "provider", "model", "", "default")
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

	before := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)
	if before.Status != runtimeGateStatusBlocked {
		t.Fatalf("expected stale session review to block, got %#v", before)
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
	if session.RuntimeGateClearedReview == nil || session.RuntimeGateClearedReview.ID != run.ID {
		t.Fatalf("expected cleared review stashed for restore, got %#v", session.RuntimeGateClearedReview)
	}
	if session.RuntimeGateDismissal == nil || session.RuntimeGateDismissal.Scope != runtimeGateDismissalScopeSession {
		t.Fatalf("expected default session dismissal, got %#v", session.RuntimeGateDismissal)
	}
	if disk, err := loadWorkspaceRuntimeGateDismissal(root); err != nil {
		t.Fatalf("load workspace dismissal: %v", err)
	} else if disk != nil {
		t.Fatalf("default clear must not write workspace dismissal, got %#v", disk)
	}

	after := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)
	if after.Status == runtimeGateStatusBlocked && runtimeGateBlockersAreReviewStalenessOnly(after) {
		t.Fatalf("stale-only block must not remain after /gate clear, got %#v", after)
	}
	if strings.Contains(strings.Join(after.Blockers, "\n"), runtimeGateBlockerStaleReviewPrefix) {
		t.Fatalf("stale blocker must be gone after clear, got %#v", after.Blockers)
	}

	// Brand-new session must not inherit disk latest.json.
	session2 := NewSession(root, "provider", "model", "", "default")
	session2.Messages = []Message{{Role: "user", Text: "UserCommon.h를 수정해"}}
	session2.PatchTransactions = session.PatchTransactions
	fresh := buildRuntimeGateLedger(root, session2, runtimeGateActionFinalAnswer)
	if fresh.ReviewRunID != "" {
		t.Fatalf("new session must not attach disk review, got review=%q blockers=%#v", fresh.ReviewRunID, fresh.Blockers)
	}
	if strings.Contains(strings.Join(fresh.Blockers, "\n"), runtimeGateBlockerStaleReviewPrefix) {
		t.Fatalf("new session must not be blocked by disk review: %#v", fresh)
	}

	// Restore re-attaches the dismissed review to *this* session only.
	out.Reset()
	if err := rt.handleGateCommand("restore"); err != nil {
		t.Fatalf("gate restore: %v", err)
	}
	if session.LastReviewRun == nil || session.LastReviewRun.ID != run.ID {
		t.Fatalf("restore must re-attach dismissed review to session, got %#v", session.LastReviewRun)
	}
	restored := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)
	if restored.Status != runtimeGateStatusBlocked {
		t.Fatalf("expected restore to re-enable stale block on same session, got %#v", restored)
	}
}

func TestGateRestoreFailsHonestlyWhenDismissedReviewUnavailable(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.RuntimeGateDismissal = &RuntimeGateDismissal{
		ClearedAt:              time.Now(),
		ReviewRunID:            "review-missing",
		Scope:                  runtimeGateDismissalScopeSession,
		IgnoreReviewUntilNewer: true,
	}
	// No RuntimeGateClearedReview and no disk artifact.
	rt := &runtimeState{
		writer:    &bytes.Buffer{},
		ui:        UI{},
		cfg:       Config{AutoLocale: boolPtr(false)},
		session:   session,
		store:     NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{Root: root, BaseRoot: root},
	}
	err := rt.handleGateCommand("restore")
	if err == nil {
		t.Fatal("expected restore to fail when dismissed review cannot be reattached")
	}
	if !strings.Contains(err.Error(), "review-missing") {
		t.Fatalf("error should name the missing review, got %v", err)
	}
	if session.RuntimeGateDismissal == nil {
		t.Fatal("failed restore must leave dismissal intact")
	}
	if session.LastReviewRun != nil {
		t.Fatalf("failed restore must not invent LastReviewRun, got %#v", session.LastReviewRun)
	}
}

func TestNewSessionDoesNotAttachDiskLatestReview(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"UserCommon.h"})
	run := ReviewRun{
		ID:                "review-prior-workspace",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Branch:            "main",
		CreatedAt:         time.Now().Add(-time.Hour),
		ReviewFingerprint: "fp-prior",
		ChangeSet:         ReviewChangeSet{ChangedPaths: []string{"other.go"}},
		Freshness:         ReviewFreshness{ReviewFingerprint: "fp-prior"},
		Gate:              GateDecision{Verdict: reviewVerdictApproved},
	}
	if err := os.MkdirAll(reviewArtifactRoot(root), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, _ := json.MarshalIndent(run, "", "  ")
	if err := os.WriteFile(filepath.Join(reviewArtifactRoot(root), "latest.json"), data, 0o644); err != nil {
		t.Fatalf("write latest: %v", err)
	}

	// Cold start with no this-session edits: disk latest must not attach or WARN.
	cold := NewSession(root, "provider", "model", "", "default")
	coldLedger := buildRuntimeGateLedger(root, cold, runtimeGateActionFinalAnswer)
	if coldLedger.ReviewRunID != "" {
		t.Fatalf("disk latest must not attach to a brand-new session, got %#v", coldLedger)
	}
	if runtimeGateNeedsRecoveryGuidance(coldLedger) {
		t.Fatalf("cold new session must not show prior-review gate CTA, got %#v", coldLedger)
	}

	// Even with this-session patch scope, prior disk review must not become ReviewRunID.
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: "UserCommon.h를 수정해"}}
	session.PatchTransactions = []PatchTransaction{{
		ID:        "patch-tx-new-session",
		Goal:      "UserCommon.h를 수정해",
		Status:    patchTransactionStatusCommitted,
		StartedAt: time.Now(),
		UpdatedAt: time.Now(),
		Entries: []PatchTransactionEntry{{
			ID:     "patch-tx-new-session-001",
			Status: "success",
			Paths:  []PatchPathChange{{Path: "UserCommon.h", Operation: "modify"}},
		}},
	}}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)
	if ledger.ReviewRunID != "" {
		t.Fatalf("disk latest must not attach as ReviewRunID, got %#v", ledger)
	}
	if strings.Contains(strings.Join(ledger.Blockers, "\n"), runtimeGateBlockerStaleReviewPrefix) {
		t.Fatalf("disk stale review must not hard-block a session that never attached it: %#v", ledger)
	}
}

func TestConversationClearResetsSessionGateState(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: "hi"}}
	session.LastReviewRun = &ReviewRun{ID: "review-keep", SchemaVersion: reviewSchemaVersion}
	session.RuntimeGateClearedReview = &ReviewRun{ID: "review-keep", SchemaVersion: reviewSchemaVersion}
	session.RuntimeGateDismissal = &RuntimeGateDismissal{
		ClearedAt:              time.Now(),
		ReviewRunID:            "review-keep",
		Scope:                  runtimeGateDismissalScopeSession,
		IgnoreReviewUntilNewer: true,
	}
	session.RuntimeGateLedger = &RuntimeGateLedger{ID: "lg", Status: runtimeGateStatusBlocked}
	session.PendingHarnessBlockedRecovery = &HarnessBlockedRecovery{Cause: harnessRecoveryCauseRepeatedToolCalls}

	var out bytes.Buffer
	rt := &runtimeState{
		writer:  &out,
		ui:      UI{color: false},
		cfg:     Config{AutoLocale: boolPtr(false)},
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
	}
	if _, err := rt.handleCommand(Command{Name: "new"}); err != nil {
		t.Fatalf("handleCommand new: %v", err)
	}
	if len(session.Messages) != 0 {
		t.Fatalf("expected messages cleared")
	}
	if session.LastReviewRun != nil {
		t.Fatalf("expected LastReviewRun cleared, got %#v", session.LastReviewRun)
	}
	if session.RuntimeGateDismissal != nil {
		t.Fatalf("expected RuntimeGateDismissal cleared, got %#v", session.RuntimeGateDismissal)
	}
	if session.RuntimeGateClearedReview != nil {
		t.Fatalf("expected RuntimeGateClearedReview cleared, got %#v", session.RuntimeGateClearedReview)
	}
	if session.RuntimeGateLedger != nil {
		t.Fatalf("expected RuntimeGateLedger cleared, got %#v", session.RuntimeGateLedger)
	}
	if session.PendingHarnessBlockedRecovery != nil {
		t.Fatalf("expected PendingHarnessBlockedRecovery cleared, got %#v", session.PendingHarnessBlockedRecovery)
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

func TestGateClearWorkspaceScopeStillWritesFile(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", nil)
	run := ReviewRun{
		ID:            "review-workspace-clear",
		SchemaVersion: reviewSchemaVersion,
		Target:        reviewTargetChange,
		Mode:          reviewModeGeneralChange,
		Branch:        "main",
		CreatedAt:     time.Now(),
		Gate:          GateDecision{Verdict: reviewVerdictApproved},
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
	if err := rt.handleGateCommand("clear --workspace"); err != nil {
		t.Fatalf("gate clear --workspace: %v", err)
	}
	disk, err := loadWorkspaceRuntimeGateDismissal(root)
	if err != nil || disk == nil || disk.ReviewRunID != run.ID {
		t.Fatalf("expected workspace dismissal, got %#v err=%v", disk, err)
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
	if !strings.Contains(joined, "keep editing") ||
		!strings.Contains(joined, "choices appear") ||
		strings.Contains(joined, "numbered option") ||
		strings.Contains(joined, "/gate clear") ||
		strings.Contains(joined, "/review") {
		t.Fatalf("expected command-free actionable CTA, got:\n%s", joined)
	}

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
