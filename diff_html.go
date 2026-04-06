package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type unifiedDiffFile struct {
	ID      string
	Lang    string
	OldPath string
	NewPath string
	Hunks   []unifiedDiffHunk
	Adds    int
	Removes int
}

type unifiedDiffHunk struct {
	Header string
	Lines  []unifiedDiffLine
}

type unifiedDiffLine struct {
	Kind  string
	OldNo int
	NewNo int
	Text  string
}

var unifiedDiffHeaderPattern = regexp.MustCompile(`^diff --git a/(.+) b/(.+)$`)

func parseUnifiedDiff(diff string) []unifiedDiffFile {
	lines := strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n")
	files := []unifiedDiffFile{}
	var currentFile *unifiedDiffFile
	var currentHunk *unifiedDiffHunk
	oldLineNo := 0
	newLineNo := 0

	flushHunk := func() {
		if currentFile == nil || currentHunk == nil {
			return
		}
		currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
		currentHunk = nil
	}
	flushFile := func() {
		if currentFile == nil {
			return
		}
		flushHunk()
		if currentFile.OldPath != "" || currentFile.NewPath != "" || len(currentFile.Hunks) > 0 {
			files = append(files, *currentFile)
		}
		currentFile = nil
	}

	for _, raw := range lines {
		if match := unifiedDiffHeaderPattern.FindStringSubmatch(raw); len(match) == 3 {
			flushFile()
			currentFile = &unifiedDiffFile{
				ID:      buildDiffAnchorID(strings.TrimSpace(match[2])),
				Lang:    detectDiffLanguage(strings.TrimSpace(match[2])),
				OldPath: strings.TrimSpace(match[1]),
				NewPath: strings.TrimSpace(match[2]),
			}
			oldLineNo = 0
			newLineNo = 0
			continue
		}
		if currentFile == nil {
			continue
		}
		switch {
		case strings.HasPrefix(raw, "--- "):
			path := strings.TrimSpace(strings.TrimPrefix(raw, "--- "))
			path = strings.TrimPrefix(path, "a/")
			if path != "/dev/null" && path != "" {
				currentFile.OldPath = path
				if strings.TrimSpace(currentFile.Lang) == "" {
					currentFile.Lang = detectDiffLanguage(path)
				}
			}
		case strings.HasPrefix(raw, "+++ "):
			path := strings.TrimSpace(strings.TrimPrefix(raw, "+++ "))
			path = strings.TrimPrefix(path, "b/")
			if path != "/dev/null" && path != "" {
				currentFile.NewPath = path
				currentFile.Lang = detectDiffLanguage(path)
			}
		case strings.HasPrefix(raw, "@@ "):
			flushHunk()
			currentHunk = &unifiedDiffHunk{Header: raw}
			oldLineNo, newLineNo = parseUnifiedDiffHunkHeader(raw)
		default:
			if currentHunk == nil {
				continue
			}
			line := unifiedDiffLine{
				Kind: "meta",
				Text: raw,
			}
			switch {
			case strings.HasPrefix(raw, " "):
				line.Kind = "context"
				line.OldNo = oldLineNo
				line.NewNo = newLineNo
				line.Text = strings.TrimPrefix(raw, " ")
				oldLineNo++
				newLineNo++
			case strings.HasPrefix(raw, "+") && !strings.HasPrefix(raw, "+++"):
				line.Kind = "add"
				line.NewNo = newLineNo
				line.Text = strings.TrimPrefix(raw, "+")
				newLineNo++
				currentFile.Adds++
			case strings.HasPrefix(raw, "-") && !strings.HasPrefix(raw, "---"):
				line.Kind = "remove"
				line.OldNo = oldLineNo
				line.Text = strings.TrimPrefix(raw, "-")
				oldLineNo++
				currentFile.Removes++
			case strings.HasPrefix(raw, `\ No newline at end of file`):
				line.Kind = "note"
			}
			currentHunk.Lines = append(currentHunk.Lines, line)
		}
	}
	flushFile()
	return files
}

func parseUnifiedDiffHunkHeader(header string) (int, int) {
	match := unifiedHunkPattern.FindStringSubmatch(header)
	if len(match) != 5 {
		return 0, 0
	}
	oldStart, _ := strconv.Atoi(match[1])
	newStart, _ := strconv.Atoi(match[3])
	return oldStart, newStart
}

func renderUnifiedDiffHTML(diff string) (string, diffPreviewMetrics, bool) {
	files := parseUnifiedDiff(diff)
	if len(files) == 0 {
		return "", diffPreviewMetrics{}, false
	}

	metrics := diffPreviewMetrics{}
	var cards []string
	for _, file := range files {
		metrics.Added += file.Adds
		metrics.Removed += file.Removes
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind == "context" {
					metrics.Context++
				}
			}
		}
		cards = append(cards, renderUnifiedDiffFileCard(file))
	}
	return strings.Join(cards, ""), metrics, true
}

func renderUnifiedDiffModesHTML(diff string) (string, diffPreviewMetrics, bool) {
	files := parseUnifiedDiff(diff)
	if len(files) == 0 {
		return "", diffPreviewMetrics{}, false
	}
	unified, metrics, ok := renderUnifiedDiffHTML(diff)
	if !ok {
		return "", diffPreviewMetrics{}, false
	}
	var splitCards []string
	for _, file := range files {
		splitCards = append(splitCards, renderSplitDiffFileCard(file))
	}
	sidebar := renderDiffFileSidebar(files)
	return fmt.Sprintf(
		`<div class="diff-layout"><aside class="diff-sidebar"><div class="diff-mode-switch" role="tablist" aria-label="Diff view mode"><button type="button" class="mode-button is-active" data-mode="unified">Unified</button><button type="button" class="mode-button" data-mode="split">Split</button></div><nav class="file-nav" aria-label="Changed files">%s</nav></aside><div class="diff-main"><div class="mode-panel" data-panel="unified">%s</div><div class="mode-panel is-hidden" data-panel="split">%s</div></div></div>`,
		sidebar,
		unified,
		strings.Join(splitCards, ""),
	), metrics, true
}

func renderUnifiedDiffFileCard(file unifiedDiffFile) string {
	title := file.NewPath
	if strings.TrimSpace(title) == "" {
		title = file.OldPath
	}
	if strings.TrimSpace(title) == "" {
		title = "(unknown file)"
	}
	meta := fmt.Sprintf("+%d -%d", file.Adds, file.Removes)
	var hunks []string
	for _, hunk := range file.Hunks {
		hunks = append(hunks, renderUnifiedDiffHunk(hunk, file.Lang))
	}
	return fmt.Sprintf(
		`<section class="file-card" id="%s"><div class="file-header"><div class="file-path">%s</div><div class="file-stats"><span class="stat-add">+%d</span><span class="stat-remove">-%d</span><span class="stat-total">%s</span></div></div><div class="file-body">%s</div></section>`,
		htmlEscape(file.ID),
		htmlEscape(title),
		file.Adds,
		file.Removes,
		htmlEscape(meta),
		joinOrFallback(hunks, `<div class="empty-state">No hunks in this file.</div>`),
	)
}

func renderUnifiedDiffHunk(hunk unifiedDiffHunk, lang string) string {
	var rows []string
	rows = append(rows, fmt.Sprintf(`<div class="gh-hunk-header">%s</div>`, htmlEscape(hunk.Header)))
	for _, pair := range buildSplitDiffRows(hunk.Lines) {
		switch {
		case pair[0] != nil && pair[1] != nil && pair[0].Kind == "remove" && pair[1].Kind == "add":
			leftHTML, rightHTML := highlightDiffPair(pair[0].Text, pair[1].Text, lang)
			rows = append(rows, renderUnifiedDiffRowHTML(*pair[0], leftHTML))
			rows = append(rows, renderUnifiedDiffRowHTML(*pair[1], rightHTML))
		case pair[0] != nil && pair[1] != nil && pair[0].Kind == "context" && pair[1].Kind == "context":
			rows = append(rows, renderUnifiedDiffRow(*pair[0]))
		case pair[0] != nil:
			rows = append(rows, renderUnifiedDiffRow(*pair[0]))
		case pair[1] != nil:
			rows = append(rows, renderUnifiedDiffRow(*pair[1]))
		}
	}
	return `<div class="gh-hunk">` + strings.Join(rows, "") + `</div>`
}

func renderSplitDiffFileCard(file unifiedDiffFile) string {
	title := file.NewPath
	if strings.TrimSpace(title) == "" {
		title = file.OldPath
	}
	if strings.TrimSpace(title) == "" {
		title = "(unknown file)"
	}
	var hunks []string
	for _, hunk := range file.Hunks {
		hunks = append(hunks, renderSplitDiffHunk(hunk, file.Lang))
	}
	return fmt.Sprintf(
		`<section class="file-card split-file-card" id="%s-split"><div class="file-header"><div class="file-path">%s</div><div class="file-stats"><span class="stat-add">+%d</span><span class="stat-remove">-%d</span></div></div><div class="file-body">%s</div></section>`,
		htmlEscape(file.ID),
		htmlEscape(title),
		file.Adds,
		file.Removes,
		joinOrFallback(hunks, `<div class="empty-state">No hunks in this file.</div>`),
	)
}

func renderSplitDiffHunk(hunk unifiedDiffHunk, lang string) string {
	var rows []string
	rows = append(rows, fmt.Sprintf(`<div class="gh-hunk-header">%s</div>`, htmlEscape(hunk.Header)))
	for _, pair := range buildSplitDiffRows(hunk.Lines) {
		rows = append(rows, renderSplitDiffRow(pair[0], pair[1], lang))
	}
	return `<div class="gh-hunk split-hunk">` + strings.Join(rows, "") + `</div>`
}

func buildSplitDiffRows(lines []unifiedDiffLine) [][2]*unifiedDiffLine {
	rows := [][2]*unifiedDiffLine{}
	for i := 0; i < len(lines); {
		line := lines[i]
		switch line.Kind {
		case "remove":
			removes := []unifiedDiffLine{}
			for i < len(lines) && lines[i].Kind == "remove" {
				removes = append(removes, lines[i])
				i++
			}
			adds := []unifiedDiffLine{}
			for i < len(lines) && lines[i].Kind == "add" {
				adds = append(adds, lines[i])
				i++
			}
			maxLen := len(removes)
			if len(adds) > maxLen {
				maxLen = len(adds)
			}
			for idx := 0; idx < maxLen; idx++ {
				var left *unifiedDiffLine
				var right *unifiedDiffLine
				if idx < len(removes) {
					item := removes[idx]
					left = &item
				}
				if idx < len(adds) {
					item := adds[idx]
					right = &item
				}
				rows = append(rows, [2]*unifiedDiffLine{left, right})
			}
		case "add":
			item := line
			rows = append(rows, [2]*unifiedDiffLine{nil, &item})
			i++
		default:
			item := line
			rows = append(rows, [2]*unifiedDiffLine{&item, &item})
			i++
		}
	}
	return rows
}

func renderSplitDiffRow(left *unifiedDiffLine, right *unifiedDiffLine, lang string) string {
	if left != nil && right != nil && left.Kind == "remove" && right.Kind == "add" {
		leftHTML, rightHTML := highlightDiffPair(left.Text, right.Text, lang)
		return fmt.Sprintf(
			`<div class="split-row">%s%s</div>`,
			renderSplitDiffCellHTML(left, "left", leftHTML),
			renderSplitDiffCellHTML(right, "right", rightHTML),
		)
	}
	return fmt.Sprintf(
		`<div class="split-row">%s%s</div>`,
		renderSplitDiffCell(left, "left", lang),
		renderSplitDiffCell(right, "right", lang),
	)
}

func renderSplitDiffCell(line *unifiedDiffLine, side string, lang string) string {
	if line == nil {
		return `<div class="split-cell split-empty"></div>`
	}
	return renderSplitDiffCellHTML(line, side, renderSyntaxHighlightedHTML(line.Text, lang))
}

func renderSplitDiffCellHTML(line *unifiedDiffLine, side string, codeHTML string) string {
	if line == nil {
		return `<div class="split-cell split-empty"></div>`
	}
	className := "split-cell split-" + line.Kind + " split-" + side
	oldNo := ""
	newNo := ""
	prefix := " "
	switch line.Kind {
	case "add":
		prefix = "+"
	case "remove":
		prefix = "-"
	case "note":
		prefix = "\\"
	}
	if line.OldNo > 0 {
		oldNo = strconv.Itoa(line.OldNo)
	}
	if line.NewNo > 0 {
		newNo = strconv.Itoa(line.NewNo)
	}
	return fmt.Sprintf(
		`<div class="%s"><div class="gh-no">%s</div><div class="gh-no">%s</div><div class="gh-prefix">%s</div><div class="gh-code">%s</div></div>`,
		className,
		htmlEscape(oldNo),
		htmlEscape(newNo),
		htmlEscape(prefix),
		codeHTML,
	)
}

func renderUnifiedDiffRow(line unifiedDiffLine) string {
	return renderUnifiedDiffRowHTML(line, renderSyntaxHighlightedHTML(line.Text, ""))
}

func renderUnifiedDiffRowHTML(line unifiedDiffLine, codeHTML string) string {
	className := "gh-line gh-" + line.Kind
	prefix := " "
	switch line.Kind {
	case "add":
		prefix = "+"
	case "remove":
		prefix = "-"
	case "note":
		prefix = "\\"
	}
	oldNo := ""
	newNo := ""
	if line.OldNo > 0 {
		oldNo = strconv.Itoa(line.OldNo)
	}
	if line.NewNo > 0 {
		newNo = strconv.Itoa(line.NewNo)
	}
	return fmt.Sprintf(
		`<div class="%s"><div class="gh-no">%s</div><div class="gh-no">%s</div><div class="gh-prefix">%s</div><div class="gh-code">%s</div></div>`,
		className,
		htmlEscape(oldNo),
		htmlEscape(newNo),
		htmlEscape(prefix),
		codeHTML,
	)
}

func highlightDiffPair(oldText string, newText string, lang string) (string, string) {
	if oldText == newText {
		return renderSyntaxHighlightedHTML(oldText, lang), renderSyntaxHighlightedHTML(newText, lang)
	}

	oldRunes := []rune(oldText)
	newRunes := []rune(newText)
	prefix := 0
	for prefix < len(oldRunes) && prefix < len(newRunes) && oldRunes[prefix] == newRunes[prefix] {
		prefix++
	}

	oldSuffix := len(oldRunes)
	newSuffix := len(newRunes)
	for oldSuffix > prefix && newSuffix > prefix && oldRunes[oldSuffix-1] == newRunes[newSuffix-1] {
		oldSuffix--
		newSuffix--
	}

	oldHead := string(oldRunes[:prefix])
	oldMid := string(oldRunes[prefix:oldSuffix])
	oldTail := string(oldRunes[oldSuffix:])
	newHead := string(newRunes[:prefix])
	newMid := string(newRunes[prefix:newSuffix])
	newTail := string(newRunes[newSuffix:])

	if oldMid == "" {
		oldMid = " "
	}
	if newMid == "" {
		newMid = " "
	}

	return renderSyntaxHighlightedHTML(oldHead, lang) + `<span class="word-remove">` + renderSyntaxHighlightedHTML(oldMid, lang) + `</span>` + renderSyntaxHighlightedHTML(oldTail, lang),
		renderSyntaxHighlightedHTML(newHead, lang) + `<span class="word-add">` + renderSyntaxHighlightedHTML(newMid, lang) + `</span>` + renderSyntaxHighlightedHTML(newTail, lang)
}

func detectDiffLanguage(path string) string {
	path = strings.ToLower(strings.TrimSpace(path))
	switch {
	case strings.HasSuffix(path, ".go"):
		return "go"
	case strings.HasSuffix(path, ".cpp"), strings.HasSuffix(path, ".cc"), strings.HasSuffix(path, ".cxx"), strings.HasSuffix(path, ".c"), strings.HasSuffix(path, ".h"), strings.HasSuffix(path, ".hpp"), strings.HasSuffix(path, ".hh"):
		return "cpp"
	case strings.HasSuffix(path, ".ts"), strings.HasSuffix(path, ".tsx"), strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".jsx"):
		return "js"
	case strings.HasSuffix(path, ".json"):
		return "json"
	case strings.HasSuffix(path, ".ps1"):
		return "ps1"
	default:
		return ""
	}
}

func renderSyntaxHighlightedHTML(text string, lang string) string {
	if text == "" {
		return ""
	}
	var keywords map[string]struct{}
	switch lang {
	case "go":
		keywords = keywordSet("package", "import", "func", "type", "struct", "interface", "map", "chan", "select", "case", "switch", "fallthrough", "go", "defer", "range", "for", "if", "else", "return", "var", "const", "nil", "true", "false")
	case "cpp":
		keywords = keywordSet("class", "struct", "namespace", "template", "typename", "auto", "const", "constexpr", "volatile", "virtual", "override", "final", "public", "private", "protected", "if", "else", "switch", "case", "for", "while", "return", "nullptr", "true", "false", "using", "typedef", "static", "inline", "void", "int", "bool")
	case "js":
		keywords = keywordSet("function", "const", "let", "var", "if", "else", "switch", "case", "for", "while", "return", "class", "new", "import", "export", "async", "await", "true", "false", "null", "undefined")
	case "json":
		keywords = map[string]struct{}{}
	case "ps1":
		keywords = keywordSet("function", "param", "if", "else", "switch", "foreach", "return", "$true", "$false", "$null")
	default:
		return htmlEscape(text)
	}

	var b strings.Builder
	for i := 0; i < len(text); {
		switch {
		case i+1 < len(text) && text[i] == '/' && text[i+1] == '/':
			b.WriteString(`<span class="tok-comment">` + htmlEscape(text[i:]) + `</span>`)
			return b.String()
		case text[i] == '"':
			j := i + 1
			for j < len(text) {
				if text[j] == '\\' && j+1 < len(text) {
					j += 2
					continue
				}
				if text[j] == '"' {
					j++
					break
				}
				j++
			}
			b.WriteString(`<span class="tok-string">` + htmlEscape(text[i:j]) + `</span>`)
			i = j
		case isDigitByte(text[i]):
			j := i + 1
			for j < len(text) && (isDigitByte(text[j]) || text[j] == '.' || text[j] == 'x' || text[j] == 'X' || (text[j] >= 'a' && text[j] <= 'f') || (text[j] >= 'A' && text[j] <= 'F')) {
				j++
			}
			b.WriteString(`<span class="tok-number">` + htmlEscape(text[i:j]) + `</span>`)
			i = j
		case isIdentStart(text[i]):
			j := i + 1
			for j < len(text) && isIdentPart(text[j]) {
				j++
			}
			word := text[i:j]
			if _, ok := keywords[word]; ok {
				b.WriteString(`<span class="tok-keyword">` + htmlEscape(word) + `</span>`)
			} else {
				b.WriteString(htmlEscape(word))
			}
			i = j
		default:
			b.WriteString(htmlEscape(string(text[i])))
			i++
		}
	}
	return b.String()
}

func keywordSet(items ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		out[item] = struct{}{}
	}
	return out
}

func isDigitByte(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func isIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentPart(ch byte) bool {
	return isIdentStart(ch) || isDigitByte(ch)
}

func renderDiffFileSidebar(files []unifiedDiffFile) string {
	items := make([]string, 0, len(files))
	for _, file := range files {
		path := strings.TrimSpace(file.NewPath)
		if path == "" {
			path = strings.TrimSpace(file.OldPath)
		}
		if path == "" {
			path = "(unknown file)"
		}
		items = append(items, fmt.Sprintf(
			`<a class="file-nav-item" href="#%s" data-anchor="%s"><span class="file-nav-path">%s</span><span class="file-nav-stats"><span class="stat-add">+%d</span><span class="stat-remove">-%d</span></span></a>`,
			htmlEscape(file.ID),
			htmlEscape(file.ID),
			htmlEscape(path),
			file.Adds,
			file.Removes,
		))
	}
	return joinOrFallback(items, `<div class="empty-state">No changed files.</div>`)
}

func buildDiffAnchorID(path string) string {
	path = strings.TrimSpace(strings.ToLower(path))
	if path == "" {
		return "diff-file"
	}
	var b strings.Builder
	b.WriteString("diff-")
	lastDash := false
	for _, r := range path {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "diff-file"
	}
	return out
}
