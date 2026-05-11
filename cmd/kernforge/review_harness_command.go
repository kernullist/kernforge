package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (rt *runtimeState) handleReviewCommand(args string) error {
	args = strings.TrimSpace(args)
	fields := splitCommandFields(args)
	if len(fields) > 0 {
		switch strings.ToLower(fields[0]) {
		case "models":
			return rt.handleReviewModelsCommand(strings.TrimSpace(strings.TrimPrefix(args, fields[0])))
		case "waive", "waivers":
			return rt.handleReviewWaiverCommand(args)
		}
	}
	opts := parseReviewCommandOptions(args)
	if strings.EqualFold(opts.Target, reviewTargetPR) && reviewCommandHasPRWriteOptions(args) {
		if _, err := rt.runReviewCommand(opts); err != nil {
			return err
		}
		if blocked, feedback := rt.runtimeGateFeedbackForAction(runtimeGateActionMCPWrite); blocked {
			return fmt.Errorf("%s", feedback)
		}
		return rt.handlePRReviewAutomationCommand(reviewPRArgsFromReviewArgs(args))
	}
	run, err := rt.runReviewCommand(opts)
	if err != nil {
		return err
	}
	if strings.EqualFold(run.Target, reviewTargetPR) && !reviewCommandHasPRWriteOptions(args) {
		_ = rt.handlePRReviewAutomationCommand(reviewPRArgsFromReviewArgs(args))
	}
	rt.printReviewRun(run)
	return nil
}

func (rt *runtimeState) printReviewRun(run ReviewRun) {
	fmt.Fprintln(rt.writer, rt.ui.section(reviewRunLocalizedText(rt.cfg, run, "Review", "리뷰")))
	switch run.Gate.Verdict {
	case reviewVerdictApproved:
		fmt.Fprintln(rt.writer, rt.ui.successLine(renderReviewCLIResult(rt.cfg, run)))
	case reviewVerdictApprovedWithWarnings, reviewVerdictNeedsRevision, reviewVerdictBlocked, reviewVerdictInsufficientEvidence:
		fmt.Fprintln(rt.writer, rt.ui.warnLine(renderReviewCLIResult(rt.cfg, run)))
	default:
		fmt.Fprintln(rt.writer, rt.ui.errorLine(renderReviewCLIResult(rt.cfg, run)))
	}
}

func (rt *runtimeState) runReviewCommand(opts ReviewHarnessOptions) (ReviewRun, error) {
	return rt.runReviewCommandWithContext(context.Background(), opts)
}

func (rt *runtimeState) runReviewCommandWithContext(ctx context.Context, opts ReviewHarnessOptions) (ReviewRun, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if strings.TrimSpace(opts.Trigger) == "" {
		opts.Trigger = "explicit_command"
	}
	if !opts.IncludeGitDiff && !opts.IncludeFileContents && strings.TrimSpace(opts.ProvidedDiff) == "" && strings.TrimSpace(opts.ProvidedCode) == "" {
		opts.IncludeGitDiff = true
	}
	return runReviewHarness(ctx, rt, opts)
}

func parseReviewCommandOptions(args string) ReviewHarnessOptions {
	fields := splitCommandFields(args)
	opts := ReviewHarnessOptions{
		Target:          reviewTargetAuto,
		Request:         args,
		IncludeGitDiff:  true,
		MaxContextChars: 60000,
		RawArgs:         args,
	}
	var requestParts []string
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		lower := strings.ToLower(field)
		if strings.HasPrefix(lower, "--mode=") {
			opts.Mode = strings.TrimSpace(field[len("--mode="):])
			continue
		}
		if lower == "--mode" && i+1 < len(fields) {
			i++
			opts.Mode = fields[i]
			continue
		}
		if lower == "--no-model" {
			opts.NoModel = true
			continue
		}
		if lower == "--no-follow-up" {
			opts.AutoFollowUp = "off"
			continue
		}
		if lower == "--follow-up" {
			opts.AutoFollowUp = "safe"
			continue
		}
		if strings.HasPrefix(lower, "--path=") {
			opts.Paths = append(opts.Paths, strings.TrimSpace(field[len("--path="):]))
			continue
		}
		if lower == "--path" && i+1 < len(fields) {
			i++
			opts.Paths = append(opts.Paths, fields[i])
			continue
		}
		if strings.HasPrefix(lower, "--max-context-chars=") {
			if n, ok := parsePositiveInt(strings.TrimSpace(field[len("--max-context-chars="):])); ok == nil && n > 0 {
				opts.MaxContextChars = n
			}
			continue
		}
		if strings.HasPrefix(field, "--") {
			continue
		}
		if opts.Target == reviewTargetAuto {
			normalized := normalizeReviewTarget(field)
			switch normalized {
			case reviewTargetChange, reviewTargetPlan, reviewTargetSelection, reviewTargetPR, reviewTargetFinal, reviewTargetGoal, reviewTargetAnalysis, reviewTargetSourceAnalysis:
				opts.Target = normalized
				continue
			}
		}
		requestParts = append(requestParts, field)
	}
	if len(requestParts) > 0 {
		opts.Request = strings.Join(requestParts, " ")
	} else if opts.Target != reviewTargetAuto {
		opts.Request = ""
	}
	opts.Paths = mcpReviewCleanPaths(opts.Paths)
	return opts
}

func (rt *runtimeState) handleReviewModelsCommand(args string) error {
	fields := splitCommandFields(args)
	if len(fields) == 0 {
		rt.printReviewModelsStatus()
		if rt.interactive {
			fmt.Fprintln(rt.writer)
			return rt.configureReviewModelInteractive("")
		}
		return nil
	}
	if strings.EqualFold(fields[0], "status") || strings.EqualFold(fields[0], "show") || strings.EqualFold(fields[0], "list") {
		rt.printReviewModelsStatus()
		return nil
	}
	switch strings.ToLower(fields[0]) {
	case "clear":
		if len(fields) < 2 {
			if rt.interactive {
				return rt.clearReviewModelInteractive()
			}
			return fmt.Errorf("usage: /review models clear <role>")
		}
		return rt.clearReviewModelRole(fields[1])
	default:
		return rt.configureReviewModelFromFields(fields)
	}
}

func (rt *runtimeState) printReviewModelsStatus() {
	reviewCfg := configReviewHarness(rt.cfg)
	fmt.Fprintln(rt.writer, rt.ui.section("Review Models"))
	fmt.Fprintln(rt.writer, rt.ui.infoLine("Use /review models to choose a reviewer role/provider/model by number."))
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.section("Automatic Review"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("after_change", reviewSettingLine(reviewBoolLabel(*reviewCfg.AutoAfterChange), "review code-changing agent edits by default")))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("after_goal_iteration", reviewSettingLine(reviewBoolLabel(*reviewCfg.AutoAfterGoalIteration), "review autonomous goal iterations before continuing")))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("before_git_write", reviewSettingLine(reviewBoolLabel(*reviewCfg.AutoBeforeGitWrite), "gate commit/push/write-side git actions with review")))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("follow_up", reviewSettingLine(reviewCfg.AutoFollowUp, "allow safe next-command recommendations after review")))
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.section("Reviewer Roles"))
	for _, option := range reviewModelRoleChoices() {
		label, source := reviewRoleModelLabelAndSource(rt.cfg, reviewCfg, option.Role)
		for _, line := range reviewRoleDisplayLines(option, label, source, false) {
			fmt.Fprintln(rt.writer, rt.ui.info(line))
		}
	}
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Direct form: /review models security openai-api gpt-5.4"))
}

func reviewBoolLabel(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func reviewSettingLine(value string, description string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "unset"
	}
	return value + " - " + strings.TrimSpace(description)
}

func reviewRoleDisplayLines(option reviewModelRoleChoice, label string, source string, numbered bool) []string {
	name := option.Token
	if numbered {
		name = option.Number + ". " + option.Token
	}
	nameWidth := 21
	prefix := "  "
	nameCell := padDisplayRight(name, nameWidth)
	continuation := strings.Repeat(" ", len(prefix)+nameWidth)
	return []string{
		prefix + nameCell + "route   " + reviewRoleRouteLine(label, source),
		continuation + "purpose " + reviewModelRoleDescription(option.Role),
	}
}

func reviewRoleRouteLine(label string, source string) string {
	route := "not configured"
	switch strings.TrimSpace(source) {
	case "role":
		route = "configured: " + label
	case "primary_reviewer":
		route = "inherits primary: " + label
	case "main":
		route = "follows main: " + label
	}
	return route
}

func reviewModelRoleDescription(role string) string {
	switch normalizeReviewRole(role) {
	case "primary_reviewer":
		return "general reviewer and fallback for roles without a dedicated model"
	case "security_reviewer":
		return "security boundary, abuse-risk, kernel, driver, and sensitive-path review"
	case "false_positive_reviewer":
		return "anti-cheat detection quality, telemetry noise, and false-positive risk review"
	case "design_reviewer":
		return "architecture, core-build direction, complexity, and long-term shape review"
	case "regression_reviewer":
		return "behavior compatibility, OS/version drift, and refactor-risk review"
	case "test_reviewer":
		return "verification plan, coverage gap, and reproducibility review"
	case "final_gate_reviewer":
		return "last gate for final answers, merge candidates, and goal completion"
	default:
		return "review role"
	}
}

type reviewModelRoleChoice struct {
	Number string
	Token  string
	Role   string
	Label  string
}

func reviewModelRoleChoices() []reviewModelRoleChoice {
	return []reviewModelRoleChoice{
		{Number: "1", Token: "primary", Role: "primary_reviewer", Label: "primary"},
		{Number: "2", Token: "security", Role: "security_reviewer", Label: "security"},
		{Number: "3", Token: "false-positive", Role: "false_positive_reviewer", Label: "false-positive"},
		{Number: "4", Token: "design", Role: "design_reviewer", Label: "design"},
		{Number: "5", Token: "regression", Role: "regression_reviewer", Label: "regression"},
		{Number: "6", Token: "test", Role: "test_reviewer", Label: "test"},
		{Number: "7", Token: "final", Role: "final_gate_reviewer", Label: "final"},
	}
}

func reviewModelRoleTokens() []string {
	roles := reviewModelRoleChoices()
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		out = append(out, role.Token)
	}
	return out
}

func resolveReviewModelRoleChoice(choice string) (reviewModelRoleChoice, bool) {
	trimmed := strings.TrimSpace(choice)
	normalized := normalizeReviewRole(trimmed)
	for _, option := range reviewModelRoleChoices() {
		if strings.EqualFold(trimmed, option.Number) ||
			strings.EqualFold(trimmed, option.Token) ||
			normalized == option.Role {
			return option, true
		}
	}
	return reviewModelRoleChoice{}, false
}

func (rt *runtimeState) configureReviewModelFromFields(fields []string) error {
	if len(fields) == 0 {
		if rt.interactive {
			return rt.configureReviewModelInteractive("")
		}
		return fmt.Errorf("usage: /review models <role> [provider] [model] [reasoning_effort]")
	}
	roleChoice, ok := resolveReviewModelRoleChoice(fields[0])
	if !ok {
		return fmt.Errorf("unknown review model role: %s", fields[0])
	}
	if len(fields) == 1 {
		if rt.interactive {
			return rt.configureReviewModelInteractive(roleChoice.Role)
		}
		return fmt.Errorf("usage: /review models %s <provider> <model> [reasoning_effort]", roleChoice.Token)
	}
	provider, ok := resolveProviderChoice(fields[1])
	if !ok {
		return fmt.Errorf("unknown provider: %s", fields[1])
	}
	if len(fields) == 2 {
		if rt.interactive {
			return rt.configureReviewModelInteractiveForProvider(roleChoice.Role, provider)
		}
		return fmt.Errorf("usage: /review models %s %s <model> [reasoning_effort]", roleChoice.Token, provider)
	}
	modelParts := append([]string(nil), fields[2:]...)
	effort := ""
	if len(modelParts) > 1 && validReasoningEffort(modelParts[len(modelParts)-1]) {
		effort = modelParts[len(modelParts)-1]
		modelParts = modelParts[:len(modelParts)-1]
	}
	model := strings.TrimSpace(strings.Join(modelParts, " "))
	if model == "" {
		return fmt.Errorf("model is required")
	}
	return rt.activateReviewModelRole(roleChoice.Role, provider, model, "", "", effort)
}

func (rt *runtimeState) configureReviewModelInteractive(role string) error {
	role = normalizeReviewRole(role)
	if _, ok := resolveReviewModelRoleChoice(role); !ok {
		fmt.Fprintln(rt.writer, rt.ui.section("Review Model Role"))
		reviewCfg := configReviewHarness(rt.cfg)
		for _, option := range reviewModelRoleChoices() {
			label, source := reviewRoleModelLabelAndSource(rt.cfg, reviewCfg, option.Role)
			for _, line := range reviewRoleDisplayLines(option, label, source, true) {
				fmt.Fprintln(rt.writer, rt.ui.info(line))
			}
		}
		for {
			choice, err := rt.promptValue("Select review role", "1")
			if err != nil {
				return err
			}
			selected, ok := resolveReviewModelRoleChoice(choice)
			if ok {
				role = selected.Role
				break
			}
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Choose a role number or name."))
		}
	}
	return rt.configureReviewModelInteractiveForProvider(role, "")
}

func (rt *runtimeState) configureReviewModelInteractiveForProvider(role string, providerArg string) error {
	roleChoice, ok := resolveReviewModelRoleChoice(role)
	if !ok {
		return fmt.Errorf("unknown review model role: %s", role)
	}
	provider := normalizeProviderName(providerArg)
	reviewCfg := configReviewHarness(rt.cfg)
	current := reviewCfg.RoleModels[roleChoice.Role]
	if provider == "" {
		fmt.Fprintln(rt.writer, rt.ui.section("Set Review "+strings.Title(roleChoice.Label)+" Model"))
		rt.printProviderChoiceOptions()
		defaultProvider := current.Provider
		if strings.TrimSpace(defaultProvider) == "" {
			defaultProvider = rt.cfg.Provider
		}
		defaultChoice := defaultProviderChoice(defaultProvider)
		choice, err := rt.promptValue("Select provider", defaultChoice)
		if err != nil {
			return err
		}
		if strings.TrimSpace(choice) == "" {
			choice = defaultChoice
		}
		resolved, ok := resolveProviderChoice(choice)
		if !ok {
			return fmt.Errorf("unknown provider: %s", choice)
		}
		provider = resolved
	}
	nextModel := ""
	nextBaseURL := ""
	nextAPIKey := ""
	if strings.EqualFold(normalizeProviderName(current.Provider), provider) {
		nextModel = current.Model
		nextBaseURL = current.BaseURL
		nextAPIKey = current.APIKey
	}
	if strings.TrimSpace(nextAPIKey) == "" {
		nextAPIKey = rt.providerAPIKey(provider)
	}
	scope := "review " + roleChoice.Label
	switch provider {
	case "ollama":
		defaultURL := nextBaseURL
		if strings.TrimSpace(defaultURL) == "" {
			defaultURL = normalizeOllamaBaseURL("")
		}
		url, err := rt.promptValue("Ollama URL", defaultURL)
		if err != nil {
			return err
		}
		url = normalizeOllamaBaseURL(url)
		models, normalized, fetchErr := rt.fetchAndShowOllamaModels(url)
		if fetchErr != nil {
			return fmt.Errorf("could not connect to Ollama server: %w", fetchErr)
		}
		if len(models) == 0 {
			return fmt.Errorf("no Ollama models were returned by %s", normalized)
		}
		rt.ollamaModels = models
		selected, err := rt.chooseOllamaModel(models)
		if err != nil {
			return err
		}
		nextModel = selected.Name
		nextBaseURL = normalized
	case "lmstudio", "vllm", "llama.cpp":
		model, normalized, apiKey, err := rt.configureLocalOpenAICompatibleModel(provider, nextModel, nextBaseURL, nextAPIKey, scope)
		if err != nil {
			return err
		}
		nextModel = model
		nextBaseURL = normalized
		nextAPIKey = apiKey
	case "anthropic", "openai", "openrouter", "deepseek", "opencode", "opencode-go":
		if strings.TrimSpace(nextAPIKey) == "" {
			keyPrompt := providerDisplayName(provider) + " API key (for " + scope + ")"
			apiKey, err := rt.promptRequiredValue(keyPrompt, "")
			if err != nil {
				return err
			}
			nextAPIKey = apiKey
		}
		if provider == "openrouter" {
			nextBaseURL = normalizeOpenRouterBaseURL(nextBaseURL)
			models, normalized, err := rt.fetchAndShowOpenRouterModels(nextBaseURL, nextAPIKey)
			if err != nil {
				return err
			}
			selected, err := rt.chooseOpenRouterModel(models)
			if err != nil {
				return err
			}
			nextModel = selected.ID
			nextBaseURL = normalized
		} else if provider == "deepseek" {
			nextBaseURL = normalizeDeepSeekBaseURL(nextBaseURL)
			model, normalized, err := rt.fetchAndChooseDeepSeekModel(nextBaseURL, nextAPIKey, nextModel)
			if err != nil {
				return err
			}
			nextModel = model
			nextBaseURL = normalized
		} else if isOpenCodeProvider(provider) {
			nextBaseURL = normalizeOpenCodeProviderBaseURL(provider, nextBaseURL)
			models, normalized, err := rt.fetchAndShowOpenCodeModelsForProvider(provider, nextBaseURL, nextAPIKey)
			if err != nil {
				return err
			}
			nextModel, err = rt.chooseOpenCodeModelForProvider(provider, models, nextModel)
			if err != nil {
				return err
			}
			nextBaseURL = normalized
		} else if provider == "anthropic" {
			model, err := rt.chooseAnthropicModel(nextModel)
			if err != nil {
				return err
			}
			nextModel = model
		} else {
			model, err := rt.chooseOpenAIModel(nextModel)
			if err != nil {
				return err
			}
			nextModel = model
		}
	case "openai-codex":
		model, err := rt.chooseOpenAICodexModel(nextModel)
		if err != nil {
			return err
		}
		nextModel = model
		nextBaseURL = normalizeOpenAICodexBaseURL(nextBaseURL)
		nextAPIKey = ""
	case "codex-cli":
		model, err := rt.chooseCodexCLIModel(nextModel)
		if err != nil {
			return err
		}
		nextModel = model
		nextBaseURL = ""
		nextAPIKey = ""
	case "anthropic-claude-cli":
		if err := rt.configureClaudeCLICommandInteractive(); err != nil {
			return err
		}
		model, err := rt.chooseClaudeCLIModel(nextModel)
		if err != nil {
			return err
		}
		nextModel = model
		nextBaseURL = ""
		nextAPIKey = ""
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}
	return rt.activateReviewModelRole(roleChoice.Role, provider, nextModel, nextBaseURL, nextAPIKey, "")
}

func (rt *runtimeState) activateReviewModelRole(role string, provider string, model string, baseURL string, apiKey string, effort string) error {
	roleChoice, ok := resolveReviewModelRoleChoice(role)
	if !ok {
		return fmt.Errorf("unknown review model role: %s", role)
	}
	provider = normalizeProviderName(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return fmt.Errorf("provider and model are required")
	}
	if strings.TrimSpace(apiKey) == "" {
		apiKey = rt.providerAPIKey(provider)
	}
	if isOpenCodeProvider(provider) {
		resolvedModel, resolvedBaseURL, err := rt.resolveOpenCodeModelForProviderAPIKey(provider, model, baseURL, apiKey, "review "+roleChoice.Label)
		if err != nil {
			return err
		}
		model = resolvedModel
		baseURL = resolvedBaseURL
	}
	baseURL = normalizeProfileBaseURL(provider, baseURL)
	reviewCfg := configReviewHarness(rt.cfg)
	current := reviewCfg.RoleModels[roleChoice.Role]
	nextEffort := ""
	defaultedEffort := false
	if strings.TrimSpace(effort) != "" {
		normalized, err := validateReasoningEffortTarget(provider, effort, "review "+roleChoice.Label+" model")
		if err != nil {
			return err
		}
		nextEffort, _ = reviewReasoningEffortOrDefaultForProvider(provider, normalized)
	} else if sameProfileRoute(current.Provider, current.Model, current.BaseURL, provider, model, baseURL) {
		nextEffort, _ = reviewReasoningEffortOrDefaultForProvider(provider, current.ReasoningEffort)
	}
	if nextEffort == "" {
		nextEffort, defaultedEffort = reviewReasoningEffortOrDefaultForProvider(provider, "")
	}
	if reviewCfg.RoleModels == nil {
		reviewCfg.RoleModels = map[string]ReviewModelConfig{}
	}
	reviewCfg.RoleModels[roleChoice.Role] = ReviewModelConfig{
		Provider:        provider,
		Model:           model,
		BaseURL:         normalizeOptionalProfileBaseURL(provider, baseURL),
		APIKey:          apiKey,
		ReasoningEffort: nextEffort,
	}
	rt.cfg.Review = reviewCfg
	rt.storeProviderKey(provider, apiKey)
	rt.syncAgentReviewerClientFromConfig()
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	rt.printReviewReasoningEffortDefaultNotice("review "+roleChoice.Label+" model", defaultedEffort)
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Review %s set: %s", roleChoice.Label, formatProviderModelEffortLabel(provider, model, nextEffort))))
	return nil
}

func (rt *runtimeState) printReviewReasoningEffortDefaultNotice(target string, defaulted bool) {
	if !defaulted || rt == nil || rt.writer == nil {
		return
	}
	fmt.Fprintln(rt.writer, rt.ui.infoLine("review reasoning_effort was undefined; defaulted to high for "+target+"."))
}

func (rt *runtimeState) clearReviewModelInteractive() error {
	fmt.Fprintln(rt.writer, rt.ui.section("Clear Review Model"))
	reviewCfg := configReviewHarness(rt.cfg)
	for _, option := range reviewModelRoleChoices() {
		label, source := reviewRoleModelLabelAndSource(rt.cfg, reviewCfg, option.Role)
		for _, line := range reviewRoleDisplayLines(option, label, source, true) {
			fmt.Fprintln(rt.writer, rt.ui.info(line))
		}
	}
	for {
		choice, err := rt.promptValue("Select review role to clear", "1")
		if err != nil {
			return err
		}
		selected, ok := resolveReviewModelRoleChoice(choice)
		if ok {
			return rt.clearReviewModelRole(selected.Role)
		}
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Choose a role number or name."))
	}
}

func (rt *runtimeState) clearReviewModelRole(role string) error {
	roleChoice, ok := resolveReviewModelRoleChoice(role)
	if !ok {
		return fmt.Errorf("unknown review model role: %s", role)
	}
	reviewCfg := configReviewHarness(rt.cfg)
	delete(reviewCfg.RoleModels, roleChoice.Role)
	rt.cfg.Review = reviewCfg
	rt.syncAgentReviewerClientFromConfig()
	if err := rt.saveUserConfigReplacingReviewRoleModels(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Cleared review "+roleChoice.Label+" model"))
	return nil
}

func (rt *runtimeState) handleReviewWaiverCommand(args string) error {
	fields := splitCommandFields(args)
	if len(fields) == 0 || strings.EqualFold(fields[0], "waivers") {
		return rt.printReviewWaivers()
	}
	if len(fields) < 2 || !strings.EqualFold(fields[0], "waive") {
		return fmt.Errorf("usage: /review waive <finding-id> --reason <text>")
	}
	findingID := fields[1]
	reason := reviewWaiverReason(args)
	if reason == "" {
		return fmt.Errorf("usage: /review waive <finding-id> --reason <text>")
	}
	root := workspaceSnapshotRoot(rt.workspace)
	run, _, ok, err := loadLatestReviewRun(root)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no latest review run found")
	}
	var matched *ReviewFinding
	for i := range run.Findings {
		if strings.EqualFold(run.Findings[i].ID, findingID) {
			matched = &run.Findings[i]
			break
		}
	}
	if matched == nil {
		return fmt.Errorf("finding not found in latest review: %s", findingID)
	}
	allowed := !strings.EqualFold(matched.Category, "security") && !strings.EqualFold(matched.Category, "bypass_surface")
	waiver := ReviewWaiver{
		ID:        fmt.Sprintf("waiver-%s", time.Now().Format("20060102-150405.000")),
		FindingID: matched.ID,
		Reason:    reason,
		Actor:     "local_user",
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
		Scope:     run.ID,
		Allowed:   allowed,
		Status:    "active",
	}
	if !allowed {
		waiver.Status = "rejected"
	}
	run.Waivers = append(run.Waivers, waiver)
	if allowed {
		run.Gate.WarningFindings = append(run.Gate.WarningFindings, matched.ID)
		run.Gate.BlockingFindings = removeStringCI(run.Gate.BlockingFindings, matched.ID)
		run.Gate.Verdict = reviewVerdictApprovedWithWarnings
		run.Gate.Reason = "blocking finding waived by explicit local user override"
		run.finalizeStatus(false)
	}
	if err := writeReviewRunArtifacts(root, &run); err != nil {
		return err
	}
	rt.session.recordReviewRun(run)
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	if !allowed {
		return fmt.Errorf("finding %s is not waiver-allowed by policy", findingID)
	}
	fmt.Fprintln(rt.writer, rt.ui.warnLine(fmt.Sprintf("Waived %s until %s", findingID, waiver.ExpiresAt.Format(time.RFC3339))))
	return nil
}

func (rt *runtimeState) printReviewWaivers() error {
	root := workspaceSnapshotRoot(rt.workspace)
	run, _, ok, err := loadLatestReviewRun(root)
	if err != nil {
		return err
	}
	if !ok || len(run.Waivers) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No review waivers recorded."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Review Waivers"))
	for _, waiver := range run.Waivers {
		fmt.Fprintf(rt.writer, "- %s finding=%s status=%s expires=%s reason=%s\n", waiver.ID, waiver.FindingID, waiver.Status, waiver.ExpiresAt.Format(time.RFC3339), waiver.Reason)
	}
	return nil
}

func reviewWaiverReason(args string) string {
	lower := strings.ToLower(args)
	idx := strings.Index(lower, "--reason")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(args[idx+len("--reason"):], "="))
}

func removeStringCI(items []string, value string) []string {
	var out []string
	for _, item := range items {
		if !strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(value)) {
			out = append(out, item)
		}
	}
	return out
}

func reviewCommandHasPRWriteOptions(args string) bool {
	lower := strings.ToLower(args)
	return strings.Contains(lower, "--draft-comments") ||
		strings.Contains(lower, "--post-comments") ||
		strings.Contains(lower, "--resolve-thread") ||
		strings.Contains(lower, "--draft-issue") ||
		strings.Contains(lower, "--create-issue")
}

func reviewPRArgsFromReviewArgs(args string) string {
	fields := splitCommandFields(args)
	var out []string
	inPR := false
	for _, field := range fields {
		if strings.EqualFold(field, "pr") {
			inPR = true
			out = append(out, "--github")
			continue
		}
		if inPR && strings.HasPrefix(field, "--") {
			out = append(out, field)
			continue
		}
		if inPR && len(out) > 0 {
			last := out[len(out)-1]
			if strings.EqualFold(last, "--resolve-thread") ||
				strings.EqualFold(last, "--label") ||
				strings.EqualFold(last, "--assignee") ||
				strings.EqualFold(last, "--milestone") {
				out = append(out, field)
			}
		}
	}
	return strings.Join(out, " ")
}

func renderReviewMCPResponse(run ReviewRun, maxChars int) string {
	return renderReviewMCPResponseWithLatestFreshness(run, run.Freshness, maxChars)
}

func renderReviewMCPResponseWithLatestFreshness(run ReviewRun, latestFreshness ReviewFreshness, maxChars int) string {
	recommended := map[string]any(nil)
	if len(run.Gate.NextCommands) > 0 {
		recommended = map[string]any{
			"command":               run.Gate.NextCommands[0].Command,
			"reason":                run.Gate.NextCommands[0].Reason,
			"when":                  run.Gate.NextCommands[0].When,
			"safety":                run.Gate.NextCommands[0].Safety,
			"auto_run":              run.Gate.NextCommands[0].AutoRun,
			"requires_confirmation": run.Gate.NextCommands[0].RequiresConfirmation,
			"client_hint":           run.Gate.NextCommands[0].ClientHint,
			"expected_result":       run.Gate.NextCommands[0].ExpectedResult,
		}
	}
	payload := map[string]any{
		"summary":                 run.Result.Summary,
		"review_id":               run.ID,
		"machine_status":          run.MachineStatus,
		"status_code":             run.ExitCode,
		"retryable":               run.ExitCode >= 2 && run.ExitCode <= 5,
		"request_analysis":        run.RequestAnalysis,
		"artifact_refs":           run.ArtifactRefs,
		"result":                  run.Result,
		"model_plan":              run.ModelPlan,
		"freshness":               run.Freshness,
		"latest_review_freshness": latestFreshness,
		"redaction":               run.Redaction,
		"edit_proposals":          run.EditProposals,
		"runtime_gate_ledger":     run.RuntimeGateLedger,
		"gate":                    run.Gate,
		"waivers":                 run.Waivers,
		"findings":                run.Findings,
		"changed_paths":           run.ChangeSet.ChangedPaths,
		"evidence_sources":        run.Evidence.Sources,
		"warnings":                run.Evidence.Warnings,
		"next_commands":           run.Gate.NextCommands,
		"recommended_command":     recommended,
		"follow_up_results":       run.NextCommandResults,
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return mcpLimitText("KernForge review\n\n```json\n"+string(data)+"\n```", maxChars)
}

func splitCommandFields(args string) []string {
	return strings.Fields(strings.TrimSpace(args))
}

func sortedReviewRoleNames(roleModels map[string]ReviewModelConfig) []string {
	var roles []string
	for role := range roleModels {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

func reviewLatestArtifactPath(root string) string {
	return filepath.Join(reviewArtifactRoot(root), "latest.md")
}
