package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type scriptedProviderClient struct {
	replies  []ChatResponse
	requests []ChatRequest
	index    int
}

func (s *scriptedProviderClient) Name() string { return "scripted" }

func (s *scriptedProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	s.requests = append(s.requests, req)
	if s.index >= len(s.replies) {
		return ChatResponse{Message: Message{Role: "assistant", Text: "done"}}, nil
	}
	resp := s.replies[s.index]
	s.index++
	return resp, nil
}

type blockingProviderClient struct {
	calls   int
	started chan struct{}
}

func (b *blockingProviderClient) Name() string { return "blocking" }

func (b *blockingProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = req
	b.calls++
	if b.started != nil {
		select {
		case <-b.started:
		default:
			close(b.started)
		}
	}
	<-ctx.Done()
	return ChatResponse{}, ctx.Err()
}

type timeoutThenSuccessProviderClient struct {
	calls int
}

func (p *timeoutThenSuccessProviderClient) Name() string { return "timeout-then-success" }

func (p *timeoutThenSuccessProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = req
	p.calls++
	if p.calls == 1 {
		<-ctx.Done()
		return ChatResponse{}, ctx.Err()
	}
	return ChatResponse{
		Message: Message{
			Role: "assistant",
			Text: "recovered",
		},
		StopReason: "stop",
	}, nil
}

type timeoutProviderClient struct {
	calls int
}

func (p *timeoutProviderClient) Name() string { return "timeout" }

func (p *timeoutProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = req
	p.calls++
	<-ctx.Done()
	return ChatResponse{}, ctx.Err()
}

type cancelDuringToolTool struct {
	cancel func()
	calls  int
}

func (t *cancelDuringToolTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "cancel_during_tool",
		Description: "Cancel the active request while simulating a completed tool call.",
		InputSchema: map[string]any{
			"type": "object",
		},
	}
}

func (t *cancelDuringToolTool) Execute(ctx context.Context, input any) (string, error) {
	_ = ctx
	_ = input
	t.calls++
	if t.cancel != nil {
		t.cancel()
	}
	return "tool finished after cancel", nil
}

type failingTool struct {
	name  string
	err   error
	calls int
}

func (t *failingTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        t.name,
		Description: "Fail with a scripted error for retry-loop tests.",
		InputSchema: map[string]any{
			"type": "object",
		},
	}
}

func (t *failingTool) Execute(ctx context.Context, input any) (string, error) {
	_ = ctx
	_ = input
	t.calls++
	return "", t.err
}

func toolCallResponse(name string, args map[string]any) ChatResponse {
	data, _ := json.Marshal(args)
	return ChatResponse{
		Message: Message{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      name,
				Arguments: string(data),
			}},
		},
	}
}

func TestAgentVerificationFailurePromptsAnotherTurnBeforeFinalAnswer(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{Message: Message{Role: "assistant", Text: "I made the change."}},
			{Message: Message{Role: "assistant", Text: "Verification is still failing because the tests are already broken upstream."}},
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
				Steps: []VerificationStep{{
					Label:  "go test ./...",
					Status: VerificationFailed,
					Output: "failing test",
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "fix the file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Verification is still failing because the tests are already broken upstream." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected 3 model turns due to verification gating, got %d", len(provider.requests))
	}
	if len(provider.requests[1].Messages) == 0 || !strings.Contains(provider.requests[1].Messages[len(provider.requests[1].Messages)-1].Text, "Likely failure summary") {
		t.Fatalf("expected failure hint to be included before retry, got %#v", provider.requests[1].Messages)
	}
	if !strings.Contains(provider.requests[1].Messages[len(provider.requests[1].Messages)-1].Text, "Suggested repair strategy") {
		t.Fatalf("expected repair strategy in retry prompt, got %#v", provider.requests[1].Messages)
	}
}

func TestAgentCanRepairAfterFailedVerificationAndReturnAfterPass(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {}\n"}),
			{Message: Message{Role: "assistant", Text: "Implemented the fix and verification now passes."}},
		},
	}
	verifyCount := 0
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
			verifyCount++
			if verifyCount == 1 {
				return VerificationReport{
					Steps: []VerificationStep{{
						Label:  "go test ./...",
						Status: VerificationFailed,
						Output: "failing test",
					}},
				}, true
			}
			return VerificationReport{
				Steps: []VerificationStep{{
					Label:  "go test ./...",
					Status: VerificationPassed,
					Output: "ok",
				}},
			}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "fix and verify")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Implemented the fix and verification now passes." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if verifyCount != 2 {
		t.Fatalf("expected verifier to run twice, got %d", verifyCount)
	}
	if session.LastVerification == nil || session.LastVerification.HasFailures() {
		t.Fatalf("expected final verification report to be passing, got %#v", session.LastVerification)
	}
}

func TestAgentSkipsAutomaticVerificationForDocsOnlyChangesByDefault(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "notes.md", "content": "# notes\n"}),
			{Message: Message{Role: "assistant", Text: "Wrote the document."}},
		},
	}
	verifyCount := 0
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			verifyCount++
			return VerificationReport{Steps: []VerificationStep{{Label: "verify", Status: VerificationPassed}}}, true
		},
	}

	reply, err := agent.Reply(context.Background(), "write a document")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Wrote the document." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if verifyCount != 1 {
		t.Fatalf("expected docs-only change to run automatic verification, got %d runs", verifyCount)
	}
}

func TestAgentSkipsAutomaticVerificationWhenDisabled(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{Message: Message{Role: "assistant", Text: "Updated the file."}},
		},
	}
	verifyCount := 0
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	cfg := DefaultConfig(root)
	cfg.AutoVerify = boolPtr(false)
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			verifyCount++
			return VerificationReport{Steps: []VerificationStep{{Label: "verify", Status: VerificationPassed}}}, true
		},
	}

	if _, err := agent.Reply(context.Background(), "update the file"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if verifyCount != 0 {
		t.Fatalf("expected automatic verification to be disabled, got %d runs", verifyCount)
	}
}

func TestAgentPromptsToDisableAutoVerifyOnFirstMissingToolFailure(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			{Message: Message{Role: "assistant", Text: "Implemented the change, but verification was disabled because the local build toolchain is unavailable."}},
		},
	}
	verifyCount := 0
	promptCount := 0
	cfg := DefaultConfig(root)
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		VerifyChanges: func(ctx context.Context) (VerificationReport, bool) {
			_ = ctx
			verifyCount++
			return VerificationReport{
				Steps: []VerificationStep{{
					Label:       "msbuild demo.sln",
					Command:     "msbuild demo.sln /m",
					Status:      VerificationFailed,
					FailureKind: "command_not_found",
					Hint:        "A required verification tool could not be started.",
					Output:      "msbuild : The term 'msbuild' is not recognized as the name of a cmdlet, function, script file, or executable program.",
				}},
			}, true
		},
	}
	agent.PromptResolveAutoVerifyFailure = func(report VerificationReport) (AutoVerifyFailureResolution, error) {
		promptCount++
		if !report.HasCommandMissingFailure() {
			t.Fatalf("expected command-missing failure report")
		}
		agent.Config.AutoVerify = boolPtr(false)
		return AutoVerifyFailureDisable, nil
	}

	reply, err := agent.Reply(context.Background(), "make the change")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "verification was disabled") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if verifyCount != 1 {
		t.Fatalf("expected first missing-tool verification failure to trigger disable prompt, got %d verification runs", verifyCount)
	}
	if promptCount != 1 {
		t.Fatalf("expected one disable prompt, got %d", promptCount)
	}
	if configAutoVerify(agent.Config) {
		t.Fatalf("expected auto_verify to be disabled after prompt")
	}
}

func TestAgentNudgesForFinalAnswerAfterMultipleSuccessfulEditTurns(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {}\n"}),
			{Message: Message{Role: "assistant", Text: "Implemented the requested change."}},
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
	}

	reply, err := agent.Reply(context.Background(), "make the change")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Implemented the requested change." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected 3 model turns, got %d", len(provider.requests))
	}
	lastTurn := provider.requests[2]
	if len(lastTurn.Messages) == 0 {
		t.Fatalf("expected follow-up nudge before final answer")
	}
	lastMessage := lastTurn.Messages[len(lastTurn.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "multiple edit rounds") {
		t.Fatalf("expected final-answer nudge after repeated edits, got %#v", lastMessage)
	}
}

func TestAgentBlocksFurtherEditToolLoopAfterPostEditNudge(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n"}),
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {}\n"}),
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {}\n// extra\n"}),
			{Message: Message{Role: "assistant", Text: "Implemented the requested change."}},
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
	}

	reply, err := agent.Reply(context.Background(), "make the change")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Implemented the requested change." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected 4 model turns, got %d", len(provider.requests))
	}
	thirdTurn := provider.requests[3]
	if len(thirdTurn.Messages) == 0 {
		t.Fatalf("expected stronger stop-editing nudge before final answer")
	}
	lastMessage := thirdTurn.Messages[len(thirdTurn.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Do not call more edit tools") {
		t.Fatalf("expected stop-editing nudge after repeated edit-tool attempt, got %#v", lastMessage)
	}
}

func TestAgentSuppressesDuplicateToolPreambleEmitsWithinATurn(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "Checking the workspace.",
					ToolCalls: []ToolCall{{
						ID:        "call-1",
						Name:      "list_files",
						Arguments: `{}`,
					}},
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Checking the workspace.",
					ToolCalls: []ToolCall{{
						ID:        "call-2",
						Name:      "list_files",
						Arguments: `{}`,
					}},
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Done.",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var emitted []string
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		EmitAssistant: func(text string) {
			emitted = append(emitted, text)
		},
	}

	reply, err := agent.Reply(context.Background(), "inspect the workspace")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Done." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if !reflect.DeepEqual(emitted, []string{"Checking the workspace."}) {
		t.Fatalf("expected duplicate preamble emit to be suppressed, got %#v", emitted)
	}
}

func TestAgentNudgesAfterRepeatedIdenticalToolCalls(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{}),
			toolCallResponse("list_files", map[string]any{}),
			toolCallResponse("list_files", map[string]any{}),
			{Message: Message{Role: "assistant", Text: "I kept seeing the same workspace state, so I am stopping here."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect the workspace")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "I kept seeing the same workspace state, so I am stopping here." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected 4 model turns, got %d", len(provider.requests))
	}
	lastTurn := provider.requests[3]
	if len(lastTurn.Messages) == 0 {
		t.Fatalf("expected repeated-tool warning before final answer")
	}
	lastMessage := lastTurn.Messages[len(lastTurn.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "repeating the same tool call sequence") {
		t.Fatalf("expected repeated-tool nudge, got %#v", lastMessage)
	}
}

func TestAgentStopsAfterRepeatedIdenticalToolCallsContinueAfterNudge(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{}),
			toolCallResponse("list_files", map[string]any{}),
			toolCallResponse("list_files", map[string]any{}),
			toolCallResponse("list_files", map[string]any{}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "inspect the workspace")
	if err == nil {
		t.Fatalf("expected repeated identical tool calls to stop the loop")
	}
	if !strings.Contains(err.Error(), "repeated identical tool calls") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSanitizeAssistantMessageTextRemovesToolPreambleNarration(t *testing.T) {
	text := "Let me read AnthropicProvider and GeminiProvider:Now I have all the files. Let me apply all the changes."

	got := sanitizeAssistantMessageText(text, true)
	if got != "" {
		t.Fatalf("expected pure tool preamble narration to be dropped, got %q", got)
	}
}

func TestSanitizeAssistantMessageTextKeepsSubstantiveToolPlan(t *testing.T) {
	text := "Let me inspect the providers.\nThe approach:\n1. Update the interface\n2. Pass reasoning effort through all providers"

	got := sanitizeAssistantMessageText(text, true)
	if !strings.Contains(got, "The approach:") {
		t.Fatalf("expected substantive content to be kept, got %q", got)
	}
	if strings.Contains(strings.ToLower(got), "let me inspect") {
		t.Fatalf("expected narration preamble to be removed, got %q", got)
	}
}

func TestAgentStoresSanitizedToolPreambleInsteadOfNarration(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "Let me read AnthropicProvider and GeminiProvider:Now I have all the files. Let me apply all the changes.",
					ToolCalls: []ToolCall{{
						ID:        "call-1",
						Name:      "list_files",
						Arguments: `{}`,
					}},
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "Done.",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect the workspace")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Done." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(session.Messages) < 3 {
		t.Fatalf("expected stored session messages, got %#v", session.Messages)
	}
	assistantTurn := session.Messages[1]
	if assistantTurn.Role != "assistant" {
		t.Fatalf("expected assistant tool turn, got %#v", assistantTurn)
	}
	if strings.TrimSpace(assistantTurn.Text) != "" {
		t.Fatalf("expected tool-turn narration to be stripped from stored message, got %q", assistantTurn.Text)
	}
}

func TestAgentReportsTokenLimitWhenModelStopsWithEmptyResponse(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message:    Message{Role: "assistant"},
				StopReason: "length",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "inspect the workspace")
	if err == nil {
		t.Fatalf("expected token limit error")
	}
	if !strings.Contains(err.Error(), "token limit") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "stop_reason=length") {
		t.Fatalf("expected stop_reason in error, got %v", err)
	}
}

func TestAgentToolLoopLimitIncludesLastToolSummaryAndStopReason(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-1",
						Name:      "list_files",
						Arguments: `{}`,
					}},
				},
				StopReason: "tool_calls",
			},
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-2",
						Name:      "list_files",
						Arguments: `{"path":"."}`,
					}},
				},
				StopReason: "tool_calls",
			},
		},
	}
	cfg := Config{MaxToolIterations: 2}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "inspect the workspace")
	if err == nil {
		t.Fatalf("expected tool loop limit error")
	}
	if !strings.Contains(err.Error(), "tool loop limit exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "last_tools=list_files") {
		t.Fatalf("expected last tool summary, got %v", err)
	}
	if !strings.Contains(err.Error(), "stop_reason=tool_calls") {
		t.Fatalf("expected stop reason, got %v", err)
	}
	if !strings.Contains(err.Error(), "iteration=2") {
		t.Fatalf("expected iteration count, got %v", err)
	}
	if !strings.Contains(err.Error(), "max_iterations=2") {
		t.Fatalf("expected max iteration count, got %v", err)
	}
	if !strings.Contains(err.Error(), "recent_turns=") {
		t.Fatalf("expected recent tool turns summary, got %v", err)
	}
}

func TestAgentPromptsRereadAfterEditTargetMismatch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "completion.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("replace_in_file", map[string]any{
				"path":    "completion.go",
				"search":  "missing",
				"replace": "found",
			}),
			{Message: Message{Role: "assistant", Text: "I need to re-read the file before editing it."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReplaceInFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update completion.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "I need to re-read the file before editing it." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected a follow-up turn after edit target mismatch, got %d", len(provider.requests))
	}
	lastTurn := provider.requests[1]
	if len(lastTurn.Messages) == 0 {
		t.Fatalf("expected reread guidance before second turn")
	}
	lastMessage := lastTurn.Messages[len(lastTurn.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "First read the exact file again from the same path") {
		t.Fatalf("expected reread guidance, got %#v", lastMessage)
	}
}

func TestAgentStopsBeforeNextModelTurnWhenContextCanceledDuringTool(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("cancel_during_tool", map[string]any{}),
			{Message: Message{Role: "assistant", Text: "this should never be requested"}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	ctx, cancel := context.WithCancel(context.Background())
	tool := &cancelDuringToolTool{
		cancel: cancel,
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(tool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(ctx, "cancel during tool execution")
	if err == nil {
		t.Fatalf("expected cancellation error")
	}
	if err != context.Canceled {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if tool.calls != 1 {
		t.Fatalf("expected tool to run once, got %d", tool.calls)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected no follow-up model turn after cancellation, got %d requests", len(provider.requests))
	}
}

func TestAgentReturnsPromptlyWhenContextCanceledDuringModelTurn(t *testing.T) {
	root := t.TempDir()
	provider := &blockingProviderClient{started: make(chan struct{})}
	session := NewSession(root, "blocking", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	ctx, cancel := context.WithCancel(context.Background())
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	done := make(chan error, 1)
	go func() {
		_, err := agent.Reply(ctx, "cancel during model execution")
		done <- err
	}()

	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("provider did not start model turn")
	}

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("agent did not return promptly after model cancellation")
	}

	if provider.calls != 1 {
		t.Fatalf("expected one provider call, got %d", provider.calls)
	}
}

func TestAgentNudgesAfterMalformedWriteFileArguments(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-1",
						Name:      "write_file",
						Arguments: `{"path":"main.go","content":"package main`,
					}},
				},
			},
			{Message: Message{Role: "assistant", Text: "I retried with apply_patch after the malformed write_file call."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws), NewApplyPatchTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "update the file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "I retried with apply_patch after the malformed write_file call." {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected follow-up turn after malformed tool arguments, got %d", len(provider.requests))
	}
	lastTurn := provider.requests[1]
	if len(lastTurn.Messages) == 0 {
		t.Fatalf("expected guidance before second turn")
	}
	lastMessage := lastTurn.Messages[len(lastTurn.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "use apply_patch instead of write_file") {
		t.Fatalf("expected apply_patch recovery guidance, got %#v", lastMessage)
	}
	for _, tool := range lastTurn.Tools {
		if tool.Name == "write_file" {
			t.Fatalf("expected write_file to be disabled after malformed arguments")
		}
	}
}

func TestCompleteModelTurnRetriesOnceOnTimeout(t *testing.T) {
	provider := &timeoutThenSuccessProviderClient{}
	var progress []string
	agent := &Agent{
		Config: Config{
			RequestTimeoutSecs: 1,
		},
		Client: provider,
		EmitProgress: func(text string) {
			progress = append(progress, text)
		},
	}

	resp, err := agent.completeModelTurn(context.Background(), ChatRequest{
		Model: "test-model",
	})
	if err != nil {
		t.Fatalf("completeModelTurn: %v", err)
	}
	if resp.Message.Text != "recovered" {
		t.Fatalf("unexpected response text: %q", resp.Message.Text)
	}
	if provider.calls != 2 {
		t.Fatalf("expected two provider attempts, got %d", provider.calls)
	}
	if len(progress) == 0 || !strings.Contains(progress[0], "Retrying once") {
		t.Fatalf("expected retry progress message, got %#v", progress)
	}
}

func TestCompleteModelTurnReturnsTimeoutAfterRetryExhausted(t *testing.T) {
	provider := &timeoutProviderClient{}
	agent := &Agent{
		Config: Config{
			RequestTimeoutSecs: 1,
		},
		Client: provider,
	}

	_, err := agent.completeModelTurn(context.Background(), ChatRequest{
		Model: "test-model",
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded after retry exhaustion, got %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("expected two provider attempts, got %d", provider.calls)
	}
}

func TestCompleteModelTurnDoesNotRetryOnUserCancellation(t *testing.T) {
	provider := &blockingProviderClient{started: make(chan struct{})}
	agent := &Agent{
		Config: Config{
			RequestTimeoutSecs: 1,
		},
		Client: provider,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := agent.completeModelTurn(ctx, ChatRequest{
			Model: "test-model",
		})
		done <- err
	}()

	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("provider did not start model turn")
	}

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("completeModelTurn did not return after cancellation")
	}

	if provider.calls != 1 {
		t.Fatalf("expected one provider attempt on cancellation, got %d", provider.calls)
	}
}

func TestAgentNudgesBeforeAbortingRepeatedToolFailure(t *testing.T) {
	root := t.TempDir()
	failTool := &failingTool{
		name: "failing_tool",
		err:  fmt.Errorf("preview surface busy"),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("failing_tool", map[string]any{}),
			toolCallResponse("failing_tool", map[string]any{}),
			{Message: Message{Role: "assistant", Text: "I could not use the preview surface, so I am stopping with guidance instead."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(failTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "try the preview flow")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "stopping with guidance") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected third turn after repeated failure nudge, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[2]
	if len(lastRequest.Messages) == 0 {
		t.Fatalf("expected guidance message before third turn")
	}
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "The same tool failure repeated") {
		t.Fatalf("expected repeated failure guidance, got %#v", lastMessage)
	}
}

func TestAgentAbortsAfterThirdRepeatedToolFailure(t *testing.T) {
	root := t.TempDir()
	failTool := &failingTool{
		name: "failing_tool",
		err:  fmt.Errorf("preview surface busy"),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("failing_tool", map[string]any{"attempt": 1}),
			toolCallResponse("failing_tool", map[string]any{"attempt": 2}),
			toolCallResponse("failing_tool", map[string]any{"attempt": 3}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(failTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "try the preview flow")
	if err == nil || !strings.Contains(err.Error(), "stopped after repeated tool failure") {
		t.Fatalf("expected repeated tool failure error, got %v", err)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected abort on third failing turn, got %d requests", len(provider.requests))
	}
}

func TestAgentDoesNotLabelSingleFinalToolFailureAsRepeated(t *testing.T) {
	root := t.TempDir()
	failTool := &failingTool{
		name: "failing_tool",
		err:  fmt.Errorf("preview surface busy"),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("failing_tool", map[string]any{}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			MaxToolIterations: 1,
		},
		Client:    provider,
		Tools:     NewToolRegistry(failTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "try the preview flow")
	if err == nil {
		t.Fatalf("expected tool loop error")
	}
	if strings.Contains(err.Error(), "stopped after repeated tool failure") {
		t.Fatalf("single final failure should not be labeled repeated: %v", err)
	}
	if !strings.Contains(err.Error(), "tool loop limit exceeded") {
		t.Fatalf("expected tool loop limit error, got %v", err)
	}
}
