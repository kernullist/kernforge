package main

import (
	"context"
	"testing"
	"time"
)

type interactiveReviewerStubClient struct {
	name string
}

func (c interactiveReviewerStubClient) Name() string {
	return c.name
}

func (c interactiveReviewerStubClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, nil
}

func TestShouldPrimeInteractivePlanSkipsLatestWebResearchRequests(t *testing.T) {
	state := &TaskState{
		Goal: "최근 3개월 이내의 Anti-Cheat 관련 최신 기술내용을 검색해줘",
	}

	if shouldPrimeInteractivePlan(state, true, false, false) {
		t.Fatalf("expected latest web research request to skip interactive preflight planning")
	}
}

func TestShouldPrimeInteractivePlanSkipsAnalysisOnlyStructureQuestions(t *testing.T) {
	state := &TaskState{
		Goal: "@TavernKernel/TavernKernel/ 드라이버 프로젝트 전체 구조를 자세히 설명해줘",
	}

	if shouldPrimeInteractivePlan(state, true, false, false) {
		t.Fatalf("expected analysis-only structure question to skip interactive preflight planning")
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

func TestShouldPrimeInteractivePlanSkipsFocusedBugFixSelection(t *testing.T) {
	state := &TaskState{
		Goal: "@Tavern/TavernWorker/TavernUpdManager.cpp:250-322 버그를 찾아서 수정해",
	}

	if shouldPrimeInteractivePlan(state, false, true, false) {
		t.Fatalf("expected focused bug-fix selection to skip slow interactive preflight planning")
	}
}

func TestShouldPrimeInteractivePlanSkipsBroadBugFindAndFix(t *testing.T) {
	state := &TaskState{
		Goal: "TavernWorker 서비스 설치/시작 과정에 버그를 찾고 수정해",
	}

	if shouldPrimeInteractivePlan(state, false, true, false) {
		t.Fatalf("expected broad bug-find-and-fix request to skip slow interactive preflight planning")
	}
}

func TestInteractivePlanReviewPolicyCapsHiddenPreflight(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.RequestTimeoutSecs = 1200
	cfg.MaxRequestRetries = 2
	agent := &Agent{Config: cfg}

	policy := interactivePlanReviewPolicy(agent)
	if policy.Timeout != interactivePlanReviewBudget {
		t.Fatalf("expected hidden preflight timeout cap %v, got %v", interactivePlanReviewBudget, policy.Timeout)
	}
	if policy.MaxRetries != 0 {
		t.Fatalf("expected hidden preflight retries to be disabled, got %d", policy.MaxRetries)
	}
}

func TestInteractivePlanReviewPolicyKeepsShorterUserTimeout(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.RequestTimeoutSecs = 30
	agent := &Agent{Config: cfg}

	policy := interactivePlanReviewPolicy(agent)
	if policy.Timeout != 30*time.Second {
		t.Fatalf("expected shorter user timeout to be preserved, got %v", policy.Timeout)
	}
}

func TestEnsureInteractiveReviewerClientSkipsSingleModelFallback(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-chat"
	agent := &Agent{
		Config: cfg,
		Session: &Session{
			Provider: "deepseek",
			Model:    "deepseek-chat",
		},
	}

	client, model := agent.ensureInteractiveReviewerClient()
	if client != nil || model != "" {
		t.Fatalf("expected no implicit same-model reviewer, got %T %q", client, model)
	}
}

func TestEnsureInteractiveReviewerClientKeepsExplicitReviewer(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-chat"
	agent := &Agent{
		Config:         cfg,
		ReviewerClient: interactiveReviewerStubClient{name: "deepseek"},
		ReviewerModel:  "deepseek-chat",
		Session: &Session{
			Provider: "deepseek",
			Model:    "deepseek-chat",
		},
	}

	client, model := agent.ensureInteractiveReviewerClient()
	if client == nil || model != "deepseek-chat" {
		t.Fatalf("expected explicit reviewer to remain available, got %T %q", client, model)
	}
}

func TestEnsureInteractiveReviewerClientKeepsDistinctReviewer(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-chat"
	agent := &Agent{
		Config:         cfg,
		ReviewerClient: interactiveReviewerStubClient{name: "openai-codex"},
		ReviewerModel:  "gpt-5.5",
		Session: &Session{
			Provider: "deepseek",
			Model:    "deepseek-chat",
		},
	}

	client, model := agent.ensureInteractiveReviewerClient()
	if client == nil || model != "gpt-5.5" {
		t.Fatalf("expected distinct reviewer to remain available, got %T %q", client, model)
	}
}

func TestMaybePrimeInteractivePlanDoesNotEmitProgressWithoutReviewer(t *testing.T) {
	state := &TaskState{
		Goal: "Fix the duplicated provider retry logic and verify the result",
	}
	agent := &Agent{
		Config: DefaultConfig(t.TempDir()),
		Client: interactiveReviewerStubClient{name: "deepseek"},
		Session: &Session{
			Provider:  "deepseek",
			Model:     "deepseek-chat",
			TaskState: state,
		},
		EmitProgress: func(message string) {
			t.Fatalf("expected no plan-review progress without reviewer, got %q", message)
		},
	}

	if err := agent.maybePrimeInteractivePlan(context.Background(), false, true, false); err != nil {
		t.Fatalf("maybePrimeInteractivePlan: %v", err)
	}
}
