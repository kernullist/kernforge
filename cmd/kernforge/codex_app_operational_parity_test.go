package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScriptedLiveProviderDrillCoversRequiredWorkflowClassesAndSurfaces(t *testing.T) {
	report := buildScriptedLiveProviderDrillReport(time.Now())
	required := []string{
		reviewLifecycleKindDocumentArtifact,
		reviewLifecycleKindReviewOnly,
		reviewLifecycleKindImplementation,
		reviewLifecycleKindModifyThenReview,
		reviewLifecycleKindFixFromReview,
		reviewLifecycleKindAnalysis,
		reviewLifecycleKindMixedFlow,
	}
	for _, want := range required {
		if !containsString(report.WorkflowClasses, want) {
			t.Fatalf("scripted drill missing workflow %q: %#v", want, report.WorkflowClasses)
		}
	}
	for _, wantRoute := range []string{reviewRouteModeSingleModel, reviewRouteModeCrossModel} {
		if !containsString(report.RouteModes, wantRoute) {
			t.Fatalf("scripted drill missing route mode %q: %#v", wantRoute, report.RouteModes)
		}
	}
	if len(report.Turns) != len(required)*2 {
		t.Fatalf("expected workflow x route-mode turns, got %d", len(report.Turns))
	}
	for _, turn := range report.Turns {
		if turn.LatencyMS <= 0 || turn.ProviderLabel == "" || turn.ModelID == "" || turn.FinalGateState == "" {
			t.Fatalf("turn did not capture route/provider/model/latency/final gate state: %#v", turn)
		}
		if turn.RouteMode == reviewRouteModeCrossModel && !turn.ReviewerSeparated {
			t.Fatalf("cross-model turn must record reviewer separation: %#v", turn)
		}
	}

	run := codexAppParityScenarioRun(t, reviewRequestClassModifyThenReview, reviewLifecycleKindMixedFlow, "post_change", reviewTargetChange, "mixed live drill", reviewRouteModeCrossModel, true, []string{reviewRequestClassDocumentArtifact})
	run.LiveProviderDrill = &report
	mcp := renderReviewMCPResponse(run, 80000)
	markdown := renderReviewRunMarkdown(run)
	for _, rendered := range []string{mcp, markdown} {
		for _, want := range []string{"live_provider_drill", "latency_ms", "reviewer_separated", "scripted live-provider drill"} {
			if !strings.Contains(rendered, want) {
				t.Fatalf("expected live-provider drill surface %q in:\n%s", want, rendered)
			}
		}
	}
}

func TestProviderFailureTelemetryDegradesRouteQualityAndBlocksRequiredWeakReviewer(t *testing.T) {
	run := codexAppParityScenarioRun(t, reviewRequestClassModifyThenReview, reviewLifecycleKindModifyThenReview, "pre_write", reviewTargetChange, "edit then review", reviewRouteModeCrossModel, false, nil)
	run.ModelPlan.RequiredRoles = []string{"primary_reviewer", "cross_reviewer"}
	run.ReviewerRuns = []ReviewReviewerRun{
		{
			Role:          "primary_reviewer",
			Kind:          "main",
			Provider:      "openai-codex",
			ProviderLabel: reviewProviderDisplayLabel("openai-codex"),
			ModelID:       "main-model",
			Model:         "openai-codex-subscription/main-model",
			Status:        "completed",
			ModelQuality:  reviewModelQualityUsable,
			LatencyMS:     1100,
		},
		{
			Role:                 "cross_reviewer",
			Kind:                 "cross",
			Provider:             "anthropic",
			ProviderLabel:        reviewProviderDisplayLabel("anthropic"),
			ModelID:              "cross-model",
			Model:                "anthropic-api/cross-model",
			Status:               "completed",
			ModelQuality:         reviewModelQualityWeak,
			LatencyMS:            2200,
			RetryCount:           2,
			MalformedOutputCount: 1,
			WeakOutputDegraded:   true,
			FailureClass:         providerFailureClassMalformedResponse,
			RawOutputPath:        ".kernforge/reviews/review/raw.md",
		},
	}
	for i := range run.ReviewerRuns {
		finalizeReviewReviewerRunTelemetry(&run.ReviewerRuns[i])
	}
	run.Findings = requiredReviewerFailureFindings(run)
	run.RouteHealthEvents = reviewRouteHealthEventsFromRun(&run)
	run.ModelPlan.RouteHealthEvents = append([]ReviewRouteHealthEvent(nil), run.RouteHealthEvents...)
	run.Gate = evaluateReviewGate(run)
	run.RuntimeGateLedger = RuntimeGateLedger{
		ID:                "runtime-gate-provider-failure",
		Action:            runtimeGateActionReview,
		Status:            runtimeGateStatusBlocked,
		RequestClass:      run.RequestClass,
		Lifecycle:         run.Lifecycle,
		ReviewRunID:       run.ID,
		RouteHealthEvents: run.RouteHealthEvents,
		Blockers:          []string{"required reviewer route failed or returned weak output"},
	}
	run.DecisionObservability = buildReviewDecisionObservability(&run, &run.RuntimeGateLedger, nil)

	route := reviewRouteQualityForRun(run)
	if route == nil || !route.Degraded || route.Status != "degraded" {
		t.Fatalf("expected degraded route quality, got %#v", route)
	}
	if !containsString(route.FailureClasses, providerFailureClassMalformedResponse) || route.MalformedOutputs == 0 || route.RetryCount == 0 {
		t.Fatalf("expected malformed/retry telemetry, got %#v", route)
	}
	if run.Gate.Verdict == reviewVerdictApproved {
		t.Fatalf("weak required reviewer output must not approve finalization: %#v", run.Gate)
	}

	mcp := renderReviewMCPResponse(run, 80000)
	markdown := renderReviewRunMarkdown(run)
	for _, rendered := range []string{mcp, markdown} {
		for _, want := range []string{"route_health_events", providerFailureClassMalformedResponse, "malformed", "retry_count"} {
			if !strings.Contains(rendered, want) {
				t.Fatalf("expected provider failure telemetry %q in:\n%s", want, rendered)
			}
		}
	}
}

func TestCrossModelDuplicatedReviewerFromSameModelIsDegradedEvidence(t *testing.T) {
	run := codexAppParityScenarioRun(t, reviewRequestClassModifyThenReview, reviewLifecycleKindModifyThenReview, "post_change", reviewTargetChange, "edit then review", reviewRouteModeCrossModel, false, nil)
	run.ReviewerRuns = []ReviewReviewerRun{
		{
			Role:         "primary_reviewer",
			Kind:         "main",
			Provider:     "openai",
			ModelID:      "same-model",
			Model:        "openai-api/same-model",
			Status:       "completed",
			ModelQuality: reviewModelQualityUsable,
		},
		{
			Role:                       "cross_reviewer",
			Kind:                       "cross",
			Provider:                   "openai",
			ModelID:                    "same-model",
			Model:                      "openai-api/same-model",
			Status:                     "completed",
			ModelQuality:               reviewModelQualityUsable,
			DuplicatedOutput:           true,
			ReviewerMainModelCollision: true,
		},
	}
	for i := range run.ReviewerRuns {
		finalizeReviewReviewerRunTelemetry(&run.ReviewerRuns[i])
	}
	run.RouteHealthEvents = reviewRouteHealthEventsFromRun(&run)
	route := reviewRouteQualityForRun(run)
	if route == nil || !route.Degraded || !containsString(route.FailureClasses, providerFailureClassReviewerMainModelCollision) {
		t.Fatalf("expected reviewer/main model collision degradation, got %#v events=%#v", route, run.RouteHealthEvents)
	}
}

func TestStaleContextRecoveryItemsExposeActionAndFinalizationPolicy(t *testing.T) {
	now := time.Now()

	t.Run("artifact quality changed after gate", func(t *testing.T) {
		root := t.TempDir()
		session := NewSession(root, "provider", "model", "", "default")
		session.AcceptanceContract = &AcceptanceContract{RequestClass: reviewRequestClassDocumentArtifact, RequiredArtifacts: []string{"report.md"}}
		session.LastCodingHarnessReport = &CodingHarnessReport{
			ArtifactQuality: ArtifactQualityReport{
				GeneratedAt: now.Add(-time.Hour),
				Artifacts: []ArtifactQualityCheck{{
					Path:         "report.md",
					Kind:         "markdown",
					Substantive:  true,
					ContentChars: 100,
				}},
			},
		}
		session.LastCodingHarnessReport.Normalize()
		session.PatchTransactions = []PatchTransaction{{
			ID:          "patch-doc",
			Status:      patchTransactionStatusCommitted,
			CompletedAt: now,
			UpdatedAt:   now,
			Entries: []PatchTransactionEntry{{
				ID:          "patch-doc-entry",
				Status:      "success",
				CompletedAt: now,
				Paths: []PatchPathChange{{
					Path:      "report.md",
					Operation: "write_file",
				}},
			}},
		}}
		ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)
		item := staleContextItemByKind(ledger.StaleContextSummary, staleContextKindChangedArtifactsAfterQuality)
		if item == nil || !item.FinalizationBlocked || item.NextCommand == "" || item.EvidenceRef == "" {
			t.Fatalf("expected blocking stale artifact-quality item with recovery metadata, got ledger=%#v item=%#v", ledger, item)
		}
	})

	t.Run("verification passed then patch changed", func(t *testing.T) {
		root := t.TempDir()
		session := NewSession(root, "provider", "model", "", "default")
		session.Messages = []Message{{Role: "user", Text: "modify main.go"}}
		session.LastVerification = &VerificationReport{
			GeneratedAt:  now.Add(-time.Hour),
			Trigger:      "automatic",
			Workspace:    root,
			ChangedPaths: []string{"main.go"},
			Steps: []VerificationStep{{
				Label:  "go test",
				Status: VerificationPassed,
			}},
		}
		session.PatchTransactions = []PatchTransaction{{
			ID:          "patch-main",
			Status:      patchTransactionStatusCommitted,
			CompletedAt: now,
			UpdatedAt:   now,
			Entries: []PatchTransactionEntry{{
				ID:          "patch-main-entry",
				Status:      "success",
				CompletedAt: now,
				Paths: []PatchPathChange{{
					Path:      "main.go",
					Operation: "write_file",
				}},
			}},
		}}
		ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)
		item := staleContextItemByKind(ledger.StaleContextSummary, staleContextKindStaleVerification)
		if item == nil || item.FinalizationBlocked || !item.AllowedWithDisclosure || item.NextCommand != "/verify --full" {
			t.Fatalf("expected stale verification warning with disclosure policy, got ledger=%#v item=%#v", ledger, item)
		}
	})

	t.Run("pending correction interrupted by new request", func(t *testing.T) {
		root := t.TempDir()
		session := NewSession(root, "provider", "model", "", "default")
		session.Messages = []Message{
			{Role: "user", Text: "fix main.go"},
			{Role: "assistant", Phase: messagePhaseFinalAnswerCandidate, Text: "candidate"},
			{Role: "user", Text: "now review other.go"},
		}
		session.LastFinalAnswerCorrection = &FinalAnswerCorrectionVisibility{
			Required:             true,
			Status:               finalAnswerCorrectionStatusRequired,
			Reasons:              []string{"validation_disclosure"},
			AttemptCount:         1,
			MaxAttempts:          finalAnswerCorrectionDefaultMaxAttempts,
			RecordedMessageCount: 1,
			HasRecordedMessages:  true,
		}
		session.LastFinalAnswerCorrection.Normalize()
		ledger := buildRuntimeGateLedger(root, session, runtimeGateActionFinalAnswer)
		item := staleContextItemByKind(ledger.StaleContextSummary, staleContextKindCorrectionPendingNewRequest)
		if item == nil || !item.FinalizationBlocked || ledger.Status != runtimeGateStatusBlocked {
			t.Fatalf("expected pending correction/new request stale blocker, got ledger=%#v item=%#v", ledger, item)
		}
	})
}

func TestCorrectionContractPersistsAcrossReloadAndRejectedStateWins(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "provider", "model", "", "default")
	session.ID = "session-correction-contract"
	session.LastFinalAnswerCorrection = &FinalAnswerCorrectionVisibility{
		Required:     true,
		Rejected:     true,
		Status:       finalAnswerCorrectionStatusRejected,
		Reasons:      []string{"changed_file_disclosure", "validation_disclosure", "correction_attempts_exhausted"},
		AttemptCount: 3,
		MaxAttempts:  finalAnswerCorrectionDefaultMaxAttempts,
	}
	session.LastFinalAnswerCorrection.Normalize()
	session.RuntimeGateLedger = &RuntimeGateLedger{
		ID:                    "runtime-gate-correction",
		Action:                runtimeGateActionFinalAnswer,
		Status:                runtimeGateStatusBlocked,
		FinalAnswerCorrection: session.LastFinalAnswerCorrection,
	}
	if err := store.Save(session); err != nil {
		t.Fatalf("save session: %v", err)
	}
	loaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if loaded.LastFinalAnswerCorrection == nil ||
		!loaded.LastFinalAnswerCorrection.Rejected ||
		loaded.LastFinalAnswerCorrection.Contract == nil ||
		!loaded.LastFinalAnswerCorrection.Contract.ManualRecoveryNeeded {
		t.Fatalf("expected rejected correction contract to survive reload, got %#v", loaded.LastFinalAnswerCorrection)
	}

	report := &CodingHarnessReport{
		Findings: []CodingHarnessFinding{{
			Severity: "blocker",
			Title:    "Changed-file summary is missing",
		}},
	}
	report.Normalize()
	run := &ReviewRun{ID: "review-correction", RequestClass: reviewRequestClassModifyThenReview}
	ledger := buildRuntimeGateLedger(root, loaded, runtimeGateActionFinalAnswer)
	observability := buildReviewDecisionObservability(run, &ledger, report)
	if observability == nil || observability.FinalAnswerCorrection == nil ||
		!observability.FinalAnswerCorrection.Rejected ||
		observability.FinalAnswerCorrection.Status != finalAnswerCorrectionStatusRejected {
		t.Fatalf("regenerated observability overwrote rejected state: %#v", observability)
	}
}

func TestRealProviderDrillSkippedReportIsExplicitWhenConfigMissing(t *testing.T) {
	report := buildRealProviderSkippedDrillReport("provider/model config is unavailable")
	if report.Status != liveProviderDrillStatusSkipped || report.SkippedReason == "" {
		t.Fatalf("expected explicit skipped real-provider drill report, got %#v", report)
	}
	liveReport, err := runOptionalRealProviderSmokeDrill(context.Background(), t.TempDir(), Config{})
	if err != nil {
		t.Fatalf("missing provider config should skip without failing: %v", err)
	}
	if liveReport.Status != liveProviderDrillStatusSkipped || !strings.Contains(liveReport.SkippedReason, "provider/model config") {
		t.Fatalf("expected optional real-provider drill to skip with config reason, got %#v", liveReport)
	}
	run := codexAppParityScenarioRun(t, reviewRequestClassReviewOnly, reviewLifecycleKindReviewOnly, "", reviewTargetChange, "review only", reviewRouteModeSingleModel, false, nil)
	run.LiveProviderDrill = &report
	markdown := renderReviewRunMarkdown(run)
	mcp := renderReviewMCPResponse(run, 80000)
	for _, rendered := range []string{markdown, mcp} {
		if !strings.Contains(rendered, "provider/model config is unavailable") || !strings.Contains(rendered, liveProviderDrillStatusSkipped) {
			t.Fatalf("expected skipped real-provider reason in surface:\n%s", rendered)
		}
	}
}

func TestStatusAndMCPPreserveRouteHealthCorrectionAndDrillAfterReload(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, ".kernforge", "sessions-test"))
	session := NewSession(root, "provider", "model", "", "default")
	session.ID = "session-reload-operational"
	drill := buildRealProviderSkippedDrillReport("real provider smoke disabled")
	session.LastLiveProviderDrill = &drill
	session.LastFinalAnswerCorrection = &FinalAnswerCorrectionVisibility{
		Required:     true,
		Rejected:     true,
		Status:       finalAnswerCorrectionStatusRejected,
		Reasons:      []string{"validation_disclosure"},
		AttemptCount: 2,
		MaxAttempts:  2,
	}
	session.LastFinalAnswerCorrection.Normalize()
	session.RuntimeGateLedger = &RuntimeGateLedger{
		ID:                    "runtime-gate-reload",
		Action:                runtimeGateActionFinalAnswer,
		Status:                runtimeGateStatusBlocked,
		FinalAnswerCorrection: session.LastFinalAnswerCorrection,
		LiveProviderDrill:     session.LastLiveProviderDrill,
		RouteHealthEvents: []ReviewRouteHealthEvent{{
			Role:         "cross_reviewer",
			Provider:     "anthropic",
			ModelID:      "reviewer",
			FailureClass: providerFailureClassTimeout,
			Status:       "failed",
		}},
	}
	if err := store.Save(session); err != nil {
		t.Fatalf("save session: %v", err)
	}
	loaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	var out bytes.Buffer
	rt := &runtimeState{
		writer:    &out,
		ui:        NewUI(),
		store:     store,
		session:   loaded,
		evidence:  &EvidenceStore{Path: filepath.Join(root, "evidence.json"), MaxEntries: 100},
		workspace: Workspace{BaseRoot: root, Root: root},
	}
	rt.printRuntimeGateStatusDetail(runtimeGateActionFinalAnswer)
	statusText := out.String()
	for _, want := range []string{"final_answer_correction_contract", "live_provider_drill", "real provider smoke disabled"} {
		if !strings.Contains(statusText, want) {
			t.Fatalf("expected reload status to contain %q, got:\n%s", want, statusText)
		}
	}

	server := &kernforgeMCPServer{rt: rt}
	mcpText, err := server.toolStatus(context.Background(), map[string]any{"detail": true})
	if err != nil {
		t.Fatalf("mcp status: %v", err)
	}
	for _, want := range []string{"final_answer_correction", "live_provider_drill", "stale_context_summary", "next_recommended_command"} {
		if !strings.Contains(mcpText, want) {
			t.Fatalf("expected MCP reload status to contain %q, got:\n%s", want, mcpText)
		}
	}
}

func TestMCPStatusJSONExposesStructuredFieldsWithoutProseParsing(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.LastLiveProviderDrill = func() *LiveProviderDrillReport {
		report := buildRealProviderSkippedDrillReport("missing config")
		return &report
	}()
	rt := &runtimeState{
		writer:    &bytes.Buffer{},
		ui:        NewUI(),
		session:   session,
		evidence:  &EvidenceStore{Path: filepath.Join(root, "evidence.json"), MaxEntries: 100},
		workspace: Workspace{BaseRoot: root, Root: root},
	}
	server := &kernforgeMCPServer{rt: rt}
	text, err := server.toolStatus(context.Background(), map[string]any{"detail": true})
	if err != nil {
		t.Fatalf("mcp status: %v", err)
	}
	payload := extractMCPJSONPayload(t, text)
	for _, key := range []string{
		"compact_status",
		"blocker_summary",
		"stale_context_summary",
		"final_answer_contract_status",
		"final_answer_correction",
		"live_provider_drill",
		"route_health_events",
	} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("expected structured MCP status key %q in %#v", key, payload)
		}
	}
}

func staleContextItemByKind(summary *StaleContextSummary, kind string) *StaleContextItem {
	if summary == nil {
		return nil
	}
	for i := range summary.Items {
		if strings.EqualFold(summary.Items[i].Kind, kind) {
			return &summary.Items[i]
		}
	}
	return nil
}

func extractMCPJSONPayload(t *testing.T, text string) map[string]any {
	t.Helper()
	start := strings.Index(text, "```json")
	if start < 0 {
		t.Fatalf("missing json fence in %q", text)
	}
	jsonText := text[start+len("```json"):]
	end := strings.Index(jsonText, "```")
	if end < 0 {
		t.Fatalf("missing json fence end in %q", text)
	}
	jsonText = strings.TrimSpace(jsonText[:end])
	var payload map[string]any
	if err := json.Unmarshal([]byte(jsonText), &payload); err != nil {
		t.Fatalf("unmarshal MCP payload: %v\n%s", err, jsonText)
	}
	return payload
}
