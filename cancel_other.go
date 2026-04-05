//go:build !windows

package main

import "time"

func startEscapeWatcher(cancel func(), shouldCancel func() bool) func() {
	_ = cancel
	_ = shouldCancel
	return func() {}
}

func stabilizeConsoleAfterRequestCancel() {
}

func isEscapePhysicallyPressed() bool {
	return false
}

func waitForEscapeRelease(timeout time.Duration) {
	_ = timeout
}
