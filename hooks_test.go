package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHookEngineMatchesGlobPatterns(t *testing.T) {
	engine := &HookEngine{
		Enabled: true,
		Rules: []HookRule{
			{
				ID:     "warn-driver-edit",
				Events: []HookEvent{HookPostEdit},
				Match: HookMatch{
					Paths: []string{"**/driver/**/*.cpp", "**/*.sys"},
				},
				Action: HookAction{
					Type:    "warn",
					Message: "driver file changed",
				},
			},
		},
	}

	verdict, err := engine.Evaluate(context.Background(), HookPostEdit, HookPayload{
		"path": "src/driver/guard.cpp",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(verdict.Warns) != 1 {
		t.Fatalf("expected one warning, got %#v", verdict.Warns)
	}
}

func TestHookEngineDenyWins(t *testing.T) {
	engine := &HookEngine{
		Enabled: true,
		Rules: []HookRule{
			{
				ID:       "warn",
				Priority: 100,
				Events:   []HookEvent{HookPreToolUse},
				Match: HookMatch{
					ToolNames: []string{"run_shell"},
				},
				Action: HookAction{Type: "warn", Message: "be careful"},
			},
			{
				ID:       "deny",
				Priority: 200,
				Events:   []HookEvent{HookPreToolUse},
				Match: HookMatch{
					ToolNames:     []string{"run_shell"},
					CommandsRegex: []string{`(?i)\bbcdedit\b`},
				},
				Action: HookAction{Type: "deny", Message: "blocked"},
			},
		},
	}

	verdict, err := engine.Evaluate(context.Background(), HookPreToolUse, HookPayload{
		"tool_name": "run_shell",
		"command":   "bcdedit /set testsigning on",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if verdict.DenyReason != "blocked" {
		t.Fatalf("unexpected deny reason: %#v", verdict)
	}
}

func TestBuiltinWindowsSecurityPreset(t *testing.T) {
	rules, err := builtinHookPresetRules("windows-security")
	if err != nil {
		t.Fatalf("builtinHookPresetRules: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("expected preset rules")
	}

	engine := &HookEngine{
		Enabled: true,
		Rules:   rules,
	}
	verdict, err := engine.Evaluate(context.Background(), HookPreToolUse, HookPayload{
		"tool_name": "run_shell",
		"command":   "verifier /reset",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !strings.Contains(strings.ToLower(verdict.AskMessage), "continue") {
		t.Fatalf("expected ask message, got %#v", verdict)
	}
}

func TestHookRuntimeAskDenied(t *testing.T) {
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "ask",
					Events: []HookEvent{HookPreToolUse},
					Match: HookMatch{
						ToolNames: []string{"run_shell"},
					},
					Action: HookAction{Type: "ask", Message: "Continue?"},
				},
			},
		},
		Ask: func(string) (bool, error) {
			return false, nil
		},
	}

	_, err := runtime.Run(context.Background(), HookPreToolUse, HookPayload{
		"tool_name": "run_shell",
	})
	if err == nil {
		t.Fatal("expected hook denial")
	}
}

func TestHookRuntimeCreatesCheckpointBeforeDeny(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		ID:        "ev-1",
		Workspace: root,
		Kind:      "verification_failure",
		Category:  "driver",
		Subject:   "runtime_error",
		Outcome:   "failed",
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	var created []string
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:       "checkpoint-first",
					Priority: 200,
					Events:   []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "create_checkpoint",
						Message: "checkpoint before deny",
					},
				},
				{
					ID:       "deny-second",
					Priority: 100,
					Events:   []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "deny",
						Message: "blocked after checkpoint",
					},
				},
			},
		},
		CreateCheckpoint: func(note string) (CheckpointMetadata, error) {
			created = append(created, note)
			return CheckpointMetadata{ID: "cp-1", Name: note}, nil
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	_, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "blocked after checkpoint") {
		t.Fatalf("expected deny after checkpoint, got %v", err)
	}
	if len(created) != 1 || created[0] != "checkpoint before deny" {
		t.Fatalf("expected checkpoint to be created before deny, got %#v", created)
	}
}

func TestHookEngineAddsVerificationStep(t *testing.T) {
	engine := &HookEngine{
		Enabled: true,
		Rules: []HookRule{
			{
				ID:     "extra-verify",
				Events: []HookEvent{HookPreVerification},
				Match: HookMatch{
					ChangedFiles: []string{"**/*.sys"},
				},
				Action: HookAction{
					Type:    "add_verification_step",
					Label:   "driver verify",
					Command: "echo verify",
					Tags:    []string{"driver"},
				},
			},
		},
	}
	verdict, err := engine.Evaluate(context.Background(), HookPreVerification, HookPayload{
		"changed_files": []string{"build/agent.sys"},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(verdict.VerificationAdds) != 1 {
		t.Fatalf("expected one verification step, got %#v", verdict.VerificationAdds)
	}
	if verdict.VerificationAdds[0].Command != "echo verify" {
		t.Fatalf("unexpected step: %#v", verdict.VerificationAdds[0])
	}
}

func TestHookEngineAppendReviewContextAlias(t *testing.T) {
	engine := &HookEngine{
		Enabled: true,
		Rules: []HookRule{
			{
				ID:     "review-context",
				Events: []HookEvent{HookPreVerification},
				Match: HookMatch{
					ChangedFiles: []string{"**/*.man"},
				},
				Action: HookAction{
					Type:    "append_review_context",
					Message: "review telemetry manifest assumptions",
				},
			},
		},
	}
	verdict, err := engine.Evaluate(context.Background(), HookPreVerification, HookPayload{
		"changed_files": []string{"telemetry/provider.man"},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(verdict.ContextAdds) != 1 || verdict.ContextAdds[0] != "review telemetry manifest assumptions" {
		t.Fatalf("unexpected context adds: %#v", verdict.ContextAdds)
	}
}

func TestLoadHookEngineFromWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	hooksDir := filepath.Join(root, userConfigDirName)
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(hooksDir, "hooks.json")
	if err := os.WriteFile(path, []byte(InitHooksTemplate()), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	engine, warns := LoadHookEngine(root, DefaultConfig(root))
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %#v", warns)
	}
	if engine == nil || len(engine.Rules) == 0 {
		t.Fatalf("expected loaded engine, got %#v", engine)
	}
}

func TestHookRuntimeInjectsRecentFailedEvidence(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		ID:        "ev-1",
		Workspace: root,
		Kind:      "verification_failure",
		Category:  "driver",
		Subject:   "runtime_error",
		Outcome:   "failed",
		Tags:      []string{"failure", "driver"},
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "driver-failure",
					Events: []HookEvent{HookPreGitPush},
					Match: HookMatch{
						EvidenceCategories:      []string{"driver"},
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "warn",
						Message: "recent failed driver evidence",
					},
				},
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	verdict, err := runtime.Run(context.Background(), HookPreGitPush, HookPayload{
		"changed_files": []string{"driver/guard.cpp"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(verdict.Warns) != 1 {
		t.Fatalf("expected evidence-based warning, got %#v", verdict)
	}
}

func TestHookRuntimeMatchesTelemetryFailedEvidence(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		ID:        "ev-telemetry",
		Workspace: root,
		Kind:      "verification_failure",
		Category:  "telemetry",
		Subject:   "runtime_error",
		Outcome:   "failed",
		Tags:      []string{"failure", "telemetry"},
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "telemetry-failure",
					Events: []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						EvidenceCategories:      []string{"telemetry"},
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "ask",
						Message: "telemetry failed evidence",
					},
				},
			},
		},
		Ask: func(string) (bool, error) {
			return true, nil
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	verdict, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{
		"changed_files": []string{"telemetry/provider.man"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if verdict.AskMessage == "" && len(verdict.MatchedRuleIDs) == 0 {
		t.Fatalf("expected telemetry evidence match, got %#v", verdict)
	}
}

func TestBuiltinWindowsSecurityPresetIncludesAdditionalEvidencePolicies(t *testing.T) {
	rules, err := builtinHookPresetRules("windows-security")
	if err != nil {
		t.Fatalf("builtinHookPresetRules: %v", err)
	}
	var telemetryRule bool
	var memoryScanRule bool
	var repeatedMemoryScanRule bool
	var telemetryReviewContextRule bool
	var unrealRule bool
	var driverDenyRule bool
	var repeatedDriverRule bool
	var checkpointBeforeDriverDenyRule bool
	var simulationTamperRule bool
	var simulationStealthRule bool
	var simulationForensicsRule bool
	for _, rule := range rules {
		switch rule.ID {
		case "deny-driver-pr-with-critical-signing-or-symbol-evidence":
			driverDenyRule = true
		case "deny-driver-pr-with-repeated-sensitive-artifact-failures":
			repeatedDriverRule = true
			if rule.Action.Type == "create_checkpoint" {
				checkpointBeforeDriverDenyRule = true
			}
		case "ask-before-telemetry-push-with-failed-evidence":
			telemetryRule = true
		case "append-review-context-before-telemetry-verification":
			telemetryReviewContextRule = true
		case "ask-before-memory-scan-push-with-failed-evidence":
			memoryScanRule = true
		case "deny-memory-scan-push-with-repeated-failures":
			repeatedMemoryScanRule = true
		case "warn-before-unreal-pr-with-failed-evidence":
			unrealRule = true
		case "warn-before-push-with-high-risk-simulation-tamper-finding":
			simulationTamperRule = true
		case "warn-before-push-with-high-risk-simulation-stealth-finding":
			simulationStealthRule = true
		case "warn-before-pr-with-forensic-blind-spot-simulation":
			simulationForensicsRule = true
		}
	}
	if !driverDenyRule || !repeatedDriverRule || !checkpointBeforeDriverDenyRule || !telemetryRule || !telemetryReviewContextRule || !memoryScanRule || !repeatedMemoryScanRule || !unrealRule || !simulationTamperRule || !simulationStealthRule || !simulationForensicsRule {
		t.Fatalf("expected expanded evidence policies, got %#v", rules)
	}
}

func TestHookRuntimeMatchesSimulationTamperEvidenceForWarn(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		ID:          "ev-sim-tamper",
		Workspace:   root,
		Kind:        "simulation_finding",
		Category:    "driver",
		Subject:     "unsigned-or-unverified-driver-surface",
		Outcome:     "failed",
		Severity:    "critical",
		SignalClass: "tamper",
		RiskScore:   88,
		Tags:        []string{"simulation", "tamper-surface", "tamper_surface"},
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "simulation-tamper-warn",
					Events: []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						EvidenceTags:            []string{"simulation", "tamper-surface"},
						EvidenceSeverities:      []string{"critical"},
						SignalClasses:           []string{"tamper"},
						MinEvidenceRiskScore:    intPtr(60),
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "warn",
						Message: "simulation tamper warning",
					},
				},
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	verdict, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(verdict.Warns) != 1 || !strings.Contains(strings.ToLower(verdict.Warns[0].Message), "simulation tamper") {
		t.Fatalf("expected simulation warning, got %#v", verdict)
	}
}

func TestHookRuntimeMatchesDriverEvidenceTagsForDeny(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		ID:        "ev-driver-signing",
		Workspace: root,
		Kind:      "verification_artifact",
		Category:  "driver",
		Subject:   "driver/guard.sys",
		Outcome:   "failed",
		Tags:      []string{"artifact", "signing", "driver"},
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "driver-deny",
					Events: []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						EvidenceCategories:      []string{"driver"},
						EvidenceTags:            []string{"signing"},
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "deny",
						Message: "failed signing evidence",
					},
				},
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	_, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{
		"changed_files": []string{"driver/guard.cpp"},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "failed signing evidence") {
		t.Fatalf("expected deny from evidence tags, got %v", err)
	}
}

func TestHookRuntimeMatchesEvidenceSeverityAndRisk(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		ID:          "ev-driver-critical",
		Workspace:   root,
		Kind:        "verification_artifact",
		Category:    "driver",
		Subject:     "driver/guard.sys",
		Outcome:     "failed",
		Severity:    "critical",
		SignalClass: "signing",
		RiskScore:   92,
		Tags:        []string{"artifact", "signing", "driver"},
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "critical-risk-deny",
					Events: []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						EvidenceCategories:      []string{"driver"},
						EvidenceSeverities:      []string{"critical"},
						SignalClasses:           []string{"signing"},
						MinEvidenceRiskScore:    intPtr(80),
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "deny",
						Message: "critical driver evidence",
					},
				},
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	_, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{
		"changed_files": []string{"driver/guard.cpp"},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "critical driver evidence") {
		t.Fatalf("expected severity/risk deny, got %v", err)
	}
}

func TestHookRuntimeMatchesRepeatedDriverArtifactEvidenceForDeny(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	for _, record := range []EvidenceRecord{
		{
			ID:        "ev-driver-sys",
			Workspace: root,
			Kind:      "verification_artifact",
			Category:  "driver",
			Subject:   "build/guard.sys",
			Outcome:   "failed",
			Tags:      []string{"artifact", "driver", "signing"},
		},
		{
			ID:        "ev-driver-inf",
			Workspace: root,
			Kind:      "verification_artifact",
			Category:  "driver",
			Subject:   "build/guard.inf",
			Outcome:   "failed",
			Tags:      []string{"artifact", "driver"},
		},
	} {
		if err := evidence.Append(record); err != nil {
			t.Fatalf("Append evidence: %v", err)
		}
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "driver-repeated-artifact-deny",
					Events: []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						EvidenceCategories:      []string{"driver"},
						EvidenceSubjects:        []string{"**/*.sys", "**/*.inf", "**/*.cat"},
						MinEvidenceMatches:      intPtr(2),
						MaxEvidenceAgeHours:     intPtr(24),
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "deny",
						Message: "repeated driver artifact failures",
					},
				},
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	_, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{
		"changed_files": []string{"driver/guard.cpp"},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "repeated driver artifact failures") {
		t.Fatalf("expected deny from repeated artifact failures, got %v", err)
	}
}

func TestHookRuntimeRequiresMinEvidenceMatches(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	if err := evidence.Append(EvidenceRecord{
		ID:        "ev-memory-1",
		Workspace: root,
		Kind:      "verification_failure",
		Category:  "memory-scan",
		Subject:   "runtime_error",
		Outcome:   "failed",
		Tags:      []string{"failure", "memory-scan"},
	}); err != nil {
		t.Fatalf("Append evidence: %v", err)
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "memory-scan-repeated",
					Events: []HookEvent{HookPreGitPush},
					Match: HookMatch{
						EvidenceCategories:      []string{"memory-scan"},
						MinEvidenceMatches:      intPtr(2),
						MaxEvidenceAgeHours:     intPtr(24),
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "deny",
						Message: "repeated memory-scan failures",
					},
				},
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	verdict, err := runtime.Run(context.Background(), HookPreGitPush, HookPayload{
		"changed_files": []string{"memoryscan/scanner.cpp"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !verdict.Allow {
		t.Fatalf("expected allow when min evidence threshold is not met: %#v", verdict)
	}
	if len(verdict.MatchedRuleIDs) != 0 {
		t.Fatalf("expected no matches when min evidence threshold is not met: %#v", verdict)
	}
}

func TestHookRuntimeIgnoresOldEvidenceWhenMaxAgeIsSet(t *testing.T) {
	root := t.TempDir()
	evidence := &EvidenceStore{
		Path: filepath.Join(root, "evidence.json"),
	}
	for _, record := range []EvidenceRecord{
		{
			ID:        "ev-old-driver-sys",
			Workspace: root,
			Kind:      "verification_artifact",
			Category:  "driver",
			Subject:   "build/guard.sys",
			Outcome:   "failed",
			Tags:      []string{"artifact", "driver"},
			CreatedAt: time.Now().Add(-48 * time.Hour),
		},
		{
			ID:        "ev-old-driver-inf",
			Workspace: root,
			Kind:      "verification_artifact",
			Category:  "driver",
			Subject:   "build/guard.inf",
			Outcome:   "failed",
			Tags:      []string{"artifact", "driver"},
			CreatedAt: time.Now().Add(-36 * time.Hour),
		},
	} {
		if err := evidence.Append(record); err != nil {
			t.Fatalf("Append evidence: %v", err)
		}
	}
	runtime := &HookRuntime{
		Engine: &HookEngine{
			Enabled: true,
			Rules: []HookRule{
				{
					ID:     "driver-recent-artifact-deny",
					Events: []HookEvent{HookPreCreatePR},
					Match: HookMatch{
						EvidenceCategories:      []string{"driver"},
						EvidenceSubjects:        []string{"**/*.sys", "**/*.inf"},
						MinEvidenceMatches:      intPtr(2),
						MaxEvidenceAgeHours:     intPtr(24),
						HasRecentFailedEvidence: boolPtr(true),
					},
					Action: HookAction{
						Type:    "deny",
						Message: "recent repeated driver artifact failures",
					},
				},
			},
		},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Evidence:  evidence,
	}
	verdict, err := runtime.Run(context.Background(), HookPreCreatePR, HookPayload{
		"changed_files": []string{"driver/guard.cpp"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !verdict.Allow {
		t.Fatalf("expected allow when evidence is older than max age: %#v", verdict)
	}
	if len(verdict.MatchedRuleIDs) != 0 {
		t.Fatalf("expected no matches for stale evidence: %#v", verdict)
	}
}
