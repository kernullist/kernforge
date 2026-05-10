package main

import (
	"context"
	"fmt"
	"strings"
)

func (a *Agent) maybeRunPostChangeReview(ctx context.Context, request string, lastFingerprint string) (bool, bool, string, string, error) {
	if a == nil || a.Session == nil {
		return false, false, "", "", nil
	}
	reviewCfg := configReviewHarness(a.Config)
	if reviewCfg.AutoAfterChange == nil || !*reviewCfg.AutoAfterChange {
		return false, false, "", "", nil
	}
	root := workspaceSnapshotRoot(a.Workspace)
	if strings.TrimSpace(root) == "" {
		root = a.Workspace.Root
	}
	if strings.TrimSpace(root) == "" {
		root = a.Session.WorkingDir
	}
	if strings.TrimSpace(root) == "" {
		return false, false, "", "", nil
	}
	if len(autoReviewChangedPaths(a.Session, root)) == 0 {
		return false, false, "", "", nil
	}
	if a.Session.LastReviewRun != nil &&
		a.Session.LastReviewRun.AutoTriggered &&
		strings.EqualFold(a.Session.LastReviewRun.Trigger, "post_change") &&
		a.Session.LastReviewRun.ReviewFingerprint != "" &&
		a.Session.LastReviewRun.ReviewFingerprint == strings.TrimSpace(lastFingerprint) {
		return false, false, "", lastFingerprint, nil
	}
	if a.EmitProgress != nil {
		a.EmitProgress(localizedText(a.Config, "Running automatic post-change review...", "자동 변경 후 리뷰를 실행합니다..."))
	}
	rt := a.reviewHarnessRuntime(root)
	run, err := runReviewHarness(ctx, rt, ReviewHarnessOptions{
		Trigger:         "post_change",
		Target:          reviewTargetChange,
		Request:         request,
		IncludeGitDiff:  true,
		NoModel:         !postChangeReviewHasDedicatedModel(a),
		AutoTriggered:   true,
		AutoFollowUp:    reviewCfg.AutoFollowUp,
		MaxContextChars: 60000,
	})
	if err != nil {
		return true, false, "", "", err
	}
	fingerprint := strings.TrimSpace(run.ReviewFingerprint)
	needsRevision := run.Gate.Verdict == reviewVerdictNeedsRevision ||
		run.Gate.Verdict == reviewVerdictBlocked ||
		run.Gate.Verdict == reviewVerdictInsufficientEvidence
	feedback := formatPostChangeReviewFeedback(run, needsRevision)
	return true, needsRevision, feedback, fingerprint, nil
}

func (a *Agent) reviewProposedEdit(ctx context.Context, preview EditPreview) error {
	if a == nil || a.Session == nil {
		return nil
	}
	reviewCfg := configReviewHarness(a.Config)
	if reviewCfg.AutoAfterChange == nil || !*reviewCfg.AutoAfterChange {
		return nil
	}
	diff := strings.TrimSpace(preview.Preview)
	if diff == "" {
		return nil
	}
	root := workspaceSnapshotRoot(a.Workspace)
	if strings.TrimSpace(root) == "" {
		root = a.Workspace.Root
	}
	if strings.TrimSpace(root) == "" {
		root = a.Session.WorkingDir
	}
	if strings.TrimSpace(root) == "" {
		return nil
	}
	if a.EmitProgress != nil {
		a.EmitProgress(localizedText(a.Config, "Running automatic pre-write review...", "자동 쓰기 전 리뷰를 실행합니다..."))
	}
	rt := a.reviewHarnessRuntime(root)
	run, err := runReviewHarness(ctx, rt, ReviewHarnessOptions{
		Trigger:         "pre_write",
		Target:          reviewTargetChange,
		Request:         latestUserMessageText(a.Session.Messages),
		Paths:           append([]string(nil), preview.Paths...),
		ProvidedDiff:    diff,
		IncludeGitDiff:  false,
		NoModel:         !postChangeReviewHasDedicatedModel(a),
		AutoTriggered:   true,
		AutoFollowUp:    "none",
		MaxContextChars: 60000,
	})
	if err != nil {
		if a.EmitProgress != nil {
			a.EmitProgress(localizedText(a.Config, "Automatic pre-write review failed: ", "자동 쓰기 전 리뷰 실패: ") + err.Error())
		}
		return fmt.Errorf("automatic pre-write review failed before writing: %w", err)
	}
	needsRevision := run.Gate.Verdict == reviewVerdictNeedsRevision ||
		run.Gate.Verdict == reviewVerdictBlocked ||
		run.Gate.Verdict == reviewVerdictInsufficientEvidence
	if needsRevision {
		if a.EmitProgress != nil {
			a.EmitProgress(localizedText(a.Config, "Automatic pre-write review found blockers. Asking the model to revise...", "자동 쓰기 전 리뷰에서 차단 finding을 발견했습니다. 모델에게 수정을 요청합니다..."))
		}
		return fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\n%s", formatPreWriteReviewFeedback(run))
	}
	if a.EmitProgress != nil {
		a.EmitProgress(localizedText(a.Config, "Automatic pre-write review completed.", "자동 쓰기 전 리뷰가 완료되었습니다."))
	}
	return nil
}

func (a *Agent) runAutomaticPostChangeReviewGate(ctx context.Context, request string, lastFingerprint *string, revisionCount *int, exhaustedNudge *bool) (bool, error) {
	if a == nil || a.Session == nil || lastFingerprint == nil || revisionCount == nil || exhaustedNudge == nil {
		return false, nil
	}
	reviewed, needsRevision, reviewFeedback, fingerprint, err := a.maybeRunPostChangeReview(ctx, request, *lastFingerprint)
	if err != nil {
		if a.EmitProgress != nil {
			a.EmitProgress("Automatic post-change review failed: " + err.Error())
		}
		return false, nil
	}
	if !reviewed {
		return false, nil
	}
	*lastFingerprint = fingerprint
	if a.EmitProgress != nil {
		if needsRevision {
			a.EmitProgress("Automatic post-change review found blockers. Asking the model to revise...")
		} else {
			a.EmitProgress("Automatic post-change review completed.")
		}
	}
	if needsRevision && *revisionCount < configReviewHarness(a.Config).AutoRepairMaxRounds {
		*revisionCount++
		a.Session.AddMessage(Message{
			Role: "user",
			Text: reviewFeedback,
		})
		if a.Store != nil {
			if err := a.Store.Save(a.Session); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	if needsRevision && !*exhaustedNudge {
		*exhaustedNudge = true
		a.Session.AddMessage(Message{
			Role: "user",
			Text: reviewFeedback + "\n\nAutomatic post-change review still has blockers, but the automatic repair limit is exhausted. Do not claim completion. Provide the final answer as blocked or incomplete, cite the review gate, and list the exact remaining actions.",
		})
		if a.Store != nil {
			if err := a.Store.Save(a.Session); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	return false, nil
}

func (a *Agent) reviewHarnessRuntime(root string) *runtimeState {
	return &runtimeState{
		cfg:           a.Config,
		store:         a.Store,
		session:       a.Session,
		agent:         a,
		memory:        a.Memory,
		longMem:       a.LongMem,
		evidence:      a.Evidence,
		verifyHistory: a.VerifyHistory,
		functionFuzz:  a.FunctionFuzz,
		fuzzCampaigns: a.FuzzCampaigns,
		modelRoutes:   a.ModelRoutes,
		mcp:           a.MCP,
		workspace: Workspace{
			BaseRoot:              root,
			Root:                  firstNonBlankString(a.Workspace.Root, root),
			Shell:                 a.Workspace.Shell,
			ShellTimeout:          a.Workspace.ShellTimeout,
			ReadHintSpans:         a.Workspace.ReadHintSpans,
			ReadCacheEntries:      a.Workspace.ReadCacheEntries,
			VerificationToolPaths: a.Workspace.VerificationToolPaths,
			ToolHints:             a.Workspace.ToolHints,
			Perms:                 a.Workspace.Perms,
			PrepareEdit:           a.Workspace.PrepareEdit,
			PrepareEditAtRoot:     a.Workspace.PrepareEditAtRoot,
			ReviewEdit:            a.Workspace.ReviewEdit,
			ReportProgress:        a.Workspace.ReportProgress,
			CurrentSelection:      a.Workspace.CurrentSelection,
			PreviewEdit:           a.Workspace.PreviewEdit,
			UpdatePlan:            a.Workspace.UpdatePlan,
			GetPlan:               a.Workspace.GetPlan,
			RunHook:               a.Workspace.RunHook,
			BackgroundJobs:        a.Workspace.BackgroundJobs,
			ResolveEditTarget:     a.Workspace.ResolveEditTarget,
			ResolveShellRoot:      a.Workspace.ResolveShellRoot,
		},
	}
}

func (a *Agent) shouldSkipPostChangeReviewForKnownFinalBlocker(reply string, unresolvedVerification bool) bool {
	if a == nil || a.Session == nil {
		return false
	}
	mentionsVerificationBlocker := replyMentionsVerificationBlocker(reply) || replyMentionsVerificationNotRun(reply)
	if !mentionsVerificationBlocker {
		return false
	}
	if unresolvedVerification || (a.Session.LastVerification != nil && a.Session.LastVerification.HasFailures()) {
		return true
	}
	if a.Session.AcceptanceContract != nil {
		contract := *a.Session.AcceptanceContract
		contract.Normalize()
		if contract.VerificationRequired && !sessionHasSuccessfulVerificationEvidence(a.Session) {
			return true
		}
	}
	return false
}

func postChangeReviewHasDedicatedModel(a *Agent) bool {
	if a == nil {
		return false
	}
	reviewCfg := configReviewHarness(a.Config)
	for _, role := range []string{"primary_reviewer", "design_reviewer", "security_reviewer", "false_positive_reviewer", "regression_reviewer", "test_reviewer", "final_gate_reviewer"} {
		if roleCfg, ok := reviewCfg.RoleModels[role]; ok && strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
			return true
		}
	}
	return false
}

func autoReviewChangedPaths(session *Session, root string) []string {
	var paths []string
	paths = append(paths, sessionPatchTransactionChangedPaths(session)...)
	paths = append(paths, filterReviewablePaths(delegationChangedFiles(root))...)
	return normalizeTaskStateList(paths, 128)
}

func formatPostChangeReviewFeedback(run ReviewRun, needsRevision bool) string {
	var b strings.Builder
	if needsRevision {
		b.WriteString("Automatic post-change review found blockers. Fix them before final answer.")
	} else {
		b.WriteString("Automatic post-change review completed.")
	}
	fmt.Fprintf(&b, "\n\nReview gate: %s", valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
	if strings.TrimSpace(run.MachineStatus) != "" {
		fmt.Fprintf(&b, " (%s)", run.MachineStatus)
	}
	if strings.TrimSpace(run.Result.Summary) != "" {
		fmt.Fprintf(&b, "\nSummary: %s", run.Result.Summary)
	}
	if needsRevision {
		if strings.TrimSpace(run.RepairPlan.Prompt) != "" {
			b.WriteString("\n\n")
			b.WriteString(run.RepairPlan.Prompt)
		} else {
			b.WriteString("\n\nInline review findings:\n")
			b.WriteString(renderReviewInlineFindings(run, true))
		}
		b.WriteString("\n\nImplementation rules:\n")
		b.WriteString("- Do not read review artifact files; all required review guidance is included here.\n")
		b.WriteString("- Revise only the changed code needed to satisfy the review gate.\n")
		b.WriteString("- Run focused verification when the finding asks for it.")
		return strings.TrimSpace(b.String())
	}
	if len(run.Gate.WarningFindings) > 0 {
		fmt.Fprintf(&b, "\nWarnings: %d", len(run.Gate.WarningFindings))
	}
	if len(run.Gate.NextCommands) > 0 {
		next := run.Gate.NextCommands[0]
		if strings.TrimSpace(next.Command) != "" {
			fmt.Fprintf(&b, "\nNext: %s", next.Command)
			if strings.TrimSpace(next.Reason) != "" {
				fmt.Fprintf(&b, " (%s)", next.Reason)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func formatPreWriteReviewFeedback(run ReviewRun) string {
	var b strings.Builder
	b.WriteString("Automatic pre-write review found blockers. Revise the proposed edit before writing files.")
	fmt.Fprintf(&b, "\n\nReview gate: %s", valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
	if strings.TrimSpace(run.MachineStatus) != "" {
		fmt.Fprintf(&b, " (%s)", run.MachineStatus)
	}
	if strings.TrimSpace(run.Result.Summary) != "" {
		fmt.Fprintf(&b, "\nSummary: %s", run.Result.Summary)
	}
	if strings.TrimSpace(run.RepairPlan.Prompt) != "" {
		b.WriteString("\n\n")
		b.WriteString(run.RepairPlan.Prompt)
	} else {
		b.WriteString("\n\nInline review findings:\n")
		b.WriteString(renderReviewInlineFindings(run, true))
	}
	b.WriteString("\n\nImplementation rules:\n")
	b.WriteString("- Do not read review artifact files; all required review guidance is included here.\n")
	b.WriteString("- Return a corrected edit proposal instead of retrying the same patch.")
	return strings.TrimSpace(b.String())
}
