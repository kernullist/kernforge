package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	crossReviewTriageAcceptedFixed      = "accepted_fixed"
	crossReviewTriageAcceptedDeferred   = "accepted_deferred"
	crossReviewTriageRejectedWithReason = "rejected_with_reason"
	crossReviewTriageNeedsUserDecision  = "needs_user_decision"
)

type CrossReviewTriageLedger struct {
	Items           []CrossReviewTriageEntry `json:"items,omitempty"`
	TotalCount      int                      `json:"total_count,omitempty"`
	StatusCounts    map[string]int           `json:"status_counts,omitempty"`
	IncompleteCount int                      `json:"incomplete_count,omitempty"`
	Blockers        []string                 `json:"blockers,omitempty"`
	Warnings        []string                 `json:"warnings,omitempty"`
}

type CrossReviewTriageEntry struct {
	FindingID        string   `json:"finding_id,omitempty"`
	ReviewerRole     string   `json:"reviewer_role,omitempty"`
	Severity         string   `json:"severity,omitempty"`
	Category         string   `json:"category,omitempty"`
	Path             string   `json:"path,omitempty"`
	Line             int      `json:"line,omitempty"`
	Symbol           string   `json:"symbol,omitempty"`
	Title            string   `json:"title,omitempty"`
	RequiredFix      string   `json:"required_fix,omitempty"`
	TriageStatus     string   `json:"triage_status,omitempty"`
	TechnicalReason  string   `json:"technical_reason,omitempty"`
	FixRefs          []string `json:"fix_refs,omitempty"`
	ChangedPaths     []string `json:"changed_paths,omitempty"`
	VerificationRefs []string `json:"verification_refs,omitempty"`
	EvidenceRefs     []string `json:"evidence_refs,omitempty"`
}

func buildCrossReviewTriageLedger(run ReviewRun) *CrossReviewTriageLedger {
	ledger := &CrossReviewTriageLedger{
		StatusCounts: map[string]int{},
	}
	for _, finding := range run.Findings {
		finding.Normalize()
		if !crossReviewFindingRequiresTriage(run, finding) {
			continue
		}
		entry := crossReviewTriageEntryFromFinding(run, finding)
		ledger.Items = append(ledger.Items, entry)
		ledger.StatusCounts[entry.TriageStatus]++
	}
	if len(ledger.Items) == 0 {
		return nil
	}
	ledger.TotalCount = len(ledger.Items)
	ledger.validate()
	return ledger
}

func crossReviewFindingRequiresTriage(run ReviewRun, finding ReviewFinding) bool {
	finding.Normalize()
	if !strings.EqualFold(strings.TrimSpace(finding.Source), "model") {
		return false
	}
	if normalizeReviewRole(finding.ReviewerRole) != "cross_reviewer" {
		return false
	}
	if reviewFindingLooksReviewMetaOnly(finding) {
		return false
	}
	explicitTriageStatus := normalizeCrossReviewTriageStatus(finding.ResolutionStatus) != ""
	if strings.EqualFold(strings.TrimSpace(finding.Quality), reviewFindingQualityWeak) ||
		strings.EqualFold(strings.TrimSpace(finding.Quality), reviewFindingQualityInvalid) {
		return explicitTriageStatus
	}
	if strings.EqualFold(strings.TrimSpace(finding.Severity), reviewSeverityInfo) {
		return false
	}
	if strings.TrimSpace(firstNonBlankString(finding.RequiredFix, finding.Title, finding.Evidence, finding.TestRecommendation)) == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(finding.Category), "evidence_gap") &&
		!reviewFindingBlocksGate(run, finding) {
		return false
	}
	return true
}

func crossReviewTriageEntryFromFinding(run ReviewRun, finding ReviewFinding) CrossReviewTriageEntry {
	status := inferCrossReviewTriageStatus(finding)
	return CrossReviewTriageEntry{
		FindingID:        strings.TrimSpace(finding.ID),
		ReviewerRole:     normalizeReviewRole(finding.ReviewerRole),
		Severity:         strings.TrimSpace(finding.Severity),
		Category:         strings.TrimSpace(finding.Category),
		Path:             filepath.ToSlash(strings.TrimSpace(finding.Path)),
		Line:             finding.Line,
		Symbol:           strings.TrimSpace(finding.Symbol),
		Title:            strings.TrimSpace(finding.Title),
		RequiredFix:      strings.TrimSpace(finding.RequiredFix),
		TriageStatus:     status,
		TechnicalReason:  inferCrossReviewTechnicalReason(status, finding),
		FixRefs:          normalizeTaskStateList(finding.FixRefs, 12),
		ChangedPaths:     crossReviewTriageChangedPaths(status, run, finding),
		VerificationRefs: crossReviewTriageVerificationRefs(run, finding),
		EvidenceRefs:     normalizeTaskStateList(finding.EvidenceRefs, 12),
	}
}

func inferCrossReviewTriageStatus(finding ReviewFinding) string {
	status := normalizeCrossReviewTriageStatus(finding.ResolutionStatus)
	if status != "" {
		return status
	}
	switch normalizeReviewRepairResolutionStatus(finding.ResolutionStatus) {
	case "resolved":
		return crossReviewTriageAcceptedFixed
	case "partial", "verification_needed", "evidence_unconfirmed":
		return crossReviewTriageAcceptedDeferred
	}
	switch strings.ToLower(strings.TrimSpace(finding.Category)) {
	case "false_positive":
		return crossReviewTriageRejectedWithReason
	default:
		return crossReviewTriageNeedsUserDecision
	}
}

func normalizeCrossReviewTriageStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(status, "-", "_")))
	switch status {
	case crossReviewTriageAcceptedFixed, "accepted_and_fixed", "accepted/fixed", "resolved", "fixed":
		return crossReviewTriageAcceptedFixed
	case crossReviewTriageAcceptedDeferred, "accepted/deferred", "deferred", "accepted_but_deferred", "partial", "verification_needed", "evidence_unconfirmed":
		return crossReviewTriageAcceptedDeferred
	case crossReviewTriageRejectedWithReason, "rejected", "false_positive", "non_blocking_review_meta":
		return crossReviewTriageRejectedWithReason
	case crossReviewTriageNeedsUserDecision, "user_decision", "needs_decision":
		return crossReviewTriageNeedsUserDecision
	default:
		return ""
	}
}

func inferCrossReviewTechnicalReason(status string, finding ReviewFinding) string {
	switch status {
	case crossReviewTriageAcceptedFixed:
		return firstNonBlankString(
			strings.TrimSpace(finding.Evidence),
			strings.TrimSpace(finding.RequiredFix),
			"Accepted because the finding is tracked as resolved.",
		)
	case crossReviewTriageAcceptedDeferred:
		return firstNonBlankString(
			strings.TrimSpace(finding.RequiredFix),
			strings.TrimSpace(finding.TestRecommendation),
			strings.TrimSpace(finding.Evidence),
			"Accepted but deferred because follow-up evidence or verification is still required.",
		)
	case crossReviewTriageRejectedWithReason:
		return firstNonBlankString(
			strings.TrimSpace(finding.Evidence),
			strings.TrimSpace(finding.Impact),
			strings.TrimSpace(finding.RequiredFix),
		)
	default:
		return "No primary repair decision has been recorded for this cross-review finding."
	}
}

func crossReviewTriageChangedPaths(status string, run ReviewRun, finding ReviewFinding) []string {
	if status != crossReviewTriageAcceptedFixed {
		return nil
	}
	paths := append([]string(nil), run.ChangeSet.ChangedPaths...)
	paths = append(paths, finding.FixRefs...)
	return normalizeTaskStateList(paths, 16)
}

func crossReviewTriageVerificationRefs(run ReviewRun, finding ReviewFinding) []string {
	refs := append([]string(nil), finding.VerificationRefs...)
	if strings.TrimSpace(run.Evidence.VerificationSummary) != "" {
		refs = append(refs, "latest_verification_summary")
	}
	if strings.TrimSpace(finding.TestRecommendation) != "" &&
		(strings.EqualFold(strings.TrimSpace(finding.Category), "test_gap") ||
			strings.Contains(strings.ToLower(finding.TestRecommendation), "test") ||
			strings.Contains(strings.ToLower(finding.TestRecommendation), "verify")) {
		refs = append(refs, finding.TestRecommendation)
	}
	return normalizeTaskStateList(refs, 12)
}

func (l *CrossReviewTriageLedger) validate() {
	if l == nil {
		return
	}
	l.Blockers = nil
	l.Warnings = nil
	l.IncompleteCount = 0
	for _, entry := range l.Items {
		status := normalizeCrossReviewTriageStatus(entry.TriageStatus)
		if status == "" {
			l.addBlocker(entry, "triage status is missing or unsupported")
			continue
		}
		switch status {
		case crossReviewTriageAcceptedFixed:
			if len(entry.FixRefs) == 0 && len(entry.ChangedPaths) == 0 {
				l.addBlocker(entry, "accepted_fixed requires fix_refs or changed_paths evidence")
			}
		case crossReviewTriageAcceptedDeferred:
			if strings.TrimSpace(entry.TechnicalReason) == "" {
				l.addBlocker(entry, "accepted_deferred requires a deferral reason")
			}
		case crossReviewTriageRejectedWithReason:
			if !crossReviewRejectionReasonLooksTechnical(entry.TechnicalReason) {
				l.addBlocker(entry, "rejected_with_reason requires a technical evidence-based reason")
			}
		case crossReviewTriageNeedsUserDecision:
			l.Warnings = append(l.Warnings, fmt.Sprintf("%s needs a user or primary repair decision", valueOrDefault(entry.FindingID, entry.Title)))
		}
	}
	l.Blockers = normalizeTaskStateList(l.Blockers, 32)
	l.Warnings = normalizeTaskStateList(l.Warnings, 32)
}

func (l *CrossReviewTriageLedger) addBlocker(entry CrossReviewTriageEntry, reason string) {
	if l == nil {
		return
	}
	l.IncompleteCount++
	label := firstNonBlankString(entry.FindingID, entry.Title, "cross-review finding")
	l.Blockers = append(l.Blockers, fmt.Sprintf("%s: %s", label, reason))
}

func crossReviewRejectionReasonLooksTechnical(reason string) bool {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return false
	}
	lower := strings.ToLower(reason)
	return containsAny(lower,
		"line", "path", "symbol", "diff", "code", "function", "call", "type", "buffer",
		"contract", "evidence", "because", "not applicable", "false positive", "already guarded",
		"검증", "근거", "코드", "함수", "경로", "라인", "오탐", "이미",
	)
}

func crossReviewTriageConsistencyFindings(run ReviewRun) []ReviewFinding {
	if run.CrossReviewTriage == nil || run.CrossReviewTriage.IncompleteCount == 0 {
		return nil
	}
	finding := ReviewFinding{
		ID:           "RF-CROSS-TRIAGE-001",
		Source:       "deterministic",
		ReviewerRole: "cross_review_triage",
		Severity:     reviewSeverityBlocker,
		Category:     "operational_risk",
		Confidence:   "high",
		Quality:      reviewFindingQualityComplete,
		Title:        "Cross-review triage ledger is incomplete",
		Evidence:     strings.Join(limitStrings(run.CrossReviewTriage.Blockers, 6), " | "),
		Impact:       "The primary repair loop cannot silently accept or reject independent cross-review findings without auditable reconciliation.",
		RequiredFix:  "Record fix refs, a deferral reason, a technical rejection reason, or a needs_user_decision status for every actionable cross-review finding.",
		BlocksGate:   true,
	}
	return []ReviewFinding{finding}
}
