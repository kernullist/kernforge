package main

import (
	"strings"
	"testing"
)

// TestIsAppendSafeFinalAnswerDisclosureTitle is the audit (Slice D): every
// blocker title must be classified as either an append-safe missing-disclosure
// gap or a real content problem that must not be auto-completed.
func TestIsAppendSafeFinalAnswerDisclosureTitle(t *testing.T) {
	safe := []string{
		"Changed-file summary is missing",
		"Review result is missing",
		"Validation result is missing",
		"Remaining-risk statement is missing",
		"Review-only no-edit statement is missing",
		"No-finding review omits residual risk",
		"Cross-review residual risk is undisclosed",
		"Verification was not run disclosure missing",
	}
	for _, title := range safe {
		if !isAppendSafeFinalAnswerDisclosureTitle(title) {
			t.Fatalf("%q should be append-safe", title)
		}
	}
	// These need a real correction (contradiction, overclaim, unresolved failure,
	// structural reorder) or are handled by another heal, so they must NOT be
	// auto-completed by appending a fact.
	unsafe := []string{
		"Final answer has inconsistent bug counts",
		"Final answer contradicts the patch transaction",
		"Verification claim has no recorded evidence",
		"Required verification has no outcome",
		"Unresolved verification failure",
		"Final answer contradicts remaining edit-loop risk",
		"Edit loop verification failure is unstated",
		"Background work is still running",
		"Review-only answer is not findings-first",
		"Document artifact path is missing",
		"Document artifact quality status is missing",
	}
	for _, title := range unsafe {
		if isAppendSafeFinalAnswerDisclosureTitle(title) {
			t.Fatalf("%q must NOT be append-safe", title)
		}
	}
}

// TestReportOnlyBlockedByAppendSafeFinalAnswerDisclosures locks the heal gate:
// only a report whose every blocker is an append-safe disclosure (and all live in
// the Outcome report) qualifies. Any contradiction, structural finding, or blocker
// in another sub-report disqualifies it.
func TestReportOnlyBlockedByAppendSafeFinalAnswerDisclosures(t *testing.T) {
	changedFile := CodingHarnessFinding{Severity: "blocker", Title: "Changed-file summary is missing", Detail: "x"}
	validation := CodingHarnessFinding{Severity: "blocker", Title: "Validation result is missing", Detail: "x"}
	remaining := CodingHarnessFinding{Severity: "blocker", Title: "Remaining-risk statement is missing", Detail: "x"}
	crossReview := CodingHarnessFinding{Severity: "blocker", Title: "Cross-review residual risk is undisclosed", Detail: "x"}

	only := &CodingHarnessReport{Outcome: OutcomeInvariantReport{Findings: []CodingHarnessFinding{changedFile, validation, remaining, crossReview}}}
	if !reportOnlyBlockedByAppendSafeFinalAnswerDisclosures(only) {
		t.Fatalf("expected append-safe disclosure blockers to qualify for the general heal")
	}

	withContradiction := &CodingHarnessReport{Outcome: OutcomeInvariantReport{Findings: []CodingHarnessFinding{
		changedFile,
		{Severity: "blocker", Title: "Final answer has inconsistent bug counts"},
	}}}
	if reportOnlyBlockedByAppendSafeFinalAnswerDisclosures(withContradiction) {
		t.Fatalf("a contradiction blocker must disqualify the heal")
	}

	notFindingsFirst := &CodingHarnessReport{Outcome: OutcomeInvariantReport{Findings: []CodingHarnessFinding{
		{Severity: "blocker", Title: "Review-only answer is not findings-first"},
	}}}
	if reportOnlyBlockedByAppendSafeFinalAnswerDisclosures(notFindingsFirst) {
		t.Fatalf("the structural findings-first blocker must not be auto-healed")
	}

	blockerElsewhere := &CodingHarnessReport{
		DiffReview: DiffAwareSelfReviewReport{Findings: []CodingHarnessFinding{{Severity: "blocker", Title: "Final answer contradicts the patch transaction"}}},
		Outcome:    OutcomeInvariantReport{Findings: []CodingHarnessFinding{changedFile}},
	}
	if reportOnlyBlockedByAppendSafeFinalAnswerDisclosures(blockerElsewhere) {
		t.Fatalf("a real defect outside the Outcome report must disqualify the heal")
	}

	if reportOnlyBlockedByAppendSafeFinalAnswerDisclosures(&CodingHarnessReport{}) {
		t.Fatalf("an approved report has nothing to heal")
	}
	if reportOnlyBlockedByAppendSafeFinalAnswerDisclosures(nil) {
		t.Fatalf("a nil report must not qualify")
	}
}

// TestHealFinalAnswerOnlyDisclosuresReviewOnly exercises the general heal end to
// end on a review-only turn: the bare reply omits the no-edit and residual-risk
// disclosures, the harness blocks, the heal appends the known facts, and the
// re-checked report approves. Review-only needs no changed-path detection, so the
// test is deterministic.
func TestHealFinalAnswerOnlyDisclosuresReviewOnly(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		RequestClass: reviewRequestClassReviewOnly,
		SourcePrompt: "review this function for bugs",
	}
	agent := &Agent{
		Config:    Config{},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}

	reply := "No issues found."
	report := agent.buildCodingHarnessReport(reply, false, false)
	if report.Approved {
		t.Fatalf("a review-only reply that omits the no-edit and residual disclosures must block, got approved")
	}
	agent.Session.LastCodingHarnessReport = &report
	if !reportOnlyBlockedByAppendSafeFinalAnswerDisclosures(&report) {
		t.Fatalf("the blocked review-only report should qualify for the general heal, findings=%#v", report.Outcome.Findings)
	}

	healed, recheck, ok := agent.healFinalAnswerOnlyDisclosures(reply, false, false)
	if !ok {
		t.Fatalf("the general heal should complete a review-only disclosure-only block, report=%#v", report.Outcome.Findings)
	}
	if recheck == nil || !recheck.Approved {
		t.Fatalf("the re-checked report must approve after the heal, got %#v", recheck)
	}
	lower := strings.ToLower(healed)
	if !strings.Contains(lower, "no files were changed") {
		t.Fatalf("heal must append the read-only no-edit disclosure, got %q", healed)
	}
	if !strings.Contains(lower, "not exhaustively tested") {
		t.Fatalf("heal must append the residual-evidence-risk disclosure, got %q", healed)
	}
	if !strings.HasPrefix(healed, reply) {
		t.Fatalf("heal must preserve the model's original answer as a prefix, got %q", healed)
	}
}

// TestHealFinalAnswerOnlyDisclosuresDoesNotMaskRealDefect guards that the general
// heal never fires when a real content defect (a contradiction) is present, even
// alongside an append-safe disclosure gap.
func TestHealFinalAnswerOnlyDisclosuresDoesNotMaskRealDefect(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Config:    Config{},
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}
	agent.Session.LastCodingHarnessReport = &CodingHarnessReport{
		Outcome: OutcomeInvariantReport{Findings: []CodingHarnessFinding{
			{Severity: "blocker", Title: "Validation result is missing", Detail: "x"},
			{Severity: "blocker", Title: "Verification claim has no recorded evidence", Detail: "overclaim"},
		}},
	}
	if _, _, ok := agent.healFinalAnswerOnlyDisclosures("Everything passed.", false, false); ok {
		t.Fatalf("the heal must NOT fire while a real content defect (overclaim) is present")
	}
}
