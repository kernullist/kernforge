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
	ID            string          `json:"id"`
	Objective     string          `json:"objective"`
	SourcePath    string          `json:"source_path,omitempty"`
	Status        string          `json:"status"`
	Iteration     int             `json:"iteration"`
	MaxIterations int             `json:"max_iterations,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	CompletedAt   time.Time       `json:"completed_at,omitempty"`
	LastError     string          `json:"last_error,omitempty"`
	LastAudit     *GoalAuditState `json:"last_audit,omitempty"`
	Iterations    []GoalIteration `json:"iterations,omitempty"`
	ArtifactRefs  []string        `json:"artifact_refs,omitempty"`
}

type GoalAuditState struct {
	ID       string   `json:"id,omitempty"`
	Ready    bool     `json:"ready"`
	Status   string   `json:"status,omitempty"`
	Blockers []string `json:"blockers,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type GoalIteration struct {
	Index          int       `json:"index"`
	Status         string    `json:"status"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	ImplementReply string    `json:"implement_reply,omitempty"`
	ReviewReply    string    `json:"review_reply,omitempty"`
	ReplySummary   string    `json:"reply_summary,omitempty"`
	Verification   string    `json:"verification,omitempty"`
	AuditID        string    `json:"audit_id,omitempty"`
	AuditReady     bool      `json:"audit_ready,omitempty"`
	AuditStatus    string    `json:"audit_status,omitempty"`
	Blockers       []string  `json:"blockers,omitempty"`
	Warnings       []string  `json:"warnings,omitempty"`
	RecoveryStatus string    `json:"recovery_status,omitempty"`
	Error          string    `json:"error,omitempty"`
}

type goalStartOptions struct {
	Objective     string
	SourcePath    string
	Run           bool
	MaxIterations int
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
	case "start", "create", "new", "run", "resume", "continue", "status", "show", "list", "audit", "cancel", "stop":
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
		ID:            fmt.Sprintf("goal-%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/1_000_000),
		Objective:     strings.TrimSpace(options.Objective),
		SourcePath:    strings.TrimSpace(options.SourcePath),
		Status:        goalStatusPending,
		MaxIterations: options.MaxIterations,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	goal.Normalize()
	rt.session.StartTaskState(goal.Objective)
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
	rt.session.UpsertGoal(goal)
	rt.session.StartTaskState(goal.Objective)
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
			goal.LastError = fmt.Sprintf("goal reached max iterations (%d) without a ready completion audit", goal.MaxIterations)
			goal.Touch()
			rt.session.UpsertGoal(goal)
			_ = rt.writeGoalArtifacts(goal)
			if rt.store != nil {
				_ = rt.store.Save(rt.session)
			}
			fmt.Fprintln(rt.writer, rt.ui.warnLine(goal.LastError))
			return nil
		}
		iteration := GoalIteration{
			Index:     goal.Iteration + 1,
			Status:    goalStatusRunning,
			StartedAt: time.Now(),
		}
		fmt.Fprintln(rt.writer, rt.ui.subsection(fmt.Sprintf("Goal iteration %d", iteration.Index)))
		implementReply, err := rt.runGoalAgentReply(ctx, buildGoalImplementationPrompt(goal, iteration.Index))
		iteration.ImplementReply = compactPromptSection(implementReply, 900)
		if err != nil {
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
			return err
		}
		reviewReply, err := rt.runGoalAgentReply(ctx, buildGoalReviewPrompt(goal, iteration.Index))
		iteration.ReviewReply = compactPromptSection(reviewReply, 900)
		if err != nil {
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
			return err
		}
		if err := rt.handleVerifyCommand("--full"); err != nil {
			iteration.Error = err.Error()
			iteration.Status = goalStatusBlocked
			goal.Status = goalStatusBlocked
			goal.LastError = err.Error()
		}
		if rt.session.LastVerification != nil {
			iteration.Verification = rt.session.LastVerification.SummaryLine()
		}
		audit, err := rt.runGoalCompletionAudit(goal)
		if err != nil {
			iteration.Error = err.Error()
			iteration.Status = goalStatusBlocked
			goal.Status = goalStatusBlocked
			goal.LastError = err.Error()
		} else {
			iteration.AuditID = audit.ID
			iteration.AuditReady = audit.Ready
			iteration.AuditStatus = audit.Status
			iteration.Blockers = append([]string(nil), audit.Blockers...)
			iteration.Warnings = append([]string(nil), audit.Warnings...)
			goal.LastAudit = &GoalAuditState{
				ID:       audit.ID,
				Ready:    audit.Ready,
				Status:   audit.Status,
				Blockers: append([]string(nil), audit.Blockers...),
				Warnings: append([]string(nil), audit.Warnings...),
			}
			if audit.Ready {
				iteration.Status = goalStatusComplete
				goal.Status = goalStatusComplete
				goal.CompletedAt = time.Now()
				goal.LastError = ""
			} else if goal.Status != goalStatusBlocked {
				iteration.Status = goalStatusPending
				if err := rt.handleRecoverCommand("execute-safe goal " + goal.ID); err != nil {
					iteration.RecoveryStatus = "failed: " + err.Error()
				} else {
					iteration.RecoveryStatus = "executed"
				}
			}
		}
		if iteration.Status == "" || iteration.Status == goalStatusRunning {
			iteration.Status = goal.Status
		}
		iteration.ReplySummary = compactPromptSection(strings.Join([]string{iteration.ImplementReply, iteration.ReviewReply}, "\n\n"), 1200)
		iteration.FinishedAt = time.Now()
		goal.Iteration = iteration.Index
		goal.Iterations = append(goal.Iterations, iteration)
		goal.Touch()
		rt.session.UpsertGoal(goal)
		rt.session.AppendConversationEvent(ConversationEvent{
			Kind:     conversationEventKindGoal,
			Severity: goalEventSeverity(goal),
			Summary:  fmt.Sprintf("goal iteration %d: %s", iteration.Index, goal.Status),
			Entities: map[string]string{
				"goal":         goal.ID,
				"status":       goal.Status,
				"iteration":    fmt.Sprintf("%d", iteration.Index),
				"audit_status": iteration.AuditStatus,
				"audit_ready":  fmt.Sprintf("%t", iteration.AuditReady),
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
		if goal.Status == goalStatusComplete {
			fmt.Fprintln(rt.writer, rt.ui.successLine("Goal complete: "+goal.ID))
			return nil
		}
		if goal.Status == goalStatusBlocked {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Goal blocked: "+goal.LastError))
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
	b.WriteString("Return a concise engineering status only after you have made the concrete next change or verified no change is needed.")
	return b.String()
}

func buildGoalReviewPrompt(goal GoalState, iteration int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Autonomous goal review pass for iteration %d.\n\n", iteration)
	fmt.Fprintf(&b, "Objective:\n%s\n\n", strings.TrimSpace(goal.Objective))
	b.WriteString("Review the changes and implementation state as if doing a bug-finding code review.\n")
	b.WriteString("Required behavior:\n")
	b.WriteString("1. Inspect the actual diff or relevant files.\n")
	b.WriteString("2. Look for correctness, security, stability, missing tests, and documentation gaps.\n")
	b.WriteString("3. Fix any bug you find directly.\n")
	b.WriteString("4. Do not ask the user whether to proceed.\n")
	b.WriteString("5. Do not claim completion unless the state is ready for /verify and /completion-audit.\n")
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
		goal.Status = goalStatusComplete
		goal.CompletedAt = time.Now()
		goal.LastError = ""
	}
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
	if goal.SourcePath != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("source", goal.SourcePath))
	}
	if goal.LastAudit != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("audit", fmt.Sprintf("%s ready=%t", valueOrUnset(goal.LastAudit.Status), goal.LastAudit.Ready)))
	}
	if goal.LastError != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("last_error", goal.LastError))
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
	fmt.Fprintf(&b, "Created: %s\n", goal.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Updated: %s\n", goal.UpdatedAt.Format(time.RFC3339))
	if !goal.CompletedAt.IsZero() {
		fmt.Fprintf(&b, "Completed: %s\n", goal.CompletedAt.Format(time.RFC3339))
	}
	if goal.SourcePath != "" {
		fmt.Fprintf(&b, "Source: %s\n", goal.SourcePath)
	}
	fmt.Fprintf(&b, "\n## Objective\n\n%s\n", strings.TrimSpace(goal.Objective))
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
	if goal.LastError != "" {
		fmt.Fprintf(&b, "\n## Last Error\n\n%s\n", goal.LastError)
	}
	if len(goal.Iterations) > 0 {
		fmt.Fprintf(&b, "\n## Iterations\n\n")
		for _, iteration := range goal.Iterations {
			fmt.Fprintf(&b, "### %d. %s\n\n", iteration.Index, valueOrUnset(iteration.Status))
			if iteration.Verification != "" {
				fmt.Fprintf(&b, "- Verification: %s\n", iteration.Verification)
			}
			if iteration.AuditStatus != "" {
				fmt.Fprintf(&b, "- Audit: %s ready=%t\n", iteration.AuditStatus, iteration.AuditReady)
			}
			if iteration.RecoveryStatus != "" {
				fmt.Fprintf(&b, "- Recovery: %s\n", iteration.RecoveryStatus)
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
	i.ImplementReply = compactPromptSection(strings.TrimSpace(i.ImplementReply), 1200)
	i.ReviewReply = compactPromptSection(strings.TrimSpace(i.ReviewReply), 1200)
	i.ReplySummary = compactPromptSection(strings.TrimSpace(i.ReplySummary), 1600)
	i.Verification = strings.TrimSpace(i.Verification)
	i.AuditStatus = strings.TrimSpace(i.AuditStatus)
	i.RecoveryStatus = strings.TrimSpace(i.RecoveryStatus)
	i.Error = compactPromptSection(strings.TrimSpace(i.Error), 600)
	i.Blockers = normalizeTaskStateList(i.Blockers, 16)
	i.Warnings = normalizeTaskStateList(i.Warnings, 16)
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
