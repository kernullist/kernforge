package main

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type TaskExecutorDecision struct {
	NodeID            string
	Title             string
	Action            string
	Reason            string
	Guidance          string
	ParallelNodeIDs   []string
	ParallelGuidances []string
}

func (s *Session) SelectTaskExecutorDecision() TaskExecutorDecision {
	if s == nil {
		return TaskExecutorDecision{}
	}
	if decision, ok := s.selectTaskExecutorDecisionFromGraph(); ok {
		return decision
	}
	if s.TaskState != nil && len(s.TaskState.PendingChecks) > 0 {
		check := strings.TrimSpace(s.TaskState.PendingChecks[0])
		if check != "" {
			return TaskExecutorDecision{
				Action:   "pending_check",
				Reason:   "a pending check still needs to be resolved before the task can be concluded",
				Guidance: check,
			}
		}
	}
	return TaskExecutorDecision{}
}

func (s *Session) selectTaskExecutorDecisionFromGraph() (TaskExecutorDecision, bool) {
	if s == nil || s.TaskGraph == nil || len(s.TaskGraph.Nodes) == 0 {
		return TaskExecutorDecision{}, false
	}
	bestIndex := -1
	bestScore := -1
	for index, node := range s.TaskGraph.Nodes {
		score := scoreTaskNodeForExecutor(node)
		if score <= bestScore {
			continue
		}
		bestScore = score
		bestIndex = index
	}
	if bestIndex < 0 {
		return TaskExecutorDecision{}, false
	}
	decision := s.buildTaskExecutorDecision(s.TaskGraph.Nodes[bestIndex])
	decision.ParallelNodeIDs, decision.ParallelGuidances = s.selectParallelTaskExecutorAssignments(s.TaskGraph.Nodes[bestIndex], 2)
	if strings.TrimSpace(decision.Guidance) == "" {
		return TaskExecutorDecision{}, false
	}
	return decision, true
}

func scoreTaskNodeForExecutor(node TaskNode) int {
	status := canonicalTaskNodeStatus(node.Status)
	score := 0
	if isPrimaryTaskNode(node) {
		score += 200
	} else {
		score -= 150
	}
	switch status {
	case "blocked":
		score += 320
	case "in_progress":
		score += 240
	case "ready":
		score += 180
	default:
		score -= 400
	}
	switch strings.TrimSpace(strings.ToLower(node.Kind)) {
	case "verification":
		score += 60
	case "edit":
		score += 45
	case "inspection":
		score += 30
	case "summary":
		score += 10
	case "background_bundle":
		score += 5
	}
	if len(node.LinkedBundleIDs) > 0 {
		score += 20
	}
	if len(node.LinkedJobIDs) > 0 {
		score += 10
	}
	if strings.TrimSpace(node.LifecycleNote) != "" {
		score += 8
	}
	if strings.TrimSpace(node.MicroWorkerBrief) != "" {
		score += 12
	}
	if strings.TrimSpace(node.ReadOnlyWorkerSummary) != "" {
		score += 14
	}
	return score
}

func (s *Session) buildTaskExecutorDecision(node TaskNode) TaskExecutorDecision {
	node.Normalize()
	decision := TaskExecutorDecision{
		NodeID: node.ID,
		Title:  node.Title,
	}
	status := canonicalTaskNodeStatus(node.Status)
	hasRunningBundle := s.nodeHasBundleStatus(node, "running")
	hasFailedBundle := s.nodeHasBundleStatus(node, "failed")

	switch status {
	case "blocked":
		decision.Action = "recover"
		if node.RetryBudget > 0 && node.RetryUsed >= node.RetryBudget {
			decision.Reason = firstNonBlankString(
				s.nodeLifecycleReason(node),
				"this task-graph node exhausted its retry budget and needs a materially different recovery path",
			)
			decision.Guidance = fmt.Sprintf("Focus on blocked node \"%s\" next. Its retry budget is exhausted, so do not repeat the same failing step. Use different evidence, a different tool path, or conclude with a concrete blocker.", node.Title)
		} else {
			decision.Reason = firstNonBlankString(
				s.nodeLifecycleReason(node),
				"this task-graph node is blocked and must be resolved before the plan can safely continue",
			)
		}
		if decision.Guidance == "" && hasFailedBundle {
			decision.Guidance = fmt.Sprintf("Focus on blocked node \"%s\" next. Inspect the failed background verification output or log, explain the blocker, and repair that failure before concluding.", node.Title)
		} else if decision.Guidance == "" {
			decision.Guidance = fmt.Sprintf("Focus on blocked node \"%s\" next. Resolve the blocker or explain the concrete limitation before moving on.", node.Title)
		}
	case "in_progress":
		switch {
		case hasRunningBundle:
			decision.Action = "poll_background"
			decision.Reason = "this node already owns active background work, so duplicating the same verification run would waste turns"
			decision.Guidance = fmt.Sprintf("Stay on in-progress node \"%s\". Poll the active background bundle or inspect its latest output before starting a duplicate command.", node.Title)
		default:
			decision.Action = "continue"
			decision.Reason = "this is the current in-progress task-graph node"
			decision.Guidance = fmt.Sprintf("Continue the in-progress node \"%s\" and finish it before moving to later steps.", node.Title)
		}
	case "ready":
		switch {
		case hasRunningBundle:
			decision.Action = "poll_background"
			decision.Reason = "this ready node already has linked background execution in flight"
			decision.Guidance = fmt.Sprintf("Advance node \"%s\" by polling its active background bundle instead of launching the same verification again.", node.Title)
		case strings.EqualFold(strings.TrimSpace(node.Kind), "verification"):
			decision.Action = "advance"
			decision.Reason = "this is the highest-priority ready verification node"
			decision.Guidance = fmt.Sprintf("Advance the ready verification node \"%s\" next. Use the narrowest build, test, or verification step that can move it to completed.", node.Title)
		case strings.EqualFold(strings.TrimSpace(node.Kind), "inspection"):
			decision.Action = "advance"
			decision.Reason = "this is the highest-priority ready inspection node"
			decision.Guidance = fmt.Sprintf("Advance the ready inspection node \"%s\" next. Read the smallest relevant file or evidence slice needed to unblock the plan.", node.Title)
		case strings.EqualFold(strings.TrimSpace(node.Kind), "summary"):
			decision.Action = "advance"
			decision.Reason = "the remaining work is mostly explanatory and this ready summary node is next"
			decision.Guidance = fmt.Sprintf("Advance the ready summary node \"%s\" next. Summarize only after checking that no earlier node still needs action.", node.Title)
		default:
			decision.Action = "advance"
			decision.Reason = "this is the highest-priority ready task-graph node"
			decision.Guidance = fmt.Sprintf("Advance the ready node \"%s\" next with the smallest reliable tool step.", node.Title)
		}
	}

	if brief := strings.TrimSpace(node.MicroWorkerBrief); brief != "" {
		decision.Guidance += "\nMicro-worker hint: " + compactPromptSection(brief, 180)
	}
	if evidence := strings.TrimSpace(node.ReadOnlyWorkerSummary); evidence != "" {
		decision.Guidance += "\nRead-only worker evidence: " + compactPromptSection(evidence, 180)
	}
	if lease := normalizeTaskStateList(node.EditableLeasePaths, 32); len(lease) > 0 {
		decision.Guidance += "\nEditable lease: " + compactPromptSection(strings.Join(lease, ", "), 180)
	}
	return decision
}

func (s *Session) selectParallelTaskExecutorAssignments(primary TaskNode, limit int) ([]string, []string) {
	if s == nil || s.TaskGraph == nil || limit <= 0 {
		return nil, nil
	}
	candidates := make([]TaskNode, 0, len(s.TaskGraph.Nodes))
	for _, node := range s.TaskGraph.Nodes {
		if strings.EqualFold(strings.TrimSpace(node.ID), strings.TrimSpace(primary.ID)) {
			continue
		}
		if !taskNodesCanRunInParallel(primary, node) {
			continue
		}
		candidates = append(candidates, node)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return scoreTaskNodeForExecutor(candidates[i]) > scoreTaskNodeForExecutor(candidates[j])
	})
	nodeIDs := make([]string, 0, min(limit, len(candidates)))
	guidance := make([]string, 0, min(limit, len(candidates)))
	for _, node := range candidates {
		if len(nodeIDs) >= limit {
			break
		}
		node.Normalize()
		nodeIDs = append(nodeIDs, node.ID)
		guidance = append(guidance, buildParallelExecutorGuidance(node))
	}
	return nodeIDs, guidance
}

func taskNodesCanRunInParallel(primary TaskNode, candidate TaskNode) bool {
	if !isPrimaryTaskNode(candidate) {
		return false
	}
	status := canonicalTaskNodeStatus(candidate.Status)
	if status != "ready" && status != "in_progress" {
		return false
	}
	kind := strings.TrimSpace(strings.ToLower(candidate.Kind))
	switch kind {
	case "inspection", "verification", "task":
	case "edit":
		if !taskNodesCanWarmEditablePathInParallel(primary, candidate) {
			return false
		}
	default:
		return false
	}
	primaryID := strings.TrimSpace(primary.ID)
	for _, dep := range candidate.DependsOn {
		if strings.EqualFold(strings.TrimSpace(dep), primaryID) {
			return false
		}
	}
	for _, dep := range primary.DependsOn {
		if strings.EqualFold(strings.TrimSpace(dep), strings.TrimSpace(candidate.ID)) {
			return false
		}
	}
	return true
}

func taskNodesCanWarmEditablePathInParallel(primary TaskNode, candidate TaskNode) bool {
	candidateLease := bestEffortParallelEditableLeasePaths(candidate)
	if len(candidateLease) == 0 {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(primary.Kind), "edit") {
		return true
	}
	primaryLease := bestEffortParallelEditableLeasePaths(primary)
	if len(primaryLease) == 0 {
		return false
	}
	return !editableLeaseCollectionsOverlap(primaryLease, candidateLease)
}

func buildParallelExecutorGuidance(node TaskNode) string {
	switch strings.TrimSpace(strings.ToLower(node.Kind)) {
	case "verification":
		return fmt.Sprintf("%s: keep a read-only worker on this verification node and poll or inspect the latest output without duplicating work.", node.ID)
	case "inspection":
		return fmt.Sprintf("%s: use a read-only worker to gather just enough evidence to unblock this node.", node.ID)
	case "edit":
		if lease := bestEffortParallelEditableLeasePaths(node); len(lease) > 0 {
			return fmt.Sprintf("%s: keep this secondary edit lane scoped to %s while gathering file-local context; do not cross into other ownership paths.", node.ID, compactPromptSection(strings.Join(lease, ", "), 140))
		}
		return fmt.Sprintf("%s: keep this secondary edit node read-only until its file scope is narrowed.", node.ID)
	default:
		return fmt.Sprintf("%s: keep this node warm with a small read-only follow-up while the main executor advances the primary path.", node.ID)
	}
}

func (s *Session) nodeHasBundleStatus(node TaskNode, want string) bool {
	if s == nil {
		return false
	}
	want = strings.TrimSpace(strings.ToLower(want))
	if want == "" {
		return false
	}
	for _, bundleID := range node.LinkedBundleIDs {
		bundle, ok := s.BackgroundBundle(bundleID)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(bundle.Status), want) {
			return true
		}
	}
	return false
}

func (s *Session) nodeLifecycleReason(node TaskNode) string {
	if s == nil {
		return strings.TrimSpace(node.LifecycleNote)
	}
	if note := strings.TrimSpace(node.LifecycleNote); note != "" {
		return note
	}
	for _, bundleID := range node.LinkedBundleIDs {
		bundle, ok := s.BackgroundBundle(bundleID)
		if !ok {
			continue
		}
		if note := strings.TrimSpace(firstNonBlankString(bundle.LifecycleNote, bundle.LastSummary)); note != "" {
			return note
		}
	}
	return ""
}

func planNodeIndex(nodeID string) (int, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(nodeID))
	if !strings.HasPrefix(trimmed, "plan-") {
		return 0, false
	}
	raw := strings.TrimPrefix(trimmed, "plan-")
	index, err := strconv.Atoi(raw)
	if err != nil || index <= 0 {
		return 0, false
	}
	return index - 1, true
}

func (s *Session) SetPlanNodeInProgress(nodeID string) bool {
	if s == nil || len(s.Plan) == 0 {
		return false
	}
	index, ok := planNodeIndex(nodeID)
	if !ok || index < 0 || index >= len(s.Plan) {
		return false
	}
	changed := false
	for current := range s.Plan {
		status := strings.TrimSpace(strings.ToLower(s.Plan[current].Status))
		switch {
		case current == index:
			if status != "completed" && status != "in_progress" {
				s.Plan[current].Status = "in_progress"
				changed = true
			}
		case status == "in_progress":
			s.Plan[current].Status = "pending"
			changed = true
		}
	}
	if changed {
		s.syncTaskStatePlanCursor()
	} else {
		s.syncTaskGraphFromPlan()
	}
	return changed
}

func (a *Agent) syncTaskExecutorFocus() error {
	if a == nil || a.Session == nil {
		return nil
	}
	state := a.Session.EnsureTaskState()
	decision := a.Session.SelectTaskExecutorDecision()
	changed := false

	if strings.TrimSpace(decision.NodeID) == "" {
		if state.ClearExecutorFocus() {
			changed = true
		}
		if len(state.PendingChecks) > 0 {
			next := strings.TrimSpace(state.PendingChecks[0])
			if next != "" && next != strings.TrimSpace(state.NextStep) {
				state.SetNextStep(next)
				changed = true
			}
		}
	} else {
		if a.Session.SetPlanNodeInProgress(decision.NodeID) {
			changed = true
		}
		if a.Session.TaskGraph != nil {
			if node, ok := a.Session.TaskGraph.Node(decision.NodeID); ok {
				if assignment, ok := selectEditableSpecialistForTaskNode(a.Config, node, state, "executor-focus"); ok {
					currentEditableSpecialist := node.EditableSpecialist
					currentEditableReason := node.EditableReason
					currentOwnership := append([]string(nil), node.EditableOwnershipPaths...)
					currentLeasePaths := append([]string(nil), node.EditableLeasePaths...)
					currentLeaseReason := node.EditableLeaseReason
					ownership := specialistOwnershipPaths(assignment.Profile, node.EditableOwnershipPaths)
					node.EditableSpecialist = assignment.Profile.Name
					node.EditableReason = assignment.Reason
					node.EditableOwnershipPaths = ownership
					leasePaths, leaseReason := deriveEditableLeasePaths(a.Session.TaskGraph, node, assignment.Profile)
					if !strings.EqualFold(strings.TrimSpace(currentEditableSpecialist), strings.TrimSpace(assignment.Profile.Name)) ||
						!strings.EqualFold(strings.TrimSpace(currentEditableReason), strings.TrimSpace(assignment.Reason)) ||
						!slices.Equal(currentOwnership, ownership) {
						a.Session.RecordTaskGraphEditableAssignment(node.ID, assignment.Profile.Name, assignment.Reason, ownership, node.EditableWorktreeRoot, node.EditableWorktreeBranch)
						changed = true
					}
					if len(leasePaths) > 0 &&
						(!slices.Equal(currentLeasePaths, leasePaths) ||
							!strings.EqualFold(strings.TrimSpace(currentLeaseReason), strings.TrimSpace(leaseReason))) {
						a.Session.RecordTaskGraphEditableLease(node.ID, leasePaths, leaseReason)
						changed = true
					}
				}
			}
		}
		if state.SetExecutorFocus(decision.NodeID, decision.Action, decision.Reason, decision.Guidance) {
			changed = true
		}
		if state.SetExecutorParallelAssignments(decision.ParallelNodeIDs, decision.ParallelGuidances) {
			changed = true
		}
		next := strings.TrimSpace(decision.Guidance)
		if next != "" && next != strings.TrimSpace(state.NextStep) {
			state.SetNextStep(next)
			changed = true
		}
		switch strings.TrimSpace(strings.ToLower(decision.Action)) {
		case "recover":
			if strings.TrimSpace(strings.ToLower(state.Phase)) != "recovery" {
				state.SetPhase("recovery")
				changed = true
			}
		case "advance", "continue", "poll_background":
			phase := strings.TrimSpace(strings.ToLower(state.Phase))
			if phase == "" || phase == "planning" || phase == "review" {
				state.SetPhase("execution")
				changed = true
			}
		}
	}
	if changed {
		state.RecordEvent("executor_assignment", strings.TrimSpace(decision.NodeID), "", firstNonBlankString(decision.Guidance, decision.Reason), strings.Join(decision.ParallelGuidances, " | "), "active", false)
	}

	if changed && a.Store != nil {
		return a.Store.Save(a.Session)
	}
	return nil
}
