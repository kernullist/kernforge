package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundToolModelText(t *testing.T) {
	a := &Agent{Config: Config{SessionDir: t.TempDir()}, Session: NewSession(t.TempDir(), "p", "m", "", "plan")}

	// Small output passes through unchanged.
	if got := a.boundToolModelText("ok", nil); got != "ok" {
		t.Fatalf("small output must pass through, got %q", got)
	}

	big := strings.Repeat("line\n", toolOutputModelMaxLines+500)

	// A tool that bounded its own output (meta flag) is not touched again.
	if got := a.boundToolModelText(big, map[string]any{"output_bounded": true}); got != big {
		t.Fatalf("output_bounded must skip bounding")
	}

	// Oversized output is previewed + spilled with a recovery hint.
	got := a.boundToolModelText(big, nil)
	if len(got) >= len(big) {
		t.Fatalf("oversized output must shrink, got %d >= %d bytes", len(got), len(big))
	}
	if !strings.Contains(got, "lines omitted") {
		t.Fatalf("preview must mark omitted lines")
	}
	if !strings.Contains(got, "full output is saved at") || !strings.Contains(got, "tool-output-spills") {
		t.Fatalf("oversized output must reference the spill file")
	}
}

func TestSafeHeadBytes(t *testing.T) {
	if got := safeHeadBytes("hello", 100); got != "hello" {
		t.Fatalf("under cap unchanged, got %q", got)
	}
	if got := safeHeadBytes("hello", 3); got != "hel" {
		t.Fatalf("ascii cut, got %q", got)
	}
	// "가" is 3 bytes; cutting at 2 must back off to a rune boundary, never split it.
	if got := safeHeadBytes("가나다", 2); !utf8.ValidString(got) {
		t.Fatalf("must not split a multibyte rune, got %q (%d bytes)", got, len(got))
	}
}
