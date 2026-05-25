package main

import (
	"context"
	"fmt"
	"strings"
)

const reviewBeforeFixTrigger = "pre_fix"

func reviewNarrowPatchGuidance(korean bool) string {
	if korean {
		return strings.Join([]string{
			"- apply_patch payload는 좁은 hunk만 포함하세요. 파일 전체 rewrite, 큰 함수 전체 교체, 여러 RF를 한 번에 합친 대형 patch를 만들지 마세요.",
			"- 일반 edit 흐름에서 한 patch가 커질 것 같으면 첫 번째 독립 hunk만 적용하고, 성공 후 파일을 다시 읽어 다음 hunk를 진행하세요. 단, pre-write gate가 필수 RF 전체 해결을 요구한다고 명시한 경우에는 RF별 좁은 hunk를 모두 포함하세요.",
			"- 각 hunk는 현재 파일 내용에서 방금 확인한 짧은 old snippet에 고정하세요.",
		}, "\n")
	}
	return strings.Join([]string{
		"- Keep apply_patch payloads narrow: do not rewrite whole files, replace large whole functions, or combine unrelated RF fixes into one large patch.",
		"- In ordinary edit flow, if the patch would be large, apply only the first independent hunk, then reread the file and continue with the next hunk after the edit succeeds. If the pre-write gate explicitly requires all RFs to be addressed, include all required RF hunks, but keep each hunk narrow.",
		"- Anchor each hunk on a short old snippet that was just verified in the current file contents.",
	}, "\n")
}

func reviewPatchRelevanceGuidance(korean bool) string {
	if korean {
		return strings.Join([]string{
			"- 수정안은 최신 차단 finding의 근거/required_fix와 추적 가능하게 연결되어야 합니다.",
			"- 하네스는 소스 의미를 대신 판정하지 않습니다. 관련 없는 보강만 제출하지 말고, 리뷰 모델이 판단할 수 있도록 실제 diff와 after 상태가 충분히 보이게 하세요.",
			"- 마지막 수정안이 최신 blocker와 무관하면 재사용하지 말고, 현재 파일 본문을 다시 확인한 뒤 좁은 hunk로 다시 작성하세요.",
		}, "\n")
	}
	return strings.Join([]string{
		"- The proposed edit must be traceably connected to the latest blocking finding evidence or required_fix.",
		"- The harness does not judge source semantics. Do not submit unrelated reinforcement only; make the actual diff and after-state visible enough for the reviewer model to judge.",
		"- If the last proposal is unrelated to the latest blocker, discard it, reread the current file body, and produce a narrow hunk.",
	}, "\n")
}

func reviewDedicatedInspectionToolGuidance(korean bool) string {
	if korean {
		return "- 파일 내용, diff, git 상태 확인은 read_file, grep, git_diff, git_status 같은 전용 workspace 도구를 사용하세요. 줄 번호나 파일 일부 출력을 위해 run_shell, Get-Content, PowerShell 파이프를 호출하지 마세요."
	}
	return "- Use dedicated workspace tools such as read_file, grep, git_diff, and git_status for file, diff, and git-state inspection. Do not call run_shell, Get-Content, or PowerShell pipelines just to print line numbers or file excerpts."
}

func (a *Agent) maybeRunReviewBeforeFix(ctx context.Context, userText string, images []MessageImage, readOnlyAnalysis bool, explicitEditRequest bool) (bool, error) {
	if a == nil || a.Session == nil || readOnlyAnalysis || !explicitEditRequest || len(images) > 0 {
		return false, nil
	}
	if !looksLikeReviewBeforeFixIntent(userText) {
		return false, nil
	}
	if !reviewHarnessHasPreFixModelRoute(a) {
		return false, nil
	}
	root := workspaceSnapshotRoot(a.Workspace)
	if strings.TrimSpace(root) == "" {
		root = a.Workspace.Root
	}
	if strings.TrimSpace(root) == "" {
		root = a.Session.WorkingDir
	}
	if strings.TrimSpace(root) == "" {
		return false, nil
	}
	rt := a.reviewHarnessRuntime(root)
	opts, selection, ok := rt.reviewBeforeFixOptions(userText, images)
	if !ok {
		return false, nil
	}
	if selection != nil {
		rt.rememberNaturalReviewSelection(*selection)
	}
	if a.EmitProgress != nil {
		a.EmitProgress(localizedTextForReviewRequest(a.Config, userText, "Running review before fix...", "수정 전 리뷰를 실행합니다..."))
	}
	run, err := runReviewHarness(ctx, rt, opts)
	if err != nil {
		if a.EmitProgress != nil {
			a.EmitProgress(localizedTextForReviewRequest(a.Config, userText, "Review before fix failed: ", "수정 전 리뷰 실패: ") + err.Error())
		}
		return true, fmt.Errorf("review before fix failed: %w", err)
	}
	if summary := formatPreFixVisibleReviewSummary(a.Config, run); summary != "" {
		a.emitPersistentAssistantSummary(summary)
		a.Session.AddMessage(Message{
			Role:  "assistant",
			Phase: messagePhaseCommentary,
			Text:  summary,
		})
	}
	allowIndependentInspection := preFixReviewCanContinueWithIndependentInspection(run)
	if reviewRunHasRequiredReviewerFailure(run) && !allowIndependentInspection {
		if a.Store != nil {
			if err := a.Store.Save(a.Session); err != nil {
				return true, err
			}
		}
		if a.EmitProgress != nil {
			a.EmitProgress(formatReviewBeforeFixProgress(a.Config, run))
		}
		return true, nil
	}
	if preFixReviewHasUnreliableNoActionableFinding(run) && !allowIndependentInspection {
		if a.Store != nil {
			if err := a.Store.Save(a.Session); err != nil {
				return true, err
			}
		}
		if a.EmitProgress != nil {
			a.EmitProgress(formatReviewBeforeFixProgress(a.Config, run))
		}
		return true, nil
	}
	a.Session.AddMessage(internalUserMessage(formatReviewBeforeFixFeedback(run)))
	a.primeTaskStateFromReviewBeforeFix(run)
	if a.Store != nil {
		if err := a.Store.Save(a.Session); err != nil {
			return true, err
		}
	}
	if a.EmitProgress != nil {
		a.EmitProgress(formatReviewBeforeFixProgress(a.Config, run))
		if allowIndependentInspection {
			a.EmitProgress(reviewRunLocalizedText(a.Config, run,
				"Pre-fix local review route was unreliable, so the main model will continue with independent source inspection instead of treating the review as approval.",
				"수정 전 로컬 리뷰 route가 신뢰 가능한 finding을 만들지 못해, 리뷰 승인으로 보지 않고 메인 모델이 소스 코드를 독립 확인한 뒤 계속 수리합니다."))
		}
		a.EmitProgress(formatReviewBeforeFixHandoffProgress(a.Config, run))
	}
	return true, nil
}

func (a *Agent) emitPersistentAssistantSummary(summary string) {
	summary = strings.TrimSpace(summary)
	if a == nil || summary == "" || summary == a.lastEmittedText {
		return
	}
	if a.EmitAssistantPersistent != nil {
		a.EmitAssistantPersistent(summary)
	} else if a.EmitAssistant != nil {
		a.EmitAssistant(summary)
	}
	a.lastEmittedText = summary
}

func (a *Agent) maybeStopAfterReviewerGateUnavailable() (string, bool) {
	if a == nil || a.Session == nil || a.Session.LastReviewRun == nil {
		return "", false
	}
	run := *a.Session.LastReviewRun
	if preFixReviewCanContinueWithIndependentInspection(run) {
		return "", false
	}
	if !reviewRunHasRequiredReviewerFailure(run) {
		if preFixReviewHasUnreliableNoActionableFinding(run) {
			return formatPreFixNoReliableActionableFindingsReply(a.Config, run), true
		}
		return "", false
	}
	return formatReviewerGateUnavailableUserDecisionReply(a.Config, a.Session), true
}

func (rt *runtimeState) reviewBeforeFixOptions(input string, images []MessageImage) (ReviewHarnessOptions, *ViewerSelection, bool) {
	request := strings.TrimSpace(input)
	if request == "" || len(images) > 0 || !looksLikeReviewBeforeFixIntent(request) {
		return ReviewHarnessOptions{}, nil, false
	}
	root := ""
	if rt != nil {
		root = strings.TrimSpace(rt.workspace.Root)
	}
	selection, hasSelectionMention := firstReviewMentionSelection(root, request)
	mentionPath, hasPathMention := firstReviewMentionPath(root, request)
	explicitReviewIntent := hasNaturalReviewIntent(request)
	hasActiveSelection := false
	if rt != nil && rt.session != nil {
		if current := rt.session.CurrentSelection(); current != nil && current.HasSelection() {
			hasActiveSelection = true
		}
	}
	target := ""
	var paths []string
	includeFileContents := false
	includeGitDiff := true
	if hasSelectionMention && selection != nil {
		target = reviewTargetSelection
		paths = []string{selection.FilePath}
		includeGitDiff = false
	} else if hasPathMention {
		target = reviewTargetChange
		paths = []string{mentionPath}
		includeFileContents = true
		includeGitDiff = false
	} else if hasActiveSelection && looksSelectionScopedReviewRequest(request) {
		target = reviewTargetSelection
		includeGitDiff = false
	} else if explicitReviewIntent && strings.TrimSpace(root) != "" {
		paths = autoReviewChangedPaths(nilSafeSession(rt), root)
		if len(paths) > 0 {
			target = reviewTargetChange
		}
	}
	if target == "" && hasActiveSelection {
		target = reviewTargetSelection
		includeGitDiff = false
	}
	if target == "" {
		return ReviewHarnessOptions{}, nil, false
	}
	maxContextChars := reviewFocusedMaxContextChars
	if target == reviewTargetSelection || len(paths) > 0 {
		maxContextChars = reviewSourceAnalysisMaxContextChars
	}
	return ReviewHarnessOptions{
		Trigger:             reviewBeforeFixTrigger,
		Target:              target,
		Request:             request,
		Paths:               paths,
		IncludeGitDiff:      includeGitDiff,
		IncludeFileContents: includeFileContents,
		AutoTriggered:       true,
		AutoFollowUp:        "none",
		MaxContextChars:     maxContextChars,
		RawArgs:             request,
	}, selection, true
}

func nilSafeSession(rt *runtimeState) *Session {
	if rt == nil {
		return nil
	}
	return rt.session
}

func (a *Agent) maybePrimeRepairFromLastReview(userText string, images []MessageImage, readOnlyAnalysis bool, explicitEditRequest bool) bool {
	if a == nil || a.Session == nil || readOnlyAnalysis || !explicitEditRequest || len(images) > 0 {
		return false
	}
	if !looksLikeReviewRepairFollowUpIntent(userText) {
		return false
	}
	run := a.Session.LastReviewRun
	if run == nil || !reviewRunNeedsRepair(*run) {
		return false
	}
	feedback := formatReviewRepairFollowUpFeedback(*run)
	if proposal := formatLatestEditProposalForUserDecision(a.Config, a.Session); proposal != "" {
		feedback += "\n\n" + proposal
	}
	a.Session.AddMessage(internalUserMessage(feedback))
	a.primeTaskStateFromReviewBeforeFix(*run)
	if a.EmitProgress != nil {
		a.EmitProgress(localizedTextForReviewRequest(a.Config, userText, "Continuing from latest review findings...", "최신 리뷰 결과를 기준으로 수정 흐름을 이어갑니다..."))
	}
	return true
}

func (a *Agent) maybePrimeRepairFromReviewerGateUnavailable(userText string, images []MessageImage, readOnlyAnalysis bool, explicitEditRequest bool) bool {
	if a == nil || a.Session == nil || readOnlyAnalysis || !explicitEditRequest || len(images) > 0 {
		return false
	}
	return a.primeReviewerGateRepairFromLastReview(userText)
}

func (a *Agent) primeReviewerGateRepairFromLastReview(userText string) bool {
	if a == nil || a.Session == nil {
		return false
	}
	run := a.Session.LastReviewRun
	if run == nil || !reviewRunNeedsRepair(*run) || !reviewRunHasActionableNonReviewerFindings(*run) {
		return false
	}
	feedback := formatReviewerGateUnavailableRepairFollowUpFeedback(*run)
	if proposal := formatLatestEditProposalForUserDecision(a.Config, a.Session); proposal != "" {
		feedback += "\n\n" + proposal
	}
	a.Session.AddMessage(internalUserMessage(feedback))
	a.primeTaskStateFromReviewBeforeFix(*run)
	if a.EmitProgress != nil {
		a.EmitProgress(localizedTextForReviewRequest(a.Config, userText, "Continuing from actionable review findings after reviewer gate failure...", "리뷰어 게이트 실패 후 실행 가능한 finding을 기준으로 수정 흐름을 이어갑니다..."))
	}
	return true
}

func looksLikeReviewRepairFollowUpIntent(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" || hasNaturalReviewNegation(lower) {
		return false
	}
	if strings.HasPrefix(lower, "/") {
		return false
	}
	return containsAny(lower,
		"수정", "고쳐", "고치", "해결", "반영", "패치", "진행", "이어",
		"fix", "repair", "patch", "address", "apply", "continue")
}

func reviewRunNeedsRepair(run ReviewRun) bool {
	verdict := strings.TrimSpace(run.Gate.Verdict)
	if verdict == "" {
		verdict = strings.TrimSpace(run.Result.Verdict)
	}
	if strings.EqualFold(verdict, reviewVerdictNeedsRevision) ||
		strings.EqualFold(verdict, reviewVerdictBlocked) ||
		strings.EqualFold(verdict, reviewVerdictInsufficientEvidence) {
		return true
	}
	return len(run.Gate.BlockingFindings) > 0
}

func reviewRunHasActionableNonReviewerFindingsFromSession(session *Session) bool {
	if session == nil || session.LastReviewRun == nil {
		return false
	}
	return reviewRunHasActionableNonReviewerFindings(*session.LastReviewRun)
}

func reviewRunHasActionableNonReviewerFindings(run ReviewRun) bool {
	return len(actionableNonReviewerFindings(run, 1)) > 0
}

func actionableNonReviewerFindings(run ReviewRun, limit int) []ReviewFinding {
	ids := append([]string{}, run.Gate.BlockingFindings...)
	ids = append(ids, run.Gate.WarningFindings...)
	idSet := reviewFindingIDSet(ids)
	out := collectActionableNonReviewerFindings(run, idSet, limit)
	if len(out) > 0 || len(idSet) == 0 {
		return out
	}
	return collectActionableNonReviewerFindings(run, nil, limit)
}

func collectActionableNonReviewerFindings(run ReviewRun, idSet map[string]bool, limit int) []ReviewFinding {
	out := make([]ReviewFinding, 0)
	for _, finding := range run.Findings {
		finding.Normalize()
		if !reviewFindingIsActionableNonReviewerFinding(run, finding, idSet) {
			continue
		}
		out = append(out, finding)
		if limit > 0 && len(out) >= limit {
			return out
		}
	}
	return out
}

func reviewFindingIsActionableNonReviewerFinding(run ReviewRun, finding ReviewFinding, idSet map[string]bool) bool {
	if strings.EqualFold(strings.TrimSpace(finding.ID), requiredReviewerFailureFindingID) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(finding.Category), "evidence_gap") ||
		strings.EqualFold(strings.TrimSpace(finding.Category), "test_gap") {
		return false
	}
	if len(idSet) > 0 && !idSet[finding.ID] {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(finding.Severity), reviewSeverityInfo) {
		return false
	}
	if strings.TrimSpace(finding.RequiredFix) == "" &&
		strings.TrimSpace(finding.Title) == "" &&
		strings.TrimSpace(finding.Evidence) == "" {
		return false
	}
	if reviewFindingBlocksGate(run, finding) || reviewFindingCountsAsWarning(finding) {
		return true
	}
	return len(idSet) > 0 && idSet[finding.ID]
}

func reviewBeforeFixNeedsDeepBugHunt(run ReviewRun) bool {
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		return false
	}
	if !looksLikeBugFindingFixIntent(run.Objective) && !(hasNaturalReviewIntent(run.Objective) && hasRepairActionIntent(strings.ToLower(run.Objective))) {
		return false
	}
	if run.Target == reviewTargetSelection {
		return true
	}
	if len(run.ChangeSet.ChangedPaths) > 0 || len(run.RequestAnalysis.ScopeDiscovery.CandidateFiles) > 0 {
		return true
	}
	return false
}

func preFixNonConclusiveBugHuntFindings(run ReviewRun) []ReviewFinding {
	if !reviewBeforeFixNeedsDeepBugHunt(run) || preFixReviewHasActionableBugHuntFinding(run) {
		return nil
	}
	if len(run.Evidence.ChangedPaths) == 0 &&
		len(run.ChangeSet.ChangedPaths) == 0 &&
		len(run.RequestAnalysis.ScopeDiscovery.CandidateFiles) == 0 &&
		!strings.EqualFold(strings.TrimSpace(run.Target), reviewTargetSelection) {
		return nil
	}
	if preFixReviewHadLocalDegradedUnreliableReviewerRoute(run) {
		if reviewRunPrefersKoreanFromRequest(run) {
			return []ReviewFinding{{
				ID:                 "RF-PREFIX-001",
				Source:             "deterministic",
				Severity:           reviewSeverityMedium,
				Category:           "evidence_gap",
				Title:              "수정 전 로컬 리뷰 route가 실행 가능한 버그 finding을 만들지 못했습니다",
				Evidence:           "요청은 버그를 검토하고 수정하라는 내용이지만, 수정 전 리뷰의 로컬/호환 모델 route가 실패했거나 weak/empty 응답을 반환했고 실행 가능한 correctness, stability, security, performance finding도 없습니다.",
				Impact:             "이 결과는 코드가 안전하다는 승인이 아닙니다. 다만 로컬 모델 route 특성상 review-only 형식화에 실패했을 수 있으므로, 구현 모델은 파일 내용을 직접 읽고 독립적으로 확인한 뒤 명확한 수리 대상이 있을 때만 수정해야 합니다.",
				RequiredFix:        "참조된 소스를 독립적으로 확인한 뒤 명확히 필요한 수정만 적용하세요. 추측성 rewrite는 피하고, 파일 쓰기 전 pre-write review는 그대로 통과해야 합니다.",
				TestRecommendation: "편집 후 touched code에 사용할 수 있는 focused verification을 실행하고 pre-write review가 승인하는지 확인하세요.",
				Confidence:         "medium",
			}}
		}
		return []ReviewFinding{{
			ID:                 "RF-PREFIX-001",
			Source:             "deterministic",
			Severity:           reviewSeverityMedium,
			Category:           "evidence_gap",
			Title:              "Pre-fix local review route produced no actionable bug findings",
			Evidence:           "The request asks to inspect and fix bugs, but a local or OpenAI-compatible pre-fix review-stage model route failed or returned weak/empty output and no actionable correctness, stability, security, or performance finding is available.",
			Impact:             "This is not evidence that the code is safe. Local model routes can fail review-only formatting, so the implementation model must inspect the referenced source directly and edit only when it finds a concrete repair target.",
			RequiredFix:        "Inspect the referenced source independently, apply only clearly necessary fixes, avoid speculative rewrites, and still pass the normal pre-write review before writing.",
			TestRecommendation: "Run focused verification for touched code after editing and confirm the pre-write review approves the proposal.",
			Confidence:         "medium",
		}}
	}
	if preFixReviewHadUnreliableReviewerRoute(run) {
		if reviewRunPrefersKoreanFromRequest(run) {
			return []ReviewFinding{{
				ID:                 "RF-PREFIX-001",
				Source:             "deterministic",
				Severity:           reviewSeverityBlocker,
				Category:           "evidence_gap",
				Title:              "수정 전 리뷰 route가 실행 가능한 버그 finding을 만들지 못했습니다",
				Evidence:           "요청은 버그를 검토하고 수정하라는 내용이지만, 수정 전 리뷰의 필수 모델 route가 실패했거나 weak/empty 응답을 반환했고 실행 가능한 correctness, stability, security, performance finding도 없습니다.",
				Impact:             "이 상태에서 구현 모델을 독립 수리 모드로 넘기면 근거 없는 추측성 패치가 생성될 수 있습니다.",
				RequiredFix:        "실패한 review route를 복구하거나 더 안정적인 모델로 바꾼 뒤 같은 요청을 다시 실행하세요. 코드 수정은 신뢰 가능한 리뷰 finding이 생긴 뒤 진행하세요.",
				TestRecommendation: "같은 review-before-fix 요청을 다시 실행하여 하나 이상의 usable actionable bug finding 또는 명시적인 approval을 확인하세요.",
				Confidence:         "high",
				Quality:            reviewFindingQualityComplete,
				BlocksGate:         true,
			}}
		}
		return []ReviewFinding{{
			ID:                 "RF-PREFIX-001",
			Source:             "deterministic",
			Severity:           reviewSeverityBlocker,
			Category:           "evidence_gap",
			Title:              "Pre-fix review route produced no actionable bug findings",
			Evidence:           "The request asks to inspect and fix bugs, but a pre-fix review-stage model route failed or returned weak/empty output and no actionable correctness, stability, security, or performance finding is available.",
			Impact:             "Handing this state to the implementation model can produce speculative edits without a reliable repair target.",
			RequiredFix:        "Restore the failed review route or switch to a stronger model, then rerun the same request. Do not edit until a reliable actionable review finding or explicit approval is available.",
			TestRecommendation: "Rerun the same review-before-fix request and confirm at least one usable actionable bug finding or a reliable approval.",
			Confidence:         "high",
			Quality:            reviewFindingQualityComplete,
			BlocksGate:         true,
		}}
	}
	if reviewRunPrefersKoreanFromRequest(run) {
		return []ReviewFinding{{
			ID:                 "RF-PREFIX-001",
			Source:             "deterministic",
			Severity:           reviewSeverityMedium,
			Category:           "evidence_gap",
			Title:              "수정 전 리뷰가 실행 가능한 버그 finding을 반환하지 않았습니다",
			Evidence:           "요청은 버그를 검토하고 수정하라는 내용이지만, 수정 전 리뷰가 실행 가능한 correctness, stability, security, performance finding을 만들지 못했습니다.",
			Impact:             "수정 전 리뷰의 approved 판정은 참조된 코드에 버그가 없다는 증거가 아닙니다. 구현 모델은 편집 전에 코드를 독립적으로 확인해야 합니다.",
			RequiredFix:        "참조된 코드를 독립적으로 확인한 뒤 명확히 필요한 수정만 적용하세요.",
			TestRecommendation: "편집 후 touched code에 사용할 수 있는 focused verification을 실행하고 가능하면 리뷰를 반복하세요.",
			Confidence:         "medium",
		}}
	}
	return []ReviewFinding{{
		ID:                 "RF-PREFIX-001",
		Source:             "deterministic",
		Severity:           reviewSeverityMedium,
		Category:           "evidence_gap",
		Title:              "Pre-fix review returned no actionable bug findings",
		Evidence:           "The request asks to inspect and fix bugs, but the pre-fix review did not produce any actionable correctness, stability, security, or performance finding.",
		Impact:             "An approved pre-fix pass is not proof that the referenced code is bug-free; the implementation model must still inspect the code before editing.",
		RequiredFix:        "Continue with an independent source inspection of the referenced code and apply only clearly necessary fixes.",
		TestRecommendation: "After editing, run the focused verification available for the touched code and repeat review if possible.",
		Confidence:         "medium",
	}}
}

func preFixReviewHasUnreliableNoActionableFinding(run ReviewRun) bool {
	return strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) &&
		preFixReviewHadUnreliableReviewerRoute(run) &&
		!preFixReviewHasActionableBugHuntFinding(run)
}

func preFixReviewCanContinueWithIndependentInspection(run ReviewRun) bool {
	return strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) &&
		reviewBeforeFixNeedsDeepBugHunt(run) &&
		preFixReviewHadLocalDegradedUnreliableReviewerRoute(run) &&
		!preFixReviewHasActionableBugHuntFinding(run)
}

func preFixReviewHadUnreliableReviewerRoute(run ReviewRun) bool {
	if reviewRunHasUsableMainReviewer(run) {
		return false
	}
	for _, reviewerRun := range run.ReviewerRuns {
		if !preFixReviewerRunIsMainRoute(reviewerRun) {
			continue
		}
		if preFixReviewerRunIsNoConfiguredModel(reviewerRun) {
			continue
		}
		if preFixReviewerRunIsUnreliable(reviewerRun) {
			return true
		}
	}
	return false
}

func preFixReviewHadLocalDegradedUnreliableReviewerRoute(run ReviewRun) bool {
	if reviewRunHasUsableMainReviewer(run) {
		return false
	}
	for _, reviewerRun := range run.ReviewerRuns {
		if !preFixReviewerRunIsMainRoute(reviewerRun) {
			continue
		}
		if !preFixReviewerRunIsUnreliable(reviewerRun) {
			continue
		}
		if reviewProviderUsesLocalModelRecovery(reviewReviewerRunProvider(Config{}, reviewerRun)) {
			return true
		}
	}
	return false
}

func preFixReviewerRunIsMainRoute(reviewerRun ReviewReviewerRun) bool {
	kind := strings.ToLower(strings.TrimSpace(reviewerRun.Kind))
	if kind == "cross" {
		return false
	}
	if kind == "main" {
		return true
	}
	return normalizeReviewRole(reviewerRun.Role) == "primary_reviewer"
}

func preFixReviewerRunIsUnreliable(reviewerRun ReviewReviewerRun) bool {
	return strings.EqualFold(strings.TrimSpace(reviewerRun.Status), "failed") ||
		strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityWeak) ||
		strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityFailed) ||
		strings.TrimSpace(reviewerRun.Error) != ""
}

func preFixReviewerRunIsNoConfiguredModel(reviewerRun ReviewReviewerRun) bool {
	text := strings.ToLower(strings.Join([]string{
		reviewerRun.Status,
		reviewerRun.ModelQuality,
		reviewerRun.Model,
		reviewerRun.Error,
	}, " "))
	if !containsAny(text, "no main model configured", "no reviewer model configured", "no model configured", "model review disabled") {
		return false
	}
	return strings.TrimSpace(reviewerRun.Model) == "" ||
		strings.Contains(text, "no main model configured") ||
		strings.Contains(text, "no reviewer model configured")
}

func preFixReviewHasActionableBugHuntFinding(run ReviewRun) bool {
	for _, finding := range run.Findings {
		finding.Normalize()
		if strings.EqualFold(finding.Source, "deterministic") && strings.EqualFold(finding.Category, "evidence_gap") {
			continue
		}
		if strings.EqualFold(finding.Category, "test_gap") || strings.EqualFold(finding.Category, "evidence_gap") {
			continue
		}
		if reviewSeverityRank(finding.Severity) > reviewSeverityRank(reviewSeverityLow) {
			continue
		}
		if strings.TrimSpace(finding.Title) != "" || strings.TrimSpace(finding.RequiredFix) != "" || strings.TrimSpace(finding.Evidence) != "" {
			return true
		}
	}
	return false
}

func formatPreFixNoReliableActionableFindingsReply(cfg Config, run ReviewRun) string {
	korean := reviewRunPrefersKorean(cfg, run)
	var b strings.Builder
	if korean {
		b.WriteString("수정 전 리뷰가 충분히 신뢰 가능한 버그 finding을 만들지 못해서 수리 단계로 진행하지 않았습니다.")
		b.WriteString("\n\n- 결과: 코드 수정은 적용하지 않았습니다.")
		b.WriteString("\n- 원인: review route가 실패/weak/empty 응답을 반환했고, 수리 기준으로 삼을 실행 가능한 code finding이 없습니다.")
		b.WriteString("\n- 중요한 점: 이 상태에서 Qwen 같은 로컬 모델을 독립 수리로 넘기면 추측성 패치가 나올 수 있으므로 멈추는 것이 맞습니다.")
		b.WriteString("\n- 다음 조치: `/model` 또는 `/review models`로 실패한 route를 바꾸거나 복구한 뒤 같은 요청을 다시 실행하세요.")
	} else {
		b.WriteString("The pre-fix review did not produce reliable actionable bug findings, so I did not continue into repair.")
		b.WriteString("\n\n- Result: no code changes were applied.")
		b.WriteString("\n- Cause: a review route failed or returned weak/empty output, and there is no actionable code finding to repair from.")
		b.WriteString("\n- Important: handing this state to a local model for independent repair can produce speculative patches, so stopping is intentional.")
		b.WriteString("\n- Next step: switch or restore the failed route with `/model` or `/review models`, then rerun the same request.")
	}
	if strings.TrimSpace(run.Result.Summary) != "" {
		if korean {
			fmt.Fprintf(&b, "\n\n요약: %s", run.Result.Summary)
		} else {
			fmt.Fprintf(&b, "\n\nSummary: %s", run.Result.Summary)
		}
	}
	if len(run.Findings) > 0 {
		if korean {
			b.WriteString("\n\n주요 finding:")
		} else {
			b.WriteString("\n\nKey findings:")
		}
		for _, finding := range limitReviewFindings(run.Findings, 3) {
			fmt.Fprintf(&b, "\n- %s [%s/%s]: %s", valueOrDefault(finding.ID, "RF"), valueOrUnset(finding.Severity), valueOrUnset(finding.Category), valueOrDefault(finding.Title, "Review finding"))
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

func formatReviewBeforeFixProgress(cfg Config, run ReviewRun) string {
	korean := reviewRunPrefersKorean(cfg, run)
	blockers := reviewProgressFindingsByID(run, run.Gate.BlockingFindings, 3)
	warnings := reviewCLIWarningFindings(run)
	if len(blockers) == 0 && len(warnings) == 0 {
		if korean {
			return "수정 전 리뷰가 완료되었습니다."
		}
		return "Review before fix completed."
	}
	parts := make([]string, 0, 4)
	verdict := valueOrDefault(run.Gate.Verdict, run.Result.Verdict)
	if strings.TrimSpace(verdict) != "" {
		if korean {
			parts = append(parts, "결과="+verdict)
		} else {
			parts = append(parts, "verdict="+verdict)
		}
	}
	if len(blockers) > 0 {
		if korean {
			parts = append(parts, fmt.Sprintf("차단 %d개. %s", len(run.Gate.BlockingFindings), strings.Join(reviewProgressFindingTitles(blockers, 3), " | ")))
		} else {
			parts = append(parts, fmt.Sprintf("blockers=%d. %s", len(run.Gate.BlockingFindings), strings.Join(reviewProgressFindingTitles(blockers, 3), " | ")))
		}
	}
	if len(warnings) > 0 {
		warningCount := len(run.Gate.WarningFindings)
		if warningCount == 0 {
			warningCount = len(warnings)
		}
		if korean {
			parts = append(parts, fmt.Sprintf("경고 %d개. %s", warningCount, strings.Join(reviewProgressFindingTitles(warnings, 3), " | ")))
		} else {
			parts = append(parts, fmt.Sprintf("warnings=%d. %s", warningCount, strings.Join(reviewProgressFindingTitles(warnings, 3), " | ")))
		}
	}
	if len(run.ArtifactRefs) > 0 {
		if korean {
			parts = append(parts, "보고서: "+run.ArtifactRefs[0])
		} else {
			parts = append(parts, "report: "+run.ArtifactRefs[0])
		}
	}
	if korean {
		if len(warnings) > 0 && len(blockers) == 0 {
			return strings.TrimSpace("수정 전 리뷰가 경고와 함께 완료되었습니다. " + strings.Join(parts, " | "))
		}
		return strings.TrimSpace("수정 전 리뷰가 완료되었습니다. " + strings.Join(parts, " | "))
	}
	if len(warnings) > 0 && len(blockers) == 0 {
		return strings.TrimSpace("Review before fix completed with warnings. " + strings.Join(parts, " | "))
	}
	return strings.TrimSpace("Review before fix completed. " + strings.Join(parts, " | "))
}

func formatReviewBeforeFixHandoffProgress(cfg Config, run ReviewRun) string {
	korean := reviewRunPrefersKorean(cfg, run)
	if reviewRunNeedsRepair(run) {
		if korean {
			return "리뷰 결과가 메인 모델에 전달되었습니다. 메인 모델이 RF 항목을 반영해 수정 계획과 패치를 작성합니다."
		}
		return "Review findings were handed back to the main model. The main model will incorporate the RF items into its repair plan and patch."
	}
	if korean {
		return "리뷰 결과가 메인 모델에 전달되었습니다. 메인 모델이 수정 필요 여부를 판단해 답변을 정리합니다."
	}
	return "Review findings were handed back to the main model. The main model will decide whether any edit is needed and summarize the result."
}

func reviewProgressFindingsByID(run ReviewRun, ids []string, limit int) []ReviewFinding {
	idSet := reviewFindingIDSet(ids)
	if len(idSet) == 0 {
		return nil
	}
	out := make([]ReviewFinding, 0, minInt(len(ids), limit))
	for _, finding := range run.Findings {
		if !idSet[finding.ID] {
			continue
		}
		out = append(out, finding)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func reviewProgressFindingTitles(findings []ReviewFinding, limit int) []string {
	titles := make([]string, 0, minInt(len(findings), limit))
	for _, finding := range limitReviewFindings(findings, limit) {
		title := strings.TrimSpace(finding.Title)
		evidence := strings.TrimSpace(finding.Evidence)
		if title == "" || (strings.Contains(strings.ToLower(title), " finding in ") && evidence != "") {
			title = evidence
		}
		if title == "" {
			title = strings.TrimSpace(finding.Evidence)
		}
		if title == "" {
			continue
		}
		titles = append(titles, fmt.Sprintf("%s %s: %s", valueOrDefault(finding.ID, "RF"), finding.Severity, compactReviewVisibleInlineText(title, 140)))
	}
	return titles
}

func (a *Agent) shouldConcludeAfterNonBlockingPreFixReview(userText string) bool {
	if a == nil || a.Session == nil || a.Session.LastReviewRun == nil {
		return false
	}
	run := *a.Session.LastReviewRun
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		return false
	}
	if reviewRunNeedsRepair(run) {
		return false
	}
	return looksLikeBugFindingFixIntent(userText) && !hasNaturalReviewIntent(userText)
}

func (a *Agent) formatNonBlockingPreFixReviewReply() string {
	if a == nil || a.Session == nil || a.Session.LastReviewRun == nil {
		return "Pre-fix review completed without blocking findings, so no edit was applied."
	}
	run := *a.Session.LastReviewRun
	korean := reviewRunPrefersKorean(a.Config, run)
	var b strings.Builder
	if korean {
		b.WriteString("수정 전 리뷰에서 차단 수준의 버그를 찾지 못해서 코드는 수정하지 않았습니다.")
	} else {
		b.WriteString("The pre-fix review did not find blocking bug findings, so I did not modify files.")
	}
	verdict := valueOrDefault(run.Gate.Verdict, run.Result.Verdict)
	if strings.TrimSpace(verdict) != "" {
		if korean {
			fmt.Fprintf(&b, "\n\n리뷰 결과: `%s`", verdict)
		} else {
			fmt.Fprintf(&b, "\n\nReview verdict: `%s`", verdict)
		}
	}
	if len(run.Gate.WarningFindings) > 0 {
		if korean {
			fmt.Fprintf(&b, "\n경고 finding: %d개", len(run.Gate.WarningFindings))
		} else {
			fmt.Fprintf(&b, "\nWarning findings: %d", len(run.Gate.WarningFindings))
		}
	}
	warnings := preFixReplyWarningFindings(run, 5)
	if len(warnings) > 0 {
		if korean {
			b.WriteString("\n\n참고 경고:")
		} else {
			b.WriteString("\n\nWarnings:")
		}
		for _, finding := range warnings {
			fmt.Fprintf(&b, "\n- [%s] %s/%s: %s", valueOrDefault(finding.ID, "finding"), finding.Severity, finding.Category, valueOrDefault(finding.Title, "Review finding"))
			if strings.TrimSpace(finding.Evidence) != "" {
				if korean {
					fmt.Fprintf(&b, "\n  근거: %s", finding.Evidence)
				} else {
					fmt.Fprintf(&b, "\n  Evidence: %s", finding.Evidence)
				}
			}
			if strings.TrimSpace(finding.Impact) != "" {
				if korean {
					fmt.Fprintf(&b, "\n  영향: %s", finding.Impact)
				} else {
					fmt.Fprintf(&b, "\n  Impact: %s", finding.Impact)
				}
			}
			if strings.TrimSpace(finding.RequiredFix) != "" {
				if korean {
					fmt.Fprintf(&b, "\n  권장 조치: %s", finding.RequiredFix)
				} else {
					fmt.Fprintf(&b, "\n  Suggested action: %s", finding.RequiredFix)
				}
			}
			if strings.TrimSpace(finding.TestRecommendation) != "" {
				if korean {
					fmt.Fprintf(&b, "\n  테스트: %s", finding.TestRecommendation)
				} else {
					fmt.Fprintf(&b, "\n  Test: %s", finding.TestRecommendation)
				}
			}
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

func preFixReplyWarningFindings(run ReviewRun, limit int) []ReviewFinding {
	warnings := reviewCLIWarningFindings(run)
	if len(warnings) == 0 {
		warnings = nonBlockingReviewFindings(run, limit)
	}
	if limit <= 0 || len(warnings) <= limit {
		return warnings
	}
	return warnings[:limit]
}

func nonBlockingReviewFindings(run ReviewRun, limit int) []ReviewFinding {
	if limit <= 0 {
		limit = 3
	}
	var out []ReviewFinding
	for _, finding := range run.Findings {
		if reviewFindingBlocksGate(run, finding) {
			continue
		}
		if strings.EqualFold(finding.Category, "test_gap") || strings.EqualFold(finding.Category, "evidence_gap") {
			continue
		}
		if reviewSeverityRank(finding.Severity) > reviewSeverityRank(reviewSeverityMedium) {
			continue
		}
		out = append(out, finding)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func formatReviewRepairFollowUpFeedback(run ReviewRun) string {
	korean := reviewRunPrefersKoreanFromRequest(run)
	var b strings.Builder
	if korean {
		b.WriteString("직전 리뷰의 차단 finding을 수정하는 후속 요청입니다. 아래 리뷰 결과를 수리 지침으로 사용해서 바로 수정하세요.")
	} else {
		b.WriteString("This is a follow-up repair request for the latest blocking review findings. Use the review below as the repair guide and proceed directly.")
	}
	b.WriteString("\n\n")
	b.WriteString(formatReviewBeforeFixFeedback(run))
	return strings.TrimSpace(b.String())
}

func formatReviewerGateUnavailableRepairFollowUpFeedback(run ReviewRun) string {
	korean := reviewRunPrefersKoreanFromRequest(run)
	findings := actionableNonReviewerFindings(run, 0)
	var b strings.Builder
	if korean {
		b.WriteString("직전 pre-write 리뷰는 필수 리뷰 단계의 모델 route 실패 때문에 승인되지 않았습니다. 이것을 쓰기 승인이나 리뷰 우회로 취급하지 마세요.")
		b.WriteString("\n\n아래 코드 finding과 마지막 수정안을 기준으로 다시 수리하세요. 파일 쓰기 또는 패치 도구를 호출하면 일반 pre-write review gate가 다시 실행되어야 합니다.")
		b.WriteString("\n\n구현 규칙:\n")
		b.WriteString("- RF-REVIEWER-001 같은 리뷰어 실패/evidence gap은 코드 수정 대상으로 삼지 마세요.\n")
		b.WriteString("- 아래 비리뷰어 finding만 고치고, 범위를 넓히지 마세요.\n")
		b.WriteString("- 리뷰 artifact 파일을 다시 읽지 마세요. 필요한 리뷰 지침은 여기 모두 포함되어 있습니다.\n")
		b.WriteString("- 패치는 좁은 hunk로 작성하고, apply_patch 문법을 엄격히 지키세요.\n")
		b.WriteString(reviewPatchRelevanceGuidance(true))
		b.WriteString("\n")
		b.WriteString("- 전체 패키지 테스트는 사용자가 요청할 때만 실행하세요. 필요한 경우 변경 범위의 targeted 검증만 하세요.\n")
		b.WriteString("\nActionable code findings:\n")
	} else {
		b.WriteString("The previous pre-write review was not approved because a required review-stage model route failed. Do not treat this as write approval or review bypass approval.")
		b.WriteString("\n\nRepair again from the code findings and the last edit proposal below. Any file write or patch tool call must go through the normal pre-write review gate again.")
		b.WriteString("\n\nImplementation rules:\n")
		b.WriteString("- Do not repair RF-REVIEWER-001 or other reviewer-failure/evidence-gap items as code findings.\n")
		b.WriteString("- Fix only the non-reviewer findings below and do not broaden scope.\n")
		b.WriteString("- Do not read review artifact files; all needed review guidance is included here.\n")
		b.WriteString("- Keep patches narrow and use strict apply_patch syntax.\n")
		b.WriteString(reviewPatchRelevanceGuidance(false))
		b.WriteString("\n")
		b.WriteString("- Run package-wide tests only when the user explicitly asks; use targeted verification if needed.\n")
		b.WriteString("\nActionable code findings:\n")
	}
	if len(findings) == 0 {
		if korean {
			b.WriteString("- 없음. 이번 중단은 코드 수정으로 해결할 항목이 아니라 필수 리뷰 단계의 모델 route 실패/약한 응답입니다. `primary`가 실패했다면 `/model`로 메인 모델을 바꾸거나 LM Studio/Qwen 응답 문제를 해결하세요. `cross`가 실패했다면 `/review models cross`로 해당 reviewer route를 바꾸거나 `/review models clear cross`로 single-model mode를 사용하세요. 그 뒤 같은 요청을 다시 실행하세요.\n")
		} else {
			b.WriteString("- None. This stop is a required review-stage model route failure or weak output, not a code-repair item. If `primary` failed, use `/model` to switch the main model or fix the LM Studio/Qwen response issue. If `cross` failed, use `/review models cross` to switch that reviewer route to a working model or `/review models clear cross` for single-model mode. Then rerun the same request.\n")
		}
		return strings.TrimSpace(b.String())
	}
	for _, finding := range findings {
		title := valueOrDefault(strings.TrimSpace(finding.Title), strings.TrimSpace(finding.Evidence))
		if title == "" {
			title = "Review finding"
		}
		fmt.Fprintf(&b, "- %s [%s/%s]: %s\n", valueOrDefault(finding.ID, "RF"), valueOrUnset(finding.Severity), valueOrUnset(finding.Category), compactPromptSection(title, 220))
		if strings.TrimSpace(finding.Evidence) != "" {
			if korean {
				fmt.Fprintf(&b, "  근거: %s\n", compactPromptSection(finding.Evidence, 500))
			} else {
				fmt.Fprintf(&b, "  Evidence: %s\n", compactPromptSection(finding.Evidence, 500))
			}
		}
		if strings.TrimSpace(finding.Impact) != "" {
			if korean {
				fmt.Fprintf(&b, "  영향: %s\n", compactPromptSection(finding.Impact, 500))
			} else {
				fmt.Fprintf(&b, "  Impact: %s\n", compactPromptSection(finding.Impact, 500))
			}
		}
		if strings.TrimSpace(finding.RequiredFix) != "" {
			if korean {
				fmt.Fprintf(&b, "  조치: %s\n", compactPromptSection(localizedReviewRequiredFixText(finding.RequiredFix, true), 600))
			} else {
				fmt.Fprintf(&b, "  Fix: %s\n", compactPromptSection(finding.RequiredFix, 600))
			}
		}
		if strings.TrimSpace(finding.TestRecommendation) != "" {
			if korean {
				fmt.Fprintf(&b, "  테스트: %s\n", compactPromptSection(finding.TestRecommendation, 500))
			} else {
				fmt.Fprintf(&b, "  Test: %s\n", compactPromptSection(finding.TestRecommendation, 500))
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func formatReviewBeforeFixFeedback(run ReviewRun) string {
	korean := reviewRunPrefersKoreanFromRequest(run)
	var b strings.Builder
	if korean {
		b.WriteString("수정 전에 리뷰를 완료했습니다. 아래 리뷰 결과를 수리 지침으로 사용해서 원래 요청을 계속 처리하세요.")
	} else {
		b.WriteString("Review-first pass completed before making edits. Continue the original fix request using this review as the repair guide.")
	}
	if strings.TrimSpace(run.Objective) != "" {
		if korean {
			b.WriteString("\n\n원래 요청:\n")
		} else {
			b.WriteString("\n\nOriginal request:\n")
		}
		b.WriteString(run.Objective)
	}
	if korean {
		b.WriteString("\n\n응답 언어 정책: 한국어로 답변하세요. 코드 식별자, 경로, 명령어는 원문을 유지하세요.")
	}
	if korean {
		fmt.Fprintf(&b, "\n\n검토 게이트: %s", valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
	} else {
		fmt.Fprintf(&b, "\n\nReview gate: %s", valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
	}
	if strings.TrimSpace(run.MachineStatus) != "" {
		fmt.Fprintf(&b, " (%s)", run.MachineStatus)
	}
	if strings.TrimSpace(run.Result.Summary) != "" {
		if korean {
			fmt.Fprintf(&b, "\n요약: %s", run.Result.Summary)
		} else {
			fmt.Fprintf(&b, "\nSummary: %s", run.Result.Summary)
		}
	}
	if strings.TrimSpace(run.RepairPlan.Prompt) != "" {
		if korean {
			b.WriteString("\n\n필수 수정 계획:\n")
		} else {
			b.WriteString("\n\nRequired repair plan:\n")
		}
		b.WriteString(run.RepairPlan.Prompt)
	} else {
		if korean {
			b.WriteString("\n\n인라인 리뷰 finding:\n")
		} else {
			b.WriteString("\n\nInline review findings:\n")
		}
		b.WriteString(renderReviewBeforeFixInlineFindings(run))
	}
	if korean {
		b.WriteString("\n\n구현 규칙:\n")
		if preFixReviewCanContinueWithIndependentInspection(run) {
			b.WriteString("- 수정 전 리뷰 route 실패는 코드 승인도, 수정 금지도 아닙니다. 원래 사용자 요청을 기준으로 참조된 파일을 직접 읽고 검토/수정을 계속하세요.\n")
			b.WriteString("- 리뷰가 실행 가능한 코드 finding을 만들지 못한 경우 하네스가 대체 버그 목록을 만들지 않습니다. 버그 판단은 메인 모델이 소스에서 직접 수행하고, 최종 수정안은 pre-write 리뷰가 실제 diff와 after excerpt로 검증합니다.\n")
			b.WriteString("- 파일 쓰기 전 일반 pre-write review는 계속 필수입니다.\n")
		}
		b.WriteString("- 필요한 범위에서만 추가로 확인하세요.\n")
		if strings.TrimSpace(run.RepairPlan.Prompt) != "" {
			b.WriteString("- 필수 수정 계획의 patch 작성 원칙을 따르세요. pre-write gate가 필수 RF 전체 해결을 검사하므로, 큰 rewrite 대신 RF별 좁은 hunk로 전체 필수 항목을 해결하세요.\n")
		} else {
			b.WriteString("- 수정 전 리뷰의 모든 차단 finding과 medium 이상 실행 가능 경고를 수정하세요.\n")
		}
		b.WriteString("- 나열된 경고를 조용히 무시하지 말고, 수정하거나 의도적으로 범위 밖인 이유를 명시하세요.\n")
		b.WriteString("- 리뷰된 코드/변경 범위를 넘겨 확장하지 마세요.\n")
		b.WriteString("- 리뷰 artifact 파일을 다시 읽지 마세요. 필요한 리뷰 지침은 여기 모두 포함되어 있습니다.\n")
		b.WriteString("- 파일 쓰기 또는 패치 도구를 호출하기 전에 사용자에게 `검토 결과:` 섹션으로 RF 항목과 조치 방향을 짧게 요약하세요.\n")
		b.WriteString(reviewPatchRelevanceGuidance(true))
		b.WriteString("\n")
		b.WriteString(reviewNarrowPatchGuidance(true))
		b.WriteString("\n")
		b.WriteString(reviewDedicatedInspectionToolGuidance(true))
		b.WriteString("\n")
		b.WriteString("- apply_patch를 사용할 때는 유효한 KernForge patch 문법만 보내세요. *** Begin Patch 다음 첫 줄은 반드시 *** Update File:, *** Add File:, 또는 *** Delete File:이어야 합니다. patch 본문을 @@로 시작하지 마세요.\n")
		b.WriteString("- 파일 쓰기 전 일반 pre-write review gate가 다시 실행됩니다.\n")
	} else {
		b.WriteString("\n\nImplementation rules:\n")
		if preFixReviewCanContinueWithIndependentInspection(run) {
			b.WriteString("- The failed pre-fix review route is neither code approval nor an editing ban. Continue the original user request by reading the referenced files directly and independently verifying the source.\n")
			b.WriteString("- If the review did not produce actionable code findings, the harness does not invent replacement bug findings. The main model must judge bugs from source, and pre-write review must validate the actual diff and after excerpt.\n")
			b.WriteString("- The normal pre-write review remains mandatory before any file write.\n")
		}
		b.WriteString("- Inspect further only where needed.\n")
		if strings.TrimSpace(run.RepairPlan.Prompt) != "" {
			b.WriteString("- Follow the repair plan's patch construction rules. The pre-write gate checks that every required RF is addressed, so satisfy the required set with separate narrow hunks instead of a large rewrite.\n")
		} else {
			b.WriteString("- Fix every blocking finding and every medium-or-higher actionable warning from the pre-fix review.\n")
		}
		b.WriteString("- Do not silently ignore a listed warning; either repair it or explicitly explain why the warning is intentionally out of scope.\n")
		b.WriteString("- Do not broaden scope beyond the reviewed code/change.\n")
		b.WriteString("- Do not read review artifact files; all required review guidance is included here.\n")
		b.WriteString("- Before calling any file write or patch tool, show the user a short `Review findings:` section with the RF items and repair direction.\n")
		b.WriteString(reviewPatchRelevanceGuidance(false))
		b.WriteString("\n")
		b.WriteString(reviewNarrowPatchGuidance(false))
		b.WriteString("\n")
		b.WriteString(reviewDedicatedInspectionToolGuidance(false))
		b.WriteString("\n")
		b.WriteString("- When using apply_patch, send only valid KernForge patch syntax. The first line after *** Begin Patch must be *** Update File:, *** Add File:, or *** Delete File:. Never start the patch body with @@.\n")
		b.WriteString("- The normal pre-write review gate will run again before any file write.\n")
	}
	return strings.TrimSpace(b.String())
}

func formatReviewerGateUnavailableReply(cfg Config, run ReviewRun) string {
	korean := reviewRunPrefersKorean(cfg, run)
	failed := reviewFailedRequiredReviewerRuns(run)
	var b strings.Builder
	preWriteGate := strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write")
	if korean {
		if preWriteGate {
			b.WriteString("쓰기 전 리뷰어 게이트가 충분한 근거를 만들지 못해서 편집을 중단했습니다.")
		} else {
			b.WriteString("리뷰어 게이트가 충분한 근거를 만들지 못해서 편집을 중단했습니다.")
		}
		b.WriteString("\n\n- 원인: 필수 리뷰 단계의 모델 route가 실패했거나 `weak` 품질로 판정되었습니다. `primary`는 현재 메인 모델 route이고, `cross`는 전용 reviewer route입니다.")
		b.WriteString("\n- 결과: 이 상태의 리뷰 결과는 쓰기 승인으로 신뢰할 수 없어 편집을 적용하지 않습니다.")
		b.WriteString("\n- 다음 조치: `primary` 실패면 `/model`로 메인 모델을 바꾸거나 해당 provider 문제를 해결하세요. `cross` 실패면 `/review models`로 reviewer route를 바꾸세요. 그 뒤 같은 요청을 다시 실행하세요.")
		if reviewRunHasUsableMainReviewer(run) {
			b.WriteString("\n- 선택: 그래도 위 메인 모델 리뷰 결과를 기준으로 사용자가 직접 diff preview에서 판단하려면 `메인 모델 리뷰 기준으로 진행`이라고 답하세요.")
		}
		b.WriteString("\n\n코드 수정은 적용하지 않았습니다.")
	} else {
		if preWriteGate {
			b.WriteString("The pre-write reviewer gate did not produce enough reliable evidence, so I stopped the edit.")
		} else {
			b.WriteString("The reviewer gate did not produce enough reliable evidence, so I stopped the edit.")
		}
		b.WriteString("\n\n- Cause: a required review-stage model route failed or was classified as `weak` quality. `primary` is the active main model route; `cross` is the independent reviewer route.")
		b.WriteString("\n- Result: this review cannot be trusted as write approval, so the edit was not applied.")
		b.WriteString("\n- Next step: if `primary` failed, use `/model` to switch the main model or fix that provider route. If `cross` failed, use `/review models` to switch the reviewer route. Then rerun the same request.")
		if reviewRunHasUsableMainReviewer(run) {
			b.WriteString("\n- Option: if you still want to decide from the main model review shown above, reply with `proceed with the main model review` and I will retry the edit with diff-preview confirmation.")
		}
		b.WriteString("\n\nNo code changes were applied.")
	}
	if recoveryOptions := formatReviewerGateRecoveryOptions(korean, &run); recoveryOptions != "" {
		b.WriteString("\n\n")
		b.WriteString(recoveryOptions)
	}
	if len(failed) > 0 {
		if korean {
			b.WriteString("\n\n실패한 리뷰어:")
		} else {
			b.WriteString("\n\nFailed reviewer:")
		}
		for _, reviewerRun := range failed {
			role := firstNonBlankString(reviewRoleProgressName(reviewerRun.Role), "reviewer")
			status := valueOrDefault(strings.TrimSpace(reviewerRun.Status), "unknown")
			quality := valueOrDefault(strings.TrimSpace(reviewerRun.ModelQuality), "unknown")
			detail := firstNonBlankString(firstNonEmptyLine(reviewerRun.Error), "reviewer output was too weak")
			fmt.Fprintf(&b, "\n- %s status=%s quality=%s: %s", role, status, quality, detail)
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

func formatReviewerGateRecoveryOptions(korean bool, run *ReviewRun) string {
	timeout := reviewRunHasReviewerTimeoutFailure(run)
	if korean {
		var b strings.Builder
		b.WriteString("[사용자 선택지]")
		if timeout {
			b.WriteString("\n- 같은 reviewer로 재시도: 최근 timeout이 기록된 route는 다음 reviewer call에서 soft timeout을 자동으로 한 단계 늘립니다.")
		} else {
			b.WriteString("\n- 같은 reviewer로 재시도: route 설정을 유지하고 같은 요청을 다시 실행합니다.")
		}
		b.WriteString("\n- reviewer 모델 변경: `/review models`로 더 가까운/강한 reviewer를 선택합니다.")
		b.WriteString("\n- reviewer 없이 single-model mode: `/review models clear cross` 후 같은 요청을 다시 실행하고 diff preview에서 직접 확인합니다.")
		b.WriteString("\n- main model 변경: `[0] 실패한 리뷰어`가 `primary`이면 `/model`로 메인 모델을 바꿉니다.")
		return b.String()
	}
	var b strings.Builder
	b.WriteString("[User actions]")
	if timeout {
		b.WriteString("\n- Retry the same reviewer: a recently timed-out route gets one automatically extended soft timeout on the next reviewer call.")
	} else {
		b.WriteString("\n- Retry the same reviewer: keep the route and rerun the same request.")
	}
	b.WriteString("\n- Change reviewer model: use `/review models` to pick a closer or stronger reviewer.")
	b.WriteString("\n- Use single-model mode: run `/review models clear cross`, rerun, and inspect the diff preview yourself.")
	b.WriteString("\n- Change main model: if `[0] Failed reviewer` shows `primary`, use `/model`.")
	return b.String()
}

func reviewRunHasReviewerTimeoutFailure(run *ReviewRun) bool {
	if run == nil {
		return false
	}
	for _, reviewerRun := range run.ReviewerRuns {
		if strings.Contains(strings.ToLower(strings.TrimSpace(reviewerRun.Error)), "timeout") {
			return true
		}
	}
	for _, health := range run.ModelPlan.RouteHealth {
		if reviewRouteHealthNeedsAdaptiveTimeout(health) {
			return true
		}
	}
	return false
}

func renderReviewBeforeFixInlineFindings(run ReviewRun) string {
	korean := reviewRunPrefersKoreanFromRequest(run)
	text := renderReviewInlineFindingsLocalized(run, false, korean)
	if preFixHasNonConclusiveBugHuntWarning(run) {
		note := "- Pre-fix review returned no actionable bug findings. Inspect the requested code independently before editing; do not treat this as proof that the code is bug-free."
		if korean {
			note = "- 수정 전 리뷰가 실행 가능한 버그 finding을 반환하지 않았습니다. 편집 전에 요청된 코드를 독립적으로 확인하고, 이를 버그가 없다는 증거로 취급하지 마세요."
		}
		if strings.TrimSpace(text) == "" {
			return note
		}
		return strings.TrimSpace(text + "\n" + note)
	}
	return text
}

func renderReviewInlineFindings(run ReviewRun, includeVerificationGaps bool) string {
	return renderReviewInlineFindingsLocalized(run, includeVerificationGaps, false)
}

func renderReviewInlineFindingsLocalized(run ReviewRun, includeVerificationGaps bool, korean bool) string {
	var b strings.Builder
	if len(run.Findings) == 0 {
		if korean {
			if strings.TrimSpace(run.Result.Summary) != "" {
				b.WriteString("- 구조화된 finding이 없습니다. 위 요약을 지침으로 사용하세요.")
			} else {
				b.WriteString("- 구조화된 finding이 없습니다. 참조된 코드를 직접 확인하고 명확히 필요한 수정만 적용하세요.")
			}
			return b.String()
		}
		if strings.TrimSpace(run.Result.Summary) != "" {
			b.WriteString("- No structured findings were returned. Use the summary above as guidance.")
		} else {
			b.WriteString("- No structured findings were returned. Inspect the referenced code and apply only clearly necessary fixes.")
		}
		return b.String()
	}
	for _, finding := range run.Findings {
		finding.Normalize()
		if !includeVerificationGaps && (strings.EqualFold(finding.Category, "test_gap") || strings.EqualFold(finding.Category, "evidence_gap")) {
			continue
		}
		title := valueOrDefault(finding.Title, "Review finding")
		fmt.Fprintf(&b, "- %s [%s/%s] %s\n", valueOrDefault(finding.ID, "finding"), finding.Severity, finding.Category, title)
		if strings.TrimSpace(finding.Path) != "" {
			if korean {
				fmt.Fprintf(&b, "  경로: %s\n", finding.Path)
			} else {
				fmt.Fprintf(&b, "  Path: %s\n", finding.Path)
			}
		}
		if strings.TrimSpace(finding.Symbol) != "" {
			if korean {
				fmt.Fprintf(&b, "  심볼: %s\n", finding.Symbol)
			} else {
				fmt.Fprintf(&b, "  Symbol: %s\n", finding.Symbol)
			}
		}
		if strings.TrimSpace(finding.Evidence) != "" {
			if korean {
				fmt.Fprintf(&b, "  근거: %s\n", finding.Evidence)
			} else {
				fmt.Fprintf(&b, "  Evidence: %s\n", finding.Evidence)
			}
		}
		if strings.TrimSpace(finding.Impact) != "" {
			if korean {
				fmt.Fprintf(&b, "  영향: %s\n", finding.Impact)
			} else {
				fmt.Fprintf(&b, "  Impact: %s\n", finding.Impact)
			}
		}
		if strings.TrimSpace(finding.RequiredFix) != "" {
			if korean {
				fmt.Fprintf(&b, "  필요한 수정: %s\n", localizedReviewRequiredFixText(finding.RequiredFix, true))
			} else {
				fmt.Fprintf(&b, "  Required fix: %s\n", finding.RequiredFix)
			}
		}
		if strings.TrimSpace(finding.TestRecommendation) != "" {
			if korean {
				fmt.Fprintf(&b, "  테스트: %s\n", finding.TestRecommendation)
			} else {
				fmt.Fprintf(&b, "  Test: %s\n", finding.TestRecommendation)
			}
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		if korean {
			b.WriteString("- 리뷰가 검증 또는 근거 gap만 보고했습니다. 요청된 코드를 확인하고, 관련 없는 정리는 피하며 가능하면 focused verification을 실행하세요.")
			return strings.TrimSpace(b.String())
		}
		b.WriteString("- Review only reported verification or evidence gaps. Inspect the requested code, apply no unrelated cleanup, and run focused verification if possible.")
	}
	return strings.TrimSpace(b.String())
}

func preFixHasNonConclusiveBugHuntWarning(run ReviewRun) bool {
	for _, finding := range run.Findings {
		if strings.EqualFold(strings.TrimSpace(finding.Title), "Pre-fix review returned no actionable bug findings") {
			return true
		}
		if strings.TrimSpace(finding.Title) == "수정 전 리뷰가 실행 가능한 버그 finding을 반환하지 않았습니다" {
			return true
		}
	}
	return false
}

func reviewRunPrefersKoreanFromRequest(run ReviewRun) bool {
	return reviewRunPrefersKorean(Config{AutoLocale: boolPtr(false)}, run)
}

func localizedReviewRequiredFixText(value string, korean bool) string {
	trimmed := strings.TrimSpace(value)
	if !korean || trimmed == "" {
		return value
	}
	const applyIfMissingPrefix = "Apply this fix if it is not already present: "
	if strings.HasPrefix(trimmed, applyIfMissingPrefix) {
		return "아직 반영되지 않았다면 이 수정을 적용하세요: " + strings.TrimSpace(trimmed[len(applyIfMissingPrefix):])
	}
	if strings.EqualFold(trimmed, "Apply the reviewer-described fix if it is not already present.") {
		return "아직 반영되지 않았다면 리뷰어가 설명한 수정을 적용하세요."
	}
	return value
}

func (a *Agent) primeTaskStateFromReviewBeforeFix(run ReviewRun) {
	if a == nil || a.Session == nil {
		return
	}
	state := a.Session.EnsureTaskState()
	if strings.TrimSpace(state.Goal) == "" && strings.TrimSpace(run.Objective) != "" {
		state.Goal = strings.TrimSpace(run.Objective)
	}
	state.SetPhase("execution")
	state.SetPlanSummary(strings.Join([]string{
		"1. Apply the pre-fix review findings to the referenced code/change.",
		"2. Use edit tools only for the reviewed repair scope.",
		"3. Run focused verification when possible, then summarize the result.",
	}, "\n"), true)
	state.SetReviewerGuidance(reviewBeforeFixTrigger, compactPromptSection(formatReviewBeforeFixFeedback(run), 1200))
	state.SetNextStep("Apply the pre-fix review findings to the referenced code/change.")
	state.Touch()
}
