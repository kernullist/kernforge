package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type SecurityVerificationCategory string

const (
	SecurityCategoryDriver     SecurityVerificationCategory = "driver"
	SecurityCategoryTelemetry  SecurityVerificationCategory = "telemetry"
	SecurityCategoryUnreal     SecurityVerificationCategory = "unreal"
	SecurityCategoryMemoryScan SecurityVerificationCategory = "memory-scan"
)

func classifySecurityVerificationCategories(changed []string) []SecurityVerificationCategory {
	set := map[SecurityVerificationCategory]bool{}
	for _, raw := range changed {
		path := strings.ToLower(filepath.ToSlash(strings.TrimSpace(raw)))
		base := filepath.Base(path)
		switch {
		case isDriverRelatedPath(path, base):
			set[SecurityCategoryDriver] = true
		}
		switch {
		case isTelemetryRelatedPath(path, base):
			set[SecurityCategoryTelemetry] = true
		}
		switch {
		case isUnrealRelatedPath(path, base):
			set[SecurityCategoryUnreal] = true
		}
		switch {
		case isMemoryScanRelatedPath(path, base):
			set[SecurityCategoryMemoryScan] = true
		}
	}
	order := []SecurityVerificationCategory{
		SecurityCategoryDriver,
		SecurityCategoryTelemetry,
		SecurityCategoryUnreal,
		SecurityCategoryMemoryScan,
	}
	var out []SecurityVerificationCategory
	for _, item := range order {
		if set[item] {
			out = append(out, item)
		}
	}
	return out
}

func buildSecurityVerificationSteps(root string, changed []string, mode VerificationMode) ([]VerificationStep, string) {
	_ = mode
	categories := classifySecurityVerificationCategories(changed)
	if len(categories) == 0 {
		return nil, ""
	}
	var steps []VerificationStep
	var names []string
	for _, category := range categories {
		names = append(names, string(category))
		switch category {
		case SecurityCategoryDriver:
			steps = append(steps, buildDriverSecurityVerificationSteps(root, changed)...)
		case SecurityCategoryTelemetry:
			steps = append(steps, buildTelemetrySecurityVerificationSteps(changed)...)
		case SecurityCategoryUnreal:
			steps = append(steps, buildUnrealSecurityVerificationSteps(changed)...)
		case SecurityCategoryMemoryScan:
			steps = append(steps, buildMemoryScanSecurityVerificationSteps(changed)...)
		}
	}
	steps = uniqueVerificationSteps(steps)
	if len(steps) == 0 {
		return nil, ""
	}
	return steps, "Security-aware verification detected categories: " + strings.Join(names, ", ") + "."
}

func buildDriverSecurityVerificationSteps(root string, changed []string) []VerificationStep {
	var steps []VerificationStep
	artifacts := collectDriverVerificationArtifacts(root, changed)
	infFiles := collectDriverInfFiles(root, changed)
	if len(artifacts) > 0 && commandExists("signtool") {
		for _, artifact := range artifacts {
			quoted := quoteVerificationCommandArg(artifact)
			steps = append(steps, VerificationStep{
				Label:   "signtool verify " + artifact,
				Command: "signtool verify /pa " + quoted,
				Scope:   artifact,
				Stage:   "targeted",
				Tags:    []string{"driver", "signing", "security"},
				Status:  VerificationPending,
			})
		}
	}
	if len(artifacts) > 0 && commandExists("dumpbin") {
		for _, artifact := range artifacts {
			if !strings.HasSuffix(strings.ToLower(filepath.ToSlash(artifact)), ".sys") {
				continue
			}
			quoted := quoteVerificationCommandArg(artifact)
			steps = append(steps, VerificationStep{
				Label:   "dumpbin /headers " + artifact,
				Command: "dumpbin /headers " + quoted,
				Scope:   artifact,
				Stage:   "targeted",
				Tags:    []string{"driver", "binary", "security"},
				Status:  VerificationPending,
			})
		}
	}
	if len(infFiles) > 0 && commandExists("inf2cat") {
		for _, inf := range infFiles {
			driverDir := filepath.ToSlash(filepath.Dir(inf))
			quotedDir := quoteVerificationCommandArg(driverDir)
			steps = append(steps, VerificationStep{
				Label:   "inf2cat " + driverDir,
				Command: "inf2cat /driver:" + quotedDir + " /os:10_X64",
				Scope:   inf,
				Stage:   "targeted",
				Tags:    []string{"driver", "package", "security"},
				Status:  VerificationPending,
			})
		}
	}
	if len(artifacts) > 0 && commandExists("symchk") {
		for _, artifact := range artifacts {
			quoted := quoteVerificationCommandArg(artifact)
			steps = append(steps, VerificationStep{
				Label:   "symchk " + artifact,
				Command: "symchk /v " + quoted,
				Scope:   artifact,
				Stage:   "targeted",
				Tags:    []string{"driver", "symbols", "security"},
				Status:  VerificationPending,
			})
		}
	}
	if commandExists("verifier") {
		steps = append(steps, VerificationStep{
			Label:   "verifier /querysettings",
			Command: "verifier /querysettings",
			Scope:   "workspace",
			Stage:   "targeted",
			Tags:    []string{"driver", "verifier", "security"},
			Status:  VerificationPending,
		})
	}
	if len(artifacts) == 0 {
		steps = append(steps, VerificationStep{
			Label:   "driver artifact discovery review",
			Command: `echo Driver-related source changes detected, but no built .sys/.cat artifacts were found in changed files or nearby workspace paths.`,
			Scope:   "targeted",
			Stage:   "targeted",
			Tags:    []string{"driver", "artifact", "security"},
			Status:  VerificationPending,
		})
	}
	steps = append(steps, VerificationStep{
		Label:   "driver readiness review",
		Command: `echo Driver-related changes detected. Review signing, symbols, verifier settings, and deployment readiness.`,
		Scope:   "targeted",
		Stage:   "targeted",
		Tags:    []string{"driver", "security"},
		Status:  VerificationPending,
	})
	return steps
}

func buildTelemetrySecurityVerificationSteps(changed []string) []VerificationStep {
	scope := strings.Join(changed, ",")
	if scope == "" {
		scope = "targeted"
	}
	var steps []VerificationStep
	for _, manifest := range collectTelemetryManifestFiles(changed) {
		if ps := preferredPowerShellExe(); ps != "" {
			steps = append(steps, VerificationStep{
				Label:   "telemetry XML validation " + manifest,
				Command: buildPowerShellXMLValidationCommand(ps, manifest),
				Scope:   manifest,
				Stage:   "targeted",
				Tags:    []string{"telemetry", "xml", "security"},
				Status:  VerificationPending,
			})
		}
		if commandExists("logman") {
			provider := telemetryProviderGuess(manifest)
			if provider != "" {
				steps = append(steps, VerificationStep{
					Label:   "logman provider lookup " + provider,
					Command: `logman query providers | findstr /i ` + quoteVerificationCommandArg(provider),
					Scope:   manifest,
					Stage:   "targeted",
					Tags:    []string{"telemetry", "provider", "security"},
					Status:  VerificationPending,
				})
			}
		}
	}
	steps = append(steps, VerificationStep{
		Label:   "telemetry contract review",
		Command: `echo Telemetry-related changes detected. Review ETW/provider contracts, event schema compatibility, and trace collection expectations.`,
		Scope:   scope,
		Stage:   "targeted",
		Tags:    []string{"telemetry", "security"},
		Status:  VerificationPending,
	})
	return steps
}

func buildUnrealSecurityVerificationSteps(changed []string) []VerificationStep {
	scope := strings.Join(changed, ",")
	if scope == "" {
		scope = "targeted"
	}
	return []VerificationStep{
		{
			Label:   "unreal integrity review",
			Command: `echo Unreal-related changes detected. Review module boundaries, integrity checks, schema drift, and cooked asset assumptions.`,
			Scope:   scope,
			Stage:   "targeted",
			Tags:    []string{"unreal", "security"},
			Status:  VerificationPending,
		},
	}
}

func buildMemoryScanSecurityVerificationSteps(changed []string) []VerificationStep {
	scope := strings.Join(changed, ",")
	if scope == "" {
		scope = "targeted"
	}
	return []VerificationStep{
		{
			Label:   "memory scan regression review",
			Command: `echo Memory-scan-related changes detected. Review synthetic evasion coverage, false positives, and performance ceilings.`,
			Scope:   scope,
			Stage:   "targeted",
			Tags:    []string{"memory-scan", "security"},
			Status:  VerificationPending,
		},
	}
}

func collectDriverVerificationArtifacts(root string, changed []string) []string {
	out := collectChangedDriverArtifacts(root, changed)
	if len(out) == 0 {
		out = append(out, discoverNearbyDriverArtifacts(root, changed)...)
	}
	return uniqueStrings(out)
}

func collectDriverInfFiles(root string, changed []string) []string {
	var out []string
	for _, raw := range changed {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(filepath.ToSlash(trimmed))
		if filepath.Ext(lower) == ".inf" {
			out = append(out, trimmed)
		}
	}
	if len(out) > 0 {
		return uniqueStrings(out)
	}
	for _, artifact := range discoverNearbyDriverArtifacts(root, changed) {
		if strings.EqualFold(filepath.Ext(artifact), ".inf") {
			out = append(out, artifact)
		}
	}
	return uniqueStrings(out)
}

func collectChangedDriverArtifacts(root string, changed []string) []string {
	var out []string
	for _, raw := range changed {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(filepath.ToSlash(trimmed))
		switch filepath.Ext(lower) {
		case ".sys", ".cat":
			out = append(out, trimmed)
		case ".inf":
			abs := filepath.Join(root, filepath.FromSlash(trimmed))
			base := strings.TrimSuffix(abs, filepath.Ext(abs))
			for _, ext := range []string{".sys", ".cat"} {
				candidate := base + ext
				if exists(candidate) {
					rel, err := filepath.Rel(root, candidate)
					if err == nil {
						out = append(out, filepath.ToSlash(rel))
					}
				}
			}
		}
	}
	return uniqueStrings(out)
}

func discoverNearbyDriverArtifacts(root string, changed []string) []string {
	seenDirs := map[string]bool{}
	var out []string
	for _, raw := range changed {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		dir := filepath.Join(root, filepath.Dir(filepath.FromSlash(trimmed)))
		if seenDirs[dir] {
			continue
		}
		seenDirs[dir] = true
		for _, ext := range []string{"*.sys", "*.cat", "*.inf"} {
			matches, err := filepath.Glob(filepath.Join(dir, ext))
			if err != nil {
				continue
			}
			for _, match := range matches {
				rel, err := filepath.Rel(root, match)
				if err == nil {
					out = append(out, filepath.ToSlash(rel))
				}
			}
		}
	}
	return uniqueStrings(out)
}

func collectTelemetryManifestFiles(changed []string) []string {
	var out []string
	for _, raw := range changed {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(filepath.ToSlash(trimmed))
		ext := filepath.Ext(lower)
		if ext == ".man" || ext == ".xml" || ext == ".mc" {
			out = append(out, trimmed)
			continue
		}
		if strings.Contains(lower, "manifest") || strings.Contains(lower, "provider") {
			out = append(out, trimmed)
		}
	}
	return uniqueStrings(out)
}

func preferredPowerShellExe() string {
	for _, name := range []string{"pwsh", "powershell"} {
		if commandExists(name) {
			return name
		}
	}
	return ""
}

func buildPowerShellXMLValidationCommand(powerShellExe, path string) string {
	return powerShellExe + ` -NoProfile -Command "[xml](Get-Content -LiteralPath ` + quotePowerShellLiteral(path) + `) | Out-Null"`
}

func quotePowerShellLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}

func telemetryProviderGuess(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	base = strings.ReplaceAll(base, "_", "-")
	return base
}

func uniqueVerificationSteps(steps []VerificationStep) []VerificationStep {
	var out []VerificationStep
	seen := map[string]bool{}
	for _, step := range steps {
		key := strings.ToLower(strings.TrimSpace(step.Command))
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(step.Label))
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, step)
	}
	return out
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func isDriverRelatedPath(path, base string) bool {
	switch filepath.Ext(path) {
	case ".sys", ".inf", ".cat":
		return true
	}
	if strings.Contains(path, "/driver/") || strings.Contains(path, "/drivers/") || strings.HasPrefix(path, "driver/") || strings.HasPrefix(path, "drivers/") || strings.Contains(path, "kernel") {
		return true
	}
	keywords := []string{"minifilter", "wdm", "wdf", "kmode", "kmdf"}
	return containsAnyKeyword(path, base, keywords)
}

func isTelemetryRelatedPath(path, base string) bool {
	keywords := []string{"telemetry", "etw", "trace", "eventlog", "provider", "manifest", "wpr", "xperf"}
	return containsAnyKeyword(path, base, keywords)
}

func isUnrealRelatedPath(path, base string) bool {
	switch filepath.Ext(path) {
	case ".uproject", ".uplugin", ".uasset", ".umap":
		return true
	}
	keywords := []string{"unreal", "ue4", "ue5", "build.cs", "target.cs", "pak", "gameplayability"}
	return containsAnyKeyword(path, base, keywords)
}

func isMemoryScanRelatedPath(path, base string) bool {
	keywords := []string{"memoryscan", "memory_scan", "memscan", "patternscan", "signature", "aob", "scanner", "inspection"}
	return containsAnyKeyword(path, base, keywords)
}

func containsAnyKeyword(path, base string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(path, keyword) || strings.Contains(base, keyword) {
			return true
		}
	}
	return false
}

func renderSecurityVerificationSummary(changed []string) string {
	categories := classifySecurityVerificationCategories(changed)
	if len(categories) == 0 {
		return ""
	}
	var parts []string
	for _, item := range categories {
		parts = append(parts, string(item))
	}
	return fmt.Sprintf("security_categories=%s", strings.Join(parts, ","))
}
