package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (rt *runtimeState) handleVerifyCommand(args string) error {
	changed := collectVerificationChangedPaths(rt.workspace.Root, rt.session)
	mode := VerificationAdaptive
	if strings.TrimSpace(args) != "" {
		override := []string{}
		for _, item := range strings.Fields(args) {
			if strings.EqualFold(strings.TrimSpace(item), "--full") {
				mode = VerificationFull
				continue
			}
			for _, path := range strings.Split(item, ",") {
				if value := normalizeVerificationOverridePath(path); value != "" {
					override = append(override, value)
				}
			}
		}
		if len(override) > 0 {
			changed = override
		}
	}
	tuning := VerificationTuning{}
	if rt.verifyHistory != nil {
		if loaded, err := rt.verifyHistory.PlannerTuning(rt.workspace.Root); err == nil {
			tuning = loaded
		}
	}
	plan := buildVerificationPlanWithTuning(rt.workspace.Root, changed, mode, tuning)
	if len(plan.Steps) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No recommended verification steps were found for this workspace."))
		return nil
	}
	if verdict, err := rt.workspace.Hook(context.Background(), HookPreVerification, HookPayload{
		"trigger":       "manual",
		"mode":          string(plan.Mode),
		"changed_files": append([]string(nil), changed...),
	}); err == nil {
		if len(verdict.VerificationAdds) > 0 {
			for _, step := range verdict.VerificationAdds {
				if !verificationStepExists(plan.Steps, VerificationPolicyStep{
					Label:   step.Label,
					Command: step.Command,
					Stage:   step.Stage,
				}) {
					plan.Steps = append(plan.Steps, step)
				}
			}
			plan.PlannerNote = joinSentence(plan.PlannerNote, fmt.Sprintf("Hook engine added %d verification step(s).", len(verdict.VerificationAdds)))
		}
		if len(verdict.ContextAdds) > 0 {
			plan.PlannerNote = joinSentence(plan.PlannerNote, "Hook review context: "+strings.Join(verdict.ContextAdds, " | "))
		}
	} else {
		return err
	}
	report := executeVerificationSteps(context.Background(), rt.workspace, "manual", plan)
	rt.session.LastVerification = &report
	_ = rt.store.Save(rt.session)
	if rt.verifyHistory != nil {
		_ = rt.verifyHistory.Append(rt.session.ID, workspaceSnapshotRoot(rt.workspace), report)
	}
	_, _ = rt.workspace.Hook(context.Background(), HookPostVerification, HookPayload{
		"trigger":       "manual",
		"mode":          string(report.Mode),
		"changed_files": append([]string(nil), changed...),
		"output":        report.SummaryLine(),
		"error":         report.FailureSummary(),
	})
	fmt.Fprintln(rt.writer, rt.ui.section("Verification"))
	fmt.Fprintln(rt.writer, report.RenderTerminal(rt.ui))
	activeFeature, hasActiveFeature := rt.activeFeatureForHandoff()
	if handoff := verificationHandoff(report, activeFeature, hasActiveFeature); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) activeFeatureForHandoff() (FeatureWorkflow, bool) {
	if rt == nil || rt.session == nil || strings.TrimSpace(rt.session.ActiveFeatureID) == "" {
		return FeatureWorkflow{}, false
	}
	activeID := strings.TrimSpace(rt.session.ActiveFeatureID)
	store := rt.featureStore()
	if store == nil {
		return FeatureWorkflow{ID: activeID}, true
	}
	feature, err := store.Load(activeID)
	if err != nil {
		return FeatureWorkflow{ID: activeID}, true
	}
	return feature, true
}

func (rt *runtimeState) handleVerifyDashboardCommand(args string) error {
	if rt.verifyHistory == nil {
		return fmt.Errorf("verification history is not configured")
	}
	all, tags := parseVerificationDashboardArgs(args)
	summary, err := rt.verifyHistory.Dashboard(workspaceSnapshotRoot(rt.workspace), all, tags, 10)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Verification Dashboard"))
	fmt.Fprintln(rt.writer, renderVerificationDashboard(summary))
	return nil
}

func (rt *runtimeState) handleVerifyDashboardHTMLCommand(args string) error {
	if rt.verifyHistory == nil {
		return fmt.Errorf("verification history is not configured")
	}
	all, tags := parseVerificationDashboardArgs(args)
	summary, err := rt.verifyHistory.Dashboard(workspaceSnapshotRoot(rt.workspace), all, tags, 20)
	if err != nil {
		return err
	}
	outputPath, err := createVerificationDashboardHTML(summary, all)
	if err != nil {
		return err
	}
	if err := OpenExternalURL(outputPath); err != nil {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Generated HTML dashboard but could not open it automatically: "+err.Error()))
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Generated verification dashboard: "+outputPath))
	return nil
}

func parseCheckpointTargetAndPaths(raw string) (string, []string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "latest", nil, nil
	}
	target := trimmed
	var paths []string
	if idx := strings.Index(trimmed, " -- "); idx >= 0 {
		target = strings.TrimSpace(trimmed[:idx])
		pathPart := strings.TrimSpace(trimmed[idx+4:])
		for _, item := range strings.Split(pathPart, ",") {
			if value := strings.TrimSpace(item); value != "" {
				paths = append(paths, value)
			}
		}
	}
	if target == "" {
		target = "latest"
	}
	return target, paths, nil
}

func (rt *runtimeState) handleCheckpointCommand(args string) error {
	if rt.checkpoints == nil {
		return fmt.Errorf("checkpoint manager is not configured")
	}
	name := strings.TrimSpace(args)
	if name == "" && rt.interactive {
		value, err := rt.promptValueAllowEmpty("Checkpoint note (optional)", "")
		if err != nil {
			if errors.Is(err, ErrPromptCanceled) {
				return fmt.Errorf("checkpoint creation canceled")
			}
			return err
		}
		name = strings.TrimSpace(value)
	}
	ok, err := rt.perms.Allow(ActionWrite, "create checkpoint for "+workspaceCheckpointRoot(rt.workspace))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("checkpoint creation canceled")
	}
	meta, err := rt.checkpoints.Create(workspaceCheckpointRoot(rt.workspace), name)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Created checkpoint %s (%s)", meta.ID, meta.Name)))
	if handoff := checkpointHandoffAfterCreate(meta); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handleCheckpointAutoCommand(args string) error {
	if strings.TrimSpace(args) == "" {
		fmt.Fprintln(rt.writer, rt.ui.infoLine(fmt.Sprintf("Automatic checkpoint before edits: %t", configAutoCheckpointEdits(rt.cfg))))
		return nil
	}
	value, ok := parseBoolString(args)
	if !ok {
		return fmt.Errorf("usage: /checkpoint-auto [on|off]")
	}
	rt.cfg.AutoCheckpointEdits = boolPtr(value)
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Automatic checkpoint before edits set to %t", value)))
	return nil
}

func (rt *runtimeState) handleCheckpointsCommand() error {
	if rt.checkpoints == nil {
		return fmt.Errorf("checkpoint manager is not configured")
	}
	items, err := rt.checkpoints.List(workspaceCheckpointRoot(rt.workspace))
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No checkpoints found for this workspace."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Checkpoints"))
	for _, item := range items {
		fmt.Fprintf(rt.writer, "%s  %s  files=%d  size=%dB\n", rt.ui.dim(item.ID), item.Name, item.FileCount, item.TotalBytes)
	}
	if handoff := checkpointsHandoff(items); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handleCheckpointDiffCommand(args string) error {
	if rt.checkpoints == nil {
		return fmt.Errorf("checkpoint manager is not configured")
	}
	target, paths, err := parseCheckpointTargetAndPaths(args)
	if err != nil {
		return err
	}
	meta, diffs, err := rt.checkpoints.Diff(workspaceCheckpointRoot(rt.workspace), target, paths)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Checkpoint Diff"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("checkpoint", meta.ID))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("name", meta.Name))
	if len(paths) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("paths", strings.Join(paths, ", ")))
	}
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, renderCheckpointDiffTerminal(rt.ui, diffs))
	if handoff := checkpointDiffHandoff(meta, diffs); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handleRollbackCommand(args string) error {
	if rt.checkpoints == nil {
		return fmt.Errorf("checkpoint manager is not configured")
	}
	trimmedArgs := strings.TrimSpace(args)
	if trimmedArgs == "" || strings.EqualFold(trimmedArgs, "pick") || strings.EqualFold(trimmedArgs, "choose") {
		selected, err := rt.pickRollbackCheckpoint()
		if err != nil {
			return err
		}
		if strings.TrimSpace(selected) == "" {
			return fmt.Errorf("rollback canceled")
		}
		args = selected
	}
	target, paths, err := parseCheckpointTargetAndPaths(args)
	if err != nil {
		return err
	}
	meta, _, err := rt.checkpoints.Resolve(workspaceCheckpointRoot(rt.workspace), target)
	if err != nil {
		return err
	}
	ok, err := rt.perms.Allow(ActionWrite, "rollback workspace to checkpoint "+meta.ID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("rollback canceled")
	}
	if rt.interactive {
		scope := "workspace"
		if len(paths) > 0 {
			scope = strings.Join(paths, ", ")
		}
		confirm, err := rt.confirm(fmt.Sprintf("Rollback %s to checkpoint %s (%s)? This will overwrite current files.", scope, meta.ID, meta.Name))
		if err != nil {
			return err
		}
		if !confirm {
			return fmt.Errorf("rollback canceled")
		}
	}
	safetyName := "pre-rollback-" + time.Now().Format("20060102-150405")
	if safety, err := rt.checkpoints.Create(workspaceCheckpointRoot(rt.workspace), safetyName); err == nil {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("Created safety checkpoint "+safety.ID))
	}
	var restored CheckpointMetadata
	if len(paths) > 0 {
		restored, err = rt.checkpoints.RollbackPaths(workspaceCheckpointRoot(rt.workspace), meta.ID, paths)
	} else {
		restored, err = rt.checkpoints.Rollback(workspaceCheckpointRoot(rt.workspace), meta.ID)
	}
	if err != nil {
		return err
	}
	if err := rt.reloadRuntimeConfig(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Rolled back to checkpoint %s (%s)", restored.ID, restored.Name)))
	return nil
}

func (rt *runtimeState) pickRollbackCheckpoint() (string, error) {
	items, err := rt.checkpoints.List(workspaceCheckpointRoot(rt.workspace))
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No checkpoints found for this workspace."))
		return "", nil
	}

	fmt.Fprintln(rt.writer, rt.ui.section("Choose Checkpoint"))
	for i, item := range items {
		fmt.Fprintf(rt.writer, "%s  %s  %s  files=%d  size=%dB\n",
			rt.ui.accent(fmt.Sprintf("%2d.", i+1)),
			rt.ui.dim(item.ID),
			item.Name,
			item.FileCount,
			item.TotalBytes,
		)
	}
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Enter a checkpoint number, or press Enter to cancel."))

	for {
		answer, err := rt.readInput(rt.ui.accent("rollback pick") + rt.ui.dim(" > "))
		if err != nil {
			if errors.Is(err, ErrPromptCanceled) {
				return "", nil
			}
			return "", err
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			return "", nil
		}
		index, convErr := strconv.Atoi(answer)
		if convErr != nil || index < 1 || index > len(items) {
			fmt.Fprintln(rt.writer, rt.ui.warnLine(fmt.Sprintf("Choose a number between 1 and %d.", len(items))))
			continue
		}
		return items[index-1].ID, nil
	}
}
