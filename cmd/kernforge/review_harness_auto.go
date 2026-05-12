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
		a.Session.LastReviewRun.ReviewFingerprint == strings.TrimSpace(lastFingerprint) &&
		postChangeReviewRunStillMatchesSessionEvidence(a.Session.LastReviewRun, a.Session) {
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
		MaxContextChars: reviewFocusedMaxContextChars,
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

func postChangeReviewRunStillMatchesSessionEvidence(run *ReviewRun, session *Session) bool {
	if run == nil || session == nil {
		return true
	}
	runSummary := strings.TrimSpace(run.Evidence.VerificationSummary)
	runFailed := run.Evidence.VerificationFailed
	currentSummary := ""
	currentFailed := false
	if session.LastVerification != nil {
		currentSummary = strings.TrimSpace(session.LastVerification.SummaryLine())
		currentFailed = session.LastVerification.HasFailures()
	}
	return strings.EqualFold(runSummary, currentSummary) && runFailed == currentFailed
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
		a.EmitProgress(localizedText(a.Config, "Main model prepared an edit proposal. Sending the diff to the review model before writing files.", "메인 모델이 수정안을 만들었습니다. 파일 쓰기 전에 diff를 리뷰 모델에 전달합니다."))
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
		MaxContextChars: reviewPreWriteMaxContextChars,
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
			a.emitPreWriteFinalVisibleReviewSummary(run, false)
			a.EmitProgress(formatPreWriteFinalReviewProgress(a.Config, run, false))
			a.EmitProgress(localizedText(a.Config, "Automatic pre-write review could not use the required reviewer. Stopping the edit loop.", "자동 쓰기 전 리뷰에서 필수 리뷰어 결과를 신뢰할 수 없어 편집 루프를 중단합니다."))
		}
		return fmt.Errorf("%w: %s", ErrReviewerGateUnavailable, formatReviewerGateUnavailableToolError(run))
	}
	if needsRevision {
		if a.EmitProgress != nil {
			a.emitPreWriteFinalVisibleReviewSummary(run, false)
			a.EmitProgress(formatPreWriteFinalReviewProgress(a.Config, run, false))
			a.EmitProgress(localizedText(a.Config, "Review model returned required changes. Sending the result back to the main model for a revised patch.", "리뷰 모델이 수정 필수 항목을 반환했습니다. 메인 모델에 결과를 전달해 패치를 다시 작성하게 합니다."))
		}
		return fmt.Errorf("automatic pre-write review blocked this edit before writing:\n\n%s", formatPreWriteReviewFeedback(run))
	}
	if warningBlockers := preWriteReviewBlockingWarningFindings(run); len(warningBlockers) > 0 {
		if a.EmitProgress != nil {
			a.emitPreWriteFinalVisibleReviewSummary(run, false)
			a.EmitProgress(formatPreWriteFinalReviewProgress(a.Config, run, false))
			a.EmitProgress(localizedText(a.Config, "Review model returned actionable warnings. Sending the result back to the main model for a revised patch.", "리뷰 모델이 수정이 필요한 경고를 반환했습니다. 메인 모델에 결과를 전달해 패치를 다시 작성하게 합니다."))
		}
		return fmt.Errorf("automatic pre-write review blocked this edit on actionable warnings before writing:\n\n%s", formatPreWriteReviewWarningBlockFeedback(run, warningBlockers))
	}
	if a.EmitProgress != nil {
		a.emitPreWriteFinalVisibleReviewSummary(run, true)
		a.EmitProgress(formatPreWriteFinalReviewProgress(a.Config, run, true))
	}
	return nil
}

func (a *Agent) emitPreWriteFinalVisibleReviewSummary(run ReviewRun, proceedToPreview bool) {
	if a == nil {
		return
	}
	summary := formatPreWriteFinalVisibleReviewSummary(a.Config, run, proceedToPreview)
	if strings.TrimSpace(summary) == "" {
		return
	}
	a.emitPersistentAssistantSummary(summary)
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

func normalizeReviewFindingCopies(findings []ReviewFinding) []ReviewFinding {
	out := make([]ReviewFinding, 0, len(findings))
	for _, finding := range findings {
		finding.Normalize()
		if strings.TrimSpace(firstNonBlankString(finding.ID, finding.Title, finding.Evidence, finding.Impact, finding.RequiredFix, finding.TestRecommendation)) == "" {
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

func formatPreWriteFinalReviewProgress(cfg Config, run ReviewRun, proceedToPreview bool) string {
	korean := reviewRunPrefersKorean(cfg, run)
	verdict := firstNonBlankString(run.Gate.Verdict, run.Result.Verdict, "unknown")
	blockerCount := len(run.Gate.BlockingFindings)
	warningCount := len(run.Gate.WarningFindings)
	content := preWriteReviewFinalContentProgress(cfg, run)
	report := preWriteReviewReportProgressSuffix(cfg, run)
	if korean {
		action := "diff preview로 진행하지 않습니다."
		if proceedToPreview {
			action = "diff preview로 진행합니다."
		}
		return strings.TrimSpace(fmt.Sprintf("자동 쓰기 전 리뷰가 완료되었습니다. 최종 검토 결과: %s (차단=%d, 경고=%d). %s %s%s", verdict, blockerCount, warningCount, content, action, report))
	}
	action := "Not proceeding to diff preview."
	if proceedToPreview {
		action = "Proceeding to diff preview."
	}
	return strings.TrimSpace(fmt.Sprintf("Automatic pre-write review completed. Final review result: %s (blockers=%d, warnings=%d). %s %s%s", verdict, blockerCount, warningCount, content, action, report))
}

func preWriteReviewFinalContentProgress(cfg Config, run ReviewRun) string {
	korean := reviewRunPrefersKorean(cfg, run)
	var parts []string
	summary := strings.TrimSpace(run.Result.Summary)
	if summary != "" {
		if korean {
			parts = append(parts, "요약: "+compactPromptSection(summary, 180))
		} else {
			parts = append(parts, "summary: "+compactPromptSection(summary, 180))
		}
	}
	findings := preWriteReviewProgressFindings(run)
	titles := reviewProgressFindingTitles(findings, 3)
	if len(titles) > 0 {
		if korean {
			parts = append(parts, "주요 finding: "+strings.Join(titles, " | "))
		} else {
			parts = append(parts, "key findings: "+strings.Join(titles, " | "))
		}
	} else if summary == "" {
		if korean {
			parts = append(parts, "주요 finding: 없음")
		} else {
			parts = append(parts, "key findings: none")
		}
	}
	if len(parts) == 0 {
		if korean {
			return "검토 내용: 주요 finding 없음."
		}
		return "Review content: no key findings."
	}
	if korean {
		return "검토 내용: " + strings.Join(parts, " | ") + "."
	}
	return "Review content: " + strings.Join(parts, " | ") + "."
}

func preWriteReviewProgressFindings(run ReviewRun) []ReviewFinding {
	ids := append(append([]string(nil), run.Gate.BlockingFindings...), run.Gate.WarningFindings...)
	if len(ids) > 0 {
		return reviewProgressFindingsByID(run, ids, 3)
	}
	if info := reviewCLIInfoFindings(run); len(info) > 0 {
		return limitReviewFindings(info, 3)
	}
	return limitReviewFindings(run.Findings, 3)
}

func preWriteReviewReportProgressSuffix(cfg Config, run ReviewRun) string {
	if len(run.ArtifactRefs) == 0 {
		return ""
	}
	if reviewRunPrefersKorean(cfg, run) {
		return " 보고서: " + run.ArtifactRefs[0]
	}
	return " report: " + run.ArtifactRefs[0]
}

func formatPreWriteFinalVisibleReviewSummary(cfg Config, run ReviewRun, proceedToPreview bool) string {
	korean := reviewRunPrefersKorean(cfg, run)
	verdict := firstNonBlankString(run.Gate.Verdict, run.Result.Verdict, "unknown")
	var b strings.Builder
	if korean {
		b.WriteString("최종 검토 결과:")
		fmt.Fprintf(&b, "\n- 판정: %s", verdict)
		fmt.Fprintf(&b, "\n- 차단: %d개", len(run.Gate.BlockingFindings))
		fmt.Fprintf(&b, "\n- 경고: %d개", len(run.Gate.WarningFindings))
		if proceedToPreview {
			b.WriteString("\n- 진행: diff preview로 진행합니다.")
		} else {
			b.WriteString("\n- 진행: diff preview로 진행하지 않습니다.")
		}
		if strings.TrimSpace(run.Result.Summary) != "" {
			fmt.Fprintf(&b, "\n- 요약: %s", compactPromptSection(run.Result.Summary, 260))
		}
	} else {
		b.WriteString("Final review result:")
		fmt.Fprintf(&b, "\n- Verdict: %s", verdict)
		fmt.Fprintf(&b, "\n- Blockers: %d", len(run.Gate.BlockingFindings))
		fmt.Fprintf(&b, "\n- Warnings: %d", len(run.Gate.WarningFindings))
		if proceedToPreview {
			b.WriteString("\n- Next: proceed to diff preview.")
		} else {
			b.WriteString("\n- Next: do not proceed to diff preview.")
		}
		if strings.TrimSpace(run.Result.Summary) != "" {
			fmt.Fprintf(&b, "\n- Summary: %s", compactPromptSection(run.Result.Summary, 260))
		}
	}
	if len(run.RepairFindings) > 0 {
		if korean {
			b.WriteString("\n\n수정 확인 대상:")
		} else {
			b.WriteString("\n\nRepair targets checked:")
		}
		for _, finding := range limitReviewFindings(run.RepairFindings, 8) {
			writePreWriteVisibleRepairTarget(&b, finding, korean)
		}
	}
	if korean {
		if len(run.RepairFindings) > 0 {
			b.WriteString("\n\n남은 검토 항목:")
		} else {
			b.WriteString("\n\n검토 항목:")
		}
	} else {
		if len(run.RepairFindings) > 0 {
			b.WriteString("\n\nRemaining review items:")
		} else {
			b.WriteString("\n\nReview items:")
		}
	}
	findings := preWriteReviewProgressFindings(run)
	if len(findings) == 0 {
		if korean {
			b.WriteString("\n- 주요 finding 없음.")
		} else {
			b.WriteString("\n- No key findings.")
		}
	} else {
		for _, finding := range limitReviewFindings(findings, 6) {
			writePreWriteVisibleFinding(&b, finding, korean)
		}
	}
	if len(run.ArtifactRefs) > 0 {
		if korean {
			fmt.Fprintf(&b, "\n\n리뷰 보고서: %s", run.ArtifactRefs[0])
		} else {
			fmt.Fprintf(&b, "\n\nReview report: %s", run.ArtifactRefs[0])
		}
	}
	return strings.TrimSpace(b.String())
}

func writePreWriteVisibleRepairTarget(b *strings.Builder, finding ReviewFinding, korean bool) {
	finding.Normalize()
	id := valueOrDefault(finding.ID, "RF")
	severity := valueOrDefault(finding.Severity, "unknown")
	category := valueOrDefault(finding.Category, "general")
	title := compactPromptSection(firstNonBlankString(finding.Title, finding.Evidence, finding.Impact, "Review finding"), 220)
	fmt.Fprintf(b, "\n- %s [%s/%s]: %s", id, severity, category, title)
	if strings.TrimSpace(finding.Path) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 코드 위치: %s", compactPromptSection(finding.Path, 220))
		} else {
			fmt.Fprintf(b, "\n  - Code location: %s", compactPromptSection(finding.Path, 220))
		}
	}
	if strings.TrimSpace(finding.Symbol) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 심볼: %s", compactPromptSection(finding.Symbol, 180))
		} else {
			fmt.Fprintf(b, "\n  - Symbol: %s", compactPromptSection(finding.Symbol, 180))
		}
	}
	if strings.TrimSpace(finding.Evidence) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 문제: %s", compactPromptSection(finding.Evidence, 300))
		} else {
			fmt.Fprintf(b, "\n  - Problem: %s", compactPromptSection(finding.Evidence, 300))
		}
	}
	if strings.TrimSpace(finding.Impact) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 영향: %s", compactPromptSection(finding.Impact, 240))
		} else {
			fmt.Fprintf(b, "\n  - Impact: %s", compactPromptSection(finding.Impact, 240))
		}
	}
	if strings.TrimSpace(finding.RequiredFix) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 수정 기준: %s", compactPromptSection(finding.RequiredFix, 300))
		} else {
			fmt.Fprintf(b, "\n  - Required fix: %s", compactPromptSection(finding.RequiredFix, 300))
		}
	}
	if strings.TrimSpace(finding.TestRecommendation) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 확인 방법: %s", compactPromptSection(finding.TestRecommendation, 240))
		} else {
			fmt.Fprintf(b, "\n  - Verification: %s", compactPromptSection(finding.TestRecommendation, 240))
		}
	}
}

func writePreWriteVisibleFinding(b *strings.Builder, finding ReviewFinding, korean bool) {
	finding.Normalize()
	id := valueOrDefault(finding.ID, "RF")
	severity := valueOrDefault(finding.Severity, "unknown")
	category := valueOrDefault(finding.Category, "general")
	title := compactPromptSection(firstNonBlankString(finding.Title, finding.Evidence, finding.Impact, "Review finding"), 220)
	fmt.Fprintf(b, "\n- %s [%s/%s]: %s", id, severity, category, title)
	if strings.TrimSpace(finding.Path) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 경로: %s", compactPromptSection(finding.Path, 220))
		} else {
			fmt.Fprintf(b, "\n  - Path: %s", compactPromptSection(finding.Path, 220))
		}
	}
	if strings.TrimSpace(finding.Evidence) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 근거: %s", compactPromptSection(finding.Evidence, 260))
		} else {
			fmt.Fprintf(b, "\n  - Evidence: %s", compactPromptSection(finding.Evidence, 260))
		}
	}
	if strings.TrimSpace(finding.Impact) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 영향: %s", compactPromptSection(finding.Impact, 220))
		} else {
			fmt.Fprintf(b, "\n  - Impact: %s", compactPromptSection(finding.Impact, 220))
		}
	}
	if strings.TrimSpace(finding.RequiredFix) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 조치: %s", compactPromptSection(finding.RequiredFix, 260))
		} else {
			fmt.Fprintf(b, "\n  - Fix: %s", compactPromptSection(finding.RequiredFix, 260))
		}
	}
	if strings.TrimSpace(finding.TestRecommendation) != "" {
		if korean {
			fmt.Fprintf(b, "\n  - 테스트: %s", compactPromptSection(finding.TestRecommendation, 220))
		} else {
			fmt.Fprintf(b, "\n  - Test: %s", compactPromptSection(finding.TestRecommendation, 220))
		}
	}
}

func limitReviewFindings(findings []ReviewFinding, limit int) []ReviewFinding {
	if limit <= 0 || len(findings) <= limit {
		return findings
	}
	return findings[:limit]
}
