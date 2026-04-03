package main

import (
	"path/filepath"
	"testing"
)

func TestBuildEvidenceRecordsFromVerification(t *testing.T) {
	dir := t.TempDir()
	sess := NewSession(dir, "fake", "fake-model", "", "default")
	sess.LastVerification = &VerificationReport{
		Decision: "Security-aware verification detected categories: driver, telemetry. security_categories=driver,telemetry",
		Steps: []VerificationStep{
			{
				Label:  "signtool verify driver/guard.sys",
				Scope:  "driver/guard.sys",
				Stage:  "targeted",
				Tags:   []string{"driver", "signing", "security"},
				Status: VerificationPassed,
			},
			{
				Label:       "telemetry XML validation telemetry/provider.man",
				Scope:       "telemetry/provider.man",
				Stage:       "targeted",
				Tags:        []string{"telemetry", "xml", "security"},
				Status:      VerificationFailed,
				FailureKind: "runtime_error",
			},
		},
	}

	records := buildEvidenceRecords(Workspace{BaseRoot: dir, Root: dir}, sess)
	if len(records) < 4 {
		t.Fatalf("expected multiple evidence records, got %#v", records)
	}

	var foundCategory bool
	var foundArtifact bool
	var foundFailure bool
	for _, record := range records {
		switch {
		case record.Kind == "verification_category" && record.Subject == "driver":
			if record.Severity == "" || record.SignalClass == "" || record.RiskScore == 0 {
				t.Fatalf("expected scored category evidence, got %#v", record)
			}
			foundCategory = true
		case record.Kind == "verification_artifact" && record.Subject == "driver/guard.sys":
			if record.Severity != "low" {
				t.Fatalf("expected passed driver artifact severity to stay low, got %#v", record)
			}
			if record.SignalClass != "signing" {
				t.Fatalf("expected signing signal for driver artifact, got %#v", record)
			}
			foundArtifact = true
		case record.Kind == "verification_failure" && record.Subject == "runtime_error":
			if record.Severity == "" || record.SignalClass == "" || record.RiskScore == 0 {
				t.Fatalf("expected scored failure evidence, got %#v", record)
			}
			foundFailure = true
		}
	}
	if !foundCategory || !foundArtifact || !foundFailure {
		t.Fatalf("missing expected evidence records: %#v", records)
	}
}

func TestEvidenceStoreCaptureVerification(t *testing.T) {
	dir := t.TempDir()
	store := &EvidenceStore{
		Path: filepath.Join(dir, "evidence.json"),
	}
	sess := NewSession(dir, "fake", "fake-model", "", "default")
	sess.LastVerification = &VerificationReport{
		Decision: "security_categories=driver",
		Steps: []VerificationStep{
			{
				Label:  "signtool verify driver/guard.sys",
				Scope:  "driver/guard.sys",
				Tags:   []string{"driver", "signing"},
				Status: VerificationPassed,
			},
		},
	}
	if err := store.CaptureVerification(Workspace{BaseRoot: dir, Root: dir}, sess); err != nil {
		t.Fatalf("CaptureVerification: %v", err)
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Count == 0 {
		t.Fatalf("expected evidence records to be stored, got %#v", stats)
	}
}
