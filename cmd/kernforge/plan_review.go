package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const planReviewMaxRounds = 3

// planReviewSystemPromptPlanner returns the system prompt for the planning model.
func planReviewSystemPromptPlanner(workspaceRoot string, memoryContext string) string {
	var b strings.Builder
	b.WriteString("You are a senior software architect. Your job is to create a detailed, step-by-step implementation plan for the user's request.\n")
	b.WriteString("The plan should cover:\n")
	b.WriteString("- Which files to create or modify\n")
	b.WriteString("- What changes to make in each file (be specific about functions, structs, logic)\n")
	b.WriteString("- The order of operations\n")
	b.WriteString("- Edge cases and potential pitfalls\n")
	b.WriteString("- Testing strategy if applicable\n\n")
	b.WriteString("Output the plan in a clear, numbered format. Be precise and actionable.\n")
	fmt.Fprintf(&b, "Workspace root: %s\n", workspaceRoot)
	if strings.TrimSpace(memoryContext) != "" {
		b.WriteString("\nLoaded memory context:\n")
		b.WriteString(memoryContext)
		b.WriteString("\n")
	}
	return b.String()
}

func planReviewSystemPromptReviewer() string {
	var b strings.Builder
	b.WriteString("You are a meticulous code review expert. Your job is to review an implementation plan and provide constructive feedback.\n")
	b.WriteString("Evaluate the plan for:\n")
	b.WriteString("- Completeness: does it cover all aspects of the requirement?\n")
	b.WriteString("- Correctness: are the proposed changes technically sound?\n")
	b.WriteString("- Order of operations: is the sequence logical?\n")
	b.WriteString("- Edge cases: are potential issues addressed?\n")
	b.WriteString("- Risks: any potential breaking changes or regressions?\n\n")
	b.WriteString("If the plan is good enough to proceed, start your response with exactly: APPROVED\n")
	b.WriteString("If changes are needed, start with: NEEDS_REVISION\n")
	b.WriteString("Then provide specific, actionable feedback.\n")
	return b.String()
}

func planReviewSystemPromptRevise() string {
	return "You received feedback on your implementation plan. Revise the plan to address the reviewer's feedback. Output the complete revised plan in the same numbered format. Be precise and actionable."
}

// PlanReviewResult holds the outcome of the plan-review loop.
type PlanReviewResult struct {
	FinalPlan string
	Rounds    int
	Approved  bool
	ReviewLog []PlanReviewRound
}

// PlanReviewRound captures one iteration of plan + review.
type PlanReviewRound struct {
	Plan   string
	Review string
}

type ModelRequestPolicy struct {
	MaxRetries       int
	RetryDelay       time.Duration
	Timeout          time.Duration
	ModelRoutes      *ModelRouteScheduler
	ModelRouteConfig Config
	ModelRoutePolicy ModelRoutePolicy
}

func modelRequestPolicyFromConfig(cfg Config) ModelRequestPolicy {
	return ModelRequestPolicy{
		MaxRetries:       configMaxRequestRetries(cfg),
		RetryDelay:       configRequestRetryDelay(cfg),
		Timeout:          configRequestTimeout(cfg),
		ModelRoutes:      defaultModelRouteScheduler(),
		ModelRouteConfig: cfg,
		ModelRoutePolicy: modelRoutePolicyFromConfig(cfg),
	}
}

// RunPlanReview orchestrates the iterative plan-review loop between two models.
// plannerClient is the model that creates/revises the plan.
// reviewerClient is the model that reviews the plan.
func RunPlanReview(
	ctx context.Context,
	plannerClient ProviderClient,
	plannerModel string,
	reviewerClient ProviderClient,
	reviewerModel string,
	userPrompt string,
	workspaceRoot string,
	memoryContext string,
	maxTokens int,
	temperature float64,
	onStatus func(string),
) (PlanReviewResult, error) {
	return RunPlanReviewWithPolicy(ctx, plannerClient, plannerModel, reviewerClient, reviewerModel, userPrompt, workspaceRoot, memoryContext, maxTokens, temperature, onStatus, ModelRequestPolicy{})
}

func RunPlanReviewWithPolicy(
	ctx context.Context,
	plannerClient ProviderClient,
	plannerModel string,
	reviewerClient ProviderClient,
	reviewerModel string,
	userPrompt string,
	workspaceRoot string,
	memoryContext string,
	maxTokens int,
	temperature float64,
	onStatus func(string),
	policy ModelRequestPolicy,
) (PlanReviewResult, error) {
	result := PlanReviewResult{}

	// Round 1: generate initial plan
	if onStatus != nil {
		onStatus("Generating initial plan...")
	}

	plannerMessages := []Message{
		{Role: "user", Text: userPrompt},
	}
	planResp, err := completePlanReviewRequest(ctx, plannerClient, ChatRequest{
		Model:       plannerModel,
		System:      planReviewSystemPromptPlanner(workspaceRoot, memoryContext),
		Messages:    plannerMessages,
		MaxTokens:   maxTokens,
		Temperature: temperature,
	}, policy, onStatus, "planner")
	if err != nil {
		return result, fmt.Errorf("planner failed to generate initial plan: %w", err)
	}
	currentPlan := strings.TrimSpace(planResp.Message.Text)
	plannerMessages = append(plannerMessages, planResp.Message)

	for round := 0; round < planReviewMaxRounds; round++ {
		if onStatus != nil {
			onStatus(fmt.Sprintf("Review round %d/%d...", round+1, planReviewMaxRounds))
		}

		// Send plan to reviewer
		reviewMessages := []Message{
			{Role: "user", Text: fmt.Sprintf("Please review the following implementation plan:\n\n%s", currentPlan)},
		}
		reviewResp, err := completePlanReviewRequest(ctx, reviewerClient, ChatRequest{
			Model:       reviewerModel,
			System:      planReviewSystemPromptReviewer(),
			Messages:    reviewMessages,
			MaxTokens:   maxTokens,
			Temperature: temperature,
		}, policy, onStatus, "reviewer")
		if err != nil {
			return result, fmt.Errorf("reviewer failed at round %d: %w", round+1, err)
		}
		reviewText := strings.TrimSpace(reviewResp.Message.Text)

		result.ReviewLog = append(result.ReviewLog, PlanReviewRound{
			Plan:   currentPlan,
			Review: reviewText,
		})
		result.Rounds = round + 1

		// Check if approved
		if strings.HasPrefix(strings.ToUpper(reviewText), "APPROVED") {
			result.FinalPlan = currentPlan
			result.Approved = true
			return result, nil
		}

		// Not the last round - revise the plan
		if round < planReviewMaxRounds-1 {
			if onStatus != nil {
				onStatus(fmt.Sprintf("Revising plan based on feedback (round %d)...", round+1))
			}

			revisionPrompt := fmt.Sprintf("The reviewer provided the following feedback on your plan:\n\n%s\n\nPlease revise your plan to address this feedback.", reviewText)
			plannerMessages = append(plannerMessages, Message{Role: "user", Text: revisionPrompt})

			reviseResp, err := completePlanReviewRequest(ctx, plannerClient, ChatRequest{
				Model:       plannerModel,
				System:      planReviewSystemPromptPlanner(workspaceRoot, memoryContext) + "\n\n" + planReviewSystemPromptRevise(),
				Messages:    plannerMessages,
				MaxTokens:   maxTokens,
				Temperature: temperature,
			}, policy, onStatus, "planner-revision")
			if err != nil {
				return result, fmt.Errorf("planner failed to revise plan at round %d: %w", round+1, err)
			}
			currentPlan = strings.TrimSpace(reviseResp.Message.Text)
			plannerMessages = append(plannerMessages, reviseResp.Message)
		}
	}

	// Max rounds reached, return the latest plan
	result.FinalPlan = currentPlan
	result.Approved = false
	return result, nil
}

func completePlanReviewRequest(ctx context.Context, client ProviderClient, req ChatRequest, policy ModelRequestPolicy, onStatus func(string), stage string) (ChatResponse, error) {
	if client == nil {
		return ChatResponse{}, fmt.Errorf("no model provider is configured")
	}
	maxRetries := policy.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	timeout := policy.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	baseDelay := policy.RetryDelay
	if baseDelay <= 0 {
		baseDelay = 1500 * time.Millisecond
	}
	totalAttempts := maxRetries + 1
	for attempt := 0; attempt < totalAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ChatResponse{}, err
		}
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		routePolicy := policy.ModelRoutePolicy
		if !routePolicy.configured {
			routePolicy = modelRoutePolicyFromConfig(policy.ModelRouteConfig)
		}
		resp, err := completeModelTurnOnceWithModelRoutes(attemptCtx, policy.ModelRoutes, routePolicy, policy.ModelRouteConfig, client, req)
		cancel()
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return ChatResponse{}, ctx.Err()
		}
		if !shouldRetryProviderError(err) || attempt == totalAttempts-1 {
			return ChatResponse{}, err
		}
		delay := providerRetryDelay(baseDelay, attempt)
		if onStatus != nil {
			onStatus(modelRetryProgressMessage(err, attempt, totalAttempts, delay))
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ChatResponse{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return ChatResponse{}, fmt.Errorf("%s plan-review request failed after retry", strings.TrimSpace(stage))
}

// createReviewerClient builds a ProviderClient from PlanReviewConfig, falling back to
// the main config's API key for the same provider.
func createReviewerClient(reviewCfg *PlanReviewConfig, mainCfg Config) (ProviderClient, error) {
	apiKey := reviewCfg.APIKey
	if strings.TrimSpace(apiKey) == "" {
		if strings.EqualFold(reviewCfg.Provider, mainCfg.Provider) {
			apiKey = mainCfg.APIKey
		}
	}
	if strings.TrimSpace(apiKey) == "" && mainCfg.ProviderKeys != nil {
		apiKey = mainCfg.ProviderKeys[normalizeProviderName(reviewCfg.Provider)]
	}
	cfg := mainCfg
	cfg.Provider = reviewCfg.Provider
	cfg.Model = reviewCfg.Model
	cfg.BaseURL = reviewCfg.BaseURL
	cfg.APIKey = apiKey
	return NewProviderClient(cfg)
}
