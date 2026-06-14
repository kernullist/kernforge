package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	RequestSemanticClassifierModeDisabled = "disabled"
	RequestSemanticClassifierModeShadow   = "shadow"
	RequestSemanticClassifierModeEnabled  = "enabled"

	defaultRequestSemanticClassifierMinConfidence = 0.72
	defaultRequestSemanticClassifierMaxTokens     = 700
)

type RequestSemanticClassifierConfig struct {
	Mode          string   `json:"mode,omitempty"`
	MinConfidence *float64 `json:"min_confidence,omitempty"`
	MaxTokens     int      `json:"max_tokens,omitempty"`
}

type RequestSemanticClassification struct {
	Intent                    string   `json:"intent,omitempty"`
	PrimaryClass              string   `json:"primary_class,omitempty"`
	ActionBoundary            string   `json:"action_boundary,omitempty"`
	ReadOnlyAnalysis          *bool    `json:"read_only_analysis,omitempty"`
	ExplicitEditRequest       *bool    `json:"explicit_edit_request,omitempty"`
	ExplicitGitRequest        *bool    `json:"explicit_git_request,omitempty"`
	DocumentAuthoring         *bool    `json:"document_authoring,omitempty"`
	AllowsWebResearch         *bool    `json:"allows_web_research,omitempty"`
	RequiresFreshExternalInfo *bool    `json:"requires_fresh_external_info,omitempty"`
	Confidence                float64  `json:"confidence,omitempty"`
	Reason                    string   `json:"reason,omitempty"`
	Signals                   []string `json:"signals,omitempty"`
}

func normalizeRequestSemanticClassifierConfig(cfg *RequestSemanticClassifierConfig) {
	if cfg == nil {
		return
	}
	cfg.Mode = normalizeRequestSemanticClassifierMode(cfg.Mode)
	if cfg.MinConfidence != nil {
		// Negative is treated as unset; >=0 is an explicit operator choice
		// (including 0 to accept all classifications).
		if *cfg.MinConfidence < 0 {
			cfg.MinConfidence = nil
		} else if *cfg.MinConfidence > 1 {
			clamped := 1.0
			cfg.MinConfidence = &clamped
		}
	}
	if cfg.MaxTokens < 0 {
		cfg.MaxTokens = 0
	}
}

func normalizeRequestSemanticClassifierMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", RequestSemanticClassifierModeDisabled:
		return RequestSemanticClassifierModeDisabled
	case RequestSemanticClassifierModeShadow:
		return RequestSemanticClassifierModeShadow
	case RequestSemanticClassifierModeEnabled:
		return RequestSemanticClassifierModeEnabled
	default:
		return RequestSemanticClassifierModeDisabled
	}
}

func requestSemanticClassifierMinConfidence(cfg RequestSemanticClassifierConfig) float64 {
	// nil means unset -> fall back to the default threshold. A configured
	// value (including 0 for accept-all) is honored verbatim after clamping.
	if cfg.MinConfidence == nil {
		return defaultRequestSemanticClassifierMinConfidence
	}
	value := *cfg.MinConfidence
	if value < 0 {
		return defaultRequestSemanticClassifierMinConfidence
	}
	if value > 1 {
		return 1
	}
	return value
}

func requestSemanticClassifierMaxTokens(cfg RequestSemanticClassifierConfig) int {
	if cfg.MaxTokens > 0 {
		return cfg.MaxTokens
	}
	return defaultRequestSemanticClassifierMaxTokens
}

func requestSemanticClassifierShadowModeEnabled(cfg Config) bool {
	runtimeCfg := cfg.RequestRuntime
	normalizeRequestRuntimeConfig(&runtimeCfg)
	return runtimeCfg.SemanticClassifier.Mode == RequestSemanticClassifierModeShadow
}

func (c *RequestSemanticClassification) Normalize() {
	if c == nil {
		return
	}
	c.Intent = string(normalizeSemanticTurnIntent(c.Intent))
	c.PrimaryClass = string(normalizeRequestClassValue(c.PrimaryClass))
	c.ActionBoundary = string(normalizeActionBoundaryValue(c.ActionBoundary))
	if c.Confidence < 0 {
		c.Confidence = 0
	}
	if c.Confidence > 1 {
		c.Confidence = 1
	}
	c.Reason = strings.TrimSpace(c.Reason)
	c.Signals = normalizeTaskStateList(c.Signals, 12)
}

func normalizeSemanticTurnIntent(value string) TurnIntent {
	switch TurnIntent(strings.ToLower(strings.TrimSpace(value))) {
	case TurnIntentDiagnoseRecentError:
		return TurnIntentDiagnoseRecentError
	case TurnIntentContinueLastTask:
		return TurnIntentContinueLastTask
	case TurnIntentExplainCurrentState:
		return TurnIntentExplainCurrentState
	case TurnIntentAskProjectKnowledge:
		return TurnIntentAskProjectKnowledge
	case TurnIntentReviewCode:
		return TurnIntentReviewCode
	case TurnIntentEditCode:
		return TurnIntentEditCode
	case TurnIntentRunCommand:
		return TurnIntentRunCommand
	case TurnIntentPlanOrDesign:
		return TurnIntentPlanOrDesign
	default:
		return ""
	}
}

func normalizeRequestClassValue(value string) RequestClass {
	switch RequestClass(strings.ToLower(strings.TrimSpace(value))) {
	case RequestClassQuestion:
		return RequestClassQuestion
	case RequestClassReview:
		return RequestClassReview
	case RequestClassPlan:
		return RequestClassPlan
	case RequestClassEdit:
		return RequestClassEdit
	case RequestClassDocument:
		return RequestClassDocument
	case RequestClassResearch:
		return RequestClassResearch
	case RequestClassGit:
		return RequestClassGit
	case RequestClassAmbiguous:
		return RequestClassAmbiguous
	default:
		return ""
	}
}

func normalizeActionBoundaryValue(value string) ActionBoundary {
	switch ActionBoundary(strings.ToLower(strings.TrimSpace(value))) {
	case ActionBoundaryReadOnly:
		return ActionBoundaryReadOnly
	case ActionBoundaryMayEdit:
		return ActionBoundaryMayEdit
	case ActionBoundaryMustEdit:
		return ActionBoundaryMustEdit
	case ActionBoundaryMayGit:
		return ActionBoundaryMayGit
	case ActionBoundaryNoCommit:
		return ActionBoundaryNoCommit
	default:
		return ""
	}
}

func parseRequestSemanticClassificationResponse(text string) (RequestSemanticClassification, error) {
	payload := extractJSONObjectText(text)
	if payload == "" {
		return RequestSemanticClassification{}, fmt.Errorf("semantic classifier response did not contain a JSON object")
	}
	var classification RequestSemanticClassification
	if err := json.Unmarshal([]byte(payload), &classification); err != nil {
		return RequestSemanticClassification{}, err
	}
	classification.Normalize()
	return classification, nil
}

func extractJSONObjectText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) >= 3 {
			lines = lines[1:]
			if strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
				lines = lines[:len(lines)-1]
			}
			trimmed = strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}
	// Scan forward from each '{', tracking brace depth and string state, and
	// return the first balanced object. Prose containing a stray '{' before
	// the real object must not break parsing, so we keep advancing to the next
	// candidate start when a balanced span does not unmarshal as an object.
	for offset := 0; offset < len(trimmed); {
		idx := strings.IndexByte(trimmed[offset:], '{')
		if idx < 0 {
			return ""
		}
		start := offset + idx
		candidate := balancedJSONObjectAt(trimmed, start)
		if candidate != "" {
			if json.Valid([]byte(candidate)) {
				return candidate
			}
		}
		offset = start + 1
	}
	return ""
}

func balancedJSONObjectAt(text string, start int) string {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(text[start : i+1])
			}
		}
	}
	return ""
}

func applySemanticRequestClassification(envelope RequestEnvelope, classification RequestSemanticClassification, cfg RequestSemanticClassifierConfig) RequestEnvelope {
	envelope.Normalize()
	classification.Normalize()
	if normalizeRequestSemanticClassifierMode(cfg.Mode) == RequestSemanticClassifierModeDisabled {
		return envelope
	}
	source := "semantic_classifier"
	minConfidence := requestSemanticClassifierMinConfidence(cfg)
	envelope.Evidence = append(envelope.Evidence, RequestEvidence{
		Source: source,
		Signal: firstNonBlankString(classification.PrimaryClass, "unclassified"),
		Detail: compactPromptSection(classification.Reason, 180),
	})
	if classification.Confidence < minConfidence {
		envelope.Warnings = append(envelope.Warnings, fmt.Sprintf("semantic classifier confidence %.2f below threshold %.2f", classification.Confidence, minConfidence))
		envelope.Normalize()
		return envelope
	}
	if normalizeRequestSemanticClassifierMode(cfg.Mode) == RequestSemanticClassifierModeShadow {
		envelope.Warnings = append(envelope.Warnings, "semantic classifier ran in shadow mode; deterministic request envelope remains authoritative")
		envelope.Normalize()
		return envelope
	}
	if semanticClassificationNarrowsToReadOnly(classification) && !requestEnvelopeHasDeterministicMutation(envelope) {
		envelope.ReadOnlyAnalysis = true
		envelope.ExplicitEditRequest = false
		envelope.DocumentAuthoring = false
		envelope.AllowsFileMutation = false
		if semanticBoolFalse(classification.ExplicitGitRequest) {
			envelope.ExplicitGitRequest = false
			envelope.AllowsGitMutation = false
		}
		if intent := normalizeSemanticTurnIntent(classification.Intent); intent != "" {
			envelope.Intent = intent
		}
		if class := normalizeRequestClassValue(classification.PrimaryClass); class != "" && !requestClassImpliesMutation(class) {
			envelope.PrimaryClass = class
			envelope.Classes = []RequestClass{class}
		} else if envelope.PrimaryClass == "" || requestClassImpliesMutation(envelope.PrimaryClass) {
			envelope.PrimaryClass = RequestClassQuestion
			envelope.Classes = []RequestClass{RequestClassQuestion}
		}
		envelope.ReviewRequestClass = reviewRequestClassGeneral
		if envelope.PrimaryClass == RequestClassReview {
			envelope.ReviewRequestClass = reviewRequestClassReviewOnly
		}
		envelope.ReviewLifecycleKind = reviewLifecycleKindAnalysis
		envelope.ReviewRequestClassReason = firstNonBlankString(classification.Reason, "semantic classifier narrowed request to read-only analysis")
	}
	if semanticClassificationPromotesDocumentAuthoring(classification) && requestEnvelopeAllowsSemanticDocumentPromotion(envelope) {
		envelope.ReadOnlyAnalysis = false
		envelope.ExplicitEditRequest = false
		envelope.DocumentAuthoring = true
		if intent := normalizeSemanticTurnIntent(classification.Intent); intent != "" {
			envelope.Intent = intent
		}
		envelope.PrimaryClass = RequestClassDocument
		envelope.Classes = []RequestClass{RequestClassDocument}
		envelope.ReviewRequestClass = reviewRequestClassDocumentArtifact
		envelope.ReviewLifecycleKind = reviewLifecycleKindDocumentArtifact
		envelope.ReviewRequestClassReason = firstNonBlankString(classification.Reason, "semantic classifier promoted low-confidence request to document artifact authoring")
	}
	if semanticBoolTrue(classification.RequiresFreshExternalInfo) && !requestEnvelopeLooksLikeLocalMutation(envelope) {
		envelope.RequiresFreshExternalInfo = true
		envelope.AllowsWebResearch = true
	}
	envelope.applyPolicy()
	if envelope.PrimaryClass == "" || len(envelope.Classes) == 0 {
		decision := requestEnvelopeReviewDecision(envelope)
		envelope.Classes = requestEnvelopeClasses(envelope, decision)
		envelope.PrimaryClass = requestEnvelopePrimaryClass(envelope, decision)
	}
	envelope.Normalize()
	return envelope
}

func semanticRequestClassificationCandidate(envelope RequestEnvelope, classification RequestSemanticClassification, cfg RequestSemanticClassifierConfig) RequestEnvelope {
	cfg.Mode = RequestSemanticClassifierModeEnabled
	return applySemanticRequestClassification(envelope, classification, cfg)
}

func sanitizeSemanticRequestEnvelopeCandidate(envelope RequestEnvelope) RequestEnvelope {
	envelope.Normalize()
	envelope.ExternalUserText = ""
	for i := range envelope.Evidence {
		envelope.Evidence[i].Source = compactPromptSection(envelope.Evidence[i].Source, 80)
		envelope.Evidence[i].Signal = compactPromptSection(envelope.Evidence[i].Signal, 80)
		envelope.Evidence[i].Detail = compactPromptSection(envelope.Evidence[i].Detail, 180)
	}
	envelope.Warnings = normalizeTaskStateList(envelope.Warnings, 16)
	envelope.Normalize()
	return envelope
}

func (a *Agent) clearSemanticRequestEnvelopeCandidate() {
	if a == nil || a.Session == nil {
		return
	}
	a.Session.LastSemanticRequestEnvelope = nil
}

func (a *Agent) rememberSemanticRequestEnvelopeCandidate(envelope RequestEnvelope) {
	if a == nil || a.Session == nil {
		return
	}
	candidate := sanitizeSemanticRequestEnvelopeCandidate(envelope)
	a.Session.LastSemanticRequestEnvelope = &candidate
}

func semanticClassificationNarrowsToReadOnly(classification RequestSemanticClassification) bool {
	if semanticBoolTrue(classification.ReadOnlyAnalysis) {
		return true
	}
	if normalizeActionBoundaryValue(classification.ActionBoundary) == ActionBoundaryReadOnly {
		return true
	}
	class := normalizeRequestClassValue(classification.PrimaryClass)
	return class == RequestClassQuestion || class == RequestClassReview || class == RequestClassPlan
}

func semanticClassificationPromotesDocumentAuthoring(classification RequestSemanticClassification) bool {
	classification.Normalize()
	if semanticBoolTrue(classification.DocumentAuthoring) {
		return true
	}
	return normalizeRequestClassValue(classification.PrimaryClass) == RequestClassDocument
}

func requestEnvelopeAllowsSemanticDocumentPromotion(envelope RequestEnvelope) bool {
	envelope.Normalize()
	if requestEnvelopeHasDeterministicMutation(envelope) {
		return false
	}
	if envelope.GoalPromptDraftOnly || envelope.ReviewOnlyModeRequest {
		return false
	}
	if requestHasExplicitNoEditLanguage(envelope.ExternalUserText) {
		return false
	}
	if envelope.ReadOnlyAnalysis && envelope.Confidence >= 0.55 {
		return false
	}
	return true
}

func requestEnvelopeHasDeterministicMutation(envelope RequestEnvelope) bool {
	envelope.Normalize()
	return envelope.ExplicitEditRequest || envelope.ExplicitGitRequest || envelope.DocumentAuthoring || envelope.AllowsFileMutation || envelope.AllowsGitMutation
}

func requestEnvelopeLooksLikeLocalMutation(envelope RequestEnvelope) bool {
	envelope.Normalize()
	return envelope.ExplicitEditRequest || envelope.DocumentAuthoring || envelope.AllowsFileMutation
}

func requestClassImpliesMutation(class RequestClass) bool {
	switch class {
	case RequestClassEdit, RequestClassDocument, RequestClassGit:
		return true
	default:
		return false
	}
}

func semanticBoolTrue(value *bool) bool {
	return value != nil && *value
}

func semanticBoolFalse(value *bool) bool {
	return value != nil && !*value
}

func buildRequestSemanticClassifierSystemPrompt() string {
	return strings.TrimSpace(`You classify user requests for a coding agent. Return exactly one JSON object and no prose.
Classify what the user wants as an outcome, not by keyword translation.
Use primary_class: question, review, plan, edit, document, research, git, or ambiguous.
Use action_boundary: read_only, may_edit, must_edit, may_git, or no_commit.
Set file/git mutation fields only when the user explicitly asks for those actions or for a concrete artifact.
If the user asks to be told, shown, summarized, compared, or explained something without asking for a file, classify it as read-only even if the wording could also mean "organize" or "write" in another context.
If the user asks to create, save, update, or write a document/report/file, classify it as document with may_edit.
Return booleans for read_only_analysis, explicit_edit_request, explicit_git_request, document_authoring, allows_web_research, and requires_fresh_external_info.
Return confidence from 0 to 1 and a short reason.`)
}

func buildRequestSemanticClassifierUserPrompt(envelope RequestEnvelope) string {
	envelope.Normalize()
	baseline := map[string]any{
		"external_user_text":            envelope.ExternalUserText,
		"deterministic_intent":          envelope.Intent,
		"deterministic_primary_class":   envelope.PrimaryClass,
		"deterministic_action_boundary": envelope.Boundary,
		"allows_file_mutation":          envelope.AllowsFileMutation,
		"allows_git_mutation":           envelope.AllowsGitMutation,
		"read_only_analysis":            envelope.ReadOnlyAnalysis,
		"document_authoring":            envelope.DocumentAuthoring,
	}
	data, _ := json.MarshalIndent(baseline, "", "  ")
	return "Classify this request and return JSON only.\n\nBaseline deterministic envelope:\n" + string(data)
}

func (a *Agent) maybeRefineRequestEnvelopeWithSemanticClassifier(ctx context.Context, envelope RequestEnvelope) RequestEnvelope {
	if a == nil {
		return envelope
	}
	a.clearSemanticRequestEnvelopeCandidate()
	cfg := a.Config.RequestRuntime.SemanticClassifier
	normalizeRequestSemanticClassifierConfig(&cfg)
	if cfg.Mode == RequestSemanticClassifierModeDisabled {
		return envelope
	}
	if a.Client == nil {
		envelope.Warnings = append(envelope.Warnings, "semantic classifier enabled but no provider client is configured")
		envelope.Normalize()
		return envelope
	}
	req := ChatRequest{
		Model:           a.Config.Model,
		System:          buildRequestSemanticClassifierSystemPrompt(),
		Messages:        []Message{{Role: "user", Text: buildRequestSemanticClassifierUserPrompt(envelope)}},
		MaxTokens:       requestSemanticClassifierMaxTokens(cfg),
		Temperature:     0,
		TemperatureSet:  true,
		ReasoningEffort: "low",
		ServiceTier:     a.Config.ServiceTier,
		WorkingDir:      a.Workspace.Root,
		JSONMode:        true,
		SessionID:       firstNonBlankString(a.SessionIDForRequest(), ""),
	}
	resp, err := a.Client.Complete(ctx, req)
	if err != nil {
		envelope.Warnings = append(envelope.Warnings, "semantic classifier failed: "+firstNonEmptyLine(err.Error()))
		envelope.Normalize()
		return envelope
	}
	classification, err := parseRequestSemanticClassificationResponse(resp.Message.Text)
	if err != nil {
		envelope.Warnings = append(envelope.Warnings, "semantic classifier returned invalid JSON: "+firstNonEmptyLine(err.Error()))
		envelope.Normalize()
		return envelope
	}
	candidate := semanticRequestClassificationCandidate(envelope, classification, cfg)
	a.rememberSemanticRequestEnvelopeCandidate(candidate)
	if cfg.Mode == RequestSemanticClassifierModeShadow {
		return applySemanticRequestClassification(envelope, classification, cfg)
	}
	// Enabled mode must still accrue calibration: record a baseline-vs-candidate
	// observation each turn so a consistently safe classifier can eventually earn
	// document promotion. A risky widening delta increments SemanticRiskyDiverged
	// and permanently blocks auto-widening, preserving the safety intent. The
	// document promotion under evaluation is itself an intended widening, so it
	// must not be scored as miscalibration evidence against its own gate.
	if !requestSemanticClassifierCandidateIsDocumentPromotion(envelope, candidate, classification) {
		a.accrueSemanticClassifierCalibration(envelope, candidate)
	}
	if !requestSemanticClassifierCandidatePromotionAllowed(envelope, candidate, classification, a.Session) {
		envelope.Warnings = append(envelope.Warnings, "semantic classifier promotion held in shadow until risky delta-free calibration is available")
		envelope.Normalize()
		return envelope
	}
	return candidate
}

// accrueSemanticClassifierCalibration records a baseline-vs-candidate semantic
// observation into the session shadow stats while running in enabled mode.
// Without this, SemanticObserved never advances in enabled mode and document
// promotion stays permanently deadlocked behind requestSemanticClassifierPromotionCalibrated.
func (a *Agent) accrueSemanticClassifierCalibration(baseline RequestEnvelope, candidate RequestEnvelope) {
	if a == nil || a.Session == nil {
		return
	}
	baseline.Normalize()
	candidate.Normalize()
	baselineSummary := semanticCalibrationDecisionSummary("baseline", baseline)
	candidateSummary := semanticCalibrationDecisionSummary("semantic_classifier", candidate)
	comparison := RequestRuntimeShadowComparison{
		GeneratedAt:            time.Now(),
		SessionID:              strings.TrimSpace(a.Session.ID),
		Mode:                   RequestRuntimeModeEnabled,
		EnabledPath:            requestRuntimeClassForEnvelope(baseline),
		SemanticClassifierMode: RequestSemanticClassifierModeEnabled,
		LegacyDecision:         baselineSummary,
		V2Decision:             baselineSummary,
		SemanticDecision:       &candidateSummary,
	}
	comparison.SemanticDifferences = requestRuntimeDecisionDifferences(baselineSummary, candidateSummary)
	comparison.SemanticDeltaLabels = requestRuntimeSemanticDeltaLabels(baselineSummary, candidateSummary)
	a.Session.RequestRuntimeShadowStats = updateRequestRuntimeShadowStats(a.Session.RequestRuntimeShadowStats, comparison)
}

// semanticCalibrationDecisionSummary builds a privilege-aware decision summary
// from an envelope without needing a turn tool plan. The synthesized tool sets
// reflect file/git mutation privilege so tool_exposure_expansion stays
// consistent with mutation_expansion during calibration.
func semanticCalibrationDecisionSummary(source string, envelope RequestEnvelope) RequestRuntimeDecisionSummary {
	envelope.Normalize()
	var exposed []string
	var disabled []string
	if envelope.AllowsFileMutation {
		exposed = append(exposed, "apply_patch", "write_file")
	} else {
		disabled = append(disabled, "apply_patch", "write_file")
	}
	if envelope.AllowsGitMutation {
		exposed = append(exposed, "git_commit", "git_push")
	} else {
		disabled = append(disabled, "git_commit", "git_push")
	}
	summary := RequestRuntimeDecisionSummary{
		Source:        source,
		RequestClass:  requestRuntimeClassForEnvelope(envelope),
		ExposedTools:  exposed,
		DisabledTools: disabled,
	}
	return sanitizeRequestRuntimeDecisionSummary(summary)
}

func requestSemanticClassifierCandidatePromotionAllowed(baseline RequestEnvelope, candidate RequestEnvelope, classification RequestSemanticClassification, session *Session) bool {
	baseline.Normalize()
	candidate.Normalize()
	classification.Normalize()
	if requestSemanticClassifierCandidateIsReadOnlyNarrowing(baseline, candidate) {
		return true
	}
	if !requestEnvelopeHasDeterministicMutation(candidate) {
		return true
	}
	if requestSemanticClassifierCandidateIsDocumentPromotion(baseline, candidate, classification) {
		return requestSemanticClassifierPromotionCalibrated(session)
	}
	return false
}

func requestSemanticClassifierCandidateIsReadOnlyNarrowing(baseline RequestEnvelope, candidate RequestEnvelope) bool {
	baseline.Normalize()
	candidate.Normalize()
	return !requestEnvelopeHasDeterministicMutation(baseline) &&
		candidate.ReadOnlyAnalysis &&
		!candidate.ExplicitEditRequest &&
		!candidate.ExplicitGitRequest &&
		!candidate.DocumentAuthoring &&
		!candidate.AllowsFileMutation &&
		!candidate.AllowsGitMutation
}

func requestSemanticClassifierCandidateIsDocumentPromotion(baseline RequestEnvelope, candidate RequestEnvelope, classification RequestSemanticClassification) bool {
	baseline.Normalize()
	candidate.Normalize()
	classification.Normalize()
	return !baseline.DocumentAuthoring &&
		candidate.DocumentAuthoring &&
		candidate.AllowsFileMutation &&
		semanticClassificationPromotesDocumentAuthoring(classification)
}

func requestSemanticClassifierPromotionCalibrated(session *Session) bool {
	if session == nil || session.RequestRuntimeShadowStats == nil {
		return false
	}
	stats := session.RequestRuntimeShadowStats
	return stats.SemanticObserved >= 3 && stats.SemanticRiskyDiverged == 0
}

func (a *Agent) SessionIDForRequest() string {
	if a == nil || a.Session == nil {
		return ""
	}
	return strings.TrimSpace(a.Session.ID)
}
