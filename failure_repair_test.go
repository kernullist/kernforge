package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFailureRepairAttemptRecordsFailingStep(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}
	report := VerificationReport{
		Trigger:      "automatic",
		GeneratedAt:  time.Now(),
		ChangedPaths: []string{"main.go"},
		Steps: []VerificationStep{{
			Label:       "go test ./...",
			Command:     "go test ./...",
			Status:      VerificationFailed,
			FailureKind: "test_failure",
			Hint:        "Inspect the failing test.",
			Output:      "ok pkg/a\n--- FAIL: TestBroken\n    main_test.go:12: expected 1 got 0\nFAIL\n",
		}},
	}

	agent.noteVerificationResult(report)

	if session.ActiveFailureRepair == nil {
		t.Fatalf("expected active failure repair attempt")
	}
	attempt := session.ActiveFailureRepair
	if attempt.Command != "go test ./..." || attempt.FailureKind != "test_failure" {
		t.Fatalf("unexpected repair attempt: %#v", attempt)
	}
	if !strings.Contains(attempt.FirstError, "FAIL") {
		t.Fatalf("expected first meaningful failure line, got %q", attempt.FirstError)
	}
	if len(attempt.NextSteps) == 0 || !strings.Contains(strings.Join(attempt.NextSteps, " "), "main.go") {
		t.Fatalf("expected changed-path next step, got %#v", attempt.NextSteps)
	}
}

func TestFailureRepairPromptIsAddedAfterVerificationFailure(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "main.go",
				"content": "package main\n",
			}),
			{Message: Message{Role: "assistant", Text: "I inspected the failure and cannot fix it yet."}},
			{Message: Message{Role: "assistant", Text: "Verification is still failing because the test fixture is broken."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			return VerificationReport{
				Trigger:      "automatic",
				ChangedPaths: []string{"main.go"},
				Steps: []VerificationStep{{
					Label:       "go test ./...",
					Command:     "go test ./...",
					Status:      VerificationFailed,
					FailureKind: "test_failure",
					Output:      "FAIL: expected 1 got 0",
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "fix the file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "still failing") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected three model requests, got %d", len(provider.requests))
	}
	last := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Text, "Failure repair harness") {
		t.Fatalf("expected failure repair harness prompt, got %#v", last)
	}
	if session.ActiveFailureRepair == nil || len(session.FailureRepairAttempts) == 0 {
		t.Fatalf("expected persisted failure repair attempt, got active=%#v attempts=%#v", session.ActiveFailureRepair, session.FailureRepairAttempts)
	}
}

func TestFailureRepairAttemptResolvesAfterPassingVerification(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}
	failing := VerificationReport{
		Trigger: "automatic",
		Steps: []VerificationStep{{
			Label:   "go test ./...",
			Command: "go test ./...",
			Status:  VerificationFailed,
			Output:  "FAIL: broken",
		}},
	}
	passing := VerificationReport{
		Trigger: "automatic",
		Steps: []VerificationStep{{
			Label:   "go test ./...",
			Command: "go test ./...",
			Status:  VerificationPassed,
			Output:  "ok",
		}},
	}

	agent.noteVerificationResult(failing)
	if session.ActiveFailureRepair == nil {
		t.Fatalf("expected active repair after failure")
	}
	agent.noteVerificationResult(passing)
	if session.ActiveFailureRepair != nil {
		t.Fatalf("expected active repair to resolve, got %#v", session.ActiveFailureRepair)
	}
	if len(session.FailureRepairAttempts) == 0 || session.FailureRepairAttempts[0].Status != "resolved" {
		t.Fatalf("expected resolved repair history, got %#v", session.FailureRepairAttempts)
	}
}
