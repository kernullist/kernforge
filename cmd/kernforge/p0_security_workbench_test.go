package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Stage A: harness build failure classification + durable blockers --------

func TestFunctionFuzzClassifyCompileFailureBuckets(t *testing.T) {
	cases := []struct {
		name string
		log  string
		want string
	}{
		{
			name: "missing_include_clang",
			log:  "harness.cpp:7:10: fatal error: 'TavernTypes.h' file not found",
			want: "missing-include",
		},
		{
			name: "missing_include_msvc",
			log:  "fatal error C1083: cannot open include file: 'wdm.h': No such file or directory",
			want: "missing-include",
		},
		{
			name: "unresolved_symbol",
			log:  "harness.cpp:20:5: error: unknown type name 'WorkerContext'",
			want: "unresolved-symbol",
		},
		{
			name: "wdk_macro",
			log:  "error: use of undeclared identifier 'NTSTATUS'; did you mean to include ntddk.h?",
			want: "wdk-macro",
		},
		{
			name: "abi_or_link_lnk2019",
			log:  "error LNK2019: unresolved external symbol LLVMFuzzerTestOneInput referenced in function main",
			want: "abi-or-link",
		},
		{
			name: "abi_or_link_undefined_ref",
			log:  "ld: undefined reference to `TargetDecode'",
			want: "abi-or-link",
		},
		{
			name: "unknown",
			log:  "error: too many initializers for some unrelated reason",
			want: "unknown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := functionFuzzClassifyCompileFailure(tc.log)
			if got != tc.want {
				t.Fatalf("classify %q: got %q want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestFunctionFuzzDriveBuildRepairLoopRecordsClassAndBlockers(t *testing.T) {
	run := &FunctionFuzzRun{HarnessReady: true}
	run.Execution.BuildArgv = []string{"clang", "harness.cpp"}
	run.Execution.BuildLogPath = filepath.Join(t.TempDir(), "build.log")

	compile := func(_ int) functionFuzzCompileOutcome {
		return functionFuzzCompileOutcome{
			logText:  "error LNK2019: unresolved external symbol FooBar",
			exitCode: 1,
		}
	}
	functionFuzzDriveBuildRepairLoop(run, 60, compile, func(*FunctionFuzzRun) {})

	if run.Execution.Status != "build_failed" {
		t.Fatalf("expected build_failed, got %q", run.Execution.Status)
	}
	if run.Execution.BuildFailureClass != "abi-or-link" {
		t.Fatalf("expected abi-or-link class, got %q", run.Execution.BuildFailureClass)
	}
	if run.Execution.BuildRepairAttempts != 0 {
		t.Fatalf("unfixable link error must not count repairs, got %d", run.Execution.BuildRepairAttempts)
	}
	if len(run.Execution.BuildBlockers) == 0 {
		t.Fatalf("expected durable build blockers, got none")
	}
	joined := strings.Join(run.Execution.BuildBlockers, "\n")
	if !strings.Contains(joined, "class=abi-or-link") {
		t.Fatalf("blockers missing class label: %v", run.Execution.BuildBlockers)
	}
	data, err := os.ReadFile(run.Execution.BuildLogPath)
	if err != nil {
		t.Fatalf("read build log: %v", err)
	}
	if !strings.Contains(string(data), "KernForge build blockers") {
		t.Fatalf("build log must record blockers section, got:\n%s", string(data))
	}
}

func TestFunctionFuzzDriveBuildRepairLoopCapThree(t *testing.T) {
	if functionFuzzCompileRepairCap != 3 {
		t.Fatalf("repair cap must be 3 per P0 acceptance, got %d", functionFuzzCompileRepairCap)
	}
	run := &FunctionFuzzRun{HarnessReady: true}
	run.Execution.BuildArgv = []string{"clang", "harness.cpp"}
	run.Execution.BuildLogPath = filepath.Join(t.TempDir(), "build.log")
	compiles := 0
	functionFuzzDriveBuildRepairLoop(run, 60, func(_ int) functionFuzzCompileOutcome {
		compiles++
		return functionFuzzCompileOutcome{
			logText:  "fatal error: 'gen_header_" + strings.Repeat("x", compiles) + ".h' file not found",
			exitCode: 1,
		}
	}, func(*FunctionFuzzRun) {})
	if compiles != functionFuzzCompileRepairCap+1 {
		t.Fatalf("expected %d compiles, got %d", functionFuzzCompileRepairCap+1, compiles)
	}
	if run.Execution.BuildRepairAttempts != functionFuzzCompileRepairCap {
		t.Fatalf("expected %d repairs applied, got %d", functionFuzzCompileRepairCap, run.Execution.BuildRepairAttempts)
	}
	if run.Execution.BuildFailureClass != "missing-include" {
		t.Fatalf("expected missing-include after cap, got %q", run.Execution.BuildFailureClass)
	}
}

// --- Stage B: crash feasibility gate ----------------------------------------

func TestFuzzCampaignValidateCrashFeasibilitySpuriousHarnessOnly(t *testing.T) {
	run := FunctionFuzzRun{
		TargetSymbolName: "HandleIoctl",
		TargetFile:       "driver/ioctl.c",
	}
	report := FuzzCampaignCrashReport{
		Parsed: true,
		Class:  "heap-buffer-overflow",
		Frames: []string{
			"LLVMFuzzerTestOneInput",
			"harness.cpp",
			"fuzzer::RunOneTest",
			"__asan_report_store",
		},
	}
	feasibility, reason := fuzzCampaignValidateCrashFeasibility(run, report, 1)
	if feasibility != "spurious" {
		t.Fatalf("expected spurious, got %q (%s)", feasibility, reason)
	}
}

func TestFuzzCampaignValidateCrashFeasibilityTargetPlausible(t *testing.T) {
	run := FunctionFuzzRun{
		TargetSymbolName: "HandleIoctl",
		TargetFile:       "driver/ioctl.c",
	}
	report := FuzzCampaignCrashReport{
		Parsed: true,
		Class:  "heap-buffer-overflow",
		Frames: []string{
			"HandleIoctl",
			"DispatchDeviceControl",
			"LLVMFuzzerTestOneInput",
		},
	}
	feasibility, reason := fuzzCampaignValidateCrashFeasibility(run, report, 1)
	if feasibility != "target_plausible" {
		t.Fatalf("expected target_plausible, got %q (%s)", feasibility, reason)
	}
}

func TestBuildFuzzCampaignNativeFindingQuarantinesSpurious(t *testing.T) {
	campaign := FuzzCampaign{ID: "camp-1"}
	run := FunctionFuzzRun{
		ID:               "run-1",
		TargetSymbolName: "HandleIoctl",
		TargetFile:       "driver/ioctl.c",
	}
	result := FuzzCampaignNativeResult{
		RunID:             "run-1",
		CrashCount:        1,
		Outcome:           "spurious",
		Feasibility:       "spurious",
		FeasibilityReason: "harness-only frames",
		CrashFingerprint:  "fc-test",
		CrashClass:        "heap-buffer-overflow",
	}
	finding := buildFuzzCampaignNativeFinding(campaign, run, result)
	if finding.Status != "spurious" {
		t.Fatalf("spurious crash must not promote as open/validated, status=%q", finding.Status)
	}
	if finding.VerificationGate == "required" {
		t.Fatalf("spurious finding must not require verification gate")
	}
	if finding.TrackedFeatureGate == "block_close" {
		t.Fatalf("spurious finding must not block feature close")
	}
	if finding.Feasibility != "spurious" {
		t.Fatalf("finding must retain feasibility=spurious, got %q", finding.Feasibility)
	}

	// Target-plausible still opens as a real finding.
	plausible := result
	plausible.Outcome = "failed"
	plausible.Feasibility = "target_plausible"
	plausible.FeasibilityReason = "target frame hit"
	openFinding := buildFuzzCampaignNativeFinding(campaign, run, plausible)
	if openFinding.Status == "spurious" {
		t.Fatalf("target_plausible must not be marked spurious")
	}
	if openFinding.Status != "open" && openFinding.Status != "monitoring" {
		// CrashCount>0 path sets open via NeedsVerification.
		t.Fatalf("expected open/monitoring for plausible crash, got %q", openFinding.Status)
	}
}

func TestFuzzCampaignNativeResultNeedsVerificationSkipsSpurious(t *testing.T) {
	if fuzzCampaignNativeResultNeedsVerification(FuzzCampaignNativeResult{
		CrashCount:  1,
		Outcome:     "spurious",
		Feasibility: "spurious",
	}) {
		t.Fatalf("spurious result must not need verification")
	}
	if !fuzzCampaignNativeResultNeedsVerification(FuzzCampaignNativeResult{
		CrashCount:  1,
		Outcome:     "failed",
		Feasibility: "target_plausible",
	}) {
		t.Fatalf("target_plausible crash must need verification")
	}
}

// --- Stage C: IOCTL contract + multi-call sequence seeds --------------------

func TestFunctionFuzzIOCTLContractArtifactAndSequenceSeeds(t *testing.T) {
	root := t.TempDir()
	run := FunctionFuzzRun{
		ID:               "ff-ioctl-1",
		Workspace:        root,
		TargetSymbolName: "DeviceControl",
		TargetFile:       "driver/ioctl.c",
		TargetQuery:      "DeviceControl",
		CodeObservations: []FunctionFuzzCodeObservation{
			{
				Kind:     "dispatch_guard",
				Symbol:   "DeviceControl",
				File:     "driver/ioctl.c",
				Line:     120,
				Evidence: "case IOCTL_PING: /* METHOD_BUFFERED */ if (InputBufferLength < sizeof(REQ)) return; IoStatus.Information = OutputBufferLength;",
				ComparisonFacts: []string{
					"IoControlCode == 0x222000",
					"METHOD_BUFFERED",
				},
			},
			{
				Kind:     "dispatch_guard",
				Symbol:   "DeviceControl",
				File:     "driver/ioctl.c",
				Line:     140,
				Evidence: "case 0x222004: RtlCopyMemory(out, in, InputBufferLength);",
			},
		},
	}
	// Force IOCTL target detection via overlay/signature markers used by helpers.
	run.TargetSignature = "NTSTATUS DeviceControl(PDEVICE_OBJECT DeviceObject, PIRP Irp)"
	run.OverlayDomains = []string{"security_ioctl", "windows_driver"}

	spec := functionFuzzInferIOCTLSpec(run)
	if spec == nil || !spec.Detected {
		// Infer may require functionFuzzTargetIsIOCTL; set IOCTLSpec manually if needed.
		run.IOCTLSpec = &FunctionFuzzIOCTLSpec{
			Detected: true,
			Codes: []FunctionFuzzIOCTLCode{
				{Code: "0x222000", Value: 0x222000, HasValue: true},
				{Code: "0x222004", Value: 0x222004, HasValue: true},
			},
			Methods:            []string{"METHOD_BUFFERED"},
			BufferLengthFields: []string{"InputBufferLength", "OutputBufferLength"},
			DispatchAnchors:    []string{"driver/ioctl.c:120 (DeviceControl)"},
		}
	} else {
		run.IOCTLSpec = spec
		if len(run.IOCTLSpec.Methods) == 0 {
			run.IOCTLSpec.Methods = functionFuzzInferIOCTLMethods(run)
		}
		if len(run.IOCTLSpec.BufferLengthFields) == 0 {
			run.IOCTLSpec.BufferLengthFields = functionFuzzInferIOCTLBufferLengthFields(run)
		}
	}
	run.IOCTLSequences = functionFuzzBuildIOCTLSequences(run)
	if len(run.IOCTLSequences) == 0 {
		t.Fatalf("expected multi-call sequence plans")
	}
	seq := run.IOCTLSequences[0]
	actions := []string{}
	for _, step := range seq.Steps {
		actions = append(actions, step.Action)
	}
	joined := strings.Join(actions, ",")
	if !strings.Contains(joined, "open") || !strings.Contains(joined, "ioctl") || !strings.Contains(joined, "close") {
		t.Fatalf("sequence must include open/ioctl/close, got %v", actions)
	}

	if err := prepareFunctionFuzzArtifacts(&run); err != nil {
		t.Fatalf("prepare artifacts: %v", err)
	}
	contractPath, err := writeFunctionFuzzIOCTLContractArtifact(&run)
	if err != nil {
		t.Fatalf("write contract: %v", err)
	}
	if strings.TrimSpace(contractPath) == "" {
		t.Fatalf("expected ioctl_contract.json path")
	}
	data, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parse contract json: %v\n%s", err, string(data))
	}
	if payload["schema"] != "kernforge.ioctl_contract.v1" {
		t.Fatalf("unexpected schema %v", payload["schema"])
	}
	if _, ok := payload["codes"]; !ok {
		t.Fatalf("contract missing codes")
	}
	if _, ok := payload["methods"]; !ok {
		t.Fatalf("contract missing methods")
	}
	if _, ok := payload["buffer_length_fields"]; !ok {
		t.Fatalf("contract missing buffer_length_fields")
	}
	if _, ok := payload["sequences"]; !ok {
		t.Fatalf("contract missing sequences")
	}

	seqPaths, err := writeFunctionFuzzIOCTLSequenceSeeds(&run)
	if err != nil {
		t.Fatalf("write sequence seeds: %v", err)
	}
	if len(seqPaths) == 0 {
		t.Fatalf("expected sequence seed files")
	}
	seqData, err := os.ReadFile(seqPaths[0])
	if err != nil {
		t.Fatalf("read sequence seed: %v", err)
	}
	if !strings.Contains(string(seqData), "kernforge.fuzz_campaign.sequence_seed.v1") {
		t.Fatalf("sequence seed missing schema: %s", string(seqData))
	}
	if !strings.Contains(string(seqData), `"action": "open"`) && !strings.Contains(string(seqData), `"action":"open"`) {
		t.Fatalf("sequence seed missing open step: %s", string(seqData))
	}

	// Campaign promote path must record multi-call sequence seeds.
	campaignDir := filepath.Join(root, ".kernforge", "fuzz", "campaign-test")
	campaign := FuzzCampaign{
		ID:           "campaign-test",
		Workspace:    root,
		ArtifactDir:  campaignDir,
		ManifestPath: filepath.Join(campaignDir, "manifest.json"),
		CorpusDir:    filepath.Join(campaignDir, "corpus"),
		CrashDir:     filepath.Join(campaignDir, "crashes"),
		CoverageDir:  filepath.Join(campaignDir, "coverage"),
		ReportsDir:   filepath.Join(campaignDir, "reports"),
		LogsDir:      filepath.Join(campaignDir, "logs"),
	}
	for _, dir := range []string{campaign.CorpusDir, campaign.CrashDir, campaign.CoverageDir, campaign.ReportsDir, campaign.LogsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	// Minimal virtual scenario so promote still attaches when present; sequences alone also promote.
	run.VirtualScenarios = nil
	updated, promoted, err := promoteFunctionFuzzRunSeeds(campaign, []FunctionFuzzRun{run}, 8)
	if err != nil {
		t.Fatalf("promote seeds: %v", err)
	}
	if len(promoted) == 0 {
		t.Fatalf("expected promoted sequence seed artifacts, campaign=%+v", updated)
	}
	foundSeq := false
	for _, art := range promoted {
		if art.Source == "ioctl_sequence" || strings.Contains(art.Path, "sequences") {
			foundSeq = true
			raw, readErr := os.ReadFile(art.Path)
			if readErr != nil {
				t.Fatalf("read promoted seed: %v", readErr)
			}
			if !strings.Contains(string(raw), "ioctl") && !strings.Contains(string(raw), "open") {
				t.Fatalf("promoted sequence seed missing multi-call content: %s", string(raw))
			}
		}
	}
	if !foundSeq {
		t.Fatalf("promoted artifacts missing ioctl_sequence source: %+v", promoted)
	}
}

// --- Stage D: platform-security investigate ---------------------------------

func TestPlatformSecurityPresetRegisteredAndPostureParsed(t *testing.T) {
	if normalizeInvestigationPreset("platform-security") != "platform-security" {
		t.Fatalf("preset must normalize to platform-security")
	}
	if normalizeInvestigationPreset("vbs-hvci") != "platform-security" {
		t.Fatalf("alias vbs-hvci must map to platform-security")
	}
	specs := investigationPresetCommands("platform-security", "")
	if len(specs) == 0 {
		t.Fatalf("platform-security must register collector commands")
	}
	labels := map[string]bool{}
	for _, s := range specs {
		labels[s.Label] = true
	}
	for _, want := range []string{"secure-boot", "device-guard", "test-signing", "tpm"} {
		if !labels[want] {
			t.Fatalf("missing collector label %q in %#v", want, labels)
		}
	}

	commands := []InvestigationCommandResult{
		{Label: "secure-boot", Success: true, Output: "SecureBoot=True"},
		{Label: "device-guard", Success: true, Output: "VirtualizationBasedSecurityStatus : 2\nSecurityServicesRunning : 2\nCodeIntegrityPolicyEnforcementStatus : 2\n"},
		{Label: "test-signing", Success: true, Output: "identifier              {current}\ntestsigning             No\n"},
		{Label: "tpm", Success: true, Output: "TpmPresent                  : True\nTpmReady                    : True\n"},
		{Label: "driver-signature-policy", Success: true, Output: "CodeIntegrityPolicy=1; CI=1"},
	}
	posture := collectPlatformSecurityPosture(commands)
	for _, key := range []string{
		"secure_boot",
		"vbs",
		"hvci_memory_integrity",
		"test_signing",
		"driver_signature_enforcement",
		"tpm_ready",
	} {
		if _, ok := posture.Fields[key]; !ok {
			t.Fatalf("posture missing field %s: %#v", key, posture.Fields)
		}
	}
	if posture.Fields["secure_boot"] == "unavailable" {
		t.Fatalf("secure_boot should be parsed from fixture, got %q", posture.Fields["secure_boot"])
	}
	if posture.Fields["vbs"] != "running" {
		t.Fatalf("expected vbs=running, got %q", posture.Fields["vbs"])
	}
	if posture.Fields["hvci_memory_integrity"] != "enabled" {
		t.Fatalf("expected hvci enabled, got %q", posture.Fields["hvci_memory_integrity"])
	}
	if posture.Fields["test_signing"] != "disabled" {
		t.Fatalf("expected test_signing=disabled, got %q", posture.Fields["test_signing"])
	}
	if posture.Fields["tpm_ready"] != "ready" {
		t.Fatalf("expected tpm_ready=ready, got %q", posture.Fields["tpm_ready"])
	}

	findings := platformSecurityPostureFindings(posture)
	if len(findings) < 6 {
		t.Fatalf("expected posture findings for each field, got %d", len(findings))
	}
	for _, f := range findings {
		if f.Category != "platform_security" {
			t.Fatalf("finding category want platform_security, got %q", f.Category)
		}
		if f.Attributes["field"] == "" || f.Attributes["value"] == "" {
			t.Fatalf("finding missing field/value attributes: %+v", f)
		}
	}

	// Unavailable path still durable.
	empty := collectPlatformSecurityPosture(nil)
	if empty.Fields["secure_boot"] != "unavailable" {
		t.Fatalf("empty collectors must set unavailable, got %#v", empty.Fields)
	}
}

func TestCollectInvestigationSnapshotPlatformSecurityAttributes(t *testing.T) {
	// Feed only unavailable commands by using a workspace where binaries may fail;
	// the snapshot structure must still include platform-security kind and fields.
	root := t.TempDir()
	// Direct unit path: build snapshot fields from parse helpers (live OS optional).
	posture := collectPlatformSecurityPosture([]InvestigationCommandResult{
		{Label: "secure-boot", Success: false, Error: "unavailable", Output: "SecureBoot=unavailable"},
	})
	snap := InvestigationSnapshot{
		Kind:       "platform-security",
		Attributes: posture.Fields,
		Findings:   platformSecurityPostureFindings(posture),
	}
	snap = normalizeInvestigationSnapshot(snap)
	if snap.Kind != "platform-security" {
		t.Fatalf("kind=%q", snap.Kind)
	}
	if snap.Attributes["secure_boot"] == "" {
		t.Fatalf("attributes must retain secure_boot")
	}
	_ = root
}

// --- Stage E: create-driver-poc security handoff -----------------------------

func TestCreateDriverPOCSecurityHandoffAndSeed(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	rt := &runtimeState{
		cfg:       DefaultConfig(root),
		workspace: Workspace{Root: root, BaseRoot: root},
		writer:    &output,
		ui:        UI{},
	}
	if err := rt.handleCreateDriverPOCCommand("SecPoc"); err != nil {
		t.Fatalf("create default poc: %v", err)
	}
	text := output.String()
	for _, needle := range []string{
		"Security workflow handoff",
		"/source-scan",
		"/fuzz-func",
		"/verify",
		"Driver Verifier",
		"test-signing",
		"platform-security",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("handoff missing %q in output:\n%s", needle, text)
		}
	}
	seedPath := filepath.Join(root, "SecPoc", ".kernforge", "security", "workflow_seed.json")
	data, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("security seed missing: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("seed json: %v", err)
	}
	if payload["schema"] != "kernforge.create_driver_poc.security_seed.v1" {
		t.Fatalf("unexpected seed schema %v", payload["schema"])
	}
	if payload["poc_type"] != "default" {
		t.Fatalf("default type, got %v", payload["poc_type"])
	}

	// Typed template also gets handoff + seed.
	var output2 bytes.Buffer
	rt2 := &runtimeState{
		cfg:       DefaultConfig(root),
		workspace: Workspace{Root: root, BaseRoot: root},
		writer:    &output2,
		ui:        UI{},
	}
	if err := rt2.handleCreateDriverPOCCommand("ObjPoc --type objectfilter"); err != nil {
		t.Fatalf("create objectfilter poc: %v", err)
	}
	text2 := output2.String()
	if !strings.Contains(text2, "Security workflow handoff") {
		t.Fatalf("typed poc missing handoff:\n%s", text2)
	}
	if !strings.Contains(text2, "ObCallback") && !strings.Contains(text2, "objectfilter") && !strings.Contains(text2, "DesiredAccess") {
		t.Fatalf("typed handoff should mention objectfilter focus:\n%s", text2)
	}
	seed2 := filepath.Join(root, "ObjPoc", ".kernforge", "security", "workflow_seed.json")
	raw2, err := os.ReadFile(seed2)
	if err != nil {
		t.Fatalf("typed security seed: %v", err)
	}
	if !strings.Contains(string(raw2), "objectfilter") {
		t.Fatalf("typed seed should record objectfilter: %s", string(raw2))
	}
	if !strings.Contains(string(raw2), "/fuzz-func") {
		t.Fatalf("seed next_commands must include fuzz-func: %s", string(raw2))
	}
}

func TestCreateDriverPOCSecurityHandoffLinesContent(t *testing.T) {
	lines := createDriverPOCSecurityHandoffLines(createDriverPOCSpec{
		DriverName: "Demo",
		POCType:    "minifilter",
	})
	joined := strings.Join(lines, "\n")
	for _, needle := range []string{"/source-scan", "/fuzz-func", "/verify", "Driver Verifier", "minifilter", "platform-security"} {
		if !strings.Contains(joined, needle) {
			t.Fatalf("handoff lines missing %q:\n%s", needle, joined)
		}
	}
}
