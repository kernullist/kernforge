package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
					Evidence:     "Single-model mode cannot independently re-check a write without a frozen diff or captured edit proposal preview.",
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
	if reviewShouldIncludeCodingHarness(run) && rt != nil && rt.session != nil && rt.session.LastCodingHarnessReport != nil {
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
			if preWrite {
				// The coding harness audits the agent's final state and final
				// answer claims. Pre-write review gates a frozen edit proposal,
				// so stale or unrelated final-state blockers must not veto the
				// exact diff before it reaches preview.
				if severity == reviewSeverityBlocker {
					severity = reviewSeverityMedium
				}
				blocks = false
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
		Evidence:     "Required repair findings from the pre-fix review do not all record resolved, partial, unresolved, verification_needed, or evidence_unconfirmed status.",
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
			if reviewPreWriteWarningLooksLikeHarnessEvidenceGap(finding) {
				return "evidence_unconfirmed"
			}
			return "partial"
		}
		if reviewSeverityRank(finding.Severity) <= reviewSeverityRank(reviewSeverityMedium) ||
			strings.EqualFold(finding.Severity, reviewSeverityBlocker) {
			return "unresolved"
		}
		return "partial"
	}
	return "evidence_unconfirmed"
}

func reviewFindingReferencesRepairFinding(finding ReviewFinding, repair ReviewFinding) bool {
	haystack := strings.ToLower(strings.Join([]string{
		finding.Title,
		finding.Evidence,
		finding.Impact,
		finding.RequiredFix,
		finding.TestRecommendation,
	}, " "))
	if reviewTextContainsIdentifierToken(haystack, repair.ID) {
		return true
	}
	title := normalizeReviewReferenceText(repair.Title)
	return len(title) >= 24 && strings.Contains(normalizeReviewReferenceText(haystack), title)
}

func reviewTextContainsIdentifierToken(text string, token string) bool {
	text = strings.ToLower(text)
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return false
	}
	offset := 0
	for {
		idx := strings.Index(text[offset:], token)
		if idx < 0 {
			return false
		}
		start := offset + idx
		end := start + len(token)
		if reviewIdentifierBoundary(text, start-1) && reviewIdentifierBoundary(text, end) {
			return true
		}
		offset = end
	}
}

func reviewIdentifierBoundary(text string, index int) bool {
	if index < 0 || index >= len(text) {
		return true
	}
	ch := text[index]
	return !((ch >= 'a' && ch <= 'z') ||
		(ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9') ||
		ch == '-' ||
		ch == '_')
}

func normalizeReviewReferenceText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
}

func singleModelPreWriteHasFrozenDiff(run ReviewRun) bool {
	proposals := normalizeEditProposals(run.EditProposals)
	if len(proposals) == 0 {
		return singleModelPreWriteHasProvidedDiffEvidence(run)
	}
	proposalFiles := singleModelPreWriteProposalFiles(proposals)
	for _, proposal := range proposals {
		if !singleModelPreWriteProposalHasBoundPreview(proposal, proposalFiles) &&
			!singleModelPreWriteProposalMatchesProvidedDiff(run, proposal) {
			return false
		}
	}
	return true
}

func singleModelPreWriteHasProvidedDiffEvidence(run ReviewRun) bool {
	if !containsString(run.Evidence.Sources, "provided_diff") {
		return false
	}
	text := strings.TrimSpace(run.Evidence.Text)
	if text == "" {
		return false
	}
	return strings.Contains(text, "## Provided diff")
}

func singleModelPreWriteProposalMatchesProvidedDiff(run ReviewRun, proposal EditProposal) bool {
	if !singleModelPreWriteHasProvidedDiffEvidence(run) {
		return false
	}
	expectedPreview := strings.TrimSpace(proposal.ExpectedPreview)
	if expectedPreview == "" {
		return false
	}
	return strings.Contains(run.Evidence.Text, expectedPreview)
}

func singleModelPreWriteProposalFiles(proposals []EditProposal) []string {
	var files []string
	for _, proposal := range proposals {
		files = append(files, normalizeEditProposalFiles(proposal.Files)...)
		if strings.TrimSpace(proposal.File) != "" {
			files = append(files, filepath.ToSlash(strings.TrimSpace(proposal.File)))
		}
	}
	return normalizeEditProposalPathList(files, 0)
}

func singleModelPreWriteProposalHasBoundPreview(proposal EditProposal, proposalFiles []string) bool {
	expectedPreview := proposal.ExpectedPreview
	if strings.TrimSpace(expectedPreview) == "" {
		return false
	}
	fingerprint := strings.TrimSpace(proposal.PreviewFingerprint)
	if fingerprint == "" {
		return false
	}
	if proposal.trustedPreviewFingerprint != "" &&
		fingerprint == strings.TrimSpace(proposal.trustedPreviewFingerprint) {
		return true
	}
	if proposal.ExpectedComplete != nil && !*proposal.ExpectedComplete {
		return false
	}
	operation := strings.TrimSpace(proposal.Operation)
	file := filepath.ToSlash(strings.TrimSpace(proposal.File))
	if fingerprint == computeReviewFingerprint(operation, file, expectedPreview) {
		return true
	}
	if len(proposalFiles) > 0 &&
		fingerprint == computeReviewFingerprint(operation, editProposalFingerprintTargetForPaths(proposalFiles), expectedPreview) {
		return true
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
	case "resolved", "partial", "unresolved", "verification_needed", "evidence_unconfirmed":
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
	case "evidence_gap", "evidence_gap_only", "evidence_unconfirmed_only":
		return "evidence_unconfirmed"
	default:
		return status
	}
}

func mergeReviewFindings(findings []ReviewFinding) ([]ReviewFinding, ReviewMergeResult) {
	result := ReviewMergeResult{}
	seen := map[string]int{}
	seenSubject := map[string]int{}
	var merged []ReviewFinding
	for _, finding := range findings {
		finding.Normalize()
		key := reviewFindingKey(finding)
		if prior, ok := seen[key]; ok {
			mergeDuplicateReviewFinding(&merged[prior], finding, &result)
			if subjectKey := reviewFindingDuplicateSubjectKey(merged[prior]); subjectKey != "" {
				seenSubject[subjectKey] = prior
			}
			continue
		}
		subjectKey := reviewFindingDuplicateSubjectKey(finding)
		if subjectKey != "" {
			if prior, ok := seenSubject[subjectKey]; ok {
				mergeDuplicateReviewFinding(&merged[prior], finding, &result)
				seen[key] = prior
				continue
			}
		}
		if prior, ok := reviewFindDuplicateByTokenOverlap(merged, finding); ok {
			mergeDuplicateReviewFinding(&merged[prior], finding, &result)
			seen[key] = prior
			if subjectKey != "" {
				seenSubject[subjectKey] = prior
			}
			continue
		}
		seen[key] = len(merged)
		if subjectKey != "" {
			seenSubject[subjectKey] = len(merged)
		}
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

func reviewFindDuplicateByTokenOverlap(findings []ReviewFinding, incoming ReviewFinding) (int, bool) {
	for i := range findings {
		if reviewFindingsHaveDuplicateTokenSubject(findings[i], incoming) {
			return i, true
		}
	}
	return 0, false
}

func reviewFindingsHaveDuplicateTokenSubject(left ReviewFinding, right ReviewFinding) bool {
	if strings.TrimSpace(left.Path) == "" || strings.TrimSpace(left.Symbol) == "" {
		return false
	}
	if !strings.EqualFold(filepathSlash(left.Path), filepathSlash(right.Path)) ||
		!strings.EqualFold(strings.TrimSpace(left.Symbol), strings.TrimSpace(right.Symbol)) ||
		!strings.EqualFold(strings.TrimSpace(left.Category), strings.TrimSpace(right.Category)) {
		return false
	}
	leftTokens := reviewFindingTokenSet(left)
	rightTokens := reviewFindingTokenSet(right)
	overlap := 0
	for token := range leftTokens {
		if rightTokens[token] {
			overlap++
			if overlap >= 2 {
				return true
			}
		}
	}
	return false
}

func reviewFindingTokenSet(finding ReviewFinding) map[string]bool {
	out := map[string]bool{}
	tokenMap := map[string]string{}
	addRepairFindingAlignmentTokens(tokenMap, strings.Join([]string{
		finding.Title,
		finding.Evidence,
		finding.Impact,
		finding.RequiredFix,
		finding.TestRecommendation,
		finding.RawExcerpt,
	}, " "))
	for token := range tokenMap {
		token = strings.ToLower(strings.TrimSpace(token))
		if reviewDuplicateSubjectTokenUseful(token) {
			out[token] = true
		}
	}
	return out
}

func reviewDuplicateSubjectTokenUseful(token string) bool {
	lower := strings.ToLower(strings.TrimSpace(token))
	if lower == "" {
		return false
	}
	switch lower {
	case "code", "finding", "fix", "issue", "review", "required", "repair",
		"line", "lines", "path", "paths", "file", "files", "name", "names",
		"current", "existing", "updated", "change", "changes":
		return false
	default:
		return true
	}
}

func mergeDuplicateReviewFinding(existing *ReviewFinding, incoming ReviewFinding, result *ReviewMergeResult) {
	if existing == nil {
		return
	}
	if result != nil {
		result.SuppressedDuplicates = append(result.SuppressedDuplicates, incoming.ID)
	}
	previousSeverity := existing.Severity
	if reviewSeverityRank(incoming.Severity) < reviewSeverityRank(existing.Severity) {
		existing.Severity = incoming.Severity
	}
	existing.BlocksGate = existing.BlocksGate || incoming.BlocksGate
	existing.Confidence = reviewStrongerConfidence(existing.Confidence, incoming.Confidence)
	existing.Quality = reviewStrongerFindingQuality(existing.Quality, incoming.Quality)
	if reviewFindingHasMoreRepairDetail(incoming, *existing) {
		id := existing.ID
		blocks := existing.BlocksGate
		severity := existing.Severity
		confidence := existing.Confidence
		quality := existing.Quality
		*existing = mergeReviewFindingText(incoming, *existing)
		existing.ID = id
		existing.BlocksGate = blocks
		existing.Severity = severity
		existing.Confidence = confidence
		existing.Quality = quality
	} else {
		*existing = mergeReviewFindingText(*existing, incoming)
	}
	if result != nil && previousSeverity != existing.Severity {
		result.SeverityChanges = append(result.SeverityChanges, fmt.Sprintf("%s kept higher severity %s over %s", existing.ID, existing.Severity, previousSeverity))
	}
}

func mergeReviewFindingText(primary ReviewFinding, secondary ReviewFinding) ReviewFinding {
	if strings.TrimSpace(primary.Path) == "" {
		primary.Path = secondary.Path
	}
	if strings.TrimSpace(primary.Symbol) == "" {
		primary.Symbol = secondary.Symbol
	}
	if strings.TrimSpace(primary.Title) == "" {
		primary.Title = secondary.Title
	}
	if strings.TrimSpace(primary.Evidence) == "" {
		primary.Evidence = secondary.Evidence
	}
	if strings.TrimSpace(primary.Impact) == "" {
		primary.Impact = secondary.Impact
	}
	if strings.TrimSpace(primary.RequiredFix) == "" {
		primary.RequiredFix = secondary.RequiredFix
	}
	if strings.TrimSpace(primary.TestRecommendation) == "" {
		primary.TestRecommendation = secondary.TestRecommendation
	}
	if strings.TrimSpace(primary.RawExcerpt) == "" {
		primary.RawExcerpt = secondary.RawExcerpt
	}
	if len(primary.EvidenceRefs) == 0 {
		primary.EvidenceRefs = append([]string(nil), secondary.EvidenceRefs...)
	}
	if len(primary.FixRefs) == 0 {
		primary.FixRefs = append([]string(nil), secondary.FixRefs...)
	}
	if len(primary.VerificationRefs) == 0 {
		primary.VerificationRefs = append([]string(nil), secondary.VerificationRefs...)
	}
	return primary
}

func reviewFindingHasMoreRepairDetail(left ReviewFinding, right ReviewFinding) bool {
	return reviewFindingRepairDetailScore(left) > reviewFindingRepairDetailScore(right)
}

func reviewFindingRepairDetailScore(f ReviewFinding) int {
	score := 0
	for _, value := range []string{f.Path, f.Symbol, f.Title, f.Evidence, f.Impact, f.RequiredFix, f.TestRecommendation, f.RawExcerpt} {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		score += 1
		if len(value) > 80 {
			score += 1
		}
	}
	return score
}

func (f *ReviewFinding) Normalize() {
	if f == nil {
		return
	}
	f.Severity = normalizeReviewSeverity(f.Severity)
	f.ResolutionStatus = normalizeReviewRepairResolutionStatus(f.ResolutionStatus)
	f.EvidenceRefs = normalizeTaskStateList(f.EvidenceRefs, 12)
	f.FixRefs = normalizeTaskStateList(f.FixRefs, 12)
	f.VerificationRefs = normalizeTaskStateList(f.VerificationRefs, 12)
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
	if reviewFindingSourceIsModelish(*f) && reviewFindingLooksNonActionablePlaceholder(*f) {
		f.Quality = reviewFindingQualityWeak
		f.Confidence = "low"
		f.BlocksGate = false
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

func reviewFindingDuplicateSubjectKey(f ReviewFinding) string {
	if reviewFindingLooksNonActionablePlaceholder(f) {
		return ""
	}
	subject := reviewFindingCanonicalDuplicateSubject(f)
	if subject == "" {
		return ""
	}
	path := strings.ToLower(strings.TrimSpace(filepathSlash(f.Path)))
	symbol := strings.ToLower(strings.TrimSpace(f.Symbol))
	if path == "" && symbol == "" {
		return subject
	}
	return path + "|" + symbol + "|" + subject
}

func reviewFindingCanonicalDuplicateSubject(f ReviewFinding) string {
	tokens := repairFindingAlignmentTokens(f)
	if len(tokens) == 0 {
		return ""
	}
	if len(tokens) > 4 {
		tokens = tokens[:4]
	}
	lowered := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.ToLower(strings.TrimSpace(token))
		if token != "" {
			lowered = append(lowered, token)
		}
	}
	return strings.Join(lowered, "|")
}

func reviewFindingSourceIsModelish(f ReviewFinding) bool {
	source := strings.ToLower(strings.TrimSpace(f.Source))
	return source == "" || source == "model" || source == "reviewer" || source == "main" || source == "cross"
}

func reviewFindingLooksNonActionablePlaceholder(f ReviewFinding) bool {
	title := strings.ToLower(strings.Trim(strings.TrimSpace(f.Title), ". "))
	fix := strings.ToLower(strings.Trim(strings.TrimSpace(f.RequiredFix), ". "))
	evidence := strings.TrimSpace(f.Evidence)
	impact := strings.TrimSpace(f.Impact)
	genericTitle := title == "stability issue" ||
		title == "correctness issue" ||
		title == "security issue" ||
		title == "performance issue" ||
		title == "maintainability issue" ||
		title == "review finding" ||
		strings.HasPrefix(title, "finding in ")
	genericFix := fix == "inspect and address this reviewer finding" ||
		fix == "inspect and address this finding" ||
		fix == "address this reviewer finding" ||
		fix == "apply the reviewer-described fix if it is not already present"
	return genericTitle && genericFix && evidence == "" && impact == ""
}

func reviewStrongerFindingQuality(left string, right string) string {
	if reviewFindingQualityRank(right) < reviewFindingQualityRank(left) {
		return right
	}
	return left
}

func reviewFindingQualityRank(quality string) int {
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case reviewFindingQualityComplete:
		return 0
	case reviewFindingQualityPartial:
		return 1
	case reviewFindingQualityWeak:
		return 2
	case reviewFindingQualityInvalid:
		return 3
	default:
		return 4
	}
}

func reviewStrongerConfidence(left string, right string) string {
	if reviewConfidenceRank(right) < reviewConfidenceRank(left) {
		return right
	}
	return left
}

func reviewConfidenceRank(confidence string) int {
	switch strings.ToLower(strings.TrimSpace(confidence)) {
	case "high":
		return 0
	case "medium":
		return 1
	case "low":
		return 2
	default:
		return 3
	}
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
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		gate = reviewPromotePreWriteActionableWarnings(run, gate)
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
	case len(gate.WarningFindings) > 0 || reviewRunDegradedShouldWarn(run):
		gate.Verdict = reviewVerdictApprovedWithWarnings
		gate.Reason = "no blockers, but warnings or degraded reviewer evidence remain"
	default:
		gate.Verdict = reviewVerdictApproved
		gate.Reason = "no blocking findings found"
	}
	run.Gate = gate
	gate.Action = reviewGateActionForRun(run)
	gate.NextCommands = reviewNextCommands(run, gate)
	if run.Result.Degraded && strings.TrimSpace(run.Result.DegradedReason) != "" {
		gate.QualityNotes = append(gate.QualityNotes, run.Result.DegradedReason)
	}
	for _, guidance := range run.ModelPlan.UserGuidance {
		gate.QualityNotes = append(gate.QualityNotes, guidance)
	}
	return gate
}

func reviewRunDegradedShouldWarn(run ReviewRun) bool {
	if !run.Result.Degraded {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") &&
		reviewRunHasUsableCrossReviewer(run) &&
		len(reviewFailedRequiredReviewerRuns(run)) == 0 {
		return false
	}
	return true
}

func normalizePreWriteVerificationOnlyFindings(run *ReviewRun) {
	normalizeNonBlockingVerificationOnlyFindings(run)
}

func normalizeNonBlockingReviewMetaFindings(run *ReviewRun) {
	if run == nil {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") &&
		!strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		return
	}
	for i := range run.Findings {
		if !reviewFindingLooksReviewMetaOnly(run.Findings[i]) {
			continue
		}
		run.Findings[i].Severity = reviewSeverityInfo
		run.Findings[i].BlocksGate = false
		run.Findings[i].ResolutionStatus = "non_blocking_review_meta"
		if strings.TrimSpace(run.Findings[i].Category) == "" {
			run.Findings[i].Category = "false_positive"
		}
		if strings.TrimSpace(run.Findings[i].Confidence) == "" {
			run.Findings[i].Confidence = "medium"
		}
	}
}

func reviewFindingLooksReviewMetaOnly(f ReviewFinding) bool {
	f.Normalize()
	text := strings.ToLower(strings.Join([]string{
		f.Title,
		f.Evidence,
		f.Impact,
		f.RequiredFix,
		f.TestRecommendation,
	}, " "))
	if strings.TrimSpace(text) == "" {
		return false
	}
	metaSubject := containsAny(text,
		"review finding",
		"reviewer finding",
		"finding severity",
		"finding's severity",
		"severity high",
		"severity:high",
		"severity: high",
		"severity를",
		"severity:",
		"1차 초안",
		"메인 초안",
		"리뷰 finding",
		"검토 finding",
		"finding의",
	)
	resolutionText := containsAny(text,
		"already resolved",
		"already fixed",
		"already addressed",
		"required_fix: none",
		"required fix: none",
		"no code change",
		"no production code",
		"downgrade",
		"lower to info",
		"mark as info",
		"info로",
		"하향",
		"이미 해결",
		"해결된 항목",
		"코드 수정은 필요",
		"수정 불필요",
	)
	if metaSubject && resolutionText {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(f.Category), "false_positive") &&
		metaSubject &&
		containsAny(text, "review", "리뷰", "finding", "초안") {
		return true
	}
	return false
}

func reviewFindingLooksLowNonBlockingPreWriteConcern(f ReviewFinding) bool {
	f.Normalize()
	if !strings.EqualFold(strings.TrimSpace(f.Severity), reviewSeverityLow) {
		return false
	}
	text := strings.ToLower(strings.Join([]string{
		f.Title,
		f.Evidence,
		f.Impact,
		f.RequiredFix,
		f.TestRecommendation,
	}, " "))
	if strings.TrimSpace(text) == "" {
		return false
	}
	if containsAny(text,
		"not directly introduced",
		"not introduced by",
		"not a regression introduced",
		"pre-existing",
		"preexisting",
		"existing code",
		"legacy code",
		"from before",
		"not newly introduced",
		"사전 코드",
		"기존 코드",
		"잔존",
		"본 변경에서 도입한 결함은 아니",
		"도입한 결함은 아니",
		"직접 도입되지",
	) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(f.Category), "maintainability") &&
		containsAny(text,
			"type intent",
			"intent is unclear",
			"clarity",
			"readability",
			"future",
			"porting",
			"static analysis",
			"code reviewer",
			"의도가 흐",
			"혼동",
			"향후",
			"포팅",
			"정적 분석",
			"가독성",
		) &&
		!containsAny(text,
			"compile",
			"build",
			"#include",
			"missing include",
			"missing declaration",
			"fail to compile",
			"can break this translation unit",
			"빌드",
			"컴파일 실패",
			"컴파일 오류",
			"컴파일 에러",
			"컴파일 가능성을 확인",
			"컴파일되지",
			"include",
			"선언 누락",
		) {
		return true
	}
	if containsAny(text,
		"optional",
		"optional hardening",
		"nice-to-have",
		"separate hardening",
		"separate change",
		"broader hardening",
		"추가 수정 불요",
		"별도",
		"권장",
	) &&
		!containsAny(text,
			"introduced by the proposed diff",
			"introduced by this diff",
			"new regression",
			"regression introduced",
			"patch can fail",
			"패치가 도입",
			"새 회귀",
			"회귀를 도입",
		) {
		return true
	}
	return false
}

func normalizeNonBlockingVerificationOnlyFindings(run *ReviewRun) {
	if run == nil || !reviewVerificationOnlyFindingsAreNonBlocking(*run) {
		return
	}
	for i := range run.Findings {
		if !reviewFindingLooksVerificationOnly(run.Findings[i]) {
			continue
		}
		if strings.EqualFold(run.Findings[i].Severity, reviewSeverityBlocker) ||
			strings.EqualFold(run.Findings[i].Severity, reviewSeverityHigh) {
			run.Findings[i].Severity = reviewSeverityMedium
		}
		run.Findings[i].Category = "test_gap"
		run.Findings[i].BlocksGate = false
		if strings.TrimSpace(run.Findings[i].Confidence) == "" {
			run.Findings[i].Confidence = "medium"
		}
	}
}

func reviewVerificationOnlyFindingsAreNonBlocking(run ReviewRun) bool {
	trigger := strings.TrimSpace(run.Trigger)
	return strings.EqualFold(trigger, "pre_write") ||
		strings.EqualFold(trigger, reviewBeforeFixTrigger)
}

func reviewFindingLooksVerificationOnly(f ReviewFinding) bool {
	if !reviewFindingSourceIsModelish(f) &&
		!reviewFindingRoleCanReportVerificationOnly(f) {
		return false
	}
	text := strings.ToLower(strings.Join([]string{
		f.Title,
		f.Evidence,
		f.Impact,
		f.RequiredFix,
		f.TestRecommendation,
	}, " "))
	if strings.TrimSpace(text) == "" {
		return false
	}
	return containsAny(text,
		"latest verification",
		"verification reports",
		"verification report",
		"verification section",
		"verification evidence",
		"verification failed",
		"verification was skipped",
		"verification skipped",
		"recommended verification not recorded",
		"recommended verification",
		"verification not recorded",
		"verification pending",
		"no recorded evidence",
		"[failed]",
		"passed=",
		"failed=",
		"최신 검증",
		"검증 기록",
		"검증 증거",
		"검증 실패",
	)
}

func reviewFindingRoleCanReportVerificationOnly(f ReviewFinding) bool {
	role := strings.TrimSpace(f.ReviewerRole)
	return strings.EqualFold(role, "verification_reviewer") ||
		strings.EqualFold(role, "test_impact_reviewer") ||
		strings.EqualFold(role, "coding_harness")
}

func reviewPromotePreWriteActionableWarnings(run ReviewRun, gate GateDecision) GateDecision {
	if len(gate.WarningFindings) == 0 {
		return gate
	}
	actionableWarnings := reviewPreWriteActionableWarningIDSet(run, gate.WarningFindings)
	if len(actionableWarnings) == 0 {
		return gate
	}
	existingBlockers := reviewFindingIDSet(gate.BlockingFindings)
	var remainingWarnings []string
	for _, id := range gate.WarningFindings {
		if actionableWarnings[id] {
			if !existingBlockers[id] {
				gate.BlockingFindings = append(gate.BlockingFindings, id)
				existingBlockers[id] = true
			}
			continue
		}
		remainingWarnings = append(remainingWarnings, id)
	}
	gate.WarningFindings = remainingWarnings
	for _, finding := range run.Findings {
		if actionableWarnings[finding.ID] && strings.TrimSpace(finding.RequiredFix) != "" {
			gate.RequiredActions = append(gate.RequiredActions, finding.RequiredFix)
		}
	}
	return gate
}

func reviewPreWriteActionableWarningIDSet(run ReviewRun, warningIDs []string) map[string]bool {
	warningSet := reviewFindingIDSet(warningIDs)
	if len(warningSet) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, finding := range run.Findings {
		if !warningSet[finding.ID] {
			continue
		}
		if preWriteReviewWarningShouldBlock(finding) {
			out[finding.ID] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func reviewFindingCountsAsWarning(finding ReviewFinding) bool {
	if reviewFindingLooksReviewMetaOnly(finding) {
		return false
	}
	if strings.EqualFold(finding.Severity, reviewSeverityHigh) ||
		strings.EqualFold(finding.Severity, reviewSeverityMedium) ||
		strings.EqualFold(finding.Severity, reviewSeverityLow) {
		return true
	}
	return false
}

func reviewFindingBlocksGate(run ReviewRun, finding ReviewFinding) bool {
	if reviewFindingSourceIsModelish(finding) &&
		(strings.EqualFold(finding.Quality, reviewFindingQualityWeak) ||
			strings.EqualFold(finding.Quality, reviewFindingQualityInvalid)) {
		return false
	}
	if reviewFindingLooksReviewMetaOnly(finding) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") &&
		reviewFindingLooksLowNonBlockingPreWriteConcern(finding) {
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
	if strings.EqualFold(finding.Category, "evidence_gap") {
		return false
	}
	if strings.EqualFold(finding.Category, "test_gap") &&
		!reviewFindingLooksImplementationRepairDespiteGapCategory(finding) {
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
		hint := "If primary failed, change the active main model with /model or fix that provider route. If cross failed, fix that route with /model cross-review or clear it with /model clear cross-review. Retry with a longer timeout only after the route is healthy."
		if reviewRunHasUsableMainReviewer(run) {
			hint = "The main review is usable. In an interactive edit flow, explicitly say that the main model review may be used for this pre-write fallback before diff preview."
		}
		out = append(out, ReviewNextCommand{
			ID:                   "reviewer-fallback",
			Command:              "/model cross-review status",
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
	if gate.Action == reviewGateActionRepairRequired && !reviewNextCommandsHasAnyID(out, "repair", "repair-warnings") {
		reason := "open repair obligations remain in the obligation ledger"
		when := "after reading review obligations"
		hint := "Use the obligation ledger and repair prompt in the review artifact."
		expected := "Open repair obligations are converted into a focused repair turn."
		if reviewRunLooksReadOnlyAnalysis(run) {
			reason = "open repair obligations were found; repair is optional unless the user asks to fix them"
			when = "if you decide to turn the analysis into a repair pass"
			hint = "Say `수정해줘` or run this command only if you want Kernforge to fix the obligations."
			expected = "Open repair obligations are converted into repair guidance only after an explicit repair request."
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
	if reviewRunHasCrossReviewUserDecision(run) {
		out = append(out, ReviewNextCommand{
			ID:                   "cross-review-triage",
			Command:              "/continuity continue from review",
			Reason:               "cross-review triage has findings that need a user or primary repair decision",
			Safety:               "safe_local",
			When:                 "before final answer if the finding affects accepted risk or next repair scope",
			AutoRun:              false,
			RequiresConfirmation: true,
			ClientHint:           "Repair the finding, or reply with accepted_fixed plus fix refs, accepted_deferred plus reason, or rejected_with_reason plus code evidence.",
			ExpectedResult:       "The cross-review triage ledger no longer has needs_user_decision items.",
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
	if reviewHasSecurityFinding(run.Findings) && !reviewStringSliceContainsCI(run.ModelPlan.OptionalRoles, "cross_reviewer") && !reviewStringSliceContainsCI(run.ModelPlan.RequiredRoles, "cross_reviewer") {
		out = append(out, ReviewNextCommand{
			ID:             "set-cross-model",
			Command:        "/model cross-review",
			Reason:         "security-sensitive review is using single-model review",
			Safety:         "read_only",
			When:           "before future security reviews",
			AutoRun:        false,
			ClientHint:     "Configure an independent cross reviewer route.",
			ExpectedResult: "Future security reviews can run an independent second-pass reviewer with the security lens.",
		})
	}
	return out
}

func reviewNextCommandsHasAnyID(commands []ReviewNextCommand, ids ...string) bool {
	wanted := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			wanted[id] = true
		}
	}
	for _, command := range commands {
		if wanted[strings.TrimSpace(command.ID)] {
			return true
		}
	}
	return false
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
	if reviewFindingLooksReviewMetaOnly(finding) {
		return false
	}
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
		if reviewFindingLooksReviewMetaOnly(finding) {
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
	repairObligations := reviewOpenRepairRequiredObligationMap(run.ObligationLedger)
	seenPlanFindings := map[string]bool{}
	addFinding := func(finding ReviewFinding, forceBlocking bool) {
		finding.Normalize()
		key := strings.ToLower(firstNonBlankString(finding.ID, finding.Path+"|"+finding.Symbol+"|"+finding.Title))
		if key == "" || seenPlanFindings[key] {
			return
		}
		if forceBlocking {
			if !reviewFindingShouldBeRepairPlanBlocker(run, finding) {
				return
			}
			seenPlanFindings[key] = true
			blocking = append(blocking, finding)
			return
		}
		if hasGateClassification {
			if blockingIDs[finding.ID] {
				if !reviewFindingShouldBeRepairPlanBlocker(run, finding) {
					return
				}
				seenPlanFindings[key] = true
				blocking = append(blocking, finding)
				return
			}
			if warningIDs[finding.ID] && reviewFindingShouldBeRepairPlanWarning(finding) {
				seenPlanFindings[key] = true
				warnings = append(warnings, finding)
			}
			return
		}
		if reviewFindingBlocksGate(run, finding) {
			if !reviewFindingShouldBeRepairPlanBlocker(run, finding) {
				return
			}
			seenPlanFindings[key] = true
			blocking = append(blocking, finding)
			return
		}
		if reviewFindingShouldBeRepairPlanWarning(finding) {
			seenPlanFindings[key] = true
			warnings = append(warnings, finding)
		}
	}
	for _, finding := range append(append([]ReviewFinding{}, run.Findings...), run.RepairFindings...) {
		if _, ok := reviewObligationMapLookupByFinding(repairObligations, finding); ok {
			addFinding(finding, true)
			continue
		}
		addFinding(finding, false)
	}
	if len(blocking) == 0 && reviewGateActionForRun(run) == reviewGateActionRepairRequired {
		for _, obligation := range repairObligations {
			addFinding(reviewFindingFromRepairObligation(obligation), true)
		}
	}
	if len(blocking) == 0 {
		return ReviewRepairPlan{}
	}
	blocking = canonicalizeReviewRepairPlanFindings(blocking)
	warnings = canonicalizeReviewRepairPlanFindings(warnings)
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
	if reviewRunUsesLocalOrDegradedPrimary(run) {
		b.WriteString("\n")
		b.WriteString(reviewLocalRepairHandoffGuidance(korean))
	}
	if reviewRepairPlanHasCrossReviewerFindings(blocking, warnings) {
		if korean {
			b.WriteString("\nCross-review finding은 독립 리뷰 피드백입니다. 각 항목을 accepted/fixed, accepted/deferred, rejected_with_reason, 또는 needs_user_decision으로 판단하고, 무시하지 말고 최종 응답이나 수리 근거에 남기세요.\n")
		} else {
			b.WriteString("\nCross-review findings are independent review feedback. Triage each item as accepted/fixed, accepted/deferred, rejected_with_reason, or needs_user_decision; do not silently ignore them.\n")
		}
	}
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

func reviewRepairPlanHasCrossReviewerFindings(blocking []ReviewFinding, warnings []ReviewFinding) bool {
	for _, finding := range append(append([]ReviewFinding{}, blocking...), warnings...) {
		role := normalizeReviewRole(finding.ReviewerRole)
		if role != "" && role != "primary_reviewer" && !strings.EqualFold(role, "single_model_policy") {
			return true
		}
	}
	return false
}

func reviewFindingShouldBeRepairPlanBlocker(run ReviewRun, finding ReviewFinding) bool {
	finding.Normalize()
	if strings.EqualFold(strings.TrimSpace(finding.ID), requiredReviewerFailureFindingID) {
		return false
	}
	if reviewFindingLooksReviewMetaOnly(finding) {
		return false
	}
	if strings.EqualFold(finding.Category, "evidence_gap") {
		return false
	}
	if strings.EqualFold(finding.Category, "test_gap") &&
		!reviewFindingLooksImplementationRepairDespiteGapCategory(finding) {
		return false
	}
	if reviewFindingLooksAdvisoryStyleCategory(finding) {
		return false
	}
	return strings.TrimSpace(finding.RequiredFix) != "" ||
		strings.TrimSpace(finding.Path) != "" ||
		strings.TrimSpace(finding.Symbol) != "" ||
		strings.TrimSpace(finding.Title) != ""
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

func canonicalizeReviewRepairPlanFindings(findings []ReviewFinding) []ReviewFinding {
	if len(findings) <= 1 {
		return findings
	}
	seen := map[string]int{}
	var out []ReviewFinding
	for _, finding := range findings {
		finding.Normalize()
		if strings.EqualFold(finding.Quality, reviewFindingQualityWeak) ||
			strings.EqualFold(finding.Quality, reviewFindingQualityInvalid) ||
			reviewFindingLooksNonActionablePlaceholder(finding) {
			continue
		}
		key := reviewFindingDuplicateSubjectKey(finding)
		if key == "" {
			key = reviewFindingKey(finding)
		}
		if prior, ok := seen[key]; ok {
			mergeDuplicateReviewFinding(&out[prior], finding, nil)
			continue
		}
		seen[key] = len(out)
		out = append(out, finding)
	}
	return out
}

func reviewRunUsesLocalOrDegradedPrimary(run ReviewRun) bool {
	for _, reviewerRun := range run.ReviewerRuns {
		if !preFixReviewerRunIsMainRoute(reviewerRun) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityWeak) ||
			strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityFailed) ||
			strings.TrimSpace(reviewerRun.Error) != "" ||
			reviewProviderUsesLocalModelRecovery(reviewReviewerRunProvider(Config{}, reviewerRun)) {
			return true
		}
	}
	return false
}

func reviewLocalRepairHandoffGuidance(korean bool) string {
	if korean {
		return strings.TrimSpace("로컬/degraded 모델용 수리 축약 규칙:\n" +
			"- 같은 호출, 변수, 라인, 실패 경로를 가리키는 RF는 하나의 코드 변경으로만 처리하세요. 중복 RF마다 별도 rewrite를 만들지 마세요.\n" +
			"- 리뷰 evidence에 직접 나온 코드 경로만 고치세요. 인접 코드 경로, 데이터 처리 정책, 함수 구조를 추측으로 재설계하지 마세요.\n" +
			"- 근거가 모호한 RF는 패치 범위로 확장하지 말고, 명확한 RF를 해결하는 좁은 hunk를 현재 파일 내용에 맞춰 작성하세요.")
	}
	return strings.TrimSpace("Local/degraded model repair compression rules:\n" +
		"- Treat RFs that point at the same call, variable, line, or failure path as one code edit. Do not create separate rewrites for duplicate RFs.\n" +
		"- Fix only the code paths directly evidenced by the review. Do not redesign adjacent code paths, data handling policy, or function structure by speculation.\n" +
		"- If an RF is vague, do not expand the patch around it; address the concrete RFs with narrow hunks anchored to the current file contents.")
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
	if reviewFindingLooksReviewMetaOnly(finding) {
		return false
	}
	if reviewSeverityRank(finding.Severity) > reviewSeverityRank(reviewSeverityMedium) {
		return false
	}
	if strings.EqualFold(finding.Quality, reviewFindingQualityWeak) ||
		strings.EqualFold(finding.Quality, reviewFindingQualityInvalid) ||
		reviewFindingLooksNonActionablePlaceholder(finding) {
		return false
	}
	if strings.EqualFold(finding.Category, "evidence_gap") {
		return false
	}
	if strings.EqualFold(finding.Category, "test_gap") &&
		!reviewFindingLooksImplementationRepairDespiteGapCategory(finding) {
		return false
	}
	return strings.TrimSpace(finding.RequiredFix) != "" ||
		strings.TrimSpace(finding.Path) != "" ||
		strings.TrimSpace(finding.Symbol) != "" ||
		strings.TrimSpace(finding.Title) != ""
}

func reviewFindingLooksImplementationRepairDespiteGapCategory(finding ReviewFinding) bool {
	finding.Normalize()
	if !strings.EqualFold(finding.Category, "test_gap") {
		return false
	}
	if reviewSeverityRank(finding.Severity) > reviewSeverityRank(reviewSeverityMedium) {
		return false
	}
	if strings.TrimSpace(finding.RequiredFix) == "" {
		return false
	}
	if strings.TrimSpace(finding.Path) == "" && strings.TrimSpace(finding.Symbol) == "" {
		return false
	}
	requiredFix := strings.TrimSpace(finding.RequiredFix)
	if reviewTextLooksVerificationOnlyAction(requiredFix) {
		return false
	}
	return reviewTextLooksImplementationRepairAction(requiredFix)
}

func reviewTextLooksVerificationOnlyAction(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if reviewTextLooksImplementationRepairAction(lower) {
		return false
	}
	return containsAny(lower,
		"add test", "add regression test", "write test", "run test", "run focused verification",
		"rerun", "re-run", "msbuild", "ctest", "go test", "cargo test", "npm test",
		"provide evidence", "verification evidence", "coverage",
		"테스트", "검증", "빌드", "증거", "커버리지",
	)
}

func reviewTextLooksImplementationRepairAction(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"change ", "modify ", "replace ", "remove ", "delete ", "restore ", "return ",
		"continue", "break", "skip ", "guard ", "validate ", "check ", "handle ",
		"allocate ", "retry ", "call ", "pass ", "use ", "set ", "clear ",
		"control flow", "data handling", "error handling",
		"변경", "수정", "교체", "제거", "삭제", "복구", "반환", "건너뛰",
		"처리", "검사", "확인한 뒤", "할당", "재시도", "재호출", "호출",
		"사용", "설정", "초기화", "제어 흐름", "데이터 처리", "오류 처리",
	)
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
			path, line := reviewPathAndOptionalLine(strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":")))
			current.Path = path
			if line > 0 {
				current.Line = line
			}
			continue
		}
		if strings.HasPrefix(lower, "line:") || strings.HasPrefix(lower, "lines:") {
			if line := parseReviewFindingLine(strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":"))); line > 0 {
				current.Line = line
			}
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
		if strings.HasPrefix(lower, "resolution_status:") || strings.HasPrefix(lower, "status:") {
			current.ResolutionStatus = strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":"))
			continue
		}
		if strings.HasPrefix(lower, "evidence_refs:") {
			current.EvidenceRefs = parseReviewModelListField(strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":")))
			continue
		}
		if strings.HasPrefix(lower, "fix_refs:") || strings.HasPrefix(lower, "changed_paths:") {
			current.FixRefs = parseReviewModelListField(strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":")))
			continue
		}
		if strings.HasPrefix(lower, "verification_refs:") {
			current.VerificationRefs = parseReviewModelListField(strings.TrimSpace(strings.TrimPrefix(trimmed, strings.SplitN(trimmed, ":", 2)[0]+":")))
			continue
		}
		if severityRe.MatchString(trimmed) && (strings.Contains(trimmed, ":") || strings.Contains(trimmed, "]")) {
			flush()
			current.Severity = normalizeReviewSeverity(severityRe.FindString(trimmed))
			current.Title = cleanReviewModelFieldValue(reviewFallbackFindingTitle(trimmed, severityRe))
			if path := pathRe.FindString(trimmed); path != "" {
				current.Path, current.Line = reviewPathAndOptionalLine(path)
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

func parseReviewModelListField(raw string) []string {
	raw = strings.TrimSpace(cleanReviewModelFieldValue(raw))
	raw = strings.Trim(raw, "[]")
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.Trim(part, "`\"'"))
		if part != "" {
			out = append(out, part)
		}
	}
	return normalizeTaskStateList(out, 12)
}

func reviewPathAndOptionalLine(raw string) (string, int) {
	path := strings.TrimSpace(strings.TrimSuffix(raw, ":"))
	if path == "" {
		return "", 0
	}
	if idx := strings.LastIndex(path, ":"); idx > 0 && idx+1 < len(path) {
		suffix := path[idx+1:]
		if reviewStringIsDigits(suffix) && !(idx == 1 && len(path) >= 2 && isASCIIAlpha(path[0])) {
			line, err := strconv.Atoi(suffix)
			if err == nil && line > 0 {
				return filepathSlash(path[:idx]), line
			}
		}
	}
	return strings.TrimSuffix(filepathSlash(path), ":"), 0
}

func parseReviewFindingLine(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	for _, sep := range []string{"-", ":", ",", " "} {
		if idx := strings.Index(raw, sep); idx > 0 {
			raw = raw[:idx]
			break
		}
	}
	if !reviewStringIsDigits(raw) {
		return 0
	}
	line, err := strconv.Atoi(raw)
	if err != nil || line <= 0 {
		return 0
	}
	return line
}

func reviewStringIsDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isASCIIAlpha(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
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
