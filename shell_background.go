package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

type BackgroundJobManager struct {
	root    string
	session *Session
	store   *SessionStore
}

type backgroundJobStatus struct {
	Status     string `json:"status"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

func NewBackgroundJobManager(root string, session *Session, store *SessionStore) *BackgroundJobManager {
	return &BackgroundJobManager{
		root:    root,
		session: session,
		store:   store,
	}
}

func (m *BackgroundJobManager) StartShellJob(shell string, workDir string, command string, assessment shellCommandAssessment, ownerNodeID string) (BackgroundShellJob, error) {
	if m == nil || m.session == nil || m.store == nil {
		return BackgroundShellJob{}, fmt.Errorf("background job manager is not configured")
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return BackgroundShellJob{}, fmt.Errorf("command is required")
	}
	now := shellJobNow()
	jobID := m.nextShellJobID(now)
	jobDir := filepath.Join(m.root, m.session.ID, jobID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return BackgroundShellJob{}, err
	}

	job := BackgroundShellJob{
		ID:             jobID,
		Command:        command,
		CommandSummary: summarizeShellCommand(command),
		OwnerNodeID:    strings.TrimSpace(ownerNodeID),
		WorkDir:        workDir,
		Status:         "running",
		MutationClass:  string(assessment.Class),
		StartedAt:      now,
		UpdatedAt:      now,
		LogPath:        filepath.Join(jobDir, "output.log"),
		StatusPath:     filepath.Join(jobDir, "status.json"),
	}

	name, args, scriptPath, err := prepareBackgroundShellRunner(shell, jobDir, command, job.LogPath, job.StatusPath)
	if err != nil {
		return BackgroundShellJob{}, err
	}
	job.ScriptPath = scriptPath
	if err := writeBackgroundJobStatus(job.StatusPath, backgroundJobStatus{Status: "running"}); err != nil {
		return BackgroundShellJob{}, err
	}

	cmd := exec.Command(name, args...)
	cmd.Dir = workDir
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return BackgroundShellJob{}, err
	}
	defer devNull.Close()
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return BackgroundShellJob{}, err
	}
	job.PID = cmd.Process.Pid
	job.UpdatedAt = shellJobNow()
	job.Normalize()
	m.session.UpsertBackgroundJob(job)
	if err := m.store.Save(m.session); err != nil {
		return BackgroundShellJob{}, err
	}
	return job, nil
}

func (m *BackgroundJobManager) nextShellJobID(now time.Time) string {
	baseID := fmt.Sprintf("job-%s-%09d", now.Format("20060102-150405"), now.Nanosecond())
	candidate := baseID
	suffix := 1
	for {
		if _, exists := m.session.BackgroundJob(candidate); !exists {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%02d", baseID, suffix)
		suffix++
	}
}

func (m *BackgroundJobManager) nextShellBundleID(now time.Time) string {
	baseID := fmt.Sprintf("bundle-%s-%09d", now.Format("20060102-150405"), now.Nanosecond())
	candidate := baseID
	suffix := 1
	for {
		if _, exists := m.session.BackgroundBundle(candidate); !exists {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%02d", baseID, suffix)
		suffix++
	}
}

func (m *BackgroundJobManager) FindReusableShellJob(command string, workDir string) (BackgroundShellJob, bool) {
	if m == nil || m.session == nil {
		return BackgroundShellJob{}, false
	}
	m.session.normalizeBackgroundJobs()
	normalizedCommand := strings.TrimSpace(command)
	normalizedWorkDir := strings.TrimSpace(workDir)
	for _, job := range m.session.BackgroundJobs {
		current := job
		if strings.TrimSpace(current.Status) == "running" {
			if synced, err := m.SyncJob(current.ID); err == nil {
				current = synced
			}
		}
		if !strings.EqualFold(strings.TrimSpace(current.Command), normalizedCommand) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(current.WorkDir), normalizedWorkDir) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(current.Status), "running") {
			return current, true
		}
	}
	return BackgroundShellJob{}, false
}

func (m *BackgroundJobManager) LatestJobID() string {
	if m == nil || m.session == nil {
		return ""
	}
	m.session.normalizeBackgroundJobs()
	if len(m.session.BackgroundJobs) == 0 {
		return ""
	}
	return strings.TrimSpace(m.session.BackgroundJobs[0].ID)
}

func (m *BackgroundJobManager) LatestBundleID() string {
	if m == nil || m.session == nil {
		return ""
	}
	m.session.normalizeBackgroundBundles()
	if len(m.session.BackgroundBundles) == 0 {
		return ""
	}
	return strings.TrimSpace(m.session.BackgroundBundles[0].ID)
}

func (m *BackgroundJobManager) SyncJob(jobID string) (BackgroundShellJob, error) {
	if m == nil || m.session == nil || m.store == nil {
		return BackgroundShellJob{}, fmt.Errorf("background job manager is not configured")
	}
	job, ok := m.session.BackgroundJob(jobID)
	if !ok {
		return BackgroundShellJob{}, fmt.Errorf("background job not found: %s", jobID)
	}
	if status, err := readBackgroundJobStatus(job.StatusPath); err == nil {
		if strings.TrimSpace(status.Status) != "" {
			job.Status = strings.TrimSpace(status.Status)
		}
		if status.ExitCode != nil {
			job.ExitCode = status.ExitCode
		}
		if strings.TrimSpace(status.FinishedAt) != "" {
			if finishedAt, parseErr := time.Parse(time.RFC3339Nano, status.FinishedAt); parseErr == nil {
				job.CompletedAt = finishedAt
			}
		}
	}
	if job.Status == "running" && job.PID > 0 && !backgroundProcessRunning(job.PID) {
		job.Status = "completed"
		if job.ExitCode == nil {
			zero := 0
			job.ExitCode = &zero
		}
		if job.CompletedAt.IsZero() {
			job.CompletedAt = shellJobNow()
		}
	}
	if tail, err := readFileTail(job.LogPath, shellOutputTailLimit); err == nil {
		job.LastOutput = strings.TrimSpace(normalizeShellOutputForDisplay(tail))
	}
	job.UpdatedAt = shellJobNow()
	job.Normalize()
	m.session.UpsertBackgroundJob(job)
	if err := m.store.Save(m.session); err != nil {
		return BackgroundShellJob{}, err
	}
	return job, nil
}

func (m *BackgroundJobManager) Snapshot() []BackgroundShellJob {
	if m == nil || m.session == nil {
		return nil
	}
	m.session.normalizeBackgroundJobs()
	return append([]BackgroundShellJob(nil), m.session.BackgroundJobs...)
}

func (m *BackgroundJobManager) SnapshotBundles() []BackgroundShellBundle {
	if m == nil || m.session == nil {
		return nil
	}
	m.session.normalizeBackgroundBundles()
	return append([]BackgroundShellBundle(nil), m.session.BackgroundBundles...)
}

func (m *BackgroundJobManager) FindReusableShellBundle(jobIDs []string) (BackgroundShellBundle, bool) {
	if m == nil || m.session == nil {
		return BackgroundShellBundle{}, false
	}
	target := normalizeBackgroundCommandList(jobIDs, 16)
	if len(target) == 0 {
		return BackgroundShellBundle{}, false
	}
	m.session.normalizeBackgroundBundles()
	for _, bundle := range m.session.BackgroundBundles {
		current := bundle
		if strings.EqualFold(strings.TrimSpace(current.Status), "running") {
			if synced, _, err := m.SyncBundle(current.ID); err == nil {
				current = synced
			}
		}
		if !strings.EqualFold(strings.TrimSpace(current.Status), "running") {
			continue
		}
		if slices.Equal(current.JobIDs, target) {
			return current, true
		}
	}
	return BackgroundShellBundle{}, false
}

func (m *BackgroundJobManager) FindBundleForJob(jobID string) (BackgroundShellBundle, bool) {
	if m == nil || m.session == nil {
		return BackgroundShellBundle{}, false
	}
	normalizedID := strings.TrimSpace(jobID)
	if normalizedID == "" {
		return BackgroundShellBundle{}, false
	}
	m.session.normalizeBackgroundBundles()
	for _, bundle := range m.session.BackgroundBundles {
		for _, currentJobID := range bundle.JobIDs {
			if strings.EqualFold(strings.TrimSpace(currentJobID), normalizedID) {
				return bundle, true
			}
		}
	}
	return BackgroundShellBundle{}, false
}

func (m *BackgroundJobManager) RecordShellBundle(jobs []BackgroundShellJob, ownerNodeID string) (BackgroundShellBundle, error) {
	if m == nil || m.session == nil || m.store == nil {
		return BackgroundShellBundle{}, fmt.Errorf("background job manager is not configured")
	}
	normalizedJobs := make([]BackgroundShellJob, 0, len(jobs))
	jobIDs := make([]string, 0, len(jobs))
	commandSummaries := make([]string, 0, len(jobs))
	for _, job := range jobs {
		job.Normalize()
		if strings.TrimSpace(job.ID) == "" {
			continue
		}
		normalizedJobs = append(normalizedJobs, job)
		jobIDs = append(jobIDs, job.ID)
		commandSummaries = append(commandSummaries, job.CommandSummary)
	}
	jobIDs = normalizeBackgroundCommandList(jobIDs, 16)
	commandSummaries = normalizeBackgroundCommandList(commandSummaries, 8)
	if len(jobIDs) == 0 {
		return BackgroundShellBundle{}, fmt.Errorf("at least one background shell job is required")
	}
	if reusable, ok := m.FindReusableShellBundle(jobIDs); ok {
		if ownerNodeID != "" && reusable.OwnerNodeID == "" {
			reusable.OwnerNodeID = strings.TrimSpace(ownerNodeID)
			reusable.Normalize()
			m.session.UpsertBackgroundBundle(reusable)
			_ = m.store.Save(m.session)
		}
		return reusable, nil
	}
	status, summary := summarizeBackgroundBundle(normalizedJobs)
	now := shellJobNow()
	bundle := BackgroundShellBundle{
		ID:               m.nextShellBundleID(now),
		Summary:          summarizeBackgroundBundleCommands(commandSummaries),
		CommandSummaries: commandSummaries,
		JobIDs:           jobIDs,
		OwnerNodeID:      strings.TrimSpace(ownerNodeID),
		Status:           status,
		LastSummary:      summary,
		StartedAt:        now,
		UpdatedAt:        now,
	}
	bundle.Normalize()
	m.supersedeMatchingBundles(bundle)
	m.session.UpsertBackgroundBundle(bundle)
	if err := m.store.Save(m.session); err != nil {
		return BackgroundShellBundle{}, err
	}
	return bundle, nil
}

func (m *BackgroundJobManager) supersedeMatchingBundles(bundle BackgroundShellBundle) {
	if m == nil || m.session == nil {
		return
	}
	targetCommands := normalizeBackgroundCommandList(bundle.CommandSummaries, 8)
	if len(targetCommands) == 0 {
		return
	}
	for _, existing := range m.session.BackgroundBundles {
		current := existing
		if strings.EqualFold(strings.TrimSpace(current.ID), strings.TrimSpace(bundle.ID)) {
			continue
		}
		if !slices.Equal(normalizeBackgroundCommandList(current.CommandSummaries, 8), targetCommands) {
			continue
		}
		status := strings.TrimSpace(strings.ToLower(current.Status))
		if status == "running" {
			if _, _, err := m.CancelBundle(current.ID, "superseded", "Replaced by newer background verification bundle.", bundle.ID); err == nil {
				continue
			}
		}
		current.Status = "superseded"
		current.SupersededBy = bundle.ID
		current.PreemptedBy = bundle.ID
		current.LifecycleNote = firstNonBlankString(current.LifecycleNote, "Replaced by newer background verification bundle.")
		current.UpdatedAt = shellJobNow()
		current.Normalize()
		m.session.UpsertBackgroundBundle(current)
		m.session.MarkBundleLifecycle(current.ID, current.Status, current.LifecycleNote)
	}
}

func (m *BackgroundJobManager) CancelJob(jobID string, reason string, preemptedBy string) (BackgroundShellJob, error) {
	if m == nil || m.session == nil || m.store == nil {
		return BackgroundShellJob{}, fmt.Errorf("background job manager is not configured")
	}
	job, ok := m.session.BackgroundJob(jobID)
	if !ok {
		return BackgroundShellJob{}, fmt.Errorf("background job not found: %s", jobID)
	}
	job.Normalize()
	status := strings.TrimSpace(strings.ToLower(job.Status))
	if status == "completed" || status == "failed" || status == "canceled" || status == "preempted" {
		return job, nil
	}
	now := shellJobNow()
	job.CancelRequestedAt = now
	job.CancelReason = strings.TrimSpace(reason)
	job.PreemptedBy = strings.TrimSpace(preemptedBy)
	if job.PID > 0 {
		_ = terminateBackgroundProcess(job.PID)
	}
	if job.PreemptedBy != "" {
		job.Status = "preempted"
	} else {
		job.Status = "canceled"
	}
	job.CanceledAt = now
	job.CompletedAt = now
	if job.ExitCode == nil {
		cancelCode := -1
		job.ExitCode = &cancelCode
	}
	if tail, err := readFileTail(job.LogPath, shellOutputTailLimit); err == nil {
		job.LastOutput = strings.TrimSpace(normalizeShellOutputForDisplay(tail))
	}
	_ = writeBackgroundJobStatus(job.StatusPath, backgroundJobStatus{
		Status:     job.Status,
		ExitCode:   job.ExitCode,
		FinishedAt: now.Format(time.RFC3339Nano),
	})
	job.UpdatedAt = now
	job.Normalize()
	m.session.UpsertBackgroundJob(job)
	if err := m.store.Save(m.session); err != nil {
		return BackgroundShellJob{}, err
	}
	return job, nil
}

func (m *BackgroundJobManager) CancelBundle(bundleID string, nextStatus string, reason string, preemptedBy string) (BackgroundShellBundle, []BackgroundShellJob, error) {
	if m == nil || m.session == nil || m.store == nil {
		return BackgroundShellBundle{}, nil, fmt.Errorf("background job manager is not configured")
	}
	bundle, ok := m.session.BackgroundBundle(bundleID)
	if !ok {
		return BackgroundShellBundle{}, nil, fmt.Errorf("background bundle not found: %s", bundleID)
	}
	normalizedStatus := strings.TrimSpace(strings.ToLower(nextStatus))
	if normalizedStatus == "" {
		normalizedStatus = "canceled"
	}
	now := shellJobNow()
	jobs := make([]BackgroundShellJob, 0, len(bundle.JobIDs))
	for _, jobID := range bundle.JobIDs {
		job, err := m.CancelJob(jobID, reason, preemptedBy)
		if err != nil {
			return BackgroundShellBundle{}, nil, err
		}
		jobs = append(jobs, job)
	}
	bundle.Normalize()
	bundle.Status = normalizedStatus
	bundle.CancelRequestedAt = now
	bundle.CanceledAt = now
	bundle.CancelReason = strings.TrimSpace(reason)
	bundle.PreemptedBy = strings.TrimSpace(preemptedBy)
	if normalizedStatus == "superseded" && bundle.PreemptedBy != "" {
		bundle.SupersededBy = bundle.PreemptedBy
	}
	if bundle.LifecycleNote == "" {
		bundle.LifecycleNote = strings.TrimSpace(reason)
	}
	bundle.UpdatedAt = now
	bundle.Normalize()
	m.session.UpsertBackgroundBundle(bundle)
	m.session.MarkBundleLifecycle(bundle.ID, bundle.Status, firstNonBlankString(bundle.LifecycleNote, bundle.CancelReason, bundle.LastSummary))
	if err := m.store.Save(m.session); err != nil {
		return BackgroundShellBundle{}, nil, err
	}
	return bundle, jobs, nil
}

func (m *BackgroundJobManager) SyncBundle(bundleID string) (BackgroundShellBundle, []BackgroundShellJob, error) {
	if m == nil || m.session == nil || m.store == nil {
		return BackgroundShellBundle{}, nil, fmt.Errorf("background job manager is not configured")
	}
	bundle, ok := m.session.BackgroundBundle(bundleID)
	if !ok {
		return BackgroundShellBundle{}, nil, fmt.Errorf("background bundle not found: %s", bundleID)
	}
	jobs := make([]BackgroundShellJob, 0, len(bundle.JobIDs))
	commandSummaries := make([]string, 0, len(bundle.CommandSummaries))
	for _, jobID := range bundle.JobIDs {
		job, err := m.SyncJob(jobID)
		if err != nil {
			return BackgroundShellBundle{}, nil, err
		}
		jobs = append(jobs, job)
		commandSummaries = append(commandSummaries, job.CommandSummary)
	}
	bundle.CommandSummaries = normalizeBackgroundCommandList(commandSummaries, 8)
	bundle.JobIDs = normalizeBackgroundCommandList(bundle.JobIDs, 16)
	summaryStatus, summaryText := summarizeBackgroundBundle(jobs)
	if bundleStatusIsOverridden(bundle.Status) {
		bundle.LastSummary = summaryText
	} else {
		bundle.Status = summaryStatus
		bundle.LastSummary = summaryText
	}
	if strings.TrimSpace(bundle.Summary) == "" {
		bundle.Summary = summarizeBackgroundBundleCommands(bundle.CommandSummaries)
	}
	bundle.UpdatedAt = shellJobNow()
	bundle.Normalize()
	m.session.UpsertBackgroundBundle(bundle)
	m.session.MarkBundleLifecycle(bundle.ID, bundle.Status, firstNonBlankString(bundle.LifecycleNote, bundle.LastSummary))
	if err := m.store.Save(m.session); err != nil {
		return BackgroundShellBundle{}, nil, err
	}
	return bundle, jobs, nil
}

func bundleStatusIsOverridden(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "stale", "superseded", "canceled", "preempted":
		return true
	default:
		return false
	}
}

func normalizeBackgroundCommandList(commands []string, limit int) []string {
	if limit <= 0 {
		limit = 4
	}
	normalized := make([]string, 0, len(commands))
	for _, command := range commands {
		trimmed := strings.TrimSpace(command)
		if trimmed == "" {
			continue
		}
		duplicate := false
		for _, existing := range normalized {
			if strings.EqualFold(existing, trimmed) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		normalized = append(normalized, trimmed)
		if len(normalized) >= limit {
			break
		}
	}
	return normalized
}

func summarizeBackgroundBundleCommands(commands []string) string {
	normalized := normalizeBackgroundCommandList(commands, 8)
	if len(normalized) == 0 {
		return ""
	}
	return strings.Join(normalized, " | ")
}

func summarizeBackgroundBundle(jobs []BackgroundShellJob) (string, string) {
	running := 0
	completed := 0
	failed := 0
	canceled := 0
	for _, job := range jobs {
		switch strings.TrimSpace(strings.ToLower(job.Status)) {
		case "completed":
			completed++
		case "failed":
			failed++
		case "canceled", "preempted":
			canceled++
		default:
			running++
		}
	}
	status := "running"
	if running == 0 {
		if failed > 0 {
			status = "failed"
		} else if canceled > 0 {
			status = "canceled"
		} else {
			status = "completed"
		}
	}
	return status, fmt.Sprintf("completed=%d running=%d failed=%d canceled=%d total=%d", completed, running, failed, canceled, len(jobs))
}

type RunBackgroundShellTool struct{ ws Workspace }

func NewRunBackgroundShellTool(ws Workspace) RunBackgroundShellTool {
	return RunBackgroundShellTool{ws: ws}
}

type RunShellBundleBackgroundTool struct{ ws Workspace }

func NewRunShellBundleBackgroundTool(ws Workspace) RunShellBundleBackgroundTool {
	return RunShellBundleBackgroundTool{ws: ws}
}

func (t RunShellBundleBackgroundTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "run_shell_bundle_background",
		Description: "Start multiple independent long-running shell commands in parallel and return persistent job ids.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"commands": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"owner_node_id": map[string]any{"type": "string"},
			},
			"required": []string{"commands"},
		},
	}
}

func (t RunShellBundleBackgroundTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t RunShellBundleBackgroundTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args := input.(map[string]any)
	commands := normalizeBackgroundCommandList(stringSliceValue(args, "commands"), 4)
	ownerNodeID := strings.TrimSpace(stringValue(args, "owner_node_id"))
	if len(commands) == 0 {
		return ToolExecutionResult{}, fmt.Errorf("commands are required")
	}
	if t.ws.BackgroundJobs == nil {
		return ToolExecutionResult{}, fmt.Errorf("background jobs are not configured")
	}
	lines := make([]string, 0, len(commands)+3)
	lines = append(lines, fmt.Sprintf("started background shell bundle with %d command(s)", len(commands)))
	bundleJobs := make([]BackgroundShellJob, 0, len(commands))
	for _, command := range commands {
		assessment := assessShellCommandMutation(command)
		if assessment.Class == shellMutationWorkspaceWrite {
			return ToolExecutionResult{}, fmt.Errorf("run_shell_bundle_background only supports read-only, verification/build, cache-only, or external-install commands")
		}
		if _, err := t.ws.Hook(ctx, HookPreToolUse, HookPayload{
			"tool_name": "run_shell_bundle_background",
			"tool_kind": "shell",
			"command":   command,
			"risk_tags": hookCommandRiskTags(command),
			"file_tags": []string{},
		}); err != nil {
			return ToolExecutionResult{}, err
		}
		if err := t.ws.EnsureShell(command); err != nil {
			return ToolExecutionResult{}, err
		}
		if reusable, ok := t.ws.BackgroundJobs.FindReusableShellJob(command, t.ws.Root); ok {
			if ownerNodeID != "" && reusable.OwnerNodeID == "" {
				reusable.OwnerNodeID = ownerNodeID
				t.ws.BackgroundJobs.session.UpsertBackgroundJob(reusable)
			}
			if _, err := t.ws.Hook(ctx, HookPostToolUse, HookPayload{
				"tool_name": "run_shell_bundle_background",
				"tool_kind": "shell",
				"command":   command,
				"risk_tags": hookCommandRiskTags(command),
				"output":    fmt.Sprintf("reused job=%s status=%s", reusable.ID, reusable.Status),
			}); err != nil {
				return ToolExecutionResult{}, err
			}
			bundleJobs = append(bundleJobs, reusable)
			lines = append(lines, fmt.Sprintf("- reused %s [%s] %s", reusable.ID, reusable.Status, reusable.CommandSummary))
			continue
		}
		job, err := t.ws.BackgroundJobs.StartShellJob(t.ws.Shell, t.ws.Root, command, assessment, ownerNodeID)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		if _, err := t.ws.Hook(ctx, HookPostToolUse, HookPayload{
			"tool_name": "run_shell_bundle_background",
			"tool_kind": "shell",
			"command":   command,
			"risk_tags": hookCommandRiskTags(command),
			"output":    fmt.Sprintf("job=%s status=%s", job.ID, job.Status),
		}); err != nil {
			return ToolExecutionResult{}, err
		}
		bundleJobs = append(bundleJobs, job)
		lines = append(lines, fmt.Sprintf("- started %s [%s] %s", job.ID, job.Status, job.CommandSummary))
	}
	bundle, err := t.ws.BackgroundJobs.RecordShellBundle(bundleJobs, ownerNodeID)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	lines = append(lines, fmt.Sprintf("bundle: %s", bundle.ID))
	lines = append(lines, fmt.Sprintf("bundle_status: %s | summary: %s", bundle.Status, bundle.LastSummary))
	lines = append(lines, "Use check_shell_bundle with the returned job ids, or check_shell_job for an individual job.")
	return ToolExecutionResult{
		DisplayText: strings.Join(lines, "\n"),
		Meta: buildBackgroundBundleMeta(bundle, bundleJobs, map[string]any{
			"tool_name":    "run_shell_bundle_background",
			"plan_effect":  "progress",
			"result_class": "background_start",
		}),
	}, nil
}

func (t RunBackgroundShellTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "run_shell_background",
		Description: "Start a long-running shell command in the background and return a persistent job id.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":       map[string]any{"type": "string"},
				"owner_node_id": map[string]any{"type": "string"},
			},
			"required": []string{"command"},
		},
	}
}

func (t RunBackgroundShellTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t RunBackgroundShellTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args := input.(map[string]any)
	command := strings.TrimSpace(stringValue(args, "command"))
	ownerNodeID := strings.TrimSpace(stringValue(args, "owner_node_id"))
	if command == "" {
		return ToolExecutionResult{}, fmt.Errorf("command is required")
	}
	if t.ws.BackgroundJobs == nil {
		return ToolExecutionResult{}, fmt.Errorf("background jobs are not configured")
	}
	assessment := assessShellCommandMutation(command)
	if assessment.Class == shellMutationWorkspaceWrite {
		return ToolExecutionResult{}, fmt.Errorf("run_shell_background only supports read-only, verification/build, cache-only, or external-install commands")
	}
	if _, err := t.ws.Hook(ctx, HookPreToolUse, HookPayload{
		"tool_name": "run_shell_background",
		"tool_kind": "shell",
		"command":   command,
		"risk_tags": hookCommandRiskTags(command),
		"file_tags": []string{},
	}); err != nil {
		return ToolExecutionResult{}, err
	}
	if err := t.ws.EnsureShell(command); err != nil {
		return ToolExecutionResult{}, err
	}
	if reusable, ok := t.ws.BackgroundJobs.FindReusableShellJob(command, t.ws.Root); ok {
		if ownerNodeID != "" && reusable.OwnerNodeID == "" {
			reusable.OwnerNodeID = ownerNodeID
			t.ws.BackgroundJobs.session.UpsertBackgroundJob(reusable)
		}
		bundle, bundleErr := t.ws.BackgroundJobs.RecordShellBundle([]BackgroundShellJob{reusable}, ownerNodeID)
		if bundleErr != nil {
			return ToolExecutionResult{}, bundleErr
		}
		if _, err := t.ws.Hook(ctx, HookPostToolUse, HookPayload{
			"tool_name": "run_shell_background",
			"tool_kind": "shell",
			"command":   command,
			"risk_tags": hookCommandRiskTags(command),
			"output":    fmt.Sprintf("reused job=%s status=%s", reusable.ID, reusable.Status),
		}); err != nil {
			return ToolExecutionResult{}, err
		}
		displayText := fmt.Sprintf("reusing background shell job %s [%s]\ncommand: %s\nlog: %s\nbundle: %s\nbundle_status: %s\nUse check_shell_job with this job id to poll progress.", reusable.ID, reusable.Status, reusable.CommandSummary, relOrAbs(t.ws.Root, reusable.LogPath), bundle.ID, bundle.Status)
		return ToolExecutionResult{
			DisplayText: displayText,
			Meta: buildBackgroundJobMeta(reusable, &bundle, map[string]any{
				"tool_name":    "run_shell_background",
				"reused":       true,
				"plan_effect":  "progress",
				"result_class": "background_start",
			}),
		}, nil
	}
	job, err := t.ws.BackgroundJobs.StartShellJob(t.ws.Shell, t.ws.Root, command, assessment, ownerNodeID)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	bundle, err := t.ws.BackgroundJobs.RecordShellBundle([]BackgroundShellJob{job}, ownerNodeID)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	if _, err := t.ws.Hook(ctx, HookPostToolUse, HookPayload{
		"tool_name": "run_shell_background",
		"tool_kind": "shell",
		"command":   command,
		"risk_tags": hookCommandRiskTags(command),
		"output":    fmt.Sprintf("job=%s status=%s", job.ID, job.Status),
	}); err != nil {
		return ToolExecutionResult{}, err
	}
	displayText := fmt.Sprintf("started background shell job %s [%s]\ncommand: %s\nlog: %s\nstatus: %s\nstatus_file: %s\nbundle: %s\nbundle_status: %s\nUse check_shell_job with this job id to poll progress.", job.ID, job.Status, job.CommandSummary, relOrAbs(t.ws.Root, job.LogPath), job.Status, relOrAbs(t.ws.Root, job.StatusPath), bundle.ID, bundle.Status)
	return ToolExecutionResult{
		DisplayText: displayText,
		Meta: buildBackgroundJobMeta(job, &bundle, map[string]any{
			"tool_name":    "run_shell_background",
			"plan_effect":  "progress",
			"result_class": "background_start",
		}),
	}, nil
}

type CheckShellJobTool struct{ ws Workspace }

func NewCheckShellJobTool(ws Workspace) CheckShellJobTool {
	return CheckShellJobTool{ws: ws}
}

type CheckShellBundleTool struct{ ws Workspace }

func NewCheckShellBundleTool(ws Workspace) CheckShellBundleTool {
	return CheckShellBundleTool{ws: ws}
}

type CancelShellJobTool struct{ ws Workspace }

func NewCancelShellJobTool(ws Workspace) CancelShellJobTool {
	return CancelShellJobTool{ws: ws}
}

type CancelShellBundleTool struct{ ws Workspace }

func NewCancelShellBundleTool(ws Workspace) CancelShellBundleTool {
	return CancelShellBundleTool{ws: ws}
}

func (t CheckShellBundleTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "check_shell_bundle",
		Description: "Poll multiple background shell jobs and summarize their combined status.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"bundle_id": map[string]any{"type": "string"},
				"job_ids": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
			},
		},
	}
}

func (t CheckShellBundleTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t CheckShellBundleTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	_ = ctx
	if t.ws.BackgroundJobs == nil {
		return ToolExecutionResult{}, fmt.Errorf("background jobs are not configured")
	}
	args := input.(map[string]any)
	bundleID := strings.TrimSpace(stringValue(args, "bundle_id"))
	jobIDs := normalizeBackgroundCommandList(stringSliceValue(args, "job_ids"), 8)
	if bundleID != "" || len(jobIDs) == 0 {
		if bundleID == "" || strings.EqualFold(bundleID, "latest") {
			bundleID = t.ws.BackgroundJobs.LatestBundleID()
		}
		if bundleID == "" {
			return ToolExecutionResult{}, fmt.Errorf("bundle_id or job_ids is required")
		}
		bundle, jobs, err := t.ws.BackgroundJobs.SyncBundle(bundleID)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		lines := []string{
			fmt.Sprintf("bundle: %s", bundle.ID),
			fmt.Sprintf("bundle_status: %s", bundle.Status),
			fmt.Sprintf("summary: %s", bundle.LastSummary),
		}
		for _, job := range jobs {
			line := fmt.Sprintf("- %s [%s] %s", job.ID, job.Status, job.CommandSummary)
			if job.LastOutput != "" {
				line += " | last=" + compactPromptSection(firstNonEmptyLine(job.LastOutput), 120)
			}
			lines = append(lines, line)
		}
		return ToolExecutionResult{
			DisplayText: strings.Join(lines, "\n"),
			Meta: buildBackgroundBundleMeta(bundle, jobs, map[string]any{
				"tool_name":    "check_shell_bundle",
				"result_class": "background_status",
			}),
		}, nil
	}
	running := 0
	completed := 0
	failed := 0
	canceled := 0
	lines := make([]string, 0, len(jobIDs)+2)
	jobs := make([]BackgroundShellJob, 0, len(jobIDs))
	for _, jobID := range jobIDs {
		job, err := t.ws.BackgroundJobs.SyncJob(jobID)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		jobs = append(jobs, job)
		switch strings.TrimSpace(job.Status) {
		case "completed":
			completed++
		case "failed":
			failed++
		case "canceled", "preempted":
			canceled++
		default:
			running++
		}
		line := fmt.Sprintf("- %s [%s] %s", job.ID, job.Status, job.CommandSummary)
		if job.LastOutput != "" {
			line += " | last=" + compactPromptSection(firstNonEmptyLine(job.LastOutput), 120)
		}
		lines = append(lines, line)
	}
	summary := fmt.Sprintf("summary: completed=%d running=%d failed=%d canceled=%d total=%d", completed, running, failed, canceled, len(jobIDs))
	return ToolExecutionResult{
		DisplayText: summary + "\n" + strings.Join(lines, "\n"),
		Meta: map[string]any{
			"tool_name":    "check_shell_bundle",
			"result_class": "background_status",
			"running":      running,
			"completed":    completed,
			"failed":       failed,
			"canceled":     canceled,
			"total":        len(jobIDs),
			"job_ids":      jobIDs,
			"job_status":   buildBackgroundJobStatusList(jobs),
		},
	}, nil
}

func (t CheckShellJobTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "check_shell_job",
		Description: "Poll the status and recent output of a background shell job started by run_shell_background.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"job_id": map[string]any{"type": "string"},
			},
		},
	}
}

func (t CheckShellJobTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t CheckShellJobTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	_ = ctx
	if t.ws.BackgroundJobs == nil {
		return ToolExecutionResult{}, fmt.Errorf("background jobs are not configured")
	}
	args := input.(map[string]any)
	jobID := strings.TrimSpace(stringValue(args, "job_id"))
	if jobID == "" || strings.EqualFold(jobID, "latest") {
		jobID = t.ws.BackgroundJobs.LatestJobID()
	}
	if jobID == "" {
		return ToolExecutionResult{}, fmt.Errorf("job_id is required")
	}
	job, err := t.ws.BackgroundJobs.SyncJob(jobID)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	var bundleMeta *BackgroundShellBundle
	if bundle, ok := t.ws.BackgroundJobs.FindBundleForJob(job.ID); ok {
		syncedBundle, _, syncErr := t.ws.BackgroundJobs.SyncBundle(bundle.ID)
		if syncErr == nil {
			bundleMeta = &syncedBundle
		} else {
			bundleCopy := bundle
			bundleMeta = &bundleCopy
		}
	}
	lines := []string{
		fmt.Sprintf("job: %s", job.ID),
		fmt.Sprintf("status: %s", job.Status),
		fmt.Sprintf("command: %s", job.CommandSummary),
	}
	if job.ExitCode != nil {
		lines = append(lines, fmt.Sprintf("exit_code: %d", *job.ExitCode))
	}
	if !job.CompletedAt.IsZero() {
		lines = append(lines, "completed_at: "+job.CompletedAt.Format(time.RFC3339))
	}
	lines = append(lines, "log: "+relOrAbs(t.ws.Root, job.LogPath))
	if strings.TrimSpace(job.LastOutput) != "" {
		lines = append(lines, "", job.LastOutput)
	}
	return ToolExecutionResult{
		DisplayText: strings.Join(lines, "\n"),
		Meta: buildBackgroundJobMeta(job, bundleMeta, map[string]any{
			"tool_name":    "check_shell_job",
			"result_class": "background_status",
		}),
	}, nil
}

func (t CancelShellJobTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "cancel_shell_job",
		Description: "Cancel a background shell job when it is stale, superseded, or no longer useful.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"job_id": map[string]any{"type": "string"},
				"reason": map[string]any{"type": "string"},
			},
		},
	}
}

func (t CancelShellJobTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t CancelShellJobTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	_ = ctx
	if t.ws.BackgroundJobs == nil {
		return ToolExecutionResult{}, fmt.Errorf("background jobs are not configured")
	}
	args := input.(map[string]any)
	jobID := strings.TrimSpace(stringValue(args, "job_id"))
	if jobID == "" || strings.EqualFold(jobID, "latest") {
		jobID = t.ws.BackgroundJobs.LatestJobID()
	}
	if jobID == "" {
		return ToolExecutionResult{}, fmt.Errorf("job_id is required")
	}
	reason := firstNonBlankString(stringValue(args, "reason"), "Background job canceled by tool request.")
	job, err := t.ws.BackgroundJobs.CancelJob(jobID, reason, "")
	if err != nil {
		return ToolExecutionResult{}, err
	}
	lines := []string{
		fmt.Sprintf("job: %s", job.ID),
		fmt.Sprintf("status: %s", job.Status),
		fmt.Sprintf("reason: %s", job.CancelReason),
	}
	return ToolExecutionResult{
		DisplayText: strings.Join(lines, "\n"),
		Meta: mergeBackgroundPolicyMeta(buildBackgroundJobMeta(job, nil, map[string]any{
			"tool_name":    "cancel_shell_job",
			"plan_effect":  "progress",
			"result_class": "background_cancel",
		}), job.OwnerNodeID),
	}, nil
}

func (t CancelShellBundleTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "cancel_shell_bundle",
		Description: "Cancel a background shell bundle when it is stale, superseded, or no longer useful.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"bundle_id": map[string]any{"type": "string"},
				"reason":    map[string]any{"type": "string"},
			},
		},
	}
}

func (t CancelShellBundleTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t CancelShellBundleTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	_ = ctx
	if t.ws.BackgroundJobs == nil {
		return ToolExecutionResult{}, fmt.Errorf("background jobs are not configured")
	}
	args := input.(map[string]any)
	bundleID := strings.TrimSpace(stringValue(args, "bundle_id"))
	if bundleID == "" || strings.EqualFold(bundleID, "latest") {
		bundleID = t.ws.BackgroundJobs.LatestBundleID()
	}
	if bundleID == "" {
		return ToolExecutionResult{}, fmt.Errorf("bundle_id is required")
	}
	reason := firstNonBlankString(stringValue(args, "reason"), "Background bundle canceled by tool request.")
	bundle, jobs, err := t.ws.BackgroundJobs.CancelBundle(bundleID, "canceled", reason, "")
	if err != nil {
		return ToolExecutionResult{}, err
	}
	lines := []string{
		fmt.Sprintf("bundle: %s", bundle.ID),
		fmt.Sprintf("bundle_status: %s", bundle.Status),
		fmt.Sprintf("reason: %s", bundle.CancelReason),
	}
	for _, job := range jobs {
		lines = append(lines, fmt.Sprintf("- %s [%s] %s", job.ID, job.Status, job.CommandSummary))
	}
	return ToolExecutionResult{
		DisplayText: strings.Join(lines, "\n"),
		Meta: mergeBackgroundPolicyMeta(buildBackgroundBundleMeta(bundle, jobs, map[string]any{
			"tool_name":    "cancel_shell_bundle",
			"plan_effect":  "progress",
			"result_class": "background_cancel",
		}), bundle.OwnerNodeID),
	}, nil
}

func buildBackgroundBundleMeta(bundle BackgroundShellBundle, jobs []BackgroundShellJob, extra map[string]any) map[string]any {
	meta := cloneMetaMap(extra)
	bundle.Normalize()
	meta["bundle_id"] = bundle.ID
	meta["bundle_status"] = bundle.Status
	meta["bundle_summary"] = bundle.LastSummary
	meta["bundle_command_summaries"] = append([]string(nil), bundle.CommandSummaries...)
	meta["bundle_job_ids"] = append([]string(nil), bundle.JobIDs...)
	if bundle.OwnerNodeID != "" {
		meta["owner_node_id"] = bundle.OwnerNodeID
	}
	if bundle.SupersededBy != "" {
		meta["superseded_by"] = bundle.SupersededBy
	}
	if bundle.LifecycleNote != "" {
		meta["lifecycle_note"] = bundle.LifecycleNote
	}
	if bundle.CancelReason != "" {
		meta["cancel_reason"] = bundle.CancelReason
	}
	if bundle.PreemptedBy != "" {
		meta["preempted_by"] = bundle.PreemptedBy
	}
	running := 0
	completed := 0
	failed := 0
	canceled := 0
	for _, job := range jobs {
		switch strings.TrimSpace(strings.ToLower(job.Status)) {
		case "completed":
			completed++
		case "failed":
			failed++
		case "canceled", "preempted":
			canceled++
		default:
			running++
		}
	}
	meta["running"] = running
	meta["completed"] = completed
	meta["failed"] = failed
	meta["canceled"] = canceled
	meta["total"] = len(jobs)
	meta["job_status"] = buildBackgroundJobStatusList(jobs)
	return meta
}

func buildBackgroundJobMeta(job BackgroundShellJob, bundle *BackgroundShellBundle, extra map[string]any) map[string]any {
	meta := cloneMetaMap(extra)
	job.Normalize()
	meta["job_id"] = job.ID
	meta["job_status"] = job.Status
	meta["command_summary"] = job.CommandSummary
	meta["log_path"] = job.LogPath
	if job.OwnerNodeID != "" {
		meta["owner_node_id"] = job.OwnerNodeID
	}
	if job.ExitCode != nil {
		meta["exit_code"] = *job.ExitCode
	}
	if job.CancelReason != "" {
		meta["cancel_reason"] = job.CancelReason
	}
	if job.PreemptedBy != "" {
		meta["preempted_by"] = job.PreemptedBy
	}
	if bundle != nil {
		bundle.Normalize()
		meta["bundle_id"] = bundle.ID
		meta["bundle_status"] = bundle.Status
		meta["bundle_summary"] = bundle.LastSummary
		meta["bundle_job_ids"] = append([]string(nil), bundle.JobIDs...)
		meta["bundle_command_summaries"] = append([]string(nil), bundle.CommandSummaries...)
		if bundle.OwnerNodeID != "" {
			meta["owner_node_id"] = bundle.OwnerNodeID
		}
	}
	return meta
}

func mergeBackgroundPolicyMeta(meta map[string]any, ownerNodeID string) map[string]any {
	merged := cloneMetaMap(meta)
	if strings.TrimSpace(ownerNodeID) != "" {
		merged["owner_node_id"] = strings.TrimSpace(ownerNodeID)
	}
	return merged
}

func buildBackgroundJobStatusList(jobs []BackgroundShellJob) []map[string]any {
	if len(jobs) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		job.Normalize()
		items = append(items, map[string]any{
			"id":              job.ID,
			"status":          job.Status,
			"owner_node_id":   job.OwnerNodeID,
			"command_summary": job.CommandSummary,
			"cancel_reason":   job.CancelReason,
			"preempted_by":    job.PreemptedBy,
		})
	}
	return items
}

func cloneMetaMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func prepareBackgroundShellRunner(shell string, jobDir string, command string, logPath string, statusPath string) (string, []string, string, error) {
	base := strings.ToLower(strings.TrimSpace(shell))
	switch {
	case strings.Contains(base, "powershell") || strings.Contains(base, "pwsh"):
		commandPath := filepath.Join(jobDir, "command.ps1")
		scriptPath := filepath.Join(jobDir, "runner.ps1")
		if err := os.WriteFile(commandPath, []byte(command+"\n"), 0o644); err != nil {
			return "", nil, "", err
		}
		script := buildPowerShellBackgroundRunner(commandPath, logPath, statusPath)
		if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
			return "", nil, "", err
		}
		return shell, []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath}, scriptPath, nil
	case strings.Contains(base, "bash"):
		commandPath := filepath.Join(jobDir, "command.sh")
		scriptPath := filepath.Join(jobDir, "runner.sh")
		if err := os.WriteFile(commandPath, []byte(command+"\n"), 0o755); err != nil {
			return "", nil, "", err
		}
		script := buildPOSIXBackgroundRunner("bash", commandPath, logPath, statusPath)
		if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
			return "", nil, "", err
		}
		return shell, []string{scriptPath}, scriptPath, nil
	case base == "sh":
		commandPath := filepath.Join(jobDir, "command.sh")
		scriptPath := filepath.Join(jobDir, "runner.sh")
		if err := os.WriteFile(commandPath, []byte(command+"\n"), 0o755); err != nil {
			return "", nil, "", err
		}
		script := buildPOSIXBackgroundRunner("sh", commandPath, logPath, statusPath)
		if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
			return "", nil, "", err
		}
		return shell, []string{scriptPath}, scriptPath, nil
	default:
		return "", nil, "", fmt.Errorf("background shell jobs are only supported for PowerShell, bash, or sh")
	}
}

func buildPowerShellBackgroundRunner(commandPath string, logPath string, statusPath string) string {
	return strings.Join([]string{
		"$ErrorActionPreference = 'Continue'",
		"[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()",
		"$OutputEncoding = [System.Text.UTF8Encoding]::new()",
		fmt.Sprintf("$logPath = '%s'", powershellLiteralString(logPath)),
		fmt.Sprintf("$statusPath = '%s'", powershellLiteralString(statusPath)),
		"$exitCode = 0",
		"$status = 'completed'",
		"$invokeOk = $true",
		"try",
		"{",
		fmt.Sprintf("    & '%s' *>> $logPath", powershellLiteralString(commandPath)),
		"    if (-not $?)",
		"    {",
		"        $invokeOk = $false",
		"    }",
		"    if ($LASTEXITCODE -is [int] -and $LASTEXITCODE -ne 0)",
		"    {",
		"        $exitCode = $LASTEXITCODE",
		"        $status = 'failed'",
		"    }",
		"}",
		"catch",
		"{",
		"    $_ | Out-File -FilePath $logPath -Encoding utf8 -Append",
		"    $status = 'failed'",
		"    if ($exitCode -eq 0)",
		"    {",
		"        $exitCode = 1",
		"    }",
		"}",
		"finally",
		"{",
		"    if (-not $invokeOk -and $exitCode -eq 0)",
		"    {",
		"        $exitCode = 1",
		"        $status = 'failed'",
		"    }",
		"    $payload = @{",
		"        status = $status",
		"        exit_code = $exitCode",
		"        finished_at = (Get-Date).ToString('o')",
		"    } | ConvertTo-Json -Compress",
		"    Set-Content -LiteralPath $statusPath -Value $payload -Encoding utf8",
		"    exit $exitCode",
		"}",
		"",
	}, "\n")
}

func buildPOSIXBackgroundRunner(shellName string, commandPath string, logPath string, statusPath string) string {
	return strings.Join([]string{
		"#!/usr/bin/env " + shellName,
		"set +e",
		fmt.Sprintf("%s %s >>%s 2>&1", shellName, bashSingleQuote(commandPath), bashSingleQuote(logPath)),
		"code=$?",
		"status=\"completed\"",
		"if [ \"$code\" -ne 0 ]; then",
		"  status=\"failed\"",
		"fi",
		fmt.Sprintf("printf '{\"status\":\"%%s\",\"exit_code\":%%d,\"finished_at\":\"%%s\"}\\n' \"$status\" \"$code\" \"$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)\" > %s", bashSingleQuote(statusPath)),
		"exit \"$code\"",
		"",
	}, "\n")
}

func powershellLiteralString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func bashSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func writeBackgroundJobStatus(path string, status backgroundJobStatus) error {
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func readBackgroundJobStatus(path string) (backgroundJobStatus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return backgroundJobStatus{}, err
	}
	var status backgroundJobStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return backgroundJobStatus{}, err
	}
	return status, nil
}

func readFileTail(path string, limit int) ([]byte, error) {
	if limit <= 0 {
		limit = shellOutputTailLimit
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	offset := int64(0)
	if size > int64(limit) {
		offset = size - int64(limit)
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func backgroundProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
		if err != nil {
			return false
		}
		text := strings.ToLower(strings.TrimSpace(string(out)))
		if text == "" || strings.Contains(text, "no tasks are running") {
			return false
		}
		return strings.Contains(text, "\""+strconv.Itoa(pid)+"\"")
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "pid=").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

func terminateBackgroundProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	if runtime.GOOS == "windows" {
		if err := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run(); err == nil {
			return nil
		}
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
