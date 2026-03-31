//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

const shellExecuteOpen = "open"

var (
	shell32DLL        = syscall.NewLazyDLL("shell32.dll")
	shellExecuteWProc = shell32DLL.NewProc("ShellExecuteW")
)

func OpenExternalURL(targetURL string) error {
	actionPtr, err := syscall.UTF16PtrFromString(shellExecuteOpen)
	if err != nil {
		return err
	}
	urlPtr, err := syscall.UTF16PtrFromString(targetURL)
	if err != nil {
		return err
	}

	result, _, callErr := shellExecuteWProc.Call(
		0,
		uintptr(unsafe.Pointer(actionPtr)),
		uintptr(unsafe.Pointer(urlPtr)),
		0,
		0,
		1,
	)
	if result <= 32 {
		if callErr != syscall.Errno(0) {
			return callErr
		}
		return fmt.Errorf("ShellExecuteW failed with code %d", result)
	}
	return nil
}
