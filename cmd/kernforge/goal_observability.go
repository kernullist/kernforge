package main

import (
	"fmt"
	"strings"
	"time"
)

// GoalEvent is a compact durable log entry for goal-loop observability.
type GoalEvent struct {
	At            time.Time `json:"at"`
	Kind          string    `json:"kind"`
	Iteration     int       `json:"iteration,omitempty"`
	SliceID       string    `json:"slice_id,omitempty"`
	SliceName     string    `json:"slice_name,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	TokenEstimate int       `json:"token_estimate,omitempty"`
	Status        string    `json:"status,omitempty"`
}

const (
	goalEventCreated        = "created"
	goalEventIterationStart = "iteration_start"
	goalEventSliceSelected  = "slice_selected"
	goalEventSliceComplete  = "slice_complete"
	goalEventSemanticReject = "semantic_reject"
	goalEventComplete       = "complete"
	goalEventBlocked        = "blocked"
	goalEventInterrupted    = "interrupted"
	goalEventBudgetLimited  = "budget_limited"

	maxGoalEvents = 48
)

func appendGoalEvent(goal *GoalState, kind string, summary string) {
	if goal == nil {
		return
	}
	summary = compactPromptSection(strings.TrimSpace(summary), 240)
	ev := GoalEvent{
		At:            time.Now(),
		Kind:          strings.TrimSpace(kind),
		Iteration:     goal.Iteration,
		Summary:       summary,
		TokenEstimate: goal.TokenUsedEstimate,
		Status:        goal.Status,
	}
	if goal.SlicePlan != nil {
		// Prefer the currently running slice for event context.
		for _, s := range goal.SlicePlan.Slices {
			if s.Status == goalSliceStatusRunning {
				ev.SliceID = s.ID
				ev.SliceName = s.Name
				break
			}
		}
	}
	goal.Events = append(goal.Events, ev)
	if len(goal.Events) > maxGoalEvents {
		goal.Events = append([]GoalEvent(nil), goal.Events[len(goal.Events)-maxGoalEvents:]...)
	}
}

func appendGoalIterationEvent(goal *GoalState, iteration GoalIteration, kind string, summary string) {
	if goal == nil {
		return
	}
	appendGoalEvent(goal, kind, summary)
	if len(goal.Events) == 0 {
		return
	}
	last := &goal.Events[len(goal.Events)-1]
	last.Iteration = iteration.Index
	if iteration.SliceID != "" {
		last.SliceID = iteration.SliceID
		last.SliceName = iteration.SliceName
	}
}

func goalResearchMode(goal GoalState) string {
	if goal.AcceptanceSpec == nil {
		return goalResearchNone
	}
	return canonicalGoalResearchMode(goal.AcceptanceSpec.ResearchMode)
}

func goalResearchModeActive(goal GoalState) bool {
	mode := goalResearchMode(goal)
	return mode == goalResearchBounded || mode == goalResearchAggressive
}

// renderGoalResearchModeSection injects research-loop instructions when the
// compiled Spec (or --research flag) enables research_mode. Local bug-fix
// goals stay on goalResearchNone and get an empty section.
func renderGoalResearchModeSection(goal GoalState) string {
	mode := goalResearchMode(goal)
	if mode == goalResearchNone || mode == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Research mode: %s\n", mode)
	b.WriteString("This goal benefits from external technical research before and during implementation.\n")
	b.WriteString("Follow the $goal-loop methodology on this pass:\n")
	b.WriteString("1. Start with bounded web_search / web_fetch against the current gap (not generic background reading).\n")
	b.WriteString("2. Compress findings into 3-6 actionable insights with sources; update the working approach.\n")
	b.WriteString("3. Implement against those insights; do not expand scope beyond the active slice/objective.\n")
	b.WriteString("4. Record assumptions and rejected approaches so the next iteration does not re-explore dead ends.\n")
	if mode == goalResearchAggressive {
		b.WriteString("Aggressive mode: broader survey is allowed, but still gate irreversible/network publish actions.\n")
	} else {
		b.WriteString("Bounded mode: prefer targeted queries tied to the failing criterion or active slice acceptance.\n")
	}
	b.WriteString("Do not enable research mode behaviors for pure local code inspection when research_mode is none.\n\n")
	return b.String()
}

func goalCostSummary(goal GoalState) string {
	parts := []string{}
	if goal.TokenBudget > 0 || goal.TokenUsedEstimate > 0 {
		if goal.TokenBudget > 0 {
			parts = append(parts, fmt.Sprintf("tokens~%d/%d", goal.TokenUsedEstimate, goal.TokenBudget))
		} else {
			parts = append(parts, fmt.Sprintf("tokens~%d", goal.TokenUsedEstimate))
		}
	}
	if goal.TimeUsedSeconds > 0 || goal.TimeBudgetSeconds > 0 {
		if goal.TimeBudgetSeconds > 0 {
			parts = append(parts, fmt.Sprintf("time=%s/%ds", formatGoalElapsedSeconds(goal.TimeUsedSeconds), goal.TimeBudgetSeconds))
		} else if goal.TimeUsedSeconds > 0 {
			parts = append(parts, fmt.Sprintf("time=%s", formatGoalElapsedSeconds(goal.TimeUsedSeconds)))
		}
	}
	parts = append(parts, fmt.Sprintf("iterations=%d", goal.Iteration))
	if goal.SlicePlan != nil && len(goal.SlicePlan.Slices) > 0 {
		done, total := goalSlicePlanCompletedCount(*goal.SlicePlan)
		parts = append(parts, fmt.Sprintf("slices=%d/%d", done, total))
	}
	if mode := goalResearchMode(goal); mode != goalResearchNone && mode != "" {
		parts = append(parts, "research="+mode)
	}
	return strings.Join(parts, " ")
}

func renderGoalEventsSection(events []GoalEvent, limit int) string {
	if len(events) == 0 {
		return ""
	}
	if limit <= 0 || limit > len(events) {
		limit = len(events)
	}
	start := len(events) - limit
	var b strings.Builder
	b.WriteString("Recent goal events:\n")
	for _, ev := range events[start:] {
		stamp := ev.At.Format(time.RFC3339)
		if ev.At.IsZero() {
			stamp = "?"
		}
		line := fmt.Sprintf("- [%s] %s", stamp, firstNonBlankString(ev.Kind, "event"))
		if ev.Iteration > 0 {
			line += fmt.Sprintf(" iter=%d", ev.Iteration)
		}
		if ev.SliceID != "" {
			line += fmt.Sprintf(" slice=%s", firstNonBlankString(ev.SliceName, ev.SliceID))
		}
		if ev.TokenEstimate > 0 {
			line += fmt.Sprintf(" tokens~%d", ev.TokenEstimate)
		}
		if ev.Summary != "" {
			line += ": " + ev.Summary
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func forceGoalResearchMode(goal *GoalState, mode string) {
	if goal == nil {
		return
	}
	mode = canonicalGoalResearchMode(mode)
	if mode == goalResearchNone {
		return
	}
	ensureGoalAcceptanceSpec(goal)
	if goal.AcceptanceSpec == nil {
		return
	}
	goal.AcceptanceSpec.ResearchMode = mode
}
