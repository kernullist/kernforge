package main

import (
	"os"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

func getSystemLocale() string {
	if lang := os.Getenv("LANG"); lang != "" {
		parts := strings.Split(lang, ".")
		return parts[0]
	}
	if runtime.GOOS == "windows" {
		kernel32 := syscall.NewLazyDLL("kernel32.dll")
		getUserDefaultLocaleName := kernel32.NewProc("GetUserDefaultLocaleName")
		buf := make([]uint16, 85)
		ret, _, _ := getUserDefaultLocaleName.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if ret != 0 {
			return syscall.UTF16ToString(buf)
		}
	}
	return "en-US"
}
