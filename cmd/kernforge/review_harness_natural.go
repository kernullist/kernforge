package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

const naturalReviewTrigger = "explicit_natural_language"

func (rt *runtimeState) maybeHandleNaturalLanguageReview(ctx context.Context, input string, images []MessageImage) (bool, error) {
	opts, selection, ok := rt.naturalLanguageReviewOptions(input, images)
	if !ok {
		return false, nil
	}
	if selection != nil {
		rt.rememberNaturalReviewSelection(*selection)
	}
	if rt != nil && rt.writer != nil {
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
	rt.printReviewRun(run)
	return true, nil
}

func (rt *runtimeState) naturalLanguageReviewOptions(input string, images []MessageImage) (ReviewHarnessOptions, *ViewerSelection, bool) {
	request := strings.TrimSpace(input)
	if request == "" || len(images) > 0 || strings.HasPrefix(request, "/") {
		return ReviewHarnessOptions{}, nil, false
	}
	if !hasNaturalReviewIntent(request) || hasNaturalReviewNegation(request) {
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
		MaxContextChars:     60000,
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
	if hasNaturalReviewIntent(lower) && hasRepairActionIntent(lower) {
		return true
	}
	return looksLikeBugFindingFixIntent(lower)
}

func hasRepairActionIntent(lower string) bool {
	return containsAny(lower,
		"수정", "고쳐", "고치", "해결", "패치", "반영",
		"fix", "repair", "patch", "address", "correct")
}

func looksLikeBugFindingFixIntent(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
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
		"리뷰해줘", "리뷰 해줘", "리뷰해봐", "리뷰 해봐", "리뷰 부탁", "리뷰하자", "리뷰 요청",
		"검토해줘", "검토 해줘", "검토해봐", "검토 해봐", "검토 부탁", "검토하자", "검토 요청",
		"코드 리뷰", "변경 리뷰", "변경사항 리뷰", "수정사항 리뷰", "diff 리뷰", "pr 리뷰",
		"please review", "review this", "review current", "review the current", "review change", "review changes",
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
	if strings.TrimSpace(root) != "" && !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	return ViewerSelection{
		FilePath:  path,
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
	path := strings.TrimSpace(raw)
	if strings.TrimSpace(root) != "" && !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	return path, true
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
