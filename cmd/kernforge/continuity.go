package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ContinuityPacket struct {
	ID                      string    `json:"id"`
	CreatedAt               time.Time `json:"created_at"`
	SessionID               string    `json:"session_id,omitempty"`
	Workspace               string    `json:"workspace,omitempty"`
	BaseRoot                string    `json:"base_root,omitempty"`
	Branch                  string    `json:"branch,omitempty"`
	Provider                string    `json:"provider,omitempty"`
	Model                   string    `json:"model,omitempty"`
	ActiveFeatureID         string    `json:"active_feature_id,omitempty"`
	ChangedFiles            []string  `json:"changed_files,omitempty"`
	OpenTasks               []string  `json:"open_tasks,omitempty"`
	Worktrees               []string  `json:"worktrees,omitempty"`
	EditLoop                string    `json:"edit_loop,omitempty"`
	FailureRepair           string    `json:"failure_repair,omitempty"`
	LastVerification        string    `json:"last_verification,omitempty"`
	LastVerificationFailure string    `json:"last_verification_failure,omitempty"`
	BackgroundJobs          []string  `json:"background_jobs,omitempty"`
	BackgroundBundles       []string  `json:"background_bundles,omitempty"`
	RecentErrors            []string  `json:"recent_errors,omitempty"`
	ArtifactRefs            []string  `json:"artifact_refs,omitempty"`
	RecoveryActions         []string  `json:"recovery_actions,omitempty"`
	NextCommands            []string  `json:"next_commands,omitempty"`
	SuggestedPrompt         string    `json:"suggested_prompt,omitempty"`
}

func (rt *runtimeState) handleContinuityCommand(args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = rt.session.WorkingDir
	}
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("workspace root is not configured")
	}
	packet := rt.buildContinuityPacket(root, args)
	outDir := filepath.Join(root, userConfigDirName, "continuity")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	jsonPath := filepath.Join(outDir, "latest.json")
	mdPath := filepath.Join(outDir, "latest.md")
	data, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(mdPath, []byte(renderContinuityPacketMarkdown(packet)), 0o644); err != nil {
		return err
	}
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindContinuity,
		Severity: continuitySeverity(packet),
		Summary:  "continuity packet generated",
		ArtifactRefs: []string{
			mdPath,
			jsonPath,
		},
		Entities: map[string]string{
			"continuity":       packet.ID,
			"changed_files":    fmt.Sprintf("%d", len(packet.ChangedFiles)),
			"open_tasks":       fmt.Sprintf("%d", len(packet.OpenTasks)),
			"recent_errors":    fmt.Sprintf("%d", len(packet.RecentErrors)),
			"recovery_actions": fmt.Sprintf("%d", len(packet.RecoveryActions)),
		},
	})
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Generated continuity packet: "+mdPath))
	if len(packet.RecoveryActions) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Recovery actions:"))
		for _, action := range packet.RecoveryActions {
			fmt.Fprintln(rt.writer, "- "+action)
		}
	}
	return nil
}

func (rt *runtimeState) buildContinuityPacket(root string, note string) ContinuityPacket {
	now := time.Now()
	packet := ContinuityPacket{
		ID:              fmt.Sprintf("continuity-%s", now.Format("20060102-150405")),
		CreatedAt:       now,
		SessionID:       rt.session.ID,
		Workspace:       strings.TrimSpace(rt.session.WorkingDir),
		BaseRoot:        sessionBaseWorkingDir(rt.session),
		Branch:          delegationGitBranch(root),
		Provider:        rt.session.Provider,
		Model:           rt.session.Model,
		ActiveFeatureID: rt.session.ActiveFeatureID,
	}
	packet.ChangedFiles = delegationChangedFiles(root)
	packet.OpenTasks = delegationOpenTasks(rt.session)
	packet.Worktrees = continuityWorktreeSummaries(rt.session)
	packet.EditLoop = continuityEditLoopSummary(rt.session)
	packet.FailureRepair = continuityFailureRepairSummary(rt.session)
	if rt.session.LastVerification != nil {
		packet.LastVerification = rt.session.LastVerification.SummaryLine()
		packet.LastVerificationFailure = continuityVerificationFailureSummary(*rt.session.LastVerification)
	}
	rt.syncContinuityBackgroundJobs()
	packet.BackgroundJobs = continuityBackgroundJobSummaries(rt.session.BackgroundJobs)
	packet.BackgroundBundles = continuityBackgroundBundleSummaries(rt.session.BackgroundBundles)
	packet.RecentErrors = continuityRecentErrors(rt.session, 6)
	packet.ArtifactRefs = delegationArtifactRefs(rt.session.ConversationEvents, 16)
	packet.RecoveryActions = continuityRecoveryActions(rt.session, packet)
	packet.NextCommands = continuityNextCommands(packet)
	packet.SuggestedPrompt = continuitySuggestedPrompt(packet, note)
	return packet
}

func (rt *runtimeState) syncContinuityBackgroundJobs() {
	if rt == nil || rt.backgroundJobs == nil || rt.session == nil {
		return
	}
	jobs := rt.backgroundJobs.Snapshot()
	for _, job := range jobs {
		if strings.EqualFold(strings.TrimSpace(job.Status), "running") {
			_, _ = rt.backgroundJobs.SyncJob(job.ID)
		}
	}
	bundles := rt.backgroundJobs.SnapshotBundles()
	for _, bundle := range bundles {
		if strings.EqualFold(strings.TrimSpace(bundle.Status), "running") {
			_, _, _ = rt.backgroundJobs.SyncBundle(bundle.ID)
		}
	}
}

func continuitySeverity(packet ContinuityPacket) string {
	if len(packet.RecentErrors) > 0 || strings.TrimSpace(packet.LastVerificationFailure) != "" || continuityHasFailedBackground(packet) {
		return conversationSeverityWarn
	}
	return conversationSeverityInfo
}

func continuityHasFailedBackground(packet ContinuityPacket) bool {
	text := strings.ToLower(strings.Join(append(append([]string{}, packet.BackgroundJobs...), packet.BackgroundBundles...), " "))
	return strings.Contains(text, "[failed]") || strings.Contains(text, "[stale]")
}

func continuityWorktreeSummaries(session *Session) []string {
	if session == nil {
		return nil
	}
	out := []string{}
	if session.Worktree != nil {
		worktree := *session.Worktree
		worktree.Normalize()
		if strings.TrimSpace(worktree.Root) != "" {
			out = append(out, fmt.Sprintf("session root=%s branch=%s active=%t managed=%t", worktree.Root, valueOrUnset(worktree.Branch), worktree.Active, worktree.Managed))
		}
	}
	for _, lease := range session.SpecialistWorktrees {
		lease.Normalize()
		if strings.TrimSpace(lease.Root) == "" {
			continue
		}
		parts := []string{
			"specialist=" + lease.Specialist,
			"root=" + lease.Root,
		}
		if lease.Branch != "" {
			parts = append(parts, "branch="+lease.Branch)
		}
		if len(lease.OwnershipPaths) > 0 {
			parts = append(parts, "ownership="+strings.Join(lease.OwnershipPaths, ","))
		}
		if len(lease.NodeIDs) > 0 {
			parts = append(parts, "nodes="+strings.Join(lease.NodeIDs, ","))
		}
		out = append(out, strings.Join(parts, " "))
	}
	return normalizeTaskStateList(out, 16)
}

func continuityEditLoopSummary(session *Session) string {
	if session == nil {
		return ""
	}
	var loop EditLoopState
	if session.ActiveEditLoop != nil {
		loop = *session.ActiveEditLoop
	} else if len(session.EditLoops) > 0 {
		loop = session.EditLoops[0]
	} else {
		return ""
	}
	loop.Normalize()
	parts := []string{
		valueOrDefault(loop.ID, "edit-loop"),
		"status=" + valueOrUnset(loop.Status),
	}
	if loop.VerificationStatus != "" {
		parts = append(parts, "verification="+loop.VerificationStatus)
	}
	if len(loop.ChangedPaths) > 0 {
		parts = append(parts, "changed="+strings.Join(limitStrings(loop.ChangedPaths, 4), ","))
	}
	if loop.RetryCount > 0 {
		parts = append(parts, fmt.Sprintf("retries=%d", loop.RetryCount))
	}
	if len(loop.RemainingRisks) > 0 {
		parts = append(parts, fmt.Sprintf("remaining_risks=%d", len(loop.RemainingRisks)))
	}
	return strings.Join(parts, " ")
}

func continuityFailureRepairSummary(session *Session) string {
	if session == nil || session.ActiveFailureRepair == nil {
		return ""
	}
	return compactPromptSection(session.ActiveFailureRepair.RenderPromptSection(), 700)
}

func continuityVerificationFailureSummary(report VerificationReport) string {
	if !report.HasFailures() {
		return ""
	}
	failure := report.FirstFailure()
	if failure == nil {
		return compactPromptSection(report.FailureSummary(), 500)
	}
	parts := []string{
		valueOrDefault(failure.Label, "verification"),
	}
	if failure.Command != "" {
		parts = append(parts, "command="+failure.Command)
	}
	if failure.FailureKind != "" {
		parts = append(parts, "kind="+failure.FailureKind)
	}
	if first := firstMeaningfulFailureLine(failure.Output); first != "" {
		parts = append(parts, "first_error="+compactPromptSection(first, 220))
	}
	if failure.Hint != "" {
		parts = append(parts, "hint="+compactPromptSection(failure.Hint, 220))
	}
	return strings.Join(parts, " | ")
}

func continuityBackgroundJobSummaries(jobs []BackgroundShellJob) []string {
	out := make([]string, 0, len(jobs))
	for _, job := range jobs {
		job.Normalize()
		if job.ID == "" {
			continue
		}
		line := fmt.Sprintf("%s [%s] %s", job.ID, valueOrUnset(job.Status), valueOrUnset(job.CommandSummary))
		if job.OwnerNodeID != "" {
			line += " owner=" + job.OwnerNodeID
		}
		if job.ExitCode != nil {
			line += fmt.Sprintf(" exit=%d", *job.ExitCode)
		}
		if job.LastOutput != "" {
			line += " last=" + compactPromptSection(firstNonEmptyLine(job.LastOutput), 140)
		}
		out = append(out, line)
	}
	return normalizeTaskStateList(out, 16)
}

func continuityBackgroundBundleSummaries(bundles []BackgroundShellBundle) []string {
	out := make([]string, 0, len(bundles))
	for _, bundle := range bundles {
		bundle.Normalize()
		if bundle.ID == "" {
			continue
		}
		line := fmt.Sprintf("%s [%s] %s", bundle.ID, valueOrUnset(bundle.Status), valueOrUnset(firstNonBlankString(bundle.LastSummary, bundle.Summary)))
		if bundle.OwnerNodeID != "" {
			line += " owner=" + bundle.OwnerNodeID
		}
		if bundle.VerificationLike {
			line += " verification_like=true"
		}
		if bundle.LifecycleNote != "" {
			line += " note=" + compactPromptSection(bundle.LifecycleNote, 120)
		}
		out = append(out, line)
	}
	return normalizeTaskStateList(out, 16)
}

func continuityRecentErrors(session *Session, limit int) []string {
	if session == nil {
		return nil
	}
	events := latestEventsByKind(session.ConversationEvents,
		conversationEventKindProviderError,
		conversationEventKindCommandError,
		conversationEventKindToolError,
	)
	out := []string{}
	for _, event := range events {
		summary := renderRecentErrorAlternateSummary(event)
		if strings.TrimSpace(summary) == "" {
			summary = compactPromptSection(firstNonBlankString(event.Summary, event.Raw), 220)
		}
		if summary != "" {
			out = append(out, summary)
		}
		if len(out) >= limit {
			break
		}
	}
	return normalizeTaskStateList(out, limit)
}

func continuityRecoveryActions(session *Session, packet ContinuityPacket) []string {
	actions := []string{}
	if session != nil && session.ActiveFailureRepair != nil {
		for _, step := range session.ActiveFailureRepair.NextSteps {
			if strings.TrimSpace(step) != "" {
				actions = append(actions, step)
			}
		}
	}
	if session != nil && session.LastVerification != nil && session.LastVerification.HasFailures() {
		if failure := session.LastVerification.FirstFailure(); failure != nil {
			if failure.Command != "" {
				actions = append(actions, "Fix the first failing verification anchor, then rerun: "+failure.Command)
			} else {
				actions = append(actions, "Fix the first failing verification anchor, then rerun /verify.")
			}
			if failure.Hint != "" {
				actions = append(actions, "Use the verifier hint: "+compactPromptSection(failure.Hint, 220))
			}
		}
	}
	if session == nil {
		return normalizeTaskStateList(actions, 10)
	}
	for _, job := range session.BackgroundJobs {
		status := strings.ToLower(strings.TrimSpace(job.Status))
		if status == "failed" || status == "stale" {
			actions = append(actions, "Inspect background job "+job.ID+" with /jobs check "+job.ID+" before starting another equivalent command.")
		}
	}
	for _, bundle := range session.BackgroundBundles {
		status := strings.ToLower(strings.TrimSpace(bundle.Status))
		if status == "failed" || status == "stale" {
			actions = append(actions, "Inspect background bundle "+bundle.ID+" with /jobs bundle "+bundle.ID+" before restarting verification.")
		}
	}
	if len(packet.RecentErrors) > 0 {
		actions = append(actions, "Review the latest runtime error first; ask '방금 오류 원인 뭐야?' or open this continuity packet before retrying the same path.")
	}
	if len(actions) == 0 && len(packet.OpenTasks) > 0 {
		actions = append(actions, "Resume the first open task, preserve existing user changes, and run the narrowest relevant verification.")
	}
	return normalizeTaskStateList(actions, 10)
}

func continuityNextCommands(packet ContinuityPacket) []string {
	commands := []string{
		"/status",
		"/session-dashboard-html",
	}
	if len(packet.Worktrees) > 0 {
		commands = append(commands, "/worktree list")
	}
	if len(packet.BackgroundJobs) > 0 || len(packet.BackgroundBundles) > 0 {
		commands = append(commands, "/jobs status")
	}
	if strings.TrimSpace(packet.LastVerificationFailure) != "" || len(packet.ChangedFiles) > 0 {
		commands = append(commands, "/verify")
	}
	if len(packet.OpenTasks) > 0 {
		commands = append(commands, "/handoff continue from continuity packet")
	}
	return normalizeTaskStateList(commands, 10)
}

func continuitySuggestedPrompt(packet ContinuityPacket, note string) string {
	var b strings.Builder
	b.WriteString("Continue this Kernforge session from the continuity packet. ")
	if trimmed := strings.TrimSpace(note); trimmed != "" {
		b.WriteString("Goal: ")
		b.WriteString(trimmed)
		b.WriteString(". ")
	}
	if len(packet.RecoveryActions) > 0 {
		b.WriteString("Start with the listed recovery actions before launching new broad verification. ")
	}
	if len(packet.OpenTasks) > 0 {
		b.WriteString("Use the open task graph nodes as the execution queue. ")
	}
	b.WriteString("Preserve user changes, reuse active worktrees/background jobs, and run focused verification before finalizing.")
	return strings.TrimSpace(b.String())
}

func renderContinuityPacketMarkdown(packet ContinuityPacket) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Continuity Packet\n\n")
	fmt.Fprintf(&b, "ID: %s\n", packet.ID)
	fmt.Fprintf(&b, "Generated: %s\n", packet.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Session: %s\n", packet.SessionID)
	fmt.Fprintf(&b, "Workspace: %s\n", valueOrUnset(packet.Workspace))
	fmt.Fprintf(&b, "Base root: %s\n", valueOrUnset(packet.BaseRoot))
	fmt.Fprintf(&b, "Branch: %s\n", valueOrDefault(packet.Branch, "unknown"))
	fmt.Fprintf(&b, "Provider/model: %s / %s\n", valueOrUnset(packet.Provider), valueOrUnset(packet.Model))
	if strings.TrimSpace(packet.ActiveFeatureID) != "" {
		fmt.Fprintf(&b, "Active feature: %s\n", packet.ActiveFeatureID)
	}
	writeDelegationList(&b, "Changed Files", packet.ChangedFiles, "No changed files detected.")
	writeDelegationList(&b, "Open Tasks", packet.OpenTasks, "No open task graph nodes.")
	writeDelegationList(&b, "Worktrees", packet.Worktrees, "No isolated or task-owner worktrees recorded.")
	if strings.TrimSpace(packet.EditLoop) != "" {
		fmt.Fprintf(&b, "\n## Edit Loop\n\n%s\n", packet.EditLoop)
	}
	if strings.TrimSpace(packet.FailureRepair) != "" {
		fmt.Fprintf(&b, "\n## Failure Repair\n\n%s\n", packet.FailureRepair)
	}
	if strings.TrimSpace(packet.LastVerification) != "" {
		fmt.Fprintf(&b, "\n## Last Verification\n\n%s\n", packet.LastVerification)
	}
	if strings.TrimSpace(packet.LastVerificationFailure) != "" {
		fmt.Fprintf(&b, "\n## Last Verification Failure\n\n%s\n", packet.LastVerificationFailure)
	}
	writeDelegationList(&b, "Background Jobs", packet.BackgroundJobs, "No background jobs recorded.")
	writeDelegationList(&b, "Background Bundles", packet.BackgroundBundles, "No background bundles recorded.")
	writeDelegationList(&b, "Recent Errors", packet.RecentErrors, "No recent runtime errors recorded.")
	writeDelegationList(&b, "Recovery Actions", packet.RecoveryActions, "No active recovery actions.")
	writeDelegationList(&b, "Next Commands", packet.NextCommands, "No next commands suggested.")
	writeDelegationList(&b, "Artifact Refs", packet.ArtifactRefs, "No artifact refs recorded.")
	fmt.Fprintf(&b, "\n## Suggested Prompt\n\n%s\n", packet.SuggestedPrompt)
	return strings.TrimSpace(b.String())
}

func (rt *runtimeState) noteLocalShellCommand(command string, output string, err error) {
	if rt == nil || rt.session == nil {
		return
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}
	kind := conversationEventKindToolResult
	severity := conversationSeverityInfo
	summary := "shell command completed: " + summarizeShellCommand(command)
	raw := compactPromptSection(output, 1200)
	if err != nil {
		kind = conversationEventKindCommandError
		severity = conversationSeverityError
		summary = "shell command failed: " + summarizeShellCommand(command)
		raw = compactPromptSection(strings.TrimSpace(output+"\n"+err.Error()), 1200)
	}
	event := ConversationEvent{
		Kind:     kind,
		Severity: severity,
		Summary:  summary,
		Raw:      raw,
		Entities: map[string]string{
			"tool":    "shell",
			"command": command,
		},
	}
	rt.session.AppendConversationEvent(event)
	if err != nil {
		rt.appendRuntimeErrorConversationEvent(event, nil)
	}
	if rt.store != nil {
		_ = rt.store.Save(rt.session)
	}
}

func (rt *runtimeState) handleJobsCommand(args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	if rt.backgroundJobs == nil {
		return fmt.Errorf("background jobs are not configured")
	}
	rt.workspace.BackgroundJobs = rt.backgroundJobs
	fields := strings.Fields(strings.TrimSpace(args))
	subcommand := "status"
	if len(fields) > 0 {
		subcommand = strings.ToLower(strings.TrimSpace(fields[0]))
	}
	switch subcommand {
	case "", "status", "list":
		return rt.handleJobsStatus()
	case "check", "job":
		jobID := "latest"
		if len(fields) > 1 {
			jobID = fields[1]
		}
		return rt.handleJobsCheck(jobID)
	case "bundle", "check-bundle":
		bundleID := "latest"
		if len(fields) > 1 {
			bundleID = fields[1]
		}
		return rt.handleJobsBundle(bundleID)
	case "cancel":
		jobID := "latest"
		if len(fields) > 1 {
			jobID = fields[1]
		}
		reason := strings.TrimSpace(strings.Join(fields[2:], " "))
		return rt.handleJobsCancel(jobID, reason)
	case "cancel-bundle":
		bundleID := "latest"
		if len(fields) > 1 {
			bundleID = fields[1]
		}
		reason := strings.TrimSpace(strings.Join(fields[2:], " "))
		return rt.handleJobsCancelBundle(bundleID, reason)
	default:
		return fmt.Errorf("usage: /jobs [status|check <job-id|latest>|bundle <bundle-id|latest>|cancel <job-id|latest> [reason]|cancel-bundle <bundle-id|latest> [reason]]")
	}
}

func (rt *runtimeState) handleJobsStatus() error {
	rt.syncContinuityBackgroundJobs()
	rt.session.normalizeBackgroundJobs()
	rt.session.normalizeBackgroundBundles()
	fmt.Fprintln(rt.writer, rt.ui.section("Background Jobs"))
	if len(rt.session.BackgroundJobs) == 0 && len(rt.session.BackgroundBundles) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No background jobs or bundles recorded."))
		return nil
	}
	summary := summarizeJobsForCommand(rt.session.BackgroundJobs, rt.session.BackgroundBundles)
	fmt.Fprintln(rt.writer, rt.ui.statusKV("jobs", summary.jobs))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("bundles", summary.bundles))
	if len(rt.session.BackgroundJobs) > 0 {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, rt.ui.subsection("Jobs"))
		for _, job := range rt.session.BackgroundJobs {
			fmt.Fprintln(rt.writer, formatJobCommandLine(job))
		}
	}
	if len(rt.session.BackgroundBundles) > 0 {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, rt.ui.subsection("Bundles"))
		for _, bundle := range rt.session.BackgroundBundles {
			fmt.Fprintln(rt.writer, formatBundleCommandLine(bundle))
		}
	}
	return nil
}

func (rt *runtimeState) handleJobsCheck(jobID string) error {
	result, err := NewCheckShellJobTool(rt.workspace).ExecuteDetailed(context.Background(), map[string]any{
		"job_id": strings.TrimSpace(jobID),
	})
	if strings.TrimSpace(result.DisplayText) != "" {
		fmt.Fprintln(rt.writer, rt.ui.section("Background Job"))
		fmt.Fprintln(rt.writer, result.DisplayText)
	}
	return err
}

func (rt *runtimeState) handleJobsBundle(bundleID string) error {
	result, err := NewCheckShellBundleTool(rt.workspace).ExecuteDetailed(context.Background(), map[string]any{
		"bundle_id": strings.TrimSpace(bundleID),
	})
	if strings.TrimSpace(result.DisplayText) != "" {
		fmt.Fprintln(rt.writer, rt.ui.section("Background Bundle"))
		fmt.Fprintln(rt.writer, result.DisplayText)
	}
	return err
}

func (rt *runtimeState) handleJobsCancel(jobID string, reason string) error {
	result, err := NewCancelShellJobTool(rt.workspace).ExecuteDetailed(context.Background(), map[string]any{
		"job_id": strings.TrimSpace(jobID),
		"reason": reason,
	})
	if strings.TrimSpace(result.DisplayText) != "" {
		fmt.Fprintln(rt.writer, rt.ui.section("Cancel Background Job"))
		fmt.Fprintln(rt.writer, result.DisplayText)
	}
	return err
}

func (rt *runtimeState) handleJobsCancelBundle(bundleID string, reason string) error {
	result, err := NewCancelShellBundleTool(rt.workspace).ExecuteDetailed(context.Background(), map[string]any{
		"bundle_id": strings.TrimSpace(bundleID),
		"reason":    reason,
	})
	if strings.TrimSpace(result.DisplayText) != "" {
		fmt.Fprintln(rt.writer, rt.ui.section("Cancel Background Bundle"))
		fmt.Fprintln(rt.writer, result.DisplayText)
	}
	return err
}

type jobsCommandSummary struct {
	jobs    string
	bundles string
}

func summarizeJobsForCommand(jobs []BackgroundShellJob, bundles []BackgroundShellBundle) jobsCommandSummary {
	jobCounts := map[string]int{}
	for _, job := range jobs {
		job.Normalize()
		jobCounts[valueOrDefault(job.Status, "running")]++
	}
	bundleCounts := map[string]int{}
	for _, bundle := range bundles {
		bundle.Normalize()
		bundleCounts[valueOrDefault(bundle.Status, "running")]++
	}
	return jobsCommandSummary{
		jobs:    formatStatusCounts(jobCounts),
		bundles: formatStatusCounts(bundleCounts),
	}
}

func formatStatusCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "total=0"
	}
	order := []string{"running", "completed", "failed", "stale", "canceled", "preempted", "superseded"}
	total := 0
	for _, count := range counts {
		total += count
	}
	parts := []string{fmt.Sprintf("total=%d", total)}
	for _, status := range order {
		if count := counts[status]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", status, count))
		}
	}
	return strings.Join(parts, " ")
}

func formatJobCommandLine(job BackgroundShellJob) string {
	job.Normalize()
	line := fmt.Sprintf("- %s [%s] %s", job.ID, valueOrUnset(job.Status), valueOrUnset(job.CommandSummary))
	if job.OwnerNodeID != "" {
		line += " owner=" + job.OwnerNodeID
	}
	if job.ExitCode != nil {
		line += fmt.Sprintf(" exit=%d", *job.ExitCode)
	}
	if job.LogPath != "" {
		line += " log=" + job.LogPath
	}
	if job.LastOutput != "" {
		line += " last=" + compactPromptSection(firstNonEmptyLine(job.LastOutput), 120)
	}
	return line
}

func formatBundleCommandLine(bundle BackgroundShellBundle) string {
	bundle.Normalize()
	line := fmt.Sprintf("- %s [%s] %s", bundle.ID, valueOrUnset(bundle.Status), valueOrUnset(firstNonBlankString(bundle.LastSummary, bundle.Summary)))
	if len(bundle.JobIDs) > 0 {
		line += " jobs=" + strings.Join(bundle.JobIDs, ",")
	}
	if bundle.OwnerNodeID != "" {
		line += " owner=" + bundle.OwnerNodeID
	}
	if bundle.VerificationLike {
		line += " verification_like=true"
	}
	if bundle.LifecycleNote != "" {
		line += " note=" + compactPromptSection(bundle.LifecycleNote, 120)
	}
	return line
}
