package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptedProviderClient struct {
	mu       sync.Mutex
	replies  []ChatResponse
	requests []ChatRequest
	index    int
}

func (s *scriptedProviderClient) Name() string { return "scripted" }

func (s *scriptedProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req)
	if s.index >= len(s.replies) {
		return ChatResponse{Message: Message{Role: "assistant", Text: "done"}}, nil
	}
	resp := s.replies[s.index]
	s.index++
	return resp, nil
}

type streamingScriptedProviderClient struct {
	mu       sync.Mutex
	replies  []ChatResponse
	requests []ChatRequest
	index    int
}

func (s *streamingScriptedProviderClient) Name() string { return "streaming-scripted" }

func (s *streamingScriptedProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req)
	if s.index >= len(s.replies) {
		return ChatResponse{Message: Message{Role: "assistant", Text: "done"}}, nil
	}
	resp := s.replies[s.index]
	s.index++
	if req.OnTextDelta != nil && strings.TrimSpace(resp.Message.Text) != "" {
		req.OnTextDelta(resp.Message.Text)
	}
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

type transientErrorThenSuccessProviderClient struct {
	calls     int
	failCount int
	err       error
}

func (p *transientErrorThenSuccessProviderClient) Name() string {
	return "transient-error-then-success"
}

func (p *transientErrorThenSuccessProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	_ = req
	p.calls++
	if p.calls <= p.failCount {
		return ChatResponse{}, p.err
	}
	return ChatResponse{
		Message: Message{
			Role: "assistant",
			Text: "recovered",
		},
		StopReason: "stop",
	}, nil
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

type observingSessionTool struct {
	name        string
	sessionPath string
	output      string
	err         error
	calls       int
	onExecute   func([]byte)
}

type staticTool struct {
	name   string
	output string
	calls  int
}

type toolUnsupportedThenSuccessClient struct {
	calls    int
	requests []ChatRequest
}

func (c *toolUnsupportedThenSuccessClient) Name() string { return "tool-unsupported-then-success" }

func (c *toolUnsupportedThenSuccessClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	c.calls++
	c.requests = append(c.requests, req)
	if c.calls == 1 {
		return ChatResponse{}, fmt.Errorf("openai API error (404 Not Found): No endpoints found that support tool use. Try disabling \"git_status\".")
	}
	return ChatResponse{
		Message: Message{
			Role: "assistant",
			Text: "첨부된 코드 기준으로 즉시 보이는 치명적 버그는 없지만 경계 조건 검토가 필요합니다.",
		},
		StopReason: "stop",
	}, nil
}

type fallbackReplayClient struct {
	calls int
}

func (c *fallbackReplayClient) Name() string { return "fallback-replay" }

func (c *fallbackReplayClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	c.calls++
	return ChatResponse{
		Message: Message{
			Role: "assistant",
			Text: "full fallback answer",
		},
		StopReason: "stop_after_stream_retry",
	}, nil
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

func (t *observingSessionTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        t.name,
		Description: "Observe the persisted session state during tool execution.",
		InputSchema: map[string]any{
			"type": "object",
		},
	}
}

func (t *observingSessionTool) Execute(ctx context.Context, input any) (string, error) {
	_ = ctx
	_ = input
	t.calls++
	if t.onExecute != nil && strings.TrimSpace(t.sessionPath) != "" {
		data, readErr := os.ReadFile(t.sessionPath)
		if readErr == nil {
			t.onExecute(data)
		}
	}
	return t.output, t.err
}

func (t *staticTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        t.name,
		Description: "Return a fixed output for agent loop tests.",
		InputSchema: map[string]any{
			"type": "object",
		},
	}
}

func (t *staticTool) Execute(ctx context.Context, input any) (string, error) {
	_ = ctx
	_ = input
	t.calls++
	return t.output, nil
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

func TestAgentAnalysisOnlyRequestHidesEditTools(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "원인은 ETW 세션 초기화 순서 문제입니다."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewApplyPatchTool(ws), NewWriteFileTool(ws), NewReplaceInFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@Tavern/Common/ETWConsumer.cpp ETWConsumer가 제대로 동작할 수 없는 로그를 수집했는데 원인을 분석해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "원인은") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) == 0 {
		t.Fatalf("expected at least one model request")
	}
	var toolNames []string
	for _, def := range provider.requests[0].Tools {
		toolNames = append(toolNames, def.Name)
	}
	if containsString(toolNames, "apply_patch") || containsString(toolNames, "write_file") || containsString(toolNames, "replace_in_file") {
		t.Fatalf("expected edit tools to be hidden for analysis-only request, got %v", toolNames)
	}
	if !containsString(toolNames, "read_file") {
		t.Fatalf("expected read tools to remain available, got %v", toolNames)
	}
}

func TestAgentRetriesEmptyResponseForAnalysisRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VAllocAnalyzer.cpp"), []byte("int Check()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: ""}},
			{Message: Message{Role: "assistant", Text: "문제점은 아직 확인되지 않았지만 경계 조건 검토가 필요합니다."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewGrepTool(ws), NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@VAllocAnalyzer.cpp 에 버그가 있는지 검토해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "검토") && !strings.Contains(reply, "문제점") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected one retry after empty response, got %d requests", len(provider.requests))
	}
	lastMessages := provider.requests[1].Messages
	if len(lastMessages) == 0 {
		t.Fatalf("expected retry guidance message")
	}
	lastText := lastMessages[len(lastMessages)-1].Text
	if !strings.Contains(lastText, "read-only analysis or review request") {
		t.Fatalf("expected analysis-specific empty-response guidance, got %q", lastText)
	}
	if !strings.Contains(lastText, "read_file") {
		t.Fatalf("expected retry guidance to mention read_file, got %q", lastText)
	}
}

func TestAgentEmptyResponseErrorIncludesProviderModelAndAfterTool(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{"path": "."}),
			{Message: Message{Role: "assistant", Text: ""}, StopReason: "stream_empty_fallback_empty_after_stream_retry"},
			{Message: Message{Role: "assistant", Text: ""}, StopReason: "stream_empty_fallback_empty_after_stream_retry"},
		},
	}
	session := NewSession(root, "openrouter", "openai/gpt-oss-120b", "", "default")
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

	_, err := agent.Reply(context.Background(), "리서치 문서를 파일로 작성해줘")
	if err == nil {
		t.Fatalf("expected empty response failure")
	}
	text := err.Error()
	if !strings.Contains(text, "provider=openrouter") {
		t.Fatalf("expected provider in error, got %q", text)
	}
	if !strings.Contains(text, "model=openai/gpt-oss-120b") {
		t.Fatalf("expected model in error, got %q", text)
	}
	if !strings.Contains(text, "stop_reason=stream_empty_fallback_empty_after_stream_retry") {
		t.Fatalf("expected detailed stop reason in error, got %q", text)
	}
	if !strings.Contains(text, "after_tool=true") {
		t.Fatalf("expected after_tool flag in error, got %q", text)
	}
}

func TestSummarizeToolInvocationReadFileIncludesPathAndRange(t *testing.T) {
	call := ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"Tavern/Common/ETWConsumer.cpp","start_line":10,"end_line":42}`,
	}

	got := summarizeToolInvocation(Config{AutoLocale: boolPtr(false)}, call)
	want := "Using read_file on Tavern/Common/ETWConsumer.cpp:10-42..."
	if got != want {
		t.Fatalf("unexpected summary: got %q want %q", got, want)
	}
}

func TestSummarizeToolInvocationRunShellIncludesCommand(t *testing.T) {
	call := ToolCall{
		Name:      "run_shell",
		Arguments: `{"command":"rg -n \"systemPrompt\" agent.go"}`,
	}

	got := summarizeToolInvocation(Config{AutoLocale: boolPtr(false)}, call)
	if !strings.Contains(got, "Running shell: rg -n") {
		t.Fatalf("unexpected shell summary: %q", got)
	}
}

func TestAgentInjectsLatestProjectAnalysisContextOnFirstTurn(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "used cached analysis"}},
		},
	}
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis dir: %v", err)
	}
	pack := KnowledgePack{
		RunID:          "run-1",
		Goal:           "map TavernWorker architecture",
		Root:           root,
		ProjectSummary: "TavernWorker owns telemetry collection and event triage.",
		Subsystems: []KnowledgeSubsystem{
			{
				Title:            "TavernWorker Runtime",
				Group:            "Forensic Analysis",
				Responsibilities: []string{"Collect telemetry", "Normalize suspicious events"},
				EntryPoints:      []string{"TavernWorker/main.cpp"},
				KeyFiles:         []string{"TavernWorker/main.cpp", "TavernWorker/collector.cpp"},
				Dependencies:     []string{"Common/ipc.hpp"},
				EvidenceFiles:    []string{"TavernWorker/main.cpp"},
			},
		},
	}
	data, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), data, 0o644); err != nil {
		t.Fatalf("write knowledge pack: %v", err)
	}
	corpus := VectorCorpus{
		RunID: "run-1",
		Documents: []VectorCorpusDocument{
			{
				ID:       "subsystem:tavernworker-runtime",
				Kind:     "subsystem",
				Title:    "Forensic Analysis: TavernWorker Runtime",
				Text:     "Startup path initializes telemetry collectors and triage workers.",
				PathHint: "TavernWorker/main.cpp",
			},
		},
	}
	corpusData, err := json.Marshal(corpus)
	if err != nil {
		t.Fatalf("marshal vector corpus: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "vector_corpus.json"), corpusData, 0o644); err != nil {
		t.Fatalf("write vector corpus: %v", err)
	}
	index := SemanticIndex{
		RunID: "run-1",
		Files: []SemanticIndexedFile{
			{Path: "TavernWorker/main.cpp", ImportanceScore: 95, Tags: []string{"entrypoint", "startup"}},
		},
		Symbols: []SemanticSymbol{
			{Name: "WorkerBootstrap", Kind: "function", File: "TavernWorker/main.cpp", Module: "TavernWorker"},
		},
	}
	indexData, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("marshal structural index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "structural_index.json"), indexData, 0o644); err != nil {
		t.Fatalf("write structural index: %v", err)
	}

	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "Explain TavernWorker startup flow.")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "used cached analysis") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) != 1 || len(provider.requests[0].Messages) == 0 {
		t.Fatalf("expected one provider request with messages, got %#v", provider.requests)
	}
	userText := ""
	for _, msg := range provider.requests[0].Messages {
		if strings.Contains(msg.Text, "Relevant project analysis from past analyze-project runs") {
			userText = msg.Text
			break
		}
	}
	if !strings.Contains(userText, "Relevant project analysis from past analyze-project runs") {
		t.Fatalf("expected cached analysis context in user text, got %q", userText)
	}
	if !strings.Contains(userText, "Forensic Analysis: TavernWorker Runtime") {
		t.Fatalf("expected matching subsystem in injected analysis context, got %q", userText)
	}
	if !strings.Contains(userText, "Relevant vector documents") {
		t.Fatalf("expected vector corpus context in injected analysis context, got %q", userText)
	}
	if !strings.Contains(userText, "Relevant structural index hits") {
		t.Fatalf("expected structural index hits in injected analysis context, got %q", userText)
	}
}

func TestAgentDoesNotRepeatLatestProjectAnalysisContextAfterFirstTurn(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "first"}},
			{Message: Message{Role: "assistant", Text: "second"}},
		},
	}
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis dir: %v", err)
	}
	pack := KnowledgePack{
		RunID:          "run-1",
		Goal:           "map worker architecture",
		Root:           root,
		ProjectSummary: "Worker summary.",
		Subsystems: []KnowledgeSubsystem{
			{
				Title:         "Worker Runtime",
				Group:         "Forensic Analysis",
				KeyFiles:      []string{"TavernWorker/main.cpp"},
				EvidenceFiles: []string{"TavernWorker/main.cpp"},
			},
		},
	}
	data, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), data, 0o644); err != nil {
		t.Fatalf("write knowledge pack: %v", err)
	}

	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "Explain TavernWorker."); err != nil {
		t.Fatalf("first Reply: %v", err)
	}
	if _, err := agent.Reply(context.Background(), "Now summarize risks only."); err != nil {
		t.Fatalf("second Reply: %v", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected two provider requests, got %d", len(provider.requests))
	}
	firstUserText := ""
	for _, msg := range provider.requests[0].Messages {
		if strings.Contains(msg.Text, "Relevant project analysis from past analyze-project runs") {
			firstUserText = msg.Text
			break
		}
	}
	secondUserText := provider.requests[1].Messages[len(provider.requests[1].Messages)-1].Text
	if !strings.Contains(firstUserText, "Relevant project analysis from past analyze-project runs") {
		t.Fatalf("expected first turn to include analysis context, got %q", firstUserText)
	}
	if strings.Contains(secondUserText, "Relevant project analysis from past analyze-project runs") {
		t.Fatalf("expected second turn not to repeat analysis context, got %q", secondUserText)
	}
}

func TestAgentReinjectsLatestProjectAnalysisContextWhenQueryChangesMaterially(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "first"}},
			{Message: Message{Role: "assistant", Text: "second"}},
		},
	}
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis dir: %v", err)
	}
	pack := KnowledgePack{
		RunID:          "run-2",
		Goal:           "map worker and common architecture",
		Root:           root,
		ProjectSummary: "Worker and Common cooperate over IPC.",
		Subsystems: []KnowledgeSubsystem{
			{
				Title:         "Worker Runtime",
				Group:         "Forensic Analysis",
				KeyFiles:      []string{"TavernWorker/main.cpp"},
				EvidenceFiles: []string{"TavernWorker/main.cpp"},
			},
			{
				Title:            "IPC Layer",
				Group:            "Shared Infrastructure",
				Responsibilities: []string{"Owns pipe framing"},
				KeyFiles:         []string{"Common/ipc.cpp"},
				EvidenceFiles:    []string{"Common/ipc.cpp"},
			},
		},
	}
	packData, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), packData, 0o644); err != nil {
		t.Fatalf("write knowledge pack: %v", err)
	}
	index := SemanticIndex{
		RunID: "run-2",
		Files: []SemanticIndexedFile{
			{Path: "Common/ipc.cpp", ImportanceScore: 80, Tags: []string{"ipc", "pipe"}},
		},
		Symbols: []SemanticSymbol{
			{Name: "PipeRouter", Kind: "class", File: "Common/ipc.cpp", Module: "Common"},
		},
	}
	indexData, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("marshal structural index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "structural_index.json"), indexData, 0o644); err != nil {
		t.Fatalf("write structural index: %v", err)
	}

	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "Explain TavernWorker startup flow."); err != nil {
		t.Fatalf("first Reply: %v", err)
	}
	if _, err := agent.Reply(context.Background(), "Explain Common IPC module architecture in detail."); err != nil {
		t.Fatalf("second Reply: %v", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected two provider requests, got %d", len(provider.requests))
	}
	secondUserText := ""
	for _, msg := range provider.requests[1].Messages {
		if strings.Contains(msg.Text, "Relevant project analysis from past analyze-project runs") {
			secondUserText = msg.Text
			break
		}
	}
	if !strings.Contains(secondUserText, "Relevant project analysis from past analyze-project runs") {
		t.Fatalf("expected materially changed query to reinject analysis context, got %q", secondUserText)
	}
	if !strings.Contains(secondUserText, "Common/ipc.cpp") {
		t.Fatalf("expected reinjected context to include Common IPC hits, got %q", secondUserText)
	}
}

func TestAgentUsesCachedProjectAnalysisFastPathWithoutTools(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "TavernWorker startup begins in main.cpp and then initializes telemetry collectors."}},
		},
	}
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis dir: %v", err)
	}
	pack := KnowledgePack{
		RunID:          "run-fastpath",
		Goal:           "map worker architecture",
		Root:           root,
		ProjectSummary: "TavernWorker owns startup and telemetry collection.",
		Subsystems: []KnowledgeSubsystem{
			{
				Title:         "Worker Runtime",
				Group:         "Forensic Analysis",
				KeyFiles:      []string{"TavernWorker/main.cpp"},
				EvidenceFiles: []string{"TavernWorker/main.cpp"},
			},
		},
	}
	packData, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), packData, 0o644); err != nil {
		t.Fatalf("write knowledge pack: %v", err)
	}

	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "Explain TavernWorker startup flow.")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "TavernWorker startup begins") {
		t.Fatalf("unexpected fast-path reply: %q", reply)
	}
	if strings.Contains(reply, "Cached analysis fast-path") || strings.Contains(reply, "NEEDS_TOOLS") {
		t.Fatalf("expected internal fast-path markers to be suppressed, got %q", reply)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected only one provider request via fast-path, got %d", len(provider.requests))
	}
	if len(provider.requests[0].Tools) != 0 {
		t.Fatalf("expected fast-path request to expose no tools, got %#v", provider.requests[0].Tools)
	}
	lastMessage := provider.requests[0].Messages[len(provider.requests[0].Messages)-1].Text
	if !strings.Contains(lastMessage, "Fast-path check") {
		t.Fatalf("expected fast-path instruction in request, got %q", lastMessage)
	}
}

func TestAgentFallsBackToNormalToolLoopWhenCachedProjectAnalysisFastPathNeedsTools(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: projectAnalysisFastPathNeedsTools}},
			toolCallResponse("read_file", map[string]any{"path": "main.go"}),
			{Message: Message{Role: "assistant", Text: "Read the file and answered with verified details."}},
		},
	}
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis dir: %v", err)
	}
	pack := KnowledgePack{
		RunID:          "run-fastpath-2",
		Goal:           "map worker architecture",
		Root:           root,
		ProjectSummary: "TavernWorker summary.",
		Subsystems: []KnowledgeSubsystem{
			{
				Title:         "Worker Runtime",
				Group:         "Forensic Analysis",
				KeyFiles:      []string{"main.go"},
				EvidenceFiles: []string{"main.go"},
			},
		},
	}
	packData, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), packData, 0o644); err != nil {
		t.Fatalf("write knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "Explain TavernWorker startup flow in verified detail.")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "verified details") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected preflight plus normal tool loop, got %d requests", len(provider.requests))
	}
	if len(provider.requests[0].Tools) != 0 {
		t.Fatalf("expected preflight request to expose no tools")
	}
	if len(provider.requests[1].Tools) == 0 {
		t.Fatalf("expected fallback request to expose tools")
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

func TestAgentStopsAfterRepeatedIdenticalToolCallsContinueAfterRecoveryTurn(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{}),
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
	if len(provider.requests) != 5 {
		t.Fatalf("expected abort on fifth repeated tool-call turn, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[4]
	if len(lastRequest.Messages) == 0 {
		t.Fatalf("expected recovery guidance before aborting repeated tool calls")
	}
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Recovery mode") {
		t.Fatalf("expected recovery guidance before abort, got %#v", lastMessage)
	}
}

func TestShouldTrackRepeatedToolCallSignatureIgnoresReadFileOnlyTurns(t *testing.T) {
	calls := []ToolCall{
		{
			Name:      "read_file",
			Arguments: `{"path":"IMLauncherMainWindow.cpp","start_line":2170,"end_line":2200}`,
		},
	}

	if shouldTrackRepeatedToolCallSignature(calls) {
		t.Fatalf("expected read_file-only turns to use dedicated repeated-read handling")
	}
}

func TestShouldTrackRepeatedToolCallSignatureKeepsMixedToolTurns(t *testing.T) {
	calls := []ToolCall{
		{
			Name:      "read_file",
			Arguments: `{"path":"IMLauncherMainWindow.cpp","start_line":2170,"end_line":2200}`,
		},
		{
			Name:      "grep",
			Arguments: `{"pattern":"LoadBackgroundImage"}`,
		},
	}

	if !shouldTrackRepeatedToolCallSignature(calls) {
		t.Fatalf("expected mixed tool turns to keep repeated identical signature protection")
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

func TestSanitizeAssistantMessageTextRemovesKoreanToolPreambleNarration(t *testing.T) {
	text := "이제 템플릿 파일을 확인하겠습니다.\n먼저 main.py를 수정하겠습니다."

	got := sanitizeAssistantMessageText(text, true)
	if got != "" {
		t.Fatalf("expected korean tool preamble narration to be dropped, got %q", got)
	}
}

func TestSanitizeAssistantMessageTextKeepsSubstantiveKoreanToolPlan(t *testing.T) {
	text := "먼저 providers를 확인하겠습니다.\n구현 계획:\n1. 인터페이스 갱신\n2. reasoning effort 전달"

	got := sanitizeAssistantMessageText(text, true)
	if !strings.Contains(got, "구현 계획:") {
		t.Fatalf("expected substantive korean content to be kept, got %q", got)
	}
	if strings.Contains(got, "먼저 providers를 확인하겠습니다.") {
		t.Fatalf("expected korean narration preamble to be removed, got %q", got)
	}
}

func TestReplyLooksAbruptlyTruncatedDetectsCutoffTail(t *testing.T) {
	if !replyLooksAbruptlyTruncated("현재 코드는 `items` 하위 키가 있는 경로만 처리하고 있어, `items` 하위 키가 없는 구조에서는 MRU 항목을 가져오지 못합니다.\n\n이") {
		t.Fatalf("expected abrupt cutoff to be detected")
	}
	if !replyLooksAbruptlyTruncated("`HasMRUItemsSubKey` 함수에서 이 문제를 수정하겠습니다. 다른 함수들(`ParsePrivateRegistryFile`, `Parse") {
		t.Fatalf("expected unbalanced code-span cutoff to be detected")
	}
	if replyLooksAbruptlyTruncated("현재 코드는 `items` 하위 키가 없는 구조도 처리해야 합니다.") {
		t.Fatalf("did not expect complete sentence to be treated as truncated")
	}
}

func TestMergeAssistantContinuationHandlesKoreanAndEnglishBoundaries(t *testing.T) {
	if got := mergeAssistantContinuation("이", "로 인해 문제가 발생합니다."); got != "이로 인해 문제가 발생합니다." {
		t.Fatalf("unexpected korean continuation merge: %q", got)
	}
	if got := mergeAssistantContinuation("This affects", "some registry layouts."); got != "This affects some registry layouts." {
		t.Fatalf("unexpected english continuation merge: %q", got)
	}
	if got := mergeAssistantContinuation("Parse", "ItemsInInstance"); got != "ParseItemsInInstance" {
		t.Fatalf("unexpected camel-case continuation merge: %q", got)
	}
}

func TestAgentRetriesAbruptlyTruncatedFinalReply(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "현재 코드는 `items` 하위 키가 있는 경로만 처리하고 있어, `items` 하위 키가 없는 구조에서는 MRU 항목을 가져오지 못합니다.\n\n이",
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "로 인해 일부 Visual Studio 버전에서 최근 파일 목록이 누락됩니다.",
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
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "review this file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	expected := "현재 코드는 `items` 하위 키가 있는 경로만 처리하고 있어, `items` 하위 키가 없는 구조에서는 MRU 항목을 가져오지 못합니다.\n\n이로 인해 일부 Visual Studio 버전에서 최근 파일 목록이 누락됩니다."
	if reply != expected {
		t.Fatalf("unexpected merged reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected continuation retry, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "cut off mid-sentence") {
		t.Fatalf("expected continuation guidance, got %#v", lastMessage)
	}
	if len(session.Messages) < 2 || session.Messages[1].Text != expected {
		t.Fatalf("expected merged answer to replace original assistant turn, got %#v", session.Messages)
	}
	if len(session.Messages) != 2 {
		t.Fatalf("expected continuation helper turns to be trimmed from history, got %#v", session.Messages)
	}
}

func TestAgentRetriesAbruptlyTruncatedFinalReplyWhileStreaming(t *testing.T) {
	root := t.TempDir()
	provider := &streamingScriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "현재 코드는 `items` 하위 키가 있는 경로만 처리하고 있어, `items` 하위 키가 없는 구조에서는 MRU 항목을 가져오지 못합니다.\n\n이",
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "로 인해 일부 Visual Studio 버전에서 최근 파일 목록이 누락됩니다.",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var emitted strings.Builder
	agent := &Agent{
		Config:             Config{},
		Client:             provider,
		Tools:              NewToolRegistry(NewReadFileTool(ws)),
		Workspace:          ws,
		Session:            session,
		Store:              store,
		EmitAssistantDelta: func(text string) { emitted.WriteString(text) },
	}

	reply, err := agent.Reply(context.Background(), "review this file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	expected := "현재 코드는 `items` 하위 키가 있는 경로만 처리하고 있어, `items` 하위 키가 없는 구조에서는 MRU 항목을 가져오지 못합니다.\n\n이로 인해 일부 Visual Studio 버전에서 최근 파일 목록이 누락됩니다."
	if reply != expected {
		t.Fatalf("unexpected merged reply: %q", reply)
	}
	if emitted.String() != expected {
		t.Fatalf("expected streamed text to continue seamlessly, got %q", emitted.String())
	}
}

func TestAgentRetriesCodeSpanTruncationWhileStreaming(t *testing.T) {
	root := t.TempDir()
	provider := &streamingScriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "`HasMRUItemsSubKey` 함수에서 이 문제를 수정하겠습니다. 다른 함수들(`ParsePrivateRegistryFile`, `Parse",
				},
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "ItemsInInstance`)도 함께 점검해 안전하게 마무리하겠습니다.",
				},
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var emitted strings.Builder
	agent := &Agent{
		Config:             Config{},
		Client:             provider,
		Tools:              NewToolRegistry(NewReadFileTool(ws)),
		Workspace:          ws,
		Session:            session,
		Store:              store,
		EmitAssistantDelta: func(text string) { emitted.WriteString(text) },
	}

	reply, err := agent.Reply(context.Background(), "review and fix this file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	expected := "`HasMRUItemsSubKey` 함수에서 이 문제를 수정하겠습니다. 다른 함수들(`ParsePrivateRegistryFile`, `ParseItemsInInstance`)도 함께 점검해 안전하게 마무리하겠습니다."
	if reply != expected {
		t.Fatalf("unexpected merged reply: %q", reply)
	}
	if emitted.String() != expected {
		t.Fatalf("expected streamed text to continue seamlessly, got %q", emitted.String())
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
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:        "call-3",
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
	if !strings.Contains(err.Error(), "iteration=3") {
		t.Fatalf("expected iteration count, got %v", err)
	}
	if !strings.Contains(err.Error(), "max_iterations=2") {
		t.Fatalf("expected max iteration count, got %v", err)
	}
	if !strings.Contains(err.Error(), "recent_turns=") {
		t.Fatalf("expected recent tool turns summary, got %v", err)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected one recovery turn beyond the normal tool budget, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[2]
	if len(lastRequest.Messages) == 0 {
		t.Fatalf("expected recovery guidance before final tool-loop-limit turn")
	}
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "tool budget has been exhausted") {
		t.Fatalf("expected tool-loop-limit recovery guidance, got %#v", lastMessage)
	}
}

func TestAgentUsesRecoveryTurnWhenToolBudgetIsExhausted(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{"path": "."}),
			{
				Message: Message{
					Role: "assistant",
					Text: "I already inspected the workspace listing and can answer from that evidence.",
				},
				StopReason: "stop",
			},
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
		Tools:     NewToolRegistry(NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect the workspace")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "I already inspected the workspace listing and can answer from that evidence." {
		t.Fatalf("unexpected reply after recovery turn: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected one extra recovery turn, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[1]
	if len(lastRequest.Messages) == 0 {
		t.Fatalf("expected recovery guidance before final turn")
	}
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "tool budget has been exhausted") {
		t.Fatalf("expected tool-loop-limit recovery guidance, got %#v", lastMessage)
	}
}

func TestAgentExtendsToolBudgetWhenRecentTurnsShowProgress(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{"path": "."}),
			toolCallResponse("grep", map[string]any{"pattern": "alpha", "path": "."}),
			toolCallResponse("read_file", map[string]any{"path": "sample.txt", "start_line": 1, "end_line": 1}),
			{
				Message: Message{
					Role: "assistant",
					Text: "I gathered enough evidence after the extended tool budget.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{
			MaxToolIterations: 2,
		},
		Client: provider,
		Tools: NewToolRegistry(
			NewListFilesTool(ws),
			NewGrepTool(ws),
			NewReadFileTool(ws),
		),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect the workspace carefully")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "I gathered enough evidence after the extended tool budget." {
		t.Fatalf("unexpected reply after budget extension: %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected extra model turns after budget extension, got %d requests", len(provider.requests))
	}
	extendedRequest := provider.requests[2]
	if len(extendedRequest.Messages) == 0 {
		t.Fatalf("expected extension guidance before the extended turn")
	}
	lastMessage := extendedRequest.Messages[len(extendedRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "tool budget is extended") {
		t.Fatalf("expected tool budget extension guidance, got %#v", lastMessage)
	}
}

func TestCompactCutIndexPreservesRecentToolTurns(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "u1"},
		{Role: "assistant", Text: "a1"},
		{Role: "user", Text: "u2"},
		{Role: "assistant", Text: "a2"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "list_files", Arguments: `{}`}}},
		{Role: "tool", ToolName: "list_files", Text: "ok"},
		{Role: "user", Text: "g1"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "grep", Arguments: `{"pattern":"x"}`}}},
		{Role: "tool", ToolName: "grep", Text: "ok"},
		{Role: "user", Text: "g2"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "read_file", Arguments: `{"path":"a.cpp"}`}}},
		{Role: "tool", ToolName: "read_file", Text: "ok"},
		{Role: "user", Text: "g3"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "run_shell", Arguments: `{"command":"go test ./..."}`}}},
		{Role: "tool", ToolName: "run_shell", Text: "ok"},
		{Role: "user", Text: "g4"},
	}

	got := compactCutIndex(messages, 8, 4)
	if got != 4 {
		t.Fatalf("expected compact cut index 4 to preserve recent tool turns, got %d", got)
	}
}

func TestCompactCutIndexPreservesPinnedVerificationMessages(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "u1"},
		{Role: "assistant", Text: "a1"},
		{Role: "user", Text: "Automatic verification results:\n- step one failed\n- step two pending"},
		{Role: "assistant", Text: "a2"},
		{Role: "user", Text: "u2"},
		{Role: "assistant", Text: "a3"},
		{Role: "user", Text: "u3"},
		{Role: "assistant", Text: "a4"},
		{Role: "user", Text: "u4"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "list_files", Arguments: `{}`}}},
		{Role: "tool", ToolName: "list_files", Text: "ok"},
		{Role: "user", Text: "u5"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "grep", Arguments: `{"pattern":"x"}`}}},
		{Role: "tool", ToolName: "grep", Text: "ok"},
		{Role: "user", Text: "u6"},
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "read_file", Arguments: `{"path":"a.cpp"}`}}},
		{Role: "tool", ToolName: "read_file", Text: "ok"},
		{Role: "user", Text: "u7"},
	}

	got := compactCutIndex(messages, 8, 4)
	if got != 2 {
		t.Fatalf("expected compact cut index 2 to preserve pinned verification message, got %d", got)
	}
}

func TestSummarizeMessagesIncludesCompactToolErrorDetails(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "inspect the failure"},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				Name:      "failing_tool",
				Arguments: `{}`,
			}},
		},
		{
			Role:     "tool",
			ToolName: "failing_tool",
			Text:     "stderr line\n\nERROR: preview surface busy",
			IsError:  true,
		},
	}

	summary := summarizeMessages(messages, "Auto-compacted due to context growth.")
	if !strings.Contains(summary, "tool turn: failing_tool:error:preview surface busy") {
		t.Fatalf("expected compact summary to preserve tool error detail, got %q", summary)
	}
}

func TestSummarizeMessagesKeepsPinnedVerificationSnippet(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "Automatic verification results:\n- msbuild failed\n- ctest skipped\n- rerun needed"},
	}

	summary := summarizeMessages(messages, "Auto-compacted due to context growth.")
	if !strings.Contains(summary, "Automatic verification results:") || !strings.Contains(summary, "msbuild failed") {
		t.Fatalf("expected compact summary to preserve pinned verification snippet, got %q", summary)
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

func TestAgentPersistsInProgressToolStateBeforeExecution(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	sessionPath := filepath.Join(store.Root(), session.ID+".json")

	sawInProgress := false
	observer := &observingSessionTool{
		name:        "observe_session",
		sessionPath: sessionPath,
		output:      "observed",
	}
	observer.onExecute = func(data []byte) {
		if strings.Contains(string(data), `"text": "IN_PROGRESS: observe_session"`) {
			sawInProgress = true
		}
	}

	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("observe_session", map[string]any{}),
			{Message: Message{Role: "assistant", Text: "done"}},
		},
	}
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(observer),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect session persistence")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "done") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if !sawInProgress {
		t.Fatalf("expected observer to see an in-progress tool state before execution")
	}
}

func TestAgentPersistsCompletedToolResultsBetweenToolCalls(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	sessionPath := filepath.Join(store.Root(), session.ID+".json")

	firstTool := &observingSessionTool{
		name:   "first_tool",
		output: "first done",
	}
	secondSawFirst := false
	secondTool := &observingSessionTool{
		name:        "second_tool",
		sessionPath: sessionPath,
		output:      "second done",
		onExecute: func(data []byte) {
			secondSawFirst = strings.Contains(string(data), `"tool_name": "first_tool"`) &&
				strings.Contains(string(data), `"text": "first done"`)
		},
	}

	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{
						{ID: "call-1", Name: "first_tool", Arguments: `{}`},
						{ID: "call-2", Name: "second_tool", Arguments: `{}`},
					},
				},
				StopReason: "tool_calls",
			},
			{Message: Message{Role: "assistant", Text: "done"}},
		},
	}
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(firstTool, secondTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect persistence between tool calls")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "done") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if !secondSawFirst {
		t.Fatalf("expected the second tool call to observe the first persisted tool result")
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
			MaxRequestRetries:   1,
			RequestRetryDelayMs: 1,
			RequestTimeoutSecs:  1,
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
			MaxRequestRetries:   1,
			RequestRetryDelayMs: 1,
			RequestTimeoutSecs:  1,
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
			MaxRequestRetries:   1,
			RequestRetryDelayMs: 1,
			RequestTimeoutSecs:  1,
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

func TestCompleteModelTurnRetriesTransientProviderErrors(t *testing.T) {
	provider := &transientErrorThenSuccessProviderClient{
		failCount: 2,
		err:       fmt.Errorf("openai API error (503 Service Unavailable): upstream overloaded"),
	}
	var progress []string
	agent := &Agent{
		Config: Config{
			MaxRequestRetries:   2,
			RequestRetryDelayMs: 1,
			RequestTimeoutSecs:  1,
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
	if provider.calls != 3 {
		t.Fatalf("expected three provider attempts, got %d", provider.calls)
	}
	if len(progress) == 0 || !strings.Contains(progress[0], "Transient provider error") {
		t.Fatalf("expected transient retry progress message, got %#v", progress)
	}
}

func TestCompleteModelTurnDoesNotRetryPermanentProviderErrors(t *testing.T) {
	provider := &transientErrorThenSuccessProviderClient{
		failCount: 1,
		err:       fmt.Errorf("openai API error (401 Unauthorized): invalid api key"),
	}
	agent := &Agent{
		Config: Config{
			MaxRequestRetries:   2,
			RequestRetryDelayMs: 1,
			RequestTimeoutSecs:  1,
		},
		Client: provider,
	}

	_, err := agent.completeModelTurn(context.Background(), ChatRequest{
		Model: "test-model",
	})
	if err == nil {
		t.Fatalf("expected permanent provider error")
	}
	if provider.calls != 1 {
		t.Fatalf("expected one provider attempt for permanent error, got %d", provider.calls)
	}
}

func TestAgentReadOnlyAnalysisFallsBackWhenModelDoesNotSupportToolUse(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VAllocAnalyzer.cpp"), []byte("int Check()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &toolUnsupportedThenSuccessClient{}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewGrepTool(ws), NewListFilesTool(ws), NewGitStatusTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@VAllocAnalyzer.cpp 코드에 버그가 있는지 검토해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "버그") && !strings.Contains(reply, "검토") && !strings.Contains(reply, "경계 조건") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if provider.calls != 2 {
		t.Fatalf("expected retry without tools, got %d calls", provider.calls)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected two recorded requests, got %d", len(provider.requests))
	}
	if len(provider.requests[0].Tools) == 0 {
		t.Fatalf("expected first request to include tools")
	}
	if len(provider.requests[1].Tools) != 0 {
		t.Fatalf("expected fallback request to omit tools, got %d", len(provider.requests[1].Tools))
	}
	if !strings.Contains(provider.requests[1].System, "does not support tool use") {
		t.Fatalf("expected fallback system guidance, got %q", provider.requests[1].System)
	}
}

func TestAgentEditRequestReturnsFriendlyToolUseUnsupportedError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VAllocAnalyzer.cpp"), []byte("int Check()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &toolUnsupportedThenSuccessClient{}
	session := NewSession(root, "openrouter", "meta-llama/llama-3.2-11b-vision-instruct", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewApplyPatchTool(ws), NewGitStatusTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "@VAllocAnalyzer.cpp 코드에 버그가 있는지 검토하고 수정해줘")
	if err == nil {
		t.Fatalf("expected tool-use unsupported error")
	}
	text := err.Error()
	if !strings.Contains(text, "selected model does not support tool use") {
		t.Fatalf("expected friendly tool-use message, got %q", text)
	}
	if strings.Contains(text, "request={") {
		t.Fatalf("expected raw request dump to be hidden, got %q", text)
	}
	if provider.calls != 1 {
		t.Fatalf("expected edit request to fail without no-tools retry, got %d calls", provider.calls)
	}
}

func TestAgentRetriesWhenEditRequestHandsPatchBackWithoutUsingTools(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "VAllocAnalyzer.cpp")
	before := "int Check()\n{\n    return 0;\n}\n"
	after := "int Check()\n{\n    return 1;\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "도구 사용에 문제가 있어 직접 패치를 적용해드리지 못합니다. 위 설명에 따라 코드를 직접 수정해주시면 됩니다."}},
			toolCallResponse("read_file", map[string]any{"path": "VAllocAnalyzer.cpp"}),
			toolCallResponse("write_file", map[string]any{"path": "VAllocAnalyzer.cpp", "content": after}),
			{Message: Message{Role: "assistant", Text: "VAllocAnalyzer.cpp를 수정했고 반환값이 1이 되도록 반영했습니다."}},
		},
	}
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@VAllocAnalyzer.cpp 코드에 버그가 있는지 검토하고 수정해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "수정") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(contents) != after {
		t.Fatalf("expected file to be updated after retry, got %q", string(contents))
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected retry path to consume four requests, got %d", len(provider.requests))
	}
	secondRequest := provider.requests[1]
	if len(secondRequest.Messages) == 0 {
		t.Fatalf("expected retry guidance before second request")
	}
	lastMessage := secondRequest.Messages[len(secondRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Do not hand the patch back to the user") {
		t.Fatalf("expected manual-edit handoff guidance, got %#v", lastMessage)
	}
}

func TestAgentBlocksGitCommitWithoutExplicitUserRequest(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("git_commit", map[string]any{"message": "fix: unexpected commit"}),
			{Message: Message{Role: "assistant", Text: "코드 수정만 완료했고 커밋은 하지 않았습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewGitCommitTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "@VAllocAnalyzer.cpp 코드에 버그가 있는지 검토하고 수정해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "커밋은 하지 않았습니다") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected git tool request to be blocked and retried, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Do not stage, commit, push, or open a PR") {
		t.Fatalf("expected git mutation guidance, got %#v", lastMessage)
	}
}

func TestAgentBlocksShellGitCommitWithoutExplicitUserRequest(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("run_shell", map[string]any{"command": `git commit -m "fix: unexpected commit"`}),
			{Message: Message{Role: "assistant", Text: "문서만 작성했고 git 작업은 하지 않았습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewRunShellTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "리서치 문서를 markdown 파일로 작성해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "git 작업은 하지 않았습니다") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected shell git mutation request to be blocked and retried, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Do not stage, commit, push, or open a PR") {
		t.Fatalf("expected git mutation guidance, got %#v", lastMessage)
	}
}

func TestAgentBlocksDocumentReadBeforeParentListing(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("read_file", map[string]any{"path": "anti-cheat-research/analysis/security-review.md"}),
			{Message: Message{Role: "assistant", Text: "먼저 부모 디렉터리를 확인한 뒤 진행하겠습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "리서치 문서를 markdown 파일로 작성해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "부모 디렉터리") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected document read request to be blocked and retried, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "First use list_files on the parent directory") {
		t.Fatalf("expected document read guidance, got %#v", lastMessage)
	}
}

func TestAgentBlocksLocalInspectionBeforeWebResearchWhenCapabilityAvailable(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	webTool := &staticTool{
		name:   "mcp__web__search_web",
		output: "source 1\nsource 2",
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{"path": "."}),
			toolCallResponse("mcp__web__search_web", map[string]any{"query": "hypervisor anti-cheat latest"}),
			{Message: Message{Role: "assistant", Text: "웹 소스를 먼저 수집한 뒤 문서를 진행하겠습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewListFilesTool(ws), webTool),
		Workspace: ws,
		Session:   session,
		Store:     store,
		MCP: &MCPManager{
			servers: []*MCPClient{
				{
					config: MCPServerConfig{Name: "web"},
					tools: []MCPToolDescriptor{
						{Name: "search_web", Description: "Search the web for current articles and references"},
					},
				},
			},
		},
	}

	reply, err := agent.Reply(context.Background(), "Hypervisor를 이용한 게임핵 탐지 최신 기술들을 리서치하고 설계 문서를 파일로 작성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "웹 소스") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if webTool.calls != 1 {
		t.Fatalf("expected web research tool to run once, got %d", webTool.calls)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected local inspection request to be blocked and retried, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Before using local inspection or edit tools") {
		t.Fatalf("expected web research guidance, got %#v", lastMessage)
	}
}

func TestAgentAllowsLocalInspectionAfterWebResearchToolUsed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	webTool := &staticTool{
		name:   "mcp__web__search_web",
		output: "source 1\nsource 2",
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("mcp__web__search_web", map[string]any{"query": "hypervisor anti-cheat latest"}),
			toolCallResponse("list_files", map[string]any{"path": "."}),
			{Message: Message{Role: "assistant", Text: "웹 조사 후 로컬 문서 경로까지 확인했습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(webTool, NewListFilesTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		MCP: &MCPManager{
			servers: []*MCPClient{
				{
					config: MCPServerConfig{Name: "web"},
					tools: []MCPToolDescriptor{
						{Name: "search_web", Description: "Search the web for current articles and references"},
					},
				},
			},
		},
	}

	reply, err := agent.Reply(context.Background(), "Hypervisor를 이용한 게임핵 탐지 최신 기술들을 리서치하고 설계 문서를 파일로 작성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "로컬 문서 경로") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if webTool.calls != 1 {
		t.Fatalf("expected web research tool to run once, got %d", webTool.calls)
	}
	toolMessages := 0
	for _, msg := range session.Messages {
		if msg.Role == "tool" && !msg.IsError {
			toolMessages++
		}
	}
	if toolMessages != 2 {
		t.Fatalf("expected both web research and list_files tool results to be recorded, got %d", toolMessages)
	}
}

func TestAgentUsesLoadedWebResearchMCPToolsBeforeLocalInspection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("workspace\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: root, Root: root}, []MCPServerConfig{{
		Name:         "web-research",
		Command:      os.Args[0],
		Args:         []string{"-test.run=TestMCPWebResearchHelperProcess"},
		Capabilities: []string{"web_search", "web_fetch"},
		Env: map[string]string{
			"KERNFORGE_MCP_WEB_HELPER": "1",
		},
	}})
	defer manager.Close()
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("list_files", map[string]any{"path": "."}),
			toolCallResponse("mcp__web_research__search_web", map[string]any{"query": "hypervisor anti-cheat latest"}),
			toolCallResponse("mcp__web_research__fetch_url", map[string]any{"url": "https://example.test/hypervisor-detection"}),
			{Message: Message{Role: "assistant", Text: "웹 검색과 본문 fetch까지 마친 뒤 로컬 파일 작업으로 넘어갈 준비가 됐습니다."}},
		},
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     buildRegistry(ws, manager),
		Workspace: ws,
		Session:   session,
		Store:     store,
		MCP:       manager,
	}

	reply, err := agent.Reply(context.Background(), "Hypervisor를 이용한 게임핵 탐지 최신 기술들을 리서치하고 설계 문서를 파일로 작성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "fetch") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected blocked local inspection plus two web tool turns, got %d requests", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Before using local inspection or edit tools") {
		t.Fatalf("expected web research guidance, got %#v", lastMessage)
	}
	toolMessages := 0
	for _, msg := range session.Messages {
		if msg.Role == "tool" && !msg.IsError {
			toolMessages++
		}
	}
	if toolMessages != 2 {
		t.Fatalf("expected search and fetch tool results to be recorded, got %d", toolMessages)
	}
}

func TestAgentDoesNotSuppressFinalReplyAfterStreamFallback(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:             Config{},
		Client:             &fallbackReplayClient{},
		Tools:              NewToolRegistry(NewReadFileTool(ws)),
		Workspace:          ws,
		Session:            session,
		Store:              store,
		EmitAssistantDelta: func(string) {},
	}
	agent.lastEmittedText = "full fallback answer"

	reply, err := agent.Reply(context.Background(), "inspect this file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "full fallback answer" {
		t.Fatalf("expected final reply replay, got %q", reply)
	}
}

func TestAgentReturnsFinalReplyEvenWhenAlreadyStreamed(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config: Config{},
		Client: &streamingScriptedProviderClient{
			replies: []ChatResponse{
				{Message: Message{Role: "assistant", Text: "final streamed answer"}},
			},
		},
		Tools:              NewToolRegistry(NewReadFileTool(ws)),
		Workspace:          ws,
		Session:            session,
		Store:              store,
		EmitAssistantDelta: func(string) {},
	}

	reply, err := agent.Reply(context.Background(), "inspect this file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "final streamed answer" {
		t.Fatalf("expected streamed final reply to be returned, got %q", reply)
	}
}

func TestAgentNudgesAfterRepeatedReadFilePathAcrossRanges(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "Tavern", "TavernWorker")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "TavernWorkerCore.cpp"), []byte("int WorkerMain()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("read_file", map[string]any{"path": "Tavern/TavernWorker/TavernWorkerCore.cpp", "start_line": 1, "end_line": 1}),
			toolCallResponse("read_file", map[string]any{"path": "Tavern/TavernWorker/TavernWorkerCore.cpp", "start_line": 1, "end_line": 2}),
			toolCallResponse("read_file", map[string]any{"path": "Tavern/TavernWorker/TavernWorkerCore.cpp", "start_line": 2, "end_line": 3}),
			toolCallResponse("read_file", map[string]any{"path": "Tavern/TavernWorker/TavernWorkerCore.cpp", "start_line": 3, "end_line": 4}),
			{Message: Message{Role: "assistant", Text: "I have enough context now and can explain the issue."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect this file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "enough context") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 5 {
		t.Fatalf("expected fifth turn after repeated read_file nudge, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[4]
	if len(lastRequest.Messages) == 0 {
		t.Fatalf("expected repeated read_file guidance before final turn")
	}
	foundGuidance := false
	for _, msg := range lastRequest.Messages {
		if msg.Role != "user" {
			continue
		}
		if strings.Contains(msg.Text, "read the same file repeatedly") || strings.Contains(msg.Text, "came from cached previously-read content") {
			foundGuidance = true
			break
		}
	}
	if !foundGuidance {
		t.Fatalf("expected repeated read_file guidance in final request, got %#v", lastRequest.Messages)
	}
}

func TestAgentStopsAfterRepeatedReadFilePathAcrossRangesAfterRecoveryTurn(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "Tavern", "TavernWorker")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "TavernWorkerCore.cpp"), []byte("int WorkerMain()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("read_file", map[string]any{"path": "Tavern/TavernWorker/TavernWorkerCore.cpp", "start_line": 1, "end_line": 1}),
			toolCallResponse("read_file", map[string]any{"path": "Tavern/TavernWorker/TavernWorkerCore.cpp", "start_line": 1, "end_line": 2}),
			toolCallResponse("read_file", map[string]any{"path": "Tavern/TavernWorker/TavernWorkerCore.cpp", "start_line": 2, "end_line": 3}),
			toolCallResponse("read_file", map[string]any{"path": "Tavern/TavernWorker/TavernWorkerCore.cpp", "start_line": 3, "end_line": 4}),
			toolCallResponse("read_file", map[string]any{"path": "Tavern/TavernWorker/TavernWorkerCore.cpp", "start_line": 1, "end_line": 3}),
			toolCallResponse("read_file", map[string]any{"path": "Tavern/TavernWorker/TavernWorkerCore.cpp", "start_line": 2, "end_line": 4}),
			toolCallResponse("read_file", map[string]any{"path": "Tavern/TavernWorker/TavernWorkerCore.cpp", "start_line": 1, "end_line": 4}),
			toolCallResponse("read_file", map[string]any{"path": "Tavern/TavernWorker/TavernWorkerCore.cpp", "start_line": 2, "end_line": 4}),
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "inspect this file")
	if err == nil {
		t.Fatalf("expected repeated same-file reads to stop the loop")
	}
	if !strings.Contains(err.Error(), "repeatedly reading the same file") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(provider.requests) != 8 {
		t.Fatalf("expected abort on eighth repeated same-file turn, got %d requests", len(provider.requests))
	}
	recoveryRequest := provider.requests[6]
	if len(recoveryRequest.Messages) == 0 {
		t.Fatalf("expected recovery guidance before final repeated read")
	}
	foundRecovery := false
	for _, msg := range recoveryRequest.Messages {
		if msg.Role == "user" && strings.Contains(msg.Text, "Recovery mode") {
			foundRecovery = true
			break
		}
	}
	if !foundRecovery {
		t.Fatalf("expected recovery guidance in request %#v", recoveryRequest.Messages)
	}
}

func TestSummarizeToolTurnIncludesReadFileRangeDetails(t *testing.T) {
	messages := []Message{
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{
					ID:        "call-1",
					Name:      "read_file",
					Arguments: `{"path":"Tavern/TavernWorker/TavernWorkerCore.cpp","start_line":29,"end_line":58}`,
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call-1",
			ToolName:   "read_file",
			Text:       "ok",
		},
	}

	got := summarizeToolTurn(messages, 0)
	if !strings.Contains(got, "read_file[Tavern/TavernWorker/TavernWorkerCore.cpp:29-58]:ok") {
		t.Fatalf("expected read_file range details in diagnostic, got %q", got)
	}
}

func TestSummarizeToolTurnMarksCachedReadFileResults(t *testing.T) {
	messages := []Message{
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{
					ID:        "call-1",
					Name:      "read_file",
					Arguments: `{"path":"Tavern/TavernWorker/TavernWorkerCore.cpp","start_line":29,"end_line":58}`,
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call-1",
			ToolName:   "read_file",
			Text:       "NOTE: returning cached content for an unchanged read_file range.\n  29 | cached",
		},
	}

	got := summarizeToolTurn(messages, 0)
	if !strings.Contains(got, "read_file[Tavern/TavernWorker/TavernWorkerCore.cpp:29-58]:cached") {
		t.Fatalf("expected cached read_file diagnostic, got %q", got)
	}
}

func TestAgentNudgesSoonerAfterCachedReadFileResult(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "Tavern", "TavernWorker")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "TavernWorkerCore.cpp"), []byte("int WorkerMain()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("read_file", map[string]any{"path": "Tavern/TavernWorker/TavernWorkerCore.cpp", "start_line": 1, "end_line": 2}),
			toolCallResponse("read_file", map[string]any{"path": "Tavern/TavernWorker/TavernWorkerCore.cpp", "start_line": 1, "end_line": 2}),
			{Message: Message{Role: "assistant", Text: "I already have enough context."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "inspect this file")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "enough context") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected cached-read nudge before final turn, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[2]
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "came from cached previously-read content") {
		t.Fatalf("expected cached read guidance, got %#v", lastMessage)
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

func TestAgentAbortsAfterFourthRepeatedToolFailure(t *testing.T) {
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
			toolCallResponse("failing_tool", map[string]any{"attempt": 4}),
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
	if len(provider.requests) != 4 {
		t.Fatalf("expected abort on fourth failing turn, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[3]
	if len(lastRequest.Messages) == 0 {
		t.Fatalf("expected recovery guidance before final repeated failure turn")
	}
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "Recovery mode") {
		t.Fatalf("expected recovery guidance before abort, got %#v", lastMessage)
	}
}

func TestAgentSingleToolFailureGetsRecoveryTurnInsteadOfRepeatedFailureLabel(t *testing.T) {
	root := t.TempDir()
	failTool := &failingTool{
		name: "failing_tool",
		err:  fmt.Errorf("preview surface busy"),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("failing_tool", map[string]any{}),
			{
				Message: Message{
					Role: "assistant",
					Text: "The preview surface stayed busy, so I am stopping instead of repeating the same failing tool call.",
				},
				StopReason: "stop",
			},
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

	reply, err := agent.Reply(context.Background(), "try the preview flow")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "preview surface stayed busy") {
		t.Fatalf("unexpected recovery-turn reply: %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected one recovery turn after the single tool failure, got %d requests", len(provider.requests))
	}
	lastRequest := provider.requests[1]
	if len(lastRequest.Messages) == 0 {
		t.Fatalf("expected recovery guidance before final turn")
	}
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "tool budget has been exhausted") {
		t.Fatalf("expected tool-loop-limit recovery guidance, got %#v", lastMessage)
	}
}

func TestAgentFinalAnswerReviewerRequestsRevisionBeforeReturn(t *testing.T) {
	root := t.TempDir()
	mainProvider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "1. Inspect the issue\n2. Fix it\n3. Summarize the result",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "The issue is fixed.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "I updated the fix, verified the result, and there are no remaining blockers.",
				},
				StopReason: "stop",
			},
		},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "APPROVED\nThe execution plan is sound.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "NEEDS_REVISION\nThe final answer does not mention verification or whether any blockers remain.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "APPROVED\nThe revised final answer is ready.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config: Config{
			Model: "model",
		},
		Client:         mainProvider,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Tools:          NewToolRegistry(),
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          store,
	}

	reply, err := agent.Reply(context.Background(), "fix the bug and summarize the result")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "verified the result") {
		t.Fatalf("expected revised final answer, got %q", reply)
	}
	if len(reviewer.requests) != 3 {
		t.Fatalf("expected plan review + 2 final reviews, got %d requests", len(reviewer.requests))
	}
	foundRevisionPrompt := false
	for _, msg := range session.Messages {
		if msg.Role == "user" && strings.Contains(msg.Text, "Reviewer feedback: the proposed final answer is not ready yet") {
			foundRevisionPrompt = true
			break
		}
	}
	if !foundRevisionPrompt {
		t.Fatalf("expected reviewer revision prompt to be added to the session")
	}
	if session.TaskState == nil || session.TaskState.FinalReviewVerdict != "approved" {
		t.Fatalf("expected final review verdict to be approved, got %#v", session.TaskState)
	}
}

func TestBuildRecoveryGuidanceCanRefreshExecutionPlan(t *testing.T) {
	root := t.TempDir()
	mainProvider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "1. Poll the active shell job\n2. Inspect the failing output\n3. Only rerun verification if the job output is stale",
				},
				StopReason: "stop",
			},
		},
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "- The loop is repeating a stale verification step.\n- Poll the running shell job before launching another build.",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "APPROVED\nThe refreshed plan breaks the loop.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		Goal:           "Finish the long-running verification flow without rerunning the same build.",
		PlanSummary:    "1. Run the build\n2. Rerun the build again",
		FailedAttempts: []string{"Repeated the same build", "Repeated the same build again"},
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config: Config{
			Model: "model",
		},
		Client:         mainProvider,
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Tools:          NewToolRegistry(),
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          store,
	}

	guidance := agent.buildRecoveryGuidance(context.Background(), string(recoveryTriggerRepeatedToolError), "fallback recovery", "recent tools", "same failure")
	if !strings.Contains(guidance, "Refreshed execution plan") {
		t.Fatalf("expected refreshed execution plan in guidance, got %q", guidance)
	}
	if session.TaskState == nil || session.TaskState.PlanRefreshCount == 0 {
		t.Fatalf("expected plan refresh count to increase, got %#v", session.TaskState)
	}
	if len(session.Plan) == 0 {
		t.Fatalf("expected refreshed session plan items")
	}
	if !strings.Contains(session.TaskState.PlanSummary, "Poll the active shell job") {
		t.Fatalf("expected refreshed plan summary, got %#v", session.TaskState)
	}
}

func TestNoteToolExecutionResultKeepsSharedPlanInProgressWhenBackgroundJobStarts(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.TaskState = &TaskState{}
	session.Plan = []PlanItem{
		{Step: "Start background verification", Status: "in_progress"},
		{Step: "Poll the background job", Status: "pending"},
	}
	now := time.Now()
	session.BackgroundJobs = []BackgroundShellJob{{
		ID:             "job-1",
		Command:        "go test ./...",
		CommandSummary: "go test ./...",
		Status:         "running",
		StartedAt:      now,
		UpdatedAt:      now,
	}}
	agent := &Agent{
		Session: session,
	}

	agent.noteToolExecutionResult(ToolCall{Name: "run_shell_background"}, "started background shell job job-1 [running]\ncommand: go test ./...\nstatus: running", nil)

	if session.Plan[0].Status != "in_progress" || session.Plan[1].Status != "pending" {
		t.Fatalf("expected shared plan to remain in progress while background job is only started, got %#v", session.Plan)
	}
	if session.TaskState.PlanCursor != 0 {
		t.Fatalf("expected plan cursor to stay at 0, got %d", session.TaskState.PlanCursor)
	}
	if !slices.Contains(session.TaskState.PendingChecks, backgroundShellJobPendingCheck) {
		t.Fatalf("expected pending background-job check, got %#v", session.TaskState.PendingChecks)
	}
}

func TestNoteToolExecutionResultAdvancesSharedPlanOnlyAfterBackgroundBundleCompletes(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		PendingChecks: []string{backgroundShellJobPendingCheck},
	}
	session.Plan = []PlanItem{
		{Step: "Poll the background jobs", Status: "in_progress"},
		{Step: "Summarize the result", Status: "pending"},
	}
	now := time.Now()
	session.BackgroundJobs = []BackgroundShellJob{
		{ID: "job-1", Status: "completed", StartedAt: now, UpdatedAt: now},
		{ID: "job-2", Status: "running", StartedAt: now, UpdatedAt: now},
	}
	agent := &Agent{
		Session: session,
	}

	agent.noteToolExecutionResult(ToolCall{Name: "check_shell_bundle"}, "summary: completed=1 running=1 failed=0 total=2\n- job-1 [completed]\n- job-2 [running]", nil)

	if session.Plan[0].Status != "in_progress" || session.TaskState.PlanCursor != 0 {
		t.Fatalf("expected plan to remain in progress while bundle still has running jobs, got %#v cursor=%d", session.Plan, session.TaskState.PlanCursor)
	}
	if !slices.Contains(session.TaskState.PendingChecks, backgroundShellJobPendingCheck) {
		t.Fatalf("expected pending background-job check while bundle is still running, got %#v", session.TaskState.PendingChecks)
	}

	session.BackgroundJobs[1].Status = "completed"
	agent.noteToolExecutionResult(ToolCall{Name: "check_shell_bundle"}, "summary: completed=2 running=0 failed=0 total=2\n- job-1 [completed]\n- job-2 [completed]", nil)

	if session.Plan[0].Status != "completed" || session.Plan[1].Status != "in_progress" {
		t.Fatalf("expected shared plan to advance after bundle completion, got %#v", session.Plan)
	}
	if session.TaskState.PlanCursor != 1 {
		t.Fatalf("expected plan cursor to advance to 1, got %d", session.TaskState.PlanCursor)
	}
	if slices.Contains(session.TaskState.PendingChecks, backgroundShellJobPendingCheck) {
		t.Fatalf("expected background-job pending check to clear after completion, got %#v", session.TaskState.PendingChecks)
	}
}

func TestCompleteLoopLeavesSharedPlanOpenWhenBackgroundWorkRemains(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "The fix is partially understood, but the background verification job is still running.",
				},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		Goal:        "Finish the long-running verification flow.",
		Phase:       "execution",
		PlanSummary: "1. Start background verification\n2. Poll the result\n3. Summarize the status",
		PendingChecks: []string{
			backgroundShellJobPendingCheck,
		},
	}
	session.Plan = []PlanItem{
		{Step: "Start background verification", Status: "completed"},
		{Step: "Poll the result", Status: "in_progress"},
		{Step: "Summarize the status", Status: "pending"},
	}
	session.syncTaskStatePlanCursor()
	now := time.Now()
	session.BackgroundJobs = []BackgroundShellJob{{
		ID:             "job-1",
		Command:        "go test ./...",
		CommandSummary: "go test ./...",
		Status:         "running",
		StartedAt:      now,
		UpdatedAt:      now,
	}}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.completeLoop(context.Background(), false, false, false)
	if err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if !strings.Contains(reply, "still running") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if session.TaskState == nil {
		t.Fatalf("expected task state to be preserved")
	}
	if session.TaskState.Phase == "done" {
		t.Fatalf("expected task state to remain open while background work remains, got %#v", session.TaskState)
	}
	if session.Plan[1].Status != "in_progress" || session.TaskState.PlanCursor != 1 {
		t.Fatalf("expected shared plan to remain open, got %#v cursor=%d", session.Plan, session.TaskState.PlanCursor)
	}

	reloaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.TaskState == nil || reloaded.TaskState.Phase == "done" {
		t.Fatalf("expected persisted task state to stay open, got %#v", reloaded.TaskState)
	}
	if len(reloaded.Plan) < 2 || reloaded.Plan[1].Status != "in_progress" {
		t.Fatalf("expected persisted shared plan to remain open, got %#v", reloaded.Plan)
	}
}

func TestNoteVerificationResultRemovesOnlyVerificationPendingCheck(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		PendingChecks: []string{
			verificationPendingCheck,
			backgroundShellJobPendingCheck,
		},
	}
	session.Plan = []PlanItem{
		{Step: "Verify the result", Status: "in_progress"},
		{Step: "Poll the background job", Status: "pending"},
	}
	now := time.Now()
	session.BackgroundJobs = []BackgroundShellJob{{
		ID:        "job-1",
		Status:    "running",
		StartedAt: now,
		UpdatedAt: now,
	}}
	agent := &Agent{
		Session: session,
	}

	agent.noteVerificationResult(VerificationReport{
		Steps: []VerificationStep{{
			Label:  "go test ./...",
			Status: VerificationPassed,
		}},
	})

	if slices.Contains(session.TaskState.PendingChecks, verificationPendingCheck) {
		t.Fatalf("expected verification pending check to be removed, got %#v", session.TaskState.PendingChecks)
	}
	if !slices.Contains(session.TaskState.PendingChecks, backgroundShellJobPendingCheck) {
		t.Fatalf("expected background pending check to remain, got %#v", session.TaskState.PendingChecks)
	}
	if session.TaskState.Phase != "execution" {
		t.Fatalf("expected task state to stay in execution while background work remains, got %#v", session.TaskState)
	}
	if session.TaskState.NextStep != backgroundShellJobPendingCheck {
		t.Fatalf("expected next step to point at the remaining background check, got %#v", session.TaskState)
	}
}

func TestAssignFocusedOwnerNodeToToolCalls(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		ExecutorFocusNode: "plan-02",
	}

	calls := []ToolCall{
		{
			Name:      "apply_patch",
			Arguments: `{"patch":"*** Begin Patch\n*** End Patch\n"}`,
		},
		{
			Name:      "run_shell",
			Arguments: `{"command":"go test ./..."}`,
		},
		{
			Name:      "read_file",
			Arguments: `{"path":"main.go"}`,
		},
	}

	updated := assignFocusedOwnerNodeToToolCalls(calls, session)
	if got := stringValue(toolCallArgumentsMap(updated[0]), "owner_node_id"); got != "plan-02" {
		t.Fatalf("expected apply_patch owner_node_id to be injected, got %#v", updated[0])
	}
	if got := stringValue(toolCallArgumentsMap(updated[1]), "owner_node_id"); got != "plan-02" {
		t.Fatalf("expected run_shell owner_node_id to be injected, got %#v", updated[1])
	}
	if got := stringValue(toolCallArgumentsMap(updated[2]), "owner_node_id"); got != "" {
		t.Fatalf("expected read-only tool call to remain unchanged, got %#v", updated[2])
	}
}

func TestAssignFocusedOwnerNodeToToolCallsPreservesExplicitOwner(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.TaskState = &TaskState{
		ExecutorFocusNode: "plan-02",
	}

	calls := []ToolCall{
		{
			Name:      "run_shell",
			Arguments: `{"command":"go test ./...","owner_node_id":"plan-99"}`,
		},
	}

	updated := assignFocusedOwnerNodeToToolCalls(calls, session)
	if got := stringValue(toolCallArgumentsMap(updated[0]), "owner_node_id"); got != "plan-99" {
		t.Fatalf("expected explicit owner_node_id to be preserved, got %#v", updated[0])
	}
}
