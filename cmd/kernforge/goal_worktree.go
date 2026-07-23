package main

import (
	"context"
	"fmt"
	"strings"
)

// ensureGoalWorktreeIsolation attaches an isolated git worktree for the goal
// when requested. Reuses an already-active session worktree when present so
// nested create does not fight /worktree create. Parallel multi-worktree
// execution is intentionally out of scope (see plan PR5).
func (rt *runtimeState) ensureGoalWorktreeIsolation(goal *GoalState) error {
	if rt == nil || rt.session == nil || goal == nil {
		return fmt.Errorf("no active session")
	}
	if sessionHasActiveWorktree(rt.session) {
		wt := rt.session.Worktree
		goal.WorktreeID = wt.ID
		goal.WorktreeRoot = wt.Root
		goal.WorktreeBranch = wt.Branch
		appendGoalEvent(goal, "worktree_reuse", wt.Root)
		if rt.writer != nil {
			fmt.Fprintln(rt.writer, rt.ui.infoLine("Goal will use the active session worktree: "+wt.Root))
		}
		return nil
	}
	name := firstNonBlankString(goal.ID, compactPromptSection(goal.Objective, 40), "goal")
	manager := newWorktreeManager(rt.cfg)
	base := sessionBaseWorkingDir(rt.session)
	if strings.TrimSpace(base) == "" {
		base = workspaceSnapshotRoot(rt.workspace)
	}
	worktree, err := manager.Create(context.Background(), base, name)
	if err != nil {
		return fmt.Errorf("goal worktree isolation: %w", err)
	}
	if err := rt.attachWorktree(worktree); err != nil {
		// Best-effort cleanup of the orphaned worktree directory/branch.
		_ = manager.Remove(context.Background(), base, worktree)
		return fmt.Errorf("goal worktree attach: %w", err)
	}
	goal.WorktreeID = worktree.ID
	goal.WorktreeRoot = worktree.Root
	goal.WorktreeBranch = worktree.Branch
	appendGoalEvent(goal, "worktree_created", worktree.Root)
	if rt.writer != nil {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Goal isolated worktree: "+worktree.Root))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("branch", worktree.Branch))
		fmt.Fprintln(rt.writer, rt.ui.hintLine("When the goal finishes, review the branch and use /worktree leave or /worktree cleanup as needed."))
	}
	return nil
}
