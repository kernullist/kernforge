package main

import "testing"

// An exact match must always win and be reported verbatim.
func TestResolveReplaceTargetPrefersExact(t *testing.T) {
	content := "a\n    foo()\nb\n"
	got, n := resolveReplaceTarget(content, "    foo()")
	if n != 1 || got != "    foo()" {
		t.Fatalf("exact: got %q n=%d, want %q n=1", got, n, "    foo()")
	}
}

// Indentation drift (model used spaces, file uses tabs) resolves to the file's
// real line so the replacement preserves the actual whitespace.
func TestFuzzyReplaceTargetToleratesIndentDrift(t *testing.T) {
	content := "func x() {\n\t\treturn 1\n}\n"
	got, ok := fuzzyReplaceTarget(content, "    return 1")
	if !ok || got != "\t\treturn 1" {
		t.Fatalf("indent drift: ok=%v got=%q, want %q", ok, got, "\t\treturn 1")
	}
	span, n := resolveReplaceTarget(content, "    return 1")
	if n != 1 || span != "\t\treturn 1" {
		t.Fatalf("resolve indent drift: span=%q n=%d", span, n)
	}
}

// Trailing whitespace in the search is tolerated.
func TestFuzzyReplaceTargetToleratesTrailingSpace(t *testing.T) {
	content := "alpha\nbeta\ngamma\n"
	got, ok := fuzzyReplaceTarget(content, "beta   ")
	if !ok || got != "beta" {
		t.Fatalf("trailing space: ok=%v got=%q, want %q", ok, got, "beta")
	}
}

// A multi-line block with no indentation resolves to the indented block in the
// file, returning the file's exact span (so a later strings.Replace succeeds).
func TestFuzzyReplaceTargetMatchesMultiLineBlock(t *testing.T) {
	content := "x\n  if a {\n    do()\n  }\ny\n"
	search := "if a {\ndo()\n}"
	want := "  if a {\n    do()\n  }"
	got, ok := fuzzyReplaceTarget(content, search)
	if !ok || got != want {
		t.Fatalf("multi-line block: ok=%v got=%q, want %q", ok, got, want)
	}
}

// An ambiguous fuzzy match (the trimmed search matches two lines) must be
// refused rather than guessing a location.
func TestFuzzyReplaceTargetRefusesAmbiguous(t *testing.T) {
	content := "  foo()\nbar\n  foo()\n"
	if got, ok := fuzzyReplaceTarget(content, "foo()"); ok {
		t.Fatalf("ambiguous search must be refused, got %q", got)
	}
}

// Blank or whitespace-only searches never match.
func TestFuzzyReplaceTargetRefusesBlankSearch(t *testing.T) {
	if _, ok := fuzzyReplaceTarget("a\nb\n", "   "); ok {
		t.Fatalf("whitespace-only search must be refused")
	}
	if _, n := resolveReplaceTarget("a\nb\n", ""); n != 0 {
		t.Fatalf("empty search must report zero occurrences")
	}
}

// A search longer than the file cannot match.
func TestFuzzyReplaceTargetRefusesOversizedSearch(t *testing.T) {
	if _, ok := fuzzyReplaceTarget("only one line\n", "a\nb\nc\nd\n"); ok {
		t.Fatalf("search longer than file must be refused")
	}
}
