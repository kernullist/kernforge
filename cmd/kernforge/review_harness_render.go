package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

func renderReviewRunMarkdown(run ReviewRun) string {
	var b strings.Builder
	b.WriteString("# KernForge Review\n\n")
	fmt.Fprintf(&b, "- Review ID: `%s`\n", run.ID)
	fmt.Fprintf(&b, "- Schema: `%s`\n", run.SchemaVersion)
	fmt.Fprintf(&b, "- Target: `%s`\n", run.Target)
	fmt.Fprintf(&b, "- Mode: `%s`\n", run.Mode)
	fmt.Fprintf(&b, "- Flow: `%s`\n", run.Flow)
	fmt.Fprintf(&b, "- Verdict: `%s`\n", valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
	if strings.TrimSpace(run.Gate.Action) != "" {
		fmt.Fprintf(&b, "- Gate action: `%s`\n", run.Gate.Action)
	}
	fmt.Fprintf(&b, "- Machine status: `%s` exit=%d\n", run.MachineStatus, run.ExitCode)
	fmt.Fprintf(&b, "- Workspace: `%s`\n", filepath.ToSlash(run.Workspace))
	if strings.TrimSpace(run.Branch) != "" {
		fmt.Fprintf(&b, "- Branch: `%s`\n", run.Branch)
	}
	if strings.TrimSpace(run.Objective) != "" {
		fmt.Fprintf(&b, "- Objective: %s\n", run.Objective)
	}
	if run.Freshness.Stale {
		fmt.Fprintf(&b, "- Freshness: stale (%s)\n", run.Freshness.StaleReason)
	}
	if run.Redaction.Redacted {
		fmt.Fprintf(&b, "- Redaction: %s\n", strings.Join(run.Redaction.Patterns, ", "))
	}
	if run.SingleModelPolicy.Enabled {
		fmt.Fprintf(&b, "- Independence: `%s` (%s)\n", run.SingleModelPolicy.IndependenceLevel, run.SingleModelPolicy.NoCrossReviewReason)
	}
	if second := buildReviewSecondPassObservability(run); second != nil {
		fmt.Fprintf(&b, "- Single-model second pass: `%s` ran=`%t` cache_hit=`%t`", second.Status, second.Ran, second.CacheHit)
		if strings.TrimSpace(second.ModelRoute) != "" {
			fmt.Fprintf(&b, " route=`%s`", second.ModelRoute)
		}
		if second.FindingCount > 0 {
			fmt.Fprintf(&b, " findings=`%d`", second.FindingCount)
		}
		if strings.TrimSpace(second.SkippedReason) != "" {
			fmt.Fprintf(&b, " reason=%s", second.SkippedReason)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n## Summary\n\n")
	b.WriteString(valueOrDefault(run.Result.Summary, run.Gate.Reason))
	b.WriteString("\n\n")
	if second := buildReviewSecondPassObservability(run); second != nil {
		b.WriteString("## Single-Model Second Pass\n\n")
		fmt.Fprintf(&b, "- status: `%s`\n", second.Status)
		fmt.Fprintf(&b, "- ran: `%t`\n", second.Ran)
		fmt.Fprintf(&b, "- cache_hit: `%t`\n", second.CacheHit)
		if strings.TrimSpace(second.ModelRoute) != "" {
			fmt.Fprintf(&b, "- model_route: `%s`\n", second.ModelRoute)
		}
		if len(second.ReviewedPaths) > 0 {
			fmt.Fprintf(&b, "- reviewed_paths: `%s`\n", strings.Join(second.ReviewedPaths, "`, `"))
		}
		fmt.Fprintf(&b, "- finding_count: `%d`\n", second.FindingCount)
		if strings.TrimSpace(second.PromptRef) != "" {
			fmt.Fprintf(&b, "- prompt_ref: `%s`\n", second.PromptRef)
		}
		if strings.TrimSpace(second.RawOutputRef) != "" {
			fmt.Fprintf(&b, "- raw_output_ref: `%s`\n", second.RawOutputRef)
		}
		if strings.TrimSpace(second.SkippedReason) != "" {
			fmt.Fprintf(&b, "- skipped_reason: %s\n", second.SkippedReason)
		}
		b.WriteString("\n")
	}
	if len(run.ObligationLedger.Items) > 0 {
		b.WriteString("## Obligation Ledger\n\n")
		fmt.Fprintf(&b, "- total: `%d` open: `%d`\n", run.ObligationLedger.TotalCount, run.ObligationLedger.OpenCount)
		if len(run.ObligationLedger.Summary) > 0 {
			fmt.Fprintf(&b, "- open_by_type: `%s`\n", strings.Join(run.ObligationLedger.Summary, ", "))
		}
		for _, obligation := range run.ObligationLedger.Items {
			fmt.Fprintf(&b, "- `%s` type=`%s` status=`%s` blocking=`%t`: %s\n", obligation.ID, obligation.Type, obligation.Status, obligation.Blocking, obligation.Title)
			if strings.TrimSpace(obligation.RequiredAction) != "" {
				fmt.Fprintf(&b, "  - Action: %s\n", obligation.RequiredAction)
			}
		}
		b.WriteString("\n")
	}
	if triage := normalizedCrossReviewTriageLedger(run.CrossReviewTriage); triage != nil && len(triage.Items) > 0 {
		b.WriteString("## Cross-Review Triage Ledger\n\n")
		fmt.Fprintf(&b, "- total: `%d` incomplete: `%d`\n", triage.TotalCount, triage.IncompleteCount)
		if len(triage.StatusCounts) > 0 {
			parts := make([]string, 0, len(triage.StatusCounts))
			for _, status := range []string{
				crossReviewTriageAcceptedFixed,
				crossReviewTriageAcceptedDeferred,
				crossReviewTriageRejectedWithReason,
				crossReviewTriageNeedsUserDecision,
			} {
				if count := triage.StatusCounts[status]; count > 0 {
					parts = append(parts, fmt.Sprintf("%s=%d", status, count))
				}
			}
			if len(parts) > 0 {
				fmt.Fprintf(&b, "- status_counts: `%s`\n", strings.Join(parts, ", "))
			}
		}
		for _, item := range triage.Items {
			fmt.Fprintf(&b, "\n### `%s` - %s\n\n", valueOrDefault(item.FindingID, "cross-review-finding"), valueOrDefault(item.Title, "untitled finding"))
			fmt.Fprintf(&b, "- status: `%s`\n", item.TriageStatus)
			fmt.Fprintf(&b, "- reviewer: `%s`\n", item.ReviewerRole)
			fmt.Fprintf(&b, "- severity: `%s`\n", item.Severity)
			fmt.Fprintf(&b, "- category: `%s`\n", item.Category)
			if strings.TrimSpace(item.Path) != "" {
				if item.Line > 0 {
					fmt.Fprintf(&b, "- location: `%s:%d`\n", filepath.ToSlash(item.Path), item.Line)
				} else {
					fmt.Fprintf(&b, "- location: `%s`\n", filepath.ToSlash(item.Path))
				}
			}
			if strings.TrimSpace(item.Symbol) != "" {
				fmt.Fprintf(&b, "- symbol: `%s`\n", item.Symbol)
			}
			if strings.TrimSpace(item.TechnicalReason) != "" {
				fmt.Fprintf(&b, "- reason: %s\n", item.TechnicalReason)
			}
			if strings.TrimSpace(item.RequiredFix) != "" {
				fmt.Fprintf(&b, "- required_fix: %s\n", item.RequiredFix)
			}
			if len(item.FixRefs) > 0 {
				fmt.Fprintf(&b, "- fix_refs: `%s`\n", strings.Join(item.FixRefs, "`, `"))
			}
			if len(item.ChangedPaths) > 0 {
				fmt.Fprintf(&b, "- changed_paths: `%s`\n", strings.Join(item.ChangedPaths, "`, `"))
			}
			if len(item.VerificationRefs) > 0 {
				fmt.Fprintf(&b, "- verification_refs: `%s`\n", strings.Join(item.VerificationRefs, "`, `"))
			}
			if len(item.EvidenceRefs) > 0 {
				fmt.Fprintf(&b, "- evidence_refs: `%s`\n", strings.Join(item.EvidenceRefs, "`, `"))
			}
			fmt.Fprintf(&b, "- user_action_needed: `%t`\n", item.UserActionNeeded)
			if strings.TrimSpace(item.UserActionPrompt) != "" {
				fmt.Fprintf(&b, "- user_action_prompt: %s\n", item.UserActionPrompt)
			}
		}
		if len(triage.Blockers) > 0 {
			fmt.Fprintf(&b, "- blockers: %s\n", strings.Join(triage.Blockers, " | "))
		}
		b.WriteString("\n")
	}
	if len(run.Gate.BlockingFindings) > 0 {
		b.WriteString("## Blocking Findings\n\n")
		for _, finding := range run.Findings {
			if reviewFindingBlocksGate(run, finding) {
				renderReviewFindingMarkdown(&b, finding)
			}
		}
	}
	if len(run.Gate.WarningFindings) > 0 {
		b.WriteString("## Warnings\n\n")
		for _, finding := range run.Findings {
			if !reviewFindingBlocksGate(run, finding) && reviewFindingCountsAsWarning(finding) {
				renderReviewFindingMarkdown(&b, finding)
			}
		}
	}
	if len(run.Findings) > 0 {
		b.WriteString("## All Findings\n\n")
		for _, finding := range run.Findings {
			fmt.Fprintf(&b, "- `%s` `%s` `%s`: %s\n", finding.ID, finding.Severity, finding.Category, finding.Title)
		}
		b.WriteString("\n")
	}
	if len(run.Gate.RequiredActions) > 0 {
		b.WriteString("## Required Actions\n\n")
		for _, action := range run.Gate.RequiredActions {
			if strings.TrimSpace(action) != "" {
				fmt.Fprintf(&b, "- %s\n", action)
			}
		}
		b.WriteString("\n")
	}
	if run.RepairPlan.Required {
		b.WriteString("## Repair Prompt\n\n")
		b.WriteString("```text\n")
		b.WriteString(run.RepairPlan.Prompt)
		b.WriteString("\n```\n\n")
	}
	if len(run.Gate.NextCommands) > 0 {
		b.WriteString("## Next Commands\n\n")
		for _, cmd := range run.Gate.NextCommands {
			fmt.Fprintf(&b, "- `%s`\n", cmd.Command)
			if strings.TrimSpace(cmd.Reason) != "" {
				fmt.Fprintf(&b, "  - Why: %s\n", cmd.Reason)
			}
			if strings.TrimSpace(cmd.When) != "" {
				fmt.Fprintf(&b, "  - When: %s\n", cmd.When)
			}
			if strings.TrimSpace(cmd.Safety) != "" {
				fmt.Fprintf(&b, "  - Safety: `%s`\n", cmd.Safety)
			}
			fmt.Fprintf(&b, "  - Auto run: `%t`\n", cmd.AutoRun)
			fmt.Fprintf(&b, "  - Requires confirmation: `%t`\n", cmd.RequiresConfirmation)
			if strings.TrimSpace(cmd.ClientHint) != "" {
				fmt.Fprintf(&b, "  - Action: %s\n", cmd.ClientHint)
			}
			if strings.TrimSpace(cmd.ExpectedResult) != "" {
				fmt.Fprintf(&b, "  - Expected result: %s\n", cmd.ExpectedResult)
			}
		}
		b.WriteString("\n")
	}
	if len(run.StateTransitions) > 0 {
		b.WriteString("## State Transitions\n\n")
		for _, transition := range run.StateTransitions {
			fmt.Fprintf(&b, "- `%s` `%s` -> `%s` actor=`%s` blocking=`%t`: %s\n", transition.ID, transition.From, transition.To, transition.Actor, transition.Blocking, transition.Reason)
		}
		b.WriteString("\n")
	}
	if len(run.ActionEnvelopes) > 0 {
		b.WriteString("## Action Envelopes\n\n")
		for _, envelope := range run.ActionEnvelopes {
			fmt.Fprintf(&b, "- `%s` `%s` actor=`%s` status=`%s` approval_required=`%t` approval_granted=`%t`", envelope.ActionID, envelope.ActionType, envelope.Actor, envelope.Status, envelope.ApprovalRequired, envelope.ApprovalGranted)
			if strings.TrimSpace(envelope.FailureClass) != "" {
				fmt.Fprintf(&b, " failure=`%s`", envelope.FailureClass)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if run.ApprovalLedger.ReviewGateApproved || len(run.ApprovalLedger.MissingApprovals) > 0 || strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		b.WriteString("## Approval Ledger\n\n")
		fmt.Fprintf(&b, "- review_gate_approved: `%t`\n", run.ApprovalLedger.ReviewGateApproved)
		fmt.Fprintf(&b, "- diff_preview_shown: `%t`\n", run.ApprovalLedger.DiffPreviewShown)
		fmt.Fprintf(&b, "- user_write_approved: `%t`\n", run.ApprovalLedger.UserWriteApproved)
		fmt.Fprintf(&b, "- write_applied: `%t`\n", run.ApprovalLedger.WriteApplied)
		fmt.Fprintf(&b, "- verification_passed: `%t`\n", run.ApprovalLedger.VerificationPassed)
		if len(run.ApprovalLedger.MissingApprovals) > 0 {
			fmt.Fprintf(&b, "- missing_approvals: `%s`\n", strings.Join(run.ApprovalLedger.MissingApprovals, ", "))
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(run.CapabilityManifest.LocalFileRead) != "" {
		b.WriteString("## Capability Manifest\n\n")
		fmt.Fprintf(&b, "- local_file_read: `%s`\n", run.CapabilityManifest.LocalFileRead)
		fmt.Fprintf(&b, "- patch_apply: `%s`\n", run.CapabilityManifest.PatchApply)
		fmt.Fprintf(&b, "- diff_preview: `%s`\n", run.CapabilityManifest.DiffPreview)
		fmt.Fprintf(&b, "- test_runner: `%s`\n", run.CapabilityManifest.TestRunner)
		fmt.Fprintf(&b, "- web_search: `%s`\n", run.CapabilityManifest.WebSearch)
		fmt.Fprintf(&b, "- primary_model: `%s`\n", run.CapabilityManifest.PrimaryModel)
		fmt.Fprintf(&b, "- cross_review_model: `%s`\n", run.CapabilityManifest.CrossReviewModel)
		fmt.Fprintf(&b, "- single_model_review_mode: `%s`\n", run.CapabilityManifest.SingleModelReviewMode)
		b.WriteString("\n")
	}
	if len(run.ExternalLookupIntents) > 0 {
		b.WriteString("## External Lookup Intents\n\n")
		for _, intent := range run.ExternalLookupIntents {
			fmt.Fprintf(&b, "- `%s` tool=`%s` status=`%s` blocked=`%t`: %s\n", intent.ID, intent.ToolName, intent.Status, intent.Blocked, intent.Intent)
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(run.ArtifactIntegrity.EvidenceHash) != "" || strings.TrimSpace(run.ArtifactIntegrity.ProposalHash) != "" {
		b.WriteString("## Artifact Integrity\n\n")
		fmt.Fprintf(&b, "- hash_algorithm: `%s`\n", valueOrDefault(run.ArtifactIntegrity.HashAlgorithm, "sha256"))
		if strings.TrimSpace(run.ArtifactIntegrity.EvidenceHash) != "" {
			fmt.Fprintf(&b, "- evidence_hash: `%s`\n", run.ArtifactIntegrity.EvidenceHash)
		}
		if strings.TrimSpace(run.ArtifactIntegrity.ProposalHash) != "" {
			fmt.Fprintf(&b, "- proposal_hash: `%s`\n", run.ArtifactIntegrity.ProposalHash)
		}
		if len(run.ArtifactIntegrity.CurrentFileHashes) > 0 {
			fmt.Fprintf(&b, "- current_file_hashes: `%d`\n", len(run.ArtifactIntegrity.CurrentFileHashes))
		}
		if len(run.ArtifactIntegrity.Warnings) > 0 {
			fmt.Fprintf(&b, "- warnings: %s\n", strings.Join(run.ArtifactIntegrity.Warnings, " | "))
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(run.LedgerConsistency.Status) != "" {
		b.WriteString("## Ledger Consistency\n\n")
		fmt.Fprintf(&b, "- status: `%s`\n", run.LedgerConsistency.Status)
		if len(run.LedgerConsistency.Blockers) > 0 {
			fmt.Fprintf(&b, "- blockers: %s\n", strings.Join(run.LedgerConsistency.Blockers, " | "))
		}
		if len(run.LedgerConsistency.Warnings) > 0 {
			fmt.Fprintf(&b, "- warnings: %s\n", strings.Join(run.LedgerConsistency.Warnings, " | "))
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(run.ResumeSanity.Status) != "" {
		b.WriteString("## Resume Sanity\n\n")
		fmt.Fprintf(&b, "- status: `%s`\n", run.ResumeSanity.Status)
		if strings.TrimSpace(run.ResumeSanity.LastStableAction) != "" {
			fmt.Fprintf(&b, "- last_stable_action: `%s`\n", run.ResumeSanity.LastStableAction)
		}
		if strings.TrimSpace(run.ResumeSanity.NextState) != "" {
			fmt.Fprintf(&b, "- next_state: `%s`\n", run.ResumeSanity.NextState)
		}
		if strings.TrimSpace(run.ResumeSanity.ConflictReason) != "" {
			fmt.Fprintf(&b, "- conflict: %s\n", run.ResumeSanity.ConflictReason)
		}
		b.WriteString("\n")
	}
	if len(run.ModelPlan.CapabilityProfiles) > 0 || len(run.ModelPlan.RouteHealth) > 0 {
		b.WriteString("## Model Route Capability\n\n")
		for _, profile := range run.ModelPlan.CapabilityProfiles {
			fmt.Fprintf(&b, "- `%s` provider=`%s` model=`%s` rank=`%d` schema=`%s` latency=`%s` timeout_ms=`%d`\n", profile.Role, profile.Provider, profile.ModelPattern, profile.CapabilityRank, profile.SchemaReliability, profile.LatencyClass, profile.RecommendedTimeoutMS)
		}
		for _, health := range run.ModelPlan.RouteHealth {
			fmt.Fprintf(&b, "- health `%s` model=`%s` status=`%s` quality=`%s` timeout_rate=`%.2f` weak_rate=`%.2f`: %s\n", health.Role, health.Model, health.LastStatus, health.LastQuality, health.TimeoutRate, health.WeakRate, health.Recommendation)
		}
		b.WriteString("\n")
	}
	if len(run.ModelPlan.RequiredLenses) > 0 || len(run.ModelPlan.OptionalLenses) > 0 {
		b.WriteString("## Review Lenses\n\n")
		if len(run.ModelPlan.RequiredLenses) > 0 {
			fmt.Fprintf(&b, "- required: `%s`\n", strings.Join(run.ModelPlan.RequiredLenses, "`, `"))
		}
		if len(run.ModelPlan.OptionalLenses) > 0 {
			fmt.Fprintf(&b, "- optional: `%s`\n", strings.Join(run.ModelPlan.OptionalLenses, "`, `"))
		}
		b.WriteString("\n")
	}
	if rendered := strings.TrimSpace(run.RuntimeGateLedger.RenderPromptSection()); rendered != "" {
		b.WriteString("## Runtime Gate Ledger\n\n")
		b.WriteString(rendered)
		b.WriteString("\n\n")
	}
	if len(run.ModelPlan.UserGuidance) > 0 {
		b.WriteString("## Model Guidance\n\n")
		for _, item := range run.ModelPlan.UserGuidance {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	if len(run.ChangeSet.ChangedPaths) > 0 {
		b.WriteString("## Changed Paths\n\n")
		for _, path := range limitStrings(run.ChangeSet.ChangedPaths, 64) {
			fmt.Fprintf(&b, "- `%s`\n", filepath.ToSlash(path))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func renderReviewFindingMarkdown(b *strings.Builder, finding ReviewFinding) {
	fmt.Fprintf(b, "### %s `%s` %s\n\n", finding.ID, finding.Severity, finding.Title)
	if strings.TrimSpace(finding.Path) != "" {
		fmt.Fprintf(b, "- Path: `%s`\n", filepath.ToSlash(finding.Path))
	}
	if strings.TrimSpace(finding.Symbol) != "" {
		fmt.Fprintf(b, "- Symbol: `%s`\n", finding.Symbol)
	}
	fmt.Fprintf(b, "- Category: `%s`\n", finding.Category)
	if strings.TrimSpace(finding.Evidence) != "" {
		fmt.Fprintf(b, "- Evidence: %s\n", finding.Evidence)
	}
	if strings.TrimSpace(finding.Impact) != "" {
		fmt.Fprintf(b, "- Impact: %s\n", finding.Impact)
	}
	if strings.TrimSpace(finding.RequiredFix) != "" {
		fmt.Fprintf(b, "- Required fix: %s\n", finding.RequiredFix)
	}
	if strings.TrimSpace(finding.TestRecommendation) != "" {
		fmt.Fprintf(b, "- Test: `%s`\n", finding.TestRecommendation)
	}
	if len(finding.FixRefs) > 0 {
		fmt.Fprintf(b, "- Fix refs: `%s`\n", strings.Join(finding.FixRefs, "`, `"))
	}
	if len(finding.VerificationRefs) > 0 {
		fmt.Fprintf(b, "- Verification refs: `%s`\n", strings.Join(finding.VerificationRefs, "`, `"))
	}
	b.WriteString("\n")
}

func renderReviewEvidenceMarkdown(run ReviewRun) string {
	var b strings.Builder
	b.WriteString("# KernForge Review Evidence\n\n")
	fmt.Fprintf(&b, "- Review ID: `%s`\n", run.ID)
	fmt.Fprintf(&b, "- Fingerprint: `%s`\n", run.ReviewFingerprint)
	if len(run.Evidence.Sources) > 0 {
		fmt.Fprintf(&b, "- Sources: %s\n", strings.Join(run.Evidence.Sources, ", "))
	}
	if len(run.Evidence.Warnings) > 0 {
		b.WriteString("\n## Warnings\n\n")
		for _, warning := range run.Evidence.Warnings {
			fmt.Fprintf(&b, "- %s\n", warning)
		}
	}
	if strings.TrimSpace(run.Evidence.Text) != "" {
		b.WriteString("\n")
		b.WriteString(run.Evidence.Text)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func reviewRunPrefersKorean(cfg Config, run ReviewRun) bool {
	for _, text := range []string{
		run.RequestAnalysis.OriginalRequest,
		run.Objective,
	} {
		text = strings.TrimSpace(baseUserQueryText(text))
		if text == "" || looksLikeInternalReviewFeedbackUserMessage(text) {
			continue
		}
		language, _ := inferResponseLanguageForUserText(text, cfg)
		switch language {
		case "ko":
			return true
		case "en":
			return false
		}
	}
	language, _ := inferResponseLanguageForUserText("", cfg)
	return language == "ko"
}

func reviewRunLocalizedText(cfg Config, run ReviewRun, english string, korean string) string {
	if reviewRunPrefersKorean(cfg, run) {
		return korean
	}
	return english
}

func renderReviewCLIResult(cfg Config, run ReviewRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s\n", reviewRunLocalizedText(cfg, run, "Review", "리뷰"), run.ID, run.Gate.Verdict)
	fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Target", "대상"), run.Target)
	fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Mode", "모드"), run.Mode)
	if strings.TrimSpace(run.Gate.Action) != "" {
		fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Gate action", "게이트 액션"), run.Gate.Action)
	}
	noteCount := reviewCLINoteFindingCount(run)
	fmt.Fprintf(&b, "- %s: %d blocker=%d warning=%d", reviewRunLocalizedText(cfg, run, "Findings", "발견"), len(run.Findings), len(run.Gate.BlockingFindings), len(run.Gate.WarningFindings))
	if noteCount > 0 {
		fmt.Fprintf(&b, " note=%d", noteCount)
	}
	b.WriteString("\n")
	if len(run.ArtifactRefs) > 0 {
		fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Report", "보고서"), run.ArtifactRefs[0])
	}
	if routeStatus := renderReviewCLIRouteStatus(cfg, run); strings.TrimSpace(routeStatus) != "" {
		fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Reviewer route", "리뷰어 경로"), routeStatus)
	}
	rendered := map[string]bool{}
	for _, finding := range run.Findings {
		if reviewFindingBlocksGate(run, finding) {
			renderReviewCLIFinding(&b, cfg, run, finding, reviewRunLocalizedText(cfg, run, "Fix", "수정"))
			rendered[finding.ID] = true
		}
	}
	warnings := reviewCLIWarningFindings(run)
	if len(warnings) > 0 {
		fmt.Fprintf(&b, "\n%s:\n", reviewRunLocalizedText(cfg, run, "Warnings", "경고"))
		for _, finding := range warnings {
			if rendered[finding.ID] {
				continue
			}
			renderReviewCLIFinding(&b, cfg, run, finding, reviewRunLocalizedText(cfg, run, "Suggested fix", "권장 조치"))
			rendered[finding.ID] = true
		}
	}
	if len(run.Gate.BlockingFindings) == 0 && len(warnings) == 0 {
		infoFindings := reviewCLIInfoFindings(run)
		if len(infoFindings) > 0 {
			fmt.Fprintf(&b, "\n%s:\n", reviewRunLocalizedText(cfg, run, "Notes", "참고"))
			for _, finding := range infoFindings {
				if rendered[finding.ID] {
					continue
				}
				renderReviewCLIFinding(&b, cfg, run, finding, reviewRunLocalizedText(cfg, run, "Note", "참고"))
				rendered[finding.ID] = true
			}
		}
	}
	if line := renderReviewCLITriageResidualRisk(cfg, run); line != "" {
		fmt.Fprintf(&b, "\n%s\n", line)
	}
	if len(run.Gate.NextCommands) > 0 {
		fmt.Fprintf(&b, "\n%s:\n", reviewRunLocalizedText(cfg, run, "Next commands", "다음 명령"))
		for _, cmd := range run.Gate.NextCommands {
			renderReviewCLINextCommand(&b, cfg, run, cmd)
		}
	}
	return strings.TrimSpace(b.String())
}

func renderReviewCLITriageResidualRisk(cfg Config, run ReviewRun) string {
	ledger := normalizedCrossReviewTriageLedger(run.CrossReviewTriage)
	if ledger == nil || len(ledger.Items) == 0 {
		return ""
	}
	obs := buildReviewCrossReviewTriageSummary(ledger)
	if obs == nil {
		return ""
	}
	if obs.IncompleteCount == 0 && !obs.UserActionNeeded {
		deferred := 0
		for _, item := range ledger.Items {
			if normalizeCrossReviewTriageStatus(item.TriageStatus) == crossReviewTriageAcceptedDeferred {
				deferred++
			}
		}
		if deferred == 0 {
			return ""
		}
	}
	label := reviewRunLocalizedText(cfg, run, "Cross-review triage", "교차 리뷰 triage")
	line := "- " + label + ": " + reviewCrossReviewTriageStatusLine(obs)
	if obs.UserActionNeeded && len(obs.UserDecisionPrompts) > 0 {
		line += "\n  " + reviewRunLocalizedText(cfg, run, "Action", "실행 방법") + ": " + obs.UserDecisionPrompts[0]
	}
	return line
}

func renderReviewCLIFinding(b *strings.Builder, cfg Config, run ReviewRun, finding ReviewFinding, fixLabel string) {
	fmt.Fprintf(b, "\n[%s] %s: %s\n", finding.ID, finding.Severity, finding.Title)
	if strings.TrimSpace(finding.Evidence) != "" && !strings.EqualFold(strings.TrimSpace(finding.Evidence), strings.TrimSpace(finding.Title)) {
		fmt.Fprintf(b, "%s: %s\n", reviewRunLocalizedText(cfg, run, "Evidence", "근거"), finding.Evidence)
	}
	if strings.TrimSpace(finding.Impact) != "" {
		fmt.Fprintf(b, "%s: %s\n", reviewRunLocalizedText(cfg, run, "Impact", "영향"), finding.Impact)
	}
	if strings.TrimSpace(finding.RequiredFix) != "" {
		fmt.Fprintf(b, "%s: %s\n", fixLabel, finding.RequiredFix)
	}
	if strings.TrimSpace(finding.TestRecommendation) != "" {
		fmt.Fprintf(b, "%s: %s\n", reviewRunLocalizedText(cfg, run, "Test", "테스트"), finding.TestRecommendation)
	}
}

func renderReviewCLIRouteStatus(cfg Config, run ReviewRun) string {
	if run.SingleModelPolicy.Enabled {
		var status string
		if reviewRunPrefersKorean(cfg, run) {
			status = "single_model_mode - 별도 cross reviewer 없이 메인 모델 structured review와 deterministic gate를 사용합니다."
		} else {
			status = "single_model_mode - using main-model structured review plus deterministic gate without a separate cross reviewer."
		}
		if detail := renderReviewCLIReviewerRunDetails(run); detail != "" {
			status += "; " + detail
		}
		if second := buildReviewSecondPassObservability(run); second != nil {
			status += "; second_pass=" + reviewSecondPassStatusLine(second)
		}
		return status
	}
	if len(run.ReviewerRuns) == 0 {
		if reviewRunPrefersKorean(cfg, run) {
			return "deterministic_only - 모델 리뷰 없이 local deterministic gate만 사용합니다."
		}
		return "deterministic_only - using local deterministic gate without model review."
	}
	return renderReviewCLIReviewerRunDetails(run)
}

func renderReviewCLIReviewerRunDetails(run ReviewRun) string {
	var parts []string
	for _, reviewerRun := range run.ReviewerRuns {
		role := valueOrDefault(reviewRoleProgressName(reviewerRun.Role), reviewerRun.Role)
		kind := valueOrDefault(reviewerRun.Kind, "review")
		status := valueOrDefault(reviewerRun.Status, "unknown")
		quality := valueOrDefault(reviewerRun.ModelQuality, "unknown")
		detail := fmt.Sprintf("%s/%s=%s quality=%s", kind, role, status, quality)
		if strings.TrimSpace(reviewerRun.RawProviderResponsePath) != "" {
			detail += " provider_raw=" + reviewerRun.RawProviderResponsePath
		}
		parts = append(parts, detail)
	}
	return strings.Join(parts, "; ")
}

func renderReviewCLINextCommand(b *strings.Builder, cfg Config, run ReviewRun, cmd ReviewNextCommand) {
	fmt.Fprintf(b, "- %s\n", cmd.Command)
	if reason := reviewNextCommandReasonText(cfg, run, cmd); strings.TrimSpace(reason) != "" {
		fmt.Fprintf(b, "  %s: %s\n", reviewRunLocalizedText(cfg, run, "Why", "이유"), reason)
	}
	if when := reviewNextCommandWhenText(cfg, run, cmd); strings.TrimSpace(when) != "" {
		fmt.Fprintf(b, "  %s: %s\n", reviewRunLocalizedText(cfg, run, "When", "시점"), when)
	}
	if strings.TrimSpace(cmd.Safety) != "" {
		fmt.Fprintf(b, "  %s: %s\n", reviewRunLocalizedText(cfg, run, "Safety", "안전성"), cmd.Safety)
	}
	fmt.Fprintf(b, "  %s: %t\n", reviewRunLocalizedText(cfg, run, "Auto run", "자동 실행"), cmd.AutoRun)
	fmt.Fprintf(b, "  %s: %t\n", reviewRunLocalizedText(cfg, run, "Requires confirmation", "확인 필요"), cmd.RequiresConfirmation)
	if hint := reviewNextCommandHintText(cfg, run, cmd); strings.TrimSpace(hint) != "" {
		fmt.Fprintf(b, "  %s: %s\n", reviewRunLocalizedText(cfg, run, "Action", "실행 방법"), hint)
	}
	if expected := reviewNextCommandExpectedResultText(cfg, run, cmd); strings.TrimSpace(expected) != "" {
		fmt.Fprintf(b, "  %s: %s\n", reviewRunLocalizedText(cfg, run, "Expected result", "예상 결과"), expected)
	}
}

func reviewNextCommandReasonText(cfg Config, run ReviewRun, cmd ReviewNextCommand) string {
	if !reviewRunPrefersKorean(cfg, run) {
		return cmd.Reason
	}
	switch strings.TrimSpace(cmd.ID) {
	case "verify":
		return "변경된 파일에 대한 최신 빌드/테스트 근거가 없습니다."
	case "repair":
		if reviewRunLooksReadOnlyAnalysis(run) {
			return "차단 finding이 발견됐지만 현재 요청은 분석/검토이므로, 수정은 사용자가 원할 때만 이어갑니다."
		}
		return "차단 finding이 있어서 위 RF 항목을 기준으로 수정 작업을 이어가야 합니다."
	case "repair-warnings":
		if reviewRunLooksReadOnlyAnalysis(run) {
			return "분석 finding이 실제 코드 수정으로 이어질 수 있지만, 수정은 사용자가 원할 때만 이어갑니다."
		}
		return "경고 finding이 실제 코드 수정으로 이어질 수 있는 항목입니다."
	case "completion-audit":
		return "경고가 남아 있으므로 완료 선언 전에 최종 준비 상태를 점검해야 합니다."
	case "narrow-review":
		return "deterministic scope discovery가 리뷰 범위를 넓다고 판단했습니다."
	case "reviewer-fallback":
		return "필수 reviewer route가 실패했거나 약한 출력을 반환했습니다."
	case "set-cross-model":
		return "고위험 리뷰가 독립 cross reviewer 없이 single-model mode로 실행되었습니다."
	default:
		return cmd.Reason
	}
}

func reviewNextCommandWhenText(cfg Config, run ReviewRun, cmd ReviewNextCommand) string {
	if !reviewRunPrefersKorean(cfg, run) {
		return cmd.When
	}
	switch strings.TrimSpace(cmd.ID) {
	case "verify":
		return "완료 선언 또는 git write 전에"
	case "repair":
		if reviewRunLooksReadOnlyAnalysis(run) {
			return "분석 결과를 실제 코드 수정으로 이어가기로 결정한 경우"
		}
		return "리뷰 finding을 확인한 직후"
	case "repair-warnings":
		if reviewRunLooksReadOnlyAnalysis(run) {
			return "분석 결과를 실제 코드 수정으로 이어가기로 결정한 경우"
		}
		return "경고를 수용하지 않고 바로 수정하려는 경우"
	case "completion-audit":
		return "최종 답변 또는 완료 처리 전에"
	case "narrow-review":
		return "모델 finding을 완료 근거로 신뢰하기 전에"
	case "reviewer-fallback":
		return "편집을 재시도하거나 파일 쓰기를 승인하기 전에"
	case "set-cross-model":
		return "다음 보안/탐지 리뷰 전에"
	default:
		return cmd.When
	}
}

func reviewNextCommandHintText(cfg Config, run ReviewRun, cmd ReviewNextCommand) string {
	if !reviewRunPrefersKorean(cfg, run) {
		return cmd.ClientHint
	}
	switch strings.TrimSpace(cmd.ID) {
	case "verify":
		return "`/verify --full`로 검증을 실행한 뒤 `/review`를 다시 실행해 최신 근거를 붙이세요."
	case "repair":
		if reviewRunLooksReadOnlyAnalysis(run) {
			return "자연어로 `수정해줘`라고 이어가거나 이 명령을 실행하면 최신 리뷰 finding을 기준으로 repair 흐름을 시작합니다."
		}
		return "이 명령을 실행하거나 자연어로 `수정해줘`라고 이어가면 최신 리뷰 finding을 기준으로 repair 흐름을 시작합니다."
	case "repair-warnings":
		if reviewRunLooksReadOnlyAnalysis(run) {
			return "자연어로 `수정해줘`라고 이어가거나 이 명령을 실행한 경우에만 최신 분석 finding을 기준으로 repair 흐름을 시작합니다."
		}
		return "자연어로 `수정해줘`라고 이어가거나 이 명령을 실행하면 최신 warning finding을 기준으로 repair 흐름을 시작합니다."
	case "completion-audit":
		return "남은 경고를 수용 가능한 잔여 리스크로 볼 수 있는지 읽기 전용으로 점검합니다."
	case "narrow-review":
		return "path, symbol, selection 또는 검색 결과로 리뷰 범위를 좁힌 뒤 `/review`를 다시 실행하세요."
	case "reviewer-fallback":
		return "`/model cross-review status`로 route 상태를 확인하고, 모델을 바꾸거나 명시적으로 main-review fallback을 승인하세요."
	case "set-cross-model":
		return "`/model cross-review`에서 독립 cross reviewer route를 번호로 선택하세요. 보안/오탐 전문성은 review lens로 같은 프롬프트에 적용됩니다."
	default:
		return cmd.ClientHint
	}
}

func reviewNextCommandExpectedResultText(cfg Config, run ReviewRun, cmd ReviewNextCommand) string {
	if !reviewRunPrefersKorean(cfg, run) {
		return cmd.ExpectedResult
	}
	switch strings.TrimSpace(cmd.ID) {
	case "verify":
		return "변경된 파일에 대한 최신 verification report가 기록됩니다."
	case "repair":
		if reviewRunLooksReadOnlyAnalysis(run) {
			return "명시적으로 수정 요청을 이어간 경우에만 최신 리뷰 blocker가 repair guidance로 변환됩니다."
		}
		return "최신 리뷰 blocker가 다음 repair 턴의 직접 지시사항으로 변환됩니다."
	case "repair-warnings":
		if reviewRunLooksReadOnlyAnalysis(run) {
			return "명시적으로 수정 요청을 이어간 경우에만 최신 분석 finding이 repair guidance로 변환됩니다."
		}
		return "수정 가능한 warning finding이 repair guidance로 큐잉됩니다."
	case "completion-audit":
		return "남은 경고를 보존한 채 완료 준비 상태가 평가됩니다."
	case "narrow-review":
		return "구체적인 candidate file 또는 symbol을 가진 focused review run이 생성됩니다."
	case "reviewer-fallback":
		return "reviewer route 변경 또는 명시적 fallback 승인 전에는 파일 쓰기가 진행되지 않습니다."
	case "set-cross-model":
		return "다음 고위험 리뷰부터 독립 second-pass reviewer route를 사용할 수 있습니다."
	default:
		return cmd.ExpectedResult
	}
}

func reviewCLIWarningFindings(run ReviewRun) []ReviewFinding {
	warningIDs := reviewFindingIDSet(run.Gate.WarningFindings)
	var out []ReviewFinding
	for _, finding := range run.Findings {
		if len(warningIDs) > 0 {
			if warningIDs[finding.ID] {
				out = append(out, finding)
			}
			continue
		}
		if reviewFindingBlocksGate(run, finding) {
			continue
		}
		if strings.EqualFold(run.Gate.Verdict, reviewVerdictApprovedWithWarnings) &&
			reviewFindingCountsAsWarning(finding) {
			out = append(out, finding)
		}
	}
	return out
}

func reviewCLIInfoFindings(run ReviewRun) []ReviewFinding {
	var out []ReviewFinding
	for _, finding := range run.Findings {
		if reviewFindingBlocksGate(run, finding) || reviewFindingCountsAsWarning(finding) {
			continue
		}
		if strings.TrimSpace(finding.Title) == "" {
			continue
		}
		out = append(out, finding)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func reviewCLINoteFindingCount(run ReviewRun) int {
	warningIDs := reviewFindingIDSet(run.Gate.WarningFindings)
	count := 0
	for _, finding := range run.Findings {
		if reviewFindingBlocksGate(run, finding) {
			continue
		}
		if len(warningIDs) > 0 {
			if warningIDs[finding.ID] {
				continue
			}
		} else if reviewFindingCountsAsWarning(finding) {
			continue
		}
		if strings.TrimSpace(finding.Title) == "" {
			continue
		}
		count++
	}
	return count
}

func reviewFindingIDSet(ids []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			out[id] = true
		}
	}
	return out
}
