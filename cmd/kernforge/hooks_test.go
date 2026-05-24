package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHookEngineMatchesGlobPatterns(t *testing.T) {
	engine := &HookEngine{
		Enabled: true,
		Rules: []HookRule{
			{
				ID:     "warn-driver-edit",
				Events: []HookEvent{HookPostEdit},
				Match: HookMatch{
					Paths: []string{"**/driver/**/*.cpp", "**/*.sys"},
				},
				Action: HookAction{
					Type:    "warn",
					Message: "driver file changed",
				},
			},
		},
	}

	verdict, err := engine.Evaluate(context.Background(), HookPostEdit, HookPayload{
		"path": "src/driver/guard.cpp",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(verdict.Warns) != 1 {
		t.Fatalf("expected one warning, got %#v", verdict.Warns)
	}
}

func TestHookEngineDenyWins(t *testing.T) {
	engine := &HookEngine{
		Enabled: true,
		Rules: []HookRule{
			{
				ID:       "warn",
				Priority: 100,
				Events:   []HookEvent{HookPreToolUse},
				Match: HookMatch{
					ToolNames: []string{"run_shell"},
				},
				Action: HookAction{Type: "warn", Message: "be careful"},
			},
			{
				ID:       "deny",
				Priority: 200,
				Events:   []HookEvent{HookPreToolUse},
				Match: HookMatch{
					ToolNames:     []string{"run_shell"},
					CommandsRegex: []string{`(?i)\bbcdedit\b`},
				},
				Action: HookAction{Type: "deny", Message: "blocked"},
			},
		},
	}

	verdict, err := engine.Evaluate(context.Background(), HookPreToolUse, HookPayload{
		"tool_name": "run_shell",
		"command":   "bcdedit /set testsigning on",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if verdict.DenyReason != "blocked" {
		t.Fatalf("unexpected deny reason: %#v", verdict)
	}
}

func TestHookRuntimeDenySkipsAskPrompt(t *testing.T) {
	askCalled := false
	runtime := &HookRuntime{
		Ask: func(string) (bool, error) {
			askCalled = true
			return true, nil
		},
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:       "ask",
					Priority: 200,
					Events:   []HookEvent{HookPreToolUse},
					Match: HookMatch{
						ToolNames: []string{"run_shell"},
					},
					Action: HookAction{Type: "ask", Message: "continue?"},
				},
				{
					ID:       "deny",
					Priority: 100,
					Events:   []HookEvent{HookPreToolUse},
					Match: HookMatch{
						ToolNames: []string{"run_shell"},
					},
					Action: HookAction{Type: "deny", Message: "blocked"},
				},
			},
		},
	}

	_, err := runtime.Run(context.Background(), HookPreToolUse, HookPayload{
		"tool_name": "run_shell",
	})
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected deny error, got %v", err)
	}
	if askCalled {
		t.Fatalf("deny verdict should skip ask prompt")
	}
}

func TestHookEnginePermissionRequestAllowAndDenyAreDecisions(t *testing.T) {
	engine := &HookEngine{
		Enabled: true,
		Rules: []HookRule{
			{
				ID:       "allow-shell",
				Priority: 100,
				Events:   []HookEvent{HookPermissionRequest},
				Match: HookMatch{
					ToolNames: []string{"run_shell"},
				},
				Action: HookAction{Type: "allow"},
			},
			{
				ID:       "deny-netsh",
				Priority: 200,
				Events:   []HookEvent{HookPermissionRequest},
				Match: HookMatch{
					ToolNames:     []string{"run_shell"},
					CommandsRegex: []string{`(?i)\bnetsh\b`},
				},
				Action: HookAction{Type: "deny", Message: "netsh requires manual approval"},
			},
		},
	}

	allowVerdict, err := engine.Evaluate(context.Background(), HookPermissionRequest, HookPayload{
		"tool_name": "run_shell",
		"command":   "echo ok",
	})
	if err != nil {
		t.Fatalf("Evaluate allow: %v", err)
	}
	if got := allowVerdict.PermissionDecision; got != "allow" {
		t.Fatalf("expected allow decision, got %#v", allowVerdict)
	}

	denyVerdict, err := engine.Evaluate(context.Background(), HookPermissionRequest, HookPayload{
		"tool_name": "run_shell",
		"command":   "netsh advfirewall show allprofiles",
	})
	if err != nil {
		t.Fatalf("Evaluate deny: %v", err)
	}
	if got := denyVerdict.PermissionDecision; got != "deny" {
		t.Fatalf("expected deny decision, got %#v", denyVerdict)
	}
	if got := denyVerdict.PermissionMessage; got != "netsh requires manual approval" {
		t.Fatalf("expected deny message, got %#v", denyVerdict)
	}
}

func TestHookRuntimePermissionRequestDenyReturnsVerdict(t *testing.T) {
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "deny",
					Events: []HookEvent{HookPermissionRequest},
					Match: HookMatch{
						ToolNames: []string{"run_shell"},
					},
					Action: HookAction{Type: "deny", Message: "blocked by policy"},
				},
			},
		},
	}

	verdict, err := runtime.Run(context.Background(), HookPermissionRequest, HookPayload{
		"tool_name": "run_shell",
		"command":   "echo denied",
	})
	if err != nil {
		t.Fatalf("PermissionRequest deny should be a decision, got error: %v", err)
	}
	if verdict.PermissionDecision != "deny" || verdict.PermissionMessage != "blocked by policy" {
		t.Fatalf("unexpected permission request verdict: %#v", verdict)
	}
}

func TestHookEngineAllowCanRewritePreToolUseInput(t *testing.T) {
	engine := &HookEngine{
		Enabled: true,
		Rules: []HookRule{
			{
				ID:     "rewrite",
				Events: []HookEvent{HookPreToolUse},
				Match: HookMatch{
					ToolNames: []string{"run_shell"},
				},
				Action: HookAction{
					Type:         "allow",
					UpdatedInput: HookPayload{"command": "echo rewritten"},
				},
			},
		},
	}

	verdict, err := engine.Evaluate(context.Background(), HookPreToolUse, HookPayload{
		"tool_name": "run_shell",
		"command":   "echo original",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := stringsValueFromAny(verdict.UpdatedInput["command"]); got != "echo rewritten" {
		t.Fatalf("expected updated command, got %#v", verdict.UpdatedInput)
	}
}

func TestUserPromptSubmitHookPayloadIncludesCodexPromptAlias(t *testing.T) {
	payload := userPromptSubmitHookPayload("please review this patch")

	if got := toolMetaString(payload, "prompt"); got != "please review this patch" {
		t.Fatalf("expected prompt alias, got %#v", payload)
	}
	if got := toolMetaString(payload, "user_text"); got != "please review this patch" {
		t.Fatalf("expected legacy user_text alias, got %#v", payload)
	}
	if text := collectPayloadText(payload); !strings.Contains(text, "please review this patch") {
		t.Fatalf("expected payload text to include prompt, got %q", text)
	}
}

func TestHookEngineMatchesUserPromptSubmitPromptAlias(t *testing.T) {
	engine := &HookEngine{
		Enabled: true,
		Rules: []HookRule{
			{
				ID:     "prompt-guidance",
				Events: []HookEvent{HookUserPromptSubmit},
				Match: HookMatch{
					ContainsText: []string{"review"},
				},
				Action: HookAction{
					Type:    "append_context",
					Message: "include review guidance",
				},
			},
		},
	}

	verdict, err := engine.Evaluate(context.Background(), HookUserPromptSubmit, HookPayload{
		"prompt": "please review this patch",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(verdict.ContextAdds) != 1 || verdict.ContextAdds[0] != "include review guidance" {
		t.Fatalf("expected prompt alias to match contains_text, got %#v", verdict)
	}
}

func TestSubagentStopHookPayloadIncludesCodexFields(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "provider", "main-model", "", "default")
	agent := &Agent{
		Config:    DefaultConfig(root),
		Session:   session,
		Store:     store,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	payload := agent.subagentStopHookPayload("plan-02", "reviewer", "reviewer-model", "worker finished")

	if got := toolMetaString(payload, "hook_event_name"); got != string(HookSubagentStop) {
		t.Fatalf("expected SubagentStop hook event, got %#v", payload)
	}
	if got := toolMetaString(payload, "agent_id"); got != "plan-02" {
		t.Fatalf("expected agent_id, got %#v", payload)
	}
	if got := toolMetaString(payload, "agent_type"); got != "reviewer" {
		t.Fatalf("expected agent_type, got %#v", payload)
	}
	if got := toolMetaString(payload, "model"); got != "reviewer-model" {
		t.Fatalf("expected specialist model, got %#v", payload)
	}
	if got := toolMetaString(payload, "last_assistant_message"); got != "worker finished" {
		t.Fatalf("expected assistant message, got %#v", payload)
	}
	if got := toolMetaString(payload, "session_id"); got != session.ID {
		t.Fatalf("expected session_id %q, got %#v", session.ID, payload)
	}
	if got := toolMetaString(payload, "turn_id"); !strings.HasPrefix(got, session.ID+":") {
		t.Fatalf("expected turn_id to include session id, got %#v", payload)
	}
	if got := toolMetaString(payload, "transcript_path"); got != store.sessionPath(session.ID) {
		t.Fatalf("expected parent transcript path, got %#v", payload)
	}
	if payload["agent_transcript_path"] != nil {
		t.Fatalf("expected nil agent transcript path for in-process worker, got %#v", payload)
	}
	if got := toolMetaString(payload, "cwd"); got != root {
		t.Fatalf("expected cwd %q, got %#v", root, payload)
	}
	if got := toolMetaString(payload, "permission_mode"); got != "default" {
		t.Fatalf("expected permission mode, got %#v", payload)
	}
	if got, ok := payload["stop_hook_active"].(bool); !ok || !got {
		t.Fatalf("expected active stop hook flag, got %#v", payload)
	}
}

func TestStopHookPayloadIncludesCodexFields(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "provider", "main-model", "", "default")
	agent := &Agent{
		Config:    DefaultConfig(root),
		Session:   session,
		Store:     store,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	payload := agent.stopHookPayload("final draft", false, "turn-123")

	if got := toolMetaString(payload, "hook_event_name"); got != string(HookStop) {
		t.Fatalf("expected Stop hook event, got %#v", payload)
	}
	if got := toolMetaString(payload, "last_assistant_message"); got != "final draft" {
		t.Fatalf("expected assistant message, got %#v", payload)
	}
	if got := toolMetaString(payload, "session_id"); got != session.ID {
		t.Fatalf("expected session_id %q, got %#v", session.ID, payload)
	}
	if got := toolMetaString(payload, "turn_id"); got != "turn-123" {
		t.Fatalf("expected supplied turn id, got %#v", payload)
	}
	if got := toolMetaString(payload, "transcript_path"); got != store.sessionPath(session.ID) {
		t.Fatalf("expected transcript path, got %#v", payload)
	}
	if got := toolMetaString(payload, "cwd"); got != root {
		t.Fatalf("expected cwd %q, got %#v", root, payload)
	}
	if got := toolMetaString(payload, "permission_mode"); got != "default" {
		t.Fatalf("expected permission mode, got %#v", payload)
	}
	if got, ok := payload["stop_hook_active"].(bool); !ok || got {
		t.Fatalf("expected inactive stop hook flag, got %#v", payload)
	}
}

func TestSubagentStartHookPayloadIncludesCodexFields(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "provider", "main-model", "", "default")
	agent := &Agent{
		Config:    DefaultConfig(root),
		Session:   session,
		Store:     store,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	payload := agent.subagentStartHookPayload("plan-02", "reviewer", "reviewer-model", "turn-123")

	if got := toolMetaString(payload, "hook_event_name"); got != string(HookSubagentStart) {
		t.Fatalf("expected SubagentStart hook event, got %#v", payload)
	}
	if got := toolMetaString(payload, "agent_id"); got != "plan-02" {
		t.Fatalf("expected agent_id, got %#v", payload)
	}
	if got := toolMetaString(payload, "agent_type"); got != "reviewer" {
		t.Fatalf("expected agent_type, got %#v", payload)
	}
	if got := toolMetaString(payload, "model"); got != "reviewer-model" {
		t.Fatalf("expected specialist model, got %#v", payload)
	}
	if got := toolMetaString(payload, "session_id"); got != session.ID {
		t.Fatalf("expected session_id %q, got %#v", session.ID, payload)
	}
	if got := toolMetaString(payload, "turn_id"); got != "turn-123" {
		t.Fatalf("expected supplied turn id, got %#v", payload)
	}
	if got := toolMetaString(payload, "transcript_path"); got != store.sessionPath(session.ID) {
		t.Fatalf("expected transcript path, got %#v", payload)
	}
	if got := toolMetaString(payload, "cwd"); got != root {
		t.Fatalf("expected cwd %q, got %#v", root, payload)
	}
	if got := toolMetaString(payload, "permission_mode"); got != "default" {
		t.Fatalf("expected permission mode, got %#v", payload)
	}
}

func TestHookEngineMatchesSubagentStartAgentIdentity(t *testing.T) {
	engine := &HookEngine{
		Enabled: true,
		Rules: []HookRule{
			{
				ID:     "subagent-start-context",
				Events: []HookEvent{HookSubagentStart},
				Match: HookMatch{
					ContainsText: []string{"plan-02", "reviewer"},
				},
				Action: HookAction{
					Type:    "append_context",
					Message: "subagent start captured",
				},
			},
		},
	}

	verdict, err := engine.Evaluate(context.Background(), HookSubagentStart, HookPayload{
		"agent_id":   "plan-02",
		"agent_type": "reviewer",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(verdict.ContextAdds) != 1 || verdict.ContextAdds[0] != "subagent start captured" {
		t.Fatalf("expected subagent identity to match contains_text, got %#v", verdict)
	}
}

func TestHookRuntimeSubagentStartDenyIsContextOnly(t *testing.T) {
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "subagent-start-deny",
					Events: []HookEvent{HookSubagentStart},
					Match: HookMatch{
						ContainsText: []string{"reviewer"},
					},
					Action: HookAction{
						Type:    "deny",
						Message: "do not start reviewer",
					},
				},
				{
					ID:     "subagent-start-context-after-deny",
					Events: []HookEvent{HookSubagentStart},
					Match: HookMatch{
						ContainsText: []string{"reviewer"},
					},
					Action: HookAction{
						Type:    "append_context",
						Message: "later context still applies",
					},
				},
			},
		},
	}

	verdict, err := runtime.Run(context.Background(), HookSubagentStart, HookPayload{
		"agent_type": "reviewer",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !verdict.Allow || verdict.DenyReason != "" {
		t.Fatalf("SubagentStart deny must not block startup, got %#v", verdict)
	}
	if len(verdict.Warns) != 1 || !strings.Contains(verdict.Warns[0].Message, "context-injection-only") {
		t.Fatalf("expected context-only warning, got %#v", verdict.Warns)
	}
	if len(verdict.ContextAdds) != 1 || verdict.ContextAdds[0] != "later context still applies" {
		t.Fatalf("expected later context rule to run after deny, got %#v", verdict.ContextAdds)
	}
}

func TestHookRuntimeSubagentStartAskIsContextOnly(t *testing.T) {
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "subagent-start-ask",
					Events: []HookEvent{HookSubagentStart},
					Match: HookMatch{
						ContainsText: []string{"reviewer"},
					},
					Action: HookAction{
						Type:    "ask",
						Message: "start reviewer?",
					},
				},
			},
		},
	}

	verdict, err := runtime.Run(context.Background(), HookSubagentStart, HookPayload{
		"agent_type": "reviewer",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !verdict.Allow || verdict.AskMessage != "" {
		t.Fatalf("SubagentStart ask must not request confirmation, got %#v", verdict)
	}
	if len(verdict.Warns) != 1 || !strings.Contains(verdict.Warns[0].Message, "context-injection-only") {
		t.Fatalf("expected context-only warning, got %#v", verdict.Warns)
	}
}

func TestHookEngineMatchesSubagentStopAgentIdentity(t *testing.T) {
	engine := &HookEngine{
		Enabled: true,
		Rules: []HookRule{
			{
				ID:     "subagent-stop-context",
				Events: []HookEvent{HookSubagentStop},
				Match: HookMatch{
					ContainsText: []string{"plan-02", "worker finished"},
				},
				Action: HookAction{
					Type:    "append_context",
					Message: "subagent completion captured",
				},
			},
		},
	}

	verdict, err := engine.Evaluate(context.Background(), HookSubagentStop, HookPayload{
		"agent_id":               "plan-02",
		"agent_type":             "reviewer",
		"last_assistant_message": "worker finished",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(verdict.ContextAdds) != 1 || verdict.ContextAdds[0] != "subagent completion captured" {
		t.Fatalf("expected subagent identity to match contains_text, got %#v", verdict)
	}
}

func TestHookRuntimeStopDenyReturnsContinuationDecision(t *testing.T) {
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "stop-revision",
					Events: []HookEvent{HookStop},
					Match: HookMatch{
						ContainsText: []string{"needs revision"},
					},
					Action: HookAction{
						Type:    "deny",
						Message: "revise before stopping",
					},
				},
			},
		},
	}

	verdict, err := runtime.Run(context.Background(), HookStop, HookPayload{
		"last_assistant_message": "this answer needs revision",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !stopHookShouldBlock(verdict) {
		t.Fatalf("expected Stop hook block decision, got %#v", verdict)
	}
	if verdict.StopMessage != "revise before stopping" {
		t.Fatalf("expected Stop hook message, got %#v", verdict)
	}
}

func TestHookRuntimeEnrichPayloadPreservesExplicitSubagentModel(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "main-model", "", "default")
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "specialist-model",
					Events: []HookEvent{HookSubagentStop},
					Match: HookMatch{
						Models: []string{"reviewer-model"},
					},
					Action: HookAction{
						Type:    "warn",
						Message: "matched specialist model",
					},
				},
			},
		},
		Session: session,
	}

	verdict, err := runtime.Run(context.Background(), HookSubagentStop, HookPayload{
		"model": "reviewer-model",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(verdict.Warns) != 1 || verdict.Warns[0].Message != "matched specialist model" {
		t.Fatalf("expected explicit subagent model to be preserved, got %#v", verdict)
	}
}

func TestSessionStartHookPayloadIncludesCodexFields(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "provider", "main-model", "", "default")
	rt := &runtimeState{
		cfg:     DefaultConfig(root),
		store:   store,
		session: session,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	payload := rt.sessionStartHookPayload("resume")

	if got := toolMetaString(payload, "hook_event_name"); got != string(HookSessionStart) {
		t.Fatalf("expected SessionStart hook event, got %#v", payload)
	}
	if got := toolMetaString(payload, "source"); got != "resume" {
		t.Fatalf("expected resume source, got %#v", payload)
	}
	if got := toolMetaString(payload, "trigger"); got != "resume" {
		t.Fatalf("expected trigger alias, got %#v", payload)
	}
	if got := toolMetaString(payload, "session_id"); got != session.ID {
		t.Fatalf("expected session id %q, got %#v", session.ID, payload)
	}
	if got := toolMetaString(payload, "transcript_path"); got != store.sessionPath(session.ID) {
		t.Fatalf("expected transcript path, got %#v", payload)
	}
	if got := toolMetaString(payload, "cwd"); got != root {
		t.Fatalf("expected cwd %q, got %#v", root, payload)
	}
	if got := toolMetaString(payload, "model"); got != "main-model" {
		t.Fatalf("expected model, got %#v", payload)
	}
	if got := toolMetaString(payload, "permission_mode"); got != "default" {
		t.Fatalf("expected permission mode, got %#v", payload)
	}
	if text := collectPayloadText(payload); !strings.Contains(text, "resume") {
		t.Fatalf("expected collectPayloadText to include source, got %q", text)
	}
}

func TestRuntimeRunsSessionStartHookAndRecordsContext(t *testing.T) {
	root := t.TempDir()
	hooksDir := filepath.Join(root, userConfigDirName)
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	hooksJSON := `{
  "enabled": true,
  "rules": [
    {
      "id": "session-start-context",
      "events": ["SessionStart"],
      "match": {"contains_text": ["startup"]},
      "action": {"type": "append_context", "message": "startup hook context"}
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(hooksJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := DefaultConfig(root)
	markConfigProjectTrustedForTest(t, &cfg, root)
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, cfg.Provider, cfg.Model, cfg.BaseURL, cfg.PermissionMode)
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	rt := &runtimeState{
		cfg:     cfg,
		store:   store,
		writer:  io.Discard,
		ui:      NewUI(),
		session: session,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
			Perms:    NewPermissionManager(ModeBypass, nil),
		},
	}
	rt.reloadHooks()

	if err := rt.runSessionStartHook(context.Background(), "startup"); err != nil {
		t.Fatalf("runSessionStartHook: %v", err)
	}
	if len(session.Messages) != 1 || !session.Messages[0].Internal || !strings.Contains(session.Messages[0].Text, "startup hook context") {
		t.Fatalf("expected internal SessionStart hook context message, got %#v", session.Messages)
	}
	loaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Messages) != 1 || !strings.Contains(loaded.Messages[0].Text, "startup hook context") {
		t.Fatalf("expected persisted SessionStart hook context, got %#v", loaded.Messages)
	}
}

func TestBuiltinWindowsSecurityPreset(t *testing.T) {
	rules, err := builtinHookPresetRules("windows-security")
	if err != nil {
		t.Fatalf("builtinHookPresetRules: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("expected preset rules")
	}

	engine := &HookEngine{
		Enabled: true,
		Rules:   rules,
	}
	verdict, err := engine.Evaluate(context.Background(), HookPreToolUse, HookPayload{
		"tool_name": "run_shell",
		"command":   "verifier /reset",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !strings.Contains(strings.ToLower(verdict.AskMessage), "continue") {
		t.Fatalf("expected ask message, got %#v", verdict)
	}
}

func TestHookRuntimeAskDenied(t *testing.T) {
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "ask",
					Events: []HookEvent{HookPreToolUse},
					Match: HookMatch{
						ToolNames: []string{"run_shell"},
					},
					Action: HookAction{Type: "ask", Message: "Continue?"},
				},
			},
		},
		Ask: func(string) (bool, error) {
			return false, nil
		},
	}

	_, err := runtime.Run(context.Background(), HookPreToolUse, HookPayload{
		"tool_name": "run_shell",
	})
	if err == nil {
		t.Fatal("expected hook denial")
	}
}

func TestHookRuntimeSpillsLargeContextAdds(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "sessions")
	longContext := strings.Repeat("remember pre tool context ", 400)
	runtime := &HookRuntime{
		Config:  Config{SessionDir: sessionDir},
		Session: NewSession(root, "openai", "gpt-test", "", "default"),
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "large-context",
					Events: []HookEvent{HookPreToolUse},
					Action: HookAction{Type: "append_context", Message: longContext},
				},
			},
		},
	}

	verdict, err := runtime.Run(context.Background(), HookPreToolUse, HookPayload{
		"tool_name": "run_shell",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(verdict.ContextAdds) != 1 {
		t.Fatalf("expected one context add, got %#v", verdict.ContextAdds)
	}
	if len(verdict.ContextAdds[0]) >= len(longContext) || !strings.Contains(verdict.ContextAdds[0], "full text written to") {
		t.Fatalf("expected context add to be spilled, got %d chars: %q", len(verdict.ContextAdds[0]), verdict.ContextAdds[0])
	}

	entries, err := os.ReadDir(filepath.Join(sessionDir, "hook-spills"))
	if err != nil {
		t.Fatalf("ReadDir hook-spills: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one spilled hook context, got %#v", entries)
	}
	data, err := os.ReadFile(filepath.Join(sessionDir, "hook-spills", entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile spilled hook context: %v", err)
	}
	if string(data) != strings.TrimSpace(longContext) {
		t.Fatalf("spilled hook context did not preserve full text")
	}
}

func TestHookRuntimeEnrichPayloadUsesEffectiveWorkspaceRoots(t *testing.T) {
	baseRoot := t.TempDir()
	activeRoot := filepath.Join(baseRoot, "worktree")
	if err := os.MkdirAll(activeRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	runtime := &HookRuntime{
		Workspace: Workspace{
			BaseRoot: baseRoot,
			Root:     activeRoot,
			Perms:    NewPermissionManager(ModeBypass, nil),
		},
	}

	payload := runtime.enrichPayload(HookPreToolUse, HookPayload{
		"tool_name": "run_shell",
	})

	if got := toolMetaString(payload, "workspace_root"); !sameFilePath(got, baseRoot) {
		t.Fatalf("expected base workspace_root %q, got %#v", baseRoot, payload)
	}
	if got := toolMetaString(payload, "active_workspace_root"); !sameFilePath(got, activeRoot) {
		t.Fatalf("expected active workspace root %q, got %#v", activeRoot, payload)
	}
	if got := toolMetaString(payload, "cwd"); !sameFilePath(got, activeRoot) {
		t.Fatalf("expected cwd to use active root %q, got %#v", activeRoot, payload)
	}
	roots := toolMetaStringSlice(payload, "workspace_roots")
	if len(roots) != 2 || !sameFilePath(roots[0], baseRoot) || !sameFilePath(roots[1], activeRoot) {
		t.Fatalf("expected effective workspace_roots [%q %q], got %#v", baseRoot, activeRoot, payload)
	}
	if got := toolMetaString(payload, "permission_mode"); got != string(ModeBypass) {
		t.Fatalf("expected effective permission mode %q, got %#v", ModeBypass, payload)
	}
	if got := toolMetaString(payload, "active_permission_profile_id"); got != builtInPermissionProfileDangerFullAccess {
		t.Fatalf("expected effective permission profile %q, got %#v", builtInPermissionProfileDangerFullAccess, payload)
	}
}

func TestHookRuntimeEnrichPayloadAddsSpecialistAgentIdentity(t *testing.T) {
	runtime := &HookRuntime{}

	payload := runtime.enrichPayload(HookPreToolUse, HookPayload{
		"tool_name":     "run_shell",
		"owner_node_id": "plan-02",
		"specialist":    "driver-build-fixer",
	})

	if got := toolMetaString(payload, "agent_id"); got != "plan-02" {
		t.Fatalf("expected specialist hook agent_id, got %#v", payload)
	}
	if got := toolMetaString(payload, "agent_type"); got != "driver-build-fixer" {
		t.Fatalf("expected specialist hook agent_type, got %#v", payload)
	}

	rootPayload := runtime.enrichPayload(HookPreToolUse, HookPayload{
		"tool_name":     "run_shell",
		"owner_node_id": "plan-02",
	})
	if got := toolMetaString(rootPayload, "agent_id"); got != "" {
		t.Fatalf("expected root hook payload to omit agent_id, got %#v", rootPayload)
	}
	if got := toolMetaString(rootPayload, "agent_type"); got != "" {
		t.Fatalf("expected root hook payload to omit agent_type, got %#v", rootPayload)
	}

	partial := runtime.enrichPayload(HookPreToolUse, HookPayload{
		"tool_name":  "run_shell",
		"specialist": "driver-build-fixer",
	})
	if got := toolMetaString(partial, "agent_id"); got != "" {
		t.Fatalf("expected partial specialist payload to omit agent_id, got %#v", partial)
	}
	if got := toolMetaString(partial, "agent_type"); got != "" {
		t.Fatalf("expected partial specialist payload to omit agent_type, got %#v", partial)
	}

	explicit := runtime.enrichPayload(HookPreToolUse, HookPayload{
		"tool_name":     "run_shell",
		"owner_node_id": "plan-02",
		"specialist":    "driver-build-fixer",
		"agent_id":      "agent-override",
		"agent_type":    "worker-override",
	})
	if got := toolMetaString(explicit, "agent_id"); got != "agent-override" {
		t.Fatalf("expected explicit agent_id to be preserved, got %#v", explicit)
	}
	if got := toolMetaString(explicit, "agent_type"); got != "worker-override" {
		t.Fatalf("expected explicit agent_type to be preserved, got %#v", explicit)
	}
}

func TestHookEngineMatchesCompactTrigger(t *testing.T) {
	engine := &HookEngine{
		Enabled: true,
		Rules: []HookRule{
			{
				ID:     "manual-compact",
				Events: []HookEvent{HookPreCompact},
				Match: HookMatch{
					Triggers: []string{"manual"},
				},
				Action: HookAction{Type: "warn", Message: "manual compact"},
			},
		},
	}

	verdict, err := engine.Evaluate(context.Background(), HookPreCompact, HookPayload{
		"trigger": "manual",
	})
	if err != nil {
		t.Fatalf("Evaluate manual: %v", err)
	}
	if len(verdict.Warns) != 1 {
		t.Fatalf("expected manual trigger warning, got %#v", verdict)
	}

	verdict, err = engine.Evaluate(context.Background(), HookPreCompact, HookPayload{
		"trigger": "auto",
	})
	if err != nil {
		t.Fatalf("Evaluate auto: %v", err)
	}
	if len(verdict.Warns) != 0 {
		t.Fatalf("expected auto trigger to be ignored, got %#v", verdict)
	}
}

func TestAgentCompactRunsPreAndPostHooks(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "mock", "model-x", "", "ask")
	addCompactTestMessages(session, 24)
	if err := store.Save(session); err != nil {
		t.Fatalf("Save session: %v", err)
	}

	var events []HookEvent
	var postStatus string
	agent := &Agent{
		Workspace: Workspace{
			BaseRoot: root,
			Root:     root,
			RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
				events = append(events, event)
				if got := stringsValueFromAny(payload["hook_event_name"]); got != string(event) {
					t.Fatalf("unexpected hook_event_name for %s: %q", event, got)
				}
				if got := stringsValueFromAny(payload["trigger"]); got != "manual" {
					t.Fatalf("unexpected compact trigger for %s: %q", event, got)
				}
				if event == HookPostCompact {
					postStatus = stringsValueFromAny(payload["status"])
					if intValueFromAny(payload["messages_after"]) >= intValueFromAny(payload["messages_before"]) {
						t.Fatalf("expected compact to reduce messages, payload=%#v", payload)
					}
				}
				return HookVerdict{Allow: true}, nil
			},
		},
		Session: session,
		Store:   store,
	}

	summary, err := agent.CompactWithTrigger(context.Background(), "manual compact", "manual", "user_requested")
	if err != nil {
		t.Fatalf("CompactWithTrigger: %v", err)
	}
	if !strings.Contains(summary, "manual compact") {
		t.Fatalf("expected compact summary to include instructions, got %q", summary)
	}
	if got, want := events, []HookEvent{HookPreCompact, HookPostCompact}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("unexpected compact hook order: got %#v want %#v", got, want)
	}
	if postStatus != "success" {
		t.Fatalf("expected post compact success status, got %q", postStatus)
	}
}

func TestAgentCompactPreHookCanBlockMutation(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "mock", "model-x", "", "ask")
	addCompactTestMessages(session, 24)
	if err := store.Save(session); err != nil {
		t.Fatalf("Save session: %v", err)
	}
	beforeMessages := append([]Message(nil), session.Messages...)
	beforeSummary := session.Summary
	agent := &Agent{
		Workspace: Workspace{
			BaseRoot: root,
			Root:     root,
			RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
				if event == HookPreCompact {
					return HookVerdict{}, fmt.Errorf("pre compact denied")
				}
				return HookVerdict{Allow: true}, nil
			},
		},
		Session: session,
		Store:   store,
	}

	_, err := agent.CompactWithTrigger(context.Background(), "manual compact", "manual", "user_requested")
	if err == nil || !strings.Contains(err.Error(), "pre compact denied") {
		t.Fatalf("expected pre compact denial, got %v", err)
	}
	if fmt.Sprint(session.Messages) != fmt.Sprint(beforeMessages) {
		t.Fatalf("expected messages to remain unchanged after pre hook denial")
	}
	if session.Summary != beforeSummary {
		t.Fatalf("expected summary to remain unchanged after pre hook denial")
	}
}

func addCompactTestMessages(session *Session, count int) {
	for i := 0; i < count; i++ {
		session.AddMessage(Message{
			Role: "user",
			Text: fmt.Sprintf("compact test message %02d", i),
		})
	}
}

func TestHookRuntimeCreatesCheckpointBeforeDeny(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		ID:        "ev-1",
		Workspace: root,
		Kind:      "verification_failure",
		Category:  "driver",
		Subject:   "runtime_error",
		Outcome:   "failed",
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	var created []string
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:       "checkpoint-first",
					Priority: 200,
					Events:   []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "create_checkpoint",
						Message: "checkpoint before deny",
					},
				},
				{
					ID:       "deny-second",
					Priority: 100,
					Events:   []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "deny",
						Message: "blocked after checkpoint",
					},
				},
			},
		},
		CreateCheckpoint: func(note string) (CheckpointMetadata, error) {
			created = append(created, note)
			return CheckpointMetadata{ID: "cp-1", Name: note}, nil
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	_, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "blocked after checkpoint") {
		t.Fatalf("expected deny after checkpoint, got %v", err)
	}
	if len(created) != 1 || created[0] != "checkpoint before deny" {
		t.Fatalf("expected checkpoint to be created before deny, got %#v", created)
	}
}

func TestHookEngineAddsVerificationStep(t *testing.T) {
	engine := &HookEngine{
		Enabled: true,
		Rules: []HookRule{
			{
				ID:     "extra-verify",
				Events: []HookEvent{HookPreVerification},
				Match: HookMatch{
					ChangedFiles: []string{"**/*.sys"},
				},
				Action: HookAction{
					Type:    "add_verification_step",
					Label:   "driver verify",
					Command: "echo verify",
					Tags:    []string{"driver"},
				},
			},
		},
	}
	verdict, err := engine.Evaluate(context.Background(), HookPreVerification, HookPayload{
		"changed_files": []string{"build/agent.sys"},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(verdict.VerificationAdds) != 1 {
		t.Fatalf("expected one verification step, got %#v", verdict.VerificationAdds)
	}
	if verdict.VerificationAdds[0].Command != "echo verify" {
		t.Fatalf("unexpected step: %#v", verdict.VerificationAdds[0])
	}
}

func TestHookEngineAppendReviewContextAlias(t *testing.T) {
	engine := &HookEngine{
		Enabled: true,
		Rules: []HookRule{
			{
				ID:     "review-context",
				Events: []HookEvent{HookPreVerification},
				Match: HookMatch{
					ChangedFiles: []string{"**/*.man"},
				},
				Action: HookAction{
					Type:    "append_review_context",
					Message: "review telemetry manifest assumptions",
				},
			},
		},
	}
	verdict, err := engine.Evaluate(context.Background(), HookPreVerification, HookPayload{
		"changed_files": []string{"telemetry/provider.man"},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(verdict.ContextAdds) != 1 || verdict.ContextAdds[0] != "review telemetry manifest assumptions" {
		t.Fatalf("unexpected context adds: %#v", verdict.ContextAdds)
	}
}

func TestLoadHookEngineFromWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	hooksDir := filepath.Join(root, userConfigDirName)
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(hooksDir, "hooks.json")
	if err := os.WriteFile(path, []byte(InitHooksTemplate()), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := DefaultConfig(root)
	markConfigProjectTrustedForTest(t, &cfg, root)
	engine, warns := LoadHookEngine(root, cfg)
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %#v", warns)
	}
	if engine == nil || len(engine.Rules) == 0 {
		t.Fatalf("expected loaded engine, got %#v", engine)
	}
}

func TestRuntimeReloadHooksAttachesStartupHookRuntime(t *testing.T) {
	root := t.TempDir()
	hooksDir := filepath.Join(root, userConfigDirName)
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	hooksJSON := `{
  "enabled": true,
  "rules": [
    {
      "id": "startup-prompt-context",
      "events": ["UserPromptSubmit"],
      "match": {"contains_text": ["startup hook"]},
      "action": {"type": "append_context", "message": "loaded at startup"}
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(hooksJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := DefaultConfig(root)
	markConfigProjectTrustedForTest(t, &cfg, root)
	session := NewSession(root, cfg.Provider, cfg.Model, cfg.BaseURL, cfg.PermissionMode)
	rt := &runtimeState{
		cfg:     cfg,
		ui:      NewUI(),
		session: session,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
			Perms:    NewPermissionManager(ModeBypass, nil),
		},
		agent: &Agent{Session: session},
	}

	rt.reloadHooks()
	if rt.hooks == nil {
		t.Fatal("expected startup hook runtime to be loaded")
	}
	if rt.agent.Hooks != rt.hooks {
		t.Fatalf("expected agent hook runtime to match runtimeState hook runtime")
	}

	verdict, err := rt.runHook(context.Background(), HookUserPromptSubmit, HookPayload{
		"prompt": "exercise startup hook",
	})
	if err != nil {
		t.Fatalf("runHook: %v", err)
	}
	if len(verdict.ContextAdds) != 1 || verdict.ContextAdds[0] != "loaded at startup" {
		t.Fatalf("expected startup hook rule to run, got %#v", verdict)
	}
}

func TestLoadHookEngineSkipsWorkspaceFileUntilProjectTrusted(t *testing.T) {
	root := t.TempDir()
	hooksDir := filepath.Join(root, userConfigDirName)
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(hooksDir, "hooks.json")
	if err := os.WriteFile(path, []byte(InitHooksTemplate()), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	engine, warns := LoadHookEngine(root, DefaultConfig(root))
	if engine != nil {
		t.Fatalf("expected untrusted workspace hooks to be ignored, got %#v", engine)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "ignored until project is trusted") {
		t.Fatalf("expected untrusted hooks warning, got %#v", warns)
	}
}

func TestLoadHookEngineBypassHookTrustLoadsWorkspaceFileForThisInvocation(t *testing.T) {
	root := t.TempDir()
	hooksDir := filepath.Join(root, userConfigDirName)
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(hooksDir, "hooks.json")
	if err := os.WriteFile(path, []byte(InitHooksTemplate()), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := DefaultConfig(root)
	cfg.BypassHookTrust = true
	engine, warns := LoadHookEngine(root, cfg)
	if engine == nil || len(engine.Rules) == 0 {
		t.Fatalf("expected bypassed workspace hooks to load, got %#v", engine)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "dangerously-bypass-hook-trust is enabled") {
		t.Fatalf("expected bypass warning, got %#v", warns)
	}
	if configProjectTrusted(cfg, root) {
		t.Fatalf("bypass must not persist project trust")
	}
}

func TestLoadHookEngineUsesRootCheckoutHooksForLinkedWorktree(t *testing.T) {
	ctx := context.Background()
	repoRoot := t.TempDir()
	if _, err := runGitCommand(ctx, repoRoot, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "config", "user.email", "test@example.invalid"); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "config", "user.name", "Test User"); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("root\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "add", "README.md"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	writeHookTestFile := func(root string, id string) {
		t.Helper()
		hooksDir := filepath.Join(root, userConfigDirName)
		if err := os.MkdirAll(hooksDir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", hooksDir, err)
		}
		body := fmt.Sprintf(`{
  "rules": [
    {
      "id": %q,
      "enabled": true,
      "events": ["PreToolUse"],
      "match": {
        "tool_names": ["run_shell"]
      },
      "action": {
        "type": "warn",
        "message": %q
      }
    }
  ]
}
`, id, id)
		if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile hook %s: %v", id, err)
		}
	}

	writeHookTestFile(repoRoot, "root-hook")
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	if _, err := runGitCommand(ctx, repoRoot, "worktree", "add", "-b", "linked-hooks", linkedRoot); err != nil {
		t.Fatalf("git worktree add: %v", err)
	}
	t.Cleanup(func() {
		_, _ = runGitCommand(ctx, repoRoot, "worktree", "remove", "--force", linkedRoot)
	})
	writeHookTestFile(linkedRoot, "linked-hook")

	cfg := DefaultConfig(linkedRoot)
	hookRoot := projectHookRoot(linkedRoot)
	markConfigProjectTrustedForTest(t, &cfg, hookRoot)
	engine, warns := LoadHookEngine(linkedRoot, cfg)
	if engine == nil || len(engine.Rules) != 1 {
		t.Fatalf("expected exactly root hook rule, engine=%#v warns=%#v", engine, warns)
	}
	if engine.Rules[0].ID != "root-hook" {
		t.Fatalf("expected root checkout hook, got %#v", engine.Rules)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "workspace hooks ignored") {
		t.Fatalf("expected linked worktree hook warning, got %#v", warns)
	}
}

func TestHookRuntimeInjectsRecentFailedEvidence(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		ID:        "ev-1",
		Workspace: root,
		Kind:      "verification_failure",
		Category:  "driver",
		Subject:   "runtime_error",
		Outcome:   "failed",
		Tags:      []string{"failure", "driver"},
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "driver-failure",
					Events: []HookEvent{HookPreGitPush},
					Match: HookMatch{
						EvidenceCategories:      []string{"driver"},
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "warn",
						Message: "recent failed driver evidence",
					},
				},
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	verdict, err := runtime.Run(context.Background(), HookPreGitPush, HookPayload{
		"changed_files": []string{"driver/guard.cpp"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(verdict.Warns) != 1 {
		t.Fatalf("expected evidence-based warning, got %#v", verdict)
	}
}

func TestHookRuntimeMatchesTelemetryFailedEvidence(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		ID:        "ev-telemetry",
		Workspace: root,
		Kind:      "verification_failure",
		Category:  "telemetry",
		Subject:   "runtime_error",
		Outcome:   "failed",
		Tags:      []string{"failure", "telemetry"},
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "telemetry-failure",
					Events: []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						EvidenceCategories:      []string{"telemetry"},
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "ask",
						Message: "telemetry failed evidence",
					},
				},
			},
		},
		Ask: func(string) (bool, error) {
			return true, nil
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	verdict, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{
		"changed_files": []string{"telemetry/provider.man"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if verdict.AskMessage == "" && len(verdict.MatchedRuleIDs) == 0 {
		t.Fatalf("expected telemetry evidence match, got %#v", verdict)
	}
}

func TestBuiltinWindowsSecurityPresetIncludesAdditionalEvidencePolicies(t *testing.T) {
	rules, err := builtinHookPresetRules("windows-security")
	if err != nil {
		t.Fatalf("builtinHookPresetRules: %v", err)
	}
	var telemetryRule bool
	var memoryScanRule bool
	var repeatedMemoryScanRule bool
	var telemetryReviewContextRule bool
	var unrealRule bool
	var driverDenyRule bool
	var repeatedDriverRule bool
	var checkpointBeforeDriverDenyRule bool
	var simulationTamperRule bool
	var simulationStealthRule bool
	var simulationForensicsRule bool
	for _, rule := range rules {
		switch rule.ID {
		case "deny-driver-pr-with-critical-signing-or-symbol-evidence":
			driverDenyRule = true
		case "deny-driver-pr-with-repeated-sensitive-artifact-failures":
			repeatedDriverRule = true
			if rule.Action.Type == "create_checkpoint" {
				checkpointBeforeDriverDenyRule = true
			}
		case "ask-before-telemetry-push-with-failed-evidence":
			telemetryRule = true
		case "append-review-context-before-telemetry-verification":
			telemetryReviewContextRule = true
		case "ask-before-memory-scan-push-with-failed-evidence":
			memoryScanRule = true
		case "deny-memory-scan-push-with-repeated-failures":
			repeatedMemoryScanRule = true
		case "warn-before-unreal-pr-with-failed-evidence":
			unrealRule = true
		case "warn-before-push-with-high-risk-simulation-tamper-finding":
			simulationTamperRule = true
		case "warn-before-push-with-high-risk-simulation-stealth-finding":
			simulationStealthRule = true
		case "warn-before-pr-with-forensic-blind-spot-simulation":
			simulationForensicsRule = true
		}
	}
	if !driverDenyRule || !repeatedDriverRule || !checkpointBeforeDriverDenyRule || !telemetryRule || !telemetryReviewContextRule || !memoryScanRule || !repeatedMemoryScanRule || !unrealRule || !simulationTamperRule || !simulationStealthRule || !simulationForensicsRule {
		t.Fatalf("expected expanded evidence policies, got %#v", rules)
	}
}

func TestHookRuntimeMatchesSimulationTamperEvidenceForWarn(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		ID:          "ev-sim-tamper",
		Workspace:   root,
		Kind:        "simulation_finding",
		Category:    "driver",
		Subject:     "unsigned-or-unverified-driver-surface",
		Outcome:     "failed",
		Severity:    "critical",
		SignalClass: "tamper",
		RiskScore:   88,
		Tags:        []string{"simulation", "tamper-surface", "tamper_surface"},
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "simulation-tamper-warn",
					Events: []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						EvidenceTags:            []string{"simulation", "tamper-surface"},
						EvidenceSeverities:      []string{"critical"},
						SignalClasses:           []string{"tamper"},
						MinEvidenceRiskScore:    intPtr(60),
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "warn",
						Message: "simulation tamper warning",
					},
				},
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	verdict, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(verdict.Warns) != 1 || !strings.Contains(strings.ToLower(verdict.Warns[0].Message), "simulation tamper") {
		t.Fatalf("expected simulation warning, got %#v", verdict)
	}
}

func TestHookRuntimeMatchesDriverEvidenceTagsForDeny(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		ID:        "ev-driver-signing",
		Workspace: root,
		Kind:      "verification_artifact",
		Category:  "driver",
		Subject:   "driver/guard.sys",
		Outcome:   "failed",
		Tags:      []string{"artifact", "signing", "driver"},
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "driver-deny",
					Events: []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						EvidenceCategories:      []string{"driver"},
						EvidenceTags:            []string{"signing"},
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "deny",
						Message: "failed signing evidence",
					},
				},
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	_, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{
		"changed_files": []string{"driver/guard.cpp"},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "failed signing evidence") {
		t.Fatalf("expected deny from evidence tags, got %v", err)
	}
}

func TestHookRuntimeMatchesEvidenceSeverityAndRisk(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		ID:          "ev-driver-critical",
		Workspace:   root,
		Kind:        "verification_artifact",
		Category:    "driver",
		Subject:     "driver/guard.sys",
		Outcome:     "failed",
		Severity:    "critical",
		SignalClass: "signing",
		RiskScore:   92,
		Tags:        []string{"artifact", "signing", "driver"},
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "critical-risk-deny",
					Events: []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						EvidenceCategories:      []string{"driver"},
						EvidenceSeverities:      []string{"critical"},
						SignalClasses:           []string{"signing"},
						MinEvidenceRiskScore:    intPtr(80),
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "deny",
						Message: "critical driver evidence",
					},
				},
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	_, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{
		"changed_files": []string{"driver/guard.cpp"},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "critical driver evidence") {
		t.Fatalf("expected severity/risk deny, got %v", err)
	}
}

func TestHookRuntimeMatchesRepeatedDriverArtifactEvidenceForDeny(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	for _, record := range []EvidenceRecord{
		{
			ID:        "ev-driver-sys",
			Workspace: root,
			Kind:      "verification_artifact",
			Category:  "driver",
			Subject:   "build/guard.sys",
			Outcome:   "failed",
			Tags:      []string{"artifact", "driver", "signing"},
		},
		{
			ID:        "ev-driver-inf",
			Workspace: root,
			Kind:      "verification_artifact",
			Category:  "driver",
			Subject:   "build/guard.inf",
			Outcome:   "failed",
			Tags:      []string{"artifact", "driver"},
		},
	} {
		if err := evidence.Append(record); err != nil {
			t.Fatalf("Append evidence: %v", err)
		}
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "driver-repeated-artifact-deny",
					Events: []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						EvidenceCategories:      []string{"driver"},
						EvidenceSubjects:        []string{"**/*.sys", "**/*.inf", "**/*.cat"},
						MinEvidenceMatches:      intPtr(2),
						MaxEvidenceAgeHours:     intPtr(24),
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "deny",
						Message: "repeated driver artifact failures",
					},
				},
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	_, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{
		"changed_files": []string{"driver/guard.cpp"},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "repeated driver artifact failures") {
		t.Fatalf("expected deny from repeated artifact failures, got %v", err)
	}
}

func TestHookRuntimeRequiresMinEvidenceMatches(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		ID:        "ev-memory-1",
		Workspace: root,
		Kind:      "verification_failure",
		Category:  "memory-scan",
		Subject:   "runtime_error",
		Outcome:   "failed",
		Tags:      []string{"failure", "memory-scan"},
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "memory-scan-repeated",
					Events: []HookEvent{HookPreGitPush},
					Match: HookMatch{
						EvidenceCategories:      []string{"memory-scan"},
						MinEvidenceMatches:      intPtr(2),
						MaxEvidenceAgeHours:     intPtr(24),
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "deny",
						Message: "repeated memory-scan failures",
					},
				},
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	verdict, err := runtime.Run(context.Background(), HookPreGitPush, HookPayload{
		"changed_files": []string{"memoryscan/scanner.cpp"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !verdict.Allow {
		t.Fatalf("expected allow when min evidence threshold is not met: %#v", verdict)
	}
	if len(verdict.MatchedRuleIDs) != 0 {
		t.Fatalf("expected no matches when min evidence threshold is not met: %#v", verdict)
	}
}

func TestHookRuntimeIgnoresOldEvidenceWhenMaxAgeIsSet(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	for _, record := range []EvidenceRecord{
		{
			ID:        "ev-old-driver-sys",
			Workspace: root,
			Kind:      "verification_artifact",
			Category:  "driver",
			Subject:   "build/guard.sys",
			Outcome:   "failed",
			Tags:      []string{"artifact", "driver"},
			CreatedAt: time.Now().Add(-48 * time.Hour),
		},
		{
			ID:        "ev-old-driver-inf",
			Workspace: root,
			Kind:      "verification_artifact",
			Category:  "driver",
			Subject:   "build/guard.inf",
			Outcome:   "failed",
			Tags:      []string{"artifact", "driver"},
			CreatedAt: time.Now().Add(-36 * time.Hour),
		},
	} {
		if err := evidence.Append(record); err != nil {
			t.Fatalf("Append evidence: %v", err)
		}
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "driver-recent-artifact-deny",
					Events: []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						EvidenceCategories:      []string{"driver"},
						EvidenceSubjects:        []string{"**/*.sys", "**/*.inf"},
						MinEvidenceMatches:      intPtr(2),
						MaxEvidenceAgeHours:     intPtr(24),
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "deny",
						Message: "recent repeated driver artifact failures",
					},
				},
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	verdict, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{
		"changed_files": []string{"driver/guard.cpp"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !verdict.Allow {
		t.Fatalf("expected allow when evidence is older than max age: %#v", verdict)
	}
	if len(verdict.MatchedRuleIDs) != 0 {
		t.Fatalf("expected no matches for stale evidence: %#v", verdict)
	}
}
