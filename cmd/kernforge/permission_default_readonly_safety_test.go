package main

import "testing"

// TestReadOnlyByDefaultSafetyIsModeAnchored locks the redesign's safety invariant:
// the read-only-by-default protection now comes from the default permission MODE
// being plan (not a retired per-request read-only-analysis hard block), so a
// default-configured workspace denies edits/shell/git until the user explicitly
// opts into edit or full.
func TestReadOnlyByDefaultSafetyIsModeAnchored(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	mode := ParseMode(cfg.PermissionMode)
	if mode != ModePlan {
		t.Fatalf("default config must resolve to plan (read-only), got %q", mode)
	}

	noPrompt := func(string) (bool, error) {
		t.Fatalf("read-only default must deny edits without prompting")
		return false, nil
	}
	perms := NewPermissionManager(mode, noPrompt)
	for _, action := range []Action{ActionWrite, ActionShell, ActionShellWrite, ActionGit} {
		if ok, err := perms.Allow(action, "x"); ok || err == nil {
			t.Fatalf("default (plan) must deny %s, got ok=%v err=%v", action, ok, err)
		}
	}
	if ok, err := perms.Allow(ActionRead, "x"); !ok || err != nil {
		t.Fatalf("default (plan) must still allow reads, got ok=%v err=%v", ok, err)
	}

	// Opting into edit/full lifts the read-only default (in-workspace writes auto).
	for _, m := range []Mode{ModeAcceptEdits, ModeBypass} {
		p := NewPermissionManager(m, func(string) (bool, error) { return true, nil })
		if ok, err := p.Allow(ActionWrite, "x"); !ok || err != nil {
			t.Fatalf("explicit %s must allow writes, got ok=%v err=%v", m, ok, err)
		}
	}
}

// TestNormalizeConfigAcceptsCanonicalModes ensures config validation accepts the
// canonical plan/edit/full names (they may normalize to a legacy internal string,
// which still parses and displays as plan/edit/full).
func TestNormalizeConfigAcceptsCanonicalModes(t *testing.T) {
	for _, name := range []string{"plan", "edit", "full"} {
		cfg := DefaultConfig(t.TempDir())
		cfg.PermissionMode = name
		if err := normalizeConfigPermissionMode(&cfg); err != nil {
			t.Fatalf("normalizeConfigPermissionMode(%q): %v", name, err)
		}
		mode, ok := ParseModeStrict(cfg.PermissionMode)
		if !ok {
			t.Fatalf("normalized %q -> %q does not parse", name, cfg.PermissionMode)
		}
		if got := permissionModeDisplayName(mode); got != name {
			t.Fatalf("canonical %q should still display as %q, got %q", name, name, got)
		}
	}
}
