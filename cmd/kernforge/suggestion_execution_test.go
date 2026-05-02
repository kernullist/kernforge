package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestSafeSuggestionCommandExecutesRecoveryArtifacts(t *testing.T) {
	root := initTestGitRepo(t)
	var output bytes.Buffer
	session := NewSession(root, "provider", "model", "", "default")
	session.LastVerification = &VerificationReport{
		GeneratedAt: time.Now(),
		Workspace:   root,
		Steps: []VerificationStep{{
			Label:       "go test",
			Command:     "go test ./...",
			Status:      VerificationFailed,
			FailureKind: "test_failure",
			Output:      "FAIL package",
		}},
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

	result, err := rt.executeSafeSuggestionCommand("/recover")
	if err != nil {
		t.Fatalf("execute /recover: %v", err)
	}
	if result != "executed /recover" {
		t.Fatalf("unexpected recover result: %q", result)
	}
	result, err = rt.executeSafeSuggestionCommand("/completion-audit")
	if err != nil {
		t.Fatalf("execute /completion-audit: %v", err)
	}
	if result != "executed /completion-audit" {
		t.Fatalf("unexpected completion-audit result: %q", result)
	}
	for _, path := range []string{
		filepath.Join(root, ".kernforge", "recovery", "latest.md"),
		filepath.Join(root, ".kernforge", "completion_audit", "latest.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected artifact %s: %v", path, err)
		}
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

func TestPRReviewAutomationIncludesGitHubContextWhenRequested(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	session := NewSession(root, "provider", "model", "", "default")
	previous := runPRReviewGitHubCommand
	defer func() {
		runPRReviewGitHubCommand = previous
	}()
	var gotArgs []string
	runPRReviewGitHubCommand = func(root string, args ...string) (string, error) {
		gotArgs = append([]string(nil), args...)
		return `{
			"url": "https://github.com/example/repo/pull/7",
			"title": "Harden automation",
			"state": "OPEN",
			"baseRefName": "main",
			"headRefName": "feature/automation",
			"isDraft": false,
			"reviewDecision": "APPROVED",
			"mergeStateStatus": "CLEAN",
			"author": {"login": "kern"},
			"comments": [{"body": "check this"}],
			"reviews": [{"state": "APPROVED", "author": {"login": "reviewer"}}],
			"statusCheckRollup": [
				{"name": "test", "conclusion": "SUCCESS"},
				{"name": "lint", "status": "IN_PROGRESS"}
			]
		}`, nil
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

	if err := rt.handlePRReviewAutomationCommand("--github"); err != nil {
		t.Fatalf("handlePRReviewAutomationCommand: %v", err)
	}
	if strings.Join(gotArgs, " ") != "pr view --json url,title,state,author,baseRefName,headRefName,isDraft,reviewDecision,mergeStateStatus,comments,reviews,statusCheckRollup" {
		t.Fatalf("unexpected gh args: %#v", gotArgs)
	}
	path := filepath.Join(root, ".kernforge", "pr_review", "latest.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"## GitHub PR",
		"Status: connected",
		"URL: https://github.com/example/repo/pull/7",
		"Title: Harden automation",
		"Review Decision: APPROVED",
		"Reviews: approved=1",
		"Comments: 1",
		"Checks: in_progress=1 success=1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected GitHub report to contain %q, got %q", want, text)
		}
	}
	if len(session.ConversationEvents) == 0 || session.ConversationEvents[0].Entities["github"] != "connected" {
		t.Fatalf("expected connected github event, got %#v", session.ConversationEvents)
	}
}

func TestPRReviewAutomationWritesCommentDraft(t *testing.T) {
	root := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "agent.go"), []byte("package main\n\nfunc changed() {}\n"), 0o644); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
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

	if err := rt.handlePRReviewAutomationCommand("--draft-comments"); err != nil {
		t.Fatalf("handlePRReviewAutomationCommand: %v", err)
	}
	path := filepath.Join(root, ".kernforge", "pr_review", "comments.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read comment draft: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"# PR Review Comment Draft",
		"agent.go",
		"targeted go test coverage",
		"Before Posting",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected comment draft to contain %q, got %q", want, text)
		}
	}
	if len(session.ConversationEvents) == 0 || session.ConversationEvents[0].Entities["comment_draft"] != "generated" {
		t.Fatalf("expected generated comment draft event, got %#v", session.ConversationEvents)
	}
}

func TestPRReviewAutomationPostsCommentDraftWhenExplicitlyRequested(t *testing.T) {
	root := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "agent.go"), []byte("package main\n\nfunc changed() {}\n"), 0o644); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	previous := runPRReviewGitHubCommand
	defer func() {
		runPRReviewGitHubCommand = previous
	}()
	var calls [][]string
	runPRReviewGitHubCommand = func(root string, args ...string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		if strings.Join(args, " ") == "pr view --json url,title,state,author,baseRefName,headRefName,isDraft,reviewDecision,mergeStateStatus,comments,reviews,statusCheckRollup" {
			return `{
				"url": "https://github.com/example/repo/pull/9",
				"title": "Review write side",
				"state": "OPEN",
				"baseRefName": "main",
				"headRefName": "feature/post-comments",
				"isDraft": false,
				"reviewDecision": "REVIEW_REQUIRED",
				"mergeStateStatus": "DIRTY",
				"author": {"login": "kern"},
				"comments": [],
				"reviews": [],
				"statusCheckRollup": []
			}`, nil
		}
		if len(args) == 5 && strings.Join(args[:4], " ") == "pr review --comment --body-file" {
			if !strings.HasSuffix(args[4], filepath.Join(".kernforge", "pr_review", "comments.md")) {
				t.Fatalf("unexpected body file: %#v", args)
			}
			return "review submitted", nil
		}
		t.Fatalf("unexpected gh args: %#v", args)
		return "", nil
	}
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

	if err := rt.handlePRReviewAutomationCommand("--post-comments"); err != nil {
		t.Fatalf("handlePRReviewAutomationCommand: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected gh view and review calls, got %#v", calls)
	}
	commentPath := filepath.Join(root, ".kernforge", "pr_review", "comments.md")
	if _, err := os.Stat(commentPath); err != nil {
		t.Fatalf("expected comment draft: %v", err)
	}
	if len(session.ConversationEvents) == 0 {
		t.Fatalf("expected conversation event")
	}
	event := session.ConversationEvents[len(session.ConversationEvents)-1]
	if event.Entities["comment_post"] != "posted" || event.Entities["post_result"] != "review submitted" {
		t.Fatalf("expected posted event, got %#v", event)
	}
	if !strings.Contains(output.String(), "Posted PR review comments") {
		t.Fatalf("expected posted output, got %q", output.String())
	}
}

func TestPRReviewAutomationResolvesExplicitReviewThread(t *testing.T) {
	root := initTestGitRepo(t)
	previous := runPRReviewGitHubCommand
	defer func() {
		runPRReviewGitHubCommand = previous
	}()
	var resolveArgs []string
	runPRReviewGitHubCommand = func(root string, args ...string) (string, error) {
		if strings.Join(args, " ") == "pr view --json url,title,state,author,baseRefName,headRefName,isDraft,reviewDecision,mergeStateStatus,comments,reviews,statusCheckRollup" {
			return `{"url":"https://github.com/example/repo/pull/10","title":"Resolve thread","state":"OPEN","author":{"login":"kern"}}`, nil
		}
		if len(args) == 6 &&
			args[0] == "api" &&
			args[1] == "graphql" &&
			args[2] == "-f" &&
			args[3] == "query=mutation($id:ID!){resolveReviewThread(input:{threadId:$id}){thread{id isResolved}}}" &&
			args[4] == "-F" &&
			args[5] == "id=PRRT_123" {
			resolveArgs = append([]string(nil), args...)
			return `{"data":{"resolveReviewThread":{"thread":{"id":"PRRT_123","isResolved":true}}}}`, nil
		}
		t.Fatalf("unexpected gh args: %#v", args)
		return "", nil
	}
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

	if err := rt.handlePRReviewAutomationCommand("--resolve-thread PRRT_123"); err != nil {
		t.Fatalf("handlePRReviewAutomationCommand: %v", err)
	}
	if len(resolveArgs) == 0 {
		t.Fatalf("expected resolve graphql call")
	}
	event := session.ConversationEvents[len(session.ConversationEvents)-1]
	if event.Entities["thread_resolve"] != "resolved" || event.Entities["resolved_threads"] != "PRRT_123" {
		t.Fatalf("expected resolved thread event, got %#v", event)
	}
	if !strings.Contains(output.String(), "Resolved GitHub review threads: 1") {
		t.Fatalf("expected resolve output, got %q", output.String())
	}
}

func TestPRReviewAutomationCreatesExplicitFollowUpIssue(t *testing.T) {
	root := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "agent.go"), []byte("package main\n\nfunc changed() {}\n"), 0o644); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	previous := runPRReviewGitHubCommand
	defer func() {
		runPRReviewGitHubCommand = previous
	}()
	var issueArgs []string
	runPRReviewGitHubCommand = func(root string, args ...string) (string, error) {
		if strings.Join(args, " ") == "pr view --json url,title,state,author,baseRefName,headRefName,isDraft,reviewDecision,mergeStateStatus,comments,reviews,statusCheckRollup" {
			return `{"url":"https://github.com/example/repo/pull/11","title":"Create issue","state":"OPEN","reviewDecision":"CHANGES_REQUESTED","author":{"login":"kern"}}`, nil
		}
		if len(args) == 16 &&
			args[0] == "issue" &&
			args[1] == "create" &&
			args[2] == "--title" &&
			args[3] == "PR review follow-up: Create issue" &&
			args[4] == "--body-file" &&
			strings.HasSuffix(args[5], filepath.Join(".kernforge", "pr_review", "issue.md")) &&
			args[6] == "--label" &&
			args[7] == "bug" &&
			args[8] == "--label" &&
			args[9] == "security" &&
			args[10] == "--assignee" &&
			args[11] == "kern" &&
			args[12] == "--assignee" &&
			args[13] == "reviewer" &&
			args[14] == "--milestone" &&
			args[15] == "May 2026" {
			issueArgs = append([]string(nil), args...)
			return "https://github.com/example/repo/issues/12", nil
		}
		t.Fatalf("unexpected gh args: %#v", args)
		return "", nil
	}
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

	if err := rt.handlePRReviewAutomationCommand(`--create-issue --label bug,security --assignee kern --assignee=reviewer --milestone "May 2026"`); err != nil {
		t.Fatalf("handlePRReviewAutomationCommand: %v", err)
	}
	if len(issueArgs) == 0 {
		t.Fatalf("expected issue create call")
	}
	issuePath := filepath.Join(root, ".kernforge", "pr_review", "issue.md")
	data, err := os.ReadFile(issuePath)
	if err != nil {
		t.Fatalf("read issue draft: %v", err)
	}
	if !strings.Contains(string(data), "PR Review Follow-up") ||
		!strings.Contains(string(data), "agent.go") ||
		!strings.Contains(string(data), "Labels: bug, security") ||
		!strings.Contains(string(data), "Assignees: kern, reviewer") ||
		!strings.Contains(string(data), "Milestone: May 2026") {
		t.Fatalf("unexpected issue draft: %q", string(data))
	}
	event := session.ConversationEvents[len(session.ConversationEvents)-1]
	if event.Entities["issue_create"] != "created" || event.Entities["issue_result"] != "https://github.com/example/repo/issues/12" {
		t.Fatalf("expected created issue event, got %#v", event)
	}
	if event.Entities["issue_labels"] != "bug,security" || event.Entities["issue_assignees"] != "kern,reviewer" || event.Entities["issue_milestone"] != "May 2026" {
		t.Fatalf("expected issue metadata event, got %#v", event)
	}
	if !strings.Contains(output.String(), "Created GitHub follow-up issue") {
		t.Fatalf("expected issue output, got %q", output.String())
	}
}

func TestPRReviewAutomationParsesIssueOperationalFields(t *testing.T) {
	options := parsePRReviewAutomationOptions(`--draft-issue --label=bug,security --labels regression --assignee kern,reviewer --milestone "May 2026"`)
	if !options.DraftIssue {
		t.Fatalf("expected draft issue")
	}
	if strings.Join(options.IssueLabels, ",") != "bug,security,regression" {
		t.Fatalf("unexpected labels: %#v", options.IssueLabels)
	}
	if strings.Join(options.IssueAssignees, ",") != "kern,reviewer" {
		t.Fatalf("unexpected assignees: %#v", options.IssueAssignees)
	}
	if options.IssueMilestone != "May 2026" {
		t.Fatalf("unexpected milestone: %q", options.IssueMilestone)
	}
	if options.HasGitHubWrite() {
		t.Fatalf("draft issue metadata should not be classified as a GitHub write")
	}
}

func TestPRReviewPostCommentsIsRejectedFromAutomaticExecution(t *testing.T) {
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

	if _, err := rt.executeSafeSuggestionCommand("/review-pr --post-comments"); err == nil {
		t.Fatalf("expected automatic post-comments execution to be rejected")
	}
	if err := validateAutomationCommand(AutomationTypePRReview, "/review-pr --post-comments"); err == nil {
		t.Fatalf("expected automation post-comments command to be rejected")
	}
	if _, err := rt.executeSafeSuggestionCommand("/review-pr --resolve-thread PRRT_123"); err == nil {
		t.Fatalf("expected automatic resolve-thread execution to be rejected")
	}
	if err := validateAutomationCommand(AutomationTypePRReview, "/review-pr --resolve-thread PRRT_123"); err == nil {
		t.Fatalf("expected automation resolve-thread command to be rejected")
	}
	if _, err := rt.executeSafeSuggestionCommand("/review-pr --create-issue"); err == nil {
		t.Fatalf("expected automatic create-issue execution to be rejected")
	}
	if err := validateAutomationCommand(AutomationTypePRReview, "/review-pr --create-issue"); err == nil {
		t.Fatalf("expected automation create-issue command to be rejected")
	}
}

func TestPRReviewChangedFilesParsesGitStatusShortForms(t *testing.T) {
	files := prReviewChangedFiles(strings.Join([]string{
		"M first.go",
		" M cmd/kernforge/main.go",
		"R  old/name.go -> new/name.go",
		"?? docs/new.md",
		"warning: in the working copy of 'README.md', LF will be replaced by CRLF the next time Git touches it",
		"plain.txt",
	}, "\n"))
	want := []string{"first.go", "cmd/kernforge/main.go", "new/name.go", "docs/new.md", "plain.txt"}
	for _, item := range want {
		found := false
		for _, file := range files {
			if file == item {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected parsed files to include %q, got %#v", item, files)
		}
	}
	for _, file := range files {
		if strings.HasPrefix(strings.ToLower(file), "warning:") {
			t.Fatalf("expected git warning line to be ignored, got %#v", files)
		}
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

func TestAutomationAddSupportsIntervalSchedule(t *testing.T) {
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

	if err := rt.handleAutomationCommand("add pr-review --every 2h /review-pr"); err != nil {
		t.Fatalf("handleAutomationCommand: %v", err)
	}
	if len(session.Automations) != 1 {
		t.Fatalf("expected automation to be added, got %#v", session.Automations)
	}
	item := session.Automations[0]
	if item.Schedule != "every 2h0m0s" {
		t.Fatalf("expected normalized interval schedule, got %#v", item)
	}
	if item.NextRunAt.IsZero() {
		t.Fatalf("expected next run time, got %#v", item)
	}
	if !strings.Contains(item.NextRunHint, "/automation run-due") {
		t.Fatalf("expected run-due hint, got %q", item.NextRunHint)
	}
}

func TestAutomationRunDueExecutesOnlyDueScheduledItems(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	session := NewSession(root, "provider", "model", "", "default")
	due := normalizeSessionAutomation(SessionAutomation{
		ID:        "auto-due",
		Type:      AutomationTypePRReview,
		Command:   "/review-pr",
		Status:    AutomationStatusActive,
		Schedule:  "every 1h",
		CreatedAt: now.Add(-3 * time.Hour),
		UpdatedAt: now.Add(-3 * time.Hour),
		LastRunAt: now.Add(-2 * time.Hour),
		NextRunAt: now.Add(-time.Hour),
	})
	later := normalizeSessionAutomation(SessionAutomation{
		ID:        "auto-later",
		Type:      AutomationTypePRReview,
		Command:   "/review-pr",
		Status:    AutomationStatusActive,
		Schedule:  "every 1h",
		CreatedAt: now,
		UpdatedAt: now,
		NextRunAt: now.Add(time.Hour),
	})
	session.Automations = []SessionAutomation{due, later}
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

	if err := rt.runDueAutomations(now); err != nil {
		t.Fatalf("runDueAutomations: %v", err)
	}
	if session.Automations[0].LastRunAt != now {
		t.Fatalf("expected due automation to run, got %#v", session.Automations[0])
	}
	if !session.Automations[0].NextRunAt.After(now) {
		t.Fatalf("expected due automation next run to advance, got %#v", session.Automations[0])
	}
	if !session.Automations[1].LastRunAt.IsZero() {
		t.Fatalf("expected later automation not to run, got %#v", session.Automations[1])
	}
	path := filepath.Join(root, ".kernforge", "pr_review", "latest.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected PR review report: %v", err)
	}
	if !strings.Contains(output.String(), "Due automations completed: 1") {
		t.Fatalf("expected due completion output, got %q", output.String())
	}
}

func TestAutomationDigestSurfacesDueAndFailedItems(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	now := time.Now().UTC()
	session := NewSession(root, "provider", "model", "", "default")
	session.Automations = []SessionAutomation{
		normalizeSessionAutomation(SessionAutomation{
			ID:        "auto-due",
			Type:      AutomationTypePRReview,
			Command:   "/review-pr",
			Status:    AutomationStatusActive,
			Schedule:  "every 1h",
			CreatedAt: now.Add(-3 * time.Hour),
			LastRunAt: now.Add(-2 * time.Hour),
			NextRunAt: now.Add(-time.Hour),
		}),
		normalizeSessionAutomation(SessionAutomation{
			ID:         "auto-failed",
			Type:       AutomationTypeRecurringVerification,
			Command:    "/verify",
			Status:     AutomationStatusFailed,
			Schedule:   "manual-recurring",
			LastResult: "verification failed",
		}),
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

	if err := rt.handleAutomationCommand("digest"); err != nil {
		t.Fatalf("handleAutomationCommand: %v", err)
	}
	text := output.String()
	for _, want := range []string{"Automation Digest", "due=1", "failed=1", "auto-due", "auto-failed", "verification failed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected digest to contain %q, got %q", want, text)
		}
	}
}

func TestAutomationStartupNoticeSurfacesAttentionOnly(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	now := time.Now().UTC()
	session := NewSession(root, "provider", "model", "", "default")
	session.Automations = []SessionAutomation{
		normalizeSessionAutomation(SessionAutomation{
			ID:        "auto-due",
			Type:      AutomationTypePRReview,
			Command:   "/review-pr",
			Status:    AutomationStatusActive,
			Schedule:  "every 1h",
			CreatedAt: now.Add(-3 * time.Hour),
			LastRunAt: now.Add(-2 * time.Hour),
			NextRunAt: now.Add(-time.Hour),
		}),
	}
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
	}

	rt.printAutomationStartupNotice(now)
	text := output.String()
	if !strings.Contains(text, "Automation attention") || !strings.Contains(text, "due=1") {
		t.Fatalf("expected automation startup notice, got %q", text)
	}

	output.Reset()
	session.Automations[0].LastRunAt = now
	session.Automations[0].NextRunAt = now.Add(time.Hour)
	session.Automations[0] = refreshAutomationScheduleHint(session.Automations[0], now)
	rt.printAutomationStartupNotice(now)
	if strings.TrimSpace(output.String()) != "" {
		t.Fatalf("expected no notice when there is no attention state, got %q", output.String())
	}
}

func TestAutomationMonitorRunsDueAndPrintsPostDigest(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	now := time.Now().UTC()
	session := NewSession(root, "provider", "model", "", "default")
	session.Automations = []SessionAutomation{
		normalizeSessionAutomation(SessionAutomation{
			ID:        "auto-due",
			Type:      AutomationTypePRReview,
			Command:   "/review-pr",
			Status:    AutomationStatusActive,
			Schedule:  "every 1h",
			CreatedAt: now.Add(-3 * time.Hour),
			LastRunAt: now.Add(-2 * time.Hour),
			NextRunAt: now.Add(-time.Hour),
		}),
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

	if err := rt.handleAutomationCommand("monitor"); err != nil {
		t.Fatalf("handleAutomationCommand: %v", err)
	}
	text := output.String()
	for _, want := range []string{"Due automations completed: 1", "Automation Digest", "due=0"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected monitor output to contain %q, got %q", want, text)
		}
	}
	if session.Automations[0].LastRunAt.IsZero() {
		t.Fatalf("expected monitor to run due automation")
	}
}

func TestAutomationNotifyWritesDigestArtifact(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	var output bytes.Buffer
	session := NewSession(root, "provider", "model", "", "default")
	session.Automations = []SessionAutomation{
		normalizeSessionAutomation(SessionAutomation{
			ID:        "auto-due",
			Type:      AutomationTypePRReview,
			Command:   "/review-pr",
			Status:    AutomationStatusActive,
			Schedule:  "every 1h",
			CreatedAt: now.Add(-3 * time.Hour),
			LastRunAt: now.Add(-2 * time.Hour),
			NextRunAt: now.Add(-time.Hour),
		}),
		normalizeSessionAutomation(SessionAutomation{
			ID:         "auto-failed",
			Type:       AutomationTypeRecurringVerification,
			Command:    "/verify",
			Status:     AutomationStatusFailed,
			Schedule:   "manual-recurring",
			LastResult: "verification failed",
		}),
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

	if err := rt.handleAutomationCommand("notify"); err != nil {
		t.Fatalf("handleAutomationCommand: %v", err)
	}
	path := filepath.Join(root, ".kernforge", "automation", "latest_digest.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read automation digest: %v", err)
	}
	text := string(data)
	for _, want := range []string{"# Automation Digest", "auto-due", "auto-failed", "verification failed", "due=yes"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected digest to contain %q, got %q", want, text)
		}
	}
	last := session.ConversationEvents[len(session.ConversationEvents)-1]
	if last.Kind != conversationEventKindAutomation || len(last.ArtifactRefs) != 1 || last.ArtifactRefs[0] != path {
		t.Fatalf("expected automation digest event, got %#v", last)
	}
	if !strings.Contains(output.String(), "Generated automation digest artifact") {
		t.Fatalf("expected digest output, got %q", output.String())
	}
}

func TestAutomationNotifyPostsWebhook(t *testing.T) {
	root := t.TempDir()
	received := make(chan automationWebhookPayload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("expected json content type, got %q", got)
		}
		var payload automationWebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		received <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	var output bytes.Buffer
	now := time.Now().UTC()
	session := NewSession(root, "provider", "model", "", "default")
	session.Automations = []SessionAutomation{
		normalizeSessionAutomation(SessionAutomation{
			ID:         "auto-failed",
			Type:       AutomationTypeRecurringVerification,
			Command:    "/verify",
			Status:     AutomationStatusFailed,
			Schedule:   "manual-recurring",
			LastResult: "verification failed",
			CreatedAt:  now,
		}),
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

	if err := rt.handleAutomationCommand("notify --no-file --webhook-url " + server.URL + "?token=secret"); err != nil {
		t.Fatalf("handleAutomationCommand: %v", err)
	}
	var payload automationWebhookPayload
	select {
	case payload = <-received:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected webhook payload")
	}
	if !strings.Contains(payload.Summary, "failed=1") || !strings.Contains(payload.Markdown, "auto-failed") {
		t.Fatalf("unexpected webhook payload: %#v", payload)
	}
	last := session.ConversationEvents[len(session.ConversationEvents)-1]
	if last.Kind != conversationEventKindAutomation || last.Summary != "automation digest webhook sent" {
		t.Fatalf("expected webhook event, got %#v", last)
	}
	if strings.Contains(last.Entities["webhook"], "token=secret") {
		t.Fatalf("expected webhook URL to be redacted, got %#v", last.Entities)
	}
	if !strings.Contains(output.String(), "Sent automation digest webhook") {
		t.Fatalf("expected webhook output, got %q", output.String())
	}
}

func TestAutomationWatchRunsDueAndWritesDigestArtifact(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	var output bytes.Buffer
	session := NewSession(root, "provider", "model", "", "default")
	session.Automations = []SessionAutomation{
		normalizeSessionAutomation(SessionAutomation{
			ID:        "auto-due",
			Type:      AutomationTypePRReview,
			Command:   "/review-pr",
			Status:    AutomationStatusActive,
			Schedule:  "every 1h",
			CreatedAt: now.Add(-3 * time.Hour),
			LastRunAt: now.Add(-2 * time.Hour),
			NextRunAt: now.Add(-time.Hour),
		}),
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

	if err := rt.handleAutomationCommand("watch --cycles 1 --notify"); err != nil {
		t.Fatalf("handleAutomationCommand: %v", err)
	}
	if session.Automations[0].LastRunAt.IsZero() {
		t.Fatalf("expected watch to run due automation")
	}
	path := filepath.Join(root, ".kernforge", "automation", "latest_digest.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read automation digest: %v", err)
	}
	if !strings.Contains(string(data), "# Automation Digest") || !strings.Contains(string(data), "auto-due") {
		t.Fatalf("unexpected automation digest: %q", string(data))
	}
	text := output.String()
	for _, want := range []string{"Automation watch started", "Automation watch cycle 1", "Automation watch completed: cycles=1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected watch output to contain %q, got %q", want, text)
		}
	}
}

func TestAutomationWatchOptionsParseAndValidate(t *testing.T) {
	options, err := parseAutomationWatchOptions([]string{"--interval", "30s", "--cycles=2", "--notify", "--webhook-url", "http://127.0.0.1/hook"})
	if err != nil {
		t.Fatalf("parseAutomationWatchOptions: %v", err)
	}
	if options.Interval != 30*time.Second || options.Cycles != 2 || !options.Notify || options.WebhookURL != "http://127.0.0.1/hook" {
		t.Fatalf("unexpected watch options: %#v", options)
	}
	if _, err := parseAutomationWatchOptions([]string{"--interval", "500ms"}); err == nil {
		t.Fatalf("expected sub-second interval to be rejected")
	}
	if _, err := parseAutomationWatchOptions([]string{"--cycles", "-1"}); err == nil {
		t.Fatalf("expected negative cycles to be rejected")
	}
}

func TestAutomationDaemonStateStatusAndStop(t *testing.T) {
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
	state := automationDaemonState{
		PID:       99999999,
		StartedAt: time.Now().UTC(),
		Command:   renderAutomationDaemonWatchCommand(automationWatchOptions{Interval: 30 * time.Second, Notify: true, WebhookURL: "https://example.invalid/hook"}),
		LogPath:   filepath.Join(root, ".kernforge", "automation", "daemon.log"),
	}
	if err := writeAutomationDaemonState(root, state); err != nil {
		t.Fatalf("writeAutomationDaemonState: %v", err)
	}
	if !strings.Contains(state.Command, "/automation watch --interval 30s --notify --webhook-url https://example.invalid/hook") {
		t.Fatalf("unexpected daemon command: %q", state.Command)
	}
	if err := rt.handleAutomationCommand("daemon-status"); err != nil {
		t.Fatalf("daemon-status: %v", err)
	}
	if !strings.Contains(output.String(), "Automation daemon stale") {
		t.Fatalf("expected stale status output, got %q", output.String())
	}
	if err := rt.handleAutomationCommand("daemon-stop"); err != nil {
		t.Fatalf("daemon-stop: %v", err)
	}
	if _, ok := readAutomationDaemonState(root); ok {
		t.Fatalf("expected daemon state to be removed")
	}
	if len(session.ConversationEvents) == 0 || session.ConversationEvents[len(session.ConversationEvents)-1].Summary != "automation daemon stopped" {
		t.Fatalf("expected daemon stopped event, got %#v", session.ConversationEvents)
	}
}

func TestAutomationNotificationOptionsParseAndValidate(t *testing.T) {
	options, err := parseAutomationNotificationOptions([]string{"--no-file", "--webhook=https://example.invalid/hook?token=secret"}, true)
	if err != nil {
		t.Fatalf("parseAutomationNotificationOptions: %v", err)
	}
	if options.WriteDigest || options.WebhookURL != "https://example.invalid/hook?token=secret" {
		t.Fatalf("unexpected notification options: %#v", options)
	}
	if _, err := parseAutomationNotificationOptions([]string{"--webhook-url"}, false); err == nil {
		t.Fatalf("expected missing webhook URL to be rejected")
	}
	if got := redactAutomationWebhookURL("https://user:pass@example.invalid/hook?token=secret#frag"); strings.Contains(got, "token=secret") || strings.Contains(got, "user:pass") || strings.Contains(got, "frag") {
		t.Fatalf("expected redacted webhook URL, got %q", got)
	}
}
