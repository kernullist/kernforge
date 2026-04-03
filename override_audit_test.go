package main

import (
	"path/filepath"
	"testing"
)

func TestRecordHookOverrideEventWritesEvidenceAndMemory(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		session: &Session{
			ID:       "session-1",
			Name:     "Test Session",
			Provider: "fake",
			Model:    "fake-model",
		},
		workspace: Workspace{BaseRoot: root, Root: root},
		evidence: &EvidenceStore{
			Path: filepath.Join(root, "evidence.json"),
		},
		longMem: &PersistentMemoryStore{
			Path: filepath.Join(root, "persistent-memory.json"),
		},
	}
	rt.recordHookOverrideEvent("active", []HookOverrideRecord{
		{
			ID:        "ovr-1",
			RuleID:    "deny-driver-pr-with-critical-signing-or-symbol-evidence",
			Workspace: root,
			Reason:    "manual verification complete",
		},
	})

	evidenceItems, err := rt.evidence.Search("kind:hook_override", root, 10)
	if err != nil {
		t.Fatalf("Search evidence: %v", err)
	}
	if len(evidenceItems) != 1 || evidenceItems[0].Subject != "deny-driver-pr-with-critical-signing-or-symbol-evidence" {
		t.Fatalf("expected hook override evidence, got %#v", evidenceItems)
	}

	memItems, err := rt.longMem.Search("hook override active", root, "", 10)
	if err != nil {
		t.Fatalf("Search memory: %v", err)
	}
	if len(memItems) != 1 {
		t.Fatalf("expected hook override memory record, got %#v", memItems)
	}
}
