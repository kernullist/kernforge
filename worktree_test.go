package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceResolveAllowsActiveRootOutsideBaseRoot(t *testing.T) {
	baseRoot := t.TempDir()
	activeRoot := t.TempDir()
	target := filepath.Join(activeRoot, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ws := Workspace{
		BaseRoot: baseRoot,
		Root:     activeRoot,
	}

	resolved, err := ws.Resolve("main.go")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(target)) {
		t.Fatalf("expected %s, got %s", target, resolved)
	}
}

func TestWorktreeManagerCreateAndRemove(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	repoRoot := t.TempDir()
	ctx := context.Background()

	if _, err := runGitCommand(ctx, repoRoot, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "config", "user.email", "kernforge-test@example.com"); err != nil {
		t.Fatalf("git config user.email: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "config", "user.name", "Kernforge Test"); err != nil {
		t.Fatalf("git config user.name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "add", "README.md"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	cfg := DefaultConfig(repoRoot)
	cfg.WorktreeIsolation.RootDir = filepath.Join(t.TempDir(), "managed")
	manager := newWorktreeManager(cfg)

	worktree, err := manager.Create(ctx, repoRoot, "Driver Fix")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !worktree.Managed || !worktree.Active {
		t.Fatalf("expected managed active worktree, got %#v", worktree)
	}
	if !strings.HasPrefix(worktree.Branch, "kernforge/driver-fix-") {
		t.Fatalf("unexpected branch name: %s", worktree.Branch)
	}
	if _, err := os.Stat(filepath.Join(worktree.Root, "README.md")); err != nil {
		t.Fatalf("expected worktree checkout to contain tracked files: %v", err)
	}

	if err := manager.Remove(ctx, repoRoot, worktree); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(worktree.Root); !os.IsNotExist(err) {
		t.Fatalf("expected worktree root to be removed, got err=%v", err)
	}
}

func TestResolveEditTargetRoutesOwnedEditsIntoSpecialistWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	repoRoot := t.TempDir()
	ctx := context.Background()

	if _, err := runGitCommand(ctx, repoRoot, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "config", "user.email", "kernforge-test@example.com"); err != nil {
		t.Fatalf("git config user.email: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "config", "user.name", "Kernforge Test"); err != nil {
		t.Fatalf("git config user.name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile main.go: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "add", "main.go"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	cfg := DefaultConfig(repoRoot)
	cfg.WorktreeIsolation.Enabled = boolPtr(true)
	cfg.WorktreeIsolation.RootDir = filepath.Join(t.TempDir(), "managed")
	sess := NewSession(repoRoot, "openai", "gpt-5.4", "", "default")
	sess.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{
				ID:          "plan-01",
				Title:       "Implement specialist ownership routing",
				Kind:        "edit",
				Status:      "ready",
				LastUpdated: time.Now(),
			},
		},
		LastUpdated: time.Now(),
	}

	rt := &runtimeState{
		cfg:     cfg,
		session: sess,
		workspace: Workspace{
			BaseRoot: repoRoot,
			Root:     repoRoot,
		},
	}
	rt.syncWorkspaceFromSession()

	route, err := rt.resolveEditTarget(EditRoutingRequest{
		Path:        "main.go",
		OwnerNodeID: "plan-01",
		ForLookup:   true,
	})
	if err != nil {
		t.Fatalf("resolveEditTarget: %v", err)
	}
	if route.Specialist != "implementation-owner" {
		t.Fatalf("expected implementation-owner route, got %#v", route)
	}
	if strings.TrimSpace(route.WorktreeRoot) == "" {
		t.Fatalf("expected a specialist worktree root, got %#v", route)
	}
	if sameFilePath(route.WorktreeRoot, repoRoot) {
		t.Fatalf("expected owned edits to use a dedicated worktree root")
	}
	if _, err := os.Stat(route.AbsolutePath); err != nil {
		t.Fatalf("expected routed file to exist inside specialist worktree: %v", err)
	}
	if len(rt.session.SpecialistWorktrees) != 1 {
		t.Fatalf("expected one specialist worktree lease, got %#v", rt.session.SpecialistWorktrees)
	}
	node, ok := rt.session.TaskGraph.Node("plan-01")
	if !ok {
		t.Fatalf("expected task node to remain available")
	}
	if !strings.EqualFold(node.EditableSpecialist, "implementation-owner") {
		t.Fatalf("expected editable specialist metadata to be recorded, got %#v", node)
	}
	if !strings.EqualFold(node.EditableWorktreeRoot, route.WorktreeRoot) {
		t.Fatalf("expected node worktree root to match routed root, got %#v", node)
	}

	manager := newWorktreeManager(cfg)
	t.Cleanup(func() {
		_ = manager.Remove(context.Background(), repoRoot, SessionWorktree{
			Root:    route.WorktreeRoot,
			Branch:  rt.session.SpecialistWorktrees[0].Branch,
			Managed: true,
		})
	})
}

func TestResolveEditTargetRejectsPathOutsideSpecialistOwnership(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	repoRoot := t.TempDir()
	ctx := context.Background()

	if _, err := runGitCommand(ctx, repoRoot, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "config", "user.email", "kernforge-test@example.com"); err != nil {
		t.Fatalf("git config user.email: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "config", "user.name", "Kernforge Test"); err != nil {
		t.Fatalf("git config user.name: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "telemetry"), 0o755); err != nil {
		t.Fatalf("MkdirAll telemetry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "telemetry", "provider.man"), []byte("<manifest/>\n"), 0o644); err != nil {
		t.Fatalf("WriteFile provider.man: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "add", "main.go", "telemetry/provider.man"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	cfg := DefaultConfig(repoRoot)
	cfg.WorktreeIsolation.Enabled = boolPtr(true)
	cfg.WorktreeIsolation.RootDir = filepath.Join(t.TempDir(), "managed")
	sess := NewSession(repoRoot, "openai", "gpt-5.4", "", "default")
	sess.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{
				ID:                     "plan-01",
				Title:                  "Update telemetry assets",
				Kind:                   "edit",
				Status:                 "ready",
				EditableSpecialist:     "telemetry-analyst",
				EditableOwnershipPaths: []string{"telemetry/**", "*.man"},
				LastUpdated:            time.Now(),
			},
		},
		LastUpdated: time.Now(),
	}

	rt := &runtimeState{
		cfg:     cfg,
		session: sess,
		workspace: Workspace{
			BaseRoot: repoRoot,
			Root:     repoRoot,
		},
	}
	rt.syncWorkspaceFromSession()

	manager := newWorktreeManager(cfg)
	t.Cleanup(func() {
		for _, lease := range rt.session.SpecialistWorktrees {
			_ = manager.Remove(context.Background(), repoRoot, SessionWorktree{
				Root:    lease.Root,
				Branch:  lease.Branch,
				Managed: true,
			})
		}
	})

	_, err := rt.resolveEditTarget(EditRoutingRequest{
		Path:        "main.go",
		OwnerNodeID: "plan-01",
		ForLookup:   true,
	})
	if err == nil {
		t.Fatalf("expected editable ownership rejection")
	}
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch, got %v", err)
	}
	if !strings.Contains(err.Error(), "editable ownership") {
		t.Fatalf("expected editable ownership guidance, got %v", err)
	}
}
