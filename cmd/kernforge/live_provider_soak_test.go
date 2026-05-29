package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLiveProviderSoakScriptedWritesArtifactsAndCoversRoutes(t *testing.T) {
	rt, _ := newLiveProviderSoakTestRuntime(t)
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	report, ledger, err := rt.runLiveProviderSoak(context.Background(), LiveProviderSoakOptions{
		Mode: liveProviderDrillModeScripted,
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("run scripted soak: %v", err)
	}
	if report.Status != liveProviderDrillStatusPassed || report.FinalGateStatus != runtimeGateStatusReady {
		t.Fatalf("expected passed scripted soak, got status=%s gate=%s report=%#v", report.Status, report.FinalGateStatus, report)
	}
	for _, want := range liveProviderDrillWorkflowClasses() {
		if !containsString(report.WorkflowClasses, want) {
			t.Fatalf("missing workflow class %q in %#v", want, report.WorkflowClasses)
		}
	}
	for _, want := range []string{reviewRouteModeSingleModel, reviewRouteModeCrossModel} {
		if !containsString(report.RouteModes, want) {
			t.Fatalf("missing route mode %q in %#v", want, report.RouteModes)
		}
	}
	if ledger.LiveProviderDrill == nil || ledger.LiveProviderDrill.ID != report.ID {
		t.Fatalf("runtime ledger did not preserve soak report: %#v", ledger.LiveProviderDrill)
	}
	if report.ReloadVerification == nil ||
		!report.ReloadVerification.Reloaded ||
		!report.ReloadVerification.StatusCompact ||
		!report.ReloadVerification.StatusDetail ||
		!report.ReloadVerification.MCPStatusJSON ||
		!report.ReloadVerification.MCPReviewJSON ||
		!report.ReloadVerification.RuntimeGateLedger {
		t.Fatalf("reload verification did not preserve all surfaces: %#v", report.ReloadVerification)
	}
	for _, name := range []string{
		"soak_report.json",
		"soak_report.md",
		"runtime_gate_ledger.json",
		"mcp_status.json",
		"mcp_review.json",
		"status.txt",
	} {
		if _, err := os.Stat(filepath.Join(report.ArtifactDir, name)); err != nil {
			t.Fatalf("expected artifact %s: %v", name, err)
		}
	}
	statusText := readTestFile(t, filepath.Join(report.ArtifactDir, "status.txt"))
	for _, want := range []string{"live_provider_drill", "final_answer_correction", "stale_context", "next_command"} {
		if !strings.Contains(statusText, want) {
			t.Fatalf("status artifact missing %q:\n%s", want, statusText)
		}
	}
	mcpStatus := readJSONMap(t, filepath.Join(report.ArtifactDir, "mcp_status.json"))
	for _, key := range []string{"live_provider_drill", "final_answer_correction", "stale_context_summary", "route_health_events"} {
		if _, ok := mcpStatus[key]; !ok {
			t.Fatalf("mcp status missing %q in %#v", key, mcpStatus)
		}
	}
}

func TestLiveProviderSoakMissingRealProviderConfigWritesSkippedArtifact(t *testing.T) {
	rt, _ := newLiveProviderSoakTestRuntime(t)
	rt.cfg.Provider = ""
	rt.cfg.Model = ""
	env := map[string]string{
		liveProviderSoakEnvEnabled: "1",
	}
	report, _, err := rt.runLiveProviderSoak(context.Background(), LiveProviderSoakOptions{
		Mode:   liveProviderDrillModeReal,
		Getenv: mapGetenv(env),
		Now:    fixedSoakNow(),
	})
	if err != nil {
		t.Fatalf("missing real provider config should not fail: %v", err)
	}
	if report.Status != liveProviderDrillStatusSkipped || !strings.Contains(report.SkippedReason, "provider/model config") {
		t.Fatalf("expected skipped artifact with missing config reason, got %#v", report)
	}
	data := readTestFile(t, filepath.Join(report.ArtifactDir, "soak_report.json"))
	if !strings.Contains(data, `"status": "skipped"`) || !strings.Contains(data, "provider/model config is unavailable") {
		t.Fatalf("skipped report artifact missing explicit reason:\n%s", data)
	}
}

func TestLiveProviderSoakProviderFailureEventsDegradeRouteQuality(t *testing.T) {
	report := LiveProviderDrillReport{
		ID:                 "soak-failure",
		SchemaVersion:      liveProviderDrillSchemaVersion,
		Mode:               liveProviderDrillModeScripted,
		Status:             liveProviderDrillStatusPassed,
		ProviderConfigured: true,
		Turns: []LiveProviderDrillTurn{{
			TurnID:                   "turn-timeout",
			RequestClass:             reviewRequestClassReviewOnly,
			LifecycleKind:            reviewLifecycleKindReviewOnly,
			RouteMode:                reviewRouteModeCrossModel,
			Provider:                 "openai",
			ProviderLabel:            "openai-api",
			ModelID:                  "main",
			ReviewerProvider:         "anthropic",
			ReviewerModelID:          "reviewer",
			ReviewerSeparated:        true,
			ReviewerSeparationStatus: "independent",
			LatencyMS:                5000,
			RetryCount:               2,
			EvidenceRef:              "fixture/timeout",
			RouteHealthEvents: []ReviewRouteHealthEvent{{
				TurnID:       "turn-timeout",
				Role:         "cross_reviewer",
				Kind:         "cross",
				Provider:     "anthropic",
				ModelID:      "reviewer",
				FailureClass: providerFailureClassTimeout,
				Severity:     staleContextSeverityBlocker,
				Status:       "failed",
				EvidenceRef:  "fixture/timeout",
			}},
		}},
	}
	finalizeLiveProviderDrillGate(&report)
	if report.FinalGateStatus != runtimeGateStatusBlocked || report.Status != liveProviderDrillStatusFailed {
		t.Fatalf("provider failure should block final gate, got status=%s gate=%s", report.Status, report.FinalGateStatus)
	}
	quality := liveProviderSoakRouteQualitySummary(report)
	if quality.Status != "failed" || !containsString(quality.FailureClasses, providerFailureClassTimeout) {
		t.Fatalf("expected failed route quality with timeout, got %#v", quality)
	}
	if len(report.ProviderRecommendations) == 0 || report.ProviderRecommendations[0].NextSafeAction == "" {
		t.Fatalf("expected provider recommendation with next safe action, got %#v", report.ProviderRecommendations)
	}
}

func TestLiveProviderSoakInvalidReviewerEvidenceCannotApprove(t *testing.T) {
	cases := []struct {
		name  string
		patch func(*LiveProviderDrillTurn)
	}{
		{"weak", func(t *LiveProviderDrillTurn) { t.WeakOutputDegraded = true }},
		{"malformed", func(t *LiveProviderDrillTurn) { t.MalformedOutputCount = 1 }},
		{"duplicated", func(t *LiveProviderDrillTurn) { t.DuplicatedOutput = true }},
		{"evidence_free", func(t *LiveProviderDrillTurn) { t.EvidenceFreeOutput = true }},
		{"stale", func(t *LiveProviderDrillTurn) { t.StaleReviewerOutput = true }},
		{"tool_call_mismatch", func(t *LiveProviderDrillTurn) { t.ToolCallMismatch = true }},
		{"collision", func(t *LiveProviderDrillTurn) { t.ReviewerMainModelCollision = true }},
		{"cross_failure", func(t *LiveProviderDrillTurn) { t.CrossModelReviewerFailure = true }},
		{"retry_exhausted", func(t *LiveProviderDrillTurn) {
			t.RouteHealthEvents = []ReviewRouteHealthEvent{{
				TurnID:       t.TurnID,
				Role:         "cross_reviewer",
				FailureClass: providerFailureClassRetryExhausted,
				Severity:     staleContextSeverityBlocker,
				Status:       "failed",
				EvidenceRef:  t.EvidenceRef,
			}}
		}},
		{"missing_final_disclosure", func(t *LiveProviderDrillTurn) { t.MissingFinalAnswerDisclosure = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := liveProviderSoakSingleCrossFixture()
			tc.patch(&report.Turns[0])
			finalizeLiveProviderDrillGate(&report)
			if report.FinalGateStatus != runtimeGateStatusBlocked || !liveProviderDrillBlocksFinalization(&report) {
				t.Fatalf("invalid reviewer evidence approved finalization: %#v", report)
			}
			rt, root := newLiveProviderSoakTestRuntime(t)
			rt.session.LastLiveProviderDrill = &report
			ledger := buildRuntimeGateLedger(root, rt.session, runtimeGateActionFinalAnswer)
			if ledger.Status != runtimeGateStatusBlocked {
				t.Fatalf("runtime gate should block invalid soak evidence, got %#v", ledger)
			}
		})
	}
}

func TestLiveProviderSoakReviewerMainCollisionDegradesRouteQuality(t *testing.T) {
	report := liveProviderSoakSingleCrossFixture()
	report.Turns[0].ReviewerProvider = report.Turns[0].Provider
	report.Turns[0].ReviewerModelID = report.Turns[0].ModelID
	report.Turns[0].ReviewerMainModelCollision = true
	finalizeLiveProviderDrillGate(&report)
	quality := liveProviderSoakRouteQualitySummary(report)
	if report.FinalGateStatus != runtimeGateStatusBlocked ||
		quality.Status != "failed" ||
		!containsString(quality.FailureClasses, providerFailureClassReviewerMainModelCollision) {
		t.Fatalf("expected collision to degrade and block, report=%#v quality=%#v", report, quality)
	}
}

func TestLiveProviderSoakSingleModelCompletesWithDisclosure(t *testing.T) {
	report := LiveProviderDrillReport{
		ID:                          "single-model-soak",
		SchemaVersion:               liveProviderDrillSchemaVersion,
		Mode:                        liveProviderDrillModeScripted,
		Status:                      liveProviderDrillStatusPassed,
		ProviderConfigured:          true,
		IndependentReviewDisclosure: "single-model soak completed with no independent reviewer evidence available",
	}
	for idx, workflow := range liveProviderDrillWorkflowSpecs() {
		report.Turns = append(report.Turns, LiveProviderDrillTurn{
			TurnID:                      "single-" + strconv.Itoa(idx+1),
			RequestClass:                workflow.requestClass,
			LifecycleKind:               workflow.lifecycleKind,
			RouteMode:                   reviewRouteModeSingleModel,
			RouteDecision:               "scripted_single_model",
			Provider:                    "openai",
			ProviderLabel:               "openai-api",
			ModelID:                     "main",
			ReviewerSeparated:           false,
			ReviewerSeparationStatus:    "single_model_disclosed",
			IndependentReviewDisclosure: "no independent reviewer evidence was available for this single-model turn",
			FinalGateState:              runtimeGateStatusReady,
			RouteQuality:                "single_model",
			EvidenceRef:                 "fixture/single",
			NextCommand:                 "/status detail",
		})
	}
	finalizeLiveProviderDrillGate(&report)
	if report.FinalGateStatus != runtimeGateStatusReady || report.Status != liveProviderDrillStatusPassed {
		t.Fatalf("single-model disclosed soak should pass, got %#v", report)
	}
	for _, turn := range report.Turns {
		if turn.ReviewerSeparationStatus != "single_model_disclosed" || turn.IndependentReviewDisclosure == "" {
			t.Fatalf("missing single-model disclosure in turn %#v", turn)
		}
	}
}

func TestLiveProviderSoakSessionReloadPreservesRejectedCorrection(t *testing.T) {
	rt, _ := newLiveProviderSoakTestRuntime(t)
	rejected := &FinalAnswerCorrectionVisibility{
		Required:     true,
		Rejected:     true,
		Status:       finalAnswerCorrectionStatusRejected,
		Reasons:      []string{"validation_disclosure"},
		AttemptCount: 2,
		MaxAttempts:  2,
	}
	rejected.Normalize()
	rt.session.LastFinalAnswerCorrection = rejected
	report, ledger, err := rt.runLiveProviderSoak(context.Background(), LiveProviderSoakOptions{
		Mode: liveProviderDrillModeScripted,
		Now:  fixedSoakNow(),
	})
	if err != nil {
		t.Fatalf("run scripted soak: %v", err)
	}
	if report.FinalAnswerCorrection == nil || !report.FinalAnswerCorrection.Rejected {
		t.Fatalf("soak regenerated correction state instead of preserving rejection: %#v", report.FinalAnswerCorrection)
	}
	if ledger.FinalAnswerCorrection == nil || !ledger.FinalAnswerCorrection.Rejected {
		t.Fatalf("ledger did not preserve rejected correction: %#v", ledger.FinalAnswerCorrection)
	}
	if report.ReloadVerification == nil || !report.ReloadVerification.Reloaded || !report.ReloadVerification.StatusDetail {
		t.Fatalf("reload verification missing detail status: %#v", report.ReloadVerification)
	}
}

func TestOptionalRealProviderSoak(t *testing.T) {
	if os.Getenv(liveProviderSoakEnvEnabled) != "1" {
		t.Skip("skipped real-provider soak: set KERNFORGE_REAL_PROVIDER_SOAK=1 with KERNFORGE_REAL_PROVIDER and KERNFORGE_REAL_MODEL to run it")
	}
	rt, _ := newLiveProviderSoakTestRuntime(t)
	report, _, err := rt.runLiveProviderSoak(context.Background(), LiveProviderSoakOptions{
		Mode:    liveProviderDrillModeReal,
		Timeout: 45 * time.Second,
		Turns:   2,
	})
	if err != nil {
		t.Fatalf("real-provider soak artifact write failed: %v", err)
	}
	if report.Status == liveProviderDrillStatusSkipped {
		t.Skipf("skipped real-provider soak: %s artifact_dir=%s", report.SkippedReason, report.ArtifactDir)
	}
	if report.Status != liveProviderDrillStatusPassed || len(report.Turns) == 0 {
		t.Fatalf("real-provider soak failed: %#v", report)
	}
}

func newLiveProviderSoakTestRuntime(t *testing.T) (*runtimeState, string) {
	t.Helper()
	root := initTestGitRepo(t)
	store := NewSessionStore(filepath.Join(root, ".kernforge", "sessions-test"))
	session := NewSession(root, "scripted", "model", "", "default")
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "model"
	rt := &runtimeState{
		writer:    &bytes.Buffer{},
		ui:        NewUI(),
		cfg:       cfg,
		store:     store,
		session:   session,
		evidence:  &EvidenceStore{Path: filepath.Join(root, "evidence.json"), MaxEntries: 100},
		workspace: Workspace{BaseRoot: root, Root: root},
	}
	if err := store.Save(session); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return rt, root
}

func liveProviderSoakSingleCrossFixture() LiveProviderDrillReport {
	report := LiveProviderDrillReport{
		ID:                 "cross-fixture",
		SchemaVersion:      liveProviderDrillSchemaVersion,
		Mode:               liveProviderDrillModeScripted,
		Status:             liveProviderDrillStatusPassed,
		ProviderConfigured: true,
		Turns: []LiveProviderDrillTurn{{
			TurnID:                      "cross-1",
			RequestClass:                reviewRequestClassModifyThenReview,
			LifecycleKind:               reviewLifecycleKindModifyThenReview,
			RouteMode:                   reviewRouteModeCrossModel,
			RouteDecision:               "fixture",
			Provider:                    "openai",
			ProviderLabel:               "openai-api",
			ModelID:                     "main",
			ReviewerProvider:            "anthropic",
			ReviewerProviderLabel:       "anthropic-api",
			ReviewerModelID:             "reviewer",
			ReviewerSeparated:           true,
			ReviewerSeparationStatus:    "independent",
			IndependentReviewDisclosure: "independent reviewer evidence was available",
			FinalGateState:              runtimeGateStatusReady,
			RouteQuality:                "healthy",
			EvidenceRef:                 "fixture/cross",
			NextCommand:                 "/status detail",
		}},
	}
	finalizeLiveProviderDrillGate(&report)
	return report
}

func mapGetenv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func fixedSoakNow() func() time.Time {
	now := time.Date(2026, 5, 29, 13, 0, 0, 0, time.UTC)
	return func() time.Time {
		return now
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return payload
}
