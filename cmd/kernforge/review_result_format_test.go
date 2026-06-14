package main

import (
	"strings"
	"testing"
)

// review_result_format_test.go locks the refined box-card review-result format.
// The same renderer (writeReviewHeaderBox + writeReviewFindingCard, both in
// review_result_render.go) is shared by every review reply surface, and the
// rendered string is reused on the terminal, in MCP responses, and in saved
// .md artifacts. These tests assert the structure (box header, RF card with all
// fields, no ANSI, no information lost) on representative reply surfaces so a
// future refactor cannot silently regress the format back to plain text or
// embed ANSI color codes into the shared reply string.

// reviewResultFormatSampleRun returns a representative ReviewRun: needs_revision
// with one blocker and one warning, and one full RF carrying every field
// (location/evidence/impact/action/test). It mirrors the user-approved target
// shape so the test guards the exact contract.
func reviewResultFormatSampleRun() ReviewRun {
	return ReviewRun{
		Trigger:   "pre_write",
		Objective: "Review the policy change before writing files.",
		RequestAnalysis: ReviewRequestAnalysis{
			OriginalRequest: "Review the policy change before writing files.",
		},
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-001"},
			WarningFindings:  []string{"RF-002"},
		},
		Result: ReviewResult{
			Verdict: reviewVerdictNeedsRevision,
			Summary: "Four policies conflict with the existing data API.",
		},
		Findings: []ReviewFinding{
			{
				ID:                 "RF-001",
				Source:             "model",
				Severity:           reviewSeverityBlocker,
				Category:           "correctness",
				Path:               "app.py",
				Symbol:             "DATA_FILE",
				Title:              "Removing DATA_FILE breaks the data API",
				Evidence:           "DATA_FILE was removed but load_data and others still reference it.",
				Impact:             "Calls to /api/data and /api/download fail.",
				RequiredFix:        "Keep DATA_FILE or point the references at the policy path.",
				TestRecommendation: "After login, check /api/data GET, POST, and download.",
				BlocksGate:         true,
			},
			{
				ID:                 "RF-002",
				Source:             "model",
				Severity:           reviewSeverityMedium,
				Category:           "stability",
				Path:               "app.py",
				Symbol:             "load_config",
				Title:              "Config reload is not guarded",
				Evidence:           "load_config reruns on every request without a guard.",
				Impact:             "Repeated disk reads add latency under load.",
				RequiredFix:        "Cache the parsed config and invalidate on change.",
				TestRecommendation: "Benchmark repeated requests after the change.",
			},
		},
		ArtifactRefs: []string{"C:/tmp/review.md"},
	}
}

// assertNoANSIEscape fails if the rendered reply embeds an ANSI escape. The
// reply string is reused on terminal, MCP, and saved .md surfaces, so it must
// be plain text and identical everywhere.
func assertNoANSIEscape(t *testing.T, surface string, rendered string) {
	t.Helper()
	if strings.Contains(rendered, "\x1b") {
		t.Fatalf("%s reply must not embed ANSI escape (\\x1b), got:\n%s", surface, rendered)
	}
}

// assertReviewResultBoxHeader checks the rounded box header is present with the
// box-drawing chars, a humanized verdict (raw enum must not leak), the
// blocker/warning counts, and the next/preview decision line.
func assertReviewResultBoxHeader(t *testing.T, surface string, rendered string, humanVerdict string, blockerCount string, warningCount string, decision string) {
	t.Helper()
	for _, want := range []string{
		reviewBoxTopLeft,
		reviewBoxTopRight,
		reviewBoxBottomLeft,
		reviewBoxBottomRight,
		reviewBoxVertical,
		humanVerdict,
		blockerCount,
		warningCount,
		decision,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("%s header should contain %q, got:\n%s", surface, want, rendered)
		}
	}
	// The raw verdict enum must never show in the header.
	if strings.Contains(rendered, reviewVerdictNeedsRevision) {
		t.Fatalf("%s header must humanize the verdict, raw %q leaked:\n%s", surface, reviewVerdictNeedsRevision, rendered)
	}
}

// assertReviewFindingCardEN checks the RF-001 card shows the severity symbol,
// id, the humanized [severity.category] tag, the title, and ALL kept fields.
func assertReviewFindingCardEN(t *testing.T, surface string, rendered string) {
	t.Helper()
	for _, want := range []string{
		reviewSymbolBlocker + " RF-001  [blocker·correctness]",
		"Removing DATA_FILE breaks the data API",
		"Location",
		"app.py :: DATA_FILE",
		"Evidence",
		"DATA_FILE was removed but load_data and others still reference it.",
		"Impact",
		"Calls to /api/data and /api/download fail.",
		"Action",
		"Keep DATA_FILE or point the references at the policy path.",
		"Test",
		"After login, check /api/data GET, POST, and download.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("%s RF-001 card should contain %q, got:\n%s", surface, want, rendered)
		}
	}
}

func TestReviewResultFormatNaturalReplyEnglishStructure(t *testing.T) {
	run := reviewResultFormatSampleRun()
	rendered := formatCodexAppReviewModeReply(Config{AutoLocale: boolPtr(false)}, run)

	assertReviewResultBoxHeader(t, "natural-language", rendered, "needs revision", "blockers 1", "warnings 1", "no edits")
	if !strings.Contains(rendered, "Review result") {
		t.Fatalf("natural-language header should carry the box title, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Four policies conflict with the existing data API.") {
		t.Fatalf("natural-language header should carry the summary, got:\n%s", rendered)
	}
	assertReviewFindingCardEN(t, "natural-language", rendered)
	// The warning RF must also render as a card below the box, not be dropped.
	if !strings.Contains(rendered, reviewSymbolWarning+" RF-002  [warning·stability]") {
		t.Fatalf("natural-language reply should render the warning RF card, got:\n%s", rendered)
	}
	assertNoANSIEscape(t, "natural-language", rendered)
}

func TestReviewResultFormatPreWriteSummaryEnglishStructure(t *testing.T) {
	run := reviewResultFormatSampleRun()
	rendered := formatPreWriteFinalVisibleReviewSummary(Config{AutoLocale: boolPtr(false)}, run, false)

	assertReviewResultBoxHeader(t, "pre-write summary", rendered, "needs revision", "blockers 1", "warnings 1", "preview stopped")
	assertReviewFindingCardEN(t, "pre-write summary", rendered)
	// Information must not be lost: the report path / review-items section stay.
	if !strings.Contains(rendered, "Review report: C:/tmp/review.md") {
		t.Fatalf("pre-write summary must keep the report path, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Review items:") {
		t.Fatalf("pre-write summary must keep the review-items section, got:\n%s", rendered)
	}
	assertNoANSIEscape(t, "pre-write summary", rendered)
}

func TestReviewResultFormatPreWriteSummaryKoreanStructure(t *testing.T) {
	run := reviewResultFormatSampleRun()
	run.Objective = "정책 변경을 파일 쓰기 전에 검토해줘."
	run.RequestAnalysis.OriginalRequest = "정책 변경을 파일 쓰기 전에 검토해줘."
	rendered := formatPreWriteFinalVisibleReviewSummary(Config{AutoLocale: boolPtr(false)}, run, false)

	// Korean surface mirrors the same structure with localized labels.
	assertReviewResultBoxHeader(t, "pre-write summary (ko)", rendered, "수정 필요", "차단 1", "경고 1", "preview 중단")
	for _, want := range []string{
		"검토 결과",
		reviewSymbolBlocker + " RF-001  [차단·correctness]",
		"Removing DATA_FILE breaks the data API",
		"위치",
		"app.py :: DATA_FILE",
		"근거",
		"영향",
		"조치",
		"테스트",
		"리뷰 보고서: C:/tmp/review.md",
		"검토 항목:",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Korean pre-write summary should contain %q, got:\n%s", want, rendered)
		}
	}
	assertNoANSIEscape(t, "pre-write summary (ko)", rendered)
}
