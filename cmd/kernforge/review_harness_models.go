package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

func planReviewModels(cfg Config, run ReviewRun) ReviewModelPlan {
	reviewCfg := configReviewHarness(cfg)
	plan := ReviewModelPlan{
		AssignedModels: map[string]string{},
	}
	plan.RequiredRoles = []string{"primary_reviewer"}
	plan.RequiredLenses, plan.OptionalLenses = reviewLensesForRun(run)
	if label := reviewMainModelLabel(cfg); label != "" {
		plan.AssignedModels["primary_reviewer"] = label
		provider, model := reviewPrimaryRouteProviderModelForRun(cfg)
		plan.CapabilityProfiles = append(plan.CapabilityProfiles, reviewModelCapabilityProfile("primary_reviewer", provider, model, reviewRoleReasoningEffortForRun(cfg, "primary_reviewer", run)))
	} else {
		label, _ := reviewRoleModelLabelAndSource(cfg, reviewCfg, "primary_reviewer")
		if label != "" {
			plan.AssignedModels["primary_reviewer"] = label
			provider, model := reviewRoleProviderModelForRun(cfg, "primary_reviewer")
			plan.CapabilityProfiles = append(plan.CapabilityProfiles, reviewModelCapabilityProfile("primary_reviewer", provider, model, reviewRoleReasoningEffortForRun(cfg, "primary_reviewer", run)))
		} else {
			plan.MissingRoles = append(plan.MissingRoles, "primary_reviewer")
		}
	}
	if label, role := reviewConfiguredCrossRouteLabelAndRole(cfg, reviewCfg, run); label != "" {
		plan.OptionalRoles = append(plan.OptionalRoles, "cross_reviewer")
		plan.AssignedModels["cross_reviewer"] = label
		provider, model := reviewRoleProviderModelForRun(cfg, role)
		plan.CapabilityProfiles = append(plan.CapabilityProfiles, reviewModelCapabilityProfile("cross_reviewer", provider, model, reviewRoleReasoningEffortForRun(cfg, "cross_reviewer", run)))
	}
	plan.MissingRoles = analysisUniqueStrings(plan.MissingRoles)
	plan.DegradedRoles = analysisUniqueStrings(plan.DegradedRoles)
	plan.RequiredLenses = analysisUniqueStrings(plan.RequiredLenses)
	plan.OptionalLenses = analysisUniqueStrings(plan.OptionalLenses)
	switch {
	case len(plan.RequiredRoles) == 0:
		plan.Strategy = "deterministic_only"
	case len(plan.RequiredRoles) == 1 && len(plan.OptionalRoles) == 0:
		plan.Strategy = "single"
	case len(plan.RequiredRoles)+len(plan.OptionalRoles) == 2:
		plan.Strategy = "dual"
	default:
		plan.Strategy = "multi"
	}
	if len(plan.AssignedModels) == 0 {
		plan.Strategy = "deterministic_only"
		plan.UserGuidance = append(plan.UserGuidance, "No reviewer model is configured; deterministic review only.")
	}
	if len(plan.RequiredLenses) > 0 || len(plan.OptionalLenses) > 0 {
		plan.UserGuidance = append(plan.UserGuidance, "Review specialization is applied as lenses, not separate reviewer routes: "+reviewLensSummary(plan.RequiredLenses, plan.OptionalLenses)+".")
	}
	return plan
}

func reviewLensesForRun(run ReviewRun) ([]string, []string) {
	required := []string{"correctness"}
	optional := []string{}
	switch {
	case strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger):
		if reviewRunSecuritySensitive(run) {
			required = append(required, "security")
		}
		if reviewRunFalsePositiveSensitive(run) {
			required = append(required, "false_positive")
		}
	case run.Flow == "plan_review":
		required = append(required, "design")
		if reviewRunSecuritySensitive(run) {
			optional = append(optional, "security")
		}
	case run.Flow == "security_review" || run.Mode == reviewModeSecurityHardening:
		required = append(required, "security")
		if reviewRunFalsePositiveSensitive(run) {
			required = append(required, "false_positive")
		}
		optional = append(optional, "test", "final_gate")
	case run.Mode == reviewModeRefactor:
		required = append(required, "regression")
	case run.Mode == reviewModeUIPolish:
		required = append(required, "design")
		if reviewRunUIPolishNeedsPrimaryCoverage(run) {
			required = append(required, "correctness")
		}
		optional = append(optional, "regression")
	case run.Mode == reviewModeLiveFix:
		required = append(required, "test")
		optional = append(optional, "regression")
	case run.Flow == "goal_review":
		required = append(required, "final_gate")
	case run.Flow == "pr_review":
		optional = append(optional, "test")
	default:
		if reviewRunSecuritySensitive(run) {
			required = append(required, "security")
		}
		if reviewRunFalsePositiveSensitive(run) {
			optional = append(optional, "false_positive")
		}
	}
	return analysisUniqueStrings(required), analysisUniqueStrings(optional)
}

func reviewLensSummary(required []string, optional []string) string {
	parts := []string{}
	if len(required) > 0 {
		parts = append(parts, "required="+strings.Join(required, ","))
	}
	if len(optional) > 0 {
		parts = append(parts, "optional="+strings.Join(optional, ","))
	}
	return strings.Join(parts, " ")
}

func reviewConfiguredCrossRouteLabelAndRole(cfg Config, reviewCfg ReviewHarnessConfig, run ReviewRun) (string, string) {
	for _, role := range reviewCrossRouteCandidateRoles(run) {
		label, source := reviewRoleModelLabelAndSource(cfg, reviewCfg, role)
		if label == "" || source == "main" {
			continue
		}
		if !reviewModelLabelDiffersFromMain(cfg, label) {
			continue
		}
		return label, role
	}
	return "", ""
}

func reviewCrossRouteCandidateRoles(run ReviewRun) []string {
	roles := []string{"cross_reviewer"}
	required, optional := reviewLensesForRun(run)
	for _, lens := range append(required, optional...) {
		switch normalizeReviewLens(lens) {
		case "design":
			roles = append(roles, "design_reviewer")
		case "security":
			roles = append(roles, "security_reviewer")
		case "false_positive":
			roles = append(roles, "false_positive_reviewer")
		case "regression":
			roles = append(roles, "regression_reviewer")
		case "test":
			roles = append(roles, "test_reviewer")
		case "final_gate":
			roles = append(roles, "final_gate_reviewer")
		}
	}
	roles = append(roles, "primary_reviewer")
	return analysisUniqueStrings(roles)
}

func reviewPrimaryRouteProviderModelForRun(cfg Config) (string, string) {
	if strings.TrimSpace(cfg.Provider) != "" || strings.TrimSpace(cfg.Model) != "" {
		return cfg.Provider, cfg.Model
	}
	return reviewRoleProviderModelForRun(cfg, "primary_reviewer")
}

func reviewRunUIPolishNeedsPrimaryCoverage(run ReviewRun) bool {
	if len(run.ChangeSet.ChangedPaths) == 0 {
		return true
	}
	for _, path := range run.ChangeSet.ChangedPaths {
		if !reviewPathLooksUIPolishOnly(path) {
			return true
		}
	}
	return false
}

func reviewPathLooksUIPolishOnly(path string) bool {
	normalized := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if normalized == "" {
		return false
	}
	if reviewPathIsExecutableSource(normalized) {
		return false
	}
	base := strings.ToLower(filepath.Base(normalized))
	if !reviewPathHasUIPolishAssetExtension(base) {
		return false
	}
	switch {
	case strings.Contains(normalized, "/ui/"),
		strings.Contains(normalized, "/views/"),
		strings.Contains(normalized, "/components/"),
		strings.Contains(normalized, "/assets/"),
		strings.Contains(normalized, "/branding/"),
		strings.Contains(normalized, "/styles/"),
		strings.Contains(normalized, "/css/"),
		strings.Contains(normalized, "/themes/"):
		return true
	case strings.HasPrefix(base, "ui.") || strings.HasPrefix(base, "ui_") || strings.HasPrefix(base, "ui-"):
		return true
	case strings.Contains(base, "_ui.") || strings.Contains(base, "-ui.") || strings.Contains(base, ".css"):
		return true
	default:
		return false
	}
}

func reviewPathHasUIPolishAssetExtension(base string) bool {
	ext := strings.ToLower(filepath.Ext(base))
	switch ext {
	case ".css", ".scss", ".sass", ".less",
		".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp", ".ico",
		".woff", ".woff2", ".ttf", ".otf", ".eot":
		return true
	default:
		return false
	}
}

func reviewPathIsExecutableSource(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c", ".cc", ".cpp", ".cxx",
		".h", ".hh", ".hpp", ".hxx",
		".go", ".rs", ".py", ".java", ".kt", ".kts",
		".cs", ".swift", ".m", ".mm", ".php", ".rb",
		".js", ".jsx", ".ts", ".tsx",
		".vue", ".svelte", ".dart":
		return true
	default:
		return false
	}
}

func reviewRunSecuritySensitive(run ReviewRun) bool {
	for _, pack := range run.PolicyPacks {
		switch strings.ToLower(strings.TrimSpace(pack)) {
		case "windows_kernel_driver", "anti_cheat_telemetry", "security_hardening":
			return true
		}
	}
	text := strings.ToLower(strings.Join(run.ChangeSet.ChangedPaths, " ") + " " + run.Objective)
	return containsAny(text,
		"security", "보안",
		"kernel", "커널", ".sys", "ioctl", "irql",
		"anti_cheat", "anti-cheat", "anticheat", "안티치트",
		"telemetry", "false_positive", "false-positive", "오탐",
		"bypass", "우회", "exploit", "token", "credential")
}

func reviewRunFalsePositiveSensitive(run ReviewRun) bool {
	for _, pack := range run.PolicyPacks {
		switch strings.ToLower(strings.TrimSpace(pack)) {
		case "anti_cheat_telemetry", "memory_scan", "unreal_integrity":
			return true
		}
	}
	text := strings.ToLower(strings.Join(run.ChangeSet.ChangedPaths, " ") + " " + run.Objective)
	return containsAny(text,
		"false positive", "false_positive", "false-positive", "오탐",
		"anti_cheat", "anti-cheat", "anticheat", "안티치트",
		"detection", "detect", "telemetry", "탐지", "텔레메트리",
		"memory scan", "memory-scan", "scanner", "scan",
		"spoof", "evasion", "우회")
}

func configuredReviewRoleLabel(cfg Config, reviewCfg ReviewHarnessConfig, role string) string {
	label, _ := reviewRoleModelLabelAndSource(cfg, reviewCfg, role)
	return label
}

func reviewRoleModelLabelAndSource(cfg Config, reviewCfg ReviewHarnessConfig, role string) (string, string) {
	role = normalizeReviewRole(role)
	if role == "primary_reviewer" && strings.TrimSpace(cfg.Provider) != "" && strings.TrimSpace(cfg.Model) != "" {
		effort, _ := reviewReasoningEffortOrDefaultForProvider(cfg.Provider, cfg.ReasoningEffort)
		return formatProviderModelEffortLabel(cfg.Provider, cfg.Model, effort), "main"
	}
	if roleCfg, ok := reviewCfg.RoleModels[role]; ok && strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
		effort := reviewRoleConfiguredReasoningEffort(cfg, role, roleCfg)
		return formatProviderModelEffortLabel(roleCfg.Provider, roleCfg.Model, effort), "role"
	}
	if role == "cross_reviewer" {
		if roleCfg, ok := reviewCfg.RoleModels["primary_reviewer"]; ok && strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
			effort := reviewRoleConfiguredReasoningEffort(cfg, "primary_reviewer", roleCfg)
			return formatProviderModelEffortLabel(roleCfg.Provider, roleCfg.Model, effort), "legacy_primary_reviewer"
		}
		return "", ""
	}
	if role != "primary_reviewer" {
		if roleCfg, ok := reviewCfg.RoleModels["primary_reviewer"]; ok && strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
			effort := reviewRoleConfiguredReasoningEffort(cfg, "primary_reviewer", roleCfg)
			return formatProviderModelEffortLabel(roleCfg.Provider, roleCfg.Model, effort), "primary_reviewer"
		}
	}
	if strings.TrimSpace(cfg.Provider) != "" && strings.TrimSpace(cfg.Model) != "" {
		return formatProviderModelEffortLabel(cfg.Provider, cfg.Model, reviewRoleReasoningEffort(cfg, role)), "main"
	}
	return "", ""
}

func executeReviewModelRuns(ctx context.Context, rt *runtimeState, root string, run *ReviewRun) ([]ReviewFinding, []ReviewReviewerRun) {
	if rt == nil || rt.agent == nil || run == nil {
		if run != nil {
			run.Result.Degraded = true
			run.Result.DegradedReason = "no active reviewer agent"
		}
		return nil, []ReviewReviewerRun{{
			Role:         "primary_reviewer",
			Status:       "failed",
			ModelQuality: reviewModelQualityFailed,
			Error:        "no active reviewer agent",
		}}
	}
	originalRequiredRoles := append([]string(nil), run.ModelPlan.RequiredRoles...)
	var findings []ReviewFinding
	var reviewerRuns []ReviewReviewerRun

	mainRole := reviewMainExecutionRole(run.ModelPlan)
	mainClient, mainModel, mainLabel, mainErr := reviewMainRoleClient(rt)
	mainLabel = reviewModelDisplayLabel(rt.cfg, mainClient, mainModel, mainLabel, reviewRoleReasoningEffortForRun(rt.cfg, mainRole, *run))
	crossClient, crossModel, crossLabel, crossRole, _, hasCrossReviewer := reviewCrossReviewerClient(rt, *run, originalRequiredRoles, mainClient, mainModel)
	run.SingleModelPolicy = buildSingleModelReviewPolicy(*run, hasCrossReviewer)
	phaseTotal := 1
	if hasCrossReviewer || run.SingleModelPolicy.Enabled {
		phaseTotal = 2
	}
	prepareMainFirstReviewModelPlan(run, mainRole, mainLabel)
	mainPrompt := buildReviewModelPrompt(rt.cfg, *run, mainRole)
	emitReviewModelPhaseBudgetProgress(rt, *run, "main", 1, phaseTotal, mainRole, mainLabel)
	emitReviewModelMainFirstPassProgress(rt)
	mainFindings, mainRun, mainRaw := executeSingleReviewModelRun(ctx, rt, root, run, mainClient, mainModel, mainLabel, mainRole, "main", mainPrompt, mainErr, reviewModelRunPeerContext{})
	reviewerRuns = append(reviewerRuns, mainRun)
	findings = append(findings, mainFindings...)

	if hasCrossReviewer {
		emitReviewModelCrossHandoffProgress(rt, mainRun)
		registerCrossReviewerInModelPlan(run, crossRole, crossLabel)
		crossPrompt := buildReviewModelCrossCheckPrompt(rt.cfg, *run, crossRole, mainRaw, findings)
		emitReviewModelPhaseBudgetProgress(rt, *run, "cross", 2, phaseTotal, crossRole, crossLabel)
		emitReviewModelCrossCheckProgress(rt)
		crossFindings, crossRun, _ := executeSingleReviewModelRun(ctx, rt, root, run, crossClient, crossModel, crossLabel, crossRole, "cross", crossPrompt, nil, reviewModelRunPeerContext{
			PriorFindings:     append([]ReviewFinding(nil), findings...),
			PriorReviewerRuns: append([]ReviewReviewerRun(nil), reviewerRuns...),
		})
		reviewerRuns = append(reviewerRuns, crossRun)
		findings = append(findings, crossFindings...)
		emitReviewModelCrossResultHandoffProgress(rt, crossRun)
	} else {
		emitReviewModelNoCrossReviewerProgress(rt)
		if run.ModelPlan.UserGuidance == nil {
			run.ModelPlan.UserGuidance = []string{}
		}
		run.ModelPlan.UserGuidance = append(run.ModelPlan.UserGuidance, "Single-model review mode is active; no independent cross reviewer is configured for this run.")
		if shouldRunSingleModelSecondPass(run, mainRun, mainRaw) {
			secondPassFingerprint := singleModelSecondPassFingerprint(*run, mainRaw, findings)
			run.SingleModelSecondPass = &SingleModelSecondPassReview{
				Enabled:       true,
				Fingerprint:   secondPassFingerprint,
				Status:        "pending",
				Model:         mainLabel,
				ReviewedPaths: normalizeTaskStateList(run.ChangeSet.ChangedPaths, 32),
			}
			prepareSingleModelSecondPassPlan(run, mainLabel)
			if cached, ok := lookupAcceptedSecondPassCache(rt, secondPassFingerprint); ok {
				cachedRun := cachedSingleModelSecondPassRun(cached)
				reviewerRuns = append(reviewerRuns, cachedRun)
				run.SingleModelSecondPass.Status = "cached"
				run.SingleModelSecondPass.CacheHit = true
				run.SingleModelSecondPass.ReviewedAt = cached.AcceptedAt
				run.SingleModelSecondPass.Model = cached.Model
				emitReviewModelResultProgress(rt, cachedRun, 0)
			} else {
				secondPrompt := buildSingleModelSecondPassReviewPrompt(rt.cfg, *run, mainRaw, findings)
				emitReviewModelPhaseBudgetProgress(rt, *run, "second_pass", 2, phaseTotal, singleModelSecondPassRole, mainLabel)
				secondFindings, secondRun, _ := executeSingleReviewModelRun(ctx, rt, root, run, mainClient, mainModel, mainLabel, singleModelSecondPassRole, "second_pass", secondPrompt, nil, reviewModelRunPeerContext{
					PriorFindings:     append([]ReviewFinding(nil), findings...),
					PriorReviewerRuns: append([]ReviewReviewerRun(nil), reviewerRuns...),
				})
				reviewerRuns = append(reviewerRuns, secondRun)
				findings = append(findings, secondFindings...)
				run.SingleModelSecondPass.Status = secondRun.Status
				run.SingleModelSecondPass.Model = secondRun.Model
				run.SingleModelSecondPass.ReviewedAt = secondRun.FinishedAt
				run.SingleModelSecondPass.FindingCount = len(secondFindings)
				run.SingleModelSecondPass.PromptPath = secondRun.PromptPath
				run.SingleModelSecondPass.RawOutputPath = secondRun.RawOutputPath
			}
		} else if run.SingleModelPolicy.Enabled {
			run.SingleModelSecondPass = &SingleModelSecondPassReview{
				Enabled:       true,
				Status:        "skipped",
				Model:         mainLabel,
				ReviewedPaths: normalizeTaskStateList(run.ChangeSet.ChangedPaths, 32),
				SkippedReason: singleModelSecondPassSkipReason(*run, mainRun, mainRaw),
			}
		}
	}
	assignReviewFindingIDs(findings)
	return findings, reviewerRuns
}

type reviewModelRunPeerContext struct {
	PriorFindings     []ReviewFinding
	PriorReviewerRuns []ReviewReviewerRun
}

func executeSingleReviewModelRun(ctx context.Context, rt *runtimeState, root string, run *ReviewRun, client ProviderClient, model string, label string, role string, kind string, prompt string, setupErr error, peer reviewModelRunPeerContext) ([]ReviewFinding, ReviewReviewerRun, string) {
	role = normalizeReviewRole(role)
	if strings.TrimSpace(role) == "" {
		role = "primary_reviewer"
	}
	reviewerRun := ReviewReviewerRun{
		Role:      role,
		Kind:      strings.TrimSpace(kind),
		Model:     label,
		StartedAt: time.Now(),
	}
	if setupErr != nil || client == nil || strings.TrimSpace(model) == "" {
		reviewerRun.Status = "failed"
		reviewerRun.ModelQuality = reviewModelQualityFailed
		if setupErr != nil {
			reviewerRun.Error = setupErr.Error()
		} else {
			reviewerRun.Error = "no reviewer model configured"
		}
		reviewerRun.FinishedAt = time.Now()
		if run != nil {
			run.Result.Degraded = true
			run.Result.DegradedReason = strings.TrimSpace(reviewerRun.Error)
			if run.Result.ModelQuality == "" || reviewModelQualityRank(reviewModelQualityFailed) > reviewModelQualityRank(run.Result.ModelQuality) {
				run.Result.ModelQuality = reviewModelQualityFailed
			}
		}
		emitReviewModelResultProgress(rt, reviewerRun, 0)
		return nil, reviewerRun, ""
	}
	usedLocalCompactRecovery := false
	systemPrompt := reviewModelSystemPrompt(rt.cfg, *run, role)
	if health, ok := reviewRouteHealthSkipsInitialModelCall(rt, reviewerRun); ok {
		if reviewLocalModelCompactRecoveryAllowed(rt.cfg, reviewerRun, health) {
			prompt = buildReviewModelLocalCompactReviewPrompt(rt.cfg, *run, role, "route_health")
			systemPrompt = reviewModelLocalCompactSystemPrompt(rt.cfg, *run, role)
			usedLocalCompactRecovery = true
			emitReviewModelLocalCompactRecoveryProgress(rt, reviewerRun, health)
		} else {
			reviewerRun.Status = "failed"
			reviewerRun.ModelQuality = reviewModelQualityFailed
			reviewerRun.Error = "review route health skipped repeated reviewer call after recent unhealthy reviewer output"
			reviewerRun.FinishedAt = time.Now()
			run.Result.Degraded = true
			run.Result.DegradedReason = reviewerRun.Error
			if run.Result.ModelQuality == "" || reviewModelQualityRank(reviewModelQualityFailed) > reviewModelQualityRank(run.Result.ModelQuality) {
				run.Result.ModelQuality = reviewModelQualityFailed
			}
			emitReviewModelHealthCallSkippedProgress(rt, reviewerRun, health)
			emitReviewModelResultProgress(rt, reviewerRun, 0)
			return nil, reviewerRun, ""
		}
	}
	if !usedLocalCompactRecovery && reviewInitialLocalCompactPromptAllowed(rt.cfg, *run, reviewerRun) {
		prompt = buildReviewModelLocalCompactReviewPrompt(rt.cfg, *run, role, "large_local_context")
		systemPrompt = reviewModelLocalCompactSystemPrompt(rt.cfg, *run, role)
		usedLocalCompactRecovery = true
		emitReviewModelLocalInitialCompactProgress(rt, reviewerRun, reviewLocalCompactReviewEvidenceLimit(*run))
	}
	promptPath, rawPath := reviewRoleArtifactPaths(root, run.ID, role)
	_ = os.WriteFile(promptPath, []byte(prompt), 0o644)
	reviewerRun.PromptPath = promptPath
	emitReviewModelRequestProgress(rt, role, label, reviewerRun.Kind)
	softTimeout := reviewModelSoftTimeoutForRun(rt.cfg, *run, reviewerRun, reviewRouteHealthForTimeout(rt, *run))
	emitReviewModelCallBudgetProgress(rt, *run, reviewerRun, softTimeout)
	callCtx, cancelCall := reviewModelCallContext(ctx, softTimeout)
	resp, err := completeReviewModelTurnWithProgress(callCtx, rt, reviewerRun, func(callCtx context.Context) (ChatResponse, error) {
		return rt.agent.completeModelTurnWithClient(callCtx, client, ChatRequest{
			Model:           model,
			System:          systemPrompt,
			Messages:        []Message{{Role: "user", Text: prompt}},
			MaxTokens:       reviewRoleMaxTokensForRoleRun(rt.cfg, role, *run),
			Temperature:     0.1,
			ReasoningEffort: reviewRoleReasoningEffortForRun(rt.cfg, role, *run),
			WorkingDir:      root,
			CodexSubagent:   openAICodexSubagentReview,
		})
	})
	cancelCall()
	reviewerRun.FinishedAt = time.Now()
	if err != nil {
		reviewerRun.Status = "failed"
		reviewerRun.ModelQuality = reviewModelQualityFailed
		reviewerRun.Error = reviewModelCallErrorText(err, softTimeout)
		run.Result.Degraded = true
		run.Result.DegradedReason = "review model failed: " + reviewerRun.Error
		emitReviewModelResultProgress(rt, reviewerRun, 0)
		return nil, reviewerRun, ""
	}
	if rawProviderPath, rawProviderRedaction := writeReviewProviderRawResponseArtifact(root, run.ID, role, "", resp.RawBody); rawProviderPath != "" {
		reviewerRun.RawProviderResponsePath = rawProviderPath
		run.Redaction = mergeReviewRedactionReports(run.Redaction, rawProviderRedaction)
	}
	raw := strings.TrimSpace(resp.Message.Text)
	rawStopReason := resp.StopReason
	reasoningRecovery := ""
	if raw == "" {
		reasoningRecovery = reviewStructuredOutputFromReasoningContent(rt.cfg, reviewerRun, resp.Message.ReasoningContent)
		if reasoningRecovery != "" && !reviewLocalModelReasoningOnlyRetryAllowed(rt.cfg, reviewerRun, resp.Message.ReasoningContent) {
			raw = reasoningRecovery
			reasoningRecovery = ""
			emitReviewModelReasoningContentRecoveryProgress(rt, reviewerRun)
		}
	}
	if raw == "" {
		retryReason := ""
		if reviewLocalModelReasoningOnlyRetryAllowed(rt.cfg, reviewerRun, resp.Message.ReasoningContent) {
			retryReason = "reasoning_only"
		} else if !usedLocalCompactRecovery && reviewLocalModelEmptyResponseRetryAllowed(rt.cfg, reviewerRun) {
			retryReason = "empty_response"
		}
		if retryReason != "" {
			if retryReason == "reasoning_only" {
				emitReviewModelReasoningOnlyRetryProgress(rt, reviewerRun, label)
			} else {
				emitReviewModelEmptyResponseRetryProgress(rt, reviewerRun, label)
			}
			retryPrompt := buildReviewModelLocalCompactReviewPrompt(rt.cfg, *run, role, retryReason)
			retryPromptPath, retryRawPath := reviewRoleNamedAttemptArtifactPaths(root, run.ID, role, "empty_retry")
			_ = os.WriteFile(retryPromptPath, []byte(retryPrompt), 0o644)
			retryRun := reviewerRun
			retryRun.PromptPath = retryPromptPath
			retryCtx, cancelRetry := reviewModelCallContext(ctx, softTimeout)
			retryResp, retryErr := completeReviewModelTurnWithProgress(retryCtx, rt, retryRun, func(callCtx context.Context) (ChatResponse, error) {
				return rt.agent.completeModelTurnWithClient(callCtx, client, ChatRequest{
					Model:           model,
					System:          reviewModelLocalCompactSystemPrompt(rt.cfg, *run, role),
					Messages:        []Message{{Role: "user", Text: retryPrompt}},
					MaxTokens:       reviewRoleRetryMaxTokensForRoleRun(rt.cfg, role, *run),
					Temperature:     0.05,
					ReasoningEffort: reviewRoleReasoningEffortForRun(rt.cfg, role, *run),
					WorkingDir:      root,
					CodexSubagent:   openAICodexSubagentReview,
				})
			})
			cancelRetry()
			reviewerRun.FinishedAt = time.Now()
			if retryErr != nil {
				reviewerRun.Error = "empty response retry failed: " + reviewModelCallErrorText(retryErr, softTimeout)
			} else {
				if retryRawProviderPath, retryRawProviderRedaction := writeReviewProviderRawResponseArtifact(root, run.ID, role, "empty_retry", retryResp.RawBody); retryRawProviderPath != "" {
					reviewerRun.RawProviderResponsePath = retryRawProviderPath
					run.Redaction = mergeReviewRedactionReports(run.Redaction, retryRawProviderRedaction)
				}
				retryRaw := strings.TrimSpace(retryResp.Message.Text)
				if retryRaw == "" {
					if recovered := reviewStructuredOutputFromReasoningContent(rt.cfg, reviewerRun, retryResp.Message.ReasoningContent); recovered != "" {
						retryRaw = recovered
						emitReviewModelReasoningContentRecoveryProgress(rt, reviewerRun)
					}
				}
				if retryRaw != "" {
					raw = retryRaw
					rawStopReason = retryResp.StopReason
					promptPath = retryPromptPath
					rawPath = retryRawPath
				}
			}
		}
		if raw == "" && reasoningRecovery != "" {
			raw = reasoningRecovery
			reasoningRecovery = ""
			reviewerRun.Error = ""
			run.Result.Degraded = true
			run.Result.DegradedReason = "review model recovered structured output from reasoning_content after final-content retry"
			emitReviewModelReasoningContentRecoveryProgress(rt, reviewerRun)
		}
		if raw == "" {
			raw = "(empty review response)"
			raw, rawRedaction := redactSensitiveText(raw)
			_ = os.WriteFile(rawPath, []byte(raw), 0o644)
			reviewerRun.RawOutputPath = rawPath
			reviewerRun.Status = "failed"
			reviewerRun.ModelQuality = reviewModelQualityFailed
			if strings.TrimSpace(reviewerRun.Error) == "" {
				reviewerRun.Error = "review model returned empty response"
				if strings.TrimSpace(resp.Message.ReasoningContent) != "" {
					reviewerRun.Error = "review model returned empty content while reasoning_content was present"
				}
			}
			run.Redaction = mergeReviewRedactionReports(run.Redaction, rawRedaction)
			run.Result.Degraded = true
			run.Result.DegradedReason = reviewerRun.Error
			if run.Result.ModelQuality == "" || reviewModelQualityRank(reviewModelQualityFailed) > reviewModelQualityRank(run.Result.ModelQuality) {
				run.Result.ModelQuality = reviewModelQualityFailed
			}
			emitReviewModelResultProgress(rt, reviewerRun, 0)
			return nil, reviewerRun, raw
		}
	}
	raw, rawRedaction := redactSensitiveText(raw)
	_ = os.WriteFile(rawPath, []byte(raw), 0o644)
	reviewerRun.PromptPath = promptPath
	reviewerRun.RawOutputPath = rawPath
	roleFindings, quality := parseModelReviewFindingsForLanguage(raw, role, reviewRunPrefersKorean(rt.cfg, *run))
	if reviewStopReasonLooksTruncated(rawStopReason) {
		roleFindings = append(roleFindings, reviewTruncatedTailFindingPlaceholder(role, reviewRunPrefersKorean(rt.cfg, *run)))
		quality = reviewModelQualityWeak
	}
	for i := range roleFindings {
		roleFindings[i].ReviewerRole = role
		roleFindings[i].Source = "model"
	}
	run.Redaction = mergeReviewRedactionReports(run.Redaction, rawRedaction)
	omissionRetryBudget := reviewRoleOmissionRetryBudgetForReviewRun(rt.cfg, role, *run, reviewerRun.Kind)
	if omissionRetryBudget > 0 &&
		reviewShouldRetryOmittedReviewOutput(raw, roleFindings, quality) &&
		reviewShouldSkipOptionalCrossOmissionRetry(rt.cfg, *run, reviewerRun, resp.StopReason, roleFindings, peer) {
		emitReviewModelRetrySkippedProgress(rt, reviewerRun, label)
		omissionRetryBudget = 0
	}
	if omissionRetryBudget > 0 &&
		reviewShouldRetryOmittedReviewOutput(raw, roleFindings, quality) &&
		reviewRouteHealthSuppressesStrictRetry(rt, reviewerRun) {
		emitReviewModelHealthRetrySuppressedProgress(rt, reviewerRun, label)
		omissionRetryBudget = 0
	}
	omissionRetryFailed := false
	for attempt := 1; attempt <= omissionRetryBudget && reviewShouldRetryOmittedReviewOutput(raw, roleFindings, quality); attempt++ {
		emitReviewModelRetryProgress(rt, role, label, attempt, omissionRetryBudget)
		retryPrompt := buildReviewModelOmissionRetryPrompt(rt.cfg, *run, role)
		retryPromptPath, retryRawPath := reviewRoleAttemptArtifactPaths(root, run.ID, role, attempt)
		_ = os.WriteFile(retryPromptPath, []byte(retryPrompt), 0o644)
		retryRun := reviewerRun
		retryRun.PromptPath = retryPromptPath
		retryCtx, cancelRetry := reviewModelCallContext(ctx, softTimeout)
		retryResp, retryErr := completeReviewModelTurnWithProgress(retryCtx, rt, retryRun, func(callCtx context.Context) (ChatResponse, error) {
			return rt.agent.completeModelTurnWithClient(callCtx, client, ChatRequest{
				Model:           model,
				System:          reviewModelSystemPrompt(rt.cfg, *run, role),
				Messages:        []Message{{Role: "user", Text: retryPrompt}},
				MaxTokens:       reviewRoleRetryMaxTokensForRoleRun(rt.cfg, role, *run),
				Temperature:     0.05,
				ReasoningEffort: reviewRoleReasoningEffortForRun(rt.cfg, role, *run),
				WorkingDir:      root,
				CodexSubagent:   openAICodexSubagentReview,
			})
		})
		cancelRetry()
		reviewerRun.FinishedAt = time.Now()
		if retryErr != nil {
			reviewerRun.Error = "omission retry failed: " + reviewModelCallErrorText(retryErr, softTimeout)
			omissionRetryFailed = true
			break
		}
		retryRaw := strings.TrimSpace(retryResp.Message.Text)
		if retryRaw == "" {
			retryRaw = "(empty review response)"
		}
		retryRaw, retryRedaction := redactSensitiveText(retryRaw)
		_ = os.WriteFile(retryRawPath, []byte(retryRaw), 0o644)
		retryFindings, retryQuality := parseModelReviewFindingsForLanguage(retryRaw, role, reviewRunPrefersKorean(rt.cfg, *run))
		if reviewStopReasonLooksTruncated(retryResp.StopReason) {
			retryFindings = append(retryFindings, reviewTruncatedTailFindingPlaceholder(role, reviewRunPrefersKorean(rt.cfg, *run)))
			retryQuality = reviewModelQualityWeak
		}
		for i := range retryFindings {
			retryFindings[i].ReviewerRole = role
			retryFindings[i].Source = "model"
		}
		run.Redaction = mergeReviewRedactionReports(run.Redaction, retryRedaction)
		reviewerRun.PromptPath = retryPromptPath
		reviewerRun.RawOutputPath = retryRawPath
		raw = retryRaw
		roleFindings = retryFindings
		quality = retryQuality
	}
	reviewerRun.Status = "completed"
	reviewerRun.ModelQuality = quality
	emitReviewModelResultProgress(rt, reviewerRun, len(roleFindings))
	if quality == reviewModelQualityWeak || quality == reviewModelQualityFailed {
		run.Result.Degraded = true
		run.Result.DegradedReason = "model reviewer output quality was " + quality
	}
	if omissionRetryFailed {
		run.Result.Degraded = true
		run.Result.DegradedReason = strings.TrimSpace(reviewerRun.Error)
	}
	if run.Result.ModelQuality == "" || reviewModelQualityRank(quality) > reviewModelQualityRank(run.Result.ModelQuality) {
		run.Result.ModelQuality = quality
	}
	return roleFindings, reviewerRun, raw
}

func reviewMainRoleClient(rt *runtimeState) (ProviderClient, string, string, error) {
	if rt == nil || rt.agent == nil {
		return nil, "", "", fmt.Errorf("no runtime")
	}
	if rt.agent.Client != nil && reviewMainModelRouteConfigured(rt.cfg) {
		return rt.agent.Client, rt.cfg.Model, reviewMainModelLabel(rt.cfg), nil
	}
	client, model, label, err := reviewRoleClient(rt, "primary_reviewer")
	if err == nil && client != nil && strings.TrimSpace(model) != "" {
		return client, model, label, nil
	}
	if err != nil {
		return nil, "", "", err
	}
	return nil, "", "", fmt.Errorf("no main model configured")
}

func reviewMainExecutionRole(plan ReviewModelPlan) string {
	return "primary_reviewer"
}

func reviewStringSliceContainsCI(items []string, value string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func prepareMainFirstReviewModelPlan(run *ReviewRun, mainRole string, mainLabel string) {
	if run == nil {
		return
	}
	mainRole = normalizeReviewRole(mainRole)
	if mainRole == "" {
		mainRole = "primary_reviewer"
	}
	if run.ModelPlan.AssignedModels == nil {
		run.ModelPlan.AssignedModels = map[string]string{}
	}
	if len(run.ModelPlan.RequiredRoles) == 0 {
		run.ModelPlan.RequiredRoles = []string{mainRole}
	} else if !reviewStringSliceContainsCI(run.ModelPlan.RequiredRoles, mainRole) {
		run.ModelPlan.RequiredRoles = analysisUniqueStrings(append([]string{mainRole}, run.ModelPlan.RequiredRoles...))
	}
	run.ModelPlan.AssignedModels[mainRole] = strings.TrimSpace(mainLabel)
	markReviewModelRoleSatisfied(run, mainRole)
}

func registerCrossReviewerInModelPlan(run *ReviewRun, role string, label string) {
	if run == nil {
		return
	}
	role = normalizeReviewRole(role)
	if role == "" {
		role = "cross_reviewer"
	}
	if run.ModelPlan.AssignedModels == nil {
		run.ModelPlan.AssignedModels = map[string]string{}
	}
	run.ModelPlan.AssignedModels[role] = strings.TrimSpace(label)
	markReviewModelRoleSatisfied(run, role)
	if reviewRunRequiresSuccessfulCrossReviewer(*run) {
		run.ModelPlan.RequiredRoles = analysisUniqueStrings(append(run.ModelPlan.RequiredRoles, role))
	} else {
		run.ModelPlan.OptionalRoles = analysisUniqueStrings(append(run.ModelPlan.OptionalRoles, role))
	}
	if len(run.ModelPlan.RequiredRoles)+len(run.ModelPlan.OptionalRoles) > 1 {
		run.ModelPlan.Strategy = "dual"
	}
}

func markReviewModelRoleSatisfied(run *ReviewRun, role string) {
	if run == nil {
		return
	}
	role = normalizeReviewRole(role)
	if role == "" {
		return
	}
	run.ModelPlan.MissingRoles = removeStringCI(run.ModelPlan.MissingRoles, role)
	run.ModelPlan.DegradedRoles = removeStringCI(run.ModelPlan.DegradedRoles, role)
	run.ModelPlan.UserGuidance = removeReviewModelRoleGuidance(run.ModelPlan.UserGuidance, role)
}

func removeReviewModelRoleGuidance(items []string, role string) []string {
	role = normalizeReviewRole(role)
	if role == "" {
		return items
	}
	var out []string
	for _, item := range items {
		if reviewModelGuidanceMentionsRole(item, role) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func reviewModelGuidanceMentionsRole(item string, role string) bool {
	text := strings.ToLower(strings.TrimSpace(item))
	switch normalizeReviewRole(role) {
	case "security_reviewer":
		return strings.Contains(text, "security reviewer")
	case "false_positive_reviewer":
		return strings.Contains(text, "false-positive reviewer") ||
			strings.Contains(text, "false positive reviewer")
	default:
		return strings.Contains(text, strings.ToLower(strings.ReplaceAll(role, "_", " ")))
	}
}

func reviewRunRequiresSuccessfulCrossReviewer(run ReviewRun) bool {
	if strings.EqualFold(normalizeReviewReviewerGatePolicy(run.ReviewerGatePolicy), reviewReviewerGatePolicyMainOnlyFallback) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write")
}

func normalizeReviewReviewerGatePolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case reviewReviewerGatePolicyMainOnlyFallback:
		return reviewReviewerGatePolicyMainOnlyFallback
	default:
		return ""
	}
}

func reviewCrossReviewerClient(rt *runtimeState, run ReviewRun, preferredRoles []string, mainClient ProviderClient, mainModel string) (ProviderClient, string, string, string, string, bool) {
	routeRole := reviewPreferredCrossReviewRouteRole(rt.cfg, run, preferredRoles)
	client, model, label, err := reviewRoleClient(rt, routeRole)
	if err != nil || client == nil || strings.TrimSpace(model) == "" {
		return nil, "", "", "", "", false
	}
	if reviewClientMatchesRoute(rt, client, model, mainClient, mainModel) || reviewClientMatchesMain(rt, client, model) {
		return nil, "", "", "", "", false
	}
	label = reviewModelDisplayLabel(rt.cfg, client, model, label, reviewRoleReasoningEffortForRun(rt.cfg, routeRole, run))
	if !reviewModelLabelDiffersFromMain(rt.cfg, label) {
		return nil, "", "", "", "", false
	}
	return client, model, label, "cross_reviewer", normalizeReviewRole(routeRole), true
}

func reviewClientMatchesMain(rt *runtimeState, client ProviderClient, model string) bool {
	if rt == nil || rt.agent == nil || client == nil || rt.agent.Client == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(model), strings.TrimSpace(rt.cfg.Model)) {
		return false
	}
	if sameProviderClient(client, rt.agent.Client) {
		return true
	}
	clientRoute := providerClientReviewRoute(client, "")
	mainRoute := providerClientReviewRoute(rt.agent.Client, rt.cfg.Provider)
	if clientRoute.Provider == "" || mainRoute.Provider == "" || clientRoute.Provider != mainRoute.Provider {
		return false
	}
	clientBaseURL := reviewClientRouteBaseURL(rt, clientRoute, model)
	mainBaseURL := reviewClientRouteBaseURL(rt, mainRoute, rt.cfg.Model)
	return strings.EqualFold(clientBaseURL, mainBaseURL)
}

func reviewClientMatchesRoute(rt *runtimeState, client ProviderClient, model string, mainClient ProviderClient, mainModel string) bool {
	if client == nil || mainClient == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(model), strings.TrimSpace(mainModel)) {
		return false
	}
	if sameProviderClient(client, mainClient) {
		return true
	}
	clientRoute := providerClientReviewRoute(client, "")
	mainRoute := providerClientReviewRoute(mainClient, "")
	if clientRoute.Provider == "" || mainRoute.Provider == "" || clientRoute.Provider != mainRoute.Provider {
		return false
	}
	clientBaseURL := reviewClientRouteBaseURL(rt, clientRoute, model)
	mainBaseURL := reviewClientRouteBaseURL(rt, mainRoute, mainModel)
	return strings.EqualFold(clientBaseURL, mainBaseURL)
}

func reviewClientRouteBaseURL(rt *runtimeState, route ModelRouteMetadata, model string) string {
	provider := normalizeProviderName(route.Provider)
	baseURL := strings.TrimSpace(route.BaseURL)
	if baseURL == "" && rt != nil &&
		provider == normalizeProviderName(rt.cfg.Provider) &&
		strings.EqualFold(strings.TrimSpace(model), strings.TrimSpace(rt.cfg.Model)) {
		baseURL = strings.TrimSpace(rt.cfg.BaseURL)
	}
	return normalizeProviderBaseURL(provider, baseURL)
}

func sameProviderClient(left ProviderClient, right ProviderClient) bool {
	if left == nil || right == nil {
		return false
	}
	leftValue := reflect.ValueOf(left)
	rightValue := reflect.ValueOf(right)
	if !leftValue.IsValid() || !rightValue.IsValid() || leftValue.Type() != rightValue.Type() {
		return false
	}
	if !leftValue.Type().Comparable() {
		return false
	}
	return left == right
}

func providerClientReviewRoute(client ProviderClient, fallbackProvider string) ModelRouteMetadata {
	route := ModelRouteMetadata{}
	if metaProvider, ok := client.(modelRouteMetadataProvider); ok {
		route = metaProvider.ModelRouteMetadata()
	}
	if strings.TrimSpace(route.Provider) == "" && client != nil {
		route.Provider = client.Name()
	}
	if strings.TrimSpace(route.Provider) == "" {
		route.Provider = fallbackProvider
	}
	route.Provider = normalizeProviderName(route.Provider)
	route.BaseURL = strings.TrimSpace(route.BaseURL)
	return route
}

func reviewModelConfigMatchesMain(cfg Config, roleCfg ReviewModelConfig) bool {
	if !strings.EqualFold(strings.TrimSpace(roleCfg.Model), strings.TrimSpace(cfg.Model)) {
		return false
	}
	roleProvider := normalizeProviderName(roleCfg.Provider)
	mainProvider := normalizeProviderName(cfg.Provider)
	if roleProvider == "" || mainProvider == "" || roleProvider != mainProvider {
		return false
	}
	roleBaseURLInput := strings.TrimSpace(roleCfg.BaseURL)
	if roleBaseURLInput == "" {
		roleBaseURLInput = strings.TrimSpace(cfg.BaseURL)
	}
	roleBaseURL := normalizeProviderBaseURL(roleProvider, roleBaseURLInput)
	mainBaseURL := normalizeProviderBaseURL(mainProvider, cfg.BaseURL)
	return strings.EqualFold(roleBaseURL, mainBaseURL)
}

func reviewPreferredCrossReviewRouteRole(cfg Config, run ReviewRun, preferredRoles []string) string {
	reviewCfg := configReviewHarness(cfg)
	for _, role := range reviewCrossRouteCandidateRoles(run) {
		role = normalizeReviewRole(role)
		if role != "" && role != "primary_reviewer" && roleHasDedicatedReviewModel(reviewCfg, role) {
			return role
		}
	}
	for _, role := range preferredRoles {
		role = normalizeReviewRole(role)
		if role != "" && role != "primary_reviewer" && roleHasDedicatedReviewModel(reviewCfg, role) {
			return role
		}
	}
	return "cross_reviewer"
}

func reviewShouldRetryOmittedReviewOutput(raw string, findings []ReviewFinding, quality string) bool {
	if reviewFindingsContainOmittedOutputPlaceholder(findings) {
		return true
	}
	if reviewFindingsContainPartialOmissionFinding(findings) {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(quality), reviewModelQualityUsable) {
		return reviewTextHasOmissionMarker(raw)
	}
	if reviewFindingsContainUsableModelFinding(findings) {
		return false
	}
	return reviewTextHasOmissionMarker(raw)
}

func reviewFindingsContainPartialOmissionFinding(findings []ReviewFinding) bool {
	for _, finding := range findings {
		if !strings.EqualFold(strings.TrimSpace(finding.Quality), reviewFindingQualityPartial) {
			continue
		}
		if reviewFindingHasOmissionMarker(finding) {
			return true
		}
		text := strings.ToLower(strings.Join([]string{
			finding.Evidence,
			finding.Impact,
			finding.RequiredFix,
			finding.TestRecommendation,
		}, " "))
		if containsAny(text, "omission marker", "omitted", "생략 표식", "생략") {
			return true
		}
	}
	return false
}

func reviewFindingsContainUsableModelFinding(findings []ReviewFinding) bool {
	for _, finding := range findings {
		finding.Normalize()
		if strings.EqualFold(strings.TrimSpace(finding.Source), "deterministic") {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(finding.Quality), reviewFindingQualityWeak) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(finding.Category), "evidence_gap") ||
			strings.EqualFold(strings.TrimSpace(finding.Category), "test_gap") {
			continue
		}
		if strings.TrimSpace(finding.Title) == "" {
			continue
		}
		if strings.TrimSpace(finding.Evidence) == "" && strings.TrimSpace(finding.RequiredFix) == "" {
			continue
		}
		return true
	}
	return false
}

const requiredReviewerFailureFindingID = "RF-REVIEWER-001"

func requiredReviewerFailureFindings(run ReviewRun) []ReviewFinding {
	if !reviewRunRequiresSuccessfulReviewer(run) {
		return nil
	}
	failed := reviewFailedRequiredReviewerRuns(run)
	if len(failed) == 0 {
		return nil
	}
	var details []string
	for _, reviewerRun := range failed {
		role := firstNonBlankString(reviewRoleProgressName(reviewerRun.Role), "reviewer")
		status := valueOrDefault(strings.TrimSpace(reviewerRun.Status), "unknown")
		quality := valueOrDefault(strings.TrimSpace(reviewerRun.ModelQuality), "unknown")
		errText := firstNonBlankString(firstNonEmptyLine(reviewerRun.Error), "reviewer output quality was too weak for the required gate")
		details = append(details, fmt.Sprintf("%s status=%s quality=%s: %s", role, status, quality, errText))
	}
	return []ReviewFinding{{
		ID:                 requiredReviewerFailureFindingID,
		Source:             "deterministic",
		ReviewerRole:       "review_harness",
		Severity:           reviewSeverityBlocker,
		Category:           "evidence_gap",
		Confidence:         "high",
		Quality:            reviewFindingQualityComplete,
		Title:              "Required review route failed or returned weak output",
		Evidence:           strings.Join(details, " | "),
		Impact:             "The review gate cannot treat a failed or weak required review-stage model route as approval for a write-gated change.",
		RequiredFix:        "Fix the failed review route. If primary failed, switch the active main model with /model or fix that provider route; if cross failed, switch that reviewer route with /model cross-review or clear it with /model clear cross-review. Then rerun the review before writing.",
		TestRecommendation: "Rerun the same review request and confirm every required review route completes with usable structured findings or approval.",
		BlocksGate:         true,
	}}
}

func reviewRunHasRequiredReviewerFailure(run ReviewRun) bool {
	if !reviewRunRequiresSuccessfulReviewer(run) {
		return false
	}
	failed := reviewFailedRequiredReviewerRuns(run)
	if len(failed) > 0 {
		return reviewFailedRequiredReviewerRunsIndicateConfiguredFailure(failed)
	}
	for _, finding := range run.Findings {
		finding.Normalize()
		if strings.EqualFold(strings.TrimSpace(finding.ID), requiredReviewerFailureFindingID) {
			return true
		}
	}
	for _, id := range run.Gate.BlockingFindings {
		if strings.EqualFold(strings.TrimSpace(id), requiredReviewerFailureFindingID) {
			return true
		}
	}
	return false
}

func reviewFailedRequiredReviewerRunsIndicateConfiguredFailure(failed []ReviewReviewerRun) bool {
	for _, reviewerRun := range failed {
		if !reviewerRunFailedBecauseNoReviewerConfigured(reviewerRun) {
			return true
		}
	}
	return false
}

func reviewerRunFailedBecauseNoReviewerConfigured(reviewerRun ReviewReviewerRun) bool {
	text := strings.ToLower(strings.Join([]string{
		reviewerRun.Status,
		reviewerRun.ModelQuality,
		reviewerRun.Error,
	}, " "))
	return strings.Contains(text, "no reviewer model configured")
}

func reviewRunRequiresSuccessfulReviewer(run ReviewRun) bool {
	if strings.EqualFold(normalizeReviewReviewerGatePolicy(run.ReviewerGatePolicy), reviewReviewerGatePolicyMainOnlyFallback) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) && preFixReviewHasActionableBugHuntFinding(run) {
		return false
	}
	if preFixReviewCanContinueWithIndependentInspection(run) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		return true
	}
	if len(run.ReviewerRuns) > 0 {
		return true
	}
	return false
}

func reviewRunHasUsableCrossReviewer(run ReviewRun) bool {
	for _, reviewerRun := range run.ReviewerRuns {
		role := normalizeReviewRole(reviewerRun.Role)
		if role != "cross_reviewer" || !strings.EqualFold(strings.TrimSpace(reviewerRun.Kind), "cross") {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(reviewerRun.Status), "completed") &&
			reviewModelQualityUsableOrBetter(reviewerRun.ModelQuality) &&
			strings.TrimSpace(reviewerRun.Error) == "" {
			return true
		}
	}
	return false
}

func reviewFailedRequiredReviewerRunCanBeCoveredByCross(run ReviewRun, reviewerRun ReviewReviewerRun) bool {
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		return false
	}
	role := normalizeReviewRole(reviewerRun.Role)
	if role == "" {
		role = "primary_reviewer"
	}
	if role != "primary_reviewer" {
		return false
	}
	return reviewRunHasUsableCrossReviewer(run)
}

func reviewFailedRequiredReviewerRuns(run ReviewRun) []ReviewReviewerRun {
	required := run.ModelPlan.RequiredRoles
	if len(required) == 0 {
		required = []string{"primary_reviewer"}
	}
	requiredSet := map[string]bool{}
	for _, role := range required {
		requiredSet[normalizeReviewRole(role)] = true
	}
	var out []ReviewReviewerRun
	for _, reviewerRun := range run.ReviewerRuns {
		role := normalizeReviewRole(reviewerRun.Role)
		if role == "" {
			role = "primary_reviewer"
		}
		if !requiredSet[role] {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(reviewerRun.Status), "failed") ||
			strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityWeak) ||
			strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityFailed) ||
			strings.TrimSpace(reviewerRun.Error) != "" {
			if reviewFailedRequiredReviewerRunCanBeCoveredByCross(run, reviewerRun) {
				continue
			}
			out = append(out, reviewerRun)
		}
	}
	return out
}

func reviewRoleClient(rt *runtimeState, role string) (ProviderClient, string, string, error) {
	if rt == nil {
		return nil, "", "", fmt.Errorf("no runtime")
	}
	role = normalizeReviewRole(role)
	reviewCfg := configReviewHarness(rt.cfg)
	if roleCfg, ok := reviewCfg.RoleModels[role]; ok && strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
		cfgCopy := roleCfg
		client, err := createReviewerClient(&cfgCopy, rt.cfg)
		effort, _ := reviewReasoningEffortOrDefaultForProvider(cfgCopy.Provider, cfgCopy.ReasoningEffort)
		return client, cfgCopy.Model, formatProviderModelEffortLabel(cfgCopy.Provider, cfgCopy.Model, effort), err
	}
	if role != "primary_reviewer" {
		if roleCfg, ok := reviewCfg.RoleModels["primary_reviewer"]; ok && strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
			cfgCopy := roleCfg
			client, err := createReviewerClient(&cfgCopy, rt.cfg)
			effort, _ := reviewReasoningEffortOrDefaultForProvider(cfgCopy.Provider, cfgCopy.ReasoningEffort)
			return client, cfgCopy.Model, formatProviderModelEffortLabel(cfgCopy.Provider, cfgCopy.Model, effort), err
		}
	}
	if rt.agent != nil && rt.agent.AuxReviewerClient != nil && strings.TrimSpace(rt.agent.AuxReviewerModel) != "" && role != "primary_reviewer" {
		return rt.agent.AuxReviewerClient, rt.agent.AuxReviewerModel, formatProviderModelEffortLabel(rt.cfg.Provider, rt.agent.AuxReviewerModel, rt.cfg.ReasoningEffort), nil
	}
	if rt.agent != nil && rt.agent.ReviewerClient != nil && strings.TrimSpace(rt.agent.ReviewerModel) != "" {
		return rt.agent.ReviewerClient, rt.agent.ReviewerModel, formatProviderModelEffortLabel(rt.cfg.Provider, rt.agent.ReviewerModel, rt.cfg.ReasoningEffort), nil
	}
	if rt.agent != nil && rt.agent.Client != nil && reviewMainModelRouteConfigured(rt.cfg) {
		return rt.agent.Client, rt.cfg.Model, formatProviderModelEffortLabel(rt.cfg.Provider, rt.cfg.Model, rt.cfg.ReasoningEffort), nil
	}
	return nil, "", "", fmt.Errorf("no reviewer model configured")
}

func reviewMainModelRouteConfigured(cfg Config) bool {
	return strings.TrimSpace(cfg.Provider) != "" && strings.TrimSpace(cfg.Model) != ""
}

func reviewModelDisplayLabel(cfg Config, client ProviderClient, model string, fallbackLabel string, effort string) string {
	provider := ""
	if client != nil {
		provider = strings.TrimSpace(client.Name())
	}
	if strings.TrimSpace(provider) != "" && strings.TrimSpace(model) != "" {
		return formatProviderModelEffortLabel(provider, model, effort)
	}
	return strings.TrimSpace(fallbackLabel)
}

func reviewMainModelLabel(cfg Config) string {
	if strings.TrimSpace(cfg.Provider) == "" || strings.TrimSpace(cfg.Model) == "" {
		return ""
	}
	return formatProviderModelEffortLabel(cfg.Provider, cfg.Model, cfg.ReasoningEffort)
}

func reviewModelLabelDiffersFromMain(cfg Config, label string) bool {
	label = normalizeReviewModelProgressLabel(label)
	mainLabel := normalizeReviewModelProgressLabel(reviewMainModelLabel(cfg))
	return label != "" && mainLabel != "" && label != mainLabel
}

func normalizeReviewModelProgressLabel(label string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(label)), " "))
}

func emitReviewPipelineProgress(rt *runtimeState, run ReviewRun, step int, englishStage string, koreanStage string, englishDetail string, koreanDetail string) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	const total = 6
	if step < 1 {
		step = 1
	}
	if step > total {
		step = total
	}
	if englishDetail == "" {
		englishDetail = englishStage
	}
	if koreanDetail == "" {
		koreanDetail = koreanStage
	}
	flowEnglish := reviewProgressFlow([]string{"scope discovery", "evidence pack", "model review", "merge/check", "gate decision", "next action"}, step)
	flowKorean := reviewProgressFlow([]string{"범위 확인", "증거 준비", "모델 검토", "병합/검산", "게이트 판정", "다음 조치"}, step)
	message := fmt.Sprintf(
		localizedText(rt.cfg,
			"Review progress %d/%d [%s]: %s. Full flow (current stage in brackets): %s.",
			"리뷰 진행 %d/%d [%s]: %s. 전체 흐름(현재 단계는 대괄호): %s."),
		step,
		total,
		localizedText(rt.cfg, englishStage, koreanStage),
		reviewProgressSentence(localizedText(rt.cfg, englishDetail, koreanDetail)),
		localizedText(rt.cfg, flowEnglish, flowKorean),
	)
	rt.agent.EmitProgress(message)
}

func reviewProgressSentence(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimRight(text, ".。!！?？")
	return strings.TrimSpace(text)
}

func reviewProgressFlow(labels []string, currentStep int) string {
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(labels))
	for i, label := range labels {
		step := i + 1
		item := fmt.Sprintf("%d %s", step, strings.TrimSpace(label))
		if step == currentStep {
			item = "[" + item + "]"
		}
		parts = append(parts, item)
	}
	return strings.Join(parts, " -> ")
}

func reviewPipelineNextActionDetail(run ReviewRun, korean bool) string {
	verdict := firstNonBlankString(run.Gate.Verdict, run.Result.Verdict, "unknown")
	blockers := len(run.Gate.BlockingFindings)
	warnings := len(run.Gate.WarningFindings)
	if korean {
		switch verdict {
		case reviewVerdictApproved:
			return fmt.Sprintf("게이트 통과: 차단 %d개, 경고 %d개. 다음 단계로 진행합니다.", blockers, warnings)
		case reviewVerdictApprovedWithWarnings:
			return fmt.Sprintf("경고와 함께 통과: 차단 %d개, 경고 %d개. 경고를 표시하고 다음 단계로 진행합니다.", blockers, warnings)
		case reviewVerdictNeedsRevision, reviewVerdictBlocked:
			return fmt.Sprintf("수정 필요: 차단 %d개, 경고 %d개. 코드 blocker를 수리 루프로 돌려보냅니다.", blockers, warnings)
		case reviewVerdictInsufficientEvidence:
			return fmt.Sprintf("근거 부족: 차단 %d개, 경고 %d개. 범위/리뷰 route/증거를 보강해야 합니다.", blockers, warnings)
		default:
			return fmt.Sprintf("판정 %s: 차단 %d개, 경고 %d개. 다음 조치를 계산합니다.", verdict, blockers, warnings)
		}
	}
	switch verdict {
	case reviewVerdictApproved:
		return fmt.Sprintf("Gate passed: blockers=%d warnings=%d. Proceeding to the next stage.", blockers, warnings)
	case reviewVerdictApprovedWithWarnings:
		return fmt.Sprintf("Gate passed with warnings: blockers=%d warnings=%d. Showing warnings and proceeding.", blockers, warnings)
	case reviewVerdictNeedsRevision, reviewVerdictBlocked:
		return fmt.Sprintf("Revision required: blockers=%d warnings=%d. Sending code blockers back to the repair loop.", blockers, warnings)
	case reviewVerdictInsufficientEvidence:
		return fmt.Sprintf("Insufficient evidence: blockers=%d warnings=%d. Scope, route, or evidence needs attention.", blockers, warnings)
	default:
		return fmt.Sprintf("Verdict %s: blockers=%d warnings=%d. Computing next action.", verdict, blockers, warnings)
	}
}

func emitReviewScopeDiscoveryProgress(rt *runtimeState, run ReviewRun) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	discovery := run.RequestAnalysis.ScopeDiscovery
	if strings.TrimSpace(discovery.ScopeWidth) == "" &&
		len(discovery.CandidateFiles) == 0 &&
		len(discovery.CandidateSymbols) == 0 &&
		len(discovery.SearchTerms) == 0 {
		return
	}
	scope := firstNonBlankString(discovery.ScopeWidth, "unknown")
	preview := reviewProgressPathPreview(discovery.CandidateFiles, 3)
	if preview != "" {
		preview = " " + fmt.Sprintf(localizedText(rt.cfg, "candidates=%s", "후보=%s"), preview)
	}
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Review scope discovery: scope=%s confidence=%.2f files=%d symbols=%d terms=%d.%s", "리뷰 scope discovery: 범위=%s 신뢰도=%.2f 파일 후보=%d 심볼=%d 검색어=%d.%s"),
		scope,
		discovery.Confidence,
		len(discovery.CandidateFiles),
		len(discovery.CandidateSymbols),
		len(discovery.SearchTerms),
		preview,
	)
	rt.agent.EmitProgress(message)
}

func emitReviewEvidenceProgress(rt *runtimeState, run ReviewRun, opts ReviewHarnessOptions) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	sourceText := "(none)"
	if len(run.Evidence.Sources) > 0 {
		sourceText = strings.Join(run.Evidence.Sources, ",")
	}
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Review evidence prepared: sources=%s paths=%d chars=%d max_context=%d.", "리뷰 evidence 준비: sources=%s paths=%d chars=%d max_context=%d."),
		sourceText,
		len(run.ChangeSet.ChangedPaths),
		len(run.Evidence.Text),
		opts.MaxContextChars,
	)
	rt.agent.EmitProgress(message)
}

func reviewProgressPathPreview(paths []string, limit int) string {
	if limit <= 0 || len(paths) == 0 {
		return ""
	}
	out := make([]string, 0, limit)
	for _, path := range paths {
		path = strings.TrimSpace(filepath.ToSlash(path))
		if path == "" {
			continue
		}
		out = append(out, truncateStatusSnippet(path, 64))
		if len(out) >= limit {
			break
		}
	}
	if len(out) == 0 {
		return ""
	}
	if len(paths) > len(out) {
		out = append(out, fmt.Sprintf("+%d", len(paths)-len(out)))
	}
	return strings.Join(out, ", ")
}

func emitReviewModelRequestProgress(rt *runtimeState, role string, label string, kind string) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	mainLabel := reviewMainModelLabel(rt.cfg)
	roleName := reviewRoleProgressName(role)
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "main":
		message := fmt.Sprintf(
			localizedText(rt.cfg, "Main model code review request: %s.", "메인 모델 코드 검토 요청: %s."),
			label,
		)
		rt.agent.EmitProgress(message)
		return
	case "cross":
		message := fmt.Sprintf(
			localizedText(rt.cfg, "Review model cross-check request: %s -> %s (main: %s).", "리뷰 모델 교차 검토 요청: %s -> %s (메인: %s)."),
			roleName,
			label,
			mainLabel,
		)
		rt.agent.EmitProgress(message)
		return
	}
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Review model request: %s -> %s (main: %s).", "리뷰 모델 요청: %s -> %s (메인: %s)."),
		roleName,
		label,
		mainLabel,
	)
	rt.agent.EmitProgress(message)
}

func emitReviewModelHealthCallSkippedProgress(rt *runtimeState, reviewerRun ReviewReviewerRun, health ReviewRouteHealth) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	roleName := reviewRoleProgressName(reviewerRun.Role)
	model := strings.TrimSpace(reviewerRun.Model)
	if model == "" {
		model = strings.TrimSpace(health.Model)
	}
	reason := firstNonBlankString(strings.TrimSpace(health.Recommendation), "recent reviewer route health is weak")
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Review route health skipped repeated reviewer call: %s -> %s (%s).", "리뷰 route health가 반복 리뷰 모델 호출을 건너뜁니다: %s -> %s (%s)."),
		roleName,
		valueOrUnset(model),
		reason,
	)
	rt.agent.EmitProgress(message)
}

func emitReviewModelLocalCompactRecoveryProgress(rt *runtimeState, reviewerRun ReviewReviewerRun, health ReviewRouteHealth) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	roleName := reviewRoleProgressName(reviewerRun.Role)
	model := strings.TrimSpace(reviewerRun.Model)
	if model == "" {
		model = strings.TrimSpace(health.Model)
	}
	reason := firstNonBlankString(strings.TrimSpace(health.Recommendation), "recent local reviewer output was weak")
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Local-model review route health is weak; using a compact recovery prompt instead of skipping: %s -> %s (%s).", "로컬 모델 리뷰 route 상태가 약해서 스킵하지 않고 compact recovery prompt로 재시도합니다: %s -> %s (%s)."),
		roleName,
		valueOrUnset(model),
		reason,
	)
	rt.agent.EmitProgress(message)
}

func emitReviewModelEmptyResponseRetryProgress(rt *runtimeState, reviewerRun ReviewReviewerRun, label string) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	roleName := reviewRoleProgressName(reviewerRun.Role)
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Local-model review returned an empty response; retrying once with a compact structured prompt: %s -> %s.", "로컬 모델 리뷰 응답이 비어 있어 compact structured prompt로 한 번 재시도합니다: %s -> %s."),
		roleName,
		valueOrUnset(label),
	)
	rt.agent.EmitProgress(message)
}

func emitReviewModelReasoningOnlyRetryProgress(rt *runtimeState, reviewerRun ReviewReviewerRun, label string) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	roleName := reviewRoleProgressName(reviewerRun.Role)
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Local-model review wrote only provider reasoning content; retrying once and requiring a final REVIEW_RESULT block: %s -> %s.", "로컬 모델 리뷰가 provider reasoning content에만 응답해, final REVIEW_RESULT 블록을 요구하며 한 번 재시도합니다: %s -> %s."),
		roleName,
		valueOrUnset(label),
	)
	rt.agent.EmitProgress(message)
}

func emitReviewModelLocalInitialCompactProgress(rt *runtimeState, reviewerRun ReviewReviewerRun, evidenceLimit int) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	roleName := reviewRoleProgressName(reviewerRun.Role)
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Local/degraded review route is using a compact initial prompt: %s prompt_limit=%d.", "로컬/degraded 리뷰 route라 compact initial prompt를 사용합니다: %s prompt_limit=%d."),
		roleName,
		evidenceLimit,
	)
	rt.agent.EmitProgress(message)
}

func emitReviewModelReasoningContentRecoveryProgress(rt *runtimeState, reviewerRun ReviewReviewerRun) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	roleName := reviewRoleProgressName(reviewerRun.Role)
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Recovered a structured review block from the provider reasoning channel for %s.", "%s의 provider reasoning channel에서 구조화 리뷰 블록을 복구했습니다."),
		roleName,
	)
	rt.agent.EmitProgress(message)
}

func emitReviewModelPhaseBudgetProgress(rt *runtimeState, run ReviewRun, kind string, phase int, total int, role string, label string) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	reviewerRun := ReviewReviewerRun{
		Role: role,
		Kind: strings.TrimSpace(kind),
	}
	softTimeout := reviewModelSoftTimeoutForRun(rt.cfg, run, reviewerRun, reviewRouteHealthForTimeout(rt, run))
	retryBudget := reviewRoleOmissionRetryBudgetForReviewRun(rt.cfg, role, run, kind)
	contextMode := "standard"
	if reviewRunUsesFocusedFastPath(run) {
		contextMode = "focused"
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		contextMode = "diff-first"
	}
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Review phase %d/%d: %s. model=%s context=%s evidence_chars=%d prompt_limit=%d retry_budget=%d soft_timeout=%s.", "리뷰 단계 %d/%d: %s. 모델=%s context=%s evidence_chars=%d prompt_limit=%d retry_budget=%d soft_timeout=%s."),
		phase,
		total,
		reviewModelPhaseName(rt.cfg, kind),
		label,
		contextMode,
		len(run.Evidence.Text),
		reviewModelPhasePromptLimitForRoute(rt.cfg, run, role, kind),
		retryBudget,
		reviewSoftTimeoutProgressText(softTimeout),
	)
	rt.agent.EmitProgress(message)
}

func emitReviewModelCallBudgetProgress(rt *runtimeState, run ReviewRun, reviewerRun ReviewReviewerRun, softTimeout time.Duration) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	retryBudget := reviewRoleOmissionRetryBudgetForReviewRun(rt.cfg, reviewerRun.Role, run, reviewerRun.Kind)
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Model-call budget: %s retry_budget=%d soft_timeout=%s.", "모델 호출 예산: %s retry_budget=%d soft_timeout=%s."),
		reviewModelPhaseName(rt.cfg, reviewerRun.Kind),
		retryBudget,
		reviewSoftTimeoutProgressText(softTimeout),
	)
	rt.agent.EmitProgress(message)
}

func reviewModelPhaseName(cfg Config, kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "main":
		return localizedText(cfg, "main model code review", "메인 모델 코드 검토")
	case "cross":
		return localizedText(cfg, "review model cross-check", "리뷰 모델 교차 검토")
	default:
		return localizedText(cfg, "review model pass", "리뷰 모델 패스")
	}
}

func reviewSoftTimeoutProgressText(timeout time.Duration) string {
	if timeout <= 0 {
		return "default"
	}
	return formatProgressElapsed(timeout)
}

func reviewModelPhasePromptLimit(run ReviewRun, kind string) int {
	if strings.EqualFold(strings.TrimSpace(kind), "cross") {
		return reviewModelCrossEvidenceLimit(run)
	}
	return reviewModelPromptEvidenceLimit(run)
}

func reviewModelPhasePromptLimitForRoute(cfg Config, run ReviewRun, role string, kind string) int {
	if strings.EqualFold(strings.TrimSpace(kind), "cross") {
		return reviewModelCrossEvidenceLimit(run)
	}
	reviewerRun := ReviewReviewerRun{
		Role: role,
		Kind: kind,
	}
	if reviewInitialLocalCompactPromptAllowed(cfg, run, reviewerRun) {
		return reviewLocalCompactReviewEvidenceLimit(run)
	}
	return reviewModelPromptEvidenceLimit(run)
}

func emitReviewModelResultProgress(rt *runtimeState, run ReviewReviewerRun, findingCount int) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	roleName := reviewRoleProgressName(run.Role)
	kind := strings.ToLower(strings.TrimSpace(run.Kind))
	status := strings.TrimSpace(run.Status)
	if status == "" {
		status = "completed"
	}
	if strings.TrimSpace(run.Error) != "" {
		if kind == "main" {
			message := fmt.Sprintf(
				localizedText(rt.cfg, "Main model code review result: %s (%s).", "메인 모델 검토 결과: %s (%s)."),
				status,
				firstNonEmptyLine(run.Error),
			)
			rt.agent.EmitProgress(message)
			return
		}
		if kind == "cross" {
			message := fmt.Sprintf(
				localizedText(rt.cfg, "Review model cross-check result: %s %s (%s).", "리뷰 모델 교차 검토 결과: %s %s (%s)."),
				roleName,
				status,
				firstNonEmptyLine(run.Error),
			)
			rt.agent.EmitProgress(message)
			return
		}
		message := fmt.Sprintf(
			localizedText(rt.cfg, "Review model result: %s %s (%s).", "리뷰 모델 결과: %s %s (%s)."),
			roleName,
			status,
			firstNonEmptyLine(run.Error),
		)
		rt.agent.EmitProgress(message)
		return
	}
	quality := firstNonBlankString(run.ModelQuality, "unknown")
	if kind == "main" {
		message := fmt.Sprintf(
			localizedText(rt.cfg, "Main model code review result: %s (quality=%s, findings=%d).", "메인 모델 검토 결과: %s (품질=%s, 발견=%d)."),
			status,
			quality,
			findingCount,
		)
		rt.agent.EmitProgress(message)
		return
	}
	if kind == "cross" {
		message := fmt.Sprintf(
			localizedText(rt.cfg, "Review model cross-check result: %s %s (quality=%s, findings=%d).", "리뷰 모델 교차 검토 결과: %s %s (품질=%s, 발견=%d)."),
			roleName,
			status,
			quality,
			findingCount,
		)
		rt.agent.EmitProgress(message)
		return
	}
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Review model result: %s %s (quality=%s, findings=%d).", "리뷰 모델 결과: %s %s (품질=%s, 발견=%d)."),
		roleName,
		status,
		quality,
		findingCount,
	)
	rt.agent.EmitProgress(message)
}

func emitReviewModelMainFirstPassProgress(rt *runtimeState) {
	emitReviewModelFlowProgress(
		rt,
		"Main model is reading the code and checking the repair direction from the collected local evidence.",
		"메인 모델이 코드를 읽고 수정 방향을 검토합니다.",
	)
}

func emitReviewModelCrossHandoffProgress(rt *runtimeState, mainRun ReviewReviewerRun) {
	if !strings.EqualFold(strings.TrimSpace(mainRun.Status), "completed") ||
		!reviewModelQualityUsableOrBetter(mainRun.ModelQuality) ||
		strings.TrimSpace(mainRun.Error) != "" {
		emitReviewModelFlowProgress(
			rt,
			"Main model code review did not produce a usable draft. The review model will use the same evidence for an independent cross-check.",
			"메인 모델 검토가 usable 초안을 만들지 못했습니다. 리뷰 모델이 동일 증거로 독립 교차 검토합니다.",
		)
		return
	}
	emitReviewModelFlowProgress(
		rt,
		"Main model code review completed. Sending its draft and the same evidence to the review model.",
		"메인 모델 검토 결과가 나왔습니다. 리뷰 모델에 초안과 동일 증거를 전달합니다.",
	)
}

func emitReviewModelCrossCheckProgress(rt *runtimeState) {
	emitReviewModelFlowProgress(
		rt,
		"Review model is cross-checking the main model draft and the same evidence before the final gate is decided.",
		"리뷰 모델이 메인 모델 초안과 동일 증거를 교차 검토합니다.",
	)
}

func emitReviewModelCrossResultHandoffProgress(rt *runtimeState, run ReviewReviewerRun) {
	if !strings.EqualFold(strings.TrimSpace(run.Status), "completed") ||
		strings.EqualFold(strings.TrimSpace(run.ModelQuality), reviewModelQualityWeak) ||
		strings.EqualFold(strings.TrimSpace(run.ModelQuality), reviewModelQualityFailed) ||
		strings.TrimSpace(run.Error) != "" {
		reason := firstNonBlankString(firstNonEmptyLine(run.Error), run.ModelQuality, run.Status, "unusable")
		emitReviewModelFlowProgress(
			rt,
			fmt.Sprintf("Review model cross-check did not produce a usable result (%s). Kernforge is merging the main review and reviewer failure state for the final gate.", reason),
			fmt.Sprintf("리뷰 모델 교차 검토가 신뢰 가능한 결과를 만들지 못했습니다(%s). Kernforge가 메인 모델 리뷰와 리뷰어 실패 상태를 병합해 최종 게이트를 계산합니다.", reason),
		)
		return
	}
	emitReviewModelFlowProgress(
		rt,
		"Review model returned its cross-check. Kernforge is merging both reviews for the final gate.",
		"리뷰 모델 검토 결과가 나왔습니다. Kernforge가 두 리뷰 결과를 병합해 최종 게이트를 계산합니다.",
	)
}

func emitReviewModelNoCrossReviewerProgress(rt *runtimeState) {
	emitReviewModelFlowProgress(
		rt,
		"No distinct review model is configured, so the main model review will be used for the final gate.",
		"별도 리뷰 모델이 없거나 메인 모델과 동일하여 메인 모델 리뷰 결과로 최종 게이트를 계산합니다.",
	)
}

func emitReviewModelFlowProgress(rt *runtimeState, english string, korean string) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	rt.agent.EmitProgress(localizedText(rt.cfg, english, korean))
}

func reviewModelSoftTimeoutForRun(cfg Config, run ReviewRun, reviewerRun ReviewReviewerRun, healthGroups ...[]ReviewRouteHealth) time.Duration {
	if !strings.EqualFold(strings.TrimSpace(reviewerRun.Kind), "cross") {
		return 0
	}
	role := normalizeReviewRole(reviewerRun.Role)
	provider, _ := reviewRoleProviderModelForRun(cfg, role)
	if labelProvider, _ := reviewProviderModelFromDisplayLabel(reviewerRun.Model); labelProvider != "" {
		provider = labelProvider
	}
	timeout := reviewDefaultCrossSoftTimeoutForProvider(provider)
	if timeout <= 0 {
		timeout = reviewCloudCrossSoftTimeout
	}
	if reviewRouteHealthHasRecentTimeout(mergeReviewRouteHealthGroups(healthGroups...), reviewerRun) {
		timeout = reviewAdaptiveCrossSoftTimeout(timeout)
	}
	return timeout
}

func reviewDefaultCrossSoftTimeoutForProvider(provider string) time.Duration {
	switch normalizeProviderName(provider) {
	case "codex-cli", "anthropic-claude-cli":
		return reviewCLICrossSoftTimeout
	case "ollama", "lmstudio", "vllm", "llama.cpp":
		return reviewLocalCrossSoftTimeout
	default:
		return reviewCloudCrossSoftTimeout
	}
}

func reviewAdaptiveCrossSoftTimeout(base time.Duration) time.Duration {
	if base <= 0 {
		base = reviewCloudCrossSoftTimeout
	}
	if base >= reviewAdaptiveTimeoutCrossSoftTimeout {
		return reviewAdaptiveTimeoutCrossSoftTimeout
	}
	extended := base + 3*time.Minute
	if extended > reviewAdaptiveTimeoutCrossSoftTimeout {
		return reviewAdaptiveTimeoutCrossSoftTimeout
	}
	return extended
}

func reviewRouteHealthForTimeout(rt *runtimeState, run ReviewRun) []ReviewRouteHealth {
	var out []ReviewRouteHealth
	if rt != nil && rt.session != nil {
		out = append(out, rt.session.ReviewRouteHealth...)
	}
	out = append(out, run.ModelPlan.RouteHealth...)
	return out
}

func mergeReviewRouteHealthGroups(groups ...[]ReviewRouteHealth) []ReviewRouteHealth {
	var out []ReviewRouteHealth
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func reviewRouteHealthHasRecentTimeout(items []ReviewRouteHealth, reviewerRun ReviewReviewerRun) bool {
	health, ok := reviewRouteHealthForReviewerRun(items, reviewerRun)
	if !ok {
		return false
	}
	return reviewRouteHealthNeedsAdaptiveTimeout(health)
}

func reviewRoleProviderModelForRun(cfg Config, role string) (string, string) {
	role = normalizeReviewRole(role)
	if role == "primary_reviewer" && (strings.TrimSpace(cfg.Provider) != "" || strings.TrimSpace(cfg.Model) != "") {
		return cfg.Provider, cfg.Model
	}
	reviewCfg := configReviewHarness(cfg)
	if roleCfg, ok := reviewCfg.RoleModels[role]; ok && strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
		return roleCfg.Provider, roleCfg.Model
	}
	if role == "cross_reviewer" {
		if roleCfg, ok := reviewCfg.RoleModels["primary_reviewer"]; ok && strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
			return roleCfg.Provider, roleCfg.Model
		}
	}
	if role != "primary_reviewer" {
		if roleCfg, ok := reviewCfg.RoleModels["primary_reviewer"]; ok && strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
			return roleCfg.Provider, roleCfg.Model
		}
	}
	return cfg.Provider, cfg.Model
}

func reviewProviderModelFromDisplayLabel(label string) (string, string) {
	parts := strings.Split(strings.TrimSpace(label), " / ")
	if len(parts) < 2 {
		return "", ""
	}
	provider := strings.TrimSpace(parts[0])
	model := strings.TrimSpace(parts[1])
	if strings.HasPrefix(strings.ToLower(model), "effort=") {
		model = ""
	}
	return provider, model
}

func reviewModelCapabilityRank(provider string, model string, effort string) int {
	provider = normalizeProviderName(provider)
	rule, ok := reviewModelCapabilityRuleFor(provider, model)
	if !ok || rule.CapabilityRank <= 0 {
		return 0
	}
	rank := rule.CapabilityRank
	switch normalizeReasoningEffort(effort) {
	case "xhigh":
		rank += 40
	case "high":
		rank += 20
	case "low":
		rank -= 20
	case "minimal":
		rank -= 40
	}
	return rank
}

func reviewModelCallContext(ctx context.Context, softTimeout time.Duration) (context.Context, context.CancelFunc) {
	if softTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, softTimeout)
}

func reviewModelCallErrorText(err error, softTimeout time.Duration) string {
	if err == nil {
		return ""
	}
	if softTimeout > 0 && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("review model soft timeout after %s", formatProgressElapsed(softTimeout))
	}
	return err.Error()
}

func completeReviewModelTurnWithProgress(ctx context.Context, rt *runtimeState, reviewerRun ReviewReviewerRun, call func(context.Context) (ChatResponse, error)) (ChatResponse, error) {
	if call == nil {
		return ChatResponse{}, fmt.Errorf("review model call is not configured")
	}
	done := make(chan struct{})
	go emitReviewModelLongWaitProgress(ctx, rt, reviewerRun, done)
	resp, err := call(ctx)
	close(done)
	return resp, err
}

func emitReviewModelLongWaitProgress(ctx context.Context, rt *runtimeState, reviewerRun ReviewReviewerRun, done <-chan struct{}) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	delay := reviewModelLongWaitInitialDelay(reviewerRun.Kind)
	if delay <= 0 {
		return
	}
	startedAt := reviewerRun.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-timer.C:
			select {
			case <-done:
				return
			default:
			}
			rt.agent.EmitProgress(formatReviewModelLongWaitProgress(rt.cfg, reviewerRun, time.Since(startedAt)))
			timer.Reset(reviewModelLongWaitInterval(reviewerRun.Kind))
		}
	}
}

func reviewModelLongWaitInitialDelay(kind string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "cross":
		return 2 * time.Minute
	default:
		return 2 * time.Minute
	}
}

func reviewModelLongWaitInterval(kind string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "cross":
		return 2 * time.Minute
	default:
		return 2 * time.Minute
	}
}

func formatReviewModelLongWaitProgress(cfg Config, reviewerRun ReviewReviewerRun, elapsed time.Duration) string {
	elapsedText := formatProgressElapsed(elapsed)
	roleName := reviewRoleProgressName(reviewerRun.Role)
	switch strings.ToLower(strings.TrimSpace(reviewerRun.Kind)) {
	case "main":
		return fmt.Sprintf(
			localizedText(cfg, "Main model is still reading code and checking the repair direction (%s elapsed). actor=main_model next_transition=cross_review_or_gate_decision. When it returns, Kernforge will pass the draft to the review model or compute the gate if no separate reviewer is configured.", "메인 모델이 아직 코드를 읽고 수정 방향을 검토 중입니다(경과 %s). actor=main_model next_transition=cross_review_or_gate_decision. 결과가 오면 리뷰 모델에 초안을 전달하거나, 별도 리뷰 모델이 없으면 바로 게이트를 계산합니다."),
			elapsedText,
		)
	case "cross":
		return fmt.Sprintf(
			localizedText(cfg, "Review model is still cross-checking the main draft (%s elapsed). actor=reviewer_model next_transition=merge_reviews. When it returns, Kernforge will merge it with the main model review; timeout, cancellation, or an empty response will be recorded in the final gate.", "리뷰 모델이 아직 메인 초안을 교차 검토 중입니다(경과 %s). actor=reviewer_model next_transition=merge_reviews. 결과가 오면 메인 모델 리뷰와 병합하고, timeout/취소/빈 응답은 최종 게이트에 실패 상태로 기록합니다."),
			elapsedText,
		)
	default:
		return fmt.Sprintf(
			localizedText(cfg, "Review model %s is still running (%s elapsed). actor=reviewer_model next_transition=gate_decision. Kernforge will use the result in the final gate when it returns.", "리뷰 모델 %s가 아직 실행 중입니다(경과 %s). actor=reviewer_model next_transition=gate_decision. 결과가 오면 최종 게이트에 반영합니다."),
			roleName,
			elapsedText,
		)
	}
}

func emitReviewModelRetryProgress(rt *runtimeState, role string, label string, attempt int, budget int) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	roleName := reviewRoleProgressName(role)
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Review model output looked omitted or cut off; retrying strict review (%d/%d): %s -> %s.", "리뷰 모델 출력에 생략/잘림 징후가 있어 엄격 리뷰로 재시도합니다(%d/%d): %s -> %s."),
		attempt,
		budget,
		roleName,
		label,
	)
	rt.agent.EmitProgress(message)
}

func emitReviewModelRetrySkippedProgress(rt *runtimeState, reviewerRun ReviewReviewerRun, label string) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	roleName := reviewRoleProgressName(reviewerRun.Role)
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Review model output looked omitted or cut off, but strict retry is skipped for optional cross-check: the main model code review already has actionable findings and the reviewer did not report an explicit token-limit stop. %s -> %s.", "리뷰 모델 출력에 생략/잘림 징후가 있지만 선택적 교차 검토 strict retry를 생략합니다. 메인 모델 코드 검토가 이미 실행 가능한 finding을 제공했고, 리뷰어가 명시적인 token-limit stop을 보고하지 않았습니다. %s -> %s."),
		roleName,
		label,
	)
	rt.agent.EmitProgress(message)
}

func emitReviewModelHealthRetrySuppressedProgress(rt *runtimeState, reviewerRun ReviewReviewerRun, label string) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	roleName := reviewRoleProgressName(reviewerRun.Role)
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Review route health suppresses strict retry for %s -> %s. Recent route failures make another same-route retry low value.", "최근 리뷰 경로 상태 때문에 %s -> %s strict retry를 생략합니다. 같은 경로 재시도는 가치가 낮습니다."),
		roleName,
		label,
	)
	rt.agent.EmitProgress(message)
}

func reviewStopReasonLooksTruncated(stopReason string) bool {
	lower := strings.ToLower(strings.TrimSpace(stopReason))
	if lower == "" {
		return false
	}
	return containsAny(lower, "length", "max_token", "max token", "token_limit", "incomplete", "partial", "truncated")
}

func reviewShouldSkipOptionalCrossOmissionRetry(cfg Config, run ReviewRun, reviewerRun ReviewReviewerRun, stopReason string, findings []ReviewFinding, peer reviewModelRunPeerContext) bool {
	if !strings.EqualFold(strings.TrimSpace(reviewerRun.Kind), "cross") {
		return false
	}
	if reviewRunRequiresSuccessfulCrossReviewer(run) {
		return false
	}
	if !strings.EqualFold(normalizeProviderName(reviewRoleProviderForRun(cfg, reviewerRun.Role)), "deepseek") {
		return false
	}
	if reviewStopReasonLooksTruncated(stopReason) {
		return false
	}
	if len(findings) == 0 {
		return false
	}
	if !reviewFindingsContainUsableModelFinding(findings) {
		return false
	}
	return reviewPeerContextHasUsableMainActionableFindings(run, peer)
}

func reviewRouteHealthSuppressesStrictRetry(rt *runtimeState, reviewerRun ReviewReviewerRun) bool {
	if rt == nil || rt.session == nil {
		return false
	}
	health, ok := reviewRouteHealthForReviewerRun(rt.session.ReviewRouteHealth, reviewerRun)
	if !ok {
		return false
	}
	if health.RecentRuns < 2 {
		return false
	}
	return health.TimeoutRate >= 0.50 || health.EmptyResponseRate >= 0.50 || health.WeakRate >= 0.50
}

func reviewLocalModelEmptyResponseRetryAllowed(cfg Config, reviewerRun ReviewReviewerRun) bool {
	return reviewProviderUsesLocalModelRecovery(reviewReviewerRunProvider(cfg, reviewerRun))
}

func reviewLocalModelReasoningOnlyRetryAllowed(cfg Config, reviewerRun ReviewReviewerRun, reasoning string) bool {
	return strings.TrimSpace(reasoning) != "" &&
		reviewProviderUsesLocalModelRecovery(reviewReviewerRunProvider(cfg, reviewerRun))
}

func reviewLocalModelCompactRecoveryAllowed(cfg Config, reviewerRun ReviewReviewerRun, health ReviewRouteHealth) bool {
	if !reviewProviderUsesLocalModelRecovery(reviewReviewerRunProvider(cfg, reviewerRun)) {
		return false
	}
	if health.EmptyResponseRate >= 0.50 || health.WeakRate >= 0.50 {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(health.LastQuality), reviewModelQualityWeak)
}

func reviewInitialLocalCompactPromptAllowed(cfg Config, run ReviewRun, reviewerRun ReviewReviewerRun) bool {
	if !reviewProviderUsesLocalModelRecovery(reviewReviewerRunProvider(cfg, reviewerRun)) {
		return false
	}
	if len(strings.TrimSpace(run.Evidence.Text)) <= reviewLocalCompactReviewEvidenceLimit(run) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) ||
		strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") ||
		reviewRunUsesFocusedFastPath(run)
}

func reviewProviderUsesLocalModelRecovery(provider string) bool {
	provider = normalizeProviderName(provider)
	return provider == "ollama" || isLocalOpenAICompatibleProvider(provider)
}

func reviewReviewerRunProvider(cfg Config, reviewerRun ReviewReviewerRun) string {
	if provider, _ := reviewProviderModelFromDisplayLabel(reviewerRun.Model); strings.TrimSpace(provider) != "" {
		return normalizeProviderName(provider)
	}
	return normalizeProviderName(reviewRoleProviderForRun(cfg, reviewerRun.Role))
}

func reviewStructuredOutputFromReasoningContent(cfg Config, reviewerRun ReviewReviewerRun, reasoning string) string {
	if !reviewProviderUsesLocalModelRecovery(reviewReviewerRunProvider(cfg, reviewerRun)) {
		return ""
	}
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return ""
	}
	index := reviewReasoningReviewResultMarkerIndex(reasoning)
	if index < 0 {
		return ""
	}
	recovered := strings.TrimSpace(reasoning[index:])
	if recovered == "" {
		return ""
	}
	return recovered
}

func reviewReasoningReviewResultMarkerIndex(reasoning string) int {
	const marker = "REVIEW_RESULT"
	offset := 0
	for offset < len(reasoning) {
		lineEnd := strings.IndexByte(reasoning[offset:], '\n')
		if lineEnd < 0 {
			lineEnd = len(reasoning)
		} else {
			lineEnd += offset
		}
		line := strings.TrimRight(reasoning[offset:lineEnd], "\r")
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, marker) {
			leading := len(line) - len(strings.TrimLeft(line, " \t"))
			return offset + leading
		}
		if lineEnd >= len(reasoning) {
			break
		}
		offset = lineEnd + 1
	}
	return -1
}

func reviewRouteHealthSkipsInitialModelCall(rt *runtimeState, reviewerRun ReviewReviewerRun) (ReviewRouteHealth, bool) {
	if rt == nil || rt.session == nil {
		return ReviewRouteHealth{}, false
	}
	health, ok := reviewRouteHealthForReviewerRun(rt.session.ReviewRouteHealth, reviewerRun)
	if !ok {
		return ReviewRouteHealth{}, false
	}
	if health.RecentRuns <= 0 {
		return ReviewRouteHealth{}, false
	}
	if strings.EqualFold(strings.TrimSpace(health.LastQuality), reviewModelQualityWeak) {
		return health, true
	}
	timeoutOnlyFailure := health.TimeoutRate >= 0.50 && health.EmptyResponseRate < 0.50 && health.WeakRate < 0.50
	if strings.EqualFold(strings.TrimSpace(health.LastQuality), reviewModelQualityFailed) && timeoutOnlyFailure {
		return ReviewRouteHealth{}, false
	}
	if strings.EqualFold(strings.TrimSpace(health.LastQuality), reviewModelQualityFailed) &&
		(health.EmptyResponseRate >= 0.50 || health.WeakRate >= 0.50) {
		return health, true
	}
	if health.RecentRuns >= 2 && health.UsableFindingRate < 0.50 &&
		(health.EmptyResponseRate >= 0.50 || health.WeakRate >= 0.50) {
		return health, true
	}
	return ReviewRouteHealth{}, false
}

func reviewRouteHealthForReviewerRun(items []ReviewRouteHealth, reviewerRun ReviewReviewerRun) (ReviewRouteHealth, bool) {
	role := normalizeReviewRole(reviewerRun.Role)
	model := strings.ToLower(strings.TrimSpace(reviewerRun.Model))
	for _, item := range items {
		if normalizeReviewRole(item.Role) != role {
			continue
		}
		if model != "" && !strings.EqualFold(strings.TrimSpace(item.Model), strings.TrimSpace(reviewerRun.Model)) {
			continue
		}
		return item, true
	}
	return ReviewRouteHealth{}, false
}

func reviewPeerContextHasUsableMainActionableFindings(run ReviewRun, peer reviewModelRunPeerContext) bool {
	mainUsable := false
	for _, reviewerRun := range peer.PriorReviewerRuns {
		if !strings.EqualFold(strings.TrimSpace(reviewerRun.Kind), "main") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(reviewerRun.Status), "completed") {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityUsable) {
			mainUsable = true
			break
		}
	}
	if !mainUsable {
		return false
	}
	for _, finding := range peer.PriorFindings {
		finding.Normalize()
		if !strings.EqualFold(strings.TrimSpace(finding.Source), "model") {
			continue
		}
		if normalizeReviewRole(finding.ReviewerRole) != "primary_reviewer" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(finding.Category), "evidence_gap") ||
			strings.EqualFold(strings.TrimSpace(finding.Category), "test_gap") {
			continue
		}
		if !reviewFindingsContainUsableModelFinding([]ReviewFinding{finding}) {
			continue
		}
		if finding.BlocksGate || reviewSeverityRank(finding.Severity) <= reviewSeverityRank(reviewSeverityMedium) || reviewFindingBlocksGate(run, finding) {
			return true
		}
	}
	return false
}

func emitDistinctReviewGateResultProgress(rt *runtimeState, run ReviewRun) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	if !reviewRunUsedDistinctReviewerModel(rt.cfg, run) {
		return
	}
	verdict := firstNonBlankString(run.Gate.Verdict, run.Result.Verdict, "unknown")
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Review gate result: %s (blockers=%d, warnings=%d).", "리뷰 게이트 결과: %s (차단=%d, 경고=%d)."),
		verdict,
		len(run.Gate.BlockingFindings),
		len(run.Gate.WarningFindings),
	)
	rt.agent.EmitProgress(message)
}

func reviewRunUsedDistinctReviewerModel(cfg Config, run ReviewRun) bool {
	for _, reviewerRun := range run.ReviewerRuns {
		if reviewModelLabelDiffersFromMain(cfg, reviewerRun.Model) {
			return true
		}
	}
	return false
}

func reviewRoleProgressName(role string) string {
	if choice, ok := resolveReviewModelRoleChoice(role); ok {
		return choice.Label
	}
	role = normalizeReviewRole(role)
	role = strings.TrimSuffix(role, "_reviewer")
	role = strings.TrimSuffix(role, "_gate")
	return strings.ReplaceAll(role, "_", "-")
}

func reviewRoleReasoningEffort(cfg Config, role string) string {
	role = normalizeReviewRole(role)
	if role == "primary_reviewer" && strings.TrimSpace(cfg.Provider) != "" && strings.TrimSpace(cfg.Model) != "" {
		if strings.TrimSpace(cfg.ReasoningEffort) != "" {
			effort, _ := reviewReasoningEffortOrDefaultForProvider(cfg.Provider, cfg.ReasoningEffort)
			return effort
		}
		effort, _ := reviewReasoningEffortOrDefaultForProvider(cfg.Provider, "")
		return effort
	}
	reviewCfg := configReviewHarness(cfg)
	if roleCfg, ok := reviewCfg.RoleModels[role]; ok && strings.TrimSpace(roleCfg.ReasoningEffort) != "" {
		return reviewRoleConfiguredReasoningEffort(cfg, role, roleCfg)
	}
	if role != "primary_reviewer" {
		if roleCfg, ok := reviewCfg.RoleModels["primary_reviewer"]; ok && strings.TrimSpace(roleCfg.ReasoningEffort) != "" {
			return reviewRoleConfiguredReasoningEffort(cfg, "primary_reviewer", roleCfg)
		}
	}
	if strings.TrimSpace(cfg.ReasoningEffort) != "" {
		effort, _ := reviewReasoningEffortOrDefaultForProvider(reviewRoleProviderForRun(cfg, role), cfg.ReasoningEffort)
		return effort
	}
	effort, _ := reviewReasoningEffortOrDefaultForProvider(reviewRoleProviderForRun(cfg, role), "")
	return effort
}

func reviewRoleReasoningEffortForRun(cfg Config, role string, run ReviewRun) string {
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		return reviewRoleReasoningEffort(cfg, role)
	}
	needsDeepBugHunt := reviewBeforeFixNeedsDeepBugHunt(run)
	role = normalizeReviewRole(role)
	if role == "primary_reviewer" && strings.TrimSpace(cfg.Provider) != "" && strings.TrimSpace(cfg.Model) != "" {
		effort := reviewRoleReasoningEffort(cfg, role)
		if needsDeepBugHunt {
			return reasoningEffortAtLeast(effort, minimumReviewRoleReasoningEffort)
		}
		return effort
	}
	reviewCfg := configReviewHarness(cfg)
	if roleCfg, ok := reviewCfg.RoleModels[role]; ok && strings.TrimSpace(roleCfg.ReasoningEffort) != "" {
		effort := reviewRoleConfiguredReasoningEffort(cfg, role, roleCfg)
		if needsDeepBugHunt {
			return reasoningEffortAtLeast(effort, minimumReviewRoleReasoningEffort)
		}
		return effort
	}
	if role != "primary_reviewer" {
		if roleCfg, ok := reviewCfg.RoleModels["primary_reviewer"]; ok && strings.TrimSpace(roleCfg.ReasoningEffort) != "" {
			effort := reviewRoleConfiguredReasoningEffort(cfg, "primary_reviewer", roleCfg)
			if needsDeepBugHunt {
				return reasoningEffortAtLeast(effort, minimumReviewRoleReasoningEffort)
			}
			return effort
		}
	}
	if needsDeepBugHunt {
		if effort := reviewRoleReasoningEffort(cfg, role); effort != "" {
			return reasoningEffortAtLeast(effort, minimumReviewRoleReasoningEffort)
		}
		return minimumReviewRoleReasoningEffort
	}
	return reviewRoleReasoningEffort(cfg, role)
}

func reviewRoleConfiguredReasoningEffort(cfg Config, role string, roleCfg ReviewModelConfig) string {
	role = normalizeReviewRole(role)
	provider := firstNonBlankString(roleCfg.Provider, reviewRoleProviderForRun(cfg, role))
	effort, _ := reviewReasoningEffortOrDefaultForProvider(provider, roleCfg.ReasoningEffort)
	if !reviewConfiguredRouteMatchesMain(cfg, provider, roleCfg.Model, roleCfg.BaseURL) {
		return effort
	}
	mainEffort := normalizeReasoningEffort(cfg.ReasoningEffort)
	if mainEffort == "" {
		return effort
	}
	return reasoningEffortAtLeast(effort, mainEffort)
}

func reviewConfiguredRouteMatchesMain(cfg Config, provider string, model string, baseURL ...string) bool {
	if strings.TrimSpace(cfg.Provider) == "" || strings.TrimSpace(cfg.Model) == "" {
		return false
	}
	roleProvider := normalizeProviderName(provider)
	mainProvider := normalizeProviderName(cfg.Provider)
	if roleProvider != mainProvider {
		return false
	}
	model = firstNonBlankString(model, cfg.Model)
	if !strings.EqualFold(strings.TrimSpace(model), strings.TrimSpace(cfg.Model)) {
		return false
	}
	roleBaseURLInput := ""
	if len(baseURL) > 0 {
		roleBaseURLInput = strings.TrimSpace(baseURL[0])
	}
	if roleBaseURLInput == "" {
		roleBaseURLInput = strings.TrimSpace(cfg.BaseURL)
	}
	roleBaseURL := normalizeProviderBaseURL(roleProvider, roleBaseURLInput)
	mainBaseURL := normalizeProviderBaseURL(mainProvider, cfg.BaseURL)
	return strings.EqualFold(roleBaseURL, mainBaseURL)
}

func reasoningEffortAtLeast(effort string, minimum string) string {
	effort = normalizeReasoningEffort(effort)
	minimum = normalizeReasoningEffort(minimum)
	if reasoningEffortRank(effort) >= reasoningEffortRank(minimum) {
		return effort
	}
	return minimum
}

func reasoningEffortRank(effort string) int {
	switch normalizeReasoningEffort(effort) {
	case "minimal":
		return 1
	case "low":
		return 2
	case "medium":
		return 3
	case "high":
		return 4
	case "xhigh":
		return 5
	default:
		return 0
	}
}

func reviewRoleMaxTokensForRun(cfg Config, run ReviewRun) int {
	return reviewRoleMaxTokensForProvider(cfg, cfg.Provider, run)
}

func reviewRoleMaxTokensForRoleRun(cfg Config, role string, run ReviewRun) int {
	return reviewRoleMaxTokensForProvider(cfg, reviewRoleProviderForRun(cfg, role), run)
}

func reviewRoleMaxTokensForProvider(cfg Config, provider string, run ReviewRun) int {
	behavior := reviewProviderBehavior(provider)
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		return reviewProviderTokenLimit(cfg.MaxTokens, behavior.MaxReviewTokens)
	}
	maxTokens := reviewProviderTokenLimit(cfg.MaxTokens, behavior.MaxReviewTokens)
	if reviewBeforeFixNeedsDeepBugHunt(run) {
		if maxTokens <= 0 {
			return 4096
		}
		if maxTokens > 6000 {
			return 6000
		}
		if maxTokens < 4096 {
			return maxTokens
		}
		return maxTokens
	}
	if maxTokens <= 0 || maxTokens > 2048 {
		return 2048
	}
	return maxTokens
}

func reviewRoleRetryMaxTokensForRun(cfg Config, run ReviewRun) int {
	return reviewRoleRetryMaxTokensForProvider(cfg, cfg.Provider, run)
}

func reviewRoleRetryMaxTokensForRoleRun(cfg Config, role string, run ReviewRun) int {
	return reviewRoleRetryMaxTokensForProvider(cfg, reviewRoleProviderForRun(cfg, role), run)
}

func reviewRoleRetryMaxTokensForProvider(cfg Config, provider string, run ReviewRun) int {
	behavior := reviewProviderBehavior(provider)
	if behavior.RetryReviewTokens > 0 {
		return behavior.RetryReviewTokens
	}
	maxTokens := reviewRoleMaxTokensForProvider(cfg, provider, run)
	if maxTokens <= 0 || maxTokens < 4096 {
		return 4096
	}
	return maxTokens
}

func reviewRoleOmissionRetryBudgetForRun(cfg Config, role string) int {
	budget := reviewProviderBehavior(reviewRoleProviderForRun(cfg, role)).OmissionRetryBudget
	if budget < 0 {
		return 0
	}
	return budget
}

func reviewRoleOmissionRetryBudgetForReviewRun(cfg Config, role string, run ReviewRun, kind string) int {
	budget := reviewRoleOmissionRetryBudgetForRun(cfg, role)
	if budget <= 0 {
		return 0
	}
	provider := normalizeProviderName(reviewRoleProviderForRun(cfg, role))
	if strings.EqualFold(provider, "deepseek") && budget > 1 {
		budget = 1
	}
	if strings.EqualFold(strings.TrimSpace(kind), "cross") && reviewRunUsesFocusedFastPath(run) && budget > 1 {
		budget = 1
	}
	return budget
}

func reviewRoleProviderForRun(cfg Config, role string) string {
	role = normalizeReviewRole(role)
	if role == "primary_reviewer" && strings.TrimSpace(cfg.Provider) != "" {
		return cfg.Provider
	}
	reviewCfg := configReviewHarness(cfg)
	if roleCfg, ok := reviewCfg.RoleModels[role]; ok && strings.TrimSpace(roleCfg.Provider) != "" {
		return roleCfg.Provider
	}
	if role == "cross_reviewer" {
		if roleCfg, ok := reviewCfg.RoleModels["primary_reviewer"]; ok && strings.TrimSpace(roleCfg.Provider) != "" {
			return roleCfg.Provider
		}
	}
	if role != "primary_reviewer" {
		if roleCfg, ok := reviewCfg.RoleModels["primary_reviewer"]; ok && strings.TrimSpace(roleCfg.Provider) != "" {
			return roleCfg.Provider
		}
	}
	return cfg.Provider
}

func roleHasDedicatedReviewModel(reviewCfg ReviewHarnessConfig, role string) bool {
	roleCfg, ok := reviewCfg.RoleModels[normalizeReviewRole(role)]
	return ok && strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != ""
}

func reviewRoleArtifactPaths(root string, id string, role string) (string, string) {
	return reviewRoleAttemptArtifactPaths(root, id, role, 0)
}

func reviewRoleAttemptArtifactPaths(root string, id string, role string, attempt int) (string, string) {
	dir := reviewRunDir(root, id)
	_ = os.MkdirAll(dir, 0o755)
	safeRole := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(normalizeReviewRole(role))
	if attempt > 0 {
		safeRole = fmt.Sprintf("%s_retry%d", safeRole, attempt)
	}
	return filepath.Join(dir, "prompt_"+safeRole+".md"), filepath.Join(dir, "raw_"+safeRole+".md")
}

func reviewRoleNamedAttemptArtifactPaths(root string, id string, role string, suffix string) (string, string) {
	dir := reviewRunDir(root, id)
	_ = os.MkdirAll(dir, 0o755)
	safeRole := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(normalizeReviewRole(role))
	safeSuffix := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_").Replace(strings.TrimSpace(suffix))
	if safeSuffix != "" {
		safeRole = safeRole + "_" + safeSuffix
	}
	return filepath.Join(dir, "prompt_"+safeRole+".md"), filepath.Join(dir, "raw_"+safeRole+".md")
}

func reviewRoleProviderRawArtifactPath(root string, id string, role string, suffix string) string {
	dir := reviewRunDir(root, id)
	_ = os.MkdirAll(dir, 0o755)
	safeRole := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(normalizeReviewRole(role))
	safeSuffix := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_").Replace(strings.TrimSpace(suffix))
	if safeSuffix != "" {
		safeRole = safeRole + "_" + safeSuffix
	}
	return filepath.Join(dir, "provider_raw_"+safeRole+".json")
}

func writeReviewProviderRawResponseArtifact(root string, id string, role string, suffix string, rawBody string) (string, ReviewRedactionReport) {
	rawBody = strings.TrimSpace(rawBody)
	if rawBody == "" {
		return "", ReviewRedactionReport{}
	}
	redacted, redaction := redactSensitiveText(rawBody)
	path := reviewRoleProviderRawArtifactPath(root, id, role, suffix)
	_ = os.WriteFile(path, []byte(redacted), 0o644)
	return path, redaction
}

func reviewModelSystemPrompt(cfg Config, run ReviewRun, role string) string {
	var b strings.Builder
	b.WriteString("You are a KernForge structured review model.\n")
	b.WriteString("Review only the supplied evidence. Do not claim that you ran tests.\n")
	if reviewRunPrefersKorean(cfg, run) {
		b.WriteString("Write all human-readable narrative fields in Korean: summary, finding titles, evidence, impact, required_fix, and test_recommendation. Keep schema keys, enum values, code identifiers, paths, API names, commands, and quoted source code unchanged.\n")
	} else {
		b.WriteString("Write human-readable narrative fields in English unless the objective explicitly asks for another language. Keep schema keys, enum values, code identifiers, paths, API names, commands, and quoted source code unchanged.\n")
	}
	b.WriteString("Never use ellipses or omission markers in any review field, including three consecutive periods, Unicode ellipsis, truncation labels, or omitted-content labels. If you need to be concise, write a complete shorter sentence without hiding the missing middle or tail.\n")
	switch normalizeReviewRole(role) {
	case "cross_reviewer":
		b.WriteString("Act as an independent second-pass reviewer. First review the supplied evidence yourself, then compare against the primary model draft. Do not assume the primary draft is correct.\n")
	default:
		b.WriteString("Focus on correctness, security, stability, test gaps, and maintainability.\n")
	}
	if lensText := reviewLensSystemPrompt(run.ModelPlan); lensText != "" {
		b.WriteString(lensText)
	}
	b.WriteString("Return structured output in this shape:\n")
	b.WriteString("REVIEW_RESULT\n")
	b.WriteString("verdict: approved|approved_with_warnings|needs_revision|blocked|insufficient_evidence\n")
	b.WriteString("summary: <one paragraph>\n")
	b.WriteString("findings:\n")
	b.WriteString("- severity: blocker|high|medium|low|info\n")
	b.WriteString("  category: correctness|security|stability|performance|test_gap|maintainability|false_positive|bypass_surface|operational_risk|evidence_gap\n")
	b.WriteString("  path: <path or empty>\n")
	b.WriteString("  line: <1-based line number or 0>\n")
	b.WriteString("  symbol: <symbol or surface>\n")
	b.WriteString("  title: <complete short finding title under 120 characters>\n")
	b.WriteString("  evidence: <specific evidence from supplied context>\n")
	b.WriteString("  impact: <why it matters>\n")
	b.WriteString("  required_fix: <concrete fix>\n")
	b.WriteString("  test_recommendation: <specific validation>\n")
	b.WriteString("  resolution_status: <empty unless this review is reconciling an existing finding>\n")
	b.WriteString("  evidence_refs: <comma-separated evidence refs when available>\n")
	b.WriteString("  fix_refs: <comma-separated changed paths or commits when available>\n")
	b.WriteString("  verification_refs: <comma-separated verification refs when available>\n")
	return b.String()
}

func reviewModelLocalCompactSystemPrompt(cfg Config, run ReviewRun, role string) string {
	var b strings.Builder
	b.WriteString("You are a KernForge local-model review recovery pass.\n")
	b.WriteString("Use only the supplied evidence. Return only the REVIEW_RESULT block.\n")
	if reviewRunPrefersKorean(cfg, run) {
		b.WriteString("Write narrative field values in Korean. Keep schema keys and code identifiers unchanged.\n")
	} else {
		b.WriteString("Write narrative field values in English unless the objective asks otherwise.\n")
	}
	switch normalizeReviewRole(role) {
	case "cross_reviewer":
		b.WriteString("Act as a compact second-pass reviewer.\n")
	default:
		b.WriteString("Prioritize concrete correctness, stability, and maintainability issues.\n")
	}
	if lensText := reviewLensSystemPrompt(run.ModelPlan); lensText != "" {
		b.WriteString(lensText)
	}
	return b.String()
}

func reviewLensSystemPrompt(plan ReviewModelPlan) string {
	lenses := analysisUniqueStrings(append(append([]string(nil), plan.RequiredLenses...), plan.OptionalLenses...))
	if len(lenses) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Apply these review lenses as checklist priorities inside this route:\n")
	for _, lens := range lenses {
		if description := reviewLensDescription(lens); description != "" {
			b.WriteString("- ")
			b.WriteString(lens)
			b.WriteString(": ")
			b.WriteString(description)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func reviewLensDescription(lens string) string {
	switch normalizeReviewLens(lens) {
	case "correctness":
		return "correctness, stability, maintainability, and concrete code behavior."
	case "design":
		return "architecture, scope, reversibility, and long-term maintenance cost."
	case "security":
		return "security boundaries, privileged paths, bypass risk, stability, and abuse cases."
	case "false_positive":
		return "false positives, telemetry provenance, operator interpretability, and version drift."
	case "regression":
		return "behavior preservation, compatibility, OS/version drift, and refactor risk."
	case "test":
		return "verification coverage, replayability, and missing validation evidence."
	case "final_gate":
		return "final-readiness, conflicting findings, and residual-risk clarity."
	default:
		return ""
	}
}

func normalizeReviewLens(lens string) string {
	lens = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(lens, "-", "_")))
	switch lens {
	case "primary", "primary_reviewer", "general":
		return "correctness"
	case "architecture", "architect", "design_reviewer":
		return "design"
	case "security_reviewer":
		return "security"
	case "falsepositive", "fp", "false_positive_reviewer":
		return "false_positive"
	case "regression_reviewer":
		return "regression"
	case "test_reviewer", "verification":
		return "test"
	case "final", "gate", "final_gate_reviewer":
		return "final_gate"
	default:
		return lens
	}
}

func appendReviewLensPromptSection(b *strings.Builder, plan ReviewModelPlan) {
	if b == nil {
		return
	}
	if len(plan.RequiredLenses) == 0 && len(plan.OptionalLenses) == 0 {
		return
	}
	b.WriteString("\nReview lenses:\n")
	if len(plan.RequiredLenses) > 0 {
		fmt.Fprintf(b, "- required: %s\n", strings.Join(plan.RequiredLenses, ", "))
	}
	if len(plan.OptionalLenses) > 0 {
		fmt.Fprintf(b, "- optional: %s\n", strings.Join(plan.OptionalLenses, ", "))
	}
}

func buildReviewModelPrompt(cfg Config, run ReviewRun, role string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review id: %s\n", run.ID)
	fmt.Fprintf(&b, "Role: %s\n", role)
	fmt.Fprintf(&b, "Target: %s\n", run.Target)
	fmt.Fprintf(&b, "Mode: %s\n", run.Mode)
	fmt.Fprintf(&b, "Flow: %s\n", run.Flow)
	appendReviewLensPromptSection(&b, run.ModelPlan)
	if strings.TrimSpace(run.Objective) != "" {
		fmt.Fprintf(&b, "\nObjective:\n%s\n", run.Objective)
	}
	if len(run.PolicyPacks) > 0 {
		fmt.Fprintf(&b, "\nPolicy packs:\n- %s\n", strings.Join(run.PolicyPacks, "\n- "))
	}
	if len(run.ChangeSet.ChangedPaths) > 0 {
		fmt.Fprintf(&b, "\nChanged paths:\n- %s\n", strings.Join(limitStrings(run.ChangeSet.ChangedPaths, 64), "\n- "))
	}
	if len(run.Evidence.Warnings) > 0 {
		fmt.Fprintf(&b, "\nEvidence warnings:\n- %s\n", strings.Join(run.Evidence.Warnings, "\n- "))
	}
	if run.Redaction.Redacted {
		fmt.Fprintf(&b, "\nRedaction:\nSensitive evidence was redacted: %s\n", strings.Join(run.Redaction.Patterns, ", "))
	}
	evidenceLimit := reviewModelPromptEvidenceLimit(run)
	b.WriteString("\nReview evidence:\n")
	b.WriteString(compactReviewPromptSection(run.Evidence.Text, evidenceLimit))
	b.WriteString("\n\nRequired review rules:\n")
	b.WriteString("- Findings must be concrete and tied to supplied evidence.\n")
	b.WriteString("- Every finding must include a title field. Do not copy a long evidence or impact sentence into title.\n")
	b.WriteString("- For code findings, include the narrowest supplied path and 1-based line when the evidence provides one. Do not invent line numbers; use 0 or leave line empty when unknown.\n")
	b.WriteString("- A blocker/high finding must include evidence, impact, required_fix, and test_recommendation when applicable.\n")
	b.WriteString("- Review touched functions, call sites, ABI or data contracts, initialization defaults, buffer sizes, error paths, cancellation or timeout behavior, logging/output compatibility, and stale docs when those surfaces are present in evidence.\n")
	b.WriteString("- Use category test_gap only when the required action is to add/run tests or provide verification evidence. If evidence describes a production behavior defect and required_fix changes production code, control flow, data handling, or error handling, use correctness, stability, security, performance, or operational_risk instead even when tests are also recommended.\n")
	b.WriteString("- If evidence is insufficient, emit insufficient_evidence or evidence_gap findings.\n")
	b.WriteString("- Do not invent files, tests, or code not present in the evidence.\n")
	b.WriteString("- Do not use ellipses or omission markers in summary, title, evidence, impact, required_fix, or test_recommendation. This includes three consecutive periods, Unicode ellipsis, truncation labels, and omitted-content labels. Every field must be a complete sentence or phrase.\n")
	if run.Target == reviewTargetSourceAnalysis {
		b.WriteString("- This is a source analysis review, not a proposed code-change review. Findings should describe risks in the supplied source evidence, not missing implementation work unless the user explicitly asked for a fix.\n")
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), naturalReviewTrigger) {
		b.WriteString("- This is a Codex App-style review-mode request. Treat it as read-only code review: do not propose patches, do not ask to edit files, and do not describe any fix as already applied.\n")
		b.WriteString("- Prioritize actionable bugs, behavioral regressions, stability/security risks, and missing tests. Put concrete findings ahead of summary-level commentary.\n")
		b.WriteString("- If no issue is found, return approved with no findings and state the remaining test or evidence risk only in the summary.\n")
	}
	if run.Mode == reviewModePerformanceAnalysis {
		b.WriteString("- For performance or hitch analysis, calibrate severity carefully: use high/blocker only for evidence-backed data races, deadlocks, main-thread blocking, unbounded growth, or hot-path work that is clearly frequent. Use medium for plausible lock contention, repeated allocation, or broad-copy overhead when call frequency or profiling data is not supplied.\n")
	}
	if reviewRunPrefersKorean(cfg, run) {
		b.WriteString("- Write narrative field values in Korean. Keep schema keys, enum values, code identifiers, paths, API names, commands, and quoted source code unchanged.\n")
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		b.WriteString("- This is a pre-fix review. Do not describe required fixes as already applied. Write required_fix as an imperative action for the implementer.\n")
		b.WriteString("- Prefer code correctness findings over generic verification-gap findings unless verification evidence is essential to the fix.\n")
		if reviewBeforeFixNeedsDeepBugHunt(run) {
			b.WriteString("- This request asks to inspect code and fix bugs. Review the supplied source line by line for correctness, stability, performance, and boundary bugs before approving.\n")
			b.WriteString("- If you return approved with no actionable bug findings, the implementation pass will still perform independent source inspection; do not imply the code is proven bug-free.\n")
		}
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		b.WriteString("- This is a pre-write review. If evidence includes required repair findings from a pre-fix review, verify the proposed edit addresses every blocking finding and every medium-or-higher actionable warning listed there.\n")
		b.WriteString("- Evidence sections can overlap. Treat the Provided diff section as the authoritative proposed edit when present; use after_excerpt and expected_preview only as supporting excerpts. Do not report an evidence_gap just because a supporting excerpt is compacted if the same code appears anywhere else in the supplied evidence.\n")
		b.WriteString("- Latest verification evidence is supporting context only. Do not create a blocking pre-write finding solely because verification failed, was skipped, or is missing. Block only when the proposed diff itself is wrong, incomplete, or demonstrably responsible for the verification failure. Treat unrelated build, test, or verification-environment failures as non-blocking test_gap evidence.\n")
		b.WriteString("- Do not block on review-meta feedback such as another finding's severity, wording, or whether a previous finding is already solved. Mark that as info/non-blocking if you need to mention it; pre-write blockers must require a production code change in the proposed diff.\n")
		b.WriteString("- New low-severity hardening ideas are notes unless they are direct regressions introduced by the proposed diff. Do not convert unrelated cleanup, broad RAII refactors, or optional extra type support into blockers for a focused repair.\n")
		b.WriteString("- Do not approve a proposed edit that only fixes a blocker while leaving a listed actionable warning unresolved, unless the diff itself contains a clear reason that the warning is intentionally out of scope.\n")
		b.WriteString("- If a required repair finding is still unresolved, emit needs_revision with a concrete finding that names the original repair id.\n")
		b.WriteString("- If the proposed diff tries to satisfy multiple RFs with a whole-file rewrite, a large whole-function replacement, duplicated function endings/braces, or code outside the intended function, treat that as a patch correctness blocker even if the idea of the fix is sound.\n")
	}
	return b.String()
}

func buildReviewModelLocalCompactReviewPrompt(cfg Config, run ReviewRun, role string, reason string) string {
	var b strings.Builder
	if reviewRunPrefersKorean(cfg, run) {
		switch strings.TrimSpace(reason) {
		case "route_health":
			b.WriteString("이 review route는 최근 빈 응답이나 약한 구조화 출력이 있었습니다. 긴 재시도 대신 더 작은 형식으로 한 번만 복구 리뷰를 수행하세요.\n")
		case "empty_response":
			b.WriteString("이전 리뷰 응답이 비어 있었습니다. 같은 증거를 더 작은 형식으로 한 번만 다시 검토하세요.\n")
		case "reasoning_only":
			b.WriteString("이전 리뷰 응답이 provider reasoning channel에만 작성되고 final content가 비어 있었습니다. 같은 증거를 다시 검토하되 final content에는 REVIEW_RESULT 블록만 출력하세요.\n")
		case "large_local_context":
			b.WriteString("이 로컬/degraded review route에는 전체 리뷰 프롬프트가 너무 크므로 처음부터 작은 형식으로 검토하세요.\n")
		default:
			b.WriteString("로컬 모델용 compact review로 검토하세요.\n")
		}
		b.WriteString("출력은 REVIEW_RESULT 블록만 반환하세요. 설명 문단, markdown table, 코드 패치, 도구 호출 요청을 쓰지 마세요.\n")
		b.WriteString("finding은 최대 3개만 작성하세요. 확실한 코드 근거가 없으면 추측하지 말고 approved 또는 insufficient_evidence를 사용하세요.\n")
	} else {
		switch strings.TrimSpace(reason) {
		case "route_health":
			b.WriteString("This review route recently returned empty or weak structured output. Run one compact recovery review instead of repeating the long prompt.\n")
		case "empty_response":
			b.WriteString("The previous review response was empty. Retry once with this smaller review format.\n")
		case "reasoning_only":
			b.WriteString("The previous review wrote only provider reasoning content and left final content empty. Review the same evidence again and put only the REVIEW_RESULT block in final content.\n")
		case "large_local_context":
			b.WriteString("This local/degraded review route is using the compact format up front because the full review prompt is too large.\n")
		default:
			b.WriteString("Run a compact local-model review.\n")
		}
		b.WriteString("Return only the REVIEW_RESULT block. Do not write markdown tables, code patches, tool requests, or extra explanation.\n")
		b.WriteString("Write at most 3 findings. If there is no concrete code-backed issue, use approved or insufficient_evidence instead of guessing.\n")
	}
	fmt.Fprintf(&b, "\nReview id: %s\n", run.ID)
	fmt.Fprintf(&b, "Role: %s\n", role)
	fmt.Fprintf(&b, "Target: %s\n", run.Target)
	fmt.Fprintf(&b, "Mode: %s\n", run.Mode)
	fmt.Fprintf(&b, "Flow: %s\n", run.Flow)
	appendReviewLensPromptSection(&b, run.ModelPlan)
	if strings.TrimSpace(run.Objective) != "" {
		fmt.Fprintf(&b, "\nObjective:\n%s\n", run.Objective)
	}
	if len(run.ChangeSet.ChangedPaths) > 0 {
		fmt.Fprintf(&b, "\nChanged paths:\n- %s\n", strings.Join(limitStrings(run.ChangeSet.ChangedPaths, 16), "\n- "))
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		b.WriteString("\nPre-fix rule: report only concrete bugs or boundary risks tied to the supplied code. Do not claim fixes are already applied.\n")
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), naturalReviewTrigger) {
		b.WriteString("\nReview-mode rule: this is read-only code review. Report concrete findings only; do not write patch text, tool requests, or implementation steps.\n")
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		b.WriteString("\nPre-write rule: review the proposed diff only. If the patch is not clearly safe, return needs_revision.\n")
	}
	b.WriteString("\nRequired schema:\n")
	b.WriteString("REVIEW_RESULT\n")
	b.WriteString("verdict: approved|approved_with_warnings|needs_revision|blocked|insufficient_evidence\n")
	b.WriteString("summary: <one complete short sentence>\n")
	b.WriteString("findings:\n")
	b.WriteString("- severity: blocker|high|medium|low|info\n")
	b.WriteString("  category: correctness|security|stability|performance|test_gap|maintainability|false_positive|bypass_surface|operational_risk|evidence_gap\n")
	b.WriteString("  path: <path or empty>\n")
	b.WriteString("  line: <1-based line number or 0>\n")
	b.WriteString("  symbol: <symbol or surface>\n")
	b.WriteString("  title: <complete short title>\n")
	b.WriteString("  evidence: <specific evidence>\n")
	b.WriteString("  impact: <why it matters>\n")
	b.WriteString("  required_fix: <concrete fix>\n")
	b.WriteString("  test_recommendation: <specific validation>\n")
	b.WriteString("  resolution_status: <empty unless this review is reconciling an existing finding>\n")
	b.WriteString("  evidence_refs: <comma-separated evidence refs when available>\n")
	b.WriteString("  fix_refs: <comma-separated changed paths or commits when available>\n")
	b.WriteString("  verification_refs: <comma-separated verification refs when available>\n")
	b.WriteString("\nReview evidence:\n")
	b.WriteString(compactReviewPromptSection(run.Evidence.Text, reviewLocalCompactReviewEvidenceLimit(run)))
	return b.String()
}

func buildReviewModelCrossCheckPrompt(cfg Config, run ReviewRun, role string, primaryRaw string, primaryFindings []ReviewFinding) string {
	var b strings.Builder
	if reviewRunPrefersKorean(cfg, run) {
		b.WriteString("당신은 두 번째 패스 리뷰어입니다.\n")
		b.WriteString("먼저 아래 코드 증거를 독립적으로 검토한 뒤, 메인 모델의 1차 리뷰 초안과 비교하세요.\n")
		b.WriteString("메인 초안을 정답으로 가정하지 말고, 확인된 문제/누락된 문제/잘못된 finding만 구조화해서 반환하세요.\n")
		b.WriteString("새로운 문제가 없고 메인 초안이 타당하면 approved 또는 approved_with_warnings를 반환하세요.\n")
	} else {
		b.WriteString("You are a second-pass reviewer.\n")
		b.WriteString("Review the code evidence independently first, then compare it with the primary model draft review.\n")
		b.WriteString("Do not assume the primary draft is correct; return structured findings only for confirmed, missed, or incorrect issues that should affect the final result.\n")
		b.WriteString("If there are no additional issues and the primary draft is sound, return approved or approved_with_warnings.\n")
	}
	fmt.Fprintf(&b, "\nReview id: %s\n", run.ID)
	fmt.Fprintf(&b, "Role: %s\n", role)
	fmt.Fprintf(&b, "Target: %s\n", run.Target)
	fmt.Fprintf(&b, "Mode: %s\n", run.Mode)
	fmt.Fprintf(&b, "Flow: %s\n", run.Flow)
	appendReviewLensPromptSection(&b, run.ModelPlan)
	if strings.TrimSpace(run.Objective) != "" {
		fmt.Fprintf(&b, "\nObjective:\n%s\n", run.Objective)
	}
	if len(run.ChangeSet.ChangedPaths) > 0 {
		fmt.Fprintf(&b, "\nChanged paths:\n- %s\n", strings.Join(limitStrings(run.ChangeSet.ChangedPaths, 64), "\n- "))
	}
	if len(primaryFindings) > 0 {
		b.WriteString("\nPrimary model structured findings:\n")
		b.WriteString(compactReviewPromptSection(renderReviewFindingsForCrossPrompt(primaryFindings), reviewPrimaryFindingsCrossPromptLimit(run)))
		b.WriteString("\n")
	}
	if strings.TrimSpace(primaryRaw) != "" {
		b.WriteString("\nPrimary model raw draft:\n")
		b.WriteString(compactReviewPromptSection(primaryRaw, reviewPrimaryRawCrossPromptLimit(run)))
		b.WriteString("\n")
	}
	b.WriteString("\nRequired second-pass rules:\n")
	if strings.EqualFold(strings.TrimSpace(run.Trigger), naturalReviewTrigger) {
		b.WriteString("- This is read-only review mode. Do not request edits, output patches, or describe fixes as already applied.\n")
	}
	b.WriteString("- Findings must be concrete and tied to supplied evidence.\n")
	b.WriteString("- For code findings, include the narrowest supplied path and 1-based line when the evidence provides one. Do not invent line numbers; use 0 or leave line empty when unknown.\n")
	b.WriteString("- Do not repeat a primary finding unless you are confirming it with clearer evidence or correcting its severity/fix.\n")
	b.WriteString("- If you reject or downgrade a primary finding, emit a finding that clearly names the disputed primary issue in evidence.\n")
	b.WriteString("- Make each finding useful for primary-model triage: the evidence or title should identify whether it is a missed issue, confirmed primary issue, incorrect primary issue, residual risk, or verification gap.\n")
	b.WriteString("- Use category test_gap only when the required action is to add/run tests or provide verification evidence. If evidence describes a production behavior defect and required_fix changes production code, control flow, data handling, or error handling, use correctness, stability, security, performance, or operational_risk instead even when tests are also recommended.\n")
	b.WriteString("- Do not invent files, tests, or code not present in the evidence.\n")
	b.WriteString("- Do not use ellipses or omission markers in any narrative field.\n")
	if reviewRunPrefersKorean(cfg, run) {
		b.WriteString("- Write narrative field values in Korean. Keep schema keys, enum values, code identifiers, paths, API names, commands, and quoted source code unchanged.\n")
	}
	b.WriteString("\nRequired schema:\n")
	b.WriteString("REVIEW_RESULT\n")
	b.WriteString("verdict: approved|approved_with_warnings|needs_revision|blocked|insufficient_evidence\n")
	b.WriteString("summary: <one paragraph>\n")
	b.WriteString("findings:\n")
	b.WriteString("- severity: blocker|high|medium|low|info\n")
	b.WriteString("  category: correctness|security|stability|performance|test_gap|maintainability|false_positive|bypass_surface|operational_risk|evidence_gap\n")
	b.WriteString("  path: <path or empty>\n")
	b.WriteString("  line: <1-based line number or 0>\n")
	b.WriteString("  symbol: <symbol or surface>\n")
	b.WriteString("  title: <complete short finding title under 120 characters>\n")
	b.WriteString("  evidence: <specific evidence from supplied context>\n")
	b.WriteString("  impact: <why it matters>\n")
	b.WriteString("  required_fix: <concrete fix>\n")
	b.WriteString("  test_recommendation: <specific validation>\n")
	b.WriteString("  resolution_status: <empty unless this review is reconciling an existing finding>\n")
	b.WriteString("  evidence_refs: <comma-separated evidence refs when available>\n")
	b.WriteString("  fix_refs: <comma-separated changed paths or commits when available>\n")
	b.WriteString("  verification_refs: <comma-separated verification refs when available>\n")
	evidenceLimit := reviewModelCrossEvidenceLimit(run)
	b.WriteString("\nReview evidence:\n")
	b.WriteString(compactReviewPromptSection(run.Evidence.Text, evidenceLimit))
	return b.String()
}

func reviewModelPromptEvidenceLimit(run ReviewRun) int {
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		return reviewPreWritePromptEvidenceLimit
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		if reviewBeforeFixNeedsDeepBugHunt(run) {
			return reviewSourceAnalysisMaxContextChars
		}
		return reviewFocusedPromptEvidenceLimit
	}
	if reviewRunNeedsMultiFileEvidencePrompt(run) {
		return reviewDefaultMaxContextChars
	}
	if reviewRunUsesFocusedFastPath(run) {
		return reviewFocusedPromptEvidenceLimit
	}
	return reviewDefaultMaxContextChars
}

func reviewLocalCompactReviewEvidenceLimit(run ReviewRun) int {
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		return reviewPreWritePromptEvidenceLimit
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) && reviewBeforeFixNeedsDeepBugHunt(run) {
		return reviewSourceAnalysisMaxContextChars / 2
	}
	return reviewFocusedPromptEvidenceLimit
}

func reviewModelCrossEvidenceLimit(run ReviewRun) int {
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		return reviewPreWriteCrossEvidenceLimit
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) && reviewBeforeFixNeedsDeepBugHunt(run) {
		return reviewSourceAnalysisMaxContextChars / 2
	}
	if reviewRunNeedsMultiFileEvidencePrompt(run) {
		return reviewDefaultMaxContextChars / 2
	}
	if reviewRunUsesFocusedFastPath(run) {
		return reviewFocusedCrossEvidenceLimit
	}
	if run.Target == reviewTargetSourceAnalysis {
		return reviewSourceAnalysisMaxContextChars / 2
	}
	return 24000
}

func reviewRunNeedsMultiFileEvidencePrompt(run ReviewRun) bool {
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		return false
	}
	paths := append([]string(nil), run.ChangeSet.ChangedPaths...)
	paths = append(paths, run.RequestAnalysis.ScopeDiscovery.CandidateFiles...)
	return len(mcpReviewCleanPaths(paths)) > 3
}

func reviewPrimaryFindingsCrossPromptLimit(run ReviewRun) int {
	if reviewRunUsesFocusedFastPath(run) {
		return reviewFocusedPrimaryFindingCrossLimit
	}
	return 8000
}

func reviewPrimaryRawCrossPromptLimit(run ReviewRun) int {
	if reviewRunUsesFocusedFastPath(run) {
		return reviewFocusedPrimaryRawCrossLimit
	}
	return 12000
}

func renderReviewFindingsForCrossPrompt(findings []ReviewFinding) string {
	var b strings.Builder
	for _, finding := range findings {
		finding.Normalize()
		fmt.Fprintf(&b, "- %s [%s/%s] %s\n", valueOrDefault(finding.ID, "finding"), finding.Severity, finding.Category, valueOrDefault(finding.Title, "Review finding"))
		if strings.TrimSpace(finding.Path) != "" {
			fmt.Fprintf(&b, "  Path: %s\n", finding.Path)
		}
		if strings.TrimSpace(finding.Symbol) != "" {
			fmt.Fprintf(&b, "  Symbol: %s\n", finding.Symbol)
		}
		if strings.TrimSpace(finding.Evidence) != "" {
			fmt.Fprintf(&b, "  Evidence: %s\n", finding.Evidence)
		}
		if strings.TrimSpace(finding.RequiredFix) != "" {
			fmt.Fprintf(&b, "  Required fix: %s\n", finding.RequiredFix)
		}
	}
	return strings.TrimSpace(b.String())
}

func buildReviewModelOmissionRetryPrompt(cfg Config, run ReviewRun, role string) string {
	var b strings.Builder
	if reviewRunPrefersKorean(cfg, run) {
		b.WriteString("이전 리뷰 출력은 구조화된 필드 안에 말줄임표나 생략 표식이 들어 있어 거부되었습니다.\n")
		b.WriteString("아래 증거만 사용해 초소형 리뷰를 다시 작성하세요. 출력은 REVIEW_RESULT 블록만 반환하세요.\n")
		b.WriteString("엄격 규칙:\n")
		b.WriteString("- finding은 최대 2개만 작성하세요.\n")
		b.WriteString("- 각 narrative field는 한국어 120자 이하의 완결된 문장이나 구문이어야 합니다.\n")
		b.WriteString("- 세 개의 연속 마침표, Unicode ellipsis, truncation label, omitted-content label을 쓰지 마세요.\n")
		b.WriteString("- 구체적인 finding을 만들 수 없으면 추측하지 말고 insufficient_evidence 또는 approved 계열 verdict를 사용하세요.\n")
	} else {
		b.WriteString("The previous review output was rejected because a structured field contained an ellipsis or omission marker.\n")
		b.WriteString("Retry as a compact structured review using only the evidence below. Return only the REVIEW_RESULT block.\n")
		b.WriteString("Strict rules:\n")
		b.WriteString("- Write at most 2 findings.\n")
		b.WriteString("- Every narrative field value must be a complete sentence or phrase under 120 characters.\n")
		b.WriteString("- Do not use three consecutive periods, Unicode ellipsis, truncation labels, or omitted-content labels.\n")
		b.WriteString("- If you cannot produce a concrete finding, do not guess; use insufficient_evidence or an approved verdict.\n")
	}
	fmt.Fprintf(&b, "\nReview id: %s\n", run.ID)
	fmt.Fprintf(&b, "Role: %s\n", role)
	fmt.Fprintf(&b, "Target: %s\n", run.Target)
	fmt.Fprintf(&b, "Mode: %s\n", run.Mode)
	if strings.TrimSpace(run.Objective) != "" {
		fmt.Fprintf(&b, "\nObjective:\n%s\n", run.Objective)
	}
	if len(run.ChangeSet.ChangedPaths) > 0 {
		fmt.Fprintf(&b, "\nChanged paths:\n- %s\n", strings.Join(limitStrings(run.ChangeSet.ChangedPaths, 24), "\n- "))
	}
	if run.Target == reviewTargetSourceAnalysis {
		b.WriteString("\nScope rule: This is a source analysis review, not a proposed code-change review.\n")
	}
	if run.Mode == reviewModePerformanceAnalysis {
		b.WriteString("Severity rule: use high/blocker only for evidence-backed data races, deadlocks, main-thread blocking, unbounded growth, or clearly frequent hot-path work. Use medium for plausible contention or allocation overhead without frequency/profiling evidence.\n")
	}
	b.WriteString("\nRequired schema:\n")
	b.WriteString("REVIEW_RESULT\n")
	b.WriteString("verdict: approved|approved_with_warnings|needs_revision|blocked|insufficient_evidence\n")
	b.WriteString("summary: <complete short sentence>\n")
	b.WriteString("findings:\n")
	b.WriteString("- severity: blocker|high|medium|low|info\n")
	b.WriteString("  category: correctness|security|stability|performance|test_gap|maintainability|false_positive|bypass_surface|operational_risk|evidence_gap\n")
	b.WriteString("  path: <path or empty>\n")
	b.WriteString("  line: <1-based line number or 0>\n")
	b.WriteString("  symbol: <symbol or surface>\n")
	b.WriteString("  title: <complete short title>\n")
	b.WriteString("  evidence: <specific evidence>\n")
	b.WriteString("  impact: <why it matters>\n")
	b.WriteString("  required_fix: <concrete fix>\n")
	b.WriteString("  test_recommendation: <specific validation>\n")
	b.WriteString("\nReview evidence:\n")
	b.WriteString(compactReviewPromptSection(run.Evidence.Text, 18000))
	return b.String()
}

func compactReviewPromptSection(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	marker := "\n[Evidence shortened to fit review prompt budget.]"
	budget := limit - len(marker)
	if budget <= 0 {
		return strings.TrimSpace(marker)
	}
	if compacted := compactReviewPromptMarkdownSections(text, budget); compacted != "" {
		return strings.TrimSpace(compacted) + marker
	}
	var b strings.Builder
	for _, r := range text {
		if b.Len()+len(string(r)) > budget {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String()) + marker
}

func compactReviewPromptMarkdownSections(text string, budget int) string {
	sections := reviewPromptMarkdownSections(text)
	if budget <= 0 || len(sections) <= 1 {
		return ""
	}
	separator := "\n\n"
	remaining := budget
	remainingSections := len(sections)
	var rendered []string
	for _, section := range sections {
		if remainingSections <= 0 {
			break
		}
		share := remaining / remainingSections
		if share <= 0 {
			break
		}
		if len(rendered) > 0 {
			remaining -= len(separator)
			if remaining <= 0 {
				break
			}
			share = remaining / remainingSections
			if share <= 0 {
				break
			}
		}
		renderedSection := compactPromptSectionPreserveHeadTail(section, share)
		if strings.TrimSpace(renderedSection) == "" {
			break
		}
		rendered = append(rendered, renderedSection)
		remaining -= len(renderedSection)
		remainingSections--
	}
	if len(rendered) <= 1 {
		return ""
	}
	return strings.Join(rendered, separator)
}

func reviewPromptMarkdownSections(text string) []string {
	lines := reviewNormalizedLines(text)
	var sections []string
	var current []string
	flush := func() {
		if len(current) == 0 {
			return
		}
		section := strings.TrimSpace(strings.Join(current, "\n"))
		if section != "" {
			sections = append(sections, section)
		}
		current = nil
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flush()
		}
		current = append(current, line)
	}
	flush()
	return sections
}

func reviewModelQualityRank(quality string) int {
	switch quality {
	case reviewModelQualityStrong:
		return 0
	case reviewModelQualityUsable:
		return 1
	case reviewModelQualityWeak:
		return 2
	default:
		return 3
	}
}

func reviewModelQualityUsableOrBetter(quality string) bool {
	rank := reviewModelQualityRank(strings.TrimSpace(quality))
	return rank <= reviewModelQualityRank(reviewModelQualityUsable)
}
