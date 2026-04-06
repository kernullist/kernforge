//go:build !windows

package main

import "fmt"

func OpenReadOnlyDiffView(title string, subtitle string, diff string) error {
	return fmt.Errorf("read-only diff webview is currently supported on Windows only")
}
