package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression (E-3): a file the collector cannot read (here, non-text) must be
// surfaced in evidence.Warnings, not silently dropped, so the reviewer is not
// left blind to a file it was asked to review.
func TestCollectFileReviewEvidenceWarnsOnNonTextDrop(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "blob.cpp")
	if err := os.WriteFile(bin, []byte{0x00, 0x01, 0x02, 0x00, 'a', 'b'}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var cs ReviewChangeSet
	var ev ReviewEvidencePack
	collectFileReviewEvidence(nil, root, []string{"blob.cpp"}, nil, &cs, &ev, 40000)
	if len(cs.ChangedPaths) != 0 {
		t.Fatalf("a non-text file must not be reported as reviewed, got %#v", cs.ChangedPaths)
	}
	if !warningsMention(ev.Warnings, "blob.cpp") {
		t.Fatalf("expected a warning naming the dropped file, got %#v", ev.Warnings)
	}
}

// Regression (EG-3): a file that yields no reviewable excerpt must not be
// counted in ChangedPaths (which the run reports as reviewed scope) while its
// content never entered the evidence text.
func TestCollectFileReviewEvidenceEmptyExcerptNotCountedAsReviewed(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "empty.go")
	if err := os.WriteFile(empty, []byte("   \n\t\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var cs ReviewChangeSet
	var ev ReviewEvidencePack
	collectFileReviewEvidence(nil, root, []string{"empty.go"}, nil, &cs, &ev, 40000)
	if len(cs.ChangedPaths) != 0 {
		t.Fatalf("an empty-excerpt file must not be counted as reviewed, got %#v", cs.ChangedPaths)
	}
	if len(ev.Sources) != 0 {
		t.Fatalf("no evidence source should be recorded, got %#v", ev.Sources)
	}
	if !warningsMention(ev.Warnings, "empty.go") {
		t.Fatalf("expected a warning naming the empty file, got %#v", ev.Warnings)
	}
}

// Regression (EG-4): a directory path is not expanded into per-file evidence,
// so it must be surfaced rather than silently skipped.
func TestCollectFileReviewEvidenceWarnsOnDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var cs ReviewChangeSet
	var ev ReviewEvidencePack
	collectFileReviewEvidence(nil, root, []string{"src"}, nil, &cs, &ev, 40000)
	if !warningsMention(ev.Warnings, "src") {
		t.Fatalf("expected a warning naming the skipped directory, got %#v", ev.Warnings)
	}
}

// Regression (E-9, ordering): source files sort ahead of document artifacts so
// the budget drops docs before code. Relative order within a class is stable.
func TestReviewOrderEvidencePathsSourceFirst(t *testing.T) {
	in := []string{"DESIGN.md", "a.go", "notes.txt", "b.cpp", "config.yaml"}
	got := reviewOrderEvidencePathsSourceFirst(in)
	// Source first (a.go, b.cpp), then other (config.yaml), then docs.
	if got[0] != "a.go" || got[1] != "b.cpp" {
		t.Fatalf("source files must come first in stable order, got %#v", got)
	}
	if got[len(got)-1] != "notes.txt" && got[len(got)-2] != "DESIGN.md" {
		t.Fatalf("document artifacts must sort last, got %#v", got)
	}
}

// Regression (E-9, drop naming): under a tight budget where the source excerpt
// exhausts the context, the trailing document is dropped and the truncation
// warning names it (instead of a generic "truncated" with no detail).
func TestCollectFileReviewEvidenceNamesDroppedDoc(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "service.go")
	doc := filepath.Join(root, "DESIGN.md")
	if err := os.WriteFile(src, []byte("package main\n"+strings.Repeat("// source line\n", 400)), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.WriteFile(doc, []byte(strings.Repeat("design paragraph\n", 400)), 0o644); err != nil {
		t.Fatalf("write doc: %v", err)
	}
	var cs ReviewChangeSet
	var ev ReviewEvidencePack
	// A budget small enough that the first (source) excerpt plus its section
	// overhead exhausts the context, so the trailing doc is dropped. Pass the
	// doc first to also exercise the source-first reordering.
	collectFileReviewEvidence(nil, root, []string{"DESIGN.md", "service.go"}, nil, &cs, &ev, 50)
	joined := strings.Join(cs.ChangedPaths, ",")
	if !strings.Contains(joined, "service.go") {
		t.Fatalf("the source file must be collected first, got %#v", cs.ChangedPaths)
	}
	if strings.Contains(joined, "DESIGN.md") {
		t.Fatalf("the doc should have been dropped under the tight budget, got %#v", cs.ChangedPaths)
	}
	if !warningsMention(ev.Warnings, "DESIGN.md") {
		t.Fatalf("truncation warning should name the dropped doc, got %#v", ev.Warnings)
	}
}

func TestReviewGitPathArgsSkipsOutOfRootAndContinues(t *testing.T) {
	root := t.TempDir()
	args, dropped, err := reviewGitPathArgs(root, []string{"in_repo.go", filepath.Join(root, "..", "outside.md")})
	if err != nil {
		t.Fatalf("reviewGitPathArgs: %v", err)
	}
	if len(dropped) != 1 {
		t.Fatalf("expected one dropped out-of-root path, got %#v", dropped)
	}
	if len(args) < 2 || args[0] != "--" {
		t.Fatalf("expected a pathspec for the in-root file, got %#v", args)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "in_repo.go") {
		t.Fatalf("the in-root path must survive, got %#v", args)
	}
	if strings.Contains(joined, "outside.md") {
		t.Fatalf("the out-of-root path must not be in the pathspec, got %#v", args)
	}
}

func warningsMention(warnings []string, needle string) bool {
	for _, w := range warnings {
		if strings.Contains(w, needle) {
			return true
		}
	}
	return false
}
