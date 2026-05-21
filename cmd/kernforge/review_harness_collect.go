package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func analyzeReviewRequest(rt *runtimeState, root string, opts ReviewHarnessOptions) ReviewRequestAnalysis {
	request := strings.TrimSpace(opts.Request)
	target := normalizeReviewTarget(opts.Target)
	scopePaths := append([]string(nil), opts.Paths...)
	for _, proposal := range opts.EditProposals {
		scopePaths = append(scopePaths, proposal.File)
	}
	scopePaths = append(scopePaths, reviewScopeCandidateFilesFromDiff(opts.ProvidedDiff)...)
	discovery := discoverReviewScopeForAnalysis(root, request, opts, scopePaths)
	if target == reviewTargetAuto {
		target = inferReviewTarget(rt, root, request, discovery)
	}
	if (strings.TrimSpace(opts.ProvidedDiff) != "" || strings.TrimSpace(opts.ProvidedCode) != "") &&
		len(discovery.CandidateFiles) == 0 &&
		(strings.EqualFold(discovery.ScopeWidth, "unknown") || strings.EqualFold(discovery.ScopeWidth, "broad")) {
		discovery.ScopeWidth = "focused"
		discovery.Confidence = 0.7
		discovery.Warnings = nil
	}
	domainSignals, riskSignals := reviewScopeSignals(discovery, request)
	mode := normalizeReviewMode(opts.Mode)
	if mode == reviewModeGeneralChange {
		mode = inferReviewMode(request, opts.Paths, target, rt)
	}
	if mode == reviewModeGeneralChange {
		if inferred := inferReviewModeFromScopeDiscovery(discovery, domainSignals, target); inferred != "" {
			mode = inferred
		}
	}
	flow := strings.TrimSpace(opts.Flow)
	if flow == "" {
		flow = reviewFlowForTargetMode(target, mode)
	}
	packs := reviewPolicyPacksFor(target, mode, append([]string(nil), opts.Paths...), request)
	packs = analysisUniqueStrings(append(packs, reviewPolicyPacksForScopeDiscovery(discovery, domainSignals)...))
	confidence := 0.78
	reason := "selected from request text and workspace state"
	var warnings []string
	warnings = append(warnings, discovery.Warnings...)
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
		DomainSignals:     domainSignals,
		RiskSignals:       riskSignals,
		ScopeDiscovery:    discovery,
		Reason:            reason,
		AmbiguityWarnings: warnings,
	}
}

func discoverReviewScopeForAnalysis(root string, request string, opts ReviewHarnessOptions, scopePaths []string) ReviewScopeDiscovery {
	if strings.EqualFold(strings.TrimSpace(opts.Trigger), "pre_write") && len(mcpReviewCleanPaths(scopePaths)) > 0 {
		discovery := discoverReviewScope(root, "", scopePaths)
		if len(discovery.CandidateFiles) > 0 {
			discovery.CandidateSymbols = nil
			discovery.SearchTerms = nil
			discovery.ScopeWidth, discovery.Confidence = reviewScopeWidth(request, discovery.CandidateFiles, nil)
			discovery.NarrowingCommands = reviewScopeNarrowingCommands(discovery.CandidateFiles, nil, nil)
			if !reviewScopeDiscoveryNeedsNarrowing(discovery) {
				discovery.Warnings = nil
			}
			return discovery
		}
	}
	return discoverReviewScope(root, request, scopePaths)
}

func inferReviewTarget(rt *runtimeState, root string, request string, discovery ReviewScopeDiscovery) string {
	lower := strings.ToLower(strings.TrimSpace(request))
	if reviewRequestPrefersSourceEvidence(request, discovery) {
		if prefersReadOnlyAnalysisIntent(request) && !looksLikeExplicitEditIntent(request) {
			return reviewTargetSourceAnalysis
		}
		return reviewTargetChange
	}
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

func reviewRequestPrefersSourceEvidence(request string, discovery ReviewScopeDiscovery) bool {
	lower := strings.ToLower(strings.TrimSpace(request))
	if lower == "" {
		return false
	}
	strongSourceIntent := containsAny(lower,
		"code", "source", "function", "class", "method", "module", "component", "server", "client",
		"performance", "hitch", "hitching", "latency", "stall",
		"코드", "소스", "함수", "클래스", "메서드", "모듈", "컴포넌트", "서버", "클라이언트",
		"성능", "히칭", "렉", "지연")
	reviewIntent := containsAny(lower, "review", "검토", "리뷰")
	if len(discovery.CandidateFiles) > 0 {
		return strongSourceIntent || reviewIntent
	}
	if len(discovery.CandidateSymbols) > 0 && strongSourceIntent {
		return true
	}
	for _, token := range strings.Fields(request) {
		cleaned := strings.Trim(token, " \t\r\n\"'`<>.,;()[]{}")
		if reviewScopeTokenLooksLikePath(cleaned) && (strongSourceIntent || reviewIntent) {
			return true
		}
	}
	return false
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
	if target == reviewTargetSourceAnalysis && containsAny(text,
		"performance", "perf", "hitch", "hitching", "latency", "stall", "frame time", "tick", "lock contention",
		"성능", "히칭", "렉", "지연", "프레임", "틱", "락 경합") {
		return reviewModePerformanceAnalysis
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
	case reviewTargetSourceAnalysis:
		if mode == reviewModePerformanceAnalysis {
			return "performance_source_review"
		}
		return "source_analysis_review"
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
	if target == reviewTargetSourceAnalysis {
		packs = append(packs, "source_analysis")
	}
	if mode == reviewModePerformanceAnalysis {
		packs = append(packs, "performance_hot_path")
	}
	return analysisUniqueStrings(packs)
}

func reviewEvidenceNeeds(target string, mode string) []string {
	needs := []string{"target metadata", "changed paths", "diff or evidence excerpt"}
	if target == reviewTargetPlan {
		needs = []string{"plan text", "objective", "non-goals", "required verification"}
	}
	if target == reviewTargetSourceAnalysis {
		needs = []string{"source excerpts", "symbols or focused files", "performance or stability hypothesis"}
	}
	if mode == reviewModeSecurityHardening {
		needs = append(needs, "security-sensitive changed paths", "verification or smoke evidence", "false-positive rationale")
	}
	return needs
}

func reviewMaxContextCharsForAnalysis(current int, analysis ReviewRequestAnalysis, defaulted bool) int {
	if !defaulted {
		return current
	}
	if !strings.EqualFold(analysis.InferredTarget, reviewTargetSourceAnalysis) {
		return current
	}
	if strings.EqualFold(analysis.InferredMode, reviewModePerformanceAnalysis) {
		return reviewSourceAnalysisMaxContextChars
	}
	return reviewSourceAnalysisMaxContextChars
}

func reviewMaxContextCharsForFastPath(current int, opts ReviewHarnessOptions, analysis ReviewRequestAnalysis, defaulted bool) int {
	if current <= 0 {
		return current
	}
	if strings.EqualFold(strings.TrimSpace(opts.Trigger), "pre_write") {
		return reviewMinPositiveContextChars(current, reviewPreWriteMaxContextChars)
	}
	if !defaulted {
		return current
	}
	if reviewOptionsUseFocusedFastPath(opts, analysis) {
		return reviewMinPositiveContextChars(current, reviewFocusedMaxContextChars)
	}
	return current
}

func reviewRunUsesFocusedFastPath(run ReviewRun) bool {
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		return true
	}
	return reviewAnalysisUsesFocusedFastPath(
		run.Trigger,
		run.Target,
		run.Mode,
		run.Objective,
		run.RequestAnalysis,
		run.ChangeSet.ChangedPaths,
	)
}

func reviewOptionsUseFocusedFastPath(opts ReviewHarnessOptions, analysis ReviewRequestAnalysis) bool {
	paths := append([]string(nil), opts.Paths...)
	paths = append(paths, reviewScopeCandidateFilesFromDiff(opts.ProvidedDiff)...)
	return reviewAnalysisUsesFocusedFastPath(
		opts.Trigger,
		analysis.InferredTarget,
		analysis.InferredMode,
		opts.Request,
		analysis,
		paths,
	)
}

func reviewAnalysisUsesFocusedFastPath(trigger string, target string, mode string, request string, analysis ReviewRequestAnalysis, paths []string) bool {
	if strings.EqualFold(strings.TrimSpace(trigger), "pre_write") {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(target), reviewTargetPR) ||
		strings.EqualFold(strings.TrimSpace(target), reviewTargetGoal) ||
		strings.EqualFold(strings.TrimSpace(target), reviewTargetAnalysis) {
		return false
	}
	hasLineRange := reviewRequestHasLineRange(request)
	if strings.EqualFold(strings.TrimSpace(target), reviewTargetSourceAnalysis) &&
		!hasLineRange &&
		!strings.EqualFold(strings.TrimSpace(mode), reviewModeLiveFix) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(target), reviewTargetSelection) || hasLineRange {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(analysis.ScopeDiscovery.ScopeWidth), "focused") &&
		len(analysis.ScopeDiscovery.CandidateFiles) <= 3 {
		return true
	}
	cleanPaths := mcpReviewCleanPaths(paths)
	return len(cleanPaths) > 0 && len(cleanPaths) <= 2
}

func reviewRequestHasLineRange(request string) bool {
	for _, token := range strings.Fields(request) {
		token = strings.Trim(token, "\"'`,;()[]{}<>")
		index := strings.LastIndex(token, ":")
		if index < 0 || index == len(token)-1 {
			continue
		}
		if reviewLineRangeSpecLooksValid(token[index+1:]) {
			return true
		}
	}
	return false
}

func reviewLineRangeSpecLooksValid(spec string) bool {
	if spec == "" {
		return false
	}
	parts := strings.Split(spec, "-")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func reviewMinPositiveContextChars(current int, cap int) int {
	if current <= 0 || cap <= 0 {
		return current
	}
	if current > cap {
		return cap
	}
	return current
}

func reviewRemainingContextChars(maxChars int, currentText string) int {
	if maxChars <= 0 {
		return 0
	}
	remaining := maxChars - len(currentText)
	if remaining < 0 {
		return 0
	}
	return remaining
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
	focusPaths := reviewEvidenceFocusPaths(run, opts)
	collectedSourceFirst := false
	collectedGitEvidence := false
	isPreWrite := strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write")
	includePreWriteFileContext := reviewPreWriteShouldIncludeFileContext(run, opts, focusPaths)
	evidence.Sources = []string{}
	evidence.Warnings = append(evidence.Warnings, run.RequestAnalysis.AmbiguityWarnings...)
	if strings.TrimSpace(opts.ProvidedDiff) != "" {
		providedPaths := append(normalizeTaskStateList(opts.Paths, 128), reviewScopeCandidateFilesFromDiff(opts.ProvidedDiff)...)
		providedPaths = analysisUniqueStrings(providedPaths)
		changeSet.Source = "provided_diff"
		changeSet.DiffExcerpt = compactPromptSection(opts.ProvidedDiff, reviewProvidedDiffEvidenceBudget(opts, includePreWriteFileContext))
		changeSet.ChangedPaths = append(changeSet.ChangedPaths, providedPaths...)
		evidence.ChangedPaths = append(evidence.ChangedPaths, changeSet.ChangedPaths...)
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Provided diff", changeSet.DiffExcerpt)
		evidence.Sources = append(evidence.Sources, "provided_diff")
	}
	preWriteProposalText := ""
	if isPreWrite {
		preWriteProposalText = renderEditProposalsForEvidenceWithoutAfterExcerpt(run.EditProposals)
		if strings.TrimSpace(preWriteProposalText) != "" {
			evidence.Text = appendReviewEvidenceSection(evidence.Text, "Edit proposal", preWriteProposalText)
			evidence.Sources = append(evidence.Sources, "edit_proposal")
		}
		if repairText := renderReviewRepairFindingsForEvidence(opts.RepairFindings); strings.TrimSpace(repairText) != "" {
			evidence.Text = appendReviewEvidenceSection(evidence.Text, "Required repair findings from pre-fix review", repairText)
			evidence.Sources = append(evidence.Sources, "pre_fix_repair_findings")
		}
	}
	preWriteAfterEvidenceAdded := appendPreWriteRequiredRepairAfterEvidence(run, opts, &evidence, reviewRemainingContextChars(opts.MaxContextChars, evidence.Text))
	appendPreWriteRequiredRepairDiffEvidence(run, opts, &evidence, reviewRemainingContextChars(opts.MaxContextChars, evidence.Text))
	if strings.TrimSpace(opts.ProvidedCode) != "" {
		if changeSet.Source == "" {
			changeSet.Source = "provided_code"
		}
		remaining := reviewRemainingContextChars(opts.MaxContextChars, evidence.Text)
		if remaining <= 0 {
			evidence.Warnings = append(evidence.Warnings, "provided code omitted because review context budget is exhausted")
		} else {
			code := compactPromptSection(opts.ProvidedCode, remaining)
			evidence.Text = appendReviewEvidenceSection(evidence.Text, "Provided code", code)
			evidence.Sources = append(evidence.Sources, "provided_code")
		}
	}
	if includePreWriteFileContext {
		collectPreWriteCurrentFileContextEvidence(rt, root, run, opts, focusPaths, &changeSet, &evidence, reviewRemainingContextChars(opts.MaxContextChars, evidence.Text))
		collectedSourceFirst = true
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) &&
		strings.EqualFold(strings.TrimSpace(run.Target), reviewTargetSelection) {
		if collectPreFixSelectionCurrentFileContextEvidence(rt, root, run, opts, focusPaths, &changeSet, &evidence, reviewRemainingContextChars(opts.MaxContextChars, evidence.Text)) {
			collectedSourceFirst = true
		}
	}
	if !isPreWrite {
		proposalText := renderEditProposalsForEvidence(run.EditProposals)
		if preWriteAfterEvidenceAdded {
			proposalText = renderEditProposalsForEvidenceWithoutAfterExcerpt(run.EditProposals)
		}
		if strings.TrimSpace(proposalText) != "" {
			evidence.Text = appendReviewEvidenceSection(evidence.Text, "Edit proposal", proposalText)
			evidence.Sources = append(evidence.Sources, "edit_proposal")
		}
		if repairText := renderReviewRepairFindingsForEvidence(opts.RepairFindings); strings.TrimSpace(repairText) != "" {
			evidence.Text = appendReviewEvidenceSection(evidence.Text, "Required repair findings from pre-fix review", repairText)
			evidence.Sources = append(evidence.Sources, "pre_fix_repair_findings")
		}
	}
	if reviewShouldCollectGitEvidenceBeforeSource(run, opts, focusPaths) {
		gitOpts := opts
		gitOpts.MaxContextChars = reviewChangeGitEvidenceBudget(opts.MaxContextChars)
		collectGitReviewEvidence(ctx, root, focusPaths, &changeSet, &evidence, gitOpts)
		collectedGitEvidence = true
	}
	if reviewShouldCollectSourceEvidenceFirst(run, opts, focusPaths) {
		collectFileReviewEvidence(rt, root, focusPaths, run.RequestAnalysis.ScopeDiscovery.CandidateSymbols, &changeSet, &evidence, reviewRemainingContextChars(opts.MaxContextChars, evidence.Text))
		collectedSourceFirst = true
	}
	switch run.Target {
	case reviewTargetPlan:
		collectPlanReviewEvidence(rt, &evidence, opts)
	case reviewTargetSelection:
		collectSelectionReviewEvidence(rt, root, &changeSet, &evidence, reviewRemainingContextChars(opts.MaxContextChars, evidence.Text))
	case reviewTargetPR:
		collectGitReviewEvidence(ctx, root, opts.Paths, &changeSet, &evidence, opts)
	case reviewTargetGoal:
		collectGoalReviewEvidence(rt, root, &changeSet, &evidence, opts)
	case reviewTargetAnalysis:
		collectAnalysisReviewEvidence(rt, root, focusPaths, &changeSet, &evidence, opts)
	default:
		if !collectedGitEvidence && (opts.IncludeGitDiff || (changeSet.Source == "" && strings.TrimSpace(evidence.Text) == "")) {
			collectGitReviewEvidence(ctx, root, focusPaths, &changeSet, &evidence, opts)
		}
	}
	if !collectedSourceFirst && (opts.IncludeFileContents || (strings.TrimSpace(evidence.Text) == "" && len(focusPaths) > 0) || reviewShouldAutoIncludeScopeFileEvidence(run, opts, focusPaths)) {
		collectFileReviewEvidence(rt, root, focusPaths, run.RequestAnalysis.ScopeDiscovery.CandidateSymbols, &changeSet, &evidence, reviewRemainingContextChars(opts.MaxContextChars, evidence.Text))
	}
	if rt != nil && rt.session != nil {
		collectSessionReviewEvidence(rt.session, &evidence, reviewShouldIncludeCodingHarness(run))
	}
	if rt != nil && rt.verifyHistory != nil && reviewShouldCollectVerificationHistory(run) {
		collectVerificationHistoryReviewEvidence(rt.verifyHistory, root, rt.session, &evidence)
	}
	changeSet.ChangedPaths = analysisUniqueStrings(append(changeSet.ChangedPaths, evidence.ChangedPaths...))
	sort.Strings(changeSet.ChangedPaths)
	evidence.ChangedPaths = append([]string(nil), changeSet.ChangedPaths...)
	if scopeText := renderReviewScopeDiscoveryForEvidence(run.RequestAnalysis); strings.TrimSpace(scopeText) != "" && strings.TrimSpace(evidence.Text) != "" {
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Deterministic scope discovery", scopeText)
		evidence.Sources = append(evidence.Sources, "scope_discovery")
	}
	if opts.MaxContextChars > 0 && len(evidence.Text) > opts.MaxContextChars {
		if isPreWrite {
			var compacted bool
			evidence.Text, compacted = compactPreWriteEvidencePreservingPriority(evidence.Text, opts.MaxContextChars)
			if compacted {
				evidence.Warnings = append(evidence.Warnings, "optional pre-write review evidence compacted to preserve edit proposal and required repair findings")
			}
		} else {
			evidence.Text = compactPromptSection(evidence.Text, opts.MaxContextChars)
			evidence.Warnings = append(evidence.Warnings, "review evidence text truncated to max context budget")
		}
	}
	if opts.MaxContextChars > 0 && len(evidence.Text) > opts.MaxContextChars {
		evidence.Text = compactPromptSectionPreserveHeadTail(evidence.Text, opts.MaxContextChars)
		evidence.Warnings = append(evidence.Warnings, "review evidence text truncated to max context budget")
	}
	if changeSet.Source == "" {
		changeSet.Source = "session"
	}
	changeSet.Fingerprint = computeReviewFingerprint(changeSet.Source, strings.Join(changeSet.ChangedPaths, ","), changeSet.DiffStat, changeSet.DiffExcerpt, evidence.Text)
	evidence.Sources = analysisUniqueStrings(evidence.Sources)
	evidence.Warnings = analysisUniqueStrings(evidence.Warnings)
	return changeSet, evidence
}

func reviewShouldCollectVerificationHistory(run ReviewRun) bool {
	trigger := strings.TrimSpace(run.Trigger)
	if strings.EqualFold(trigger, "pre_write") || strings.EqualFold(trigger, reviewBeforeFixTrigger) {
		return false
	}
	return true
}

func reviewShouldCollectGitEvidenceBeforeSource(run ReviewRun, opts ReviewHarnessOptions, focusPaths []string) bool {
	if len(focusPaths) == 0 ||
		!strings.EqualFold(strings.TrimSpace(run.Target), reviewTargetChange) ||
		strings.TrimSpace(opts.ProvidedDiff) != "" ||
		strings.TrimSpace(opts.ProvidedCode) != "" {
		return false
	}
	return opts.IncludeGitDiff || strings.EqualFold(strings.TrimSpace(run.Flow), "change_review")
}

func reviewChangeGitEvidenceBudget(maxChars int) int {
	if maxChars <= 0 {
		return maxChars
	}
	budget := maxChars / 3
	if budget > 60000 {
		return 60000
	}
	if budget < 20000 && maxChars >= 40000 {
		return 20000
	}
	if budget <= 0 {
		return maxChars
	}
	return budget
}

func appendPreWriteRequiredRepairDiffEvidence(run ReviewRun, opts ReviewHarnessOptions, evidence *ReviewEvidencePack, maxChars int) bool {
	if evidence == nil ||
		maxChars <= 0 ||
		!strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") ||
		strings.TrimSpace(opts.ProvidedDiff) == "" ||
		len(opts.RepairFindings) == 0 {
		return false
	}
	tokens := reviewRepairDiffEvidenceTokens(opts.RepairFindings, opts.ProvidedDiff)
	if len(tokens) == 0 {
		return false
	}
	budget := maxChars / 3
	if budget > reviewPreWriteDiffEvidenceMaxChars {
		budget = reviewPreWriteDiffEvidenceMaxChars
	}
	if budget < 1600 && maxChars >= 1600 {
		budget = 1600
	}
	if budget <= 0 {
		return false
	}
	excerpt := reviewDiffTokenWindowExcerpt(opts.ProvidedDiff, tokens, budget)
	if strings.TrimSpace(excerpt) == "" {
		return false
	}
	evidence.Text = appendReviewEvidenceSection(evidence.Text, "Pre-write required repair diff excerpts", excerpt)
	evidence.Sources = append(evidence.Sources, "repair_diff_excerpt")
	return true
}

func appendPreWriteRequiredRepairAfterEvidence(run ReviewRun, opts ReviewHarnessOptions, evidence *ReviewEvidencePack, maxChars int) bool {
	if evidence == nil ||
		maxChars <= 0 ||
		!strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") ||
		len(opts.RepairFindings) == 0 ||
		len(run.EditProposals) == 0 {
		return false
	}
	var sections []string
	for _, proposal := range normalizeEditProposals(run.EditProposals) {
		after := proposal.AfterExcerpt
		if strings.TrimSpace(after) == "" {
			continue
		}
		var b strings.Builder
		if strings.TrimSpace(proposal.File) != "" {
			fmt.Fprintf(&b, "file: %s\n", proposal.File)
		}
		if strings.TrimSpace(proposal.Operation) != "" {
			fmt.Fprintf(&b, "operation: %s\n", proposal.Operation)
		}
		b.WriteString(after)
		sections = append(sections, strings.TrimSpace(b.String()))
	}
	if len(sections) == 0 {
		return false
	}
	budget := maxChars
	if budget > reviewPreWriteFileContextChars {
		budget = reviewPreWriteFileContextChars
	}
	if budget < 10000 && maxChars >= 10000 {
		budget = 10000
	}
	if budget <= 0 {
		return false
	}
	body := compactPromptSectionPreserveHeadTail(strings.Join(sections, "\n\n-- after excerpt gap --\n\n"), budget)
	if strings.TrimSpace(body) == "" {
		return false
	}
	evidence.Text = appendReviewEvidenceSection(evidence.Text, "Pre-write required repair after excerpts", body)
	evidence.Sources = append(evidence.Sources, "after_excerpt")
	return true
}

func reviewRepairDiffEvidenceTokens(repairs []ReviewFinding, diff string) []string {
	diffLower := strings.ToLower(diff)
	stop := map[string]bool{
		"code": true, "path": true, "line": true, "lines": true, "this": true, "that": true,
		"with": true, "from": true, "into": true, "return": true,
		"failed": true, "failure": true, "error": true, "current": true,
		"review": true, "finding": true, "required": true, "repair": true, "should": true,
		"문제": true, "영향": true, "수정": true, "확인": true, "경우": true, "처리": true,
		"현재": true, "리뷰": true, "코드": true,
	}
	seen := map[string]bool{}
	var out []string
	add := func(token string) {
		token = strings.Trim(token, "`'\".,:;()[]{}<>")
		if len(token) < 5 {
			return
		}
		lower := strings.ToLower(token)
		if stop[lower] || seen[lower] || !strings.Contains(diffLower, lower) {
			return
		}
		seen[lower] = true
		out = append(out, token)
	}
	for _, repair := range repairs {
		text := strings.Join([]string{
			repair.ID,
			repair.Title,
			repair.Evidence,
			repair.Impact,
			repair.RequiredFix,
			repair.TestRecommendation,
		}, " ")
		for _, quoted := range reviewBacktickTokens(text) {
			add(quoted)
		}
		for _, token := range strings.FieldsFunc(text, func(r rune) bool {
			return !(r == '_' || r == ':' || r == '\\' || r == '/' || r == '-' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'))
		}) {
			add(token)
		}
	}
	return limitStrings(out, 24)
}

func reviewBacktickTokens(text string) []string {
	var out []string
	inTick := false
	var b strings.Builder
	for _, r := range text {
		if r == '`' {
			if inTick {
				if token := strings.TrimSpace(b.String()); token != "" {
					out = append(out, token)
				}
				b.Reset()
			}
			inTick = !inTick
			continue
		}
		if inTick {
			b.WriteRune(r)
		}
	}
	return out
}

func reviewDiffTokenWindowExcerpt(diff string, tokens []string, maxChars int) string {
	if maxChars <= 0 || len(tokens) == 0 {
		return ""
	}
	lines := reviewNormalizedLines(diff)
	if len(lines) == 0 {
		return ""
	}
	tokenLower := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.ToLower(strings.TrimSpace(token))
		if token != "" {
			tokenLower = append(tokenLower, token)
		}
	}
	selected := map[int]bool{}
	for i, line := range lines {
		lower := strings.ToLower(line)
		matched := false
		for _, token := range tokenLower {
			if strings.Contains(lower, token) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		start := i - 16
		if start < 0 {
			start = 0
		}
		end := i + 24
		if end >= len(lines) {
			end = len(lines) - 1
		}
		for j := start; j <= end; j++ {
			selected[j] = true
		}
	}
	if len(selected) == 0 {
		return ""
	}
	var indices []int
	for idx := range selected {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	var b strings.Builder
	last := -2
	for _, idx := range indices {
		if b.Len() >= maxChars {
			break
		}
		if idx != last+1 && b.Len() > 0 {
			b.WriteString("\n-- diff excerpt gap --\n")
		}
		fmt.Fprintf(&b, "%5d | %s\n", idx+1, lines[idx])
		last = idx
	}
	return compactPromptSectionPreserveHeadTail(b.String(), maxChars)
}

func reviewEvidenceFocusPaths(run ReviewRun, opts ReviewHarnessOptions) []string {
	if len(opts.Paths) > 0 {
		return mcpReviewCleanPaths(opts.Paths)
	}
	if strings.TrimSpace(opts.ProvidedDiff) != "" || strings.TrimSpace(opts.ProvidedCode) != "" {
		return nil
	}
	return mcpReviewCleanPaths(run.RequestAnalysis.ScopeDiscovery.CandidateFiles)
}

func reviewPreWriteShouldIncludeFileContext(run ReviewRun, opts ReviewHarnessOptions, focusPaths []string) bool {
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		return false
	}
	if strings.TrimSpace(opts.ProvidedDiff) == "" {
		return false
	}
	return len(reviewPreWriteFileContextPaths(run, opts, focusPaths)) > 0
}

func reviewPreWriteFileContextPaths(run ReviewRun, opts ReviewHarnessOptions, focusPaths []string) []string {
	var paths []string
	paths = append(paths, focusPaths...)
	paths = append(paths, normalizeTaskStateList(opts.Paths, 128)...)
	paths = append(paths, reviewScopeCandidateFilesFromDiff(opts.ProvidedDiff)...)
	paths = append(paths, run.RequestAnalysis.ScopeDiscovery.CandidateFiles...)
	for _, proposal := range run.EditProposals {
		paths = append(paths, proposal.File)
	}
	return mcpReviewCleanPaths(analysisUniqueStrings(paths))
}

func reviewProvidedDiffEvidenceBudget(opts ReviewHarnessOptions, includePreWriteFileContext bool) int {
	if opts.MaxContextChars <= 0 || !includePreWriteFileContext {
		return opts.MaxContextChars
	}
	if strings.EqualFold(strings.TrimSpace(opts.Trigger), "pre_write") && strings.TrimSpace(opts.ProvidedDiff) != "" {
		reserve := reviewPreWriteFileContextChars
		if reserve > 20000 {
			reserve = 20000
		}
		if opts.MaxContextChars < reserve*2 {
			reserve = opts.MaxContextChars / 4
		}
		fullDiffBudget := opts.MaxContextChars - reserve
		if fullDiffBudget > 0 && len(opts.ProvidedDiff) <= fullDiffBudget {
			return len(opts.ProvidedDiff)
		}
	}
	budget := reviewPreWriteDiffEvidenceMaxChars
	if opts.MaxContextChars <= reviewPreWriteFileContextChars+budget {
		budget = opts.MaxContextChars / 2
	}
	if budget < 4000 && opts.MaxContextChars >= 8000 {
		return 4000
	}
	if budget <= 0 {
		return opts.MaxContextChars
	}
	return budget
}

func collectPreWriteCurrentFileContextEvidence(rt *runtimeState, root string, run ReviewRun, opts ReviewHarnessOptions, focusPaths []string, changeSet *ReviewChangeSet, evidence *ReviewEvidencePack, maxChars int) {
	if maxChars <= 0 {
		evidence.Warnings = append(evidence.Warnings, "pre-write file context omitted because review context budget is exhausted")
		return
	}
	if maxChars > reviewPreWriteFileContextChars {
		maxChars = reviewPreWriteFileContextChars
	}
	paths := reviewPreWriteFileContextPaths(run, opts, focusPaths)
	if len(paths) == 0 {
		return
	}
	requestText := strings.Join([]string{opts.Request, run.Objective, run.RequestAnalysis.OriginalRequest}, " ")
	selections := reviewMentionSelections(root, requestText)
	for _, selection := range selections {
		for _, raw := range paths {
			if !reviewSelectionMatchesPath(root, selection, raw) {
				continue
			}
			if appendPreWriteSelectionFileContext(root, selection, changeSet, evidence, maxChars) {
				if reviewPreWriteNeedsHeaderContext(run, opts) {
					appendPreWriteHeaderContext(root, selection.FilePath, changeSet, evidence, preWriteHeaderContextBudget(maxChars))
				}
				return
			}
		}
	}
	if reviewPreWriteNeedsHeaderContext(run, opts) {
		for _, raw := range paths {
			appendPreWriteHeaderContext(root, raw, changeSet, evidence, preWriteHeaderContextBudget(maxChars))
		}
	}
	collectFileReviewEvidence(rt, root, paths, run.RequestAnalysis.ScopeDiscovery.CandidateSymbols, changeSet, evidence, maxChars)
}

func collectPreFixSelectionCurrentFileContextEvidence(rt *runtimeState, root string, run ReviewRun, opts ReviewHarnessOptions, focusPaths []string, changeSet *ReviewChangeSet, evidence *ReviewEvidencePack, maxChars int) bool {
	if maxChars <= 0 {
		return false
	}
	selection, ok := reviewCurrentSelectionForEvidence(rt, root, strings.Join([]string{opts.Request, run.Objective, run.RequestAnalysis.OriginalRequest}, " "))
	if !ok {
		return false
	}
	paths := reviewPreWriteFileContextPaths(run, opts, focusPaths)
	if len(paths) == 0 {
		paths = []string{selection.FilePath}
	}
	for _, raw := range paths {
		if !reviewSelectionMatchesPath(root, selection, raw) {
			continue
		}
		return appendFocusedSelectionFileContext(root, selection, changeSet, evidence, maxChars, "Pre-fix current file context", "Pre-fix function body excerpt")
	}
	return false
}

func reviewCurrentSelectionForEvidence(rt *runtimeState, root string, requestText string) (ViewerSelection, bool) {
	if selections := reviewMentionSelections(root, requestText); len(selections) > 0 {
		return selections[0], true
	}
	if rt != nil && rt.session != nil {
		if selection := rt.session.CurrentSelection(); selection != nil && selection.HasSelection() {
			return *selection, true
		}
	}
	return ViewerSelection{}, false
}

func reviewMentionSelections(root string, input string) []ViewerSelection {
	var selections []ViewerSelection
	seen := map[string]bool{}
	for _, token := range strings.Fields(input) {
		selection, ok := parseReviewMentionSelection(root, token)
		if !ok || !selection.HasSelection() {
			continue
		}
		key := strings.ToLower(filepath.Clean(selection.FilePath)) + fmt.Sprintf(":%d-%d", selection.StartLine, selection.EndLine)
		if seen[key] {
			continue
		}
		seen[key] = true
		selections = append(selections, selection)
	}
	return selections
}

func reviewSelectionMatchesPath(root string, selection ViewerSelection, path string) bool {
	selectionPath, ok := reviewAbsolutePath(root, selection.FilePath)
	if !ok {
		return false
	}
	candidatePath, ok := reviewAbsolutePath(root, path)
	if !ok {
		return false
	}
	return strings.EqualFold(selectionPath, candidatePath)
}

func reviewAbsolutePath(root string, path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return filepath.Clean(abs), true
}

func appendPreWriteSelectionFileContext(root string, selection ViewerSelection, changeSet *ReviewChangeSet, evidence *ReviewEvidencePack, maxChars int) bool {
	return appendFocusedSelectionFileContext(root, selection, changeSet, evidence, maxChars, "Pre-write current file context", "Pre-write function body excerpt")
}

func reviewPreWriteNeedsHeaderContext(run ReviewRun, opts ReviewHarnessOptions) bool {
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		return false
	}
	text := strings.ToLower(strings.Join([]string{
		opts.ProvidedDiff,
		renderEditProposalsForEvidence(run.EditProposals),
		renderReviewRepairFindingsForEvidence(opts.RepairFindings),
	}, "\n"))
	return containsAny(text,
		"unique_ptr",
		"std::unique_ptr",
		"shared_ptr",
		"std::shared_ptr",
		"vector<",
		"std::vector",
		"map<",
		"std::map",
		"unordered_map",
		"std::unordered_map",
		"set<",
		"std::set",
		"#include",
		"missing include",
		"header",
	)
}

func preWriteHeaderContextBudget(maxChars int) int {
	if maxChars <= 0 {
		return 0
	}
	budget := maxChars / 5
	if budget > 8000 {
		return 8000
	}
	if budget < 800 && maxChars >= 1600 {
		return 800
	}
	return budget
}

func appendPreWriteHeaderContext(root string, path string, changeSet *ReviewChangeSet, evidence *ReviewEvidencePack, maxChars int) bool {
	if maxChars <= 0 {
		return false
	}
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(root, resolved)
	}
	resolved, ok := reviewAbsolutePath(root, resolved)
	if !ok {
		return false
	}
	rel := filepath.ToSlash(relOrAbs(root, resolved))
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return false
	}
	data, err := os.ReadFile(resolved)
	if err != nil || !isText(data) {
		return false
	}
	content := string(data)
	lines := reviewNormalizedLines(content)
	end := reviewHeaderContextEndLine(lines)
	if end <= 0 {
		return false
	}
	body := compactPromptSection(sliceLines(content, 1, end), maxChars)
	if strings.TrimSpace(body) == "" {
		return false
	}
	changeSet.ChangedPaths = append(changeSet.ChangedPaths, rel)
	evidence.ChangedPaths = append(evidence.ChangedPaths, rel)
	if changeSet.Source == "" {
		changeSet.Source = "workspace_files"
	}
	evidence.Text = appendReviewEvidenceSection(evidence.Text, fmt.Sprintf("Pre-write include/header context: %s:1-%d", rel, end), body)
	evidence.Sources = append(evidence.Sources, "header_excerpt")
	return true
}

func reviewHeaderContextEndLine(lines []string) int {
	if len(lines) == 0 {
		return 0
	}
	limit := len(lines)
	if limit > 80 {
		limit = 80
	}
	lastInteresting := 0
	seenInclude := false
	for i := 0; i < limit; i++ {
		trimmed := strings.TrimSpace(lines[i])
		lower := strings.ToLower(trimmed)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			if !seenInclude {
				lastInteresting = i + 1
			}
			continue
		}
		if strings.HasPrefix(lower, "#include") || strings.HasPrefix(lower, "#pragma once") ||
			strings.HasPrefix(lower, "#define") || strings.HasPrefix(lower, "#ifdef") ||
			strings.HasPrefix(lower, "#ifndef") || strings.HasPrefix(lower, "#endif") {
			lastInteresting = i + 1
			if strings.HasPrefix(lower, "#include") {
				seenInclude = true
			}
			continue
		}
		if seenInclude {
			break
		}
		lastInteresting = i + 1
	}
	if lastInteresting <= 0 {
		if limit < 40 {
			return limit
		}
		return 40
	}
	return lastInteresting
}

func appendFocusedSelectionFileContext(root string, selection ViewerSelection, changeSet *ReviewChangeSet, evidence *ReviewEvidencePack, maxChars int, contextTitle string, functionTitle string) bool {
	resolved := selection.FilePath
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(root, resolved)
	}
	resolved, ok := reviewAbsolutePath(root, resolved)
	if !ok {
		return false
	}
	rel := filepath.ToSlash(relOrAbs(root, resolved))
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		evidence.Warnings = append(evidence.Warnings, fmt.Sprintf("pre-write file context skipped outside workspace: %s", selection.FilePath))
		return false
	}
	data, err := os.ReadFile(resolved)
	if err != nil || !isText(data) {
		if err != nil {
			evidence.Warnings = append(evidence.Warnings, fmt.Sprintf("pre-write file context unavailable for %s: %v", selection.FilePath, err))
		}
		return false
	}
	content := string(data)
	lineCount := reviewLineCount(content)
	contextStart := selection.StartLine - reviewPreWriteLineContextBefore
	if contextStart < 1 {
		contextStart = 1
	}
	contextEnd := selection.EndLine + reviewPreWriteLineContextAfter
	if functionStart, functionEnd, ok := reviewFunctionSpanForSelection(content, selection); ok {
		appendPreWriteFunctionBodyEvidence(rel, content, selection, functionStart, functionEnd, changeSet, evidence, maxChars, functionTitle)
		contextStart = selection.StartLine
		contextEnd = functionEnd
	}
	if lineCount > 0 && contextEnd > lineCount {
		contextEnd = lineCount
	}
	body := preWriteSelectionFileContextBody(content, selection, contextStart, contextEnd, preWriteCurrentFileContextBudget(maxChars))
	if strings.TrimSpace(body) == "" {
		return false
	}
	changeSet.ChangedPaths = append(changeSet.ChangedPaths, rel)
	evidence.ChangedPaths = append(evidence.ChangedPaths, rel)
	if changeSet.Source == "" {
		changeSet.Source = "workspace_files"
	}
	evidence.Text = appendReviewEvidenceSection(evidence.Text, fmt.Sprintf("%s: %s:%d-%d", contextTitle, rel, contextStart, contextEnd), body)
	evidence.Sources = append(evidence.Sources, "file_excerpt")
	return true
}

func appendPreWriteFunctionBodyEvidence(rel string, content string, selection ViewerSelection, start int, end int, changeSet *ReviewChangeSet, evidence *ReviewEvidencePack, maxChars int, title string) {
	budget := preWriteFunctionBodyContextBudget(maxChars)
	if budget <= 0 {
		return
	}
	body := preWriteFunctionBodyContextBody(content, selection, start, end, budget)
	if strings.TrimSpace(body) == "" {
		return
	}
	changeSet.ChangedPaths = append(changeSet.ChangedPaths, rel)
	evidence.ChangedPaths = append(evidence.ChangedPaths, rel)
	if changeSet.Source == "" {
		changeSet.Source = "workspace_files"
	}
	if strings.TrimSpace(title) == "" {
		title = "Pre-write function body excerpt"
	}
	evidence.Text = appendReviewEvidenceSection(evidence.Text, fmt.Sprintf("%s: %s:%d-%d", title, rel, start, end), body)
	evidence.Sources = append(evidence.Sources, "function_body_excerpt")
}

func preWriteFunctionBodyContextBudget(maxChars int) int {
	if maxChars <= 0 {
		return 0
	}
	budget := (maxChars * 3) / 5
	if budget < 3000 && maxChars >= 5000 {
		return 3000
	}
	return budget
}

func preWriteCurrentFileContextBudget(maxChars int) int {
	if maxChars <= 0 {
		return 0
	}
	budget := maxChars - preWriteFunctionBodyContextBudget(maxChars)
	if budget < 2500 && maxChars >= 5000 {
		return 2500
	}
	return budget
}

func preWriteFunctionBodyContextBody(content string, selection ViewerSelection, start int, end int, maxChars int) string {
	full := sliceLines(content, start, end)
	if maxChars <= 0 {
		return full
	}
	if len(full) <= maxChars {
		if structured := preWriteFunctionBodyStructuredContext(content, selection, start, end, 0, 0, 0, maxChars/5, maxChars); len(structured) <= maxChars {
			return structured
		}
		return full
	}
	beforeBudget := maxChars / 5
	selectedBudget := maxChars / 5
	tailExcerptBudget := maxChars / 5
	tailBudget := maxChars - beforeBudget - selectedBudget - tailExcerptBudget - 384
	if tailBudget < 1500 {
		tailBudget = 1500
		if beforeBudget > 800 {
			beforeBudget -= 400
		}
		if selectedBudget > 800 {
			selectedBudget -= 400
		}
		if tailExcerptBudget > 800 {
			tailExcerptBudget -= 400
		}
	}
	return preWriteFunctionBodyStructuredContext(content, selection, start, end, beforeBudget, selectedBudget, tailBudget, tailExcerptBudget, maxChars)
}

func preWriteFunctionBodyStructuredContext(content string, selection ViewerSelection, start int, end int, beforeBudget int, selectedBudget int, tailBudget int, tailExcerptBudget int, maxChars int) string {
	var sections []string
	if start < selection.StartLine {
		before := sliceLines(content, start, selection.StartLine-1)
		if beforeBudget > 0 {
			before = compactPromptSection(before, beforeBudget)
		}
		if strings.TrimSpace(before) != "" {
			sections = append(sections, "-- function start to selection --\n"+before)
		}
	}
	selected := sliceLines(content, selection.StartLine, selection.EndLine)
	if selectedBudget > 0 {
		selected = compactPromptSectionPreserveHeadTail(selected, selectedBudget)
	}
	if strings.TrimSpace(selected) != "" {
		sections = append(sections, "-- selected range inside function --\n"+selected)
	}
	if selection.EndLine < end {
		tail := sliceLines(content, selection.EndLine+1, end)
		if tailBudget > 0 {
			tail = compactPromptSectionPreserveHeadTail(tail, tailBudget)
		}
		if strings.TrimSpace(tail) != "" {
			sections = append(sections, "-- selection to function end --\n"+tail)
		}
	}
	if tailExcerptBudget > 0 {
		tailExcerpt := preWriteFunctionTailExcerpt(content, selection.EndLine+1, end, tailExcerptBudget)
		if strings.TrimSpace(tailExcerpt) != "" {
			sections = append(sections, "-- function tail from selection to function end --\n"+tailExcerpt)
		}
	}
	if len(sections) == 0 {
		return sliceLines(content, start, end)
	}
	if maxChars <= 0 {
		return strings.Join(sections, "\n\n")
	}
	return compactPromptSectionPreserveHeadTail(strings.Join(sections, "\n\n"), maxChars)
}

func preWriteSelectionFileContextBody(content string, selection ViewerSelection, start int, end int, maxChars int) string {
	full := sliceLines(content, start, end)
	if maxChars <= 0 || len(full) <= maxChars {
		return full
	}
	immediateEnd := selection.EndLine + 80
	if immediateEnd > end {
		immediateEnd = end
	}
	beforeBudget := maxChars / 10
	selectionBudget := maxChars / 5
	postSelectionBudget := (maxChars * 3) / 5
	laterBudget := maxChars - beforeBudget - selectionBudget - postSelectionBudget - 384
	if laterBudget < 0 {
		laterBudget = 0
	}
	var sections []string
	if start < selection.StartLine {
		before := compactPromptSectionPreserveHeadTail(sliceLines(content, start, selection.StartLine-1), beforeBudget)
		if strings.TrimSpace(before) != "" {
			sections = append(sections, "-- pre-selection context --\n"+before)
		}
	}
	selected := compactPromptSectionPreserveHeadTail(sliceLines(content, selection.StartLine, selection.EndLine), selectionBudget)
	if strings.TrimSpace(selected) != "" {
		sections = append(sections, "-- selected range context --\n"+selected)
	}
	if selection.EndLine < immediateEnd {
		immediate := compactPromptSection(sliceLines(content, selection.EndLine+1, immediateEnd), postSelectionBudget)
		if strings.TrimSpace(immediate) != "" {
			sections = append(sections, "-- immediate post-selection context --\n"+immediate)
		}
	}
	if immediateEnd < end {
		later := compactPromptSectionPreserveHeadTail(sliceLines(content, immediateEnd+1, end), laterBudget)
		if strings.TrimSpace(later) != "" {
			sections = append(sections, "-- later post-selection context --\n"+later)
		}
	}
	tailExcerpt := preWriteFunctionTailExcerpt(content, selection.EndLine+1, end, maxChars/5)
	if strings.TrimSpace(tailExcerpt) != "" {
		sections = append(sections, "-- function tail from selection to function end --\n"+tailExcerpt)
	}
	return compactPromptSectionPreserveHeadTail(strings.Join(sections, "\n\n"), maxChars)
}

func preWriteFunctionTailExcerpt(content string, start int, end int, maxChars int) string {
	if maxChars <= 0 || end < start {
		return ""
	}
	lines := reviewNormalizedLines(content)
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end < start {
		return ""
	}
	if end-start <= 17 {
		return compactPromptSectionPreserveHeadTail(sliceLines(content, start, end), maxChars)
	}
	var sections []string
	mid := start + ((end - start) / 2)
	sections = append(sections, sliceLines(content, maxInt(start, mid-1), minInt(end, mid+1)))
	sections = append(sections, sliceLines(content, start, minInt(end, start+2)))
	sections = append(sections, sliceLines(content, maxInt(start, end-2), end))
	return compactPromptSectionPreserveHeadTail(strings.Join(uniqueStrings(sections), "\n...\n"), maxChars)
}

func compactPromptSectionPreserveHeadTail(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	marker := "\n...\n"
	if limit <= len(marker)+8 {
		return compactPromptSection(text, limit)
	}
	remaining := limit - len(marker)
	headBudget := remaining / 2
	tailBudget := remaining - headBudget
	head := compactPromptSection(text, headBudget)
	head = strings.TrimSuffix(strings.TrimSpace(head), "...")
	tail := compactPromptTail(text, tailBudget)
	return strings.TrimSpace(head) + marker + strings.TrimSpace(tail)
}

func compactPromptTail(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	runes := []rune(text)
	var size int
	start := len(runes)
	for start > 0 {
		next := string(runes[start-1])
		nextSize := len(next)
		if size+nextSize > limit {
			break
		}
		start--
		size += nextSize
	}
	if start <= 0 {
		return text
	}
	return "..." + strings.TrimSpace(string(runes[start:]))
}

type reviewBraceSpan struct {
	start int
	end   int
}

func reviewFunctionSpanForSelection(content string, selection ViewerSelection) (int, int, bool) {
	if !selection.HasSelection() {
		return 0, 0, false
	}
	lines := reviewNormalizedLines(content)
	spans := reviewBraceSpans(lines)
	var best reviewBraceSpan
	bestSet := false
	for _, span := range spans {
		if span.start > selection.StartLine || span.end < selection.EndLine {
			continue
		}
		signatureStart := reviewFunctionSignatureStartLine(lines, span.start)
		if signatureStart <= 0 || !reviewSpanLooksLikeFunction(lines, signatureStart, span.start) {
			continue
		}
		candidate := reviewBraceSpan{start: signatureStart, end: span.end}
		if !bestSet ||
			candidate.start > best.start ||
			(candidate.start == best.start && candidate.end < best.end) {
			best = candidate
			bestSet = true
		}
	}
	if !bestSet {
		return 0, 0, false
	}
	return best.start, best.end, true
}

func reviewNormalizedLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return strings.Split(content, "\n")
}

func reviewBraceSpans(lines []string) []reviewBraceSpan {
	var stack []int
	var spans []reviewBraceSpan
	inBlockComment := false
	var quote rune
	escape := false
	for lineIndex, line := range lines {
		lineSpans, lineStack, lineBlockComment, lineQuote, lineEscape := reviewBraceSpansForLine(line, lineIndex+1, stack, inBlockComment, quote, escape)
		spans = append(spans, lineSpans...)
		stack = lineStack
		inBlockComment = lineBlockComment
		quote = lineQuote
		escape = lineEscape
	}
	return spans
}

func reviewBraceSpansForLine(line string, lineNumber int, stack []int, inBlockComment bool, quote rune, escape bool) ([]reviewBraceSpan, []int, bool, rune, bool) {
	var spans []reviewBraceSpan
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		next := rune(0)
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		if inBlockComment {
			if ch == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if quote != 0 {
			if escape {
				escape = false
				continue
			}
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '/' && next == '/' {
			break
		}
		if ch == '/' && next == '*' {
			inBlockComment = true
			i++
			continue
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			quote = ch
			escape = false
			continue
		}
		if ch == '{' {
			stack = append(stack, lineNumber)
			continue
		}
		if ch == '}' && len(stack) > 0 {
			start := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			spans = append(spans, reviewBraceSpan{start: start, end: lineNumber})
		}
	}
	return spans, stack, inBlockComment, quote, escape
}

func reviewFunctionSignatureStartLine(lines []string, braceLine int) int {
	if braceLine <= 0 || braceLine > len(lines) {
		return 0
	}
	start := braceLine
	for start > 1 {
		trimmed := strings.TrimSpace(lines[start-2])
		if trimmed == "" {
			break
		}
		start--
		if strings.Contains(trimmed, ";") {
			start++
			break
		}
		if strings.Contains(trimmed, "}") {
			start++
			break
		}
		if strings.HasPrefix(trimmed, ":") || strings.HasPrefix(trimmed, ",") {
			continue
		}
		if strings.Contains(trimmed, ")") {
			break
		}
	}
	return start
}

func reviewSpanLooksLikeFunction(lines []string, signatureStart int, braceLine int) bool {
	if signatureStart <= 0 || braceLine <= 0 || signatureStart > len(lines) || braceLine > len(lines) {
		return false
	}
	text := strings.TrimSpace(strings.Join(lines[signatureStart-1:braceLine], " "))
	text = strings.TrimSpace(strings.TrimSuffix(text, "{"))
	if text == "" || !strings.Contains(text, ")") {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(text))
	if strings.Contains(lower, "](") && strings.Contains(lower, "[") {
		return false
	}
	rejectPrefixes := []string{
		"if ",
		"if(",
		"for ",
		"for(",
		"while ",
		"while(",
		"switch ",
		"switch(",
		"catch ",
		"catch(",
		"else ",
		"else{",
		"else\t",
		"do ",
		"do{",
		"do\t",
		"try ",
		"try{",
		"try\t",
		"namespace ",
		"class ",
		"struct ",
		"enum ",
		"union ",
	}
	for _, prefix := range rejectPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	return !strings.Contains(lower, ";")
}

func reviewLineCount(content string) int {
	if content == "" {
		return 0
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return len(strings.Split(content, "\n"))
}

func reviewShouldCollectSourceEvidenceFirst(run ReviewRun, opts ReviewHarnessOptions, focusPaths []string) bool {
	if len(focusPaths) == 0 {
		return false
	}
	if opts.IncludeFileContents {
		return true
	}
	if strings.TrimSpace(opts.ProvidedDiff) != "" || strings.TrimSpace(opts.ProvidedCode) != "" {
		return false
	}
	if strings.TrimSpace(run.Objective) == "" {
		return false
	}
	width := strings.ToLower(strings.TrimSpace(run.RequestAnalysis.ScopeDiscovery.ScopeWidth))
	return width == "focused" || width == "bounded" || reviewRequestPrefersSourceEvidence(run.Objective, run.RequestAnalysis.ScopeDiscovery)
}

func reviewShouldAutoIncludeScopeFileEvidence(run ReviewRun, opts ReviewHarnessOptions, focusPaths []string) bool {
	if len(focusPaths) == 0 || len(opts.Paths) > 0 {
		return false
	}
	if strings.TrimSpace(opts.ProvidedDiff) != "" || strings.TrimSpace(opts.ProvidedCode) != "" {
		return false
	}
	if strings.TrimSpace(run.Objective) == "" {
		return false
	}
	width := strings.ToLower(strings.TrimSpace(run.RequestAnalysis.ScopeDiscovery.ScopeWidth))
	return width == "focused" || width == "bounded"
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

func collectAnalysisReviewEvidence(rt *runtimeState, root string, paths []string, changeSet *ReviewChangeSet, evidence *ReviewEvidencePack, opts ReviewHarnessOptions) {
	hasAnalysis := false
	if rt != nil && rt.session != nil && rt.session.LastAnalysis != nil {
		data, _ := json.MarshalIndent(rt.session.LastAnalysis, "", "  ")
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Latest analysis summary", compactPromptSection(string(data), 8000))
		evidence.Sources = append(evidence.Sources, "analysis_summary")
		hasAnalysis = true
	}
	if !hasAnalysis {
		evidence.Warnings = append(evidence.Warnings, "no latest analysis summary found")
	}
	collectGitReviewEvidence(context.Background(), root, paths, changeSet, evidence, opts)
}

func collectGitReviewEvidence(ctx context.Context, root string, paths []string, changeSet *ReviewChangeSet, evidence *ReviewEvidencePack, opts ReviewHarnessOptions) {
	if changeSet.Source == "" {
		changeSet.Source = "git_worktree"
	}
	pathArgs, err := reviewGitPathArgs(root, paths)
	if err != nil {
		evidence.Warnings = append(evidence.Warnings, err.Error())
		return
	}
	statusArgs := []string{"status", "--short", "--branch"}
	statusArgs = append(statusArgs, pathArgs...)
	status := runGitText(root, statusArgs...)
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
		remaining := reviewRemainingContextChars(opts.MaxContextChars, evidence.Text)
		if remaining <= 0 {
			evidence.Warnings = append(evidence.Warnings, "git diff excerpt omitted because review context budget is exhausted")
		} else {
			changeSet.DiffExcerpt = compactPromptSection(combined, remaining)
			evidence.Text = appendReviewEvidenceSection(evidence.Text, "Git diff excerpt", changeSet.DiffExcerpt)
			evidence.Sources = append(evidence.Sources, "git_diff")
		}
	}
	if len(untracked) > 0 {
		collectFileReviewEvidence(nil, root, limitStrings(untracked, 8), nil, changeSet, evidence, reviewRemainingContextChars(opts.MaxContextChars, evidence.Text))
	}
	_ = ctx
}

func collectFileReviewEvidence(rt *runtimeState, root string, paths []string, symbols []string, changeSet *ReviewChangeSet, evidence *ReviewEvidencePack, maxChars int) {
	if maxChars <= 0 {
		evidence.Warnings = append(evidence.Warnings, "file review context budget exhausted")
		return
	}
	cleanPaths := mcpReviewCleanPaths(paths)
	remaining := maxChars
	for i, raw := range cleanPaths {
		if remaining <= 0 {
			evidence.Warnings = append(evidence.Warnings, "file excerpts truncated by review context budget")
			return
		}
		if shouldSkipMCPReviewFile(raw) {
			continue
		}
		fileBudget := reviewFileEvidenceBudget(remaining, len(cleanPaths)-i)
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
		if err != nil || info.IsDir() {
			continue
		}
		rel := filepath.ToSlash(relOrAbs(root, resolved))
		fileSymbols := reviewFileEvidenceSymbols(symbols, rel)
		if info.Size() > 8*1024*1024 {
			if len(fileSymbols) > 0 {
				body := reviewFileSymbolExcerptsFromPath(resolved, fileSymbols, fileBudget)
				if strings.TrimSpace(body) != "" {
					changeSet.ChangedPaths = append(changeSet.ChangedPaths, rel)
					evidence.ChangedPaths = append(evidence.ChangedPaths, rel)
					if changeSet.Source == "" {
						changeSet.Source = "workspace_files"
					}
					beforeLen := len(evidence.Text)
					evidence.Text = appendReviewEvidenceSection(evidence.Text, "Symbol excerpt: "+rel+" :: "+strings.Join(fileSymbols, ", "), body)
					remaining -= len(evidence.Text) - beforeLen
					evidence.Sources = append(evidence.Sources, "file_excerpt")
					continue
				}
				evidence.Warnings = append(evidence.Warnings, fmt.Sprintf("skipped %s: requested symbols not found in large file: %s", raw, strings.Join(fileSymbols, ", ")))
				continue
			}
			evidence.Warnings = append(evidence.Warnings, fmt.Sprintf("skipped %s: file is too large for review excerpt collection", raw))
			continue
		}
		data, err := os.ReadFile(resolved)
		if err != nil || !isText(data) {
			continue
		}
		changeSet.ChangedPaths = append(changeSet.ChangedPaths, rel)
		evidence.ChangedPaths = append(evidence.ChangedPaths, rel)
		body, title, symbolMatched := reviewFileEvidenceBody(rel, string(data), fileSymbols, fileBudget)
		if strings.TrimSpace(body) == "" {
			if len(fileSymbols) > 0 {
				evidence.Warnings = append(evidence.Warnings, fmt.Sprintf("skipped %s: requested symbols not found: %s", rel, strings.Join(fileSymbols, ", ")))
			}
			continue
		}
		if len(fileSymbols) > 0 && !symbolMatched {
			evidence.Warnings = append(evidence.Warnings, fmt.Sprintf("symbol-focused excerpt unavailable in %s: %s", rel, strings.Join(fileSymbols, ", ")))
		}
		if changeSet.Source == "" {
			changeSet.Source = "workspace_files"
		}
		beforeLen := len(evidence.Text)
		evidence.Text = appendReviewEvidenceSection(evidence.Text, title, body)
		remaining -= len(evidence.Text) - beforeLen
		evidence.Sources = append(evidence.Sources, "file_excerpt")
	}
}

func reviewFileEvidenceBudget(remaining int, candidatesLeft int) int {
	if remaining <= 0 {
		return 0
	}
	if candidatesLeft <= 1 {
		return remaining
	}
	budget := remaining / candidatesLeft
	if budget < 12000 && remaining > 12000 {
		budget = 12000
	}
	if budget > 30000 {
		budget = 30000
	}
	if budget > remaining {
		return remaining
	}
	return budget
}

func reviewFileEvidenceSymbols(symbols []string, rel string) []string {
	pathLower := strings.ToLower(filepath.ToSlash(rel))
	var out []string
	seen := map[string]bool{}
	for _, symbol := range symbols {
		symbol = strings.TrimSpace(strings.TrimSuffix(symbol, "()"))
		if len(symbol) < 4 {
			continue
		}
		lower := strings.ToLower(symbol)
		if seen[lower] || strings.Contains(pathLower, lower) || reviewScopeStopWords[lower] {
			continue
		}
		seen[lower] = true
		out = append(out, symbol)
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func reviewFileEvidenceBody(rel string, content string, symbols []string, maxChars int) (string, string, bool) {
	if maxChars <= 0 {
		return "", "", false
	}
	if len(symbols) > 0 {
		if excerpt := reviewFileSymbolExcerpts(content, symbols, maxChars); strings.TrimSpace(excerpt) != "" {
			return excerpt, "Symbol excerpt: " + rel + " :: " + strings.Join(symbols, ", "), true
		}
		if len(content) > 256*1024 {
			return "", "Symbol excerpt: " + rel + " :: " + strings.Join(symbols, ", "), false
		}
	}
	return compactPromptSection(content, maxChars), "File excerpt: " + rel, false
}

func reviewFileSymbolExcerpts(content string, symbols []string, maxChars int) string {
	if maxChars <= 0 || len(symbols) == 0 {
		return ""
	}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")
	type span struct {
		start int
		end   int
	}
	var spans []span
	lastEnd := -1
	for i, line := range lines {
		if !reviewLineContainsAnySymbol(line, symbols) {
			continue
		}
		start := i - 80
		if start < 0 {
			start = 0
		}
		end := i + 120
		if end >= len(lines) {
			end = len(lines) - 1
		}
		if len(spans) > 0 && start <= lastEnd+20 {
			if end > spans[len(spans)-1].end {
				spans[len(spans)-1].end = end
				lastEnd = end
			}
		} else {
			spans = append(spans, span{start: start, end: end})
			lastEnd = end
		}
		if len(spans) >= 4 {
			break
		}
	}
	if len(spans) == 0 {
		return ""
	}
	var b strings.Builder
	for _, sp := range spans {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "-- lines %d-%d --\n", sp.start+1, sp.end+1)
		for i := sp.start; i <= sp.end && i < len(lines); i++ {
			fmt.Fprintf(&b, "%5d | %s\n", i+1, strings.TrimSuffix(lines[i], "\r"))
			if b.Len() >= maxChars {
				break
			}
		}
		if b.Len() >= maxChars {
			break
		}
	}
	return strings.TrimSpace(compactPromptSection(b.String(), maxChars))
}

func reviewFileSymbolExcerptsFromPath(path string, symbols []string, maxChars int) string {
	if maxChars <= 0 || len(symbols) == 0 {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	type numberedLine struct {
		number int
		text   string
	}
	var previous []numberedLine
	var b strings.Builder
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNumber := 0
	afterLines := 0
	matches := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		matched := reviewLineContainsAnySymbol(line, symbols)
		if matched {
			matches++
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			startLine := lineNumber
			if len(previous) > 0 {
				startLine = previous[0].number
			}
			fmt.Fprintf(&b, "-- lines %d-%d --\n", startLine, lineNumber+120)
			for _, prev := range previous {
				fmt.Fprintf(&b, "%5d | %s\n", prev.number, strings.TrimSuffix(prev.text, "\r"))
				if b.Len() >= maxChars {
					return strings.TrimSpace(compactPromptSection(b.String(), maxChars))
				}
			}
			fmt.Fprintf(&b, "%5d | %s\n", lineNumber, strings.TrimSuffix(line, "\r"))
			afterLines = 120
		} else if afterLines > 0 {
			fmt.Fprintf(&b, "%5d | %s\n", lineNumber, strings.TrimSuffix(line, "\r"))
			afterLines--
		}
		previous = append(previous, numberedLine{number: lineNumber, text: line})
		if len(previous) > 80 {
			previous = previous[len(previous)-80:]
		}
		if b.Len() >= maxChars || matches >= 4 {
			break
		}
	}
	return strings.TrimSpace(compactPromptSection(b.String(), maxChars))
}

func reviewLineContainsAnySymbol(line string, symbols []string) bool {
	for _, symbol := range symbols {
		symbol = strings.TrimSpace(symbol)
		if symbol == "" {
			continue
		}
		if bytes.Contains([]byte(line), []byte(symbol)) || strings.Contains(strings.ToLower(line), strings.ToLower(symbol)) {
			return true
		}
	}
	return false
}

func collectSessionReviewEvidence(session *Session, evidence *ReviewEvidencePack, includeCodingHarness bool) {
	if session == nil || evidence == nil {
		return
	}
	if changed := currentTurnPatchTransactionChangedPaths(session); len(changed) > 0 {
		evidence.ChangedPaths = append(evidence.ChangedPaths, changed...)
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Patch transaction changed paths", strings.Join(changed, "\n"))
		evidence.Sources = append(evidence.Sources, "patch_transaction")
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
	if session.LastVerification != nil && verificationReportCoversCurrentPatch(session, *session.LastVerification, time.Time{}, evidence.ChangedPaths) {
		evidence.VerificationSummary = session.LastVerification.SummaryLine()
		evidence.VerificationFailed = session.LastVerification.HasFailures()
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Latest verification", compactPromptSection(session.LastVerification.RenderShort(), 4000))
		evidence.Sources = append(evidence.Sources, "verification")
	} else if session.LastVerification != nil {
		evidence.Warnings = append(evidence.Warnings, "latest session verification was ignored because it predates or does not cover the current patch transaction")
	}
	if includeCodingHarness && session.LastCodingHarnessReport != nil {
		report := *session.LastCodingHarnessReport
		report.Normalize()
		evidence.CodingHarnessSummary = report.RenderPromptSection()
		evidence.Text = appendReviewEvidenceSection(evidence.Text, "Latest coding harness", compactPromptSection(evidence.CodingHarnessSummary, 4000))
		evidence.Sources = append(evidence.Sources, "coding_harness")
	}
}

func reviewShouldIncludeCodingHarness(run ReviewRun) bool {
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") ||
		strings.EqualFold(strings.TrimSpace(run.Trigger), "post_change") {
		return false
	}
	target := strings.TrimSpace(run.Target)
	if strings.EqualFold(target, reviewTargetFinal) ||
		strings.EqualFold(target, reviewTargetGoal) ||
		strings.EqualFold(target, reviewTargetAnalysis) {
		return true
	}
	flow := strings.ToLower(strings.TrimSpace(run.Flow))
	return containsAny(flow, "final", "completion", "goal")
}

func collectVerificationHistoryReviewEvidence(history *VerificationHistoryStore, root string, session *Session, evidence *ReviewEvidencePack) {
	if history == nil || evidence == nil || strings.TrimSpace(evidence.VerificationSummary) != "" {
		return
	}
	latest, ok, err := history.Latest(root)
	if err != nil || !ok {
		return
	}
	if !verificationReportCoversCurrentPatch(session, latest.Report, latest.RecordedAt, evidence.ChangedPaths) {
		evidence.Warnings = append(evidence.Warnings, "latest verification history was ignored because it predates or does not cover the current patch transaction")
		return
	}
	evidence.VerificationSummary = latest.Report.SummaryLine()
	evidence.VerificationFailed = latest.Report.HasFailures()
	evidence.Text = appendReviewEvidenceSection(evidence.Text, "Latest verification", compactPromptSection(latest.Report.RenderShort(), 4000))
	evidence.Sources = append(evidence.Sources, "verification_history")
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

type reviewEvidenceSectionBlock struct {
	Title string
	Body  string
	Raw   string
}

func splitReviewEvidenceSections(text string) []reviewEvidenceSectionBlock {
	text = strings.TrimSpace(text)
	if text == "" || !strings.HasPrefix(text, "## ") {
		return nil
	}
	parts := strings.Split(text, "\n\n## ")
	sections := make([]reviewEvidenceSectionBlock, 0, len(parts))
	for idx, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx > 0 {
			part = "## " + part
		}
		title, body, ok := parseReviewEvidenceSection(part)
		if !ok {
			title = reviewEvidenceSectionTitle(part)
		}
		sections = append(sections, reviewEvidenceSectionBlock{
			Title: title,
			Body:  body,
			Raw:   part,
		})
	}
	return sections
}

func parseReviewEvidenceSection(section string) (string, string, bool) {
	section = strings.TrimSpace(section)
	if !strings.HasPrefix(section, "## ") {
		return "", "", false
	}
	lineEnd := strings.IndexByte(section, '\n')
	if lineEnd < 0 {
		return strings.TrimSpace(strings.TrimPrefix(section, "## ")), "", false
	}
	title := strings.TrimSpace(strings.TrimPrefix(section[:lineEnd], "## "))
	rest := strings.TrimSpace(section[lineEnd+1:])
	const prefix = "```text\n"
	const suffix = "\n```"
	if !strings.HasPrefix(rest, prefix) || !strings.HasSuffix(rest, suffix) {
		return title, strings.TrimSpace(rest), false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(rest, prefix), suffix)
	return title, body, true
}

func reviewEvidenceSectionTitle(section string) string {
	section = strings.TrimSpace(section)
	if !strings.HasPrefix(section, "## ") {
		return ""
	}
	lineEnd := strings.IndexByte(section, '\n')
	if lineEnd < 0 {
		return strings.TrimSpace(strings.TrimPrefix(section, "## "))
	}
	return strings.TrimSpace(strings.TrimPrefix(section[:lineEnd], "## "))
}

func appendRawReviewEvidenceSection(text string, section string) string {
	section = strings.TrimSpace(section)
	if section == "" {
		return text
	}
	if strings.TrimSpace(text) == "" {
		return section
	}
	return strings.TrimSpace(text) + "\n\n" + section
}

func compactPreWriteEvidencePreservingPriority(text string, maxChars int) (string, bool) {
	text = strings.TrimSpace(text)
	if maxChars <= 0 || len(text) <= maxChars {
		return text, false
	}
	sections := splitReviewEvidenceSections(text)
	if len(sections) == 0 {
		return compactPromptSection(text, maxChars), true
	}
	order := []string{
		"Provided diff",
		"Edit proposal",
		"Required repair findings from pre-fix review",
		"Pre-write required repair diff excerpts",
		"Pre-write required repair after excerpts",
		"Pre-write function body excerpt",
		"Pre-write current file context",
	}
	used := make([]bool, len(sections))
	ordered := make([]reviewEvidenceSectionBlock, 0, len(sections))
	for _, title := range order {
		for idx, section := range sections {
			if used[idx] || !reviewEvidenceSectionTitleMatches(section.Title, title) {
				continue
			}
			used[idx] = true
			ordered = append(ordered, section)
		}
	}
	for idx, section := range sections {
		if used[idx] {
			continue
		}
		ordered = append(ordered, section)
	}
	var out string
	compacted := false
	for _, section := range ordered {
		candidate := appendRawReviewEvidenceSection(out, section.Raw)
		if len(candidate) <= maxChars {
			out = candidate
			continue
		}
		if preWriteEvidenceSectionIsRequired(section.Title) {
			remaining := maxChars - len(strings.TrimSpace(out))
			if strings.TrimSpace(out) != "" {
				remaining -= 2
			}
			if remaining > 512 {
				out = appendRawReviewEvidenceSection(out, compactReviewEvidenceSectionBlock(section, remaining))
			}
		}
		compacted = true
	}
	if strings.TrimSpace(out) == "" {
		return compactPromptSection(text, maxChars), true
	}
	return out, compacted
}

func preWriteEvidenceSectionIsRequired(title string) bool {
	normalized := strings.ToLower(strings.TrimSpace(title))
	switch normalized {
	case "provided diff",
		"edit proposal",
		"required repair findings from pre-fix review":
		return true
	default:
		return strings.HasPrefix(normalized, "pre-write function body excerpt") ||
			strings.HasPrefix(normalized, "pre-write current file context")
	}
}

func reviewEvidenceSectionTitleMatches(actual string, expected string) bool {
	actual = strings.ToLower(strings.TrimSpace(actual))
	expected = strings.ToLower(strings.TrimSpace(expected))
	if actual == expected {
		return true
	}
	return expected != "" && strings.HasPrefix(actual, expected+":")
}

func compactReviewEvidenceSectionBlock(section reviewEvidenceSectionBlock, maxChars int) string {
	if maxChars <= 0 || len(section.Raw) <= maxChars {
		return section.Raw
	}
	title := strings.TrimSpace(section.Title)
	body := strings.TrimSpace(section.Body)
	if title == "" || body == "" {
		return compactPromptSectionPreserveHeadTail(section.Raw, maxChars)
	}
	overhead := len("## "+title+"\n\n```text\n\n```") + 16
	bodyBudget := maxChars - overhead
	if bodyBudget < 256 {
		return compactPromptSectionPreserveHeadTail(section.Raw, maxChars)
	}
	return appendReviewEvidenceSection("", title, compactPromptSectionPreserveHeadTail(body, bodyBudget))
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
		if !shouldSkipMCPReviewFile(path) && !reviewScopeCandidatePathLooksSynthetic(path) {
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
