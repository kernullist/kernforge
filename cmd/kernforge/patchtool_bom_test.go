package main

import (
	"errors"
	"strings"
	"testing"
)

// A UTF-8 BOM is invisible in read_file output, so no model can reproduce it
// in patch context. These tests pin the contract that matching ignores a
// leading BOM while the applied result preserves the file's original marker.

func TestApplyPatchMatchesLineOneOfBOMFile(t *testing.T) {
	content := utf8BOMPrefix + "using System;\n\nnamespace RegGit.Core\n{\n}\n"
	patch := "*** Begin Patch\n" +
		"*** Update File: Service.cs\n" +
		"@@\n" +
		" using System;\n" +
		"+using System.Linq;\n" +
		"\n" +
		" namespace RegGit.Core\n" +
		"*** End Patch\n"

	got, err := applyTestPatch(t, content, patch)
	if err != nil {
		t.Fatalf("expected line-1 hunk on BOM file to apply, got error: %v", err)
	}
	want := utf8BOMPrefix + "using System;\nusing System.Linq;\n\nnamespace RegGit.Core\n{\n}\n"
	if got != want {
		t.Fatalf("unexpected result.\n got: %q\nwant: %q", got, want)
	}
}

func TestApplyPatchWholeFileRewriteOnBOMFile(t *testing.T) {
	// Mirrors the failure shape seen in the field: a single numeric-header hunk
	// that lists every existing line as context/deletion starting at line 1.
	content := utf8BOMPrefix + "using System;\n\nnamespace App\n{\n}\n"
	patch := "*** Begin Patch\n" +
		"*** Update File: Service.cs\n" +
		"@@ -1,5 +1,2 @@\n" +
		"-using System;\n" +
		"-\n" +
		"-namespace App\n" +
		"-{\n" +
		"-}\n" +
		"+using System.Text;\n" +
		"+namespace App.Core { }\n" +
		"*** End Patch\n"

	got, err := applyTestPatch(t, content, patch)
	if err != nil {
		t.Fatalf("expected whole-file rewrite on BOM file to apply, got error: %v", err)
	}
	want := utf8BOMPrefix + "using System.Text;\nnamespace App.Core { }\n"
	if got != want {
		t.Fatalf("unexpected result.\n got: %q\nwant: %q", got, want)
	}
}

func TestApplyPatchBOMFilePreservesCRLF(t *testing.T) {
	content := utf8BOMPrefix + "using System;\r\n\r\nnamespace App\r\n{\r\n}\r\n"
	patch := "*** Begin Patch\n" +
		"*** Update File: Service.cs\n" +
		"@@\n" +
		"-using System;\n" +
		"+using System.IO;\n" +
		"*** End Patch\n"

	got, err := applyTestPatch(t, content, patch)
	if err != nil {
		t.Fatalf("expected BOM+CRLF file to apply, got error: %v", err)
	}
	want := utf8BOMPrefix + "using System.IO;\r\n\r\nnamespace App\r\n{\r\n}\r\n"
	if got != want {
		t.Fatalf("unexpected result.\n got: %q\nwant: %q", got, want)
	}
}

func TestApplyPatchBOMFileStillRejectsWrongContext(t *testing.T) {
	// BOM tolerance must not loosen genuine content matching: stale context on
	// a BOM file still fails with an edit target mismatch.
	content := utf8BOMPrefix + "using System;\n\nnamespace App\n{\n}\n"
	patch := "*** Begin Patch\n" +
		"*** Update File: Service.cs\n" +
		"@@\n" +
		" using System.Collections.Generic;\n" +
		"-namespace App\n" +
		"+namespace App.Core\n" +
		"*** End Patch\n"

	_, err := applyTestPatch(t, content, patch)
	if err == nil {
		t.Fatalf("expected stale context on BOM file to fail")
	}
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch, got %v", err)
	}
}

func TestPatchLineComparatorsIgnoreLeadingBOM(t *testing.T) {
	if !patchLinesEqualTrailing(utf8BOMPrefix+"using System;", "using System;") {
		t.Fatalf("patchLinesEqualTrailing should ignore a leading BOM")
	}
	if !patchLinesEqualTrimmed(utf8BOMPrefix+"using System;", "  using System;") {
		t.Fatalf("patchLinesEqualTrimmed should ignore a leading BOM")
	}
	if patchLinesEqualTrailing(utf8BOMPrefix+"using System;", "using System.IO;") {
		t.Fatalf("BOM tolerance must not equate different content")
	}
}

func TestResolveReplaceTargetFuzzyBOMFirstLineKeepsMarker(t *testing.T) {
	// The search text drifts by trailing whitespace on the first line, so the
	// exact substring rung fails and the fuzzy rung must both match despite the
	// BOM and return a span that excludes it, keeping the marker in the file
	// after the caller's replacement.
	content := utf8BOMPrefix + "using System;\nnamespace App\n{\n}\n"
	search := "using System; \nnamespace App"

	target, count := resolveReplaceTarget(content, search)
	if count != 1 {
		t.Fatalf("expected exactly one fuzzy match, got %d (target %q)", count, target)
	}
	if strings.HasPrefix(target, utf8BOMPrefix) {
		t.Fatalf("resolved span must not swallow the BOM: %q", target)
	}
	if target != "using System;\nnamespace App" {
		t.Fatalf("unexpected resolved span: %q", target)
	}
	replaced := strings.Replace(content, target, "using System.IO;\nnamespace App.Core", 1)
	if !strings.HasPrefix(replaced, utf8BOMPrefix+"using System.IO;") {
		t.Fatalf("BOM must survive replacement, got: %q", replaced)
	}
}
