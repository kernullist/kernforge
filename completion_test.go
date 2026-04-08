package main

import (
	"testing"
	"time"
)

func TestCompleteSlashSubcommandEnumeratedArguments(t *testing.T) {
	rt := &runtimeState{}

	cases := []struct {
		input       string
		wantBuffer  string
		wantSuggest []string
	}{
		{input: "/permissions a", wantBuffer: "/permissions acceptEdits "},
		{input: "/checkpoint-auto of", wantBuffer: "/checkpoint-auto off "},
		{input: "/locale-auto of", wantBuffer: "/locale-auto off "},
		{input: "/set-auto-verify of", wantBuffer: "/set-auto-verify off "},
		{input: "/verify --", wantBuffer: "/verify --full "},
		{input: "/verify-dashboard a", wantBuffer: "/verify-dashboard all "},
		{input: "/verify-dashboard-html a", wantBuffer: "/verify-dashboard-html all "},
		{input: "/mem-prune a", wantBuffer: "/mem-prune all "},
		{input: "/set-plan-review an", wantBuffer: "/set-plan-review anthropic "},
		{input: "/set-analysis-models ", wantSuggest: []string{"/set-analysis-models status", "/set-analysis-models worker", "/set-analysis-models reviewer", "/set-analysis-models clear"}},
		{input: "/set-analysis-models w", wantBuffer: "/set-analysis-models worker "},
		{input: "/set-analysis-models worker op", wantBuffer: "/set-analysis-models worker open"},
		{input: "/investigate ", wantSuggest: []string{"/investigate status", "/investigate start", "/investigate snapshot", "/investigate note", "/investigate stop", "/investigate show", "/investigate list", "/investigate dashboard", "/investigate dashboard-html"}},
		{input: "/investigate start d", wantBuffer: "/investigate start driver-visibility "},
		{input: "/simulate ", wantSuggest: []string{"/simulate status", "/simulate show", "/simulate list", "/simulate dashboard", "/simulate dashboard-html", "/simulate tamper-surface", "/simulate stealth-surface", "/simulate forensic-blind-spot"}},
		{input: "/simulate t", wantBuffer: "/simulate tamper-surface "},
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
		{input: "/simu", wantBuffer: "/simulate"},
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

	rt := &runtimeState{
		store:          store,
		evidence:       evidence,
		longMem:        longMem,
		investigations: investigations,
		simulations:    simulations,
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
