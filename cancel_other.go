//go:build !windows

package main

func startEscapeWatcher(cancel func()) func() {
	_ = cancel
	return func() {}
}
