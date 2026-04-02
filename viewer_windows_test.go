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

func TestViewerFindPanelLayout(t *testing.T) {
	editW, prevW, nextW := viewerFindPanelLayout(420, 8, 84)
	if editW != 180 {
		t.Fatalf("expected remaining edit width, got %d", editW)
	}
	if prevW != 84 || nextW != 84 {
		t.Fatalf("expected fixed navigation widths, got prev=%d next=%d", prevW, nextW)
	}

	editW, prevW, nextW = viewerFindPanelLayout(160, 8, 84)
	if editW != 0 || prevW != 84 || nextW != 84 {
		t.Fatalf("expected narrow panel to shrink edit only, got edit=%d prev=%d next=%d", editW, prevW, nextW)
	}
}

func TestViewerFindAllMatches(t *testing.T) {
	got := viewerFindAllMatches("alpha beta alpha", "alpha")
	if len(got) != 2 {
		t.Fatalf("expected two matches, got %d", len(got))
	}
	if got[0] != 0 || got[1] != 11 {
		t.Fatalf("unexpected match offsets: %v", got)
	}
}

func TestViewerSelectionFromOffsets(t *testing.T) {
	selection, ok := viewerSelectionFromOffsets("1 | alpha\r\n2 | beta\r\n3 | gamma", 10, 18)
	if !ok {
		t.Fatal("expected selection")
	}
	if selection.StartLine != 1 || selection.EndLine != 2 {
		t.Fatalf("unexpected selection range: %+v", selection)
	}
}
