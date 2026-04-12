package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type UI struct {
	color bool
}

func NewUI() UI {
	return UI{color: colorsEnabled()}
}

func colorsEnabled() bool {
	if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" {
		return false
	}
	term := strings.TrimSpace(os.Getenv("TERM"))
	if strings.EqualFold(term, "dumb") {
		return false
	}
	return true
}

func (ui UI) paint(code, text string) string {
	if !ui.color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (ui UI) bold(text string) string    { return ui.paint("1", text) }
func (ui UI) dim(text string) string     { return ui.paint("38;5;245", text) }
func (ui UI) accent(text string) string  { return ui.paint("38;5;81", text) }
func (ui UI) accent2(text string) string { return ui.paint("38;5;214", text) }
func (ui UI) success(text string) string { return ui.paint("38;5;42", text) }
func (ui UI) warn(text string) string    { return ui.paint("38;5;220", text) }
func (ui UI) error(text string) string   { return ui.paint("38;5;203", text) }
func (ui UI) info(text string) string    { return ui.paint("38;5;117", text) }
func (ui UI) cloud(text string) string   { return ui.paint("38;5;255", text) }
func (ui UI) blush(text string) string   { return ui.paint("38;5;218", text) }
func (ui UI) mint(text string) string    { return ui.paint("38;5;121", text) }

func (ui UI) clearScreen() string {
	return "\x1b[2J\x1b[H"
}

func (ui UI) banner(provider, model, sessionID, cwd string) string {
	if strings.TrimSpace(provider) == "" {
		provider = "(unset)"
	}
	if strings.TrimSpace(model) == "" {
		model = "(unset)"
	}
	titleArt := []string{
		ui.accent(" _  __  _____  ____   _   _  _____   ___   ____    ____  _____"),
		ui.accent("| |/ / | ____||  _ \\ | \\ | ||  ___| / _ \\ |  _ \\  / ___|| ____|"),
		ui.info("| ' /  |  _|  | |_) ||  \\| || |_   | | | || |_) || |  _ |  _|  "),
		ui.info("| . \\  | |___ |  _ < | |\\  ||  _|  | |_| ||  _ < | |_| || |___ "),
		ui.accent2("|_|\\_\\ |_____||_| \\_\\|_| \\_||_|     \\___/ |_| \\_\\\\____||_____|"),
	}

	title := ui.bold(ui.accent("Kernforge")) + "  " + ui.dim("forge-ready terminal coding agent")
	tagline := ui.info("Sharpen ideas, shape patches, and keep momentum in the shell.")
	versionLine := ui.accent("version") + "=" + ui.info(currentVersion())
	meta1 := ui.accent("provider") + "=" + ui.info(provider) + "  " + ui.accent("model") + "=" + ui.info(model)
	meta2 := ui.accent("session") + "=" + ui.dim(sessionID)
	meta3 := ui.accent("workspace") + "=" + ui.dim(cwd)
	divider := ui.dim(strings.Repeat("=", 72))

	lines := []string{divider}
	lines = append(lines, titleArt...)
	lines = append(lines, "", title, tagline, versionLine, meta1, meta2, meta3, divider)
	return strings.Join(lines, "\n")
}

func (ui UI) prompt(provider, model string) string {
	return ui.bold(ui.accent("kernforge")) + ui.dim("("+provider+":"+model+")") + ui.accent(" > ")
}

func (ui UI) continuationPrompt() string {
	return ui.dim("... ")
}

func (ui UI) assistant(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 1 {
		return ui.bold(ui.info("assistant")) + ui.dim(": ") + ui.mint(lines[0])
	}
	var out []string
	out = append(out, ui.bold(ui.info("assistant"))+ui.dim(": ")+ui.mint(lines[0]))
	for _, line := range lines[1:] {
		out = append(out, ui.mint("           "+line))
	}
	return strings.Join(out, "\n")
}

func (ui UI) shell(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return ui.bold(ui.accent2("shell")) + "\n" + text
}

func (ui UI) statusKV(key, value string) string {
	return ui.bold(ui.accent(key+":")) + " " + value
}

func (ui UI) section(title string) string {
	return ui.bold(ui.accent2(title))
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

func (ui UI) thinkingLine(frame string, elapsed time.Duration, status string) string {
	seconds := int(elapsed.Seconds())
	if strings.TrimSpace(status) == "" {
		status = "Sending prompt to model..."
		if seconds >= 15 {
			status = "Still waiting for the model response..."
		}
	}
	line := ui.bold(ui.accent("thinking")) + " " + ui.dim("["+frame+"]")
	if !isRedundantThinkingStatus(status) {
		line += " " + ui.info(status)
	}
	line += " " + ui.dim(fmt.Sprintf("(%ds, Esc to cancel)", seconds))
	return line
}

func (ui UI) hintLine(text string) string {
	return ui.accent2("TIP") + "  " + ui.info(text)
}

var commandHighlightPattern = regexp.MustCompile(`(?m)(^|\s)(/[A-Za-z0-9_-]+|![^\s]+|@[^\s]+)`)
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

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

	header := ui.completionHeader(category, len(suggestions))

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
