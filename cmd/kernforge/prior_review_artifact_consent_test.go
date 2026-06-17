package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writePriorReviewArtifact(t *testing.T, root, rel string, mtime time.Time) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(abs, []byte("# stale review\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chtimes(abs, mtime, mtime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	return abs
}

func TestPriorSessionReviewArtifactDetection(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")

	// Artifact written before the session started -> prior session.
	writePriorReviewArtifact(t, root, ".kernforge/reviews/review-old/review.md", session.CreatedAt.Add(-time.Hour))
	// Artifact written after the session started -> current session.
	writePriorReviewArtifact(t, root, ".kernforge/reviews/review-new/review.md", session.CreatedAt.Add(time.Hour))

	priorCall := []ToolCall{{Name: "read_file", Arguments: `{"path":".kernforge/reviews/review-old/review.md"}`}}
	if got := priorSessionReviewArtifactReadPaths(root, session, priorCall); len(got) != 1 {
		t.Fatalf("expected prior-session artifact to be gated, got %#v", got)
	}

	currentCall := []ToolCall{{Name: "read_file", Arguments: `{"path":".kernforge/reviews/review-new/review.md"}`}}
	if got := priorSessionReviewArtifactReadPaths(root, session, currentCall); len(got) != 0 {
		t.Fatalf("current-session artifact must not be gated, got %#v", got)
	}

	sourceCall := []ToolCall{{Name: "read_file", Arguments: `{"path":"app.py"}`}}
	if got := priorSessionReviewArtifactReadPaths(root, session, sourceCall); len(got) != 0 {
		t.Fatalf("non-review path must not be gated, got %#v", got)
	}
}

// TestPriorReviewArtifactConsentNonInteractiveSkips confirms the lazy first-read
// consent gate defaults to skipping prior-session review artifacts when there is
// no interactive prompt: the read is not executed, the model is steered back to
// the workspace, and the decision is remembered for the session.
func TestPriorReviewArtifactConsentNonInteractiveSkips(t *testing.T) {
	root := t.TempDir()
	ws := Workspace{BaseRoot: root, Root: root}
	session := NewSession(root, "scripted", "model", "", "default")
	writePriorReviewArtifact(t, root, ".kernforge/reviews/review-old/review.md", session.CreatedAt.Add(-time.Hour))

	provider := &scriptedProviderClient{replies: []ChatResponse{
		toolCallResponse("read_file", map[string]any{"path": ".kernforge/reviews/review-old/review.md"}),
		{Message: Message{Role: "assistant", Text: "done"}},
	}}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	reply, err := agent.Reply(context.Background(), ".env에 gitlab 토큰을 넣어두고 사용하게 하자")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if session.PriorReviewArtifactConsent != priorReviewArtifactConsentSkip {
		t.Fatalf("expected consent=skip, got %q", session.PriorReviewArtifactConsent)
	}
	if reply == "" {
		t.Fatalf("expected a final reply after the gated read was skipped")
	}
	foundGuidance := false
	for _, m := range session.Messages {
		if m.Role == "user" && strings.Contains(m.Text, ".kernforge/reviews") && strings.Contains(m.Text, "다시 읽지") {
			foundGuidance = true
		}
	}
	if !foundGuidance {
		// English locale fallback check.
		for _, m := range session.Messages {
			if m.Role == "user" && strings.Contains(m.Text, "prior-session review artifacts") {
				foundGuidance = true
			}
		}
	}
	if !foundGuidance {
		t.Fatalf("expected skip guidance injected for the model")
	}
}

// TestPriorReviewArtifactConsentUseAllows confirms that when the user consents,
// the prior-session artifact read is allowed (the gate does not block it) and the
// decision is remembered.
func TestPriorReviewArtifactConsentUseAllows(t *testing.T) {
	root := t.TempDir()
	ws := Workspace{BaseRoot: root, Root: root}
	session := NewSession(root, "scripted", "model", "", "default")
	writePriorReviewArtifact(t, root, ".kernforge/reviews/review-old/review.md", session.CreatedAt.Add(-time.Hour))

	provider := &scriptedProviderClient{replies: []ChatResponse{
		toolCallResponse("read_file", map[string]any{"path": ".kernforge/reviews/review-old/review.md"}),
		{Message: Message{Role: "assistant", Text: "done"}},
	}}
	asked := 0
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
		PromptUsePriorReviewArtifacts: func(paths []string) (bool, error) {
			asked++
			return true, nil
		},
	}

	if _, err := agent.Reply(context.Background(), "이전 리뷰 참고해서 고쳐줘"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if asked != 1 {
		t.Fatalf("expected exactly one consent prompt, got %d", asked)
	}
	if session.PriorReviewArtifactConsent != priorReviewArtifactConsentUse {
		t.Fatalf("expected consent=use, got %q", session.PriorReviewArtifactConsent)
	}
}
