package main

import (
	"encoding/json"
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

func TestBuildVerificationPlanUsesLatestAnalysisDocsMatrix(t *testing.T) {
	root := t.TempDir()
	analysisCfg := configProjectAnalysis(DefaultConfig(root), root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest: %v", err)
	}
	manifest := AnalysisDocsManifest{
		Documents: []AnalysisGeneratedDoc{{Name: "VERIFICATION_MATRIX.md"}},
		VerificationMatrix: []AnalysisVerificationMatrixEntry{
			{
				ChangeArea:           "Driver or IOCTL",
				RequiredVerification: "driver build and symbol/signing readiness",
				OptionalVerification: "Driver Verifier smoke checklist",
				EvidenceHook:         "driver evidence bundle",
				SourceAnchors:        []string{"driver/dispatch.cpp:42"},
				Confidence:           "high",
			},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "docs_manifest.json"), data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	plan := buildVerificationPlan(root, []string{"driver/dispatch.cpp"}, VerificationAdaptive)
	found := false
	for _, step := range plan.Steps {
		if strings.Contains(step.Label, "analysis docs verification") && strings.Contains(step.Command, "driver build and symbol/signing readiness") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected analysis docs verification step, got %+v", plan.Steps)
	}
	if !strings.Contains(plan.PlannerNote, "Generated verification matrix added") {
		t.Fatalf("expected planner note to cite generated matrix, got %q", plan.PlannerNote)
	}
}

func TestBuildVerificationPlanUsesFuzzCampaignNativeResults(t *testing.T) {
	root := t.TempDir()
	campaign, err := createFuzzCampaignFromWorkspace(root, "driver fuzz", AnalysisDocsManifest{})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	campaign.NativeResults = []FuzzCampaignNativeResult{{
		RunID:              "ff-1",
		Target:             "ValidatePacket",
		TargetFile:         "driver/packet.cpp",
		Status:             "failed",
		Outcome:            "failed",
		CrashCount:         1,
		CrashFingerprint:   "ff-deadbeef",
		SuspectedInvariant: "packet length must remain bounded",
		ReportPath:         filepath.Join(campaign.ReportsDir, "native-result-ff-1-failed.md"),
		CrashDir:           filepath.Join(campaign.CrashDir, "ff-1"),
		MinimizeCommand:    "fuzz-driver -merge=1 corpus/minimized corpus",
	}}
	if err := writeFuzzCampaignManifest(campaign); err != nil {
		t.Fatalf("write campaign manifest: %v", err)
	}

	plan := buildVerificationPlan(root, []string{"driver/packet.cpp"}, VerificationAdaptive)
	found := false
	for _, step := range plan.Steps {
		if strings.Contains(step.Label, "fuzz evidence regression") && strings.Contains(step.Command, "ff-deadbeef") {
			found = true
			if !containsString(step.Tags, "fuzz_native_result") {
				t.Fatalf("expected fuzz_native_result tag, got %#v", step.Tags)
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected fuzz native verification step, got %#v", plan.Steps)
	}
	if !strings.Contains(plan.PlannerNote, "Fuzz campaign native results added") {
		t.Fatalf("expected fuzz planner note, got %q", plan.PlannerNote)
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
