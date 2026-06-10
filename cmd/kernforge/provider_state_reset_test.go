package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderStateResetAfterCompactionClearsStickyTurnState(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	for i := 0; i < 18; i++ {
		session.AddMessage(Message{Role: "user", Text: strings.Repeat("old-context-", 12)})
	}
	agent := &Agent{
		Config: Config{
			AutoCompactChars: 80,
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	state := &ProviderTurnState{}
	state.Capture("sticky-response-id")
	seenRevision := session.ProviderStateRevision

	if _, err := agent.CompactWithTrigger(context.Background(), "compact for reset test", "auto", "test"); err != nil {
		t.Fatalf("CompactWithTrigger: %v", err)
	}
	if session.ProviderStateRevision <= seenRevision {
		t.Fatalf("expected provider state revision to advance after compaction")
	}
	if state.Value() != "sticky-response-id" {
		t.Fatalf("test setup expected local sticky state before sync, got %q", state.Value())
	}
	if session.ProviderStateRevision != seenRevision {
		state.Reset(session.LastProviderStateResetReason)
		seenRevision = session.ProviderStateRevision
	}
	req, err := http.NewRequest(http.MethodPost, "https://example.test/v1/chat/completions", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	applyProviderTurnStateHeader(req, state)
	if got := req.Header.Get(codexTurnStateHeader); got != "" {
		t.Fatalf("stale provider turn state header survived compaction reset: %q", got)
	}
	if state.ResetReason() == "" || state.ResetCount() != 1 {
		t.Fatalf("expected provider turn state reset metadata, reason=%q count=%d", state.ResetReason(), state.ResetCount())
	}
}

func TestProviderStateResetAfterManualHistoryMutation(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "작업해"})
	session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswerCandidate, Text: "초안 답변"})
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}
	before := session.ProviderStateRevision

	if !agent.discardRecentFinalAnswerCandidate("초안 답변") {
		t.Fatalf("expected final-answer candidate to be discarded")
	}
	if session.ProviderStateRevision <= before {
		t.Fatalf("expected provider state revision to advance after manual history mutation")
	}
	if session.LastProviderStateResetReason != "discard_final_answer_candidate" {
		t.Fatalf("unexpected reset reason %q", session.LastProviderStateResetReason)
	}
}

func TestProviderStateResetAfterSessionReload(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "hello"})
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ProviderStateRevision == 0 {
		t.Fatalf("expected provider state revision to advance on session load")
	}
	if loaded.LastProviderStateResetReason != "session_load" {
		t.Fatalf("expected session_load reset reason, got %q", loaded.LastProviderStateResetReason)
	}
}
