package main

import (
	"strings"
	"testing"
)

// TestSummarizeProposedEditDiffRevealsScope locks the pre-write scope summary:
// a change that adds unrelated subsystems (subprocess/threading) to a small
// request must surface those imports, the new definitions, and per-file line
// deltas so the user can catch the over-scope before approving.
func TestSummarizeProposedEditDiffRevealsScope(t *testing.T) {
	before := strings.Join([]string{
		"import json",
		"import os",
		"import hashlib",
		"DEFAULT_POLICY = 'tvp'",
	}, "\n")
	after := strings.Join([]string{
		"import json",
		"import os",
		"import base64",
		"import subprocess",
		"import threading",
		"",
		"def load_env_file(path):",
		"    return None",
	}, "\n")

	summary := summarizeProposedEditDiff(buildEditPreview("app.py", before, after))
	if summary == "" {
		t.Fatal("expected a non-empty change summary")
	}
	for _, needle := range []string{
		"Change summary",
		"app.py: +",
		"new imports:",
		"base64",
		"subprocess",
		"threading",
		"new definitions:",
		"load_env_file",
	} {
		if !strings.Contains(summary, needle) {
			t.Fatalf("summary missing %q, got:\n%s", needle, summary)
		}
	}
}

// TestSummarizeProposedEditDiffEmptyForNoDiff keeps the summary absent when there
// is nothing to summarize (so it never injects noise into the preview).
func TestSummarizeProposedEditDiffEmptyForNoDiff(t *testing.T) {
	if got := summarizeProposedEditDiff(""); got != "" {
		t.Fatalf("expected empty summary for empty diff, got %q", got)
	}
	if got := summarizeProposedEditDiff("   \n  "); got != "" {
		t.Fatalf("expected empty summary for blank diff, got %q", got)
	}
}

// TestSummarizeProposedEditDiffParsesUnifiedDiff confirms the summary also works
// on a standard unified diff (no line-number gutter).
func TestSummarizeProposedEditDiffParsesUnifiedDiff(t *testing.T) {
	diff := strings.Join([]string{
		"--- a/app.py",
		"+++ b/app.py",
		"@@ -1,2 +1,3 @@",
		" import os",
		"-import hashlib",
		"+import subprocess",
		"+def run_sync():",
	}, "\n")
	summary := summarizeProposedEditDiff(diff)
	for _, needle := range []string{"app.py", "new imports:", "subprocess", "new definitions:", "run_sync"} {
		if !strings.Contains(summary, needle) {
			t.Fatalf("unified-diff summary missing %q, got:\n%s", needle, summary)
		}
	}
}
