package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestScenarioReplayMatrix(t *testing.T) {
	scenarios, err := LoadRequestScenarios(filepath.Join("testdata", "request_scenarios"))
	if err != nil {
		t.Fatalf("LoadRequestScenarios: %v", err)
	}
	if len(scenarios) < 14 {
		t.Fatalf("expected minimum scenario set, got %d", len(scenarios))
	}
	registry := requestScenarioReplayRegistry()
	seen := map[string]bool{}
	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.Name, func(t *testing.T) {
			seen[scenario.Name] = true
			result, err := ReplayRequestScenario(t.TempDir(), scenario, registry)
			if err != nil {
				t.Fatalf("ReplayRequestScenario: %v", err)
			}
			assertRequestScenarioEnvelope(t, scenario.ExpectedRequestEnvelope, result.RequestEnvelope)
			assertRequestScenarioToolExposure(t, scenario.ExpectedToolExposure, result.ToolExposure)
			assertRequestScenarioInterventions(t, scenario.ExpectedInterventions, result.Interventions)
			assertRequestScenarioFinalGate(t, scenario.ExpectedFinalGate, result.FinalGateDecision)
		})
	}
	for _, name := range []string{
		"korean_explicit_edit",
		"korean_review_only",
		"plan_only",
		"korean_answer_only_comparison_summary",
		"korean_document_artifact_summary",
		"draft_only_goal_prompt",
		"document_authoring",
		"latest_current_research",
		"explicit_git_commit_push",
		"non_explicit_git",
		"empty_stop",
		"orphan_tool_call",
		"repeated_read_loop",
		"generated_doc_final_only",
		"verification_unavailable",
		"compaction_history_rewrite",
	} {
		if !seen[name] {
			t.Fatalf("required scenario %q was not loaded", name)
		}
	}
}

func TestScenarioReplayLoadsRequestScenarioDirectory(t *testing.T) {
	scenarios, err := LoadRequestScenarios(filepath.Join("testdata", "request_scenarios"))
	if err != nil {
		t.Fatalf("LoadRequestScenarios: %v", err)
	}
	if len(scenarios) == 0 {
		t.Fatalf("expected scenarios")
	}
	for _, scenario := range scenarios {
		if strings.TrimSpace(scenario.Name) == "" || strings.TrimSpace(scenario.UserText) == "" {
			t.Fatalf("scenario must have name and user_text: %#v", scenario)
		}
	}
}

func TestRequestRuntimeShadowModeDoesNotChangeLegacyDecision(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.RequestRuntime = RequestRuntimeConfig{Mode: RequestRuntimeModeShadow, EnabledClasses: []string{RequestRuntimeClassAll}}
	envelope := buildRequestEnvelope("main.go 버그를 고쳐줘")
	legacy := RequestRuntimeDecisionSummary{Source: "legacy", RequestClass: RequestRuntimeClassExplicitEdit, FinalGateState: string(FinalGateReady), FinalGateReady: true}
	v2 := RequestRuntimeDecisionSummary{Source: "v2", RequestClass: RequestRuntimeClassExplicitEdit, FinalGateState: string(FinalGateNeedsVerification), FinalGateReady: false}
	selected := selectRequestRuntimeDecision(cfg, envelope, legacy, v2)
	if selected.Source != "legacy" || !selected.FinalGateReady {
		t.Fatalf("shadow mode must preserve legacy behavior, got %#v", selected)
	}
}

func TestRequestRuntimeEnabledModeUsesV2DecisionForEnabledClass(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.RequestRuntime = RequestRuntimeConfig{Mode: RequestRuntimeModeEnabled, EnabledClasses: []string{RequestRuntimeClassExplicitEdit}}
	editEnvelope := buildRequestEnvelope("main.go 버그를 고쳐줘")
	planEnvelope := buildRequestEnvelope("개선 방향을 먼저 정하자")
	legacy := RequestRuntimeDecisionSummary{Source: "legacy", RequestClass: RequestRuntimeClassExplicitEdit, FinalGateState: string(FinalGateReady), FinalGateReady: true}
	v2 := RequestRuntimeDecisionSummary{Source: "v2", RequestClass: RequestRuntimeClassExplicitEdit, FinalGateState: string(FinalGateNeedsVerification), FinalGateReady: false}
	selected := selectRequestRuntimeDecision(cfg, editEnvelope, legacy, v2)
	if selected.Source != "v2" || selected.FinalGateReady {
		t.Fatalf("enabled explicit_edit path should select v2 decision, got %#v", selected)
	}
	selected = selectRequestRuntimeDecision(cfg, planEnvelope, legacy, v2)
	if selected.Source != "legacy" {
		t.Fatalf("unenabled plan path should keep legacy decision, got %#v", selected)
	}
}

func TestRequestRuntimeShadowDivergenceLogIsSanitizedAndUnderKernforge(t *testing.T) {
	root := t.TempDir()
	comparison := compareRequestRuntimeDecisions(
		RequestRuntimeDecisionSummary{
			Source:         "legacy",
			RequestClass:   RequestRuntimeClassExplicitEdit,
			ExposedTools:   []string{"read_file"},
			FinalGateState: string(FinalGateReady),
			FinalGateReady: true,
		},
		RequestRuntimeDecisionSummary{
			Source:         "v2",
			RequestClass:   RequestRuntimeClassExplicitEdit,
			ExposedTools:   []string{"read_file", "apply_patch"},
			FinalGateState: string(FinalGateNeedsVerification),
			FinalGateReady: false,
		},
	)
	path, err := writeRequestRuntimeShadowDivergence(root, RequestRuntimeConfig{}, comparison)
	if err != nil {
		t.Fatalf("writeRequestRuntimeShadowDivergence: %v", err)
	}
	expectedRoot := filepath.Join(root, userConfigDirName, requestRuntimeShadowDirName)
	if !pathIsInsideRoot(expectedRoot, path) {
		t.Fatalf("shadow divergence log escaped .kernforge shadow dir: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	for _, forbidden := range []string{"user_text", "provider_scripted_outputs", "messages", "large_context", "secret-token"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("shadow log must not contain %q:\n%s", forbidden, text)
		}
	}
	for _, want := range []string{"request_class", "exposed_tools", "final_gate"} {
		if !strings.Contains(text, want) {
			t.Fatalf("shadow log should contain sanitized summary %q:\n%s", want, text)
		}
	}
}

func TestRequestRuntimeShadowRejectsLogOutsideKernforge(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "shadow")
	_, err := writeRequestRuntimeShadowDivergence(root, RequestRuntimeConfig{LogDir: outside}, RequestRuntimeShadowComparison{Diverged: true})
	if err == nil {
		t.Fatalf("expected outside shadow log path to be rejected")
	}
}

func TestRequestRuntimeDisabledModeHasNoShadowSideEffects(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Config:    DefaultConfig(root),
		Tools:     requestScenarioReplayRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}
	envelope := buildRequestEnvelope("main.go 버그를 고쳐줘")
	agent.observeRequestRuntimeShadow(envelope, NewTurnRuntimeState(envelope), FinalGateDecision{State: FinalGateReady, Ready: true}, false, false, false, false, false, false, "완료했습니다.", TurnRuntimeFinalContext{})
	if session.LastRequestRuntimeShadow != nil {
		t.Fatalf("disabled mode should not record shadow comparison, got %#v", session.LastRequestRuntimeShadow)
	}
	if _, err := os.Stat(filepath.Join(root, userConfigDirName, requestRuntimeShadowDirName)); !os.IsNotExist(err) {
		t.Fatalf("disabled mode should not create shadow log directory, err=%v", err)
	}
}

func TestSemanticClassifierShadowRecordsCandidateDecision(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.RequestRuntime.SemanticClassifier = RequestSemanticClassifierConfig{Mode: RequestSemanticClassifierModeShadow}
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Config:    cfg,
		Tools:     requestScenarioReplayRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}
	envelope := buildRequestEnvelope("main.go 버그를 고쳐줘")
	semanticCandidate := sanitizeSemanticRequestEnvelopeCandidate(buildRequestEnvelope("main.go를 분석만 해. 파일은 수정하지 마"))
	session.LastSemanticRequestEnvelope = &semanticCandidate
	agent.observeRequestRuntimeShadow(envelope, NewTurnRuntimeState(envelope), FinalGateDecision{State: FinalGateReady, Ready: true}, false, false, false, false, false, false, "수정 완료했습니다.", TurnRuntimeFinalContext{
		AttemptedEditTool:   true,
		ExplicitEditRequest: true,
	})
	comparison := session.LastRequestRuntimeShadow
	if comparison == nil {
		t.Fatalf("semantic shadow mode should record comparison")
	}
	if comparison.SemanticDecision == nil {
		t.Fatalf("expected semantic decision summary, got %#v", comparison)
	}
	if comparison.SemanticDecision.RequestClass == comparison.V2Decision.RequestClass {
		t.Fatalf("expected semantic request class to differ, got %#v", comparison)
	}
	if !sliceContainsFold(comparison.SemanticDifferences, "final_gate") {
		t.Fatalf("expected semantic final gate difference, got %#v", comparison.SemanticDifferences)
	}
	if !sliceContainsFold(comparison.Differences, "semantic_final_gate") {
		t.Fatalf("expected top-level semantic final gate label, got %#v", comparison.Differences)
	}
	if strings.TrimSpace(comparison.ShadowLogPath) == "" {
		t.Fatalf("expected semantic divergence log path, got %#v", comparison)
	}
	data, err := os.ReadFile(comparison.ShadowLogPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), "main.go를 분석만") {
		t.Fatalf("semantic shadow log must not contain candidate user text:\n%s", string(data))
	}
}

func requestScenarioReplayRegistry() *ToolRegistry {
	return NewToolRegistry(
		&toolContractRecordingTool{name: "read_file", readOnly: true},
		&toolContractRecordingTool{name: "apply_patch"},
		&toolContractRecordingTool{name: "write_file"},
		&toolContractRecordingTool{name: "replace_in_file"},
		&toolContractRecordingTool{name: "git_commit"},
		&toolContractRecordingTool{name: "git_push"},
		&toolContractRecordingTool{name: "web_search", readOnly: true},
	)
}

func assertRequestScenarioEnvelope(t *testing.T, expected RequestScenarioExpectedEnvelope, actual RequestEnvelope) {
	t.Helper()
	actual.Normalize()
	if expected.PrimaryClass != "" && string(actual.PrimaryClass) != expected.PrimaryClass {
		t.Fatalf("primary_class: expected %q got %q in %#v", expected.PrimaryClass, actual.PrimaryClass, actual)
	}
	assertBoolPtr := func(label string, expected *bool, actual bool) {
		t.Helper()
		if expected != nil && *expected != actual {
			t.Fatalf("%s: expected %t got %t in %#v", label, *expected, actual, actual)
		}
	}
	assertBoolPtr("allows_file_mutation", expected.AllowsFileMutation, actual.AllowsFileMutation)
	assertBoolPtr("allows_git_mutation", expected.AllowsGitMutation, actual.AllowsGitMutation)
	assertBoolPtr("allows_web_research", expected.AllowsWebResearch, actual.AllowsWebResearch)
	assertBoolPtr("requires_fresh_external_info", expected.RequiresFreshExternalInfo, actual.RequiresFreshExternalInfo)
	assertBoolPtr("requires_verification", expected.RequiresVerification, actual.RequiresVerification)
	assertBoolPtr("read_only_analysis", expected.ReadOnlyAnalysis, actual.ReadOnlyAnalysis)
	assertBoolPtr("explicit_edit_request", expected.ExplicitEditRequest, actual.ExplicitEditRequest)
	assertBoolPtr("explicit_git_request", expected.ExplicitGitRequest, actual.ExplicitGitRequest)
	assertBoolPtr("document_authoring", expected.DocumentAuthoring, actual.DocumentAuthoring)
	assertBoolPtr("goal_prompt_draft_only", expected.GoalPromptDraftOnly, actual.GoalPromptDraftOnly)
}

func assertRequestScenarioToolExposure(t *testing.T, expected RequestScenarioExpectedToolExposure, actual RequestRuntimeDecisionSummary) {
	t.Helper()
	for _, name := range expected.Enabled {
		if !sliceContainsFold(actual.ExposedTools, name) {
			t.Fatalf("expected tool %q to be exposed, got exposed=%#v disabled=%#v", name, actual.ExposedTools, actual.DisabledTools)
		}
	}
	for _, name := range expected.Disabled {
		if !sliceContainsFold(actual.DisabledTools, name) {
			t.Fatalf("expected tool %q to be disabled, got exposed=%#v disabled=%#v", name, actual.ExposedTools, actual.DisabledTools)
		}
	}
}

func assertRequestScenarioInterventions(t *testing.T, expected []string, actual []string) {
	t.Helper()
	if len(expected) == 0 && len(actual) == 0 {
		return
	}
	for _, want := range expected {
		if !sliceContainsFold(actual, want) {
			t.Fatalf("expected intervention %q, got %#v", want, actual)
		}
	}
	if len(expected) == 0 && len(actual) > 0 {
		t.Fatalf("expected no interventions, got %#v", actual)
	}
}

func assertRequestScenarioFinalGate(t *testing.T, expected RequestScenarioExpectedFinalGate, actual FinalGateDecision) {
	t.Helper()
	if expected.State != "" && string(actual.State) != expected.State {
		t.Fatalf("final gate state: expected %q got %q in %#v", expected.State, actual.State, actual)
	}
	if expected.Ready != nil && *expected.Ready != actual.Ready {
		t.Fatalf("final gate ready: expected %t got %t in %#v", *expected.Ready, actual.Ready, actual)
	}
}
