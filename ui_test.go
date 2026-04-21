package main

import (
	"strings"
	"testing"
)

func TestBannerUsesCurrentKernforgeBranding(t *testing.T) {
	ui := UI{color: false}
	banner := ui.banner("openai", "gpt-5.4", "session-123", `F:\kernullist\kernforge`)

	for _, needle := range []string{
		"Kernforge",
		"forge-ready terminal coding agent",
		"version=",
		"K\\  /F====",
		"Welcome back.",
		"Describe the task and Kernforge will inspect, edit, and verify with you.",
		"provider=openai",
		"model=gpt-5.4",
		"session=session-123",
		"workspace=F:\\kernullist\\kernforge",
		"ready=edit / review / verify",
		"commands=/help /status /model /config",
		"tip=Esc cancels the active turn.",
	} {
		if !strings.Contains(banner, needle) {
			t.Fatalf("expected banner to contain %q\n%s", needle, banner)
		}
	}

	for _, legacy := range []string{"IM-CLI", "im-cli", "imcli"} {
		if strings.Contains(banner, legacy) {
			t.Fatalf("banner should not contain legacy branding %q\n%s", legacy, banner)
		}
	}
}

func TestStatusKVAlignsShortKeysAndFallsBackForPaths(t *testing.T) {
	ui := UI{color: false}

	short := ui.statusKV("model", "gpt-5.4")
	if !strings.Contains(short, "model") || !strings.Contains(short, "gpt-5.4") {
		t.Fatalf("expected compact key-value rendering, got %q", short)
	}
	if strings.Contains(short, "->") {
		t.Fatalf("expected short key to use aligned column rendering, got %q", short)
	}

	pathLike := ui.statusKV(`F:\kernullist\kernforge`, "workspace root")
	if !strings.Contains(pathLike, " -> workspace root") {
		t.Fatalf("expected path-like key to use arrow rendering, got %q", pathLike)
	}
}

func TestStatusKVAlignedKeepsLongKeysInColumnLayout(t *testing.T) {
	ui := UI{color: false}

	rendered := ui.statusKVAligned("memory-inspection-reviewer", "openrouter / z-ai/glm-5.1", 30)
	if strings.Contains(rendered, "->") {
		t.Fatalf("expected aligned helper to avoid arrow fallback, got %q", rendered)
	}
	if !strings.Contains(rendered, "memory-inspection-reviewer:") {
		t.Fatalf("expected aligned helper to keep colon layout, got %q", rendered)
	}
}

func TestPromptUsesUserScopedTargetLabel(t *testing.T) {
	ui := UI{color: false}
	prompt := ui.prompt("openai", "gpt-5.4")
	if prompt != "you [openai / gpt-5.4] > " {
		t.Fatalf("unexpected prompt rendering: %q", prompt)
	}
}

func TestTurnSeparatorUsesSubtleDivider(t *testing.T) {
	ui := UI{color: false}
	line := ui.turnSeparator(3, "openai", "gpt-5.4")
	if strings.Contains(strings.ToLower(line), "turn") {
		t.Fatalf("expected turn separator to avoid explicit turn labels, got %q", line)
	}
	if strings.Contains(line, "openai") || strings.Contains(line, "gpt-5.4") {
		t.Fatalf("expected turn separator to stay neutral, got %q", line)
	}
	if strings.Count(line, "-") < 20 {
		t.Fatalf("expected turn separator to render as a faint divider, got %q", line)
	}
}

func TestSectionAndSubsectionUseRuledLabels(t *testing.T) {
	ui := UI{color: false}

	section := ui.section("Status")
	if !strings.Contains(section, "== Status ") {
		t.Fatalf("expected ruled section label, got %q", section)
	}
	if !strings.Contains(section, "====") {
		t.Fatalf("expected section ruler fill, got %q", section)
	}

	subsection := ui.subsection("Approvals")
	if !strings.Contains(subsection, "-- Approvals ") {
		t.Fatalf("expected ruled subsection label, got %q", subsection)
	}
	if !strings.Contains(subsection, "----") {
		t.Fatalf("expected subsection ruler fill, got %q", subsection)
	}
}

func TestPlanItemUsesModernBadge(t *testing.T) {
	ui := UI{color: false}

	rendered := ui.planItem(1, "in_progress", "Refine status layout")
	if !strings.Contains(rendered, "02.") {
		t.Fatalf("expected numbered plan item, got %q", rendered)
	}
	if !strings.Contains(rendered, "[work]") {
		t.Fatalf("expected in-progress badge, got %q", rendered)
	}
	if !strings.Contains(rendered, "Refine status layout") {
		t.Fatalf("expected step text, got %q", rendered)
	}
}

func TestAssistantHeaderUsesRuledLabel(t *testing.T) {
	ui := UI{color: false}

	header := ui.assistantHeader()
	if !strings.Contains(header, ">> assistant ") {
		t.Fatalf("expected assistant header label, got %q", header)
	}
	if !strings.Contains(header, "--------") {
		t.Fatalf("expected assistant header ruler fill, got %q", header)
	}
}

func TestActivityLineUsesPaddedBadge(t *testing.T) {
	ui := UI{color: false}

	line := ui.activityLine("tool", "read_file on main.go")
	if !strings.Contains(line, "[tool") {
		t.Fatalf("expected tool badge, got %q", line)
	}
	if !strings.Contains(line, "read_file on main.go") {
		t.Fatalf("expected activity body, got %q", line)
	}
}

func TestShellUsesOutputHeaderAndBody(t *testing.T) {
	ui := UI{color: false}
	rendered := ui.shell("line1\nline2\n")
	if !strings.Contains(rendered, ">> shell output [2 line(s)] ") {
		t.Fatalf("expected shell output header, got %q", rendered)
	}
	if !strings.Contains(rendered, "line1\nline2") {
		t.Fatalf("expected shell body to remain visible, got %q", rendered)
	}
}

func TestAssistantFormatsParagraphsListsAndHeadings(t *testing.T) {
	ui := UI{color: false}
	rendered := ui.assistant("Summary:\n- first\n- second\n## Next\nMore detail")

	for _, needle := range []string{
		"Summary:\n\n- first\n- second",
		"- second\n\n## Next",
		"## Next\n\nMore detail",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected assistant rendering to contain %q, got %q", needle, rendered)
		}
	}
}

func TestAssistantCodeBlocksUseSeparateToneWhenColorEnabled(t *testing.T) {
	ui := UI{color: true}
	rendered := ui.assistant("Summary\n```go\nfmt.Println(\"hi\")\n```\nDone")

	if !strings.Contains(rendered, ui.mint("Summary")) {
		t.Fatalf("expected paragraph text to keep assistant body tone, got %q", rendered)
	}
	if !strings.Contains(rendered, ui.assistantCode("```go")) {
		t.Fatalf("expected fence line to use code tone, got %q", rendered)
	}
	if !strings.Contains(rendered, ui.assistantCode("fmt.Println(\"hi\")")) {
		t.Fatalf("expected code body to use separate tone, got %q", rendered)
	}
	if !strings.Contains(rendered, ui.mint("Done")) {
		t.Fatalf("expected trailing paragraph to return to body tone, got %q", rendered)
	}
}

func TestFormatCompletionSuggestionsShowsCommandDescriptions(t *testing.T) {
	ui := UI{color: false}
	rendered := ui.formatCompletionSuggestions([]string{"/status", "/verify", "/simulate"}, "/")

	for _, needle := range []string{
		"Commands",
		"/status",
		"Show current session state, approvals, and extension status.",
		"/verify",
		"Run manual verification for the current workspace state.",
		"/simulate",
		"Run or inspect anti-tamper simulation profiles.",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected command completion rendering to contain %q, got %q", needle, rendered)
		}
	}
}

func TestFormatCompletionSuggestionsShowsSubcommandDescriptions(t *testing.T) {
	ui := UI{color: false}
	rendered := ui.formatCompletionSuggestions([]string{"/new-feature status", "/new-feature implement"}, "/new-feature ")

	for _, needle := range []string{
		"/new-feature status",
		"Show the current state of a tracked feature.",
		"/new-feature implement",
		"Execute the next implementation slice for a tracked feature.",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected subcommand completion rendering to contain %q, got %q", needle, rendered)
		}
	}
}
