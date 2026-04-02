//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
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
	kernel32DLL                      = syscall.NewLazyDLL("kernel32.dll")
	getConsoleModeProc               = kernel32DLL.NewProc("GetConsoleMode")
	setConsoleModeProc               = kernel32DLL.NewProc("SetConsoleMode")
	readConsoleInputProc             = kernel32DLL.NewProc("ReadConsoleInputW")
	getConsoleScreenBufferInfoProc   = kernel32DLL.NewProc("GetConsoleScreenBufferInfo")
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
		charsAfter := len(buffer) - cursorPos
		if charsAfter > 0 {
			fmt.Fprintf(rt.writer, "\x1b[%dD", charsAfter)
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
		case inputVKReturn:
			fmt.Fprint(rt.writer, "\n")
			return string(buffer), true, nil
		case inputVKEscape:
			fmt.Fprint(rt.writer, cancelInteractiveLine(prevLines))
			return "", true, ErrPromptCanceled
		case inputVKBack:
			for i := 0; i < repeatCount && cursorPos > 0; i++ {
				buffer = append(buffer[:cursorPos-1], buffer[cursorPos:]...)
				cursorPos--
			}
			historyNav.SyncBuffer(string(buffer))
			redraw()
		case inputVKDelete:
			for i := 0; i < repeatCount && cursorPos < len(buffer); i++ {
				buffer = append(buffer[:cursorPos], buffer[cursorPos+1:]...)
			}
			historyNav.SyncBuffer(string(buffer))
			redraw()
		case inputVKLeft:
			for i := 0; i < repeatCount && cursorPos > 0; i++ {
				cursorPos--
			}
			redraw()
		case inputVKRight:
			for i := 0; i < repeatCount && cursorPos < len(buffer); i++ {
				cursorPos++
			}
			redraw()
		case inputVKHome:
			cursorPos = 0
			redraw()
		case inputVKEnd:
			cursorPos = len(buffer)
			redraw()
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
				for i := 0; i < repeatCount; i++ {
					buffer = append(buffer, 0)
					copy(buffer[cursorPos+1:], buffer[cursorPos:])
					buffer[cursorPos] = ch
					cursorPos++
				}
				historyNav.SyncBuffer(string(buffer))
				redraw()
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
	var record inputRecord
	var read uint32
	for {
		r1, _, err := readConsoleInputProc.Call(
			uintptr(handle),
			uintptr(unsafe.Pointer(&record)),
			1,
			uintptr(unsafe.Pointer(&read)),
		)
		if r1 == 0 {
			return keyEventRecord{}, err
		}
		if read == 0 {
			return keyEventRecord{}, io.EOF
		}
		if record.EventType != keyEventType || record.KeyEvent.KeyDown == 0 {
			continue
		}
		return record.KeyEvent, nil
	}
}
