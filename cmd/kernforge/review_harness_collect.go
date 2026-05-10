package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func analyzeReviewRequest(rt *runtimeState, root string, opts ReviewHarnessOptions) ReviewRequestAnalysis {
	request := strings.TrimSpace(opts.Request)
	target := normalizeReviewTarget(opts.Target)
	if target == reviewTargetAuto {
		target = inferReviewTarget(rt, root, request)
	}
	mode := normalizeReviewMode(opts.Mode)
	if mode == reviewModeGeneralChange {
		mode = inferReviewMode(request, opts.Paths, target, rt)
	}
	flow := strings.TrimSpace(opts.Flow)
	if flow == "" {
		flow = reviewFlowForTargetMode(target, mode)
	}
	packs := reviewPolicyPacksFor(target, mode, append([]string(nil), opts.Paths...), request)
	confidence := 0.78
	reason := "selected from request text and workspace state"
	var warnings []string
	if strings.TrimSpace(opts.Target) == "" && strings.TrimSpace(request) == "" {
		confidence = 0.64
		reason = "selected from workspace state because no explicit review target was provided"
		warnings = append(warnings, "review target inferred from current session/workspace state")
	}
	if target == reviewTargetAuto {
		target = reviewTargetChange
		confidence = 0.45
		warnings = append(warnings, "no strong target signal found; defaulted to change review")
	}
	return ReviewRequestAnalysis{
		OriginalRequest:   request,
		InferredTarget:    target,
		InferredMode:      mode,
		SelectedFlow:      flow,
		Confidence:        confidence,
		EvidenceNeeds:     reviewEvidenceNeeds(target, mode),
		PolicyPacks:       packs,
		CandidateFlows:    reviewCandidateFlows(target, mode),
		Reason:            reason,
		AmbiguityWarnings: warnings,
	}
}

func inferReviewTarget(rt *runtimeState, root string, request string) string {
	lower := strings.ToLower(strings.TrimSpace(request))
	if containsAny(lower, "plan", "design", "architecture", "설계", "계획") {
		return reviewTargetPlan
	}
	if containsAny(lower, "pr", "pull request", "merge request") {
		return reviewTargetPR
	}
	if containsAny(lower, "final answer", "최종 답변") {
		return reviewTargetFinal
	}
	if containsAny(lower, "analysis", "report", "root cause", "분석", "보고서") {
		return reviewTargetAnalysis
	}
	if rt != nil && rt.session != nil {
		if selection := rt.session.CurrentSelection(); selection != nil && selection.HasSelection() {
			return reviewTargetSelection
		}
		if _, ok := rt.session.ActiveGoal(); ok {
			return reviewTargetGoal
		}
	}
	if len(delegationChangedFiles(root)) > 0 {
		return reviewTargetChange
	}
	return reviewTargetAuto
}

func inferReviewMode(request string, paths []string, target string, rt *runtimeState) string {
	text := strings.ToLower(strings.TrimSpace(request + " " + strings.Join(paths, " ")))
	if containsAny(text, "security", "threat", "bypass", "exploit", "kernel", ".sys", "ioctl", "irql", "anti-cheat", "anticheat", "false positive", "오탐", "보안", "커널", "우회") {
		return reviewModeSecurityHardening
	}
	if containsAny(text,
		"createservice", "createservicew", "startservice", "startservicew", "openscmanager", "openscmanagerw",
		"sc_manager", "service_control_manager", "service install", "service start", "service creation",
		"서비스 설치", "서비스 시작", "서비스 생성", "서비스 등록", "서비스 제어 관리자") {
		return reviewModeSecurityHardening
	}
	if containsAny(text, "refactor", "rename", "cleanup", "리팩터") {
		return reviewModeRefactor
	}
	if containsAny(text, "research", "poc", "조사", "연구") {
		return reviewModeResearch
	}
	if containsAny(text, "ui", "layout", "visual", "accessibility") {
		return reviewModeUIPolish
	}
	if target == reviewTargetPlan && containsAny(text, "core", "architecture", "핵심", "설계") {
		return reviewModeCoreBuild
	}
	if target == reviewTargetPR || containsAny(text, "bug", "regression", "fix", "회귀", "버그") {
		return reviewModeLiveFix
	}
	if rt != nil && rt.session != nil && rt.session.AcceptanceContract != nil {
		mode := strings.ToLower(strings.TrimSpace(rt.session.AcceptanceContract.Mode))
		if strings.Contains(mode, "edit") && containsAny(text, "driver", "kernel", "security") {
			return reviewModeSecurityHardening
		}
	}
	return reviewModeGeneralChange
}

func reviewFlowForTargetMode(target string, mode string) string {
	if mode == reviewModeSecurityHardening {
		return "security_review"
	}
	switch target {
	case reviewTargetPlan:
		return "plan_review"
	case reviewTargetSelection:
		return "selection_review"
	case reviewTargetPR:
		return "pr_review"
	case reviewTargetGoal:
		return "goal_review"
	case reviewTargetAnalysis:
		return "analysis_review"
	case reviewTargetFinal:
		return "final_answer_review"
	default:
		return "change_review"
	}
}

func reviewPolicyPacksFor(target string, mode string, paths []string, request string) []string {
	packs := []string{"base_correctness", "base_security", "base_stability", "base_maintainability", "base_testability"}
	text := strings.ToLower(strings.Join(append(paths, request), " "))
	if mode == reviewModeSecurityHardening || containsAny(text, ".sys", "ioctl", "irql", "kernel", "커널") {
		packs = append(packs, "windows_kernel_driver")
	}
	if containsAny(text, "anti-cheat", "anticheat", "telemetry", "etw", "false positive", "오탐") {
		packs = append(packs, "anti_cheat_telemetry")
	}
	if containsAny(text, "memory", "scan", "vad", "page table", "메모리") {
		packs = append(packs, "memory_scan")
	}
	if containsAny(text, "unreal", "ue5", "uobject") {
		packs = append(packs, "unreal_integrity")
	}
	if containsAny(text, "mcp", "tool") {
		packs = append(packs, "mcp_tooling")
	}
	if target == reviewTargetAnalysis {
		packs = append(packs, "docs_artifact")
	}
	return analysisUniqueStrings(packs)
}

func reviewEvidenceNeeds(target string, mode string) []string {
	needs := []string{"target metadata", "changed paths", "diff or evidence excerpt"}
	if target == reviewTargetPlan {
		needs = []string{"plan text", "objective", "non-goals", "required verification"}
	}
	if mode == reviewModeSecurityHardening {
		needs = append(needs, "security-sensitive changed paths", "verification or smoke evidence", "false-positive rationale")
	}
	return needs
}

func reviewCandidateFlows(target string, mode string) []string {
	out := []string{reviewFlowForTargetMode(target, mode)}
	if mode != reviewModeSecurityHardening {
		out = append(out, "security_review")
	}
	if target != reviewTargetChange {
		out = append(out, "change_review")
	}
	return analysisUniqueStrings(out)
}

func collectReviewEvidence(ctx context.Context, rt *runtimeState, root string, run ReviewRun, opts ReviewHarnessOptions) (ReviewChangeSet, ReviewEvidencePack) {
	var changeSet ReviewChangeSet
	var evidence ReviewEvidencePack
	evidence.Sources = []string{}
	evidence.Warnings = append(evidence.Warnings, run.RequestAnalysis.AmbiguityWarnings...)
	if strings.TrimSpace(opts.ProvidedDiff) != "" {
		changeSet.Source = "provided_diff"
		changeSet.DiffExcerpt = compactPromptSection(opts.ProvidedDiff, opts.MaxContextChars)
		changeSet.ChangedPaths = append(changeSet.ChangedPaths, normalizeTaskStateList(opts.Paths, 128)...)
		evidence.ChangedPaths = append(evidence.ChangedPaths, changeSet.ChangedPaths...)
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Provided diff", changeSet.DiffExcerpt)
		evidence.Sources = append(evidence.Sources, "provided_diff")
	}
	if strings.TrimSpace(opts.ProvidedCode) != "" {
		if changeSet.Source == "" {
			changeSet.Source = "provided_code"
		}
		code := compactPromptSection(opts.ProvidedCode, opts.MaxContextChars-len(evidence.Text))
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Provided code", code)
		evidence.Sources = append(evidence.Sources, "provided_code")
	}
	switch run.Target {
	case reviewTargetPlan:
		collectPlanReviewEvidence(rt, &evidence, opts)
	case reviewTargetSelection:
		collectSelectionReviewEvidence(rt, root, &changeSet, &evidence, opts.MaxContextChars-len(evidence.Text))
	case reviewTargetPR:
		collectGitReviewEvidence(ctx, root, opts.Paths, &changeSet, &evidence, opts)
	case reviewTargetGoal:
		collectGoalReviewEvidence(rt, root, &changeSet, &evidence, opts)
	case reviewTargetAnalysis:
		collectAnalysisReviewEvidence(rt, root, &changeSet, &evidence, opts)
	default:
		if opts.IncludeGitDiff || (changeSet.Source == "" && strings.TrimSpace(evidence.Text) == "") {
			collectGitReviewEvidence(ctx, root, opts.Paths, &changeSet, &evidence, opts)
		}
	}
	if opts.IncludeFileContents || (strings.TrimSpace(evidence.Text) == "" && len(opts.Paths) > 0) {
		collectFileReviewEvidence(rt, root, opts.Paths, &changeSet, &evidence, opts.MaxContextChars-len(evidence.Text))
	}
	if rt != nil && rt.session != nil {
		collectSessionReviewEvidence(rt.session, &evidence)
	}
	changeSet.ChangedPaths = analysisUniqueStrings(append(changeSet.ChangedPaths, evidence.ChangedPaths...))
	sort.Strings(changeSet.ChangedPaths)
	evidence.ChangedPaths = append([]string(nil), changeSet.ChangedPaths...)
	if changeSet.Source == "" {
		changeSet.Source = "session"
	}
	changeSet.Fingerprint = computeReviewFingerprint(changeSet.Source, strings.Join(changeSet.ChangedPaths, ","), changeSet.DiffStat, changeSet.DiffExcerpt, evidence.Text)
	evidence.Sources = analysisUniqueStrings(evidence.Sources)
	evidence.Warnings = analysisUniqueStrings(evidence.Warnings)
	return changeSet, evidence
}

func collectPlanReviewEvidence(rt *runtimeState, evidence *ReviewEvidencePack, opts ReviewHarnessOptions) {
	plan := strings.TrimSpace(opts.Request)
	if plan == "" && rt != nil && rt.session != nil && len(rt.session.Plan) > 0 {
		var lines []string
		for _, item := range rt.session.Plan {
			lines = append(lines, fmt.Sprintf("- [%s] %s", item.Status, item.Step))
		}
		plan = strings.Join(lines, "\n")
	}
	if plan != "" {
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Plan under review", plan)
		evidence.Sources = append(evidence.Sources, "plan_text")
	}
}

func collectSelectionReviewEvidence(rt *runtimeState, root string, changeSet *ReviewChangeSet, evidence *ReviewEvidencePack, maxChars int) {
	if rt == nil || rt.session == nil {
		return
	}
	selection := rt.session.CurrentSelection()
	if selection == nil || !selection.HasSelection() {
		evidence.Warnings = append(evidence.Warnings, "no active selection found")
		return
	}
	excerpt, truncated, err := loadSelectionReviewExcerpt(root, *selection, maxChars)
	rel := selection.RelativePrompt(root)
	if err != nil {
		evidence.Warnings = append(evidence.Warnings, "selection excerpt unavailable: "+err.Error())
		return
	}
	if truncated {
		evidence.Warnings = append(evidence.Warnings, "selection excerpt truncated by review context budget")
	}
	changeSet.Source = "selection"
	changeSet.ChangedPaths = append(changeSet.ChangedPaths, filepath.ToSlash(relOrAbs(root, selection.FilePath)))
	evidence.ChangedPaths = append(evidence.ChangedPaths, changeSet.ChangedPaths...)
	evidence.Text = appendReviewEvidenceSection(evidence.Text, "Selection "+rel, excerpt)
	evidence.Sources = append(evidence.Sources, "selection")
}

func loadSelectionReviewExcerpt(root string, selection ViewerSelection, maxChars int) (string, bool, error) {
	if !selection.HasSelection() {
		return "", false, fmt.Errorf("no selection")
	}
	path := selection.FilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	content := sliceLines(string(data), selection.StartLine, selection.EndLine)
	if maxChars > 0 && len(content) > maxChars {
		return compactPromptSection(content, maxChars), true, nil
	}
	return content, false, nil
}

func collectGoalReviewEvidence(rt *runtimeState, root string, changeSet *ReviewChangeSet, evidence *ReviewEvidencePack, opts ReviewHarnessOptions) {
	if rt == nil || rt.session == nil {
		return
	}
	if goal, ok := rt.session.ActiveGoal(); ok {
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Active goal", goal.Objective)
		evidence.Sources = append(evidence.Sources, "goal")
		if len(goal.Iterations) > 0 {
			iter := goal.Iterations[len(goal.Iterations)-1]
			evidence.Text = appendReviewEvidenceSection(evidence.Text, "Latest goal iteration", renderGoalIterationReviewSummary(iter))
		}
	}
	collectGitReviewEvidence(context.Background(), root, opts.Paths, changeSet, evidence, opts)
}

func collectAnalysisReviewEvidence(rt *runtimeState, root string, changeSet *ReviewChangeSet, evidence *ReviewEvidencePack, opts ReviewHarnessOptions) {
	if rt != nil && rt.session != nil && rt.session.LastAnalysis != nil {
		data, _ := json.MarshalIndent(rt.session.LastAnalysis, "", "  ")
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Latest analysis summary", compactPromptSection(string(data), 8000))
		evidence.Sources = append(evidence.Sources, "analysis_summary")
	}
	if strings.TrimSpace(evidence.Text) == "" {
		evidence.Warnings = append(evidence.Warnings, "no latest analysis summary found")
	}
	collectGitReviewEvidence(context.Background(), root, opts.Paths, changeSet, evidence, opts)
}

func collectGitReviewEvidence(ctx context.Context, root string, paths []string, changeSet *ReviewChangeSet, evidence *ReviewEvidencePack, opts ReviewHarnessOptions) {
	if changeSet.Source == "" {
		changeSet.Source = "git_worktree"
	}
	status := runGitText(root, "status", "--short", "--branch")
	if reviewGitOutputIsUnavailable(status) {
		evidence.Warnings = append(evidence.Warnings, "git status unavailable: "+firstNonEmptyLine(status))
		return
	}
	if strings.TrimSpace(status) != "" {
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Git status", status)
		evidence.Sources = append(evidence.Sources, "git_status")
	}
	changed := parseMCPReviewGitStatusPaths(status)
	untracked := parseMCPReviewUntrackedPaths(status)
	changed = filterReviewablePaths(changed)
	untracked = filterReviewablePaths(untracked)
	if len(paths) > 0 {
		changed = filterMCPReviewPaths(changed, paths)
		untracked = filterMCPReviewPaths(untracked, paths)
	}
	changeSet.ChangedPaths = append(changeSet.ChangedPaths, changed...)
	changeSet.UntrackedPaths = append(changeSet.UntrackedPaths, untracked...)
	pathArgs, err := reviewGitPathArgs(root, paths)
	if err != nil {
		evidence.Warnings = append(evidence.Warnings, err.Error())
		return
	}
	diffStat := runGitText(root, append([]string{"diff", "--stat"}, pathArgs...)...)
	if strings.TrimSpace(diffStat) != "" {
		changeSet.DiffStat = diffStat
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Git diff stat", diffStat)
		evidence.Sources = append(evidence.Sources, "git_diff_stat")
	}
	diff := runGitText(root, append([]string{"diff", "--unified=3", "--no-ext-diff"}, pathArgs...)...)
	staged := runGitText(root, append([]string{"diff", "--staged", "--unified=3", "--no-ext-diff"}, pathArgs...)...)
	combined := strings.TrimSpace(strings.Join([]string{diff, staged}, "\n\n"))
	if combined != "" {
		changeSet.DiffExcerpt = compactPromptSection(combined, opts.MaxContextChars-len(evidence.Text))
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Git diff excerpt", changeSet.DiffExcerpt)
		evidence.Sources = append(evidence.Sources, "git_diff")
	}
	if len(untracked) > 0 {
		collectFileReviewEvidence(nil, root, limitStrings(untracked, 8), changeSet, evidence, opts.MaxContextChars-len(evidence.Text))
	}
	_ = ctx
}

func collectFileReviewEvidence(rt *runtimeState, root string, paths []string, changeSet *ReviewChangeSet, evidence *ReviewEvidencePack, maxChars int) {
	if maxChars <= 0 {
		evidence.Warnings = append(evidence.Warnings, "file review context budget exhausted")
		return
	}
	for _, raw := range mcpReviewCleanPaths(paths) {
		if len(evidence.Text) >= maxChars {
			evidence.Warnings = append(evidence.Warnings, "file excerpts truncated by review context budget")
			return
		}
		if shouldSkipMCPReviewFile(raw) {
			continue
		}
		resolved := raw
		var err error
		if rt != nil {
			resolved, err = rt.workspace.Resolve(raw)
		} else if !filepath.IsAbs(raw) {
			resolved = filepath.Join(root, raw)
		}
		if err != nil {
			evidence.Warnings = append(evidence.Warnings, fmt.Sprintf("skipped %s: %v", raw, err))
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || info.IsDir() || info.Size() > 256*1024 {
			continue
		}
		data, err := os.ReadFile(resolved)
		if err != nil || !isText(data) {
			continue
		}
		rel := filepath.ToSlash(relOrAbs(root, resolved))
		changeSet.ChangedPaths = append(changeSet.ChangedPaths, rel)
		evidence.ChangedPaths = append(evidence.ChangedPaths, rel)
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "File excerpt: "+rel, compactPromptSection(string(data), maxChars-len(evidence.Text)))
		evidence.Sources = append(evidence.Sources, "file_excerpt")
	}
}

func collectSessionReviewEvidence(session *Session, evidence *ReviewEvidencePack) {
	if session == nil || evidence == nil {
		return
	}
	if session.AcceptanceContract != nil {
		contract := *session.AcceptanceContract
		contract.Normalize()
		if contract.VerificationRequired {
			evidence.VerificationRequired = true
		}
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Acceptance contract", compactPromptSection(contract.RenderPromptSection(), 4000))
		evidence.Sources = append(evidence.Sources, "acceptance_contract")
	}
	if session.LastVerification != nil {
		evidence.VerificationSummary = session.LastVerification.SummaryLine()
		evidence.VerificationFailed = session.LastVerification.HasFailures()
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Latest verification", compactPromptSection(session.LastVerification.RenderShort(), 4000))
		evidence.Sources = append(evidence.Sources, "verification")
	}
	if session.LastCodingHarnessReport != nil {
		report := *session.LastCodingHarnessReport
		report.Normalize()
		evidence.CodingHarnessSummary = report.RenderPromptSection()
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Latest coding harness", compactPromptSection(evidence.CodingHarnessSummary, 4000))
		evidence.Sources = append(evidence.Sources, "coding_harness")
	}
}

func appendReviewEvidenceSection(text string, title string, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return text
	}
	section := "## " + strings.TrimSpace(title) + "\n\n```text\n" + body + "\n```"
	if strings.TrimSpace(text) == "" {
		return section
	}
	return strings.TrimSpace(text) + "\n\n" + section
}

func reviewGitPathArgs(root string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out := []string{"--"}
	for _, path := range mcpReviewCleanPaths(paths) {
		resolved := path
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(root, path)
		}
		rel := filepath.ToSlash(relOrAbs(root, resolved))
		if strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("review path is outside workspace: %s", path)
		}
		out = append(out, rel)
	}
	return out, nil
}

func reviewGitOutputIsUnavailable(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(lower, "fatal: not a git repository") ||
		strings.Contains(lower, "not a git repository") ||
		strings.Contains(lower, "outside repository")
}

func filterReviewablePaths(paths []string) []string {
	var out []string
	for _, path := range paths {
		if !shouldSkipMCPReviewFile(path) {
			out = append(out, path)
		}
	}
	return analysisUniqueStrings(out)
}

func renderGoalIterationReviewSummary(iter GoalIteration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "iteration=%d status=%s reviewer_verdict=%s\n", iter.Index, iter.Status, iter.ReviewerVerdict)
	if strings.TrimSpace(iter.ImplementReply) != "" {
		fmt.Fprintf(&b, "implement_reply=%s\n", compactPromptSection(iter.ImplementReply, 800))
	}
	if strings.TrimSpace(iter.ReviewerFeedback) != "" {
		fmt.Fprintf(&b, "reviewer_feedback=%s\n", compactPromptSection(iter.ReviewerFeedback, 800))
	}
	if strings.TrimSpace(iter.Verification) != "" {
		fmt.Fprintf(&b, "verification=%s\n", iter.Verification)
	}
	if len(iter.ChangedFiles) > 0 {
		fmt.Fprintf(&b, "changed_files=%s\n", strings.Join(limitStrings(iter.ChangedFiles, 16), ", "))
	}
	return strings.TrimSpace(b.String())
}
