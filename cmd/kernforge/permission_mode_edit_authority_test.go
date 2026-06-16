package main

import "testing"

// TestEditPermissionGrantedByMode locks the single-authority gate added in Slice 2:
// an explicitly edit-capable mode (edit/full) grants edit authority, so the
// per-request read-only-analysis classification is forced off and no longer blocks
// edits or tells the model it is read-only. plan stays read-only; a nil or legacy
// mode preserves the prior request-based behavior.
func TestEditPermissionGrantedByMode(t *testing.T) {
	mk := func(mode Mode) *Agent {
		return &Agent{Workspace: Workspace{Perms: NewPermissionManager(mode, func(string) (bool, error) { return true, nil })}}
	}
	if !mk(ModeAcceptEdits).editPermissionGranted() {
		t.Fatalf("edit (ModeAcceptEdits) must grant edit authority")
	}
	if !mk(ModeBypass).editPermissionGranted() {
		t.Fatalf("full (ModeBypass) must grant edit authority")
	}
	if mk(ModePlan).editPermissionGranted() {
		t.Fatalf("plan must NOT grant edit authority (read-only)")
	}
	if mk(ModeDefault).editPermissionGranted() {
		t.Fatalf("legacy default must preserve prior request-based behavior, not force edits")
	}
	if (&Agent{}).editPermissionGranted() {
		t.Fatalf("nil Perms must not grant edit authority")
	}
}
