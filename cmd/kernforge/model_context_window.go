package main

import (
	"strings"
	"unicode"
)

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
	// used elsewhere (for example goals_runtime token estimation). It is the
	// ASCII-text default and the fallback divisor when no better signal exists.
	charsPerTokenEstimate = 4

	// asciiBytesPerToken is the approximate UTF-8 BYTES-per-token weight for
	// Latin/ASCII text. ApproxChars() returns a byte count, so this matches the
	// flat ~4 used historically.
	asciiBytesPerToken = 4.0

	// cjkBytesPerToken is the approximate UTF-8 BYTES-per-token weight for
	// CJK/Hangul text. A Han/Hangul rune is 3 bytes in UTF-8 and costs roughly
	// 1.5-2 tokens, so the effective bytes-per-token is far below 4. Using ~1.7
	// tokens/rune over 3 bytes/rune gives about 1.76 bytes/token; we keep 1.7 as
	// a slightly conservative (over-estimating) bytes-per-token weight so a
	// Korean-heavy session is no longer over-counted ~4x and compacted too early.
	cjkBytesPerToken = 1.7

	// minCorrectionRatio / maxCorrectionRatio clamp the per-session learned
	// bytes-per-token correction so a single anomalous usage report (tiny prompt,
	// huge cache hit, provider quirk) cannot drive the estimate to an absurd
	// value. The range spans CJK-dense (~1.2) to ASCII-with-overhead (~6.0).
	minCorrectionRatio = 1.2
	maxCorrectionRatio = 6.0

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
		// 1M-capable Claude models advertise the larger window; everything else
		// keeps the 3.x/4.x default published 200k window.
		if claudeSupportsOneMillionContext(id) {
			return 1000000
		}
		return 200000
	case isOneMillionContextFableModel(id):
		// Fable family served on the 1M API.
		return 1000000
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

// oneMillionContextClaudeIDs lists Claude model ids known to serve the 1M
// context window even without an explicit marker on the id.
var oneMillionContextClaudeIDs = []string{
	"claude-opus-4-8",
	"claude-opus-4-7",
}

// claudeSupportsOneMillionContext reports whether a (already lowercased and
// route-stripped) Claude model id maps to the 1M window. Detection covers both
// an explicit "[1m]" marker carried on the id (for example
// "claude-opus-4-8[1m]") and a small allow-list of known 1M models.
func claudeSupportsOneMillionContext(id string) bool {
	if id == "" {
		return false
	}
	if strings.Contains(id, "[1m]") || strings.Contains(id, "-1m") {
		return true
	}
	for _, known := range oneMillionContextClaudeIDs {
		if strings.HasPrefix(id, known) {
			return true
		}
	}
	return false
}

// isOneMillionContextFableModel reports whether a (lowercased, route-stripped)
// id is a Fable-family model served on the 1M API.
func isOneMillionContextFableModel(id string) bool {
	if id == "" {
		return false
	}
	if !strings.HasPrefix(id, "fable") {
		return false
	}
	return strings.HasPrefix(id, "fable-5") || strings.Contains(id, "[1m]") || strings.Contains(id, "-1m")
}

// runeIsCJK reports whether a rune is a CJK/Hangul code point that a tokenizer
// tends to split into ~1-2 tokens (rather than the ~4 chars/token of Latin
// text). This drives the CJK-aware char/token estimate.
func runeIsCJK(r rune) bool {
	switch {
	case unicode.Is(unicode.Han, r),
		unicode.Is(unicode.Hangul, r),
		unicode.Is(unicode.Hiragana, r),
		unicode.Is(unicode.Katakana, r):
		return true
	default:
		return false
	}
}

// estimateTokensFromText returns a CJK-aware token estimate for raw text. It
// weighs CJK/Hangul runes by their UTF-8 byte cost at cjkBytesPerToken and all
// other runes at asciiBytesPerToken, so a Korean-heavy string is no longer
// over-counted roughly 4x the way a flat bytes/4 estimate would.
func estimateTokensFromText(text string) int {
	if text == "" {
		return 0
	}
	cjkBytes := 0
	otherBytes := 0
	for _, r := range text {
		size := utf8RuneByteLen(r)
		if runeIsCJK(r) {
			cjkBytes += size
		} else {
			otherBytes += size
		}
	}
	tokens := float64(cjkBytes)/cjkBytesPerToken + float64(otherBytes)/asciiBytesPerToken
	if tokens <= 0 {
		return 0
	}
	estimate := int(tokens + 0.999) // round up so we never under-budget
	if estimate < 1 {
		estimate = 1
	}
	return estimate
}

// utf8RuneByteLen returns the UTF-8 byte length of a rune (1-4). It mirrors
// utf8.RuneLen but treats the invalid-rune (-1) case as a single byte so a
// malformed input still contributes a sane, non-negative weight.
func utf8RuneByteLen(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}

// cjkAwareBytesPerToken estimates the effective UTF-8 bytes-per-token divisor
// for a sample of text by blending the ASCII and CJK weights according to how
// many CJK bytes the sample contains. The result is the value to divide a BYTE
// count (such as Session.ApproxChars) by to estimate tokens. When the sample is
// empty it returns the flat ASCII default so behavior matches the historical
// estimate before any text is seen.
func cjkAwareBytesPerToken(sample string) float64 {
	if sample == "" {
		return asciiBytesPerToken
	}
	cjkBytes := 0
	totalBytes := 0
	for _, r := range sample {
		size := utf8RuneByteLen(r)
		totalBytes += size
		if runeIsCJK(r) {
			cjkBytes += size
		}
	}
	if totalBytes <= 0 {
		return asciiBytesPerToken
	}
	cjkFraction := float64(cjkBytes) / float64(totalBytes)
	ratio := cjkFraction*cjkBytesPerToken + (1.0-cjkFraction)*asciiBytesPerToken
	return clampCorrectionRatio(ratio)
}

// clampCorrectionRatio keeps a bytes-per-token ratio inside the sane learned
// range so neither the heuristic nor a noisy usage report can push it out of
// bounds.
func clampCorrectionRatio(ratio float64) float64 {
	if ratio < minCorrectionRatio {
		return minCorrectionRatio
	}
	if ratio > maxCorrectionRatio {
		return maxCorrectionRatio
	}
	return ratio
}

// updatedTokenCorrectionRatio folds a freshly observed usage sample into the
// running per-session bytes-per-token ratio. inputTokens is the real prompt
// token count reported by the provider and promptChars is the BYTE count that
// was estimated for that same prompt (Session.ApproxChars plus system-prompt
// bytes). The observed ratio promptChars/inputTokens is clamped, then blended
// with the prior ratio using an exponential moving average so the estimate
// adapts without lurching on a single turn. When there is no prior ratio
// (prior <= 0) the clamped observation is adopted directly. Invalid samples
// (non-positive tokens or chars) leave the prior unchanged.
func updatedTokenCorrectionRatio(prior float64, inputTokens, promptChars int) float64 {
	if inputTokens <= 0 || promptChars <= 0 {
		return prior
	}
	observed := clampCorrectionRatio(float64(promptChars) / float64(inputTokens))
	if prior <= 0 {
		return observed
	}
	const emaAlpha = 0.3
	blended := prior*(1.0-emaAlpha) + observed*emaAlpha
	return clampCorrectionRatio(blended)
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
// reserved for prompt content, converted from tokens to BYTES using the
// supplied bytes-per-token divisor (CJK-aware / usage-corrected). A
// non-positive divisor falls back to the flat ASCII default so callers can pass
// 0 to keep historical behavior.
func compactionTriggerChars(window, fallback int, bytesPerToken float64) int {
	if window <= 0 {
		return fallback
	}
	if bytesPerToken <= 0 {
		bytesPerToken = charsPerTokenEstimate
	}
	triggerTokens := window * compactionWindowFractionNum / compactionWindowFractionDen
	triggerChars := int(float64(triggerTokens) * bytesPerToken)
	if triggerChars <= 0 {
		return fallback
	}
	return triggerChars
}

// estimatedInputTokensFromChars converts an approximate BYTE count to a token
// estimate using the supplied bytes-per-token divisor (CJK-aware /
// usage-corrected). A non-positive divisor falls back to the flat ASCII
// default so callers can pass 0 to keep historical behavior.
func estimatedInputTokensFromChars(chars int, bytesPerToken float64) int {
	if chars <= 0 {
		return 0
	}
	if bytesPerToken <= 0 {
		bytesPerToken = charsPerTokenEstimate
	}
	tokens := int(float64(chars)/bytesPerToken + 0.999) // round up so we never under-budget
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}
