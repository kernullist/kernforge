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

func sessionInternalContextContains(session *Session, needles ...string) bool {
	if session == nil {
		return false
	}
	for _, msg := range session.Messages {
		if !msg.Internal {
			continue
		}
		matched := true
		for _, needle := range needles {
			if !strings.Contains(msg.Text, needle) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
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

func TestPersistentMemoryPromptContextIncludesWorkspaceContinuityWithoutQueryMatch(t *testing.T) {
	store := &PersistentMemoryStore{
		Path: filepath.Join(t.TempDir(), "persistent-memory.json"),
	}
	repo := filepath.Join("F:", "repo")
	if err := store.Append(PersistentMemoryRecord{
		ID:          "mem-important",
		SessionID:   "old-session",
		SessionName: "Provider Routing",
		Workspace:   repo,
		Summary:     "DeepSeek provider routing fix touched config.go and provider.go. Keep profile role overrides separate from main model activation.",
		Importance:  PersistentMemoryHigh,
		Files:       []string{"cmd/kernforge/config.go", "cmd/kernforge/provider.go"},
		Keywords:    []string{"deepseek", "provider"},
	}); err != nil {
		t.Fatalf("Append important: %v", err)
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:         "mem-noise",
		SessionID:  "old-session",
		Workspace:  repo,
		Summary:    "Tiny chat note",
		Importance: PersistentMemoryLow,
	}); err != nil {
		t.Fatalf("Append noise: %v", err)
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:         "mem-other-workspace",
		SessionID:  "old-session",
		Workspace:  filepath.Join("F:", "other"),
		Summary:    "Important but from another workspace",
		Importance: PersistentMemoryHigh,
	}); err != nil {
		t.Fatalf("Append other workspace: %v", err)
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:         "mem-current-session",
		SessionID:  "current-session",
		Workspace:  repo,
		Summary:    "Current session should not be reinjected.",
		Importance: PersistentMemoryHigh,
	}); err != nil {
		t.Fatalf("Append current session: %v", err)
	}

	contextText := store.PromptContext(repo, "계속 진행해줘", "current-session")
	if !strings.Contains(contextText, "Workspace continuity:") {
		t.Fatalf("expected workspace continuity section, got %q", contextText)
	}
	if !strings.Contains(contextText, "mem-important") || !strings.Contains(contextText, "DeepSeek provider routing fix") {
		t.Fatalf("expected important memory in continuity context, got %q", contextText)
	}
	for _, unwanted := range []string{"mem-noise", "mem-other-workspace", "mem-current-session"} {
		if strings.Contains(contextText, unwanted) {
			t.Fatalf("did not expect %s in continuity context: %q", unwanted, contextText)
		}
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
	var progressEvents []ProgressEvent
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
		EmitProgressEvent: func(event ProgressEvent) {
			progressEvents = append(progressEvents, event)
		},
	}

	reply, err := agent.Reply(context.Background(), "update login auth flow")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "Implemented the login update.") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(agent.Session.Messages) == 0 || !sessionInternalContextContains(agent.Session, "Relevant persistent memory from past sessions") {
		t.Fatalf("expected injected persistent memory in user message, got %#v", agent.Session.Messages)
	}
	if !sessionInternalContextContains(agent.Session, "mem-1") {
		t.Fatalf("expected injected citation marker, got %#v", agent.Session.Messages)
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Count != 2 {
		t.Fatalf("expected captured memory record, got count=%d", stats.Count)
	}
}

func TestAgentReplyInjectsWorkspaceContinuityMemoryWithoutQueryMatch(t *testing.T) {
	dir := t.TempDir()
	store := &PersistentMemoryStore{
		Path: filepath.Join(dir, "persistent-memory.json"),
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:         "mem-continuity",
		SessionID:  "old-session",
		Workspace:  dir,
		Summary:    "Analyze-project cancellation fix: suppress late provider progress after cancellation and keep worker callbacks cancel-aware.",
		Importance: PersistentMemoryHigh,
		Files:      []string{"cmd/kernforge/analysis_project.go", "cmd/kernforge/parallel_edit_workers.go"},
		Keywords:   []string{"cancel", "analyze-project"},
	}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	session := NewSession(dir, "fake", "fake-model", "", "default")
	var progressEvents []ProgressEvent
	agent := &Agent{
		Config: Config{AutoCompactChars: 45000},
		Client: &fakeProviderClient{
			replies: []ChatResponse{{
				Message: Message{
					Role: "assistant",
					Text: "Continuing from the previous work.",
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
		EmitProgressEvent: func(event ProgressEvent) {
			progressEvents = append(progressEvents, event)
		},
	}

	if _, err := agent.Reply(context.Background(), "계속 진행해줘"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(agent.Session.Messages) == 0 {
		t.Fatalf("expected injected user message")
	}
	if !sessionInternalContextContains(agent.Session, "Workspace continuity:", "mem-continuity") {
		t.Fatalf("expected continuity memory injection, got %#v", agent.Session.Messages)
	}
	if len(progressEvents) == 0 {
		t.Fatalf("expected visible memory progress event")
	}
	foundMemoryEvent := false
	for _, event := range progressEvents {
		if event.Kind == progressKindMemoryContext && strings.Contains(event.Message, "mem-continuity") {
			foundMemoryEvent = true
			break
		}
	}
	if !foundMemoryEvent {
		t.Fatalf("expected memory progress event with cited memory, got %#v", progressEvents)
	}
}

func TestAgentReplySuppressesContinuityMemoryForFreshDocumentArtifactTask(t *testing.T) {
	dir := t.TempDir()
	store := &PersistentMemoryStore{
		Path: filepath.Join(dir, "persistent-memory.json"),
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:         "mem-old-report",
		SessionID:  "old-session",
		Workspace:  dir,
		Summary:    "Request: 각 소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성해\n\nOutcome: wrote a stale BugReport.md summary.",
		Importance: PersistentMemoryHigh,
		Trust:      PersistentMemoryConfirmed,
		Files:      []string{"Tavern/BugReport.md"},
	}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	session := NewSession(dir, "fake", "fake-model", "", "default")
	var progressEvents []ProgressEvent
	agent := &Agent{
		Config: Config{AutoCompactChars: 45000},
		Client: &fakeProviderClient{
			replies: []ChatResponse{{
				Message: Message{
					Role: "assistant",
					Text: "보고서를 새로 작성했습니다.",
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
		EmitProgressEvent: func(event ProgressEvent) {
			progressEvents = append(progressEvents, event)
		},
	}

	_, err := agent.Reply(context.Background(), "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(agent.Session.Messages) == 0 {
		t.Fatalf("expected user message")
	}
	if sessionInternalContextContains(agent.Session, "Relevant persistent memory from past sessions") ||
		sessionInternalContextContains(agent.Session, "mem-old-report") {
		t.Fatalf("fresh document artifact task should not inject stale continuity memory, got %#v", agent.Session.Messages)
	}
	for _, event := range progressEvents {
		if event.Kind == progressKindMemoryContext {
			t.Fatalf("fresh document artifact task should not emit memory progress, got %#v", progressEvents)
		}
	}
}

func TestAgentReplyKeepsExplicitMemoryForDocumentArtifactTask(t *testing.T) {
	dir := t.TempDir()
	store := &PersistentMemoryStore{
		Path: filepath.Join(dir, "persistent-memory.json"),
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:         "mem-old-report",
		SessionID:  "old-session",
		Workspace:  dir,
		Summary:    "Request: 각 소스코드 파일들을 검토해서 버그를 찾아서 별도 문서로 생성해\n\nOutcome: wrote a BugReport.md summary.",
		Importance: PersistentMemoryHigh,
		Trust:      PersistentMemoryConfirmed,
		Files:      []string{"Tavern/BugReport.md"},
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
					Text: "메모리까지 참고했습니다.",
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

	_, err := agent.Reply(context.Background(), "워크스페이스 메모리 참고해서 각 소스코드 파일들을 검토하고 별도 문서로 생성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(agent.Session.Messages) == 0 {
		t.Fatalf("expected user message")
	}
	if !sessionInternalContextContains(agent.Session, "Relevant persistent memory from past sessions", "mem-old-report") {
		t.Fatalf("explicit memory request should keep continuity memory, got %#v", agent.Session.Messages)
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

func TestBuildPersistentMemoryRecordCapturesStructuredVerificationMetadata(t *testing.T) {
	dir := t.TempDir()
	sess := NewSession(dir, "fake", "fake-model", "", "default")
	sess.LastVerification = &VerificationReport{
		Decision: "Security-aware verification detected categories: driver, telemetry. security_categories=driver,telemetry",
		Steps: []VerificationStep{
			{
				Label:  "signtool verify driver/guard.sys",
				Scope:  "driver/guard.sys",
				Stage:  "targeted",
				Tags:   []string{"driver", "signing", "security"},
				Status: VerificationPassed,
			},
			{
				Label:       "telemetry manifest review",
				Scope:       "telemetry/provider.man",
				Stage:       "targeted",
				Tags:        []string{"telemetry", "security"},
				Status:      VerificationFailed,
				FailureKind: "runtime_error",
			},
		},
	}
	record, ok := buildPersistentMemoryRecord(Workspace{BaseRoot: dir, Root: dir}, sess, "review changes", "done", nil)
	if !ok {
		t.Fatal("expected persistent memory record")
	}
	if len(record.VerificationCategories) != 2 {
		t.Fatalf("unexpected verification categories: %#v", record.VerificationCategories)
	}
	if !sliceContainsFold(record.VerificationTags, "signing") {
		t.Fatalf("expected verification tags to include signing, got %#v", record.VerificationTags)
	}
	if !sliceContainsFold(record.VerificationArtifacts, "driver/guard.sys") {
		t.Fatalf("expected verification artifacts to include driver artifact, got %#v", record.VerificationArtifacts)
	}
	if !sliceContainsFold(record.VerificationFailures, "runtime_error") {
		t.Fatalf("expected verification failures to include runtime_error, got %#v", record.VerificationFailures)
	}
	if len(record.VerificationSeverities) == 0 {
		t.Fatalf("expected verification severities, got %#v", record)
	}
	if len(record.VerificationSignals) == 0 {
		t.Fatalf("expected verification signals, got %#v", record)
	}
	if record.VerificationMaxRisk == 0 {
		t.Fatalf("expected verification max risk, got %#v", record)
	}
}

func TestBuildPersistentMemoryRecordCapturesToolReferencesAndTaskNotes(t *testing.T) {
	dir := t.TempDir()
	sess := NewSession(dir, "fake", "fake-model", "", "default")
	sess.TaskState = &TaskState{
		Goal:           "Fix project analysis cancellation",
		CompletedSteps: []string{"suppressed late provider progress"},
		FailedAttempts: []string{"context cancel without callback guard still emitted worker errors"},
	}
	turnMessages := []Message{
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				Name:      "read_file",
				Arguments: `{"path":"cmd/kernforge/analysis_project.go"}`,
			}},
		},
		{
			Role:     "tool",
			ToolName: "grep",
			ToolMeta: map[string]any{
				"matched_paths": []string{"cmd/kernforge/main.go", "cmd/kernforge/parallel_edit_workers.go"},
			},
		},
		{
			Role:     "tool",
			ToolName: "apply_patch",
			ToolMeta: map[string]any{
				"changed_paths": []string{"cmd/kernforge/persistent_memory.go"},
			},
		},
	}
	record, ok := buildPersistentMemoryRecord(Workspace{BaseRoot: dir, Root: dir}, sess, "continue", "done", turnMessages)
	if !ok {
		t.Fatal("expected persistent memory record")
	}
	for _, want := range []string{
		"cmd/kernforge/analysis_project.go",
		"cmd/kernforge/main.go",
		"cmd/kernforge/parallel_edit_workers.go",
		"cmd/kernforge/persistent_memory.go",
	} {
		if !sliceContainsFold(record.Files, want) {
			t.Fatalf("expected record files to include %s, got %#v", want, record.Files)
		}
	}
	if !strings.Contains(record.Summary, "Task notes:") || !strings.Contains(record.Summary, "suppressed late provider progress") {
		t.Fatalf("expected task notes in summary, got %q", record.Summary)
	}
	if !sliceContainsFold(record.ToolNames, "read_file") || !sliceContainsFold(record.ToolNames, "apply_patch") {
		t.Fatalf("expected tool names to be captured, got %#v", record.ToolNames)
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
		ID:                     "mem-a",
		SessionID:              "s1",
		Workspace:              filepath.Join("F:", "repo"),
		Request:                "auth",
		Reply:                  "done",
		Summary:                "Request: auth\n\nOutcome: done",
		Importance:             PersistentMemoryHigh,
		Trust:                  PersistentMemoryConfirmed,
		Files:                  []string{"internal/auth/service.go"},
		VerificationCategories: []string{"driver"},
		VerificationTags:       []string{"signing", "security"},
		VerificationArtifacts:  []string{"driver/guard.sys"},
		Keywords:               []string{"auth"},
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
	if !strings.Contains(rendered, "Importance distribution:") || !strings.Contains(rendered, "Trust distribution:") || !strings.Contains(rendered, "Top verification categories:") {
		t.Fatalf("unexpected memory dashboard render: %q", rendered)
	}
}

func TestPersistentMemorySearchHitsUseVerificationMetadata(t *testing.T) {
	store := &PersistentMemoryStore{
		Path: filepath.Join(t.TempDir(), "persistent-memory.json"),
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:                     "mem-driver",
		SessionID:              "s1",
		Workspace:              filepath.Join("F:", "repo"),
		Request:                "review driver",
		Reply:                  "done",
		Summary:                "driver verification complete",
		VerificationCategories: []string{"driver"},
		VerificationTags:       []string{"signing", "security"},
		VerificationArtifacts:  []string{"driver/guard.sys"},
		VerificationSeverities: []string{"critical"},
		VerificationSignals:    []string{"signing"},
		VerificationMaxRisk:    92,
		Keywords:               []string{"driver"},
	}); err != nil {
		t.Fatalf("Append driver: %v", err)
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:        "mem-docs",
		SessionID: "s2",
		Workspace: filepath.Join("F:", "repo"),
		Request:   "update docs",
		Reply:     "done",
		Summary:   "docs update",
		Keywords:  []string{"docs"},
	}); err != nil {
		t.Fatalf("Append docs: %v", err)
	}
	hits, err := store.SearchHits("guard.sys signing", filepath.Join("F:", "repo"), "", 5)
	if err != nil {
		t.Fatalf("SearchHits: %v", err)
	}
	if len(hits) == 0 || hits[0].Record.ID != "mem-driver" {
		t.Fatalf("expected verification metadata match to rank first, got %#v", hits)
	}
}

func TestPersistentMemoryQueryFiltersStructuredVerificationMetadata(t *testing.T) {
	store := &PersistentMemoryStore{
		Path: filepath.Join(t.TempDir(), "persistent-memory.json"),
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:                     "mem-driver",
		SessionID:              "s1",
		Workspace:              filepath.Join("F:", "repo"),
		Summary:                "driver verification complete",
		VerificationCategories: []string{"driver"},
		VerificationTags:       []string{"signing", "security"},
		VerificationArtifacts:  []string{"driver/guard.sys"},
		VerificationFailures:   []string{"runtime_error"},
		VerificationSeverities: []string{"critical"},
		VerificationSignals:    []string{"signing"},
		VerificationMaxRisk:    92,
		Keywords:               []string{"driver"},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	results, err := store.Search("category:driver tag:signing artifact:guard.sys failure:runtime_error severity:critical signal:signing risk:>=80", filepath.Join("F:", "repo"), "", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "mem-driver" {
		t.Fatalf("unexpected filtered results: %#v", results)
	}
}

func TestPersistentMemoryDashboardFiltersStructuredVerificationMetadata(t *testing.T) {
	store := &PersistentMemoryStore{
		Path: filepath.Join(t.TempDir(), "persistent-memory.json"),
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:                     "mem-driver",
		SessionID:              "s1",
		Workspace:              filepath.Join("F:", "repo"),
		Summary:                "driver verification complete",
		VerificationCategories: []string{"driver"},
		VerificationTags:       []string{"signing"},
		VerificationArtifacts:  []string{"driver/guard.sys"},
		VerificationSeverities: []string{"critical"},
		VerificationSignals:    []string{"signing"},
		VerificationMaxRisk:    92,
		Keywords:               []string{"driver"},
	}); err != nil {
		t.Fatalf("Append driver: %v", err)
	}
	if err := store.Append(PersistentMemoryRecord{
		ID:                     "mem-telemetry",
		SessionID:              "s2",
		Workspace:              filepath.Join("F:", "repo"),
		Summary:                "telemetry verification complete",
		VerificationCategories: []string{"telemetry"},
		VerificationTags:       []string{"provider"},
		VerificationSeverities: []string{"high"},
		VerificationSignals:    []string{"provider"},
		VerificationMaxRisk:    68,
		Keywords:               []string{"telemetry"},
	}); err != nil {
		t.Fatalf("Append telemetry: %v", err)
	}
	summary, err := store.Dashboard(filepath.Join("F:", "repo"), "category:driver tag:signing severity:critical signal:signing risk:>=80", 10)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if summary.TotalRecords != 1 {
		t.Fatalf("expected one filtered record, got %#v", summary)
	}
	if len(summary.TopVerificationCategories) != 1 || summary.TopVerificationCategories[0].Name != "driver" {
		t.Fatalf("unexpected category summary: %#v", summary.TopVerificationCategories)
	}
	if len(summary.TopVerificationSeverities) != 1 || summary.TopVerificationSeverities[0].Name != "critical" {
		t.Fatalf("unexpected severity summary: %#v", summary.TopVerificationSeverities)
	}
	if len(summary.TopVerificationSignals) != 1 || summary.TopVerificationSignals[0].Name != "signing" {
		t.Fatalf("unexpected signal summary: %#v", summary.TopVerificationSignals)
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
