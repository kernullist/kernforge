package main

import "testing"

// Regression (EG-1/E-5): request routing predicates matched short ASCII tokens
// as bare substrings, so "improve"/"print" tripped the "pr" (PR review) branch
// and a filename like RegGit_Design_Doc.md tripped the "design" (plan) branch,
// misrouting a code-change request. They now use word-boundary matching.
func TestInferReviewTargetIgnoresSubstringFalsePositives(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name    string
		request string
		notWant string
	}{
		{"improve-not-pr", "improve the retry loop in the driver", reviewTargetPR},
		{"print-not-pr", "fix the print formatting in the logger", reviewTargetPR},
		{"design-filename-not-plan", "read RegGit_Design_Doc.md and fix DatabaseService.cs", reviewTargetPlan},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Empty discovery so reviewRequestPrefersSourceEvidence does not short-circuit.
			got := inferReviewTarget(nil, root, tc.request, ReviewScopeDiscovery{})
			if got == tc.notWant {
				t.Fatalf("request %q must not route to %q, got %q", tc.request, tc.notWant, got)
			}
		})
	}
}

// A genuine PR / design request still routes correctly.
func TestInferReviewTargetKeepsRealMatches(t *testing.T) {
	root := t.TempDir()
	if got := inferReviewTarget(nil, root, "review this pr before merge", ReviewScopeDiscovery{}); got != reviewTargetPR {
		t.Fatalf("a real PR request should route to PR, got %q", got)
	}
	if got := inferReviewTarget(nil, root, "review the design of the new module", ReviewScopeDiscovery{}); got != reviewTargetPlan {
		t.Fatalf("a real design request should route to plan, got %q", got)
	}
}

// Regression (E-5): naming a design doc in a code-fix request must not skip the
// session-changed seed via reviewRequestNamesNonChangeTarget substring match.
func TestReviewRequestNamesNonChangeTargetWordBoundary(t *testing.T) {
	if reviewRequestNamesNonChangeTarget("improve the printer path handling") {
		t.Fatalf("'improve'/'printer' must not count as naming a non-change target")
	}
	if !reviewRequestNamesNonChangeTarget("review the design of this system") {
		t.Fatalf("a real design-review request should be recognized")
	}
}
