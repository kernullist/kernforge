package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var unifiedHunkPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func renderSelectionGitDiff(root string, selection ViewerSelection) (string, error) {
	if !selection.HasSelection() {
		return "", fmt.Errorf("no current selection")
	}
	path := selection.FilePath
	if !strings.HasPrefix(strings.ToLower(path), strings.ToLower(root)) {
		path = filepath.Join(root, path)
	}
	rel := relOrAbs(root, path)
	cmd := exec.CommandContext(context.Background(), "git", "diff", "--", rel)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil && text == "" {
		return "", fmt.Errorf("git diff failed: %w", err)
	}
	if text == "" {
		return "(no git diff for current selection)", nil
	}
	return filterUnifiedDiffBySelection(text, selection.StartLine, selection.EndLine), nil
}

func filterUnifiedDiffBySelection(diff string, startLine, endLine int) string {
	lines := strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n")
	var out []string
	headerDone := false
	includeCurrentHunk := false
	var currentHunk []string
	for _, line := range lines {
		if strings.HasPrefix(line, "@@ ") {
			if includeCurrentHunk && len(currentHunk) > 0 {
				out = append(out, currentHunk...)
			}
			currentHunk = []string{line}
			includeCurrentHunk = hunkIntersectsSelection(line, startLine, endLine)
			headerDone = true
			continue
		}
		if !headerDone {
			out = append(out, line)
			continue
		}
		currentHunk = append(currentHunk, line)
	}
	if includeCurrentHunk && len(currentHunk) > 0 {
		out = append(out, currentHunk...)
	}
	if len(out) == 0 {
		return "(no overlapping diff hunks for current selection)"
	}
	return strings.Join(out, "\n")
}

func hunkIntersectsSelection(header string, startLine, endLine int) bool {
	match := unifiedHunkPattern.FindStringSubmatch(header)
	if len(match) != 5 {
		return false
	}
	oldStart, _ := strconv.Atoi(match[1])
	oldCount := 1
	if match[2] != "" {
		oldCount, _ = strconv.Atoi(match[2])
	}
	newStart, _ := strconv.Atoi(match[3])
	newCount := 1
	if match[4] != "" {
		newCount, _ = strconv.Atoi(match[4])
	}
	oldEnd := oldStart + maxInt(oldCount, 1) - 1
	newEnd := newStart + maxInt(newCount, 1) - 1
	return rangesOverlap(startLine, endLine, oldStart, oldEnd) || rangesOverlap(startLine, endLine, newStart, newEnd)
}

func rangesOverlap(aStart, aEnd, bStart, bEnd int) bool {
	return aStart <= bEnd && bStart <= aEnd
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
