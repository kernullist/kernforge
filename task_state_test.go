package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskStateRenderPromptSectionIncludesStructuredFields(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	state := session.StartTaskState("Fix the verification loop and keep long-running jobs recoverable.")
	state.SetPlanSummary("1. Inspect the current loop\n2. Add a task state\n3. Add background jobs", true)
	state.SetHypothesis("The normal loop loses recovery context when long shell commands stall.")
	state.AddConfirmedFact("run_shell is synchronous")
	state.AddCompletedStep("Added a structured task state")
	state.AddFailedAttempt("Repeated run_shell timeout")
	state.AddPendingCheck("Verify background job polling")
	state.SetReviewerGuidance("tool_budget_exceeded", "Poll the existing background job instead of rerunning the same command.")
	state.SetExecutorFocus("plan-02", "poll_background", "verification already owns an active bundle", "Poll the running verification bundle before starting another command.")
	state.SetNextStep("Run the focused background job test.")

	rendered := state.RenderPromptSection()
	for _, want := range []string{
		"Goal:",
		"Execution plan (approved):",
		"Current hypothesis:",
		"Confirmed facts:",
		"Completed steps:",
		"Failed attempts:",
		"Pending checks:",
		"Reviewer guidance:",
		"Executor focus:",
		"Executor guidance:",
		"Next step:",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected prompt section to contain %q, got %q", want, rendered)
		}
	}
}

func TestSessionSelectTaskExecutorDecisionPrefersBlockedVerificationNode(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	session.TaskState = &TaskState{}
	session.SetSharedPlan([]PlanItem{
		{Step: "Inspect the code", Status: "completed"},
		{Step: "Run verification bundle", Status: "in_progress"},
		{Step: "Summarize", Status: "pending"},
	})
	bundle := BackgroundShellBundle{
		ID:               "bundle-1",
		Status:           "failed",
		CommandSummaries: []string{"go test ./..."},
		LastSummary:      "completed=0 running=0 failed=1 total=1",
		LifecycleNote:    "go test ./... failed",
	}
	session.UpsertBackgroundBundle(bundle)
	session.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{ID: "plan-01", Title: "Inspect the code", Kind: "inspection", Status: "completed"},
			{ID: "plan-02", Title: "Run verification bundle", Kind: "verification", Status: "blocked", LinkedBundleIDs: []string{"bundle-1"}, LifecycleNote: "go test ./... failed"},
			{ID: "plan-03", Title: "Summarize", Kind: "summary", Status: "pending"},
		},
		LastUpdated: time.Now(),
	}
	session.TaskGraph.Normalize()

	decision := session.SelectTaskExecutorDecision()
	if decision.NodeID != "plan-02" {
		t.Fatalf("expected blocked verification node focus, got %#v", decision)
	}
	if decision.Action != "recover" {
		t.Fatalf("expected recover action, got %#v", decision)
	}
	if !strings.Contains(strings.ToLower(decision.Guidance), "failed") && !strings.Contains(strings.ToLower(decision.Guidance), "blocker") {
		t.Fatalf("expected recovery guidance, got %#v", decision)
	}
}

func TestRenderBackgroundJobsPromptIncludesStatusAndLastOutput(t *testing.T) {
	now := time.Now()
	text := renderBackgroundJobsPrompt([]BackgroundShellJob{{
		ID:             "job-1",
		Command:        "go test ./...",
		CommandSummary: "go test ./...",
		Status:         "running",
		LogPath:        "C:\\workspace\\.kernforge\\jobs\\job-1\\output.log",
		LastOutput:     "ok   kernforge",
		StartedAt:      now,
		UpdatedAt:      now,
	}}, "C:\\workspace")

	if !strings.Contains(text, "job-1 [running]") {
		t.Fatalf("expected job id and status, got %q", text)
	}
	if !strings.Contains(text, "last=ok   kernforge") {
		t.Fatalf("expected last output in prompt, got %q", text)
	}
}

func TestRenderBackgroundBundlesPromptIncludesBundleStatusAndCommands(t *testing.T) {
	now := time.Now()
	text := renderBackgroundBundlesPrompt([]BackgroundShellBundle{{
		ID:               "bundle-1",
		Status:           "running",
		CommandSummaries: []string{"go test ./pkg/...", "ctest --output-on-failure"},
		JobIDs:           []string{"job-1", "job-2"},
		LastSummary:      "completed=1 running=1 failed=0 total=2",
		StartedAt:        now,
		UpdatedAt:        now,
	}})

	if !strings.Contains(text, "bundle-1 [running] jobs=2") {
		t.Fatalf("expected bundle id and status, got %q", text)
	}
	if !strings.Contains(text, "commands=go test ./pkg/... | ctest --output-on-failure") {
		t.Fatalf("expected command summaries in prompt, got %q", text)
	}
	if !strings.Contains(text, "summary=completed=1 running=1 failed=0 total=2") {
		t.Fatalf("expected bundle summary in prompt, got %q", text)
	}
}

func TestSessionSharedPlanProgressAdvancesAndTracksCursor(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	session.TaskState = &TaskState{}
	session.Plan = []PlanItem{
		{Step: "Inspect the code", Status: "pending"},
		{Step: "Apply the fix", Status: "pending"},
		{Step: "Verify the result", Status: "pending"},
	}

	session.ensureSharedPlanInProgress()
	if session.Plan[0].Status != "in_progress" {
		t.Fatalf("expected first plan item to become in_progress, got %#v", session.Plan)
	}
	if session.TaskState.PlanCursor != 0 {
		t.Fatalf("expected initial plan cursor 0, got %d", session.TaskState.PlanCursor)
	}

	session.advanceSharedPlan()
	if session.Plan[0].Status != "completed" || session.Plan[1].Status != "in_progress" {
		t.Fatalf("expected plan to advance, got %#v", session.Plan)
	}
	if session.TaskState.PlanCursor != 1 {
		t.Fatalf("expected plan cursor 1 after first advance, got %d", session.TaskState.PlanCursor)
	}

	session.completeSharedPlan()
	for _, item := range session.Plan {
		if item.Status != "completed" {
			t.Fatalf("expected all plan items completed, got %#v", session.Plan)
		}
	}
	if session.TaskState.PlanCursor != 3 {
		t.Fatalf("expected final plan cursor 3, got %d", session.TaskState.PlanCursor)
	}
}

func TestTaskStateRenderPromptSectionIncludesEventJournalAndParallelExecutorAssignments(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	state := session.StartTaskState("Keep long verification work recoverable.")
	state.SetExecutorFocus("plan-02", "poll_background", "verification already owns a running bundle", "Poll the running bundle first.")
	state.SetExecutorParallelAssignments([]string{"plan-03", "plan-04"}, []string{"plan-03: inspect the latest log tail", "plan-04: gather read-only evidence"})
	state.RecordEvent("background_start", "plan-02", "run_shell_background", "Started focused verification", "bundle-1", "running", true)

	rendered := state.RenderPromptSection()
	for _, want := range []string{
		"Executor parallel nodes:",
		"Executor parallel guidance:",
		"Event journal:",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected prompt section to contain %q, got %q", want, rendered)
		}
	}
}

func TestTaskStateEventJournalPersistsAcrossSessionStore(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "openai", "gpt-test", "", "default")
	state := session.StartTaskState("Persist long-running recovery context.")
	state.RecordEvent("tool_result", "plan-02", "check_shell_bundle", "Polled background bundle", "completed=1 running=0 failed=0 canceled=0 total=1", "completed", true)

	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.TaskState == nil || len(reloaded.TaskState.Events) == 0 {
		t.Fatalf("expected persisted task events, got %#v", reloaded.TaskState)
	}
	if !strings.Contains(reloaded.TaskState.RenderPromptSection(), "Event journal:") {
		t.Fatalf("expected rendered event journal after reload, got %q", reloaded.TaskState.RenderPromptSection())
	}
}

func TestSessionSelectTaskExecutorDecisionTracksParallelReadyNodes(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	session.TaskState = &TaskState{}
	session.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{ID: "plan-01", Title: "Run focused verification", Kind: "verification", Status: "ready"},
			{ID: "plan-02", Title: "Inspect stale output", Kind: "inspection", Status: "ready"},
			{ID: "plan-03", Title: "Collect another evidence slice", Kind: "inspection", Status: "ready"},
		},
		LastUpdated: time.Now(),
	}
	session.TaskGraph.Normalize()

	decision := session.SelectTaskExecutorDecision()
	if decision.NodeID != "plan-01" {
		t.Fatalf("expected verification node as primary focus, got %#v", decision)
	}
	if len(decision.ParallelNodeIDs) == 0 {
		t.Fatalf("expected secondary parallel node assignments, got %#v", decision)
	}
	if decision.ParallelNodeIDs[0] != "plan-02" && decision.ParallelNodeIDs[0] != "plan-03" {
		t.Fatalf("unexpected parallel node assignments: %#v", decision)
	}
}

func TestSessionRecordPlanNodeFailureExhaustsRetryBudgetAndRecovers(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	session.TaskState = &TaskState{}
	session.SetSharedPlan([]PlanItem{
		{Step: "Run focused verification", Status: "in_progress"},
		{Step: "Summarize", Status: "pending"},
	})

	for i := 0; i < 3; i++ {
		if !session.RecordPlanNodeFailure("plan-01", "run_shell", "go test ./... failed") {
			t.Fatalf("expected failure accounting to update node state on iteration %d", i)
		}
	}

	node, ok := session.TaskGraph.Node("plan-01")
	if !ok {
		t.Fatalf("expected plan node to exist")
	}
	if canonicalTaskNodeStatus(node.Status) != "blocked" {
		t.Fatalf("expected retry exhaustion to block the node, got %#v", node)
	}
	if node.RetryUsed != node.RetryBudget {
		t.Fatalf("expected retry usage to reach the budget, got %#v", node)
	}
	if session.Plan[0].Status != "pending" {
		t.Fatalf("expected exhausted node to yield its plan slot, got %#v", session.Plan)
	}

	if !session.SetPlanNodeLifecycle("plan-01", "in_progress", "Recovered after a different inspection path.") {
		t.Fatalf("expected lifecycle update to recover the node")
	}
	node, ok = session.TaskGraph.Node("plan-01")
	if !ok {
		t.Fatalf("expected recovered plan node to exist")
	}
	if node.RetryUsed != 0 || node.LastFailure != "" || node.LastFailureTool != "" {
		t.Fatalf("expected recovery to reset retry state, got %#v", node)
	}
}

func TestSessionSelectTaskExecutorDecisionHighlightsRetryBudgetExhaustion(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	session.TaskState = &TaskState{}
	session.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{
				ID:            "plan-01",
				Title:         "Run focused verification",
				Kind:          "verification",
				Status:        "blocked",
				RetryBudget:   3,
				RetryUsed:     3,
				LifecycleNote: "Retry budget exhausted for Run focused verification after go test ./... failed",
			},
		},
		LastUpdated: time.Now(),
	}
	session.TaskGraph.Normalize()

	decision := session.SelectTaskExecutorDecision()
	if !strings.Contains(strings.ToLower(decision.Guidance), "retry budget") {
		t.Fatalf("expected executor guidance to mention retry budget exhaustion, got %#v", decision)
	}
}

func TestStartTaskStateDropsOldPlanNodesButPreservesBundleNodes(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	session.SetSharedPlan([]PlanItem{
		{Step: "Inspect the stale task", Status: "in_progress"},
		{Step: "Summarize", Status: "pending"},
	})
	bundle := BackgroundShellBundle{
		ID:               "bundle-1",
		Summary:          "go test ./...",
		CommandSummaries: []string{"go test ./..."},
		JobIDs:           []string{"job-1"},
		Status:           "running",
		LastSummary:      "completed=0 running=1 failed=0 canceled=0 total=1",
	}
	session.UpsertBackgroundBundle(bundle)
	session.AttachBackgroundBundle(bundle, []BackgroundShellJob{{
		ID:             "job-1",
		CommandSummary: "go test ./...",
		Status:         "running",
	}})

	session.StartTaskState("Begin a completely new task")

	if session.Plan != nil {
		t.Fatalf("expected a fresh task to clear the shared plan, got %#v", session.Plan)
	}
	if session.TaskGraph == nil {
		t.Fatalf("expected active bundle nodes to remain available")
	}
	if _, ok := session.TaskGraph.Node("plan-01"); ok {
		t.Fatalf("expected old plan nodes to be dropped for the new task, got %#v", session.TaskGraph.Nodes)
	}
	if _, ok := session.TaskGraph.Node("bundle:bundle-1"); !ok {
		t.Fatalf("expected active bundle node to survive task reset, got %#v", session.TaskGraph.Nodes)
	}
}
