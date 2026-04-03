package main

import (
	"testing"
	"time"
)

func TestRunSimulationProfileTamperSurface(t *testing.T) {
	result := runSimulationProfile(
		"tamper-surface",
		"guard.sys",
		"C:\\repo",
		[]EvidenceRecord{
			{
				ID:          "ev-1",
				Category:    "driver",
				Subject:     "guard.sys signing failed",
				Outcome:     "failed",
				Severity:    "critical",
				SignalClass: "signing",
				RiskScore:   88,
			},
		},
		nil,
		nil,
	)
	if len(result.Findings) == 0 {
		t.Fatalf("expected findings, got %#v", result)
	}
	if result.Findings[0].Subject != "unsigned-or-unverified-driver-surface" {
		t.Fatalf("unexpected top finding: %#v", result.Findings[0])
	}
}

func TestRunSimulationProfileForensicBlindSpot(t *testing.T) {
	report := &VerificationReport{
		GeneratedAt: time.Now(),
		Steps: []VerificationStep{
			{Label: "go test ./...", Status: VerificationFailed, FailureKind: "runtime_error"},
		},
	}
	result := runSimulationProfile(
		"forensic-blind-spot",
		"game.exe",
		"C:\\repo",
		[]EvidenceRecord{
			{ID: "ev-1", Kind: "verification_failure", Outcome: "failed", Category: "driver", Subject: "runtime_error"},
		},
		nil,
		report,
	)
	if len(result.Findings) == 0 {
		t.Fatalf("expected findings, got %#v", result)
	}
}
