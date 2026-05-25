package main

import (
	"sort"
	"strings"
	"time"
)

const minimumReviewRoleReasoningEffort = "high"

type ReviewProviderBehavior struct {
	Provider                   string   `json:"provider,omitempty"`
	DisplayLabel               string   `json:"display_label,omitempty"`
	DefaultReviewEffort        string   `json:"default_review_effort,omitempty"`
	MaxReviewTokens            int      `json:"max_review_tokens,omitempty"`
	RetryReviewTokens          int      `json:"retry_review_tokens,omitempty"`
	OmissionRetryBudget        int      `json:"omission_retry_budget,omitempty"`
	SchemaStrictness           string   `json:"schema_strictness,omitempty"`
	ToolCallRecoveryPolicy     string   `json:"tool_call_recovery_policy,omitempty"`
	PatchFormatRisk            string   `json:"patch_format_risk,omitempty"`
	RecommendedRoles           []string `json:"recommended_roles,omitempty"`
	UnavailableCapabilityNotes []string `json:"unavailable_capability_notes,omitempty"`
}

type ReviewModelCapability struct {
	Role                    string `json:"role,omitempty"`
	Provider                string `json:"provider,omitempty"`
	ModelPattern            string `json:"model_pattern,omitempty"`
	CapabilityRank          int    `json:"capability_rank,omitempty"`
	SchemaReliability       string `json:"schema_reliability,omitempty"`
	BlockerDetectionPrior   string `json:"blocker_detection_prior,omitempty"`
	LatencyClass            string `json:"latency_class,omitempty"`
	SupportsReasoningEffort bool   `json:"supports_reasoning_effort"`
	RecommendedTimeoutMS    int64  `json:"recommended_timeout_ms,omitempty"`
	RetryBudget             int    `json:"retry_budget,omitempty"`
}

type ReviewRouteHealth struct {
	Role              string  `json:"role,omitempty"`
	Model             string  `json:"model,omitempty"`
	RecentRuns        int     `json:"recent_runs,omitempty"`
	TimeoutRate       float64 `json:"timeout_rate,omitempty"`
	EmptyResponseRate float64 `json:"empty_response_rate,omitempty"`
	WeakRate          float64 `json:"weak_rate,omitempty"`
	UsableFindingRate float64 `json:"usable_finding_rate,omitempty"`
	LastStatus        string  `json:"last_status,omitempty"`
	LastQuality       string  `json:"last_quality,omitempty"`
	LastTimeout       bool    `json:"last_timeout,omitempty"`
	MedianLatencyMS   int64   `json:"median_latency_ms,omitempty"`
	Recommendation    string  `json:"recommendation,omitempty"`
}

type reviewModelCapabilityRule struct {
	Provider              string
	ModelPattern          string
	CapabilityRank        int
	SchemaReliability     string
	BlockerDetectionPrior string
	LatencyClass          string
	RecommendedTimeout    time.Duration
}

var reviewModelCapabilityRules = []reviewModelCapabilityRule{
	{ModelPattern: "gpt-5.5", CapabilityRank: 1000, SchemaReliability: "high", BlockerDetectionPrior: "high", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{ModelPattern: "gpt-5.4", CapabilityRank: 940, SchemaReliability: "high", BlockerDetectionPrior: "high", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{ModelPattern: "gpt-5.3", CapabilityRank: 900, SchemaReliability: "high", BlockerDetectionPrior: "high", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{ModelPattern: "gpt-5.2", CapabilityRank: 860, SchemaReliability: "high", BlockerDetectionPrior: "high", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{ModelPattern: "opus", CapabilityRank: 900, SchemaReliability: "high", BlockerDetectionPrior: "high", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{ModelPattern: "sonnet", CapabilityRank: 780, SchemaReliability: "high", BlockerDetectionPrior: "medium", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{ModelPattern: "deepseek-v4-pro", CapabilityRank: 760, SchemaReliability: "standard", BlockerDetectionPrior: "medium", LatencyClass: "slow", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{ModelPattern: "deepseek v4 pro", CapabilityRank: 760, SchemaReliability: "standard", BlockerDetectionPrior: "medium", LatencyClass: "slow", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{ModelPattern: "deepseek", CapabilityRank: 730, SchemaReliability: "standard", BlockerDetectionPrior: "medium", LatencyClass: "slow", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{ModelPattern: "haiku", CapabilityRank: 480, SchemaReliability: "standard", BlockerDetectionPrior: "low", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{ModelPattern: "gpt-4.1", CapabilityRank: 760, SchemaReliability: "standard", BlockerDetectionPrior: "medium", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{ModelPattern: "gpt-4", CapabilityRank: 700, SchemaReliability: "standard", BlockerDetectionPrior: "medium", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{Provider: "openai-codex", CapabilityRank: 900, SchemaReliability: "high", BlockerDetectionPrior: "high", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{Provider: "codex-cli", CapabilityRank: 840, SchemaReliability: "high", BlockerDetectionPrior: "medium", LatencyClass: "cli", RecommendedTimeout: reviewCLICrossSoftTimeout},
	{Provider: "anthropic-claude-cli", CapabilityRank: 760, SchemaReliability: "high", BlockerDetectionPrior: "medium", LatencyClass: "cli", RecommendedTimeout: reviewCLICrossSoftTimeout},
	{Provider: "anthropic", CapabilityRank: 760, SchemaReliability: "standard", BlockerDetectionPrior: "medium", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{Provider: "deepseek", CapabilityRank: 730, SchemaReliability: "standard", BlockerDetectionPrior: "medium", LatencyClass: "slow", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{Provider: "openai", CapabilityRank: 720, SchemaReliability: "standard", BlockerDetectionPrior: "medium", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{Provider: "openrouter", CapabilityRank: 650, SchemaReliability: "standard", BlockerDetectionPrior: "medium", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{Provider: "opencode", CapabilityRank: 650, SchemaReliability: "standard", BlockerDetectionPrior: "medium", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{Provider: "opencode-go", CapabilityRank: 650, SchemaReliability: "standard", BlockerDetectionPrior: "medium", LatencyClass: "standard", RecommendedTimeout: reviewCloudCrossSoftTimeout},
	{Provider: "ollama", CapabilityRank: 520, SchemaReliability: "low", BlockerDetectionPrior: "low", LatencyClass: "variable", RecommendedTimeout: reviewLocalCrossSoftTimeout},
	{Provider: "lmstudio", CapabilityRank: 520, SchemaReliability: "low", BlockerDetectionPrior: "low", LatencyClass: "variable", RecommendedTimeout: reviewLocalCrossSoftTimeout},
	{Provider: "vllm", CapabilityRank: 520, SchemaReliability: "low", BlockerDetectionPrior: "low", LatencyClass: "variable", RecommendedTimeout: reviewLocalCrossSoftTimeout},
	{Provider: "llama.cpp", CapabilityRank: 520, SchemaReliability: "low", BlockerDetectionPrior: "low", LatencyClass: "variable", RecommendedTimeout: reviewLocalCrossSoftTimeout},
}

func reviewProviderBehavior(provider string) ReviewProviderBehavior {
	normalized := normalizeProviderName(provider)
	behavior := ReviewProviderBehavior{
		Provider:               normalized,
		DisplayLabel:           reviewProviderDisplayLabel(normalized),
		DefaultReviewEffort:    "",
		MaxReviewTokens:        0,
		RetryReviewTokens:      4096,
		OmissionRetryBudget:    1,
		SchemaStrictness:       "standard",
		ToolCallRecoveryPolicy: "standard",
		PatchFormatRisk:        "medium",
		RecommendedRoles:       []string{"primary_reviewer", "cross_reviewer"},
	}
	switch normalized {
	case "openai-codex":
		behavior.DefaultReviewEffort = minimumReviewRoleReasoningEffort
		behavior.MaxReviewTokens = 12000
		behavior.RetryReviewTokens = 6000
		behavior.OmissionRetryBudget = 2
		behavior.SchemaStrictness = "strict"
		behavior.ToolCallRecoveryPolicy = "structured"
		behavior.PatchFormatRisk = "low"
		behavior.RecommendedRoles = []string{"primary_reviewer", "cross_reviewer"}
	case "codex-cli":
		behavior.DefaultReviewEffort = minimumReviewRoleReasoningEffort
		behavior.MaxReviewTokens = 6000
		behavior.RetryReviewTokens = 3000
		behavior.OmissionRetryBudget = 1
		behavior.SchemaStrictness = "strict"
		behavior.ToolCallRecoveryPolicy = "cli_retry"
		behavior.PatchFormatRisk = "medium"
	case "anthropic-claude-cli":
		behavior.DefaultReviewEffort = minimumReviewRoleReasoningEffort
		behavior.MaxReviewTokens = 5000
		behavior.RetryReviewTokens = 3000
		behavior.SchemaStrictness = "strict"
		behavior.ToolCallRecoveryPolicy = "compact_json_recovery"
		behavior.PatchFormatRisk = "medium"
	case "deepseek":
		behavior.DefaultReviewEffort = minimumReviewRoleReasoningEffort
		behavior.MaxReviewTokens = 4096
		behavior.RetryReviewTokens = 2500
		behavior.OmissionRetryBudget = 1
		behavior.SchemaStrictness = "strict"
		behavior.ToolCallRecoveryPolicy = "bounded_retry"
		behavior.PatchFormatRisk = "high"
	case "ollama", "lmstudio", "vllm", "llama.cpp":
		behavior.MaxReviewTokens = 6000
		behavior.RetryReviewTokens = 5000
		behavior.OmissionRetryBudget = 1
		behavior.SchemaStrictness = "degraded"
		behavior.ToolCallRecoveryPolicy = "minimal_retry"
		behavior.PatchFormatRisk = "high"
		behavior.UnavailableCapabilityNotes = []string{"Local or OpenAI-compatible providers may not reliably preserve structured review fields."}
	case "openrouter", "opencode", "opencode-go":
		behavior.MaxReviewTokens = 5000
		behavior.RetryReviewTokens = 3000
		behavior.SchemaStrictness = "standard"
		behavior.ToolCallRecoveryPolicy = "bounded_retry"
		behavior.PatchFormatRisk = "medium"
	}
	if behavior.DisplayLabel == "" {
		behavior.DisplayLabel = strings.TrimSpace(provider)
	}
	return behavior
}

func defaultReviewReasoningEffortForProvider(provider string) string {
	effort := normalizeReasoningEffort(reviewProviderBehavior(provider).DefaultReviewEffort)
	if effort != "" {
		return reasoningEffortAtLeast(effort, minimumReviewRoleReasoningEffort)
	}
	if providerSupportsReasoningEffort(provider) {
		return minimumReviewRoleReasoningEffort
	}
	return ""
}

func reviewReasoningEffortOrDefaultForProvider(provider string, effort string) (string, bool) {
	normalized := normalizeReasoningEffort(effort)
	if normalized != "" {
		if defaultReviewReasoningEffortForProvider(provider) != "" {
			return reasoningEffortAtLeast(normalized, minimumReviewRoleReasoningEffort), false
		}
		return normalized, false
	}
	defaultEffort := defaultReviewReasoningEffortForProvider(provider)
	if defaultEffort == "" {
		return "", false
	}
	return defaultEffort, true
}

func reviewProviderDisplayLabel(provider string) string {
	switch normalizeProviderName(provider) {
	case "openai-codex":
		return "openai-codex-subscription"
	case "codex-cli":
		return "openai-codex-cli"
	case "openai":
		return "openai-api"
	case "anthropic-claude-cli":
		return "anthropic-claude-cli"
	case "anthropic":
		return "anthropic-api"
	case "deepseek":
		return "DeepSeek"
	case "openrouter":
		return "openrouter"
	case "opencode":
		return "OpenCode Zen"
	case "opencode-go":
		return "OpenCode Go"
	case "ollama":
		return "ollama"
	case "lmstudio":
		return "LM Studio"
	case "vllm":
		return "vLLM"
	case "llama.cpp":
		return "llama.cpp"
	default:
		return ""
	}
}

func reviewProviderTokenLimit(configured int, behaviorLimit int) int {
	if configured <= 0 {
		return behaviorLimit
	}
	if behaviorLimit > 0 && configured > behaviorLimit {
		return behaviorLimit
	}
	return configured
}

func reviewModelCapabilityProfile(role string, provider string, model string, effort string) ReviewModelCapability {
	provider = normalizeProviderName(provider)
	behavior := reviewProviderBehavior(provider)
	rule, _ := reviewModelCapabilityRuleFor(provider, model)
	rank := reviewModelCapabilityRank(provider, model, effort)
	latency := valueOrDefault(rule.LatencyClass, "standard")
	switch provider {
	case "codex-cli", "anthropic-claude-cli":
		latency = "cli"
	case "ollama", "lmstudio", "vllm", "llama.cpp":
		latency = "variable"
	}
	timeout := reviewDefaultCrossSoftTimeoutForProvider(provider)
	if timeout <= 0 {
		timeout = reviewCloudCrossSoftTimeout
	}
	reliability := "standard"
	if strings.TrimSpace(rule.SchemaReliability) != "" {
		reliability = rule.SchemaReliability
	} else {
		switch behavior.SchemaStrictness {
		case "strict":
			reliability = "high"
		case "degraded":
			reliability = "low"
		}
	}
	prior := valueOrDefault(rule.BlockerDetectionPrior, "medium")
	if rule.BlockerDetectionPrior == "" {
		if rank >= 900 {
			prior = "high"
		} else if rank > 0 && rank < 650 {
			prior = "low"
		}
	}
	return ReviewModelCapability{
		Role:                    normalizeReviewRole(role),
		Provider:                provider,
		ModelPattern:            strings.TrimSpace(model),
		CapabilityRank:          rank,
		SchemaReliability:       reliability,
		BlockerDetectionPrior:   prior,
		LatencyClass:            latency,
		SupportsReasoningEffort: providerSupportsReasoningEffort(provider),
		RecommendedTimeoutMS:    timeout.Milliseconds(),
		RetryBudget:             behavior.OmissionRetryBudget,
	}
}

func reviewModelCapabilityRuleFor(provider string, model string) (reviewModelCapabilityRule, bool) {
	provider = normalizeProviderName(provider)
	modelLower := strings.ToLower(strings.TrimSpace(model))
	for _, rule := range reviewModelCapabilityRules {
		if strings.TrimSpace(rule.ModelPattern) == "" {
			continue
		}
		if strings.Contains(modelLower, strings.ToLower(strings.TrimSpace(rule.ModelPattern))) {
			return rule, true
		}
	}
	for _, rule := range reviewModelCapabilityRules {
		if strings.TrimSpace(rule.Provider) == "" {
			continue
		}
		if strings.EqualFold(provider, normalizeProviderName(rule.Provider)) {
			return rule, true
		}
	}
	return reviewModelCapabilityRule{}, false
}

func reviewRouteHealthForRun(rt *runtimeState, run *ReviewRun) []ReviewRouteHealth {
	latest := reviewRouteHealthFromRun(run)
	if rt == nil || rt.session == nil {
		return latest
	}
	return mergeReviewRouteHealthHistory(rt.session.ReviewRouteHealth, latest, 8)
}

func reviewRouteHealthFromRun(run *ReviewRun) []ReviewRouteHealth {
	if run == nil {
		return nil
	}
	var out []ReviewRouteHealth
	for _, reviewerRun := range run.ReviewerRuns {
		role := normalizeReviewRole(reviewerRun.Role)
		if role == "" {
			role = "primary_reviewer"
		}
		health := ReviewRouteHealth{
			Role:        role,
			Model:       reviewerRun.Model,
			RecentRuns:  1,
			LastStatus:  reviewerRun.Status,
			LastQuality: reviewerRun.ModelQuality,
		}
		if !reviewerRun.StartedAt.IsZero() && !reviewerRun.FinishedAt.IsZero() {
			health.MedianLatencyMS = reviewerRun.FinishedAt.Sub(reviewerRun.StartedAt).Milliseconds()
		}
		lowerError := strings.ToLower(strings.TrimSpace(reviewerRun.Error))
		if strings.Contains(lowerError, "timeout") {
			health.LastTimeout = true
			health.TimeoutRate = 1
			health.Recommendation = "route timed out recently; consider a stronger or closer reviewer route"
		}
		if strings.Contains(lowerError, "empty response") {
			health.EmptyResponseRate = 1
			health.Recommendation = "route returned empty output recently; retry with a different reviewer"
		}
		if strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityWeak) {
			health.WeakRate = 1
			health.Recommendation = "route produced weak structured output recently; avoid strict retry loops"
		}
		if strings.EqualFold(strings.TrimSpace(reviewerRun.Status), "completed") &&
			(strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityUsable) ||
				strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityStrong)) {
			health.UsableFindingRate = 1
			if health.Recommendation == "" {
				health.Recommendation = "route is usable based on the latest review run"
			}
		}
		if health.Recommendation == "" {
			health.Recommendation = "insufficient recent route history"
		}
		out = append(out, health)
	}
	return out
}

func mergeReviewRouteHealthHistory(existing []ReviewRouteHealth, latest []ReviewRouteHealth, limit int) []ReviewRouteHealth {
	if limit <= 0 {
		limit = 8
	}
	merged := map[string]ReviewRouteHealth{}
	order := []string{}
	for _, item := range existing {
		key := reviewRouteHealthKey(item)
		if key == "" {
			continue
		}
		if _, ok := merged[key]; !ok {
			order = append(order, key)
		}
		merged[key] = item
	}
	for _, item := range latest {
		key := reviewRouteHealthKey(item)
		if key == "" {
			continue
		}
		if previous, ok := merged[key]; ok {
			item = combineReviewRouteHealth(previous, item, limit)
		} else {
			order = append(order, key)
			item.RecentRuns = maxInt(item.RecentRuns, 1)
		}
		item.Recommendation = reviewRouteHealthRecommendation(item)
		merged[key] = item
	}
	out := make([]ReviewRouteHealth, 0, len(merged))
	for _, key := range order {
		if item, ok := merged[key]; ok {
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].Role != out[j].Role {
			return out[i].Role < out[j].Role
		}
		return out[i].Model < out[j].Model
	})
	return out
}

func reviewRouteHealthKey(item ReviewRouteHealth) string {
	role := normalizeReviewRole(item.Role)
	model := strings.ToLower(strings.TrimSpace(item.Model))
	if role == "" && model == "" {
		return ""
	}
	return role + "|" + model
}

func combineReviewRouteHealth(previous ReviewRouteHealth, latest ReviewRouteHealth, limit int) ReviewRouteHealth {
	oldRuns := previous.RecentRuns
	if oldRuns <= 0 {
		oldRuns = 1
	}
	newRuns := oldRuns + 1
	if newRuns > limit {
		newRuns = limit
		oldRuns = limit - 1
	}
	weighted := func(old float64, now float64) float64 {
		return ((old * float64(oldRuns)) + now) / float64(newRuns)
	}
	latency := latest.MedianLatencyMS
	if latency == 0 {
		latency = previous.MedianLatencyMS
	} else if previous.MedianLatencyMS > 0 {
		latency = (previous.MedianLatencyMS*int64(oldRuns) + latest.MedianLatencyMS) / int64(newRuns)
	}
	return ReviewRouteHealth{
		Role:              firstNonBlankString(latest.Role, previous.Role),
		Model:             firstNonBlankString(latest.Model, previous.Model),
		RecentRuns:        newRuns,
		TimeoutRate:       weighted(previous.TimeoutRate, latest.TimeoutRate),
		EmptyResponseRate: weighted(previous.EmptyResponseRate, latest.EmptyResponseRate),
		WeakRate:          weighted(previous.WeakRate, latest.WeakRate),
		UsableFindingRate: weighted(previous.UsableFindingRate, latest.UsableFindingRate),
		LastStatus:        firstNonBlankString(latest.LastStatus, previous.LastStatus),
		LastQuality:       firstNonBlankString(latest.LastQuality, previous.LastQuality),
		LastTimeout:       latest.LastTimeout,
		MedianLatencyMS:   latency,
	}
}

func reviewRouteHealthRecommendation(item ReviewRouteHealth) string {
	if item.RecentRuns >= 2 {
		if item.TimeoutRate >= 0.50 {
			return "route is timeout-heavy; reduce strict retries and consider a closer or stronger reviewer"
		}
		if item.EmptyResponseRate >= 0.50 {
			return "route often returns empty output; avoid repeating the same reviewer route"
		}
		if item.WeakRate >= 0.50 {
			return "route often returns weak structured output; avoid strict retry loops"
		}
	}
	if item.UsableFindingRate >= 0.50 {
		return "route is usable across recent runs"
	}
	return valueOrDefault(item.Recommendation, "insufficient recent route history")
}
