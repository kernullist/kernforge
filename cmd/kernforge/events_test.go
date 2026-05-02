package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEventsExportWritesJSONLAndRecordsEvent(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindUserMessage,
		Severity: conversationSeverityInfo,
		Summary:  "user asked for event stream",
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

	if err := rt.handleEventsCommand("export"); err != nil {
		t.Fatalf("handleEventsCommand export: %v", err)
	}

	path := filepath.Join(root, ".kernforge", "events", session.ID+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read event stream: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two event stream records, got %d: %q", len(lines), data)
	}
	last := SessionEventStreamRecord{}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("unmarshal event stream record: %v", err)
	}
	if last.SessionID != session.ID || last.Event.Kind != conversationEventKindEventStream {
		t.Fatalf("expected exported event stream record, got %#v", last)
	}
	if _, err := os.Stat(filepath.Join(root, ".kernforge", "events", "latest.jsonl")); err != nil {
		t.Fatalf("expected latest event stream: %v", err)
	}
	if !strings.Contains(output.String(), "Exported session events") {
		t.Fatalf("expected export output, got %q", output.String())
	}
}

func TestEventsTailPrintsRecentJSONL(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	session.AppendConversationEvent(ConversationEvent{
		Kind:    conversationEventKindUserMessage,
		Summary: "first",
	})
	session.AppendConversationEvent(ConversationEvent{
		Kind:    conversationEventKindAssistantReply,
		Summary: "second",
	})
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleEventsCommand("tail 1"); err != nil {
		t.Fatalf("handleEventsCommand tail: %v", err)
	}

	text := strings.TrimSpace(output.String())
	if strings.Contains(text, "first") || !strings.Contains(text, "second") {
		t.Fatalf("expected only latest event in tail output, got %q", text)
	}
	record := SessionEventStreamRecord{}
	if err := json.Unmarshal([]byte(text), &record); err != nil {
		t.Fatalf("tail output should be JSONL: %v", err)
	}
	if record.Event.Kind != conversationEventKindAssistantReply {
		t.Fatalf("expected assistant event, got %#v", record)
	}
}
