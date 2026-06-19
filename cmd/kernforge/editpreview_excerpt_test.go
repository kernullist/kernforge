package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildChangedAfterExcerptKeepsEveryChangedRegion(t *testing.T) {
	// A 100-line file changed in three places: near the top, the middle, and
	// the bottom. The old single-block excerpt spanned the first-to-last change
	// and lost the middle to head/tail truncation; the multi-region excerpt must
	// keep all three changes while collapsing the unchanged spans between them.
	var beforeLines, afterLines []string
	for i := 1; i <= 100; i++ {
		beforeLines = append(beforeLines, fmt.Sprintf("line %d", i))
		afterLines = append(afterLines, fmt.Sprintf("line %d", i))
	}
	afterLines[1] = "line 2 CHANGED-TOP"
	afterLines[49] = "line 50 CHANGED-MID"
	afterLines[98] = "line 99 CHANGED-BOT"

	before := strings.Join(beforeLines, "\n") + "\n"
	after := strings.Join(afterLines, "\n") + "\n"

	excerpt := buildChangedAfterExcerpt("app.py", before, after, 12000)

	for _, needle := range []string{
		"CHANGED-TOP",
		"CHANGED-MID", // the middle change the old window-then-truncate dropped
		"CHANGED-BOT",
	} {
		if !strings.Contains(excerpt, needle) {
			t.Fatalf("expected excerpt to include %q, got:\n%s", needle, excerpt)
		}
	}
	if !strings.Contains(excerpt, "changed region(s)") {
		t.Fatalf("expected multi-region header, got:\n%s", excerpt)
	}
	if !strings.Contains(excerpt, "unchanged line(s)") {
		t.Fatalf("expected collapsed-gap markers between regions, got:\n%s", excerpt)
	}
	// Far-away unchanged lines must be collapsed, not included verbatim.
	if strings.Contains(excerpt, "line 30") || strings.Contains(excerpt, "line 75") {
		t.Fatalf("expected unchanged middle lines to be collapsed, got:\n%s", excerpt)
	}
}

func TestBuildChangedAfterExcerptEmptyForNoChange(t *testing.T) {
	content := "a\nb\nc\n"
	if got := buildChangedAfterExcerpt("x.go", content, content, 12000); got != "" {
		t.Fatalf("expected empty excerpt when nothing changed, got %q", got)
	}
}

func TestChangedNewLineRegionsMergesNearbyChanges(t *testing.T) {
	old := []string{"a", "b", "c", "d", "e", "f"}
	// Change index 1 and index 3; with ctx=4 they overlap and must merge into
	// a single region rather than two.
	updated := []string{"a", "B", "c", "D", "e", "f"}
	regions := changedNewLineRegions(old, updated, 4)
	if len(regions) != 1 {
		t.Fatalf("expected nearby changes to merge into one region, got %d: %+v", len(regions), regions)
	}
}
