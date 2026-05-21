package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKernforgeMCPServerAcceptsJSONLineFrames(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Provider = ""
	cfg.Model = ""
	cfg.BaseURL = ""
	cfg.PermissionMode = string(ModeBypass)
	cfg.SessionDir = filepath.Join(root, ".kernforge", "sessions")
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	cfg.HooksEnabled = boolPtr(false)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := runKernforgeMCPServer(root, cfg, "", strings.NewReader(input), &output); err != nil {
		t.Fatalf("run mcp server: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two json-line responses, got %d: %q", len(lines), output.String())
	}
	for _, line := range lines {
		if strings.Contains(line, "Content-Length") {
			t.Fatalf("json-line request received header-framed response: %q", output.String())
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("response is not a json line: %v: %q", err, line)
		}
	}
	if !strings.Contains(lines[1], "kernforge_status") {
		t.Fatalf("tools/list response missing kernforge_status: %s", lines[1])
	}
}

func TestMCPConfigProviderOverrideResetsProviderSpecificBaseURL(t *testing.T) {
	cases := []struct {
		name          string
		startProvider string
		startBaseURL  string
		override      mcpServerConfigOverrides
		want          string
	}{
		{
			name:     "openai-codex",
			override: mcpServerConfigOverrides{Provider: "openai-codex"},
			want:     openAICodexDefaultBaseURL,
		},
		{
			name:     "deepseek",
			override: mcpServerConfigOverrides{Provider: "deepseek"},
			want:     "https://api.deepseek.com",
		},
		{
			name:     "lmstudio",
			override: mcpServerConfigOverrides{Provider: "lmstudio"},
			want:     "http://localhost:1234/v1",
		},
		{
			name:     "vllm explicit",
			override: mcpServerConfigOverrides{Provider: "vllm", BaseURL: "http://127.0.0.1:9000/v1"},
			want:     "http://127.0.0.1:9000/v1",
		},
		{
			name:          "same provider preserves custom URL",
			startProvider: "lmstudio",
			startBaseURL:  "http://192.0.2.10:1234/v1",
			override:      mcpServerConfigOverrides{Provider: "lmstudio"},
			want:          "http://192.0.2.10:1234/v1",
		},
		{
			name:     "codex-cli",
			override: mcpServerConfigOverrides{Provider: "codex-cli"},
			want:     "",
		},
	}

	for _, tc := range cases {
		startProvider := firstNonBlankString(tc.startProvider, "openai")
		startBaseURL := firstNonBlankString(tc.startBaseURL, "https://api.openai.com/v1")
		cfg := Config{
			Provider: startProvider,
			BaseURL:  startBaseURL,
		}
		tc.override.apply(&cfg)
		if got := cfg.BaseURL; got != tc.want {
			t.Fatalf("%s: BaseURL = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestMCPConfigOverrideDefaultsUndefinedReasoningEffort(t *testing.T) {
	cfg := Config{
		Provider: "openai",
		Model:    "gpt-5.4",
	}
	override := mcpServerConfigOverrides{
		Provider: "openai-codex",
		Model:    "gpt-5.5",
	}

	override.apply(&cfg)

	if cfg.ReasoningEffort != "low" {
		t.Fatalf("expected reasoning effort to default to low, got %q", cfg.ReasoningEffort)
	}
}

func TestMCPConfigOverrideSetsBypassHookTrustForInvocation(t *testing.T) {
	cfg := Config{}
	override := mcpServerConfigOverrides{BypassHookTrust: true}

	override.apply(&cfg)

	if !configBypassHookTrust(cfg) {
		t.Fatalf("expected MCP override to enable hook trust bypass")
	}
}

func TestMCPRuntimeUsesConfiguredPermissionMode(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Provider = ""
	cfg.Model = ""
	cfg.BaseURL = ""
	cfg.PermissionMode = string(ModePlan)
	cfg.SessionDir = filepath.Join(root, ".kernforge", "sessions")
	cfg.HooksEnabled = boolPtr(false)

	rt, err := newRuntimeStateForMCPServer(root, cfg, "", io.Discard)
	if err != nil {
		t.Fatalf("newRuntimeStateForMCPServer: %v", err)
	}
	defer rt.closeExtensions()

	if rt.perms == nil || rt.perms.Mode() != ModePlan {
		t.Fatalf("expected MCP runtime permission mode %q, got %#v", ModePlan, rt.perms)
	}
	if rt.workspace.Perms == nil || rt.workspace.Perms.Mode() != ModePlan {
		t.Fatalf("expected MCP workspace permission mode %q, got %#v", ModePlan, rt.workspace.Perms)
	}
}

func TestMCPVerifyExecuteRespectsConfiguredPermissionMode(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	server.rt.perms.SetMode(ModePlan)
	if err := os.WriteFile(filepath.Join(server.rt.workspace.Root, "go.mod"), []byte("module example.com/mcpverify\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_verify",
			"arguments": map[string]any{
				"execute": true,
			},
		},
	})
	if !ok {
		t.Fatalf("verify tool produced no response")
	}
	text := requireMCPTextResult(t, resp)
	if !strings.Contains(text, "Permission denied") || !strings.Contains(text, "plan mode") {
		t.Fatalf("expected plan-mode permission denial, got: %s", text)
	}
}

func TestKernforgeMCPServerUsesInitializeRootURI(t *testing.T) {
	fallbackRoot := t.TempDir()
	clientRoot := t.TempDir()
	cfg := DefaultConfig(fallbackRoot)
	cfg.Provider = ""
	cfg.Model = ""
	cfg.BaseURL = ""
	cfg.PermissionMode = string(ModeBypass)
	cfg.SessionDir = filepath.Join(fallbackRoot, ".kernforge", "sessions")
	cfg.ProjectAnalysis.OutputDir = filepath.Join(fallbackRoot, ".kernforge", "analysis")
	cfg.HooksEnabled = boolPtr(false)

	hinted, hintedSource := mcpWorkspaceHintFromMessage(map[string]any{
		"method": "initialize",
		"params": map[string]any{
			"rootUri": testMCPFileURI(clientRoot),
		},
	})
	if hinted == "" {
		t.Fatalf("expected rootUri workspace hint, source=%s uri=%s", hintedSource, testMCPFileURI(clientRoot))
	}

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"` + testMCPFileURI(clientRoot) + `"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kernforge_status","arguments":{}}}`,
	}, "\n") + "\n"
	msg, _, err := readRPCMessageFramed(bufio.NewReader(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("read test initialize frame: %v", err)
	}
	if framedHint, framedSource := mcpWorkspaceHintFromMessage(msg); framedHint == "" {
		t.Fatalf("expected framed rootUri workspace hint, source=%s msg=%#v", framedSource, msg)
	}
	var output bytes.Buffer
	if err := runKernforgeMCPServer(fallbackRoot, cfg, "", strings.NewReader(input), &output); err != nil {
		t.Fatalf("run mcp server: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two json-line responses, got %d: %q", len(lines), output.String())
	}
	text := requireMCPJSONLineText(t, lines[1])
	if !mcpStatusTextContainsPath(text, clientRoot) {
		t.Fatalf("status did not use client root %q: %s", clientRoot, text)
	}
	if mcpStatusTextContainsPath(text, fallbackRoot) {
		t.Fatalf("status unexpectedly used fallback root %q: %s", fallbackRoot, text)
	}
	if !strings.Contains(text, "initialize.params.rootUri") {
		t.Fatalf("status did not report workspace source: %s", text)
	}
}

func TestKernforgeMCPServerUsesInitializeWorkspaceFolders(t *testing.T) {
	fallbackRoot := t.TempDir()
	clientRoot := t.TempDir()
	cfg := DefaultConfig(fallbackRoot)
	cfg.Provider = ""
	cfg.Model = ""
	cfg.BaseURL = ""
	cfg.PermissionMode = string(ModeBypass)
	cfg.SessionDir = filepath.Join(fallbackRoot, ".kernforge", "sessions")
	cfg.ProjectAnalysis.OutputDir = filepath.Join(fallbackRoot, ".kernforge", "analysis")
	cfg.HooksEnabled = boolPtr(false)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"workspaceFolders":[{"uri":"` + testMCPFileURI(clientRoot) + `","name":"client"}]}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kernforge_status","arguments":{}}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := runKernforgeMCPServer(fallbackRoot, cfg, "", strings.NewReader(input), &output); err != nil {
		t.Fatalf("run mcp server: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two json-line responses, got %d: %q", len(lines), output.String())
	}
	text := requireMCPJSONLineText(t, lines[1])
	if !mcpStatusTextContainsPath(text, clientRoot) {
		t.Fatalf("status did not use workspace folder root %q: %s", clientRoot, text)
	}
	if !strings.Contains(text, "initialize.params.workspaceFolders") {
		t.Fatalf("status did not report workspace folder source: %s", text)
	}
}

func TestKernforgeMCPServerUsesToolCallWorkspaceArgument(t *testing.T) {
	fallbackRoot := t.TempDir()
	clientRoot := t.TempDir()
	cfg := DefaultConfig(fallbackRoot)
	cfg.Provider = ""
	cfg.Model = ""
	cfg.BaseURL = ""
	cfg.PermissionMode = string(ModeBypass)
	cfg.SessionDir = filepath.Join(fallbackRoot, ".kernforge", "sessions")
	cfg.ProjectAnalysis.OutputDir = filepath.Join(fallbackRoot, ".kernforge", "analysis")
	cfg.HooksEnabled = boolPtr(false)

	input := mcpTestJSONLines(t,
		map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"},
		map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "kernforge_status",
				"arguments": map[string]any{
					"workspace": clientRoot,
				},
			},
		},
	)
	var output bytes.Buffer
	if err := runKernforgeMCPServer(fallbackRoot, cfg, "", strings.NewReader(input), &output); err != nil {
		t.Fatalf("run mcp server: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two json-line responses, got %d: %q", len(lines), output.String())
	}
	text := requireMCPJSONLineText(t, lines[1])
	if !mcpStatusTextContainsPath(text, clientRoot) {
		t.Fatalf("status did not use tool workspace root %q: %s", clientRoot, text)
	}
	if mcpStatusTextContainsPath(text, fallbackRoot) {
		t.Fatalf("status unexpectedly used fallback root %q: %s", fallbackRoot, text)
	}
	if !strings.Contains(text, "tools/call.params.arguments.workspace") {
		t.Fatalf("status did not report tool workspace source: %s", text)
	}
}

func TestKernforgeMCPServerUsesToolCallMetaCwd(t *testing.T) {
	fallbackRoot := t.TempDir()
	clientRoot := t.TempDir()
	cfg := DefaultConfig(fallbackRoot)
	cfg.Provider = ""
	cfg.Model = ""
	cfg.BaseURL = ""
	cfg.PermissionMode = string(ModeBypass)
	cfg.SessionDir = filepath.Join(fallbackRoot, ".kernforge", "sessions")
	cfg.ProjectAnalysis.OutputDir = filepath.Join(fallbackRoot, ".kernforge", "analysis")
	cfg.HooksEnabled = boolPtr(false)

	input := mcpTestJSONLines(t,
		map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"},
		map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "kernforge_status",
				"_meta":     map[string]any{"cwd": clientRoot},
				"arguments": map[string]any{},
			},
		},
	)
	var output bytes.Buffer
	if err := runKernforgeMCPServer(fallbackRoot, cfg, "", strings.NewReader(input), &output); err != nil {
		t.Fatalf("run mcp server: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two json-line responses, got %d: %q", len(lines), output.String())
	}
	text := requireMCPJSONLineText(t, lines[1])
	if !mcpStatusTextContainsPath(text, clientRoot) {
		t.Fatalf("status did not use meta cwd root %q: %s", clientRoot, text)
	}
	if !strings.Contains(text, "tools/call.params._meta.cwd") {
		t.Fatalf("status did not report meta cwd source: %s", text)
	}
}

func TestKernforgeMCPServerInitializeAndListTools(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	initResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})
	if !ok {
		t.Fatalf("initialize produced no response")
	}
	initResult := requireMCPResult(t, initResp)
	if initResult["protocolVersion"] != kernforgeMCPProtocolVersion {
		t.Fatalf("unexpected protocol version: %v", initResult["protocolVersion"])
	}
	pingResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      11,
		"method":  "ping",
	})
	if !ok {
		t.Fatalf("ping produced no response")
	}
	_ = requireMCPResult(t, pingResp)

	toolsResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	if !ok {
		t.Fatalf("tools/list produced no response")
	}
	toolsResult := requireMCPResult(t, toolsResp)
	for _, name := range []string{
		"kernforge",
		"kernforge_fuzz",
		"kernforge_guide",
		"kernforge_look",
		"kernforge_review",
		"kernforge_status",
		"kernforge_verify",
		"kernforge_analyze_project",
		"kernforge_find_root_cause",
		"kernforge_memory_search",
		"kernforge_verification_history",
		"kernforge_analysis_context",
		"kernforge_artifact_index",
		"kernforge_fuzz_targets",
		"kernforge_source_scan",
		"kernforge_source_candidate_list",
		"kernforge_source_candidate_show",
		"kernforge_fuzz_workflow",
		"kernforge_fuzz_func_preview",
		"kernforge_fuzz_func_build",
		"kernforge_fuzz_artifacts",
		"kernforge_fuzz_campaign_run",
	} {
		if !mcpListContainsName(toolsResult["tools"], name) {
			t.Fatalf("tools/list did not include %s", name)
		}
	}
	fuzzTool := mcpListItemByName(toolsResult["tools"], "kernforge_fuzz")
	fuzzDescription := stringValue(fuzzTool, "description")
	for _, want := range []string{
		"PRE-CALL NARRATION",
		"say exactly one short sentence",
		"KernForge source-level fuzzing을 실행하겠습니다",
		"KernForge 도구를 확인",
		"정의와 호출부를 찾겠다",
		"테스트 구조를 보겠다",
		"퍼징 대상만 좁게 건드리겠다",
	} {
		if !strings.Contains(fuzzDescription, want) {
			t.Fatalf("kernforge_fuzz tool description missing %q: %s", want, fuzzDescription)
		}
	}

	templatesResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "resources/templates/list",
	})
	if !ok {
		t.Fatalf("resources/templates/list produced no response")
	}
	templatesResult := requireMCPResult(t, templatesResp)
	for _, name := range []string{"analysis-context-by-query", "memory-search-by-query", "function-fuzz-run-by-id", "fuzz-campaign-by-id"} {
		if !mcpListContainsName(templatesResult["resourceTemplates"], name) {
			t.Fatalf("resources/templates/list did not include %s", name)
		}
	}
	reviewTool := mcpListItemByName(toolsResult["tools"], "kernforge_review")
	schema, _ := reviewTool["inputSchema"].(map[string]any)
	properties, _ := schema["properties"].(map[string]any)
	if _, ok := properties["workspace"]; !ok {
		t.Fatalf("kernforge_review schema missing workspace hint: %#v", schema)
	}
}

func TestKernforgeMCPReviewCodeUsesMainModelAndProvidedDiff(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	client := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: "No blocking findings. Add a regression test for the new branch."},
		}},
	}
	server.rt.cfg.Provider = "openai"
	server.rt.cfg.Model = "gpt-main-review"
	server.rt.cfg.ReasoningEffort = "low"
	server.rt.cfg.MaxTokens = 1234
	server.rt.agent.Config = server.rt.cfg
	server.rt.agent.Client = client

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_review",
			"arguments": map[string]any{
				"request": "KernForge로 방금 수정한 코드를 리뷰해줘",
				"diff": strings.Join([]string{
					"diff --git a/driver.cpp b/driver.cpp",
					"@@ -1,3 +1,4 @@",
					"+if (Size > MaxSize) { return false; }",
				}, "\n"),
			},
		},
	})
	if !ok {
		t.Fatalf("review tool produced no response")
	}
	text := requireMCPTextResult(t, resp)
	for _, want := range []string{
		"KernForge review",
		"gpt-main-review",
		"provided_diff",
		"No blocking findings",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected review output to contain %q: %s", want, text)
		}
	}
	if len(client.requests) != 1 {
		t.Fatalf("expected one main model request, got %d", len(client.requests))
	}
	req := client.requests[0]
	if req.Model != "gpt-main-review" {
		t.Fatalf("review used model %q, want main model", req.Model)
	}
	if len(req.Tools) != 0 {
		t.Fatalf("review request should be read-only without tools, got %#v", req.Tools)
	}
	if req.MaxTokens != 1234 || req.ReasoningEffort != "low" {
		t.Fatalf("review request did not preserve main route settings: max=%d effort=%q", req.MaxTokens, req.ReasoningEffort)
	}
	if !strings.Contains(req.System, "structured review model") {
		t.Fatalf("review system prompt missing read-only framing: %s", req.System)
	}
	if len(req.Messages) != 1 || !strings.Contains(req.Messages[0].Text, "diff --git a/driver.cpp b/driver.cpp") {
		t.Fatalf("review prompt missing provided diff: %#v", req.Messages)
	}
}

func TestKernforgeMCPRouterRoutesReviewRequestsToMainModelReview(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	client := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: "Finding: keep the bounds check before the copy."},
		}},
	}
	server.rt.cfg.Provider = "openai"
	server.rt.cfg.Model = "gpt-main-review"
	server.rt.agent.Config = server.rt.cfg
	server.rt.agent.Client = client

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge",
			"arguments": map[string]any{
				"request": "KernForge로 코드 리뷰해줘",
				"diff":    "diff --git a/a.cpp b/a.cpp\n+memcpy(dst, src, len);",
			},
		},
	})
	if !ok {
		t.Fatalf("router produced no response")
	}
	text := requireMCPTextResult(t, resp)
	if !strings.Contains(text, "KernForge review") || !strings.Contains(text, "bounds check") {
		t.Fatalf("expected router to run code review, got: %s", text)
	}
	if len(client.requests) != 1 {
		t.Fatalf("expected router to call main model once, got %d", len(client.requests))
	}
}

func TestKernforgeMCPReviewCodeWithoutContextDoesNotCallModel(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	client := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: "should not be used"},
		}},
	}
	server.rt.cfg.Provider = "openai"
	server.rt.cfg.Model = "gpt-main-review"
	server.rt.agent.Config = server.rt.cfg
	server.rt.agent.Client = client

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_review",
			"arguments": map[string]any{
				"request": "review current changes",
			},
		},
	})
	if !ok {
		t.Fatalf("review tool produced no response")
	}
	text := requireMCPTextResult(t, resp)
	if !strings.Contains(text, "No reviewable evidence was collected") || !strings.Contains(text, "insufficient_evidence") {
		t.Fatalf("expected missing context response, got: %s", text)
	}
	if len(client.requests) != 0 {
		t.Fatalf("expected no model call without reviewable context, got %d", len(client.requests))
	}
}

func TestKernforgeMCPReviewCodeWithoutContextDoesNotRequireProvider(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_review",
			"arguments": map[string]any{
				"request": "review current changes",
			},
		},
	})
	if !ok {
		t.Fatalf("review tool produced no response")
	}
	result := requireMCPResult(t, resp)
	if isError, _ := result["isError"].(bool); isError {
		t.Fatalf("missing context should not require provider setup: %#v", result)
	}
	text := requireMCPTextResult(t, resp)
	if !strings.Contains(text, "No reviewable evidence was collected") || !strings.Contains(text, "insufficient_evidence") {
		t.Fatalf("expected missing context response, got: %s", text)
	}
}

func TestKernforgeMCPReviewCodeCollectsWorkspaceGitDiff(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	root := server.rt.workspace.Root
	runTestGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "driver.cpp"), []byte("bool Check()\n{\n    return false;\n}\n"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	runTestGit(t, root, "add", "driver.cpp")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(root, "driver.cpp"), []byte("bool Check()\n{\n    return true;\n}\n"), 0o644); err != nil {
		t.Fatalf("write modified file: %v", err)
	}

	client := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: "Finding: changed return value needs a test."},
		}},
	}
	server.rt.cfg.Provider = "openai"
	server.rt.cfg.Model = "gpt-main-review"
	server.rt.agent.Config = server.rt.cfg
	server.rt.agent.Client = client

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_review",
			"arguments": map[string]any{
				"request": "review current changes",
			},
		},
	})
	if !ok {
		t.Fatalf("review tool produced no response")
	}
	text := requireMCPTextResult(t, resp)
	if !strings.Contains(text, "git_diff") || !strings.Contains(text, "changed return value") {
		t.Fatalf("expected git diff-backed review output, got: %s", text)
	}
	if len(client.requests) != 1 {
		t.Fatalf("expected one model call, got %d", len(client.requests))
	}
	prompt := client.requests[0].Messages[0].Text
	if !strings.Contains(prompt, "Git diff excerpt") || !strings.Contains(prompt, "+    return true;") {
		t.Fatalf("review prompt did not include workspace git diff: %s", prompt)
	}
}

func TestKernforgeMCPReviewCodeCollectsGitDiffForAbsolutePath(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	root := server.rt.workspace.Root
	runTestGit(t, root, "init")
	filePath := filepath.Join(root, "driver.cpp")
	if err := os.WriteFile(filePath, []byte("bool Check()\n{\n    return false;\n}\n"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	runTestGit(t, root, "add", "driver.cpp")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")
	if err := os.WriteFile(filePath, []byte("bool Check()\n{\n    return true;\n}\n"), 0o644); err != nil {
		t.Fatalf("write modified file: %v", err)
	}

	client := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: "Finding: absolute path diff was reviewed."},
		}},
	}
	server.rt.cfg.Provider = "openai"
	server.rt.cfg.Model = "gpt-main-review"
	server.rt.agent.Config = server.rt.cfg
	server.rt.agent.Client = client

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_review",
			"arguments": map[string]any{
				"request": "review current changes",
				"paths":   []string{filePath},
			},
		},
	})
	if !ok {
		t.Fatalf("review tool produced no response")
	}
	text := requireMCPTextResult(t, resp)
	if !strings.Contains(text, "git_diff") || !strings.Contains(text, "absolute path diff was reviewed") {
		t.Fatalf("expected absolute path git diff-backed review output, got: %s", text)
	}
	if len(client.requests) != 1 {
		t.Fatalf("expected one model call, got %d", len(client.requests))
	}
	prompt := client.requests[0].Messages[0].Text
	if !strings.Contains(prompt, "Git diff excerpt") || !strings.Contains(prompt, "+    return true;") {
		t.Fatalf("review prompt did not include workspace git diff for absolute path: %s", prompt)
	}
	if strings.Contains(prompt, filePath) {
		t.Fatalf("review prompt should normalize absolute path to workspace-relative path: %s", prompt)
	}
}

func TestParseMCPReviewGitStatusPathsPreservesUnstagedPathFirstCharacter(t *testing.T) {
	status := strings.Join([]string{
		"## main",
		" M driver.cpp",
		"A  staged.cpp",
		"?? new.cpp",
	}, "\n")
	paths := parseMCPReviewGitStatusPaths(status)
	for _, want := range []string{"driver.cpp", "staged.cpp", "new.cpp"} {
		if !containsString(paths, want) {
			t.Fatalf("expected parsed status paths to contain %s, got %#v", want, paths)
		}
	}
	for _, bad := range []string{"river.cpp", "taged.cpp", "ew.cpp"} {
		if containsString(paths, bad) {
			t.Fatalf("status parser lost first path character: %#v", paths)
		}
	}
}

func TestKernforgeMCPGuideRecommendsMainModelReviewForCodeReview(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_guide",
			"arguments": map[string]any{
				"request": "KernForge로 방금 작성한 코드를 검토해줘",
			},
		},
	})
	if !ok {
		t.Fatalf("guide produced no response")
	}
	text := requireMCPTextResult(t, resp)
	for _, want := range []string{
		`"intent": "review"`,
		`"name": "kernforge_review"`,
		"common_review_harness_read_only",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected guide output to contain %q: %s", want, text)
		}
	}
}

func TestKernforgeMCPGuideDoesNotTreatEveryKoreanReviewWordAsCodeReview(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_guide",
			"arguments": map[string]any{
				"request": "KernForge로 프로젝트 구조를 검토해줘",
			},
		},
	})
	if !ok {
		t.Fatalf("guide produced no response")
	}
	text := requireMCPTextResult(t, resp)
	for _, want := range []string{
		`"intent": "analyze"`,
		`"name": "kernforge_analyze_project"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected guide output to contain %q: %s", want, text)
		}
	}
	if strings.Contains(text, "kernforge_review") {
		t.Fatalf("non-code review request should not route to code review: %s", text)
	}
}

func TestKernforgeMCPServerGuideAsksForMissingFuzzInput(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_guide",
			"arguments": map[string]any{
				"request": "fuzz this driver entry point",
			},
		},
	})
	if !ok {
		t.Fatalf("guide produced no response")
	}
	text := requireMCPTextResult(t, resp)
	for _, want := range []string{
		`"state": "needs_input"`,
		`"intent": "fuzz"`,
		`"questions":`,
		`"recommended_tool_call"`,
		`"name": "kernforge_fuzz_targets"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected guide output to contain %q: %s", want, text)
		}
	}
}

func TestKernforgeMCPServerGuideRecommendsSourceFuzzForExplicitFuzzRequest(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_guide",
			"arguments": map[string]any{
				"request": "fuzz IsValidCommand",
				"file":    "src/driver/IoctlDispatch.cpp",
			},
		},
	})
	if !ok {
		t.Fatalf("guide produced no response")
	}
	text := requireMCPTextResult(t, resp)
	for _, want := range []string{
		`"state": "ready"`,
		`"intent": "fuzz"`,
		`"safe_default": "source_only"`,
		`"stop_after_recommended_tool": true`,
		`"name": "kernforge_fuzz"`,
		`"mode": "source"`,
		`"target": ""`,
		`"file": "src/driver/IoctlDispatch.cpp"`,
		`"codex_instruction":`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected guide output to contain %q: %s", want, text)
		}
	}
	if strings.Contains(text, `"name": "kernforge_fuzz_func_preview"`) {
		t.Fatalf("explicit fuzz guide should not default to native preview: %s", text)
	}
}

func TestKernforgeMCPServerGuideBareTargetAsksUserForAction(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_guide",
			"arguments": map[string]any{
				"request": "IsValidCommand 봐줘",
				"file":    "src/driver/IoctlDispatch.cpp",
			},
		},
	})
	if !ok {
		t.Fatalf("guide produced no response")
	}
	text := requireMCPTextResult(t, resp)
	for _, want := range []string{
		`"state": "needs_input"`,
		`"intent": "target_only"`,
		`"ask_user":`,
		`"stop_after_response": true`,
		`"requires_user_choice": true`,
		`"codex_instruction":`,
		`"do_not_call_before_user_choice":`,
		`"choices":`,
		`"id": "preview"`,
		`"id": "build_only"`,
		`"id": "verify"`,
		`"name": "kernforge_fuzz_func_preview"`,
		`"query": "IsValidCommand --file src/driver/IoctlDispatch.cpp"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected guide output to contain %q: %s", want, text)
		}
	}
}

func TestKernforgeMCPServerLookAsksUserForAction(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_look",
			"arguments": map[string]any{
				"request": "KernForge로 IsValidCommand 봐줘",
				"file":    "src/driver/IoctlDispatch.cpp",
			},
		},
	})
	if !ok {
		t.Fatalf("look produced no response")
	}
	text := requireMCPTextResult(t, resp)
	for _, want := range []string{
		"KernForge look MCP",
		`"state": "needs_input"`,
		`"intent": "target_only"`,
		`"ask_user":`,
		`"stop_after_response": true`,
		`"choices":`,
		`"id": "preview"`,
		`"name": "kernforge_fuzz_func_preview"`,
		`"query": "IsValidCommand --file src/driver/IoctlDispatch.cpp"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected look output to contain %q: %s", want, text)
		}
	}
}

func TestKernforgeMCPServerDefaultRouterAsksUserForAction(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge",
			"arguments": map[string]any{
				"request": "KernForge로 IsValidCommand 봐줘",
				"file":    "src/driver/IoctlDispatch.cpp",
			},
		},
	})
	if !ok {
		t.Fatalf("router produced no response")
	}
	text := requireMCPTextResult(t, resp)
	for _, want := range []string{
		"KernForge MCP router",
		`"state": "needs_input"`,
		`"intent": "target_only"`,
		`"stop_after_response": true`,
		`"do_not_call_before_user_choice":`,
		`"id": "preview"`,
		`"id": "plan"`,
		`"id": "build_only"`,
		`"id": "verify"`,
		`"query": "IsValidCommand --file src/driver/IoctlDispatch.cpp"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected router output to contain %q: %s", want, text)
		}
	}
	if strings.Contains(text, "source_review") {
		t.Fatalf("router output should not suggest source_review before user choice: %s", text)
	}
}

func TestKernforgeMCPServerDefaultRouterResolvesPreviewChoice(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge",
			"arguments": map[string]any{
				"request": "KernForge로 IsValidCommand 봐줘",
				"file":    "src/driver/IoctlDispatch.cpp",
				"choice":  "preview",
			},
		},
	})
	if !ok {
		t.Fatalf("router produced no response")
	}
	text := requireMCPTextResult(t, resp)
	for _, want := range []string{
		`"state": "ready"`,
		`"intent": "target_choice"`,
		`"user_choice": "preview"`,
		`"recommended_tool_call":`,
		`"name": "kernforge_fuzz_func_preview"`,
		`"query": "IsValidCommand --file src/driver/IoctlDispatch.cpp"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected router choice output to contain %q: %s", want, text)
		}
	}
}

func TestKernforgeMCPServerStatusTool(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "kernforge_status",
			"arguments": map[string]any{},
		},
	})
	if !ok {
		t.Fatalf("status tool produced no response")
	}
	text := requireMCPTextResult(t, resp)
	if !strings.Contains(text, "KernForge MCP status") {
		t.Fatalf("status output missing title: %s", text)
	}
	escapedRoot := strings.ReplaceAll(server.rt.workspace.BaseRoot, "\\", "\\\\")
	if !strings.Contains(text, filepath.ToSlash(server.rt.workspace.BaseRoot)) && !strings.Contains(text, server.rt.workspace.BaseRoot) && !strings.Contains(text, escapedRoot) {
		t.Fatalf("status output missing workspace root: %s", text)
	}
}

func TestKernforgeMCPServerLatestAnalysisDocResource(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	analysisCfg := configProjectAnalysis(server.rt.cfg, server.rt.workspace.BaseRoot)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	docsDir := filepath.Join(latestDir, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "INDEX.md"), []byte("# Index\n\nTelemetry map."), 0o644); err != nil {
		t.Fatalf("write index doc: %v", err)
	}
	manifest := AnalysisDocsManifest{
		SchemaVersion:       analysisDocsManifestSchemaVersion,
		MinReaderVersion:    analysisDocsManifestMinReaderVersion,
		CompatibilityPolicy: analysisDocsManifestCompatPolicy,
		RunID:               "run-1",
		Goal:                "map telemetry surface",
		Mode:                "map",
		GeneratedAt:         time.Now().UTC(),
		DocumentCount:       1,
		Documents: []AnalysisGeneratedDoc{
			{
				Name:  "INDEX.md",
				Title: "Project Documentation Index",
				Kind:  "index",
				Path:  "docs/INDEX.md",
			},
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "docs_manifest.json"), data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "run.json"), []byte(`{"summary":{"run_id":"run-1","mode":"map"}}`), 0o644); err != nil {
		t.Fatalf("write run json: %v", err)
	}

	listResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "resources/list",
	})
	if !ok {
		t.Fatalf("resources/list produced no response")
	}
	listResult := requireMCPResult(t, listResp)
	if !mcpListContainsURI(listResult["resources"], "kernforge://analysis/latest/docs/INDEX.md") {
		t.Fatalf("resources/list did not include latest INDEX.md resource")
	}

	readResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "resources/read",
		"params": map[string]any{
			"uri": "kernforge://analysis/latest/docs/INDEX.md",
		},
	})
	if !ok {
		t.Fatalf("resources/read produced no response")
	}
	text := requireMCPResourceText(t, readResp)
	if !strings.Contains(text, "# INDEX.md") || !strings.Contains(text, "Telemetry map.") {
		t.Fatalf("unexpected resource text: %s", text)
	}

	manifestResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "resources/read",
		"params": map[string]any{
			"uri": "kernforge://analysis/latest/manifest",
		},
	})
	if !ok {
		t.Fatalf("manifest resource produced no response")
	}
	if manifestText := requireMCPResourceText(t, manifestResp); !strings.Contains(manifestText, `"run_id": "run-1"`) {
		t.Fatalf("unexpected manifest resource: %s", manifestText)
	}

	runResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "resources/read",
		"params": map[string]any{
			"uri": "kernforge://analysis/latest/run",
		},
	})
	if !ok {
		t.Fatalf("run resource produced no response")
	}
	if runText := requireMCPResourceText(t, runResp); !strings.Contains(runText, `"run_id": "run-1"`) {
		t.Fatalf("unexpected run resource: %s", runText)
	}
}

func TestKernforgeMCPServerMemoryHistoryAndArtifactResources(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	root := server.rt.workspace.BaseRoot
	if err := server.rt.longMem.Append(PersistentMemoryRecord{
		ID:                     "mem-mcp-001",
		SessionID:              server.rt.session.ID,
		Workspace:              root,
		Request:                "Verify the driver IOCTL parser and fuzz gate.",
		Reply:                  "Driver verify passed after fuzz campaign evidence review.",
		Summary:                "Driver IOCTL verification passed with confirmed fuzz evidence.",
		Importance:             PersistentMemoryHigh,
		Trust:                  PersistentMemoryConfirmed,
		VerificationCategories: []string{"driver", "fuzz"},
		VerificationTags:       []string{"ioctl", "campaign"},
		Files:                  []string{"src/guard.cpp"},
		Keywords:               []string{"driver", "ioctl", "verify", "fuzz"},
	}); err != nil {
		t.Fatalf("append memory: %v", err)
	}
	report := VerificationReport{
		GeneratedAt: time.Now().UTC(),
		Trigger:     "mcp-test",
		Mode:        VerificationAdaptive,
		Decision:    "verification passed",
		Workspace:   root,
		Steps: []VerificationStep{
			{
				Label:   "go test ./...",
				Command: "go test ./...",
				Status:  VerificationPassed,
				Tags:    []string{"unit"},
			},
		},
	}
	if err := server.rt.verifyHistory.Append(server.rt.session.ID, root, report); err != nil {
		t.Fatalf("append verification history: %v", err)
	}

	memoryResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_memory_search",
			"arguments": map[string]any{
				"query": "driver fuzz",
				"limit": float64(5),
			},
		},
	})
	if !ok {
		t.Fatalf("memory search produced no response")
	}
	if memoryText := requireMCPTextResult(t, memoryResp); !strings.Contains(memoryText, "Driver IOCTL verification") {
		t.Fatalf("unexpected memory search output: %s", memoryText)
	}

	historyResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_verification_history",
			"arguments": map[string]any{
				"limit": float64(5),
			},
		},
	})
	if !ok {
		t.Fatalf("verification history produced no response")
	}
	if historyText := requireMCPTextResult(t, historyResp); !strings.Contains(historyText, "Reports: total=1") || !strings.Contains(historyText, "verification passed") {
		t.Fatalf("unexpected verification history output: %s", historyText)
	}

	artifactResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "kernforge_artifact_index",
			"arguments": map[string]any{},
		},
	})
	if !ok {
		t.Fatalf("artifact index produced no response")
	}
	if artifactText := requireMCPTextResult(t, artifactResp); !strings.Contains(artifactText, `"persistent_memory"`) || !strings.Contains(artifactText, `"verification_history"`) {
		t.Fatalf("unexpected artifact index output: %s", artifactText)
	}

	for _, uri := range []string{
		"kernforge://memory/search/driver%20fuzz",
		"kernforge://verification/history",
		"kernforge://artifacts/index",
	} {
		resp, ok := server.handleMessage(context.Background(), map[string]any{
			"jsonrpc": "2.0",
			"id":      10,
			"method":  "resources/read",
			"params": map[string]any{
				"uri": uri,
			},
		})
		if !ok {
			t.Fatalf("resources/read produced no response for %s", uri)
		}
		text := requireMCPResourceText(t, resp)
		if strings.TrimSpace(text) == "" {
			t.Fatalf("empty resource text for %s", uri)
		}
	}
}

func TestKernforgeMCPServerFuzzTargetsFromLatestManifest(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	analysisCfg := configProjectAnalysis(server.rt.cfg, server.rt.workspace.BaseRoot)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis: %v", err)
	}
	manifest := AnalysisDocsManifest{
		SchemaVersion:       analysisDocsManifestSchemaVersion,
		MinReaderVersion:    analysisDocsManifestMinReaderVersion,
		CompatibilityPolicy: analysisDocsManifestCompatPolicy,
		RunID:               "run-fuzz",
		Goal:                "map fuzz targets",
		FuzzTargets: []AnalysisFuzzTargetCatalogEntry{
			{
				Name:             "ValidateRequest",
				File:             "src/guard.cpp",
				SymbolID:         "func:ValidateRequest",
				SourceAnchor:     "src/guard.cpp:10",
				PriorityScore:    91,
				PriorityReasons:  []string{"input-facing parser"},
				SuggestedCommand: "/fuzz-func ValidateRequest --file src/guard.cpp",
			},
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "docs_manifest.json"), data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_fuzz_targets",
			"arguments": map[string]any{
				"query": "parser",
				"limit": float64(5),
			},
		},
	})
	if !ok {
		t.Fatalf("fuzz targets produced no response")
	}
	text := requireMCPTextResult(t, resp)
	if !strings.Contains(text, "ValidateRequest") || !strings.Contains(text, "/fuzz-func ValidateRequest") {
		t.Fatalf("unexpected fuzz target output: %s", text)
	}

	listResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "resources/list",
	})
	if !ok {
		t.Fatalf("resources/list produced no response")
	}
	listResult := requireMCPResult(t, listResp)
	if !mcpListContainsURI(listResult["resources"], "kernforge://fuzz/targets") {
		t.Fatalf("resources/list did not include fuzz targets resource")
	}
}

func TestKernforgeMCPServerSourceCandidateToolsReturnStructuredHandoff(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	root := workspaceSnapshotRoot(server.rt.workspace)
	candidate := SourceCandidateRecord{
		ID:           "sc-mcp",
		Workspace:    root,
		Status:       "pending",
		MatcherSlug:  "probe-copy-size-drift",
		NoiseTier:    "precise",
		File:         "driver/dispatch.cpp",
		SymbolName:   "DispatchIoctl",
		SourceAnchor: "driver/dispatch.cpp:42",
		Score:        91,
		ConfidenceBreakdown: map[string]int{
			"matcher_base":   86,
			"dataflow_facts": 6,
		},
		EvidenceSpans: []SourceCandidateEvidenceSpan{{
			Kind:      "matcher_focus",
			File:      "driver/dispatch.cpp",
			StartLine: 42,
			EndLine:   42,
			Text:      "RtlCopyMemory(g_Buffer, UserBuffer, Size);",
		}},
		DataflowFacts: []SourceCandidateFact{{
			Kind:   "probe_to_copy",
			Line:   42,
			Detail: "matched window contains both user-buffer probe and memory-copy sink",
		}},
	}
	if _, err := server.rt.sourceScan.UpsertCandidate(candidate); err != nil {
		t.Fatalf("upsert candidate: %v", err)
	}
	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      21,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_source_candidate_show",
			"arguments": map[string]any{
				"id": "sc-mcp",
			},
		},
	})
	if !ok {
		t.Fatalf("source candidate show produced no response")
	}
	text := requireMCPTextResult(t, resp)
	for _, want := range []string{
		`"candidate_id": "sc-mcp"`,
		`"confidence_breakdown"`,
		`"next_command": "/fuzz-func --from-candidate sc-mcp"`,
		`"next_tool_call"`,
		"probe_to_copy",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("source candidate payload missing %q: %s", want, text)
		}
	}
}

func TestKernforgeMCPServerFuzzFuncAndCampaignRun(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	srcDir := filepath.Join(server.rt.workspace.BaseRoot, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	source := strings.Join([]string{
		"#include <cstddef>",
		"#include <cstdint>",
		"",
		"static bool ParseHeader(const uint8_t* data, size_t size)",
		"{",
		"    return data != nullptr && size > 0;",
		"}",
		"",
		"bool ValidateRequest(const uint8_t* data, size_t size, uint32_t flags)",
		"{",
		"    if (!ParseHeader(data, size))",
		"    {",
		"        return false;",
		"    }",
		"    return flags != 0;",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(srcDir, "guard.cpp"), []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	fuzzResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_fuzz_func",
			"arguments": map[string]any{
				"query":     "ValidateRequest",
				"execute":   false,
				"max_chars": float64(50000),
			},
		},
	})
	if !ok {
		t.Fatalf("fuzz func produced no response")
	}
	fuzzText := requireMCPTextResult(t, fuzzResp)
	if !strings.Contains(fuzzText, "Source-level fuzz planning completed") || !strings.Contains(fuzzText, "ValidateRequest") {
		t.Fatalf("unexpected fuzz func output: %s", fuzzText)
	}
	runs, err := server.rt.functionFuzz.ListRecent(server.rt.workspace.BaseRoot, 1)
	if err != nil {
		t.Fatalf("list fuzz runs: %v", err)
	}
	if len(runs) != 1 || runs[0].TargetSymbolName != "ValidateRequest" {
		t.Fatalf("expected stored ValidateRequest fuzz run, got %#v", runs)
	}

	planResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_fuzz_campaign_run",
			"arguments": map[string]any{
				"execute": false,
			},
		},
	})
	if !ok {
		t.Fatalf("fuzz campaign plan produced no response")
	}
	if planText := requireMCPTextResult(t, planResp); !strings.Contains(planText, "KernForge fuzz campaign plan") {
		t.Fatalf("unexpected campaign plan output: %s", planText)
	}

	runResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_fuzz_campaign_run",
			"arguments": map[string]any{
				"execute": true,
				"name":    "mcp campaign",
			},
		},
	})
	if !ok {
		t.Fatalf("fuzz campaign run produced no response")
	}
	runText := requireMCPTextResult(t, runResp)
	if !strings.Contains(runText, "Fuzz Campaign") || !strings.Contains(runText, "ValidateRequest") {
		t.Fatalf("unexpected campaign run output: %s", runText)
	}
	campaigns, err := server.rt.fuzzCampaigns.ListRecent(server.rt.workspace.BaseRoot, 1)
	if err != nil {
		t.Fatalf("list campaigns: %v", err)
	}
	if len(campaigns) != 1 || len(campaigns[0].FunctionRuns) == 0 || len(campaigns[0].SeedArtifacts) == 0 {
		t.Fatalf("expected campaign with attached run and promoted seed artifacts, got %#v", campaigns)
	}
}

func TestKernforgeMCPServerFuzzFuncExecutePendingSummarizesNativePlan(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	root := server.rt.workspace.BaseRoot
	srcDir := filepath.Join(root, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	source := strings.Join([]string{
		"#include <cstddef>",
		"#include <cstdint>",
		"",
		"bool ValidateRequest(const uint8_t* data, size_t size, uint32_t flags)",
		"{",
		"    if (data == nullptr || size == 0)",
		"    {",
		"        return false;",
		"    }",
		"    return flags != 0;",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(srcDir, "guard.cpp"), []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	fakeCompiler := filepath.Join(root, "toolchain", "clang-cl.exe")
	if err := os.MkdirAll(filepath.Dir(fakeCompiler), 0o755); err != nil {
		t.Fatalf("mkdir toolchain: %v", err)
	}
	if err := os.WriteFile(fakeCompiler, []byte("fake compiler marker"), 0o644); err != nil {
		t.Fatalf("write fake compiler: %v", err)
	}

	analysisCfg := configProjectAnalysis(server.rt.cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis: %v", err)
	}
	pack := KnowledgePack{
		RunID:          "run-mcp-fuzz-native-plan",
		Goal:           "recover native fuzz build context",
		Root:           root,
		GeneratedAt:    time.Now(),
		ProjectSummary: "Guard runtime validates request payloads before dispatch.",
	}
	packData, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), packData, 0o644); err != nil {
		t.Fatalf("write knowledge pack: %v", err)
	}
	index := SemanticIndexV2{
		RunID:       "run-mcp-fuzz-native-plan",
		Goal:        "recover native fuzz build context",
		Root:        root,
		GeneratedAt: time.Now(),
		BuildContexts: []BuildContextRecord{
			{
				ID:           "buildctx:compile:guard",
				Name:         "Guard compile context",
				Kind:         "compile_context",
				Directory:    root,
				Files:        []string{"src/guard.cpp"},
				Compiler:     fakeCompiler,
				IncludePaths: []string{"include"},
				Defines:      []string{"GUARD_MODE=1"},
			},
		},
		Symbols: []SymbolRecord{
			{
				ID:             "func:ValidateRequest@src/guard.cpp",
				Name:           "ValidateRequest",
				Kind:           "function",
				Language:       "cpp",
				File:           "src/guard.cpp",
				BuildContextID: "buildctx:compile:guard",
				Signature:      "bool ValidateRequest(const uint8_t* data, size_t size, uint32_t flags)",
				StartLine:      4,
				EndLine:        11,
			},
		},
	}
	indexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatalf("marshal structural index v2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "structural_index_v2.json"), indexData, 0o644); err != nil {
		t.Fatalf("write structural index v2: %v", err)
	}

	previewResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_fuzz_func_preview",
			"arguments": map[string]any{
				"query":     "ValidateRequest",
				"max_chars": float64(50000),
			},
		},
	})
	if !ok {
		t.Fatalf("fuzz func preview produced no response")
	}
	previewText := requireMCPTextResult(t, previewResp)
	previewWants := []string{
		"KernForge function fuzz native execution preview",
		`"preview_only": true`,
		`"history_saved": false`,
		`"artifacts_written": true`,
		`"continue_command_available": false`,
		`"native_execution_started": false`,
		`"auto_exec": "pending_confirmation"`,
		`"build_argv":`,
		`"run_argv":`,
		`"build_command":`,
		`"missing_settings":`,
	}
	for _, want := range previewWants {
		if !strings.Contains(previewText, want) {
			t.Fatalf("expected fuzz func preview output to contain %q: %s", want, previewText)
		}
	}
	if runs, err := server.rt.functionFuzz.ListRecent(root, 1); err != nil {
		t.Fatalf("list preview fuzz runs: %v", err)
	} else if len(runs) != 0 {
		t.Fatalf("preview should not save function fuzz history, got %#v", runs)
	}

	aliasResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      12,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_fuzz",
			"arguments": map[string]any{
				"request":           "KernForge로 ValidateRequest 퍼징해줘",
				"file":              "src/guard.cpp",
				"include_artifacts": true,
				"max_chars":         float64(12000),
			},
		},
	})
	if !ok {
		t.Fatalf("fuzz alias produced no response")
	}
	aliasText := requireMCPTextResult(t, aliasResp)
	for _, want := range []string{
		"KernForge source-level fuzz plan",
		"User-facing result",
		"Use this section first",
		"Source fuzz summary",
		`"source_only": true`,
		`"compile_required": false`,
		`"native_build_requested": false`,
		`"native_execution_requested": false`,
		`"native_execution_started": false`,
		`"artifacts_available": true`,
		`"artifacts_included": true`,
		`"artifact_paths_included": true`,
		`"artifact_content_included": false`,
		`"must_report_artifact_paths": true`,
		`"artifact_paths":`,
		`"artifact_reader_tool":`,
		`"result": "source_level_fuzz_completed"`,
		`"bottom_line":`,
		`"fuzz_result":`,
		`"meaningful_result": true`,
		`"meaningful_result_kind": "source_predicted_issue"`,
		`"meaningful_result_summary":`,
		`"meaningful_results":`,
		`"must_report_problem_code_and_trigger_values": true`,
		`"problem_code":`,
		`"problem_code_location":`,
		`"problem_code_summary":`,
		`"trigger_values":`,
		`"trigger_values_summary":`,
		`"result_kind": "source_predicted_issue"`,
		`"confirmed_runtime_finding": false`,
		`"predicted_source_finding_count":`,
		`"confirmed_runtime_bug": false`,
		`"runtime_crash_observed": false`,
		`"sanitizer_finding_observed": false`,
		`"finding_count":`,
		`"top_finding":`,
		`"what_this_means":`,
		`"default_next_step": "report_result_and_offer_native_runtime_fuzzing"`,
		`"recommended_followup":`,
		`"must_not_auto_call_next_steps":`,
		`"stop_after_response": true`,
		`"do_not_call_after_response":`,
		`"target": "ValidateRequest"`,
		`"target_file": "src/guard.cpp"`,
		"Conclusion",
		"Meaningful result: yes",
		"Problem evidence",
		"Trigger values / conditions",
		"Code location:",
		"Confirmed crash/sanitizer finding: no",
		"Highlighted result labels",
		`🟧 **Result**`,
		`🟨 **Top candidate**`,
		`🟧 **Problem location**`,
		`🟨 **Trigger conditions KernForge generated**`,
		`🟧 **Artifacts**`,
		"Artifact paths to report",
		"artifact_dir:",
		"report_path:",
		"plan_path:",
		"harness_path:",
		"Assistant contract",
		"Use the Markdown-safe orange/yellow square labels",
		"Do not use raw HTML spans",
		"Recommended next path",
		"Optional native/runtime follow-up",
		"Recommendation is allowed",
		"Do not create custom harnesses or run ad hoc fuzz/semantic scripts",
		"Do not add a source-code modification/no-modification note",
		"Artifact paths",
		"Artifact paths are included for user inspection",
		"Always include all artifact paths",
		"The final answer is incomplete if it lists only artifact_dir",
		"For future KernForge-prefixed user requests",
		"Do not preface source-only fuzz answers with tool availability checks",
		"Avoid Korean preamble phrases",
		"KernForge 도구를 확인",
		"정의와 호출부를 찾겠다",
	} {
		if !strings.Contains(aliasText, want) {
			t.Fatalf("expected fuzz alias output to contain %q: %s", want, aliasText)
		}
	}
	if detailsIndex := strings.Index(aliasText, "Problem evidence"); detailsIndex < 0 {
		t.Fatalf("expected problem evidence before JSON summary: %s", aliasText)
	} else if summaryIndex := strings.Index(aliasText, "Source fuzz summary"); summaryIndex < 0 || summaryIndex < detailsIndex {
		t.Fatalf("expected human-readable details before source fuzz JSON summary: %s", aliasText)
	}
	for _, notWant := range []string{`"build_command":`, `"run_command":`, `"build_argv":`, `"run_argv":`, "auto_exec", "Campaign handoff", "Continue: /fuzz-campaign"} {
		if strings.Contains(aliasText, notWant) {
			t.Fatalf("source-level fuzz alias should not include native build field %q: %s", notWant, aliasText)
		}
	}
	if strings.Contains(aliasText, "<span") || strings.Contains(aliasText, "</span>") {
		t.Fatalf("source-level fuzz alias should not emit raw HTML spans: %s", aliasText)
	}

	artifactReadResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      18,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_fuzz_artifacts",
			"arguments": map[string]any{
				"id":        "latest",
				"artifact":  "report",
				"max_chars": float64(50000),
			},
		},
	})
	if !ok {
		t.Fatalf("fuzz artifact read produced no response")
	}
	artifactReadText := requireMCPTextResult(t, artifactReadResp)
	for _, want := range []string{
		"KernForge function fuzz artifacts",
		`"source_only_artifacts": true`,
		`"artifact": "report"`,
		`"report_path":`,
		"report.md",
		"# Function Fuzz Plan",
		"Reading artifacts never builds or runs native fuzzing",
	} {
		if !strings.Contains(artifactReadText, want) {
			t.Fatalf("expected fuzz artifact output to contain %q: %s", want, artifactReadText)
		}
	}
	for _, notWant := range []string{`"build_argv":`, `"run_argv":`, "KernForge function fuzz native execution preview"} {
		if strings.Contains(artifactReadText, notWant) {
			t.Fatalf("fuzz artifact read should not include native preview/build field %q: %s", notWant, artifactReadText)
		}
	}

	routerResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      17,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge",
			"arguments": map[string]any{
				"request":   "KernForge로 ValidateRequest 퍼징해줘",
				"file":      "src/guard.cpp",
				"max_chars": float64(50000),
			},
		},
	})
	if !ok {
		t.Fatalf("router explicit fuzz produced no response")
	}
	routerText := requireMCPTextResult(t, routerResp)
	for _, want := range []string{
		"KernForge MCP router source-level fuzz result",
		`"source_only": true`,
		`"compile_required": false`,
		`"native_execution_started": false`,
		`"stop_after_response": true`,
		`"fuzz_result":`,
		`"problem_code":`,
		`"problem_code_location":`,
		`"trigger_values":`,
		`"trigger_values_summary":`,
		`"artifact_dir":`,
		`"report_path":`,
		"Meaningful result: yes",
		"Problem evidence",
		"Do not create custom harnesses or run ad hoc fuzz/semantic scripts",
	} {
		if !strings.Contains(routerText, want) {
			t.Fatalf("expected router explicit fuzz output to contain %q: %s", want, routerText)
		}
	}
	for _, notWant := range []string{`"build_command":`, `"run_command":`, `"build_argv":`, `"run_argv":`, "KernForge function fuzz native execution preview"} {
		if strings.Contains(routerText, notWant) {
			t.Fatalf("router explicit fuzz should not include native preview/build field %q: %s", notWant, routerText)
		}
	}

	artifactResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      16,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_fuzz",
			"arguments": map[string]any{
				"request":           "KernForge로 ValidateRequest 퍼징해줘. 산출물 경로도 보여줘",
				"file":              "src/guard.cpp",
				"include_artifacts": true,
				"max_chars":         float64(50000),
			},
		},
	})
	if !ok {
		t.Fatalf("fuzz alias artifact output produced no response")
	}
	artifactText := requireMCPTextResult(t, artifactResp)
	for _, want := range []string{
		`"artifacts_included": true`,
		`"artifact_dir":`,
		`"plan_path":`,
		`"report_path":`,
		`"harness_path":`,
		`"artifact_paths":`,
	} {
		if !strings.Contains(artifactText, want) {
			t.Fatalf("expected artifact output to contain %q: %s", want, artifactText)
		}
	}

	retryResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      13,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_fuzz",
			"arguments": map[string]any{
				"request":   "KernForge로 ValidateRequest 퍼징해줘",
				"file":      "src/wrong.cpp",
				"max_chars": float64(50000),
			},
		},
	})
	if !ok {
		t.Fatalf("fuzz alias retry produced no response")
	}
	retryText := requireMCPTextResult(t, retryResp)
	for _, want := range []string{
		"retried by target symbol only",
		"KernForge source-level fuzz plan",
		`"source_only": true`,
		`"compile_required": false`,
		`"artifacts_included": true`,
		`"stop_after_response": true`,
		`"target": "ValidateRequest"`,
		`"target_file": "src/guard.cpp"`,
	} {
		if !strings.Contains(retryText, want) {
			t.Fatalf("expected fuzz alias retry output to contain %q: %s", want, retryText)
		}
	}
	for _, notWant := range []string{`"build_command":`, `"run_command":`, `"build_argv":`, `"run_argv":`, "auto_exec", "Campaign handoff", "Continue: /fuzz-campaign"} {
		if strings.Contains(retryText, notWant) {
			t.Fatalf("source-level fuzz retry should not include native build field %q: %s", notWant, retryText)
		}
	}

	nativePreviewResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      14,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_fuzz",
			"arguments": map[string]any{
				"request":   "KernForge로 ValidateRequest 퍼징해줘",
				"file":      "src/guard.cpp",
				"mode":      "native_preview",
				"max_chars": float64(50000),
			},
		},
	})
	if !ok {
		t.Fatalf("fuzz alias native preview produced no response")
	}
	nativePreviewText := requireMCPTextResult(t, nativePreviewResp)
	for _, want := range []string{
		"KernForge function fuzz native execution preview",
		`"preview_only": true`,
		`"build_command":`,
		`"run_command":`,
		`"target": "ValidateRequest"`,
		`"target_file": "src/guard.cpp"`,
	} {
		if !strings.Contains(nativePreviewText, want) {
			t.Fatalf("expected fuzz alias native preview output to contain %q: %s", want, nativePreviewText)
		}
	}

	executeAliasResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      15,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_fuzz",
			"arguments": map[string]any{
				"request":   "KernForge로 ValidateRequest 퍼징해줘",
				"file":      "src/guard.cpp",
				"mode":      "execute",
				"max_chars": float64(50000),
			},
		},
	})
	if !ok {
		t.Fatalf("fuzz alias execute guard produced no response")
	}
	executeAliasText := requireMCPTextResult(t, executeAliasResp)
	for _, want := range []string{
		`"state": "needs_explicit_native_tool"`,
		`"requested_mode": "execute"`,
		`"compile_required": false`,
		`"native_execution": false`,
		`"mode": "source"`,
	} {
		if !strings.Contains(executeAliasText, want) {
			t.Fatalf("expected fuzz alias execute guard output to contain %q: %s", want, executeAliasText)
		}
	}
	if strings.Contains(executeAliasText, `"approve_recovered_build": true`) {
		t.Fatalf("fuzz alias execute guard should not approve native execution: %s", executeAliasText)
	}

	buildResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_fuzz_func_build",
			"arguments": map[string]any{
				"query":                   "ValidateRequest",
				"approve_recovered_build": false,
				"max_chars":               float64(50000),
			},
		},
	})
	if !ok {
		t.Fatalf("fuzz func build produced no response")
	}
	buildText := requireMCPTextResult(t, buildResp)
	for _, want := range []string{
		"KernForge function fuzz build-only",
		`"build_only": true`,
		`"build_attempted": false`,
		`"history_saved": true`,
		`"native_execution_started": false`,
		`"auto_exec": "pending_confirmation"`,
	} {
		if !strings.Contains(buildText, want) {
			t.Fatalf("expected fuzz func build output to contain %q: %s", want, buildText)
		}
	}

	fuzzResp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_fuzz_func",
			"arguments": map[string]any{
				"query":                   "ValidateRequest",
				"execute":                 true,
				"approve_recovered_build": false,
				"max_chars":               float64(50000),
			},
		},
	})
	if !ok {
		t.Fatalf("fuzz func produced no response")
	}
	fuzzText := requireMCPTextResult(t, fuzzResp)
	wants := []string{
		"Native execution was not started because recovered build settings need explicit approval",
		"Execution summary",
		`"requested_execute": true`,
		`"approve_recovered_build": false`,
		`"auto_exec": "pending_confirmation"`,
		`"requires_approval": true`,
		`"native_execution_started": false`,
		`"build_argv":`,
		`"run_argv":`,
		`"build_command":`,
		`"missing_settings":`,
		"clang-cl.exe",
	}
	for _, want := range wants {
		if !strings.Contains(fuzzText, want) {
			t.Fatalf("expected fuzz func output to contain %q: %s", want, fuzzText)
		}
	}
	if strings.Contains(fuzzText, "because execute=false") {
		t.Fatalf("execute=true pending output should not claim execute=false: %s", fuzzText)
	}
}

func TestKernforgeMCPServerFuzzFuncReportsSourceScanContext(t *testing.T) {
	server, cleanup := newTestKernforgeMCPServer(t)
	defer cleanup()

	root := server.rt.workspace.BaseRoot
	srcDir := filepath.Join(root, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "guard.cpp"), []byte("bool ValidateRequest(const uint8_t* data, size_t size) { return data != nullptr && size > 0; }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	index := SemanticIndexV2{
		RunID: "run-mcp-source-scan-context",
		Goal:  "mcp source scan context",
		Files: []FileRecord{
			{Path: "src/guard.cpp", Extension: ".cpp", Language: "cpp"},
		},
		Symbols: []SymbolRecord{
			{
				ID:        "func:ValidateRequest@src/guard.cpp",
				Name:      "ValidateRequest",
				Kind:      "function",
				Language:  "cpp",
				File:      "src/guard.cpp",
				Signature: "bool ValidateRequest(const uint8_t* data, size_t size)",
				StartLine: 1,
				EndLine:   1,
			},
		},
	}
	writeFunctionFuzzTestAnalysisArtifacts(t, root, index.RunID, index)
	candidate := SourceCandidateRecord{
		ID:          "sc-mcp-existing",
		Workspace:   root,
		RunID:       "source-scan-mcp-existing",
		Status:      "pending",
		MatcherSlug: "size-contract-drift",
		NoiseTier:   "normal",
		File:        "src/guard.cpp",
		SymbolID:    "func:ValidateRequest@src/guard.cpp",
		SymbolName:  "ValidateRequest",
		Score:       88,
	}
	if _, err := server.rt.sourceScan.UpsertCandidate(candidate); err != nil {
		t.Fatalf("upsert candidate: %v", err)
	}

	resp, ok := server.handleMessage(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kernforge_fuzz_func",
			"arguments": map[string]any{
				"query":       "ValidateRequest",
				"execute":     false,
				"source_scan": "focused",
				"max_chars":   float64(50000),
			},
		},
	})
	if !ok {
		t.Fatalf("fuzz func produced no response")
	}
	text := requireMCPTextResult(t, resp)
	for _, want := range []string{
		`"source_candidate_id": "sc-mcp-existing"`,
		`"source_matcher_slug": "size-contract-drift"`,
		`"source_scan_mode": "focused"`,
		"Reused source candidate sc-mcp-existing",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected MCP fuzz output to contain %q: %s", want, text)
		}
	}
	runs, err := server.rt.functionFuzz.ListRecent(root, 1)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].SourceCandidateID != "sc-mcp-existing" {
		t.Fatalf("expected stored run to keep source candidate context, got %#v", runs)
	}
	linked, ok, err := server.rt.sourceScan.GetCandidate("sc-mcp-existing")
	if err != nil || !ok {
		t.Fatalf("get linked candidate: ok=%v err=%v", ok, err)
	}
	if !containsString(linked.LinkedFuzzRunIDs, runs[0].ID) {
		t.Fatalf("expected MCP fuzz run link, got %#v", linked.LinkedFuzzRunIDs)
	}
}

func TestMCPFunctionFuzzResultSummaryReportsMeaningfulAndEmptyResults(t *testing.T) {
	empty := mcpFunctionFuzzResultSummary(FunctionFuzzRun{}, true)
	if boolValue(empty, "meaningful_result", true) {
		t.Fatalf("expected empty source-only result to be non-meaningful: %#v", empty)
	}
	if got := stringValue(empty, "result_kind"); got != "no_source_issue_predicted" {
		t.Fatalf("unexpected empty result kind: %q in %#v", got, empty)
	}
	if !strings.Contains(stringValue(empty, "summary"), "No meaningful source-level issue scenario") {
		t.Fatalf("expected empty result summary to say no meaningful result: %#v", empty)
	}

	predicted := mcpFunctionFuzzResultSummary(FunctionFuzzRun{
		TargetSymbolName: "ValidateRequest",
		TargetFile:       "src/guard.cpp",
		TargetStartLine:  10,
		VirtualScenarios: []FunctionFuzzVirtualScenario{{
			Title:          "State divergence",
			RiskScore:      42,
			ConcreteInputs: []string{"size = 0xffffffff"},
			LikelyIssues:   []string{"validation and sink disagree"},
			SourceExcerpt: FunctionFuzzSourceExcerpt{
				File:      "src/guard.cpp",
				StartLine: 10,
				FocusLine: 12,
				EndLine:   13,
				Snippet:   []string{"if (size > limit)", "copy(data, size);"},
			},
		}},
	}, true)
	if !boolValue(predicted, "meaningful_result", false) {
		t.Fatalf("expected predicted source result to be meaningful: %#v", predicted)
	}
	if got := stringValue(predicted, "result_kind"); got != "source_predicted_issue" {
		t.Fatalf("unexpected predicted result kind: %q in %#v", got, predicted)
	}
	if !boolValue(predicted, "must_report_problem_code_and_trigger_values", false) {
		t.Fatalf("expected predicted result to require problem code and trigger values: %#v", predicted)
	}
	if _, ok := predicted["problem_code"].(map[string]any); !ok {
		t.Fatalf("expected predicted result to expose problem_code: %#v", predicted)
	}
	if got := stringValue(predicted, "problem_code_location"); got != "src/guard.cpp:12" {
		t.Fatalf("expected predicted result to expose problem code location, got %q in %#v", got, predicted)
	}
	values, ok := predicted["trigger_values"].([]string)
	if !ok || len(values) == 0 || values[0] != "size = 0xffffffff" {
		t.Fatalf("expected predicted result to expose trigger values: %#v", predicted)
	}
	if got := stringValue(predicted, "trigger_values_summary"); !strings.Contains(got, "size = 0xffffffff") {
		t.Fatalf("expected predicted result to expose trigger summary, got %q in %#v", got, predicted)
	}

	crash := mcpFunctionFuzzResultSummary(FunctionFuzzRun{
		TargetSymbolName: "ValidateRequest",
		TargetFile:       "src/guard.cpp",
		TargetStartLine:  10,
		Execution: FunctionFuzzExecution{
			Status:     "completed",
			CrashCount: 2,
			CrashDir:   "crashes",
		},
	}, false)
	if !boolValue(crash, "confirmed_runtime_finding", false) {
		t.Fatalf("expected crash result to be confirmed: %#v", crash)
	}
	if got := stringValue(crash, "result_kind"); got != "confirmed_runtime_finding" {
		t.Fatalf("unexpected crash result kind: %q in %#v", got, crash)
	}
}

func TestMCPFunctionFuzzProblemCodeIncludesFullShortTargetFunction(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	sourceLines := []string{
		"bool IsValidCommand(PIOCTL_COMMAND_HEADER commandData)",
		"{",
		"    bool isValid = false;",
		"",
		"    do",
		"    {",
		"        if (commandData == nullptr ||",
		"            commandData->totalSize < sizeof(IOCTL_COMMAND_HEADER) ||",
		"            commandData->command == (ULONG)CommandId::Unknown ||",
		"            commandData->command >= (ULONG)CommandId::Max)",
		"        {",
		"            break;",
		"        }",
		"",
		"        isValid = true;",
		"    }",
		"    while (false);",
		"",
		"    return isValid;",
		"}",
	}
	if err := os.WriteFile(filepath.Join(srcDir, "core.cpp"), []byte(strings.Join(sourceLines, "\n")), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	run := FunctionFuzzRun{
		Workspace:        root,
		TargetSymbolName: "IsValidCommand",
		TargetFile:       "src/core.cpp",
		TargetStartLine:  1,
		TargetEndLine:    len(sourceLines),
	}
	item := FunctionFuzzVirtualScenario{
		Title: "State divergence",
		SourceExcerpt: FunctionFuzzSourceExcerpt{
			File:      "src/core.cpp",
			StartLine: 1,
			FocusLine: 1,
			EndLine:   12,
			Snippet:   append([]string{}, sourceLines[:12]...),
		},
	}

	code := mcpFunctionFuzzProblemCodeForScenario(run, item)
	snippet := strings.Join(mcpFunctionFuzzProblemCodeSnippet(code), "\n")
	if !strings.Contains(snippet, "return isValid;") {
		t.Fatalf("expected full short target function to include return, got:\n%s", snippet)
	}
	if !strings.HasSuffix(strings.TrimSpace(snippet), "}") {
		t.Fatalf("expected full short target function to include closing brace, got:\n%s", snippet)
	}
	if got := intValue(code, "end_line", 0); got != len(sourceLines) {
		t.Fatalf("expected expanded problem code end line %d, got %d in %#v", len(sourceLines), got, code)
	}
}

func TestMCPFunctionFuzzTriggerValuesIncludeCommandDataFields(t *testing.T) {
	values := mcpFunctionFuzzTriggerValuesForScenario(FunctionFuzzVirtualScenario{
		Inputs: []string{"vary commandData across edge cases"},
		SourceExcerpt: FunctionFuzzSourceExcerpt{
			Snippet: []string{
				"if (commandData == nullptr ||",
				"    commandData->totalSize < sizeof(IOCTL_COMMAND_HEADER) ||",
				"    commandData->command == (ULONG)CommandId::Unknown ||",
				"    commandData->command >= (ULONG)CommandId::Max)",
			},
		},
	})
	joined := strings.Join(values, "\n")
	for _, want := range []string{
		"commandData.totalSize = sizeof(IOCTL_COMMAND_HEADER), commandData.command = first valid command requiring a larger command-specific payload",
		"commandData.totalSize = sizeof(IOCTL_COMMAND_HEADER) + 1, commandData.command = valid command with payload shorter than its concrete struct",
		"commandData.totalSize = oversized value, commandData.command = valid/reserved command below CommandId::Max",
		"commandData.command = reserved command below CommandId::Max",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected trigger values to contain %q, got %#v", want, values)
		}
	}
	if strings.Contains(joined, "size/length field") {
		t.Fatalf("commandData-specific trigger values should not be diluted by generic size/length examples: %#v", values)
	}
}

func TestMCPSourceFuzzMaxCharsKeepsHumanResultVisible(t *testing.T) {
	if got := mcpSourceFuzzMaxChars(map[string]any{"max_chars": float64(12000)}, 40000); got != 30000 {
		t.Fatalf("expected source fuzz max chars floor to raise 12000 to 30000, got %d", got)
	}
	if got := mcpSourceFuzzMaxChars(map[string]any{"max_chars": float64(50000)}, 40000); got != 50000 {
		t.Fatalf("expected source fuzz max chars to respect larger explicit limit, got %d", got)
	}
}

func newTestKernforgeMCPServer(t *testing.T) (*kernforgeMCPServer, func()) {
	t.Helper()

	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Provider = ""
	cfg.Model = ""
	cfg.BaseURL = ""
	cfg.PermissionMode = string(ModeBypass)
	cfg.SessionDir = filepath.Join(root, ".kernforge", "sessions")
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	cfg.HooksEnabled = boolPtr(false)

	rt, err := newRuntimeStateForMCPServer(root, cfg, "", io.Discard)
	if err != nil {
		t.Fatalf("new mcp runtime: %v", err)
	}
	stateDir := filepath.Join(root, ".kernforge", "test-state")
	rt.evidence = &EvidenceStore{Path: filepath.Join(stateDir, "evidence.json"), MaxEntries: 100}
	rt.verifyHistory = &VerificationHistoryStore{Path: filepath.Join(stateDir, "verification-history.json"), MaxEntries: 100}
	rt.longMem = &PersistentMemoryStore{Path: filepath.Join(stateDir, "persistent-memory.json"), MaxEntries: 100}
	rt.sourceScan = &SourceScanStore{Path: filepath.Join(stateDir, "source-scan.json"), MaxRuns: 100, MaxCandidates: 100}
	if rt.agent != nil {
		rt.agent.Evidence = rt.evidence
		rt.agent.VerifyHistory = rt.verifyHistory
		rt.agent.LongMem = rt.longMem
	}
	return newKernforgeMCPServer(rt), func() {
		rt.closeExtensions()
	}
}

func runTestGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func requireMCPResult(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	if errObj, ok := resp["error"]; ok {
		t.Fatalf("unexpected mcp error: %#v", errObj)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result: %#v", resp)
	}
	return result
}

func requireMCPTextResult(t *testing.T, resp map[string]any) string {
	t.Helper()
	result := requireMCPResult(t, resp)
	content, ok := result["content"].([]map[string]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing text content: %#v", result["content"])
	}
	text, ok := content[0]["text"].(string)
	if !ok {
		t.Fatalf("missing text field: %#v", content[0])
	}
	return text
}

func requireMCPResourceText(t *testing.T, resp map[string]any) string {
	t.Helper()
	result := requireMCPResult(t, resp)
	contents, ok := result["contents"].([]map[string]any)
	if !ok || len(contents) == 0 {
		t.Fatalf("missing resource contents: %#v", result["contents"])
	}
	text, ok := contents[0]["text"].(string)
	if !ok {
		t.Fatalf("missing text field: %#v", contents[0])
	}
	return text
}

func requireMCPJSONLineText(t *testing.T, line string) string {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("response is not json: %v: %q", err, line)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result: %#v", resp)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing content: %#v", result)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected content item: %#v", content[0])
	}
	text, ok := first["text"].(string)
	if !ok {
		t.Fatalf("missing text: %#v", first)
	}
	return text
}

func testMCPFileURI(path string) string {
	slashed := filepath.ToSlash(path)
	if strings.HasPrefix(slashed, "/") {
		return "file://" + slashed
	}
	return "file:///" + slashed
}

func mcpTestJSONLines(t *testing.T, messages ...map[string]any) string {
	t.Helper()
	var b strings.Builder
	for _, msg := range messages {
		data, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal mcp test message: %v", err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

func mcpStatusTextContainsPath(text string, path string) bool {
	escaped := strings.ReplaceAll(path, "\\", "\\\\")
	return strings.Contains(text, filepath.ToSlash(path)) ||
		strings.Contains(text, path) ||
		strings.Contains(text, escaped)
}

func mcpListContainsName(raw any, name string) bool {
	return len(mcpListItemByName(raw, name)) > 0
}

func mcpListItemByName(raw any, name string) map[string]any {
	items, ok := raw.([]map[string]any)
	if !ok {
		return nil
	}
	for _, item := range items {
		if item["name"] == name {
			return item
		}
	}
	return nil
}

func mcpListContainsURI(raw any, uri string) bool {
	items, ok := raw.([]map[string]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if item["uri"] == uri {
			return true
		}
	}
	return false
}
