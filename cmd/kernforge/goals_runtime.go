package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	goalNoProgressBlockThreshold      = 3
	goalRepeatedFailureBlockThreshold = 3
)

type goalReviewDecision struct {
	Verdict       string
	NeedsRevision bool
	Feedback      string
}

func parseGoalTimeBudgetSeconds(raw string) (int, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("time budget is empty")
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, fmt.Errorf("invalid time budget: %s", raw)
		}
		return seconds, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 {
		return 0, fmt.Errorf("invalid time budget: %s", raw)
	}
	return int(duration.Seconds()), nil
}

func (rt *runtimeState) primeGoalRuntimeState(goal *GoalState, reason string) {
	if rt == nil || rt.session == nil || goal == nil {
		return
	}
	primeGoalSessionState(rt.session, goal, reason, rt.goalReviewerProfileLabel())
}

func primeGoalSessionState(session *Session, goal *GoalState, reason string, reviewerProfile string) {
	if session == nil || goal == nil {
		return
	}
	state := session.StartTaskState(goal.Objective)
	state.SetProfiles(session.Provider+" / "+session.Model, reviewerProfile)
	state.SetPhase("execution")
	state.SetHypothesis("Autonomous goal runtime should inspect, implement, independently review, verify, audit, and recover until the objective is satisfied or a concrete blocker is recorded.")
	state.SetNextStep("Run the next autonomous goal iteration.")
	state.RecordEvent(conversationEventKindGoal, "", "", "Goal runtime primed.", strings.TrimSpace(reason), "active", true)
	session.AcceptanceContract = goalAcceptanceContract(goal.Objective)
	criteria := goalCompletionCriteria(goal.Objective, session.AcceptanceContract)
	if len(goal.CompletionCriteria) == 0 {
		goal.CompletionCriteria = criteria
	}
	session.SetSharedPlan(goalPlanItems())
	session.ensureSharedPlanInProgress()
}

func (a *Agent) accountGoalProgressAfterTool(call ToolCall) {
	if a == nil || a.Session == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(call.Name), "update_goal") {
		return
	}
	index, ok := a.Session.GoalIndex("active")
	if !ok {
		return
	}
	goal := a.Session.Goals[index]
	goal.Normalize()
	if goal.Status != goalStatusActive {
		return
	}
	previousStatus := goal.Status
	goal.updateUsageTelemetry(a.Session)
	if goal.TokenBudget > 0 && goal.TokenUsedEstimate > goal.TokenBudget {
		goal.Status = goalStatusBudgetLimited
		goal.LastError = fmt.Sprintf("goal exceeded token budget estimate (%d > %d)", goal.TokenUsedEstimate, goal.TokenBudget)
	}
	goal.Touch()
	a.Session.UpsertGoal(goal)
	if previousStatus != goal.Status {
		a.Session.AppendConversationEvent(ConversationEvent{
			Kind:     conversationEventKindGoal,
			Severity: goalEventSeverity(goal),
			Summary:  fmt.Sprintf("goal status changed: %s", goal.Status),
			Entities: map[string]string{
				"goal":   goal.ID,
				"status": goal.Status,
				"tool":   strings.TrimSpace(call.Name),
			},
		})
	}
}

func (rt *runtimeState) goalReviewerProfileLabel() string {
	if rt == nil || rt.agent == nil {
		return ""
	}
	return rt.agent.reviewerProfileLabel()
}

func goalAcceptanceContract(objective string) *AcceptanceContract {
	intent := classifyTurnIntent(objective)
	explicitEdit := looksLikeExplicitEditIntent(objective) || intent == TurnIntentEditCode
	explicitGit := looksLikeExplicitGitIntent(objective)
	contract := buildAcceptanceContract(objective, intent, false, explicitEdit, explicitGit)
	if contract.Mode == "general" {
		contract.Mode = "inspect_and_fix"
	}
	contract.ExpectedBehaviors = append(contract.ExpectedBehaviors,
		"Run autonomous implementation, review, verification, completion-audit, final semantic review, and recovery loops until ready or blocked.",
	)
	contract.VerificationNotes = append(contract.VerificationNotes, "Autonomous goals should run /verify --full before completion audit when verification steps are available.")
	contract.Normalize()
	return &contract
}

func goalCompletionCriteria(objective string, contract *AcceptanceContract) []string {
	criteria := []string{
		"Objective is explicit and persisted in GoalState.",
		"Implementation pass has inspected and changed the workspace when needed.",
		"Independent review pass has run and any concrete revision request has been repaired.",
		"Latest /verify --full has no failing steps when verification steps are available.",
		"Latest /completion-audit is ready with no blockers or warnings.",
		"Final semantic goal review has approved the completed state.",
		"No unrecovered repeated failure or no-progress loop remains.",
	}
	if contract != nil {
		for _, behavior := range contract.ExpectedBehaviors {
			criteria = append(criteria, behavior)
		}
		if len(contract.RequiredArtifacts) > 0 {
			criteria = append(criteria, "Required artifacts exist: "+strings.Join(contract.RequiredArtifacts, ", "))
		}
	}
	if strings.TrimSpace(objective) != "" && containsAny(strings.ToLower(objective), "markdown", ".md", "문서", "파일") {
		criteria = append(criteria, "Requested file or markdown objective source is reflected in artifacts.")
	}
	return normalizeTaskStateList(criteria, 16)
}

func goalPlanItems() []PlanItem {
	return []PlanItem{
		{Step: "Inspect the objective, repository state, acceptance contract, and task graph.", Status: "pending"},
		{Step: "Implement or modify code and documentation required by the goal.", Status: "pending"},
		{Step: "Run an independent review pass and repair concrete findings.", Status: "pending"},
		{Step: "Run full verification and record the result.", Status: "pending"},
		{Step: "Run completion audit and compare it to the goal criteria.", Status: "pending"},
		{Step: "Recover, replan, or block only with a concrete repeated failure or no-progress reason.", Status: "pending"},
	}
}

func (rt *runtimeState) runGoalIteration(ctx context.Context, goal GoalState) (GoalState, bool, error) {
	if rt == nil || rt.session == nil {
		return goal, true, fmt.Errorf("no active session")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		iteration := GoalIteration{
			Index:     goal.Iteration + 1,
			Status:    goalStatusCanceled,
			StartedAt: time.Now(),
		}
		return rt.finishGoalIterationError(goal, iteration, err)
	}
	if goal.TimeBudgetSeconds > 0 && !goal.CreatedAt.IsZero() && time.Since(goal.CreatedAt) > time.Duration(goal.TimeBudgetSeconds)*time.Second {
		goal.Status = goalStatusBudgetLimited
		goal.LastError = fmt.Sprintf("goal exceeded time budget (%ds)", goal.TimeBudgetSeconds)
		goal.Touch()
		rt.session.UpsertGoal(goal)
		_ = rt.writeGoalArtifacts(goal)
		if rt.store != nil {
			_ = rt.store.Save(rt.session)
		}
		fmt.Fprintln(rt.writer, rt.ui.warnLine(goal.LastError))
		return goal, true, nil
	}
	goal.updateUsageTelemetry(rt.session)
	if goal.TokenBudget > 0 && goal.TokenUsedEstimate > goal.TokenBudget {
		goal.Status = goalStatusBudgetLimited
		goal.LastError = fmt.Sprintf("goal exceeded token budget estimate (%d > %d)", goal.TokenUsedEstimate, goal.TokenBudget)
		goal.Touch()
		rt.session.UpsertGoal(goal)
		_ = rt.writeGoalArtifacts(goal)
		if rt.store != nil {
			_ = rt.store.Save(rt.session)
		}
		fmt.Fprintln(rt.writer, rt.ui.warnLine(goal.LastError))
		return goal, true, nil
	}
	iteration := GoalIteration{
		Index:     goal.Iteration + 1,
		Status:    goalStatusRunning,
		StartedAt: time.Now(),
	}
	fmt.Fprintln(rt.writer, rt.ui.subsection(fmt.Sprintf("Goal iteration %d", iteration.Index)))
	rt.primeGoalRuntimeState(&goal, fmt.Sprintf("iteration-%d", iteration.Index))
	rt.session.SetPlanNodeLifecycle("plan-01", "in_progress", "Inspecting goal state for autonomous iteration.")
	if ref, err := rt.createGoalCheckpoint(goal, iteration.Index); err == nil && strings.TrimSpace(ref.ID) != "" {
		iteration.CheckpointID = ref.ID
		iteration.CheckpointName = ref.Name
		goal.CheckpointRefs = append(goal.CheckpointRefs, ref)
	} else if err != nil {
		iteration.Commands = append(iteration.Commands, GoalCommandRecord{
			Iteration:  iteration.Index,
			Name:       "checkpoint",
			Status:     "failed",
			Summary:    err.Error(),
			StartedAt:  time.Now(),
			FinishedAt: time.Now(),
		})
	}
	implementReply, err := rt.runGoalAgentReply(ctx, buildGoalImplementationPrompt(goal, iteration.Index))
	iteration.ImplementReply = compactPromptSection(implementReply, 900)
	if err != nil {
		return rt.finishGoalIterationError(goal, iteration, err)
	}
	rt.session.SetPlanNodeLifecycle("plan-01", "completed", "Implementation pass inspected current goal state.")
	rt.session.SetPlanNodeLifecycle("plan-02", "completed", "Implementation pass completed or confirmed no code change was needed.")

	reviewRoot := rt.goalWorkspaceRoot()
	reviewReply, err := rt.runGoalReviewHarnessReply(ctx, goal, iteration, reviewRoot)
	iteration.ReviewReply = compactPromptSection(reviewReply, 900)
	if err != nil {
		return rt.finishGoalIterationError(goal, iteration, err)
	}
	decision := parseGoalReviewDecision(reviewReply)
	iteration.ReviewerVerdict = decision.Verdict
	iteration.ReviewerFeedback = compactPromptSection(decision.Feedback, 900)
	if decision.NeedsRevision {
		repairReply, repairErr := rt.runGoalAgentReply(ctx, buildGoalRepairPrompt(goal, iteration, decision, reviewRoot, rt.checkpoints))
		iteration.RepairReply = compactPromptSection(repairReply, 900)
		if repairErr != nil {
			return rt.finishGoalIterationError(goal, iteration, repairErr)
		}
	}
	rt.session.SetPlanNodeLifecycle("plan-03", "completed", "Review pass completed and concrete findings were repaired or cleared.")

	verifyCommand := startGoalCommand(iteration.Index, "verify", "/verify --full")
	verifyErr := rt.handleVerifyCommandContext(ctx, "--full")
	verifyCommand.finish(statusForErr(verifyErr), verificationSummaryOrError(rt.session, verifyErr))
	iteration.Commands = append(iteration.Commands, verifyCommand)
	if isGoalCancellationError(verifyErr) {
		return rt.finishGoalIterationError(goal, iteration, verifyErr)
	}
	if verifyErr != nil {
		iteration.Error = verifyErr.Error()
		iteration.Status = goalStatusBlocked
		goal.Status = goalStatusBlocked
		goal.LastError = verifyErr.Error()
	} else {
		rt.session.SetPlanNodeLifecycle("plan-04", "completed", "Full verification command completed.")
	}
	if rt.session.LastVerification != nil {
		iteration.Verification = rt.session.LastVerification.SummaryLine()
	}

	if err := ctx.Err(); err != nil {
		return rt.finishGoalIterationError(goal, iteration, err)
	}
	rt.session.SetPlanNodeLifecycle("plan-05", "completed", "Completion audit is being generated for the goal.")
	auditCommand := startGoalCommand(iteration.Index, "completion-audit", "/completion-audit <goal objective>")
	audit, auditErr := rt.runGoalCompletionAudit(goal)
	auditCommand.finish(statusForErr(auditErr), completionAuditSummaryOrError(audit, auditErr))
	iteration.Commands = append(iteration.Commands, auditCommand)
	if auditErr != nil {
		iteration.Error = auditErr.Error()
		iteration.Status = goalStatusBlocked
		goal.Status = goalStatusBlocked
		goal.LastError = auditErr.Error()
	} else {
		applyGoalAuditToIteration(&iteration, audit)
		goal.LastAudit = goalAuditStateFromArtifact(audit)
		progress := rt.evaluateGoalProgress(goal, audit, iteration)
		iteration.Progress = &progress
		iteration.ChangedFiles = append([]string(nil), progress.ChangedFiles...)
		goal.applyProgress(progress)
		if audit.Ready {
			semanticCommand := startGoalCommand(iteration.Index, "semantic-review", "independent semantic goal review")
			semanticReview, semanticErr := rt.runGoalSemanticReview(ctx, goal, audit, iteration)
			semanticCommand.finish(statusForErr(semanticErr), semanticReviewSummaryOrError(semanticReview, semanticErr))
			iteration.Commands = append(iteration.Commands, semanticCommand)
			if isGoalCancellationError(semanticErr) {
				return rt.finishGoalIterationError(goal, iteration, semanticErr)
			}
			if semanticErr != nil {
				iteration.Error = semanticErr.Error()
				iteration.Status = goalStatusBlocked
				goal.Status = goalStatusBlocked
				goal.LastError = semanticErr.Error()
			} else {
				iteration.SemanticReview = &semanticReview
				goal.LastSemanticReview = &semanticReview
				if semanticReview.Approved {
					iteration.Status = goalStatusComplete
					goal.Status = goalStatusComplete
					goal.CompletedAt = time.Now()
					goal.LastError = ""
					rt.session.SetPlanNodeLifecycle("plan-06", "completed", "Completion audit and semantic goal review are ready.")
				} else {
					repairReply, repairErr := rt.runGoalAgentReply(ctx, buildGoalSemanticRepairPrompt(goal, iteration, semanticReview))
					iteration.RepairReply = compactPromptSection(strings.Join([]string{iteration.RepairReply, repairReply}, "\n\n"), 1200)
					if isGoalCancellationError(repairErr) {
						return rt.finishGoalIterationError(goal, iteration, repairErr)
					}
					if repairErr != nil {
						iteration.Error = repairErr.Error()
						iteration.Status = goalStatusBlocked
						goal.Status = goalStatusBlocked
						goal.LastError = repairErr.Error()
					} else {
						iteration.Status = goalStatusPending
						rt.session.SetPlanNodeLifecycle("plan-06", "in_progress", "Semantic goal review requested a repair before completion.")
					}
				}
			}
		} else if goal.Status != goalStatusBlocked {
			if blocker := goalStagnationBlocker(goal); blocker != "" {
				iteration.Status = goalStatusBlocked
				goal.Status = goalStatusBlocked
				goal.LastError = blocker
				rt.session.SetPlanNodeLifecycle("plan-06", "blocked", blocker)
				if goal.AutoRollback {
					iteration.RollbackStatus = rt.rollbackGoalIterationCheckpoint(goal, iteration)
				}
			} else {
				iteration.Status = goalStatusPending
				rt.session.SetPlanNodeLifecycle("plan-06", "in_progress", "Completion audit is not ready; executing safe recovery actions.")
				if err := ctx.Err(); err != nil {
					return rt.finishGoalIterationError(goal, iteration, err)
				}
				recoveryCommand := startGoalCommand(iteration.Index, "recover", "/recover execute-safe goal "+goal.ID)
				recoveryErr := rt.handleRecoverCommandContext(ctx, "execute-safe goal "+goal.ID)
				recoveryCommand.finish(statusForErr(recoveryErr), errorOrText(recoveryErr, "safe recovery plan executed"))
				iteration.Commands = append(iteration.Commands, recoveryCommand)
				if isGoalCancellationError(recoveryErr) {
					return rt.finishGoalIterationError(goal, iteration, recoveryErr)
				}
				if recoveryErr != nil {
					iteration.RecoveryStatus = "failed: " + recoveryErr.Error()
				} else {
					iteration.RecoveryStatus = "executed"
				}
			}
		}
	}
	goal.updateUsageTelemetry(rt.session)
	return rt.finishGoalIteration(goal, iteration), goalStatusStopsAutonomousLoop(goal.Status), nil
}

func (rt *runtimeState) finishGoalIterationError(goal GoalState, iteration GoalIteration, err error) (GoalState, bool, error) {
	if isGoalCancellationError(err) {
		return rt.finishGoalIterationCanceled(goal, iteration, err)
	}
	iteration.Error = err.Error()
	iteration.Status = goalStatusBlocked
	if goalErrorLooksUsageLimited(err) {
		iteration.Status = goalStatusUsageLimited
	}
	iteration.FinishedAt = time.Now()
	goal.Iteration = iteration.Index
	goal.Status = iteration.Status
	goal.LastError = err.Error()
	goal.Iterations = append(goal.Iterations, iteration)
	goal.Touch()
	rt.session.UpsertGoal(goal)
	_ = rt.writeGoalArtifacts(goal)
	if rt.store != nil {
		_ = rt.store.Save(rt.session)
	}
	return goal, true, err
}

func (rt *runtimeState) finishGoalIterationCanceled(goal GoalState, iteration GoalIteration, err error) (GoalState, bool, error) {
	reason := goalCancellationReason(err)
	if iteration.Index <= 0 {
		iteration.Index = goal.Iteration + 1
	}
	if iteration.StartedAt.IsZero() {
		iteration.StartedAt = time.Now()
	}
	iteration.Error = reason
	iteration.Status = goalStatusCanceled
	iteration.FinishedAt = time.Now()
	goal.Iteration = iteration.Index
	goal.Status = goalStatusActive
	goal.LastError = reason
	goal.CommandHistory = append(goal.CommandHistory, iteration.Commands...)
	goal.Iterations = append(goal.Iterations, iteration)
	goal.Touch()
	rt.session.UpsertGoal(goal)
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindGoal,
		Severity: conversationSeverityInfo,
		Summary:  fmt.Sprintf("goal iteration %d canceled", iteration.Index),
		Entities: map[string]string{
			"goal":      goal.ID,
			"status":    goalStatusCanceled,
			"iteration": fmt.Sprintf("%d", iteration.Index),
		},
	})
	_ = rt.writeGoalArtifacts(goal)
	if rt.store != nil {
		_ = rt.store.Save(rt.session)
	}
	fmt.Fprintln(rt.writer, rt.ui.infoLine("Goal interrupted: "+goal.ID+" (goal remains active)"))
	return goal, true, nil
}

func (rt *runtimeState) finishGoalIteration(goal GoalState, iteration GoalIteration) GoalState {
	if iteration.Status == "" || iteration.Status == goalStatusRunning {
		iteration.Status = goal.Status
	}
	semanticSummary := ""
	if iteration.SemanticReview != nil {
		semanticSummary = semanticReviewSummaryOrError(*iteration.SemanticReview, nil)
	}
	iteration.ReplySummary = compactPromptSection(strings.Join([]string{iteration.ImplementReply, iteration.ReviewReply, iteration.RepairReply, semanticSummary}, "\n\n"), 1200)
	iteration.FinishedAt = time.Now()
	goal.Iteration = iteration.Index
	goal.CommandHistory = append(goal.CommandHistory, iteration.Commands...)
	goal.Iterations = append(goal.Iterations, iteration)
	goal.Touch()
	rt.session.UpsertGoal(goal)
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindGoal,
		Severity: goalEventSeverity(goal),
		Summary:  fmt.Sprintf("goal iteration %d: %s", iteration.Index, goal.Status),
		Entities: map[string]string{
			"goal":             goal.ID,
			"status":           goal.Status,
			"iteration":        fmt.Sprintf("%d", iteration.Index),
			"audit_status":     iteration.AuditStatus,
			"audit_ready":      fmt.Sprintf("%t", iteration.AuditReady),
			"progress_score":   fmt.Sprintf("%d", progressScore(iteration.Progress)),
			"no_progress":      fmt.Sprintf("%d", goal.NoProgressCount),
			"repeated_failure": fmt.Sprintf("%d", goal.RepeatedFailureCount),
		},
	})
	if err := rt.writeGoalArtifacts(goal); err != nil {
		goal.Status = goalStatusBlocked
		goal.LastError = err.Error()
	}
	if rt.store != nil {
		_ = rt.store.Save(rt.session)
	}
	if goal.Status == goalStatusComplete {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Goal complete: "+goal.ID))
	} else if goal.Status == goalStatusBlocked {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Goal blocked: "+goal.LastError))
	} else if goal.Status == goalStatusUsageLimited {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Goal usage-limited: "+goal.LastError))
	} else if goal.Status == goalStatusBudgetLimited {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Goal budget-limited: "+goal.LastError))
	}
	return goal
}

func goalErrorLooksUsageLimited(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *ProviderAPIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusTooManyRequests {
			return true
		}
		text := strings.ToLower(strings.Join([]string{
			apiErr.Message,
			apiErr.ErrorType,
			apiErr.Code,
			apiErr.RawBody,
		}, " "))
		if containsAny(text,
			"usage_limit",
			"usage limit",
			"quota",
			"insufficient_quota",
			"insufficient credits",
			"spend limit",
			"billing limit",
			"rate_limit",
			"rate limit",
		) {
			return true
		}
	}
	text := strings.ToLower(err.Error())
	return containsAny(text,
		"usage_limit",
		"usage limit",
		"quota",
		"insufficient_quota",
		"insufficient credits",
		"spend limit",
		"billing limit",
		"rate_limit",
		"rate limit",
	)
}

func (rt *runtimeState) goalWorkspaceRoot() string {
	if rt == nil {
		return ""
	}
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" && rt.session != nil {
		root = rt.session.WorkingDir
	}
	return strings.TrimSpace(root)
}

func (rt *runtimeState) createGoalCheckpoint(goal GoalState, iteration int) (GoalCheckpointRef, error) {
	if rt == nil || rt.checkpoints == nil {
		return GoalCheckpointRef{}, nil
	}
	root := workspaceCheckpointRoot(rt.workspace)
	if strings.TrimSpace(root) == "" && rt.session != nil {
		root = rt.session.WorkingDir
	}
	if strings.TrimSpace(root) == "" {
		return GoalCheckpointRef{}, nil
	}
	name := fmt.Sprintf("goal-%s-iteration-%02d", goal.ID, iteration)
	meta, err := rt.checkpoints.Create(root, name)
	if err != nil {
		return GoalCheckpointRef{}, err
	}
	return GoalCheckpointRef{
		Iteration: iteration,
		ID:        meta.ID,
		Name:      meta.Name,
		CreatedAt: meta.CreatedAt,
		Status:    "created",
	}, nil
}

func (rt *runtimeState) rollbackGoalIterationCheckpoint(goal GoalState, iteration GoalIteration) string {
	if rt == nil || rt.checkpoints == nil || strings.TrimSpace(iteration.CheckpointID) == "" {
		return "skipped: no checkpoint available"
	}
	root := workspaceCheckpointRoot(rt.workspace)
	if strings.TrimSpace(root) == "" && rt.session != nil {
		root = rt.session.WorkingDir
	}
	if strings.TrimSpace(root) == "" {
		return "skipped: workspace root is not configured"
	}
	if _, err := rt.checkpoints.Rollback(root, iteration.CheckpointID); err != nil {
		return "failed: " + err.Error()
	}
	return "rolled back to checkpoint " + iteration.CheckpointID
}

func (rt *runtimeState) runGoalSemanticReview(ctx context.Context, goal GoalState, audit CompletionAuditArtifact, iteration GoalIteration) (GoalSemanticReview, error) {
	if completionAuditSessionOwnsGoal(rt.session, &audit) {
		audit.OpenTasks = filterCompletionAuditOpenGoalTasks(audit.OpenTasks)
	}
	reply, err := rt.runGoalReviewerReply(ctx, buildGoalSemanticReviewPrompt(goal, audit, iteration, rt.goalWorkspaceRoot(), rt.checkpoints))
	if err != nil {
		return GoalSemanticReview{}, err
	}
	review := parseGoalSemanticReview(reply)
	if !audit.Ready && review.Approved {
		review.Approved = false
		review.Verdict = "needs_revision"
		review.Feedback = compactPromptSection("Completion audit is not ready; approval cannot be accepted. "+review.Feedback, 1200)
	}
	return review, nil
}

func buildGoalSemanticReviewPrompt(goal GoalState, audit CompletionAuditArtifact, iteration GoalIteration, root string, checkpoints *CheckpointManager) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Final semantic goal review for autonomous goal %s.\n\n", valueOrUnset(goal.ID))
	fmt.Fprintf(&b, "Objective:\n%s\n\n", strings.TrimSpace(goal.Objective))
	b.WriteString("Decide whether the actual workspace state satisfies the goal, not merely whether a command succeeded.\n")
	b.WriteString("Start with APPROVED only if the objective, completion criteria, verification result, and completion audit evidence are all sufficient.\n")
	b.WriteString("Start with NEEDS_REVISION if any meaningful implementation, test, documentation, artifact, or evidence gap remains.\n\n")
	if len(goal.CompletionCriteria) > 0 {
		b.WriteString("Completion criteria:\n")
		for _, item := range goal.CompletionCriteria {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	b.WriteString("Latest audit:\n")
	fmt.Fprintf(&b, "- Status: %s\n", valueOrDefault(audit.Status, "unknown"))
	fmt.Fprintf(&b, "- Ready: %t\n", audit.Ready)
	fmt.Fprintf(&b, "- Blockers: %d\n", len(audit.Blockers))
	for _, blocker := range limitStrings(audit.Blockers, 8) {
		fmt.Fprintf(&b, "  - %s\n", blocker)
	}
	fmt.Fprintf(&b, "- Warnings: %d\n", len(audit.Warnings))
	for _, warning := range limitStrings(audit.Warnings, 8) {
		fmt.Fprintf(&b, "  - %s\n", warning)
	}
	if audit.Verification != "" {
		fmt.Fprintf(&b, "- Audit verification: %s\n", audit.Verification)
	}
	if len(audit.OpenTasks) > 0 {
		b.WriteString("- Open tasks:\n")
		for _, task := range limitStrings(audit.OpenTasks, 8) {
			fmt.Fprintf(&b, "  - %s\n", task)
		}
	}
	b.WriteString("\nIteration evidence:\n")
	fmt.Fprintf(&b, "- Iteration: %d\n", iteration.Index)
	if iteration.Verification != "" {
		fmt.Fprintf(&b, "- Verification: %s\n", iteration.Verification)
	}
	if iteration.ReviewerVerdict != "" {
		fmt.Fprintf(&b, "- Review verdict: %s\n", iteration.ReviewerVerdict)
	}
	if iteration.ReviewerFeedback != "" {
		fmt.Fprintf(&b, "- Review feedback: %s\n", compactPromptSection(iteration.ReviewerFeedback, 500))
	}
	if len(iteration.ChangedFiles) > 0 {
		b.WriteString("- Changed files:\n")
		for _, path := range limitStrings(iteration.ChangedFiles, 16) {
			fmt.Fprintf(&b, "  - %s\n", path)
		}
	}
	if iteration.Progress != nil {
		fmt.Fprintf(&b, "- Progress score: %d\n", iteration.Progress.Score)
		for _, signal := range limitStrings(iteration.Progress.Signals, 8) {
			fmt.Fprintf(&b, "  - %s\n", signal)
		}
	}
	if len(goal.CommandHistory) > 0 {
		b.WriteString("\nRecent goal commands:\n")
		for _, command := range limitGoalCommands(goal.CommandHistory, 8) {
			fmt.Fprintf(&b, "- %s [%s] %s\n", valueOrUnset(command.Name), valueOrUnset(command.Status), compactPromptSection(command.Summary, 180))
		}
	}
	if evidence := buildGoalIterationReviewEvidence(root, iteration, checkpoints); evidence != "" {
		b.WriteString("\nWorkspace review evidence:\n")
		b.WriteString(evidence)
		b.WriteString("\n")
	}
	b.WriteString("\nReturn one of these exact leading verdicts:\n")
	b.WriteString("APPROVED: <concise evidence>\n")
	b.WriteString("NEEDS_REVISION: <concrete missing work>\n")
	return b.String()
}

func parseGoalReviewDecision(text string) goalReviewDecision {
	trimmed := strings.TrimSpace(text)
	upper := strings.ToUpper(trimmed)
	decision := goalReviewDecision{
		Verdict:       "reviewed",
		NeedsRevision: false,
		Feedback:      trimmed,
	}
	switch {
	case strings.HasPrefix(upper, "APPROVED"):
		decision.Verdict = "approved"
	case strings.HasPrefix(upper, "NEEDS_REVISION"), strings.HasPrefix(upper, "NEEDS REVISION"), strings.HasPrefix(upper, "REJECTED"):
		decision.Verdict = "needs_revision"
		decision.NeedsRevision = true
	case containsAny(
		strings.ToLower(trimmed),
		"needs_revision",
		"needs revision",
		"requires revision",
		"revision required",
		"must fix",
		"fix required",
		"blocking issue",
		"blocker",
		"not ready",
		"missing",
		"failed",
		"failure",
		"incomplete",
		"수정 필요",
		"수정해야",
		"보완 필요",
		"준비되지",
		"누락",
		"미흡",
		"실패",
		"문제",
		"부족",
	):
		decision.Verdict = "needs_revision"
		decision.NeedsRevision = true
	}
	return decision
}

func buildGoalRepairPrompt(goal GoalState, iteration GoalIteration, decision goalReviewDecision, root string, checkpoints *CheckpointManager) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Autonomous goal repair pass for iteration %d.\n\n", iteration.Index)
	fmt.Fprintf(&b, "Objective:\n%s\n\n", strings.TrimSpace(goal.Objective))
	b.WriteString("The independent review found concrete issues. Fix them directly now without asking the user.\n")
	if strings.TrimSpace(decision.Feedback) != "" {
		b.WriteString("\nReviewer feedback:\n")
		b.WriteString(decision.Feedback)
		b.WriteString("\n")
	}
	if evidence := buildGoalIterationReviewEvidence(root, iteration, checkpoints); evidence != "" {
		b.WriteString("\nImplementation context:\n")
		b.WriteString(evidence)
		b.WriteString("\n")
	}
	if len(goal.CompletionCriteria) > 0 {
		b.WriteString("\nCompletion criteria:\n")
		for _, item := range goal.CompletionCriteria {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	b.WriteString("\nAfter repairing, return a concise status and do not claim completion before verification and completion audit.")
	return b.String()
}

func buildGoalSemanticRepairPrompt(goal GoalState, iteration GoalIteration, review GoalSemanticReview) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Autonomous final-goal repair pass for iteration %d.\n\n", iteration.Index)
	fmt.Fprintf(&b, "Objective:\n%s\n\n", strings.TrimSpace(goal.Objective))
	b.WriteString("The final semantic goal reviewer did not approve completion. Fix the missing work directly without asking the user.\n")
	if strings.TrimSpace(review.Feedback) != "" {
		b.WriteString("\nSemantic review feedback:\n")
		b.WriteString(review.Feedback)
		b.WriteString("\n")
	}
	if len(goal.CompletionCriteria) > 0 {
		b.WriteString("\nCompletion criteria:\n")
		for _, item := range goal.CompletionCriteria {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	b.WriteString("\nAfter repairing, return a concise status. The runtime will re-run verification, completion audit, and final semantic review.")
	return b.String()
}

func parseGoalSemanticReview(text string) GoalSemanticReview {
	trimmed := strings.TrimSpace(text)
	upper := strings.ToUpper(trimmed)
	review := GoalSemanticReview{
		Verdict:    "needs_revision",
		Approved:   false,
		Feedback:   trimmed,
		ReviewedAt: time.Now(),
	}
	switch {
	case strings.HasPrefix(upper, "APPROVED"):
		review.Verdict = "approved"
		review.Approved = true
	case strings.HasPrefix(upper, "NEEDS_REVISION"), strings.HasPrefix(upper, "NEEDS REVISION"), strings.HasPrefix(upper, "REJECTED"):
		review.Verdict = "needs_revision"
	case containsAny(strings.ToLower(trimmed), "not ready", "missing", "must fix", "blocker", "수정 필요", "준비되지", "부족"):
		review.Verdict = "needs_revision"
	default:
		if trimmed == "" {
			review.Feedback = "semantic review returned no verdict"
		}
	}
	return review
}

func applyGoalAuditToIteration(iteration *GoalIteration, audit CompletionAuditArtifact) {
	if iteration == nil {
		return
	}
	iteration.AuditID = audit.ID
	iteration.AuditReady = audit.Ready
	iteration.AuditStatus = audit.Status
	iteration.Blockers = append([]string(nil), audit.Blockers...)
	iteration.Warnings = append([]string(nil), audit.Warnings...)
}

func goalAuditStateFromArtifact(audit CompletionAuditArtifact) *GoalAuditState {
	return &GoalAuditState{
		ID:       audit.ID,
		Ready:    audit.Ready,
		Status:   audit.Status,
		Blockers: append([]string(nil), audit.Blockers...),
		Warnings: append([]string(nil), audit.Warnings...),
	}
}

func (rt *runtimeState) evaluateGoalProgress(goal GoalState, audit CompletionAuditArtifact, iteration GoalIteration) GoalProgressState {
	changed := delegationChangedFiles(workspaceSnapshotRoot(rt.workspace))
	verification := strings.TrimSpace(iteration.Verification)
	signals := []string{}
	score := 0
	if len(changed) > 0 {
		score += 20
		signals = append(signals, fmt.Sprintf("%d changed file(s) accounted for", len(changed)))
	}
	if verification != "" {
		score += 20
		signals = append(signals, verification)
		if rt.session != nil && rt.session.LastVerification != nil && !rt.session.LastVerification.HasFailures() && completionAuditVerificationHasPassedStep(*rt.session.LastVerification) {
			score += 20
			signals = append(signals, "latest verification has no failures")
		}
	}
	if strings.EqualFold(iteration.ReviewerVerdict, "approved") || strings.EqualFold(iteration.ReviewerVerdict, "reviewed") {
		score += 10
		signals = append(signals, "review pass completed")
	}
	if audit.Ready {
		score += 40
		signals = append(signals, "completion audit ready")
	} else {
		signals = append(signals, fmt.Sprintf("completion audit not ready: blockers=%d warnings=%d", len(audit.Blockers), len(audit.Warnings)))
	}
	openTasks := len(audit.OpenTasks)
	failureSignature := goalFailureSignature(audit, iteration)
	fingerprint := goalProgressFingerprint(changed, verification, audit, failureSignature)
	progress := GoalProgressState{
		Score:            score,
		Fingerprint:      fingerprint,
		Signals:          normalizeTaskStateList(signals, 16),
		ChangedFiles:     normalizeTaskStateList(changed, 64),
		Verification:     verification,
		AuditReady:       audit.Ready,
		AuditStatus:      audit.Status,
		BlockerCount:     len(audit.Blockers),
		WarningCount:     len(audit.Warnings),
		OpenTaskCount:    openTasks,
		FailureSignature: failureSignature,
	}
	progress.Normalize()
	return progress
}

func (g *GoalState) applyProgress(progress GoalProgressState) {
	progress.Normalize()
	if !progress.AuditReady {
		if g.LastProgressFingerprint != "" && progress.Fingerprint == g.LastProgressFingerprint {
			g.NoProgressCount++
		} else {
			g.NoProgressCount = 0
		}
		if progress.FailureSignature != "" && progress.FailureSignature == g.LastFailureSignature {
			g.RepeatedFailureCount++
		} else if progress.FailureSignature != "" {
			g.RepeatedFailureCount = 1
		} else {
			g.RepeatedFailureCount = 0
		}
	} else {
		g.NoProgressCount = 0
		g.RepeatedFailureCount = 0
	}
	g.LastProgressFingerprint = progress.Fingerprint
	g.LastFailureSignature = progress.FailureSignature
	progress.NoProgressCount = g.NoProgressCount
	progress.RepeatedFailures = g.RepeatedFailureCount
	g.LastProgress = &progress
}

func goalStagnationBlocker(goal GoalState) string {
	if goal.NoProgressCount >= goalNoProgressBlockThreshold {
		return fmt.Sprintf("goal made no measurable progress for %d consecutive audit cycles", goal.NoProgressCount)
	}
	if goal.RepeatedFailureCount >= goalRepeatedFailureBlockThreshold {
		return fmt.Sprintf("goal hit the same failure signature for %d consecutive audit cycles: %s", goal.RepeatedFailureCount, valueOrUnset(goal.LastFailureSignature))
	}
	return ""
}

func goalFailureSignature(audit CompletionAuditArtifact, iteration GoalIteration) string {
	items := []string{}
	items = append(items, audit.Blockers...)
	items = append(items, audit.Warnings...)
	if strings.TrimSpace(iteration.Error) != "" {
		items = append(items, iteration.Error)
	}
	if len(items) == 0 && strings.TrimSpace(iteration.Verification) != "" && !iteration.AuditReady {
		items = append(items, iteration.Verification)
	}
	if len(items) == 0 {
		return ""
	}
	return compactPromptSection(strings.Join(normalizeTaskStateList(items, 8), " | "), 300)
}

func goalProgressFingerprint(changed []string, verification string, audit CompletionAuditArtifact, failureSignature string) string {
	parts := []string{
		strings.Join(normalizeTaskStateList(changed, 128), "|"),
		strings.TrimSpace(verification),
		strings.TrimSpace(audit.Status),
		fmt.Sprintf("ready=%t", audit.Ready),
		strings.TrimSpace(failureSignature),
		strings.Join(normalizeTaskStateList(audit.Blockers, 16), "|"),
		strings.Join(normalizeTaskStateList(audit.Warnings, 16), "|"),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:12])
}

func startGoalCommand(iteration int, name string, summary string) GoalCommandRecord {
	return GoalCommandRecord{
		Iteration: iteration,
		Name:      strings.TrimSpace(name),
		Summary:   strings.TrimSpace(summary),
		Status:    "running",
		StartedAt: time.Now(),
	}
}

func (r *GoalCommandRecord) finish(status string, summary string) {
	if r == nil {
		return
	}
	r.Status = strings.TrimSpace(status)
	if strings.TrimSpace(summary) != "" {
		r.Summary = strings.TrimSpace(summary)
	}
	r.FinishedAt = time.Now()
}

func statusForErr(err error) string {
	if isGoalCancellationError(err) {
		return "canceled"
	}
	if err != nil {
		return "failed"
	}
	return "passed"
}

func isGoalCancellationError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "request canceled") || strings.Contains(text, "context canceled")
}

func goalCancellationReason(err error) string {
	if err == nil {
		return "goal interrupted by user"
	}
	text := strings.TrimSpace(err.Error())
	if text == "" || errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(text), "context canceled") {
		return "goal interrupted by user"
	}
	return text
}

func errorOrText(err error, text string) string {
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(text)
}

func verificationSummaryOrError(session *Session, err error) string {
	if err != nil {
		return err.Error()
	}
	if session != nil && session.LastVerification != nil {
		return session.LastVerification.SummaryLine()
	}
	return "no verification report recorded"
}

func completionAuditSummaryOrError(audit CompletionAuditArtifact, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("%s ready=%t blockers=%d warnings=%d", valueOrDefault(audit.Status, "unknown"), audit.Ready, len(audit.Blockers), len(audit.Warnings))
}

func semanticReviewSummaryOrError(review GoalSemanticReview, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("%s approved=%t %s", valueOrDefault(review.Verdict, "unknown"), review.Approved, compactPromptSection(review.Feedback, 220))
}

func progressScore(progress *GoalProgressState) int {
	if progress == nil {
		return 0
	}
	return progress.Score
}

func (g *GoalState) updateUsageEstimate(session *Session) {
	if g == nil {
		return
	}
	chars := len(g.ID) + len(g.Status) + len(g.Objective) + len(g.SourcePath) + len(g.LastError)
	for _, item := range g.CompletionCriteria {
		chars += len(item)
	}
	if g.LastAudit != nil {
		chars += len(g.LastAudit.ID) + len(g.LastAudit.Status)
		for _, item := range g.LastAudit.Blockers {
			chars += len(item)
		}
		for _, item := range g.LastAudit.Warnings {
			chars += len(item)
		}
	}
	if g.LastSemanticReview != nil {
		chars += len(g.LastSemanticReview.Verdict) + len(g.LastSemanticReview.Feedback)
	}
	if g.LastProgress != nil {
		chars += len(g.LastProgress.Fingerprint) + len(g.LastProgress.Verification) + len(g.LastProgress.AuditStatus) + len(g.LastProgress.FailureSignature)
		for _, item := range g.LastProgress.Signals {
			chars += len(item)
		}
		for _, item := range g.LastProgress.ChangedFiles {
			chars += len(item)
		}
	}
	for _, command := range g.CommandHistory {
		chars += len(command.Name) + len(command.Status) + len(command.Summary)
	}
	for _, iteration := range g.Iterations {
		chars += len(iteration.Status) + len(iteration.ImplementReply) + len(iteration.ReviewReply) + len(iteration.ReviewerVerdict)
		chars += len(iteration.ReviewerFeedback) + len(iteration.RepairReply) + len(iteration.ReplySummary)
		chars += len(iteration.Verification) + len(iteration.AuditStatus) + len(iteration.RecoveryStatus) + len(iteration.RollbackStatus) + len(iteration.Error)
		if iteration.SemanticReview != nil {
			chars += len(iteration.SemanticReview.Verdict) + len(iteration.SemanticReview.Feedback)
		}
		for _, item := range iteration.ChangedFiles {
			chars += len(item)
		}
		for _, item := range iteration.Blockers {
			chars += len(item)
		}
		for _, item := range iteration.Warnings {
			chars += len(item)
		}
		for _, command := range iteration.Commands {
			chars += len(command.Name) + len(command.Status) + len(command.Summary)
		}
	}
	if session != nil {
		chars += session.ApproxChars()
	}
	if chars <= 0 {
		g.TokenUsedEstimate = 0
		return
	}
	g.TokenUsedEstimate = (chars + 3) / 4
}

func (g *GoalState) updateUsageTelemetry(session *Session) {
	if g == nil {
		return
	}
	g.updateUsageEstimate(session)
	g.updateTimeUsedSeconds(time.Now())
}

func (g *GoalState) updateTimeUsedSeconds(now time.Time) {
	if g == nil || g.CreatedAt.IsZero() {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	end := now
	if !g.CompletedAt.IsZero() {
		end = g.CompletedAt
	}
	if end.Before(g.CreatedAt) {
		return
	}
	seconds := int(end.Sub(g.CreatedAt).Seconds())
	if seconds < 0 {
		seconds = 0
	}
	if seconds > g.TimeUsedSeconds || strings.EqualFold(g.Status, goalStatusComplete) {
		g.TimeUsedSeconds = seconds
	}
}

func (p *GoalProgressState) Normalize() {
	if p == nil {
		return
	}
	if p.Score < 0 {
		p.Score = 0
	}
	p.Fingerprint = strings.TrimSpace(p.Fingerprint)
	p.Signals = normalizeTaskStateList(p.Signals, 16)
	p.ChangedFiles = normalizeTaskStateList(p.ChangedFiles, 64)
	p.Verification = strings.TrimSpace(p.Verification)
	p.AuditStatus = strings.TrimSpace(p.AuditStatus)
	if p.BlockerCount < 0 {
		p.BlockerCount = 0
	}
	if p.WarningCount < 0 {
		p.WarningCount = 0
	}
	if p.OpenTaskCount < 0 {
		p.OpenTaskCount = 0
	}
	if p.NoProgressCount < 0 {
		p.NoProgressCount = 0
	}
	p.FailureSignature = strings.TrimSpace(p.FailureSignature)
	if p.RepeatedFailures < 0 {
		p.RepeatedFailures = 0
	}
}

func (r *GoalCheckpointRef) Normalize() {
	if r == nil {
		return
	}
	r.ID = strings.TrimSpace(r.ID)
	r.Name = strings.TrimSpace(r.Name)
	r.Status = strings.TrimSpace(strings.ToLower(r.Status))
}

func (r *GoalCommandRecord) Normalize() {
	if r == nil {
		return
	}
	r.Name = strings.TrimSpace(r.Name)
	r.Status = strings.TrimSpace(strings.ToLower(r.Status))
	r.Summary = compactPromptSection(strings.TrimSpace(r.Summary), 400)
}

func normalizeGoalCheckpointRefs(items []GoalCheckpointRef, limit int) []GoalCheckpointRef {
	if len(items) == 0 {
		return nil
	}
	out := make([]GoalCheckpointRef, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item.Normalize()
		key := strings.TrimSpace(item.ID)
		if key == "" {
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	if limit > 0 && len(out) > limit {
		out = append([]GoalCheckpointRef(nil), out[len(out)-limit:]...)
	}
	return out
}

func normalizeGoalCommandRecords(items []GoalCommandRecord, limit int) []GoalCommandRecord {
	if len(items) == 0 {
		return nil
	}
	out := make([]GoalCommandRecord, 0, len(items))
	for _, item := range items {
		item.Normalize()
		if item.Name == "" && item.Summary == "" {
			continue
		}
		out = append(out, item)
	}
	if limit > 0 && len(out) > limit {
		out = append([]GoalCommandRecord(nil), out[len(out)-limit:]...)
	}
	return out
}

func limitGoalCommands(items []GoalCommandRecord, limit int) []GoalCommandRecord {
	if len(items) == 0 {
		return nil
	}
	if limit <= 0 || len(items) <= limit {
		return append([]GoalCommandRecord(nil), items...)
	}
	return append([]GoalCommandRecord(nil), items[len(items)-limit:]...)
}

func relGoalPath(root string, path string) string {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return strings.TrimSpace(path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return strings.TrimSpace(path)
	}
	return filepath.ToSlash(rel)
}
