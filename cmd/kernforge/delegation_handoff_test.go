package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDelegationHandoffWritesCompactArtifacts(t *testing.T) {
	root := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "agent.go"), []byte("package main\n\nfunc delegated() {}\n"), 0o644); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	session := NewSession(root, "provider", "model", "", "default")
	session.TaskGraph = &TaskGraph{Nodes: []TaskNode{{
		ID:     "plan-01",
		Title:  "Finish automation monitor",
		Kind:   "task",
		Status: "in_progress",
	}}}
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindHandoff,
		Severity: conversationSeverityInfo,
		Summary:  "PR review automation report generated",
		ArtifactRefs: []string{
			filepath.Join(root, ".kernforge", "pr_review", "latest.md"),
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

	if err := rt.handleDelegationHandoffCommand("continue Codex parity work"); err != nil {
		t.Fatalf("handleDelegationHandoffCommand: %v", err)
	}
	mdPath := filepath.Join(root, ".kernforge", "handoff", "latest.md")
	jsonPath := filepath.Join(root, ".kernforge", "handoff", "latest.json")
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read markdown handoff: %v", err)
	}
	text := string(md)
	for _, want := range []string{"# Delegation Handoff", "continue Codex parity work", "agent.go", "Finish automation monitor", "Suggested Prompt"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected markdown handoff to contain %q, got %q", want, text)
		}
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json handoff: %v", err)
	}
	artifact := DelegationHandoffArtifact{}
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("unmarshal handoff: %v", err)
	}
	if artifact.Note != "continue Codex parity work" || len(artifact.ChangedFiles) == 0 || len(artifact.OpenTasks) == 0 {
		t.Fatalf("expected populated handoff artifact, got %#v", artifact)
	}
	if len(session.ConversationEvents) < 2 || len(session.ConversationEvents[len(session.ConversationEvents)-1].ArtifactRefs) != 2 {
		t.Fatalf("expected handoff conversation event, got %#v", session.ConversationEvents)
	}
	if !strings.Contains(output.String(), "Generated delegation handoff") {
		t.Fatalf("expected handoff output, got %q", output.String())
	}
}

func TestDelegationHandoffImportRecordsResultAndMarksTasks(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "cloud_result.json")
	result := DelegationImportArtifact{
		ID:            "cloud-task-17",
		Status:        "completed",
		Summary:       "Cloud task finished review fixes",
		Verification:  "go test ./...",
		ChangedFiles:  []string{"cmd/kernforge/agent.go"},
		CompletedTask: []string{"task-17"},
		ArtifactRefs:  []string{"cloud://task/17/log"},
		Notes:         []string{"No remaining blocker."},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if err := os.WriteFile(sourcePath, data, 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}
	session := NewSession(root, "provider", "model", "", "default")
	session.TaskGraph = &TaskGraph{Nodes: []TaskNode{{
		ID:     "task-17",
		Title:  "Review fixes",
		Kind:   "task",
		Status: "in_progress",
	}}}
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

	if err := rt.handleDelegationHandoffCommand("import cloud_result.json"); err != nil {
		t.Fatalf("handleDelegationHandoffCommand: %v", err)
	}
	node, ok := session.TaskGraph.Node("task-17")
	if !ok || node.Status != "completed" || !strings.Contains(node.LifecycleNote, "Cloud task finished") {
		t.Fatalf("expected task to be marked completed, got %#v ok=%t", node, ok)
	}
	jsonPath := filepath.Join(root, ".kernforge", "handoff", "imports", "cloud-task-17.json")
	mdPath := filepath.Join(root, ".kernforge", "handoff", "imports", "cloud-task-17.md")
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("expected normalized import json: %v", err)
	}
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read normalized import markdown: %v", err)
	}
	for _, want := range []string{"# Delegation Import", "Cloud task finished review fixes", "Tasks marked: 1", "task-17", "cloud://task/17/log"} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("expected import markdown to contain %q, got %q", want, string(md))
		}
	}
	event := session.ConversationEvents[len(session.ConversationEvents)-1]
	if event.Kind != conversationEventKindHandoff || event.Entities["tasks_marked"] != "1" || !strings.Contains(event.Summary, "Cloud task finished") {
		t.Fatalf("expected import event, got %#v", event)
	}
	if !strings.Contains(output.String(), "Imported delegation result") {
		t.Fatalf("expected import output, got %q", output.String())
	}
}

func TestRunSingleCommandExecutesSlashCommand(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "result.json")
	result := DelegationImportArtifact{
		ID:      "single-command-result",
		Status:  "completed",
		Summary: "Imported through -command mode",
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if err := os.WriteFile(sourcePath, data, 0o644); err != nil {
		t.Fatalf("write result: %v", err)
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

	if err := rt.runSingleCommand("/handoff import result.json"); err != nil {
		t.Fatalf("runSingleCommand: %v", err)
	}
	if !strings.Contains(output.String(), "Imported delegation result") {
		t.Fatalf("expected command output, got %q", output.String())
	}
}
