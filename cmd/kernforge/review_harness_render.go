package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

func renderReviewRunMarkdown(run ReviewRun) string {
	var b strings.Builder
	korean := reviewRunPrefersKoreanFromRequest(run)
	// diag accumulates every lifecycle / ledger / action-envelope / capability /
	// state-transition / sanity section. It is appended after the human-readable
	// outcome under a single trailing "## Diagnostics" ("## 진단 상세") heading so
	// the user sees the outcome first and the machine diagnostics last.
	var diag strings.Builder
	b.WriteString("# KernForge Review\n\n")
	fmt.Fprintf(&b, "- Review ID: `%s`\n", run.ID)
	if build := kernforgeBuildIdentitySummary(run.KernforgeBuild); strings.TrimSpace(build) != "" {
		fmt.Fprintf(&b, "- KernForge build: `%s`\n", build)
	} else if strings.TrimSpace(run.KernforgeVersion) != "" {
		fmt.Fprintf(&b, "- KernForge version: `%s`\n", run.KernforgeVersion)
	}
	// Lead with a plain-language verdict and the kind of change reviewed; the raw
	// enum tokens are preserved in the Diagnostics block and machine fields.
	fmt.Fprintf(&b, "- %s: %s\n",
		localizedReviewText(korean, "Verdict", "결과"),
		humanizeReviewVerdict(valueOrDefault(run.Gate.Verdict, run.Result.Verdict), korean))
	if strings.TrimSpace(run.Gate.Action) != "" {
		fmt.Fprintf(&b, "- %s: %s\n",
			localizedReviewText(korean, "Next step", "다음 단계"),
			humanizeGateAction(run.Gate.Action, korean))
	}
	fmt.Fprintf(&b, "- %s: %s\n",
		localizedReviewText(korean, "Reviewed", "검토 대상"),
		humanizeReviewTarget(run.Target, korean))
	if strings.TrimSpace(run.Mode) != "" {
		fmt.Fprintf(&b, "- %s: %s\n",
			localizedReviewText(korean, "Change kind", "변경 유형"),
			humanizeReviewMode(run.Mode, korean))
	}
	if class := normalizeReviewRequestClass(firstNonBlankString(run.RequestClass, run.RequestAnalysis.RequestClass)); class != "" && class != reviewRequestClassGeneral {
		fmt.Fprintf(&b, "- %s: %s\n",
			localizedReviewText(korean, "Request type", "요청 유형"),
			humanizeRequestClass(class, korean))
	}
	fmt.Fprintf(&b, "- %s: `%s`\n", localizedReviewText(korean, "Workspace", "작업 공간"), filepath.ToSlash(run.Workspace))
	if strings.TrimSpace(run.Branch) != "" {
		fmt.Fprintf(&b, "- %s: `%s`\n", localizedReviewText(korean, "Branch", "브랜치"), run.Branch)
	}
	if strings.TrimSpace(run.Objective) != "" {
		fmt.Fprintf(&b, "- %s: %s\n", localizedReviewText(korean, "Objective", "목표"), run.Objective)
	}
	if run.Freshness.Stale {
		if korean {
			fmt.Fprintf(&b, "- 최신성: 오래됨 (%s)\n", run.Freshness.StaleReason)
		} else {
			fmt.Fprintf(&b, "- Freshness: stale (%s)\n", run.Freshness.StaleReason)
		}
	}
	if run.Redaction.Redacted {
		fmt.Fprintf(&b, "- %s: %s\n", localizedReviewText(korean, "Redaction", "민감정보 마스킹"), strings.Join(run.Redaction.Patterns, ", "))
	}
	if skip := compactModelReviewSkipLine(run, korean); skip != "" {
		fmt.Fprintf(&b, "- %s: %s\n", localizedReviewText(korean, "Model review", "모델 리뷰"), skip)
	}

	// --- Diagnostics: schema/identity and classification internals ---
	fmt.Fprintf(&diag, "- schema: `%s`\n", run.SchemaVersion)
	fmt.Fprintf(&diag, "- flow: `%s`\n", run.Flow)
	fmt.Fprintf(&diag, "- target (raw): `%s`\n", run.Target)
	fmt.Fprintf(&diag, "- mode (raw): `%s`\n", run.Mode)
	fmt.Fprintf(&diag, "- verdict (raw): `%s`\n", valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
	if strings.TrimSpace(run.Gate.Action) != "" {
		fmt.Fprintf(&diag, "- gate_action (raw): `%s`\n", run.Gate.Action)
	}
	fmt.Fprintf(&diag, "- machine_status: `%s` exit=%d\n", run.MachineStatus, run.ExitCode)
	if class := normalizeReviewRequestClass(firstNonBlankString(run.RequestClass, run.RequestAnalysis.RequestClass)); class != "" && class != reviewRequestClassGeneral {
		fmt.Fprintf(&diag, "- request_class (raw): `%s`\n", class)
	}
	if kind := normalizeReviewLifecycleKind(firstNonBlankString(run.RequestAnalysis.LifecycleKind, reviewLifecycleKindForRun(&run))); kind != "" && kind != reviewLifecycleKindGeneral {
		fmt.Fprintf(&diag, "- lifecycle_kind (raw): `%s`\n", kind)
	}
	if strings.TrimSpace(run.RequestAnalysis.RequestClassReason) != "" {
		fmt.Fprintf(&diag, "- request_class_reason: %s\n", run.RequestAnalysis.RequestClassReason)
	}
	if run.RequestAnalysis.RequestClassConfidence > 0 {
		fmt.Fprintf(&diag, "- request_class_confidence: `%.2f`\n", run.RequestAnalysis.RequestClassConfidence)
	}
	if run.RequestAnalysis.RequestClassAmbiguous {
		fmt.Fprintf(&diag, "- request_class_ambiguity: `%s`\n", strings.Join(run.RequestAnalysis.AmbiguityWarnings, " | "))
	}
	if run.Lifecycle != nil {
		fmt.Fprintf(&diag, "- lifecycle_phase (raw): `%s`\n", run.Lifecycle.Phase)
		if strings.TrimSpace(run.Lifecycle.RouteMode) != "" {
			fmt.Fprintf(&diag, "- route_mode (raw): `%s`\n", run.Lifecycle.RouteMode)
		}
		if strings.TrimSpace(run.Lifecycle.RouteQuality) != "" {
			fmt.Fprintf(&diag, "- route_quality (raw): `%s`\n", run.Lifecycle.RouteQuality)
		}
	}
	if strings.TrimSpace(run.ModelReviewConsent) != "" {
		fmt.Fprintf(&diag, "- model_review_consent: `%s`", run.ModelReviewConsent)
		if strings.TrimSpace(run.ConsentSource) != "" {
			fmt.Fprintf(&diag, " source=`%s`", run.ConsentSource)
		}
		if strings.TrimSpace(run.SkipReason) != "" {
			fmt.Fprintf(&diag, " skip_reason=`%s`", run.SkipReason)
		}
		diag.WriteString("\n")
	}
	if strings.TrimSpace(run.OriginalMainProposalRef) != "" {
		fmt.Fprintf(&diag, "- original_main_proposal: `%s`\n", filepath.ToSlash(run.OriginalMainProposalRef))
	}
	if run.SingleModelPolicy.Enabled {
		fmt.Fprintf(&diag, "- independence: `%s` (%s)\n", run.SingleModelPolicy.IndependenceLevel, run.SingleModelPolicy.NoCrossReviewReason)
	}
	if second := buildReviewSecondPassObservability(run); second != nil {
		fmt.Fprintf(&diag, "- single_model_second_pass: `%s` ran=`%t` cache_hit=`%t`", second.Status, second.Ran, second.CacheHit)
		if strings.TrimSpace(second.Independence) != "" {
			fmt.Fprintf(&diag, " independence=`%s`", second.Independence)
		}
		if strings.TrimSpace(second.ModelRoute) != "" {
			fmt.Fprintf(&diag, " route=`%s`", second.ModelRoute)
		}
		if second.FindingCount > 0 {
			fmt.Fprintf(&diag, " findings=`%d`", second.FindingCount)
		}
		if strings.TrimSpace(second.SkippedReason) != "" {
			fmt.Fprintf(&diag, " reason=%s", second.SkippedReason)
		}
		diag.WriteString("\n")
	}
	b.WriteString("\n## Summary\n\n")
	b.WriteString(valueOrDefault(run.Result.Summary, run.Gate.Reason))
	b.WriteString("\n\n")
	if strings.TrimSpace(run.OriginalMainProposalRef) != "" || strings.TrimSpace(run.OriginalMainProposal) != "" {
		b.WriteString("## Original Main Proposal\n\n")
		if strings.TrimSpace(run.OriginalMainProposalRef) != "" {
			fmt.Fprintf(&b, "- ref: `%s`\n", filepath.ToSlash(run.OriginalMainProposalRef))
		}
		if strings.TrimSpace(run.OriginalMainProposal) != "" {
			b.WriteString("\n```text\n")
			b.WriteString(compactPromptSection(run.OriginalMainProposal, 2000))
			b.WriteString("\n```\n")
		}
		b.WriteString("\n")
	}
	if len(run.ChangeSet.ChangedPaths) > 0 {
		b.WriteString(reviewChangeSetPathSectionHeading(run))
		b.WriteString("\n\n")
		for _, path := range limitStrings(run.ChangeSet.ChangedPaths, 64) {
			fmt.Fprintf(&b, "- `%s`\n", filepath.ToSlash(path))
		}
		b.WriteString("\n")
	}
	if second := buildReviewSecondPassObservability(run); second != nil {
		diag.WriteString("## Single-Model Second Pass\n\n")
		fmt.Fprintf(&diag, "- status: `%s`\n", second.Status)
		fmt.Fprintf(&diag, "- ran: `%t`\n", second.Ran)
		fmt.Fprintf(&diag, "- cache_hit: `%t`\n", second.CacheHit)
		if strings.TrimSpace(second.ModelRoute) != "" {
			fmt.Fprintf(&diag, "- model_route: `%s`\n", second.ModelRoute)
		}
		if len(second.ReviewedPaths) > 0 {
			fmt.Fprintf(&diag, "- reviewed_paths: `%s`\n", strings.Join(second.ReviewedPaths, "`, `"))
		}
		fmt.Fprintf(&diag, "- finding_count: `%d`\n", second.FindingCount)
		if strings.TrimSpace(second.PromptRef) != "" {
			fmt.Fprintf(&diag, "- prompt_ref: `%s`\n", second.PromptRef)
		}
		if strings.TrimSpace(second.RawOutputRef) != "" {
			fmt.Fprintf(&diag, "- raw_output_ref: `%s`\n", second.RawOutputRef)
		}
		if strings.TrimSpace(second.SkippedReason) != "" {
			fmt.Fprintf(&diag, "- skipped_reason: %s\n", second.SkippedReason)
		}
		diag.WriteString("\n")
	}
	if run.Lifecycle != nil {
		diag.WriteString("## Request Lifecycle\n\n")
		fmt.Fprintf(&diag, "- request_class: `%s`\n", run.Lifecycle.RequestClass)
		if strings.TrimSpace(run.Lifecycle.LifecycleKind) != "" {
			fmt.Fprintf(&diag, "- lifecycle_kind: `%s`\n", run.Lifecycle.LifecycleKind)
		}
		if run.Lifecycle.MixedFlow {
			fmt.Fprintf(&diag, "- mixed_flow: `%t`\n", run.Lifecycle.MixedFlow)
		}
		if len(run.Lifecycle.SecondaryRequestClasses) > 0 {
			fmt.Fprintf(&diag, "- secondary_request_classes: `%s`\n", strings.Join(run.Lifecycle.SecondaryRequestClasses, "`, `"))
		}
		fmt.Fprintf(&diag, "- phase: `%s`\n", run.Lifecycle.Phase)
		if strings.TrimSpace(run.Lifecycle.RouteMode) != "" {
			fmt.Fprintf(&diag, "- route_mode: `%s`\n", run.Lifecycle.RouteMode)
		}
		if strings.TrimSpace(run.Lifecycle.Reason) != "" {
			fmt.Fprintf(&diag, "- reason: %s\n", run.Lifecycle.Reason)
		}
		if run.Lifecycle.ClassificationConfidence > 0 {
			fmt.Fprintf(&diag, "- classification_confidence: `%.2f`\n", run.Lifecycle.ClassificationConfidence)
		}
		fmt.Fprintf(&diag, "- classification_ambiguous: `%t`\n", run.Lifecycle.ClassificationAmbiguous)
		if len(run.Lifecycle.AmbiguityWarnings) > 0 {
			fmt.Fprintf(&diag, "- ambiguity_warnings: `%s`\n", strings.Join(run.Lifecycle.AmbiguityWarnings, "`, `"))
		}
		if run.Lifecycle.Contract != nil && len(run.Lifecycle.Contract.FinalAnswerRequirements) > 0 {
			fmt.Fprintf(&diag, "- final_answer_contract: `%s`\n", strings.Join(run.Lifecycle.Contract.FinalAnswerRequirements, "`, `"))
		}
		if strings.TrimSpace(run.Lifecycle.RouteQuality) != "" {
			fmt.Fprintf(&diag, "- route_quality: `%s`\n", run.Lifecycle.RouteQuality)
		}
		if len(run.Lifecycle.RouteDegradedReasons) > 0 {
			fmt.Fprintf(&diag, "- route_degraded_reasons: `%s`\n", strings.Join(run.Lifecycle.RouteDegradedReasons, "`, `"))
		}
		if strings.TrimSpace(run.Lifecycle.ReviewGateStatus) != "" {
			fmt.Fprintf(&diag, "- review_gate: `%s`\n", run.Lifecycle.ReviewGateStatus)
		}
		if strings.TrimSpace(run.Lifecycle.RepairGateStatus) != "" {
			fmt.Fprintf(&diag, "- repair_gate: `%s`\n", run.Lifecycle.RepairGateStatus)
		}
		if strings.TrimSpace(run.Lifecycle.DocumentGateStatus) != "" {
			fmt.Fprintf(&diag, "- document_gate: `%s`\n", run.Lifecycle.DocumentGateStatus)
		}
		if strings.TrimSpace(run.Lifecycle.VerificationGateStatus) != "" {
			fmt.Fprintf(&diag, "- verification_gate: `%s`\n", run.Lifecycle.VerificationGateStatus)
		}
		if strings.TrimSpace(run.Lifecycle.SecondPassStatus) != "" {
			fmt.Fprintf(&diag, "- second_pass: %s\n", run.Lifecycle.SecondPassStatus)
		}
		if strings.TrimSpace(run.Lifecycle.CrossReviewTriage) != "" {
			fmt.Fprintf(&diag, "- cross_review_triage: %s\n", run.Lifecycle.CrossReviewTriage)
		}
		if len(run.Lifecycle.RemainingObligations) > 0 {
			fmt.Fprintf(&diag, "- remaining_obligations: `%s`\n", strings.Join(run.Lifecycle.RemainingObligations, "`, `"))
		}
		if strings.TrimSpace(run.Lifecycle.NextRecommendedCommand) != "" {
			fmt.Fprintf(&diag, "- next_recommended_command: `%s`\n", run.Lifecycle.NextRecommendedCommand)
		}
		diag.WriteString("\n")
	}
	if compact := buildReviewCompactStatus(&run, &run.RuntimeGateLedger, nil); compact != nil {
		diag.WriteString("## Compact Operator Status\n\n")
		fmt.Fprintf(&diag, "- status: %s\n", reviewCompactStatusLine(compact, korean))
		fmt.Fprintf(&diag, "- gates: `%s`\n", reviewGateCompactLine(compact))
		if len(compact.CrossReviewTriageCounts) > 0 {
			fmt.Fprintf(&diag, "- cross_review_triage_counts: `%s`\n", reviewCompactMapLine(compact.CrossReviewTriageCounts, []string{crossReviewTriageAcceptedFixed, crossReviewTriageAcceptedDeferred, crossReviewTriageRejectedWithReason, crossReviewTriageNeedsUserDecision, "incomplete_invalid"}))
		}
		if len(compact.BlockersByClass) > 0 {
			fmt.Fprintf(&diag, "- blockers_by_class: `%s`\n", reviewCompactMapLine(compact.BlockersByClass, reviewBlockerClassOrder()))
		}
		if compact.NextRecommendedCommand != "" {
			fmt.Fprintf(&diag, "- next_recommended_command: `%s`\n", compact.NextRecommendedCommand)
		}
		diag.WriteString("\n")
	}
	if timeline := reviewLifecycleTimelineForRun(&run, nil, &run.RuntimeGateLedger, nil); len(timeline) > 0 {
		diag.WriteString("## Lifecycle Timeline\n\n")
		for _, item := range timeline {
			fmt.Fprintf(&diag, "- `%s` status=`%s`", item.Phase, item.Status)
			if strings.TrimSpace(item.Reason) != "" {
				fmt.Fprintf(&diag, " reason=%s", item.Reason)
			}
			if strings.TrimSpace(item.EvidenceRef) != "" {
				fmt.Fprintf(&diag, " evidence=`%s`", item.EvidenceRef)
			}
			if strings.TrimSpace(item.NextSafeAction) != "" {
				fmt.Fprintf(&diag, " next_safe_action=%s", item.NextSafeAction)
			}
			if strings.TrimSpace(item.NextCommand) != "" {
				fmt.Fprintf(&diag, " next_command=`%s`", item.NextCommand)
			}
			diag.WriteString("\n")
		}
		diag.WriteString("\n")
	}
	if blockerSummary := buildReviewBlockerSummary(&run, &run.RuntimeGateLedger, nil); blockerSummary != nil && blockerSummary.HasBlockers {
		diag.WriteString("## Blocker Summary\n\n")
		fmt.Fprintf(&diag, "- counts: `%s`\n", reviewBlockerSummaryStatusLine(blockerSummary))
		for _, item := range blockerSummary.Primary {
			fmt.Fprintf(&diag, "- `%s` %s\n", item.Class, item.Title)
			if strings.TrimSpace(item.WhyBlocks) != "" {
				fmt.Fprintf(&diag, "  - why_blocks: %s\n", item.WhyBlocks)
			}
			if strings.TrimSpace(item.AlreadyChecked) != "" {
				fmt.Fprintf(&diag, "  - already_checked: %s\n", item.AlreadyChecked)
			}
			if len(item.EvidenceRefs) > 0 {
				fmt.Fprintf(&diag, "  - evidence_refs: `%s`\n", strings.Join(item.EvidenceRefs, "`, `"))
			}
			if strings.TrimSpace(item.NextSafeAction) != "" {
				fmt.Fprintf(&diag, "  - next_safe_action: %s\n", item.NextSafeAction)
			}
			if strings.TrimSpace(item.NextCommand) != "" {
				fmt.Fprintf(&diag, "  - next_command: `%s`\n", item.NextCommand)
			}
		}
		diag.WriteString("\n")
	}
	if staleSummary := buildStaleContextSummary(nil, &run, &run.RuntimeGateLedger, nil); staleSummary != nil {
		diag.WriteString("## Stale Context Summary\n\n")
		fmt.Fprintf(&diag, "- status: `%s`\n", staleContextSummaryStatusLine(staleSummary))
		for _, item := range staleSummary.Items {
			fmt.Fprintf(&diag, "- `%s` status=`%s` severity=`%s`", item.Kind, item.Status, item.Severity)
			if strings.TrimSpace(item.Reason) != "" {
				fmt.Fprintf(&diag, " reason=%s", item.Reason)
			}
			if strings.TrimSpace(item.EvidenceRef) != "" {
				fmt.Fprintf(&diag, " evidence=`%s`", item.EvidenceRef)
			}
			if strings.TrimSpace(item.NextSafeAction) != "" {
				fmt.Fprintf(&diag, " next_safe_action=%s", item.NextSafeAction)
			}
			if strings.TrimSpace(item.NextCommand) != "" {
				fmt.Fprintf(&diag, " next_command=`%s`", item.NextCommand)
			}
			fmt.Fprintf(&diag, " finalization_blocked=`%t` allowed_with_disclosure=`%t`", item.FinalizationBlocked, item.AllowedWithDisclosure)
			diag.WriteString("\n")
		}
		diag.WriteString("\n")
	}
	if finalContract := reviewFinalAnswerContractStatusForRun(&run, nil, nil, ""); finalContract != nil {
		diag.WriteString("## Final Answer Contract\n\n")
		fmt.Fprintf(&diag, "- status: `%s`\n", finalContract.Status)
		fmt.Fprintf(&diag, "- request_class: `%s`\n", finalContract.RequestClass)
		if strings.TrimSpace(finalContract.LifecycleKind) != "" {
			fmt.Fprintf(&diag, "- lifecycle_kind: `%s`\n", finalContract.LifecycleKind)
		}
		if strings.TrimSpace(finalContract.Reason) != "" {
			fmt.Fprintf(&diag, "- reason: %s\n", finalContract.Reason)
		}
		for _, requirement := range finalContract.Requirements {
			fmt.Fprintf(&diag, "- `%s` status=`%s`", requirement.Requirement, requirement.Status)
			if strings.TrimSpace(requirement.Reason) != "" {
				fmt.Fprintf(&diag, " reason=%s", requirement.Reason)
			}
			diag.WriteString("\n")
		}
		diag.WriteString("\n")
	}
	if run.RuntimeGateLedger.FinalAnswerCorrection != nil {
		correction := *run.RuntimeGateLedger.FinalAnswerCorrection
		correction.Normalize()
		diag.WriteString("## Final Answer Correction Contract\n\n")
		fmt.Fprintf(&diag, "- status: `%s`\n", correction.Status)
		fmt.Fprintf(&diag, "- attempt_count: `%d`\n", correction.AttemptCount)
		fmt.Fprintf(&diag, "- max_attempts: `%d`\n", correction.MaxAttempts)
		fmt.Fprintf(&diag, "- accepted: `%t`\n", correction.Corrected)
		fmt.Fprintf(&diag, "- rejected: `%t`\n", correction.Rejected)
		if correction.Contract != nil {
			fmt.Fprintf(&diag, "- edits_prohibited: `%t`\n", correction.Contract.EditsProhibited)
			fmt.Fprintf(&diag, "- verification_mode: `%s`\n", correction.Contract.VerificationMode)
			if len(correction.Contract.MissingDisclosureFields) > 0 {
				fmt.Fprintf(&diag, "- missing_disclosure_fields: `%s`\n", strings.Join(correction.Contract.MissingDisclosureFields, "`, `"))
			}
			if len(correction.Contract.RequiredAnswerShape) > 0 {
				fmt.Fprintf(&diag, "- required_answer_shape: `%s`\n", strings.Join(correction.Contract.RequiredAnswerShape, "`, `"))
			}
			if correction.Contract.NextCommand != "" {
				fmt.Fprintf(&diag, "- next_command: `%s`\n", correction.Contract.NextCommand)
			}
		}
		diag.WriteString("\n")
	}
	if len(run.RouteHealthEvents) > 0 {
		diag.WriteString("## Route Health Events\n\n")
		fmt.Fprintf(&diag, "- route_health_events: `%s`\n", strings.Join(reviewRouteHealthEventClasses(run.RouteHealthEvents), "`, `"))
		for _, event := range dedupeReviewRouteHealthEvents(run.RouteHealthEvents, 16) {
			fmt.Fprintf(&diag, "- `%s` role=`%s` class=`%s` status=`%s` provider=`%s` model=`%s` latency_ms=`%d` retry_count=`%d` malformed=`%d` recommendation=%s\n",
				firstNonBlankString(event.TurnID, "turn"),
				event.Role,
				event.FailureClass,
				event.Status,
				firstNonBlankString(event.ProviderLabel, event.Provider),
				firstNonBlankString(event.ModelID, event.ModelLabel),
				event.LatencyMS,
				event.RetryCount,
				event.MalformedOutputCount,
				event.Recommendation,
			)
		}
		diag.WriteString("\n")
	}
	if run.LiveProviderDrill != nil {
		diag.WriteString(renderLiveProviderDrillMarkdown(run.LiveProviderDrill))
	}
	if len(run.ObligationLedger.Items) > 0 {
		diag.WriteString("## Obligation Ledger\n\n")
		fmt.Fprintf(&diag, "- total: `%d` open: `%d`\n", run.ObligationLedger.TotalCount, run.ObligationLedger.OpenCount)
		if len(run.ObligationLedger.Summary) > 0 {
			fmt.Fprintf(&diag, "- open_by_type: `%s`\n", strings.Join(run.ObligationLedger.Summary, ", "))
		}
		for _, obligation := range run.ObligationLedger.Items {
			fmt.Fprintf(&diag, "- `%s` type=`%s` status=`%s` blocking=`%t`: %s\n", obligation.ID, obligation.Type, obligation.Status, obligation.Blocking, obligation.Title)
			if strings.TrimSpace(obligation.RequiredAction) != "" {
				fmt.Fprintf(&diag, "  - Action: %s\n", obligation.RequiredAction)
			}
		}
		diag.WriteString("\n")
	}
	if triage := normalizedCrossReviewTriageLedger(run.CrossReviewTriage); triage != nil && len(triage.Items) > 0 {
		diag.WriteString("## Cross-Review Triage Ledger\n\n")
		fmt.Fprintf(&diag, "- total: `%d` incomplete: `%d`\n", triage.TotalCount, triage.IncompleteCount)
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
				fmt.Fprintf(&diag, "- status_counts: `%s`\n", strings.Join(parts, ", "))
			}
		}
		for _, item := range triage.Items {
			fmt.Fprintf(&diag, "\n### `%s` - %s\n\n", valueOrDefault(item.FindingID, "cross-review-finding"), valueOrDefault(item.Title, "untitled finding"))
			fmt.Fprintf(&diag, "- status: `%s`\n", item.TriageStatus)
			fmt.Fprintf(&diag, "- reviewer: `%s`\n", item.ReviewerRole)
			fmt.Fprintf(&diag, "- severity: `%s`\n", item.Severity)
			fmt.Fprintf(&diag, "- category: `%s`\n", item.Category)
			if strings.TrimSpace(item.Path) != "" {
				if item.Line > 0 {
					fmt.Fprintf(&diag, "- location: `%s:%d`\n", filepath.ToSlash(item.Path), item.Line)
				} else {
					fmt.Fprintf(&diag, "- location: `%s`\n", filepath.ToSlash(item.Path))
				}
			}
			if strings.TrimSpace(item.Symbol) != "" {
				fmt.Fprintf(&diag, "- symbol: `%s`\n", item.Symbol)
			}
			if strings.TrimSpace(item.TechnicalReason) != "" {
				fmt.Fprintf(&diag, "- reason: %s\n", item.TechnicalReason)
			}
			if strings.TrimSpace(item.RequiredFix) != "" {
				fmt.Fprintf(&diag, "- required_fix: %s\n", item.RequiredFix)
			}
			if len(item.FixRefs) > 0 {
				fmt.Fprintf(&diag, "- fix_refs: `%s`\n", strings.Join(item.FixRefs, "`, `"))
			}
			if len(item.ChangedPaths) > 0 {
				fmt.Fprintf(&diag, "- changed_paths: `%s`\n", strings.Join(item.ChangedPaths, "`, `"))
			}
			if len(item.VerificationRefs) > 0 {
				fmt.Fprintf(&diag, "- verification_refs: `%s`\n", strings.Join(item.VerificationRefs, "`, `"))
			}
			if len(item.EvidenceRefs) > 0 {
				fmt.Fprintf(&diag, "- evidence_refs: `%s`\n", strings.Join(item.EvidenceRefs, "`, `"))
			}
			fmt.Fprintf(&diag, "- user_action_needed: `%t`\n", item.UserActionNeeded)
			if strings.TrimSpace(item.UserActionPrompt) != "" {
				fmt.Fprintf(&diag, "- user_action_prompt: %s\n", item.UserActionPrompt)
			}
			if len(item.InspectTargets) > 0 {
				fmt.Fprintf(&diag, "- inspect_targets: `%s`\n", strings.Join(item.InspectTargets, "`, `"))
			}
			if strings.TrimSpace(item.SafeToChange) != "" {
				fmt.Fprintf(&diag, "- safe_to_change: %s\n", item.SafeToChange)
			}
			if strings.TrimSpace(item.DoNotChangeYet) != "" {
				fmt.Fprintf(&diag, "- do_not_change_yet: %s\n", item.DoNotChangeYet)
			}
			if strings.TrimSpace(item.NextCommand) != "" {
				fmt.Fprintf(&diag, "- next_command: `%s`\n", item.NextCommand)
			}
		}
		if len(triage.Blockers) > 0 {
			fmt.Fprintf(&diag, "- blockers: %s\n", strings.Join(triage.Blockers, " | "))
		}
		diag.WriteString("\n")
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
		diag.WriteString("## State Transitions\n\n")
		for _, transition := range run.StateTransitions {
			fmt.Fprintf(&diag, "- `%s` `%s` -> `%s` actor=`%s` blocking=`%t`: %s\n", transition.ID, transition.From, transition.To, transition.Actor, transition.Blocking, transition.Reason)
		}
		diag.WriteString("\n")
	}
	if len(run.ActionEnvelopes) > 0 {
		diag.WriteString("## Action Envelopes\n\n")
		for _, envelope := range run.ActionEnvelopes {
			fmt.Fprintf(&diag, "- `%s` `%s` actor=`%s` status=`%s` approval_required=`%t` approval_granted=`%t`", envelope.ActionID, envelope.ActionType, envelope.Actor, envelope.Status, envelope.ApprovalRequired, envelope.ApprovalGranted)
			if strings.TrimSpace(envelope.FailureClass) != "" {
				fmt.Fprintf(&diag, " failure=`%s`", envelope.FailureClass)
			}
			diag.WriteString("\n")
		}
		diag.WriteString("\n")
	}
	if run.ApprovalLedger.ReviewGateApproved || len(run.ApprovalLedger.MissingApprovals) > 0 || strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		diag.WriteString("## Approval Ledger\n\n")
		fmt.Fprintf(&diag, "- review_gate_approved: `%t`\n", run.ApprovalLedger.ReviewGateApproved)
		fmt.Fprintf(&diag, "- diff_preview_shown: `%t`\n", run.ApprovalLedger.DiffPreviewShown)
		fmt.Fprintf(&diag, "- user_write_approved: `%t`\n", run.ApprovalLedger.UserWriteApproved)
		fmt.Fprintf(&diag, "- write_applied: `%t`\n", run.ApprovalLedger.WriteApplied)
		fmt.Fprintf(&diag, "- verification_passed: `%t`\n", run.ApprovalLedger.VerificationPassed)
		if len(run.ApprovalLedger.MissingApprovals) > 0 {
			fmt.Fprintf(&diag, "- missing_approvals: `%s`\n", strings.Join(run.ApprovalLedger.MissingApprovals, ", "))
		}
		diag.WriteString("\n")
	}
	if strings.TrimSpace(run.CapabilityManifest.LocalFileRead) != "" {
		diag.WriteString("## Capability Manifest\n\n")
		fmt.Fprintf(&diag, "- local_file_read: `%s`\n", run.CapabilityManifest.LocalFileRead)
		fmt.Fprintf(&diag, "- patch_apply: `%s`\n", run.CapabilityManifest.PatchApply)
		fmt.Fprintf(&diag, "- diff_preview: `%s`\n", run.CapabilityManifest.DiffPreview)
		fmt.Fprintf(&diag, "- test_runner: `%s`\n", run.CapabilityManifest.TestRunner)
		fmt.Fprintf(&diag, "- web_search: `%s`\n", run.CapabilityManifest.WebSearch)
		fmt.Fprintf(&diag, "- primary_model: `%s`\n", run.CapabilityManifest.PrimaryModel)
		fmt.Fprintf(&diag, "- cross_review_model: `%s`\n", run.CapabilityManifest.CrossReviewModel)
		fmt.Fprintf(&diag, "- single_model_review_mode: `%s`\n", run.CapabilityManifest.SingleModelReviewMode)
		diag.WriteString("\n")
	}
	if len(run.ExternalLookupIntents) > 0 {
		diag.WriteString("## External Lookup Intents\n\n")
		for _, intent := range run.ExternalLookupIntents {
			fmt.Fprintf(&diag, "- `%s` tool=`%s` status=`%s` blocked=`%t`: %s\n", intent.ID, intent.ToolName, intent.Status, intent.Blocked, intent.Intent)
		}
		diag.WriteString("\n")
	}
	if strings.TrimSpace(run.ArtifactIntegrity.EvidenceHash) != "" || strings.TrimSpace(run.ArtifactIntegrity.ProposalHash) != "" {
		diag.WriteString("## Artifact Integrity\n\n")
		fmt.Fprintf(&diag, "- hash_algorithm: `%s`\n", valueOrDefault(run.ArtifactIntegrity.HashAlgorithm, "sha256"))
		if strings.TrimSpace(run.ArtifactIntegrity.EvidenceHash) != "" {
			fmt.Fprintf(&diag, "- evidence_hash: `%s`\n", run.ArtifactIntegrity.EvidenceHash)
		}
		if strings.TrimSpace(run.ArtifactIntegrity.ProposalHash) != "" {
			fmt.Fprintf(&diag, "- proposal_hash: `%s`\n", run.ArtifactIntegrity.ProposalHash)
		}
		if len(run.ArtifactIntegrity.CurrentFileHashes) > 0 {
			fmt.Fprintf(&diag, "- current_file_hashes: `%d`\n", len(run.ArtifactIntegrity.CurrentFileHashes))
		}
		if len(run.ArtifactIntegrity.Warnings) > 0 {
			fmt.Fprintf(&diag, "- warnings: %s\n", strings.Join(run.ArtifactIntegrity.Warnings, " | "))
		}
		diag.WriteString("\n")
	}
	if strings.TrimSpace(run.LedgerConsistency.Status) != "" {
		if korean {
			b.WriteString("## 검토 범위\n\n")
		} else {
			b.WriteString("## Coverage\n\n")
		}
		fmt.Fprintf(&b, "- %s\n", reviewLedgerCoverageHeadline(run.LedgerConsistency, korean))
		if len(run.LedgerConsistency.Blockers) > 0 {
			if korean {
				fmt.Fprintf(&b, "- 확인 필요: %s\n", strings.Join(run.LedgerConsistency.Blockers, " | "))
			} else {
				fmt.Fprintf(&b, "- Needs attention: %s\n", strings.Join(run.LedgerConsistency.Blockers, " | "))
			}
		}
		if len(run.LedgerConsistency.Warnings) > 0 {
			if korean {
				fmt.Fprintf(&b, "- 참고: %s\n", strings.Join(run.LedgerConsistency.Warnings, " | "))
			} else {
				fmt.Fprintf(&b, "- Notes: %s\n", strings.Join(run.LedgerConsistency.Warnings, " | "))
			}
		}
		// Keep the raw status token only as a machine/debug field, never as the human line.
		fmt.Fprintf(&b, "- status (debug): `%s`\n", run.LedgerConsistency.Status)
		b.WriteString("\n")
	}
	if strings.TrimSpace(run.ResumeSanity.Status) != "" {
		diag.WriteString("## Resume Sanity\n\n")
		fmt.Fprintf(&diag, "- status: `%s`\n", run.ResumeSanity.Status)
		if strings.TrimSpace(run.ResumeSanity.LastStableAction) != "" {
			fmt.Fprintf(&diag, "- last_stable_action: `%s`\n", run.ResumeSanity.LastStableAction)
		}
		if strings.TrimSpace(run.ResumeSanity.NextState) != "" {
			fmt.Fprintf(&diag, "- next_state: `%s`\n", run.ResumeSanity.NextState)
		}
		if strings.TrimSpace(run.ResumeSanity.ConflictReason) != "" {
			fmt.Fprintf(&diag, "- conflict: %s\n", run.ResumeSanity.ConflictReason)
		}
		diag.WriteString("\n")
	}
	if len(run.ModelPlan.CapabilityProfiles) > 0 || len(run.ModelPlan.RouteHealth) > 0 {
		diag.WriteString("## Model Route Capability\n\n")
		for _, profile := range run.ModelPlan.CapabilityProfiles {
			fmt.Fprintf(&diag, "- `%s` provider=`%s` model=`%s` rank=`%d` schema=`%s` latency=`%s` timeout_ms=`%d`\n", profile.Role, profile.Provider, profile.ModelPattern, profile.CapabilityRank, profile.SchemaReliability, profile.LatencyClass, profile.RecommendedTimeoutMS)
		}
		for _, health := range run.ModelPlan.RouteHealth {
			fmt.Fprintf(&diag, "- health `%s` model=`%s` status=`%s` quality=`%s` timeout_rate=`%.2f` weak_rate=`%.2f`: %s\n", health.Role, health.Model, health.LastStatus, health.LastQuality, health.TimeoutRate, health.WeakRate, health.Recommendation)
		}
		diag.WriteString("\n")
	}
	if len(run.ModelPlan.RequiredLenses) > 0 || len(run.ModelPlan.OptionalLenses) > 0 {
		diag.WriteString("## Review Lenses\n\n")
		if len(run.ModelPlan.RequiredLenses) > 0 {
			fmt.Fprintf(&diag, "- required: `%s`\n", strings.Join(run.ModelPlan.RequiredLenses, "`, `"))
		}
		if len(run.ModelPlan.OptionalLenses) > 0 {
			fmt.Fprintf(&diag, "- optional: `%s`\n", strings.Join(run.ModelPlan.OptionalLenses, "`, `"))
		}
		diag.WriteString("\n")
	}
	if rendered := strings.TrimSpace(run.RuntimeGateLedger.RenderPromptSection()); rendered != "" {
		diag.WriteString("## Runtime Gate Ledger\n\n")
		diag.WriteString(rendered)
		diag.WriteString("\n\n")
	}
	if len(run.ModelPlan.UserGuidance) > 0 {
		diag.WriteString("## Model Guidance\n\n")
		for _, item := range run.ModelPlan.UserGuidance {
			fmt.Fprintf(&diag, "- %s\n", item)
		}
		diag.WriteString("\n")
	}
	// Append all lifecycle / ledger / state-transition / capability / sanity
	// diagnostics under a single trailing heading so the human-readable outcome
	// above stays uncluttered. Nothing is dropped, only moved to the back.
	if diagBody := strings.TrimSpace(diag.String()); diagBody != "" {
		if korean {
			b.WriteString("## 진단 상세\n\n")
		} else {
			b.WriteString("## Diagnostics\n\n")
		}
		b.WriteString(diagBody)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func reviewChangeSetPathSectionHeading(run ReviewRun) string {
	if reviewRunHasBlockedPreWriteProposal(run) {
		return "## Blocked Proposal Paths"
	}
	if reviewRunHasUnappliedPreWriteProposal(run) {
		return "## Proposed Paths"
	}
	return "## Changed Paths"
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

// collapseExcerptUnavailableWarnings folds the near-identical "... excerpt
// unavailable ..." warnings into a single summary line so the evidence artifact
// is not buried under nine repetitive entries. All other warnings pass through
// unchanged and in their original order.
func collapseExcerptUnavailableWarnings(warnings []string) []string {
	out := make([]string, 0, len(warnings))
	excerptCount := 0
	insertedAt := -1
	for _, warning := range warnings {
		if strings.Contains(warning, "excerpt unavailable") {
			excerptCount++
			if insertedAt < 0 {
				insertedAt = len(out)
				out = append(out, "")
			}
			continue
		}
		out = append(out, warning)
	}
	if insertedAt >= 0 {
		if excerptCount == 1 {
			out[insertedAt] = "1 source excerpt was unavailable; reviewed from the remaining evidence."
		} else {
			out[insertedAt] = fmt.Sprintf("%d source excerpts were unavailable; reviewed from the remaining evidence.", excerptCount)
		}
	}
	return out
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
		for _, warning := range collapseExcerptUnavailableWarnings(run.Evidence.Warnings) {
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

// reviewLedgerCoverageHeadline turns the internal ledger-consistency status into a
// human sentence about review coverage. It deliberately avoids surfacing the raw
// "blocked" token next to an approved verdict, which reads as a contradiction.
func reviewLedgerCoverageHeadline(check ReviewLedgerConsistencyCheck, korean bool) string {
	switch strings.TrimSpace(check.Status) {
	case reviewLedgerConsistencyBlocked:
		if korean {
			return "검증 보고가 아직 연결되지 않아 검토 범위가 일부에 한정됩니다."
		}
		return "Coverage is partial: verification evidence is not linked yet."
	case reviewLedgerConsistencyWarning:
		if korean {
			return "검토 범위에 보완이 필요한 부분이 있습니다."
		}
		return "Coverage has gaps worth noting."
	default:
		if korean {
			return "변경 내용이 이번 검토 범위 안에서 모두 확인되었습니다."
		}
		return "All changes are covered by this review."
	}
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
	if configProgressDisplay(cfg) == "stream" {
		return renderReviewCLIResultDetailed(cfg, run)
	}
	return renderReviewCLIResultCompact(cfg, run)
}

func renderReviewCLIResultDetailed(cfg Config, run ReviewRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s\n", reviewRunLocalizedText(cfg, run, "Review", "리뷰"), run.ID, run.Gate.Verdict)
	fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Target", "대상"), run.Target)
	fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Mode", "모드"), run.Mode)
	if class := normalizeReviewRequestClass(firstNonBlankString(run.RequestClass, run.RequestAnalysis.RequestClass)); class != "" && class != reviewRequestClassGeneral {
		fmt.Fprintf(&b, "- %s: %s", reviewRunLocalizedText(cfg, run, "Request type", "요청 유형"), humanizeRequestClass(class, reviewRunPrefersKorean(cfg, run)))
		if strings.TrimSpace(run.RequestAnalysis.RequestClassReason) != "" {
			fmt.Fprintf(&b, " (%s)", compactPromptSection(run.RequestAnalysis.RequestClassReason, 120))
		}
		b.WriteString("\n")
	}
	if run.Lifecycle != nil {
		fmt.Fprintf(&b, "- %s: phase=%s route=%s review_gate=%s repair_gate=%s document_gate=%s\n",
			reviewRunLocalizedText(cfg, run, "Lifecycle", "라이프사이클"),
			valueOrDefault(run.Lifecycle.Phase, "unknown"),
			valueOrDefault(run.Lifecycle.RouteMode, "unknown"),
			valueOrDefault(run.Lifecycle.ReviewGateStatus, "unknown"),
			valueOrDefault(run.Lifecycle.RepairGateStatus, "unknown"),
			valueOrDefault(run.Lifecycle.DocumentGateStatus, "unknown"))
	}
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
	// Second-pass internals are a key=value diagnostic blob, not a user-facing
	// sentence; keep them in the trailing Diagnostics section.
	if second := buildReviewSecondPassObservability(run); second != nil {
		fmt.Fprintf(&b, "\n%s:\n", reviewRunLocalizedText(cfg, run, "Diagnostics", "진단"))
		fmt.Fprintf(&b, "- second_pass: %s\n", reviewSecondPassStatusLine(second))
	}
	return strings.TrimSpace(b.String())
}

func renderReviewCLIResultCompact(cfg Config, run ReviewRun) string {
	var b strings.Builder
	korean := reviewRunPrefersKorean(cfg, run)
	verdict := valueOrDefault(run.Gate.Verdict, run.Result.Verdict)
	fmt.Fprintf(&b, "%s %s: %s\n", reviewRunLocalizedText(cfg, run, "Review", "리뷰"), run.ID, humanizeReviewVerdict(verdict, korean))
	noteCount := reviewCLINoteFindingCount(run)
	fmt.Fprintf(&b, "- %s: %s\n",
		reviewRunLocalizedText(cfg, run, "Findings", "발견 항목"),
		reviewFindingCountPhrase(len(run.Findings), len(run.Gate.BlockingFindings), len(run.Gate.WarningFindings), noteCount, korean))
	if line := compactModelReviewSkipLine(run, korean); line != "" {
		fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Model review", "모델 리뷰"), line)
	}
	if strings.TrimSpace(run.Gate.Action) != "" {
		fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Gate action", "게이트 처리"), humanizeGateAction(run.Gate.Action, korean))
	}
	if strings.TrimSpace(run.Target) != "" {
		fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Target", "대상"), humanizeReviewTarget(run.Target, korean))
	}
	if strings.TrimSpace(run.Mode) != "" {
		fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Mode", "유형"), humanizeReviewMode(run.Mode, korean))
	}
	if class := normalizeReviewRequestClass(firstNonBlankString(run.RequestClass, run.RequestAnalysis.RequestClass)); class != "" && class != reviewRequestClassGeneral {
		fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Request type", "요청 유형"), humanizeRequestClass(class, korean))
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
	if len(run.ArtifactRefs) > 0 {
		fmt.Fprintf(&b, "\n%s: %s\n", reviewRunLocalizedText(cfg, run, "Report", "보고서"), run.ArtifactRefs[0])
	}
	if cmd, ok := compactReviewCLIRecommendedNextCommand(cfg, run); ok {
		fmt.Fprintf(&b, "\n%s:\n", reviewRunLocalizedText(cfg, run, "Next command", "다음 명령"))
		renderReviewCLICompactNextCommand(&b, cfg, run, cmd)
	}
	return strings.TrimSpace(b.String())
}

// compactModelReviewSkipLine renders the localized "only deterministic checks
// ran" explanation when model review was skipped. consent_source is only shown
// when it carries information beyond the skip reason itself.
func compactModelReviewSkipLine(run ReviewRun, korean bool) string {
	if strings.TrimSpace(run.SkipReason) == "" {
		return ""
	}
	line := humanizeModelReviewSkipReason(run.SkipReason, korean)
	source := strings.TrimSpace(run.ConsentSource)
	if source != "" && source != "unknown" && source != "not_applicable" && source != "allowed" {
		if korean {
			line += " (동의 경로: " + source + ")"
		} else {
			line += " (consent source: " + source + ")"
		}
	}
	return line
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
	korean := reviewRunPrefersKorean(cfg, run)
	if run.SingleModelPolicy.Enabled {
		var status string
		if korean {
			status = "메인 모델이 구조화된 리뷰를 수행하고, 별도 교차 리뷰어 없이 자동 점검으로 검증했습니다."
		} else {
			status = "The main model ran a structured review and verified it with automated checks, without a separate cross reviewer."
		}
		if detail := renderReviewCLIReviewerRunDetails(run, korean); detail != "" {
			if korean {
				status += " (" + detail + ")"
			} else {
				status += " (" + detail + ")"
			}
		}
		return status
	}
	if len(run.ReviewerRuns) == 0 {
		if korean {
			return "모델 리뷰 없이 자동 점검만으로 검토했습니다."
		}
		return "Reviewed with automated checks only, without a model review."
	}
	return renderReviewCLIReviewerRunDetails(run, korean)
}

// renderReviewCLIReviewerRunDetails renders each reviewer run as a localized
// short phrase ("primary reviewer: done, quality strong"). The raw kind/status/
// quality enums and the provider_raw path are preserved in the Diagnostics
// section and machine fields, not here.
func renderReviewCLIReviewerRunDetails(run ReviewRun, korean bool) string {
	var parts []string
	for _, reviewerRun := range run.ReviewerRuns {
		role := valueOrDefault(reviewRoleProgressName(reviewerRun.Role), reviewerRun.Role)
		status := humanizeGateStatus(reviewerRun.Status, korean)
		quality := humanizeRouteQuality(reviewerRun.ModelQuality, korean)
		var detail string
		if korean {
			detail = role + ": " + status + ", 품질 " + quality
		} else {
			detail = role + ": " + status + ", quality " + quality
		}
		parts = append(parts, detail)
	}
	if korean {
		return strings.Join(parts, " / ")
	}
	return strings.Join(parts, " / ")
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

func compactReviewCLIRecommendedNextCommand(cfg Config, run ReviewRun) (ReviewNextCommand, bool) {
	commands := compactReviewCLINextCommands(cfg, run)
	if len(commands) == 0 {
		return ReviewNextCommand{}, false
	}
	return commands[0], true
}

func compactReviewCLINextCommands(cfg Config, run ReviewRun) []ReviewNextCommand {
	seen := map[string]int{}
	var out []ReviewNextCommand
	for _, cmd := range run.Gate.NextCommands {
		command := strings.TrimSpace(cmd.Command)
		if command == "" {
			continue
		}
		key := strings.ToLower(command)
		if idx, ok := seen[key]; ok {
			merged := out[idx]
			merged.RequiresConfirmation = merged.RequiresConfirmation || cmd.RequiresConfirmation
			merged.AutoRun = merged.AutoRun || cmd.AutoRun
			if strings.TrimSpace(merged.Reason) != "" && strings.TrimSpace(cmd.Reason) != "" && !strings.Contains(merged.Reason, cmd.Reason) {
				merged.Reason = compactPromptSection(merged.Reason+"; "+cmd.Reason, 240)
			} else if strings.TrimSpace(merged.Reason) == "" {
				merged.Reason = cmd.Reason
			}
			if strings.TrimSpace(merged.ClientHint) == "" {
				merged.ClientHint = cmd.ClientHint
			}
			if strings.TrimSpace(merged.ExpectedResult) == "" {
				merged.ExpectedResult = cmd.ExpectedResult
			}
			out[idx] = merged
			continue
		}
		seen[key] = len(out)
		out = append(out, cmd)
	}
	return out
}

func renderReviewCLICompactNextCommand(b *strings.Builder, cfg Config, run ReviewRun, cmd ReviewNextCommand) {
	fmt.Fprintf(b, "- %s", cmd.Command)
	fmt.Fprintf(b, " (%s=%t)", reviewRunLocalizedText(cfg, run, "requires_confirmation", "확인 필요"), cmd.RequiresConfirmation)
	b.WriteString("\n")
	if reason := compactPromptSection(reviewNextCommandReasonText(cfg, run, cmd), 180); strings.TrimSpace(reason) != "" {
		fmt.Fprintf(b, "  %s: %s\n", reviewRunLocalizedText(cfg, run, "Reason", "이유"), reason)
	}
	if when := compactPromptSection(reviewNextCommandWhenText(cfg, run, cmd), 180); strings.TrimSpace(when) != "" {
		fmt.Fprintf(b, "  %s: %s\n", reviewRunLocalizedText(cfg, run, "When", "시점"), when)
	}
	if hint := compactPromptSection(reviewNextCommandHintText(cfg, run, cmd), 220); strings.TrimSpace(hint) != "" {
		fmt.Fprintf(b, "  %s: %s\n", reviewRunLocalizedText(cfg, run, "Action", "실행 방법"), hint)
	}
	if expected := compactPromptSection(reviewNextCommandExpectedResultText(cfg, run, cmd), 220); strings.TrimSpace(expected) != "" {
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
