package main

import (
	"path/filepath"
	"strings"
)

func (a *Agent) finalAnswerCompletenessFindings(reply string, attemptedEditTool bool) []CodingHarnessFinding {
	if a == nil || a.Session == nil {
		return nil
	}
	if a.changesAreGeneratedDocumentArtifactsForTurn(codingHarnessSourcePrompt(a.Session)) {
		return nil
	}
	if finalAnswerCompletenessIsTrivialOrStatusOnly(a.Session) {
		return nil
	}
	var findings []CodingHarnessFinding
	if a.finalAnswerCompletenessRequiresModificationFacts(attemptedEditTool) {
		findings = append(findings, a.modificationFinalAnswerCompletenessFindings(reply)...)
	}
	if a.finalAnswerCompletenessRequiresReviewOnlyFacts() {
		findings = append(findings, a.reviewOnlyFinalAnswerCompletenessFindings(reply)...)
	}
	if a.finalAnswerCompletenessRequiresCrossReviewDisclosure(reply) {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Cross-review residual risk is undisclosed",
			Detail:   "The latest cross-review triage ledger still has incomplete, deferred, or user-decision items, but the final answer does not mention the cross-review outcome or residual risk.",
		})
	}
	return normalizeCodingHarnessFindings(findings)
}

func finalAnswerCompletenessIsTrivialOrStatusOnly(session *Session) bool {
	if session == nil || session.AcceptanceContract == nil {
		return false
	}
	mode := strings.TrimSpace(session.AcceptanceContract.Mode)
	switch mode {
	case "command", "project_knowledge", "diagnose_recent_error", "general":
		return len(currentTurnPatchTransactionChangedPaths(session)) == 0 && currentTurnActiveEditLoop(session) == nil
	default:
		return false
	}
}

func (a *Agent) finalAnswerCompletenessRequiresModificationFacts(attemptedEditTool bool) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if attemptedEditTool || len(currentTurnPatchTransactionChangedPaths(a.Session)) > 0 {
		return true
	}
	if loop := currentTurnActiveEditLoop(a.Session); loop != nil {
		loop.Normalize()
		if len(loop.ChangedPaths) > 0 || len(loop.WorkerSummaries) > 0 {
			return true
		}
	}
	if a.Session.AcceptanceContract != nil && strings.EqualFold(strings.TrimSpace(a.Session.AcceptanceContract.Mode), "inspect_and_fix") {
		return true
	}
	return false
}

func (a *Agent) modificationFinalAnswerCompletenessFindings(reply string) []CodingHarnessFinding {
	var findings []CodingHarnessFinding
	changed := currentTurnPatchTransactionChangedPaths(a.Session)
	if len(changed) == 0 {
		if !replyClaimsNoFileChanges(strings.ToLower(reply)) {
			findings = append(findings, CodingHarnessFinding{
				Severity: "blocker",
				Title:    "Changed-file summary is missing",
				Detail:   "This was a modification lifecycle, but no changed paths were recorded and the final answer does not clearly state that no file changed.",
			})
		}
	} else if !replyMentionsChangedFileSummary(reply, changed) {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Changed-file summary is missing",
			Detail:   "Changed paths are recorded in the patch transaction, but the final answer does not name the changed file(s): " + strings.Join(changed, ", "),
		})
	}
	if !replyMentionsReviewResult(reply) {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Review result is missing",
			Detail:   "A modification final answer must state the review or self-review result before it is shown to the user.",
		})
	}
	if !replyMentionsValidationResultForCompleteness(reply) {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Validation result is missing",
			Detail:   "A modification final answer must state validation results or explicitly say validation was not run.",
		})
	}
	if !replyMentionsRemainingRisk(reply) && !replyMentionsVerificationBlocker(reply) {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Remaining-risk statement is missing",
			Detail:   "A modification final answer must state remaining risks or say that no known remaining blocker remains.",
		})
	}
	return findings
}

func replyMentionsValidationResultForCompleteness(reply string) bool {
	if replyMentionsVerificationNotRun(reply) || replyClaimsVerificationSuccess(reply) {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(reply))
	if !verificationClaimLineHasSubject(lower) {
		return false
	}
	return containsAny(lower,
		"failed",
		"failing",
		"blocked",
		"blocker",
		"unavailable",
		"skipped",
		"declined",
		"not run",
		"실패",
		"차단",
		"불가",
		"생략",
		"미실행",
	)
}

func replyMentionsChangedFileSummary(reply string, changed []string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return false
	}
	for _, path := range changed {
		normalized := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
		if normalized == "" {
			continue
		}
		if strings.Contains(lower, normalized) {
			return true
		}
		base := strings.ToLower(filepath.Base(normalized))
		if base != "" && strings.Contains(lower, base) {
			return true
		}
	}
	return containsAny(lower,
		"changed files:",
		"modified files:",
		"updated files:",
		"files changed:",
		"변경 파일:",
		"수정 파일:",
	)
}

func replyMentionsReviewResult(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"self-review",
		"self review",
		"review:",
		"review result",
		"review passed",
		"review found",
		"reviewed",
		"post-change review",
		"리뷰",
		"검토",
	)
}

func (a *Agent) finalAnswerCompletenessRequiresReviewOnlyFacts() bool {
	if a == nil || a.Session == nil || a.Session.AcceptanceContract == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(a.Session.AcceptanceContract.Mode), "analysis_only") {
		return false
	}
	if len(currentTurnPatchTransactionChangedPaths(a.Session)) > 0 {
		return false
	}
	return hasTurnReviewIntent(a.Session.AcceptanceContract.SourcePrompt)
}

func (a *Agent) reviewOnlyFinalAnswerCompletenessFindings(reply string) []CodingHarnessFinding {
	var findings []CodingHarnessFinding
	if !replyLooksFindingsFirst(reply) {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Review-only answer is not findings-first",
			Detail:   "Review-only requests must lead with findings or an explicit no-findings result before summary context.",
		})
	}
	if !replyClaimsNoFileChanges(strings.ToLower(reply)) {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Review-only no-edit statement is missing",
			Detail:   "This review-only request is read-only, but the final answer does not clearly state that no files were edited.",
		})
	}
	if replyClaimsNoFindings(reply) && !replyMentionsResidualEvidenceRisk(reply) {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "No-finding review omits residual risk",
			Detail:   "A no-finding review answer must mention residual test or evidence risk.",
		})
	}
	return findings
}

func replyLooksFindingsFirst(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return false
	}
	first := strings.ToLower(compactPromptSection(trimmed, 260))
	return containsAny(first,
		"finding",
		"findings",
		"no findings",
		"no issues",
		"issue",
		"issues",
		"검토 결과",
		"finding",
		"발견",
		"문제",
		"이슈",
	)
}

func replyClaimsNoFindings(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	return containsAny(lower,
		"no findings",
		"no actionable findings",
		"no issues",
		"no bugs found",
		"no blocking findings",
		"finding 없음",
		"문제 없음",
		"이슈 없음",
		"버그 없음",
		"차단 항목 없음",
	)
}

func replyMentionsResidualEvidenceRisk(reply string) bool {
	if replyMentionsRemainingRisk(reply) || replyMentionsVerificationOutcome(reply) {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(reply))
	return containsAny(lower,
		"test gap",
		"evidence risk",
		"evidence gap",
		"not exhaustively tested",
		"not fully verified",
		"limited evidence",
		"검증 범위",
		"증거 범위",
		"테스트 공백",
		"검증 공백",
	)
}

func (a *Agent) finalAnswerCompletenessRequiresCrossReviewDisclosure(reply string) bool {
	if a == nil || a.Session == nil || a.Session.LastReviewRun == nil {
		return false
	}
	run := a.Session.LastReviewRun
	if run.CrossReviewTriage == nil || len(run.CrossReviewTriage.Items) == 0 {
		return false
	}
	meaningfulResidual := run.CrossReviewTriage.IncompleteCount > 0
	for _, item := range run.CrossReviewTriage.Items {
		switch normalizeCrossReviewTriageStatus(item.TriageStatus) {
		case crossReviewTriageAcceptedDeferred, crossReviewTriageNeedsUserDecision:
			meaningfulResidual = true
		}
	}
	if !meaningfulResidual {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(reply))
	return !containsAny(lower,
		"cross-review",
		"cross review",
		"second-pass review",
		"second pass review",
		"교차 리뷰",
		"2차 리뷰",
	)
}
