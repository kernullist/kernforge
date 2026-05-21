package main

import (
	"fmt"
	"strings"
	"time"
)

type TestImpactReport struct {
	GeneratedAt          time.Time              `json:"generated_at,omitempty"`
	ChangedPaths         []string               `json:"changed_paths,omitempty"`
	CodeLikeChangedPaths []string               `json:"code_like_changed_paths,omitempty"`
	RecommendedCommands  []string               `json:"recommended_commands,omitempty"`
	Confidence           string                 `json:"confidence,omitempty"`
	Notes                []string               `json:"notes,omitempty"`
	Gaps                 []string               `json:"gaps,omitempty"`
	Findings             []CodingHarnessFinding `json:"findings,omitempty"`
}

func (a *Agent) buildTestImpactReport() TestImpactReport {
	report := TestImpactReport{
		GeneratedAt: time.Now(),
		Confidence:  "not_applicable",
	}
	if a == nil || a.Session == nil {
		return report
	}
	changed := collectTestImpactChangedPaths(a.Session)
	report.ChangedPaths = changed
	codeChanged := filterCodeLikePaths(changed)
	report.CodeLikeChangedPaths = codeChanged
	if len(changed) == 0 {
		report.Notes = append(report.Notes, "No workspace changes were recorded for this turn.")
		report.Normalize()
		return report
	}
	if len(codeChanged) == 0 {
		report.Notes = append(report.Notes, "Only non-code-like paths changed; no code test impact command was inferred.")
		report.Normalize()
		return report
	}
	plan := buildVerificationPlanWithTuning(a.Workspace.Root, codeChanged, VerificationAdaptive, VerificationTuning{})
	for _, step := range plan.Steps {
		command := strings.TrimSpace(step.Command)
		if command != "" {
			report.RecommendedCommands = append(report.RecommendedCommands, command)
		}
	}
	report.RecommendedCommands = normalizeTaskStateList(report.RecommendedCommands, 12)
	if strings.TrimSpace(plan.PlannerNote) != "" {
		report.Notes = append(report.Notes, compactPromptSection(plan.PlannerNote, 280))
	}
	report.Confidence = testImpactConfidence(codeChanged, plan.Steps)
	if len(report.RecommendedCommands) == 0 {
		report.Gaps = append(report.Gaps, "No local verification command could be inferred for code-like changed path(s): "+strings.Join(codeChanged, ", "))
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: "warning",
			Title:    "No test impact command inferred",
			Detail:   "Code-like paths changed, but the verification planner could not infer a local test/build command.",
		})
	} else if !sessionHasSuccessfulVerificationEvidence(a.Session) {
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: "warning",
			Title:    "Recommended verification not recorded",
			Detail:   "Recommended command(s): " + strings.Join(report.RecommendedCommands, " | "),
		})
	}
	report.Normalize()
	return report
}

func collectTestImpactChangedPaths(sess *Session) []string {
	if sess == nil {
		return nil
	}
	paths := make([]string, 0)
	paths = append(paths, currentTurnPatchTransactionChangedPaths(sess)...)
	paths = append(paths, collectRecentSessionChangedPaths(sess)...)
	if len(paths) > 0 && sess.LastVerification != nil && changedPathsCovered(paths, sess.LastVerification.ChangedPaths) {
		paths = append(paths, sess.LastVerification.ChangedPaths...)
	}
	return normalizeTaskStateList(paths, 64)
}

func testImpactConfidence(changed []string, steps []VerificationStep) string {
	if len(changed) == 0 {
		return "not_applicable"
	}
	if len(steps) == 0 {
		return "low"
	}
	targeted := 0
	workspace := 0
	for _, step := range steps {
		switch strings.TrimSpace(strings.ToLower(step.Stage)) {
		case "targeted":
			targeted++
		case "workspace":
			workspace++
		}
	}
	switch {
	case targeted > 0 && workspace > 0:
		return "high"
	case targeted > 0 || workspace > 0:
		return "medium"
	default:
		return "low"
	}
}

func (r *TestImpactReport) Normalize() {
	if r == nil {
		return
	}
	r.ChangedPaths = normalizeTaskStateList(r.ChangedPaths, 64)
	r.CodeLikeChangedPaths = normalizeTaskStateList(r.CodeLikeChangedPaths, 64)
	r.RecommendedCommands = normalizeTaskStateList(r.RecommendedCommands, 12)
	r.Notes = normalizeTaskStateList(r.Notes, 8)
	r.Gaps = normalizeTaskStateList(r.Gaps, 8)
	r.Findings = normalizeCodingHarnessFindings(r.Findings)
	r.Confidence = strings.TrimSpace(strings.ToLower(r.Confidence))
	if r.Confidence == "" {
		r.Confidence = "not_applicable"
	}
}

func (r TestImpactReport) RenderPromptSection() string {
	r.Normalize()
	lines := make([]string, 0, 8)
	if len(r.ChangedPaths) > 0 {
		lines = append(lines, "- Changed paths: "+strings.Join(r.ChangedPaths, ", "))
	}
	if len(r.CodeLikeChangedPaths) > 0 {
		lines = append(lines, "- Code-like paths: "+strings.Join(r.CodeLikeChangedPaths, ", "))
	}
	if r.Confidence != "" {
		lines = append(lines, "- Confidence: "+r.Confidence)
	}
	if len(r.RecommendedCommands) > 0 {
		lines = append(lines, "- Recommended commands: "+strings.Join(r.RecommendedCommands, " | "))
	}
	if len(r.Gaps) > 0 {
		lines = append(lines, "- Gaps: "+strings.Join(r.Gaps, " | "))
	}
	for _, finding := range r.Findings {
		lines = append(lines, fmt.Sprintf("- [%s] %s: %s", finding.Severity, finding.Title, compactPromptSection(finding.Detail, 220)))
	}
	if len(r.Notes) > 0 {
		lines = append(lines, "- Notes: "+strings.Join(r.Notes, " | "))
	}
	return strings.Join(lines, "\n")
}
