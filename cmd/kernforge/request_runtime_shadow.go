package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	RequestRuntimeModeDisabled = "disabled"
	RequestRuntimeModeShadow   = "shadow"
	RequestRuntimeModeEnabled  = "enabled"

	RequestRuntimeClassReviewOnly        = "review_only"
	RequestRuntimeClassPlanOnly          = "plan_only"
	RequestRuntimeClassDocumentAuthoring = "document_authoring"
	RequestRuntimeClassExplicitEdit      = "explicit_edit"
	RequestRuntimeClassGit               = "git"
	RequestRuntimeClassResearch          = "research"
	RequestRuntimeClassDefault           = "default"
	RequestRuntimeClassAll               = "all"

	requestRuntimeShadowDirName          = "request_runtime_shadow"
	requestRuntimeShadowMaxRecentSamples = 12
)

type RequestRuntimeConfig struct {
	Mode               string                          `json:"mode,omitempty"`
	EnabledClasses     []string                        `json:"enabled_classes,omitempty"`
	LogDir             string                          `json:"log_dir,omitempty"`
	SemanticClassifier RequestSemanticClassifierConfig `json:"semantic_classifier,omitempty"`
}

type RequestRuntimeDecisionSummary struct {
	Source            string   `json:"source,omitempty"`
	RequestClass      string   `json:"request_class,omitempty"`
	ExposedTools      []string `json:"exposed_tools,omitempty"`
	DisabledTools     []string `json:"disabled_tools,omitempty"`
	InterventionKinds []string `json:"intervention_kinds,omitempty"`
	FinalGateState    string   `json:"final_gate_state,omitempty"`
	FinalGateReady    bool     `json:"final_gate_ready"`
}

type RequestRuntimeShadowComparison struct {
	GeneratedAt            time.Time                      `json:"generated_at,omitempty"`
	SessionID              string                         `json:"session_id,omitempty"`
	Mode                   string                         `json:"mode,omitempty"`
	EnabledPath            string                         `json:"enabled_path,omitempty"`
	SemanticClassifierMode string                         `json:"semantic_classifier_mode,omitempty"`
	Diverged               bool                           `json:"diverged"`
	Differences            []string                       `json:"differences,omitempty"`
	LegacyDecision         RequestRuntimeDecisionSummary  `json:"legacy_decision"`
	V2Decision             RequestRuntimeDecisionSummary  `json:"v2_decision"`
	SemanticDecision       *RequestRuntimeDecisionSummary `json:"semantic_decision,omitempty"`
	SemanticDifferences    []string                       `json:"semantic_differences,omitempty"`
	SemanticDeltaLabels    []string                       `json:"semantic_delta_labels,omitempty"`
	ShadowLogPath          string                         `json:"shadow_log_path,omitempty"`
	WriteError             string                         `json:"write_error,omitempty"`
}

type RequestRuntimeShadowStats struct {
	FirstObservedAt       time.Time                       `json:"first_observed_at,omitempty"`
	LastObservedAt        time.Time                       `json:"last_observed_at,omitempty"`
	Total                 int                             `json:"total"`
	Diverged              int                             `json:"diverged"`
	RuntimeDiverged       int                             `json:"runtime_diverged"`
	SemanticObserved      int                             `json:"semantic_observed"`
	SemanticDiverged      int                             `json:"semantic_diverged"`
	SemanticRiskyDiverged int                             `json:"semantic_risky_diverged"`
	ByRequestClass        []RequestRuntimeShadowClassStat `json:"by_request_class,omitempty"`
	RecentSamples         []RequestRuntimeShadowSample    `json:"recent_samples,omitempty"`
}

type RequestRuntimeShadowClassStat struct {
	RequestClass          string `json:"request_class,omitempty"`
	Total                 int    `json:"total"`
	Diverged              int    `json:"diverged"`
	RuntimeDiverged       int    `json:"runtime_diverged"`
	SemanticObserved      int    `json:"semantic_observed"`
	SemanticDiverged      int    `json:"semantic_diverged"`
	SemanticRiskyDiverged int    `json:"semantic_risky_diverged"`
}

type RequestRuntimeShadowSample struct {
	ObservedAt             time.Time `json:"observed_at,omitempty"`
	Mode                   string    `json:"mode,omitempty"`
	EnabledPath            string    `json:"enabled_path,omitempty"`
	RequestClass           string    `json:"request_class,omitempty"`
	SemanticRequestClass   string    `json:"semantic_request_class,omitempty"`
	FinalGateState         string    `json:"final_gate_state,omitempty"`
	SemanticFinalGateState string    `json:"semantic_final_gate_state,omitempty"`
	Differences            []string  `json:"differences,omitempty"`
	SemanticDifferences    []string  `json:"semantic_differences,omitempty"`
	SemanticDeltaLabels    []string  `json:"semantic_delta_labels,omitempty"`
	SemanticClassifierMode string    `json:"semantic_classifier_mode,omitempty"`
	ShadowLogPath          string    `json:"shadow_log_path,omitempty"`
	WriteError             string    `json:"write_error,omitempty"`
}

func mergeRequestRuntimeConfig(dst *RequestRuntimeConfig, src RequestRuntimeConfig) {
	if dst == nil {
		return
	}
	if strings.TrimSpace(src.Mode) != "" {
		dst.Mode = normalizeRequestRuntimeMode(src.Mode)
	}
	if len(src.EnabledClasses) > 0 {
		dst.EnabledClasses = normalizeRequestRuntimeClasses(src.EnabledClasses)
	}
	if strings.TrimSpace(src.LogDir) != "" {
		dst.LogDir = strings.TrimSpace(src.LogDir)
	}
	mergeRequestSemanticClassifierConfig(&dst.SemanticClassifier, src.SemanticClassifier)
}

func normalizeRequestRuntimeConfig(cfg *RequestRuntimeConfig) {
	if cfg == nil {
		return
	}
	cfg.Mode = normalizeRequestRuntimeMode(cfg.Mode)
	cfg.EnabledClasses = normalizeRequestRuntimeClasses(cfg.EnabledClasses)
	cfg.LogDir = strings.TrimSpace(cfg.LogDir)
	normalizeRequestSemanticClassifierConfig(&cfg.SemanticClassifier)
}

func mergeRequestSemanticClassifierConfig(dst *RequestSemanticClassifierConfig, src RequestSemanticClassifierConfig) {
	if dst == nil {
		return
	}
	if strings.TrimSpace(src.Mode) != "" {
		dst.Mode = normalizeRequestSemanticClassifierMode(src.Mode)
	}
	if src.MinConfidence != 0 {
		dst.MinConfidence = src.MinConfidence
	}
	if src.MaxTokens != 0 {
		dst.MaxTokens = src.MaxTokens
	}
}

func normalizeRequestRuntimeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", RequestRuntimeModeDisabled:
		return RequestRuntimeModeDisabled
	case RequestRuntimeModeShadow:
		return RequestRuntimeModeShadow
	case RequestRuntimeModeEnabled:
		return RequestRuntimeModeEnabled
	default:
		return RequestRuntimeModeDisabled
	}
}

func normalizeRequestRuntimeClasses(classes []string) []string {
	out := make([]string, 0, len(classes))
	seen := map[string]bool{}
	for _, class := range classes {
		class = normalizeRequestRuntimeClass(class)
		if class == "" || seen[class] {
			continue
		}
		seen[class] = true
		out = append(out, class)
	}
	slices.Sort(out)
	return out
}

func normalizeRequestRuntimeClass(class string) string {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case "review", "review-only", RequestRuntimeClassReviewOnly:
		return RequestRuntimeClassReviewOnly
	case "plan", "plan-only", RequestRuntimeClassPlanOnly:
		return RequestRuntimeClassPlanOnly
	case "document", "document-authoring", "document_artifact", RequestRuntimeClassDocumentAuthoring:
		return RequestRuntimeClassDocumentAuthoring
	case "edit", "explicit-edit", RequestRuntimeClassExplicitEdit:
		return RequestRuntimeClassExplicitEdit
	case RequestRuntimeClassGit:
		return RequestRuntimeClassGit
	case RequestRuntimeClassResearch:
		return RequestRuntimeClassResearch
	case RequestRuntimeClassDefault:
		return RequestRuntimeClassDefault
	case RequestRuntimeClassAll:
		return RequestRuntimeClassAll
	default:
		return ""
	}
}

func requestRuntimeClassForEnvelope(envelope RequestEnvelope) string {
	envelope.Normalize()
	switch {
	case envelope.GoalPromptDraftOnly:
		return RequestRuntimeClassPlanOnly
	case envelope.ReviewOnlyModeRequest || envelope.PrimaryClass == RequestClassReview:
		return RequestRuntimeClassReviewOnly
	case envelope.DocumentAuthoring || envelope.PrimaryClass == RequestClassDocument:
		return RequestRuntimeClassDocumentAuthoring
	case envelope.ExplicitEditRequest || envelope.AllowsFileMutation || envelope.PrimaryClass == RequestClassEdit:
		return RequestRuntimeClassExplicitEdit
	case envelope.ExplicitGitRequest || envelope.AllowsGitMutation || envelope.PrimaryClass == RequestClassGit:
		return RequestRuntimeClassGit
	case envelope.RequiresFreshExternalInfo || envelope.AllowsWebResearch || envelope.PrimaryClass == RequestClassResearch:
		return RequestRuntimeClassResearch
	default:
		return RequestRuntimeClassDefault
	}
}

func requestRuntimeV2EnabledForEnvelope(cfg Config, envelope RequestEnvelope) bool {
	runtimeCfg := cfg.RequestRuntime
	normalizeRequestRuntimeConfig(&runtimeCfg)
	if runtimeCfg.Mode != RequestRuntimeModeEnabled {
		return false
	}
	class := requestRuntimeClassForEnvelope(envelope)
	for _, enabled := range runtimeCfg.EnabledClasses {
		if enabled == RequestRuntimeClassAll || enabled == class {
			return true
		}
	}
	return false
}

func requestRuntimeShadowModeEnabled(cfg Config) bool {
	runtimeCfg := cfg.RequestRuntime
	normalizeRequestRuntimeConfig(&runtimeCfg)
	return runtimeCfg.Mode == RequestRuntimeModeShadow
}

func selectRequestRuntimeDecision(cfg Config, envelope RequestEnvelope, legacy RequestRuntimeDecisionSummary, v2 RequestRuntimeDecisionSummary) RequestRuntimeDecisionSummary {
	if requestRuntimeV2EnabledForEnvelope(cfg, envelope) {
		v2.Source = "v2"
		return v2
	}
	legacy.Source = "legacy"
	return legacy
}

func buildRequestRuntimeDecisionSummary(source string, envelope RequestEnvelope, plan turnToolExposurePlan, turnRuntime *TurnRuntimeState, finalDecision FinalGateDecision, registry *ToolRegistry) RequestRuntimeDecisionSummary {
	envelope.Normalize()
	summary := RequestRuntimeDecisionSummary{
		Source:         strings.TrimSpace(source),
		RequestClass:   requestRuntimeClassForEnvelope(envelope),
		ExposedTools:   requestRuntimeExposedTools(plan, registry),
		DisabledTools:  requestRuntimeDisabledTools(plan, registry),
		FinalGateState: string(finalDecision.State),
		FinalGateReady: finalDecision.Ready,
	}
	if turnRuntime != nil {
		for _, item := range turnRuntime.Interventions {
			kind := strings.TrimSpace(string(item.Kind))
			if kind != "" {
				summary.InterventionKinds = append(summary.InterventionKinds, kind)
			}
		}
	}
	summary.InterventionKinds = normalizeTaskStateList(summary.InterventionKinds, 32)
	return summary
}

func requestRuntimeExposedTools(plan turnToolExposurePlan, registry *ToolRegistry) []string {
	if registry == nil {
		return nil
	}
	names := registry.ToolNames()
	out := make([]string, 0, len(names))
	for _, name := range names {
		if plan.toolDisabled(name) {
			continue
		}
		out = append(out, name)
	}
	return normalizeTaskStateList(out, 128)
}

func requestRuntimeDisabledTools(plan turnToolExposurePlan, registry *ToolRegistry) []string {
	if registry == nil {
		var names []string
		for name, disabled := range plan.DisabledTools {
			if disabled {
				names = append(names, strings.TrimSpace(name))
			}
		}
		return normalizeTaskStateList(names, 128)
	}
	names := registry.ToolNames()
	out := make([]string, 0, len(names))
	for _, name := range names {
		if plan.toolDisabled(name) {
			out = append(out, name)
		}
	}
	return normalizeTaskStateList(out, 128)
}

func requestRuntimeDecisionDifferences(legacy RequestRuntimeDecisionSummary, v2 RequestRuntimeDecisionSummary) []string {
	var differences []string
	add := func(label string) {
		differences = append(differences, label)
	}
	if legacy.RequestClass != v2.RequestClass {
		add("request_class")
	}
	if !stringSlicesEqual(legacy.ExposedTools, v2.ExposedTools) {
		add("exposed_tools")
	}
	if !stringSlicesEqual(legacy.DisabledTools, v2.DisabledTools) {
		add("disabled_tools")
	}
	if !stringSlicesEqual(legacy.InterventionKinds, v2.InterventionKinds) {
		add("interventions")
	}
	if legacy.FinalGateState != v2.FinalGateState || legacy.FinalGateReady != v2.FinalGateReady {
		add("final_gate")
	}
	return normalizeTaskStateList(differences, 16)
}

func compareRequestRuntimeDecisions(legacy RequestRuntimeDecisionSummary, v2 RequestRuntimeDecisionSummary) RequestRuntimeShadowComparison {
	comparison := RequestRuntimeShadowComparison{
		GeneratedAt:    time.Now(),
		LegacyDecision: sanitizeRequestRuntimeDecisionSummary(legacy),
		V2Decision:     sanitizeRequestRuntimeDecisionSummary(v2),
	}
	comparison.Differences = requestRuntimeDecisionDifferences(comparison.LegacyDecision, comparison.V2Decision)
	comparison.Diverged = len(comparison.Differences) > 0
	return comparison
}

func semanticRequestRuntimeDifferenceLabels(differences []string) []string {
	out := make([]string, 0, len(differences))
	for _, difference := range differences {
		difference = strings.TrimSpace(difference)
		if difference == "" {
			continue
		}
		out = append(out, "semantic_"+difference)
	}
	return normalizeTaskStateList(out, 16)
}

func requestRuntimeSemanticDeltaLabels(baseline RequestRuntimeDecisionSummary, semantic RequestRuntimeDecisionSummary) []string {
	baseline = sanitizeRequestRuntimeDecisionSummary(baseline)
	semantic = sanitizeRequestRuntimeDecisionSummary(semantic)
	var labels []string
	if baseline.RequestClass != semantic.RequestClass {
		labels = append(labels, "request_class_delta")
	}
	if !stringSlicesEqual(baseline.ExposedTools, semantic.ExposedTools) || !stringSlicesEqual(baseline.DisabledTools, semantic.DisabledTools) {
		labels = append(labels, "tool_exposure_delta")
		if requestRuntimeToolExposureExpanded(baseline, semantic) {
			labels = append(labels, "tool_exposure_expansion")
		}
		if requestRuntimeToolExposureNarrowed(baseline, semantic) {
			labels = append(labels, "tool_exposure_narrowing")
		}
	}
	baselineMutates := requestRuntimeClassAllowsMutation(baseline.RequestClass)
	semanticMutates := requestRuntimeClassAllowsMutation(semantic.RequestClass)
	if !baselineMutates && semanticMutates {
		labels = append(labels, "mutation_expansion")
	}
	if baselineMutates && !semanticMutates {
		labels = append(labels, "mutation_narrowing")
	}
	if baseline.RequestClass != RequestRuntimeClassGit && semantic.RequestClass == RequestRuntimeClassGit {
		labels = append(labels, "git_expansion")
	}
	if baseline.FinalGateState != semantic.FinalGateState || baseline.FinalGateReady != semantic.FinalGateReady {
		labels = append(labels, "final_gate_delta")
		if !baseline.FinalGateReady && semantic.FinalGateReady {
			labels = append(labels, "weaker_final_gate")
		}
		if baseline.FinalGateReady && !semantic.FinalGateReady {
			labels = append(labels, "stricter_final_gate")
		}
	}
	return normalizeTaskStateList(labels, 16)
}

func requestRuntimeClassAllowsMutation(class string) bool {
	switch normalizeRequestRuntimeClass(class) {
	case RequestRuntimeClassDocumentAuthoring, RequestRuntimeClassExplicitEdit, RequestRuntimeClassGit:
		return true
	default:
		return false
	}
}

func requestRuntimeToolExposureExpanded(baseline RequestRuntimeDecisionSummary, semantic RequestRuntimeDecisionSummary) bool {
	return requestRuntimeListHasExtra(semantic.ExposedTools, baseline.ExposedTools) || requestRuntimeListHasExtra(baseline.DisabledTools, semantic.DisabledTools)
}

func requestRuntimeToolExposureNarrowed(baseline RequestRuntimeDecisionSummary, semantic RequestRuntimeDecisionSummary) bool {
	return requestRuntimeListHasExtra(baseline.ExposedTools, semantic.ExposedTools) || requestRuntimeListHasExtra(semantic.DisabledTools, baseline.DisabledTools)
}

func requestRuntimeListHasExtra(left []string, right []string) bool {
	rightSet := make(map[string]struct{})
	for _, item := range normalizeTaskStateList(right, 128) {
		rightSet[strings.ToLower(item)] = struct{}{}
	}
	for _, item := range normalizeTaskStateList(left, 128) {
		if _, ok := rightSet[strings.ToLower(item)]; !ok {
			return true
		}
	}
	return false
}

func requestRuntimeSemanticDeltaRisky(labels []string) bool {
	for _, label := range normalizeTaskStateList(labels, 16) {
		switch strings.TrimSpace(label) {
		case "mutation_expansion", "git_expansion", "weaker_final_gate", "tool_exposure_expansion":
			return true
		}
	}
	return false
}

func sanitizeRequestRuntimeDecisionSummary(summary RequestRuntimeDecisionSummary) RequestRuntimeDecisionSummary {
	summary.Source = strings.TrimSpace(summary.Source)
	summary.RequestClass = normalizeRequestRuntimeClass(summary.RequestClass)
	if summary.RequestClass == "" {
		summary.RequestClass = RequestRuntimeClassDefault
	}
	summary.ExposedTools = normalizeTaskStateList(summary.ExposedTools, 128)
	summary.DisabledTools = normalizeTaskStateList(summary.DisabledTools, 128)
	summary.InterventionKinds = normalizeTaskStateList(summary.InterventionKinds, 32)
	summary.FinalGateState = strings.TrimSpace(summary.FinalGateState)
	return summary
}

func updateRequestRuntimeShadowStats(stats *RequestRuntimeShadowStats, comparison RequestRuntimeShadowComparison) *RequestRuntimeShadowStats {
	if stats == nil {
		stats = &RequestRuntimeShadowStats{}
	}
	comparison.LegacyDecision = sanitizeRequestRuntimeDecisionSummary(comparison.LegacyDecision)
	comparison.V2Decision = sanitizeRequestRuntimeDecisionSummary(comparison.V2Decision)
	if comparison.GeneratedAt.IsZero() {
		comparison.GeneratedAt = time.Now()
	}
	if stats.FirstObservedAt.IsZero() {
		stats.FirstObservedAt = comparison.GeneratedAt
	}
	stats.LastObservedAt = comparison.GeneratedAt
	stats.Total++
	runtimeDifferences := requestRuntimeDecisionDifferences(comparison.LegacyDecision, comparison.V2Decision)
	if len(runtimeDifferences) > 0 {
		stats.RuntimeDiverged++
	}
	if comparison.SemanticDecision != nil {
		semantic := sanitizeRequestRuntimeDecisionSummary(*comparison.SemanticDecision)
		comparison.SemanticDecision = &semantic
		if len(comparison.SemanticDifferences) == 0 {
			comparison.SemanticDifferences = requestRuntimeDecisionDifferences(comparison.V2Decision, semantic)
		}
		if len(comparison.SemanticDeltaLabels) == 0 {
			comparison.SemanticDeltaLabels = requestRuntimeSemanticDeltaLabels(comparison.V2Decision, semantic)
		}
	}
	comparison.SemanticDifferences = normalizeTaskStateList(comparison.SemanticDifferences, 16)
	comparison.SemanticDeltaLabels = normalizeTaskStateList(comparison.SemanticDeltaLabels, 16)
	semanticObserved := comparison.SemanticDecision != nil || strings.TrimSpace(comparison.SemanticClassifierMode) != ""
	semanticDiverged := len(comparison.SemanticDifferences) > 0
	semanticRiskyDiverged := semanticDiverged && requestRuntimeSemanticDeltaRisky(comparison.SemanticDeltaLabels)
	if semanticDiverged {
		comparison.Diverged = true
	}
	if comparison.Diverged {
		stats.Diverged++
	}
	if semanticObserved {
		stats.SemanticObserved++
	}
	if semanticDiverged {
		stats.SemanticDiverged++
	}
	if semanticRiskyDiverged {
		stats.SemanticRiskyDiverged++
	}
	class := requestRuntimeShadowStatsClass(comparison)
	requestRuntimeShadowStatsRecordClass(stats, class, comparison.Diverged, len(runtimeDifferences) > 0, semanticObserved, semanticDiverged, semanticRiskyDiverged)
	stats.RecentSamples = append(stats.RecentSamples, requestRuntimeShadowSampleFromComparison(comparison))
	if len(stats.RecentSamples) > requestRuntimeShadowMaxRecentSamples {
		stats.RecentSamples = stats.RecentSamples[len(stats.RecentSamples)-requestRuntimeShadowMaxRecentSamples:]
	}
	return stats
}

func requestRuntimeShadowStatsClass(comparison RequestRuntimeShadowComparison) string {
	class := normalizeRequestRuntimeClass(comparison.EnabledPath)
	if class == "" {
		class = normalizeRequestRuntimeClass(comparison.V2Decision.RequestClass)
	}
	if class == "" {
		class = normalizeRequestRuntimeClass(comparison.LegacyDecision.RequestClass)
	}
	if class == "" {
		class = RequestRuntimeClassDefault
	}
	return class
}

func requestRuntimeShadowStatsRecordClass(stats *RequestRuntimeShadowStats, class string, diverged bool, runtimeDiverged bool, semanticObserved bool, semanticDiverged bool, semanticRiskyDiverged bool) {
	if stats == nil {
		return
	}
	class = normalizeRequestRuntimeClass(class)
	if class == "" {
		class = RequestRuntimeClassDefault
	}
	for i := range stats.ByRequestClass {
		if stats.ByRequestClass[i].RequestClass == class {
			requestRuntimeShadowStatsIncrementClass(&stats.ByRequestClass[i], diverged, runtimeDiverged, semanticObserved, semanticDiverged, semanticRiskyDiverged)
			return
		}
	}
	stat := RequestRuntimeShadowClassStat{RequestClass: class}
	requestRuntimeShadowStatsIncrementClass(&stat, diverged, runtimeDiverged, semanticObserved, semanticDiverged, semanticRiskyDiverged)
	stats.ByRequestClass = append(stats.ByRequestClass, stat)
	slices.SortFunc(stats.ByRequestClass, func(a RequestRuntimeShadowClassStat, b RequestRuntimeShadowClassStat) int {
		return strings.Compare(a.RequestClass, b.RequestClass)
	})
}

func requestRuntimeShadowStatsIncrementClass(stat *RequestRuntimeShadowClassStat, diverged bool, runtimeDiverged bool, semanticObserved bool, semanticDiverged bool, semanticRiskyDiverged bool) {
	if stat == nil {
		return
	}
	stat.Total++
	if diverged {
		stat.Diverged++
	}
	if runtimeDiverged {
		stat.RuntimeDiverged++
	}
	if semanticObserved {
		stat.SemanticObserved++
	}
	if semanticDiverged {
		stat.SemanticDiverged++
	}
	if semanticRiskyDiverged {
		stat.SemanticRiskyDiverged++
	}
}

func requestRuntimeShadowSampleFromComparison(comparison RequestRuntimeShadowComparison) RequestRuntimeShadowSample {
	sample := RequestRuntimeShadowSample{
		ObservedAt:             comparison.GeneratedAt,
		Mode:                   normalizeRequestRuntimeMode(comparison.Mode),
		EnabledPath:            requestRuntimeShadowStatsClass(comparison),
		RequestClass:           normalizeRequestRuntimeClass(comparison.V2Decision.RequestClass),
		FinalGateState:         strings.TrimSpace(comparison.V2Decision.FinalGateState),
		Differences:            normalizeTaskStateList(comparison.Differences, 32),
		SemanticDifferences:    normalizeTaskStateList(comparison.SemanticDifferences, 16),
		SemanticDeltaLabels:    normalizeTaskStateList(comparison.SemanticDeltaLabels, 16),
		SemanticClassifierMode: normalizeRequestSemanticClassifierMode(comparison.SemanticClassifierMode),
		ShadowLogPath:          requestRuntimeShadowLogRef(comparison.ShadowLogPath),
		WriteError:             compactPromptSection(comparison.WriteError, 180),
	}
	if comparison.SemanticDecision != nil {
		semantic := sanitizeRequestRuntimeDecisionSummary(*comparison.SemanticDecision)
		sample.SemanticRequestClass = semantic.RequestClass
		sample.SemanticFinalGateState = strings.TrimSpace(semantic.FinalGateState)
	}
	if sample.RequestClass == "" {
		sample.RequestClass = RequestRuntimeClassDefault
	}
	return sample
}

func requestRuntimeShadowLogRef(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	name := filepath.Base(filepath.Clean(path))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Join(requestRuntimeShadowDirName, name))
}

func (a *Agent) observeRequestRuntimeShadow(envelope RequestEnvelope, turnRuntime *TurnRuntimeState, finalDecision FinalGateDecision, unresolvedVerification bool, finalAnswerOnlyCorrection bool, verificationOutOfScopeFinalOnly bool, verificationSkippedFinalOnly bool, latestUserExplicitWebResearch bool, localCodeToolPolicyForTurn bool, reply string, finalCtx TurnRuntimeFinalContext) {
	if a == nil || a.Session == nil {
		return
	}
	runtimeShadow := requestRuntimeShadowModeEnabled(a.Config)
	semanticShadow := requestSemanticClassifierShadowModeEnabled(a.Config) && a.Session.LastSemanticRequestEnvelope != nil
	if !runtimeShadow && !semanticShadow {
		return
	}
	plan := a.buildTurnToolExposurePlanForEnvelope(nil, envelope, unresolvedVerification, finalAnswerOnlyCorrection, verificationOutOfScopeFinalOnly, verificationSkippedFinalOnly, latestUserExplicitWebResearch, localCodeToolPolicyForTurn)
	legacy := buildRequestRuntimeDecisionSummary("legacy", envelope, plan, turnRuntime, finalDecision, a.Tools)
	v2 := buildRequestRuntimeDecisionSummary("v2", envelope, plan, turnRuntime, finalDecision, a.Tools)
	comparison := compareRequestRuntimeDecisions(legacy, v2)
	if runtimeShadow {
		comparison.Mode = RequestRuntimeModeShadow
	} else {
		comparison.Mode = RequestRuntimeModeDisabled
	}
	comparison.SessionID = strings.TrimSpace(a.Session.ID)
	comparison.EnabledPath = requestRuntimeClassForEnvelope(envelope)
	if semanticShadow {
		semanticEnvelope := *a.Session.LastSemanticRequestEnvelope
		semanticPlan := a.buildTurnToolExposurePlanForEnvelope(nil, semanticEnvelope, unresolvedVerification, finalAnswerOnlyCorrection, verificationOutOfScopeFinalOnly, verificationSkippedFinalOnly, latestUserExplicitWebResearch, localCodeToolPolicyForTurn)
		semanticFinalCtx := finalCtx
		semanticFinalCtx.ExplicitEditRequest = semanticEnvelope.ExplicitEditRequest
		semanticFinalCtx.GeneratedDocumentHarnessOwnsIt = semanticEnvelope.DocumentAuthoring && finalCtx.GeneratedDocumentHarnessOwnsIt
		semanticFinalDecision := DecideFinalGate(a.buildFinalGateInput(semanticEnvelope, turnRuntime, reply, semanticFinalCtx))
		semantic := buildRequestRuntimeDecisionSummary("semantic_classifier", semanticEnvelope, semanticPlan, turnRuntime, semanticFinalDecision, a.Tools)
		semantic = sanitizeRequestRuntimeDecisionSummary(semantic)
		comparison.SemanticClassifierMode = RequestSemanticClassifierModeShadow
		comparison.SemanticDecision = &semantic
		comparison.SemanticDifferences = requestRuntimeDecisionDifferences(comparison.V2Decision, semantic)
		comparison.SemanticDeltaLabels = requestRuntimeSemanticDeltaLabels(comparison.V2Decision, semantic)
		if len(comparison.SemanticDifferences) > 0 {
			comparison.Differences = normalizeTaskStateList(append(comparison.Differences, semanticRequestRuntimeDifferenceLabels(comparison.SemanticDifferences)...), 32)
			comparison.Diverged = true
		}
	}
	if comparison.Diverged {
		path, err := writeRequestRuntimeShadowDivergence(a.Workspace.Root, a.Config.RequestRuntime, comparison)
		if err != nil {
			comparison.WriteError = err.Error()
		} else {
			comparison.ShadowLogPath = path
		}
	}
	a.Session.LastRequestRuntimeShadow = &comparison
	a.Session.RequestRuntimeShadowStats = updateRequestRuntimeShadowStats(a.Session.RequestRuntimeShadowStats, comparison)
}

func writeRequestRuntimeShadowDivergence(root string, cfg RequestRuntimeConfig, comparison RequestRuntimeShadowComparison) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("workspace root is required")
	}
	logRoot := requestRuntimeShadowLogRoot(root, cfg)
	artifactRoot := filepath.Join(root, userConfigDirName)
	if !pathIsInsideRoot(artifactRoot, logRoot) {
		return "", fmt.Errorf("request runtime shadow log path must stay inside %s", artifactRoot)
	}
	if err := os.MkdirAll(logRoot, 0o755); err != nil {
		return "", err
	}
	comparison.GeneratedAt = time.Now()
	comparison.ShadowLogPath = ""
	comparison.WriteError = ""
	data, err := json.MarshalIndent(comparison, "", "  ")
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("shadow-%s.json", comparison.GeneratedAt.Format("20060102-150405.000000000"))
	path := filepath.Join(logRoot, name)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func requestRuntimeShadowLogRoot(root string, cfg RequestRuntimeConfig) string {
	if strings.TrimSpace(cfg.LogDir) != "" {
		logDir := filepath.Clean(cfg.LogDir)
		if !filepath.IsAbs(logDir) {
			return filepath.Join(root, logDir)
		}
		return logDir
	}
	return filepath.Join(root, userConfigDirName, requestRuntimeShadowDirName)
}

func pathIsInsideRoot(root string, path string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func stringSlicesEqual(a []string, b []string) bool {
	a = normalizeTaskStateList(a, 256)
	b = normalizeTaskStateList(b, 256)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
