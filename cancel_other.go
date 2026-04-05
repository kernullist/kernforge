//go:build !windows

package main

import "time"

func startEscapeWatcher(cancel func(), shouldCancel func() bool, confirmCancel func() bool) func() {
	_ = cancel
	_ = shouldCancel
	_ = confirmCancel
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
