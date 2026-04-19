package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceEnsureWriteRejectsNestedClaudeWorktreeOutsideActiveRoot(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, ".claude", "worktrees", "compassionate-goldberg", "completion.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	ws := Workspace{
		BaseRoot: base,
		Root:     base,
	}

	err := ws.EnsureWrite(target)
	if err == nil {
		t.Fatalf("expected nested worktree edit to be rejected")
	}
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch, got %v", err)
	}
}

func TestWorkspaceEnsureWriteAllowsActiveNestedClaudeWorktree(t *testing.T) {
	base := t.TempDir()
	activeRoot := filepath.Join(base, ".claude", "worktrees", "compassionate-goldberg")
	target := filepath.Join(activeRoot, "completion.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	ws := Workspace{
		BaseRoot: base,
		Root:     activeRoot,
	}

	if err := ws.EnsureWrite(target); err != nil {
		t.Fatalf("expected active worktree edit to be allowed, got %v", err)
	}
}

func TestReplaceInFileReturnsEditTargetMismatchWhenSearchTextMissing(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "completion.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewReplaceInFileTool(Workspace{BaseRoot: root, Root: root})

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "completion.go",
		"search":  "missing",
		"replace": "found",
	})
	if err == nil {
		t.Fatalf("expected replace failure")
	}
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch, got %v", err)
	}
}

func TestEditToolDescriptionsBiasTowardApplyPatch(t *testing.T) {
	ws := Workspace{}

	patchDesc := NewApplyPatchTool(ws).Definition().Description
	if !strings.Contains(patchDesc, "default edit tool") {
		t.Fatalf("expected apply_patch description to emphasize default usage, got %q", patchDesc)
	}

	replaceDesc := NewReplaceInFileTool(ws).Definition().Description
	if !strings.Contains(replaceDesc, "only for very small single-location substitutions") {
		t.Fatalf("expected replace_in_file description to emphasize narrow usage, got %q", replaceDesc)
	}
}

func TestApplyPatchRequiresPreviewApprovalBeforeWriting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package main\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	previewCalls := 0
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			previewCalls++
			if !strings.Contains(preview.Title, "Apply patch") {
				t.Fatalf("unexpected preview title: %q", preview.Title)
			}
			if !strings.Contains(preview.Preview, "Preview for main.go") {
				t.Fatalf("expected patch preview contents, got %q", preview.Preview)
			}
			return false, nil
		},
	}
	tool := NewApplyPatchTool(ws)

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+func main() {}\n*** End Patch\n",
	})
	if !errors.Is(err, ErrEditCanceled) {
		t.Fatalf("expected ErrEditCanceled, got %v", err)
	}
	if previewCalls != 1 {
		t.Fatalf("expected one preview confirmation, got %d", previewCalls)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Fatalf("expected file to remain unchanged after preview rejection, got %q", string(data))
	}
}

func TestToolRegistryExecuteWrapsMalformedJSONAsInvalidToolArguments(t *testing.T) {
	ws := Workspace{}
	registry := NewToolRegistry(NewWriteFileTool(ws))

	_, err := registry.Execute(context.Background(), "write_file", `{"path":"main.go","content":"package main`)
	if !errors.Is(err, ErrInvalidToolArgumentsJSON) {
		t.Fatalf("expected ErrInvalidToolArgumentsJSON, got %v", err)
	}
}

func TestRunShellRejectsWorkspaceMutatingCommands(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	_, err := tool.Execute(context.Background(), map[string]any{
		"command": "Set-Content test.txt 'hello'",
	})
	if err == nil {
		t.Fatalf("expected mutating shell command to be rejected")
	}
	if !strings.Contains(err.Error(), "run_shell cannot modify workspace files") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunShellAllowsReadOnlyCommands(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo hello",
	}); err != nil {
		t.Fatalf("expected read-only shell command to succeed, got %v", err)
	}
}

func TestRunShellAllowsExternalInstallCommands(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"command": "go install example.com/tool@latest",
	}); err == nil {
		t.Fatalf("expected external install command to reach the shell instead of the workspace-write guard")
	} else if strings.Contains(err.Error(), "run_shell cannot modify workspace files") {
		t.Fatalf("external install command should not be blocked as a workspace write: %v", err)
	}
}

func TestRunShellRejectsWorkspaceMutatingDependencyCommands(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	_, err := tool.Execute(context.Background(), map[string]any{
		"command": "npm install react",
	})
	if err == nil {
		t.Fatalf("expected workspace-mutating dependency command to be rejected")
	}
	if !strings.Contains(err.Error(), "run_shell cannot modify workspace files") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunShellAllowsScopedWorkspaceWriteWithinDeclaredPaths(t *testing.T) {
	root := t.TempDir()
	prepareCalls := 0
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PrepareEdit: func(reason string) error {
			prepareCalls++
			if !strings.Contains(reason, "scoped mutating shell") {
				t.Fatalf("unexpected prepare reason: %q", reason)
			}
			return nil
		},
	})

	command := "printf 'hello\\n' > allowed.txt"
	if runtime.GOOS == "windows" {
		command = "Set-Content allowed.txt 'hello'"
	}
	out, err := tool.Execute(context.Background(), map[string]any{
		"command":                command,
		"allow_workspace_writes": true,
		"write_paths":            []string{"allowed.txt"},
	})
	if err != nil {
		t.Fatalf("expected scoped mutating shell command to succeed, got %v", err)
	}
	if prepareCalls != 1 {
		t.Fatalf("expected prepare edit to run once, got %d", prepareCalls)
	}
	data, err := os.ReadFile(filepath.Join(root, "allowed.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Fatalf("expected allowed file to be written, got %q", string(data))
	}
	if !strings.Contains(out, "allowed.txt") {
		t.Fatalf("expected scoped write summary in output, got %q", out)
	}
}

func TestRunShellRejectsScopedWorkspaceWriteOutsideDeclaredPaths(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	command := "printf 'oops\\n' > blocked.txt"
	if runtime.GOOS == "windows" {
		command = "Set-Content blocked.txt 'oops'"
	}
	_, err := tool.Execute(context.Background(), map[string]any{
		"command":                command,
		"allow_workspace_writes": true,
		"write_paths":            []string{"allowed.txt"},
	})
	if err == nil {
		t.Fatalf("expected scoped mutating shell command to be rejected when it writes outside the declared paths")
	}
	if !strings.Contains(err.Error(), "outside write_paths") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunShellAllowsDescriptorRedirectionWithoutFileWrite(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	command := "echo hello 2>&1"
	if runtime.GOOS == "windows" {
		command = "Write-Output hello 2>&1"
	}
	if _, err := tool.Execute(context.Background(), map[string]any{
		"command": command,
	}); err != nil {
		t.Fatalf("expected descriptor redirection without file write to succeed, got %v", err)
	}
}

func TestRunShellStreamsProgressFromOutput(t *testing.T) {
	root := t.TempDir()
	var progress []string
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		ReportProgress: func(message string) {
			progress = append(progress, message)
		},
	})

	command := "printf 'alpha\\nomega\\n'"
	if runtime.GOOS == "windows" {
		command = "Write-Output alpha; Write-Output omega"
	}
	out, err := tool.Execute(context.Background(), map[string]any{
		"command": command,
	})
	if err != nil {
		t.Fatalf("expected streamed shell command to succeed, got %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "omega") {
		t.Fatalf("expected shell output to be captured, got %q", out)
	}
	found := false
	for _, item := range progress {
		if strings.HasPrefix(item, "run_shell output: ") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected run_shell output progress, got %#v", progress)
	}
}

func TestAssessShellCommandMutationClassifiesVerificationArtifactCommands(t *testing.T) {
	cases := map[string]shellMutationClass{
		"go test ./...":             shellMutationVerificationArtifacts,
		"go build ./cmd/app":        shellMutationVerificationArtifacts,
		"cargo test":                shellMutationVerificationArtifacts,
		"cargo check":               shellMutationVerificationArtifacts,
		"pytest":                    shellMutationVerificationArtifacts,
		"ctest --output-on-failure": shellMutationVerificationArtifacts,
		"cmake --build build":       shellMutationVerificationArtifacts,
		"msbuild demo.sln /m":       shellMutationVerificationArtifacts,
		"ninja":                     shellMutationVerificationArtifacts,
		"go list ./...":             shellMutationCacheOnly,
		"npm install react":         shellMutationWorkspaceWrite,
	}

	for command, want := range cases {
		got := assessShellCommandMutation(command)
		if got.Class != want {
			t.Fatalf("unexpected shell mutation class for %q: got %q want %q", command, got.Class, want)
		}
	}
}

func TestShellOutputCollectorTracksCarriageReturnProgress(t *testing.T) {
	var progress []string
	collector := newShellOutputCollector(Workspace{
		ReportProgress: func(message string) {
			progress = append(progress, message)
		},
	}, "demo command")

	collector.AppendBytes([]byte("step 1\rstep 2\rfinal\n"))

	if len(progress) == 0 {
		t.Fatalf("expected carriage-return progress updates")
	}
	if !strings.Contains(progress[len(progress)-1], "final") {
		t.Fatalf("expected final progress line to be tracked, got %#v", progress)
	}
	if !strings.Contains(collector.Text(), "final") {
		t.Fatalf("expected collector text to retain final output, got %q", collector.Text())
	}
}

func TestRunShellUsesWorkspaceDefaultTimeout(t *testing.T) {
	root := t.TempDir()
	command := "sleep 0.2"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Milliseconds 200"
	}
	tool := NewRunShellTool(Workspace{
		BaseRoot:     root,
		Root:         root,
		ShellTimeout: 50 * time.Millisecond,
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		"command": command,
	})
	if err == nil {
		t.Fatalf("expected shell command to time out")
	}
	if !strings.Contains(err.Error(), "command timed out after") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunBackgroundShellStartsAndCanBePolled(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunBackgroundShellTool(ws)
	checkTool := NewCheckShellJobTool(ws)

	command := "sleep 0.1; echo ready"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Milliseconds 100; Write-Output ready"
	}
	if _, err := runTool.Execute(context.Background(), map[string]any{
		"command": command,
	}); err != nil {
		t.Fatalf("run background shell: %v", err)
	}

	jobID := jobs.LatestJobID()
	if jobID == "" {
		t.Fatalf("expected a background job id")
	}

	var status string
	var output string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		result, err := checkTool.Execute(context.Background(), map[string]any{
			"job_id": jobID,
		})
		if err != nil {
			t.Fatalf("check shell job: %v", err)
		}
		output = result
		if strings.Contains(result, "status: completed") || strings.Contains(result, "status: failed") {
			status = result
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status == "" {
		t.Fatalf("expected background job to finish, last output: %q", output)
	}
	if !strings.Contains(status, "ready") {
		t.Fatalf("expected background job output to contain ready, got %q", status)
	}
}

func TestRunBackgroundShellReusesMatchingRunningJob(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunBackgroundShellTool(ws)

	command := "sleep 1; echo ready"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 1; Write-Output ready"
	}
	first, err := runTool.Execute(context.Background(), map[string]any{
		"command": command,
	})
	if err != nil {
		t.Fatalf("first background shell: %v", err)
	}
	second, err := runTool.Execute(context.Background(), map[string]any{
		"command": command,
	})
	if err != nil {
		t.Fatalf("second background shell: %v", err)
	}
	if !strings.Contains(second, "reusing background shell job") {
		t.Fatalf("expected reuse message, got %q", second)
	}
	snapshot := jobs.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected one reusable background job, got %d", len(snapshot))
	}
	if !strings.Contains(first, snapshot[0].ID) || !strings.Contains(second, snapshot[0].ID) {
		t.Fatalf("expected both outputs to reference the same job id, got first=%q second=%q", first, second)
	}
}

func TestRunShellBundleBackgroundStartsParallelJobsAndCheckBundleSummarizes(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunShellBundleBackgroundTool(ws)
	checkTool := NewCheckShellBundleTool(ws)

	commands := []string{"go version", "go env GOOS"}
	if runtime.GOOS == "windows" {
		commands = []string{"Write-Output alpha", "Write-Output beta"}
	}
	started, err := runTool.Execute(context.Background(), map[string]any{
		"commands": commands,
	})
	if err != nil {
		t.Fatalf("run shell bundle: %v", err)
	}
	if !strings.Contains(started, "started background shell bundle") {
		t.Fatalf("unexpected bundle start output: %q", started)
	}
	if !strings.Contains(started, "bundle: ") {
		t.Fatalf("expected bundle id in start output, got %q", started)
	}
	snapshot := jobs.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 background jobs, got %d", len(snapshot))
	}
	bundleSnapshot := jobs.SnapshotBundles()
	if len(bundleSnapshot) != 1 {
		t.Fatalf("expected 1 background bundle, got %d", len(bundleSnapshot))
	}

	var summary string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		result, err := checkTool.Execute(context.Background(), map[string]any{
			"bundle_id": "latest",
		})
		if err != nil {
			t.Fatalf("check shell bundle: %v", err)
		}
		summary = result
		if strings.Contains(strings.ToLower(result), "running=0") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(strings.ToLower(summary), "summary:") {
		t.Fatalf("expected bundle summary, got %q", summary)
	}
	if !strings.Contains(strings.ToLower(summary), "bundle_status:") {
		t.Fatalf("expected bundle status, got %q", summary)
	}
	if !strings.Contains(strings.ToLower(summary), "total=2") {
		t.Fatalf("expected total count in bundle summary, got %q", summary)
	}
}

func TestRunShellBundleBackgroundReusesExistingRunningBundle(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunShellBundleBackgroundTool(ws)

	commands := []string{"sleep 1; echo alpha", "sleep 1; echo beta"}
	if runtime.GOOS == "windows" {
		commands = []string{"Start-Sleep -Seconds 1; Write-Output alpha", "Start-Sleep -Seconds 1; Write-Output beta"}
	}

	first, err := runTool.Execute(context.Background(), map[string]any{
		"commands": commands,
	})
	if err != nil {
		t.Fatalf("first shell bundle: %v", err)
	}
	second, err := runTool.Execute(context.Background(), map[string]any{
		"commands": commands,
	})
	if err != nil {
		t.Fatalf("second shell bundle: %v", err)
	}

	bundles := jobs.SnapshotBundles()
	if len(bundles) != 1 {
		t.Fatalf("expected one reusable background bundle, got %d", len(bundles))
	}
	if !strings.Contains(first, bundles[0].ID) || !strings.Contains(second, bundles[0].ID) {
		t.Fatalf("expected both outputs to reference the same bundle id, got first=%q second=%q", first, second)
	}
}

func TestRunShellBundleBackgroundExecuteDetailedReturnsStructuredMeta(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunShellBundleBackgroundTool(ws)

	commands := []string{"go version", "go env GOOS"}
	if runtime.GOOS == "windows" {
		commands = []string{"Write-Output alpha", "Write-Output beta"}
	}
	result, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"commands": commands,
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if strings.TrimSpace(result.DisplayText) == "" {
		t.Fatalf("expected display text in detailed result")
	}
	if toolMetaString(result.Meta, "bundle_id") == "" {
		t.Fatalf("expected bundle id in structured meta, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "bundle_status") == "" {
		t.Fatalf("expected bundle status in structured meta, got %#v", result.Meta)
	}
	jobIDs := toolMetaStringSlice(result.Meta, "bundle_job_ids")
	if len(jobIDs) != 2 {
		t.Fatalf("expected 2 bundle job ids, got %#v", result.Meta)
	}
}

func TestCheckShellJobExecuteDetailedIncludesBundleMeta(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunBackgroundShellTool(ws)
	checkTool := NewCheckShellJobTool(ws)

	command := "go version"
	if runtime.GOOS == "windows" {
		command = "Write-Output alpha"
	}
	start, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"command": command,
	})
	if err != nil {
		t.Fatalf("run background shell detailed: %v", err)
	}
	jobID := toolMetaString(start.Meta, "job_id")
	if jobID == "" {
		t.Fatalf("expected job id in start meta, got %#v", start.Meta)
	}
	result, err := checkTool.ExecuteDetailed(context.Background(), map[string]any{
		"job_id": jobID,
	})
	if err != nil {
		t.Fatalf("check shell job detailed: %v", err)
	}
	if toolMetaString(result.Meta, "bundle_id") == "" {
		t.Fatalf("expected bundle id in check-shell-job meta, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "job_id") != jobID {
		t.Fatalf("expected stable job id in result meta, got %#v", result.Meta)
	}
}

func TestRunBackgroundShellReuseRunsPostHookAndReportsActualStatus(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	postHooks := 0
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			_ = payload
			if event == HookPostToolUse {
				postHooks++
			}
			return HookVerdict{}, nil
		},
	}
	runTool := NewRunBackgroundShellTool(ws)

	command := "sleep 1; echo ready"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 1; Write-Output ready"
	}
	first, err := runTool.Execute(context.Background(), map[string]any{
		"command": command,
	})
	if err != nil {
		t.Fatalf("first background shell: %v", err)
	}
	second, err := runTool.Execute(context.Background(), map[string]any{
		"command": command,
	})
	if err != nil {
		t.Fatalf("second background shell: %v", err)
	}

	if !strings.Contains(first, "status: running") {
		t.Fatalf("expected actual running status in first output, got %q", first)
	}
	if !strings.Contains(first, "status_file:") {
		t.Fatalf("expected explicit status file in first output, got %q", first)
	}
	if !strings.Contains(second, "reusing background shell job") {
		t.Fatalf("expected reuse message, got %q", second)
	}
	if postHooks != 2 {
		t.Fatalf("expected HookPostToolUse for both start and reuse paths, got %d", postHooks)
	}
}

func TestRunBackgroundShellExecuteDetailedCarriesOwnerNodeID(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunBackgroundShellTool(ws)

	command := "go version"
	if runtime.GOOS == "windows" {
		command = "Write-Output alpha"
	}
	result, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"command":       command,
		"owner_node_id": "plan-02",
	})
	if err != nil {
		t.Fatalf("run background shell detailed: %v", err)
	}
	if toolMetaString(result.Meta, "owner_node_id") != "plan-02" {
		t.Fatalf("expected owner node id in meta, got %#v", result.Meta)
	}
	bundles := jobs.SnapshotBundles()
	if len(bundles) != 1 || bundles[0].OwnerNodeID != "plan-02" {
		t.Fatalf("expected owner node id on persisted bundle, got %#v", bundles)
	}
}

func TestCancelShellBundleStopsRunningJobs(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunBackgroundShellTool(ws)
	cancelTool := NewCancelShellBundleTool(ws)

	command := "sleep 5; echo test-ready"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 5; Write-Output test-ready"
	}
	started, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"command": command,
	})
	if err != nil {
		t.Fatalf("run background shell detailed: %v", err)
	}
	bundleID := toolMetaString(started.Meta, "bundle_id")
	if bundleID == "" {
		t.Fatalf("expected bundle id in start meta, got %#v", started.Meta)
	}

	result, err := cancelTool.ExecuteDetailed(context.Background(), map[string]any{
		"bundle_id": bundleID,
		"reason":    "Newer verification replaced this run.",
	})
	if err != nil {
		t.Fatalf("cancel shell bundle detailed: %v", err)
	}
	if toolMetaString(result.Meta, "bundle_status") != "canceled" {
		t.Fatalf("expected canceled bundle status, got %#v", result.Meta)
	}
	snapshotBundles := jobs.SnapshotBundles()
	if len(snapshotBundles) != 1 || snapshotBundles[0].Status != "canceled" {
		t.Fatalf("expected persisted canceled bundle, got %#v", snapshotBundles)
	}
	snapshotJobs := jobs.Snapshot()
	if len(snapshotJobs) != 1 || (snapshotJobs[0].Status != "canceled" && snapshotJobs[0].Status != "preempted") {
		t.Fatalf("expected canceled or preempted job, got %#v", snapshotJobs)
	}
}

func TestMarkBackgroundBundlesStalePreemptsRunningBundle(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunBackgroundShellTool(ws)
	command := "sleep 5; echo test-ready"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 5; Write-Output test-ready"
	}
	started, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"command":       command,
		"owner_node_id": "plan-02",
	})
	if err != nil {
		t.Fatalf("run background shell detailed: %v", err)
	}
	bundleID := toolMetaString(started.Meta, "bundle_id")
	jobID := toolMetaString(started.Meta, "job_id")
	if bundleID == "" || jobID == "" {
		t.Fatalf("expected bundle/job ids, got %#v", started.Meta)
	}
	agent := &Agent{
		Config:    Config{},
		Session:   session,
		Workspace: ws,
	}
	agent.markBackgroundBundlesStale("A newer edit invalidated the previous verification.")

	bundle, ok := session.BackgroundBundle(bundleID)
	if !ok || bundle.Status != "stale" {
		t.Fatalf("expected stale bundle after preemption, got %#v", bundle)
	}
	job, ok := session.BackgroundJob(jobID)
	if !ok || job.Status != "preempted" {
		t.Fatalf("expected preempted job after stale invalidation, got %#v", job)
	}
}

func TestApplyPatchUpdatesFallbackTargetWhenSourceExistsAtBaseRoot(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(base, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewApplyPatchTool(Workspace{
		BaseRoot: base,
		Root:     current,
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+func main() {}\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "func main() {}") {
		t.Fatalf("expected fallback patch target update, got %q", string(data))
	}
}
