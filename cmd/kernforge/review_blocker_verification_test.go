package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func blockerVerificationContainsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestReviewBlockerVerificationDisabled locks the config toggle: empty/auto keep
// the pass enabled, "off" disables it (case- and space-insensitive).
func TestReviewBlockerVerificationDisabled(t *testing.T) {
	if reviewBlockerVerificationDisabled(Config{Review: ReviewHarnessConfig{VerifyBlockers: ""}}) {
		t.Fatal("empty must default to enabled (auto)")
	}
	if reviewBlockerVerificationDisabled(Config{Review: ReviewHarnessConfig{VerifyBlockers: "auto"}}) {
		t.Fatal("auto must be enabled")
	}
	if !reviewBlockerVerificationDisabled(Config{Review: ReviewHarnessConfig{VerifyBlockers: "off"}}) {
		t.Fatal("off must disable the pass")
	}
	if !reviewBlockerVerificationDisabled(Config{Review: ReviewHarnessConfig{VerifyBlockers: "  OFF "}}) {
		t.Fatal("off detection should be case- and space-insensitive")
	}
}

func TestNormalizeReviewVerifyBlockersDefault(t *testing.T) {
	c := ReviewHarnessConfig{}
	normalizeReviewHarnessConfig(&c)
	if c.VerifyBlockers != "auto" {
		t.Fatalf("empty verify_blockers should normalize to auto, got %q", c.VerifyBlockers)
	}
	c2 := ReviewHarnessConfig{VerifyBlockers: " OFF "}
	normalizeReviewHarnessConfig(&c2)
	if c2.VerifyBlockers != "off" {
		t.Fatalf("OFF should normalize to off, got %q", c2.VerifyBlockers)
	}
}

// TestReviewBlockerVerificationTriggerGates locks the scope: verification runs
// only for the hard write/completion gates, not for advisory reviews.
func TestReviewBlockerVerificationTriggerGates(t *testing.T) {
	for _, trigger := range []string{"pre_write", "post_change", "goal_iteration", "  Pre_Write "} {
		if !reviewBlockerVerificationTriggerGates(trigger) {
			t.Fatalf("trigger %q should be a verification gate", trigger)
		}
	}
	for _, trigger := range []string{reviewBeforeFixTrigger, naturalReviewTrigger, "explicit_command", "", "automatic"} {
		if reviewBlockerVerificationTriggerGates(trigger) {
			t.Fatalf("advisory trigger %q must not run verification", trigger)
		}
	}
}

// TestReviewBlockerVerificationGate is the heart of Slice C: a would-be blocking
// model finding only keeps blocking when it is confirmed (or when verification
// was unavailable, the conservative fail-closed default). Refuted and unverified
// findings stop blocking.
func TestReviewBlockerVerificationGate(t *testing.T) {
	mk := func(verified string) ReviewRun {
		f := trustFloorSecurityFinding("medium")
		f.Verified = verified
		return ReviewRun{
			Trigger:   "pre_write",
			ChangeSet: ReviewChangeSet{ChangedPaths: []string{"driver.c"}},
			Findings:  []ReviewFinding{f},
		}
	}
	if g := evaluateReviewGate(mk(reviewFindingVerifiedConfirmed)); len(g.BlockingFindings) == 0 {
		t.Fatal("a confirmed model blocker must still block")
	}
	if g := evaluateReviewGate(mk("")); len(g.BlockingFindings) == 0 {
		t.Fatal("an unrun/unavailable finding must keep blocking (conservative fail-closed default)")
	}
	if g := evaluateReviewGate(mk(reviewFindingVerifiedRefuted)); len(g.BlockingFindings) != 0 {
		t.Fatalf("a refuted finding must not block, got blockers %v", g.BlockingFindings)
	}
	if g := evaluateReviewGate(mk(reviewFindingVerifiedUnverified)); len(g.BlockingFindings) != 0 {
		t.Fatalf("an unverified finding must not block, got blockers %v", g.BlockingFindings)
	}
}

// TestReviewFindingVerificationRejectsBlock guards that only model findings honor
// the verdict: a deterministic finding with the same Verified value is never
// rejected (deterministic checks are trusted and not verified).
func TestReviewFindingVerificationRejectsBlock(t *testing.T) {
	model := trustFloorSecurityFinding("medium")
	model.Verified = reviewFindingVerifiedRefuted
	if !reviewFindingVerificationRejectsBlock(model) {
		t.Fatal("a refuted model finding must be rejected from blocking")
	}
	model.Verified = reviewFindingVerifiedUnverified
	if !reviewFindingVerificationRejectsBlock(model) {
		t.Fatal("an unverified model finding must be rejected from blocking")
	}
	model.Verified = reviewFindingVerifiedConfirmed
	if reviewFindingVerificationRejectsBlock(model) {
		t.Fatal("a confirmed model finding must not be rejected")
	}
	model.Verified = ""
	if reviewFindingVerificationRejectsBlock(model) {
		t.Fatal("an empty Verified must not be rejected (conservative)")
	}
	det := ReviewFinding{Source: "deterministic", Severity: reviewSeverityBlocker, BlocksGate: true, Verified: reviewFindingVerifiedRefuted}
	if reviewFindingVerificationRejectsBlock(det) {
		t.Fatal("a deterministic finding must never be rejected by verification, even if labeled refuted")
	}
}

// TestReviewBlockerVerificationCandidates ensures only model would-be-blockers
// are selected: deterministic findings, already-verified findings, and
// trust-floor warnings are excluded.
func TestReviewBlockerVerificationCandidates(t *testing.T) {
	model := trustFloorSecurityFinding("medium")
	model.ID = "RF-M"
	det := ReviewFinding{
		ID: "RF-D", Source: "deterministic", Severity: reviewSeverityBlocker,
		BlocksGate: true, Quality: reviewFindingQualityComplete, Confidence: "high",
		Title: "det", Evidence: "e", Impact: "i", RequiredFix: "f",
	}
	already := trustFloorSecurityFinding("medium")
	already.ID = "RF-V"
	already.Verified = reviewFindingVerifiedConfirmed
	belowFloor := trustFloorSecurityFinding("low")
	belowFloor.ID = "RF-W"
	run := ReviewRun{
		Trigger:   "pre_write",
		ChangeSet: ReviewChangeSet{ChangedPaths: []string{"driver.c"}},
		Findings:  []ReviewFinding{model, det, already, belowFloor},
	}
	idx := reviewBlockerVerificationCandidates(run)
	if len(idx) != 1 || run.Findings[idx[0]].ID != "RF-M" {
		got := []string{}
		for _, i := range idx {
			got = append(got, run.Findings[i].ID)
		}
		t.Fatalf("expected only RF-M as a candidate, got %v", got)
	}
}

// TestReviewApplyBlockerVerdict locks the per-finding mutation: confirmed keeps
// the severity, refuted demotes to a weak info note, unverified downgrades a
// blocker to a prominent warning.
func TestReviewApplyBlockerVerdict(t *testing.T) {
	c := trustFloorSecurityFinding("medium")
	reviewApplyBlockerVerdict(&c, reviewFindingVerifiedConfirmed)
	if c.Verified != reviewFindingVerifiedConfirmed {
		t.Fatal("confirmed verdict not recorded")
	}
	if !strings.EqualFold(c.Severity, reviewSeverityHigh) {
		t.Fatalf("confirmed should keep severity high, got %s", c.Severity)
	}

	r := trustFloorSecurityFinding("medium")
	r.BlocksGate = true
	reviewApplyBlockerVerdict(&r, reviewFindingVerifiedRefuted)
	if r.BlocksGate {
		t.Fatal("refuted must clear BlocksGate")
	}
	if !strings.EqualFold(r.Severity, reviewSeverityInfo) {
		t.Fatalf("refuted should demote to info, got %s", r.Severity)
	}
	if !strings.EqualFold(r.Quality, reviewFindingQualityWeak) {
		t.Fatalf("refuted should mark quality weak, got %s", r.Quality)
	}

	u := trustFloorSecurityFinding("medium")
	u.Severity = reviewSeverityBlocker
	u.BlocksGate = true
	reviewApplyBlockerVerdict(&u, reviewFindingVerifiedUnverified)
	if u.BlocksGate {
		t.Fatal("unverified must clear BlocksGate")
	}
	if !strings.EqualFold(u.Severity, reviewSeverityHigh) {
		t.Fatalf("unverified blocker should downgrade to high, got %s", u.Severity)
	}
}

// TestReviewApplyBlockerVerification covers the apply step over a candidate set:
// a ran pass writes verdicts (an unaddressed candidate becomes unverified), while
// an unavailable pass leaves every candidate untouched (conservative fail-closed).
func TestReviewApplyBlockerVerification(t *testing.T) {
	mkRun := func() *ReviewRun {
		a := trustFloorSecurityFinding("medium")
		a.ID = "RF-1"
		b := trustFloorSecurityFinding("medium")
		b.ID = "RF-2"
		c := trustFloorSecurityFinding("medium")
		c.ID = "RF-3"
		return &ReviewRun{
			Trigger:   "pre_write",
			ChangeSet: ReviewChangeSet{ChangedPaths: []string{"driver.c"}},
			Findings:  []ReviewFinding{a, b, c},
		}
	}

	run := mkRun()
	rec := &ReviewBlockerVerification{}
	reviewApplyBlockerVerification(run, []int{0, 1, 2}, blockerVerificationOutcome{
		Status: reviewBlockerVerificationStatusRan,
		Verdicts: map[string]string{
			"RF-1": reviewFindingVerifiedConfirmed,
			"RF-2": reviewFindingVerifiedRefuted,
		},
	}, rec)
	if run.Findings[0].Verified != reviewFindingVerifiedConfirmed {
		t.Fatalf("RF-1 should be confirmed, got %q", run.Findings[0].Verified)
	}
	if run.Findings[1].Verified != reviewFindingVerifiedRefuted {
		t.Fatalf("RF-2 should be refuted, got %q", run.Findings[1].Verified)
	}
	if run.Findings[2].Verified != reviewFindingVerifiedUnverified {
		t.Fatalf("RF-3 (unaddressed) should default to unverified, got %q", run.Findings[2].Verified)
	}
	if len(rec.ConfirmedIDs) != 1 || len(rec.RefutedIDs) != 1 || len(rec.UnverifiedIDs) != 1 {
		t.Fatalf("record counts wrong: %+v", rec)
	}

	run2 := mkRun()
	rec2 := &ReviewBlockerVerification{}
	reviewApplyBlockerVerification(run2, []int{0, 1, 2}, blockerVerificationOutcome{
		Status: reviewBlockerVerificationStatusUnavailable,
	}, rec2)
	for _, f := range run2.Findings {
		if f.Verified != "" {
			t.Fatalf("unavailable must leave Verified empty (conservative), got %q on %s", f.Verified, f.ID)
		}
	}
	if len(rec2.CandidateIDs) != 3 {
		t.Fatalf("unavailable should still record the 3 candidates, got %d", len(rec2.CandidateIDs))
	}
}

func TestParseBlockerVerificationVerdicts(t *testing.T) {
	raw := strings.Join([]string{
		"VERIFICATION_RESULT",
		"RF-1: confirmed | the diff drops the bounds check",
		"RF-2: refuted | this is a false positive, len is clamped above",
		"RF-3: unverified | cannot determine from the supplied evidence",
		"RF-4: not confirmed | the evidence does not show the call site",
	}, "\n")
	ids := []string{"RF-1", "RF-2", "RF-3", "RF-4"}
	got := parseBlockerVerificationVerdicts(raw, ids)
	if got["RF-1"] != reviewFindingVerifiedConfirmed {
		t.Fatalf("RF-1 should parse confirmed, got %q", got["RF-1"])
	}
	if got["RF-2"] != reviewFindingVerifiedRefuted {
		t.Fatalf("RF-2 should parse refuted, got %q", got["RF-2"])
	}
	if got["RF-3"] != reviewFindingVerifiedUnverified {
		t.Fatalf("RF-3 should parse unverified, got %q", got["RF-3"])
	}
	if got["RF-4"] != reviewFindingVerifiedUnverified {
		t.Fatalf("RF-4 'not confirmed' should resolve to unverified, got %q", got["RF-4"])
	}
}

// TestReviewBlockerVerificationDowngradesGate is the end-to-end behavior the
// slice promises: after applying verdicts, a confirmed finding blocks, a refuted
// one becomes a dismissed note, and an unverified one surfaces as a warning.
func TestReviewBlockerVerificationDowngradesGate(t *testing.T) {
	confirmed := trustFloorSecurityFinding("medium")
	confirmed.ID = "RF-C"
	refuted := trustFloorSecurityFinding("medium")
	refuted.ID = "RF-R"
	unverified := trustFloorSecurityFinding("medium")
	unverified.ID = "RF-U"
	reviewApplyBlockerVerdict(&confirmed, reviewFindingVerifiedConfirmed)
	reviewApplyBlockerVerdict(&refuted, reviewFindingVerifiedRefuted)
	reviewApplyBlockerVerdict(&unverified, reviewFindingVerifiedUnverified)
	run := ReviewRun{
		Trigger:   "pre_write",
		ChangeSet: ReviewChangeSet{ChangedPaths: []string{"driver.c"}},
		Findings:  []ReviewFinding{confirmed, refuted, unverified},
	}
	g := evaluateReviewGate(run)
	if !blockerVerificationContainsID(g.BlockingFindings, "RF-C") {
		t.Fatalf("confirmed finding should block, blockers=%v", g.BlockingFindings)
	}
	if blockerVerificationContainsID(g.BlockingFindings, "RF-R") || blockerVerificationContainsID(g.BlockingFindings, "RF-U") {
		t.Fatalf("refuted/unverified findings must not block, blockers=%v", g.BlockingFindings)
	}
	if !blockerVerificationContainsID(g.WarningFindings, "RF-U") {
		t.Fatalf("unverified finding should surface as a warning, warnings=%v", g.WarningFindings)
	}
	if blockerVerificationContainsID(g.WarningFindings, "RF-R") {
		t.Fatalf("refuted finding should be a dismissed info note, not a warning, warnings=%v", g.WarningFindings)
	}
}

// TestReviewBlockerVerificationRefutesBlockerEndToEnd drives the full harness
// with a scripted model: the second pass produces a blocker, then the scripted
// verification reply refutes it, so the gate no longer blocks. This locks the
// parse-and-apply wiring through runReviewHarness, not just the pure helpers.
func TestReviewBlockerVerificationRefutesBlockerEndToEnd(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			approvedReviewResponse("main first-pass review found no blockers"),
			{Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: needs_revision",
				"summary: second pass found a blocker",
				"findings:",
				"- severity: blocker",
				"  category: correctness",
				"  path: main.go",
				"  line: 3",
				"  symbol: main",
				"  title: Startup path drops the required initialization",
				"  evidence: The diff changes main without calling the initialization helper mentioned in the request.",
				"  impact: The program can start without required initialization.",
				"  required_fix: Call the initialization helper before returning from main.",
				"  test_recommendation: Add a startup behavior test.",
			}, "\n")}},
			{Message: Message{Role: "assistant", Text: strings.Join([]string{
				"VERIFICATION_RESULT",
				"RF-001: refuted | main does call the initialization helper; the finding misreads the diff",
			}, "\n")}},
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
	run, err := runReviewHarness(context.Background(), agent.reviewHarnessRuntime(root), ReviewHarnessOptions{
		Trigger:             "post_change",
		Target:              reviewTargetChange,
		Request:             "fix main startup initialization",
		ProvidedDiff:        "diff --git a/main.go b/main.go\n@@\n-func main() {}\n+func main() { println(\"ok\") }\n",
		ImplementationReply: "Changed main.go.",
		Paths:               []string{"main.go"},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if run.BlockerVerification == nil || run.BlockerVerification.Status != reviewBlockerVerificationStatusRan {
		t.Fatalf("expected a ran blocker-verification record, got %#v", run.BlockerVerification)
	}
	for _, f := range run.Findings {
		if f.ID == "RF-001" && f.Verified != reviewFindingVerifiedRefuted {
			t.Fatalf("RF-001 should be marked refuted by verification, got %q", f.Verified)
		}
	}
	if len(run.Gate.BlockingFindings) != 0 {
		t.Fatalf("a refuted blocker must not block the gate, got blockers %v", run.Gate.BlockingFindings)
	}
}
