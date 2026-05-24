package main

import "testing"

func TestEnsureVirtualTerminalProcessingIsSafeForCurrentOutput(t *testing.T) {
	if err := ensureVirtualTerminalProcessing(); err != nil {
		t.Fatalf("ensureVirtualTerminalProcessing() returned error: %v", err)
	}
}
