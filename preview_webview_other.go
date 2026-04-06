//go:build !windows

package main

import "fmt"

func OpenDiffPreviewWebView(preview EditPreview) (bool, error) {
	return false, fmt.Errorf("internal WebView preview is currently supported on Windows only")
}
