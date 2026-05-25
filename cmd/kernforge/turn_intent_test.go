package main

import "testing"

func TestClassifyTurnIntentRecognizesReviewOnlyRequest(t *testing.T) {
	if got := classifyTurnIntent("RuntimeManager.cpp 코드 리뷰해줘"); got != TurnIntentReviewCode {
		t.Fatalf("expected review-only request intent, got %q", got)
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
