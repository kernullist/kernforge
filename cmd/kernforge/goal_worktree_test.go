package main

import (
	"strings"
	"testing"
)

func TestParseGoalStartOptionsWorktreeFlag(t *testing.T) {
	rt := &runtimeState{workspace: Workspace{Root: t.TempDir()}}
	opts, err := rt.parseGoalStartOptions([]string{"--worktree", "--no-run", "ship feature"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !opts.UseWorktree {
		t.Fatal("expected UseWorktree")
	}
	if opts.Objective != "ship feature" {
		t.Fatalf("objective=%q", opts.Objective)
	}
	opts2, err := rt.parseGoalStartOptions([]string{"--isolated", "ship feature"})
	if err != nil {
		t.Fatalf("parse isolated: %v", err)
	}
	if !opts2.UseWorktree {
		t.Fatal("expected --isolated to enable worktree")
	}
}

func TestEnsureGoalWorktreeReusesActiveSessionWorktree(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.BaseWorkingDir = root
	session.WorkingDir = root
	session.Worktree = &SessionWorktree{
		ID:     "existing",
		Root:   root,
		Branch: "kernforge/existing",
		Active: true,
		Managed: true,
	}
	rt := &runtimeState{
		session: session,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}
	goal := GoalState{ID: "goal-1", Objective: "do work"}
	if err := rt.ensureGoalWorktreeIsolation(&goal); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if goal.WorktreeID != "existing" || goal.WorktreeRoot != root {
		t.Fatalf("expected reuse of session worktree, got id=%q root=%q", goal.WorktreeID, goal.WorktreeRoot)
	}
	if len(goal.Events) == 0 || !strings.Contains(goal.Events[len(goal.Events)-1].Kind, "worktree") {
		t.Fatalf("expected worktree event, got %#v", goal.Events)
	}
}
