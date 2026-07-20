package main

import (
	"fmt"
	"sort"
	"strings"
)

const (
	reviewObligationTypeRepair        = "repair"
	reviewObligationTypeVerification  = "verification"
	reviewObligationTypeEvidence      = "evidence"
	reviewObligationTypeReviewerRoute = "reviewer_route"

	reviewObligationStatusOpen                 = "open"
	reviewObligationStatusResolved             = "resolved"
	reviewObligationStatusEvidenceUnconfirmed  = "evidence_unconfirmed"
	reviewObligationStatusVerificationRequired = "verification_required"
	reviewObligationStatusRouteUnavailable     = "route_unavailable"
	reviewObligationStatusDisclosedFinal       = "disclosed_in_final_answer"
)

// reviewRepairObligationCandidateFindings filters carried repair findings down
// to concrete repair obligations: review-meta items and non-actionable
// placeholders are user-visible guidance, not obligations that can feed this
// ledger.
func reviewRepairObligationCandidateFindings(findings []ReviewFinding) []ReviewFinding {
	out := make([]ReviewFinding, 0, len(findings))
	for _, finding := range findings {
		finding.Normalize()
		if strings.TrimSpace(finding.ID) == "" {
			continue
		}
		if !preWritePreFixFindingIsConcreteRepairObligation(finding) &&
			!preWritePreFixWarningIsConcreteRepairObligation(finding) &&
			!reviewFindingLooksActionableForRepairGate(finding) {
			continue
		}
		out = append(out, finding)
	}
	return out
}

// reviewRepairFindingsRequiringResolutionStatus narrows carried repair
// findings to the ones the single-model RF-obligation policy may demand a
// resolution status for. This bar is stricter than the obligation-ledger
// candidate filter: evidence_gap/test_gap items are explicitly not code
// repair targets (see formatReviewerGateUnavailableRepairFollowUpFeedback),
// so a carried note-level finding must never arm the deterministic
// "lacks repair obligation status" blocker on its own.
func reviewRepairFindingsRequiringResolutionStatus(findings []ReviewFinding) []ReviewFinding {
	out := make([]ReviewFinding, 0, len(findings))
	for _, finding := range findings {
		finding.Normalize()
		if strings.TrimSpace(finding.ID) == "" {
			continue
		}
		if !preWritePreFixFindingIsConcreteRepairObligation(finding) &&
			!preWritePreFixWarningIsConcreteRepairObligation(finding) {
			continue
		}
		out = append(out, finding)
	}
	return out
}

func buildReviewObligationLedger(run ReviewRun) ReviewObligationLedger {
	var ledger ReviewObligationLedger
	seen := map[string]int{}

	add := func(obligation ReviewObligation) {
		obligation = normalizeReviewObligation(obligation)
		if strings.TrimSpace(obligation.ID) == "" || strings.TrimSpace(obligation.Type) == "" {
			return
		}
		key := reviewObligationKey(obligation)
		if existingIndex, ok := seen[key]; ok {
			ledger.Items[existingIndex] = mergeReviewObligations(ledger.Items[existingIndex], obligation)
			return
		}
		seen[key] = len(ledger.Items)
		ledger.Items = append(ledger.Items, obligation)
	}

	for _, finding := range reviewRepairObligationCandidateFindings(run.RepairFindings) {
		add(reviewObligationFromFinding(run, finding, reviewObligationTypeRepair))
	}

	for _, finding := range run.Findings {
		finding.Normalize()
		obligationType, ok := reviewObligationTypeForFinding(run, finding)
		if !ok {
			continue
		}
		add(reviewObligationFromFinding(run, finding, obligationType))
	}

	sort.SliceStable(ledger.Items, func(i int, j int) bool {
		left := ledger.Items[i]
		right := ledger.Items[j]
		if left.Type != right.Type {
			return reviewObligationTypeRank(left.Type) < reviewObligationTypeRank(right.Type)
		}
		return strings.ToLower(left.ID) < strings.ToLower(right.ID)
	})
	ledger.TotalCount = len(ledger.Items)
	for _, obligation := range ledger.Items {
		if !reviewObligationStatusIsOpen(obligation.Status) {
			continue
		}
		ledger.OpenCount++
		switch normalizeReviewObligationType(obligation.Type) {
		case reviewObligationTypeRepair:
			if reviewObligationStatusRequiresRepair(obligation.Status) {
				ledger.OpenRepairCount++
			} else if normalizeReviewObligationStatus(obligation.Status) == reviewObligationStatusVerificationRequired {
				ledger.OpenVerificationCount++
			}
		case reviewObligationTypeVerification:
			ledger.OpenVerificationCount++
		case reviewObligationTypeEvidence:
			ledger.OpenEvidenceCount++
		case reviewObligationTypeReviewerRoute:
			ledger.OpenRouteCount++
		}
	}
	ledger.Summary = reviewObligationLedgerSummary(ledger)
	return ledger
}

func reviewObligationTypeForFinding(run ReviewRun, finding ReviewFinding) (string, bool) {
	finding.Normalize()
	if strings.EqualFold(strings.TrimSpace(finding.ID), requiredReviewerFailureFindingID) {
		return reviewObligationTypeReviewerRoute, true
	}
	if reviewFindingLooksReviewMetaOnly(finding) {
		return "", false
	}
	if strings.EqualFold(strings.TrimSpace(finding.Category), "evidence_gap") {
		return reviewObligationTypeEvidence, true
	}
	if reviewFindingLooksImplementationRepairDespiteGapCategory(finding) {
		return reviewObligationTypeRepair, true
	}
	if strings.EqualFold(strings.TrimSpace(finding.Category), "test_gap") ||
		reviewFindingLooksVerificationObligation(finding) {
		return reviewObligationTypeVerification, true
	}
	if reviewFindingIsActionableNonReviewerFinding(run, finding, nil) ||
		reviewFindingBlocksGate(run, finding) {
		return reviewObligationTypeRepair, true
	}
	return "", false
}

func reviewFindingLooksVerificationObligation(finding ReviewFinding) bool {
	finding.Normalize()
	if !reviewFindingLooksVerificationOnly(finding) {
		return false
	}
	requiredFix := strings.TrimSpace(finding.RequiredFix)
	if requiredFix == "" {
		return true
	}
	if reviewTextLooksVerificationOnlyAction(requiredFix) {
		return true
	}
	return !reviewTextLooksImplementationRepairAction(requiredFix)
}

func reviewObligationFromFinding(run ReviewRun, finding ReviewFinding, obligationType string) ReviewObligation {
	finding.Normalize()
	status := reviewObligationStatusForFinding(obligationType, finding)
	return ReviewObligation{
		ID:              reviewObligationIDForFinding(finding, obligationType),
		Type:            obligationType,
		Status:          status,
		SourceFindingID: strings.TrimSpace(finding.ID),
		Severity:        strings.TrimSpace(finding.Severity),
		Category:        strings.TrimSpace(finding.Category),
		ReviewerRole:    strings.TrimSpace(finding.ReviewerRole),
		Title:           strings.TrimSpace(finding.Title),
		RequiredAction:  firstNonBlankString(strings.TrimSpace(finding.RequiredFix), strings.TrimSpace(finding.TestRecommendation)),
		Blocking:        reviewObligationShouldBlock(run, finding, obligationType),
		EvidenceRefs:    normalizeTaskStateList(finding.EvidenceRefs, 12),
		FixRefs:         normalizeTaskStateList(finding.FixRefs, 12),
	}
}

func reviewObligationShouldBlock(run ReviewRun, finding ReviewFinding, obligationType string) bool {
	if obligationType == reviewObligationTypeReviewerRoute {
		return true
	}
	if obligationType == reviewObligationTypeRepair {
		return reviewFindingBlocksGate(run, finding)
	}
	if obligationType == reviewObligationTypeEvidence {
		return reviewFindingBlocksGate(run, finding)
	}
	if obligationType == reviewObligationTypeVerification {
		return reviewFindingBlocksGate(run, finding)
	}
	return false
}

func reviewObligationStatusForFinding(obligationType string, finding ReviewFinding) string {
	status := normalizeReviewObligationStatus(finding.ResolutionStatus)
	if status != "" {
		return status
	}
	switch obligationType {
	case reviewObligationTypeReviewerRoute:
		return reviewObligationStatusRouteUnavailable
	case reviewObligationTypeVerification:
		return reviewObligationStatusVerificationRequired
	case reviewObligationTypeEvidence:
		return reviewObligationStatusEvidenceUnconfirmed
	default:
		return reviewObligationStatusOpen
	}
}

func reviewObligationIDForFinding(finding ReviewFinding, obligationType string) string {
	id := strings.TrimSpace(finding.ID)
	if id != "" {
		return id
	}
	title := compactPromptSection(strings.TrimSpace(finding.Title), 48)
	if title == "" {
		title = compactPromptSection(strings.TrimSpace(finding.RequiredFix), 48)
	}
	if title == "" {
		return ""
	}
	token := normalizeReviewObligationIDToken(title)
	if token == "" {
		token = computeReviewFingerprint(obligationType, title)
	}
	return fmt.Sprintf("OBL-%s-%s", obligationType, token)
}

func normalizeReviewObligationIDToken(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	var b strings.Builder
	lastDash := false
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func normalizeReviewObligation(obligation ReviewObligation) ReviewObligation {
	obligation.ID = strings.TrimSpace(obligation.ID)
	obligation.Type = normalizeReviewObligationType(obligation.Type)
	obligation.Status = normalizeReviewObligationStatus(obligation.Status)
	if obligation.Status == "" {
		obligation.Status = reviewObligationStatusOpen
	}
	obligation.SourceFindingID = strings.TrimSpace(obligation.SourceFindingID)
	obligation.Severity = strings.TrimSpace(obligation.Severity)
	obligation.Category = strings.TrimSpace(obligation.Category)
	obligation.ReviewerRole = strings.TrimSpace(obligation.ReviewerRole)
	obligation.Title = strings.TrimSpace(obligation.Title)
	obligation.RequiredAction = strings.TrimSpace(obligation.RequiredAction)
	obligation.EvidenceRefs = normalizeTaskStateList(obligation.EvidenceRefs, 12)
	obligation.FixRefs = normalizeTaskStateList(obligation.FixRefs, 12)
	return obligation
}

func normalizeReviewObligationType(obligationType string) string {
	switch strings.ToLower(strings.TrimSpace(obligationType)) {
	case reviewObligationTypeRepair:
		return reviewObligationTypeRepair
	case reviewObligationTypeVerification:
		return reviewObligationTypeVerification
	case reviewObligationTypeEvidence:
		return reviewObligationTypeEvidence
	case reviewObligationTypeReviewerRoute:
		return reviewObligationTypeReviewerRoute
	default:
		return strings.ToLower(strings.TrimSpace(obligationType))
	}
}

func normalizeReviewObligationStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "unknown":
		return ""
	case reviewObligationStatusOpen, "unresolved", "needs_revision", "repair_required":
		return reviewObligationStatusOpen
	case reviewObligationStatusResolved, "fixed", "addressed":
		return reviewObligationStatusResolved
	case reviewObligationStatusEvidenceUnconfirmed, "partial", "unconfirmed":
		return reviewObligationStatusEvidenceUnconfirmed
	case reviewObligationStatusVerificationRequired, "verification_needed", "verification_pending":
		return reviewObligationStatusVerificationRequired
	case reviewObligationStatusRouteUnavailable, "reviewer_unavailable", "route_failed":
		return reviewObligationStatusRouteUnavailable
	case reviewObligationStatusDisclosedFinal, "non_blocking_review_meta", "false_positive", "waived", "accepted_risk":
		return reviewObligationStatusDisclosedFinal
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

func reviewObligationStatusIsOpen(status string) bool {
	switch normalizeReviewObligationStatus(status) {
	case reviewObligationStatusResolved, reviewObligationStatusDisclosedFinal:
		return false
	default:
		return true
	}
}

func reviewObligationKey(obligation ReviewObligation) string {
	return strings.ToLower(strings.TrimSpace(obligation.Type) + "|" + firstNonBlankString(obligation.SourceFindingID, obligation.ID))
}

func mergeReviewObligations(existing ReviewObligation, incoming ReviewObligation) ReviewObligation {
	existing = normalizeReviewObligation(existing)
	incoming = normalizeReviewObligation(incoming)
	if reviewObligationIncomingStatusShouldReplace(existing.Status, incoming.Status) {
		existing.Status = incoming.Status
	}
	if strings.TrimSpace(existing.RequiredAction) == "" {
		existing.RequiredAction = incoming.RequiredAction
	}
	if strings.TrimSpace(existing.Title) == "" {
		existing.Title = incoming.Title
	}
	if strings.TrimSpace(existing.Severity) == "" {
		existing.Severity = incoming.Severity
	}
	if strings.TrimSpace(existing.Category) == "" {
		existing.Category = incoming.Category
	}
	if strings.TrimSpace(existing.ReviewerRole) == "" {
		existing.ReviewerRole = incoming.ReviewerRole
	}
	existing.Blocking = existing.Blocking || incoming.Blocking
	existing.EvidenceRefs = normalizeTaskStateList(append(existing.EvidenceRefs, incoming.EvidenceRefs...), 12)
	existing.FixRefs = normalizeTaskStateList(append(existing.FixRefs, incoming.FixRefs...), 12)
	return existing
}

func reviewObligationIncomingStatusShouldReplace(existing string, incoming string) bool {
	existing = normalizeReviewObligationStatus(existing)
	incoming = normalizeReviewObligationStatus(incoming)
	if incoming == "" {
		return false
	}
	if existing == "" {
		return true
	}
	return reviewObligationStatusRank(incoming) > reviewObligationStatusRank(existing)
}

func reviewObligationStatusRank(status string) int {
	switch normalizeReviewObligationStatus(status) {
	case reviewObligationStatusOpen:
		return 0
	case reviewObligationStatusEvidenceUnconfirmed:
		return 1
	case reviewObligationStatusVerificationRequired:
		return 2
	case reviewObligationStatusRouteUnavailable:
		return 2
	case reviewObligationStatusResolved, reviewObligationStatusDisclosedFinal:
		return 3
	default:
		return 1
	}
}

func reviewObligationTypeRank(obligationType string) int {
	switch obligationType {
	case reviewObligationTypeRepair:
		return 0
	case reviewObligationTypeReviewerRoute:
		return 1
	case reviewObligationTypeVerification:
		return 2
	case reviewObligationTypeEvidence:
		return 3
	default:
		return 4
	}
}

func reviewObligationLedgerSummary(ledger ReviewObligationLedger) []string {
	var summary []string
	if ledger.OpenRepairCount > 0 {
		summary = append(summary, fmt.Sprintf("repair=%d", ledger.OpenRepairCount))
	}
	if ledger.OpenRouteCount > 0 {
		summary = append(summary, fmt.Sprintf("reviewer_route=%d", ledger.OpenRouteCount))
	}
	if ledger.OpenVerificationCount > 0 {
		summary = append(summary, fmt.Sprintf("verification=%d", ledger.OpenVerificationCount))
	}
	if ledger.OpenEvidenceCount > 0 {
		summary = append(summary, fmt.Sprintf("evidence=%d", ledger.OpenEvidenceCount))
	}
	return summary
}

func reviewObligationLedgerHasOpenType(ledger ReviewObligationLedger, obligationType string) bool {
	targetType := normalizeReviewObligationType(obligationType)
	for _, obligation := range ledger.Items {
		if normalizeReviewObligationType(obligation.Type) == targetType && reviewObligationStatusIsOpen(obligation.Status) {
			return true
		}
	}
	return false
}

func reviewObligationLedgerHasOpenBlockingRepair(ledger ReviewObligationLedger) bool {
	for _, obligation := range ledger.Items {
		if normalizeReviewObligationType(obligation.Type) == reviewObligationTypeRepair &&
			obligation.Blocking &&
			reviewObligationStatusRequiresRepair(obligation.Status) {
			return true
		}
	}
	return false
}

func reviewObligationLedgerHasOpenRepairRequired(ledger ReviewObligationLedger) bool {
	for _, obligation := range ledger.Items {
		if normalizeReviewObligationType(obligation.Type) == reviewObligationTypeRepair &&
			reviewObligationStatusRequiresRepair(obligation.Status) {
			return true
		}
	}
	return false
}

func reviewObligationLedgerHasOpenVerificationRequired(ledger ReviewObligationLedger) bool {
	for _, obligation := range ledger.Items {
		if !reviewObligationStatusIsOpen(obligation.Status) {
			continue
		}
		if normalizeReviewObligationType(obligation.Type) == reviewObligationTypeVerification {
			return true
		}
		if normalizeReviewObligationType(obligation.Type) == reviewObligationTypeRepair &&
			normalizeReviewObligationStatus(obligation.Status) == reviewObligationStatusVerificationRequired {
			return true
		}
	}
	return false
}

func reviewObligationLedgerHasOpenEvidenceRequired(ledger ReviewObligationLedger) bool {
	for _, obligation := range ledger.Items {
		if normalizeReviewObligationType(obligation.Type) == reviewObligationTypeEvidence &&
			reviewObligationStatusIsOpen(obligation.Status) {
			return true
		}
	}
	return false
}

func reviewObligationStatusRequiresRepair(status string) bool {
	normalized := normalizeReviewObligationStatus(status)
	if !reviewObligationStatusIsOpen(normalized) {
		return false
	}
	return normalized != reviewObligationStatusVerificationRequired
}

func reviewOpenRepairRequiredObligationMap(ledger ReviewObligationLedger) map[string]ReviewObligation {
	out := map[string]ReviewObligation{}
	for _, obligation := range ledger.Items {
		obligation = normalizeReviewObligation(obligation)
		if obligation.Type != reviewObligationTypeRepair || !reviewObligationStatusRequiresRepair(obligation.Status) {
			continue
		}
		for _, id := range []string{obligation.ID, obligation.SourceFindingID} {
			id = strings.TrimSpace(id)
			if id != "" {
				out[strings.ToLower(id)] = obligation
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func reviewObligationMapLookupByFinding(obligations map[string]ReviewObligation, finding ReviewFinding) (ReviewObligation, bool) {
	if len(obligations) == 0 {
		return ReviewObligation{}, false
	}
	finding.Normalize()
	for _, id := range []string{finding.ID} {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			continue
		}
		if obligation, ok := obligations[id]; ok {
			return obligation, true
		}
	}
	return ReviewObligation{}, false
}

func reviewFindingFromRepairObligation(obligation ReviewObligation) ReviewFinding {
	obligation = normalizeReviewObligation(obligation)
	return ReviewFinding{
		ID:           firstNonBlankString(obligation.SourceFindingID, obligation.ID),
		Severity:     firstNonBlankString(obligation.Severity, reviewSeverityMedium),
		Category:     firstNonBlankString(obligation.Category, "correctness"),
		ReviewerRole: obligation.ReviewerRole,
		Title:        firstNonBlankString(obligation.Title, obligation.ID),
		RequiredFix:  obligation.RequiredAction,
		BlocksGate:   obligation.Blocking,
	}
}
