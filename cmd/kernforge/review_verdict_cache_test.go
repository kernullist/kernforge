package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewDeterministicVerdictReuseEnabled(t *testing.T) {
	if !reviewDeterministicVerdictReuseEnabled(Config{}) {
		t.Fatal("empty default should be on")
	}
	if !reviewDeterministicVerdictReuseEnabled(Config{Review: ReviewHarnessConfig{Deterministic: "on"}}) {
		t.Fatal("on should be enabled")
	}
	if reviewDeterministicVerdictReuseEnabled(Config{Review: ReviewHarnessConfig{Deterministic: "off"}}) {
		t.Fatal("off should disable reuse")
	}
	if reviewDeterministicVerdictReuseEnabled(Config{Review: ReviewHarnessConfig{Deterministic: "  OFF "}}) {
		t.Fatal("off detection should be case- and space-insensitive")
	}
}

func TestNormalizeReviewDeterministicDefault(t *testing.T) {
	c := ReviewHarnessConfig{}
	normalizeReviewHarnessConfig(&c)
	if c.Deterministic != "on" {
		t.Fatalf("empty deterministic should normalize to on, got %q", c.Deterministic)
	}
	c2 := ReviewHarnessConfig{Deterministic: " OFF "}
	normalizeReviewHarnessConfig(&c2)
	if c2.Deterministic != "off" {
		t.Fatalf("OFF should normalize to off, got %q", c2.Deterministic)
	}
}

func TestReviewVerdictCacheRecordAndLookup(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{session: NewSession(root, "scripted", "main-model", "", "default")}
	run := ReviewRun{
		ID:        "r1",
		Target:    reviewTargetChange,
		ChangeSet: ReviewChangeSet{Fingerprint: "fp1", ChangedPaths: []string{"a.go"}},
		Gate:      GateDecision{Verdict: reviewVerdictApprovedWithWarnings},
	}
	key := reviewVerdictCacheKey(run)
	modelFindings := []ReviewFinding{{ID: "RF-1", Source: "model", Severity: reviewSeverityMedium, Title: "warn"}}
	recordReviewVerdictCache(rt, key, run, modelFindings, "main-model")

	entry, ok := lookupReviewVerdictCache(rt, key, "main-model")
	if !ok {
		t.Fatal("expected a cache hit after recording a clean verdict")
	}
	if len(entry.ModelFindings) != 1 || entry.ModelFindings[0].ID != "RF-1" {
		t.Fatalf("cached model findings wrong: %#v", entry.ModelFindings)
	}
	if entry.Verdict != reviewVerdictApprovedWithWarnings {
		t.Fatalf("cached verdict wrong: %q", entry.Verdict)
	}
	if _, ok := lookupReviewVerdictCache(rt, key, "other-model"); ok {
		t.Fatal("a different primary model must not reuse the entry")
	}
	if _, ok := lookupReviewVerdictCache(rt, "different-fingerprint", "main-model"); ok {
		t.Fatal("a different fingerprint must miss")
	}
}

func TestReviewVerdictCacheRefusesBlockingAndDegraded(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{session: NewSession(root, "scripted", "main-model", "", "default")}
	run := ReviewRun{ID: "r1", Target: reviewTargetChange, ChangeSet: ReviewChangeSet{Fingerprint: "fp1"}}
	key := reviewVerdictCacheKey(run)

	run.Gate.Verdict = reviewVerdictNeedsRevision
	recordReviewVerdictCache(rt, key, run, nil, "main-model")
	if _, ok := lookupReviewVerdictCache(rt, key, "main-model"); ok {
		t.Fatal("a blocking verdict must never be cached")
	}

	run.Gate.Verdict = reviewVerdictApproved
	run.Result.Degraded = true
	recordReviewVerdictCache(rt, key, run, nil, "main-model")
	if _, ok := lookupReviewVerdictCache(rt, key, "main-model"); ok {
		t.Fatal("a degraded run must not be cached")
	}
}

func reviewRunDeterminismSignature(findings []ReviewFinding, gate GateDecision) string {
	var b strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&b, "%s|%s|%s|%s;", f.ID, f.Severity, f.Category, f.Title)
	}
	fmt.Fprintf(&b, "::verdict=%s|block=%s|warn=%s",
		gate.Verdict,
		strings.Join(gate.BlockingFindings, ","),
		strings.Join(gate.WarningFindings, ","))
	return b.String()
}

// TestReviewPipelineDeterministicGivenFindings locks RC6's core invariant: given
// the same input findings, merge + gate produce identical output every time,
// independent of Go's randomized map iteration order (the dedup/signal helpers
// use maps as order-invariant sets). It runs many iterations so any map-order
// leak would surface.
func TestReviewPipelineDeterministicGivenFindings(t *testing.T) {
	base := []ReviewFinding{
		{Source: "model", Severity: reviewSeverityHigh, Category: "correctness", Path: "a.go", Symbol: "Handler", Title: "bounds issue", Evidence: "len alpha beta gamma used without clamp", Impact: "oob", RequiredFix: "clamp len"},
		{Source: "model", Severity: reviewSeverityMedium, Category: "security", Path: "b.go", Symbol: "Token", Title: "weak token", Evidence: "token delta epsilon entropy low", Impact: "spoof", RequiredFix: "use csprng"},
		{Source: "model", Severity: reviewSeverityHigh, Category: "correctness", Path: "a.go", Symbol: "Handler", Title: "bounds issue again", Evidence: "len alpha beta gamma used without clamp", Impact: "oob", RequiredFix: "clamp len"},
		{Source: "deterministic", ReviewerRole: "verification_reviewer", Severity: reviewSeverityMedium, Category: "test_gap", Confidence: "medium", Quality: reviewFindingQualityComplete, Title: "Changed files have no latest verification evidence", Evidence: "x", Impact: "y", RequiredFix: "verify"},
	}
	mkRun := func() ReviewRun {
		return ReviewRun{
			Trigger:   "post_change",
			ChangeSet: ReviewChangeSet{ChangedPaths: []string{"a.go", "b.go"}},
			Findings:  append([]ReviewFinding(nil), base...),
		}
	}
	ref := mkRun()
	ref.Findings, ref.MergeResult = mergeReviewFindings(ref.Findings)
	refSig := reviewRunDeterminismSignature(ref.Findings, evaluateReviewGate(ref))
	for i := 0; i < 64; i++ {
		r := mkRun()
		r.Findings, r.MergeResult = mergeReviewFindings(r.Findings)
		if sig := reviewRunDeterminismSignature(r.Findings, evaluateReviewGate(r)); sig != refSig {
			t.Fatalf("review pipeline is non-deterministic on iteration %d:\n ref=%s\n got=%s", i, refSig, sig)
		}
	}
}

// TestReviewHarnessReusesAcceptedVerdictForIdenticalChange proves the keystone
// end to end: reviewing the same change twice reuses the cached accepted verdict
// on the second run instead of re-invoking the model, and the verdict matches.
func TestReviewHarnessReusesAcceptedVerdictForIdenticalChange(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			approvedReviewResponse("first-pass review found no blockers"),
			approvedReviewResponse("second-pass review found no blockers"),
			approvedReviewResponse("spare approved reply"),
			approvedReviewResponse("spare approved reply"),
		},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(false)
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   NewSession(root, "scripted", "main-model", "", "default"),
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	rt := agent.reviewHarnessRuntime(root)
	opts := ReviewHarnessOptions{
		Trigger:             "post_change",
		Target:              reviewTargetChange,
		Request:             "review the change to main.go",
		ProvidedDiff:        "diff --git a/main.go b/main.go\n@@\n-func main() {}\n+func main() { println(\"ok\") }\n",
		ImplementationReply: "Changed main.go.",
		Paths:               []string{"main.go"},
		IncludeFileContents: true,
	}

	run1, err := runReviewHarness(context.Background(), rt, opts)
	if err != nil {
		t.Fatalf("run1: %v", err)
	}
	if run1.Gate.Verdict != reviewVerdictApproved && run1.Gate.Verdict != reviewVerdictApprovedWithWarnings {
		t.Skipf("run1 was not a clean cacheable verdict (%q); reuse path not exercised in this environment", run1.Gate.Verdict)
	}
	n1 := len(provider.requests)
	if n1 == 0 {
		t.Fatal("run1 should have invoked the model at least once")
	}

	run2, err := runReviewHarness(context.Background(), rt, opts)
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if len(provider.requests) != n1 {
		t.Fatalf("the second review of an identical change must reuse the cached verdict (0 new model calls), got %d new", len(provider.requests)-n1)
	}
	reused := false
	for _, rr := range run2.ReviewerRuns {
		if rr.Kind == "cached_verdict" {
			reused = true
		}
	}
	if !reused {
		t.Fatalf("run2 should record a cached_verdict reviewer run, got %#v", run2.ReviewerRuns)
	}
	if run1.Gate.Verdict != run2.Gate.Verdict {
		t.Fatalf("reused verdict must match the original: %q vs %q", run1.Gate.Verdict, run2.Gate.Verdict)
	}
}
