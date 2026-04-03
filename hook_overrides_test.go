package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHookOverrideStoreAppendListAndRemove(t *testing.T) {
	root := t.TempDir()
	store := &HookOverrideStore{
		Path: filepath.Join(root, "hook-overrides.json"),
	}
	record, err := store.Append(HookOverrideRecord{
		RuleID:    "deny-driver-pr-with-critical-signing-or-symbol-evidence",
		Workspace: filepath.Join("F:", "repo"),
		Reason:    "manual review completed",
		ExpiresAt: time.Now().Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if record.ID == "" {
		t.Fatalf("expected generated id, got %#v", record)
	}
	items, err := store.List(filepath.Join("F:", "repo"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].RuleID != record.RuleID {
		t.Fatalf("unexpected overrides: %#v", items)
	}
	removed, err := store.Remove(record.ID, filepath.Join("F:", "repo"), false)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("expected one removed override, got %#v", removed)
	}
}

func TestHookRuntimeSkipsOverriddenRule(t *testing.T) {
	root := t.TempDir()
	overrideStore := &HookOverrideStore{
		Path: filepath.Join(root, "hook-overrides.json"),
	}
	if _, err := overrideStore.Append(HookOverrideRecord{
		RuleID:    "driver-deny",
		Workspace: root,
		Reason:    "approved hotfix",
		ExpiresAt: time.Now().Add(2 * time.Hour),
	}); err != nil {
		t.Fatalf("Append override: %v", err)
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "driver-deny",
					Events: []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "deny",
						Message: "blocked",
					},
				},
			},
		},
		Overrides: overrideStore,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence: &EvidenceStore{
			Path: filepath.Join(root, "evidence.json"),
		},
	}
	if err := runtime.Evidence.Append(EvidenceRecord{
		ID:        "ev-1",
		Workspace: root,
		Kind:      "verification_failure",
		Category:  "driver",
		Subject:   "runtime_error",
		Outcome:   "failed",
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	verdict, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{})
	if err != nil {
		t.Fatalf("expected overridden rule to be skipped, got %v", err)
	}
	if !verdict.Allow {
		t.Fatalf("expected allow due to override, got %#v", verdict)
	}
}

func TestHookOverrideStoreIgnoresExpiredOverrides(t *testing.T) {
	root := t.TempDir()
	store := &HookOverrideStore{
		Path: filepath.Join(root, "hook-overrides.json"),
	}
	if _, err := store.Append(HookOverrideRecord{
		RuleID:    "driver-deny",
		Workspace: root,
		Reason:    "expired test",
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	items, err := store.List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected expired overrides to be skipped, got %#v", items)
	}
	active := store.IsActive("driver-deny", root, time.Now())
	if active {
		t.Fatal("expected expired override to be inactive")
	}
}

func TestHookOverrideClearByRuleID(t *testing.T) {
	root := t.TempDir()
	store := &HookOverrideStore{
		Path: filepath.Join(root, "hook-overrides.json"),
	}
	for _, ruleID := range []string{"driver-deny", "driver-deny", "telemetry-ask"} {
		if _, err := store.Append(HookOverrideRecord{
			RuleID:    ruleID,
			Workspace: root,
			Reason:    "test",
			ExpiresAt: time.Now().Add(2 * time.Hour),
		}); err != nil {
			t.Fatalf("Append %s: %v", ruleID, err)
		}
	}
	removed, err := store.Remove("driver-deny", root, false)
	if err != nil {
		t.Fatalf("Remove by rule: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("expected 2 overrides removed by rule id, got %#v", removed)
	}
	items, err := store.List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || !strings.EqualFold(items[0].RuleID, "telemetry-ask") {
		t.Fatalf("unexpected overrides after remove: %#v", items)
	}
}
