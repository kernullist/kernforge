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
	user32CancelDLL      = syscall.NewLazyDLL("user32.dll")
	kernel32CancelDLL    = syscall.NewLazyDLL("kernel32.dll")
	getAsyncKeyStateProc = user32CancelDLL.NewProc("GetAsyncKeyState")
	getForegroundWndProc = user32CancelDLL.NewProc("GetForegroundWindow")
	getConsoleWndProc    = kernel32CancelDLL.NewProc("GetConsoleWindow")
	getWindowPIDProc     = user32CancelDLL.NewProc("GetWindowThreadProcessId")
	createSnapshotProc   = kernel32CancelDLL.NewProc("CreateToolhelp32Snapshot")
	process32FirstProc   = kernel32CancelDLL.NewProc("Process32FirstW")
	process32NextProc    = kernel32CancelDLL.NewProc("Process32NextW")
	closeHandleProc      = kernel32CancelDLL.NewProc("CloseHandle")
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
				state, _, _ := getAsyncKeyStateProc.Call(vkEscape)
				down := (uint16(state) & 0x8000) != 0
				if down && !wasDown {
					hasForegroundTarget := isRequestCancelForegroundTarget()
					if shouldCancelOnEscape(hasForegroundTarget, shouldCancel) {
						cancel()
						return
					}
					repeatedPress := !lastBlockedEscape.IsZero() && time.Since(lastBlockedEscape) <= 1500*time.Millisecond
					if shouldCancelOnRepeatedEscape(hasForegroundTarget, repeatedPress, shouldCancel) {
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
