package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func preferenceDecision(id string, project string, domain string, selected string) ImplementationDecisionRecord {
	record := testImplementationDecisionRecord()
	record.ID = id
	record.ProjectID = project
	record.Domains = []string{domain}
	record.SelectedOptionID = selected
	if selected == "single-json" {
		record.RejectedReasons = []ImplementationDecisionRejection{{OptionID: "per-record-json", Reason: "simpler"}}
	} else {
		record.RejectedReasons = []ImplementationDecisionRejection{{OptionID: "single-json", Reason: "unsafe concurrent update"}}
	}
	return normalizeImplementationDecisionRecord(record, record.UpdatedAt)
}

func TestBuildImplementationPreferenceProfilePromotesConservatively(t *testing.T) {
	one := BuildImplementationPreferenceProfile([]ImplementationDecisionRecord{
		preferenceDecision("decision-1", "project-a", "tooling", "per-record-json"),
	})
	if len(one.Rules) != 0 {
		t.Fatalf("one decision must not become a preference rule: %#v", one.Rules)
	}

	records := []ImplementationDecisionRecord{
		preferenceDecision("decision-1", "project-a", "tooling", "per-record-json"),
		preferenceDecision("decision-2", "project-a", "tooling", "per-record-json"),
		preferenceDecision("decision-3", "project-b", "tooling", "per-record-json"),
		preferenceDecision("decision-4", "project-b", "tooling", "per-record-json"),
		preferenceDecision("decision-5", "project-c", "tooling", "per-record-json"),
	}
	profile := BuildImplementationPreferenceProfile(records)
	var projectRule, domainRule, globalRule bool
	for _, rule := range profile.Rules {
		switch rule.Scope {
		case implementationPreferenceScopeProject:
			projectRule = true
		case implementationPreferenceScopeDomain:
			domainRule = true
		case implementationPreferenceScopeGlobal:
			globalRule = true
		}
	}
	if !projectRule || !domainRule || !globalRule {
		t.Fatalf("expected project/domain/global promotion: %#v", profile.Rules)
	}
}

func TestBuildImplementationPreferenceProfileTracksContradictions(t *testing.T) {
	records := []ImplementationDecisionRecord{
		preferenceDecision("decision-1", "project-a", "tooling", "per-record-json"),
		preferenceDecision("decision-2", "project-a", "tooling", "per-record-json"),
		preferenceDecision("decision-3", "project-a", "tooling", "single-json"),
	}
	profile := BuildImplementationPreferenceProfile(records)
	if len(profile.Rules) != 1 {
		t.Fatalf("expected one promoted project rule, got %#v", profile.Rules)
	}
	if len(profile.Rules[0].ContradictionDecisionIDs) != 1 || profile.Rules[0].ContradictionDecisionIDs[0] != "decision-3" {
		t.Fatalf("expected contradiction evidence, got %#v", profile.Rules[0])
	}
}

func TestBuildImplementationPreferenceProfileRejectsTiesAndNonUserEvidence(t *testing.T) {
	tied := []ImplementationDecisionRecord{
		preferenceDecision("decision-1", "project-a", "tooling", "per-record-json"),
		preferenceDecision("decision-2", "project-a", "tooling", "per-record-json"),
		preferenceDecision("decision-3", "project-a", "tooling", "single-json"),
		preferenceDecision("decision-4", "project-a", "tooling", "single-json"),
	}
	if profile := BuildImplementationPreferenceProfile(tied); len(profile.Rules) != 0 {
		t.Fatalf("tied preferences must not be activated: %#v", profile.Rules)
	}
	model := preferenceDecision("decision-model", "project-a", "tooling", "per-record-json")
	model.Provenance.Selection = "model"
	model.Provenance.Rationale = "model"
	model.DetectorConfidence = 1
	user := preferenceDecision("decision-user", "project-a", "tooling", "per-record-json")
	if profile := BuildImplementationPreferenceProfile([]ImplementationDecisionRecord{model, user}); len(profile.Rules) != 0 {
		t.Fatalf("model-selected records must not promote user preferences: %#v", profile.Rules)
	}
}

func TestImplementationPreferenceProfileRebuildPreservesUserOverrides(t *testing.T) {
	root := t.TempDir()
	decisions := &ImplementationDecisionStore{Dir: filepath.Join(root, "records")}
	for _, record := range []ImplementationDecisionRecord{
		preferenceDecision("decision-1", "project-a", "tooling", "per-record-json"),
		preferenceDecision("decision-2", "project-a", "tooling", "per-record-json"),
	} {
		if _, err := decisions.Put(record); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	profiles := &ImplementationPreferenceProfileStore{Path: filepath.Join(root, "profiles", "profile.json")}
	first, err := profiles.Rebuild(decisions)
	if err != nil || len(first.Rules) != 1 {
		t.Fatalf("first Rebuild: %#v err=%v", first, err)
	}
	first.Rules[0].Enabled = false
	first.Rules[0].Pinned = true
	first.Rules[0].UserNote = "Keep this project-specific"
	if err := profiles.Save(first); err != nil {
		t.Fatalf("Save override: %v", err)
	}
	second, err := profiles.Rebuild(decisions)
	if err != nil || len(second.Rules) != 1 {
		t.Fatalf("second Rebuild: %#v err=%v", second, err)
	}
	if second.Rules[0].Enabled || !second.Rules[0].Pinned || second.Rules[0].UserNote == "" {
		t.Fatalf("user override was not preserved: %#v", second.Rules[0])
	}
}

func TestImplementationPreferenceProfileRebuildPreservesOverridesAcrossDemotionAndRestore(t *testing.T) {
	root := t.TempDir()
	decisions := &ImplementationDecisionStore{Dir: filepath.Join(root, "records")}
	for _, record := range []ImplementationDecisionRecord{
		preferenceDecision("decision-1", "project-a", "tooling", "per-record-json"),
		preferenceDecision("decision-2", "project-a", "tooling", "per-record-json"),
	} {
		if _, err := decisions.Put(record); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	profiles := &ImplementationPreferenceProfileStore{Path: filepath.Join(root, "profiles", "profile.json")}
	profile, err := profiles.Rebuild(decisions)
	if err != nil || len(profile.Rules) != 1 {
		t.Fatalf("initial Rebuild: %#v err=%v", profile, err)
	}
	ruleID := profile.Rules[0].ID
	profile, err = profiles.UpdateRule(ruleID, profile.Revision, func(rule *ImplementationPreferenceRule) error {
		rule.Enabled = false
		rule.Pinned = true
		rule.UserNote = "Keep this override while evidence is temporarily absent"
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	record, ok, err := decisions.Get("decision-2")
	if err != nil || !ok {
		t.Fatalf("Get decision-2: ok=%v err=%v", ok, err)
	}
	deleted, err := decisions.SoftDelete(record.ID, record.Revision)
	if err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	demoted, err := profiles.Rebuild(decisions)
	if err != nil || len(demoted.Rules) != 0 {
		t.Fatalf("demotion Rebuild: %#v err=%v", demoted, err)
	}
	override, ok := demoted.Overrides[ruleID]
	if !ok || override.Enabled || !override.Pinned || override.UserNote == "" {
		t.Fatalf("durable override was not retained while demoted: %#v", demoted.Overrides)
	}
	if _, err := decisions.Revise(deleted.ID, deleted.Revision, func(current *ImplementationDecisionRecord) error {
		current.Status = implementationDecisionStatusCorrected
		current.DeletedAt = nil
		return nil
	}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	restored, err := profiles.Rebuild(decisions)
	if err != nil || len(restored.Rules) != 1 {
		t.Fatalf("restore Rebuild: %#v err=%v", restored, err)
	}
	rule := restored.Rules[0]
	if rule.ID != ruleID || rule.Enabled || !rule.Pinned || rule.UserNote != override.UserNote {
		t.Fatalf("restored rule lost its durable override: rule=%#v override=%#v", rule, override)
	}
}

func TestImplementationPreferenceProfileRejectsStaleSave(t *testing.T) {
	root := t.TempDir()
	decisions := &ImplementationDecisionStore{Dir: filepath.Join(root, "records")}
	for _, record := range []ImplementationDecisionRecord{
		preferenceDecision("decision-1", "project-a", "tooling", "per-record-json"),
		preferenceDecision("decision-2", "project-a", "tooling", "per-record-json"),
	} {
		if _, err := decisions.Put(record); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	profiles := &ImplementationPreferenceProfileStore{Path: filepath.Join(root, "profiles", "profile.json")}
	if _, err := profiles.Rebuild(decisions); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	left, err := profiles.Load()
	if err != nil {
		t.Fatalf("Load left: %v", err)
	}
	right, err := profiles.Load()
	if err != nil {
		t.Fatalf("Load right: %v", err)
	}
	left.Rules[0].Pinned = true
	if err := profiles.Save(left); err != nil {
		t.Fatalf("Save left: %v", err)
	}
	right.Rules[0].Enabled = false
	if err := profiles.Save(right); !errors.Is(err, ErrImplementationPreferenceConflict) {
		t.Fatalf("expected stale profile conflict, got %v", err)
	}
	current, err := profiles.Load()
	if err != nil || !current.Rules[0].Pinned {
		t.Fatalf("first update was lost: %#v err=%v", current, err)
	}
}

func TestImplementationPreferenceProfileRebuildPreservesRedactionFlag(t *testing.T) {
	root := t.TempDir()
	decisions := &ImplementationDecisionStore{Dir: filepath.Join(root, "records")}
	for _, record := range []ImplementationDecisionRecord{
		preferenceDecision("decision-1", "project-a", "tooling", "per-record-json"),
		preferenceDecision("decision-2", "project-a", "tooling", "per-record-json"),
	} {
		if _, err := decisions.Put(record); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	profiles := &ImplementationPreferenceProfileStore{Path: filepath.Join(root, "profiles", "profile.json")}
	profile, err := profiles.Rebuild(decisions)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	profile.Rules[0].UserNote = "api_key=sk-123456789012345678901234567890"
	if err := profiles.Save(profile); err != nil {
		t.Fatalf("Save: %v", err)
	}
	redacted, err := profiles.Load()
	if err != nil || !redacted.Rules[0].UserNoteRedacted || strings.Contains(redacted.Rules[0].UserNote, "sk-123") {
		t.Fatalf("note was not redacted: %#v err=%v", redacted.Rules[0], err)
	}
	rebuilt, err := profiles.Rebuild(decisions)
	if err != nil || !rebuilt.Rules[0].UserNoteRedacted {
		t.Fatalf("redaction flag was lost on rebuild: %#v err=%v", rebuilt.Rules[0], err)
	}
}

func TestImplementationPreferenceProfileRebuildRefusesCorruptPriorProfile(t *testing.T) {
	root := t.TempDir()
	decisions := &ImplementationDecisionStore{Dir: filepath.Join(root, "records")}
	for _, record := range []ImplementationDecisionRecord{
		preferenceDecision("decision-1", "project-a", "tooling", "per-record-json"),
		preferenceDecision("decision-2", "project-a", "tooling", "per-record-json"),
	} {
		if _, err := decisions.Put(record); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	profiles := &ImplementationPreferenceProfileStore{Path: filepath.Join(root, "profiles", "profile.json")}
	if err := os.MkdirAll(filepath.Dir(profiles.Path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(profiles.Path, []byte("{broken"), 0o600); err != nil {
		t.Fatalf("write corrupt profile: %v", err)
	}
	if _, err := profiles.Rebuild(decisions); err == nil {
		t.Fatal("rebuild must not overwrite a corrupt profile")
	}
	data, err := os.ReadFile(profiles.Path)
	if err != nil || string(data) != "{broken" {
		t.Fatalf("corrupt profile was unexpectedly overwritten: %q err=%v", data, err)
	}
}
