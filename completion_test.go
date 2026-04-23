package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompleteSlashSubcommandEnumeratedArguments(t *testing.T) {
	rt := &runtimeState{
		cfg: DefaultConfig(t.TempDir()),
	}

	cases := []struct {
		input       string
		wantBuffer  string
		wantSuggest []string
	}{
		{input: "/permissions a", wantBuffer: "/permissions acceptEdits "},
		{input: "/analyze-project ", wantBuffer: "/analyze-project --mode "},
		{input: "/analyze-project --m", wantBuffer: "/analyze-project --mode "},
		{input: "/analyze-project --mode ", wantSuggest: []string{"/analyze-project --mode map", "/analyze-project --mode trace", "/analyze-project --mode impact", "/analyze-project --mode security", "/analyze-project --mode performance"}},
		{input: "/analyze-project --mode s", wantBuffer: "/analyze-project --mode security "},
		{input: "/checkpoint-auto of", wantBuffer: "/checkpoint-auto off "},
		{input: "/locale-auto of", wantBuffer: "/locale-auto off "},
		{input: "/set-auto-verify of", wantBuffer: "/set-auto-verify off "},
		{input: "/worktree ", wantSuggest: []string{"/worktree status", "/worktree create", "/worktree leave", "/worktree cleanup"}},
		{input: "/worktree cr", wantBuffer: "/worktree create "},
		{input: "/specialists ", wantSuggest: []string{"/specialists status", "/specialists assign", "/specialists cleanup"}},
		{input: "/specialists cl", wantBuffer: "/specialists cleanup "},
		{input: "/provider ", wantSuggest: []string{"/provider status", "/provider anthropic", "/provider openai", "/provider openrouter", "/provider ollama"}},
		{input: "/provider st", wantBuffer: "/provider status "},
		{input: "/verify --", wantBuffer: "/verify --full "},
		{input: "/verify-dashboard a", wantBuffer: "/verify-dashboard all "},
		{input: "/verify-dashboard-html a", wantBuffer: "/verify-dashboard-html all "},
		{input: "/mem-prune a", wantBuffer: "/mem-prune all "},
		{input: "/set-plan-review an", wantBuffer: "/set-plan-review anthropic "},
		{input: "/set-analysis-models ", wantSuggest: []string{"/set-analysis-models status", "/set-analysis-models worker", "/set-analysis-models reviewer", "/set-analysis-models clear"}},
		{input: "/set-analysis-models w", wantBuffer: "/set-analysis-models worker "},
		{input: "/set-analysis-models worker op", wantBuffer: "/set-analysis-models worker open"},
		{input: "/set-specialist-model ", wantSuggest: []string{"/set-specialist-model status", "/set-specialist-model clear", "/set-specialist-model attack-surface-reviewer", "/set-specialist-model driver-build-fixer", "/set-specialist-model implementation-owner", "/set-specialist-model kernel-investigator", "/set-specialist-model memory-inspection-reviewer", "/set-specialist-model planner", "/set-specialist-model reviewer", "/set-specialist-model telemetry-analyst", "/set-specialist-model unreal-integrity-reviewer"}},
		{input: "/set-specialist-model pl", wantBuffer: "/set-specialist-model planner "},
		{input: "/set-specialist-model planner op", wantBuffer: "/set-specialist-model planner open"},
		{input: "/set-specialist-model clear al", wantBuffer: "/set-specialist-model clear all "},
		{input: "/new-feature ", wantSuggest: []string{"/new-feature start", "/new-feature status", "/new-feature list", "/new-feature plan", "/new-feature implement", "/new-feature close"}},
		{input: "/new-feature im", wantBuffer: "/new-feature implement "},
		{input: "/investigate ", wantSuggest: []string{"/investigate status", "/investigate start", "/investigate snapshot", "/investigate note", "/investigate stop", "/investigate show", "/investigate list", "/investigate dashboard", "/investigate dashboard-html"}},
		{input: "/investigate start d", wantBuffer: "/investigate start driver-visibility "},
		{input: "/simulate ", wantSuggest: []string{"/simulate status", "/simulate show", "/simulate list", "/simulate dashboard", "/simulate dashboard-html", "/simulate tamper-surface", "/simulate stealth-surface", "/simulate forensic-blind-spot"}},
		{input: "/simulate t", wantBuffer: "/simulate tamper-surface "},
		{input: "/fuzz-func ", wantSuggest: []string{"/fuzz-func <function-name>", "/fuzz-func <function-name> --file <path>", "/fuzz-func <function-name> @<path>", "/fuzz-func --file <path>", "/fuzz-func @<path>", "/fuzz-func status", "/fuzz-func show", "/fuzz-func list", "/fuzz-func continue", "/fuzz-func language"}},
		{input: "/fuzz-func sh", wantBuffer: "/fuzz-func show "},
		{input: "/fuzz-func language ", wantSuggest: []string{"/fuzz-func language system", "/fuzz-func language english"}},
		{input: "/init ", wantSuggest: []string{"/init config", "/init hooks", "/init memory-policy", "/init skill", "/init verify"}},
		{input: "/init m", wantBuffer: "/init memory-policy "},
	}

	for _, tc := range cases {
		gotBuffer, gotSuggest, ok := rt.completeSlashCommand(tc.input)
		if !ok {
			t.Fatalf("%q: expected completion to apply", tc.input)
		}
		if tc.wantBuffer != "" && gotBuffer != tc.wantBuffer {
			t.Fatalf("%q: unexpected buffer: got %q want %q", tc.input, gotBuffer, tc.wantBuffer)
		}
		if tc.wantSuggest != nil {
			if len(gotSuggest) != len(tc.wantSuggest) {
				t.Fatalf("%q: unexpected suggestion count: got %#v want %#v", tc.input, gotSuggest, tc.wantSuggest)
			}
			for i := range tc.wantSuggest {
				if gotSuggest[i] != tc.wantSuggest[i] {
					t.Fatalf("%q: unexpected suggestion[%d]: got %q want %q", tc.input, i, gotSuggest[i], tc.wantSuggest[i])
				}
			}
		}
	}
}

func TestCompleteSlashCommandIncludesRecentlyAddedCommands(t *testing.T) {
	rt := &runtimeState{}

	cases := []struct {
		input      string
		wantBuffer string
	}{
		{input: "/evi", wantBuffer: "/evidence"},
		{input: "/invest", wantBuffer: "/investigate"},
		{input: "/new-f", wantBuffer: "/new-feature "},
		{input: "/simu", wantBuffer: "/simulate"},
		{input: "/fuzz-f", wantBuffer: "/fuzz-func "},
		{input: "/spec", wantBuffer: "/specialists "},
		{input: "/workt", wantBuffer: "/worktree "},
		{input: "/override-a", wantBuffer: "/override-add "},
		{input: "/hook-r", wantBuffer: "/hook-reload "},
	}

	for _, tc := range cases {
		gotBuffer, _, ok := rt.completeSlashCommand(tc.input)
		if !ok {
			t.Fatalf("%q: expected completion to apply", tc.input)
		}
		if gotBuffer != tc.wantBuffer {
			t.Fatalf("%q: unexpected buffer: got %q want %q", tc.input, gotBuffer, tc.wantBuffer)
		}
	}
}

func TestCompleteSlashSubcommandFuzzFuncAtPathListsWorkspaceCandidates(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "include"), 0o755); err != nil {
		t.Fatalf("mkdir include: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "driver.cpp"), []byte(""), 0o644); err != nil {
		t.Fatalf("write driver.cpp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "guard.cpp"), []byte(""), 0o644); err != nil {
		t.Fatalf("write guard.cpp: %v", err)
	}

	rt := &runtimeState{
		cfg: DefaultConfig(dir),
		workspace: Workspace{
			BaseRoot: dir,
			Root:     dir,
		},
	}

	cases := []struct {
		input       string
		wantSuggest []string
	}{
		{
			input:       "/fuzz-func @",
			wantSuggest: []string{"/fuzz-func @include/", "/fuzz-func @src/"},
		},
		{
			input:       "/fuzz-func ValidateRequest @src/",
			wantSuggest: []string{"/fuzz-func ValidateRequest @src/driver.cpp", "/fuzz-func ValidateRequest @src/guard.cpp"},
		},
	}

	for _, tc := range cases {
		gotBuffer, gotSuggest, ok := rt.completeSlashCommand(tc.input)
		if !ok {
			t.Fatalf("%q: expected completion to apply", tc.input)
		}
		if gotBuffer != tc.input {
			t.Fatalf("%q: unexpected buffer: got %q want %q", tc.input, gotBuffer, tc.input)
		}
		if len(gotSuggest) != len(tc.wantSuggest) {
			t.Fatalf("%q: unexpected suggestion count: got %#v want %#v", tc.input, gotSuggest, tc.wantSuggest)
		}
		for i := range tc.wantSuggest {
			if gotSuggest[i] != tc.wantSuggest[i] {
				t.Fatalf("%q: unexpected suggestion[%d]: got %q want %q", tc.input, i, gotSuggest[i], tc.wantSuggest[i])
			}
		}
		for _, suggestion := range gotSuggest {
			if suggestion == "/fuzz-func @<path>" || suggestion == "/fuzz-func ValidateRequest @<path>" {
				t.Fatalf("%q: placeholder suggestion leaked into @ completion: %#v", tc.input, gotSuggest)
			}
		}
	}
}

func TestCompleteSlashSubcommandDynamicIdentifiers(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)

	store := NewSessionStore(dir)
	if err := store.Save(&Session{
		ID:         "session-abc",
		Name:       "Recent Session",
		WorkingDir: dir,
		Provider:   "openai",
		Model:      "gpt-5.4",
		CreatedAt:  now,
		UpdatedAt:  now,
		Messages:   []Message{},
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	evidence := &EvidenceStore{Path: dir + "\\evidence.json"}
	if err := evidence.Append(EvidenceRecord{
		ID:        "evidence-abc",
		Workspace: dir,
		CreatedAt: now,
		Kind:      "verification",
		Subject:   "subject",
	}); err != nil {
		t.Fatalf("append evidence: %v", err)
	}

	longMem := &PersistentMemoryStore{Path: dir + "\\memory.json"}
	if err := longMem.Append(PersistentMemoryRecord{
		ID:          "mem-abc",
		SessionID:   "session-abc",
		SessionName: "Recent Session",
		Workspace:   dir,
		CreatedAt:   now,
		Request:     "request",
		Reply:       "reply",
		Summary:     "summary",
	}); err != nil {
		t.Fatalf("append memory: %v", err)
	}

	investigations := &InvestigationStore{Path: dir + "\\investigations.json"}
	if _, err := investigations.Append(InvestigationRecord{
		ID:        "invest-abc",
		Workspace: dir,
		Preset:    "driver-visibility",
		Status:    InvestigationCompleted,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("append investigation: %v", err)
	}

	simulations := &SimulationStore{Path: dir + "\\simulations.json"}
	if _, err := simulations.Append(SimulationResult{
		ID:        "sim-abc",
		Workspace: dir,
		Profile:   "tamper-surface",
		CreatedAt: now,
		Summary:   "summary",
	}); err != nil {
		t.Fatalf("append simulation: %v", err)
	}
	functionFuzz := &FunctionFuzzStore{Path: dir + "\\function_fuzz.json"}
	if _, err := functionFuzz.Append(FunctionFuzzRun{
		ID:               "fuzz-abc",
		Workspace:        dir,
		TargetQuery:      "ValidateRequest",
		TargetSymbolID:   "func:ValidateRequest",
		TargetSymbolName: "ValidateRequest",
		CreatedAt:        now,
		PrimaryEngine:    "libFuzzer + ASan/UBSan",
		Summary:          "summary",
	}); err != nil {
		t.Fatalf("append function fuzz: %v", err)
	}

	featureStore := NewFeatureStore(dir)
	feature, err := featureStore.Create(dir, "add tracked feature workflow", "openai / gpt-5.4", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}

	rt := &runtimeState{
		store:          store,
		evidence:       evidence,
		longMem:        longMem,
		investigations: investigations,
		simulations:    simulations,
		functionFuzz:   functionFuzz,
		workspace: Workspace{
			BaseRoot: dir,
			Root:     dir,
		},
	}

	cases := []struct {
		input      string
		wantBuffer string
	}{
		{input: "/resume sess", wantBuffer: "/resume session-abc "},
		{input: "/evidence-show evid", wantBuffer: "/evidence-show evidence-abc "},
		{input: "/mem-show mem", wantBuffer: "/mem-show mem-abc "},
		{input: "/mem-promote mem", wantBuffer: "/mem-promote mem-abc "},
		{input: "/investigate show inv", wantBuffer: "/investigate show invest-abc "},
		{input: "/simulate show sim", wantBuffer: "/simulate show sim-abc "},
		{input: "/fuzz-func show fu", wantBuffer: "/fuzz-func show fuzz-abc "},
		{input: "/new-feature status " + feature.ID[:8], wantBuffer: "/new-feature status " + feature.ID + " "},
	}

	for _, tc := range cases {
		gotBuffer, _, ok := rt.completeSlashCommand(tc.input)
		if !ok {
			t.Fatalf("%q: expected completion to apply", tc.input)
		}
		if gotBuffer != tc.wantBuffer {
			t.Fatalf("%q: unexpected buffer: got %q want %q", tc.input, gotBuffer, tc.wantBuffer)
		}
	}
}

func TestCommandCompletionDescriptionCoversCommandsAndSubcommands(t *testing.T) {
	cases := map[string]string{
		"/status":                    "Show current session state, approvals, and extension status.",
		"/provider status":           "Show the current provider, base URL, key state, and billing visibility.",
		"/verify":                    "Run manual verification for the current workspace state.",
		"/specialists":               "Show specialist profiles plus editable ownership and worktree routing state.",
		"/specialists cleanup":       "Remove one or all specialist worktrees recorded for this session.",
		"/worktree cleanup":          "Remove the recorded isolated worktree after it is clean.",
		"/new-feature status":        "Show the current state of a tracked feature.",
		"/simulate tamper-surface":   "Model obvious tamper vectors and exposed surfaces.",
		"/fuzz-func":                 "Auto-plan directed function fuzzing. Use /fuzz-func <function-name> [--file <path>|@<path>] for one function, or /fuzz-func --file <path> / @<path> to analyze a file and its include/import closure.",
		"/fuzz-func <function-name>": "Target one function by name and let Kernforge resolve the best matching symbol automatically.",
		"/fuzz-func <function-name> --file <path>": "Target one function by name and pin matching to a specific source file when names collide.",
		"/fuzz-func <function-name> @<path>":       "Target one function by name and use @<path> as a short file-hint alias.",
		"/fuzz-func --file <path>":                 "Analyze one file plus the files it includes or imports, then let Kernforge choose the best starting function automatically.",
		"/fuzz-func @<path>":                       "Analyze one file plus the files it includes or imports, then let Kernforge choose the best starting function automatically.",
		"/fuzz-func language":                      "Show or change /fuzz-func output language. Use system to follow the PC language or english to force English.",
		"/fuzz-func show":                          "Open one saved function fuzz plan by id.",
		"/fuzz-func continue":                      "Approve a pending recovered build configuration and start autonomous fuzzing.",
	}

	for item, want := range cases {
		if got := commandCompletionDescription(item); got != want {
			t.Fatalf("%q: got %q want %q", item, got, want)
		}
	}
}
