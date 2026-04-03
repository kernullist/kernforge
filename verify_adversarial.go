package main

import (
	"fmt"
	"strings"
)

func buildRecentAdversarialVerificationSteps(root string) ([]VerificationStep, string) {
	evidence := NewEvidenceStore()
	investigations := NewInvestigationStore()
	simulations := NewSimulationStore()

	evidenceItems := []EvidenceRecord{}
	if evidence != nil {
		if items, err := evidence.Search("kind:simulation_finding outcome:failed", root, 16); err == nil {
			evidenceItems = items
		}
	}
	investigationItems := []InvestigationRecord{}
	if investigations != nil {
		if items, err := investigations.ListRecent(root, 4); err == nil {
			investigationItems = items
		}
	}
	simulationItems := []SimulationResult{}
	if simulations != nil {
		if items, err := simulations.ListRecent(root, 4); err == nil {
			simulationItems = items
		}
	}
	return buildAdversarialVerificationStepsFromState(evidenceItems, investigationItems, simulationItems)
}

func buildAdversarialVerificationStepsFromState(evidenceItems []EvidenceRecord, investigationItems []InvestigationRecord, simulationItems []SimulationResult) ([]VerificationStep, string) {
	var steps []VerificationStep
	var notes []string

	if hasHighRiskSimulationSignal(evidenceItems, "tamper", 60) {
		steps = append(steps, VerificationStep{
			Label:   "tamper surface follow-up review",
			Command: `echo Recent tamper-surface simulation findings detected. Re-check integrity enforcement, signing gates, registration paths, and tamper-resistance assumptions before treating the change as verified.`,
			Scope:   "targeted",
			Stage:   "targeted",
			Tags:    []string{"simulation", "tamper", "security"},
			Status:  VerificationPending,
		})
		notes = append(notes, "recent_simulation=tamper")
	}
	if hasHighRiskSimulationSignal(evidenceItems, "stealth", 55) {
		steps = append(steps, VerificationStep{
			Label:   "stealth surface follow-up review",
			Command: `echo Recent stealth-surface simulation findings detected. Re-check observer coverage, telemetry visibility, and module or process paths that could fall outside normal monitoring.`,
			Scope:   "targeted",
			Stage:   "targeted",
			Tags:    []string{"simulation", "stealth", "security"},
			Status:  VerificationPending,
		})
		notes = append(notes, "recent_simulation=stealth")
	}
	if hasHighRiskSimulationSignal(evidenceItems, "forensics", 50) {
		steps = append(steps, VerificationStep{
			Label:   "forensic blind spot follow-up review",
			Command: `echo Recent forensic-blind-spot simulation findings detected. Re-check artifact retention, audit trails, snapshot capture, and post-incident reconstruction assumptions.`,
			Scope:   "targeted",
			Stage:   "targeted",
			Tags:    []string{"simulation", "forensics", "security"},
			Status:  VerificationPending,
		})
		notes = append(notes, "recent_simulation=forensics")
	}

	activeCount := countActiveInvestigations(investigationItems)
	if activeCount > 0 {
		steps = append(steps, VerificationStep{
			Label:   "active investigation alignment review",
			Command: `echo An active investigation session exists for this workspace. Re-check that verification covers the same live findings, targets, and suspected risk surfaces before closing the loop.`,
			Scope:   "workspace",
			Stage:   "targeted",
			Tags:    []string{"investigation", "security"},
			Status:  VerificationPending,
		})
		notes = append(notes, fmt.Sprintf("active_investigations=%d", activeCount))
	}

	if hasHighRiskInvestigationFinding(investigationItems, 65) {
		steps = append(steps, VerificationStep{
			Label:   "investigation finding follow-up review",
			Command: `echo Recent live investigation findings include high-risk signals. Re-check the exact subject, service, driver, or provider implicated by those findings during verification.`,
			Scope:   "targeted",
			Stage:   "targeted",
			Tags:    []string{"investigation", "security"},
			Status:  VerificationPending,
		})
		notes = append(notes, "recent_investigation=high-risk")
	}

	if hasHighRiskSimulationRun(simulationItems, 70) {
		notes = append(notes, "recent_simulation_run=high-risk")
	}

	steps = uniqueVerificationSteps(steps)
	return steps, strings.Join(uniqueStrings(notes), " ")
}

func hasHighRiskSimulationSignal(records []EvidenceRecord, signal string, minRisk int) bool {
	for _, record := range records {
		if !strings.EqualFold(record.Kind, "simulation_finding") {
			continue
		}
		if !strings.EqualFold(record.Outcome, "failed") {
			continue
		}
		if !strings.EqualFold(record.SignalClass, signal) {
			continue
		}
		if record.RiskScore >= minRisk {
			return true
		}
	}
	return false
}

func countActiveInvestigations(items []InvestigationRecord) int {
	count := 0
	for _, item := range items {
		if item.Status == InvestigationActive {
			count++
		}
	}
	return count
}

func hasHighRiskInvestigationFinding(items []InvestigationRecord, minRisk int) bool {
	for _, item := range items {
		for _, snapshot := range item.Snapshots {
			for _, finding := range snapshot.Findings {
				if finding.RiskScore >= minRisk {
					return true
				}
			}
		}
	}
	return false
}

func hasHighRiskSimulationRun(items []SimulationResult, minRisk int) bool {
	for _, item := range items {
		for _, finding := range item.Findings {
			if finding.RiskScore >= minRisk {
				return true
			}
		}
	}
	return false
}
