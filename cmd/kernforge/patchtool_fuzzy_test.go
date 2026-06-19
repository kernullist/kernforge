package main

import (
	"errors"
	"strings"
	"testing"
)

// applyTestPatch parses a patch document and applies its single update op's
// hunks to content, returning the rewritten content.
func applyTestPatch(t *testing.T, content, patch string) (string, error) {
	t.Helper()
	doc, err := parsePatchDocument(patch)
	if err != nil {
		t.Fatalf("parsePatchDocument: %v", err)
	}
	if len(doc.ops) != 1 {
		t.Fatalf("expected exactly one patch op, got %d", len(doc.ops))
	}
	return applyPatchHunks(content, doc.ops[0].hunks)
}

func TestApplyPatchFuzzyToleratesTrailingWhitespaceDrift(t *testing.T) {
	// File has no trailing whitespace; the patch context carries a stray
	// trailing space on the context lines (as if copied from a review excerpt).
	content := "package main\n\nfunc main() {\n\tx := 1\n}\n"
	patch := "*** Begin Patch\n" +
		"*** Update File: main.go\n" +
		"@@\n" +
		" func main() { \n" +
		"-\tx := 1\n" +
		"+\tx := 2\n" +
		" } \n" +
		"*** End Patch\n"

	got, err := applyTestPatch(t, content, patch)
	if err != nil {
		t.Fatalf("expected trailing-whitespace drift to apply, got error: %v", err)
	}
	want := "package main\n\nfunc main() {\n\tx := 2\n}\n"
	if got != want {
		t.Fatalf("unexpected result.\n got: %q\nwant: %q", got, want)
	}
	// The unchanged context line must keep the file's real form (no stray
	// trailing space leaked in from the patch).
	if strings.Contains(got, "func main() { \n") {
		t.Fatalf("patch's trailing whitespace leaked into context line: %q", got)
	}
}

func TestApplyPatchFuzzyToleratesIndentationDriftAndPreservesFileIndent(t *testing.T) {
	// File is tab-indented; the patch context uses spaces for the same lines.
	content := "func f() {\n\tif ok {\n\t\tdoThing()\n\t}\n}\n"
	patch := "*** Begin Patch\n" +
		"*** Update File: main.go\n" +
		"@@\n" +
		"    if ok {\n" +
		"-        doThing()\n" +
		"+        doOther()\n" +
		"    }\n" +
		"*** End Patch\n"

	got, err := applyTestPatch(t, content, patch)
	if err != nil {
		t.Fatalf("expected indentation drift to apply, got error: %v", err)
	}
	// The unchanged context lines must keep the file's tab indentation; only the
	// added line uses the patch's own indentation.
	want := "func f() {\n\tif ok {\n        doOther()\n\t}\n}\n"
	if got != want {
		t.Fatalf("unexpected result.\n got: %q\nwant: %q", got, want)
	}
	if strings.Contains(got, "    if ok {") {
		t.Fatalf("context line should keep the file's tab indent, got: %q", got)
	}
}

func TestApplyPatchFuzzyRefusesAmbiguousMatch(t *testing.T) {
	// Two blocks match the context once trailing whitespace is ignored, so the
	// fuzzy fallback must refuse rather than guess a location.
	content := "a := 1\nflag := true\nb := 2\nflag := true\nc := 3\n"
	patch := "*** Begin Patch\n" +
		"*** Update File: main.go\n" +
		"@@\n" +
		"-flag := true \n" +
		"+flag := false\n" +
		"*** End Patch\n"

	_, err := applyTestPatch(t, content, patch)
	if err == nil {
		t.Fatalf("expected ambiguous fuzzy context to fail, but it applied")
	}
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch, got %v", err)
	}
}

func TestApplyPatchMismatchDiagnosticIncludesCurrentContentWindow(t *testing.T) {
	// The patch context matches nothing in the file (stale/structurally wrong),
	// so the diagnostic must surface the file's actual current content so the
	// model can re-anchor without a separate read.
	content := "package main\n\nfunc Renamed() {\n\treturn\n}\n"
	patch := "*** Begin Patch\n" +
		"*** Update File: main.go\n" +
		"@@\n" +
		" func Original() {\n" +
		"-\treturn\n" +
		"+\treturn nil\n" +
		" }\n" +
		"*** End Patch\n"

	_, err := applyTestPatch(t, content, patch)
	if err == nil {
		t.Fatalf("expected stale context to fail")
	}
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch, got %v", err)
	}
	if !strings.Contains(err.Error(), "current file content") {
		t.Fatalf("expected diagnostic to include the current content window, got: %v", err)
	}
	if !strings.Contains(err.Error(), "func Renamed()") {
		t.Fatalf("expected diagnostic to show the file's actual line, got: %v", err)
	}
}
