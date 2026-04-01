//go:build windows

package main

import (
	"sync"
	"syscall"
	"time"
)

const vkEscape = 0x1B

var (
	user32CancelDLL      = syscall.NewLazyDLL("user32.dll")
	getAsyncKeyStateProc = user32CancelDLL.NewProc("GetAsyncKeyState")
)

func startEscapeWatcher(cancel func(), shouldCancel func() bool) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once

	go func() {
		defer once.Do(func() { close(done) })

		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		wasDown := false
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				state, _, _ := getAsyncKeyStateProc.Call(vkEscape)
				down := (uint16(state) & 0x8000) != 0
				if down && !wasDown {
					if shouldCancel != nil && !shouldCancel() {
						wasDown = down
						continue
					}
					cancel()
					return
				}
				wasDown = down
			}
		}
	}()

	return func() {
		close(stop)
		<-done
	}
}
