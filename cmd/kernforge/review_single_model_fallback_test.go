package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

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

func singleModelConsentTestRuntime(cfg Config, input string) (*runtimeState, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return &runtimeState{
		reader:                          bufio.NewReader(strings.NewReader(input)),
		writer:                          out,
		ui:                              UI{},
		cfg:                             cfg,
		interactive:                     true,
		modelReviewConsentPromptEnabled: true,
		agent:                           &Agent{Config: cfg},
	}, out
}

// On a single-model route (no independent reviewer configured anywhere) the
// implicit model-review consent prompt must not appear: the review is skipped
// automatically with skipped_single_model_route, even when a stale session
// auto-approval is still set. This is the agreed single-model policy: the
// self-review adds no independent signal and the diff preview is the gate.
func TestImplicitModelReviewSingleModelRouteAutoSkips(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.Review.ModelReviewConsent = modelReviewConsentAsk
	rt, out := singleModelConsentTestRuntime(cfg, "y\n")
	rt.alwaysApproveModelReview = true

	decision := rt.confirmImplicitModelReview(ModelReviewConsentRequest{Trigger: "pre-write"})
	if decision.Allowed {
		t.Fatalf("single-model implicit review must be skipped, got %#v", decision)
	}
	if decision.SkipReason != modelReviewSkipSingleModelRoute || decision.ConsentSource != "single_model_route" {
		t.Fatalf("expected single-model route skip, got %#v", decision)
	}
	if strings.Contains(out.String(), modelReviewQuestionEnglish) {
		t.Fatalf("single-model route must not render the consent prompt, got %q", out.String())
	}
}

// The single-model auto-skip must not swallow the explicit opt-ins: the
// "always" consent policy still runs the implicit self-review, a configured
// independent cross route still prompts, and the analysis-reviewer trigger
// (which has its own dedicated route) still reaches the prompt.
func TestImplicitModelReviewSingleModelRouteRespectsOptIns(t *testing.T) {
	base := DefaultConfig(t.TempDir())
	base.Provider = "scripted"
	base.Model = "main-model"

	alwaysCfg := base
	alwaysCfg.Review.ModelReviewConsent = modelReviewConsentAlways
	rt, _ := singleModelConsentTestRuntime(alwaysCfg, "")
	if decision := rt.confirmImplicitModelReview(ModelReviewConsentRequest{Trigger: "pre-write"}); !decision.Allowed || decision.ConsentSource != "config_always" {
		t.Fatalf("consent=always must keep running the implicit self-review, got %#v", decision)
	}

	crossCfg := base
	crossCfg.Review.ModelReviewConsent = modelReviewConsentAsk
	crossCfg.Review.RoleModels = map[string]ReviewModelConfig{
		"cross_reviewer": {Provider: "scripted", Model: "cross-model"},
	}
	rt, out := singleModelConsentTestRuntime(crossCfg, "n\n")
	decision := rt.confirmImplicitModelReview(ModelReviewConsentRequest{Trigger: "pre-write"})
	if decision.SkipReason != modelReviewSkipByUser {
		t.Fatalf("a configured cross route must keep the consent prompt, got %#v", decision)
	}
	if !strings.Contains(out.String(), modelReviewQuestionEnglish) {
		t.Fatalf("expected the consent prompt with a cross route configured, got %q", out.String())
	}

	analysisCfg := base
	analysisCfg.Review.ModelReviewConsent = modelReviewConsentAsk
	rt, out = singleModelConsentTestRuntime(analysisCfg, "n\n")
	decision = rt.confirmImplicitModelReview(ModelReviewConsentRequest{Trigger: "analysis reviewer"})
	if decision.SkipReason == modelReviewSkipSingleModelRoute {
		t.Fatalf("analysis reviewer trigger must not be single-model auto-skipped, got %#v", decision)
	}
	if !strings.Contains(out.String(), modelReviewQuestionEnglish) {
		t.Fatalf("expected the consent prompt for the analysis reviewer trigger, got %q", out.String())
	}
}

func mainOnlyFallbackSelfReviewFinding() ReviewFinding {
	return ReviewFinding{
		ID:          "RF-001",
		Source:      "model",
		Severity:    reviewSeverityBlocker,
		Category:    "correctness",
		Confidence:  "high",
		Quality:     reviewFindingQualityComplete,
		Title:       "AddWithValue parameter binding can infer the wrong SQL type",
		Evidence:    "SaveSnapshot passes parameters through command.Parameters.AddWithValue.",
		Impact:      "Type inference can map values to unexpected SQLite storage classes.",
		RequiredFix: "Use Parameters.Add with an explicit SqliteType and assign Value.",
		BlocksGate:  true,
	}
}

// Under the main-only advisory fallback (single-model pre-write), a model
// self-review finding must never hard-block the write: it is downgraded to a
// prominent warning, warning promotion stays off, and the warning-block path
// returns nothing. The identical finding without the policy still blocks, and
// deterministic findings keep blocking even under the policy.
func TestMainOnlyFallbackSelfReviewFindingsAreAdvisory(t *testing.T) {
	run := ReviewRun{
		Trigger:            "pre_write",
		ReviewerGatePolicy: reviewReviewerGatePolicyMainOnlyFallback,
		Findings:           []ReviewFinding{mainOnlyFallbackSelfReviewFinding()},
	}
	normalizeAdvisoryMainOnlyFallbackModelFindings(&run)
	run.Gate = evaluateReviewGate(run)
	if len(run.Gate.BlockingFindings) != 0 {
		t.Fatalf("self-review finding must not block under main_only_fallback, got %#v", run.Gate)
	}
	if run.Gate.Verdict != reviewVerdictApprovedWithWarnings || len(run.Gate.WarningFindings) != 1 {
		t.Fatalf("self-review finding must surface as a warning, got %#v", run.Gate)
	}
	if blockers := preWriteReviewBlockingWarningFindings(run); len(blockers) != 0 {
		t.Fatalf("warning-block path must stay empty under main_only_fallback, got %#v", blockers)
	}

	control := ReviewRun{
		Trigger:  "pre_write",
		Findings: []ReviewFinding{mainOnlyFallbackSelfReviewFinding()},
	}
	control.Gate = evaluateReviewGate(control)
	if control.Gate.Verdict != reviewVerdictNeedsRevision || len(control.Gate.BlockingFindings) != 1 {
		t.Fatalf("the same finding without the policy must still block, got %#v", control.Gate)
	}

	deterministic := ReviewRun{
		Trigger:            "pre_write",
		ReviewerGatePolicy: reviewReviewerGatePolicyMainOnlyFallback,
		Findings: []ReviewFinding{{
			ID:           "RF-DET-001",
			Source:       "deterministic",
			ReviewerRole: "verification",
			Severity:     reviewSeverityBlocker,
			Category:     "correctness",
			Confidence:   "high",
			Quality:      reviewFindingQualityComplete,
			Title:        "verification command failed on the changed file",
			Evidence:     "go build failed after the proposed edit was applied to the staging copy.",
			Impact:       "The change does not compile.",
			RequiredFix:  "Fix the compile error before writing.",
			BlocksGate:   true,
		}},
	}
	deterministic.Gate = evaluateReviewGate(deterministic)
	if deterministic.Gate.Verdict == reviewVerdictApproved || len(deterministic.Gate.BlockingFindings) != 1 {
		t.Fatalf("deterministic findings must keep blocking under main_only_fallback, got %#v", deterministic.Gate)
	}
}
