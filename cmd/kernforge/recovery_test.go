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

func TestRecoverCommandWritesRecoveryBrief(t *testing.T) {
	root := initTestGitRepo(t)
	now := time.Now()
	exitCode := 1
	session := NewSession(root, "provider", "model", "", "default")
	session.TaskGraph = &TaskGraph{Nodes: []TaskNode{{
		ID:     "plan-01",
		Title:  "Repair failing tests",
		Kind:   "verification",
		Status: "in_progress",
	}}}
	session.LastVerification = &VerificationReport{
		GeneratedAt:  now,
		Trigger:      "manual",
		Workspace:    root,
		ChangedPaths: []string{"agent.go"},
		Steps: []VerificationStep{{
			Label:       "go test",
			Command:     "go test ./...",
			Status:      VerificationFailed,
			FailureKind: "test_failure",
			Output:      "FAIL package",
			Hint:        "Fix tests first.",
		}},
	}
	attempt := buildFailureRepairAttempt(session, *session.LastVerification)
	session.ActiveFailureRepair = &attempt
	session.BackgroundJobs = []BackgroundShellJob{{
		ID:             "job-1",
		Command:        "go test ./...",
		CommandSummary: "go test ./...",
		Status:         "failed",
		ExitCode:       &exitCode,
		LastOutput:     "FAIL package",
		StartedAt:      now,
		UpdatedAt:      now,
	}}
	session.BackgroundBundles = []BackgroundShellBundle{{
		ID:               "bundle-1",
		CommandSummaries: []string{"go test ./..."},
		JobIDs:           []string{"job-1"},
		Status:           "failed",
		LastSummary:      "completed=0 running=0 failed=1 total=1",
		StartedAt:        now,
		UpdatedAt:        now,
	}}
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindCommandError,
		Severity: conversationSeverityError,
		Summary:  "shell command failed: go test ./...",
		Raw:      "FAIL package",
		Time:     now,
		Entities: map[string]string{
			"tool":    "shell",
			"command": "go test ./...",
		},
	})
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

	if err := rt.handleRecoverCommand("continue tests"); err != nil {
		t.Fatalf("handleRecoverCommand: %v", err)
	}

	mdPath := filepath.Join(root, ".kernforge", "recovery", "latest.md")
	jsonPath := filepath.Join(root, ".kernforge", "recovery", "latest.json")
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read recovery markdown: %v", err)
	}
	text := string(md)
	for _, want := range []string{"# Recovery Brief", "Primary Failure", "Recovery Actions", "Fix tests first", "/jobs check job-1", "/jobs bundle bundle-1", "!go test ./...", "Repair failing tests"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected recovery markdown to contain %q, got %q", want, text)
		}
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read recovery json: %v", err)
	}
	brief := RecoveryBrief{}
	if err := json.Unmarshal(data, &brief); err != nil {
		t.Fatalf("unmarshal recovery brief: %v", err)
	}
	if len(brief.RecoveryActions) == 0 || len(brief.NextCommands) == 0 || !strings.Contains(brief.SuggestedPrompt, "Continue from the recovery brief") {
		t.Fatalf("expected populated recovery brief, got %#v", brief)
	}
	if brief.Diagnosis.Class != "verification_failure" || brief.Diagnosis.Signature == "" || !brief.Diagnosis.Blocking {
		t.Fatalf("expected structured verification diagnosis, got %#v", brief.Diagnosis)
	}
	verificationAction, ok := recoveryTestActionByID(brief.ActionPlan, "verification-rerun-01")
	if !ok {
		t.Fatalf("expected verification action in %#v", brief.ActionPlan)
	}
	if verificationAction.Command != "!go test ./..." || !verificationAction.VerificationGate || !verificationAction.SafeAuto {
		t.Fatalf("expected safe verification action, got %#v", verificationAction)
	}
	if !strings.Contains(text, "## Diagnosis") || !strings.Contains(text, "## Action Plan") || !strings.Contains(text, "verification-rerun-01") {
		t.Fatalf("expected recovery markdown to include structured diagnosis/action plan, got %q", text)
	}
	last := session.ConversationEvents[len(session.ConversationEvents)-1]
	if last.Kind != conversationEventKindRecovery || len(last.ArtifactRefs) != 2 {
		t.Fatalf("expected recovery conversation event, got %#v", last)
	}
	if !strings.Contains(output.String(), "Generated recovery brief") || !strings.Contains(output.String(), "Recovery actions") {
		t.Fatalf("expected recovery command output, got %q", output.String())
	}
}

func TestRecoverExecuteSafeRunsWhitelistedActionAndLogsStatus(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindCommandError,
		Severity: conversationSeverityError,
		Summary:  "shell command failed: git status --short",
		Raw:      "previous failure",
		Entities: map[string]string{
			"tool":    "shell",
			"command": "git status --short",
		},
	})
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
			Shell:    defaultShell(),
		},
	}

	if err := rt.handleRecoverCommand("execute-safe check status"); err != nil {
		t.Fatalf("handleRecoverCommand execute-safe: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".kernforge", "recovery", "latest.json"))
	if err != nil {
		t.Fatalf("read recovery json: %v", err)
	}
	brief := RecoveryBrief{}
	if err := json.Unmarshal(data, &brief); err != nil {
		t.Fatalf("unmarshal recovery brief: %v", err)
	}
	action, ok := recoveryTestActionByID(brief.ActionPlan, "event-command-rerun-01")
	if !ok {
		t.Fatalf("expected event command action in %#v", brief.ActionPlan)
	}
	if action.Status != recoveryActionStatusExecuted {
		t.Fatalf("expected event command action to execute, got %#v", action)
	}
	if len(brief.ExecutionLog) == 0 || brief.ExecutionLog[0].Status != recoveryActionStatusExecuted {
		t.Fatalf("expected execution log, got %#v", brief.ExecutionLog)
	}
	if brief.ExecutionLog[0].StartedAt.IsZero() || brief.ExecutionLog[0].FinishedAt.IsZero() {
		t.Fatalf("expected execution log timestamps, got %#v", brief.ExecutionLog[0])
	}
	md, err := os.ReadFile(filepath.Join(root, ".kernforge", "recovery", "latest.md"))
	if err != nil {
		t.Fatalf("read recovery markdown: %v", err)
	}
	if !strings.Contains(string(md), "## Execution Log") || !strings.Contains(string(md), "event-command-rerun-01") {
		t.Fatalf("expected execution log in markdown, got %q", string(md))
	}
}

func TestRecoverExecuteSafeHonorsCanceledContext(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindCommandError,
		Severity: conversationSeverityError,
		Summary:  "shell command failed: git status --short",
		Raw:      "previous failure",
		Entities: map[string]string{
			"tool":    "shell",
			"command": "git status --short",
		},
	})
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
			Shell:    defaultShell(),
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := rt.handleRecoverCommandContext(ctx, "execute-safe check status")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}

	data, readErr := os.ReadFile(filepath.Join(root, ".kernforge", "recovery", "latest.json"))
	if readErr != nil {
		t.Fatalf("read recovery json: %v", readErr)
	}
	brief := RecoveryBrief{}
	if err := json.Unmarshal(data, &brief); err != nil {
		t.Fatalf("unmarshal recovery brief: %v", err)
	}
	action, ok := recoveryTestActionByID(brief.ActionPlan, "event-command-rerun-01")
	if !ok {
		t.Fatalf("expected event command action in %#v", brief.ActionPlan)
	}
	if action.Status != recoveryActionStatusSkipped {
		t.Fatalf("expected canceled recovery action to be skipped, got %#v", action)
	}
	if len(brief.ExecutionLog) != 0 {
		t.Fatalf("expected no recovery execution log after pre-cancel, got %#v", brief.ExecutionLog)
	}
	if strings.Contains(output.String(), "Generated recovery brief") {
		t.Fatalf("expected canceled recovery command not to print success, got %q", output.String())
	}
}

func TestRecoverExecuteSafeMarksFailedSlashVerifyActionFailed(t *testing.T) {
	root := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/recoveryfail\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	testSource := "package recoveryfail\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) {\n\tt.Fatal(\"broken\")\n}\n"
	if err := os.WriteFile(filepath.Join(root, "broken_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatalf("write broken test: %v", err)
	}
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		verifyHistory: &VerificationHistoryStore{
			Path:       filepath.Join(root, "verify-history.json"),
			MaxEntries: defaultVerificationHistoryMaxEntries,
		},
		workspace: Workspace{
			BaseRoot:     root,
			Root:         root,
			Shell:        defaultShell(),
			ShellTimeout: 30 * time.Second,
		},
	}

	if err := rt.handleRecoverCommand("execute-safe failing verify"); err != nil {
		t.Fatalf("handleRecoverCommand execute-safe: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".kernforge", "recovery", "latest.json"))
	if err != nil {
		t.Fatalf("read recovery json: %v", err)
	}
	brief := RecoveryBrief{}
	if err := json.Unmarshal(data, &brief); err != nil {
		t.Fatalf("unmarshal recovery brief: %v", err)
	}
	action, ok := recoveryTestActionByID(brief.ActionPlan, "verify-changed-files-01")
	if !ok {
		t.Fatalf("expected verify action in %#v", brief.ActionPlan)
	}
	if action.Status != recoveryActionStatusFailed {
		t.Fatalf("expected failed verify action, got %#v", action)
	}
	continuity, ok := recoveryTestActionByID(brief.ActionPlan, "continuity-01")
	if !ok {
		t.Fatalf("expected continuity action in %#v", brief.ActionPlan)
	}
	if continuity.Status != recoveryActionStatusSkipped {
		t.Fatalf("expected continuity action to be skipped after verify failure, got %#v", continuity)
	}
	if session.LastVerification == nil || !session.LastVerification.HasFailures() {
		t.Fatalf("expected failed verification report, got %#v", session.LastVerification)
	}
	if len(brief.ExecutionLog) == 0 || brief.ExecutionLog[0].Status != recoveryActionStatusFailed {
		t.Fatalf("expected failed execution log, got %#v", brief.ExecutionLog)
	}
	if !strings.Contains(brief.ExecutionLog[0].Output, "go test") {
		t.Fatalf("expected failed execution log to include verification summary, got %#v", brief.ExecutionLog[0])
	}
}

func TestRecoveryActionPlanMarksUnsafeCommandManualOnly(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindCommandError,
		Severity: conversationSeverityError,
		Summary:  "shell command failed: Remove-Item -Recurse .",
		Raw:      "blocked destructive command",
		Entities: map[string]string{
			"tool":    "shell",
			"command": "Remove-Item -Recurse .",
		},
	})
	rt := &runtimeState{
		session: session,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	brief := rt.buildRecoveryBrief(root, "unsafe command")

	action, ok := recoveryTestActionByID(brief.ActionPlan, "event-command-rerun-01")
	if !ok {
		t.Fatalf("expected event command action in %#v", brief.ActionPlan)
	}
	if action.SafeAuto || action.Status != recoveryActionStatusManualOnly {
		t.Fatalf("expected unsafe command to be manual-only, got %#v", action)
	}
	if recoveryCommandAutoRunnable("Remove-Item -Recurse .") {
		t.Fatalf("expected destructive command to be outside safe-auto whitelist")
	}
}

func TestRecoveryCommandAutoRunnableRejectsUnsafeExecutionFlags(t *testing.T) {
	allowed := []string{
		"go test ./... -run TestRecover -count=1",
		"go test -race ./cmd/kernforge/...",
		"go vet ./...",
		"go list -json ./...",
		"git status --short",
		"git diff --check -- cmd/kernforge/recovery.go",
	}
	for _, command := range allowed {
		if !recoveryCommandAutoRunnable(command) {
			t.Fatalf("expected safe-auto command to be allowed: %s", command)
		}
	}
	rejected := []string{
		"go test -exec=calc ./...",
		"go test -toolexec=calc ./...",
		"go test -args -danger",
		"go test -coverprofile=cover.out ./...",
		"go vet -vettool=customvet ./...",
		"git diff --check --output=diff.txt",
	}
	for _, command := range rejected {
		if recoveryCommandAutoRunnable(command) {
			t.Fatalf("expected safe-auto command to be rejected: %s", command)
		}
	}
}

func TestRecoveryNextCommandsDropVerifyAfterSuccessfulVerification(t *testing.T) {
	root := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "agent.go"), []byte("package main\n\nfunc changed() {}\n"), 0o644); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	session := NewSession(root, "provider", "model", "", "default")
	session.LastVerification = &VerificationReport{
		GeneratedAt: time.Now(),
		Trigger:     "recovery",
		Workspace:   root,
		Steps: []VerificationStep{{
			Label:   "go test",
			Command: "go test ./...",
			Status:  VerificationPassed,
			Output:  "ok",
		}},
	}
	rt := &runtimeState{
		session: session,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	brief := rt.buildRecoveryBrief(root, "verified recovery")

	for _, command := range brief.NextCommands {
		if command == "/verify" {
			t.Fatalf("did not expect /verify after successful verification, got %#v", brief.NextCommands)
		}
	}
	if !recoveryVerificationSatisfied(session) {
		t.Fatalf("expected recovery verification to be satisfied")
	}
}

func TestRecoveryPostExecutionActionsSummarizePassedGate(t *testing.T) {
	session := NewSession(t.TempDir(), "provider", "model", "", "default")
	session.LastVerification = &VerificationReport{
		GeneratedAt: time.Now(),
		Trigger:     "recovery",
		Steps: []VerificationStep{{
			Label:  "go test",
			Status: VerificationPassed,
		}},
	}
	brief := RecoveryBrief{
		ActionPlan: []RecoveryActionPlanItem{{
			ID:     "verify-changed-files-01",
			Title:  "Run focused verification for changed files.",
			Status: recoveryActionStatusExecuted,
		}},
		ExecutionLog: []RecoveryExecutionRecord{{
			ActionID: "verify-changed-files-01",
			Status:   recoveryActionStatusExecuted,
		}},
	}

	actions := recoveryBriefPostExecutionActions(session, brief)

	if len(actions) != 1 || !strings.Contains(actions[0], "verification gate passed") {
		t.Fatalf("expected passed recovery summary, got %#v", actions)
	}
}

func recoveryTestActionByID(plan []RecoveryActionPlanItem, id string) (RecoveryActionPlanItem, bool) {
	for _, item := range plan {
		if item.ID == id {
			return item, true
		}
	}
	return RecoveryActionPlanItem{}, false
}
