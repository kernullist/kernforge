package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type UI struct {
	color bool
}

const (
	shellOutputPreviewHeadLines = 80
	shellOutputPreviewTailLines = 20
	shellOutputPreviewMaxChars  = 12000
	shellOutputPreviewTailChars = 4000
)

type shellOutputPreview struct {
	Text      string
	LineCount int
	Collapsed bool
}

type statusSummaryItem struct {
	Label string
	Value string
	Tone  string
}

func NewUI() UI {
	color := colorsEnabled()
	if color {
		ensureVirtualTerminalProcessing()
	}
	return UI{color: color}
}

func colorsEnabled() bool {
	if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" {
		return false
	}
	forceColor := strings.TrimSpace(os.Getenv("FORCE_COLOR"))
	if forceColor != "" && forceColor != "0" {
		return true
	}
	term := strings.TrimSpace(os.Getenv("TERM"))
	if strings.EqualFold(term, "dumb") {
		return false
	}
	if info, err := os.Stdout.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	return true
}

func (ui UI) paint(code, text string) string {
	if !ui.color || text == "" {
		return text
	}
	ensureVirtualTerminalProcessing()
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (ui UI) bold(text string) string          { return ui.paint("1", text) }
func (ui UI) dim(text string) string           { return ui.paint("38;5;245", text) }
func (ui UI) accent(text string) string        { return ui.paint("38;5;81", text) }
func (ui UI) accent2(text string) string       { return ui.paint("38;5;214", text) }
func (ui UI) success(text string) string       { return ui.paint("38;5;42", text) }
func (ui UI) warn(text string) string          { return ui.paint("38;5;220", text) }
func (ui UI) error(text string) string         { return ui.paint("38;5;203", text) }
func (ui UI) info(text string) string          { return ui.paint("38;5;117", text) }
func (ui UI) cloud(text string) string         { return ui.paint("38;5;255", text) }
func (ui UI) blush(text string) string         { return ui.paint("38;5;218", text) }
func (ui UI) mint(text string) string          { return ui.paint("38;5;121", text) }
func (ui UI) assistantCode(text string) string { return ui.paint("38;5;153", text) }

func (ui UI) clearScreen() string {
	ensureVirtualTerminalProcessing()
	return "\x1b[2J\x1b[H"
}

func (ui UI) banner(provider, model, sessionID, cwd string) string {
	if strings.TrimSpace(provider) == "" {
		provider = "(unset)"
	}
	if strings.TrimSpace(model) == "" {
		model = "(unset)"
	}
	width := terminalWidth()
	if width < 72 {
		width = 72
	}
	if width > 108 {
		width = 108
	}

	divider := ui.dim(strings.Repeat("-", width))
	meta := ui.bannerMeta("provider", ui.info(provider)) +
		"  " + ui.bannerMeta("model", ui.info(model)) +
		"  " + ui.bannerMeta("session", ui.dim(sessionID))
	workspaceLine := ui.bannerMeta("workspace", ui.dim(cwd))
	readinessLine := ui.bannerMeta(
		"ready",
		ui.bold(ui.accent("edit"))+
			ui.dim(" / ")+
			ui.bold(ui.warn("review"))+
			ui.dim(" / ")+
			ui.bold(ui.success("verify")),
	)
	commandLine := ui.bannerMeta("commands", ui.highlightCommands("/help /status /model /config")) +
		"  " + ui.bannerMeta("shell", ui.highlightCommands("!cmd")) +
		"  " + ui.bannerMeta("files", ui.highlightCommands("@path"))
	tipLine := ui.bannerMeta("tip", ui.info("Esc cancels the active turn. End a line with \\ for multiline input."))
	hero := ui.bannerHero(
		ui.bannerLogo(),
		[]string{
			ui.bold(ui.accent("Kernforge")) + "  " + ui.accent("version") + "=" + ui.info(currentVersion()),
			ui.dim("forge-ready terminal coding agent"),
			ui.bold(ui.cloud("Welcome back.")),
			ui.info("Describe the task and Kernforge will inspect, edit, and verify with you."),
			meta,
			workspaceLine,
		},
		3,
	)

	lines := []string{
		divider,
		hero,
		"",
		readinessLine,
		commandLine,
		tipLine,
		divider,
	}
	return strings.Join(lines, "\n")
}

func (ui UI) prompt(provider, model string, effort string) string {
	target := ui.promptTarget(provider, model, effort)
	return ui.bold(ui.accent("you")) + " " + ui.dim("["+target+"]") + ui.accent(" > ")
}

func (ui UI) turnSeparator(turn int, provider, model string) string {
	_ = turn
	_ = provider
	_ = model

	width := terminalWidth()
	if width < 48 {
		width = 48
	}
	if width > 96 {
		width = 96
	}

	span := width / 2
	if span < 28 {
		span = 28
	}
	if span > 44 {
		span = 44
	}
	return ui.dim(strings.Repeat("-", span))
}

func (ui UI) continuationPrompt() string {
	return ui.dim("... ")
}

func (ui UI) assistant(text string) string {
	body := formatAssistantText(text)
	if body == "" {
		return ""
	}
	return ui.assistantHeader() + "\n" + ui.renderAssistantBody(body)
}

func (ui UI) shell(text string) string {
	return ui.shellWithMeta(text)
}

func (ui UI) shellWithMeta(text string, extraMeta ...string) string {
	return ui.shellWithMetaForLocale(false, text, extraMeta...)
}

func (ui UI) shellWithMetaLocalized(cfg Config, text string, extraMeta ...string) string {
	return ui.shellWithMetaForLocale(localePrefersKorean(cfg), text, extraMeta...)
}

func (ui UI) shellWithMetaForLocale(korean bool, text string, extraMeta ...string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	preview := shellOutputPreviewForLocale(text, korean)
	metaItems := []string{fmt.Sprintf("%d line(s)", preview.LineCount)}
	if preview.Collapsed {
		metaItems = append(metaItems, "collapsed")
	}
	metaItems = append(metaItems, cleanShellOutputMetaItems(extraMeta)...)
	meta := strings.Join(metaItems, ", ")
	return ui.outputHeader("shell output", meta, ui.accent2) + "\n" + ui.cloud(preview.Text)
}

func cleanShellOutputMetaItems(items []string) []string {
	cleaned := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.Join(strings.Fields(strings.TrimSpace(item)), " ")
		if trimmed == "" {
			continue
		}
		cleaned = append(cleaned, trimmed)
	}
	return cleaned
}

func shellOutputPreviewFor(text string) shellOutputPreview {
	return shellOutputPreviewForLocale(text, false)
}

func shellOutputPreviewForLocale(text string, korean bool) shellOutputPreview {
	body := normalizeBlockLineEndings(strings.TrimRight(text, "\r\n"))
	preview := shellOutputPreview{
		Text:      body,
		LineCount: countBlockLines(body),
	}
	if body == "" {
		return preview
	}

	if preview.LineCount > shellOutputPreviewHeadLines+shellOutputPreviewTailLines {
		lines := strings.Split(normalizeBlockLineEndings(body), "\n")
		preview.Text = collapseShellOutputLines(lines, korean)
		preview.Collapsed = true
	}

	if utf8.RuneCountInString(preview.Text) > shellOutputPreviewMaxChars {
		preview.Text = collapseShellOutputChars(preview.Text, korean)
		preview.Collapsed = true
	}

	return preview
}

func collapseShellOutputLines(lines []string, korean bool) string {
	maxLines := shellOutputPreviewHeadLines + shellOutputPreviewTailLines
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}

	omitted := len(lines) - shellOutputPreviewHeadLines - shellOutputPreviewTailLines
	if omitted <= 0 {
		return strings.Join(lines, "\n")
	}

	out := make([]string, 0, maxLines+1)
	out = append(out, lines[:shellOutputPreviewHeadLines]...)
	out = append(out, shellOutputLinesCollapsedMarker(omitted, shellOutputPreviewHeadLines, shellOutputPreviewTailLines, korean))
	out = append(out, lines[len(lines)-shellOutputPreviewTailLines:]...)
	return strings.Join(out, "\n")
}

func shellOutputLinesCollapsedMarker(omitted int, headLines int, tailLines int, korean bool) string {
	if korean {
		return fmt.Sprintf(
			"[출력 접힘: %d줄 생략; 처음 %d줄과 마지막 %d줄 표시]",
			omitted,
			headLines,
			tailLines,
		)
	}
	return fmt.Sprintf(
		"[output collapsed: %d line(s) omitted; showing first %d and last %d line(s)]",
		omitted,
		headLines,
		tailLines,
	)
}

func collapseShellOutputChars(text string, korean bool) string {
	runes := []rune(text)
	if len(runes) <= shellOutputPreviewMaxChars {
		return text
	}

	tailChars := shellOutputPreviewTailChars
	if tailChars >= shellOutputPreviewMaxChars {
		tailChars = shellOutputPreviewMaxChars / 3
	}
	headChars := shellOutputPreviewMaxChars - tailChars
	if headChars <= 0 || tailChars <= 0 || len(runes) <= headChars+tailChars {
		return text
	}

	omitted := len(runes) - headChars - tailChars
	marker := shellOutputCharsCollapsedMarker(omitted, headChars, tailChars, korean)
	return string(runes[:headChars]) + "\n" + marker + "\n" + string(runes[len(runes)-tailChars:])
}

func shellOutputCharsCollapsedMarker(omitted int, headChars int, tailChars int, korean bool) string {
	if korean {
		return fmt.Sprintf(
			"[출력 접힘: %d자 생략; 처음 %d자와 마지막 %d자 표시]",
			omitted,
			headChars,
			tailChars,
		)
	}
	return fmt.Sprintf(
		"[output collapsed: %d char(s) omitted; showing first %d and last %d char(s)]",
		omitted,
		headChars,
		tailChars,
	)
}

func normalizeBlockLineEndings(text string) string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(normalized, "\r", "\n")
}

func (ui UI) statusKV(key, value string) string {
	trimmedKey := strings.TrimSpace(key)
	if trimmedKey == "" {
		trimmedKey = "-"
	}
	if ui.shouldCompactStatusKey(trimmedKey) {
		return ui.statusKVAligned(trimmedKey, value, 25)
	}
	return ui.bold(ui.accent(trimmedKey)) + ui.dim(" -> ") + value
}

func (ui UI) statusKVAligned(key, value string, width int) string {
	trimmedKey := strings.TrimSpace(key)
	if trimmedKey == "" {
		trimmedKey = "-"
	}
	label := trimmedKey + ":"
	if width <= 0 {
		width = 25
	}
	return ui.bold(ui.accent(padDisplayRight(label, width))) + ui.dim(" ") + value
}

func (ui UI) statusPill(label, value string, tone string) string {
	trimmedLabel := strings.TrimSpace(label)
	trimmedValue := strings.TrimSpace(value)
	if trimmedLabel == "" {
		trimmedLabel = "state"
	}
	if trimmedValue == "" {
		trimmedValue = "unknown"
	}
	text := "[" + trimmedLabel + ":" + trimmedValue + "]"
	switch strings.ToLower(strings.TrimSpace(tone)) {
	case "success", "ready", "ok", "pass":
		return ui.success(text)
	case "warn", "warning", "needs_review", "skip":
		return ui.warn(text)
	case "error", "fail", "blocked":
		return ui.error(text)
	case "info", "active":
		return ui.info(text)
	default:
		return ui.dim(text)
	}
}

func (ui UI) summaryLine(items ...string) string {
	cleaned := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) == "" {
			continue
		}
		cleaned = append(cleaned, strings.TrimSpace(item))
	}
	if len(cleaned) == 0 {
		return ""
	}
	return strings.Join(cleaned, "  ")
}

func (ui UI) statusSummaryBlock(prefix string, items []statusSummaryItem, width int) string {
	rendered := ui.renderStatusSummaryItems(items)
	if len(rendered) == 0 {
		return ""
	}
	if width <= 0 {
		width = terminalWidth()
	}
	prefix = strings.TrimSpace(prefix)
	if prefix != "" {
		prefix += " "
	}
	prefixText := ui.dim(prefix)
	oneLine := prefixText + ui.summaryLine(rendered...)
	if visibleLen(oneLine) <= width || len(rendered) == 1 {
		return oneLine
	}

	contentWidth := width - visibleLen(prefix)
	if contentWidth < 1 {
		contentWidth = 1
	}
	rows := splitStatusSummaryRows(rendered, contentWidth)
	if len(rows) == 1 {
		return oneLine
	}
	lines := make([]string, 0, len(rows))
	for i, row := range rows {
		if len(row) == 0 {
			continue
		}
		if i == 0 {
			lines = append(lines, prefixText+ui.summaryLine(row...))
			continue
		}
		lines = append(lines, ui.dim(strings.Repeat(" ", visibleLen(prefix)))+ui.summaryLine(row...))
	}
	return strings.Join(lines, "\n")
}

func (ui UI) renderStatusSummaryItems(items []statusSummaryItem) []string {
	rendered := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Label) == "" && strings.TrimSpace(item.Value) == "" {
			continue
		}
		rendered = append(rendered, ui.statusPill(item.Label, item.Value, item.Tone))
	}
	return rendered
}

func splitStatusSummaryRows(items []string, width int) [][]string {
	if len(items) == 0 {
		return nil
	}
	if width <= 0 {
		width = 1
	}
	if visibleLen(strings.Join(items, "  ")) <= width {
		return [][]string{items}
	}
	if len(items) == 1 {
		return [][]string{items}
	}

	bestSplit := 1
	bestScore := int(^uint(0) >> 1)
	for split := 1; split < len(items); split++ {
		leftWidth := visibleLen(strings.Join(items[:split], "  "))
		rightWidth := visibleLen(strings.Join(items[split:], "  "))
		score := maxInt(leftWidth, rightWidth)
		if leftWidth > width {
			score += (leftWidth - width) * 4
		}
		if rightWidth > width {
			score += (rightWidth - width) * 2
		}
		if score < bestScore {
			bestScore = score
			bestSplit = split
		}
	}
	return [][]string{items[:bestSplit], items[bestSplit:]}
}

func (ui UI) section(title string) string {
	trimmedTitle := strings.TrimSpace(title)
	if trimmedTitle == "" {
		trimmedTitle = "Section"
	}
	label := "== " + trimmedTitle + " "
	return ui.bold(ui.accent2(label)) + ui.dim(strings.Repeat("=", ui.rulePadding(label, 6)))
}

func (ui UI) subsection(title string) string {
	trimmedTitle := strings.TrimSpace(title)
	if trimmedTitle == "" {
		trimmedTitle = "Group"
	}
	label := "-- " + trimmedTitle + " "
	return ui.bold(ui.info(label)) + ui.dim(strings.Repeat("-", ui.rulePadding(label, 4)))
}

func (ui UI) progressLine(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return ui.dim("  | ") + text
}

func (ui UI) turnElapsedLine(cfg Config, elapsed time.Duration) string {
	text := fmt.Sprintf(localizedText(cfg, "turn elapsed: %s", "턴 소요 시간: %s"), formatProgressElapsed(elapsed))
	return ui.progressLine(ui.activityLine("time", text))
}

func (ui UI) planItem(index int, status string, step string) string {
	number := fmt.Sprintf("%02d.", index+1)
	return ui.dim(number) + " " + ui.planBadge(status) + " " + strings.TrimSpace(step)
}

func (ui UI) assistantHeader() string {
	label := ">> assistant "
	return ui.bold(ui.mint(label)) + ui.dim(strings.Repeat("-", ui.rulePadding(label, 8)))
}

func (ui UI) outputHeader(title string, meta string, paint func(string) string) string {
	trimmedTitle := strings.TrimSpace(title)
	if trimmedTitle == "" {
		trimmedTitle = "output"
	}
	label := ">> " + trimmedTitle
	if trimmedMeta := strings.TrimSpace(meta); trimmedMeta != "" {
		label += " [" + trimmedMeta + "]"
	}
	label += " "
	return ui.bold(paint(label)) + ui.dim(strings.Repeat("-", ui.rulePadding(label, 8)))
}

func (ui UI) activityLine(kind string, text string) string {
	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return ""
	}
	return ui.activityBadge(kind) + " " + trimmedText
}

func (ui UI) successLine(text string) string {
	return ui.success("OK  " + text)
}

func (ui UI) infoLine(text string) string {
	return ui.info("INFO  " + text)
}

func (ui UI) warnLine(text string) string {
	return ui.warn("WARN  " + text)
}

func (ui UI) errorLine(text string) string {
	return ui.error("ERROR  " + text)
}

// maxThinkingElapsedDisplay caps how long the thinking spinner is allowed to
// show. The underlying timer can drift (e.g. machine sleep, a stale goroutine
// that survived a turn boundary), and runaway values like "[81389s | Esc]"
// only confuse the user. 2 hours is well past any reasonable single request
// — anything larger is almost certainly a clock or lifecycle bug, so we clamp
// the display rather than leaking the raw value.
const maxThinkingElapsedDisplay = 2 * time.Hour

func (ui UI) thinkingLine(frame string, elapsed time.Duration, status string) string {
	if elapsed < 0 {
		elapsed = 0
	}
	clampedDisplay := false
	if elapsed > maxThinkingElapsedDisplay {
		elapsed = maxThinkingElapsedDisplay
		clampedDisplay = true
	}
	seconds := int(elapsed.Seconds())
	if strings.TrimSpace(status) == "" {
		status = "Sending prompt to model..."
		if seconds >= 15 {
			status = "Still waiting for the model response..."
		}
	}
	line := ui.bold(ui.accent("[thinking]")) + " " + ui.dim("["+frame+"]")
	if !isRedundantThinkingStatus(status) {
		line += " " + ui.info(status)
	}
	if clampedDisplay {
		line += " " + ui.dim(fmt.Sprintf("[%ds+ | Esc]", seconds))
	} else {
		line += " " + ui.dim(fmt.Sprintf("[%ds | Esc]", seconds))
	}
	return line
}

func (ui UI) hintLine(text string) string {
	return ui.accent2("TIP") + "  " + ui.info(text)
}

func (ui UI) bannerMeta(key, value string) string {
	return ui.accent(key) + ui.dim("=") + value
}

func (ui UI) bannerLogo() []string {
	return []string{
		ui.dim(".--------------."),
		ui.bold(ui.accent("| K\\  /F====   |")),
		ui.bold(ui.accent2("| K \\/ F___    |")),
		ui.bold(ui.mint("| K /\\ F       |")),
		ui.bold(ui.info("| K/  \\F       |")),
		ui.dim("'--------------'"),
	}
}

func (ui UI) bannerHero(left []string, right []string, gap int) string {
	if gap < 1 {
		gap = 1
	}

	leftWidth := 0
	for _, line := range left {
		if visible := visibleLen(line); visible > leftWidth {
			leftWidth = visible
		}
	}

	totalLines := len(left)
	if len(right) > totalLines {
		totalLines = len(right)
	}

	var lines []string
	for i := 0; i < totalLines; i++ {
		leftLine := ""
		rightLine := ""
		if i < len(left) {
			leftLine = left[i]
		}
		if i < len(right) {
			rightLine = right[i]
		}
		if strings.TrimSpace(rightLine) == "" {
			lines = append(lines, leftLine)
			continue
		}
		lines = append(lines, padDisplayRight(leftLine, leftWidth)+strings.Repeat(" ", gap)+rightLine)
	}
	return strings.Join(lines, "\n")
}

func (ui UI) promptTarget(provider, model string, effort string) string {
	var parts []string
	if trimmed := strings.TrimSpace(providerUserLabel(provider)); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(model); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(effort); trimmed != "" {
		parts = append(parts, "effort="+trimmed)
	}
	if len(parts) == 0 {
		return "unconfigured"
	}
	return strings.Join(parts, " / ")
}

func (ui UI) shouldCompactStatusKey(key string) bool {
	if visibleLen(key) > 24 {
		return false
	}
	if strings.Contains(key, " ") {
		return false
	}
	if strings.ContainsAny(key, `/\`) {
		return false
	}
	return true
}

func (ui UI) rulePadding(label string, minimum int) int {
	width := terminalWidth()
	if width < 48 {
		width = 48
	}
	if width > 100 {
		width = 100
	}
	padding := width - visibleLen(label)
	if padding < minimum {
		padding = minimum
	}
	return padding
}

func (ui UI) planBadge(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed":
		return ui.success("[done]")
	case "in_progress":
		return ui.accent2("[work]")
	default:
		return ui.dim("[todo]")
	}
}

func (ui UI) verificationBadge(status VerificationStatus) string {
	switch status {
	case VerificationPassed:
		return ui.success("[pass]")
	case VerificationFailed:
		return ui.error("[fail]")
	case VerificationSkipped:
		return ui.dim("[skip]")
	default:
		return ui.accent("[wait]")
	}
}

func (ui UI) verificationStepLine(index int, step VerificationStep) string {
	number := fmt.Sprintf("%02d.", index+1)
	line := ui.dim(number) + " " + ui.verificationBadge(step.Status) + " " + strings.TrimSpace(step.Label)
	if step.Status == VerificationFailed && strings.TrimSpace(step.FailureKind) != "" {
		line += " " + ui.warn("["+strings.TrimSpace(step.FailureKind)+"]")
	}
	return line
}

func (ui UI) activityBadge(kind string) string {
	normalized := strings.ToLower(strings.TrimSpace(kind))
	if normalized == "" {
		normalized = "info"
	}
	label := "[" + padDisplayRight(normalized, 7) + "]"
	switch normalized {
	case "tool":
		return ui.bold(ui.accent2(label))
	case "shell":
		return ui.bold(ui.accent2(label))
	case "edit":
		return ui.bold(ui.success(label))
	case "verify":
		return ui.bold(ui.warn(label))
	case "model":
		return ui.bold(ui.accent(label))
	case "main":
		return ui.bold(ui.accent2(label))
	case "review":
		return ui.bold(ui.blush(label))
	case "next":
		return ui.bold(ui.info(label))
	case "analysis":
		return ui.bold(ui.blush(label))
	case "memory":
		return ui.bold(ui.info(label))
	case "time":
		return ui.bold(ui.dim(label))
	default:
		return ui.bold(ui.dim(label))
	}
}

var commandHighlightPattern = regexp.MustCompile(`(?m)(^|\s)(/[A-Za-z0-9_-]+|![^\s]+|@[^\s]+)`)
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)
var assistantBulletListPattern = regexp.MustCompile(`^[-*+]\s+`)
var assistantOrderedListPattern = regexp.MustCompile(`^\d+[\.\)]\s+`)

func isRedundantThinkingStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "Thinking ...", "생각 중 ...":
		return true
	default:
		return false
	}
}

func shouldAnimateThinkingStatus(status string) bool {
	trimmed := strings.TrimSpace(status)
	if trimmed == "" {
		return true
	}
	if isRedundantThinkingStatus(trimmed) {
		return true
	}
	return false
}

func (ui UI) highlightCommands(text string) string {
	if !ui.color {
		return text
	}
	return commandHighlightPattern.ReplaceAllStringFunc(text, func(match string) string {
		if len(match) == 0 {
			return match
		}
		prefix := ""
		token := match
		if match[0] == ' ' || match[0] == '\t' || match[0] == '\n' || match[0] == '\r' {
			prefix = match[:1]
			token = match[1:]
		}
		return prefix + ui.bold(ui.accent(token))
	})
}

func (ui UI) formatOllamaModels(models []OllamaModelInfo, current string) string {
	if len(models) == 0 {
		return ui.warnLine("No Ollama models were returned by the server.")
	}
	var lines []string
	for i, model := range models {
		label := fmt.Sprintf("%2d. %s", i+1, model.Name)
		if model.Name == current {
			label += " " + ui.success("[current]")
		}
		meta := []string{}
		if model.Details.ParameterSize != "" {
			meta = append(meta, model.Details.ParameterSize)
		}
		if model.Details.QuantizationLevel != "" {
			meta = append(meta, model.Details.QuantizationLevel)
		}
		if model.Details.Family != "" {
			meta = append(meta, model.Details.Family)
		}
		if len(meta) > 0 {
			label += "  " + ui.dim(strings.Join(meta, " | "))
		}
		lines = append(lines, label)
	}
	return strings.Join(lines, "\n")
}

func (ui UI) formatOpenRouterModels(models []OpenRouterModelInfo, current string, page, pageSize int, reasoningOnly bool, recommendedOnly bool, sortMode string, keyword string) string {
	if len(models) == 0 {
		return ui.warnLine("No OpenRouter models were returned by the server.")
	}
	if page < 0 {
		page = 0
	}
	start := page * pageSize
	if start > len(models) {
		start = 0
	}
	end := start + pageSize
	if end > len(models) {
		end = len(models)
	}

	totalPages := (len(models) + pageSize - 1) / pageSize
	filterLabel := "all models"
	if reasoningOnly {
		filterLabel = "reasoning only"
	}
	if recommendedOnly {
		if filterLabel == "all models" {
			filterLabel = "recommended only"
		} else {
			filterLabel += " + recommended"
		}
	}
	if strings.TrimSpace(keyword) != "" {
		filterLabel += " + keyword=" + keyword
	}
	header := ui.accent2(fmt.Sprintf("Page %d/%d", page+1, totalPages)) +
		"  " + ui.info(fmt.Sprintf("showing %d-%d of %d", start+1, end, len(models))) +
		"  " + ui.warn("filter="+filterLabel) +
		"  " + ui.success("sort="+sortMode) +
		"  " + ui.dim("keys:") +
		" " + ui.bold(ui.accent("n")) + "=next" +
		" " + ui.bold(ui.accent("p")) + "=prev" +
		" " + ui.bold(ui.accent("r")) + "=reasoning" +
		" " + ui.bold(ui.accent("m")) + "=recommended" +
		" " + ui.bold(ui.accent("o")) + "=order" +
		" " + ui.bold(ui.accent("f")) + "=filter"
	lines := []string{
		header,
	}
	for i := start; i < end; i++ {
		model := models[i]
		number := i - start + 1
		label := fmt.Sprintf("%2d. %s", number, model.ID)
		if model.ID == current {
			label += " " + ui.success("[current]")
		}
		meta := []string{}
		if model.Name != "" && model.Name != model.ID {
			meta = append(meta, model.Name)
		}
		if vendor := openRouterVendor(model.ID); vendor != "" {
			meta = append(meta, vendor)
		}
		if model.ContextLength > 0 {
			meta = append(meta, fmt.Sprintf("%dk ctx", model.ContextLength/1000))
		}
		if pricing := openRouterPricingSummary(model); pricing != "" {
			meta = append(meta, pricing)
		}
		if openRouterCuratedPriority(model.ID) > 0 {
			meta = append(meta, "recommended")
		}
		if openRouterSupportsReasoning(model) {
			meta = append(meta, "reasoning")
		}
		if model.Architecture.Modality != "" {
			meta = append(meta, model.Architecture.Modality)
		}
		if len(meta) > 0 {
			label += "  " + ui.dim(strings.Join(meta, " | "))
		}
		lines = append(lines, label)
	}
	return strings.Join(lines, "\n")
}

func openRouterVendor(modelID string) string {
	parts := strings.SplitN(modelID, "/", 2)
	if len(parts) == 2 && parts[0] != "" {
		return parts[0]
	}
	return ""
}

func openRouterSupportsReasoning(model OpenRouterModelInfo) bool {
	for _, item := range model.SupportedParameters {
		lower := strings.ToLower(strings.TrimSpace(item))
		if lower == "reasoning" || lower == "include_reasoning" || lower == "reasoning_effort" {
			return true
		}
	}
	return false
}

func openRouterPricingSummary(model OpenRouterModelInfo) string {
	prompt := pricePerMillion(model.Pricing.Prompt)
	completion := pricePerMillion(model.Pricing.Completion)
	switch {
	case prompt != "" && completion != "":
		return prompt + " / " + completion + " per 1M"
	case prompt != "":
		return prompt + " prompt per 1M"
	case completion != "":
		return completion + " completion per 1M"
	default:
		return ""
	}
}

func pricePerMillion(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return ""
	}
	perMillion := parsed * 1_000_000
	switch {
	case perMillion >= 10:
		return fmt.Sprintf("$%.0f", perMillion)
	case perMillion >= 1:
		return fmt.Sprintf("$%.2f", perMillion)
	default:
		return fmt.Sprintf("$%.3f", perMillion)
	}
}

func padDisplayRight(text string, width int) string {
	padding := width - visibleLen(text)
	if padding <= 0 {
		return text
	}
	return text + strings.Repeat(" ", padding)
}

func truncateDisplayText(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if visibleLen(text) <= limit {
		return text
	}
	if limit <= 3 {
		return truncateDisplayTextWithoutSuffix(text, limit)
	}
	prefix := strings.TrimSpace(truncateDisplayTextWithoutSuffix(text, limit-3))
	if prefix == "" {
		return "..."
	}
	return prefix + "..."
}

func truncateDisplayTextMiddle(text string, limit int) string {
	trimmed := strings.TrimSpace(text)
	if limit <= 0 || trimmed == "" {
		return ""
	}
	if visibleLen(trimmed) <= limit {
		return trimmed
	}
	if limit <= 3 {
		return truncateDisplayText(trimmed, limit)
	}
	tailWidth := (limit - 3) / 2
	headWidth := limit - 3 - tailWidth
	if headWidth <= 0 || tailWidth <= 0 {
		return truncateDisplayText(trimmed, limit)
	}
	head := truncateDisplayTextWithoutSuffix(trimmed, headWidth)
	tail := displayTextTail(trimmed, tailWidth)
	if head == "" || tail == "" {
		return truncateDisplayText(trimmed, limit)
	}
	return head + "..." + tail
}

func displayTextTail(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	width := 0
	start := len(runes)
	for start > 0 {
		next := runeWidth(runes[start-1])
		if width+next > limit {
			break
		}
		start--
		width += next
	}
	return string(runes[start:])
}

func truncateDisplayTextWithoutSuffix(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	var b strings.Builder
	width := 0
	for _, r := range text {
		next := runeWidth(r)
		if width+next > limit {
			break
		}
		b.WriteRune(r)
		width += next
	}
	return b.String()
}

func countBlockLines(text string) int {
	trimmed := normalizeBlockLineEndings(strings.TrimRight(text, "\r\n"))
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "\n") + 1
}

func (ui UI) renderAssistantBody(text string) string {
	if text == "" || !ui.color {
		return text
	}

	var out strings.Builder
	inFence := false
	for _, line := range strings.SplitAfter(text, "\n") {
		if line == "" {
			continue
		}
		hasNewline := strings.HasSuffix(line, "\n")
		content := strings.TrimSuffix(line, "\n")
		kind := classifyAssistantLine(content, inFence)
		out.WriteString(ui.renderAssistantLine(kind, content))
		if hasNewline {
			out.WriteByte('\n')
		}
		if isAssistantFenceLine(content) {
			inFence = !inFence
		}
	}
	return out.String()
}

func (ui UI) renderAssistantStreamDelta(text string, inFence *bool, linePrefix *string) string {
	if text == "" {
		return ""
	}
	if !ui.color {
		return text
	}

	var out strings.Builder
	for _, segment := range strings.SplitAfter(text, "\n") {
		if segment == "" {
			continue
		}

		hasNewline := strings.HasSuffix(segment, "\n")
		content := strings.TrimSuffix(segment, "\n")
		if hasNewline {
			fullLine := *linePrefix + content
			kind := classifyAssistantLine(fullLine, *inFence)
			out.WriteString(ui.renderAssistantLine(kind, content))
			out.WriteByte('\n')
			if isAssistantFenceLine(fullLine) {
				*inFence = !*inFence
			}
			*linePrefix = ""
			continue
		}

		previewLine := *linePrefix + content
		kind := classifyAssistantLine(previewLine, *inFence)
		out.WriteString(ui.renderAssistantLine(kind, content))
		*linePrefix = previewLine
	}
	return out.String()
}

func (ui UI) renderAssistantLine(kind assistantLineKind, text string) string {
	if text == "" {
		return ""
	}
	switch kind {
	case assistantLineFence, assistantLineCode:
		return ui.assistantCode(text)
	default:
		return ui.mint(text)
	}
}

type assistantLineKind int

const (
	assistantLineNone assistantLineKind = iota
	assistantLineParagraph
	assistantLineList
	assistantLineHeading
	assistantLineFence
	assistantLineCode
	assistantLineQuote
)

func formatAssistantText(text string) string {
	normalized := sanitizeAssistantFinalText(text)
	if normalized == "" {
		return ""
	}

	lines := strings.Split(normalized, "\n")
	var out []string
	prevKind := assistantLineNone
	prevBlank := false
	inFence := false

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		if strings.TrimSpace(line) == "" {
			if len(out) == 0 || prevBlank {
				continue
			}
			out = append(out, "")
			prevBlank = true
			continue
		}

		kind := classifyAssistantLine(line, inFence)
		if shouldInsertAssistantSpacer(prevKind, kind, prevBlank) {
			out = append(out, "")
		}
		out = append(out, line)
		prevBlank = false
		prevKind = kind
		if isAssistantFenceLine(line) {
			inFence = !inFence
		}
	}

	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

func sanitizeAssistantFinalText(text string) string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	normalized = stripHiddenAssistantMarkup(normalized)
	if strings.TrimSpace(normalized) == "" {
		return ""
	}
	normalized = collapseRepeatedAssistantSentences(normalized)
	normalized = normalizeAssistantSentenceSpacing(normalized)
	return trimOuterBlankLines(normalized)
}

func collapseRepeatedAssistantSentences(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	inFence := false
	for i, line := range lines {
		if isAssistantFenceLine(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lines[i] = collapseRepeatedAssistantSentenceRuns(line)
	}
	return strings.Join(lines, "\n")
}

func normalizeAssistantSentenceSpacing(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	inFence := false
	for i, line := range lines {
		if isAssistantFenceLine(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lines[i] = normalizeAssistantSentenceSpacingLine(line)
	}
	return strings.Join(lines, "\n")
}

func normalizeAssistantSentenceSpacingLine(line string) string {
	if line == "" {
		return line
	}
	var out strings.Builder
	var prev rune
	for _, r := range line {
		if out.Len() > 0 && isAssistantSentenceTerminator(prev) && !unicode.IsSpace(r) && isCJKOrHangulRune(r) {
			out.WriteByte(' ')
		}
		out.WriteRune(r)
		prev = r
	}
	return out.String()
}

func isCJKOrHangulRune(r rune) bool {
	return (r >= 0x1100 && r <= 0x11FF) ||
		(r >= 0x3130 && r <= 0x318F) ||
		(r >= 0xAC00 && r <= 0xD7AF) ||
		(r >= 0x2E80 && r <= 0x9FFF)
}

func collapseRepeatedAssistantSentenceRuns(line string) string {
	if strings.TrimSpace(line) == "" {
		return line
	}
	var out strings.Builder
	rest := line
	for rest != "" {
		candidate, count, consumed := repeatedAssistantSentencePrefix(rest)
		if count >= 3 && consumed > 0 {
			out.WriteString(candidate)
			rest = rest[consumed:]
			continue
		}
		r, size := utf8.DecodeRuneInString(rest)
		if r == utf8.RuneError && size == 0 {
			break
		}
		out.WriteString(rest[:size])
		rest = rest[size:]
	}
	return out.String()
}

func repeatedAssistantSentencePrefix(text string) (string, int, int) {
	for _, end := range assistantSentenceEndIndexes(text) {
		candidate := text[:end]
		if !isRepeatedAssistantSentenceCandidate(candidate) {
			continue
		}
		count, consumed := countRepeatedAssistantSentence(text, candidate)
		if count >= 3 {
			return candidate, count, consumed
		}
	}
	return "", 0, 0
}

func assistantSentenceEndIndexes(text string) []int {
	var out []int
	for idx, r := range text {
		if isAssistantSentenceTerminator(r) {
			out = append(out, idx+utf8.RuneLen(r))
		}
	}
	return out
}

func isAssistantSentenceTerminator(r rune) bool {
	switch r {
	case '.', '!', '?', 0x3002, 0xFF01, 0xFF1F:
		return true
	default:
		return false
	}
}

func isRepeatedAssistantSentenceCandidate(sentence string) bool {
	trimmed := strings.TrimSpace(sentence)
	if utf8.RuneCountInString(trimmed) < 20 {
		return false
	}
	return strings.IndexFunc(trimmed, unicode.IsLetter) >= 0
}

func countRepeatedAssistantSentence(text string, sentence string) (int, int) {
	count := 0
	pos := 0
	for pos < len(text) {
		start := pos
		for start < len(text) {
			r, size := utf8.DecodeRuneInString(text[start:])
			if !unicode.IsSpace(r) {
				break
			}
			start += size
		}
		if !strings.HasPrefix(text[start:], sentence) {
			break
		}
		count++
		pos = start + len(sentence)
	}
	if count == 0 {
		return 0, 0
	}
	return count, pos
}

func trimOuterBlankLines(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if start >= end {
		return ""
	}
	return strings.Join(lines[start:end], "\n")
}

func classifyAssistantLine(line string, inFence bool) assistantLineKind {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return assistantLineNone
	}
	if isAssistantFenceLine(trimmed) {
		return assistantLineFence
	}
	if inFence {
		return assistantLineCode
	}
	if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
		return assistantLineCode
	}
	if assistantBulletListPattern.MatchString(trimmed) || assistantOrderedListPattern.MatchString(trimmed) {
		return assistantLineList
	}
	if strings.HasPrefix(trimmed, "#") {
		return assistantLineHeading
	}
	if strings.HasPrefix(trimmed, ">") {
		return assistantLineQuote
	}
	return assistantLineParagraph
}

func shouldInsertAssistantSpacer(previous, current assistantLineKind, previousBlank bool) bool {
	if previous == assistantLineNone || previousBlank {
		return false
	}
	switch current {
	case assistantLineList:
		return previous == assistantLineParagraph || previous == assistantLineHeading || previous == assistantLineQuote
	case assistantLineHeading:
		return previous != assistantLineHeading
	case assistantLineFence:
		return previous != assistantLineFence
	case assistantLineParagraph, assistantLineQuote:
		return previous == assistantLineHeading || previous == assistantLineFence
	default:
		return false
	}
}

func isAssistantFenceLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func visibleLen(text string) int {
	clean := ansiPattern.ReplaceAllString(text, "")
	width := 0
	for _, r := range clean {
		width += runeWidth(r)
	}
	return width
}

// runeWidth returns the display width of a rune in a terminal.
// CJK and other wide characters occupy 2 columns; most others occupy 1.
func runeWidth(r rune) int {
	if r < 32 {
		return 0
	}
	// CJK Unified Ideographs and extensions
	if r >= 0x1100 && r <= 0x115F { // Hangul Jamo
		return 2
	}
	if r >= 0x2E80 && r <= 0x9FFF { // CJK Radicals .. CJK Unified Ideographs
		return 2
	}
	if r >= 0xAC00 && r <= 0xD7AF { // Hangul Syllables
		return 2
	}
	if r >= 0xF900 && r <= 0xFAFF { // CJK Compatibility Ideographs
		return 2
	}
	if r >= 0xFE10 && r <= 0xFE6F { // CJK Compatibility Forms .. Small Form Variants
		return 2
	}
	if r >= 0xFF01 && r <= 0xFF60 { // Fullwidth Forms
		return 2
	}
	if r >= 0xFFE0 && r <= 0xFFE6 { // Fullwidth Sign
		return 2
	}
	if r >= 0x20000 && r <= 0x2FFFF { // CJK Ext B..F, Supplementary
		return 2
	}
	if r >= 0x30000 && r <= 0x3FFFF { // CJK Ext G..
		return 2
	}
	return 1
}

func (ui UI) formatCompletionSuggestions(suggestions []string, typed string) string {
	if len(suggestions) == 0 {
		return ""
	}

	category := completionCategory(suggestions)
	prefix := completionMatchPrefix(typed, category)
	header := ui.completionHeader(category, len(suggestions))

	if category == "command" {
		return ui.formatCommandCompletionSuggestions(suggestions, prefix, header)
	}

	termW := terminalWidth()
	if termW < 40 {
		termW = 80
	}

	maxRaw := 0
	for _, s := range suggestions {
		if l := len([]rune(s)); l > maxRaw {
			maxRaw = l
		}
	}

	colW := maxRaw + 3
	if colW < 10 {
		colW = 10
	}
	cols := termW / colW
	if cols < 1 {
		cols = 1
	}

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteByte('\n')

	for i, s := range suggestions {
		if i > 0 && i%cols == 0 {
			sb.WriteByte('\n')
		}

		painted := ui.paintCompletionItem(s, prefix, category)
		padLen := colW - len([]rune(s))
		if padLen < 1 {
			padLen = 1
		}
		sb.WriteString(painted)
		if (i+1)%cols != 0 && i < len(suggestions)-1 {
			sb.WriteString(strings.Repeat(" ", padLen))
		}
	}
	return sb.String()
}

func (ui UI) formatCommandCompletionSuggestions(suggestions []string, prefix string, header string) string {
	commandWidth := 0
	for _, suggestion := range suggestions {
		if width := visibleLen(suggestion); width > commandWidth {
			commandWidth = width
		}
	}
	if commandWidth < 12 {
		commandWidth = 12
	}

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteByte('\n')
	for i, suggestion := range suggestions {
		if i > 0 {
			sb.WriteByte('\n')
		}
		command := padDisplayRight(ui.paintCompletionItem(suggestion, prefix, "command"), commandWidth)
		description := strings.TrimSpace(commandCompletionDescription(suggestion))
		sb.WriteString("  ")
		sb.WriteString(command)
		if description != "" {
			sb.WriteString("  ")
			sb.WriteString(ui.dim(description))
		}
	}
	return sb.String()
}

func (ui UI) completionHeader(cat string, count int) string {
	var icon, label string
	switch cat {
	case "command":
		icon = "/"
		label = "Commands"
	case "file":
		icon = "@"
		label = "Files"
	case "mcp":
		icon = "~"
		label = "MCP"
	default:
		icon = ">"
		label = "Suggestions"
	}
	countStr := fmt.Sprintf("(%d)", count)
	return ui.dim("  "+icon+" ") + ui.bold(ui.accent(label)) + " " + ui.dim(countStr)
}

func (ui UI) paintCompletionItem(item, prefix, cat string) string {
	lower := strings.ToLower(item)
	lowerPfx := strings.ToLower(prefix)

	matchEnd := 0
	if lowerPfx != "" && strings.HasPrefix(lower, lowerPfx) {
		matchEnd = len([]rune(lowerPfx))
	}

	runes := []rune(item)
	matched := string(runes[:matchEnd])
	rest := string(runes[matchEnd:])

	isDir := strings.HasSuffix(item, "/")

	switch cat {
	case "command":
		return ui.bold(ui.accent(matched)) + ui.info(rest)
	case "file":
		if isDir {
			return ui.bold(ui.accent2(matched)) + ui.accent2(rest)
		}
		return ui.bold(ui.accent(matched)) + ui.cloud(rest)
	case "mcp":
		return ui.bold(ui.accent(matched)) + ui.blush(rest)
	default:
		return ui.bold(ui.accent(matched)) + ui.dim(rest)
	}
}

func completionCategory(suggestions []string) string {
	if len(suggestions) == 0 {
		return ""
	}
	first := suggestions[0]
	switch {
	case strings.HasPrefix(first, "/"):
		return "command"
	case strings.HasPrefix(first, "@mcp:") || strings.HasPrefix(first, "mcp:"):
		return "mcp"
	case strings.HasPrefix(first, "@") || strings.HasPrefix(first, "/open "):
		return "file"
	default:
		return ""
	}
}

func completionMatchPrefix(typed, cat string) string {
	typed = strings.TrimSpace(typed)
	switch cat {
	case "command":
		if strings.HasPrefix(typed, "/") {
			return typed
		}
	case "file":
		if strings.HasPrefix(typed, "/open ") {
			return "/open " + strings.TrimPrefix(typed, "/open ")
		}
		idx := strings.LastIndex(typed, "@")
		if idx >= 0 {
			return typed[idx:]
		}
	case "mcp":
		idx := strings.LastIndex(typed, "@")
		if idx >= 0 {
			return typed[idx:]
		}
		if strings.HasPrefix(typed, "/resource ") || strings.HasPrefix(typed, "/prompt ") {
			parts := strings.SplitN(typed, " ", 2)
			if len(parts) == 2 {
				return parts[0] + " " + parts[1]
			}
		}
	}
	return typed
}
