//go:build !windows

package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

func OpenExternalURL(targetURL string) error {
	var name string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	default:
		name = "xdg-open"
	}
	cmd := exec.Command(name, targetURL)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %s: %w", targetURL, err)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return nil
}
