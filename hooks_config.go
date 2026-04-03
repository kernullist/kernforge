package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func intPtr(value int) *int {
	return &value
}

func InitHooksTemplate() string {
	sample := HookFile{
		Enabled:     boolPtr(true),
		StopOnMatch: false,
		Rules: []HookRule{
			{
				ID:       "warn-driver-edit",
				Enabled:  boolPtr(true),
				Priority: 200,
				Events:   []HookEvent{HookPostEdit},
				Match: HookMatch{
					Paths: []string{"**/driver/**/*.cpp", "**/driver/**/*.h", "**/*.sys", "**/*.inf", "**/*.cat"},
				},
				Action: HookAction{
					Type:    "warn",
					Message: "Driver-related changes detected. Consider signing, symbol, and verifier checks.",
				},
			},
			{
				ID:       "extra-driver-verification",
				Enabled:  boolPtr(true),
				Priority: 180,
				Events:   []HookEvent{HookPreVerification},
				Match: HookMatch{
					ChangedFiles: []string{"**/driver/**/*.cpp", "**/driver/**/*.h", "**/*.sys", "**/*.inf", "**/*.cat"},
				},
				Action: HookAction{
					Type:    "add_verification_step",
					Label:   "driver artifact readiness",
					Command: "echo Review signing, symbol, and verifier readiness for driver-related changes.",
					Tags:    []string{"driver", "security"},
					Scope:   "workspace",
					Stage:   "workspace",
				},
			},
			{
				ID:       "extra-telemetry-verification",
				Enabled:  boolPtr(true),
				Priority: 170,
				Events:   []HookEvent{HookPreVerification},
				Match: HookMatch{
					ChangedFiles: []string{"**/*telemetry*", "**/*provider*", "**/*.man", "**/*.mc", "**/*.xml"},
				},
				Action: HookAction{
					Type:    "add_verification_step",
					Label:   "telemetry contract readiness",
					Command: "echo Review ETW/provider compatibility and telemetry schema readiness.",
					Tags:    []string{"telemetry", "security"},
					Scope:   "workspace",
					Stage:   "workspace",
				},
			},
			{
				ID:       "ask-before-telemetry-push-with-failed-evidence",
				Enabled:  boolPtr(true),
				Priority: 165,
				Events:   []HookEvent{HookPreGitPush, HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*telemetry*", "**/*provider*", "**/*.man", "**/*.mc", "**/*.xml"},
					EvidenceCategories:      []string{"telemetry"},
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "ask",
					Message: "Recent failed telemetry evidence exists in this workspace. Continue anyway?",
				},
			},
			{
				ID:       "deny-driver-pr-with-repeated-sensitive-artifact-failures",
				Enabled:  boolPtr(true),
				Priority: 164,
				Events:   []HookEvent{HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/driver/**/*.cpp", "**/driver/**/*.h", "**/*.sys", "**/*.inf", "**/*.cat"},
					EvidenceCategories:      []string{"driver"},
					EvidenceSubjects:        []string{"**/*.sys", "**/*.inf", "**/*.cat"},
					MinEvidenceMatches:      intPtr(2),
					MaxEvidenceAgeHours:     intPtr(24),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "create_checkpoint",
					Message: "Pre-PR safety checkpoint before repeated driver artifact failure review",
				},
			},
			{
				ID:       "deny-driver-pr-with-repeated-sensitive-artifact-failures",
				Enabled:  boolPtr(true),
				Priority: 162,
				Events:   []HookEvent{HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/driver/**/*.cpp", "**/driver/**/*.h", "**/*.sys", "**/*.inf", "**/*.cat"},
					EvidenceCategories:      []string{"driver"},
					EvidenceSubjects:        []string{"**/*.sys", "**/*.inf", "**/*.cat"},
					MinEvidenceMatches:      intPtr(2),
					MaxEvidenceAgeHours:     intPtr(24),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "deny",
					Message: "Repeated failed driver artifact evidence exists for .sys/.inf/.cat outputs. Resolve it before creating a PR.",
				},
			},
			{
				ID:       "warn-before-memory-scan-push-with-failed-evidence",
				Enabled:  boolPtr(true),
				Priority: 160,
				Events:   []HookEvent{HookPreGitPush, HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*memoryscan*", "**/*memory_scan*", "**/*memscan*", "**/*patternscan*", "**/*signature*", "**/*scanner*"},
					EvidenceCategories:      []string{"memory-scan"},
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "ask",
					Message: "Recent failed memory-scan evidence exists in this workspace. Continue anyway?",
				},
			},
			{
				ID:       "append-review-context-before-telemetry-verification",
				Enabled:  boolPtr(true),
				Priority: 158,
				Events:   []HookEvent{HookPreVerification},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*telemetry*", "**/*provider*", "**/*.man", "**/*.mc", "**/*.xml"},
					EvidenceCategories:      []string{"telemetry"},
					EvidenceTags:            []string{"provider", "xml"},
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "append_review_context",
					Message: "Recent telemetry provider/XML failures exist. Re-check manifest schema drift, provider naming, and ETW contract compatibility before trusting the verification result.",
				},
			},
			{
				ID:       "deny-memory-scan-push-with-repeated-failures",
				Enabled:  boolPtr(true),
				Priority: 157,
				Events:   []HookEvent{HookPreGitPush, HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*memoryscan*", "**/*memory_scan*", "**/*memscan*", "**/*patternscan*", "**/*signature*", "**/*scanner*"},
					EvidenceCategories:      []string{"memory-scan"},
					MinEvidenceMatches:      intPtr(2),
					MaxEvidenceAgeHours:     intPtr(24),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "create_checkpoint",
					Message: "Pre-push safety checkpoint before repeated memory-scan failure enforcement",
				},
			},
			{
				ID:       "deny-memory-scan-push-with-repeated-failures",
				Enabled:  boolPtr(true),
				Priority: 155,
				Events:   []HookEvent{HookPreGitPush, HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*memoryscan*", "**/*memory_scan*", "**/*memscan*", "**/*patternscan*", "**/*signature*", "**/*scanner*"},
					EvidenceCategories:      []string{"memory-scan"},
					MinEvidenceMatches:      intPtr(2),
					MaxEvidenceAgeHours:     intPtr(24),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "deny",
					Message: "Repeated failed memory-scan evidence exists in this workspace. Resolve those failures before push or PR.",
				},
			},
		},
	}
	data, err := json.MarshalIndent(sample, "", "  ")
	if err != nil {
		return "{\n  \"rules\": []\n}\n"
	}
	return string(data) + "\n"
}

func LoadHookEngine(root string, cfg Config) (*HookEngine, []string) {
	if !configHooksEnabled(cfg) {
		return nil, nil
	}
	engine := &HookEngine{Enabled: true}
	var warns []string
	for _, preset := range cfg.HookPresets {
		rules, err := builtinHookPresetRules(preset)
		if err != nil {
			warns = append(warns, err.Error())
			continue
		}
		engine.Rules = append(engine.Rules, rules...)
	}
	for _, path := range hookConfigSearchPaths(root) {
		file, err := loadHookFile(path)
		if err != nil {
			warns = append(warns, err.Error())
			continue
		}
		if file == nil {
			continue
		}
		if file.Enabled != nil {
			engine.Enabled = *file.Enabled
		}
		if file.StopOnMatch {
			engine.StopOnMatch = true
		}
		engine.Rules = append(engine.Rules, file.Rules...)
	}
	if len(engine.Rules) == 0 {
		return nil, warns
	}
	return engine, warns
}

func hookConfigSearchPaths(root string) []string {
	return []string{
		filepath.Join(userConfigDir(), "hooks.json"),
		filepath.Join(root, userConfigDirName, "hooks.json"),
	}
}

func loadHookFile(path string) (*HookFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("load hooks %s: %w", path, err)
	}
	var file HookFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse hooks %s: %w", path, err)
	}
	for i := range file.Rules {
		if strings.TrimSpace(file.Rules[i].ID) == "" {
			file.Rules[i].ID = fmt.Sprintf("rule-%d", i+1)
		}
	}
	return &file, nil
}

func builtinHookPresetRules(name string) ([]HookRule, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "":
		return nil, nil
	case "windows-security":
		return []HookRule{
			{
				ID:       "ask-before-boot-config-change",
				Priority: 300,
				Events:   []HookEvent{HookPreToolUse},
				Match: HookMatch{
					ToolNames:     []string{"run_shell"},
					CommandsRegex: []string{`(?i)\bbcdedit\b`, `(?i)\bverifier\b`, `(?i)\bsc\s+stop\b`, `(?i)\bfltmc\s+unload\b`},
				},
				Action: HookAction{
					Type:    "ask",
					Message: "This command changes Windows boot, verifier, or driver state. Continue?",
				},
			},
			{
				ID:       "warn-driver-edit",
				Priority: 180,
				Events:   []HookEvent{HookPostEdit},
				Match: HookMatch{
					Paths: []string{"**/*.sys", "**/*.inf", "**/*.cat", "**/driver/**/*.c", "**/driver/**/*.cc", "**/driver/**/*.cpp", "**/driver/**/*.h", "**/driver/**/*.hpp"},
				},
				Action: HookAction{
					Type:    "warn",
					Message: "Driver-related changes detected. Consider symbol, signing, and verifier checks.",
				},
			},
			{
				ID:       "warn-before-driver-pr",
				Priority: 160,
				Events:   []HookEvent{HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles: []string{"**/*.sys", "**/*.inf", "**/*.cat", "**/driver/**/*.c", "**/driver/**/*.cc", "**/driver/**/*.cpp", "**/driver/**/*.h", "**/driver/**/*.hpp"},
				},
				Action: HookAction{
					Type:    "warn",
					Message: "Driver-related changes are included in this PR. Verify signing and symbol readiness before review.",
				},
			},
			{
				ID:       "deny-driver-pr-with-critical-signing-or-symbol-evidence",
				Priority: 260,
				Events:   []HookEvent{HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*.sys", "**/*.inf", "**/*.cat", "**/driver/**/*.c", "**/driver/**/*.cc", "**/driver/**/*.cpp", "**/driver/**/*.h", "**/driver/**/*.hpp"},
					EvidenceCategories:      []string{"driver"},
					EvidenceSeverities:      []string{"critical"},
					SignalClasses:           []string{"signing", "symbols"},
					MinEvidenceRiskScore:    intPtr(80),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "deny",
					Message: "Recent critical driver signing or symbol evidence exists in this workspace. Resolve it before creating a PR.",
				},
			},
			{
				ID:       "deny-driver-pr-with-repeated-sensitive-artifact-failures",
				Priority: 247,
				Events:   []HookEvent{HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*.sys", "**/*.inf", "**/*.cat", "**/driver/**/*.c", "**/driver/**/*.cc", "**/driver/**/*.cpp", "**/driver/**/*.h", "**/driver/**/*.hpp"},
					EvidenceCategories:      []string{"driver"},
					EvidenceSubjects:        []string{"**/*.sys", "**/*.inf", "**/*.cat"},
					MinEvidenceMatches:      intPtr(2),
					MaxEvidenceAgeHours:     intPtr(24),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "create_checkpoint",
					Message: "Pre-PR safety checkpoint before repeated driver artifact failure review",
				},
			},
			{
				ID:       "deny-driver-pr-with-repeated-sensitive-artifact-failures",
				Priority: 245,
				Events:   []HookEvent{HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*.sys", "**/*.inf", "**/*.cat", "**/driver/**/*.c", "**/driver/**/*.cc", "**/driver/**/*.cpp", "**/driver/**/*.h", "**/driver/**/*.hpp"},
					EvidenceCategories:      []string{"driver"},
					EvidenceSubjects:        []string{"**/*.sys", "**/*.inf", "**/*.cat"},
					MinEvidenceMatches:      intPtr(2),
					MaxEvidenceAgeHours:     intPtr(24),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "deny",
					Message: "Repeated failed driver artifact evidence exists for .sys/.inf/.cat outputs. Resolve it before creating a PR.",
				},
			},
			{
				ID:       "ask-before-driver-push-with-failed-evidence",
				Priority: 220,
				Events:   []HookEvent{HookPreGitPush, HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*.sys", "**/*.inf", "**/*.cat", "**/driver/**/*.c", "**/driver/**/*.cc", "**/driver/**/*.cpp", "**/driver/**/*.h", "**/driver/**/*.hpp"},
					EvidenceCategories:      []string{"driver"},
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "ask",
					Message: "Recent failed driver evidence exists in this workspace. Continue anyway?",
				},
			},
			{
				ID:       "ask-before-telemetry-push-with-failed-evidence",
				Priority: 205,
				Events:   []HookEvent{HookPreGitPush, HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*telemetry*", "**/*provider*", "**/*.man", "**/*.mc", "**/*.xml"},
					EvidenceCategories:      []string{"telemetry"},
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "ask",
					Message: "Recent failed telemetry evidence exists in this workspace. Continue anyway?",
				},
			},
			{
				ID:       "ask-before-telemetry-pr-with-provider-failure-evidence",
				Priority: 210,
				Events:   []HookEvent{HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*telemetry*", "**/*provider*", "**/*.man", "**/*.mc", "**/*.xml"},
					EvidenceCategories:      []string{"telemetry"},
					EvidenceSeverities:      []string{"high", "critical"},
					SignalClasses:           []string{"provider", "xml"},
					MinEvidenceRiskScore:    intPtr(60),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "ask",
					Message: "Recent high-risk telemetry provider or XML evidence exists in this workspace. Continue anyway?",
				},
			},
			{
				ID:       "append-review-context-before-telemetry-verification",
				Priority: 208,
				Events:   []HookEvent{HookPreVerification},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*telemetry*", "**/*provider*", "**/*.man", "**/*.mc", "**/*.xml"},
					EvidenceCategories:      []string{"telemetry"},
					EvidenceSeverities:      []string{"high", "critical"},
					SignalClasses:           []string{"provider", "xml"},
					MinEvidenceRiskScore:    intPtr(60),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "append_review_context",
					Message: "Recent telemetry provider/XML failures exist. Re-check manifest schema drift, provider naming, and ETW contract compatibility before trusting the verification result.",
				},
			},
			{
				ID:       "ask-before-memory-scan-push-with-failed-evidence",
				Priority: 200,
				Events:   []HookEvent{HookPreGitPush, HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*memoryscan*", "**/*memory_scan*", "**/*memscan*", "**/*patternscan*", "**/*signature*", "**/*scanner*"},
					EvidenceCategories:      []string{"memory-scan"},
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "ask",
					Message: "Recent failed memory-scan evidence exists in this workspace. Continue anyway?",
				},
			},
			{
				ID:       "deny-memory-scan-push-with-repeated-failures",
				Priority: 217,
				Events:   []HookEvent{HookPreGitPush, HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*memoryscan*", "**/*memory_scan*", "**/*memscan*", "**/*patternscan*", "**/*signature*", "**/*scanner*"},
					EvidenceCategories:      []string{"memory-scan"},
					EvidenceSeverities:      []string{"high", "critical"},
					MinEvidenceRiskScore:    intPtr(70),
					MinEvidenceMatches:      intPtr(2),
					MaxEvidenceAgeHours:     intPtr(24),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "create_checkpoint",
					Message: "Pre-push safety checkpoint before repeated memory-scan failure enforcement",
				},
			},
			{
				ID:       "deny-memory-scan-push-with-repeated-failures",
				Priority: 215,
				Events:   []HookEvent{HookPreGitPush, HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*memoryscan*", "**/*memory_scan*", "**/*memscan*", "**/*patternscan*", "**/*signature*", "**/*scanner*"},
					EvidenceCategories:      []string{"memory-scan"},
					EvidenceSeverities:      []string{"high", "critical"},
					MinEvidenceRiskScore:    intPtr(70),
					MinEvidenceMatches:      intPtr(2),
					MaxEvidenceAgeHours:     intPtr(24),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "deny",
					Message: "Repeated failed memory-scan evidence exists in this workspace. Resolve those failures before push or PR.",
				},
			},
			{
				ID:       "warn-before-unreal-pr-with-failed-evidence",
				Priority: 150,
				Events:   []HookEvent{HookPreGitPush, HookPreCreatePR},
				Match: HookMatch{
					ChangedFiles:            []string{"**/*.uproject", "**/*.uplugin", "**/*.uasset", "**/*.umap", "**/*build.cs", "**/*target.cs", "**/*unreal*", "**/*ue4*", "**/*ue5*"},
					EvidenceCategories:      []string{"unreal"},
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "warn",
					Message: "Recent failed Unreal-related evidence exists in this workspace. Review integrity and schema assumptions before shipping these changes.",
				},
			},
			{
				ID:       "warn-before-push-with-high-risk-simulation-tamper-finding",
				Priority: 149,
				Events:   []HookEvent{HookPreGitPush, HookPreCreatePR},
				Match: HookMatch{
					EvidenceTags:            []string{"simulation", "tamper-surface"},
					EvidenceSeverities:      []string{"high", "critical"},
					SignalClasses:           []string{"tamper"},
					MinEvidenceRiskScore:    intPtr(60),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "warn",
					Message: "Recent tamper-surface simulation findings suggest unresolved high-risk tamper risk.",
				},
			},
			{
				ID:       "warn-before-push-with-high-risk-simulation-stealth-finding",
				Priority: 148,
				Events:   []HookEvent{HookPreGitPush, HookPreCreatePR},
				Match: HookMatch{
					EvidenceTags:            []string{"simulation", "stealth-surface"},
					EvidenceSeverities:      []string{"high", "critical"},
					SignalClasses:           []string{"stealth"},
					MinEvidenceRiskScore:    intPtr(55),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "warn",
					Message: "Recent stealth-surface simulation findings indicate a visibility gap around the target.",
				},
			},
			{
				ID:       "warn-before-pr-with-forensic-blind-spot-simulation",
				Priority: 147,
				Events:   []HookEvent{HookPreCreatePR},
				Match: HookMatch{
					EvidenceTags:            []string{"simulation", "forensic-blind-spot"},
					EvidenceSeverities:      []string{"high", "critical"},
					SignalClasses:           []string{"forensics"},
					MinEvidenceRiskScore:    intPtr(50),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "warn",
					Message: "Recent forensic blind spot simulation findings suggest weak incident reconstruction coverage before this PR.",
				},
			},
			{
				ID:       "append-review-context-before-verification-with-simulation-tamper-finding",
				Priority: 146,
				Events:   []HookEvent{HookPreVerification},
				Match: HookMatch{
					EvidenceTags:            []string{"simulation", "tamper-surface"},
					EvidenceSeverities:      []string{"high", "critical"},
					SignalClasses:           []string{"tamper"},
					MinEvidenceRiskScore:    intPtr(60),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "append_review_context",
					Message: "Recent tamper-surface findings suggest integrity, signing, or registration risk. Re-check replacement surfaces and tamper assumptions before trusting verification results.",
				},
			},
			{
				ID:       "append-context-before-review-with-simulation-stealth-finding",
				Priority: 145,
				Events:   []HookEvent{HookUserPromptSubmit},
				Match: HookMatch{
					ContainsText:            []string{"review"},
					EvidenceTags:            []string{"simulation", "stealth-surface"},
					EvidenceSeverities:      []string{"high", "critical"},
					SignalClasses:           []string{"stealth"},
					MinEvidenceRiskScore:    intPtr(55),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "append_context",
					Message: "Recent stealth-surface findings indicate a visibility gap around the target. Review with emphasis on observer coverage, missing runtime visibility, and single-signal blind spots.",
				},
			},
			{
				ID:       "append-context-before-review-with-forensic-blind-spot-simulation",
				Priority: 144,
				Events:   []HookEvent{HookUserPromptSubmit},
				Match: HookMatch{
					ContainsText:            []string{"review"},
					EvidenceTags:            []string{"simulation", "forensic-blind-spot"},
					EvidenceSeverities:      []string{"high", "critical"},
					SignalClasses:           []string{"forensics"},
					MinEvidenceRiskScore:    intPtr(50),
					HasRecentFailedEvidence: boolPtr(true),
				},
				Action: HookAction{
					Type:    "append_context",
					Message: "Recent forensic blind spot findings suggest weak incident reconstruction coverage. Review with emphasis on missing snapshots, weak audit trails, and low-artifact failure paths.",
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unknown hook preset: %s", name)
	}
}
