package main

import (
	"path/filepath"
	"strings"
)

type verificationRepairScopeDecision struct {
	ShouldRepair bool
	Reason       string
	ChangedPaths []string
	Anchor       string
}

func (a *Agent) verificationFailureRepairScope(report VerificationReport) verificationRepairScopeDecision {
	changed := normalizeTaskStateList(verificationRepairChangedPaths(a, report), 32)
	if !report.HasFailures() {
		return verificationRepairScopeDecision{
			ChangedPaths: changed,
			Reason:       "verification passed",
		}
	}
	if report.HasCommandMissingFailure() {
		return verificationRepairScopeDecision{
			ChangedPaths: changed,
			Reason:       "verification tool is unavailable",
			Anchor:       report.FailureSummary(),
		}
	}
	for _, step := range report.Steps {
		if step.Status != VerificationFailed {
			continue
		}
		if verificationStepIsPatchScoped(step, changed) {
			return verificationRepairScopeDecision{
				ShouldRepair: true,
				ChangedPaths: changed,
				Reason:       "failing verification step is scoped to the current patch",
				Anchor:       firstNonBlankString(firstMeaningfulFailureLine(step.Output), step.Hint, step.Label),
			}
		}
	}
	return verificationRepairScopeDecision{
		ChangedPaths: changed,
		Reason:       "failing verification output does not reference the current patch scope",
		Anchor:       report.FailureSummary(),
	}
}

func verificationRepairChangedPaths(a *Agent, report VerificationReport) []string {
	var paths []string
	if a != nil && a.Session != nil {
		paths = append(paths, currentTurnPatchTransactionChangedPaths(a.Session)...)
		paths = append(paths, collectRecentSessionChangedPaths(a.Session)...)
	}
	paths = append(paths, report.ChangedPaths...)
	return paths
}

func verificationStepIsPatchScoped(step VerificationStep, changed []string) bool {
	changed = normalizeTaskStateList(changed, 32)
	if len(changed) == 0 {
		return true
	}
	scope := strings.TrimSpace(strings.ToLower(step.Scope))
	if scope != "" && scope != "workspace" && scope != "targeted" && verificationTextMentionsChangedPath(scope, changed) {
		return true
	}
	text := strings.Join([]string{
		step.Label,
		step.Command,
		step.Scope,
		step.Hint,
		step.FailureKind,
	}, "\n")
	if verificationTextMentionsChangedPath(text, changed) {
		return true
	}
	return verificationTextMentionsChangedPath(verificationFailureEvidenceText(step.Output), changed)
}

func verificationFailureEvidenceText(output string) string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if containsAny(lower, " error ", ": error", "failed", "failing", "failure", "fail:", " fail", "panic:", "exception", "undefined", "unresolved", "fatal", "traceback") {
			lines = append(lines, trimmed)
		}
	}
	return strings.Join(lines, "\n")
}

func verificationTextMentionsChangedPath(text string, changed []string) bool {
	text = normalizeVerificationComparablePath(text)
	if text == "" {
		return false
	}
	for _, raw := range changed {
		path := normalizeVerificationComparablePath(raw)
		if path == "" {
			continue
		}
		base := normalizeVerificationComparablePath(filepath.Base(path))
		switch {
		case strings.Contains(text, path):
			return true
		case base != "" && base != "." && strings.Contains(text, base):
			return true
		}
	}
	return false
}

func automaticVerificationOutOfScopeMessage(cfg Config, report VerificationReport, decision verificationRepairScopeDecision) string {
	var lines []string
	lines = append(lines, localizedText(
		cfg,
		"Automatic verification failed, but the failure is not clearly tied to the current patch scope.",
		"자동 검증이 실패했지만, 실패 근거가 현재 patch scope와 명확히 연결되어 있지 않습니다.",
	))
	if len(decision.ChangedPaths) > 0 {
		lines = append(lines, localizedText(cfg, "Current patch scope: ", "현재 patch scope: ")+strings.Join(limitStrings(decision.ChangedPaths, 8), ", "))
	}
	if strings.TrimSpace(decision.Reason) != "" {
		lines = append(lines, localizedText(cfg, "Scope decision: ", "scope 판정: ")+decision.Reason)
	}
	if anchor := strings.TrimSpace(decision.Anchor); anchor != "" {
		lines = append(lines, localizedText(cfg, "Failure anchor: ", "실패 anchor: ")+compactPromptSection(anchor, 260))
	}
	if failure := strings.TrimSpace(report.FailureSummary()); failure != "" {
		lines = append(lines, localizedText(cfg, "Verification failure summary:", "검증 실패 요약:"))
		lines = append(lines, failure)
	}
	lines = append(lines, localizedText(
		cfg,
		"Do not broaden the repair into unrelated build files, project settings, or other source paths. Stop editing unless a failure line directly references the current patch. In the final answer, disclose the verification failure as an external or ambient blocker/risk.",
		"관련 없는 빌드 파일, 프로젝트 설정, 다른 소스 경로로 수정 범위를 넓히지 마십시오. 실패 라인이 현재 patch를 직접 가리키지 않으면 편집을 중단하고, 최종 답변에서 이 검증 실패를 외부/환경성 blocker 또는 risk로 명시하십시오.",
	))
	return strings.Join(lines, "\n")
}
