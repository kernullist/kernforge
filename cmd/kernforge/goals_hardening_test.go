package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSplitGoalUserCriteria(t *testing.T) {
	got := splitGoalUserCriteria("detects bypass A; detects bypass B\n- no false positives\n1. build passes")
	want := []string{"detects bypass A", "detects bypass B", "no false positives", "build passes"}
	if len(got) != len(want) {
		t.Fatalf("unexpected count: got %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d: got %q want %q", i, got[i], want[i])
		}
	}
	if len(splitGoalUserCriteria("   \n  ;  ")) != 0 {
		t.Fatalf("expected empty result for blank input")
	}
}

func TestParseGoalStartOptionsHardeningFlags(t *testing.T) {
	rt := &runtimeState{workspace: Workspace{Root: t.TempDir()}}

	opts, err := rt.parseGoalStartOptions([]string{"--gated", "--require-review", "--criteria", "a;b", "finish", "the", "thing"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !opts.GatedPermissions {
		t.Fatalf("expected gated permissions")
	}
	if !opts.RequireIndependentReview {
		t.Fatalf("expected require independent review")
	}
	if len(opts.UserCriteria) != 2 || opts.UserCriteria[0] != "a" || opts.UserCriteria[1] != "b" {
		t.Fatalf("unexpected criteria: %#v", opts.UserCriteria)
	}
	if opts.Objective != "finish the thing" {
		t.Fatalf("unexpected objective: %q", opts.Objective)
	}

	full, err := rt.parseGoalStartOptions([]string{"--full-auto", "objective"})
	if err != nil {
		t.Fatalf("parse full-auto: %v", err)
	}
	if full.GatedPermissions {
		t.Fatalf("--full-auto must not gate permissions")
	}
}

func TestParseGoalReviewDecisionEmptyIsNeedsRevision(t *testing.T) {
	decision := parseGoalReviewDecision("   \n  ")
	if !decision.NeedsRevision || decision.Verdict != "needs_revision" {
		t.Fatalf("empty reviewer verdict must need revision, got %#v", decision)
	}
	// A non-empty, non-committal reply is still allowed to pass (it is not the
	// completion gate); only a truly empty verdict is forced to needs_revision.
	if parseGoalReviewDecision("looks generally fine to me").NeedsRevision {
		t.Fatalf("non-empty ambiguous reply should not be forced to needs_revision")
	}
}

func TestRenderGoalUserCriteriaSection(t *testing.T) {
	// Empty goals still compile a substance criterion so process-only completion
	// cannot be treated as done.
	bare := renderGoalUserCriteriaSection(GoalState{})
	if !strings.Contains(bare, "Acceptance criteria") {
		t.Fatalf("expected Acceptance criteria header, got:\n%s", bare)
	}
	lowerBare := strings.ToLower(bare)
	if !strings.Contains(lowerBare, "objective") && !strings.Contains(lowerBare, "workspace") {
		t.Fatalf("expected compiled substance checklist for empty goal, got:\n%s", bare)
	}
	section := renderGoalUserCriteriaSection(GoalState{
		Objective:    "fix the detector",
		UserCriteria: []string{"criterion one", "criterion two"},
	})
	for _, want := range []string{"Acceptance criteria", "criterion one", "criterion two", "do not treat the goal as complete"} {
		if !strings.Contains(section, want) {
			t.Fatalf("section missing %q:\n%s", want, section)
		}
	}
}

func TestGoalReviewerReplyWasSkipped(t *testing.T) {
	skipped := []string{
		"APPROVED: independent semantic reviewer skipped because no cross review route is configured; relying on completion audit and verification evidence.",
		"APPROVED: model_review_status=x; consent_source=user. Semantic goal review skipped because consent not granted. No reviewer model request was sent; relying on deterministic completion audit and verification evidence.",
	}
	for _, reply := range skipped {
		if !goalReviewerReplyWasSkipped(reply) {
			t.Fatalf("expected skip detection for %q", reply)
		}
	}
	if goalReviewerReplyWasSkipped("APPROVED: the objective is fully implemented and verified") {
		t.Fatalf("a real reviewer verdict must not be flagged as skipped")
	}
}

func TestWithAutonomousGoalPermissionsSelectsMode(t *testing.T) {
	rt := &runtimeState{perms: NewPermissionManager(ModePlan, nil)}

	if err := rt.withAutonomousGoalPermissions(false, func() error {
		if rt.perms.Mode() != ModeBypass {
			t.Fatalf("full-auto must use ModeBypass, got %v", rt.perms.Mode())
		}
		return nil
	}); err != nil {
		t.Fatalf("full-auto envelope: %v", err)
	}
	if err := rt.withAutonomousGoalPermissions(true, func() error {
		if rt.perms.Mode() != ModeAcceptEdits {
			t.Fatalf("gated must use ModeAcceptEdits, got %v", rt.perms.Mode())
		}
		return nil
	}); err != nil {
		t.Fatalf("gated envelope: %v", err)
	}
	if rt.perms.Mode() != ModePlan {
		t.Fatalf("expected the previous mode to be restored, got %v", rt.perms.Mode())
	}
}

func TestRunGoalSemanticReviewRequireReviewBlocksSkippedApproval(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	rt := &runtimeState{
		writer:    &bytes.Buffer{},
		ui:        NewUI(),
		session:   session,
		workspace: Workspace{BaseRoot: root, Root: root},
		goalReply: func(ctx context.Context, prompt string) (string, error) {
			return "APPROVED: independent semantic reviewer skipped because no cross review route is configured; relying on completion audit and verification evidence.", nil
		},
	}
	useFastGoalRuntime(t, rt)
	audit := CompletionAuditArtifact{Ready: true}

	// Default: skipped review still auto-approves, but is labeled as skipped.
	relaxed, err := rt.runGoalSemanticReview(context.Background(), GoalState{ID: "g"}, audit, GoalIteration{Index: 1})
	if err != nil {
		t.Fatalf("semantic review: %v", err)
	}
	if !relaxed.Approved || !relaxed.IndependentReviewSkipped {
		t.Fatalf("default skipped review should approve but be labeled skipped, got %#v", relaxed)
	}

	// --require-review: a skipped review may not auto-approve completion.
	strict, err := rt.runGoalSemanticReview(context.Background(), GoalState{ID: "g", RequireIndependentReview: true}, audit, GoalIteration{Index: 1})
	if err != nil {
		t.Fatalf("semantic review: %v", err)
	}
	if strict.Approved || strict.Verdict != "needs_revision" || !strict.IndependentReviewSkipped {
		t.Fatalf("require-review must block a skipped approval, got %#v", strict)
	}
}

func TestGoalAbsoluteIterationCeilingBlocksLoop(t *testing.T) {
	prev := goalAbsoluteIterationCeiling
	goalAbsoluteIterationCeiling = 3
	t.Cleanup(func() { goalAbsoluteIterationCeiling = prev })

	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	goal := GoalState{ID: "goal-ceiling", Objective: "never-ending objective", Status: goalStatusActive, Iteration: 3}
	goal.Normalize()
	session.UpsertGoal(goal)
	rt := &runtimeState{
		writer:    &bytes.Buffer{},
		ui:        NewUI(),
		session:   session,
		store:     NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{BaseRoot: root, Root: root},
		goalReply: func(ctx context.Context, prompt string) (string, error) {
			t.Fatalf("loop must not run another iteration past the absolute ceiling")
			return "", nil
		},
	}
	if err := rt.runGoalLoop(context.Background(), goal.ID); err != nil {
		t.Fatalf("runGoalLoop: %v", err)
	}
	current, _ := session.ActiveGoal()
	if current.Status != goalStatusBlocked || !strings.Contains(current.LastError, "absolute safety ceiling") {
		t.Fatalf("expected absolute-ceiling block, got %#v", current)
	}
}

func TestGoalWallClockCeilingBlocksLoop(t *testing.T) {
	// The wall-clock backstop is a per-run boundary check, so it trips once real
	// time has elapsed across at least one iteration. Run one fast iteration with a
	// 1ns ceiling and assert the next boundary blocks on wall-clock before the
	// semantic-reject threshold (3) is reached.
	prev := goalDefaultWallClockCeiling
	goalDefaultWallClockCeiling = time.Nanosecond
	t.Cleanup(func() { goalDefaultWallClockCeiling = prev })

	root := t.TempDir()
	writeGoalTestModule(t, root)
	session := NewSession(root, "provider", "model", "", "default")
	rt := &runtimeState{
		writer:        &bytes.Buffer{},
		ui:            NewUI(),
		session:       session,
		store:         NewSessionStore(filepath.Join(root, "sessions")),
		verifyHistory: &VerificationHistoryStore{Path: filepath.Join(root, "verify-history.json"), MaxEntries: defaultVerificationHistoryMaxEntries},
		workspace: Workspace{
			BaseRoot:     root,
			Root:         root,
			Shell:        defaultShell(),
			ShellTimeout: 30 * time.Second,
		},
		goalReply: func(ctx context.Context, prompt string) (string, error) {
			if strings.Contains(prompt, "Final semantic goal review") {
				return "NEEDS_REVISION: keep going", nil
			}
			return "fake goal agent reply", nil
		},
	}
	useFastGoalRuntime(t, rt)

	if err := rt.handleGoalCommand("--run finish sample objective"); err != nil {
		t.Fatalf("handleGoalCommand: %v", err)
	}
	current, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if current.Status != goalStatusBlocked || !strings.Contains(current.LastError, "wall-clock safety ceiling") {
		t.Fatalf("expected wall-clock block, got %#v", current)
	}
	if current.Iteration < 1 || current.Iteration >= goalSemanticRejectBlockThreshold {
		t.Fatalf("expected wall-clock block after 1-2 iterations, got iteration %d", current.Iteration)
	}
}

func TestHasIndependentReviewerRouteNilAgent(t *testing.T) {
	rt := &runtimeState{}
	if rt.hasIndependentReviewerRoute() {
		t.Fatalf("no agent must mean no independent reviewer route")
	}
}

func TestBuildGoalSemanticSelfReviewPrompt(t *testing.T) {
	out := buildGoalSemanticSelfReviewPrompt("BASE PROMPT BODY")
	for _, want := range []string{"no independent reviewer", "adversarial", "NEEDS_REVISION", "BASE PROMPT BODY"} {
		if !strings.Contains(out, want) {
			t.Fatalf("self-review prompt missing %q:\n%s", want, out)
		}
	}
}

func TestGoalRequireReviewWithoutRouteFailsFast(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	goal := GoalState{ID: "goal-req", Objective: "do the thing", Status: goalStatusActive, RequireIndependentReview: true}
	goal.Normalize()
	session.UpsertGoal(goal)
	rt := &runtimeState{
		writer:    &bytes.Buffer{},
		ui:        NewUI(),
		session:   session,
		store:     NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{BaseRoot: root, Root: root},
		// goalReply nil and agent nil => no independent reviewer route.
		goalReply: nil,
	}
	if err := rt.runGoalBySelector(goal.ID, 0); err != nil {
		t.Fatalf("runGoalBySelector: %v", err)
	}
	current, _ := session.ActiveGoal()
	if current.Status != goalStatusBlocked || !strings.Contains(current.LastError, "--require-review") {
		t.Fatalf("expected fail-fast require-review block, got %#v", current)
	}
}

func TestGoalSemanticRejectBoundBlocksLoop(t *testing.T) {
	root := t.TempDir()
	writeGoalTestModule(t, root)
	session := NewSession(root, "provider", "model", "", "default")
	rt := &runtimeState{
		writer:        &bytes.Buffer{},
		ui:            NewUI(),
		session:       session,
		store:         NewSessionStore(filepath.Join(root, "sessions")),
		verifyHistory: &VerificationHistoryStore{Path: filepath.Join(root, "verify-history.json"), MaxEntries: defaultVerificationHistoryMaxEntries},
		workspace: Workspace{
			BaseRoot:     root,
			Root:         root,
			Shell:        defaultShell(),
			ShellTimeout: 30 * time.Second,
		},
		goalReply: func(ctx context.Context, prompt string) (string, error) {
			if strings.Contains(prompt, "Final semantic goal review") {
				return "NEEDS_REVISION: the objective is not actually satisfied yet", nil
			}
			return "fake goal agent reply", nil
		},
	}
	useFastGoalRuntime(t, rt)

	if err := rt.handleGoalCommand("--run finish sample objective"); err != nil {
		t.Fatalf("handleGoalCommand: %v", err)
	}
	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if goal.Status != goalStatusBlocked || !strings.Contains(goal.LastError, "consecutive audit-ready cycles") {
		t.Fatalf("expected semantic-reject block, got %#v", goal)
	}
	if goal.Iteration != goalSemanticRejectBlockThreshold {
		t.Fatalf("expected block at the reject threshold %d, got iteration %d", goalSemanticRejectBlockThreshold, goal.Iteration)
	}
}
