package main

import (
	"context"
	"fmt"
	"strings"
)

const reviewBeforeFixTrigger = "pre_fix"

func (a *Agent) maybeRunReviewBeforeFix(ctx context.Context, userText string, images []MessageImage, readOnlyAnalysis bool, explicitEditRequest bool) (bool, error) {
	if a == nil || a.Session == nil || readOnlyAnalysis || !explicitEditRequest || len(images) > 0 {
		return false, nil
	}
	if !looksLikeReviewBeforeFixIntent(userText) {
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
		a.EmitProgress(localizedText(a.Config, "Running review before fix...", "수정 전 리뷰를 실행합니다..."))
	}
	run, err := runReviewHarness(ctx, rt, opts)
	if err != nil {
		if a.EmitProgress != nil {
			a.EmitProgress(localizedText(a.Config, "Review before fix failed: ", "수정 전 리뷰 실패: ") + err.Error())
		}
		return true, fmt.Errorf("review before fix failed: %w", err)
	}
	if summary := formatPreFixVisibleReviewSummary(a.Config, run); summary != "" {
		a.emitPersistentAssistantSummary(summary)
		a.Session.AddMessage(Message{
			Role: "assistant",
			Text: summary,
		})
	}
	if reviewRunHasRequiredReviewerFailure(run) {
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
	a.Session.AddMessage(Message{
		Role: "user",
		Text: formatReviewBeforeFixFeedback(run),
	})
	a.primeTaskStateFromReviewBeforeFix(run)
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
	if !reviewRunHasRequiredReviewerFailure(run) {
		return "", false
	}
	return formatReviewerGateUnavailableReply(a.Config, run), true
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
	maxContextChars := 20000
	if target == reviewTargetSelection || len(paths) > 0 {
		maxContextChars = 60000
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
	a.Session.AddMessage(Message{
		Role: "user",
		Text: formatReviewRepairFollowUpFeedback(*run),
	})
	a.primeTaskStateFromReviewBeforeFix(*run)
	if a.EmitProgress != nil {
		a.EmitProgress(localizedText(a.Config, "Continuing from latest review findings...", "최신 리뷰 결과를 기준으로 수정 흐름을 이어갑니다..."))
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
	if len(run.Evidence.ChangedPaths) == 0 && len(run.ChangeSet.ChangedPaths) == 0 {
		return nil
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
		titles = append(titles, fmt.Sprintf("%s %s: %s", valueOrDefault(finding.ID, "RF"), finding.Severity, compactPromptSection(title, 140)))
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
	korean := textContainsHangul(run.Objective)
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

func formatReviewBeforeFixFeedback(run ReviewRun) string {
	korean := textContainsHangul(run.Objective)
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
	fmt.Fprintf(&b, "\n\nReview gate: %s", valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
	if strings.TrimSpace(run.MachineStatus) != "" {
		fmt.Fprintf(&b, " (%s)", run.MachineStatus)
	}
	if strings.TrimSpace(run.Result.Summary) != "" {
		fmt.Fprintf(&b, "\nSummary: %s", run.Result.Summary)
	}
	if strings.TrimSpace(run.RepairPlan.Prompt) != "" {
		b.WriteString("\n\nRequired repair plan:\n")
		b.WriteString(run.RepairPlan.Prompt)
	} else {
		b.WriteString("\n\nInline review findings:\n")
		b.WriteString(renderReviewBeforeFixInlineFindings(run))
	}
	b.WriteString("\n\nImplementation rules:\n")
	b.WriteString("- Inspect further only where needed.\n")
	b.WriteString("- Fix every blocking finding and every medium-or-higher actionable warning from the pre-fix review.\n")
	b.WriteString("- Do not silently ignore a listed warning; either repair it or explicitly explain why the warning is intentionally out of scope.\n")
	b.WriteString("- Do not broaden scope beyond the reviewed code/change.\n")
	b.WriteString("- Do not read review artifact files; all required review guidance is included here.\n")
	if korean {
		b.WriteString("- 파일 쓰기 또는 패치 도구를 호출하기 전에 사용자에게 `검토 결과:` 섹션으로 RF 항목과 조치 방향을 짧게 요약하세요.\n")
	} else {
		b.WriteString("- Before calling any file write or patch tool, show the user a short `Review findings:` section with the RF items and repair direction.\n")
	}
	b.WriteString("- When using apply_patch, send only valid KernForge patch syntax. The first line after *** Begin Patch must be *** Update File:, *** Add File:, or *** Delete File:. Never start the patch body with @@.\n")
	b.WriteString("- The normal pre-write review gate will run again before any file write.\n")
	return strings.TrimSpace(b.String())
}

func formatReviewerGateUnavailableReply(cfg Config, run ReviewRun) string {
	korean := reviewRunPrefersKorean(cfg, run)
	failed := reviewFailedRequiredReviewerRuns(run)
	var b strings.Builder
	if korean {
		b.WriteString("쓰기 전 리뷰어 게이트가 충분한 근거를 만들지 못해서 편집을 중단했습니다.")
		b.WriteString("\n\n- 원인: 필수 리뷰어 모델이 실패했거나 `weak` 품질로 판정되었습니다.")
		b.WriteString("\n- 결과: 이 상태의 리뷰 결과는 쓰기 승인으로 신뢰할 수 없어 편집을 적용하지 않습니다.")
		b.WriteString("\n- 다음 조치: 리뷰 모델 라우팅을 정상 동작하는 모델로 바꾸거나 같은 요청을 다시 실행하세요.")
		b.WriteString("\n\n코드 수정은 적용하지 않았습니다.")
	} else {
		b.WriteString("The pre-write reviewer gate did not produce enough reliable evidence, so I stopped the edit.")
		b.WriteString("\n\n- Cause: the required reviewer model failed or was classified as `weak` quality.")
		b.WriteString("\n- Result: this review cannot be trusted as write approval, so the edit was not applied.")
		b.WriteString("\n- Next step: switch the reviewer route to a working model or rerun the same request.")
		b.WriteString("\n\nNo code changes were applied.")
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

func renderReviewBeforeFixInlineFindings(run ReviewRun) string {
	text := renderReviewInlineFindings(run, false)
	if preFixHasNonConclusiveBugHuntWarning(run) {
		note := "- Pre-fix review returned no actionable bug findings. Inspect the requested code independently before editing; do not treat this as proof that the code is bug-free."
		if strings.TrimSpace(text) == "" {
			return note
		}
		return strings.TrimSpace(text + "\n" + note)
	}
	return text
}

func renderReviewInlineFindings(run ReviewRun, includeVerificationGaps bool) string {
	var b strings.Builder
	if len(run.Findings) == 0 {
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
			fmt.Fprintf(&b, "  Path: %s\n", finding.Path)
		}
		if strings.TrimSpace(finding.Symbol) != "" {
			fmt.Fprintf(&b, "  Symbol: %s\n", finding.Symbol)
		}
		if strings.TrimSpace(finding.Evidence) != "" {
			fmt.Fprintf(&b, "  Evidence: %s\n", finding.Evidence)
		}
		if strings.TrimSpace(finding.Impact) != "" {
			fmt.Fprintf(&b, "  Impact: %s\n", finding.Impact)
		}
		if strings.TrimSpace(finding.RequiredFix) != "" {
			fmt.Fprintf(&b, "  Required fix: %s\n", finding.RequiredFix)
		}
		if strings.TrimSpace(finding.TestRecommendation) != "" {
			fmt.Fprintf(&b, "  Test: %s\n", finding.TestRecommendation)
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		b.WriteString("- Review only reported verification or evidence gaps. Inspect the requested code, apply no unrelated cleanup, and run focused verification if possible.")
	}
	return strings.TrimSpace(b.String())
}

func preFixHasNonConclusiveBugHuntWarning(run ReviewRun) bool {
	for _, finding := range run.Findings {
		if strings.EqualFold(strings.TrimSpace(finding.Title), "Pre-fix review returned no actionable bug findings") {
			return true
		}
	}
	return false
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
