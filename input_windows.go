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
	inputVKBack          = 0x08
	inputVKTab           = 0x09
	inputVKReturn        = 0x0D
	inputVKEscape        = 0x1B
	inputVKLeft          = 0x25
	inputVKUp            = 0x26
	inputVKRight         = 0x27
	inputVKDown          = 0x28
	inputVKDelete        = 0x2E
	inputVKHome          = 0x24
	inputVKEnd           = 0x23
)

var (
	kernel32DLL                    = syscall.NewLazyDLL("kernel32.dll")
	getConsoleModeProc             = kernel32DLL.NewProc("GetConsoleMode")
	setConsoleModeProc             = kernel32DLL.NewProc("SetConsoleMode")
	readConsoleInputProc           = kernel32DLL.NewProc("ReadConsoleInputW")
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

func (rt *runtimeState) readInteractiveLine(prompt string, initial string, historyNav *inputHistoryNavigator) (string, bool, error) {
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
	prevLines := 0
	currentLineCount := func() int {
		current := prompt + string(buffer)
		termW := terminalWidth()
		if termW <= 0 {
			return 1
		}
		width := visibleLen(current)
		if width <= 0 {
			return 1
		}
		return ((width - 1) / termW) + 1
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
			fmt.Fprintf(rt.writer, "\x1b[%dD", count)
		}
	}
	moveCursorRight := func(count int) {
		if count > 0 {
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
		case inputVKReturn:
			fmt.Fprint(rt.writer, "\n")
			return string(buffer), true, nil
		case inputVKEscape:
			if rt.shouldIgnorePromptEscape() {
				continue
			}
			fmt.Fprint(rt.writer, cancelInteractiveLine(prevLines))
			return "", true, ErrPromptCanceled
		case inputVKBack:
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
		case inputVKDelete:
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
		case inputVKLeft:
			moved := 0
			movedWidth := 0
			for i := 0; i < repeatCount && cursorPos > 0; i++ {
				cursorPos--
				moved++
				movedWidth += runeWidth(buffer[cursorPos])
			}
			_ = moved
			moveCursorLeft(movedWidth)
		case inputVKRight:
			movedWidth := 0
			for i := 0; i < repeatCount && cursorPos < len(buffer); i++ {
				movedWidth += runeWidth(buffer[cursorPos])
				cursorPos++
			}
			moveCursorRight(movedWidth)
		case inputVKHome:
			moveCursorLeft(runeSliceWidth(buffer[:cursorPos]))
			cursorPos = 0
		case inputVKEnd:
			moveCursorRight(widthAfterCursor())
			cursorPos = len(buffer)
		case inputVKTab:
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
		case inputVKUp:
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
		case inputVKDown:
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
