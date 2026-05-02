package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	state := rt.session.StartTaskState(goal.Objective)
	state.SetProfiles(rt.session.Provider+" / "+rt.session.Model, rt.goalReviewerProfileLabel())
	state.SetPhase("execution")
	state.SetHypothesis("Autonomous goal runtime should inspect, implement, independently review, verify, audit, and recover until the objective is satisfied or a concrete blocker is recorded.")
	state.SetNextStep("Run the next autonomous goal iteration.")
	state.RecordEvent(conversationEventKindGoal, "", "", "Goal runtime primed.", strings.TrimSpace(reason), "active", true)
	rt.session.AcceptanceContract = goalAcceptanceContract(goal.Objective)
	criteria := goalCompletionCriteria(goal.Objective, rt.session.AcceptanceContract)
	if len(goal.CompletionCriteria) == 0 {
		goal.CompletionCriteria = criteria
	}
	rt.session.SetSharedPlan(goalPlanItems())
	rt.session.ensureSharedPlanInProgress()
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
		"Run autonomous implementation, review, verification, completion-audit, and recovery loops until ready or blocked.",
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
	if goal.TimeBudgetSeconds > 0 && !goal.CreatedAt.IsZero() && time.Since(goal.CreatedAt) > time.Duration(goal.TimeBudgetSeconds)*time.Second {
		goal.Status = goalStatusBlocked
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

	reviewReply, err := rt.runGoalReviewerReply(ctx, buildGoalReviewPrompt(goal, iteration.Index))
	iteration.ReviewReply = compactPromptSection(reviewReply, 900)
	if err != nil {
		return rt.finishGoalIterationError(goal, iteration, err)
	}
	decision := parseGoalReviewDecision(reviewReply)
	iteration.ReviewerVerdict = decision.Verdict
	iteration.ReviewerFeedback = compactPromptSection(decision.Feedback, 900)
	if decision.NeedsRevision {
		repairReply, repairErr := rt.runGoalAgentReply(ctx, buildGoalRepairPrompt(goal, iteration, decision))
		iteration.RepairReply = compactPromptSection(repairReply, 900)
		if repairErr != nil {
			return rt.finishGoalIterationError(goal, iteration, repairErr)
		}
	}
	rt.session.SetPlanNodeLifecycle("plan-03", "completed", "Review pass completed and concrete findings were repaired or cleared.")

	verifyCommand := startGoalCommand(iteration.Index, "verify", "/verify --full")
	verifyErr := rt.handleVerifyCommand("--full")
	verifyCommand.finish(statusForErr(verifyErr), verificationSummaryOrError(rt.session, verifyErr))
	iteration.Commands = append(iteration.Commands, verifyCommand)
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
			iteration.Status = goalStatusComplete
			goal.Status = goalStatusComplete
			goal.CompletedAt = time.Now()
			goal.LastError = ""
			rt.session.SetPlanNodeLifecycle("plan-06", "completed", "No recovery needed; completion audit is ready.")
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
				recoveryCommand := startGoalCommand(iteration.Index, "recover", "/recover execute-safe goal "+goal.ID)
				recoveryErr := rt.handleRecoverCommand("execute-safe goal " + goal.ID)
				recoveryCommand.finish(statusForErr(recoveryErr), errorOrText(recoveryErr, "safe recovery plan executed"))
				iteration.Commands = append(iteration.Commands, recoveryCommand)
				if recoveryErr != nil {
					iteration.RecoveryStatus = "failed: " + recoveryErr.Error()
				} else {
					iteration.RecoveryStatus = "executed"
				}
			}
		}
	}
	return rt.finishGoalIteration(goal, iteration), goalStatusTerminal(goal.Status), nil
}

func (rt *runtimeState) finishGoalIterationError(goal GoalState, iteration GoalIteration, err error) (GoalState, bool, error) {
	iteration.Error = err.Error()
	iteration.Status = goalStatusBlocked
	iteration.FinishedAt = time.Now()
	goal.Iteration = iteration.Index
	goal.Status = goalStatusBlocked
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

func (rt *runtimeState) finishGoalIteration(goal GoalState, iteration GoalIteration) GoalState {
	if iteration.Status == "" || iteration.Status == goalStatusRunning {
		iteration.Status = goal.Status
	}
	iteration.ReplySummary = compactPromptSection(strings.Join([]string{iteration.ImplementReply, iteration.ReviewReply, iteration.RepairReply}, "\n\n"), 1200)
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
	}
	return goal
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
	case containsAny(strings.ToLower(trimmed), "needs_revision", "must fix", "blocking issue", "not ready", "수정 필요", "준비되지"):
		decision.Verdict = "needs_revision"
		decision.NeedsRevision = true
	}
	return decision
}

func buildGoalRepairPrompt(goal GoalState, iteration GoalIteration, decision goalReviewDecision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Autonomous goal repair pass for iteration %d.\n\n", iteration.Index)
	fmt.Fprintf(&b, "Objective:\n%s\n\n", strings.TrimSpace(goal.Objective))
	b.WriteString("The independent review found concrete issues. Fix them directly now without asking the user.\n")
	if strings.TrimSpace(decision.Feedback) != "" {
		b.WriteString("\nReviewer feedback:\n")
		b.WriteString(decision.Feedback)
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
	if err != nil {
		return "failed"
	}
	return "passed"
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

func progressScore(progress *GoalProgressState) int {
	if progress == nil {
		return 0
	}
	return progress.Score
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
