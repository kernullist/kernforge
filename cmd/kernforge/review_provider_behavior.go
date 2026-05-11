package main

import "strings"

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
		RecommendedRoles:       []string{"primary_reviewer"},
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
		behavior.RecommendedRoles = []string{"primary_reviewer", "security_reviewer", "final_gate_reviewer"}
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
		behavior.OmissionRetryBudget = 2
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
