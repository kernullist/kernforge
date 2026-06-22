package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// reviewBlockerVerificationRole is the reviewer role used for the independent
// second-opinion pass that corroborates the model findings that would
// hard-block. It reuses the review model machinery but with a focused
// confirm/refute/unverified persona instead of a full code review.
const reviewBlockerVerificationRole = "blocker_verification"

const (
	reviewBlockerVerificationStatusRan          = "ran"
	reviewBlockerVerificationStatusUnavailable  = "unavailable"
	reviewBlockerVerificationStatusSkipped      = "skipped"
	reviewBlockerVerificationPolicyConservative = "conservative_block"
	reviewBlockerVerificationRouteIndependent   = "independent_cross"
	reviewBlockerVerificationRouteSameModel     = "same_model"
)

// ReviewBlockerVerification records the independent blocker-verification pass:
// which model findings would have hard-blocked, the route used to corroborate
// them, and how each was judged. It exists for transparency so an operator can
// see why a confident-looking finding did or did not block.
type ReviewBlockerVerification struct {
	Enabled           bool      `json:"enabled,omitempty"`
	Status            string    `json:"status,omitempty"`
	Route             string    `json:"route,omitempty"`
	Model             string    `json:"model,omitempty"`
	Policy            string    `json:"policy,omitempty"`
	ReviewedAt        time.Time `json:"reviewed_at,omitempty"`
	CandidateIDs      []string  `json:"candidate_ids,omitempty"`
	ConfirmedIDs      []string  `json:"confirmed_ids,omitempty"`
	RefutedIDs        []string  `json:"refuted_ids,omitempty"`
	UnverifiedIDs     []string  `json:"unverified_ids,omitempty"`
	UnavailableReason string    `json:"unavailable_reason,omitempty"`
	PromptPath        string    `json:"prompt_path,omitempty"`
	RawOutputPath     string    `json:"raw_output_path,omitempty"`
}

// blockerVerificationOutcome carries the parsed result of one verification call
// so the (pure, testable) apply step can mutate findings without re-running the
// model.
type blockerVerificationOutcome struct {
	Status            string
	Route             string
	UnavailableReason string
	Verdicts          map[string]string
}

// reviewFindingVerificationRejectsBlock reports whether the blocker-verification
// pass refuted or could-not-confirm this model finding, in which case it must
// not hard-block regardless of severity. A "confirmed" finding (or an empty
// Verified, meaning the pass did not run / was unavailable) falls through to the
// normal gate logic, which is the conservative fail-closed default.
func reviewFindingVerificationRejectsBlock(finding ReviewFinding) bool {
	if !reviewFindingSourceIsModelish(finding) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(finding.Verified)) {
	case reviewFindingVerifiedRefuted, reviewFindingVerifiedUnverified:
		return true
	}
	return false
}

// reviewBlockerVerificationCandidates returns the indices of the model findings
// that would hard-block right now. Deterministic KernForge checks are trusted
// and never verified. Findings already carrying a verdict are skipped so the
// pass is idempotent. The gate logic this calls reads Verified, but every
// candidate has an empty Verified at this point, so it behaves as the
// pre-verification gate.
func reviewBlockerVerificationCandidates(run ReviewRun) []int {
	var idx []int
	for i := range run.Findings {
		finding := run.Findings[i]
		if !reviewFindingSourceIsModelish(finding) {
			continue
		}
		if strings.TrimSpace(finding.Verified) != "" {
			continue
		}
		if !reviewFindingBlocksGate(run, finding) {
			continue
		}
		idx = append(idx, i)
	}
	return idx
}

// reviewApplyBlockerVerdict mutates a single finding to reflect the verification
// verdict. Confirmed leaves it blocking; refuted demotes it to a dismissed note;
// unverified downgrades it to a non-blocking warning.
func reviewApplyBlockerVerdict(finding *ReviewFinding, verdict string) {
	if finding == nil {
		return
	}
	switch verdict {
	case reviewFindingVerifiedConfirmed:
		finding.Verified = reviewFindingVerifiedConfirmed
	case reviewFindingVerifiedRefuted:
		finding.Verified = reviewFindingVerifiedRefuted
		finding.BlocksGate = false
		finding.Quality = reviewFindingQualityWeak
		finding.Confidence = "low"
		// Demote a refuted blocker to an info note so it is preserved for
		// transparency but no longer competes with real warnings.
		finding.Severity = reviewSeverityInfo
	case reviewFindingVerifiedUnverified:
		finding.Verified = reviewFindingVerifiedUnverified
		finding.BlocksGate = false
		// Keep an uncorroborated blocker visible as a prominent warning rather
		// than a silent note: only high/medium/low severities count as warnings.
		if strings.EqualFold(finding.Severity, reviewSeverityBlocker) {
			finding.Severity = reviewSeverityHigh
		}
	}
}

// reviewApplyBlockerVerification writes verdicts onto the candidate findings and
// records the outcome. It is pure and fully testable. When the pass was
// unavailable, candidates are left untouched (Verified stays empty), which is
// the conservative fail-closed default: they keep blocking via the normal gate.
func reviewApplyBlockerVerification(run *ReviewRun, candidateIdx []int, outcome blockerVerificationOutcome, record *ReviewBlockerVerification) {
	if run == nil || record == nil {
		return
	}
	for _, i := range candidateIdx {
		if i < 0 || i >= len(run.Findings) {
			continue
		}
		id := run.Findings[i].ID
		record.CandidateIDs = append(record.CandidateIDs, id)
		if outcome.Status != reviewBlockerVerificationStatusRan {
			// Unavailable / did not run: fail closed. Leave Verified empty so the
			// finding keeps blocking through the unchanged gate logic.
			continue
		}
		verdict := outcome.Verdicts[id]
		if verdict == "" {
			// The pass ran but did not address this candidate: treat as
			// uncorroborated (warning), not as a confirmed blocker.
			verdict = reviewFindingVerifiedUnverified
		}
		reviewApplyBlockerVerdict(&run.Findings[i], verdict)
		switch verdict {
		case reviewFindingVerifiedConfirmed:
			record.ConfirmedIDs = append(record.ConfirmedIDs, id)
		case reviewFindingVerifiedRefuted:
			record.RefutedIDs = append(record.RefutedIDs, id)
		case reviewFindingVerifiedUnverified:
			record.UnverifiedIDs = append(record.UnverifiedIDs, id)
		}
	}
}

// classifyBlockerVerdictLine maps one response line to a verdict. Refuted is
// checked before unverified, and unverified before confirmed, so "not confirmed"
// resolves to unverified and "false positive" resolves to refuted. The
// verification prompt asks for the exact English tokens; the extra phrases are a
// tolerance layer.
func classifyBlockerVerdictLine(line string) string {
	lower := strings.ToLower(line)
	if containsAny(lower,
		"refuted", "refute", "false positive", "false-positive",
		"not a real", "not a bug", "not an issue", "not a problem",
		"does not hold", "doesn't hold", "no issue", "not valid", "invalid",
		"incorrect", "오탐", "사실이 아", "성립하지", "해당 없", "결함 아니", "문제 없") {
		return reviewFindingVerifiedRefuted
	}
	if containsAny(lower,
		"unverified", "unconfirmed", "not confirmed", "cannot confirm",
		"can't confirm", "could not confirm", "couldn't confirm",
		"cannot determine", "can't determine", "uncertain", "unclear",
		"unknown", "insufficient", "not enough", "확인 불가", "불확실",
		"판단 어려", "근거 부족", "알 수 없") {
		return reviewFindingVerifiedUnverified
	}
	if containsAny(lower,
		"confirmed", "confirm", "holds", "valid", "is a real",
		"real bug", "real issue", "true positive", "substantiated",
		"correct", "확인됨", "유효", "실제 결함", "맞는 지적") {
		return reviewFindingVerifiedConfirmed
	}
	return ""
}

// parseBlockerVerificationVerdicts maps each candidate finding id to a verdict
// by scanning the response line by line. A line is attributed to the first
// candidate id whose identifier token it contains.
func parseBlockerVerificationVerdicts(raw string, candidateIDs []string) map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(raw) == "" || len(candidateIDs) == 0 {
		return out
	}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, id := range candidateIDs {
			if strings.TrimSpace(id) == "" {
				continue
			}
			if _, done := out[id]; done {
				continue
			}
			if !reviewTextContainsIdentifierToken(trimmed, id) {
				continue
			}
			verdict := classifyBlockerVerdictLine(trimmed)
			if verdict != "" {
				out[id] = verdict
			}
			break
		}
	}
	return out
}

// reviewBlockerVerificationModelLabel annotates the route label so a same-model
// verification pass never reads as an independent cross reviewer.
func reviewBlockerVerificationModelLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "same model (blocker verification)"
	}
	if strings.Contains(strings.ToLower(label), "blocker verification") {
		return label
	}
	return label + " (blocker verification; same model)"
}

func prepareBlockerVerificationPlan(run *ReviewRun, label string) {
	if run == nil {
		return
	}
	if run.ModelPlan.AssignedModels == nil {
		run.ModelPlan.AssignedModels = map[string]string{}
	}
	if !reviewStringSliceContainsCI(run.ModelPlan.OptionalRoles, reviewBlockerVerificationRole) {
		run.ModelPlan.OptionalRoles = analysisUniqueStrings(append(run.ModelPlan.OptionalRoles, reviewBlockerVerificationRole))
	}
	run.ModelPlan.AssignedModels[reviewBlockerVerificationRole] = strings.TrimSpace(label)
	markReviewModelRoleSatisfied(run, reviewBlockerVerificationRole)
}

// buildBlockerVerificationPrompt asks the model to independently confirm or
// refute the would-be-blocking findings against the supplied diff and evidence.
// It is deliberately skeptical: an unsupported claim must resolve to refuted or
// unverified so a confident-but-wrong finding stops blocking.
func buildBlockerVerificationPrompt(cfg Config, run ReviewRun, candidates []ReviewFinding) string {
	var b strings.Builder
	korean := reviewRunPrefersKorean(cfg, run)
	if korean {
		b.WriteString("차단성(blocker) 후보 finding에 대한 독립 검증 패스입니다.\n")
		b.WriteString("아래 finding들은 코드 적용/완료를 막을 수 있는 후보입니다. 각 finding이 제공된 diff와 근거에 비추어 실제로 성립하는지 회의적으로(skeptical) 재검토하세요.\n")
		b.WriteString("근거가 분명히 뒷받침할 때만 confirmed, 명백히 틀렸으면 refuted, 근거로 확정할 수 없으면 unverified로 판정하세요. 없는 사실을 만들지 마세요.\n")
	} else {
		b.WriteString("This is an independent verification pass over would-be blocking findings.\n")
		b.WriteString("Each finding below could stop a code write or completion. Skeptically re-check whether it genuinely holds against the supplied diff and evidence.\n")
		b.WriteString("Answer confirmed only when the evidence clearly substantiates it, refuted when it is clearly wrong, and unverified when you cannot confirm it from the evidence. Do not invent facts.\n")
	}
	fmt.Fprintf(&b, "\nReview id: %s\n", run.ID)
	fmt.Fprintf(&b, "Role: %s\n", reviewBlockerVerificationRole)
	fmt.Fprintf(&b, "Target: %s\n", run.Target)
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
	b.WriteString("\nFindings to verify:\n")
	for _, finding := range candidates {
		fmt.Fprintf(&b, "- id: %s\n", strings.TrimSpace(finding.ID))
		fmt.Fprintf(&b, "  severity: %s\n", strings.TrimSpace(finding.Severity))
		fmt.Fprintf(&b, "  category: %s\n", strings.TrimSpace(finding.Category))
		if strings.TrimSpace(finding.Path) != "" {
			fmt.Fprintf(&b, "  path: %s\n", strings.TrimSpace(finding.Path))
		}
		if strings.TrimSpace(finding.Symbol) != "" {
			fmt.Fprintf(&b, "  symbol: %s\n", strings.TrimSpace(finding.Symbol))
		}
		fmt.Fprintf(&b, "  title: %s\n", compactReviewPromptSection(finding.Title, 400))
		if strings.TrimSpace(finding.Evidence) != "" {
			fmt.Fprintf(&b, "  evidence: %s\n", compactReviewPromptSection(finding.Evidence, 1200))
		}
		if strings.TrimSpace(finding.Impact) != "" {
			fmt.Fprintf(&b, "  impact: %s\n", compactReviewPromptSection(finding.Impact, 800))
		}
		if strings.TrimSpace(finding.RequiredFix) != "" {
			fmt.Fprintf(&b, "  required_fix: %s\n", compactReviewPromptSection(finding.RequiredFix, 800))
		}
	}
	b.WriteString("\nRequired output: one line per finding id, nothing else.\n")
	b.WriteString("VERIFICATION_RESULT\n")
	b.WriteString("<finding-id>: confirmed|refuted|unverified | <one line of evidence>\n")
	b.WriteString("\nReview evidence:\n")
	b.WriteString(compactReviewPromptSection(run.Evidence.Text, reviewModelCrossEvidenceLimit(run)))
	return b.String()
}

// reviewBlockerVerificationTriggerGates reports whether this review trigger is a
// hard write/completion gate, where a model finding can actually block an action
// or completion. Advisory reviews (before-fix guidance "pre_fix",
// natural-language review requests "explicit_natural_language", and the explicit
// /review command) surface findings as feedback without trapping the operator,
// and any code they lead to is still verified at the pre_write gate, so spending
// an extra verification call on them is not warranted.
func reviewBlockerVerificationTriggerGates(trigger string) bool {
	switch strings.ToLower(strings.TrimSpace(trigger)) {
	case "pre_write", "post_change", "goal_iteration":
		return true
	default:
		return false
	}
}

// runReviewBlockerVerificationPass corroborates the model findings that would
// hard-block before the gate is computed. It runs at most one bounded model call
// and only when there are model would-be-blockers, so a clean review costs
// nothing. It is inserted after the final merge and before the gate decision.
func runReviewBlockerVerificationPass(ctx context.Context, rt *runtimeState, root string, run *ReviewRun) {
	if rt == nil || rt.agent == nil || run == nil {
		return
	}
	if reviewBlockerVerificationDisabled(rt.cfg) {
		return
	}
	if run.AdvisoryReview {
		// Advisory mode already surfaces every would-be blocker as a warning, so a
		// verification call would add latency without changing the gate.
		return
	}
	if !reviewBlockerVerificationTriggerGates(run.Trigger) {
		// Only the hard write/completion gates can trap the operator on a
		// confident-but-wrong model finding. Advisory reviews (before-fix guidance,
		// natural-language review requests, explicit /review) surface findings as
		// feedback without blocking, and any code they lead to is still verified at
		// the pre_write gate, so an extra verification call there is not warranted.
		return
	}
	candidateIdx := reviewBlockerVerificationCandidates(*run)
	if len(candidateIdx) == 0 {
		return
	}
	candidates := make([]ReviewFinding, 0, len(candidateIdx))
	candidateIDs := make([]string, 0, len(candidateIdx))
	for _, i := range candidateIdx {
		candidates = append(candidates, run.Findings[i])
		candidateIDs = append(candidateIDs, run.Findings[i].ID)
	}

	client, model, label, route, setupErr := reviewBlockerVerificationRoute(rt, *run)
	record := &ReviewBlockerVerification{
		Enabled:    true,
		Route:      route,
		Policy:     reviewBlockerVerificationPolicyConservative,
		ReviewedAt: time.Now(),
	}
	prepareBlockerVerificationPlan(run, label)

	outcome := blockerVerificationOutcome{Route: route}
	if setupErr != nil || client == nil || strings.TrimSpace(model) == "" {
		outcome.Status = reviewBlockerVerificationStatusUnavailable
		outcome.UnavailableReason = "no reviewer model available for blocker verification"
		if setupErr != nil {
			outcome.UnavailableReason = setupErr.Error()
		}
	} else {
		raw, reviewerRun, ok := reviewExecuteBlockerVerificationCall(ctx, rt, root, run, client, model, label, candidates)
		run.ReviewerRuns = append(run.ReviewerRuns, reviewerRun)
		record.PromptPath = reviewerRun.PromptPath
		record.RawOutputPath = reviewerRun.RawOutputPath
		switch {
		case !ok:
			outcome.Status = reviewBlockerVerificationStatusUnavailable
			outcome.UnavailableReason = firstNonBlankString(reviewerRun.Error, "blocker verification call did not return usable output")
		default:
			verdicts := parseBlockerVerificationVerdicts(raw, candidateIDs)
			if len(verdicts) == 0 {
				// The verifier replied but we could not read a verdict for any
				// candidate (off-format or unrelated output). That is no corroboration
				// signal at all, so fail closed (conservative) rather than downgrading
				// every blocker on an unparseable response.
				outcome.Status = reviewBlockerVerificationStatusUnavailable
				outcome.UnavailableReason = "blocker verification returned no parseable verdicts"
			} else {
				outcome.Status = reviewBlockerVerificationStatusRan
				outcome.Verdicts = verdicts
			}
		}
	}

	record.Status = outcome.Status
	// label is already the honest per-route display string: a same-model route is
	// annotated by reviewBlockerVerificationRoute; an independent cross route keeps
	// its own label so it is never mislabeled as same-model.
	record.Model = label
	record.UnavailableReason = outcome.UnavailableReason
	reviewApplyBlockerVerification(run, candidateIdx, outcome, record)
	run.BlockerVerification = record
}

// reviewBlockerVerificationRoute resolves the route for the verification call,
// preferring a genuinely independent cross reviewer and falling back to the main
// model (honestly labeled as a same-model pass).
func reviewBlockerVerificationRoute(rt *runtimeState, run ReviewRun) (ProviderClient, string, string, string, error) {
	mainClient, mainModel, mainLabel, mainErr := reviewMainRoleClient(rt)
	crossClient, crossModel, crossLabel, _, _, hasCross := reviewCrossReviewerClient(rt, run, run.ModelPlan.RequiredRoles, mainClient, mainModel)
	if hasCross && !reviewCrossReviewerFallbackEngaged(rt) {
		return crossClient, crossModel, crossLabel, reviewBlockerVerificationRouteIndependent, nil
	}
	if mainErr != nil || mainClient == nil || strings.TrimSpace(mainModel) == "" {
		return nil, "", strings.TrimSpace(mainLabel), reviewBlockerVerificationRouteSameModel, mainErr
	}
	return mainClient, mainModel, reviewBlockerVerificationModelLabel(mainLabel), reviewBlockerVerificationRouteSameModel, nil
}

// reviewExecuteBlockerVerificationCall makes the single bounded verification call
// and returns the raw response. A failed or empty call returns ok=false, which
// the caller treats as unavailable (conservative fail-closed). It deliberately
// does no omission retries: an indeterminate result never loses a real blocker.
func reviewExecuteBlockerVerificationCall(ctx context.Context, rt *runtimeState, root string, run *ReviewRun, client ProviderClient, model string, label string, candidates []ReviewFinding) (string, ReviewReviewerRun, bool) {
	reviewerRun := ReviewReviewerRun{
		Role:      reviewBlockerVerificationRole,
		Kind:      "verification",
		Model:     label,
		StartedAt: time.Now(),
	}
	reviewerRun.Provider, reviewerRun.ProviderLabel, reviewerRun.ModelID = reviewReviewerRunProviderModel(rt.cfg, reviewBlockerVerificationRole, label, model)
	prompt := buildBlockerVerificationPrompt(rt.cfg, *run, candidates)
	promptPath, rawPath := reviewRoleArtifactPaths(root, run.ID, reviewBlockerVerificationRole)
	_ = os.WriteFile(promptPath, []byte(prompt), 0o644)
	reviewerRun.PromptPath = promptPath
	emitReviewModelRequestProgress(rt, reviewBlockerVerificationRole, label, reviewerRun.Kind)
	softTimeout := reviewModelSoftTimeoutForRun(rt.cfg, *run, reviewerRun)
	callCtx, cancelCall := reviewModelCallContext(ctx, softTimeout)
	resp, err := completeReviewModelTurnWithProgress(callCtx, rt, reviewerRun, func(callCtx context.Context) (ChatResponse, error) {
		return rt.agent.completeModelTurnWithClient(callCtx, client, ChatRequest{
			Model:           model,
			System:          reviewModelSystemPrompt(rt.cfg, *run, reviewBlockerVerificationRole),
			Messages:        []Message{{Role: "user", Text: prompt}},
			MaxTokens:       reviewRoleMaxTokensForRoleRun(rt.cfg, reviewBlockerVerificationRole, *run),
			Temperature:     0.1,
			ReasoningEffort: reviewRoleReasoningEffortForRun(rt.cfg, reviewBlockerVerificationRole, *run),
			WorkingDir:      root,
			CodexSubagent:   openAICodexSubagentReview,
		})
	})
	cancelCall()
	reviewerRun.FinishedAt = time.Now()
	if err != nil {
		reviewerRun.Status = "failed"
		reviewerRun.ModelQuality = reviewModelQualityFailed
		reviewerRun.Error = reviewModelCallErrorText(err, softTimeout)
		finalizeReviewReviewerRunTelemetry(&reviewerRun)
		emitReviewModelResultProgress(rt, reviewerRun, 0)
		return "", reviewerRun, false
	}
	if rawProviderPath, rawProviderRedaction := writeReviewProviderRawResponseArtifact(root, run.ID, reviewBlockerVerificationRole, "", resp.RawBody); rawProviderPath != "" {
		reviewerRun.RawProviderResponsePath = rawProviderPath
		run.Redaction = mergeReviewRedactionReports(run.Redaction, rawProviderRedaction)
	}
	raw := strings.TrimSpace(resp.Message.Text)
	if raw == "" {
		raw = strings.TrimSpace(reviewStructuredOutputFromReasoningContent(rt.cfg, reviewerRun, resp.Message.ReasoningContent))
	}
	if raw == "" {
		reviewerRun.Status = "failed"
		reviewerRun.ModelQuality = reviewModelQualityFailed
		reviewerRun.Error = "blocker verification returned empty response"
		finalizeReviewReviewerRunTelemetry(&reviewerRun)
		emitReviewModelResultProgress(rt, reviewerRun, 0)
		return "", reviewerRun, false
	}
	redacted, rawRedaction := redactSensitiveText(raw)
	_ = os.WriteFile(rawPath, []byte(redacted), 0o644)
	reviewerRun.RawOutputPath = rawPath
	run.Redaction = mergeReviewRedactionReports(run.Redaction, rawRedaction)
	reviewerRun.Status = "completed"
	reviewerRun.ModelQuality = reviewModelQualityUsable
	finalizeReviewReviewerRunTelemetry(&reviewerRun)
	emitReviewModelResultProgress(rt, reviewerRun, len(candidates))
	return redacted, reviewerRun, true
}
