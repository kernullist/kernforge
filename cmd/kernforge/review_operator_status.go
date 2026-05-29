package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	reviewTimelineStatusPending = "pending"
	reviewTimelineStatusRunning = "running"
	reviewTimelineStatusPassed  = "passed"
	reviewTimelineStatusWarned  = "warned"
	reviewTimelineStatusBlocked = "blocked"
	reviewTimelineStatusSkipped = "skipped"

	reviewLifecyclePhaseClassifiedRequest        = "classified_request"
	reviewLifecyclePhaseCollectingContext        = "collecting_context"
	reviewLifecyclePhasePreWriteReview           = "pre_write_review"
	reviewLifecyclePhaseApplyingChange           = "applying_change"
	reviewLifecyclePhasePostChangeReview         = "post_change_review"
	reviewLifecyclePhaseSingleModelSecondPass    = "single_model_second_pass"
	reviewLifecyclePhaseCrossReviewTriage        = "cross_review_triage"
	reviewLifecyclePhaseArtifactQualityGate      = "artifact_quality_gate"
	reviewLifecyclePhaseVerification             = "verification"
	reviewLifecyclePhaseFinalAnswerContract      = "final_answer_contract"
	reviewLifecyclePhaseBlocked                  = "blocked"
	reviewLifecyclePhaseCompleted                = "completed"
	reviewBlockerClassCodeRepair                 = "code_repair_blocker"
	reviewBlockerClassReviewerRouteProblem       = "reviewer_route_problem"
	reviewBlockerClassEvidenceGap                = "evidence_gap"
	reviewBlockerClassVerificationGap            = "verification_gap"
	reviewBlockerClassDocumentArtifactQuality    = "document_artifact_quality"
	reviewBlockerClassFinalAnswerContract        = "final_answer_contract"
	reviewBlockerClassUserDecisionRequired       = "user_decision_required"
	reviewFinalAnswerContractStatusPassed        = "passed"
	reviewFinalAnswerContractStatusPending       = "pending"
	reviewFinalAnswerContractStatusBlocked       = "blocked"
	reviewFinalAnswerContractStatusNotApplicable = "not_applicable"
)

type ReviewLifecyclePhase struct {
	Phase          string `json:"phase,omitempty"`
	Status         string `json:"status,omitempty"`
	Reason         string `json:"reason,omitempty"`
	EvidenceRef    string `json:"evidence_ref,omitempty"`
	NextSafeAction string `json:"next_safe_action,omitempty"`
	NextCommand    string `json:"next_command,omitempty"`
}

type ReviewCompactStatus struct {
	RequestClass              string         `json:"request_class,omitempty"`
	LifecycleKind             string         `json:"lifecycle_kind,omitempty"`
	MixedFlow                 bool           `json:"mixed_flow,omitempty"`
	SecondaryRequestClasses   []string       `json:"secondary_request_classes,omitempty"`
	ClassificationConfidence  float64        `json:"classification_confidence,omitempty"`
	ClassificationAmbiguous   bool           `json:"classification_ambiguous,omitempty"`
	ClassificationAmbiguity   []string       `json:"classification_ambiguity,omitempty"`
	CurrentLifecyclePhase     string         `json:"current_lifecycle_phase,omitempty"`
	RouteMode                 string         `json:"route_mode,omitempty"`
	ReviewerRouteQuality      string         `json:"reviewer_route_quality,omitempty"`
	ReviewGateStatus          string         `json:"review_gate_status,omitempty"`
	RepairGateStatus          string         `json:"repair_gate_status,omitempty"`
	DocumentGateStatus        string         `json:"document_gate_status,omitempty"`
	VerificationGateStatus    string         `json:"verification_gate_status,omitempty"`
	FinalAnswerContractStatus string         `json:"final_answer_contract_status,omitempty"`
	SecondPassState           string         `json:"second_pass_state,omitempty"`
	CrossReviewTriageCounts   map[string]int `json:"cross_review_triage_counts,omitempty"`
	BlockersByClass           map[string]int `json:"blockers_by_class,omitempty"`
	RemainingObligations      []string       `json:"remaining_obligations,omitempty"`
	NextRecommendedCommand    string         `json:"next_recommended_command,omitempty"`
	DocumentArtifactPath      string         `json:"document_artifact_path,omitempty"`
	ArtifactQualityStatus     string         `json:"artifact_quality_status,omitempty"`
	VerificationSkipReason    string         `json:"verification_skip_reason,omitempty"`
}

type ReviewBlockerSummary struct {
	HasBlockers       bool                    `json:"has_blockers"`
	Primary           []ReviewOperatorBlocker `json:"primary,omitempty"`
	SecondaryWarnings []ReviewOperatorBlocker `json:"secondary_warnings,omitempty"`
	Counts            map[string]int          `json:"counts,omitempty"`
	NextCommand       string                  `json:"next_command,omitempty"`
}

type ReviewOperatorBlocker struct {
	Class          string   `json:"class,omitempty"`
	Title          string   `json:"title,omitempty"`
	WhyBlocks      string   `json:"why_blocks,omitempty"`
	AlreadyChecked string   `json:"already_checked,omitempty"`
	EvidenceRefs   []string `json:"evidence_refs,omitempty"`
	NextSafeAction string   `json:"next_safe_action,omitempty"`
	NextCommand    string   `json:"next_command,omitempty"`
}

type ReviewFinalAnswerContractStatus struct {
	RequestClass              string                               `json:"request_class,omitempty"`
	LifecycleKind             string                               `json:"lifecycle_kind,omitempty"`
	Status                    string                               `json:"status,omitempty"`
	Reason                    string                               `json:"reason,omitempty"`
	Requirements              []ReviewFinalAnswerRequirementStatus `json:"requirements,omitempty"`
	GenericCompletionRejected bool                                 `json:"generic_completion_rejected,omitempty"`
	CorrectionRequired        bool                                 `json:"correction_required,omitempty"`
	CorrectionAccepted        bool                                 `json:"correction_accepted,omitempty"`
	ArtifactPath              string                               `json:"artifact_path,omitempty"`
	ArtifactQualityStatus     string                               `json:"artifact_quality_status,omitempty"`
	VerificationLimitation    string                               `json:"verification_limitation,omitempty"`
	RemainingLimitation       string                               `json:"remaining_limitation,omitempty"`
}

type ReviewFinalAnswerRequirementStatus struct {
	Requirement string `json:"requirement,omitempty"`
	Status      string `json:"status,omitempty"`
	Reason      string `json:"reason,omitempty"`
	EvidenceRef string `json:"evidence_ref,omitempty"`
}

func normalizeReviewLifecycleTimeline(items []ReviewLifecyclePhase) []ReviewLifecyclePhase {
	out := make([]ReviewLifecyclePhase, 0, len(items))
	for _, item := range items {
		item.Phase = strings.TrimSpace(item.Phase)
		item.Status = normalizeReviewTimelineStatus(item.Status)
		item.Reason = strings.TrimSpace(item.Reason)
		item.EvidenceRef = filepath.ToSlash(strings.TrimSpace(item.EvidenceRef))
		item.NextSafeAction = strings.TrimSpace(item.NextSafeAction)
		item.NextCommand = strings.TrimSpace(item.NextCommand)
		if item.Phase == "" {
			continue
		}
		if item.Status == "" {
			item.Status = reviewTimelineStatusPending
		}
		out = append(out, item)
	}
	return out
}

func normalizeReviewTimelineStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(status, "-", "_")))
	switch status {
	case reviewTimelineStatusPending, reviewTimelineStatusRunning, reviewTimelineStatusPassed, reviewTimelineStatusWarned, reviewTimelineStatusBlocked, reviewTimelineStatusSkipped:
		return status
	case "warning", "warn":
		return reviewTimelineStatusWarned
	case "ok", "approved", "complete", "completed", "accepted", "recorded":
		return reviewTimelineStatusPassed
	case "failed", "needs_revision", "insufficient_evidence":
		return reviewTimelineStatusBlocked
	default:
		return strings.TrimSpace(status)
	}
}

func reviewLifecycleTimelineForRun(run *ReviewRun, session *Session, ledger *RuntimeGateLedger, report *CodingHarnessReport) []ReviewLifecyclePhase {
	if run == nil {
		return nil
	}
	runCopy := *run
	class := normalizeReviewRequestClass(firstNonBlankString(runCopy.RequestClass, runCopy.RequestAnalysis.RequestClass))
	contract := reviewFinalAnswerContractStatusForRun(&runCopy, session, report, "")
	triageSummary := buildReviewCrossReviewTriageSummary(runCopy.CrossReviewTriage)
	secondPass := buildReviewSecondPassObservability(runCopy)
	blockers := buildReviewBlockerSummary(&runCopy, ledger, report)
	verificationStatus := reviewVerificationGateStatusForRun(runCopy)
	documentStatus := reviewDocumentGateStatusForRun(runCopy, session)
	nextCommand := reviewLifecycleNextCommand(runCopy.Gate.NextCommands)
	if nextCommand == "" && ledger != nil {
		nextCommand = reviewLifecycleNextCommand(ledger.NextCommands)
	}
	items := []ReviewLifecyclePhase{
		{
			Phase:       reviewLifecyclePhaseClassifiedRequest,
			Status:      reviewTimelineStatusPassed,
			Reason:      firstNonBlankString(runCopy.RequestAnalysis.RequestClassReason, "request class selected by review request analysis"),
			EvidenceRef: "request_analysis",
		},
		{
			Phase:       reviewLifecyclePhaseCollectingContext,
			Status:      reviewContextTimelineStatus(runCopy),
			Reason:      reviewContextTimelineReason(runCopy),
			EvidenceRef: reviewContextEvidenceRef(runCopy),
		},
		reviewTimelinePhaseForPreWrite(runCopy),
		reviewTimelinePhaseForApplyingChange(runCopy),
		reviewTimelinePhaseForPostChange(runCopy),
		reviewTimelinePhaseForSecondPass(secondPass),
		reviewTimelinePhaseForTriage(triageSummary),
		reviewTimelinePhaseForDocumentGate(class, documentStatus, session, report),
		reviewTimelinePhaseForVerification(verificationStatus, runCopy, ledger),
		reviewTimelinePhaseForFinalAnswerContract(contract),
	}
	if blockers != nil && blockers.HasBlockers {
		items = append(items, ReviewLifecyclePhase{
			Phase:          reviewLifecyclePhaseBlocked,
			Status:         reviewTimelineStatusBlocked,
			Reason:         reviewPrimaryBlockerReason(blockers),
			EvidenceRef:    reviewPrimaryBlockerEvidence(blockers),
			NextSafeAction: reviewPrimaryBlockerNextAction(blockers),
			NextCommand:    firstNonBlankString(blockers.NextCommand, nextCommand),
		})
	} else {
		items = append(items, ReviewLifecyclePhase{
			Phase:       reviewLifecyclePhaseCompleted,
			Status:      reviewCompletedTimelineStatus(runCopy, ledger),
			Reason:      reviewCompletedTimelineReason(runCopy, ledger),
			EvidenceRef: firstNonBlankString("review:"+runCopy.ID, runtimeGateEvidenceRef(ledger)),
		})
	}
	return normalizeReviewLifecycleTimeline(items)
}

func reviewLifecycleTimelineForRuntimeGate(session *Session, action string, changedPaths []string, ledger *RuntimeGateLedger, report *CodingHarnessReport) []ReviewLifecyclePhase {
	class, reason := classifyRuntimeGateRequestClass(session, action, changedPaths)
	documentStatus := reviewRuntimeGateDocumentStatus(session, class)
	contract := reviewFinalAnswerContractStatusForClass(class, session, report, "")
	blockers := buildReviewBlockerSummary(nil, ledger, report)
	nextCommand := ""
	if ledger != nil {
		nextCommand = reviewLifecycleNextCommand(ledger.NextCommands)
	}
	status := reviewTimelineStatusRunning
	if ledger != nil {
		if len(ledger.Blockers) > 0 {
			status = reviewTimelineStatusBlocked
		} else if len(ledger.Warnings) > 0 {
			status = reviewTimelineStatusWarned
		} else if ledger.Ready {
			status = reviewTimelineStatusPassed
		}
	}
	postChangePhase := ReviewLifecyclePhase{
		Phase:          reviewLifecyclePhasePostChangeReview,
		Status:         reviewRuntimeReviewStatus(ledger),
		Reason:         reviewRuntimeReviewReason(ledger),
		EvidenceRef:    reviewRuntimeReviewEvidence(ledger),
		NextSafeAction: reviewRuntimeReviewNextAction(ledger),
		NextCommand:    reviewRuntimeReviewNextCommand(ledger),
	}
	if normalizeReviewRequestClass(class) == reviewRequestClassDocumentArtifact {
		postChangePhase = ReviewLifecyclePhase{
			Phase:          reviewLifecyclePhasePostChangeReview,
			Status:         reviewTimelineStatusSkipped,
			Reason:         "document artifact-only flow uses artifact-quality evidence instead of code review",
			NextSafeAction: "inspect the artifact-quality gate and final-answer contract",
		}
	}
	items := []ReviewLifecyclePhase{
		{
			Phase:       reviewLifecyclePhaseClassifiedRequest,
			Status:      reviewTimelineStatusPassed,
			Reason:      reason,
			EvidenceRef: "runtime_gate.request_context",
		},
		{
			Phase:       reviewLifecyclePhaseCollectingContext,
			Status:      reviewTimelineStatusPassed,
			Reason:      fmt.Sprintf("runtime gate is evaluating %d changed path(s)", len(changedPaths)),
			EvidenceRef: runtimeGateEvidenceRef(ledger),
		},
		{
			Phase:          reviewLifecyclePhasePreWriteReview,
			Status:         reviewTimelineStatusSkipped,
			Reason:         "runtime status is not executing a pre-write review",
			NextSafeAction: "run /review when changed files need review evidence",
			NextCommand:    "/review",
		},
		{
			Phase:       reviewLifecyclePhaseApplyingChange,
			Status:      reviewRuntimeApplyingChangeStatus(changedPaths),
			Reason:      reviewRuntimeApplyingChangeReason(changedPaths),
			EvidenceRef: runtimeGateEvidenceRef(ledger),
		},
		postChangePhase,
		{
			Phase:          reviewLifecyclePhaseSingleModelSecondPass,
			Status:         reviewTimelineStatusSkipped,
			Reason:         "no active review run is attached to this runtime status snapshot",
			NextSafeAction: "run /review to create second-pass or cross-review evidence when required",
			NextCommand:    "/review",
		},
		{
			Phase:          reviewLifecyclePhaseCrossReviewTriage,
			Status:         reviewTimelineStatusSkipped,
			Reason:         "no cross-review triage ledger is attached to this runtime status snapshot",
			NextSafeAction: "run /review when cross-review evidence is required",
			NextCommand:    "/review",
		},
		reviewTimelinePhaseForDocumentGate(class, documentStatus, session, report),
		reviewRuntimeVerificationTimelinePhase(session, ledger),
		reviewTimelinePhaseForFinalAnswerContract(contract),
	}
	if blockers != nil && blockers.HasBlockers {
		items = append(items, ReviewLifecyclePhase{
			Phase:          reviewLifecyclePhaseBlocked,
			Status:         reviewTimelineStatusBlocked,
			Reason:         reviewPrimaryBlockerReason(blockers),
			EvidenceRef:    reviewPrimaryBlockerEvidence(blockers),
			NextSafeAction: reviewPrimaryBlockerNextAction(blockers),
			NextCommand:    firstNonBlankString(blockers.NextCommand, nextCommand),
		})
	} else {
		items = append(items, ReviewLifecyclePhase{
			Phase:       reviewLifecyclePhaseCompleted,
			Status:      status,
			Reason:      reviewRuntimeCompletedReason(ledger),
			EvidenceRef: runtimeGateEvidenceRef(ledger),
		})
	}
	return normalizeReviewLifecycleTimeline(items)
}

func buildReviewCompactStatus(run *ReviewRun, ledger *RuntimeGateLedger, report *CodingHarnessReport) *ReviewCompactStatus {
	var lifecycle *ReviewRequestLifecycle
	var timeline []ReviewLifecyclePhase
	if run != nil {
		if run.Lifecycle != nil {
			copyLifecycle := *run.Lifecycle
			lifecycle = &copyLifecycle
		} else {
			lifecycle = buildReviewRequestLifecycle(run, nil)
		}
		timeline = reviewLifecycleTimelineForRun(run, nil, ledger, report)
	}
	if lifecycle == nil && ledger != nil {
		lifecycle = ledger.Lifecycle
	}
	if len(timeline) == 0 && lifecycle != nil && len(lifecycle.Timeline) > 0 {
		timeline = lifecycle.Timeline
	}
	if lifecycle == nil && run == nil && ledger == nil && report == nil {
		return nil
	}
	status := &ReviewCompactStatus{}
	if lifecycle != nil {
		lifecycle.Normalize()
		status.RequestClass = lifecycle.RequestClass
		status.LifecycleKind = lifecycle.LifecycleKind
		status.MixedFlow = lifecycle.MixedFlow
		status.SecondaryRequestClasses = lifecycle.SecondaryRequestClasses
		status.ClassificationConfidence = lifecycle.ClassificationConfidence
		status.ClassificationAmbiguous = lifecycle.ClassificationAmbiguous
		status.ClassificationAmbiguity = normalizeTaskStateList(lifecycle.AmbiguityWarnings, 4)
		status.CurrentLifecyclePhase = lifecycle.Phase
		status.RouteMode = lifecycle.RouteMode
		status.ReviewerRouteQuality = lifecycle.RouteQuality
		status.ReviewGateStatus = lifecycle.ReviewGateStatus
		status.RepairGateStatus = lifecycle.RepairGateStatus
		status.DocumentGateStatus = lifecycle.DocumentGateStatus
		status.VerificationGateStatus = lifecycle.VerificationGateStatus
		if len(lifecycle.RemainingObligations) > 0 {
			status.RemainingObligations = normalizeTaskStateList(lifecycle.RemainingObligations, 8)
		}
		status.NextRecommendedCommand = lifecycle.NextRecommendedCommand
	}
	if run != nil {
		second := buildReviewSecondPassObservability(*run)
		if second != nil {
			status.SecondPassState = firstNonBlankString(second.State, second.Status)
		}
		if triage := buildReviewCrossReviewTriageSummary(run.CrossReviewTriage); triage != nil {
			status.CrossReviewTriageCounts = reviewCrossReviewTriageCountsMap(triage)
		}
		if route := reviewRouteQualityForRun(*run); route != nil && status.ReviewerRouteQuality == "" {
			status.ReviewerRouteQuality = route.Status
		}
		if status.NextRecommendedCommand == "" {
			status.NextRecommendedCommand = reviewLifecycleNextCommand(run.Gate.NextCommands)
		}
	}
	if ledger != nil {
		if status.RequestClass == "" {
			status.RequestClass = ledger.RequestClass
		}
		if status.LifecycleKind == "" && ledger.Lifecycle != nil {
			status.LifecycleKind = ledger.Lifecycle.LifecycleKind
		}
		if !status.MixedFlow && ledger.Lifecycle != nil {
			status.MixedFlow = ledger.Lifecycle.MixedFlow
		}
		if len(status.SecondaryRequestClasses) == 0 && ledger.Lifecycle != nil {
			status.SecondaryRequestClasses = ledger.Lifecycle.SecondaryRequestClasses
		}
		if status.NextRecommendedCommand == "" {
			status.NextRecommendedCommand = reviewLifecycleNextCommand(ledger.NextCommands)
		}
	}
	blockers := buildReviewBlockerSummary(run, ledger, report)
	if blockers != nil {
		status.BlockersByClass = reviewBlockerCountsMap(blockers)
	}
	contract := reviewFinalAnswerContractStatusForRun(run, nil, report, "")
	if contract == nil && ledger != nil {
		contract = reviewFinalAnswerContractStatusForClass(status.RequestClass, nil, report, "")
	}
	if contract != nil {
		status.FinalAnswerContractStatus = contract.Status
		status.DocumentArtifactPath = contract.ArtifactPath
		status.ArtifactQualityStatus = contract.ArtifactQualityStatus
		status.VerificationSkipReason = contract.VerificationLimitation
	}
	if normalizeReviewRequestClass(status.RequestClass) == reviewRequestClassDocumentArtifact && report != nil {
		if status.DocumentArtifactPath == "" {
			status.DocumentArtifactPath = documentArtifactPathFromReport(report)
		}
		if status.ArtifactQualityStatus == "" || status.ArtifactQualityStatus == "unknown" {
			status.ArtifactQualityStatus = documentArtifactQualityStatusFromReport(report)
		}
	}
	if normalizeReviewRequestClass(status.RequestClass) == reviewRequestClassDocumentArtifact && status.VerificationSkipReason == "" {
		status.VerificationSkipReason = reviewDocumentArtifactVerificationSkipReason(status.VerificationGateStatus)
	}
	if status.CurrentLifecyclePhase == "" && lifecycle != nil {
		status.CurrentLifecyclePhase = lifecycle.Phase
	}
	if len(timeline) > 0 {
		status.CurrentLifecyclePhase = reviewCurrentLifecyclePhase(timeline, status.CurrentLifecyclePhase)
	}
	status.Normalize()
	return status
}

func (s *ReviewCompactStatus) Normalize() {
	if s == nil {
		return
	}
	s.RequestClass = normalizeReviewRequestClass(s.RequestClass)
	if s.RequestClass == reviewRequestClassGeneral {
		s.RequestClass = ""
	}
	s.LifecycleKind = normalizeReviewLifecycleKind(s.LifecycleKind)
	if s.LifecycleKind == reviewLifecycleKindGeneral && s.RequestClass != "" {
		s.LifecycleKind = reviewLifecycleKindForRequestClass(s.RequestClass)
	}
	s.SecondaryRequestClasses = normalizeReviewRequestClasses(s.SecondaryRequestClasses, 6)
	if len(s.SecondaryRequestClasses) > 0 || s.LifecycleKind == reviewLifecycleKindMixedFlow {
		s.MixedFlow = true
	}
	if s.ClassificationConfidence < 0 {
		s.ClassificationConfidence = 0
	}
	if s.ClassificationConfidence > 1 {
		s.ClassificationConfidence = 1
	}
	s.ClassificationAmbiguity = normalizeTaskStateList(s.ClassificationAmbiguity, 4)
	if len(s.ClassificationAmbiguity) > 0 {
		s.ClassificationAmbiguous = true
	}
	s.CurrentLifecyclePhase = strings.TrimSpace(s.CurrentLifecyclePhase)
	s.RouteMode = strings.TrimSpace(s.RouteMode)
	s.ReviewerRouteQuality = strings.TrimSpace(s.ReviewerRouteQuality)
	s.ReviewGateStatus = strings.TrimSpace(s.ReviewGateStatus)
	s.RepairGateStatus = strings.TrimSpace(s.RepairGateStatus)
	s.DocumentGateStatus = strings.TrimSpace(s.DocumentGateStatus)
	s.VerificationGateStatus = strings.TrimSpace(s.VerificationGateStatus)
	s.FinalAnswerContractStatus = strings.TrimSpace(s.FinalAnswerContractStatus)
	s.SecondPassState = strings.TrimSpace(s.SecondPassState)
	s.RemainingObligations = normalizeTaskStateList(s.RemainingObligations, 8)
	s.NextRecommendedCommand = strings.TrimSpace(s.NextRecommendedCommand)
	s.DocumentArtifactPath = filepath.ToSlash(strings.TrimSpace(s.DocumentArtifactPath))
	s.ArtifactQualityStatus = strings.TrimSpace(s.ArtifactQualityStatus)
	s.VerificationSkipReason = strings.TrimSpace(s.VerificationSkipReason)
	if len(s.CrossReviewTriageCounts) == 0 {
		s.CrossReviewTriageCounts = nil
	}
	if len(s.BlockersByClass) == 0 {
		s.BlockersByClass = nil
	}
}

func reviewCompactStatusLine(status *ReviewCompactStatus) string {
	if status == nil {
		return "none"
	}
	copyStatus := *status
	copyStatus.Normalize()
	parts := []string{}
	if copyStatus.RequestClass != "" {
		parts = append(parts, "class="+copyStatus.RequestClass)
	}
	if copyStatus.LifecycleKind != "" && copyStatus.LifecycleKind != reviewLifecycleKindGeneral {
		parts = append(parts, "kind="+copyStatus.LifecycleKind)
	}
	if copyStatus.MixedFlow {
		parts = append(parts, "mixed_flow=true")
	}
	if len(copyStatus.SecondaryRequestClasses) > 0 {
		parts = append(parts, "secondary="+strings.Join(copyStatus.SecondaryRequestClasses, ","))
	}
	if copyStatus.ClassificationConfidence > 0 {
		parts = append(parts, fmt.Sprintf("confidence=%.2f", copyStatus.ClassificationConfidence))
	}
	parts = append(parts, fmt.Sprintf("ambiguous=%t", copyStatus.ClassificationAmbiguous))
	if copyStatus.CurrentLifecyclePhase != "" {
		parts = append(parts, "phase="+copyStatus.CurrentLifecyclePhase)
	}
	if copyStatus.RouteMode != "" {
		parts = append(parts, "route="+copyStatus.RouteMode)
	}
	if copyStatus.ReviewerRouteQuality != "" {
		parts = append(parts, "route_quality="+copyStatus.ReviewerRouteQuality)
	}
	if copyStatus.FinalAnswerContractStatus != "" {
		parts = append(parts, "final_answer_contract="+copyStatus.FinalAnswerContractStatus)
	}
	if copyStatus.NextRecommendedCommand != "" {
		parts = append(parts, "next="+copyStatus.NextRecommendedCommand)
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, " ")
}

func reviewGateCompactLine(status *ReviewCompactStatus) string {
	if status == nil {
		return "none"
	}
	parts := []string{}
	for _, item := range []struct {
		name  string
		value string
	}{
		{"review", status.ReviewGateStatus},
		{"repair", status.RepairGateStatus},
		{"document", status.DocumentGateStatus},
		{"verification", status.VerificationGateStatus},
		{"final_answer", status.FinalAnswerContractStatus},
	} {
		if strings.TrimSpace(item.value) != "" {
			parts = append(parts, item.name+"="+strings.TrimSpace(item.value))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}

func reviewCompactMapLine(values map[string]int, ordered []string) string {
	if len(values) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, key := range ordered {
		parts = append(parts, fmt.Sprintf("%s=%d", key, values[key]))
		seen[key] = true
	}
	for key, value := range values {
		if seen[key] {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%d", key, value))
	}
	return strings.Join(parts, " ")
}

func reviewCrossReviewTriageCountsMap(summary *ReviewCrossReviewTriageSummary) map[string]int {
	if summary == nil {
		return nil
	}
	counts := map[string]int{
		crossReviewTriageAcceptedFixed:      summary.StatusCounts[crossReviewTriageAcceptedFixed],
		crossReviewTriageAcceptedDeferred:   summary.StatusCounts[crossReviewTriageAcceptedDeferred],
		crossReviewTriageRejectedWithReason: summary.StatusCounts[crossReviewTriageRejectedWithReason],
		crossReviewTriageNeedsUserDecision:  summary.StatusCounts[crossReviewTriageNeedsUserDecision],
		"incomplete_invalid":                summary.IncompleteCount,
	}
	return counts
}

func buildReviewBlockerSummary(run *ReviewRun, ledger *RuntimeGateLedger, report *CodingHarnessReport) *ReviewBlockerSummary {
	summary := &ReviewBlockerSummary{
		Counts: map[string]int{},
	}
	if run != nil {
		reviewBlockerSummaryFromRun(summary, *run)
	}
	if ledger != nil {
		reviewBlockerSummaryFromLedger(summary, *ledger)
	}
	if report != nil {
		reviewBlockerSummaryFromHarnessReport(summary, *report)
	}
	summary.Primary = sortReviewOperatorBlockers(summary.Primary)
	summary.SecondaryWarnings = sortReviewOperatorBlockers(summary.SecondaryWarnings)
	summary.HasBlockers = len(summary.Primary) > 0
	summary.Counts = reviewBlockerSummaryCounts(summary.Primary)
	summary.NextCommand = reviewBlockerSummaryNextCommand(summary.Primary)
	if !summary.HasBlockers && len(summary.SecondaryWarnings) == 0 {
		return nil
	}
	return summary
}

func reviewBlockerSummaryFromRun(summary *ReviewBlockerSummary, run ReviewRun) {
	for _, obligation := range run.ObligationLedger.Items {
		if !reviewObligationStatusIsOpen(obligation.Status) {
			continue
		}
		class := reviewBlockerClassForObligation(obligation)
		command := reviewBlockerDefaultCommand(class)
		if cmd := reviewLifecycleNextCommand(run.Gate.NextCommands); cmd != "" {
			command = cmd
		}
		summary.Primary = append(summary.Primary, ReviewOperatorBlocker{
			Class:          class,
			Title:          firstNonBlankString(obligation.Title, obligation.ID, "open review obligation"),
			WhyBlocks:      firstNonBlankString(obligation.RequiredAction, obligation.Status, "open review obligation blocks this lifecycle"),
			AlreadyChecked: "review obligation ledger",
			EvidenceRefs:   normalizeTaskStateList([]string{firstNonBlankString(obligation.ID, "obligation_ledger")}, 4),
			NextSafeAction: reviewBlockerDefaultNextAction(class),
			NextCommand:    command,
		})
	}
	if run.ObligationLedger.TotalCount == 0 {
		for _, finding := range run.Findings {
			if !reviewFindingBlocksGate(run, finding) {
				continue
			}
			class := reviewBlockerClassForFinding(run, finding)
			summary.Primary = append(summary.Primary, ReviewOperatorBlocker{
				Class:          class,
				Title:          firstNonBlankString(finding.Title, finding.ID, "blocking review finding"),
				WhyBlocks:      firstNonBlankString(finding.RequiredFix, finding.Impact, finding.Evidence, "blocking review finding requires a decision"),
				AlreadyChecked: "review findings and gate decision",
				EvidenceRefs:   reviewFindingEvidenceRefs(finding),
				NextSafeAction: reviewBlockerDefaultNextAction(class),
				NextCommand:    firstNonBlankString(reviewLifecycleNextCommand(run.Gate.NextCommands), reviewBlockerDefaultCommand(class)),
			})
		}
	}
	if triage := normalizedCrossReviewTriageLedger(run.CrossReviewTriage); triage != nil {
		for _, blocker := range triage.Blockers {
			summary.Primary = append(summary.Primary, ReviewOperatorBlocker{
				Class:          reviewBlockerClassEvidenceGap,
				Title:          "Incomplete cross-review triage",
				WhyBlocks:      blocker,
				AlreadyChecked: "cross_review_triage ledger validation",
				EvidenceRefs:   []string{"cross_review_triage"},
				NextSafeAction: "record a technical accepted_fixed, accepted_deferred, rejected_with_reason, or needs_user_decision decision",
				NextCommand:    "/review",
			})
		}
		for _, item := range triage.Items {
			if normalizeCrossReviewTriageStatus(item.TriageStatus) != crossReviewTriageNeedsUserDecision {
				continue
			}
			summary.Primary = append(summary.Primary, ReviewOperatorBlocker{
				Class:          reviewBlockerClassUserDecisionRequired,
				Title:          firstNonBlankString(item.Title, item.FindingID, "cross-review decision required"),
				WhyBlocks:      "cross-review finding still needs a user or primary repair decision",
				AlreadyChecked: "cross_review_triage needs_user_decision entry",
				EvidenceRefs:   normalizeTaskStateList(append([]string{firstNonBlankString(item.FindingID, "cross_review_triage")}, item.EvidenceRefs...), 8),
				NextSafeAction: crossReviewTriageUserActionPrompt(item),
				NextCommand:    crossReviewTriageNextCommand(item),
			})
		}
	}
}

func reviewBlockerSummaryFromLedger(summary *ReviewBlockerSummary, ledger RuntimeGateLedger) {
	for _, blocker := range ledger.Blockers {
		class := reviewBlockerClassForText(blocker)
		summary.Primary = append(summary.Primary, ReviewOperatorBlocker{
			Class:          class,
			Title:          reviewBlockerTitleFromText(blocker),
			WhyBlocks:      blocker,
			AlreadyChecked: "runtime gate ledger",
			EvidenceRefs:   normalizeTaskStateList([]string{runtimeGateEvidenceRef(&ledger)}, 4),
			NextSafeAction: reviewBlockerDefaultNextAction(class),
			NextCommand:    firstNonBlankString(reviewLifecycleNextCommand(ledger.NextCommands), reviewBlockerDefaultCommand(class)),
		})
	}
	for _, warning := range ledger.Warnings {
		class := reviewBlockerClassForText(warning)
		summary.SecondaryWarnings = append(summary.SecondaryWarnings, ReviewOperatorBlocker{
			Class:          class,
			Title:          reviewBlockerTitleFromText(warning),
			WhyBlocks:      warning,
			AlreadyChecked: "runtime gate ledger",
			EvidenceRefs:   normalizeTaskStateList([]string{runtimeGateEvidenceRef(&ledger)}, 4),
			NextSafeAction: reviewBlockerDefaultNextAction(class),
			NextCommand:    firstNonBlankString(reviewLifecycleNextCommand(ledger.NextCommands), reviewBlockerDefaultCommand(class)),
		})
	}
}

func reviewBlockerSummaryFromHarnessReport(summary *ReviewBlockerSummary, report CodingHarnessReport) {
	report.Normalize()
	for _, finding := range report.allFindings() {
		if !strings.EqualFold(strings.TrimSpace(finding.Severity), "blocker") {
			continue
		}
		class := reviewBlockerClassForHarnessFinding(finding)
		summary.Primary = append(summary.Primary, ReviewOperatorBlocker{
			Class:          class,
			Title:          firstNonBlankString(finding.Title, "coding harness blocker"),
			WhyBlocks:      firstNonBlankString(finding.Detail, "pre-final coding harness did not approve the state"),
			AlreadyChecked: "pre-final coding harness",
			EvidenceRefs:   []string{"coding_harness"},
			NextSafeAction: reviewBlockerDefaultNextAction(class),
			NextCommand:    reviewBlockerDefaultCommand(class),
		})
	}
}

func reviewBlockerClassForObligation(obligation ReviewObligation) string {
	switch normalizeReviewObligationType(obligation.Type) {
	case reviewObligationTypeReviewerRoute:
		return reviewBlockerClassReviewerRouteProblem
	case reviewObligationTypeVerification:
		return reviewBlockerClassVerificationGap
	case reviewObligationTypeEvidence:
		return reviewBlockerClassEvidenceGap
	case reviewObligationTypeRepair:
		if normalizeReviewObligationStatus(obligation.Status) == reviewObligationStatusVerificationRequired {
			return reviewBlockerClassVerificationGap
		}
		return reviewBlockerClassCodeRepair
	default:
		return reviewBlockerClassUserDecisionRequired
	}
}

func reviewBlockerClassForFinding(run ReviewRun, finding ReviewFinding) string {
	if strings.EqualFold(strings.TrimSpace(finding.ID), requiredReviewerFailureFindingID) {
		return reviewBlockerClassReviewerRouteProblem
	}
	if strings.EqualFold(finding.Category, "evidence_gap") {
		if reviewFindingLooksVerificationOnly(finding) {
			return reviewBlockerClassVerificationGap
		}
		return reviewBlockerClassEvidenceGap
	}
	if strings.EqualFold(finding.Category, "test_gap") {
		return reviewBlockerClassVerificationGap
	}
	if reviewFindingShouldBeRepairPlanBlocker(run, finding) {
		return reviewBlockerClassCodeRepair
	}
	return reviewBlockerClassUserDecisionRequired
}

func reviewBlockerClassForHarnessFinding(finding CodingHarnessFinding) string {
	title := strings.ToLower(strings.TrimSpace(finding.Title + " " + finding.Detail))
	switch {
	case containsAny(title, "document artifact", "artifact quality", "generated document", "문서", "산출물"):
		return reviewBlockerClassDocumentArtifactQuality
	case codingHarnessFindingRequiresFinalAnswerOnlyRevision(finding):
		return reviewBlockerClassFinalAnswerContract
	case containsAny(title, "verification", "validation", "test", "검증", "테스트"):
		return reviewBlockerClassVerificationGap
	default:
		return reviewBlockerClassCodeRepair
	}
}

func reviewBlockerClassForText(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case containsAny(lower, "reviewer", "review route", "cross reviewer", "route"):
		return reviewBlockerClassReviewerRouteProblem
	case containsAny(lower, "evidence", "freshness", "stale", "scope", "unreviewed"):
		return reviewBlockerClassEvidenceGap
	case containsAny(lower, "verification", "verify", "test", "build"):
		return reviewBlockerClassVerificationGap
	case containsAny(lower, "document", "artifact"):
		return reviewBlockerClassDocumentArtifactQuality
	case containsAny(lower, "final answer", "completion", "coding harness"):
		return reviewBlockerClassFinalAnswerContract
	case containsAny(lower, "user", "decision", "waiver"):
		return reviewBlockerClassUserDecisionRequired
	default:
		return reviewBlockerClassCodeRepair
	}
}

func reviewBlockerTitleFromText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "runtime gate blocker"
	}
	if idx := strings.Index(text, ":"); idx > 0 {
		return strings.TrimSpace(text[:idx])
	}
	return compactPromptSection(text, 80)
}

func reviewFindingEvidenceRefs(finding ReviewFinding) []string {
	refs := append([]string(nil), finding.EvidenceRefs...)
	refs = append(refs, finding.FixRefs...)
	refs = append(refs, finding.VerificationRefs...)
	if strings.TrimSpace(finding.Path) != "" {
		ref := filepath.ToSlash(strings.TrimSpace(finding.Path))
		if finding.Line > 0 {
			ref = fmt.Sprintf("%s:%d", ref, finding.Line)
		}
		refs = append(refs, ref)
	}
	if strings.TrimSpace(finding.ID) != "" {
		refs = append(refs, finding.ID)
	}
	return normalizeTaskStateList(refs, 8)
}

func reviewBlockerDefaultNextAction(class string) string {
	switch class {
	case reviewBlockerClassCodeRepair:
		return "repair the blocking finding, then rerun /review"
	case reviewBlockerClassReviewerRouteProblem:
		return "switch or clear the reviewer route, or explicitly continue with single-model disclosure"
	case reviewBlockerClassEvidenceGap:
		return "collect local evidence and rerun the review gate"
	case reviewBlockerClassVerificationGap:
		return "run or disclose focused verification before final answer"
	case reviewBlockerClassDocumentArtifactQuality:
		return "fix the generated artifact or disclose the artifact-quality limitation"
	case reviewBlockerClassFinalAnswerContract:
		return "revise only the final answer disclosure; do not expand code changes unless a real blocker remains"
	case reviewBlockerClassUserDecisionRequired:
		return "inspect the cited finding and choose accept, defer, reject, or repair"
	default:
		return "inspect the blocker and rerun the relevant gate"
	}
}

func reviewBlockerDefaultCommand(class string) string {
	switch class {
	case reviewBlockerClassCodeRepair, reviewBlockerClassUserDecisionRequired:
		return "/continuity continue from review"
	case reviewBlockerClassReviewerRouteProblem:
		return "/model cross-review"
	case reviewBlockerClassEvidenceGap:
		return "/review"
	case reviewBlockerClassVerificationGap:
		return "/verify --full"
	case reviewBlockerClassDocumentArtifactQuality, reviewBlockerClassFinalAnswerContract:
		return "/status detail"
	default:
		return "/status detail"
	}
}

func sortReviewOperatorBlockers(items []ReviewOperatorBlocker) []ReviewOperatorBlocker {
	order := map[string]int{}
	for i, class := range reviewBlockerClassOrder() {
		order[class] = i
	}
	out := append([]ReviewOperatorBlocker(nil), items...)
	for i := range out {
		out[i].Class = strings.TrimSpace(out[i].Class)
		out[i].Title = strings.TrimSpace(out[i].Title)
		out[i].WhyBlocks = strings.TrimSpace(out[i].WhyBlocks)
		out[i].AlreadyChecked = strings.TrimSpace(out[i].AlreadyChecked)
		out[i].EvidenceRefs = normalizeTaskStateList(out[i].EvidenceRefs, 8)
		out[i].NextSafeAction = strings.TrimSpace(out[i].NextSafeAction)
		out[i].NextCommand = strings.TrimSpace(out[i].NextCommand)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			left := order[valueOrDefault(out[j-1].Class, reviewBlockerClassUserDecisionRequired)]
			right := order[valueOrDefault(out[j].Class, reviewBlockerClassUserDecisionRequired)]
			if left <= right {
				break
			}
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func reviewBlockerClassOrder() []string {
	return []string{
		reviewBlockerClassCodeRepair,
		reviewBlockerClassReviewerRouteProblem,
		reviewBlockerClassEvidenceGap,
		reviewBlockerClassVerificationGap,
		reviewBlockerClassDocumentArtifactQuality,
		reviewBlockerClassFinalAnswerContract,
		reviewBlockerClassUserDecisionRequired,
	}
}

func reviewBlockerSummaryCounts(items []ReviewOperatorBlocker) map[string]int {
	counts := map[string]int{}
	for _, item := range items {
		class := strings.TrimSpace(item.Class)
		if class == "" {
			continue
		}
		counts[class]++
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

func reviewBlockerCountsMap(summary *ReviewBlockerSummary) map[string]int {
	if summary == nil {
		return nil
	}
	return reviewBlockerSummaryCounts(summary.Primary)
}

func reviewBlockerSummaryNextCommand(items []ReviewOperatorBlocker) string {
	for _, item := range items {
		if strings.TrimSpace(item.NextCommand) != "" {
			return strings.TrimSpace(item.NextCommand)
		}
	}
	return ""
}

func reviewBlockerSummaryStatusLine(summary *ReviewBlockerSummary) string {
	if summary == nil || !summary.HasBlockers {
		return "none"
	}
	parts := []string{}
	for _, class := range reviewBlockerClassOrder() {
		if count := summary.Counts[class]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", class, count))
		}
	}
	if summary.NextCommand != "" {
		parts = append(parts, "next="+summary.NextCommand)
	}
	return strings.Join(parts, " ")
}

func reviewPrimaryBlockerReason(summary *ReviewBlockerSummary) string {
	if summary == nil || len(summary.Primary) == 0 {
		return ""
	}
	item := summary.Primary[0]
	return firstNonBlankString(item.WhyBlocks, item.Title, item.Class)
}

func reviewPrimaryBlockerEvidence(summary *ReviewBlockerSummary) string {
	if summary == nil || len(summary.Primary) == 0 {
		return ""
	}
	if len(summary.Primary[0].EvidenceRefs) == 0 {
		return ""
	}
	return summary.Primary[0].EvidenceRefs[0]
}

func reviewPrimaryBlockerNextAction(summary *ReviewBlockerSummary) string {
	if summary == nil || len(summary.Primary) == 0 {
		return ""
	}
	return summary.Primary[0].NextSafeAction
}

func reviewFinalAnswerContractStatusForRun(run *ReviewRun, session *Session, report *CodingHarnessReport, reply string) *ReviewFinalAnswerContractStatus {
	class := reviewRequestClassGeneral
	if run != nil {
		class = normalizeReviewRequestClass(firstNonBlankString(run.RequestClass, run.RequestAnalysis.RequestClass))
	}
	if class == reviewRequestClassGeneral && session != nil {
		class = finalAnswerCompletenessRequestClass(session)
	}
	status := reviewFinalAnswerContractStatusForClass(class, session, report, reply)
	if status != nil && run != nil {
		status.LifecycleKind = reviewLifecycleKindForRun(run)
	}
	return status
}

func reviewFinalAnswerContractStatusForClass(class string, session *Session, report *CodingHarnessReport, reply string) *ReviewFinalAnswerContractStatus {
	class = normalizeReviewRequestClass(class)
	contract := reviewLifecycleContractForClass(class)
	if contract == nil {
		return nil
	}
	status := &ReviewFinalAnswerContractStatus{
		RequestClass:  class,
		LifecycleKind: reviewFinalAnswerContractLifecycleKindForClass(class, session),
		Status:        reviewFinalAnswerContractStatusPending,
		Reason:        "final answer has not been accepted against the class-specific contract yet",
	}
	if class == reviewRequestClassGeneral {
		status.Status = reviewFinalAnswerContractStatusNotApplicable
		status.Reason = "general request class has only generic final-answer obligations"
		return status
	}
	for _, requirement := range contract.FinalAnswerRequirements {
		status.Requirements = append(status.Requirements, ReviewFinalAnswerRequirementStatus{
			Requirement: requirement,
			Status:      reviewTimelineStatusPending,
			Reason:      "waiting for final-answer disclosure",
		})
	}
	if session != nil && class == reviewRequestClassDocumentArtifact {
		if path := firstDocumentArtifactPathForStatus(session, reply); path != "" {
			status.ArtifactPath = path
		}
		status.ArtifactQualityStatus = reviewRuntimeGateDocumentStatus(session, class)
	}
	if report != nil && class == reviewRequestClassDocumentArtifact {
		if status.ArtifactPath == "" {
			status.ArtifactPath = documentArtifactPathFromReport(report)
		}
		if status.ArtifactQualityStatus == "" || status.ArtifactQualityStatus == "unknown" {
			status.ArtifactQualityStatus = documentArtifactQualityStatusFromReport(report)
		}
	}
	if strings.TrimSpace(reply) != "" {
		reviewFinalAnswerContractApplyReply(status, class, session, reply)
		return status
	}
	if report != nil {
		copyReport := *report
		copyReport.Normalize()
		if copyReport.FinalAnswerCorrection != nil {
			correction := *copyReport.FinalAnswerCorrection
			correction.Normalize()
			status.CorrectionRequired = correction.Required
			status.CorrectionAccepted = correction.Corrected
			if correction.Required && !correction.Corrected {
				status.Status = reviewFinalAnswerContractStatusBlocked
				status.Reason = "final-answer contract correction is required"
				reviewFinalAnswerContractBlockRequirements(status, correction.Reasons)
				return status
			}
			if correction.Required && correction.Corrected {
				status.Status = reviewFinalAnswerContractStatusPassed
				status.Reason = "final-answer contract correction was accepted"
				reviewFinalAnswerContractPassRequirements(status, "final_answer_correction")
				return status
			}
		}
		if copyReport.Approved {
			status.Status = reviewFinalAnswerContractStatusPassed
			status.Reason = "pre-final coding harness approved the final-answer contract"
			reviewFinalAnswerContractPassRequirements(status, "coding_harness")
			return status
		}
		if codingHarnessReportRequiresFinalAnswerOnlyRevision(&copyReport) {
			status.Status = reviewFinalAnswerContractStatusBlocked
			status.Reason = "pre-final coding harness requires final-answer-only correction"
			reviewFinalAnswerContractBlockRequirements(status, []string{"final_answer_contract"})
			return status
		}
	}
	return status
}

func reviewFinalAnswerContractLifecycleKindForClass(class string, session *Session) string {
	if session != nil && session.AcceptanceContract != nil {
		if kind := normalizeReviewLifecycleKind(session.AcceptanceContract.LifecycleKind); kind != reviewLifecycleKindGeneral {
			return kind
		}
		if request := strings.TrimSpace(session.AcceptanceContract.SourcePrompt); request != "" {
			readOnly := strings.EqualFold(strings.TrimSpace(session.AcceptanceContract.Mode), "analysis_only") ||
				prefersReadOnlyAnalysisIntent(request) ||
				looksLikeReviewInspectionOnlyRequest(request)
			explicitEdit := strings.EqualFold(strings.TrimSpace(session.AcceptanceContract.Mode), "edit") ||
				looksLikeExplicitEditIntent(request)
			decision := classifyAcceptanceContractRequestClassDecision(request, classifyTurnIntent(request), readOnly, explicitEdit)
			if normalizedClass := normalizeReviewRequestClass(class); normalizedClass != reviewRequestClassGeneral {
				decision.RequestClass = normalizedClass
			}
			decision = applyReviewLifecycleKindToDecision(decision, request, classifyTurnIntent(request), "", session.AcceptanceContract.Mode)
			if kind := normalizeReviewLifecycleKind(decision.LifecycleKind); kind != reviewLifecycleKindGeneral {
				return kind
			}
		}
	}
	return reviewLifecycleKindForRequestClass(class)
}

func reviewFinalAnswerContractApplyReply(status *ReviewFinalAnswerContractStatus, class string, session *Session, reply string) {
	if status == nil {
		return
	}
	generic := finalAnswerLooksGenericCompletionOnly(reply)
	status.GenericCompletionRejected = generic
	switch class {
	case reviewRequestClassDocumentArtifact:
		status.ArtifactPath = firstDocumentArtifactPathForStatus(session, reply)
		status.ArtifactQualityStatus = boolRequirementStatus(replyMentionsArtifactQualityStatus(reply))
		status.VerificationLimitation = boolRequirementStatus(replyMentionsVerificationOutcome(reply))
		status.RemainingLimitation = boolRequirementStatus(replyMentionsDocumentArtifactLimitation(reply))
		reviewFinalAnswerContractSetRequirement(status, "artifact path", status.ArtifactPath != "", "document_artifact_path")
		reviewFinalAnswerContractSetRequirement(status, "artifact-quality status", replyMentionsArtifactQualityStatus(reply), "final_answer")
		reviewFinalAnswerContractSetRequirement(status, "verification limitation or result", replyMentionsVerificationOutcome(reply), "final_answer")
		reviewFinalAnswerContractSetRequirement(status, "remaining limitation", replyMentionsDocumentArtifactLimitation(reply), "final_answer")
	case reviewRequestClassReviewOnly:
		reviewFinalAnswerContractSetRequirement(status, "findings first", replyLooksFindingsFirst(reply), "final_answer")
		reviewFinalAnswerContractSetRequirement(status, "no file edits", replyClaimsNoFileChanges(strings.ToLower(reply)), "final_answer")
		reviewFinalAnswerContractSetRequirement(status, "residual evidence or verification risk", replyMentionsResidualEvidenceRisk(reply) || replyMentionsVerificationOutcome(reply), "final_answer")
	case reviewRequestClassReviewThenModify, reviewRequestClassModifyThenReview:
		changed := finalAnswerCompletenessChangedPaths(session)
		reviewFinalAnswerContractSetRequirement(status, "changed files", len(changed) == 0 || replyMentionsChangedFileSummary(reply, changed), "patch_transaction")
		reviewFinalAnswerContractSetRequirement(status, "post-change review or self-review result", replyMentionsReviewResult(reply), "final_answer")
		reviewFinalAnswerContractSetRequirement(status, "validation result or explicit gap", replyMentionsValidationResultForCompleteness(reply), "final_answer")
		reviewFinalAnswerContractSetRequirement(status, "residual risk", replyMentionsRemainingRisk(reply) || replyMentionsVerificationBlocker(reply), "final_answer")
	case reviewRequestClassVerificationOnly:
		reviewFinalAnswerContractSetRequirement(status, "verification command or source", replyMentionsVerificationOutcome(reply), "final_answer")
		reviewFinalAnswerContractSetRequirement(status, "pass/fail/skipped outcome", replyMentionsVerificationOutcome(reply), "final_answer")
		reviewFinalAnswerContractSetRequirement(status, "remaining verification gap", replyMentionsResidualEvidenceRisk(reply) || replyMentionsVerificationOutcome(reply), "final_answer")
	case reviewRequestClassValidationOnly:
		reviewFinalAnswerContractSetRequirement(status, "validation decision", replyMentionsValidationResultForCompleteness(reply), "final_answer")
		reviewFinalAnswerContractSetRequirement(status, "evidence checked", replyMentionsResidualEvidenceRisk(reply) || replyMentionsReviewResult(reply), "final_answer")
		reviewFinalAnswerContractSetRequirement(status, "remaining limitation", replyMentionsRemainingRisk(reply) || replyMentionsVerificationBlocker(reply), "final_answer")
	}
	if generic {
		status.Status = reviewFinalAnswerContractStatusBlocked
		status.Reason = "generic completion wording is not sufficient evidence for the final-answer contract"
		return
	}
	blocked := false
	for _, requirement := range status.Requirements {
		if requirement.Status == reviewTimelineStatusBlocked {
			blocked = true
			break
		}
	}
	if blocked {
		status.Status = reviewFinalAnswerContractStatusBlocked
		status.Reason = "one or more final-answer contract disclosures are missing"
	} else {
		status.Status = reviewFinalAnswerContractStatusPassed
		status.Reason = "reply satisfies the visible final-answer contract checks"
	}
}

func (a *Agent) finalAnswerContractStatusForReply(reply string, attemptedEditTool bool) *ReviewFinalAnswerContractStatus {
	if a == nil || a.Session == nil {
		return nil
	}
	status := reviewFinalAnswerContractStatusForClass(finalAnswerCompletenessRequestClass(a.Session), a.Session, a.Session.LastCodingHarnessReport, reply)
	if status == nil {
		return nil
	}
	findings := a.finalAnswerCompletenessFindings(reply, attemptedEditTool)
	if len(findings) > 0 {
		status.Status = reviewFinalAnswerContractStatusBlocked
		status.Reason = "final-answer completeness findings block the class-specific contract"
		for _, finding := range findings {
			if !strings.EqualFold(strings.TrimSpace(finding.Severity), "blocker") {
				continue
			}
			reason := finalAnswerCorrectionReasonForFinding(finding)
			if reason == "" {
				reason = strings.TrimSpace(finding.Title)
			}
			reviewFinalAnswerContractBlockRequirements(status, []string{reason})
		}
	}
	return status
}

func reviewFinalAnswerContractSetRequirement(status *ReviewFinalAnswerContractStatus, requirement string, passed bool, evidence string) {
	if status == nil {
		return
	}
	for i := range status.Requirements {
		if !strings.EqualFold(strings.TrimSpace(status.Requirements[i].Requirement), strings.TrimSpace(requirement)) &&
			!strings.Contains(strings.ToLower(status.Requirements[i].Requirement), strings.ToLower(requirement)) &&
			!strings.Contains(strings.ToLower(requirement), strings.ToLower(status.Requirements[i].Requirement)) {
			continue
		}
		if passed {
			status.Requirements[i].Status = reviewTimelineStatusPassed
			status.Requirements[i].Reason = "disclosed"
			status.Requirements[i].EvidenceRef = evidence
		} else {
			status.Requirements[i].Status = reviewTimelineStatusBlocked
			status.Requirements[i].Reason = "missing disclosure"
			status.Requirements[i].EvidenceRef = evidence
		}
		return
	}
	item := ReviewFinalAnswerRequirementStatus{
		Requirement: requirement,
		EvidenceRef: evidence,
	}
	if passed {
		item.Status = reviewTimelineStatusPassed
		item.Reason = "disclosed"
	} else {
		item.Status = reviewTimelineStatusBlocked
		item.Reason = "missing disclosure"
	}
	status.Requirements = append(status.Requirements, item)
}

func reviewFinalAnswerContractPassRequirements(status *ReviewFinalAnswerContractStatus, evidence string) {
	if status == nil {
		return
	}
	for i := range status.Requirements {
		status.Requirements[i].Status = reviewTimelineStatusPassed
		status.Requirements[i].Reason = "satisfied"
		status.Requirements[i].EvidenceRef = evidence
	}
}

func reviewFinalAnswerContractBlockRequirements(status *ReviewFinalAnswerContractStatus, reasons []string) {
	if status == nil {
		return
	}
	lowerReasons := strings.ToLower(strings.Join(reasons, " "))
	for i := range status.Requirements {
		req := strings.ToLower(status.Requirements[i].Requirement)
		if lowerReasons == "" ||
			strings.Contains(lowerReasons, req) ||
			strings.Contains(req, "changed") && strings.Contains(lowerReasons, "changed") ||
			strings.Contains(req, "review") && strings.Contains(lowerReasons, "review") ||
			strings.Contains(req, "validation") && strings.Contains(lowerReasons, "validation") ||
			strings.Contains(req, "verification") && strings.Contains(lowerReasons, "verification") ||
			strings.Contains(req, "risk") && strings.Contains(lowerReasons, "risk") ||
			strings.Contains(req, "artifact") && strings.Contains(lowerReasons, "artifact") {
			status.Requirements[i].Status = reviewTimelineStatusBlocked
			status.Requirements[i].Reason = "missing or corrected by final-answer contract"
			status.Requirements[i].EvidenceRef = "coding_harness"
		}
	}
}

func finalAnswerLooksGenericCompletionOnly(reply string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(reply)), " "))
	if lower == "" {
		return false
	}
	words := strings.Fields(lower)
	if len(words) > 18 {
		return false
	}
	if !containsAny(lower, "done", "complete", "completed", "patched", "implemented", "fixed", "완료", "구현", "수정했습니다", "끝났") {
		return false
	}
	return !containsAny(lower,
		"changed file", "changed files", "modified", "review", "self-review", "validation", "verification", "risk", "artifact", "path", "blocked", "not run",
		"변경 파일", "수정 파일", "리뷰", "검토", "검증", "위험", "리스크", "산출물", "문서", "경로", "차단", "미실행",
	)
}

func firstDocumentArtifactPathForStatus(session *Session, reply string) string {
	paths := documentArtifactFinalAnswerPaths(session, reply)
	if len(paths) == 0 {
		return ""
	}
	return filepath.ToSlash(paths[0])
}

func documentArtifactPathFromReport(report *CodingHarnessReport) string {
	if report == nil {
		return ""
	}
	copyReport := *report
	copyReport.Normalize()
	for _, artifact := range copyReport.ArtifactQuality.Artifacts {
		if path := normalizeSessionRelativePath(artifact.Path); path != "" {
			return filepath.ToSlash(path)
		}
	}
	return ""
}

func documentArtifactQualityStatusFromReport(report *CodingHarnessReport) string {
	if report == nil {
		return ""
	}
	copyReport := *report
	copyReport.Normalize()
	if len(copyReport.ArtifactQuality.Artifacts) == 0 {
		return "pending"
	}
	if codingHarnessFindingsHaveBlockers(copyReport.ArtifactQuality.Findings) {
		return "blocked"
	}
	for _, artifact := range copyReport.ArtifactQuality.Artifacts {
		if !artifactQualityDocumentArtifactLooksAccepted(artifact) {
			return "pending"
		}
	}
	return "accepted"
}

func boolRequirementStatus(ok bool) string {
	if ok {
		return reviewTimelineStatusPassed
	}
	return reviewTimelineStatusBlocked
}

func reviewSecondPassState(status string, ran bool, cacheHit bool, crossModelRan bool, reviewerOnly bool, skippedReason string) string {
	if crossModelRan {
		return "cross_model_review_ran"
	}
	if reviewerOnly {
		return "reviewer_only_post_change_review_used"
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if cacheHit {
		return "single_model_second_pass_cached"
	}
	if ran {
		return "single_model_second_pass_ran"
	}
	if status == "skipped" {
		return "single_model_second_pass_skipped"
	}
	if strings.TrimSpace(skippedReason) != "" {
		return "single_model_second_pass_skipped"
	}
	return status
}

func reviewSecondPassSkippedIsEvidenceGap(state string, reason string) bool {
	state = strings.TrimSpace(state)
	if state != "single_model_second_pass_skipped" {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(reason))
	return !containsAny(lower, "not required", "not_applicable", "cross-review route ran", "cross review route ran", "policy")
}

func reviewTimelinePhaseForPreWrite(run ReviewRun) ReviewLifecyclePhase {
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		return ReviewLifecyclePhase{
			Phase:          reviewLifecyclePhasePreWriteReview,
			Status:         reviewTimelineStatusSkipped,
			Reason:         "this review run was not a pre-write gate",
			NextSafeAction: "run /review change before writing when a frozen diff needs review",
			NextCommand:    "/review change",
		}
	}
	status := reviewTimelineStatusFromVerdict(firstNonBlankString(run.Gate.Verdict, run.Result.Verdict))
	return ReviewLifecyclePhase{
		Phase:          reviewLifecyclePhasePreWriteReview,
		Status:         status,
		Reason:         firstNonBlankString(run.Gate.Reason, run.Result.Summary, "pre-write review gate evaluated the proposed diff"),
		EvidenceRef:    firstNonBlankString("review:"+run.ID, firstArtifactRef(run)),
		NextSafeAction: reviewGateNextSafeAction(status),
		NextCommand:    reviewLifecycleNextCommand(run.Gate.NextCommands),
	}
}

func reviewTimelinePhaseForApplyingChange(run ReviewRun) ReviewLifecyclePhase {
	if len(run.ChangeSet.ChangedPaths) > 0 {
		return ReviewLifecyclePhase{
			Phase:       reviewLifecyclePhaseApplyingChange,
			Status:      reviewTimelineStatusPassed,
			Reason:      fmt.Sprintf("%d changed path(s) are recorded", len(run.ChangeSet.ChangedPaths)),
			EvidenceRef: "change_set",
		}
	}
	if reviewRunLooksReadOnlyAnalysis(run) {
		return ReviewLifecyclePhase{
			Phase:          reviewLifecyclePhaseApplyingChange,
			Status:         reviewTimelineStatusSkipped,
			Reason:         "request is read-only",
			NextSafeAction: "do not edit files unless the user starts a follow-up modification request",
		}
	}
	return ReviewLifecyclePhase{
		Phase:  reviewLifecyclePhaseApplyingChange,
		Status: reviewTimelineStatusPending,
		Reason: "no changed path is recorded yet",
	}
}

func reviewTimelinePhaseForPostChange(run ReviewRun) ReviewLifecyclePhase {
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "post_change") ||
		(strings.EqualFold(strings.TrimSpace(run.Target), reviewTargetChange) && !strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write")) {
		status := reviewTimelineStatusFromVerdict(firstNonBlankString(run.Gate.Verdict, run.Result.Verdict))
		return ReviewLifecyclePhase{
			Phase:          reviewLifecyclePhasePostChangeReview,
			Status:         status,
			Reason:         firstNonBlankString(run.Gate.Reason, run.Result.Summary, "post-change review gate evaluated the changed files"),
			EvidenceRef:    firstNonBlankString("review:"+run.ID, firstArtifactRef(run)),
			NextSafeAction: reviewGateNextSafeAction(status),
			NextCommand:    reviewLifecycleNextCommand(run.Gate.NextCommands),
		}
	}
	return ReviewLifecyclePhase{
		Phase:          reviewLifecyclePhasePostChangeReview,
		Status:         reviewTimelineStatusSkipped,
		Reason:         "this run did not execute a post-change review",
		NextSafeAction: "run /review change after source modifications when no fresh review exists",
		NextCommand:    "/review change",
	}
}

func reviewTimelinePhaseForSecondPass(second *ReviewSecondPassObservability) ReviewLifecyclePhase {
	if second == nil {
		return ReviewLifecyclePhase{
			Phase:          reviewLifecyclePhaseSingleModelSecondPass,
			Status:         reviewTimelineStatusSkipped,
			Reason:         "single-model second pass state is unavailable",
			NextSafeAction: "rerun /review if second-pass evidence is required",
			NextCommand:    "/review",
		}
	}
	status := reviewTimelineStatusPassed
	if second.EvidenceGap {
		status = reviewTimelineStatusWarned
	}
	if second.State == "single_model_second_pass_skipped" {
		status = reviewTimelineStatusSkipped
	}
	return ReviewLifecyclePhase{
		Phase:          reviewLifecyclePhaseSingleModelSecondPass,
		Status:         status,
		Reason:         firstNonBlankString(second.SkippedReason, second.State),
		EvidenceRef:    firstNonBlankString(second.RawOutputRef, second.PromptRef),
		NextSafeAction: reviewSecondPassNextSafeAction(second),
		NextCommand:    reviewSecondPassNextCommand(second),
	}
}

func reviewTimelinePhaseForTriage(summary *ReviewCrossReviewTriageSummary) ReviewLifecyclePhase {
	if summary == nil {
		return ReviewLifecyclePhase{
			Phase:          reviewLifecyclePhaseCrossReviewTriage,
			Status:         reviewTimelineStatusSkipped,
			Reason:         "no cross-review triage items were produced",
			NextSafeAction: "no action unless a separate reviewer produces findings",
		}
	}
	status := reviewTimelineStatusPassed
	if summary.IncompleteCount > 0 || summary.UserActionNeeded {
		status = reviewTimelineStatusBlocked
	}
	return ReviewLifecyclePhase{
		Phase:          reviewLifecyclePhaseCrossReviewTriage,
		Status:         status,
		Reason:         reviewCrossReviewTriageStatusLine(summary),
		EvidenceRef:    "cross_review_triage",
		NextSafeAction: reviewTriageNextSafeAction(summary),
		NextCommand:    reviewTriageNextCommand(summary),
	}
}

func reviewTimelinePhaseForDocumentGate(class string, status string, session *Session, report *CodingHarnessReport) ReviewLifecyclePhase {
	if normalizeReviewRequestClass(class) != reviewRequestClassDocumentArtifact {
		return ReviewLifecyclePhase{
			Phase:          reviewLifecyclePhaseArtifactQualityGate,
			Status:         reviewTimelineStatusSkipped,
			Reason:         "request class is not document_artifact",
			NextSafeAction: "no document artifact-quality gate is required",
		}
	}
	timelineStatus := reviewTimelineStatusPending
	switch strings.TrimSpace(status) {
	case "accepted":
		timelineStatus = reviewTimelineStatusPassed
	case "blocked":
		timelineStatus = reviewTimelineStatusBlocked
	case "pending":
		timelineStatus = reviewTimelineStatusPending
	default:
		timelineStatus = reviewTimelineStatusPending
	}
	return ReviewLifecyclePhase{
		Phase:          reviewLifecyclePhaseArtifactQualityGate,
		Status:         timelineStatus,
		Reason:         "document artifact-quality status is " + valueOrDefault(status, "unknown"),
		EvidenceRef:    documentArtifactEvidenceRef(session, report),
		NextSafeAction: documentArtifactNextSafeAction(timelineStatus),
		NextCommand:    documentArtifactNextCommand(timelineStatus),
	}
}

func reviewTimelinePhaseForVerification(status string, run ReviewRun, ledger *RuntimeGateLedger) ReviewLifecyclePhase {
	timelineStatus := reviewTimelineStatusFromVerification(status)
	return ReviewLifecyclePhase{
		Phase:          reviewLifecyclePhaseVerification,
		Status:         timelineStatus,
		Reason:         "verification gate status is " + valueOrDefault(status, "unknown"),
		EvidenceRef:    firstNonBlankString(runtimeGateVerificationEvidenceRef(ledger), run.Evidence.VerificationSummary),
		NextSafeAction: verificationNextSafeAction(timelineStatus),
		NextCommand:    verificationNextCommand(timelineStatus),
	}
}

func reviewRuntimeVerificationTimelinePhase(session *Session, ledger *RuntimeGateLedger) ReviewLifecyclePhase {
	status := "not_required"
	if ledger != nil {
		for _, blocker := range ledger.Blockers {
			if reviewBlockerClassForText(blocker) == reviewBlockerClassVerificationGap {
				status = "gap_recorded"
				break
			}
		}
		if status == "not_required" {
			for _, warning := range ledger.Warnings {
				if reviewBlockerClassForText(warning) == reviewBlockerClassVerificationGap {
					status = "gap_recorded"
					break
				}
			}
		}
		if ledger.VerificationReportID != "" {
			status = "recorded"
		}
	}
	if session != nil && session.LastVerification != nil && session.LastVerification.WasSkipped() {
		status = "skipped"
	}
	return ReviewLifecyclePhase{
		Phase:          reviewLifecyclePhaseVerification,
		Status:         reviewTimelineStatusFromVerification(status),
		Reason:         "verification gate status is " + status,
		EvidenceRef:    runtimeGateVerificationEvidenceRef(ledger),
		NextSafeAction: verificationNextSafeAction(reviewTimelineStatusFromVerification(status)),
		NextCommand:    verificationNextCommand(reviewTimelineStatusFromVerification(status)),
	}
}

func reviewTimelinePhaseForFinalAnswerContract(status *ReviewFinalAnswerContractStatus) ReviewLifecyclePhase {
	if status == nil {
		return ReviewLifecyclePhase{
			Phase:  reviewLifecyclePhaseFinalAnswerContract,
			Status: reviewTimelineStatusSkipped,
			Reason: "no specialized final-answer contract is active",
		}
	}
	timelineStatus := reviewTimelineStatusPending
	switch status.Status {
	case reviewFinalAnswerContractStatusPassed:
		timelineStatus = reviewTimelineStatusPassed
	case reviewFinalAnswerContractStatusBlocked:
		timelineStatus = reviewTimelineStatusBlocked
	case reviewFinalAnswerContractStatusNotApplicable:
		timelineStatus = reviewTimelineStatusSkipped
	}
	return ReviewLifecyclePhase{
		Phase:          reviewLifecyclePhaseFinalAnswerContract,
		Status:         timelineStatus,
		Reason:         status.Reason,
		EvidenceRef:    "final_answer_contract",
		NextSafeAction: finalAnswerContractNextSafeAction(timelineStatus),
		NextCommand:    finalAnswerContractNextCommand(timelineStatus),
	}
}

func reviewTimelineStatusFromVerdict(verdict string) string {
	switch strings.TrimSpace(verdict) {
	case reviewVerdictApproved:
		return reviewTimelineStatusPassed
	case reviewVerdictApprovedWithWarnings:
		return reviewTimelineStatusWarned
	case reviewVerdictNeedsRevision, reviewVerdictBlocked, reviewVerdictInsufficientEvidence:
		return reviewTimelineStatusBlocked
	default:
		return reviewTimelineStatusPending
	}
}

func reviewTimelineStatusFromVerification(status string) string {
	switch strings.TrimSpace(status) {
	case "recorded", "passed", "accepted":
		return reviewTimelineStatusPassed
	case "failed", "gap_recorded", "blocked":
		return reviewTimelineStatusBlocked
	case "skipped", "skipped_document_artifact_only", "not_required", "not_applicable":
		return reviewTimelineStatusSkipped
	default:
		return reviewTimelineStatusPending
	}
}

func reviewCurrentLifecyclePhase(timeline []ReviewLifecyclePhase, fallback string) string {
	items := normalizeReviewLifecycleTimeline(timeline)
	for _, item := range items {
		if item.Phase == reviewLifecyclePhaseBlocked && item.Status == reviewTimelineStatusBlocked {
			return reviewLifecyclePhaseBlocked
		}
	}
	for _, wantStatus := range []string{
		reviewTimelineStatusRunning,
		reviewTimelineStatusBlocked,
		reviewTimelineStatusPending,
		reviewTimelineStatusWarned,
	} {
		for _, item := range items {
			if item.Status == wantStatus {
				return item.Phase
			}
		}
	}
	for _, item := range items {
		if item.Phase == reviewLifecyclePhaseCompleted {
			return reviewLifecyclePhaseCompleted
		}
	}
	return strings.TrimSpace(fallback)
}

func reviewContextTimelineStatus(run ReviewRun) string {
	if len(run.Evidence.Sources) > 0 || strings.TrimSpace(run.Evidence.Text) != "" || len(run.ChangeSet.ChangedPaths) > 0 {
		return reviewTimelineStatusPassed
	}
	return reviewTimelineStatusPending
}

func reviewContextTimelineReason(run ReviewRun) string {
	if len(run.Evidence.Sources) > 0 {
		return fmt.Sprintf("%d evidence source(s) collected", len(run.Evidence.Sources))
	}
	if len(run.ChangeSet.ChangedPaths) > 0 {
		return fmt.Sprintf("%d changed path(s) collected", len(run.ChangeSet.ChangedPaths))
	}
	return "waiting for local context or supplied diff/code"
}

func reviewContextEvidenceRef(run ReviewRun) string {
	if len(run.Evidence.Sources) > 0 {
		return run.Evidence.Sources[0]
	}
	if len(run.ChangeSet.ChangedPaths) > 0 {
		return "change_set"
	}
	return ""
}

func reviewRuntimeApplyingChangeStatus(changedPaths []string) string {
	if len(changedPaths) == 0 {
		return reviewTimelineStatusSkipped
	}
	return reviewTimelineStatusPassed
}

func reviewRuntimeApplyingChangeReason(changedPaths []string) string {
	if len(changedPaths) == 0 {
		return "no changed paths are recorded"
	}
	return fmt.Sprintf("%d changed path(s) are recorded", len(changedPaths))
}

func reviewRuntimeReviewStatus(ledger *RuntimeGateLedger) string {
	if ledger == nil || ledger.ReviewRunID == "" {
		return reviewTimelineStatusPending
	}
	if len(ledger.Blockers) > 0 {
		return reviewTimelineStatusBlocked
	}
	if len(ledger.Warnings) > 0 {
		return reviewTimelineStatusWarned
	}
	return reviewTimelineStatusPassed
}

func reviewRuntimeReviewReason(ledger *RuntimeGateLedger) string {
	if ledger == nil {
		return "runtime gate has no review ledger"
	}
	if ledger.ReviewRunID == "" {
		return "no latest review run covers current changed files"
	}
	return "latest review run is " + runtimeGateReviewFreshnessLabel(*ledger)
}

func reviewRuntimeReviewEvidence(ledger *RuntimeGateLedger) string {
	if ledger == nil || ledger.ReviewRunID == "" {
		return ""
	}
	return "review:" + ledger.ReviewRunID
}

func reviewRuntimeReviewNextAction(ledger *RuntimeGateLedger) string {
	if ledger == nil || ledger.ReviewRunID == "" || len(ledger.Blockers) > 0 {
		return "run or rerun /review before final answer or write-side actions"
	}
	return ""
}

func reviewRuntimeReviewNextCommand(ledger *RuntimeGateLedger) string {
	if ledger == nil {
		return "/review"
	}
	if cmd := reviewLifecycleNextCommand(ledger.NextCommands); cmd != "" {
		return cmd
	}
	if ledger.ReviewRunID == "" || len(ledger.Blockers) > 0 {
		return "/review"
	}
	return ""
}

func reviewCompletedTimelineStatus(run ReviewRun, ledger *RuntimeGateLedger) string {
	if ledger != nil {
		if len(ledger.Blockers) > 0 {
			return reviewTimelineStatusBlocked
		}
		if len(ledger.Warnings) > 0 {
			return reviewTimelineStatusWarned
		}
	}
	return reviewTimelineStatusFromVerdict(firstNonBlankString(run.Gate.Verdict, run.Result.Verdict))
}

func reviewCompletedTimelineReason(run ReviewRun, ledger *RuntimeGateLedger) string {
	if ledger != nil && ledger.Status != "" {
		return "runtime gate status is " + ledger.Status
	}
	return "review gate verdict is " + firstNonBlankString(run.Gate.Verdict, run.Result.Verdict, "unknown")
}

func reviewRuntimeCompletedReason(ledger *RuntimeGateLedger) string {
	if ledger == nil {
		return "runtime status has not been evaluated"
	}
	return "runtime gate status is " + valueOrDefault(ledger.Status, "unknown")
}

func reviewGateNextSafeAction(status string) string {
	switch status {
	case reviewTimelineStatusBlocked:
		return "repair or decide the blocker before writing or finalizing"
	case reviewTimelineStatusWarned:
		return "disclose warnings and run the next recommended gate"
	case reviewTimelineStatusPassed:
		return "continue to the next lifecycle phase"
	default:
		return ""
	}
}

func reviewSecondPassNextSafeAction(second *ReviewSecondPassObservability) string {
	if second == nil {
		return ""
	}
	if second.EvidenceGap {
		return "disclose the skipped single-model second pass as an evidence gap or rerun /review"
	}
	if second.State == "cross_model_review_ran" {
		return "use the independent cross-review evidence instead of presenting second pass as independent"
	}
	return ""
}

func reviewSecondPassNextCommand(second *ReviewSecondPassObservability) string {
	if second != nil && second.EvidenceGap {
		return "/review"
	}
	return ""
}

func reviewTriageNextSafeAction(summary *ReviewCrossReviewTriageSummary) string {
	if summary == nil || (!summary.UserActionNeeded && summary.IncompleteCount == 0) {
		return ""
	}
	return "inspect needs_user_decision or incomplete triage items before continuing"
}

func reviewTriageNextCommand(summary *ReviewCrossReviewTriageSummary) string {
	if summary == nil || (!summary.UserActionNeeded && summary.IncompleteCount == 0) {
		return ""
	}
	return "/continuity continue from review"
}

func documentArtifactEvidenceRef(session *Session, report *CodingHarnessReport) string {
	if report != nil && len(report.ArtifactQuality.Artifacts) > 0 {
		return "artifact_quality"
	}
	if path := firstDocumentArtifactPathForStatus(session, ""); path != "" {
		return path
	}
	return ""
}

func documentArtifactNextSafeAction(status string) string {
	switch status {
	case reviewTimelineStatusBlocked:
		return "fix the document artifact or disclose the artifact-quality blocker"
	case reviewTimelineStatusPending:
		return "run the artifact-quality gate before final answer"
	default:
		return ""
	}
}

func documentArtifactNextCommand(status string) string {
	if status == reviewTimelineStatusBlocked || status == reviewTimelineStatusPending {
		return "/status detail"
	}
	return ""
}

func verificationNextSafeAction(status string) string {
	switch status {
	case reviewTimelineStatusBlocked:
		return "run focused verification or disclose the verification gap"
	case reviewTimelineStatusSkipped:
		return "disclose why build/test verification was skipped if finalizing"
	default:
		return ""
	}
}

func verificationNextCommand(status string) string {
	if status == reviewTimelineStatusBlocked {
		return "/verify --full"
	}
	return ""
}

func finalAnswerContractNextSafeAction(status string) string {
	switch status {
	case reviewTimelineStatusBlocked:
		return "revise the final answer disclosure without claiming generic completion"
	case reviewTimelineStatusPending:
		return "prepare a final answer that names class-specific evidence and limitations"
	default:
		return ""
	}
}

func finalAnswerContractNextCommand(status string) string {
	if status == reviewTimelineStatusBlocked || status == reviewTimelineStatusPending {
		return "/status detail"
	}
	return ""
}

func runtimeGateEvidenceRef(ledger *RuntimeGateLedger) string {
	if ledger == nil || strings.TrimSpace(ledger.ID) == "" {
		return ""
	}
	return "runtime_gate:" + strings.TrimSpace(ledger.ID)
}

func runtimeGateVerificationEvidenceRef(ledger *RuntimeGateLedger) string {
	if ledger == nil || strings.TrimSpace(ledger.VerificationReportID) == "" {
		return ""
	}
	return strings.TrimSpace(ledger.VerificationReportID)
}

func firstArtifactRef(run ReviewRun) string {
	if len(run.ArtifactRefs) == 0 {
		return ""
	}
	return filepath.ToSlash(strings.TrimSpace(run.ArtifactRefs[0]))
}

func reviewLifecyclePhaseLine(item ReviewLifecyclePhase) string {
	item.Phase = strings.TrimSpace(item.Phase)
	item.Status = normalizeReviewTimelineStatus(item.Status)
	parts := []string{item.Phase + "=" + valueOrDefault(item.Status, reviewTimelineStatusPending)}
	if strings.TrimSpace(item.Reason) != "" {
		parts = append(parts, "reason="+compactPromptSection(item.Reason, 120))
	}
	if strings.TrimSpace(item.EvidenceRef) != "" {
		parts = append(parts, "evidence="+item.EvidenceRef)
	}
	if strings.TrimSpace(item.NextSafeAction) != "" {
		parts = append(parts, "next_safe_action="+compactPromptSection(item.NextSafeAction, 120))
	}
	if strings.TrimSpace(item.NextCommand) != "" {
		parts = append(parts, "next_command="+item.NextCommand)
	}
	return strings.Join(parts, " ")
}

func statusCommandDetailRequested(args string) bool {
	for _, field := range splitCommandFields(args) {
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "detail", "details", "--detail", "-v", "--verbose":
			return true
		}
	}
	return false
}

func compactRequestClass(status *ReviewCompactStatus) string {
	if status == nil {
		return ""
	}
	return status.RequestClass
}

func reviewCompactLifecycleKind(status *ReviewCompactStatus) string {
	if status == nil {
		return ""
	}
	return status.LifecycleKind
}

func reviewOperatorProgressSuffix(phase string, status string, reason string, waitingOn string, next string) string {
	parts := []string{}
	if phase = strings.TrimSpace(phase); phase != "" {
		parts = append(parts, "phase="+phase)
	}
	if status = strings.TrimSpace(status); status != "" {
		parts = append(parts, "status="+status)
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		parts = append(parts, "reason="+sanitizeProgressToken(reason, 96))
	}
	if waitingOn = strings.TrimSpace(waitingOn); waitingOn != "" {
		parts = append(parts, "waiting_on="+waitingOn)
	}
	if next = strings.TrimSpace(next); next != "" {
		parts = append(parts, "next="+next)
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func sanitizeProgressToken(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	text = strings.ReplaceAll(text, "[", "(")
	text = strings.ReplaceAll(text, "]", ")")
	text = strings.ReplaceAll(text, "\n", " ")
	return compactPromptSection(text, limit)
}

func reviewPipelinePhaseForStep(step int) string {
	switch step {
	case 1:
		return reviewLifecyclePhaseCollectingContext
	case 2:
		return reviewLifecyclePhaseCollectingContext
	case 3:
		return "model_review"
	case 4:
		return "cross_review_triage"
	case 5:
		return "review_gate"
	case 6:
		return "next_safe_action"
	default:
		return "review_pipeline"
	}
}

func reviewPipelineWaitingOnForStep(step int) string {
	switch step {
	case 1, 2:
		return "local_tool"
	case 3:
		return "primary_model"
	case 4:
		return "cross_reviewer"
	case 5:
		return "local_gate"
	case 6:
		return "operator_action"
	default:
		return ""
	}
}

func repairWorkflowPhaseForStep(step int) string {
	switch step {
	case 1:
		return reviewLifecyclePhasePreWriteReview
	case 2:
		return "repairing_patch"
	case 3:
		return reviewLifecyclePhasePreWriteReview
	case 4:
		return reviewLifecyclePhaseApplyingChange
	case 5:
		return reviewLifecyclePhaseVerification
	case 6:
		return reviewLifecyclePhaseFinalAnswerContract
	default:
		return "repair_workflow"
	}
}

func repairWorkflowWaitingOnForStep(step int) string {
	switch step {
	case 1, 3:
		return "reviewer"
	case 2:
		return "primary_model"
	case 4:
		return "user_or_local_tool"
	case 5:
		return "verification_command"
	case 6:
		return "final_answer_correction"
	default:
		return ""
	}
}
