package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerificationHistoryDashboardSummarizesWorkspaceReports(t *testing.T) {
	store := &VerificationHistoryStore{
		Path: filepath.Join(t.TempDir(), "verification-history.json"),
	}
	rootA := filepath.Join("F:", "repo-a")
	rootB := filepath.Join("F:", "repo-b")

	reportFail := VerificationReport{
		GeneratedAt: time.Now(),
		Decision:    "Adaptive verification stopped after a targeted failure to keep feedback local.",
		Steps: []VerificationStep{{
			Label:       "go test ./internal/auth/...",
			Status:      VerificationFailed,
			FailureKind: "compile_error",
			Hint:        "Fix compile errors first.",
		}},
	}
	reportPass := VerificationReport{
		GeneratedAt: time.Now(),
		Decision:    "Verification completed all planned steps.",
		Steps: []VerificationStep{{
			Label:  "go test ./...",
			Status: VerificationPassed,
		}},
	}

	if err := store.Append("s1", rootA, reportFail); err != nil {
		t.Fatalf("Append fail report: %v", err)
	}
	if err := store.Append("s2", rootA, reportPass); err != nil {
		t.Fatalf("Append pass report: %v", err)
	}
	if err := store.Append("s3", rootB, reportFail); err != nil {
		t.Fatalf("Append other workspace report: %v", err)
	}

	summary, err := store.Dashboard(rootA, false, nil, 10)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if summary.TotalReports != 2 || summary.PassedReports != 1 || summary.FailedReports != 1 {
		t.Fatalf("unexpected summary counts: %#v", summary)
	}
	if len(summary.FailureKinds) == 0 || summary.FailureKinds[0].Name != "compile_error" {
		t.Fatalf("expected compile_error in dashboard, got %#v", summary.FailureKinds)
	}
	rendered := renderVerificationDashboard(summary)
	if !strings.Contains(rendered, "Top failure kinds:") || !strings.Contains(rendered, "Recent reports:") {
		t.Fatalf("unexpected dashboard render: %q", rendered)
	}
}

func TestVerificationDashboardCanFilterByTag(t *testing.T) {
	store := &VerificationHistoryStore{
		Path: filepath.Join(t.TempDir(), "verification-history.json"),
	}
	root := filepath.Join("F:", "repo-a")
	withTag := VerificationReport{
		GeneratedAt: time.Now(),
		Steps: []VerificationStep{{
			Label:  "go test ./...",
			Status: VerificationPassed,
			Tags:   []string{"integration"},
		}},
	}
	withoutTag := VerificationReport{
		GeneratedAt: time.Now(),
		Steps: []VerificationStep{{
			Label:  "go vet ./...",
			Status: VerificationPassed,
		}},
	}
	if err := store.Append("s1", root, withTag); err != nil {
		t.Fatalf("Append tagged report: %v", err)
	}
	if err := store.Append("s2", root, withoutTag); err != nil {
		t.Fatalf("Append untagged report: %v", err)
	}
	summary, err := store.Dashboard(root, false, []string{"integration"}, 10)
	if err != nil {
		t.Fatalf("Dashboard tag filter: %v", err)
	}
	if summary.TotalReports != 1 {
		t.Fatalf("expected only tagged report to remain, got %#v", summary)
	}
}

func TestRenderVerificationDashboardHTMLContainsKeySections(t *testing.T) {
	summary := VerificationDashboardSummary{
		Scope:         "current workspace",
		TotalReports:  3,
		PassedReports: 2,
		FailedReports: 1,
		FailureKinds:  []NamedCount{{Name: "compile_error", Count: 1}},
		FailingChecks: []NamedCount{{Name: "go test ./...", Count: 1}},
		Recent: []VerificationHistoryEntry{{
			RecordedAt: time.Now(),
			Report: VerificationReport{
				Decision: "Adaptive verification stopped after a targeted failure to keep feedback local.",
				Steps: []VerificationStep{{
					Status: VerificationFailed,
					Tags:   []string{"integration"},
				}},
			},
		}},
		LastUpdated: time.Now(),
	}
	html := renderVerificationDashboardHTML(summary)
	for _, needle := range []string{"Verification Dashboard", "Top failure kinds", "Most frequently failing checks", "Recent reports", "Report drill-down", "tag"} {
		if !strings.Contains(html, needle) {
			t.Fatalf("expected html dashboard to contain %q, got %q", needle, html)
		}
	}
}

func TestCreateVerificationDashboardHTMLWritesFile(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", tempRoot)
	summary := VerificationDashboardSummary{Scope: "current workspace"}
	path, err := createVerificationDashboardHTML(summary, false)
	if err != nil {
		t.Fatalf("createVerificationDashboardHTML: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected html report file to exist: %v", err)
	}
}
