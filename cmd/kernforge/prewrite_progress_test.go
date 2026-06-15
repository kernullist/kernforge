package main

import "testing"

// TestPreWriteReviewRepairProgressed locks in the progress-aware non-convergence
// rule: a repair round that resolves at least one prior blocker without growing
// the total is real (multi-stage) progress and must not count toward the cut,
// while the same finding repeating or the blocker set growing is not progress.
func TestPreWriteReviewRepairProgressed(t *testing.T) {
	set := func(ids ...string) map[string]bool {
		m := map[string]bool{}
		for _, id := range ids {
			m[id] = true
		}
		return m
	}

	cases := []struct {
		name    string
		prev    map[string]bool
		current map[string]bool
		want    bool
	}{
		{"first block has no prior set", set(), set("rf-001", "rf-002"), false},
		{"shrink 4 to 2 is progress", set("a", "b", "c", "d"), set("a", "b"), true},
		{"shrink 2 to 1 is progress", set("a", "b"), set("a"), true},
		{"swap one for another at same count is churn not progress", set("a", "b"), set("a", "c"), false},
		{"fully swapped same count is churn not progress", set("a"), set("b"), false},
		{"identical set is not progress", set("a", "b"), set("a", "b"), false},
		{"grew 1 to 2 is not progress", set("a"), set("a", "b"), false},
		{"resolved one but total grew is not progress", set("a", "b"), set("c", "d", "e"), false},
		{"fully resolved is progress", set("a"), set(), true},
		// A strict count drop mathematically guarantees at least one prior
		// blocker was resolved (zero-resolved would mean prev is a subset of
		// current, forcing the count to not shrink), so a disjoint shrink is
		// genuine progress even though every id is new. These pin the count-based
		// contract for the shapes the regression tests do not otherwise exercise.
		{"disjoint shrink is progress", set("a", "b", "c", "d"), set("e", "f"), true},
		{"fewer but all-new ids is progress", set("a", "b", "c"), set("x", "y"), true},
	}
	for _, tc := range cases {
		if got := preWriteReviewRepairProgressed(tc.prev, tc.current); got != tc.want {
			t.Errorf("%s: preWriteReviewRepairProgressed=%v want %v", tc.name, got, tc.want)
		}
	}
}
