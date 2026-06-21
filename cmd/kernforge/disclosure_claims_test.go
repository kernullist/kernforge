package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// withScriptedDisclosureRunner installs a scripted disclosure cross-check model
// runner for the duration of one test and restores the previous (nil) seam after.
// It keeps the pass hermetic: no real provider, no network.
func withScriptedDisclosureRunner(t *testing.T, raw string, runErr error, capture *string) {
	t.Helper()
	prev := disclosureClaimsModelRunner
	disclosureClaimsModelRunner = func(ctx context.Context, a *Agent, prompt string) (string, error) {
		if capture != nil {
			*capture = prompt
		}
		if runErr != nil {
			return "", runErr
		}
		return raw, nil
	}
	t.Cleanup(func() {
		disclosureClaimsModelRunner = prev
	})
}

func disclosureModificationSession(t *testing.T, root string, goal string, changedPath string) *Session {
	t.Helper()
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: goal}}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-disclosure",
		Goal:   goal,
		Status: patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ID:          "patch-disclosure-001",
			ToolName:    "apply_patch",
			Status:      "success",
			UnifiedDiff: "diff --git a/" + changedPath + " b/" + changedPath + "\n--- a/" + changedPath + "\n+++ b/" + changedPath + "\n@@ -1 +1 @@\n-old\n+new\n",
			Paths: []PatchPathChange{{
				Path:      changedPath,
				Operation: "modify",
			}},
		}},
	}
	return session
}

func disclosureTestAgent(root string, session *Session) *Agent {
	cfg := DefaultConfig(root)
	cfg.Review.ModelReviewConsent = modelReviewConsentAlways
	return &Agent{
		Config:        cfg,
		Workspace:     Workspace{BaseRoot: root, Root: root},
		Session:       session,
		ReviewerModel: "reviewer-model",
	}
}

// TestDisclosureClaimsCheckFlagsNoChangeClaimAgainstDiff verifies a final answer
// that claims no files changed contradicts the recorded patch diff.
func TestDisclosureClaimsCheckFlagsNoChangeClaimAgainstDiff(t *testing.T) {
	root := t.TempDir()
	session := disclosureModificationSession(t, root, "fix main.go", "main.go")
	agent := disclosureTestAgent(root, session)

	scripted := strings.Join([]string{
		"DISCLOSURE_CHECK",
		"verdict: contradicted",
		"contradictions:",
		"- reason: changed_files",
		"  claim: no files were changed",
		"  observed: patch transaction recorded a diff for main.go",
	}, "\n")
	var capturedPrompt string
	withScriptedDisclosureRunner(t, scripted, nil, &capturedPrompt)

	reply := "No files were changed. Validation: verification not run. Remaining risk: no known remaining blocker."
	report := agent.buildCodingHarnessReport(reply, true, false)

	if report.Approved {
		t.Fatalf("expected disclosure contradiction to block approval, findings=%#v", report.Outcome.Findings)
	}
	if !codingHarnessReportHasFinding(report.Outcome.Findings, disclosureContradictionFindingTitle) {
		t.Fatalf("expected disclosure contradiction finding, got %#v", report.Outcome.Findings)
	}
	if session.LastDisclosureClaimsCheck == nil || len(session.LastDisclosureClaimsCheck.Contradictions) != 1 {
		t.Fatalf("expected one recorded contradiction, got %#v", session.LastDisclosureClaimsCheck)
	}
	if session.LastDisclosureClaimsCheck.Contradictions[0].Reason != disclosureContradictionChangedFiles {
		t.Fatalf("expected changed_files reason, got %#v", session.LastDisclosureClaimsCheck.Contradictions[0])
	}
	if !strings.Contains(capturedPrompt, "main.go") {
		t.Fatalf("expected evidence bundle to include changed path in prompt, got %q", capturedPrompt)
	}
	// The contradiction must route to the final-answer-only correction reason.
	correction := finalAnswerCorrectionVisibilityFromReport(&report, false)
	if correction == nil || !containsString(correction.Reasons, "disclosure_contradiction") {
		t.Fatalf("expected disclosure_contradiction correction reason, got %#v", correction)
	}
}

// TestDisclosureClaimsCheckFlagsVerificationClaimAgainstFailure verifies a final
// answer that claims verification passed contradicts a recorded verification
// failure.
func TestDisclosureClaimsCheckFlagsVerificationClaimAgainstFailure(t *testing.T) {
	root := t.TempDir()
	session := disclosureModificationSession(t, root, "fix main.go", "main.go")
	session.LastVerification = &VerificationReport{
		Steps: []VerificationStep{{
			Label:      "build",
			Status:     VerificationFailed,
			FirstError: "undefined: Foo",
		}},
	}
	agent := disclosureTestAgent(root, session)

	scripted := strings.Join([]string{
		"DISCLOSURE_CHECK",
		"verdict: contradicted",
		"contradictions:",
		"- reason: verification",
		"  claim: build passed",
		"  observed: recorded verification status is failed",
	}, "\n")
	withScriptedDisclosureRunner(t, scripted, nil, nil)

	reply := "Changed files: main.go. Build passed and tests succeeded. Remaining risk: no known remaining blocker."
	report := agent.buildCodingHarnessReport(reply, true, false)

	if !codingHarnessReportHasFinding(report.Outcome.Findings, disclosureContradictionFindingTitle) {
		t.Fatalf("expected disclosure verification contradiction, got %#v", report.Outcome.Findings)
	}
	if session.LastDisclosureClaimsCheck == nil ||
		len(session.LastDisclosureClaimsCheck.Contradictions) != 1 ||
		session.LastDisclosureClaimsCheck.Contradictions[0].Reason != disclosureContradictionVerification {
		t.Fatalf("expected one verification contradiction, got %#v", session.LastDisclosureClaimsCheck)
	}
}

// TestDisclosureClaimsCheckHonestAnswerHasNoContradiction verifies an honest
// answer matching reality produces no contradiction even when the model is run.
func TestDisclosureClaimsCheckHonestAnswerHasNoContradiction(t *testing.T) {
	root := t.TempDir()
	session := disclosureModificationSession(t, root, "fix main.go", "main.go")
	agent := disclosureTestAgent(root, session)

	withScriptedDisclosureRunner(t, "DISCLOSURE_CHECK\nverdict: consistent\ncontradictions:\n", nil, nil)

	reply := "Changed files: main.go. Self-review: no code blocker found. Validation: verification not run. Remaining risk: no known remaining blocker."
	report := agent.buildCodingHarnessReport(reply, true, false)

	if codingHarnessReportHasFinding(report.Outcome.Findings, disclosureContradictionFindingTitle) {
		t.Fatalf("honest answer must not produce a disclosure contradiction, got %#v", report.Outcome.Findings)
	}
	if session.LastDisclosureClaimsCheck == nil || session.LastDisclosureClaimsCheck.Status != "completed" {
		t.Fatalf("expected a completed disclosure check with no contradictions, got %#v", session.LastDisclosureClaimsCheck)
	}
	if len(session.LastDisclosureClaimsCheck.Contradictions) != 0 {
		t.Fatalf("expected zero contradictions, got %#v", session.LastDisclosureClaimsCheck.Contradictions)
	}
}

// TestDisclosureClaimsCheckSkipsTrivialTurn verifies the pass does not run on a
// trivial/status-only turn (no modification, no disclosure claims).
func TestDisclosureClaimsCheckSkipsTrivialTurn(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	contract := buildAcceptanceContract("what is the current status", TurnIntentGeneral, false, false, false)
	contract.Mode = "command"
	session.AcceptanceContract = &contract

	agent := disclosureTestAgent(root, session)

	ran := false
	prev := disclosureClaimsModelRunner
	disclosureClaimsModelRunner = func(ctx context.Context, a *Agent, prompt string) (string, error) {
		ran = true
		return "DISCLOSURE_CHECK\nverdict: contradicted\ncontradictions:\n- reason: changed_files\n  claim: x\n  observed: y\n", nil
	}
	t.Cleanup(func() { disclosureClaimsModelRunner = prev })

	bundle := agent.collectDisclosureEvidence()
	if agent.shouldRunDisclosureClaimsCheck("Here is the status: nothing changed.", bundle) {
		t.Fatalf("trivial/status-only turn must not run the disclosure cross-check")
	}
	report := agent.buildCodingHarnessReport("Here is the status.", false, false)
	if ran {
		t.Fatalf("disclosure runner must not be invoked on a trivial turn")
	}
	if session.LastDisclosureClaimsCheck != nil {
		t.Fatalf("no disclosure check should be recorded on a trivial turn, got %#v", session.LastDisclosureClaimsCheck)
	}
	if codingHarnessReportHasFinding(report.Outcome.Findings, disclosureContradictionFindingTitle) {
		t.Fatalf("trivial turn must not produce a disclosure contradiction, got %#v", report.Outcome.Findings)
	}
}

// TestDisclosureClaimsCheckSkipsWhenNoRunnerWired verifies that with the default
// (nil) seam the pass skips and leaves existing behavior unchanged.
func TestDisclosureClaimsCheckSkipsWhenNoRunnerWired(t *testing.T) {
	root := t.TempDir()
	session := disclosureModificationSession(t, root, "fix main.go", "main.go")
	agent := disclosureTestAgent(root, session)

	if disclosureClaimsModelRunner != nil {
		t.Fatalf("default disclosure runner seam must be nil")
	}
	reply := "No files were changed. Validation: verification not run. Remaining risk: no known remaining blocker."
	bundle := agent.collectDisclosureEvidence()
	if agent.shouldRunDisclosureClaimsCheck(reply, bundle) {
		t.Fatalf("disclosure cross-check must skip when no model runner is wired")
	}
	report := agent.buildCodingHarnessReport(reply, true, false)
	if session.LastDisclosureClaimsCheck != nil {
		t.Fatalf("no disclosure check should be recorded without a runner, got %#v", session.LastDisclosureClaimsCheck)
	}
	if codingHarnessReportHasFinding(report.Outcome.Findings, disclosureContradictionFindingTitle) {
		t.Fatalf("no disclosure contradiction without a runner, got %#v", report.Outcome.Findings)
	}
}

// TestDisclosureClaimsCheckSkipsWhenModelErrors verifies a model error skips the
// pass (no fabricated verdict, no contradiction) and leaves behavior unchanged.
func TestDisclosureClaimsCheckSkipsWhenModelErrors(t *testing.T) {
	root := t.TempDir()
	session := disclosureModificationSession(t, root, "fix main.go", "main.go")
	agent := disclosureTestAgent(root, session)

	withScriptedDisclosureRunner(t, "", errors.New("route down"), nil)

	reply := "No files were changed. Validation: verification not run. Remaining risk: no known remaining blocker."
	bundle := agent.collectDisclosureEvidence()
	check := agent.runDisclosureClaimsCheck(context.Background(), reply, bundle)
	if check.Status != "skipped" {
		t.Fatalf("expected skipped status on model error, got %#v", check)
	}
	if len(check.Contradictions) != 0 {
		t.Fatalf("model error must not fabricate contradictions, got %#v", check.Contradictions)
	}
	if len(disclosureClaimsContradictionFindings(check)) != 0 {
		t.Fatalf("model error must not yield findings, got %#v", check)
	}
}

// TestDisclosureClaimsCheckDropsUnsupportedContradiction verifies a model that
// over-claims a contradiction the bundle does not back is filtered out, so the
// pass cannot manufacture a gate.
func TestDisclosureClaimsCheckDropsUnsupportedContradiction(t *testing.T) {
	root := t.TempDir()
	session := disclosureModificationSession(t, root, "fix main.go", "main.go")
	// No verification report and no edit-loop verification recorded, so a
	// "verification" contradiction is unsupported and must be dropped.
	agent := disclosureTestAgent(root, session)

	scripted := strings.Join([]string{
		"DISCLOSURE_CHECK",
		"verdict: contradicted",
		"contradictions:",
		"- reason: verification",
		"  claim: build passed",
		"  observed: nothing recorded",
	}, "\n")
	withScriptedDisclosureRunner(t, scripted, nil, nil)

	reply := "Changed files: main.go. Build passed. Remaining risk: no known remaining blocker."
	bundle := agent.collectDisclosureEvidence()
	check := agent.runDisclosureClaimsCheck(context.Background(), reply, bundle)
	if len(check.Contradictions) != 0 {
		t.Fatalf("unsupported verification contradiction must be dropped, got %#v", check.Contradictions)
	}
}

// scriptedDisclosureReviewerClient is a hermetic ProviderClient that returns a
// canned DISCLOSURE_CHECK contract for the bounded single-model disclosure pass.
// It exercises the REAL runner (defaultDisclosureClaimsModelRun) without any
// network or external binary, and records the prompts it received.
type scriptedDisclosureReviewerClient struct {
	raw     string
	err     error
	prompts []string
	calls   int
}

func (c *scriptedDisclosureReviewerClient) Name() string { return "scripted-reviewer" }

func (c *scriptedDisclosureReviewerClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	c.calls++
	for _, m := range req.Messages {
		c.prompts = append(c.prompts, m.Text)
	}
	if c.err != nil {
		return ChatResponse{}, c.err
	}
	return ChatResponse{Message: Message{Role: "assistant", Text: c.raw}}, nil
}

// liveDisclosureReviewerAgent builds an agent whose disclosure cross-check uses a
// real reviewer ROUTE (scripted client/model) and no injected global seam, so the
// production resolver path (disclosureClaimsRunner -> defaultDisclosureClaimsModelRun)
// is exercised end-to-end.
func liveDisclosureReviewerAgent(root string, session *Session, client ProviderClient) *Agent {
	cfg := DefaultConfig(root)
	cfg.Review.ModelReviewConsent = modelReviewConsentAlways
	return &Agent{
		Config:         cfg,
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		ReviewerClient: client,
		ReviewerModel:  "reviewer-model",
	}
}

// TestDisclosureClaimsCheckLivePathFlagsContradiction drives the LIVE reviewer
// path (ReviewerClient set, no injected global seam) through runPreFinalCodingHarnesses
// and verifies a contradiction is detected end-to-end via the real bounded
// single-model invocation, blocks the final answer, and records the check.
func TestDisclosureClaimsCheckLivePathFlagsContradiction(t *testing.T) {
	if disclosureClaimsModelRunner != nil {
		t.Fatalf("test seam must be nil so the real reviewer-route runner is exercised")
	}
	root := t.TempDir()
	session := disclosureModificationSession(t, root, "fix main.go", "main.go")
	client := &scriptedDisclosureReviewerClient{
		raw: strings.Join([]string{
			"DISCLOSURE_CHECK",
			"verdict: contradicted",
			"contradictions:",
			"- reason: changed_files",
			"  claim: no files were changed",
			"  observed: patch transaction recorded a diff for main.go",
		}, "\n"),
	}
	agent := liveDisclosureReviewerAgent(root, session, client)

	reply := "No files were changed. Validation: verification not run. Remaining risk: no known remaining blocker."
	approved, feedback := agent.runPreFinalCodingHarnesses(context.Background(), reply, true, false)
	if approved {
		t.Fatalf("expected live-path disclosure contradiction to block the final answer, feedback=%q", feedback)
	}
	if client.calls == 0 {
		t.Fatalf("expected the real reviewer route to be called on the live path")
	}
	if session.LastDisclosureClaimsCheck == nil ||
		session.LastDisclosureClaimsCheck.Status != "completed" ||
		len(session.LastDisclosureClaimsCheck.Contradictions) != 1 ||
		session.LastDisclosureClaimsCheck.Contradictions[0].Reason != disclosureContradictionChangedFiles {
		t.Fatalf("expected one recorded changed_files contradiction on the live path, got %#v", session.LastDisclosureClaimsCheck)
	}
	if !strings.Contains(feedback, disclosureContradictionFindingTitle) {
		t.Fatalf("expected blocking feedback to cite the disclosure contradiction, got %q", feedback)
	}
	// The live-path block must route to the disclosure_contradiction final-answer
	// correction reason, exactly like the deterministic path.
	if session.LastCodingHarnessReport == nil {
		t.Fatalf("expected a recorded coding harness report on the live-path block")
	}
	correction := finalAnswerCorrectionVisibilityFromReport(session.LastCodingHarnessReport, false)
	if correction == nil || !containsString(correction.Reasons, "disclosure_contradiction") {
		t.Fatalf("expected disclosure_contradiction correction reason, got %#v", correction)
	}
	// At least one prompt the reviewer saw must carry the changed path evidence.
	sawPath := false
	for _, p := range client.prompts {
		if strings.Contains(p, "main.go") {
			sawPath = true
			break
		}
	}
	if !sawPath {
		t.Fatalf("expected the reviewer prompt to include the changed path evidence, prompts=%#v", client.prompts)
	}
}

// TestDisclosureClaimsCheckLivePathHonestAnswerApproves verifies the live path
// approves when the real reviewer route reports a consistent verdict.
func TestDisclosureClaimsCheckLivePathHonestAnswerApproves(t *testing.T) {
	root := t.TempDir()
	session := disclosureModificationSession(t, root, "fix main.go", "main.go")
	client := &scriptedDisclosureReviewerClient{
		raw: "DISCLOSURE_CHECK\nverdict: consistent\ncontradictions:\n",
	}
	agent := liveDisclosureReviewerAgent(root, session, client)

	reply := "Changed files: main.go. Validation: verification not run. Remaining risk: no known remaining blocker."
	approved, feedback := agent.runPreFinalCodingHarnesses(context.Background(), reply, true, false)
	if !approved {
		t.Fatalf("expected an honest answer to pass the live disclosure gate, feedback=%q", feedback)
	}
	if client.calls == 0 {
		t.Fatalf("expected the real reviewer route to be called on the live path")
	}
	if session.LastDisclosureClaimsCheck == nil || session.LastDisclosureClaimsCheck.Status != "completed" {
		t.Fatalf("expected a completed disclosure check, got %#v", session.LastDisclosureClaimsCheck)
	}
	if len(session.LastDisclosureClaimsCheck.Contradictions) != 0 {
		t.Fatalf("expected zero contradictions, got %#v", session.LastDisclosureClaimsCheck.Contradictions)
	}
}

// TestDisclosureClaimsCheckSkipsWhenNoReviewerRoute verifies that with no injected
// seam AND no reviewer route configured, the resolver returns nil so the pass
// skips cleanly and does not call any model.
func TestDisclosureClaimsCheckSkipsWhenNoReviewerRoute(t *testing.T) {
	if disclosureClaimsModelRunner != nil {
		t.Fatalf("test seam must be nil for this case")
	}
	root := t.TempDir()
	session := disclosureModificationSession(t, root, "fix main.go", "main.go")
	// disclosureTestAgent sets ReviewerModel but no ReviewerClient/Client, so no
	// usable route exists and the resolver must yield nil.
	agent := disclosureTestAgent(root, session)
	agent.ReviewerModel = ""

	reply := "No files were changed. Validation: verification not run. Remaining risk: no known remaining blocker."
	bundle := agent.collectDisclosureEvidence()
	if agent.disclosureClaimsRunner() != nil {
		t.Fatalf("resolver must be nil with no reviewer route")
	}
	if agent.shouldRunDisclosureClaimsCheck(reply, bundle) {
		t.Fatalf("disclosure cross-check must skip with no reviewer route")
	}
	// runDisclosureClaimsLivePath returns (blocked, feedback); blocked must be false.
	blocked, feedback := agent.runDisclosureClaimsLivePath(context.Background(), reply, true, false)
	if blocked {
		t.Fatalf("no-route live path must not block, feedback=%q", feedback)
	}
	if session.LastDisclosureClaimsCheck != nil {
		t.Fatalf("no disclosure check should be recorded with no reviewer route, got %#v", session.LastDisclosureClaimsCheck)
	}
}

// TestDisclosureClaimsCheckLivePathSkipsWhenConsentNever verifies that even with a
// real reviewer route, a model_review_consent=never policy skips the pass cleanly
// (no model call, no fabricated verdict, no block).
func TestDisclosureClaimsCheckLivePathSkipsWhenConsentNever(t *testing.T) {
	root := t.TempDir()
	session := disclosureModificationSession(t, root, "fix main.go", "main.go")
	client := &scriptedDisclosureReviewerClient{
		raw: strings.Join([]string{
			"DISCLOSURE_CHECK",
			"verdict: contradicted",
			"contradictions:",
			"- reason: changed_files",
			"  claim: no files were changed",
			"  observed: patch transaction recorded a diff for main.go",
		}, "\n"),
	}
	agent := liveDisclosureReviewerAgent(root, session, client)
	agent.Config.Review.ModelReviewConsent = modelReviewConsentNever

	reply := "No files were changed. Validation: verification not run. Remaining risk: no known remaining blocker."
	bundle := agent.collectDisclosureEvidence()
	// shouldRun short-circuits on consent=never before any model interaction.
	if agent.shouldRunDisclosureClaimsCheck(reply, bundle) {
		t.Fatalf("consent=never must skip the disclosure cross-check")
	}
	approved, feedback := agent.runPreFinalCodingHarnesses(context.Background(), reply, true, false)
	if !approved {
		t.Fatalf("consent=never must not block the final answer, feedback=%q", feedback)
	}
	if client.calls != 0 {
		t.Fatalf("consent=never must not call the reviewer model, calls=%d", client.calls)
	}
	if session.LastDisclosureClaimsCheck != nil {
		t.Fatalf("no disclosure check should be recorded when consent=never, got %#v", session.LastDisclosureClaimsCheck)
	}
}

func TestParseDisclosureClaimsResult(t *testing.T) {
	raw := strings.Join([]string{
		"some preamble",
		"DISCLOSURE_CHECK",
		"verdict: contradicted",
		"contradictions:",
		"- reason: changed_files",
		"  claim: no files changed",
		"  observed: diff present",
		"- reason: bogus_reason",
		"  claim: x",
		"- reason: remaining-risk",
		"  claim: no remaining risk",
		"  observed: edit loop records risk",
	}, "\n")
	got := parseDisclosureClaimsResult(raw)
	if len(got) != 2 {
		t.Fatalf("expected two recognized contradictions (bogus dropped), got %#v", got)
	}
	if got[0].Reason != disclosureContradictionChangedFiles {
		t.Fatalf("expected changed_files first, got %#v", got[0])
	}
	if got[1].Reason != disclosureContradictionRemainingRisk {
		t.Fatalf("expected remaining_risk normalized from 'remaining-risk', got %#v", got[1])
	}
}

func TestDisclosureEvidenceRedactsSecretsBeforeModel(t *testing.T) {
	root := t.TempDir()
	session := disclosureModificationSession(t, root, "fix main.go", "main.go")
	session.ActivePatchTransaction.Entries[0].UnifiedDiff = "diff --git a/main.go b/main.go\n+const token = \"sk-abcdefghijklmnopqrstuvwxyz123456\"\n"
	agent := disclosureTestAgent(root, session)

	var capturedPrompt string
	withScriptedDisclosureRunner(t, "DISCLOSURE_CHECK\nverdict: consistent\ncontradictions:\n", nil, &capturedPrompt)

	reply := "Changed files: main.go. Validation: verification not run. Remaining risk: no known remaining blocker."
	bundle := agent.collectDisclosureEvidence()
	check := agent.runDisclosureClaimsCheck(context.Background(), reply, bundle)
	if !check.RedactedInput {
		t.Fatalf("expected redacted input flag when diff carries a secret, got %#v", check)
	}
	if strings.Contains(capturedPrompt, "sk-abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("secret must be redacted before reaching the model prompt, got %q", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "[REDACTED:openai_api_key]") {
		t.Fatalf("expected redaction marker in prompt, got %q", capturedPrompt)
	}
}
