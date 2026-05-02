package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	goalStatusPending  = "pending"
	goalStatusRunning  = "running"
	goalStatusComplete = "complete"
	goalStatusBlocked  = "blocked"
	goalStatusCanceled = "canceled"

	defaultGoalMaxIterations = 8
)

type GoalState struct {
	ID                      string              `json:"id"`
	Objective               string              `json:"objective"`
	SourcePath              string              `json:"source_path,omitempty"`
	Status                  string              `json:"status"`
	Iteration               int                 `json:"iteration"`
	MaxIterations           int                 `json:"max_iterations,omitempty"`
	TimeBudgetSeconds       int                 `json:"time_budget_seconds,omitempty"`
	TokenBudget             int                 `json:"token_budget,omitempty"`
	TokenUsedEstimate       int                 `json:"token_used_estimate,omitempty"`
	AutoRollback            bool                `json:"auto_rollback,omitempty"`
	CreatedAt               time.Time           `json:"created_at"`
	UpdatedAt               time.Time           `json:"updated_at"`
	CompletedAt             time.Time           `json:"completed_at,omitempty"`
	LastError               string              `json:"last_error,omitempty"`
	LastAudit               *GoalAuditState     `json:"last_audit,omitempty"`
	LastSemanticReview      *GoalSemanticReview `json:"last_semantic_review,omitempty"`
	LastProgress            *GoalProgressState  `json:"last_progress,omitempty"`
	LastProgressFingerprint string              `json:"last_progress_fingerprint,omitempty"`
	NoProgressCount         int                 `json:"no_progress_count,omitempty"`
	LastFailureSignature    string              `json:"last_failure_signature,omitempty"`
	RepeatedFailureCount    int                 `json:"repeated_failure_count,omitempty"`
	CompletionCriteria      []string            `json:"completion_criteria,omitempty"`
	CheckpointRefs          []GoalCheckpointRef `json:"checkpoint_refs,omitempty"`
	CommandHistory          []GoalCommandRecord `json:"command_history,omitempty"`
	Iterations              []GoalIteration     `json:"iterations,omitempty"`
	ArtifactRefs            []string            `json:"artifact_refs,omitempty"`
}

type GoalAuditState struct {
	ID       string   `json:"id,omitempty"`
	Ready    bool     `json:"ready"`
	Status   string   `json:"status,omitempty"`
	Blockers []string `json:"blockers,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type GoalSemanticReview struct {
	Verdict    string    `json:"verdict,omitempty"`
	Approved   bool      `json:"approved"`
	Feedback   string    `json:"feedback,omitempty"`
	ReviewedAt time.Time `json:"reviewed_at,omitempty"`
}

type GoalProgressState struct {
	Score            int      `json:"score,omitempty"`
	Fingerprint      string   `json:"fingerprint,omitempty"`
	Signals          []string `json:"signals,omitempty"`
	ChangedFiles     []string `json:"changed_files,omitempty"`
	Verification     string   `json:"verification,omitempty"`
	AuditReady       bool     `json:"audit_ready,omitempty"`
	AuditStatus      string   `json:"audit_status,omitempty"`
	BlockerCount     int      `json:"blocker_count,omitempty"`
	WarningCount     int      `json:"warning_count,omitempty"`
	OpenTaskCount    int      `json:"open_task_count,omitempty"`
	NoProgressCount  int      `json:"no_progress_count,omitempty"`
	FailureSignature string   `json:"failure_signature,omitempty"`
	RepeatedFailures int      `json:"repeated_failures,omitempty"`
}

type GoalCheckpointRef struct {
	Iteration int       `json:"iteration,omitempty"`
	ID        string    `json:"id,omitempty"`
	Name      string    `json:"name,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	Status    string    `json:"status,omitempty"`
}

type GoalCommandRecord struct {
	Iteration  int       `json:"iteration,omitempty"`
	Name       string    `json:"name,omitempty"`
	Status     string    `json:"status,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

type GoalIteration struct {
	Index            int                 `json:"index"`
	Status           string              `json:"status"`
	StartedAt        time.Time           `json:"started_at"`
	FinishedAt       time.Time           `json:"finished_at"`
	CheckpointID     string              `json:"checkpoint_id,omitempty"`
	CheckpointName   string              `json:"checkpoint_name,omitempty"`
	ImplementReply   string              `json:"implement_reply,omitempty"`
	ReviewReply      string              `json:"review_reply,omitempty"`
	ReviewerVerdict  string              `json:"reviewer_verdict,omitempty"`
	ReviewerFeedback string              `json:"reviewer_feedback,omitempty"`
	SemanticReview   *GoalSemanticReview `json:"semantic_review,omitempty"`
	RepairReply      string              `json:"repair_reply,omitempty"`
	ReplySummary     string              `json:"reply_summary,omitempty"`
	Verification     string              `json:"verification,omitempty"`
	AuditID          string              `json:"audit_id,omitempty"`
	AuditReady       bool                `json:"audit_ready,omitempty"`
	AuditStatus      string              `json:"audit_status,omitempty"`
	Progress         *GoalProgressState  `json:"progress,omitempty"`
	ChangedFiles     []string            `json:"changed_files,omitempty"`
	Blockers         []string            `json:"blockers,omitempty"`
	Warnings         []string            `json:"warnings,omitempty"`
	Commands         []GoalCommandRecord `json:"commands,omitempty"`
	RecoveryStatus   string              `json:"recovery_status,omitempty"`
	RollbackStatus   string              `json:"rollback_status,omitempty"`
	Error            string              `json:"error,omitempty"`
}

type goalStartOptions struct {
	Objective         string
	SourcePath        string
	Run               bool
	MaxIterations     int
	TimeBudgetSeconds int
	TokenBudget       int
	AutoRollback      bool
}

func (rt *runtimeState) handleGoalCommand(args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	fields := splitGoalFields(args)
	if len(fields) == 0 {
		return rt.printGoalStatus("")
	}
	action := strings.ToLower(strings.TrimSpace(fields[0]))
	if !isGoalCommandAction(action) {
		return rt.handleGoalStart(fields)
	}
	switch action {
	case "start", "create", "new":
		return rt.handleGoalStart(fields[1:])
	case "run", "resume", "continue":
		selector := ""
		if len(fields) > 1 {
			selector = fields[1]
		}
		return rt.runGoalBySelector(selector, 0)
	case "status", "show", "list":
		selector := ""
		if len(fields) > 1 {
			selector = fields[1]
		}
		return rt.printGoalStatus(selector)
	case "audit":
		selector := ""
		if len(fields) > 1 {
			selector = fields[1]
		}
		return rt.auditGoalBySelector(selector)
	case "complete", "done":
		selector := ""
		if len(fields) > 1 {
			selector = fields[1]
		}
		return rt.completeGoalBySelector(selector)
	case "cancel", "stop":
		selector := ""
		if len(fields) > 1 {
			selector = fields[1]
		}
		return rt.cancelGoalBySelector(selector)
	default:
		return fmt.Errorf("unsupported /goal action: %s", action)
	}
}

func isGoalCommandAction(action string) bool {
	switch strings.TrimSpace(strings.ToLower(action)) {
	case "start", "create", "new", "run", "resume", "continue", "status", "show", "list", "audit", "complete", "done", "cancel", "stop":
		return true
	default:
		return false
	}
}

func (rt *runtimeState) handleGoalStart(fields []string) error {
	options, err := rt.parseGoalStartOptions(fields)
	if err != nil {
		return err
	}
	if strings.TrimSpace(options.Objective) == "" {
		return fmt.Errorf("usage: /goal start [--file GOAL.md|@GOAL.md] [--no-run] [--max-iterations N] <objective>")
	}
	now := time.Now()
	goal := GoalState{
		ID:                fmt.Sprintf("goal-%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/1_000_000),
		Objective:         strings.TrimSpace(options.Objective),
		SourcePath:        strings.TrimSpace(options.SourcePath),
		Status:            goalStatusPending,
		MaxIterations:     options.MaxIterations,
		TimeBudgetSeconds: options.TimeBudgetSeconds,
		TokenBudget:       options.TokenBudget,
		AutoRollback:      options.AutoRollback,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	goal.Normalize()
	rt.primeGoalRuntimeState(&goal, "created")
	goal.updateUsageEstimate(rt.session)
	rt.session.UpsertGoal(goal)
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindGoal,
		Severity: conversationSeverityInfo,
		Summary:  "goal created: " + compactPromptSection(goal.Objective, 120),
		Entities: map[string]string{
			"goal":   goal.ID,
			"status": goal.Status,
		},
	})
	if err := rt.writeGoalArtifacts(goal); err != nil {
		return err
	}
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Created goal: "+goal.ID))
	if !options.Run {
		fmt.Fprintln(rt.writer, rt.ui.hintLine("Run it with /goal run "+goal.ID))
		return nil
	}
	return rt.runGoalBySelector(goal.ID, options.MaxIterations)
}

func (rt *runtimeState) parseGoalStartOptions(fields []string) (goalStartOptions, error) {
	options := goalStartOptions{
		Run:           true,
		MaxIterations: defaultGoalMaxIterations,
	}
	objectiveParts := []string{}
	for i := 0; i < len(fields); i++ {
		field := strings.TrimSpace(fields[i])
		if field == "" {
			continue
		}
		switch field {
		case "--no-run":
			options.Run = false
		case "--run":
			options.Run = true
		case "--rollback-on-regression":
			options.AutoRollback = true
		case "--no-rollback":
			options.AutoRollback = false
		case "--until-complete":
			options.MaxIterations = 0
		case "--file", "-f":
			if i+1 >= len(fields) {
				return options, fmt.Errorf("%s requires a file path", field)
			}
			i++
			options.SourcePath = fields[i]
		case "--max-iterations", "--iterations":
			if i+1 >= len(fields) {
				return options, fmt.Errorf("%s requires a number", field)
			}
			i++
			value, err := strconv.Atoi(strings.TrimSpace(fields[i]))
			if err != nil || value < 0 {
				return options, fmt.Errorf("invalid max iterations: %s", fields[i])
			}
			options.MaxIterations = value
		case "--time-budget", "--time-budget-seconds":
			if i+1 >= len(fields) {
				return options, fmt.Errorf("%s requires seconds or a Go duration", field)
			}
			i++
			value, err := parseGoalTimeBudgetSeconds(fields[i])
			if err != nil {
				return options, err
			}
			options.TimeBudgetSeconds = value
		case "--token-budget", "--tokens":
			if i+1 >= len(fields) {
				return options, fmt.Errorf("%s requires a positive token budget", field)
			}
			i++
			value, err := strconv.Atoi(strings.TrimSpace(fields[i]))
			if err != nil || value < 0 {
				return options, fmt.Errorf("invalid token budget: %s", fields[i])
			}
			options.TokenBudget = value
		default:
			if strings.HasPrefix(field, "@") && options.SourcePath == "" {
				candidate := strings.TrimPrefix(field, "@")
				if goalFileExists(rt.workspace.Root, candidate) {
					options.SourcePath = candidate
					continue
				}
			}
			if options.SourcePath == "" && len(fields) == 1 && goalFileExists(rt.workspace.Root, field) {
				options.SourcePath = field
				continue
			}
			objectiveParts = append(objectiveParts, field)
		}
	}
	inlineObjective := strings.TrimSpace(strings.Join(objectiveParts, " "))
	fileObjective := ""
	if strings.TrimSpace(options.SourcePath) != "" {
		content, resolved, err := readGoalObjectiveFile(rt.workspace.Root, options.SourcePath)
		if err != nil {
			return options, err
		}
		options.SourcePath = resolved
		fileObjective = strings.TrimSpace(content)
	}
	switch {
	case fileObjective != "" && inlineObjective != "":
		options.Objective = fileObjective + "\n\nAdditional objective:\n" + inlineObjective
	case fileObjective != "":
		options.Objective = fileObjective
	default:
		options.Objective = inlineObjective
	}
	return options, nil
}

func (rt *runtimeState) runGoalBySelector(selector string, maxIterationsOverride int) error {
	index, ok := rt.session.GoalIndex(selector)
	if !ok {
		return fmt.Errorf("goal not found: %s", valueOrDefault(selector, "latest"))
	}
	goal := rt.session.Goals[index]
	if goalStatusTerminal(goal.Status) {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("Goal is already "+goal.Status+": "+goal.ID))
		return nil
	}
	if maxIterationsOverride > 0 {
		goal.MaxIterations = maxIterationsOverride
	} else if goal.MaxIterations < 0 {
		goal.MaxIterations = defaultGoalMaxIterations
	}
	goal.Status = goalStatusRunning
	goal.Touch()
	rt.primeGoalRuntimeState(&goal, "run")
	rt.session.UpsertGoal(goal)
	if rt.store != nil {
		_ = rt.store.Save(rt.session)
	}
	if err := rt.writeGoalArtifacts(goal); err != nil {
		return err
	}
	if rt.clientErr != nil && rt.goalReply == nil {
		goal.Status = goalStatusBlocked
		goal.LastError = rt.clientErr.Error()
		goal.Touch()
		rt.session.UpsertGoal(goal)
		_ = rt.writeGoalArtifacts(goal)
		if rt.store != nil {
			_ = rt.store.Save(rt.session)
		}
		return rt.clientErr
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Goal"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("id", goal.ID))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("mode", "autonomous"))
	return rt.withAutonomousGoalPermissions(func() error {
		return rt.runGoalLoop(context.Background(), goal.ID)
	})
}

func (rt *runtimeState) runGoalLoop(ctx context.Context, goalID string) error {
	for {
		index, ok := rt.session.GoalIndex(goalID)
		if !ok {
			return fmt.Errorf("goal disappeared: %s", goalID)
		}
		goal := rt.session.Goals[index]
		if goalStatusTerminal(goal.Status) {
			return nil
		}
		if goal.MaxIterations > 0 && goal.Iteration >= goal.MaxIterations {
			goal.Status = goalStatusBlocked
			goal.LastError = fmt.Sprintf("goal reached max iterations (%d) without approved completion gates", goal.MaxIterations)
			goal.Touch()
			rt.session.UpsertGoal(goal)
			_ = rt.writeGoalArtifacts(goal)
			if rt.store != nil {
				_ = rt.store.Save(rt.session)
			}
			fmt.Fprintln(rt.writer, rt.ui.warnLine(goal.LastError))
			return nil
		}
		updated, done, err := rt.runGoalIteration(ctx, goal)
		if err != nil {
			return err
		}
		goal = updated
		if done {
			return nil
		}
	}
}

func (rt *runtimeState) runGoalAgentReply(ctx context.Context, prompt string) (string, error) {
	if rt.goalReply != nil {
		return rt.goalReply(ctx, prompt)
	}
	return rt.runAgentReply(ctx, prompt)
}

func (rt *runtimeState) runGoalReviewerReply(ctx context.Context, prompt string) (string, error) {
	if rt == nil {
		return "", fmt.Errorf("no active runtime")
	}
	if rt.goalReply != nil {
		return rt.goalReply(ctx, prompt)
	}
	if rt.agent == nil {
		return rt.runGoalAgentReply(ctx, prompt)
	}
	client, model := rt.agent.ensureInteractiveReviewerClient()
	if client == nil || strings.TrimSpace(model) == "" {
		return rt.runGoalAgentReply(ctx, prompt)
	}
	resp, err := rt.agent.completeModelTurnWithClient(ctx, client, ChatRequest{
		Model: model,
		System: strings.Join([]string{
			"You are an independent goal reviewer for a coding agent.",
			"Review the actual workspace state against the goal.",
			"Start with APPROVED or NEEDS_REVISION.",
			"If revision is needed, identify exact fixes or verification gaps.",
		}, "\n"),
		Messages: []Message{{
			Role: "user",
			Text: prompt,
		}},
		MaxTokens:   min(768, max(256, rt.cfg.MaxTokens/3)),
		Temperature: 0.1,
		WorkingDir:  rt.session.WorkingDir,
	})
	if err != nil {
		return rt.runGoalAgentReply(ctx, prompt+"\n\nReviewer provider failed: "+err.Error()+"\nRun the review/fix pass with the main agent now.")
	}
	return strings.TrimSpace(resp.Message.Text), nil
}

func buildGoalImplementationPrompt(goal GoalState, iteration int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Autonomous goal iteration %d.\n\n", iteration)
	fmt.Fprintf(&b, "Objective:\n%s\n\n", strings.TrimSpace(goal.Objective))
	b.WriteString("Run this as a Codex-style goal loop without asking the user for intervention.\n")
	b.WriteString("Required behavior:\n")
	b.WriteString("1. Inspect the repository and current task state.\n")
	b.WriteString("2. Implement or modify the code and docs needed to satisfy the objective.\n")
	b.WriteString("3. Use tools directly; do not hand patches back to the user.\n")
	b.WriteString("4. Keep user changes intact and avoid unrelated refactors.\n")
	b.WriteString("5. Run narrow tests or checks when useful before you return.\n")
	b.WriteString("6. If you find a blocker, record the exact blocker and the next repair action.\n\n")
	if len(goal.CompletionCriteria) > 0 {
		b.WriteString("Completion criteria:\n")
		for _, item := range goal.CompletionCriteria {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	if goal.LastProgress != nil {
		b.WriteString("Latest progress ledger:\n")
		fmt.Fprintf(&b, "- Score: %d\n", goal.LastProgress.Score)
		fmt.Fprintf(&b, "- No-progress count: %d\n", goal.NoProgressCount)
		fmt.Fprintf(&b, "- Repeated failure count: %d\n", goal.RepeatedFailureCount)
		for _, signal := range limitStrings(goal.LastProgress.Signals, 6) {
			fmt.Fprintf(&b, "- Signal: %s\n", signal)
		}
		b.WriteString("\n")
	}
	if goal.LastAudit != nil && (!goal.LastAudit.Ready || len(goal.LastAudit.Blockers) > 0 || len(goal.LastAudit.Warnings) > 0) {
		b.WriteString("Latest completion audit state:\n")
		fmt.Fprintf(&b, "- Status: %s ready=%t\n", goal.LastAudit.Status, goal.LastAudit.Ready)
		for _, blocker := range limitStrings(goal.LastAudit.Blockers, 6) {
			fmt.Fprintf(&b, "- Blocker: %s\n", blocker)
		}
		for _, warning := range limitStrings(goal.LastAudit.Warnings, 6) {
			fmt.Fprintf(&b, "- Warning: %s\n", warning)
		}
		b.WriteString("\n")
	}
	if goal.LastSemanticReview != nil && !goal.LastSemanticReview.Approved {
		b.WriteString("Latest final semantic review:\n")
		fmt.Fprintf(&b, "- Verdict: %s\n", valueOrUnset(goal.LastSemanticReview.Verdict))
		if goal.LastSemanticReview.Feedback != "" {
			fmt.Fprintf(&b, "- Feedback: %s\n", compactPromptSection(goal.LastSemanticReview.Feedback, 700))
		}
		b.WriteString("\n")
	}
	b.WriteString("Return a concise engineering status only after you have made the concrete next change or verified no change is needed.")
	return b.String()
}

func buildGoalReviewPrompt(goal GoalState, iteration int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Autonomous goal independent review pass for iteration %d.\n\n", iteration)
	fmt.Fprintf(&b, "Objective:\n%s\n\n", strings.TrimSpace(goal.Objective))
	b.WriteString("Review the changes and implementation state as if doing a bug-finding code review.\n")
	b.WriteString("Required behavior:\n")
	b.WriteString("1. Inspect the actual diff or relevant files.\n")
	b.WriteString("2. Look for correctness, security, stability, missing tests, and documentation gaps.\n")
	b.WriteString("3. Start with APPROVED if the implementation can proceed to verification, or NEEDS_REVISION if a concrete fix is still required.\n")
	b.WriteString("4. Do not ask the user whether to proceed.\n")
	b.WriteString("5. Do not claim completion unless the state is ready for /verify and /completion-audit.\n")
	if len(goal.CompletionCriteria) > 0 {
		b.WriteString("\nCompletion criteria:\n")
		for _, item := range goal.CompletionCriteria {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	return b.String()
}

func (rt *runtimeState) runGoalCompletionAudit(goal GoalState) (CompletionAuditArtifact, error) {
	if err := rt.handleCompletionAuditCommand(goal.Objective); err != nil {
		return CompletionAuditArtifact{}, err
	}
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = rt.session.WorkingDir
	}
	path := filepath.Join(root, userConfigDirName, "completion_audit", "latest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return CompletionAuditArtifact{}, err
	}
	artifact := CompletionAuditArtifact{}
	if err := json.Unmarshal(data, &artifact); err != nil {
		return CompletionAuditArtifact{}, err
	}
	return artifact, nil
}

func (rt *runtimeState) auditGoalBySelector(selector string) error {
	index, ok := rt.session.GoalIndex(selector)
	if !ok {
		return fmt.Errorf("goal not found: %s", valueOrDefault(selector, "latest"))
	}
	goal := rt.session.Goals[index]
	rt.primeGoalRuntimeState(&goal, "audit")
	rt.session.UpsertGoal(goal)
	audit, err := rt.runGoalCompletionAudit(goal)
	if err != nil {
		return err
	}
	goal.LastAudit = &GoalAuditState{
		ID:       audit.ID,
		Ready:    audit.Ready,
		Status:   audit.Status,
		Blockers: append([]string(nil), audit.Blockers...),
		Warnings: append([]string(nil), audit.Warnings...),
	}
	if audit.Ready {
		if goal.LastSemanticReview != nil && !goal.LastSemanticReview.Approved && !goalStatusTerminal(goal.Status) {
			goal.Status = goalStatusBlocked
			goal.LastError = "goal still needs final semantic approval: " + compactPromptSection(goal.LastSemanticReview.Feedback, 500)
		} else if !goalStatusTerminal(goal.Status) {
			goal.LastError = ""
			goal.Status = goalStatusPending
		}
	} else if !goalStatusTerminal(goal.Status) {
		goal.Status = goalStatusBlocked
		goal.LastError = completionAuditSummaryOrError(audit, nil)
	}
	goal.updateUsageEstimate(rt.session)
	goal.Touch()
	rt.session.UpsertGoal(goal)
	if err := rt.writeGoalArtifacts(goal); err != nil {
		return err
	}
	if rt.store != nil {
		return rt.store.Save(rt.session)
	}
	return nil
}

func (rt *runtimeState) completeGoalBySelector(selector string) error {
	index, ok := rt.session.GoalIndex(selector)
	if !ok {
		return fmt.Errorf("goal not found: %s", valueOrDefault(selector, "latest"))
	}
	goal := rt.session.Goals[index]
	if strings.EqualFold(goal.Status, goalStatusCanceled) {
		return fmt.Errorf("cannot complete canceled goal: %s", goal.ID)
	}
	if strings.EqualFold(goal.Status, goalStatusComplete) {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("Goal is already complete: "+goal.ID))
		return nil
	}
	rt.primeGoalRuntimeState(&goal, "complete")
	rt.session.UpsertGoal(goal)
	goal.updateUsageEstimate(rt.session)
	if goal.TokenBudget > 0 && goal.TokenUsedEstimate > goal.TokenBudget {
		goal.Status = goalStatusBlocked
		goal.LastError = fmt.Sprintf("goal exceeded token budget estimate (%d > %d)", goal.TokenUsedEstimate, goal.TokenBudget)
		goal.Touch()
		rt.session.UpsertGoal(goal)
		_ = rt.writeGoalArtifacts(goal)
		if rt.store != nil {
			_ = rt.store.Save(rt.session)
		}
		return fmt.Errorf("%s", goal.LastError)
	}
	commands := []GoalCommandRecord{}
	auditCommand := startGoalCommand(goal.Iteration, "completion-audit", "/completion-audit <goal objective>")
	audit, auditErr := rt.runGoalCompletionAudit(goal)
	auditCommand.finish(statusForErr(auditErr), completionAuditSummaryOrError(audit, auditErr))
	commands = append(commands, auditCommand)
	if auditErr != nil {
		goal.Status = goalStatusBlocked
		goal.LastError = auditErr.Error()
	} else {
		goal.LastAudit = goalAuditStateFromArtifact(audit)
		if !audit.Ready {
			goal.Status = goalStatusBlocked
			goal.LastError = fmt.Sprintf("goal cannot be marked complete: %s", completionAuditSummaryOrError(audit, nil))
		} else {
			iteration := GoalIteration{
				Index:        goal.Iteration,
				Status:       goal.Status,
				Verification: verificationSummaryOrError(rt.session, nil),
				AuditID:      audit.ID,
				AuditReady:   audit.Ready,
				AuditStatus:  audit.Status,
				Blockers:     append([]string(nil), audit.Blockers...),
				Warnings:     append([]string(nil), audit.Warnings...),
			}
			if goal.LastProgress != nil {
				iteration.Progress = goal.LastProgress
				iteration.ChangedFiles = append([]string(nil), goal.LastProgress.ChangedFiles...)
			}
			semanticCommand := startGoalCommand(goal.Iteration, "semantic-review", "independent semantic goal review")
			semanticReview, semanticErr := rt.runGoalSemanticReview(context.Background(), goal, audit, iteration)
			semanticCommand.finish(statusForErr(semanticErr), semanticReviewSummaryOrError(semanticReview, semanticErr))
			commands = append(commands, semanticCommand)
			if semanticErr != nil {
				goal.Status = goalStatusBlocked
				goal.LastError = semanticErr.Error()
			} else {
				goal.LastSemanticReview = &semanticReview
				if semanticReview.Approved {
					goal.Status = goalStatusComplete
					goal.CompletedAt = time.Now()
					goal.LastError = ""
					rt.session.SetPlanNodeLifecycle("plan-06", "completed", "Goal was explicitly completed after audit and semantic review.")
				} else {
					goal.Status = goalStatusBlocked
					goal.LastError = "goal cannot be marked complete: " + compactPromptSection(semanticReview.Feedback, 500)
					rt.session.SetPlanNodeLifecycle("plan-06", "blocked", goal.LastError)
				}
			}
		}
	}
	goal.CommandHistory = append(goal.CommandHistory, commands...)
	goal.updateUsageEstimate(rt.session)
	goal.Touch()
	rt.session.UpsertGoal(goal)
	if err := rt.writeGoalArtifacts(goal); err != nil {
		return err
	}
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	if goal.Status == goalStatusComplete {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Goal complete: "+goal.ID))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.warnLine("Goal not complete: "+goal.LastError))
	return fmt.Errorf("%s", goal.LastError)
}

func (rt *runtimeState) cancelGoalBySelector(selector string) error {
	index, ok := rt.session.GoalIndex(selector)
	if !ok {
		return fmt.Errorf("goal not found: %s", valueOrDefault(selector, "latest"))
	}
	goal := rt.session.Goals[index]
	goal.Status = goalStatusCanceled
	goal.Touch()
	rt.session.UpsertGoal(goal)
	if err := rt.writeGoalArtifacts(goal); err != nil {
		return err
	}
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.infoLine("Goal canceled: "+goal.ID))
	return nil
}

func (rt *runtimeState) printGoalStatus(selector string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	rt.session.normalizeGoals()
	if strings.EqualFold(strings.TrimSpace(selector), "list") || (strings.TrimSpace(selector) == "" && len(rt.session.Goals) > 1) {
		fmt.Fprintln(rt.writer, rt.ui.section("Goals"))
		if len(rt.session.Goals) == 0 {
			fmt.Fprintln(rt.writer, rt.ui.hintLine("No goals recorded. Use /goal start <objective> or /goal start @GOAL.md."))
			return nil
		}
		for _, goal := range rt.session.Goals {
			fmt.Fprintf(rt.writer, "- %s [%s] iteration=%d objective=%s\n", goal.ID, goal.Status, goal.Iteration, compactPromptSection(goal.Objective, 100))
		}
		return nil
	}
	index, ok := rt.session.GoalIndex(selector)
	if !ok {
		if len(rt.session.Goals) == 0 {
			fmt.Fprintln(rt.writer, rt.ui.hintLine("No goals recorded. Use /goal start <objective> or /goal start @GOAL.md."))
			return nil
		}
		return fmt.Errorf("goal not found: %s", valueOrDefault(selector, "latest"))
	}
	goal := rt.session.Goals[index]
	fmt.Fprintln(rt.writer, rt.ui.section("Goal"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("id", goal.ID))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("status", goal.Status))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("iteration", fmt.Sprintf("%d", goal.Iteration)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("max_iterations", goalMaxIterationsLabel(goal.MaxIterations)))
	if goal.TimeBudgetSeconds > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("time_budget", fmt.Sprintf("%ds", goal.TimeBudgetSeconds)))
	}
	if goal.TokenBudget > 0 || goal.TokenUsedEstimate > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("token_budget", goalTokenBudgetLabel(goal.TokenBudget)))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("token_used_estimate", fmt.Sprintf("%d", goal.TokenUsedEstimate)))
	}
	if goal.LastProgress != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("progress_score", fmt.Sprintf("%d", goal.LastProgress.Score)))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("no_progress", fmt.Sprintf("%d", goal.NoProgressCount)))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("repeated_failure", fmt.Sprintf("%d", goal.RepeatedFailureCount)))
	}
	if goal.SourcePath != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("source", goal.SourcePath))
	}
	if goal.LastAudit != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("audit", fmt.Sprintf("%s ready=%t", valueOrUnset(goal.LastAudit.Status), goal.LastAudit.Ready)))
	}
	if goal.LastSemanticReview != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("semantic_review", fmt.Sprintf("%s approved=%t", valueOrUnset(goal.LastSemanticReview.Verdict), goal.LastSemanticReview.Approved)))
	}
	if goal.LastError != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("last_error", goal.LastError))
	}
	if len(goal.CheckpointRefs) > 0 {
		lastCheckpoint := goal.CheckpointRefs[len(goal.CheckpointRefs)-1]
		fmt.Fprintln(rt.writer, rt.ui.statusKV("last_checkpoint", lastCheckpoint.ID))
	}
	if len(goal.ArtifactRefs) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("artifacts", strings.Join(goal.ArtifactRefs, ", ")))
	}
	fmt.Fprintln(rt.writer, rt.ui.statusKV("objective", compactPromptSection(goal.Objective, 260)))
	return nil
}

func goalMaxIterationsLabel(max int) string {
	if max == 0 {
		return "until-complete"
	}
	return fmt.Sprintf("%d", max)
}

func goalTokenBudgetLabel(max int) string {
	if max <= 0 {
		return "unset"
	}
	return fmt.Sprintf("%d", max)
}

func (rt *runtimeState) writeGoalArtifacts(goal GoalState) error {
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" && rt.session != nil {
		root = rt.session.WorkingDir
	}
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("workspace root is not configured")
	}
	outDir := filepath.Join(root, userConfigDirName, "goals")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	jsonPath := filepath.Join(outDir, "latest.json")
	mdPath := filepath.Join(outDir, "latest.md")
	idJSONPath := filepath.Join(outDir, goal.ID+".json")
	idMDPath := filepath.Join(outDir, goal.ID+".md")
	goal.ArtifactRefs = []string{mdPath, jsonPath, idMDPath, idJSONPath}
	data, err := json.MarshalIndent(goal, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(idJSONPath, data, 0o644); err != nil {
		return err
	}
	markdown := []byte(renderGoalMarkdown(goal))
	if err := os.WriteFile(mdPath, markdown, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(idMDPath, markdown, 0o644); err != nil {
		return err
	}
	rt.session.UpsertGoal(goal)
	return nil
}

func renderGoalMarkdown(goal GoalState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Goal\n\n")
	fmt.Fprintf(&b, "ID: %s\n", goal.ID)
	fmt.Fprintf(&b, "Status: %s\n", goal.Status)
	fmt.Fprintf(&b, "Iteration: %d\n", goal.Iteration)
	fmt.Fprintf(&b, "Max iterations: %s\n", goalMaxIterationsLabel(goal.MaxIterations))
	if goal.TimeBudgetSeconds > 0 {
		fmt.Fprintf(&b, "Time budget: %ds\n", goal.TimeBudgetSeconds)
	}
	if goal.TokenBudget > 0 || goal.TokenUsedEstimate > 0 {
		fmt.Fprintf(&b, "Token budget: %s\n", goalTokenBudgetLabel(goal.TokenBudget))
		fmt.Fprintf(&b, "Token used estimate: %d\n", goal.TokenUsedEstimate)
	}
	if goal.AutoRollback {
		fmt.Fprintf(&b, "Auto rollback: true\n")
	}
	fmt.Fprintf(&b, "Created: %s\n", goal.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Updated: %s\n", goal.UpdatedAt.Format(time.RFC3339))
	if !goal.CompletedAt.IsZero() {
		fmt.Fprintf(&b, "Completed: %s\n", goal.CompletedAt.Format(time.RFC3339))
	}
	if goal.SourcePath != "" {
		fmt.Fprintf(&b, "Source: %s\n", goal.SourcePath)
	}
	fmt.Fprintf(&b, "\n## Objective\n\n%s\n", strings.TrimSpace(goal.Objective))
	if len(goal.CompletionCriteria) > 0 {
		fmt.Fprintf(&b, "\n## Completion Criteria\n\n")
		for _, item := range goal.CompletionCriteria {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if goal.LastProgress != nil {
		fmt.Fprintf(&b, "\n## Progress Ledger\n\n")
		fmt.Fprintf(&b, "- Score: %d\n", goal.LastProgress.Score)
		fmt.Fprintf(&b, "- No-progress count: %d\n", goal.NoProgressCount)
		fmt.Fprintf(&b, "- Repeated failure count: %d\n", goal.RepeatedFailureCount)
		for _, signal := range goal.LastProgress.Signals {
			fmt.Fprintf(&b, "- Signal: %s\n", signal)
		}
	}
	if goal.LastAudit != nil {
		fmt.Fprintf(&b, "\n## Latest Audit\n\n")
		fmt.Fprintf(&b, "- ID: %s\n", valueOrUnset(goal.LastAudit.ID))
		fmt.Fprintf(&b, "- Status: %s\n", valueOrUnset(goal.LastAudit.Status))
		fmt.Fprintf(&b, "- Ready: %t\n", goal.LastAudit.Ready)
		for _, blocker := range goal.LastAudit.Blockers {
			fmt.Fprintf(&b, "- Blocker: %s\n", blocker)
		}
		for _, warning := range goal.LastAudit.Warnings {
			fmt.Fprintf(&b, "- Warning: %s\n", warning)
		}
	}
	if goal.LastSemanticReview != nil {
		fmt.Fprintf(&b, "\n## Latest Semantic Review\n\n")
		fmt.Fprintf(&b, "- Verdict: %s\n", valueOrUnset(goal.LastSemanticReview.Verdict))
		fmt.Fprintf(&b, "- Approved: %t\n", goal.LastSemanticReview.Approved)
		if !goal.LastSemanticReview.ReviewedAt.IsZero() {
			fmt.Fprintf(&b, "- Reviewed: %s\n", goal.LastSemanticReview.ReviewedAt.Format(time.RFC3339))
		}
		if goal.LastSemanticReview.Feedback != "" {
			fmt.Fprintf(&b, "\n%s\n", goal.LastSemanticReview.Feedback)
		}
	}
	if goal.LastError != "" {
		fmt.Fprintf(&b, "\n## Last Error\n\n%s\n", goal.LastError)
	}
	if len(goal.CheckpointRefs) > 0 {
		fmt.Fprintf(&b, "\n## Checkpoints\n\n")
		for _, ref := range goal.CheckpointRefs {
			fmt.Fprintf(&b, "- Iteration %d: %s [%s] %s\n", ref.Iteration, valueOrUnset(ref.ID), valueOrUnset(ref.Status), ref.Name)
		}
	}
	if len(goal.Iterations) > 0 {
		fmt.Fprintf(&b, "\n## Iterations\n\n")
		for _, iteration := range goal.Iterations {
			fmt.Fprintf(&b, "### %d. %s\n\n", iteration.Index, valueOrUnset(iteration.Status))
			if iteration.CheckpointID != "" {
				fmt.Fprintf(&b, "- Checkpoint: %s (%s)\n", iteration.CheckpointID, iteration.CheckpointName)
			}
			if iteration.ReviewerVerdict != "" {
				fmt.Fprintf(&b, "- Reviewer: %s\n", iteration.ReviewerVerdict)
			}
			if iteration.SemanticReview != nil {
				fmt.Fprintf(&b, "- Semantic review: %s approved=%t\n", valueOrUnset(iteration.SemanticReview.Verdict), iteration.SemanticReview.Approved)
			}
			if iteration.Verification != "" {
				fmt.Fprintf(&b, "- Verification: %s\n", iteration.Verification)
			}
			if iteration.AuditStatus != "" {
				fmt.Fprintf(&b, "- Audit: %s ready=%t\n", iteration.AuditStatus, iteration.AuditReady)
			}
			if iteration.Progress != nil {
				fmt.Fprintf(&b, "- Progress score: %d\n", iteration.Progress.Score)
				for _, signal := range iteration.Progress.Signals {
					fmt.Fprintf(&b, "- Progress: %s\n", signal)
				}
			}
			if iteration.RecoveryStatus != "" {
				fmt.Fprintf(&b, "- Recovery: %s\n", iteration.RecoveryStatus)
			}
			if iteration.RollbackStatus != "" {
				fmt.Fprintf(&b, "- Rollback: %s\n", iteration.RollbackStatus)
			}
			if iteration.Error != "" {
				fmt.Fprintf(&b, "- Error: %s\n", iteration.Error)
			}
			for _, blocker := range iteration.Blockers {
				fmt.Fprintf(&b, "- Blocker: %s\n", blocker)
			}
			for _, warning := range iteration.Warnings {
				fmt.Fprintf(&b, "- Warning: %s\n", warning)
			}
			if iteration.ReplySummary != "" {
				fmt.Fprintf(&b, "\n%s\n\n", iteration.ReplySummary)
			}
		}
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func (rt *runtimeState) withAutonomousGoalPermissions(fn func() error) error {
	prevWrites := rt.alwaysApproveWrites
	prevPreview := rt.alwaysApprovePreview
	rt.alwaysApproveWrites = true
	rt.alwaysApprovePreview = true
	var prevMode Mode
	if rt.perms != nil {
		prevMode = rt.perms.Mode()
		rt.perms.SetMode(ModeBypass)
	}
	defer func() {
		rt.alwaysApproveWrites = prevWrites
		rt.alwaysApprovePreview = prevPreview
		if rt.perms != nil {
			rt.perms.SetMode(prevMode)
		}
	}()
	return fn()
}

func (s *Session) normalizeGoals() {
	if s == nil {
		return
	}
	filtered := make([]GoalState, 0, len(s.Goals))
	for _, goal := range s.Goals {
		goal.Normalize()
		if strings.TrimSpace(goal.ID) == "" || strings.TrimSpace(goal.Objective) == "" {
			continue
		}
		filtered = append(filtered, goal)
	}
	s.Goals = filtered
	if strings.TrimSpace(s.ActiveGoalID) == "" && len(s.Goals) > 0 {
		s.ActiveGoalID = s.Goals[len(s.Goals)-1].ID
	}
	if strings.TrimSpace(s.ActiveGoalID) != "" {
		if _, ok := s.GoalIndex(s.ActiveGoalID); !ok && len(s.Goals) > 0 {
			s.ActiveGoalID = s.Goals[len(s.Goals)-1].ID
		}
	}
}

func (s *Session) ActiveGoal() (GoalState, bool) {
	if s == nil {
		return GoalState{}, false
	}
	index, ok := s.GoalIndex(s.ActiveGoalID)
	if !ok {
		return GoalState{}, false
	}
	return s.Goals[index], true
}

func (s *Session) GoalIndex(selector string) (int, bool) {
	if s == nil {
		return -1, false
	}
	selector = strings.TrimSpace(selector)
	if selector == "" || strings.EqualFold(selector, "latest") || strings.EqualFold(selector, "active") {
		if strings.TrimSpace(s.ActiveGoalID) != "" {
			for i := range s.Goals {
				if strings.EqualFold(s.Goals[i].ID, s.ActiveGoalID) {
					return i, true
				}
			}
		}
		if len(s.Goals) > 0 {
			return len(s.Goals) - 1, true
		}
		return -1, false
	}
	for i := range s.Goals {
		if strings.EqualFold(s.Goals[i].ID, selector) {
			return i, true
		}
	}
	return -1, false
}

func (s *Session) UpsertGoal(goal GoalState) {
	if s == nil {
		return
	}
	goal.Normalize()
	if strings.TrimSpace(goal.ID) == "" || strings.TrimSpace(goal.Objective) == "" {
		return
	}
	for i := range s.Goals {
		if strings.EqualFold(s.Goals[i].ID, goal.ID) {
			s.Goals[i] = goal
			s.ActiveGoalID = goal.ID
			s.UpdatedAt = time.Now()
			return
		}
	}
	s.Goals = append(s.Goals, goal)
	s.ActiveGoalID = goal.ID
	s.UpdatedAt = time.Now()
}

func (g *GoalState) Normalize() {
	if g == nil {
		return
	}
	g.ID = strings.TrimSpace(g.ID)
	g.Objective = strings.TrimSpace(g.Objective)
	g.SourcePath = strings.TrimSpace(g.SourcePath)
	g.Status = strings.TrimSpace(strings.ToLower(g.Status))
	if g.Status == "" {
		g.Status = goalStatusPending
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	if g.UpdatedAt.IsZero() {
		g.UpdatedAt = g.CreatedAt
	}
	if g.MaxIterations < 0 {
		g.MaxIterations = defaultGoalMaxIterations
	}
	if g.TimeBudgetSeconds < 0 {
		g.TimeBudgetSeconds = 0
	}
	if g.TokenBudget < 0 {
		g.TokenBudget = 0
	}
	if g.TokenUsedEstimate < 0 {
		g.TokenUsedEstimate = 0
	}
	g.CompletionCriteria = normalizeTaskStateList(g.CompletionCriteria, 16)
	for i := range g.CheckpointRefs {
		g.CheckpointRefs[i].Normalize()
	}
	g.CheckpointRefs = normalizeGoalCheckpointRefs(g.CheckpointRefs, 32)
	for i := range g.CommandHistory {
		g.CommandHistory[i].Normalize()
	}
	g.CommandHistory = normalizeGoalCommandRecords(g.CommandHistory, 64)
	for i := range g.Iterations {
		g.Iterations[i].Normalize()
	}
	if len(g.Iterations) > 64 {
		g.Iterations = append([]GoalIteration(nil), g.Iterations[len(g.Iterations)-64:]...)
	}
	g.ArtifactRefs = uniqueStrings(g.ArtifactRefs)
	if g.LastAudit != nil {
		g.LastAudit.Blockers = normalizeTaskStateList(g.LastAudit.Blockers, 16)
		g.LastAudit.Warnings = normalizeTaskStateList(g.LastAudit.Warnings, 16)
	}
	if g.LastSemanticReview != nil {
		g.LastSemanticReview.Normalize()
	}
	if g.LastProgress != nil {
		g.LastProgress.Normalize()
	}
	g.LastProgressFingerprint = strings.TrimSpace(g.LastProgressFingerprint)
	if g.NoProgressCount < 0 {
		g.NoProgressCount = 0
	}
	g.LastFailureSignature = strings.TrimSpace(g.LastFailureSignature)
	if g.RepeatedFailureCount < 0 {
		g.RepeatedFailureCount = 0
	}
}

func (g *GoalState) Touch() {
	if g == nil {
		return
	}
	g.UpdatedAt = time.Now()
}

func (i *GoalIteration) Normalize() {
	if i == nil {
		return
	}
	i.Status = strings.TrimSpace(strings.ToLower(i.Status))
	i.CheckpointID = strings.TrimSpace(i.CheckpointID)
	i.CheckpointName = strings.TrimSpace(i.CheckpointName)
	i.ImplementReply = compactPromptSection(strings.TrimSpace(i.ImplementReply), 1200)
	i.ReviewReply = compactPromptSection(strings.TrimSpace(i.ReviewReply), 1200)
	i.ReviewerVerdict = strings.TrimSpace(strings.ToLower(i.ReviewerVerdict))
	i.ReviewerFeedback = compactPromptSection(strings.TrimSpace(i.ReviewerFeedback), 1200)
	if i.SemanticReview != nil {
		i.SemanticReview.Normalize()
	}
	i.RepairReply = compactPromptSection(strings.TrimSpace(i.RepairReply), 1200)
	i.ReplySummary = compactPromptSection(strings.TrimSpace(i.ReplySummary), 1600)
	i.Verification = strings.TrimSpace(i.Verification)
	i.AuditStatus = strings.TrimSpace(i.AuditStatus)
	i.RecoveryStatus = strings.TrimSpace(i.RecoveryStatus)
	i.RollbackStatus = strings.TrimSpace(i.RollbackStatus)
	i.Error = compactPromptSection(strings.TrimSpace(i.Error), 600)
	if i.Progress != nil {
		i.Progress.Normalize()
	}
	i.ChangedFiles = normalizeTaskStateList(i.ChangedFiles, 64)
	i.Blockers = normalizeTaskStateList(i.Blockers, 16)
	i.Warnings = normalizeTaskStateList(i.Warnings, 16)
	for index := range i.Commands {
		i.Commands[index].Normalize()
	}
	i.Commands = normalizeGoalCommandRecords(i.Commands, 16)
}

func (r *GoalSemanticReview) Normalize() {
	if r == nil {
		return
	}
	r.Verdict = strings.TrimSpace(strings.ToLower(r.Verdict))
	if r.Verdict == "" {
		if r.Approved {
			r.Verdict = "approved"
		} else {
			r.Verdict = "needs_revision"
		}
	}
	r.Feedback = compactPromptSection(strings.TrimSpace(r.Feedback), 1200)
	if r.ReviewedAt.IsZero() {
		r.ReviewedAt = time.Now()
	}
}

func goalStatusTerminal(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case goalStatusComplete, goalStatusCanceled:
		return true
	default:
		return false
	}
}

func goalEventSeverity(goal GoalState) string {
	switch strings.TrimSpace(strings.ToLower(goal.Status)) {
	case goalStatusComplete:
		return conversationSeverityInfo
	case goalStatusBlocked:
		return conversationSeverityError
	default:
		return conversationSeverityWarn
	}
}

func splitGoalFields(input string) []string {
	fields := []string{}
	var b strings.Builder
	inQuote := false
	escape := false
	for _, r := range input {
		switch {
		case escape:
			b.WriteRune(r)
			escape = false
		case r == '\\' && inQuote:
			escape = true
		case r == '"':
			inQuote = !inQuote
		case !inQuote && (r == ' ' || r == '\t' || r == '\n' || r == '\r'):
			if b.Len() > 0 {
				fields = append(fields, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		fields = append(fields, b.String())
	}
	return fields
}

func quoteGoalArg(value string) string {
	if strings.TrimSpace(value) == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\r\n\"") {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return value
}

func goalFileExists(root string, raw string) bool {
	path := resolveGoalPath(root, raw)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func readGoalObjectiveFile(root string, raw string) (string, string, error) {
	path := resolveGoalPath(root, raw)
	if path == "" {
		return "", "", fmt.Errorf("goal file path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	return string(data), filepath.ToSlash(path), nil
}

func resolveGoalPath(root string, raw string) string {
	trimmed := strings.TrimSpace(strings.TrimPrefix(raw, "@"))
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	base := strings.TrimSpace(root)
	if base == "" {
		base = "."
	}
	return filepath.Clean(filepath.Join(base, trimmed))
}
