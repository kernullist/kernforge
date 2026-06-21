package main

import (
	"strings"
	"testing"
)

// TestPlanModeToolBlockWording locks Polish 1: when an edit is refused in plan
// mode the message says "plan mode" (and tags plan_mode_block), while a non-plan
// read-only refusal keeps the generic read-only-analysis wording.
func TestPlanModeToolBlockWording(t *testing.T) {
	call := ToolCall{Name: "write_file"}

	// result_class is the locale-independent discriminator; "plan" appears in both
	// the English ("plan mode") and Korean ("plan 모드") refusal text.
	plan := readOnlyAnalysisToolBlockedResult(Config{}, call, true)
	if plan.Meta["result_class"] != "plan_mode_block" || !strings.Contains(plan.DisplayText, "plan") {
		t.Fatalf("plan-mode block must tag plan_mode_block and mention plan, got text=%q class=%v", plan.DisplayText, plan.Meta["result_class"])
	}

	ro := readOnlyAnalysisToolBlockedResult(Config{}, call, false)
	if ro.Meta["result_class"] != "read_only_policy_block" {
		t.Fatalf("non-plan block must tag read_only_policy_block, got text=%q class=%v", ro.DisplayText, ro.Meta["result_class"])
	}
}

// TestNormalizeConfigCanonicalizesPersistedMode locks Polish 2: every accepted
// mode string (canonical, legacy, or Codex profile id) persists as its canonical
// user-facing name. permission_sandbox-7: the :workspace profile is ModeDefault
// (prompt-on-write), which is NOT the auto-write "edit" tier, so it now persists
// as "workspace" to match its behavior instead of misreporting as "edit".
func TestNormalizeConfigCanonicalizesPersistedMode(t *testing.T) {
	cases := map[string]string{
		"plan":                "plan",
		"edit":                "edit",
		"full":                "full",
		"default":             "plan",
		"acceptEdits":         "edit",
		"bypassPermissions":   "full",
		":read-only":          "plan",
		":workspace":          "workspace",
		":danger-full-access": "full",
	}
	for input, want := range cases {
		cfg := DefaultConfig(t.TempDir())
		cfg.PermissionMode = input
		if err := normalizeConfigPermissionMode(&cfg); err != nil {
			t.Fatalf("normalizeConfigPermissionMode(%q): %v", input, err)
		}
		if cfg.PermissionMode != want {
			t.Fatalf("normalize(%q) persisted %q, want canonical %q", input, cfg.PermissionMode, want)
		}
	}
}
