package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testImplementationDecisionRecord() ImplementationDecisionRecord {
	return ImplementationDecisionRecord{
		ID:           "decision-test-1",
		ProjectID:    "kernforge",
		ProjectAlias: "KernForge",
		Workspace:    `F:\kernullist\kernforge`,
		Domains:      []string{"tooling", "security"},
		Languages:    []string{"go"},
		DecisionKind: "storage",
		RiskLevel:    "medium",
		Problem:      "store implementation decisions across projects",
		Options: []ImplementationDecisionOption{
			{ID: "per-record-json", Label: "Per-record JSON", Recommended: true},
			{ID: "single-json", Label: "Single JSON array"},
		},
		RecommendedOptionID: "per-record-json",
		SelectedOptionID:    "per-record-json",
		SelectionReasonRaw:  "avoids cross-process lost updates",
		RejectedReasons: []ImplementationDecisionRejection{
			{OptionID: "single-json", Reason: "read-modify-write can lose updates", Source: "user"},
		},
		Tags:               []string{"decision", "storage"},
		DetectorConfidence: 0.91,
		Provenance: ImplementationDecisionProvenance{
			Options:   "model",
			Selection: "user",
			Rationale: "user",
			Summary:   "runtime",
		},
	}
}

func TestImplementationDecisionStorePutGetListSearchExport(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	record, err := store.Put(testImplementationDecisionRecord())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if record.Revision != 1 || record.SchemaVersion != implementationDecisionSchemaVersion {
		t.Fatalf("unexpected stored metadata: %#v", record)
	}
	if !strings.Contains(record.SummarySentence, "Per-record JSON") {
		t.Fatalf("expected deterministic summary, got %q", record.SummarySentence)
	}
	if info, err := os.Stat(filepath.Join(store.Dir, record.ID+".json")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("expected decision record file: info=%v err=%v", info, err)
	}
	loaded, ok, err := store.Get(record.ID)
	if err != nil || !ok || loaded.ID != record.ID {
		t.Fatalf("Get: ok=%v record=%#v err=%v", ok, loaded, err)
	}
	items, err := store.List(ImplementationDecisionFilter{ProjectID: "kernforge"})
	if err != nil || len(items) != 1 {
		t.Fatalf("List: len=%d err=%v", len(items), err)
	}
	hits, err := store.Search("lost updates", ImplementationDecisionFilter{})
	if err != nil || len(hits) != 1 {
		t.Fatalf("Search: len=%d err=%v", len(hits), err)
	}
	exported, err := store.Export(ImplementationDecisionFilter{})
	if err != nil || !strings.Contains(string(exported), `"decision-test-1"`) {
		t.Fatalf("Export: %s err=%v", exported, err)
	}
}

func TestImplementationDecisionStorePutIsIdempotentAndRejectsOverwrite(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	first, err := store.Put(testImplementationDecisionRecord())
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	second, err := store.Put(first)
	if err != nil || second.Revision != first.Revision {
		t.Fatalf("idempotent Put: %#v err=%v", second, err)
	}
	changed := first
	changed.SelectionReasonRaw = "different reason"
	if _, err := store.Put(changed); !errors.Is(err, ErrImplementationDecisionConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestImplementationDecisionStoreDerivesStableIDFromCaptureFingerprints(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	record := testImplementationDecisionRecord()
	record.ID = ""
	record.TaskFingerprint = "task-abc"
	record.EvidenceFingerprint = "evidence-def"
	first, err := store.Put(record)
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	second, err := store.Put(record)
	if err != nil {
		t.Fatalf("retry Put: %v", err)
	}
	if first.ID != second.ID || first.Revision != second.Revision {
		t.Fatalf("capture retry was not idempotent: first=%#v second=%#v", first, second)
	}
}

func TestImplementationDecisionStoreRevisionAndSoftDelete(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	record, err := store.Put(testImplementationDecisionRecord())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	updated, err := store.Revise(record.ID, record.Revision, func(item *ImplementationDecisionRecord) error {
		item.SelectionReasonRaw = "keeps the canonical journal append-safe"
		return nil
	})
	if err != nil || updated.Revision != 2 || updated.Status != implementationDecisionStatusCorrected {
		t.Fatalf("Revise: %#v err=%v", updated, err)
	}
	if _, err := store.Revise(record.ID, record.Revision, func(item *ImplementationDecisionRecord) error { return nil }); !errors.Is(err, ErrImplementationDecisionConflict) {
		t.Fatalf("expected stale revision conflict, got %v", err)
	}
	deleted, err := store.SoftDelete(updated.ID, updated.Revision)
	if err != nil || deleted.Status != implementationDecisionStatusDeleted || deleted.DeletedAt == nil {
		t.Fatalf("SoftDelete: %#v err=%v", deleted, err)
	}
	visible, err := store.List(ImplementationDecisionFilter{})
	if err != nil || len(visible) != 0 {
		t.Fatalf("deleted decision must be hidden: len=%d err=%v", len(visible), err)
	}
	all, err := store.List(ImplementationDecisionFilter{IncludeDeleted: true})
	if err != nil || len(all) != 1 {
		t.Fatalf("deleted decision must be available for audit: len=%d err=%v", len(all), err)
	}
	history, err := store.History(record.ID)
	if err != nil || len(history) != 3 || history[0].Revision != 1 || history[1].Revision != 2 || history[2].Revision != 3 {
		t.Fatalf("revision history was not preserved: %#v err=%v", history, err)
	}
}

func TestImplementationDecisionStoreSupportsCustomChoiceAndRedactsSecrets(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	record := testImplementationDecisionRecord()
	record.ID = "decision-custom"
	record.SelectedOptionID = ""
	record.CustomSelection = "Use a small generated index"
	record.RejectedReasons = []ImplementationDecisionRejection{
		{OptionID: "per-record-json", Reason: "the generated index needs a separate canonical record"},
		{OptionID: "single-json", Reason: "read-modify-write can lose updates"},
	}
	record.SelectionReasonRaw = "api_key=sk-123456789012345678901234567890"
	stored, err := store.Put(record)
	if err != nil {
		t.Fatalf("Put custom decision: %v", err)
	}
	if stored.CustomSelection == "" || !stored.Redaction.Redacted || strings.Contains(stored.SelectionReasonRaw, "sk-123") {
		t.Fatalf("custom/redaction contract failed: %#v", stored)
	}
	data, err := os.ReadFile(filepath.Join(store.Dir, stored.ID+".json"))
	if err != nil || strings.Contains(string(data), "sk-123") {
		t.Fatalf("secret reached disk: %s err=%v", data, err)
	}
	updated, err := store.Revise(stored.ID, stored.Revision, func(item *ImplementationDecisionRecord) error {
		item.ProjectAlias = "Updated"
		return nil
	})
	if err != nil || !updated.Redaction.Redacted {
		t.Fatalf("redaction metadata must survive revisions: %#v err=%v", updated, err)
	}
}

func TestImplementationDecisionStoreRequiresSelectionAndRejectionRationales(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	missingSelection := testImplementationDecisionRecord()
	missingSelection.ID = "decision-missing-selection-rationale"
	missingSelection.SelectionReasonRaw = ""
	if _, err := store.Put(missingSelection); err == nil || !strings.Contains(err.Error(), "selection rationale") {
		t.Fatalf("missing selection rationale was accepted: %v", err)
	}

	missingRejection := testImplementationDecisionRecord()
	missingRejection.ID = "decision-missing-rejection-rationale"
	missingRejection.RejectedReasons[0].Reason = ""
	if _, err := store.Put(missingRejection); err == nil || !strings.Contains(err.Error(), "rejection rationale") {
		t.Fatalf("missing rejection rationale was accepted: %v", err)
	}
}

func TestImplementationDecisionStoreRedactsAllFreeTextAndIgnoresInjectedRedactionMetadata(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	record := testImplementationDecisionRecord()
	record.ID = "decision-redaction-fields"
	record.Tags = []string{"api_key=sk-123456789012345678901234567890"}
	record.Domains = []string{"token=ghp_123456789012345678901234567890123456"}
	record.Redaction = ImplementationDecisionRedaction{
		Redacted: true,
		Patterns: []string{"sk-attacker-controlled-metadata"},
	}
	stored, err := store.Put(record)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(store.Dir, stored.ID+".json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, secret := range []string{"sk-123456", "ghp_123456", "attacker-controlled"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("secret or injected redaction metadata reached disk: %s", data)
		}
	}
	if !stored.Redaction.Redacted || len(stored.Redaction.Patterns) == 0 {
		t.Fatalf("derived redaction report was not retained: %#v", stored.Redaction)
	}
}

func TestImplementationDecisionStoreRejectsSecretsInIdentifierFields(t *testing.T) {
	secret := "ghp_123456789012345678901234567890123456"
	for _, mutate := range []func(*ImplementationDecisionRecord){
		func(record *ImplementationDecisionRecord) { record.DecisionKind = secret },
		func(record *ImplementationDecisionRecord) { record.TaskFingerprint = secret },
		func(record *ImplementationDecisionRecord) { record.EvidenceFingerprint = secret },
		func(record *ImplementationDecisionRecord) {
			record.Options[0].ID = secret
			record.RecommendedOptionID = secret
			record.SelectedOptionID = secret
		},
	} {
		store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
		record := testImplementationDecisionRecord()
		mutate(&record)
		if _, err := store.Put(record); err == nil || !strings.Contains(strings.ToLower(err.Error()), "sensitive") {
			t.Fatalf("sensitive identifier was not rejected: %v", err)
		}
		files, err := filepath.Glob(filepath.Join(store.Dir, "*.json"))
		if err != nil {
			t.Fatalf("Glob: %v", err)
		}
		if len(files) != 0 {
			t.Fatalf("sensitive identifier reached storage: %#v", files)
		}
	}
}

func TestImplementationDecisionStoreRejectsTamperedHistoryRevision(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	stored, err := store.Put(testImplementationDecisionRecord())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	fake := stored
	fake.Revision = 99
	historyPath := filepath.Join(filepath.Dir(store.Dir), "history", stored.ID, "revision-000001.json")
	if err := writeImplementationDecisionRecord(historyPath, fake); err != nil {
		t.Fatalf("write tampered history: %v", err)
	}
	if _, err := store.Revise(stored.ID, stored.Revision, func(item *ImplementationDecisionRecord) error {
		item.SelectionReasonRaw = "new reason"
		return nil
	}); err == nil {
		t.Fatal("revision must fail when an existing history file has mismatched content")
	}
}

func TestImplementationDecisionStoreExportIsPrivacyReducedByDefault(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	record := testImplementationDecisionRecord()
	record.SessionID = "session-private"
	record.EvidenceRefs = []string{`F:\private\source.go:10`}
	if _, err := store.Put(record); err != nil {
		t.Fatalf("Put: %v", err)
	}
	data, err := store.Export(ImplementationDecisionFilter{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	for _, forbidden := range []string{`F:\\kernullist\\kernforge`, "session-private", `F:\\private\\source.go`} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("default export leaked %q: %s", forbidden, data)
		}
	}
	privateData, err := store.Export(ImplementationDecisionFilter{IncludePrivateMetadata: true})
	if err != nil || !strings.Contains(string(privateData), "session-private") {
		t.Fatalf("explicit private export did not retain metadata: %s err=%v", privateData, err)
	}
}

func TestImplementationDecisionStoreReportsCorruptRecordsWithoutHidingHealthyOnes(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	if _, err := store.Put(testImplementationDecisionRecord()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir, "decision-corrupt.json"), []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatalf("write corrupt fixture: %v", err)
	}
	records, issues, err := store.ListWithIssues(ImplementationDecisionFilter{})
	if err != nil || len(records) != 1 || len(issues) != 1 {
		t.Fatalf("ListWithIssues: records=%#v issues=%#v err=%v", records, issues, err)
	}
	if _, err := store.List(ImplementationDecisionFilter{}); err == nil {
		t.Fatal("strict List must report corrupt records")
	}
}

func TestImplementationDecisionStoreRejectsInvalidOptionsAndIDs(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	record := testImplementationDecisionRecord()
	record.ID = "../escape"
	if _, err := store.Put(record); err == nil {
		t.Fatal("unsafe id must be rejected")
	}
	record = testImplementationDecisionRecord()
	record.ID = "decision-one-option"
	record.Options = record.Options[:1]
	if _, err := store.Put(record); err == nil {
		t.Fatal("one option must be rejected")
	}
	record = testImplementationDecisionRecord()
	record.ID = "decision-bad-selection"
	record.SelectedOptionID = "missing"
	if _, err := store.Put(record); err == nil {
		t.Fatal("unknown selected option must be rejected")
	}
}
