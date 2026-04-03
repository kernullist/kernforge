package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScoreEvidenceRecordDriverSigningFailure(t *testing.T) {
	record := EvidenceRecord{
		Workspace: filepath.Join("F:", "repo"),
		CreatedAt: time.Now(),
		Kind:      "verification_artifact",
		Category:  "driver",
		Subject:   "build/guard.sys",
		Outcome:   "failed",
		Tags:      []string{"artifact", "driver", "signing"},
	}
	scored := scoreEvidenceRecord(record, buildEvidenceScoringContext(nil, record.Workspace, time.Now()))
	if scored.SignalClass != "signing" {
		t.Fatalf("expected signing signal, got %#v", scored)
	}
	if scored.Severity != "critical" {
		t.Fatalf("expected critical severity, got %#v", scored)
	}
	if scored.RiskScore < 80 {
		t.Fatalf("expected high risk score, got %#v", scored)
	}
	if !strings.Contains(strings.Join(scored.SeverityReasons, " "), "signing") {
		t.Fatalf("expected signing reason, got %#v", scored.SeverityReasons)
	}
}

func TestScoreEvidenceRecordTelemetryXMLFailure(t *testing.T) {
	record := EvidenceRecord{
		Workspace: filepath.Join("F:", "repo"),
		CreatedAt: time.Now(),
		Kind:      "verification_failure",
		Category:  "telemetry",
		Subject:   "telemetry/provider.xml",
		Outcome:   "failed",
		Tags:      []string{"failure", "telemetry", "xml"},
	}
	scored := scoreEvidenceRecord(record, buildEvidenceScoringContext(nil, record.Workspace, time.Now()))
	if scored.SignalClass != "xml" {
		t.Fatalf("expected xml signal, got %#v", scored)
	}
	if scored.Severity != "medium" {
		t.Fatalf("expected medium severity, got %#v", scored)
	}
	if scored.Confidence != "high" {
		t.Fatalf("expected high confidence, got %#v", scored)
	}
}

func TestScoreEvidenceRecordUsesRepeatFailures(t *testing.T) {
	now := time.Now()
	record := EvidenceRecord{
		Workspace: filepath.Join("F:", "repo"),
		CreatedAt: now,
		Kind:      "verification_artifact",
		Category:  "driver",
		Subject:   "build/guard.sys",
		Outcome:   "failed",
		Tags:      []string{"artifact", "driver", "signing"},
	}
	ctx := buildEvidenceScoringContext([]EvidenceRecord{
		{
			Workspace:   record.Workspace,
			CreatedAt:   now.Add(-2 * time.Hour),
			Kind:        "verification_artifact",
			Category:    "driver",
			Subject:     "build/guard.sys",
			Outcome:     "failed",
			SignalClass: "signing",
		},
		{
			Workspace:   record.Workspace,
			CreatedAt:   now.Add(-4 * time.Hour),
			Kind:        "verification_artifact",
			Category:    "driver",
			Subject:     "build/guard.sys",
			Outcome:     "failed",
			SignalClass: "signing",
		},
	}, record.Workspace, now)
	scored := scoreEvidenceRecord(record, ctx)
	if scored.Severity != "critical" {
		t.Fatalf("expected critical severity from repeated failures, got %#v", scored)
	}
	if scored.RiskScore < 90 {
		t.Fatalf("expected repeated failures to raise risk score, got %#v", scored)
	}
}
