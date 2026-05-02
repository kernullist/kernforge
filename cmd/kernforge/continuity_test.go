package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContinuityCommandWritesRecoveryPacket(t *testing.T) {
	root := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "agent.go"), []byte("package main\n\nfunc changed() {}\n"), 0o644); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	now := time.Now()
	exitCode := 1
	session := NewSession(root, "provider", "model", "", "default")
	session.TaskGraph = &TaskGraph{Nodes: []TaskNode{{
		ID:     "plan-01",
		Title:  "Finish Codex parity loop",
		Kind:   "edit",
		Status: "in_progress",
	}}}
	session.LastVerification = &VerificationReport{
		GeneratedAt:  now,
		Trigger:      "auto",
		ChangedPaths: []string{"agent.go"},
		Steps: []VerificationStep{{
			Label:       "go test",
			Command:     "go test ./...",
			Status:      VerificationFailed,
			FailureKind: "test_failure",
			Output:      "FAIL\nagent_test.go:12: expected recovery",
			Hint:        "Fix the failing assertion first.",
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
		LastOutput:     "FAIL agent",
		StartedAt:      now,
		UpdatedAt:      now,
	}}
	session.BackgroundBundles = []BackgroundShellBundle{{
		ID:               "bundle-1",
		CommandSummaries: []string{"go test ./..."},
		JobIDs:           []string{"job-1"},
		Status:           "failed",
		LastSummary:      "completed=0 running=0 failed=1 total=1",
		VerificationLike: true,
		StartedAt:        now,
		UpdatedAt:        now,
	}}
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindCommandError,
		Severity: conversationSeverityError,
		Summary:  "shell command failed: go test ./...",
		Raw:      "FAIL agent",
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

	if err := rt.handleContinuityCommand("Codex parity"); err != nil {
		t.Fatalf("handleContinuityCommand: %v", err)
	}

	mdPath := filepath.Join(root, ".kernforge", "continuity", "latest.md")
	jsonPath := filepath.Join(root, ".kernforge", "continuity", "latest.json")
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read continuity markdown: %v", err)
	}
	text := string(md)
	for _, want := range []string{"# Continuity Packet", "Recovery Actions", "go test ./...", "job-1", "bundle-1", "Finish Codex parity loop", "/jobs status"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected continuity markdown to contain %q, got %q", want, text)
		}
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read continuity json: %v", err)
	}
	packet := ContinuityPacket{}
	if err := json.Unmarshal(data, &packet); err != nil {
		t.Fatalf("unmarshal continuity packet: %v", err)
	}
	if len(packet.RecoveryActions) == 0 || len(packet.NextCommands) == 0 || len(packet.RecentErrors) == 0 {
		t.Fatalf("expected populated recovery packet, got %#v", packet)
	}
	last := session.ConversationEvents[len(session.ConversationEvents)-1]
	if last.Kind != conversationEventKindContinuity || len(last.ArtifactRefs) != 2 {
		t.Fatalf("expected continuity event, got %#v", last)
	}
	if !strings.Contains(output.String(), "Generated continuity packet") {
		t.Fatalf("expected command output, got %q", output.String())
	}
}

func TestJobsCommandShowsAndPollsPersistedJobs(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	exitCode := 1
	session := NewSession(root, "provider", "model", "", "default")
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
	var output bytes.Buffer
	store := NewSessionStore(filepath.Join(root, "sessions"))
	rt := &runtimeState{
		writer:         &output,
		ui:             NewUI(),
		session:        session,
		store:          store,
		backgroundJobs: NewBackgroundJobManager(filepath.Join(root, ".kernforge", "jobs"), session, store),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}
	rt.workspace.BackgroundJobs = rt.backgroundJobs

	if err := rt.handleJobsCommand("status"); err != nil {
		t.Fatalf("handleJobsCommand status: %v", err)
	}
	if err := rt.handleJobsCommand("check latest"); err != nil {
		t.Fatalf("handleJobsCommand check: %v", err)
	}
	if err := rt.handleJobsCommand("bundle latest"); err != nil {
		t.Fatalf("handleJobsCommand bundle: %v", err)
	}
	text := output.String()
	for _, want := range []string{"Background Jobs", "job-1", "failed=1", "Background Job", "Background Bundle", "FAIL package"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected jobs output to contain %q, got %q", want, text)
		}
	}
}

func TestLocalShellFailureRecordsCommandErrorEvent(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	rt := &runtimeState{
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
	}

	rt.noteLocalShellCommand("go test ./...", "FAIL package", errors.New("exit status 1"))

	if len(session.ConversationEvents) != 1 {
		t.Fatalf("expected one conversation event, got %#v", session.ConversationEvents)
	}
	event := session.ConversationEvents[0]
	if event.Kind != conversationEventKindCommandError || event.Severity != conversationSeverityError {
		t.Fatalf("expected command error event, got %#v", event)
	}
	if event.Entities["command"] != "go test ./..." || !strings.Contains(event.Raw, "exit status 1") {
		t.Fatalf("expected command metadata and raw error, got %#v", event)
	}
}
