package main

import (
	"bytes"
	"embed"
	"fmt"
	"sort"
	"strings"
	"text/template"
)

//go:embed prompts/*.md
var promptAssetsFS embed.FS

type PromptBlockID string

const (
	PromptBlockSystemBase             PromptBlockID = "system_base"
	PromptBlockToolPolicy             PromptBlockID = "tool_policy"
	PromptBlockRequestEnvelope        PromptBlockID = "request_envelope"
	PromptBlockEmptyStopRetry         PromptBlockID = "empty_stop_retry"
	PromptBlockRepeatedToolRedirect   PromptBlockID = "repeated_tool_redirect"
	PromptBlockBlockedTool            PromptBlockID = "blocked_tool"
	PromptBlockManualEditHandoffBlock PromptBlockID = "manual_edit_handoff_block"
	PromptBlockVerificationUnresolved PromptBlockID = "verification_unresolved"
	PromptBlockLengthStopContinue     PromptBlockID = "length_stop_continue"
)

var promptBlockAssetPaths = map[PromptBlockID]string{
	PromptBlockSystemBase:             "prompts/system_base.md",
	PromptBlockToolPolicy:             "prompts/tool_policy.md",
	PromptBlockRequestEnvelope:        "prompts/request_envelope.md",
	PromptBlockEmptyStopRetry:         "prompts/empty_stop_retry.md",
	PromptBlockRepeatedToolRedirect:   "prompts/repeated_tool_redirect.md",
	PromptBlockBlockedTool:            "prompts/blocked_tool.md",
	PromptBlockManualEditHandoffBlock: "prompts/manual_edit_handoff_block.md",
	PromptBlockVerificationUnresolved: "prompts/verification_unresolved.md",
	PromptBlockLengthStopContinue:     "prompts/length_stop_continue.md",
}

var renderPromptBlockForRuntime = RenderPromptBlock

type RequestEnvelopePromptData struct {
	PrimaryClass              string
	ClassesText               string
	Boundary                  string
	AllowsFileMutation        bool
	AllowsGitMutation         bool
	AllowsWebResearch         bool
	RequiresVerification      bool
	RequiresFreshExternalInfo bool
	ReviewRequestClass        string
	ConfidenceText            string
	WarningsText              string
	ReadOnlyAnalysis          bool
	ExplicitEditRequest       bool
	ExplicitGitRequest        bool
	GoalPromptDraftOnly       bool
	DocumentAuthoring         bool
	GitMutationGuard          bool
}

type EmptyStopRetryPromptData struct {
	ReadOnlyAnalysis bool
	StopReason       string
	Count            int
}

type RepeatedToolRedirectPromptData struct {
	Reason               string
	NextStepRequirements string
	LoopSignature        string
	DetailTitle          string
	RepeatedSequence     string
	RecentToolTurns      string
}

type RuntimeInterventionPromptData struct {
	Kind                           string
	Reason                         string
	Guidance                       string
	RuntimeState                   string
	StopReason                     string
	Count                          int
	Iteration                      int
	ToolCallsSummary               string
	Message                        string
	UnresolvedVerification         bool
	FinalAnswerOnlyCorrection      bool
	GeneratedDocumentHarnessOwnsIt bool
}

type LengthStopContinuePromptData struct {
	StopReason string
}

type ToolContractPromptData struct {
	ResultKind       string
	Reason           string
	ToolName         string
	ToolCallID       string
	ToolCallsSummary string
	Guidance         string
}

func PromptBlockIDs() []PromptBlockID {
	ids := make([]PromptBlockID, 0, len(promptBlockAssetPaths))
	for id := range promptBlockAssetPaths {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return ids[i] < ids[j]
	})
	return ids
}

func PromptBlockAssetPath(id PromptBlockID) (string, bool) {
	path, ok := promptBlockAssetPaths[id]
	return path, ok
}

func LoadPromptBlock(id PromptBlockID) (string, error) {
	path, ok := PromptBlockAssetPath(id)
	if !ok {
		return "", fmt.Errorf("unknown prompt block %q", id)
	}
	data, err := promptAssetsFS.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("load prompt block %s: %w", id, err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", fmt.Errorf("prompt block %s is empty", id)
	}
	return text, nil
}

func RenderPromptBlock(id PromptBlockID, data any) (string, error) {
	raw, err := LoadPromptBlock(id)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New(string(id)).Option("missingkey=error").Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse prompt block %s: %w", id, err)
	}
	if data == nil {
		data = map[string]any{}
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render prompt block %s: %w", id, err)
	}
	rendered := strings.TrimSpace(out.String())
	if rendered == "" {
		return "", fmt.Errorf("render prompt block %s: empty output", id)
	}
	return rendered, nil
}

func RenderRequestEnvelopePromptBlock(envelope RequestEnvelope) (string, error) {
	return RenderPromptBlock(PromptBlockRequestEnvelope, NewRequestEnvelopePromptData(envelope))
}

func NewRequestEnvelopePromptData(envelope RequestEnvelope) RequestEnvelopePromptData {
	envelope.Normalize()
	classes := make([]string, 0, len(envelope.Classes))
	for _, class := range envelope.Classes {
		if strings.TrimSpace(string(class)) != "" {
			classes = append(classes, string(class))
		}
	}
	confidenceText := ""
	if envelope.Confidence > 0 {
		confidenceText = fmt.Sprintf("%.2f", envelope.Confidence)
	}
	return RequestEnvelopePromptData{
		PrimaryClass:              strings.TrimSpace(string(envelope.PrimaryClass)),
		ClassesText:               strings.Join(classes, ", "),
		Boundary:                  strings.TrimSpace(string(envelope.Boundary)),
		AllowsFileMutation:        envelope.AllowsFileMutation,
		AllowsGitMutation:         envelope.AllowsGitMutation,
		AllowsWebResearch:         envelope.AllowsWebResearch,
		RequiresVerification:      envelope.RequiresVerification,
		RequiresFreshExternalInfo: envelope.RequiresFreshExternalInfo,
		ReviewRequestClass:        strings.TrimSpace(envelope.ReviewRequestClass),
		ConfidenceText:            confidenceText,
		WarningsText:              strings.Join(envelope.Warnings, " | "),
		ReadOnlyAnalysis:          envelope.ReadOnlyAnalysis,
		ExplicitEditRequest:       envelope.ExplicitEditRequest,
		ExplicitGitRequest:        envelope.ExplicitGitRequest,
		GoalPromptDraftOnly:       envelope.GoalPromptDraftOnly,
		DocumentAuthoring:         envelope.DocumentAuthoring,
		GitMutationGuard:          !envelope.AllowsGitMutation,
	}
}

func RenderEmptyStopRetryPrompt(readOnlyAnalysis bool, stopReason string, count int) string {
	fallback := "Please provide the final answer to the user now. Do not return an empty message."
	if readOnlyAnalysis {
		fallback = "Your last reply was empty. This is a read-only analysis or review request. If you need more evidence, use read_file, grep, or list_files on the referenced code first. Then provide a concrete final answer with findings, likely root causes, and file references. Do not return an empty message."
	}
	rendered, err := RenderPromptBlock(PromptBlockEmptyStopRetry, EmptyStopRetryPromptData{
		ReadOnlyAnalysis: readOnlyAnalysis,
		StopReason:       normalizeStopReason(stopReason),
		Count:            count,
	})
	if err != nil {
		return fallback
	}
	return rendered
}

func RenderRepeatedToolRedirectPrompt(data RepeatedToolRedirectPromptData, fallback string) string {
	data.Reason = strings.TrimSpace(data.Reason)
	data.NextStepRequirements = strings.TrimSpace(data.NextStepRequirements)
	data.LoopSignature = strings.TrimSpace(data.LoopSignature)
	data.DetailTitle = strings.TrimSpace(data.DetailTitle)
	if data.DetailTitle == "" {
		data.DetailTitle = "Repeated tool sequence"
	}
	data.RepeatedSequence = strings.TrimSpace(data.RepeatedSequence)
	data.RecentToolTurns = strings.TrimSpace(data.RecentToolTurns)
	rendered, err := RenderPromptBlock(PromptBlockRepeatedToolRedirect, data)
	if err != nil {
		return strings.TrimSpace(fallback)
	}
	return rendered
}

func RenderManualEditHandoffBlockPrompt(reason string, count int) string {
	fallback := "This request explicitly asks you to inspect and fix the code. Do not hand the patch back to the user. Read the relevant file if needed, then use the available edit tools directly. Only ask the user to edit manually if an edit tool actually failed, and cite that exact tool error."
	rendered, err := RenderPromptBlock(PromptBlockManualEditHandoffBlock, RuntimeInterventionPromptData{
		Kind:     string(RuntimeInterventionManualEditHandoff),
		Reason:   strings.TrimSpace(reason),
		Count:    count,
		Guidance: fallback,
	})
	if err != nil {
		return fallback
	}
	return rendered
}

func RenderVerificationUnresolvedPrompt(state *TurnRuntimeState, item RuntimeIntervention, message string, generatedDocumentHarnessOwnsIt bool) string {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Verification is still unresolved. Continue fixing the issue if possible. If verification was skipped or declined, give a final answer that explicitly says verification was not run and do not describe it as completed."
	}
	data := RuntimeInterventionPromptData{
		Kind:                           string(RuntimeInterventionVerificationUnresolved),
		Reason:                         strings.TrimSpace(item.Reason),
		Guidance:                       strings.TrimSpace(item.Guidance),
		Message:                        message,
		StopReason:                     normalizeStopReason(item.StopReason),
		Count:                          item.Count,
		Iteration:                      item.Iteration,
		GeneratedDocumentHarnessOwnsIt: generatedDocumentHarnessOwnsIt,
	}
	if state != nil {
		data.RuntimeState = string(state.State)
		data.UnresolvedVerification = state.UnresolvedVerification
		data.FinalAnswerOnlyCorrection = state.FinalAnswerOnlyCorrection
	}
	rendered, err := RenderPromptBlock(PromptBlockVerificationUnresolved, data)
	if err != nil {
		return message
	}
	return rendered
}

func RenderLengthStopContinuePrompt(stopReason string) string {
	rendered, err := RenderPromptBlock(PromptBlockLengthStopContinue, LengthStopContinuePromptData{
		StopReason: normalizeStopReason(stopReason),
	})
	if err != nil {
		if normalized := normalizeStopReason(stopReason); normalized != "" {
			return "The model stopped before producing a usable response because the response hit a token limit. Do not treat the turn as complete.\nStop reason: " + normalized + "."
		}
		return "The model stopped before producing a usable response because the response hit a token limit. Do not treat the turn as complete."
	}
	return rendered
}

func RenderBlockedToolPromptFromIntervention(item RuntimeIntervention) string {
	data := RuntimeInterventionPromptData{
		Kind:             string(normalizeRuntimeInterventionKind(item.Kind)),
		Reason:           strings.TrimSpace(item.Reason),
		Guidance:         strings.TrimSpace(item.Guidance),
		StopReason:       normalizeStopReason(item.StopReason),
		Count:            item.Count,
		Iteration:        item.Iteration,
		ToolCallsSummary: summarizePromptToolCalls(item.ToolCalls),
	}
	if data.Reason == "" {
		data.Reason = "this tool-call batch was redirected by the runtime before execution"
	}
	rendered, err := RenderPromptBlock(PromptBlockBlockedTool, data)
	if err != nil {
		return strings.TrimSpace(item.Guidance)
	}
	return rendered
}

func RenderBlockedToolPromptFromContract(result ToolContractSyntheticResult, guidance string) string {
	data := ToolContractPromptData{
		ResultKind: string(result.Kind),
		Reason:     strings.TrimSpace(result.Reason),
		ToolName:   strings.TrimSpace(result.Call.Name),
		ToolCallID: strings.TrimSpace(result.Call.ID),
		Guidance:   strings.TrimSpace(guidance),
	}
	if data.Reason == "" {
		data.Reason = defaultToolContractSyntheticReason(result.Kind)
	}
	data.ToolCallsSummary = summarizePromptToolCalls([]ToolCall{result.Call})
	rendered, err := RenderPromptBlock(PromptBlockBlockedTool, data)
	if err != nil {
		return data.Guidance
	}
	return rendered
}

func summarizePromptToolCalls(calls []ToolCall) string {
	lines := make([]string, 0, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			name = "unknown_tool"
		}
		id := strings.TrimSpace(call.ID)
		line := "- " + name
		if id != "" {
			line += " id=" + id
		}
		if args := truncateStatusSnippet(strings.Join(strings.Fields(call.Arguments), " "), 180); args != "" {
			line += " args=" + args
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (a *Agent) renderPromptBlockOrFallback(id PromptBlockID, data any, fallback string) string {
	rendered, err := renderPromptBlockForRuntime(id, data)
	if err != nil {
		a.emitPromptAssemblyProgress(id, err)
		return strings.TrimSpace(fallback)
	}
	return rendered
}

func (a *Agent) safePromptSection(label string, fallback string, build func() string) string {
	if build == nil {
		return strings.TrimSpace(fallback)
	}
	var text string
	var recovered any
	func() {
		defer func() {
			if value := recover(); value != nil {
				recovered = value
			}
		}()
		text = build()
	}()
	if recovered != nil {
		a.emitPromptAssemblyProgress(PromptBlockID(label), fmt.Errorf("prompt section panic: %v", recovered))
		return strings.TrimSpace(fallback)
	}
	return strings.TrimSpace(text)
}

func (a *Agent) emitPromptAssemblyProgress(id PromptBlockID, err error) {
	if a == nil || err == nil {
		return
	}
	a.emitProgressEvent(ProgressEvent{
		Kind:        progressKindPromptAssembly,
		PromptBlock: string(id),
		Status:      truncateStatusSnippet(err.Error(), 200),
	})
}
