package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (rt *runtimeState) handleFindRootCauseCommand(args string) error {
	options := parseFindRootCauseCommandArgs(args)
	problem := strings.TrimSpace(options.Problem)
	if problem == "" {
		fmt.Fprintln(rt.writer, rt.ui.section("Find Root Cause"))
		fmt.Fprintln(rt.writer, rt.ui.highlightCommands(rootCauseUsage()))
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, rt.ui.hintLine("Example: /find-root-cause 내 게임에서 파티원을 초대하고 추방하다 보면 파티원 제한 숫자를 넘어서서 파티원을 초대할 수 있게 돼"))
		fmt.Fprintln(rt.writer, rt.ui.hintLine("Example: /find-root-cause 내 Win32 서비스 프로세스가 sc stop으로 종료되지 않아"))
		return nil
	}
	sourceHints := rootCauseWorkspaceSourceHints(rt.workspace.Root, 1600)
	clarity := analyzeRootCausePromptClarityWithSourceHints(problem, sourceHints)
	if !clarity.Clear && rootCausePromptClarityIsBorderline(problem, clarity) && rt.agent != nil && rt.agent.Client != nil {
		if refined, ok := rt.refineRootCausePromptClarityWithModel(context.Background(), problem, clarity); ok {
			clarity = refined
		}
	}
	if !clarity.Clear {
		rt.printRootCausePromptClarification(problem, clarity)
		return nil
	}
	if rt.agent == nil || rt.agent.Client == nil {
		return fmt.Errorf("no model provider is configured")
	}

	analysisWorkspace := rt.workspace
	analysisCfg := configProjectAnalysis(rt.cfg, rt.workspace.BaseRoot)
	analysisCfg = rootCauseProjectAnalysisConfig(analysisCfg)
	analysisCfg, err := rt.prepareAnalysisDirectorySelection(analysisWorkspace.Root, analysisCfg)
	if err != nil {
		return err
	}

	goal := buildRootCauseGoal(problem)
	workerLabel := rt.session.Provider + " / " + rt.session.Model
	reviewerLabel := workerLabel
	if analysisCfg.WorkerProfile != nil {
		workerLabel = analysisCfg.WorkerProfile.Provider + " / " + analysisCfg.WorkerProfile.Model
	}
	if analysisCfg.ReviewerProfile != nil {
		reviewerLabel = analysisCfg.ReviewerProfile.Provider + " / " + analysisCfg.ReviewerProfile.Model
	} else if analysisCfg.WorkerProfile != nil {
		reviewerLabel = workerLabel
	}

	fmt.Fprintln(rt.writer, rt.ui.section("Find Root Cause"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("workspace", rt.session.WorkingDir))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("problem", problem))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_worker", workerLabel))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_reviewer", reviewerLabel))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("worker_limit", "1..8"))
	if len(options.PatternPackPaths) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("pattern_packs", strings.Join(options.PatternPackPaths, ", ")))
	}

	analyzer := newProjectAnalyzer(rt.cfg, rt.agent.Client, analysisWorkspace, func(status string) {
		fmt.Fprintln(rt.writer, rt.ui.hintLine(status))
	}, func(debug string) {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("root-cause: "+debug))
	})
	analyzer.analysisCfg = analysisCfg
	analyzer.rootCausePatternPacks = append([]string(nil), options.PatternPackPaths...)
	previewSnapshot, err := analyzer.scanProject()
	if err != nil {
		return err
	}
	estimatedConcurrency := analyzer.estimateAgentCount(previewSnapshot)
	estimatedTotalShards := analyzer.estimateShardCount(previewSnapshot, estimatedConcurrency)
	estimatedConcurrency = analyzer.effectiveShardConcurrency(estimatedConcurrency, estimatedTotalShards, "root-cause")
	fmt.Fprintln(rt.writer, rt.ui.statusKV("estimated_files", fmt.Sprintf("%d", previewSnapshot.TotalFiles)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("estimated_lines", fmt.Sprintf("%d", previewSnapshot.TotalLines)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("estimated_workers", fmt.Sprintf("%d", estimatedConcurrency)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("estimated_shards", fmt.Sprintf("%d", estimatedTotalShards)))
	fmt.Fprintln(rt.writer)

	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stopEscapeWatcher := startEscapeWatcher(cancel, rt.shouldHonorRequestCancel, rt.confirmRequestCancel)
	defer stopEscapeWatcher()

	rt.startThinkingIndicator()
	defer rt.stopThinkingIndicator()

	analyzer = newProjectAnalyzer(rt.cfg, rt.agent.Client, analysisWorkspace, func(status string) {
		if !rt.showTransientWhileThinking(rt.ui.hintLine(status)) {
			rt.printPersistentWhileThinking(rt.ui.hintLine(status))
		}
	}, func(debug string) {
		if !rt.showTransientWhileThinking(rt.ui.infoLine("root-cause: " + debug)) {
			rt.printPersistentWhileThinking(rt.ui.infoLine("root-cause: " + debug))
		}
	})
	analyzer.analysisCfg = analysisCfg
	analyzer.rootCausePatternPacks = append([]string(nil), options.PatternPackPaths...)
	run, err := analyzer.Run(requestCtx, goal, "root-cause")
	if err != nil {
		if requestCtx.Err() == context.Canceled {
			rt.noteRecentRequestCancel()
			return fmt.Errorf("root-cause analysis canceled by user")
		}
		return err
	}
	rt.clearThinkingDetails()

	rt.session.LastAnalysis = &run.Summary
	rt.session.Summary = mergeSessionSummaryWithAnalysis(rt.session.Summary, run)
	rt.session.LastAnalysisContextQuery = problem
	rt.session.LastAnalysisContextRunID = run.Summary.RunID
	if err := rt.store.Save(rt.session); err != nil {
		return err
	}

	rt.printPersistentWhileThinking(rt.ui.successLine(fmt.Sprintf("Root-cause analysis completed with %d shard(s).", run.Summary.TotalShards)))
	if run.Summary.ReviewFailures > 0 {
		rt.printPersistentWhileThinking(rt.ui.statusKV("review_failures", fmt.Sprintf("%d", run.Summary.ReviewFailures)))
	}
	rt.printPersistentWhileThinking(rt.ui.statusKV("output", run.Summary.OutputPath))
	rt.printPersistentWhileThinking(rt.ui.statusKV("dashboard", filepath.Join(analysisCfg.OutputDir, "latest", "dashboard.html")))
	rt.printAssistant(run.FinalDocument)
	if artifacts := renderAnalysisProjectArtifactPathsStyled(run, analysisCfg.OutputDir, rt.ui); strings.TrimSpace(artifacts) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, artifacts)
	}
	return nil
}

type findRootCauseCommandOptions struct {
	Problem          string
	PatternPackPaths []string
}

func parseFindRootCauseCommandArgs(args string) findRootCauseCommandOptions {
	fields := splitAnalysisCommandLine(strings.TrimSpace(args))
	options := findRootCauseCommandOptions{}
	problemStart := 0
	for problemStart < len(fields) {
		field := fields[problemStart]
		if strings.HasPrefix(field, "--pattern-pack=") {
			options.PatternPackPaths = append(options.PatternPackPaths, strings.TrimSpace(strings.TrimPrefix(field, "--pattern-pack=")))
			problemStart++
			continue
		}
		if field == "--pattern-pack" {
			if problemStart+1 < len(fields) {
				options.PatternPackPaths = append(options.PatternPackPaths, fields[problemStart+1])
				problemStart += 2
				continue
			}
			problemStart++
			continue
		}
		break
	}
	options.PatternPackPaths = analysisUniqueStrings(options.PatternPackPaths)
	options.Problem = strings.Join(fields[problemStart:], " ")
	return options
}

type rootCausePromptClarity struct {
	Clear            bool
	UnclearParts     []string
	SuggestedCommand string
	ModelChecked     bool
	ModelReason      string
}

func analyzeRootCausePromptClarity(problem string) rootCausePromptClarity {
	return analyzeRootCausePromptClarityWithSourceHints(problem, nil)
}

func analyzeRootCausePromptClarityWithSourceHints(problem string, sourceHints []string) rootCausePromptClarity {
	trimmed := strings.TrimSpace(problem)
	clarity := rootCausePromptClarity{
		Clear:            true,
		SuggestedCommand: rootCauseClarifiedPromptTemplate(""),
	}
	if trimmed == "" {
		clarity.Clear = false
		clarity.UnclearParts = append(clarity.UnclearParts, "No problem description was provided.")
		return clarity
	}

	hasSurface := rootCausePromptHasAffectedSurface(trimmed)
	hasTrigger := rootCausePromptHasTrigger(trimmed)
	hasObservedFailure := rootCausePromptHasObservedFailure(trimmed)
	hasExpectedBehavior := rootCausePromptHasExpectedBehavior(trimmed, hasObservedFailure, hasTrigger)
	if len([]rune(trimmed)) < 12 || len(strings.Fields(trimmed)) < 2 {
		clarity.UnclearParts = append(clarity.UnclearParts, "The symptom is too short to separate the affected code path from the failure.")
	}
	if !hasSurface {
		clarity.UnclearParts = append(clarity.UnclearParts, "Affected component, feature, process, command, API, or file type is unclear.")
	}
	if !hasTrigger {
		clarity.UnclearParts = append(clarity.UnclearParts, "Trigger or reproduction path is unclear, such as the input, command, event sequence, or state transition.")
	}
	if !hasObservedFailure {
		clarity.UnclearParts = append(clarity.UnclearParts, "Observed failure is unclear, such as what fails, hangs, exceeds a limit, is skipped, or is missing.")
	}
	if !hasExpectedBehavior {
		clarity.UnclearParts = append(clarity.UnclearParts, "Expected behavior or violated invariant is unclear.")
	}
	if len(sourceHints) > 0 {
		clarity = applyRootCauseSourceHintsToPromptClarity(trimmed, clarity, sourceHints)
	}
	clarity.UnclearParts = analysisUniqueStrings(clarity.UnclearParts)
	clarity.Clear = len(clarity.UnclearParts) == 0
	if !clarity.Clear {
		clarity.SuggestedCommand = rootCauseClarifiedPromptTemplateWithSourceHints(trimmed, sourceHints)
	}
	return clarity
}

func applyRootCauseSourceHintsToPromptClarity(problem string, clarity rootCausePromptClarity, sourceHints []string) rootCausePromptClarity {
	component := rootCauseBestSourceHintForProblem(problem, sourceHints)
	if component == "" {
		return clarity
	}
	filtered := []string{}
	for _, part := range clarity.UnclearParts {
		if strings.Contains(part, "Affected component") {
			continue
		}
		filtered = append(filtered, part)
	}
	if len(filtered) != len(clarity.UnclearParts) {
		clarity.ModelReason = strings.TrimSpace(strings.Join(analysisUniqueStrings([]string{clarity.ModelReason, "Source-aware hint matched workspace component: " + component}), " "))
	}
	clarity.UnclearParts = filtered
	return clarity
}

func rootCauseClarifiedPromptTemplateWithSourceHints(problem string, sourceHints []string) string {
	component := rootCauseBestSourceHintForProblem(problem, sourceHints)
	if component == "" {
		return rootCauseClarifiedPromptTemplate(problem)
	}
	observed := "<failure symptom>"
	if strings.TrimSpace(problem) != "" {
		observed = strings.TrimSpace(problem)
	}
	return "/find-root-cause In " + component + ", when <input/command/event sequence/state>, expected <normal behavior or invariant>, but observed " + observed + ". Frequency/env: <how often and where>. Repro/log/value: <exact prompt, API call, command, DB value, or log line>."
}

func rootCauseWorkspaceSourceHints(root string, limit int) []string {
	root = strings.TrimSpace(root)
	if root == "" || limit <= 0 {
		return nil
	}
	hints := []string{}
	skipDirs := map[string]struct{}{
		".git": {}, ".kernforge": {}, ".claude": {}, "node_modules": {}, "vendor": {}, "release": {}, "bin": {}, "obj": {}, ".vs": {},
	}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || len(hints) >= limit {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if _, skip := skipDirs[strings.ToLower(name)]; skip && path != root {
				return filepath.SkipDir
			}
			if path != root {
				hints = append(hints, name)
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		switch ext {
		case ".go", ".cpp", ".cc", ".cxx", ".c", ".h", ".hpp", ".cs", ".ts", ".tsx", ".js", ".jsx", ".py", ".ini", ".json", ".uproject", ".uplugin", ".Build.cs", ".Target.cs":
			hints = append(hints, strings.TrimSuffix(name, filepath.Ext(name)))
		}
		return nil
	})
	return limitStrings(analysisUniqueStrings(hints), limit)
}

func rootCauseBestSourceHintForProblem(problem string, sourceHints []string) string {
	if len(sourceHints) == 0 {
		return ""
	}
	problemTerms := rootCauseMeaningfulTokens(problem)
	best := ""
	bestScore := 0
	for _, hint := range sourceHints {
		score := rootCauseSourceHintScore(problemTerms, hint)
		if score > bestScore {
			bestScore = score
			best = hint
		}
	}
	if bestScore <= 0 {
		return ""
	}
	return best
}

func rootCauseSourceHintScore(problemTerms []string, hint string) int {
	hintLower := strings.ToLower(strings.TrimSpace(hint))
	if hintLower == "" {
		return 0
	}
	score := 0
	for _, term := range problemTerms {
		term = strings.ToLower(term)
		if len([]rune(term)) < 2 {
			continue
		}
		if strings.Contains(hintLower, term) {
			score += 4
		}
		for _, synonym := range rootCauseDomainSynonyms(term) {
			if strings.Contains(hintLower, synonym) {
				score += 3
			}
		}
	}
	return score
}

func rootCauseDomainSynonyms(term string) []string {
	term = strings.ToLower(term)
	switch {
	case strings.Contains(term, "초대") || term == "invite" || term == "party" || strings.Contains(term, "파티"):
		return []string{"party", "invite", "member", "session", "lobby"}
	case strings.Contains(term, "추방") || term == "kick":
		return []string{"kick", "remove", "member", "party"}
	case strings.Contains(term, "서비스") || term == "service" || term == "stop" || strings.Contains(term, "종료"):
		return []string{"service", "daemon", "control", "stop", "scm"}
	case strings.Contains(term, "문서") || term == "document" || term == "file":
		return []string{"document", "file", "write", "artifact"}
	default:
		return nil
	}
}

func (rt *runtimeState) printRootCausePromptClarification(problem string, clarity rootCausePromptClarity) {
	fmt.Fprintln(rt.writer, rt.ui.section("Find Root Cause"))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("The problem description is not specific enough for a reliable root-cause investigation."))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("problem", strings.TrimSpace(problem)))
	if strings.TrimSpace(clarity.ModelReason) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("clarity_check", strings.TrimSpace(clarity.ModelReason)))
	}
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, "Unclear parts:")
	for _, part := range clarity.UnclearParts {
		fmt.Fprintln(rt.writer, "- "+part)
	}
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, "Run the command again with a more precise prompt:")
	fmt.Fprintln(rt.writer, rt.ui.highlightCommands(clarity.SuggestedCommand))
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Example: /find-root-cause In the party system, after inviting and kicking members repeatedly, expected the party size limit to block new invites, but extra members can still be invited. Frequency: intermittent. Repro: invite 4, kick 1, invite 2."))
}

func rootCauseClarifiedPromptTemplate(problem string) string {
	observed := "<failure symptom>"
	if strings.TrimSpace(problem) != "" {
		observed = strings.TrimSpace(problem)
	}
	return "/find-root-cause In <component/feature>, when <input/command/event sequence/state>, expected <normal behavior or invariant>, but observed " + observed + ". Frequency/env: <how often and where>. Repro/log/value: <exact prompt, API call, command, DB value, or log line>."
}

func rootCausePromptClarityIsBorderline(problem string, clarity rootCausePromptClarity) bool {
	if clarity.Clear || len(clarity.UnclearParts) == 0 || len(clarity.UnclearParts) > 2 {
		return false
	}
	trimmed := strings.TrimSpace(problem)
	if len([]rune(trimmed)) < 18 || len(rootCauseMeaningfulTokens(trimmed)) < 2 {
		return false
	}
	return true
}

func (rt *runtimeState) refineRootCausePromptClarityWithModel(ctx context.Context, problem string, fallback rootCausePromptClarity) (rootCausePromptClarity, bool) {
	if rt == nil || rt.agent == nil || rt.agent.Client == nil {
		return fallback, false
	}
	resp, err := rt.agent.completeModelTurn(ctx, ChatRequest{
		Model:       rt.session.Model,
		System:      rootCausePromptClaritySystemPrompt(),
		Messages:    []Message{{Role: "user", Text: buildRootCausePromptClarityPrompt(problem, fallback)}},
		MaxTokens:   900,
		Temperature: 0,
		WorkingDir:  rt.session.WorkingDir,
		JSONMode:    true,
	})
	if err != nil {
		return fallback, false
	}
	refined, ok := parseRootCausePromptClarityPayload(resp.Message.Text)
	if !ok {
		return fallback, false
	}
	refined.ModelChecked = true
	if strings.TrimSpace(refined.SuggestedCommand) == "" {
		refined.SuggestedCommand = rootCauseClarifiedPromptTemplate(problem)
	}
	return refined, true
}

func rootCausePromptClaritySystemPrompt() string {
	return strings.TrimSpace(`
You classify whether a root-cause investigation prompt is specific enough to start code analysis.
Return strict JSON only:
{
  "clear": false,
  "unclear_parts": ["string"],
  "suggested_command": "/find-root-cause ...",
  "reason": "string"
}
Rules:
- clear=true only when the prompt identifies an affected surface, a trigger/reproduction path, observed failure, and expected behavior or violated invariant.
- Do not require perfect repro steps if the symptom is intermittent but the affected surface and failure are concrete.
- If clear=false, list the missing fields and provide a better /find-root-cause command preserving the user's original symptom.
`)
}

func buildRootCausePromptClarityPrompt(problem string, fallback rootCausePromptClarity) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Problem:\n%s\n\n", strings.TrimSpace(problem))
	if len(fallback.UnclearParts) > 0 {
		b.WriteString("Heuristic unclear parts:\n")
		for _, part := range fallback.UnclearParts {
			fmt.Fprintf(&b, "- %s\n", part)
		}
	}
	b.WriteString("\nReturn JSON only.")
	return strings.TrimSpace(b.String())
}

func parseRootCausePromptClarityPayload(raw string) (rootCausePromptClarity, bool) {
	type payload struct {
		Clear            bool     `json:"clear"`
		UnclearParts     []string `json:"unclear_parts"`
		SuggestedCommand string   `json:"suggested_command"`
		Reason           string   `json:"reason"`
	}
	for _, candidate := range analysisJSONCandidates(raw) {
		parsed := payload{}
		if err := json.Unmarshal([]byte(candidate), &parsed); err != nil {
			continue
		}
		clarity := rootCausePromptClarity{
			Clear:            parsed.Clear,
			UnclearParts:     analysisUniqueStrings(parsed.UnclearParts),
			SuggestedCommand: strings.TrimSpace(parsed.SuggestedCommand),
			ModelChecked:     true,
			ModelReason:      strings.TrimSpace(parsed.Reason),
		}
		if clarity.SuggestedCommand == "" {
			clarity.SuggestedCommand = rootCauseClarifiedPromptTemplate("")
		}
		return clarity, true
	}
	return rootCausePromptClarity{}, false
}

func rootCausePromptHasAffectedSurface(problem string) bool {
	tokens := rootCauseMeaningfulTokens(problem)
	return len(tokens) >= 2
}

func rootCausePromptHasTrigger(problem string) bool {
	return rootCausePromptContainsAny(problem, []string{
		"when", "after", "while", "during", "with", "using", "via", "on ", "if ", "trigger", "repro", "sequence",
		"하면", "때", "하다 보면", "보다 보면", "후", "뒤", "중", "동안", "에서", "입력", "호출", "요청", "실행", "클릭", "누르", "보내", "받", "생성", "삭제", "수정", "초대", "추방", "종료", "시작", "재시작", "sc stop",
	})
}

func rootCausePromptHasObservedFailure(problem string) bool {
	return rootCausePromptContainsAny(problem, []string{
		"fail", "failed", "failure", "error", "crash", "hang", "hung", "timeout", "stuck", "missing", "skip", "skipped", "wrong", "incorrect", "invalid", "exceed", "bypass", "over limit", "not ", "cannot", "can't", "doesn't", "won't", "never",
		"실패", "오류", "에러", "크래시", "죽", "멈", "타임아웃", "누락", "빠뜨", "건너", "이상", "잘못", "초과", "넘", "우회", "안돼", "안됨", "안 되", "않", "못", "되지 않아", "생성하질 않아", "종료되지 않아",
	})
}

func rootCausePromptHasExpectedBehavior(problem string, hasObservedFailure bool, hasTrigger bool) bool {
	if rootCausePromptContainsAny(problem, []string{
		"expected", "should", "must", "instead", "but", "invariant", "limit", "allowed", "blocked",
		"기대", "정상", "해야", "되어야", "되야", "원래", "대신", "하지만", "그런데", "제한", "허용", "차단", "막", "범위",
	}) {
		return true
	}
	if hasObservedFailure && hasTrigger {
		return rootCausePromptContainsAny(problem, []string{
			"not ", "cannot", "can't", "doesn't", "won't", "missing", "skip", "exceed", "bypass",
			"안돼", "안됨", "안 되", "않", "못", "누락", "초과", "넘", "우회", "생성하질 않아", "종료되지 않아",
		})
	}
	return false
}

func rootCausePromptContainsAny(problem string, needles []string) bool {
	lower := strings.ToLower(problem)
	for _, needle := range needles {
		if strings.Contains(lower, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func rootCauseMeaningfulTokens(problem string) []string {
	out := []string{}
	for _, field := range strings.Fields(strings.ToLower(problem)) {
		token := strings.Trim(field, " \t\r\n.,;:!?()[]{}<>\"'`~")
		token = strings.TrimSuffix(token, "가")
		token = strings.TrimSuffix(token, "이")
		token = strings.TrimSuffix(token, "을")
		token = strings.TrimSuffix(token, "를")
		token = strings.TrimSuffix(token, "은")
		token = strings.TrimSuffix(token, "는")
		token = strings.TrimSuffix(token, "에서")
		token = strings.TrimSuffix(token, "으로")
		token = strings.TrimSuffix(token, "로")
		if len([]rune(token)) < 2 || rootCausePromptIsGenericToken(token) {
			continue
		}
		out = append(out, token)
	}
	return analysisUniqueStrings(out)
}

func rootCausePromptIsGenericToken(token string) bool {
	switch token {
	case "내", "내가", "나의", "가끔", "자주", "항상", "계속", "문제", "버그", "오류", "에러", "실패", "이상", "증상", "발생", "발생해", "안돼", "안됨", "않아", "못해", "됨", "안", "때", "하면", "후", "뒤", "중", "동안", "expected", "actual", "problem", "bug", "error", "failure", "fails", "failed", "sometimes", "often", "always", "when", "after", "while", "during", "with", "using", "doesn't", "cannot", "can't", "not":
		return true
	default:
		return false
	}
}

func buildRootCauseGoal(problem string) string {
	trimmed := strings.TrimSpace(problem)
	if trimmed == "" {
		return ""
	}
	return "Find the most likely root cause(s) for this reported problem: " + trimmed + "\n\nAnalyze the workspace like a fuzzing-driven bug investigation. Select source files that can affect the symptom, plan 1 to 8 worker shards depending on source size and count, cap concurrent model calls by the configured model route policy, have each worker test assumptions about inputs and persisted state, require a five-stage causal chain (trigger, invalid state, state transition, missing guard, user-visible symptom), have reviewer passes reject weak claims, deep-verify reviewer-approved candidates, write an audit trail, and synthesize only plausible root causes with evidence, confidence, instrumentation, and verification steps."
}
