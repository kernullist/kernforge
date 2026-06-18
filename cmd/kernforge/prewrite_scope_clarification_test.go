package main

import (
	"strings"
	"testing"
)

func TestPreWriteReviewRepairLooksLikeScopeChurn(t *testing.T) {
	cases := []struct {
		name         string
		fingerprints map[string]int
		rounds       int
		want         bool
	}{
		{"many distinct finding sets is scope churn", map[string]int{"a": 1, "b": 1, "c": 1}, 5, true},
		{"too few rounds is not churn", map[string]int{"a": 1, "b": 1, "c": 1}, 2, false},
		{"stuck on one repeating finding is not churn", map[string]int{"a": 4}, 4, false},
		{"two distinct sets is not yet churn", map[string]int{"a": 2, "b": 2}, 4, false},
	}
	for _, tc := range cases {
		if got := preWriteReviewRepairLooksLikeScopeChurn(tc.fingerprints, tc.rounds); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestPreWriteReviewRepairScopeClarificationReplyAsksForScope(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: ".env에 gitlab 토큰을 넣어두고 사용하게 하자"})
	session.LastReviewRun = &ReviewRun{
		Trigger:   "pre_write",
		Objective: ".env에 gitlab 토큰을 넣어두고 사용하게 하자",
		Gate:      GateDecision{Verdict: reviewVerdictNeedsRevision, BlockingFindings: []string{"RF-001"}},
		Findings:  []ReviewFinding{{ID: "RF-001", Severity: reviewSeverityMedium, Category: "correctness", Title: "scope keeps growing"}},
	}

	en := formatPreWriteReviewRepairScopeClarificationReply(Config{AutoLocale: boolPtr(false)}, session, 5)
	if !strings.Contains(en, "minimal behavior") || !strings.Contains(en, "beyond the original request") {
		t.Fatalf("English scope-clarification reply should ask for minimal behavior, got %q", en)
	}
	ko := formatPreWriteReviewRepairScopeClarificationReply(Config{AutoLocale: boolPtr(true)}, session, 5)
	if !strings.Contains(ko, "최소 동작") {
		t.Fatalf("Korean scope-clarification reply should ask for the minimal behavior, got %q", ko)
	}
}
