package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const backgroundShellJobPendingCheck = "Poll the active background shell job(s) before concluding."
const verificationPendingCheck = "Run verification or a focused build/test after the latest edits."

func (a *Agent) initializeTaskState(userText string) {
	state := a.Session.StartTaskState(userText)
	state.SetProfiles(a.plannerProfileLabel(), a.reviewerProfileLabel())
}

func (a *Agent) plannerProfileLabel() string {
	if a == nil || a.Session == nil {
		return ""
	}
	return strings.TrimSpace(a.Session.Provider) + " / " + strings.TrimSpace(a.Session.Model)
}

func (a *Agent) reviewerProfileLabel() string {
	if a == nil {
		return ""
	}
	if strings.TrimSpace(a.ReviewerModel) != "" && a.ReviewerClient != nil {
		return strings.TrimSpace(a.ReviewerClient.Name()) + " / " + strings.TrimSpace(a.ReviewerModel)
	}
	if strings.TrimSpace(a.AuxReviewerModel) != "" && a.AuxReviewerClient != nil {
		return strings.TrimSpace(a.AuxReviewerClient.Name()) + " / " + strings.TrimSpace(a.AuxReviewerModel)
	}
	return a.plannerProfileLabel()
}

func (a *Agent) reviewerOrDefaultClient() ProviderClient {
	if a != nil && a.ReviewerClient != nil {
		return a.ReviewerClient
	}
	if a != nil && a.AuxReviewerClient != nil {
		return a.AuxReviewerClient
	}
	if a != nil {
		return a.Client
	}
	return nil
}

func (a *Agent) reviewerOrDefaultModel() string {
	if a != nil && strings.TrimSpace(a.ReviewerModel) != "" {
		return strings.TrimSpace(a.ReviewerModel)
	}
	if a != nil && strings.TrimSpace(a.AuxReviewerModel) != "" {
		return strings.TrimSpace(a.AuxReviewerModel)
	}
	if a != nil && a.Session != nil {
		return strings.TrimSpace(a.Session.Model)
	}
	return ""
}

func (a *Agent) maybePrimeInteractivePlan(ctx context.Context, readOnlyAnalysis bool, explicitEditRequest bool, explicitGitRequest bool) error {
	if a == nil || a.Session == nil || a.Session.TaskState == nil {
		return nil
	}
	state := a.Session.TaskState
	if !shouldPrimeInteractivePlan(state, readOnlyAnalysis, explicitEditRequest, explicitGitRequest) {
		return nil
	}
	if a.EmitProgress != nil {
		a.EmitProgress("Building an execution plan before the main tool loop...")
	}
	reviewerClient, reviewerModel := a.ensureInteractiveReviewerClient()
	if a.Client == nil || reviewerClient == nil || strings.TrimSpace(reviewerModel) == "" {
		return nil
	}
	memoryContext := strings.TrimSpace(a.Memory.Combined())
	result, err := RunPlanReview(
		ctx,
		a.Client,
		a.Session.Model,
		reviewerClient,
		reviewerModel,
		buildInteractiveExecutionPlanPrompt(state, readOnlyAnalysis),
		a.Session.WorkingDir,
		memoryContext,
		max(768, a.Config.MaxTokens/2),
		a.Config.Temperature,
		nil,
	)
	if err != nil {
		state.SetReviewerGuidance("planner_error", "Planner/reviewer preflight was unavailable: "+err.Error())
		state.SetNextStep("Inspect the relevant code directly and proceed without the preflight plan.")
		return nil
	}
	state.SetPlanSummary(result.FinalPlan, result.Approved)
	state.SetPhase("execution")
	state.SetProfiles(a.plannerProfileLabel(), a.reviewerProfileLabel())
	items := parsePlanItemsFromText(result.FinalPlan)
	if len(items) > 0 {
		a.Session.SetSharedPlan(items)
		a.Session.ensureSharedPlanInProgress()
	}
	if state.NextStep == "" {
		state.SetNextStep(firstPlanItemText(result.FinalPlan))
	}
	state.Touch()
	_ = a.maybeRunInteractiveMicroWorkers(ctx, "plan_prime")
	return a.Store.Save(a.Session)
}

func shouldPrimeInteractivePlan(state *TaskState, readOnlyAnalysis bool, explicitEditRequest bool, explicitGitRequest bool) bool {
	if state == nil {
		return false
	}
	goal := strings.TrimSpace(state.Goal)
	if goal == "" {
		return false
	}
	if strings.TrimSpace(state.PlanSummary) != "" {
		return false
	}
	if shouldSkipInteractivePlanPreflight(goal, readOnlyAnalysis, explicitEditRequest, explicitGitRequest) {
		return false
	}
	return true
}

func shouldSkipInteractivePlanPreflight(goal string, readOnlyAnalysis bool, explicitEditRequest bool, explicitGitRequest bool) bool {
	_ = readOnlyAnalysis
	_ = explicitEditRequest
	_ = explicitGitRequest
	lowerGoal := strings.ToLower(strings.TrimSpace(goal))
	if shouldPrioritizeWebResearchInSystemPrompt(lowerGoal) {
		return true
	}
	return false
}

func buildInteractiveExecutionPlanPrompt(state *TaskState, readOnlyAnalysis bool) string {
	var b strings.Builder
	b.WriteString("Create a concrete execution plan for this coding task.\n")
	if readOnlyAnalysis {
		b.WriteString("- This is analysis-only. Do not plan any file edits.\n")
	} else {
		b.WriteString("- Assume the agent should inspect, edit, verify, and summarize directly when appropriate.\n")
	}
	b.WriteString("- Keep the plan practical for an interactive terminal coding agent.\n")
	b.WriteString("- Prefer the smallest reliable path to a verified result.\n")
	if state != nil {
		if state.Goal != "" {
			b.WriteString("\nUser goal:\n")
			b.WriteString(state.Goal)
			b.WriteString("\n")
		}
		if state.CurrentHypothesis != "" {
			b.WriteString("\nCurrent hypothesis:\n")
			b.WriteString(state.CurrentHypothesis)
			b.WriteString("\n")
		}
		if len(state.ConfirmedFacts) > 0 {
			b.WriteString("\nConfirmed facts:\n- ")
			b.WriteString(strings.Join(state.ConfirmedFacts, "\n- "))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (a *Agent) buildRecoveryGuidance(ctx context.Context, reason string, fallback string, recent string, detail string) string {
	state := a.Session.EnsureTaskState()
	state.SetPhase("recovery")
	if detail != "" {
		state.AddFailedAttempt(detail)
	}
	guidance := strings.TrimSpace(fallback)
	if reviewerText := strings.TrimSpace(a.requestReviewerGuidance(ctx, reason, recent, detail)); reviewerText != "" {
		state.SetReviewerGuidance(reason, reviewerText)
		if guidance == "" {
			guidance = "Recovery guidance from reviewer:\n" + reviewerText
		} else {
			guidance += "\n\nReviewer guidance:\n" + reviewerText
		}
		if first := firstPlanItemText(reviewerText); first != "" {
			state.SetNextStep(first)
		}
	} else if guidance != "" {
		state.SetReviewerGuidance(reason, guidance)
	}
	if refreshedPlan := strings.TrimSpace(a.maybeRefreshInteractivePlanForRecovery(ctx, reason)); refreshedPlan != "" {
		if guidance == "" {
			guidance = "Refreshed execution plan:\n" + refreshedPlan
		} else {
			guidance += "\n\nRefreshed execution plan:\n" + refreshedPlan
		}
		if first := firstPlanItemText(refreshedPlan); first != "" {
			state.SetNextStep(first)
		}
	}
	return guidance
}

func (a *Agent) requestReviewerGuidance(ctx context.Context, reason string, recent string, detail string) string {
	client, model := a.ensureInteractiveReviewerClient()
	if client == nil || strings.TrimSpace(model) == "" || a.Session == nil || a.Session.TaskState == nil {
		return ""
	}
	resp, err := client.Complete(ctx, ChatRequest{
		Model: model,
		System: strings.Join([]string{
			"You are a recovery reviewer for a coding agent.",
			"Diagnose why the agent is stuck and give the next materially different step.",
			"Keep the answer short, concrete, and tool-aware.",
			"Do not repeat the same failing step.",
		}, "\n"),
		Messages: []Message{{
			Role: "user",
			Text: buildInteractiveRecoveryReviewerPrompt(a.Session.TaskState, reason, recent, detail),
		}},
		MaxTokens:   min(512, max(256, a.Config.MaxTokens/4)),
		Temperature: 0.1,
		WorkingDir:  a.Session.WorkingDir,
	})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(resp.Message.Text)
}

func (a *Agent) ensureInteractiveReviewerClient() (ProviderClient, string) {
	if a == nil {
		return nil, ""
	}
	if a.ReviewerClient != nil && strings.TrimSpace(a.ReviewerModel) != "" {
		return a.ReviewerClient, strings.TrimSpace(a.ReviewerModel)
	}
	if a.AuxReviewerClient != nil && strings.TrimSpace(a.AuxReviewerModel) != "" {
		return a.AuxReviewerClient, strings.TrimSpace(a.AuxReviewerModel)
	}
	if a.Session == nil {
		return nil, ""
	}
	cfg := a.Config
	if strings.TrimSpace(cfg.Provider) == "" {
		cfg.Provider = strings.TrimSpace(a.Session.Provider)
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = strings.TrimSpace(a.Session.Model)
	}
	if strings.TrimSpace(cfg.Provider) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil, ""
	}
	client, err := NewProviderClient(cfg)
	if err != nil {
		return nil, ""
	}
	a.AuxReviewerClient = client
	a.AuxReviewerModel = cfg.Model
	return a.AuxReviewerClient, a.AuxReviewerModel
}

func (a *Agent) maybeRefreshInteractivePlanForRecovery(ctx context.Context, reason string) string {
	if a == nil || a.Session == nil || a.Session.TaskState == nil {
		return ""
	}
	state := a.Session.TaskState
	if state.PlanRefreshCount >= 2 {
		return ""
	}
	if len(state.FailedAttempts) < 2 && !strings.EqualFold(strings.TrimSpace(reason), string(recoveryTriggerToolBudgetExceeded)) {
		return ""
	}
	reviewerClient, reviewerModel := a.ensureInteractiveReviewerClient()
	if a.Client == nil || reviewerClient == nil || strings.TrimSpace(reviewerModel) == "" {
		return ""
	}
	result, err := RunPlanReview(
		ctx,
		a.Client,
		a.Session.Model,
		reviewerClient,
		reviewerModel,
		buildInteractiveReplanPrompt(state, reason),
		a.Session.WorkingDir,
		strings.TrimSpace(a.Memory.Combined()),
		max(768, a.Config.MaxTokens/2),
		a.Config.Temperature,
		nil,
	)
	if err != nil {
		return ""
	}
	plan := strings.TrimSpace(result.FinalPlan)
	if plan == "" {
		return ""
	}
	state.NotePlanRefresh(plan, result.Approved, reason)
	items := parsePlanItemsFromText(plan)
	if len(items) > 0 {
		a.Session.SetSharedPlan(items)
		a.Session.ensureSharedPlanInProgress()
	}
	_ = a.maybeRunInteractiveMicroWorkers(ctx, "replan")
	return plan
}

func buildInteractiveRecoveryReviewerPrompt(state *TaskState, reason string, recent string, detail string) string {
	var b strings.Builder
	b.WriteString("The coding agent is stuck and needs a short recovery plan.\n")
	if state != nil {
		if state.Goal != "" {
			b.WriteString("\nGoal:\n")
			b.WriteString(state.Goal)
			b.WriteString("\n")
		}
		if state.PlanSummary != "" {
			b.WriteString("\nExecution plan summary:\n")
			b.WriteString(compactPromptSection(compactPlanSummary(state.PlanSummary), 500))
			b.WriteString("\n")
		}
		if len(state.CompletedSteps) > 0 {
			b.WriteString("\nCompleted steps:\n- ")
			b.WriteString(strings.Join(state.CompletedSteps, "\n- "))
			b.WriteString("\n")
		}
		if len(state.FailedAttempts) > 0 {
			b.WriteString("\nRecent failed attempts:\n- ")
			b.WriteString(strings.Join(state.FailedAttempts, "\n- "))
			b.WriteString("\n")
		}
	}
	b.WriteString("\nStall reason:\n")
	b.WriteString(reason)
	b.WriteString("\n")
	if detail != "" {
		b.WriteString("\nFailure detail:\n")
		b.WriteString(detail)
		b.WriteString("\n")
	}
	if recent != "" {
		b.WriteString("\nRecent tool turns:\n")
		b.WriteString(recent)
		b.WriteString("\n")
	}
	b.WriteString("\nReturn 3-5 short bullets with:")
	b.WriteString("\n1. the most likely cause of the stall")
	b.WriteString("\n2. the next materially different tool step")
	b.WriteString("\n3. whether to conclude with a partial answer instead of more tool churn")
	return b.String()
}

func buildInteractiveReplanPrompt(state *TaskState, reason string) string {
	var b strings.Builder
	b.WriteString("Refresh the execution plan for a coding task that is currently stalled.\n")
	if state != nil {
		if state.Goal != "" {
			b.WriteString("\nGoal:\n")
			b.WriteString(state.Goal)
			b.WriteString("\n")
		}
		if state.PlanSummary != "" {
			b.WriteString("\nPrevious plan summary:\n")
			b.WriteString(compactPromptSection(compactPlanSummary(state.PlanSummary), 600))
			b.WriteString("\n")
		}
		if len(state.CompletedSteps) > 0 {
			b.WriteString("\nCompleted steps:\n- ")
			b.WriteString(strings.Join(state.CompletedSteps, "\n- "))
			b.WriteString("\n")
		}
		if len(state.FailedAttempts) > 0 {
			b.WriteString("\nFailed attempts:\n- ")
			b.WriteString(strings.Join(state.FailedAttempts, "\n- "))
			b.WriteString("\n")
		}
		if len(state.PendingChecks) > 0 {
			b.WriteString("\nPending checks:\n- ")
			b.WriteString(strings.Join(state.PendingChecks, "\n- "))
			b.WriteString("\n")
		}
	}
	b.WriteString("\nRecovery trigger:\n")
	b.WriteString(reason)
	b.WriteString("\n\nReturn a concise updated numbered plan that avoids the previous dead end.")
	return b.String()
}

func (a *Agent) maybeRunInteractiveMicroWorkers(ctx context.Context, trigger string) error {
	if a == nil || a.Session == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(trigger)), "executor") &&
		(a.Session.TaskState == nil || len(a.Session.TaskState.ExecutorParallelNodes) == 0) {
		return nil
	}
	graph := a.Session.TaskGraph
	if graph == nil || len(graph.Nodes) == 0 {
		return nil
	}
	if !shouldRunInteractiveMicroWorkers(trigger, graph) {
		return nil
	}
	candidates := a.executorAwareMicroWorkerCandidates(3)
	if len(candidates) == 0 {
		return nil
	}
	type microWorkerResult struct {
		nodeID     string
		brief      string
		specialist string
		reason     string
	}
	workerCount := min(3, len(candidates))
	results := make(chan microWorkerResult, workerCount)
	var wg sync.WaitGroup
	updated := false
	for _, node := range candidates[:workerCount] {
		node := node
		status := strings.TrimSpace(strings.ToLower(node.Status))
		if node.MicroWorkerBrief != "" || status == "completed" || status == "failed" || status == "stale" || status == "superseded" {
			continue
		}
		assignment, ok := selectSpecialistForTaskNode(a.Config, node, a.Session.TaskState, trigger, true)
		if !ok {
			continue
		}
		client, model := a.specialistClient(assignment.Profile)
		if client == nil || strings.TrimSpace(model) == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Complete(ctx, ChatRequest{
				Model:  model,
				System: buildSpecialistMicroWorkerSystemPrompt(assignment.Profile),
				Messages: []Message{{
					Role: "user",
					Text: buildSpecialistMicroWorkerPrompt(assignment.Profile, a.Session.TaskState, node, trigger, assignment.Reason),
				}},
				MaxTokens:   min(256, max(128, a.Config.MaxTokens/6)),
				Temperature: 0.1,
				WorkingDir:  a.Session.WorkingDir,
			})
			if err != nil {
				return
			}
			brief := compactPromptSection(strings.TrimSpace(resp.Message.Text), 220)
			if brief == "" {
				return
			}
			results <- microWorkerResult{
				nodeID:     node.ID,
				brief:      brief,
				specialist: assignment.Profile.Name,
				reason:     assignment.Reason,
			}
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		if strings.TrimSpace(result.nodeID) == "" || strings.TrimSpace(result.brief) == "" {
			continue
		}
		a.Session.RecordTaskGraphSpecialistAssignment(result.nodeID, result.specialist, result.reason)
		a.Session.RecordTaskGraphMicroWorkerBrief(result.nodeID, result.brief)
		updated = true
	}
	if updated && a.Store != nil {
		return a.Store.Save(a.Session)
	}
	return nil
}

func shouldRunInteractiveMicroWorkers(trigger string, graph *TaskGraph) bool {
	if graph == nil || len(graph.Nodes) == 0 {
		return false
	}
	normalizedTrigger := strings.ToLower(strings.TrimSpace(trigger))
	switch {
	case strings.Contains(normalizedTrigger, "replan"):
		return true
	case strings.Contains(normalizedTrigger, "executor"):
		return true
	case strings.Contains(normalizedTrigger, "tool:run_shell_background"),
		strings.Contains(normalizedTrigger, "tool:run_shell_bundle_background"),
		strings.Contains(normalizedTrigger, "tool:check_shell_job"),
		strings.Contains(normalizedTrigger, "tool:check_shell_bundle"):
		return true
	default:
		return false
	}
}

func (a *Agent) executorAwareMicroWorkerCandidates(limit int) []TaskNode {
	if a == nil || a.Session == nil || a.Session.TaskGraph == nil {
		return nil
	}
	state := a.Session.TaskState
	orderedIDs := make([]string, 0, 4)
	if state != nil {
		if focus := strings.TrimSpace(state.ExecutorFocusNode); focus != "" {
			orderedIDs = append(orderedIDs, focus)
		}
		orderedIDs = append(orderedIDs, state.ExecutorParallelNodes...)
	}
	candidates := make([]TaskNode, 0, limit)
	seen := map[string]struct{}{}
	for _, nodeID := range orderedIDs {
		node, ok := a.Session.TaskGraph.Node(nodeID)
		if !ok || !taskNodeEligibleForMicroWorker(node) {
			continue
		}
		if _, exists := seen[node.ID]; exists {
			continue
		}
		seen[node.ID] = struct{}{}
		candidates = append(candidates, node)
		if limit > 0 && len(candidates) >= limit {
			return candidates
		}
	}
	for _, node := range a.Session.TaskGraphMicroWorkerCandidates(limit) {
		if _, exists := seen[node.ID]; exists {
			continue
		}
		seen[node.ID] = struct{}{}
		candidates = append(candidates, node)
		if limit > 0 && len(candidates) >= limit {
			break
		}
	}
	return candidates
}

func taskNodeEligibleForMicroWorker(node TaskNode) bool {
	if !isPrimaryTaskNode(node) {
		return false
	}
	status := canonicalTaskNodeStatus(node.Status)
	if node.MicroWorkerBrief != "" || status == "completed" || status == "failed" || status == "stale" || status == "superseded" || status == "canceled" || status == "preempted" {
		return false
	}
	return true
}

func buildInteractiveMicroWorkerPrompt(state *TaskState, node TaskNode, trigger string) string {
	var b strings.Builder
	b.WriteString("Produce a focused brief for the following task-graph node.\n")
	if state != nil {
		if state.Goal != "" {
			b.WriteString("\nGoal:\n")
			b.WriteString(state.Goal)
			b.WriteString("\n")
		}
		if state.PlanSummary != "" {
			b.WriteString("\nPlan summary:\n")
			b.WriteString(compactPromptSection(compactPlanSummary(state.PlanSummary), 500))
			b.WriteString("\n")
		}
		if len(state.PendingChecks) > 0 {
			b.WriteString("\nPending checks:\n- ")
			b.WriteString(strings.Join(state.PendingChecks, "\n- "))
			b.WriteString("\n")
		}
	}
	b.WriteString("\nTrigger:\n")
	b.WriteString(strings.TrimSpace(trigger))
	b.WriteString("\n\nTask-graph node:\n")
	b.WriteString(fmt.Sprintf("- ID: %s\n- Title: %s\n- Kind: %s\n- Status: %s\n", node.ID, node.Title, node.Kind, node.Status))
	if len(node.DependsOn) > 0 {
		b.WriteString("- Depends on: " + strings.Join(node.DependsOn, ", ") + "\n")
	}
	if node.RetryBudget > 0 {
		b.WriteString(fmt.Sprintf("- Retry budget: %d/%d used\n", node.RetryUsed, node.RetryBudget))
	}
	if node.LastFailure != "" {
		b.WriteString("- Last failure: " + compactPromptSection(node.LastFailure, 160) + "\n")
	}
	if node.EditableSpecialist != "" {
		b.WriteString("- Editable specialist: " + node.EditableSpecialist + "\n")
	}
	if len(node.EditableLeasePaths) > 0 {
		b.WriteString("- Editable lease: " + strings.Join(node.EditableLeasePaths, ", ") + "\n")
	} else if len(node.EditableOwnershipPaths) > 0 {
		b.WriteString("- Editable ownership: " + strings.Join(node.EditableOwnershipPaths, ", ") + "\n")
	}
	if node.ReadOnlyWorkerSummary != "" {
		b.WriteString("- Read-only worker evidence: " + compactPromptSection(node.ReadOnlyWorkerSummary, 180) + "\n")
	}
	if node.LifecycleNote != "" {
		b.WriteString("- Lifecycle note: " + node.LifecycleNote + "\n")
	}
	b.WriteString("\nReturn 3 short bullets covering:\n1. what to watch\n2. the next check or tool step\n3. why this node matters for final correctness")
	return b.String()
}

func (a *Agent) shouldReviewInteractiveFinalAnswer(reply string, attemptedEditTool bool, unresolvedVerification bool) bool {
	if a == nil || a.Session == nil || a.Session.TaskState == nil {
		return false
	}
	if strings.TrimSpace(reply) == "" {
		return false
	}
	state := a.Session.TaskState
	if state.FinalReviewCount >= 2 {
		return false
	}
	if unresolvedVerification || attemptedEditTool {
		return true
	}
	if len(state.CompletedSteps) > 0 || len(state.FailedAttempts) > 0 || len(state.PendingChecks) > 0 {
		return true
	}
	if a.Session.TaskGraph != nil {
		for _, node := range a.Session.TaskGraph.Nodes {
			status := canonicalTaskNodeStatus(node.Status)
			if status == "blocked" || status == "in_progress" || status == "ready" {
				return true
			}
		}
	}
	if len(a.Session.Plan) > 0 || len(a.Session.BackgroundJobs) > 0 {
		return true
	}
	return false
}

func (a *Agent) reviewInteractiveFinalAnswer(ctx context.Context, reply string, unresolvedVerification bool) (bool, string) {
	client, model := a.ensureInteractiveReviewerClient()
	if client == nil || strings.TrimSpace(model) == "" || a.Session == nil || a.Session.TaskState == nil {
		return true, ""
	}
	resp, err := client.Complete(ctx, ChatRequest{
		Model: model,
		System: strings.Join([]string{
			"You review a coding agent's proposed final answer before it is shown to the user.",
			"Approve only if the answer matches the task state and does not skip an obvious remaining step.",
			"Start with APPROVED or NEEDS_REVISION.",
			"If revision is needed, give short concrete feedback.",
		}, "\n"),
		Messages: []Message{{
			Role: "user",
			Text: buildInteractiveFinalAnswerReviewerPrompt(a.Session.TaskState, a.Session.TaskGraph, a.Session.BackgroundJobs, a.Session.BackgroundBundles, reply, unresolvedVerification),
		}},
		MaxTokens:   min(512, max(256, a.Config.MaxTokens/4)),
		Temperature: 0.1,
		WorkingDir:  a.Session.WorkingDir,
	})
	if err != nil {
		return true, ""
	}
	text := strings.TrimSpace(resp.Message.Text)
	if strings.HasPrefix(strings.ToUpper(text), "APPROVED") {
		a.Session.TaskState.NoteFinalReview("approved", text)
		return true, text
	}
	a.Session.TaskState.NoteFinalReview("needs_revision", text)
	return false, text
}

func buildInteractiveFinalAnswerReviewerPrompt(state *TaskState, graph *TaskGraph, jobs []BackgroundShellJob, bundles []BackgroundShellBundle, reply string, unresolvedVerification bool) string {
	var b strings.Builder
	b.WriteString("Review the following proposed final answer from a coding agent.\n")
	if state != nil {
		if rendered := strings.TrimSpace(state.RenderPromptSection()); rendered != "" {
			b.WriteString("\nTask state:\n")
			b.WriteString(rendered)
			b.WriteString("\n")
		}
	}
	if graph != nil {
		if rendered := strings.TrimSpace(graph.RenderPromptSection()); rendered != "" {
			b.WriteString("\nTask graph:\n")
			b.WriteString(rendered)
			b.WriteString("\n")
		}
	}
	if jobsText := strings.TrimSpace(renderBackgroundJobsPrompt(jobs, "")); jobsText != "" {
		b.WriteString("\nBackground jobs:\n")
		b.WriteString(jobsText)
		b.WriteString("\n")
	}
	if bundlesText := strings.TrimSpace(renderBackgroundBundlesPrompt(bundles)); bundlesText != "" {
		b.WriteString("\nBackground bundles:\n")
		b.WriteString(bundlesText)
		b.WriteString("\n")
	}
	if unresolvedVerification {
		b.WriteString("\nVerification status: unresolved failures remain.\n")
	}
	b.WriteString("\nProposed final answer:\n")
	b.WriteString(reply)
	b.WriteString("\n\nApprove only if it does not ignore pending checks, active background jobs, or unresolved verification.")
	return b.String()
}

func firstPlanItemText(plan string) string {
	items := parsePlanItemsFromText(plan)
	if len(items) == 0 {
		return firstNonEmptyLine(plan)
	}
	return strings.TrimSpace(items[0].Step)
}

func (a *Agent) noteToolExecutionStart(call ToolCall) {
	state := a.Session.EnsureTaskState()
	state.SetPhase("execution")
	state.SetNextStep(fmt.Sprintf("Finish %s and inspect the result.", strings.TrimSpace(call.Name)))
	state.RecordEvent("tool_start", strings.TrimSpace(state.ExecutorFocusNode), call.Name, "Started "+strings.TrimSpace(call.Name), "", "started", false)
	a.Session.ensureSharedPlanInProgress()
}

func (a *Agent) noteToolExecutionResult(call ToolCall, out string, err error) {
	a.noteToolExecutionResultDetailed(call, ToolExecutionResult{DisplayText: out}, err)
}

func (a *Agent) noteToolExecutionResultDetailed(call ToolCall, result ToolExecutionResult, err error) {
	state := a.Session.EnsureTaskState()
	summary := summarizeToolInvocation(a.Config, call)
	if summary == "" {
		summary = strings.TrimSpace(call.Name)
	}
	out := strings.TrimSpace(result.DisplayText)
	meta := result.Meta
	a.syncTaskGraphFromToolMeta(meta)
	policy := buildToolExecutionPolicy(call, result, a.Session)
	if err != nil {
		state.AddFailedAttempt(summary + ": " + truncateStatusSnippet(firstNonEmptyLine(err.Error()), 160))
		a.Session.RecordPlanNodeFailure(policy.OwnerNodeID, call.Name, err.Error())
		state.RecordEvent("tool_error", policy.OwnerNodeID, call.Name, summary, err.Error(), "error", true)
		state.SetPhase("recovery")
		return
	}
	effect := strings.TrimSpace(strings.ToLower(toolMetaString(meta, "effect")))
	switch call.Name {
	case "check_shell_job", "check_shell_bundle":
		if backgroundCheckHasFailureDetailed(call, out, meta) {
			a.applyToolExecutionPolicy(policy, call, summary, out)
			state.AddFailedAttempt(summary + ": " + truncateStatusSnippet(firstNonEmptyLine(out), 160))
			a.Session.RecordPlanNodeFailure(policy.OwnerNodeID, call.Name, out)
			state.RecordEvent("background_failure", policy.OwnerNodeID, call.Name, summary, out, "failed", true)
			state.SetPhase("recovery")
			state.SetNextStep("Inspect the failed background job output or explain the blocker.")
			a.syncBackgroundJobPendingCheck()
			return
		}
		planHandled := a.applyToolExecutionPolicy(policy, call, summary, out)
		if !planHandled && toolExecutionShouldAdvancePlanDetailed(call, out, meta) {
			state.AddCompletedStep(summary)
			a.Session.advanceSharedPlan()
		} else {
			state.AddConfirmedFact(summary)
		}
		state.RecordEvent("tool_result", policy.OwnerNodeID, call.Name, summary, out, "ok", false)
		a.syncBackgroundJobPendingCheck()
	case "run_shell":
		planHandled := a.applyToolExecutionPolicy(policy, call, summary, out)
		state.AddCompletedStep(summary)
		if !planHandled && toolExecutionShouldAdvancePlanDetailed(call, out, meta) {
			a.Session.advanceSharedPlan()
		}
		state.RecordEvent("tool_result", policy.OwnerNodeID, call.Name, summary, out, "ok", false)
	case "run_shell_background", "run_shell_bundle_background":
		a.applyToolExecutionPolicy(policy, call, summary, out)
		state.AddCompletedStep(summary)
		state.SetNextStep(backgroundShellJobPendingCheck)
		a.attachBackgroundBundleFromMeta(meta)
		state.RecordEvent("background_start", policy.OwnerNodeID, call.Name, summary, out, "running", false)
		a.syncBackgroundJobPendingCheck()
	default:
		planHandled := a.applyToolExecutionPolicy(policy, call, summary, out)
		switch effect {
		case "inspect":
			state.AddConfirmedFact(summary)
		case "edit":
			state.AddCompletedStep(summary)
			if toolMetaBool(meta, "requires_verification") || len(toolMetaStringSlice(meta, "changed_paths")) > 0 {
				state.AddPendingCheck(verificationPendingCheck)
				a.markBackgroundBundlesStale("A newer edit changed the workspace after this bundle was started.")
				if !planHandled {
					a.Session.advanceSharedPlan()
				}
			}
		case "git_mutation":
			state.AddCompletedStep(summary)
			if !planHandled {
				a.Session.advanceSharedPlan()
			}
		case "plan":
			state.AddConfirmedFact(summary)
		default:
			switch call.Name {
			case "read_file", "grep", "list_files", "git_status", "git_diff":
				state.AddConfirmedFact(summary)
			case "apply_patch", "write_file", "replace_in_file":
				state.AddCompletedStep(summary)
				state.AddPendingCheck(verificationPendingCheck)
				a.markBackgroundBundlesStale("A newer edit changed the workspace after this bundle was started.")
				if !planHandled {
					a.Session.advanceSharedPlan()
				}
			default:
				state.AddCompletedStep(summary)
			}
		}
		state.RecordEvent("tool_result", policy.OwnerNodeID, call.Name, summary, out, "ok", false)
	}
	state.SetPhase("execution")
}

func (a *Agent) applyToolExecutionPolicy(policy ToolExecutionPolicyOutcome, call ToolCall, summary string, out string) bool {
	if a == nil || a.Session == nil {
		return false
	}
	ownerNodeID := strings.TrimSpace(policy.OwnerNodeID)
	if ownerNodeID == "" {
		return false
	}
	currentStatus := ""
	if a.Session.TaskGraph != nil {
		if node, ok := a.Session.TaskGraph.Node(ownerNodeID); ok {
			currentStatus = canonicalTaskNodeStatus(node.Status)
		}
	}
	note := firstNonBlankString(
		policy.LifecycleNote,
		truncateStatusSnippet(firstNonEmptyLine(out), 160),
		summary,
	)
	if currentStatus == "blocked" && strings.TrimSpace(strings.ToLower(policy.PlanEffect)) != "block" {
		note = "Recovered after " + truncateStatusSnippet(summary, 140)
	}
	switch strings.TrimSpace(strings.ToLower(policy.PlanEffect)) {
	case "complete":
		return a.Session.SetPlanNodeLifecycle(ownerNodeID, "completed", note)
	case "block":
		return a.Session.SetPlanNodeLifecycle(ownerNodeID, "blocked", note)
	default:
		return a.Session.SetPlanNodeLifecycle(ownerNodeID, "in_progress", note)
	}
}

func (a *Agent) noteFocusedExecutorNodeActivity(call ToolCall, meta map[string]any, summary string) {
	if a == nil || a.Session == nil || a.Session.TaskState == nil || a.Session.TaskGraph == nil {
		return
	}
	focusNodeID := strings.TrimSpace(a.Session.TaskState.ExecutorFocusNode)
	if focusNodeID == "" {
		return
	}
	node, ok := a.Session.TaskGraph.Node(focusNodeID)
	if !ok || canonicalTaskNodeStatus(node.Status) != "blocked" {
		return
	}
	if !toolExecutionCanUnblockFocusedNode(call, meta) {
		return
	}
	note := "Recovered after " + truncateStatusSnippet(summary, 140)
	a.Session.TaskGraph.SetNodeLifecycle(focusNodeID, "in_progress", note)
	a.Session.SetPlanNodeInProgress(focusNodeID)
}

func toolExecutionCanUnblockFocusedNode(call ToolCall, meta map[string]any) bool {
	effect := strings.TrimSpace(strings.ToLower(toolMetaString(meta, "effect")))
	switch effect {
	case "inspect", "edit", "execute", "git_mutation":
		return true
	}
	switch strings.TrimSpace(call.Name) {
	case "read_file", "grep", "list_files", "git_status", "git_diff",
		"apply_patch", "write_file", "replace_in_file",
		"run_shell", "run_shell_background", "run_shell_bundle_background",
		"check_shell_job", "check_shell_bundle",
		"git_add", "git_commit", "git_push", "git_create_pr":
		return true
	default:
		return false
	}
}

func (a *Agent) noteVerificationResult(report VerificationReport) {
	state := a.Session.EnsureTaskState()
	if report.HasFailures() {
		state.AddFailedAttempt("Verification failed: " + truncateStatusSnippet(report.FailureSummary(), 180))
		a.Session.RecordPlanNodeFailure(strings.TrimSpace(state.ExecutorFocusNode), "verify", report.FailureSummary())
		state.RecordEvent("verification", strings.TrimSpace(state.ExecutorFocusNode), "verify", report.FailureSummary(), report.RenderShort(), "failed", true)
		state.SetPhase("recovery")
		state.SetNextStep("Fix the failing verification or explain the blocker clearly.")
		return
	}
	state.AddCompletedStep("Automatic verification passed.")
	state.RecordEvent("verification", strings.TrimSpace(state.ExecutorFocusNode), "verify", "Automatic verification passed.", report.SummaryLine(), "completed", true)
	state.RemovePendingCheck(verificationPendingCheck)
	a.syncBackgroundJobPendingCheck()
	if len(state.PendingChecks) > 0 {
		state.SetPhase("execution")
		state.SetNextStep(state.PendingChecks[0])
	} else {
		state.SetPhase("review")
		state.SetNextStep("Summarize the completed change and the verification result.")
	}
	a.Session.advanceSharedPlan()
}

func (a *Agent) refreshBackgroundJobs() {
	if a == nil || a.Workspace.BackgroundJobs == nil {
		return
	}
	jobs := a.Workspace.BackgroundJobs.Snapshot()
	for _, job := range jobs {
		if strings.TrimSpace(job.Status) == "running" {
			_, _ = a.Workspace.BackgroundJobs.SyncJob(job.ID)
		}
	}
	bundles := a.Workspace.BackgroundJobs.SnapshotBundles()
	for _, bundle := range bundles {
		if strings.EqualFold(strings.TrimSpace(bundle.Status), "running") {
			_, _, _ = a.Workspace.BackgroundJobs.SyncBundle(bundle.ID)
		}
	}
	a.syncBackgroundJobPendingCheck()
}

func (a *Agent) hasRunningBackgroundJobs() bool {
	return a.hasActiveBackgroundWork()
}

func (a *Agent) hasActiveBackgroundWork() bool {
	if a == nil || a.Session == nil {
		return false
	}
	activeBundleJobs := map[string]struct{}{}
	inactiveBundleJobs := map[string]struct{}{}
	for _, bundle := range a.Session.BackgroundBundles {
		status := strings.TrimSpace(strings.ToLower(bundle.Status))
		for _, jobID := range bundle.JobIDs {
			trimmedID := strings.TrimSpace(jobID)
			if trimmedID == "" {
				continue
			}
			switch status {
			case "running":
				activeBundleJobs[trimmedID] = struct{}{}
			case "stale", "superseded", "completed", "failed", "canceled", "preempted":
				inactiveBundleJobs[trimmedID] = struct{}{}
			}
		}
	}
	if len(activeBundleJobs) > 0 {
		return true
	}
	for _, job := range a.Session.BackgroundJobs {
		if !strings.EqualFold(strings.TrimSpace(job.Status), "running") {
			continue
		}
		if _, blocked := inactiveBundleJobs[strings.TrimSpace(job.ID)]; blocked {
			continue
		}
		if _, active := activeBundleJobs[strings.TrimSpace(job.ID)]; active {
			return true
		}
		if len(activeBundleJobs) == 0 {
			return true
		}
	}
	return false
}

func (a *Agent) syncBackgroundJobPendingCheck() {
	if a == nil || a.Session == nil {
		return
	}
	state := a.Session.EnsureTaskState()
	if a.hasRunningBackgroundJobs() {
		state.AddPendingCheck(backgroundShellJobPendingCheck)
		return
	}
	state.RemovePendingCheck(backgroundShellJobPendingCheck)
}

func backgroundCheckHasFailure(call ToolCall, out string) bool {
	return backgroundCheckHasFailureDetailed(call, out, nil)
}

func backgroundCheckHasFailureDetailed(call ToolCall, out string, meta map[string]any) bool {
	if strings.TrimSpace(call.Name) == "check_shell_job" {
		if strings.EqualFold(toolMetaString(meta, "job_status"), "failed") {
			return true
		}
	}
	if strings.TrimSpace(call.Name) == "check_shell_bundle" {
		if toolMetaInt(meta, "failed") > 0 {
			return true
		}
		if strings.EqualFold(toolMetaString(meta, "bundle_status"), "failed") {
			return true
		}
	}
	lower := strings.ToLower(strings.TrimSpace(out))
	switch strings.TrimSpace(call.Name) {
	case "check_shell_job":
		return strings.Contains(lower, "status: failed")
	case "check_shell_bundle":
		return strings.Contains(lower, "summary:") && !strings.Contains(lower, "failed=0")
	default:
		return false
	}
}

func toolExecutionShouldAdvancePlan(call ToolCall, out string) bool {
	return toolExecutionShouldAdvancePlanDetailed(call, out, nil)
}

func toolExecutionShouldAdvancePlanDetailed(call ToolCall, out string, meta map[string]any) bool {
	if strings.EqualFold(toolMetaString(meta, "plan_effect"), "complete") {
		return true
	}
	switch strings.TrimSpace(call.Name) {
	case "check_shell_job":
		if strings.EqualFold(toolMetaString(meta, "job_status"), "completed") {
			return true
		}
	case "check_shell_bundle":
		if strings.EqualFold(toolMetaString(meta, "bundle_status"), "completed") &&
			toolMetaInt(meta, "running") == 0 &&
			toolMetaInt(meta, "failed") == 0 {
			return true
		}
	case "run_shell":
		if toolMetaBool(meta, "verification_like") {
			return true
		}
	}
	lower := strings.ToLower(strings.TrimSpace(out))
	switch strings.TrimSpace(call.Name) {
	case "run_shell":
		return runShellOutputLooksLikeVerification(out)
	case "run_shell_background", "run_shell_bundle_background":
		return false
	case "check_shell_job":
		return strings.Contains(lower, "status: completed")
	case "check_shell_bundle":
		return strings.Contains(lower, "summary:") && strings.Contains(lower, "running=0") && strings.Contains(lower, "failed=0")
	default:
		return false
	}
}

func runShellOutputLooksLikeVerification(out string) bool {
	lower := strings.ToLower(strings.TrimSpace(out))
	return strings.Contains(lower, "build") ||
		strings.Contains(lower, "test") ||
		strings.Contains(lower, "verification") ||
		strings.Contains(lower, "passed") ||
		strings.Contains(lower, "ok ")
}

func (a *Agent) markBackgroundBundlesStale(reason string) {
	if a == nil || a.Session == nil {
		return
	}
	changed := false
	for _, bundle := range a.Session.BackgroundBundles {
		current := bundle
		status := strings.TrimSpace(strings.ToLower(current.Status))
		if status == "stale" || status == "superseded" {
			continue
		}
		if !shouldInvalidateBackgroundBundle(current) {
			continue
		}
		if a.Workspace.BackgroundJobs != nil {
			if canceled, _, err := a.Workspace.BackgroundJobs.CancelBundle(current.ID, "stale", reason, "workspace_edit"); err == nil {
				current = canceled
			} else {
				current.Status = "stale"
				current.PreemptedBy = "workspace_edit"
				current.CancelReason = strings.TrimSpace(reason)
				current.LifecycleNote = strings.TrimSpace(reason)
				current.UpdatedAt = time.Now()
				current.Normalize()
				a.Session.UpsertBackgroundBundle(current)
				a.Session.MarkBundleLifecycle(current.ID, current.Status, current.LifecycleNote)
			}
		} else {
			current.Status = "stale"
			current.PreemptedBy = "workspace_edit"
			current.CancelReason = strings.TrimSpace(reason)
			current.LifecycleNote = strings.TrimSpace(reason)
			current.UpdatedAt = time.Now()
			current.Normalize()
			a.Session.UpsertBackgroundBundle(current)
			a.Session.MarkBundleLifecycle(current.ID, current.Status, current.LifecycleNote)
		}
		a.Session.EnsureTaskState().RecordEvent("background_preempt", current.OwnerNodeID, "background_bundle", current.Summary, firstNonBlankString(current.CancelReason, current.LifecycleNote), current.Status, true)
		changed = true
	}
	if changed {
		a.syncBackgroundJobPendingCheck()
	}
}

func shouldInvalidateBackgroundBundle(bundle BackgroundShellBundle) bool {
	status := strings.TrimSpace(strings.ToLower(bundle.Status))
	switch status {
	case "running", "completed", "failed":
	default:
		return false
	}
	commandText := strings.ToLower(strings.Join(bundle.CommandSummaries, " "))
	if strings.Contains(commandText, "test") ||
		strings.Contains(commandText, "build") ||
		strings.Contains(commandText, "verify") ||
		strings.Contains(commandText, "check") ||
		strings.Contains(commandText, "ctest") ||
		strings.Contains(commandText, "msbuild") ||
		strings.Contains(commandText, "ninja") {
		return true
	}
	return false
}

func (a *Agent) syncTaskGraphFromToolMeta(meta map[string]any) {
	if a == nil || a.Session == nil {
		return
	}
	a.attachBackgroundBundleFromMeta(meta)
}

func (a *Agent) attachBackgroundBundleFromMeta(meta map[string]any) {
	if a == nil || a.Session == nil {
		return
	}
	bundle, jobs, ok := backgroundBundleFromToolMeta(meta)
	if !ok {
		return
	}
	a.Session.UpsertBackgroundBundle(bundle)
	a.Session.AttachBackgroundBundle(bundle, jobs)
	a.Session.MarkBundleLifecycle(bundle.ID, bundle.Status, firstNonBlankString(bundle.LifecycleNote, bundle.LastSummary))
}

func backgroundBundleFromToolMeta(meta map[string]any) (BackgroundShellBundle, []BackgroundShellJob, bool) {
	if len(meta) == 0 {
		return BackgroundShellBundle{}, nil, false
	}
	bundleID := toolMetaString(meta, "bundle_id")
	if bundleID == "" {
		return BackgroundShellBundle{}, nil, false
	}
	bundle := BackgroundShellBundle{
		ID:               bundleID,
		OwnerNodeID:      toolMetaString(meta, "owner_node_id"),
		OwnerLeasePaths:  toolMetaStringSlice(meta, "owner_lease_paths"),
		Status:           firstNonBlankString(toolMetaString(meta, "bundle_status"), "running"),
		Summary:          summarizeBackgroundBundleCommands(toolMetaStringSlice(meta, "bundle_command_summaries")),
		CommandSummaries: toolMetaStringSlice(meta, "bundle_command_summaries"),
		JobIDs:           toolMetaStringSlice(meta, "bundle_job_ids"),
		LastSummary:      toolMetaString(meta, "bundle_summary"),
		VerificationLike: toolMetaBool(meta, "verification_like"),
		SupersededBy:     toolMetaString(meta, "superseded_by"),
		LifecycleNote:    toolMetaString(meta, "lifecycle_note"),
		CancelReason:     toolMetaString(meta, "cancel_reason"),
		PreemptedBy:      toolMetaString(meta, "preempted_by"),
		UpdatedAt:        time.Now(),
	}
	if bundle.Summary == "" {
		bundle.Summary = firstNonBlankString(bundle.LastSummary, bundle.ID)
	}
	if len(bundle.JobIDs) == 0 {
		if jobID := toolMetaString(meta, "job_id"); jobID != "" {
			bundle.JobIDs = []string{jobID}
		}
	}
	jobs := backgroundJobsFromMeta(meta)
	bundle.Normalize()
	return bundle, jobs, true
}

func backgroundJobsFromMeta(meta map[string]any) []BackgroundShellJob {
	if len(meta) == 0 {
		return nil
	}
	raw, ok := meta["job_status"]
	if !ok {
		if jobID := toolMetaString(meta, "job_id"); jobID != "" {
			return []BackgroundShellJob{{
				ID:             jobID,
				Status:         toolMetaString(meta, "job_status"),
				OwnerNodeID:    toolMetaString(meta, "owner_node_id"),
				CommandSummary: toolMetaString(meta, "command_summary"),
				CancelReason:   toolMetaString(meta, "cancel_reason"),
				PreemptedBy:    toolMetaString(meta, "preempted_by"),
			}}
		}
		return nil
	}
	items, ok := raw.([]map[string]any)
	if ok {
		out := make([]BackgroundShellJob, 0, len(items))
		for _, item := range items {
			out = append(out, BackgroundShellJob{
				ID:             toolMetaString(item, "id"),
				Status:         toolMetaString(item, "status"),
				OwnerNodeID:    toolMetaString(item, "owner_node_id"),
				CommandSummary: toolMetaString(item, "command_summary"),
				CancelReason:   toolMetaString(item, "cancel_reason"),
				PreemptedBy:    toolMetaString(item, "preempted_by"),
			})
		}
		return out
	}
	rawList, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]BackgroundShellJob, 0, len(rawList))
	for _, item := range rawList {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, BackgroundShellJob{
			ID:             toolMetaString(entry, "id"),
			Status:         toolMetaString(entry, "status"),
			OwnerNodeID:    toolMetaString(entry, "owner_node_id"),
			CommandSummary: toolMetaString(entry, "command_summary"),
			CancelReason:   toolMetaString(entry, "cancel_reason"),
			PreemptedBy:    toolMetaString(entry, "preempted_by"),
		})
	}
	return out
}

func toolMetaString(meta map[string]any, key string) string {
	if len(meta) == 0 {
		return ""
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	}
}

func toolMetaStringSlice(meta map[string]any, key string) []string {
	if len(meta) == 0 {
		return nil
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return normalizeTaskStateList(typed, 32)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(fmt.Sprintf("%v", item))
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return normalizeTaskStateList(out, 32)
	default:
		return nil
	}
}

func toolMetaInt(meta map[string]any, key string) int {
	if len(meta) == 0 {
		return 0
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		return parsed
	default:
		return 0
	}
}

func toolMetaBool(meta map[string]any, key string) bool {
	if len(meta) == 0 {
		return false
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}
