package main

import (
	"fmt"
	"strings"
	"time"
)

const (
	runtimeGateStatusReady       = "ready"
	runtimeGateStatusNeedsReview = "needs_review"
	runtimeGateStatusBlocked     = "blocked"

	runtimeGateActionReview          = "review"
	runtimeGateActionFinalAnswer     = "final_answer"
	runtimeGateActionGitWrite        = "git_write"
	runtimeGateActionMCPWrite        = "mcp_write"
	runtimeGateActionCompletionAudit = "completion_audit"
)

type RuntimeGateLedger struct {
	ID                    string                           `json:"id,omitempty"`
	GeneratedAt           time.Time                        `json:"generated_at,omitempty"`
	Action                string                           `json:"action,omitempty"`
	Status                string                           `json:"status,omitempty"`
	Ready                 bool                             `json:"ready"`
	Branch                string                           `json:"branch,omitempty"`
	ReviewRunID           string                           `json:"review_run_id,omitempty"`
	PatchTransactionID    string                           `json:"patch_transaction_id,omitempty"`
	VerificationReportID  string                           `json:"verification_report_id,omitempty"`
	CompletionAuditID     string                           `json:"completion_audit_id,omitempty"`
	FinalAnswerReviewID   string                           `json:"final_answer_review_id,omitempty"`
	ReviewTransaction     ReviewTransaction                `json:"review_transaction,omitempty"`
	ChangedPaths          []string                         `json:"changed_paths,omitempty"`
	Blockers              []string                         `json:"blockers,omitempty"`
	Warnings              []string                         `json:"warnings,omitempty"`
	StaleReasons          []string                         `json:"stale_reasons,omitempty"`
	Waivers               []string                         `json:"waivers,omitempty"`
	NextCommands          []ReviewNextCommand              `json:"next_commands,omitempty"`
	ReviewObservability   *ReviewDecisionObservability     `json:"review_observability,omitempty"`
	FinalAnswerCorrection *FinalAnswerCorrectionVisibility `json:"final_answer_correction,omitempty"`
}

type ReviewTransaction struct {
	ID               string              `json:"id,omitempty"`
	ReviewRunID      string              `json:"review_run_id,omitempty"`
	Status           string              `json:"status,omitempty"`
	Verdict          string              `json:"verdict,omitempty"`
	Fresh            bool                `json:"fresh"`
	Stale            bool                `json:"stale,omitempty"`
	StaleReasons     []string            `json:"stale_reasons,omitempty"`
	BlockingFindings []string            `json:"blocking_findings,omitempty"`
	WarningFindings  []string            `json:"warning_findings,omitempty"`
	Waivers          []string            `json:"waivers,omitempty"`
	NextCommands     []ReviewNextCommand `json:"next_commands,omitempty"`
}

func buildRuntimeGateLedger(root string, session *Session, action string) RuntimeGateLedger {
	return buildRuntimeGateLedgerWithReview(root, session, action, nil, "")
}

func buildRuntimeGateLedgerWithCompletionAudit(root string, session *Session, action string, completionAuditID string) RuntimeGateLedger {
	return buildRuntimeGateLedgerWithReview(root, session, action, nil, completionAuditID)
}

func buildRuntimeGateLedgerWithReview(root string, session *Session, action string, review *ReviewRun, completionAuditID string) RuntimeGateLedger {
	now := time.Now()
	action = normalizeRuntimeGateAction(action)
	ledger := RuntimeGateLedger{
		ID:          fmt.Sprintf("runtime-gate-%s", now.Format("20060102-150405.000")),
		GeneratedAt: now,
		Action:      action,
		Branch:      delegationGitBranch(root),
	}
	ledger.ChangedPaths = runtimeGateChangedPathsForAction(root, session, action)
	documentArtifactOnly := runtimeGateDocumentArtifactOnly(session, action, ledger.ChangedPaths)
	if tx := runtimeGatePatchTransactionForAction(session, action); tx != nil {
		ledger.PatchTransactionID = strings.TrimSpace(tx.ID)
	}
	runtimeGateAttachPatchTransactionScope(session, action, &ledger)
	if session != nil && session.LastVerification != nil {
		ledger.VerificationReportID = runtimeGateVerificationReportID(*session.LastVerification)
	}
	ledger.CompletionAuditID = firstNonBlankString(completionAuditID, latestRuntimeGateCompletionAuditID(session))
	ledger.FinalAnswerReviewID = runtimeGateFinalAnswerReviewID(session)

	reviewRun, ok := runtimeGateReviewRun(root, session, review)
	var observedReview *ReviewRun
	if documentArtifactOnly {
		// Generated document artifacts are guarded by deterministic artifact
		// quality checks, not the code-review freshness ledger.
	} else if ok {
		runtimeGateAttachReview(root, &ledger, reviewRun)
		observedReview = &reviewRun
	} else if len(ledger.ChangedPaths) > 0 {
		message := "no latest review run covers current changed files"
		if runtimeGateActionRequiresReview(action) {
			ledger.Blockers = append(ledger.Blockers, message)
		} else if !strings.EqualFold(action, runtimeGateActionCompletionAudit) || !runtimeGateVerificationPassed(session) {
			ledger.Warnings = append(ledger.Warnings, message)
		}
		if runtimeGateActionRequiresReview(action) || !strings.EqualFold(action, runtimeGateActionCompletionAudit) || !runtimeGateVerificationPassed(session) {
			ledger.NextCommands = appendRuntimeGateNextCommand(ledger.NextCommands, ReviewNextCommand{
				ID:             "review",
				Command:        "/review",
				Reason:         "current changed files do not have a review transaction",
				Safety:         "read_only",
				When:           "before final answer or git write",
				ClientHint:     "Run /review to create a fresh review transaction.",
				ExpectedResult: "A latest review run is linked into the runtime gate ledger.",
			})
		}
	}

	runtimeGateAttachVerification(session, &ledger)
	runtimeGateAttachCodingHarness(session, &ledger)
	ledger.Normalize()
	if observedReview != nil {
		var report *CodingHarnessReport
		if session != nil && session.LastCodingHarnessReport != nil {
			report = session.LastCodingHarnessReport
		}
		ledger.ReviewObservability = buildReviewDecisionObservability(observedReview, &ledger, report)
		ledger.Normalize()
	}
	return ledger
}

func runtimeGateDocumentArtifactOnly(session *Session, action string, changedPaths []string) bool {
	action = normalizeRuntimeGateAction(action)
	switch action {
	case runtimeGateActionFinalAnswer, runtimeGateActionCompletionAudit:
	default:
		return false
	}
	if runtimeGateLatestUserStartsFreshNonDocumentArtifactTurn(session) {
		return false
	}
	if generatedDocumentArtifactGateAcceptedForRequest(session, "", changedPaths) {
		return true
	}
	if sessionHasApprovedDocumentArtifactOnlyHarness(session) {
		return true
	}
	if sessionHasDocumentArtifactQualityAcceptedHarness(session) &&
		runtimeGateHasGeneratedDocumentArtifactContext(session, changedPaths) {
		return true
	}
	return changedPathsAreGeneratedDocumentArtifacts(session, "", changedPaths)
}

func runtimeGateLatestUserStartsFreshNonDocumentArtifactTurn(session *Session) bool {
	if session == nil {
		return false
	}
	latestUser := strings.TrimSpace(baseUserQueryText(latestExternalOrUserMessageText(session.Messages)))
	if latestUser == "" || looksLikeInternalReviewFeedbackUserMessage(latestUser) {
		return false
	}
	return generatedDocumentArtifactRequestStartsFreshNonArtifactTurn(latestUser)
}

func runtimeGateHasGeneratedDocumentArtifactContext(session *Session, changedPaths []string) bool {
	if session == nil {
		return false
	}
	normalizedChangedPaths := normalizeTaskStateList(changedPaths, 64)
	if len(normalizedChangedPaths) > 0 {
		return changedPathsMatchDocumentArtifactQuality(session, normalizedChangedPaths) ||
			changedPathsAreGeneratedDocumentArtifacts(session, "", normalizedChangedPaths)
	}
	latestUser := strings.TrimSpace(baseUserQueryText(latestExternalOrUserMessageText(session.Messages)))
	if latestUser != "" && !looksLikeInternalReviewFeedbackUserMessage(latestUser) {
		return preWriteRequestLooksLikeGeneratedDocumentArtifact(latestUser)
	}
	if session.AcceptanceContract != nil &&
		generatedDocumentArtifactRequestContextForTurn(session, session.AcceptanceContract.SourcePrompt) != "" {
		return true
	}
	if session.TaskState != nil &&
		generatedDocumentArtifactRequestContextForTurn(session, session.TaskState.Goal) != "" {
		return true
	}
	return false
}

func (l *RuntimeGateLedger) Normalize() {
	if l == nil {
		return
	}
	l.ID = strings.TrimSpace(l.ID)
	l.Action = normalizeRuntimeGateAction(l.Action)
	l.Branch = strings.TrimSpace(l.Branch)
	l.ReviewRunID = strings.TrimSpace(l.ReviewRunID)
	l.PatchTransactionID = strings.TrimSpace(l.PatchTransactionID)
	l.VerificationReportID = strings.TrimSpace(l.VerificationReportID)
	l.CompletionAuditID = strings.TrimSpace(l.CompletionAuditID)
	l.FinalAnswerReviewID = strings.TrimSpace(l.FinalAnswerReviewID)
	l.ChangedPaths = normalizeCompletionAuditReviewPaths(l.ChangedPaths)
	l.Blockers = normalizeTaskStateList(l.Blockers, 32)
	l.Warnings = normalizeTaskStateList(l.Warnings, 32)
	l.StaleReasons = normalizeTaskStateList(l.StaleReasons, 16)
	l.Waivers = normalizeTaskStateList(l.Waivers, 16)
	l.NextCommands = normalizeRuntimeGateNextCommands(l.NextCommands, 8)
	l.ReviewTransaction.Normalize()
	if l.FinalAnswerCorrection != nil {
		l.FinalAnswerCorrection.Normalize()
	}
	if l.ReviewObservability != nil && l.ReviewObservability.FinalAnswerCorrection != nil {
		l.ReviewObservability.FinalAnswerCorrection.Normalize()
	}
	if len(l.Blockers) > 0 {
		l.Status = runtimeGateStatusBlocked
		l.Ready = false
		return
	}
	if len(l.Warnings) > 0 {
		l.Status = runtimeGateStatusNeedsReview
		l.Ready = false
		return
	}
	l.Status = runtimeGateStatusReady
	l.Ready = true
}

func (t *ReviewTransaction) Normalize() {
	if t == nil {
		return
	}
	t.ID = strings.TrimSpace(t.ID)
	t.ReviewRunID = strings.TrimSpace(t.ReviewRunID)
	t.Status = strings.TrimSpace(strings.ToLower(t.Status))
	t.Verdict = strings.TrimSpace(strings.ToLower(t.Verdict))
	t.StaleReasons = normalizeTaskStateList(t.StaleReasons, 16)
	t.BlockingFindings = normalizeTaskStateList(t.BlockingFindings, 32)
	t.WarningFindings = normalizeTaskStateList(t.WarningFindings, 32)
	t.Waivers = normalizeTaskStateList(t.Waivers, 16)
	t.NextCommands = normalizeRuntimeGateNextCommands(t.NextCommands, 8)
	if t.Status == "" {
		if t.Stale {
			t.Status = "stale"
		} else if len(t.BlockingFindings) > 0 {
			t.Status = "blocked"
		} else {
			t.Status = "fresh"
		}
	}
	t.Fresh = !t.Stale && t.Status != "stale"
}

func (l RuntimeGateLedger) RenderPromptSection() string {
	l.Normalize()
	if strings.TrimSpace(l.ID) == "" {
		return ""
	}
	lines := []string{
		fmt.Sprintf("- Runtime gate ledger: %s [%s]", l.ID, l.Status),
	}
	if l.Action != "" {
		lines = append(lines, "- Action: "+l.Action)
	}
	if l.ReviewRunID != "" {
		lines = append(lines, "- Review run: "+l.ReviewRunID)
	}
	if l.PatchTransactionID != "" {
		lines = append(lines, "- Patch transaction: "+l.PatchTransactionID)
	}
	if l.VerificationReportID != "" {
		lines = append(lines, "- Verification: "+l.VerificationReportID)
	}
	if l.CompletionAuditID != "" {
		lines = append(lines, "- Completion audit: "+l.CompletionAuditID)
	}
	if l.FinalAnswerReviewID != "" {
		lines = append(lines, "- Final answer review: "+l.FinalAnswerReviewID)
	}
	if len(l.ChangedPaths) > 0 {
		lines = append(lines, "- Changed paths: "+strings.Join(limitStrings(l.ChangedPaths, 8), ", "))
	}
	if len(l.StaleReasons) > 0 {
		lines = append(lines, "- Stale reasons: "+strings.Join(l.StaleReasons, " | "))
	}
	if len(l.Blockers) > 0 {
		lines = append(lines, "- Blockers: "+strings.Join(limitStrings(l.Blockers, 4), " | "))
	}
	if len(l.Warnings) > 0 {
		lines = append(lines, "- Warnings: "+strings.Join(limitStrings(l.Warnings, 4), " | "))
	}
	if len(l.Waivers) > 0 {
		lines = append(lines, "- Waivers: "+strings.Join(l.Waivers, " | "))
	}
	if len(l.NextCommands) > 0 {
		next := l.NextCommands[0]
		if strings.TrimSpace(next.Command) != "" {
			lines = append(lines, "- Next command: "+next.Command)
		}
	}
	if l.ReviewObservability != nil {
		lines = append(lines, "- Review decision: "+reviewDecisionObservabilityStatusLine(l.ReviewObservability))
		lines = append(lines, "- Review gate: "+reviewGateObservabilityStatusLine(l.ReviewObservability))
		lines = append(lines, "- Single-model second pass: "+reviewSecondPassStatusLine(l.ReviewObservability.SingleModelSecondPass))
		lines = append(lines, "- Cross-review triage: "+reviewCrossReviewTriageStatusLine(l.ReviewObservability.CrossReviewTriage))
		lines = append(lines, "- Remaining obligations: "+reviewRemainingObligationsStatusLine(l.ReviewObservability.RemainingObligations))
		lines = append(lines, "- Blocker classes: "+reviewBlockerClassesStatusLine(l.ReviewObservability.BlockerClasses))
	}
	if l.FinalAnswerCorrection != nil {
		lines = append(lines, "- Final answer correction: "+finalAnswerCorrectionStatusLine(l.FinalAnswerCorrection))
	}
	return strings.Join(lines, "\n")
}

func normalizeRuntimeGateAction(action string) string {
	action = strings.TrimSpace(strings.ToLower(strings.ReplaceAll(action, "-", "_")))
	switch action {
	case "", "final", "final_answer":
		return runtimeGateActionFinalAnswer
	case "git", "git_write":
		return runtimeGateActionGitWrite
	case "mcp", "mcp_write", "write_side":
		return runtimeGateActionMCPWrite
	case "completion", "completion_audit":
		return runtimeGateActionCompletionAudit
	case "review":
		return runtimeGateActionReview
	default:
		return action
	}
}

func runtimeGateActionRequiresReview(action string) bool {
	switch normalizeRuntimeGateAction(action) {
	case runtimeGateActionGitWrite, runtimeGateActionMCPWrite:
		return true
	default:
		return false
	}
}

func runtimeGateChangedPaths(root string, session *Session) []string {
	return runtimeGateChangedPathsForAction(root, session, "")
}

func runtimeGateChangedPathsForAction(root string, session *Session, action string) []string {
	var paths []string
	action = normalizeRuntimeGateAction(action)
	patchPaths := currentTurnPatchTransactionChangedPaths(session)
	if len(patchPaths) == 0 && runtimeGateActionMayUseArchivedPatchScope(action) {
		patchPaths = sessionPatchTransactionChangedPaths(session)
	}
	paths = append(paths, patchPaths...)
	includeGitChanged := true
	if len(patchPaths) > 0 {
		switch action {
		case runtimeGateActionGitWrite, runtimeGateActionMCPWrite:
			includeGitChanged = true
		default:
			includeGitChanged = false
		}
	} else if strings.EqualFold(action, runtimeGateActionFinalAnswer) {
		includeGitChanged = runtimeGateFinalAnswerShouldUseGitChangedFallback(session)
	}
	if includeGitChanged && strings.TrimSpace(root) != "" && reviewScopeGitStatusLooksUsable(root) {
		paths = append(paths, filterReviewablePaths(delegationChangedFiles(root))...)
	}
	return normalizeCompletionAuditReviewPaths(paths)
}

func runtimeGateFinalAnswerShouldUseGitChangedFallback(session *Session) bool {
	if session == nil {
		return false
	}
	latestUser := strings.TrimSpace(baseUserQueryText(latestExternalOrUserMessageText(session.Messages)))
	if generatedDocumentArtifactRequestContextForTurn(session, latestUser) != "" {
		return false
	}
	if latestUser != "" && !looksLikeInternalReviewFeedbackUserMessage(latestUser) {
		if classifyTurnIntent(latestUser) == TurnIntentEditCode {
			return true
		}
		if !controlRequestContinuesCurrentWorkContext(latestUser) {
			return false
		}
		effective := strings.TrimSpace(baseUserQueryText(sessionEffectiveUserRequestText(session)))
		if effective == "" || strings.EqualFold(effective, latestUser) {
			return false
		}
		if generatedDocumentArtifactRequestContextForTurn(session, effective) != "" {
			return false
		}
		return classifyTurnIntent(effective) == TurnIntentEditCode ||
			requestModeLooksCodeChanging(effective)
	}
	if latestUser == "" && session.AcceptanceContract == nil && session.TaskState == nil {
		return true
	}
	if session.AcceptanceContract != nil {
		contract := *session.AcceptanceContract
		contract.Normalize()
		if generatedDocumentArtifactRequestContextForTurn(session, contract.SourcePrompt) != "" {
			return false
		}
		if strings.EqualFold(contract.Mode, "inspect_and_fix") {
			return true
		}
		if classifyTurnIntent(contract.SourcePrompt) == TurnIntentEditCode {
			return true
		}
	}
	if session.TaskState != nil {
		return classifyTurnIntent(session.TaskState.Goal) == TurnIntentEditCode
	}
	return false
}

func runtimeGateActionMayUseArchivedPatchScope(action string) bool {
	switch normalizeRuntimeGateAction(action) {
	case runtimeGateActionGitWrite, runtimeGateActionMCPWrite, runtimeGateActionReview, runtimeGateActionCompletionAudit:
		return true
	default:
		return false
	}
}

func runtimeGateReviewRun(root string, session *Session, provided *ReviewRun) (ReviewRun, bool) {
	if provided != nil {
		copyRun := *provided
		return copyRun, strings.TrimSpace(copyRun.ID) != ""
	}
	if session != nil && session.LastReviewRun != nil {
		copyRun := *session.LastReviewRun
		return copyRun, strings.TrimSpace(copyRun.ID) != ""
	}
	if strings.TrimSpace(root) == "" {
		return ReviewRun{}, false
	}
	latest, _, ok, err := loadLatestReviewRun(root)
	if err != nil || !ok || strings.TrimSpace(latest.ID) == "" {
		return ReviewRun{}, false
	}
	return latest, true
}

func runtimeGateAttachReview(root string, ledger *RuntimeGateLedger, review ReviewRun) {
	if ledger == nil {
		return
	}
	freshness := review.Freshness
	if strings.TrimSpace(freshness.ReviewFingerprint) == "" {
		freshness.ReviewFingerprint = strings.TrimSpace(review.ReviewFingerprint)
	}
	freshness.CheckedAt = time.Now()
	if !strings.EqualFold(ledger.Action, runtimeGateActionReview) {
		freshness = reviewLatestFreshnessAgainstPaths(root, review, ledger.ChangedPaths)
		if missing := reviewUnreviewedChangedPaths(review.ChangeSet.ChangedPaths, ledger.ChangedPaths); len(missing) > 0 {
			freshness.Stale = true
			freshness.InvalidatedBy = analysisUniqueStrings(append(freshness.InvalidatedBy, "changed_paths"))
			reason := "unreviewed changed files: " + strings.Join(limitStrings(missing, 6), ", ")
			if strings.Contains(freshness.StaleReason, reason) {
				// Already recorded by the git freshness pass.
			} else if strings.TrimSpace(freshness.StaleReason) != "" {
				freshness.StaleReason += "; " + reason
			} else {
				freshness.StaleReason = reason
			}
		}
	}
	waivers := runtimeGateActiveWaiverSummaries(review.Waivers)
	blockers := runtimeGateUnwaivedBlockers(review, time.Now())
	tx := ReviewTransaction{
		ID:               "review-tx-" + strings.TrimSpace(review.ID),
		ReviewRunID:      strings.TrimSpace(review.ID),
		Verdict:          firstNonBlankString(review.Gate.Verdict, review.Result.Verdict),
		Fresh:            !freshness.Stale,
		Stale:            freshness.Stale,
		BlockingFindings: blockers,
		WarningFindings:  append([]string(nil), review.Gate.WarningFindings...),
		Waivers:          waivers,
		NextCommands:     append([]ReviewNextCommand(nil), review.Gate.NextCommands...),
	}
	if freshness.Stale {
		tx.Status = "stale"
		tx.StaleReasons = append(tx.StaleReasons, freshness.StaleReason)
		ledger.StaleReasons = append(ledger.StaleReasons, freshness.StaleReason)
		ledger.Blockers = append(ledger.Blockers, "latest review is stale: "+valueOrDefault(freshness.StaleReason, "review fingerprint changed"))
		ledger.NextCommands = appendRuntimeGateNextCommand(ledger.NextCommands, ReviewNextCommand{
			ID:             "review",
			Command:        "/review",
			Reason:         "latest review freshness is stale",
			Safety:         "read_only",
			When:           "before final answer or git write",
			ClientHint:     "Repeat /review after the latest changes.",
			ExpectedResult: "A fresh review transaction replaces the stale one.",
		})
	} else if len(blockers) > 0 {
		tx.Status = "blocked"
		ledger.Blockers = append(ledger.Blockers, "latest review has unwaived blockers: "+strings.Join(limitStrings(blockers, 6), ", "))
		ledger.NextCommands = appendRuntimeGateNextCommand(ledger.NextCommands, ReviewNextCommand{
			ID:             "repair",
			Command:        "/continuity continue from review",
			Reason:         "latest review has blocking findings",
			Safety:         "safe_local",
			When:           "after reading review findings",
			ClientHint:     "Repair the latest review blockers, then rerun /review.",
			ExpectedResult: "Review blockers are resolved or explicitly waived.",
		})
	} else if len(review.Gate.WarningFindings) > 0 || strings.EqualFold(review.Gate.Verdict, reviewVerdictApprovedWithWarnings) {
		tx.Status = "warning"
		ledger.Warnings = append(ledger.Warnings, "latest review has warnings: "+strings.Join(limitStrings(review.Gate.WarningFindings, 6), ", "))
	} else {
		tx.Status = "fresh"
	}
	ledger.ReviewRunID = strings.TrimSpace(review.ID)
	ledger.ReviewTransaction = tx
	ledger.Waivers = append(ledger.Waivers, waivers...)
	ledger.NextCommands = append(ledger.NextCommands, review.Gate.NextCommands...)
}

func runtimeGateAttachVerification(session *Session, ledger *RuntimeGateLedger) {
	if session == nil || ledger == nil {
		return
	}
	if generatedDocumentArtifactGateAcceptedForRequest(session, "", ledger.ChangedPaths) {
		return
	}
	if sessionHasApprovedDocumentArtifactOnlyHarness(session) {
		return
	}
	if changedPathsAreGeneratedDocumentArtifacts(session, "", ledger.ChangedPaths) {
		return
	}
	if len(ledger.ChangedPaths) == 0 {
		return
	}
	if session.LastVerification == nil {
		ledger.Warnings = append(ledger.Warnings, "changed files have no linked verification report")
		ledger.NextCommands = appendRuntimeGateNextCommand(ledger.NextCommands, ReviewNextCommand{
			ID:             "verify",
			Command:        "/verify --full",
			Reason:         "changed files have no latest verification report",
			Safety:         "safe_local",
			When:           "before completion or git write",
			ClientHint:     "Run verification, then repeat /review or /completion-audit.",
			ExpectedResult: "A current verification report is linked into the runtime gate ledger.",
		})
		return
	}
	report := *session.LastVerification
	if !verificationReportCoversRuntimeGateScope(session, report, time.Time{}, ledger.ChangedPaths, ledger.PatchTransactionID) {
		ledger.Warnings = append(ledger.Warnings, "latest verification predates or does not cover the current patch transaction")
		ledger.NextCommands = appendRuntimeGateNextCommand(ledger.NextCommands, ReviewNextCommand{
			ID:             "verify",
			Command:        "/verify --full",
			Reason:         "latest verification is stale for current changed files",
			Safety:         "safe_local",
			When:           "before completion or git write",
			ClientHint:     "Run verification for the current patch transaction, then repeat /review or /completion-audit.",
			ExpectedResult: "A current verification report is linked into the runtime gate ledger.",
		})
		return
	}
	if report.HasFailures() {
		ledger.Blockers = append(ledger.Blockers, "latest verification failed: "+compactPromptSection(report.FailureSummary(), 240))
		ledger.NextCommands = appendRuntimeGateNextCommand(ledger.NextCommands, ReviewNextCommand{
			ID:             "verify",
			Command:        "/verify --full",
			Reason:         "latest verification has failures",
			Safety:         "safe_local",
			When:           "after fixing the failure",
			ClientHint:     "Fix the failing command and rerun verification.",
			ExpectedResult: "Latest verification passes before completion.",
		})
		return
	}
	if !completionAuditVerificationHasPassedStep(report) {
		ledger.Warnings = append(ledger.Warnings, "latest verification has no passing step")
	}
}

func verificationReportCoversCurrentPatch(session *Session, report VerificationReport, recordedAt time.Time, changedPaths []string) bool {
	return verificationReportCoversPatchTime(report, recordedAt, changedPaths, runtimeGateLatestPatchChangeTime(session))
}

func verificationReportCoversRuntimeGateScope(session *Session, report VerificationReport, recordedAt time.Time, changedPaths []string, patchTransactionID string) bool {
	return verificationReportCoversPatchTime(report, recordedAt, changedPaths, runtimeGatePatchChangeTimeByID(session, patchTransactionID))
}

func verificationReportCoversPatchTime(report VerificationReport, recordedAt time.Time, changedPaths []string, patchTime time.Time) bool {
	changedPaths = normalizeTaskStateList(changedPaths, 64)
	if len(changedPaths) > 0 && len(report.ChangedPaths) > 0 && !changedPathsCovered(changedPaths, report.ChangedPaths) {
		return false
	}
	if patchTime.IsZero() {
		return true
	}
	reportTime := verificationReportTimestamp(report, recordedAt)
	if reportTime.IsZero() {
		return false
	}
	return !reportTime.Before(patchTime.Add(-time.Second))
}

func verificationReportTimestamp(report VerificationReport, recordedAt time.Time) time.Time {
	if !report.GeneratedAt.IsZero() {
		return report.GeneratedAt
	}
	return recordedAt
}

func runtimeGateLatestPatchChangeTime(session *Session) time.Time {
	tx := latestRuntimeGatePatchTransaction(session)
	if tx == nil {
		return time.Time{}
	}
	return runtimeGatePatchTransactionChangeTime(*tx)
}

func runtimeGatePatchChangeTimeByID(session *Session, id string) time.Time {
	id = strings.TrimSpace(id)
	if session == nil || id == "" {
		return time.Time{}
	}
	if session.ActivePatchTransaction != nil {
		tx := *session.ActivePatchTransaction
		tx.Normalize()
		if strings.TrimSpace(tx.ID) == id {
			return runtimeGatePatchTransactionChangeTime(tx)
		}
	}
	for _, candidate := range session.PatchTransactions {
		tx := candidate
		tx.Normalize()
		if strings.TrimSpace(tx.ID) == id {
			return runtimeGatePatchTransactionChangeTime(tx)
		}
	}
	return time.Time{}
}

func runtimeGatePatchTransactionChangeTime(tx PatchTransaction) time.Time {
	tx.Normalize()
	for _, candidate := range []time.Time{tx.CompletedAt, tx.UpdatedAt, tx.StartedAt} {
		if !candidate.IsZero() {
			return candidate
		}
	}
	return time.Time{}
}

func runtimeGateVerificationPassed(session *Session) bool {
	if session == nil || session.LastVerification == nil {
		return false
	}
	if !verificationReportCoversCurrentPatch(session, *session.LastVerification, time.Time{}, currentTurnPatchTransactionChangedPaths(session)) {
		return false
	}
	report := *session.LastVerification
	return !report.HasFailures() && completionAuditVerificationHasPassedStep(report)
}

func runtimeGatePatchTransactionForAction(session *Session, action string) *PatchTransaction {
	if tx := currentTurnPatchTransaction(session); tx != nil {
		return tx
	}
	if runtimeGateActionMayUseArchivedPatchScope(action) {
		return latestRuntimeGatePatchTransaction(session)
	}
	return nil
}

func runtimeGateAttachPatchTransactionScope(session *Session, action string, ledger *RuntimeGateLedger) {
	if ledger == nil {
		return
	}
	warnings := runtimeGatePatchTransactionScopeWarningsForAction(session, action)
	if len(warnings) == 0 {
		return
	}
	ledger.Blockers = append(ledger.Blockers, "patch transaction has unknown changed-file scope: "+strings.Join(limitStrings(warnings, 4), " | "))
	ledger.NextCommands = appendRuntimeGateNextCommand(ledger.NextCommands, ReviewNextCommand{
		ID:             "review",
		Command:        "/review",
		Reason:         "workspace mutation was recorded without changed_paths metadata",
		Safety:         "read_only",
		When:           "before final answer or git write",
		ClientHint:     "Inspect the real workspace diff and rerun review with a known changed-file scope.",
		ExpectedResult: "A fresh review transaction covers the actual changed files.",
	})
}

func runtimeGatePatchTransactionScopeWarningsForAction(session *Session, action string) []string {
	tx := runtimeGatePatchTransactionForAction(session, action)
	if tx == nil {
		return nil
	}
	return patchTransactionScopeWarnings(*tx)
}

func runtimeGateAttachCodingHarness(session *Session, ledger *RuntimeGateLedger) {
	if session == nil || ledger == nil || session.LastCodingHarnessReport == nil {
		return
	}
	report := *session.LastCodingHarnessReport
	report.Normalize()
	if session.LastFinalAnswerCorrection != nil {
		correction := *session.LastFinalAnswerCorrection
		correction.Normalize()
		ledger.FinalAnswerCorrection = &correction
	} else if report.FinalAnswerCorrection != nil {
		correction := *report.FinalAnswerCorrection
		correction.Normalize()
		ledger.FinalAnswerCorrection = &correction
	}
	if report.Approved {
		return
	}
	if runtimeGateGeneratedDocumentArtifactHarnessBlockersAreAnswerOnly(session, ledger, &report) {
		return
	}
	for _, finding := range report.allFindings() {
		if !strings.EqualFold(strings.TrimSpace(finding.Severity), "blocker") {
			continue
		}
		title := firstNonBlankString(finding.Title, "coding harness blocker")
		ledger.Blockers = append(ledger.Blockers, "coding harness blocker: "+title)
	}
	if len(ledger.Blockers) == 0 {
		ledger.Warnings = append(ledger.Warnings, "coding harness did not approve the final state")
	}
}

func runtimeGateGeneratedDocumentArtifactHarnessBlockersAreAnswerOnly(session *Session, ledger *RuntimeGateLedger, report *CodingHarnessReport) bool {
	if session == nil || ledger == nil || report == nil {
		return false
	}
	action := normalizeRuntimeGateAction(ledger.Action)
	if action != runtimeGateActionFinalAnswer && action != runtimeGateActionCompletionAudit {
		return false
	}
	if !sessionHasDocumentArtifactQualityAcceptedHarness(session) {
		return false
	}
	if !runtimeGateHasGeneratedDocumentArtifactContext(session, ledger.ChangedPaths) {
		return false
	}
	return codingHarnessReportRequiresFinalAnswerOnlyRevision(report)
}

func latestRuntimeGatePatchTransaction(session *Session) *PatchTransaction {
	if session == nil {
		return nil
	}
	if tx := currentTurnPatchTransaction(session); tx != nil {
		return tx
	}
	if len(session.PatchTransactions) == 0 {
		return nil
	}
	copyTx := session.PatchTransactions[0]
	copyTx.Normalize()
	if strings.TrimSpace(copyTx.ID) == "" {
		return nil
	}
	return &copyTx
}

func runtimeGateVerificationReportID(report VerificationReport) string {
	if !report.GeneratedAt.IsZero() {
		return "verification-" + report.GeneratedAt.Format("20060102-150405")
	}
	return "verification-" + computeReviewFingerprint(report.Trigger, string(report.Mode), report.Workspace, strings.Join(report.ChangedPaths, ","))
}

func latestRuntimeGateCompletionAuditID(session *Session) string {
	if session == nil {
		return ""
	}
	for i := len(session.ConversationEvents) - 1; i >= 0; i-- {
		event := session.ConversationEvents[i]
		if event.Kind != conversationEventKindCompletionAudit {
			continue
		}
		if event.Entities != nil {
			if id := strings.TrimSpace(event.Entities["completion_audit"]); id != "" {
				return id
			}
		}
	}
	return ""
}

func runtimeGateFinalAnswerReviewID(session *Session) string {
	if session == nil || session.TaskState == nil || session.TaskState.FinalReviewCount <= 0 {
		return ""
	}
	return fmt.Sprintf("final-review-%03d", session.TaskState.FinalReviewCount)
}

func runtimeGateActiveWaiverSummaries(waivers []ReviewWaiver) []string {
	now := time.Now()
	var out []string
	for _, waiver := range waivers {
		if !runtimeGateWaiverActive(waiver, now) {
			continue
		}
		out = append(out, strings.TrimSpace(waiver.ID)+":"+strings.TrimSpace(waiver.FindingID))
	}
	return normalizeTaskStateList(out, 16)
}

func runtimeGateUnwaivedBlockers(run ReviewRun, now time.Time) []string {
	waived := map[string]bool{}
	for _, waiver := range run.Waivers {
		if runtimeGateWaiverActive(waiver, now) {
			waived[strings.ToLower(strings.TrimSpace(waiver.FindingID))] = true
		}
	}
	var out []string
	for _, id := range run.Gate.BlockingFindings {
		id = strings.TrimSpace(id)
		if id == "" || waived[strings.ToLower(id)] {
			continue
		}
		out = append(out, id)
	}
	return normalizeTaskStateList(out, 32)
}

func runtimeGateWaiverActive(waiver ReviewWaiver, now time.Time) bool {
	if !waiver.Allowed {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(waiver.Status), "active") {
		return false
	}
	if !waiver.ExpiresAt.IsZero() && now.After(waiver.ExpiresAt) {
		return false
	}
	return strings.TrimSpace(waiver.FindingID) != ""
}

func runtimeGateBlocksAction(ledger RuntimeGateLedger) bool {
	ledger.Normalize()
	return strings.EqualFold(ledger.Status, runtimeGateStatusBlocked)
}

func runtimeGateBlocksFinalAnswer(ledger RuntimeGateLedger, reply string) bool {
	if !runtimeGateBlocksAction(ledger) {
		return false
	}
	return !runtimeGateFinalAnswerDisclosesBlockers(ledger, reply)
}

func runtimeGateFinalAnswerDisclosesBlockers(ledger RuntimeGateLedger, reply string) bool {
	reply = strings.ToLower(strings.TrimSpace(reply))
	if reply == "" {
		return false
	}
	blockers := strings.ToLower(strings.Join(ledger.Blockers, " "))
	if blockers == "" {
		return false
	}
	if strings.Contains(blockers, "verification") &&
		(replyMentionsVerificationBlocker(reply) || replyMentionsVerificationNotRun(reply)) {
		return true
	}
	if strings.Contains(blockers, "review") || strings.Contains(blockers, "stale") || strings.Contains(blockers, "coding harness") {
		return containsAny(reply,
			"blocked", "blocker", "remaining", "unresolved", "incomplete", "cannot complete",
			"stale review", "missing review", "runtime gate blocked", "gate blocked",
			"차단", "남아", "미완료", "리뷰 차단", "게이트 차단")
	}
	return containsAny(reply,
		"blocked", "blocker", "remaining", "unresolved", "incomplete", "cannot complete",
		"failed", "failing", "not run", "차단", "남아", "미완료", "실패")
}

func (a *Agent) refreshRuntimeGateLedger(action string) RuntimeGateLedger {
	root := ""
	if a != nil {
		root = workspaceSnapshotRoot(a.Workspace)
		if strings.TrimSpace(root) == "" {
			root = a.Workspace.Root
		}
		if strings.TrimSpace(root) == "" && a.Session != nil {
			root = a.Session.WorkingDir
		}
	}
	var session *Session
	if a != nil {
		session = a.Session
	}
	ledger := buildRuntimeGateLedger(root, session, action)
	if session != nil {
		session.RuntimeGateLedger = &ledger
	}
	return ledger
}

func (a *Agent) runtimeGateFeedbackForAction(action string) (bool, string) {
	ledger := a.refreshRuntimeGateLedger(action)
	if !runtimeGateBlocksAction(ledger) {
		return false, ""
	}
	return true, renderRuntimeGateBlockedFeedback(ledger, action)
}

func (rt *runtimeState) runtimeGateFeedbackForAction(action string) (bool, string) {
	root := ""
	if rt != nil {
		root = workspaceSnapshotRoot(rt.workspace)
		if strings.TrimSpace(root) == "" {
			root = rt.workspace.Root
		}
		if strings.TrimSpace(root) == "" && rt.session != nil {
			root = rt.session.WorkingDir
		}
	}
	var session *Session
	if rt != nil {
		session = rt.session
	}
	ledger := buildRuntimeGateLedger(root, session, action)
	if session != nil {
		session.RuntimeGateLedger = &ledger
	}
	if !runtimeGateBlocksAction(ledger) {
		return false, ""
	}
	return true, renderRuntimeGateBlockedFeedback(ledger, action)
}

func (rt *runtimeState) runtimeGateLedgerForStatus(action string) RuntimeGateLedger {
	root := ""
	if rt != nil {
		root = workspaceSnapshotRoot(rt.workspace)
		if strings.TrimSpace(root) == "" {
			root = rt.workspace.Root
		}
		if strings.TrimSpace(root) == "" && rt.session != nil {
			root = rt.session.WorkingDir
		}
	}
	var session *Session
	if rt != nil {
		session = rt.session
	}
	ledger := buildRuntimeGateLedger(root, session, action)
	if session != nil {
		session.RuntimeGateLedger = &ledger
	}
	return ledger
}

func (rt *runtimeState) printRuntimeGateStatus(action string) {
	if rt == nil {
		return
	}
	ledger := rt.runtimeGateLedgerForStatus(action)
	ledger.Normalize()
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.subsection("Runtime Gate"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("runtime_gate", runtimeGateStatusSummary(ledger)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("review_freshness", runtimeGateReviewFreshnessLabel(ledger)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("changed_paths", fmt.Sprintf("%d", len(ledger.ChangedPaths))))
	if len(ledger.ChangedPaths) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("latest_changed", strings.Join(limitStrings(ledger.ChangedPaths, 4), ", ")))
	}
	if ledger.ReviewRunID != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("latest_review", ledger.ReviewRunID))
	}
	if ledger.ReviewObservability != nil {
		obs := ledger.ReviewObservability
		fmt.Fprintln(rt.writer, rt.ui.statusKV("review_decision", reviewDecisionObservabilityStatusLine(obs)))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("gate_decision", reviewGateObservabilityStatusLine(obs)))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("second_pass", reviewSecondPassStatusLine(obs.SingleModelSecondPass)))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("cross_review_triage", reviewCrossReviewTriageStatusLine(obs.CrossReviewTriage)))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("remaining_obligations", reviewRemainingObligationsStatusLine(obs.RemainingObligations)))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("blocker_classes", reviewBlockerClassesStatusLine(obs.BlockerClasses)))
		if len(obs.IncompleteTriageBlockers) > 0 {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("triage_blockers", strings.Join(limitStrings(obs.IncompleteTriageBlockers, 2), " | ")))
		}
		if strings.TrimSpace(obs.ResidualRiskSummary) != "" {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("triage_residual", obs.ResidualRiskSummary))
		}
	}
	if ledger.FinalAnswerCorrection != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("final_answer_correction", finalAnswerCorrectionStatusLine(ledger.FinalAnswerCorrection)))
		if detail := finalAnswerCorrectionDetailedLine(ledger.FinalAnswerCorrection); detail != "" {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("final_answer_correction_detail", detail))
		}
	}
	if ledger.PatchTransactionID != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("patch_transaction", ledger.PatchTransactionID))
	}
	if ledger.VerificationReportID != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("verification_report", ledger.VerificationReportID))
	}
	if ledger.CompletionAuditID != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("completion_audit", ledger.CompletionAuditID))
	}
	if len(ledger.Blockers) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("blockers", fmt.Sprintf("%d", len(ledger.Blockers))))
		fmt.Fprintln(rt.writer, rt.ui.warnLine(strings.Join(limitStrings(ledger.Blockers, 2), " | ")))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("blockers", "0"))
	}
	if len(ledger.Warnings) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("warnings", fmt.Sprintf("%d", len(ledger.Warnings))))
		fmt.Fprintln(rt.writer, rt.ui.warnLine(strings.Join(limitStrings(ledger.Warnings, 2), " | ")))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("warnings", "0"))
	}
	if len(ledger.Waivers) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("waivers", strings.Join(limitStrings(ledger.Waivers, 4), ", ")))
	}
	if len(ledger.StaleReasons) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("stale_reason", strings.Join(limitStrings(ledger.StaleReasons, 2), " | ")))
	}
	if line := runtimeGatePrimaryNextCommandLine(ledger); line != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("next_command", line))
	}
}

func runtimeGateStatusSummary(ledger RuntimeGateLedger) string {
	if runtimeGateLedgerEmpty(ledger) {
		return "unknown"
	}
	ledger.Normalize()
	parts := []string{valueOrDefault(ledger.Status, runtimeGateStatusReady)}
	if ledger.ReviewRunID != "" {
		parts = append(parts, "review="+ledger.ReviewRunID)
	}
	if len(ledger.Blockers) > 0 {
		parts = append(parts, fmt.Sprintf("blockers=%d", len(ledger.Blockers)))
	}
	if len(ledger.Warnings) > 0 {
		parts = append(parts, fmt.Sprintf("warnings=%d", len(ledger.Warnings)))
	}
	return strings.Join(parts, " ")
}

func runtimeGateReviewFreshnessLabel(ledger RuntimeGateLedger) string {
	if runtimeGateLedgerEmpty(ledger) {
		return "unknown"
	}
	ledger.Normalize()
	if ledger.ReviewRunID == "" {
		if len(ledger.ChangedPaths) > 0 {
			return "missing"
		}
		return "not_required"
	}
	if ledger.ReviewTransaction.Stale || strings.EqualFold(ledger.ReviewTransaction.Status, "stale") || len(ledger.StaleReasons) > 0 {
		return "stale"
	}
	if ledger.ReviewTransaction.Fresh || strings.EqualFold(ledger.ReviewTransaction.Status, "fresh") {
		return "fresh"
	}
	return valueOrDefault(ledger.ReviewTransaction.Status, "unknown")
}

func runtimeGateLedgerEmpty(ledger RuntimeGateLedger) bool {
	return strings.TrimSpace(ledger.ID) == "" &&
		strings.TrimSpace(ledger.Action) == "" &&
		strings.TrimSpace(ledger.Status) == "" &&
		strings.TrimSpace(ledger.ReviewRunID) == "" &&
		strings.TrimSpace(ledger.PatchTransactionID) == "" &&
		strings.TrimSpace(ledger.VerificationReportID) == "" &&
		strings.TrimSpace(ledger.CompletionAuditID) == "" &&
		strings.TrimSpace(ledger.FinalAnswerReviewID) == "" &&
		len(ledger.ChangedPaths) == 0 &&
		len(ledger.Blockers) == 0 &&
		len(ledger.Warnings) == 0 &&
		len(ledger.StaleReasons) == 0 &&
		len(ledger.Waivers) == 0 &&
		len(ledger.NextCommands) == 0 &&
		ledger.ReviewObservability == nil &&
		ledger.FinalAnswerCorrection == nil
}

func runtimeGatePrimaryNextCommandLine(ledger RuntimeGateLedger) string {
	ledger.Normalize()
	if len(ledger.NextCommands) == 0 {
		return ""
	}
	next := ledger.NextCommands[0]
	command := strings.TrimSpace(next.Command)
	if command == "" {
		return ""
	}
	if strings.TrimSpace(next.Reason) == "" {
		return command
	}
	return command + " - " + strings.TrimSpace(next.Reason)
}

func renderRuntimeGateBlockedFeedback(ledger RuntimeGateLedger, action string) string {
	ledger.Normalize()
	var b strings.Builder
	fmt.Fprintf(&b, "Runtime gate ledger blocked %s. Resolve the blockers before continuing.", normalizeRuntimeGateAction(action))
	if ledger.ID != "" {
		fmt.Fprintf(&b, "\n\nLedger: %s", ledger.ID)
	}
	if ledger.ReviewRunID != "" {
		fmt.Fprintf(&b, "\nReview: %s", ledger.ReviewRunID)
	}
	if len(ledger.Blockers) > 0 {
		b.WriteString("\n\nBlockers:\n")
		for _, blocker := range limitStrings(ledger.Blockers, 6) {
			fmt.Fprintf(&b, "- %s\n", blocker)
		}
	}
	if len(ledger.StaleReasons) > 0 {
		b.WriteString("\nStale reasons:\n")
		for _, reason := range limitStrings(ledger.StaleReasons, 4) {
			fmt.Fprintf(&b, "- %s\n", reason)
		}
	}
	if len(ledger.NextCommands) > 0 {
		b.WriteString("\nNext commands:\n")
		for _, cmd := range limitRuntimeGateNextCommands(ledger.NextCommands, 4) {
			if strings.TrimSpace(cmd.Command) != "" {
				fmt.Fprintf(&b, "- %s", cmd.Command)
				if strings.TrimSpace(cmd.Reason) != "" {
					fmt.Fprintf(&b, " (%s)", cmd.Reason)
				}
				b.WriteString("\n")
			}
		}
	}
	b.WriteString("\nDo not claim completion or perform write-side git/MCP actions until the ledger is fresh and blocker-free.")
	return strings.TrimSpace(b.String())
}

func appendRuntimeGateNextCommand(items []ReviewNextCommand, item ReviewNextCommand) []ReviewNextCommand {
	items = append(items, item)
	return normalizeRuntimeGateNextCommands(items, 8)
}

func normalizeRuntimeGateNextCommands(items []ReviewNextCommand, limit int) []ReviewNextCommand {
	out := make([]ReviewNextCommand, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.Command = strings.TrimSpace(item.Command)
		if item.ID == "" && item.Command == "" {
			continue
		}
		key := strings.ToLower(firstNonBlankString(item.ID, item.Command))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	if limit > 0 && len(out) > limit {
		return out[:limit]
	}
	return out
}

func limitRuntimeGateNextCommands(items []ReviewNextCommand, limit int) []ReviewNextCommand {
	items = normalizeRuntimeGateNextCommands(items, limit)
	return append([]ReviewNextCommand(nil), items...)
}
