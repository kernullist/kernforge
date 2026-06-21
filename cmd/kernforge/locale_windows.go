//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// platformSystemLocale returns the user's default locale name on Windows via
// GetUserDefaultLocaleName, or "" when the query fails.
func platformSystemLocale() string {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getUserDefaultLocaleName := kernel32.NewProc("GetUserDefaultLocaleName")
	buf := make([]uint16, 85)
	ret, _, _ := getUserDefaultLocaleName.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if ret != 0 {
		return syscall.UTF16ToString(buf)
	}
	return ""
}
