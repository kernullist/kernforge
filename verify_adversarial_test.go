package main

import (
	"strings"
	"testing"
	"time"
)

func TestBuildAdversarialVerificationStepsFromStateAddsSimulationAndInvestigationFollowUps(t *testing.T) {
	steps, note := buildAdversarialVerificationStepsFromState(
		[]EvidenceRecord{
			{
				Kind:        "simulation_finding",
				Outcome:     "failed",
				SignalClass: "tamper",
				RiskScore:   82,
			},
			{
				Kind:        "simulation_finding",
				Outcome:     "failed",
				SignalClass: "forensics",
				RiskScore:   65,
			},
		},
		[]InvestigationRecord{
			{
				Status: InvestigationActive,
				Snapshots: []InvestigationSnapshot{
					{
						Findings: []InvestigationFinding{
							{Subject: "guard.sys", RiskScore: 72},
						},
					},
				},
			},
		},
		[]SimulationResult{
			{
				CreatedAt: time.Now(),
				Findings: []SimulationFinding{
					{Subject: "unsigned surface", RiskScore: 88},
				},
			},
		},
	)
	if len(steps) < 3 {
		t.Fatalf("expected multiple adversarial follow-up steps, got %#v", steps)
	}
	joinedLabels := strings.ToLower(steps[0].Label + " " + steps[1].Label + " " + steps[2].Label)
	for _, needle := range []string{"tamper", "forensic", "investigation"} {
		if !strings.Contains(joinedLabels, needle) && !strings.Contains(strings.ToLower(note), needle) {
			t.Fatalf("expected adversarial note or labels to include %q, got labels=%q note=%q", needle, joinedLabels, note)
		}
	}
}
