package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadUserCommandsDiscoversWorkspaceCommands(t *testing.T) {
	dir := t.TempDir()
	isolateUserConfigDir(t)
	cmdDir := filepath.Join(dir, ".kernforge", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatalf("mkdir commands dir: %v", err)
	}
	content := "---\n" +
		"description: Summarize the open PR risks.\n" +
		"argument-hint: <pr-number>\n" +
		"---\n" +
		"Review PR $ARGUMENTS and list the top risks.\n"
	if err := os.WriteFile(filepath.Join(cmdDir, "pr-risks.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write command file: %v", err)
	}

	set, warnings := LoadUserCommands(dir, nil)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	cmd, ok := set.Lookup("pr-risks")
	if !ok {
		t.Fatalf("expected pr-risks command, items=%#v", set.Items())
	}
	if cmd.Description != "Summarize the open PR risks." {
		t.Fatalf("expected frontmatter description, got %q", cmd.Description)
	}
	if cmd.ArgHint != "<pr-number>" {
		t.Fatalf("expected argument-hint, got %q", cmd.ArgHint)
	}
	prompt := cmd.RenderPrompt("123")
	if prompt != "Review PR 123 and list the top risks." {
		t.Fatalf("expected argument substitution, got %q", prompt)
	}
}

func TestLoadUserCommandsSkipsBuiltinNameCollision(t *testing.T) {
	dir := t.TempDir()
	isolateUserConfigDir(t)
	cmdDir := filepath.Join(dir, ".kernforge", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatalf("mkdir commands dir: %v", err)
	}
	// "review" is a built-in command and must not be shadowed.
	if err := os.WriteFile(filepath.Join(cmdDir, "review.md"), []byte("do a custom review\n"), 0o644); err != nil {
		t.Fatalf("write command file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "triage.md"), []byte("triage the failure\n"), 0o644); err != nil {
		t.Fatalf("write command file: %v", err)
	}

	set, warnings := LoadUserCommands(dir, nil)
	if _, ok := set.Lookup("review"); ok {
		t.Fatalf("built-in command name should not be overridden by user command")
	}
	if _, ok := set.Lookup("triage"); !ok {
		t.Fatalf("expected non-colliding command triage to load")
	}
	foundCollisionWarning := false
	for _, warning := range warnings {
		if strings.Contains(warning, "review") && strings.Contains(warning, "built-in") {
			foundCollisionWarning = true
		}
	}
	if !foundCollisionWarning {
		t.Fatalf("expected a built-in collision warning, got %v", warnings)
	}
}

func TestLoadUserCommandsWorkspaceOverridesUserGlobal(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// User-global command.
	userDir := filepath.Join(userConfigDir(), "commands")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("mkdir user commands dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "deploy.md"), []byte("global deploy body\n"), 0o644); err != nil {
		t.Fatalf("write user command: %v", err)
	}

	// Workspace command with the same name should win because the workspace
	// path is searched first.
	wsDir := filepath.Join(dir, ".kernforge", "commands")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace commands dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "deploy.md"), []byte("workspace deploy body\n"), 0o644); err != nil {
		t.Fatalf("write workspace command: %v", err)
	}

	set, _ := LoadUserCommands(dir, nil)
	cmd, ok := set.Lookup("deploy")
	if !ok {
		t.Fatalf("expected deploy command, items=%#v", set.Items())
	}
	if cmd.RenderPrompt("") != "workspace deploy body" {
		t.Fatalf("expected workspace command to win, got %q", cmd.RenderPrompt(""))
	}
}

func TestRenderPromptAppendsArgumentsWithoutToken(t *testing.T) {
	cmd := UserCommand{Name: "note", Body: "Take a careful note."}
	// Without a token, extra args are appended so user input is preserved.
	got := cmd.RenderPrompt("about the parser")
	if got != "Take a careful note.\n\nabout the parser" {
		t.Fatalf("unexpected appended prompt: %q", got)
	}
	if cmd.RenderPrompt("") != "Take a careful note." {
		t.Fatalf("expected body only when no args, got %q", cmd.RenderPrompt(""))
	}
}

func TestCompletionCommandNamesMergesUserCommands(t *testing.T) {
	set, _ := loadUserCommandsFromFixtures(t, map[string]string{
		"deploy-staging.md": "deploy to staging\n",
		"review.md":         "shadowed built-in\n", // collides, must be dropped
	})
	rt := &runtimeState{userCommands: set}
	names := rt.completionCommandNames()
	if !containsStringValue(names, "deploy-staging") {
		t.Fatalf("expected merged user command in completion names")
	}
	// "review" appears once (as the built-in), never duplicated by the user file.
	count := 0
	for _, name := range names {
		if name == "review" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one review entry, got %d", count)
	}
}

func loadUserCommandsFromFixtures(t *testing.T, files map[string]string) (UserCommandSet, []string) {
	t.Helper()
	dir := t.TempDir()
	isolateUserConfigDir(t)
	cmdDir := filepath.Join(dir, ".kernforge", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatalf("mkdir commands dir: %v", err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(cmdDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return LoadUserCommands(dir, nil)
}

func containsStringValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
