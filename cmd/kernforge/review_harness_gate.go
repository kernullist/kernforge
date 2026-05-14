package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

func deterministicReviewFindings(rt *runtimeState, run ReviewRun) []ReviewFinding {
	var findings []ReviewFinding
	preWrite := strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write")
	if reviewScopeDiscoveryNeedsNarrowing(run.RequestAnalysis.ScopeDiscovery) {
		findings = append(findings, ReviewFinding{
			Source:             "deterministic",
			ReviewerRole:       "scope_discovery",
			Severity:           reviewSeverityMedium,
			Category:           "evidence_gap",
			Confidence:         "medium",
			Quality:            reviewFindingQualityComplete,
			Title:              "Review scope needs narrowing",
			Evidence:           reviewScopeDiscoveryFindingEvidence(run.RequestAnalysis.ScopeDiscovery),
			Impact:             "A broad review can miss the exact bug surface or produce generic findings.",
			RequiredFix:        "Rerun review with a focused path, symbol, selection, or one of the suggested narrowing commands.",
			TestRecommendation: reviewScopePreferredNarrowingCommand(run.RequestAnalysis.ScopeDiscovery),
			BlocksGate:         false,
		})
	}
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
		findings = append(findings, deterministicPreWriteRepairProposalFindings(run)...)
		// Pre-write review gates the proposed diff before it is applied. Runtime
		// verification is still expected after the edit, so missing verification
		// evidence must not block the pre-write gate.
		if run.SingleModelPolicy.Enabled {
			if !singleModelPreWriteHasFrozenDiff(run) {
				findings = append(findings, ReviewFinding{
					Source:       "deterministic",
					ReviewerRole: "single_model_policy",
					Severity:     reviewSeverityBlocker,
					Category:     "evidence_gap",
					Confidence:   "high",
					Quality:      reviewFindingQualityComplete,
					Title:        "Single-model pre-write review lacks a frozen diff",
					Evidence:     "Single-model mode cannot independently re-check a write without a frozen diff or edit proposal fingerprint.",
					Impact:       "The same model could approve a moving patch instead of the exact edit that will be shown in diff preview.",
					RequiredFix:  "Create a frozen edit proposal with diff preview evidence, then rerun the pre-write review.",
					BlocksGate:   true,
				})
			}
		}
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

func deterministicPreWriteRepairProposalFindings(run ReviewRun) []ReviewFinding {
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") || len(run.RepairFindings) == 0 {
		return nil
	}
	proposalText := preWriteProposalAlignmentText(run)
	if strings.TrimSpace(proposalText) == "" {
		return nil
	}
	includeOnly := proposalLooksIncludeOnly(proposalText)
	var findings []ReviewFinding
	for _, repair := range run.RepairFindings {
		repair.Normalize()
		if !repairFindingNeedsProposalAlignment(repair) {
			continue
		}
		if includeOnly && !repairFindingAllowsIncludeOnlyProposal(repair) {
			findings = append(findings, preWriteUnalignedRepairFinding(repair, "The proposed edit appears to only modify #include lines."))
			continue
		}
		if repairFindingProposalAligned(repair, proposalText) {
			continue
		}
		findings = append(findings, preWriteUnalignedRepairFinding(repair, "The proposed edit does not contain enough code tokens from the required repair finding."))
	}
	return limitReviewFindings(findings, 4)
}

func preWriteProposalAlignmentText(run ReviewRun) string {
	var parts []string
	if strings.TrimSpace(run.ChangeSet.DiffExcerpt) != "" {
		parts = append(parts, run.ChangeSet.DiffExcerpt)
	}
	if proposalText := renderEditProposalsForEvidence(run.EditProposals); strings.TrimSpace(proposalText) != "" {
		parts = append(parts, proposalText)
	}
	return strings.Join(parts, "\n")
}

func repairFindingNeedsProposalAlignment(finding ReviewFinding) bool {
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(finding.ID)), "RF-REVIEWER") {
		return false
	}
	if strings.EqualFold(finding.Category, "evidence_gap") ||
		strings.EqualFold(finding.Category, "test_gap") {
		return false
	}
	return finding.BlocksGate || reviewSeverityRank(finding.Severity) <= reviewSeverityRank(reviewSeverityMedium)
}

func repairFindingAllowsIncludeOnlyProposal(finding ReviewFinding) bool {
	text := strings.ToLower(strings.Join([]string{
		finding.Title,
		finding.Evidence,
		finding.RequiredFix,
		finding.TestRecommendation,
	}, " "))
	if strings.Contains(text, "#include") || strings.Contains(text, "missing include") ||
		strings.Contains(text, "include directive") || strings.Contains(text, "header include") {
		return true
	}
	return false
}

func proposalLooksIncludeOnly(text string) bool {
	changed := 0
	includeChanges := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < 2 {
			continue
		}
		if strings.HasPrefix(trimmed, "+++") || strings.HasPrefix(trimmed, "---") {
			continue
		}
		if trimmed[0] != '+' && trimmed[0] != '-' {
			continue
		}
		body := strings.TrimSpace(trimmed[1:])
		if body == "" {
			continue
		}
		changed++
		if strings.HasPrefix(body, "#include") {
			includeChanges++
		}
	}
	return changed > 0 && changed == includeChanges
}

func repairFindingProposalAligned(finding ReviewFinding, proposalText string) bool {
	tokens := repairFindingAlignmentTokens(finding)
	if len(tokens) == 0 {
		return true
	}
	lowerProposal := strings.ToLower(proposalText)
	matches := 0
	for _, token := range tokens {
		if strings.Contains(lowerProposal, strings.ToLower(token)) {
			matches++
			if matches >= 2 {
				return true
			}
		}
	}
	return false
}

func repairFindingAlignmentTokens(finding ReviewFinding) []string {
	text := strings.Join([]string{
		finding.Symbol,
		finding.Title,
		finding.Evidence,
		finding.RequiredFix,
		finding.TestRecommendation,
	}, " ")
	tokenMap := map[string]string{}
	backtickRe := regexp.MustCompile("`([^`]{1,160})`")
	for _, match := range backtickRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		addRepairFindingAlignmentTokens(tokenMap, match[1])
	}
	addRepairFindingAlignmentTokens(tokenMap, text)
	out := make([]string, 0, len(tokenMap))
	for _, token := range tokenMap {
		out = append(out, token)
	}
	sort.Strings(out)
	if len(out) > 16 {
		out = out[:16]
	}
	return out
}

func addRepairFindingAlignmentTokens(out map[string]string, text string) {
	identifierRe := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{2,}`)
	for _, token := range identifierRe.FindAllString(text, -1) {
		if !repairAlignmentTokenUseful(token) {
			continue
		}
		lower := strings.ToLower(token)
		if _, exists := out[lower]; !exists {
			out[lower] = token
		}
	}
}

func repairAlignmentTokenUseful(token string) bool {
	lower := strings.ToLower(strings.TrimSpace(token))
	if len(lower) < 4 && lower != "len" {
		return false
	}
	switch lower {
	case "the", "and", "for", "with", "from", "this", "that", "when", "then",
		"code", "path", "file", "line", "lines", "current", "required",
		"repair", "finding", "proposed", "preview", "after", "before",
		"error", "failed", "failure", "success", "handle", "check", "fix",
		"uses", "using", "should", "could", "would", "must", "only",
		"each", "existing", "source", "target", "change", "changes":
		return false
	default:
		return true
	}
}

func preWriteUnalignedRepairFinding(repair ReviewFinding, reason string) ReviewFinding {
	title := "Proposed edit does not address a required repair finding"
	repairID := valueOrDefault(strings.TrimSpace(repair.ID), "required repair")
	expected := strings.Join(limitStrings(repairFindingAlignmentTokens(repair), 8), ", ")
	evidence := fmt.Sprintf("%s %s asks for: %s", reason, repairID, compactPromptSection(repair.Title, 240))
	if expected != "" {
		evidence += fmt.Sprintf(" Expected code evidence tokens include: %s.", expected)
	}
	return ReviewFinding{
		Source:       "deterministic",
		ReviewerRole: "proposal_alignment",
		Severity:     reviewSeverityBlocker,
		Category:     "correctness",
		Confidence:   "high",
		Quality:      reviewFindingQualityComplete,
		Path:         repair.Path,
		Symbol:       repair.Symbol,
		Title:        title,
		Evidence:     evidence,
		Impact:       "The repair loop can continue with an unrelated patch while the original blocker remains unresolved.",
		RequiredFix:  "Discard the unrelated proposal and create a narrow patch that touches the code path and identifiers cited by the required repair finding.",
		BlocksGate:   true,
		FixRefs:      []string{repairID},
	}
}

func singleModelPreWritePolicyFindings(run ReviewRun) []ReviewFinding {
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") || !run.SingleModelPolicy.Enabled {
		return nil
	}
	if reviewRunHasRequiredReviewerFailure(run) {
		return nil
	}
	if !run.SingleModelPolicy.RequiresRFObligationStatus || repairFindingsHaveResolutionStatus(run.RepairFindings) {
		return nil
	}
	findings := []ReviewFinding{{
		Source:       "deterministic",
		ReviewerRole: "single_model_policy",
		Severity:     reviewSeverityBlocker,
		Category:     "evidence_gap",
		Confidence:   "high",
		Quality:      reviewFindingQualityComplete,
		Title:        "Single-model pre-write review lacks repair obligation status",
		Evidence:     "Required repair findings from the pre-fix review do not all record resolved, partial, unresolved, or verification_needed status.",
		Impact:       "A single-model pre-write approval could hide an unresolved pre-fix obligation.",
		RequiredFix:  "Record a resolution_status for each required repair finding before approving the single-model pre-write review.",
		BlocksGate:   true,
	}}
	assignReviewFindingIDs(findings)
	return findings
}

func annotateSingleModelPreWriteRepairStatuses(run *ReviewRun) {
	if run == nil ||
		!strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") ||
		!run.SingleModelPolicy.Enabled ||
		len(run.RepairFindings) == 0 ||
		!reviewRunHasUsableMainReviewer(*run) {
		return
	}
	for i := range run.RepairFindings {
		if reviewRepairResolutionStatusKnown(run.RepairFindings[i].ResolutionStatus) {
			continue
		}
		run.RepairFindings[i].ResolutionStatus = inferSingleModelPreWriteRepairStatus(*run, run.RepairFindings[i])
	}
}

func inferSingleModelPreWriteRepairStatus(run ReviewRun, repair ReviewFinding) string {
	for _, finding := range run.Findings {
		if !strings.EqualFold(strings.TrimSpace(finding.Source), "model") {
			continue
		}
		if !reviewFindingReferencesRepairFinding(finding, repair) {
			continue
		}
		if strings.EqualFold(finding.Category, "test_gap") {
			return "verification_needed"
		}
		if strings.EqualFold(finding.Category, "evidence_gap") {
			return "partial"
		}
		if reviewSeverityRank(finding.Severity) <= reviewSeverityRank(reviewSeverityMedium) ||
			strings.EqualFold(finding.Severity, reviewSeverityBlocker) {
			return "unresolved"
		}
		return "partial"
	}
	return "resolved"
}

func reviewFindingReferencesRepairFinding(finding ReviewFinding, repair ReviewFinding) bool {
	needles := []string{
		strings.TrimSpace(repair.ID),
		strings.TrimSpace(repair.Title),
	}
	haystack := strings.ToLower(strings.Join([]string{
		finding.Title,
		finding.Evidence,
		finding.Impact,
		finding.RequiredFix,
		finding.TestRecommendation,
	}, " "))
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle == "" {
			continue
		}
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func singleModelPreWriteHasFrozenDiff(run ReviewRun) bool {
	if strings.TrimSpace(run.ChangeSet.DiffExcerpt) != "" {
		return true
	}
	for _, proposal := range run.EditProposals {
		if strings.TrimSpace(proposal.PreviewFingerprint) != "" ||
			strings.TrimSpace(proposal.ExpectedPreview) != "" ||
			strings.TrimSpace(proposal.ExactSearch) != "" ||
			strings.TrimSpace(proposal.Replacement) != "" {
			return true
		}
	}
	return false
}

func repairFindingsHaveResolutionStatus(findings []ReviewFinding) bool {
	if len(findings) == 0 {
		return true
	}
	for _, finding := range findings {
		if !reviewRepairResolutionStatusKnown(finding.ResolutionStatus) {
			return false
		}
	}
	return true
}

func reviewRepairResolutionStatusKnown(status string) bool {
	switch normalizeReviewRepairResolutionStatus(status) {
	case "resolved", "partial", "unresolved", "verification_needed":
		return true
	default:
		return false
	}
}

func normalizeReviewRepairResolutionStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(status, "-", "_")))
	switch status {
	case "verification_needed_only":
		return "verification_needed"
	default:
		return status
	}
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
	f.ResolutionStatus = normalizeReviewRepairResolutionStatus(f.ResolutionStatus)
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
		f.Title = synthesizeReviewFindingTitle(*f)
	}
	if f.Severity == reviewSeverityBlocker || (f.Severity == reviewSeverityHigh && f.Quality == reviewFindingQualityComplete) {
		f.BlocksGate = f.BlocksGate || f.Severity == reviewSeverityBlocker
	}
}

func synthesizeReviewFindingTitle(f ReviewFinding) string {
	if strings.TrimSpace(firstNonBlankString(f.Path, f.Symbol, f.Evidence, f.Impact, f.RequiredFix, f.TestRecommendation, f.RawExcerpt)) == "" {
		return ""
	}
	category := strings.TrimSpace(strings.ReplaceAll(f.Category, "_", " "))
	pathBase := ""
	if strings.TrimSpace(f.Path) != "" {
		pathBase = filepath.Base(filepath.ToSlash(f.Path))
	}
	subject := strings.TrimSpace(firstNonBlankString(f.Symbol, pathBase))
	if subject != "" && category != "" {
		return strings.TrimSpace(category + " finding in " + subject)
	}
	if subject != "" {
		return "Review finding in " + subject
	}
	if category != "" {
		return strings.TrimSpace(category + " review finding")
	}
	return "Model reviewer finding"
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
	run.Gate = gate
	gate.Action = reviewGateActionForRun(run)
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
	if strings.EqualFold(strings.TrimSpace(finding.Source), "model") &&
		(strings.EqualFold(finding.Quality, reviewFindingQualityWeak) ||
			strings.EqualFold(finding.Quality, reviewFindingQualityInvalid)) {
		return false
	}
	if finding.BlocksGate || strings.EqualFold(finding.Severity, reviewSeverityBlocker) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(finding.Source), "deterministic") &&
		strings.EqualFold(strings.TrimSpace(finding.ReviewerRole), "coding_harness") {
		return false
	}
	if reviewRunLooksReadOnlyAnalysis(run) {
		return false
	}
	if strings.EqualFold(finding.Category, "evidence_gap") ||
		strings.EqualFold(finding.Category, "test_gap") {
		return false
	}
	if reviewFindingLooksAdvisoryStyleCategory(finding) {
		return false
	}
	if reviewRunLooksExplicitRepairIntent(run) &&
		reviewSeverityRank(finding.Severity) <= reviewSeverityRank(reviewSeverityMedium) &&
		reviewFindingLooksActionableForRepairGate(finding) {
		return true
	}
	if !strings.EqualFold(finding.Severity, reviewSeverityHigh) {
		return false
	}
	if reviewRunLooksExplicitRepairIntent(run) {
		return true
	}
	if strings.EqualFold(finding.Category, "security") ||
		strings.EqualFold(finding.Category, "bypass_surface") ||
		strings.EqualFold(finding.Category, "credential_leak") {
		return true
	}
	for _, pack := range run.PolicyPacks {
		if strings.EqualFold(pack, "windows_kernel_driver") ||
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
	if reviewScopeDiscoveryNeedsNarrowing(run.RequestAnalysis.ScopeDiscovery) {
		out = append(out, ReviewNextCommand{
			ID:             "narrow-review",
			Command:        reviewScopePreferredNarrowingCommand(run.RequestAnalysis.ScopeDiscovery),
			Reason:         "deterministic scope discovery marked the review target as broad",
			Safety:         "read_only",
			When:           "before relying on model findings as complete",
			AutoRun:        false,
			ClientHint:     "Narrow the review to a path, symbol, selection, or search result, then repeat /review.",
			ExpectedResult: "A focused review run is created with concrete candidate files or symbols.",
		})
	}
	if strings.TrimSpace(run.Evidence.VerificationSummary) == "" && reviewRunHasChangeEvidence(run) && run.Target != reviewTargetPlan {
		out = append(out, ReviewNextCommand{
			ID:             "verify",
			Command:        "/verify --full",
			Reason:         "changed files have no latest verification evidence",
			Safety:         "safe_local",
			When:           "before completion or git write",
			AutoRun:        false,
			ClientHint:     "Run verification, then repeat /review.",
			ExpectedResult: "A current verification report is recorded for the changed files.",
		})
	}
	if reviewRunHasRequiredReviewerFailure(run) {
		expected := "The failed review route is changed or the user explicitly chooses main-model fallback before any write is attempted."
		hint := "If primary failed, change the active main model with /model or fix that provider route. If cross/dedicated reviewer failed, fix that route with /review models. Retry with a longer timeout only after the route is healthy."
		if reviewRunHasUsableMainReviewer(run) {
			hint = "The main review is usable. In an interactive edit flow, explicitly say that the main model review may be used for this pre-write fallback before diff preview."
		}
		out = append(out, ReviewNextCommand{
			ID:                   "reviewer-fallback",
			Command:              "/review models status",
			Reason:               "a required review route failed or returned weak output",
			Safety:               "read_only",
			When:                 "before retrying the edit or approving a write",
			AutoRun:              false,
			RequiresConfirmation: true,
			ClientHint:           hint,
			ExpectedResult:       expected,
		})
	}
	if gate.Verdict == reviewVerdictNeedsRevision || gate.Verdict == reviewVerdictBlocked {
		reason := "blocking findings need a focused repair pass"
		when := "after reading review findings"
		hint := "Use the repair prompt in the review artifact."
		expected := "The latest review blockers are converted into a focused repair turn."
		if reviewRunLooksReadOnlyAnalysis(run) {
			reason = "blocking findings were found; repair is optional unless the user asks to fix them"
			when = "if you decide to turn the analysis into a repair pass"
			hint = "Say `수정해줘` or run this command only if you want Kernforge to fix the findings."
			expected = "The latest review blockers are converted into repair guidance only after an explicit repair request."
		}
		out = append(out, ReviewNextCommand{
			ID:             "repair",
			Command:        "/continuity continue from review",
			Reason:         reason,
			Safety:         "safe_local",
			When:           when,
			AutoRun:        false,
			ClientHint:     hint,
			ExpectedResult: expected,
		})
	}
	if gate.Verdict == reviewVerdictApprovedWithWarnings && reviewHasActionableWarningFindings(run, gate.WarningFindings) {
		reason := "warning findings are actionable and can be fixed now"
		when := "if you want to address the warnings instead of accepting them"
		hint := "Say `수정해줘` or run this command to repair the latest review warnings."
		expected := "Actionable warning findings are queued as repair guidance."
		if reviewRunLooksReadOnlyAnalysis(run) {
			reason = "analysis findings are actionable, but repair is optional unless the user asks to fix them"
			when = "if you decide to turn the analysis into a repair pass"
			hint = "Say `수정해줘` or run this command only if you want Kernforge to fix the analysis findings."
			expected = "The latest analysis findings are converted into repair guidance only after an explicit repair request."
		}
		out = append(out, ReviewNextCommand{
			ID:             "repair-warnings",
			Command:        "/continuity continue from review",
			Reason:         reason,
			Safety:         "safe_local",
			When:           when,
			AutoRun:        false,
			ClientHint:     hint,
			ExpectedResult: expected,
		})
	}
	if gate.Verdict == reviewVerdictApprovedWithWarnings {
		out = append(out, ReviewNextCommand{
			ID:             "completion-audit",
			Command:        "/completion-audit",
			Reason:         "warnings remain; completion audit can validate final readiness",
			Safety:         "read_only",
			When:           "before final answer",
			AutoRun:        false,
			ClientHint:     "Check final readiness before claiming completion.",
			ExpectedResult: "Completion readiness is evaluated with warnings still visible.",
		})
	}
	if strings.Contains(strings.Join(run.ModelPlan.MissingRoles, ","), "security_reviewer") || reviewHasSecurityFinding(run.Findings) {
		out = append(out, ReviewNextCommand{
			ID:             "set-security-model",
			Command:        "/review models security",
			Reason:         "security-sensitive review is using a fallback reviewer",
			Safety:         "read_only",
			When:           "before future security reviews",
			AutoRun:        false,
			ClientHint:     "Configure a dedicated security reviewer model.",
			ExpectedResult: "Future security reviews use a dedicated reviewer route.",
		})
	}
	if strings.Contains(strings.Join(run.ModelPlan.MissingRoles, ","), "false_positive_reviewer") {
		out = append(out, ReviewNextCommand{
			ID:             "set-false-positive-model",
			Command:        "/review models false-positive",
			Reason:         "anti-cheat or detection review is using a fallback false-positive reviewer",
			Safety:         "read_only",
			When:           "before future security reviews",
			AutoRun:        false,
			ClientHint:     "Configure a dedicated false-positive reviewer model.",
			ExpectedResult: "Future detection reviews include a dedicated false-positive reviewer route.",
		})
	}
	return out
}

func reviewRunLooksReadOnlyAnalysis(run ReviewRun) bool {
	return prefersReadOnlyAnalysisIntent(run.Objective) && !looksLikeExplicitEditIntent(run.Objective)
}

func reviewRunLooksExplicitRepairIntent(run ReviewRun) bool {
	if strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) ||
		strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(run.Mode), reviewModeLiveFix) {
		return true
	}
	return looksLikeExplicitEditIntent(run.Objective)
}

func reviewFindingLooksActionableForRepairGate(finding ReviewFinding) bool {
	finding.Normalize()
	if reviewFindingLooksAdvisoryStyleCategory(finding) {
		return false
	}
	if strings.TrimSpace(finding.RequiredFix) != "" {
		return true
	}
	if strings.TrimSpace(finding.Path) != "" || strings.TrimSpace(finding.Symbol) != "" {
		return true
	}
	return false
}

func reviewFindingLooksAdvisoryStyleCategory(finding ReviewFinding) bool {
	finding.Normalize()
	return strings.EqualFold(finding.Category, "style") ||
		strings.EqualFold(finding.Category, "formatting") ||
		strings.EqualFold(finding.Category, "maintainability")
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
	var warnings []ReviewFinding
	korean := reviewRunPrefersKoreanFromRequest(run)
	blockingIDs := reviewFindingIDSet(run.Gate.BlockingFindings)
	warningIDs := reviewFindingIDSet(run.Gate.WarningFindings)
	hasGateClassification := len(blockingIDs) > 0 || len(warningIDs) > 0
	for _, finding := range run.Findings {
		finding.Normalize()
		if hasGateClassification {
			if blockingIDs[finding.ID] {
				blocking = append(blocking, finding)
				continue
			}
			if warningIDs[finding.ID] && reviewFindingShouldBeRepairPlanWarning(finding) {
				warnings = append(warnings, finding)
			}
			continue
		}
		if reviewFindingBlocksGate(run, finding) {
			blocking = append(blocking, finding)
			continue
		}
		if reviewFindingShouldBeRepairPlanWarning(finding) {
			warnings = append(warnings, finding)
		}
	}
	if len(blocking) == 0 {
		return ReviewRepairPlan{}
	}
	var b strings.Builder
	if korean {
		b.WriteString("리뷰 차단 항목을 범위 확장 없이 수정하세요.\n\n")
	} else {
		b.WriteString("Repair the review blockers without broadening scope.\n\n")
	}
	if strings.TrimSpace(run.Objective) != "" {
		if korean {
			b.WriteString("원래 요청:\n")
		} else {
			b.WriteString("Objective:\n")
		}
		b.WriteString(run.Objective)
		b.WriteString("\n\n")
	}
	b.WriteString(reviewRepairPatchConstructionGuidance(blocking, warnings, korean))
	b.WriteString("\n\n")
	if korean {
		b.WriteString("차단 finding:\n")
	} else {
		b.WriteString("Blocking findings:\n")
	}
	var ids []string
	var actions []string
	for _, finding := range blocking {
		ids = append(ids, finding.ID)
		actions = append(actions, finding.RequiredFix)
		writeReviewRepairPlanFinding(&b, finding, korean)
	}
	if len(warnings) > 0 {
		if korean {
			b.WriteString("\n반드시 함께 처리할 medium 이상 실행 가능 경고:\n")
		} else {
			b.WriteString("\nMedium-or-higher actionable warnings that must also be handled:\n")
		}
		for _, finding := range warnings {
			ids = append(ids, finding.ID)
			actions = append(actions, finding.RequiredFix)
			writeReviewRepairPlanFinding(&b, finding, korean)
		}
	}
	if korean {
		b.WriteString("\n차단 finding이 명시적으로 요구하지 않는 한 관련 없는 정리, 의존성 업그레이드, 대규모 리팩터링은 하지 마세요.")
	} else {
		b.WriteString("\nDo not do unrelated cleanup, dependency upgrades, or large refactors unless a blocking finding explicitly requires it.")
	}
	b.WriteString("\n")
	b.WriteString(reviewNarrowPatchGuidance(korean))
	return ReviewRepairPlan{
		Required:        true,
		Prompt:          b.String(),
		Findings:        ids,
		RequiredActions: normalizeTaskStateList(actions, 12),
	}
}

func writeReviewRepairPlanFinding(b *strings.Builder, finding ReviewFinding, korean bool) {
	fmt.Fprintf(b, "- %s [%s/%s] %s\n", finding.ID, finding.Severity, finding.Category, finding.Title)
	if strings.TrimSpace(finding.Evidence) != "" {
		if korean {
			fmt.Fprintf(b, "  근거: %s\n", compactPromptSection(finding.Evidence, 350))
		} else {
			fmt.Fprintf(b, "  Evidence: %s\n", compactPromptSection(finding.Evidence, 350))
		}
	}
	if strings.TrimSpace(finding.RequiredFix) != "" {
		if korean {
			fmt.Fprintf(b, "  필요한 수정: %s\n", compactPromptSection(localizedReviewRequiredFixText(finding.RequiredFix, true), 350))
		} else {
			fmt.Fprintf(b, "  Required fix: %s\n", compactPromptSection(finding.RequiredFix, 350))
		}
	}
	if strings.TrimSpace(finding.TestRecommendation) != "" {
		if korean {
			fmt.Fprintf(b, "  검증: %s\n", finding.TestRecommendation)
		} else {
			fmt.Fprintf(b, "  Verification: %s\n", finding.TestRecommendation)
		}
	}
}

func reviewRepairPatchConstructionGuidance(blocking []ReviewFinding, warnings []ReviewFinding, korean bool) string {
	required := append([]ReviewFinding(nil), blocking...)
	required = append(required, warnings...)
	if len(required) == 0 {
		return ""
	}
	var b strings.Builder
	if korean {
		b.WriteString("patch 작성 원칙:\n")
		b.WriteString("- pre-write gate는 부분 수리를 승인하지 않습니다. 이번 edit proposal은 아래 필수 RF를 모두 해결해야 합니다.\n")
		b.WriteString("- 단, 하나의 큰 hunk나 함수/파일 전체 rewrite로 합치지 말고, RF별로 현재 파일에서 방금 확인한 snippet에 고정된 독립 hunk를 작성하세요.\n")
		b.WriteString("- 서로 다른 함수나 위치를 고칠 때는 같은 proposal 안에서도 hunk를 분리하고, 기존 함수 종료부/중괄호를 새 위치에 중복 삽입하지 마세요.\n")
		b.WriteString("- 필요한 target snippet이 현재 context에 없으면 patch를 추측하지 말고 해당 함수 범위를 전용 파일 읽기 도구로 먼저 확인하세요.\n")
		b.WriteString("필수 RF 처리 순서:\n")
	} else {
		b.WriteString("Patch construction rules:\n")
		b.WriteString("- The pre-write gate does not approve partial repairs. This edit proposal must address every required RF below.\n")
		b.WriteString("- Still, do not merge the fixes into one large hunk or whole-file/function rewrite. Use separate narrow hunks anchored to snippets just verified in the current file contents.\n")
		b.WriteString("- When fixes touch different functions or locations, keep the hunks separate in the same proposal and do not duplicate existing function endings or braces in a new location.\n")
		b.WriteString("- If a required target snippet is not in current context, do not guess the patch; inspect that exact function range with a dedicated file-read tool first.\n")
		b.WriteString("Required RF order:\n")
	}
	for _, finding := range required {
		id := strings.TrimSpace(finding.ID)
		if id == "" {
			id = "RF"
		}
		target := firstNonBlankString(strings.TrimSpace(finding.Symbol), strings.TrimSpace(finding.Path), strings.TrimSpace(finding.Title))
		if target == "" {
			target = strings.TrimSpace(finding.Title)
		}
		if target == "" {
			target = "review finding"
		}
		fmt.Fprintf(&b, "- %s: %s\n", id, compactPromptSection(target, 140))
	}
	return strings.TrimSpace(b.String())
}

func reviewFindingShouldBeRepairPlanWarning(finding ReviewFinding) bool {
	finding.Normalize()
	if reviewSeverityRank(finding.Severity) > reviewSeverityRank(reviewSeverityMedium) {
		return false
	}
	if strings.EqualFold(finding.Category, "test_gap") ||
		strings.EqualFold(finding.Category, "evidence_gap") {
		return false
	}
	return strings.TrimSpace(finding.RequiredFix) != "" ||
		strings.TrimSpace(finding.Path) != "" ||
		strings.TrimSpace(finding.Symbol) != "" ||
		strings.TrimSpace(finding.Title) != ""
}

func reviewResultSummary(run ReviewRun) string {
	return reviewResultSummaryForLanguage(run, reviewRunPrefersKoreanFromRequest(run))
}

func reviewResultSummaryForConfig(cfg Config, run ReviewRun) string {
	return reviewResultSummaryForLanguage(run, reviewRunPrefersKorean(cfg, run))
}

func reviewResultSummaryForLanguage(run ReviewRun, korean bool) string {
	switch run.Gate.Verdict {
	case reviewVerdictApproved:
		if korean {
			return "차단 finding 없이 리뷰가 승인되었습니다."
		}
		return "Review approved with no blocking findings."
	case reviewVerdictApprovedWithWarnings:
		if korean {
			return fmt.Sprintf("리뷰가 경고와 함께 승인되었습니다: 경고 finding %d개.", len(run.Gate.WarningFindings))
		}
		return fmt.Sprintf("Review approved with warnings: %d warning finding(s).", len(run.Gate.WarningFindings))
	case reviewVerdictNeedsRevision:
		if korean {
			return fmt.Sprintf("리뷰가 수정을 요구합니다: 차단 finding %d개.", len(run.Gate.BlockingFindings))
		}
		return fmt.Sprintf("Review needs revision: %d blocking finding(s).", len(run.Gate.BlockingFindings))
	case reviewVerdictInsufficientEvidence:
		if korean {
			return "리뷰 승인에 필요한 근거가 부족합니다."
		}
		return "Review has insufficient evidence for approval."
	default:
		if korean {
			return "리뷰가 차단되었습니다."
		}
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
	truncatedStructuredTail := reviewRawLooksIncompleteStructuredTail(raw)
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
			normalizeModelReviewFindingForGate(&current, korean)
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
			reviewLineIsEmptyFindingsMarker(lower) ||
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
	if truncatedStructuredTail {
		findings = append(findings, reviewTruncatedTailFindingPlaceholder(role, korean))
	}
	assignReviewFindingIDs(findings)
	if len(findings) == 0 {
		if strings.EqualFold(reviewStructuredReviewVerdict(raw), reviewVerdictApproved) {
			return nil, reviewModelQualityUsable
		}
		if reviewRawLooksNonBlockingApproval(raw) {
			return []ReviewFinding{reviewUnstructuredApprovalFinding(raw, role, korean)}, reviewModelQualityUsable
		}
		return []ReviewFinding{reviewNoStructuredFindingPlaceholder(raw, role, korean)}, reviewModelQualityWeak
	}
	if omittedStructuredField || truncatedStructuredTail {
		if !reviewFindingsContainOmittedOutputPlaceholder(findings) {
			return findings, reviewModelQualityUsable
		}
		return findings, reviewModelQualityWeak
	}
	return findings, reviewModelQualityUsable
}

func reviewLineIsEmptyFindingsMarker(lowerLine string) bool {
	lowerLine = strings.TrimSpace(lowerLine)
	switch lowerLine {
	case "findings: []", "findings:[]", "findings: none", "findings: none.", "findings: n/a", "findings: null":
		return true
	default:
		return false
	}
}

func reviewStructuredReviewVerdict(raw string) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimLeft(line, "-*0123456789. "))
		lower := strings.ToLower(trimmed)
		if !strings.HasPrefix(lower, "verdict:") {
			continue
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		return strings.ToLower(strings.TrimSpace(parts[1]))
	}
	return ""
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

func normalizeModelReviewFindingForGate(f *ReviewFinding, korean bool) {
	if f == nil || !strings.EqualFold(strings.TrimSpace(f.Source), "model") {
		return
	}
	if strings.TrimSpace(f.Quality) == "" {
		f.Quality = classifyReviewFindingQuality(*f)
	}
	if strings.EqualFold(f.Quality, reviewFindingQualityWeak) ||
		strings.EqualFold(f.Quality, reviewFindingQualityInvalid) {
		if strings.EqualFold(f.Severity, reviewSeverityBlocker) ||
			strings.EqualFold(f.Severity, reviewSeverityHigh) {
			f.Severity = reviewSeverityMedium
		}
		f.BlocksGate = false
		return
	}
	if !strings.EqualFold(f.Severity, reviewSeverityBlocker) &&
		!strings.EqualFold(f.Severity, reviewSeverityHigh) {
		return
	}
	if reviewModelFindingHasActionableHighEvidence(*f) {
		return
	}
	f.Severity = reviewSeverityMedium
	f.BlocksGate = false
	if !strings.EqualFold(f.Category, "test_gap") {
		f.Category = "evidence_gap"
	}
	f.Confidence = "low"
	f.Quality = reviewFindingQualityPartial
	if strings.TrimSpace(f.Impact) == "" {
		if korean {
			f.Impact = "리뷰어가 high/blocker로 표시했지만 필수 근거 필드가 부족해 코드 수정 blocker로 승격하지 않습니다."
		} else {
			f.Impact = "The reviewer marked this as high or blocker, but required evidence fields are missing."
		}
	}
	if strings.TrimSpace(f.RequiredFix) == "" {
		if korean {
			f.RequiredFix = "경로, 심볼, 근거, 영향, 수정 방법이 포함되도록 범위를 좁혀 리뷰를 다시 실행하세요."
		} else {
			f.RequiredFix = "Rerun review with a narrower target that includes path, symbol, evidence, impact, and fix details."
		}
	}
}

func reviewModelFindingHasActionableHighEvidence(f ReviewFinding) bool {
	return (strings.TrimSpace(f.Path) != "" || strings.TrimSpace(f.Symbol) != "") &&
		strings.TrimSpace(f.Evidence) != "" &&
		strings.TrimSpace(f.Impact) != "" &&
		strings.TrimSpace(f.RequiredFix) != ""
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
		title == "리뷰어 출력이 finding 일부를 생략함" ||
		title == "Reviewer output appears cut off" ||
		title == "리뷰어 출력이 중간에 잘린 것으로 보임"
}

func reviewTruncatedTailFindingPlaceholder(role string, korean bool) ReviewFinding {
	if korean {
		return ReviewFinding{
			Source:       "model",
			ReviewerRole: role,
			Severity:     reviewSeverityMedium,
			Category:     "evidence_gap",
			Confidence:   "medium",
			Quality:      reviewFindingQualityWeak,
			Title:        "리뷰어 출력이 중간에 잘린 것으로 보임",
			Evidence:     "리뷰어의 마지막 구조화 필드가 완성된 문장이나 충분한 검증 설명 없이 끝났습니다.",
			Impact:       "잘린 finding을 그대로 승인하면 테스트 권고나 수정 조건이 누락된 상태로 repair 흐름이 시작될 수 있습니다.",
			RequiredFix:  "엄격 리뷰를 다시 실행해 완성된 evidence, impact, required_fix, test_recommendation을 받으십시오.",
			BlocksGate:   false,
		}
	}
	return ReviewFinding{
		Source:       "model",
		ReviewerRole: role,
		Severity:     reviewSeverityMedium,
		Category:     "evidence_gap",
		Confidence:   "medium",
		Quality:      reviewFindingQualityWeak,
		Title:        "Reviewer output appears cut off",
		Evidence:     "The reviewer ended the final structured field without a complete sentence or sufficient validation detail.",
		Impact:       "Accepting a cut-off finding can start repair with missing test guidance or fix conditions.",
		RequiredFix:  "Retry strict review and require complete evidence, impact, required_fix, and test_recommendation fields.",
		BlocksGate:   false,
	}
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

func reviewRawLooksIncompleteStructuredTail(raw string) bool {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n"), "\n")
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		last = strings.TrimSpace(strings.TrimLeft(lines[i], "-*0123456789. "))
		if last != "" {
			break
		}
	}
	if last == "" || !strings.Contains(last, ":") {
		return false
	}
	parts := strings.SplitN(last, ":", 2)
	key := strings.ToLower(strings.TrimSpace(parts[0]))
	value := strings.TrimSpace(parts[1])
	if key != "test_recommendation" && key != "test" {
		return false
	}
	if value == "" {
		return true
	}
	if reviewTextEndsLikeCompleteSentence(value) {
		return false
	}
	if reviewTextContainsHangul(value) && len([]rune(value)) < 12 {
		return true
	}
	return false
}

func reviewTextEndsLikeCompleteSentence(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(text)
	switch last {
	case '.', '!', '?', '。', '다', '요', '오', '음', '함', '됨':
		return true
	default:
		return false
	}
}

func reviewTextContainsHangul(text string) bool {
	for _, r := range text {
		if r >= 0xAC00 && r <= 0xD7A3 {
			return true
		}
	}
	return false
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
