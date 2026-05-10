package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderSessionDashboardHTMLEscapesAndSummarizesState(t *testing.T) {
	now := time.Date(2026, 5, 2, 10, 30, 0, 0, time.UTC)
	snapshot := SessionDashboardSnapshot{
		GeneratedAt:    now,
		SessionID:      "session-<unsafe>",
		SessionName:    "Unsafe <session>",
		Workspace:      "C:/repo",
		BaseRoot:       "C:/repo",
		Branch:         "feature/session-dashboard",
		Provider:       "openai-codex",
		Model:          "gpt-test",
		PermissionMode: "default",
		ApproxChars:    1200,
		MessageCount:   4,
		SummaryChars:   20,
		PlanItems:      2,
		TaskCounts: []NamedCount{
			{Name: "blocked", Count: 1},
			{Name: "in_progress", Count: 1},
		},
		OpenTasks: []TaskNode{
			{
				ID:            "plan-01",
				Title:         "Fix <script>alert(1)</script>",
				Kind:          "edit",
				Status:        "blocked",
				LastFailure:   "panic <bad>",
				LifecycleNote: "needs review",
			},
		},
		ChangedFiles: []string{"cmd/kernforge/session_dashboard.go"},
		AutomationSummary: automationRuntimeSummary{
			Total:     1,
			Active:    1,
			Scheduled: 1,
			Due:       1,
		},
		Automations: []SessionAutomation{
			{
				ID:         "auto-1",
				Type:       AutomationTypePRReview,
				Command:    "/review pr --github",
				Status:     AutomationStatusActive,
				Schedule:   "every 1h",
				LastRunAt:  now.Add(-2 * time.Hour),
				NextRunAt:  now.Add(-time.Hour),
				LastResult: "failed <output>",
			},
		},
		RecentEvents: []ConversationEvent{
			{
				Kind:     conversationEventKindToolError,
				Severity: conversationSeverityError,
				Summary:  "tool returned <error>",
				Time:     now,
				Entities: map[string]string{
					"tool": "run_shell",
				},
				ArtifactRefs: []string{"artifact<1>.log"},
			},
		},
		ArtifactRefs:        []string{"artifact<1>.log"},
		LastVerification:    "Verification: passed=0 failed=1 skipped=0",
		VerificationFailure: "- go test [test_failure]: bad <assertion>",
	}

	html := renderSessionDashboardHTML(snapshot)

	for _, want := range []string{
		"Kernforge Session Dashboard",
		"Open Task Graph",
		"Automation Runtime",
		"Recent Thread Events",
		"/automation run-due",
		"Verification: passed=0 failed=1 skipped=0",
		"&lt;script&gt;alert(1)&lt;/script&gt;",
		"artifact&lt;1&gt;.log",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected dashboard HTML to contain %q, got %q", want, html)
		}
	}
	if strings.Contains(html, "<script>alert(1)</script>") || strings.Contains(html, "bad <assertion>") {
		t.Fatalf("expected user-controlled text to be escaped, got %q", html)
	}
}

func TestSessionDashboardHTMLCommandWritesArtifactAndEvent(t *testing.T) {
	root := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "agent.go"), []byte("package main\n\nfunc changed() {}\n"), 0o644); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	now := time.Now()
	session := NewSession(root, "provider", "model", "", "default")
	session.TaskGraph = &TaskGraph{Nodes: []TaskNode{
		{
			ID:     "plan-01",
			Title:  "Render session dashboard",
			Kind:   "summary",
			Status: "in_progress",
		},
	}}
	session.Automations = []SessionAutomation{
		normalizeSessionAutomation(SessionAutomation{
			ID:        "auto-test",
			Type:      AutomationTypePRReview,
			Command:   "/review pr",
			Status:    AutomationStatusActive,
			Schedule:  "every 1h",
			CreatedAt: now.Add(-3 * time.Hour),
			LastRunAt: now.Add(-2 * time.Hour),
		}),
	}
	session.LastVerification = &VerificationReport{
		GeneratedAt: now,
		Steps: []VerificationStep{
			{Label: "unit", Status: VerificationPassed},
		},
	}
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindHandoff,
		Severity: conversationSeverityInfo,
		Summary:  "handoff generated",
		ArtifactRefs: []string{
			filepath.Join(root, ".kernforge", "handoff", "latest.md"),
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

	if err := rt.handleSessionDashboardHTMLCommand(""); err != nil {
		t.Fatalf("handleSessionDashboardHTMLCommand: %v", err)
	}

	path := filepath.Join(root, ".kernforge", "session_dashboard", "latest.html")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dashboard: %v", err)
	}
	text := string(data)
	for _, want := range []string{"Kernforge Session Dashboard", "plan-01", "auto-test", "agent.go", "handoff"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected dashboard to contain %q, got %q", want, text)
		}
	}
	last := session.ConversationEvents[len(session.ConversationEvents)-1]
	if last.Kind != conversationEventKindDashboard || len(last.ArtifactRefs) != 1 || last.ArtifactRefs[0] != path {
		t.Fatalf("expected dashboard conversation event, got %#v", last)
	}
	if !strings.Contains(output.String(), "Generated session dashboard") {
		t.Fatalf("expected command output, got %q", output.String())
	}
}
