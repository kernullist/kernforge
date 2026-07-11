package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoalPendingImplementationDecisionMaxOneResumesSameIteration(t *testing.T) {
	root := t.TempDir()
	writeGoalTestModule(t, root)
	session := NewSession(root, "provider", "model", "", "default")
	goal := GoalState{
		ID:            "goal-decision-max-one",
		Objective:     "finish the implementation after a user decision",
		Status:        goalStatusActive,
		MaxIterations: 1,
	}
	goal.Normalize()
	session.UpsertGoal(goal)

	var output bytes.Buffer
	implementationCalls := 0
	store := NewSessionStore(filepath.Join(root, "sessions"))
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   store,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
		goalReply: func(ctx context.Context, prompt string) (string, error) {
			switch {
			case strings.Contains(prompt, "Autonomous goal iteration"):
				implementationCalls++
				if implementationCalls == 1 {
					session.PendingImplementationDecision = &PendingImplementationDecision{
						DecisionID: "decision-max-one",
						Stage:      implementationDecisionPendingChoice,
						Record: ImplementationDecisionRecord{
							ID:      "decision-max-one",
							Problem: "Choose the implementation strategy",
						},
					}
					return "waiting for the user decision", nil
				}
				return "implementation resumed after the user decision", nil
			case strings.Contains(prompt, "Autonomous goal independent review pass"):
				return "APPROVED: implementation is correct", nil
			case strings.Contains(prompt, "Final semantic goal review"):
				return "APPROVED: all goal criteria are satisfied", nil
			default:
				t.Fatalf("unexpected goal prompt: %s", prompt)
				return "", nil
			}
		},
	}
	rt.checkpoints = &CheckpointManager{Root: filepath.Join(root, "checkpoints")}
	useFastGoalRuntime(t, rt)

	if err := rt.runGoalBySelector(goal.ID, 0); err != nil {
		t.Fatalf("initial goal run: %v", err)
	}
	paused, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("load paused session: %v", err)
	}
	pausedGoal, ok := paused.ActiveGoal()
	if !ok {
		t.Fatal("paused session lost the active goal")
	}
	if pausedGoal.Status != goalStatusPaused || pausedGoal.Iteration != 0 {
		t.Fatalf("decision pause consumed the iteration budget: %#v", pausedGoal)
	}
	if len(pausedGoal.Iterations) != 1 || pausedGoal.Iterations[0].Index != 1 || pausedGoal.Iterations[0].Status != goalStatusPaused {
		t.Fatalf("paused iteration evidence was not retained: %#v", pausedGoal.Iterations)
	}
	if len(pausedGoal.CheckpointRefs) != 1 || pausedGoal.CheckpointRefs[0].Iteration != 1 {
		t.Fatalf("paused iteration checkpoint was not retained exactly once: %#v", pausedGoal.CheckpointRefs)
	}
	pausedCheckpointID := pausedGoal.CheckpointRefs[0].ID

	paused.PendingImplementationDecision = nil
	if err := store.Save(paused); err != nil {
		t.Fatalf("save completed decision state: %v", err)
	}
	rt.session = paused
	if err := rt.runGoalBySelector(goal.ID, 0); err != nil {
		t.Fatalf("resume goal run: %v", err)
	}

	completed, ok := paused.ActiveGoal()
	if !ok {
		t.Fatal("resumed session lost the active goal")
	}
	if completed.Status != goalStatusComplete || completed.Iteration != 1 {
		t.Fatalf("max_iterations=1 goal did not complete after resume: %#v", completed)
	}
	if len(completed.Iterations) != 2 || completed.Iterations[1].Index != 1 || completed.Iterations[1].Status != goalStatusComplete {
		t.Fatalf("resume did not finish the paused iteration: %#v", completed.Iterations)
	}
	if len(completed.CheckpointRefs) != 1 || completed.CheckpointRefs[0].ID != pausedCheckpointID {
		t.Fatalf("resume replaced the original iteration checkpoint baseline: before=%q refs=%#v", pausedCheckpointID, completed.CheckpointRefs)
	}
	if strings.Contains(output.String(), "goal reached max iterations") {
		t.Fatalf("resume was blocked by max_iterations: %s", output.String())
	}
}
