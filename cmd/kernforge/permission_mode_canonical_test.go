package main

import "testing"

// TestParseModeCanonicalAndAliases locks the canonical plan/edit/full names plus
// the retained legacy aliases, and that an unspecified mode defaults to plan.
func TestParseModeCanonicalAndAliases(t *testing.T) {
	cases := map[string]Mode{
		"":                    ModePlan, // unspecified -> read-only plan (new default)
		"plan":                ModePlan,
		"edit":                ModeAcceptEdits,
		"full":                ModeBypass,
		"default":             ModeDefault,     // legacy alias still parses
		"acceptEdits":         ModeAcceptEdits, // legacy alias
		"bypassPermissions":   ModeBypass,      // legacy alias
		":read-only":          ModePlan,
		":workspace":          ModeDefault,
		":danger-full-access": ModeBypass,
	}
	for input, want := range cases {
		got, ok := ParseModeStrict(input)
		if !ok {
			t.Fatalf("ParseModeStrict(%q) should parse", input)
		}
		if got != want {
			t.Fatalf("ParseModeStrict(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestDefaultConfigPermissionModeIsPlan locks the new read-only-by-default mode.
func TestDefaultConfigPermissionModeIsPlan(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	if cfg.PermissionMode != "plan" {
		t.Fatalf("default permission mode should be plan (read-only), got %q", cfg.PermissionMode)
	}
	if mode, ok := ParseModeStrict(cfg.PermissionMode); !ok || mode != ModePlan {
		t.Fatalf("default config mode should parse to ModePlan, got %q ok=%v", mode, ok)
	}
}

// TestCanonicalModeEnforcement documents the gate semantics the three canonical
// modes resolve to: plan = read-only, edit = in-workspace write auto + dangerous
// ops prompt, full = everything without prompts.
func TestCanonicalModeEnforcement(t *testing.T) {
	noPrompt := func(string) (bool, error) {
		t.Fatalf("unexpected permission prompt")
		return false, nil
	}

	plan := NewPermissionManager(ModePlan, noPrompt)
	if ok, err := plan.Allow(ActionRead, "x"); !ok || err != nil {
		t.Fatalf("plan must allow read without prompt, got ok=%v err=%v", ok, err)
	}
	if ok, err := plan.Allow(ActionWrite, "x"); ok || err == nil {
		t.Fatalf("plan must deny write, got ok=%v err=%v", ok, err)
	}

	editNoPrompt := NewPermissionManager(ModeAcceptEdits, noPrompt)
	if ok, err := editNoPrompt.Allow(ActionWrite, "x"); !ok || err != nil {
		t.Fatalf("edit must auto-allow an in-workspace write, got ok=%v err=%v", ok, err)
	}
	editPromptDenies := NewPermissionManager(ModeAcceptEdits, func(string) (bool, error) { return false, nil })
	if ok, _ := editPromptDenies.Allow(ActionShell, "go test ./..."); ok {
		t.Fatalf("edit must require approval for a dangerous op (shell)")
	}

	full := NewPermissionManager(ModeBypass, noPrompt)
	for _, action := range []Action{ActionWrite, ActionShell, ActionGit} {
		if ok, err := full.Allow(action, "x"); !ok || err != nil {
			t.Fatalf("full must auto-allow %s without prompt, got ok=%v err=%v", action, ok, err)
		}
	}
}
