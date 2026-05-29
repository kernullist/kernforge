package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewRequestClassClassificationCoversCoreLifecycles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	rt := requestClassTestRuntime(root, &scriptedProviderClient{})
	tests := []struct {
		name      string
		opts      ReviewHarnessOptions
		want      string
		wantKind  string
		wantMixed bool
		ambiguous bool
	}{
		{
			name: "review only",
			opts: ReviewHarnessOptions{
				Trigger:             naturalReviewTrigger,
				Target:              reviewTargetChange,
				Request:             "review main.go for correctness issues",
				Paths:               []string{"main.go"},
				IncludeFileContents: true,
			},
			want:     reviewRequestClassReviewOnly,
			wantKind: reviewLifecycleKindReviewOnly,
		},
		{
			name: "document artifact",
			opts: ReviewHarnessOptions{
				Target:  reviewTargetAnalysis,
				Request: "write docs/report.md as a report about the current bug findings",
				Paths:   []string{"docs/report.md"},
			},
			want:     reviewRequestClassDocumentArtifact,
			wantKind: reviewLifecycleKindDocumentArtifact,
		},
		{
			name: "review then modify",
			opts: ReviewHarnessOptions{
				Trigger: reviewBeforeFixTrigger,
				Target:  reviewTargetChange,
				Request: "review main.go and fix any problems",
				Paths:   []string{"main.go"},
			},
			want:     reviewRequestClassReviewThenModify,
			wantKind: reviewLifecycleKindFixFromReview,
		},
		{
			name: "direct implementation",
			opts: ReviewHarnessOptions{
				Target:  reviewTargetChange,
				Request: "implement startup retry in main.go",
				Paths:   []string{"main.go"},
			},
			want:     reviewRequestClassModifyThenReview,
			wantKind: reviewLifecycleKindImplementation,
		},
		{
			name: "modify then review",
			opts: ReviewHarnessOptions{
				Trigger: "post_change",
				Target:  reviewTargetChange,
				Request: "fix main.go startup behavior",
				Paths:   []string{"main.go"},
			},
			want:     reviewRequestClassModifyThenReview,
			wantKind: reviewLifecycleKindModifyThenReview,
		},
		{
			name: "verification only",
			opts: ReviewHarnessOptions{
				Request: "run tests only and report the verification result",
			},
			want:     reviewRequestClassVerificationOnly,
			wantKind: reviewLifecycleKindVerificationOnly,
		},
		{
			name: "validation only",
			opts: ReviewHarnessOptions{
				Request: "validate only the saved output against the acceptance criteria",
			},
			want:     reviewRequestClassValidationOnly,
			wantKind: reviewLifecycleKindValidationOnly,
		},
		{
			name: "analysis",
			opts: ReviewHarnessOptions{
				Target:  reviewTargetAnalysis,
				Request: "analyze the current project architecture and explain the flow",
			},
			want:     reviewRequestClassGeneral,
			wantKind: reviewLifecycleKindAnalysis,
		},
		{
			name: "mixed review document stays document artifact",
			opts: ReviewHarnessOptions{
				Target:  reviewTargetAnalysis,
				Request: "review main.go and create docs/review_report.md as a report",
				Paths:   []string{"main.go", "docs/review_report.md"},
			},
			want:      reviewRequestClassDocumentArtifact,
			wantKind:  reviewLifecycleKindMixedFlow,
			wantMixed: true,
			ambiguous: true,
		},
		{
			name: "inspect bugs fix confirmed uses review then modify",
			opts: ReviewHarnessOptions{
				Request: "inspect bugs in main.go and fix only confirmed issues",
				Paths:   []string{"main.go"},
			},
			want:      reviewRequestClassReviewThenModify,
			wantKind:  reviewLifecycleKindFixFromReview,
			ambiguous: true,
		},
		{
			name: "explicit no edit review stays read only",
			opts: ReviewHarnessOptions{
				Request: "review main.go only; do not edit files",
				Paths:   []string{"main.go"},
			},
			want:     reviewRequestClassReviewOnly,
			wantKind: reviewLifecycleKindReviewOnly,
		},
		{
			name: "document request with code change uses modification lifecycle",
			opts: ReviewHarnessOptions{
				Request: "fix main.go and write docs/review_report.md with the review notes",
				Paths:   []string{"main.go", "docs/review_report.md"},
			},
			want:      reviewRequestClassModifyThenReview,
			wantKind:  reviewLifecycleKindMixedFlow,
			wantMixed: true,
			ambiguous: true,
		},
		{
			name: "verify only after existing changes",
			opts: ReviewHarnessOptions{
				Request: "verify only after the existing changes and report the result",
			},
			want:     reviewRequestClassVerificationOnly,
			wantKind: reviewLifecycleKindVerificationOnly,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysis := analyzeReviewRequest(rt, root, tt.opts)
			if analysis.RequestClass != tt.want {
				t.Fatalf("request class = %q, want %q; analysis=%#v", analysis.RequestClass, tt.want, analysis)
			}
			if analysis.LifecycleKind != tt.wantKind {
				t.Fatalf("lifecycle kind = %q, want %q; analysis=%#v", analysis.LifecycleKind, tt.wantKind, analysis)
			}
			if analysis.MixedFlow != tt.wantMixed {
				t.Fatalf("mixed flow = %t, want %t; analysis=%#v", analysis.MixedFlow, tt.wantMixed, analysis)
			}
			if strings.TrimSpace(analysis.RequestClassReason) == "" {
				t.Fatalf("expected request class reason")
			}
			if analysis.RequestClassConfidence <= 0 {
				t.Fatalf("expected request class confidence, got %#v", analysis)
			}
			if tt.ambiguous && !analysis.RequestClassAmbiguous {
				t.Fatalf("expected ambiguous request class decision, got %#v", analysis)
			}
		})
	}
}

func TestReviewRunPersistsRequestClassLifecycleAndMCPResponse(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	rt := requestClassTestRuntime(root, &scriptedProviderClient{})
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             naturalReviewTrigger,
		Target:              reviewTargetChange,
		Request:             "review main.go for regressions",
		Paths:               []string{"main.go"},
		IncludeFileContents: true,
		NoModel:             true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if run.RequestClass != reviewRequestClassReviewOnly || run.RequestAnalysis.RequestClass != reviewRequestClassReviewOnly {
		t.Fatalf("expected review_only request class, got run=%q analysis=%q", run.RequestClass, run.RequestAnalysis.RequestClass)
	}
	if run.Lifecycle == nil || run.Lifecycle.RequestClass != reviewRequestClassReviewOnly || run.Lifecycle.RouteMode == "" {
		t.Fatalf("expected lifecycle state, got %#v", run.Lifecycle)
	}
	if run.RequestAnalysis.LifecycleKind != reviewLifecycleKindReviewOnly ||
		run.Lifecycle.LifecycleKind != reviewLifecycleKindReviewOnly {
		t.Fatalf("expected review_only lifecycle kind, got analysis=%q lifecycle=%#v", run.RequestAnalysis.LifecycleKind, run.Lifecycle)
	}
	if run.Lifecycle.ClassificationConfidence <= 0 || run.Lifecycle.Contract == nil || len(run.Lifecycle.Contract.FinalAnswerRequirements) == 0 {
		t.Fatalf("expected lifecycle classification confidence and contract, got %#v", run.Lifecycle)
	}
	if run.RuntimeGateLedger.RequestClass != reviewRequestClassReviewOnly ||
		run.RuntimeGateLedger.Lifecycle == nil ||
		run.RuntimeGateLedger.Lifecycle.RequestClass != reviewRequestClassReviewOnly ||
		run.RuntimeGateLedger.Lifecycle.LifecycleKind != reviewLifecycleKindReviewOnly {
		t.Fatalf("expected runtime gate request class lifecycle, got %#v", run.RuntimeGateLedger)
	}
	markdown := renderReviewRunMarkdown(run)
	for _, want := range []string{"Request class: `review_only`", "Lifecycle kind: `review_only`", "Request Lifecycle", "lifecycle_kind", "route_mode", "classification_confidence", "final_answer_contract"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("expected markdown to contain %q, got:\n%s", want, markdown)
		}
	}
	mcp := renderReviewMCPResponse(run, 40000)
	for _, want := range []string{`"request_class": "review_only"`, `"lifecycle_kind": "review_only"`, `"lifecycle"`, `"review_gate_status"`, `"classification_confidence"`, `"contract"`, `"route_quality"`} {
		if !strings.Contains(mcp, want) {
			t.Fatalf("expected MCP response to contain %q, got:\n%s", want, mcp)
		}
	}
}

func TestReviewOnlyModeReplyIsFindingsFirstAndReadOnly(t *testing.T) {
	run := ReviewRun{
		ID:           "review-read-only",
		RequestClass: reviewRequestClassReviewOnly,
		Target:       reviewTargetChange,
		Mode:         reviewModeGeneralChange,
		RequestAnalysis: ReviewRequestAnalysis{
			RequestClass: reviewRequestClassReviewOnly,
		},
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
		},
		Result: ReviewResult{
			Verdict: reviewVerdictApproved,
			Summary: "No blocking findings were discovered in the supplied evidence.",
		},
	}
	reply := formatCodexAppReviewModeReply(Config{AutoLocale: boolPtr(false)}, run)
	if !strings.HasPrefix(reply, "Review findings:") {
		t.Fatalf("review-only reply must be findings-first, got:\n%s", reply)
	}
	if !strings.Contains(reply, "Files edited: none.") {
		t.Fatalf("review-only reply must disclose read-only behavior, got:\n%s", reply)
	}
}

func TestDocumentArtifactRuntimeGateExposesAcceptedLifecycleWithoutReviewBlocker(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	contract := buildAcceptanceContract("write Tavern/BugReport.md as a bug report document", TurnIntentEditCode, false, true, false)
	session.AcceptanceContract = &contract
	session.LastCodingHarnessReport = &CodingHarnessReport{
		Approved: true,
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Size:         256,
				ContentChars: 256,
				Substantive:  true,
			}},
		},
	}
	session.LastReviewRun = &ReviewRun{
		ID:            "stale-code-review",
		SchemaVersion: reviewSchemaVersion,
		RequestClass:  reviewRequestClassModifyThenReview,
		Target:        reviewTargetChange,
		Mode:          reviewModeGeneralChange,
		Freshness: ReviewFreshness{
			Stale:       true,
			StaleReason: "unreviewed changed files: main.go",
		},
		Gate: GateDecision{
			Verdict: reviewVerdictNeedsRevision,
		},
	}
	ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)
	if ledger.RequestClass != reviewRequestClassDocumentArtifact ||
		ledger.Lifecycle == nil ||
		ledger.Lifecycle.DocumentGateStatus != "accepted" {
		t.Fatalf("expected accepted document artifact lifecycle, got %#v", ledger)
	}
	for _, blocker := range ledger.Blockers {
		if strings.Contains(blocker, "latest review is stale") {
			t.Fatalf("document artifact final answer must not be blocked by stale code review, got %#v", ledger.Blockers)
		}
	}
}

func TestDocumentArtifactQualityFailureBlocksFinalAnswer(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Tavern"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Tavern", "BugReport.md"), []byte("TODO\n"), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	session := NewSession(root, "scripted", "model", "", "default")
	contract := buildAcceptanceContract("write Tavern/BugReport.md as a report about kernel bug handling", TurnIntentEditCode, false, true, false)
	session.AcceptanceContract = &contract
	agent := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}
	report := agent.buildCodingHarnessReport("Saved `Tavern/BugReport.md`. Verification was not run because this is a document artifact.", true, true)
	if !codingHarnessFindingsHaveBlockers(report.ArtifactQuality.Findings) {
		t.Fatalf("expected artifact-quality blocker, got %#v", report.ArtifactQuality)
	}
	if report.Approved {
		t.Fatalf("placeholder document artifact must block final answer")
	}
}

func TestReviewThenModifyLifecycleRecordsReviewBeforeRepairAndSecondPass(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: needs_revision",
				"summary: review found a startup bug",
				"findings:",
				"- severity: blocker",
				"  category: correctness",
				"  path: main.go",
				"  line: 3",
				"  symbol: main",
				"  title: Startup cleanup is missing",
				"  evidence: main returns without cleanup in the supplied source.",
				"  impact: Resources can remain active after startup failure.",
				"  required_fix: Add cleanup before returning from main.",
				"  test_recommendation: Add startup failure coverage.",
			}, "\n")}},
			approvedReviewResponse("single-model second pass found no additional blockers"),
		},
	}
	rt := requestClassTestRuntime(root, provider)
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             reviewBeforeFixTrigger,
		Target:              reviewTargetChange,
		Request:             "review main.go and fix startup cleanup",
		Paths:               []string{"main.go"},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if run.RequestClass != reviewRequestClassReviewThenModify {
		t.Fatalf("expected review_then_modify, got %q", run.RequestClass)
	}
	if !run.RepairPlan.Required {
		t.Fatalf("expected repair plan tied to review findings, got %#v", run.RepairPlan)
	}
	if run.SingleModelSecondPass == nil || run.SingleModelSecondPass.Status == "" {
		t.Fatalf("expected single-model second-pass state to be recorded, got %#v", run.SingleModelSecondPass)
	}
	if len(run.StateTransitions) < 3 ||
		run.StateTransitions[0].To != reviewStateCollectEvidence ||
		run.StateTransitions[1].To != reviewStateMainReview {
		t.Fatalf("expected evidence collection before review and repair boundary, got %#v", run.StateTransitions)
	}
}

func TestCrossModelReviewThenModifyProducesTriageObligationAndGuidance(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	mainReviewer := &scriptedProviderClient{replies: []ChatResponse{approvedReviewResponse("main review found no blockers")}}
	crossReviewer := &scriptedProviderClient{
		replies: []ChatResponse{{Message: Message{Role: "assistant", Text: strings.Join([]string{
			"REVIEW_RESULT",
			"verdict: needs_revision",
			"summary: cross review found a decision item",
			"findings:",
			"- severity: medium",
			"  category: correctness",
			"  path: main.go",
			"  line: 3",
			"  symbol: main",
			"  title: Missing startup validation",
			"  evidence: main uses startup input without validation.",
			"  impact: Invalid input can be accepted.",
			"  required_fix: Validate startup input before use.",
			"  test_recommendation: Add invalid startup input coverage.",
		}, "\n")}}},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(false)
	agent := &Agent{
		Config:         cfg,
		Client:         mainReviewer,
		ReviewerClient: crossReviewer,
		ReviewerModel:  "cross-model",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        NewSession(root, "scripted", "main-model", "", "default"),
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
	}
	run, err := runReviewHarness(context.Background(), agent.reviewHarnessRuntime(root), ReviewHarnessOptions{
		Trigger:             "post_change",
		Target:              reviewTargetChange,
		Request:             "fix main.go and review after modification",
		ProvidedDiff:        "diff --git a/main.go b/main.go\n@@\n-func main() {}\n+func main() { println(\"ok\") }\n",
		ImplementationReply: "Changed main.go.",
		Paths:               []string{"main.go"},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if run.RequestClass != reviewRequestClassModifyThenReview {
		t.Fatalf("expected modify_then_review, got %q", run.RequestClass)
	}
	if run.Lifecycle == nil || run.Lifecycle.RouteMode != reviewRouteModeCrossModel {
		t.Fatalf("expected cross-model lifecycle, got %#v", run.Lifecycle)
	}
	if run.CrossReviewTriage == nil || run.CrossReviewTriage.StatusCounts[crossReviewTriageNeedsUserDecision] != 1 {
		t.Fatalf("expected needs_user_decision triage, got %#v", run.CrossReviewTriage)
	}
	if !reviewNextCommandsContainID(run.Gate.NextCommands, "cross-review-triage") {
		t.Fatalf("expected actionable triage next command, got %#v", run.Gate.NextCommands)
	}
	if !strings.Contains(run.CrossReviewTriage.Items[0].UserActionPrompt, "accepted_fixed") ||
		!strings.Contains(run.CrossReviewTriage.Items[0].UserActionPrompt, "rejected_with_reason") {
		t.Fatalf("expected concrete safe continuation guidance, got %#v", run.CrossReviewTriage.Items[0])
	}
	if !strings.Contains(run.CrossReviewTriage.Items[0].UserActionPrompt, "Inspect") ||
		!strings.Contains(run.CrossReviewTriage.Items[0].UserActionPrompt, "Safe change") ||
		!strings.Contains(run.CrossReviewTriage.Items[0].UserActionPrompt, "Do not change yet") ||
		run.CrossReviewTriage.Items[0].NextCommand != "/continuity continue from review" {
		t.Fatalf("expected actionable continuation guidance, got %#v", run.CrossReviewTriage.Items[0])
	}
}

func TestFinalAnswerCompletenessUsesRequestClassForReviewOnlyDisclosure(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	contract := buildAcceptanceContract("review main.go", TurnIntentReviewCode, true, false, false)
	session.AcceptanceContract = &contract
	agent := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}
	report := agent.buildCodingHarnessReport("Summary: no issues.", false, false)
	if report.FinalAnswerCorrection == nil || !report.FinalAnswerCorrection.Required {
		t.Fatalf("expected final answer completeness correction for review_only, got %#v", report.FinalAnswerCorrection)
	}
	if !containsString(report.FinalAnswerCorrection.Reasons, "review_only_findings_first_no_edit") {
		t.Fatalf("expected review-only correction reason, got %#v", report.FinalAnswerCorrection)
	}
}

func TestFinalAnswerContractRejectsGenericModificationAndDocumentAnswers(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		ID:                 "accept-modify",
		SourcePrompt:       "fix main.go",
		Mode:               "edit",
		RequestClass:       reviewRequestClassModifyThenReview,
		RequestClassReason: "test",
	}
	session.ActivePatchTransaction = &PatchTransaction{
		ID:     "patch-1",
		Goal:   "fix main.go",
		Status: patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ToolName: "apply_patch",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "main.go",
				Operation: "apply_patch",
			}},
		}},
	}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}
	report := agent.buildCodingHarnessReport("Done.", false, false)
	for _, want := range []string{
		"Changed-file summary is missing",
		"Review result is missing",
		"Validation result is missing",
		"Remaining-risk statement is missing",
	} {
		if !codingHarnessReportContainsFindingTitle(report, want) {
			t.Fatalf("expected generic modification answer to be rejected for %q, got %#v", want, report.FinalAnswerCorrection)
		}
	}

	docSession := NewSession(root, "scripted", "model", "", "default")
	docSession.AcceptanceContract = &AcceptanceContract{
		ID:                "accept-doc",
		SourcePrompt:      "write docs/review_report.md as a report",
		Mode:              "edit",
		RequestClass:      reviewRequestClassDocumentArtifact,
		RequiredArtifacts: []string{"docs/review_report.md"},
	}
	docAgent := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   docSession,
	}
	docReport := docAgent.buildCodingHarnessReport("Done.", false, false)
	for _, want := range []string{
		"Document artifact path is missing",
		"Document artifact quality status is missing",
		"Document artifact verification disclosure is missing",
		"Document artifact limitation statement is missing",
	} {
		if !codingHarnessReportContainsFindingTitle(docReport, want) {
			t.Fatalf("expected generic document answer to be rejected for %q, got %#v", want, docReport.FinalAnswerCorrection)
		}
	}
}

func codingHarnessReportContainsFindingTitle(report CodingHarnessReport, title string) bool {
	for _, finding := range report.allFindings() {
		if strings.EqualFold(strings.TrimSpace(finding.Title), strings.TrimSpace(title)) {
			return true
		}
	}
	return false
}

func requestClassTestRuntime(root string, provider *scriptedProviderClient) *runtimeState {
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(false)
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   NewSession(root, "scripted", "main-model", "", "default"),
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	return agent.reviewHarnessRuntime(root)
}
