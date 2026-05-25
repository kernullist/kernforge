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
