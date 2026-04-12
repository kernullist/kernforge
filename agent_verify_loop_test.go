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

type streamingScriptedProviderClient struct {
	replies  []ChatResponse
	requests []ChatRequest
	index    int
}

func (s *streamingScriptedProviderClient) Name() string { return "streaming-scripted" }

func (s *streamingScriptedProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
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
	if !strings.Contains(reply, "Cached analysis fast-path") || !strings.Contains(reply, "confidence:") {
		t.Fatalf("expected fast-path provenance and confidence, got %q", reply)
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
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != "user" || !strings.Contains(lastMessage.Text, "read the same file repeatedly") {
		t.Fatalf("expected repeated read_file guidance, got %#v", lastMessage)
	}
}

func TestAgentStopsAfterRepeatedReadFilePathAcrossRanges(t *testing.T) {
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
