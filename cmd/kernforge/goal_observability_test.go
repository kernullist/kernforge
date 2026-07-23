package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestForceGoalResearchModeAndRender(t *testing.T) {
	goal := GoalState{Objective: "fix local parser crash"}
	ensureGoalAcceptanceSpec(&goal)
	if goalResearchModeActive(goal) {
		t.Fatalf("local fix objective must not auto-enable research, got %q", goalResearchMode(goal))
	}
	forceGoalResearchMode(&goal, goalResearchBounded)
	if !goalResearchModeActive(goal) || goalResearchMode(goal) != goalResearchBounded {
		t.Fatalf("expected forced bounded research, got %q", goalResearchMode(goal))
	}
	section := renderGoalResearchModeSection(goal)
	for _, want := range []string{"Research mode: bounded", "$goal-loop", "web_search"} {
		if !strings.Contains(section, want) {
			t.Fatalf("research section missing %q:\n%s", want, section)
		}
	}
	if renderGoalResearchModeSection(GoalState{Objective: "fix typo"}) != "" {
		t.Fatal("research section must be empty when research_mode is none")
	}
}

func TestParseGoalStartOptionsResearchFlag(t *testing.T) {
	rt := &runtimeState{workspace: Workspace{Root: t.TempDir()}}
	opts, err := rt.parseGoalStartOptions([]string{"--research", "survey TPM attestation", "trends"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.ResearchMode != goalResearchBounded {
		t.Fatalf("expected bounded research from bare --research, got %q", opts.ResearchMode)
	}
	if !strings.Contains(opts.Objective, "survey TPM") {
		t.Fatalf("objective should keep survey text, got %q", opts.Objective)
	}
	opts2, err := rt.parseGoalStartOptions([]string{"--research-mode", "aggressive", "deep survey"})
	if err != nil {
		t.Fatalf("parse research-mode: %v", err)
	}
	if opts2.ResearchMode != goalResearchAggressive {
		t.Fatalf("expected aggressive, got %q", opts2.ResearchMode)
	}
}

func TestGoalEventsAndCostSummary(t *testing.T) {
	goal := GoalState{
		Objective:         "finish",
		Status:            goalStatusActive,
		Iteration:         2,
		TokenUsedEstimate: 1200,
		TokenBudget:       5000,
		TimeUsedSeconds:   90,
		SlicePlan: &GoalSlicePlan{
			Slices: []GoalSlice{
				{ID: "a", Name: "A", Status: goalSliceStatusComplete},
				{ID: "b", Name: "B", Status: goalSliceStatusRunning},
			},
		},
	}
	ensureGoalAcceptanceSpec(&goal)
	forceGoalResearchMode(&goal, goalResearchBounded)
	appendGoalEvent(&goal, goalEventCreated, "start")
	appendGoalEvent(&goal, goalEventIterationStart, "iter 1")
	if len(goal.Events) < 2 {
		t.Fatalf("expected events, got %#v", goal.Events)
	}
	cost := goalCostSummary(goal)
	for _, want := range []string{"tokens~1200/5000", "iterations=2", "slices=1/2", "research=bounded"} {
		if !strings.Contains(cost, want) {
			t.Fatalf("cost summary missing %q: %q", want, cost)
		}
	}
	events := renderGoalEventsSection(goal.Events, 10)
	if !strings.Contains(events, "created") || !strings.Contains(events, "iteration_start") {
		t.Fatalf("events render missing kinds:\n%s", events)
	}
}

func TestGoalCreateWithResearchPersistsMode(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(root),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}
	if err := rt.recordGoalWithoutLoop("--research ship research note on latest TPM"); err != nil {
		t.Fatalf("handleGoalCommand: %v", err)
	}
	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatal("expected goal")
	}
	if goalResearchMode(goal) != goalResearchBounded {
		t.Fatalf("expected bounded research mode, got %q spec=%#v", goalResearchMode(goal), goal.AcceptanceSpec)
	}
	if len(goal.Events) == 0 {
		t.Fatal("expected created event")
	}
	prompt := buildGoalImplementationPrompt(goal, 1)
	if !strings.Contains(prompt, "Research mode: bounded") {
		t.Fatalf("implement prompt should carry research section:\n%s", prompt)
	}
}
