//go:build !windows

package main

func startEscapeWatcher(cancel func(), shouldCancel func() bool) func() {
	_ = cancel
	_ = shouldCancel
	return func() {}
}
