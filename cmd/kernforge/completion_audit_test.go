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

func TestCompletionAuditCommandBlocksMissingArtifactAndFailedVerification(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	exitCode := 1
	session := NewSession(root, "provider", "model", "", "default")
	session.TaskState = &TaskState{Goal: "Codex parity completion"}
	session.AcceptanceContract = &AcceptanceContract{
		ID:                   "contract-1",
		SourcePrompt:         "finish everything",
		ExpectedBehaviors:    []string{"record completion evidence"},
		RequiredArtifacts:    []string{"docs/report.md"},
		VerificationRequired: true,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	session.TaskGraph = &TaskGraph{Nodes: []TaskNode{{
		ID:     "plan-01",
		Title:  "Fix failing verifier",
		Kind:   "verification",
		Status: "in_progress",
	}}}
	session.LastVerification = &VerificationReport{
		GeneratedAt: now,
		Trigger:     "manual",
		Workspace:   root,
		Steps: []VerificationStep{{
			Label:       "go test",
			Command:     "go test ./...",
			Status:      VerificationFailed,
			FailureKind: "test_failure",
			Output:      "FAIL package",
			Hint:        "Fix tests first.",
		}},
	}
	session.ActiveEditLoop = &EditLoopState{
		ID:                  "loop-1",
		Goal:                "finish",
		Status:              editLoopStatusActive,
		VerificationStatus:  string(VerificationFailed),
		VerificationSummary: "go test failed",
		StartedAt:           now,
		UpdatedAt:           now,
	}
	attempt := buildFailureRepairAttempt(session, *session.LastVerification)
	session.ActiveFailureRepair = &attempt
	session.BackgroundJobs = []BackgroundShellJob{{
		ID:             "job-1",
		Command:        "go test ./...",
		CommandSummary: "go test ./...",
		Status:         "failed",
		ExitCode:       &exitCode,
		LastOutput:     "FAIL package",
		StartedAt:      now,
		UpdatedAt:      now,
	}}
	session.BackgroundBundles = []BackgroundShellBundle{{
		ID:               "bundle-1",
		CommandSummaries: []string{"go test ./..."},
		JobIDs:           []string{"job-1"},
		Status:           "failed",
		LastSummary:      "completed=0 running=0 failed=1 total=1",
		StartedAt:        now,
		UpdatedAt:        now,
	}}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Findings: []CodingHarnessFinding{{
			Severity: "blocker",
			Title:    "Final state overclaims verification",
			Detail:   "The latest verification failed.",
		}},
	}
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleCompletionAuditCommand("finish Codex parity"); err != nil {
		t.Fatalf("handleCompletionAuditCommand: %v", err)
	}

	mdPath := filepath.Join(root, ".kernforge", "completion_audit", "latest.md")
	jsonPath := filepath.Join(root, ".kernforge", "completion_audit", "latest.json")
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read completion audit markdown: %v", err)
	}
	text := string(md)
	for _, want := range []string{"# Completion Audit", "Status: blocked", "Required artifact exists", completionAuditVerificationRequirement, "job-1", "bundle-1", "Final state overclaims verification"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected completion audit markdown to contain %q, got %q", want, text)
		}
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read completion audit json: %v", err)
	}
	artifact := CompletionAuditArtifact{}
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("unmarshal completion audit: %v", err)
	}
	if artifact.Ready || artifact.Status != "blocked" || len(artifact.Blockers) == 0 {
		t.Fatalf("expected blocked audit, got %#v", artifact)
	}
	last := session.ConversationEvents[len(session.ConversationEvents)-1]
	if last.Kind != conversationEventKindCompletionAudit || len(last.ArtifactRefs) != 2 || last.Entities["ready"] != "false" {
		t.Fatalf("expected completion audit event, got %#v", last)
	}
	if !strings.Contains(output.String(), "Completion is blocked") {
		t.Fatalf("expected blocked output, got %q", output.String())
	}
}

func TestCompletionAuditUsesDirectBlockerLabels(t *testing.T) {
	now := time.Now()
	session := NewSession(t.TempDir(), "provider", "model", "", "default")
	session.ActiveEditLoop = &EditLoopState{
		ID:        "loop-1",
		Goal:      "finish",
		Status:    editLoopStatusActive,
		StartedAt: now,
		UpdatedAt: now,
	}
	artifact := &CompletionAuditArtifact{}
	completionAuditEditLoop(session, artifact)
	item := completionAuditFindItem(artifact.Checklist, "edit_loop")
	if item == nil {
		t.Fatalf("expected edit loop audit item")
	}
	if item.Requirement != "Edit loop is still active" || item.Status != completionAuditStatusBlocked {
		t.Fatalf("expected direct edit-loop blocker label, got %#v", item)
	}

	reviewArtifact := &CompletionAuditArtifact{ChangedFiles: []string{"cmd/kernforge/goals.go"}}
	session.LastReviewRun = &ReviewRun{
		SchemaVersion: reviewSchemaVersion,
		ID:            "review-1",
		Freshness: ReviewFreshness{
			Stale:       true,
			StaleReason: "fingerprint changed",
		},
	}
	completionAuditReviewGate(t.TempDir(), session, reviewArtifact)
	reviewItem := completionAuditFindItem(reviewArtifact.Checklist, "review")
	if reviewItem == nil {
		t.Fatalf("expected review audit item")
	}
	if reviewItem.Requirement != "Latest review is stale" || reviewItem.Status != completionAuditStatusBlocked {
		t.Fatalf("expected direct stale-review blocker label, got %#v", reviewItem)
	}
}

func completionAuditFindItem(items []CompletionAuditItem, source string) *CompletionAuditItem {
	for i := range items {
		if items[i].Source == source {
			return &items[i]
		}
	}
	return nil
}

func TestCompletionAuditCommandPassesWhenArtifactsAndVerificationPass(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "report.md"), []byte("# Report\n"), 0o644); err != nil {
		t.Fatalf("write required artifact: %v", err)
	}
	now := time.Now()
	session := NewSession(root, "provider", "model", "", "default")
	session.TaskState = &TaskState{Goal: "Complete auditable work"}
	session.AcceptanceContract = &AcceptanceContract{
		ID:                   "contract-1",
		SourcePrompt:         "finish everything",
		RequiredArtifacts:    []string{"docs/report.md"},
		VerificationRequired: true,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	session.TaskGraph = &TaskGraph{Nodes: []TaskNode{{
		ID:     "plan-01",
		Title:  "Done",
		Kind:   "summary",
		Status: "completed",
	}}}
	session.LastVerification = &VerificationReport{
		GeneratedAt: now,
		Trigger:     "manual",
		Workspace:   root,
		Steps: []VerificationStep{{
			Label:   "go test",
			Command: "go test ./...",
			Status:  VerificationPassed,
			Output:  "ok",
		}},
	}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
	}
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleCompletionAuditCommand(""); err != nil {
		t.Fatalf("handleCompletionAuditCommand: %v", err)
	}

	jsonPath := filepath.Join(root, ".kernforge", "completion_audit", "latest.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read completion audit json: %v", err)
	}
	artifact := CompletionAuditArtifact{}
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("unmarshal completion audit: %v", err)
	}
	if !artifact.Ready || artifact.Status != "ready" || len(artifact.Blockers) != 0 || len(artifact.Warnings) != 0 {
		t.Fatalf("expected ready audit without warnings, got %#v", artifact)
	}
	if !strings.Contains(output.String(), "Completion audit is ready") {
		t.Fatalf("expected ready output, got %q", output.String())
	}
}

func TestCompletionAuditWarningsAreNotReady(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
	}
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleCompletionAuditCommand("finish with warning"); err != nil {
		t.Fatalf("handleCompletionAuditCommand: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".kernforge", "completion_audit", "latest.json"))
	if err != nil {
		t.Fatalf("read completion audit json: %v", err)
	}
	artifact := CompletionAuditArtifact{}
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("unmarshal completion audit: %v", err)
	}
	if artifact.Ready || artifact.Status != "needs_review" || len(artifact.Warnings) == 0 {
		t.Fatalf("expected warning audit to require review, got %#v", artifact)
	}
	event := session.ConversationEvents[len(session.ConversationEvents)-1]
	if event.Entities["ready"] != "false" || event.Entities["status"] != "needs_review" {
		t.Fatalf("expected not-ready event metadata, got %#v", event)
	}
	if !strings.Contains(output.String(), "Completion has warnings") {
		t.Fatalf("expected warning output, got %q", output.String())
	}
}

func TestCompletionAuditUsesVerificationHistoryForStandaloneAudit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Updated\n"), 0o644); err != nil {
		t.Fatalf("write readme change: %v", err)
	}
	history := &VerificationHistoryStore{
		Path:       filepath.Join(root, "verification-history.json"),
		MaxEntries: 10,
	}
	report := VerificationReport{
		GeneratedAt:  time.Now(),
		Trigger:      "manual",
		Mode:         VerificationFull,
		Workspace:    root,
		ChangedPaths: []string{"README.md"},
		Steps: []VerificationStep{{
			Label:   "go test",
			Command: "go test ./...",
			Status:  VerificationPassed,
			Output:  "ok",
		}},
	}
	if err := history.Append("previous-session", root, report); err != nil {
		t.Fatalf("append verification history: %v", err)
	}
	session := NewSession(root, "provider", "model", "", "default")
	rt := &runtimeState{
		session:       session,
		verifyHistory: history,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	artifact := rt.buildCompletionAuditArtifact(root, "finish tracked doc update")

	if !artifact.Ready || artifact.Status != "ready" {
		t.Fatalf("expected standalone audit to be ready, got %#v", artifact)
	}
	for _, requirement := range []string{
		completionAuditVerificationRequirement,
		"Changed files are accounted for",
		"Pre-final coding harness has no blockers",
	} {
		item, ok := completionAuditChecklistItem(artifact, requirement)
		if !ok {
			t.Fatalf("expected checklist item %q in %#v", requirement, artifact.Checklist)
		}
		if item.Status != completionAuditStatusPassed {
			t.Fatalf("expected %q to pass, got %#v", requirement, item)
		}
	}
}

func TestCompletionAuditDoesNotAcceptSkippedOnlyVerification(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.LastVerification = &VerificationReport{
		GeneratedAt: time.Now(),
		Trigger:     "manual",
		Mode:        VerificationFull,
		Workspace:   root,
		Steps: []VerificationStep{{
			Label:   "go test",
			Command: "go test ./...",
			Status:  VerificationSkipped,
			Output:  "Permission denied: interactive confirmation unavailable",
		}},
	}
	rt := &runtimeState{
		session: session,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	artifact := rt.buildCompletionAuditArtifact(root, "finish skipped verification case")

	item, ok := completionAuditChecklistItem(artifact, completionAuditVerificationRequirement)
	if !ok {
		t.Fatalf("expected verification checklist item in %#v", artifact.Checklist)
	}
	if item.Status != completionAuditStatusWarning {
		t.Fatalf("expected skipped-only verification to warn, got %#v", item)
	}
	if artifact.Ready || artifact.Status != "needs_review" {
		t.Fatalf("expected skipped-only verification to require review, got %#v", artifact)
	}
}

func TestCompletionAuditObjectiveEvidenceMapsGoalArtifacts(t *testing.T) {
	root := t.TempDir()
	specs := []string{
		"cmd/kernforge/recovery.go::handleRecoverCommand",
		"cmd/kernforge/recovery.go::RecoveryDiagnosis",
		"cmd/kernforge/recovery.go::RecoveryActionPlanItem",
		"cmd/kernforge/recovery.go::executeRecoverySafePlan",
		"cmd/kernforge/recovery.go::recoveryCommandAutoRunnable",
		"cmd/kernforge/recovery_test.go::TestRecoverCommandWritesRecoveryBrief",
		"cmd/kernforge/recovery_test.go::TestRecoverExecuteSafeRunsWhitelistedActionAndLogsStatus",
		"cmd/kernforge/recovery_test.go::TestRecoveryActionPlanMarksUnsafeCommandManualOnly",
		"cmd/kernforge/proactive_suggestions_test.go::TestFailedVerificationSuggestionUsesRecoverBrief",
		"cmd/kernforge/suggestion_execution_test.go::TestSafeSuggestionCommandExecutesRecoveryArtifacts",
		"cmd/kernforge/worktree.go::handleWorktreeListCommand",
		"cmd/kernforge/worktree.go::handleWorktreeEnterCommand",
		"cmd/kernforge/worktree.go::handleWorktreeAttachCommand",
		"cmd/kernforge/worktree_test.go::TestWorktreeListCommandShowsSessionSpecialistAndGitEntries",
		"cmd/kernforge/worktree_test.go::TestWorktreeEnterReattachesRecordedInactiveWorktree",
		"cmd/kernforge/worktree_test.go::TestWorktreeAttachExistingWorktree",
		"cmd/kernforge/delegation_handoff.go::handleDelegationHandoffCommand",
		"cmd/kernforge/delegation_handoff_test.go::TestDelegationHandoffImportRecordsResultAndMarksTasks",
		"cmd/kernforge/continuity.go::handleContinuityCommand",
		"cmd/kernforge/continuity.go::handleJobsCommand",
		"cmd/kernforge/continuity_test.go::TestContinuityCommandWritesRecoveryPacket",
		"cmd/kernforge/continuity_test.go::TestJobsCommandShowsAndPollsPersistedJobs",
		"cmd/kernforge/session_dashboard.go::handleSessionDashboardHTMLCommand",
		"cmd/kernforge/session_dashboard_test.go::TestSessionDashboardHTMLCommandWritesArtifactAndEvent",
		"cmd/kernforge/continuity.go::noteLocalShellCommand",
		"cmd/kernforge/completion_audit.go::handleCompletionAuditCommand",
		"cmd/kernforge/completion_audit_test.go::TestCompletionAuditWarningsAreNotReady",
		"cmd/kernforge/suggestion_execution.go::executeSafeSuggestionCommand",
		"cmd/kernforge/suggestion_execution_test.go::TestPRReviewChangedFilesParsesGitStatusShortForms",
		"README.md::/session recover [note]",
		"README.md::/session audit [note]",
		"FEATURE_USAGE_GUIDE.md::/session continuity",
		"cmd/kernforge/events.go::handleEventsCommand",
		"cmd/kernforge/events.go::renderSessionEventsJSONL",
		"cmd/kernforge/events_test.go::TestEventsExportWritesJSONLAndRecordsEvent",
		"cmd/kernforge/events_test.go::TestEventsTailPrintsRecentJSONL",
		"cmd/kernforge/conversation_events.go::conversationEventKindEventStream",
		"README.md::/session events [tail|export]",
		"FEATURE_USAGE_GUIDE.md::/session events export",
		"cmd/kernforge/goals.go::handleGoalCommand",
		"cmd/kernforge/goals.go::GoalState",
		"cmd/kernforge/goals.go::runGoalLoop",
		"cmd/kernforge/goals.go::completeGoalBySelector",
		"cmd/kernforge/goals_runtime.go::runGoalIteration",
		"cmd/kernforge/goals_runtime.go::runGoalSemanticReview",
		"cmd/kernforge/goals_runtime.go::buildGoalSemanticReviewPrompt",
		"cmd/kernforge/goals_runtime.go::goalAcceptanceContract",
		"cmd/kernforge/goals_runtime.go::evaluateGoalProgress",
		"cmd/kernforge/goals.go::buildGoalImplementationPrompt",
		"cmd/kernforge/goals.go::buildGoalReviewPrompt",
		"cmd/kernforge/goals_test.go::TestGoalStartFromMarkdownNoRunPersistsArtifacts",
		"cmd/kernforge/goals_test.go::TestGoalRunWithFakeAgentCompletesAfterAudit",
		"cmd/kernforge/goals_test.go::TestGoalReviewNeedsRevisionRunsRepairPass",
		"cmd/kernforge/goals_test.go::TestGoalTokenBudgetBlocksBeforeAgentPrompt",
		"cmd/kernforge/goals_test.go::TestGoalCompleteRequiresSemanticApproval",
		"README.md::/goal",
		"FEATURE_USAGE_GUIDE.md::Autonomous Goals",
	}
	for _, spec := range specs {
		path, symbol := splitCompletionAuditEvidenceSpec(spec)
		abs := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", abs, err)
		}
		f, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			t.Fatalf("open %s: %v", abs, err)
		}
		if _, err := f.WriteString(symbol + "\n"); err != nil {
			_ = f.Close()
			t.Fatalf("write %s: %v", abs, err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close %s: %v", abs, err)
		}
	}
	session := NewSession(root, "provider", "model", "", "default")
	rt := &runtimeState{
		session: session,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	artifact := rt.buildCompletionAuditArtifact(root, "natural failure recovery multi-agent worktree long-task continuity local edit command loop Codex autonomous goals")

	for _, requirement := range []string{
		"Natural failure recovery command and tests",
		"Multi-agent and worktree continuity surfaces",
		"Long-task continuity packet and job polling",
		"Local edit and command loop recovery gates",
		"Codex-style local event stream",
		"Autonomous goals command and loop",
	} {
		item, ok := completionAuditChecklistItem(artifact, requirement)
		if !ok {
			t.Fatalf("expected objective evidence item %q in %#v", requirement, artifact.Checklist)
		}
		if item.Status != completionAuditStatusPassed {
			t.Fatalf("expected %q to pass, got %#v", requirement, item)
		}
	}
}

func TestCompletionAuditObjectiveEvidenceSkipsKernforgeChecksOutsideKernforgeRepo(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	rt := &runtimeState{
		session: session,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	artifact := rt.buildCompletionAuditArtifact(root, "natural failure recovery long-task continuity local edit command loop Codex autonomous goals")

	for _, requirement := range []string{
		"Natural failure recovery command and tests",
		"Long-task continuity packet and job polling",
		"Local edit and command loop recovery gates",
		"Codex-style local event stream",
		"Autonomous goals command and loop",
	} {
		if item, ok := completionAuditChecklistItem(artifact, requirement); ok {
			t.Fatalf("expected kernforge-specific objective evidence to be skipped outside kernforge repo, got %#v", item)
		}
	}
	for _, blocker := range artifact.Blockers {
		if strings.Contains(blocker, "cmd/kernforge/") {
			t.Fatalf("expected no kernforge source blocker outside kernforge repo, got %q", blocker)
		}
	}
}

func completionAuditChecklistItem(artifact CompletionAuditArtifact, requirement string) (CompletionAuditItem, bool) {
	for _, item := range artifact.Checklist {
		if item.Requirement == requirement {
			return item, true
		}
	}
	return CompletionAuditItem{}, false
}
