package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	recoveryActionStatusPending    = "pending"
	recoveryActionStatusManualOnly = "manual_only"
	recoveryActionStatusExecuted   = "executed"
	recoveryActionStatusFailed     = "failed"
	recoveryActionStatusSkipped    = "skipped"
)

type RecoveryBrief struct {
	ID                     string                    `json:"id"`
	CreatedAt              time.Time                 `json:"created_at"`
	SessionID              string                    `json:"session_id,omitempty"`
	Workspace              string                    `json:"workspace,omitempty"`
	BaseRoot               string                    `json:"base_root,omitempty"`
	Branch                 string                    `json:"branch,omitempty"`
	Provider               string                    `json:"provider,omitempty"`
	Model                  string                    `json:"model,omitempty"`
	ActiveFeatureID        string                    `json:"active_feature_id,omitempty"`
	Note                   string                    `json:"note,omitempty"`
	PrimaryFailure         string                    `json:"primary_failure,omitempty"`
	Diagnosis              RecoveryDiagnosis         `json:"diagnosis,omitempty"`
	RecentErrorExplanation string                    `json:"recent_error_explanation,omitempty"`
	LastVerification       string                    `json:"last_verification,omitempty"`
	VerificationFailure    string                    `json:"verification_failure,omitempty"`
	FailureRepair          string                    `json:"failure_repair,omitempty"`
	RecentErrors           []string                  `json:"recent_errors,omitempty"`
	BackgroundJobs         []string                  `json:"background_jobs,omitempty"`
	BackgroundBundles      []string                  `json:"background_bundles,omitempty"`
	ChangedFiles           []string                  `json:"changed_files,omitempty"`
	OpenTasks              []string                  `json:"open_tasks,omitempty"`
	Worktrees              []string                  `json:"worktrees,omitempty"`
	RecoveryActions        []string                  `json:"recovery_actions,omitempty"`
	ActionPlan             []RecoveryActionPlanItem  `json:"action_plan,omitempty"`
	ExecutionLog           []RecoveryExecutionRecord `json:"execution_log,omitempty"`
	NextCommands           []string                  `json:"next_commands,omitempty"`
	ArtifactRefs           []string                  `json:"artifact_refs,omitempty"`
	SuggestedPrompt        string                    `json:"suggested_prompt,omitempty"`
}

type RecoveryDiagnosis struct {
	Class     string `json:"class,omitempty"`
	Category  string `json:"category,omitempty"`
	Source    string `json:"source,omitempty"`
	Signature string `json:"signature,omitempty"`
	Evidence  string `json:"evidence,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
	Blocking  bool   `json:"blocking,omitempty"`
}

type RecoveryActionPlanItem struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Command          string   `json:"command,omitempty"`
	Rationale        string   `json:"rationale,omitempty"`
	Source           string   `json:"source,omitempty"`
	Status           string   `json:"status"`
	SafeAuto         bool     `json:"safe_auto,omitempty"`
	VerificationGate bool     `json:"verification_gate,omitempty"`
	StopOnFailure    bool     `json:"stop_on_failure,omitempty"`
	DependsOn        []string `json:"depends_on,omitempty"`
}

type RecoveryExecutionRecord struct {
	ActionID   string    `json:"action_id"`
	Command    string    `json:"command"`
	Status     string    `json:"status"`
	ExitCode   *int      `json:"exit_code,omitempty"`
	Output     string    `json:"output,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

func (rt *runtimeState) handleRecoverCommand(args string) error {
	return rt.handleRecoverCommandContext(context.Background(), args)
}

func (rt *runtimeState) handleRecoverCommandContext(ctx context.Context, args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	options := parseRecoverCommandOptions(args)
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = rt.session.WorkingDir
	}
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("workspace root is not configured")
	}
	brief := rt.buildRecoveryBrief(root, options.Note)
	if options.ExecuteSafe {
		rt.executeRecoverySafePlanContext(ctx, &brief)
		brief.LastVerification = ""
		brief.VerificationFailure = ""
		if rt.session.LastVerification != nil {
			brief.LastVerification = rt.session.LastVerification.SummaryLine()
			brief.VerificationFailure = continuityVerificationFailureSummary(*rt.session.LastVerification)
		}
		brief.RecoveryActions = recoveryBriefPostExecutionActions(rt.session, brief)
		brief.NextCommands = recoveryBriefNextCommands(rt.session, brief)
		brief.SuggestedPrompt = recoveryBriefSuggestedPrompt(brief)
	}
	outDir := filepath.Join(root, userConfigDirName, "recovery")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	jsonPath := filepath.Join(outDir, "latest.json")
	mdPath := filepath.Join(outDir, "latest.md")
	data, err := json.MarshalIndent(brief, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(mdPath, []byte(renderRecoveryBriefMarkdown(brief)), 0o644); err != nil {
		return err
	}
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindRecovery,
		Severity: recoveryBriefSeverity(brief),
		Summary:  "recovery brief generated: " + valueOrDefault(brief.PrimaryFailure, "no active failure"),
		ArtifactRefs: []string{
			mdPath,
			jsonPath,
		},
		Entities: map[string]string{
			"recovery":           brief.ID,
			"actions":            fmt.Sprintf("%d", len(brief.RecoveryActions)),
			"planned_actions":    fmt.Sprintf("%d", len(brief.ActionPlan)),
			"executed_actions":   fmt.Sprintf("%d", recoveryExecutedActionCount(brief.ActionPlan)),
			"recent_errors":      fmt.Sprintf("%d", len(brief.RecentErrors)),
			"background_jobs":    fmt.Sprintf("%d", len(brief.BackgroundJobs)),
			"background_bundles": fmt.Sprintf("%d", len(brief.BackgroundBundles)),
		},
	})
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Generated recovery brief: "+mdPath))
	if strings.TrimSpace(brief.PrimaryFailure) != "" {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Primary failure: "+brief.PrimaryFailure))
	}
	if len(brief.RecoveryActions) > 0 {
		if brief.Diagnosis.Blocking || strings.TrimSpace(brief.PrimaryFailure) != "" {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Recovery actions:"))
		} else {
			fmt.Fprintln(rt.writer, rt.ui.successLine("Recovery actions:"))
		}
		for _, action := range brief.RecoveryActions {
			fmt.Fprintln(rt.writer, "- "+action)
		}
	}
	return nil
}

type recoverCommandOptions struct {
	ExecuteSafe bool
	Note        string
}

func parseRecoverCommandOptions(args string) recoverCommandOptions {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 {
		return recoverCommandOptions{}
	}
	first := strings.ToLower(strings.TrimSpace(fields[0]))
	switch first {
	case "execute-safe", "run-safe", "repair-safe":
		return recoverCommandOptions{
			ExecuteSafe: true,
			Note:        strings.TrimSpace(strings.Join(fields[1:], " ")),
		}
	default:
		return recoverCommandOptions{Note: strings.TrimSpace(args)}
	}
}

func (rt *runtimeState) buildRecoveryBrief(root string, note string) RecoveryBrief {
	now := time.Now()
	brief := RecoveryBrief{
		ID:              fmt.Sprintf("recovery-%s", now.Format("20060102-150405")),
		CreatedAt:       now,
		SessionID:       rt.session.ID,
		Workspace:       strings.TrimSpace(rt.session.WorkingDir),
		BaseRoot:        sessionBaseWorkingDir(rt.session),
		Branch:          delegationGitBranch(root),
		Provider:        rt.session.Provider,
		Model:           rt.session.Model,
		ActiveFeatureID: rt.session.ActiveFeatureID,
		Note:            strings.TrimSpace(note),
	}
	brief.ChangedFiles = delegationChangedFiles(root)
	brief.OpenTasks = delegationOpenTasks(rt.session)
	brief.Worktrees = continuityWorktreeSummaries(rt.session)
	if rt.session.LastVerification != nil {
		brief.LastVerification = rt.session.LastVerification.SummaryLine()
		brief.VerificationFailure = continuityVerificationFailureSummary(*rt.session.LastVerification)
	}
	brief.FailureRepair = continuityFailureRepairSummary(rt.session)
	rt.syncContinuityBackgroundJobs()
	brief.BackgroundJobs = continuityBackgroundJobSummaries(rt.session.BackgroundJobs)
	brief.BackgroundBundles = continuityBackgroundBundleSummaries(rt.session.BackgroundBundles)
	brief.RecentErrors = continuityRecentErrors(rt.session, 6)
	brief.ArtifactRefs = delegationArtifactRefs(rt.session.ConversationEvents, 16)
	events := recentErrorEvents(rt.session, 3)
	if len(events) > 0 {
		brief.PrimaryFailure = renderRecentErrorAlternateSummary(events[0])
		brief.RecentErrorExplanation = renderRecentErrorAnswer(events[0], events[1:])
	}
	if strings.TrimSpace(brief.PrimaryFailure) == "" {
		brief.PrimaryFailure = firstNonBlankString(brief.VerificationFailure, recoveryPrimaryBackgroundFailure(rt.session))
	}
	brief.Diagnosis = recoveryBriefDiagnosis(rt.session, brief, events)
	brief.RecoveryActions = recoveryBriefActions(rt.session, brief, events)
	brief.ActionPlan = recoveryBriefActionPlan(rt.session, brief, events)
	brief.NextCommands = recoveryBriefNextCommands(rt.session, brief)
	brief.SuggestedPrompt = recoveryBriefSuggestedPrompt(brief)
	return brief
}

func recoveryBriefDiagnosis(session *Session, brief RecoveryBrief, events []ConversationEvent) RecoveryDiagnosis {
	if session != nil && session.LastVerification != nil && session.LastVerification.HasFailures() {
		failure := session.LastVerification.FirstFailure()
		if failure != nil {
			category := firstNonBlankString(failure.FailureKind, "verification_failure")
			evidence := compactPromptSection(firstNonBlankString(failure.Hint, failure.Output, failure.Label), 320)
			return RecoveryDiagnosis{
				Class:     "verification_failure",
				Category:  category,
				Source:    "verification",
				Signature: recoverySignature("verification", failure.Label, failure.Command, category, failure.Output),
				Evidence:  evidence,
				Retryable: strings.TrimSpace(failure.Command) != "",
				Blocking:  true,
			}
		}
	}
	if len(events) > 0 {
		event := events[0]
		entities := event.Entities
		class := strings.TrimSpace(event.Kind)
		category := firstNonBlankString(entities["category"], entities["code"], entities["status_code"], class)
		evidence := compactPromptSection(firstNonBlankString(event.Summary, event.Raw), 320)
		return RecoveryDiagnosis{
			Class:     class,
			Category:  category,
			Source:    "conversation_event",
			Signature: recoverySignature(class, category, entities["command"], entities["tool"], event.Raw),
			Evidence:  evidence,
			Retryable: recoveryEventIsRetryable(event),
			Blocking:  event.Severity == conversationSeverityError,
		}
	}
	if failure := recoveryPrimaryBackgroundFailure(session); strings.TrimSpace(failure) != "" {
		return RecoveryDiagnosis{
			Class:     "background_failure",
			Category:  "job_or_bundle",
			Source:    "background_jobs",
			Signature: recoverySignature("background", failure),
			Evidence:  compactPromptSection(failure, 320),
			Retryable: true,
			Blocking:  true,
		}
	}
	if len(brief.ChangedFiles) > 0 {
		return RecoveryDiagnosis{
			Class:     "verification_needed",
			Category:  "changed_files",
			Source:    "git",
			Signature: recoverySignature("changed_files", strings.Join(brief.ChangedFiles, ",")),
			Evidence:  strings.Join(limitStrings(brief.ChangedFiles, 8), ", "),
			Retryable: true,
			Blocking:  false,
		}
	}
	return RecoveryDiagnosis{
		Class:     "no_active_failure",
		Category:  "none",
		Source:    "session",
		Signature: recoverySignature("no_active_failure", brief.SessionID),
		Retryable: false,
		Blocking:  false,
	}
}

func recoverySignature(parts ...string) string {
	joined := strings.Join(parts, "\x00")
	sum := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(sum[:])[:16]
}

func recoveryEventIsRetryable(event ConversationEvent) bool {
	entities := event.Entities
	category := strings.TrimSpace(strings.ToLower(entities["category"]))
	code := firstNonEmptyRuntimeString(entities["code"], entities["status_code"])
	command := strings.TrimSpace(entities["command"])
	switch {
	case category == "rate_limit" || code == "429" || category == "timeout":
		return true
	case command != "":
		return recoveryCommandAutoRunnable(command)
	default:
		return event.Kind == conversationEventKindToolError || event.Kind == conversationEventKindProviderError
	}
}

func recoveryPrimaryBackgroundFailure(session *Session) string {
	if session == nil {
		return ""
	}
	for _, job := range session.BackgroundJobs {
		job.Normalize()
		status := strings.TrimSpace(strings.ToLower(job.Status))
		if status == "failed" || status == "stale" {
			return jobSupervisorJobSummary(job)
		}
	}
	for _, bundle := range session.BackgroundBundles {
		bundle.Normalize()
		status := strings.TrimSpace(strings.ToLower(bundle.Status))
		if status == "failed" || status == "stale" {
			return jobSupervisorBundleSummary(bundle)
		}
	}
	return ""
}

func recoveryBriefActions(session *Session, brief RecoveryBrief, events []ConversationEvent) []string {
	actions := []string{}
	if session == nil {
		return nil
	}
	if session.ActiveFailureRepair != nil {
		for _, step := range session.ActiveFailureRepair.NextSteps {
			if strings.TrimSpace(step) != "" {
				actions = append(actions, step)
			}
		}
	}
	if session.LastVerification != nil && session.LastVerification.HasFailures() {
		failure := session.LastVerification.FirstFailure()
		if failure != nil {
			if strings.TrimSpace(failure.Command) != "" {
				actions = append(actions, "Fix the first failing verification, then rerun: "+failure.Command)
			} else {
				actions = append(actions, "Fix the first failing verification, then rerun /verify.")
			}
			if strings.TrimSpace(failure.Hint) != "" {
				actions = append(actions, "Use verifier hint: "+compactPromptSection(failure.Hint, 220))
			}
		}
	}
	if len(events) > 0 {
		actions = append(actions, recoveryActionsForEvent(events[0])...)
	}
	for _, job := range session.BackgroundJobs {
		job.Normalize()
		status := strings.TrimSpace(strings.ToLower(job.Status))
		switch status {
		case "failed", "stale":
			actions = append(actions, "Inspect background job "+job.ID+" with /jobs check "+job.ID+" before retrying equivalent work.")
		case "running":
			actions = append(actions, "Poll running background job "+job.ID+" with /jobs check "+job.ID+" before launching duplicate work.")
		}
	}
	for _, bundle := range session.BackgroundBundles {
		bundle.Normalize()
		status := strings.TrimSpace(strings.ToLower(bundle.Status))
		switch status {
		case "failed", "stale":
			actions = append(actions, "Inspect background bundle "+bundle.ID+" with /jobs bundle "+bundle.ID+" before restarting verification.")
		case "running":
			actions = append(actions, "Poll running background bundle "+bundle.ID+" with /jobs bundle "+bundle.ID+" before launching duplicate verification.")
		}
	}
	if len(brief.OpenTasks) > 0 {
		actions = append(actions, "Resume or unblock the first open task node before claiming completion.")
	}
	if len(actions) == 0 {
		if len(brief.ChangedFiles) > 0 {
			actions = append(actions, "Run focused verification for changed files before finalizing.")
		} else {
			actions = append(actions, "No active failure was found. Run /completion-audit before finalizing.")
		}
	}
	return normalizeTaskStateList(actions, 12)
}

func recoveryBriefPostExecutionActions(session *Session, brief RecoveryBrief) []string {
	actions := []string{}
	for _, item := range brief.ActionPlan {
		switch item.Status {
		case recoveryActionStatusFailed:
			actions = append(actions, "Inspect failed recovery action "+item.ID+" before retrying: "+item.Title)
		case recoveryActionStatusSkipped:
			actions = append(actions, "Review skipped recovery action "+item.ID+" before claiming recovery is complete: "+item.Title)
		case recoveryActionStatusManualOnly:
			actions = append(actions, "Complete manual-only recovery action "+item.ID+": "+item.Title)
		}
	}
	if len(actions) == 0 {
		if recoveryVerificationSatisfied(session) {
			actions = append(actions, "Safe recovery action plan executed and the latest verification gate passed.")
		} else if len(brief.ExecutionLog) > 0 {
			actions = append(actions, "Safe recovery action plan executed; review the execution log before finalizing.")
		}
	}
	if len(actions) == 0 {
		return brief.RecoveryActions
	}
	return normalizeTaskStateList(actions, 12)
}

func recoveryBriefActionPlan(session *Session, brief RecoveryBrief, events []ConversationEvent) []RecoveryActionPlanItem {
	plan := []RecoveryActionPlanItem{}
	add := func(item RecoveryActionPlanItem) {
		item.ID = strings.TrimSpace(item.ID)
		item.Title = strings.TrimSpace(item.Title)
		item.Command = strings.TrimSpace(item.Command)
		item.Rationale = compactPromptSection(strings.TrimSpace(item.Rationale), 320)
		item.Source = strings.TrimSpace(item.Source)
		item.Status = strings.TrimSpace(item.Status)
		if item.Status == "" {
			item.Status = recoveryActionStatusPending
		}
		if item.ID == "" {
			item.ID = fmt.Sprintf("recover-%02d", len(plan)+1)
		}
		if item.Command != "" && !item.SafeAuto {
			item.Status = recoveryActionStatusManualOnly
		}
		if item.Title == "" {
			return
		}
		plan = append(plan, item)
	}
	if session != nil && session.ActiveFailureRepair != nil {
		for index, step := range session.ActiveFailureRepair.NextSteps {
			if strings.TrimSpace(step) == "" {
				continue
			}
			add(RecoveryActionPlanItem{
				ID:        fmt.Sprintf("repair-step-%02d", index+1),
				Title:     step,
				Rationale: "Preserved from the active failure repair attempt.",
				Source:    "failure_repair",
				Status:    recoveryActionStatusManualOnly,
			})
		}
	}
	if session != nil && session.LastVerification != nil && session.LastVerification.HasFailures() {
		if failure := session.LastVerification.FirstFailure(); failure != nil {
			command := strings.TrimSpace(failure.Command)
			item := RecoveryActionPlanItem{
				ID:               "verification-rerun-01",
				Title:            "Rerun the first failing verification after applying the repair.",
				Rationale:        firstNonBlankString(failure.Hint, "The latest verification failed and must be rechecked before finalizing."),
				Source:           "verification",
				VerificationGate: true,
				StopOnFailure:    true,
			}
			if command != "" {
				item.Command = "!" + command
				item.SafeAuto = recoveryCommandAutoRunnable(command)
			} else {
				item.Command = "/verify"
				item.SafeAuto = true
			}
			add(item)
		}
	}
	if len(events) > 0 {
		event := events[0]
		command := strings.TrimSpace(event.Entities["command"])
		tool := strings.TrimSpace(event.Entities["tool"])
		switch {
		case command != "":
			add(RecoveryActionPlanItem{
				ID:            "event-command-rerun-01",
				Title:         "Rerun the failed command only if the failure condition has changed.",
				Command:       "!" + command,
				Rationale:     compactPromptSection(firstNonBlankString(event.Summary, event.Raw), 320),
				Source:        "conversation_event",
				SafeAuto:      recoveryCommandAutoRunnable(command),
				StopOnFailure: true,
			})
		case tool != "":
			add(RecoveryActionPlanItem{
				ID:        "event-tool-review-01",
				Title:     "Refresh stale context and rerun the narrowest equivalent tool step: " + tool,
				Rationale: compactPromptSection(firstNonBlankString(event.Summary, event.Raw), 320),
				Source:    "conversation_event",
				Status:    recoveryActionStatusManualOnly,
			})
		}
	}
	if session != nil {
		for _, job := range session.BackgroundJobs {
			job.Normalize()
			status := strings.TrimSpace(strings.ToLower(job.Status))
			if status == "failed" || status == "stale" || status == "running" {
				add(RecoveryActionPlanItem{
					ID:        "job-" + strings.TrimSpace(job.ID),
					Title:     "Inspect background job " + job.ID + " before launching duplicate work.",
					Command:   "/jobs check " + job.ID,
					Rationale: jobSupervisorJobSummary(job),
					Source:    "background_job",
					SafeAuto:  true,
				})
			}
		}
		for _, bundle := range session.BackgroundBundles {
			bundle.Normalize()
			status := strings.TrimSpace(strings.ToLower(bundle.Status))
			if status == "failed" || status == "stale" || status == "running" {
				add(RecoveryActionPlanItem{
					ID:        "bundle-" + strings.TrimSpace(bundle.ID),
					Title:     "Inspect background bundle " + bundle.ID + " before restarting verification.",
					Command:   "/jobs bundle " + bundle.ID,
					Rationale: jobSupervisorBundleSummary(bundle),
					Source:    "background_bundle",
					SafeAuto:  true,
				})
			}
		}
	}
	if len(brief.OpenTasks) > 0 {
		add(RecoveryActionPlanItem{
			ID:        "open-tasks-01",
			Title:     "Review open task graph nodes before claiming completion.",
			Command:   "/tasks",
			Rationale: strings.Join(limitStrings(brief.OpenTasks, 4), " | "),
			Source:    "task_graph",
			SafeAuto:  true,
		})
	}
	if len(brief.ChangedFiles) > 0 && !recoveryPlanHasVerificationGate(plan) {
		add(RecoveryActionPlanItem{
			ID:               "verify-changed-files-01",
			Title:            "Run focused verification for changed files.",
			Command:          "/verify",
			Rationale:        strings.Join(limitStrings(brief.ChangedFiles, 8), ", "),
			Source:           "git",
			SafeAuto:         true,
			VerificationGate: true,
			StopOnFailure:    true,
		})
	}
	add(RecoveryActionPlanItem{
		ID:        "continuity-01",
		Title:     "Refresh the long-task continuity packet after recovery actions.",
		Command:   "/continuity continue from recovery brief",
		Rationale: "Keeps compact/resume state aligned with the latest recovery attempt.",
		Source:    "continuity",
		SafeAuto:  true,
	})
	add(RecoveryActionPlanItem{
		ID:               "completion-audit-01",
		Title:            "Run completion audit after recovery to prevent overclaiming.",
		Command:          "/completion-audit after recovery",
		Rationale:        "Final readiness must be based on blockers, warnings, verification, tasks, jobs, and artifact evidence.",
		Source:           "completion_audit",
		SafeAuto:         true,
		VerificationGate: true,
	})
	return normalizeRecoveryActionPlan(plan, 16)
}

func recoveryPlanHasVerificationGate(plan []RecoveryActionPlanItem) bool {
	for _, item := range plan {
		if item.VerificationGate {
			return true
		}
	}
	return false
}

func normalizeRecoveryActionPlan(plan []RecoveryActionPlanItem, limit int) []RecoveryActionPlanItem {
	if limit <= 0 {
		limit = len(plan)
	}
	out := make([]RecoveryActionPlanItem, 0, len(plan))
	seen := map[string]bool{}
	for _, item := range plan {
		item.ID = strings.TrimSpace(item.ID)
		item.Title = strings.Join(strings.Fields(strings.TrimSpace(item.Title)), " ")
		item.Command = strings.Join(strings.Fields(strings.TrimSpace(item.Command)), " ")
		item.Source = strings.TrimSpace(strings.ToLower(item.Source))
		item.Status = strings.TrimSpace(strings.ToLower(item.Status))
		if item.Status == "" {
			item.Status = recoveryActionStatusPending
		}
		if item.Command != "" && !item.SafeAuto && item.Status == recoveryActionStatusPending {
			item.Status = recoveryActionStatusManualOnly
		}
		key := strings.ToLower(item.Title + "\x00" + item.Command)
		if item.Title == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func recoveryActionsForEvent(event ConversationEvent) []string {
	entities := event.Entities
	category := strings.TrimSpace(strings.ToLower(entities["category"]))
	code := firstNonEmptyRuntimeString(entities["code"], entities["status_code"])
	command := strings.TrimSpace(entities["command"])
	tool := strings.TrimSpace(entities["tool"])
	actions := []string{}
	switch {
	case category == "rate_limit" || code == "429":
		actions = append(actions, "Retry after the provider rate limit cools down, switch the worker/reviewer model, or configure provider BYOK for this route.")
	case category == "timeout":
		actions = append(actions, "Reduce the command/model shard size or rerun with a longer timeout after checking partial output.")
	case command != "":
		actions = append(actions, "Review the failed command output, fix the first concrete error, then rerun: "+command)
	case tool != "":
		actions = append(actions, "Review the failed tool call, refresh any stale file/context, then rerun the narrowest equivalent tool step: "+tool)
	default:
		actions = append(actions, "Review the recent runtime error and retry only after changing the failing condition.")
	}
	return normalizeTaskStateList(actions, 4)
}

func recoveryBriefNextCommands(session *Session, brief RecoveryBrief) []string {
	commands := []string{}
	if session != nil && session.LastVerification != nil && session.LastVerification.HasFailures() {
		if failure := session.LastVerification.FirstFailure(); failure != nil && strings.TrimSpace(failure.Command) != "" {
			commands = append(commands, "!"+strings.TrimSpace(failure.Command))
		} else {
			commands = append(commands, "/verify")
		}
	} else if len(brief.ChangedFiles) > 0 && !recoveryVerificationSatisfied(session) {
		commands = append(commands, "/verify")
	}
	if len(brief.BackgroundJobs) > 0 || len(brief.BackgroundBundles) > 0 {
		commands = append(commands, "/jobs status")
	}
	if len(brief.OpenTasks) > 0 {
		commands = append(commands, "/tasks")
	}
	commands = append(commands, "/continuity continue from recovery brief")
	commands = append(commands, "/completion-audit after recovery")
	return normalizeTaskStateList(commands, 10)
}

func recoveryVerificationSatisfied(session *Session) bool {
	if session == nil || session.LastVerification == nil {
		return false
	}
	if session.LastVerification.HasFailures() {
		return false
	}
	return completionAuditVerificationHasPassedStep(*session.LastVerification)
}

func (rt *runtimeState) executeRecoverySafePlan(brief *RecoveryBrief) {
	rt.executeRecoverySafePlanContext(context.Background(), brief)
}

func (rt *runtimeState) executeRecoverySafePlanContext(ctx context.Context, brief *RecoveryBrief) {
	if rt == nil || brief == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for index := range brief.ActionPlan {
		action := &brief.ActionPlan[index]
		if ctx.Err() != nil {
			if action.Status == recoveryActionStatusPending {
				action.Status = recoveryActionStatusSkipped
			}
			continue
		}
		if !action.SafeAuto || strings.TrimSpace(action.Command) == "" {
			if action.Status == recoveryActionStatusPending {
				action.Status = recoveryActionStatusManualOnly
			}
			continue
		}
		record := rt.executeRecoveryActionContext(ctx, *action)
		brief.ExecutionLog = append(brief.ExecutionLog, record)
		action.Status = record.Status
		if action.StopOnFailure && record.Status != recoveryActionStatusExecuted {
			for j := index + 1; j < len(brief.ActionPlan); j++ {
				if brief.ActionPlan[j].Status == recoveryActionStatusPending {
					brief.ActionPlan[j].Status = recoveryActionStatusSkipped
				}
			}
			break
		}
	}
	brief.ExecutionLog = normalizeRecoveryExecutionLog(brief.ExecutionLog, 16)
}

func (rt *runtimeState) executeRecoveryAction(action RecoveryActionPlanItem) (record RecoveryExecutionRecord) {
	return rt.executeRecoveryActionContext(context.Background(), action)
}

func (rt *runtimeState) executeRecoveryActionContext(ctx context.Context, action RecoveryActionPlanItem) (record RecoveryExecutionRecord) {
	command := strings.TrimSpace(action.Command)
	record = RecoveryExecutionRecord{
		ActionID:  strings.TrimSpace(action.ID),
		Command:   command,
		Status:    recoveryActionStatusSkipped,
		StartedAt: time.Now(),
	}
	defer func() {
		if record.FinishedAt.IsZero() {
			record.FinishedAt = time.Now()
		}
	}()
	if command == "" {
		record.Output = "No command attached to recovery action."
		return record
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		record.Status = recoveryActionStatusFailed
		record.Output = "command canceled"
		return record
	}
	if strings.HasPrefix(command, "!") {
		record = rt.executeRecoveryShellActionContext(ctx, action, strings.TrimSpace(strings.TrimPrefix(command, "!")))
		return record
	}
	if strings.HasPrefix(command, "/") {
		if err := rt.executeRecoverySlashActionContext(ctx, command); err != nil {
			record.Status = recoveryActionStatusFailed
			record.Output = err.Error()
			return record
		}
		if failure := rt.recoverySlashActionFailure(command); failure != "" {
			record.Status = recoveryActionStatusFailed
			record.Output = failure
			return record
		}
		record.Status = recoveryActionStatusExecuted
		record.Output = "executed " + command
		return record
	}
	record.Output = "Unsupported recovery command form."
	return record
}

func (rt *runtimeState) executeRecoverySlashAction(command string) error {
	return rt.executeRecoverySlashActionContext(context.Background(), command)
}

func (rt *runtimeState) executeRecoverySlashActionContext(ctx context.Context, command string) error {
	cmd, ok := ParseCommand(command)
	if !ok {
		return fmt.Errorf("recovery slash command is invalid: %s", command)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch cmd.Name {
	case "jobs":
		return rt.handleJobsCommand(cmd.Args)
	case "tasks":
		return rt.printRecoveryTasks()
	default:
		_, err := rt.executeSafeSuggestionCommandContext(ctx, command)
		return err
	}
}

func (rt *runtimeState) recoverySlashActionFailure(command string) string {
	cmd, ok := ParseCommand(command)
	if !ok {
		return "recovery slash command did not parse after execution"
	}
	switch cmd.Name {
	case "verify":
		if rt == nil || rt.session == nil || rt.session.LastVerification == nil {
			return "verification command did not record a verification report"
		}
		report := *rt.session.LastVerification
		if report.HasFailures() {
			return firstNonBlankString(report.FailureSummary(), report.RenderShort())
		}
		if !completionAuditVerificationHasPassedStep(report) {
			return "verification command did not pass any step"
		}
	case "completion-audit":
		ready, status, ok := rt.latestCompletionAuditReadiness()
		if !ok {
			return "completion audit command did not record an audit event"
		}
		if !ready {
			return "completion audit is not ready: " + valueOrDefault(status, "unknown")
		}
	}
	return ""
}

func (rt *runtimeState) latestCompletionAuditReadiness() (bool, string, bool) {
	if rt == nil || rt.session == nil {
		return false, "", false
	}
	for i := len(rt.session.ConversationEvents) - 1; i >= 0; i-- {
		event := rt.session.ConversationEvents[i]
		if event.Kind != conversationEventKindCompletionAudit {
			continue
		}
		ready := strings.EqualFold(strings.TrimSpace(event.Entities["ready"]), "true")
		status := strings.TrimSpace(event.Entities["status"])
		return ready, status, true
	}
	return false, "", false
}

func (rt *runtimeState) printRecoveryTasks() error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	if len(rt.session.Plan) == 0 && (rt.session.TaskGraph == nil || len(rt.session.TaskGraph.Nodes) == 0) {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No active plan."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Tasks"))
	if rt.session.TaskGraph != nil && len(rt.session.TaskGraph.Nodes) > 0 {
		fmt.Fprintln(rt.writer, rt.session.TaskGraph.RenderExportSection())
		return nil
	}
	for i, item := range rt.session.Plan {
		fmt.Fprintln(rt.writer, rt.ui.planItem(i, item.Status, item.Step))
	}
	return nil
}

func (rt *runtimeState) executeRecoveryShellAction(action RecoveryActionPlanItem, command string) RecoveryExecutionRecord {
	return rt.executeRecoveryShellActionContext(context.Background(), action, command)
}

func (rt *runtimeState) executeRecoveryShellActionContext(ctx context.Context, action RecoveryActionPlanItem, command string) RecoveryExecutionRecord {
	record := RecoveryExecutionRecord{
		ActionID:  strings.TrimSpace(action.ID),
		Command:   "!" + strings.TrimSpace(command),
		Status:    recoveryActionStatusSkipped,
		StartedAt: time.Now(),
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !recoveryCommandAutoRunnable(command) {
		record.Output = "Command is not in the recovery safe-auto whitelist."
		record.FinishedAt = time.Now()
		return record
	}
	if rt == nil {
		record.Output = "Runtime is not configured."
		record.FinishedAt = time.Now()
		return record
	}
	if err := rt.workspace.EnsureShellWithContext(ctx, command); err != nil {
		record.Output = err.Error()
		record.FinishedAt = time.Now()
		return record
	}
	timeout := rt.workspace.ShellTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	name, args := shellInvocation(rt.workspace.Shell, command)
	cmd := exec.CommandContext(runCtx, name, args...)
	cmd.Dir = rt.workspace.Root
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	if len(output) > 5000 {
		output = output[:5000] + "\n... (truncated)"
	}
	record.Output = output
	record.FinishedAt = time.Now()
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode := exitErr.ExitCode()
		record.ExitCode = &exitCode
	}
	if runCtx.Err() == context.Canceled {
		record.Status = recoveryActionStatusFailed
		if record.Output == "" {
			record.Output = "command canceled"
		}
		rt.noteLocalShellCommand(command, record.Output, runCtx.Err())
		rt.recordRecoveryVerification(action, command, record)
		return record
	}
	if runCtx.Err() == context.DeadlineExceeded {
		record.Status = recoveryActionStatusFailed
		if record.Output == "" {
			record.Output = "command timed out"
		}
		rt.noteLocalShellCommand(command, record.Output, runCtx.Err())
		rt.recordRecoveryVerification(action, command, record)
		return record
	}
	if err != nil {
		record.Status = recoveryActionStatusFailed
		if record.Output == "" {
			record.Output = err.Error()
		}
		rt.noteLocalShellCommand(command, record.Output, err)
		rt.recordRecoveryVerification(action, command, record)
		return record
	}
	exitCode := 0
	record.ExitCode = &exitCode
	record.Status = recoveryActionStatusExecuted
	if record.Output == "" {
		record.Output = "(no output)"
	}
	rt.noteLocalShellCommand(command, record.Output, nil)
	rt.recordRecoveryVerification(action, command, record)
	return record
}

func (rt *runtimeState) recordRecoveryVerification(action RecoveryActionPlanItem, command string, record RecoveryExecutionRecord) {
	if rt == nil || rt.session == nil {
		return
	}
	if !action.VerificationGate && !recoveryCommandLooksVerification(command) {
		return
	}
	stepStatus := VerificationSkipped
	switch record.Status {
	case recoveryActionStatusExecuted:
		stepStatus = VerificationPassed
	case recoveryActionStatusFailed:
		stepStatus = VerificationFailed
	}
	step := VerificationStep{
		Label:   "recovery: " + valueOrDefault(action.Title, action.ID),
		Command: command,
		Status:  stepStatus,
		Output:  record.Output,
	}
	if step.Status == VerificationFailed {
		step.FailureKind, step.Hint = classifyVerificationFailure(step)
	}
	report := VerificationReport{
		GeneratedAt:  record.FinishedAt,
		Trigger:      "recovery",
		Mode:         VerificationAdaptive,
		Workspace:    rt.workspace.Root,
		ChangedPaths: collectVerificationChangedPaths(rt.workspace.Root, rt.session),
		Steps:        []VerificationStep{step},
		Decision:     "Recovery safe execution recorded a verification gate result.",
	}
	rt.session.LastVerification = &report
	if rt.store != nil {
		_ = rt.store.Save(rt.session)
	}
	if rt.verifyHistory != nil {
		_ = rt.verifyHistory.Append(rt.session.ID, workspaceSnapshotRoot(rt.workspace), report)
	}
}

func recoveryCommandAutoRunnable(command string) bool {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" || recoveryCommandHasUnsafeShellSyntax(trimmed) {
		return false
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return false
	}
	name := strings.ToLower(fields[0])
	switch name {
	case "go":
		return recoveryGoCommandAutoRunnable(fields)
	case "git":
		return recoveryGitCommandAutoRunnable(fields)
	default:
		return false
	}
}

func recoveryGoCommandAutoRunnable(fields []string) bool {
	if len(fields) < 2 {
		return false
	}
	sub := strings.ToLower(fields[1])
	switch sub {
	case "test":
		return recoveryGoArgsAutoRunnable(fields[2:], recoveryGoTestBoolFlags(), recoveryGoTestValueFlags())
	case "vet":
		return recoveryGoArgsAutoRunnable(fields[2:], recoveryGoVetBoolFlags(), recoveryGoVetValueFlags())
	case "list":
		return recoveryGoArgsAutoRunnable(fields[2:], recoveryGoListBoolFlags(), recoveryGoListValueFlags())
	default:
		return false
	}
}

func recoveryGoArgsAutoRunnable(args []string, boolFlags map[string]bool, valueFlags map[string]bool) bool {
	expectValueFor := ""
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			return false
		}
		if expectValueFor != "" {
			if !recoveryGoFlagValueAutoRunnable(expectValueFor, arg) {
				return false
			}
			expectValueFor = ""
			continue
		}
		if strings.HasPrefix(arg, "-") {
			name, value, hasValue := recoverySplitFlag(arg)
			name = strings.ToLower(name)
			if valueFlags[name] {
				if hasValue {
					if !recoveryGoFlagValueAutoRunnable(name, value) {
						return false
					}
				} else {
					expectValueFor = name
				}
				continue
			}
			if boolFlags[name] {
				if hasValue && !recoveryGoBoolFlagValueAutoRunnable(value) {
					return false
				}
				continue
			}
			return false
		}
		if !recoveryGoPackageArgAutoRunnable(arg) {
			return false
		}
	}
	return expectValueFor == ""
}

func recoverySplitFlag(arg string) (string, string, bool) {
	if idx := strings.Index(arg, "="); idx > 0 {
		return arg[:idx], arg[idx+1:], true
	}
	return arg, "", false
}

func recoveryGoFlagValueAutoRunnable(flag string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || recoveryCommandHasUnsafeShellSyntax(value) {
		return false
	}
	if strings.ContainsAny(value, "\"'\\") {
		return false
	}
	switch flag {
	case "-count", "-p", "-parallel":
		return recoveryStringIsDigits(value)
	default:
		return !strings.HasPrefix(value, "@")
	}
}

func recoveryGoBoolFlagValueAutoRunnable(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "1", "f", "false", "t", "true":
		return true
	default:
		return false
	}
}

func recoveryStringIsDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func recoveryGoPackageArgAutoRunnable(arg string) bool {
	if arg == "" || strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "@") {
		return false
	}
	if recoveryCommandHasUnsafeShellSyntax(arg) || strings.ContainsAny(arg, "\"'\\") {
		return false
	}
	if strings.Contains(arg, ":") || strings.Contains(arg, "=") {
		return false
	}
	return true
}

func recoveryGoTestBoolFlags() map[string]bool {
	return map[string]bool{
		"-benchmem": true,
		"-cover":    true,
		"-failfast": true,
		"-json":     true,
		"-race":     true,
		"-short":    true,
		"-v":        true,
	}
}

func recoveryGoTestValueFlags() map[string]bool {
	return map[string]bool{
		"-bench":            true,
		"-benchtime":        true,
		"-count":            true,
		"-covermode":        true,
		"-coverpkg":         true,
		"-cpu":              true,
		"-fuzz":             true,
		"-fuzzminimizetime": true,
		"-fuzztime":         true,
		"-list":             true,
		"-p":                true,
		"-parallel":         true,
		"-run":              true,
		"-shuffle":          true,
		"-skip":             true,
		"-tags":             true,
		"-timeout":          true,
		"-vet":              true,
	}
}

func recoveryGoVetBoolFlags() map[string]bool {
	return map[string]bool{
		"-json": true,
		"-n":    true,
		"-v":    true,
		"-x":    true,
	}
}

func recoveryGoVetValueFlags() map[string]bool {
	return map[string]bool{
		"-tags": true,
	}
}

func recoveryGoListBoolFlags() map[string]bool {
	return map[string]bool{
		"-compiled": true,
		"-deps":     true,
		"-e":        true,
		"-export":   true,
		"-find":     true,
		"-json":     true,
		"-m":        true,
		"-test":     true,
	}
}

func recoveryGoListValueFlags() map[string]bool {
	return map[string]bool{
		"-f":    true,
		"-tags": true,
	}
}

func recoveryGitCommandAutoRunnable(fields []string) bool {
	if len(fields) < 2 {
		return false
	}
	sub := strings.ToLower(fields[1])
	switch sub {
	case "status":
		return recoveryGitStatusArgsAutoRunnable(fields[2:])
	case "diff":
		return recoveryGitDiffArgsAutoRunnable(fields[2:])
	default:
		return false
	}
}

func recoveryGitStatusArgsAutoRunnable(args []string) bool {
	allowedFlags := map[string]bool{
		"--ahead-behind":    true,
		"--branch":          true,
		"--ignored":         true,
		"--long":            true,
		"--no-ahead-behind": true,
		"--no-column":       true,
		"--no-renames":      true,
		"--porcelain":       true,
		"--renames":         true,
		"--short":           true,
		"--show-stash":      true,
		"--untracked-files": true,
		"-b":                true,
		"-s":                true,
		"-u":                true,
		"-uno":              true,
	}
	return recoveryGitArgsAutoRunnable(args, allowedFlags, nil, false)
}

func recoveryGitDiffArgsAutoRunnable(args []string) bool {
	allowedFlags := map[string]bool{
		"--cached":      true,
		"--check":       true,
		"--name-only":   true,
		"--name-status": true,
		"--staged":      true,
		"--stat":        true,
	}
	return containsString(args, "--check") && recoveryGitArgsAutoRunnable(args, allowedFlags, nil, true)
}

func recoveryGitArgsAutoRunnable(args []string, boolFlags map[string]bool, valueFlags map[string]bool, allowPathspec bool) bool {
	pathspecMode := false
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" || recoveryCommandHasUnsafeShellSyntax(arg) {
			return false
		}
		if arg == "--" {
			pathspecMode = true
			continue
		}
		if pathspecMode {
			if !allowPathspec || !recoveryGitPathspecAutoRunnable(arg) {
				return false
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			name, value, hasValue := recoverySplitFlag(arg)
			name = strings.ToLower(name)
			if valueFlags != nil && valueFlags[name] {
				if !hasValue || strings.TrimSpace(value) == "" {
					return false
				}
				continue
			}
			if boolFlags[name] {
				if hasValue && strings.TrimSpace(value) == "" {
					return false
				}
				continue
			}
			return false
		}
		if !allowPathspec || !recoveryGitPathspecAutoRunnable(arg) {
			return false
		}
	}
	return true
}

func recoveryGitPathspecAutoRunnable(arg string) bool {
	if arg == "" || strings.HasPrefix(arg, "@") {
		return false
	}
	if strings.ContainsAny(arg, "\"'\\") || strings.Contains(arg, ":") {
		return false
	}
	return true
}

func recoveryCommandHasUnsafeShellSyntax(command string) bool {
	dangerous := []string{";", "&&", "||", "|", ">", "<", "`", "$(", "\n", "\r"}
	for _, token := range dangerous {
		if strings.Contains(command, token) {
			return true
		}
	}
	return false
}

func recoveryCommandLooksVerification(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	return strings.HasPrefix(lower, "go test") ||
		strings.HasPrefix(lower, "go vet") ||
		strings.Contains(lower, "test") ||
		strings.Contains(lower, "verify")
}

func normalizeRecoveryExecutionLog(items []RecoveryExecutionRecord, limit int) []RecoveryExecutionRecord {
	if limit <= 0 || len(items) <= limit {
		return append([]RecoveryExecutionRecord(nil), items...)
	}
	return append([]RecoveryExecutionRecord(nil), items[len(items)-limit:]...)
}

func recoveryBriefSuggestedPrompt(brief RecoveryBrief) string {
	var b strings.Builder
	b.WriteString("Continue from the recovery brief. ")
	if strings.TrimSpace(brief.Note) != "" {
		b.WriteString("Goal: ")
		b.WriteString(strings.TrimSpace(brief.Note))
		b.WriteString(". ")
	}
	if strings.TrimSpace(brief.PrimaryFailure) != "" {
		b.WriteString("Start from the primary failure and listed recovery actions before launching new broad work. ")
	}
	if len(brief.ActionPlan) > 0 {
		b.WriteString("Use the structured action plan statuses as the recovery queue. ")
	}
	b.WriteString("Preserve user changes, reuse or cancel existing background jobs deliberately, and rerun the narrowest relevant verification before finalizing.")
	return strings.TrimSpace(b.String())
}

func recoveryBriefSeverity(brief RecoveryBrief) string {
	if strings.TrimSpace(brief.PrimaryFailure) != "" || len(brief.RecentErrors) > 0 || strings.TrimSpace(brief.VerificationFailure) != "" || continuityHasFailedBackground(ContinuityPacket{BackgroundJobs: brief.BackgroundJobs, BackgroundBundles: brief.BackgroundBundles}) {
		return conversationSeverityWarn
	}
	return conversationSeverityInfo
}

func renderRecoveryBriefMarkdown(brief RecoveryBrief) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Recovery Brief\n\n")
	fmt.Fprintf(&b, "ID: %s\n", brief.ID)
	fmt.Fprintf(&b, "Generated: %s\n", brief.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Session: %s\n", brief.SessionID)
	fmt.Fprintf(&b, "Workspace: %s\n", valueOrUnset(brief.Workspace))
	fmt.Fprintf(&b, "Base root: %s\n", valueOrUnset(brief.BaseRoot))
	fmt.Fprintf(&b, "Branch: %s\n", valueOrDefault(brief.Branch, "unknown"))
	fmt.Fprintf(&b, "Provider/model: %s / %s\n", valueOrUnset(brief.Provider), valueOrUnset(brief.Model))
	if strings.TrimSpace(brief.ActiveFeatureID) != "" {
		fmt.Fprintf(&b, "Active feature: %s\n", brief.ActiveFeatureID)
	}
	if strings.TrimSpace(brief.Note) != "" {
		fmt.Fprintf(&b, "\n## Note\n\n%s\n", brief.Note)
	}
	if strings.TrimSpace(brief.PrimaryFailure) != "" {
		fmt.Fprintf(&b, "\n## Primary Failure\n\n%s\n", brief.PrimaryFailure)
	}
	if strings.TrimSpace(brief.RecentErrorExplanation) != "" {
		fmt.Fprintf(&b, "\n## Recent Error Explanation\n\n%s\n", brief.RecentErrorExplanation)
	}
	if strings.TrimSpace(brief.LastVerification) != "" {
		fmt.Fprintf(&b, "\n## Last Verification\n\n%s\n", brief.LastVerification)
	}
	if strings.TrimSpace(brief.VerificationFailure) != "" {
		fmt.Fprintf(&b, "\n## Verification Failure\n\n%s\n", brief.VerificationFailure)
	}
	if strings.TrimSpace(brief.FailureRepair) != "" {
		fmt.Fprintf(&b, "\n## Failure Repair\n\n%s\n", brief.FailureRepair)
	}
	if strings.TrimSpace(brief.Diagnosis.Class) != "" {
		fmt.Fprintf(&b, "\n## Diagnosis\n\n")
		fmt.Fprintf(&b, "- Class: %s\n", valueOrUnset(brief.Diagnosis.Class))
		fmt.Fprintf(&b, "- Category: %s\n", valueOrUnset(brief.Diagnosis.Category))
		fmt.Fprintf(&b, "- Source: %s\n", valueOrUnset(brief.Diagnosis.Source))
		fmt.Fprintf(&b, "- Signature: %s\n", valueOrUnset(brief.Diagnosis.Signature))
		fmt.Fprintf(&b, "- Retryable: %t\n", brief.Diagnosis.Retryable)
		fmt.Fprintf(&b, "- Blocking: %t\n", brief.Diagnosis.Blocking)
		if strings.TrimSpace(brief.Diagnosis.Evidence) != "" {
			fmt.Fprintf(&b, "- Evidence: %s\n", brief.Diagnosis.Evidence)
		}
	}
	writeDelegationList(&b, "Recovery Actions", brief.RecoveryActions, "No active recovery actions.")
	writeRecoveryActionPlanMarkdown(&b, brief.ActionPlan)
	writeRecoveryExecutionLogMarkdown(&b, brief.ExecutionLog)
	writeDelegationList(&b, "Next Commands", brief.NextCommands, "No next commands suggested.")
	writeDelegationList(&b, "Recent Errors", brief.RecentErrors, "No recent runtime errors recorded.")
	writeDelegationList(&b, "Background Jobs", brief.BackgroundJobs, "No background jobs recorded.")
	writeDelegationList(&b, "Background Bundles", brief.BackgroundBundles, "No background bundles recorded.")
	writeDelegationList(&b, "Changed Files", brief.ChangedFiles, "No changed files detected.")
	writeDelegationList(&b, "Open Tasks", brief.OpenTasks, "No open task graph nodes.")
	writeDelegationList(&b, "Worktrees", brief.Worktrees, "No isolated or task-owner worktrees recorded.")
	writeDelegationList(&b, "Artifact Refs", brief.ArtifactRefs, "No artifact refs recorded.")
	fmt.Fprintf(&b, "\n## Suggested Prompt\n\n%s\n", brief.SuggestedPrompt)
	return strings.TrimSpace(b.String())
}

func writeRecoveryActionPlanMarkdown(b *strings.Builder, plan []RecoveryActionPlanItem) {
	if b == nil {
		return
	}
	fmt.Fprintf(b, "\n## Action Plan\n\n")
	if len(plan) == 0 {
		fmt.Fprintf(b, "- No structured recovery actions.\n")
		return
	}
	for _, item := range plan {
		line := "- [" + valueOrDefault(item.Status, recoveryActionStatusPending) + "] " + item.ID + ": " + item.Title
		if item.Command != "" {
			line += " (`" + item.Command + "`)"
		}
		if item.SafeAuto {
			line += " safe_auto=true"
		}
		if item.VerificationGate {
			line += " verification_gate=true"
		}
		if item.Rationale != "" {
			line += " - " + item.Rationale
		}
		fmt.Fprintln(b, line)
	}
}

func writeRecoveryExecutionLogMarkdown(b *strings.Builder, log []RecoveryExecutionRecord) {
	if b == nil || len(log) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Execution Log\n\n")
	for _, record := range log {
		line := "- [" + valueOrDefault(record.Status, recoveryActionStatusSkipped) + "] " + record.ActionID + ": `" + record.Command + "`"
		if record.ExitCode != nil {
			line += fmt.Sprintf(" exit=%d", *record.ExitCode)
		}
		if record.Output != "" {
			line += " - " + compactPromptSection(record.Output, 240)
		}
		fmt.Fprintln(b, line)
	}
}

func recoveryExecutedActionCount(plan []RecoveryActionPlanItem) int {
	count := 0
	for _, item := range plan {
		if item.Status == recoveryActionStatusExecuted {
			count++
		}
	}
	return count
}
