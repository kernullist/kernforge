package main

import "testing"

func TestShouldPrimeInteractivePlanSkipsLatestWebResearchRequests(t *testing.T) {
	state := &TaskState{
		Goal: "최근 3개월 이내의 Anti-Cheat 관련 최신 기술내용을 검색해줘",
	}

	if shouldPrimeInteractivePlan(state, true, false, false) {
		t.Fatalf("expected latest web research request to skip interactive preflight planning")
	}
}

func TestShouldPrimeInteractivePlanKeepsNormalCodingTasks(t *testing.T) {
	state := &TaskState{
		Goal: "Fix the duplicated provider retry logic and verify the result",
	}

	if !shouldPrimeInteractivePlan(state, false, true, false) {
		t.Fatalf("expected normal coding task to keep interactive preflight planning")
	}
}
