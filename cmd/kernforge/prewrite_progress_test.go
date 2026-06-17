package main

import "testing"

// TestPreWriteReviewRepairProgressed locks in the running-minimum progress rule:
// a repair round counts as progress only when its blocker total falls below the
// lowest count seen so far this turn. Measuring against the running minimum (not
// just the previous round) is what makes an oscillating count (3 -> 2 -> 3 -> 2
// whack-a-mole) read as no-progress instead of resetting the guard each dip.
func TestPreWriteReviewRepairProgressed(t *testing.T) {
	set := func(ids ...string) map[string]bool {
		m := map[string]bool{}
		for _, id := range ids {
			m[id] = true
		}
		return m
	}

	cases := []struct {
		name     string
		current  map[string]bool
		minSoFar int
		want     bool
	}{
		{"first block (min unset) is not progress", set("a", "b"), -1, false},
		{"below running min is progress", set("a", "b"), 4, true},
		{"shrink to new low is progress", set("a"), 2, true},
		{"equal to running min is churn not progress", set("a", "b"), 2, false},
		{"equal min but fully swapped ids is still churn", set("c", "d"), 2, false},
		{"above running min is not progress", set("a", "b", "c"), 2, false},
		{"fully resolved below min is progress", set(), 1, true},
		{"zero stays not progress once min already zero", set(), 0, false},
	}
	for _, tc := range cases {
		if got := preWriteReviewRepairProgressed(nil, tc.current, tc.minSoFar); got != tc.want {
			t.Errorf("%s: preWriteReviewRepairProgressed=%v want %v", tc.name, got, tc.want)
		}
	}
}
