package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// disclosureClaimsRole is the reviewer role used for the bounded model-based
// disclosure claims cross-check. It reuses the single-model review consent and
// budget mechanics but with a disclosure-specific persona: instead of reviewing
// code behavior or document content, it compares the final-answer text's
// explicit CLAIMS against the OBSERVED reality recorded in the existing truth
// sources (patch transaction, verification report, review run, edit-loop).
const disclosureClaimsRole = "disclosure_claims_check"

// disclosureContradiction reason ids. These map to the existing
// final-answer-correction disclosure reasons in
// finalAnswerCorrectionVisibilityFromReport so a detected contradiction blocks
// or corrects the final answer. They are stable machine tokens; user-facing
// language is derived elsewhere.
const (
	disclosureContradictionChangedFiles  = "changed_files"
	disclosureContradictionVerification  = "verification"
	disclosureContradictionReview        = "review"
	disclosureContradictionRemainingRisk = "remaining_risk"
)

// disclosureContradictionFindingTitle is the single blocker title used for every
// disclosure cross-check contradiction. It is recognized by
// codingHarnessFindingRequiresFinalAnswerOnlyRevision and mapped to the
// disclosure_contradiction correction reason. Keeping one title keeps the
// final-answer-only correction path additive and easy to gate.
const disclosureContradictionFindingTitle = "Final answer disclosure contradicts observed reality"

// DisclosureContradiction is one detected mismatch between an explicit
// final-answer claim and the observed reality collected from existing truth
// sources. Reason is one of the disclosureContradiction* ids.
type DisclosureContradiction struct {
	Reason   string `json:"reason,omitempty"`
	Claim    string `json:"claim,omitempty"`
	Observed string `json:"observed,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// DisclosureEvidenceBundle is the OBSERVED reality the disclosure cross-check
// compares the final answer against. Every field is collected from an existing
// truth source via existing accessors; nothing here is recomputed. The bundle is
// the only ground truth the model is allowed to use, so a claim with no support
// in the bundle is a contradiction (fail-closed), not a license to invent facts.
type DisclosureEvidenceBundle struct {
	ChangedPaths         []string `json:"changed_paths,omitempty"`
	UnifiedDiff          string   `json:"unified_diff,omitempty"`
	HasDiff              bool     `json:"has_diff,omitempty"`
	VerificationStatus   string   `json:"verification_status,omitempty"`
	VerificationSummary  string   `json:"verification_summary,omitempty"`
	ReviewVerdict        string   `json:"review_verdict,omitempty"`
	ReviewBlockers       []string `json:"review_blockers,omitempty"`
	ReviewWarnings       []string `json:"review_warnings,omitempty"`
	EditLoopStatus       string   `json:"edit_loop_status,omitempty"`
	EditLoopVerification string   `json:"edit_loop_verification,omitempty"`
	RemainingRisks       []string `json:"remaining_risks,omitempty"`
}

// HasModification reports whether the bundle carries any evidence of a workspace
// modification this turn (changed files or an active edit-loop with changes).
// Question/status-only turns have none, so the check is skipped for them.
func (b DisclosureEvidenceBundle) HasModification() bool {
	if len(b.ChangedPaths) > 0 || b.HasDiff {
		return true
	}
	if strings.TrimSpace(b.EditLoopStatus) != "" && (len(b.RemainingRisks) > 0 || strings.TrimSpace(b.EditLoopVerification) != "") {
		return true
	}
	return false
}

// DisclosureClaimsCheck records the bounded model-based disclosure cross-check.
// It mirrors ReviewDocumentClaimsCheck: presence/shape of the disclosures is
// already gated deterministically elsewhere, this adds a lightweight model read
// that compares the answer's explicit claims against observed reality so a
// false/unsupported claim surfaces as a contradiction. The pass is purely
// additive and fail-closed: it can flag a contradiction (stricter) but never
// relaxes an existing gate, and it is skipped (not fabricated) when no model or
// consent is available.
type DisclosureClaimsCheck struct {
	Enabled        bool                      `json:"enabled,omitempty"`
	Status         string                    `json:"status,omitempty"`
	Model          string                    `json:"model,omitempty"`
	ReviewedAt     time.Time                 `json:"reviewed_at,omitempty"`
	ReviewedPaths  []string                  `json:"reviewed_paths,omitempty"`
	Contradictions []DisclosureContradiction `json:"contradictions,omitempty"`
	SkippedReason  string                    `json:"skipped_reason,omitempty"`
	RedactedInput  bool                      `json:"redacted_input,omitempty"`
}

// disclosureClaimsModelRunner is the seam used to perform the bounded single
// model pass. It is nil by default, which makes the disclosure cross-check skip
// (model unavailable) instead of fabricating a verdict, leaving existing
// behavior unchanged. Tests inject a scripted runner; a future slice wires the
// real review provider through this seam. The runner receives the already
// redacted prompt and must return the raw model text, exactly like the bounded
// single-model review path.
var disclosureClaimsModelRunner func(ctx context.Context, a *Agent, prompt string) (string, error)

// shouldRunDisclosureClaimsCheck reports whether the bounded model-based
// disclosure cross-check should run. It mirrors shouldRunDocumentClaimsSanityPass:
// it runs only when there is a modification this turn AND a non-empty final
// answer that makes disclosure-style claims, it skips trivial/question/status
// turns, and it respects the model_review_consent policy. When no model runner
// is wired, it does not run (skip, do not fabricate).
func (a *Agent) shouldRunDisclosureClaimsCheck(reply string, bundle DisclosureEvidenceBundle) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if strings.TrimSpace(reply) == "" {
		return false
	}
	if disclosureClaimsModelRunner == nil {
		return false
	}
	if finalAnswerCompletenessIsTrivialOrStatusOnly(a.Session) {
		return false
	}
	if !bundle.HasModification() {
		return false
	}
	if !replyMakesDisclosureClaims(reply) {
		return false
	}
	if configModelReviewConsent(a.Config) == modelReviewConsentNever {
		return false
	}
	return true
}

// replyMakesDisclosureClaims reports whether the final answer makes any explicit
// claim about changed files, verification status, review outcome, or remaining
// risk. It reuses the existing reply-claim detectors so the gate stays aligned
// with the deterministic presence checks.
func replyMakesDisclosureClaims(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return false
	}
	if replyClaimsNoFileChanges(lower) {
		return true
	}
	if replyClaimsVerificationSuccess(reply) || replyMentionsVerificationNotRun(reply) || replyMentionsVerificationBlocker(reply) {
		return true
	}
	if replyMentionsRemainingRisk(reply) || replyClaimsNoRemainingRisk(reply) {
		return true
	}
	// A changed-file disclosure ("changed files:", "변경된 파일") is a claim worth
	// cross-checking even when no other signal is present.
	return containsAny(lower, "changed files", "files changed", "변경된 파일", "변경한 파일")
}

// collectDisclosureEvidence gathers the observed reality from existing truth
// sources only. It reuses existing accessors (patch transaction, LastVerification,
// LastReviewRun, current edit-loop) and does not recompute any of them.
func (a *Agent) collectDisclosureEvidence() DisclosureEvidenceBundle {
	bundle := DisclosureEvidenceBundle{}
	if a == nil || a.Session == nil {
		return bundle
	}
	// Patch transaction: changed paths + unified diff.
	bundle.ChangedPaths = currentTurnPatchTransactionChangedPaths(a.Session)
	if len(bundle.ChangedPaths) == 0 {
		if scopeResolved := a.generatedDocumentArtifactScopeResolvedChangedPaths(); len(scopeResolved) > 0 {
			bundle.ChangedPaths = scopeResolved
		}
	}
	if tx := currentTurnPatchTransaction(a.Session); tx != nil {
		diff := strings.TrimSpace(tx.UnifiedDiff())
		if diff != "" {
			bundle.UnifiedDiff = diff
			bundle.HasDiff = true
		}
	}
	// Verification report: status + failure summary.
	if a.Session.LastVerification != nil {
		report := *a.Session.LastVerification
		switch {
		case report.HasFailures():
			bundle.VerificationStatus = "failed"
			bundle.VerificationSummary = report.FailureSummary()
		case report.WasSkipped(), report.WasNotExecuted():
			bundle.VerificationStatus = "not_run"
		case report.HasPassedStep():
			bundle.VerificationStatus = "passed"
		default:
			bundle.VerificationStatus = "unknown"
		}
	}
	// Review run: verdict + blocking/warning findings.
	if a.Session.LastReviewRun != nil {
		run := *a.Session.LastReviewRun
		bundle.ReviewVerdict = strings.TrimSpace(firstNonBlankString(run.Gate.Verdict, run.Result.Verdict))
		bundle.ReviewBlockers = normalizeTaskStateList(run.Gate.BlockingFindings, 16)
		bundle.ReviewWarnings = normalizeTaskStateList(run.Gate.WarningFindings, 16)
	}
	// Edit-loop ledger: status + verification outcome + remaining risk.
	if loop := currentTurnActiveEditLoop(a.Session); loop != nil {
		loop.Normalize()
		bundle.EditLoopStatus = strings.TrimSpace(loop.Status)
		bundle.EditLoopVerification = strings.TrimSpace(loop.VerificationStatus)
		bundle.RemainingRisks = normalizeTaskStateList(loop.RemainingRisks, 16)
	}
	return bundle
}

// buildDisclosureClaimsCheckPrompt builds a bounded prompt that asks the model to
// compare the final answer's explicit claims against the observed evidence
// bundle. It mirrors buildDocumentClaimsCheckPrompt's structure but is keyed on
// the final-answer text plus the observed reality, and uses a small line-based
// DISCLOSURE_CHECK contract instead of the heavy REVIEW_RESULT schema so the
// pass stays self-contained. Both reply and evidence are expected to be redacted
// by the caller before reaching here.
func buildDisclosureClaimsCheckPrompt(cfg Config, reply string, bundle DisclosureEvidenceBundle) string {
	korean := localePrefersKorean(cfg)
	var b strings.Builder
	if korean {
		b.WriteString("최종 답변의 명시적 주장(변경한 파일, 검증 상태, 리뷰 결과, 남은 위험)을 관찰된 실제 상태와 대조하는 검사입니다.\n")
		b.WriteString("코드 동작이나 문서 내용을 리뷰하지 마세요. 답변의 주장이 아래 관찰 근거와 모순되거나 근거가 없는지만 판정하세요.\n")
		b.WriteString("근거에 없는 사실을 만들어내지 마세요. 모순이 없으면 contradiction을 보고하지 마세요.\n")
	} else {
		b.WriteString("This pass cross-checks the final answer's explicit claims (changed files, verification status, review result, remaining risk) against the observed reality.\n")
		b.WriteString("Do not review code behavior or document content. Decide only whether each claim is contradicted by or unsupported by the observed evidence below.\n")
		b.WriteString("Do not invent facts. If there is no contradiction, report no contradictions.\n")
	}
	fmt.Fprintf(&b, "Role: %s\n", disclosureClaimsRole)
	b.WriteString("\nObserved reality:\n")
	if len(bundle.ChangedPaths) > 0 {
		fmt.Fprintf(&b, "- changed files: %s\n", strings.Join(limitStrings(bundle.ChangedPaths, 32), ", "))
	} else {
		b.WriteString("- changed files: none recorded\n")
	}
	if bundle.HasDiff {
		b.WriteString("- patch transaction recorded a unified diff (files were changed)\n")
		b.WriteString("- unified diff excerpt:\n")
		b.WriteString(indentBlock(compactPromptSection(bundle.UnifiedDiff, 1600), "  "))
		b.WriteString("\n")
	} else {
		b.WriteString("- patch transaction recorded no unified diff\n")
	}
	fmt.Fprintf(&b, "- verification status: %s\n", valueOrDefault(bundle.VerificationStatus, "none recorded"))
	if strings.TrimSpace(bundle.VerificationSummary) != "" {
		fmt.Fprintf(&b, "- verification failures:\n%s\n", indentBlock(compactPromptSection(bundle.VerificationSummary, 600), "  "))
	}
	if strings.TrimSpace(bundle.ReviewVerdict) != "" {
		fmt.Fprintf(&b, "- review verdict: %s\n", bundle.ReviewVerdict)
	}
	if len(bundle.ReviewBlockers) > 0 {
		fmt.Fprintf(&b, "- review blockers: %s\n", strings.Join(limitStrings(bundle.ReviewBlockers, 8), " | "))
	}
	if len(bundle.ReviewWarnings) > 0 {
		fmt.Fprintf(&b, "- review warnings: %s\n", strings.Join(limitStrings(bundle.ReviewWarnings, 8), " | "))
	}
	if strings.TrimSpace(bundle.EditLoopStatus) != "" {
		fmt.Fprintf(&b, "- edit loop status: %s\n", bundle.EditLoopStatus)
	}
	if strings.TrimSpace(bundle.EditLoopVerification) != "" {
		fmt.Fprintf(&b, "- edit loop verification: %s\n", bundle.EditLoopVerification)
	}
	if len(bundle.RemainingRisks) > 0 {
		fmt.Fprintf(&b, "- recorded remaining risks: %s\n", strings.Join(limitStrings(bundle.RemainingRisks, 8), " | "))
	}
	b.WriteString("\nFinal answer under check:\n")
	b.WriteString(compactPromptSection(reply, 4000))
	b.WriteString("\n\nReason ids you may use:\n")
	b.WriteString("- changed_files: claim about which files were or were not changed contradicts the observed diff/changed paths\n")
	b.WriteString("- verification: claim about verification/build/test outcome contradicts the recorded verification status\n")
	b.WriteString("- review: claim about review result contradicts the recorded review verdict/blockers\n")
	b.WriteString("- remaining_risk: claim about remaining risk/blockers contradicts the recorded remaining risk\n")
	b.WriteString("\nRequired output contract (emit exactly this shape):\n")
	b.WriteString("DISCLOSURE_CHECK\n")
	b.WriteString("verdict: consistent|contradicted\n")
	b.WriteString("contradictions:\n")
	b.WriteString("- reason: <changed_files|verification|review|remaining_risk>\n")
	b.WriteString("  claim: <the exact claim text from the final answer>\n")
	b.WriteString("  observed: <what the evidence actually shows>\n")
	b.WriteString("When there is no contradiction, emit verdict: consistent and an empty contradictions list.\n")
	return b.String()
}

// parseDisclosureClaimsResult parses the bounded DISCLOSURE_CHECK contract emitted
// by the model into structured contradictions. It is intentionally lenient about
// surrounding prose: it scans for the contradiction blocks and keeps only those
// with a recognized reason id. Unknown reason ids are dropped (never fabricated
// into a block), so a malformed result yields zero contradictions rather than a
// spurious gate.
func parseDisclosureClaimsResult(raw string) []DisclosureContradiction {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []DisclosureContradiction
	var current *DisclosureContradiction
	flush := func() {
		if current != nil && disclosureContradictionReasonValid(current.Reason) {
			out = append(out, *current)
		}
		current = nil
	}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		switch {
		case strings.HasPrefix(lower, "- reason:") || strings.HasPrefix(lower, "reason:"):
			flush()
			reason := normalizeDisclosureContradictionReason(disclosureClaimsLineValue(trimmed, "reason:"))
			current = &DisclosureContradiction{Reason: reason}
		case strings.HasPrefix(lower, "claim:") && current != nil:
			current.Claim = disclosureClaimsLineValue(trimmed, "claim:")
		case strings.HasPrefix(lower, "observed:") && current != nil:
			current.Observed = disclosureClaimsLineValue(trimmed, "observed:")
		case strings.HasPrefix(lower, "detail:") && current != nil:
			current.Detail = disclosureClaimsLineValue(trimmed, "detail:")
		}
	}
	flush()
	return out
}

func disclosureClaimsLineValue(line string, key string) string {
	idx := strings.Index(strings.ToLower(line), key)
	if idx < 0 {
		return ""
	}
	value := line[idx+len(key):]
	value = strings.TrimLeft(value, " \t")
	// Strip a leading list marker if the model prefixed "- reason: x".
	value = strings.TrimPrefix(value, "- ")
	return strings.TrimSpace(value)
}

func normalizeDisclosureContradictionReason(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = strings.TrimPrefix(v, "- ")
	v = strings.ReplaceAll(v, " ", "_")
	v = strings.ReplaceAll(v, "-", "_")
	switch v {
	case disclosureContradictionChangedFiles, "changed_file", "files", "diff":
		return disclosureContradictionChangedFiles
	case disclosureContradictionVerification, "validation", "verify", "build", "test", "tests":
		return disclosureContradictionVerification
	case disclosureContradictionReview, "review_result", "reviews":
		return disclosureContradictionReview
	case disclosureContradictionRemainingRisk, "remaining_risks", "risk", "residual_risk", "remaining_blocker", "remaining_blockers":
		return disclosureContradictionRemainingRisk
	default:
		return ""
	}
}

func disclosureContradictionReasonValid(reason string) bool {
	switch reason {
	case disclosureContradictionChangedFiles,
		disclosureContradictionVerification,
		disclosureContradictionReview,
		disclosureContradictionRemainingRisk:
		return true
	default:
		return false
	}
}

// runDisclosureClaimsCheck performs the bounded model-based disclosure
// cross-check. It redacts the final-answer text and the evidence bundle via the
// existing review redaction machinery before the model reads them, invokes the
// model runner seam, parses the contradictions, and returns the recorded check.
// It never relaxes an existing gate; the only effect of a contradiction is a
// stricter blocker mapped later in buildOutcomeInvariantReport. When the runner
// is unavailable or errors, it returns a skipped check with no contradictions so
// existing behavior is unchanged.
func (a *Agent) runDisclosureClaimsCheck(ctx context.Context, reply string, bundle DisclosureEvidenceBundle) *DisclosureClaimsCheck {
	check := &DisclosureClaimsCheck{
		Enabled:       true,
		Status:        "pending",
		ReviewedPaths: normalizeTaskStateList(bundle.ChangedPaths, 32),
	}
	if disclosureClaimsModelRunner == nil {
		check.Status = "skipped"
		check.SkippedReason = "no disclosure cross-check model runner is wired"
		return check
	}
	redactedReply, replyReport := redactSensitiveText(reply)
	redactedBundle, bundleRedacted := redactDisclosureEvidenceBundle(bundle)
	check.RedactedInput = replyReport.Redacted || bundleRedacted
	prompt := buildDisclosureClaimsCheckPrompt(a.Config, redactedReply, redactedBundle)
	raw, err := disclosureClaimsModelRunner(ctx, a, prompt)
	if err != nil {
		check.Status = "skipped"
		check.SkippedReason = "disclosure cross-check model run failed: " + compactPromptSection(err.Error(), 160)
		return check
	}
	check.Status = "completed"
	check.ReviewedAt = time.Now()
	check.Model = strings.TrimSpace(a.ReviewerModel)
	check.Contradictions = filterDisclosureContradictionsAgainstBundle(parseDisclosureClaimsResult(raw), redactedBundle)
	return check
}

// redactDisclosureEvidenceBundle redacts the free-text fields of the evidence
// bundle (diff and verification summary) before the model reads them, reusing the
// review redaction primitive. Paths and enum-like status tokens are not secrets
// and are left intact.
func redactDisclosureEvidenceBundle(bundle DisclosureEvidenceBundle) (DisclosureEvidenceBundle, bool) {
	redacted := false
	if strings.TrimSpace(bundle.UnifiedDiff) != "" {
		diff, report := redactSensitiveText(bundle.UnifiedDiff)
		bundle.UnifiedDiff = diff
		if report.Redacted {
			redacted = true
		}
	}
	if strings.TrimSpace(bundle.VerificationSummary) != "" {
		summary, report := redactSensitiveText(bundle.VerificationSummary)
		bundle.VerificationSummary = summary
		if report.Redacted {
			redacted = true
		}
	}
	return bundle, redacted
}

// filterDisclosureContradictionsAgainstBundle drops any reported contradiction
// that is not actually supported by the observed bundle, so a model that
// over-claims cannot manufacture a gate. This keeps the pass fail-closed for
// real mismatches while refusing to block on a contradiction the evidence does
// not back. The model is the detector; the bundle is the arbiter.
func filterDisclosureContradictionsAgainstBundle(contradictions []DisclosureContradiction, bundle DisclosureEvidenceBundle) []DisclosureContradiction {
	if len(contradictions) == 0 {
		return nil
	}
	out := make([]DisclosureContradiction, 0, len(contradictions))
	seen := map[string]bool{}
	for _, c := range contradictions {
		if !disclosureContradictionReasonValid(c.Reason) {
			continue
		}
		if !disclosureContradictionSupportedByBundle(c.Reason, bundle) {
			continue
		}
		if seen[c.Reason] {
			continue
		}
		seen[c.Reason] = true
		out = append(out, c)
	}
	return out
}

// disclosureContradictionSupportedByBundle reports whether the observed bundle
// carries enough recorded state for the given contradiction reason to be a real
// mismatch (as opposed to a hallucinated one). It is deliberately permissive:
// it requires only that the relevant truth source recorded something to
// contradict, not that it re-derive the mismatch.
func disclosureContradictionSupportedByBundle(reason string, bundle DisclosureEvidenceBundle) bool {
	switch reason {
	case disclosureContradictionChangedFiles:
		return len(bundle.ChangedPaths) > 0 || bundle.HasDiff
	case disclosureContradictionVerification:
		return strings.TrimSpace(bundle.VerificationStatus) != "" || strings.TrimSpace(bundle.EditLoopVerification) != ""
	case disclosureContradictionReview:
		return strings.TrimSpace(bundle.ReviewVerdict) != "" || len(bundle.ReviewBlockers) > 0 || len(bundle.ReviewWarnings) > 0
	case disclosureContradictionRemainingRisk:
		return len(bundle.RemainingRisks) > 0 || len(bundle.ReviewBlockers) > 0
	default:
		return false
	}
}

// disclosureClaimsContradictionFindings maps detected contradictions to coding
// harness blocker findings. Every finding uses the single recognized
// disclosureContradictionFindingTitle so it is classified as a final-answer-only
// revision and routed to the disclosure_contradiction correction reason. The
// findings are additive blockers (stricter); they never downgrade an existing
// gate.
func disclosureClaimsContradictionFindings(check *DisclosureClaimsCheck) []CodingHarnessFinding {
	if check == nil || len(check.Contradictions) == 0 {
		return nil
	}
	var findings []CodingHarnessFinding
	for _, c := range check.Contradictions {
		if !disclosureContradictionReasonValid(c.Reason) {
			continue
		}
		detail := disclosureContradictionDetail(c)
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    disclosureContradictionFindingTitle,
			Detail:   detail,
		})
	}
	return normalizeCodingHarnessFindings(findings)
}

func disclosureContradictionDetail(c DisclosureContradiction) string {
	subject := disclosureContradictionSubject(c.Reason)
	parts := []string{
		"The final answer's " + subject + " claim contradicts the observed reality.",
	}
	if strings.TrimSpace(c.Claim) != "" {
		parts = append(parts, "Claim: "+compactPromptSection(c.Claim, 240)+".")
	}
	if strings.TrimSpace(c.Observed) != "" {
		parts = append(parts, "Observed: "+compactPromptSection(c.Observed, 240)+".")
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func disclosureContradictionSubject(reason string) string {
	switch reason {
	case disclosureContradictionChangedFiles:
		return "changed-file"
	case disclosureContradictionVerification:
		return "verification"
	case disclosureContradictionReview:
		return "review-result"
	case disclosureContradictionRemainingRisk:
		return "remaining-risk"
	default:
		return "disclosure"
	}
}

// disclosureClaimsCheckStatusLine renders the disclosure cross-check state as a
// compact key=value line for the runtime gate ledger detail view.
func disclosureClaimsCheckStatusLine(check *DisclosureClaimsCheck) string {
	if check == nil {
		return "none"
	}
	parts := []string{}
	if status := strings.TrimSpace(check.Status); status != "" {
		parts = append(parts, "status="+status)
	}
	if len(check.Contradictions) > 0 {
		reasons := make([]string, 0, len(check.Contradictions))
		for _, c := range check.Contradictions {
			if r := strings.TrimSpace(c.Reason); r != "" {
				reasons = append(reasons, r)
			}
		}
		parts = append(parts, fmt.Sprintf("contradictions=%d", len(check.Contradictions)))
		if len(reasons) > 0 {
			parts = append(parts, "reasons="+strings.Join(analysisUniqueStrings(reasons), ","))
		}
	}
	if check.RedactedInput {
		parts = append(parts, "redacted=true")
	}
	if reason := strings.TrimSpace(check.SkippedReason); reason != "" {
		parts = append(parts, "skip="+compactPromptSection(reason, 80))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}
