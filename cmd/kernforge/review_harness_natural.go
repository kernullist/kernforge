package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

const naturalReviewTrigger = "explicit_natural_language"

func (rt *runtimeState) maybeHandleNaturalLanguageReview(ctx context.Context, input string, images []MessageImage) (bool, error) {
	if rt == nil {
		return false, nil
	}
	opts, selection, ok := rt.naturalLanguageReviewOptions(input, images)
	if !ok {
		return false, nil
	}
	if selection != nil {
		rt.rememberNaturalReviewSelection(*selection)
	}
	if rt.writer != nil {
		target := strings.TrimSpace(opts.Target)
		command := "/review"
		if target != "" && target != reviewTargetAuto {
			command += " " + target
		}
		message := localizedText(rt.cfg,
			"Routing review request through "+command+".",
			"리뷰 요청을 "+command+" 흐름으로 라우팅합니다.")
		fmt.Fprintln(rt.writer, rt.ui.infoLine(message))
	}
	run, err := rt.runReviewCommandWithContext(ctx, opts)
	if err != nil {
		return true, err
	}
	if shouldRenderCodexAppReviewModeReply(input) {
		rt.printReviewModeRun(run)
	} else {
		rt.printReviewRun(run)
	}
	return true, nil
}

func (rt *runtimeState) printReviewModeRun(run ReviewRun) {
	if rt == nil || rt.writer == nil {
		return
	}
	fmt.Fprintln(rt.writer, rt.ui.section(reviewRunLocalizedText(rt.cfg, run, "Review Mode", "리뷰 모드")))
	rendered := formatCodexAppReviewModeReply(rt.cfg, run)
	switch run.Gate.Verdict {
	case reviewVerdictApproved:
		fmt.Fprintln(rt.writer, rt.ui.successLine(rendered))
	case reviewVerdictApprovedWithWarnings, reviewVerdictNeedsRevision, reviewVerdictBlocked, reviewVerdictInsufficientEvidence:
		fmt.Fprintln(rt.writer, rt.ui.warnLine(rendered))
	default:
		fmt.Fprintln(rt.writer, rt.ui.errorLine(rendered))
	}
}

func (a *Agent) maybeRunCodexAppReviewMode(ctx context.Context, userText string, images []MessageImage) (bool, string, error) {
	if a == nil || a.Session == nil {
		return false, "", nil
	}
	if !looksLikeReviewOnlyModeIntent(userText) {
		return false, "", nil
	}
	root := workspaceSnapshotRoot(a.Workspace)
	if strings.TrimSpace(root) == "" {
		root = firstNonBlankString(a.Workspace.Root, a.Session.WorkingDir)
	}
	rt := a.reviewHarnessRuntime(root)
	opts, selection, ok := rt.naturalLanguageReviewOptions(userText, images)
	if !ok {
		return false, "", nil
	}
	if selection != nil {
		rt.rememberNaturalReviewSelection(*selection)
	}
	run, err := rt.runReviewCommandWithContext(ctx, opts)
	if err != nil {
		return true, "", err
	}
	return true, formatCodexAppReviewModeReply(a.Config, run), nil
}

func formatCodexAppReviewModeReply(cfg Config, run ReviewRun) string {
	korean := reviewRunPrefersKorean(cfg, run)
	findings := reviewModeVisibleFindings(run, 10)
	var b strings.Builder
	if korean {
		b.WriteString("검토 결과:")
		if len(findings) == 0 {
			if strings.EqualFold(run.Gate.Verdict, reviewVerdictApproved) {
				b.WriteString("\n\n- 차단 finding 없음.")
			} else {
				b.WriteString("\n\n- 구조화된 finding 없음.")
			}
		} else {
			for _, finding := range findings {
				writeCodexAppReviewModeFinding(&b, finding, true)
			}
		}
		b.WriteString("\n\n요약:")
		fmt.Fprintf(&b, "\n- 판정: %s", firstNonBlankString(run.Gate.Verdict, run.Result.Verdict, "unknown"))
		fmt.Fprintf(&b, "\n- 차단: %d개", len(run.Gate.BlockingFindings))
		fmt.Fprintf(&b, "\n- 경고: %d개", len(run.Gate.WarningFindings))
		if strings.TrimSpace(run.Result.Summary) != "" {
			fmt.Fprintf(&b, "\n- 리뷰 요약: %s", reviewVisibleInlineText(run.Result.Summary))
		}
		if len(findings) == 0 && !strings.EqualFold(run.Gate.Verdict, reviewVerdictApproved) {
			b.WriteString("\n- 남은 리스크: evidence가 부족할 수 있으니 대상 범위를 좁혀 다시 리뷰하는 편이 안전합니다.")
		}
		return strings.TrimSpace(b.String())
	}
	b.WriteString("Review findings:")
	if len(findings) == 0 {
		if strings.EqualFold(run.Gate.Verdict, reviewVerdictApproved) {
			b.WriteString("\n\n- No blocking findings.")
		} else {
			b.WriteString("\n\n- No structured findings.")
		}
	} else {
		for _, finding := range findings {
			writeCodexAppReviewModeFinding(&b, finding, false)
		}
	}
	b.WriteString("\n\nSummary:")
	fmt.Fprintf(&b, "\n- Verdict: %s", firstNonBlankString(run.Gate.Verdict, run.Result.Verdict, "unknown"))
	fmt.Fprintf(&b, "\n- Blockers: %d", len(run.Gate.BlockingFindings))
	fmt.Fprintf(&b, "\n- Warnings: %d", len(run.Gate.WarningFindings))
	if strings.TrimSpace(run.Result.Summary) != "" {
		fmt.Fprintf(&b, "\n- Review summary: %s", reviewVisibleInlineText(run.Result.Summary))
	}
	if len(findings) == 0 && !strings.EqualFold(run.Gate.Verdict, reviewVerdictApproved) {
		b.WriteString("\n- Residual risk: review evidence may be incomplete; rerun with a narrower target before relying on it.")
	}
	return strings.TrimSpace(b.String())
}

func reviewModeVisibleFindings(run ReviewRun, limit int) []ReviewFinding {
	findings := make([]ReviewFinding, 0, len(run.Findings))
	for _, finding := range run.Findings {
		finding.Normalize()
		if strings.TrimSpace(finding.Title) == "" &&
			strings.TrimSpace(finding.Evidence) == "" &&
			strings.TrimSpace(finding.Impact) == "" {
			continue
		}
		findings = append(findings, finding)
	}
	sortReviewFindings(findings)
	return limitReviewFindings(findings, limit)
}

func writeCodexAppReviewModeFinding(b *strings.Builder, finding ReviewFinding, korean bool) {
	finding.Normalize()
	id := valueOrDefault(finding.ID, "RF")
	severity := valueOrDefault(finding.Severity, "unknown")
	category := valueOrDefault(finding.Category, "general")
	title := reviewVisibleInlineText(firstNonBlankString(finding.Title, finding.Evidence, finding.Impact, "Review finding"))
	fmt.Fprintf(b, "\n\n- %s [%s/%s]: %s", id, severity, category, title)
	if location := codexAppReviewModeFindingLocation(finding, korean); location != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 위치: %s", location)
		} else {
			fmt.Fprintf(b, "\n  - Location: %s", location)
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

func codexAppReviewModeFindingLocation(finding ReviewFinding, korean bool) string {
	var parts []string
	if strings.TrimSpace(finding.Path) != "" {
		path := reviewVisibleInlineText(finding.Path)
		if finding.Line > 0 {
			path = fmt.Sprintf("%s:%d", path, finding.Line)
		}
		parts = append(parts, path)
	}
	if strings.TrimSpace(finding.Symbol) != "" {
		if korean {
			parts = append(parts, "심볼 "+reviewVisibleInlineText(finding.Symbol))
		} else {
			parts = append(parts, "symbol "+reviewVisibleInlineText(finding.Symbol))
		}
	}
	return strings.Join(parts, ", ")
}

func (rt *runtimeState) naturalLanguageReviewOptions(input string, images []MessageImage) (ReviewHarnessOptions, *ViewerSelection, bool) {
	request := strings.TrimSpace(input)
	if request == "" || len(images) > 0 || strings.HasPrefix(request, "/") {
		return ReviewHarnessOptions{}, nil, false
	}
	if !hasNaturalReviewIntent(request) || hasNaturalReviewNegation(request) {
		return ReviewHarnessOptions{}, nil, false
	}
	if looksLikeReviewArtifactAuthoringRequest(request) {
		return ReviewHarnessOptions{}, nil, false
	}
	if looksLikeReviewBeforeFixIntent(request) {
		return ReviewHarnessOptions{}, nil, false
	}
	root := ""
	if rt != nil {
		root = strings.TrimSpace(rt.workspace.Root)
	}
	selection, hasSelectionMention := firstReviewMentionSelection(root, request)
	mentionPath, hasPathMention := firstReviewMentionPath(root, request)
	if reviewRequestHasPathLikeMentionToken(request) && !hasSelectionMention && !hasPathMention {
		return ReviewHarnessOptions{}, nil, false
	}
	hasActiveSelection := false
	if rt != nil && rt.session != nil {
		if current := rt.session.CurrentSelection(); current != nil && current.HasSelection() {
			hasActiveSelection = true
		}
	}
	target := reviewTargetAuto
	includeFileContents := false
	var paths []string
	if hasSelectionMention || (hasActiveSelection && looksSelectionScopedReviewRequest(request)) {
		target = reviewTargetSelection
		if hasSelectionMention && selection != nil {
			paths = []string{selection.FilePath}
		}
	} else if hasPathMention {
		target = reviewTargetChange
		paths = []string{mentionPath}
		includeFileContents = true
	} else if !looksGeneralReviewCommandRequest(request) {
		return ReviewHarnessOptions{}, nil, false
	}
	opts := ReviewHarnessOptions{
		Trigger:             naturalReviewTrigger,
		Target:              target,
		Request:             request,
		Paths:               paths,
		IncludeGitDiff:      !includeFileContents,
		IncludeFileContents: includeFileContents,
		MaxContextChars:     reviewDefaultMaxContextChars,
		RawArgs:             request,
	}
	return opts, selection, true
}

func (rt *runtimeState) rememberNaturalReviewSelection(selection ViewerSelection) {
	if rt == nil || rt.session == nil || !selection.HasSelection() {
		return
	}
	rt.session.AddSelection(selection)
	if rt.store != nil {
		_ = rt.store.Save(rt.session)
	}
	if strings.TrimSpace(rt.workspace.Root) != "" {
		_ = SyncWorkspaceSelections(rt.workspace.Root, rt.session.Selections)
	}
}

func hasNaturalReviewIntent(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	return containsAny(lower,
		"리뷰", "검토", "검수", "코드리뷰", "코드 리뷰",
		"review", "code review", "audit", "inspect")
}

func hasTurnReviewIntent(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	if containsAny(lower,
		"리뷰", "검토", "검수", "코드리뷰", "코드 리뷰",
		"review", "code review", "audit") {
		return true
	}
	return containsAny(lower, "inspect") && containsAny(lower,
		"code", "source", "file", "diff", "patch", "change", "changes", "bug", "bugs", "regression", "pr",
		"코드", "소스", "파일", "패치", "변경", "변경사항", "버그", "회귀")
}

func looksLikeReviewInspectionOnlyRequest(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(input)))
	if lower == "" || hasNaturalReviewNegation(lower) || !hasTurnReviewIntent(lower) {
		return false
	}
	if looksLikeReviewArtifactAuthoringRequest(lower) {
		return false
	}
	if looksLikeBugSearchAndFixIntent(lower) {
		return false
	}
	if containsAny(lower,
		"검토하고 수정", "검토 후 수정", "검토해서 수정", "리뷰하고 수정", "리뷰 후 수정", "리뷰해서 수정",
		"review and fix", "review then fix", "review, then fix", "fix after review",
		" and fix", " and repair", " and patch", " and address", " and correct",
	) {
		return false
	}
	if looksLikeReviewSubjectOnlyRepairNoun(lower) {
		return true
	}
	if containsAny(lower,
		"수정해", "수정 해", "수정하", "고쳐", "고치", "패치해", "패치 해", "패치하", "반영해", "반영 해", "반영하", "해결해", "해결 해", "해결하",
		"fix ", "fix this", "fix the", "repair ", "repair this", "repair the", "patch ", "patch this", "patch the", "address ", "address this", "address the", "correct ", "correct this", "correct the",
	) {
		return false
	}
	return true
}

func looksLikeReviewSubjectOnlyRepairNoun(lower string) bool {
	return containsAny(lower,
		"수정사항 리뷰", "수정 사항 리뷰", "수정본 리뷰", "패치 리뷰", "변경사항 리뷰", "변경 사항 리뷰", "diff 리뷰",
		"review the fix", "review this fix", "review my fix", "review our fix",
		"review the patch", "review this patch", "review my patch", "review our patch",
		"review the diff", "review this diff",
		"review the change", "review this change", "review changes", "review the changes",
		"patch review", "diff review",
	)
}

func hasNaturalReviewNegation(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	return containsAny(lower,
		"리뷰 없이", "검토 없이", "리뷰하지 말", "검토하지 말",
		"no review", "without review", "skip review", "don't review", "do not review")
}

func looksLikeReviewBeforeFixIntent(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if hasNaturalReviewNegation(lower) {
		return false
	}
	if hasRepairActionNegation(lower) {
		return false
	}
	if looksLikeReviewArtifactAuthoringRequest(lower) && !hasRepairActionIntent(lower) {
		return false
	}
	if hasNaturalReviewIntent(lower) && hasRepairActionIntent(lower) {
		return true
	}
	return looksLikeBugFindingFixIntent(lower)
}

func looksLikeReviewArtifactAuthoringRequest(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" || !looksLikeDocumentAuthoringIntent(lower) {
		return false
	}
	hasAuditSignal := hasNaturalReviewIntent(lower) || containsAny(lower,
		"analyze", "analyse", "analysis", "audit", "bug", "bugs", "defect", "defects", "inspect", "issue", "issues", "problem", "problems",
		"검토", "리뷰", "분석", "감사", "문제", "문제점", "버그",
	)
	if !hasAuditSignal {
		return false
	}
	return containsAny(lower,
		"code", "file", "files", "source", "source code", "workspace",
		"코드", "소스", "소스코드", "워크스페이스", "파일", "파일들",
		"별도 문서", "문서로", "보고서로",
	)
}

func hasRepairActionIntent(lower string) bool {
	lower = strings.ToLower(strings.TrimSpace(lower))
	if hasRepairActionNegation(lower) {
		return false
	}
	return containsAny(lower,
		"수정", "고쳐", "고치", "해결", "패치", "반영",
		"fix", "repair", "patch", "address", "correct")
}

func hasRepairActionNegation(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	return containsAny(lower,
		"수정하지 말", "수정 하지 말", "수정은 하지", "수정 없이", "수정하지마", "수정 하지마",
		"고치지 말", "고치지는 말", "고치지마", "패치하지 말", "패치 하지 말", "패치 없이",
		"do not edit", "don't edit", "dont edit", "no edit", "no edits", "without editing",
		"do not modify", "don't modify", "dont modify", "without modifying",
		"do not patch", "don't patch", "dont patch", "without patching")
}

func looksLikeExplicitReviewModeIntent(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	return containsAny(lower,
		"리뷰 모드", "리뷰모드", "검토 모드", "검토모드", "코드 리뷰 모드",
		"review mode", "review-mode", "code review mode", "code-review mode",
		"reviewer mode", "reviewer stance", "review stance", "code-review stance")
}

func shouldRenderCodexAppReviewModeReply(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" || hasNaturalReviewNegation(lower) {
		return false
	}
	return looksLikeExplicitReviewModeIntent(lower)
}

func looksLikeReviewOnlyModeIntent(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" || hasNaturalReviewNegation(lower) || !hasNaturalReviewIntent(lower) {
		return false
	}
	if looksLikeReviewArtifactAuthoringRequest(lower) || looksLikeReviewBeforeFixIntent(lower) || looksLikeBugFindingFixIntent(lower) {
		return false
	}
	if hasRepairActionNegation(lower) {
		return true
	}
	if looksLikeExplicitReviewModeIntent(lower) && !hasRepairActionIntent(lower) {
		return true
	}
	return false
}

func looksLikeBugFindingFixIntent(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	if hasRepairActionNegation(lower) {
		return false
	}
	if looksLikeBugSearchAndFixIntent(lower) {
		return true
	}
	hasBugSignal := containsAny(lower,
		"bug", "bugs", "defect", "regression", "wrong", "crash", "failure", "failing", "fails", "broken",
		"버그", "오류", "에러", "문제", "회귀", "잘못", "깨짐", "실패")
	return hasBugSignal && hasRepairActionIntent(lower)
}

func looksLikeBugSearchAndFixIntent(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if hasRepairActionNegation(lower) {
		return false
	}
	return containsAny(lower,
		"find and fix", "find bug and fix", "find bugs and fix", "find the bug and fix", "find defects and fix",
		"찾아서 수정", "찾아 수정", "찾고 수정", "찾아서 고쳐", "찾아 고쳐", "찾고 고쳐",
		"찾고 고치", "찾아서 고치", "찾아 고치")
}

func looksLikeFocusedRepairRequest(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if !looksLikeBugFindingFixIntent(lower) {
		return false
	}
	return strings.Contains(lower, "@") || containsAny(lower,
		"선택", "선택한", "이 코드", "이 부분", "이 함수", "이 파일", "해당 코드",
		"selection", "selected", "this code", "this function", "this file")
}

func looksSelectionScopedReviewRequest(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if containsAny(lower,
		"선택", "선택한", "이 코드", "이 부분", "이 함수", "이 파일", "코드",
		"selection", "selected", "this code", "this function", "this file") {
		return true
	}
	return looksGeneralReviewCommandRequest(input)
}

func looksGeneralReviewCommandRequest(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	return containsAny(lower,
		"리뷰 모드", "리뷰모드", "검토 모드", "검토모드", "코드 리뷰 모드",
		"리뷰해줘", "리뷰 해줘", "리뷰해봐", "리뷰 해봐", "리뷰 부탁", "리뷰하자", "리뷰 요청",
		"검토해", "검토 해", "검토해줘", "검토 해줘", "검토해봐", "검토 해봐", "검토 부탁", "검토하자", "검토 요청",
		"코드 리뷰", "변경 리뷰", "변경사항 리뷰", "수정사항 리뷰", "diff 리뷰", "pr 리뷰",
		"please review", "review this", "review current", "review the current", "review change", "review changes",
		"review mode", "review-mode", "code review mode", "reviewer stance", "review stance",
		"code review", "run review", "audit this", "inspect this", "review selection", "review selected")
}

func firstReviewMentionSelection(root string, input string) (*ViewerSelection, bool) {
	for _, token := range strings.Fields(input) {
		if selection, ok := parseReviewMentionSelection(root, token); ok {
			return &selection, true
		}
	}
	return nil, false
}

func firstReviewMentionPath(root string, input string) (string, bool) {
	for _, token := range strings.Fields(input) {
		if path, ok := parseReviewMentionPath(root, token); ok {
			return path, true
		}
	}
	return "", false
}

func reviewRequestHasPathLikeMentionToken(input string) bool {
	for _, token := range strings.Fields(input) {
		raw := strings.TrimSpace(token)
		raw = strings.Trim(raw, " \t\r\n\"'`<>.,;()[]{}")
		if !strings.HasPrefix(raw, "@") {
			continue
		}
		mention := strings.TrimPrefix(raw, "@")
		if mention == "" {
			continue
		}
		if len(mentionRangePattern.FindStringSubmatch(mention)) == 4 || looksLikeReviewMentionPath(mention) {
			return true
		}
	}
	return false
}

func parseReviewMentionSelection(root string, token string) (ViewerSelection, bool) {
	raw := strings.TrimSpace(token)
	raw = strings.Trim(raw, " \t\r\n\"'`<>.,;()[]{}")
	raw = strings.TrimPrefix(raw, "@")
	if raw == "" {
		return ViewerSelection{}, false
	}
	match := mentionRangePattern.FindStringSubmatch(raw)
	if len(match) != 4 {
		return ViewerSelection{}, false
	}
	path := strings.TrimSpace(match[1])
	start, err := parsePositiveInt(match[2])
	if err != nil || start <= 0 {
		return ViewerSelection{}, false
	}
	end := start
	if strings.TrimSpace(match[3]) != "" {
		end, err = parsePositiveInt(match[3])
		if err != nil || end < start {
			return ViewerSelection{}, false
		}
	}
	if path == "" {
		return ViewerSelection{}, false
	}
	resolvedPath, ok := resolveReviewMentionPathWithinRoot(root, path)
	if !ok {
		return ViewerSelection{}, false
	}
	return ViewerSelection{
		FilePath:  resolvedPath,
		StartLine: start,
		EndLine:   end,
	}, true
}

func parseReviewMentionPath(root string, token string) (string, bool) {
	raw := strings.TrimSpace(token)
	raw = strings.Trim(raw, " \t\r\n\"'`<>.,;()[]{}")
	if !strings.HasPrefix(raw, "@") {
		return "", false
	}
	raw = strings.TrimPrefix(raw, "@")
	if raw == "" {
		return "", false
	}
	if selection, ok := parseReviewMentionSelection(root, "@"+raw); ok {
		return selection.FilePath, true
	}
	if !looksLikeReviewMentionPath(raw) {
		return "", false
	}
	path, ok := resolveReviewMentionPathWithinRoot(root, raw)
	if !ok {
		return "", false
	}
	return path, true
}

func resolveReviewMentionPathWithinRoot(root string, rawPath string) (string, bool) {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return "", false
	}
	path = filepath.Clean(path)
	if path == "." {
		return "", false
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return path, true
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	resolved, err := ensurePathWithinRoot(rawPath, root, path)
	if err != nil {
		return "", false
	}
	return resolved, true
}

func looksLikeReviewMentionPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || strings.ContainsAny(path, "\r\n\t") {
		return false
	}
	if strings.Contains(path, "/") || strings.Contains(path, "\\") {
		return true
	}
	return filepath.Ext(path) != ""
}
