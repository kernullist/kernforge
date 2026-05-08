//go:build windows

package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var (
	versionDLL                  = syscall.NewLazyDLL("version.dll")
	procGetFileVersionInfoSizeW = versionDLL.NewProc("GetFileVersionInfoSizeW")
	procGetFileVersionInfoW     = versionDLL.NewProc("GetFileVersionInfoW")
	procVerQueryValueW          = versionDLL.NewProc("VerQueryValueW")
	currentPEVersionOnce        sync.Once
	currentPEVersionValue       string
)

func currentExecutablePEVersion() string {
	currentPEVersionOnce.Do(func() {
		exePath, err := os.Executable()
		if err != nil {
			return
		}
		currentPEVersionValue = readPEFileVersion(exePath)
	})
	return currentPEVersionValue
}

func readPEFileVersion(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}

	var handle uint32
	size, _, _ := procGetFileVersionInfoSizeW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&handle)),
	)
	if size == 0 {
		return ""
	}

	data := make([]byte, size)
	ok, _, _ := procGetFileVersionInfoW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		size,
		uintptr(unsafe.Pointer(&data[0])),
	)
	if ok == 0 {
		return ""
	}

	for _, translation := range queryPEVersionTranslations(data) {
		if version := queryPEVersionString(data, `\StringFileInfo\`+translation+`\FileVersion`); version != "" {
			return version
		}
	}
	if version := queryPEVersionString(data, `\StringFileInfo\040904b0\FileVersion`); version != "" {
		return version
	}
	if version := queryPEVersionString(data, `\StringFileInfo\040904e4\FileVersion`); version != "" {
		return version
	}
	return queryPEVersionString(data, `\StringFileInfo\000004b0\FileVersion`)
}

func queryPEVersionTranslations(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	queryPtr, err := syscall.UTF16PtrFromString(`\VarFileInfo\Translation`)
	if err != nil {
		return nil
	}

	var value uintptr
	var valueLen uint32
	ok, _, _ := procVerQueryValueW.Call(
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(unsafe.Pointer(queryPtr)),
		uintptr(unsafe.Pointer(&value)),
		uintptr(unsafe.Pointer(&valueLen)),
	)
	if ok == 0 || value == 0 || valueLen < 4 {
		return nil
	}

	words := unsafe.Slice((*uint16)(unsafe.Pointer(value)), valueLen/2)
	translations := make([]string, 0, len(words)/2)
	for i := 0; i+1 < len(words); i += 2 {
		translations = append(translations, fmt.Sprintf("%04x%04x", words[i], words[i+1]))
	}
	return translations
}

func queryPEVersionString(data []byte, query string) string {
	if len(data) == 0 {
		return ""
	}
	queryPtr, err := syscall.UTF16PtrFromString(query)
	if err != nil {
		return ""
	}

	var value uintptr
	var valueLen uint32
	ok, _, _ := procVerQueryValueW.Call(
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(unsafe.Pointer(queryPtr)),
		uintptr(unsafe.Pointer(&value)),
		uintptr(unsafe.Pointer(&valueLen)),
	)
	if ok == 0 || value == 0 || valueLen == 0 {
		return ""
	}

	chars := unsafe.Slice((*uint16)(unsafe.Pointer(value)), valueLen)
	if len(chars) > 0 && chars[len(chars)-1] == 0 {
		chars = chars[:len(chars)-1]
	}
	return strings.TrimSpace(syscall.UTF16ToString(chars))
}
