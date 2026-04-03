package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSimulationStoreAppendAndGet(t *testing.T) {
	root := t.TempDir()
	store := &SimulationStore{
		Path: filepath.Join(root, "simulations.json"),
	}
	result, err := store.Append(SimulationResult{
		Workspace: root,
		Profile:   "tamper-surface",
		Target:    "guard.sys",
		Findings: []SimulationFinding{
			{Subject: "unsigned-or-unverified-driver-surface", RiskScore: 82},
		},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if result.ID == "" {
		t.Fatal("expected generated id")
	}
	got, ok, err := store.Get(result.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || got.Profile != "tamper-surface" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestSimulationDashboardSummarizesFindings(t *testing.T) {
	root := t.TempDir()
	store := &SimulationStore{
		Path: filepath.Join(root, "simulations.json"),
	}
	_, err := store.Append(SimulationResult{
		Workspace: root,
		Profile:   "tamper-surface",
		Findings: []SimulationFinding{
			{
				Category:           "driver",
				Subject:            "unsigned-or-unverified-driver-surface",
				Severity:           "critical",
				SignalClass:        "tamper",
				RiskScore:          88,
				RecommendedActions: []string{"verify signtool /pa output"},
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
	if summary.TotalRuns != 1 || summary.MaxRisk != 88 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	rendered := renderSimulationDashboard(summary)
	if !strings.Contains(rendered, "Recommended actions:") {
		t.Fatalf("expected recommended actions in dashboard, got %q", rendered)
	}
}
