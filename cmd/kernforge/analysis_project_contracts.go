package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type AnalysisClaim struct {
	ID                string   `json:"id,omitempty"`
	Kind              string   `json:"kind,omitempty"`
	Claim             string   `json:"claim"`
	SourceAnchors     []string `json:"source_anchors,omitempty"`
	EvidencePacketIDs []string `json:"evidence_packet_ids,omitempty"`
	Confidence        string   `json:"confidence,omitempty"`
	DependsOn         []string `json:"depends_on,omitempty"`
	DisprovesWhen     string   `json:"disproves_when,omitempty"`
	VerificationHint  string   `json:"verification_hint,omitempty"`
}

type AnalysisSkippedFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size,omitempty"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

type AnalysisCoverageLedger struct {
	GeneratedAt      time.Time             `json:"generated_at,omitempty"`
	Root             string                `json:"root,omitempty"`
	MaxFileBytes     int64                 `json:"max_file_bytes,omitempty"`
	VisitedFiles     int                   `json:"visited_files,omitempty"`
	IncludedFiles    int                   `json:"included_files,omitempty"`
	SkippedFileCount int                   `json:"skipped_file_count,omitempty"`
	SkippedBytes     int64                 `json:"skipped_bytes,omitempty"`
	OversizedFiles   int                   `json:"oversized_files,omitempty"`
	UnreadableFiles  int                   `json:"unreadable_files,omitempty"`
	NonTextFiles     int                   `json:"non_text_files,omitempty"`
	ExcludedFiles    int                   `json:"excluded_files,omitempty"`
	ExcludedDirs     int                   `json:"excluded_dirs,omitempty"`
	SkippedFiles     []AnalysisSkippedFile `json:"skipped_files,omitempty"`
}

type EvidencePacket struct {
	ID               string   `json:"id"`
	ShardID          string   `json:"shard_id,omitempty"`
	Kind             string   `json:"kind"`
	Path             string   `json:"path"`
	SymbolID         string   `json:"symbol_id,omitempty"`
	SymbolName       string   `json:"symbol_name,omitempty"`
	StartLine        int      `json:"start_line,omitempty"`
	EndLine          int      `json:"end_line,omitempty"`
	ExtractionMethod string   `json:"extraction_method,omitempty"`
	Confidence       string   `json:"confidence,omitempty"`
	ContentHash      string   `json:"content_hash,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	Text             string   `json:"text,omitempty"`
}

type AnalysisRuntimeFeedback struct {
	ErrorLogPath         string    `json:"error_log_path,omitempty"`
	RecentProviderErrors int       `json:"recent_provider_errors,omitempty"`
	RecentRetryCount     int       `json:"recent_retry_count,omitempty"`
	RecentTimeoutCount   int       `json:"recent_timeout_count,omitempty"`
	RecentRateLimitCount int       `json:"recent_rate_limit_count,omitempty"`
	RecentOverloadCount  int       `json:"recent_overload_count,omitempty"`
	RecentErrorModels    []string  `json:"recent_error_models,omitempty"`
	LastErrorAt          time.Time `json:"last_error_at,omitempty"`
	SuggestedAdjustments []string  `json:"suggested_adjustments,omitempty"`
	AppliedAdjustments   []string  `json:"applied_adjustments,omitempty"`
}

type AnalysisPreflightShard struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Type             string   `json:"type,omitempty"`
	Objective        string   `json:"objective,omitempty"`
	RequiredEvidence []string `json:"required_evidence,omitempty"`
	SuccessCriteria  []string `json:"success_criteria,omitempty"`
	PrimaryFileCount int      `json:"primary_file_count,omitempty"`
	ReferenceCount   int      `json:"reference_count,omitempty"`
	EstimatedLines   int      `json:"estimated_lines,omitempty"`
}

type AnalysisPreflight struct {
	GeneratedAt      time.Time                `json:"generated_at,omitempty"`
	Intent           string                   `json:"intent,omitempty"`
	RequestedMode    string                   `json:"requested_mode,omitempty"`
	EffectiveMode    string                   `json:"effective_mode,omitempty"`
	Scope            AnalysisGoalScope        `json:"scope,omitempty"`
	RequiredIndexes  []string                 `json:"required_indexes,omitempty"`
	RiskLenses       []string                 `json:"risk_lenses,omitempty"`
	RecommendedDepth string                   `json:"recommended_depth,omitempty"`
	EstimatedFiles   int                      `json:"estimated_files,omitempty"`
	EstimatedLines   int                      `json:"estimated_lines,omitempty"`
	PlannedShards    int                      `json:"planned_shards,omitempty"`
	WorkerSlots      int                      `json:"worker_slots,omitempty"`
	ProviderProfiles map[string]string        `json:"provider_profiles,omitempty"`
	RuntimeFeedback  AnalysisRuntimeFeedback  `json:"runtime_feedback,omitempty"`
	Warnings         []string                 `json:"warnings,omitempty"`
	SuccessCriteria  []string                 `json:"success_criteria,omitempty"`
	ShardContracts   []AnalysisPreflightShard `json:"shard_contracts,omitempty"`
}

type AnalysisModeCriterion struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Score    int      `json:"score"`
	Evidence []string `json:"evidence,omitempty"`
	Missing  []string `json:"missing,omitempty"`
}

type AnalysisCoverageGap struct {
	ID               string   `json:"id"`
	Kind             string   `json:"kind"`
	Severity         string   `json:"severity"`
	ShardID          string   `json:"shard_id,omitempty"`
	ShardName        string   `json:"shard_name,omitempty"`
	Reason           string   `json:"reason"`
	TargetFiles      []string `json:"target_files,omitempty"`
	RequiredEvidence []string `json:"required_evidence,omitempty"`
}

type AnalysisModeScorecard struct {
	GeneratedAt  time.Time               `json:"generated_at,omitempty"`
	Mode         string                  `json:"mode,omitempty"`
	Status       string                  `json:"status,omitempty"`
	Score        int                     `json:"score,omitempty"`
	Criteria     []AnalysisModeCriterion `json:"criteria,omitempty"`
	CoverageGaps []AnalysisCoverageGap   `json:"coverage_gaps,omitempty"`
	Summary      []string                `json:"summary,omitempty"`
}

const analysisRuntimeFeedbackWindow = 24 * time.Hour

func analysisRuntimeFeedbackFromLog(workspaceRoot string, currentModel string) AnalysisRuntimeFeedback {
	feedback := AnalysisRuntimeFeedback{
		ErrorLogPath: runtimeErrorLogPath(workspaceRoot),
	}
	path := strings.TrimSpace(feedback.ErrorLogPath)
	if path == "" {
		return feedback
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return feedback
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 120 {
		lines = lines[len(lines)-120:]
	}
	now := time.Now().UTC()
	currentModelID := strings.ToLower(strings.TrimSpace(openCodeAPIModelID(currentModel)))
	models := []string{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		entry := RuntimeErrorLogEntry{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		text := strings.ToLower(entry.Kind + " " + entry.Summary + " " + entry.Raw + " " + entry.CorrelationID)
		if !strings.Contains(text, "provider") && !strings.Contains(text, "model") {
			continue
		}
		if !entry.Time.IsZero() {
			entryTime := entry.Time.UTC()
			if entryTime.Before(now.Add(-analysisRuntimeFeedbackWindow)) || entryTime.After(now.Add(5*time.Minute)) {
				continue
			}
		}
		model := strings.TrimSpace(entry.Entities["model"])
		if model == "" {
			model = strings.TrimSpace(entry.Entities["session_model"])
		}
		entryModelID := strings.ToLower(strings.TrimSpace(openCodeAPIModelID(model)))
		if currentModelID != "" && entryModelID != "" && entryModelID != currentModelID {
			continue
		}
		feedback.RecentProviderErrors++
		if !entry.Time.IsZero() && entry.Time.After(feedback.LastErrorAt) {
			feedback.LastErrorAt = entry.Time
		}
		if strings.Contains(text, "retry") || strings.Contains(strings.ToLower(entry.CorrelationID), "retry") {
			feedback.RecentRetryCount++
		}
		if strings.Contains(text, "timeout") || strings.Contains(text, "deadline exceeded") || strings.Contains(text, "context deadline") {
			feedback.RecentTimeoutCount++
		}
		if strings.Contains(text, "rate limit") || strings.Contains(text, "429") {
			feedback.RecentRateLimitCount++
		}
		if containsAny(text, "overload", "server error", "bad gateway", "gateway timeout", "service unavailable", "temporarily unavailable", " 500", " 502", " 503", " 504") {
			feedback.RecentOverloadCount++
		}
		if model == "" {
			model = strings.TrimSpace(currentModel)
		}
		if model != "" {
			models = append(models, model)
		}
		if attemptText := strings.TrimSpace(entry.Entities["attempt"]); attemptText != "" {
			if attempt, err := strconv.Atoi(attemptText); err == nil && attempt > 1 {
				feedback.RecentRetryCount++
			}
		}
	}
	feedback.RecentErrorModels = limitStrings(analysisUniqueStrings(models), 8)
	if feedback.RecentTimeoutCount+feedback.RecentOverloadCount >= 2 {
		feedback.SuggestedAdjustments = append(feedback.SuggestedAdjustments, "reduce shard size for provider pressure")
	}
	if feedback.RecentRetryCount >= 3 {
		feedback.SuggestedAdjustments = append(feedback.SuggestedAdjustments, "keep worker concurrency conservative")
	}
	if feedback.RecentRateLimitCount >= 2 {
		feedback.SuggestedAdjustments = append(feedback.SuggestedAdjustments, "avoid gap expansion unless coverage is materially weak")
	}
	feedback.SuggestedAdjustments = analysisUniqueStrings(feedback.SuggestedAdjustments)
	return feedback
}

func (a *projectAnalyzer) applyRuntimeFeedbackToAnalysisConfig(feedback AnalysisRuntimeFeedback) []string {
	if a == nil {
		return nil
	}
	pressure := feedback.RecentTimeoutCount + feedback.RecentOverloadCount + feedback.RecentRetryCount
	if feedback.RecentProviderErrors < 2 && pressure < 2 {
		return nil
	}
	notes := []string{}
	beforeLines := a.analysisCfg.MaxLinesPerShard
	beforeFiles := a.analysisCfg.MaxFilesPerShard
	beforeTotal := a.analysisCfg.MaxTotalShards
	if !a.analysisCfg.maxLinesPerShardConfigured && a.analysisCfg.MaxLinesPerShard > 1200 {
		next := a.analysisCfg.MaxLinesPerShard * 70 / 100
		if feedback.RecentTimeoutCount+feedback.RecentOverloadCount >= 3 {
			next = a.analysisCfg.MaxLinesPerShard * 55 / 100
		}
		if next < 1200 {
			next = 1200
		}
		a.analysisCfg.MaxLinesPerShard = next
	}
	if !a.analysisCfg.maxFilesPerShardConfigured && a.analysisCfg.MaxFilesPerShard > 12 {
		next := a.analysisCfg.MaxFilesPerShard * 70 / 100
		if feedback.RecentTimeoutCount+feedback.RecentOverloadCount >= 3 {
			next = a.analysisCfg.MaxFilesPerShard * 55 / 100
		}
		if next < 12 {
			next = 12
		}
		a.analysisCfg.MaxFilesPerShard = next
	}
	if !a.analysisCfg.maxTotalShardsConfigured && a.analysisCfg.MaxTotalShards > 0 {
		next := a.analysisCfg.MaxTotalShards * 4 / 3
		if next > a.analysisCfg.MaxTotalShards {
			a.analysisCfg.MaxTotalShards = analysisMinInt(next, 128)
		}
	}
	if beforeLines != a.analysisCfg.MaxLinesPerShard || beforeFiles != a.analysisCfg.MaxFilesPerShard || beforeTotal != a.analysisCfg.MaxTotalShards {
		notes = append(notes, fmt.Sprintf(
			"runtime provider feedback adjusted shard planner: max_lines_per_shard=%d->%d max_files_per_shard=%d->%d max_total_shards=%d->%d",
			beforeLines,
			a.analysisCfg.MaxLinesPerShard,
			beforeFiles,
			a.analysisCfg.MaxFilesPerShard,
			beforeTotal,
			a.analysisCfg.MaxTotalShards,
		))
	}
	return notes
}

func (a *projectAnalyzer) buildAnalysisPreflight(snapshot ProjectSnapshot, goal string, requestedMode string, scope AnalysisGoalScope, shards []AnalysisShard, workerSlots int, feedback AnalysisRuntimeFeedback) AnalysisPreflight {
	preflight := AnalysisPreflight{
		GeneratedAt:      time.Now(),
		Intent:           strings.TrimSpace(goal),
		RequestedMode:    strings.TrimSpace(requestedMode),
		EffectiveMode:    firstNonBlankAnalysisString(snapshot.AnalysisMode, defaultProjectAnalysisMode),
		Scope:            scope,
		RequiredIndexes:  analysisRequiredIndexes(snapshot),
		RiskLenses:       analysisLensNames(snapshot.AnalysisLenses),
		RecommendedDepth: analysisRecommendedDepth(snapshot, feedback),
		EstimatedFiles:   snapshot.TotalFiles,
		EstimatedLines:   snapshot.TotalLines,
		PlannedShards:    len(shards),
		WorkerSlots:      workerSlots,
		RuntimeFeedback:  feedback,
		Warnings:         analysisPreflightWarnings(snapshot, feedback),
		SuccessCriteria:  analysisPreflightSuccessCriteria(snapshot.AnalysisMode),
		ProviderProfiles: map[string]string{},
	}
	if a != nil {
		preflight.ProviderProfiles["conductor"] = strings.TrimSpace(a.cfg.Provider) + " / " + strings.TrimSpace(a.cfg.Model)
		preflight.ProviderProfiles["worker"] = describeAnalysisProfile(a.analysisCfg.WorkerProfile, a.workerOrDefaultClient(), a.workerModel())
		if a.shouldSkipModelReviewerForMode(snapshot.AnalysisMode) {
			preflight.ProviderProfiles["reviewer"] = "not configured; skipped in single-model mode"
		} else {
			preflight.ProviderProfiles["reviewer"] = describeAnalysisProfile(a.analysisCfg.ReviewerProfile, a.reviewerOrDefaultClient(), a.reviewerModel())
		}
	}
	for _, shard := range shards {
		preflight.ShardContracts = append(preflight.ShardContracts, AnalysisPreflightShard{
			ID:               shard.ID,
			Name:             shard.Name,
			Type:             shard.Type,
			Objective:        shard.Objective,
			RequiredEvidence: append([]string(nil), shard.RequiredEvidence...),
			SuccessCriteria:  append([]string(nil), shard.SuccessCriteria...),
			PrimaryFileCount: len(shard.PrimaryFiles),
			ReferenceCount:   len(shard.ReferenceFiles),
			EstimatedLines:   shard.EstimatedLines,
		})
	}
	return preflight
}

func analysisRequiredIndexes(snapshot ProjectSnapshot) []string {
	indexes := []string{"scan_snapshot", "architecture_facts", "structural_index_v2", "project_edges"}
	if len(snapshot.RuntimeEdges) > 0 {
		indexes = append(indexes, "runtime_edges")
	}
	if len(snapshot.UnrealProjects)+len(snapshot.UnrealModules)+len(snapshot.UnrealTypes) > 0 {
		indexes = append(indexes, "unreal_graph")
	}
	if normalizeProjectAnalysisMode(snapshot.AnalysisMode) == "root-cause" {
		indexes = append(indexes, "root_cause_plan", "root_cause_patterns")
	}
	return analysisUniqueStrings(indexes)
}

func analysisLensNames(lenses []AnalysisLens) []string {
	out := []string{}
	for _, lens := range lenses {
		if strings.TrimSpace(lens.Type) != "" {
			out = append(out, lens.Type)
		}
	}
	return analysisUniqueStrings(out)
}

func analysisRecommendedDepth(snapshot ProjectSnapshot, feedback AnalysisRuntimeFeedback) string {
	mode := normalizeProjectAnalysisMode(snapshot.AnalysisMode)
	if feedback.RecentTimeoutCount+feedback.RecentOverloadCount >= 2 {
		return "deep_with_smaller_shards"
	}
	if mode == "root-cause" || mode == "security" || mode == "surface" || snapshot.TotalLines > 120000 || snapshot.TotalFiles > 700 {
		return "deep"
	}
	if mode == "trace" || mode == "performance" || mode == "impact" {
		return "focused_deep"
	}
	return "standard"
}

func analysisPreflightWarnings(snapshot ProjectSnapshot, feedback AnalysisRuntimeFeedback) []string {
	warnings := []string{}
	if feedback.RecentProviderErrors > 0 {
		warnings = append(warnings, fmt.Sprintf("recent provider errors detected in runtime log: %d", feedback.RecentProviderErrors))
	}
	if snapshot.TotalFiles == 0 {
		warnings = append(warnings, "no analyzable source files were found")
	}
	if len(snapshot.EntrypointFiles) == 0 && strings.TrimSpace(snapshot.PrimaryStartup) == "" {
		warnings = append(warnings, "no deterministic entrypoint files were detected")
	}
	if normalizeProjectAnalysisMode(snapshot.AnalysisMode) == "root-cause" && !rootCauseInvestigationHasContent(snapshot.RootCause) {
		warnings = append(warnings, "root-cause mode has no normalized symptom plan")
	}
	return analysisUniqueStrings(warnings)
}

func analysisPreflightSuccessCriteria(mode string) []string {
	criteria := []string{
		"Every shard has an explicit objective, required evidence, and success criteria.",
		"Worker reports separate direct facts from inferences and include claim-level source anchors.",
		"Reviewer decisions validate evidence support before synthesis.",
	}
	criteria = append(criteria, projectAnalysisModePromptRequirements(mode)...)
	return analysisUniqueStrings(criteria)
}

func annotateAnalysisShards(snapshot ProjectSnapshot, shards []AnalysisShard, goal string) []AnalysisShard {
	out := append([]AnalysisShard(nil), shards...)
	for i := range out {
		annotateAnalysisShardContract(snapshot, &out[i], goal)
	}
	return out
}

func annotateAnalysisShardContract(snapshot ProjectSnapshot, shard *AnalysisShard, goal string) {
	if shard == nil {
		return
	}
	if strings.TrimSpace(shard.Type) == "" {
		shard.Type = analysisShardType(snapshot, *shard)
	}
	if strings.TrimSpace(shard.Objective) == "" {
		shard.Objective = analysisShardObjective(snapshot, *shard, goal)
	}
	if len(shard.RequiredEvidence) == 0 {
		shard.RequiredEvidence = analysisShardRequiredEvidence(snapshot, *shard)
	}
	if len(shard.SuccessCriteria) == 0 {
		shard.SuccessCriteria = analysisShardSuccessCriteria(snapshot, *shard)
	}
	shard.RequiredEvidence = analysisUniqueStrings(shard.RequiredEvidence)
	shard.SuccessCriteria = analysisUniqueStrings(shard.SuccessCriteria)
}

func analysisShardType(snapshot ProjectSnapshot, shard AnalysisShard) string {
	name := strings.ToLower(strings.TrimSpace(shard.Name))
	switch {
	case strings.TrimSpace(shard.CoverageGapID) != "":
		return "gap_filling"
	case strings.TrimSpace(shard.EvidenceRequestID) != "":
		return "root_cause_evidence"
	case shard.RefinementStage > 0:
		return "refinement"
	case containsAny(name, "startup", "trace", "flow"):
		return "runtime_flow"
	case containsAny(name, "security", "surface", "driver", "ioctl", "rpc", "validation", "integrity"):
		return "security_surface"
	case containsAny(name, "performance", "hot", "latency", "allocation"):
		return "performance"
	case containsAny(name, "docs", "manifest", "build", "project"):
		return "structure"
	default:
		mode := normalizeProjectAnalysisMode(snapshot.AnalysisMode)
		if mode != "" && mode != "map" {
			return mode
		}
		return "architecture"
	}
}

func analysisShardObjective(snapshot ProjectSnapshot, shard AnalysisShard, goal string) string {
	mode := firstNonBlankAnalysisString(normalizeProjectAnalysisMode(snapshot.AnalysisMode), defaultProjectAnalysisMode)
	scope := strings.TrimSpace(shard.Name)
	if scope == "" {
		scope = strings.TrimSpace(shard.ID)
	}
	switch strings.TrimSpace(shard.Type) {
	case "gap_filling":
		return fmt.Sprintf("Close coverage gap %s for %s by collecting missing source-grounded evidence.", firstNonBlankAnalysisString(shard.CoverageGapID, "gap"), scope)
	case "root_cause_evidence":
		return fmt.Sprintf("Inspect requested root-cause evidence for %s and prove or falsify the routed hypothesis.", scope)
	case "refinement":
		return fmt.Sprintf("Refine high-value shard %s into a narrower source-grounded analysis.", scope)
	}
	if strings.TrimSpace(goal) != "" {
		return fmt.Sprintf("Analyze %s for %s mode in support of: %s", scope, mode, compactPromptSection(goal, 180))
	}
	return fmt.Sprintf("Analyze %s for %s mode with concrete source anchors.", scope, mode)
}

func analysisShardRequiredEvidence(snapshot ProjectSnapshot, shard AnalysisShard) []string {
	required := []string{
		"direct facts from assigned primary files",
		"exact relative source anchors in evidence_files",
		"claim entries with source_anchors and confidence",
		"control or data flow when visible in assigned source",
	}
	switch normalizeProjectAnalysisMode(snapshot.AnalysisMode) {
	case "trace":
		required = append(required, "caller/callee or dispatch path evidence", "entry point evidence")
	case "impact":
		required = append(required, "upstream and downstream dependency evidence", "retest or blast-radius evidence")
	case "surface":
		required = append(required, "exposed entry surface evidence", "input shape or validation evidence")
	case "security":
		required = append(required, "trust boundary evidence", "privileged or tamper-sensitive path evidence", "validation or enforcement evidence")
	case "performance":
		required = append(required, "hot path or startup-cost evidence", "blocking, allocation, copy, or contention evidence")
	case "root-cause":
		required = append(required, "trigger-to-symptom causal evidence", "falsification or disconfirming evidence", "runtime observation needed to confirm the claim")
	default:
		required = append(required, "ownership and collaboration evidence")
	}
	if strings.TrimSpace(shard.CoverageGapID) != "" {
		required = append(required, "explicit answer to the routed coverage gap")
	}
	return analysisUniqueStrings(required)
}

func analysisShardSuccessCriteria(snapshot ProjectSnapshot, shard AnalysisShard) []string {
	criteria := []string{
		"All key claims cite files from the assigned primary or reference lists.",
		"Responsibilities, internal flow, collaboration, risks, and unknowns are concrete enough for synthesis.",
		"Unsupported speculation is placed in unknowns or low-confidence claims.",
	}
	switch normalizeProjectAnalysisMode(snapshot.AnalysisMode) {
	case "trace":
		criteria = append(criteria, "The report contains a stepwise runtime flow or explains why the flow is not visible.")
	case "impact":
		criteria = append(criteria, "The report identifies likely affected consumers and retest targets.")
	case "surface", "security":
		criteria = append(criteria, "The report names exposed or privileged surfaces with validation and trust-boundary evidence.")
	case "performance":
		criteria = append(criteria, "The report names plausible runtime cost centers and profiling priorities.")
	case "root-cause":
		criteria = append(criteria, "Every candidate is falsifiable and links state or input values to the reported symptom.")
	}
	if strings.TrimSpace(shard.CoverageGapID) != "" {
		criteria = append(criteria, "The coverage gap is closed or explicitly downgraded with a source-grounded reason.")
	}
	return analysisUniqueStrings(criteria)
}

func normalizeAnalysisClaims(claims []AnalysisClaim, report WorkerReport, shard AnalysisShard) []AnalysisClaim {
	out := []AnalysisClaim{}
	defaultAnchors := filterEvidence(firstNonEmpty(firstNonEmpty(report.EvidenceFiles, report.KeyFiles), shard.PrimaryFiles), shard)
	if len(defaultAnchors) == 0 {
		defaultAnchors = append([]string(nil), shard.PrimaryFiles...)
	}
	for _, claim := range claims {
		claim.Claim = strings.TrimSpace(claim.Claim)
		if claim.Claim == "" {
			continue
		}
		hadSourceAnchors := len(analysisUniqueStrings(claim.SourceAnchors)) > 0
		claim.ID = strings.TrimSpace(claim.ID)
		claim.Kind = normalizeAnalysisClaimKind(claim.Kind)
		claim.SourceAnchors = analysisUniqueStrings(filterEvidence(claim.SourceAnchors, shard))
		if len(claim.SourceAnchors) == 0 && !hadSourceAnchors {
			claim.SourceAnchors = append([]string(nil), defaultAnchors...)
		}
		claim.Confidence = normalizeAnalysisClaimConfidence(claim.Confidence)
		if len(claim.SourceAnchors) == 0 && hadSourceAnchors {
			claim.Confidence = "low"
		}
		claim.EvidencePacketIDs = analysisUniqueStrings(claim.EvidencePacketIDs)
		claim.DependsOn = analysisUniqueStrings(claim.DependsOn)
		claim.DisprovesWhen = strings.TrimSpace(claim.DisprovesWhen)
		claim.VerificationHint = strings.TrimSpace(claim.VerificationHint)
		if claim.DisprovesWhen == "" {
			claim.DisprovesWhen = defaultAnalysisClaimDisproof(claim.Kind)
		}
		if claim.VerificationHint == "" {
			claim.VerificationHint = "Re-check the cited source anchors and any referenced caller/callee context."
		}
		if len(claim.SourceAnchors) == 0 && hadSourceAnchors {
			claim.VerificationHint = strings.TrimSpace(claim.VerificationHint + " Provided source anchors were outside the assigned shard scope.")
		}
		out = append(out, claim)
	}
	if len(out) == 0 {
		out = deriveAnalysisClaimsFromReport(report, shard, defaultAnchors)
	}
	for i := range out {
		if strings.TrimSpace(out[i].ID) == "" {
			out[i].ID = fmt.Sprintf("claim-%02d", i+1)
		}
	}
	return limitAnalysisClaims(out, 10)
}

func deriveAnalysisClaimsFromReport(report WorkerReport, shard AnalysisShard, defaultAnchors []string) []AnalysisClaim {
	out := []AnalysisClaim{}
	add := func(kind string, confidence string, values []string) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			out = append(out, AnalysisClaim{
				Kind:              normalizeAnalysisClaimKind(kind),
				Claim:             value,
				SourceAnchors:     append([]string(nil), defaultAnchors...),
				EvidencePacketIDs: nil,
				Confidence:        normalizeAnalysisClaimConfidence(confidence),
				DisprovesWhen:     defaultAnalysisClaimDisproof(kind),
				VerificationHint:  "Re-check the cited source anchors and dependency context.",
			})
			if len(out) >= 8 {
				return
			}
		}
	}
	add("fact", "high", report.Facts)
	add("inference", "medium", report.Inferences)
	add("risk", "medium", report.Risks)
	add("unknown", "low", report.Unknowns)
	if len(out) == 0 && strings.TrimSpace(report.ScopeSummary) != "" {
		out = append(out, AnalysisClaim{
			Kind:              "summary",
			Claim:             strings.TrimSpace(report.ScopeSummary),
			SourceAnchors:     append([]string(nil), defaultAnchors...),
			EvidencePacketIDs: nil,
			Confidence:        "medium",
			DisprovesWhen:     "The assigned files show different ownership, flow, or evidence boundaries.",
			VerificationHint:  "Re-check the shard scope summary against cited source anchors.",
		})
	}
	return out
}

func normalizeAnalysisClaimKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "fact", "direct_fact":
		return "fact"
	case "inference", "interpretation":
		return "inference"
	case "risk", "security", "performance":
		return strings.ToLower(strings.TrimSpace(kind))
	case "unknown", "gap":
		return "unknown"
	case "summary":
		return "summary"
	default:
		if strings.TrimSpace(kind) == "" {
			return "claim"
		}
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func normalizeAnalysisClaimConfidence(confidence string) string {
	switch strings.ToLower(strings.TrimSpace(confidence)) {
	case "high", "medium", "low":
		return strings.ToLower(strings.TrimSpace(confidence))
	case "confirmed", "strong":
		return "high"
	case "weak", "uncertain", "unknown":
		return "low"
	default:
		return "medium"
	}
}

func defaultAnalysisClaimDisproof(kind string) string {
	switch normalizeAnalysisClaimKind(kind) {
	case "fact":
		return "The cited source anchor no longer contains this direct observation."
	case "risk", "security":
		return "The cited source or caller context proves the surface is guarded or unreachable."
	case "performance":
		return "Profiling or source flow shows this path is not hot, blocking, or allocation-heavy."
	case "unknown":
		return "The missing evidence is found in assigned source anchors."
	default:
		return "Counter-evidence in the assigned source anchors contradicts this claim."
	}
}

func limitAnalysisClaims(claims []AnalysisClaim, limit int) []AnalysisClaim {
	if limit <= 0 || len(claims) <= limit {
		return claims
	}
	return claims[:limit]
}

func buildAnalysisModeScorecard(snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, reviews []ReviewDecision, goal string, mode string) AnalysisModeScorecard {
	scorecard := AnalysisModeScorecard{
		GeneratedAt: time.Now(),
		Mode:        firstNonBlankAnalysisString(normalizeProjectAnalysisMode(mode), normalizeProjectAnalysisMode(snapshot.AnalysisMode)),
	}
	if scorecard.Mode == "" {
		scorecard.Mode = defaultProjectAnalysisMode
	}
	criteria := []AnalysisModeCriterion{}
	gaps := []AnalysisCoverageGap{}
	total := analysisMaxInt(len(shards), 1)
	approved := 0
	withEvidence := 0
	withClaims := 0
	withModeEvidence := 0
	for i, shard := range shards {
		report := WorkerReport{}
		if i < len(reports) {
			report = reports[i]
		}
		review := ReviewDecision{}
		if i < len(reviews) {
			review = reviews[i]
		}
		if strings.EqualFold(review.Status, "approved") {
			approved++
		}
		if len(report.EvidenceFiles) > 0 && len(report.KeyFiles) > 0 {
			withEvidence++
		} else {
			gaps = append(gaps, analysisCoverageGap("missing_evidence", "high", shard, "Shard report lacks concrete key_files or evidence_files.", shard.PrimaryFiles, analysisShardRequiredEvidence(snapshot, shard)))
		}
		if analysisReportHasSupportedClaims(report) {
			withClaims++
		} else {
			gaps = append(gaps, analysisCoverageGap("missing_claims", "medium", shard, "Shard report lacks claim-level source anchors.", shard.PrimaryFiles, []string{"claim entries with source_anchors and confidence"}))
		}
		if analysisReportSatisfiesModeEvidence(snapshot.AnalysisMode, report) {
			withModeEvidence++
		} else {
			gaps = append(gaps, analysisCoverageGap("mode_evidence_gap", analysisModeGapSeverity(snapshot.AnalysisMode), shard, analysisModeGapReason(snapshot.AnalysisMode), shard.PrimaryFiles, analysisModeRequiredEvidence(snapshot.AnalysisMode)))
		}
		if classifyReviewIssueKind(review) == analysisReviewIssueProvider {
			gaps = append(gaps, analysisCoverageGap("provider_failure", "high", shard, "Provider failure left this shard with reduced review confidence.", shard.PrimaryFiles, []string{"rerun this shard when the provider recovers"}))
		}
	}
	criteria = append(criteria, analysisPercentCriterion("review_approval", "Reviewer approval coverage", approved, total, []string{fmt.Sprintf("%d/%d shard reviews approved", approved, len(reviews))}, []string{"unapproved shards should be revised or manually verified"}))
	criteria = append(criteria, analysisPercentCriterion("source_evidence", "Source evidence coverage", withEvidence, total, []string{fmt.Sprintf("%d/%d reports include key files and evidence files", withEvidence, len(reports))}, []string{"every report should include key_files and evidence_files"}))
	criteria = append(criteria, analysisPercentCriterion("claim_contract", "Claim contract coverage", withClaims, total, []string{fmt.Sprintf("%d/%d reports include supported claims", withClaims, len(reports))}, []string{"every report should include claims with source_anchors"}))
	criteria = append(criteria, analysisPercentCriterion("mode_specific", "Mode-specific evidence coverage", withModeEvidence, total, []string{fmt.Sprintf("%d/%d reports satisfy %s evidence requirements", withModeEvidence, total, scorecard.Mode)}, analysisModeRequiredEvidence(snapshot.AnalysisMode)))
	score := 0
	for _, criterion := range criteria {
		score += criterion.Score
	}
	if len(criteria) > 0 {
		score = score / len(criteria)
	}
	scorecard.Score = score
	scorecard.Status = analysisScorecardStatus(score)
	scorecard.Criteria = criteria
	scorecard.CoverageGaps = normalizeAnalysisCoverageGaps(gaps, 16)
	scorecard.Summary = []string{
		fmt.Sprintf("mode=%s score=%d status=%s", scorecard.Mode, scorecard.Score, scorecard.Status),
		fmt.Sprintf("coverage_gaps=%d", len(scorecard.CoverageGaps)),
	}
	return scorecard
}

func analysisPercentCriterion(id string, title string, count int, total int, evidence []string, missing []string) AnalysisModeCriterion {
	if total < 1 {
		total = 1
	}
	score := count * 100 / total
	return AnalysisModeCriterion{
		ID:       id,
		Title:    title,
		Status:   analysisScorecardStatus(score),
		Score:    score,
		Evidence: evidence,
		Missing:  missing,
	}
}

func analysisScorecardStatus(score int) string {
	if score >= 80 {
		return "pass"
	}
	if score >= 55 {
		return "partial"
	}
	return "fail"
}

func analysisReportHasSupportedClaims(report WorkerReport) bool {
	for _, claim := range report.Claims {
		if strings.TrimSpace(claim.Claim) != "" && len(claim.SourceAnchors) > 0 {
			return true
		}
	}
	return false
}

func analysisReportSatisfiesModeEvidence(mode string, report WorkerReport) bool {
	switch normalizeProjectAnalysisMode(mode) {
	case "trace":
		return len(report.EntryPoints) > 0 && len(report.InternalFlow) > 0
	case "impact":
		return len(report.Dependencies)+len(report.Collaboration) > 0
	case "surface", "security":
		return len(report.EntryPoints)+len(report.Risks)+len(report.Dependencies) > 0
	case "performance":
		return len(report.InternalFlow)+len(report.Risks) > 0
	case "root-cause":
		return len(report.RootCauseCandidates) > 0 || len(report.Unknowns) > 0
	default:
		return len(report.Responsibilities) > 0 && len(report.Collaboration)+len(report.EntryPoints)+len(report.InternalFlow) > 0
	}
}

func analysisModeRequiredEvidence(mode string) []string {
	switch normalizeProjectAnalysisMode(mode) {
	case "trace":
		return []string{"entry_points", "internal_flow", "caller/callee or dispatch evidence"}
	case "impact":
		return []string{"dependencies", "collaboration", "blast radius or retest evidence"}
	case "surface":
		return []string{"entry_points", "exposed surface evidence", "validation evidence"}
	case "security":
		return []string{"trust boundary evidence", "privileged path evidence", "validation or enforcement evidence"}
	case "performance":
		return []string{"internal_flow", "hot path evidence", "blocking/allocation/contention evidence"}
	case "root-cause":
		return []string{"root_cause_candidates or explicit disconfirmation", "causal chain evidence", "falsification evidence"}
	default:
		return []string{"responsibilities", "ownership boundaries", "collaboration or entry point evidence"}
	}
}

func analysisModeGapSeverity(mode string) string {
	switch normalizeProjectAnalysisMode(mode) {
	case "root-cause", "security", "surface":
		return "high"
	default:
		return "medium"
	}
}

func analysisModeGapReason(mode string) string {
	switch normalizeProjectAnalysisMode(mode) {
	case "trace":
		return "Trace mode requires entry point and internal flow evidence."
	case "impact":
		return "Impact mode requires dependency, collaboration, or retest evidence."
	case "surface":
		return "Surface mode requires exposed entry surface and validation evidence."
	case "security":
		return "Security mode requires trust-boundary, privileged-path, validation, or enforcement evidence."
	case "performance":
		return "Performance mode requires flow and runtime cost evidence."
	case "root-cause":
		return "Root-cause mode requires a candidate, disconfirmation, or source-grounded unknown tied to the symptom."
	default:
		return "Map mode requires ownership, collaboration, or entry point evidence."
	}
}

func analysisCoverageGap(kind string, severity string, shard AnalysisShard, reason string, targetFiles []string, requiredEvidence []string) AnalysisCoverageGap {
	return AnalysisCoverageGap{
		ID:               "",
		Kind:             kind,
		Severity:         severity,
		ShardID:          shard.ID,
		ShardName:        shard.Name,
		Reason:           strings.TrimSpace(reason),
		TargetFiles:      limitStrings(analysisUniqueStrings(targetFiles), 12),
		RequiredEvidence: analysisUniqueStrings(requiredEvidence),
	}
}

func normalizeAnalysisCoverageGaps(gaps []AnalysisCoverageGap, limit int) []AnalysisCoverageGap {
	sort.SliceStable(gaps, func(i int, j int) bool {
		left := analysisGapSeverityRank(gaps[i].Severity)
		right := analysisGapSeverityRank(gaps[j].Severity)
		if left == right {
			if gaps[i].ShardID == gaps[j].ShardID {
				return gaps[i].Kind < gaps[j].Kind
			}
			return gaps[i].ShardID < gaps[j].ShardID
		}
		return left > right
	})
	out := []AnalysisCoverageGap{}
	seen := map[string]struct{}{}
	for _, gap := range gaps {
		key := gap.Kind + "|" + gap.ShardID + "|" + strings.Join(gap.TargetFiles, ",")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		gap.ID = fmt.Sprintf("gap-%02d", len(out)+1)
		out = append(out, gap)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func analysisGapSeverityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func (a *projectAnalyzer) planCoverageGapShards(snapshot ProjectSnapshot, existing []AnalysisShard, reports []WorkerReport, reviews []ReviewDecision, scorecard AnalysisModeScorecard, limit int) []AnalysisShard {
	if a == nil || limit <= 0 || len(scorecard.CoverageGaps) == 0 {
		return nil
	}
	out := []AnalysisShard{}
	seenTargets := map[string]struct{}{}
	providerFailedShards := map[string]struct{}{}
	for index, review := range reviews {
		if index >= len(existing) {
			continue
		}
		if classifyReviewIssueKind(review) == analysisReviewIssueProvider {
			providerFailedShards[existing[index].ID] = struct{}{}
		}
	}
	nextID := len(existing) + 1
	maxFiles := a.analysisCfg.MaxFilesPerShard
	if maxFiles <= 0 || maxFiles > 8 {
		maxFiles = 8
	}
	maxLines := a.analysisCfg.MaxLinesPerShard
	if maxLines <= 0 || maxLines > 2500 {
		maxLines = 2500
	}
	for _, gap := range scorecard.CoverageGaps {
		if len(out) >= limit {
			break
		}
		if strings.EqualFold(gap.Kind, "provider_failure") {
			continue
		}
		if _, providerFailed := providerFailedShards[gap.ShardID]; providerFailed {
			continue
		}
		files := analysisFilesFromPaths(snapshot, gap.TargetFiles)
		if len(files) == 0 {
			continue
		}
		chunks := chunkFiles(files, maxFiles, maxLines)
		if len(chunks) == 0 {
			continue
		}
		chunk := chunks[0]
		paths := filesToPaths(chunk)
		key := strings.Join(paths, "|") + "|" + gap.Kind
		if _, ok := seenTargets[key]; ok {
			continue
		}
		seenTargets[key] = struct{}{}
		name := "gap_" + sanitizeFileName(firstNonBlankAnalysisString(gap.Kind, gap.ID))
		if name == "gap_" {
			name = "gap_coverage"
		}
		shard := AnalysisShard{
			ID:               fmt.Sprintf("shard-%02d", nextID),
			Name:             name,
			Type:             "gap_filling",
			CoverageGapID:    gap.ID,
			PrimaryFiles:     paths,
			EstimatedFiles:   len(chunk),
			EstimatedLines:   sumLines(chunk),
			Objective:        fmt.Sprintf("Close %s for %s: %s", gap.Kind, firstNonBlankAnalysisString(gap.ShardName, gap.ShardID), gap.Reason),
			RequiredEvidence: append([]string(nil), gap.RequiredEvidence...),
			SuccessCriteria: []string{
				"The routed coverage gap is answered with source-grounded claims.",
				"The report either supplies the missing evidence or explains why assigned source disproves the gap.",
			},
			InvalidationReason: "coverage_gap",
		}
		a.finalizeShard(snapshot, &shard, 8)
		annotateAnalysisShardContract(snapshot, &shard, "")
		out = append(out, shard)
		nextID++
	}
	return out
}

func analysisFilesFromPaths(snapshot ProjectSnapshot, paths []string) []ScannedFile {
	out := []ScannedFile{}
	seen := map[string]struct{}{}
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		file, ok := snapshot.FilesByPath[path]
		if !ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, file)
	}
	sort.SliceStable(out, func(i int, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out
}

func refineSemanticInvalidation(shard *AnalysisShard) {
	if shard == nil {
		return
	}
	reason := strings.ToLower(strings.TrimSpace(shard.InvalidationReason))
	if reason == "" {
		return
	}
	class, signals := classifyInvalidationChanges(shard.InvalidationChanges, *shard)
	if class != "" {
		shard.InvalidationClass = class
	}
	if len(signals) > 0 {
		shard.InvalidationSignals = analysisUniqueStrings(append(shard.InvalidationSignals, signals...))
	}
	if !strings.Contains(reason, "semantic") || class == "" {
		return
	}
	switch class {
	case "network_contract":
		shard.InvalidationReason = "semantic_network_contract_changed"
	case "security_contract":
		shard.InvalidationReason = "semantic_security_contract_changed"
	case "build_startup_contract":
		shard.InvalidationReason = "semantic_build_startup_changed"
	case "asset_config_contract":
		shard.InvalidationReason = "semantic_asset_config_changed"
	case "runtime_flow_contract":
		shard.InvalidationReason = "semantic_runtime_flow_changed"
	}
}

func classifyInvalidationChanges(changes []InvalidationChange, shard AnalysisShard) (string, []string) {
	signals := []string{}
	add := func(items ...string) {
		for _, item := range items {
			if strings.TrimSpace(item) != "" {
				signals = append(signals, strings.TrimSpace(item))
			}
		}
	}
	joined := strings.ToLower(shard.Name + " " + strings.Join(shard.PrimaryFiles, " "))
	for _, change := range changes {
		joined += " " + strings.ToLower(change.Kind+" "+change.Scope+" "+change.Owner+" "+change.Subject+" "+change.Source)
		add(change.Kind)
	}
	switch {
	case containsAny(joined, "rpc", "replication", "replicated", "authority", "network"):
		return "network_contract", analysisUniqueStrings(signals)
	case containsAny(joined, "security", "trust", "validation", "tamper", "ioctl", "privilege", "handle"):
		return "security_contract", analysisUniqueStrings(signals)
	case containsAny(joined, "startup", "bootstrap", "build", "target", "module", "plugin", "manifest"):
		return "build_startup_contract", analysisUniqueStrings(signals)
	case containsAny(joined, "asset", "config", "default_map", "game_mode", "ini"):
		return "asset_config_contract", analysisUniqueStrings(signals)
	case containsAny(joined, "runtime", "flow", "call", "symbol", "function", "import", "dependency"):
		return "runtime_flow_contract", analysisUniqueStrings(signals)
	default:
		return "", analysisUniqueStrings(signals)
	}
}

func writeAnalysisClaims(b *strings.Builder, title string, claims []AnalysisClaim) {
	if b == nil || len(claims) == 0 {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Claims"
	}
	fmt.Fprintf(b, "## %s\n\n", title)
	for _, claim := range claims {
		text := strings.TrimSpace(claim.Claim)
		if text == "" {
			continue
		}
		fmt.Fprintf(b, "- %s", text)
		parts := []string{}
		if strings.TrimSpace(claim.Kind) != "" {
			parts = append(parts, "kind="+claim.Kind)
		}
		if strings.TrimSpace(claim.Confidence) != "" {
			parts = append(parts, "confidence="+claim.Confidence)
		}
		if len(claim.SourceAnchors) > 0 {
			parts = append(parts, "sources="+strings.Join(limitStrings(claim.SourceAnchors, 4), ", "))
		}
		if strings.TrimSpace(claim.DisprovesWhen) != "" {
			parts = append(parts, "disproves_when="+claim.DisprovesWhen)
		}
		if len(parts) > 0 {
			fmt.Fprintf(b, " (%s)", strings.Join(parts, "; "))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}
