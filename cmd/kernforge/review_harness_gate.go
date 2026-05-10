package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

func deterministicReviewFindings(rt *runtimeState, run ReviewRun) []ReviewFinding {
	var findings []ReviewFinding
	preWrite := strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write")
	if len(run.Evidence.Sources) == 0 || strings.TrimSpace(run.Evidence.Text) == "" {
		findings = append(findings, ReviewFinding{
			Source:       "deterministic",
			ReviewerRole: "evidence_reviewer",
			Severity:     reviewSeverityBlocker,
			Category:     "evidence_gap",
			Confidence:   "high",
			Quality:      reviewFindingQualityComplete,
			Title:        "No reviewable evidence was collected",
			Evidence:     "The review target did not provide diff, code, selection, plan, analysis, or workspace change evidence.",
			Impact:       "A review approval without evidence would be misleading.",
			RequiredFix:  "Provide a review target, changed files, diff, code excerpt, selection, or plan text.",
			BlocksGate:   true,
		})
	}
	if preWrite {
		// Pre-write review gates the proposed diff before it is applied. Runtime
		// verification is still expected after the edit, so missing verification
		// evidence must not block the pre-write gate.
	} else if run.Evidence.VerificationFailed {
		findings = append(findings, ReviewFinding{
			Source:             "deterministic",
			ReviewerRole:       "verification_reviewer",
			Severity:           reviewSeverityBlocker,
			Category:           "test_gap",
			Confidence:         "high",
			Quality:            reviewFindingQualityComplete,
			Title:              "Latest verification has failures",
			Evidence:           run.Evidence.VerificationSummary,
			Impact:             "The review gate cannot approve a change while the latest verification is failing.",
			RequiredFix:        "Fix the failing verification or record a narrow waiver with a concrete reason.",
			TestRecommendation: "/verify --full",
			BlocksGate:         true,
		})
	} else if run.Evidence.VerificationRequired && strings.TrimSpace(run.Evidence.VerificationSummary) == "" {
		findings = append(findings, ReviewFinding{
			Source:             "deterministic",
			ReviewerRole:       "verification_reviewer",
			Severity:           reviewSeverityBlocker,
			Category:           "test_gap",
			Confidence:         "high",
			Quality:            reviewFindingQualityComplete,
			Title:              "Verification is required but missing",
			Evidence:           "Acceptance contract requires verification, but no latest verification report is recorded.",
			Impact:             "The gate lacks the evidence required by the original task contract.",
			RequiredFix:        "Run the required verification and repeat /review.",
			TestRecommendation: "/verify --full",
			BlocksGate:         true,
		})
	} else if reviewRunHasChangeEvidence(run) && strings.TrimSpace(run.Evidence.VerificationSummary) == "" && run.Target != reviewTargetPlan {
		severity := reviewSeverityMedium
		if reviewChangedPathsDocsOnly(run.ChangeSet.ChangedPaths) {
			severity = reviewSeverityLow
		}
		findings = append(findings, ReviewFinding{
			Source:             "deterministic",
			ReviewerRole:       "test_impact_reviewer",
			Severity:           severity,
			Category:           "test_gap",
			Confidence:         "medium",
			Quality:            reviewFindingQualityComplete,
			Title:              "Changed files have no latest verification evidence",
			Evidence:           fmt.Sprintf("Changed paths: %s", strings.Join(limitStrings(run.ChangeSet.ChangedPaths, 8), ", ")),
			Impact:             "Regression risk remains unknown.",
			RequiredFix:        "Run a focused or full verification pass before treating the review as final.",
			TestRecommendation: "/verify --full",
			BlocksGate:         false,
		})
	}
	if run.Redaction.Redacted || strings.EqualFold(run.Redaction.Status, "warning") {
		findings = append(findings, ReviewFinding{
			Source:       "deterministic",
			ReviewerRole: "redaction_reviewer",
			Severity:     reviewSeverityLow,
			Category:     "operational_risk",
			Confidence:   "medium",
			Quality:      reviewFindingQualityComplete,
			Title:        "Sensitive evidence was redacted",
			Evidence:     strings.Join(run.Redaction.Patterns, ", "),
			Impact:       "The reviewer saw a bounded/redacted context; some raw evidence may require local inspection.",
			RequiredFix:  "Inspect the sensitive source locally if it is relevant to the finding.",
			BlocksGate:   false,
		})
	}
	if rt != nil && rt.session != nil && rt.session.LastCodingHarnessReport != nil {
		report := *rt.session.LastCodingHarnessReport
		report.Normalize()
		for _, finding := range report.allFindings() {
			title := strings.TrimSpace(finding.Title)
			if title == "" {
				title = compactPromptSection(finding.Detail, 100)
			}
			if title == "" {
				continue
			}
			severity := reviewSeverityLow
			blocks := false
			switch strings.ToLower(strings.TrimSpace(finding.Severity)) {
			case "blocker":
				severity = reviewSeverityBlocker
				blocks = true
			case "warning":
				severity = reviewSeverityMedium
			}
			findings = append(findings, ReviewFinding{
				Source:       "deterministic",
				ReviewerRole: "coding_harness",
				Severity:     severity,
				Category:     "operational_risk",
				Confidence:   "high",
				Quality:      reviewFindingQualityComplete,
				Title:        title,
				Evidence:     compactPromptSection(finding.Detail, 500),
				Impact:       "Existing coding harness state is part of the review gate.",
				RequiredFix:  "Resolve the coding harness finding or record a scoped waiver.",
				BlocksGate:   blocks,
			})
		}
	}
	for _, warning := range run.Evidence.Warnings {
		findings = append(findings, ReviewFinding{
			Source:       "deterministic",
			ReviewerRole: "collector",
			Severity:     reviewSeverityInfo,
			Category:     "evidence_gap",
			Confidence:   "medium",
			Quality:      reviewFindingQualityPartial,
			Title:        "Review evidence warning",
			Evidence:     warning,
			Impact:       "The collected context may be incomplete.",
			RequiredFix:  "Repeat /review with a narrower target if this warning affects the result.",
		})
	}
	assignReviewFindingIDs(findings)
	return findings
}

func mergeReviewFindings(findings []ReviewFinding) ([]ReviewFinding, ReviewMergeResult) {
	result := ReviewMergeResult{}
	seen := map[string]int{}
	var merged []ReviewFinding
	for _, finding := range findings {
		finding.Normalize()
		key := reviewFindingKey(finding)
		if prior, ok := seen[key]; ok {
			existing := merged[prior]
			result.SuppressedDuplicates = append(result.SuppressedDuplicates, finding.ID)
			if reviewSeverityRank(finding.Severity) < reviewSeverityRank(existing.Severity) {
				result.SeverityChanges = append(result.SeverityChanges, fmt.Sprintf("%s kept higher severity %s over %s", existing.ID, finding.Severity, existing.Severity))
				merged[prior].Severity = finding.Severity
				merged[prior].BlocksGate = merged[prior].BlocksGate || finding.BlocksGate
			}
			continue
		}
		seen[key] = len(merged)
		if strings.EqualFold(finding.Source, "deterministic") && finding.BlocksGate {
			result.DeterministicPreserved = append(result.DeterministicPreserved, finding.ID)
		}
		merged = append(merged, finding)
	}
	sortReviewFindings(merged)
	assignReviewFindingIDs(merged)
	for _, finding := range merged {
		result.MergedFindings = append(result.MergedFindings, finding.ID)
	}
	return merged, result
}

func (f *ReviewFinding) Normalize() {
	if f == nil {
		return
	}
	f.Severity = normalizeReviewSeverity(f.Severity)
	if strings.TrimSpace(f.Category) == "" {
		f.Category = "correctness"
	}
	f.RequiredFix = sanitizeReviewRequiredFix(f.RequiredFix)
	if strings.TrimSpace(f.Confidence) == "" {
		f.Confidence = "medium"
	}
	if strings.TrimSpace(f.Quality) == "" {
		f.Quality = classifyReviewFindingQuality(*f)
	}
	if strings.TrimSpace(f.Title) == "" {
		f.Title = compactPromptSection(firstNonBlankString(f.Impact, f.Evidence, f.RequiredFix), 100)
	}
	if f.Severity == reviewSeverityBlocker || (f.Severity == reviewSeverityHigh && f.Quality == reviewFindingQualityComplete) {
		f.BlocksGate = f.BlocksGate || f.Severity == reviewSeverityBlocker
	}
}

func assignReviewFindingIDs(findings []ReviewFinding) {
	used := map[string]bool{}
	next := 1
	for i := range findings {
		id := strings.TrimSpace(findings[i].ID)
		if id == "" || used[id] {
			for {
				id = fmt.Sprintf("RF-%03d", next)
				next++
				if !used[id] {
					break
				}
			}
		}
		findings[i].ID = id
		used[id] = true
	}
}

func sanitizeReviewRequiredFix(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"fixed by ", "already fixed by ", "this was fixed by "} {
		if strings.HasPrefix(lower, prefix) {
			rest := strings.TrimSpace(trimmed[len(prefix):])
			if rest == "" {
				return "Apply the reviewer-described fix if it is not already present."
			}
			return "Apply this fix if it is not already present: " + rest
		}
	}
	return trimmed
}

func normalizeReviewSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case reviewSeverityBlocker, "critical", "error":
		return reviewSeverityBlocker
	case reviewSeverityHigh:
		return reviewSeverityHigh
	case reviewSeverityMedium, "warning", "warn":
		return reviewSeverityMedium
	case reviewSeverityLow:
		return reviewSeverityLow
	case reviewSeverityInfo, "":
		return reviewSeverityInfo
	default:
		return severity
	}
}

func classifyReviewFindingQuality(f ReviewFinding) string {
	specific := 0
	if strings.TrimSpace(f.Path) != "" || strings.TrimSpace(f.Symbol) != "" {
		specific++
	}
	if strings.TrimSpace(f.Evidence) != "" {
		specific++
	}
	if strings.TrimSpace(f.Impact) != "" {
		specific++
	}
	if strings.TrimSpace(f.RequiredFix) != "" {
		specific++
	}
	if specific >= 4 {
		return reviewFindingQualityComplete
	}
	if specific >= 2 {
		return reviewFindingQualityPartial
	}
	return reviewFindingQualityWeak
}

func reviewFindingKey(f ReviewFinding) string {
	path := strings.ToLower(strings.TrimSpace(filepathSlash(f.Path)))
	symbol := strings.ToLower(strings.TrimSpace(f.Symbol))
	category := strings.ToLower(strings.TrimSpace(f.Category))
	title := strings.ToLower(strings.TrimSpace(f.Title))
	if path == "" && symbol == "" {
		return category + "|" + title
	}
	return path + "|" + symbol + "|" + category + "|" + title
}

func evaluateReviewGate(run ReviewRun) GateDecision {
	gate := GateDecision{
		WaiverAllowed:        true,
		WaiverReasonRequired: true,
	}
	for _, finding := range run.Findings {
		if reviewFindingBlocksGate(run, finding) {
			gate.BlockingFindings = append(gate.BlockingFindings, finding.ID)
			gate.RequiredActions = append(gate.RequiredActions, finding.RequiredFix)
			continue
		}
		if reviewFindingCountsAsWarning(finding) {
			gate.WarningFindings = append(gate.WarningFindings, finding.ID)
		}
	}
	gate.RequiredActions = normalizeTaskStateList(gate.RequiredActions, 12)
	switch {
	case len(gate.BlockingFindings) > 0:
		if reviewHasOnlyEvidenceBlockers(run.Findings, gate.BlockingFindings) {
			gate.Verdict = reviewVerdictInsufficientEvidence
			gate.Reason = "required review evidence is missing or stale"
		} else {
			gate.Verdict = reviewVerdictNeedsRevision
			gate.Reason = "blocking review findings require revision"
		}
	case len(gate.WarningFindings) > 0 || run.Result.Degraded:
		gate.Verdict = reviewVerdictApprovedWithWarnings
		gate.Reason = "no blockers, but warnings or degraded reviewer evidence remain"
	default:
		gate.Verdict = reviewVerdictApproved
		gate.Reason = "no blocking findings found"
	}
	gate.NextCommands = reviewNextCommands(run, gate)
	if run.Result.Degraded && strings.TrimSpace(run.Result.DegradedReason) != "" {
		gate.QualityNotes = append(gate.QualityNotes, run.Result.DegradedReason)
	}
	for _, guidance := range run.ModelPlan.UserGuidance {
		gate.QualityNotes = append(gate.QualityNotes, guidance)
	}
	return gate
}

func reviewFindingCountsAsWarning(finding ReviewFinding) bool {
	if strings.EqualFold(finding.Severity, reviewSeverityHigh) ||
		strings.EqualFold(finding.Severity, reviewSeverityMedium) ||
		strings.EqualFold(finding.Severity, reviewSeverityLow) {
		return true
	}
	return false
}

func reviewFindingBlocksGate(run ReviewRun, finding ReviewFinding) bool {
	if finding.BlocksGate || strings.EqualFold(finding.Severity, reviewSeverityBlocker) {
		return true
	}
	if !strings.EqualFold(finding.Severity, reviewSeverityHigh) {
		return false
	}
	if strings.EqualFold(finding.Category, "security") ||
		strings.EqualFold(finding.Category, "bypass_surface") ||
		strings.EqualFold(finding.Category, "credential_leak") {
		return true
	}
	for _, pack := range run.PolicyPacks {
		if strings.EqualFold(pack, "base_security") ||
			strings.EqualFold(pack, "windows_kernel_driver") ||
			strings.EqualFold(pack, "anti_cheat_telemetry") {
			return true
		}
	}
	return false
}

func reviewHasOnlyEvidenceBlockers(findings []ReviewFinding, ids []string) bool {
	if len(ids) == 0 {
		return false
	}
	idSet := map[string]bool{}
	for _, id := range ids {
		idSet[id] = true
	}
	for _, finding := range findings {
		if !idSet[finding.ID] {
			continue
		}
		if !strings.EqualFold(finding.Category, "evidence_gap") && !strings.EqualFold(finding.Category, "test_gap") {
			return false
		}
	}
	return true
}

func reviewNextCommands(run ReviewRun, gate GateDecision) []ReviewNextCommand {
	var out []ReviewNextCommand
	if strings.TrimSpace(run.Evidence.VerificationSummary) == "" && reviewRunHasChangeEvidence(run) && run.Target != reviewTargetPlan {
		out = append(out, ReviewNextCommand{
			ID:         "verify",
			Command:    "/verify --full",
			Reason:     "changed files have no latest verification evidence",
			Safety:     "safe_local",
			When:       "before completion or git write",
			AutoRun:    false,
			ClientHint: "Run verification, then repeat /review.",
		})
	}
	if gate.Verdict == reviewVerdictNeedsRevision || gate.Verdict == reviewVerdictBlocked {
		out = append(out, ReviewNextCommand{
			ID:         "repair",
			Command:    "/continuity continue from review",
			Reason:     "blocking findings need a focused repair pass",
			Safety:     "safe_local",
			When:       "after reading review findings",
			AutoRun:    false,
			ClientHint: "Use the repair prompt in the review artifact.",
		})
	}
	if gate.Verdict == reviewVerdictApprovedWithWarnings && reviewHasActionableWarningFindings(run, gate.WarningFindings) {
		out = append(out, ReviewNextCommand{
			ID:         "repair-warnings",
			Command:    "/continuity continue from review",
			Reason:     "warning findings are actionable and can be fixed now",
			Safety:     "safe_local",
			When:       "if you want to address the warnings instead of accepting them",
			AutoRun:    false,
			ClientHint: "Say `수정해줘` or run this command to repair the latest review warnings.",
		})
	}
	if gate.Verdict == reviewVerdictApprovedWithWarnings {
		out = append(out, ReviewNextCommand{
			ID:         "completion-audit",
			Command:    "/completion-audit",
			Reason:     "warnings remain; completion audit can validate final readiness",
			Safety:     "read_only",
			When:       "before final answer",
			AutoRun:    false,
			ClientHint: "Check final readiness before claiming completion.",
		})
	}
	if strings.Contains(strings.Join(run.ModelPlan.MissingRoles, ","), "security_reviewer") || reviewHasSecurityFinding(run.Findings) {
		out = append(out, ReviewNextCommand{
			ID:         "set-security-model",
			Command:    "/review models security",
			Reason:     "security-sensitive review is using a fallback reviewer",
			Safety:     "read_only",
			When:       "before future security reviews",
			AutoRun:    false,
			ClientHint: "Configure a dedicated security reviewer model.",
		})
	}
	if strings.Contains(strings.Join(run.ModelPlan.MissingRoles, ","), "false_positive_reviewer") {
		out = append(out, ReviewNextCommand{
			ID:         "set-false-positive-model",
			Command:    "/review models false-positive",
			Reason:     "anti-cheat or detection review is using a fallback false-positive reviewer",
			Safety:     "read_only",
			When:       "before future security reviews",
			AutoRun:    false,
			ClientHint: "Configure a dedicated false-positive reviewer model.",
		})
	}
	return out
}

func reviewHasActionableWarningFindings(run ReviewRun, warningIDs []string) bool {
	if len(warningIDs) == 0 {
		return false
	}
	warnings := reviewFindingIDSet(warningIDs)
	for _, finding := range run.Findings {
		if !warnings[finding.ID] {
			continue
		}
		if strings.EqualFold(finding.Category, "test_gap") ||
			strings.EqualFold(finding.Category, "evidence_gap") {
			continue
		}
		if strings.TrimSpace(finding.RequiredFix) != "" ||
			strings.TrimSpace(finding.Path) != "" ||
			strings.TrimSpace(finding.Symbol) != "" {
			return true
		}
	}
	return false
}

func reviewHasSecurityFinding(findings []ReviewFinding) bool {
	for _, finding := range findings {
		if strings.EqualFold(finding.Category, "security") ||
			strings.EqualFold(finding.Category, "bypass_surface") ||
			strings.EqualFold(finding.Category, "credential_leak") {
			return true
		}
	}
	return false
}

func reviewRunHasChangeEvidence(run ReviewRun) bool {
	if strings.TrimSpace(run.ChangeSet.DiffExcerpt) != "" || strings.TrimSpace(run.ChangeSet.DiffStat) != "" {
		return true
	}
	if len(run.ChangeSet.AddedPaths) > 0 ||
		len(run.ChangeSet.ModifiedPaths) > 0 ||
		len(run.ChangeSet.DeletedPaths) > 0 ||
		len(run.ChangeSet.RenamedPaths) > 0 ||
		len(run.ChangeSet.BinaryPaths) > 0 ||
		len(run.ChangeSet.UntrackedPaths) > 0 {
		return true
	}
	return false
}

func buildReviewRepairPlan(run ReviewRun) ReviewRepairPlan {
	var blocking []ReviewFinding
	for _, finding := range run.Findings {
		if reviewFindingBlocksGate(run, finding) {
			blocking = append(blocking, finding)
		}
	}
	if len(blocking) == 0 {
		return ReviewRepairPlan{}
	}
	var b strings.Builder
	b.WriteString("Repair the review blockers without broadening scope.\n\n")
	if strings.TrimSpace(run.Objective) != "" {
		b.WriteString("Objective:\n")
		b.WriteString(run.Objective)
		b.WriteString("\n\n")
	}
	b.WriteString("Blocking findings:\n")
	var ids []string
	var actions []string
	for _, finding := range blocking {
		ids = append(ids, finding.ID)
		actions = append(actions, finding.RequiredFix)
		fmt.Fprintf(&b, "- %s [%s/%s] %s\n", finding.ID, finding.Severity, finding.Category, finding.Title)
		if strings.TrimSpace(finding.Evidence) != "" {
			fmt.Fprintf(&b, "  Evidence: %s\n", compactPromptSection(finding.Evidence, 350))
		}
		if strings.TrimSpace(finding.RequiredFix) != "" {
			fmt.Fprintf(&b, "  Required fix: %s\n", compactPromptSection(finding.RequiredFix, 350))
		}
		if strings.TrimSpace(finding.TestRecommendation) != "" {
			fmt.Fprintf(&b, "  Verification: %s\n", finding.TestRecommendation)
		}
	}
	b.WriteString("\nDo not do unrelated cleanup, dependency upgrades, or large refactors unless a blocking finding explicitly requires it.")
	return ReviewRepairPlan{
		Required:        true,
		Prompt:          b.String(),
		Findings:        ids,
		RequiredActions: normalizeTaskStateList(actions, 12),
	}
}

func reviewResultSummary(run ReviewRun) string {
	switch run.Gate.Verdict {
	case reviewVerdictApproved:
		return "Review approved with no blocking findings."
	case reviewVerdictApprovedWithWarnings:
		return fmt.Sprintf("Review approved with warnings: %d warning finding(s).", len(run.Gate.WarningFindings))
	case reviewVerdictNeedsRevision:
		return fmt.Sprintf("Review needs revision: %d blocking finding(s).", len(run.Gate.BlockingFindings))
	case reviewVerdictInsufficientEvidence:
		return "Review has insufficient evidence for approval."
	default:
		return "Review is blocked."
	}
}

func reviewKeyRisks(findings []ReviewFinding) []string {
	var out []string
	for _, finding := range findings {
		if reviewSeverityRank(finding.Severity) <= reviewSeverityRank(reviewSeverityMedium) {
			out = append(out, finding.Title)
		}
	}
	return normalizeTaskStateList(out, 8)
}

func reviewMissingEvidence(findings []ReviewFinding) []string {
	var out []string
	for _, finding := range findings {
		if strings.EqualFold(finding.Category, "evidence_gap") || strings.EqualFold(finding.Category, "test_gap") {
			out = append(out, firstNonBlankString(finding.RequiredFix, finding.Title))
		}
	}
	return normalizeTaskStateList(out, 8)
}

func reviewVerifiedEvidence(run ReviewRun) []string {
	var out []string
	if strings.TrimSpace(run.Evidence.VerificationSummary) != "" {
		out = append(out, run.Evidence.VerificationSummary)
	}
	if strings.TrimSpace(run.Evidence.CodingHarnessSummary) != "" {
		out = append(out, "coding harness evidence collected")
	}
	return out
}

func parseModelReviewFindings(raw string, role string) ([]ReviewFinding, string) {
	return parseModelReviewFindingsForLanguage(raw, role, false)
}

func parseModelReviewFindingsForLanguage(raw string, role string, korean bool) ([]ReviewFinding, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, reviewModelQualityFailed
	}
	var findings []ReviewFinding
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	current := ReviewFinding{
		Source:       "model",
		ReviewerRole: role,
		Severity:     reviewSeverityInfo,
		Category:     "correctness",
		Confidence:   "medium",
	}
	omittedStructuredField := false
	omittedPlaceholderAdded := false
	flush := func() {
		current.Normalize()
		if reviewFindingHasContent(current) {
			if reviewFindingHasOmissionMarker(current) {
				omittedStructuredField = true
				if salvaged, ok := reviewSalvageOmittedFinding(current, korean); ok {
					current = salvaged
				} else {
					if omittedPlaceholderAdded {
						current = ReviewFinding{
							Source:       "model",
							ReviewerRole: role,
							Severity:     reviewSeverityInfo,
							Category:     "correctness",
							Confidence:   "medium",
						}
						return
					}
					current = reviewOmittedFindingPlaceholder(current, korean)
					omittedPlaceholderAdded = true
				}
			}
			findings = append(findings, current)
		}
		current = ReviewFinding{
			Source:       "model",
			ReviewerRole: role,
			Severity:     reviewSeverityInfo,
			Category:     "correctness",
			Confidence:   "medium",
		}
	}
	severityRe := regexp.MustCompile(`(?i)\b(blocker|high|medium|low|info|warning|critical)\b`)
	pathRe := regexp.MustCompile(`([A-Za-z]:)?[A-Za-z0-9_\-./\\]+\.[A-Za-z0-9_]+(:\d+)?`)
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimLeft(line, "-*0123456789. "))
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if lower == "review_result" ||
			lower == "findings:" ||
			strings.HasPrefix(lower, "verdict:") ||
			strings.HasPrefix(lower, "summary:") {
			continue
		}
		if strings.HasPrefix(lower, "severity:") {
			if reviewFindingHasContent(current) {
				flush()
			}
			current.Severity = strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":"))
			continue
		}
		if strings.HasPrefix(lower, "title:") {
			current.Title = cleanReviewModelFieldValue(strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":")))
			continue
		}
		if strings.HasPrefix(lower, "category:") {
			current.Category = strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":"))
			continue
		}
		if strings.HasPrefix(lower, "path:") {
			current.Path = strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":"))
			continue
		}
		if strings.HasPrefix(lower, "symbol:") {
			current.Symbol = strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":"))
			continue
		}
		if strings.HasPrefix(lower, "evidence:") {
			current.Evidence = cleanReviewModelFieldValue(strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":")))
			continue
		}
		if strings.HasPrefix(lower, "impact:") {
			current.Impact = cleanReviewModelFieldValue(strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":")))
			continue
		}
		if strings.HasPrefix(lower, "required_fix:") || strings.HasPrefix(lower, "fix:") {
			current.RequiredFix = cleanReviewModelFieldValue(strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":")))
			continue
		}
		if strings.HasPrefix(lower, "test_recommendation:") || strings.HasPrefix(lower, "test:") {
			current.TestRecommendation = cleanReviewModelFieldValue(strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":")))
			continue
		}
		if severityRe.MatchString(trimmed) && (strings.Contains(trimmed, ":") || strings.Contains(trimmed, "]")) {
			flush()
			current.Severity = normalizeReviewSeverity(severityRe.FindString(trimmed))
			current.Title = cleanReviewModelFieldValue(reviewFallbackFindingTitle(trimmed, severityRe))
			if path := pathRe.FindString(trimmed); path != "" {
				current.Path = strings.TrimSuffix(filepathSlash(path), ":")
			}
			current.Evidence = trimmed
			current.Impact = "Model reviewer identified this as a review risk."
			current.RequiredFix = "Inspect and address this reviewer finding."
			continue
		}
		if strings.TrimSpace(current.Title) == "" && strings.Contains(trimmed, ":") {
			current.Title = cleanReviewModelFieldValue(trimmed)
			current.Evidence = trimmed
		}
	}
	flush()
	assignReviewFindingIDs(findings)
	if len(findings) == 0 {
		if reviewRawLooksNonBlockingApproval(raw) {
			return []ReviewFinding{reviewUnstructuredApprovalFinding(raw, role, korean)}, reviewModelQualityUsable
		}
		return []ReviewFinding{reviewNoStructuredFindingPlaceholder(raw, role, korean)}, reviewModelQualityWeak
	}
	if omittedStructuredField {
		if !reviewFindingsContainOmittedOutputPlaceholder(findings) {
			return findings, reviewModelQualityUsable
		}
		return findings, reviewModelQualityWeak
	}
	return findings, reviewModelQualityUsable
}

func reviewRawLooksNonBlockingApproval(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "" {
		return false
	}
	if containsAny(lower, "needs revision", "needs_revision", "blocked", "blocker:", "critical:", "must fix") {
		return false
	}
	return containsAny(lower,
		"no blocking finding", "no blocking findings", "no blockers", "no blocker",
		"no critical issue", "no critical issues", "approved", "looks good",
		"차단 finding 없음", "차단 이슈 없음", "차단 문제 없음", "승인")
}

func reviewUnstructuredApprovalFinding(raw string, role string, korean bool) ReviewFinding {
	raw = cleanReviewModelFieldValue(raw)
	if korean {
		return ReviewFinding{
			Source:       "model",
			ReviewerRole: role,
			Severity:     reviewSeverityInfo,
			Category:     "maintainability",
			Confidence:   "medium",
			Quality:      reviewFindingQualityPartial,
			Title:        raw,
			Evidence:     "리뷰어가 구조화된 finding 없이 차단 이슈가 없다는 비정형 요약을 반환했습니다.",
			Impact:       "차단 이슈는 없지만, 비정형 요약이므로 구체적인 경고나 권고는 별도 finding보다 약한 근거로 취급합니다.",
			BlocksGate:   false,
		}
	}
	return ReviewFinding{
		Source:       "model",
		ReviewerRole: role,
		Severity:     reviewSeverityInfo,
		Category:     "maintainability",
		Confidence:   "medium",
		Quality:      reviewFindingQualityPartial,
		Title:        raw,
		Evidence:     "Reviewer returned an unstructured non-blocking approval summary.",
		Impact:       "No blocking issue was reported, but the summary is weaker than structured findings.",
		BlocksGate:   false,
	}
}

func reviewNoStructuredFindingPlaceholder(raw string, role string, korean bool) ReviewFinding {
	if korean {
		return ReviewFinding{
			Source:       "model",
			ReviewerRole: role,
			Severity:     reviewSeverityInfo,
			Category:     "evidence_gap",
			Confidence:   "low",
			Quality:      reviewFindingQualityWeak,
			Title:        "모델 리뷰가 구조화된 finding을 반환하지 않음",
			Evidence:     "모델 출력에서 정확한 gate finding을 추출할 수 없었습니다.",
			Impact:       "정확한 finding 없이 리뷰를 승인하거나 수정 흐름을 시작하면 잘못된 결론으로 이어질 수 있습니다.",
			RequiredFix:  "더 좁은 범위나 더 강한 리뷰어 모델로 리뷰를 다시 실행하고, deterministic finding만 신뢰하십시오.",
		}
	}
	return ReviewFinding{
		Source:       "model",
		ReviewerRole: role,
		Severity:     reviewSeverityInfo,
		Category:     "evidence_gap",
		Confidence:   "low",
		Quality:      reviewFindingQualityWeak,
		Title:        "Model review returned no structured findings",
		Evidence:     compactPromptSection(raw, 500),
		Impact:       "The model output could not be used as a precise gate finding.",
		RequiredFix:  "Use deterministic findings and rerun with a stronger reviewer if needed.",
	}
}

func reviewOmittedFindingPlaceholder(finding ReviewFinding, korean bool) ReviewFinding {
	if korean {
		return ReviewFinding{
			Source:       firstNonBlankString(finding.Source, "model"),
			ReviewerRole: finding.ReviewerRole,
			Severity:     reviewSeverityMedium,
			Category:     "evidence_gap",
			Confidence:   "medium",
			Quality:      reviewFindingQualityWeak,
			Path:         finding.Path,
			Line:         finding.Line,
			Symbol:       finding.Symbol,
			Title:        "리뷰어 출력이 finding 일부를 생략함",
			Evidence:     "리뷰어가 구조화된 finding 필드 안에 말줄임표 또는 생략 표식을 반환해서, 이 finding은 실행 가능한 코드 이슈로 다루기에는 충분히 정밀하지 않습니다.",
			Impact:       "생략된 finding을 기준으로 수정하거나 차단하면 repair 흐름이 잘못된 방향으로 갈 수 있습니다.",
			RequiredFix:  "더 좁은 범위나 더 강한 리뷰어 모델로 리뷰를 다시 실행하세요. 생략된 finding만 근거로 코드 변경을 하지 마세요.",
			BlocksGate:   false,
		}
	}
	return ReviewFinding{
		Source:       firstNonBlankString(finding.Source, "model"),
		ReviewerRole: finding.ReviewerRole,
		Severity:     reviewSeverityMedium,
		Category:     "evidence_gap",
		Confidence:   "medium",
		Quality:      reviewFindingQualityWeak,
		Path:         finding.Path,
		Line:         finding.Line,
		Symbol:       finding.Symbol,
		Title:        "Reviewer output omitted part of a finding",
		Evidence:     "The reviewer returned an ellipsis or truncation marker inside a structured finding field, so the finding is not precise enough to treat as an actionable code issue.",
		Impact:       "Applying or blocking on an omitted finding can send the repair flow in the wrong direction.",
		RequiredFix:  "Rerun the review with a narrower target or a stronger reviewer model; do not make code changes based only on the omitted finding.",
		BlocksGate:   false,
	}
}

func reviewFindingsContainOmittedOutputPlaceholder(findings []ReviewFinding) bool {
	for _, finding := range findings {
		if reviewFindingIsOmittedOutputPlaceholder(finding) {
			return true
		}
	}
	return false
}

func reviewFindingIsOmittedOutputPlaceholder(finding ReviewFinding) bool {
	if !strings.EqualFold(strings.TrimSpace(finding.Category), "evidence_gap") {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(finding.Quality), reviewFindingQualityWeak) {
		return false
	}
	title := strings.TrimSpace(finding.Title)
	return title == "Reviewer output omitted part of a finding" ||
		title == "리뷰어 출력이 finding 일부를 생략함"
}

func reviewSalvageOmittedFinding(finding ReviewFinding, korean bool) (ReviewFinding, bool) {
	salvaged := finding
	salvaged.Title = reviewRemoveOmissionMarkers(salvaged.Title)
	salvaged.Evidence = reviewRemoveOmissionMarkers(salvaged.Evidence)
	salvaged.Impact = reviewRemoveOmissionMarkers(salvaged.Impact)
	salvaged.RequiredFix = reviewRemoveOmissionMarkers(salvaged.RequiredFix)
	salvaged.TestRecommendation = reviewRemoveOmissionMarkers(salvaged.TestRecommendation)
	salvaged.RawExcerpt = ""
	if reviewFindingHasOmissionMarker(salvaged) {
		return ReviewFinding{}, false
	}
	if strings.TrimSpace(salvaged.Title) == "" {
		salvaged.Title = reviewShortTextNoOmissionMarker(firstNonBlankString(salvaged.Evidence, salvaged.Impact, salvaged.RequiredFix), 120)
	}
	if strings.TrimSpace(salvaged.Title) == "" {
		return ReviewFinding{}, false
	}
	if strings.TrimSpace(salvaged.Evidence) == "" && strings.TrimSpace(salvaged.RequiredFix) == "" && strings.TrimSpace(salvaged.Impact) == "" {
		return ReviewFinding{}, false
	}
	if strings.EqualFold(salvaged.Severity, reviewSeverityBlocker) || strings.EqualFold(salvaged.Severity, reviewSeverityHigh) {
		salvaged.Severity = reviewSeverityMedium
	}
	salvaged.Confidence = "low"
	salvaged.Quality = reviewFindingQualityPartial
	salvaged.BlocksGate = false
	if strings.TrimSpace(salvaged.Impact) == "" {
		if korean {
			salvaged.Impact = "원본 리뷰어 출력의 일부 문장이 생략 표식을 포함했지만 남은 근거는 확인할 가치가 있습니다."
		} else {
			salvaged.Impact = "The original reviewer output contained an omission marker, but the remaining evidence is still worth inspecting."
		}
	}
	if strings.TrimSpace(salvaged.RequiredFix) == "" {
		if korean {
			salvaged.RequiredFix = "표시된 근거를 기준으로 코드를 직접 확인하고, 같은 범위를 더 좁혀 재리뷰하십시오."
		} else {
			salvaged.RequiredFix = "Inspect the cited evidence directly and rerun review with a narrower target."
		}
	}
	salvaged.Normalize()
	salvaged.Quality = reviewFindingQualityPartial
	salvaged.Confidence = "low"
	salvaged.BlocksGate = false
	return salvaged, true
}

func reviewRemoveOmissionMarkers(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	replacements := []struct {
		old string
		new string
	}{
		{"...", ""},
		{"…", ""},
		{"(truncated)", ""},
		{"[truncated]", ""},
		{"(omitted)", ""},
		{"[omitted]", ""},
		{"omitted for brevity", ""},
		{"content omitted", ""},
		{"details omitted", ""},
		{"output omitted", ""},
		{"omitted part", ""},
	}
	for _, replacement := range replacements {
		text = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(replacement.old)).ReplaceAllString(text, replacement.new)
	}
	text = strings.Join(strings.Fields(text), " ")
	return strings.TrimSpace(strings.Trim(text, "-:;,. "))
}

func reviewShortTextNoOmissionMarker(text string, limit int) string {
	text = reviewRemoveOmissionMarkers(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	var b strings.Builder
	for _, r := range text {
		if b.Len()+len(string(r)) > limit {
			break
		}
		b.WriteRune(r)
	}
	shortened := strings.TrimSpace(b.String())
	if shortened == "" {
		return ""
	}
	return shortened + " [shortened]"
}

func reviewFindingHasOmissionMarker(finding ReviewFinding) bool {
	return reviewTextHasOmissionMarker(finding.Title) ||
		reviewTextHasOmissionMarker(finding.Evidence) ||
		reviewTextHasOmissionMarker(finding.Impact) ||
		reviewTextHasOmissionMarker(finding.RequiredFix) ||
		reviewTextHasOmissionMarker(finding.TestRecommendation)
}

func reviewTextHasOmissionMarker(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "...") ||
		strings.Contains(lower, "…") ||
		strings.Contains(lower, "(truncated)") ||
		strings.Contains(lower, "[truncated]") ||
		strings.Contains(lower, "truncated by") ||
		strings.Contains(lower, "omitted for brevity") ||
		strings.Contains(lower, "(omitted)") ||
		strings.Contains(lower, "[omitted]") ||
		strings.Contains(lower, "content omitted") ||
		strings.Contains(lower, "details omitted") ||
		strings.Contains(lower, "output omitted") ||
		strings.Contains(lower, "omitted part")
}

func reviewFallbackFindingTitle(line string, severityRe *regexp.Regexp) string {
	line = strings.TrimSpace(line)
	line = regexp.MustCompile(`^\[[^\]]+\]\s*`).ReplaceAllString(line, "")
	if severityRe != nil {
		if loc := severityRe.FindStringIndex(line); loc != nil {
			rest := strings.TrimSpace(line[loc[1]:])
			rest = strings.TrimLeft(rest, ":-] \t")
			if rest != "" {
				return rest
			}
		}
	}
	if parts := strings.SplitN(line, ":", 2); len(parts) == 2 && len(parts[0]) <= 48 {
		return strings.TrimSpace(parts[1])
	}
	return line
}

func cleanReviewModelFieldValue(value string) string {
	value = strings.TrimSpace(value)
	for {
		if len(value) < 2 {
			return value
		}
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' && last == '"') ||
			(first == '\'' && last == '\'') ||
			(first == '`' && last == '`') {
			value = strings.TrimSpace(value[1 : len(value)-1])
			continue
		}
		return value
	}
}

func filepathSlash(path string) string {
	return strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
}

func reviewFindingHasContent(f ReviewFinding) bool {
	return strings.TrimSpace(f.Title) != "" ||
		strings.TrimSpace(f.Evidence) != "" ||
		strings.TrimSpace(f.Impact) != "" ||
		strings.TrimSpace(f.RequiredFix) != "" ||
		strings.TrimSpace(f.Path) != "" ||
		strings.TrimSpace(f.Symbol) != "" ||
		strings.TrimSpace(f.TestRecommendation) != ""
}

func reviewChangedPathsDocsOnly(paths []string) bool {
	sawPath := false
	for _, path := range paths {
		path = strings.ToLower(filepathSlash(path))
		if path == "" {
			continue
		}
		sawPath = true
		base := strings.ToLower(filepath.Base(path))
		ext := strings.ToLower(filepath.Ext(path))
		if strings.HasPrefix(path, "docs/") ||
			strings.HasPrefix(path, "doc/") ||
			strings.HasPrefix(path, ".github/") ||
			strings.HasPrefix(base, "readme") ||
			base == "license" ||
			base == "changelog" ||
			ext == ".md" ||
			ext == ".mdx" ||
			ext == ".txt" ||
			ext == ".rst" ||
			ext == ".adoc" {
			continue
		}
		return false
	}
	return sawPath
}
