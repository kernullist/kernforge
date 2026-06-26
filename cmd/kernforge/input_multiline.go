package main

import (
	"fmt"
	"io"
	"strings"
)

// This file holds the platform-independent geometry and rendering helpers for
// the interactive line editor's multiline mode. The editor's rune buffer may
// contain embedded '\n' once a paste (or an Alt+Enter chord) introduces one;
// every helper here understands those newlines. The Windows and POSIX readers
// both call into these so the layout math lives in one tested place.

// bufferHasNewline reports whether the buffer contains an embedded newline.
// When true the line editor switches from the single-line fast paths to a full
// multiline redraw.
func bufferHasNewline(buffer []rune) bool {
	for _, r := range buffer {
		if r == '\n' {
			return true
		}
	}
	return false
}

// splitRuneLines splits a rune buffer on '\n', preserving an empty trailing
// segment so "a\n" yields {"a", ""}. The newline runes are not included in any
// segment. The returned slices share the buffer's backing array, so callers
// must not mutate the buffer while holding them.
func splitRuneLines(buffer []rune) [][]rune {
	lines := make([][]rune, 0, 4)
	start := 0
	for i, r := range buffer {
		if r == '\n' {
			lines = append(lines, buffer[start:i])
			start = i + 1
		}
	}
	lines = append(lines, buffer[start:])
	return lines
}

// runeCellWidth returns the display width of a rune slice in terminal cells.
func runeCellWidth(rs []rune) int {
	w := 0
	for _, r := range rs {
		w += runeWidth(r)
	}
	return w
}

// wrapRows returns how many terminal rows a single logical line of the given
// cell width occupies at the given terminal width. It uses the pending-wrap
// convention (a line that exactly fills the width stays on one row), matching
// the single-line renderer's ((width-1)/termW)+1 formula.
func wrapRows(width, termW int) int {
	if termW <= 0 {
		return 1
	}
	if width <= 0 {
		return 1
	}
	return ((width - 1) / termW) + 1
}

// multilineGeometry describes how a (possibly newline-containing) buffer maps
// onto terminal rows for a given prompt/continuation prefix and width.
type multilineGeometry struct {
	totalRows int // total terminal rows the rendered content occupies (>= 1)
	cursorRow int // 0-based row of the cursor measured from the first row
	cursorCol int // 0-based column of the cursor within its row (0..termW-1)
}

// computeMultilineGeometry computes the row/column layout for a rune buffer.
// firstPrefix is the visible width of the primary prompt shown on the first
// line; contPrefix is the visible width of the continuation prompt shown on
// each line after an embedded newline. cursorPos is a rune index into buffer.
//
// Rows are counted with the same pending-wrap convention as the single-line
// renderer. When the cursor sits exactly at the end of a logical line whose
// width is an exact multiple of termW it is reported at the right margin of
// that line's last row rather than column 0 of a non-existent next row.
func computeMultilineGeometry(buffer []rune, cursorPos, firstPrefix, contPrefix, termW int) multilineGeometry {
	if termW <= 0 {
		termW = 1
	}
	if cursorPos < 0 {
		cursorPos = 0
	}
	if cursorPos > len(buffer) {
		cursorPos = len(buffer)
	}
	lines := splitRuneLines(buffer)
	geo := multilineGeometry{}
	cursorFound := false
	runeIndex := 0
	for i, line := range lines {
		prefix := firstPrefix
		if i > 0 {
			prefix = contPrefix
		}
		segStart := runeIndex
		segEnd := runeIndex + len(line) // index just past this line's last content rune
		if !cursorFound && cursorPos >= segStart && cursorPos <= segEnd {
			flatCol := prefix + runeCellWidth(line[:cursorPos-segStart])
			row := flatCol / termW
			col := flatCol % termW
			if col == 0 && flatCol > 0 && cursorPos == segEnd {
				// The cursor rests at the end of a line that exactly fills the
				// terminal width: pending-wrap keeps it on the previous row.
				row--
				col = termW - 1
			}
			geo.cursorRow = geo.totalRows + row
			geo.cursorCol = col
			cursorFound = true
		}
		geo.totalRows += wrapRows(prefix+runeCellWidth(line), termW)
		runeIndex = segEnd + 1 // skip the '\n' separator
	}
	if !cursorFound {
		geo.cursorRow = geo.totalRows - 1
		if geo.cursorRow < 0 {
			geo.cursorRow = 0
		}
	}
	return geo
}

// renderMultilineBody builds the visible string for a multiline buffer: the
// primary prompt followed by the first line, then for every later line a CRLF,
// the continuation prompt, and that line's text. It emits no trailing newline.
// The CRLF separators move both Windows consoles and POSIX raw-mode terminals
// to a fresh row. For a buffer with no newline the result is exactly
// prompt+text, identical to the single-line renderer.
func renderMultilineBody(buffer []rune, prompt, continuation string) string {
	lines := splitRuneLines(buffer)
	var b strings.Builder
	for i, line := range lines {
		if i == 0 {
			b.WriteString(prompt)
		} else {
			b.WriteString("\r\n")
			b.WriteString(continuation)
		}
		b.WriteString(string(line))
	}
	return b.String()
}

// redrawMultiline repaints a newline-containing buffer. It steps the cursor up
// from its current row (*prevLines) to the top of the input area, clears
// downward, prints the prompt/continuation-prefixed lines, then positions the
// cursor at its logical row and column. *prevLines is updated to the cursor's
// new row so the next redraw (and the cancel path) can step back up correctly.
// On Windows the caller must have enabled virtual-terminal processing first.
func redrawMultiline(w io.Writer, prompt, continuation string, buffer []rune, cursorPos int, prevLines *int, termW int) {
	if *prevLines > 0 {
		fmt.Fprintf(w, "\x1b[%dA", *prevLines)
	}
	fmt.Fprint(w, "\r\x1b[J")
	fmt.Fprint(w, renderMultilineBody(buffer, prompt, continuation))
	geo := computeMultilineGeometry(buffer, cursorPos, visibleLen(prompt), visibleLen(continuation), termW)
	// After printing, the cursor is at the end of the last row; return to the
	// first column, step up to the cursor's row, then right to its column.
	fmt.Fprint(w, "\r")
	if up := (geo.totalRows - 1) - geo.cursorRow; up > 0 {
		fmt.Fprintf(w, "\x1b[%dA", up)
	}
	if geo.cursorCol > 0 {
		fmt.Fprintf(w, "\x1b[%dC", geo.cursorCol)
	}
	*prevLines = geo.cursorRow
}

// verticalCursorTarget returns the rune index after moving the cursor one
// logical line up (dir < 0) or down (dir > 0), preserving the cell column
// within the line as closely as possible without exceeding it. It returns -1
// when there is no logical line in that direction, so the caller can fall back
// to history navigation. Motion is by logical (newline-delimited) line; a line
// that visually wraps is treated as one line.
func verticalCursorTarget(buffer []rune, cursorPos, dir int) int {
	lines := splitRuneLines(buffer)
	if len(lines) <= 1 {
		return -1
	}
	if cursorPos < 0 {
		cursorPos = 0
	}
	if cursorPos > len(buffer) {
		cursorPos = len(buffer)
	}
	starts := make([]int, len(lines))
	idx := 0
	for i, line := range lines {
		starts[i] = idx
		idx += len(line) + 1
	}
	cur := len(lines) - 1
	for i, line := range lines {
		if cursorPos >= starts[i] && cursorPos <= starts[i]+len(line) {
			cur = i
			break
		}
	}
	target := cur + dir
	if target < 0 || target >= len(lines) {
		return -1
	}
	colCells := runeCellWidth(lines[cur][:cursorPos-starts[cur]])
	targetLine := lines[target]
	acc, offset := 0, 0
	for offset < len(targetLine) {
		w := runeWidth(targetLine[offset])
		if acc+w > colCells {
			break
		}
		acc += w
		offset++
	}
	return starts[target] + offset
}

// lineHomeIndex returns the rune index of the start of the logical line that
// contains cursorPos: the position just after the preceding '\n', or 0.
func lineHomeIndex(buffer []rune, cursorPos int) int {
	if cursorPos > len(buffer) {
		cursorPos = len(buffer)
	}
	for i := cursorPos - 1; i >= 0; i-- {
		if buffer[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

// lineEndIndex returns the rune index of the end of the logical line that
// contains cursorPos: the position just before the next '\n', or len(buffer).
func lineEndIndex(buffer []rune, cursorPos int) int {
	if cursorPos < 0 {
		cursorPos = 0
	}
	for i := cursorPos; i < len(buffer); i++ {
		if buffer[i] == '\n' {
			return i
		}
	}
	return len(buffer)
}
