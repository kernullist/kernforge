package main

import (
	"strings"
	"time"
)

type FinalAnswerCorrectionVisibility struct {
	Required             bool      `json:"required"`
	Corrected            bool      `json:"corrected,omitempty"`
	Source               string    `json:"source,omitempty"`
	Reasons              []string  `json:"reasons,omitempty"`
	FindingTitles        []string  `json:"finding_titles,omitempty"`
	RecordedAt           time.Time `json:"recorded_at,omitempty"`
	CorrectedAt          time.Time `json:"corrected_at,omitempty"`
	recordedMessageCount int
	hasRecordedMessages  bool
}

func (v *FinalAnswerCorrectionVisibility) Normalize() {
	if v == nil {
		return
	}
	v.Source = strings.TrimSpace(v.Source)
	if v.Source == "" {
		v.Source = "pre_final_coding_harness"
	}
	v.Reasons = normalizeTaskStateList(v.Reasons, 8)
	v.FindingTitles = normalizeTaskStateList(v.FindingTitles, 12)
	if !v.Required && len(v.Reasons) > 0 {
		v.Required = true
	}
}

func finalAnswerCorrectionVisibilityFromReport(report *CodingHarnessReport, corrected bool) *FinalAnswerCorrectionVisibility {
	if report == nil || !codingHarnessReportRequiresFinalAnswerOnlyRevision(report) {
		return nil
	}
	copyReport := *report
	copyReport.Normalize()
	visibility := &FinalAnswerCorrectionVisibility{
		Required:   true,
		Corrected:  corrected,
		Source:     "pre_final_coding_harness",
		RecordedAt: time.Now(),
	}
	for _, finding := range copyReport.allFindings() {
		if !strings.EqualFold(strings.TrimSpace(finding.Severity), "blocker") ||
			!codingHarnessFindingRequiresFinalAnswerOnlyRevision(finding) {
			continue
		}
		if reason := finalAnswerCorrectionReasonForFinding(finding); reason != "" {
			visibility.Reasons = append(visibility.Reasons, reason)
		}
		if title := strings.TrimSpace(finding.Title); title != "" {
			visibility.FindingTitles = append(visibility.FindingTitles, title)
		}
	}
	visibility.Normalize()
	if len(visibility.Reasons) == 0 {
		return nil
	}
	return visibility
}

func finalAnswerCorrectionReasonForFinding(finding CodingHarnessFinding) string {
	switch strings.TrimSpace(finding.Title) {
	case "Changed-file summary is missing",
		"Final answer contradicts the patch transaction":
		return "changed_file_disclosure"
	case "Review result is missing":
		return "review_self_review_disclosure"
	case "Validation result is missing",
		"Required verification has no outcome",
		"Unresolved verification failure",
		"Verification was not run disclosure missing",
		"Generated document artifact verification disclosure is missing",
		"Verification claim has no recorded evidence",
		"Edit loop verification failure is unstated":
		return "validation_disclosure"
	case "Remaining-risk statement is missing",
		"Final answer contradicts remaining edit-loop risk",
		"Edit loop remaining risk is omitted",
		"No-finding review omits residual risk",
		"Cross-review residual risk is undisclosed":
		return "remaining_risk_disclosure"
	case "Review-only answer is not findings-first",
		"Review-only no-edit statement is missing":
		return "review_only_findings_first_no_edit"
	default:
		return ""
	}
}

func finalAnswerCorrectionStatusLine(visibility *FinalAnswerCorrectionVisibility) string {
	if visibility == nil {
		return "none"
	}
	copyVisibility := *visibility
	copyVisibility.Normalize()
	parts := []string{}
	if copyVisibility.Required {
		parts = append(parts, "required=true")
	}
	if copyVisibility.Corrected {
		parts = append(parts, "corrected=true")
	}
	if len(copyVisibility.Reasons) > 0 {
		parts = append(parts, "reasons="+strings.Join(copyVisibility.Reasons, ","))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}

func finalAnswerCorrectionDetailedLine(visibility *FinalAnswerCorrectionVisibility) string {
	if visibility == nil {
		return ""
	}
	copyVisibility := *visibility
	copyVisibility.Normalize()
	if len(copyVisibility.FindingTitles) == 0 {
		return ""
	}
	return "titles=" + strings.Join(limitStrings(copyVisibility.FindingTitles, 4), " | ")
}

func (a *Agent) recordFinalAnswerCorrectionRequired(report *CodingHarnessReport) {
	if a == nil || a.Session == nil {
		return
	}
	visibility := finalAnswerCorrectionVisibilityFromReport(report, false)
	if visibility == nil {
		return
	}
	visibility.recordedMessageCount = len(a.Session.Messages)
	visibility.hasRecordedMessages = true
	a.Session.LastFinalAnswerCorrection = visibility
	if report != nil {
		report.FinalAnswerCorrection = visibility
	}
}

func (a *Agent) markFinalAnswerCorrectionAccepted() {
	if a == nil || a.Session == nil || a.Session.LastFinalAnswerCorrection == nil {
		return
	}
	if a.Session.LastCodingHarnessReport != nil && !a.Session.LastCodingHarnessReport.Approved {
		return
	}
	visibility := *a.Session.LastFinalAnswerCorrection
	if visibility.hasRecordedMessages && finalAnswerCorrectionHasExternalUserAfter(a.Session, visibility.recordedMessageCount) {
		return
	}
	visibility.Corrected = true
	if visibility.CorrectedAt.IsZero() {
		visibility.CorrectedAt = time.Now()
	}
	visibility.Normalize()
	a.Session.LastFinalAnswerCorrection = &visibility
	if a.Session.LastCodingHarnessReport != nil {
		a.Session.LastCodingHarnessReport.FinalAnswerCorrection = &visibility
	}
}

func finalAnswerCorrectionHasExternalUserAfter(session *Session, recordedMessageCount int) bool {
	if session == nil || recordedMessageCount < 0 || recordedMessageCount >= len(session.Messages) {
		return false
	}
	for _, message := range session.Messages[recordedMessageCount:] {
		if message.Role == "user" && !message.Internal {
			return true
		}
	}
	return false
}
