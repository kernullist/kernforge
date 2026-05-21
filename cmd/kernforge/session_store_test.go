package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionStoreSaveWritesBackup(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(root)
	session := &Session{
		ID:             "session-a",
		Name:           "saved",
		WorkingDir:     root,
		Provider:       "test",
		Model:          "test-model",
		PermissionMode: "default",
		CreatedAt:      time.Now(),
	}

	if err := store.Save(session); err != nil {
		t.Fatalf("save session: %v", err)
	}

	backupPath := filepath.Join(root, "session-a.json.bak")
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("expected session backup: %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("expected valid session backup JSON")
	}
}

func TestSessionStoreLoadRecoversCorruptPrimaryFromBackup(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(root)
	session := &Session{
		ID:             "session-a",
		Name:           "good",
		WorkingDir:     root,
		Provider:       "test",
		Model:          "test-model",
		PermissionMode: "default",
		CreatedAt:      time.Now(),
	}

	if err := store.Save(session); err != nil {
		t.Fatalf("save session: %v", err)
	}
	primaryPath := filepath.Join(root, "session-a.json")
	if err := os.WriteFile(primaryPath, []byte("{"), 0o644); err != nil {
		t.Fatalf("corrupt primary: %v", err)
	}

	loaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("load recovered session: %v", err)
	}
	if loaded.Name != "good" {
		t.Fatalf("expected backup session name, got %q", loaded.Name)
	}

	reloaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("load restored primary: %v", err)
	}
	if reloaded.Name != "good" {
		t.Fatalf("expected restored primary session name, got %q", reloaded.Name)
	}
}

func TestSessionStoreListUsesBackupWhenPrimaryIsCorrupt(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(root)
	session := &Session{
		ID:             "session-a",
		Name:           "listed",
		WorkingDir:     root,
		Provider:       "test",
		Model:          "test-model",
		PermissionMode: "default",
		CreatedAt:      time.Now(),
	}

	if err := store.Save(session); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "session-a.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("corrupt primary: %v", err)
	}

	items, err := store.List()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one listed session, got %d", len(items))
	}
	if items[0].ID != "session-a" || items[0].Name != "listed" {
		t.Fatalf("unexpected listed session: %#v", items[0])
	}
}

func TestSessionStoreSearchIsCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(root)
	session := NewSession(root, "test", "test-model", "", "default")
	session.ID = "session-search"
	session.Name = "Search Target"
	session.Summary = "Runtime gate work is unrelated."
	session.Messages = []Message{
		{Role: "user", Text: "Please compare Codex Thread Search behavior against kernforge."},
	}

	if err := store.Save(session); err != nil {
		t.Fatalf("save session: %v", err)
	}

	results, err := store.Search("thread search", 10)
	if err != nil {
		t.Fatalf("search sessions: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one search result, got %d", len(results))
	}
	if results[0].ID != "session-search" {
		t.Fatalf("unexpected result id: %#v", results[0])
	}
	if !strings.Contains(results[0].Snippet, "Thread Search") {
		t.Fatalf("expected original-case snippet, got %q", results[0].Snippet)
	}
}

func TestSessionStoreSearchUsesBackupWhenPrimaryIsCorrupt(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(root)
	session := NewSession(root, "test", "test-model", "", "default")
	session.ID = "session-backup"
	session.Name = "Backup Target"
	session.Summary = "Backup-only search evidence."

	if err := store.Save(session); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "session-backup.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("corrupt primary: %v", err)
	}

	results, err := store.Search("backup-only", 10)
	if err != nil {
		t.Fatalf("search sessions: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one backup-backed search result, got %d", len(results))
	}
	if results[0].ID != "session-backup" {
		t.Fatalf("unexpected result id: %#v", results[0])
	}
}

func TestSessionsCommandSearchesSavedContent(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(root)
	session := NewSession(root, "test", "test-model", "", "default")
	session.ID = "session-command"
	session.Name = "Command Target"
	session.Summary = "Codex parity search through slash command."

	if err := store.Save(session); err != nil {
		t.Fatalf("save session: %v", err)
	}

	var out bytes.Buffer
	rt := &runtimeState{
		writer:  &out,
		ui:      NewUI(),
		store:   store,
		session: NewSession(root, "test", "test-model", "", "default"),
	}
	if _, err := rt.handleCommand(Command{Name: "sessions", Args: "search parity"}); err != nil {
		t.Fatalf("handleCommand(sessions search): %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "Session Search") {
		t.Fatalf("expected session search header, got %q", output)
	}
	if !strings.Contains(output, "session-command") {
		t.Fatalf("expected matching session id, got %q", output)
	}
	if !strings.Contains(output, "Codex parity search") {
		t.Fatalf("expected matching snippet, got %q", output)
	}
}
