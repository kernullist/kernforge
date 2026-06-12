package main

import "testing"

func TestImplicitModelReviewPolicySkipsReadOnlyAutoReviewAndGoalTriggers(t *testing.T) {
	root := t.TempDir()
	request := "TavernKernel이 다른 Global Anti-Cheat 대비 부족한 기능들을 정리해서 알려줘."
	cfg := DefaultConfig(root)
	cfg.Review.ModelReviewConsent = modelReviewConsentAlways
	envelope := buildRequestEnvelope(request)
	session := NewSession(root, "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: request})
	session.LastRequestEnvelope = &envelope
	rt := &runtimeState{cfg: cfg, session: session}

	for _, trigger := range []string{"pre_write review", "goal reviewer", "MCP auto review", "final-answer"} {
		decision := rt.confirmImplicitModelReview(ModelReviewConsentRequest{
			Trigger:              trigger,
			OriginalMainProposal: "would otherwise call a reviewer",
		})
		if decision.Allowed || decision.SkipReason != modelReviewSkipReadOnlyBoundary {
			t.Fatalf("trigger %q should skip by read-only boundary, got %#v", trigger, decision)
		}
	}
	if session.ImplicitModelReviewBudget == nil || session.ImplicitModelReviewBudget.SkippedReadOnly != 4 {
		t.Fatalf("expected read-only skip telemetry, got %#v", session.ImplicitModelReviewBudget)
	}
}

func TestImplicitModelReviewPolicyCapsPerTurnReviewerCalls(t *testing.T) {
	root := t.TempDir()
	request := "main.go 버그를 고쳐줘"
	cfg := DefaultConfig(root)
	cfg.Review.ModelReviewConsent = modelReviewConsentAlways
	envelope := buildRequestEnvelope(request)
	session := NewSession(root, "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: request})
	session.LastRequestEnvelope = &envelope
	rt := &runtimeState{cfg: cfg, session: session}

	for i := 0; i < maxImplicitModelReviewsPerTurn; i++ {
		decision := rt.confirmImplicitModelReview(ModelReviewConsentRequest{Trigger: "pre_write review"})
		if !decision.Allowed {
			t.Fatalf("review %d should be allowed before budget cap, got %#v", i, decision)
		}
	}
	decision := rt.confirmImplicitModelReview(ModelReviewConsentRequest{Trigger: "final-answer"})
	if decision.Allowed || decision.SkipReason != modelReviewSkipTurnBudgetExceeded {
		t.Fatalf("expected turn budget skip, got %#v", decision)
	}
	if session.ImplicitModelReviewBudget == nil || session.ImplicitModelReviewBudget.Used != maxImplicitModelReviewsPerTurn || session.ImplicitModelReviewBudget.SkippedBudget != 1 {
		t.Fatalf("expected budget telemetry, got %#v", session.ImplicitModelReviewBudget)
	}
}

func TestProjectAnalysisReviewerRemainsAvailableForExplicitAnalysis(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Review.ModelReviewConsent = modelReviewConsentAlways
	analyzer := &projectAnalyzer{cfg: cfg, workspace: Workspace{BaseRoot: root, Root: root}}

	decision := analyzer.confirmImplicitModelReview("analysis reviewer", "explicit analyze-project shard review")
	if !decision.Allowed {
		t.Fatalf("explicit project analysis reviewer should remain available, got %#v", decision)
	}
}
