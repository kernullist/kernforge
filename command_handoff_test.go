package main

import (
	"strings"
	"testing"
)

func TestSimulationHandoffGuidesVerificationAndEvidence(t *testing.T) {
	out := simulationHandoff(SimulationResult{
		ID: "sim-1",
		Findings: []SimulationFinding{
			{Subject: "tamper path", RiskScore: 80},
		},
	})
	for _, needle := range []string{
		"Simulation handoff:",
		"Verify: /verify",
		"Explore: /simulate-dashboard",
		"Evidence: /evidence-dashboard",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("expected handoff to include %q, got:\n%s", needle, out)
		}
	}
}

func TestVerificationHandoffGuidesFailureAndSuccess(t *testing.T) {
	failed := verificationHandoff(VerificationReport{
		Steps: []VerificationStep{
			{Label: "unit tests", Status: VerificationFailed, FailureKind: "test_failure"},
		},
	}, FeatureWorkflow{}, false)
	for _, needle := range []string{
		"Verification handoff:",
		"Retry: /verify",
		"Inspect: /verify-dashboard",
	} {
		if !strings.Contains(failed, needle) {
			t.Fatalf("expected failed handoff to include %q, got:\n%s", needle, failed)
		}
	}

	passed := verificationHandoff(VerificationReport{
		Steps: []VerificationStep{
			{Label: "unit tests", Status: VerificationPassed},
		},
	}, FeatureWorkflow{ID: "feature-1", Status: featureStatusImplemented}, true)
	for _, needle := range []string{
		"Close feature: /new-feature close",
		"Checkpoint: /checkpoint verified-state",
	} {
		if !strings.Contains(passed, needle) {
			t.Fatalf("expected passed handoff to include %q, got:\n%s", needle, passed)
		}
	}

	planned := verificationHandoff(VerificationReport{
		Steps: []VerificationStep{
			{Label: "unit tests", Status: VerificationPassed},
		},
	}, FeatureWorkflow{ID: "feature-1", Status: featureStatusPlanned}, true)
	if strings.Contains(planned, "/new-feature close") || !strings.Contains(planned, "Feature: /new-feature status") {
		t.Fatalf("expected planned feature handoff to inspect status instead of close, got:\n%s", planned)
	}
}

func TestInvestigationAndPerformanceHandoffs(t *testing.T) {
	investigation := investigationHandoffAfterSnapshot(InvestigationRecord{
		ID:     "inv-1",
		Preset: "tamper",
	}, InvestigationSnapshot{
		ID: "snap-1",
		Findings: []InvestigationFinding{
			{Subject: "driver surface", RiskScore: 60},
		},
	})
	if !strings.Contains(investigation, "Next: /simulate tamper-surface") {
		t.Fatalf("expected investigation handoff to suggest tamper simulation, got:\n%s", investigation)
	}

	performance := performanceHandoff(PerformanceAnalysisResult{
		TopHotspots: []PerformanceHotspot{
			{EntryPoints: []string{"ParsePacket"}},
		},
	})
	if !strings.Contains(performance, "Target drilldown: /fuzz-func ParsePacket") {
		t.Fatalf("expected performance handoff to suggest fuzz drilldown, got:\n%s", performance)
	}
}

func TestEvidenceAndMemoryHandoffs(t *testing.T) {
	evidence := evidenceHandoff([]EvidenceRecord{
		{
			ID:          "ev-1",
			Kind:        "simulation_finding",
			Outcome:     "failed",
			SignalClass: "simulation",
			RiskScore:   75,
		},
	})
	for _, needle := range []string{
		"Evidence handoff:",
		"Verify: /verify",
		"Explore: /evidence-dashboard",
		"Source: /simulate-dashboard",
	} {
		if !strings.Contains(evidence, needle) {
			t.Fatalf("expected evidence handoff to include %q, got:\n%s", needle, evidence)
		}
	}

	memory := memoryHandoff([]PersistentMemoryRecord{
		{
			ID:                   "mem-1",
			Trust:                PersistentMemoryTentative,
			Importance:           PersistentMemoryMedium,
			VerificationFailures: []string{"test_failure"},
			VerificationMaxRisk:  70,
		},
	})
	for _, needle := range []string{
		"Memory handoff:",
		"Verify: /verify",
		"Confirm: /mem-confirm mem-1",
		"Promote: /mem-promote mem-1",
	} {
		if !strings.Contains(memory, needle) {
			t.Fatalf("expected memory handoff to include %q, got:\n%s", needle, memory)
		}
	}

	anonymousMemory := memoryHandoff([]PersistentMemoryRecord{
		{
			Trust:               PersistentMemoryTentative,
			Importance:          PersistentMemoryMedium,
			VerificationMaxRisk: 70,
		},
	})
	if strings.Contains(anonymousMemory, "/mem-confirm ") || strings.Contains(anonymousMemory, "/mem-promote ") {
		t.Fatalf("expected memory handoff without id to avoid invalid commands, got:\n%s", anonymousMemory)
	}

	fuzzEvidence := evidenceHandoff([]EvidenceRecord{
		{
			ID:          "ev-fuzz-1",
			Kind:        "fuzz_native_result",
			Outcome:     "failed",
			SignalClass: "fuzzing",
			RiskScore:   85,
		},
	})
	for _, needle := range []string{
		"Verify: /verify",
		"Source: /fuzz-campaign",
	} {
		if !strings.Contains(fuzzEvidence, needle) {
			t.Fatalf("expected fuzz evidence handoff to include %q, got:\n%s", needle, fuzzEvidence)
		}
	}
}

func TestCheckpointFeatureAndWorktreeHandoffs(t *testing.T) {
	checkpoint := checkpointHandoffAfterCreate(CheckpointMetadata{ID: "cp-1", FileCount: 3})
	if !strings.Contains(checkpoint, "Inspect: /checkpoint-diff latest") {
		t.Fatalf("expected checkpoint handoff to suggest diff, got:\n%s", checkpoint)
	}

	feature := featureStatusHandoff(FeatureWorkflow{ID: "feat-1", Status: featureStatusImplemented})
	if !strings.Contains(feature, "Verify: /verify") || !strings.Contains(feature, "Close: /new-feature close") {
		t.Fatalf("expected implemented feature handoff, got:\n%s", feature)
	}

	featureFuzz := featureFuzzHandoff(FeatureWorkflow{ID: "feat-1", Status: featureStatusImplemented}, FuzzCampaign{
		ID: "campaign-1",
		NativeResults: []FuzzCampaignNativeResult{
			{RunID: "ff-1", Outcome: "failed", CrashCount: 1},
		},
	})
	for _, needle := range []string{
		"Feature fuzz handoff:",
		"Verify: /verify",
		"Evidence: /evidence-search kind:fuzz_native_result",
		"Campaign: /fuzz-campaign run",
	} {
		if !strings.Contains(featureFuzz, needle) {
			t.Fatalf("expected feature fuzz handoff to include %q, got:\n%s", needle, featureFuzz)
		}
	}

	worktree := worktreeHandoff("create", "feat-1")
	if strings.Contains(worktree, "/new-feature implement") || !strings.Contains(worktree, "Feature: /new-feature status") {
		t.Fatalf("expected worktree handoff to inspect feature status, got:\n%s", worktree)
	}

	specialist := specialistAssignHandoff("node-1", "feat-1")
	if strings.Contains(specialist, "/new-feature implement") || !strings.Contains(specialist, "Feature: /new-feature status") {
		t.Fatalf("expected specialist handoff to inspect feature status, got:\n%s", specialist)
	}
}
