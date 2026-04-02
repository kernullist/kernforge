//go:build windows

package main

import "testing"

func TestCancelInteractiveLineWithoutWrappedLines(t *testing.T) {
	got := cancelInteractiveLine(0)
	want := "\r\x1b[J"
	if got != want {
		t.Fatalf("cancelInteractiveLine(0) = %q, want %q", got, want)
	}
}

func TestCancelInteractiveLineWithWrappedLines(t *testing.T) {
	got := cancelInteractiveLine(2)
	want := "\x1b[2A\r\x1b[J"
	if got != want {
		t.Fatalf("cancelInteractiveLine(2) = %q, want %q", got, want)
	}
}
