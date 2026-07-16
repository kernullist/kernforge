package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Grok-aligned pipeline: config deny rules still apply in full mode.
func TestFullModeHonorsConfigDenyRules(t *testing.T) {
	perms := NewPermissionManager(ModeBypass, func(string) (bool, error) {
		t.Fatalf("full mode deny rule must not prompt")
		return false, nil
	})
	if err := perms.ApplyConfigRules(PermissionRulesConfig{
		Shell: map[string]string{`rm\s+-rf`: "deny"},
		Edit:  map[string]string{"secrets": "deny"},
	}); err != nil {
		t.Fatalf("ApplyConfigRules: %v", err)
	}
	if ok, err := perms.Allow(ActionShell, "rm -rf /tmp/x"); ok || err == nil {
		t.Fatalf("full mode must honor shell deny rule, got ok=%v err=%v", ok, err)
	}
	if ok, err := perms.Allow(ActionWrite, "secrets/key.pem"); ok || err == nil {
		t.Fatalf("full mode must honor edit deny rule, got ok=%v err=%v", ok, err)
	}
	// Unrelated actions still auto-approve in full.
	if ok, err := perms.Allow(ActionShell, "echo hi"); !ok || err != nil {
		t.Fatalf("full mode must auto-allow unrelated shell, got ok=%v err=%v", ok, err)
	}
}

// Grok-aligned: shell ask rules force a prompt even in full mode.
func TestFullModeShellAskRuleStillPrompts(t *testing.T) {
	promptCount := 0
	perms := NewPermissionManager(ModeBypass, func(string) (bool, error) {
		promptCount++
		return true, nil
	})
	if err := perms.SetShellCommandRules(map[string]string{`git\s+push`: "ask"}); err != nil {
		t.Fatalf("SetShellCommandRules: %v", err)
	}
	if ok, err := perms.Allow(ActionShell, "git push origin main"); !ok || err != nil {
		t.Fatalf("ask rule should allow after prompt, got ok=%v err=%v", ok, err)
	}
	if promptCount != 1 {
		t.Fatalf("full mode ask rule must prompt once, got %d", promptCount)
	}
}

// /permissions and normalize always persist plan|edit|full.
func TestPermissionsPersistCanonicalNamesOnly(t *testing.T) {
	for _, input := range []string{"acceptEdits", "bypassPermissions", ":workspace", "workspace", "edit", "full", "plan"} {
		cfg := DefaultConfig(t.TempDir())
		cfg.PermissionMode = input
		if err := normalizeConfigPermissionMode(&cfg); err != nil {
			t.Fatalf("normalize(%q): %v", input, err)
		}
		switch cfg.PermissionMode {
		case "plan", "edit", "full":
			// ok
		default:
			t.Fatalf("normalize(%q) persisted non-canonical %q", input, cfg.PermissionMode)
		}
	}
}

// EnsureWrite routes through EnsureEditableTarget so full mode can approve
// outside-root directories automatically.
func TestEnsureWriteExternalPathAutoApprovedInFullMode(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "file.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	promptCalled := false
	perms := NewPermissionManager(ModeBypass, func(string) (bool, error) {
		promptCalled = true
		return false, nil
	})
	ws := Workspace{Root: root, BaseRoot: root, Perms: perms}
	if err := ws.EnsureWriteWithContext(context.Background(), target); err != nil {
		t.Fatalf("full mode should auto-approve external write path: %v", err)
	}
	if promptCalled {
		t.Fatalf("full mode must not prompt for external directory approval")
	}
	if !perms.IsExternalDirAllowed(outside) {
		t.Fatalf("external directory should be remembered after full-mode auto-approve")
	}
}

// run_shell workspace writes: plan/edit deny manual; full allows.
func TestRunShellWorkspaceWriteModePolicy(t *testing.T) {
	root := t.TempDir()
	command := "Set-Content allowed.txt 'hello'"

	t.Run("plan denies manual write", func(t *testing.T) {
		tool := NewRunShellTool(Workspace{
			BaseRoot: root,
			Root:     root,
			Perms:    NewPermissionManager(ModePlan, nil),
		})
		_, err := tool.Execute(context.Background(), map[string]any{"command": command})
		if err == nil || !strings.Contains(err.Error(), "manual workspace file writes") {
			t.Fatalf("plan must deny manual shell write, got %v", err)
		}
	})

	t.Run("edit denies manual write", func(t *testing.T) {
		tool := NewRunShellTool(Workspace{
			BaseRoot: root,
			Root:     root,
			Perms: NewPermissionManager(ModeAcceptEdits, func(string) (bool, error) {
				t.Fatalf("edit mode must hard-deny manual shell write without prompting")
				return true, nil
			}),
		})
		_, err := tool.Execute(context.Background(), map[string]any{"command": command})
		if err == nil || !strings.Contains(err.Error(), "manual workspace file writes") {
			t.Fatalf("edit must deny manual shell write, got %v", err)
		}
	})

	t.Run("full allows manual write", func(t *testing.T) {
		wsRoot := t.TempDir()
		tool := NewRunShellTool(Workspace{
			BaseRoot: wsRoot,
			Root:     wsRoot,
			Shell:    defaultShell(),
			Perms:    NewPermissionManager(ModeBypass, nil),
		})
		_, err := tool.Execute(context.Background(), map[string]any{"command": command})
		if err != nil {
			t.Fatalf("full mode must allow manual shell workspace write, got %v", err)
		}
		data, readErr := os.ReadFile(filepath.Join(wsRoot, "allowed.txt"))
		if readErr != nil {
			t.Fatalf("expected file created by full-mode shell write: %v", readErr)
		}
		if !strings.Contains(string(data), "hello") {
			t.Fatalf("unexpected file contents %q", string(data))
		}
	})
}

// edit mode prompts for tool-style workspace writes (gofmt -w) via ActionShellWrite.
func TestRunShellToolStyleWritePromptsInEditMode(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Use a non-executing path: deny at the shell-write prompt so we never need gofmt.
	var prompted string
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
		Perms: NewPermissionManager(ModeAcceptEdits, func(q string) (bool, error) {
			prompted = q
			return false, nil
		}),
	})
	_, err := tool.Execute(context.Background(), map[string]any{"command": "gofmt -w main.go"})
	if err == nil {
		t.Fatalf("edit mode tool-style write must require approval")
	}
	if !strings.Contains(prompted, "Allow shell write?") {
		t.Fatalf("expected ActionShellWrite prompt, got %q (err=%v)", prompted, err)
	}
}
