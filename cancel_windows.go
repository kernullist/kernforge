//go:build windows

package main

import (
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const vkEscape = 0x1B

var (
	user32CancelDLL                   = syscall.NewLazyDLL("user32.dll")
	kernel32CancelDLL                 = syscall.NewLazyDLL("kernel32.dll")
	getAsyncKeyStateProc              = user32CancelDLL.NewProc("GetAsyncKeyState")
	getForegroundWndProc              = user32CancelDLL.NewProc("GetForegroundWindow")
	getConsoleWndProc                 = kernel32CancelDLL.NewProc("GetConsoleWindow")
	getConsoleModeCancelProc          = kernel32CancelDLL.NewProc("GetConsoleMode")
	flushConsoleInputBufferProc       = kernel32CancelDLL.NewProc("FlushConsoleInputBuffer")
	getNumberOfConsoleInputEventsProc = kernel32CancelDLL.NewProc("GetNumberOfConsoleInputEvents")
	getWindowPIDProc                  = user32CancelDLL.NewProc("GetWindowThreadProcessId")
	createSnapshotProc                = kernel32CancelDLL.NewProc("CreateToolhelp32Snapshot")
	process32FirstProc                = kernel32CancelDLL.NewProc("Process32FirstW")
	process32NextProc                 = kernel32CancelDLL.NewProc("Process32NextW")
	closeHandleProc                   = kernel32CancelDLL.NewProc("CloseHandle")
)

const th32csSnapProcess = 0x00000002

type processEntry32 struct {
	Size            uint32
	CntUsage        uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	CntThreads      uint32
	ParentProcessID uint32
	PcPriClassBase  int32
	Flags           uint32
	ExeFile         [260]uint16
}

func startEscapeWatcher(cancel func(), shouldCancel func() bool) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once

	go func() {
		defer once.Do(func() { close(done) })

		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		wasDown := false
		var lastBlockedEscape time.Time
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if pollConsoleEscape(cancel, shouldCancel) {
					return
				}
				state, _, _ := getAsyncKeyStateProc.Call(vkEscape)
				down := isAsyncKeyPressed(state)
				if down && !wasDown {
					hasForegroundTarget := isRequestCancelForegroundTarget()
					if shouldCancelOnEscape(hasForegroundTarget, shouldCancel) {
						flushPendingConsoleInput()
						cancel()
						return
					}
					repeatedPress := !lastBlockedEscape.IsZero() && time.Since(lastBlockedEscape) <= 1500*time.Millisecond
					if shouldCancelOnRepeatedEscape(hasForegroundTarget, repeatedPress, shouldCancel) {
						flushPendingConsoleInput()
						cancel()
						return
					}
					lastBlockedEscape = time.Now()
				}
				wasDown = down
			}
		}
	}()

	return func() {
		close(stop)
		<-done
	}
}

func pollConsoleEscape(cancel func(), shouldCancel func() bool) bool {
	handle := syscall.Handle(os.Stdin.Fd())
	var mode uint32
	r1, _, _ := getConsoleModeCancelProc.Call(uintptr(handle), uintptr(unsafe.Pointer(&mode)))
	if r1 == 0 {
		return false
	}

	var eventCount uint32
	r1, _, _ = getNumberOfConsoleInputEventsProc.Call(uintptr(handle), uintptr(unsafe.Pointer(&eventCount)))
	if r1 == 0 || eventCount == 0 {
		return false
	}

	remaining := eventCount
	for remaining > 0 {
		record, err := readConsoleInputRecord(handle)
		if err != nil {
			return false
		}
		remaining--
		if record.EventType != keyEventType || record.KeyEvent.KeyDown == 0 {
			continue
		}
		event := record.KeyEvent
		if event.VirtualKeyCode != vkEscape {
			continue
		}
		if !shouldCancelOnEscape(true, shouldCancel) {
			continue
		}
		flushPendingConsoleInput()
		cancel()
		return true
	}

	return false
}

func flushPendingConsoleInput() {
	handle := syscall.Handle(os.Stdin.Fd())
	flushConsoleInputBufferProc.Call(uintptr(handle))
}

func stabilizeConsoleAfterRequestCancel() {
	waitForEscapeRelease(500 * time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	flushPendingConsoleInput()
}

func isEscapePhysicallyPressed() bool {
	state, _, _ := getAsyncKeyStateProc.Call(vkEscape)
	return isAsyncKeyPressed(state)
}

func waitForEscapeRelease(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for isEscapePhysicallyPressed() {
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func isRequestCancelForegroundTarget() bool {
	foregroundWnd, _, _ := getForegroundWndProc.Call()
	if foregroundWnd == 0 {
		return false
	}

	consoleWnd, _, _ := getConsoleWndProc.Call()
	if consoleWnd != 0 {
		return foregroundWnd == consoleWnd
	}

	var pid uint32
	getWindowPIDProc.Call(foregroundWnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return false
	}

	currentPID := uint32(os.Getpid())
	if pid == currentPID {
		return true
	}

	return isPIDInParentChain(pid, currentPID, lookupParentPID)
}

func lookupParentPID(pid uint32) (uint32, bool) {
	snapshot, _, err := createSnapshotProc.Call(th32csSnapProcess, 0)
	if snapshot == uintptr(^uintptr(0)) {
		_ = err
		return 0, false
	}
	defer closeHandleProc.Call(snapshot)

	var entry processEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	ok, _, _ := process32FirstProc.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	if ok == 0 {
		return 0, false
	}

	for {
		if entry.ProcessID == pid {
			return entry.ParentProcessID, true
		}

		ok, _, _ = process32NextProc.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
		if ok == 0 {
			break
		}
	}

	return 0, false
}
