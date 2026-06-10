package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type RequestScenario struct {
	Name                    string                              `json:"name"`
	UserText                string                              `json:"user_text"`
	SessionState            RequestScenarioSessionState         `json:"session_state,omitempty"`
	ProviderScriptedOutputs []RequestScenarioProviderOutput     `json:"provider_scripted_outputs,omitempty"`
	ExpectedRequestEnvelope RequestScenarioExpectedEnvelope     `json:"expected_request_envelope,omitempty"`
	ExpectedToolExposure    RequestScenarioExpectedToolExposure `json:"expected_tool_exposure,omitempty"`
	ExpectedInterventions   []string                            `json:"expected_interventions,omitempty"`
	ExpectedFinalGate       RequestScenarioExpectedFinalGate    `json:"expected_final_gate,omitempty"`
}

type RequestScenarioSessionState struct {
	Messages                       []Message `json:"messages,omitempty"`
	ChangedFiles                   []string  `json:"changed_files,omitempty"`
	VerificationStatus             string    `json:"verification_status,omitempty"`
	UnresolvedVerification         bool      `json:"unresolved_verification,omitempty"`
	GeneratedDocumentHarnessOwnsIt bool      `json:"generated_document_harness_owns_it,omitempty"`
	OrphanToolResult               bool      `json:"orphan_tool_result,omitempty"`
}

type RequestScenarioProviderOutput struct {
	Text       string     `json:"text,omitempty"`
	StopReason string     `json:"stop_reason,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type RequestScenarioExpectedEnvelope struct {
	PrimaryClass              string `json:"primary_class,omitempty"`
	AllowsFileMutation        *bool  `json:"allows_file_mutation,omitempty"`
	AllowsGitMutation         *bool  `json:"allows_git_mutation,omitempty"`
	AllowsWebResearch         *bool  `json:"allows_web_research,omitempty"`
	RequiresFreshExternalInfo *bool  `json:"requires_fresh_external_info,omitempty"`
	RequiresVerification      *bool  `json:"requires_verification,omitempty"`
	ReadOnlyAnalysis          *bool  `json:"read_only_analysis,omitempty"`
	ExplicitEditRequest       *bool  `json:"explicit_edit_request,omitempty"`
	ExplicitGitRequest        *bool  `json:"explicit_git_request,omitempty"`
	DocumentAuthoring         *bool  `json:"document_authoring,omitempty"`
	GoalPromptDraftOnly       *bool  `json:"goal_prompt_draft_only,omitempty"`
}

type RequestScenarioExpectedToolExposure struct {
	Enabled  []string `json:"enabled,omitempty"`
	Disabled []string `json:"disabled,omitempty"`
}

type RequestScenarioExpectedFinalGate struct {
	State string `json:"state,omitempty"`
	Ready *bool  `json:"ready,omitempty"`
}

type RequestScenarioReplayResult struct {
	Name              string                        `json:"name,omitempty"`
	RequestEnvelope   RequestEnvelope               `json:"request_envelope"`
	ToolExposure      RequestRuntimeDecisionSummary `json:"tool_exposure"`
	Interventions     []string                      `json:"interventions,omitempty"`
	FinalGateDecision FinalGateDecision             `json:"final_gate_decision"`
}

func LoadRequestScenarios(dir string) ([]RequestScenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var scenarios []RequestScenario
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") || strings.EqualFold(entry.Name(), "schema.json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		loaded, err := parseRequestScenarioFile(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		scenarios = append(scenarios, loaded...)
	}
	if len(scenarios) == 0 {
		return nil, fmt.Errorf("no request scenarios found in %s", dir)
	}
	for i := range scenarios {
		scenarios[i].Normalize()
	}
	return scenarios, nil
}

func parseRequestScenarioFile(data []byte) ([]RequestScenario, error) {
	var list []RequestScenario
	if err := json.Unmarshal(data, &list); err == nil {
		return list, nil
	}
	var single RequestScenario
	if err := json.Unmarshal(data, &single); err != nil {
		return nil, err
	}
	return []RequestScenario{single}, nil
}

func (s *RequestScenario) Normalize() {
	if s == nil {
		return
	}
	s.Name = strings.TrimSpace(s.Name)
	s.UserText = strings.TrimSpace(s.UserText)
	s.SessionState.ChangedFiles = normalizeTaskStateList(s.SessionState.ChangedFiles, 64)
	s.SessionState.VerificationStatus = strings.TrimSpace(s.SessionState.VerificationStatus)
	s.ExpectedToolExposure.Enabled = normalizeTaskStateList(s.ExpectedToolExposure.Enabled, 64)
	s.ExpectedToolExposure.Disabled = normalizeTaskStateList(s.ExpectedToolExposure.Disabled, 64)
	s.ExpectedInterventions = normalizeTaskStateList(s.ExpectedInterventions, 32)
	s.ExpectedFinalGate.State = strings.TrimSpace(s.ExpectedFinalGate.State)
}

func ReplayRequestScenario(root string, scenario RequestScenario, registry *ToolRegistry) (RequestScenarioReplayResult, error) {
	scenario.Normalize()
	if scenario.Name == "" {
		return RequestScenarioReplayResult{}, fmt.Errorf("scenario name is required")
	}
	if scenario.UserText == "" {
		return RequestScenarioReplayResult{}, fmt.Errorf("scenario %s user_text is required", scenario.Name)
	}
	session := requestScenarioSession(root, scenario)
	agent := &Agent{
		Config:    DefaultConfig(root),
		Tools:     registry,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	envelope := agent.latestRequestEnvelopeFor(scenario.UserText)
	plan := agent.buildTurnToolExposurePlanForEnvelope(nil, envelope, scenario.SessionState.UnresolvedVerification, false, false, false, envelope.AllowsWebResearch, false)
	turnRuntime := NewTurnRuntimeState(envelope)
	extraInterventions := applyRequestScenarioRuntimeSignals(turnRuntime, envelope, scenario, registry, session)
	finalInput := BuildFinalGateInput(root, session, envelope, turnRuntime, requestScenarioLastOutputText(scenario), TurnRuntimeFinalContext{
		GeneratedDocumentHarnessOwnsIt: scenario.SessionState.GeneratedDocumentHarnessOwnsIt,
		ExplicitEditRequest:            envelope.ExplicitEditRequest,
	})
	finalDecision := DecideFinalGate(finalInput)
	decisionSummary := buildRequestRuntimeDecisionSummary("v2", envelope, plan, turnRuntime, finalDecision, registry)
	result := RequestScenarioReplayResult{
		Name:              scenario.Name,
		RequestEnvelope:   envelope,
		ToolExposure:      decisionSummary,
		Interventions:     normalizeTaskStateList(append(requestScenarioInterventionNames(turnRuntime), extraInterventions...), 32),
		FinalGateDecision: finalDecision,
	}
	return result, nil
}

func requestScenarioSession(root string, scenario RequestScenario) *Session {
	session := NewSession(root, "scenario", "replay", "", "default")
	if len(scenario.SessionState.Messages) > 0 {
		session.Messages = append([]Message(nil), scenario.SessionState.Messages...)
	} else {
		session.Messages = []Message{{Role: "user", Text: scenario.UserText}}
	}
	if len(scenario.SessionState.ChangedFiles) > 0 {
		session.ActivePatchTransaction = &PatchTransaction{
			ID:            "scenario-patch",
			Goal:          scenario.UserText,
			WorkspaceRoot: root,
			Status:        patchTransactionStatusActive,
			StartedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Entries: []PatchTransactionEntry{{
				ID:       "scenario-entry",
				ToolName: "scenario",
				Status:   "success",
				Paths:    requestScenarioPatchPaths(scenario.SessionState.ChangedFiles),
			}},
		}
	}
	if report, ok := requestScenarioVerificationReport(root, scenario); ok {
		session.LastVerification = &report
	}
	return session
}

func requestScenarioPatchPaths(paths []string) []PatchPathChange {
	out := make([]PatchPathChange, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		out = append(out, PatchPathChange{Path: path, Operation: "modify"})
	}
	return out
}

func requestScenarioVerificationReport(root string, scenario RequestScenario) (VerificationReport, bool) {
	status := strings.ToLower(strings.TrimSpace(scenario.SessionState.VerificationStatus))
	if status == "" {
		return VerificationReport{}, false
	}
	stepStatus := VerificationStatus(status)
	switch stepStatus {
	case VerificationPassed, VerificationFailed, VerificationSkipped:
	default:
		return VerificationReport{}, false
	}
	return VerificationReport{
		GeneratedAt:  time.Now(),
		Trigger:      "request_scenario_replay",
		Mode:         VerificationAdaptive,
		Workspace:    root,
		ChangedPaths: append([]string(nil), scenario.SessionState.ChangedFiles...),
		Steps: []VerificationStep{{
			Label:   "scenario verification",
			Command: "scenario",
			Status:  stepStatus,
		}},
	}, true
}

func applyRequestScenarioRuntimeSignals(turnRuntime *TurnRuntimeState, envelope RequestEnvelope, scenario RequestScenario, registry *ToolRegistry, session *Session) []string {
	var extra []string
	readPathCounts := map[string]int{}
	for _, output := range scenario.ProviderScriptedOutputs {
		if strings.TrimSpace(output.Text) == "" && len(output.ToolCalls) == 0 {
			turnRuntime.RecordStopIntervention(RuntimeInterventionEmptyStop, output.StopReason, "scenario replay empty model response", "Recover from empty model response before finalizing.")
			continue
		}
		normalized := NormalizeAssistantToolCalls(output.ToolCalls, ToolContractNormalizationOptions{
			Registry:   registry,
			StopReason: output.StopReason,
		})
		for _, synthetic := range normalized.SyntheticResults {
			if synthetic.Kind == ToolContractSyntheticIncomplete || synthetic.Kind == ToolContractSyntheticUnsupported || synthetic.Kind == ToolContractSyntheticInvalid {
				turnRuntime.RecordInterventionKind(RuntimeInterventionBlockedTool, synthetic.Reason, synthetic.Guidance, []ToolCall{synthetic.Call})
			}
		}
		blocked := ValidateToolCallsAgainstEnvelope(normalized.Calls, envelope, ToolContractValidationOptions{Registry: registry, Session: session})
		for _, synthetic := range blocked {
			if synthetic.Kind == ToolContractSyntheticBlocked {
				turnRuntime.RecordInterventionKind(RuntimeInterventionBlockedTool, synthetic.Reason, synthetic.Guidance, []ToolCall{synthetic.Call})
			}
		}
		for _, call := range normalized.Calls {
			if strings.EqualFold(strings.TrimSpace(call.Name), "read_file") {
				path := normalizeSessionRelativePath(toolCallPathArgument(call))
				if path != "" {
					readPathCounts[path]++
				}
			}
		}
	}
	for path, count := range readPathCounts {
		if count > 1 {
			turnRuntime.RecordRepeatedTool(RuntimeInterventionRepeatedTool, "read_file repeated the same path", "Use the cached file evidence or move to a different action.", []ToolCall{{ID: "scenario_repeated_read", Name: "read_file", Arguments: fmt.Sprintf(`{"path":%q}`, path)}}, count)
		}
	}
	if scenario.SessionState.UnresolvedVerification {
		turnRuntime.RecordInterventionKind(RuntimeInterventionVerificationUnresolved, "scenario replay verification unresolved", "Resolve or disclose verification before finalizing.", nil)
	}
	if scenario.SessionState.OrphanToolResult {
		extra = append(extra, "orphan_tool_result")
	}
	return extra
}

func requestScenarioInterventionNames(turnRuntime *TurnRuntimeState) []string {
	if turnRuntime == nil {
		return nil
	}
	out := make([]string, 0, len(turnRuntime.Interventions))
	for _, item := range turnRuntime.Interventions {
		if item.Kind != "" {
			out = append(out, string(item.Kind))
		}
	}
	return out
}

func requestScenarioLastOutputText(scenario RequestScenario) string {
	for i := len(scenario.ProviderScriptedOutputs) - 1; i >= 0; i-- {
		if text := strings.TrimSpace(scenario.ProviderScriptedOutputs[i].Text); text != "" {
			return text
		}
	}
	return ""
}
