package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Approving a directory covers its whole subtree for the rest of the session,
// without another prompt, while unrelated paths stay outside.
func TestExternalDirApprovalRemembersSubtree(t *testing.T) {
	allow := true
	perms := NewPermissionManager(ModeAcceptEdits, func(string) (bool, error) { return allow, nil })
	dir := t.TempDir()

	ok, err := perms.AllowExternalDir(dir)
	if err != nil || !ok {
		t.Fatalf("approve external dir: ok=%v err=%v", ok, err)
	}
	// Prove the subtree is allowed from the allowlist alone (no re-prompt).
	allow = false
	if !perms.IsExternalDirAllowed(filepath.Join(dir, "sub", "file.go")) {
		t.Fatalf("approved dir must cover its subtree")
	}
	if perms.IsExternalDirAllowed(filepath.Join(t.TempDir(), "other.go")) {
		t.Fatalf("an unrelated path must not be allowed")
	}
}

// A denied directory is never remembered, so the strict boundary still applies.
func TestExternalDirDenialKeepsStrictBoundary(t *testing.T) {
	perms := NewPermissionManager(ModeAcceptEdits, func(string) (bool, error) { return false, nil })
	dir := t.TempDir()

	ok, err := perms.AllowExternalDir(dir)
	if err != nil || ok {
		t.Fatalf("denied external dir must return false: ok=%v err=%v", ok, err)
	}
	if perms.IsExternalDirAllowed(dir) {
		t.Fatalf("a denied directory must not be remembered")
	}
}

// Full (bypass) mode auto-approves without prompting.
func TestExternalDirAutoApprovedUnderBypass(t *testing.T) {
	promptCalled := false
	perms := NewPermissionManager(ModeBypass, func(string) (bool, error) {
		promptCalled = true
		return false, nil
	})
	dir := t.TempDir()

	ok, err := perms.AllowExternalDir(dir)
	if err != nil || !ok {
		t.Fatalf("bypass mode must auto-approve: ok=%v err=%v", ok, err)
	}
	if promptCalled {
		t.Fatalf("bypass mode must not prompt for an external directory")
	}
}

// EnsureEditableTarget approves an outside directory on demand and lets in-root
// targets through unchanged.
func TestEnsureEditableTargetApprovesOutsideDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "file.go")
	perms := NewPermissionManager(ModeAcceptEdits, func(string) (bool, error) { return true, nil })
	ws := Workspace{Root: root, BaseRoot: root, Perms: perms}

	if err := ws.EnsureEditableTarget(context.Background(), target); err != nil {
		t.Fatalf("approved outside dir should pass: %v", err)
	}
	if err := ws.EnsureEditableTarget(context.Background(), filepath.Join(root, "x.go")); err != nil {
		t.Fatalf("in-root target should always pass: %v", err)
	}
}

// A denied outside directory keeps the boundary error.
func TestEnsureEditableTargetRespectsDenial(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "file.go")
	perms := NewPermissionManager(ModeAcceptEdits, func(string) (bool, error) { return false, nil })
	ws := Workspace{Root: root, BaseRoot: root, Perms: perms}

	if err := ws.EnsureEditableTarget(context.Background(), target); err == nil {
		t.Fatalf("denied outside dir must keep the boundary error")
	}
}

// A symlink inside the root that escapes outside must stay blocked and must NOT
// trigger an external-directory prompt for its in-root parent.
func TestEnsureEditableTargetDoesNotPromptForSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "ext.txt")
	if err := os.WriteFile(external, []byte("x"), 0o644); err != nil {
		t.Fatalf("write external: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	prompted := false
	perms := NewPermissionManager(ModeAcceptEdits, func(string) (bool, error) {
		prompted = true
		return true, nil
	})
	ws := Workspace{Root: root, BaseRoot: root, Perms: perms}

	if err := ws.EnsureEditableTarget(context.Background(), link); err == nil {
		t.Fatalf("a symlink escaping the root must remain blocked")
	}
	if prompted {
		t.Fatalf("symlink escape must not prompt for external-directory approval")
	}
}
