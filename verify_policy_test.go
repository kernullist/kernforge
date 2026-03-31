package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildVerificationPlanWithPolicyCanPromoteDefaultStep(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".kernforge"), 0o755); err != nil {
		t.Fatalf("mkdir .kernforge: %v", err)
	}
	policy := `{
  "defaults": [
    { "match": "go vet workspace", "priority": 500 }
  ]
}`
	if err := os.WriteFile(filepath.Join(root, ".kernforge", "verify.json"), []byte(policy), 0o644); err != nil {
		t.Fatalf("write verify.json: %v", err)
	}

	plan := buildVerificationPlanWithTuning(root, []string{filepath.Join(root, "internal", "auth", "service.go")}, VerificationFull, VerificationTuning{})
	if len(plan.Steps) < 2 {
		t.Fatalf("unexpected verification plan: %#v", plan.Steps)
	}
	if plan.Steps[0].Command != "go vet ./..." {
		t.Fatalf("expected policy-promoted go vet first, got %#v", plan.Steps)
	}
}

func TestBuildVerificationPlanWithPolicyAddsCustomStep(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".kernforge"), 0o755); err != nil {
		t.Fatalf("mkdir .kernforge: %v", err)
	}
	policy := `{
  "steps": [
    {
      "label": "go test ./integration/...",
      "command": "go test ./integration/...",
      "stage": "workspace",
      "priority": 250,
      "when": ["internal/auth/*.go"]
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(root, ".kernforge", "verify.json"), []byte(policy), 0o644); err != nil {
		t.Fatalf("write verify.json: %v", err)
	}

	plan := buildVerificationPlanWithTuning(root, []string{"internal/auth/service.go"}, VerificationAdaptive, VerificationTuning{})
	found := false
	for _, step := range plan.Steps {
		if step.Command == "go test ./integration/..." {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected custom verification step in plan, got %#v", plan.Steps)
	}
	if plan.PlannerNote == "" {
		t.Fatalf("expected planner note for custom policy step, got empty note")
	}
}

func TestInitVerifyPolicyTemplateIsValidJSON(t *testing.T) {
	text := InitVerifyPolicyTemplate()
	var decoded VerificationPolicy
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("template must be valid json: %v\n%s", err, text)
	}
	if len(decoded.Defaults) == 0 {
		t.Fatalf("expected defaults in verify policy template, got %#v", decoded)
	}
	if len(decoded.Steps) == 0 {
		t.Fatalf("expected sample custom step in verify policy template, got %#v", decoded)
	}
}

func TestVerificationPolicyCanEnableContinueOnFailure(t *testing.T) {
	policy := VerificationPolicy{
		Defaults: []VerificationPolicyDefault{{
			Match:             "go test workspace",
			ContinueOnFailure: boolPtr(true),
		}},
	}
	steps, _ := applyVerificationPolicy("", []VerificationStep{
		{Label: "go test ./...", Command: "go test ./...", Stage: "workspace", Status: VerificationPending},
	}, nil, VerificationAdaptive, policy)
	if len(steps) != 1 || !steps[0].ContinueOnFailure {
		t.Fatalf("expected continue_on_failure to be applied, got %#v", steps)
	}
}

func TestVerificationPolicyCanEnableStopOnFailure(t *testing.T) {
	policy := VerificationPolicy{
		Defaults: []VerificationPolicyDefault{{
			Match:         "go test workspace",
			StopOnFailure: boolPtr(true),
		}},
	}
	steps, _ := applyVerificationPolicy("", []VerificationStep{
		{Label: "go test ./...", Command: "go test ./...", Stage: "workspace", Status: VerificationPending},
	}, nil, VerificationAdaptive, policy)
	if len(steps) != 1 || !steps[0].StopOnFailure {
		t.Fatalf("expected stop_on_failure to be applied, got %#v", steps)
	}
}

func TestVerificationPolicyStepOnlyIfAndExcludeWhen(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kernforge"), 0o755); err != nil {
		t.Fatalf("mkdir .kernforge: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"lint":"eslint ."}}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	policy := VerificationPolicy{
		Steps: []VerificationPolicyStep{{
			Label:       "npm run lint",
			Command:     "npm run lint",
			OnlyIf:      []string{"script:lint", "mode:adaptive"},
			ExcludeWhen: []string{"changed:**/*.md"},
		}},
	}
	steps, _ := applyVerificationPolicy(root, nil, []string{"src/app.ts"}, VerificationAdaptive, policy)
	if len(steps) != 1 {
		t.Fatalf("expected policy step to be enabled, got %#v", steps)
	}
	steps, _ = applyVerificationPolicy(root, nil, []string{"docs/readme.md"}, VerificationAdaptive, policy)
	if len(steps) != 0 {
		t.Fatalf("expected policy step to be excluded for markdown-only changes, got %#v", steps)
	}
	steps, _ = applyVerificationPolicy(root, nil, []string{"src/app.ts"}, VerificationFull, policy)
	if len(steps) != 0 {
		t.Fatalf("expected policy step to be disabled in full mode, got %#v", steps)
	}
}
