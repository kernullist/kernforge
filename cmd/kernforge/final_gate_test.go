package main

import "testing"

func TestFinalGateReviewOnlyProducesFindingsFirstFinalWithoutMutation(t *testing.T) {
	envelope := buildRequestEnvelope("RuntimeManager.cpp 코드 리뷰해줘")
	input := FinalGateInput{
		RequestEnvelope: envelope,
		Review: FinalGateReviewResult{
			Present:    true,
			RunID:      "review-read-only",
			Verdict:    reviewVerdictApproved,
			GateAction: reviewGateActionFinalSummary,
		},
		Reply: "Review findings:\n- No blocking findings.\n\nFiles edited: none.",
	}
	decision := DecideFinalGate(input)
	if !decision.Ready || decision.State != FinalGateReady {
		t.Fatalf("review-only final should be ready when no mutation happened, got %#v", decision)
	}
	if envelope.AllowsFileMutation || envelope.ExplicitEditRequest {
		t.Fatalf("review-only envelope must stay read-only, got %#v", envelope)
	}
}

func TestFinalGatePlanOnlyDoesNotPromoteToCodeEdit(t *testing.T) {
	envelope := buildRequestEnvelope("Codex repo와 비교해서 개선 방향을 먼저 정하자")
	input := FinalGateInput{
		RequestEnvelope:   envelope,
		AttemptedEditTool: true,
	}
	decision := DecideFinalGate(input)
	if decision.State != FinalGateNeedsRecovery {
		t.Fatalf("plan-only mutation should require recovery, got %#v", decision)
	}
	if envelope.AllowsFileMutation || envelope.PrimaryClass != RequestClassPlan {
		t.Fatalf("plan-only request should remain read-only planning, got %#v", envelope)
	}
}

func TestFinalGateGoalPromptDraftOnlyDoesNotPromoteToExecution(t *testing.T) {
	envelope := buildRequestEnvelope("goal 프롬프트를 작성해줘")
	input := FinalGateInput{
		RequestEnvelope: envelope,
		ChangedFiles:    []string{".kernforge/goals/latest.md"},
	}
	decision := DecideFinalGate(input)
	if decision.State != FinalGateNeedsRecovery {
		t.Fatalf("draft-only goal prompt mutation should require recovery, got %#v", decision)
	}
	if !envelope.GoalPromptDraftOnly || envelope.AllowsGitMutation || envelope.ExplicitEditRequest {
		t.Fatalf("draft-only goal prompt must not become active execution, got %#v", envelope)
	}
}

func TestFinalGateExplicitEditPreservesEditAndVerificationPath(t *testing.T) {
	envelope := buildRequestEnvelope("main.go 버그를 고쳐줘")
	input := FinalGateInput{
		RequestEnvelope:     envelope,
		ChangedFiles:        []string{"main.go"},
		AttemptedEditTool:   true,
		ExplicitEditRequest: true,
		Verification: FinalGateVerificationResult{
			Present: true,
			Summary: "Verification: passed=1 failed=0 skipped=0",
			Passed:  true,
		},
	}
	decision := DecideFinalGate(input)
	if !decision.Ready || decision.State != FinalGateReady {
		t.Fatalf("explicit edit with applied change and passing verification should be ready, got %#v", decision)
	}
}

func TestFinalGateExplicitEditManualHandoffNeedsRecovery(t *testing.T) {
	envelope := buildRequestEnvelope("main.go 버그를 고쳐줘")
	input := FinalGateInput{
		RequestEnvelope:     envelope,
		ExplicitEditRequest: true,
		Reply:               "도구 사용에 문제가 있어 직접 수정해 주시면 됩니다.",
	}
	decision := DecideFinalGate(input)
	if decision.State != FinalGateNeedsRecovery {
		t.Fatalf("explicit edit manual handoff should require recovery, got %#v", decision)
	}
}

func TestFinalGateVerificationUnresolvedIsNotReady(t *testing.T) {
	envelope := buildRequestEnvelope("main.go 버그를 고쳐줘")
	input := FinalGateInput{
		RequestEnvelope: envelope,
		ChangedFiles:    []string{"main.go"},
		Verification: FinalGateVerificationResult{
			Missing:    true,
			Unresolved: true,
		},
	}
	decision := DecideFinalGate(input)
	if decision.Ready || decision.State != FinalGateNeedsVerification {
		t.Fatalf("unresolved verification must not be ready, got %#v", decision)
	}
}

func TestFinalGateReviewNeedsRevisionIsNotReady(t *testing.T) {
	envelope := buildRequestEnvelope("main.go 버그를 고쳐줘")
	input := FinalGateInput{
		RequestEnvelope: envelope,
		Review: FinalGateReviewResult{
			Present:       true,
			RunID:         "review-needs-repair",
			Verdict:       reviewVerdictNeedsRevision,
			GateAction:    reviewGateActionRepairRequired,
			NeedsRevision: true,
			BlocksFinal:   true,
		},
	}
	decision := DecideFinalGate(input)
	if decision.Ready || decision.State != FinalGateNeedsReview {
		t.Fatalf("unresolved review findings must not be ready, got %#v", decision)
	}
}

func TestFinalGateSingleModelReviewModeRecordsNoCrossReviewReason(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.LastReviewRun = &ReviewRun{
		ID:      "review-single-model",
		Trigger: "post_change",
		Target:  reviewTargetChange,
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
			Action:  reviewGateActionFinalSummary,
		},
		SingleModelPolicy: SingleModelReviewPolicy{
			Enabled:             true,
			IndependenceLevel:   "single_model",
			NoCrossReviewReason: "single_model_mode",
		},
	}
	input := BuildFinalGateInput(root, session, buildRequestEnvelope("main.go를 수정해줘"), nil, "완료했습니다.", TurnRuntimeFinalContext{})
	if input.Review.NoCrossReviewReason != "single_model_mode" {
		t.Fatalf("expected exact no_cross_review reason, got %#v", input.Review)
	}
	decision := DecideFinalGate(input)
	if decision.NoCrossReviewReason != "single_model_mode" {
		t.Fatalf("expected decision to preserve no_cross_review reason, got %#v", decision)
	}
}

func TestFinalGateCommitGateRequiresExplicitRequest(t *testing.T) {
	envelope := buildRequestEnvelope("작업 끝내줘")
	input := FinalGateInput{
		RequestEnvelope: envelope,
		GitMutation: FinalGateGitMutationState{
			MutationAttempted: true,
		},
	}
	decision := DecideFinalGate(input)
	if decision.State != FinalGateNeedsUserConfirmation {
		t.Fatalf("git mutation without explicit request should require user confirmation, got %#v", decision)
	}
}
