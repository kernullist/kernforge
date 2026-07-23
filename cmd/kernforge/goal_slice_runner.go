package main

import (
	"fmt"
	"strings"
)

// selectActiveGoalSlice picks the next ready slice for Runner v2. Returns
// (nil, nil) when every slice is already complete/skipped. Returns an error
// when incomplete slices remain but none are ready (dependency stall).
func selectActiveGoalSlice(goal *GoalState) (*GoalSlice, error) {
	if goal == nil {
		return nil, fmt.Errorf("nil goal")
	}
	if goal.SlicePlan == nil || len(goal.SlicePlan.Slices) == 0 {
		fb := fallbackGoalSlicePlan(*goal)
		goal.SlicePlan = &fb
	}
	goal.SlicePlan.Normalize()
	if goalSlicePlanAllComplete(*goal.SlicePlan) {
		return nil, nil
	}
	ready := goal.SlicePlan.ReadySlices()
	if len(ready) == 0 {
		incomplete := goalSlicePlanIncompleteIDs(*goal.SlicePlan)
		return nil, fmt.Errorf("no ready slices; incomplete=%s", strings.Join(incomplete, ","))
	}
	want := strings.ToLower(ready[0].ID)
	for i := range goal.SlicePlan.Slices {
		if strings.EqualFold(goal.SlicePlan.Slices[i].ID, want) {
			return &goal.SlicePlan.Slices[i], nil
		}
	}
	return nil, fmt.Errorf("ready slice %q missing from plan", ready[0].ID)
}

func goalSlicePlanAllComplete(plan GoalSlicePlan) bool {
	if len(plan.Slices) == 0 {
		return false
	}
	for _, s := range plan.Slices {
		switch s.Status {
		case goalSliceStatusComplete, goalSliceStatusSkipped:
			continue
		default:
			return false
		}
	}
	return true
}

func goalSlicePlanIncompleteIDs(plan GoalSlicePlan) []string {
	out := make([]string, 0, len(plan.Slices))
	for _, s := range plan.Slices {
		switch s.Status {
		case goalSliceStatusComplete, goalSliceStatusSkipped:
			continue
		default:
			out = append(out, s.ID)
		}
	}
	return out
}

func goalSlicePlanCompletedCount(plan GoalSlicePlan) (done int, total int) {
	total = len(plan.Slices)
	for _, s := range plan.Slices {
		if s.Status == goalSliceStatusComplete || s.Status == goalSliceStatusSkipped {
			done++
		}
	}
	return done, total
}

func markGoalSliceStatus(goal *GoalState, sliceID string, status string) bool {
	if goal == nil || goal.SlicePlan == nil || strings.TrimSpace(sliceID) == "" {
		return false
	}
	status = canonicalGoalSliceStatus(status)
	for i := range goal.SlicePlan.Slices {
		if strings.EqualFold(goal.SlicePlan.Slices[i].ID, sliceID) {
			goal.SlicePlan.Slices[i].Status = status
			if status != goalSliceStatusBlocked {
				goal.SlicePlan.Slices[i].Error = ""
			}
			// Keep legacy Plan projection in sync for UI/session.
			if projected := normalizeGoalPlanItems(PlanItemsFromSlices(*goal.SlicePlan)); len(projected) > 0 {
				goal.Plan = projected
			}
			return true
		}
	}
	return false
}

func markGoalSliceBlocked(goal *GoalState, sliceID string, reason string) bool {
	if !markGoalSliceStatus(goal, sliceID, goalSliceStatusBlocked) {
		return false
	}
	for i := range goal.SlicePlan.Slices {
		if strings.EqualFold(goal.SlicePlan.Slices[i].ID, sliceID) {
			goal.SlicePlan.Slices[i].Error = compactPromptSection(reason, 300)
			return true
		}
	}
	return false
}

func findGoalSlice(goal GoalState, sliceID string) *GoalSlice {
	if goal.SlicePlan == nil || strings.TrimSpace(sliceID) == "" {
		return nil
	}
	for i := range goal.SlicePlan.Slices {
		if strings.EqualFold(goal.SlicePlan.Slices[i].ID, sliceID) {
			s := goal.SlicePlan.Slices[i]
			return &s
		}
	}
	return nil
}

// renderActiveGoalSliceSection scopes implement/review/semantic prompts to the
// current DAG node without dropping the overall objective.
func renderActiveGoalSliceSection(slice GoalSlice) string {
	var b strings.Builder
	b.WriteString("Active slice (complete THIS slice this pass; do not expand into later slices):\n")
	fmt.Fprintf(&b, "- ID: %s\n", firstNonBlankString(slice.ID, "(unknown)"))
	fmt.Fprintf(&b, "- Name: %s\n", firstNonBlankString(slice.Name, slice.ID))
	if slice.Outcome != "" {
		fmt.Fprintf(&b, "- Outcome: %s\n", slice.Outcome)
	}
	if slice.Scope != "" {
		fmt.Fprintf(&b, "- Scope: %s\n", slice.Scope)
	}
	if len(slice.LikelyFiles) > 0 {
		fmt.Fprintf(&b, "- Likely files: %s\n", strings.Join(slice.LikelyFiles, ", "))
	}
	if len(slice.Acceptance) > 0 {
		b.WriteString("- Acceptance (must satisfy for this slice):\n")
		for _, item := range slice.Acceptance {
			fmt.Fprintf(&b, "  - [ ] %s\n", item)
		}
	}
	if slice.Validation != "" {
		fmt.Fprintf(&b, "- Validation: %s\n", slice.Validation)
	}
	if slice.Docs != "" {
		fmt.Fprintf(&b, "- Docs: %s\n", slice.Docs)
	}
	fmt.Fprintf(&b, "- Risk: %s\n", firstNonBlankString(slice.Risk, goalSliceRiskMedium))
	if len(slice.DependsOn) > 0 {
		fmt.Fprintf(&b, "- Depends on (already required complete): %s\n", strings.Join(slice.DependsOn, ", "))
	}
	b.WriteString("\n")
	return b.String()
}

func goalSlicePartialSummary(goal GoalState) string {
	if goal.SlicePlan == nil || len(goal.SlicePlan.Slices) == 0 {
		return ""
	}
	done, total := goalSlicePlanCompletedCount(*goal.SlicePlan)
	if total == 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("slices %d/%d complete", done, total)}
	for _, s := range goal.SlicePlan.Slices {
		if s.Status == goalSliceStatusComplete || s.Status == goalSliceStatusSkipped {
			parts = append(parts, fmt.Sprintf("done:%s", firstNonBlankString(s.Name, s.ID)))
		}
	}
	for _, s := range goal.SlicePlan.Slices {
		switch s.Status {
		case goalSliceStatusComplete, goalSliceStatusSkipped:
			continue
		default:
			parts = append(parts, fmt.Sprintf("open:%s[%s]", firstNonBlankString(s.Name, s.ID), s.Status))
		}
	}
	return strings.Join(parts, "; ")
}

// applySliceCompletionAfterSemanticApproval marks the active slice done and
// decides whether the whole goal can complete. remaining=true means the loop
// should continue with other slices.
func applySliceCompletionAfterSemanticApproval(goal *GoalState, sliceID string) (remaining bool) {
	if goal == nil {
		return false
	}
	if strings.TrimSpace(sliceID) != "" {
		markGoalSliceStatus(goal, sliceID, goalSliceStatusComplete)
	}
	if goal.SlicePlan == nil || len(goal.SlicePlan.Slices) <= 1 {
		return false
	}
	return !goalSlicePlanAllComplete(*goal.SlicePlan)
}
