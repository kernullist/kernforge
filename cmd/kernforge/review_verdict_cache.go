package main

import (
	"strings"
	"time"
)

// reviewModelDecodeTemperature is the decode temperature for review model calls.
// It is 0 (greedy decode) and always sent explicitly (TemperatureSet) so the
// same prompt is decoded as deterministically as the provider allows. Reasoning
// models may ignore temperature, so this is a best-effort tightening, not a
// guarantee on its own; the verdict cache is the primary determinism lever.
const reviewModelDecodeTemperature = 0.0

// ReviewVerdictCacheEntry records an accepted review verdict and the model
// findings that produced it, keyed by the review fingerprint. On an identical
// change fingerprint with the same primary model, the model review is reused
// instead of re-run, so an unchanged clean change reviews to the same verdict.
// Only accepted (approved / approved_with_warnings) verdicts are stored, so a
// blocking verdict is never frozen.
type ReviewVerdictCacheEntry struct {
	Fingerprint   string          `json:"fingerprint,omitempty"`
	ReviewRunID   string          `json:"review_run_id,omitempty"`
	Verdict       string          `json:"verdict,omitempty"`
	Model         string          `json:"model,omitempty"`
	AcceptedAt    time.Time       `json:"accepted_at,omitempty"`
	ReviewedPaths []string        `json:"reviewed_paths,omitempty"`
	ModelFindings []ReviewFinding `json:"model_findings,omitempty"`
}

// reviewDeterministicVerdictReuseEnabled reports whether accepted-verdict reuse
// is active. Any value other than "off" (including the empty default) keeps it
// on.
func reviewDeterministicVerdictReuseEnabled(cfg Config) bool {
	return !strings.EqualFold(strings.TrimSpace(cfg.Review.Deterministic), "off")
}

// reviewVerdictCacheKey computes the fingerprint that keys the verdict cache. It
// is the same identity as ReviewRun.ReviewFingerprint (target/mode/flow + change
// fingerprint + policy packs + objective), so any change to the diff, scope, or
// policy invalidates the entry.
func reviewVerdictCacheKey(run ReviewRun) string {
	return computeReviewFingerprint(run.Target, run.Mode, run.Flow, run.ChangeSet.Fingerprint, strings.Join(run.PolicyPacks, ","), run.Objective)
}

// lookupReviewVerdictCache returns a reusable accepted entry for the fingerprint
// and primary model, if one is recorded. A stored entry with a different model
// is not reused (a model switch must re-review), and only accepted verdicts are
// returned.
func lookupReviewVerdictCache(rt *runtimeState, fingerprint string, model string) (ReviewVerdictCacheEntry, bool) {
	if rt == nil || rt.session == nil {
		return ReviewVerdictCacheEntry{}, false
	}
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return ReviewVerdictCacheEntry{}, false
	}
	model = strings.TrimSpace(model)
	for _, item := range rt.session.ReviewVerdictCache {
		if !strings.EqualFold(strings.TrimSpace(item.Fingerprint), fingerprint) {
			continue
		}
		if strings.TrimSpace(item.Model) != "" && model != "" &&
			!strings.EqualFold(strings.TrimSpace(item.Model), model) {
			continue
		}
		switch strings.TrimSpace(item.Verdict) {
		case reviewVerdictApproved, reviewVerdictApprovedWithWarnings:
			return item, true
		}
	}
	return ReviewVerdictCacheEntry{}, false
}

// recordReviewVerdictCache stores the model findings and accepted verdict for a
// fingerprint so an identical future change can reuse them. It refuses to cache
// anything but a clean verdict, a degraded run, or a run with a required
// reviewer failure, so a flaky or blocking result is never frozen.
func recordReviewVerdictCache(rt *runtimeState, fingerprint string, run ReviewRun, modelFindings []ReviewFinding, model string) {
	if rt == nil || rt.session == nil {
		return
	}
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return
	}
	verdict := strings.TrimSpace(firstNonBlankString(run.Gate.Verdict, run.Result.Verdict))
	if verdict != reviewVerdictApproved && verdict != reviewVerdictApprovedWithWarnings {
		return
	}
	if run.Result.Degraded {
		return
	}
	if reviewRunHasRequiredReviewerFailure(run) {
		return
	}
	entry := ReviewVerdictCacheEntry{
		Fingerprint:   fingerprint,
		ReviewRunID:   strings.TrimSpace(run.ID),
		Verdict:       verdict,
		Model:         strings.TrimSpace(model),
		AcceptedAt:    time.Now(),
		ReviewedPaths: normalizeTaskStateList(run.ChangeSet.ChangedPaths, 32),
		ModelFindings: cloneReviewFindings(modelFindings),
	}
	rt.session.ReviewVerdictCache = mergeReviewVerdictCache(rt.session.ReviewVerdictCache, entry)
}

func mergeReviewVerdictCache(existing []ReviewVerdictCacheEntry, incoming ReviewVerdictCacheEntry) []ReviewVerdictCacheEntry {
	fingerprint := strings.TrimSpace(incoming.Fingerprint)
	out := make([]ReviewVerdictCacheEntry, 0, len(existing)+1)
	for _, item := range existing {
		if fingerprint != "" && strings.EqualFold(strings.TrimSpace(item.Fingerprint), fingerprint) {
			continue
		}
		out = append(out, item)
	}
	if fingerprint != "" {
		out = append([]ReviewVerdictCacheEntry{incoming}, out...)
	}
	if len(out) > 16 {
		out = out[:16]
	}
	return out
}

// cloneReviewFindings returns a deep-enough copy so a reusing run cannot mutate
// the cached findings in place (the slice fields are copied, not shared).
func cloneReviewFindings(findings []ReviewFinding) []ReviewFinding {
	if len(findings) == 0 {
		return nil
	}
	out := make([]ReviewFinding, len(findings))
	for i, finding := range findings {
		finding.EvidenceRefs = append([]string(nil), finding.EvidenceRefs...)
		finding.FixRefs = append([]string(nil), finding.FixRefs...)
		finding.VerificationRefs = append([]string(nil), finding.VerificationRefs...)
		out[i] = finding
	}
	return out
}

// cachedReviewVerdictReviewerRun is the reviewer-run marker recorded when a
// review reuses a cached verdict, so the artifact transparently shows the model
// was not re-invoked.
func cachedReviewVerdictReviewerRun(entry ReviewVerdictCacheEntry) ReviewReviewerRun {
	return ReviewReviewerRun{
		Role:         "primary_reviewer",
		Kind:         "cached_verdict",
		Model:        strings.TrimSpace(entry.Model),
		StartedAt:    entry.AcceptedAt,
		FinishedAt:   entry.AcceptedAt,
		Status:       "cached",
		ModelQuality: reviewModelQualityUsable,
	}
}
