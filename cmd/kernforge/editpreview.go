package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

func buildEditPreview(path, before, after string) string {
	before = normalizePreviewText(before)
	after = normalizePreviewText(after)
	if before == after {
		return "No textual changes detected."
	}

	oldLines := splitPreviewLines(before)
	newLines := splitPreviewLines(after)

	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}

	oldSuffix := len(oldLines) - 1
	newSuffix := len(newLines) - 1
	for oldSuffix >= prefix && newSuffix >= prefix && oldLines[oldSuffix] == newLines[newSuffix] {
		oldSuffix--
		newSuffix--
	}

	contextStart := prefix - 2
	if contextStart < 0 {
		contextStart = 0
	}
	contextOldEnd := oldSuffix + 2
	if contextOldEnd >= len(oldLines) {
		contextOldEnd = len(oldLines) - 1
	}
	contextNewEnd := newSuffix + 2
	if contextNewEnd >= len(newLines) {
		contextNewEnd = len(newLines) - 1
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Preview for %s", path))
	lines = append(lines, fmt.Sprintf("--- before/%s", path))
	lines = append(lines, fmt.Sprintf("+++ after/%s", path))

	for i := contextStart; i < prefix; i++ {
		lines = append(lines, fmt.Sprintf(" %4d | %s", i+1, oldLines[i]))
	}

	if prefix <= oldSuffix {
		for i := prefix; i <= oldSuffix; i++ {
			lines = append(lines, fmt.Sprintf("-%4d | %s", i+1, oldLines[i]))
		}
	}
	if prefix <= newSuffix {
		for i := prefix; i <= newSuffix; i++ {
			lines = append(lines, fmt.Sprintf("+%4d | %s", i+1, newLines[i]))
		}
	}

	startAfter := newSuffix + 1
	if startAfter < prefix {
		startAfter = prefix
	}
	for i := startAfter; i <= contextNewEnd && i < len(newLines); i++ {
		lines = append(lines, fmt.Sprintf(" %4d | %s", i+1, newLines[i]))
	}

	return strings.Join(lines, "\n")
}

// summarizeProposedEditDiff renders a concise, plain-language summary of a
// proposed change so the pre-write checkpoint shows its SHAPE and SCOPE before
// the raw diff: per-file added/removed line counts plus any new imports, new
// top-level definitions, and removed definitions. It accepts the buildEditPreview
// format ("+%4d | code") and standard unified diffs, and returns "" when there is
// no diff content to summarize. This lets a user see that a small request grew
// into a large unrelated change without reading the whole diff.
func summarizeProposedEditDiff(diff string) string {
	diff = strings.TrimSpace(diff)
	if diff == "" {
		return ""
	}
	type fileStat struct {
		path    string
		added   int
		removed int
	}
	var order []string
	stats := map[string]*fileStat{}
	current := ""
	statFor := func(path string) *fileStat {
		if strings.TrimSpace(path) == "" {
			path = "(file)"
		}
		s, ok := stats[path]
		if !ok {
			s = &fileStat{path: path}
			stats[path] = s
			order = append(order, path)
		}
		return s
	}
	var addedImports, addedDefs, removedDefs []string
	seenImport := map[string]bool{}
	seenAddedDef := map[string]bool{}
	seenRemovedDef := map[string]bool{}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "Preview for "):
			current = strings.TrimSpace(strings.TrimPrefix(line, "Preview for "))
			statFor(current)
			continue
		case strings.HasPrefix(line, "+++ "):
			p := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			p = strings.TrimPrefix(p, "after/")
			p = strings.TrimPrefix(p, "b/")
			if p != "" && p != "/dev/null" {
				current = p
				statFor(current)
			}
			continue
		case strings.HasPrefix(line, "--- "):
			continue
		case line == "":
			continue
		}
		switch line[0] {
		case '+':
			content := previewDiffLineContent(line)
			statFor(current).added++
			if name := diffImportToken(content); name != "" {
				if !seenImport[name] {
					seenImport[name] = true
					addedImports = append(addedImports, name)
				}
			} else if name := diffDefinitionToken(content); name != "" && !seenAddedDef[name] {
				seenAddedDef[name] = true
				addedDefs = append(addedDefs, name)
			}
		case '-':
			content := previewDiffLineContent(line)
			statFor(current).removed++
			if name := diffDefinitionToken(content); name != "" && !seenRemovedDef[name] {
				seenRemovedDef[name] = true
				removedDefs = append(removedDefs, name)
			}
		}
	}
	if len(order) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Change summary (check the scope matches the request before approving):")
	for _, p := range order {
		s := stats[p]
		fmt.Fprintf(&b, "\n- %s: +%d / -%d lines", s.path, s.added, s.removed)
	}
	if len(addedImports) > 0 {
		fmt.Fprintf(&b, "\n- new imports: %s", joinCappedList(addedImports, 12))
	}
	if len(addedDefs) > 0 {
		fmt.Fprintf(&b, "\n- new definitions: %s", joinCappedList(addedDefs, 12))
	}
	if len(removedDefs) > 0 {
		fmt.Fprintf(&b, "\n- removed definitions: %s", joinCappedList(removedDefs, 12))
	}
	return b.String()
}

// previewDiffLineContent strips the diff sign and, for buildEditPreview lines
// ("+%4d | code"), the leading line-number gutter, returning the code content.
func previewDiffLineContent(line string) string {
	if line == "" {
		return ""
	}
	body := line[1:]
	if idx := strings.Index(body, "| "); idx > 0 && idx <= 7 && previewLineNumberPrefix(body[:idx]) {
		return strings.TrimSpace(body[idx+2:])
	}
	return strings.TrimSpace(body)
}

func previewLineNumberPrefix(s string) bool {
	hasDigit := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			hasDigit = true
		case c == ' ':
		default:
			return false
		}
	}
	return hasDigit
}

func diffImportToken(content string) string {
	c := strings.TrimSpace(content)
	lower := strings.ToLower(c)
	switch {
	case strings.HasPrefix(c, "import "):
		rest := strings.Trim(strings.TrimSpace(c[len("import "):]), "\"'`();")
		return firstFieldToken(rest)
	case strings.HasPrefix(lower, "from ") && strings.Contains(lower, " import "):
		return firstFieldToken(strings.TrimSpace(c[len("from "):]))
	case strings.HasPrefix(lower, "#include"):
		return strings.TrimSpace(c[len("#include"):])
	case strings.HasPrefix(c, "using "):
		return strings.TrimRight(strings.TrimSpace(c[len("using "):]), ";")
	case strings.HasPrefix(c, "use "):
		return strings.TrimRight(strings.TrimSpace(c[len("use "):]), ";")
	}
	return ""
}

func diffDefinitionToken(content string) string {
	c := strings.TrimSpace(content)
	for _, kw := range []string{"async def ", "def ", "class ", "func ", "fn ", "type ", "interface "} {
		if strings.HasPrefix(c, kw) {
			return leadingIdentifier(strings.TrimSpace(c[len(kw):]))
		}
	}
	return ""
}

func leadingIdentifier(s string) string {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) {
		c := s[end]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			end++
			continue
		}
		break
	}
	return s[:end]
}

func firstFieldToken(s string) string {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == ',' || s[i] == ';' {
			return s[:i]
		}
	}
	return s
}

func joinCappedList(items []string, limit int) string {
	if limit <= 0 || len(items) <= limit {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:limit], ", ") + fmt.Sprintf(", (+%d more)", len(items)-limit)
}

func buildUnifiedDiff(path, before, after string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		path = "file"
	}
	before = normalizePreviewText(before)
	after = normalizePreviewText(after)
	if before == after {
		return ""
	}
	oldLines := splitPreviewLines(before)
	newLines := splitPreviewLines(after)
	if before == "" {
		return buildUnifiedDiffAllAdded(path, newLines)
	}
	if after == "" {
		return buildUnifiedDiffAllDeleted(path, oldLines)
	}

	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	oldSuffix := len(oldLines) - 1
	newSuffix := len(newLines) - 1
	for oldSuffix >= prefix && newSuffix >= prefix && oldLines[oldSuffix] == newLines[newSuffix] {
		oldSuffix--
		newSuffix--
	}

	contextStart := prefix - 3
	if contextStart < 0 {
		contextStart = 0
	}
	oldContextEnd := oldSuffix + 3
	if oldContextEnd >= len(oldLines) {
		oldContextEnd = len(oldLines) - 1
	}
	newContextEnd := newSuffix + 3
	if newContextEnd >= len(newLines) {
		newContextEnd = len(newLines) - 1
	}

	contextBeforeCount := prefix - contextStart
	if contextBeforeCount < 0 {
		contextBeforeCount = 0
	}
	removedCount := 0
	if prefix <= oldSuffix {
		removedCount = oldSuffix - prefix + 1
	}
	addedCount := 0
	if prefix <= newSuffix {
		addedCount = newSuffix - prefix + 1
	}
	contextAfterCount := newContextEnd - newSuffix
	if contextAfterCount < 0 {
		contextAfterCount = 0
	}
	oldCount := contextBeforeCount + removedCount + contextAfterCount
	newCount := contextBeforeCount + addedCount + contextAfterCount

	var lines []string
	lines = append(lines, fmt.Sprintf("diff --git a/%s b/%s", path, path))
	lines = append(lines, fmt.Sprintf("--- a/%s", path))
	lines = append(lines, fmt.Sprintf("+++ b/%s", path))
	lines = append(lines, fmt.Sprintf("@@ -%d,%d +%d,%d @@", contextStart+1, oldCount, contextStart+1, newCount))
	for i := contextStart; i < prefix; i++ {
		lines = append(lines, " "+oldLines[i])
	}
	for i := prefix; i <= oldSuffix; i++ {
		lines = append(lines, "-"+oldLines[i])
	}
	for i := prefix; i <= newSuffix; i++ {
		lines = append(lines, "+"+newLines[i])
	}
	afterStart := newSuffix + 1
	for i := afterStart; i <= newContextEnd && i < len(newLines); i++ {
		lines = append(lines, " "+newLines[i])
	}
	return strings.Join(lines, "\n")
}

func buildUnifiedDiffAllAdded(path string, lines []string) string {
	var out []string
	out = append(out, fmt.Sprintf("diff --git a/%s b/%s", path, path))
	out = append(out, "new file mode 100644")
	out = append(out, "--- /dev/null")
	out = append(out, fmt.Sprintf("+++ b/%s", path))
	out = append(out, fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines)))
	for _, line := range lines {
		out = append(out, "+"+line)
	}
	return strings.Join(out, "\n")
}

func buildUnifiedDiffAllDeleted(path string, lines []string) string {
	var out []string
	out = append(out, fmt.Sprintf("diff --git a/%s b/%s", path, path))
	out = append(out, "deleted file mode 100644")
	out = append(out, fmt.Sprintf("--- a/%s", path))
	out = append(out, "+++ /dev/null")
	out = append(out, fmt.Sprintf("@@ -1,%d +0,0 @@", len(lines)))
	for _, line := range lines {
		out = append(out, "-"+line)
	}
	return strings.Join(out, "\n")
}

func normalizePreviewText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

func splitPreviewLines(text string) []string {
	if text == "" {
		return []string{}
	}
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return []string{""}
	}
	return strings.Split(text, "\n")
}

func buildSelectionAwareEditPreview(ws Workspace, path, before, after string) string {
	selection := ws.Selection()
	full := buildEditPreview(path, before, after)
	if selection == nil || !selection.HasSelection() {
		return full
	}
	target := path
	if filepath.IsAbs(target) {
		target = relOrAbs(ws.Root, target)
	}
	selectedPath := selection.FilePath
	if filepath.IsAbs(selectedPath) {
		selectedPath = relOrAbs(ws.Root, selectedPath)
	}
	if !strings.EqualFold(filepath.ToSlash(target), filepath.ToSlash(selectedPath)) {
		return full
	}

	selectionPath := fmt.Sprintf("%s:%d-%d", target, selection.StartLine, selection.EndLine)
	beforeSelection := sliceLines(before, selection.StartLine, selection.EndLine)
	afterSelection := sliceLines(after, selection.StartLine, selection.EndLine)
	selectionPreview := buildEditPreview(selectionPath, beforeSelection, afterSelection)
	if strings.Contains(selectionPreview, "No textual changes detected.") {
		selectionPreview = fmt.Sprintf("Selection focus for %s\nNo changes detected inside the current selection. Some edits may be outside the selected range.", selectionPath)
	} else {
		selectionPreview = "Selection-focused preview\n" + selectionPreview
	}
	return selectionPreview + "\n\n" + full
}

func buildSelectionAwareAfterExcerpt(ws Workspace, path, before, after string, limit int) string {
	if limit <= 0 {
		return ""
	}
	after = normalizePreviewText(after)
	if strings.TrimSpace(after) == "" {
		return ""
	}
	selection := ws.Selection()
	target := path
	if filepath.IsAbs(target) {
		target = relOrAbs(ws.Root, target)
	}
	if selection != nil && selection.HasSelection() {
		selectedPath := selection.FilePath
		if filepath.IsAbs(selectedPath) {
			selectedPath = relOrAbs(ws.Root, selectedPath)
		}
		if strings.EqualFold(filepath.ToSlash(target), filepath.ToSlash(selectedPath)) {
			return buildSelectionAfterExcerptForSelection(target, after, *selection, limit)
		}
	}
	return buildChangedAfterExcerpt(target, before, after, limit)
}

func buildSelectionAfterExcerptForSelection(path string, after string, selection ViewerSelection, limit int) string {
	if start, end, ok := reviewFunctionSpanForSelection(after, selection); ok {
		body := preWriteFunctionBodyContextBody(after, selection, start, end, limit)
		if strings.TrimSpace(body) != "" {
			return fmt.Sprintf("After function body excerpt: %s:%d-%d\n%s", filepath.ToSlash(path), start, end, body)
		}
	}
	lines := reviewNormalizedLines(after)
	start := selection.StartLine - 12
	if start < 1 {
		start = 1
	}
	end := selection.EndLine + 120
	if end > len(lines) {
		end = len(lines)
	}
	if end < start {
		return ""
	}
	body := preWriteSelectionFileContextBody(after, selection, start, end, limit)
	if strings.TrimSpace(body) == "" {
		return ""
	}
	return fmt.Sprintf("After selected-range excerpt: %s:%d-%d\n%s", filepath.ToSlash(path), start, end, body)
}

func buildChangedAfterExcerpt(path string, before string, after string, limit int) string {
	before = normalizePreviewText(before)
	after = normalizePreviewText(after)
	oldLines := splitPreviewLines(before)
	newLines := splitPreviewLines(after)
	if len(newLines) == 0 {
		return ""
	}
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	oldSuffix := len(oldLines) - 1
	newSuffix := len(newLines) - 1
	for oldSuffix >= prefix && newSuffix >= prefix && oldLines[oldSuffix] == newLines[newSuffix] {
		oldSuffix--
		newSuffix--
	}
	if prefix >= len(newLines) && newSuffix < prefix {
		return ""
	}
	start := prefix - 40
	if start < 0 {
		start = 0
	}
	end := newSuffix + 80
	if end < prefix {
		end = prefix + 80
	}
	if end >= len(newLines) {
		end = len(newLines) - 1
	}
	var b strings.Builder
	fmt.Fprintf(&b, "After changed-code excerpt: %s:%d-%d\n", filepath.ToSlash(path), start+1, end+1)
	for i := start; i <= end && i < len(newLines); i++ {
		fmt.Fprintf(&b, "%5d | %s\n", i+1, newLines[i])
	}
	return compactPromptSectionPreserveHeadTail(b.String(), limit)
}
