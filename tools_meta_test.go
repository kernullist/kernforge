package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadFileExecuteDetailedReturnsStructuredMeta(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewReadFileTool(Workspace{BaseRoot: root, Root: root})
	registry := NewToolRegistry(tool)

	result, err := registry.ExecuteDetailed(context.Background(), "read_file", `{"path":"sample.txt","start_line":2,"end_line":3}`)
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if toolMetaString(result.Meta, "effect") != "inspect" {
		t.Fatalf("expected inspect effect, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "path") != "sample.txt" {
		t.Fatalf("expected relative path metadata, got %#v", result.Meta)
	}
	if toolMetaInt(result.Meta, "line_count") != 2 {
		t.Fatalf("expected line count 2, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "cache_mode") != "fresh" {
		t.Fatalf("expected fresh cache mode, got %#v", result.Meta)
	}
}

func TestRunShellExecuteDetailedReturnsStructuredMeta(t *testing.T) {
	root := t.TempDir()
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
	}
	registry := NewToolRegistry(NewRunShellTool(ws))
	command := "echo alpha"
	if runtime.GOOS == "windows" {
		command = "Write-Output alpha"
	}

	jsonInput := `{"command":"` + strings.ReplaceAll(command, `\`, `\\`) + `"}`
	result, err := registry.ExecuteDetailed(context.Background(), "run_shell", jsonInput)
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if toolMetaString(result.Meta, "mutation_class") != string(shellMutationReadOnly) {
		t.Fatalf("expected read_only mutation class, got %#v", result.Meta)
	}
	if !toolMetaBool(result.Meta, "success") {
		t.Fatalf("expected success flag, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "command") == "" {
		t.Fatalf("expected command metadata, got %#v", result.Meta)
	}
}

func TestUpdatePlanExecuteDetailedReturnsCounts(t *testing.T) {
	root := t.TempDir()
	var captured []PlanItem
	tool := NewUpdatePlanTool(Workspace{
		BaseRoot: root,
		Root:     root,
		UpdatePlan: func(items []PlanItem) {
			captured = append([]PlanItem(nil), items...)
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"items": []any{
			map[string]any{"step": "Inspect", "status": "completed"},
			map[string]any{"step": "Fix", "status": "in_progress"},
			map[string]any{"step": "Verify", "status": "pending"},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if len(captured) != 3 {
		t.Fatalf("expected captured plan items, got %#v", captured)
	}
	if toolMetaString(result.Meta, "effect") != "plan" {
		t.Fatalf("expected plan effect, got %#v", result.Meta)
	}
	if toolMetaInt(result.Meta, "completed_count") != 1 || toolMetaInt(result.Meta, "in_progress_count") != 1 || toolMetaInt(result.Meta, "pending_count") != 1 {
		t.Fatalf("expected balanced plan counts, got %#v", result.Meta)
	}
}

func TestApplyPatchExecuteDetailedReturnsChangedPathsMeta(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: main.go",
			"@@",
			" package main",
			"+func main() {}",
			"*** End Patch",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	changedPaths := toolMetaStringSlice(result.Meta, "changed_paths")
	if len(changedPaths) != 1 || changedPaths[0] != "main.go" {
		t.Fatalf("expected changed path metadata, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "effect") != "edit" {
		t.Fatalf("expected edit effect, got %#v", result.Meta)
	}
}

func TestGitAddExecuteDetailedReturnsStructuredMeta(t *testing.T) {
	repo := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	registry := NewToolRegistry(NewGitAddTool(Workspace{BaseRoot: repo, Root: repo}))
	payload, err := json.Marshal(map[string]any{
		"paths": []string{"feature.txt"},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := registry.ExecuteDetailed(context.Background(), "git_add", string(payload))
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if toolMetaString(result.Meta, "effect") != "git_mutation" {
		t.Fatalf("expected git_mutation effect, got %#v", result.Meta)
	}
	if !toolMetaBool(result.Meta, "staged") || toolMetaInt(result.Meta, "staged_count") != 1 {
		t.Fatalf("expected staged metadata, got %#v", result.Meta)
	}
	paths := toolMetaStringSlice(result.Meta, "paths")
	if len(paths) != 1 || paths[0] != "feature.txt" {
		t.Fatalf("expected staged paths metadata, got %#v", result.Meta)
	}
}

func TestGitCommitExecuteDetailedReturnsStructuredMeta(t *testing.T) {
	repo := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws := Workspace{BaseRoot: repo, Root: repo}
	if _, err := NewGitAddTool(ws).Execute(context.Background(), map[string]any{
		"paths": []any{"feature.txt"},
	}); err != nil {
		t.Fatalf("git_add: %v", err)
	}
	registry := NewToolRegistry(NewGitCommitTool(ws))
	payload, err := json.Marshal(map[string]any{
		"message": "Add feature file",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := registry.ExecuteDetailed(context.Background(), "git_commit", string(payload))
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if !toolMetaBool(result.Meta, "created_commit") {
		t.Fatalf("expected created_commit metadata, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "commit_subject") != "Add feature file" {
		t.Fatalf("expected commit subject metadata, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "commit_sha") == "" {
		t.Fatalf("expected commit sha metadata, got %#v", result.Meta)
	}
}

func TestGitPushExecuteDetailedReturnsStructuredMeta(t *testing.T) {
	repo := initTestGitRepo(t)
	remote := initBareRemote(t)
	mustRunGit(t, repo, "remote", "add", "origin", remote)
	mustRunGit(t, repo, "checkout", "-b", "feature/push-meta")
	if err := os.WriteFile(filepath.Join(repo, "push.txt"), []byte("push\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws := Workspace{BaseRoot: repo, Root: repo}
	if _, err := NewGitAddTool(ws).Execute(context.Background(), map[string]any{
		"paths": []any{"push.txt"},
	}); err != nil {
		t.Fatalf("git_add: %v", err)
	}
	if _, err := NewGitCommitTool(ws).Execute(context.Background(), map[string]any{
		"message": "Add push meta file",
	}); err != nil {
		t.Fatalf("git_commit: %v", err)
	}
	registry := NewToolRegistry(NewGitPushTool(ws))
	payload, err := json.Marshal(map[string]any{
		"remote": "origin",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := registry.ExecuteDetailed(context.Background(), "git_push", string(payload))
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if !toolMetaBool(result.Meta, "pushed") {
		t.Fatalf("expected pushed metadata, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "remote") != "origin" || toolMetaString(result.Meta, "branch") != "feature/push-meta" {
		t.Fatalf("expected remote/branch metadata, got %#v", result.Meta)
	}
}

func TestGitCreatePRExecuteDetailedReturnsStructuredMeta(t *testing.T) {
	repo := initTestGitRepo(t)
	remote := initBareRemote(t)
	mustRunGit(t, repo, "remote", "add", "origin", remote)
	mustRunGit(t, repo, "checkout", "-b", "feature/pr-meta")
	if err := os.WriteFile(filepath.Join(repo, "pr.txt"), []byte("pr\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws := Workspace{BaseRoot: repo, Root: repo}
	if _, err := NewGitAddTool(ws).Execute(context.Background(), map[string]any{
		"paths": []any{"pr.txt"},
	}); err != nil {
		t.Fatalf("git_add: %v", err)
	}
	if _, err := NewGitCommitTool(ws).Execute(context.Background(), map[string]any{
		"message": "Add pr meta file",
	}); err != nil {
		t.Fatalf("git_commit: %v", err)
	}

	capturePath := filepath.Join(t.TempDir(), "gh_args.txt")
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	installFakeGh(t, binDir)
	t.Setenv("GH_ARGS_FILE", capturePath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	registry := NewToolRegistry(NewGitCreatePRTool(ws))
	payload, err := json.Marshal(map[string]any{
		"title":       "Feature PR",
		"body":        "Meta coverage.",
		"base_branch": "main",
		"draft":       true,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := registry.ExecuteDetailed(context.Background(), "git_create_pr", string(payload))
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if !toolMetaBool(result.Meta, "pr_created") {
		t.Fatalf("expected pr_created metadata, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "pr_url") != "https://github.com/example/repo/pull/123" {
		t.Fatalf("expected PR URL metadata, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "branch") != "feature/pr-meta" || !toolMetaBool(result.Meta, "draft") {
		t.Fatalf("expected branch/draft metadata, got %#v", result.Meta)
	}
}
