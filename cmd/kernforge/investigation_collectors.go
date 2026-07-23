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

func normalizeInvestigationPreset(preset string) string {
	value := strings.ToLower(strings.TrimSpace(preset))
	switch value {
	case "driver-load":
		return "driver-visibility"
	case "process-attach":
		return "process-visibility"
	case "telemetry-provider":
		return "provider-visibility"
	case "platform", "platform-posture", "vbs-hvci", "security-posture":
		return "platform-security"
	default:
		return value
	}
}

func collectInvestigationSnapshot(ctx context.Context, ws Workspace, preset, target string, evidence *EvidenceStore) InvestigationSnapshot {
	preset = normalizeInvestigationPreset(preset)
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

	if preset == "platform-security" {
		posture := collectPlatformSecurityPosture(snapshot.Commands)
		snapshot.Attributes = mergeStringMaps(snapshot.Attributes, posture.Fields)
		findings = append(findings, platformSecurityPostureFindings(posture)...)
		if posture.Summary != "" {
			summaries = append(summaries, "platform-posture: "+posture.Summary)
		}
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

// PlatformSecurityPosture is the durable structured result of the
// platform-security investigation preset. Fields that cannot be observed are
// recorded as "unavailable" rather than omitted.
type PlatformSecurityPosture struct {
	Fields  map[string]string
	Summary string
}

// collectPlatformSecurityPosture merges command outputs into named posture
// fields. Pure parsing so tests can feed fixture outputs without live OS state.
func collectPlatformSecurityPosture(commands []InvestigationCommandResult) PlatformSecurityPosture {
	fields := map[string]string{
		"secure_boot":                 "unavailable",
		"vbs":                         "unavailable",
		"hvci_memory_integrity":       "unavailable",
		"test_signing":                "unavailable",
		"driver_signature_enforcement": "unavailable",
		"tpm_ready":                   "unavailable",
	}
	for _, cmd := range commands {
		label := strings.ToLower(strings.TrimSpace(cmd.Label))
		output := cmd.Output
		if !cmd.Success && strings.TrimSpace(output) == "" {
			output = cmd.Error
		}
		parsed := parsePlatformSecurityCommandOutput(label, output)
		for key, value := range parsed {
			if strings.TrimSpace(value) == "" {
				continue
			}
			fields[key] = value
		}
	}
	parts := []string{}
	for _, key := range []string{"secure_boot", "vbs", "hvci_memory_integrity", "test_signing", "driver_signature_enforcement", "tpm_ready"} {
		parts = append(parts, key+"="+fields[key])
	}
	return PlatformSecurityPosture{
		Fields:  fields,
		Summary: strings.Join(parts, "; "),
	}
}

// parsePlatformSecurityCommandOutput extracts posture fields from a single
// collector label + output blob. Returns only keys it could interpret.
func parsePlatformSecurityCommandOutput(label, output string) map[string]string {
	out := map[string]string{}
	text := strings.TrimSpace(output)
	if text == "" {
		return out
	}
	lower := strings.ToLower(text)
	switch label {
	case "secure-boot":
		switch {
		case strings.Contains(lower, "secureboot=true") || strings.Contains(lower, "true"):
			if strings.Contains(lower, "unavailable") {
				out["secure_boot"] = "unavailable"
			} else if strings.Contains(lower, "false") {
				out["secure_boot"] = "disabled"
			} else {
				out["secure_boot"] = "enabled"
			}
		case strings.Contains(lower, "secureboot=false") || (strings.Contains(lower, "false") && !strings.Contains(lower, "unavailable")):
			out["secure_boot"] = "disabled"
		default:
			out["secure_boot"] = "unavailable"
		}
	case "device-guard":
		// VirtualizationBasedSecurityStatus: 0=off, 1=enabled not running, 2=running
		if strings.Contains(lower, "virtualizationbasedsecuritystatus") {
			switch {
			case strings.Contains(lower, "virtualizationbasedsecuritystatus : 2") || strings.Contains(lower, "virtualizationbasedsecuritystatus: 2"):
				out["vbs"] = "running"
			case strings.Contains(lower, "virtualizationbasedsecuritystatus : 1") || strings.Contains(lower, "virtualizationbasedsecuritystatus: 1"):
				out["vbs"] = "enabled"
			case strings.Contains(lower, "virtualizationbasedsecuritystatus : 0") || strings.Contains(lower, "virtualizationbasedsecuritystatus: 0"):
				out["vbs"] = "disabled"
			default:
				out["vbs"] = "observed"
			}
		} else if strings.Contains(lower, "unavailable") {
			out["vbs"] = "unavailable"
		}
		// SecurityServicesRunning bit 2 (value often listed as 2) = HVCI / Memory Integrity
		if strings.Contains(lower, "securityservicesrunning") {
			// Common Format-List values include "2" or "{2}" or "HVCI".
			if strings.Contains(lower, "securityservicesrunning : 2") ||
				strings.Contains(lower, "securityservicesrunning: 2") ||
				strings.Contains(lower, "{2}") ||
				strings.Contains(lower, "hypervisor enforced code integrity") ||
				strings.Contains(lower, "hvci") {
				out["hvci_memory_integrity"] = "enabled"
			} else if strings.Contains(lower, "securityservicesrunning : 0") || strings.Contains(lower, "securityservicesrunning: 0") || strings.Contains(lower, "{}") {
				out["hvci_memory_integrity"] = "disabled"
			} else {
				out["hvci_memory_integrity"] = "observed"
			}
		}
		if strings.Contains(lower, "codeintegritypolicyenforcementstatus") {
			if strings.Contains(lower, "codeintegritypolicyenforcementstatus : 2") || strings.Contains(lower, "codeintegritypolicyenforcementstatus: 2") {
				out["driver_signature_enforcement"] = "enforced"
			} else if strings.Contains(lower, "codeintegritypolicyenforcementstatus : 0") || strings.Contains(lower, "codeintegritypolicyenforcementstatus: 0") {
				out["driver_signature_enforcement"] = "disabled"
			} else {
				out["driver_signature_enforcement"] = "observed"
			}
		}
	case "test-signing":
		// bcdedit output uses "testsigning             Yes/No"
		if strings.Contains(lower, "testsigning") {
			if strings.Contains(lower, "testsigning") && (strings.Contains(lower, "yes") || strings.Contains(lower, "on")) {
				// Prefer the line that mentions testsigning.
				for _, line := range strings.Split(lower, "\n") {
					if strings.Contains(line, "testsigning") {
						if strings.Contains(line, "yes") || strings.Contains(line, " on") {
							out["test_signing"] = "enabled"
						} else if strings.Contains(line, "no") || strings.Contains(line, " off") {
							out["test_signing"] = "disabled"
						}
						break
					}
				}
				if _, ok := out["test_signing"]; !ok {
					out["test_signing"] = "observed"
				}
			} else {
				out["test_signing"] = "disabled"
			}
		} else if strings.Contains(lower, "unavailable") || strings.Contains(lower, "access is denied") {
			out["test_signing"] = "unavailable"
		}
	case "tpm":
		if strings.Contains(lower, "tpmpresent") || strings.Contains(lower, "tpmready") {
			present := strings.Contains(lower, "tpmpresent") && (strings.Contains(lower, "tpmpresent                  : true") || strings.Contains(lower, "tpmpresent : true") || strings.Contains(lower, "tpmpresent: true"))
			ready := strings.Contains(lower, "tpmready") && (strings.Contains(lower, "tpmready                    : true") || strings.Contains(lower, "tpmready : true") || strings.Contains(lower, "tpmready: true"))
			// Looser parse for Format-List with variable spacing.
			if regexpContainsTPMTrue(lower, "tpmpresent") {
				present = true
			}
			if regexpContainsTPMTrue(lower, "tpmready") {
				ready = true
			}
			switch {
			case present && ready:
				out["tpm_ready"] = "ready"
			case present:
				out["tpm_ready"] = "present"
			case strings.Contains(lower, "tpmpresent") && regexpContainsTPMFalse(lower, "tpmpresent"):
				out["tpm_ready"] = "absent"
			default:
				out["tpm_ready"] = "observed"
			}
		} else if strings.Contains(lower, "unavailable") {
			out["tpm_ready"] = "unavailable"
		}
	case "driver-signature-policy":
		if strings.Contains(lower, "unavailable") {
			if out["driver_signature_enforcement"] == "" {
				out["driver_signature_enforcement"] = "unavailable"
			}
		} else if strings.Contains(lower, "codeintegritypolicy=") {
			out["driver_signature_enforcement"] = "observed"
		}
	}
	return out
}

func regexpContainsTPMTrue(lower, key string) bool {
	// "tpmpresent : true" with flexible whitespace
	idx := strings.Index(lower, key)
	if idx < 0 {
		return false
	}
	window := lower[idx:]
	if len(window) > 48 {
		window = window[:48]
	}
	return strings.Contains(window, "true")
}

func regexpContainsTPMFalse(lower, key string) bool {
	idx := strings.Index(lower, key)
	if idx < 0 {
		return false
	}
	window := lower[idx:]
	if len(window) > 48 {
		window = window[:48]
	}
	return strings.Contains(window, "false")
}

func platformSecurityPostureFindings(posture PlatformSecurityPosture) []InvestigationFinding {
	if posture.Fields == nil {
		return nil
	}
	findings := []InvestigationFinding{}
	add := func(field, subject string, risk int, severity, message string) {
		value := strings.TrimSpace(posture.Fields[field])
		if value == "" {
			value = "unavailable"
		}
		outcome := platformSecurityFieldOutcome(field, value)
		findings = append(findings, InvestigationFinding{
			Kind:        "platform_security_" + field,
			Category:    "platform_security",
			Subject:     subject + "=" + value,
			Outcome:     outcome,
			Severity:    severity,
			SignalClass: "platform_security",
			RiskScore:   risk,
			Message:     message + " (" + value + ")",
			Attributes:  map[string]string{"field": field, "value": value},
		})
	}
	add("secure_boot", "secure_boot", 40, "medium", "Secure Boot posture")
	add("vbs", "vbs", 45, "medium", "Virtualization-based security posture")
	add("hvci_memory_integrity", "hvci_memory_integrity", 50, "medium", "HVCI / Memory Integrity posture")
	add("test_signing", "test_signing", 55, "high", "Test signing posture")
	add("driver_signature_enforcement", "driver_signature_enforcement", 45, "medium", "Driver signature enforcement posture")
	add("tpm_ready", "tpm_ready", 40, "medium", "TPM readiness posture")
	return findings
}

// platformSecurityFieldOutcome maps a posture field value to investigation outcome.
// test_signing enabled is a lab risk (failed); disabled is expected for production (passed).
func platformSecurityFieldOutcome(field, value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	field = strings.ToLower(strings.TrimSpace(field))
	if value == "" || value == "unavailable" {
		return "unknown"
	}
	if field == "test_signing" {
		if value == "enabled" {
			return "failed"
		}
		return "passed"
	}
	switch value {
	case "disabled", "absent":
		return "failed"
	case "enabled", "running", "ready", "present", "enforced", "observed":
		return "passed"
	default:
		return "unknown"
	}
}

func mergeStringMaps(dst map[string]string, src map[string]string) map[string]string {
	if dst == nil {
		dst = map[string]string{}
	}
	for k, v := range src {
		if strings.TrimSpace(k) == "" {
			continue
		}
		dst[k] = v
	}
	return dst
}

func investigationPresetCommands(preset, target string) []investigationCommandSpec {
	switch normalizeInvestigationPreset(preset) {
	case "driver-visibility":
		return []investigationCommandSpec{
			{Label: "driver services", Name: "sc", Args: []string{"query", "type=", "driver"}, Command: "sc query type= driver"},
			{Label: "driverquery", Name: "driverquery", Args: []string{"/v"}, Command: "driverquery /v"},
			{Label: "verifier", Name: "verifier", Args: []string{"/querysettings"}, Command: "verifier /querysettings"},
			{Label: "fltmc", Name: "fltmc", Args: nil, Command: "fltmc"},
		}
	case "process-visibility":
		return []investigationCommandSpec{
			{Label: "tasklist", Name: "tasklist", Args: []string{"/v"}, Command: "tasklist /v"},
			{Label: "services", Name: "sc", Args: []string{"query"}, Command: "sc query"},
			{Label: "powershell-process", Name: "powershell", Args: []string{"-NoProfile", "-Command", "Get-Process | Select-Object -First 120 Name,Id,Path | Format-Table -AutoSize | Out-String -Width 220"}, Command: "powershell Get-Process"},
		}
	case "provider-visibility":
		return []investigationCommandSpec{
			{Label: "logman providers", Name: "logman", Args: []string{"query", "providers"}, Command: "logman query providers"},
			{Label: "wevtutil logs", Name: "wevtutil", Args: []string{"el"}, Command: "wevtutil el"},
		}
	case "platform-security":
		// Best-effort user-mode-visible posture probes. Individual commands may
		// be unavailable without elevation; parse helpers still record explicit
		// unavailable fields so the snapshot structure is durable.
		return []investigationCommandSpec{
			{
				Label:   "secure-boot",
				Name:    "powershell",
				Args:    []string{"-NoProfile", "-Command", "try { $v = Confirm-SecureBootUEFI; \"SecureBoot=$v\" } catch { \"SecureBoot=unavailable; error=$($_.Exception.Message)\" }"},
				Command: "powershell Confirm-SecureBootUEFI",
			},
			{
				Label:   "device-guard",
				Name:    "powershell",
				Args:    []string{"-NoProfile", "-Command", "try { Get-CimInstance -ClassName Win32_DeviceGuard -Namespace root\\Microsoft\\Windows\\DeviceGuard | Select-Object VirtualizationBasedSecurityStatus,SecurityServicesRunning,SecurityServicesConfigured,CodeIntegrityPolicyEnforcementStatus | Format-List | Out-String -Width 220 } catch { \"DeviceGuard=unavailable; error=$($_.Exception.Message)\" }"},
				Command: "powershell Win32_DeviceGuard",
			},
			{
				Label:   "test-signing",
				Name:    "bcdedit",
				Args:    []string{"/enum", "{current}"},
				Command: "bcdedit /enum {current}",
			},
			{
				Label:   "tpm",
				Name:    "powershell",
				Args:    []string{"-NoProfile", "-Command", "try { Get-Tpm | Select-Object TpmPresent,TpmReady,TpmEnabled,TpmActivated,ManagedAuthLevel | Format-List | Out-String -Width 220 } catch { \"TPM=unavailable; error=$($_.Exception.Message)\" }"},
				Command: "powershell Get-Tpm",
			},
			{
				Label:   "driver-signature-policy",
				Name:    "powershell",
				Args:    []string{"-NoProfile", "-Command", "try { $ci = Get-ItemProperty -Path 'HKLM:\\SYSTEM\\CurrentControlSet\\Control\\CI\\Policy' -ErrorAction SilentlyContinue; $ci2 = Get-ItemProperty -Path 'HKLM:\\SYSTEM\\CurrentControlSet\\Control\\CI' -ErrorAction SilentlyContinue; \"CodeIntegrityPolicy=$($ci.VerifiedAndReputablePolicyState); CI=$($ci2.VulnerableDriverBlocklistEnable)\" } catch { \"DriverSignaturePolicy=unavailable\" }"},
				Command: "powershell CI policy keys",
			},
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
	switch normalizeInvestigationPreset(preset) {
	case "driver-visibility":
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
	case "process-visibility":
		if targetLower != "" && (result.Label == "tasklist" || result.Label == "powershell-process") {
			base := strings.ToLower(strings.TrimSuffix(filepath.Base(targetLower), filepath.Ext(targetLower)))
			if base != "" && !strings.Contains(output, base) {
				findings = append(findings, InvestigationFinding{Kind: "process_presence", Category: "telemetry", Subject: "target process missing", Outcome: "failed", Severity: "high", SignalClass: "process_state", RiskScore: 68, Message: "Target process was not observed in process listings.", Attributes: map[string]string{"target": target}})
			}
		}
	case "provider-visibility":
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
	switch normalizeInvestigationPreset(preset) {
	case "driver-visibility":
		return "driver"
	case "provider-visibility":
		return "telemetry"
	case "platform-security":
		return "platform_security"
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
	if strings.EqualFold(normalizeInvestigationPreset(preset), "driver-visibility") {
		base := strings.TrimSuffix(target, filepath.Ext(target))
		candidates = append(candidates, base+".sys", base+".inf", base+".cat")
	}
	if strings.EqualFold(normalizeInvestigationPreset(preset), "provider-visibility") {
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
	preset = normalizeInvestigationPreset(preset)
	if strings.TrimSpace(target) == "" {
		return nil
	}
	if len(artifacts) > 0 {
		return nil
	}
	category := investigationCategoryForPreset(preset)
	signal := "artifact"
	if preset == "driver-visibility" {
		signal = "driver_artifact"
	}
	if preset == "provider-visibility" {
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
	preset = normalizeInvestigationPreset(preset)
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
