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
		a.EmitProgress(localizedText(a.Config, "Review before fix completed.", "수정 전 리뷰가 완료되었습니다."))
	}
	return true, nil
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
	return ReviewHarnessOptions{
		Trigger:             reviewBeforeFixTrigger,
		Target:              target,
		Request:             request,
		Paths:               paths,
		IncludeGitDiff:      includeGitDiff,
		IncludeFileContents: includeFileContents,
		AutoTriggered:       true,
		AutoFollowUp:        "none",
		MaxContextChars:     20000,
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
	b.WriteString("- Fix the reviewed issue directly with edit tools when a concrete fix is required.\n")
	b.WriteString("- Do not broaden scope beyond the reviewed code/change.\n")
	b.WriteString("- Do not read review artifact files; all required review guidance is included here.\n")
	b.WriteString("- When using apply_patch, send only valid KernForge patch syntax. The first line after *** Begin Patch must be *** Update File:, *** Add File:, or *** Delete File:. Never start the patch body with @@.\n")
	b.WriteString("- The normal pre-write review gate will run again before any file write.\n")
	return strings.TrimSpace(b.String())
}

func renderReviewBeforeFixInlineFindings(run ReviewRun) string {
	return renderReviewInlineFindings(run, false)
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
