package main

import (
	"fmt"
	"strings"
)

const selfDrivingLoopEventKind = "self_driving_loop"

func (a *Agent) primeSelfDrivingWorkLoop(userText string, intent TurnIntent, readOnlyAnalysis bool, explicitEditRequest bool, explicitGitRequest bool) bool {
	if a == nil || a.Session == nil {
		return false
	}
	base := strings.TrimSpace(baseUserQueryText(userText))
	if base == "" {
		base = strings.TrimSpace(userText)
	}
	if base == "" {
		return false
	}
	if shouldContinueSelfDrivingWorkLoop(a.Session, intent) {
		state := a.Session.EnsureTaskState()
		if !strings.EqualFold(state.Phase, "done") {
			state.SetPhase("execution")
			state.SetNextStep("Continue the active task from the current task graph and pending checks.")
			state.RecordEvent(selfDrivingLoopEventKind, strings.TrimSpace(state.ExecutorFocusNode), "", "Continued active self-driving work loop.", compactPromptSection(base, 500), "active", true)
			a.Session.ensureSharedPlanInProgress()
			return true
		}
	}
	if !shouldStartSelfDrivingWorkLoop(base, intent, readOnlyAnalysis, explicitEditRequest, explicitGitRequest) {
		return false
	}
	state := a.Session.EnsureTaskState()
	phase := strings.TrimSpace(strings.ToLower(state.Phase))
	if strings.TrimSpace(state.Goal) == "" || phase == "done" || phase == "canceled" {
		state = a.Session.StartTaskState(base)
	}
	if a.canUseInteractivePlanPreflight() && strings.TrimSpace(state.PlanSummary) == "" && len(a.Session.Plan) == 0 {
		state.SetHypothesis("The task should be handled as an inspect, implement, verify, summarize loop unless a concrete blocker appears.")
		state.SetNextStep("Build or reuse an execution plan, then start with the first concrete inspection step.")
		state.RecordEvent(selfDrivingLoopEventKind, "", "", "Armed self-driving work loop before planner preflight.", compactPromptSection(base, 500), "active", true)
		return true
	}
	items := defaultSelfDrivingPlanItems(intent, explicitEditRequest, explicitGitRequest)
	a.Session.SetSharedPlan(items)
	a.Session.ensureSharedPlanInProgress()
	planSummary := renderPlanItemsForTaskState(items)
	state.SetPlanSummary(planSummary, true)
	state.SetPhase("execution")
	state.SetNextStep("Inspect the relevant files and evidence before making the first change.")
	state.SetHypothesis("The task should be handled as an inspect, implement, verify, summarize loop unless a concrete blocker appears.")
	state.RecordEvent(selfDrivingLoopEventKind, "plan-01", "", "Started self-driving work loop.", compactPromptSection(base, 500), "active", true)
	return true
}

func (a *Agent) canUseInteractivePlanPreflight() bool {
	if a == nil || a.Client == nil {
		return false
	}
	_, model := a.ensureInteractiveReviewerClient()
	return strings.TrimSpace(model) != ""
}

func shouldContinueSelfDrivingWorkLoop(session *Session, intent TurnIntent) bool {
	if session == nil || session.TaskState == nil {
		return false
	}
	state := session.TaskState
	phase := strings.TrimSpace(strings.ToLower(state.Phase))
	if strings.TrimSpace(state.Goal) == "" {
		return false
	}
	if phase == "done" || phase == "canceled" {
		return false
	}
	if intent == TurnIntentContinueLastTask {
		return true
	}
	if len(state.PendingChecks) > 0 {
		return true
	}
	if session.TaskGraph != nil {
		for _, node := range session.TaskGraph.Nodes {
			switch canonicalTaskNodeStatus(node.Status) {
			case "ready", "in_progress", "blocked":
				return true
			}
		}
	}
	return false
}

func shouldStartSelfDrivingWorkLoop(userText string, intent TurnIntent, readOnlyAnalysis bool, explicitEditRequest bool, explicitGitRequest bool) bool {
	_ = explicitGitRequest
	base := strings.ToLower(strings.TrimSpace(baseUserQueryText(userText)))
	if base == "" {
		return false
	}
	if strings.HasPrefix(base, "/") {
		return false
	}
	if readOnlyAnalysis || intent == TurnIntentDiagnoseRecentError || intent == TurnIntentExplainCurrentState {
		return false
	}
	if explicitEditRequest || intent == TurnIntentEditCode || intent == TurnIntentRunCommand {
		return true
	}
	return containsAny(base,
		"맡기", "끝까지", "전체 루프", "전체 작업", "이어가", "진행", "처리", "완료", "구현하자", "개발하자", "추가 구현",
		"take this", "handle this", "drive the loop", "finish this", "complete this", "carry this through",
	)
}

func defaultSelfDrivingPlanItems(intent TurnIntent, explicitEditRequest bool, explicitGitRequest bool) []PlanItem {
	_ = intent
	_ = explicitEditRequest
	items := []PlanItem{
		{Step: "Inspect the current request, repository state, and relevant files before deciding the change.", Status: "pending"},
		{Step: "Update the code or documents with the smallest correct change for the requested outcome.", Status: "pending"},
		{Step: "Verify the change with targeted tests, builds, or a clearly stated verification fallback.", Status: "pending"},
		{Step: "Report changed files, checks performed, remaining risks, and a concrete next step if needed.", Status: "pending"},
	}
	if explicitGitRequest {
		items = append(items, PlanItem{Step: "Perform the explicitly requested git action only after the work and verification are ready.", Status: "pending"})
	}
	return items
}

func renderPlanItemsForTaskState(items []PlanItem) string {
	lines := make([]string, 0, len(items))
	for index, item := range items {
		step := strings.TrimSpace(item.Step)
		if step == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%d. %s", index+1, step))
	}
	return strings.Join(lines, "\n")
}

func (a *Agent) finalizeSelfDrivingWorkLoopOnReturn(reply string, unresolvedVerification bool) bool {
	if a == nil || a.Session == nil || a.Session.TaskState == nil {
		return false
	}
	state := a.Session.TaskState
	if strings.TrimSpace(state.Goal) == "" {
		return false
	}
	if unresolvedVerification {
		state.SetPhase("recovery")
		state.SetNextStep("Fix the failing verification or explain the blocker clearly.")
		state.RecordEvent(selfDrivingLoopEventKind, strings.TrimSpace(state.ExecutorFocusNode), "", "Self-driving loop held open by failing verification.", compactPromptSection(reply, 500), "blocked", true)
		return true
	}
	if !a.shouldCompleteSharedPlanOnReturn(false) {
		return false
	}
	state.SetPhase("done")
	state.SetNextStep("Wait for the next user instruction.")
	state.ClearExecutorFocus()
	state.RecordEvent(selfDrivingLoopEventKind, "", "", "Completed self-driving work loop.", compactPromptSection(reply, 500), "completed", true)
	a.Session.completeSharedPlan()
	return true
}

func renderSelfDrivingWorkLoopPrompt(state *TaskState) string {
	if state == nil || strings.TrimSpace(state.Goal) == "" {
		return ""
	}
	phase := strings.TrimSpace(strings.ToLower(state.Phase))
	if phase == "done" || phase == "canceled" {
		return ""
	}
	var b strings.Builder
	b.WriteString("Self-driving work loop:\n")
	b.WriteString("- The user has delegated an active task. Treat it as an inspect -> implement -> verify -> summarize loop.\n")
	b.WriteString("- Do not stop at analysis when a concrete edit or command is needed and tools are available.\n")
	b.WriteString("- After edits, use the automatic verification result to either repair failures or summarize the verified outcome.\n")
	b.WriteString("- If blocked by missing tools, permissions, provider limits, or external state, give a final answer that names the blocker and the best next action.\n")
	b.WriteString("- Keep the task graph current: advance completed nodes, focus ready nodes, and leave pending checks visible until resolved.\n")
	return strings.TrimSpace(b.String())
}
