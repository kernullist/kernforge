//go:build !windows

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// terminalWidth returns the real width of the controlling terminal on
// non-Windows platforms. It falls back to 120 columns when stdout is not a
// terminal or the size query fails.
func terminalWidth() int {
	fd := int(os.Stdout.Fd())
	if term.IsTerminal(fd) {
		if w, _, err := term.GetSize(fd); err == nil && w > 0 {
			return w
		}
	}
	// Some environments report only stdin as a terminal (piped stdout).
	if term.IsTerminal(int(os.Stdin.Fd())) {
		if w, _, err := term.GetSize(int(os.Stdin.Fd())); err == nil && w > 0 {
			return w
		}
	}
	return 120
}

// readInteractiveLine implements a cross-platform raw-mode line editor for
// POSIX terminals (Linux/macOS/WSL). It mirrors the Windows implementation:
// cursor editing (Left/Right/Home/End), Up/Down history navigation,
// Backspace/Delete, and a Tab-completion hook compatible with completeLine.
//
// When stdin is not a terminal it returns usedInteractive=false so the caller
// falls back to the plain bufio reader, preserving non-interactive behavior.
func (rt *runtimeState) readInteractiveLine(prompt string, initial string, historyNav *inputHistoryNavigator, allowEmptySubmit bool) (string, bool, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", false, nil
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		// Could not enter raw mode; let the caller use the line reader.
		return "", false, nil
	}
	defer term.Restore(fd, oldState)

	// Enable bracketed paste: the terminal then wraps pasted text in
	// ESC[200~ ... ESC[201~ markers, letting us capture a multi-line paste as a
	// single block instead of submitting on the first embedded newline.
	fmt.Fprint(rt.writer, "\x1b[?2004h")
	defer fmt.Fprint(rt.writer, "\x1b[?2004l")

	reader := bufio.NewReader(os.Stdin)

	var buffer []rune
	if initial != "" {
		buffer = []rune(initial)
	}
	cursorPos := len(buffer)
	prevLines := 0

	runeSliceWidth := func(items []rune) int {
		width := 0
		for _, r := range items {
			width += runeWidth(r)
		}
		return width
	}
	widthAfterCursor := func() int {
		if cursorPos >= len(buffer) {
			return 0
		}
		return runeSliceWidth(buffer[cursorPos:])
	}
	moveCursorLeft := func(count int) {
		if count > 0 {
			fmt.Fprintf(rt.writer, "\x1b[%dD", count)
		}
	}
	moveCursorRight := func(count int) {
		if count > 0 {
			fmt.Fprintf(rt.writer, "\x1b[%dC", count)
		}
	}
	redraw := func() {
		termW := terminalWidth()
		if bufferHasNewline(buffer) {
			redrawMultiline(rt.writer, prompt, rt.ui.continuationPrompt(), buffer, cursorPos, &prevLines, termW)
			return
		}
		if prevLines > 0 {
			fmt.Fprintf(rt.writer, "\x1b[%dA", prevLines)
		}
		// Return to column 0 and clear to end of screen, then redraw.
		fmt.Fprint(rt.writer, "\r\x1b[J")
		current := prompt + string(buffer)
		fmt.Fprint(rt.writer, current)
		w := visibleLen(current)
		if termW > 0 && w > 0 {
			prevLines = (w - 1) / termW
		} else {
			prevLines = 0
		}
		if cells := widthAfterCursor(); cells > 0 {
			moveCursorLeft(cells)
		}
	}

	redraw()
	for {
		key, err := readPosixKey(reader)
		if err != nil {
			if err == io.EOF {
				// Ctrl-D / closed stdin with empty buffer terminates input.
				if len(buffer) == 0 {
					return "", true, io.EOF
				}
				// Otherwise treat as submit of the current buffer.
				fmt.Fprint(rt.writer, "\n")
				return string(buffer), true, nil
			}
			return "", true, err
		}
		switch key.kind {
		case posixKeyEnter:
			if !allowEmptySubmit && len(buffer) == 0 {
				continue
			}
			fmt.Fprint(rt.writer, "\r\n")
			return string(buffer), true, nil
		case posixKeyInterrupt:
			// Ctrl-C: surface as EOF so the caller treats it as a clean exit,
			// matching the Windows path (Ctrl-C maps to io.EOF there too).
			return "", true, io.EOF
		case posixKeyEscape:
			if rt.shouldIgnorePromptEscape() {
				continue
			}
			fmt.Fprint(rt.writer, cancelInteractiveLinePosix(prevLines))
			return "", true, ErrPromptCanceled
		case posixKeyBackspace:
			if cursorPos == 0 {
				continue
			}
			buffer = append(buffer[:cursorPos-1], buffer[cursorPos:]...)
			cursorPos--
			historyNav.SyncBuffer(string(buffer))
			redraw()
		case posixKeyDelete:
			if cursorPos >= len(buffer) {
				continue
			}
			buffer = append(buffer[:cursorPos], buffer[cursorPos+1:]...)
			historyNav.SyncBuffer(string(buffer))
			redraw()
		case posixKeyLeft:
			if bufferHasNewline(buffer) {
				if cursorPos > 0 {
					cursorPos--
					redraw()
				}
				continue
			}
			if cursorPos > 0 {
				cursorPos--
				moveCursorLeft(runeWidth(buffer[cursorPos]))
			}
		case posixKeyRight:
			if bufferHasNewline(buffer) {
				if cursorPos < len(buffer) {
					cursorPos++
					redraw()
				}
				continue
			}
			if cursorPos < len(buffer) {
				moveCursorRight(runeWidth(buffer[cursorPos]))
				cursorPos++
			}
		case posixKeyHome:
			if bufferHasNewline(buffer) {
				cursorPos = lineHomeIndex(buffer, cursorPos)
				redraw()
				continue
			}
			moveCursorLeft(runeSliceWidth(buffer[:cursorPos]))
			cursorPos = 0
		case posixKeyEnd:
			if bufferHasNewline(buffer) {
				cursorPos = lineEndIndex(buffer, cursorPos)
				redraw()
				continue
			}
			moveCursorRight(widthAfterCursor())
			cursorPos = len(buffer)
		case posixKeyUp:
			if bufferHasNewline(buffer) {
				// Multiline buffer: move the cursor up a logical line instead
				// of navigating history.
				if target := verticalCursorTarget(buffer, cursorPos, -1); target >= 0 {
					cursorPos = target
					redraw()
				}
				continue
			}
			if next, ok := historyNav.Previous(string(buffer)); ok {
				buffer = []rune(next)
				cursorPos = len(buffer)
				redraw()
			}
		case posixKeyDown:
			if bufferHasNewline(buffer) {
				if target := verticalCursorTarget(buffer, cursorPos, 1); target >= 0 {
					cursorPos = target
					redraw()
				}
				continue
			}
			if next, ok := historyNav.Next(string(buffer)); ok {
				buffer = []rune(next)
				cursorPos = len(buffer)
				redraw()
			}
		case posixKeyTab:
			updated, suggestions, handled := rt.completeLine(string(buffer))
			if !handled {
				continue
			}
			buffer = []rune(updated)
			cursorPos = len(buffer)
			historyNav.SyncBuffer(updated)
			if len(suggestions) > 0 {
				rendered := rt.ui.formatCompletionSuggestions(suggestions, string(buffer))
				fmt.Fprint(rt.writer, "\r\n"+rendered+"\r\n")
				prevLines = 0
			}
			redraw()
		case posixKeyRune:
			if key.r < 32 {
				continue
			}
			buffer = append(buffer, 0)
			copy(buffer[cursorPos+1:], buffer[cursorPos:])
			buffer[cursorPos] = key.r
			cursorPos++
			historyNav.SyncBuffer(string(buffer))
			redraw()
		case posixKeyAltEnter:
			// Manual multiline: insert a literal newline at the cursor.
			buffer = insertRuneAt(buffer, cursorPos, '\n')
			cursorPos++
			historyNav.SyncBuffer(string(buffer))
			redraw()
		case posixKeyPasteStart:
			pasted, perr := readBracketedPaste(reader)
			// Stage the block for review; strip a single trailing newline so a
			// copied block ending in a line break leaves no dangling empty line.
			// The operator presses Enter to submit.
			pasted = strings.TrimSuffix(pasted, "\n")
			if pasted != "" {
				for _, r := range []rune(pasted) {
					buffer = insertRuneAt(buffer, cursorPos, r)
					cursorPos++
				}
				historyNav.SyncBuffer(string(buffer))
				redraw()
			}
			if perr != nil {
				// Stdin closed mid-paste: keep what arrived; the next read hits
				// EOF and the loop's error path handles submit/terminate.
				continue
			}
		case posixKeyPasteEnd:
			// A stray paste-end with no matching start: nothing to do.
			continue
		}
	}
}

// insertRuneAt returns buffer with r inserted at index pos.
func insertRuneAt(buffer []rune, pos int, r rune) []rune {
	buffer = append(buffer, 0)
	copy(buffer[pos+1:], buffer[pos:])
	buffer[pos] = r
	return buffer
}

// readBracketedPaste reads the body of a bracketed paste after the ESC[200~
// start marker, up to and including the ESC[201~ end marker (which it
// consumes but does not return). Carriage returns are normalized to '\n' so
// pasted line breaks become embedded newlines in the buffer. A read error
// (including io.EOF on a truncated paste) returns what was collected so far.
func readBracketedPaste(reader *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		r, _, err := reader.ReadRune()
		if err != nil {
			return normalizePastedNewlines(b.String()), err
		}
		if r == 0x1b {
			if consumePasteEndMarker(reader) {
				return normalizePastedNewlines(b.String()), nil
			}
			// A literal ESC inside pasted text (uncommon): keep it verbatim.
			b.WriteRune(r)
			continue
		}
		b.WriteRune(r)
	}
}

// consumePasteEndMarker checks whether the bytes following an ESC are the
// remainder of the bracketed-paste end marker ("[201~"). If so it consumes
// them and returns true; otherwise it leaves the reader untouched.
func consumePasteEndMarker(reader *bufio.Reader) bool {
	const rest = "[201~"
	peeked, err := reader.Peek(len(rest))
	if err != nil || string(peeked) != rest {
		return false
	}
	if _, err := reader.Discard(len(rest)); err != nil {
		return false
	}
	return true
}

// normalizePastedNewlines converts CRLF and bare CR line endings to '\n'.
func normalizePastedNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

func cancelInteractiveLinePosix(prevLines int) string {
	var out string
	if prevLines > 0 {
		out = fmt.Sprintf("\x1b[%dA", prevLines)
	}
	return out + "\r\x1b[J"
}

type posixKeyKind int

const (
	posixKeyRune posixKeyKind = iota
	posixKeyEnter
	posixKeyBackspace
	posixKeyDelete
	posixKeyLeft
	posixKeyRight
	posixKeyUp
	posixKeyDown
	posixKeyHome
	posixKeyEnd
	posixKeyTab
	posixKeyEscape
	posixKeyInterrupt
	posixKeyPasteStart
	posixKeyPasteEnd
	posixKeyAltEnter
)

type posixKey struct {
	kind posixKeyKind
	r    rune
}

// readPosixKey reads and decodes a single key (or key sequence) from a raw-mode
// terminal. It understands the common CSI/SS3 escape sequences emitted by
// xterm-compatible terminals for arrows, Home/End, and Delete.
func readPosixKey(reader *bufio.Reader) (posixKey, error) {
	r, _, err := reader.ReadRune()
	if err != nil {
		return posixKey{}, err
	}
	switch r {
	case '\r', '\n':
		return posixKey{kind: posixKeyEnter}, nil
	case '\t':
		return posixKey{kind: posixKeyTab}, nil
	case 0x03: // Ctrl-C
		return posixKey{kind: posixKeyInterrupt}, nil
	case 0x04: // Ctrl-D
		return posixKey{}, io.EOF
	case 0x7f, 0x08: // Backspace / Ctrl-H
		return posixKey{kind: posixKeyBackspace}, nil
	case 0x1b: // ESC: could be a lone Escape or the start of a sequence.
		return readPosixEscape(reader)
	}
	return posixKey{kind: posixKeyRune, r: r}, nil
}

// readPosixEscape decodes an escape sequence after the leading ESC byte. When
// no recognizable sequence follows (a bare ESC keypress) it reports an Escape.
func readPosixEscape(reader *bufio.Reader) (posixKey, error) {
	if reader.Buffered() == 0 {
		// Lone ESC with nothing buffered: treat as the Escape key.
		return posixKey{kind: posixKeyEscape}, nil
	}
	next, _, err := reader.ReadRune()
	if err != nil {
		if err == io.EOF {
			return posixKey{kind: posixKeyEscape}, nil
		}
		return posixKey{}, err
	}
	switch next {
	case '[': // CSI
		return readPosixCSI(reader)
	case 'O': // SS3 (application cursor keys)
		final, _, err := reader.ReadRune()
		if err != nil {
			return posixKey{}, err
		}
		return mapPosixFinalByte(final), nil
	case '\r', '\n': // Alt+Enter: insert a literal newline (manual multiline).
		return posixKey{kind: posixKeyAltEnter}, nil
	}
	// Unknown ESC-prefixed sequence: ignore the introducer, emit the rune.
	if next < 32 {
		return posixKey{kind: posixKeyEscape}, nil
	}
	return posixKey{kind: posixKeyRune, r: next}, nil
}

func readPosixCSI(reader *bufio.Reader) (posixKey, error) {
	var params []rune
	for {
		r, _, err := reader.ReadRune()
		if err != nil {
			return posixKey{}, err
		}
		// Final bytes of a CSI sequence are in the range 0x40-0x7E.
		if r >= 0x40 && r <= 0x7e {
			if r == '~' {
				return mapPosixTildeSequence(string(params)), nil
			}
			return mapPosixFinalByte(r), nil
		}
		params = append(params, r)
	}
}

func mapPosixFinalByte(final rune) posixKey {
	switch final {
	case 'A':
		return posixKey{kind: posixKeyUp}
	case 'B':
		return posixKey{kind: posixKeyDown}
	case 'C':
		return posixKey{kind: posixKeyRight}
	case 'D':
		return posixKey{kind: posixKeyLeft}
	case 'H':
		return posixKey{kind: posixKeyHome}
	case 'F':
		return posixKey{kind: posixKeyEnd}
	}
	// Unrecognized: swallow it (no rune emitted) to avoid corrupting the line.
	return posixKey{kind: posixKeyRune, r: 0}
}

func mapPosixTildeSequence(params string) posixKey {
	switch params {
	case "1", "7":
		return posixKey{kind: posixKeyHome}
	case "4", "8":
		return posixKey{kind: posixKeyEnd}
	case "3":
		return posixKey{kind: posixKeyDelete}
	case "200":
		return posixKey{kind: posixKeyPasteStart}
	case "201":
		return posixKey{kind: posixKeyPasteEnd}
	}
	return posixKey{kind: posixKeyRune, r: 0}
}
