package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fuzzCampaignTestLaunchableRun(id string, name string, file string) FunctionFuzzRun {
	return FunctionFuzzRun{
		ID:               id,
		TargetSymbolName: name,
		TargetFile:       file,
		Execution: FunctionFuzzExecution{
			Eligible:             true,
			Status:               "planned",
			BuildScriptPath:      "C:/work/build.ps1",
			CompilerResolvedPath: "C:/llvm/bin/clang-cl.exe",
		},
	}
}

func TestFuzzCampaignSelectCoverageRelaunchesIsBounded(t *testing.T) {
	run := fuzzCampaignTestLaunchableRun("run-cov-1", "ParsePacketHeader", "src/parser.cpp")
	runsByID := map[string]FunctionFuzzRun{run.ID: run}
	gaps := []FuzzCampaignCoverageGap{
		{
			Target:     "ParsePacketHeader",
			TargetFile: "src/parser.cpp",
			RunID:      "run-cov-1",
			Reason:     "coverage gap",
		},
		// A duplicate gap for the same target must not earn a second re-launch.
		{
			Target:     "ParsePacketHeader",
			TargetFile: "src/parser.cpp",
			Reason:     "coverage gap (libfuzzer feature count low)",
		},
	}

	campaign := FuzzCampaign{ID: "campaign-cov-1"}
	first := fuzzCampaignSelectCoverageRelaunches(campaign, gaps, runsByID)
	if len(first) != 1 {
		t.Fatalf("expected exactly one re-launch decision, got %d", len(first))
	}
	if first[0].Run.ID != "run-cov-1" {
		t.Fatalf("expected decision to bind run-cov-1, got %q", first[0].Run.ID)
	}

	// Record the issued re-launch, then a second pass must not re-launch the
	// same target again (the loop guard).
	campaign.RelaunchedTargets = normalizeFuzzCampaignRelaunchTargets(append(campaign.RelaunchedTargets, first[0].TargetKey))
	second := fuzzCampaignSelectCoverageRelaunches(campaign, gaps, runsByID)
	if len(second) != 0 {
		t.Fatalf("expected no re-launch on second pass after recording target, got %d", len(second))
	}
}

func TestFuzzCampaignSelectCoverageRelaunchesSkipsRunWithoutContext(t *testing.T) {
	// A target whose attached run has no model/compile context (not eligible,
	// no build script) records the gap but never earns a fake re-launch.
	run := FunctionFuzzRun{
		ID:               "run-no-ctx",
		TargetSymbolName: "DispatchIoctl",
		TargetFile:       "src/dispatch.cpp",
		Execution:        FunctionFuzzExecution{Eligible: false},
	}
	runsByID := map[string]FunctionFuzzRun{run.ID: run}
	gaps := []FuzzCampaignCoverageGap{
		{Target: "DispatchIoctl", TargetFile: "src/dispatch.cpp", RunID: "run-no-ctx", Reason: "coverage gap"},
	}
	decisions := fuzzCampaignSelectCoverageRelaunches(FuzzCampaign{ID: "campaign-no-ctx"}, gaps, runsByID)
	if len(decisions) != 0 {
		t.Fatalf("expected no re-launch decision without model/compile context, got %d", len(decisions))
	}
}

func TestFuzzCampaignSelectCoverageRelaunchesMatchesByTargetWhenRunIDMissing(t *testing.T) {
	run := fuzzCampaignTestLaunchableRun("run-match-1", "ValidateRequest", "src/net/guard.cpp")
	runsByID := map[string]FunctionFuzzRun{run.ID: run}
	// Gap carries no RunID; matching falls back to target name + file.
	gaps := []FuzzCampaignCoverageGap{
		{Target: "ValidateRequest", TargetFile: "src/net/guard.cpp", Reason: "coverage gap"},
	}
	decisions := fuzzCampaignSelectCoverageRelaunches(FuzzCampaign{ID: "campaign-match-1"}, gaps, runsByID)
	if len(decisions) != 1 {
		t.Fatalf("expected one decision via target match, got %d", len(decisions))
	}
	if decisions[0].Run.ID != "run-match-1" {
		t.Fatalf("expected matched run-match-1, got %q", decisions[0].Run.ID)
	}
}

func TestMaybeRelaunchFuzzCampaignCoverageGapsIsBoundedAndIdempotent(t *testing.T) {
	root := t.TempDir()
	// Real temp paths so the runner-script rewrite succeeds without a real
	// compiler or background job manager (rt.backgroundJobs stays nil).
	buildScript := filepath.Join(root, "build.ps1")
	if err := os.WriteFile(buildScript, []byte("# placeholder\n"), 0o644); err != nil {
		t.Fatalf("seed build script: %v", err)
	}
	run := FunctionFuzzRun{
		ID:               "run-e2e-1",
		Workspace:        root,
		TargetSymbolName: "ParsePacketHeader",
		TargetFile:       "src/parser.cpp",
		StaticRiskScore:  60,
		RiskScore:        60,
		Execution: FunctionFuzzExecution{
			Eligible:             true,
			Status:               "completed",
			Profile:              "smoke",
			BuildScriptPath:      buildScript,
			CompilerResolvedPath: filepath.Join(root, "clang-cl.exe"),
			ExecutablePath:       filepath.Join(root, "out", "fuzzer.exe"),
			CorpusDir:            filepath.Join(root, "corpus"),
			CrashDir:             filepath.Join(root, "crashes"),
		},
	}

	store := &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")}
	if _, err := store.Upsert(run); err != nil {
		t.Fatalf("seed function fuzz run: %v", err)
	}
	rt := &runtimeState{
		cfg:          DefaultConfig(root),
		writer:       &bytes.Buffer{},
		ui:           NewUI(),
		functionFuzz: store,
		workspace:    Workspace{BaseRoot: root, Root: root},
	}

	campaign := FuzzCampaign{
		ID:           "campaign-e2e-1",
		Workspace:    root,
		FunctionRuns: []string{run.ID},
		SeedTargets: []FuzzCampaignSeedTarget{
			{Name: "ParsePacketHeader", File: "src/parser.cpp"},
		},
		CoverageReports: []FuzzCampaignCoverageReport{
			{
				ID:         "cov-gap-1",
				Target:     "ParsePacketHeader",
				TargetFile: "src/parser.cpp",
				RunID:      run.ID,
				Gap:        true,
				GapReason:  "libFuzzer feature count is low: 8",
			},
		},
	}

	relaunchedRuns := []FunctionFuzzRun{run}
	updated, relaunched, err := rt.maybeRelaunchFuzzCampaignCoverageGaps(campaign, relaunchedRuns)
	if err != nil {
		t.Fatalf("first relaunch pass: %v", err)
	}
	if len(relaunched) != 1 {
		t.Fatalf("expected exactly one re-launch, got %d (%v)", len(relaunched), relaunched)
	}
	wantKey := fuzzCampaignRelaunchTargetKey("ParsePacketHeader", "src/parser.cpp")
	if !fuzzCampaignTargetAlreadyRelaunched(updated, wantKey) {
		t.Fatalf("expected target %q recorded in RelaunchedTargets %v", wantKey, updated.RelaunchedTargets)
	}

	// The persisted run was restaged on the extended profile with a bumped budget.
	stored, ok, err := store.Get(run.ID)
	if err != nil || !ok {
		t.Fatalf("load restaged run: ok=%v err=%v", ok, err)
	}
	if stored.Execution.Profile != "extended" {
		t.Fatalf("expected extended profile after re-launch, got %q", stored.Execution.Profile)
	}
	foundBump := false
	for _, arg := range stored.Execution.RunArgv {
		if arg == fmt.Sprintf("-max_total_time=%d", functionFuzzExtendedRelaunchMaxTotalTime) {
			foundBump = true
		}
	}
	if !foundBump {
		t.Fatalf("expected bumped -max_total_time in run argv %v", stored.Execution.RunArgv)
	}

	// Second pass over the SAME campaign state must not re-launch again.
	_, relaunchedAgain, err := rt.maybeRelaunchFuzzCampaignCoverageGaps(updated, relaunchedRuns)
	if err != nil {
		t.Fatalf("second relaunch pass: %v", err)
	}
	if len(relaunchedAgain) != 0 {
		t.Fatalf("expected no re-launch on second pass, got %d (%v)", len(relaunchedAgain), relaunchedAgain)
	}
}

func TestMaybeRelaunchFuzzCampaignCoverageGapsSkipsRunWithoutContext(t *testing.T) {
	root := t.TempDir()
	run := FunctionFuzzRun{
		ID:               "run-no-ctx-e2e",
		Workspace:        root,
		TargetSymbolName: "DispatchIoctl",
		TargetFile:       "src/dispatch.cpp",
		Execution:        FunctionFuzzExecution{Eligible: false},
	}
	store := &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")}
	if _, err := store.Upsert(run); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	rt := &runtimeState{
		cfg:          DefaultConfig(root),
		writer:       &bytes.Buffer{},
		ui:           NewUI(),
		functionFuzz: store,
		workspace:    Workspace{BaseRoot: root, Root: root},
	}
	campaign := FuzzCampaign{
		ID:           "campaign-no-ctx-e2e",
		Workspace:    root,
		FunctionRuns: []string{run.ID},
		SeedTargets: []FuzzCampaignSeedTarget{
			{Name: "DispatchIoctl", File: "src/dispatch.cpp"},
		},
		CoverageReports: []FuzzCampaignCoverageReport{
			{ID: "cov-gap-2", Target: "DispatchIoctl", TargetFile: "src/dispatch.cpp", RunID: run.ID, Gap: true, GapReason: "coverage gap"},
		},
	}
	updated, relaunched, err := rt.maybeRelaunchFuzzCampaignCoverageGaps(campaign, []FunctionFuzzRun{run})
	if err != nil {
		t.Fatalf("relaunch pass: %v", err)
	}
	if len(relaunched) != 0 {
		t.Fatalf("expected no re-launch without context, got %d", len(relaunched))
	}
	if len(updated.RelaunchedTargets) != 0 {
		t.Fatalf("expected no recorded targets, got %v", updated.RelaunchedTargets)
	}
}

func TestCreateFuzzCampaignFromWorkspaceWritesStandardLayout(t *testing.T) {
	root := t.TempDir()
	manifest := AnalysisDocsManifest{
		RunID: "analysis-1",
		FuzzTargets: []AnalysisFuzzTargetCatalogEntry{
			{
				Name:             "ValidateRequest",
				File:             "src/guard.cpp",
				SymbolID:         "func:ValidateRequest",
				SourceAnchor:     "src/guard.cpp:42",
				PriorityScore:    91,
				PriorityReasons:  []string{"parser surface"},
				SuggestedCommand: "/fuzz-func ValidateRequest --file src/guard.cpp",
			},
		},
	}

	campaign, err := createFuzzCampaignFromWorkspace(root, "driver parser campaign", manifest)
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}

	for _, path := range []string{
		campaign.ManifestPath,
		campaign.CorpusDir,
		campaign.CrashDir,
		campaign.CoverageDir,
		campaign.ReportsDir,
		campaign.LogsDir,
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected campaign path %s: %v", path, err)
		}
	}
	if len(campaign.SeedTargets) != 1 {
		t.Fatalf("expected one seed target, got %#v", campaign.SeedTargets)
	}
	if len(campaign.CoverageGaps) != 1 || campaign.CoverageGaps[0].SourceAnchor != "src/guard.cpp:42" {
		t.Fatalf("expected initial coverage gap feedback, got %#v", campaign.CoverageGaps)
	}
	if !strings.Contains(renderFuzzCampaign(campaign), "Coverage gaps:") {
		t.Fatalf("expected rendered campaign to show coverage gaps")
	}
	if campaign.SeedTargets[0].Provenance != "analysis_docs:analysis-1" {
		t.Fatalf("unexpected provenance: %#v", campaign.SeedTargets[0])
	}
	if !strings.Contains(filepath.ToSlash(campaign.ManifestPath), ".kernforge/fuzz/") {
		t.Fatalf("expected manifest under .kernforge/fuzz, got %s", campaign.ManifestPath)
	}
}

func TestFuzzCampaignStoreAppendAndGet(t *testing.T) {
	dir := t.TempDir()
	store := &FuzzCampaignStore{Path: filepath.Join(dir, "fuzz_campaigns.json")}
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	campaign, err := store.Append(FuzzCampaign{
		ID:        "campaign-test",
		Workspace: dir,
		Name:      "ioctl campaign",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("append campaign: %v", err)
	}
	if campaign.Status != "planned" {
		t.Fatalf("expected planned status, got %q", campaign.Status)
	}

	got, ok, err := store.Get("campaign-test")
	if err != nil {
		t.Fatalf("get campaign: %v", err)
	}
	if !ok || got.ID != "campaign-test" {
		t.Fatalf("expected stored campaign, got ok=%v %#v", ok, got)
	}
	recent, err := store.ListRecent(dir, 5)
	if err != nil {
		t.Fatalf("list campaign: %v", err)
	}
	if len(recent) != 1 || recent[0].ID != "campaign-test" {
		t.Fatalf("unexpected recent campaigns: %#v", recent)
	}
}

func TestAttachFunctionFuzzRunToCampaignAddsRunAndSeedTarget(t *testing.T) {
	root := t.TempDir()
	campaign, err := createFuzzCampaignFromWorkspace(root, "ioctl campaign", AnalysisDocsManifest{})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	run := FunctionFuzzRun{
		ID:               "fuzz-run-1",
		Workspace:        root,
		TargetQuery:      "ValidateRequest",
		TargetSymbolID:   "func:ValidateRequest",
		TargetSymbolName: "ValidateRequest",
		TargetFile:       "src/guard.cpp",
		TargetStartLine:  42,
		RiskScore:        88,
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{
				Title:          "length mismatch reaches copy",
				ConcreteInputs: []string{"len=4096, buffer=16 bytes"},
			},
		},
	}

	updated := attachFunctionFuzzRunToCampaign(campaign, run)
	if len(updated.FunctionRuns) != 1 || updated.FunctionRuns[0] != "fuzz-run-1" {
		t.Fatalf("expected attached run id, got %#v", updated.FunctionRuns)
	}
	if len(updated.SeedTargets) != 1 {
		t.Fatalf("expected one seed target, got %#v", updated.SeedTargets)
	}
	if updated.SeedTargets[0].Provenance != "fuzz_func:fuzz-run-1" {
		t.Fatalf("unexpected provenance: %#v", updated.SeedTargets[0])
	}
	if updated.SeedTargets[0].SourceAnchor != "src/guard.cpp:42" {
		t.Fatalf("unexpected source anchor: %#v", updated.SeedTargets[0])
	}
}

func TestPromoteFunctionFuzzRunSeedsWritesDeterministicCorpusArtifacts(t *testing.T) {
	root := t.TempDir()
	campaign, err := createFuzzCampaignFromWorkspace(root, "seed campaign", AnalysisDocsManifest{})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	run := FunctionFuzzRun{
		ID:               "fuzz-run-2",
		Workspace:        root,
		TargetQuery:      "ValidateRequest",
		TargetSymbolID:   "func:ValidateRequest",
		TargetSymbolName: "ValidateRequest",
		TargetFile:       "src/guard.cpp",
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{
				Title:          "oversized length",
				Confidence:     "medium",
				RiskScore:      91,
				ConcreteInputs: []string{"len=65535"},
				Inputs:         []string{"large caller supplied length"},
				ExpectedFlow:   "length flows into copy",
				LikelyIssues:   []string{"out-of-bounds read"},
				SourceExcerpt: FunctionFuzzSourceExcerpt{
					File:      "src/guard.cpp",
					FocusLine: 77,
				},
			},
		},
	}

	updated, promoted, err := promoteFunctionFuzzRunSeeds(campaign, []FunctionFuzzRun{run}, 16)
	if err != nil {
		t.Fatalf("promote seeds: %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("expected one promoted seed, got %#v", promoted)
	}
	if _, err := os.Stat(promoted[0].Path); err != nil {
		t.Fatalf("expected promoted seed file: %v", err)
	}
	data, err := os.ReadFile(promoted[0].Path)
	if err != nil {
		t.Fatalf("read promoted seed: %v", err)
	}
	text := string(data)
	for _, want := range []string{"kernforge.fuzz_campaign.seed.v1", "fuzz-run-2", "oversized length", "len=65535", "src/guard.cpp:77"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected seed file to contain %q\n%s", want, text)
		}
	}
	if len(updated.SeedArtifacts) != 1 {
		t.Fatalf("expected manifest seed artifact, got %#v", updated.SeedArtifacts)
	}
	if len(updated.Findings) != 1 {
		t.Fatalf("expected seed finding, got %#v", updated.Findings)
	}
	if updated.Findings[0].Status != "seeded" || updated.Findings[0].VerificationGate != "pending_native" {
		t.Fatalf("expected seeded finding lifecycle state, got %#v", updated.Findings[0])
	}
	if updated.ArtifactGraph.Schema != "kernforge.fuzz_campaign.artifact_graph.v1" || len(updated.ArtifactGraph.Nodes) == 0 || len(updated.ArtifactGraph.Edges) == 0 {
		t.Fatalf("expected artifact graph in campaign manifest, got %#v", updated.ArtifactGraph)
	}
	if !strings.Contains(filepath.ToSlash(updated.SeedArtifacts[0].Path), "corpus/fuzz-run-2/scenario-01-oversized-length.json") {
		t.Fatalf("unexpected seed path: %#v", updated.SeedArtifacts[0])
	}
}

func TestHandleFuzzCampaignRunAutomaticallyAttachesAndPromotesSeeds(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	campaignStore := &FuzzCampaignStore{Path: filepath.Join(root, "campaigns.json")}
	functionStore := &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")}
	campaign, err := createFuzzCampaignFromWorkspace(root, "campaign", AnalysisDocsManifest{})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if _, err := campaignStore.Append(campaign); err != nil {
		t.Fatalf("append campaign: %v", err)
	}
	if _, err := functionStore.Append(FunctionFuzzRun{
		ID:               "fuzz-run-cmd",
		Workspace:        root,
		TargetQuery:      "ValidateRequest",
		TargetSymbolName: "ValidateRequest",
		TargetFile:       "src/guard.cpp",
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{
				Title:          "signed length drift",
				ConcreteInputs: []string{"len=-1"},
			},
		},
	}); err != nil {
		t.Fatalf("append function fuzz: %v", err)
	}
	rt := &runtimeState{
		cfg:           DefaultConfig(root),
		writer:        &output,
		ui:            NewUI(),
		fuzzCampaigns: campaignStore,
		functionFuzz:  functionStore,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleFuzzCampaignCommand("run"); err != nil {
		t.Fatalf("run automation: %v", err)
	}
	updated, ok, err := campaignStore.Get(campaign.ID)
	if err != nil {
		t.Fatalf("get campaign: %v", err)
	}
	if !ok {
		t.Fatalf("expected campaign to exist")
	}
	if len(updated.FunctionRuns) != 1 || updated.FunctionRuns[0] != "fuzz-run-cmd" {
		t.Fatalf("expected attached run, got %#v", updated.FunctionRuns)
	}
	if len(updated.SeedArtifacts) != 1 {
		t.Fatalf("expected promoted seed artifact, got %#v", updated.SeedArtifacts)
	}
	if _, err := os.Stat(updated.SeedArtifacts[0].Path); err != nil {
		t.Fatalf("expected seed artifact file: %v", err)
	}
	if !strings.Contains(output.String(), "Kernforge advanced the fuzz campaign") || !strings.Contains(output.String(), "promoted 1 seed artifact") {
		t.Fatalf("expected automation output, got %q", output.String())
	}
}

func TestHandleFuzzCampaignRunCapturesNativeResultEvidence(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	campaignStore := &FuzzCampaignStore{Path: filepath.Join(root, "campaigns.json")}
	functionStore := &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")}
	evidenceStore := &EvidenceStore{Path: filepath.Join(root, "evidence.json")}
	campaign, err := createFuzzCampaignFromWorkspace(root, "native campaign", AnalysisDocsManifest{})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if _, err := campaignStore.Append(campaign); err != nil {
		t.Fatalf("append campaign: %v", err)
	}
	crashDir := filepath.Join(root, "fuzz-run-native", "crashes")
	if err := os.MkdirAll(crashDir, 0o755); err != nil {
		t.Fatalf("mkdir crash dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(crashDir, "crash-001"), []byte("boom"), 0o644); err != nil {
		t.Fatalf("write crash artifact: %v", err)
	}
	if _, err := functionStore.Append(FunctionFuzzRun{
		ID:               "fuzz-run-native",
		Workspace:        root,
		TargetQuery:      "ValidateRequest",
		TargetSymbolName: "ValidateRequest",
		TargetSymbolID:   "func:ValidateRequest",
		TargetFile:       "src/guard.cpp",
		RiskScore:        72,
		Execution: FunctionFuzzExecution{
			Status:     "completed",
			CrashDir:   crashDir,
			RunLogPath: filepath.Join(root, "fuzz-run-native", "run.log"),
			RunCommand: "fuzzer.exe -max_total_time=20 corpus",
		},
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{
				Title:        "oversized length",
				RiskScore:    91,
				LikelyIssues: []string{"buffer contract drift"},
				ConcreteInputs: []string{
					"len=65535",
				},
			},
		},
	}); err != nil {
		t.Fatalf("append fuzz run: %v", err)
	}
	rt := &runtimeState{
		cfg:           Config{},
		writer:        &output,
		ui:            NewUI(),
		fuzzCampaigns: campaignStore,
		functionFuzz:  functionStore,
		evidence:      evidenceStore,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
		session: &Session{ID: "session-native"},
	}

	if err := rt.handleFuzzCampaignCommand("run"); err != nil {
		t.Fatalf("run automation: %v", err)
	}
	updated, ok, err := campaignStore.Get(campaign.ID)
	if err != nil {
		t.Fatalf("get campaign: %v", err)
	}
	if !ok {
		t.Fatalf("expected campaign to exist")
	}
	if len(updated.NativeResults) != 1 {
		t.Fatalf("expected one native result, got %#v", updated.NativeResults)
	}
	if len(updated.Findings) == 0 {
		t.Fatalf("expected native finding lifecycle entries, got %#v", updated.Findings)
	}
	foundNativeFinding := false
	for _, finding := range updated.Findings {
		if finding.EvidenceID != "" && finding.VerificationGate == "required" && finding.TrackedFeatureGate == "block_close" {
			foundNativeFinding = true
			if finding.CrashFingerprint == "" || finding.ReportPath == "" || finding.SourceAnchor != "src/guard.cpp" {
				t.Fatalf("unexpected native finding content: %#v", finding)
			}
		}
	}
	if !foundNativeFinding {
		t.Fatalf("expected required native finding gate, got %#v", updated.Findings)
	}
	if updated.NativeResults[0].CrashCount != 1 || updated.NativeResults[0].EvidenceID == "" {
		t.Fatalf("expected crash evidence to be captured, got %#v", updated.NativeResults[0])
	}
	if _, err := os.Stat(updated.NativeResults[0].ReportPath); err != nil {
		t.Fatalf("expected native result report: %v", err)
	}
	records, err := evidenceStore.Search("kind:fuzz_native_result", root, 10)
	if err != nil {
		t.Fatalf("search evidence: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one fuzz evidence record, got %#v", records)
	}
	if records[0].Attributes["finding_id"] == "" || records[0].Attributes["source_anchor"] != "src/guard.cpp" {
		t.Fatalf("expected evidence to link finding and source anchor, got %#v", records[0])
	}
	if !strings.Contains(output.String(), "captured 1 native result") {
		t.Fatalf("expected native capture output, got %q", output.String())
	}
	if !strings.Contains(output.String(), "Findings:") || !strings.Contains(output.String(), "verify=required") {
		t.Fatalf("expected campaign output to show finding gates, got %q", output.String())
	}
}

func TestFuzzCampaignNativeFindingsDedupByFingerprintAndSourceAnchor(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	evidenceStore := &EvidenceStore{Path: filepath.Join(root, "evidence.json")}
	campaign, err := createFuzzCampaignFromWorkspace(root, "dedup campaign", AnalysisDocsManifest{})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	rt := &runtimeState{
		workspace: Workspace{Root: root},
		writer:    &output,
		ui:        NewUI(),
		evidence:  evidenceStore,
		session:   &Session{ID: "session-dedup"},
	}
	runs := []FunctionFuzzRun{
		{
			ID:               "fuzz-run-dedup-1",
			Workspace:        root,
			TargetQuery:      "ValidateRequest",
			TargetSymbolName: "ValidateRequest",
			TargetSymbolID:   "func:ValidateRequest",
			TargetFile:       "src/guard.cpp",
			TargetStartLine:  42,
			RiskScore:        80,
			Execution: FunctionFuzzExecution{
				Status:     "completed",
				CrashCount: 1,
				RunCommand: "fuzzer.exe corpus",
			},
			VirtualScenarios: []FunctionFuzzVirtualScenario{
				{Title: "oversized length", LikelyIssues: []string{"buffer contract drift"}},
			},
		},
		{
			ID:               "fuzz-run-dedup-2",
			Workspace:        root,
			TargetQuery:      "ValidateRequest",
			TargetSymbolName: "ValidateRequest",
			TargetSymbolID:   "func:ValidateRequest",
			TargetFile:       "src/guard.cpp",
			TargetStartLine:  42,
			RiskScore:        82,
			Execution: FunctionFuzzExecution{
				Status:     "completed",
				CrashCount: 2,
				RunCommand: "fuzzer.exe corpus",
			},
			VirtualScenarios: []FunctionFuzzVirtualScenario{
				{Title: "oversized length again", LikelyIssues: []string{"buffer contract drift"}},
			},
		},
	}

	updated, captured, err := rt.captureFuzzCampaignNativeResults(campaign, runs)
	if err != nil {
		t.Fatalf("capture native results: %v", err)
	}
	if len(captured) != 2 || len(updated.NativeResults) != 2 {
		t.Fatalf("expected two native results, captured=%#v campaign=%#v", captured, updated.NativeResults)
	}
	if len(updated.Findings) != 1 {
		t.Fatalf("expected one deduplicated finding, got %#v", updated.Findings)
	}
	finding := updated.Findings[0]
	if finding.DedupKey == "" || finding.DuplicateCount == 0 {
		t.Fatalf("expected dedup metadata, got %#v", finding)
	}
	if len(finding.NativeResultKeys) != 2 || len(finding.EvidenceIDs) != 2 {
		t.Fatalf("expected merged native/evidence links, got %#v", finding)
	}
	if !strings.Contains(renderFuzzCampaign(updated), "duplicates=1") {
		t.Fatalf("expected rendered campaign to show duplicate count")
	}
}

func TestFuzzCampaignCapturesCoverageReportsAndFeedsGaps(t *testing.T) {
	root := t.TempDir()
	campaign, err := createFuzzCampaignFromWorkspace(root, "coverage campaign", AnalysisDocsManifest{
		RunID: "analysis-coverage",
		FuzzTargets: []AnalysisFuzzTargetCatalogEntry{
			{
				Name:         "ParsePacket",
				File:         "src/parser.cpp",
				SymbolID:     "func:ParsePacket",
				SourceAnchor: "src/parser.cpp:77",
			},
		},
	})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	runLog := filepath.Join(root, "fuzz-run-coverage", "run.log")
	if err := os.MkdirAll(filepath.Dir(runLog), 0o755); err != nil {
		t.Fatalf("mkdir run log: %v", err)
	}
	if err := os.WriteFile(runLog, []byte(`#2 INITED cov: 8 ft: 9 corp: 2/64b exec/s: 10 rss: 44Mb`), 0o644); err != nil {
		t.Fatalf("write run log: %v", err)
	}
	llvmPath := filepath.Join(campaign.CoverageDir, "coverage-fuzz-run-coverage.txt")
	if err := os.WriteFile(llvmPath, []byte("TOTAL 100 80 20.00%\n"), 0o644); err != nil {
		t.Fatalf("write coverage report: %v", err)
	}
	rt := &runtimeState{
		workspace: Workspace{Root: root},
		writer:    &bytes.Buffer{},
		ui:        NewUI(),
		evidence:  &EvidenceStore{Path: filepath.Join(root, "evidence.json")},
		session:   &Session{ID: "session-coverage"},
	}
	run := FunctionFuzzRun{
		ID:               "fuzz-run-coverage",
		Workspace:        root,
		TargetQuery:      "ParsePacket",
		TargetSymbolName: "ParsePacket",
		TargetSymbolID:   "func:ParsePacket",
		TargetFile:       "src/parser.cpp",
		TargetStartLine:  77,
		Execution: FunctionFuzzExecution{
			Status:     "completed",
			RunLogPath: runLog,
		},
	}

	updated, _, err := rt.captureFuzzCampaignNativeResults(campaign, []FunctionFuzzRun{run})
	if err != nil {
		t.Fatalf("capture native result: %v", err)
	}
	if len(updated.CoverageReports) < 2 {
		t.Fatalf("expected libFuzzer and llvm coverage reports, got %#v", updated.CoverageReports)
	}
	foundLLVMGap := false
	foundLibFuzzerGap := false
	for _, report := range updated.CoverageReports {
		if report.Format == "llvm-cov" && report.Gap && report.CoveragePercent == 20 {
			foundLLVMGap = true
		}
		if report.Format == "libfuzzer" && report.Gap && report.FeatureCount == 9 {
			foundLibFuzzerGap = true
		}
	}
	if !foundLLVMGap || !foundLibFuzzerGap {
		t.Fatalf("expected coverage report gaps, got %#v", updated.CoverageReports)
	}
	if len(updated.CoverageGaps) == 0 {
		t.Fatalf("expected coverage gaps from reports, got %#v", updated.CoverageGaps)
	}
	rendered := renderFuzzCampaign(updated)
	if !strings.Contains(rendered, "Coverage reports:") || !strings.Contains(rendered, "gap=true") {
		t.Fatalf("expected rendered coverage report gaps, got %q", rendered)
	}
}

func TestFuzzCampaignCapturesNativeVerifierAndSanitizerArtifacts(t *testing.T) {
	root := t.TempDir()
	campaign, err := createFuzzCampaignFromWorkspace(root, "artifact campaign", AnalysisDocsManifest{})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	runDir := filepath.Join(root, "fuzz-run-artifacts")
	crashDir := filepath.Join(runDir, "crashes")
	if err := os.MkdirAll(crashDir, 0o755); err != nil {
		t.Fatalf("mkdir crash dir: %v", err)
	}
	dumpPath := filepath.Join(crashDir, "target-crash.dmp")
	if err := os.WriteFile(dumpPath, []byte("mini dump"), 0o644); err != nil {
		t.Fatalf("write dump: %v", err)
	}
	runLog := filepath.Join(runDir, "run.log")
	logText := strings.Join([]string{
		"==123==ERROR: AddressSanitizer: heap-buffer-overflow on address 0x41414141",
		"VERIFIER STOP 0000000A: Application Verifier detected invalid handle use",
		"DRIVER_VERIFIER_DETECTED_VIOLATION (c4)",
	}, "\n")
	if err := os.WriteFile(runLog, []byte(logText), 0o644); err != nil {
		t.Fatalf("write run log: %v", err)
	}
	rt := &runtimeState{
		cfg:      Config{},
		writer:   &bytes.Buffer{},
		ui:       NewUI(),
		evidence: &EvidenceStore{Path: filepath.Join(root, "evidence.json")},
		session:  &Session{ID: "session-artifacts"},
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}
	run := FunctionFuzzRun{
		ID:               "fuzz-run-artifacts",
		Workspace:        root,
		TargetQuery:      "ValidatePacket",
		TargetSymbolName: "ValidatePacket",
		TargetFile:       "src/parser.cpp",
		TargetStartLine:  91,
		RiskScore:        40,
		Execution: FunctionFuzzExecution{
			Status:     "completed",
			CrashDir:   crashDir,
			RunLogPath: runLog,
			RunCommand: "fuzzer.exe corpus",
		},
	}

	updated, captured, err := rt.captureFuzzCampaignNativeResults(campaign, []FunctionFuzzRun{run})
	if err != nil {
		t.Fatalf("capture native result: %v", err)
	}
	if len(captured) != 1 || len(updated.NativeResults) != 1 {
		t.Fatalf("expected one native result, captured=%#v campaign=%#v", captured, updated.NativeResults)
	}
	result := updated.NativeResults[0]
	if len(result.ArtifactIDs) < 4 {
		t.Fatalf("expected sanitizer/verifier/dump artifacts on result, got %#v", result)
	}
	kinds := map[string]bool{}
	for _, artifact := range updated.RunArtifacts {
		kinds[artifact.Kind] = true
		if artifact.EvidenceID == "" {
			t.Fatalf("expected artifact evidence link, got %#v", artifact)
		}
	}
	for _, kind := range []string{"sanitizer_report", "application_verifier_report", "driver_verifier_report", "windows_crash_dump"} {
		if !kinds[kind] {
			t.Fatalf("expected artifact kind %s in %#v", kind, updated.RunArtifacts)
		}
	}
	if len(updated.Findings) == 0 || updated.Findings[0].VerificationGate != "required" || updated.Findings[0].Severity != "high" {
		t.Fatalf("expected artifact-backed required finding, got %#v", updated.Findings)
	}
	records, err := rt.evidence.Search("kind:fuzz_native_result", root, 10)
	if err != nil {
		t.Fatalf("search evidence: %v", err)
	}
	if len(records) != 1 || records[0].Attributes["artifact_ids"] == "" {
		t.Fatalf("expected evidence artifact ids, got %#v", records)
	}
	rendered := renderFuzzCampaign(updated)
	if !strings.Contains(rendered, "Run artifacts:") || !strings.Contains(rendered, "driver_verifier_report") {
		t.Fatalf("expected rendered run artifacts, got %q", rendered)
	}
	if len(updated.ArtifactGraph.Nodes) == 0 || !strings.Contains(fmt.Sprintf("%#v", updated.ArtifactGraph), "run_artifact") {
		t.Fatalf("expected run artifact graph nodes, got %#v", updated.ArtifactGraph)
	}
}

func TestHandleFuzzCampaignStatusRecommendsSingleRunCommand(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	campaignStore := &FuzzCampaignStore{Path: filepath.Join(root, "campaigns.json")}
	functionStore := &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")}
	if _, err := functionStore.Append(FunctionFuzzRun{
		ID:               "fuzz-run-status",
		Workspace:        root,
		TargetQuery:      "ValidateRequest",
		TargetSymbolName: "ValidateRequest",
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{Title: "boundary drift"},
		},
	}); err != nil {
		t.Fatalf("append function fuzz: %v", err)
	}
	rt := &runtimeState{
		cfg:           DefaultConfig(root),
		writer:        &output,
		ui:            NewUI(),
		fuzzCampaigns: campaignStore,
		functionFuzz:  functionStore,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleFuzzCampaignCommand(""); err != nil {
		t.Fatalf("show status: %v", err)
	}
	text := output.String()
	if !strings.Contains(text, "Suggested next step") || !strings.Contains(text, "Continue: /fuzz-campaign run") {
		t.Fatalf("expected single-command planner guidance, got %q", text)
	}
	if strings.Contains(text, "attach <campaign") || strings.Contains(text, "promote-seeds <campaign") {
		t.Fatalf("expected status to hide expert subcommands, got %q", text)
	}
}

// Canned sanitizer reports used by the crash-report parser tests. They are
// hermetic strings (no real fuzzer/compiler) shaped after real ASan/UBSan output.
const (
	cannedAsanHeapWrite = "" +
		"==12345==ERROR: AddressSanitizer: heap-buffer-overflow on address 0x602000000050 at pc 0x0001 bp 0x7ffd sp 0x7ffd\n" +
		"WRITE of size 4 at 0x602000000050 thread T0\n" +
		"    #0 0x4a1b2c in ParsePacketHeader src/parser.cpp:128:9\n" +
		"    #1 0x4a2000 in HandleRequest src/server.cpp:55:3\n" +
		"    #2 0x4a3000 in main src/main.cpp:12:1\n"

	cannedAsanHeapRead = "" +
		"==12345==ERROR: AddressSanitizer: heap-buffer-overflow on address 0x602000000050 at pc 0x0001 bp 0x7ffd sp 0x7ffd\n" +
		"READ of size 1 at 0x602000000050 thread T0\n" +
		"    #0 0x4a1b2c in ParsePacketHeader src/parser.cpp:128:9\n" +
		"    #1 0x4a2000 in HandleRequest src/server.cpp:55:3\n"

	cannedAsanUseAfterFree = "" +
		"==222==ERROR: AddressSanitizer: heap-use-after-free on address 0x603000000010 at pc 0x0002 bp 0x7ffd sp 0x7ffd\n" +
		"WRITE of size 8 at 0x603000000010 thread T0\n" +
		"    #0 0x4b1000 in ReleaseSession src/session.cpp:90:5\n" +
		"    #1 0x4b2000 in HandleRequest src/server.cpp:60:3\n"

	cannedUBSan = "" +
		"src/math.cpp:44:17: runtime error: signed integer overflow: 2147483647 + 1 cannot be represented in type 'int'\n" +
		"    #0 0x4c1000 in ComputeChecksum src/math.cpp:44:17\n" +
		"    #1 0x4c2000 in HandleRequest src/server.cpp:70:3\n"

	cannedSegvNull = "" +
		"==333==ERROR: AddressSanitizer: SEGV on unknown address 0x000000000000 (pc 0x0003 bp 0x7ffd sp 0x7ffd T0)\n" +
		"    #0 0x4d1000 in DerefConfig src/config.cpp:21:7\n" +
		"    #1 0x4d2000 in main src/main.cpp:15:1\n"
)

func TestParseFuzzCampaignCrashReportExtractsFacts(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		wantClass  string
		wantAccess string
		wantAddr   string
		wantParsed bool
		wantFrame0 string
	}{
		{
			name:       "heap overflow write",
			text:       cannedAsanHeapWrite,
			wantClass:  "heap-buffer-overflow",
			wantAccess: "WRITE",
			wantAddr:   "0x602000000050",
			wantParsed: true,
			wantFrame0: "ParsePacketHeader",
		},
		{
			name:       "use after free",
			text:       cannedAsanUseAfterFree,
			wantClass:  "use-after-free",
			wantAccess: "WRITE",
			wantAddr:   "0x603000000010",
			wantParsed: true,
			wantFrame0: "ReleaseSession",
		},
		{
			name:       "ubsan runtime error",
			text:       cannedUBSan,
			wantClass:  "ubsan-runtime-error",
			wantAccess: "",
			wantAddr:   "",
			wantParsed: true,
			wantFrame0: "ComputeChecksum",
		},
		{
			name:       "segv null deref",
			text:       cannedSegvNull,
			wantClass:  "segv",
			wantAccess: "",
			wantAddr:   "0x000000000000",
			wantParsed: true,
			wantFrame0: "DerefConfig",
		},
		{
			name:       "plain non-crash text",
			text:       "INFO: Seed corpus loaded, 12 files, no findings",
			wantParsed: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := parseFuzzCampaignCrashReport(tc.text)
			if report.Parsed != tc.wantParsed {
				t.Fatalf("parsed=%v want %v (report=%#v)", report.Parsed, tc.wantParsed, report)
			}
			if !tc.wantParsed {
				return
			}
			if report.Class != tc.wantClass {
				t.Fatalf("class=%q want %q", report.Class, tc.wantClass)
			}
			if report.Access != tc.wantAccess {
				t.Fatalf("access=%q want %q", report.Access, tc.wantAccess)
			}
			if report.Address != tc.wantAddr {
				t.Fatalf("address=%q want %q", report.Address, tc.wantAddr)
			}
			if len(report.Frames) == 0 || report.Frames[0] != tc.wantFrame0 {
				t.Fatalf("frame0=%v want %q", report.Frames, tc.wantFrame0)
			}
			// Normalized frames must not leak absolute addresses or line columns.
			for _, frame := range report.Frames {
				if strings.Contains(frame, "0x4") || strings.Contains(frame, ".cpp:") {
					t.Fatalf("frame not normalized: %q", frame)
				}
			}
		})
	}
}

func TestFuzzCampaignExploitabilityBandMapping(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantBand string
		wantSev  string
	}{
		{name: "heap write overflow", text: cannedAsanHeapWrite, wantBand: "EXPLOITABLE", wantSev: "critical"},
		{name: "heap read overflow", text: cannedAsanHeapRead, wantBand: "NOT_LIKELY", wantSev: "medium"},
		{name: "use after free write", text: cannedAsanUseAfterFree, wantBand: "EXPLOITABLE", wantSev: "critical"},
		{name: "ubsan", text: cannedUBSan, wantBand: "UNKNOWN", wantSev: "high"},
		{name: "segv null deref", text: cannedSegvNull, wantBand: "NOT_LIKELY", wantSev: "medium"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := parseFuzzCampaignCrashReport(tc.text)
			band := fuzzCampaignExploitabilityBand(report)
			if band != tc.wantBand {
				t.Fatalf("band=%q want %q", band, tc.wantBand)
			}
			if sev := fuzzCampaignExploitabilitySeverity(band); sev != tc.wantSev {
				t.Fatalf("severity=%q want %q", sev, tc.wantSev)
			}
		})
	}
	// Unparsed report stays at the conservative UNKNOWN band and high severity.
	if band := fuzzCampaignExploitabilityBand(FuzzCampaignCrashReport{}); band != "UNKNOWN" {
		t.Fatalf("unparsed band=%q want UNKNOWN", band)
	}
}

func TestFuzzCampaignCrashFingerprintFromReport(t *testing.T) {
	runA := FunctionFuzzRun{
		ID:               "run-a",
		TargetSymbolID:   "func:Alpha",
		TargetSymbolName: "Alpha",
		TargetFile:       "src/a.cpp",
		Execution:        FunctionFuzzExecution{CrashCount: 1},
	}
	runB := FunctionFuzzRun{
		ID:               "run-b",
		TargetSymbolID:   "func:Beta",
		TargetSymbolName: "Beta",
		TargetFile:       "src/b.cpp",
		Execution:        FunctionFuzzExecution{CrashCount: 1},
	}

	heapWrite := parseFuzzCampaignCrashReport(cannedAsanHeapWrite)
	uaf := parseFuzzCampaignCrashReport(cannedAsanUseAfterFree)

	// Distinct crash classes/frames in the same target produce distinct fingerprints.
	fpHeap := fuzzCampaignCrashFingerprintFromReport(runA, heapWrite)
	fpUAF := fuzzCampaignCrashFingerprintFromReport(runA, uaf)
	if fpHeap == "" || fpUAF == "" {
		t.Fatalf("expected non-empty fingerprints, got %q %q", fpHeap, fpUAF)
	}
	if fpHeap == fpUAF {
		t.Fatalf("expected distinct fingerprints for distinct crash classes, both=%q", fpHeap)
	}

	// The same report seen in different target functions shares a fingerprint
	// because the bucket is rooted in crash class + normalized frames.
	fpHeapInB := fuzzCampaignCrashFingerprintFromReport(runB, heapWrite)
	if fpHeap != fpHeapInB {
		t.Fatalf("expected shared fingerprint across targets, got %q vs %q", fpHeap, fpHeapInB)
	}

	// Unparsed text falls back to the target-identity fingerprint, which differs
	// between the two targets and matches the legacy helper.
	fallbackA := fuzzCampaignCrashFingerprintFromReport(runA, FuzzCampaignCrashReport{})
	fallbackB := fuzzCampaignCrashFingerprintFromReport(runB, FuzzCampaignCrashReport{})
	if fallbackA == fallbackB {
		t.Fatalf("expected distinct target-identity fingerprints, both=%q", fallbackA)
	}
	if fallbackA != fuzzCampaignCrashFingerprint(runA) {
		t.Fatalf("fallback fingerprint diverged from legacy helper: %q vs %q", fallbackA, fuzzCampaignCrashFingerprint(runA))
	}
	if !strings.HasPrefix(fallbackA, "ff-") || !strings.HasPrefix(fpHeap, "fc-") {
		t.Fatalf("expected ff- fallback and fc- report fingerprints, got %q %q", fallbackA, fpHeap)
	}
}

func TestParseFuzzCampaignRunArtifactsAttachExploitability(t *testing.T) {
	base := FuzzCampaignRunArtifact{RunID: "run-x"}
	artifacts := parseFuzzCampaignRunArtifactsFromText("last_output:run-x", cannedAsanHeapWrite, base)
	if len(artifacts) != 1 {
		t.Fatalf("expected one sanitizer artifact, got %#v", artifacts)
	}
	art := artifacts[0]
	if art.Kind != "sanitizer_report" {
		t.Fatalf("kind=%q want sanitizer_report", art.Kind)
	}
	if art.CrashClass != "heap-buffer-overflow" || art.CrashAccess != "WRITE" {
		t.Fatalf("expected parsed crash facts, got %#v", art)
	}
	if art.Exploitability != "EXPLOITABLE" || art.Severity != "critical" {
		t.Fatalf("expected EXPLOITABLE/critical, got %#v", art)
	}

	// Unparsed sanitizer-ish text keeps the conservative high severity and no band.
	plain := parseFuzzCampaignRunArtifactsFromText("last_output:run-y", "AddressSanitizer initialized; no crash recorded", base)
	if len(plain) != 1 {
		t.Fatalf("expected one artifact, got %#v", plain)
	}
	if plain[0].Severity != "high" {
		t.Fatalf("expected conservative high severity for unparsed report, got %#v", plain[0])
	}
	if plain[0].Exploitability != "" {
		t.Fatalf("expected no fabricated band for unparsed report, got %#v", plain[0])
	}
}

func TestFuzzCampaignSanitizerSignalCoversAlternativeProfiles(t *testing.T) {
	// Canned banner strings match what TSan/MSan/LSan actually print at the top of
	// a crash report; the selectable sanitizer profiles make these reachable.
	cases := []struct {
		name       string
		text       string
		wantSignal string
		wantClass  string
	}{
		{
			name:       "thread sanitizer data race",
			text:       "==1234==WARNING: ThreadSanitizer: data race (pid=1234)\n  Write of size 4 at 0x7b0400000010 by thread T1:",
			wantSignal: "thread_sanitizer",
			wantClass:  "data-race",
		},
		{
			name:       "memory sanitizer uninitialized value",
			text:       "==1234==WARNING: MemorySanitizer: use-of-uninitialized-value\n    #0 0x4a1b2c in parse src/parser.cpp:128:7",
			wantSignal: "memory_sanitizer",
			wantClass:  "use-of-uninitialized-value",
		},
		{
			name:       "leak sanitizer detected leaks",
			text:       "==1234==ERROR: LeakSanitizer: detected memory leaks\n\nDirect leak of 16 byte(s) in 1 object(s) allocated from:",
			wantSignal: "leak_sanitizer",
			wantClass:  "memory-leak",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fuzzCampaignSanitizerSignal(tc.text); got != tc.wantSignal {
				t.Fatalf("sanitizer signal = %q, want %q", got, tc.wantSignal)
			}
			// The detection gate must let these reports through to a sanitizer_report
			// artifact; before the profile work MSan fell through to the default and
			// was never emitted.
			base := FuzzCampaignRunArtifact{RunID: "run-san"}
			artifacts := parseFuzzCampaignRunArtifactsFromText("last_output:run-san", tc.text, base)
			if len(artifacts) != 1 {
				t.Fatalf("expected one sanitizer artifact for %s, got %#v", tc.name, artifacts)
			}
			art := artifacts[0]
			if art.Kind != "sanitizer_report" {
				t.Fatalf("kind=%q want sanitizer_report for %s", art.Kind, tc.name)
			}
			if art.Signal != tc.wantSignal {
				t.Fatalf("artifact signal = %q, want %q for %s", art.Signal, tc.wantSignal, tc.name)
			}
			if art.CrashClass != tc.wantClass {
				t.Fatalf("artifact crash class = %q, want %q for %s", art.CrashClass, tc.wantClass, tc.name)
			}
		})
	}
}

func TestParseFuzzCampaignDumpRecordsSymbolizationNote(t *testing.T) {
	base := FuzzCampaignRunArtifact{RunID: "run-dump"}
	art := parseFuzzCampaignRunArtifactFromCrashFile("crashes/target.dmp", base)
	if art.Kind != "windows_crash_dump" {
		t.Fatalf("kind=%q want windows_crash_dump", art.Kind)
	}
	if !strings.Contains(art.Summary, "needs symbolization") || !strings.Contains(art.Summary, "!analyze -v") {
		t.Fatalf("expected symbolization note, got %q", art.Summary)
	}
	if art.Exploitability != "UNKNOWN" {
		t.Fatalf("expected UNKNOWN band for unanalyzed dump, got %q", art.Exploitability)
	}
}

func TestFuzzCampaignFindingDedupKeyUsesParsedFingerprint(t *testing.T) {
	// Two findings in different functions that share a parsed crash fingerprint
	// and source anchor merge into one bucket via the dedup key.
	left := FuzzCampaignFinding{
		CrashFingerprint: "fc-deadbeef",
		SourceAnchor:     "src/parser.cpp:128",
	}
	right := FuzzCampaignFinding{
		CrashFingerprint: "fc-deadbeef",
		SourceAnchor:     "src/parser.cpp:128",
	}
	if fuzzCampaignFindingDedupKey(left) != fuzzCampaignFindingDedupKey(right) {
		t.Fatalf("expected identical dedup keys for shared fingerprint+anchor")
	}
	// A different fingerprint yields a different bucket even at the same anchor.
	other := FuzzCampaignFinding{
		CrashFingerprint: "fc-feedface",
		SourceAnchor:     "src/parser.cpp:128",
	}
	if fuzzCampaignFindingDedupKey(left) == fuzzCampaignFindingDedupKey(other) {
		t.Fatalf("expected distinct dedup keys for distinct fingerprints")
	}
}

func TestCaptureFuzzCampaignNativeResultBandsFromReport(t *testing.T) {
	root := t.TempDir()
	campaign, err := createFuzzCampaignFromWorkspace(root, "band campaign", AnalysisDocsManifest{})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	runDir := filepath.Join(root, "fuzz-run-band")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	runLog := filepath.Join(runDir, "run.log")
	if err := os.WriteFile(runLog, []byte(cannedAsanUseAfterFree), 0o644); err != nil {
		t.Fatalf("write run log: %v", err)
	}
	rt := &runtimeState{
		writer:   &bytes.Buffer{},
		ui:       NewUI(),
		evidence: &EvidenceStore{Path: filepath.Join(root, "evidence.json")},
		session:  &Session{ID: "session-band"},
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}
	run := FunctionFuzzRun{
		ID:               "fuzz-run-band",
		Workspace:        root,
		TargetQuery:      "ReleaseSession",
		TargetSymbolName: "ReleaseSession",
		TargetSymbolID:   "func:ReleaseSession",
		TargetFile:       "src/session.cpp",
		TargetStartLine:  90,
		RiskScore:        30,
		Execution: FunctionFuzzExecution{
			Status:     "completed",
			CrashCount: 1,
			RunLogPath: runLog,
			RunCommand: "fuzzer.exe corpus",
		},
	}

	updated, captured, err := rt.captureFuzzCampaignNativeResults(campaign, []FunctionFuzzRun{run})
	if err != nil {
		t.Fatalf("capture native result: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected one native result, got %#v", captured)
	}
	result := captured[0]
	if result.CrashClass != "use-after-free" || result.Exploitability != "EXPLOITABLE" {
		t.Fatalf("expected parsed UAF/EXPLOITABLE on result, got %#v", result)
	}
	if !strings.HasPrefix(result.CrashFingerprint, "fc-") {
		t.Fatalf("expected report-rooted fingerprint, got %q", result.CrashFingerprint)
	}
	if len(updated.Findings) == 0 {
		t.Fatalf("expected a native finding, got none")
	}
	finding := updated.Findings[0]
	if finding.Severity != "critical" || finding.Exploitability != "EXPLOITABLE" {
		t.Fatalf("expected critical/EXPLOITABLE finding, got %#v", finding)
	}
	records, err := rt.evidence.Search("kind:fuzz_native_result", root, 10)
	if err != nil {
		t.Fatalf("search evidence: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one evidence record, got %#v", records)
	}
	if records[0].Severity != "critical" || records[0].RiskScore < 95 {
		t.Fatalf("expected critical evidence with raised risk, got severity=%q risk=%d", records[0].Severity, records[0].RiskScore)
	}
	if records[0].Attributes["exploitability"] != "EXPLOITABLE" || records[0].Attributes["crash_class"] != "use-after-free" {
		t.Fatalf("expected crash facts on evidence, got %#v", records[0].Attributes)
	}
}
