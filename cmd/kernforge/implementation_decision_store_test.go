package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
	if _, err := store.Put(missingSelection); err == nil || !errors.Is(err, ErrImplementationDecisionInvalid) || !strings.Contains(err.Error(), "selection rationale") {
		t.Fatalf("missing selection rationale was accepted: %v", err)
	}

	missingRejection := testImplementationDecisionRecord()
	missingRejection.ID = "decision-missing-rejection-rationale"
	missingRejection.RejectedReasons[0].Reason = ""
	if _, err := store.Put(missingRejection); err == nil || !errors.Is(err, ErrImplementationDecisionInvalid) || !strings.Contains(err.Error(), "rejection rationale") {
		t.Fatalf("missing rejection rationale was accepted: %v", err)
	}
}

func TestImplementationDecisionStoreReviseWrapsMutationValidationOnly(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	stored, err := store.Put(testImplementationDecisionRecord())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := store.Revise(stored.ID, stored.Revision, func(record *ImplementationDecisionRecord) error {
		record.SelectedOptionID = "missing"
		return nil
	}); err == nil || !errors.Is(err, ErrImplementationDecisionInvalid) {
		t.Fatalf("mutation validation did not expose typed invalid error: %v", err)
	}
	path := filepath.Join(store.Dir, stored.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := os.WriteFile(path, append(data, []byte("trailing corruption")...), 0o600); err != nil {
		t.Fatalf("write corrupt fixture: %v", err)
	}
	if _, _, err := store.Get(stored.ID); err == nil || errors.Is(err, ErrImplementationDecisionInvalid) {
		t.Fatalf("stored corruption was misclassified as request validation: %v", err)
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
		func(record *ImplementationDecisionRecord) { record.ID = secret },
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
		entries, err := os.ReadDir(store.Dir)
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("ReadDir: %v", err)
		}
		for _, entry := range entries {
			if strings.Contains(entry.Name(), secret) {
				t.Fatalf("sensitive identifier reached storage filename: %s", entry.Name())
			}
		}
	}
}

func TestImplementationDecisionStoreRejectsRevisionOverflowWithoutChangingCurrent(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	record := normalizeImplementationDecisionRecord(testImplementationDecisionRecord(), testImplementationDecisionRecord().UpdatedAt)
	record.Revision = int(^uint(0) >> 1)
	path := filepath.Join(store.Dir, record.ID+".json")
	if err := writeImplementationDecisionRecord(path, record); err != nil {
		t.Fatalf("write max revision fixture: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read max revision fixture: %v", err)
	}
	if _, err := store.Revise(record.ID, record.Revision, func(item *ImplementationDecisionRecord) error {
		item.SelectionReasonRaw = "must not be written"
		return nil
	}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "revision") {
		t.Fatalf("revision overflow was not rejected: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read record after rejected revision: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("rejected revision overflow changed the current record")
	}
}

func TestImplementationDecisionStoreSupportsHistoryPrefixedDecisionID(t *testing.T) {
	for _, id := range []string{"revision-custom-decision", "revision-000001"} {
		t.Run(id, func(t *testing.T) {
			store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
			record := testImplementationDecisionRecord()
			record.ID = id
			stored, err := store.Put(record)
			if err != nil {
				t.Fatalf("Put: %v", err)
			}
			loaded, ok, err := store.Get(stored.ID)
			if err != nil || !ok || loaded.ID != stored.ID {
				t.Fatalf("history-prefixed current record was misclassified: ok=%v record=%#v err=%v", ok, loaded, err)
			}
			revised, err := store.Revise(loaded.ID, loaded.Revision, func(record *ImplementationDecisionRecord) error {
				record.SelectionReasonRaw = "history-prefixed id revision"
				return nil
			})
			if err != nil {
				t.Fatalf("Revise: %v", err)
			}
			loaded, ok, err = store.Get(revised.ID)
			if err != nil || !ok || loaded.Revision != 2 {
				t.Fatalf("revised history-prefixed current record was misclassified: ok=%v record=%#v err=%v", ok, loaded, err)
			}
		})
	}
}

func TestImplementationDecisionHistoryRejectsCurrentStyleFilename(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	stored, err := store.Put(testImplementationDecisionRecord())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	invalidHistoryPath := filepath.Join(filepath.Dir(store.Dir), "history", stored.ID, stored.ID+".json")
	if err := writeImplementationDecisionRecord(invalidHistoryPath, stored); err != nil {
		t.Fatalf("write invalid history filename: %v", err)
	}
	if _, err := store.History(stored.ID); err == nil || !strings.Contains(strings.ToLower(err.Error()), "history filename") {
		t.Fatalf("current-style filename was accepted in history: %v", err)
	}
}

func TestImplementationDecisionStorePutDoesNotMutateCallerOrChangeStableID(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	record := testImplementationDecisionRecord()
	record.ID = ""
	record.TaskFingerprint = "stable-task"
	record.Options[0].Label = "api_key=sk-123456789012345678901234567890"
	original, err := cloneImplementationDecisionRecord(record)
	if err != nil {
		t.Fatalf("clone input: %v", err)
	}
	first, err := store.Put(record)
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	if !reflect.DeepEqual(record, original) {
		t.Fatalf("Put mutated caller-owned slices: before=%#v after=%#v", original, record)
	}
	second, err := store.Put(record)
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("stable retry created a different id: first=%s second=%s", first.ID, second.ID)
	}
	records, err := store.List(ImplementationDecisionFilter{})
	if err != nil || len(records) != 1 {
		t.Fatalf("stable retry created duplicate records: len=%d err=%v", len(records), err)
	}
}

func TestImplementationDecisionStableIDAvoidsDelimiterCollisions(t *testing.T) {
	left := testImplementationDecisionRecord()
	left.ID = ""
	left.TaskFingerprint = "stable-task"
	left.Options = []ImplementationDecisionOption{{ID: "a", Label: "x"}, {ID: "b", Label: "y|c=z"}}
	right := left
	right.Options = []ImplementationDecisionOption{{ID: "a", Label: "x|b=y"}, {ID: "c", Label: "z"}}
	leftID, err := implementationDecisionIDForRecord(left)
	if err != nil {
		t.Fatalf("left id: %v", err)
	}
	rightID, err := implementationDecisionIDForRecord(right)
	if err != nil {
		t.Fatalf("right id: %v", err)
	}
	if leftID == rightID {
		t.Fatalf("structured option sets collided: %s", leftID)
	}
}

func TestImplementationDecisionHistoryIsConsistentWithConcurrentRevision(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	first, err := store.Put(testImplementationDecisionRecord())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	second, err := store.Revise(first.ID, first.Revision, func(record *ImplementationDecisionRecord) error {
		record.SelectionReasonRaw = "revision two"
		return nil
	})
	if err != nil {
		t.Fatalf("first Revise: %v", err)
	}
	afterReadDir := make(chan struct{})
	releaseHistory := make(chan struct{})
	store.historyAfterReadDir = func() {
		close(afterReadDir)
		<-releaseHistory
	}
	type historyResult struct {
		records []ImplementationDecisionRecord
		err     error
	}
	historyDone := make(chan historyResult, 1)
	go func() {
		records, historyErr := store.History(first.ID)
		historyDone <- historyResult{records: records, err: historyErr}
	}()
	<-afterReadDir
	reviseDone := make(chan error, 1)
	go func() {
		_, reviseErr := store.Revise(second.ID, second.Revision, func(record *ImplementationDecisionRecord) error {
			record.SelectionReasonRaw = "revision three"
			return nil
		})
		reviseDone <- reviseErr
	}()
	var earlyReviseErr error
	revisedWhilePaused := false
	select {
	case earlyReviseErr = <-reviseDone:
		revisedWhilePaused = true
	case <-time.After(time.Second):
	}
	close(releaseHistory)
	history := <-historyDone
	if history.err != nil || len(history.records) != 2 || history.records[0].Revision != 1 || history.records[1].Revision != 2 {
		t.Fatalf("History observed a torn revision sequence: %#v err=%v", history.records, history.err)
	}
	if revisedWhilePaused {
		if earlyReviseErr != nil {
			t.Fatalf("concurrent Revise: %v", earlyReviseErr)
		}
	} else if err := <-reviseDone; err != nil {
		t.Fatalf("concurrent Revise after History: %v", err)
	}
}

func TestImplementationDecisionStoreCrossProcessCAS(t *testing.T) {
	if os.Getenv("KERNFORGE_DECISION_CAS_HELPER") == "1" {
		store := &ImplementationDecisionStore{Dir: os.Getenv("KERNFORGE_DECISION_CAS_DIR")}
		_, err := store.Revise(os.Getenv("KERNFORGE_DECISION_CAS_ID"), 1, func(record *ImplementationDecisionRecord) error {
			record.SelectionReasonRaw = "cross-process revision"
			return nil
		})
		if errors.Is(err, ErrImplementationDecisionConflict) {
			os.Exit(17)
		}
		if err != nil {
			t.Fatalf("helper Revise: %v", err)
		}
		return
	}

	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	stored, err := store.Put(testImplementationDecisionRecord())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	commands := make([]*exec.Cmd, 2)
	for index := range commands {
		commands[index] = exec.Command(os.Args[0], "-test.run=^TestImplementationDecisionStoreCrossProcessCAS$")
		commands[index].Env = append(os.Environ(),
			"KERNFORGE_DECISION_CAS_HELPER=1",
			"KERNFORGE_DECISION_CAS_DIR="+store.Dir,
			"KERNFORGE_DECISION_CAS_ID="+stored.ID,
		)
		if err := commands[index].Start(); err != nil {
			t.Fatalf("start helper %d: %v", index, err)
		}
	}
	successes := 0
	conflicts := 0
	for index, command := range commands {
		err := command.Wait()
		if err == nil {
			successes++
			continue
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 17 {
			conflicts++
			continue
		}
		t.Fatalf("helper %d failed unexpectedly: %v", index, err)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("cross-process CAS results: successes=%d conflicts=%d", successes, conflicts)
	}
	current, ok, err := store.Get(stored.ID)
	if err != nil || !ok || current.Revision != 2 {
		t.Fatalf("cross-process CAS produced invalid current record: ok=%v record=%#v err=%v", ok, current, err)
	}
}

func TestImplementationDecisionStoreRejectsOversizedCorruptFileBeforeDecode(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	if err := os.MkdirAll(store.Dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(store.Dir, "decision-oversized-file.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", implementationDecisionRecordMaxBytes+1)), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, _, err := store.Get("decision-oversized-file"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "storage limit") {
		t.Fatalf("oversized corrupt file was not bounded: %v", err)
	}
}

func TestImplementationDecisionStoreReadsHistoryRevisionAboveSixDigits(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	record := normalizeImplementationDecisionRecord(testImplementationDecisionRecord(), testImplementationDecisionRecord().UpdatedAt)
	record.Revision = 1_000_000
	historyPath := filepath.Join(filepath.Dir(store.Dir), "history", record.ID, "revision-1000000.json")
	if err := writeImplementationDecisionRecord(historyPath, record); err != nil {
		t.Fatalf("write high revision: %v", err)
	}
	loaded, ok, err := store.readHistoryRecord(historyPath, record.ID)
	if err != nil || !ok || loaded.Revision != record.Revision {
		t.Fatalf("read high revision: ok=%v record=%#v err=%v", ok, loaded, err)
	}
}

func TestImplementationDecisionStoreRejectsOversizedWorkspace(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	record := testImplementationDecisionRecord()
	record.ID = "decision-oversized-workspace"
	record.Workspace = strings.Repeat("x", 2<<20)
	if _, err := store.Put(record); err == nil || !strings.Contains(strings.ToLower(err.Error()), "storage limit") {
		t.Fatalf("oversized workspace was accepted: %v", err)
	}
}

func TestImplementationDecisionStoreRejectsSymlinkRecord(t *testing.T) {
	root := t.TempDir()
	store := &ImplementationDecisionStore{Dir: filepath.Join(root, "records")}
	if err := os.MkdirAll(store.Dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	record := testImplementationDecisionRecord()
	record.ID = "decision-linked-record"
	record = normalizeImplementationDecisionRecord(record, record.UpdatedAt)
	record = redactImplementationDecisionRecord(record)
	target := filepath.Join(root, "outside.json")
	if err := writeImplementationDecisionRecord(target, record); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(store.Dir, record.ID+".json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	if _, _, err := store.Get(record.ID); err == nil {
		t.Fatal("decision store followed a symlinked record")
	}
}

func TestImplementationDecisionStoreRejectsSymlinkedStoreAndLock(t *testing.T) {
	t.Run("store", func(t *testing.T) {
		root := t.TempDir()
		outside := filepath.Join(root, "outside")
		if err := os.MkdirAll(outside, 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		linkedStore := filepath.Join(root, "records")
		if err := os.Symlink(outside, linkedStore); err != nil {
			t.Skipf("directory symlink creation is unavailable: %v", err)
		}
		store := &ImplementationDecisionStore{Dir: linkedStore}
		if _, err := store.Put(testImplementationDecisionRecord()); err == nil {
			t.Fatal("decision store followed a symlinked storage directory")
		}
	})
	t.Run("lock", func(t *testing.T) {
		root := t.TempDir()
		store := &ImplementationDecisionStore{Dir: filepath.Join(root, "records")}
		if err := os.MkdirAll(store.Dir, 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		target := filepath.Join(root, "outside.lock")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		lockPath := filepath.Join(store.Dir, testImplementationDecisionRecord().ID+".json.lock")
		if err := os.Symlink(target, lockPath); err != nil {
			t.Skipf("file symlink creation is unavailable: %v", err)
		}
		if _, err := store.Put(testImplementationDecisionRecord()); err == nil {
			t.Fatal("decision store followed a symlinked lock file")
		}
	})
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

func TestImplementationDecisionStoreRequiresBoundedKindAndKnownRisk(t *testing.T) {
	for name, mutate := range map[string]func(*ImplementationDecisionRecord){
		"missing-kind": func(record *ImplementationDecisionRecord) {
			record.DecisionKind = ""
		},
		"oversized-kind": func(record *ImplementationDecisionRecord) {
			record.DecisionKind = strings.Repeat("k", 129)
		},
		"unknown-risk": func(record *ImplementationDecisionRecord) {
			record.RiskLevel = "urgent"
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
			record := testImplementationDecisionRecord()
			mutate(&record)
			if _, err := store.Put(record); err == nil {
				t.Fatalf("invalid decision metadata was accepted: %#v", record)
			}
		})
	}

	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	stored, err := store.Put(testImplementationDecisionRecord())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := store.Revise(stored.ID, stored.Revision, func(record *ImplementationDecisionRecord) error {
		record.DecisionKind = ""
		return nil
	}); err == nil {
		t.Fatal("revision cleared required decision kind")
	}
	current, ok, err := store.Get(stored.ID)
	if err != nil || !ok || current.Revision != stored.Revision || current.DecisionKind != stored.DecisionKind {
		t.Fatalf("rejected metadata revision changed current record: ok=%v record=%#v err=%v", ok, current, err)
	}
}

func TestImplementationDecisionStoreAllowsLegacyMetadataRepair(t *testing.T) {
	store := &ImplementationDecisionStore{Dir: filepath.Join(t.TempDir(), "records")}
	record := normalizeImplementationDecisionRecord(testImplementationDecisionRecord(), testImplementationDecisionRecord().UpdatedAt)
	record.ID = "decision-legacy-metadata"
	record.DecisionKind = strings.Repeat("k", 129)
	record.RiskLevel = "urgent"
	path := filepath.Join(store.Dir, record.ID+".json")
	if err := writeImplementationDecisionRecord(path, record); err != nil {
		t.Fatalf("write legacy fixture: %v", err)
	}
	legacy, ok, err := store.Get(record.ID)
	if err != nil || !ok || legacy.DecisionKind != record.DecisionKind || legacy.RiskLevel != record.RiskLevel {
		t.Fatalf("legacy metadata became unreadable: ok=%v record=%#v err=%v", ok, legacy, err)
	}
	if _, err := store.Revise(legacy.ID, legacy.Revision, func(current *ImplementationDecisionRecord) error {
		current.ProjectAlias = "unrelated change"
		return nil
	}); err == nil {
		t.Fatal("revision preserved invalid legacy metadata")
	}
	repaired, err := store.Revise(legacy.ID, legacy.Revision, func(current *ImplementationDecisionRecord) error {
		current.DecisionKind = "storage"
		current.RiskLevel = "high"
		return nil
	})
	if err != nil {
		t.Fatalf("repair Revise: %v", err)
	}
	if repaired.DecisionKind != "storage" || repaired.RiskLevel != "high" || repaired.Revision != 2 {
		t.Fatalf("legacy metadata repair was not persisted: %#v", repaired)
	}
	emptyKind := normalizeImplementationDecisionRecord(testImplementationDecisionRecord(), testImplementationDecisionRecord().UpdatedAt)
	emptyKind.ID = "decision-legacy-empty-kind"
	emptyKind.DecisionKind = ""
	emptyPath := filepath.Join(store.Dir, emptyKind.ID+".json")
	if err := writeImplementationDecisionRecord(emptyPath, emptyKind); err != nil {
		t.Fatalf("write empty-kind fixture: %v", err)
	}
	legacy, ok, err = store.Get(emptyKind.ID)
	if err != nil || !ok || legacy.DecisionKind != "" {
		t.Fatalf("legacy empty decision kind became unreadable: ok=%v record=%#v err=%v", ok, legacy, err)
	}
	if _, err := store.Revise(legacy.ID, legacy.Revision, func(current *ImplementationDecisionRecord) error {
		current.DecisionKind = "storage"
		return nil
	}); err != nil {
		t.Fatalf("repair empty decision kind: %v", err)
	}
}
