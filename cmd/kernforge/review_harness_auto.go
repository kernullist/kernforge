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
		EditProposals:   editProposalsForPreview(preview),
		RepairFindings:  preWriteRepairObligationsFromLastReview(a.Session),
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
	if needsRevision && reviewRunHasRequiredReviewerFailure(run) {
		if a.EmitProgress != nil {
			a.EmitProgress(localizedText(a.Config, "Automatic pre-write review could not use the required reviewer. Stopping the edit loop.", "자동 쓰기 전 리뷰에서 필수 리뷰어 결과를 신뢰할 수 없어 편집 루프를 중단합니다."))
		}
		return fmt.Errorf("%w: %s", ErrReviewerGateUnavailable, formatReviewerGateUnavailableToolError(run))
	}
	if needsRevision {
		if a.EmitProgress != nil {
			a.EmitProgress(localizedText(a.Config, "Automatic pre-write review found blockers. Asking the model to revise...", "자동 쓰기 전 리뷰에서 차단 finding을 발견했습니다. 모델에게 수정을 요청합니다..."))
		}
		return fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\n%s", formatPreWriteReviewFeedback(run))
	}
	if warningBlockers := preWriteReviewBlockingWarningFindings(run); len(warningBlockers) > 0 {
		if a.EmitProgress != nil {
			a.EmitProgress(localizedText(a.Config, "Automatic pre-write review found actionable warnings. Asking the model to revise...", "자동 쓰기 전 리뷰에서 수정이 필요한 경고를 발견했습니다. 모델에게 수정을 요청합니다..."))
		}
		return fmt.Errorf("automatic pre-write review blocked this edit on actionable warnings before writing:\n\n%s", formatPreWriteReviewWarningBlockFeedback(run, warningBlockers))
	}
	if a.EmitProgress != nil {
		if len(run.Gate.WarningFindings) > 0 {
			a.EmitProgress(formatPreWriteReviewWarningProgress(a.Config, run))
		} else {
			a.EmitProgress(localizedText(a.Config, "Automatic pre-write review completed.", "자동 쓰기 전 리뷰가 완료되었습니다."))
		}
	}
	return nil
}

func preWriteRepairObligationsFromLastReview(session *Session) []ReviewFinding {
	if session == nil || session.LastReviewRun == nil {
		return nil
	}
	return preFixRepairObligationFindings(*session.LastReviewRun)
}

func preFixRepairObligationFindings(run ReviewRun) []ReviewFinding {
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		return nil
	}
	out := make([]ReviewFinding, 0, len(run.Findings))
	for _, finding := range run.Findings {
		finding.Normalize()
		if strings.TrimSpace(finding.ID) == "" {
			continue
		}
		if reviewFindingBlocksGate(run, finding) {
			out = append(out, finding)
			continue
		}
		if !preWritePreFixWarningShouldBeRepairObligation(finding) {
			continue
		}
		out = append(out, finding)
	}
	return out
}

func preWritePreFixWarningShouldBeRepairObligation(finding ReviewFinding) bool {
	return reviewFindingShouldBeRepairPlanWarning(finding)
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
	if a.ReviewerClient != nil && strings.TrimSpace(a.ReviewerModel) != "" {
		return true
	}
	if a.AuxReviewerClient != nil && strings.TrimSpace(a.AuxReviewerModel) != "" {
		return true
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

func formatReviewerGateUnavailableToolError(run ReviewRun) string {
	failed := reviewFailedRequiredReviewerRuns(run)
	if len(failed) == 0 {
		return "required reviewer failed or returned weak output; stop editing and report the reviewer route issue"
	}
	var details []string
	for _, reviewerRun := range failed {
		role := firstNonBlankString(reviewRoleProgressName(reviewerRun.Role), "reviewer")
		status := valueOrDefault(strings.TrimSpace(reviewerRun.Status), "unknown")
		quality := valueOrDefault(strings.TrimSpace(reviewerRun.ModelQuality), "unknown")
		detail := firstNonBlankString(firstNonEmptyLine(reviewerRun.Error), "reviewer output was too weak")
		details = append(details, fmt.Sprintf("%s status=%s quality=%s: %s", role, status, quality, detail))
	}
	return "required reviewer failed or returned weak output; stop editing and report the reviewer route issue: " + strings.Join(details, " | ")
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
	b.WriteString("\n- Do not use run_shell, PowerShell file APIs, redirection, or direct filesystem writes to bypass pre-write review; use edit tools so the corrected proposal is reviewed.")
	b.WriteString("\n- This is local code review/repair work. Do not use MCP web/search/browser tools or external web research to satisfy this gate; use local source evidence and the inline findings above.")
	return strings.TrimSpace(b.String())
}

func formatPreWriteReviewWarningBlockFeedback(run ReviewRun, warnings []ReviewFinding) string {
	var b strings.Builder
	b.WriteString("Automatic pre-write review found actionable warnings. Revise the proposed edit before writing files.")
	fmt.Fprintf(&b, "\n\nReview gate: %s", valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
	if strings.TrimSpace(run.MachineStatus) != "" {
		fmt.Fprintf(&b, " (%s)", run.MachineStatus)
	}
	if strings.TrimSpace(run.Result.Summary) != "" {
		fmt.Fprintf(&b, "\nSummary: %s", run.Result.Summary)
	}
	b.WriteString("\n\nActionable warning findings:\n")
	b.WriteString(renderReviewInlineFindings(ReviewRun{Findings: warnings}, true))
	b.WriteString("\n\nImplementation rules:\n")
	b.WriteString("- Treat these pre-write warnings as required repair guidance.\n")
	b.WriteString("- Revise the proposed edit so the requested API surface and implementation evidence are both present.\n")
	b.WriteString("- Do not write the previous incomplete patch.\n")
	b.WriteString("- Do not use run_shell, PowerShell file APIs, redirection, or direct filesystem writes to bypass pre-write review; use edit tools so the corrected proposal is reviewed.\n")
	b.WriteString("- This is local code review/repair work. Do not use MCP web/search/browser tools or external web research to satisfy this gate; use local source evidence and the actionable warnings above.\n")
	return strings.TrimSpace(b.String())
}

func preWriteReviewBlockingWarningFindings(run ReviewRun) []ReviewFinding {
	warningIDs := reviewFindingIDSet(run.Gate.WarningFindings)
	if len(warningIDs) == 0 {
		return nil
	}
	var out []ReviewFinding
	for _, finding := range run.Findings {
		if !warningIDs[finding.ID] {
			continue
		}
		if preWriteReviewWarningShouldBlock(finding) {
			out = append(out, finding)
		}
	}
	return out
}

func preWriteReviewWarningShouldBlock(finding ReviewFinding) bool {
	finding.Normalize()
	if !strings.EqualFold(strings.TrimSpace(finding.Source), "model") {
		return false
	}
	if reviewPreWriteWarningLooksLikeStyleGap(finding) {
		return true
	}
	if reviewSeverityRank(finding.Severity) > reviewSeverityRank(reviewSeverityMedium) {
		return false
	}
	if reviewPreWriteWarningLooksLikePureVerificationGap(finding) {
		return false
	}
	if strings.EqualFold(finding.Category, "test_gap") {
		return false
	}
	if strings.EqualFold(finding.Category, "evidence_gap") {
		return true
	}
	return true
}

func reviewPreWriteWarningLooksLikeStyleGap(finding ReviewFinding) bool {
	text := strings.ToLower(strings.Join([]string{
		finding.Title,
		finding.Evidence,
		finding.Impact,
		finding.RequiredFix,
		finding.TestRecommendation,
	}, " "))
	if text == "" {
		return false
	}
	return containsAny(text,
		"allman",
		"brace style",
		"braces style",
		"formatting",
		"indentation",
		"opening brace",
		"style violation",
		"여는 중괄호",
		"중괄호",
		"들여쓰기",
		"스타일",
		"포매팅",
	)
}

func reviewPreWriteWarningLooksLikePureVerificationGap(finding ReviewFinding) bool {
	text := strings.ToLower(strings.Join([]string{
		finding.Title,
		finding.Evidence,
		finding.Impact,
		finding.RequiredFix,
		finding.TestRecommendation,
	}, " "))
	if containsAny(text,
		"api surface",
		"accessor",
		"getter",
		"member declaration",
		"member declarations",
		"missing declaration",
		"missing implementation",
		"requested api",
		"requested scope",
		"does not implement",
		"구현 증거",
		"구현이",
		"구현되지",
		"멤버 선언",
		"선언",
		"초기값",
		"조회 기능",
		"요청 범위",
		"충족하지",
	) {
		return false
	}
	return containsAny(text,
		"verification was not run",
		"verification skipped",
		"verification was skipped",
		"verification omitted",
		"build verification",
		"build was not run",
		"build was skipped",
		"no latest verification",
		"no build verification",
		"no verification evidence",
		"no test was run",
		"not verified",
		"run verification",
		"tests were not run",
		"/verify",
		"빌드 검증",
		"검증",
		"검증 생략",
		"검증이 생략",
		"검증을 생략",
		"테스트 실행",
		"테스트가 생략",
	)
}

func formatPreWriteReviewWarningProgress(cfg Config, run ReviewRun) string {
	warnings := reviewCLIWarningFindings(run)
	korean := reviewRunPrefersKorean(cfg, run)
	if len(warnings) == 0 {
		if korean {
			return fmt.Sprintf("자동 쓰기 전 리뷰가 경고와 함께 완료되었습니다. 경고 %d개.", len(run.Gate.WarningFindings))
		}
		return fmt.Sprintf("Automatic pre-write review completed with warnings. warnings=%d.", len(run.Gate.WarningFindings))
	}
	var titles []string
	for _, finding := range limitReviewFindings(warnings, 3) {
		title := strings.TrimSpace(finding.Title)
		if title == "" {
			title = strings.TrimSpace(finding.Evidence)
		}
		if title != "" {
			titles = append(titles, fmt.Sprintf("%s %s: %s", valueOrDefault(finding.ID, "RF"), finding.Severity, compactPromptSection(title, 140)))
		}
	}
	suffix := strings.Join(titles, " | ")
	if len(warnings) > len(titles) {
		if suffix != "" {
			suffix += " | "
		}
		if korean {
			suffix += fmt.Sprintf("외 %d개", len(warnings)-len(titles))
		} else {
			suffix += fmt.Sprintf("%d more", len(warnings)-len(titles))
		}
	}
	if len(run.ArtifactRefs) > 0 {
		if suffix != "" {
			suffix += " | "
		}
		if korean {
			suffix += "보고서: " + run.ArtifactRefs[0]
		} else {
			suffix += "report: " + run.ArtifactRefs[0]
		}
	}
	if korean {
		return strings.TrimSpace(fmt.Sprintf("자동 쓰기 전 리뷰가 경고와 함께 완료되었습니다. 경고 %d개. %s", len(run.Gate.WarningFindings), suffix))
	}
	return strings.TrimSpace(fmt.Sprintf("Automatic pre-write review completed with warnings. warnings=%d. %s", len(run.Gate.WarningFindings), suffix))
}

func limitReviewFindings(findings []ReviewFinding, limit int) []ReviewFinding {
	if limit <= 0 || len(findings) <= limit {
		return findings
	}
	return findings[:limit]
}
