package main

import "testing"

// Single-model pre-write review (no cross reviewer) runs the self-review as an
// advisory fallback when a diff preview is available, so a weak/truncated
// self-review never hard-blocks the write. A configured cross reviewer keeps the
// hard gate, and no diff preview means no place to vet -> hard gate stays.
func TestPreWriteUsesMainOnlyReviewerFallback(t *testing.T) {
	// single-model (no cross) + diff preview -> advisory fallback
	if !preWriteUsesMainOnlyReviewerFallback(nil, false, true) {
		t.Fatalf("single-model with a diff preview must use the main-only advisory fallback")
	}
	// configured cross reviewer, not user-approved -> keep the hard gate
	if preWriteUsesMainOnlyReviewerFallback(nil, true, true) {
		t.Fatalf("a configured cross reviewer must keep the hard reviewer gate")
	}
	// no diff preview -> no fallback even single-model (nothing to vet)
	if preWriteUsesMainOnlyReviewerFallback(nil, false, false) {
		t.Fatalf("without a diff preview the fallback must not engage")
	}
}

// Reasoning models routed through OpenRouter/OpenCode need enough review tokens
// that the structured output is not truncated (which would be misread as a weak
// model). The earlier 5000 cap caused that false verdict on GLM 5.2.
func TestOpenRouterReviewTokensAccommodateReasoning(t *testing.T) {
	for _, provider := range []string{"openrouter", "opencode", "opencode-go"} {
		b := reviewProviderBehavior(provider)
		if b.MaxReviewTokens < 12000 {
			t.Fatalf("%s review max tokens must accommodate reasoning models, got %d", provider, b.MaxReviewTokens)
		}
	}
}
