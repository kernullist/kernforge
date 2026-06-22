package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFunctionFuzzWriteDictionaryEmitsConstantsAndIdentifiers(t *testing.T) {
	dir := t.TempDir()
	dictPath := filepath.Join(dir, "dict.txt")
	run := FunctionFuzzRun{
		ID:               "run-1",
		TargetSymbolName: "Validate",
		CodeObservations: []FunctionFuzzCodeObservation{
			{
				Kind:            "size_guard",
				Evidence:        "if (len > 0x1000) return STATUS_BUFFER_TOO_SMALL;",
				ComparisonFacts: []string{"len > 0x1000"},
				FocusInputs:     []string{"len"},
			},
			{
				Kind:     "dispatch_guard",
				Evidence: "if (ioctl == IOCTL_DEVICE_INIT) { ... }",
			},
		},
		SinkSignals: []FunctionFuzzSinkSignal{
			{Kind: "copy", Name: "RtlCopyMemory", Reason: "writes user buffer"},
		},
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{
				Title:          "size drift on copy",
				ConcreteInputs: []string{"len=0xFFFFFFFF, header=\"AAAA\""},
				Invariants: []FunctionFuzzInvariant{
					{Kind: "size_eq", Left: "claimed", Right: "actual", Detail: "claimed == actual"},
				},
			},
		},
		ParameterStrategies: []FunctionFuzzParamStrategy{
			{Index: 0, Name: "buf", Class: "buffer"},
			{Index: 1, Name: "len", Class: "length"},
		},
	}
	written, err := functionFuzzWriteDictionary(run, dictPath)
	if err != nil {
		t.Fatalf("write dictionary: %v", err)
	}
	if written == 0 {
		t.Fatalf("expected dictionary entries to be written")
	}
	data, err := os.ReadFile(dictPath)
	if err != nil {
		t.Fatalf("read dictionary: %v", err)
	}
	text := string(data)
	checks := []string{
		`"0x1000"`,           // raw integer literal as text
		`"\x00\x10\x00\x00"`, // little-endian encoding of 0x1000
		`"RtlCopyMemory"`,    // sink identifier
		`"AAAA"`,             // string literal pulled from scenario concrete inputs
	}
	for _, want := range checks {
		if !strings.Contains(text, want) {
			t.Fatalf("dictionary missing expected entry %q\nfull dictionary:\n%s", want, text)
		}
	}
}

func TestFunctionFuzzWriteDictionaryIsEmptyWhenNoSignals(t *testing.T) {
	dir := t.TempDir()
	dictPath := filepath.Join(dir, "dict.txt")
	count, err := functionFuzzWriteDictionary(FunctionFuzzRun{ID: "empty"}, dictPath)
	if err != nil {
		t.Fatalf("write dictionary: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected zero entries for empty run, got %d", count)
	}
	if _, err := os.Stat(dictPath); !os.IsNotExist(err) {
		t.Fatalf("expected dictionary file to be absent when empty, stat err=%v", err)
	}
}

func TestFunctionFuzzWriteSeedCorpusWithProvenanceProducesBoundarySeeds(t *testing.T) {
	dir := t.TempDir()
	corpusDir := filepath.Join(dir, "corpus")
	manifestPath := filepath.Join(dir, "corpus_manifest.json")
	run := FunctionFuzzRun{
		ID:               "run-corpus",
		TargetSymbolName: "Handle",
		ParameterStrategies: []FunctionFuzzParamStrategy{
			{Index: 0, Name: "buf", Class: "buffer"},
			{Index: 1, Name: "len", Class: "length"},
		},
		VirtualScenarios: []FunctionFuzzVirtualScenario{
			{Title: "short header", ConcreteInputs: []string{"len=0xFFFFFFFF"}},
		},
	}
	if err := functionFuzzWriteSeedCorpusWithProvenance(run, corpusDir, manifestPath, ""); err != nil {
		t.Fatalf("seed corpus: %v", err)
	}
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest functionFuzzCorpusManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.RunID != "run-corpus" {
		t.Fatalf("manifest run id = %q, want run-corpus", manifest.RunID)
	}
	requiredRules := []string{
		"empty_input",
		"diagnostic_pattern",
		"length_prefix_header",
		"buffer_empty",
		"buffer_oversized_4k",
		"scalar_max_u32",
		"scenario_concrete_inputs",
	}
	have := map[string]bool{}
	for _, seed := range manifest.Seeds {
		have[seed.Rule] = true
		if seed.Sha256 == "" {
			t.Fatalf("seed %q has empty sha256", seed.Name)
		}
		path := filepath.Join(corpusDir, seed.Name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("seed file %s missing: %v", path, err)
		}
		if int(info.Size()) != seed.Size {
			t.Fatalf("seed %q manifest size=%d on-disk size=%d", seed.Name, seed.Size, info.Size())
		}
	}
	for _, rule := range requiredRules {
		if !have[rule] {
			t.Fatalf("expected boundary seed with rule %q not present in manifest %+v", rule, manifest.Seeds)
		}
	}
}

func TestFunctionFuzzRunArgsSelectsProfileBehavior(t *testing.T) {
	run := FunctionFuzzRun{
		ParameterStrategies: []FunctionFuzzParamStrategy{
			{Index: 0, Name: "buf", Class: "buffer"},
		},
	}
	exec := FunctionFuzzExecution{
		CorpusDir:      filepath.Join("art", "corpus"),
		CrashDir:       filepath.Join("art", "crashes"),
		DictionaryPath: filepath.Join("art", "dict.txt"),
		Profile:        "smoke",
	}

	smokeArgs := functionFuzzRunArgs(run, exec)
	if !sliceContainsString(smokeArgs, "-max_total_time=20") {
		t.Fatalf("smoke profile missing short total time: %v", smokeArgs)
	}
	if !sliceContainsPrefix(smokeArgs, "-dict=") {
		t.Fatalf("smoke profile must include dict: %v", smokeArgs)
	}

	exec.Profile = "extended"
	extArgs := functionFuzzRunArgs(run, exec)
	if !sliceContainsString(extArgs, "-max_total_time=600") {
		t.Fatalf("extended profile missing long total time: %v", extArgs)
	}
	if !sliceContainsString(extArgs, "-fork=2") {
		t.Fatalf("extended profile missing fork workers: %v", extArgs)
	}

	exec.Profile = "repro"
	exec.CrashInputPath = filepath.Join("art", "crashes", "crash-1.bin")
	reproArgs := functionFuzzRunArgs(run, exec)
	if !sliceContainsString(reproArgs, "-runs=1") {
		t.Fatalf("repro profile must run a single iteration: %v", reproArgs)
	}
	if !sliceContainsString(reproArgs, exec.CrashInputPath) {
		t.Fatalf("repro profile must include crash input path: %v", reproArgs)
	}

	exec.Profile = "minimize"
	minArgs := functionFuzzRunArgs(run, exec)
	if !sliceContainsString(minArgs, "-minimize_crash=1") {
		t.Fatalf("minimize profile must include -minimize_crash=1: %v", minArgs)
	}
}

func TestParseFunctionFuzzContinueArgsAcceptsProfile(t *testing.T) {
	id, profile, err := parseFunctionFuzzContinueArgs("run-123 --profile extended")
	if err != nil {
		t.Fatalf("parse continue args: %v", err)
	}
	if id != "run-123" {
		t.Fatalf("id = %q, want run-123", id)
	}
	if profile != "extended" {
		t.Fatalf("profile = %q, want extended", profile)
	}
}

func TestParseFunctionFuzzContinueArgsRejectsUnknownProfile(t *testing.T) {
	if _, _, err := parseFunctionFuzzContinueArgs("run-123 --profile bogus"); err == nil {
		t.Fatalf("expected error for unknown profile")
	}
}

func TestParseFunctionFuzzReplayArgsAcceptsPositionalCrashPath(t *testing.T) {
	id, crashInput, err := parseFunctionFuzzReplayArgs("run-1 crashes/crash-abc.bin")
	if err != nil {
		t.Fatalf("parse replay args: %v", err)
	}
	if id != "run-1" {
		t.Fatalf("id = %q", id)
	}
	if crashInput != "crashes/crash-abc.bin" {
		t.Fatalf("crashInput = %q", crashInput)
	}
}

func TestFunctionFuzzSeedsForScenarioEncodesConstantNotProse(t *testing.T) {
	// A scenario whose invariant pins a comparison constant must land that
	// constant, little-endian, at the leading scalar parameter's offset - and
	// must NOT carry the English ConcreteInputs prose into the seed bytes.
	const prose = "claimed size larger than backing store"
	scenario := FunctionFuzzVirtualScenario{
		Title: "attacker-controlled size",
		Invariants: []FunctionFuzzInvariant{
			{Kind: "size_eq", Left: "size", Right: "cap", Detail: "size == 0x1000"},
		},
		ConcreteInputs: []string{"size = 0x1000 -> " + prose},
	}
	params := []FunctionFuzzParamStrategy{
		{Index: 0, Name: "size", Class: "scalar_int", RawType: "uint32_t"},
	}
	seeds := functionFuzzSeedsForScenario(0, scenario, params)
	if len(seeds) != 1 {
		t.Fatalf("expected exactly one scenario seed, got %d", len(seeds))
	}
	payload := seeds[0].payload

	// uint32_t scalar -> 4 LE bytes of 0x1000 at offset 0.
	wantLE := []byte{0x00, 0x10, 0x00, 0x00}
	if len(payload) < len(wantLE) {
		t.Fatalf("payload too short (%d bytes) to hold the leading scalar: % x", len(payload), payload)
	}
	if !bytes.Equal(payload[:len(wantLE)], wantLE) {
		t.Fatalf("leading scalar bytes = % x, want % x (full payload % x)", payload[:len(wantLE)], wantLE, payload)
	}
	// The constant must appear LE-encoded somewhere too (offset already checked).
	if !bytes.Contains(payload, wantLE) {
		t.Fatalf("payload missing LE-encoded constant % x: % x", wantLE, payload)
	}
	// The English description prose must not be embedded as raw bytes.
	if bytes.Contains(payload, []byte(prose)) {
		t.Fatalf("payload must not contain English description prose %q: % x", prose, payload)
	}
	if seeds[0].Rule != "scenario_concrete_inputs" {
		t.Fatalf("seed rule = %q, want scenario_concrete_inputs", seeds[0].Rule)
	}
}

func TestFunctionFuzzSeedsForScenarioCouplesDesyncVersusConsistent(t *testing.T) {
	// Buffer 'buf' is statically related to length 'len' (sized_by:len). The
	// harness derives len from buf.size() and reads a desync-flag byte (plus an
	// optional delta byte) from the seed. A size-desync scenario must encode the
	// desync path; a normal scenario must keep them consistent.
	params := []FunctionFuzzParamStrategy{
		{Index: 0, Name: "buf", Class: "buffer", RawType: "uint8_t*", Relation: "sized_by:len"},
		{Index: 1, Name: "len", Class: "length", RawType: "uint32_t"},
	}

	desyncScenario := FunctionFuzzVirtualScenario{
		Title: "attacker-controlled size desync",
	}
	normalScenario := FunctionFuzzVirtualScenario{
		Title: "happy path round trip",
	}

	desyncSeeds := functionFuzzSeedsForScenario(0, desyncScenario, params)
	normalSeeds := functionFuzzSeedsForScenario(1, normalScenario, params)
	if len(desyncSeeds) != 1 || len(normalSeeds) != 1 {
		t.Fatalf("expected one seed each, got desync=%d normal=%d", len(desyncSeeds), len(normalSeeds))
	}
	desyncPayload := desyncSeeds[0].payload
	normalPayload := normalSeeds[0].payload

	// Common prefix is the buffer: LE size_t(4) + 4 body bytes = 12 bytes, equal
	// for both because a coupled buffer keeps a consistent declared prefix.
	wantBufferPrefix := append(functionFuzzLEBytes(4, 8), 0x41, 0x41, 0x41, 0x41)
	if !bytes.HasPrefix(desyncPayload, wantBufferPrefix) {
		t.Fatalf("desync payload missing coupled buffer prefix % x: % x", wantBufferPrefix, desyncPayload)
	}
	if !bytes.HasPrefix(normalPayload, wantBufferPrefix) {
		t.Fatalf("normal payload missing coupled buffer prefix % x: % x", wantBufferPrefix, normalPayload)
	}

	// The coupled-length encoding is the tail after the buffer prefix.
	desyncTail := desyncPayload[len(wantBufferPrefix):]
	normalTail := normalPayload[len(wantBufferPrefix):]

	// Consistent: single flag byte whose low 3 bits are non-zero so the harness
	// keeps length == buffer.size().
	if len(normalTail) != 1 {
		t.Fatalf("normal coupled-length tail must be one byte, got % x", normalTail)
	}
	if normalTail[0]&0x07 == 0 {
		t.Fatalf("normal coupled-length flag % x must keep (flag & 0x07) != 0", normalTail)
	}

	// Desync: flag byte clears the low 3 bits (harness takes the desync path) and
	// is followed by a non-zero delta byte that perturbs the length.
	if len(desyncTail) != 2 {
		t.Fatalf("desync coupled-length tail must be two bytes, got % x", desyncTail)
	}
	if desyncTail[0]&0x07 != 0 {
		t.Fatalf("desync coupled-length flag % x must clear (flag & 0x07) == 0", desyncTail)
	}
	if desyncTail[1] == 0 {
		t.Fatalf("desync coupled-length delta must be non-zero, got % x", desyncTail)
	}
}

func TestFunctionFuzzSeedsForScenarioOmittedWithoutParameters(t *testing.T) {
	// With no parameter layout to encode against, the scenario must produce no
	// seed rather than writing its prose as bytes.
	scenario := FunctionFuzzVirtualScenario{
		Title:          "lonely scenario",
		ConcreteInputs: []string{"buf = 0x10000000 -> bytes[41]"},
	}
	if seeds := functionFuzzSeedsForScenario(0, scenario, nil); len(seeds) != 0 {
		t.Fatalf("expected no seed when there are no parameters, got %d", len(seeds))
	}
}

func sliceContainsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func sliceContainsPrefix(items []string, prefix string) bool {
	for _, item := range items {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
}
