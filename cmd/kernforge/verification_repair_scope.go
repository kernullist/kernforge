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
	// Configuration/environment failures are never "the current patch is wrong".
	// Many product trees only maintain Release; a Debug or toolset failure must
	// not force a code repair loop.
	if verificationFailureIsNonCodeBuildIssue(step) {
		return false
	}
	changed = normalizeTaskStateList(changed, 32)
	if len(changed) == 0 {
		// No patch scope is known, so any failure is treated as in-scope for
		// repair decisions (full-workspace /verify without an active patch).
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
	// A Go package-scoped step ("go test ./cmd/app/...") compiles the whole
	// package that contains the changed files, so any compile failure in that
	// package blocks verifying the current patch even when the failing line
	// names a sibling file. Treat a concrete Go package scope that covers the
	// changed file's directory as in-scope. This is deliberately Go-only:
	// MSBuild/vcxproj sibling failures stay ambient (see
	// TestVerificationRepairScopeDoesNotTreatProjectBuildSiblingFailureAsPatchScoped),
	// and workspace-wide runs ("go test ./...") keep the ambient default.
	if verificationGoPackageScopeCoversChangedDir(step, changed) {
		return true
	}
	return verificationTextMentionsChangedPath(verificationFailureEvidenceText(step.Output), changed)
}

func verificationFailureIsNonCodeBuildIssue(step VerificationStep) bool {
	kind := strings.ToLower(strings.TrimSpace(step.FailureKind))
	switch kind {
	case "build_config", "build_environment", "command_not_found":
		return true
	}
	output := strings.ToLower(step.Output)
	command := strings.ToLower(step.Command + " " + step.Label)
	if looksLikeBuildConfigurationFailure(output, command) || looksLikeBuildEnvironmentFailure(output) {
		return true
	}
	return false
}

// verificationFailureTouchesChangedPaths reports whether any failed step is
// attributable to the current patch. When changed paths are empty the failure
// is treated as in-scope (same as verificationStepIsPatchScoped).
func verificationFailureTouchesChangedPaths(report VerificationReport, changed []string) bool {
	if !report.HasFailures() {
		return false
	}
	changed = normalizeTaskStateList(changed, 32)
	for _, step := range report.Steps {
		if step.Status != VerificationFailed {
			continue
		}
		if verificationStepIsPatchScoped(step, changed) {
			return true
		}
	}
	return false
}

func verificationReportIsOnlyNonCodeBuildIssue(report VerificationReport) bool {
	if !report.HasFailures() {
		return false
	}
	sawFailed := false
	for _, step := range report.Steps {
		if step.Status != VerificationFailed {
			continue
		}
		sawFailed = true
		if !verificationFailureIsNonCodeBuildIssue(step) {
			return false
		}
	}
	return sawFailed
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

// verificationGoPackageScopeCoversChangedDir reports whether a Go verification
// step carries a concrete package scope (for example "./cmd/app/...") that
// covers the directory of a changed file. Go package scopes are derived from
// the changed directories by buildGoVerificationSteps, so such a scope means
// the step exists precisely because the patch touched that package.
func verificationGoPackageScopeCoversChangedDir(step VerificationStep, changed []string) bool {
	command := strings.ToLower(" " + strings.TrimSpace(step.Command) + " " + strings.TrimSpace(step.Label) + " ")
	if !strings.Contains(command, " go test ") && !strings.Contains(command, " go build ") && !strings.Contains(command, " go vet ") {
		return false
	}
	scope := normalizeVerificationComparablePath(step.Scope)
	if scope == "" || scope == "workspace" || scope == "targeted" || scope == "..." {
		return false
	}
	for _, raw := range changed {
		dir := normalizeVerificationComparablePath(filepath.Dir(strings.TrimSpace(raw)))
		if dir == "" || dir == "." || dir == "/" {
			continue
		}
		if verificationTextMentionsPathSegment(scope, dir) {
			return true
		}
	}
	return false
}

// verificationTextMentionsPathSegment reports whether text contains segment
// as a whole path segment: it must not be preceded or followed by additional
// file-name characters (letters, digits, '_', '-', '.'). Path separators and
// wildcards around the segment still count as a match.
func verificationTextMentionsPathSegment(text string, segment string) bool {
	if segment == "" {
		return false
	}
	offset := 0
	for offset <= len(text) {
		idx := strings.Index(text[offset:], segment)
		if idx < 0 {
			return false
		}
		start := offset + idx
		end := start + len(segment)
		beforeOK := start == 0 || !isVerificationNameChar(text[start-1])
		afterOK := end >= len(text) || !isVerificationNameChar(text[end])
		if beforeOK && afterOK {
			return true
		}
		offset = start + 1
	}
	return false
}

func isVerificationNameChar(b byte) bool {
	return b == '_' || b == '-' || b == '.' || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

func automaticVerificationOutOfScopeMessage(cfg Config, report VerificationReport, decision verificationRepairScopeDecision) string {
	var lines []string
	lines = append(lines, localizedText(
		cfg,
		"Automatic verification failed, but the failure is not clearly tied to the current patch scope. Do not treat the user request as failed.",
		"자동 검증이 실패했지만, 실패 근거가 현재 patch scope와 명확히 연결되어 있지 않습니다. 사용자 요청 자체는 실패로 취급하지 마십시오.",
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
		"Do not broaden the repair into unrelated build files, project settings, or other source paths. Stop editing unless a failure line directly references the current patch. In the final answer, disclose this as ambient verification risk; the scoped edit may still complete successfully.",
		"관련 없는 빌드 파일, 프로젝트 설정, 다른 소스 경로로 수정 범위를 넓히지 마십시오. 실패 라인이 현재 patch를 직접 가리키지 않으면 편집을 중단하고, 최종 답변에서 환경성 검증 risk로 명시하십시오. 범위 안 편집은 성공으로 마무리할 수 있습니다.",
	))
	return strings.Join(lines, "\n")
}

// automaticVerificationOutOfScopeContinueGuidance is the guided-continuation
// message used when an out-of-scope verification failure does NOT force the
// turn to stop. The model decides with the user's request in mind: keep
// repairing when the failure blocks the request, or finish with an ambient
// risk disclosure when the failure is genuinely unrelated. The edit-loop
// retry budget still bounds repeated attempts of the same failure.
func automaticVerificationOutOfScopeContinueGuidance(cfg Config, report VerificationReport, decision verificationRepairScopeDecision) string {
	var lines []string
	lines = append(lines, localizedText(
		cfg,
		"Automatic verification failed, and the failure is not clearly tied to the current patch scope. Do not stop the turn; decide with the user's request in mind:",
		"자동 검증이 실패했고, 실패 근거가 현재 patch scope와 명확히 연결되어 있지 않습니다. 턴을 중단하지 말고 사용자 요청 기준으로 판단하십시오:",
	))
	lines = append(lines, localizedText(
		cfg,
		"- If this failure blocks completing the user's request (for example the package no longer builds, or the user asked you to fix these failures), keep repairing: investigate the failure output, fix the responsible files, and rerun verification.",
		"- 이 실패가 사용자 요청 완료를 막는 경우(예: 패키지가 더 이상 빌드되지 않거나, 사용자가 이 실패들을 고치라고 요청한 경우) 수리를 계속하십시오. 실패 출력을 조사하고 책임 파일을 고친 뒤 검증을 다시 실행하십시오.",
	))
	lines = append(lines, localizedText(
		cfg,
		"- If the failure is genuinely unrelated to the user's request (pre-existing breakage in unrelated projects or files), do not broaden the repair into them. Provide the final answer and disclose the failure as ambient verification risk; the user request itself is not failed.",
		"- 실패가 사용자 요청과 정말 무관한 경우(무관한 프로젝트나 파일의 기존 breakage) 수리를 그쪽으로 넓히지 마십시오. 최종 답변을 작성하고 환경성 검증 risk로 고지하십시오. 사용자 요청 자체는 실패가 아닙니다.",
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
	return strings.Join(lines, "\n")
}
