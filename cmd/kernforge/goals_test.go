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
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/goaltest\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package goaltest\n\nfunc Answer() int {\n\treturn 42\n}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main_test.go"), []byte("package goaltest\n\nimport \"testing\"\n\nfunc TestAnswer(t *testing.T) {\n\tif Answer() != 42 {\n\t\tt.Fatal(\"bad answer\")\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("write main_test.go: %v", err)
	}
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
	if replyCount != 2 {
		t.Fatalf("expected implement and review prompts, got %d", replyCount)
	}
	if goal.LastAudit == nil || !goal.LastAudit.Ready || goal.LastAudit.Status != "ready" {
		t.Fatalf("expected ready goal audit, got %#v", goal.LastAudit)
	}
	if session.LastVerification == nil || session.LastVerification.HasFailures() {
		t.Fatalf("expected passing verification, got %#v", session.LastVerification)
	}
	if !strings.Contains(output.String(), "Goal complete") {
		t.Fatalf("expected completion output, got %q", output.String())
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

	err := rt.runSingleGoal(`fix "quoted" objective`, "")
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
