package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeProviderClient struct {
	replies []ChatResponse
	index   int
}

func (f *fakeProviderClient) Name() string { return "fake" }

func (f *fakeProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	_ = req
	if f.index >= len(f.replies) {
		return ChatResponse{Message: Message{Role: "assistant", Text: "ok"}}, nil
	}
	resp := f.replies[f.index]
	f.index++
	return resp, nil
}

func TestPersistentMemoryAppendSearchAndExcludeSession(t *testing.T) {
	store := &PersistentMemoryStore{
		Path: filepath.Join(t.TempDir(), "persistent-memory.json"),
	}
	err := store.Append(PersistentMemoryRecord{
		ID:        "mem-1",
		SessionID: "session-a",
		Workspace: filepath.Join("F:", "repo"),
		Request:   "fix authentication",
		Reply:     "updated the login flow",
		Summary:   "Request: fix authentication\n\nOutcome: updated the login flow",
		Keywords:  []string{"authentication", "login"},
	})
	if err != nil {
		t.Fatalf("Append first: %v", err)
	}
	err = store.Append(PersistentMemoryRecord{
		ID:        "mem-2",
		SessionID: "session-b",
		Workspace: filepath.Join("F:", "other"),
		Request:   "cleanup docs",
		Reply:     "updated docs",
		Summary:   "Request: cleanup docs\n\nOutcome: updated docs",
		Keywords:  []string{"docs"},
	})
	if err != nil {
		t.Fatalf("Append second: %v", err)
	}

	results, err := store.Search("authentication", filepath.Join("F:", "repo"), "session-current", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "mem-1" {
		t.Fatalf("unexpected search results: %#v", results)
	}
	if results[0].Importance == "" {
		t.Fatalf("expected stored memory importance tier, got %#v", results[0])
	}

	excluded, err := store.Search("authentication", filepath.Join("F:", "repo"), "session-a", 5)
	if err != nil {
		t.Fatalf("Search exclude: %v", err)
	}
	if len(excluded) != 0 {
		t.Fatalf("expected excluded session to be skipped, got %#v", excluded)
	}
}

func TestPersistentMemoryRelevantContextFormatsResults(t *testing.T) {
	store := &PersistentMemoryStore{
		Path: filepath.Join(t.TempDir(), "persistent-memory.json"),
	}
	err := store.Append(PersistentMemoryRecord{
		ID:        "mem-1",
		SessionID: "session-a",
		Workspace: filepath.Join("F:", "repo"),
		Summary:   "Request: fix auth bug\n\nOutcome: updated the auth middleware and tests",
		Keywords:  []string{"auth", "middleware"},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	contextText := store.RelevantContext(filepath.Join("F:", "repo"), "auth middleware", "session-current")
	if !strings.Contains(contextText, "fix auth bug") {
		t.Fatalf("expected formatted memory context, got %q", contextText)
	}
	if !strings.Contains(contextText, "mem-1") {
		t.Fatalf("expected memory citation in context, got %q", contextText)
	}
	if !strings.Contains(contextText, "medium") && !strings.Contains(contextText, "high") && !strings.Contains(contextText, "low") {
		t.Fatalf("expected importance tier in context citation, got %q", contextText)
	}
}

func TestAgentReplyInjectsAndCapturesPersistentMemory(t *testing.T) {
	dir := t.TempDir()
	store := &PersistentMemoryStore{
		Path: filepath.Join(dir, "persistent-memory.json"),
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:        "mem-1",
		SessionID: "old-session",
		Workspace: dir,
		Summary:   "Request: update login\n\nOutcome: switched to token auth",
		Keywords:  []string{"login", "token", "auth"},
	}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	session := NewSession(dir, "fake", "fake-model", "", "default")
	agent := &Agent{
		Config: Config{AutoCompactChars: 45000},
		Client: &fakeProviderClient{
			replies: []ChatResponse{{
				Message: Message{
					Role: "assistant",
					Text: "Implemented the login update.",
				},
			}},
		},
		Tools: NewToolRegistry(),
		Workspace: Workspace{
			BaseRoot: dir,
			Root:     dir,
		},
		Session: session,
		Store:   NewSessionStore(filepath.Join(dir, "sessions")),
		LongMem: store,
	}

	reply, err := agent.Reply(context.Background(), "update login auth flow")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Implemented the login update.") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(agent.Session.Messages) == 0 || !strings.Contains(agent.Session.Messages[0].Text, "Relevant persistent memory from past sessions") {
		t.Fatalf("expected injected persistent memory in user message, got %#v", agent.Session.Messages)
	}
	if !strings.Contains(agent.Session.Messages[0].Text, "mem-1") {
		t.Fatalf("expected injected citation marker, got %#v", agent.Session.Messages[0].Text)
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Count != 2 {
		t.Fatalf("expected captured memory record, got count=%d", stats.Count)
	}
}

func TestPersistentMemorySearchHitsPreferExactReferencedFile(t *testing.T) {
	store := &PersistentMemoryStore{
		Path: filepath.Join(t.TempDir(), "persistent-memory.json"),
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:          "mem-a",
		SessionID:   "session-a",
		SessionName: "Auth Work",
		Workspace:   filepath.Join("F:", "repo"),
		Request:     "update auth service",
		Reply:       "done",
		Summary:     "Request: update auth service\n\nOutcome: done",
		Files:       []string{"internal/auth/service.go"},
		Keywords:    []string{"auth", "service"},
	}); err != nil {
		t.Fatalf("Append auth: %v", err)
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:          "mem-b",
		SessionID:   "session-b",
		SessionName: "Docs Work",
		Workspace:   filepath.Join("F:", "repo"),
		Request:     "update docs",
		Reply:       "done",
		Summary:     "Request: update docs\n\nOutcome: done",
		Files:       []string{"docs/readme.md"},
		Keywords:    []string{"docs"},
	}); err != nil {
		t.Fatalf("Append docs: %v", err)
	}
	hits, err := store.SearchHits("@internal/auth/service.go fix flow", filepath.Join("F:", "repo"), "", 5)
	if err != nil {
		t.Fatalf("SearchHits: %v", err)
	}
	if len(hits) == 0 || hits[0].Record.ID != "mem-a" {
		t.Fatalf("expected exact file ref memory first, got %#v", hits)
	}
}

func TestPersistentMemoryImportanceBoostHelpsPrioritizeVerifiedMemory(t *testing.T) {
	store := &PersistentMemoryStore{
		Path: filepath.Join(t.TempDir(), "persistent-memory.json"),
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:          "mem-low",
		SessionID:   "session-a",
		SessionName: "Short note",
		Workspace:   filepath.Join("F:", "repo"),
		Request:     "auth",
		Reply:       "done",
		Summary:     "Request: auth\n\nOutcome: done",
		Importance:  PersistentMemoryLow,
		Keywords:    []string{"auth"},
	}); err != nil {
		t.Fatalf("Append low: %v", err)
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:                  "mem-high",
		SessionID:           "session-b",
		SessionName:         "Verified auth work",
		Workspace:           filepath.Join("F:", "repo"),
		Request:             "auth",
		Reply:               "done",
		Summary:             "Request: auth\n\nOutcome: done",
		Importance:          PersistentMemoryHigh,
		VerificationSummary: "Verification: passed=1 failed=0 skipped=0",
		Keywords:            []string{"auth"},
	}); err != nil {
		t.Fatalf("Append high: %v", err)
	}
	hits, err := store.SearchHits("auth", filepath.Join("F:", "repo"), "", 5)
	if err != nil {
		t.Fatalf("SearchHits: %v", err)
	}
	if len(hits) == 0 || hits[0].Record.ID != "mem-high" {
		t.Fatalf("expected high-importance memory to rank first, got %#v", hits)
	}
}

func TestPersistentMemoryPromoteDemoteAndTrustAdjustments(t *testing.T) {
	store := &PersistentMemoryStore{
		Path: filepath.Join(t.TempDir(), "persistent-memory.json"),
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:         "mem-x",
		SessionID:  "session-a",
		Workspace:  filepath.Join("F:", "repo"),
		Request:    "auth",
		Reply:      "done",
		Summary:    "Request: auth\n\nOutcome: done",
		Importance: PersistentMemoryLow,
		Trust:      PersistentMemoryTentative,
		Keywords:   []string{"auth"},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	record, ok, err := store.Promote("mem-x")
	if err != nil || !ok {
		t.Fatalf("Promote: ok=%v err=%v", ok, err)
	}
	if record.Importance != PersistentMemoryMedium {
		t.Fatalf("expected medium after promote, got %#v", record)
	}
	record, ok, err = store.SetTrust("mem-x", PersistentMemoryConfirmed)
	if err != nil || !ok {
		t.Fatalf("SetTrust confirm: ok=%v err=%v", ok, err)
	}
	if record.Trust != PersistentMemoryConfirmed {
		t.Fatalf("expected confirmed trust, got %#v", record)
	}
	record, ok, err = store.Demote("mem-x")
	if err != nil || !ok {
		t.Fatalf("Demote: ok=%v err=%v", ok, err)
	}
	if record.Importance != PersistentMemoryLow {
		t.Fatalf("expected low after demote, got %#v", record)
	}
}

func TestPersistentMemoryDashboardSummarizesImportanceAndTrust(t *testing.T) {
	store := &PersistentMemoryStore{
		Path: filepath.Join(t.TempDir(), "persistent-memory.json"),
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:         "mem-a",
		SessionID:  "s1",
		Workspace:  filepath.Join("F:", "repo"),
		Request:    "auth",
		Reply:      "done",
		Summary:    "Request: auth\n\nOutcome: done",
		Importance: PersistentMemoryHigh,
		Trust:      PersistentMemoryConfirmed,
		Files:      []string{"internal/auth/service.go"},
		Keywords:   []string{"auth"},
	}); err != nil {
		t.Fatalf("Append a: %v", err)
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:         "mem-b",
		SessionID:  "s2",
		Workspace:  filepath.Join("F:", "repo"),
		Request:    "docs",
		Reply:      "done",
		Summary:    "Request: docs\n\nOutcome: done",
		Importance: PersistentMemoryLow,
		Trust:      PersistentMemoryTentative,
		Files:      []string{"docs/readme.md"},
		Keywords:   []string{"docs"},
	}); err != nil {
		t.Fatalf("Append b: %v", err)
	}
	summary, err := store.Dashboard(filepath.Join("F:", "repo"), "importance:high trust:confirmed", 10)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if summary.TotalRecords != 1 {
		t.Fatalf("expected filtered dashboard to show one record, got %#v", summary)
	}
	rendered := renderPersistentMemoryDashboard(summary)
	if !strings.Contains(rendered, "Importance distribution:") || !strings.Contains(rendered, "Trust distribution:") {
		t.Fatalf("unexpected memory dashboard render: %q", rendered)
	}
}

func TestPersistentMemoryDashboardHTMLWritesFile(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", tempRoot)
	summary := PersistentMemoryDashboardSummary{
		Scope:        "current workspace",
		TotalRecords: 1,
		ByImportance: []NamedCount{{Name: "high", Count: 1}},
		ByTrust:      []NamedCount{{Name: "confirmed", Count: 1}},
		Recent: []PersistentMemoryRecord{{
			ID:         "mem-1",
			SessionID:  "session-a",
			Workspace:  filepath.Join("F:", "repo"),
			Summary:    "Request: auth\n\nOutcome: done",
			Importance: PersistentMemoryHigh,
			Trust:      PersistentMemoryConfirmed,
		}},
		LastUpdated: time.Now(),
	}
	path, err := createPersistentMemoryDashboardHTML(summary)
	if err != nil {
		t.Fatalf("createPersistentMemoryDashboardHTML: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected html memory dashboard file to exist: %v", err)
	}
}
