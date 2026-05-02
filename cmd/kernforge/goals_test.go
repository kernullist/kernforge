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

	if err := rt.handleGoalCommand("start --max-iterations 2 finish sample objective"); err != nil {
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
			if len(replies) == 0 {
				t.Fatalf("unexpected extra goal prompt: %s", prompt)
			}
			reply := replies[0]
			replies = replies[1:]
			return reply, nil
		},
	}

	if err := rt.handleGoalCommand("start --max-iterations 2 finish sample objective"); err != nil {
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

	if err := rt.handleGoalCommand("start --token-budget 1 finish sample objective"); err != nil {
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

	if err := rt.handleGoalCommand("start --no-run finish sample objective"); err != nil {
		t.Fatalf("create goal: %v", err)
	}
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
