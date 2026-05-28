package main

import (
	"path/filepath"
	"strings"
)

func (a *Agent) finalAnswerCompletenessFindings(reply string, attemptedEditTool bool) []CodingHarnessFinding {
	if a == nil || a.Session == nil {
		return nil
	}
	if finalAnswerCompletenessIsTrivialOrStatusOnly(a.Session) {
		return nil
	}
	var findings []CodingHarnessFinding
	if a.finalAnswerCompletenessRequiresDocumentArtifactFacts(reply) {
		findings = append(findings, a.documentArtifactFinalAnswerCompletenessFindings(reply)...)
		return normalizeCodingHarnessFindings(findings)
	}
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

func (a *Agent) finalAnswerCompletenessRequiresDocumentArtifactFacts(reply string) bool {
	if a == nil || a.Session == nil {
		return false
	}
	class := finalAnswerCompletenessRequestClass(a.Session)
	if currentTurnPatchTransactionIncludesNonDocumentChange(a.Session) {
		return false
	}
	if a.changesAreGeneratedDocumentArtifactsForTurn(codingHarnessSourcePrompt(a.Session)) {
		return true
	}
	if class == reviewRequestClassDocumentArtifact {
		if len(documentArtifactFinalAnswerPaths(a.Session, reply)) > 0 {
			return true
		}
		return replyClaimsDocumentArtifactCompletion(reply) || replyStatesNoDocumentArtifactCreated(reply)
	}
	return false
}

func finalAnswerCompletenessRequestClass(session *Session) string {
	if session == nil {
		return reviewRequestClassGeneral
	}
	if session.AcceptanceContract != nil {
		if class := normalizeReviewRequestClass(session.AcceptanceContract.RequestClass); class != reviewRequestClassGeneral {
			return class
		}
	}
	if session.LastReviewRun != nil {
		if class := normalizeReviewRequestClass(firstNonBlankString(session.LastReviewRun.RequestClass, session.LastReviewRun.RequestAnalysis.RequestClass)); class != reviewRequestClassGeneral {
			return class
		}
	}
	return reviewRequestClassGeneral
}

func finalAnswerCompletenessIsTrivialOrStatusOnly(session *Session) bool {
	if session == nil || session.AcceptanceContract == nil {
		return false
	}
	mode := strings.TrimSpace(session.AcceptanceContract.Mode)
	switch mode {
	case "command", "project_knowledge", "diagnose_recent_error", "general":
		if requestModeLooksCodeChanging(session.AcceptanceContract.SourcePrompt) {
			return false
		}
		return len(finalAnswerCompletenessChangedPaths(session)) == 0 && currentTurnActiveEditLoop(session) == nil
	default:
		return false
	}
}

func (a *Agent) finalAnswerCompletenessRequiresModificationFacts(attemptedEditTool bool) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if a.ReviewerClient != nil {
		return false
	}
	if a.Session.AcceptanceContract != nil &&
		normalizeReviewRequestClass(a.Session.AcceptanceContract.RequestClass) == reviewRequestClassDocumentArtifact &&
		!currentTurnPatchTransactionIncludesNonDocumentChange(a.Session) {
		return false
	}
	changed := finalAnswerCompletenessChangedPaths(a.Session)
	if len(changed) > 0 {
		return true
	}
	if attemptedEditTool {
		return false
	}
	if loop := currentTurnActiveEditLoop(a.Session); loop != nil {
		loop.Normalize()
		if len(loop.ChangedPaths) > 0 {
			return true
		}
	}
	return false
}

func currentTurnPatchTransactionIncludesNonDocumentChange(session *Session) bool {
	for _, path := range finalAnswerCompletenessChangedPaths(session) {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if !pathLooksLikeDocumentArtifact(path) {
			return true
		}
	}
	return false
}

func (a *Agent) documentArtifactFinalAnswerCompletenessFindings(reply string) []CodingHarnessFinding {
	paths := documentArtifactFinalAnswerPaths(a.Session, reply)
	var findings []CodingHarnessFinding
	noArtifactCreated := replyStatesNoDocumentArtifactCreated(reply)
	if len(paths) == 0 && !noArtifactCreated {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Document artifact path is missing",
			Detail:   "A document-artifact final answer must name the artifact path that was created or updated.",
		})
	} else if len(paths) > 0 && !noArtifactCreated && !replyMentionsChangedFileSummary(reply, paths) {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Document artifact path is missing",
			Detail:   "The document artifact path is known, but the final answer does not name it: " + strings.Join(paths, ", "),
		})
	}
	if !noArtifactCreated && !replyMentionsArtifactQualityStatus(reply) {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Document artifact quality status is missing",
			Detail:   "A document-artifact final answer must disclose the artifact-quality check status instead of only saying it is done.",
		})
	}
	if !replyMentionsVerificationOutcome(reply) {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Document artifact verification disclosure is missing",
			Detail:   "A document-artifact final answer must say whether build/test verification ran, did not run, or is blocked.",
		})
	}
	if !replyMentionsDocumentArtifactLimitation(reply) {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Document artifact limitation statement is missing",
			Detail:   "A document-artifact final answer must state remaining limitations or that no known remaining artifact limitation is recorded.",
		})
	}
	return findings
}

func replyStatesNoDocumentArtifactCreated(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"did not create",
		"didn't create",
		"not created",
		"was not created",
		"no artifact was created",
		"no document was created",
		"no files were changed",
		"no file was changed",
		"생성하지 않았",
		"생성되지 않았",
		"만들지 않았",
		"문서를 만들지",
		"파일 변경 없음",
		"변경된 파일 없음",
	)
}

func replyClaimsDocumentArtifactCompletion(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return false
	}
	if containsAny(lower,
		"continue",
		"continuing",
		"will proceed",
		"ready to proceed",
		"first ",
		"진행하겠습니다",
		"진행합니다",
		"계속합니다",
		"넘어갈 준비",
		"먼저 ",
		"확인한 뒤",
	) {
		return false
	}
	return containsAny(lower,
		"created",
		"generated",
		"wrote",
		"written",
		"saved",
		"complete",
		"completed",
		"document is done",
		"report is done",
		"문서를 생성",
		"문서가 생성",
		"문서를 작성",
		"문서가 작성",
		"문서만 작성",
		"보고서를 생성",
		"보고서를 작성",
		"저장",
		"완료",
		"기록했습니다",
	)
}

func documentArtifactFinalAnswerPaths(session *Session, reply string) []string {
	if session == nil {
		return nil
	}
	paths := make([]string, 0)
	if session.AcceptanceContract != nil {
		for _, path := range session.AcceptanceContract.RequiredArtifacts {
			if pathLooksLikeDocumentArtifact(path) {
				paths = append(paths, normalizeSessionRelativePath(path))
			}
		}
	}
	for _, path := range currentTurnPatchTransactionChangedPaths(session) {
		if pathLooksLikeDocumentArtifact(path) {
			paths = append(paths, normalizeSessionRelativePath(path))
		}
	}
	paths = append(paths, generatedDocumentArtifactPathsFromHarnessReport(session.LastCodingHarnessReport)...)
	for _, path := range extractClaimedArtifactPaths(reply) {
		if pathLooksLikeDocumentArtifact(path) {
			paths = append(paths, normalizeSessionRelativePath(path))
		}
	}
	return normalizeTaskStateList(paths, 16)
}

func replyMentionsArtifactQualityStatus(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"artifact-quality",
		"artifact quality",
		"quality check",
		"quality gate",
		"content quality",
		"document-content",
		"document content",
		"placeholder",
		"topic coverage",
		"품질 검사",
		"품질 게이트",
		"산출물 품질",
		"문서 품질",
		"문서 내용",
		"내용 차단",
		"내용 확인",
	)
}

func replyMentionsDocumentArtifactLimitation(reply string) bool {
	if replyMentionsRemainingRisk(reply) || replyMentionsVerificationBlocker(reply) {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(reply))
	return containsAny(lower,
		"remaining limitation",
		"remaining limitations",
		"known limitation",
		"known limitations",
		"no known remaining limitation",
		"no known remaining limitations",
		"no remaining artifact limitation",
		"no remaining document limitation",
		"limited to",
		"limitation:",
		"limitations:",
		"잔여 제한",
		"남은 제한",
		"알려진 제한",
		"남은 한계",
		"잔여 한계",
		"남은 산출물 제한",
		"남은 문서 제한",
		"제한 없음",
		"한계 없음",
	)
}

func renderFinalAnswerContractPrompt(session *Session) string {
	if session == nil {
		return ""
	}
	class := finalAnswerCompletenessRequestClass(session)
	if class == reviewRequestClassGeneral {
		return ""
	}
	contract := reviewLifecycleContractForClass(class)
	if contract == nil {
		return ""
	}
	contract.Normalize()
	lines := []string{
		"- Request class: " + contract.RequestClass,
	}
	if contract.ReadOnly {
		lines = append(lines, "- Read-only: true")
	}
	if contract.RequiresReviewBeforeModify {
		lines = append(lines, "- Requires review before modification: true")
	}
	if contract.RequiresPostChangeReview {
		lines = append(lines, "- Requires post-change review or explicit self-review state: true")
	}
	if contract.RequiresDocumentGate {
		lines = append(lines, "- Requires document artifact-quality gate: true")
	}
	if contract.RequiresVerificationDisclosure {
		lines = append(lines, "- Requires verification disclosure: true")
	}
	if contract.RequiresValidationDisclosure {
		lines = append(lines, "- Requires validation disclosure: true")
	}
	if len(contract.FinalAnswerRequirements) > 0 {
		lines = append(lines, "- Final answer must disclose: "+strings.Join(contract.FinalAnswerRequirements, ", "))
	}
	if len(contract.SkipRules) > 0 {
		lines = append(lines, "- Skip rules: "+strings.Join(contract.SkipRules, " | "))
	}
	return strings.Join(lines, "\n")
}

func finalAnswerCompletenessChangedPaths(session *Session) []string {
	changed := currentTurnPatchTransactionChangedPaths(session)
	if len(changed) == 0 {
		changed = sessionPatchTransactionChangedPaths(session)
	}
	if len(changed) == 0 && session != nil && session.ActivePatchTransaction != nil {
		changed = session.ActivePatchTransaction.ChangedPaths()
	}
	if len(changed) == 0 && session != nil && len(session.PatchTransactions) > 0 {
		changed = session.PatchTransactions[0].ChangedPaths()
	}
	if len(changed) == 0 {
		changed = finalAnswerCompletenessToolChangedPaths(session)
	}
	return normalizeTaskStateList(changed, 64)
}

func finalAnswerCompletenessToolChangedPaths(session *Session) []string {
	if session == nil {
		return nil
	}
	paths := make([]string, 0)
	successToolCallIDs := map[string]bool{}
	toolResultByID := map[string]Message{}
	for _, msg := range session.Messages {
		if msg.Role != "tool" || msg.IsError {
			continue
		}
		if strings.TrimSpace(msg.ToolCallID) != "" {
			successToolCallIDs[strings.TrimSpace(msg.ToolCallID)] = true
			toolResultByID[strings.TrimSpace(msg.ToolCallID)] = msg
		}
		if !toolResultLooksWorkspaceChanging(msg) {
			continue
		}
		switch strings.TrimSpace(msg.ToolName) {
		case "write_file", "replace_in_file":
			if path := normalizeSessionRelativePath(toolMetaString(msg.ToolMeta, "path")); path != "" {
				paths = append(paths, path)
			}
		case "apply_patch":
			for _, path := range toolMetaStringSlice(msg.ToolMeta, "changed_paths") {
				if normalized := normalizeSessionRelativePath(path); normalized != "" {
					paths = append(paths, normalized)
				}
			}
		}
	}
	for _, msg := range session.Messages {
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		for _, call := range msg.ToolCalls {
			if !successToolCallIDs[strings.TrimSpace(call.ID)] {
				continue
			}
			if result, ok := toolResultByID[strings.TrimSpace(call.ID)]; ok && !toolResultLooksWorkspaceChanging(result) {
				continue
			}
			switch strings.TrimSpace(call.Name) {
			case "write_file", "replace_in_file":
				if path := normalizeSessionRelativePath(toolCallPathArgument(call)); path != "" {
					paths = append(paths, path)
				}
			}
		}
	}
	return normalizeTaskStateList(paths, 64)
}

func toolResultLooksWorkspaceChanging(msg Message) bool {
	if msg.Role != "tool" || msg.IsError {
		return false
	}
	if toolMetaBool(msg.ToolMeta, "changed_workspace") {
		return true
	}
	if len(toolMetaStringSlice(msg.ToolMeta, "changed_paths")) > 0 {
		return true
	}
	if len(toolMetaStringSlice(msg.ToolMeta, "changed_paths_after")) > 0 {
		return true
	}
	return false
}

func (a *Agent) modificationFinalAnswerCompletenessFindings(reply string) []CodingHarnessFinding {
	changed := finalAnswerCompletenessChangedPaths(a.Session)
	var findings []CodingHarnessFinding
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
	contractClass := normalizeReviewRequestClass(a.Session.AcceptanceContract.RequestClass)
	if contractClass == reviewRequestClassReviewOnly {
		return len(currentTurnPatchTransactionChangedPaths(a.Session)) == 0 && len(a.Session.Messages) == 0
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
	if strings.HasPrefix(first, "summary:") ||
		strings.HasPrefix(first, "summary ") ||
		strings.HasPrefix(first, "요약:") ||
		strings.HasPrefix(first, "요약 ") {
		return false
	}
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
	) || !containsAny(first, "summary", "요약", "context", "background", "배경")
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
