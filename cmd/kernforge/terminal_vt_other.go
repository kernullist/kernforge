//go:build !windows

package main

func ensureVirtualTerminalProcessing() error {
	return nil
}
