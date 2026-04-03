package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifySecurityVerificationCategories(t *testing.T) {
	changed := []string{
		"driver/guard.cpp",
		"telemetry/etw_provider.cpp",
		"Game/AntiCheat.Build.cs",
		"scanner/patternscan.cpp",
	}
	got := classifySecurityVerificationCategories(changed)
	want := []SecurityVerificationCategory{
		SecurityCategoryDriver,
		SecurityCategoryTelemetry,
		SecurityCategoryUnreal,
		SecurityCategoryMemoryScan,
	}
	if len(got) != len(want) {
		t.Fatalf("unexpected categories: %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected category order: got %#v want %#v", got, want)
		}
	}
}

func TestBuildSecurityVerificationStepsForDriverSource(t *testing.T) {
	root := t.TempDir()
	steps, note := buildSecurityVerificationSteps(root, []string{"driver/guard.cpp"}, VerificationAdaptive)
	if len(steps) == 0 {
		t.Fatal("expected security verification steps")
	}
	if !strings.Contains(strings.ToLower(note), "driver") {
		t.Fatalf("unexpected note: %q", note)
	}
	found := false
	for _, step := range steps {
		if strings.Contains(strings.ToLower(step.Label), "driver readiness review") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected driver readiness review step, got %#v", steps)
	}
}

func TestBuildVerificationPlanIncludesSecuritySummary(t *testing.T) {
	root := t.TempDir()
	plan := buildVerificationPlan(root, []string{"telemetry/provider.cpp"}, VerificationAdaptive)
	if len(plan.Steps) == 0 {
		t.Fatal("expected verification steps")
	}
	if !strings.Contains(plan.PlannerNote, "security_categories=telemetry") {
		t.Fatalf("unexpected planner note: %q", plan.PlannerNote)
	}
}

func TestCollectTelemetryManifestFiles(t *testing.T) {
	got := collectTelemetryManifestFiles([]string{
		"telemetry/provider.man",
		"trace/events.xml",
		"docs/readme.md",
	})
	if len(got) != 2 {
		t.Fatalf("unexpected manifest files: %#v", got)
	}
}

func TestDiscoverNearbyDriverArtifacts(t *testing.T) {
	root := t.TempDir()
	driverDir := filepath.Join(root, "driver")
	if err := os.MkdirAll(driverDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, name := range []string{"guard.sys", "guard.cat", "guard.inf"} {
		if err := os.WriteFile(filepath.Join(driverDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	got := discoverNearbyDriverArtifacts(root, []string{"driver/guard.cpp"})
	if len(got) < 3 {
		t.Fatalf("expected nearby artifacts, got %#v", got)
	}
}

func TestCollectDriverInfFilesFallsBackToNearbyArtifacts(t *testing.T) {
	root := t.TempDir()
	driverDir := filepath.Join(root, "driver")
	if err := os.MkdirAll(driverDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(driverDir, "guard.inf"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got := collectDriverInfFiles(root, []string{"driver/guard.cpp"})
	if len(got) != 1 || got[0] != "driver/guard.inf" {
		t.Fatalf("unexpected inf files: %#v", got)
	}
}
