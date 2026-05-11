package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestNewProviderClientSupportsClaudeCLIAliases(t *testing.T) {
	for _, provider := range []string{"anthropic-claude-cli", "claude-cli", "claude_code_cli", "claude code cli"} {
		client, err := NewProviderClient(Config{Provider: provider, Model: claudeCLIDefaultModel})
		if err != nil {
			t.Fatalf("NewProviderClient(%q): %v", provider, err)
		}
		if client.Name() != "anthropic-claude-cli" {
			t.Fatalf("expected anthropic-claude-cli client for %q, got %q", provider, client.Name())
		}
	}
}

func TestBuildClaudeCLIArgsUsesModelConfigOverride(t *testing.T) {
	args := buildClaudeCLIArgs("sonnet", []string{"--permission-mode", "plan"}, "hello")
	want := []string{"--model", "sonnet", "--permission-mode", "plan", "-p", "hello"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}

	args = buildClaudeCLIArgs(claudeCLIDefaultModel, []string{"--output-format", "text"}, "hello")
	want = []string{"--output-format", "text", "-p", "hello"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("default model args = %#v, want %#v", args, want)
	}
}

func TestBuildClaudeCLIArgsMapsVersionedBuiltinsToAliases(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{name: "sonnet 4.7", model: "claude-sonnet-4-7", want: "sonnet"},
		{name: "opus 4.7", model: "claude-opus-4-7", want: "opus"},
		{name: "haiku 3.5", model: "claude-haiku-3-5", want: "haiku"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildClaudeCLIArgs(tt.model, nil, "hello")
			want := []string{"--model", tt.want, "-p", "hello"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("args = %#v, want %#v", args, want)
			}
		})
	}
}

func TestClaudeCLIClientCompleteInvokesRunner(t *testing.T) {
	root := t.TempDir()
	client := NewClaudeCLIClient("claude-test", []string{"--permission-mode", "plan"})
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
		Model:      "sonnet",
		System:     "system prompt",
		WorkingDir: root,
		Messages: []Message{{
			Role: "user",
			Text: "review this code",
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
	if gotExecutable != "claude-test" {
		t.Fatalf("unexpected executable: %q", gotExecutable)
	}
	if gotDir != root {
		t.Fatalf("unexpected working dir: %q", gotDir)
	}
	if len(gotArgs) < 6 {
		t.Fatalf("expected args to include model, extra args, -p, and prompt, got %#v", gotArgs)
	}
	joinedArgs := strings.Join(gotArgs[:len(gotArgs)-1], "\x00")
	for _, needle := range []string{"--model", "sonnet", "--permission-mode", "plan", "-p"} {
		if !strings.Contains(joinedArgs, needle) {
			t.Fatalf("expected %q in args %#v", needle, gotArgs)
		}
	}
	prompt := gotArgs[len(gotArgs)-1]
	for _, needle := range []string{"Kernforge request for Claude Code CLI", "Do not emit Kernforge tool-call JSON", "system prompt", "review this code"} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("expected prompt to contain %q, got %q", needle, prompt)
		}
	}
}

func TestClaudeCLIModelChoicesIncludeCurrentCustomModel(t *testing.T) {
	choices := claudeCLIModelChoices("claude-custom")
	if choices[len(choices)-1].ID != "claude-custom" {
		t.Fatalf("expected custom current model to be appended, got %#v", choices)
	}
}

func TestClaudeCLIModelChoicesShowCurrentVersionsWithSafeAliases(t *testing.T) {
	choices := claudeCLIModelChoices("claude-sonnet-4-7")
	wantPrefix := []string{
		claudeCLIDefaultModel,
		"sonnet",
		"opus",
		"haiku",
	}
	for i, want := range wantPrefix {
		if choices[i].ID != want {
			t.Fatalf("choice %d = %q, want %q; choices=%#v", i, choices[i].ID, want, choices)
		}
	}
	if len(choices) != len(wantPrefix) {
		t.Fatalf("versioned current model should be represented by built-in alias choices, got %#v", choices)
	}
	sonnetSeen := false
	legacyIDSeen := false
	for _, choice := range choices {
		if choice.ID == "sonnet" && strings.Contains(choice.Name, "4.7") && strings.Contains(choice.Name, "CLI alias") {
			sonnetSeen = true
		}
		if strings.Contains(choice.ID, "4-6") || strings.Contains(choice.ID, "4-7") {
			legacyIDSeen = true
		}
	}
	if !sonnetSeen {
		t.Fatalf("expected sonnet alias to display the current family version, got %#v", choices)
	}
	if legacyIDSeen {
		t.Fatalf("built-in Claude CLI choices must not pass versioned IDs directly, got %#v", choices)
	}
}
