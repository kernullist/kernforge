package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownMemoryAppendNoteCreatesFileAndIndex(t *testing.T) {
	base := t.TempDir()
	store := NewMarkdownMemoryStore(base)
	if store == nil {
		t.Fatal("NewMarkdownMemoryStore returned nil for a valid base root")
	}

	path, err := store.AppendNote("", "first quick note")
	if err != nil {
		t.Fatalf("AppendNote() error = %v", err)
	}
	if filepath.Base(path) != "notes.md" {
		t.Fatalf("untitled note went to %q, want notes.md", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if !strings.Contains(string(data), "first quick note") {
		t.Fatalf("note body missing from file: %q", string(data))
	}

	// Index must list the note.
	indexData, err := os.ReadFile(filepath.Join(store.Dir, markdownMemoryIndexFile))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !strings.Contains(string(indexData), "notes.md") {
		t.Fatalf("index does not reference notes.md: %q", string(indexData))
	}
}

func TestMarkdownMemoryAppendTitledNoteAndAppendAgain(t *testing.T) {
	store := NewMarkdownMemoryStore(t.TempDir())
	first, err := store.AppendNote("Driver Audit", "IRQL check noted")
	if err != nil {
		t.Fatalf("first AppendNote() error = %v", err)
	}
	if filepath.Base(first) != "driver-audit.md" {
		t.Fatalf("titled note went to %q, want driver-audit.md", filepath.Base(first))
	}
	second, err := store.AppendNote("Driver Audit", "second observation")
	if err != nil {
		t.Fatalf("second AppendNote() error = %v", err)
	}
	if first != second {
		t.Fatalf("second note path %q differs from first %q", second, first)
	}
	data, _ := os.ReadFile(second)
	if !strings.Contains(string(data), "IRQL check noted") || !strings.Contains(string(data), "second observation") {
		t.Fatalf("appended note lost prior content: %q", string(data))
	}
	// Title heading should appear exactly once.
	if got := strings.Count(string(data), "# Driver Audit"); got != 1 {
		t.Fatalf("expected one title heading, got %d", got)
	}
}

func TestMarkdownMemoryListAndRead(t *testing.T) {
	store := NewMarkdownMemoryStore(t.TempDir())
	if _, err := store.AppendNote("Alpha Note", "body a"); err != nil {
		t.Fatalf("append alpha: %v", err)
	}
	if _, err := store.AppendNote("Beta Note", "body b"); err != nil {
		t.Fatalf("append beta: %v", err)
	}
	notes, err := store.ListNotes()
	if err != nil {
		t.Fatalf("ListNotes() error = %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("ListNotes() returned %d notes, want 2", len(notes))
	}
	// index.md must never appear in the listing.
	for _, note := range notes {
		if strings.EqualFold(note.File, markdownMemoryIndexFile) {
			t.Fatalf("index file leaked into ListNotes output")
		}
	}
	content, err := store.ReadNote("alpha-note")
	if err != nil {
		t.Fatalf("ReadNote() error = %v", err)
	}
	if !strings.Contains(content, "body a") {
		t.Fatalf("ReadNote missing body: %q", content)
	}
}

func TestMarkdownMemoryReadNoteRejectsTraversal(t *testing.T) {
	store := NewMarkdownMemoryStore(t.TempDir())
	for _, bad := range []string{"../secret", "sub/dir", `..\win`} {
		if _, err := store.ReadNote(bad); err == nil {
			t.Fatalf("ReadNote(%q) accepted a traversal name", bad)
		}
	}
}

func TestMarkdownMemoryEmptyBaseRootIsNil(t *testing.T) {
	if store := NewMarkdownMemoryStore("   "); store != nil {
		t.Fatal("blank base root should yield a nil store")
	}
}
