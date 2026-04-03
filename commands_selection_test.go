package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendSelectionSimulationReviewContext(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		Workspace:           root,
		Kind:                "simulation_finding",
		Category:            "driver",
		Subject:             "src/guard.cpp tamper path exposed",
		Outcome:             "failed",
		Severity:            "high",
		SignalClass:         "tamper",
		RiskScore:           72,
		VerificationSummary: "selection overlaps guard.cpp path",
		Tags:                []string{"simulation", "tamper-surface"},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	rt := &runtimeState{
		evidence: evidence,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}
	prompt := rt.appendSelectionSimulationReviewContext("review prompt", []ViewerSelection{
		{FilePath: "src/guard.cpp"},
	})
	if !strings.Contains(prompt, "Additional simulation risk focus:") {
		t.Fatalf("expected simulation risk context in prompt, got %q", prompt)
	}
}

func TestAppendSimulationPlanningContext(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		Workspace:           root,
		Kind:                "simulation_finding",
		Category:            "driver",
		Subject:             "guard signing bypass surface",
		Outcome:             "failed",
		Severity:            "critical",
		SignalClass:         "tamper",
		RiskScore:           88,
		VerificationSummary: "driver guard signing path is bypassable",
		Tags:                []string{"simulation", "tamper-surface"},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	rt := &runtimeState{
		evidence: evidence,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}
	prompt := rt.appendSimulationPlanningContext("plan prompt", "investigate guard signing hardening")
	if !strings.Contains(prompt, "Additional simulation planning focus:") {
		t.Fatalf("expected planning context in prompt, got %q", prompt)
	}
}
