package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testOperatorStatusReviewRun() ReviewRun {
	return ReviewRun{
		ID:            "review-operator-1",
		SchemaVersion: reviewSchemaVersion,
		Trigger:       "post_change",
		Target:        reviewTargetChange,
		Mode:          reviewModeGeneralChange,
		Flow:          "change_review",
		RequestClass:  reviewRequestClassModifyThenReview,
		RequestAnalysis: ReviewRequestAnalysis{
			RequestClass:           reviewRequestClassModifyThenReview,
			RequestClassReason:     "test selected modification lifecycle",
			RequestClassConfidence: 0.91,
		},
		CreatedAt: time.Now(),
		ChangeSet: ReviewChangeSet{ChangedPaths: []string{"main.go"}},
		Evidence: ReviewEvidencePack{
			Sources:              []string{"git_diff"},
			VerificationRequired: true,
		},
		Gate: GateDecision{
			Verdict: reviewVerdictNeedsRevision,
			Action:  reviewGateActionRepairRequired,
			Reason:  "blocking review findings require repair",
			NextCommands: []ReviewNextCommand{{
				ID:             "repair",
				Command:        "/continuity continue from review",
				Reason:         "latest review has blocking findings",
				Safety:         "safe_local",
				ClientHint:     "Repair RF-OPS-1 and rerun review.",
				ExpectedResult: "review blockers are resolved",
			}},
		},
		Result: ReviewResult{
			Verdict: reviewVerdictNeedsRevision,
			Summary: "blocking review findings require repair",
		},
		SingleModelPolicy: SingleModelReviewPolicy{
			Enabled:             true,
			IndependenceLevel:   "single_model",
			NoCrossReviewReason: "single_model_mode",
		},
		SingleModelSecondPass: &SingleModelSecondPassReview{
			Enabled:       true,
			Status:        "cached",
			CacheHit:      true,
			Model:         "openai-codex-subscription/gpt-5.4",
			ReviewedPaths: []string{"main.go"},
			FindingCount:  1,
		},
		ObligationLedger: ReviewObligationLedger{
			TotalCount:      1,
			OpenCount:       1,
			OpenRepairCount: 1,
			Summary:         []string{"repair=1"},
			Items: []ReviewObligation{{
				ID:             "RO-OPS-1",
				Type:           reviewObligationTypeRepair,
				Status:         reviewObligationStatusOpen,
				Blocking:       true,
				Title:          "Repair bounds check",
				RequiredAction: "Fix RF-OPS-1 before finalizing.",
			}},
		},
		Findings: []ReviewFinding{{
			ID:          "RF-OPS-1",
			Source:      "model",
			Severity:    reviewSeverityHigh,
			Category:    "correctness",
			Title:       "Bounds check is missing",
			Path:        "main.go",
			Line:        7,
			RequiredFix: "Add the missing bounds check.",
			BlocksGate:  true,
		}},
		ArtifactRefs: []string{".kernforge/reviews/review-operator-1.md"},
	}
}

func TestOperatorStatusCompactOutputIncludesLifecycleGatesBlockersAndNextCommand(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	run := testOperatorStatusReviewRun()
	session.LastReviewRun = &run
	var output bytes.Buffer
	rt := &runtimeState{
		writer:    &output,
		ui:        NewUI(),
		session:   session,
		workspace: Workspace{BaseRoot: root, Root: root},
	}

	rt.printRuntimeGateStatus(runtimeGateActionFinalAnswer)

	text := output.String()
	for _, want := range []string{
		"operator_status",
		"class=modify_then_review",
		"kind=modify_then_review",
		"confidence=0.91",
		"phase=blocked",
		"route=single_model",
		"gates",
		"review=needs_revision",
		"repair=required",
		"verification=gap_recorded",
		"second_pass_state",
		"single_model_second_pass_cached",
		"blocker_summary",
		"code_repair_blocker=1",
		"remaining_obligations",
		"next_command",
		"/continuity continue from review",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected compact status output to contain %q, got:\n%s", want, text)
		}
	}
}

func TestOperatorStatusDetailIncludesLifecycleTimelineAndEvidenceRefs(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	run := testOperatorStatusReviewRun()
	session.LastReviewRun = &run
	var output bytes.Buffer
	rt := &runtimeState{
		writer:    &output,
		ui:        NewUI(),
		session:   session,
		workspace: Workspace{BaseRoot: root, Root: root},
	}

	rt.printRuntimeGateStatusDetail(runtimeGateActionFinalAnswer)

	text := output.String()
	for _, want := range []string{
		"Lifecycle Timeline",
		"classified_request",
		"collecting_context",
		"pre_write_review",
		"final_answer_contract",
		"evidence=",
		"Blocker Details",
		"next_safe_action=",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected detail status output to contain %q, got:\n%s", want, text)
		}
	}
}

func TestOperatorProgressLinesArePhaseAwareAndNonRepeated(t *testing.T) {
	var progress []string
	agent := &Agent{
		Config:       Config{AutoLocale: boolPtr(false)},
		EmitProgress: func(message string) { progress = append(progress, message) },
	}
	rt := &runtimeState{
		cfg:   Config{AutoLocale: boolPtr(false)},
		agent: agent,
	}

	emitReviewPipelineProgress(rt, testOperatorStatusReviewRun(), 3, "model review", "모델 검토", "Review the gathered evidence.", "증거를 검토합니다.")
	agent.emitRepairWorkflowProgress("fix the bug", 5, "verification", "검증", "Run verification.", "검증을 실행합니다.")

	if len(progress) != 2 {
		t.Fatalf("expected two progress lines, got %#v", progress)
	}
	seen := map[string]bool{}
	for _, line := range progress {
		if seen[line] {
			t.Fatalf("progress line repeated: %#v", progress)
		}
		seen[line] = true
		for _, want := range []string{"phase=", "status=running", "reason=", "waiting_on=", "next="} {
			if !strings.Contains(line, want) {
				t.Fatalf("expected phase-aware progress token %q in %q", want, line)
			}
		}
	}
}

func TestBlockerSummaryPrioritizesCodeRepairBeforeCrossReviewNoise(t *testing.T) {
	run := testOperatorStatusReviewRun()
	run.CrossReviewTriage = &CrossReviewTriageLedger{
		Items: []CrossReviewTriageEntry{{
			FindingID:    "RF-CROSS-1",
			ReviewerRole: "cross_reviewer",
			Severity:     reviewSeverityMedium,
			Path:         "main.go",
			Title:        "Needs product decision",
			TriageStatus: crossReviewTriageNeedsUserDecision,
		}},
	}

	summary := buildReviewBlockerSummary(&run, nil, nil)
	if summary == nil || len(summary.Primary) < 2 {
		t.Fatalf("expected repair and user-decision blockers, got %#v", summary)
	}
	if got := summary.Primary[0].Class; got != reviewBlockerClassCodeRepair {
		t.Fatalf("primary blocker class = %q, want %q; summary=%#v", got, reviewBlockerClassCodeRepair, summary.Primary)
	}
	if summary.Counts[reviewBlockerClassUserDecisionRequired] == 0 {
		t.Fatalf("expected user decision blocker count, got %#v", summary.Counts)
	}
}

func TestNeedsUserDecisionRendersActionableGuidanceInMarkdownAndMCP(t *testing.T) {
	run := testOperatorStatusReviewRun()
	run.CrossReviewTriage = &CrossReviewTriageLedger{
		Items: []CrossReviewTriageEntry{{
			FindingID:    "RF-CROSS-2",
			ReviewerRole: "cross_reviewer",
			Severity:     reviewSeverityHigh,
			Path:         "cmd/kernforge/main.go",
			Line:         42,
			Symbol:       "run",
			Title:        "Ambiguous reviewer claim",
			RequiredFix:  "Inspect the exact dispatch path before editing.",
			TriageStatus: crossReviewTriageNeedsUserDecision,
			EvidenceRefs: []string{"review:evidence"},
		}},
	}

	markdown := renderReviewRunMarkdown(run)
	mcp := renderReviewMCPResponse(run, 40000)
	for _, rendered := range []string{markdown, mcp} {
		for _, want := range []string{
			"needs_user_decision",
			"inspect_targets",
			"safe_to_change",
			"do_not_change_yet",
			"next_command",
			"/continuity continue from review",
		} {
			if !strings.Contains(rendered, want) {
				t.Fatalf("expected actionable triage guidance %q in:\n%s", want, rendered)
			}
		}
	}
}

func TestSingleModelSecondPassStatesAreExplicit(t *testing.T) {
	run := testOperatorStatusReviewRun()
	second := buildReviewSecondPassObservability(run)
	if second == nil || second.State != "single_model_second_pass_cached" {
		t.Fatalf("unexpected single-model second pass state: %#v", second)
	}
	line := reviewSecondPassStatusLine(second)
	if !strings.Contains(line, "single_model_second_pass_cached") || strings.Contains(line, "cross_model_review_ran=true") {
		t.Fatalf("single-model state should not look like independent cross-review: %q", line)
	}

	crossRun := run
	crossRun.SingleModelPolicy = SingleModelReviewPolicy{}
	crossRun.SingleModelSecondPass = nil
	crossRun.ReviewerRuns = []ReviewReviewerRun{{
		Role:         "cross_reviewer",
		Kind:         "cross",
		Status:       "completed",
		ModelQuality: reviewModelQualityUsable,
	}}
	crossSecond := buildReviewSecondPassObservability(crossRun)
	if crossSecond == nil || crossSecond.State != "cross_model_review_ran" || !crossSecond.CrossModelRan {
		t.Fatalf("expected cross-model review state, got %#v", crossSecond)
	}
}

func TestDocumentArtifactStatusShowsQualityAndVerificationSkipWithoutCodeReviewOverblocking(t *testing.T) {
	root := initTestGitRepo(t)
	path := filepath.Join(root, "BugReport.md")
	if err := os.WriteFile(path, []byte("# Bug Report\n\nConcrete finding.\n"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	session := NewSession(root, "provider", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		RequestClass:           reviewRequestClassDocumentArtifact,
		RequestClassReason:     "document artifact request",
		RequestClassConfidence: 0.9,
		RequiredArtifacts:      []string{"BugReport.md"},
	}
	session.LastCodingHarnessReport = &CodingHarnessReport{
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "BugReport.md",
				Kind:         "markdown",
				Size:         32,
				ContentChars: 32,
				Substantive:  true,
				Checks:       []string{"substantive content"},
			}},
		},
	}
	session.LastCodingHarnessReport.Normalize()
	var output bytes.Buffer
	rt := &runtimeState{
		writer:    &output,
		ui:        NewUI(),
		session:   session,
		workspace: Workspace{BaseRoot: root, Root: root},
	}

	rt.printRuntimeGateStatus(runtimeGateActionFinalAnswer)

	text := output.String()
	for _, want := range []string{
		"class=document_artifact",
		"kind=document_artifact",
		"document=accepted",
		"verification=skipped_document_artifact_only",
		"document_artifact",
		"path=BugReport.md",
		"artifact_quality=accepted",
		"document-only artifact flow",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected document artifact status %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "code_repair_blocker") {
		t.Fatalf("document-only artifact flow should not show code repair overblocking language:\n%s", text)
	}
}

func TestFinalAnswerContractRejectsGenericCompletionWording(t *testing.T) {
	session := NewSession(t.TempDir(), "provider", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		RequestClass:           reviewRequestClassModifyThenReview,
		RequestClassReason:     "modification lifecycle",
		RequestClassConfidence: 0.9,
		Mode:                   "edit",
	}
	agent := &Agent{Session: session}

	status := agent.finalAnswerContractStatusForReply("Done.", true)

	if status == nil || status.Status != reviewFinalAnswerContractStatusBlocked {
		t.Fatalf("expected generic completion to be blocked, got %#v", status)
	}
	if !status.GenericCompletionRejected {
		t.Fatalf("expected generic completion wording to be explicitly rejected: %#v", status)
	}
}

func TestMCPReviewResponseExposesOperatorCardFieldsWithoutRawModelDump(t *testing.T) {
	run := testOperatorStatusReviewRun()
	run.ReviewerRuns = []ReviewReviewerRun{{
		Role:                    "primary_reviewer",
		Kind:                    "main",
		Status:                  "completed",
		ModelQuality:            reviewModelQualityUsable,
		RawProviderResponsePath: ".kernforge/reviews/raw-provider.json",
	}}

	rendered := renderReviewMCPResponse(run, 40000)

	for _, want := range []string{
		`"lifecycle_timeline"`,
		`"lifecycle_kind"`,
		`"compact_status"`,
		`"blocker_summary"`,
		`"route_quality"`,
		`"final_answer_contract_status"`,
		`"next_recommended_command"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected MCP response to include %s, got:\n%s", want, rendered)
		}
	}
	if strings.Contains(strings.ToLower(rendered), "raw model output") {
		t.Fatalf("MCP response should not dump raw model output:\n%s", rendered)
	}
}

func TestMCPStatusResponseAddsCompactOperatorFields(t *testing.T) {
	root := initTestGitRepo(t)
	session := NewSession(root, "provider", "model", "", "default")
	run := testOperatorStatusReviewRun()
	session.LastReviewRun = &run
	rt := &runtimeState{
		session:   session,
		cfg:       Config{Provider: "provider", Model: "model"},
		workspace: Workspace{BaseRoot: root, Root: root},
		agent:     &Agent{Session: session},
		evidence:  NewEvidenceStore(),
	}
	rt.evidence.Path = filepath.Join(root, ".kernforge", "evidence.json")
	server := &kernforgeMCPServer{rt: rt, workspaceSource: "test"}

	rendered, err := server.toolStatus(context.Background(), map[string]any{"detail": true})
	if err != nil {
		t.Fatalf("toolStatus: %v", err)
	}
	for _, want := range []string{`"compact_status"`, `"lifecycle_timeline"`, `"blocker_summary"`, `"next_recommended_command"`} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected MCP status to include %s, got:\n%s", want, rendered)
		}
	}
}

func TestMCPReviewAutoOffExposesLifecycleKindWithoutModelRun(t *testing.T) {
	server := &kernforgeMCPServer{}
	rendered, err := server.toolReview(context.Background(), map[string]any{
		"auto_review": "off",
		"request":     "fix main.go and write docs/review_report.md with the review notes",
	})
	if err != nil {
		t.Fatalf("toolReview: %v", err)
	}
	for _, want := range []string{
		`"request_class": "modify_then_review"`,
		`"lifecycle_kind": "mixed_flow"`,
		`"mixed_flow": true`,
		`"secondary_request_classes"`,
		`"compact_status"`,
		`"lifecycle_timeline"`,
		`"next_recommended_command"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected MCP auto_review=off response to contain %q, got:\n%s", want, rendered)
		}
	}
}
