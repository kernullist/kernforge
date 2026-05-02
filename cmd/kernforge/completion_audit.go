package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	completionAuditStatusPassed  = "passed"
	completionAuditStatusBlocked = "blocked"
	completionAuditStatusWarning = "warning"
	completionAuditStatusUnknown = "unknown"
)

type CompletionAuditArtifact struct {
	ID                string                `json:"id"`
	CreatedAt         time.Time             `json:"created_at"`
	SessionID         string                `json:"session_id,omitempty"`
	Workspace         string                `json:"workspace,omitempty"`
	BaseRoot          string                `json:"base_root,omitempty"`
	Branch            string                `json:"branch,omitempty"`
	Provider          string                `json:"provider,omitempty"`
	Model             string                `json:"model,omitempty"`
	ActiveFeatureID   string                `json:"active_feature_id,omitempty"`
	Objective         string                `json:"objective,omitempty"`
	Ready             bool                  `json:"ready"`
	Status            string                `json:"status"`
	Checklist         []CompletionAuditItem `json:"checklist,omitempty"`
	Blockers          []string              `json:"blockers,omitempty"`
	Warnings          []string              `json:"warnings,omitempty"`
	ChangedFiles      []string              `json:"changed_files,omitempty"`
	OpenTasks         []string              `json:"open_tasks,omitempty"`
	Verification      string                `json:"verification,omitempty"`
	RecentErrors      []string              `json:"recent_errors,omitempty"`
	BackgroundJobs    []string              `json:"background_jobs,omitempty"`
	BackgroundBundles []string              `json:"background_bundles,omitempty"`
	Worktrees         []string              `json:"worktrees,omitempty"`
	ArtifactRefs      []string              `json:"artifact_refs,omitempty"`
	NextCommands      []string              `json:"next_commands,omitempty"`
	SuggestedPrompt   string                `json:"suggested_prompt,omitempty"`
}

type CompletionAuditItem struct {
	Requirement string `json:"requirement"`
	Evidence    string `json:"evidence,omitempty"`
	Status      string `json:"status"`
	Source      string `json:"source,omitempty"`
}

func (rt *runtimeState) handleCompletionAuditCommand(args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = rt.session.WorkingDir
	}
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("workspace root is not configured")
	}
	artifact := rt.buildCompletionAuditArtifact(root, args)
	outDir := filepath.Join(root, userConfigDirName, "completion_audit")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	jsonPath := filepath.Join(outDir, "latest.json")
	mdPath := filepath.Join(outDir, "latest.md")
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(mdPath, []byte(renderCompletionAuditMarkdown(artifact)), 0o644); err != nil {
		return err
	}
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindCompletionAudit,
		Severity: completionAuditSeverity(artifact),
		Summary:  "completion audit generated: " + artifact.Status,
		ArtifactRefs: []string{
			mdPath,
			jsonPath,
		},
		Entities: map[string]string{
			"completion_audit": artifact.ID,
			"ready":            fmt.Sprintf("%t", artifact.Ready),
			"status":           artifact.Status,
			"blockers":         fmt.Sprintf("%d", len(artifact.Blockers)),
			"warnings":         fmt.Sprintf("%d", len(artifact.Warnings)),
		},
	})
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Generated completion audit: "+mdPath))
	if len(artifact.Blockers) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Completion is blocked:"))
		for _, blocker := range artifact.Blockers {
			fmt.Fprintln(rt.writer, "- "+blocker)
		}
	} else if len(artifact.Warnings) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Completion has warnings:"))
		for _, warning := range artifact.Warnings {
			fmt.Fprintln(rt.writer, "- "+warning)
		}
	} else {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Completion audit is ready."))
	}
	return nil
}

func (rt *runtimeState) buildCompletionAuditArtifact(root string, note string) CompletionAuditArtifact {
	now := time.Now()
	auditSession := rt.session
	if auditSession != nil && auditSession.LastVerification == nil && rt.verifyHistory != nil {
		if latest, ok := completionAuditLatestVerification(root, rt.verifyHistory); ok {
			copySession := *auditSession
			copySession.LastVerification = &latest
			auditSession = &copySession
		}
	}
	artifact := CompletionAuditArtifact{
		ID:              fmt.Sprintf("completion-audit-%s", now.Format("20060102-150405")),
		CreatedAt:       now,
		SessionID:       auditSession.ID,
		Workspace:       strings.TrimSpace(auditSession.WorkingDir),
		BaseRoot:        sessionBaseWorkingDir(auditSession),
		Branch:          delegationGitBranch(root),
		Provider:        auditSession.Provider,
		Model:           auditSession.Model,
		ActiveFeatureID: auditSession.ActiveFeatureID,
		Objective:       completionAuditObjective(auditSession, note),
	}
	artifact.ChangedFiles = delegationChangedFiles(root)
	artifact.OpenTasks = delegationOpenTasks(auditSession)
	artifact.Worktrees = continuityWorktreeSummaries(auditSession)
	if auditSession.LastVerification != nil {
		artifact.Verification = auditSession.LastVerification.SummaryLine()
	}
	rt.syncContinuityBackgroundJobs()
	artifact.BackgroundJobs = continuityBackgroundJobSummaries(auditSession.BackgroundJobs)
	artifact.BackgroundBundles = continuityBackgroundBundleSummaries(auditSession.BackgroundBundles)
	artifact.RecentErrors = continuityRecentErrors(auditSession, 6)
	artifact.ArtifactRefs = delegationArtifactRefs(auditSession.ConversationEvents, 16)
	completionAuditPopulateChecklist(root, auditSession, &artifact)
	completionAuditObjectiveEvidence(root, &artifact)
	completionAuditHarness(auditSession, &artifact)
	artifact.Blockers = normalizeTaskStateList(artifact.Blockers, 32)
	artifact.Warnings = normalizeTaskStateList(artifact.Warnings, 32)
	artifact.Ready = len(artifact.Blockers) == 0 && len(artifact.Warnings) == 0
	switch {
	case len(artifact.Blockers) > 0:
		artifact.Status = "blocked"
	case len(artifact.Warnings) > 0:
		artifact.Status = "needs_review"
	default:
		artifact.Status = "ready"
	}
	artifact.NextCommands = completionAuditNextCommands(artifact)
	artifact.SuggestedPrompt = completionAuditSuggestedPrompt(artifact)
	return artifact
}

func completionAuditLatestVerification(root string, history *VerificationHistoryStore) (VerificationReport, bool) {
	if history == nil {
		return VerificationReport{}, false
	}
	summary, err := history.Dashboard(root, false, nil, 1)
	if err != nil || len(summary.Recent) == 0 {
		return VerificationReport{}, false
	}
	report := summary.Recent[0].Report
	if len(report.Steps) == 0 {
		return VerificationReport{}, false
	}
	return report, true
}

func completionAuditObjective(session *Session, note string) string {
	if trimmed := strings.TrimSpace(note); trimmed != "" {
		return strings.Join(strings.Fields(trimmed), " ")
	}
	if session == nil {
		return ""
	}
	if session.TaskState != nil {
		if trimmed := strings.TrimSpace(session.TaskState.Goal); trimmed != "" {
			return strings.Join(strings.Fields(trimmed), " ")
		}
	}
	if session.AcceptanceContract != nil {
		contract := *session.AcceptanceContract
		contract.Normalize()
		if contract.SourcePrompt != "" {
			return contract.SourcePrompt
		}
	}
	if strings.TrimSpace(session.ActiveFeatureID) != "" {
		return "Finish active feature " + strings.TrimSpace(session.ActiveFeatureID)
	}
	return strings.TrimSpace(session.Name)
}

func completionAuditPopulateChecklist(root string, session *Session, artifact *CompletionAuditArtifact) {
	if session == nil || artifact == nil {
		return
	}
	completionAuditAddItem(artifact, CompletionAuditItem{
		Requirement: "Completion objective is explicit",
		Evidence:    valueOrDefault(artifact.Objective, "No objective recorded."),
		Status:      completionAuditStatusForBool(strings.TrimSpace(artifact.Objective) != ""),
		Source:      "session",
	})
	completionAuditAcceptance(root, session, artifact)
	completionAuditVerification(session, artifact)
	completionAuditTaskGraph(artifact)
	completionAuditEditLoop(session, artifact)
	completionAuditFailureRepair(session, artifact)
	completionAuditBackground(session, artifact)
	completionAuditChangedFiles(session, artifact)
	completionAuditRecentErrors(artifact)
}

func completionAuditAcceptance(root string, session *Session, artifact *CompletionAuditArtifact) {
	if session.AcceptanceContract == nil {
		if strings.TrimSpace(artifact.Objective) != "" {
			completionAuditAddItem(artifact, CompletionAuditItem{
				Requirement: "Acceptance contract captured",
				Evidence:    "Objective-derived audit contract: " + artifact.Objective,
				Status:      completionAuditStatusPassed,
				Source:      "acceptance",
			})
			return
		}
		completionAuditAddItem(artifact, CompletionAuditItem{
			Requirement: "Acceptance contract captured",
			Evidence:    "No acceptance contract is recorded for this session.",
			Status:      completionAuditStatusWarning,
			Source:      "acceptance",
		})
		return
	}
	contract := *session.AcceptanceContract
	contract.Normalize()
	completionAuditAddItem(artifact, CompletionAuditItem{
		Requirement: "Acceptance contract captured",
		Evidence:    valueOrDefault(contract.ID, "contract recorded"),
		Status:      completionAuditStatusPassed,
		Source:      "acceptance",
	})
	for _, expected := range contract.ExpectedBehaviors {
		completionAuditAddItem(artifact, CompletionAuditItem{
			Requirement: "Expected behavior: " + expected,
			Evidence:    "Tracked in acceptance contract; semantic proof depends on verification and review evidence.",
			Status:      completionAuditStatusWarning,
			Source:      "acceptance",
		})
	}
	for _, raw := range contract.RequiredArtifacts {
		abs, rel, ok := resolveClaimedArtifactPath(root, raw)
		if !ok {
			completionAuditAddItem(artifact, CompletionAuditItem{
				Requirement: "Required artifact exists: " + raw,
				Evidence:    "Artifact path is empty, outside the workspace, or invalid.",
				Status:      completionAuditStatusBlocked,
				Source:      "acceptance",
			})
			continue
		}
		if info, err := os.Stat(abs); err != nil {
			completionAuditAddItem(artifact, CompletionAuditItem{
				Requirement: "Required artifact exists: " + rel,
				Evidence:    err.Error(),
				Status:      completionAuditStatusBlocked,
				Source:      "acceptance",
			})
		} else {
			kind := "file"
			if info.IsDir() {
				kind = "directory"
			}
			completionAuditAddItem(artifact, CompletionAuditItem{
				Requirement: "Required artifact exists: " + rel,
				Evidence:    fmt.Sprintf("%s size=%d", kind, info.Size()),
				Status:      completionAuditStatusPassed,
				Source:      "acceptance",
			})
		}
	}
	if contract.VerificationRequired {
		status := completionAuditStatusPassed
		evidence := "Verification requirement is recorded and checked below."
		if session.LastVerification == nil {
			status = completionAuditStatusBlocked
			evidence = "Verification is required but no verification report is recorded."
		} else if session.LastVerification.HasFailures() {
			status = completionAuditStatusBlocked
			evidence = "Verification is required but the latest verification has failures."
		} else if !completionAuditVerificationHasPassedStep(*session.LastVerification) {
			status = completionAuditStatusBlocked
			evidence = "Verification is required but the latest verification did not execute any passing step."
		}
		completionAuditAddItem(artifact, CompletionAuditItem{
			Requirement: "Required verification completed",
			Evidence:    evidence,
			Status:      status,
			Source:      "acceptance",
		})
	}
}

func completionAuditVerification(session *Session, artifact *CompletionAuditArtifact) {
	if session.LastVerification == nil {
		status := completionAuditStatusUnknown
		evidence := "No verification report is recorded."
		if len(artifact.ChangedFiles) > 0 {
			status = completionAuditStatusWarning
			evidence = "Workspace has changed files but no latest verification report."
		}
		completionAuditAddItem(artifact, CompletionAuditItem{
			Requirement: "Latest verification has no failures",
			Evidence:    evidence,
			Status:      status,
			Source:      "verification",
		})
		return
	}
	report := *session.LastVerification
	if report.HasFailures() {
		evidence := compactPromptSection(firstNonBlankString(report.FailureSummary(), report.RenderShort()), 500)
		completionAuditAddItem(artifact, CompletionAuditItem{
			Requirement: "Latest verification has no failures",
			Evidence:    evidence,
			Status:      completionAuditStatusBlocked,
			Source:      "verification",
		})
		return
	}
	completionAuditAddItem(artifact, CompletionAuditItem{
		Requirement: "Latest verification has no failures",
		Evidence:    completionAuditVerificationEvidence(report),
		Status:      completionAuditVerificationStatus(report),
		Source:      "verification",
	})
}

func completionAuditVerificationStatus(report VerificationReport) string {
	if report.HasFailures() {
		return completionAuditStatusBlocked
	}
	if !completionAuditVerificationHasPassedStep(report) {
		return completionAuditStatusWarning
	}
	return completionAuditStatusPassed
}

func completionAuditVerificationEvidence(report VerificationReport) string {
	if completionAuditVerificationHasPassedStep(report) {
		return report.SummaryLine()
	}
	return report.SummaryLine() + "; no verification step executed successfully"
}

func completionAuditVerificationHasPassedStep(report VerificationReport) bool {
	for _, step := range report.Steps {
		if step.Status == VerificationPassed {
			return true
		}
	}
	return false
}

func completionAuditTaskGraph(artifact *CompletionAuditArtifact) {
	if len(artifact.OpenTasks) == 0 {
		completionAuditAddItem(artifact, CompletionAuditItem{
			Requirement: "No open task graph nodes remain",
			Evidence:    "No open tasks recorded.",
			Status:      completionAuditStatusPassed,
			Source:      "task_graph",
		})
		return
	}
	completionAuditAddItem(artifact, CompletionAuditItem{
		Requirement: "No open task graph nodes remain",
		Evidence:    strings.Join(limitStrings(artifact.OpenTasks, 4), " | "),
		Status:      completionAuditStatusBlocked,
		Source:      "task_graph",
	})
}

func completionAuditEditLoop(session *Session, artifact *CompletionAuditArtifact) {
	loop := latestCompletionAuditEditLoop(session)
	if loop == nil {
		completionAuditAddItem(artifact, CompletionAuditItem{
			Requirement: "Edit loop is closed or not active",
			Evidence:    "No edit loop state recorded.",
			Status:      completionAuditStatusPassed,
			Source:      "edit_loop",
		})
		return
	}
	loop.Normalize()
	status := completionAuditStatusPassed
	evidence := valueOrDefault(loop.Status, "unknown")
	if !editLoopClosedStatus(loop.Status) {
		status = completionAuditStatusBlocked
		evidence = "Active edit loop is still " + valueOrUnset(loop.Status)
	} else if len(loop.RemainingRisks) > 0 {
		status = completionAuditStatusWarning
		evidence = "Remaining risks: " + strings.Join(limitStrings(loop.RemainingRisks, 4), " | ")
	}
	if strings.EqualFold(loop.VerificationStatus, string(VerificationFailed)) {
		status = completionAuditStatusBlocked
		evidence = "Edit loop verification failed: " + valueOrDefault(loop.VerificationSummary, loop.VerificationBundleID)
	}
	completionAuditAddItem(artifact, CompletionAuditItem{
		Requirement: "Edit loop is closed or not active",
		Evidence:    evidence,
		Status:      status,
		Source:      "edit_loop",
	})
}

func latestCompletionAuditEditLoop(session *Session) *EditLoopState {
	if session == nil {
		return nil
	}
	if session.ActiveEditLoop != nil {
		return session.ActiveEditLoop
	}
	if len(session.EditLoops) == 0 {
		return nil
	}
	return &session.EditLoops[0]
}

func completionAuditFailureRepair(session *Session, artifact *CompletionAuditArtifact) {
	if session.ActiveFailureRepair == nil {
		completionAuditAddItem(artifact, CompletionAuditItem{
			Requirement: "No active failure repair remains",
			Evidence:    "No active failure repair recorded.",
			Status:      completionAuditStatusPassed,
			Source:      "failure_repair",
		})
		return
	}
	completionAuditAddItem(artifact, CompletionAuditItem{
		Requirement: "No active failure repair remains",
		Evidence:    compactPromptSection(session.ActiveFailureRepair.RenderPromptSection(), 500),
		Status:      completionAuditStatusBlocked,
		Source:      "failure_repair",
	})
}

func completionAuditBackground(session *Session, artifact *CompletionAuditArtifact) {
	if len(session.BackgroundJobs) == 0 && len(session.BackgroundBundles) == 0 {
		completionAuditAddItem(artifact, CompletionAuditItem{
			Requirement: "No blocking background jobs or bundles remain",
			Evidence:    "No background jobs or bundles recorded.",
			Status:      completionAuditStatusPassed,
			Source:      "jobs",
		})
		return
	}
	status := completionAuditStatusPassed
	evidence := []string{}
	for _, job := range session.BackgroundJobs {
		job.Normalize()
		jobStatus := strings.TrimSpace(strings.ToLower(job.Status))
		if completionAuditBlockingBackgroundStatus(jobStatus) {
			status = completionAuditStatusBlocked
			evidence = append(evidence, jobSupervisorJobSummary(job))
		}
	}
	for _, bundle := range session.BackgroundBundles {
		bundle.Normalize()
		bundleStatus := strings.TrimSpace(strings.ToLower(bundle.Status))
		if completionAuditBlockingBackgroundStatus(bundleStatus) {
			status = completionAuditStatusBlocked
			evidence = append(evidence, jobSupervisorBundleSummary(bundle))
		}
	}
	if len(evidence) == 0 {
		evidence = append(evidence, fmt.Sprintf("jobs=%d bundles=%d; no running, failed, or stale status found", len(session.BackgroundJobs), len(session.BackgroundBundles)))
	}
	completionAuditAddItem(artifact, CompletionAuditItem{
		Requirement: "No blocking background jobs or bundles remain",
		Evidence:    strings.Join(limitStrings(evidence, 6), " | "),
		Status:      status,
		Source:      "jobs",
	})
}

func completionAuditBlockingBackgroundStatus(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "running", "failed", "stale":
		return true
	default:
		return false
	}
}

func completionAuditHarness(session *Session, artifact *CompletionAuditArtifact) {
	if session.LastCodingHarnessReport == nil {
		if completionAuditStandaloneHarnessApproves(*artifact) {
			completionAuditAddItem(artifact, CompletionAuditItem{
				Requirement: "Pre-final coding harness has no blockers",
				Evidence:    "Standalone completion audit checked objective, verification, task graph, edit loop, failure repair, background jobs, recent errors, changed files, and objective artifact evidence.",
				Status:      completionAuditStatusPassed,
				Source:      "coding_harness",
			})
			return
		}
		completionAuditAddItem(artifact, CompletionAuditItem{
			Requirement: "Pre-final coding harness has no blockers",
			Evidence:    "No coding harness report is recorded.",
			Status:      completionAuditStatusWarning,
			Source:      "coding_harness",
		})
		return
	}
	report := *session.LastCodingHarnessReport
	report.Normalize()
	findings := report.allFindings()
	blockers := []string{}
	warnings := []string{}
	for _, finding := range findings {
		item := strings.TrimSpace(finding.Title)
		if item == "" {
			item = strings.TrimSpace(finding.Detail)
		}
		switch strings.TrimSpace(strings.ToLower(finding.Severity)) {
		case "blocker":
			blockers = append(blockers, item)
		case "warning":
			warnings = append(warnings, item)
		}
	}
	if !report.Approved || len(blockers) > 0 {
		completionAuditAddItem(artifact, CompletionAuditItem{
			Requirement: "Pre-final coding harness has no blockers",
			Evidence:    valueOrDefault(strings.Join(limitStrings(blockers, 5), " | "), "Harness did not approve the final state."),
			Status:      completionAuditStatusBlocked,
			Source:      "coding_harness",
		})
		return
	}
	if len(warnings) > 0 {
		completionAuditAddItem(artifact, CompletionAuditItem{
			Requirement: "Pre-final coding harness has no blockers",
			Evidence:    strings.Join(limitStrings(warnings, 5), " | "),
			Status:      completionAuditStatusWarning,
			Source:      "coding_harness",
		})
		return
	}
	completionAuditAddItem(artifact, CompletionAuditItem{
		Requirement: "Pre-final coding harness has no blockers",
		Evidence:    "Harness approved the latest final-state report.",
		Status:      completionAuditStatusPassed,
		Source:      "coding_harness",
	})
}

func completionAuditStandaloneHarnessApproves(artifact CompletionAuditArtifact) bool {
	if strings.TrimSpace(artifact.Objective) == "" {
		return false
	}
	for _, item := range artifact.Checklist {
		if item.Source == "coding_harness" {
			continue
		}
		if item.Status != completionAuditStatusPassed {
			return false
		}
	}
	return true
}

func completionAuditChangedFiles(session *Session, artifact *CompletionAuditArtifact) {
	if len(artifact.ChangedFiles) == 0 {
		completionAuditAddItem(artifact, CompletionAuditItem{
			Requirement: "Changed files are accounted for",
			Evidence:    "No changed files detected.",
			Status:      completionAuditStatusPassed,
			Source:      "git",
		})
		return
	}
	status := completionAuditStatusWarning
	evidence := strings.Join(limitStrings(artifact.ChangedFiles, 8), ", ")
	if session != nil && session.LastVerification != nil && completionAuditVerificationStatus(*session.LastVerification) == completionAuditStatusPassed {
		status = completionAuditStatusPassed
		evidence = "Changed files listed for final review; latest verification: " + session.LastVerification.SummaryLine() + "; files: " + evidence
	}
	completionAuditAddItem(artifact, CompletionAuditItem{
		Requirement: "Changed files are accounted for",
		Evidence:    evidence,
		Status:      status,
		Source:      "git",
	})
}

func completionAuditRecentErrors(artifact *CompletionAuditArtifact) {
	if len(artifact.RecentErrors) == 0 {
		completionAuditAddItem(artifact, CompletionAuditItem{
			Requirement: "No recent runtime errors are unresolved",
			Evidence:    "No recent provider, tool, or command errors recorded.",
			Status:      completionAuditStatusPassed,
			Source:      "conversation_events",
		})
		return
	}
	completionAuditAddItem(artifact, CompletionAuditItem{
		Requirement: "No recent runtime errors are unresolved",
		Evidence:    strings.Join(limitStrings(artifact.RecentErrors, 4), " | "),
		Status:      completionAuditStatusWarning,
		Source:      "conversation_events",
	})
}

func completionAuditObjectiveEvidence(root string, artifact *CompletionAuditArtifact) {
	if artifact == nil {
		return
	}
	objective := strings.ToLower(strings.TrimSpace(artifact.Objective))
	if objective == "" {
		return
	}
	if containsAny(objective, "failure recovery", "natural failure", "recover", "recovery", "실패", "복구") {
		completionAuditFeatureEvidence(root, artifact, "Natural failure recovery command and tests", []string{
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
		})
	}
	if containsAny(objective, "worktree", "multi-agent", "multi agent", "specialist", "워크트리", "멀티") {
		completionAuditFeatureEvidence(root, artifact, "Multi-agent and worktree continuity surfaces", []string{
			"cmd/kernforge/worktree.go::handleWorktreeListCommand",
			"cmd/kernforge/worktree.go::handleWorktreeEnterCommand",
			"cmd/kernforge/worktree.go::handleWorktreeAttachCommand",
			"cmd/kernforge/worktree_test.go::TestWorktreeListCommandShowsSessionSpecialistAndGitEntries",
			"cmd/kernforge/worktree_test.go::TestWorktreeEnterReattachesRecordedInactiveWorktree",
			"cmd/kernforge/worktree_test.go::TestWorktreeAttachExistingWorktree",
			"cmd/kernforge/delegation_handoff.go::handleDelegationHandoffCommand",
			"cmd/kernforge/delegation_handoff_test.go::TestDelegationHandoffImportRecordsResultAndMarksTasks",
		})
	}
	if containsAny(objective, "continuity", "long-task", "long task", "장기", "이어") {
		completionAuditFeatureEvidence(root, artifact, "Long-task continuity packet and job polling", []string{
			"cmd/kernforge/continuity.go::handleContinuityCommand",
			"cmd/kernforge/continuity.go::handleJobsCommand",
			"cmd/kernforge/continuity_test.go::TestContinuityCommandWritesRecoveryPacket",
			"cmd/kernforge/continuity_test.go::TestJobsCommandShowsAndPollsPersistedJobs",
			"cmd/kernforge/session_dashboard.go::handleSessionDashboardHTMLCommand",
			"cmd/kernforge/session_dashboard_test.go::TestSessionDashboardHTMLCommandWritesArtifactAndEvent",
		})
	}
	if containsAny(objective, "local edit", "command loop", "shell", "명령", "편집", "codex") {
		completionAuditFeatureEvidence(root, artifact, "Local edit and command loop recovery gates", []string{
			"cmd/kernforge/continuity.go::noteLocalShellCommand",
			"cmd/kernforge/completion_audit.go::handleCompletionAuditCommand",
			"cmd/kernforge/completion_audit_test.go::TestCompletionAuditWarningsAreNotReady",
			"cmd/kernforge/suggestion_execution.go::executeSafeSuggestionCommand",
			"cmd/kernforge/suggestion_execution_test.go::TestPRReviewChangedFilesParsesGitStatusShortForms",
			"README.md::/recover [note]",
			"README.md::/completion-audit [note]",
			"FEATURE_USAGE_GUIDE.md::/continuity",
		})
	}
	if containsAny(objective, "codex", "harness", "app server", "event stream", "jsonl", "하네스") {
		completionAuditFeatureEvidence(root, artifact, "Codex-style local event stream", []string{
			"cmd/kernforge/events.go::handleEventsCommand",
			"cmd/kernforge/events.go::renderSessionEventsJSONL",
			"cmd/kernforge/events_test.go::TestEventsExportWritesJSONLAndRecordsEvent",
			"cmd/kernforge/events_test.go::TestEventsTailPrintsRecentJSONL",
			"cmd/kernforge/conversation_events.go::conversationEventKindEventStream",
			"README.md::/events [tail|export]",
			"FEATURE_USAGE_GUIDE.md::/events export",
		})
	}
	if containsAny(objective, "goal", "goals", "autonomous", "자율", "목표") {
		completionAuditFeatureEvidence(root, artifact, "Autonomous goals command and loop", []string{
			"cmd/kernforge/goals.go::handleGoalCommand",
			"cmd/kernforge/goals.go::GoalState",
			"cmd/kernforge/goals.go::runGoalLoop",
			"cmd/kernforge/goals.go::buildGoalImplementationPrompt",
			"cmd/kernforge/goals.go::buildGoalReviewPrompt",
			"cmd/kernforge/goals_test.go::TestGoalStartFromMarkdownNoRunPersistsArtifacts",
			"cmd/kernforge/goals_test.go::TestGoalRunWithFakeAgentCompletesAfterAudit",
			"README.md::/goal",
			"FEATURE_USAGE_GUIDE.md::Autonomous Goals",
		})
	}
}

func completionAuditFeatureEvidence(root string, artifact *CompletionAuditArtifact, requirement string, specs []string) {
	evidence := []string{}
	missing := []string{}
	for _, spec := range specs {
		path, symbol := splitCompletionAuditEvidenceSpec(spec)
		abs := filepath.Join(root, filepath.FromSlash(path))
		data, err := os.ReadFile(abs)
		if err != nil {
			missing = append(missing, path+": "+err.Error())
			continue
		}
		if symbol != "" && !strings.Contains(string(data), symbol) {
			missing = append(missing, spec)
			continue
		}
		evidence = append(evidence, spec)
	}
	status := completionAuditStatusPassed
	text := "Evidence: " + strings.Join(limitStrings(evidence, 6), " | ")
	if len(missing) > 0 {
		status = completionAuditStatusBlocked
		text = "Missing: " + strings.Join(limitStrings(missing, 6), " | ")
		if len(evidence) > 0 {
			text += " ; present: " + strings.Join(limitStrings(evidence, 4), " | ")
		}
	}
	completionAuditAddItem(artifact, CompletionAuditItem{
		Requirement: requirement,
		Evidence:    text,
		Status:      status,
		Source:      "objective_artifacts",
	})
}

func splitCompletionAuditEvidenceSpec(spec string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(spec), "::", 2)
	path := strings.TrimSpace(parts[0])
	if len(parts) == 1 {
		return path, ""
	}
	return path, strings.TrimSpace(parts[1])
}

func completionAuditAddItem(artifact *CompletionAuditArtifact, item CompletionAuditItem) {
	if artifact == nil {
		return
	}
	item.Requirement = strings.Join(strings.Fields(strings.TrimSpace(item.Requirement)), " ")
	item.Evidence = compactPromptSection(strings.TrimSpace(item.Evidence), 700)
	item.Status = strings.TrimSpace(strings.ToLower(item.Status))
	item.Source = strings.TrimSpace(strings.ToLower(item.Source))
	if item.Status == "" {
		item.Status = completionAuditStatusUnknown
	}
	if item.Requirement == "" {
		return
	}
	artifact.Checklist = append(artifact.Checklist, item)
	line := item.Requirement
	if item.Evidence != "" {
		line += ": " + item.Evidence
	}
	switch item.Status {
	case completionAuditStatusBlocked:
		artifact.Blockers = append(artifact.Blockers, line)
	case completionAuditStatusWarning, completionAuditStatusUnknown:
		artifact.Warnings = append(artifact.Warnings, line)
	}
}

func completionAuditStatusForBool(ok bool) string {
	if ok {
		return completionAuditStatusPassed
	}
	return completionAuditStatusBlocked
}

func completionAuditNextCommands(artifact CompletionAuditArtifact) []string {
	commands := []string{
		"/session-dashboard-html",
		"/continuity continue from completion audit",
	}
	if len(artifact.BackgroundJobs) > 0 || len(artifact.BackgroundBundles) > 0 {
		commands = append(commands, "/jobs status")
	}
	if len(artifact.OpenTasks) > 0 {
		commands = append(commands, "/tasks")
		commands = append(commands, "/handoff continue from completion audit")
	}
	if completionAuditNeedsVerification(artifact) {
		commands = append(commands, "/verify")
	}
	if len(artifact.Blockers) > 0 {
		commands = append(commands, "/completion-audit after blockers are fixed")
	}
	return normalizeTaskStateList(commands, 10)
}

func completionAuditNeedsVerification(artifact CompletionAuditArtifact) bool {
	if strings.TrimSpace(artifact.Verification) == "" && len(artifact.ChangedFiles) > 0 {
		return true
	}
	for _, item := range artifact.Checklist {
		if item.Source != "verification" && item.Source != "acceptance" {
			continue
		}
		if item.Status == completionAuditStatusBlocked || item.Status == completionAuditStatusWarning {
			return true
		}
	}
	return false
}

func completionAuditSuggestedPrompt(artifact CompletionAuditArtifact) string {
	var b strings.Builder
	b.WriteString("Continue from the completion audit. ")
	if artifact.Objective != "" {
		b.WriteString("Objective: ")
		b.WriteString(artifact.Objective)
		b.WriteString(". ")
	}
	if len(artifact.Blockers) > 0 {
		b.WriteString("Resolve the listed blockers before claiming completion. ")
	} else if len(artifact.Warnings) > 0 {
		b.WriteString("Review the warnings and decide whether they are acceptable residual risk. ")
	} else {
		b.WriteString("The audit has no blockers or warnings; preserve the evidence when finalizing. ")
	}
	b.WriteString("Do not claim verification, artifacts, or task completion beyond the recorded evidence.")
	return strings.TrimSpace(b.String())
}

func completionAuditSeverity(artifact CompletionAuditArtifact) string {
	if len(artifact.Blockers) > 0 || len(artifact.Warnings) > 0 {
		return conversationSeverityWarn
	}
	return conversationSeverityInfo
}

func renderCompletionAuditMarkdown(artifact CompletionAuditArtifact) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Completion Audit\n\n")
	fmt.Fprintf(&b, "ID: %s\n", artifact.ID)
	fmt.Fprintf(&b, "Generated: %s\n", artifact.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Status: %s\n", artifact.Status)
	fmt.Fprintf(&b, "Ready: %t\n", artifact.Ready)
	fmt.Fprintf(&b, "Session: %s\n", artifact.SessionID)
	fmt.Fprintf(&b, "Workspace: %s\n", valueOrUnset(artifact.Workspace))
	fmt.Fprintf(&b, "Base root: %s\n", valueOrUnset(artifact.BaseRoot))
	fmt.Fprintf(&b, "Branch: %s\n", valueOrDefault(artifact.Branch, "unknown"))
	fmt.Fprintf(&b, "Provider/model: %s / %s\n", valueOrUnset(artifact.Provider), valueOrUnset(artifact.Model))
	if artifact.ActiveFeatureID != "" {
		fmt.Fprintf(&b, "Active feature: %s\n", artifact.ActiveFeatureID)
	}
	if artifact.Objective != "" {
		fmt.Fprintf(&b, "\n## Objective\n\n%s\n", artifact.Objective)
	}
	fmt.Fprintf(&b, "\n## Checklist\n\n")
	if len(artifact.Checklist) == 0 {
		fmt.Fprintf(&b, "- [unknown] No checklist items generated.\n")
	} else {
		for _, item := range artifact.Checklist {
			line := fmt.Sprintf("- [%s] %s", valueOrDefault(item.Status, completionAuditStatusUnknown), item.Requirement)
			if item.Source != "" {
				line += " (" + item.Source + ")"
			}
			if item.Evidence != "" {
				line += ": " + item.Evidence
			}
			fmt.Fprintln(&b, line)
		}
	}
	writeDelegationList(&b, "Blockers", artifact.Blockers, "No hard blockers recorded.")
	writeDelegationList(&b, "Warnings", artifact.Warnings, "No warnings recorded.")
	writeDelegationList(&b, "Changed Files", artifact.ChangedFiles, "No changed files detected.")
	writeDelegationList(&b, "Open Tasks", artifact.OpenTasks, "No open task graph nodes.")
	if artifact.Verification != "" {
		fmt.Fprintf(&b, "\n## Verification\n\n%s\n", artifact.Verification)
	}
	writeDelegationList(&b, "Background Jobs", artifact.BackgroundJobs, "No background jobs recorded.")
	writeDelegationList(&b, "Background Bundles", artifact.BackgroundBundles, "No background bundles recorded.")
	writeDelegationList(&b, "Recent Errors", artifact.RecentErrors, "No recent runtime errors recorded.")
	writeDelegationList(&b, "Worktrees", artifact.Worktrees, "No isolated or specialist worktrees recorded.")
	writeDelegationList(&b, "Artifact Refs", artifact.ArtifactRefs, "No artifact refs recorded.")
	writeDelegationList(&b, "Next Commands", artifact.NextCommands, "No next commands suggested.")
	fmt.Fprintf(&b, "\n## Suggested Prompt\n\n%s\n", artifact.SuggestedPrompt)
	return strings.TrimSpace(b.String())
}
