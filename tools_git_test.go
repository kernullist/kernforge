package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGitAddAndCommitTool(t *testing.T) {
	repo := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}

	ws := Workspace{Root: repo, BaseRoot: repo}
	if _, err := NewGitAddTool(ws).Execute(context.Background(), map[string]any{
		"paths": []any{"feature.txt"},
	}); err != nil {
		t.Fatalf("git_add: %v", err)
	}
	if _, err := NewGitCommitTool(ws).Execute(context.Background(), map[string]any{
		"message": "Add feature file",
	}); err != nil {
		t.Fatalf("git_commit: %v", err)
	}

	subject := mustRunGit(t, repo, "log", "-1", "--pretty=%s")
	if subject != "Add feature file" {
		t.Fatalf("unexpected commit subject: %q", subject)
	}
}

func TestGitPushToolSetsUpstream(t *testing.T) {
	repo := initTestGitRepo(t)
	remote := initBareRemote(t)
	mustRunGit(t, repo, "remote", "add", "origin", remote)
	mustRunGit(t, repo, "checkout", "-b", "feature/push-test")
	if err := os.WriteFile(filepath.Join(repo, "push.txt"), []byte("push\n"), 0o644); err != nil {
		t.Fatalf("write push file: %v", err)
	}

	ws := Workspace{Root: repo, BaseRoot: repo}
	if _, err := NewGitAddTool(ws).Execute(context.Background(), map[string]any{
		"paths": []any{"push.txt"},
	}); err != nil {
		t.Fatalf("git_add: %v", err)
	}
	if _, err := NewGitCommitTool(ws).Execute(context.Background(), map[string]any{
		"message": "Add push file",
	}); err != nil {
		t.Fatalf("git_commit: %v", err)
	}
	if _, err := NewGitPushTool(ws).Execute(context.Background(), map[string]any{
		"remote": "origin",
	}); err != nil {
		t.Fatalf("git_push: %v", err)
	}

	upstream := mustRunGit(t, repo, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if upstream != "origin/feature/push-test" {
		t.Fatalf("unexpected upstream: %q", upstream)
	}
}

func TestGitCreatePRToolUsesGhAndPushesBranch(t *testing.T) {
	repo := initTestGitRepo(t)
	remote := initBareRemote(t)
	mustRunGit(t, repo, "remote", "add", "origin", remote)
	mustRunGit(t, repo, "checkout", "-b", "feature/pr-test")
	if err := os.WriteFile(filepath.Join(repo, "pr.txt"), []byte("pr\n"), 0o644); err != nil {
		t.Fatalf("write pr file: %v", err)
	}

	ws := Workspace{Root: repo, BaseRoot: repo}
	if _, err := NewGitAddTool(ws).Execute(context.Background(), map[string]any{
		"paths": []any{"pr.txt"},
	}); err != nil {
		t.Fatalf("git_add: %v", err)
	}
	if _, err := NewGitCommitTool(ws).Execute(context.Background(), map[string]any{
		"message": "Add pr file",
	}); err != nil {
		t.Fatalf("git_commit: %v", err)
	}

	capturePath := filepath.Join(t.TempDir(), "gh_args.txt")
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	installFakeGh(t, binDir)
	t.Setenv("GH_ARGS_FILE", capturePath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, err := NewGitCreatePRTool(ws).Execute(context.Background(), map[string]any{
		"title":       "Feature PR",
		"body":        "Implements commit and PR flow.",
		"base_branch": "main",
		"draft":       true,
	})
	if err != nil {
		t.Fatalf("git_create_pr: %v", err)
	}
	if !strings.Contains(out, "https://github.com/example/repo/pull/123") {
		t.Fatalf("expected PR URL in output, got: %s", out)
	}

	argsBytes, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read gh args: %v", err)
	}
	argsText := string(argsBytes)
	for _, needle := range []string{
		"pr create",
		"--head feature/pr-test",
		"--base main",
		"--draft",
		"--title \"Feature PR\"",
		"--body \"Implements commit and PR flow.\"",
	} {
		if !strings.Contains(argsText, needle) {
			t.Fatalf("expected gh args to contain %q, got %q", needle, argsText)
		}
	}

	upstream := mustRunGit(t, repo, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if upstream != "origin/feature/pr-test" {
		t.Fatalf("unexpected upstream after PR creation: %q", upstream)
	}
}

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	mustRunGit(t, repo, "init", "-b", "main")
	mustRunGit(t, repo, "config", "user.name", "Kernforge Test")
	mustRunGit(t, repo, "config", "user.email", "kernforge@example.com")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	mustRunGit(t, repo, "add", "README.md")
	mustRunGit(t, repo, "commit", "-m", "Initial commit")
	return repo
}

func initBareRemote(t *testing.T) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	mustRunGit(t, t.TempDir(), "init", "--bare", remote)
	return remote
}

func mustRunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGitCommand(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func installFakeGh(t *testing.T, binDir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		path := filepath.Join(binDir, "gh.cmd")
		text := "@echo off\r\n" +
			"echo %* > \"%GH_ARGS_FILE%\"\r\n" +
			"echo https://github.com/example/repo/pull/123\r\n"
		if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
			t.Fatalf("write fake gh: %v", err)
		}
		return
	}
	path := filepath.Join(binDir, "gh")
	text := "#!/bin/sh\n" +
		"printf '%s' \"$*\" > \"$GH_ARGS_FILE\"\n" +
		"printf '%s\n' 'https://github.com/example/repo/pull/123'\n"
	if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
}
