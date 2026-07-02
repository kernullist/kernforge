package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression (E-1): "read the design doc and fix the code" must not collapse
// scope to the document alone. When every candidate is a doc and the request
// wants source changes, scope discovery mines the doc's references and pulls
// the referenced source files into scope.
func TestReviewScopeExpandsFromDocumentReferences(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "RegGitCore"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// The referenced source file the doc points at.
	if err := os.WriteFile(filepath.Join(root, "RegGitCore", "DatabaseService.cs"),
		[]byte("namespace RegGitCore { class DatabaseService {} }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	doc := filepath.Join(root, "RegGit_Design_Doc.md")
	if err := os.WriteFile(doc,
		[]byte("# Design\nThe RegGitCore.DatabaseService stores deltas.\nSee RegGitCore/DatabaseService.cs for the schema.\n"), 0o644); err != nil {
		t.Fatalf("write doc: %v", err)
	}

	candidates := reviewScopeCandidateFiles(root, "read RegGit_Design_Doc.md and fix the code accordingly", []string{"RegGit_Design_Doc.md"})
	joined := strings.Join(candidates, " ")
	if !strings.Contains(joined, "DatabaseService.cs") {
		t.Fatalf("scope should expand from the doc's references to the source file, got %#v", candidates)
	}
}

// A pure document-review request (no source-change intent) must NOT trigger the
// expansion — it stays scoped to the document.
func TestReviewScopeDoesNotExpandForDocReviewRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "RegGitCore"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "RegGitCore", "DatabaseService.cs"),
		[]byte("namespace RegGitCore { class DatabaseService {} }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	doc := filepath.Join(root, "RegGit_Design_Doc.md")
	if err := os.WriteFile(doc,
		[]byte("# Design\nThe RegGitCore.DatabaseService stores deltas.\n"), 0o644); err != nil {
		t.Fatalf("write doc: %v", err)
	}
	candidates := reviewScopeCandidateFiles(root, "proofread RegGit_Design_Doc.md for typos", []string{"RegGit_Design_Doc.md"})
	if strings.Contains(strings.Join(candidates, " "), "DatabaseService.cs") {
		t.Fatalf("a document proofreading request must not pull in source files, got %#v", candidates)
	}
}

// Regression (E-6): a code-change request whose evidence contains no executable
// source file gets a prominent non-blocking warning from the deterministic
// pass, instead of leaving the model to emit an evidence_gap blocker.
func TestDeterministicWarnsOnCodeChangeWithoutSourceEvidence(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	run := ReviewRun{
		Trigger:   "post_change",
		Target:    reviewTargetChange,
		Objective: "read RegGit_Design_Doc.md and implement the fix in the code",
		ChangeSet: ReviewChangeSet{ChangedPaths: []string{"RegGit_Design_Doc.md"}},
		Evidence: ReviewEvidencePack{
			Sources:      []string{"file_excerpt"},
			ChangedPaths: []string{"RegGit_Design_Doc.md"},
			Text:         "design document excerpt",
		},
	}
	findings := deterministicReviewFindings(rt, run)
	var got *ReviewFinding
	for i := range findings {
		if findings[i].Title == "Code-change request reviewed without source-code evidence" {
			got = &findings[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("expected a zero-source-evidence warning, got %#v", findings)
	}
	if got.BlocksGate {
		t.Fatalf("the zero-source-evidence signal must be a warning, not a blocker")
	}
}

// The zero-source-evidence signal must NOT fire when source evidence is present.
func TestDeterministicNoSourceWarningWhenSourcePresent(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	run := ReviewRun{
		Trigger:   "post_change",
		Target:    reviewTargetChange,
		Objective: "fix the bug in DatabaseService.cs",
		ChangeSet: ReviewChangeSet{ChangedPaths: []string{"RegGitCore/DatabaseService.cs"}},
		Evidence: ReviewEvidencePack{
			Sources:      []string{"file_excerpt"},
			ChangedPaths: []string{"RegGitCore/DatabaseService.cs"},
			Text:         "class DatabaseService {}",
		},
	}
	for _, f := range deterministicReviewFindings(rt, run) {
		if f.Title == "Code-change request reviewed without source-code evidence" {
			t.Fatalf("must not warn when executable source evidence is present")
		}
	}
}
