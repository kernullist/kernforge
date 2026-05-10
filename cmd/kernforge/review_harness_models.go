package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func planReviewModels(cfg Config, run ReviewRun) ReviewModelPlan {
	reviewCfg := configReviewHarness(cfg)
	plan := ReviewModelPlan{
		AssignedModels: map[string]string{},
	}
	required := []string{"primary_reviewer"}
	optional := []string{}
	switch {
	case strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger):
		required = []string{preFixReviewRole(reviewCfg, run)}
	case run.Flow == "plan_review":
		required = []string{"design_reviewer"}
		if reviewRunSecuritySensitive(run) {
			optional = append(optional, "security_reviewer")
		}
	case run.Flow == "security_review" || run.Mode == reviewModeSecurityHardening:
		required = []string{"security_reviewer"}
		if reviewRunFalsePositiveSensitive(run) {
			required = append(required, "false_positive_reviewer")
		}
		optional = append(optional, "test_reviewer", "final_gate_reviewer")
	case run.Mode == reviewModeRefactor:
		required = []string{"primary_reviewer", "regression_reviewer"}
	case run.Mode == reviewModeUIPolish:
		required = []string{"design_reviewer"}
		optional = append(optional, "regression_reviewer")
	case run.Mode == reviewModeLiveFix:
		required = []string{"primary_reviewer", "test_reviewer"}
		optional = append(optional, "regression_reviewer")
	case run.Flow == "goal_review":
		required = []string{"primary_reviewer", "final_gate_reviewer"}
	case run.Flow == "pr_review":
		required = []string{"primary_reviewer"}
		optional = append(optional, "test_reviewer")
	default:
		required = []string{"primary_reviewer"}
		if reviewRunSecuritySensitive(run) {
			optional = append(optional, "security_reviewer")
		}
	}
	plan.RequiredRoles = analysisUniqueStrings(required)
	plan.OptionalRoles = analysisUniqueStrings(optional)
	for _, role := range append(append([]string(nil), plan.RequiredRoles...), plan.OptionalRoles...) {
		label, source := reviewRoleModelLabelAndSource(cfg, reviewCfg, role)
		if label != "" {
			plan.AssignedModels[role] = label
		} else {
			plan.MissingRoles = append(plan.MissingRoles, role)
		}
		if label != "" && reviewRoleNeedsDedicatedModel(role, run) && source != "role" {
			plan.MissingRoles = append(plan.MissingRoles, role)
			plan.DegradedRoles = append(plan.DegradedRoles, role)
		}
	}
	plan.MissingRoles = analysisUniqueStrings(plan.MissingRoles)
	plan.DegradedRoles = analysisUniqueStrings(plan.DegradedRoles)
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
	for _, role := range plan.MissingRoles {
		switch role {
		case "security_reviewer":
			plan.UserGuidance = append(plan.UserGuidance, "This review would benefit from a dedicated security reviewer. Configure it with /review models security.")
		case "false_positive_reviewer":
			plan.UserGuidance = append(plan.UserGuidance, "Anti-cheat or detection reviews benefit from a false-positive reviewer. Configure it with /review models false-positive.")
		}
	}
	return plan
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
	if roleCfg, ok := reviewCfg.RoleModels[role]; ok && strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
		return formatProviderModelEffortLabel(roleCfg.Provider, roleCfg.Model, roleCfg.ReasoningEffort), "role"
	}
	if role != "primary_reviewer" {
		if roleCfg, ok := reviewCfg.RoleModels["primary_reviewer"]; ok && strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
			return formatProviderModelEffortLabel(roleCfg.Provider, roleCfg.Model, roleCfg.ReasoningEffort), "primary_reviewer"
		}
	}
	if strings.TrimSpace(cfg.Provider) != "" && strings.TrimSpace(cfg.Model) != "" {
		return formatProviderModelEffortLabel(cfg.Provider, cfg.Model, cfg.ReasoningEffort), "main"
	}
	return "", ""
}

func reviewRoleNeedsDedicatedModel(role string, run ReviewRun) bool {
	switch normalizeReviewRole(role) {
	case "security_reviewer":
		return run.Mode == reviewModeSecurityHardening || reviewRunSecuritySensitive(run)
	case "false_positive_reviewer":
		return reviewRunFalsePositiveSensitive(run)
	default:
		return false
	}
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
	roles := run.ModelPlan.RequiredRoles
	if len(roles) == 0 {
		roles = []string{"primary_reviewer"}
	}
	if len(roles) > 2 {
		roles = roles[:2]
	}
	var findings []ReviewFinding
	var reviewerRuns []ReviewReviewerRun
	for _, role := range roles {
		role = normalizeReviewRole(role)
		client, model, label, err := reviewRoleClient(rt, role)
		label = reviewModelDisplayLabel(rt.cfg, client, model, label, reviewRoleReasoningEffortForRun(rt.cfg, role, *run))
		distinctReviewer := reviewModelLabelDiffersFromMain(rt.cfg, label)
		reviewerRun := ReviewReviewerRun{
			Role:      role,
			Model:     label,
			StartedAt: time.Now(),
		}
		if err != nil || client == nil || strings.TrimSpace(model) == "" {
			reviewerRun.Status = "failed"
			reviewerRun.ModelQuality = reviewModelQualityFailed
			if err != nil {
				reviewerRun.Error = err.Error()
			} else {
				reviewerRun.Error = "no reviewer model configured"
			}
			reviewerRun.FinishedAt = time.Now()
			reviewerRuns = append(reviewerRuns, reviewerRun)
			run.Result.Degraded = true
			run.Result.DegradedReason = strings.TrimSpace(reviewerRun.Error)
			if distinctReviewer {
				emitReviewModelResultProgress(rt, reviewerRun, 0)
			}
			continue
		}
		prompt := buildReviewModelPrompt(rt.cfg, *run, role)
		promptPath, rawPath := reviewRoleArtifactPaths(root, run.ID, role)
		_ = os.WriteFile(promptPath, []byte(prompt), 0o644)
		reviewerRun.PromptPath = promptPath
		if distinctReviewer {
			emitReviewModelRequestProgress(rt, role, label)
		}
		resp, err := rt.agent.completeModelTurnWithClient(ctx, client, ChatRequest{
			Model:           model,
			System:          reviewModelSystemPrompt(rt.cfg, *run, role),
			Messages:        []Message{{Role: "user", Text: prompt}},
			MaxTokens:       reviewRoleMaxTokensForRun(rt.cfg, *run),
			Temperature:     0.1,
			ReasoningEffort: reviewRoleReasoningEffortForRun(rt.cfg, role, *run),
			WorkingDir:      root,
		})
		reviewerRun.FinishedAt = time.Now()
		if err != nil {
			reviewerRun.Status = "failed"
			reviewerRun.ModelQuality = reviewModelQualityFailed
			reviewerRun.Error = err.Error()
			reviewerRuns = append(reviewerRuns, reviewerRun)
			run.Result.Degraded = true
			run.Result.DegradedReason = "review model failed: " + err.Error()
			if distinctReviewer {
				emitReviewModelResultProgress(rt, reviewerRun, 0)
			}
			continue
		}
		raw := strings.TrimSpace(resp.Message.Text)
		if raw == "" {
			raw = "(empty review response)"
		}
		raw, rawRedaction := redactSensitiveText(raw)
		_ = os.WriteFile(rawPath, []byte(raw), 0o644)
		reviewerRun.RawOutputPath = rawPath
		roleFindings, quality := parseModelReviewFindingsForLanguage(raw, role, reviewRunPrefersKorean(rt.cfg, *run))
		for i := range roleFindings {
			roleFindings[i].ReviewerRole = role
			roleFindings[i].Source = "model"
		}
		run.Redaction = mergeReviewRedactionReports(run.Redaction, rawRedaction)
		if reviewTextHasOmissionMarker(raw) || reviewFindingsContainOmittedOutputPlaceholder(roleFindings) {
			if distinctReviewer {
				emitReviewModelRetryProgress(rt, role, label)
			}
			retryPrompt := buildReviewModelOmissionRetryPrompt(rt.cfg, *run, role)
			retryPromptPath, retryRawPath := reviewRoleAttemptArtifactPaths(root, run.ID, role, 1)
			_ = os.WriteFile(retryPromptPath, []byte(retryPrompt), 0o644)
			retryResp, retryErr := rt.agent.completeModelTurnWithClient(ctx, client, ChatRequest{
				Model:           model,
				System:          reviewModelSystemPrompt(rt.cfg, *run, role),
				Messages:        []Message{{Role: "user", Text: retryPrompt}},
				MaxTokens:       reviewRoleRetryMaxTokensForRun(rt.cfg, *run),
				Temperature:     0.05,
				ReasoningEffort: reviewRoleReasoningEffortForRun(rt.cfg, role, *run),
				WorkingDir:      root,
			})
			reviewerRun.FinishedAt = time.Now()
			if retryErr == nil {
				retryRaw := strings.TrimSpace(retryResp.Message.Text)
				if retryRaw == "" {
					retryRaw = "(empty review response)"
				}
				retryRaw, retryRedaction := redactSensitiveText(retryRaw)
				_ = os.WriteFile(retryRawPath, []byte(retryRaw), 0o644)
				retryFindings, retryQuality := parseModelReviewFindingsForLanguage(retryRaw, role, reviewRunPrefersKorean(rt.cfg, *run))
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
		}
		reviewerRun.Status = "completed"
		reviewerRun.ModelQuality = quality
		reviewerRuns = append(reviewerRuns, reviewerRun)
		findings = append(findings, roleFindings...)
		if distinctReviewer {
			emitReviewModelResultProgress(rt, reviewerRun, len(roleFindings))
		}
		if quality == reviewModelQualityWeak || quality == reviewModelQualityFailed {
			run.Result.Degraded = true
			run.Result.DegradedReason = "model reviewer output quality was " + quality
		}
		if run.Result.ModelQuality == "" || reviewModelQualityRank(quality) > reviewModelQualityRank(run.Result.ModelQuality) {
			run.Result.ModelQuality = quality
		}
	}
	assignReviewFindingIDs(findings)
	return findings, reviewerRuns
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
		return client, cfgCopy.Model, formatProviderModelEffortLabel(cfgCopy.Provider, cfgCopy.Model, cfgCopy.ReasoningEffort), err
	}
	if role != "primary_reviewer" {
		if roleCfg, ok := reviewCfg.RoleModels["primary_reviewer"]; ok && strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
			cfgCopy := roleCfg
			client, err := createReviewerClient(&cfgCopy, rt.cfg)
			return client, cfgCopy.Model, formatProviderModelEffortLabel(cfgCopy.Provider, cfgCopy.Model, cfgCopy.ReasoningEffort), err
		}
	}
	if rt.agent != nil && rt.agent.AuxReviewerClient != nil && strings.TrimSpace(rt.agent.AuxReviewerModel) != "" && role != "primary_reviewer" {
		return rt.agent.AuxReviewerClient, rt.agent.AuxReviewerModel, formatProviderModelEffortLabel(rt.cfg.Provider, rt.agent.AuxReviewerModel, rt.cfg.ReasoningEffort), nil
	}
	if rt.agent != nil && rt.agent.ReviewerClient != nil && strings.TrimSpace(rt.agent.ReviewerModel) != "" {
		return rt.agent.ReviewerClient, rt.agent.ReviewerModel, formatProviderModelEffortLabel(rt.cfg.Provider, rt.agent.ReviewerModel, rt.cfg.ReasoningEffort), nil
	}
	if rt.agent != nil && rt.agent.Client != nil && strings.TrimSpace(rt.cfg.Model) != "" {
		return rt.agent.Client, rt.cfg.Model, formatProviderModelEffortLabel(rt.cfg.Provider, rt.cfg.Model, rt.cfg.ReasoningEffort), nil
	}
	return nil, "", "", fmt.Errorf("no reviewer model configured")
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

func emitReviewModelRequestProgress(rt *runtimeState, role string, label string) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	mainLabel := reviewMainModelLabel(rt.cfg)
	roleName := reviewRoleProgressName(role)
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Review model request: %s -> %s (main: %s).", "리뷰 모델 요청: %s -> %s (메인: %s)."),
		roleName,
		label,
		mainLabel,
	)
	rt.agent.EmitProgress(message)
}

func emitReviewModelResultProgress(rt *runtimeState, run ReviewReviewerRun, findingCount int) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	roleName := reviewRoleProgressName(run.Role)
	status := strings.TrimSpace(run.Status)
	if status == "" {
		status = "completed"
	}
	if strings.TrimSpace(run.Error) != "" {
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
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Review model result: %s %s (quality=%s, findings=%d).", "리뷰 모델 결과: %s %s (품질=%s, 발견=%d)."),
		roleName,
		status,
		quality,
		findingCount,
	)
	rt.agent.EmitProgress(message)
}

func emitReviewModelRetryProgress(rt *runtimeState, role string, label string) {
	if rt == nil || rt.agent == nil || rt.agent.EmitProgress == nil {
		return
	}
	roleName := reviewRoleProgressName(role)
	message := fmt.Sprintf(
		localizedText(rt.cfg, "Review model output used omission markers; retrying strict review: %s -> %s.", "리뷰 모델 출력에 생략 표식이 있어 엄격 리뷰로 재시도합니다: %s -> %s."),
		roleName,
		label,
	)
	rt.agent.EmitProgress(message)
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
	reviewCfg := configReviewHarness(cfg)
	if roleCfg, ok := reviewCfg.RoleModels[role]; ok && strings.TrimSpace(roleCfg.ReasoningEffort) != "" {
		return roleCfg.ReasoningEffort
	}
	if role != "primary_reviewer" {
		if roleCfg, ok := reviewCfg.RoleModels["primary_reviewer"]; ok && strings.TrimSpace(roleCfg.ReasoningEffort) != "" {
			return roleCfg.ReasoningEffort
		}
	}
	return cfg.ReasoningEffort
}

func reviewRoleReasoningEffortForRun(cfg Config, role string, run ReviewRun) string {
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		return reviewRoleReasoningEffort(cfg, role)
	}
	role = normalizeReviewRole(role)
	reviewCfg := configReviewHarness(cfg)
	if roleCfg, ok := reviewCfg.RoleModels[role]; ok && strings.TrimSpace(roleCfg.ReasoningEffort) != "" {
		return roleCfg.ReasoningEffort
	}
	if role != "primary_reviewer" {
		if roleCfg, ok := reviewCfg.RoleModels["primary_reviewer"]; ok && strings.TrimSpace(roleCfg.ReasoningEffort) != "" {
			return roleCfg.ReasoningEffort
		}
	}
	return "low"
}

func reviewRoleMaxTokensForRun(cfg Config, run ReviewRun) int {
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		return cfg.MaxTokens
	}
	if cfg.MaxTokens <= 0 || cfg.MaxTokens > 2048 {
		return 2048
	}
	return cfg.MaxTokens
}

func reviewRoleRetryMaxTokensForRun(cfg Config, run ReviewRun) int {
	maxTokens := reviewRoleMaxTokensForRun(cfg, run)
	if maxTokens <= 0 || maxTokens < 4096 {
		return 4096
	}
	return maxTokens
}

func preFixReviewRole(reviewCfg ReviewHarnessConfig, run ReviewRun) string {
	if reviewRunSecuritySensitive(run) {
		if roleHasDedicatedReviewModel(reviewCfg, "security_reviewer") {
			return "security_reviewer"
		}
		if strings.Contains(strings.ToLower(run.Objective), "오탐") ||
			strings.Contains(strings.ToLower(run.Objective), "false positive") ||
			strings.Contains(strings.ToLower(run.Objective), "false-positive") {
			if roleHasDedicatedReviewModel(reviewCfg, "false_positive_reviewer") {
				return "false_positive_reviewer"
			}
		}
	}
	if roleHasDedicatedReviewModel(reviewCfg, "primary_reviewer") {
		return "primary_reviewer"
	}
	return "primary_reviewer"
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
	case "design_reviewer":
		b.WriteString("Focus on architecture, scope, reversibility, and long-term maintenance cost.\n")
	case "security_reviewer":
		b.WriteString("Focus on security boundaries, privileged paths, bypass risk, stability, and abuse cases.\n")
	case "false_positive_reviewer":
		b.WriteString("Focus on false positives, telemetry provenance, operator interpretability, and version drift.\n")
	case "regression_reviewer":
		b.WriteString("Focus on behavior preservation, compatibility, and regression risk.\n")
	default:
		b.WriteString("Focus on correctness, security, stability, test gaps, and maintainability.\n")
	}
	b.WriteString("Return structured output in this shape:\n")
	b.WriteString("REVIEW_RESULT\n")
	b.WriteString("verdict: approved|approved_with_warnings|needs_revision|blocked|insufficient_evidence\n")
	b.WriteString("summary: <one paragraph>\n")
	b.WriteString("findings:\n")
	b.WriteString("- severity: blocker|high|medium|low|info\n")
	b.WriteString("  category: correctness|security|stability|performance|test_gap|maintainability|false_positive|bypass_surface|operational_risk|evidence_gap\n")
	b.WriteString("  path: <path or empty>\n")
	b.WriteString("  symbol: <symbol or surface>\n")
	b.WriteString("  evidence: <specific evidence from supplied context>\n")
	b.WriteString("  impact: <why it matters>\n")
	b.WriteString("  required_fix: <concrete fix>\n")
	b.WriteString("  test_recommendation: <specific validation>\n")
	return b.String()
}

func buildReviewModelPrompt(cfg Config, run ReviewRun, role string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review id: %s\n", run.ID)
	fmt.Fprintf(&b, "Role: %s\n", role)
	fmt.Fprintf(&b, "Target: %s\n", run.Target)
	fmt.Fprintf(&b, "Mode: %s\n", run.Mode)
	fmt.Fprintf(&b, "Flow: %s\n", run.Flow)
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
	evidenceLimit := 60000
	if strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		evidenceLimit = 12000
	}
	b.WriteString("\nReview evidence:\n")
	b.WriteString(compactReviewPromptSection(run.Evidence.Text, evidenceLimit))
	b.WriteString("\n\nRequired review rules:\n")
	b.WriteString("- Findings must be concrete and tied to supplied evidence.\n")
	b.WriteString("- A blocker/high finding must include evidence, impact, required_fix, and test_recommendation when applicable.\n")
	b.WriteString("- If evidence is insufficient, emit insufficient_evidence or evidence_gap findings.\n")
	b.WriteString("- Do not invent files, tests, or code not present in the evidence.\n")
	b.WriteString("- Do not use ellipses or omission markers in summary, title, evidence, impact, required_fix, or test_recommendation. This includes three consecutive periods, Unicode ellipsis, truncation labels, and omitted-content labels. Every field must be a complete sentence or phrase.\n")
	if reviewRunPrefersKorean(cfg, run) {
		b.WriteString("- Write narrative field values in Korean. Keep schema keys, enum values, code identifiers, paths, API names, commands, and quoted source code unchanged.\n")
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		b.WriteString("- This is a pre-fix review. Do not describe required fixes as already applied. Write required_fix as an imperative action for the implementer.\n")
		b.WriteString("- Prefer code correctness findings over generic verification-gap findings unless verification evidence is essential to the fix.\n")
	}
	return b.String()
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
	b.WriteString("\nRequired schema:\n")
	b.WriteString("REVIEW_RESULT\n")
	b.WriteString("verdict: approved|approved_with_warnings|needs_revision|blocked|insufficient_evidence\n")
	b.WriteString("summary: <complete short sentence>\n")
	b.WriteString("findings:\n")
	b.WriteString("- severity: blocker|high|medium|low|info\n")
	b.WriteString("  category: correctness|security|stability|performance|test_gap|maintainability|false_positive|bypass_surface|operational_risk|evidence_gap\n")
	b.WriteString("  path: <path or empty>\n")
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
	var b strings.Builder
	for _, r := range text {
		if b.Len()+len(string(r)) > budget {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String()) + marker
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
