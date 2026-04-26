package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSuggestAcceptConfirmExecutesAutomationAndPersistsPreference(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	session := NewSession(root, "provider", "model", "", "default")
	mem := session.ensureSuggestionMemory()
	mem.Mode = SuggestionModeConfirm
	suggestion := normalizeSuggestion(Suggestion{
		Type:      AutomationTypePRReview,
		Title:     "PR review automation report 준비",
		Reason:    "dirty diff needs review automation",
		Command:   "/automation add pr-review /review-pr",
		DedupKey:  "automation:pr-review:test",
		CreatedAt: time.Now(),
	})
	mem.recordShown(suggestion)
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		longMem: &PersistentMemoryStore{Path: filepath.Join(root, "memory.json")},
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleSuggestCommand("accept " + suggestion.ID); err != nil {
		t.Fatalf("handleSuggestCommand: %v", err)
	}
	if len(session.Automations) != 1 {
		t.Fatalf("expected automation to be added, got %#v", session.Automations)
	}
	if session.SuggestionMemory.Records[0].Status != SuggestionStatusExecuted {
		t.Fatalf("expected executed suggestion, got %#v", session.SuggestionMemory.Records[0])
	}
	if session.TaskGraph == nil {
		t.Fatalf("expected task graph")
	}
	node, ok := session.TaskGraph.Node("suggest:" + shortStableID(suggestion.DedupKey))
	if !ok || node.Status != "completed" {
		t.Fatalf("expected completed suggestion node, got %#v ok=%t", node, ok)
	}
	items, err := rt.longMem.ListRecent(root, 4)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(items) == 0 || !strings.Contains(items[0].Summary, "Suggestion accepted") {
		t.Fatalf("expected accepted suggestion memory, got %#v", items)
	}
	if !strings.Contains(output.String(), "Executed accepted suggestion") {
		t.Fatalf("expected execution output, got %q", output.String())
	}
}

func TestPRReviewAutomationWritesReport(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	session := NewSession(root, "provider", "model", "", "default")
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

	if err := rt.handlePRReviewAutomationCommand(""); err != nil {
		t.Fatalf("handlePRReviewAutomationCommand: %v", err)
	}
	path := filepath.Join(root, ".kernforge", "pr_review", "latest.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	text := string(data)
	for _, want := range []string{"# PR Review Automation", "## Review Checklist", "Correctness", "Security"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected report to contain %q, got %q", want, text)
		}
	}
	if len(session.ConversationEvents) == 0 {
		t.Fatalf("expected conversation event")
	}
}

func TestSuggestListPreservesExecutedTaskGraphStatus(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	session := NewSession(root, "provider", "model", "", "default")
	mem := session.ensureSuggestionMemory()
	suggestion := normalizeSuggestion(Suggestion{
		Type:      "run_verification",
		Title:     "변경 파일에 맞는 verification 실행",
		Reason:    "changed files need verification",
		Command:   "/verify",
		DedupKey:  "verify:driver.cpp",
		CreatedAt: time.Now(),
	})
	mem.recordShown(suggestion)
	record, ok := mem.mark(suggestion.ID, SuggestionStatusExecuted, "done")
	if !ok {
		t.Fatalf("expected mark to succeed")
	}
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
	rt.syncSuggestionToTaskGraph(record)
	rt.syncSuggestionCandidatesToTaskGraph([]Suggestion{suggestion}, mem)
	node, ok := session.TaskGraph.Node("suggest:" + shortStableID(suggestion.DedupKey))
	if !ok {
		t.Fatalf("expected suggestion node")
	}
	if node.Status != "completed" {
		t.Fatalf("expected completed status to be preserved, got %#v", node)
	}
}

func TestAutomationAddRejectsUnsafeCommand(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	session := NewSession(root, "provider", "model", "", "default")
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

	if err := rt.handleAutomationCommand("add recurring-verification /review-pr"); err == nil {
		t.Fatalf("expected unsafe automation command to be rejected")
	}
	if len(session.Automations) != 0 {
		t.Fatalf("expected no automation to be added, got %#v", session.Automations)
	}
}
