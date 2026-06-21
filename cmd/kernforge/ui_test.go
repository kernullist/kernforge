package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

	rendered := ui.statusKVAligned("memory-inspection-analyst", "openrouter / z-ai/glm-5.1", 30)
	if strings.Contains(rendered, "->") {
		t.Fatalf("expected aligned helper to avoid arrow fallback, got %q", rendered)
	}
	if !strings.Contains(rendered, "memory-inspection-analyst:") {
		t.Fatalf("expected aligned helper to keep colon layout, got %q", rendered)
	}
}

func TestStatusPillAndSummaryLineRenderCompactOverview(t *testing.T) {
	ui := UI{color: false}

	pill := ui.statusPill("gate", "needs_review", "warn")
	if pill != "[gate:needs_review]" {
		t.Fatalf("unexpected status pill: %q", pill)
	}

	summary := ui.summaryLine("", pill, "  ", ui.statusPill("mcp", "2/3 ok 1 fail", "warn"))
	if summary != "[gate:needs_review]  [mcp:2/3 ok 1 fail]" {
		t.Fatalf("unexpected summary line: %q", summary)
	}
}

func TestColorsEnabledRespectsNoColorAndForceColor(t *testing.T) {
	t.Setenv("TERM", "")
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("NO_COLOR", "")
	if !colorsEnabled() {
		t.Fatalf("expected FORCE_COLOR to enable color")
	}

	t.Setenv("NO_COLOR", "1")
	if colorsEnabled() {
		t.Fatalf("expected NO_COLOR to override FORCE_COLOR")
	}
}

func TestCompactThinkingStatusDoesNotSplitKoreanUTF8(t *testing.T) {
	status := "worker root: deepseek / deepseek-v4-pro 모델 응답 대기 중 (1m20s)"
	got := compactThinkingStatus(Config{AutoLocale: boolPtr(false)}, status)

	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8 status, got %q", got)
	}
	if strings.Contains(got, "\uFFFD") {
		t.Fatalf("expected status truncation to avoid replacement characters, got %q", got)
	}
	if got != "worker root: 모델 답변 대기 중 ..." {
		t.Fatalf("expected model-wait status to remain readable, got %q", got)
	}
	if visibleLen(got) > 72 {
		t.Fatalf("expected compact status to fit display width, width=%d status=%q", visibleLen(got), got)
	}
}

func TestCompactThinkingStatusKeepsSynthesisModelWaitReadable(t *testing.T) {
	t.Setenv("LANG", "ko_KR.UTF-8")
	got := compactThinkingStatus(Config{AutoLocale: boolPtr(true)}, "synthesis: anthropic-claude-cli / default 답변을 기다리는 중입니다(1m31s 경과).")

	if got != "synthesis: 모델 답변 대기 중 ..." {
		t.Fatalf("expected readable synthesis wait status, got %q", got)
	}
	if strings.Contains(got, "답...") {
		t.Fatalf("expected status not to cut the Korean answer word, got %q", got)
	}
}

func TestThinkingLineFitsWidthAndKeepsCancelHint(t *testing.T) {
	ui := UI{color: false}
	line := ui.thinkingLineForWidth("-", 5493*time.Second, "synthesis: 모델 답변 대기 중 ...", 72)

	if visibleLen(line) > 71 {
		t.Fatalf("expected thinking line to fit terminal width, width=%d line=%q", visibleLen(line), line)
	}
	for _, want := range []string{"[thinking]", "synthesis: 모델 답변 대기 중 ...", "[5493s | Esc]"} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected thinking line to contain %q, got %q", want, line)
		}
	}
}

func TestTruncateStatusSnippetDoesNotSplitKoreanUTF8(t *testing.T) {
	status := "provider retry: 모델 응답 대기 중이며 다음 route permit을 기다리는 중입니다"
	got := truncateStatusSnippet(status, 32)

	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8 snippet, got %q", got)
	}
	if strings.Contains(got, "\uFFFD") {
		t.Fatalf("expected snippet truncation to avoid replacement characters, got %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected long snippet to be truncated with ellipsis, got %q", got)
	}
	if visibleLen(got) > 32 {
		t.Fatalf("expected compact snippet to fit display width, width=%d snippet=%q", visibleLen(got), got)
	}
}

func TestPromptOmitsModelTargetAndStaysClean(t *testing.T) {
	ui := UI{color: false}
	prompt := ui.prompt()
	if prompt != "you > " {
		t.Fatalf("unexpected prompt rendering: %q", prompt)
	}
	// The active provider/model/effort now lives in the operator status footer,
	// not the input prompt line; make sure none of it leaked back into the prompt.
	for _, leaked := range []string{"gpt-", "openai", "effort=", "[", "]"} {
		if strings.Contains(prompt, leaked) {
			t.Fatalf("prompt should not include model/target detail %q, got %q", leaked, prompt)
		}
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

func TestActivityBadgeDistinguishesMainAndReviewModels(t *testing.T) {
	ui := UI{color: true}

	mainBadge := ui.activityBadge("main")
	reviewBadge := ui.activityBadge("review")
	if !strings.Contains(mainBadge, "[main") {
		t.Fatalf("expected main badge, got %q", mainBadge)
	}
	if !strings.Contains(reviewBadge, "[review") {
		t.Fatalf("expected review badge, got %q", reviewBadge)
	}
	if !strings.Contains(mainBadge, "38;5;214") {
		t.Fatalf("expected main badge to use accent2 color, got %q", mainBadge)
	}
	if !strings.Contains(reviewBadge, "38;5;218") {
		t.Fatalf("expected review badge to use review color, got %q", reviewBadge)
	}
}

func TestClassifyProgressKindDistinguishesReviewStages(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{text: "Main model is reading the code and checking the repair direction from the collected local evidence.", want: "main"},
		{text: "Main model code review result: completed (quality=usable, findings=3).", want: "main"},
		{text: "Review model is cross-checking the main model draft and the same evidence before the final gate is decided.", want: "review"},
		{text: "Review model cross-check result: cross completed (quality=usable, findings=1).", want: "review"},
		{text: "메인 모델이 코드를 읽고 수정 방향을 검토합니다.", want: "main"},
		{text: "리뷰 모델 검토 결과가 나왔습니다. Kernforge가 두 리뷰 결과를 병합해 최종 게이트를 계산합니다.", want: "review"},
	}

	for _, tc := range cases {
		if got := classifyProgressKind(tc.text); got != tc.want {
			t.Fatalf("classifyProgressKind(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestCompactThinkingStatusUsesReviewStageLanguage(t *testing.T) {
	cfg := Config{AutoLocale: boolPtr(false)}
	if got := compactThinkingStatus(cfg, "Main model is reading the code and checking the repair direction from the collected local evidence."); got != "Main model is reading code..." {
		t.Fatalf("unexpected main review status: %q", got)
	}
	if got := compactThinkingStatus(cfg, "Review model cross-check result: cross completed (quality=usable, findings=1)."); got != "Review model result received." {
		t.Fatalf("unexpected review result status: %q", got)
	}

	t.Setenv("LANG", "ko_KR.UTF-8")
	ko := Config{AutoLocale: boolPtr(true)}
	if got := compactThinkingStatus(ko, "메인 모델이 코드를 읽고 수정 방향을 검토합니다."); got != "메인 모델이 코드 검토 중 ..." {
		t.Fatalf("unexpected Korean main review status: %q", got)
	}
	if got := compactThinkingStatus(ko, "리뷰 모델 검토 결과가 나왔습니다. Kernforge가 두 리뷰 결과를 병합해 최종 게이트를 계산합니다."); got != "리뷰 모델 검토 결과 수신." {
		t.Fatalf("unexpected Korean review result status: %q", got)
	}
}

func TestTurnElapsedLineUsesTimeBadge(t *testing.T) {
	ui := UI{color: false}

	line := ui.turnElapsedLine(Config{AutoLocale: boolPtr(false)}, 90*time.Second)
	for _, want := range []string{"[time", "turn elapsed: 1m30s"} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected turn elapsed line to contain %q, got %q", want, line)
		}
	}

	t.Setenv("LANG", "ko_KR.UTF-8")
	korean := ui.turnElapsedLine(Config{AutoLocale: boolPtr(true)}, 75*time.Second)
	for _, want := range []string{"[time", "턴 소요 시간: 1m15s"} {
		if !strings.Contains(korean, want) {
			t.Fatalf("expected localized turn elapsed line to contain %q, got %q", want, korean)
		}
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

func TestShellWithMetaAppendsExecutionSummary(t *testing.T) {
	ui := UI{color: false}
	rendered := ui.shellWithMeta("line1\n", "exit=0", "12ms")
	if !strings.Contains(rendered, ">> shell output [1 line(s), exit=0, 12ms] ") {
		t.Fatalf("expected shell output header metadata, got %q", rendered)
	}
	if !strings.Contains(rendered, "line1") {
		t.Fatalf("expected shell body to remain visible, got %q", rendered)
	}
}

func TestStatusSummaryBlockSplitsWhenTerminalIsNarrow(t *testing.T) {
	ui := UI{color: false}
	rendered := ui.statusSummaryBlock("status", []statusSummaryItem{
		{Label: "cwd", Value: "kernforge", Tone: "info"},
		{Label: "provider", Value: "openrouter/google/gemini-2.5-pro", Tone: "info"},
		{Label: "gate", Value: "ready", Tone: "ready"},
		{Label: "perm", Value: "danger-full-access", Tone: "warn"},
		{Label: "progress", Value: "compact", Tone: "info"},
	}, 76)

	if !strings.Contains(rendered, "\n") {
		t.Fatalf("expected narrow status summary to split, got %q", rendered)
	}
	if !strings.Contains(rendered, "[provider:openrouter/google/gemini-2.5-pro]") ||
		!strings.Contains(rendered, "[progress:compact]") {
		t.Fatalf("expected split summary to retain all status pills, got %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if visibleLen(line) > 76 {
			t.Fatalf("expected split summary line to fit width, got width=%d line=%q", visibleLen(line), line)
		}
	}
}

func TestStatusSummaryBlockUsesActualNarrowWidth(t *testing.T) {
	ui := UI{color: false}
	rendered := ui.statusSummaryBlock("status", []statusSummaryItem{
		{Label: "cwd", Value: "kernforge", Tone: "info"},
		{Label: "gate", Value: "ready", Tone: "ready"},
		{Label: "memory", Value: "0", Tone: "info"},
	}, 44)

	if !strings.Contains(rendered, "\n") {
		t.Fatalf("expected status summary to split at actual width, got %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if visibleLen(line) > 44 {
			t.Fatalf("expected split summary line to fit actual width, got width=%d line=%q", visibleLen(line), line)
		}
	}
}

func TestStatusSummaryBlockSplitsTwoItemsWhenNeeded(t *testing.T) {
	ui := UI{color: false}
	rendered := ui.statusSummaryBlock("status", []statusSummaryItem{
		{Label: "provider", Value: "openrouter/google/gemini-2.5-pro", Tone: "info"},
		{Label: "perm", Value: "danger-full-access", Tone: "warn"},
	}, 62)

	if !strings.Contains(rendered, "\n") {
		t.Fatalf("expected two-item summary to split when it exceeds width, got %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if visibleLen(line) > 62 {
			t.Fatalf("expected two-item split line to fit width, got width=%d line=%q", visibleLen(line), line)
		}
	}
}

func TestStatusSummaryBlockAlignsLongWelcomeFooterRows(t *testing.T) {
	ui := UI{color: false}
	width := operatorFooterDisplayWidth(120)
	rendered := ui.statusSummaryBlock("status", []statusSummaryItem{
		{Label: "cwd", Value: "im-tavern-client", Tone: "info"},
		{Label: "provider", Value: "LM Studio/qwen3.6-35b-a3b...ucs-aggressive", Tone: "info"},
		{Label: "gate", Value: "needs_review/2 warnings", Tone: "warn"},
		{Label: "perm", Value: "workspace", Tone: "info"},
		{Label: "progress", Value: "compact", Tone: "info"},
		{Label: "mcp", Value: "0", Tone: "info"},
		{Label: "skills", Value: "0/0", Tone: "info"},
		{Label: "verify", Value: "none", Tone: "info"},
		{Label: "memory", Value: "174", Tone: "info"},
	}, width)

	if !strings.Contains(rendered, "\n") {
		t.Fatalf("expected long welcome footer status to split, got %q", rendered)
	}
	if !strings.Contains(rendered, "[gate:needs_review/2 warnings]") {
		t.Fatalf("expected gate pill to remain intact, got %q", rendered)
	}
	lines := strings.Split(rendered, "\n")
	for index, line := range lines {
		if visibleLen(line) > width {
			t.Fatalf("expected line %d to fit footer width=%d, got width=%d line=%q", index, width, visibleLen(line), line)
		}
		if index > 0 && !strings.HasPrefix(line, "       [") {
			t.Fatalf("expected continuation line %d to align under status items, got %q", index, line)
		}
	}
}

func TestShellNormalizesCarriageReturnOnlyLineEndings(t *testing.T) {
	ui := UI{color: false}
	rendered := ui.shell("line1\rline2\rline3\r")
	if !strings.Contains(rendered, ">> shell output [3 line(s)] ") {
		t.Fatalf("expected carriage-return lines to be counted, got %q", rendered)
	}
	if !strings.Contains(rendered, "line1\nline2\nline3") {
		t.Fatalf("expected carriage-return lines to render as normal lines, got %q", rendered)
	}
}

func TestTruncateDisplayTextMiddlePreservesTail(t *testing.T) {
	got := truncateDisplayTextMiddle("google/gemini-2.5-pro-preview-2026-critical-tail", 24)
	if !strings.HasPrefix(got, "google/") {
		t.Fatalf("expected provider-style prefix to remain visible, got %q", got)
	}
	if !strings.HasSuffix(got, "tical-tail") {
		t.Fatalf("expected important tail to remain visible, got %q", got)
	}
	if visibleLen(got) > 24 {
		t.Fatalf("expected middle truncation to respect display limit, got %q", got)
	}
}

func TestShellCollapsesLongOutputWithHeadAndTail(t *testing.T) {
	ui := UI{color: false}
	totalLines := shellOutputPreviewHeadLines + shellOutputPreviewTailLines + 20
	lines := make([]string, 0, totalLines)
	for i := 0; i < totalLines; i++ {
		lines = append(lines, fmt.Sprintf("line%03d", i+1))
	}

	rendered := ui.shell(strings.Join(lines, "\n"))
	marker := fmt.Sprintf(
		"[output collapsed: 20 line(s) omitted; showing first %d and last %d line(s)]",
		shellOutputPreviewHeadLines,
		shellOutputPreviewTailLines,
	)
	for _, want := range []string{
		">> shell output [120 line(s), collapsed] ",
		"line001",
		"line080",
		marker,
		"line101",
		"line120",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected collapsed shell output to contain %q, got %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "line090") {
		t.Fatalf("expected middle shell output to be omitted, got %q", rendered)
	}
}

func TestShellCollapseMarkerUsesKoreanLocale(t *testing.T) {
	t.Setenv("LANG", "ko_KR.UTF-8")
	ui := UI{color: false}
	totalLines := shellOutputPreviewHeadLines + shellOutputPreviewTailLines + 1
	lines := make([]string, 0, totalLines)
	for i := 0; i < totalLines; i++ {
		lines = append(lines, fmt.Sprintf("line%03d", i+1))
	}

	rendered := ui.shellWithMetaLocalized(Config{}, strings.Join(lines, "\n"))
	if !strings.Contains(rendered, "[출력 접힘: 1줄 생략; 처음 80줄과 마지막 20줄 표시]") {
		t.Fatalf("expected Korean collapse marker, got %q", rendered)
	}
}

func TestShellCollapsesVeryLongSingleLineOutput(t *testing.T) {
	ui := UI{color: false}
	body := strings.Repeat("x", shellOutputPreviewMaxChars+100)

	rendered := ui.shell(body)
	marker := fmt.Sprintf(
		"[output collapsed: 100 char(s) omitted; showing first %d and last %d char(s)]",
		shellOutputPreviewMaxChars-shellOutputPreviewTailChars,
		shellOutputPreviewTailChars,
	)
	for _, want := range []string{
		">> shell output [1 line(s), collapsed] ",
		marker,
		strings.Repeat("x", 80),
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected long single-line shell output to contain %q, got %q", want, rendered)
		}
	}
}

func TestShellCollapsesVeryLongUnicodeOutputWithoutSplittingUTF8(t *testing.T) {
	ui := UI{color: false}
	body := strings.Repeat("가", shellOutputPreviewMaxChars+100)

	rendered := ui.shell(body)
	if !utf8.ValidString(rendered) {
		t.Fatalf("expected collapsed shell output to remain valid UTF-8, got %q", rendered)
	}
	if strings.Contains(rendered, "\uFFFD") {
		t.Fatalf("expected collapsed shell output to avoid replacement characters, got %q", rendered)
	}
	if !strings.Contains(rendered, "[output collapsed: 100 char(s) omitted;") {
		t.Fatalf("expected collapsed shell output marker, got %q", rendered)
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
	// Inside a known-language fence the body is now syntax-highlighted: the
	// string literal carries the string tone rather than the flat code tone.
	if !strings.Contains(rendered, ui.paintSyntax(syntaxStringCode, "\"hi\"")) {
		t.Fatalf("expected code body string literal to be highlighted, got %q", rendered)
	}
	if !strings.Contains(rendered, ui.mint("Done")) {
		t.Fatalf("expected trailing paragraph to return to body tone, got %q", rendered)
	}
}

func TestAssistantBodyFramesEachLineWithLeftRailWhenColorEnabled(t *testing.T) {
	ui := UI{color: true}
	rendered := ui.assistant("Summary:\n- first\n- second")

	gutter := ui.assistantGutter()
	for _, needle := range []string{
		gutter + ui.mint("Summary:"),
		gutter + ui.mint("- first"),
		gutter + ui.mint("- second"),
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected body line framed by left rail %q, got %q", needle, rendered)
		}
	}
	// The rail must not bleed into the no-color path.
	plain := UI{color: false}.assistant("Summary:\n- first")
	if strings.Contains(plain, assistantGutterBar) {
		t.Fatalf("expected no rail in plain output, got %q", plain)
	}
}

func TestAssistantStreamDeltaDrawsRailOncePerLine(t *testing.T) {
	ui := UI{color: true}
	var ctx assistantRenderContext
	prefix := ""

	// A line split across two deltas must carry exactly one rail.
	out := ui.renderAssistantStreamDelta("hel", &ctx, &prefix)
	out += ui.renderAssistantStreamDelta("lo\nworld\n", &ctx, &prefix)

	if got := strings.Count(out, assistantGutterBar); got != 2 {
		t.Fatalf("expected one rail per line (2 total), got %d in %q", got, out)
	}
	if prefix != "" {
		t.Fatalf("expected line prefix reset after completed lines, got %q", prefix)
	}
}

func TestAssistantClosingRailCarriesElapsed(t *testing.T) {
	plain := UI{color: false}.assistantClosingRail(805 * time.Second)
	if !strings.Contains(plain, assistantRailCorner) || !strings.Contains(plain, "13m25s") {
		t.Fatalf("expected closing rail with elapsed, got %q", plain)
	}
}

func TestAssistantCollapsesRepeatedSentenceRun(t *testing.T) {
	ui := UI{color: false}
	repeated := "검증 실패가 현재 patch scope 밖입니다. 검증 실패가 현재 patch scope 밖입니다. 검증 실패가 현재 patch scope 밖입니다.수정 완료했습니다."
	rendered := ui.assistant(repeated)

	if strings.Count(rendered, "검증 실패가 현재 patch scope 밖입니다.") != 1 {
		t.Fatalf("expected repeated sentence to be collapsed, got %q", rendered)
	}
	if !strings.Contains(rendered, "수정 완료했습니다.") {
		t.Fatalf("expected trailing content to remain, got %q", rendered)
	}
	if strings.Contains(rendered, "입니다.수정") {
		t.Fatalf("expected adjacent Korean sentences to be spaced after collapse, got %q", rendered)
	}
}

func TestAssistantDoesNotCollapseCodeFenceRepeatedText(t *testing.T) {
	ui := UI{color: false}
	repeated := "```\n검증 실패가 현재 patch scope 밖입니다. 검증 실패가 현재 patch scope 밖입니다. 검증 실패가 현재 patch scope 밖입니다.\n```"
	rendered := ui.assistant(repeated)

	if strings.Count(rendered, "검증 실패가 현재 patch scope 밖입니다.") != 3 {
		t.Fatalf("expected repeated code text to remain intact, got %q", rendered)
	}
}

func TestAssistantSentenceSpacingPreservesCodeLikeDots(t *testing.T) {
	ui := UI{color: false}
	rendered := ui.assistant("PathConverter.cpp:132 확인.수정 완료했습니다.\n```\n확인.수정\n```")

	if !strings.Contains(rendered, "PathConverter.cpp:132") {
		t.Fatalf("expected path-like dots to remain untouched, got %q", rendered)
	}
	if !strings.Contains(rendered, "확인. 수정 완료했습니다.") {
		t.Fatalf("expected adjacent Korean sentences to be spaced, got %q", rendered)
	}
	if !strings.Contains(rendered, "확인.수정") {
		t.Fatalf("expected code fence text to remain untouched, got %q", rendered)
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
		"Run verification and suggest the next repair, dashboard, checkpoint, or feature workflow step.",
		"/simulate",
		"Run anti-tamper simulation profiles and suggest verification or evidence follow-up.",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected command completion rendering to contain %q, got %q", needle, rendered)
		}
	}
}

func TestFormatCompletionSuggestionsShowsSubcommandDescriptions(t *testing.T) {
	ui := UI{color: false}
	rendered := ui.formatCompletionSuggestions([]string{"/new-feature next", "/new-feature list"}, "/new-feature ")

	for _, needle := range []string{
		"/new-feature next",
		"Run the next safe lifecycle action for the active tracked feature.",
		"/new-feature list",
		"List tracked feature workspaces.",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected subcommand completion rendering to contain %q, got %q", needle, rendered)
		}
	}
}

func TestFormatCompletionSuggestionsShowsReviewSubcommandDescriptions(t *testing.T) {
	ui := UI{color: false}
	rendered := ui.formatCompletionSuggestions([]string{"/review change", "/review plan", "/model cross-review", "/review --mode"}, "/review ")

	for _, needle := range []string{
		"/review change",
		"Review the current workspace diff",
		"/review plan",
		"Review an implementation plan",
		"/model cross-review",
		"independent second-pass reviewer route",
		"/review --mode",
		"Force review mode",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected review completion rendering to contain %q, got %q", needle, rendered)
		}
	}
	if strings.Count(rendered, "Run the common review harness") > 1 {
		t.Fatalf("expected review subcommands to avoid repeated parent descriptions, got %q", rendered)
	}
}

func TestFormatCompletionSuggestionsShowsAnalyzeProjectModeDescriptionsAfterPath(t *testing.T) {
	ui := UI{color: false}
	rendered := ui.formatCompletionSuggestions([]string{
		"/analyze-project --path SampleKernel/SampleKernel/ --mode map",
		"/analyze-project --path SampleKernel/SampleKernel/ --mode trace",
	}, "/analyze-project --path SampleKernel/SampleKernel/ --mode ")

	for _, needle := range []string{
		"/analyze-project --path SampleKernel/SampleKernel/ --mode map",
		"Build the default architecture map:",
		"/analyze-project --path SampleKernel/SampleKernel/ --mode trace",
		"Follow one runtime or request flow through",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected analyze-project mode completion rendering to contain %q, got %q", needle, rendered)
		}
	}
	if strings.Contains(rendered, "Limit analysis to one workspace directory or file path") {
		t.Fatalf("expected mode descriptions after --path, got %q", rendered)
	}
}
