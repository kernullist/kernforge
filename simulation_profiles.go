package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func runSimulationProfile(profile, target, workspace string, evidence []EvidenceRecord, investigations []InvestigationRecord, report *VerificationReport) SimulationResult {
	profile = strings.ToLower(strings.TrimSpace(profile))
	result := SimulationResult{
		Workspace: workspace,
		Profile:   profile,
		Target:    strings.TrimSpace(target),
		CreatedAt: time.Now(),
	}
	for _, item := range evidence {
		result.SourceEvidenceIDs = append(result.SourceEvidenceIDs, item.ID)
	}
	for _, item := range investigations {
		result.SourceInvestigationIDs = append(result.SourceInvestigationIDs, item.ID)
	}
	switch profile {
	case "tamper-surface":
		result.Findings = evaluateTamperSurface(target, evidence, investigations)
	case "stealth-surface":
		result.Findings = evaluateStealthSurface(target, evidence, investigations)
	case "forensic-blind-spot":
		result.Findings = evaluateForensicBlindSpot(target, evidence, investigations, report)
	}
	result.Findings = sortSimulationFindings(uniqueSimulationFindings(result.Findings))
	result.Tags = []string{"simulation", profile}
	if len(result.Findings) > 0 {
		var top []string
		for _, finding := range result.Findings {
			top = append(top, finding.Subject)
			if len(top) >= 3 {
				break
			}
		}
		result.Summary = fmt.Sprintf("%s found %d issue(s): %s", profile, len(result.Findings), strings.Join(top, ", "))
	} else {
		result.Summary = fmt.Sprintf("%s found no high-signal issues", profile)
	}
	return normalizeSimulationResult(result)
}

func evaluateTamperSurface(target string, evidence []EvidenceRecord, investigations []InvestigationRecord) []SimulationFinding {
	var findings []SimulationFinding
	targetBase := strings.ToLower(strings.TrimSuffix(filepath.Base(strings.TrimSpace(target)), filepath.Ext(strings.TrimSpace(target))))
	for _, item := range evidence {
		if !strings.EqualFold(item.Outcome, "failed") {
			continue
		}
		if targetBase != "" && !strings.Contains(strings.ToLower(item.Subject), targetBase) && !strings.Contains(strings.ToLower(item.VerificationSummary), targetBase) {
			continue
		}
		if strings.EqualFold(item.SignalClass, "signing") || strings.Contains(strings.ToLower(item.Subject), "artifact missing") {
			findings = append(findings, SimulationFinding{
				Kind:        "tamper_surface",
				Category:    item.Category,
				Subject:     "unsigned-or-unverified-driver-surface",
				Severity:    "critical",
				SignalClass: "tamper",
				RiskScore:   max(82, item.RiskScore),
				Message:     "A signing or artifact weakness suggests a straightforward replacement or registration tamper surface.",
				RecommendedActions: []string{
					"/investigate start driver-visibility " + simulationValueOrDefault(target, item.Subject),
					"/verify",
				},
			})
		}
		if strings.EqualFold(item.SignalClass, "provider") {
			findings = append(findings, SimulationFinding{
				Kind:        "tamper_surface",
				Category:    "telemetry",
				Subject:     "provider-registration-risk-surface",
				Severity:    "high",
				SignalClass: "tamper",
				RiskScore:   max(68, item.RiskScore),
				Message:     "Provider registration weakness can allow telemetry degradation or visibility gaps.",
				RecommendedActions: []string{
					"/investigate start provider-visibility " + simulationValueOrDefault(target, item.Subject),
					"/evidence-search category:telemetry outcome:failed",
				},
			})
		}
	}
	for _, inv := range investigations {
		for _, snap := range inv.Snapshots {
			for _, finding := range snap.Findings {
				if strings.Contains(strings.ToLower(finding.Subject), "not listed") {
					findings = append(findings, SimulationFinding{
						Kind:        "tamper_surface",
						Category:    finding.Category,
						Subject:     "repeated-failure-path-keeps-tamper-risk-open",
						Severity:    "high",
						SignalClass: "tamper",
						RiskScore:   max(72, finding.RiskScore),
						Message:     "Repeated inability to observe the target suggests an unresolved tamper-related risk path.",
						RecommendedActions: []string{
							"/investigate snapshot",
							"/evidence-search outcome:failed",
						},
					})
				}
			}
		}
	}
	return findings
}

func evaluateStealthSurface(target string, evidence []EvidenceRecord, investigations []InvestigationRecord) []SimulationFinding {
	var findings []SimulationFinding
	observerActive := false
	for _, inv := range investigations {
		for _, snap := range inv.Snapshots {
			for _, finding := range snap.Findings {
				if strings.Contains(strings.ToLower(finding.Subject), "verifier active") {
					observerActive = true
				}
				if strings.Contains(strings.ToLower(finding.Subject), "target process missing") || strings.Contains(strings.ToLower(finding.Subject), "target driver not listed") {
					findings = append(findings, SimulationFinding{
						Kind:        "stealth_surface",
						Category:    finding.Category,
						Subject:     "visibility-gap-around-target",
						Severity:    "high",
						SignalClass: "stealth",
						RiskScore:   max(70, finding.RiskScore),
						Message:     "The target can disappear from normal observation paths, leaving a visibility gap.",
						RecommendedActions: []string{
							"/investigate snapshot " + strings.TrimSpace(target),
							"/simulate forensic-blind-spot " + strings.TrimSpace(target),
						},
					})
				}
			}
		}
	}
	if !observerActive {
		findings = append(findings, SimulationFinding{
			Kind:        "stealth_surface",
			Category:    "driver",
			Subject:     "observer-not-active",
			Severity:    "medium",
			SignalClass: "stealth",
			RiskScore:   48,
			Message:     "Recent investigation state did not show strong signs of active observer instrumentation.",
			RecommendedActions: []string{
				"/investigate start driver-visibility " + strings.TrimSpace(target),
			},
		})
	}
	if len(evidence) > 0 {
		categorySet := map[string]bool{}
		for _, item := range evidence {
			if strings.EqualFold(item.Outcome, "failed") {
				categorySet[item.Category] = true
			}
		}
		if len(categorySet) == 1 {
			findings = append(findings, SimulationFinding{
				Kind:        "stealth_surface",
				Category:    firstCategory(categorySet),
				Subject:     "single-signal-dependency",
				Severity:    "medium",
				SignalClass: "stealth",
				RiskScore:   44,
				Message:     "Recent failure evidence is concentrated in one category, which may leave alternate visibility gaps weakly covered.",
				RecommendedActions: []string{
					"/evidence-dashboard",
					"/investigate list",
				},
			})
		}
	}
	return findings
}

func evaluateForensicBlindSpot(target string, evidence []EvidenceRecord, investigations []InvestigationRecord, report *VerificationReport) []SimulationFinding {
	var findings []SimulationFinding
	failedCount := 0
	artifactEvidence := 0
	for _, item := range evidence {
		if strings.EqualFold(item.Outcome, "failed") {
			failedCount++
		}
		if strings.EqualFold(item.Kind, "verification_artifact") || strings.EqualFold(item.Kind, "investigation_snapshot") {
			artifactEvidence++
		}
	}
	if failedCount > 0 && artifactEvidence == 0 {
		findings = append(findings, SimulationFinding{
			Kind:        "forensic_blind_spot",
			Category:    "policy",
			Subject:     "low-artifact-forensics",
			Severity:    "high",
			SignalClass: "forensics",
			RiskScore:   66,
			Message:     "Failures exist, but there is little artifact-backed evidence to reconstruct what happened later.",
			RecommendedActions: []string{
				"/investigate start process-visibility " + strings.TrimSpace(target),
				"/investigate snapshot",
			},
		})
	}
	if failedCount > 0 && len(investigations) == 0 {
		findings = append(findings, SimulationFinding{
			Kind:        "forensic_blind_spot",
			Category:    "policy",
			Subject:     "failure-without-repro-observation",
			Severity:    "high",
			SignalClass: "forensics",
			RiskScore:   64,
			Message:     "Recent failures exist without a corresponding live investigation record.",
			RecommendedActions: []string{
				"/investigate start process-visibility " + strings.TrimSpace(target),
			},
		})
	}
	overrideCount := 0
	for _, item := range evidence {
		if strings.EqualFold(item.Kind, "hook_override") {
			overrideCount++
		}
	}
	if overrideCount > 0 && len(investigations) == 0 {
		findings = append(findings, SimulationFinding{
			Kind:        "forensic_blind_spot",
			Category:    "policy",
			Subject:     "override-without-strong-audit-context",
			Severity:    "medium",
			SignalClass: "forensics",
			RiskScore:   42,
			Message:     "Overrides were used without a nearby investigation context, which weakens incident reconstruction.",
			RecommendedActions: []string{
				"/override",
				"/investigate list",
			},
		})
	}
	if report != nil && report.HasFailures() && len(investigations) == 0 {
		findings = append(findings, SimulationFinding{
			Kind:        "forensic_blind_spot",
			Category:    "policy",
			Subject:     "missing-live-snapshot",
			Severity:    "medium",
			SignalClass: "forensics",
			RiskScore:   46,
			Message:     "Verification failed recently, but there is no linked live snapshot to preserve runtime state.",
			RecommendedActions: []string{
				"/investigate start process-visibility " + strings.TrimSpace(target),
				"/verify-dashboard",
			},
		})
	}
	return findings
}

func uniqueSimulationFindings(findings []SimulationFinding) []SimulationFinding {
	var out []SimulationFinding
	seen := map[string]bool{}
	for _, finding := range findings {
		finding = normalizeSimulationFinding(finding)
		key := strings.ToLower(strings.Join([]string{
			finding.Kind,
			finding.Category,
			finding.Subject,
			finding.Severity,
			finding.SignalClass,
		}, "\x1f"))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, finding)
	}
	return out
}

func firstCategory(items map[string]bool) string {
	for key := range items {
		return key
	}
	return ""
}

func simulationValueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
