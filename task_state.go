package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type TaskState struct {
	Goal                     string      `json:"goal,omitempty"`
	Phase                    string      `json:"phase,omitempty"`
	PlanSummary              string      `json:"plan_summary,omitempty"`
	PlanApproved             bool        `json:"plan_approved,omitempty"`
	PlanRefreshCount         int         `json:"plan_refresh_count,omitempty"`
	PlanCursor               int         `json:"plan_cursor,omitempty"`
	PlannerProfile           string      `json:"planner_profile,omitempty"`
	ReviewerProfile          string      `json:"reviewer_profile,omitempty"`
	CurrentHypothesis        string      `json:"current_hypothesis,omitempty"`
	ConfirmedFacts           []string    `json:"confirmed_facts,omitempty"`
	FailedAttempts           []string    `json:"failed_attempts,omitempty"`
	CompletedSteps           []string    `json:"completed_steps,omitempty"`
	PendingChecks            []string    `json:"pending_checks,omitempty"`
	ReviewerGuidance         string      `json:"reviewer_guidance,omitempty"`
	ExecutorFocusNode        string      `json:"executor_focus_node,omitempty"`
	ExecutorAction           string      `json:"executor_action,omitempty"`
	ExecutorReason           string      `json:"executor_reason,omitempty"`
	ExecutorGuidance         string      `json:"executor_guidance,omitempty"`
	ExecutorParallelNodes    []string    `json:"executor_parallel_nodes,omitempty"`
	ExecutorParallelGuidance []string    `json:"executor_parallel_guidance,omitempty"`
	LastRecoveryReason       string      `json:"last_recovery_reason,omitempty"`
	FinalReviewCount         int         `json:"final_review_count,omitempty"`
	FinalReviewVerdict       string      `json:"final_review_verdict,omitempty"`
	NextStep                 string      `json:"next_step,omitempty"`
	Events                   []TaskEvent `json:"events,omitempty"`
	LastUpdated              time.Time   `json:"last_updated,omitempty"`
}

type TaskEvent struct {
	Kind       string    `json:"kind,omitempty"`
	NodeID     string    `json:"node_id,omitempty"`
	ToolName   string    `json:"tool_name,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	Status     string    `json:"status,omitempty"`
	Pinned     bool      `json:"pinned,omitempty"`
	RecordedAt time.Time `json:"recorded_at,omitempty"`
}

type BackgroundShellJob struct {
	ID                string    `json:"id"`
	Command           string    `json:"command"`
	CommandSummary    string    `json:"command_summary,omitempty"`
	OwnerNodeID       string    `json:"owner_node_id,omitempty"`
	WorkDir           string    `json:"work_dir,omitempty"`
	Status            string    `json:"status,omitempty"`
	MutationClass     string    `json:"mutation_class,omitempty"`
	AllowedWritePaths []string  `json:"allowed_write_paths,omitempty"`
	ScriptPath        string    `json:"script_path,omitempty"`
	LogPath           string    `json:"log_path,omitempty"`
	StatusPath        string    `json:"status_path,omitempty"`
	StartedAt         time.Time `json:"started_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
	CompletedAt       time.Time `json:"completed_at,omitempty"`
	PID               int       `json:"pid,omitempty"`
	ExitCode          *int      `json:"exit_code,omitempty"`
	LastOutput        string    `json:"last_output,omitempty"`
	CancelReason      string    `json:"cancel_reason,omitempty"`
	PreemptedBy       string    `json:"preempted_by,omitempty"`
	CancelRequestedAt time.Time `json:"cancel_requested_at,omitempty"`
	CanceledAt        time.Time `json:"canceled_at,omitempty"`
}

type BackgroundShellBundle struct {
	ID                string    `json:"id"`
	Summary           string    `json:"summary,omitempty"`
	CommandSummaries  []string  `json:"command_summaries,omitempty"`
	JobIDs            []string  `json:"job_ids,omitempty"`
	OwnerNodeID       string    `json:"owner_node_id,omitempty"`
	Status            string    `json:"status,omitempty"`
	LastSummary       string    `json:"last_summary,omitempty"`
	SupersededBy      string    `json:"superseded_by,omitempty"`
	LifecycleNote     string    `json:"lifecycle_note,omitempty"`
	CancelReason      string    `json:"cancel_reason,omitempty"`
	PreemptedBy       string    `json:"preempted_by,omitempty"`
	CancelRequestedAt time.Time `json:"cancel_requested_at,omitempty"`
	CanceledAt        time.Time `json:"canceled_at,omitempty"`
	StartedAt         time.Time `json:"started_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

func (s *Session) EnsureTaskState() *TaskState {
	if s.TaskState == nil {
		s.TaskState = &TaskState{}
	}
	s.TaskState.Normalize()
	return s.TaskState
}

func (s *Session) StartTaskState(goal string) *TaskState {
	state := s.EnsureTaskState()
	trimmedGoal := strings.TrimSpace(goal)
	if trimmedGoal == "" {
		return state
	}
	preservedBundles := make([]TaskNode, 0)
	if s.TaskGraph != nil {
		for _, node := range s.TaskGraph.Nodes {
			if strings.HasPrefix(strings.TrimSpace(node.ID), "bundle:") {
				preservedBundles = append(preservedBundles, node)
			}
		}
	}
	state.Goal = strings.Join(strings.Fields(trimmedGoal), " ")
	state.Phase = "planning"
	state.PlanSummary = ""
	state.PlanApproved = false
	state.PlanRefreshCount = 0
	state.PlanCursor = 0
	state.CurrentHypothesis = ""
	state.ConfirmedFacts = nil
	state.FailedAttempts = nil
	state.CompletedSteps = nil
	state.PendingChecks = nil
	state.ReviewerGuidance = ""
	state.ExecutorFocusNode = ""
	state.ExecutorAction = ""
	state.ExecutorReason = ""
	state.ExecutorGuidance = ""
	state.ExecutorParallelNodes = nil
	state.ExecutorParallelGuidance = nil
	state.LastRecoveryReason = ""
	state.FinalReviewCount = 0
	state.FinalReviewVerdict = ""
	state.NextStep = "Inspect the relevant code and establish a concrete execution plan."
	state.Events = nil
	state.Touch()
	s.Plan = nil
	if len(preservedBundles) > 0 {
		s.TaskGraph = &TaskGraph{
			Nodes:       append([]TaskNode(nil), preservedBundles...),
			LastUpdated: time.Now(),
		}
	} else {
		s.TaskGraph = nil
	}
	s.syncTaskGraphFromPlan()
	return state
}

func (s *Session) UpsertBackgroundJob(job BackgroundShellJob) {
	job.Normalize()
	if strings.TrimSpace(job.ID) == "" {
		return
	}
	for i := range s.BackgroundJobs {
		if strings.EqualFold(strings.TrimSpace(s.BackgroundJobs[i].ID), strings.TrimSpace(job.ID)) {
			s.BackgroundJobs[i] = job
			return
		}
	}
	s.BackgroundJobs = append(s.BackgroundJobs, job)
}

func (s *Session) UpsertBackgroundBundle(bundle BackgroundShellBundle) {
	bundle.Normalize()
	if strings.TrimSpace(bundle.ID) == "" {
		return
	}
	for i := range s.BackgroundBundles {
		if strings.EqualFold(strings.TrimSpace(s.BackgroundBundles[i].ID), strings.TrimSpace(bundle.ID)) {
			s.BackgroundBundles[i] = bundle
			return
		}
	}
	s.BackgroundBundles = append(s.BackgroundBundles, bundle)
}

func (s *Session) BackgroundJob(jobID string) (BackgroundShellJob, bool) {
	for _, job := range s.BackgroundJobs {
		if strings.EqualFold(strings.TrimSpace(job.ID), strings.TrimSpace(jobID)) {
			return job, true
		}
	}
	return BackgroundShellJob{}, false
}

func (s *Session) BackgroundBundle(bundleID string) (BackgroundShellBundle, bool) {
	for _, bundle := range s.BackgroundBundles {
		if strings.EqualFold(strings.TrimSpace(bundle.ID), strings.TrimSpace(bundleID)) {
			return bundle, true
		}
	}
	return BackgroundShellBundle{}, false
}

func (s *Session) normalizeTaskState() {
	if s.TaskState == nil {
		return
	}
	s.TaskState.Normalize()
}

func (s *Session) normalizeBackgroundJobs() {
	if len(s.BackgroundJobs) == 0 {
		return
	}
	for i := range s.BackgroundJobs {
		s.BackgroundJobs[i].Normalize()
	}
	slices.SortFunc(s.BackgroundJobs, func(a, b BackgroundShellJob) int {
		if a.StartedAt.Equal(b.StartedAt) {
			return strings.Compare(a.ID, b.ID)
		}
		if a.StartedAt.After(b.StartedAt) {
			return -1
		}
		return 1
	})
}

func (s *Session) normalizeBackgroundBundles() {
	if len(s.BackgroundBundles) == 0 {
		return
	}
	for i := range s.BackgroundBundles {
		s.BackgroundBundles[i].Normalize()
	}
	slices.SortFunc(s.BackgroundBundles, func(a, b BackgroundShellBundle) int {
		if a.StartedAt.Equal(b.StartedAt) {
			return strings.Compare(a.ID, b.ID)
		}
		if a.StartedAt.After(b.StartedAt) {
			return -1
		}
		return 1
	})
}

func (t *TaskState) Normalize() {
	if t == nil {
		return
	}
	t.Goal = strings.Join(strings.Fields(strings.TrimSpace(t.Goal)), " ")
	t.Phase = strings.TrimSpace(strings.ToLower(t.Phase))
	if t.Phase == "" {
		t.Phase = "planning"
	}
	t.PlanSummary = strings.TrimSpace(t.PlanSummary)
	t.PlannerProfile = strings.TrimSpace(t.PlannerProfile)
	t.ReviewerProfile = strings.TrimSpace(t.ReviewerProfile)
	t.CurrentHypothesis = strings.TrimSpace(t.CurrentHypothesis)
	t.ConfirmedFacts = normalizeTaskStateList(t.ConfirmedFacts, 8)
	t.FailedAttempts = normalizeTaskStateList(t.FailedAttempts, 8)
	t.CompletedSteps = normalizeTaskStateList(t.CompletedSteps, 8)
	t.PendingChecks = normalizeTaskStateList(t.PendingChecks, 8)
	t.ReviewerGuidance = strings.TrimSpace(t.ReviewerGuidance)
	t.ExecutorFocusNode = strings.TrimSpace(t.ExecutorFocusNode)
	t.ExecutorAction = strings.TrimSpace(strings.ToLower(t.ExecutorAction))
	t.ExecutorReason = strings.TrimSpace(t.ExecutorReason)
	t.ExecutorGuidance = strings.TrimSpace(t.ExecutorGuidance)
	t.ExecutorParallelNodes = normalizeTaskStateList(t.ExecutorParallelNodes, 8)
	t.ExecutorParallelGuidance = normalizeTaskStateList(t.ExecutorParallelGuidance, 8)
	t.LastRecoveryReason = strings.TrimSpace(t.LastRecoveryReason)
	t.FinalReviewVerdict = strings.TrimSpace(strings.ToLower(t.FinalReviewVerdict))
	t.NextStep = strings.TrimSpace(t.NextStep)
	t.Events = normalizeTaskEvents(t.Events, 48, 12)
}

func (t *TaskState) Touch() {
	if t == nil {
		return
	}
	t.LastUpdated = time.Now()
}

func (t *TaskState) ApproxChars() int {
	if t == nil {
		return 0
	}
	total := len(t.Goal) + len(t.Phase) + len(t.PlanSummary) + len(t.PlannerProfile) + len(t.ReviewerProfile)
	total += len(t.CurrentHypothesis) + len(t.ReviewerGuidance) + len(t.ExecutorFocusNode) + len(t.ExecutorAction)
	total += len(t.ExecutorReason) + len(t.ExecutorGuidance) + len(t.LastRecoveryReason) + len(t.FinalReviewVerdict) + len(t.NextStep)
	for _, item := range t.ConfirmedFacts {
		total += len(item)
	}
	for _, item := range t.FailedAttempts {
		total += len(item)
	}
	for _, item := range t.CompletedSteps {
		total += len(item)
	}
	for _, item := range t.PendingChecks {
		total += len(item)
	}
	for _, item := range t.ExecutorParallelNodes {
		total += len(item)
	}
	for _, item := range t.ExecutorParallelGuidance {
		total += len(item)
	}
	for _, event := range t.Events {
		total += len(event.Kind) + len(event.NodeID) + len(event.ToolName) + len(event.Summary) + len(event.Detail) + len(event.Status)
	}
	return total
}

func (t *TaskState) SetPhase(phase string) {
	if t == nil {
		return
	}
	t.Phase = strings.TrimSpace(strings.ToLower(phase))
	if t.Phase == "" {
		t.Phase = "planning"
	}
	t.Touch()
}

func (t *TaskState) SetPlanSummary(plan string, approved bool) {
	if t == nil {
		return
	}
	t.PlanSummary = strings.TrimSpace(plan)
	t.PlanApproved = approved
	t.Touch()
}

func (t *TaskState) NotePlanRefresh(plan string, approved bool, reason string) {
	if t == nil {
		return
	}
	t.PlanRefreshCount++
	t.PlanSummary = strings.TrimSpace(plan)
	t.PlanApproved = approved
	t.LastRecoveryReason = strings.TrimSpace(reason)
	t.PlanCursor = 0
	t.Touch()
}

func (t *TaskState) SetProfiles(planner string, reviewer string) {
	if t == nil {
		return
	}
	t.PlannerProfile = strings.TrimSpace(planner)
	t.ReviewerProfile = strings.TrimSpace(reviewer)
	t.Touch()
}

func (t *TaskState) SetNextStep(next string) {
	if t == nil {
		return
	}
	t.NextStep = strings.TrimSpace(next)
	t.Touch()
}

func (t *TaskState) SetHypothesis(hypothesis string) {
	if t == nil {
		return
	}
	t.CurrentHypothesis = strings.TrimSpace(hypothesis)
	t.Touch()
}

func (t *TaskState) SetReviewerGuidance(reason string, guidance string) {
	if t == nil {
		return
	}
	t.LastRecoveryReason = strings.TrimSpace(reason)
	t.ReviewerGuidance = strings.TrimSpace(guidance)
	t.Touch()
}

func (t *TaskState) SetExecutorFocus(nodeID string, action string, reason string, guidance string) bool {
	if t == nil {
		return false
	}
	nodeID = strings.TrimSpace(nodeID)
	action = strings.TrimSpace(strings.ToLower(action))
	reason = strings.TrimSpace(reason)
	guidance = strings.TrimSpace(guidance)
	if t.ExecutorFocusNode == nodeID &&
		t.ExecutorAction == action &&
		t.ExecutorReason == reason &&
		t.ExecutorGuidance == guidance {
		return false
	}
	t.ExecutorFocusNode = nodeID
	t.ExecutorAction = action
	t.ExecutorReason = reason
	t.ExecutorGuidance = guidance
	t.Touch()
	return true
}

func (t *TaskState) SetExecutorParallelAssignments(nodeIDs []string, guidance []string) bool {
	if t == nil {
		return false
	}
	normalizedNodes := normalizeTaskStateList(nodeIDs, 8)
	normalizedGuidance := normalizeTaskStateList(guidance, 8)
	if slices.Equal(t.ExecutorParallelNodes, normalizedNodes) && slices.Equal(t.ExecutorParallelGuidance, normalizedGuidance) {
		return false
	}
	t.ExecutorParallelNodes = normalizedNodes
	t.ExecutorParallelGuidance = normalizedGuidance
	t.Touch()
	return true
}

func (t *TaskState) ClearExecutorFocus() bool {
	if t == nil {
		return false
	}
	if t.ExecutorFocusNode == "" &&
		t.ExecutorAction == "" &&
		t.ExecutorReason == "" &&
		t.ExecutorGuidance == "" &&
		len(t.ExecutorParallelNodes) == 0 &&
		len(t.ExecutorParallelGuidance) == 0 {
		return false
	}
	t.ExecutorFocusNode = ""
	t.ExecutorAction = ""
	t.ExecutorReason = ""
	t.ExecutorGuidance = ""
	t.ExecutorParallelNodes = nil
	t.ExecutorParallelGuidance = nil
	t.Touch()
	return true
}

func (t *TaskState) NoteFinalReview(verdict string, guidance string) {
	if t == nil {
		return
	}
	t.FinalReviewCount++
	t.FinalReviewVerdict = strings.TrimSpace(strings.ToLower(verdict))
	if strings.TrimSpace(guidance) != "" {
		t.ReviewerGuidance = strings.TrimSpace(guidance)
	}
	t.Touch()
}

func (t *TaskState) AddConfirmedFact(fact string) {
	if t == nil {
		return
	}
	t.ConfirmedFacts = appendTaskStateItem(t.ConfirmedFacts, fact, 8)
	t.Touch()
}

func (t *TaskState) AddFailedAttempt(attempt string) {
	if t == nil {
		return
	}
	t.FailedAttempts = appendTaskStateItem(t.FailedAttempts, attempt, 8)
	t.Touch()
}

func (t *TaskState) AddCompletedStep(step string) {
	if t == nil {
		return
	}
	t.CompletedSteps = appendTaskStateItem(t.CompletedSteps, step, 8)
	t.Touch()
}

func (t *TaskState) AddPendingCheck(check string) {
	if t == nil {
		return
	}
	t.PendingChecks = appendTaskStateItem(t.PendingChecks, check, 8)
	t.Touch()
}

func (t *TaskState) RecordEvent(kind string, nodeID string, toolName string, summary string, detail string, status string, pinned bool) {
	if t == nil {
		return
	}
	event := TaskEvent{
		Kind:       strings.TrimSpace(strings.ToLower(kind)),
		NodeID:     strings.TrimSpace(nodeID),
		ToolName:   strings.TrimSpace(toolName),
		Summary:    strings.TrimSpace(summary),
		Detail:     strings.TrimSpace(detail),
		Status:     strings.TrimSpace(strings.ToLower(status)),
		Pinned:     pinned,
		RecordedAt: time.Now(),
	}
	event.Normalize()
	if event.Kind == "" && event.Summary == "" {
		return
	}
	t.Events = append(t.Events, event)
	t.Events = normalizeTaskEvents(t.Events, 48, 12)
	t.Touch()
}

func (t *TaskState) ClearPendingChecks() {
	if t == nil {
		return
	}
	t.PendingChecks = nil
	t.Touch()
}

func (t *TaskState) RemovePendingCheck(check string) {
	if t == nil {
		return
	}
	target := strings.Join(strings.Fields(strings.TrimSpace(check)), " ")
	if target == "" || len(t.PendingChecks) == 0 {
		return
	}
	filtered := make([]string, 0, len(t.PendingChecks))
	removed := false
	for _, current := range t.PendingChecks {
		normalizedCurrent := strings.Join(strings.Fields(strings.TrimSpace(current)), " ")
		if strings.EqualFold(normalizedCurrent, target) {
			removed = true
			continue
		}
		filtered = append(filtered, current)
	}
	if !removed {
		return
	}
	t.PendingChecks = normalizeTaskStateList(filtered, 8)
	t.Touch()
}

func (t *TaskState) RemovePendingChecksContaining(needle string) {
	if t == nil {
		return
	}
	normalizedNeedle := strings.TrimSpace(strings.ToLower(needle))
	if normalizedNeedle == "" || len(t.PendingChecks) == 0 {
		return
	}
	filtered := make([]string, 0, len(t.PendingChecks))
	removed := false
	for _, check := range t.PendingChecks {
		normalizedCheck := strings.TrimSpace(strings.ToLower(check))
		if strings.Contains(normalizedCheck, normalizedNeedle) {
			removed = true
			continue
		}
		filtered = append(filtered, check)
	}
	if !removed {
		return
	}
	t.PendingChecks = normalizeTaskStateList(filtered, 8)
	t.Touch()
}

func (t *TaskState) RenderPromptSection() string {
	if t == nil {
		return ""
	}
	t.Normalize()
	lines := make([]string, 0, 12)
	if t.Goal != "" {
		lines = append(lines, "- Goal: "+compactPromptSection(t.Goal, 240))
	}
	if t.Phase != "" {
		lines = append(lines, "- Phase: "+t.Phase)
	}
	if t.PlanSummary != "" {
		label := "draft"
		if t.PlanApproved {
			label = "approved"
		}
		lines = append(lines, "- Execution plan ("+label+"): "+compactPromptSection(compactPlanSummary(t.PlanSummary), 700))
	}
	if t.PlanCursor > 0 {
		lines = append(lines, fmt.Sprintf("- Plan progress cursor: %d", t.PlanCursor))
	}
	if t.CurrentHypothesis != "" {
		lines = append(lines, "- Current hypothesis: "+compactPromptSection(t.CurrentHypothesis, 240))
	}
	if len(t.ConfirmedFacts) > 0 {
		lines = append(lines, "- Confirmed facts: "+strings.Join(t.ConfirmedFacts, " | "))
	}
	if len(t.CompletedSteps) > 0 {
		lines = append(lines, "- Completed steps: "+strings.Join(t.CompletedSteps, " | "))
	}
	if len(t.FailedAttempts) > 0 {
		lines = append(lines, "- Failed attempts: "+strings.Join(t.FailedAttempts, " | "))
	}
	if len(t.PendingChecks) > 0 {
		lines = append(lines, "- Pending checks: "+strings.Join(t.PendingChecks, " | "))
	}
	if t.ReviewerGuidance != "" {
		lines = append(lines, "- Reviewer guidance: "+compactPromptSection(t.ReviewerGuidance, 400))
	}
	if t.ExecutorFocusNode != "" {
		focus := "- Executor focus: " + t.ExecutorFocusNode
		if t.ExecutorAction != "" {
			focus += " [" + t.ExecutorAction + "]"
		}
		if t.ExecutorReason != "" {
			focus += " | reason=" + compactPromptSection(t.ExecutorReason, 220)
		}
		lines = append(lines, focus)
	}
	if t.ExecutorGuidance != "" {
		lines = append(lines, "- Executor guidance: "+compactPromptSection(t.ExecutorGuidance, 280))
	}
	if len(t.ExecutorParallelNodes) > 0 {
		lines = append(lines, "- Executor parallel nodes: "+strings.Join(t.ExecutorParallelNodes, " | "))
	}
	if len(t.ExecutorParallelGuidance) > 0 {
		lines = append(lines, "- Executor parallel guidance: "+strings.Join(t.ExecutorParallelGuidance, " | "))
	}
	if t.FinalReviewVerdict != "" {
		lines = append(lines, "- Final answer review: "+t.FinalReviewVerdict)
	}
	if t.NextStep != "" {
		lines = append(lines, "- Next step: "+compactPromptSection(t.NextStep, 200))
	}
	if rendered := renderTaskEventsPrompt(t.Events, 6); rendered != "" {
		lines = append(lines, "- Event journal: "+rendered)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func (t *TaskState) RenderExportSection() string {
	if t == nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(t.RenderPromptSection(), "- "))
}

func renderBackgroundJobsPrompt(jobs []BackgroundShellJob, workingDir string) string {
	lines := make([]string, 0, len(jobs))
	for _, job := range jobs {
		job.Normalize()
		if strings.TrimSpace(job.ID) == "" {
			continue
		}
		line := fmt.Sprintf("- %s [%s] %s", job.ID, job.Status, compactPromptSection(job.CommandSummary, 120))
		if job.OwnerNodeID != "" {
			line += " | owner=" + job.OwnerNodeID
		}
		if workingDir != "" && job.LogPath != "" {
			if rel, err := filepath.Rel(workingDir, job.LogPath); err == nil {
				line += " | log=" + rel
			}
		}
		if job.LastOutput != "" {
			line += " | last=" + compactPromptSection(firstNonEmptyLine(job.LastOutput), 120)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func renderBackgroundBundlesPrompt(bundles []BackgroundShellBundle) string {
	lines := make([]string, 0, len(bundles))
	for _, bundle := range bundles {
		bundle.Normalize()
		if strings.TrimSpace(bundle.ID) == "" {
			continue
		}
		line := fmt.Sprintf("- %s [%s] jobs=%d", bundle.ID, bundle.Status, len(bundle.JobIDs))
		if bundle.OwnerNodeID != "" {
			line += " | owner=" + bundle.OwnerNodeID
		}
		if len(bundle.CommandSummaries) > 0 {
			line += " | commands=" + compactPromptSection(strings.Join(bundle.CommandSummaries, " | "), 180)
		}
		if bundle.LastSummary != "" {
			line += " | summary=" + compactPromptSection(bundle.LastSummary, 120)
		}
		if bundle.SupersededBy != "" {
			line += " | superseded_by=" + bundle.SupersededBy
		}
		if bundle.LifecycleNote != "" {
			line += " | note=" + compactPromptSection(bundle.LifecycleNote, 120)
		}
		if bundle.CancelReason != "" {
			line += " | cancel=" + compactPromptSection(bundle.CancelReason, 120)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func renderBackgroundJobsExport(jobs []BackgroundShellJob) string {
	if len(jobs) == 0 {
		return ""
	}
	lines := make([]string, 0, len(jobs))
	for _, job := range jobs {
		job.Normalize()
		line := fmt.Sprintf("- %s [%s] %s", job.ID, job.Status, compactPromptSection(job.Command, 160))
		if job.OwnerNodeID != "" {
			line += " | owner=" + job.OwnerNodeID
		}
		if job.ExitCode != nil {
			line += fmt.Sprintf(" | exit=%d", *job.ExitCode)
		}
		if job.CancelReason != "" {
			line += " | cancel=" + compactPromptSection(job.CancelReason, 120)
		}
		if job.LastOutput != "" {
			line += " | last=" + compactPromptSection(firstNonEmptyLine(job.LastOutput), 120)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func renderBackgroundBundlesExport(bundles []BackgroundShellBundle) string {
	if len(bundles) == 0 {
		return ""
	}
	lines := make([]string, 0, len(bundles))
	for _, bundle := range bundles {
		bundle.Normalize()
		line := fmt.Sprintf("- %s [%s] jobs=%d", bundle.ID, bundle.Status, len(bundle.JobIDs))
		if bundle.OwnerNodeID != "" {
			line += " | owner=" + bundle.OwnerNodeID
		}
		if len(bundle.CommandSummaries) > 0 {
			line += " | commands=" + compactPromptSection(strings.Join(bundle.CommandSummaries, " | "), 180)
		}
		if bundle.LastSummary != "" {
			line += " | summary=" + compactPromptSection(bundle.LastSummary, 120)
		}
		if bundle.SupersededBy != "" {
			line += " | superseded_by=" + bundle.SupersededBy
		}
		if bundle.LifecycleNote != "" {
			line += " | note=" + compactPromptSection(bundle.LifecycleNote, 120)
		}
		if bundle.CancelReason != "" {
			line += " | cancel=" + compactPromptSection(bundle.CancelReason, 120)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (j *BackgroundShellJob) Normalize() {
	j.ID = strings.TrimSpace(j.ID)
	j.Command = strings.TrimSpace(j.Command)
	j.CommandSummary = strings.TrimSpace(j.CommandSummary)
	if j.CommandSummary == "" {
		j.CommandSummary = summarizeShellCommand(j.Command)
	}
	j.OwnerNodeID = strings.TrimSpace(j.OwnerNodeID)
	j.WorkDir = strings.TrimSpace(j.WorkDir)
	j.Status = strings.TrimSpace(strings.ToLower(j.Status))
	if j.Status == "" {
		j.Status = "running"
	}
	j.MutationClass = strings.TrimSpace(j.MutationClass)
	j.ScriptPath = strings.TrimSpace(j.ScriptPath)
	j.LogPath = strings.TrimSpace(j.LogPath)
	j.StatusPath = strings.TrimSpace(j.StatusPath)
	j.LastOutput = strings.TrimSpace(j.LastOutput)
	j.CancelReason = strings.TrimSpace(j.CancelReason)
	j.PreemptedBy = strings.TrimSpace(j.PreemptedBy)
	j.AllowedWritePaths = normalizeTaskStateList(j.AllowedWritePaths, 16)
	if j.UpdatedAt.IsZero() {
		j.UpdatedAt = j.StartedAt
	}
}

func (b *BackgroundShellBundle) Normalize() {
	b.ID = strings.TrimSpace(b.ID)
	b.Summary = strings.TrimSpace(b.Summary)
	b.OwnerNodeID = strings.TrimSpace(b.OwnerNodeID)
	b.Status = strings.TrimSpace(strings.ToLower(b.Status))
	if b.Status == "" {
		b.Status = "running"
	}
	b.LastSummary = strings.TrimSpace(b.LastSummary)
	b.SupersededBy = strings.TrimSpace(b.SupersededBy)
	b.LifecycleNote = strings.TrimSpace(b.LifecycleNote)
	b.CancelReason = strings.TrimSpace(b.CancelReason)
	b.PreemptedBy = strings.TrimSpace(b.PreemptedBy)
	b.CommandSummaries = normalizeTaskStateList(b.CommandSummaries, 8)
	b.JobIDs = normalizeTaskStateList(b.JobIDs, 16)
	if b.UpdatedAt.IsZero() {
		b.UpdatedAt = b.StartedAt
	}
}

func normalizeTaskStateList(items []string, limit int) []string {
	if len(items) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.Join(strings.Fields(strings.TrimSpace(item)), " ")
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
	}
	if limit > 0 && len(normalized) > limit {
		normalized = normalized[len(normalized)-limit:]
	}
	return normalized
}

func (e *TaskEvent) Normalize() {
	e.Kind = strings.TrimSpace(strings.ToLower(e.Kind))
	e.NodeID = strings.TrimSpace(e.NodeID)
	e.ToolName = strings.TrimSpace(e.ToolName)
	e.Summary = strings.TrimSpace(e.Summary)
	e.Detail = strings.TrimSpace(e.Detail)
	e.Status = strings.TrimSpace(strings.ToLower(e.Status))
	if e.RecordedAt.IsZero() {
		e.RecordedAt = time.Now()
	}
}

func normalizeTaskEvents(events []TaskEvent, limit int, pinnedLimit int) []TaskEvent {
	if limit <= 0 {
		limit = 48
	}
	if pinnedLimit <= 0 {
		pinnedLimit = 12
	}
	pinned := make([]TaskEvent, 0, len(events))
	regular := make([]TaskEvent, 0, len(events))
	for _, event := range events {
		event.Normalize()
		if event.Kind == "" && event.Summary == "" {
			continue
		}
		if event.Pinned {
			pinned = append(pinned, event)
		} else {
			regular = append(regular, event)
		}
	}
	if len(pinned) > pinnedLimit {
		pinned = append([]TaskEvent(nil), pinned[len(pinned)-pinnedLimit:]...)
	}
	remaining := limit - len(pinned)
	if remaining < 0 {
		remaining = 0
	}
	if len(regular) > remaining {
		regular = append([]TaskEvent(nil), regular[len(regular)-remaining:]...)
	}
	combined := append([]TaskEvent(nil), pinned...)
	combined = append(combined, regular...)
	slices.SortStableFunc(combined, func(a, b TaskEvent) int {
		if a.RecordedAt.Equal(b.RecordedAt) {
			return strings.Compare(a.Summary, b.Summary)
		}
		if a.RecordedAt.Before(b.RecordedAt) {
			return -1
		}
		return 1
	})
	return combined
}

func renderTaskEventsPrompt(events []TaskEvent, limit int) string {
	if len(events) == 0 {
		return ""
	}
	normalized := normalizeTaskEvents(events, 48, 12)
	if limit > 0 && len(normalized) > limit {
		normalized = normalized[len(normalized)-limit:]
	}
	lines := make([]string, 0, len(normalized))
	for _, event := range normalized {
		item := firstNonBlankString(event.Summary, event.Kind)
		if event.NodeID != "" {
			item += "@" + event.NodeID
		}
		if event.Status != "" {
			item += " [" + event.Status + "]"
		}
		lines = append(lines, compactPromptSection(item, 120))
	}
	return strings.Join(lines, " | ")
}

func appendTaskStateItem(items []string, item string, limit int) []string {
	combined := append(append([]string(nil), items...), item)
	return normalizeTaskStateList(combined, limit)
}

func (s *Session) ensureSharedPlanInProgress() {
	if s == nil || len(s.Plan) == 0 {
		return
	}
	for _, item := range s.Plan {
		if strings.EqualFold(strings.TrimSpace(item.Status), "in_progress") {
			s.syncTaskStatePlanCursor()
			return
		}
	}
	for i := range s.Plan {
		if strings.EqualFold(strings.TrimSpace(s.Plan[i].Status), "completed") {
			continue
		}
		s.Plan[i].Status = "in_progress"
		s.syncTaskStatePlanCursor()
		s.syncTaskGraphFromPlan()
		return
	}
	s.syncTaskStatePlanCursor()
	s.syncTaskGraphFromPlan()
}

func (s *Session) advanceSharedPlan() {
	if s == nil || len(s.Plan) == 0 {
		return
	}
	current := -1
	for i := range s.Plan {
		if strings.EqualFold(strings.TrimSpace(s.Plan[i].Status), "in_progress") {
			current = i
			break
		}
	}
	if current == -1 {
		s.ensureSharedPlanInProgress()
		for i := range s.Plan {
			if strings.EqualFold(strings.TrimSpace(s.Plan[i].Status), "in_progress") {
				current = i
				break
			}
		}
	}
	if current >= 0 {
		s.Plan[current].Status = "completed"
	}
	for i := current + 1; i < len(s.Plan); i++ {
		if strings.EqualFold(strings.TrimSpace(s.Plan[i].Status), "completed") {
			continue
		}
		s.Plan[i].Status = "in_progress"
		s.syncTaskStatePlanCursor()
		s.syncTaskGraphFromPlan()
		return
	}
	s.syncTaskStatePlanCursor()
	s.syncTaskGraphFromPlan()
}

func (s *Session) SetPlanNodeLifecycle(nodeID string, status string, note string) bool {
	if s == nil {
		return false
	}
	changed := false
	normalizedStatus := canonicalTaskNodeStatus(status)
	if index, ok := planNodeIndex(nodeID); ok && index >= 0 && index < len(s.Plan) {
		switch normalizedStatus {
		case "completed":
			if !strings.EqualFold(strings.TrimSpace(s.Plan[index].Status), "completed") {
				s.Plan[index].Status = "completed"
				changed = true
			}
			if !sessionHasInProgressPlanItem(s.Plan) {
				for next := index + 1; next < len(s.Plan); next++ {
					if strings.EqualFold(strings.TrimSpace(s.Plan[next].Status), "completed") {
						continue
					}
					s.Plan[next].Status = "in_progress"
					changed = true
					break
				}
			}
		case "in_progress":
			if s.SetPlanNodeInProgress(nodeID) {
				changed = true
			}
		case "pending", "ready", "blocked", "failed", "stale", "superseded", "canceled", "preempted":
			current := strings.TrimSpace(strings.ToLower(s.Plan[index].Status))
			if current != "completed" && current != "pending" {
				s.Plan[index].Status = "pending"
				changed = true
			}
		}
	}
	if s.TaskGraph != nil {
		before, ok := s.TaskGraph.Node(nodeID)
		s.TaskGraph.SetNodeLifecycle(nodeID, normalizedStatus, note)
		after, afterOK := s.TaskGraph.Node(nodeID)
		if ok != afterOK || !ok || before.Status != after.Status || before.LifecycleNote != after.LifecycleNote {
			changed = true
		}
	}
	if changed {
		s.syncTaskStatePlanCursor()
	}
	return changed
}

func sessionHasInProgressPlanItem(items []PlanItem) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Status), "in_progress") {
			return true
		}
	}
	return false
}

func (s *Session) completeSharedPlan() {
	if s == nil {
		return
	}
	for i := range s.Plan {
		s.Plan[i].Status = "completed"
	}
	s.syncTaskStatePlanCursor()
	s.syncTaskGraphFromPlan()
}

func (s *Session) syncTaskStatePlanCursor() {
	if s == nil {
		return
	}
	if s.TaskState == nil {
		s.syncTaskGraphFromPlan()
		return
	}
	cursor := 0
	for _, item := range s.Plan {
		status := strings.TrimSpace(strings.ToLower(item.Status))
		if status == "completed" {
			cursor++
			continue
		}
		break
	}
	s.TaskState.PlanCursor = cursor
	s.TaskState.Touch()
	s.syncTaskGraphFromPlan()
}

func compactPlanSummary(plan string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(plan), "\r\n", "\n")
	if normalized == "" {
		return ""
	}
	lines := strings.Split(normalized, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		filtered = append(filtered, trimmed)
		if len(filtered) >= 8 {
			break
		}
	}
	return strings.Join(filtered, " | ")
}
