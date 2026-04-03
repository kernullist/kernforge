package main

import (
	"path/filepath"
	"testing"
)

func TestInvestigationArtifactFindingsDriverMissing(t *testing.T) {
	findings := investigationArtifactFindings("driver-visibility", "guard.sys", nil)
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %#v", findings)
	}
	if findings[0].Subject != "target artifact missing" || findings[0].SignalClass != "driver_artifact" {
		t.Fatalf("unexpected finding: %#v", findings[0])
	}
}

func TestInvestigationEvidenceReferenceFindingsMatchesCategoryAndTarget(t *testing.T) {
	records := []EvidenceRecord{
		{
			ID:                  "ev-1",
			Category:            "driver",
			Subject:             filepath.Join("bin", "guard.sys"),
			Outcome:             "failed",
			Severity:            "high",
			SignalClass:         "signing",
			RiskScore:           82,
			VerificationSummary: "guard.sys signing failure",
		},
		{
			ID:          "ev-2",
			Category:    "telemetry",
			Subject:     "provider missing",
			Outcome:     "failed",
			Severity:    "high",
			SignalClass: "provider",
			RiskScore:   65,
		},
	}
	findings := investigationEvidenceReferenceFindings("driver-visibility", "guard.sys", records)
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %#v", findings)
	}
	if findings[0].Attributes["evidence_id"] != "ev-1" {
		t.Fatalf("unexpected evidence reference: %#v", findings[0])
	}
}
