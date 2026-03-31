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
