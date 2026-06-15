package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewTargetAllowsSessionChangedScope(t *testing.T) {
	for _, target := range []string{"", "auto", "change", "changes", "diff", "code"} {
		if !reviewTargetAllowsSessionChangedScope(target) {
			t.Fatalf("target %q should allow session-changed scope seeding", target)
		}
	}
	for _, target := range []string{"plan", "selection", "pr", "final", "goal", "analysis"} {
		if reviewTargetAllowsSessionChangedScope(target) {
			t.Fatalf("explicit target %q must not be overridden by session-changed scope seeding", target)
		}
	}
}

// TestBarePostEditReviewAnchorsToSessionChangedPaths proves that an argument-less
// `/review` after an edit anchors its scope to the files the session changed --
// recovered from the (possibly already-finalized) patch transaction via the same
// archive-aware source the runtime gate uses -- instead of discovering an empty
// scope and reviewing blind.
func TestBarePostEditReviewAnchorsToSessionChangedPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("print('hello')\n"), 0o644); err != nil {
		t.Fatalf("write app.py: %v", err)
	}
	session := NewSession(root, "scripted", "model", "", "default")
	// An earlier edit turn's patch transaction, archived on the session (no active
	// current-turn transaction), as it would be when /review runs on a later turn.
	session.PatchTransactions = []PatchTransaction{{
		Entries: []PatchTransactionEntry{{
			Paths: []PatchPathChange{{Path: "app.py", Operation: "edit"}},
		}},
	}}
	rt := &runtimeState{
		ui:        NewUI(),
		session:   session,
		store:     NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{BaseRoot: root, Root: root},
	}

	analysis := analyzeReviewRequest(rt, root, ReviewHarnessOptions{Request: "review"})
	if !containsString(analysis.ScopeDiscovery.CandidateFiles, "app.py") {
		t.Fatalf("a bare post-edit review must anchor scope to the session-changed app.py, got candidates=%#v width=%q", analysis.ScopeDiscovery.CandidateFiles, analysis.ScopeDiscovery.ScopeWidth)
	}
	if strings.EqualFold(analysis.ScopeDiscovery.ScopeWidth, "unknown") {
		t.Fatalf("anchoring to the changed file should give a concrete (non-unknown) scope, got width=%q", analysis.ScopeDiscovery.ScopeWidth)
	}
	// The review must become source-aware (it can now see the change) rather than
	// the blind/broad state that only warns it cannot inspect the change.
	if !strings.EqualFold(analysis.InferredTarget, reviewTargetChange) &&
		!strings.EqualFold(analysis.InferredTarget, reviewTargetSourceAnalysis) {
		t.Fatalf("a bare review with session changes should infer a source-aware review, got %q", analysis.InferredTarget)
	}
}

// TestReviewSessionChangedScopePathsRecoversArchivedPatch isolates the seed
// source: it must recover the changed paths from an already-finalized (archived)
// patch transaction, which is the case when /review runs on a turn after the
// edit, so the review no longer discovers an empty scope.
func TestReviewSessionChangedScopePathsRecoversArchivedPatch(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	if got := reviewSessionChangedScopePaths(&runtimeState{session: session, workspace: Workspace{BaseRoot: root, Root: root}}, root); len(got) != 0 {
		t.Fatalf("a session with no changes should seed no scope, got %#v", got)
	}
	session.PatchTransactions = []PatchTransaction{{
		Entries: []PatchTransactionEntry{{
			Paths: []PatchPathChange{{Path: "app.py", Operation: "edit"}},
		}},
	}}
	got := reviewSessionChangedScopePaths(&runtimeState{session: session, workspace: Workspace{BaseRoot: root, Root: root}}, root)
	if !containsString(got, "app.py") {
		t.Fatalf("expected the archived patch-transaction path to be recovered as review scope, got %#v", got)
	}
	if reviewSessionChangedScopePaths(nil, root) != nil {
		t.Fatalf("nil runtime state must seed no scope")
	}
}
