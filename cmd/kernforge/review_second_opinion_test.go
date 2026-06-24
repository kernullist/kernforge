package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// secondOpinionReviewResponse is a scripted REVIEW_RESULT with one concrete
// finding so the parser yields a structured ReviewFinding.
func secondOpinionReviewResponse(summary string, title string) ChatResponse {
	return ChatResponse{
		Message: Message{Role: "assistant", Text: strings.Join([]string{
			"REVIEW_RESULT",
			"verdict: needs_revision",
			"summary: " + summary,
			"findings:",
			"- severity: high",
			"  category: correctness",
			"  path: main.cpp",
			"  title: " + title,
			"  evidence: the supplied snippet returns without the requested guard",
			"  impact: callers observe the wrong result",
			"  required_fix: add the missing guard",
		}, "\n")},
	}
}

func newSecondOpinionTestAgent(t *testing.T, working ProviderClient, reviewer ProviderClient, reviewerModel string) *Agent {
	t.Helper()
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "scripted", "main-model", "", "default")
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(false)
	agent := &Agent{
		Config:         cfg,
		Client:         working,
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          store,
		ReviewerClient: reviewer,
		ReviewerModel:  reviewerModel,
	}
	return agent
}

func runSecondOpinion(t *testing.T, agent *Agent, args map[string]any) reviewSecondOpinionResponse {
	t.Helper()
	ws := Workspace{}
	if agent != nil {
		ws = agent.Workspace
		ws.ResolveAgent = func() *Agent { return agent }
	}
	tool := NewReviewSecondOpinionTool(ws)
	result, err := tool.ExecuteDetailed(context.Background(), args)
	if err != nil {
		t.Fatalf("review_second_opinion returned error: %v", err)
	}
	var response reviewSecondOpinionResponse
	if jsonErr := json.Unmarshal([]byte(result.DisplayText), &response); jsonErr != nil {
		t.Fatalf("decode second opinion response: %v\nraw=%s", jsonErr, result.DisplayText)
	}
	return response
}

// TestReviewSecondOpinionReturnsStructuredFindings verifies the tool runs the
// configured distinct reviewer route, returns structured findings, and labels
// the result independent.
func TestReviewSecondOpinionReturnsStructuredFindings(t *testing.T) {
	working := &scriptedProviderClient{}
	reviewer := &scriptedProviderClient{replies: []ChatResponse{
		secondOpinionReviewResponse("guest reviewer found an issue", "missing guard before return"),
	}}
	agent := newSecondOpinionTestAgent(t, working, reviewer, "reviewer-model")

	response := runSecondOpinion(t, agent, map[string]any{
		"request":      "Does this return path need a guard?",
		"code_snippet": "int f() { return compute(); }",
		"mode":         "quick",
	})

	if response.Independence != reviewSecondOpinionIndependent {
		t.Fatalf("expected independent second opinion, got %q (response=%#v)", response.Independence, response)
	}
	if response.Role != guestReviewerRole {
		t.Fatalf("expected role %q, got %q", guestReviewerRole, response.Role)
	}
	if len(response.Findings) == 0 {
		t.Fatalf("expected at least one structured finding, got none (response=%#v)", response)
	}
	if !strings.Contains(strings.ToLower(response.Findings[0].Title), "guard") {
		t.Fatalf("expected the scripted finding title, got %q", response.Findings[0].Title)
	}
	if response.Findings[0].ReviewerRole != guestReviewerRole {
		t.Fatalf("expected finding reviewer_role %q, got %q", guestReviewerRole, response.Findings[0].ReviewerRole)
	}
	if len(reviewer.requests) == 0 {
		t.Fatalf("expected the reviewer client to be called")
	}
	if len(working.requests) != 0 {
		t.Fatalf("independent second opinion must not call the working client, got %d call(s)", len(working.requests))
	}
}

// TestReviewSecondOpinionUsesGuestReviewerRole verifies the bounded pass runs as
// guest_reviewer, distinct from the primary review pass, so it can never be
// confused with the gate's own primary/cross passes.
func TestReviewSecondOpinionUsesGuestReviewerRole(t *testing.T) {
	working := &scriptedProviderClient{}
	reviewer := &scriptedProviderClient{replies: []ChatResponse{
		secondOpinionReviewResponse("guest reviewer pass", "issue from guest"),
	}}
	agent := newSecondOpinionTestAgent(t, working, reviewer, "reviewer-model")

	_ = runSecondOpinion(t, agent, map[string]any{
		"request": "Review the change.",
	})

	if len(reviewer.requests) == 0 {
		t.Fatalf("expected reviewer request")
	}
	prompt := reviewer.requests[0].Messages[0].Text
	if !strings.Contains(prompt, "Role: "+guestReviewerRole) {
		t.Fatalf("expected guest_reviewer role in the reviewer prompt, got %q", prompt)
	}
	if strings.Contains(prompt, "Role: primary_reviewer") {
		t.Fatalf("guest reviewer prompt must not be a primary_reviewer pass, got %q", prompt)
	}
}

// TestReviewSecondOpinionSoftGateWithoutReviewer verifies that when no model
// route is available at all the tool degrades to a deterministic-only second
// opinion without crashing or calling any client.
func TestReviewSecondOpinionSoftGateWithoutReviewer(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "", "", "default")
	cfg := DefaultConfig(root)
	cfg.AutoLocale = boolPtr(false)
	// No working client and no reviewer client => no usable route.
	agent := &Agent{
		Config:    cfg,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}

	response := runSecondOpinion(t, agent, map[string]any{
		"request": "Is there a bug here?",
	})

	if response.Independence != reviewSecondOpinionDeterministicOnly {
		t.Fatalf("expected deterministic_only second opinion, got %q (response=%#v)", response.Independence, response)
	}
	if !response.Degraded {
		t.Fatalf("expected degraded=true for deterministic-only second opinion")
	}
	if !strings.Contains(strings.ToLower(response.Summary), "deterministic") {
		t.Fatalf("expected deterministic summary, got %q", response.Summary)
	}
}

// TestReviewSecondOpinionNilAgentDoesNotCrash verifies the tool handles a nil
// resolver / no agent without panicking and returns a deterministic-only,
// non-independent result.
func TestReviewSecondOpinionNilAgentDoesNotCrash(t *testing.T) {
	tool := NewReviewSecondOpinionTool(Workspace{})
	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"request": "review please",
	})
	if err != nil {
		t.Fatalf("nil-agent second opinion must not error, got %v", err)
	}
	var response reviewSecondOpinionResponse
	if jsonErr := json.Unmarshal([]byte(result.DisplayText), &response); jsonErr != nil {
		t.Fatalf("decode response: %v", jsonErr)
	}
	if response.Independence != reviewSecondOpinionDeterministicOnly {
		t.Fatalf("expected deterministic_only without an agent, got %q", response.Independence)
	}
}

// TestReviewSecondOpinionSameModelIsLabeledNotIndependent verifies that when the
// only available route is the working client (configured reviewer resolves to
// the same client + model), the second opinion is labeled same_model, runs as an
// adversarial same-model pass, and warns exactly once per session. It must never
// be presented as independent corroboration.
func TestReviewSecondOpinionSameModelIsLabeledNotIndependent(t *testing.T) {
	// One shared client for both the working route and the configured reviewer,
	// and the reviewer model equals the working model: this is the same-model
	// collision the isolation rule must catch.
	shared := &scriptedProviderClient{replies: []ChatResponse{
		secondOpinionReviewResponse("same-model adversarial pass", "issue from same model"),
		secondOpinionReviewResponse("same-model adversarial pass 2", "another issue"),
	}}
	agent := newSecondOpinionTestAgent(t, shared, shared, "main-model")

	// Count only the same-model independence warning, not the unrelated review
	// progress lines that executeSingleReviewModelRun also emits.
	sameModelWarnings := 0
	agent.EmitProgress = func(text string) {
		if strings.Contains(strings.ToLower(text), "not independent corroboration") {
			sameModelWarnings++
		}
	}

	response := runSecondOpinion(t, agent, map[string]any{
		"request": "Second opinion on this code.",
	})

	if response.Independence != reviewSecondOpinionSameModel {
		t.Fatalf("expected same_model label, got %q (response=%#v)", response.Independence, response)
	}
	if response.Independence == reviewSecondOpinionIndependent {
		t.Fatalf("same-model second opinion must never be labeled independent")
	}
	if !strings.Contains(strings.ToLower(response.Note), "not independent") {
		t.Fatalf("expected the note to state the result is not independent, got %q", response.Note)
	}
	if !agent.Session.SecondOpinionSameModelWarned {
		t.Fatalf("expected the once-per-session same-model warning flag to be set")
	}
	if sameModelWarnings != 1 {
		t.Fatalf("expected exactly one same-model warning, got %d", sameModelWarnings)
	}

	// A second same-model call in the same session must not repeat the warning.
	_ = runSecondOpinion(t, agent, map[string]any{
		"request": "Another second opinion.",
	})
	if sameModelWarnings != 1 {
		t.Fatalf("expected the same-model warning to fire only once per session, got %d", sameModelWarnings)
	}
}

// TestReviewSecondOpinionRegisteredInRegistry verifies buildRegistry exposes the
// tool as a read-only, parallel-safe function tool.
func TestReviewSecondOpinionRegisteredInRegistry(t *testing.T) {
	root := t.TempDir()
	registry := buildRegistry(Workspace{BaseRoot: root, Root: root}, nil, SkillCatalog{})
	tool, ok := registry.tools["review_second_opinion"]
	if !ok {
		t.Fatalf("buildRegistry must register review_second_opinion; have %v", registry.ToolNames())
	}
	if ro, ok := tool.(readOnlyToolCallSupport); !ok || !ro.ReadOnlyToolCall() {
		t.Fatalf("review_second_opinion must report ReadOnlyToolCall true")
	}
	if par, ok := tool.(parallelToolCallSupport); !ok || !par.SupportsParallelToolCalls() {
		t.Fatalf("review_second_opinion must support parallel tool calls")
	}
}
