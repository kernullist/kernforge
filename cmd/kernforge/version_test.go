package main

import (
	"strings"
	"testing"
)

// TestCurrentBuildStampReflectsCommit locks the build stamp surfaced in the
// banner / --help / version so a stale transferred binary is detectable: the
// stamp is never empty, and is either "unstamped" (no VCS info) or prefixed by
// the embedded short commit.
func TestCurrentBuildStampReflectsCommit(t *testing.T) {
	stamp := currentBuildStamp()
	if strings.TrimSpace(stamp) == "" {
		t.Fatal("currentBuildStamp must never be empty")
	}
	commit := strings.TrimSpace(currentBuildIdentity().Commit)
	if commit == "" {
		if stamp != "unstamped" {
			t.Fatalf("no VCS commit should yield \"unstamped\", got %q", stamp)
		}
		return
	}
	short := commit
	if len(short) > 12 {
		short = short[:12]
	}
	if !strings.HasPrefix(stamp, short) {
		t.Fatalf("stamp %q should start with short commit %q", stamp, short)
	}
}
