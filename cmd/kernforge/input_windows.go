//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const (
	enableProcessedInput = 0x0001
	enableLineInput      = 0x0002
	enableEchoInput      = 0x0004
	keyEventType         = 0x0001
	inputVirtualKeyBack  = 0x08
	inputVirtualKeyTab   = 0x09
	inputVirtualKeyEnter = 0x0D
	inputVirtualKeyEsc   = 0x1B
	inputVirtualKeyLeft  = 0x25
	inputVirtualKeyUp    = 0x26
	inputVirtualKeyRight = 0x27
	inputVirtualKeyDown  = 0x28
	inputVirtualKeyDel   = 0x2E
	inputVirtualKeyHome  = 0x24
	inputVirtualKeyEnd   = 0x23

	// Modifier bits in keyEventRecord.ControlKeyState. Holding either Alt while
	// pressing Enter inserts a literal newline instead of submitting, giving the
	// operator a manual multiline chord alongside paste detection. Alt is used
	// (not Shift) because nobody holds Alt while typing text, so it never
	// false-triggers on a normal submit.
	rightAltPressed = 0x0001
	leftAltPressed  = 0x0002
)

var (
	kernel32DLL                    = syscall.NewLazyDLL("kernel32.dll")
	getConsoleModeProc             = kernel32DLL.NewProc("GetConsoleMode")
	setConsoleModeProc             = kernel32DLL.NewProc("SetConsoleMode")
	readConsoleInputProc           = kernel32DLL.NewProc("ReadConsoleInputW")
	peekConsoleInputProc           = kernel32DLL.NewProc("PeekConsoleInputW")
	getConsoleScreenBufferInfoProc = kernel32DLL.NewProc("GetConsoleScreenBufferInfo")
)

type consoleScreenBufferInfo struct {
	Size              [2]int16
	CursorPosition    [2]int16
	Attributes        uint16
	Window            [4]int16
	MaximumWindowSize [2]int16
}

func terminalWidth() int {
	handle := syscall.Handle(os.Stdout.Fd())
	var info consoleScreenBufferInfo
	r1, _, _ := getConsoleScreenBufferInfoProc.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&info)),
	)
	if r1 != 0 {
		w := int(info.Window[2]-info.Window[0]) + 1
		if w > 0 {
			return w
		}
	}
	return 120
}

type keyEventRecord struct {
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	UnicodeChar     uint16
	ControlKeyState uint32
}

type inputRecord struct {
	EventType uint16
	_         uint16
	KeyEvent  keyEventRecord
}

func (rt *runtimeState) readInteractiveLine(prompt string, initial string, historyNav *inputHistoryNavigator, allowEmptySubmit bool) (string, bool, error) {
	handle := syscall.Handle(os.Stdin.Fd())
	var originalMode uint32
	r1, _, _ := getConsoleModeProc.Call(uintptr(handle), uintptr(unsafe.Pointer(&originalMode)))
	if r1 == 0 {
		return "", false, nil
	}

	rawMode := originalMode &^ (enableLineInput | enableEchoInput)
	rawMode &^= enableProcessedInput
	r1, _, err := setConsoleModeProc.Call(uintptr(handle), uintptr(rawMode))
	if r1 == 0 {
		return "", false, err
	}
	defer setConsoleModeProc.Call(uintptr(handle), uintptr(originalMode))

	var buffer []rune
	if initial != "" {
		buffer = []rune(initial)
	}
	cursorPos := len(buffer)
	// prevLines tracks the cursor's current row offset from the top of the
	// rendered input area. For a single unwrapped line this is 0; for wrapped
	// or multiline (newline-containing) input the next redraw and the cancel
	// path use it to step back up to the first row before clearing.
	prevLines := 0
	currentLineCount := func() int {
		termW := terminalWidth()
		geo := computeMultilineGeometry(buffer, cursorPos,
			visibleLen(prompt), visibleLen(rt.ui.continuationPrompt()), termW)
		return geo.totalRows
	}
	insertRune := func(ch rune, count int) {
		for i := 0; i < count; i++ {
			buffer = append(buffer, 0)
			copy(buffer[cursorPos+1:], buffer[cursorPos:])
			buffer[cursorPos] = ch
			cursorPos++
		}
	}
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
	redraw := func() {
		termW := terminalWidth()
		ensureVirtualTerminalProcessing()
		if bufferHasNewline(buffer) {
			redrawMultiline(rt.writer, prompt, rt.ui.continuationPrompt(), buffer, cursorPos, &prevLines, termW)
			return
		}
		// Move cursor up to the first line if previous content wrapped
		if prevLines > 0 {
			fmt.Fprintf(rt.writer, "\x1b[%dA", prevLines)
		}
		// Clear from cursor to end of screen, then return to column 0
		fmt.Fprint(rt.writer, "\r\x1b[J")
		current := prompt + string(buffer)
		fmt.Fprint(rt.writer, current)
		w := visibleLen(current)
		if termW > 0 {
			prevLines = (w - 1) / termW
			if w == 0 {
				prevLines = 0
			}
		} else {
			prevLines = 0
		}
		// Move cursor back to cursorPos
		cellsAfter := widthAfterCursor()
		if cellsAfter > 0 {
			fmt.Fprintf(rt.writer, "\x1b[%dD", cellsAfter)
		}
	}
	moveCursorLeft := func(count int) {
		if count > 0 {
			ensureVirtualTerminalProcessing()
			fmt.Fprintf(rt.writer, "\x1b[%dD", count)
		}
	}
	moveCursorRight := func(count int) {
		if count > 0 {
			ensureVirtualTerminalProcessing()
			fmt.Fprintf(rt.writer, "\x1b[%dC", count)
		}
	}
	eraseTrailing := func(count int) {
		if count <= 0 {
			return
		}
		spaces := strings.Repeat(" ", count)
		fmt.Fprint(rt.writer, spaces)
		moveCursorLeft(count)
	}
	rewriteFromCursor := func(charsAfter int, blankCount int) {
		if charsAfter < 0 {
			charsAfter = 0
		}
		if blankCount < 0 {
			blankCount = 0
		}
		tail := ""
		if charsAfter > 0 {
			tail = string(buffer[cursorPos:])
		}
		fmt.Fprint(rt.writer, tail)
		if blankCount > 0 {
			fmt.Fprint(rt.writer, strings.Repeat(" ", blankCount))
		}
		moveCursorLeft(charsAfter + blankCount)
	}
	// drainPasteBurst consumes the remaining queued events of a paste burst,
	// inserting printable runes and embedded newlines at the cursor without
	// redrawing per character (the caller redraws once afterward). The pasted
	// block is staged, not auto-submitted: the operator reviews it and presses
	// Enter to send. A single trailing newline is stripped (the terminating
	// Enter is dropped rather than inserted), so a copied block that ends in a
	// line break does not leave a dangling empty line.
	drainPasteBurst := func() {
		for consoleInputHasPendingKey(handle) {
			event, err := readConsoleKeyEvent(handle)
			if err != nil {
				return
			}
			rc := int(event.RepeatCount)
			if rc < 1 {
				rc = 1
			}
			if event.VirtualKeyCode == inputVirtualKeyEnter {
				if !consoleInputHasPendingKey(handle) {
					// Trailing newline at the end of the burst: drop it.
					return
				}
				insertRune('\n', 1)
				continue
			}
			if event.UnicodeChar == 3 {
				// Ctrl-C inside a paste: stop draining and keep what arrived.
				return
			}
			if ch := rune(event.UnicodeChar); ch >= 32 {
				insertRune(ch, rc)
			}
			// Other control or navigation keys inside a paste are ignored.
		}
	}

	redraw()
	for {
		event, err := readConsoleKeyEvent(handle)
		if err != nil {
			return "", true, err
		}
		repeatCount := int(event.RepeatCount)
		if repeatCount < 1 {
			repeatCount = 1
		}
		switch event.VirtualKeyCode {
		case inputVirtualKeyEnter:
			if enterInsertsNewline(event.ControlKeyState) {
				// Alt+Enter: insert a literal newline (manual multiline).
				insertRune('\n', 1)
				historyNav.SyncBuffer(string(buffer))
				redraw()
				continue
			}
			if consoleInputHasPendingKey(handle) {
				// More input is already queued behind this Enter, so it is a
				// newline inside a paste rather than a submit. Capture this
				// newline, then drain the rest of the burst in one pass. The
				// block is staged for review, not auto-submitted.
				insertRune('\n', 1)
				drainPasteBurst()
				historyNav.SyncBuffer(string(buffer))
				redraw()
				continue
			}
			if !allowEmptySubmit && len(buffer) == 0 {
				continue
			}
			fmt.Fprint(rt.writer, "\n")
			return string(buffer), true, nil
		case inputVirtualKeyEsc:
			if rt.shouldIgnorePromptEscape() {
				continue
			}
			ensureVirtualTerminalProcessing()
			fmt.Fprint(rt.writer, cancelInteractiveLine(prevLines))
			return "", true, ErrPromptCanceled
		case inputVirtualKeyBack:
			if bufferHasNewline(buffer) {
				removed := 0
				for i := 0; i < repeatCount && cursorPos > 0; i++ {
					buffer = append(buffer[:cursorPos-1], buffer[cursorPos:]...)
					cursorPos--
					removed++
				}
				historyNav.SyncBuffer(string(buffer))
				if removed > 0 {
					redraw()
				}
				continue
			}
			beforeLines := currentLineCount()
			removed := 0
			wasAtEnd := cursorPos == len(buffer)
			removedWidth := 0
			for i := 0; i < repeatCount && cursorPos > 0; i++ {
				removedWidth += runeWidth(buffer[cursorPos-1])
				buffer = append(buffer[:cursorPos-1], buffer[cursorPos:]...)
				cursorPos--
				removed++
			}
			historyNav.SyncBuffer(string(buffer))
			if removed == 0 {
				continue
			}
			afterLines := currentLineCount()
			if afterLines == beforeLines {
				moveCursorLeft(removedWidth)
				if wasAtEnd {
					eraseTrailing(removedWidth)
				} else {
					cellsAfter := widthAfterCursor()
					rewriteFromCursor(cellsAfter, removedWidth)
				}
				prevLines = afterLines - 1
			} else {
				redraw()
			}
		case inputVirtualKeyDel:
			if bufferHasNewline(buffer) {
				removed := 0
				for i := 0; i < repeatCount && cursorPos < len(buffer); i++ {
					buffer = append(buffer[:cursorPos], buffer[cursorPos+1:]...)
					removed++
				}
				historyNav.SyncBuffer(string(buffer))
				if removed > 0 {
					redraw()
				}
				continue
			}
			beforeLines := currentLineCount()
			removed := 0
			deletingTrailing := false
			removedWidth := 0
			if cursorPos < len(buffer) {
				deleteCount := repeatCount
				if deleteCount > len(buffer)-cursorPos {
					deleteCount = len(buffer) - cursorPos
				}
				deletingTrailing = cursorPos+deleteCount == len(buffer)
			}
			for i := 0; i < repeatCount && cursorPos < len(buffer); i++ {
				removedWidth += runeWidth(buffer[cursorPos])
				buffer = append(buffer[:cursorPos], buffer[cursorPos+1:]...)
				removed++
			}
			historyNav.SyncBuffer(string(buffer))
			if removed == 0 {
				continue
			}
			afterLines := currentLineCount()
			if afterLines == beforeLines {
				if deletingTrailing {
					eraseTrailing(removedWidth)
				} else {
					cellsAfter := widthAfterCursor()
					rewriteFromCursor(cellsAfter, removedWidth)
				}
				prevLines = afterLines - 1
			} else {
				redraw()
			}
		case inputVirtualKeyLeft:
			if bufferHasNewline(buffer) {
				for i := 0; i < repeatCount && cursorPos > 0; i++ {
					cursorPos--
				}
				redraw()
				continue
			}
			moved := 0
			movedWidth := 0
			for i := 0; i < repeatCount && cursorPos > 0; i++ {
				cursorPos--
				moved++
				movedWidth += runeWidth(buffer[cursorPos])
			}
			_ = moved
			moveCursorLeft(movedWidth)
		case inputVirtualKeyRight:
			if bufferHasNewline(buffer) {
				for i := 0; i < repeatCount && cursorPos < len(buffer); i++ {
					cursorPos++
				}
				redraw()
				continue
			}
			movedWidth := 0
			for i := 0; i < repeatCount && cursorPos < len(buffer); i++ {
				movedWidth += runeWidth(buffer[cursorPos])
				cursorPos++
			}
			moveCursorRight(movedWidth)
		case inputVirtualKeyHome:
			if bufferHasNewline(buffer) {
				cursorPos = lineHomeIndex(buffer, cursorPos)
				redraw()
				continue
			}
			moveCursorLeft(runeSliceWidth(buffer[:cursorPos]))
			cursorPos = 0
		case inputVirtualKeyEnd:
			if bufferHasNewline(buffer) {
				cursorPos = lineEndIndex(buffer, cursorPos)
				redraw()
				continue
			}
			moveCursorRight(widthAfterCursor())
			cursorPos = len(buffer)
		case inputVirtualKeyTab:
			updated, suggestions, handled := rt.completeLine(string(buffer))
			if !handled {
				continue
			}
			buffer = []rune(updated)
			cursorPos = len(buffer)
			historyNav.SyncBuffer(updated)
			if len(suggestions) > 0 {
				rendered := rt.ui.formatCompletionSuggestions(suggestions, string(buffer))
				fmt.Fprint(rt.writer, "\n"+rendered+"\n")
			}
			redraw()
		case inputVirtualKeyUp:
			if bufferHasNewline(buffer) {
				// Multiline buffer: Up moves the cursor between logical lines
				// rather than navigating history.
				moved := false
				for i := 0; i < repeatCount; i++ {
					target := verticalCursorTarget(buffer, cursorPos, -1)
					if target < 0 {
						break
					}
					cursorPos = target
					moved = true
				}
				if moved {
					redraw()
				}
				continue
			}
			updated := string(buffer)
			for i := 0; i < repeatCount; i++ {
				next, ok := historyNav.Previous(updated)
				if !ok {
					break
				}
				updated = next
			}
			buffer = []rune(updated)
			cursorPos = len(buffer)
			redraw()
		case inputVirtualKeyDown:
			if bufferHasNewline(buffer) {
				moved := false
				for i := 0; i < repeatCount; i++ {
					target := verticalCursorTarget(buffer, cursorPos, 1)
					if target < 0 {
						break
					}
					cursorPos = target
					moved = true
				}
				if moved {
					redraw()
				}
				continue
			}
			updated := string(buffer)
			for i := 0; i < repeatCount; i++ {
				next, ok := historyNav.Next(updated)
				if !ok {
					break
				}
				updated = next
			}
			buffer = []rune(updated)
			cursorPos = len(buffer)
			redraw()
		default:
			if event.UnicodeChar == 3 {
				return "", true, io.EOF
			}
			ch := rune(event.UnicodeChar)
			if ch >= 32 && bufferHasNewline(buffer) {
				insertRune(ch, repeatCount)
				historyNav.SyncBuffer(string(buffer))
				redraw()
				continue
			}
			if ch >= 32 {
				beforeLines := currentLineCount()
				typedAtEnd := cursorPos == len(buffer)
				for i := 0; i < repeatCount; i++ {
					buffer = append(buffer, 0)
					copy(buffer[cursorPos+1:], buffer[cursorPos:])
					buffer[cursorPos] = ch
					cursorPos++
				}
				historyNav.SyncBuffer(string(buffer))
				if typedAtEnd {
					inserted := strings.Repeat(string(ch), repeatCount)
					fmt.Fprint(rt.writer, inserted)
					afterLines := currentLineCount()
					if afterLines > 0 {
						prevLines = afterLines - 1
					} else {
						prevLines = 0
					}
				} else if afterLines := currentLineCount(); afterLines == beforeLines {
					cellsAfter := widthAfterCursor()
					inserted := strings.Repeat(string(ch), repeatCount)
					fmt.Fprint(rt.writer, inserted)
					rewriteFromCursor(cellsAfter, 0)
					prevLines = afterLines - 1
				} else {
					redraw()
				}
			}
		}
	}
}

func cancelInteractiveLine(prevLines int) string {
	var out string
	if prevLines > 0 {
		out = fmt.Sprintf("\x1b[%dA", prevLines)
	}
	return out + "\r\x1b[J"
}

func readConsoleKeyEvent(handle syscall.Handle) (keyEventRecord, error) {
	for {
		record, err := readConsoleInputRecord(handle)
		if err != nil {
			return keyEventRecord{}, err
		}
		if record.EventType != keyEventType || record.KeyEvent.KeyDown == 0 {
			continue
		}
		return record.KeyEvent, nil
	}
}

func readConsoleInputRecord(handle syscall.Handle) (inputRecord, error) {
	var record inputRecord
	var read uint32
	r1, _, err := readConsoleInputProc.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&record)),
		1,
		uintptr(unsafe.Pointer(&read)),
	)
	if r1 == 0 {
		return inputRecord{}, err
	}
	if read == 0 {
		return inputRecord{}, io.EOF
	}
	return record, nil
}

// consoleInputHasPendingKey peeks the console input queue without consuming it
// and reports whether a text-bearing key event is already waiting. It is the
// paste-burst signal: when an Enter is read and more printable input (or
// another Enter) is already queued, the Enter is part of a multi-line paste and
// must be captured as a literal newline rather than submitting the buffer.
func consoleInputHasPendingKey(handle syscall.Handle) bool {
	var records [16]inputRecord
	var read uint32
	r1, _, _ := peekConsoleInputProc.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&records[0])),
		uintptr(len(records)),
		uintptr(unsafe.Pointer(&read)),
	)
	if r1 == 0 || read == 0 {
		return false
	}
	return inputRecordsHavePendingKey(records[:read])
}

// inputRecordsHavePendingKey reports whether a peeked slice of console input
// records contains a key-down event representing pasted or typed text: a
// printable Unicode character or another Enter. Key-up events and non-key
// events (focus, mouse, buffer resize) are ignored. It is kept pure so the
// paste-burst classification can be unit-tested without a live console.
func inputRecordsHavePendingKey(records []inputRecord) bool {
	for i := range records {
		rec := records[i]
		if rec.EventType != keyEventType || rec.KeyEvent.KeyDown == 0 {
			continue
		}
		if rec.KeyEvent.UnicodeChar != 0 || rec.KeyEvent.VirtualKeyCode == inputVirtualKeyEnter {
			return true
		}
	}
	return false
}

// enterInsertsNewline reports whether an Enter key event carries a modifier
// that should insert a literal newline instead of submitting the buffer.
func enterInsertsNewline(controlKeyState uint32) bool {
	return controlKeyState&(leftAltPressed|rightAltPressed) != 0
}
