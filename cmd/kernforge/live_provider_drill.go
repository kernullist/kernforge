package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	providerFailureClassTimeout                    = "timeout"
	providerFailureClassRateLimit                  = "rate_limit"
	providerFailureClassAuthConfigMissing          = "auth_config_missing"
	providerFailureClassMalformedResponse          = "malformed_response"
	providerFailureClassEmptyResponse              = "empty_response"
	providerFailureClassToolCallMismatch           = "tool_call_mismatch"
	providerFailureClassReviewerMainModelCollision = "reviewer_main_model_collision"
	providerFailureClassRetryExhausted             = "retry_exhausted"

	liveProviderDrillSchemaVersion = "live_provider_drill.v1"
	liveProviderDrillModeScripted  = "scripted"
	liveProviderDrillModeReal      = "real_provider"
	liveProviderDrillStatusPassed  = "passed"
	liveProviderDrillStatusSkipped = "skipped"
	liveProviderDrillStatusFailed  = "failed"
)

type ReviewRouteHealthEvent struct {
	TurnID                     string    `json:"turn_id,omitempty"`
	Role                       string    `json:"role,omitempty"`
	Kind                       string    `json:"kind,omitempty"`
	Provider                   string    `json:"provider,omitempty"`
	ProviderLabel              string    `json:"provider_label,omitempty"`
	ModelID                    string    `json:"model_id,omitempty"`
	ModelLabel                 string    `json:"model_label,omitempty"`
	FailureClass               string    `json:"failure_class,omitempty"`
	Severity                   string    `json:"severity,omitempty"`
	Status                     string    `json:"status,omitempty"`
	LatencyMS                  int64     `json:"latency_ms,omitempty"`
	RetryCount                 int       `json:"retry_count,omitempty"`
	MalformedOutputCount       int       `json:"malformed_output_count,omitempty"`
	EmptyResponseCount         int       `json:"empty_response_count,omitempty"`
	WeakOutputDegraded         bool      `json:"weak_output_degraded,omitempty"`
	DuplicatedOutput           bool      `json:"duplicated_output,omitempty"`
	EvidenceFreeOutput         bool      `json:"evidence_free_output,omitempty"`
	ToolCallMismatch           bool      `json:"tool_call_mismatch,omitempty"`
	ReviewerMainModelCollision bool      `json:"reviewer_main_model_collision,omitempty"`
	EvidenceRef                string    `json:"evidence_ref,omitempty"`
	Recommendation             string    `json:"recommendation,omitempty"`
	NextSafeAction             string    `json:"next_safe_action,omitempty"`
	NextCommand                string    `json:"next_command,omitempty"`
	OccurredAt                 time.Time `json:"occurred_at,omitempty"`
}

type LiveProviderDrillReport struct {
	ID                          string                              `json:"id,omitempty"`
	SchemaVersion               string                              `json:"schema_version,omitempty"`
	Mode                        string                              `json:"mode,omitempty"`
	Status                      string                              `json:"status,omitempty"`
	SkippedReason               string                              `json:"skipped_reason,omitempty"`
	ProviderConfigured          bool                                `json:"provider_configured"`
	StartedAt                   time.Time                           `json:"started_at,omitempty"`
	FinishedAt                  time.Time                           `json:"finished_at,omitempty"`
	WorkflowClasses             []string                            `json:"workflow_classes,omitempty"`
	RouteModes                  []string                            `json:"route_modes,omitempty"`
	Turns                       []LiveProviderDrillTurn             `json:"turns,omitempty"`
	RouteHealthEvents           []ReviewRouteHealthEvent            `json:"route_health_events,omitempty"`
	FinalGateStatus             string                              `json:"final_gate_status,omitempty"`
	ArtifactDir                 string                              `json:"artifact_dir,omitempty"`
	ArtifactPath                string                              `json:"artifact_path,omitempty"`
	StaleContextSummary         *StaleContextSummary                `json:"stale_context_summary,omitempty"`
	FinalAnswerCorrection       *FinalAnswerCorrectionVisibility    `json:"final_answer_correction,omitempty"`
	ProviderRecommendations     []LiveProviderSoakRecommendation    `json:"provider_recommendations,omitempty"`
	ValidationResults           []LiveProviderSoakValidationResult  `json:"validation_results,omitempty"`
	ReloadVerification          *LiveProviderSoakReloadVerification `json:"reload_verification,omitempty"`
	NextRecommendedCommand      string                              `json:"next_recommended_command,omitempty"`
	IndependentReviewDisclosure string                              `json:"independent_review_disclosure,omitempty"`
	Summary                     string                              `json:"summary,omitempty"`
}

type LiveProviderDrillTurn struct {
	TurnID                       string                   `json:"turn_id,omitempty"`
	RequestClass                 string                   `json:"request_class,omitempty"`
	LifecycleKind                string                   `json:"lifecycle_kind,omitempty"`
	RouteMode                    string                   `json:"route_mode,omitempty"`
	RouteDecision                string                   `json:"route_decision,omitempty"`
	Provider                     string                   `json:"provider,omitempty"`
	ProviderLabel                string                   `json:"provider_label,omitempty"`
	ModelID                      string                   `json:"model_id,omitempty"`
	ReviewerProvider             string                   `json:"reviewer_provider,omitempty"`
	ReviewerProviderLabel        string                   `json:"reviewer_provider_label,omitempty"`
	ReviewerModelID              string                   `json:"reviewer_model_id,omitempty"`
	ReviewerSeparated            bool                     `json:"reviewer_separated"`
	ReviewerSeparationStatus     string                   `json:"reviewer_separation_status,omitempty"`
	LatencyMS                    int64                    `json:"latency_ms,omitempty"`
	RetryCount                   int                      `json:"retry_count,omitempty"`
	MalformedOutputCount         int                      `json:"malformed_output_count,omitempty"`
	EmptyResponseCount           int                      `json:"empty_response_count,omitempty"`
	WeakOutputDegraded           bool                     `json:"weak_output_degraded,omitempty"`
	DuplicatedOutput             bool                     `json:"duplicated_output,omitempty"`
	EvidenceFreeOutput           bool                     `json:"evidence_free_output,omitempty"`
	ToolCallMismatch             bool                     `json:"tool_call_mismatch,omitempty"`
	ReviewerMainModelCollision   bool                     `json:"reviewer_main_model_collision,omitempty"`
	StaleReviewerOutput          bool                     `json:"stale_reviewer_output,omitempty"`
	CrossModelReviewerFailure    bool                     `json:"cross_model_reviewer_failure,omitempty"`
	MissingFinalAnswerDisclosure bool                     `json:"missing_final_answer_disclosure,omitempty"`
	IndependentReviewDisclosure  string                   `json:"independent_review_disclosure,omitempty"`
	FinalGateState               string                   `json:"final_gate_state,omitempty"`
	RouteQuality                 string                   `json:"route_quality,omitempty"`
	EvidenceRef                  string                   `json:"evidence_ref,omitempty"`
	NextCommand                  string                   `json:"next_command,omitempty"`
	RouteHealthEvents            []ReviewRouteHealthEvent `json:"route_health_events,omitempty"`
}

type liveProviderDrillWorkflowSpec struct {
	requestClass  string
	lifecycleKind string
}

func (e *ReviewRouteHealthEvent) Normalize() {
	if e == nil {
		return
	}
	e.TurnID = strings.TrimSpace(e.TurnID)
	e.Role = normalizeReviewRole(e.Role)
	e.Kind = strings.TrimSpace(e.Kind)
	e.Provider = normalizeProviderName(e.Provider)
	e.ProviderLabel = strings.TrimSpace(e.ProviderLabel)
	if e.ProviderLabel == "" && e.Provider != "" {
		e.ProviderLabel = reviewProviderDisplayLabel(e.Provider)
	}
	e.ModelID = strings.TrimSpace(e.ModelID)
	e.ModelLabel = strings.TrimSpace(e.ModelLabel)
	e.FailureClass = normalizeProviderFailureClass(e.FailureClass)
	e.Severity = strings.TrimSpace(strings.ToLower(e.Severity))
	if e.Severity == "" {
		e.Severity = staleContextSeverityWarning
	}
	e.Status = strings.TrimSpace(strings.ToLower(e.Status))
	if e.Status == "" {
		e.Status = "degraded"
	}
	if e.LatencyMS < 0 {
		e.LatencyMS = 0
	}
	if e.RetryCount < 0 {
		e.RetryCount = 0
	}
	if e.MalformedOutputCount < 0 {
		e.MalformedOutputCount = 0
	}
	if e.EmptyResponseCount < 0 {
		e.EmptyResponseCount = 0
	}
	e.EvidenceRef = filepath.ToSlash(strings.TrimSpace(e.EvidenceRef))
	if e.FailureClass == providerFailureClassToolCallMismatch {
		e.ToolCallMismatch = true
	}
	if e.FailureClass == providerFailureClassReviewerMainModelCollision {
		e.ReviewerMainModelCollision = true
	}
	if e.FailureClass == providerFailureClassMalformedResponse {
		e.MalformedOutputCount = maxInt(e.MalformedOutputCount, 1)
	}
	if e.FailureClass == providerFailureClassEmptyResponse {
		e.EmptyResponseCount = maxInt(e.EmptyResponseCount, 1)
	}
	if e.FailureClass != "" && e.Recommendation == "" {
		e.Recommendation = reviewRouteHealthRecommendationForClass(e.FailureClass)
	}
	e.Recommendation = strings.TrimSpace(e.Recommendation)
	if e.FailureClass != "" && e.NextSafeAction == "" {
		e.NextSafeAction = reviewRouteHealthNextSafeActionForClass(e.FailureClass)
	}
	if e.FailureClass != "" && e.NextCommand == "" {
		e.NextCommand = reviewRouteHealthNextCommandForClass(e.FailureClass)
	}
	e.NextSafeAction = strings.TrimSpace(e.NextSafeAction)
	e.NextCommand = strings.TrimSpace(e.NextCommand)
}

func (t *LiveProviderDrillTurn) Normalize() {
	if t == nil {
		return
	}
	t.TurnID = strings.TrimSpace(t.TurnID)
	t.RequestClass = normalizeReviewRequestClass(t.RequestClass)
	if t.RequestClass == reviewRequestClassGeneral {
		t.RequestClass = ""
	}
	t.LifecycleKind = normalizeReviewLifecycleKind(t.LifecycleKind)
	t.RouteMode = strings.TrimSpace(t.RouteMode)
	t.RouteDecision = strings.TrimSpace(t.RouteDecision)
	t.Provider = normalizeProviderName(t.Provider)
	t.ProviderLabel = strings.TrimSpace(t.ProviderLabel)
	if t.ProviderLabel == "" && t.Provider != "" {
		t.ProviderLabel = reviewProviderDisplayLabel(t.Provider)
	}
	t.ModelID = strings.TrimSpace(t.ModelID)
	t.ReviewerProvider = normalizeProviderName(t.ReviewerProvider)
	t.ReviewerProviderLabel = strings.TrimSpace(t.ReviewerProviderLabel)
	if t.ReviewerProviderLabel == "" && t.ReviewerProvider != "" {
		t.ReviewerProviderLabel = reviewProviderDisplayLabel(t.ReviewerProvider)
	}
	t.ReviewerModelID = strings.TrimSpace(t.ReviewerModelID)
	t.ReviewerSeparationStatus = strings.TrimSpace(t.ReviewerSeparationStatus)
	if t.ReviewerSeparationStatus == "" {
		if t.ReviewerSeparated {
			t.ReviewerSeparationStatus = "independent"
		} else if t.RouteMode == reviewRouteModeSingleModel {
			t.ReviewerSeparationStatus = "single_model_disclosed"
		}
	}
	if t.LatencyMS < 0 {
		t.LatencyMS = 0
	}
	if t.RetryCount < 0 {
		t.RetryCount = 0
	}
	if t.MalformedOutputCount < 0 {
		t.MalformedOutputCount = 0
	}
	if t.EmptyResponseCount < 0 {
		t.EmptyResponseCount = 0
	}
	t.FinalGateState = strings.TrimSpace(t.FinalGateState)
	t.RouteQuality = strings.TrimSpace(t.RouteQuality)
	t.EvidenceRef = filepath.ToSlash(strings.TrimSpace(t.EvidenceRef))
	t.NextCommand = strings.TrimSpace(t.NextCommand)
	t.IndependentReviewDisclosure = strings.TrimSpace(t.IndependentReviewDisclosure)
	events := make([]ReviewRouteHealthEvent, 0, len(t.RouteHealthEvents))
	for _, event := range t.RouteHealthEvents {
		event.Normalize()
		if event.FailureClass != "" || event.Status != "" {
			events = append(events, event)
		}
	}
	t.RouteHealthEvents = events
}

func (r *LiveProviderDrillReport) Normalize() {
	if r == nil {
		return
	}
	r.ID = strings.TrimSpace(r.ID)
	if r.SchemaVersion == "" {
		r.SchemaVersion = liveProviderDrillSchemaVersion
	}
	r.Mode = strings.TrimSpace(strings.ToLower(r.Mode))
	if r.Mode == "" {
		r.Mode = liveProviderDrillModeScripted
	}
	r.Status = strings.TrimSpace(strings.ToLower(r.Status))
	if r.Status == "" {
		r.Status = liveProviderDrillStatusPassed
	}
	r.SkippedReason = strings.TrimSpace(r.SkippedReason)
	r.WorkflowClasses = normalizeTaskStateList(r.WorkflowClasses, 16)
	r.RouteModes = normalizeTaskStateList(r.RouteModes, 8)
	turns := make([]LiveProviderDrillTurn, 0, len(r.Turns))
	for _, turn := range r.Turns {
		turn.Normalize()
		if turn.TurnID == "" {
			turn.TurnID = fmt.Sprintf("turn-%02d", len(turns)+1)
		}
		turns = append(turns, turn)
	}
	r.Turns = turns
	events := make([]ReviewRouteHealthEvent, 0, len(r.RouteHealthEvents))
	for _, event := range r.RouteHealthEvents {
		event.Normalize()
		if event.FailureClass != "" || event.Status != "" {
			events = append(events, event)
		}
	}
	for _, turn := range r.Turns {
		for _, event := range turn.RouteHealthEvents {
			event.Normalize()
			if event.FailureClass != "" || event.Status != "" {
				events = append(events, event)
			}
		}
	}
	r.RouteHealthEvents = dedupeReviewRouteHealthEvents(events, 32)
	r.FinalGateStatus = strings.TrimSpace(r.FinalGateStatus)
	r.ArtifactDir = filepath.ToSlash(strings.TrimSpace(r.ArtifactDir))
	r.ArtifactPath = filepath.ToSlash(strings.TrimSpace(r.ArtifactPath))
	if r.StaleContextSummary != nil {
		r.StaleContextSummary.Normalize()
	}
	if r.FinalAnswerCorrection != nil {
		r.FinalAnswerCorrection.Normalize()
	}
	r.ProviderRecommendations = normalizeLiveProviderSoakRecommendations(r.ProviderRecommendations)
	r.ValidationResults = normalizeLiveProviderSoakValidationResults(r.ValidationResults)
	if r.ReloadVerification != nil {
		r.ReloadVerification.Normalize()
	}
	r.NextRecommendedCommand = strings.TrimSpace(r.NextRecommendedCommand)
	r.IndependentReviewDisclosure = strings.TrimSpace(r.IndependentReviewDisclosure)
	r.Summary = strings.TrimSpace(r.Summary)
}

func normalizeProviderFailureClass(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "-", "_")))
	switch value {
	case providerFailureClassTimeout, "deadline_exceeded":
		return providerFailureClassTimeout
	case providerFailureClassRateLimit, "rate_limited", "too_many_requests":
		return providerFailureClassRateLimit
	case providerFailureClassAuthConfigMissing, "auth_missing", "config_missing", "unauthorized", "forbidden":
		return providerFailureClassAuthConfigMissing
	case providerFailureClassMalformedResponse, "malformed", "schema_invalid", "invalid_response", "truncated":
		return providerFailureClassMalformedResponse
	case providerFailureClassEmptyResponse, "empty", "empty_content":
		return providerFailureClassEmptyResponse
	case providerFailureClassToolCallMismatch, "tool_mismatch", "tool_role_mismatch":
		return providerFailureClassToolCallMismatch
	case providerFailureClassReviewerMainModelCollision, "model_collision", "reviewer_collision":
		return providerFailureClassReviewerMainModelCollision
	case providerFailureClassRetryExhausted, "retry_failed", "retries_exhausted":
		return providerFailureClassRetryExhausted
	default:
		return value
	}
}

func reviewRouteHealthRecommendationForClass(class string) string {
	switch normalizeProviderFailureClass(class) {
	case providerFailureClassTimeout:
		return "increase the reviewer timeout only after confirming the route is still useful, or switch to a closer/faster model route"
	case providerFailureClassRateLimit:
		return "wait for the provider quota window or move review traffic to another configured route"
	case providerFailureClassAuthConfigMissing:
		return "fix provider credentials/configuration before rerunning the live-provider drill"
	case providerFailureClassMalformedResponse:
		return "treat the output as degraded evidence and rerun with a stricter or more reliable reviewer route"
	case providerFailureClassEmptyResponse:
		return "retry with compact evidence or switch away from routes that repeatedly return empty content"
	case providerFailureClassToolCallMismatch:
		return "repair provider transcript/tool-call ordering before replaying the request"
	case providerFailureClassReviewerMainModelCollision:
		return "configure an independent reviewer route or explicitly disclose single-model mode"
	case providerFailureClassRetryExhausted:
		return "stop automatic retries and require operator action before another provider call"
	default:
		return "inspect provider route health before treating reviewer evidence as approval"
	}
}

func reviewRouteHealthNextSafeActionForClass(class string) string {
	switch normalizeProviderFailureClass(class) {
	case providerFailureClassTimeout:
		return "keep the current result out of final approval and rerun the soak with a bounded timeout or a faster route"
	case providerFailureClassRateLimit:
		return "wait for quota recovery or switch to a configured route with available capacity"
	case providerFailureClassAuthConfigMissing:
		return "fix provider credentials before attempting another real-provider run"
	case providerFailureClassMalformedResponse:
		return "discard the reviewer output as approval evidence and rerun with stricter output requirements"
	case providerFailureClassEmptyResponse:
		return "retry with compact evidence or switch away from the empty-response route"
	case providerFailureClassToolCallMismatch:
		return "repair the provider transcript/tool-call order before replaying the request"
	case providerFailureClassReviewerMainModelCollision:
		return "configure an independent reviewer route or explicitly run single-model mode with disclosure"
	case providerFailureClassRetryExhausted:
		return "stop automatic retry and require an operator decision before another provider call"
	default:
		return "inspect route health before finalizing"
	}
}

func reviewRouteHealthNextCommandForClass(class string) string {
	switch normalizeProviderFailureClass(class) {
	case providerFailureClassAuthConfigMissing:
		return "/config"
	case providerFailureClassReviewerMainModelCollision:
		return "/model cross-review"
	default:
		return "/review-soak --mode scripted"
	}
}

func reviewRouteHealthFailureClassFromText(status string, quality string, errText string) string {
	text := strings.ToLower(strings.Join([]string{status, quality, errText}, " "))
	switch {
	case containsAny(text, "deadline exceeded", "timed out", "timeout"):
		return providerFailureClassTimeout
	case containsAny(text, "rate limit", "rate_limit", "too many requests", "429"):
		return providerFailureClassRateLimit
	case containsAny(text, "unauthorized", "forbidden", "api key", "credential", "credentials", "auth", "no reviewer model configured", "missing config", "unsupported provider"):
		return providerFailureClassAuthConfigMissing
	case containsAny(text, "role 'tool'", "tool_calls", "tool call", "tool-call", "tool role"):
		return providerFailureClassToolCallMismatch
	case containsAny(text, "empty response", "empty choices", "empty content", "empty review response"):
		return providerFailureClassEmptyResponse
	case containsAny(text, "retry exhausted", "retries exhausted", "repeated", "retry failed"):
		return providerFailureClassRetryExhausted
	case containsAny(text, "malformed", "schema", "parse", "invalid", "truncated", "omitted", "omission", "weak structured", "weak output"):
		return providerFailureClassMalformedResponse
	default:
		if strings.EqualFold(strings.TrimSpace(quality), reviewModelQualityWeak) {
			return providerFailureClassMalformedResponse
		}
		if strings.EqualFold(strings.TrimSpace(quality), reviewModelQualityFailed) {
			return providerFailureClassRetryExhausted
		}
		return ""
	}
}

func reviewRouteHealthEventFromReviewerRun(reviewerRun ReviewReviewerRun) (ReviewRouteHealthEvent, bool) {
	class := normalizeProviderFailureClass(reviewerRun.FailureClass)
	if class == "" {
		class = reviewRouteHealthFailureClassFromText(reviewerRun.Status, reviewerRun.ModelQuality, reviewerRun.Error)
	}
	degraded := class != "" ||
		strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityWeak) ||
		strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityFailed) ||
		reviewerRun.MalformedOutputCount > 0 ||
		reviewerRun.WeakOutputDegraded ||
		reviewerRun.DuplicatedOutput ||
		reviewerRun.EvidenceFreeOutput ||
		reviewerRun.ToolCallMismatch ||
		reviewerRun.ReviewerMainModelCollision
	if !degraded {
		return ReviewRouteHealthEvent{}, false
	}
	event := ReviewRouteHealthEvent{
		Role:                       reviewerRun.Role,
		Kind:                       reviewerRun.Kind,
		Provider:                   reviewerRun.Provider,
		ProviderLabel:              reviewerRun.ProviderLabel,
		ModelID:                    reviewerRun.ModelID,
		ModelLabel:                 reviewerRun.Model,
		FailureClass:               class,
		Severity:                   staleContextSeverityWarning,
		Status:                     firstNonBlankString(reviewerRun.Status, "degraded"),
		LatencyMS:                  reviewerRun.LatencyMS,
		RetryCount:                 reviewerRun.RetryCount,
		MalformedOutputCount:       reviewerRun.MalformedOutputCount,
		WeakOutputDegraded:         reviewerRun.WeakOutputDegraded || strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityWeak),
		DuplicatedOutput:           reviewerRun.DuplicatedOutput,
		EvidenceFreeOutput:         reviewerRun.EvidenceFreeOutput,
		ToolCallMismatch:           reviewerRun.ToolCallMismatch,
		ReviewerMainModelCollision: reviewerRun.ReviewerMainModelCollision,
		EvidenceRef:                firstNonBlankString(reviewerRun.RawOutputPath, reviewerRun.RawProviderResponsePath, reviewerRun.PromptPath),
		Recommendation:             reviewerRun.FailureRecommendation,
		OccurredAt:                 reviewerRun.FinishedAt,
	}
	if strings.EqualFold(strings.TrimSpace(reviewerRun.ModelQuality), reviewModelQualityFailed) || strings.EqualFold(strings.TrimSpace(reviewerRun.Status), "failed") {
		event.Severity = staleContextSeverityBlocker
	}
	if event.FailureClass == "" && event.WeakOutputDegraded {
		event.FailureClass = providerFailureClassMalformedResponse
	}
	if event.FailureClass == "" && event.DuplicatedOutput {
		event.FailureClass = providerFailureClassReviewerMainModelCollision
	}
	event.Normalize()
	return event, true
}

func reviewReviewerRunProviderModel(cfg Config, role string, label string, model string) (string, string, string) {
	provider, labelModel := reviewProviderModelFromDisplayLabel(label)
	if provider == "" {
		provider = reviewRoleProviderForRun(cfg, role)
	}
	modelID := firstNonBlankString(model, labelModel)
	provider = normalizeProviderName(provider)
	providerLabel := reviewProviderDisplayLabel(provider)
	if providerLabel == "" {
		providerLabel = provider
	}
	return provider, providerLabel, strings.TrimSpace(modelID)
}

func finalizeReviewReviewerRunTelemetry(run *ReviewReviewerRun) {
	if run == nil {
		return
	}
	run.Role = normalizeReviewRole(run.Role)
	run.Provider = normalizeProviderName(run.Provider)
	run.ProviderLabel = strings.TrimSpace(run.ProviderLabel)
	if run.ProviderLabel == "" && run.Provider != "" {
		run.ProviderLabel = reviewProviderDisplayLabel(run.Provider)
	}
	run.ModelID = strings.TrimSpace(run.ModelID)
	run.FailureClass = normalizeProviderFailureClass(run.FailureClass)
	if !run.StartedAt.IsZero() && !run.FinishedAt.IsZero() {
		run.LatencyMS = run.FinishedAt.Sub(run.StartedAt).Milliseconds()
	}
	if run.LatencyMS < 0 {
		run.LatencyMS = 0
	}
	if run.RetryCount < 0 {
		run.RetryCount = 0
	}
	if run.MalformedOutputCount < 0 {
		run.MalformedOutputCount = 0
	}
	if run.FailureClass == "" {
		run.FailureClass = reviewRouteHealthFailureClassFromText(run.Status, run.ModelQuality, run.Error)
	}
	if strings.EqualFold(strings.TrimSpace(run.ModelQuality), reviewModelQualityWeak) {
		run.WeakOutputDegraded = true
		if run.FailureClass == "" {
			run.FailureClass = providerFailureClassMalformedResponse
		}
	}
	if run.ToolCallMismatch {
		run.FailureClass = providerFailureClassToolCallMismatch
	}
	if run.ReviewerMainModelCollision {
		run.FailureClass = providerFailureClassReviewerMainModelCollision
	}
	if run.FailureClass != "" && run.FailureRecommendation == "" {
		run.FailureRecommendation = reviewRouteHealthRecommendationForClass(run.FailureClass)
	}
	run.RawOutputPath = filepath.ToSlash(strings.TrimSpace(run.RawOutputPath))
	run.RawProviderResponsePath = filepath.ToSlash(strings.TrimSpace(run.RawProviderResponsePath))
	run.PromptPath = filepath.ToSlash(strings.TrimSpace(run.PromptPath))
}

func reviewRouteHealthEventsFromRun(run *ReviewRun) []ReviewRouteHealthEvent {
	if run == nil {
		return nil
	}
	events := append([]ReviewRouteHealthEvent(nil), run.RouteHealthEvents...)
	events = append(events, run.ModelPlan.RouteHealthEvents...)
	for _, reviewerRun := range run.ReviewerRuns {
		if event, ok := reviewRouteHealthEventFromReviewerRun(reviewerRun); ok {
			events = append(events, event)
		}
	}
	events = append(events, reviewReviewerCollisionEvents(*run)...)
	if run.LiveProviderDrill != nil {
		copyReport := *run.LiveProviderDrill
		copyReport.Normalize()
		events = append(events, copyReport.RouteHealthEvents...)
	}
	return dedupeReviewRouteHealthEvents(events, 32)
}

func reviewReviewerCollisionEvents(run ReviewRun) []ReviewRouteHealthEvent {
	primaryProvider, primaryModel := "", ""
	for _, reviewerRun := range run.ReviewerRuns {
		if normalizeReviewRole(reviewerRun.Role) != "primary_reviewer" {
			continue
		}
		primaryProvider = firstNonBlankString(reviewerRun.Provider, providerFromModelLabel(reviewerRun.Model))
		primaryModel = firstNonBlankString(reviewerRun.ModelID, modelFromModelLabel(reviewerRun.Model), reviewerRun.Model)
		break
	}
	if strings.TrimSpace(primaryProvider) == "" && len(run.ModelPlan.AssignedModels) > 0 {
		primaryProvider = providerFromModelLabel(run.ModelPlan.AssignedModels["primary_reviewer"])
		primaryModel = modelFromModelLabel(run.ModelPlan.AssignedModels["primary_reviewer"])
	}
	if primaryProvider == "" && primaryModel == "" {
		return nil
	}
	var events []ReviewRouteHealthEvent
	for _, reviewerRun := range run.ReviewerRuns {
		role := normalizeReviewRole(reviewerRun.Role)
		if role != "cross_reviewer" {
			continue
		}
		crossProvider := firstNonBlankString(reviewerRun.Provider, providerFromModelLabel(reviewerRun.Model))
		crossModel := firstNonBlankString(reviewerRun.ModelID, modelFromModelLabel(reviewerRun.Model), reviewerRun.Model)
		if !reviewProviderModelCollides(primaryProvider, primaryModel, crossProvider, crossModel) {
			continue
		}
		event := ReviewRouteHealthEvent{
			Role:                       role,
			Kind:                       reviewerRun.Kind,
			Provider:                   crossProvider,
			ProviderLabel:              reviewerRun.ProviderLabel,
			ModelID:                    crossModel,
			ModelLabel:                 reviewerRun.Model,
			FailureClass:               providerFailureClassReviewerMainModelCollision,
			Severity:                   staleContextSeverityWarning,
			Status:                     "degraded",
			LatencyMS:                  reviewerRun.LatencyMS,
			ReviewerMainModelCollision: true,
			EvidenceRef:                firstNonBlankString(reviewerRun.RawOutputPath, "reviewer_runs"),
			OccurredAt:                 reviewerRun.FinishedAt,
		}
		event.Normalize()
		events = append(events, event)
	}
	return events
}

func reviewProviderModelCollides(aProvider string, aModel string, bProvider string, bModel string) bool {
	aProvider = normalizeProviderName(aProvider)
	bProvider = normalizeProviderName(bProvider)
	aModel = strings.ToLower(strings.TrimSpace(aModel))
	bModel = strings.ToLower(strings.TrimSpace(bModel))
	if aProvider == "" || bProvider == "" || aModel == "" || bModel == "" {
		return false
	}
	return aProvider == bProvider && aModel == bModel
}

func providerFromModelLabel(label string) string {
	provider, _ := reviewProviderModelFromDisplayLabel(label)
	return provider
}

func modelFromModelLabel(label string) string {
	_, model := reviewProviderModelFromDisplayLabel(label)
	return model
}

func dedupeReviewRouteHealthEvents(items []ReviewRouteHealthEvent, limit int) []ReviewRouteHealthEvent {
	out := make([]ReviewRouteHealthEvent, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item.Normalize()
		if item.FailureClass == "" && item.Status == "" {
			continue
		}
		key := strings.Join([]string{item.TurnID, item.Role, item.Kind, item.Provider, item.ModelID, item.FailureClass, item.EvidenceRef}, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func reviewRouteHealthEventClasses(items []ReviewRouteHealthEvent) []string {
	classes := make([]string, 0, len(items))
	for _, item := range items {
		item.Normalize()
		if item.FailureClass != "" {
			classes = append(classes, item.FailureClass)
		}
	}
	return analysisUniqueStrings(classes)
}

func liveProviderDrillStatusLine(report *LiveProviderDrillReport) string {
	if report == nil {
		return "none"
	}
	copyReport := *report
	copyReport.Normalize()
	parts := []string{
		"mode=" + copyReport.Mode,
		"status=" + copyReport.Status,
		fmt.Sprintf("turns=%d", len(copyReport.Turns)),
	}
	if len(copyReport.RouteModes) > 0 {
		parts = append(parts, "routes="+strings.Join(copyReport.RouteModes, ","))
	}
	if len(copyReport.RouteHealthEvents) > 0 {
		parts = append(parts, "route_health_events="+strings.Join(reviewRouteHealthEventClasses(copyReport.RouteHealthEvents), ","))
	}
	if copyReport.FinalGateStatus != "" {
		parts = append(parts, "final_gate="+copyReport.FinalGateStatus)
	}
	if copyReport.SkippedReason != "" {
		parts = append(parts, "skipped_reason="+copyReport.SkippedReason)
	}
	return strings.Join(parts, " ")
}

func renderLiveProviderDrillMarkdown(report *LiveProviderDrillReport) string {
	if report == nil {
		return ""
	}
	copyReport := *report
	copyReport.Normalize()
	var b strings.Builder
	b.WriteString("## Live Provider Drill\n\n")
	fmt.Fprintf(&b, "- live_provider_drill: `%s`\n", liveProviderDrillStatusLine(&copyReport))
	fmt.Fprintf(&b, "- status: `%s`\n", copyReport.Status)
	fmt.Fprintf(&b, "- mode: `%s`\n", copyReport.Mode)
	fmt.Fprintf(&b, "- provider_configured: `%t`\n", copyReport.ProviderConfigured)
	if copyReport.SkippedReason != "" {
		fmt.Fprintf(&b, "- skipped_reason: %s\n", copyReport.SkippedReason)
	}
	if len(copyReport.WorkflowClasses) > 0 {
		fmt.Fprintf(&b, "- workflow_classes: `%s`\n", strings.Join(copyReport.WorkflowClasses, "`, `"))
	}
	if len(copyReport.RouteModes) > 0 {
		fmt.Fprintf(&b, "- route_modes: `%s`\n", strings.Join(copyReport.RouteModes, "`, `"))
	}
	if copyReport.FinalGateStatus != "" {
		fmt.Fprintf(&b, "- final_gate_status: `%s`\n", copyReport.FinalGateStatus)
	}
	if copyReport.IndependentReviewDisclosure != "" {
		fmt.Fprintf(&b, "- independent_review_disclosure: %s\n", copyReport.IndependentReviewDisclosure)
	}
	if copyReport.NextRecommendedCommand != "" {
		fmt.Fprintf(&b, "- next_recommended_command: `%s`\n", copyReport.NextRecommendedCommand)
	}
	if copyReport.Summary != "" {
		fmt.Fprintf(&b, "- summary: %s\n", copyReport.Summary)
	}
	if copyReport.FinalAnswerCorrection != nil {
		fmt.Fprintf(&b, "- final_answer_correction: `%s`\n", finalAnswerCorrectionStatusLine(copyReport.FinalAnswerCorrection))
	}
	if copyReport.StaleContextSummary != nil {
		fmt.Fprintf(&b, "- stale_context_summary: `%s`\n", staleContextSummaryStatusLine(copyReport.StaleContextSummary))
	}
	for _, turn := range copyReport.Turns {
		fmt.Fprintf(&b, "- `%s` class=`%s` kind=`%s` route=`%s` quality=`%s` gate=`%s` latency_ms=`%d` retries=`%d` malformed=`%d` empty_response=`%d` reviewer_separated=`%t` reviewer_separation=`%s` weak=`%t` duplicated=`%t` evidence_free=`%t` tool_call_mismatch=`%t` collision=`%t`\n",
			turn.TurnID,
			turn.RequestClass,
			turn.LifecycleKind,
			turn.RouteMode,
			turn.RouteQuality,
			turn.FinalGateState,
			turn.LatencyMS,
			turn.RetryCount,
			turn.MalformedOutputCount,
			turn.EmptyResponseCount,
			turn.ReviewerSeparated,
			valueOrDefault(turn.ReviewerSeparationStatus, fmt.Sprintf("%t", turn.ReviewerSeparated)),
			turn.WeakOutputDegraded,
			turn.DuplicatedOutput,
			turn.EvidenceFreeOutput,
			turn.ToolCallMismatch,
			turn.ReviewerMainModelCollision,
		)
	}
	if len(copyReport.RouteHealthEvents) > 0 {
		b.WriteString("\n### Route Health Events\n\n")
		for _, event := range copyReport.RouteHealthEvents {
			fmt.Fprintf(&b, "- `%s` role=`%s` class=`%s` status=`%s` provider=`%s` model=`%s` retry_count=`%d` malformed=`%d` empty_response=`%d` recommendation=%s next_command=`%s`\n",
				firstNonBlankString(event.TurnID, "turn"),
				event.Role,
				event.FailureClass,
				event.Status,
				firstNonBlankString(event.ProviderLabel, event.Provider),
				firstNonBlankString(event.ModelID, event.ModelLabel),
				event.RetryCount,
				event.MalformedOutputCount,
				event.EmptyResponseCount,
				event.Recommendation,
				event.NextCommand,
			)
		}
	}
	if len(copyReport.ProviderRecommendations) > 0 {
		b.WriteString("\n### Provider Recommendations\n\n")
		for _, item := range copyReport.ProviderRecommendations {
			fmt.Fprintf(&b, "- class=`%s` severity=`%s` evidence=`%s` next=`%s` recommendation=%s\n",
				item.FailureClass,
				item.Severity,
				item.EvidenceRef,
				item.NextCommand,
				item.Recommendation,
			)
		}
	}
	if len(copyReport.ValidationResults) > 0 {
		b.WriteString("\n### Validation Results\n\n")
		for _, item := range copyReport.ValidationResults {
			fmt.Fprintf(&b, "- command=`%s` status=`%s` evidence=`%s` summary=%s\n",
				item.Command,
				item.Status,
				item.EvidenceRef,
				item.Summary,
			)
		}
	}
	b.WriteString("\n")
	return b.String()
}

func buildScriptedLiveProviderDrillReport(now time.Time) LiveProviderDrillReport {
	workflows := liveProviderDrillWorkflowSpecs()
	report := LiveProviderDrillReport{
		ID:                          "live-provider-drill-scripted",
		SchemaVersion:               liveProviderDrillSchemaVersion,
		Mode:                        liveProviderDrillModeScripted,
		Status:                      liveProviderDrillStatusPassed,
		ProviderConfigured:          true,
		StartedAt:                   now,
		FinishedAt:                  now.Add(time.Second),
		RouteModes:                  []string{reviewRouteModeSingleModel, reviewRouteModeCrossModel},
		FinalGateStatus:             runtimeGateStatusReady,
		IndependentReviewDisclosure: "scripted soak includes independent cross-model turns and disclosed single-model turns",
		NextRecommendedCommand:      "/status detail",
		Summary:                     "scripted live-provider drill covered required workflow classes in single-model and cross-model route modes",
	}
	turnIndex := 0
	for _, workflow := range workflows {
		report.WorkflowClasses = append(report.WorkflowClasses, workflow.lifecycleKind)
		for _, routeMode := range report.RouteModes {
			turnIndex++
			turn := LiveProviderDrillTurn{
				TurnID:                      fmt.Sprintf("turn-%02d", turnIndex),
				RequestClass:                workflow.requestClass,
				LifecycleKind:               workflow.lifecycleKind,
				RouteMode:                   routeMode,
				RouteDecision:               "scripted_route_decision",
				Provider:                    "openai-codex",
				ProviderLabel:               reviewProviderDisplayLabel("openai-codex"),
				ModelID:                     "scripted-main-model",
				LatencyMS:                   int64(100 + turnIndex),
				FinalGateState:              runtimeGateStatusReady,
				RouteQuality:                "healthy",
				EvidenceRef:                 "scripted_drill_fixture",
				NextCommand:                 "/status detail",
				ReviewerSeparated:           routeMode == reviewRouteModeCrossModel,
				ReviewerSeparationStatus:    "single_model_disclosed",
				IndependentReviewDisclosure: "no independent reviewer evidence was available for this single-model fixture turn",
			}
			if routeMode == reviewRouteModeCrossModel {
				turn.ReviewerProvider = "anthropic"
				turn.ReviewerProviderLabel = reviewProviderDisplayLabel("anthropic")
				turn.ReviewerModelID = "scripted-cross-model"
				turn.ReviewerSeparationStatus = "independent"
				turn.IndependentReviewDisclosure = "independent reviewer evidence was available for this scripted cross-model turn"
			}
			report.Turns = append(report.Turns, turn)
		}
	}
	report.Normalize()
	return report
}

func buildRealProviderSkippedDrillReport(reason string) LiveProviderDrillReport {
	report := LiveProviderDrillReport{
		ID:                          "live-provider-drill-real-skipped",
		SchemaVersion:               liveProviderDrillSchemaVersion,
		Mode:                        liveProviderDrillModeReal,
		Status:                      liveProviderDrillStatusSkipped,
		SkippedReason:               firstNonBlankString(reason, "real provider smoke is not configured"),
		WorkflowClasses:             liveProviderDrillWorkflowClasses(),
		RouteModes:                  []string{reviewRouteModeSingleModel, reviewRouteModeCrossModel},
		FinalGateStatus:             "skipped",
		NextRecommendedCommand:      "/review-soak --mode scripted",
		IndependentReviewDisclosure: "real-provider soak was skipped before reviewer separation could be evaluated",
		Summary:                     "real-provider drill skipped because provider credentials or model config were unavailable",
	}
	report.Normalize()
	return report
}

func runOptionalRealProviderSmokeDrill(ctx context.Context, root string, cfg Config) (LiveProviderDrillReport, error) {
	cfg.Provider = normalizeProviderName(cfg.Provider)
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.Provider == "" || cfg.Model == "" {
		report := buildRealProviderSkippedDrillReport("provider/model config is unavailable")
		return report, nil
	}
	client, err := NewProviderClient(cfg)
	if err != nil {
		report := buildRealProviderSkippedDrillReport("provider config unavailable: " + err.Error())
		return report, nil
	}
	started := time.Now()
	report := LiveProviderDrillReport{
		ID:                 "live-provider-drill-real-smoke",
		SchemaVersion:      liveProviderDrillSchemaVersion,
		Mode:               liveProviderDrillModeReal,
		Status:             liveProviderDrillStatusPassed,
		ProviderConfigured: true,
		StartedAt:          started,
		WorkflowClasses:    liveProviderDrillWorkflowClasses(),
		RouteModes:         []string{reviewRouteModeSingleModel, reviewRouteModeCrossModel},
		FinalGateStatus:    runtimeGateStatusReady,
		Summary:            "real-provider smoke completed and produced provider-backed route evidence",
	}
	turnStart := time.Now()
	resp, err := client.Complete(ctx, ChatRequest{
		Model: cfg.Model,
		Messages: []Message{{
			Role: "user",
			Text: "Reply with exactly: REVIEW_RESULT\nverdict: approved\nsummary: provider smoke ok",
		}},
		MaxTokens:  128,
		WorkingDir: root,
	})
	latencyMS := time.Since(turnStart).Milliseconds()
	turn := LiveProviderDrillTurn{
		TurnID:            "real-provider-smoke-01",
		RequestClass:      reviewRequestClassReviewOnly,
		LifecycleKind:     reviewLifecycleKindAnalysis,
		RouteMode:         reviewRouteModeSingleModel,
		RouteDecision:     "real_provider_smoke",
		Provider:          cfg.Provider,
		ProviderLabel:     reviewProviderDisplayLabel(cfg.Provider),
		ModelID:           cfg.Model,
		LatencyMS:         latencyMS,
		FinalGateState:    runtimeGateStatusReady,
		RouteQuality:      "healthy",
		EvidenceRef:       "real_provider_smoke",
		NextCommand:       "/status detail",
		ReviewerSeparated: false,
	}
	if err != nil {
		event := ReviewRouteHealthEvent{
			TurnID:        turn.TurnID,
			Role:          "primary_reviewer",
			Kind:          "main",
			Provider:      cfg.Provider,
			ProviderLabel: turn.ProviderLabel,
			ModelID:       cfg.Model,
			FailureClass:  reviewRouteHealthFailureClassFromText("failed", reviewModelQualityFailed, err.Error()),
			Severity:      "blocker",
			Status:        "failed",
			LatencyMS:     latencyMS,
			EvidenceRef:   "real_provider_smoke",
		}
		event.Normalize()
		turn.RouteQuality = "degraded"
		turn.FinalGateState = runtimeGateStatusBlocked
		turn.WeakOutputDegraded = true
		turn.RouteHealthEvents = []ReviewRouteHealthEvent{event}
		report.RouteHealthEvents = append(report.RouteHealthEvents, event)
		report.Status = liveProviderDrillStatusFailed
		report.FinalGateStatus = runtimeGateStatusBlocked
		report.Summary = "real-provider smoke failed: " + err.Error()
		report.Turns = []LiveProviderDrillTurn{turn}
		report.FinishedAt = time.Now()
		report.Normalize()
		return report, err
	}
	if !strings.Contains(strings.ToLower(resp.Message.Text), "provider smoke ok") {
		event := ReviewRouteHealthEvent{
			TurnID:        turn.TurnID,
			Role:          "primary_reviewer",
			Kind:          "main",
			Provider:      cfg.Provider,
			ProviderLabel: turn.ProviderLabel,
			ModelID:       cfg.Model,
			FailureClass:  providerFailureClassMalformedResponse,
			Severity:      "blocker",
			Status:        "failed",
			LatencyMS:     latencyMS,
			EvidenceRef:   "real_provider_smoke",
		}
		event.Normalize()
		turn.RouteQuality = "degraded"
		turn.FinalGateState = runtimeGateStatusBlocked
		turn.WeakOutputDegraded = true
		turn.MalformedOutputCount = 1
		turn.RouteHealthEvents = []ReviewRouteHealthEvent{event}
		report.RouteHealthEvents = append(report.RouteHealthEvents, event)
		report.Status = liveProviderDrillStatusFailed
		report.FinalGateStatus = runtimeGateStatusBlocked
		report.Summary = "real-provider smoke returned malformed output"
		report.Turns = []LiveProviderDrillTurn{turn}
		report.FinishedAt = time.Now()
		report.Normalize()
		return report, fmt.Errorf("unexpected real-provider smoke response: %q", resp.Message.Text)
	}
	report.Turns = []LiveProviderDrillTurn{turn}
	report.FinishedAt = time.Now()
	report.Normalize()
	return report, nil
}

func liveProviderDrillWorkflowSpecs() []liveProviderDrillWorkflowSpec {
	return []liveProviderDrillWorkflowSpec{
		{reviewRequestClassDocumentArtifact, reviewLifecycleKindDocumentArtifact},
		{reviewRequestClassReviewOnly, reviewLifecycleKindReviewOnly},
		{reviewRequestClassModifyThenReview, reviewLifecycleKindImplementation},
		{reviewRequestClassModifyThenReview, reviewLifecycleKindModifyThenReview},
		{reviewRequestClassReviewThenModify, reviewLifecycleKindFixFromReview},
		{reviewRequestClassReviewOnly, reviewLifecycleKindAnalysis},
		{reviewRequestClassModifyThenReview, reviewLifecycleKindMixedFlow},
	}
}

func liveProviderDrillWorkflowClasses() []string {
	classes := []string{}
	for _, workflow := range liveProviderDrillWorkflowSpecs() {
		classes = append(classes, workflow.lifecycleKind)
	}
	return normalizeTaskStateList(classes, 16)
}
