package main

import "testing"

func TestClassifyTurnIntentRecognizesReviewOnlyRequest(t *testing.T) {
	if got := classifyTurnIntent("RuntimeManager.cpp 코드 리뷰해줘"); got != TurnIntentReviewCode {
		t.Fatalf("expected review-only request intent, got %q", got)
	}
}

func TestResolveAgentRequestModeTreatsReviewIntentAsReadOnly(t *testing.T) {
	for _, request := range []string{
		"RuntimeManager.cpp 코드 리뷰해줘",
		"Review RuntimeManager.cpp for bugs",
		"이 코드 검토하고 버그 찾아줘",
		"수정한 코드에 버그는 없는지 검토해",
		"변경사항 리뷰해줘",
		"Review the modified RuntimeManager.cpp for regressions",
	} {
		mode := resolveAgentRequestMode(request, classifyTurnIntent(request))
		if !mode.ReadOnlyAnalysis || mode.ExplicitEditRequest {
			t.Fatalf("review-only request %q should be read-only, got %#v", request, mode)
		}
	}
}

func TestClassifyTurnIntentKeepsReviewOnlySubjectsOutOfEditMode(t *testing.T) {
	for _, request := range []string{
		"수정한 코드에 버그는 없는지 검토해",
		"수정된 부분 코드 리뷰해줘",
		"변경사항 리뷰해줘",
		"Review the modified RuntimeManager.cpp for regressions",
	} {
		if got := classifyTurnIntent(request); got != TurnIntentReviewCode {
			t.Fatalf("expected review-only subject request %q to be review intent, got %q", request, got)
		}
		if !prefersReadOnlyAnalysisIntent(request) {
			t.Fatalf("review-only subject request %q should prefer read-only analysis", request)
		}
	}
}

func TestClassifyTurnIntentKeepsReviewThenFixAsEdit(t *testing.T) {
	for _, request := range []string{
		"검토하고 버그 있으면 수정해",
		"리뷰 후 문제를 수정해줘",
		"Review and fix RuntimeManager.cpp",
		"find bugs and fix RuntimeManager.cpp",
	} {
		if got := classifyTurnIntent(request); got != TurnIntentEditCode {
			t.Fatalf("expected review-then-fix request %q to remain edit intent, got %q", request, got)
		}
		mode := resolveAgentRequestMode(request, classifyTurnIntent(request))
		if mode.ReadOnlyAnalysis || !mode.ExplicitEditRequest {
			t.Fatalf("review-then-fix request %q should be explicit edit mode, got %#v", request, mode)
		}
	}
}

func TestClassifyTurnIntentKeepsReviewReportAuthoringAsEdit(t *testing.T) {
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성해"
	if got := classifyTurnIntent(request); got != TurnIntentEditCode {
		t.Fatalf("expected review report authoring to remain edit intent, got %q", got)
	}
}

func TestClassifyTurnIntentRecognizesGitOperationRequest(t *testing.T) {
	for _, request := range []string{
		"커밋하자",
		"변경사항 commit 해줘",
		"git status 확인해줘",
		"push 하자",
	} {
		if got := classifyTurnIntent(request); got != TurnIntentRunCommand {
			t.Fatalf("expected git operation request %q to be run-command intent, got %q", request, got)
		}
	}
}

func TestClassifyTurnIntentPreservesCurrentTaskSteering(t *testing.T) {
	for _, request := range []string{
		"좋아 너무 작은 기능까지 먼저 확인하지 말고 전체적인 큰 흐름과 관련된 것들 위주로 먼저 확인하자",
		"문서 산출에 관해서만 검토하지 말고 모든 영역을 검토해야 해. 잊지마",
		"좁게만 수정하려고 하지 말고 근본적으로 개선해야 해",
		"Focus on the broader flow first, not tiny feature details.",
		"Do not just review document artifacts; inspect all areas.",
	} {
		if got := classifyTurnIntent(request); got != TurnIntentContinueLastTask {
			t.Fatalf("expected current-task steering request %q to preserve active task, got %q", request, got)
		}
	}
}

func TestClassifyTurnIntentSeparatesFixDirectionFromEditExecution(t *testing.T) {
	for _, request := range []string{
		"이번 테스트 로그를 분석하고 Codex repo와 자세히 비교 분석해서 수정 방향을 잡자",
		"Codex repo와 비교해서 개선 방향을 먼저 정하자",
		"Analyze the log and plan the fix direction before editing.",
	} {
		if got := classifyTurnIntent(request); got != TurnIntentPlanOrDesign {
			t.Fatalf("expected fix-direction request %q to be plan/design intent, got %q", request, got)
		}
		if looksLikeExplicitEditIntent(request) {
			t.Fatalf("fix-direction request %q should not be treated as explicit edit execution", request)
		}
		mode := resolveAgentRequestMode(request, classifyTurnIntent(request))
		if !mode.ReadOnlyAnalysis || mode.ExplicitEditRequest {
			t.Fatalf("fix-direction request %q should be read-only planning, got %#v", request, mode)
		}
	}

	for _, request := range []string{
		"수정 방향을 잡고 바로 적용하자",
		"개선 방향대로 구현해",
		"Plan the fix direction and then implement it.",
	} {
		if !looksLikeExplicitEditIntent(request) {
			t.Fatalf("execution request %q should still be treated as explicit edit", request)
		}
	}
}
