package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type investigationCommandSpec struct {
	Label   string
	Name    string
	Args    []string
	Command string
}

func collectInvestigationSnapshot(ctx context.Context, ws Workspace, preset, target string, evidence *EvidenceStore) InvestigationSnapshot {
	preset = strings.ToLower(strings.TrimSpace(preset))
	snapshot := InvestigationSnapshot{
		Kind:      preset,
		Target:    strings.TrimSpace(target),
		CreatedAt: time.Now(),
	}
	var findings []InvestigationFinding
	var artifacts []string
	var summaries []string

	for _, spec := range investigationPresetCommands(preset, target) {
		result := runInvestigationCommand(ctx, ws, spec)
		snapshot.Commands = append(snapshot.Commands, result)
		summaries = append(summaries, fmt.Sprintf("%s: %s", spec.Label, investigationResultSummary(result)))
		findings = append(findings, investigationFindingsFromCommand(preset, target, result)...)
	}

	artifacts = append(artifacts, investigationArtifactsForTarget(ws, preset, target)...)
	findings = append(findings, investigationArtifactFindings(preset, target, artifacts)...)

	if evidence != nil {
		if repeated, err := evidence.Search("outcome:failed", ws.BaseRoot, 6); err == nil {
			findings = append(findings, investigationEvidenceReferenceFindings(preset, target, repeated)...)
		}
	}

	snapshot.Artifacts = uniqueStrings(artifacts)
	snapshot.Findings = sortFindingsByRisk(uniqueInvestigationFindings(findings))
	snapshot.RawSummary = compactPersistentMemoryText(strings.Join(uniqueStrings(summaries), " | "), 400)
	return normalizeInvestigationSnapshot(snapshot)
}

func investigationPresetCommands(preset, target string) []investigationCommandSpec {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "driver-load":
		return []investigationCommandSpec{
			{Label: "driver services", Name: "sc", Args: []string{"query", "type=", "driver"}, Command: "sc query type= driver"},
			{Label: "driverquery", Name: "driverquery", Args: []string{"/v"}, Command: "driverquery /v"},
			{Label: "verifier", Name: "verifier", Args: []string{"/querysettings"}, Command: "verifier /querysettings"},
			{Label: "fltmc", Name: "fltmc", Args: nil, Command: "fltmc"},
		}
	case "process-attach":
		return []investigationCommandSpec{
			{Label: "tasklist", Name: "tasklist", Args: []string{"/v"}, Command: "tasklist /v"},
			{Label: "services", Name: "sc", Args: []string{"query"}, Command: "sc query"},
			{Label: "powershell-process", Name: "powershell", Args: []string{"-NoProfile", "-Command", "Get-Process | Select-Object -First 120 Name,Id,Path | Format-Table -AutoSize | Out-String -Width 220"}, Command: "powershell Get-Process"},
		}
	case "telemetry-provider":
		return []investigationCommandSpec{
			{Label: "logman providers", Name: "logman", Args: []string{"query", "providers"}, Command: "logman query providers"},
			{Label: "wevtutil logs", Name: "wevtutil", Args: []string{"el"}, Command: "wevtutil el"},
		}
	default:
		return nil
	}
}

func runInvestigationCommand(ctx context.Context, ws Workspace, spec investigationCommandSpec) InvestigationCommandResult {
	started := time.Now()
	result := InvestigationCommandResult{
		Label:     spec.Label,
		Command:   spec.Command,
		StartedAt: started,
	}
	if _, err := exec.LookPath(spec.Name); err != nil {
		result.Error = "unavailable"
		result.Output = "command not available"
		return result
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, spec.Name, spec.Args...)
	cmd.Dir = ws.Root
	output, err := cmd.CombinedOutput()
	result.DurationMs = time.Since(started).Milliseconds()
	result.Output = compactPersistentMemoryText(string(output), 1200)
	if err != nil {
		result.Error = err.Error()
		result.Success = false
		return result
	}
	result.Success = true
	return result
}

func investigationResultSummary(result InvestigationCommandResult) string {
	if result.Success {
		return "ok"
	}
	if strings.TrimSpace(result.Error) != "" {
		return result.Error
	}
	return "failed"
}

func investigationFindingsFromCommand(preset, target string, result InvestigationCommandResult) []InvestigationFinding {
	var findings []InvestigationFinding
	output := strings.ToLower(result.Output)
	targetLower := strings.ToLower(strings.TrimSpace(target))
	switch preset {
	case "driver-load":
		if result.Label == "verifier" {
			if strings.Contains(output, "no drivers are currently verified") {
				findings = append(findings, InvestigationFinding{Kind: "verifier_state", Category: "driver", Subject: "verifier inactive", Outcome: "passed", Severity: "low", SignalClass: "verifier", RiskScore: 10, Message: "Driver Verifier appears inactive."})
			} else if strings.TrimSpace(output) != "" {
				findings = append(findings, InvestigationFinding{Kind: "verifier_state", Category: "driver", Subject: "verifier active", Outcome: "failed", Severity: "medium", SignalClass: "verifier", RiskScore: 45, Message: "Driver Verifier appears active."})
			}
		}
		if targetLower != "" && (result.Label == "driver services" || result.Label == "driverquery" || result.Label == "fltmc") {
			base := strings.ToLower(strings.TrimSuffix(filepath.Base(targetLower), filepath.Ext(targetLower)))
			if base != "" && !strings.Contains(output, base) {
				findings = append(findings, InvestigationFinding{Kind: "driver_visibility", Category: "driver", Subject: "target driver not listed", Outcome: "failed", Severity: "high", SignalClass: "driver_state", RiskScore: 72, Message: "Target driver was not observed in live driver listings.", Attributes: map[string]string{"target": target}})
			}
		}
	case "process-attach":
		if targetLower != "" && (result.Label == "tasklist" || result.Label == "powershell-process") {
			base := strings.ToLower(strings.TrimSuffix(filepath.Base(targetLower), filepath.Ext(targetLower)))
			if base != "" && !strings.Contains(output, base) {
				findings = append(findings, InvestigationFinding{Kind: "process_presence", Category: "telemetry", Subject: "target process missing", Outcome: "failed", Severity: "high", SignalClass: "process_state", RiskScore: 68, Message: "Target process was not observed in process listings.", Attributes: map[string]string{"target": target}})
			}
		}
	case "telemetry-provider":
		if targetLower != "" && (result.Label == "logman providers" || result.Label == "wevtutil logs") {
			base := strings.ToLower(strings.TrimSuffix(filepath.Base(targetLower), filepath.Ext(targetLower)))
			if base != "" && !strings.Contains(output, base) {
				findings = append(findings, InvestigationFinding{Kind: "provider_presence", Category: "telemetry", Subject: "provider not registered", Outcome: "failed", Severity: "high", SignalClass: "provider", RiskScore: 66, Message: "Target provider was not observed in provider listings.", Attributes: map[string]string{"target": target}})
			}
		}
	}
	if !result.Success && strings.TrimSpace(result.Error) != "" && !strings.EqualFold(result.Error, "unavailable") {
		findings = append(findings, InvestigationFinding{
			Kind:        "command_error",
			Category:    investigationCategoryForPreset(preset),
			Subject:     result.Label + " failed",
			Outcome:     "failed",
			Severity:    "medium",
			SignalClass: "runtime",
			RiskScore:   35,
			Message:     result.Error,
		})
	}
	return findings
}

func investigationCategoryForPreset(preset string) string {
	switch preset {
	case "driver-load":
		return "driver"
	case "telemetry-provider":
		return "telemetry"
	case "memory-scan":
		return "memory-scan"
	case "unreal-integrity":
		return "unreal"
	default:
		return "telemetry"
	}
}

func investigationArtifactsForTarget(ws Workspace, preset, target string) []string {
	var artifacts []string
	if strings.TrimSpace(target) == "" {
		return nil
	}
	candidates := []string{target}
	if strings.EqualFold(preset, "driver-load") {
		base := strings.TrimSuffix(target, filepath.Ext(target))
		candidates = append(candidates, base+".sys", base+".inf", base+".cat")
	}
	if strings.EqualFold(preset, "telemetry-provider") {
		base := strings.TrimSuffix(target, filepath.Ext(target))
		candidates = append(candidates, base+".man", base+".xml", base+".mc")
	}
	for _, candidate := range candidates {
		if path := resolveInvestigationArtifact(ws, candidate); path != "" {
			artifacts = append(artifacts, path)
		}
	}
	return uniqueStrings(artifacts)
}

func resolveInvestigationArtifact(ws Workspace, candidate string) string {
	value := strings.TrimSpace(candidate)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		if _, err := os.Stat(value); err == nil {
			return value
		}
		return ""
	}
	for _, root := range []string{ws.Root, ws.BaseRoot} {
		if strings.TrimSpace(root) == "" {
			continue
		}
		path := filepath.Join(root, value)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func investigationArtifactFindings(preset, target string, artifacts []string) []InvestigationFinding {
	if strings.TrimSpace(target) == "" {
		return nil
	}
	if len(artifacts) > 0 {
		return nil
	}
	category := investigationCategoryForPreset(preset)
	signal := "artifact"
	if preset == "driver-load" {
		signal = "driver_artifact"
	}
	if preset == "telemetry-provider" {
		signal = "provider"
	}
	return []InvestigationFinding{{
		Kind:        "artifact_presence",
		Category:    category,
		Subject:     "target artifact missing",
		Outcome:     "failed",
		Severity:    "high",
		SignalClass: signal,
		RiskScore:   70,
		Message:     "Expected target-related artifacts were not found in the workspace or by absolute path.",
		Attributes:  map[string]string{"target": target},
	}}
}

func investigationEvidenceReferenceFindings(preset, target string, records []EvidenceRecord) []InvestigationFinding {
	category := investigationCategoryForPreset(preset)
	var findings []InvestigationFinding
	for _, record := range records {
		if category != "" && !strings.EqualFold(record.Category, category) {
			continue
		}
		if strings.TrimSpace(target) != "" {
			base := strings.ToLower(strings.TrimSuffix(filepath.Base(target), filepath.Ext(target)))
			if base != "" && !strings.Contains(strings.ToLower(record.Subject), base) && !strings.Contains(strings.ToLower(record.VerificationSummary), base) {
				continue
			}
		}
		findings = append(findings, InvestigationFinding{
			Kind:        "evidence_reference",
			Category:    record.Category,
			Subject:     "repeated failed evidence still relevant",
			Outcome:     "failed",
			Severity:    record.Severity,
			SignalClass: record.SignalClass,
			RiskScore:   max(40, record.RiskScore),
			Message:     compactPersistentMemoryText(record.Subject+" | "+record.VerificationSummary, 180),
			Attributes:  map[string]string{"evidence_id": record.ID},
		})
	}
	return uniqueInvestigationFindings(findings)
}

func uniqueInvestigationFindings(findings []InvestigationFinding) []InvestigationFinding {
	var out []InvestigationFinding
	seen := map[string]bool{}
	for _, finding := range findings {
		finding = normalizeInvestigationFinding(finding)
		key := strings.ToLower(strings.Join([]string{
			finding.Kind,
			finding.Category,
			finding.Subject,
			finding.Outcome,
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
