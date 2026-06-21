//go:build !windows

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"

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
			if cursorPos > 0 {
				cursorPos--
				moveCursorLeft(runeWidth(buffer[cursorPos]))
			}
		case posixKeyRight:
			if cursorPos < len(buffer) {
				moveCursorRight(runeWidth(buffer[cursorPos]))
				cursorPos++
			}
		case posixKeyHome:
			moveCursorLeft(runeSliceWidth(buffer[:cursorPos]))
			cursorPos = 0
		case posixKeyEnd:
			moveCursorRight(widthAfterCursor())
			cursorPos = len(buffer)
		case posixKeyUp:
			if next, ok := historyNav.Previous(string(buffer)); ok {
				buffer = []rune(next)
				cursorPos = len(buffer)
				redraw()
			}
		case posixKeyDown:
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
		}
	}
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
	}
	return posixKey{kind: posixKeyRune, r: 0}
}
