package main

import (
	"strings"
	"testing"
)

// The status line pills use English domain terms consistently (perm:plan,
// verify:none, mcp:.../ok). The gate label must be English too -- not switched to
// Korean by locale -- so the status line is never a mix like "gate:준비됨 perm:plan".
func TestStatusGateLabelIsEnglishOnly(t *testing.T) {
	cases := []RuntimeGateLedger{
		{},
		{Status: runtimeGateStatusReady},
		{Status: runtimeGateStatusBlocked},
		{Status: runtimeGateStatusNeedsReview},
	}
	for _, ledger := range cases {
		got := statusOverviewGateLabel(ledger)
		if strings.ContainsAny(got, "막힘차단준비경고알없음리뷰필요") {
			t.Fatalf("status gate label must be English, got %q for %#v", got, ledger)
		}
	}
	if got := statusOverviewGateLabel(RuntimeGateLedger{}); got != "unknown" {
		t.Fatalf("empty gate label must be English 'unknown', got %q", got)
	}
}
