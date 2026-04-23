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

func resolvedDisplayLocale(cfg Config) string {
	if configAutoLocale(cfg) {
		return strings.TrimSpace(getSystemLocale())
	}
	return "en-US"
}

func localePrefersKorean(cfg Config) bool {
	locale := strings.ToLower(strings.TrimSpace(resolvedDisplayLocale(cfg)))
	return strings.HasPrefix(locale, "ko")
}

func localizedText(cfg Config, english string, korean string) string {
	if localePrefersKorean(cfg) {
		return korean
	}
	return english
}

func resolvedFunctionFuzzLocale(cfg Config) string {
	if configFuzzFuncOutputLanguage(cfg) == "english" {
		return "en-US"
	}
	return strings.TrimSpace(getSystemLocale())
}

func functionFuzzPrefersKorean(cfg Config) bool {
	locale := strings.ToLower(strings.TrimSpace(resolvedFunctionFuzzLocale(cfg)))
	return strings.HasPrefix(locale, "ko")
}

func functionFuzzLocalizedText(cfg Config, english string, korean string) string {
	if functionFuzzPrefersKorean(cfg) {
		return korean
	}
	return english
}
