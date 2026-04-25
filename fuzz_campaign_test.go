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
