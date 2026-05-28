package main

import (
	"fmt"
	"strings"
	"time"
)

const singleModelSecondPassRole = "single_model_second_pass"

type SingleModelSecondPassReview struct {
	Enabled       bool      `json:"enabled,omitempty"`
	Fingerprint   string    `json:"fingerprint,omitempty"`
	Status        string    `json:"status,omitempty"`
	CacheHit      bool      `json:"cache_hit,omitempty"`
	Model         string    `json:"model,omitempty"`
	ReviewedAt    time.Time `json:"reviewed_at,omitempty"`
	ReviewedPaths []string  `json:"reviewed_paths,omitempty"`
	FindingCount  int       `json:"finding_count,omitempty"`
	PromptPath    string    `json:"prompt_path,omitempty"`
	RawOutputPath string    `json:"raw_output_path,omitempty"`
}

type SecondPassReviewCacheEntry struct {
	Fingerprint   string    `json:"fingerprint,omitempty"`
	ReviewRunID   string    `json:"review_run_id,omitempty"`
	Model         string    `json:"model,omitempty"`
	Verdict       string    `json:"verdict,omitempty"`
	AcceptedAt    time.Time `json:"accepted_at,omitempty"`
	ReviewedPaths []string  `json:"reviewed_paths,omitempty"`
}

func shouldRunSingleModelSecondPass(run *ReviewRun, mainRun ReviewReviewerRun, mainRaw string) bool {
	if run == nil {
		return false
	}
	if !run.SingleModelPolicy.Enabled {
		return false
	}
	if strings.TrimSpace(mainRaw) == "" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(mainRun.Status), "completed") ||
		!reviewModelQualityUsableOrBetter(mainRun.ModelQuality) ||
		strings.TrimSpace(mainRun.Error) != "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		return true
	}
	if reviewRunHasChangeEvidence(*run) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(run.Target), reviewTargetChange) ||
		strings.EqualFold(strings.TrimSpace(run.Target), reviewTargetFinal) ||
		strings.EqualFold(strings.TrimSpace(run.Target), reviewTargetGoal) {
		return true
	}
	return false
}

func prepareSingleModelSecondPassPlan(run *ReviewRun, label string) {
	if run == nil {
		return
	}
	if run.ModelPlan.AssignedModels == nil {
		run.ModelPlan.AssignedModels = map[string]string{}
	}
	if !reviewStringSliceContainsCI(run.ModelPlan.RequiredRoles, singleModelSecondPassRole) {
		run.ModelPlan.RequiredRoles = append(run.ModelPlan.RequiredRoles, singleModelSecondPassRole)
	}
	run.ModelPlan.AssignedModels[singleModelSecondPassRole] = strings.TrimSpace(label)
	markReviewModelRoleSatisfied(run, singleModelSecondPassRole)
	run.ModelPlan.Strategy = "single_model_second_pass"
}

func singleModelSecondPassFingerprint(run ReviewRun, mainRaw string, mainFindings []ReviewFinding) string {
	parts := []string{
		"single_model_second_pass",
		run.Target,
		run.Mode,
		run.Flow,
		run.Trigger,
		run.Objective,
		run.ChangeSet.Fingerprint,
		strings.Join(run.ChangeSet.ChangedPaths, ","),
		run.ChangeSet.DiffExcerpt,
		run.ImplementationReply,
		run.Evidence.VerificationSummary,
		compactPromptSection(mainRaw, 4000),
	}
	for _, finding := range mainFindings {
		finding.Normalize()
		parts = append(parts, strings.Join([]string{
			finding.ID,
			finding.Severity,
			finding.Category,
			finding.Path,
			finding.Symbol,
			finding.Title,
			finding.RequiredFix,
		}, "|"))
	}
	return computeReviewFingerprint(parts...)
}

func lookupAcceptedSecondPassCache(rt *runtimeState, fingerprint string) (SecondPassReviewCacheEntry, bool) {
	if rt == nil || rt.session == nil {
		return SecondPassReviewCacheEntry{}, false
	}
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return SecondPassReviewCacheEntry{}, false
	}
	for _, item := range rt.session.SecondPassReviewCache {
		if strings.EqualFold(strings.TrimSpace(item.Fingerprint), fingerprint) &&
			strings.EqualFold(strings.TrimSpace(item.Verdict), reviewVerdictApproved) {
			return item, true
		}
		if strings.EqualFold(strings.TrimSpace(item.Fingerprint), fingerprint) &&
			strings.EqualFold(strings.TrimSpace(item.Verdict), reviewVerdictApprovedWithWarnings) {
			return item, true
		}
	}
	return SecondPassReviewCacheEntry{}, false
}

func recordAcceptedSecondPassCache(rt *runtimeState, run ReviewRun, review SingleModelSecondPassReview) {
	if rt == nil || rt.session == nil {
		return
	}
	if strings.TrimSpace(review.Fingerprint) == "" {
		return
	}
	verdict := firstNonBlankString(run.Gate.Verdict, run.Result.Verdict, reviewVerdictApproved)
	if verdict != reviewVerdictApproved && verdict != reviewVerdictApprovedWithWarnings {
		return
	}
	entry := SecondPassReviewCacheEntry{
		Fingerprint:   review.Fingerprint,
		ReviewRunID:   strings.TrimSpace(run.ID),
		Model:         strings.TrimSpace(review.Model),
		Verdict:       verdict,
		AcceptedAt:    time.Now(),
		ReviewedPaths: normalizeTaskStateList(review.ReviewedPaths, 32),
	}
	rt.session.SecondPassReviewCache = mergeSecondPassReviewCache(rt.session.SecondPassReviewCache, entry)
}

func mergeSecondPassReviewCache(existing []SecondPassReviewCacheEntry, incoming SecondPassReviewCacheEntry) []SecondPassReviewCacheEntry {
	out := make([]SecondPassReviewCacheEntry, 0, len(existing)+1)
	fingerprint := strings.TrimSpace(incoming.Fingerprint)
	for _, item := range existing {
		if fingerprint != "" && strings.EqualFold(strings.TrimSpace(item.Fingerprint), fingerprint) {
			continue
		}
		out = append(out, item)
	}
	if fingerprint != "" {
		out = append([]SecondPassReviewCacheEntry{incoming}, out...)
	}
	if len(out) > 16 {
		out = out[:16]
	}
	return out
}

func cachedSingleModelSecondPassRun(entry SecondPassReviewCacheEntry) ReviewReviewerRun {
	return ReviewReviewerRun{
		Role:         singleModelSecondPassRole,
		Kind:         "second_pass",
		Model:        strings.TrimSpace(entry.Model),
		StartedAt:    entry.AcceptedAt,
		FinishedAt:   entry.AcceptedAt,
		Status:       "cached",
		ModelQuality: reviewModelQualityUsable,
	}
}

func buildSingleModelSecondPassReviewPrompt(cfg Config, run ReviewRun, mainRaw string, mainFindings []ReviewFinding) string {
	var b strings.Builder
	if reviewRunPrefersKorean(cfg, run) {
		b.WriteString("단일 모델 강제 두 번째 패스 리뷰입니다.\n")
		b.WriteString("같은 primary model route를 사용하지만, 이 호출은 이전 구현/리뷰 응답과 분리된 별도 runtime phase입니다.\n")
		b.WriteString("아래 원 요청, touched files, diff, 구현 답변, 검증 요약, 1차 리뷰 결과를 독립적으로 다시 검토하세요.\n")
	} else {
		b.WriteString("This is an enforced single-model second-pass review.\n")
		b.WriteString("It uses the primary model route, but this call is a separate runtime phase from the implementation and first review response.\n")
		b.WriteString("Review the original request, touched files, diff, implementation reply, verification summary, and first-pass review independently.\n")
	}
	fmt.Fprintf(&b, "\nReview id: %s\n", run.ID)
	fmt.Fprintf(&b, "Role: %s\n", singleModelSecondPassRole)
	fmt.Fprintf(&b, "Target: %s\n", run.Target)
	fmt.Fprintf(&b, "Mode: %s\n", run.Mode)
	fmt.Fprintf(&b, "Flow: %s\n", run.Flow)
	appendReviewLensPromptSection(&b, run.ModelPlan)
	if strings.TrimSpace(run.Objective) != "" {
		fmt.Fprintf(&b, "\nOriginal user request:\n%s\n", run.Objective)
	}
	if len(run.ChangeSet.ChangedPaths) > 0 {
		fmt.Fprintf(&b, "\nTouched files:\n- %s\n", strings.Join(limitStrings(run.ChangeSet.ChangedPaths, 64), "\n- "))
	}
	if strings.TrimSpace(run.ChangeSet.DiffExcerpt) != "" {
		b.WriteString("\nRelevant diff:\n")
		b.WriteString(compactReviewPromptSection(run.ChangeSet.DiffExcerpt, 24000))
		b.WriteString("\n")
	}
	if strings.TrimSpace(run.ImplementationReply) != "" {
		b.WriteString("\nImplementation reply:\n")
		b.WriteString(compactReviewPromptSection(run.ImplementationReply, 6000))
		b.WriteString("\n")
	}
	if strings.TrimSpace(run.Evidence.VerificationSummary) != "" {
		b.WriteString("\nLatest verification summary:\n")
		b.WriteString(compactReviewPromptSection(run.Evidence.VerificationSummary, 6000))
		b.WriteString("\n")
	} else {
		b.WriteString("\nLatest verification summary:\n(no verification summary available)\n")
	}
	if len(mainFindings) > 0 {
		b.WriteString("\nFirst-pass structured findings:\n")
		b.WriteString(compactReviewPromptSection(renderReviewFindingsForCrossPrompt(mainFindings), reviewPrimaryFindingsCrossPromptLimit(run)))
		b.WriteString("\n")
	}
	if strings.TrimSpace(mainRaw) != "" {
		b.WriteString("\nFirst-pass raw review:\n")
		b.WriteString(compactReviewPromptSection(mainRaw, reviewPrimaryRawCrossPromptLimit(run)))
		b.WriteString("\n")
	}
	b.WriteString("\nSecond-pass focus checklist:\n")
	b.WriteString("- touched functions and nearby call sites\n")
	b.WriteString("- ABI or data contracts, struct fields, enum values, and serialization compatibility\n")
	b.WriteString("- initialization defaults, zero values, and backward compatibility\n")
	b.WriteString("- buffer sizes, length checks, path normalization, and truncation handling\n")
	b.WriteString("- error paths, cleanup, cancellation, timeout, and retry behavior\n")
	b.WriteString("- logging/output compatibility and operator-visible text changes\n")
	b.WriteString("- stale docs, missing focused validation, and verification claims\n")
	b.WriteString("- whether the implementation reply omitted changed files, review result, validation result, or residual risk\n")
	b.WriteString("\nRequired schema:\n")
	b.WriteString("REVIEW_RESULT\n")
	b.WriteString("verdict: approved|approved_with_warnings|needs_revision|blocked|insufficient_evidence\n")
	b.WriteString("summary: <one paragraph>\n")
	b.WriteString("findings:\n")
	b.WriteString("- severity: blocker|high|medium|low|info\n")
	b.WriteString("  category: correctness|security|stability|performance|test_gap|maintainability|false_positive|bypass_surface|operational_risk|evidence_gap\n")
	b.WriteString("  path: <path or empty>\n")
	b.WriteString("  line: <1-based line number or 0>\n")
	b.WriteString("  symbol: <symbol or surface>\n")
	b.WriteString("  title: <complete short finding title under 120 characters>\n")
	b.WriteString("  evidence: <specific evidence from supplied context>\n")
	b.WriteString("  impact: <why it matters>\n")
	b.WriteString("  required_fix: <concrete fix>\n")
	b.WriteString("  test_recommendation: <specific validation>\n")
	b.WriteString("  resolution_status: <empty unless reconciling an existing finding>\n")
	b.WriteString("  evidence_refs: <comma-separated evidence refs when available>\n")
	b.WriteString("  fix_refs: <comma-separated changed paths or commits when available>\n")
	b.WriteString("  verification_refs: <comma-separated verification refs when available>\n")
	b.WriteString("\nReview evidence:\n")
	b.WriteString(compactReviewPromptSection(run.Evidence.Text, reviewModelCrossEvidenceLimit(run)))
	return b.String()
}
