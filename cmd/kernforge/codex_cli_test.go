package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNewProviderClientSupportsCodexCLIAliases(t *testing.T) {
	for _, provider := range []string{"codex", "codex-cli", "codex_cli", "openai-codex-cli", "openai_codex_cli"} {
		client, err := NewProviderClient(Config{Provider: provider, Model: codexCLIDefaultModel})
		if err != nil {
			t.Fatalf("NewProviderClient(%q): %v", provider, err)
		}
		if client.Name() != "codex-cli" {
			t.Fatalf("expected codex-cli client for %q, got %q", provider, client.Name())
		}
	}
}

func TestCodexCLIProviderDoesNotInheritStaleAPIKey(t *testing.T) {
	rt := &runtimeState{
		cfg: Config{
			Provider: "codex-cli",
			APIKey:   "stale-openai-compatible-key",
			ProviderKeys: map[string]string{
				"codex-cli": "stale-codex-key",
				"opencode":  "opencode-key",
			},
		},
	}
	if key := rt.providerAPIKey("codex-cli"); key != "" {
		t.Fatalf("codex-cli should not expose an API key, got %q", key)
	}
	if key := rt.providerAPIKey("opencode"); key != "opencode-key" {
		t.Fatalf("expected other provider key lookup to keep working, got %q", key)
	}
}

func TestBuildCodexCLIArgsUsesModelConfigOverride(t *testing.T) {
	args := buildCodexCLIArgs("gpt-5.1-codex", []string{"--sandbox", "read-only"}, "hello")
	want := []string{"exec", "-c", "model=gpt-5.1-codex", "--sandbox", "read-only", "hello"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}

	args = buildCodexCLIArgs(codexCLIDefaultModel, []string{"--json"}, "hello")
	want = []string{"exec", "--json", "hello"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("default model args = %#v, want %#v", args, want)
	}
}

func TestParseCodexCLIModelsJSONFiltersHiddenUnsupportedAndDuplicates(t *testing.T) {
	models, err := parseCodexCLIModelsJSON([]byte(strings.Join([]string{
		"plugin warning before json",
		`{"models":[` +
			`{"slug":"gpt-5.5","display_name":"GPT-5.5","supported_in_api":true,"visibility":"list","priority":0},` +
			`{"slug":"gpt-5.5","display_name":"duplicate","supported_in_api":true,"visibility":"list","priority":1},` +
			`{"slug":"hidden-model","display_name":"Hidden","supported_in_api":true,"visibility":"hide"},` +
			`{"slug":"unsupported-model","display_name":"Unsupported","supported_in_api":false,"visibility":"list"},` +
			`{"id":"custom-codex","name":"Custom Codex","visibility":"list"}` +
			`]}`,
		"plugin warning after json",
	}, "\n")))
	if err != nil {
		t.Fatalf("parseCodexCLIModelsJSON: %v", err)
	}
	want := []CodexCLIModelInfo{
		{ID: "gpt-5.5", Name: "GPT-5.5", SupportedInAPI: true, Visibility: "list", Priority: 0},
		{ID: "custom-codex", Name: "Custom Codex", SupportedInAPI: true, Visibility: "list", Priority: 0},
	}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
}

func TestCodexCLIModelListIncludesGPT55Pro(t *testing.T) {
	for _, model := range codexCLIModels {
		if model.ID == "gpt-5.5-pro" {
			return
		}
	}
	t.Fatalf("expected Codex CLI model chooser to include gpt-5.5-pro")
}

func TestCodexCLIClientCompleteInvokesRunner(t *testing.T) {
	root := t.TempDir()
	client := NewCodexCLIClient("codex-test", []string{"--sandbox", "read-only"})
	var gotExecutable string
	var gotArgs []string
	var gotDir string
	client.run = func(ctx context.Context, executable string, args []string, dir string, env []string) ([]byte, error) {
		gotExecutable = executable
		gotArgs = append([]string(nil), args...)
		gotDir = dir
		return []byte("\x1b[32manswer\x1b[0m"), nil
	}
	delta := ""
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model:      "gpt-5.1-codex",
		System:     "system prompt",
		WorkingDir: root,
		Messages: []Message{{
			Role: "user",
			Text: "explain the project",
		}},
		Tools: []ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: map[string]any{
				"type": "object",
			},
		}},
		OnTextDelta: func(text string) {
			delta += text
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text != "answer" || delta != "answer" {
		t.Fatalf("expected sanitized answer and delta, got resp=%q delta=%q", resp.Message.Text, delta)
	}
	if gotExecutable != "codex-test" {
		t.Fatalf("unexpected executable: %q", gotExecutable)
	}
	if gotDir != root {
		t.Fatalf("unexpected working dir: %q", gotDir)
	}
	if len(gotArgs) < 6 {
		t.Fatalf("expected args to include exec, model, extra args, and prompt, got %#v", gotArgs)
	}
	joinedArgs := strings.Join(gotArgs[:len(gotArgs)-1], "\x00")
	for _, needle := range []string{"exec", "-c", "model=gpt-5.1-codex", "--sandbox", "read-only"} {
		if !strings.Contains(joinedArgs, needle) {
			t.Fatalf("expected %q in args %#v", needle, gotArgs)
		}
	}
	prompt := gotArgs[len(gotArgs)-1]
	for _, needle := range []string{"Kernforge request for Codex CLI", "Do not emit Kernforge tool-call JSON", "system prompt", "explain the project"} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("expected prompt to contain %q, got %q", needle, prompt)
		}
	}
}

func TestCodexCLIClientCompleteExtractsJSONFromTranscript(t *testing.T) {
	root := t.TempDir()
	client := NewCodexCLIClient("codex-test", nil)
	client.run = func(ctx context.Context, executable string, args []string, dir string, env []string) ([]byte, error) {
		return []byte(strings.Join([]string{
			"Reading additional input from stdin...",
			"OpenAI Codex v0.125.0 (research preview)",
			"--------",
			"user",
			"Return JSON",
			"codex",
			"{\"report\":{\"title\":\"final\",\"scope_summary\":\"ok\"}}",
			"2026-04-28T13:50:20Z ERROR codex_core::session: failed to record rollout items",
			"tokens used",
			"3,785",
			"{\"report\":{\"title\":\"duplicate\",\"scope_summary\":\"ok\"}}",
		}, "\n")), nil
	}
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model:      "gpt-5.4-mini",
		WorkingDir: root,
		JSONMode:   true,
		Messages: []Message{{
			Role: "user",
			Text: "return json",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text != `{"report":{"title":"final","scope_summary":"ok"}}` {
		t.Fatalf("expected final JSON object, got %q", resp.Message.Text)
	}
}

func TestCodexCLIClientCompleteDoesNotExtractSchemaFromPromptTranscript(t *testing.T) {
	root := t.TempDir()
	client := NewCodexCLIClient("codex-test", nil)
	client.run = func(ctx context.Context, executable string, args []string, dir string, env []string) ([]byte, error) {
		return []byte(strings.Join([]string{
			"Reading additional input from stdin...",
			"OpenAI Codex v0.125.0 (research preview)",
			"user",
			"Read the complete Kernforge request from this file, follow it, and return the final answer: request.md",
			"codex",
			"요청 파일의 전체 내용을 먼저 읽겠습니다.",
			"exec",
			"# Kernforge request for Codex CLI",
			"{\"report\":{\"title\":\"string\",\"scope_summary\":\"string\"}}",
			"2026-04-28T13:50:20Z ERROR codex_core::session: failed to record rollout items",
		}, "\n")), nil
	}
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model:      "gpt-5.4-mini",
		WorkingDir: root,
		JSONMode:   true,
		Messages: []Message{{
			Role: "user",
			Text: "return json",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if strings.Contains(resp.Message.Text, `"title":"string"`) {
		t.Fatalf("did not expect schema example to be extracted as final JSON, got %q", resp.Message.Text)
	}
	if !strings.Contains(resp.Message.Text, "요청 파일") {
		t.Fatalf("expected assistant block fallback, got %q", resp.Message.Text)
	}
}

func TestCodexCLIClientCompleteExtractsFinalPlainBlockFromTranscript(t *testing.T) {
	root := t.TempDir()
	client := NewCodexCLIClient("codex-test", nil)
	client.run = func(ctx context.Context, executable string, args []string, dir string, env []string) ([]byte, error) {
		return []byte("OpenAI Codex\nuser\nhello\ncodex\nworking...\nexec\ncmd\ncodex\nfinal answer\n2026-04-28T13:50:20Z ERROR codex_core::session: failed to record rollout items\n"), nil
	}
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model:      "gpt-5.4-mini",
		WorkingDir: root,
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text != "final answer" {
		t.Fatalf("expected final answer block, got %q", resp.Message.Text)
	}
}

func TestCodexCLIPromptArgumentSpillsLargePromptToWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	prompt := strings.Repeat("x", codexCLIPromptArgLimit+1)
	arg, cleanup, err := codexCLIPromptArgument(root, prompt)
	if err != nil {
		t.Fatalf("codexCLIPromptArgument: %v", err)
	}
	if cleanup == nil {
		t.Fatalf("expected cleanup for spilled prompt")
	}
	defer cleanup()
	if !strings.Contains(arg, filepath.Join(root, userConfigDirName, "tmp")) {
		t.Fatalf("expected prompt arg to reference workspace tmp file, got %q", arg)
	}
	prefix := "Read the complete Kernforge request from this file, follow it, and return the final answer: "
	path := strings.TrimSpace(strings.TrimPrefix(arg, prefix))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	if !strings.Contains(string(data), prompt) {
		t.Fatalf("spilled prompt file did not contain prompt")
	}
	cleanup()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected spilled prompt file to be removed, stat err=%v", err)
	}
}
