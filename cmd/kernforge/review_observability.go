package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

type ReviewDecisionObservability struct {
	ReviewID                 string                            `json:"review_id,omitempty"`
	Trigger                  string                            `json:"trigger,omitempty"`
	Target                   string                            `json:"target,omitempty"`
	Mode                     string                            `json:"mode,omitempty"`
	Flow                     string                            `json:"flow,omitempty"`
	Gate                     ReviewGateObservability           `json:"gate,omitempty"`
	SingleModelSecondPass    *ReviewSecondPassObservability    `json:"single_model_second_pass,omitempty"`
	CrossReviewTriage        *ReviewCrossReviewTriageSummary   `json:"cross_review_triage,omitempty"`
	BlockerClasses           *ReviewBlockerClassCounts         `json:"blocker_classes,omitempty"`
	RemainingObligations     *ReviewRemainingObligationSummary `json:"remaining_obligations,omitempty"`
	IncompleteTriageBlockers []string                          `json:"incomplete_triage_blockers,omitempty"`
	FinalAnswerCorrection    *FinalAnswerCorrectionVisibility  `json:"final_answer_correction,omitempty"`
	NextRecommendedCommand   *ReviewNextCommand                `json:"next_recommended_command,omitempty"`
	ResidualRiskSummary      string                            `json:"residual_risk_summary,omitempty"`
}

type ReviewGateObservability struct {
	Verdict string `json:"verdict,omitempty"`
	Action  string `json:"action,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type ReviewSecondPassObservability struct {
	Status        string   `json:"status,omitempty"`
	Ran           bool     `json:"ran"`
	CacheHit      bool     `json:"cache_hit,omitempty"`
	ModelRoute    string   `json:"model_route,omitempty"`
	FindingCount  int      `json:"finding_count,omitempty"`
	ReviewedPaths []string `json:"reviewed_paths,omitempty"`
	PromptRef     string   `json:"prompt_ref,omitempty"`
	RawOutputRef  string   `json:"raw_output_ref,omitempty"`
	SkippedReason string   `json:"skipped_reason,omitempty"`
}

type ReviewCrossReviewTriageSummary struct {
	TotalCount          int            `json:"total_count,omitempty"`
	IncompleteCount     int            `json:"incomplete_count,omitempty"`
	StatusCounts        map[string]int `json:"status_counts,omitempty"`
	UserActionNeeded    bool           `json:"user_action_needed,omitempty"`
	UserDecisionCount   int            `json:"user_decision_count,omitempty"`
	Blockers            []string       `json:"blockers,omitempty"`
	UserDecisionPrompts []string       `json:"user_decision_prompts,omitempty"`
}

type ReviewBlockerClassCounts struct {
	CodeRepair              int `json:"code_repair,omitempty"`
	ReviewerRouteProblem    int `json:"reviewer_route_problem,omitempty"`
	EvidenceGap             int `json:"evidence_gap,omitempty"`
	VerificationGap         int `json:"verification_gap,omitempty"`
	FinalAnswerCompleteness int `json:"final_answer_completeness,omitempty"`
}

type ReviewRemainingObligationSummary struct {
	Repair        int      `json:"repair,omitempty"`
	Verification  int      `json:"verification,omitempty"`
	Evidence      int      `json:"evidence,omitempty"`
	ReviewerRoute int      `json:"reviewer_route,omitempty"`
	Summary       []string `json:"summary,omitempty"`
}

func buildReviewDecisionObservability(run *ReviewRun, ledger *RuntimeGateLedger, report *CodingHarnessReport) *ReviewDecisionObservability {
	if run == nil {
		return nil
	}
	copyRun := *run
	if copyRun.ObligationLedger.TotalCount == 0 && len(copyRun.Findings) > 0 {
		copyRun.ObligationLedger = buildReviewObligationLedger(copyRun)
	}
	summary := &ReviewDecisionObservability{
		ReviewID: copyRun.ID,
		Trigger:  strings.TrimSpace(copyRun.Trigger),
		Target:   strings.TrimSpace(copyRun.Target),
		Mode:     strings.TrimSpace(copyRun.Mode),
		Flow:     strings.TrimSpace(copyRun.Flow),
		Gate: ReviewGateObservability{
			Verdict: firstNonBlankString(copyRun.Gate.Verdict, copyRun.Result.Verdict),
			Action:  strings.TrimSpace(copyRun.Gate.Action),
			Reason:  strings.TrimSpace(copyRun.Gate.Reason),
		},
		SingleModelSecondPass:    buildReviewSecondPassObservability(copyRun),
		CrossReviewTriage:        buildReviewCrossReviewTriageSummary(copyRun.CrossReviewTriage),
		BlockerClasses:           buildReviewBlockerClassCounts(copyRun, report),
		RemainingObligations:     buildReviewRemainingObligationSummary(copyRun.ObligationLedger),
		IncompleteTriageBlockers: reviewIncompleteTriageBlockers(copyRun.CrossReviewTriage),
		ResidualRiskSummary:      reviewResidualRiskSummary(copyRun),
	}
	if ledger != nil {
		if len(ledger.NextCommands) > 0 {
			next := ledger.NextCommands[0]
			summary.NextRecommendedCommand = &next
		}
		if summary.FinalAnswerCorrection == nil && ledger.FinalAnswerCorrection != nil {
			correction := *ledger.FinalAnswerCorrection
			correction.Normalize()
			summary.FinalAnswerCorrection = &correction
		}
	}
	if summary.NextRecommendedCommand == nil && len(copyRun.Gate.NextCommands) > 0 {
		next := copyRun.Gate.NextCommands[0]
		summary.NextRecommendedCommand = &next
	}
	if report != nil {
		if correction := finalAnswerCorrectionVisibilityFromReport(report, false); correction != nil {
			summary.FinalAnswerCorrection = correction
		} else if report.FinalAnswerCorrection != nil {
			correction := *report.FinalAnswerCorrection
			correction.Normalize()
			summary.FinalAnswerCorrection = &correction
		}
	}
	return summary
}

func buildReviewSecondPassObservability(run ReviewRun) *ReviewSecondPassObservability {
	if run.SingleModelSecondPass != nil {
		second := *run.SingleModelSecondPass
		status := strings.TrimSpace(strings.ToLower(second.Status))
		ran := status != "" && status != "skipped" && status != "not_applicable" && status != "pending"
		return &ReviewSecondPassObservability{
			Status:        valueOrDefault(status, "unknown"),
			Ran:           ran,
			CacheHit:      second.CacheHit,
			ModelRoute:    strings.TrimSpace(second.Model),
			FindingCount:  second.FindingCount,
			ReviewedPaths: normalizeTaskStateList(second.ReviewedPaths, 32),
			PromptRef:     filepath.ToSlash(strings.TrimSpace(second.PromptPath)),
			RawOutputRef:  filepath.ToSlash(strings.TrimSpace(second.RawOutputPath)),
			SkippedReason: strings.TrimSpace(second.SkippedReason),
		}
	}
	status := "not_applicable"
	reason := "independent cross-review was configured or single-model review policy was not active"
	if run.SingleModelPolicy.Enabled {
		status = "skipped"
		reason = "single-model second pass was not required for this review trigger and evidence shape"
	} else if len(run.ReviewerRuns) == 0 {
		status = "skipped"
		reason = "model review did not run, so there was no first-pass output to second-check"
	} else if reviewRunHasReviewerRun(run, "cross_reviewer") {
		reason = "independent cross-review route ran instead of single-model second pass"
	}
	return &ReviewSecondPassObservability{
		Status:        status,
		Ran:           false,
		SkippedReason: reason,
	}
}

func reviewRunHasReviewerRun(run ReviewRun, role string) bool {
	role = normalizeReviewRole(role)
	for _, reviewerRun := range run.ReviewerRuns {
		if normalizeReviewRole(reviewerRun.Role) == role {
			return true
		}
	}
	return false
}

func buildReviewCrossReviewTriageSummary(ledger *CrossReviewTriageLedger) *ReviewCrossReviewTriageSummary {
	ledger = normalizedCrossReviewTriageLedger(ledger)
	if ledger == nil || len(ledger.Items) == 0 {
		return nil
	}
	summary := &ReviewCrossReviewTriageSummary{
		TotalCount:      ledger.TotalCount,
		IncompleteCount: ledger.IncompleteCount,
		StatusCounts:    map[string]int{},
		Blockers:        normalizeTaskStateList(ledger.Blockers, 8),
	}
	for status, count := range ledger.StatusCounts {
		status = normalizeCrossReviewTriageStatus(status)
		if status == "" || count <= 0 {
			continue
		}
		summary.StatusCounts[status] += count
		if status == crossReviewTriageNeedsUserDecision {
			summary.UserDecisionCount += count
		}
	}
	for _, item := range ledger.Items {
		if normalizeCrossReviewTriageStatus(item.TriageStatus) != crossReviewTriageNeedsUserDecision {
			continue
		}
		summary.UserActionNeeded = true
		if prompt := crossReviewTriageUserActionPrompt(item); prompt != "" {
			summary.UserDecisionPrompts = append(summary.UserDecisionPrompts, prompt)
		}
	}
	summary.UserDecisionPrompts = normalizeTaskStateList(summary.UserDecisionPrompts, 4)
	if len(summary.StatusCounts) == 0 {
		summary.StatusCounts = nil
	}
	return summary
}

func reviewIncompleteTriageBlockers(ledger *CrossReviewTriageLedger) []string {
	ledger = normalizedCrossReviewTriageLedger(ledger)
	if ledger == nil {
		return nil
	}
	out := append([]string(nil), ledger.Blockers...)
	for _, item := range ledger.Items {
		if normalizeCrossReviewTriageStatus(item.TriageStatus) != crossReviewTriageNeedsUserDecision {
			continue
		}
		label := firstNonBlankString(item.FindingID, item.Title, "cross-review finding")
		out = append(out, label+": needs user or primary repair decision")
	}
	return normalizeTaskStateList(out, 8)
}

func buildReviewRemainingObligationSummary(ledger ReviewObligationLedger) *ReviewRemainingObligationSummary {
	if ledger.TotalCount == 0 && len(ledger.Items) == 0 {
		return nil
	}
	return &ReviewRemainingObligationSummary{
		Repair:        ledger.OpenRepairCount,
		Verification:  ledger.OpenVerificationCount,
		Evidence:      ledger.OpenEvidenceCount,
		ReviewerRoute: ledger.OpenRouteCount,
		Summary:       normalizeTaskStateList(ledger.Summary, 8),
	}
}

func buildReviewBlockerClassCounts(run ReviewRun, report *CodingHarnessReport) *ReviewBlockerClassCounts {
	counts := &ReviewBlockerClassCounts{}
	for _, obligation := range run.ObligationLedger.Items {
		if !reviewObligationStatusIsOpen(obligation.Status) {
			continue
		}
		switch normalizeReviewObligationType(obligation.Type) {
		case reviewObligationTypeReviewerRoute:
			counts.ReviewerRouteProblem++
		case reviewObligationTypeVerification:
			counts.VerificationGap++
		case reviewObligationTypeEvidence:
			counts.EvidenceGap++
		case reviewObligationTypeRepair:
			if normalizeReviewObligationStatus(obligation.Status) == reviewObligationStatusVerificationRequired {
				counts.VerificationGap++
			} else {
				counts.CodeRepair++
			}
		}
	}
	if run.ObligationLedger.TotalCount == 0 {
		for _, finding := range run.Findings {
			if !reviewFindingBlocksGate(run, finding) {
				continue
			}
			classifyReviewFindingBlocker(run, finding, counts)
		}
	}
	if report != nil {
		copyReport := *report
		copyReport.Normalize()
		for _, finding := range copyReport.allFindings() {
			if !strings.EqualFold(strings.TrimSpace(finding.Severity), "blocker") {
				continue
			}
			if codingHarnessFindingRequiresFinalAnswerOnlyRevision(finding) {
				counts.FinalAnswerCompleteness++
			}
		}
	}
	if counts.CodeRepair == 0 &&
		counts.ReviewerRouteProblem == 0 &&
		counts.EvidenceGap == 0 &&
		counts.VerificationGap == 0 &&
		counts.FinalAnswerCompleteness == 0 {
		return nil
	}
	return counts
}

func classifyReviewFindingBlocker(run ReviewRun, finding ReviewFinding, counts *ReviewBlockerClassCounts) {
	if counts == nil {
		return
	}
	finding.Normalize()
	if strings.EqualFold(strings.TrimSpace(finding.ID), requiredReviewerFailureFindingID) {
		counts.ReviewerRouteProblem++
		return
	}
	if strings.EqualFold(finding.Category, "evidence_gap") {
		if reviewFindingLooksVerificationOnly(finding) {
			counts.VerificationGap++
		} else {
			counts.EvidenceGap++
		}
		return
	}
	if strings.EqualFold(finding.Category, "test_gap") {
		counts.VerificationGap++
		return
	}
	if reviewFindingShouldBeRepairPlanBlocker(run, finding) {
		counts.CodeRepair++
	}
}

func reviewResidualRiskSummary(run ReviewRun) string {
	ledger := normalizedCrossReviewTriageLedger(run.CrossReviewTriage)
	if ledger == nil || len(ledger.Items) == 0 {
		return ""
	}
	parts := reviewCrossReviewTriageCountParts(ledger)
	if ledger.IncompleteCount > 0 {
		parts = append(parts, fmt.Sprintf("incomplete=%d", ledger.IncompleteCount))
	}
	userDecision := 0
	deferred := 0
	for _, item := range ledger.Items {
		switch normalizeCrossReviewTriageStatus(item.TriageStatus) {
		case crossReviewTriageNeedsUserDecision:
			userDecision++
		case crossReviewTriageAcceptedDeferred:
			deferred++
		}
	}
	if userDecision > 0 {
		parts = append(parts, fmt.Sprintf("user_decision=%d", userDecision))
	}
	if deferred > 0 {
		parts = append(parts, fmt.Sprintf("deferred=%d", deferred))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

func reviewCrossReviewTriageCountParts(ledger *CrossReviewTriageLedger) []string {
	ledger = normalizedCrossReviewTriageLedger(ledger)
	if ledger == nil {
		return nil
	}
	parts := make([]string, 0, len(ledger.StatusCounts))
	for _, status := range []string{
		crossReviewTriageAcceptedFixed,
		crossReviewTriageAcceptedDeferred,
		crossReviewTriageRejectedWithReason,
		crossReviewTriageNeedsUserDecision,
	} {
		if count := ledger.StatusCounts[status]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", status, count))
		}
	}
	return parts
}

func reviewDecisionObservabilityStatusLine(obs *ReviewDecisionObservability) string {
	if obs == nil {
		return "none"
	}
	parts := []string{}
	if obs.ReviewID != "" {
		parts = append(parts, "id="+obs.ReviewID)
	}
	if obs.Trigger != "" {
		parts = append(parts, "trigger="+obs.Trigger)
	}
	if obs.Target != "" {
		parts = append(parts, "target="+obs.Target)
	}
	if obs.Mode != "" {
		parts = append(parts, "mode="+obs.Mode)
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, " ")
}

func reviewGateObservabilityStatusLine(obs *ReviewDecisionObservability) string {
	if obs == nil {
		return "none"
	}
	parts := []string{}
	if obs.Gate.Verdict != "" {
		parts = append(parts, "verdict="+obs.Gate.Verdict)
	}
	if obs.Gate.Action != "" {
		parts = append(parts, "action="+obs.Gate.Action)
	}
	if obs.Gate.Reason != "" {
		parts = append(parts, "reason="+compactPromptSection(obs.Gate.Reason, 80))
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, " ")
}

func reviewSecondPassStatusLine(obs *ReviewSecondPassObservability) string {
	if obs == nil {
		return "none"
	}
	parts := []string{"status=" + valueOrDefault(obs.Status, "unknown")}
	parts = append(parts, fmt.Sprintf("ran=%t", obs.Ran))
	if obs.CacheHit {
		parts = append(parts, "cache_hit=true")
	}
	if obs.ModelRoute != "" {
		parts = append(parts, "route="+obs.ModelRoute)
	}
	if len(obs.ReviewedPaths) > 0 {
		parts = append(parts, "paths="+strings.Join(limitStrings(obs.ReviewedPaths, 3), ","))
	}
	if obs.FindingCount > 0 {
		parts = append(parts, fmt.Sprintf("findings=%d", obs.FindingCount))
	}
	if obs.SkippedReason != "" {
		parts = append(parts, "reason="+compactPromptSection(obs.SkippedReason, 100))
	}
	return strings.Join(parts, " ")
}

func reviewCrossReviewTriageStatusLine(obs *ReviewCrossReviewTriageSummary) string {
	if obs == nil {
		return "none"
	}
	parts := []string{fmt.Sprintf("total=%d", obs.TotalCount)}
	for _, status := range []string{
		crossReviewTriageAcceptedFixed,
		crossReviewTriageAcceptedDeferred,
		crossReviewTriageRejectedWithReason,
		crossReviewTriageNeedsUserDecision,
	} {
		if count := obs.StatusCounts[status]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", status, count))
		}
	}
	if obs.IncompleteCount > 0 {
		parts = append(parts, fmt.Sprintf("incomplete=%d", obs.IncompleteCount))
	}
	if obs.UserActionNeeded {
		parts = append(parts, "user_action=true")
	}
	return strings.Join(parts, " ")
}

func reviewRemainingObligationsStatusLine(obs *ReviewRemainingObligationSummary) string {
	if obs == nil {
		return "none"
	}
	parts := []string{}
	if obs.Repair > 0 {
		parts = append(parts, fmt.Sprintf("repair=%d", obs.Repair))
	}
	if obs.Verification > 0 {
		parts = append(parts, fmt.Sprintf("verification=%d", obs.Verification))
	}
	if obs.Evidence > 0 {
		parts = append(parts, fmt.Sprintf("evidence=%d", obs.Evidence))
	}
	if obs.ReviewerRoute > 0 {
		parts = append(parts, fmt.Sprintf("reviewer_route=%d", obs.ReviewerRoute))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}

func reviewBlockerClassesStatusLine(obs *ReviewBlockerClassCounts) string {
	if obs == nil {
		return "none"
	}
	parts := []string{}
	if obs.CodeRepair > 0 {
		parts = append(parts, fmt.Sprintf("code_repair=%d", obs.CodeRepair))
	}
	if obs.ReviewerRouteProblem > 0 {
		parts = append(parts, fmt.Sprintf("reviewer_route=%d", obs.ReviewerRouteProblem))
	}
	if obs.EvidenceGap > 0 {
		parts = append(parts, fmt.Sprintf("evidence_gap=%d", obs.EvidenceGap))
	}
	if obs.VerificationGap > 0 {
		parts = append(parts, fmt.Sprintf("verification_gap=%d", obs.VerificationGap))
	}
	if obs.FinalAnswerCompleteness > 0 {
		parts = append(parts, fmt.Sprintf("final_answer_completeness=%d", obs.FinalAnswerCompleteness))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}
