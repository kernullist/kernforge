package main

import (
	"context"
	"fmt"
	"strings"
)

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
	if len(autoReviewChangedPaths(a.Session, root)) == 0 {
		return false, false, "", "", nil
	}
	if a.Session.LastReviewRun != nil &&
		a.Session.LastReviewRun.AutoTriggered &&
		strings.EqualFold(a.Session.LastReviewRun.Trigger, "post_change") &&
		a.Session.LastReviewRun.ReviewFingerprint != "" &&
		a.Session.LastReviewRun.ReviewFingerprint == strings.TrimSpace(lastFingerprint) &&
		postChangeReviewRunStillMatchesSessionEvidence(a.Session.LastReviewRun, a.Session) {
		return false, false, "", lastFingerprint, nil
	}
	if a.EmitProgress != nil {
		a.EmitProgress(localizedText(a.Config, "Running automatic post-change review...", "자동 변경 후 리뷰를 실행합니다..."))
	}
	rt := a.reviewHarnessRuntime(root)
	run, err := runReviewHarness(ctx, rt, ReviewHarnessOptions{
		Trigger:         "post_change",
		Target:          reviewTargetChange,
		Request:         request,
		IncludeGitDiff:  true,
		NoModel:         !reviewHarnessCanUseAnyModel(a),
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
	feedback := formatPostChangeReviewFeedback(run, needsRevision)
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
	request := preWriteReviewUserRequest(a.Session)
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

func preWriteReviewUserRequest(session *Session) string {
	if session == nil {
		return ""
	}
	lastReviewRequest := preWriteReviewLastReviewRequest(session)
	for i := len(session.Messages) - 1; i >= 0; i-- {
		msg := session.Messages[i]
		if !strings.EqualFold(msg.Role, "user") {
			continue
		}
		text := strings.TrimSpace(baseUserQueryText(msg.Text))
		if text == "" || looksLikeInternalReviewFeedbackUserMessage(text) {
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
	text := strings.TrimSpace(baseUserQueryText(latestUserMessageText(session.Messages)))
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
		strings.HasPrefix(lower, "수정 전에 리뷰를 완료했습니다.") ||
		strings.HasPrefix(lower, "직전 리뷰의 차단 finding을 수정하는 후속 요청입니다.")

	return strings.HasPrefix(lower, "automatic pre-write review ") ||
		strings.HasPrefix(lower, "automatic post-change review ") ||
		strings.HasPrefix(lower, "automatic verification ") ||
		strings.HasPrefix(lower, "final review result:") ||
		strings.HasPrefix(lower, "repair targets checked:") ||
		strings.HasPrefix(lower, "remaining review items:") ||
		strings.HasPrefix(lower, "자동 쓰기 전 리뷰") ||
		strings.HasPrefix(lower, "자동 변경 후 리뷰") ||
		strings.HasPrefix(lower, "자동 검증") ||
		strings.HasPrefix(lower, "도구 경로 업데이트 후 자동 검증") ||
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
		strings.HasPrefix(lower, "your last edit targeted stale or mismatched file contents") ||
		strings.HasPrefix(lower, "your latest read_file result") ||
		strings.HasPrefix(lower, "the same tool failure repeated") ||
		strings.HasPrefix(lower, "recovery mode:") ||
		strings.HasPrefix(lower, "next step requirements:") ||
		strings.HasPrefix(lower, "use the extra turns to finish the investigation or fix") ||
		strings.HasPrefix(lower, "the normal tool budget has been exhausted") ||
		strings.HasPrefix(lower, "blocked web research tool call during local code review/repair") ||
		strings.HasPrefix(lower, "this is a local code review or repair request") ||
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
	out := make([]ReviewFinding, 0, len(run.Findings))
	for _, finding := range run.Findings {
		finding.Normalize()
		if strings.TrimSpace(finding.ID) == "" {
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

func preWritePreFixWarningShouldBeRepairObligation(finding ReviewFinding) bool {
	return reviewFindingShouldBeRepairPlanWarning(finding)
}

func (a *Agent) runAutomaticPostChangeReviewGate(ctx context.Context, request string, lastFingerprint *string, revisionCount *int, exhaustedNudge *bool) (bool, error) {
	if a == nil || a.Session == nil || lastFingerprint == nil || revisionCount == nil || exhaustedNudge == nil {
		return false, nil
	}
	reviewed, needsRevision, reviewFeedback, fingerprint, err := a.maybeRunPostChangeReview(ctx, request, *lastFingerprint)
	if err != nil {
		if a.EmitProgress != nil {
			a.EmitProgress("Automatic post-change review failed: " + err.Error())
		}
		return false, nil
	}
	if !reviewed {
		return false, nil
	}
	*lastFingerprint = fingerprint
	if a.EmitProgress != nil {
		if needsRevision {
			a.EmitProgress("Automatic post-change review found blockers. Asking the model to revise...")
		} else {
			a.EmitProgress("Automatic post-change review completed.")
		}
	}
	if needsRevision && *revisionCount < configReviewHarness(a.Config).AutoRepairMaxRounds {
		*revisionCount++
		a.Session.AddMessage(Message{
			Role: "user",
			Text: reviewFeedback,
		})
		if a.Store != nil {
			if err := a.Store.Save(a.Session); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	if needsRevision && !*exhaustedNudge {
		*exhaustedNudge = true
		a.Session.AddMessage(Message{
			Role: "user",
			Text: reviewFeedback + "\n\nAutomatic post-change review still has blockers, but the automatic repair limit is exhausted. Do not claim completion. Provide the final answer as blocked or incomplete, cite the review gate, and list the exact remaining actions.",
		})
		if a.Store != nil {
			if err := a.Store.Save(a.Session); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	return false, nil
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
	if unresolvedVerification || (a.Session.LastVerification != nil && a.Session.LastVerification.HasFailures()) {
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

func reviewHarnessCanUseAnyModel(a *Agent) bool {
	if a == nil {
		return false
	}
	if a.Client != nil && strings.TrimSpace(a.Config.Model) != "" {
		return true
	}
	return postChangeReviewHasDedicatedModel(a)
}

func autoReviewChangedPaths(session *Session, root string) []string {
	var paths []string
	paths = append(paths, sessionPatchTransactionChangedPaths(session)...)
	paths = append(paths, filterReviewablePaths(delegationChangedFiles(root))...)
	return normalizeTaskStateList(paths, 128)
}

func formatPostChangeReviewFeedback(run ReviewRun, needsRevision bool) string {
	var b strings.Builder
	if needsRevision {
		b.WriteString("Automatic post-change review found blockers. Fix them before final answer.")
	} else {
		b.WriteString("Automatic post-change review completed.")
	}
	fmt.Fprintf(&b, "\n\nReview gate: %s", valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
	if strings.TrimSpace(run.MachineStatus) != "" {
		fmt.Fprintf(&b, " (%s)", run.MachineStatus)
	}
	if strings.TrimSpace(run.Result.Summary) != "" {
		fmt.Fprintf(&b, "\nSummary: %s", run.Result.Summary)
	}
	if needsRevision {
		if strings.TrimSpace(run.RepairPlan.Prompt) != "" {
			b.WriteString("\n\n")
			b.WriteString(run.RepairPlan.Prompt)
		} else {
			b.WriteString("\n\nInline review findings:\n")
			b.WriteString(renderReviewInlineFindings(run, true))
		}
		b.WriteString("\n\nImplementation rules:\n")
		b.WriteString("- Do not read review artifact files; all required review guidance is included here.\n")
		b.WriteString("- Revise only the changed code needed to satisfy the review gate.\n")
		b.WriteString("- Run focused verification when the finding asks for it.")
		return strings.TrimSpace(b.String())
	}
	if len(run.Gate.WarningFindings) > 0 {
		fmt.Fprintf(&b, "\nWarnings: %d", len(run.Gate.WarningFindings))
	}
	if len(run.Gate.NextCommands) > 0 {
		next := run.Gate.NextCommands[0]
		if strings.TrimSpace(next.Command) != "" {
			fmt.Fprintf(&b, "\nNext: %s", next.Command)
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
	if korean {
		return "필수 리뷰 단계의 모델 route가 실패했거나 약한 결과를 반환했습니다. 편집을 멈추고 실패한 route 문제를 보고하세요: " + strings.Join(details, " | ")
	}
	return "required review route failed or returned weak output; stop editing and report the failed route issue: " + strings.Join(details, " | ")
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
	b.WriteString(reviewPatchRelevanceGuidance(false))
	b.WriteString("\n")
	b.WriteString(reviewNarrowPatchGuidance(false))
	b.WriteString("\n- Do not use run_shell, PowerShell file APIs, redirection, or direct filesystem writes to bypass pre-write review; use edit tools so the corrected proposal is reviewed.")
	b.WriteString("\n")
	b.WriteString(reviewDedicatedInspectionToolGuidance(false))
	b.WriteString("\n- This is local code review/repair work. Do not use MCP web/search/browser tools or external web research to satisfy this gate; use local source evidence and the inline findings above.")
	return strings.TrimSpace(b.String())
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
		b.WriteString("\n\n수정 필요한 경고 finding:\n")
		b.WriteString(renderReviewInlineFindingsLocalized(ReviewRun{Findings: warnings}, true, true))
		b.WriteString("\n\n구현 규칙:\n")
		b.WriteString("- 이 pre-write 경고를 필수 수정 지침으로 취급하세요.\n")
		b.WriteString("- 요청된 API surface와 구현 근거가 모두 보이도록 수정안을 다시 작성하세요.\n")
		b.WriteString("- 이전의 불완전한 patch를 쓰지 마세요.\n")
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
	b.WriteString("\n\nActionable warning findings:\n")
	b.WriteString(renderReviewInlineFindings(ReviewRun{Findings: warnings}, true))
	b.WriteString("\n\nImplementation rules:\n")
	b.WriteString("- Treat these pre-write warnings as required repair guidance.\n")
	b.WriteString("- Revise the proposed edit so the requested API surface and implementation evidence are both present.\n")
	b.WriteString("- Do not write the previous incomplete patch.\n")
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
		"findnextvolume",
		"findvolumeclose",
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
		}
		return strings.TrimSpace(fmt.Sprintf("자동 쓰기 전 리뷰가 완료되었습니다. 최종 검토 결과: %s (차단=%d, 경고=%d). %s %s%s", verdict, blockerCount, warningCount, content, action, report))
	}
	action := "Not proceeding to diff preview."
	if proceedToPreview {
		action = "Proceeding to diff preview."
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
		for _, finding := range limitReviewFindings(run.RepairFindings, 8) {
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
			fmt.Fprintf(b, "\n  - 해결 상태: %s", reviewVisibleInlineText(finding.ResolutionStatus))
		} else {
			fmt.Fprintf(b, "\n  - Resolution status: %s", reviewVisibleInlineText(finding.ResolutionStatus))
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
