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
	got, ok, ambiguous := fuzzyReplaceTarget(content, "    return 1")
	if !ok || ambiguous || got != "\t\treturn 1" {
		t.Fatalf("indent drift: ok=%v ambiguous=%v got=%q, want %q", ok, ambiguous, got, "\t\treturn 1")
	}
	span, n := resolveReplaceTarget(content, "    return 1")
	if n != 1 || span != "\t\treturn 1" {
		t.Fatalf("resolve indent drift: span=%q n=%d", span, n)
	}
}

// Trailing whitespace in the search is tolerated.
func TestFuzzyReplaceTargetToleratesTrailingSpace(t *testing.T) {
	content := "alpha\nbeta\ngamma\n"
	got, ok, ambiguous := fuzzyReplaceTarget(content, "beta   ")
	if !ok || ambiguous || got != "beta" {
		t.Fatalf("trailing space: ok=%v ambiguous=%v got=%q, want %q", ok, ambiguous, got, "beta")
	}
}

// A multi-line block with no indentation resolves to the indented block in the
// file, returning the file's exact span (so a later strings.Replace succeeds).
func TestFuzzyReplaceTargetMatchesMultiLineBlock(t *testing.T) {
	content := "x\n  if a {\n    do()\n  }\ny\n"
	search := "if a {\ndo()\n}"
	want := "  if a {\n    do()\n  }"
	got, ok, ambiguous := fuzzyReplaceTarget(content, search)
	if !ok || ambiguous || got != want {
		t.Fatalf("multi-line block: ok=%v ambiguous=%v got=%q, want %q", ok, ambiguous, got, want)
	}
}

// An ambiguous fuzzy match (the trimmed search matches two lines) must be
// refused rather than guessing a location.
func TestFuzzyReplaceTargetRefusesAmbiguous(t *testing.T) {
	content := "  foo()\nbar\n  foo()\n"
	got, ok, ambiguous := fuzzyReplaceTarget(content, "foo()")
	if ok || got != "" {
		t.Fatalf("ambiguous search must be refused, got %q ok=%v", got, ok)
	}
	if !ambiguous {
		t.Fatalf("ambiguous search must report ambiguous=true")
	}
}

// Blank or whitespace-only searches never match.
func TestFuzzyReplaceTargetRefusesBlankSearch(t *testing.T) {
	if _, ok, _ := fuzzyReplaceTarget("a\nb\n", "   "); ok {
		t.Fatalf("whitespace-only search must be refused")
	}
	if _, n := resolveReplaceTarget("a\nb\n", ""); n != 0 {
		t.Fatalf("empty search must report zero occurrences")
	}
}

// A search longer than the file cannot match.
func TestFuzzyReplaceTargetRefusesOversizedSearch(t *testing.T) {
	if _, ok, _ := fuzzyReplaceTarget("only one line\n", "a\nb\nc\nd\n"); ok {
		t.Fatalf("search longer than file must be refused")
	}
}

// A multi-line search written with bare LF must find its CRLF counterpart in
// the file. This is the exact shape that dead-locked a real session: the model
// re-read a CRLF C# file, re-issued an LF-only exact_search, and mismatched
// forever until the retry-loop guard aborted the run.
func TestResolveReplaceTargetMatchesCRLFContentWithLFSearch(t *testing.T) {
	content := "                    );\";\r\n                    CREATE TABLE IF NOT EXISTS Deltas (\r\n"
	search := "                    );\";\n                    CREATE TABLE IF NOT EXISTS Deltas ("
	want := "                    );\";\r\n                    CREATE TABLE IF NOT EXISTS Deltas ("
	got, n := resolveReplaceTarget(content, search)
	if n != 1 || got != want {
		t.Fatalf("crlf content: got %q n=%d, want %q n=1", got, n, want)
	}
}

// The reverse direction: a CRLF search against an LF file.
func TestResolveReplaceTargetMatchesLFContentWithCRLFSearch(t *testing.T) {
	content := "alpha\nbeta\ngamma\n"
	got, n := resolveReplaceTarget(content, "alpha\r\nbeta")
	if n != 1 || got != "alpha\nbeta" {
		t.Fatalf("lf content: got %q n=%d, want %q n=1", got, n, "alpha\nbeta")
	}
}

// Whitespace drift inside a CRLF file resolves via the fuzzy ladder; the span
// keeps the file's real bytes including interior CRLF and the trailing CR of
// the final matched line.
func TestResolveReplaceTargetFuzzyMatchInCRLFContent(t *testing.T) {
	content := "func x() {\r\n\t\treturn 1\r\n}\r\n"
	span, n := resolveReplaceTarget(content, "    return 1")
	if n != 1 || span != "\t\treturn 1\r" {
		t.Fatalf("crlf fuzzy: span=%q n=%d", span, n)
	}
}

// Replacements are rewritten to the file's dominant line ending, and a
// match-final bare CR stays paired so no mixed LF terminator is introduced.
func TestAdaptReplacementToMatchPreservesCRLF(t *testing.T) {
	content := "a\r\nb\r\nc\r\n"
	got := adaptReplacementToMatch(content, "a\r\nb", "one\ntwo")
	if got != "one\r\ntwo" {
		t.Fatalf("crlf replacement: got %q, want %q", got, "one\r\ntwo")
	}
	got = adaptReplacementToMatch(content, "b\r", "middle")
	if got != "middle\r" {
		t.Fatalf("trailing CR: got %q, want %q", got, "middle\r")
	}
	got = adaptReplacementToMatch("a\nb\n", "a", "one\r\ntwo")
	if got != "one\ntwo" {
		t.Fatalf("lf replacement: got %q, want %q", got, "one\ntwo")
	}
	if got = adaptReplacementToMatch(content, "b\r", "plain"); got != "plain\r" {
		t.Fatalf("newline-free replacement after CR match: got %q, want %q", got, "plain\r")
	}
}
