//go:build !windows

package main

import "fmt"

func RunDiffPreviewWindow(previewPath, resultPath string) error {
	_ = previewPath
	_ = resultPath
	return fmt.Errorf("diff preview window is currently supported on Windows only")
}

func runDiffPreviewProcess(previewPath, resultPath string) error {
	_ = previewPath
	_ = resultPath
	return fmt.Errorf("diff preview window is currently supported on Windows only")
}
