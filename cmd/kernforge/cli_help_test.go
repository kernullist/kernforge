package main

import (
	"strings"
	"testing"
)

func TestKernforgeCLIHelpRequestRecognizesCommonForms(t *testing.T) {
	cases := []struct {
		args      []string
		wantOK    bool
		wantTopic string
	}{
		{args: []string{"--help"}, wantOK: true, wantTopic: ""},
		{args: []string{"/help"}, wantOK: true, wantTopic: ""},
		{args: []string{"help", "mcp"}, wantOK: true, wantTopic: "mcp"},
		{args: []string{"help", "doctor"}, wantOK: true, wantTopic: "doctor"},
		{args: []string{"-cwd", `C:\repo\driver`, "help", "mcp"}, wantOK: true, wantTopic: "mcp"},
		{args: []string{"daemon", "help"}, wantOK: true, wantTopic: "daemon"},
		{args: []string{"doctor", "help"}, wantOK: true, wantTopic: "doctor"},
		{args: []string{"-cwd", `C:\repo\driver`, "daemon", "--help"}, wantOK: true, wantTopic: "daemon"},
		{args: []string{"-mcp-server", "--help"}, wantOK: true, wantTopic: "mcp"},
		{args: []string{"-prompt", "--help"}, wantOK: true, wantTopic: "standalone"},
		{args: []string{"-prompt", "help"}, wantOK: false, wantTopic: ""},
		{args: []string{"-command", "/help"}, wantOK: false, wantTopic: ""},
		{args: []string{"-command", "/help review"}, wantOK: false, wantTopic: ""},
		{args: []string{"-prompt", "/help"}, wantOK: false, wantTopic: ""},
	}
	for _, tc := range cases {
		gotOK, gotTopic := kernforgeCLIHelpRequest(tc.args)
		if gotOK != tc.wantOK || gotTopic != tc.wantTopic {
			t.Fatalf("kernforgeCLIHelpRequest(%v) = (%v, %q), want (%v, %q)", tc.args, gotOK, gotTopic, tc.wantOK, tc.wantTopic)
		}
	}
}

func TestKernforgeCLIVersionRequestRecognizesSafeForms(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{args: []string{"--version"}, want: true},
		{args: []string{"-version"}, want: true},
		{args: []string{"version"}, want: true},
		{args: []string{"-cwd", `C:\repo\driver`, "version"}, want: true},
		{args: []string{"help", "version"}, want: false},
		{args: []string{"-model", "--version"}, want: false},
		{args: []string{"-prompt", "version"}, want: false},
	}
	for _, tc := range cases {
		if got := kernforgeCLIVersionRequest(tc.args); got != tc.want {
			t.Fatalf("kernforgeCLIVersionRequest(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestKernforgeGeneralHelpIncludesStandaloneAndMCPUsage(t *testing.T) {
	text := renderKernforgeCLIHelp("")
	for _, needle := range []string{
		"Version: ",
		"Start the standalone interactive REPL",
		`kernforge -prompt "<task>"`,
		`kernforge -command "/status"`,
		"Run Kernforge as a stdio MCP server",
		"kernforge -mcp-server -cwd",
		"plan | edit | full (default: plan)",
		"-dangerously-bypass-hook-trust",
		"--version",
		"You usually do not need daemon mode",
		"kernforge doctor [--summary|--json]",
		`"args": ["-mcp-server", "-cwd", "C:\\repo\\driver"]`,
		"kernforge help doctor",
		"kernforge help mcp",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("expected general help to contain %q, got:\n%s", needle, text)
		}
	}
	// The help advertises only the canonical modes; legacy and Codex profile
	// aliases stay accepted by the parser but are no longer documented here.
	for _, absent := range []string{
		"Legacy aliases accepted",
		"Codex profile ids",
		"acceptEdits",
		"bypassPermissions",
		":danger-full-access",
	} {
		if strings.Contains(text, absent) {
			t.Fatalf("general help must not advertise legacy/Codex aliases, found %q in:\n%s", absent, text)
		}
	}
}

func TestKernforgeTopicHelp(t *testing.T) {
	mcp := renderKernforgeCLIHelp("mcp")
	if !strings.Contains(mcp, "Most users only need this MCP client setup") || !strings.Contains(mcp, "What this does") {
		t.Fatalf("expected MCP topic help, got:\n%s", mcp)
	}
	if !strings.Contains(mcp, "Advanced shared daemon mode") || !strings.Contains(mcp, "Use this only when multiple MCP clients") {
		t.Fatalf("expected advanced MCP proxy explanation, got:\n%s", mcp)
	}
	daemon := renderKernforgeCLIHelp("daemon")
	if !strings.Contains(daemon, "kernforge daemon start") || !strings.Contains(daemon, "kernforge daemon stop") {
		t.Fatalf("expected daemon topic help, got:\n%s", daemon)
	}
	if !strings.Contains(daemon, "Most users do not need this") || !strings.Contains(daemon, "The second command is the MCP client entrypoint") {
		t.Fatalf("expected daemon proxy flow explanation, got:\n%s", daemon)
	}
	standalone := renderKernforgeCLIHelp("standalone")
	if !strings.Contains(standalone, "Interactive REPL") || !strings.Contains(standalone, "One-shot prompt") {
		t.Fatalf("expected standalone topic help, got:\n%s", standalone)
	}
	doctor := renderKernforgeCLIHelp("doctor")
	if !strings.Contains(doctor, "kernforge doctor --json") || !strings.Contains(doctor, "Reported areas") {
		t.Fatalf("expected doctor topic help, got:\n%s", doctor)
	}
}
