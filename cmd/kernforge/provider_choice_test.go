package main

import "testing"

func TestProviderChoiceOptionsUsePreferredOrder(t *testing.T) {
	got := providerChoiceOptions()
	want := []providerChoiceOption{
		{Number: "1", ID: "openai-codex", Label: "openai-codex-subscription"},
		{Number: "2", ID: "codex-cli", Label: "openai-codex-cli"},
		{Number: "3", ID: "openai", Label: "openai-api"},
		{Number: "4", ID: "anthropic-claude-cli", Label: "anthropic-claude-cli"},
		{Number: "5", ID: "anthropic", Label: "anthropic-api"},
		{Number: "6", ID: "deepseek", Label: "DeepSeek"},
		{Number: "7", ID: "openrouter", Label: "openrouter"},
		{Number: "8", ID: "opencode", Label: "OpenCode Zen"},
		{Number: "9", ID: "opencode-go", Label: "OpenCode Go"},
		{Number: "10", ID: "ollama", Label: "ollama"},
		{Number: "11", ID: "lmstudio", Label: "LM Studio"},
		{Number: "12", ID: "vllm", Label: "vLLM"},
		{Number: "13", ID: "llama.cpp", Label: "llama.cpp"},
	}
	if len(got) != len(want) {
		t.Fatalf("provider choices len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("provider choice %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestResolveProviderChoiceAcceptsNewDisplayAliases(t *testing.T) {
	cases := map[string]string{
		"1":                         "openai-codex",
		"openai-codex-subscription": "openai-codex",
		"2":                         "codex-cli",
		"openai-codex-cli":          "codex-cli",
		"3":                         "openai",
		"openai-api":                "openai",
		"4":                         "anthropic-claude-cli",
		"claude-cli":                "anthropic-claude-cli",
		"5":                         "anthropic",
		"anthropic-api":             "anthropic",
		"DeepSeek":                  "deepseek",
		"OpenCode Zen":              "opencode",
		"LM Studio":                 "lmstudio",
	}
	for choice, want := range cases {
		got, ok := resolveProviderChoice(choice)
		if !ok {
			t.Fatalf("resolveProviderChoice(%q) failed", choice)
		}
		if got != want {
			t.Fatalf("resolveProviderChoice(%q) = %q, want %q", choice, got, want)
		}
	}
}

func TestDefaultProviderChoiceFallsBackToFirstPreferredProvider(t *testing.T) {
	if got := defaultProviderChoice(""); got != "1" {
		t.Fatalf("default provider choice = %q, want 1", got)
	}
	if got := defaultProviderChoice("anthropic"); got != "5" {
		t.Fatalf("anthropic provider choice = %q, want 5", got)
	}
	if got := defaultProviderChoice("anthropic-claude-cli"); got != "4" {
		t.Fatalf("claude cli provider choice = %q, want 4", got)
	}
}
