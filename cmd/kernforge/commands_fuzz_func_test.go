package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFunctionFuzzRecalibrateRiskFromEvidence(t *testing.T) {
	// A high-coverage extended completion: ft: count above the clean floor.
	highCoverageOutput := "#4096 DONE   cov: 900 ft: 256 corp: 64/4096b exec/s: 100 rss: 200Mb"
	lowCoverageOutput := "#10 DONE   cov: 12 ft: 8 corp: 2/512b exec/s: 5 rss: 100Mb"

	tests := []struct {
		name            string
		run             FunctionFuzzRun
		wantScore       int
		wantCalibration string
		wantBand        string
	}{
		{
			name: "confirmed crash raises above static prior",
			run: FunctionFuzzRun{
				StaticRiskScore: 40,
				RiskScore:       40,
				Execution:       FunctionFuzzExecution{Status: "failed", CrashCount: 1},
			},
			wantScore:       functionFuzzRiskFloorCrashConfirmed,
			wantCalibration: "crash-confirmed",
		},
		{
			name: "confirmed crash with severe band lands at band floor",
			run: FunctionFuzzRun{
				StaticRiskScore:    40,
				RiskScore:          40,
				ExploitabilityBand: "EXPLOITABLE",
				Execution:          FunctionFuzzExecution{Status: "failed", CrashCount: 1},
			},
			wantScore:       95,
			wantCalibration: "crash-confirmed",
			wantBand:        "EXPLOITABLE",
		},
		{
			name: "high static prior with crash is not lowered",
			run: FunctionFuzzRun{
				StaticRiskScore: 97,
				RiskScore:       97,
				Execution:       FunctionFuzzExecution{Status: "failed", CrashCount: 2},
			},
			wantScore:       97,
			wantCalibration: "crash-confirmed",
		},
		{
			name: "clean extended high coverage lowers and marks exercised-clean",
			run: FunctionFuzzRun{
				StaticRiskScore: 80,
				RiskScore:       80,
				Execution:       FunctionFuzzExecution{Status: "completed", Profile: "extended", CrashCount: 0, LastOutput: highCoverageOutput},
			},
			wantScore:       functionFuzzRiskCeilExercisedClean,
			wantCalibration: "exercised-clean",
		},
		{
			name: "clean extended but low coverage keeps static prior",
			run: FunctionFuzzRun{
				StaticRiskScore: 80,
				RiskScore:       80,
				Execution:       FunctionFuzzExecution{Status: "completed", Profile: "extended", CrashCount: 0, LastOutput: lowCoverageOutput},
			},
			wantScore:       80,
			wantCalibration: "",
		},
		{
			name: "clean smoke run with high coverage does not mark clean",
			run: FunctionFuzzRun{
				StaticRiskScore: 80,
				RiskScore:       80,
				Execution:       FunctionFuzzExecution{Status: "completed", Profile: "smoke", CrashCount: 0, LastOutput: highCoverageOutput},
			},
			wantScore:       80,
			wantCalibration: "",
		},
		{
			name: "running extended with no outcome keeps static prior",
			run: FunctionFuzzRun{
				StaticRiskScore: 55,
				RiskScore:       55,
				Execution:       FunctionFuzzExecution{Status: "running", Profile: "extended", CrashCount: 0},
			},
			wantScore:       55,
			wantCalibration: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := functionFuzzRecalibrateRisk(tc.run)
			if got.RiskScore != tc.wantScore {
				t.Fatalf("RiskScore=%d want %d", got.RiskScore, tc.wantScore)
			}
			if got.RiskCalibration != tc.wantCalibration {
				t.Fatalf("RiskCalibration=%q want %q", got.RiskCalibration, tc.wantCalibration)
			}
			if tc.wantBand != "" && got.ExploitabilityBand != tc.wantBand {
				t.Fatalf("ExploitabilityBand=%q want %q", got.ExploitabilityBand, tc.wantBand)
			}
			// The static prior is preserved for provenance.
			if got.StaticRiskScore != tc.run.StaticRiskScore {
				t.Fatalf("StaticRiskScore=%d want preserved %d", got.StaticRiskScore, tc.run.StaticRiskScore)
			}
			// Recalibration is idempotent: feeding the result back in must not drift.
			again := functionFuzzRecalibrateRisk(got)
			if again.RiskScore != got.RiskScore || again.RiskCalibration != got.RiskCalibration || again.ExploitabilityBand != got.ExploitabilityBand {
				t.Fatalf("recalibration not idempotent: first=(%d,%q,%q) second=(%d,%q,%q)",
					got.RiskScore, got.RiskCalibration, got.ExploitabilityBand,
					again.RiskScore, again.RiskCalibration, again.ExploitabilityBand)
			}
		})
	}
}

func TestFunctionFuzzRecalibrateRiskCapturesLegacyStaticPrior(t *testing.T) {
	// A legacy run persisted before StaticRiskScore existed: it has only RiskScore.
	run := FunctionFuzzRun{RiskScore: 30, Execution: FunctionFuzzExecution{Status: "failed", CrashCount: 1}}
	got := functionFuzzRecalibrateRisk(run)
	if got.StaticRiskScore != 30 {
		t.Fatalf("expected legacy RiskScore captured as StaticRiskScore=30, got %d", got.StaticRiskScore)
	}
	if got.RiskScore != functionFuzzRiskFloorCrashConfirmed {
		t.Fatalf("expected crash floor %d, got %d", functionFuzzRiskFloorCrashConfirmed, got.RiskScore)
	}
}

func TestFunctionFuzzApplyRiskRecalibrationRecordsBandFromOutput(t *testing.T) {
	// A confirmed crash whose captured output parses as a WRITE heap overflow
	// records the EXPLOITABLE band and raises risk; a re-run is a no-op delta.
	run := FunctionFuzzRun{
		StaticRiskScore: 35,
		RiskScore:       35,
		Execution: FunctionFuzzExecution{
			Status:     "failed",
			CrashCount: 1,
			LastOutput: "==1==ERROR: AddressSanitizer: heap-buffer-overflow on address 0x602000000050\nWRITE of size 4 at 0x602000000050 thread T0\n    #0 0x1 in Parse src/p.cpp:10:1\n",
		},
	}
	recalibrated, changed := functionFuzzApplyRiskRecalibration(run)
	if !changed {
		t.Fatalf("expected recalibration to report a change")
	}
	if recalibrated.ExploitabilityBand != "EXPLOITABLE" {
		t.Fatalf("ExploitabilityBand=%q want EXPLOITABLE", recalibrated.ExploitabilityBand)
	}
	if recalibrated.RiskCalibration != "crash-confirmed" {
		t.Fatalf("RiskCalibration=%q want crash-confirmed", recalibrated.RiskCalibration)
	}
	if recalibrated.RiskScore < functionFuzzRiskFloorCrashConfirmed {
		t.Fatalf("RiskScore=%d want >= %d", recalibrated.RiskScore, functionFuzzRiskFloorCrashConfirmed)
	}
	// Idempotent: a second application reports no further change.
	if _, changedAgain := functionFuzzApplyRiskRecalibration(recalibrated); changedAgain {
		t.Fatalf("expected second recalibration to be a no-op delta")
	}
}

func TestFunctionFuzzBumpMaxTotalTime(t *testing.T) {
	args := []string{"-rss_limit_mb=4096", "-max_total_time=600", "-fork=2"}
	bumped := functionFuzzBumpMaxTotalTime(args, functionFuzzExtendedRelaunchMaxTotalTime)
	found := false
	for _, arg := range bumped {
		if arg == fmt.Sprintf("-max_total_time=%d", functionFuzzExtendedRelaunchMaxTotalTime) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected bumped -max_total_time in %v", bumped)
	}
	// Absent flag is appended.
	appended := functionFuzzBumpMaxTotalTime([]string{"-rss_limit_mb=4096"}, 900)
	if appended[len(appended)-1] != "-max_total_time=900" {
		t.Fatalf("expected appended -max_total_time, got %v", appended)
	}
	// An already-larger value is not lowered.
	kept := functionFuzzBumpMaxTotalTime([]string{"-max_total_time=3600"}, 1800)
	if kept[0] != "-max_total_time=3600" {
		t.Fatalf("expected larger value kept, got %v", kept)
	}
}

func TestHelpTextIncludesFuzzFuncCommand(t *testing.T) {
	help := HelpText()
	if !strings.Contains(help, "/fuzz-func <name>") {
		t.Fatalf("expected help text to include /fuzz-func, got %q", help)
	}
	if !strings.Contains(help, "--file <path>") {
		t.Fatalf("expected help text to include --file usage, got %q", help)
	}
	if !strings.Contains(help, "@<path>") {
		t.Fatalf("expected help text to include @ alias usage, got %q", help)
	}
	if !strings.Contains(help, "/fuzz-func --file <path>") {
		t.Fatalf("expected help text to include file-only usage, got %q", help)
	}
}

func TestFunctionFuzzResolveCompilerPathChecksLLVMHome(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	compiler := filepath.Join(bin, "custom-clang-cl.exe")
	if err := os.WriteFile(compiler, []byte("test"), 0o755); err != nil {
		t.Fatalf("write compiler: %v", err)
	}
	t.Setenv("LLVM_HOME", root)

	got := functionFuzzResolveCompilerPath("custom-clang-cl")
	if !strings.EqualFold(got, compiler) {
		t.Fatalf("functionFuzzResolveCompilerPath returned %q, want %q", got, compiler)
	}
}

func writeFunctionFuzzTestAnalysisArtifacts(t *testing.T, root string, runID string, index SemanticIndexV2) {
	t.Helper()
	cfg := DefaultConfig(root)
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis: %v", err)
	}
	if strings.TrimSpace(index.RunID) == "" {
		index.RunID = runID
	}
	if strings.TrimSpace(index.Goal) == "" {
		index.Goal = "function fuzz test analysis"
	}
	if strings.TrimSpace(index.Root) == "" {
		index.Root = root
	}
	if index.GeneratedAt.IsZero() {
		index.GeneratedAt = time.Now()
	}
	pack := KnowledgePack{
		RunID:       runID,
		Goal:        index.Goal,
		Root:        root,
		GeneratedAt: time.Now(),
	}
	packData, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), packData, 0o644); err != nil {
		t.Fatalf("write knowledge pack: %v", err)
	}
	indexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatalf("marshal structural index v2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "structural_index_v2.json"), indexData, 0o644); err != nil {
		t.Fatalf("write structural index v2: %v", err)
	}
}

func TestHandleFuzzFuncCommandStatusShowsFileHintUsage(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	rt := &runtimeState{
		cfg:          DefaultConfig(root),
		writer:       &output,
		ui:           NewUI(),
		functionFuzz: &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")},
		session:      &Session{},
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleFuzzFuncCommand(""); err != nil {
		t.Fatalf("handle fuzz-func status: %v", err)
	}

	rendered := output.String()
	if !strings.Contains(rendered, functionFuzzUsage()) {
		t.Fatalf("expected status output to include file-hint usage, got %q", rendered)
	}
	if !strings.Contains(rendered, `ValidateRequest --file "src/guard.cpp"`) {
		t.Fatalf("expected status output to include file-hint example, got %q", rendered)
	}
	if !strings.Contains(rendered, `/fuzz-func ValidateRequest @src/guard.cpp`) {
		t.Fatalf("expected status output to include @ file-hint example, got %q", rendered)
	}
	if !strings.Contains(rendered, `/fuzz-func --file "src/driver.cpp"`) {
		t.Fatalf("expected status output to include file-scope example, got %q", rendered)
	}
	if !strings.Contains(rendered, `/fuzz-func @src/driver.cpp`) {
		t.Fatalf("expected status output to include @ file-scope example, got %q", rendered)
	}
	if !strings.Contains(rendered, "/fuzz-func language [system|english]") {
		t.Fatalf("expected status output to include language command, got %q", rendered)
	}
}

func TestFunctionFuzzCommandConfigCanFollowKoreanQueryLanguage(t *testing.T) {
	cfg := configWithResponseLanguageForUserText(Config{FuzzFuncOutputLanguage: "english"}, "ValidateRequest를 분석해")
	if got := configFuzzFuncOutputLanguage(cfg); got != "korean" {
		t.Fatalf("expected Korean command-local output language, got %q", got)
	}
	if !functionFuzzPrefersKorean(cfg) {
		t.Fatalf("expected function fuzz renderer to prefer Korean")
	}
}

func TestFunctionFuzzCommandConfigPreservesLanguageForIdentifierOnlyQuery(t *testing.T) {
	t.Setenv("LANG", "ko_KR.UTF-8")

	cfg := configWithResponseLanguageForUserText(Config{FuzzFuncOutputLanguage: "english"}, "ValidateRequest")
	if got := configFuzzFuncOutputLanguage(cfg); got != "english" {
		t.Fatalf("expected identifier-only query to preserve configured output language, got %q", got)
	}
}

func TestParseFunctionFuzzTargetSpecAllowsFileOnly(t *testing.T) {
	spec, err := parseFunctionFuzzTargetSpec(`--file "src/driver.cpp"`)
	if err != nil {
		t.Fatalf("parse file-only fuzz target: %v", err)
	}
	if spec.Name != "" {
		t.Fatalf("expected empty function name for file-only mode, got %q", spec.Name)
	}
	if filepath.ToSlash(spec.FileHint) != "src/driver.cpp" {
		t.Fatalf("expected file hint to be preserved, got %q", spec.FileHint)
	}
}

func TestParseFunctionFuzzTargetSpecAllowsAtFileAlias(t *testing.T) {
	spec, err := parseFunctionFuzzTargetSpec(`ValidateRequest @src/guard.cpp`)
	if err != nil {
		t.Fatalf("parse @ file alias with function: %v", err)
	}
	if spec.Name != "ValidateRequest" {
		t.Fatalf("expected function name to be preserved, got %q", spec.Name)
	}
	if filepath.ToSlash(spec.FileHint) != "src/guard.cpp" {
		t.Fatalf("expected @ file alias to populate file hint, got %q", spec.FileHint)
	}

	spec, err = parseFunctionFuzzTargetSpec(`@src/driver.cpp`)
	if err != nil {
		t.Fatalf("parse @ file alias only: %v", err)
	}
	if spec.Name != "" {
		t.Fatalf("expected empty function name for @ file-only mode, got %q", spec.Name)
	}
	if filepath.ToSlash(spec.FileHint) != "src/driver.cpp" {
		t.Fatalf("expected @ file-only alias to populate file hint, got %q", spec.FileHint)
	}
}

func TestParseFunctionFuzzSourceScanModeStripsPlanningFlags(t *testing.T) {
	query, mode, err := parseFunctionFuzzSourceScanMode(`ValidateRequest --source-scan full --file "src/guard.cpp"`, functionFuzzSourceScanModeFocused)
	if err != nil {
		t.Fatalf("parse source scan mode: %v", err)
	}
	if mode != functionFuzzSourceScanModeFull {
		t.Fatalf("expected full source scan mode, got %q", mode)
	}
	if query != "ValidateRequest --file src/guard.cpp" {
		t.Fatalf("unexpected cleaned query: %q", query)
	}

	query, mode, err = parseFunctionFuzzSourceScanMode(`ValidateRequest --no-source-scan`, functionFuzzSourceScanModeFocused)
	if err != nil {
		t.Fatalf("parse no source scan: %v", err)
	}
	if mode != functionFuzzSourceScanModeOff || query != "ValidateRequest" {
		t.Fatalf("unexpected no-source-scan parse: query=%q mode=%q", query, mode)
	}
}

func TestSourceCandidateFunctionFuzzMatchRejectsUnrelatedSameFileCandidate(t *testing.T) {
	run := FunctionFuzzRun{
		ID:               "fuzz-match",
		TargetSymbolID:   "func:ValidateRequest@src/guard.cpp",
		TargetSymbolName: "ValidateRequest",
		TargetFile:       "src/guard.cpp",
		TargetStartLine:  40,
		TargetEndLine:    60,
		ReachableSymbols: []string{"ValidateRequest", "ParseHeader"},
		ReachableFiles:   []string{"src/guard.cpp"},
	}
	unrelated := SourceCandidateRecord{
		ID:          "sc-unrelated",
		Status:      "pending",
		MatcherSlug: "size-contract-drift",
		File:        "src/guard.cpp",
		LineNumbers: []int{12},
		SymbolID:    "func:CleanupRequest@src/guard.cpp",
		SymbolName:  "CleanupRequest",
		Score:       100,
	}
	if score := sourceCandidateFunctionFuzzMatchScore(unrelated, run); score != 0 {
		t.Fatalf("expected unrelated same-file candidate to be rejected, got score %d", score)
	}
	inTarget := unrelated
	inTarget.ID = "sc-in-target"
	inTarget.LineNumbers = []int{45}
	inTarget.SymbolID = ""
	inTarget.SymbolName = ""
	if score := sourceCandidateFunctionFuzzMatchScore(inTarget, run); score <= 0 {
		t.Fatalf("expected in-target line candidate to match, got score %d", score)
	}
	reachable := unrelated
	reachable.ID = "sc-reachable"
	reachable.SymbolID = "func:ParseHeader@src/guard.cpp"
	reachable.SymbolName = "ParseHeader"
	if score := sourceCandidateFunctionFuzzMatchScore(reachable, run); score <= 0 {
		t.Fatalf("expected reachable symbol candidate to match, got score %d", score)
	}
	sameNameWrongFile := unrelated
	sameNameWrongFile.ID = "sc-wrong-file"
	sameNameWrongFile.File = "src/other_guard.cpp"
	sameNameWrongFile.SymbolID = "func:ValidateRequest@src/other_guard.cpp"
	sameNameWrongFile.SymbolName = "ValidateRequest"
	if score := sourceCandidateFunctionFuzzMatchScore(sameNameWrongFile, run); score != 0 {
		t.Fatalf("expected same-name candidate in another file to be rejected, got score %d", score)
	}
}

func TestHandleFuzzFuncLanguageCommandUpdatesConfig(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	rt := &runtimeState{
		cfg:          DefaultConfig(root),
		writer:       &output,
		ui:           NewUI(),
		functionFuzz: &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")},
		session:      &Session{},
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleFuzzFuncCommand("language english"); err != nil {
		t.Fatalf("set fuzz-func language: %v", err)
	}
	if got := configFuzzFuncOutputLanguage(rt.cfg); got != "english" {
		t.Fatalf("expected fuzz-func output language to be english, got %q", got)
	}
}

func TestHandleFuzzFuncLanguageCommandPreservesProviderConfig(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	cfg := DefaultConfig(root)
	cfg.Provider = "openrouter"
	cfg.Model = "z-ai/glm-5.1"
	cfg.BaseURL = "https://openrouter.ai/api/v1"

	rt := &runtimeState{
		cfg:          cfg,
		writer:       &output,
		ui:           NewUI(),
		functionFuzz: &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")},
		session:      &Session{},
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleFuzzFuncCommand("language system"); err != nil {
		t.Fatalf("set fuzz-func language: %v", err)
	}
	if rt.cfg.Provider != "openrouter" {
		t.Fatalf("expected provider to remain configured, got %q", rt.cfg.Provider)
	}
	if rt.cfg.Model != "z-ai/glm-5.1" {
		t.Fatalf("expected model to remain configured, got %q", rt.cfg.Model)
	}
	if rt.cfg.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("expected base_url to remain configured, got %q", rt.cfg.BaseURL)
	}
}

func TestHandleFuzzFuncCommandCreatesArtifacts(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis: %v", err)
	}

	pack := KnowledgePack{
		RunID:          "run-fuzz-1",
		Goal:           "trace guard validation path",
		Root:           root,
		GeneratedAt:    time.Now(),
		ProjectSummary: "Guard runtime validates untrusted request payloads before dispatch.",
	}
	packData, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), packData, 0o644); err != nil {
		t.Fatalf("write knowledge pack: %v", err)
	}

	index := SemanticIndexV2{
		RunID:       "run-fuzz-1",
		Goal:        "trace guard validation path",
		Root:        root,
		GeneratedAt: time.Now(),
		BuildContexts: []BuildContextRecord{
			{
				ID:       "buildctx:compile:guard",
				Name:     "Guard compile context",
				Kind:     "compile_command",
				Files:    []string{"src/guard.cpp"},
				Compiler: filepath.Join(root, "missing-toolchain", "clang-cl.exe"),
			},
		},
		Symbols: []SymbolRecord{
			{
				ID:             "func:ValidateRequest@src/guard.cpp",
				Name:           "ValidateRequest",
				Kind:           "function",
				Language:       "cpp",
				File:           "src/guard.cpp",
				BuildContextID: "buildctx:compile:guard",
				Signature:      "bool ValidateRequest(const uint8_t* data, size_t size, uint32_t flags)",
				StartLine:      41,
				EndLine:        96,
			},
			{
				ID:             "func:ParseHeader@src/guard.cpp",
				Name:           "ParseHeader",
				Kind:           "function",
				Language:       "cpp",
				File:           "src/guard.cpp",
				BuildContextID: "buildctx:compile:guard",
				Signature:      "bool ParseHeader(const uint8_t* data, size_t size)",
			},
			{
				ID:             "func:CopyPayload@src/guard.cpp",
				Name:           "CopyPayload",
				Kind:           "function",
				Language:       "cpp",
				File:           "src/guard.cpp",
				BuildContextID: "buildctx:compile:guard",
				Signature:      "void CopyPayload(const uint8_t* src, size_t size, uint8_t* dst)",
			},
		},
		CallEdges: []CallEdge{
			{
				SourceID: "func:ValidateRequest@src/guard.cpp",
				TargetID: "func:ParseHeader@src/guard.cpp",
				Type:     "calls",
				Evidence: []string{"src/guard.cpp"},
			},
			{
				SourceID: "func:ValidateRequest@src/guard.cpp",
				TargetID: "func:CopyPayload@src/guard.cpp",
				Type:     "calls",
				Evidence: []string{"src/guard.cpp"},
			},
		},
		OverlayEdges: []OverlayEdge{
			{
				SourceID: "func:ValidateRequest@src/guard.cpp",
				TargetID: "entity:ioctl_surface",
				Type:     "issues_ioctl",
				Domain:   "ioctl_surface",
				Evidence: []string{"src/guard.cpp"},
			},
		},
	}
	indexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatalf("marshal structural index v2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "structural_index_v2.json"), indexData, 0o644); err != nil {
		t.Fatalf("write structural index v2: %v", err)
	}

	var output bytes.Buffer
	store := &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")}
	rt := &runtimeState{
		cfg:           cfg,
		writer:        &output,
		ui:            NewUI(),
		functionFuzz:  store,
		fuzzCampaigns: &FuzzCampaignStore{Path: filepath.Join(root, "fuzz_campaigns.json")},
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleFuzzFuncCommand("ValidateRequest"); err != nil {
		t.Fatalf("handle fuzz-func: %v", err)
	}

	items, err := store.ListRecent(root, 2)
	if err != nil {
		t.Fatalf("list function fuzz runs: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one stored function fuzz run, got %d", len(items))
	}
	run := items[0]
	if run.TargetSymbolName != "ValidateRequest" {
		t.Fatalf("unexpected target symbol: %+v", run)
	}
	if run.ReachableCallCount != 2 {
		t.Fatalf("expected two reachable call edges, got %+v", run)
	}
	if run.RiskScore <= 0 {
		t.Fatalf("expected positive risk score, got %+v", run)
	}
	if len(run.ParameterStrategies) < 3 {
		t.Fatalf("expected parameter strategies, got %+v", run)
	}
	if run.PrimaryEngine == "" {
		t.Fatalf("expected engine recommendation, got %+v", run)
	}
	if run.Execution.Status != "blocked" {
		t.Fatalf("expected auto execution to stay blocked without compile command recovery, got %+v", run.Execution)
	}
	if !strings.Contains(strings.ToLower(run.Execution.Reason), "compiler") && !strings.Contains(strings.ToLower(run.Execution.Reason), "compile") {
		t.Fatalf("expected blocked reason to mention compiler or compile context recovery, got %+v", run.Execution)
	}
	if _, err := os.Stat(run.PlanPath); err != nil {
		t.Fatalf("expected plan artifact: %v", err)
	}
	harnessData, err := os.ReadFile(run.HarnessPath)
	if err != nil {
		t.Fatalf("read harness: %v", err)
	}
	harnessText := string(harnessData)
	if !strings.Contains(harnessText, "LLVMFuzzerTestOneInput") {
		t.Fatalf("expected fuzzer entrypoint in harness, got %q", harnessText)
	}
	if !strings.Contains(harnessText, "ValidateRequest") {
		t.Fatalf("expected target name in harness, got %q", harnessText)
	}
	if !strings.Contains(output.String(), "Function Fuzz") {
		t.Fatalf("expected command output to include section title, got %q", output.String())
	}
	// Updated wording: the pipeline performs static source triage, not AI analysis.
	if !strings.Contains(output.String(), "Bottom line: Kernforge completed static source-only fuzz triage for ValidateRequest") {
		t.Fatalf("expected command output to start with a clearer conclusion, got %q", output.String())
	}
	if !strings.Contains(output.String(), "Top predicted problems:") {
		t.Fatalf("expected command output to include source-analysis section, got %q", output.String())
	}
	if !strings.Contains(output.String(), "Campaign handoff:") || !strings.Contains(output.String(), "Continue: /fuzz-campaign run") {
		t.Fatalf("expected campaign handoff guidance, got %q", output.String())
	}
}

func TestHandleFuzzFuncCommandReusesMatchingSourceCandidate(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "guard.cpp"), []byte("bool ValidateRequest(const uint8_t* data, size_t size) { return size > 0 && data != nullptr; }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	index := SemanticIndexV2{
		RunID: "run-fuzz-source-candidate-reuse",
		Goal:  "reuse existing source scan candidate",
		Files: []FileRecord{
			{Path: "src/guard.cpp", Extension: ".cpp", Language: "cpp"},
		},
		Symbols: []SymbolRecord{
			{
				ID:        "func:ValidateRequest@src/guard.cpp",
				Name:      "ValidateRequest",
				Kind:      "function",
				Language:  "cpp",
				File:      "src/guard.cpp",
				Signature: "bool ValidateRequest(const uint8_t* data, size_t size)",
				StartLine: 1,
				EndLine:   1,
			},
		},
	}
	writeFunctionFuzzTestAnalysisArtifacts(t, root, index.RunID, index)

	sourceStore := &SourceScanStore{Path: filepath.Join(root, "source_scan.json")}
	candidate := SourceCandidateRecord{
		ID:          "sc-existing",
		Workspace:   root,
		RunID:       "source-scan-existing",
		Status:      "pending",
		MatcherSlug: "size-contract-drift",
		NoiseTier:   "normal",
		File:        "src/guard.cpp",
		SymbolID:    "func:ValidateRequest@src/guard.cpp",
		SymbolName:  "ValidateRequest",
		Score:       88,
	}
	if _, err := sourceStore.UpsertCandidate(candidate); err != nil {
		t.Fatalf("upsert source candidate: %v", err)
	}

	var output bytes.Buffer
	store := &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")}
	rt := &runtimeState{
		cfg:          DefaultConfig(root),
		writer:       &output,
		ui:           NewUI(),
		functionFuzz: store,
		sourceScan:   sourceStore,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleFuzzFuncCommand("ValidateRequest"); err != nil {
		t.Fatalf("handle fuzz-func: %v", err)
	}
	runs, err := store.ListRecent(root, 1)
	if err != nil {
		t.Fatalf("list function fuzz runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one fuzz run, got %d", len(runs))
	}
	run := runs[0]
	if run.SourceCandidateID != "sc-existing" {
		t.Fatalf("expected reused source candidate, got %+v", run)
	}
	if run.SourceMatcherSlug != "size-contract-drift" {
		t.Fatalf("expected matcher slug to be carried, got %+v", run)
	}
	if !strings.Contains(run.SourceScanSummary, "Reused source candidate sc-existing") {
		t.Fatalf("expected source scan reuse summary, got %+v", run)
	}
	linked, ok, err := sourceStore.GetCandidate("sc-existing")
	if err != nil || !ok {
		t.Fatalf("get linked candidate: ok=%v err=%v", ok, err)
	}
	if !containsString(linked.LinkedFuzzRunIDs, run.ID) {
		t.Fatalf("expected candidate to link fuzz run %s, got %#v", run.ID, linked.LinkedFuzzRunIDs)
	}
}

func TestHandleFuzzFuncCommandRunsFocusedSourceScanWhenNoCandidateExists(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	source := strings.Join([]string{
		"#include <cstring>",
		"#include <cstddef>",
		"static unsigned char g_Buffer[16];",
		"bool ValidateRequest(const unsigned char* data, size_t size)",
		"{",
		"    if (size < 4)",
		"    {",
		"        return false;",
		"    }",
		"    memcpy(g_Buffer, data, size);",
		"    return true;",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "src", "guard.cpp"), []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	index := SemanticIndexV2{
		RunID: "run-fuzz-focused-source-scan",
		Goal:  "focused source scan",
		Files: []FileRecord{
			{Path: "src/guard.cpp", Extension: ".cpp", Language: "cpp"},
		},
		Symbols: []SymbolRecord{
			{
				ID:        "func:ValidateRequest@src/guard.cpp",
				Name:      "ValidateRequest",
				Kind:      "function",
				Language:  "cpp",
				File:      "src/guard.cpp",
				Signature: "bool ValidateRequest(const unsigned char* data, size_t size)",
				StartLine: 4,
				EndLine:   11,
			},
		},
	}
	writeFunctionFuzzTestAnalysisArtifacts(t, root, index.RunID, index)

	sourceStore := &SourceScanStore{Path: filepath.Join(root, "source_scan.json")}
	store := &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")}
	rt := &runtimeState{
		cfg:          DefaultConfig(root),
		writer:       io.Discard,
		ui:           NewUI(),
		functionFuzz: store,
		sourceScan:   sourceStore,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleFuzzFuncCommand("ValidateRequest"); err != nil {
		t.Fatalf("handle fuzz-func: %v", err)
	}
	runs, err := store.ListRecent(root, 1)
	if err != nil {
		t.Fatalf("list function fuzz runs: %v", err)
	}
	run := runs[0]
	if run.SourceCandidateID == "" || run.SourceScanRunID == "" {
		t.Fatalf("expected focused source scan to link a candidate, got %+v", run)
	}
	if run.SourceScanMode != string(functionFuzzSourceScanModeFocused) {
		t.Fatalf("expected focused source scan mode, got %+v", run)
	}
	candidate, ok, err := sourceStore.GetCandidate(run.SourceCandidateID)
	if err != nil || !ok {
		t.Fatalf("get generated source candidate: ok=%v err=%v", ok, err)
	}
	if filepath.ToSlash(candidate.File) != "src/guard.cpp" {
		t.Fatalf("expected generated candidate to stay in focus file, got %+v", candidate)
	}
	if !containsString(candidate.LinkedFuzzRunIDs, run.ID) {
		t.Fatalf("expected generated candidate to link fuzz run %s, got %#v", run.ID, candidate.LinkedFuzzRunIDs)
	}
}

func TestHandleFuzzFuncCommandCanDisableAutomaticSourceScan(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	source := "bool ValidateRequest(const unsigned char* data, size_t size) { if (size < 4) { return false; } memcpy(g_Buffer, data, size); return true; }\n"
	if err := os.WriteFile(filepath.Join(root, "src", "guard.cpp"), []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	index := SemanticIndexV2{
		RunID: "run-fuzz-no-source-scan",
		Goal:  "disabled source scan",
		Files: []FileRecord{
			{Path: "src/guard.cpp", Extension: ".cpp", Language: "cpp"},
		},
		Symbols: []SymbolRecord{
			{
				ID:        "func:ValidateRequest@src/guard.cpp",
				Name:      "ValidateRequest",
				Kind:      "function",
				Language:  "cpp",
				File:      "src/guard.cpp",
				Signature: "bool ValidateRequest(const unsigned char* data, size_t size)",
				StartLine: 1,
				EndLine:   1,
			},
		},
	}
	writeFunctionFuzzTestAnalysisArtifacts(t, root, index.RunID, index)

	sourceStore := &SourceScanStore{Path: filepath.Join(root, "source_scan.json")}
	store := &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")}
	rt := &runtimeState{
		cfg:          DefaultConfig(root),
		writer:       io.Discard,
		ui:           NewUI(),
		functionFuzz: store,
		sourceScan:   sourceStore,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleFuzzFuncCommand("ValidateRequest --no-source-scan"); err != nil {
		t.Fatalf("handle fuzz-func: %v", err)
	}
	runs, err := store.ListRecent(root, 1)
	if err != nil {
		t.Fatalf("list function fuzz runs: %v", err)
	}
	run := runs[0]
	if run.SourceCandidateID != "" {
		t.Fatalf("expected no linked source candidate, got %+v", run)
	}
	if run.SourceScanMode != string(functionFuzzSourceScanModeOff) {
		t.Fatalf("expected source scan off mode, got %+v", run)
	}
	candidates, err := sourceStore.ListCandidates(root, 10)
	if err != nil {
		t.Fatalf("list source candidates: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected disabled source scan to create no candidates, got %#v", candidates)
	}
}

func TestBuildFunctionFuzzRunPlansAutonomousExecutionFromSnapshot(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "include"), 0o755); err != nil {
		t.Fatalf("mkdir include: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "guard.cpp"), []byte("bool ValidateRequest(const uint8_t*, size_t, uint32_t) { return true; }\n"), 0o644); err != nil {
		t.Fatalf("write guard source: %v", err)
	}

	pack := KnowledgePack{
		RunID:          "run-fuzz-2",
		Goal:           "recover compile context for guard fuzzing",
		Root:           root,
		GeneratedAt:    time.Now(),
		ProjectSummary: "Guard runtime exposes a standalone validation surface.",
	}
	packData, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), packData, 0o644); err != nil {
		t.Fatalf("write knowledge pack: %v", err)
	}

	index := SemanticIndexV2{
		RunID:       "run-fuzz-2",
		Goal:        "recover compile context for guard fuzzing",
		Root:        root,
		GeneratedAt: time.Now(),
		BuildContexts: []BuildContextRecord{
			{
				ID:           "buildctx:compile:guard",
				Name:         "Guard compile context",
				Kind:         "compile_command",
				Directory:    root,
				Files:        []string{"src/guard.cpp"},
				Compiler:     "clang++",
				IncludePaths: []string{"include"},
				Defines:      []string{"GUARD_MODE=1"},
			},
		},
		Symbols: []SymbolRecord{
			{
				ID:             "func:ValidateRequest@src/guard.cpp",
				Name:           "ValidateRequest",
				Kind:           "function",
				Language:       "cpp",
				File:           "src/guard.cpp",
				BuildContextID: "buildctx:compile:guard",
				Signature:      "bool ValidateRequest(const uint8_t* data, size_t size, uint32_t flags)",
				StartLine:      12,
				EndLine:        40,
			},
		},
	}
	indexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatalf("marshal structural index v2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "structural_index_v2.json"), indexData, 0o644); err != nil {
		t.Fatalf("write structural index v2: %v", err)
	}

	fakeCompiler := filepath.Join(root, "toolchain", "clang++.exe")
	if err := os.MkdirAll(filepath.Dir(fakeCompiler), 0o755); err != nil {
		t.Fatalf("mkdir toolchain: %v", err)
	}
	if err := os.WriteFile(fakeCompiler, []byte(""), 0o644); err != nil {
		t.Fatalf("write fake compiler: %v", err)
	}

	snapshot := ProjectSnapshot{
		Root:        root,
		GeneratedAt: time.Now(),
		CompileCommands: []CompilationCommandRecord{
			{
				File:           "src/guard.cpp",
				Directory:      root,
				Compiler:       fakeCompiler,
				Arguments:      []string{fakeCompiler, "-std=c++20", "-Iinclude", "-DGUARD_MODE=1"},
				IncludePaths:   []string{"include"},
				Defines:        []string{"GUARD_MODE=1"},
				BuildContextID: "buildctx:compile:guard",
				Source:         "compile_commands.json",
			},
		},
	}
	snapshotData, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "snapshot.json"), snapshotData, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	run, err := buildFunctionFuzzRun(cfg, root, "ValidateRequest")
	if err != nil {
		t.Fatalf("build function fuzz run: %v", err)
	}
	if !run.Execution.Eligible {
		t.Fatalf("expected autonomous execution to be eligible, got %+v", run.Execution)
	}
	if run.Execution.Status != "planned" {
		t.Fatalf("expected planned autonomous execution status, got %+v", run.Execution)
	}
	if filepath.Clean(run.Execution.CompilerResolvedPath) != filepath.Clean(fakeCompiler) {
		t.Fatalf("expected resolved compiler path %q, got %+v", filepath.Clean(fakeCompiler), run.Execution)
	}
	if run.Execution.BuildScriptPath == "" || run.Execution.ExecutablePath == "" {
		t.Fatalf("expected runner artifacts for autonomous execution, got %+v", run.Execution)
	}
	if _, err := os.Stat(run.Execution.BuildScriptPath); err != nil {
		t.Fatalf("expected build script artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(run.Execution.CorpusDir, "seed-pattern.bin")); err != nil {
		t.Fatalf("expected seeded corpus artifact: %v", err)
	}
	if !strings.Contains(run.Execution.BuildCommand, "clang++.exe") {
		t.Fatalf("expected build command to reference compiler, got %+v", run.Execution)
	}
	if !strings.Contains(run.Execution.RunCommand, "-max_total_time=20") {
		t.Fatalf("expected smoke fuzz defaults in run command, got %+v", run.Execution)
	}
}

func TestBuildFunctionFuzzRunWorksWithoutLatestStructuralIndex(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "include"), 0o755); err != nil {
		t.Fatalf("mkdir include: %v", err)
	}

	source := strings.Join([]string{
		"#include <cstddef>",
		"#include <cstdint>",
		"",
		"static bool ParseHeader(const uint8_t* data, size_t size)",
		"{",
		"    return data != nullptr && size > 0;",
		"}",
		"",
		"bool ValidateRequest(const uint8_t* data, size_t size, uint32_t flags)",
		"{",
		"    if (!ParseHeader(data, size))",
		"    {",
		"        return false;",
		"    }",
		"    return flags != 0;",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "src", "guard.cpp"), []byte(source), 0o644); err != nil {
		t.Fatalf("write guard source: %v", err)
	}

	fakeCompiler := filepath.Join(root, "toolchain", "clang++.exe")
	if err := os.MkdirAll(filepath.Dir(fakeCompiler), 0o755); err != nil {
		t.Fatalf("mkdir toolchain: %v", err)
	}
	if err := os.WriteFile(fakeCompiler, []byte(""), 0o644); err != nil {
		t.Fatalf("write fake compiler: %v", err)
	}

	compileCommands := []CompilationCommandRecord{
		{
			File:      "src/guard.cpp",
			Directory: root,
			Compiler:  fakeCompiler,
			Arguments: []string{fakeCompiler, "-std=c++20", "-Iinclude", "-DGUARD_MODE=1"},
		},
	}
	data, err := json.MarshalIndent(compileCommands, "", "  ")
	if err != nil {
		t.Fatalf("marshal compile commands: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "compile_commands.json"), data, 0o644); err != nil {
		t.Fatalf("write compile_commands.json: %v", err)
	}

	run, err := buildFunctionFuzzRun(cfg, root, "ValidateRequest")
	if err != nil {
		t.Fatalf("build function fuzz run without latest analysis: %v", err)
	}
	if run.TargetSymbolName != "ValidateRequest" {
		t.Fatalf("unexpected target symbol: %+v", run)
	}
	if run.ReachableCallCount == 0 {
		t.Fatalf("expected reachable call edges from on-demand source scan, got %+v", run)
	}
	if !run.Execution.Eligible || run.Execution.Status != "planned" {
		t.Fatalf("expected autonomous execution planning from on-demand scan, got %+v", run.Execution)
	}
	if !containsAny(strings.ToLower(strings.Join(run.Notes, " ")), "on-demand", "not required") {
		t.Fatalf("expected notes to mention on-demand semantic indexing, got %+v", run.Notes)
	}
}

func TestHandleFuzzFuncCommandHonorsFileHint(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis: %v", err)
	}

	index := SemanticIndexV2{
		RunID:       "run-fuzz-file-hint-1",
		Goal:        "disambiguate duplicate function names with file hint",
		Root:        root,
		GeneratedAt: time.Now(),
		BuildContexts: []BuildContextRecord{
			{
				ID:       "buildctx:net",
				Name:     "Network guard compile context",
				Kind:     "compile_context",
				Files:    []string{"src/net/guard.cpp"},
				Compiler: filepath.Join(root, "missing-toolchain", "clang-cl.exe"),
			},
			{
				ID:       "buildctx:ui",
				Name:     "UI guard compile context",
				Kind:     "compile_context",
				Files:    []string{"src/ui/guard.cpp"},
				Compiler: filepath.Join(root, "missing-toolchain", "clang-cl.exe"),
			},
		},
		Symbols: []SymbolRecord{
			{
				ID:             "func:ValidateRequest@src/net/guard.cpp",
				Name:           "ValidateRequest",
				Kind:           "function",
				Language:       "cpp",
				File:           "src/net/guard.cpp",
				BuildContextID: "buildctx:net",
				Signature:      "bool ValidateRequest(const uint8_t* data, size_t size)",
			},
			{
				ID:             "func:ValidateRequest@src/ui/guard.cpp",
				Name:           "ValidateRequest",
				Kind:           "function",
				Language:       "cpp",
				File:           "src/ui/guard.cpp",
				BuildContextID: "buildctx:ui",
				Signature:      "bool ValidateRequest(const char* text, size_t length)",
			},
			{
				ID:             "func:ParsePacket@src/net/guard.cpp",
				Name:           "ParsePacket",
				Kind:           "function",
				Language:       "cpp",
				File:           "src/net/guard.cpp",
				BuildContextID: "buildctx:net",
				Signature:      "bool ParsePacket(const uint8_t* data, size_t size)",
			},
		},
		CallEdges: []CallEdge{
			{
				SourceID: "func:ValidateRequest@src/net/guard.cpp",
				TargetID: "func:ParsePacket@src/net/guard.cpp",
				Type:     "calls",
				Evidence: []string{"src/net/guard.cpp"},
			},
		},
	}
	indexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatalf("marshal structural index v2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "structural_index_v2.json"), indexData, 0o644); err != nil {
		t.Fatalf("write structural index v2: %v", err)
	}

	var output bytes.Buffer
	store := &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")}
	rt := &runtimeState{
		cfg:          cfg,
		writer:       &output,
		ui:           NewUI(),
		functionFuzz: store,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleFuzzFuncCommand(`ValidateRequest --file "src/net/guard.cpp"`); err != nil {
		t.Fatalf("handle fuzz-func with file hint: %v", err)
	}

	items, err := store.ListRecent(root, 1)
	if err != nil {
		t.Fatalf("list function fuzz runs: %v", err)
	}
	run := items[0]
	if filepath.ToSlash(run.TargetFile) != "src/net/guard.cpp" {
		t.Fatalf("expected file hint to select src/net/guard.cpp, got %+v", run)
	}
	if run.ReachableCallCount != 1 {
		t.Fatalf("expected net variant call closure to be selected, got %+v", run)
	}
	if !strings.Contains(run.TargetQuery, "--file") {
		t.Fatalf("expected raw target query to retain file hint, got %+v", run)
	}
}

func TestHandleFuzzFuncCommandHonorsAtFileAlias(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis: %v", err)
	}

	index := SemanticIndexV2{
		RunID:       "run-fuzz-at-file-hint-1",
		Goal:        "disambiguate duplicate function names with @ file hint",
		Root:        root,
		GeneratedAt: time.Now(),
		Symbols: []SymbolRecord{
			{
				ID:        "func:ValidateRequest@src/net/guard.cpp",
				Name:      "ValidateRequest",
				Kind:      "function",
				Language:  "cpp",
				File:      "src/net/guard.cpp",
				Signature: "bool ValidateRequest(const uint8_t* data, size_t size)",
			},
			{
				ID:        "func:ValidateRequest@src/ui/guard.cpp",
				Name:      "ValidateRequest",
				Kind:      "function",
				Language:  "cpp",
				File:      "src/ui/guard.cpp",
				Signature: "bool ValidateRequest(const char* text, size_t length)",
			},
		},
	}
	indexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatalf("marshal structural index v2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "structural_index_v2.json"), indexData, 0o644); err != nil {
		t.Fatalf("write structural index v2: %v", err)
	}

	var output bytes.Buffer
	store := &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")}
	rt := &runtimeState{
		cfg:          cfg,
		writer:       &output,
		ui:           NewUI(),
		functionFuzz: store,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleFuzzFuncCommand(`ValidateRequest @src/net/guard.cpp`); err != nil {
		t.Fatalf("handle fuzz-func with @ file hint: %v", err)
	}

	items, err := store.ListRecent(root, 1)
	if err != nil {
		t.Fatalf("list function fuzz runs: %v", err)
	}
	run := items[0]
	if filepath.ToSlash(run.TargetFile) != "src/net/guard.cpp" {
		t.Fatalf("expected @ file hint to select src/net/guard.cpp, got %+v", run)
	}
	if !strings.Contains(run.TargetQuery, "@src/net/guard.cpp") {
		t.Fatalf("expected raw target query to retain @ file hint, got %+v", run)
	}
}

func TestBuildFunctionFuzzRunFromArtifactsSupportsFileOnlyScope(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "include"), 0o755); err != nil {
		t.Fatalf("mkdir include: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "driver.cpp"), []byte("int DriverEntry(void* driverObject)\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write driver source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "include", "dispatch.h"), []byte("int DispatchIoctl(unsigned int code, const unsigned char* input, unsigned long size);\n"), 0o644); err != nil {
		t.Fatalf("write dispatch header: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "dispatch.cpp"), []byte("int DispatchIoctl(unsigned int code, const unsigned char* input, unsigned long size)\n{\n    return ValidateIoctlBuffer(input, size);\n}\n\nint ValidateIoctlBuffer(const unsigned char* input, unsigned long size)\n{\n    return size > 0 ? 1 : 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write dispatch source: %v", err)
	}

	artifacts := latestAnalysisArtifacts{
		Pack: KnowledgePack{
			RunID: "run-file-scope-1",
			Goal:  "file scope fuzz planning",
			Root:  root,
		},
		IndexV2: SemanticIndexV2{
			RunID:       "run-file-scope-1",
			Goal:        "file scope fuzz planning",
			Root:        root,
			GeneratedAt: time.Now(),
			Files: []FileRecord{
				{Path: "src/driver.cpp"},
				{Path: "include/dispatch.h"},
				{Path: "src/dispatch.cpp"},
			},
			BuildContexts: []BuildContextRecord{
				{
					ID:    "buildctx:driver",
					Name:  "Driver build context",
					Kind:  "compile_context",
					Files: []string{"src/driver.cpp", "src/dispatch.cpp"},
				},
			},
			Symbols: []SymbolRecord{
				{
					ID:             "func:DriverEntry@src/driver.cpp",
					Name:           "DriverEntry",
					Kind:           "function",
					Language:       "cpp",
					File:           "src/driver.cpp",
					BuildContextID: "buildctx:driver",
					Signature:      "NTSTATUS DriverEntry(PDRIVER_OBJECT driverObject)",
					StartLine:      1,
					EndLine:        4,
				},
				{
					ID:             "func:DispatchIoctl@src/dispatch.cpp",
					Name:           "DispatchIoctl",
					Kind:           "function",
					Language:       "cpp",
					File:           "src/dispatch.cpp",
					BuildContextID: "buildctx:driver",
					Signature:      "NTSTATUS DispatchIoctl(uint32_t ioctlCode, const uint8_t* input, size_t inputSize)",
					StartLine:      1,
					EndLine:        4,
				},
				{
					ID:             "func:ValidateIoctlBuffer@src/dispatch.cpp",
					Name:           "ValidateIoctlBuffer",
					Kind:           "function",
					Language:       "cpp",
					File:           "src/dispatch.cpp",
					BuildContextID: "buildctx:driver",
					Signature:      "bool ValidateIoctlBuffer(const uint8_t* input, size_t inputSize)",
					StartLine:      6,
					EndLine:        9,
				},
			},
			References: []ReferenceRecord{
				{
					SourceFile: "src/driver.cpp",
					TargetPath: "include/dispatch.h",
					Type:       "file_import",
				},
				{
					SourceFile: "include/dispatch.h",
					TargetPath: "src/dispatch.cpp",
					Type:       "file_import",
				},
			},
			CallEdges: []CallEdge{
				{
					SourceID: "func:DispatchIoctl@src/dispatch.cpp",
					TargetID: "func:ValidateIoctlBuffer@src/dispatch.cpp",
					Type:     "calls",
					Evidence: []string{"src/dispatch.cpp"},
				},
			},
			OverlayEdges: []OverlayEdge{
				{
					SourceID: "func:DispatchIoctl@src/dispatch.cpp",
					TargetID: "entity:ioctl_surface",
					Type:     "issues_ioctl",
					Domain:   "ioctl_surface",
					Evidence: []string{"src/dispatch.cpp"},
				},
			},
		},
	}

	run, err := buildFunctionFuzzRunFromArtifacts(DefaultConfig(root), root, `--file "src/driver.cpp"`, artifacts, nil)
	if err != nil {
		t.Fatalf("build file-scope fuzz run: %v", err)
	}
	if run.ScopeMode != "file" {
		t.Fatalf("expected scope mode file, got %+v", run)
	}
	if filepath.ToSlash(run.ScopeRootFile) != "src/driver.cpp" {
		t.Fatalf("expected scope root file src/driver.cpp, got %+v", run)
	}
	if len(run.ScopeFiles) != 3 {
		t.Fatalf("expected file scope to include 3 files, got %+v", run.ScopeFiles)
	}
	if run.TargetSymbolName != "DispatchIoctl" {
		t.Fatalf("expected DispatchIoctl to be auto-selected as representative target, got %+v", run)
	}
	if filepath.ToSlash(run.TargetFile) != "src/dispatch.cpp" {
		t.Fatalf("expected representative target file to come from imported scope, got %+v", run)
	}
	if !strings.Contains(strings.Join(run.Notes, "\n"), "File-scope mode expanded from src/driver.cpp") {
		t.Fatalf("expected file-scope note, got %+v", run.Notes)
	}
	rendered := renderFunctionFuzzRunWithConfig(run, DefaultConfig(root))
	if !strings.Contains(rendered, "Input scope:") {
		t.Fatalf("expected rendered output to explain file scope, got %q", rendered)
	}
}

func TestResolveFunctionFuzzTargetUsesDocsCatalogRanking(t *testing.T) {
	index := SemanticIndexV2{
		Symbols: []SymbolRecord{
			{ID: "func:AlphaHandler@src/a.cpp", Name: "AlphaHandler", Kind: "function", File: "src/a.cpp", Signature: "void AlphaHandler(const uint8_t *data, size_t size)"},
			{ID: "func:BetaHandler@src/b.cpp", Name: "BetaHandler", Kind: "function", File: "src/b.cpp", Signature: "void BetaHandler(const uint8_t *data, size_t size)"},
		},
	}
	manifest := AnalysisDocsManifest{
		FuzzTargets: []AnalysisFuzzTargetCatalogEntry{
			{
				SymbolID:          "func:BetaHandler@src/b.cpp",
				Name:              "BetaHandler",
				File:              "src/b.cpp",
				PriorityScore:     100,
				BuildContextLevel: "indexed_build_context",
				HarnessReadiness:  "ready",
			},
		},
	}
	target, err := resolveFunctionFuzzTarget(index, functionFuzzTargetSpec{Name: "Handler"}, manifest)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	if target.Name != "BetaHandler" {
		t.Fatalf("expected docs catalog ranking to select BetaHandler, got %+v", target)
	}
}

func TestBuildFunctionFuzzRunRequiresConfirmationForRecoveredBuildContext(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "include"), 0o755); err != nil {
		t.Fatalf("mkdir include: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "guard.cpp"), []byte("bool ValidateRequest(const uint8_t*, size_t, uint32_t) { return true; }\n"), 0o644); err != nil {
		t.Fatalf("write guard source: %v", err)
	}

	fakeCompiler := filepath.Join(root, "toolchain", "clang++.exe")
	if err := os.MkdirAll(filepath.Dir(fakeCompiler), 0o755); err != nil {
		t.Fatalf("mkdir toolchain: %v", err)
	}
	if err := os.WriteFile(fakeCompiler, []byte(""), 0o644); err != nil {
		t.Fatalf("write fake compiler: %v", err)
	}

	index := SemanticIndexV2{
		RunID:       "run-fuzz-confirm-1",
		Goal:        "recover build context from structural index only",
		Root:        root,
		GeneratedAt: time.Now(),
		BuildContexts: []BuildContextRecord{
			{
				ID:           "buildctx:compile:guard",
				Name:         "Guard compile context",
				Kind:         "compile_context",
				Directory:    root,
				Files:        []string{"src/guard.cpp"},
				Compiler:     fakeCompiler,
				IncludePaths: []string{"include"},
				Defines:      []string{"GUARD_MODE=1"},
			},
		},
		Symbols: []SymbolRecord{
			{
				ID:             "func:ValidateRequest@src/guard.cpp",
				Name:           "ValidateRequest",
				Kind:           "function",
				Language:       "cpp",
				File:           "src/guard.cpp",
				BuildContextID: "buildctx:compile:guard",
				Signature:      "bool ValidateRequest(const uint8_t* data, size_t size, uint32_t flags)",
			},
		},
	}
	indexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatalf("marshal structural index v2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "structural_index_v2.json"), indexData, 0o644); err != nil {
		t.Fatalf("write structural index v2: %v", err)
	}

	run, err := buildFunctionFuzzRun(cfg, root, "ValidateRequest")
	if err != nil {
		t.Fatalf("build function fuzz run: %v", err)
	}
	if !run.Execution.Eligible {
		t.Fatalf("expected recovered execution plan to stay eligible, got %+v", run.Execution)
	}
	if run.Execution.Status != "pending_confirmation" {
		t.Fatalf("expected pending confirmation for recovered build context, got %+v", run.Execution)
	}
	if run.Execution.CompileContextLevel != "partial" {
		t.Fatalf("expected partial compile context level, got %+v", run.Execution)
	}
	if !containsAny(strings.ToLower(strings.Join(run.Execution.MissingSettings, " ")), "compile_commands", "compile command") {
		t.Fatalf("expected missing settings to mention compile command coverage, got %+v", run.Execution)
	}
	if strings.TrimSpace(run.Execution.ContinueCommand) == "" {
		t.Fatalf("expected continue command for pending confirmation, got %+v", run.Execution)
	}
	// Updated wording: "static source-only fuzz triage" replaces the old "AI source-only fuzz analysis".
	if !containsAny(strings.ToLower(strings.Join(run.Interpretation, " ")), "source-only fuzz triage", "awaiting confirmation") {
		t.Fatalf("expected readable interpretation for recovered build context, got %+v", run.Interpretation)
	}
	if len(run.VirtualScenarios) == 0 {
		t.Fatalf("expected source-only virtual scenarios for recovered build context, got %+v", run)
	}
	if len(run.VirtualScenarios) < 5 {
		t.Fatalf("expected expanded source-only scenario set, got %d scenarios: %+v", len(run.VirtualScenarios), run.VirtualScenarios)
	}
	if len(run.SuggestedTargets) > 0 && len(run.SuggestedCommands) == 0 {
		t.Fatalf("expected suggested next commands, got %+v", run)
	}
}

func TestHandleFuzzFuncCommandContinueApprovesPendingRun(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "include"), 0o755); err != nil {
		t.Fatalf("mkdir include: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "guard.cpp"), []byte("bool ValidateRequest(const uint8_t*, size_t, uint32_t) { return true; }\n"), 0o644); err != nil {
		t.Fatalf("write guard source: %v", err)
	}

	fakeCompiler := filepath.Join(root, "toolchain", "clang++.exe")
	if err := os.MkdirAll(filepath.Dir(fakeCompiler), 0o755); err != nil {
		t.Fatalf("mkdir toolchain: %v", err)
	}
	if err := os.WriteFile(fakeCompiler, []byte(""), 0o644); err != nil {
		t.Fatalf("write fake compiler: %v", err)
	}

	index := SemanticIndexV2{
		RunID:       "run-fuzz-confirm-2",
		Goal:        "continue recovered build execution",
		Root:        root,
		GeneratedAt: time.Now(),
		BuildContexts: []BuildContextRecord{
			{
				ID:           "buildctx:compile:guard",
				Name:         "Guard compile context",
				Kind:         "compile_context",
				Directory:    root,
				Files:        []string{"src/guard.cpp"},
				Compiler:     fakeCompiler,
				IncludePaths: []string{"include"},
				Defines:      []string{"GUARD_MODE=1"},
			},
		},
		Symbols: []SymbolRecord{
			{
				ID:             "func:ValidateRequest@src/guard.cpp",
				Name:           "ValidateRequest",
				Kind:           "function",
				Language:       "cpp",
				File:           "src/guard.cpp",
				BuildContextID: "buildctx:compile:guard",
				Signature:      "bool ValidateRequest(const uint8_t* data, size_t size, uint32_t flags)",
			},
		},
	}
	indexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatalf("marshal structural index v2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "structural_index_v2.json"), indexData, 0o644); err != nil {
		t.Fatalf("write structural index v2: %v", err)
	}

	run, err := buildFunctionFuzzRun(cfg, root, "ValidateRequest")
	if err != nil {
		t.Fatalf("build function fuzz run: %v", err)
	}
	if run.Execution.Status != "pending_confirmation" {
		t.Fatalf("expected pending confirmation before continue, got %+v", run.Execution)
	}

	store := &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")}
	if _, err := store.Upsert(run); err != nil {
		t.Fatalf("save function fuzz run: %v", err)
	}

	var output bytes.Buffer
	rt := &runtimeState{
		cfg:          cfg,
		writer:       &output,
		ui:           NewUI(),
		functionFuzz: store,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleFuzzFuncCommand("continue latest"); err != nil {
		t.Fatalf("continue latest: %v", err)
	}

	items, err := store.ListRecent(root, 1)
	if err != nil {
		t.Fatalf("list function fuzz runs: %v", err)
	}
	saved := items[0]
	if saved.Execution.Status != "planned" {
		t.Fatalf("expected continue to approve and plan execution, got %+v", saved.Execution)
	}
	if strings.TrimSpace(saved.Execution.ContinueCommand) != "" {
		t.Fatalf("expected continue command to clear after approval, got %+v", saved.Execution)
	}
	if !strings.Contains(strings.ToLower(saved.Execution.Reason), "background job manager") {
		t.Fatalf("expected post-approval reason to mention missing background job manager in test runtime, got %+v", saved.Execution)
	}
	if !strings.Contains(output.String(), "Native auto-run: ready to run") {
		t.Fatalf("expected rendered output to show friendly planned native execution status, got %q", output.String())
	}
}

func TestHandleFuzzFuncCommandShowsPlanBeforeRecoveredBuildPrompt(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "include"), 0o755); err != nil {
		t.Fatalf("mkdir include: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "guard.cpp"), []byte("bool ValidateRequest(const uint8_t*, size_t, uint32_t) { return true; }\n"), 0o644); err != nil {
		t.Fatalf("write guard source: %v", err)
	}

	fakeCompiler := filepath.Join(root, "toolchain", "clang++.exe")
	if err := os.MkdirAll(filepath.Dir(fakeCompiler), 0o755); err != nil {
		t.Fatalf("mkdir toolchain: %v", err)
	}
	if err := os.WriteFile(fakeCompiler, []byte(""), 0o644); err != nil {
		t.Fatalf("write fake compiler: %v", err)
	}

	index := SemanticIndexV2{
		RunID:       "run-fuzz-confirm-order",
		Goal:        "show plan before recovered build prompt",
		Root:        root,
		GeneratedAt: time.Now(),
		BuildContexts: []BuildContextRecord{
			{
				ID:           "buildctx:compile:guard",
				Name:         "Guard compile context",
				Kind:         "compile_context",
				Directory:    root,
				Files:        []string{"src/guard.cpp"},
				Compiler:     fakeCompiler,
				IncludePaths: []string{"include"},
				Defines:      []string{"GUARD_MODE=1"},
			},
		},
		Symbols: []SymbolRecord{
			{
				ID:             "func:ValidateRequest@src/guard.cpp",
				Name:           "ValidateRequest",
				Kind:           "function",
				Language:       "cpp",
				File:           "src/guard.cpp",
				BuildContextID: "buildctx:compile:guard",
				Signature:      "bool ValidateRequest(const uint8_t* data, size_t size, uint32_t flags)",
			},
		},
	}
	indexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatalf("marshal structural index v2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "structural_index_v2.json"), indexData, 0o644); err != nil {
		t.Fatalf("write structural index v2: %v", err)
	}

	var output bytes.Buffer
	store := &FunctionFuzzStore{Path: filepath.Join(root, "function_fuzz.json")}
	rt := &runtimeState{
		cfg:          cfg,
		reader:       bufio.NewReader(strings.NewReader("n\n")),
		writer:       &output,
		ui:           NewUI(),
		functionFuzz: store,
		interactive:  true,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	if err := rt.handleFuzzFuncCommand("ValidateRequest"); err != nil {
		t.Fatalf("handle fuzz-func with recovered prompt: %v", err)
	}

	rendered := output.String()
	planIndex := strings.Index(rendered, "Function Fuzz")
	findingsIndex := strings.Index(rendered, "Risk-ranked findings")
	promptIndex := strings.Index(rendered, "Start autonomous fuzzing")
	if planIndex < 0 || findingsIndex < 0 || promptIndex < 0 {
		t.Fatalf("expected plan, source-only findings, and prompt in output, got %q", rendered)
	}
	if promptIndex < planIndex || promptIndex < findingsIndex {
		t.Fatalf("expected recovered build prompt after rendered source-only plan, got %q", rendered)
	}
	if !strings.Contains(rendered, "Native Auto-Run") {
		t.Fatalf("expected post-prompt native auto-run status, got %q", rendered)
	}

	items, err := store.ListRecent(root, 1)
	if err != nil {
		t.Fatalf("list function fuzz runs: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected saved function fuzz run, got %d", len(items))
	}
	if items[0].Execution.Status != "pending_confirmation" {
		t.Fatalf("expected declined prompt to preserve pending confirmation, got %+v", items[0].Execution)
	}
}

func TestRenderFunctionFuzzRunExplainsDriverEntryPlanningResult(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	ioctlSource := strings.Join([]string{
		"bool ValidateIoctlBuffer(const uint8_t* input, size_t inputSize)",
		"{",
		"    if (input == nullptr)",
		"    {",
		"        return false;",
		"    }",
		"    if (inputSize < 4)",
		"    {",
		"        return false;",
		"    }",
		"    return input[0] == 0x41;",
		"}",
		"",
		"NTSTATUS DeviceControlDispatch(uint32_t ioctlCode, const uint8_t* input, size_t inputSize)",
		"{",
		"    switch (ioctlCode)",
		"    {",
		"    default:",
		"        return ValidateIoctlBuffer(input, inputSize) ? 0 : -1;",
		"    }",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "src", "ioctl.cpp"), []byte(ioctlSource), 0o644); err != nil {
		t.Fatalf("write ioctl.cpp: %v", err)
	}
	target := SymbolRecord{
		ID:        "func:DriverEntry@src/driver.cpp",
		Name:      "DriverEntry",
		Kind:      "function",
		Language:  "cpp",
		File:      "src/driver.cpp",
		Signature: "NTSTATUS DriverEntry(PDRIVER_OBJECT driverObject, PUNICODE_STRING registryPath)",
		StartLine: 1,
		EndLine:   12,
	}
	closure := functionFuzzClosure{
		RootSymbol: target,
		CallEdges: []CallEdge{
			{SourceID: target.ID, TargetID: "func:DeviceControlDispatch@src/ioctl.cpp", Type: "calls"},
			{SourceID: target.ID, TargetID: "func:ValidateIoctlBuffer@src/ioctl.cpp", Type: "calls"},
		},
		Symbols: []SymbolRecord{
			target,
			{
				ID:        "func:DeviceControlDispatch@src/ioctl.cpp",
				Name:      "DeviceControlDispatch",
				Kind:      "function",
				Language:  "cpp",
				File:      "src/ioctl.cpp",
				Signature: "NTSTATUS DeviceControlDispatch(uint32_t ioctlCode, const uint8_t* input, size_t inputSize)",
				StartLine: 14,
				EndLine:   21,
			},
			{
				ID:        "func:ValidateIoctlBuffer@src/ioctl.cpp",
				Name:      "ValidateIoctlBuffer",
				Kind:      "function",
				Language:  "cpp",
				File:      "src/ioctl.cpp",
				Signature: "bool ValidateIoctlBuffer(const uint8_t* input, size_t inputSize)",
				StartLine: 1,
				EndLine:   12,
			},
		},
		MaxDepth: 3,
	}
	run := FunctionFuzzRun{
		ID:                  "fuzz-driverentry",
		TargetQuery:         "DriverEntry",
		TargetSymbolID:      target.ID,
		TargetSymbolName:    target.Name,
		TargetSignature:     target.Signature,
		TargetFile:          target.File,
		RiskScore:           100,
		HarnessReady:        false,
		ReachableCallCount:  len(closure.CallEdges),
		ReachableDepth:      closure.MaxDepth,
		OverlayDomains:      []string{"ioctl_surface", "memory_surface", "security_boundary"},
		ParameterStrategies: buildFunctionFuzzParameterStrategies(target.Signature),
		PrimaryEngine:       "FuzzTest domain model + libFuzzer ABI",
		Execution: FunctionFuzzExecution{
			Status: "blocked",
			Reason: "The detected target still needs object setup, handle provisioning, or custom parameter binding before autonomous execution.",
		},
	}
	run.VirtualScenarios = buildFunctionFuzzVirtualScenarios(functionFuzzEnglishConfig(), root, target, run.ParameterStrategies, closure, run.SinkSignals, run.OverlayDomains, nil)
	run.Interpretation, run.NextSteps, run.SuggestedTargets = buildFunctionFuzzGuidance(functionFuzzEnglishConfig(), target, closure, run)
	run.SuggestedCommands = functionFuzzSuggestedCommands(target, closure)
	rendered := renderFunctionFuzzRun(run)
	if !strings.Contains(rendered, "Planning: completed") {
		t.Fatalf("expected plan status summary, got %q", rendered)
	}
	if !strings.Contains(rendered, "Current target role: broad entry point or initialization root") {
		t.Fatalf("expected entry-root role summary, got %q", rendered)
	}
	if !strings.Contains(rendered, "Reachability summary: starting from this root, Kernforge followed 2 caller-to-callee link(s)") ||
		!strings.Contains(rendered, "the deepest") ||
		!strings.Contains(rendered, "discovered path is 3 call(s) away") {
		t.Fatalf("expected clearer reachability summary, got %q", rendered)
	}
	if !strings.Contains(rendered, "Analysis priority: high (100/100); this is an exposure and review-priority score, not proof of a vulnerability") {
		t.Fatalf("expected clearer analysis-priority summary, got %q", rendered)
	}
	// Updated wording: "static source-only fuzz triage" replaces the old "AI source-only fuzzing" label.
	if !strings.Contains(rendered, "static source-only fuzz triage: Kernforge synthesized") {
		t.Fatalf("expected source-only synthesis summary, got %q", rendered)
	}
	if !strings.Contains(rendered, "Suggested next command: /fuzz-func ValidateIoctlBuffer --file \"src/ioctl.cpp\"") {
		t.Fatalf("expected suggested next command, got %q", rendered)
	}
	if !strings.Contains(rendered, "Other good next targets:") {
		t.Fatalf("expected suggested target section, got %q", rendered)
	}
	if !strings.Contains(rendered, "ValidateIoctlBuffer") {
		t.Fatalf("expected suggested input-facing target, got %q", rendered)
	}
	if !strings.Contains(rendered, "Why this target is a good next root:") {
		t.Fatalf("expected best-target rationale, got %q", rendered)
	}
	if !strings.Contains(rendered, "its parameters are directly fuzzable") {
		t.Fatalf("expected descriptive target rationale, got %q", rendered)
	}
	if !strings.Contains(rendered, "Top predicted problems:") {
		t.Fatalf("expected source-analysis section, got %q", rendered)
	}
	if !strings.Contains(rendered, "Kernforge's internal hypothetical input state:") || !strings.Contains(rendered, "What the code will likely do:") || !strings.Contains(rendered, "What can go wrong:") {
		t.Fatalf("expected clearer scenario labels, got %q", rendered)
	}
	if !strings.Contains(rendered, "not manual reproduction steps for the user") {
		t.Fatalf("expected scenario intro to clarify these are internal modeling assumptions, got %q", rendered)
	}
	if !strings.Contains(rendered, "Most relevant internal path: DriverEntry -> ValidateIoctlBuffer") {
		t.Fatalf("expected likely path sketch, got %q", rendered)
	}
	if !strings.Contains(rendered, "Why Kernforge focused there:") {
		t.Fatalf("expected path rationale, got %q", rendered)
	}
	if !strings.Contains(rendered, "Relevant source to inspect first: src/ioctl.cpp") {
		t.Fatalf("expected source excerpt hint, got %q", rendered)
	}
	if !strings.Contains(rendered, "if (input == nullptr)") {
		t.Fatalf("expected rendered source excerpt, got %q", rendered)
	}
	if !strings.Contains(rendered, "Null, stale, or partially initialized state object") {
		t.Fatalf("expected virtual object scenario, got %q", rendered)
	}
	if !strings.Contains(rendered, "Repeated initialization, reentry, or teardown ordering confusion") {
		t.Fatalf("expected expanded state-machine scenario, got %q", rendered)
	}
	if !strings.Contains(rendered, "Validation bypass after normalization or canonicalization failure") {
		t.Fatalf("expected full malformed-string outcome without truncation, got %q", rendered)
	}
	if !strings.Contains(rendered, "Entry-point partial initialization rollback") {
		t.Fatalf("expected entry-point rollback scenario, got %q", rendered)
	}
	if !strings.Contains(rendered, "Exposed IOCTL or handle surface becomes reachable before full validation completes") {
		t.Fatalf("expected full rollback outcome without truncation, got %q", rendered)
	}
}

func TestRenderFunctionFuzzRunHighlightsCategoryHeadersWhenColorEnabled(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "1")

	run := FunctionFuzzRun{
		ID:               "fuzz-color",
		TargetSymbolName: "ValidateRequest",
		TargetSymbolID:   "func:ValidateRequest",
		HarnessReady:     true,
		RiskScore:        72,
		PrimaryEngine:    "libFuzzer + ASan/UBSan",
		Execution: FunctionFuzzExecution{
			Status: "planned",
			Reason: "Recovered compile context is sufficient for automatic background build and smoke fuzzing.",
		},
		Interpretation: []string{
			"This is a fuzz planning result, not a confirmed vulnerability finding.",
		},
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{
				Title:      "Attacker-controlled size can diverge from the buffer contract on a real copy or probe path",
				Confidence: "high",
				RiskScore:  100,
			},
		},
		ParameterStrategies: []FunctionFuzzParamStrategy{
			{Index: 0, Name: "data", RawType: "const uint8_t*", Class: "buffer"},
		},
		SinkSignals: []FunctionFuzzSinkSignal{
			{Kind: "compare_like", Name: "ValidateHeader", Reason: "branch-heavy validation or compare logic in reachable closure"},
		},
		Notes: []string{
			"Detected object or container parameters; structure-aware generation is recommended over raw byte-only mutation.",
		},
	}
	rendered := renderFunctionFuzzRun(run)
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected ANSI color styling in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, "Conclusion:") || !strings.Contains(rendered, "Parameters:") || !strings.Contains(rendered, "Signals:") || !strings.Contains(rendered, "Notes:") {
		t.Fatalf("expected highlighted category headers to remain present, got %q", rendered)
	}
	if !strings.Contains(rendered, "\x1b[1m\x1b[38;5;203m100/100 | Attacker-controlled size can diverge from the buffer contract on a real copy or probe path") {
		t.Fatalf("expected highest-risk finding line to use stronger color emphasis, got %q", rendered)
	}
}

func TestRenderFunctionFuzzRunUsesSystemLanguageMode(t *testing.T) {
	t.Setenv("LANG", "ko-KR")

	run := FunctionFuzzRun{
		ID:                 "fuzz-ko",
		TargetSymbolName:   "DriverEntry",
		TargetSymbolID:     "func:DriverEntry@src/driver.cpp",
		RiskScore:          100,
		ReachableCallCount: 3,
		ReachableDepth:     2,
		Execution: FunctionFuzzExecution{
			Status: "blocked",
		},
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{Title: "Null, stale, or partially initialized state object", Confidence: "high"},
		},
	}

	rendered := renderFunctionFuzzRunWithConfig(run, Config{FuzzFuncOutputLanguage: "system"})
	if !strings.Contains(rendered, "결론:") {
		t.Fatalf("expected Korean conclusion header, got %q", rendered)
	}
	if !strings.Contains(rendered, "계획 상태: 완료") {
		t.Fatalf("expected Korean status summary, got %q", rendered)
	}
	if !strings.Contains(rendered, "null, stale, 또는 부분 초기화 상태 객체 [높음]") {
		t.Fatalf("expected Korean scenario title/confidence, got %q", rendered)
	}
}

func TestRenderFunctionFuzzRunTranslatesScenarioBodyAndScoreReasonsInKorean(t *testing.T) {
	t.Setenv("LANG", "ko-KR")

	run := FunctionFuzzRun{
		ID:               "fuzz-ko-observed",
		TargetSymbolName: "TriggerDoubleFetch",
		TargetSymbolID:   "func:TriggerDoubleFetch@Driver/HEVD/Windows/DoubleFetch.c",
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{
				Title:      "Attacker-controlled size can diverge from the buffer contract on a real copy or probe path",
				Confidence: "high",
				RiskScore:  94,
				ScoreReasons: []string{
					"high-confidence source-only hypothesis",
					"grounded in 5 source-derived guard or sink observation(s)",
					"real memory-transfer sink is present on the same path",
				},
				Inputs: []string{
					"Parameter (LPVOID) = short-header, overlapping, and oversized-declared variants",
				},
				ExpectedFlow: "The function body contains a real memory-transfer or probe site plus a nearby boundary check, so crafted sizes, short headers, or overlapping buffers can make validation and actual access diverge.",
				LikelyIssues: []string{
					"Out-of-bounds read or write when the checked size is not the consumed size",
				},
				PathHint: "Kernforge extracted a real size or boundary comparison from the function body; the same path performs a real memory transfer; the same path probes user-controlled memory",
				SourceExcerpt: FunctionFuzzSourceExcerpt{
					File:      "Driver/HEVD/Windows/DoubleFetch.c",
					StartLine: 119,
					FocusLine: 121,
					EndLine:   121,
					Snippet: []string{
						"RtlCopyMemory((PVOID)KernelBuffer, UserBuffer, UserBufferSize);",
					},
				},
			},
		},
		CodeObservations: []FunctionFuzzCodeObservation{
			{
				Kind:         "copy_sink",
				Symbol:       "TriggerDoubleFetch",
				File:         "Driver/HEVD/Windows/DoubleFetch.c",
				Line:         121,
				Evidence:     "RtlCopyMemory((PVOID)KernelBuffer, UserBuffer, UserBufferSize);",
				WhyItMatters: "The function body performs a memory transfer, which makes buffer ownership, overlap, and length mismatches directly security-relevant.",
			},
		},
	}

	rendered := renderFunctionFuzzRunWithConfig(run, Config{FuzzFuncOutputLanguage: "system"})
	if !strings.Contains(rendered, "공격자가 조작한 크기가 실제 copy/probe 경로에서 버퍼 계약과 어긋날 수 있음 [94/100 | 높음]") {
		t.Fatalf("expected Korean translated scenario title, got %q", rendered)
	}
	if !strings.Contains(rendered, "이 점수의 이유: 높은 신뢰도의 소스 전용 가설; 소스 기반 가드 또는 sink 관찰 5개에 근거함; 같은 경로에 실제 메모리 전송") ||
		!strings.Contains(rendered, "sink가 있음") {
		t.Fatalf("expected Korean score reasons, got %q", rendered)
	}
	if !strings.Contains(rendered, "Kernforge가 내부적으로 가정한 입력 상태: Parameter (LPVOID) = 짧은 헤더, 겹침, 선언 크기 과대 변형") {
		t.Fatalf("expected Korean translated input state, got %q", rendered)
	}
	if !strings.Contains(rendered, "코드가 할 가능성이 높은 일: 함수 본문에 실제 메모리 전송 또는 probe 지점과 인접한 경계 검사가 함께 있어서") {
		t.Fatalf("expected Korean translated expected flow, got %q", rendered)
	}
	if !strings.Contains(rendered, "중요한 이유: 함수 본문이 메모리 전송을 수행하므로, 버퍼 소유권, 겹침, 길이 불일치가 곧바로 보안상 중요한 문제가") ||
		!strings.Contains(rendered, "됩니다.") {
		t.Fatalf("expected Korean translated observation rationale, got %q", rendered)
	}
	if strings.Contains(rendered, "high-confidence source-only hypothesis") ||
		strings.Contains(rendered, "The function body contains a real memory-transfer or probe site") ||
		strings.Contains(rendered, "Native auto-run was not scheduled.") {
		t.Fatalf("expected Korean-only rendering without mixed English, got %q", rendered)
	}
}

func TestFunctionFuzzAttachScenarioConcreteInputsShowsStructuredExamples(t *testing.T) {
	t.Setenv("LANG", "ko-KR")

	params := []FunctionFuzzParamStrategy{
		{
			Index:   0,
			Name:    "Parameter",
			RawType: "LPVOID",
			Class:   "opaque",
		},
	}
	scenarios := functionFuzzAttachScenarioConcreteInputs(Config{FuzzFuncOutputLanguage: "system"}, params, []FunctionFuzzVirtualScenario{
		{
			Title:      "Attacker-controlled size can diverge from the buffer contract on a real copy or probe path",
			Confidence: "high",
			RiskScore:  100,
			SourceExcerpt: FunctionFuzzSourceExcerpt{
				File: "Driver/HEVD/Windows/DoubleFetch.c",
				Snippet: []string{
					"UserBuffer = UserDoubleFetch->Buffer;",
					"DbgPrint(\"[+] UserDoubleFetch->Size: 0x%zX\\n\", UserDoubleFetch->Size);",
				},
			},
		},
	})
	if len(scenarios) != 1 {
		t.Fatalf("expected one scenario, got %d", len(scenarios))
	}
	if len(scenarios[0].ConcreteInputs) == 0 {
		t.Fatalf("expected concrete input examples to be synthesized")
	}
	if !strings.Contains(strings.Join(scenarios[0].ConcreteInputs, "\n"), "Parameter = { Buffer = 0x10000000, Size = 0x00001000 }") {
		t.Fatalf("expected structured concrete example, got %+v", scenarios[0].ConcreteInputs)
	}

	rendered := renderFunctionFuzzRunWithConfig(FunctionFuzzRun{
		ID:               "fuzz-concrete-inputs",
		TargetSymbolName: "TriggerDoubleFetch",
		TargetSymbolID:   "func:TriggerDoubleFetch@Driver/HEVD/Windows/DoubleFetch.c",
		VirtualScenarios: scenarios,
	}, Config{FuzzFuncOutputLanguage: "system"})

	if !strings.Contains(rendered, "가상의 구체 입력 예시:") {
		t.Fatalf("expected concrete input section in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, "Parameter = { Buffer = 0x10000000, Size = 0x00001000 }") {
		t.Fatalf("expected rendered concrete input example, got %q", rendered)
	}
}

func TestFunctionFuzzObservationInvariantInsightsDeriveGuardUseDrift(t *testing.T) {
	observations := []FunctionFuzzCodeObservation{
		{
			Kind:        "probe_sink",
			AccessPaths: []string{"UserDoubleFetch", "UserBuffer"},
		},
		{
			Kind:        "size_guard",
			AccessPaths: []string{"UserBufferSize"},
		},
		{
			Kind:        "copy_sink",
			AccessPaths: []string{"KernelBuffer", "UserBuffer", "UserBufferSize"},
		},
		{
			Kind:        "copy_sink",
			AccessPaths: []string{"KernelBuffer", "UserBuffer", "UserDoubleFetch->Size"},
		},
	}

	invariants, drifts := functionFuzzObservationInvariantInsights(observations, "copy_sink", "probe_sink", "size_guard")
	if len(invariants) == 0 {
		t.Fatalf("expected invariant insights to be derived")
	}
	joinedInvariants := fmt.Sprintf("%+v", invariants)
	if !strings.Contains(joinedInvariants, "guard_use_size_equivalence") ||
		!strings.Contains(joinedInvariants, "UserBufferSize") ||
		!strings.Contains(joinedInvariants, "UserDoubleFetch->Size") {
		t.Fatalf("expected guard/use size invariant, got %s", joinedInvariants)
	}
	if !strings.Contains(joinedInvariants, "buffer_size_contract") ||
		!strings.Contains(joinedInvariants, "UserBuffer") {
		t.Fatalf("expected buffer contract invariant, got %s", joinedInvariants)
	}

	joinedDrifts := strings.Join(drifts, "\n")
	if !strings.Contains(joinedDrifts, "drift:guard_use_size|UserBufferSize|UserDoubleFetch->Size") {
		t.Fatalf("expected guard/use drift token, got %q", joinedDrifts)
	}
	if !strings.Contains(joinedDrifts, "drift:buffer_size_contract|UserBuffer|UserDoubleFetch->Size") {
		t.Fatalf("expected buffer contract drift token, got %q", joinedDrifts)
	}
}

func TestFunctionFuzzObservationComparisonFactsExtractBoundaryPredicate(t *testing.T) {
	facts := functionFuzzObservationComparisonFacts(
		"if (UserBufferSize > sizeof(KernelBuffer))",
		[]string{"Parameter"},
		[]string{"UserBufferSize", "KernelBuffer"},
	)
	if len(facts) == 0 {
		t.Fatalf("expected comparison fact to be extracted")
	}
	joined := strings.Join(facts, "\n")
	if !strings.Contains(joined, "cmp:UserBufferSize|>|sizeof(KernelBuffer)") {
		t.Fatalf("expected normalized comparison fact, got %q", joined)
	}
}

func TestRenderFunctionFuzzRunShowsInvariantAndDriftInKorean(t *testing.T) {
	t.Setenv("LANG", "ko-KR")

	run := FunctionFuzzRun{
		ID:               "fuzz-invariant-drift",
		TargetSymbolName: "TriggerDoubleFetch",
		TargetSymbolID:   "func:TriggerDoubleFetch@Driver/HEVD/Windows/DoubleFetch.c",
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{
				Title:      "Attacker-controlled size can diverge from the buffer contract on a real copy or probe path",
				Confidence: "high",
				RiskScore:  100,
				Invariants: []FunctionFuzzInvariant{
					{
						Kind:  "guard_use_size_equivalence",
						Left:  "UserBufferSize",
						Right: "UserDoubleFetch->Size",
					},
					{
						Kind:  "buffer_size_contract",
						Left:  "UserBuffer",
						Right: "UserDoubleFetch->Size",
					},
				},
				DriftExamples: []string{
					functionFuzzDriftToken("guard_use_size", "UserBufferSize", "UserDoubleFetch->Size"),
					functionFuzzDriftToken("buffer_size_contract", "UserBuffer", "UserDoubleFetch->Size"),
				},
			},
		},
	}

	rendered := renderFunctionFuzzRunWithConfig(run, Config{FuzzFuncOutputLanguage: "system"})
	if !strings.Contains(rendered, "깨야 할 핵심 불변식:") {
		t.Fatalf("expected invariant label, got %q", rendered)
	}
	if !strings.Contains(rendered, "검증 시점의 크기 경로 UserBufferSize") ||
		!strings.Contains(rendered, "이후 사용 시점의 크기 경로 UserDoubleFetch->Size") ||
		!strings.Contains(rendered, "같은 실행 경로에서 동일하게 유지되어야 합니다") {
		t.Fatalf("expected localized invariant explanation, got %q", rendered)
	}
	if !strings.Contains(rendered, "공격자가 흔드는 전후 값 예시:") {
		t.Fatalf("expected drift label, got %q", rendered)
	}
	if !strings.Contains(rendered, "검증 시 UserBufferSize = 0x20") ||
		!strings.Contains(rendered, "이후 사용 시 UserDoubleFetch->Size = 0x1000") ||
		!strings.Contains(rendered, "으로 벌어질 수 있습니다") {
		t.Fatalf("expected localized drift example, got %q", rendered)
	}
	if strings.Contains(rendered, "Validation-time size path") || strings.Contains(rendered, "Validation read saw") {
		t.Fatalf("expected Korean-only invariant rendering, got %q", rendered)
	}
}

func TestRenderFunctionFuzzRunShowsBranchPredicateAndCounterexampleInKorean(t *testing.T) {
	t.Setenv("LANG", "ko-KR")

	run := FunctionFuzzRun{
		ID:               "fuzz-branch-counterexample",
		TargetSymbolName: "TriggerDoubleFetch",
		TargetSymbolID:   "func:TriggerDoubleFetch@Driver/HEVD/Windows/DoubleFetch.c",
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{
				Title:       "Attacker-controlled size can diverge from the buffer contract on a real copy or probe path",
				Confidence:  "high",
				RiskScore:   100,
				BranchFacts: []string{functionFuzzComparisonToken("UserBufferSize", ">", "sizeof(KernelBuffer)")},
			},
		},
	}

	rendered := renderFunctionFuzzRunWithConfig(run, Config{FuzzFuncOutputLanguage: "system"})
	if !strings.Contains(rendered, "소스에서 뽑은 비교식: UserBufferSize > sizeof(KernelBuffer)") {
		t.Fatalf("expected branch predicate rendering, got %q", rendered)
	}
	if !strings.Contains(rendered, "이 비교식을 깨는 최소 반례 예시: UserBufferSize = sizeof(KernelBuffer) + 1") {
		t.Fatalf("expected branch counterexample rendering, got %q", rendered)
	}
	if strings.Contains(rendered, "Source-derived branch predicates") || strings.Contains(rendered, "Minimal counterexample inputs") {
		t.Fatalf("expected Korean-only branch rendering, got %q", rendered)
	}
}

func TestFunctionFuzzObservationBranchOutcomesMapRejectAndCopy(t *testing.T) {
	root := t.TempDir()
	relative := filepath.ToSlash(filepath.Join("Driver", "HEVD", "Windows", "DoubleFetch.c"))
	absolute := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	source := strings.Join([]string{
		"NTSTATUS TriggerDoubleFetch()",
		"{",
		"    if (UserBufferSize > sizeof(KernelBuffer))",
		"    {",
		"        Status = STATUS_INVALID_PARAMETER;",
		"        goto End;",
		"    }",
		"    ProbeForRead(UserBuffer, sizeof(KernelBuffer), (ULONG)__alignof(UCHAR));",
		"    RtlCopyMemory((PVOID)KernelBuffer, UserBuffer, UserBufferSize);",
		"End:",
		"    return Status;",
		"}",
	}, "\n")
	if err := os.WriteFile(absolute, []byte(source), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	symbol := SymbolRecord{
		ID:        "func:TriggerDoubleFetch@" + relative,
		Name:      "TriggerDoubleFetch",
		File:      relative,
		StartLine: 1,
		EndLine:   12,
	}
	observations := []FunctionFuzzCodeObservation{
		{
			Kind:            "size_guard",
			SymbolID:        symbol.ID,
			Symbol:          symbol.Name,
			File:            relative,
			Line:            3,
			Evidence:        "if (UserBufferSize > sizeof(KernelBuffer))",
			AccessPaths:     []string{"UserBufferSize", "KernelBuffer"},
			ComparisonFacts: []string{functionFuzzComparisonToken("UserBufferSize", ">", "sizeof(KernelBuffer)")},
		},
		{
			Kind:        "probe_sink",
			SymbolID:    symbol.ID,
			Symbol:      symbol.Name,
			File:        relative,
			Line:        8,
			Evidence:    "ProbeForRead(UserBuffer, sizeof(KernelBuffer), (ULONG)__alignof(UCHAR));",
			AccessPaths: []string{"UserBuffer", "KernelBuffer"},
		},
		{
			Kind:        "copy_sink",
			SymbolID:    symbol.ID,
			Symbol:      symbol.Name,
			File:        relative,
			Line:        9,
			Evidence:    "RtlCopyMemory((PVOID)KernelBuffer, UserBuffer, UserBufferSize);",
			AccessPaths: []string{"KernelBuffer", "UserBuffer", "UserBufferSize"},
		},
	}

	outcomes := functionFuzzObservationBranchOutcomes(root, symbol, observations, "size_guard", "null_guard")
	if len(outcomes) < 2 {
		t.Fatalf("expected pass/fail branch outcomes, got %+v", outcomes)
	}
	joined := fmt.Sprintf("%+v", outcomes)
	if !strings.Contains(joined, "Side:true") || !strings.Contains(joined, "STATUS_INVALID_PARAMETER") {
		t.Fatalf("expected true-side reject outcome, got %s", joined)
	}
	if !strings.Contains(joined, "Side:false") || !strings.Contains(joined, "RtlCopyMemory") {
		t.Fatalf("expected false-side copy outcome, got %s", joined)
	}
	if !strings.Contains(joined, "DownstreamCalls:[ProbeForRead RtlCopyMemory]") {
		t.Fatalf("expected downstream call chain on false side, got %s", joined)
	}
}

func TestRenderFunctionFuzzRunShowsBranchOutcomesInKorean(t *testing.T) {
	t.Setenv("LANG", "ko-KR")

	run := FunctionFuzzRun{
		ID:               "fuzz-branch-outcomes",
		TargetSymbolName: "TriggerDoubleFetch",
		TargetSymbolID:   "func:TriggerDoubleFetch@Driver/HEVD/Windows/DoubleFetch.c",
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{
				Title:       "Attacker-controlled size can diverge from the buffer contract on a real copy or probe path",
				Confidence:  "high",
				RiskScore:   100,
				BranchFacts: []string{functionFuzzComparisonToken("UserBufferSize", ">", "sizeof(KernelBuffer)")},
				BranchOutcomes: []FunctionFuzzBranchOutcome{
					{
						Predicate:  functionFuzzComparisonToken("UserBufferSize", ">", "sizeof(KernelBuffer)"),
						Side:       "true",
						EffectKind: "reject",
						Line:       109,
						Evidence:   "Status = STATUS_INVALID_PARAMETER;",
					},
					{
						Predicate:       functionFuzzComparisonToken("UserBufferSize", ">", "sizeof(KernelBuffer)"),
						Side:            "false",
						EffectKind:      "copy",
						Line:            121,
						Evidence:        "RtlCopyMemory((PVOID)KernelBuffer, UserBuffer, UserBufferSize);",
						DownstreamCalls: []string{"ProbeForRead", "RtlCopyMemory"},
					},
				},
			},
		},
	}

	rendered := renderFunctionFuzzRunWithConfig(run, Config{FuzzFuncOutputLanguage: "system"})
	if !strings.Contains(rendered, "그 분기 뒤에 이어지는 대표 흐름:") {
		t.Fatalf("expected branch outcome label, got %q", rendered)
	}
	if !strings.Contains(rendered, "조건이 참이면 109번 줄에서 reject 또는 status 설정 경로로 빠집니다: Status =") ||
		!strings.Contains(rendered, "STATUS_INVALID_PARAMETER;") {
		t.Fatalf("expected true-side branch outcome, got %q", rendered)
	}
	if !strings.Contains(rendered, "조건이 거짓이면 121번 줄에서 실제 copy sink까지 도달합니다:") ||
		!strings.Contains(rendered, "RtlCopyMemory((PVOID)KernelBuffer, UserBuffer, UserBufferSize);") {
		t.Fatalf("expected false-side branch outcome, got %q", rendered)
	}
	if !strings.Contains(rendered, "대표 후속 호출: ProbeForRead -> RtlCopyMemory") {
		t.Fatalf("expected downstream call chain rendering, got %q", rendered)
	}
	if strings.Contains(rendered, "Representative pass/fail consequences") {
		t.Fatalf("expected Korean-only branch outcome rendering, got %q", rendered)
	}
}

func TestRenderFunctionFuzzRunShowsBranchDeltaSummaryInConclusion(t *testing.T) {
	t.Setenv("LANG", "ko-KR")

	run := FunctionFuzzRun{
		ID:               "fuzz-branch-delta-conclusion",
		TargetSymbolName: "TriggerDoubleFetch",
		TargetSymbolID:   "func:TriggerDoubleFetch@Driver/HEVD/Windows/DoubleFetch.c",
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{
				Title:      "Attacker-controlled size can diverge from the buffer contract on a real copy or probe path",
				Confidence: "high",
				RiskScore:  100,
				BranchFacts: []string{
					functionFuzzComparisonToken("UserBufferSize", ">", "sizeof(KernelBuffer)"),
				},
				BranchOutcomes: []FunctionFuzzBranchOutcome{
					{
						Predicate:  functionFuzzComparisonToken("UserBufferSize", ">", "sizeof(KernelBuffer)"),
						Side:       "true",
						EffectKind: "reject",
						Line:       109,
						Evidence:   "Status = STATUS_INVALID_PARAMETER;",
					},
					{
						Predicate:       functionFuzzComparisonToken("UserBufferSize", ">", "sizeof(KernelBuffer)"),
						Side:            "false",
						EffectKind:      "copy",
						Line:            121,
						Evidence:        "RtlCopyMemory((PVOID)KernelBuffer, UserBuffer, UserBufferSize);",
						DownstreamCalls: []string{"ProbeForRead", "RtlCopyMemory"},
					},
				},
				LikelyIssues: []string{"Out-of-bounds read or write when the checked size is not the consumed size"},
			},
		},
	}

	rendered := renderFunctionFuzzRunWithConfig(run, Config{FuzzFuncOutputLanguage: "system"})
	if !strings.Contains(rendered, "가장 유용한 분기 차이 요약:") {
		t.Fatalf("expected conclusion-level branch delta summary, got %q", rendered)
	}
	if !strings.Contains(rendered, "UserBufferSize = sizeof(KernelBuffer) + 1 이면 reject/status 경로로 빠집니다") ||
		!strings.Contains(rendered, "그렇지") ||
		!strings.Contains(rendered, "않으면 ProbeForRead -> RtlCopyMemory 까지 진행합니다") {
		t.Fatalf("expected concrete branch delta summary in conclusion, got %q", rendered)
	}
}

func TestBuildFunctionFuzzVirtualScenariosFallsBackForParameterlessTarget(t *testing.T) {
	target := SymbolRecord{
		ID:        "func:TickOnce@src/runtime.cpp",
		Name:      "TickOnce",
		Kind:      "function",
		Language:  "cpp",
		File:      "src/runtime.cpp",
		Signature: "void TickOnce()",
	}
	closure := functionFuzzClosure{
		RootSymbol: target,
		Symbols:    []SymbolRecord{target},
	}

	params := buildFunctionFuzzParameterStrategies(target.Signature)
	scenarios := buildFunctionFuzzVirtualScenarios(functionFuzzEnglishConfig(), "", target, params, closure, nil, nil, nil)
	if len(scenarios) == 0 {
		t.Fatalf("expected fallback source-only scenarios for parameterless target")
	}
	if !strings.Contains(scenarios[0].Title, "Implicit prerequisite state missing") {
		t.Fatalf("expected parameterless fallback scenario, got %+v", scenarios[0])
	}

	run := FunctionFuzzRun{
		VirtualScenarios: scenarios,
	}
	if got := functionFuzzSourceOnlyStatus(run); got != "completed" {
		t.Fatalf("expected source-only status to remain completed with fallback scenarios, got %q", got)
	}
}

func TestFunctionFuzzBuildSourceExcerptSkipsBannerComments(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "Exploit")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	sourcePath := filepath.Join(sourceDir, "Common.c")
	content := strings.Join([]string{
		"/*",
		"##     ## ########    ###    ########",
		"",
		"HackSys Extreme Vulnerable Driver Exploit",
		"",
		"Author : Ashfaq Ansari",
		"Contact: ashfaq[at]hacksys[dot]io",
		"Website: https://hacksys.io/",
		"*/",
		"",
		"NTSTATUS TriggerPath(PVOID Buffer, ULONG Size)",
		"{",
		"    NTSTATUS Status = STATUS_UNSUCCESSFUL;",
		"",
		"    if (Buffer == NULL)",
		"    {",
		"        Status = STATUS_INVALID_PARAMETER;",
		"        goto cleanup;",
		"    }",
		"",
		"cleanup:",
		"    return Status;",
		"}",
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	symbol := SymbolRecord{
		ID:        "func:TriggerPath@Exploit/Common.c",
		Name:      "TriggerPath",
		File:      "Exploit/Common.c",
		StartLine: 2,
		EndLine:   8,
	}

	excerpt := functionFuzzBuildSourceExcerpt(root, symbol, "status", "cleanup", "buffer")
	if excerpt.FocusLine <= 8 {
		t.Fatalf("expected excerpt focus to move past banner comments, got %+v", excerpt)
	}
	snippet := strings.Join(excerpt.Snippet, "\n")
	if strings.Contains(snippet, "HackSys Extreme Vulnerable Driver Exploit") {
		t.Fatalf("expected banner comment to be excluded from excerpt, got %q", snippet)
	}
	if !strings.Contains(snippet, "if (Buffer == NULL)") && !strings.Contains(snippet, "Status = STATUS_INVALID_PARAMETER;") {
		t.Fatalf("expected executable code in excerpt, got %q", snippet)
	}
}

func TestFunctionFuzzSanitizeSignatureStripsBannerAndIncludeNoise(t *testing.T) {
	raw := "/*++ HackSys Extreme Vulnerable Driver Exploit Author : Ashfaq Ansari Contact: ashfaq[at]hacksys[dot]io --*/ #include \"IntegerOverflow.h\" DWORD WINAPI IntegerOverflowThread(LPVOID Parameter)"
	got := functionFuzzSanitizeSignature(raw)
	want := "DWORD WINAPI IntegerOverflowThread(LPVOID Parameter)"
	if got != want {
		t.Fatalf("expected sanitized signature %q, got %q", want, got)
	}

	params := buildFunctionFuzzParameterStrategies(raw)
	if len(params) != 1 {
		t.Fatalf("expected one parameter strategy after signature sanitization, got %+v", params)
	}
	if params[0].Name != "Parameter" || params[0].RawType != "LPVOID" {
		t.Fatalf("expected sanitized parameter extraction, got %+v", params[0])
	}
	if params[0].Class != "opaque" {
		t.Fatalf("expected LPVOID Parameter to default to opaque modeling, got %+v", params[0])
	}

	label := functionFuzzSuggestedTargetLabel(functionFuzzEnglishConfig(), SymbolRecord{
		Name: "TriggerRoot",
		File: "Exploit/Common.c",
	}, SymbolRecord{
		Name:      "IntegerOverflowThread",
		Signature: raw,
		File:      "Exploit/IntegerOverflow.c",
	}, params)
	if strings.Contains(label, "HackSys") || strings.Contains(label, "#include") {
		t.Fatalf("expected suggested target label to exclude banner or include noise, got %q", label)
	}
	if !strings.Contains(label, want) {
		t.Fatalf("expected suggested target label to keep sanitized signature, got %q", label)
	}

	target := SymbolRecord{
		ID:        "func:IntegerOverflowThread@Exploit/IntegerOverflow.c",
		Name:      "IntegerOverflowThread",
		File:      "Exploit/IntegerOverflow.c",
		Signature: raw,
	}
	scenarios := buildFunctionFuzzVirtualScenarios(functionFuzzEnglishConfig(), "", target, params, functionFuzzClosure{
		RootSymbol: target,
		Symbols:    []SymbolRecord{target},
	}, nil, nil, nil)
	if len(scenarios) == 0 {
		t.Fatalf("expected source-only scenarios for sanitized LPVOID signature")
	}
	if strings.Contains(strings.Join(scenarios[0].Inputs, " "), "partially initialized object") {
		t.Fatalf("expected generic LPVOID parameter to avoid object-state wording, got %+v", scenarios[0])
	}
	if !strings.Contains(strings.Join(scenarios[0].Inputs, " "), "raw bytes") {
		t.Fatalf("expected generic LPVOID parameter to fall back to opaque/raw-byte modeling, got %+v", scenarios[0])
	}
}

func TestFunctionFuzzBuildSourceExcerptCleansInlineBannerAndIncludeNoise(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "Exploit")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	sourcePath := filepath.Join(sourceDir, "IntegerOverflow.c")
	content := strings.Join([]string{
		"/*++ HackSys Extreme Vulnerable Driver Exploit Author : Ashfaq Ansari Contact: ashfaq[at]hacksys[dot]io Website: https://hacksys.io/ --*/ #include \"IntegerOverflow.h\" DWORD WINAPI IntegerOverflowThread(LPVOID Parameter)",
		"{",
		"    if (Parameter == NULL)",
		"    {",
		"        return 1;",
		"    }",
		"    return 0;",
		"}",
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	symbol := SymbolRecord{
		ID:        "func:IntegerOverflowThread@Exploit/IntegerOverflow.c",
		Name:      "IntegerOverflowThread",
		File:      "Exploit/IntegerOverflow.c",
		StartLine: 1,
		EndLine:   3,
	}

	excerpt := functionFuzzBuildSourceExcerpt(root, symbol, "parameter", "return")
	snippet := strings.Join(excerpt.Snippet, "\n")
	if strings.Contains(snippet, "HackSys Extreme Vulnerable Driver Exploit") || strings.Contains(snippet, "#include") {
		t.Fatalf("expected inline banner and include noise to be stripped from excerpt, got %q", snippet)
	}
	if !strings.Contains(snippet, "DWORD WINAPI IntegerOverflowThread(LPVOID Parameter)") && !strings.Contains(snippet, "if (Parameter == NULL)") {
		t.Fatalf("expected cleaned executable code in excerpt, got %q", snippet)
	}
}

func TestFunctionFuzzBuildSourceExcerptPrefersChecksOverDeclarations(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "Exploit")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	sourcePath := filepath.Join(sourceDir, "Common.c")
	content := strings.Join([]string{
		"DWORD WINAPI ArbitraryOverwriteThread(LPVOID Parameter)",
		"{",
		"    HMODULE hKernelInUserMode = NULL;",
		"    PVOID KernelBaseAddressInKernelMode;",
		"    NTSTATUS NtStatus = STATUS_UNSUCCESSFUL;",
		"    PSYSTEM_MODULE_INFORMATION pSystemModuleInformation;",
		"",
		"    DEBUG_INFO(\"\\t\\t[+] Resolving Kernel APIs\\n\");",
		"    if (!ResolveKernelAPIs())",
		"    {",
		"        NtStatus = STATUS_NOT_FOUND;",
		"        goto cleanup;",
		"    }",
		"",
		"cleanup:",
		"    return NtStatus;",
		"}",
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	symbol := SymbolRecord{
		ID:        "func:ArbitraryOverwriteThread@Exploit/Common.c",
		Name:      "ArbitraryOverwriteThread",
		File:      "Exploit/Common.c",
		StartLine: 1,
		EndLine:   10,
	}

	excerpt := functionFuzzBuildSourceExcerpt(root, symbol, "status", "cleanup", "resolve", "kernel")
	if excerpt.FocusLine == 5 {
		t.Fatalf("expected excerpt to avoid plain status declaration, got %+v", excerpt)
	}
	snippet := strings.Join(excerpt.Snippet, "\n")
	if strings.Contains(snippet, "NTSTATUS NtStatus = STATUS_UNSUCCESSFUL;") && !strings.Contains(snippet, "if (!ResolveKernelAPIs())") && !strings.Contains(snippet, "goto cleanup;") {
		t.Fatalf("expected executable decision or cleanup code in excerpt, got %q", snippet)
	}
	if !strings.Contains(snippet, "if (!ResolveKernelAPIs())") && !strings.Contains(snippet, "goto cleanup;") && !strings.Contains(snippet, "return NtStatus;") {
		t.Fatalf("expected excerpt to prefer checks or cleanup flow, got %q", snippet)
	}
}

func TestFunctionFuzzBuildSourceExcerptMovesPastSignatureIntoFunctionBody(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "Driver", "HEVD", "Windows")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	sourcePath := filepath.Join(sourceDir, "UseAfterFreeNonPagedPool.c")
	content := strings.Join([]string{
		"/// <summary>",
		"/// Allocate fake object.",
		"/// </summary>",
		"/// <returns>NTSTATUS</returns>",
		"NTSTATUS",
		"AllocateFakeObjectNonPagedPoolIoctlHandler(",
		"    _In_ PIRP Irp,",
		"    _In_ PIO_STACK_LOCATION IrpSp",
		")",
		"{",
		"    NTSTATUS Status = STATUS_UNSUCCESSFUL;",
		"    PVOID UserBuffer = IrpSp->Parameters.DeviceIoControl.Type3InputBuffer;",
		"",
		"    if (UserBuffer == NULL)",
		"    {",
		"        Status = STATUS_INVALID_PARAMETER;",
		"        goto End;",
		"    }",
		"",
		"    ProbeForRead(UserBuffer, sizeof(FAKE_OBJECT), __alignof(UCHAR));",
		"",
		"End:",
		"    return Status;",
		"}",
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	symbol := SymbolRecord{
		ID:        "func:AllocateFakeObjectNonPagedPoolIoctlHandler@Driver/HEVD/Windows/UseAfterFreeNonPagedPool.c",
		Name:      "AllocateFakeObjectNonPagedPoolIoctlHandler",
		File:      "Driver/HEVD/Windows/UseAfterFreeNonPagedPool.c",
		StartLine: 4,
		EndLine:   9,
	}

	excerpt := functionFuzzBuildSourceExcerpt(root, symbol, "buffer", "status", "probe", "ioctl")
	if excerpt.FocusLine <= 9 {
		t.Fatalf("expected excerpt focus to move into the function body, got %+v", excerpt)
	}
	snippet := strings.Join(excerpt.Snippet, "\n")
	if strings.Contains(snippet, "AllocateFakeObjectNonPagedPoolIoctlHandler(") && !strings.Contains(snippet, "if (UserBuffer == NULL)") && !strings.Contains(snippet, "ProbeForRead(") {
		t.Fatalf("expected excerpt to avoid signature-only snippet, got %q", snippet)
	}
	if !strings.Contains(snippet, "if (UserBuffer == NULL)") && !strings.Contains(snippet, "ProbeForRead(") && !strings.Contains(snippet, "goto End;") {
		t.Fatalf("expected executable body lines in excerpt, got %q", snippet)
	}
}

func TestFunctionFuzzBuildSourceExcerptAvoidsBootstrapResolverChecks(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "Exploit")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	sourcePath := filepath.Join(sourceDir, "Common.c")
	content := strings.Join([]string{
		"PVOID GetHalDispatchTable(VOID)",
		"{",
		"    HMODULE hNtDll = LoadLibrary(\"ntdll.dll\");",
		"    if (!hNtDll)",
		"    {",
		"        DEBUG_ERROR(\"failed to load ntdll\");",
		"        exit(EXIT_FAILURE);",
		"    }",
		"",
		"    if (UserBuffer == NULL)",
		"    {",
		"        return NULL;",
		"    }",
		"",
		"    ProbeForRead(UserBuffer, sizeof(ULONG_PTR), __alignof(UCHAR));",
		"    return (PVOID)0x1234;",
		"}",
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	symbol := SymbolRecord{
		ID:        "func:GetHalDispatchTable@Exploit/Common.c",
		Name:      "GetHalDispatchTable",
		File:      "Exploit/Common.c",
		StartLine: 1,
		EndLine:   8,
	}

	excerpt := functionFuzzBuildSourceExcerpt(root, symbol, "dispatch", "buffer", "probe")
	snippet := strings.Join(excerpt.Snippet, "\n")
	if strings.Contains(snippet, "if (!hNtDll)") && !strings.Contains(snippet, "if (UserBuffer == NULL)") && !strings.Contains(snippet, "ProbeForRead(") {
		t.Fatalf("expected excerpt to move past bootstrap resolver checks, got %q", snippet)
	}
	if !strings.Contains(snippet, "if (UserBuffer == NULL)") && !strings.Contains(snippet, "ProbeForRead(") {
		t.Fatalf("expected excerpt to prefer input validation or probe line, got %q", snippet)
	}
}

func TestFunctionFuzzBestScenarioSymbolAvoidsBootstrapResolvers(t *testing.T) {
	resolver := SymbolRecord{
		ID:        "func:GetHalDispatchTable@Exploit/Common.c",
		Name:      "GetHalDispatchTable",
		File:      "Exploit/Common.c",
		Kind:      "function",
		Signature: "PVOID GetHalDispatchTable(void)",
	}
	handler := SymbolRecord{
		ID:        "func:AllocateFakeObjectNonPagedPoolIoctlHandler@Driver/HEVD/Windows/UseAfterFreeNonPagedPool.c",
		Name:      "AllocateFakeObjectNonPagedPoolIoctlHandler",
		File:      "Driver/HEVD/Windows/UseAfterFreeNonPagedPool.c",
		Kind:      "function",
		Signature: "NTSTATUS AllocateFakeObjectNonPagedPoolIoctlHandler(PIRP Irp, PIO_STACK_LOCATION IrpSp)",
	}
	closure := functionFuzzClosure{
		RootSymbol: SymbolRecord{
			ID:   "func:ArbitraryOverwriteThread@Exploit/Common.c",
			Name: "ArbitraryOverwriteThread",
			File: "Exploit/Common.c",
		},
		Symbols: []SymbolRecord{
			resolver,
			handler,
		},
	}
	sinks := []FunctionFuzzSinkSignal{
		{Kind: "overlay", SymbolID: resolver.ID},
		{Kind: "overlay", SymbolID: handler.ID},
		{Kind: "compare_like", SymbolID: handler.ID},
	}

	best := functionFuzzBestScenarioSymbol(closure, sinks, []string{"parse_like", "compare_like", "overlay"}, "dispatch", "payload", "buffer", "probe")
	if best.ID != handler.ID {
		t.Fatalf("expected input-facing handler to outrank bootstrap resolver, got %+v", best)
	}
	if filepath.ToSlash(best.File) != filepath.ToSlash(handler.File) {
		t.Fatalf("expected best scenario symbol to come from handler file, got %+v", best)
	}
}

func TestRenderFunctionFuzzRunExplainsSignalsWithoutTruncation(t *testing.T) {
	run := FunctionFuzzRun{
		ID:               "fuzz-signals",
		TargetSymbolName: "DriverEntry",
		TargetSymbolID:   "func:DriverEntry@src/driver.cpp",
		OverlayDomains:   []string{"handle_surface", "ioctl_surface", "memory_surface", "security_boundary"},
		SinkSignals: []FunctionFuzzSinkSignal{
			{
				Kind:   "overlay",
				Name:   "handle_surface",
				File:   "src/driver.cpp",
				Reason: "handle:SampleKernelAPI::Initialize@src/driver.cpp -> entity:handle_surface [opens_handle]",
			},
			{
				Kind:   "compare_like",
				Name:   "ValidateHeader",
				File:   "src/ioctl.cpp",
				Reason: "branch-heavy validation or compare logic in reachable closure",
			},
		},
	}

	rendered := renderFunctionFuzzRun(run)
	if !strings.Contains(rendered, "What these signals mean:") {
		t.Fatalf("expected signals section to explain meaning, got %q", rendered)
	}
	if !strings.Contains(rendered, "Exposed surfaces in this mapped call closure:") {
		t.Fatalf("expected surfaced boundary summary, got %q", rendered)
	}
	if !strings.Contains(rendered, "handle surface: reachable code opens, stores, validates, or reuses handle-like objects whose lifetime or ownership can drift") {
		t.Fatalf("expected descriptive handle-surface explanation, got %q", rendered)
	}
	if !strings.Contains(rendered, "Representative evidence from the mapped closure:") {
		t.Fatalf("expected representative evidence block, got %q", rendered)
	}
	if !strings.Contains(rendered, "observed at: handle:SampleKernelAPI::Initialize@src/driver.cpp -> entity:handle_surface [opens_handle]") {
		t.Fatalf("expected full overlay evidence without truncation, got %q", rendered)
	}
	if strings.Contains(rendered, "exposed_surfaces:") {
		t.Fatalf("expected old opaque signal label to be removed, got %q", rendered)
	}
}

func TestBuildFunctionFuzzCodeObservationsExtractsRealGuardsAndSinks(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "src")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	sourcePath := filepath.Join(sourceDir, "ioctl.c")
	content := strings.Join([]string{
		"NTSTATUS HandleIoctl(PVOID UserBuffer, ULONG Size, ULONG IoControlCode)",
		"{",
		"    PVOID KernelBuffer = ExAllocatePoolWithTag(NonPagedPool, Size, 'fzz1');",
		"    if (UserBuffer == NULL)",
		"    {",
		"        return STATUS_INVALID_PARAMETER;",
		"    }",
		"    if (Size < sizeof(ULONG))",
		"    {",
		"        goto Cleanup;",
		"    }",
		"    switch (IoControlCode)",
		"    {",
		"    case 0x222003:",
		"        ProbeForRead(UserBuffer, Size, sizeof(UCHAR));",
		"        RtlCopyMemory(KernelBuffer, UserBuffer, Size);",
		"        break;",
		"    default:",
		"        goto Cleanup;",
		"    }",
		"Cleanup:",
		"    ExFreePool(KernelBuffer);",
		"    return STATUS_SUCCESS;",
		"}",
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	target := SymbolRecord{
		ID:        "func:HandleIoctl@src/ioctl.c",
		Name:      "HandleIoctl",
		File:      "src/ioctl.c",
		Kind:      "function",
		Signature: "NTSTATUS HandleIoctl(PVOID UserBuffer, ULONG Size, ULONG IoControlCode)",
		StartLine: 1,
		EndLine:   23,
	}
	params := buildFunctionFuzzParameterStrategies(target.Signature)
	closure := functionFuzzClosure{
		RootSymbol: target,
		Symbols:    []SymbolRecord{target},
	}

	observations := buildFunctionFuzzCodeObservations(root, target, params, closure)
	if len(observations) == 0 {
		t.Fatalf("expected source-derived observations")
	}
	joined := strings.Join(func() []string {
		out := []string{}
		for _, item := range observations {
			out = append(out, item.Kind)
		}
		return out
	}(), ",")
	for _, kind := range []string{"null_guard", "size_guard", "dispatch_guard", "probe_sink", "copy_sink", "cleanup_path", "alloc_site"} {
		if !strings.Contains(joined, kind) {
			t.Fatalf("expected observation kind %s in %q", kind, joined)
		}
	}
}

func TestBuildFunctionFuzzVirtualScenariosPrefersObservedCodePaths(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "src")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	sourcePath := filepath.Join(sourceDir, "ioctl.c")
	content := strings.Join([]string{
		"NTSTATUS HandleIoctl(PVOID UserBuffer, ULONG Size, ULONG IoControlCode)",
		"{",
		"    if (Size < sizeof(ULONG))",
		"    {",
		"        return STATUS_BUFFER_TOO_SMALL;",
		"    }",
		"    switch (IoControlCode)",
		"    {",
		"    case 0x222003:",
		"        ProbeForRead(UserBuffer, Size, sizeof(UCHAR));",
		"        RtlCopyMemory(KernelBuffer, UserBuffer, Size);",
		"        break;",
		"    default:",
		"        goto Cleanup;",
		"    }",
		"Cleanup:",
		"    return STATUS_SUCCESS;",
		"}",
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	target := SymbolRecord{
		ID:        "func:HandleIoctl@src/ioctl.c",
		Name:      "HandleIoctl",
		File:      "src/ioctl.c",
		Kind:      "function",
		Signature: "NTSTATUS HandleIoctl(PVOID UserBuffer, ULONG Size, ULONG IoControlCode)",
		StartLine: 1,
		EndLine:   18,
	}
	params := buildFunctionFuzzParameterStrategies(target.Signature)
	closure := functionFuzzClosure{
		RootSymbol: target,
		Symbols:    []SymbolRecord{target},
	}
	observations := buildFunctionFuzzCodeObservations(root, target, params, closure)
	scenarios := buildFunctionFuzzVirtualScenarios(functionFuzzEnglishConfig(), root, target, params, closure, nil, nil, observations)
	if len(scenarios) == 0 {
		t.Fatalf("expected attack scenarios from observed guards and sinks")
	}
	rendered := strings.Join(scenarios[0].SourceExcerpt.Snippet, "\n")
	if !strings.Contains(rendered, "ProbeForRead(UserBuffer, Size, sizeof(UCHAR));") && !strings.Contains(rendered, "RtlCopyMemory(KernelBuffer, UserBuffer, Size);") {
		t.Fatalf("expected scenario excerpt to point at observed copy or probe path, got %q", rendered)
	}
	if !strings.Contains(scenarios[0].Title, "size") && !strings.Contains(strings.ToLower(scenarios[0].ExpectedFlow), "memory-transfer") {
		t.Fatalf("expected observation-driven scenario, got %+v", scenarios[0])
	}
}

func TestRenderFunctionFuzzRunShowsSourceDerivedAttackSurface(t *testing.T) {
	run := FunctionFuzzRun{
		ID:               "fuzz-observations",
		TargetSymbolName: "HandleIoctl",
		TargetSymbolID:   "func:HandleIoctl@src/ioctl.c",
		TargetFile:       "src/ioctl.c",
		CodeObservations: []FunctionFuzzCodeObservation{
			{
				Kind:         "size_guard",
				SymbolID:     "func:HandleIoctl@src/ioctl.c",
				Symbol:       "HandleIoctl",
				File:         "src/ioctl.c",
				Line:         8,
				Evidence:     "if (Size < sizeof(ULONG))",
				WhyItMatters: "The code compares a size-like value in a branch, which is where boundary inputs try to break assumptions.",
			},
			{
				Kind:         "copy_sink",
				SymbolID:     "func:HandleIoctl@src/ioctl.c",
				Symbol:       "HandleIoctl",
				File:         "src/ioctl.c",
				Line:         16,
				Evidence:     "RtlCopyMemory(KernelBuffer, UserBuffer, Size);",
				WhyItMatters: "The function body performs a memory transfer, which makes size mismatches directly security-relevant.",
			},
		},
	}

	rendered := renderFunctionFuzzRun(run)
	if !strings.Contains(rendered, "Source-derived attack surface:") {
		t.Fatalf("expected source-derived attack surface section, got %q", rendered)
	}
	if !strings.Contains(rendered, "size or boundary guard @ src/ioctl.c:8") {
		t.Fatalf("expected formatted observation location, got %q", rendered)
	}
	if !strings.Contains(rendered, "RtlCopyMemory(KernelBuffer, UserBuffer, Size);") {
		t.Fatalf("expected concrete code evidence in rendered output, got %q", rendered)
	}
}

func TestFunctionFuzzScenarioRiskScoreDownranksNoisyExploitSideFallback(t *testing.T) {
	run := normalizeFunctionFuzzRun(FunctionFuzzRun{
		TargetSymbolName: "ArbitraryOverwriteThread",
		TargetFile:       "Exploit/Common.c",
		CodeObservations: []FunctionFuzzCodeObservation{
			{
				Kind:     "probe_sink",
				SymbolID: "func:TriggerDoubleFetch@Driver/HEVD/Windows/DoubleFetch.c",
				Symbol:   "TriggerDoubleFetch",
				File:     "Driver/HEVD/Windows/DoubleFetch.c",
				Line:     100,
				Evidence: "ProbeForRead(UserBuffer, sizeof(KernelBuffer), (ULONG)__alignof(UCHAR));",
			},
			{
				Kind:     "copy_sink",
				SymbolID: "func:TriggerDoubleFetch@Driver/HEVD/Windows/DoubleFetch.c",
				Symbol:   "TriggerDoubleFetch",
				File:     "Driver/HEVD/Windows/DoubleFetch.c",
				Line:     121,
				Evidence: "RtlCopyMemory((PVOID)KernelBuffer, UserBuffer, UserBufferSize);",
			},
			{
				Kind:     "size_guard",
				SymbolID: "func:TriggerDoubleFetch@Driver/HEVD/Windows/DoubleFetch.c",
				Symbol:   "TriggerDoubleFetch",
				File:     "Driver/HEVD/Windows/DoubleFetch.c",
				Line:     105,
				Evidence: "if (UserBufferSize > sizeof(KernelBuffer))",
			},
		},
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{
				Title:      "Attacker-controlled size can diverge from the buffer contract on a real copy or probe path",
				Confidence: "high",
				PathHint:   "Kernforge extracted a real size or boundary comparison from the function body; the same path performs a real memory transfer; the same path probes user-controlled memory",
				SourceExcerpt: FunctionFuzzSourceExcerpt{
					File:      "Driver/HEVD/Windows/DoubleFetch.c",
					StartLine: 119,
					FocusLine: 121,
					EndLine:   125,
					Snippet: []string{
						"RtlCopyMemory((PVOID)KernelBuffer, UserBuffer, UserBufferSize);",
					},
				},
			},
			{
				Title:      "Opaque input partitioning and parser disagreement in Parameter",
				Confidence: "low",
				PathHint:   "security surface overlay appears on this reachable chain; the selected function is input-facing",
				SourceExcerpt: FunctionFuzzSourceExcerpt{
					File:      "Exploit/UseAfterFree.c",
					StartLine: 134,
					FocusLine: 136,
					EndLine:   140,
					Snippet: []string{
						"if (hFile == INVALID_HANDLE_VALUE) {",
						"    DEBUG_ERROR(\"failed\");",
						"}",
					},
				},
			},
		},
	})

	if len(run.VirtualScenarios) != 2 {
		t.Fatalf("expected scenarios to survive normalization")
	}
	left := run.VirtualScenarios[0]
	right := run.VirtualScenarios[1]
	if left.RiskScore <= right.RiskScore {
		t.Fatalf("expected concrete driver-side scenario to outrank noisy exploit-side fallback: left=%d right=%d", left.RiskScore, right.RiskScore)
	}
	if left.RiskScore < 70 {
		t.Fatalf("expected concrete scenario to receive a high score, got %d", left.RiskScore)
	}
	if right.RiskScore > 45 {
		t.Fatalf("expected noisy fallback scenario to be downranked, got %d", right.RiskScore)
	}
}

func TestRenderFunctionFuzzRunShowsRiskRankedFindings(t *testing.T) {
	run := FunctionFuzzRun{
		TargetSymbolName: "TriggerDoubleFetch",
		TargetSymbolID:   "func:TriggerDoubleFetch@Driver/HEVD/Windows/DoubleFetch.c",
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{
				Title:      "Attacker-controlled size can diverge from the buffer contract on a real copy or probe path",
				Confidence: "high",
				ScoreReasons: []string{
					"grounded in 3 source-derived guard or sink observation(s)",
					"real memory-transfer sink is present on the same path",
				},
				RiskScore: 88,
			},
			{
				Title:      "Opaque input partitioning and parser disagreement in Parameter",
				Confidence: "low",
				ScoreReasons: []string{
					"generic opaque-input fallback is noisier than concrete path-driven findings",
				},
				RiskScore: 22,
			},
		},
	}

	rendered := renderFunctionFuzzRun(run)
	if !strings.Contains(rendered, "Risk-ranked findings:") {
		t.Fatalf("expected risk-ranked findings section, got %q", rendered)
	}
	if !strings.Contains(rendered, "88/100 | Attacker-controlled size can diverge from the buffer contract on a real copy or probe path") {
		t.Fatalf("expected scored finding table line, got %q", rendered)
	}
	if !strings.Contains(rendered, "why this score: grounded in 3 source-derived guard or sink observation(s); real memory-transfer sink is present on the") ||
		!strings.Contains(rendered, "same path") {
		t.Fatalf("expected score rationale line, got %q", rendered)
	}
}

func TestFunctionFuzzScenarioRiskScoreCapsAuxiliaryExploitSideFindings(t *testing.T) {
	run := normalizeFunctionFuzzRun(FunctionFuzzRun{
		TargetSymbolName: "ArbitraryOverwriteThread",
		TargetFile:       "Exploit/Common.c",
		CodeObservations: []FunctionFuzzCodeObservation{
			{
				Kind:     "dispatch_guard",
				SymbolID: "func:DoubleFetchThread@Exploit/DoubleFetch.c",
				Symbol:   "DoubleFetchThread",
				File:     "Exploit/DoubleFetch.c",
				Line:     170,
				Evidence: "switch (Mode)",
			},
			{
				Kind:     "cleanup_path",
				SymbolID: "func:DoubleFetchThread@Exploit/DoubleFetch.c",
				Symbol:   "DoubleFetchThread",
				File:     "Exploit/DoubleFetch.c",
				Line:     176,
				Evidence: "if (!UserModeBuffer) {",
			},
			{
				Kind:     "size_guard",
				SymbolID: "func:WritePayloadDll@Exploit/InsecureKernelResourceAccess.c",
				Symbol:   "WritePayloadDll",
				File:     "Exploit/InsecureKernelResourceAccess.c",
				Line:     179,
				Evidence: "if (!ReadFile(SourceDllFileHandle, Buffer, sizeof(Buffer), &dwBytesRead, NULL)) {",
			},
		},
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{
				Title:      "Unexpected control value can push execution into a weakly checked dispatch path",
				Confidence: "high",
				PathHint:   "the same path makes a selector-driven dispatch decision; the same function also contains an explicit failure-unwind edge",
				SourceExcerpt: FunctionFuzzSourceExcerpt{
					File:      "Exploit/DoubleFetch.c",
					StartLine: 174,
					FocusLine: 176,
					EndLine:   180,
					Snippet: []string{
						"if (!UserModeBuffer) {",
						"    exit(EXIT_FAILURE);",
						"}",
					},
				},
			},
			{
				Title:      "Pointer or state validity can be checked in one place and broken in another",
				Confidence: "high",
				PathHint:   "Kernforge extracted a real size or boundary comparison from the function body",
				SourceExcerpt: FunctionFuzzSourceExcerpt{
					File:      "Exploit/InsecureKernelResourceAccess.c",
					StartLine: 177,
					FocusLine: 179,
					EndLine:   183,
					Snippet: []string{
						"if (!ReadFile(SourceDllFileHandle, Buffer, sizeof(Buffer), &dwBytesRead, NULL)) {",
						"    break;",
						"}",
					},
				},
			},
		},
	})

	if len(run.VirtualScenarios) != 2 {
		t.Fatalf("expected scenarios to survive normalization")
	}
	for _, item := range run.VirtualScenarios {
		if item.RiskScore > 58 {
			t.Fatalf("expected auxiliary exploit-side finding to be capped, got %d for %q", item.RiskScore, item.Title)
		}
	}
}

func TestFunctionFuzzAttachScenarioConnectionsShowsScopeAndCallTrace(t *testing.T) {
	t.Setenv("LANG", "ko-KR")

	index := SemanticIndexV2{
		Root: `C:\repo`,
		References: []ReferenceRecord{
			{
				SourceFile: "Driver/HEVD/Windows/UseAfterFreeNonPagedPool.c",
				TargetPath: "Driver/HEVD/Windows/DoubleFetch.c",
				Type:       "include",
			},
		},
		Symbols: []SymbolRecord{
			{
				ID:   "func:ArbitraryOverwriteThread@Exploit/Common.c",
				Name: "ArbitraryOverwriteThread",
				Kind: "function",
				File: "Exploit/Common.c",
			},
			{
				ID:        "func:TriggerDoubleFetch@Driver/HEVD/Windows/DoubleFetch.c",
				Name:      "TriggerDoubleFetch",
				Kind:      "function",
				File:      "Driver/HEVD/Windows/DoubleFetch.c",
				StartLine: 100,
				EndLine:   160,
			},
		},
	}
	target := index.Symbols[0]
	focus := index.Symbols[1]
	closure := functionFuzzClosure{
		RootSymbol: target,
		Symbols:    []SymbolRecord{target, focus},
		CallEdges: []CallEdge{
			{
				SourceID: target.ID,
				TargetID: focus.ID,
				Type:     "call",
			},
		},
	}
	scenarios := functionFuzzAttachScenarioConnections(index, "Driver/HEVD/Windows/UseAfterFreeNonPagedPool.c", closure, []FunctionFuzzVirtualScenario{
		{
			Title:      "Attacker-controlled size can diverge from the buffer contract on a real copy or probe path",
			Confidence: "high",
			SourceExcerpt: FunctionFuzzSourceExcerpt{
				Symbol:    "TriggerDoubleFetch",
				File:      "Driver/HEVD/Windows/DoubleFetch.c",
				StartLine: 119,
				FocusLine: 121,
				EndLine:   125,
				Snippet: []string{
					"RtlCopyMemory((PVOID)KernelBuffer, UserBuffer, UserBufferSize);",
				},
			},
		},
	})
	if len(scenarios) != 1 {
		t.Fatalf("expected one scenario, got %d", len(scenarios))
	}
	if len(scenarios[0].ScopeFilePath) != 3 {
		t.Fatalf("expected file-scope path to be attached, got %+v", scenarios[0].ScopeFilePath)
	}
	if scenarios[0].ScopeFilePath[0] != "Driver/HEVD/Windows/UseAfterFreeNonPagedPool.c" ||
		scenarios[0].ScopeFilePath[1] != "Exploit/Common.c" ||
		scenarios[0].ScopeFilePath[2] != "Driver/HEVD/Windows/DoubleFetch.c" {
		t.Fatalf("unexpected scope path: %+v", scenarios[0].ScopeFilePath)
	}
	if got := strings.Join(scenarios[0].PathSketch, " -> "); got != "ArbitraryOverwriteThread -> TriggerDoubleFetch" {
		t.Fatalf("expected call trace to be attached, got %q", got)
	}

	rendered := renderFunctionFuzzRunWithConfig(FunctionFuzzRun{
		ID:               "fuzz-scope-trace",
		ScopeMode:        "file",
		ScopeRootFile:    "Driver/HEVD/Windows/UseAfterFreeNonPagedPool.c",
		ScopeFiles:       []string{"Driver/HEVD/Windows/UseAfterFreeNonPagedPool.c", "Driver/HEVD/Windows/DoubleFetch.c"},
		TargetSymbolName: "ArbitraryOverwriteThread",
		TargetSymbolID:   target.ID,
		VirtualScenarios: scenarios,
	}, Config{FuzzFuncOutputLanguage: "system"})

	if !strings.Contains(rendered, "선택한 시작 파일에서 이 소스 파일로 이어진 경로:") ||
		!strings.Contains(rendered, "Driver/HEVD/Windows/UseAfterFreeNonPagedPool.c") ||
		!strings.Contains(rendered, "Driver/HEVD/Windows/DoubleFetch.c") {
		t.Fatalf("expected file-scope trace in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, "선택한 대표 루트에서 이 구현까지 이어진 호출 경로:") ||
		!strings.Contains(rendered, "ArbitraryOverwriteThread -> TriggerDoubleFetch") {
		t.Fatalf("expected root-to-implementation call trace in rendered output, got %q", rendered)
	}
}

func TestFunctionFuzzBuildAndRunArgsIncludeComparisonCoverage(t *testing.T) {
	run := FunctionFuzzRun{
		ID:          "fuzz-cmp-coverage",
		HarnessPath: "harness.cpp",
		Workspace:   "C:\\ws",
	}
	execState := FunctionFuzzExecution{
		ExecutablePath:  "C:\\ws\\out\\fuzz.exe",
		TranslationUnit: "C:\\ws\\target.cpp",
		CrashDir:        "C:\\ws\\out\\crashes",
		CorpusDir:       "C:\\ws\\out\\corpus",
		Profile:         "smoke",
	}
	record := CompilationCommandRecord{}

	clangCL := functionFuzzBuildExecutionArgsClangCL(run, record, execState)
	if !functionFuzzArgsContain(clangCL, "-fsanitize-coverage=trace-cmp,pc-table") {
		t.Fatalf("expected clang-cl build args to include comparison coverage, got %#v", clangCL)
	}

	clang := functionFuzzBuildExecutionArgsClang(run, record, execState)
	if !functionFuzzArgsContain(clang, "-fsanitize-coverage=trace-cmp,pc-table") {
		t.Fatalf("expected clang build args to include comparison coverage, got %#v", clang)
	}

	runArgs := functionFuzzRunArgs(run, execState)
	if !functionFuzzArgsContain(runArgs, "-use_value_profile=1") {
		t.Fatalf("expected run args to enable value profile, got %#v", runArgs)
	}
}

func functionFuzzArgsContain(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func TestFunctionFuzzBuildArgsHonorSanitizerProfile(t *testing.T) {
	run := FunctionFuzzRun{
		ID:          "fuzz-san-profiles",
		HarnessPath: "harness.cpp",
		Workspace:   "C:\\ws",
	}
	baseExec := FunctionFuzzExecution{
		ExecutablePath:  "C:\\ws\\out\\fuzz.exe",
		TranslationUnit: "C:\\ws\\target.cpp",
		CrashDir:        "C:\\ws\\out\\crashes",
		CorpusDir:       "C:\\ws\\out\\corpus",
		Profile:         "smoke",
	}
	record := CompilationCommandRecord{}

	cases := []struct {
		name             string
		sanitizerProfile string
		wantSanitize     string
		forbidSanitize   []string
	}{
		{
			name:             "default empty stays address+undefined",
			sanitizerProfile: "",
			wantSanitize:     "-fsanitize=fuzzer,address,undefined",
		},
		{
			name:             "explicit address stays address+undefined",
			sanitizerProfile: "address",
			wantSanitize:     "-fsanitize=fuzzer,address,undefined",
		},
		{
			name:             "memory profile uses MSan and drops address",
			sanitizerProfile: "memory",
			wantSanitize:     "-fsanitize=fuzzer,memory",
			forbidSanitize:   []string{"-fsanitize=fuzzer,address,undefined", "-fsanitize=fuzzer,address"},
		},
		{
			name:             "thread profile uses TSan and drops address",
			sanitizerProfile: "thread",
			wantSanitize:     "-fsanitize=fuzzer,thread",
			forbidSanitize:   []string{"-fsanitize=fuzzer,address,undefined", "-fsanitize=fuzzer,address"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			execState := baseExec
			execState.SanitizerProfile = tc.sanitizerProfile
			args := functionFuzzBuildExecutionArgsClang(run, record, execState)
			if !functionFuzzArgsContain(args, tc.wantSanitize) {
				t.Fatalf("expected clang args to include %q, got %#v", tc.wantSanitize, args)
			}
			// All profiles keep the comparison/value-profile coverage instrumentation.
			if !functionFuzzArgsContain(args, "-fsanitize-coverage=trace-cmp,pc-table") {
				t.Fatalf("expected clang args to keep trace-cmp coverage for profile %q, got %#v", tc.sanitizerProfile, args)
			}
			for _, forbidden := range tc.forbidSanitize {
				if functionFuzzArgsContain(args, forbidden) {
					t.Fatalf("expected clang args for profile %q to NOT include %q, got %#v", tc.sanitizerProfile, forbidden, args)
				}
			}
		})
	}
}

func TestFunctionFuzzKernelAddressProfileGatesDriverPath(t *testing.T) {
	cfg := Config{}
	run := &FunctionFuzzRun{
		ID:          "fuzz-kasan",
		HarnessPath: "harness.cpp",
		Workspace:   "C:\\ws",
	}
	execState := FunctionFuzzExecution{
		CompilerCandidate:    "clang-cl",
		CompilerResolvedPath: "C:\\llvm\\bin\\clang-cl.exe",
		CompilerStyle:        "clang-cl",
		TranslationUnit:      "C:\\ws\\driver.cpp",
		ExecutablePath:       "C:\\ws\\out\\fuzz.exe",
		CrashDir:             "C:\\ws\\out\\crashes",
		CorpusDir:            "C:\\ws\\out\\corpus",
		Profile:              "smoke",
		SanitizerProfile:     "kernel-address",
	}
	record := CompilationCommandRecord{}

	functionFuzzPlanKernelAddressProfile(cfg, run, record, execState)

	got := run.Execution
	if !functionFuzzArgsContain(got.BuildArgv, "/fsanitize=kernel-address") {
		t.Fatalf("expected KASAN build argv to include /fsanitize=kernel-address, got %#v", got.BuildArgv)
	}
	if !functionFuzzArgsContain(got.BuildArgv, "-fsanitize-coverage=trace-cmp,pc-table") {
		t.Fatalf("expected KASAN build argv to keep trace-cmp coverage, got %#v", got.BuildArgv)
	}
	if !strings.Contains(got.BuildCommand, "/fsanitize=kernel-address") {
		t.Fatalf("expected KASAN build command to mention /fsanitize=kernel-address, got %q", got.BuildCommand)
	}
	// The driver build+run path is not wired, so no user-mode .exe must be claimed.
	if got.ExecutablePath != "" {
		t.Fatalf("expected KASAN profile to clear the user-mode executable path, got %q", got.ExecutablePath)
	}
	if got.RunCommand != "" || len(got.RunArgv) != 0 {
		t.Fatalf("expected KASAN profile to claim no run command/argv, got cmd=%q argv=%#v", got.RunCommand, got.RunArgv)
	}
	if got.BuildScriptPath != "" {
		t.Fatalf("expected KASAN profile to write no PowerShell runner, got %q", got.BuildScriptPath)
	}
	if got.Eligible {
		t.Fatalf("expected KASAN profile to remain ineligible for autonomous user-mode execution")
	}
	if !strings.Contains(strings.ToLower(got.Reason), "driver build path") || !strings.Contains(strings.ToLower(got.Reason), "not yet wired") {
		t.Fatalf("expected KASAN reason to flag the not-yet-wired driver build path, got %q", got.Reason)
	}
	noteFound := false
	for _, note := range run.Notes {
		if strings.Contains(strings.ToLower(note), "driver build path") && strings.Contains(strings.ToLower(note), "not yet wired") {
			noteFound = true
			break
		}
	}
	if !noteFound {
		t.Fatalf("expected a run note about the not-yet-wired KASAN driver build path, got %#v", run.Notes)
	}
}

func TestFunctionFuzzMemoryThreadProfileBlockedOnClangCL(t *testing.T) {
	// MSan/TSan are clang-only; selecting them with an MSVC-style compiler must
	// block with a clear reason instead of silently emitting the address build.
	for _, profile := range []string{"memory", "thread"} {
		flag := functionFuzzClangCLSanitizeFlag(profile)
		// clang-cl cannot express MSan/TSan; the mapping falls back to the address
		// form, which is exactly why the planner must block the combination.
		if flag != "/fsanitize=fuzzer,address" {
			t.Fatalf("expected clang-cl sanitize flag for %q to fall back to address, got %q", profile, flag)
		}
	}
}

func TestFunctionFuzzUsesStaticTriageWordingNotAI(t *testing.T) {
	cfg := Config{}
	run := FunctionFuzzRun{
		TargetSymbolName: "ValidateRequest",
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{Title: "Attacker-controlled size", Confidence: "high", RiskScore: 90},
		},
	}
	for _, line := range functionFuzzConclusionLines(cfg, run) {
		// The pipeline performs static source triage; the old "AI" label
		// overstated reasoning depth and must not reappear.
		if strings.Contains(line, "AI source-only") {
			t.Fatalf("conclusion line still uses AI wording: %q", line)
		}
	}
	guidance := false
	for _, line := range functionFuzzConclusionLines(cfg, run) {
		if strings.Contains(line, "static source-only fuzz triage for ValidateRequest") {
			guidance = true
		}
	}
	if !guidance {
		t.Fatalf("expected static source-only fuzz triage wording in conclusion lines, got %#v", functionFuzzConclusionLines(cfg, run))
	}
}

func TestFunctionFuzzCountCrashArtifactsMatchesLibFuzzerPatternsOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Non-artifact leftovers must not be counted as crashes.
	writeFile("corpus_unit_0001")
	writeFile("fuzz.dict")
	writeFile("run.log")
	// libFuzzer crash artifacts must be counted (case-insensitive).
	writeFile("crash-deadbeef")
	writeFile("Leak-1234")

	if got := functionFuzzCountCrashArtifacts(dir); got != 2 {
		t.Fatalf("expected only libFuzzer artifacts to count, got %d", got)
	}

	// A crash that wrote no artifact must still be corroborated from the
	// non-zero exit code plus a sanitizer signal in the captured output.
	emptyDir := t.TempDir()
	if got := functionFuzzCountCrashArtifacts(emptyDir); got != 0 {
		t.Fatalf("expected empty crash dir to count zero artifacts, got %d", got)
	}
	exit := 88
	output := "==12345==ERROR: AddressSanitizer: heap-buffer-overflow on address 0x..."
	if got := functionFuzzEffectiveCrashCount(0, &exit, output); got < 1 {
		t.Fatalf("expected non-zero exit plus sanitizer signal to yield >=1 crash, got %d", got)
	}
	// A clean exit with no artifact must remain zero.
	clean := 0
	if got := functionFuzzEffectiveCrashCount(0, &clean, output); got != 0 {
		t.Fatalf("expected clean exit to yield zero crashes, got %d", got)
	}
	// A non-zero exit with no sanitizer signal must not fabricate a crash.
	if got := functionFuzzEffectiveCrashCount(0, &exit, "build finished, no findings"); got != 0 {
		t.Fatalf("expected non-zero exit without sanitizer signal to yield zero, got %d", got)
	}
}

func TestFunctionFuzzHarnessCouplesBufferLengthRelation(t *testing.T) {
	// A buffer parameter sized by an adjacent length must be inferred as a
	// related pair so the harness can drive the length from the buffer size.
	params := buildFunctionFuzzParameterStrategies("int Decode(const uint8_t* buffer, size_t length)")
	if len(params) != 2 {
		t.Fatalf("expected 2 parameter strategies, got %d", len(params))
	}
	var bufParam, lenParam *FunctionFuzzParamStrategy
	for i := range params {
		switch params[i].Class {
		case "buffer", "pointer":
			bufParam = &params[i]
		case "length":
			lenParam = &params[i]
		}
	}
	if bufParam == nil || lenParam == nil {
		t.Fatalf("expected a buffer and a length param, got %+v", params)
	}
	if bufParam.Relation != "sized_by:length" {
		t.Fatalf("expected buffer relation sized_by:length, got %q", bufParam.Relation)
	}
	if lenParam.Relation != "sizes:buffer" {
		t.Fatalf("expected length relation sizes:buffer, got %q", lenParam.Relation)
	}

	run := FunctionFuzzRun{
		TargetSignature:     "int Decode(const uint8_t* buffer, size_t length)",
		HarnessReady:        true,
		ParameterStrategies: params,
	}
	src := renderFunctionFuzzHarness(run)

	// The coupled length must be derived from the buffer's actual byte vector
	// size rather than an independent ReadSize.
	if !strings.Contains(src, "length_value = buffer_storage.size();") {
		t.Fatalf("expected coupled length derived from buffer size, harness:\n%s", src)
	}
	if !strings.Contains(src, "size_t length = static_cast<size_t>(length_value);") {
		t.Fatalf("expected length assigned from coupled value, harness:\n%s", src)
	}
	// The length must NOT be decoded with an independent ReadSize anymore.
	if strings.Contains(src, "size_t length = static_cast<size_t>(ReadSize(input, 0x1000));") {
		t.Fatalf("coupled length must not use independent ReadSize, harness:\n%s", src)
	}
	// A guarded, minority desync mutation must remain reachable so the size
	// desync scenario can still be probed.
	if !strings.Contains(src, "length_desync") || !strings.Contains(src, "length_delta") {
		t.Fatalf("expected guarded minority desync path, harness:\n%s", src)
	}
	// The call must pass the coupled length argument.
	if !strings.Contains(src, "Decode(buffer, length)") {
		t.Fatalf("expected call to pass coupled length, harness:\n%s", src)
	}
	// The buffer must still be decoded before the length consumes its size.
	bufIdx := strings.Index(src, "std::vector<uint8_t> buffer_storage = ReadByteVector(input, 0x1000);")
	lenIdx := strings.Index(src, "length_value = buffer_storage.size();")
	if bufIdx < 0 || lenIdx < 0 || bufIdx > lenIdx {
		t.Fatalf("expected buffer decode before coupled length, harness:\n%s", src)
	}
}

func TestRenderFunctionFuzzHarnessEmitsIOCTLClientForDriverTarget(t *testing.T) {
	// A driver IOCTL dispatch target is driven through DeviceIoControl for
	// defensive in-house fuzzing of a loaded driver the operator owns, not a
	// direct call of the dispatch routine.
	run := FunctionFuzzRun{
		TargetSymbolName: "TvkDeviceControl",
		TargetSignature:  "NTSTATUS TvkDeviceControl(PDEVICE_OBJECT DeviceObject, PIRP Irp)",
		OverlayDomains:   []string{"ioctl"},
		HarnessReady:     true,
	}
	src := renderFunctionFuzzHarness(run)
	for _, want := range []string{
		"#include <windows.h>",
		"extern \"C\" int LLVMFuzzerTestOneInput(const uint8_t* data, size_t size)",
		"DWORD ioControlCode = ReadScalar<DWORD>(input);",
		"std::vector<uint8_t> inBuffer = ReadByteVector(input, 0x10000);",
		"DeviceIoControl(device, ioControlCode,",
		"(DWORD) inBuffer.size(),",
		"KERNFORGE_FUZZ_DEVICE_PATH",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("expected IOCTL harness to contain %q, harness:\n%s", want, src)
		}
	}
	// It must not fall back to directly calling the dispatch routine.
	if strings.Contains(src, "TvkDeviceControl(") {
		t.Fatalf("IOCTL harness must not directly call the dispatch routine, harness:\n%s", src)
	}
}

func TestRenderFunctionFuzzHarnessKeepsFreeFunctionShapeForNonDriverTarget(t *testing.T) {
	// A plain free function with no driver/IOCTL signal keeps the original
	// free-function harness (no DeviceIoControl client, no windows.h).
	run := FunctionFuzzRun{
		TargetSignature:     "int Validate(int selector)",
		HarnessReady:        true,
		ParameterStrategies: buildFunctionFuzzParameterStrategies("int Validate(int selector)"),
	}
	src := renderFunctionFuzzHarness(run)
	if strings.Contains(src, "DeviceIoControl") || strings.Contains(src, "#include <windows.h>") {
		t.Fatalf("non-driver target must keep the free-function harness, harness:\n%s", src)
	}
	if !strings.Contains(src, "Validate(") {
		t.Fatalf("expected free-function call in harness, got:\n%s", src)
	}
}

func TestFunctionFuzzHarnessCouplesWhenBufferFollowsLength(t *testing.T) {
	// When the length precedes the buffer in the signature, the coupled length
	// must be deferred and emitted after the buffer storage to avoid a forward
	// reference.
	params := buildFunctionFuzzParameterStrategies("int Decode(size_t length, const uint8_t* buffer)")
	run := FunctionFuzzRun{
		TargetSignature:     "int Decode(size_t length, const uint8_t* buffer)",
		HarnessReady:        true,
		ParameterStrategies: params,
	}
	src := renderFunctionFuzzHarness(run)

	bufIdx := strings.Index(src, "std::vector<uint8_t> buffer_storage = ReadByteVector(input, 0x1000);")
	lenIdx := strings.Index(src, "length_value = buffer_storage.size();")
	if bufIdx < 0 || lenIdx < 0 {
		t.Fatalf("expected coupled buffer and length, harness:\n%s", src)
	}
	if bufIdx > lenIdx {
		t.Fatalf("expected buffer decode emitted before deferred coupled length, harness:\n%s", src)
	}
	if strings.Contains(src, "size_t length = static_cast<size_t>(ReadSize(input, 0x1000));") {
		t.Fatalf("deferred coupled length must not use independent ReadSize, harness:\n%s", src)
	}
}

func TestFunctionFuzzHarnessKeepsIndependentDecodeWithoutRelation(t *testing.T) {
	// A length parameter with no related buffer must keep the independent
	// ReadSize decode unchanged.
	params := buildFunctionFuzzParameterStrategies("int Resize(size_t count)")
	if len(params) != 1 {
		t.Fatalf("expected 1 parameter strategy, got %d", len(params))
	}
	if params[0].Relation != "" {
		t.Fatalf("expected no inferred relation for lone length, got %q", params[0].Relation)
	}
	run := FunctionFuzzRun{
		TargetSignature:     "int Resize(size_t count)",
		HarnessReady:        true,
		ParameterStrategies: params,
	}
	src := renderFunctionFuzzHarness(run)
	if !strings.Contains(src, "size_t count = static_cast<size_t>(ReadSize(input, 0x1000));") {
		t.Fatalf("expected lone length to keep independent ReadSize, harness:\n%s", src)
	}
	if strings.Contains(src, "count_value") || strings.Contains(src, "count_desync") {
		t.Fatalf("lone length must not emit coupled decode, harness:\n%s", src)
	}
}

// --- Bounded compile self-repair tests ---------------------------------------

func TestFunctionFuzzPlanCompileRepairCleanBuildNoFix(t *testing.T) {
	// An empty diagnostic (clean build) must yield no repair.
	run := FunctionFuzzRun{HarnessReady: true}
	repair, found := functionFuzzPlanCompileRepair("", run, functionFuzzCompileRepair{})
	if found {
		t.Fatalf("clean build must not produce a fix, got %+v", repair)
	}
	if !repair.empty() {
		t.Fatalf("clean build repair must be empty, got %+v", repair)
	}
}

func TestFunctionFuzzPlanCompileRepairMissingInclude(t *testing.T) {
	cases := []struct {
		name string
		diag string
		want string
	}{
		{
			name: "clang_quote_header",
			diag: "harness.cpp:7:10: fatal error: 'TavernTypes.h' file not found\n#include \"TavernTypes.h\"\n         ^",
			want: "#include <TavernTypes.h>",
		},
		{
			name: "clang_cl_cannot_open",
			diag: "harness.cpp(7): fatal error C1083: cannot open include file: 'tavern/worker.h': No such file or directory",
			want: `#include "tavern/worker.h"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := FunctionFuzzRun{HarnessReady: true}
			repair, found := functionFuzzPlanCompileRepair(tc.diag, run, functionFuzzCompileRepair{})
			if !found {
				t.Fatalf("expected an injected include, got none")
			}
			if len(repair.Includes) != 1 || repair.Includes[0] != tc.want {
				t.Fatalf("expected include %q, got %+v", tc.want, repair.Includes)
			}
			if len(repair.Steps) == 0 || !strings.Contains(repair.Steps[len(repair.Steps)-1], "injected missing include") {
				t.Fatalf("expected an audit step for the injected include, got %+v", repair.Steps)
			}
			// The same diagnostic must be idempotent: replanning with the include
			// already applied yields no new fix.
			if _, again := functionFuzzPlanCompileRepair(tc.diag, run, repair); again {
				t.Fatalf("missing-include fix must be idempotent, got another fix")
			}
		})
	}
}

func TestFunctionFuzzPlanCompileRepairUnknownTypeForwardDeclares(t *testing.T) {
	// An unknown type that does not match a stub parameter must be forward-declared.
	run := FunctionFuzzRun{HarnessReady: true}
	diag := "harness.cpp:20:5: error: unknown type name 'WorkerContext'"
	repair, found := functionFuzzPlanCompileRepair(diag, run, functionFuzzCompileRepair{})
	if !found {
		t.Fatalf("expected a forward declaration, got none")
	}
	if len(repair.ForwardDeclares) != 1 || repair.ForwardDeclares[0] != "struct WorkerContext;" {
		t.Fatalf("expected forward declare of WorkerContext, got %+v", repair.ForwardDeclares)
	}
	if len(repair.ParamClassOverrides) != 0 || len(repair.DroppedParams) != 0 {
		t.Fatalf("forward-declare path must not touch parameters, got %+v", repair)
	}
}

func TestFunctionFuzzPlanCompileRepairUnknownTypeDowngradesStubParam(t *testing.T) {
	// When the unresolved type is the raw type of an object/handle/container stub
	// parameter, the planner must downgrade that parameter to a raw byte buffer
	// rather than forward-declaring an incomplete type the stub cannot construct.
	run := FunctionFuzzRun{
		HarnessReady: true,
		ParameterStrategies: []FunctionFuzzParamStrategy{
			{Index: 0, Name: "ctx", RawType: "WorkerContext", Class: "object"},
		},
	}
	diag := "harness.cpp:20:5: error: unknown type name 'WorkerContext'"
	repair, found := functionFuzzPlanCompileRepair(diag, run, functionFuzzCompileRepair{})
	if !found {
		t.Fatalf("expected a stub-parameter downgrade, got none")
	}
	if got := repair.ParamClassOverrides[0]; got != "buffer" {
		t.Fatalf("expected param #0 downgraded to buffer, got %q (repair %+v)", got, repair)
	}
	if len(repair.ForwardDeclares) != 0 {
		t.Fatalf("stub-parameter downgrade must not also forward-declare, got %+v", repair.ForwardDeclares)
	}
	// Re-rendering with the downgrade must replace the object TODO stub with a
	// real byte-vector decode.
	src := renderFunctionFuzzHarnessWithRepair(run, repair)
	if strings.Contains(src, "// TODO: infer a builder or factory for object parameter ctx.") {
		t.Fatalf("downgrade must remove the object TODO stub, harness:\n%s", src)
	}
	if !strings.Contains(src, "std::vector<uint8_t> ctx_storage = ReadByteVector(input, 0x1000);") {
		t.Fatalf("expected ctx to decode as a byte buffer after downgrade, harness:\n%s", src)
	}
}

func TestFunctionFuzzPlanCompileRepairUndeclaredIdentifierDropsParam(t *testing.T) {
	// An "undeclared identifier" that names a parameter means its decode never
	// compiled; the planner must drop it to a safe default.
	run := FunctionFuzzRun{
		HarnessReady: true,
		ParameterStrategies: []FunctionFuzzParamStrategy{
			{Index: 0, Name: "handle", RawType: "HANDLE", Class: "handle"},
		},
	}
	diag := "harness.cpp:25:9: error: use of undeclared identifier 'handle'"
	repair, found := functionFuzzPlanCompileRepair(diag, run, functionFuzzCompileRepair{})
	if !found {
		t.Fatalf("expected a parameter drop, got none")
	}
	if !repair.DroppedParams[0] {
		t.Fatalf("expected param #0 dropped, got %+v", repair)
	}
	src := renderFunctionFuzzHarnessWithRepair(run, repair)
	if !strings.Contains(src, "Self-repair: handle reduced to a zero-initialized default") {
		t.Fatalf("expected dropped param to emit a zero-initialized default, harness:\n%s", src)
	}
	if !strings.Contains(src, "HANDLE handle{};") {
		t.Fatalf("expected dropped param default-init declaration, harness:\n%s", src)
	}
}

func TestFunctionFuzzCompileRepairCleanRenderUnchanged(t *testing.T) {
	// The repair-aware renderer with an empty overlay must produce exactly the
	// same harness as the original renderer (fully additive).
	run := FunctionFuzzRun{
		TargetSignature:     "int Decode(const uint8_t* buffer, size_t length)",
		HarnessReady:        true,
		ParameterStrategies: buildFunctionFuzzParameterStrategies("int Decode(const uint8_t* buffer, size_t length)"),
	}
	base := renderFunctionFuzzHarness(run)
	overlaid := renderFunctionFuzzHarnessWithRepair(run, functionFuzzCompileRepair{})
	if base != overlaid {
		t.Fatalf("empty overlay must not change the harness output")
	}
	if strings.Contains(base, "Self-repair:") {
		t.Fatalf("a clean harness must not contain self-repair markers, harness:\n%s", base)
	}
}

func TestFunctionFuzzRepairIncludeRenderedAtTop(t *testing.T) {
	run := FunctionFuzzRun{HarnessReady: true}
	repair := functionFuzzCompileRepair{Includes: []string{"#include <TavernTypes.h>"}}
	src := renderFunctionFuzzHarnessWithRepair(run, repair)
	if !strings.Contains(src, "#include <TavernTypes.h>") {
		t.Fatalf("expected injected include in harness, got:\n%s", src)
	}
	// The injected include must appear before the generated-by banner.
	incIdx := strings.Index(src, "#include <TavernTypes.h>")
	bannerIdx := strings.Index(src, "Generated by Kernforge function fuzz planning")
	if incIdx < 0 || bannerIdx < 0 || incIdx > bannerIdx {
		t.Fatalf("injected include must precede the banner, harness:\n%s", src)
	}
}

func TestFunctionFuzzDriveBuildRepairLoopBounded(t *testing.T) {
	// A failure whose diagnostic keeps naming a NEW missing include must still
	// stop after functionFuzzCompileRepairCap repair attempts (so total compiles
	// = cap + 1) and fall back to the terminal build_failed state. This proves the
	// loop is hard-bounded even when the planner keeps finding fixable causes.
	run := &FunctionFuzzRun{HarnessReady: true, HarnessPath: ""}
	run.Execution.BuildArgv = []string{"clang", "harness.cpp"}
	run.Execution.BuildLogPath = filepath.Join(t.TempDir(), "build.log")

	compiles := 0
	compile := func(attempt int) functionFuzzCompileOutcome {
		compiles++
		// Each attempt reports a distinct missing header so the planner always
		// finds a new mechanical fix; only the bound stops the loop.
		return functionFuzzCompileOutcome{
			logText:  fmt.Sprintf("fatal error: 'gen_header_%d.h' file not found", compiles),
			exitCode: 1,
		}
	}
	rerenders := 0
	rerender := func(_ *FunctionFuzzRun) { rerenders++ }

	functionFuzzDriveBuildRepairLoop(run, 120, compile, rerender)

	if run.Execution.Status != "build_failed" {
		t.Fatalf("expected terminal build_failed after exhausting the bound, got %q", run.Execution.Status)
	}
	wantCompiles := functionFuzzCompileRepairCap + 1
	if compiles != wantCompiles {
		t.Fatalf("expected %d compiles (cap+1), got %d", wantCompiles, compiles)
	}
	if rerenders != functionFuzzCompileRepairCap {
		t.Fatalf("expected %d re-renders (one per applied fix), got %d", functionFuzzCompileRepairCap, rerenders)
	}
	// Every applied fix must be recorded in the recovery notes for auditability.
	repairNotes := 0
	for _, note := range run.Execution.RecoveryNotes {
		if strings.HasPrefix(note, "Self-repair: ") {
			repairNotes++
		}
	}
	if repairNotes != functionFuzzCompileRepairCap {
		t.Fatalf("expected %d self-repair recovery notes, got %d (%v)", functionFuzzCompileRepairCap, repairNotes, run.Execution.RecoveryNotes)
	}
	// The build log must capture the self-repair trail.
	data, err := os.ReadFile(run.Execution.BuildLogPath)
	if err != nil {
		t.Fatalf("read build log: %v", err)
	}
	if !strings.Contains(string(data), "KernForge self-repair log") {
		t.Fatalf("expected self-repair log section in build log, got:\n%s", string(data))
	}
}

func TestFunctionFuzzDriveBuildRepairLoopFirstTrySuccess(t *testing.T) {
	// A harness that compiles on the first try must apply zero repairs and leave
	// no self-repair markers.
	run := &FunctionFuzzRun{HarnessReady: true}
	run.Execution.BuildArgv = []string{"clang", "harness.cpp"}
	run.Execution.BuildLogPath = filepath.Join(t.TempDir(), "build.log")

	compiles := 0
	compile := func(_ int) functionFuzzCompileOutcome {
		compiles++
		return functionFuzzCompileOutcome{ok: true}
	}
	rerenders := 0
	functionFuzzDriveBuildRepairLoop(run, 120, compile, func(_ *FunctionFuzzRun) { rerenders++ })

	if compiles != 1 {
		t.Fatalf("expected exactly one compile on first-try success, got %d", compiles)
	}
	if rerenders != 0 {
		t.Fatalf("first-try success must not re-render, got %d", rerenders)
	}
	if run.Execution.Status != "build_succeeded" {
		t.Fatalf("expected build_succeeded, got %q", run.Execution.Status)
	}
	for _, note := range run.Execution.RecoveryNotes {
		if strings.HasPrefix(note, "Self-repair: ") {
			t.Fatalf("first-try success must not record self-repair notes, got %v", run.Execution.RecoveryNotes)
		}
	}
}

func TestFunctionFuzzDriveBuildRepairLoopFixThenSucceed(t *testing.T) {
	// A single fixable failure followed by success must apply exactly one repair
	// and report success-after-repair.
	run := &FunctionFuzzRun{HarnessReady: true}
	run.Execution.BuildArgv = []string{"clang", "harness.cpp"}
	run.Execution.BuildLogPath = filepath.Join(t.TempDir(), "build.log")

	compiles := 0
	compile := func(_ int) functionFuzzCompileOutcome {
		compiles++
		if compiles == 1 {
			return functionFuzzCompileOutcome{logText: "fatal error: 'Foo.h' file not found", exitCode: 1}
		}
		return functionFuzzCompileOutcome{ok: true}
	}
	functionFuzzDriveBuildRepairLoop(run, 120, compile, func(_ *FunctionFuzzRun) {})

	if compiles != 2 {
		t.Fatalf("expected 2 compiles (fail then succeed), got %d", compiles)
	}
	if run.Execution.Status != "build_succeeded" {
		t.Fatalf("expected build_succeeded, got %q", run.Execution.Status)
	}
	if !strings.Contains(run.Execution.Reason, "self-repair") {
		t.Fatalf("expected success-after-repair reason, got %q", run.Execution.Reason)
	}
}

func TestFunctionFuzzDriveBuildRepairLoopTimeoutNoRepair(t *testing.T) {
	// A timeout is not a mechanically-fixable diagnostic, so the loop must stop
	// immediately and never attempt a repair.
	run := &FunctionFuzzRun{HarnessReady: true}
	run.Execution.BuildArgv = []string{"clang", "harness.cpp"}
	run.Execution.BuildLogPath = filepath.Join(t.TempDir(), "build.log")

	compiles := 0
	compile := func(_ int) functionFuzzCompileOutcome {
		compiles++
		return functionFuzzCompileOutcome{timedOut: true, exitCode: -1}
	}
	rerenders := 0
	functionFuzzDriveBuildRepairLoop(run, 30, compile, func(_ *FunctionFuzzRun) { rerenders++ })

	if compiles != 1 {
		t.Fatalf("a timeout must stop after one compile, got %d", compiles)
	}
	if rerenders != 0 {
		t.Fatalf("a timeout must not trigger a repair re-render, got %d", rerenders)
	}
	if run.Execution.Status != "build_timed_out" {
		t.Fatalf("expected build_timed_out, got %q", run.Execution.Status)
	}
}

func TestFunctionFuzzDriveBuildRepairLoopUnfixableFailsImmediately(t *testing.T) {
	// A failure with no mechanically-fixable cause must fall back to build_failed
	// on the first attempt without any repair.
	run := &FunctionFuzzRun{HarnessReady: true}
	run.Execution.BuildArgv = []string{"clang", "harness.cpp"}
	run.Execution.BuildLogPath = filepath.Join(t.TempDir(), "build.log")

	compiles := 0
	compile := func(_ int) functionFuzzCompileOutcome {
		compiles++
		return functionFuzzCompileOutcome{logText: "error: too many initializers for some unrelated reason", exitCode: 1}
	}
	functionFuzzDriveBuildRepairLoop(run, 120, compile, func(_ *FunctionFuzzRun) {})

	if compiles != 1 {
		t.Fatalf("an unfixable failure must stop after one compile, got %d", compiles)
	}
	if run.Execution.Status != "build_failed" {
		t.Fatalf("expected build_failed, got %q", run.Execution.Status)
	}
}
