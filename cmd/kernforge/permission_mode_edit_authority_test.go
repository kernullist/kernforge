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

// TestApplyEditAuthorityToEnvelopeRespectsReadOnly locks the chosen behavior:
// the permission mode authorizes edits but does NOT compel one. In edit/full a
// non-read-only request becomes edit-capable, but a request the classifier read
// as a genuine read-only question stays answer-only (no file mutation), so edit
// tools are not exposed for it even in full mode. plan never grants mutation.
func TestApplyEditAuthorityToEnvelopeRespectsReadOnly(t *testing.T) {
	mk := func(mode Mode) *Agent {
		return &Agent{Workspace: Workspace{Perms: NewPermissionManager(mode, func(string) (bool, error) { return true, nil })}}
	}
	for _, mode := range []Mode{ModeAcceptEdits, ModeBypass} {
		env := RequestEnvelope{ReadOnlyAnalysis: false}
		mk(mode).applyEditAuthorityToEnvelope(&env)
		if !env.AllowsFileMutation || !env.RequiresVerification {
			t.Fatalf("mode %v: a non-read-only request must become edit-capable, got %#v", mode, env)
		}
		ro := RequestEnvelope{ReadOnlyAnalysis: true}
		mk(mode).applyEditAuthorityToEnvelope(&ro)
		if ro.AllowsFileMutation {
			t.Fatalf("mode %v: a read-only question must stay answer-only even in edit/full, got AllowsFileMutation=true", mode)
		}
	}
	plan := RequestEnvelope{ReadOnlyAnalysis: false}
	mk(ModePlan).applyEditAuthorityToEnvelope(&plan)
	if plan.AllowsFileMutation {
		t.Fatalf("plan must not grant mutation, got AllowsFileMutation=true")
	}
	// plan is the single authority: it REVOKES a mutation the request
	// classification (or inherited session context) may already have set, and
	// forces read-only -- not merely refrains from granting.
	planPreset := RequestEnvelope{ReadOnlyAnalysis: false, ExplicitEditRequest: true, AllowsFileMutation: true, AllowsGitMutation: true}
	mk(ModePlan).applyEditAuthorityToEnvelope(&planPreset)
	if planPreset.AllowsFileMutation || planPreset.AllowsGitMutation || !planPreset.ReadOnlyAnalysis {
		t.Fatalf("plan must revoke pre-set mutation and force read-only, got %#v", planPreset)
	}
	// legacy default / nil mode preserves the prior request-based behavior:
	// neither granting nor revoking a pre-set mutation signal.
	def := RequestEnvelope{ReadOnlyAnalysis: false, ExplicitEditRequest: true, AllowsFileMutation: true}
	mk(ModeDefault).applyEditAuthorityToEnvelope(&def)
	if !def.AllowsFileMutation {
		t.Fatalf("legacy default must preserve a pre-set mutation signal, got %#v", def)
	}
}
