package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const maxFailureRepairAttempts = 8

type FailureRepairAttempt struct {
	ID             string    `json:"id,omitempty"`
	Trigger        string    `json:"trigger,omitempty"`
	Status         string    `json:"status,omitempty"`
	Command        string    `json:"command,omitempty"`
	Label          string    `json:"label,omitempty"`
	FailureKind    string    `json:"failure_kind,omitempty"`
	FailureSummary string    `json:"failure_summary,omitempty"`
	FirstError     string    `json:"first_error,omitempty"`
	Hint           string    `json:"hint,omitempty"`
	ChangedPaths   []string  `json:"changed_paths,omitempty"`
	RepeatedCount  int       `json:"repeated_count,omitempty"`
	NextSteps      []string  `json:"next_steps,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
	ResolvedAt     time.Time `json:"resolved_at,omitempty"`
}

func (a *Agent) startFailureRepairAttempt(report VerificationReport) *FailureRepairAttempt {
	if a == nil || a.Session == nil || !report.HasFailures() {
		return nil
	}
	attempt := buildFailureRepairAttempt(a.Session, report)
	a.Session.ActiveFailureRepair = &attempt
	a.Session.FailureRepairAttempts = append([]FailureRepairAttempt{attempt}, a.Session.FailureRepairAttempts...)
	a.Session.normalizeFailureRepairState()
	if a.Session.TaskState != nil {
		a.Session.TaskState.RecordEvent("failure_repair", strings.TrimSpace(a.Session.TaskState.ExecutorFocusNode), "verify", "Started failure repair loop.", attempt.RenderPromptSection(), "active", true)
	}
	return a.Session.ActiveFailureRepair
}

func (a *Agent) resolveFailureRepairAttempt(report VerificationReport) {
	if a == nil || a.Session == nil || a.Session.ActiveFailureRepair == nil {
		return
	}
	now := time.Now()
	active := a.Session.ActiveFailureRepair
	active.Status = "resolved"
	active.UpdatedAt = now
	active.ResolvedAt = now
	if !report.GeneratedAt.IsZero() {
		active.Hint = joinSentence(active.Hint, "Resolved by verification generated at "+report.GeneratedAt.Format(time.RFC3339)+".")
	} else {
		active.Hint = joinSentence(active.Hint, "Resolved by a passing verification run.")
	}
	for i := range a.Session.FailureRepairAttempts {
		if strings.EqualFold(strings.TrimSpace(a.Session.FailureRepairAttempts[i].ID), strings.TrimSpace(active.ID)) {
			a.Session.FailureRepairAttempts[i] = *active
			break
		}
	}
	a.Session.ActiveFailureRepair = nil
	a.Session.normalizeFailureRepairState()
}

func buildFailureRepairAttempt(sess *Session, report VerificationReport) FailureRepairAttempt {
	now := time.Now()
	step := firstFailedVerificationStep(report)
	summary := strings.TrimSpace(report.FailureSummary())
	if summary == "" {
		summary = strings.TrimSpace(step.Output)
	}
	firstErr := firstMeaningfulFailureLine(step.Output)
	if firstErr == "" {
		firstErr = firstMeaningfulFailureLine(summary)
	}
	attempt := FailureRepairAttempt{
		ID:             fmt.Sprintf("repair-%s", now.Format("20060102-150405.000")),
		Trigger:        strings.TrimSpace(report.Trigger),
		Status:         "active",
		Command:        strings.TrimSpace(step.Command),
		Label:          strings.TrimSpace(step.Label),
		FailureKind:    strings.TrimSpace(step.FailureKind),
		FailureSummary: compactPromptSection(summary, 500),
		FirstError:     compactPromptSection(firstErr, 240),
		Hint:           strings.TrimSpace(step.Hint),
		ChangedPaths:   normalizeTaskStateList(report.ChangedPaths, 32),
		StartedAt:      now,
		UpdatedAt:      now,
	}
	attempt.RepeatedCount = repeatedFailureRepairCount(sess, attempt)
	attempt.NextSteps = buildFailureRepairNextSteps(attempt)
	attempt.Normalize()
	return attempt
}

func firstFailedVerificationStep(report VerificationReport) VerificationStep {
	for _, step := range report.Steps {
		if step.Status == VerificationFailed {
			return step
		}
	}
	if len(report.Steps) > 0 {
		return report.Steps[0]
	}
	return VerificationStep{}
}

func firstMeaningfulFailureLine(text string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n")
	if normalized == "" {
		return ""
	}
	fallback := ""
	for _, line := range strings.Split(normalized, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if fallback == "" {
			fallback = trimmed
		}
		if containsAny(lower, "error", "fail", "failed", "failure", "panic", "exception", "undefined", "not found", "cannot", "fatal", "에러", "실패", "없", "못") {
			return trimmed
		}
	}
	return fallback
}

func repeatedFailureRepairCount(sess *Session, attempt FailureRepairAttempt) int {
	if sess == nil {
		return 1
	}
	signature := attempt.failureSignature()
	if signature == "" {
		return 1
	}
	count := 1
	for _, existing := range sess.FailureRepairAttempts {
		if strings.EqualFold(existing.failureSignature(), signature) {
			count++
		}
	}
	return count
}

func buildFailureRepairNextSteps(attempt FailureRepairAttempt) []string {
	steps := make([]string, 0)
	if len(attempt.ChangedPaths) > 0 {
		steps = append(steps, "Inspect the changed path(s) most likely tied to the failure: "+strings.Join(attempt.ChangedPaths, ", "))
	}
	if attempt.FirstError != "" {
		steps = append(steps, "Use the first meaningful failure line as the repair anchor: "+attempt.FirstError)
	}
	if attempt.Command != "" {
		steps = append(steps, "After a material fix, rerun the narrow failing command before broader verification: "+attempt.Command)
	}
	if attempt.RepeatedCount >= 2 {
		steps = append(steps, fmt.Sprintf("This failure signature repeated %d time(s); change the repair approach before rerunning the same command.", attempt.RepeatedCount))
	}
	if len(steps) == 0 {
		steps = append(steps, "Inspect the failure output, make one material fix, then rerun the narrowest relevant verification.")
	}
	return normalizeTaskStateList(steps, 6)
}

func (a *Agent) renderFailureRepairPrompt() string {
	if a == nil || a.Session == nil || a.Session.ActiveFailureRepair == nil {
		return ""
	}
	rendered := strings.TrimSpace(a.Session.ActiveFailureRepair.RenderPromptSection())
	if rendered == "" {
		return ""
	}
	return "Failure repair harness:\n" + rendered
}

func (a *Agent) appendFailureRepairPrompt(text string) string {
	repair := strings.TrimSpace(a.renderFailureRepairPrompt())
	if repair == "" {
		return text
	}
	if strings.TrimSpace(text) == "" {
		return repair
	}
	return strings.TrimSpace(text) + "\n\n" + repair
}

func (r *FailureRepairAttempt) Normalize() {
	if r == nil {
		return
	}
	r.ID = strings.TrimSpace(r.ID)
	r.Trigger = strings.TrimSpace(r.Trigger)
	r.Status = strings.TrimSpace(strings.ToLower(r.Status))
	if r.Status == "" {
		r.Status = "active"
	}
	r.Command = strings.TrimSpace(r.Command)
	r.Label = strings.TrimSpace(r.Label)
	r.FailureKind = strings.TrimSpace(r.FailureKind)
	r.FailureSummary = strings.TrimSpace(r.FailureSummary)
	r.FirstError = strings.TrimSpace(r.FirstError)
	r.Hint = strings.TrimSpace(r.Hint)
	r.ChangedPaths = normalizeTaskStateList(r.ChangedPaths, 32)
	r.NextSteps = normalizeTaskStateList(r.NextSteps, 6)
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.StartedAt
	}
}

func (r FailureRepairAttempt) RenderPromptSection() string {
	r.Normalize()
	if strings.TrimSpace(r.ID) == "" {
		return ""
	}
	lines := []string{
		"- Repair attempt: " + r.ID + " [" + r.Status + "]",
	}
	if r.Label != "" {
		lines = append(lines, "- Failed step: "+r.Label)
	}
	if r.Command != "" {
		lines = append(lines, "- Command: "+r.Command)
	}
	if r.FailureKind != "" {
		lines = append(lines, "- Failure kind: "+r.FailureKind)
	}
	if r.FirstError != "" {
		lines = append(lines, "- First error: "+r.FirstError)
	}
	if r.FailureSummary != "" {
		lines = append(lines, "- Summary: "+compactPromptSection(r.FailureSummary, 260))
	}
	if r.Hint != "" {
		lines = append(lines, "- Hint: "+compactPromptSection(r.Hint, 220))
	}
	if len(r.ChangedPaths) > 0 {
		lines = append(lines, "- Changed paths: "+strings.Join(r.ChangedPaths, ", "))
	}
	if r.RepeatedCount > 1 {
		lines = append(lines, fmt.Sprintf("- Repeated signature count: %d", r.RepeatedCount))
	}
	if len(r.NextSteps) > 0 {
		lines = append(lines, "- Next repair steps: "+strings.Join(r.NextSteps, " | "))
	}
	return strings.Join(lines, "\n")
}

func (r FailureRepairAttempt) failureSignature() string {
	parts := []string{
		strings.ToLower(strings.TrimSpace(r.Command)),
		strings.ToLower(strings.TrimSpace(r.Label)),
		strings.ToLower(strings.TrimSpace(r.FailureKind)),
		strings.ToLower(strings.TrimSpace(r.FirstError)),
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

func (s *Session) normalizeFailureRepairState() {
	if s == nil {
		return
	}
	if s.ActiveFailureRepair != nil {
		s.ActiveFailureRepair.Normalize()
		if strings.TrimSpace(s.ActiveFailureRepair.ID) == "" {
			s.ActiveFailureRepair = nil
		}
	}
	filtered := make([]FailureRepairAttempt, 0, len(s.FailureRepairAttempts))
	for _, attempt := range s.FailureRepairAttempts {
		attempt.Normalize()
		if strings.TrimSpace(attempt.ID) != "" {
			filtered = append(filtered, attempt)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
	})
	if len(filtered) > maxFailureRepairAttempts {
		filtered = filtered[:maxFailureRepairAttempts]
	}
	s.FailureRepairAttempts = filtered
}
