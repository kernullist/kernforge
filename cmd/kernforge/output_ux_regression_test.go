package main

import (
	"strings"
	"testing"
)

// output_ux_regression_test.go locks in the user-facing output cleanup. Each
// test captures the IMPROVED rendering for a key renderer and asserts (a) that
// the old confusing artifacts are gone (raw codenames, self-contradictions,
// English-in-Korean glue) and (b) that the information the user actually needs
// (changed files, verification/review result, the single next command) still
// appears. These are PERMANENT guards: if a future change reintroduces a
// codename leak or drops required info, one of these fails.

// koreanReviewRun returns a ReviewRun whose locale signal resolves to Korean
// (the OriginalRequest carries Hangul, which inferResponseLanguageForUserText
// treats as a Korean question regardless of the host locale).
func koreanReviewRun() ReviewRun {
	return ReviewRun{
		ID:     "rev-ko-1",
		Target: reviewTargetChange,
		Mode:   reviewModeCoreBuild,
		RequestAnalysis: ReviewRequestAnalysis{
			OriginalRequest: "이 변경을 리뷰해줘",
		},
	}
}

// TestRenderReviewCLIResultCompactKoreanHasNoCodenames asserts the compact CLI
// review view (the primary user-facing surface) leads with localized words and
// never leaks raw enum codenames or key=value soup.
func TestRenderReviewCLIResultCompactKoreanHasNoCodenames(t *testing.T) {
	run := koreanReviewRun()
	run.RequestClass = reviewRequestClassReviewOnly
	run.RequestAnalysis.RequestClass = reviewRequestClassReviewOnly
	run.Gate = GateDecision{
		Verdict:          reviewVerdictNeedsRevision,
		Action:           reviewGateActionRepairRequired,
		BlockingFindings: []string{"F1"},
		WarningFindings:  []string{"F2"},
	}
	run.Findings = []ReviewFinding{
		{ID: "F1", Severity: reviewSeverityHigh, Category: "correctness", Title: "널 포인터 역참조 가능성", RequiredFix: "역참조 전에 널 검사를 추가하세요.", BlocksGate: true},
		{ID: "F2", Severity: reviewSeverityMedium, Category: "maintainability", Title: "중복 코드"},
	}
	run.ArtifactRefs = []string{".kernforge/reviews/rev-ko-1/review.md"}

	out := renderReviewCLIResultCompact(Config{AutoLocale: boolPtr(false)}, run)

	// Defect guards: no raw codenames, no key=value soup, no English-in-Korean.
	for _, banned := range []string{
		"request_class",
		"review_only",
		"blocker=",
		"warning=",
		"note=",
		"요청 class",
		"needs_revision",
		"repair_required",
	} {
		if strings.Contains(out, banned) {
			t.Fatalf("compact Korean review output must not contain %q\n---\n%s", banned, out)
		}
	}

	// Localized presence guards: the user must see plain Korean words.
	for _, want := range []string{
		"요청 유형", // request type label
		"차단",    // blocker count word
		"경고",    // warning count word
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("compact Korean review output must contain %q\n---\n%s", want, out)
		}
	}

	// Information-presence guards: the verdict, a finding, the report path, and
	// a fix must still be visible (the cleanup must not drop info).
	for _, want := range []string{
		"수정 필요",                                 // localized needs_revision verdict
		"널 포인터 역참조 가능성",                         // the actual finding title
		"역참조 전에 널 검사를 추가하세요.",                   // the required fix
		".kernforge/reviews/rev-ko-1/review.md", // report path
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("compact Korean review output dropped required info %q\n---\n%s", want, out)
		}
	}
}

// TestRenderReviewCLIResultCompactEnglishUsesPlainVerdict guards the English
// path: the localized verdict word replaces the raw enum on the human line.
func TestRenderReviewCLIResultCompactEnglishUsesPlainVerdict(t *testing.T) {
	run := ReviewRun{
		ID:              "rev-en-1",
		Target:          reviewTargetChange,
		Mode:            reviewModeCoreBuild,
		RequestAnalysis: ReviewRequestAnalysis{OriginalRequest: "please review this change in English"},
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"F1"},
		},
		Findings: []ReviewFinding{{ID: "F1", Severity: reviewSeverityHigh, Category: "correctness", Title: "off-by-one", BlocksGate: true}},
	}
	out := renderReviewCLIResultCompact(Config{AutoLocale: boolPtr(false)}, run)
	if strings.Contains(out, "needs_revision") {
		t.Fatalf("English compact output must not contain raw verdict enum\n---\n%s", out)
	}
	if !strings.Contains(out, "needs revision") {
		t.Fatalf("English compact output must contain localized verdict\n---\n%s", out)
	}
	if !strings.Contains(out, "off-by-one") {
		t.Fatalf("English compact output dropped the finding title\n---\n%s", out)
	}
}

// TestApprovedWithWarningsZeroWarningsNamesRealDriver asserts the
// approved_with_warnings verdict WITH zero warning findings does not print the
// self-contradictory "0 warning" count and instead names the real driver
// (degraded reviewer evidence because the model review was skipped).
func TestApprovedWithWarningsZeroWarningsNamesRealDriver(t *testing.T) {
	t.Run("korean", func(t *testing.T) {
		run := koreanReviewRun()
		run.Gate = GateDecision{Verdict: reviewVerdictApprovedWithWarnings}
		run.Result = ReviewResult{
			Verdict:        reviewVerdictApprovedWithWarnings,
			DegradedReason: "reviewer route returned weak output",
		}
		summary := reviewResultSummaryForLanguage(run, true)
		for _, banned := range []string{"0 warning", "경고 finding 0", "경고 항목 0", "0개"} {
			if strings.Contains(summary, banned) {
				t.Fatalf("zero-warning approved_with_warnings summary must not contain %q\n---\n%s", banned, summary)
			}
		}
		if !strings.Contains(summary, "모델 리뷰가 생략") {
			t.Fatalf("summary must name the real driver (skipped model review)\n---\n%s", summary)
		}
	})
	t.Run("english", func(t *testing.T) {
		run := ReviewRun{
			RequestAnalysis: ReviewRequestAnalysis{OriginalRequest: "please review this change in English"},
			Gate:            GateDecision{Verdict: reviewVerdictApprovedWithWarnings},
			Result:          ReviewResult{Verdict: reviewVerdictApprovedWithWarnings, DegradedReason: "reviewer route returned weak output"},
		}
		summary := reviewResultSummaryForLanguage(run, false)
		if strings.Contains(summary, "0 warning") {
			t.Fatalf("zero-warning approved_with_warnings summary must not contain '0 warning'\n---\n%s", summary)
		}
		if !strings.Contains(summary, "reviewer evidence is limited") {
			t.Fatalf("summary must name the real driver\n---\n%s", summary)
		}
	})
}

// TestApprovedWithWarningsNonZeroStillCountsWarnings makes sure the cleanup did
// not silence the honest count when there really are warning findings.
func TestApprovedWithWarningsNonZeroStillCountsWarnings(t *testing.T) {
	run := ReviewRun{
		RequestAnalysis: ReviewRequestAnalysis{OriginalRequest: "please review this change in English"},
		Gate:            GateDecision{Verdict: reviewVerdictApprovedWithWarnings, WarningFindings: []string{"W1", "W2"}},
		Result:          ReviewResult{Verdict: reviewVerdictApprovedWithWarnings},
	}
	summary := reviewResultSummaryForLanguage(run, false)
	if !strings.Contains(summary, "2 warning finding(s)") {
		t.Fatalf("real warning count must still be reported\n---\n%s", summary)
	}
}

// TestHumanizeGateStatusMapsToLocalizedWords asserts runtime gate status enums
// are translated to plain words in both locales.
func TestHumanizeGateStatusMapsToLocalizedWords(t *testing.T) {
	cases := []struct {
		raw     string
		korean  string
		english string
	}{
		{runtimeGateStatusNeedsReview, "리뷰 필요", "needs review"},
		{runtimeGateStatusBlocked, "막힘", "blocked"},
		{runtimeGateStatusReady, "준비됨", "ready"},
	}
	for _, tc := range cases {
		ko := humanizeGateStatus(tc.raw, true)
		if ko != tc.korean {
			t.Fatalf("humanizeGateStatus(%q, korean) = %q, want %q", tc.raw, ko, tc.korean)
		}
		if strings.Contains(ko, "_") {
			t.Fatalf("humanizeGateStatus(%q, korean) leaked a raw enum: %q", tc.raw, ko)
		}
		en := humanizeGateStatus(tc.raw, false)
		if en != tc.english {
			t.Fatalf("humanizeGateStatus(%q, english) = %q, want %q", tc.raw, en, tc.english)
		}
		if strings.Contains(en, "_") {
			t.Fatalf("humanizeGateStatus(%q, english) leaked a raw enum: %q", tc.raw, en)
		}
	}
}

// TestProgressInterventionRenderingHasNoRawEnum asserts runtime intervention
// progress lines render as plain sentences and never emit the codename
// verbatim (VerificationUnresolved / FinalLooksPremature).
func TestProgressInterventionRenderingHasNoRawEnum(t *testing.T) {
	t.Setenv("LANG", "ko_KR.UTF-8")
	cfgKo := Config{AutoLocale: boolPtr(true)}
	cfgEn := Config{AutoLocale: boolPtr(false)}

	cases := []RuntimeInterventionKind{
		RuntimeInterventionVerificationUnresolved,
		RuntimeInterventionFinalLooksPremature,
	}
	for _, kind := range cases {
		event := ProgressEvent{
			Kind:                progressKindRuntimeIntervention,
			RuntimeIntervention: string(kind),
		}
		ko := formatProgressEventMessage(cfgKo, event)
		en := formatProgressEventMessage(cfgEn, event)
		for _, raw := range []string{"VerificationUnresolved", "FinalLooksPremature", string(kind)} {
			if strings.Contains(ko, raw) {
				t.Fatalf("Korean intervention line leaked raw enum %q: %q", raw, ko)
			}
			if strings.Contains(en, raw) {
				t.Fatalf("English intervention line leaked raw enum %q: %q", raw, en)
			}
		}
		if strings.TrimSpace(ko) == "" || strings.TrimSpace(en) == "" {
			t.Fatalf("intervention line must not be empty for %q (ko=%q en=%q)", kind, ko, en)
		}
	}

	// The Korean verification-unresolved line should explain it held the finish
	// until verification completes (information must remain).
	verif := formatProgressEventMessage(cfgKo, ProgressEvent{
		Kind:                progressKindRuntimeIntervention,
		RuntimeIntervention: string(RuntimeInterventionVerificationUnresolved),
	})
	if !strings.Contains(verif, "검증") {
		t.Fatalf("verification-unresolved Korean line should mention verification\n---\n%s", verif)
	}
}

// TestRenderReviewRunMarkdownOutcomeBeforeDiagnostics asserts the markdown
// artifact leads with the human outcome (Summary, Findings) and pushes the
// lifecycle/ledger Diagnostics to the back.
func TestRenderReviewRunMarkdownOutcomeBeforeDiagnostics(t *testing.T) {
	run := ReviewRun{
		ID:              "rev-md-1",
		SchemaVersion:   "1",
		Target:          reviewTargetChange,
		Mode:            reviewModeCoreBuild,
		RequestAnalysis: ReviewRequestAnalysis{OriginalRequest: "please review this change in English"},
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			Action:           reviewGateActionRepairRequired,
			BlockingFindings: []string{"F1"},
		},
		Result:    ReviewResult{Summary: "One blocking issue found in the guard clause."},
		Findings:  []ReviewFinding{{ID: "F1", Severity: reviewSeverityHigh, Category: "correctness", Title: "missing guard", BlocksGate: true}},
		ChangeSet: ReviewChangeSet{ChangedPaths: []string{"main.cpp"}},
		Lifecycle: &ReviewRequestLifecycle{
			RequestClass: reviewRequestClassReviewThenModify,
			Phase:        reviewLifecyclePhasePostChangeReview,
			RouteMode:    "single",
		},
	}
	out := renderReviewRunMarkdown(run)

	summaryIdx := strings.Index(out, "## Summary")
	findingsIdx := strings.Index(out, "## Blocking Findings")
	diagIdx := strings.Index(out, "## Diagnostics")
	if summaryIdx < 0 || diagIdx < 0 {
		t.Fatalf("expected Summary and Diagnostics sections\n---\n%s", out)
	}
	if !(summaryIdx < diagIdx) {
		t.Fatalf("Summary must come before Diagnostics (summary=%d diag=%d)", summaryIdx, diagIdx)
	}
	if findingsIdx >= 0 && !(findingsIdx < diagIdx) {
		t.Fatalf("Findings must come before Diagnostics (findings=%d diag=%d)", findingsIdx, diagIdx)
	}

	// Information presence: changed file, the verdict (humanized), and the
	// finding title must all be in the artifact. The raw enums live only in the
	// Diagnostics block.
	for _, want := range []string{"main.cpp", "needs revision", "missing guard", "One blocking issue"} {
		if !strings.Contains(out, want) {
			t.Fatalf("markdown artifact dropped required info %q\n---\n%s", want, out)
		}
	}
	// The human leading section (before Diagnostics) must not show the raw
	// request_class token; that belongs in Diagnostics only.
	human := out[:diagIdx]
	if strings.Contains(human, "review_then_modify") {
		t.Fatalf("human section leaked raw request_class codename\n---\n%s", human)
	}
}

// TestMCPReviewReplyLeadsWithHumanSummary asserts the MCP review reply leads
// with a human-readable summary before the JSON block, so a client renders the
// outcome first rather than parsing a struct dump.
func TestMCPReviewReplyLeadsWithHumanSummary(t *testing.T) {
	run := ReviewRun{
		ID:              "rev-mcp-1",
		SchemaVersion:   "1",
		MachineStatus:   reviewMachineStatusWarning,
		Target:          reviewTargetChange,
		Mode:            reviewModeCoreBuild,
		RequestAnalysis: ReviewRequestAnalysis{OriginalRequest: "please review this change in English"},
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			Action:           reviewGateActionRepairRequired,
			BlockingFindings: []string{"F1"},
		},
		Result:   ReviewResult{Summary: "One blocking issue found."},
		Findings: []ReviewFinding{{ID: "F1", Severity: reviewSeverityHigh, Category: "correctness", Title: "missing guard", BlocksGate: true}},
	}
	out := renderReviewMCPResponse(run, 80000)

	jsonIdx := strings.Index(out, "```json")
	if jsonIdx < 0 {
		t.Fatalf("MCP reply must contain a JSON block\n---\n%s", out)
	}
	head := out[:jsonIdx]
	// The human summary (verdict, finding title) must appear before the JSON.
	if !strings.Contains(head, "needs revision") {
		t.Fatalf("MCP reply must lead with the localized verdict before JSON\n---\n%s", head)
	}
	if !strings.Contains(head, "missing guard") {
		t.Fatalf("MCP reply must lead with the finding before JSON\n---\n%s", head)
	}
	// The structured JSON must still carry the raw field names tools depend on.
	tail := out[jsonIdx:]
	for _, field := range []string{`"machine_status"`, `"request_class"`, `"findings"`, `"gate"`} {
		if !strings.Contains(tail, field) {
			t.Fatalf("MCP JSON dropped machine field %q\n---\n%s", field, tail)
		}
	}
}

// TestPasswordRedactionDoesNotFireOnPlainWord asserts the password_assignment
// redaction leaves harmless string literals such as Token: "security" intact,
// while still redacting a value that looks like a real secret.
func TestPasswordRedactionDoesNotFireOnPlainWord(t *testing.T) {
	plain := `Token: "security"`
	out, redacted := redactPasswordAssignments(plain)
	if redacted {
		t.Fatalf("redaction must not fire on a plain word value: %q -> %q", plain, out)
	}
	if out != plain {
		t.Fatalf("plain value must pass through unchanged: %q -> %q", plain, out)
	}
	if strings.Contains(out, "[REDACTED") {
		t.Fatalf("plain value must not be redacted: %q", out)
	}

	// A genuinely secret-looking value must still be redacted (we did not
	// disable the guard, only narrow it).
	secret := `password: "Aa1!supersecretvalue"`
	redOut, didRedact := redactPasswordAssignments(secret)
	if !didRedact {
		t.Fatalf("a credential-like value must still be redacted: %q -> %q", secret, redOut)
	}
	if !strings.Contains(redOut, "[REDACTED:password_assignment]") {
		t.Fatalf("redacted credential must be masked: %q", redOut)
	}
}
