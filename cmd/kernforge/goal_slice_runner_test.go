package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSelectActiveGoalSliceSequential(t *testing.T) {
	goal := GoalState{
		Objective: "ship feature",
		SlicePlan: &GoalSlicePlan{
			Slices: []GoalSlice{
				{ID: "a", Name: "A", Outcome: "a", Status: goalSliceStatusPending},
				{ID: "b", Name: "B", Outcome: "b", Status: goalSliceStatusPending, DependsOn: []string{"a"}},
			},
		},
	}
	first, err := selectActiveGoalSlice(&goal)
	if err != nil || first == nil || first.ID != "a" {
		t.Fatalf("expected slice a ready, got %#v err=%v", first, err)
	}
	markGoalSliceStatus(&goal, "a", goalSliceStatusComplete)
	second, err := selectActiveGoalSlice(&goal)
	if err != nil || second == nil || second.ID != "b" {
		t.Fatalf("expected slice b after a complete, got %#v err=%v", second, err)
	}
	markGoalSliceStatus(&goal, "b", goalSliceStatusComplete)
	done, err := selectActiveGoalSlice(&goal)
	if err != nil || done != nil {
		t.Fatalf("expected nil when all complete, got %#v err=%v", done, err)
	}
}

func TestApplySliceCompletionKeepsGoalOpen(t *testing.T) {
	goal := GoalState{
		SlicePlan: &GoalSlicePlan{
			Slices: []GoalSlice{
				{ID: "a", Name: "A", Outcome: "a", Status: goalSliceStatusRunning},
				{ID: "b", Name: "B", Outcome: "b", Status: goalSliceStatusPending, DependsOn: []string{"a"}},
			},
		},
	}
	if !applySliceCompletionAfterSemanticApproval(&goal, "a") {
		t.Fatal("expected remaining slices after completing a")
	}
	if goal.SlicePlan.Slices[0].Status != goalSliceStatusComplete {
		t.Fatalf("slice a should be complete, got %#v", goal.SlicePlan.Slices[0])
	}
	if applySliceCompletionAfterSemanticApproval(&goal, "b") {
		t.Fatal("no remaining slices after completing last")
	}
}

func TestGoalMultiSliceRunnerAdvancesAfterSliceApproval(t *testing.T) {
	root := t.TempDir()
	writeGoalTestModule(t, root)
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	semanticApprovals := 0
	rt := &runtimeState{
		writer:        &output,
		ui:            NewUI(),
		session:       session,
		store:         NewSessionStore(filepath.Join(root, "sessions")),
		verifyHistory: &VerificationHistoryStore{Path: filepath.Join(root, "verify-history.json"), MaxEntries: defaultVerificationHistoryMaxEntries},
		workspace: Workspace{
			BaseRoot:     root,
			Root:         root,
			Shell:        defaultShell(),
			ShellTimeout: 30 * time.Second,
		},
		goalReply: func(ctx context.Context, prompt string) (string, error) {
			_ = ctx
			switch {
			case strings.Contains(prompt, "Final semantic goal review"):
				semanticApprovals++
				return "APPROVED: active slice acceptance is met", nil
			case strings.Contains(prompt, "Active slice"):
				return "implemented active slice", nil
			default:
				return "fake goal agent reply", nil
			}
		},
	}
	useFastGoalRuntime(t, rt)

	// Seed a multi-slice goal without model planning.
	now := time.Now()
	goal := GoalState{
		ID:        "goal-multi-slice",
		Objective: "finish multi-slice objective",
		Status:    goalStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
		SlicePlan: &GoalSlicePlan{
			ObjectiveRestated: "finish multi-slice objective",
			CompiledFrom:      "test",
			Slices: []GoalSlice{
				{
					ID:         "slice-a",
					Name:       "First",
					Outcome:    "first outcome",
					Acceptance: []string{"first done"},
					Status:     goalSliceStatusPending,
					Order:      1,
				},
				{
					ID:         "slice-b",
					Name:       "Second",
					Outcome:    "second outcome",
					Acceptance: []string{"second done"},
					Status:     goalSliceStatusPending,
					DependsOn:  []string{"slice-a"},
					Order:      2,
				},
			},
		},
	}
	ensureGoalAcceptanceSpec(&goal)
	goal.Normalize()
	session.UpsertGoal(goal)

	if err := rt.runGoalLoop(context.Background(), goal.ID); err != nil {
		t.Fatalf("runGoalLoop: %v", err)
	}
	active, ok := session.ActiveGoal()
	if !ok {
		// complete goals may not be "active" depending on ActiveGoal filter
		idx, found := session.GoalIndex(goal.ID)
		if !found {
			t.Fatal("goal missing after run")
		}
		active = session.Goals[idx]
	}
	if active.Status != goalStatusComplete {
		t.Fatalf("expected goal complete after both slices, got %#v out=%s", active, output.String())
	}
	if active.SlicePlan == nil || !goalSlicePlanAllComplete(*active.SlicePlan) {
		t.Fatalf("expected all slices complete, got %#v", active.SlicePlan)
	}
	if semanticApprovals < 2 {
		t.Fatalf("expected at least 2 semantic approvals (one per slice), got %d", semanticApprovals)
	}
	if !strings.Contains(output.String(), "Slice complete") {
		t.Fatalf("expected slice-complete message, got %q", output.String())
	}
}
