//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

const (
	stdOutputHandle                 = ^uint32(10)
	stdErrorHandle                  = ^uint32(11)
	invalidHandleValue              = ^uintptr(0)
	enableProcessedOutput           = 0x0001
	enableVirtualTerminalProcessing = 0x0004
)

var (
	kernel32TerminalVTDLL = syscall.NewLazyDLL("kernel32.dll")
	getStdHandleVTProc    = kernel32TerminalVTDLL.NewProc("GetStdHandle")
	getConsoleModeVTProc  = kernel32TerminalVTDLL.NewProc("GetConsoleMode")
	setConsoleModeVTProc  = kernel32TerminalVTDLL.NewProc("SetConsoleMode")
)

func ensureVirtualTerminalProcessing() error {
	if err := enableVirtualTerminalProcessingForHandle(stdOutputHandle); err != nil {
		return err
	}
	return enableVirtualTerminalProcessingForHandle(stdErrorHandle)
}

func enableVirtualTerminalProcessingForHandle(stdHandle uint32) error {
	handle, _, _ := getStdHandleVTProc.Call(uintptr(stdHandle))
	if handle == 0 || handle == invalidHandleValue {
		return nil
	}

	var mode uint32
	ok, _, _ := getConsoleModeVTProc.Call(handle, uintptr(unsafe.Pointer(&mode)))
	if ok == 0 {
		return nil
	}

	requested := uint32(enableProcessedOutput | enableVirtualTerminalProcessing)
	if mode&requested == requested {
		return nil
	}

	ok, _, err := setConsoleModeVTProc.Call(handle, uintptr(mode|requested))
	if ok == 0 {
		if err != syscall.Errno(0) {
			return err
		}
		return syscall.EINVAL
	}
	return nil
}
