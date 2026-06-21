package main

import (
	"fmt"
	"strings"
	"time"
)

// reviewDocumentClaimsRole is the reviewer role used for the bounded
// model-based document claims sanity check. It reuses the single-model review
// machinery but with a document-specific persona instead of a code-behavior
// second pass.
const reviewDocumentClaimsRole = "document_claims_check"

// ReviewDocumentClaimsCheck records the bounded model-based correctness/claims
// sanity pass over a generated document artifact. Document-artifact turns
// otherwise rely on a byte-fingerprint and structural artifact-quality checks
// only; this pass adds a lightweight model read of the artifact's claims so
// fabricated or unsupported statements can surface as findings.
type ReviewDocumentClaimsCheck struct {
	Enabled       bool      `json:"enabled,omitempty"`
	Status        string    `json:"status,omitempty"`
	Model         string    `json:"model,omitempty"`
	ReviewedAt    time.Time `json:"reviewed_at,omitempty"`
	ReviewedPaths []string  `json:"reviewed_paths,omitempty"`
	FindingCount  int       `json:"finding_count,omitempty"`
	PromptPath    string    `json:"prompt_path,omitempty"`
	RawOutputPath string    `json:"raw_output_path,omitempty"`
	SkippedReason string    `json:"skipped_reason,omitempty"`
}

// shouldRunDocumentClaimsSanityPass reports whether a bounded model-based
// document claims check should run. It is gated to document-artifact requests
// with a usable first-pass result and some artifact/document evidence, and is
// skipped on pre-write (write-gate) turns and when no model can run.
func shouldRunDocumentClaimsSanityPass(rt *runtimeState, run *ReviewRun, mainRun ReviewReviewerRun, mainRaw string) bool {
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
		return false
	}
	class := normalizeReviewRequestClass(firstNonBlankString(run.RequestClass, run.RequestAnalysis.RequestClass))
	if class != reviewRequestClassDocumentArtifact {
		return false
	}
	return reviewRunHasDocumentClaimsEvidence(*run)
}

func documentClaimsSanityPassSkipReason(run ReviewRun, mainRun ReviewReviewerRun, mainRaw string) string {
	if !run.SingleModelPolicy.Enabled {
		return "single-model review policy is not active"
	}
	if strings.TrimSpace(mainRaw) == "" {
		return "first-pass review output was empty"
	}
	if !strings.EqualFold(strings.TrimSpace(mainRun.Status), "completed") ||
		!reviewModelQualityUsableOrBetter(mainRun.ModelQuality) ||
		strings.TrimSpace(mainRun.Error) != "" {
		return "first-pass review did not complete with usable quality"
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		return "pre-write document review is the write gate; claims sanity pass is deferred"
	}
	if !reviewRunHasDocumentClaimsEvidence(run) {
		return "no document artifact text was available to sanity-check claims"
	}
	return ""
}

// reviewRunHasDocumentClaimsEvidence reports whether there is artifact text or
// document-path evidence worth sanity-checking. It avoids a wasted model call
// when the run carries no document content.
func reviewRunHasDocumentClaimsEvidence(run ReviewRun) bool {
	if strings.TrimSpace(run.Evidence.Text) != "" {
		return true
	}
	for _, path := range run.ChangeSet.ChangedPaths {
		if pathLooksLikeDocumentArtifact(path) || preWritePathLooksLikeGeneratedDocumentArtifact(path) {
			return true
		}
	}
	return false
}

func prepareDocumentClaimsCheckPlan(run *ReviewRun, label string) {
	if run == nil {
		return
	}
	if run.ModelPlan.AssignedModels == nil {
		run.ModelPlan.AssignedModels = map[string]string{}
	}
	if !reviewStringSliceContainsCI(run.ModelPlan.OptionalRoles, reviewDocumentClaimsRole) {
		run.ModelPlan.OptionalRoles = analysisUniqueStrings(append(run.ModelPlan.OptionalRoles, reviewDocumentClaimsRole))
	}
	run.ModelPlan.AssignedModels[reviewDocumentClaimsRole] = strings.TrimSpace(label)
	markReviewModelRoleSatisfied(run, reviewDocumentClaimsRole)
}

// reviewDocumentClaimsModelLabel annotates the route label so the document
// claims pass reads as a same-model artifact check, not independent review.
func reviewDocumentClaimsModelLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "same model (document claims check)"
	}
	if strings.Contains(strings.ToLower(label), "document claims check") {
		return label
	}
	return label + " (document claims check; same model)"
}

// buildDocumentClaimsCheckPrompt builds a bounded prompt that asks the model to
// sanity-check the generated document's claims against the supplied evidence
// instead of reviewing code behavior. It deliberately reuses the structured
// REVIEW_RESULT schema so findings merge into the normal pipeline.
func buildDocumentClaimsCheckPrompt(cfg Config, run ReviewRun, mainRaw string) string {
	var b strings.Builder
	if reviewRunPrefersKorean(cfg, run) {
		b.WriteString("생성된 문서/보고서 산출물에 대한 주장 검증 패스입니다.\n")
		b.WriteString("코드 동작이 아니라 문서가 주장하는 내용이 제공된 근거와 일치하는지, 근거 없이 단정하거나 사실과 다른 부분이 없는지 점검하세요.\n")
		b.WriteString("문서에 없는 사실을 만들어내지 말고, 근거가 부족한 주장은 evidence_gap 또는 maintainability 범주의 finding으로 보고하세요.\n")
	} else {
		b.WriteString("This is a claims sanity-check pass over a generated document/report artifact.\n")
		b.WriteString("Do not review code behavior. Check whether the document's claims are supported by the supplied evidence, and flag fabricated, unsupported, or contradicted statements.\n")
		b.WriteString("Do not invent facts. Report unsupported claims as evidence_gap findings and inaccurate or misleading statements as correctness/maintainability findings.\n")
	}
	fmt.Fprintf(&b, "\nReview id: %s\n", run.ID)
	fmt.Fprintf(&b, "Role: %s\n", reviewDocumentClaimsRole)
	fmt.Fprintf(&b, "Target: %s\n", run.Target)
	if strings.TrimSpace(run.Objective) != "" {
		fmt.Fprintf(&b, "\nOriginal user request:\n%s\n", run.Objective)
	}
	if len(run.ChangeSet.ChangedPaths) > 0 {
		fmt.Fprintf(&b, "\nDocument/changed paths:\n- %s\n", strings.Join(limitStrings(run.ChangeSet.ChangedPaths, 64), "\n- "))
	}
	if strings.TrimSpace(mainRaw) != "" {
		b.WriteString("\nFirst-pass review notes:\n")
		b.WriteString(compactReviewPromptSection(mainRaw, reviewPrimaryRawCrossPromptLimit(run)))
		b.WriteString("\n")
	}
	b.WriteString("\nClaims sanity checklist:\n")
	b.WriteString("- factual claims that are not supported by the supplied evidence\n")
	b.WriteString("- references to files, symbols, line numbers, or commands that do not match the evidence\n")
	b.WriteString("- overstated completeness, coverage, or verification claims\n")
	b.WriteString("- internal contradictions or stale statements\n")
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
