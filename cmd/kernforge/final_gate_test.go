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

// Regression (2026-07-21, F1): the v2 structured final gate must follow the
// same verification scope policy as the legacy turn path -- an ambient
// (out-of-patch-scope) failure is not unresolved and must not block.
func TestFinalGateVerificationAmbientFailureStaysReady(t *testing.T) {
	envelope := buildRequestEnvelope("main.go 버그를 고쳐줘")
	session := NewSession(t.TempDir(), "provider", "model", "", "default")
	session.LastVerification = &VerificationReport{
		ChangedPaths: []string{"main.go"},
		Steps: []VerificationStep{{
			Label:       "other package tests",
			Command:     "go test ./other/pkg/...",
			Status:      VerificationFailed,
			FailureKind: "compile_error",
			Output:      "other/pkg/x.go:9:2: undefined: Other",
		}},
	}
	result := finalGateVerificationResult(session, envelope, []string{"main.go"})
	if result.Unresolved {
		t.Fatalf("ambient verification failure must not be unresolved, got %#v", result)
	}
	input := FinalGateInput{
		RequestEnvelope: envelope,
		ChangedFiles:    []string{"main.go"},
		Verification:    result,
		Reply:           "수정했습니다. other/pkg 컴파일 에러는 patch scope 밖 환경성 실패라 추가 수리를 진행하지 않았습니다.",
	}
	if decision := DecideFinalGate(input); !decision.Ready {
		t.Fatalf("ambient verification failure must not block the final gate, got %#v", decision)
	}
}

// Regression (2026-07-21, F1): an honestly disclosed patch-scoped verification
// failure resolves the legacy readiness intervention; the v2 gate must accept
// the same disclosure instead of nudging until the turn stalls.
func TestFinalGateVerificationDisclosedFailureStaysReady(t *testing.T) {
	envelope := buildRequestEnvelope("main.go 버그를 고쳐줘")
	session := NewSession(t.TempDir(), "provider", "model", "", "default")
	session.LastVerification = &VerificationReport{
		ChangedPaths: []string{"main.go"},
		Steps: []VerificationStep{{
			Label:       "package tests",
			Command:     "go test ./...",
			Status:      VerificationFailed,
			FailureKind: "compile_error",
			Output:      "main.go:12:2: undefined: Foo",
		}},
	}
	result := finalGateVerificationResult(session, envelope, []string{"main.go"})
	if !result.Unresolved {
		t.Fatalf("patch-scoped verification failure must stay unresolved, got %#v", result)
	}
	input := FinalGateInput{
		RequestEnvelope: envelope,
		ChangedFiles:    []string{"main.go"},
		Verification:    result,
		Reply:           "수정했지만 verification failed: main.go의 컴파일 에러가 아직 남아 있습니다.",
	}
	if decision := DecideFinalGate(input); !decision.Ready {
		t.Fatalf("disclosed patch-scoped failure must not block the final gate, got %#v", decision)
	}
}

// Regression (2026-07-21, F1): a config/environment-only failure is ambient
// even when its output names the changed file -- the legacy ledger treats it
// as a warning, so the v2 gate must not count it as unresolved either.
func TestFinalGateVerificationConfigOnlyFailureStaysReady(t *testing.T) {
	envelope := buildRequestEnvelope("main.go 버그를 고쳐줘")
	session := NewSession(t.TempDir(), "provider", "model", "", "default")
	session.LastVerification = &VerificationReport{
		ChangedPaths: []string{"main.go"},
		Steps: []VerificationStep{{
			Label:       "msbuild",
			Command:     "msbuild app.sln /p:Configuration=Debug",
			Status:      VerificationFailed,
			FailureKind: "build_config",
			Output:      "main.go: build configuration Debug|x64 is not installed",
		}},
	}
	result := finalGateVerificationResult(session, envelope, []string{"main.go"})
	if result.Unresolved {
		t.Fatalf("config-only verification failure must not be unresolved, got %#v", result)
	}
}

// Control: an undisclosed patch-scoped failure must still block.
func TestFinalGateVerificationUndisclosedFailureStillBlocks(t *testing.T) {
	envelope := buildRequestEnvelope("main.go 버그를 고쳐줘")
	session := NewSession(t.TempDir(), "provider", "model", "", "default")
	session.LastVerification = &VerificationReport{
		ChangedPaths: []string{"main.go"},
		Steps: []VerificationStep{{
			Label:       "package tests",
			Command:     "go test ./...",
			Status:      VerificationFailed,
			FailureKind: "compile_error",
			Output:      "main.go:12:2: undefined: Foo",
		}},
	}
	result := finalGateVerificationResult(session, envelope, []string{"main.go"})
	input := FinalGateInput{
		RequestEnvelope: envelope,
		ChangedFiles:    []string{"main.go"},
		Verification:    result,
		Reply:           "수정 완료했습니다.",
	}
	if decision := DecideFinalGate(input); decision.Ready || decision.State != FinalGateNeedsVerification {
		t.Fatalf("undisclosed patch-scoped failure must keep blocking, got %#v", decision)
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
