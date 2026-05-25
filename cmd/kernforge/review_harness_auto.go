package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const generatedDocumentArtifactQualityFingerprint = "generated-document-artifact-quality"

func (a *Agent) maybeRunPostChangeReview(ctx context.Context, request string, lastFingerprint string) (bool, bool, string, string, error) {
	if a == nil || a.Session == nil {
		return false, false, "", "", nil
	}
	reviewCfg := configReviewHarness(a.Config)
	if reviewCfg.AutoAfterChange == nil || !*reviewCfg.AutoAfterChange {
		return false, false, "", "", nil
	}
	root := workspaceSnapshotRoot(a.Workspace)
	if strings.TrimSpace(root) == "" {
		root = a.Workspace.Root
	}
	if strings.TrimSpace(root) == "" {
		root = a.Session.WorkingDir
	}
	if strings.TrimSpace(root) == "" {
		return false, false, "", "", nil
	}
	changedPaths := autoReviewChangedPaths(a.Session, root)
	if len(changedPaths) == 0 {
		return false, false, "", "", nil
	}
	if skipRequest := postChangeGeneratedDocumentArtifactRequest(a.Session, request, changedPaths); skipRequest != "" {
		artifactFingerprint := generatedDocumentArtifactQualityFingerprintForPaths(root, changedPaths)
		if generatedDocumentArtifactQualityAlreadyAccepted(a.Session, artifactFingerprint, lastFingerprint) {
			return false, false, "", artifactFingerprint, nil
		}
		if needsRevision, feedback := a.validateGeneratedDocumentArtifactForPostChangeSkip(skipRequest, changedPaths); needsRevision {
			a.Session.LastDocumentArtifactFingerprint = ""
			if a.EmitProgress != nil {
				a.EmitProgress(localizedTextForReviewRequest(a.Config, skipRequest, "Generated document artifact quality checks found blockers. Asking the model to revise the document artifact without starting code review.", "생성 문서 산출물 품질 검사에서 차단 항목을 발견했습니다. 코드 리뷰를 시작하지 않고 문서 산출물 수정을 요청합니다."))
			}
			return true, true, feedback, artifactFingerprint, nil
		}
		a.Session.LastDocumentArtifactFingerprint = artifactFingerprint
		if a.EmitProgress != nil {
			a.EmitProgress(localizedTextForReviewRequest(a.Config, skipRequest, "Skipping automatic post-change review because this turn only generated document artifacts. Artifact quality checks will validate the saved report without starting a repair loop.", "이번 턴은 생성 문서 산출물만 변경했으므로 자동 변경 후 리뷰를 건너뜁니다. 저장된 보고서는 산출물 품질 검사로 확인하고 코드 수리 루프는 시작하지 않습니다."))
		}
		return true, false, "", artifactFingerprint, nil
	}
	if a.Session.LastReviewRun != nil &&
		a.Session.LastReviewRun.AutoTriggered &&
		strings.EqualFold(a.Session.LastReviewRun.Trigger, "post_change") &&
		a.Session.LastReviewRun.ReviewFingerprint != "" &&
		a.Session.LastReviewRun.ReviewFingerprint == strings.TrimSpace(lastFingerprint) &&
		postChangeReviewRunStillMatchesSessionEvidence(a.Session.LastReviewRun, a.Session) {
		cachedRun := *a.Session.LastReviewRun
		needsRevision := cachedRun.Gate.Verdict == reviewVerdictNeedsRevision ||
			cachedRun.Gate.Verdict == reviewVerdictBlocked ||
			cachedRun.Gate.Verdict == reviewVerdictInsufficientEvidence
		if !needsRevision {
			return false, false, "", lastFingerprint, nil
		}
		return true, true, formatPostChangeReviewFeedback(a.Config, cachedRun, true), lastFingerprint, nil
	}
	if a.EmitProgress != nil {
		a.EmitProgress(localizedTextForReviewRequest(a.Config, request, "Running automatic post-change review...", "자동 변경 후 리뷰를 실행합니다..."))
	}
	rt := a.reviewHarnessRuntime(root)
	run, err := runReviewHarness(ctx, rt, ReviewHarnessOptions{
		Trigger:         "post_change",
		Target:          reviewTargetChange,
		Request:         request,
		Paths:           append([]string(nil), changedPaths...),
		IncludeGitDiff:  true,
		NoModel:         !reviewHarnessHasConfiguredModelRoute(a),
		AutoTriggered:   true,
		AutoFollowUp:    reviewCfg.AutoFollowUp,
		MaxContextChars: reviewFocusedMaxContextChars,
	})
	if err != nil {
		return true, false, "", "", err
	}
	fingerprint := strings.TrimSpace(run.ReviewFingerprint)
	needsRevision := run.Gate.Verdict == reviewVerdictNeedsRevision ||
		run.Gate.Verdict == reviewVerdictBlocked ||
		run.Gate.Verdict == reviewVerdictInsufficientEvidence
	feedback := formatPostChangeReviewFeedback(a.Config, run, needsRevision)
	return true, needsRevision, feedback, fingerprint, nil
}

func postChangeReviewRunStillMatchesSessionEvidence(run *ReviewRun, session *Session) bool {
	if run == nil || session == nil {
		return true
	}
	runSummary := strings.TrimSpace(run.Evidence.VerificationSummary)
	runFailed := run.Evidence.VerificationFailed
	currentSummary := ""
	currentFailed := false
	if session.LastVerification != nil {
		currentSummary = strings.TrimSpace(session.LastVerification.SummaryLine())
		currentFailed = session.LastVerification.HasFailures()
	}
	return strings.EqualFold(runSummary, currentSummary) && runFailed == currentFailed
}

func generatedDocumentArtifactQualityFingerprintForPaths(root string, changedPaths []string) string {
	parts := []string{generatedDocumentArtifactQualityFingerprint}
	for _, path := range normalizeTaskStateList(changedPaths, 64) {
		normalized := normalizeSessionRelativePath(path)
		parts = append(parts, "path:"+normalized)
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, filepath.FromSlash(normalized))
		}
		if strings.TrimSpace(root) != "" && !pathWithinRoot(root, abs) {
			parts = append(parts, "outside-root")
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			if os.IsNotExist(err) {
				parts = append(parts, "missing")
			} else {
				parts = append(parts, "read-error")
			}
			continue
		}
		parts = append(parts, fmt.Sprintf("size:%d", len(data)), string(data))
	}
	return generatedDocumentArtifactQualityFingerprint + ":" + computeReviewFingerprint(parts...)
}

func isGeneratedDocumentArtifactQualityFingerprint(fingerprint string) bool {
	trimmed := strings.TrimSpace(fingerprint)
	return strings.EqualFold(trimmed, generatedDocumentArtifactQualityFingerprint) ||
		strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(generatedDocumentArtifactQualityFingerprint)+":")
}

func generatedDocumentArtifactQualityAlreadyAccepted(session *Session, artifactFingerprint string, lastFingerprint string) bool {
	if session == nil || strings.TrimSpace(artifactFingerprint) == "" {
		return false
	}
	if !sessionHasDocumentArtifactContentAcceptedHarness(session) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(lastFingerprint), artifactFingerprint) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(session.LastDocumentArtifactFingerprint), artifactFingerprint)
}

func (a *Agent) reviewProposedEdit(ctx context.Context, preview EditPreview) error {
	if a == nil || a.Session == nil {
		return nil
	}
	reviewCfg := configReviewHarness(a.Config)
	if reviewCfg.AutoAfterChange == nil || !*reviewCfg.AutoAfterChange {
		return nil
	}
	diff := strings.TrimSpace(preview.Preview)
	if diff == "" {
		return nil
	}
	if paths := preWritePreviewCurrentEditPaths(preview); len(paths) > 0 {
		preview.Paths = paths
	}
	request := preWriteReviewUserRequest(a.Session)
	if skipRequest := preWriteGeneratedDocumentArtifactRequest(a.Session, preview, request); skipRequest != "" {
		if a.EmitProgress != nil {
			a.EmitProgress(localizedTextForReviewRequest(a.Config, skipRequest, "Skipping blocking pre-write review for generated document artifact; artifact quality checks will validate the saved report after writing.", "생성 문서 산출물은 차단형 쓰기 전 리뷰를 건너뜁니다. 저장 후 산출물 품질 검사로 보고서를 확인합니다."))
		}
		return nil
	}
	root := workspaceSnapshotRoot(a.Workspace)
	if strings.TrimSpace(root) == "" {
		root = a.Workspace.Root
	}
	if strings.TrimSpace(root) == "" {
		root = a.Session.WorkingDir
	}
	if strings.TrimSpace(root) == "" {
		return nil
	}
	if a.EmitProgress != nil {
		a.EmitProgress(localizedTextForReviewRequest(a.Config, request, "Running automatic pre-write review...", "자동 쓰기 전 리뷰를 실행합니다..."))
		a.EmitProgress(localizedTextForReviewRequest(a.Config, request, "Main model prepared an edit proposal. Sending the diff to the review model before writing files.", "메인 모델이 수정안을 만들었습니다. 파일 쓰기 전에 diff를 리뷰 모델에 전달합니다."))
	}
	rt := a.reviewHarnessRuntime(root)
	reviewerGatePolicy := ""
	if preWriteMainOnlyReviewerFallbackApproved(a.Session) {
		if a.Workspace.PreviewEdit != nil {
			reviewerGatePolicy = reviewReviewerGatePolicyMainOnlyFallback
			if a.EmitProgress != nil {
				a.EmitProgress(localizedTextForReviewRequest(a.Config, request, "User approved main-model-only pre-write fallback. Cross-reviewer failure will be recorded, but it will not block this edit before diff preview.", "사용자가 메인 모델 기준 쓰기 전 리뷰 fallback을 승인했습니다. cross reviewer 실패는 기록하지만 이번 편집의 diff preview 진입을 막지는 않습니다."))
			}
		} else if a.EmitProgress != nil {
			a.EmitProgress(localizedTextForReviewRequest(a.Config, request, "Main-model-only fallback was requested, but no diff preview confirmation is available, so the reviewer gate remains a hard stop.", "메인 모델 기준 fallback이 요청되었지만 diff preview 확인을 사용할 수 없어 reviewer gate를 계속 hard stop으로 유지합니다."))
		}
	}
	run, err := runReviewHarness(ctx, rt, ReviewHarnessOptions{
		Trigger:            "pre_write",
		Target:             reviewTargetChange,
		Request:            request,
		Paths:              append([]string(nil), preview.Paths...),
		ProvidedDiff:       diff,
		IncludeGitDiff:     false,
		NoModel:            !reviewHarnessCanUseAnyModel(a),
		AutoTriggered:      true,
		AutoFollowUp:       "none",
		EditProposals:      editProposalsForPreview(preview),
		RepairFindings:     preWriteRepairObligationsFromLastReview(a.Session),
		MaxContextChars:    reviewPreWriteMaxContextChars,
		ReviewerGatePolicy: reviewerGatePolicy,
	})
	if err != nil {
		if a.EmitProgress != nil {
			a.EmitProgress(localizedTextForReviewRequest(a.Config, request, "Automatic pre-write review failed: ", "자동 쓰기 전 리뷰 실패: ") + err.Error())
		}
		return fmt.Errorf("automatic pre-write review failed before writing: %w", err)
	}
	needsRevision := run.Gate.Verdict == reviewVerdictNeedsRevision ||
		run.Gate.Verdict == reviewVerdictBlocked ||
		run.Gate.Verdict == reviewVerdictInsufficientEvidence
	if needsRevision && reviewRunHasRequiredReviewerFailure(run) {
		if a.EmitProgress != nil {
			a.emitPreWriteFinalVisibleReviewSummary(run, false)
			a.EmitProgress(formatPreWriteFinalReviewProgress(a.Config, run, false))
			a.EmitProgress(reviewRunLocalizedText(a.Config, run, "Automatic pre-write review could not use a required review route. Stopping the edit loop.", "자동 쓰기 전 리뷰에서 필수 리뷰 route 결과를 신뢰할 수 없어 편집 루프를 중단합니다."))
		}
		return fmt.Errorf("%w: %s", ErrReviewerGateUnavailable, formatReviewerGateUnavailableToolError(a.Config, run))
	}
	if needsRevision {
		if a.EmitProgress != nil {
			a.emitPreWriteFinalVisibleReviewSummary(run, false)
			a.EmitProgress(formatPreWriteFinalReviewProgress(a.Config, run, false))
			a.EmitProgress(reviewRunLocalizedText(a.Config, run, "Review model returned required changes. Sending the result back to the main model for a revised patch.", "리뷰 모델이 수정 필수 항목을 반환했습니다. 메인 모델에 결과를 전달해 패치를 다시 작성하게 합니다."))
		}
		return fmt.Errorf("%s\n\n%s",
			reviewRunLocalizedText(a.Config, run, "automatic pre-write review blocked this edit before writing:", "자동 쓰기 전 리뷰가 파일 쓰기를 차단했습니다:"),
			formatPreWriteReviewFeedback(a.Config, run))
	}
	if warningBlockers := preWriteReviewBlockingWarningFindings(run); len(warningBlockers) > 0 {
		if a.EmitProgress != nil {
			a.emitPreWriteFinalVisibleReviewSummary(run, false)
			a.EmitProgress(formatPreWriteFinalReviewProgress(a.Config, run, false))
			a.EmitProgress(reviewRunLocalizedText(a.Config, run, "Review model returned actionable warnings. Sending the result back to the main model for a revised patch.", "리뷰 모델이 수정이 필요한 경고를 반환했습니다. 메인 모델에 결과를 전달해 패치를 다시 작성하게 합니다."))
		}
		return fmt.Errorf("%s\n\n%s",
			reviewRunLocalizedText(a.Config, run, "automatic pre-write review blocked this edit on actionable warnings before writing:", "자동 쓰기 전 리뷰가 수정 필요한 경고 때문에 파일 쓰기를 차단했습니다:"),
			formatPreWriteReviewWarningBlockFeedback(a.Config, run, warningBlockers))
	}
	if a.EmitProgress != nil {
		a.emitPreWriteFinalVisibleReviewSummary(run, true)
		a.EmitProgress(formatPreWriteFinalReviewProgress(a.Config, run, true))
	}
	return nil
}

func preWritePreviewLooksLikeGeneratedDocumentArtifact(preview EditPreview, request string) bool {
	return preWriteGeneratedDocumentArtifactRequest(nil, preview, request) != ""
}

func preWriteGeneratedDocumentArtifactRequest(session *Session, preview EditPreview, request string) string {
	contextRequest := generatedDocumentArtifactRequestContext(session, request)
	if contextRequest == "" {
		return ""
	}
	paths := preWritePreviewDocumentArtifactPaths(preview)
	if len(paths) == 0 {
		return ""
	}
	for _, path := range paths {
		if !preWritePathLooksLikeGeneratedDocumentArtifact(path) {
			return ""
		}
	}
	return contextRequest
}

func preWriteRequestLooksLikeGeneratedDocumentArtifact(request string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(request)))
	if lower == "" {
		return false
	}
	if looksLikeReviewArtifactAuthoringRequest(lower) {
		return true
	}
	return looksLikeDocumentAuthoringIntent(lower) && requestLooksLikeLocalCodeWork(lower)
}

func preWritePreviewDocumentArtifactPaths(preview EditPreview) []string {
	return preWritePreviewCurrentEditPaths(preview)
}

func preWritePreviewCurrentEditPaths(preview EditPreview) []string {
	if paths := preWritePreviewProposalPaths(preview.Proposals); len(paths) > 0 {
		return paths
	}
	if paths := preWritePreviewPathsFromText(preview.Preview); len(paths) > 0 {
		return paths
	}
	return normalizeEditProposalPathList(preview.Paths, 0)
}

func preWritePreviewProposalPaths(proposals []EditProposal) []string {
	proposals = normalizeEditProposals(proposals)
	pathSet := map[string]struct{}{}
	for _, proposal := range proposals {
		if strings.TrimSpace(proposal.File) != "" {
			pathSet[normalizeSessionRelativePath(proposal.File)] = struct{}{}
		}
		for _, file := range proposal.Files {
			if strings.TrimSpace(file) != "" {
				pathSet[normalizeSessionRelativePath(file)] = struct{}{}
			}
		}
	}
	var paths []string
	for path := range pathSet {
		paths = append(paths, path)
	}
	return uniqueStrings(paths)
}

func preWritePreviewPathsFromText(text string) []string {
	var paths []string
	appendPath := func(raw string) {
		path := preWritePreviewCleanPathToken(raw)
		if path != "" {
			paths = append(paths, path)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Preview for "):
			appendPath(strings.TrimSpace(strings.TrimPrefix(trimmed, "Preview for ")))
		case strings.HasPrefix(trimmed, "*** Add File: "):
			appendPath(strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Add File: ")))
		case strings.HasPrefix(trimmed, "*** Update File: "):
			appendPath(strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Update File: ")))
		case strings.HasPrefix(trimmed, "*** Delete File: "):
			appendPath(strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Delete File: ")))
		case strings.HasPrefix(trimmed, "diff --git "):
			fields := strings.Fields(trimmed)
			if len(fields) >= 4 {
				appendPath(fields[2])
				appendPath(fields[3])
			}
		case strings.HasPrefix(trimmed, "--- "):
			appendPath(strings.TrimSpace(strings.TrimPrefix(trimmed, "--- ")))
		case strings.HasPrefix(trimmed, "+++ "):
			appendPath(strings.TrimSpace(strings.TrimPrefix(trimmed, "+++ ")))
		}
	}
	return uniqueStrings(paths)
}

func preWritePreviewCleanPathToken(raw string) string {
	token := strings.TrimSpace(raw)
	token = strings.Trim(token, "\"'")
	if token == "" {
		return ""
	}
	if tab := strings.IndexByte(token, '\t'); tab >= 0 {
		token = strings.TrimSpace(token[:tab])
	}
	if preWritePreviewIsNullDiffPath(token) {
		return ""
	}
	for _, prefix := range []string{"a/", "b/", "before/", "after/"} {
		token = strings.TrimPrefix(token, prefix)
	}
	token = preWritePreviewStripLineRangeSuffix(token)
	if token == "" || preWritePreviewIsNullDiffPath(token) {
		return ""
	}
	return normalizeSessionRelativePath(token)
}

func preWritePreviewIsNullDiffPath(path string) bool {
	return strings.EqualFold(strings.TrimSpace(path), "/dev/null") ||
		strings.EqualFold(strings.TrimSpace(path), "NUL")
}

func preWritePreviewStripLineRangeSuffix(path string) string {
	idx := strings.LastIndex(path, ":")
	if idx < 0 || idx == len(path)-1 {
		return path
	}
	suffix := path[idx+1:]
	if !preWritePreviewLooksLikeLineRange(suffix) {
		return path
	}
	return path[:idx]
}

func preWritePreviewLooksLikeLineRange(suffix string) bool {
	if suffix == "" {
		return false
	}
	parts := strings.Split(suffix, "-")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

func preWritePathLooksLikeGeneratedDocumentArtifact(path string) bool {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return false
	}
	if isCodeLikePath(trimmed) {
		return false
	}
	return pathLooksLikeDocumentArtifact(trimmed)
}

func postChangeReviewShouldSkipGeneratedDocumentArtifact(request string, changedPaths []string) bool {
	return postChangeGeneratedDocumentArtifactRequest(nil, request, changedPaths) != ""
}

func sessionChangesAreGeneratedDocumentArtifacts(session *Session, request string) bool {
	return changedPathsAreGeneratedDocumentArtifacts(session, request, currentTurnPatchTransactionChangedPaths(session))
}

func changedPathsAreGeneratedDocumentArtifacts(session *Session, request string, changedPaths []string) bool {
	return postChangeGeneratedDocumentArtifactRequest(session, request, changedPaths) != ""
}

func postChangeGeneratedDocumentArtifactRequest(session *Session, request string, changedPaths []string) string {
	contextRequest := generatedDocumentArtifactRequestContextForTurn(session, request)
	if contextRequest == "" {
		return ""
	}
	paths := normalizeTaskStateList(changedPaths, 64)
	if len(paths) == 0 {
		return ""
	}
	for _, path := range paths {
		if !preWritePathLooksLikeGeneratedDocumentArtifact(path) {
			return ""
		}
	}
	return contextRequest
}

func generatedDocumentArtifactRequestContextForTurn(session *Session, request string) string {
	if contextRequest := generatedDocumentArtifactCurrentRequestContext(session, request); contextRequest != "" {
		return contextRequest
	}
	requestText := strings.TrimSpace(baseUserQueryText(request))
	if looksLikeInternalReviewFeedbackUserMessage(requestText) ||
		looksLikeFinalAnswerFollowupPrompt(requestText) {
		return generatedDocumentArtifactRequestContext(session, request)
	}
	if classifyTurnIntent(requestText) == TurnIntentContinueLastTask &&
		session != nil &&
		session.TaskState != nil &&
		!strings.EqualFold(strings.TrimSpace(session.TaskState.Phase), "done") {
		return generatedDocumentArtifactRequestContext(session, request)
	}
	return ""
}

func looksLikeFinalAnswerFollowupPrompt(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		lower = strings.ToLower(strings.TrimSpace(text))
	}
	if lower == "" {
		return false
	}
	if looksLikeExplicitEditIntent(lower) ||
		requestLooksLikeLocalVerificationWork(lower) ||
		looksLikeExecutionFlowQuestion(lower) {
		return false
	}
	return containsAny(lower,
		"final answer",
		"final summary",
		"provide the final",
		"give the final",
		"answer now",
		"conclude",
		"wrap up",
		"최종 답변",
		"최종답변",
		"최종 요약",
		"마무리",
		"결론",
	)
}

func generatedDocumentArtifactCurrentRequestContext(session *Session, request string) string {
	for _, text := range generatedDocumentArtifactCurrentRequestCandidates(session, request) {
		if preWriteRequestLooksLikeGeneratedDocumentArtifact(text) {
			return text
		}
	}
	return ""
}

func generatedDocumentArtifactCurrentRequestCandidates(session *Session, request string) []string {
	var candidates []string
	appendCandidate := func(text string) {
		text = strings.TrimSpace(baseUserQueryText(text))
		if text == "" || looksLikeInternalReviewFeedbackUserMessage(text) {
			return
		}
		candidates = append(candidates, text)
	}
	appendCandidate(request)
	if session == nil {
		return uniqueStrings(candidates)
	}
	if session.AcceptanceContract != nil {
		appendCandidate(session.AcceptanceContract.SourcePrompt)
	}
	if session.TaskState != nil {
		appendCandidate(session.TaskState.Goal)
	}
	if session.ActivePatchTransaction != nil {
		appendCandidate(session.ActivePatchTransaction.Goal)
	}
	return uniqueStrings(candidates)
}

func generatedDocumentArtifactRequestContext(session *Session, request string) string {
	for _, text := range generatedDocumentArtifactRequestCandidates(session, request) {
		if preWriteRequestLooksLikeGeneratedDocumentArtifact(text) {
			return text
		}
	}
	return ""
}

func generatedDocumentArtifactRequestCandidates(session *Session, request string) []string {
	var candidates []string
	appendCandidate := func(text string) {
		text = strings.TrimSpace(baseUserQueryText(text))
		if text == "" || looksLikeInternalReviewFeedbackUserMessage(text) {
			return
		}
		candidates = append(candidates, text)
	}
	appendCandidate(request)
	if session == nil {
		return uniqueStrings(candidates)
	}
	appendCandidate(preWriteReviewLastReviewRequest(session))
	if session.AcceptanceContract != nil {
		appendCandidate(session.AcceptanceContract.SourcePrompt)
	}
	if session.TaskState != nil {
		appendCandidate(session.TaskState.Goal)
	}
	if session.ActivePatchTransaction != nil {
		appendCandidate(session.ActivePatchTransaction.Goal)
	}
	for i, tx := range session.PatchTransactions {
		if i >= 4 {
			break
		}
		appendCandidate(tx.Goal)
	}
	for i := len(session.Messages) - 1; i >= 0 && len(candidates) < 12; i-- {
		msg := session.Messages[i]
		if !strings.EqualFold(msg.Role, "user") || messageIsInternalUserGuidance(msg) {
			continue
		}
		appendCandidate(msg.Text)
	}
	return uniqueStrings(candidates)
}

func preWriteReviewUserRequest(session *Session) string {
	if session == nil {
		return ""
	}
	lastReviewRequest := preWriteReviewLastReviewRequest(session)
	for i := len(session.Messages) - 1; i >= 0; i-- {
		msg := session.Messages[i]
		if !strings.EqualFold(msg.Role, "user") || messageIsInternalUserGuidance(msg) {
			continue
		}
		text := strings.TrimSpace(baseUserQueryText(msg.Text))
		if text == "" {
			continue
		}
		if shouldPreferLastReviewRequestForPreWrite(text, lastReviewRequest) {
			return lastReviewRequest
		}
		return text
	}
	if lastReviewRequest != "" {
		return lastReviewRequest
	}
	if session.AcceptanceContract != nil {
		text := strings.TrimSpace(baseUserQueryText(session.AcceptanceContract.SourcePrompt))
		if text != "" && !looksLikeInternalReviewFeedbackUserMessage(text) {
			return text
		}
	}
	if session.TaskState != nil {
		text := strings.TrimSpace(baseUserQueryText(session.TaskState.Goal))
		if text != "" && !looksLikeInternalReviewFeedbackUserMessage(text) {
			return text
		}
	}
	text := strings.TrimSpace(baseUserQueryText(latestExternalUserMessageText(session.Messages)))
	if text != "" && !looksLikeInternalReviewFeedbackUserMessage(text) {
		return text
	}
	return ""
}

func preWriteReviewLastReviewRequest(session *Session) string {
	if session == nil || session.LastReviewRun == nil {
		return ""
	}
	for _, text := range []string{
		session.LastReviewRun.RequestAnalysis.OriginalRequest,
		session.LastReviewRun.Objective,
	} {
		text = strings.TrimSpace(baseUserQueryText(text))
		if text != "" && !looksLikeInternalReviewFeedbackUserMessage(text) {
			return text
		}
	}
	return ""
}

func shouldPreferLastReviewRequestForPreWrite(candidate string, lastReviewRequest string) bool {
	if strings.TrimSpace(lastReviewRequest) == "" {
		return false
	}
	if strings.TrimSpace(candidate) == "" {
		return true
	}
	if preWriteReviewTextRequestsEnglish(candidate) {
		return false
	}
	return textContainsHangul(lastReviewRequest) && !textContainsHangul(candidate) && looksLikePreWriteInternalContextMessage(candidate)
}

func preWriteReviewTextRequestsEnglish(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	return containsAny(lower, "답변은 영어", "영어로 답", "영어로 설명", "english only", "in english", "respond in english", "answer in english")
}

func localizedTextForReviewRequest(cfg Config, request string, english string, korean string) string {
	language, _ := inferResponseLanguageForUserText(strings.TrimSpace(baseUserQueryText(request)), cfg)
	if language == "ko" {
		return korean
	}
	return english
}

func looksLikeInternalReviewFeedbackUserMessage(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n")))
	if lower == "" {
		return false
	}

	hasToolFailurePrefix := strings.HasPrefix(lower, "apply_patch failed:") ||
		strings.HasPrefix(lower, "apply_patch 실패:") ||
		strings.HasPrefix(lower, "tool apply_patch failed:") ||
		strings.HasPrefix(lower, "tool call failed:") ||
		strings.HasPrefix(lower, "도구 apply_patch 실패:") ||
		strings.HasPrefix(lower, "도구 호출 실패:") ||
		strings.HasPrefix(lower, "run_shell failed:") ||
		strings.HasPrefix(lower, "run_shell 실패:") ||
		strings.HasPrefix(lower, "read_file failed:") ||
		strings.HasPrefix(lower, "read_file 실패:") ||
		strings.HasPrefix(lower, "git_diff failed:") ||
		strings.HasPrefix(lower, "git_diff 실패:") ||
		strings.HasPrefix(lower, "git_status failed:") ||
		strings.HasPrefix(lower, "git_status 실패:")
	hasInternalReviewPrefix := strings.HasPrefix(lower, "review-first pass completed before making edits.") ||
		strings.HasPrefix(lower, "this is a follow-up repair request for the latest blocking review findings.") ||
		strings.HasPrefix(lower, "show the pre-fix review findings to the user before editing.") ||
		strings.HasPrefix(lower, "수정 전에 리뷰를 완료했습니다.") ||
		strings.HasPrefix(lower, "수정 전 리뷰 finding을 사용자에게 먼저 보여줘야 합니다.") ||
		strings.HasPrefix(lower, "직전 리뷰의 차단 finding을 수정하는 후속 요청입니다.")
	hasGoalContextPrefix := strings.HasPrefix(lower, "autonomous goal iteration ") ||
		strings.HasPrefix(lower, "autonomous goal repair pass ") ||
		strings.HasPrefix(lower, "autonomous final-goal repair pass ") ||
		strings.HasPrefix(lower, "final semantic goal review for autonomous goal ") ||
		strings.HasPrefix(lower, "continue working toward the active thread goal.") ||
		strings.HasPrefix(lower, "<goal_context>")

	return strings.HasPrefix(lower, "automatic pre-write review ") ||
		strings.HasPrefix(lower, "automatic post-change review ") ||
		strings.HasPrefix(lower, "automatic verification ") ||
		strings.HasPrefix(lower, "reviewer feedback:") ||
		strings.HasPrefix(lower, "final review result:") ||
		strings.HasPrefix(lower, "repair targets checked:") ||
		strings.HasPrefix(lower, "remaining review items:") ||
		strings.HasPrefix(lower, "자동 쓰기 전 리뷰") ||
		strings.HasPrefix(lower, "자동 변경 후 리뷰") ||
		strings.HasPrefix(lower, "자동 검증") ||
		strings.HasPrefix(lower, "리뷰어 피드백:") ||
		strings.HasPrefix(lower, "도구 경로 업데이트 후 자동 검증") ||
		hasGoalContextPrefix ||
		hasInternalReviewPrefix ||
		hasToolFailurePrefix ||
		looksLikePreWriteInternalContextMessage(text) ||
		looksLikeMainOnlyReviewFallbackApproval(text)
}

func looksLikePreWriteInternalContextMessage(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		return false
	}
	return strings.HasPrefix(lower, "you are repeating the same tool call sequence") ||
		strings.HasPrefix(lower, "you have already made multiple rounds of edits") ||
		strings.HasPrefix(lower, "you have already completed multiple edit rounds") ||
		strings.HasPrefix(lower, "your last assistant message was commentary/progress") ||
		strings.HasPrefix(lower, "this request explicitly asks you to inspect and fix the code") ||
		strings.HasPrefix(lower, "your last answer appears to have been cut off") ||
		strings.HasPrefix(lower, "your last response was a raw internal review_result block") ||
		strings.HasPrefix(lower, "verification is still unresolved") ||
		strings.HasPrefix(lower, "please provide the final answer to the user now. do not return an empty message") ||
		strings.HasPrefix(lower, "your last edit targeted stale or mismatched file contents") ||
		strings.HasPrefix(lower, "the last read-only inspection tool was blocked by editable ownership routing") ||
		strings.HasPrefix(lower, "your latest read_file result") ||
		strings.HasPrefix(lower, "the same tool failure repeated") ||
		strings.HasPrefix(lower, "recovery mode:") ||
		strings.HasPrefix(lower, "pre-final coding harness found issues") ||
		strings.HasPrefix(lower, "generated document artifact finalization is answer-only now") ||
		strings.HasPrefix(lower, "runtime gate ledger blocked") ||
		strings.HasPrefix(lower, "automatic verification has been disabled") ||
		strings.HasPrefix(lower, "this request likely needs current external research") ||
		strings.HasPrefix(lower, "pre-write 리뷰가 이미 수정안을 차단했고") ||
		strings.HasPrefix(lower, "the pre-write review already blocked the edit") ||
		strings.HasPrefix(lower, "next step requirements:") ||
		strings.HasPrefix(lower, "use the extra turns to finish the investigation or fix") ||
		strings.HasPrefix(lower, "the normal tool budget has been exhausted") ||
		strings.HasPrefix(lower, "blocked web research tool call during local code review/repair") ||
		strings.HasPrefix(lower, "this is a local code review or repair request") ||
		strings.HasPrefix(lower, "this is a generated document artifact turn") ||
		strings.HasPrefix(lower, "this is local code review/repair work") ||
		strings.HasPrefix(lower, "this is still local code review/repair work") ||
		strings.HasPrefix(lower, "recovered transcript note:") ||
		strings.HasPrefix(lower, "your last reply was empty") ||
		strings.HasPrefix(lower, "do not repeat the same tool call; continue from local context")
}

func preWriteMainOnlyReviewerFallbackApproved(session *Session) bool {
	if session == nil || session.LastReviewRun == nil {
		return false
	}
	last := *session.LastReviewRun
	if !strings.EqualFold(strings.TrimSpace(last.Trigger), "pre_write") {
		return false
	}
	if !reviewRunHasRequiredReviewerFailure(last) {
		return false
	}
	if !reviewRunHasUsableMainReviewer(last) {
		return false
	}
	return looksLikeMainOnlyReviewFallbackApproval(latestUserMessageText(session.Messages))
}

func looksLikeMainOnlyReviewFallbackApproval(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		return false
	}
	koreanApproval := strings.Contains(lower, "메인") &&
		strings.Contains(lower, "리뷰") &&
		strings.Contains(lower, "기준") &&
		containsAny(lower, "진행", "수정", "적용")
	englishApproval := strings.Contains(lower, "main") &&
		strings.Contains(lower, "review") &&
		containsAny(lower, "proceed", "continue", "apply")
	return koreanApproval || englishApproval
}

func (a *Agent) emitPreWriteFinalVisibleReviewSummary(run ReviewRun, proceedToPreview bool) {
	if a == nil {
		return
	}
	summary := formatPreWriteFinalVisibleReviewSummary(a.Config, run, proceedToPreview)
	if strings.TrimSpace(summary) == "" {
		return
	}
	a.emitPersistentAssistantSummary(summary)
}

func preWriteRepairObligationsFromLastReview(session *Session) []ReviewFinding {
	if session == nil || session.LastReviewRun == nil {
		return nil
	}
	last := *session.LastReviewRun
	if strings.EqualFold(strings.TrimSpace(last.Trigger), "pre_write") && len(last.RepairFindings) > 0 {
		return preWriteCarriedRepairObligations(last.RepairFindings)
	}
	return preFixRepairObligationFindings(last)
}

func reviewRunHasUsableMainReviewer(run ReviewRun) bool {
	for _, reviewerRun := range run.ReviewerRuns {
		if !strings.EqualFold(strings.TrimSpace(reviewerRun.Kind), "main") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(reviewerRun.Status), "completed") {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityUsable) ||
			strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityStrong) {
			return true
		}
	}
	return false
}

func preFixRepairObligationFindings(run ReviewRun) []ReviewFinding {
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		return nil
	}
	blockingIDs := reviewFindingIDSet(run.Gate.BlockingFindings)
	warningIDs := reviewFindingIDSet(run.Gate.WarningFindings)
	out := make([]ReviewFinding, 0, len(run.Findings))
	for _, finding := range run.Findings {
		finding.Normalize()
		if strings.TrimSpace(finding.ID) == "" {
			continue
		}
		if blockingIDs[finding.ID] && preWritePreFixFindingIsConcreteRepairObligation(finding) {
			out = append(out, finding)
			continue
		}
		if warningIDs[finding.ID] && preWritePreFixWarningIsConcreteRepairObligation(finding) {
			out = append(out, finding)
			continue
		}
		if reviewFindingBlocksGate(run, finding) {
			out = append(out, finding)
			continue
		}
		if !preWritePreFixWarningShouldBeRepairObligation(finding) {
			continue
		}
		out = append(out, finding)
	}
	return out
}

func normalizeReviewFindingCopies(findings []ReviewFinding) []ReviewFinding {
	out := make([]ReviewFinding, 0, len(findings))
	for _, finding := range findings {
		finding.Normalize()
		if strings.TrimSpace(firstNonBlankString(finding.ID, finding.Title, finding.Evidence, finding.Impact, finding.RequiredFix, finding.TestRecommendation)) == "" {
			continue
		}
		out = append(out, finding)
	}
	return out
}

func preWriteCarriedRepairObligations(findings []ReviewFinding) []ReviewFinding {
	out := normalizeReviewFindingCopies(findings)
	for i := range out {
		out[i].ResolutionStatus = ""
	}
	return out
}

func formatPreWriteCarriedRepairObligationsFeedback(run ReviewRun, korean bool) string {
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") || len(run.RepairFindings) == 0 {
		return ""
	}
	findings := preWriteCarriedRepairObligations(run.RepairFindings)
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	if korean {
		b.WriteString("계속 유효한 pre-fix 필수 RF:\n")
		b.WriteString("- 이전 edit proposal은 파일에 적용되지 않았습니다. 다음 수정안은 최신 pre-write blocker만 덧붙이는 delta가 아니라, 아래 필수 RF 전체를 현재 파일 기준 standalone patch로 다시 만족해야 합니다.\n")
		b.WriteString("- 최신 pre-write finding은 이전 proposal이 왜 불완전했는지를 보여주는 추가 근거입니다. 원래 RF 목록에서 조용히 빠지는 항목이 있으면 안 됩니다.\n")
		b.WriteString(renderReviewInlineFindingsLocalized(ReviewRun{Findings: findings}, true, true))
	} else {
		b.WriteString("Still-active required pre-fix RFs:\n")
		b.WriteString("- The previous edit proposal was not applied to disk. The next proposal must not be a delta that only adds the latest pre-write blocker; it must again satisfy every required RF below as a standalone patch against the current file.\n")
		b.WriteString("- Treat the latest pre-write finding as extra evidence of why the previous proposal was incomplete. Do not silently drop any original RF obligation.\n")
		b.WriteString(renderReviewInlineFindings(ReviewRun{Findings: findings}, true))
	}
	return strings.TrimSpace(b.String())
}

func preWritePreFixWarningShouldBeRepairObligation(finding ReviewFinding) bool {
	return reviewFindingShouldBeRepairPlanWarning(finding)
}

func preWritePreFixFindingIsConcreteRepairObligation(finding ReviewFinding) bool {
	finding.Normalize()
	if strings.EqualFold(finding.Category, "test_gap") ||
		strings.EqualFold(finding.Category, "evidence_gap") ||
		reviewFindingLooksNonActionablePlaceholder(finding) {
		return false
	}
	return strings.TrimSpace(finding.RequiredFix) != "" ||
		strings.TrimSpace(finding.Path) != "" ||
		strings.TrimSpace(finding.Symbol) != "" ||
		strings.TrimSpace(finding.Title) != ""
}

func preWritePreFixWarningIsConcreteRepairObligation(finding ReviewFinding) bool {
	finding.Normalize()
	if reviewSeverityRank(finding.Severity) != reviewSeverityRank(reviewSeverityMedium) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(finding.Category)) {
	case "correctness", "stability", "performance", "security", "operational_risk":
	default:
		return false
	}
	return reviewFindingShouldBeRepairPlanWarning(finding) &&
		preWritePreFixFindingIsConcreteRepairObligation(finding)
}

func (a *Agent) runAutomaticPostChangeReviewGate(ctx context.Context, request string, lastFingerprint *string, revisionCount *int, exhaustedNudge *bool) (bool, error) {
	if a == nil || a.Session == nil || lastFingerprint == nil || revisionCount == nil || exhaustedNudge == nil {
		return false, nil
	}
	reviewed, needsRevision, reviewFeedback, fingerprint, err := a.maybeRunPostChangeReview(ctx, request, *lastFingerprint)
	if err != nil {
		if a.EmitProgress != nil {
			a.EmitProgress(localizedTextForReviewRequest(a.Config, request, "Automatic post-change review failed: ", "자동 변경 후 리뷰 실패: ") + err.Error())
		}
		return false, nil
	}
	if !reviewed {
		return false, nil
	}
	*lastFingerprint = fingerprint
	if isGeneratedDocumentArtifactQualityFingerprint(fingerprint) && !needsRevision {
		return false, nil
	}
	if a.EmitProgress != nil {
		if needsRevision {
			a.EmitProgress(localizedTextForReviewRequest(a.Config, request, "Automatic post-change review found blockers. Asking the model to revise...", "자동 변경 후 리뷰에서 차단 항목을 발견했습니다. 모델에 수정안을 다시 요청합니다..."))
		} else {
			a.EmitProgress(localizedTextForReviewRequest(a.Config, request, "Automatic post-change review completed.", "자동 변경 후 리뷰가 완료되었습니다."))
		}
	}
	if needsRevision && *revisionCount < configReviewHarness(a.Config).AutoRepairMaxRounds {
		*revisionCount++
		a.Session.AddMessage(internalUserMessage(reviewFeedback))
		if a.Store != nil {
			if err := a.Store.Save(a.Session); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	if needsRevision && !*exhaustedNudge {
		*exhaustedNudge = true
		exhaustedInstruction := localizedTextForReviewRequest(a.Config, request,
			"Automatic post-change review still has blockers, but the automatic repair limit is exhausted. Do not claim completion. Provide the final answer as blocked or incomplete, cite the review gate, and list the exact remaining actions.",
			"자동 변경 후 리뷰에 아직 차단 항목이 있지만 자동 수정 한도에 도달했습니다. 완료됐다고 주장하지 말고, 최종 답변에서 차단 또는 미완료 상태를 명시하고 리뷰 게이트와 남은 조치를 정확히 나열하세요.")
		a.Session.AddMessage(internalUserMessage(reviewFeedback + "\n\n" + exhaustedInstruction))
		if a.Store != nil {
			if err := a.Store.Save(a.Session); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	return false, nil
}

func (a *Agent) validateGeneratedDocumentArtifactForPostChangeSkip(request string, changedPaths []string) (bool, string) {
	if a == nil || a.Session == nil {
		return false, ""
	}
	if requestLooksLikeLocalVerificationWork(strings.ToLower(strings.TrimSpace(baseUserQueryText(request)))) {
		return false, ""
	}
	reply := a.generatedDocumentArtifactSeedFinalReply()
	report := a.buildCodingHarnessReport(reply, true, false)
	reconcileGeneratedDocumentArtifactPostChangeScope(&report, changedPaths)
	a.Session.LastCodingHarnessReport = &report
	a.Session.LastTestImpactReport = &report.TestImpact
	a.Session.LastJobSupervisorReport = &report.JobSupervisor
	copyReport := report
	copyReport.Normalize()
	if codingHarnessFindingsHaveBlockers(copyReport.allFindings()) {
		return true, copyReport.BlockingFeedback()
	}
	return false, ""
}

func reconcileGeneratedDocumentArtifactPostChangeScope(report *CodingHarnessReport, changedPaths []string) {
	if report == nil {
		return
	}
	normalized := normalizeTaskStateList(changedPaths, 64)
	if !changedPathsLookLikeGeneratedReportArtifacts(normalized) {
		return
	}
	report.DiffReview.ChangedPaths = normalized
	report.DiffReview.Findings = filterCodingHarnessFindingsByTitle(report.DiffReview.Findings, "Workspace mutation has unknown review scope")
	report.TestImpact.ChangedPaths = normalized
	report.TestImpact.CodeLikeChangedPaths = nil
	report.TestImpact.Confidence = "not_applicable"
	report.TestImpact.Notes = normalizeTaskStateList([]string{"Only generated document artifact paths changed in the post-change evidence."}, 8)
	report.TestImpact.Gaps = nil
	report.Normalize()
}

func filterCodingHarnessFindingsByTitle(findings []CodingHarnessFinding, title string) []CodingHarnessFinding {
	if len(findings) == 0 {
		return nil
	}
	filtered := make([]CodingHarnessFinding, 0, len(findings))
	for _, finding := range findings {
		if strings.EqualFold(strings.TrimSpace(finding.Title), strings.TrimSpace(title)) {
			continue
		}
		filtered = append(filtered, finding)
	}
	return normalizeCodingHarnessFindings(filtered)
}

func (a *Agent) reviewHarnessRuntime(root string) *runtimeState {
	return &runtimeState{
		cfg:           a.Config,
		store:         a.Store,
		session:       a.Session,
		agent:         a,
		memory:        a.Memory,
		longMem:       a.LongMem,
		evidence:      a.Evidence,
		verifyHistory: a.VerifyHistory,
		functionFuzz:  a.FunctionFuzz,
		fuzzCampaigns: a.FuzzCampaigns,
		modelRoutes:   a.ModelRoutes,
		mcp:           a.MCP,
		workspace: Workspace{
			BaseRoot:              root,
			Root:                  firstNonBlankString(a.Workspace.Root, root),
			Shell:                 a.Workspace.Shell,
			ShellTimeout:          a.Workspace.ShellTimeout,
			ReadHintSpans:         a.Workspace.ReadHintSpans,
			ReadCacheEntries:      a.Workspace.ReadCacheEntries,
			VerificationToolPaths: a.Workspace.VerificationToolPaths,
			ToolHints:             a.Workspace.ToolHints,
			Perms:                 a.Workspace.Perms,
			PrepareEdit:           a.Workspace.PrepareEdit,
			PrepareEditAtRoot:     a.Workspace.PrepareEditAtRoot,
			ReviewEdit:            a.Workspace.ReviewEdit,
			ReportProgress:        a.Workspace.ReportProgress,
			CurrentSelection:      a.Workspace.CurrentSelection,
			PreviewEdit:           a.Workspace.PreviewEdit,
			UpdatePlan:            a.Workspace.UpdatePlan,
			GetPlan:               a.Workspace.GetPlan,
			RunHook:               a.Workspace.RunHook,
			BackgroundJobs:        a.Workspace.BackgroundJobs,
			ResolveEditTarget:     a.Workspace.ResolveEditTarget,
			ResolveShellRoot:      a.Workspace.ResolveShellRoot,
		},
	}
}

func (a *Agent) shouldSkipPostChangeReviewForKnownFinalBlocker(reply string, unresolvedVerification bool) bool {
	if a == nil || a.Session == nil {
		return false
	}
	mentionsVerificationBlocker := replyMentionsVerificationBlocker(reply) || replyMentionsVerificationNotRun(reply)
	if !mentionsVerificationBlocker {
		return false
	}
	if a.Session.LastVerification != nil {
		report := *a.Session.LastVerification
		if report.WasSkipped() {
			return false
		}
		if report.HasFailures() {
			return true
		}
	}
	if unresolvedVerification && a.Session.LastVerification == nil {
		return true
	}
	if a.Session.AcceptanceContract != nil {
		contract := *a.Session.AcceptanceContract
		contract.Normalize()
		if contract.VerificationRequired && !sessionHasSuccessfulVerificationEvidence(a.Session) {
			return true
		}
	}
	return false
}

func postChangeReviewHasDedicatedModel(a *Agent) bool {
	if a == nil {
		return false
	}
	if a.ReviewerClient != nil && strings.TrimSpace(a.ReviewerModel) != "" {
		return true
	}
	if a.AuxReviewerClient != nil && strings.TrimSpace(a.AuxReviewerModel) != "" {
		return true
	}
	reviewCfg := configReviewHarness(a.Config)
	for _, role := range []string{"primary_reviewer", "design_reviewer", "security_reviewer", "false_positive_reviewer", "regression_reviewer", "test_reviewer", "final_gate_reviewer"} {
		if roleCfg, ok := reviewCfg.RoleModels[role]; ok && strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
			return true
		}
	}
	return false
}

func reviewHarnessHasConfiguredModelRoute(a *Agent) bool {
	if a == nil {
		return false
	}
	if a.Client != nil && reviewMainModelRouteConfigured(a.Config) {
		return true
	}
	if a.ReviewerClient != nil && strings.TrimSpace(a.ReviewerModel) != "" {
		return true
	}
	if a.AuxReviewerClient != nil && strings.TrimSpace(a.AuxReviewerModel) != "" {
		return true
	}
	reviewCfg := configReviewHarness(a.Config)
	for _, roleCfg := range reviewCfg.RoleModels {
		if strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
			return true
		}
	}
	return false
}

func reviewHarnessHasPreFixModelRoute(a *Agent) bool {
	if reviewHarnessHasConfiguredModelRoute(a) {
		return true
	}
	if a == nil {
		return false
	}
	if a.ReviewerClient != nil && strings.TrimSpace(a.ReviewerModel) != "" {
		return true
	}
	if a.AuxReviewerClient != nil && strings.TrimSpace(a.AuxReviewerModel) != "" {
		return true
	}
	return false
}

func reviewHarnessCanUseAnyModel(a *Agent) bool {
	if a == nil {
		return false
	}
	if a.Client != nil && reviewMainModelRouteConfigured(a.Config) {
		return true
	}
	return postChangeReviewHasDedicatedModel(a)
}

func autoReviewChangedPaths(session *Session, root string) []string {
	paths := currentTurnPatchTransactionChangedPaths(session)
	if len(paths) > 0 {
		return normalizeTaskStateList(paths, 128)
	}
	return normalizeTaskStateList(filterReviewablePaths(delegationChangedFiles(root)), 128)
}

func formatPostChangeReviewFeedback(cfg Config, run ReviewRun, needsRevision bool) string {
	korean := reviewRunPrefersKorean(cfg, run)
	var b strings.Builder
	if needsRevision {
		if korean {
			b.WriteString("자동 변경 후 리뷰가 차단 항목을 발견했습니다. 최종 답변 전에 수정하세요.")
		} else {
			b.WriteString("Automatic post-change review found blockers. Fix them before final answer.")
		}
	} else {
		if korean {
			b.WriteString("자동 변경 후 리뷰가 완료되었습니다.")
		} else {
			b.WriteString("Automatic post-change review completed.")
		}
	}
	fmt.Fprintf(&b, "\n\n%s: %s", reviewRunLocalizedText(cfg, run, "Review gate", "검토 게이트"), valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
	if strings.TrimSpace(run.MachineStatus) != "" {
		fmt.Fprintf(&b, " (%s)", run.MachineStatus)
	}
	if strings.TrimSpace(run.Result.Summary) != "" {
		fmt.Fprintf(&b, "\n%s: %s", reviewRunLocalizedText(cfg, run, "Summary", "요약"), run.Result.Summary)
	}
	if needsRevision {
		if strings.TrimSpace(run.RepairPlan.Prompt) != "" {
			b.WriteString("\n\n")
			b.WriteString(run.RepairPlan.Prompt)
		} else {
			if korean {
				b.WriteString("\n\n인라인 리뷰 finding:\n")
				b.WriteString(renderReviewInlineFindingsLocalized(run, true, true))
			} else {
				b.WriteString("\n\nInline review findings:\n")
				b.WriteString(renderReviewInlineFindings(run, true))
			}
		}
		if korean {
			b.WriteString("\n\n구현 규칙:\n")
			b.WriteString("- 리뷰 artifact 파일을 다시 읽지 마세요. 필요한 리뷰 지침은 여기 모두 포함되어 있습니다.\n")
			b.WriteString("- 리뷰 게이트를 만족하는 데 필요한 변경 코드만 수정하세요.\n")
			b.WriteString("- finding이 요구하면 집중 검증을 실행하세요.")
		} else {
			b.WriteString("\n\nImplementation rules:\n")
			b.WriteString("- Do not read review artifact files; all required review guidance is included here.\n")
			b.WriteString("- Revise only the changed code needed to satisfy the review gate.\n")
			b.WriteString("- Run focused verification when the finding asks for it.")
		}
		return strings.TrimSpace(b.String())
	}
	if len(run.Gate.WarningFindings) > 0 {
		fmt.Fprintf(&b, "\n%s: %d", reviewRunLocalizedText(cfg, run, "Warnings", "경고"), len(run.Gate.WarningFindings))
	}
	if len(run.Gate.NextCommands) > 0 {
		next := run.Gate.NextCommands[0]
		if strings.TrimSpace(next.Command) != "" {
			fmt.Fprintf(&b, "\n%s: %s", reviewRunLocalizedText(cfg, run, "Next", "다음"), next.Command)
			if strings.TrimSpace(next.Reason) != "" {
				fmt.Fprintf(&b, " (%s)", next.Reason)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func formatReviewerGateUnavailableToolError(cfg Config, run ReviewRun) string {
	korean := reviewRunPrefersKorean(cfg, run)
	failed := reviewFailedRequiredReviewerRuns(run)
	if len(failed) == 0 {
		if korean {
			return "필수 리뷰 단계의 모델 route가 실패했거나 약한 결과를 반환했습니다. 편집을 멈추고 실패한 route 문제를 보고하세요"
		}
		return "required review route failed or returned weak output; stop editing and report the failed route issue"
	}
	var details []string
	for _, reviewerRun := range failed {
		role := firstNonBlankString(reviewRoleProgressName(reviewerRun.Role), "reviewer")
		status := valueOrDefault(strings.TrimSpace(reviewerRun.Status), "unknown")
		quality := valueOrDefault(strings.TrimSpace(reviewerRun.ModelQuality), "unknown")
		detail := firstNonBlankString(firstNonEmptyLine(reviewerRun.Error), "reviewer output was too weak")
		details = append(details, fmt.Sprintf("%s status=%s quality=%s: %s", role, status, quality, detail))
	}
	hint := ""
	if reviewRunHasReviewerTimeoutFailure(&run) {
		if korean {
			hint = " 다음 reviewer call은 timeout을 자동으로 한 단계 늘립니다; `/review models`, `/review models clear primary`, `/model` 중 하나로도 복구할 수 있습니다."
		} else {
			hint = " The next reviewer call will automatically extend the timeout once; recovery options are `/review models`, `/review models clear primary`, or `/model`."
		}
	}
	if korean {
		return "필수 리뷰 단계의 모델 route가 실패했거나 약한 결과를 반환했습니다. 편집을 멈추고 실패한 route 문제를 보고하세요: " + strings.Join(details, " | ") + hint
	}
	return "required review route failed or returned weak output; stop editing and report the failed route issue: " + strings.Join(details, " | ") + hint
}

func formatPreWriteReviewFeedback(cfg Config, run ReviewRun) string {
	korean := reviewRunPrefersKorean(cfg, run)
	var b strings.Builder
	if korean {
		b.WriteString("자동 쓰기 전 리뷰가 차단 항목을 발견했습니다. 파일을 쓰기 전에 수정안을 다시 작성하세요.")
		fmt.Fprintf(&b, "\n\n검토 게이트: %s", valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
		if strings.TrimSpace(run.MachineStatus) != "" {
			fmt.Fprintf(&b, " (%s)", run.MachineStatus)
		}
		if strings.TrimSpace(run.Result.Summary) != "" {
			fmt.Fprintf(&b, "\n요약: %s", run.Result.Summary)
		}
		if carried := formatPreWriteCarriedRepairObligationsFeedback(run, true); carried != "" {
			b.WriteString("\n\n")
			b.WriteString(carried)
		}
		if strings.TrimSpace(run.RepairPlan.Prompt) != "" {
			b.WriteString("\n\n")
			b.WriteString(run.RepairPlan.Prompt)
		} else {
			b.WriteString("\n\n인라인 리뷰 finding:\n")
			b.WriteString(renderReviewInlineFindingsLocalized(run, true, true))
		}
		b.WriteString("\n\n구현 규칙:\n")
		b.WriteString("- 리뷰 artifact 파일을 다시 읽지 마세요. 필요한 리뷰 지침은 여기 모두 포함되어 있습니다.\n")
		b.WriteString("- 같은 patch를 반복하지 말고 수정된 edit proposal을 반환하세요.")
		b.WriteString("\n")
		b.WriteString(preWriteBlockedProposalStandaloneGuidance(true))
		b.WriteString("\n")
		b.WriteString(reviewPatchRelevanceGuidance(true))
		b.WriteString("\n")
		b.WriteString(reviewNarrowPatchGuidance(true))
		b.WriteString("\n- pre-write review를 우회하기 위해 run_shell, PowerShell 파일 API, redirection, 직접 파일 쓰기를 사용하지 마세요. 수정안이 다시 리뷰되도록 edit tool을 사용하세요.")
		b.WriteString("\n")
		b.WriteString(reviewDedicatedInspectionToolGuidance(true))
		b.WriteString("\n- 이 작업은 로컬 코드 리뷰/수리입니다. MCP web/search/browser 도구나 외부 웹 리서치를 사용하지 말고, 로컬 소스 근거와 위 finding만 사용하세요.")
		return strings.TrimSpace(b.String())
	}
	b.WriteString("Automatic pre-write review found blockers. Revise the proposed edit before writing files.")
	fmt.Fprintf(&b, "\n\nReview gate: %s", valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
	if strings.TrimSpace(run.MachineStatus) != "" {
		fmt.Fprintf(&b, " (%s)", run.MachineStatus)
	}
	if strings.TrimSpace(run.Result.Summary) != "" {
		fmt.Fprintf(&b, "\nSummary: %s", run.Result.Summary)
	}
	if carried := formatPreWriteCarriedRepairObligationsFeedback(run, false); carried != "" {
		b.WriteString("\n\n")
		b.WriteString(carried)
	}
	if strings.TrimSpace(run.RepairPlan.Prompt) != "" {
		b.WriteString("\n\n")
		b.WriteString(run.RepairPlan.Prompt)
	} else {
		b.WriteString("\n\nInline review findings:\n")
		b.WriteString(renderReviewInlineFindings(run, true))
	}
	b.WriteString("\n\nImplementation rules:\n")
	b.WriteString("- Do not read review artifact files; all required review guidance is included here.\n")
	b.WriteString("- Return a corrected edit proposal instead of retrying the same patch.")
	b.WriteString("\n")
	b.WriteString(preWriteBlockedProposalStandaloneGuidance(false))
	b.WriteString("\n")
	b.WriteString(reviewPatchRelevanceGuidance(false))
	b.WriteString("\n")
	b.WriteString(reviewNarrowPatchGuidance(false))
	b.WriteString("\n- Do not use run_shell, PowerShell file APIs, redirection, or direct filesystem writes to bypass pre-write review; use edit tools so the corrected proposal is reviewed.")
	b.WriteString("\n")
	b.WriteString(reviewDedicatedInspectionToolGuidance(false))
	b.WriteString("\n- This is local code review/repair work. Do not use MCP web/search/browser tools or external web research to satisfy this gate; use local source evidence and the inline findings above.")
	return strings.TrimSpace(b.String())
}

func preWriteBlockedProposalStandaloneGuidance(korean bool) string {
	if korean {
		return strings.Join([]string{
			"- 차단된 이전 patch는 파일에 적용되지 않았습니다. 다음 edit proposal은 이전 patch에 덧붙이는 delta가 아니라, 현재 파일에 바로 적용 가능한 완전한 standalone patch여야 합니다.",
			"- 이전 patch가 대부분 맞고 작은 보강만 빠졌더라도 보강 delta만 보내지 마세요. 필요한 hunk를 모두 포함한 standalone patch로 다시 제출하세요.",
		}, "\n")
	}
	return strings.Join([]string{
		"- The previously blocked patch was not applied to disk. The next edit proposal must be a complete standalone patch for the current file, not a delta on top of the blocked patch.",
		"- If the previous patch was mostly correct but only missed a small reinforcement, do not send only the reinforcement delta. Resubmit a standalone patch containing every required hunk.",
	}, "\n")
}

func formatPreWriteReviewWarningBlockFeedback(cfg Config, run ReviewRun, warnings []ReviewFinding) string {
	korean := reviewRunPrefersKorean(cfg, run)
	var b strings.Builder
	if korean {
		b.WriteString("자동 쓰기 전 리뷰가 수정 필요한 경고를 발견했습니다. 파일을 쓰기 전에 수정안을 다시 작성하세요.")
		fmt.Fprintf(&b, "\n\n검토 게이트: %s", valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
		if strings.TrimSpace(run.MachineStatus) != "" {
			fmt.Fprintf(&b, " (%s)", run.MachineStatus)
		}
		if strings.TrimSpace(run.Result.Summary) != "" {
			fmt.Fprintf(&b, "\n요약: %s", run.Result.Summary)
		}
		if carried := formatPreWriteCarriedRepairObligationsFeedback(run, true); carried != "" {
			b.WriteString("\n\n")
			b.WriteString(carried)
		}
		b.WriteString("\n\n수정 필요한 경고 finding:\n")
		b.WriteString(renderReviewInlineFindingsLocalized(ReviewRun{Findings: warnings}, true, true))
		b.WriteString("\n\n구현 규칙:\n")
		b.WriteString("- 이 pre-write 경고를 필수 수정 지침으로 취급하세요.\n")
		b.WriteString("- 요청된 코드 표면과 구현 근거가 모두 보이도록 수정안을 다시 작성하세요.\n")
		b.WriteString("- 이전의 불완전한 patch를 쓰지 마세요.\n")
		b.WriteString(preWriteBlockedProposalStandaloneGuidance(true))
		b.WriteString("\n")
		b.WriteString(reviewPatchRelevanceGuidance(true))
		b.WriteString("\n")
		b.WriteString(reviewNarrowPatchGuidance(true))
		b.WriteString("\n")
		b.WriteString("- pre-write review를 우회하기 위해 run_shell, PowerShell 파일 API, redirection, 직접 파일 쓰기를 사용하지 말고 edit tool을 사용하세요.\n")
		b.WriteString(reviewDedicatedInspectionToolGuidance(true))
		b.WriteString("\n")
		b.WriteString("- 이 작업은 로컬 코드 리뷰/수리입니다. MCP web/search/browser 도구나 외부 웹 리서치를 사용하지 말고, 로컬 소스 근거와 위 경고만 사용하세요.\n")
		return strings.TrimSpace(b.String())
	}
	b.WriteString("Automatic pre-write review found actionable warnings. Revise the proposed edit before writing files.")
	fmt.Fprintf(&b, "\n\nReview gate: %s", valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
	if strings.TrimSpace(run.MachineStatus) != "" {
		fmt.Fprintf(&b, " (%s)", run.MachineStatus)
	}
	if strings.TrimSpace(run.Result.Summary) != "" {
		fmt.Fprintf(&b, "\nSummary: %s", run.Result.Summary)
	}
	if carried := formatPreWriteCarriedRepairObligationsFeedback(run, false); carried != "" {
		b.WriteString("\n\n")
		b.WriteString(carried)
	}
	b.WriteString("\n\nActionable warning findings:\n")
	b.WriteString(renderReviewInlineFindings(ReviewRun{Findings: warnings}, true))
	b.WriteString("\n\nImplementation rules:\n")
	b.WriteString("- Treat these pre-write warnings as required repair guidance.\n")
	b.WriteString("- Revise the proposed edit so the requested code surface and implementation evidence are both present.\n")
	b.WriteString("- Do not write the previous incomplete patch.\n")
	b.WriteString("\n")
	b.WriteString(preWriteBlockedProposalStandaloneGuidance(false))
	b.WriteString("\n")
	b.WriteString(reviewPatchRelevanceGuidance(false))
	b.WriteString("\n")
	b.WriteString(reviewNarrowPatchGuidance(false))
	b.WriteString("\n")
	b.WriteString("- Do not use run_shell, PowerShell file APIs, redirection, or direct filesystem writes to bypass pre-write review; use edit tools so the corrected proposal is reviewed.\n")
	b.WriteString(reviewDedicatedInspectionToolGuidance(false))
	b.WriteString("\n")
	b.WriteString("- This is local code review/repair work. Do not use MCP web/search/browser tools or external web research to satisfy this gate; use local source evidence and the actionable warnings above.\n")
	return strings.TrimSpace(b.String())
}

func preWriteReviewBlockingWarningFindings(run ReviewRun) []ReviewFinding {
	warningIDs := reviewFindingIDSet(run.Gate.WarningFindings)
	if len(warningIDs) == 0 {
		return nil
	}
	var out []ReviewFinding
	for _, finding := range run.Findings {
		if !warningIDs[finding.ID] {
			continue
		}
		if preWriteReviewWarningShouldBlock(finding) {
			out = append(out, finding)
		}
	}
	return out
}

func preWriteReviewWarningShouldBlock(finding ReviewFinding) bool {
	finding.Normalize()
	if !strings.EqualFold(strings.TrimSpace(finding.Source), "model") {
		return false
	}
	if reviewPreWriteWarningLooksLikeStyleGap(finding) {
		return true
	}
	if reviewPreWriteWarningLooksLikePureVerificationGap(finding) {
		return false
	}
	if strings.EqualFold(finding.Category, "test_gap") {
		return false
	}
	if strings.EqualFold(finding.Category, "evidence_gap") {
		if reviewPreWriteWarningLooksLikeHarnessEvidenceGap(finding) {
			return false
		}
		return true
	}
	if reviewSeverityRank(finding.Severity) > reviewSeverityRank(reviewSeverityMedium) {
		return reviewPreWriteWarningLooksLikeActionableCodeGap(finding)
	}
	return true
}

func reviewPreWriteWarningLooksLikeHarnessEvidenceGap(finding ReviewFinding) bool {
	actionableText := strings.ToLower(strings.Join([]string{
		finding.Title,
		finding.Evidence,
		finding.Impact,
		finding.RequiredFix,
	}, " "))
	if actionableText == "" && strings.TrimSpace(finding.TestRecommendation) == "" {
		return false
	}
	if containsAny(actionableText,
		"api surface",
		"accessor",
		"getter",
		"member declaration",
		"member declarations",
		"missing declaration",
		"missing implementation",
		"requested api",
		"requested scope",
		"does not implement",
		"build",
		"compile",
		"header",
		"#include",
		"missing include",
		"storage",
		"구현 증거",
		"구현이",
		"구현되지",
		"빌드",
		"컴파일",
		"헤더",
		"멤버 선언",
		"선언",
		"초기값",
		"조회 기능",
		"요청 범위",
		"충족하지",
	) {
		return false
	}
	harnessText := strings.ToLower(strings.Join([]string{
		finding.Title,
		finding.Evidence,
		finding.Impact,
		finding.RequiredFix,
		finding.TestRecommendation,
	}, " "))
	return containsAny(harnessText,
		"complete modified function body is not visible",
		"complete function body is not visible",
		"function body is not visible",
		"provided after-preview",
		"selection-focused preview",
		"after-preview",
		"does not show the remaining",
		"does not show the rest",
		"provide the complete current contents",
		"complete current contents",
		"remaining braces",
		"remaining cleanup",
		"success calculation",
		"m_volumepathmap",
		"loop continuation",
		"cleanup path",
		"close handle",
		"function 후반부",
		"함수 후반부",
		"변경 결과를 확인할 증거",
		"확인할 증거가 부족",
		"제공된 selection",
	)
}

func reviewPreWriteWarningLooksLikeActionableCodeGap(finding ReviewFinding) bool {
	if strings.TrimSpace(finding.RequiredFix) == "" {
		return false
	}
	category := strings.ToLower(strings.TrimSpace(finding.Category))
	switch category {
	case "correctness", "security", "stability", "performance", "maintainability", "false_positive", "bypass_surface", "operational_risk", "design":
		return strings.TrimSpace(finding.Path) != "" ||
			strings.TrimSpace(finding.Symbol) != "" ||
			strings.TrimSpace(finding.Evidence) != "" ||
			strings.TrimSpace(finding.Impact) != ""
	default:
		return false
	}
}

func reviewPreWriteWarningLooksLikeStyleGap(finding ReviewFinding) bool {
	text := strings.ToLower(strings.Join([]string{
		finding.Title,
		finding.Evidence,
		finding.Impact,
		finding.RequiredFix,
		finding.TestRecommendation,
	}, " "))
	if text == "" {
		return false
	}
	return containsAny(text,
		"allman",
		"brace style",
		"braces style",
		"formatting",
		"indentation",
		"opening brace",
		"style violation",
		"여는 중괄호",
		"중괄호",
		"들여쓰기",
		"스타일",
		"포매팅",
	)
}

func reviewPreWriteWarningLooksLikePureVerificationGap(finding ReviewFinding) bool {
	text := strings.ToLower(strings.Join([]string{
		finding.Title,
		finding.Evidence,
		finding.Impact,
		finding.RequiredFix,
		finding.TestRecommendation,
	}, " "))
	if containsAny(text,
		"api surface",
		"accessor",
		"getter",
		"member declaration",
		"member declarations",
		"missing declaration",
		"missing implementation",
		"requested api",
		"requested scope",
		"does not implement",
		"구현 증거",
		"구현이",
		"구현되지",
		"멤버 선언",
		"선언",
		"초기값",
		"조회 기능",
		"요청 범위",
		"충족하지",
	) {
		return false
	}
	return containsAny(text,
		"verification was not run",
		"verification skipped",
		"verification was skipped",
		"verification omitted",
		"build verification",
		"build was not run",
		"build was skipped",
		"no latest verification",
		"no build verification",
		"no verification evidence",
		"no test was run",
		"not verified",
		"run verification",
		"tests were not run",
		"/verify",
		"빌드 검증",
		"검증 생략",
		"검증이 생략",
		"검증을 생략",
		"테스트 실행",
		"테스트가 생략",
	)
}

func formatPreWriteReviewWarningProgress(cfg Config, run ReviewRun) string {
	warnings := reviewCLIWarningFindings(run)
	korean := reviewRunPrefersKorean(cfg, run)
	if len(warnings) == 0 {
		if korean {
			return fmt.Sprintf("자동 쓰기 전 리뷰가 경고와 함께 완료되었습니다. 경고 %d개.", len(run.Gate.WarningFindings))
		}
		return fmt.Sprintf("Automatic pre-write review completed with warnings. warnings=%d.", len(run.Gate.WarningFindings))
	}
	var titles []string
	for _, finding := range limitReviewFindings(warnings, 3) {
		title := strings.TrimSpace(finding.Title)
		if title == "" {
			title = strings.TrimSpace(finding.Evidence)
		}
		if title != "" {
			titles = append(titles, fmt.Sprintf("%s %s: %s", valueOrDefault(finding.ID, "RF"), finding.Severity, compactPromptSection(title, 140)))
		}
	}
	suffix := strings.Join(titles, " | ")
	if len(warnings) > len(titles) {
		if suffix != "" {
			suffix += " | "
		}
		if korean {
			suffix += fmt.Sprintf("외 %d개", len(warnings)-len(titles))
		} else {
			suffix += fmt.Sprintf("%d more", len(warnings)-len(titles))
		}
	}
	if len(run.ArtifactRefs) > 0 {
		if suffix != "" {
			suffix += " | "
		}
		if korean {
			suffix += "보고서: " + run.ArtifactRefs[0]
		} else {
			suffix += "report: " + run.ArtifactRefs[0]
		}
	}
	if korean {
		return strings.TrimSpace(fmt.Sprintf("자동 쓰기 전 리뷰가 경고와 함께 완료되었습니다. 경고 %d개. %s", len(run.Gate.WarningFindings), suffix))
	}
	return strings.TrimSpace(fmt.Sprintf("Automatic pre-write review completed with warnings. warnings=%d. %s", len(run.Gate.WarningFindings), suffix))
}

func formatPreWriteFinalReviewProgress(cfg Config, run ReviewRun, proceedToPreview bool) string {
	korean := reviewRunPrefersKorean(cfg, run)
	verdict := firstNonBlankString(run.Gate.Verdict, run.Result.Verdict, "unknown")
	blockerCount := len(run.Gate.BlockingFindings)
	warningCount := len(run.Gate.WarningFindings)
	content := preWriteReviewFinalContentProgress(cfg, run)
	report := preWriteReviewReportProgressSuffix(cfg, run)
	if korean {
		action := "diff preview로 진행하지 않습니다."
		if proceedToPreview {
			action = "diff preview로 진행합니다."
			if preWriteWarningsAreHarnessEvidenceOnly(run) {
				action += " 남은 경고는 코드 미해결 blocker가 아니라 리뷰 evidence 확인 부족입니다."
			}
		}
		return strings.TrimSpace(fmt.Sprintf("자동 쓰기 전 리뷰가 완료되었습니다. 최종 검토 결과: %s (차단=%d, 경고=%d). %s %s%s", verdict, blockerCount, warningCount, content, action, report))
	}
	action := "Not proceeding to diff preview."
	if proceedToPreview {
		action = "Proceeding to diff preview."
		if preWriteWarningsAreHarnessEvidenceOnly(run) {
			action += " Remaining warnings are review-evidence visibility gaps, not confirmed unresolved code blockers."
		}
	}
	return strings.TrimSpace(fmt.Sprintf("Automatic pre-write review completed. Final review result: %s (blockers=%d, warnings=%d). %s %s%s", verdict, blockerCount, warningCount, content, action, report))
}

func preWriteReviewFinalContentProgress(cfg Config, run ReviewRun) string {
	korean := reviewRunPrefersKorean(cfg, run)
	var parts []string
	summary := strings.TrimSpace(run.Result.Summary)
	if summary != "" {
		if korean {
			parts = append(parts, "요약: "+compactReviewVisibleInlineText(summary, 180))
		} else {
			parts = append(parts, "summary: "+compactReviewVisibleInlineText(summary, 180))
		}
	}
	findings := preWriteReviewProgressFindings(run)
	titles := reviewProgressFindingTitles(findings, 3)
	if len(titles) > 0 {
		if korean {
			parts = append(parts, "주요 finding: "+strings.Join(titles, " | "))
		} else {
			parts = append(parts, "key findings: "+strings.Join(titles, " | "))
		}
	} else if summary == "" {
		if korean {
			parts = append(parts, "주요 finding: 없음")
		} else {
			parts = append(parts, "key findings: none")
		}
	}
	if len(parts) == 0 {
		if korean {
			return "검토 내용: 주요 finding 없음."
		}
		return "Review content: no key findings."
	}
	if korean {
		return "검토 내용: " + strings.Join(parts, " | ") + "."
	}
	return "Review content: " + strings.Join(parts, " | ") + "."
}

func preWriteReviewProgressFindings(run ReviewRun) []ReviewFinding {
	ids := append(append([]string(nil), run.Gate.BlockingFindings...), run.Gate.WarningFindings...)
	if len(ids) > 0 {
		return reviewProgressFindingsByID(run, ids, 3)
	}
	if info := reviewCLIInfoFindings(run); len(info) > 0 {
		return limitReviewFindings(info, 3)
	}
	return limitReviewFindings(run.Findings, 3)
}

func preWriteReviewReportProgressSuffix(cfg Config, run ReviewRun) string {
	if len(run.ArtifactRefs) == 0 {
		return ""
	}
	if reviewRunPrefersKorean(cfg, run) {
		return " 보고서: " + run.ArtifactRefs[0]
	}
	return " report: " + run.ArtifactRefs[0]
}

func formatPreWriteFinalVisibleReviewSummary(cfg Config, run ReviewRun, proceedToPreview bool) string {
	korean := reviewRunPrefersKorean(cfg, run)
	verdict := firstNonBlankString(run.Gate.Verdict, run.Result.Verdict, "unknown")
	var b strings.Builder
	if korean {
		b.WriteString("최종 검토 결과:")
		fmt.Fprintf(&b, "\n- 판정: %s", verdict)
		fmt.Fprintf(&b, "\n- 차단: %d개", len(run.Gate.BlockingFindings))
		fmt.Fprintf(&b, "\n- 경고: %d개", len(run.Gate.WarningFindings))
		if proceedToPreview {
			b.WriteString("\n- 진행: diff preview로 진행합니다.")
		} else {
			b.WriteString("\n- 진행: diff preview로 진행하지 않습니다.")
		}
		if proceedToPreview && preWriteWarningsAreHarnessEvidenceOnly(run) {
			b.WriteString("\n- 참고: 남은 경고는 코드 미해결 blocker가 아니라 리뷰 evidence/after-preview 확인 부족 경고입니다.")
		}
		if strings.TrimSpace(run.Result.Summary) != "" {
			fmt.Fprintf(&b, "\n- 요약: %s", reviewVisibleInlineText(run.Result.Summary))
		}
	} else {
		b.WriteString("Final review result:")
		fmt.Fprintf(&b, "\n- Verdict: %s", verdict)
		fmt.Fprintf(&b, "\n- Blockers: %d", len(run.Gate.BlockingFindings))
		fmt.Fprintf(&b, "\n- Warnings: %d", len(run.Gate.WarningFindings))
		if proceedToPreview {
			b.WriteString("\n- Next: proceed to diff preview.")
		} else {
			b.WriteString("\n- Next: do not proceed to diff preview.")
		}
		if proceedToPreview && preWriteWarningsAreHarnessEvidenceOnly(run) {
			b.WriteString("\n- Note: remaining warnings are review-evidence or after-preview visibility gaps, not confirmed unresolved code blockers.")
		}
		if strings.TrimSpace(run.Result.Summary) != "" {
			fmt.Fprintf(&b, "\n- Summary: %s", reviewVisibleInlineText(run.Result.Summary))
		}
	}
	if len(run.RepairFindings) > 0 {
		if korean {
			b.WriteString("\n\n수정 확인 대상:")
		} else {
			b.WriteString("\n\nRepair targets checked:")
		}
		unresolvedRepairIDs := preWriteUnresolvedRepairIDs(run)
		for _, finding := range limitReviewFindings(run.RepairFindings, 8) {
			if unresolvedRepairIDs[strings.TrimSpace(finding.ID)] {
				finding.ResolutionStatus = "unresolved"
			}
			writePreWriteVisibleRepairTarget(&b, finding, korean)
		}
	}
	if korean {
		if len(run.RepairFindings) > 0 {
			b.WriteString("\n\n남은 검토 항목:")
		} else {
			b.WriteString("\n\n검토 항목:")
		}
	} else {
		if len(run.RepairFindings) > 0 {
			b.WriteString("\n\nRemaining review items:")
		} else {
			b.WriteString("\n\nReview items:")
		}
	}
	findings := preWriteReviewProgressFindings(run)
	if len(findings) == 0 {
		if korean {
			b.WriteString("\n- 주요 finding 없음.")
		} else {
			b.WriteString("\n- No key findings.")
		}
	} else {
		for _, finding := range limitReviewFindings(findings, 6) {
			writePreWriteVisibleFinding(&b, finding, korean)
		}
	}
	if len(run.ArtifactRefs) > 0 {
		if korean {
			fmt.Fprintf(&b, "\n\n리뷰 보고서: %s", run.ArtifactRefs[0])
		} else {
			fmt.Fprintf(&b, "\n\nReview report: %s", run.ArtifactRefs[0])
		}
	}
	return strings.TrimSpace(b.String())
}

func preWriteUnresolvedRepairIDs(run ReviewRun) map[string]bool {
	out := map[string]bool{}
	for _, finding := range run.Findings {
		finding.Normalize()
		if !strings.Contains(strings.ToLower(finding.Title), "required repair unresolved") {
			continue
		}
		if !reviewFindingBlocksGate(run, finding) && !strings.EqualFold(strings.TrimSpace(finding.Severity), reviewSeverityBlocker) {
			continue
		}
		for _, ref := range finding.FixRefs {
			ref = strings.TrimSpace(ref)
			if ref != "" {
				out[ref] = true
			}
		}
	}
	return out
}

func preWriteWarningsAreHarnessEvidenceOnly(run ReviewRun) bool {
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") ||
		len(run.Gate.BlockingFindings) > 0 ||
		len(run.Gate.WarningFindings) == 0 {
		return false
	}
	warnings := reviewProgressFindingsByID(run, run.Gate.WarningFindings, len(run.Gate.WarningFindings))
	if len(warnings) == 0 {
		return false
	}
	for _, finding := range warnings {
		finding.Normalize()
		if !strings.EqualFold(finding.Category, "evidence_gap") || !reviewPreWriteWarningLooksLikeHarnessEvidenceGap(finding) {
			return false
		}
	}
	return true
}

func writePreWriteVisibleRepairTarget(b *strings.Builder, finding ReviewFinding, korean bool) {
	finding.Normalize()
	id := valueOrDefault(finding.ID, "RF")
	severity := valueOrDefault(finding.Severity, "unknown")
	category := valueOrDefault(finding.Category, "general")
	title := reviewVisibleInlineText(firstNonBlankString(finding.Title, finding.Evidence, finding.Impact, "Review finding"))
	fmt.Fprintf(b, "\n- %s [%s/%s]: %s", id, severity, category, title)
	if strings.TrimSpace(finding.Path) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 코드 위치: %s", reviewVisibleInlineText(finding.Path))
		} else {
			fmt.Fprintf(b, "\n  - Code location: %s", reviewVisibleInlineText(finding.Path))
		}
	}
	if strings.TrimSpace(finding.Symbol) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 심볼: %s", reviewVisibleInlineText(finding.Symbol))
		} else {
			fmt.Fprintf(b, "\n  - Symbol: %s", reviewVisibleInlineText(finding.Symbol))
		}
	}
	if strings.TrimSpace(finding.Evidence) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 문제: %s", reviewVisibleInlineText(finding.Evidence))
		} else {
			fmt.Fprintf(b, "\n  - Problem: %s", reviewVisibleInlineText(finding.Evidence))
		}
	}
	if strings.TrimSpace(finding.Impact) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 영향: %s", reviewVisibleInlineText(finding.Impact))
		} else {
			fmt.Fprintf(b, "\n  - Impact: %s", reviewVisibleInlineText(finding.Impact))
		}
	}
	if strings.TrimSpace(finding.RequiredFix) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 수정 기준: %s", reviewVisibleInlineText(finding.RequiredFix))
		} else {
			fmt.Fprintf(b, "\n  - Required fix: %s", reviewVisibleInlineText(finding.RequiredFix))
		}
	}
	if strings.TrimSpace(finding.ResolutionStatus) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 해결 상태: %s", reviewRepairResolutionStatusVisibleText(finding.ResolutionStatus, true))
		} else {
			fmt.Fprintf(b, "\n  - Resolution status: %s", reviewRepairResolutionStatusVisibleText(finding.ResolutionStatus, false))
		}
	}
	if strings.TrimSpace(finding.TestRecommendation) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 확인 방법: %s", reviewVisibleInlineText(finding.TestRecommendation))
		} else {
			fmt.Fprintf(b, "\n  - Verification: %s", reviewVisibleInlineText(finding.TestRecommendation))
		}
	}
}

func reviewRepairResolutionStatusVisibleText(status string, korean bool) string {
	status = normalizeReviewRepairResolutionStatus(status)
	switch status {
	case "evidence_unconfirmed":
		if korean {
			return "evidence_unconfirmed (리뷰 evidence 부족으로 확인 불가, 코드 미해결로 확정된 것은 아님)"
		}
		return "evidence_unconfirmed (review evidence was insufficient; not a confirmed unresolved code blocker)"
	default:
		return reviewVisibleInlineText(status)
	}
}

func writePreWriteVisibleFinding(b *strings.Builder, finding ReviewFinding, korean bool) {
	finding.Normalize()
	id := valueOrDefault(finding.ID, "RF")
	severity := valueOrDefault(finding.Severity, "unknown")
	category := valueOrDefault(finding.Category, "general")
	title := reviewVisibleInlineText(firstNonBlankString(finding.Title, finding.Evidence, finding.Impact, "Review finding"))
	fmt.Fprintf(b, "\n- %s [%s/%s]: %s", id, severity, category, title)
	if strings.TrimSpace(finding.Path) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 경로: %s", reviewVisibleInlineText(finding.Path))
		} else {
			fmt.Fprintf(b, "\n  - Path: %s", reviewVisibleInlineText(finding.Path))
		}
	}
	if strings.TrimSpace(finding.Evidence) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 근거: %s", reviewVisibleInlineText(finding.Evidence))
		} else {
			fmt.Fprintf(b, "\n  - Evidence: %s", reviewVisibleInlineText(finding.Evidence))
		}
	}
	if strings.TrimSpace(finding.Impact) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 영향: %s", reviewVisibleInlineText(finding.Impact))
		} else {
			fmt.Fprintf(b, "\n  - Impact: %s", reviewVisibleInlineText(finding.Impact))
		}
	}
	if strings.TrimSpace(finding.RequiredFix) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 조치: %s", reviewVisibleInlineText(finding.RequiredFix))
		} else {
			fmt.Fprintf(b, "\n  - Fix: %s", reviewVisibleInlineText(finding.RequiredFix))
		}
	}
	if strings.TrimSpace(finding.TestRecommendation) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 테스트: %s", reviewVisibleInlineText(finding.TestRecommendation))
		} else {
			fmt.Fprintf(b, "\n  - Test: %s", reviewVisibleInlineText(finding.TestRecommendation))
		}
	}
}

func reviewVisibleInlineText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func compactReviewVisibleInlineText(text string, limit int) string {
	text = reviewVisibleInlineText(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	end := 0
	for idx, r := range text {
		size := len(string(r))
		if idx+size > limit {
			break
		}
		end = idx + size
	}
	if end == 0 {
		return ""
	}
	return strings.TrimSpace(text[:end])
}

func limitReviewFindings(findings []ReviewFinding, limit int) []ReviewFinding {
	if limit <= 0 || len(findings) <= limit {
		return findings
	}
	return findings[:limit]
}
