package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestInitMemoryPolicyTemplateIsValidJSON(t *testing.T) {
	text := InitMemoryPolicyTemplate()
	var decoded PersistentMemoryPolicy
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("template must be valid json: %v\n%s", err, text)
	}
	if decoded.WorkspaceMaxRecords == 0 || decoded.ProtectRecent == 0 {
		t.Fatalf("expected default retention values, got %#v", decoded)
	}
}

func TestPersistentMemoryPruneRespectsImportanceAndTrust(t *testing.T) {
	store := &PersistentMemoryStore{
		Path: filepath.Join(t.TempDir(), "persistent-memory.json"),
	}
	now := time.Now()
	records := []PersistentMemoryRecord{
		{
			ID:         "mem-low",
			SessionID:  "s1",
			Workspace:  filepath.Join("F:", "repo"),
			CreatedAt:  now.AddDate(0, 0, -90),
			Summary:    "old low",
			Importance: PersistentMemoryLow,
			Trust:      PersistentMemoryTentative,
		},
		{
			ID:         "mem-high",
			SessionID:  "s2",
			Workspace:  filepath.Join("F:", "repo"),
			CreatedAt:  now.AddDate(0, 0, -90),
			Summary:    "old high confirmed",
			Importance: PersistentMemoryHigh,
			Trust:      PersistentMemoryConfirmed,
		},
	}
	if err := store.save(records); err != nil {
		t.Fatalf("seed records: %v", err)
	}
	policy := PersistentMemoryPolicy{
		AutoPrune:           false,
		MaxRecords:          50,
		WorkspaceMaxRecords: 50,
		ProtectRecent:       0,
		KeepDaysDefault:     180,
		KeepDaysLow:         30,
		KeepDaysMedium:      180,
		KeepDaysHigh:        365,
		KeepDaysTentative:   30,
		KeepDaysConfirmed:   365,
	}
	result, err := store.Prune(filepath.Join("F:", "repo"), policy, false)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if result.Deleted != 1 || len(result.DeletedIDs) != 1 || result.DeletedIDs[0] != "mem-low" {
		t.Fatalf("expected low/tentative record to be pruned first, got %#v", result)
	}
}
