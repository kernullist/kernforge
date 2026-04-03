package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInvestigationStoreAppendUpdateActiveAndGet(t *testing.T) {
	root := t.TempDir()
	store := &InvestigationStore{
		Path: filepath.Join(root, "investigations.json"),
	}
	record, err := store.Append(InvestigationRecord{
		Workspace: root,
		Preset:    "driver-load",
		Target:    "guard.sys",
		Status:    InvestigationActive,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if record.ID == "" {
		t.Fatal("expected generated id")
	}
	active, ok, err := store.Active(root)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if !ok || active.ID != record.ID {
		t.Fatalf("unexpected active record: %#v", active)
	}
	updated, ok, err := store.Update(record.ID, func(item *InvestigationRecord) error {
		item.Status = InvestigationCompleted
		item.UpdatedAt = time.Now()
		item.Notes = append(item.Notes, "finished")
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !ok || updated.Status != InvestigationCompleted {
		t.Fatalf("unexpected updated record: %#v", updated)
	}
	got, ok, err := store.Get(record.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || len(got.Notes) != 1 {
		t.Fatalf("unexpected stored record: %#v", got)
	}
}

func TestInvestigationDashboardSummarizesFindings(t *testing.T) {
	root := t.TempDir()
	store := &InvestigationStore{
		Path: filepath.Join(root, "investigations.json"),
	}
	_, err := store.Append(InvestigationRecord{
		Workspace: root,
		Preset:    "driver-load",
		Status:    InvestigationCompleted,
		Snapshots: []InvestigationSnapshot{
			{
				Findings: []InvestigationFinding{
					{Category: "driver", Severity: "high", Subject: "guard.sys loaded"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	summary, err := store.Dashboard(root, 5)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if summary.TotalSessions != 1 {
		t.Fatalf("expected one session, got %d", summary.TotalSessions)
	}
	if len(summary.ByCategory) == 0 || summary.ByCategory[0].Name != "driver" {
		t.Fatalf("expected driver category summary, got %#v", summary.ByCategory)
	}
	rendered := renderInvestigationDashboard(summary)
	if !strings.Contains(rendered, "Finding categories:") {
		t.Fatalf("expected rendered dashboard to include finding categories, got %q", rendered)
	}
}
