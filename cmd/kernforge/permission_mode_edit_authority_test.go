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
// the permission MODE is the single authority for whether edits are ALLOWED. In
// edit/full every request becomes edit-capable so the edit tools stay exposed and
// the model decides whether to act -- the per-request read-only classification is
// only a soft hint and must not strip tools or hard-block the mutation. A request
// that already reads as a non-read-only edit keeps verification required; a
// read-only-classified request is granted capability WITHOUT forcing verification
// (so a pure-question turn is not trapped behind a verification gate) and WITHOUT
// clearing the soft read-only hint (the analysis-only prompt guidance survives).
// plan never grants mutation.
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
		if !ro.AllowsFileMutation {
			t.Fatalf("mode %v: edit/full is the single authority and must keep edit capability even for a read-only-classified request, got AllowsFileMutation=false", mode)
		}
		if ro.RequiresVerification {
			t.Fatalf("mode %v: a read-only-classified turn must not be forced into verification (avoids a no-edit deadlock), got RequiresVerification=true", mode)
		}
		if !ro.ReadOnlyAnalysis {
			t.Fatalf("mode %v: the read-only classification is preserved as a soft prompt hint, got ReadOnlyAnalysis=false", mode)
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

// TestEditAuthorityKeepsEditToolsExposedInEditMode locks INV-1: the permission
// mode is the single authority for edit-tool EXPOSURE. A request the classifier
// reads as read-only (here a Korean review-only request) keeps the edit tools
// exposed in edit/full so the model can act if it judges an edit is needed, while
// plan mode strips them. This is what makes the semantic classifier safe to enable
// as the primary intent source: a misread "read-only" can no longer strand the
// model by removing the edit tools mid-turn.
func TestEditAuthorityKeepsEditToolsExposedInEditMode(t *testing.T) {
	mk := func(mode Mode) *Agent {
		root := t.TempDir()
		return &Agent{
			Config:    DefaultConfig(root),
			Tools:     requestEnvelopeTestRegistry(),
			Workspace: Workspace{BaseRoot: root, Root: root, Perms: NewPermissionManager(mode, func(string) (bool, error) { return true, nil })},
			Session:   NewSession(root, "scripted", "model", "", "default"),
		}
	}
	exposeAfterAuthority := func(a *Agent) turnToolExposurePlan {
		env := buildRequestEnvelope("RuntimeManager.cpp 코드 리뷰해줘")
		if !env.ReadOnlyAnalysis {
			t.Fatalf("precondition: review-only request should classify read-only, got %#v", env)
		}
		a.applyEditAuthorityToEnvelope(&env)
		return a.buildTurnToolExposurePlanForEnvelope(nil, env, false, false, false, false, false, false)
	}
	for _, mode := range []Mode{ModeAcceptEdits, ModeBypass} {
		plan := exposeAfterAuthority(mk(mode))
		if plan.toolDisabled("apply_patch") || plan.toolDisabled("write_file") {
			t.Fatalf("mode %v: edit mode must keep edit tools exposed for a read-only-classified request, got disabled=%#v", mode, plan.DisabledTools)
		}
	}
	planPlan := exposeAfterAuthority(mk(ModePlan))
	if !planPlan.toolDisabled("apply_patch") || !planPlan.toolDisabled("write_file") {
		t.Fatalf("plan mode must strip edit tools for a read-only request, got disabled=%#v", planPlan.DisabledTools)
	}
}
