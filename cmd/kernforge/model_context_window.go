package main

import "strings"

// M24: per-model context-window derivation.
//
// MaxTokens and the auto-compaction trigger were previously global constants
// (config.MaxTokens, config.AutoCompactChars) that ignored the selected model's
// real context window. A small model would over-request output tokens and a
// large model would compact far too early. The helpers below derive a
// per-model context window (seeded from known model families and provider
// defaults), then compute a safe output-token budget and a compaction trigger
// as a fraction of that window. When the window is unknown the original global
// defaults remain the fallback so behavior does not regress.

const (
	// charsPerTokenEstimate matches the codebase convention of ~4 chars/token
	// used elsewhere (for example goals_runtime token estimation).
	charsPerTokenEstimate = 4

	// contextWindowSafetyMargin reserves headroom (in tokens) for prompt
	// overhead the char estimate does not capture (role framing, tool schemas,
	// provider-side formatting) so the computed output budget stays safe.
	contextWindowSafetyMargin = 2048

	// minDerivedMaxTokens is the floor for the derived output budget so a nearly
	// full context still leaves the model room to produce a usable reply.
	minDerivedMaxTokens = 512

	// compactionWindowFraction is the fraction of the context window (in tokens)
	// at which auto-compaction triggers. Kept conservative so compaction runs
	// before the window is exhausted by a large turn.
	compactionWindowFractionNum = 7
	compactionWindowFractionDen = 10
)

// modelContextWindow returns the context window (in tokens) for the selected
// provider/model. Resolution order:
//  1. an explicit configured override (configured > 0)
//  2. a known model-family seed matched against the model id
//  3. a provider default
//  4. 0 (unknown) so callers fall back to global defaults
func modelContextWindow(provider, model string, configured int) int {
	if configured > 0 {
		return configured
	}
	id := strings.ToLower(strings.TrimSpace(model))
	// Strip a leading route prefix such as "openai/" or "openrouter/".
	if slash := strings.LastIndex(id, "/"); slash >= 0 && slash+1 < len(id) {
		id = id[slash+1:]
	}
	if window := modelFamilyContextWindow(id); window > 0 {
		return window
	}
	return providerDefaultContextWindow(provider)
}

// modelFamilyContextWindow matches well-known model id prefixes to their
// documented context windows. Returns 0 when no family matches.
func modelFamilyContextWindow(id string) int {
	if id == "" {
		return 0
	}
	switch {
	case strings.HasPrefix(id, "gpt-5"),
		strings.HasPrefix(id, "gpt-4.1"),
		strings.HasPrefix(id, "o1"),
		strings.HasPrefix(id, "o3"),
		strings.HasPrefix(id, "o4"):
		return 200000
	case strings.HasPrefix(id, "gpt-4o"):
		return 128000
	case strings.HasPrefix(id, "claude"):
		// Claude 3.x / 4.x default published window.
		return 200000
	case strings.HasPrefix(id, "deepseek"):
		return 128000
	case strings.HasPrefix(id, "kimi"):
		return 200000
	case strings.Contains(id, "glm"):
		return 128000
	case strings.HasPrefix(id, "qwen"):
		return 128000
	case strings.HasPrefix(id, "llama"):
		return 128000
	case strings.HasPrefix(id, "gemini"):
		return 1000000
	default:
		return 0
	}
}

// providerDefaultContextWindow returns a conservative per-provider default
// context window when the model id is not recognized. Returns 0 when the
// provider is unknown so callers fall back to global defaults.
func providerDefaultContextWindow(provider string) int {
	switch normalizeProviderName(provider) {
	case "openai", "openai-compatible":
		return 128000
	case "anthropic":
		return 200000
	case "deepseek":
		return 128000
	case "openrouter":
		return 128000
	default:
		return 0
	}
}

// effectiveMaxTokens computes the output-token budget for a turn. With a known
// window it returns clamp(configured, min, window - estimatedInputTokens -
// safetyMargin). With an unknown window it returns the configured value
// unchanged so the global default is preserved.
func effectiveMaxTokens(window, configured, estimatedInputTokens int) int {
	if window <= 0 {
		return configured
	}
	available := window - estimatedInputTokens - contextWindowSafetyMargin
	if available < minDerivedMaxTokens {
		available = minDerivedMaxTokens
	}
	result := configured
	if configured <= 0 || configured > available {
		result = available
	}
	if result < minDerivedMaxTokens {
		result = minDerivedMaxTokens
	}
	return result
}

// compactionTriggerChars computes the auto-compaction threshold in characters
// from the context window. With an unknown window it returns the fallback
// (the global AutoCompactChars). The trigger is a fraction of the window
// reserved for prompt content, converted from tokens to characters.
func compactionTriggerChars(window, fallback int) int {
	if window <= 0 {
		return fallback
	}
	triggerTokens := window * compactionWindowFractionNum / compactionWindowFractionDen
	triggerChars := triggerTokens * charsPerTokenEstimate
	if triggerChars <= 0 {
		return fallback
	}
	return triggerChars
}

// estimatedInputTokensFromChars converts an approximate character count to a
// token estimate using the codebase ~4 chars/token convention.
func estimatedInputTokensFromChars(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + charsPerTokenEstimate - 1) / charsPerTokenEstimate
}
