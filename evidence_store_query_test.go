package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEvidenceStoreSearchFilters(t *testing.T) {
	store := &EvidenceStore{
		Path: filepath.Join(t.TempDir(), "evidence.json"),
	}
	if err := store.Append(EvidenceRecord{
		ID:          "ev-driver",
		Workspace:   filepath.Join("F:", "repo"),
		Kind:        "verification_artifact",
		Category:    "driver",
		Subject:     "driver/guard.sys",
		Outcome:     "passed",
		Severity:    "low",
		SignalClass: "signing",
		RiskScore:   35,
		Tags:        []string{"artifact", "signing", "driver"},
	}); err != nil {
		t.Fatalf("Append driver: %v", err)
	}
	if err := store.Append(EvidenceRecord{
		ID:          "ev-telemetry",
		Workspace:   filepath.Join("F:", "repo"),
		Kind:        "verification_failure",
		Category:    "telemetry",
		Subject:     "runtime_error",
		Outcome:     "failed",
		Severity:    "high",
		SignalClass: "provider",
		RiskScore:   78,
		Tags:        []string{"failure", "telemetry"},
	}); err != nil {
		t.Fatalf("Append telemetry: %v", err)
	}

	results, err := store.Search("kind:verification_artifact category:driver tag:signing outcome:passed", filepath.Join("F:", "repo"), 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "ev-driver" {
		t.Fatalf("unexpected filtered evidence results: %#v", results)
	}

	results, err = store.Search("severity:high signal:provider risk:>=70", filepath.Join("F:", "repo"), 10)
	if err != nil {
		t.Fatalf("Search severity: %v", err)
	}
	if len(results) != 1 || results[0].ID != "ev-telemetry" {
		t.Fatalf("unexpected severity-filtered evidence results: %#v", results)
	}
}

func TestEvidenceStoreGetAndListRecent(t *testing.T) {
	store := &EvidenceStore{
		Path: filepath.Join(t.TempDir(), "evidence.json"),
	}
	if err := store.Append(EvidenceRecord{
		ID:        "ev-1",
		Workspace: filepath.Join("F:", "repo"),
		Kind:      "verification_category",
		Category:  "driver",
		Subject:   "driver",
		Outcome:   "passed",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	record, ok, err := store.Get("ev-1")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if record.Kind != "verification_category" {
		t.Fatalf("unexpected record: %#v", record)
	}
	items, err := store.ListRecent(filepath.Join("F:", "repo"), 5)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(items) != 1 || items[0].ID != "ev-1" {
		t.Fatalf("unexpected recent evidence: %#v", items)
	}
}

func TestEvidenceStoreDashboardSummary(t *testing.T) {
	store := &EvidenceStore{
		Path: filepath.Join(t.TempDir(), "evidence.json"),
	}
	workspace := filepath.Join("F:", "repo")
	appendRecord := func(record EvidenceRecord) {
		t.Helper()
		if err := store.Append(record); err != nil {
			t.Fatalf("Append %s: %v", record.ID, err)
		}
	}

	appendRecord(EvidenceRecord{
		ID:          "ev-1",
		Workspace:   workspace,
		Kind:        "verification_artifact",
		Category:    "driver",
		Subject:     "driver/guard.sys",
		Outcome:     "failed",
		Severity:    "critical",
		SignalClass: "signing",
		RiskScore:   92,
		Tags:        []string{"artifact", "driver", "signing"},
	})
	appendRecord(EvidenceRecord{
		ID:          "ev-2",
		Workspace:   workspace,
		Kind:        "verification_failure",
		Category:    "driver",
		Subject:     "runtime_error",
		Outcome:     "failed",
		Severity:    "high",
		SignalClass: "runtime",
		RiskScore:   76,
		Tags:        []string{"failure", "driver", "signing"},
	})
	appendRecord(EvidenceRecord{
		ID:          "ev-3",
		Workspace:   workspace,
		Kind:        "verification_category",
		Category:    "telemetry",
		Subject:     "telemetry",
		Outcome:     "passed",
		Severity:    "low",
		SignalClass: "provider",
		RiskScore:   24,
		Tags:        []string{"telemetry", "provider"},
	})

	summary, err := store.Dashboard(workspace, "category:driver", 5)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if summary.TotalRecords != 2 {
		t.Fatalf("expected 2 filtered records, got %#v", summary)
	}
	if len(summary.ByCategory) != 1 || summary.ByCategory[0].Name != "driver" || summary.ByCategory[0].Count != 2 {
		t.Fatalf("unexpected category summary: %#v", summary.ByCategory)
	}
	if len(summary.ByOutcome) != 1 || summary.ByOutcome[0].Name != "failed" || summary.ByOutcome[0].Count != 2 {
		t.Fatalf("unexpected outcome summary: %#v", summary.ByOutcome)
	}
	if len(summary.BySeverity) == 0 || summary.BySeverity[0].Name == "" {
		t.Fatalf("expected severity summary, got %#v", summary.BySeverity)
	}
	if len(summary.BySignalClass) == 0 || summary.BySignalClass[0].Name == "" {
		t.Fatalf("expected signal summary, got %#v", summary.BySignalClass)
	}
	rendered := renderEvidenceDashboard(summary)
	if !strings.Contains(rendered, "Evidence categories:") || !strings.Contains(rendered, "driver: 2") || !strings.Contains(rendered, "Evidence severities:") {
		t.Fatalf("dashboard render missing expected content:\n%s", rendered)
	}
}

func TestEvidenceDashboardRendersActiveOverrides(t *testing.T) {
	summary := EvidenceDashboardSummary{
		Scope:        "current workspace",
		TotalRecords: 1,
		ActiveOverrides: []HookOverrideRecord{
			{
				RuleID:    "deny-driver-pr-with-critical-signing-or-symbol-evidence",
				Reason:    "manual verification complete",
				ExpiresAt: time.Now().Add(2 * time.Hour),
			},
		},
	}
	rendered := renderEvidenceDashboard(summary)
	if !strings.Contains(rendered, "Active hook overrides:") || !strings.Contains(rendered, "manual verification complete") {
		t.Fatalf("expected override section in dashboard render, got:\n%s", rendered)
	}
	html := renderEvidenceDashboardHTML(summary)
	if !strings.Contains(html, "Active hook overrides") || !strings.Contains(html, "deny-driver-pr-with-critical-signing-or-symbol-evidence") {
		t.Fatalf("expected override section in dashboard html, got:\n%s", html)
	}
}

func TestCreateEvidenceDashboardHTML(t *testing.T) {
	summary := EvidenceDashboardSummary{
		Scope:        "current workspace",
		FilterText:   "category:driver",
		TotalRecords: 2,
		ByKind: []NamedCount{
			{Name: "verification_artifact", Count: 1},
			{Name: "verification_failure", Count: 1},
		},
		ByCategory: []NamedCount{
			{Name: "driver", Count: 2},
		},
		ByOutcome: []NamedCount{
			{Name: "failed", Count: 2},
		},
		TopTags: []NamedCount{
			{Name: "signing", Count: 2},
		},
		TopSubjects: []NamedCount{
			{Name: "driver/guard.sys", Count: 1},
			{Name: "runtime_error", Count: 1},
		},
		Recent: []EvidenceRecord{
			{
				ID:                  "ev-1",
				Kind:                "verification_artifact",
				Category:            "driver",
				Subject:             "driver/guard.sys",
				Outcome:             "failed",
				Severity:            "critical",
				SignalClass:         "signing",
				RiskScore:           92,
				VerificationSummary: "signing check failed",
				Tags:                []string{"artifact", "driver", "signing"},
			},
		},
	}

	path, err := createEvidenceDashboardHTML(summary)
	if err != nil {
		t.Fatalf("createEvidenceDashboardHTML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	html := string(data)
	if !strings.Contains(html, "Evidence Dashboard") || !strings.Contains(html, "driver/guard.sys") || !strings.Contains(html, "critical") {
		t.Fatalf("html output missing expected content:\n%s", html)
	}
}
