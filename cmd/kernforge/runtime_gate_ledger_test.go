package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func useRuntimeGateGitFixture(t *testing.T, branch string, changed []string) {
	t.Helper()
	previousBranch := runtimeGateGitBranchProvider
	previousChanged := runtimeGateChangedFilesProvider
	previousUsable := runtimeGateGitStatusUsableProvider
	previousFreshnessBranch := reviewFreshnessGitBranchProvider
	previousFreshnessChanged := reviewFreshnessChangedFilesProvider
	previousFreshnessUsable := reviewFreshnessGitStatusUsableProvider
	runtimeGateGitBranchProvider = func(root string) string {
		return branch
	}
	runtimeGateChangedFilesProvider = func(root string) []string {
		return append([]string(nil), changed...)
	}
	runtimeGateGitStatusUsableProvider = func(root string) bool {
		return true
	}
	reviewFreshnessGitBranchProvider = runtimeGateGitBranchProvider
	reviewFreshnessChangedFilesProvider = runtimeGateChangedFilesProvider
	reviewFreshnessGitStatusUsableProvider = runtimeGateGitStatusUsableProvider
	t.Cleanup(func() {
		runtimeGateGitBranchProvider = previousBranch
		runtimeGateChangedFilesProvider = previousChanged
		runtimeGateGitStatusUsableProvider = previousUsable
		reviewFreshnessGitBranchProvider = previousFreshnessBranch
		reviewFreshnessChangedFilesProvider = previousFreshnessChanged
		reviewFreshnessGitStatusUsableProvider = previousFreshnessUsable
	})
}

func TestRuntimeGateLedgerBlocksStaleReviewForFinalAnswer(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"main.go", "other.go"})
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "main.go와 other.go를 수정해",
	}}
	session.LastReviewRun = &ReviewRun{
		ID:                "review-stale",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Trigger:           "pre_write",
		Branch:            "main",
		ReviewFingerprint: "fp-1",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"main.go"},
		},
		Freshness: ReviewFreshness{
			ReviewFingerprint: "fp-1",
		},
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
		},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)

	if ledger.Status != runtimeGateStatusBlocked || ledger.Ready {
		t.Fatalf("expected stale review to block final answer, got %#v", ledger)
	}
	if !strings.Contains(strings.Join(ledger.StaleReasons, " "), "other.go") {
		t.Fatalf("expected stale reason to mention unreviewed file, got %#v", ledger.StaleReasons)
	}
	if len(ledger.NextCommands) == 0 || ledger.NextCommands[0].Command != "/review" {
		t.Fatalf("expected /review recovery command, got %#v", ledger.NextCommands)
	}
	if ledger.StaleContextSummary == nil ||
		ledger.StaleContextSummary.Status != staleContextStatusBlocked ||
		ledger.StaleContextSummary.Counts[staleContextKindChangedFilesAfterReview] == 0 {
		t.Fatalf("expected stale context summary for changed files after review, got %#v", ledger.StaleContextSummary)
	}
}

func TestRuntimeGateFinalAnswerSkipsStaleReviewForGeneratedDocumentArtifact(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"SampleGame/BugReport.md"})
	if err := os.MkdirAll(filepath.Join(root, "SampleGame"), 0o755); err != nil {
		t.Fatalf("mkdir SampleGame: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "SampleGame", "BugReport.md"), []byte("# Bug Report\n"), 0o644); err != nil {
		t.Fatalf("write BugReport.md: %v", err)
	}
	session := NewSession(root, "provider", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		ID:           "accept-doc",
		SourcePrompt: "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 문서로 생성해",
		Mode:         "edit_code",
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "SampleGame/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	session.LastReviewRun = &ReviewRun{
		ID:                "review-stale-code",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Trigger:           "post_change",
		Branch:            "main",
		ReviewFingerprint: "fp-1",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"main.go"},
		},
		Freshness: ReviewFreshness{
			ReviewFingerprint: "fp-1",
		},
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
		},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)

	if ledger.Status != runtimeGateStatusReady || !ledger.Ready {
		t.Fatalf("expected document artifact final answer to be ready without stale review blockers, got %#v", ledger)
	}
	if strings.Contains(strings.Join(ledger.Blockers, " "), "review") ||
		strings.Contains(strings.Join(ledger.StaleReasons, " "), "BugReport.md") {
		t.Fatalf("expected generated document artifact to bypass stale review blockers, got %#v", ledger)
	}
}

func TestRuntimeGateApprovedDocumentArtifactHarnessSkipsStaleReviewWithoutRequestContext(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"SampleGame/BugReport.md"})
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "Reviewer feedback: revise the final answer before concluding.",
	}}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "SampleGame/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 4096,
			}},
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-doc-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "SampleGame/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	session.LastReviewRun = &ReviewRun{
		ID:                "review-stale-code",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Trigger:           "post_change",
		Branch:            "main",
		ReviewFingerprint: "fp-1",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"main.go"},
		},
		Freshness: ReviewFreshness{
			ReviewFingerprint: "fp-1",
		},
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
		},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)

	if ledger.Status != runtimeGateStatusReady || !ledger.Ready {
		t.Fatalf("expected approved document artifact harness to bypass stale review blockers, got %#v", ledger)
	}
	if strings.Contains(strings.Join(ledger.Blockers, " "), "review") ||
		strings.Contains(strings.Join(ledger.StaleReasons, " "), "BugReport.md") {
		t.Fatalf("expected approved document artifact harness to bypass review freshness, got %#v", ledger)
	}
}

func TestRuntimeGateQualityAcceptedDocumentArtifactSkipsStaleReviewWithoutPatchPaths(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"SampleGame/BugReport.md"})
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 문서로 생성해",
	}}
	session.AcceptanceContract = &AcceptanceContract{
		ID:           "accept-doc",
		SourcePrompt: "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 문서로 생성해",
		Mode:         "inspect_and_fix",
	}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: false,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "SampleGame/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 4096,
				Checks:       []string{"text readable"},
			}},
		},
		Outcome: OutcomeInvariantReport{
			Findings: []CodingHarnessFinding{{
				Severity: "blocker",
				Title:    "Final answer has inconsistent bug counts",
				Detail:   "The final answer needs a wording-only correction.",
			}},
		},
	}
	session.LastReviewRun = &ReviewRun{
		ID:                "review-stale-code",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Trigger:           "post_change",
		Branch:            "main",
		ReviewFingerprint: "fp-1",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"main.go"},
		},
		Freshness: ReviewFreshness{
			ReviewFingerprint: "fp-1",
			Stale:             true,
			StaleReason:       "unreviewed changed files: main.go",
		},
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
		},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)

	if ledger.Status != runtimeGateStatusReady || !ledger.Ready {
		t.Fatalf("expected accepted document content to bypass stale code review while final answer is repaired, got %#v", ledger)
	}
	if strings.Contains(strings.Join(ledger.Blockers, " "), "review") ||
		strings.Contains(strings.Join(ledger.StaleReasons, " "), "main.go") {
		t.Fatalf("expected generated document artifact content gate to suppress stale code review, got %#v", ledger)
	}
}

func TestRuntimeGateQualityAcceptedDocumentArtifactDoesNotSkipUnrelatedTurn(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"main.go"})
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{
		{
			Role: "user",
			Text: "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 문서로 생성해",
		},
		{
			Role:  "assistant",
			Phase: messagePhaseFinalAnswer,
			Text:  "SampleGame/BugReport.md 생성 완료",
		},
		{
			Role: "user",
			Text: "main.go 버그를 수정해",
		},
	}
	session.AcceptanceContract = &AcceptanceContract{
		ID:           "accept-doc",
		SourcePrompt: "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 문서로 생성해",
		Mode:         "inspect_and_fix",
	}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "SampleGame/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 4096,
				Checks:       []string{"text readable"},
			}},
		},
	}
	session.LastReviewRun = &ReviewRun{
		ID:                "review-stale-code",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Trigger:           "post_change",
		Branch:            "main",
		ReviewFingerprint: "fp-1",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"main.go"},
		},
		Freshness: ReviewFreshness{
			ReviewFingerprint: "fp-1",
			Stale:             true,
			StaleReason:       "unreviewed changed files: main.go",
		},
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
		},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)

	if ledger.Status != runtimeGateStatusBlocked || ledger.Ready {
		t.Fatalf("stale document artifact state must not waive review freshness for an unrelated code turn, got %#v", ledger)
	}
	if !strings.Contains(strings.Join(ledger.Blockers, " "), "latest review is stale") {
		t.Fatalf("expected stale review blocker for unrelated code turn, got %#v", ledger)
	}
}

func TestRuntimeGateBroaderScopeSteeringDoesNotUseDocumentArtifactBypass(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{
		{
			Role: "user",
			Text: "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 문서로 생성해",
		},
		{
			Role:  "assistant",
			Phase: messagePhaseFinalAnswer,
			Text:  "SampleGame/BugReport.md 생성 완료",
		},
		{
			Role: "user",
			Text: "문서 산출에 관해서만 검토하지 말고 모든 영역을 검토해야 해",
		},
	}
	session.AcceptanceContract = &AcceptanceContract{
		ID:           "accept-doc",
		SourcePrompt: "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 문서로 생성해",
		Mode:         "inspect_and_fix",
	}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "SampleGame/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 4096,
				Checks:       []string{"text readable"},
			}},
		},
	}

	if runtimeGateDocumentArtifactOnly(session, runtimeGateActionFinalAnswer, nil) {
		t.Fatalf("broader-scope steering must not use generated document artifact runtime-gate bypass")
	}
}

func TestRuntimeGateBlocksUnknownPatchScopeForFinalAnswer(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", nil)
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "main.go를 수정해",
	}}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-unknown",
		Goal:   "main.go를 수정해",
		Status: patchTransactionStatusActive,
		Warnings: []string{
			"external_edit reported a workspace mutation without changed_paths metadata, so the changed file scope is unknown.",
		},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)

	if ledger.Status != runtimeGateStatusBlocked || ledger.Ready {
		t.Fatalf("expected unknown patch scope to block final answer, got %#v", ledger)
	}
	blockers := strings.Join(ledger.Blockers, " ")
	if !strings.Contains(blockers, "unknown changed-file scope") ||
		!strings.Contains(blockers, "without changed_paths") {
		t.Fatalf("expected unknown scope blocker, got %#v", ledger.Blockers)
	}
	if len(ledger.NextCommands) == 0 || ledger.NextCommands[0].Command != "/review" {
		t.Fatalf("expected /review recovery command, got %#v", ledger.NextCommands)
	}
	if !runtimeGateBlocksFinalAnswer(ledger, "작업 완료") {
		t.Fatalf("expected undisclosed unknown scope blocker to block final answer")
	}
}

func TestRuntimeGateUnknownPatchScopeOverridesDocumentArtifactBypass(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", nil)
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 문서로 생성해",
	}}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "SampleGame/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 4096,
			}},
		},
	}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-doc-unknown",
		Goal:   "각 소스코드 파일들을 검토해서 버그를 찾아서 SampleGame/BugReport.md 문서로 생성해",
		Status: patchTransactionStatusActive,
		Warnings: []string{
			"write_file reported a workspace mutation without changed_paths metadata, so the changed file scope is unknown.",
		},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)

	if ledger.Status != runtimeGateStatusBlocked || ledger.Ready {
		t.Fatalf("expected unknown patch scope to override document artifact bypass, got %#v", ledger)
	}
	if !strings.Contains(strings.Join(ledger.Blockers, " "), "unknown changed-file scope") {
		t.Fatalf("expected unknown scope blocker, got %#v", ledger.Blockers)
	}
}

func TestRuntimeGateReviewBlocksArchivedUnknownPatchScope(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", nil)
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "main.go를 수정해",
	}}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-archived-unknown",
		Goal:   "main.go를 수정해",
		Status: patchTransactionStatusCommitted,
		Warnings: []string{
			"apply_patch reported a workspace mutation without changed_paths metadata, so the changed file scope is unknown.",
		},
	}}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionReview)

	if ledger.Status != runtimeGateStatusBlocked || ledger.Ready {
		t.Fatalf("expected review gate to block archived unknown patch scope, got %#v", ledger)
	}
	if ledger.PatchTransactionID != "patch-archived-unknown" {
		t.Fatalf("expected archived patch transaction to be linked, got %#v", ledger)
	}
}

func TestRuntimeGateFinalAnswerUsesPatchTransactionScopeOverUnrelatedDirtyFiles(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"README.md", "unrelated.go"})
	now := time.Now()
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "README.md를 업데이트해",
	}}
	session.PatchTransactions = []PatchTransaction{{
		ID:        "patch-tx-1",
		Goal:      "README.md를 업데이트해",
		Status:    patchTransactionStatusCommitted,
		StartedAt: now,
		UpdatedAt: now,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-tx-1-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "README.md",
				Operation: "modify",
			}},
		}},
	}}
	session.LastReviewRun = &ReviewRun{
		ID:                "review-scoped",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Branch:            "main",
		ReviewFingerprint: "fp-1",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"README.md"},
		},
		Freshness: ReviewFreshness{
			ReviewFingerprint: "fp-1",
		},
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
		},
	}

	finalLedger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)
	if strings.Contains(strings.Join(finalLedger.Blockers, " "), "unrelated.go") ||
		strings.Contains(strings.Join(finalLedger.StaleReasons, " "), "unrelated.go") {
		t.Fatalf("expected final answer gate to use patch transaction scope, got %#v", finalLedger)
	}
	if strings.Join(finalLedger.ChangedPaths, ",") != "README.md" {
		t.Fatalf("expected final changed paths to be scoped to patch transaction, got %#v", finalLedger.ChangedPaths)
	}

	gitLedger := buildRuntimeGateLedger(root, session, runtimeGateActionGitWrite)
	if !strings.Contains(strings.Join(gitLedger.Blockers, " "), "unrelated.go") {
		t.Fatalf("expected git write gate to account for unrelated dirty files, got %#v", gitLedger)
	}
}

func TestRuntimeGateCompletionAuditSkipsStaleActivePatchTransaction(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", nil)
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "README.md를 업데이트해",
	}}
	now := time.Now()
	session.ActivePatchTransaction = &PatchTransaction{
		ID:        "patch-old-active",
		Goal:      "RuntimeManager.cpp 버그를 수정해",
		Status:    patchTransactionStatusActive,
		StartedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
		Entries: []PatchTransactionEntry{{
			ID:     "patch-old-active-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "cmd/kernforge/agent.go",
				Operation: "modify",
			}},
		}},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:        "patch-readme",
		Goal:      "README.md를 업데이트해",
		Status:    patchTransactionStatusCommitted,
		StartedAt: now,
		UpdatedAt: now,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-readme-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "README.md",
				Operation: "modify",
			}},
		}},
	}}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionCompletionAudit)
	if ledger.PatchTransactionID != "patch-readme" {
		t.Fatalf("expected runtime gate to attach archived current-goal patch, got %#v", ledger)
	}
	if strings.Contains(strings.Join(ledger.ChangedPaths, ","), "cmd/kernforge/agent.go") {
		t.Fatalf("expected stale active patch path to be ignored, got %#v", ledger.ChangedPaths)
	}
	if !containsString(ledger.ChangedPaths, "README.md") {
		t.Fatalf("expected archived current-goal patch path, got %#v", ledger.ChangedPaths)
	}
}

func TestRuntimeGateFinalAnswerIgnoresArchivedPatchFromPreviousTurn(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"README.md"})
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{
		{
			Role: "user",
			Text: "README.md를 업데이트해",
		},
		{
			Role:  "assistant",
			Phase: messagePhaseFinalAnswer,
			Text:  "README.md 업데이트 완료",
		},
		{
			Role: "user",
			Text: "현재 상태만 알려줘",
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:        "patch-tx-old",
		Goal:      "README.md를 업데이트해",
		Status:    patchTransactionStatusCommitted,
		StartedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now().Add(-time.Hour),
		Entries: []PatchTransactionEntry{{
			ID:     "patch-tx-old-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "README.md",
				Operation: "modify",
			}},
		}},
	}}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)

	if len(ledger.ChangedPaths) != 0 {
		t.Fatalf("expected final-answer gate to ignore previous-turn archived patch paths, got %#v", ledger.ChangedPaths)
	}
	if ledger.PatchTransactionID != "" {
		t.Fatalf("expected final-answer gate not to attach previous-turn patch transaction, got %#v", ledger)
	}
	if ledger.Status != runtimeGateStatusReady || !ledger.Ready {
		t.Fatalf("expected previous-turn patch history not to require review for a read-only final answer, got %#v", ledger)
	}
}

func TestRuntimeGateFinalAnswerDoesNotUseArchivedPatchTimeForGitFallback(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"main.go"})
	now := time.Now()
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{
		{
			Role: "user",
			Text: "README.md를 수정해",
		},
		{
			Role:  "assistant",
			Phase: messagePhaseFinalAnswer,
			Text:  "README.md 수정 완료",
		},
		{
			Role: "user",
			Text: "main.go를 수정해",
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:          "patch-tx-old",
		Goal:        "README.md를 수정해",
		Status:      patchTransactionStatusCommitted,
		StartedAt:   now.Add(-time.Minute),
		UpdatedAt:   now.Add(time.Minute),
		CompletedAt: now.Add(time.Minute),
		Entries: []PatchTransactionEntry{{
			ID:     "patch-tx-old-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "README.md",
				Operation: "modify",
			}},
		}},
	}}
	session.LastVerification = &VerificationReport{
		GeneratedAt:  now,
		Trigger:      "manual",
		Workspace:    root,
		ChangedPaths: []string{"main.go"},
		Steps: []VerificationStep{{
			Label:   "go test",
			Command: "go test ./cmd/kernforge",
			Status:  VerificationPassed,
		}},
	}
	session.LastReviewRun = &ReviewRun{
		ID:                "review-main",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Branch:            "main",
		ReviewFingerprint: "fp-main",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"main.go"},
		},
		Freshness: ReviewFreshness{
			ReviewFingerprint: "fp-main",
		},
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
		},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)

	if ledger.PatchTransactionID != "" {
		t.Fatalf("expected final-answer git fallback not to attach previous-turn patch transaction, got %#v", ledger)
	}
	if !slices.Equal(ledger.ChangedPaths, []string{"main.go"}) {
		t.Fatalf("expected final-answer git fallback to use current dirty file only, got %#v", ledger.ChangedPaths)
	}
	if len(ledger.Warnings) != 0 || !ledger.Ready {
		t.Fatalf("expected stale archived patch time not to stale the current final-answer ledger, got %#v", ledger)
	}
}

func TestRuntimeGateFinalAnswerUsesGitFallbackForPreservedCodeContinuation(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"main.go"})
	original := "main.go 버그를 수정해"
	session := NewSession(root, "provider", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		ID:           "accept-code",
		SourcePrompt: original,
		Mode:         "inspect_and_fix",
	}
	session.TaskState = &TaskState{Goal: original}
	session.Messages = []Message{
		{Role: "user", Text: original},
		{Role: "assistant", Text: "main.go 수정 중입니다."},
		{Role: "user", Text: "좋아 너무 작은 기능까지 먼저 확인하지 말고 전체적인 큰 흐름과 관련된 것들 위주로 먼저 확인하자"},
	}
	session.LastVerification = &VerificationReport{
		GeneratedAt:  time.Now(),
		Trigger:      "manual",
		Workspace:    root,
		ChangedPaths: []string{"main.go"},
		Steps: []VerificationStep{{
			Label:   "go test",
			Command: "go test ./cmd/kernforge",
			Status:  VerificationPassed,
		}},
	}
	session.LastReviewRun = &ReviewRun{
		ID:                "review-main",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Branch:            "main",
		ReviewFingerprint: "fp-main",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"main.go"},
		},
		Freshness: ReviewFreshness{
			ReviewFingerprint: "fp-main",
		},
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
		},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)

	if !slices.Equal(ledger.ChangedPaths, []string{"main.go"}) {
		t.Fatalf("expected preserved code continuation to use git changed fallback, got %#v", ledger.ChangedPaths)
	}
	if ledger.Status != runtimeGateStatusReady || !ledger.Ready {
		t.Fatalf("expected reviewed preserved code continuation to be ready, got %#v", ledger)
	}
}

func TestRuntimeGateReviewUsesPatchTransactionScopeOverUnrelatedDirtyFiles(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"README.md", "unrelated.go"})
	now := time.Now()
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "README.md를 수정해",
	}}
	session.PatchTransactions = []PatchTransaction{{
		ID:        "patch-tx-1",
		Goal:      "README.md를 수정해",
		Status:    patchTransactionStatusCommitted,
		StartedAt: now,
		UpdatedAt: now,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-tx-1-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "README.md",
				Operation: "modify",
			}},
		}},
	}}
	session.LastReviewRun = &ReviewRun{
		ID:                "review-scoped",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Branch:            "main",
		ReviewFingerprint: "fp-1",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"README.md"},
		},
		Freshness: ReviewFreshness{
			ReviewFingerprint: "fp-1",
		},
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
		},
	}

	reviewLedger := buildRuntimeGateLedger(root, session, runtimeGateActionReview)
	if strings.Contains(strings.Join(reviewLedger.ChangedPaths, " "), "unrelated.go") ||
		strings.Contains(strings.Join(reviewLedger.Blockers, " "), "unrelated.go") ||
		strings.Contains(strings.Join(reviewLedger.StaleReasons, " "), "unrelated.go") {
		t.Fatalf("expected review gate to use patch transaction scope, got %#v", reviewLedger)
	}
	if strings.Join(reviewLedger.ChangedPaths, ",") != "README.md" {
		t.Fatalf("expected review changed paths to be scoped to patch transaction, got %#v", reviewLedger.ChangedPaths)
	}
}

func TestReviewFreshnessDetectsEditedReviewedPathByHash(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"main.go"})
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\nconst value = 1\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	run := ReviewRun{
		ID:                "review-hash",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Branch:            "main",
		ReviewFingerprint: "fp-1",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"main.go"},
		},
		Freshness: ReviewFreshness{
			ReviewFingerprint: "fp-1",
		},
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
		},
	}
	run.ArtifactIntegrity = buildReviewArtifactIntegrity(root, run)
	if err := os.WriteFile(target, []byte("package main\nconst value = 2\n"), 0o644); err != nil {
		t.Fatalf("rewrite main.go: %v", err)
	}

	freshness := reviewLatestFreshnessForRoot(root, run)
	if !freshness.Stale || !slices.Contains(freshness.InvalidatedBy, "file_hashes") {
		t.Fatalf("expected file hash mismatch to stale review, got %#v", freshness)
	}

	session := NewSession(root, "provider", "model", "", "default")
	session.LastReviewRun = &run
	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)
	if ledger.Status != runtimeGateStatusBlocked || !strings.Contains(strings.Join(ledger.StaleReasons, " "), "main.go") {
		t.Fatalf("expected runtime gate to block stale reviewed file, got %#v", ledger)
	}
}

func TestRuntimeGateFinalAnswerRequiresExplicitBlockerDisclosure(t *testing.T) {
	ledger := RuntimeGateLedger{
		Status: runtimeGateStatusBlocked,
		Blockers: []string{
			"latest review is stale: unreviewed changed files: main.go",
		},
	}
	if runtimeGateFinalAnswerDisclosesBlockers(ledger, "review completed") {
		t.Fatalf("generic review wording should not disclose a runtime gate blocker")
	}
	if !runtimeGateFinalAnswerDisclosesBlockers(ledger, "Blocked: latest review is stale because main.go is unreviewed.") {
		t.Fatalf("explicit stale review blocker should be accepted")
	}
}

func TestRuntimeGateLedgerLinksReviewPatchVerificationAndWaiver(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"README.md"})
	now := time.Now()
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "README.md를 수정해",
	}}
	session.PatchTransactions = []PatchTransaction{{
		ID:        "patch-tx-1",
		Goal:      "README.md를 수정해",
		Status:    patchTransactionStatusCommitted,
		StartedAt: now,
		UpdatedAt: now,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-tx-1-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "README.md",
				Operation: "modify",
			}},
		}},
	}}
	session.LastVerification = &VerificationReport{
		GeneratedAt:  now,
		Trigger:      "manual",
		Workspace:    root,
		ChangedPaths: []string{"README.md"},
		Steps: []VerificationStep{{
			Label:   "go test",
			Command: "go test ./...",
			Status:  VerificationPassed,
		}},
	}
	session.LastReviewRun = &ReviewRun{
		ID:                "review-waived",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Branch:            "main",
		ReviewFingerprint: "fp-1",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"README.md"},
		},
		Freshness: ReviewFreshness{
			ReviewFingerprint: "fp-1",
		},
		Gate: GateDecision{
			Verdict:          reviewVerdictApproved,
			BlockingFindings: []string{"RF-001"},
		},
		Waivers: []ReviewWaiver{{
			ID:        "waiver-1",
			FindingID: "RF-001",
			Reason:    "accepted for test",
			Allowed:   true,
			Status:    "active",
			ExpiresAt: now.Add(time.Hour),
		}},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)

	if ledger.Status != runtimeGateStatusReady || !ledger.Ready {
		t.Fatalf("expected waived blocker and passing verification to be ready, got %#v", ledger)
	}
	if ledger.ReviewRunID != "review-waived" || ledger.PatchTransactionID != "patch-tx-1" {
		t.Fatalf("expected linked review and patch IDs, got %#v", ledger)
	}
	if ledger.VerificationReportID == "" || ledger.FinalAnswerReviewID != "" {
		t.Fatalf("unexpected verification/final-review IDs: %#v", ledger)
	}
	if len(ledger.Waivers) != 1 || !strings.Contains(ledger.Waivers[0], "RF-001") {
		t.Fatalf("expected active waiver summary, got %#v", ledger.Waivers)
	}
}

func TestRuntimeGateTreatsFailedVerificationBeforeCurrentPatchAsWarning(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"README.md"})
	now := time.Now()
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "README.md를 수정해",
	}}
	session.PatchTransactions = []PatchTransaction{{
		ID:            "patch-tx-current",
		Goal:          "README.md를 수정해",
		Status:        patchTransactionStatusCommitted,
		WorkspaceRoot: root,
		StartedAt:     now.Add(-time.Minute),
		UpdatedAt:     now,
		CompletedAt:   now,
		Entries: []PatchTransactionEntry{{
			ID:     "patch-tx-current-001",
			Status: "success",
			Paths: []PatchPathChange{{
				Path:      "README.md",
				Operation: "modify",
			}},
		}},
	}}
	session.LastVerification = &VerificationReport{
		GeneratedAt:  now.Add(-2 * time.Hour),
		Workspace:    root,
		ChangedPaths: []string{"README.md"},
		Steps: []VerificationStep{{
			Label:   "go test",
			Command: "go test ./...",
			Status:  VerificationFailed,
			Output:  "compile failed",
		}},
	}
	session.LastReviewRun = &ReviewRun{
		ID:                "review-current",
		SchemaVersion:     reviewSchemaVersion,
		Target:            reviewTargetChange,
		Mode:              reviewModeGeneralChange,
		Branch:            "main",
		ReviewFingerprint: "fp-1",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"README.md"},
		},
		Freshness: ReviewFreshness{
			ReviewFingerprint: "fp-1",
		},
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
		},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)

	if strings.Contains(strings.Join(ledger.Blockers, " "), "verification failed") {
		t.Fatalf("stale failed verification must not block current patch, got %#v", ledger)
	}
	if !strings.Contains(strings.Join(ledger.Warnings, " "), "latest verification predates") {
		t.Fatalf("expected stale verification warning, got %#v", ledger.Warnings)
	}
	if ledger.StaleContextSummary == nil ||
		ledger.StaleContextSummary.Status != staleContextStatusWarned ||
		ledger.StaleContextSummary.Counts[staleContextKindStaleVerification] == 0 {
		t.Fatalf("expected stale verification summary warning, got %#v", ledger.StaleContextSummary)
	}
}

func TestRuntimeGateBlocksDocumentArtifactChangedAfterQualityGate(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"docs/report.md"})
	now := time.Now()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "report.md"), []byte("# Report\n\nCurrent content.\n"), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	session := NewSession(root, "provider", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		RequestClass:      reviewRequestClassDocumentArtifact,
		LifecycleKind:     reviewLifecycleKindDocumentArtifact,
		SourcePrompt:      "write docs/report.md as the document artifact",
		RequiredArtifacts: []string{"docs/report.md"},
	}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		GeneratedAt: now.Add(-2 * time.Minute),
		Approved:    true,
		ArtifactQuality: ArtifactQualityReport{
			GeneratedAt: now.Add(-2 * time.Minute),
			Artifacts: []ArtifactQualityCheck{{
				Path:         "docs/report.md",
				Kind:         "markdown",
				ContentChars: 64,
				Substantive:  true,
			}},
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:          "patch-doc-after-quality",
		Status:      patchTransactionStatusCommitted,
		StartedAt:   now.Add(-time.Minute),
		UpdatedAt:   now,
		CompletedAt: now,
		Entries: []PatchTransactionEntry{{
			ID:          "patch-doc-after-quality-001",
			ToolName:    "write_file",
			Status:      "success",
			StartedAt:   now.Add(-time.Minute),
			CompletedAt: now,
			Paths: []PatchPathChange{{
				Path:      "docs/report.md",
				Operation: "write_file",
			}},
		}},
	}}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)

	if ledger.Status != runtimeGateStatusBlocked {
		t.Fatalf("expected stale document artifact quality to block final answer, got %#v", ledger)
	}
	if ledger.StaleContextSummary == nil ||
		ledger.StaleContextSummary.Counts[staleContextKindChangedArtifactsAfterQuality] == 0 {
		t.Fatalf("expected stale artifact-quality summary, got %#v", ledger.StaleContextSummary)
	}
	if !strings.Contains(strings.Join(ledger.Blockers, " "), "artifact-quality evidence is stale") {
		t.Fatalf("expected artifact-quality stale blocker, got %#v", ledger.Blockers)
	}
}

func TestCompletionAuditIncludesRuntimeGateLedger(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:            "review-blocked",
		SchemaVersion: reviewSchemaVersion,
		Target:        reviewTargetChange,
		Mode:          reviewModeGeneralChange,
		Branch:        "main",
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-001"},
		},
		Findings: []ReviewFinding{{
			ID:         "RF-001",
			Severity:   reviewSeverityBlocker,
			Title:      "blocking review finding",
			BlocksGate: true,
		}},
	}
	rt := &runtimeState{
		session: session,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	artifact := rt.buildCompletionAuditArtifact(root, "finish guarded work")

	if artifact.RuntimeGateLedger == nil {
		t.Fatalf("expected runtime gate ledger in completion audit")
	}
	if artifact.RuntimeGateLedger.Status != runtimeGateStatusBlocked {
		t.Fatalf("expected blocked runtime gate ledger, got %#v", artifact.RuntimeGateLedger)
	}
	item, ok := completionAuditChecklistItem(artifact, "No runtime blockers remain")
	if !ok || item.Status != completionAuditStatusBlocked {
		t.Fatalf("expected blocked runtime gate checklist item, got %#v ok=%t", item, ok)
	}
}

func TestRuntimeGateFeedbackBlocksGitWriteOnReviewBlocker(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"README.md"})
	session := NewSession(root, "provider", "model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:            "review-blocker",
		SchemaVersion: reviewSchemaVersion,
		Target:        reviewTargetChange,
		Mode:          reviewModeGeneralChange,
		Branch:        "main",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"README.md"},
		},
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-001"},
		},
	}
	agent := &Agent{
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}

	blocked, feedback := agent.runtimeGateFeedbackForAction(runtimeGateActionGitWrite)

	if !blocked {
		t.Fatalf("expected git write to be blocked")
	}
	if !strings.Contains(feedback, "RF-001") || session.RuntimeGateLedger == nil {
		t.Fatalf("expected feedback and session ledger to include review blocker, got %q %#v", feedback, session.RuntimeGateLedger)
	}
}

// TestRuntimeGateReviewerRouteFailureWithoutChangesDoesNotBlockNewSession locks
// in the fix for the cross-session permanent-block bug: a prior review whose
// only unwaived blocker is a required-reviewer-route failure (RF-REVIEWER-001)
// must NOT keep blocking a new session on unchanged code. The review-route
// failure is an infrastructure state (the review model was down), not a code
// defect, so with no files in gate scope the gate degrades to needs_review and
// asks for a fresh /review instead of staying blocked forever.
func TestRuntimeGateReviewerRouteFailureWithoutChangesDoesNotBlockNewSession(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", nil)
	session := NewSession(root, "provider", "model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:            "review-route-failed",
		SchemaVersion: reviewSchemaVersion,
		Target:        reviewTargetChange,
		Mode:          reviewModeGeneralChange,
		Branch:        "main",
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{requiredReviewerFailureFindingID},
		},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionGitWrite)

	if strings.EqualFold(ledger.Status, runtimeGateStatusBlocked) {
		t.Fatalf("reviewer-route failure with no changes must not hard-block, got status %q blockers %#v", ledger.Status, ledger.Blockers)
	}
	if !strings.EqualFold(ledger.Status, runtimeGateStatusNeedsReview) {
		t.Fatalf("expected needs_review, got status %q", ledger.Status)
	}
	if len(ledger.Blockers) != 0 {
		t.Fatalf("expected no hard blockers, got %#v", ledger.Blockers)
	}
	joinedWarnings := strings.ToLower(strings.Join(ledger.Warnings, " | "))
	if !strings.Contains(joinedWarnings, "review route") {
		t.Fatalf("expected a review-route warning, got %#v", ledger.Warnings)
	}
	foundReviewNext := false
	for _, cmd := range ledger.NextCommands {
		if strings.TrimSpace(cmd.Command) == "/review" {
			foundReviewNext = true
			break
		}
	}
	if !foundReviewNext {
		t.Fatalf("expected a /review next command, got %#v", ledger.NextCommands)
	}
}

// TestRuntimeGateReviewerRouteFailureWithCodeBlockerStillBlocks confirms the
// safety boundary: when a genuine code finding is also blocking, the gate still
// hard-blocks. The route-failure relaxation only applies when every unwaived
// blocker is a reviewer-route failure.
func TestRuntimeGateReviewerRouteFailureWithCodeBlockerStillBlocks(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", nil)
	session := NewSession(root, "provider", "model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:            "review-route-and-code",
		SchemaVersion: reviewSchemaVersion,
		Target:        reviewTargetChange,
		Mode:          reviewModeGeneralChange,
		Branch:        "main",
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{requiredReviewerFailureFindingID, "RF-001"},
		},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionGitWrite)

	if !strings.EqualFold(ledger.Status, runtimeGateStatusBlocked) {
		t.Fatalf("a real code blocker must still hard-block, got status %q", ledger.Status)
	}
	if !strings.Contains(strings.Join(ledger.Blockers, " | "), "RF-001") {
		t.Fatalf("expected the code blocker to be carried, got %#v", ledger.Blockers)
	}
}

// TestRuntimeGateReviewerRouteFailureWithChangedFilesStillBlocks confirms that a
// reviewer-route failure with files in gate scope still hard-blocks: the change
// genuinely needs a completed review, so the relaxation must not apply.
func TestRuntimeGateReviewerRouteFailureWithChangedFilesStillBlocks(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"README.md"})
	session := NewSession(root, "provider", "model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:            "review-route-failed-with-changes",
		SchemaVersion: reviewSchemaVersion,
		Target:        reviewTargetChange,
		Mode:          reviewModeGeneralChange,
		Branch:        "main",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"README.md"},
		},
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{requiredReviewerFailureFindingID},
		},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionGitWrite)

	if !strings.EqualFold(ledger.Status, runtimeGateStatusBlocked) {
		t.Fatalf("reviewer-route failure with changed files in scope must still hard-block, got status %q", ledger.Status)
	}
}

// TestRuntimeGateReviewOriginLineSummarizesProvenance locks in that the gate
// status can answer "which prompt/command/time produced this review" -- the
// origin line carries the trigger, the auto/manual mode, the creation time, and
// an echo of the original request, and does not duplicate the mode word when the
// trigger already conveys it.
func TestRuntimeGateReviewOriginLineSummarizesProvenance(t *testing.T) {
	ledger := RuntimeGateLedger{
		ReviewTrigger:         "pre_write",
		ReviewAutoTriggered:   true,
		ReviewCreatedAt:       time.Date(2026, 6, 14, 10, 10, 0, 0, time.UTC),
		ReviewOriginalRequest: "implement the policy export feature",
	}
	line := runtimeGateReviewOriginLine(Config{AutoLocale: boolPtr(false)}, ledger)
	for _, want := range []string{"auto", "pre_write", "2026-06-14 10:10", "implement the policy export feature"} {
		if !strings.Contains(line, want) {
			t.Fatalf("origin line missing %q: %s", want, line)
		}
	}
	// A trigger that already conveys the mode must localize without duplicating
	// it (no "수동 manual").
	manual := runtimeGateReviewOriginLine(Config{AutoLocale: boolPtr(true)}, RuntimeGateLedger{ReviewTrigger: "manual"})
	if strings.Contains(manual, "manual") {
		t.Fatalf("manual trigger should not echo the raw mode token: %s", manual)
	}
	if !strings.Contains(manual, "수동") {
		t.Fatalf("manual trigger should localize to 수동: %s", manual)
	}
	// No review provenance at all yields an empty line (caller omits the row).
	if got := runtimeGateReviewOriginLine(Config{}, RuntimeGateLedger{}); got != "" {
		t.Fatalf("empty provenance should render nothing, got %q", got)
	}

	// An explicit /review invocation shows the actual command (so an arg-less run
	// still discloses its origin) instead of the opaque "explicit_command" enum.
	bare := runtimeGateReviewOriginLine(Config{AutoLocale: boolPtr(false)}, RuntimeGateLedger{ReviewTrigger: "explicit_command"})
	if !strings.Contains(bare, "/review") || strings.Contains(bare, "explicit_command") {
		t.Fatalf("arg-less explicit_command should show /review, got %q", bare)
	}
	withArgs := runtimeGateReviewOriginLine(Config{AutoLocale: boolPtr(false)}, RuntimeGateLedger{ReviewTrigger: "explicit_command", ReviewOriginalRequest: "--path app.py"})
	if !strings.Contains(withArgs, "/review --path app.py") {
		t.Fatalf("explicit_command with args should show them after /review, got %q", withArgs)
	}
	if strings.Contains(withArgs, "\"--path app.py\"") {
		t.Fatalf("explicit_command args must not be double-quoted as a prompt, got %q", withArgs)
	}
}

// TestRuntimeGateNeedsReviewStatusOmitsRedundantBlockerSentences confirms that
// when the gate has degraded to needs_review (reviewer-route failure, nothing in
// scope), the compact status no longer echoes the underlying review-finding
// blocker sentences -- the single ledger warning already explains it. A genuinely
// blocked gate still shows its blocker sentence.
func TestRuntimeGateNeedsReviewStatusOmitsRedundantBlockerSentences(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", nil)
	session := NewSession(root, "provider", "model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:            "review-route-failed",
		SchemaVersion: reviewSchemaVersion,
		Target:        reviewTargetChange,
		Mode:          reviewModeGeneralChange,
		Branch:        "main",
		Trigger:       "explicit_command",
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{requiredReviewerFailureFindingID},
		},
		Findings: []ReviewFinding{{
			ID:         requiredReviewerFailureFindingID,
			Source:     "deterministic",
			Severity:   reviewSeverityBlocker,
			Category:   "evidence_gap",
			Title:      "Required review route failed",
			BlocksGate: true,
		}},
	}
	var buf bytes.Buffer
	rt := &runtimeState{cfg: Config{AutoLocale: boolPtr(false)}, session: session, workspace: Workspace{BaseRoot: root, Root: root}, ui: NewUI(), writer: &buf}
	rt.writeRuntimeGateStatusWithDetail(&buf, runtimeGateActionGitWrite, false)
	out := buf.String()

	if !strings.Contains(out, "needs review") {
		t.Fatalf("expected a needs_review gate, got:\n%s", out)
	}
	if !strings.Contains(out, "latest review did not complete") {
		t.Fatalf("expected the single ledger warning to remain, got:\n%s", out)
	}
	// The underlying review-finding blocker sentence (a humanized blocker-class
	// label like "missing evidence: ...") must not be echoed in compact mode when
	// the gate is not actually blocked.
	if strings.Contains(out, "missing evidence") {
		t.Fatalf("needs_review status should not echo the review-finding blocker sentence:\n%s", out)
	}
}

// TestRuntimeGateAttachReviewCarriesProvenance confirms the review provenance is
// copied into the ledger so /status can render it.
func TestRuntimeGateAttachReviewCarriesProvenance(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", nil)
	session := NewSession(root, "provider", "model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:            "review-origin",
		SchemaVersion: reviewSchemaVersion,
		Target:        reviewTargetChange,
		Mode:          reviewModeGeneralChange,
		Branch:        "main",
		Trigger:       "pre_write",
		AutoTriggered: true,
		CreatedAt:     time.Date(2026, 6, 14, 10, 10, 0, 0, time.UTC),
		RequestAnalysis: ReviewRequestAnalysis{
			OriginalRequest: "implement the policy export feature",
		},
		Gate: GateDecision{Verdict: reviewVerdictApproved},
	}

	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionGitWrite)

	if ledger.ReviewTrigger != "pre_write" || !ledger.ReviewAutoTriggered {
		t.Fatalf("expected trigger provenance, got trigger=%q auto=%t", ledger.ReviewTrigger, ledger.ReviewAutoTriggered)
	}
	if ledger.ReviewOriginalRequest != "implement the policy export feature" {
		t.Fatalf("expected original request provenance, got %q", ledger.ReviewOriginalRequest)
	}
	if ledger.ReviewCreatedAt.IsZero() {
		t.Fatalf("expected created-at provenance to be carried")
	}
}

func TestGitWriteGateDoesNotAutoRunImplicitModelReview(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"README.md"})
	cfg := DefaultConfig(root)
	cfg.Review.ModelReviewConsent = modelReviewConsentAsk
	reviewer := &scriptedProviderClient{replies: []ChatResponse{approvedReviewResponse("reviewer should not run")}}
	session := NewSession(root, "provider", "model", "", "default")
	agent := &Agent{
		Config:         cfg,
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
	}

	blocked, feedback := agent.runtimeGateFeedbackForAction(runtimeGateActionGitWrite)

	if !blocked {
		t.Fatalf("expected git write gate to block until an explicit review exists")
	}
	if len(reviewer.requests) != 0 {
		t.Fatalf("git write gate must not send an implicit reviewer request, got %d request(s)", len(reviewer.requests))
	}
	if !strings.Contains(feedback, "/review") || session.RuntimeGateLedger == nil {
		t.Fatalf("expected deterministic git-write gate feedback to point at explicit review, got %q %#v", feedback, session.RuntimeGateLedger)
	}
}

func TestRuntimeGateStatusOutputShowsRecoveryCommand(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"main.go"})
	session := NewSession(root, "provider", "model", "", "default")
	session.Messages = []Message{{
		Role: "user",
		Text: "main.go를 수정해",
	}}
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	rt.printRuntimeGateStatusDetail(runtimeGateActionFinalAnswer)

	text := output.String()
	for _, want := range []string{"Runtime Gate", "runtime_gate", runtimeGateStatusNeedsReview, "review_freshness", "missing", "next_command", "/review"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected status output to contain %q, got %q", want, text)
		}
	}
	if session.RuntimeGateLedger == nil || session.RuntimeGateLedger.Status != runtimeGateStatusNeedsReview {
		t.Fatalf("expected session runtime gate ledger to be refreshed, got %#v", session.RuntimeGateLedger)
	}
}

func TestRuntimeGateStatusShowsReviewDecisionObservability(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"main.go"})
	session := NewSession(root, "provider", "model", "", "default")
	run := ReviewRun{
		ID:           "review-ops-1",
		Trigger:      "post_change",
		Target:       reviewTargetChange,
		Mode:         reviewModeGeneralChange,
		Flow:         "change_review",
		RequestClass: reviewRequestClassModifyThenReview,
		RequestAnalysis: ReviewRequestAnalysis{
			RequestClass:           reviewRequestClassModifyThenReview,
			RequestClassReason:     "test classification",
			RequestClassConfidence: 0.87,
		},
		CreatedAt: time.Now(),
		Gate: GateDecision{
			Verdict: reviewVerdictNeedsRevision,
			Action:  reviewGateActionRepairRequired,
			Reason:  "blocking review findings require revision",
			NextCommands: []ReviewNextCommand{{
				ID:      "repair",
				Command: "/session continuity continue from review",
				Reason:  "latest review has blocking findings",
				Safety:  "safe_local",
			}},
		},
		Result: ReviewResult{Verdict: reviewVerdictNeedsRevision},
		SingleModelPolicy: SingleModelReviewPolicy{
			Enabled:             true,
			IndependenceLevel:   "single_model",
			NoCrossReviewReason: "no independent reviewer configured",
		},
		SingleModelSecondPass: &SingleModelSecondPassReview{
			Enabled:       true,
			Status:        "cached",
			CacheHit:      true,
			Model:         "openai-codex-subscription/gpt-5.4",
			ReviewedPaths: []string{"main.go"},
		},
		CrossReviewTriage: &CrossReviewTriageLedger{
			Items: []CrossReviewTriageEntry{{
				FindingID:        "RF-X",
				TriageStatus:     crossReviewTriageNeedsUserDecision,
				Title:            "Needs a product decision",
				UserActionNeeded: true,
				UserActionPrompt: "Use `/session continuity continue from review` to repair RF-X.",
			}},
			TotalCount:      1,
			StatusCounts:    map[string]int{crossReviewTriageNeedsUserDecision: 1},
			IncompleteCount: 0,
		},
		ObligationLedger: ReviewObligationLedger{
			TotalCount:            3,
			OpenCount:             3,
			OpenRepairCount:       1,
			OpenVerificationCount: 1,
			OpenEvidenceCount:     1,
			Summary:               []string{"repair=1", "verification=1", "evidence=1"},
			Items: []ReviewObligation{
				{ID: "RO-1", Type: reviewObligationTypeRepair, Status: reviewObligationStatusOpen, Blocking: true},
				{ID: "RO-2", Type: reviewObligationTypeVerification, Status: reviewObligationStatusVerificationRequired, Blocking: true},
				{ID: "RO-3", Type: reviewObligationTypeEvidence, Status: reviewObligationStatusEvidenceUnconfirmed, Blocking: true},
			},
		},
	}
	session.LastReviewRun = &run
	var output bytes.Buffer
	rt := &runtimeState{
		writer:    &output,
		ui:        NewUI(),
		session:   session,
		workspace: Workspace{BaseRoot: root, Root: root},
	}

	rt.printRuntimeGateStatusDetail(runtimeGateActionFinalAnswer)

	text := output.String()
	for _, want := range []string{
		"review_decision",
		"id=review-ops-1",
		"trigger=post_change",
		"target=change",
		"mode=general_change",
		"class=modify_then_review",
		"classification_confidence",
		"classification_ambiguous",
		"gate_decision",
		"verdict=needs_revision",
		"action=repair_required",
		"second_pass",
		"review_route_quality",
		"status=cached",
		"cache_hit=true",
		"cross_review_triage",
		"needs_user_decision=1",
		"remaining_obligations",
		"repair=1",
		"verification=1",
		"evidence=1",
		"blocker_classes",
		"code_repair=1",
		"verification_gap=1",
		"evidence_gap=1",
		"next_command",
		"/session continuity continue from review",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected status output to contain %q, got %q", want, text)
		}
	}
}

func TestHooksStatusIncludesRuntimeGateSummary(t *testing.T) {
	root := t.TempDir()
	useRuntimeGateGitFixture(t, "main", []string{"main.go"})
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	rt.handleHooksCommand()

	text := output.String()
	for _, want := range []string{"Hooks", "runtime_gate", runtimeGateStatusNeedsReview, "review_freshness", "missing", "No hook rules loaded."} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected hooks output to contain %q, got %q", want, text)
		}
	}
}

func TestRuntimeGateSummaryTreatsEmptyLedgerAsUnknown(t *testing.T) {
	if got := runtimeGateStatusSummary(RuntimeGateLedger{}); got != "unknown" {
		t.Fatalf("expected empty ledger summary to be unknown, got %q", got)
	}
	if got := runtimeGateReviewFreshnessLabel(RuntimeGateLedger{}); got != "unknown" {
		t.Fatalf("expected empty ledger review freshness to be unknown, got %q", got)
	}
}
