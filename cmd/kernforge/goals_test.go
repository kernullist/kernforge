package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeGoalTestModule(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/goaltest\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package goaltest\n\nfunc Answer() int {\n\treturn 42\n}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main_test.go"), []byte("package goaltest\n\nimport \"testing\"\n\nfunc TestAnswer(t *testing.T) {\n\tif Answer() != 42 {\n\t\tt.Fatal(\"bad answer\")\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("write main_test.go: %v", err)
	}
}

func TestGoalStartFromMarkdownNoRunPersistsArtifacts(t *testing.T) {
	root := initTestGitRepo(t)
	goalPath := filepath.Join(root, "GOAL.md")
	if err := os.WriteFile(goalPath, []byte("# Goal\n\nImplement autonomous loop.\n"), 0o644); err != nil {
		t.Fatalf("write goal file: %v", err)
	}
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleGoalCommand("start --no-run @GOAL.md"); err != nil {
		t.Fatalf("handleGoalCommand: %v", err)
	}

	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if goal.Status != goalStatusPending || !strings.Contains(goal.Objective, "Implement autonomous loop.") {
		t.Fatalf("unexpected goal state: %#v", goal)
	}
	if session.TaskState == nil || !strings.Contains(session.TaskState.Goal, "Implement autonomous loop.") {
		t.Fatalf("expected task state to inherit goal, got %#v", session.TaskState)
	}
	if session.AcceptanceContract == nil || !strings.Contains(session.AcceptanceContract.SourcePrompt, "Implement autonomous loop.") {
		t.Fatalf("expected goal acceptance contract, got %#v", session.AcceptanceContract)
	}
	if session.TaskGraph == nil || len(session.TaskGraph.Nodes) < 6 {
		t.Fatalf("expected goal task graph, got %#v", session.TaskGraph)
	}
	data, err := os.ReadFile(filepath.Join(root, ".kernforge", "goals", "latest.json"))
	if err != nil {
		t.Fatalf("read goal json: %v", err)
	}
	persisted := GoalState{}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("unmarshal goal json: %v", err)
	}
	if persisted.ID != goal.ID || persisted.Status != goalStatusPending {
		t.Fatalf("unexpected persisted goal: %#v", persisted)
	}
	if len(persisted.CompletionCriteria) == 0 {
		t.Fatalf("expected persisted goal completion criteria")
	}
	if len(persisted.ArtifactRefs) != 4 {
		t.Fatalf("expected persisted per-goal and latest artifact refs, got %#v", persisted.ArtifactRefs)
	}
	md, err := os.ReadFile(filepath.Join(root, ".kernforge", "goals", "latest.md"))
	if err != nil {
		t.Fatalf("read goal markdown: %v", err)
	}
	if !strings.Contains(string(md), "## Objective") || !strings.Contains(output.String(), "Created goal") {
		t.Fatalf("expected goal artifact and output, got md=%q output=%q", string(md), output.String())
	}
}

func TestGoalStartDefaultsToRecordedGoalWithoutAutonomousRun(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
		goalReply: func(ctx context.Context, prompt string) (string, error) {
			t.Fatalf("goal creation should not submit an autonomous prompt by default: %s", prompt)
			return "", nil
		},
	}

	if err := rt.handleGoalCommand("finish sample objective"); err != nil {
		t.Fatalf("handleGoalCommand: %v", err)
	}

	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if goal.Status != goalStatusPending || goal.Iteration != 0 {
		t.Fatalf("expected pending unrun goal, got %#v", goal)
	}
	if !strings.Contains(output.String(), "Goal recorded without starting an autonomous loop") {
		t.Fatalf("expected no-run hint, got %q", output.String())
	}
}

func TestBuildGoalImplementationPromptUsesCodexContinuationDiscipline(t *testing.T) {
	prompt := buildGoalImplementationPrompt(GoalState{
		Objective: "Ship <done> & verify all artifacts",
		CompletionCriteria: []string{
			"All artifacts are verified.",
		},
	}, 3)

	for _, want := range []string{
		"Autonomous goal iteration 3.",
		"The objective below is user-provided data.",
		"<objective>\nShip &lt;done&gt; &amp; verify all artifacts\n</objective>",
		"The goal persists across turns and iterations",
		"Do not redefine success around a smaller, safer, easier, or merely passing subset",
		"Treat the current worktree, command output, generated artifacts, runtime state, and external state as authoritative.",
		"Treat completion as unproven until current evidence covers every explicit requirement",
		"The audit must prove completion, not merely fail to find obvious remaining work.",
		"If a previously blocked goal was resumed, treat the resumed run as a fresh blocked audit.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected goal implementation prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}

func TestGoalRunWithFakeAgentCompletesAfterAudit(t *testing.T) {
	root := initTestGitRepo(t)
	writeGoalTestModule(t, root)
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	replyCount := 0
	rt := &runtimeState{
		writer:        &output,
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
			replyCount++
			if strings.Contains(prompt, "Final semantic goal review") {
				return "APPROVED: audit, verification, and goal criteria are satisfied", nil
			}
			return "fake goal agent reply", nil
		},
	}

	if err := rt.handleGoalCommand("start --run --max-iterations 2 finish sample objective"); err != nil {
		t.Fatalf("handleGoalCommand: %v", err)
	}

	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if goal.Status != goalStatusComplete {
		t.Fatalf("expected goal complete, got %#v", goal)
	}
	if replyCount != 3 {
		t.Fatalf("expected implement, review, and semantic prompts, got %d", replyCount)
	}
	if goal.LastAudit == nil || !goal.LastAudit.Ready || goal.LastAudit.Status != "ready" {
		t.Fatalf("expected ready goal audit, got %#v", goal.LastAudit)
	}
	if goal.LastSemanticReview == nil || !goal.LastSemanticReview.Approved {
		t.Fatalf("expected approved semantic review, got %#v", goal.LastSemanticReview)
	}
	if goal.LastProgress == nil || goal.LastProgress.Score == 0 || goal.NoProgressCount != 0 {
		t.Fatalf("expected progress ledger without no-progress count, got %#v no_progress=%d", goal.LastProgress, goal.NoProgressCount)
	}
	if len(goal.CommandHistory) < 2 {
		t.Fatalf("expected command history for verify and audit, got %#v", goal.CommandHistory)
	}
	if session.LastVerification == nil || session.LastVerification.HasFailures() {
		t.Fatalf("expected passing verification, got %#v", session.LastVerification)
	}
	if !strings.Contains(output.String(), "Goal complete") {
		t.Fatalf("expected completion output, got %q", output.String())
	}
}

func TestGoalReviewNeedsRevisionRunsRepairPass(t *testing.T) {
	root := initTestGitRepo(t)
	writeGoalTestModule(t, root)
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	replies := []string{
		"implementation done",
		"NEEDS_REVISION: add the missing review repair",
		"repair done",
		"APPROVED: repaired, verified, and audit is ready",
	}
	prompts := []string{}
	rt := &runtimeState{
		writer:        &output,
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
			prompts = append(prompts, prompt)
			if len(replies) == 0 {
				t.Fatalf("unexpected extra goal prompt: %s", prompt)
			}
			reply := replies[0]
			replies = replies[1:]
			return reply, nil
		},
	}

	if err := rt.handleGoalCommand("start --run --max-iterations 2 finish sample objective"); err != nil {
		t.Fatalf("handleGoalCommand: %v", err)
	}

	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if goal.Status != goalStatusComplete {
		t.Fatalf("expected goal complete, got %#v", goal)
	}
	if len(replies) != 0 {
		t.Fatalf("expected repair pass to consume all replies, remaining=%#v", replies)
	}
	if len(goal.Iterations) != 1 || goal.Iterations[0].ReviewerVerdict != "needs_revision" || goal.Iterations[0].RepairReply == "" {
		t.Fatalf("expected repair iteration evidence, got %#v", goal.Iterations)
	}
	if len(prompts) != 4 {
		t.Fatalf("expected implement, review, repair, and semantic prompts, got %d", len(prompts))
	}
	if !strings.Contains(prompts[1], "Review evidence:") ||
		!strings.Contains(prompts[1], "Implementation pass reply:") ||
		!strings.Contains(prompts[1], "implementation done") ||
		!strings.Contains(prompts[1], "Git status:") ||
		!strings.Contains(prompts[1], "go.mod") {
		t.Fatalf("expected review prompt to include implementation and workspace evidence:\n%s", prompts[1])
	}
	if !strings.Contains(prompts[2], "Reviewer feedback:") ||
		!strings.Contains(prompts[2], "add the missing review repair") ||
		!strings.Contains(prompts[2], "Implementation context:") ||
		!strings.Contains(prompts[2], "implementation done") {
		t.Fatalf("expected repair prompt to preserve review feedback and implementation context:\n%s", prompts[2])
	}
}

func TestGoalReviewerSkipsImplicitMainModelFallback(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "deepseek", "deepseek-chat", "", "default")
	rt := &runtimeState{
		session: session,
		agent: &Agent{
			Session: session,
			Config: Config{
				Provider: "deepseek",
				Model:    "deepseek-chat",
			},
		},
	}

	reply, err := rt.runGoalReviewerReply(context.Background(), "Autonomous goal independent review pass")
	if err != nil {
		t.Fatalf("runGoalReviewerReply: %v", err)
	}
	if !strings.HasPrefix(reply, "APPROVED: independent reviewer skipped") {
		t.Fatalf("expected skipped reviewer approval, got %q", reply)
	}

	reply, err = rt.runGoalReviewerReply(context.Background(), "Final semantic goal review")
	if err != nil {
		t.Fatalf("runGoalReviewerReply semantic: %v", err)
	}
	if !strings.HasPrefix(reply, "APPROVED: independent semantic reviewer skipped") {
		t.Fatalf("expected skipped semantic reviewer approval, got %q", reply)
	}
}

func TestGoalReviewEvidencePrefersCheckpointDiff(t *testing.T) {
	root := initTestGitRepo(t)
	writeGoalTestModule(t, root)
	if err := os.WriteFile(filepath.Join(root, "preexisting.md"), []byte("preexisting dirty content should stay out of the goal diff\n"), 0o644); err != nil {
		t.Fatalf("write preexisting file: %v", err)
	}
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	prompts := []string{}
	rt := &runtimeState{
		writer:        &output,
		ui:            NewUI(),
		session:       session,
		store:         NewSessionStore(filepath.Join(root, "sessions")),
		checkpoints:   &CheckpointManager{Root: filepath.Join(t.TempDir(), "checkpoints")},
		verifyHistory: &VerificationHistoryStore{Path: filepath.Join(root, "verify-history.json"), MaxEntries: defaultVerificationHistoryMaxEntries},
		workspace: Workspace{
			BaseRoot:     root,
			Root:         root,
			Shell:        defaultShell(),
			ShellTimeout: 30 * time.Second,
		},
		goalReply: func(ctx context.Context, prompt string) (string, error) {
			prompts = append(prompts, prompt)
			switch {
			case strings.Contains(prompt, "Autonomous goal iteration"):
				if err := os.WriteFile(filepath.Join(root, "generated.md"), []byte("# Generated\n\nreview me\n"), 0o644); err != nil {
					t.Fatalf("write generated file: %v", err)
				}
				return "created generated.md", nil
			case strings.Contains(prompt, "Autonomous goal independent review pass"):
				return "APPROVED: checkpoint diff shows generated.md", nil
			case strings.Contains(prompt, "Final semantic goal review"):
				return "APPROVED: semantic evidence includes generated.md", nil
			default:
				t.Fatalf("unexpected prompt: %s", prompt)
				return "", nil
			}
		},
	}

	if err := rt.handleGoalCommand("start --run --max-iterations 1 create generated review artifact"); err != nil {
		t.Fatalf("handleGoalCommand: %v", err)
	}
	if len(prompts) != 3 {
		t.Fatalf("expected implement, review, and semantic prompts, got %d", len(prompts))
	}
	if !strings.Contains(prompts[1], "Changes since iteration checkpoint:") ||
		!strings.Contains(prompts[1], "generated.md") ||
		!strings.Contains(prompts[1], "review me") {
		t.Fatalf("expected review prompt to include generated checkpoint diff:\n%s", prompts[1])
	}
	if strings.Contains(prompts[1], "preexisting dirty content") {
		t.Fatalf("review prompt should not include preexisting dirty content:\n%s", prompts[1])
	}
	if !strings.Contains(prompts[2], "Workspace review evidence:") ||
		!strings.Contains(prompts[2], "Changes since iteration checkpoint:") ||
		!strings.Contains(prompts[2], "generated.md") {
		t.Fatalf("expected semantic review prompt to include checkpoint workspace evidence:\n%s", prompts[2])
	}
	if strings.Contains(prompts[2], ".kernforge/completion_audit") {
		t.Fatalf("semantic review evidence should omit internal completion audit artifacts:\n%s", prompts[2])
	}
}

func TestGoalReviewGitTextKeepsFatalTextInDiff(t *testing.T) {
	root := initTestGitRepo(t)
	path := filepath.Join(root, "fatal.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write fatal.txt: %v", err)
	}
	mustRunGit(t, root, "add", "fatal.txt")
	mustRunGit(t, root, "commit", "-m", "Add fatal text fixture")
	if err := os.WriteFile(path, []byte("fatal: this is source text, not a git error\n"), 0o644); err != nil {
		t.Fatalf("modify fatal.txt: %v", err)
	}

	diff := goalReviewGitText(root, "diff", "--", "fatal.txt")
	if !strings.Contains(diff, "fatal: this is source text") {
		t.Fatalf("expected diff text containing fatal marker to be preserved, got:\n%s", diff)
	}
}

func TestBuildGoalUntrackedFileReviewEvidenceLimitsLargeFiles(t *testing.T) {
	root := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "huge.log"), []byte(strings.Repeat("A", 20000)), 0o644); err != nil {
		t.Fatalf("write huge.log: %v", err)
	}

	evidence := buildGoalUntrackedFileReviewEvidence(root, 1, 1200)
	if !strings.Contains(evidence, "huge.log") {
		t.Fatalf("expected untracked evidence to include huge.log, got:\n%s", evidence)
	}
	if !strings.Contains(evidence, "... (truncated)") {
		t.Fatalf("expected large untracked evidence to be truncated, got:\n%s", evidence)
	}
	if len(evidence) > 1800 {
		t.Fatalf("expected large untracked evidence to stay compact, len=%d", len(evidence))
	}
}

func TestGoalReviewPathWithinRootAllowsDotDotPrefixNames(t *testing.T) {
	root := t.TempDir()
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	if !goalReviewPathWithinRoot(rootAbs, filepath.Join(root, "..not-parent.txt")) {
		t.Fatalf("expected dot-dot-prefixed file name inside root to be allowed")
	}
	if goalReviewPathWithinRoot(rootAbs, filepath.Join(root, "..", "outside.txt")) {
		t.Fatalf("expected parent traversal outside root to be rejected")
	}
}

func TestParseGoalReviewDecisionRecognizesRevisionWording(t *testing.T) {
	cases := []string{
		"보완 필요: 테스트가 누락되었습니다.",
		"누락된 문서 검증이 있습니다.",
		"missing tests for the new goal flow",
		"blocker: verification is incomplete",
	}
	for _, text := range cases {
		decision := parseGoalReviewDecision(text)
		if !decision.NeedsRevision || decision.Verdict != "needs_revision" {
			t.Fatalf("expected needs_revision for %q, got %#v", text, decision)
		}
	}

	approved := parseGoalReviewDecision("APPROVED: no missing work remains")
	if approved.NeedsRevision || approved.Verdict != "approved" {
		t.Fatalf("expected explicit approval to win over keyword text, got %#v", approved)
	}
}

func TestGoalTokenBudgetBlocksBeforeAgentPrompt(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	replyCount := 0
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
		goalReply: func(ctx context.Context, prompt string) (string, error) {
			replyCount++
			return "unexpected", nil
		},
	}

	if err := rt.handleGoalCommand("start --run --token-budget 1 finish sample objective"); err != nil {
		t.Fatalf("handleGoalCommand: %v", err)
	}

	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if goal.Status != goalStatusBlocked || !strings.Contains(goal.LastError, "token budget") {
		t.Fatalf("expected token budget blocker, got %#v", goal)
	}
	if goal.TokenBudget != 1 || goal.TokenUsedEstimate <= goal.TokenBudget {
		t.Fatalf("expected token estimate over budget, got budget=%d used=%d", goal.TokenBudget, goal.TokenUsedEstimate)
	}
	if replyCount != 0 {
		t.Fatalf("expected no agent prompt before token budget block, got %d", replyCount)
	}
}

func TestGoalLoopStopsOnBlockedGoalUntilExplicitResume(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	goal := GoalState{
		ID:        "goal-blocked",
		Objective: "finish blocked objective",
		Status:    goalStatusBlocked,
		LastError: "waiting for stronger evidence",
	}
	goal.Normalize()
	session.UpsertGoal(goal)
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
		goalReply: func(ctx context.Context, prompt string) (string, error) {
			t.Fatalf("blocked goal should not continue automatically: %s", prompt)
			return "", nil
		},
	}

	if err := rt.runGoalLoop(context.Background(), goal.ID); err != nil {
		t.Fatalf("runGoalLoop: %v", err)
	}
	current, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if current.Status != goalStatusBlocked || current.LastError != goal.LastError {
		t.Fatalf("expected blocked goal to remain stopped, got %#v", current)
	}
}

func TestGoalResumeResetsBlockedAuditCounters(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	goal := GoalState{
		ID:                      "goal-blocked",
		Objective:               "finish blocked objective",
		Status:                  goalStatusBlocked,
		LastError:               "same blocker",
		NoProgressCount:         4,
		RepeatedFailureCount:    4,
		LastProgressFingerprint: "stale-progress",
		LastFailureSignature:    "same blocker",
	}
	goal.Normalize()
	session.UpsertGoal(goal)
	var output bytes.Buffer
	rt := &runtimeState{
		writer:    &output,
		ui:        NewUI(),
		session:   session,
		store:     NewSessionStore(filepath.Join(root, "sessions")),
		clientErr: errors.New("provider offline"),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleGoalCommand("resume " + goal.ID); err == nil || !strings.Contains(err.Error(), "provider offline") {
		t.Fatalf("expected provider error after resume setup, got %v", err)
	}
	current, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if current.NoProgressCount != 0 || current.RepeatedFailureCount != 0 ||
		current.LastProgressFingerprint != "" || current.LastFailureSignature != "" {
		t.Fatalf("expected blocked audit counters to reset on resume, got %#v", current)
	}
	if current.Status != goalStatusBlocked {
		t.Fatalf("expected provider error to leave goal blocked, got %#v", current)
	}
}

func TestGoalRunCancelsBeforeIteration(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
		goalReply: func(ctx context.Context, prompt string) (string, error) {
			t.Fatalf("unexpected goal prompt after cancellation: %s", prompt)
			return "", nil
		},
	}

	if err := rt.handleGoalCommand("start --no-run finish sample objective"); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := rt.runGoalLoop(ctx, goal.ID); err != nil {
		t.Fatalf("runGoalLoop: %v", err)
	}

	active, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if active.Status != goalStatusCanceled || !strings.Contains(active.LastError, "canceled") {
		t.Fatalf("expected canceled goal, got %#v", active)
	}
	if len(active.Iterations) != 0 {
		t.Fatalf("expected no iteration to start after pre-cancel, got %#v", active.Iterations)
	}
	if !strings.Contains(output.String(), "Goal canceled") {
		t.Fatalf("expected cancel output, got %q", output.String())
	}
}

func TestGoalRunCancelsDuringAgentPrompt(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	replyCount := 0
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
		goalReply: func(ctx context.Context, prompt string) (string, error) {
			replyCount++
			cancel()
			return "", ctx.Err()
		},
	}

	if err := rt.handleGoalCommand("start --no-run finish sample objective"); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if err := rt.runGoalLoop(ctx, goal.ID); err != nil {
		t.Fatalf("runGoalLoop: %v", err)
	}

	active, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if active.Status != goalStatusCanceled || !strings.Contains(active.LastError, "canceled") {
		t.Fatalf("expected canceled goal, got %#v", active)
	}
	if replyCount != 1 {
		t.Fatalf("expected one goal prompt before cancellation, got %d", replyCount)
	}
	if len(active.Iterations) != 1 || active.Iterations[0].Status != goalStatusCanceled {
		t.Fatalf("expected canceled iteration evidence, got %#v", active.Iterations)
	}
	if !strings.Contains(output.String(), "Goal canceled") {
		t.Fatalf("expected cancel output, got %q", output.String())
	}
}

func TestGoalRunCancelsDuringVerification(t *testing.T) {
	root := initTestGitRepo(t)
	writeGoalTestModule(t, root)
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	replyCount := 0
	rt := &runtimeState{
		writer:        &output,
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
			replyCount++
			if replyCount == 2 {
				cancel()
			}
			return "fake goal reply", nil
		},
	}

	if err := rt.handleGoalCommand("start --no-run finish sample objective"); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if err := rt.runGoalLoop(ctx, goal.ID); err != nil {
		t.Fatalf("runGoalLoop: %v", err)
	}

	active, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if active.Status != goalStatusCanceled || !strings.Contains(active.LastError, "canceled") {
		t.Fatalf("expected canceled goal, got %#v", active)
	}
	if replyCount != 2 {
		t.Fatalf("expected implementation and review prompts before verification cancellation, got %d", replyCount)
	}
	if len(active.Iterations) != 1 || active.Iterations[0].Status != goalStatusCanceled {
		t.Fatalf("expected canceled iteration evidence, got %#v", active.Iterations)
	}
	if len(active.CommandHistory) == 0 || active.CommandHistory[0].Name != "verify" || active.CommandHistory[0].Status != "canceled" {
		t.Fatalf("expected canceled verify command evidence, got %#v", active.CommandHistory)
	}
}

func TestGoalCompleteRequiresSemanticApproval(t *testing.T) {
	root := initTestGitRepo(t)
	writeGoalTestModule(t, root)
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	rt := &runtimeState{
		writer:        &output,
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
				return "NEEDS_REVISION: final evidence is intentionally incomplete", nil
			}
			return "APPROVED: unused", nil
		},
	}

	if err := rt.handleGoalCommand("start --no-run finish sample objective"); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if err := rt.handleVerifyCommand("--full"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	err := rt.handleGoalCommand("complete latest")
	if err == nil || !strings.Contains(err.Error(), "cannot be marked complete") {
		t.Fatalf("expected semantic complete gate error, got %v", err)
	}

	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if goal.Status != goalStatusBlocked {
		t.Fatalf("expected blocked goal, got %#v", goal)
	}
	if goal.LastSemanticReview == nil || goal.LastSemanticReview.Approved {
		t.Fatalf("expected rejected semantic review, got %#v", goal.LastSemanticReview)
	}
}

func TestGoalCompleteMarksApprovedGoalComplete(t *testing.T) {
	root := initTestGitRepo(t)
	writeGoalTestModule(t, root)
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	rt := &runtimeState{
		writer:        &output,
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
				return "APPROVED: verification and completion audit satisfy the objective", nil
			}
			return "APPROVED: unused", nil
		},
	}

	if err := rt.handleGoalCommand("start --no-run --token-budget 1000000 finish sample objective"); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	createdGoal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	createdGoal.CreatedAt = time.Now().Add(-75 * time.Second)
	createdGoal.UpdatedAt = createdGoal.CreatedAt
	session.UpsertGoal(createdGoal)
	if err := rt.handleVerifyCommand("--full"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := rt.handleGoalCommand("complete latest"); err != nil {
		t.Fatalf("complete goal: %v", err)
	}

	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if goal.Status != goalStatusComplete || goal.CompletedAt.IsZero() {
		t.Fatalf("expected complete goal, got %#v", goal)
	}
	if goal.LastAudit == nil || !goal.LastAudit.Ready {
		t.Fatalf("expected ready audit, got %#v", goal.LastAudit)
	}
	if goal.LastSemanticReview == nil || !goal.LastSemanticReview.Approved {
		t.Fatalf("expected approved semantic review, got %#v", goal.LastSemanticReview)
	}
	if goal.TokenBudget != 1000000 || goal.TokenUsedEstimate <= 0 {
		t.Fatalf("expected token usage telemetry, got budget=%d used=%d", goal.TokenBudget, goal.TokenUsedEstimate)
	}
	if goal.TimeUsedSeconds <= 0 {
		t.Fatalf("expected time usage telemetry, got %#v", goal)
	}
	remaining, ok := goalTokenRemainingEstimate(goal)
	if !ok || remaining != goal.TokenBudget-goal.TokenUsedEstimate {
		t.Fatalf("expected token remaining estimate, got remaining=%d ok=%t goal=%#v", remaining, ok, goal)
	}
	completionOutput := output.String()
	for _, want := range []string{
		"completion_budget_report",
		"token_budget",
		"token_used_estimate",
		"token_remaining_estimate",
		"time_used_seconds",
		"time_used",
	} {
		if !strings.Contains(completionOutput, want) {
			t.Fatalf("expected completion output to contain %q, got:\n%s", want, completionOutput)
		}
	}
	artifact, err := os.ReadFile(filepath.Join(root, userConfigDirName, "goals", "latest.md"))
	if err != nil {
		t.Fatalf("read goal artifact: %v", err)
	}
	for _, want := range []string{
		"Time used seconds:",
		"Time used:",
		"Token remaining estimate:",
		"Completion budget report:",
	} {
		if !strings.Contains(string(artifact), want) {
			t.Fatalf("expected goal artifact to contain %q, got:\n%s", want, string(artifact))
		}
	}
}

func TestGoalAuditPreservesSemanticRejection(t *testing.T) {
	root := initTestGitRepo(t)
	writeGoalTestModule(t, root)
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	rt := &runtimeState{
		writer:        &output,
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
				return "NEEDS_REVISION: keep the final gate blocked", nil
			}
			return "APPROVED: unused", nil
		},
	}

	if err := rt.handleGoalCommand("start --no-run finish sample objective"); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if err := rt.handleVerifyCommand("--full"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := rt.handleGoalCommand("complete latest"); err == nil {
		t.Fatalf("expected complete to reject semantic review")
	}
	if err := rt.handleGoalCommand("audit latest"); err != nil {
		t.Fatalf("audit goal: %v", err)
	}

	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if goal.Status != goalStatusBlocked || !strings.Contains(goal.LastError, "semantic approval") {
		t.Fatalf("expected semantic rejection to remain blocked after audit, got %#v", goal)
	}
	if goal.LastSemanticReview == nil || goal.LastSemanticReview.Approved {
		t.Fatalf("expected rejected semantic review to remain, got %#v", goal.LastSemanticReview)
	}
}

func TestGoalCompleteSpecificIDActivatesSelectedGoal(t *testing.T) {
	root := initTestGitRepo(t)
	writeGoalTestModule(t, root)
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	rt := &runtimeState{
		writer:        &output,
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
				return "APPROVED: selected goal audit and verification satisfy the objective", nil
			}
			return "APPROVED: unused", nil
		},
	}

	if err := rt.handleGoalCommand("start --no-run finish first sample objective"); err != nil {
		t.Fatalf("create first goal: %v", err)
	}
	first, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected first goal")
	}
	if err := rt.handleGoalCommand("start --no-run finish second sample objective"); err != nil {
		t.Fatalf("create second goal: %v", err)
	}
	second, ok := session.ActiveGoal()
	if !ok || second.ID == first.ID {
		t.Fatalf("expected second active goal, got %#v first=%s", second, first.ID)
	}
	if err := rt.handleVerifyCommand("--full"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := rt.handleGoalCommand("complete " + first.ID); err != nil {
		t.Fatalf("complete selected goal: %v", err)
	}

	active, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if active.ID != first.ID || active.Status != goalStatusComplete {
		t.Fatalf("expected selected first goal to become active and complete, got %#v", active)
	}
	index, ok := session.GoalIndex(second.ID)
	if !ok {
		t.Fatalf("expected second goal to remain tracked")
	}
	if session.Goals[index].Status != goalStatusPending {
		t.Fatalf("expected second goal to remain pending, got %#v", session.Goals[index])
	}
}

func TestGoalProgressBlocksRepeatedNoProgress(t *testing.T) {
	goal := GoalState{ID: "goal-1", Objective: "finish", Status: goalStatusRunning}
	progress := GoalProgressState{
		Fingerprint:      "same",
		FailureSignature: "same blocker",
		AuditStatus:      "blocked",
	}

	goal.applyProgress(progress)
	goal.applyProgress(progress)
	goal.applyProgress(progress)
	goal.applyProgress(progress)

	if goalStagnationBlocker(goal) == "" {
		t.Fatalf("expected stagnation blocker, got %#v", goal)
	}
}

func TestParseGoalTimeBudgetSeconds(t *testing.T) {
	for raw, want := range map[string]int{
		"30": 30,
		"2m": 120,
		"1h": 3600,
	} {
		got, err := parseGoalTimeBudgetSeconds(raw)
		if err != nil {
			t.Fatalf("parseGoalTimeBudgetSeconds(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("parseGoalTimeBudgetSeconds(%q) = %d, want %d", raw, got, want)
		}
	}
	if _, err := parseGoalTimeBudgetSeconds("-1"); err == nil {
		t.Fatalf("expected negative budget to fail")
	}
}

func TestFormatGoalElapsedSecondsIsCompact(t *testing.T) {
	cases := map[int]string{
		0:                             "0s",
		59:                            "59s",
		60:                            "1m",
		30 * 60:                       "30m",
		90 * 60:                       "1h 30m",
		2 * 60 * 60:                   "2h",
		24*60*60 - 1:                  "23h 59m",
		24 * 60 * 60:                  "1d 0h 0m",
		2*24*60*60 + 23*60*60 + 42*60: "2d 23h 42m",
	}
	for seconds, want := range cases {
		if got := formatGoalElapsedSeconds(seconds); got != want {
			t.Fatalf("formatGoalElapsedSeconds(%d) = %q, want %q", seconds, got, want)
		}
	}
}

func TestGoalTimeUsedPreservesExistingValueOnClockSkew(t *testing.T) {
	now := time.Now()
	goal := GoalState{
		ID:              "goal-skew",
		Objective:       "finish",
		Status:          goalStatusRunning,
		CreatedAt:       now.Add(time.Minute),
		TimeUsedSeconds: 42,
	}

	goal.updateTimeUsedSeconds(now)

	if goal.TimeUsedSeconds != 42 {
		t.Fatalf("expected clock skew to preserve time used, got %d", goal.TimeUsedSeconds)
	}
}

func TestRunSingleGoalPreservesCLIObjectiveText(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	rt := &runtimeState{
		writer:    &output,
		ui:        NewUI(),
		session:   session,
		store:     NewSessionStore(filepath.Join(root, "sessions")),
		clientErr: errors.New("provider unavailable"),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	err := rt.runSingleGoal(`fix "quoted" objective`, "", singleGoalOptions{
		MaxIterations: 3,
		TimeBudget:    "1m",
		TokenBudget:   100000,
		AutoRollback:  true,
	})
	if err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("expected provider error, got %v", err)
	}
	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if !strings.Contains(goal.Objective, `"quoted"`) {
		t.Fatalf("expected CLI objective to preserve quotes, got %q", goal.Objective)
	}
	if goal.Status != goalStatusBlocked {
		t.Fatalf("expected blocked goal after provider error, got %#v", goal)
	}
	if goal.MaxIterations != 3 || goal.TimeBudgetSeconds != 60 || goal.TokenBudget != 100000 || !goal.AutoRollback {
		t.Fatalf("expected CLI goal options to persist, got %#v", goal)
	}
}

func TestRunSingleGoalDefaultsToUntilComplete(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	rt := &runtimeState{
		writer:    &output,
		ui:        NewUI(),
		session:   session,
		store:     NewSessionStore(filepath.Join(root, "sessions")),
		clientErr: errors.New("provider unavailable"),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	err := rt.runSingleGoal("finish without an implicit iteration cap", "")
	if err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("expected provider error, got %v", err)
	}
	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatalf("expected active goal")
	}
	if goal.MaxIterations != 0 {
		t.Fatalf("expected default goal loop to run until completion, got max_iterations=%d goal=%#v", goal.MaxIterations, goal)
	}
	if label := goalMaxIterationsLabel(goal.MaxIterations); label != "until-complete" {
		t.Fatalf("expected until-complete label, got %q", label)
	}
}

func TestGoalFieldsSupportQuotedFilePaths(t *testing.T) {
	fields := splitGoalFields(`start --file "docs/My Goal.md" --max-iterations 2 finish it`)
	want := []string{"start", "--file", "docs/My Goal.md", "--max-iterations", "2", "finish", "it"}
	if len(fields) != len(want) {
		t.Fatalf("fields length = %d, want %d: %#v", len(fields), len(want), fields)
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Fatalf("field[%d] = %q, want %q in %#v", i, fields[i], want[i], fields)
		}
	}
}
