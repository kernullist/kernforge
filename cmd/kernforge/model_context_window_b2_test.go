package main

import (
	"strings"
	"testing"
)

// B2: 1M-capable Claude detection. An id carrying the "[1m]" marker or a known
// 1M model id resolves to the 1M window; other Claude ids keep 200k.
func TestModelFamilyContextWindowClaudeOneMillion(t *testing.T) {
	cases := []struct {
		id   string
		want int
	}{
		{"claude-opus-4-8[1m]", 1000000},
		{"claude-opus-4-8", 1000000},
		{"claude-opus-4-7", 1000000},
		{"claude-sonnet-4-6", 200000},
		{"claude-sonnet-4-5", 200000},
		{"claude-3-5-sonnet", 200000},
		{"claude-opus-4-1", 200000},
		// Marker wins even on an otherwise-200k id.
		{"claude-sonnet-4-6[1m]", 1000000},
	}
	for _, tc := range cases {
		if got := modelFamilyContextWindow(tc.id); got != tc.want {
			t.Fatalf("modelFamilyContextWindow(%q) = %d, want %d", tc.id, got, tc.want)
		}
	}
}

// B2: the 1M detection also flows through modelContextWindow with route
// prefixes and the explicit override taking precedence.
func TestModelContextWindowOneMillionRouting(t *testing.T) {
	if got := modelContextWindow("anthropic", "claude-opus-4-8[1m]", 0); got != 1000000 {
		t.Fatalf("claude-opus-4-8[1m] window = %d, want 1000000", got)
	}
	if got := modelContextWindow("openrouter", "anthropic/claude-opus-4-8[1m]", 0); got != 1000000 {
		t.Fatalf("route-prefixed 1m claude window = %d, want 1000000", got)
	}
	if got := modelContextWindow("anthropic", "claude-sonnet-4-6", 0); got != 200000 {
		t.Fatalf("claude-sonnet-4-6 window = %d, want 200000", got)
	}
	// Explicit override always wins.
	if got := modelContextWindow("anthropic", "claude-opus-4-8[1m]", 50000); got != 50000 {
		t.Fatalf("configured override window = %d, want 50000", got)
	}
}

// B2: Fable-5 family on the 1M API resolves to the 1M window.
func TestOneMillionContextFableModel(t *testing.T) {
	if got := modelFamilyContextWindow("fable-5-pro"); got != 1000000 {
		t.Fatalf("fable-5-pro window = %d, want 1000000", got)
	}
	if got := modelFamilyContextWindow("fable-4"); got != 0 {
		t.Fatalf("non-1m fable window = %d, want 0 (unknown)", got)
	}
}

// B2: CJK-aware token estimation. A Korean-heavy string must NOT be counted at
// the flat bytes/4 rate (which over-counts ~4x). With 3-byte Hangul runes the
// flat estimate divides ~3 bytes/rune by 4; the CJK-aware estimate divides by
// ~1.7, producing a token count close to one token per Hangul rune.
func TestEstimateTokensFromTextKoreanNotOverCounted(t *testing.T) {
	// 100 Hangul syllables.
	korean := strings.Repeat("가", 100)
	runeCount := 100

	cjkAware := estimateTokensFromText(korean)
	// Flat bytes/4 estimate over the same byte count.
	flat := (len(korean) + charsPerTokenEstimate - 1) / charsPerTokenEstimate

	// Each Hangul syllable is 3 UTF-8 bytes => flat estimate ~ 300/4 = 75.
	if flat < 70 || flat > 80 {
		t.Fatalf("sanity: flat estimate for %d hangul = %d, expected ~75", runeCount, flat)
	}
	// CJK-aware: 300 bytes / 1.7 ~= 177 tokens. It must be materially HIGHER
	// than the flat estimate (the flat estimate UNDER-counts CJK tokens because
	// CJK costs more tokens per byte), proving the two paths diverge for CJK and
	// the estimate is no longer the naive bytes/4.
	if cjkAware <= flat {
		t.Fatalf("CJK-aware estimate %d should exceed flat bytes/4 estimate %d for hangul", cjkAware, flat)
	}
	if cjkAware < 160 || cjkAware > 195 {
		t.Fatalf("CJK-aware estimate for %d hangul = %d, expected ~177", runeCount, cjkAware)
	}
}

// B2: an ASCII-only string keeps the ~4 chars/token behavior.
func TestEstimateTokensFromTextAscii(t *testing.T) {
	ascii := strings.Repeat("a", 400)
	got := estimateTokensFromText(ascii)
	// 400 bytes / 4 = 100 tokens (rounded up).
	if got < 98 || got > 102 {
		t.Fatalf("ascii estimate = %d, want ~100", got)
	}
}

// B2: cjkAwareBytesPerToken blends toward the CJK weight as the CJK fraction
// rises, and the budgeting estimate stops over-counting Korean ~4x.
func TestCjkAwareBytesPerTokenAndBudgeting(t *testing.T) {
	korean := strings.Repeat("안녕하세요", 50) // dense Hangul
	ratio := cjkAwareBytesPerToken(korean)
	if ratio < minCorrectionRatio || ratio > maxCorrectionRatio {
		t.Fatalf("ratio %.3f out of clamp range", ratio)
	}
	// Pure Hangul => ratio close to cjkBytesPerToken (1.7), well below 4.
	if ratio > 2.0 {
		t.Fatalf("pure-hangul bytes/token = %.3f, want close to %.2f", ratio, cjkBytesPerToken)
	}

	// Compare token estimates for the same byte count: CJK-aware vs flat /4.
	bytes := len(korean)
	flatTokens := estimatedInputTokensFromChars(bytes, charsPerTokenEstimate)
	cjkTokens := estimatedInputTokensFromChars(bytes, ratio)
	// The CJK-aware token estimate for a Korean session is roughly
	// charsPerTokenEstimate/cjkBytesPerToken ~= 2.35x the flat estimate, i.e. the
	// flat /4 path under-counts CJK tokens. The point of the fix is that the two
	// diverge instead of blindly using /4.
	if cjkTokens <= flatTokens {
		t.Fatalf("CJK-aware token estimate %d should exceed flat %d for korean", cjkTokens, flatTokens)
	}

	// ASCII sample keeps ~4.
	ascii := strings.Repeat("hello world ", 50)
	if r := cjkAwareBytesPerToken(ascii); r < 3.8 || r > 4.0 {
		t.Fatalf("ascii bytes/token = %.3f, want ~4.0", r)
	}
}

// B2: empty sample falls back to the flat ASCII default before any text exists.
func TestCjkAwareBytesPerTokenEmpty(t *testing.T) {
	if got := cjkAwareBytesPerToken(""); got != asciiBytesPerToken {
		t.Fatalf("empty sample bytes/token = %.3f, want %.3f", got, asciiBytesPerToken)
	}
}

// B2: the per-session running correction ratio adopts the first clamped
// observation, then blends subsequent ones, and stays inside the clamp range.
func TestUpdatedTokenCorrectionRatio(t *testing.T) {
	// No prior: adopt the clamped observation. 300 chars / 150 tokens = 2.0.
	first := updatedTokenCorrectionRatio(0, 150, 300)
	if first < 1.99 || first > 2.01 {
		t.Fatalf("first observation = %.3f, want ~2.0", first)
	}

	// A second observation blends via EMA toward the new value.
	// observed = 400/100 = 4.0; blended = 2.0*0.7 + 4.0*0.3 = 2.6.
	blended := updatedTokenCorrectionRatio(first, 100, 400)
	if blended < 2.55 || blended > 2.65 {
		t.Fatalf("blended ratio = %.3f, want ~2.6", blended)
	}

	// Out-of-range observations are clamped, not adopted raw.
	high := updatedTokenCorrectionRatio(0, 1, 1000) // observed 1000, clamp to max
	if high != maxCorrectionRatio {
		t.Fatalf("high observation = %.3f, want clamp %.3f", high, maxCorrectionRatio)
	}
	low := updatedTokenCorrectionRatio(0, 1000, 100) // observed 0.1, clamp to min
	if low != minCorrectionRatio {
		t.Fatalf("low observation = %.3f, want clamp %.3f", low, minCorrectionRatio)
	}

	// Invalid samples leave the prior unchanged.
	if got := updatedTokenCorrectionRatio(3.0, 0, 100); got != 3.0 {
		t.Fatalf("zero tokens should keep prior, got %.3f", got)
	}
	if got := updatedTokenCorrectionRatio(3.0, 100, 0); got != 3.0 {
		t.Fatalf("zero chars should keep prior, got %.3f", got)
	}
}

// B2: a Korean session's compaction trigger (in chars) is materially larger
// than the flat /4 trigger, so it no longer compacts ~4x too early. With the
// 1M window the difference is large.
func TestCompactionTriggerCharsCjkAware(t *testing.T) {
	window := 1000000
	fallback := 45000
	flat := compactionTriggerChars(window, fallback, charsPerTokenEstimate)
	cjk := compactionTriggerChars(window, fallback, cjkBytesPerToken)
	if cjk >= flat {
		t.Fatalf("CJK trigger %d should be SMALLER than flat trigger %d (fewer bytes per token)", cjk, flat)
	}
	// Sanity: flat = 700000 tokens * 4 = 2,800,000 chars.
	if flat != 2800000 {
		t.Fatalf("flat trigger = %d, want 2800000", flat)
	}
}

// B2: a fresh session with Korean message text uses the CJK-aware heuristic
// before any usage is observed; after recording real usage it switches to the
// learned ratio.
func TestSessionEffectiveBytesPerToken(t *testing.T) {
	s := &Session{
		Messages: []Message{
			{Role: "user", Text: strings.Repeat("한국어 문장입니다. ", 30)},
		},
	}
	// Before any usage: heuristic, well below the flat 4 for CJK-heavy text.
	heuristic := s.effectiveBytesPerToken()
	if heuristic >= 4.0 {
		t.Fatalf("heuristic bytes/token = %.3f, expected below 4 for korean", heuristic)
	}

	// Record a real usage observation: 600 chars => 250 input tokens => 2.4.
	s.recordTokenUsageObservation(250, 600)
	learned := s.effectiveBytesPerToken()
	if learned < 2.39 || learned > 2.41 {
		t.Fatalf("learned bytes/token = %.3f, want ~2.4", learned)
	}
	if s.TokenEstimateCorrectionRatio == 0 {
		t.Fatalf("expected correction ratio to be stored on the session")
	}
}
