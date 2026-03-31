//go:build windows

package main

import (
	"strings"
	"testing"
)

func TestFormatViewerText(t *testing.T) {
	got := formatViewerText("alpha\nbeta")
	want := "1 | alpha\r\n2 | beta"
	if got != want {
		t.Fatalf("formatViewerText() = %q, want %q", got, want)
	}
}

func TestViewerSelectionStatusTextNoSelection(t *testing.T) {
	got := viewerSelectionStatusText(248, ViewerSelection{}, false)
	if !strings.Contains(got, "No lines selected yet.") {
		t.Fatalf("expected no-selection text, got %q", got)
	}
	if !strings.Contains(got, "248 lines loaded.") {
		t.Fatalf("expected line count in status, got %q", got)
	}
}

func TestViewerSelectionStatusTextRange(t *testing.T) {
	got := viewerSelectionStatusText(248, ViewerSelection{
		FilePath:  "main.go",
		StartLine: 12,
		EndLine:   18,
	}, true)
	want := "Selected lines 12-18 (7 lines). Close the window to use this range in the next prompt."
	if got != want {
		t.Fatalf("viewerSelectionStatusText() = %q, want %q", got, want)
	}
}

func TestCompactViewerPath(t *testing.T) {
	got := compactViewerPath(`F:\kernullist\kernforge\internal\auth\service.go`, 30)
	if len(got) > 30 {
		t.Fatalf("expected compacted path length <= 30, got %d (%q)", len(got), got)
	}
	if !strings.Contains(got, "...") {
		t.Fatalf("expected compacted path to contain ellipsis, got %q", got)
	}
	if !strings.HasSuffix(got, `service.go`) {
		t.Fatalf("expected compacted path to preserve suffix, got %q", got)
	}
}
