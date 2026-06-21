package main

import (
	"strconv"
	"strings"
	"time"
)

type FinalAnswerCorrectionVisibility struct {
	Required             bool                           `json:"required"`
	Corrected            bool                           `json:"corrected,omitempty"`
	Rejected             bool                           `json:"rejected,omitempty"`
	Status               string                         `json:"status,omitempty"`
	Source               string                         `json:"source,omitempty"`
	Reasons              []string                       `json:"reasons,omitempty"`
	FindingTitles        []string                       `json:"finding_titles,omitempty"`
	Contract             *FinalAnswerCorrectionContract `json:"contract,omitempty"`
	AttemptCount         int                            `json:"attempt_count,omitempty"`
	MaxAttempts          int                            `json:"max_attempts,omitempty"`
	RecordedMessageCount int                            `json:"recorded_message_count,omitempty"`
	HasRecordedMessages  bool                           `json:"has_recorded_messages,omitempty"`
	RecordedAt           time.Time                      `json:"recorded_at,omitempty"`
	CorrectedAt          time.Time                      `json:"corrected_at,omitempty"`
	RejectedAt           time.Time                      `json:"rejected_at,omitempty"`
	LastAttemptAt        time.Time                      `json:"last_attempt_at,omitempty"`
	recordedMessageCount int
	hasRecordedMessages  bool
}

type FinalAnswerCorrectionContract struct {
	MissingDisclosureFields    []string `json:"missing_disclosure_fields,omitempty"`
	RequiredAnswerShape        []string `json:"required_answer_shape,omitempty"`
	EditsProhibited            bool     `json:"edits_prohibited"`
	VerificationMode           string   `json:"verification_mode,omitempty"`
	VerificationMustRerun      bool     `json:"verification_must_rerun,omitempty"`
	VerificationDisclosureOnly bool     `json:"verification_disclosure_only,omitempty"`
	AttemptCount               int      `json:"attempt_count,omitempty"`
	MaxAttempts                int      `json:"max_attempts,omitempty"`
	State                      string   `json:"state,omitempty"`
	Accepted                   bool     `json:"accepted,omitempty"`
	Rejected                   bool     `json:"rejected,omitempty"`
	ManualRecoveryNeeded       bool     `json:"manual_recovery_needed,omitempty"`
	NextCommand                string   `json:"next_command,omitempty"`
}

const (
	finalAnswerCorrectionStatusRequired     = "required"
	finalAnswerCorrectionStatusAccepted     = "accepted"
	finalAnswerCorrectionStatusRejected     = "rejected"
	finalAnswerCorrectionDefaultMaxAttempts = 2
)

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
	if v.RecordedMessageCount > 0 {
		v.recordedMessageCount = v.RecordedMessageCount
		v.hasRecordedMessages = true
	}
	if v.HasRecordedMessages {
		v.hasRecordedMessages = true
	}
	if v.hasRecordedMessages {
		v.RecordedMessageCount = v.recordedMessageCount
		v.HasRecordedMessages = true
	}
	if !v.Required && len(v.Reasons) > 0 {
		v.Required = true
	}
	v.Status = strings.TrimSpace(strings.ToLower(v.Status))
	if v.MaxAttempts < 0 {
		v.MaxAttempts = 0
	}
	if v.AttemptCount < 0 {
		v.AttemptCount = 0
	}
	if v.Corrected {
		v.Status = finalAnswerCorrectionStatusAccepted
		v.Rejected = false
	}
	if v.Rejected {
		v.Status = finalAnswerCorrectionStatusRejected
		v.Corrected = false
	}
	if v.Status == "" && v.Required {
		v.Status = finalAnswerCorrectionStatusRequired
	}
	switch v.Status {
	case finalAnswerCorrectionStatusAccepted:
		v.Corrected = true
		v.Rejected = false
	case finalAnswerCorrectionStatusRejected:
		v.Rejected = true
		v.Corrected = false
	case finalAnswerCorrectionStatusRequired:
	default:
		v.Status = strings.TrimSpace(v.Status)
	}
	if v.Contract == nil && v.Required {
		v.Contract = buildFinalAnswerCorrectionContract(*v)
	}
	if v.Contract != nil {
		v.Contract.Normalize(v)
	}
}

func (c *FinalAnswerCorrectionContract) Normalize(parent *FinalAnswerCorrectionVisibility) {
	if c == nil {
		return
	}
	c.MissingDisclosureFields = normalizeTaskStateList(c.MissingDisclosureFields, 12)
	c.RequiredAnswerShape = normalizeTaskStateList(c.RequiredAnswerShape, 12)
	c.VerificationMode = strings.TrimSpace(strings.ToLower(c.VerificationMode))
	c.State = strings.TrimSpace(strings.ToLower(c.State))
	c.NextCommand = strings.TrimSpace(c.NextCommand)
	if parent != nil {
		c.AttemptCount = parent.AttemptCount
		c.MaxAttempts = parent.MaxAttempts
		c.State = parent.Status
		c.Accepted = parent.Corrected
		c.Rejected = parent.Rejected
	}
	if c.VerificationMode == "" {
		if containsString(c.MissingDisclosureFields, "validation_disclosure") {
			c.VerificationMode = "disclose_only"
			c.VerificationDisclosureOnly = true
		} else {
			c.VerificationMode = "not_required"
		}
	}
	if c.VerificationMode == "rerun_required" {
		c.VerificationMustRerun = true
		c.VerificationDisclosureOnly = false
	} else if c.VerificationMode == "disclose_only" {
		c.VerificationDisclosureOnly = true
	}
	if len(c.RequiredAnswerShape) == 0 {
		c.RequiredAnswerShape = finalAnswerRequiredShapeForReasons(c.MissingDisclosureFields)
	}
	if c.NextCommand == "" {
		c.NextCommand = "/status detail"
	}
	c.ManualRecoveryNeeded = c.Rejected || (c.MaxAttempts > 0 && c.AttemptCount >= c.MaxAttempts && !c.Accepted)
}

func buildFinalAnswerCorrectionContract(visibility FinalAnswerCorrectionVisibility) *FinalAnswerCorrectionContract {
	reasons := normalizeTaskStateList(visibility.Reasons, 12)
	contract := &FinalAnswerCorrectionContract{
		MissingDisclosureFields: reasons,
		RequiredAnswerShape:     finalAnswerRequiredShapeForReasons(reasons),
		EditsProhibited:         true,
		VerificationMode:        finalAnswerCorrectionVerificationMode(reasons),
		AttemptCount:            visibility.AttemptCount,
		MaxAttempts:             visibility.MaxAttempts,
		State:                   visibility.Status,
		Accepted:                visibility.Corrected,
		Rejected:                visibility.Rejected,
		NextCommand:             "/status detail",
	}
	contract.Normalize(&visibility)
	return contract
}

// finalAnswerRequiredShapeInstructions maps each missing-disclosure reason id to
// the single concrete instruction for that field. Every line in the required
// shape is derived from an actual missing field; there is no meta/boilerplate
// "start with the concrete outcome" line and no padding when only one field is
// missing.
var finalAnswerRequiredShapeInstructions = map[string]string{
	"review_only_findings_first_no_edit": "for review-only requests, list findings first and explicitly state that no edits were made",
	"changed_file_disclosure":            "include the changed file or artifact paths that were actually touched",
	"document_artifact_disclosure":       "include the document artifact path and artifact-quality gate status",
	"review_self_review_disclosure":      "include the latest review result or state that review was not run",
	"validation_disclosure":              "include verification status exactly: passed, failed, skipped, or not run",
	"remaining_risk_disclosure":          "include unresolved risks, residual risk, or the absence of remaining known blockers",
}

// finalAnswerRequiredShapeOrder pins a deterministic order so the rendered shape
// is stable regardless of reason ordering.
var finalAnswerRequiredShapeOrder = []string{
	"review_only_findings_first_no_edit",
	"changed_file_disclosure",
	"document_artifact_disclosure",
	"review_self_review_disclosure",
	"validation_disclosure",
	"remaining_risk_disclosure",
}

func finalAnswerRequiredShapeForReasons(reasons []string) []string {
	shape := []string{}
	for _, id := range finalAnswerRequiredShapeOrder {
		if !containsString(reasons, id) {
			continue
		}
		if instruction := finalAnswerRequiredShapeInstructions[id]; instruction != "" {
			shape = append(shape, instruction)
		}
	}
	return normalizeTaskStateList(shape, 12)
}

func finalAnswerCorrectionVerificationMode(reasons []string) string {
	if containsString(reasons, "validation_disclosure") {
		return "disclose_only"
	}
	return "not_required"
}

func finalAnswerCorrectionVisibilityFromReport(report *CodingHarnessReport, corrected bool) *FinalAnswerCorrectionVisibility {
	if report == nil || !codingHarnessReportRequiresFinalAnswerOnlyRevision(report) {
		return nil
	}
	copyReport := *report
	copyReport.Normalize()
	visibility := &FinalAnswerCorrectionVisibility{
		Required:    true,
		Corrected:   corrected,
		Source:      "pre_final_coding_harness",
		RecordedAt:  time.Now(),
		MaxAttempts: finalAnswerCorrectionDefaultMaxAttempts,
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
	if corrected {
		visibility.Status = finalAnswerCorrectionStatusAccepted
	} else {
		visibility.Status = finalAnswerCorrectionStatusRequired
	}
	return visibility
}

func finalAnswerCorrectionShouldReplace(existing *FinalAnswerCorrectionVisibility, next *FinalAnswerCorrectionVisibility) bool {
	if next == nil {
		return false
	}
	if existing == nil {
		return true
	}
	existingCopy := *existing
	nextCopy := *next
	existingCopy.Normalize()
	nextCopy.Normalize()
	if existingCopy.Rejected && !nextCopy.Rejected {
		return false
	}
	if existingCopy.Corrected && !nextCopy.Corrected {
		return false
	}
	return true
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
		"Cross-review residual risk is undisclosed",
		"Document artifact limitation statement is missing":
		return "remaining_risk_disclosure"
	case "Review-only answer is not findings-first",
		"Review-only no-edit statement is missing":
		return "review_only_findings_first_no_edit"
	case "Document artifact path is missing",
		"Document artifact quality status is missing":
		return "document_artifact_disclosure"
	case "Document artifact verification disclosure is missing":
		return "validation_disclosure"
	default:
		return ""
	}
}

// humanizeFinalAnswerCorrectionReason maps a final-answer-correction disclosure
// reason id to plain, locale-aware language for the user-facing line. The raw id
// stays in machine fields; this only feeds the human sentence.
func humanizeFinalAnswerCorrectionReason(value string, korean bool) string {
	switch strings.TrimSpace(value) {
	case "changed_file_disclosure":
		if korean {
			return "변경된 파일"
		}
		return "changed files"
	case "review_self_review_disclosure":
		if korean {
			return "리뷰 결과"
		}
		return "review results"
	case "validation_disclosure":
		if korean {
			return "검증 결과"
		}
		return "validation results"
	case "remaining_risk_disclosure":
		if korean {
			return "남은 위험"
		}
		return "remaining risk"
	case "document_artifact_disclosure":
		if korean {
			return "생성한 문서"
		}
		return "generated document"
	default:
		return humanizeEnumFallback(value)
	}
}

// finalAnswerCorrectionHumanLine renders the final-answer-correction state as a
// plain sentence (e.g. "Final answer is missing: changed files, remaining risk").
// It returns "" when there is nothing meaningful to surface so callers can omit
// the line. The raw key=value form stays available via finalAnswerCorrectionStatusLine
// for the detail/debug view.
func finalAnswerCorrectionHumanLine(visibility *FinalAnswerCorrectionVisibility, korean bool) string {
	if visibility == nil {
		return ""
	}
	copyVisibility := *visibility
	copyVisibility.Normalize()
	if !copyVisibility.Required && !copyVisibility.Corrected && !copyVisibility.Rejected {
		return ""
	}
	missing := []string{}
	for _, reason := range copyVisibility.Reasons {
		if phrase := humanizeFinalAnswerCorrectionReason(reason, korean); strings.TrimSpace(phrase) != "" {
			missing = append(missing, phrase)
		}
	}
	switch {
	case copyVisibility.Corrected:
		if korean {
			return "최종 답변에 빠졌던 항목을 보완했습니다."
		}
		return "The final answer was corrected to include the required disclosures."
	case copyVisibility.Rejected:
		if korean {
			return "최종 답변 보완이 받아들여지지 않았습니다."
		}
		return "The final-answer correction was rejected."
	default:
		if len(missing) == 0 {
			if korean {
				return "최종 답변에 필요한 설명이 빠져 있습니다."
			}
			return "The final answer is missing required disclosures."
		}
		if korean {
			return "최종 답변에 빠진 항목: " + strings.Join(missing, ", ")
		}
		return "Final answer is missing: " + strings.Join(missing, ", ")
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
	if copyVisibility.Status != "" {
		parts = append(parts, "status="+copyVisibility.Status)
	}
	if copyVisibility.Corrected {
		parts = append(parts, "corrected=true")
	}
	if copyVisibility.Rejected {
		parts = append(parts, "rejected=true")
	}
	if copyVisibility.AttemptCount > 0 {
		attempts := "attempts=" + strconv.Itoa(copyVisibility.AttemptCount)
		if copyVisibility.MaxAttempts > 0 {
			attempts += "/" + strconv.Itoa(copyVisibility.MaxAttempts)
		}
		parts = append(parts, attempts)
	}
	if len(copyVisibility.Reasons) > 0 {
		parts = append(parts, "reasons="+strings.Join(copyVisibility.Reasons, ","))
	}
	if copyVisibility.Contract != nil {
		parts = append(parts, "contract_state="+valueOrDefault(copyVisibility.Contract.State, copyVisibility.Status))
		if copyVisibility.Contract.EditsProhibited {
			parts = append(parts, "edits_prohibited=true")
		}
		if copyVisibility.Contract.VerificationMode != "" {
			parts = append(parts, "verification="+copyVisibility.Contract.VerificationMode)
		}
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
		if copyVisibility.Contract == nil {
			return ""
		}
	}
	parts := []string{}
	if len(copyVisibility.FindingTitles) > 0 {
		parts = append(parts, "titles="+strings.Join(limitStrings(copyVisibility.FindingTitles, 4), " | "))
	}
	if copyVisibility.Contract != nil {
		if len(copyVisibility.Contract.MissingDisclosureFields) > 0 {
			parts = append(parts, "missing="+strings.Join(copyVisibility.Contract.MissingDisclosureFields, ","))
		}
		if len(copyVisibility.Contract.RequiredAnswerShape) > 0 {
			parts = append(parts, "shape="+strings.Join(limitStrings(copyVisibility.Contract.RequiredAnswerShape, 3), " | "))
		}
		if copyVisibility.Contract.NextCommand != "" {
			parts = append(parts, "next="+copyVisibility.Contract.NextCommand)
		}
	}
	return strings.Join(parts, " ")
}

func (a *Agent) recordFinalAnswerCorrectionRequired(report *CodingHarnessReport) {
	if a == nil || a.Session == nil {
		return
	}
	visibility := finalAnswerCorrectionVisibilityFromReport(report, false)
	if visibility == nil {
		return
	}
	now := time.Now()
	if existing := a.Session.LastFinalAnswerCorrection; existing != nil {
		existing.Normalize()
		if existing.Required && !existing.Corrected && !existing.Rejected {
			visibility.AttemptCount = existing.AttemptCount + 1
			visibility.MaxAttempts = existing.MaxAttempts
			visibility.RecordedAt = existing.RecordedAt
			if visibility.RecordedAt.IsZero() {
				visibility.RecordedAt = now
			}
		}
	}
	if visibility.AttemptCount <= 0 {
		visibility.AttemptCount = 1
	}
	if visibility.MaxAttempts <= 0 {
		visibility.MaxAttempts = finalAnswerCorrectionDefaultMaxAttempts
	}
	visibility.LastAttemptAt = now
	visibility.Status = finalAnswerCorrectionStatusRequired
	visibility.recordedMessageCount = len(a.Session.Messages)
	visibility.hasRecordedMessages = true
	visibility.RecordedMessageCount = visibility.recordedMessageCount
	visibility.HasRecordedMessages = true
	visibility.Contract = buildFinalAnswerCorrectionContract(*visibility)
	a.Session.LastFinalAnswerCorrection = visibility
	if report != nil {
		report.FinalAnswerCorrection = visibility
	}
}

func (a *Agent) markFinalAnswerCorrectionAccepted() {
	if a == nil || a.Session == nil || a.Session.LastFinalAnswerCorrection == nil {
		return
	}
	// A missing coding-harness report is NOT acceptance: with no recorded report
	// there is no evidence the correction was rechecked and approved, so refuse
	// to promote the correction to accepted. Only an explicitly approved report
	// can mark the correction accepted; a nil or unapproved report leaves the
	// correction unverified and pending its required recheck.
	if a.Session.LastCodingHarnessReport == nil || !a.Session.LastCodingHarnessReport.Approved {
		return
	}
	visibility := *a.Session.LastFinalAnswerCorrection
	if visibility.hasRecordedMessages && finalAnswerCorrectionHasExternalUserAfter(a.Session, visibility.recordedMessageCount) {
		return
	}
	visibility.Corrected = true
	visibility.Rejected = false
	visibility.Status = finalAnswerCorrectionStatusAccepted
	if visibility.CorrectedAt.IsZero() {
		visibility.CorrectedAt = time.Now()
	}
	visibility.Normalize()
	if visibility.Contract != nil {
		visibility.Contract.State = finalAnswerCorrectionStatusAccepted
		visibility.Contract.Accepted = true
		visibility.Contract.Rejected = false
		visibility.Contract.Normalize(&visibility)
	}
	a.Session.LastFinalAnswerCorrection = &visibility
	if a.Session.LastCodingHarnessReport != nil {
		a.Session.LastCodingHarnessReport.FinalAnswerCorrection = &visibility
	}
}

func (a *Agent) markFinalAnswerCorrectionRejected(reason string) {
	if a == nil || a.Session == nil || a.Session.LastFinalAnswerCorrection == nil {
		return
	}
	visibility := *a.Session.LastFinalAnswerCorrection
	visibility.Rejected = true
	visibility.Corrected = false
	visibility.Status = finalAnswerCorrectionStatusRejected
	if strings.TrimSpace(reason) != "" {
		visibility.Reasons = append(visibility.Reasons, strings.TrimSpace(reason))
	}
	if visibility.RejectedAt.IsZero() {
		visibility.RejectedAt = time.Now()
	}
	visibility.Normalize()
	if visibility.Contract != nil {
		visibility.Contract.State = finalAnswerCorrectionStatusRejected
		visibility.Contract.Accepted = false
		visibility.Contract.Rejected = true
		visibility.Contract.ManualRecoveryNeeded = true
		visibility.Contract.Normalize(&visibility)
	}
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
