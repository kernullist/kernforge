//go:build !windows

package main

import "fmt"

func OpenTextViewer(path string, data []byte, resultPath string) error {
	_ = data
	_ = resultPath
	return fmt.Errorf("open viewer window is currently supported on Windows only: %s", path)
}

func RunTextViewerWindow(path string, resultPath string) error {
	_ = resultPath
	return fmt.Errorf("internal viewer window is currently supported on Windows only: %s", path)
}
