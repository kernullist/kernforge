package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	analysisFreshnessFresh    = "fresh"
	analysisFreshnessSuspect  = "suspect"
	analysisFreshnessStale    = "stale"
	analysisFreshnessUnusable = "unusable"
)

type analysisFreshnessReport struct {
	Status               string
	Action               string
	GeneratedAt          time.Time
	Age                  time.Duration
	RunID                string
	ChangedFiles         []string
	OverlapFiles         []string
	CriticalChangedFiles []string
	StaleMarkers         []string
	Reasons              []string
	GitBranch            string
	GitHead              string
}

func evaluateLatestAnalysisFreshness(cfg Config, root string, artifacts latestAnalysisArtifacts, query string) analysisFreshnessReport {
	analysisCfg := configProjectAnalysis(cfg, root)
	report := analysisFreshnessReport{
		Status:      analysisFreshnessFresh,
		Action:      "use",
		GeneratedAt: latestAnalysisGeneratedAt(artifacts),
		RunID:       latestAnalysisArtifactsRunID(artifacts),
	}
	if report.GeneratedAt.IsZero() {
		report.Status = analysisFreshnessSuspect
		report.Action = "use_with_verification"
		report.Reasons = append(report.Reasons, "missing_generated_at")
	} else {
		report.Age = time.Since(report.GeneratedAt)
		if report.Age < 0 {
			report.Age = 0
		}
		freshTTL := time.Duration(analysisCfg.FreshnessFreshHours) * time.Hour
		staleTTL := time.Duration(analysisCfg.FreshnessStaleHours) * time.Hour
		switch {
		case staleTTL > 0 && report.Age > staleTTL:
			report.Status = analysisFreshnessStale
			report.Action = "refresh_recommended"
			report.Reasons = append(report.Reasons, "age_exceeds_stale_ttl")
		case freshTTL > 0 && report.Age > freshTTL:
			report.Status = analysisFreshnessSuspect
			report.Action = "use_with_verification"
			report.Reasons = append(report.Reasons, "age_exceeds_fresh_ttl")
		}
	}
	if artifacts.RunSummary.ParseFailedShards > 0 || artifacts.RunSummary.ProviderFailedShards > 0 {
		report = escalateAnalysisFreshness(report, analysisFreshnessUnusable, "refresh_required", "previous_run_incomplete")
	}
	if artifacts.RunSummary.VerifierBlockingIssues > 0 || artifacts.ClaimVerification.BlockingCount > 0 {
		report = escalateAnalysisFreshness(report, analysisFreshnessUnusable, "refresh_required", "verifier_blockers")
	}
	if strings.EqualFold(strings.TrimSpace(artifacts.RunSummary.Status), "draft") ||
		strings.Contains(strings.ToLower(strings.TrimSpace(artifacts.RunSummary.Status)), "blocker") {
		report = escalateAnalysisFreshness(report, analysisFreshnessUnusable, "refresh_required", "analysis_status_not_reusable")
	}
	staleMarkers := latestAnalysisRealStaleMarkers(artifacts)
	report.StaleMarkers = staleMarkers
	if containsCurrentSourceNeededMarker(staleMarkers) {
		report = escalateAnalysisFreshness(report, analysisFreshnessUnusable, "refresh_required", "current_source_needed")
	} else if len(staleMarkers) > 0 {
		report = escalateAnalysisFreshness(report, analysisFreshnessSuspect, "use_with_verification", "stale_markers_present")
	}
	changed, branch, head := collectLatestAnalysisFreshnessGitState(root)
	report.ChangedFiles = changed
	report.GitBranch = branch
	report.GitHead = head
	if len(changed) > 0 {
		report = escalateAnalysisFreshness(report, analysisFreshnessSuspect, "use_with_verification", "workspace_has_changed_files")
	}
	if maxChanged := analysisCfg.FreshnessMaxChangedFiles; maxChanged > 0 && len(changed) > maxChanged {
		report = escalateAnalysisFreshness(report, analysisFreshnessStale, "refresh_recommended", "changed_file_count_exceeds_limit")
	}
	sourceAnchors := latestAnalysisFreshnessSourceAnchors(artifacts, query)
	report.OverlapFiles = overlapAnalysisFreshnessFiles(changed, sourceAnchors)
	report.CriticalChangedFiles = criticalAnalysisFreshnessChangedFiles(changed)
	if len(report.CriticalChangedFiles) > 0 {
		report = escalateAnalysisFreshness(report, analysisFreshnessStale, "refresh_recommended", "critical_project_metadata_changed")
	}
	if len(report.OverlapFiles) > analysisCfg.FreshnessMaxOverlapFiles {
		if looksLikeHighRiskAnalysisFreshnessQuery(query) {
			report = escalateAnalysisFreshness(report, analysisFreshnessStale, "refresh_recommended", "relevant_analysis_files_changed")
		} else {
			report = escalateAnalysisFreshness(report, analysisFreshnessSuspect, "use_with_verification", "relevant_analysis_files_changed")
		}
	}
	if len(sourceAnchors) == 0 && strings.TrimSpace(artifacts.Pack.ProjectSummary) == "" {
		report = escalateAnalysisFreshness(report, analysisFreshnessUnusable, "refresh_required", "missing_reusable_source_anchors")
	}
	return report
}

func latestAnalysisGeneratedAt(artifacts latestAnalysisArtifacts) time.Time {
	for _, candidate := range []time.Time{
		artifacts.DocsManifest.GeneratedAt,
		artifacts.RunSummary.CompletedAt,
		artifacts.RunSummary.StartedAt,
		artifacts.Pack.GeneratedAt,
		artifacts.Snapshot.GeneratedAt,
		artifacts.Corpus.GeneratedAt,
		artifacts.Index.GeneratedAt,
		artifacts.IndexV2.GeneratedAt,
	} {
		if !candidate.IsZero() {
			return candidate.UTC()
		}
	}
	return time.Time{}
}

func analysisFreshnessAllowsContext(report analysisFreshnessReport) bool {
	switch strings.TrimSpace(report.Status) {
	case analysisFreshnessFresh, analysisFreshnessSuspect:
		return true
	default:
		return false
	}
}

func analysisFreshnessPromptBlock(report analysisFreshnessReport) string {
	status := strings.TrimSpace(report.Status)
	if status == "" {
		return ""
	}
	parts := []string{"Analysis cache freshness: " + status}
	if strings.TrimSpace(report.Action) != "" {
		parts = append(parts, "action="+strings.TrimSpace(report.Action))
	}
	if !report.GeneratedAt.IsZero() {
		parts = append(parts, "age="+formatAnalysisFreshnessAge(report.Age))
	}
	if len(report.Reasons) > 0 {
		parts = append(parts, "reasons="+strings.Join(limitStrings(report.Reasons, 4), ","))
	}
	if len(report.OverlapFiles) > 0 {
		parts = append(parts, "changed_relevant_files="+strings.Join(limitStrings(report.OverlapFiles, 4), "; "))
	}
	if len(report.CriticalChangedFiles) > 0 {
		parts = append(parts, "critical_changed="+strings.Join(limitStrings(report.CriticalChangedFiles, 3), "; "))
	}
	switch status {
	case analysisFreshnessSuspect:
		parts = append(parts, "Use cached structure only as a starting point; re-read changed or target files before edits or high-risk claims.")
	case analysisFreshnessStale, analysisFreshnessUnusable:
		parts = append(parts, "Do not rely on cached structure until analyze-project is refreshed or target files are re-read.")
	}
	return strings.Join(parts, " | ")
}

func formatAnalysisFreshnessProgressParts(report analysisFreshnessReport) []string {
	parts := []string{}
	if strings.TrimSpace(report.Status) != "" {
		parts = append(parts, "freshness="+strings.TrimSpace(report.Status))
	}
	if strings.TrimSpace(report.Action) != "" {
		parts = append(parts, "action="+strings.TrimSpace(report.Action))
	}
	if !report.GeneratedAt.IsZero() {
		parts = append(parts, "age="+formatAnalysisFreshnessAge(report.Age))
	}
	if len(report.ChangedFiles) > 0 {
		parts = append(parts, fmt.Sprintf("changed=%d", len(report.ChangedFiles)))
	}
	if len(report.OverlapFiles) > 0 {
		parts = append(parts, fmt.Sprintf("overlap=%d", len(report.OverlapFiles)))
	}
	if len(report.Reasons) > 0 {
		parts = append(parts, "reasons="+strings.Join(limitStrings(report.Reasons, 3), ","))
	}
	return parts
}

func formatAnalysisFreshnessAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	if age < time.Hour {
		return age.Round(time.Minute).String()
	}
	if age < 48*time.Hour {
		return age.Round(time.Hour).String()
	}
	days := int(age.Hours() / 24)
	if days <= 0 {
		days = 1
	}
	return fmt.Sprintf("%dd", days)
}

func collectLatestAnalysisFreshnessGitState(root string) ([]string, string, string) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, "", ""
	}
	gitRoot := findGitProjectRoot(root)
	if strings.TrimSpace(gitRoot) == "" {
		return nil, "", ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	changed, _ := gitChangedFiles(ctx, gitRoot)
	branch := ""
	if text, err := runGitHelperCommand(ctx, gitRoot, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		branch = text
	}
	head := ""
	if text, err := runGitHelperCommand(ctx, gitRoot, "rev-parse", "--short", "HEAD"); err == nil {
		head = text
	}
	return analysisUniqueStrings(analysisDocSlashPaths(changed)), strings.TrimSpace(branch), strings.TrimSpace(head)
}

func latestAnalysisFreshnessSourceAnchors(artifacts latestAnalysisArtifacts, query string) []string {
	items := []string{}
	items = append(items, artifacts.Pack.TopImportantFiles...)
	items = append(items, artifacts.Pack.ManifestFiles...)
	items = append(items, artifacts.Pack.EntrypointFiles...)
	items = append(items, artifacts.Pack.HighRiskFiles...)
	items = append(items, artifacts.Pack.StartupEntryFiles...)
	for _, subsystem := range selectRelevantKnowledgeSubsystems(artifacts.Pack, query, 8) {
		items = append(items, subsystem.KeyFiles...)
		items = append(items, subsystem.EvidenceFiles...)
		items = append(items, subsystem.EntryPoints...)
		items = append(items, subsystem.Dependencies...)
	}
	for _, doc := range selectRelevantVectorDocuments(artifacts.Corpus, query, 4) {
		items = append(items, doc.PathHint)
	}
	for _, file := range selectRelevantIndexedFiles(artifacts.Index, query, 6) {
		items = append(items, file.Path)
	}
	v2Hits := collectRelevantSemanticIndexV2Hits(artifacts.IndexV2, query)
	for _, file := range v2Hits.Files {
		items = append(items, file.Path)
	}
	for _, symbol := range v2Hits.Symbols {
		items = append(items, symbol.File)
	}
	for _, edge := range v2Hits.Overlays {
		items = append(items, edge.Evidence...)
	}
	for _, doc := range limitScoredAnalysisDocs(scoreRelevantAnalysisDocs(artifacts.DocsManifest, query), 4) {
		items = append(items, doc.doc.SourceAnchors...)
	}
	for _, file := range artifacts.Snapshot.ManifestFiles {
		items = append(items, file)
	}
	for _, file := range artifacts.Snapshot.EntrypointFiles {
		items = append(items, file)
	}
	return normalizeAnalysisFreshnessFiles(items)
}

func overlapAnalysisFreshnessFiles(changed []string, anchors []string) []string {
	anchorSet := map[string]struct{}{}
	for _, item := range normalizeAnalysisFreshnessFiles(anchors) {
		anchorSet[strings.ToLower(item)] = struct{}{}
	}
	out := []string{}
	for _, item := range normalizeAnalysisFreshnessFiles(changed) {
		lower := strings.ToLower(item)
		if _, ok := anchorSet[lower]; ok {
			out = append(out, item)
			continue
		}
		for anchor := range anchorSet {
			if strings.HasPrefix(lower, anchor+"/") || strings.HasPrefix(anchor, lower+"/") {
				out = append(out, item)
				break
			}
		}
	}
	return analysisUniqueStrings(out)
}

func criticalAnalysisFreshnessChangedFiles(changed []string) []string {
	out := []string{}
	for _, item := range normalizeAnalysisFreshnessFiles(changed) {
		lower := strings.ToLower(filepath.ToSlash(item))
		base := strings.ToLower(filepath.Base(lower))
		if base == "go.mod" ||
			base == "go.sum" ||
			base == "package.json" ||
			base == "package-lock.json" ||
			base == "pnpm-lock.yaml" ||
			base == "yarn.lock" ||
			base == "cargo.toml" ||
			base == "cargo.lock" ||
			base == "cmakelists.txt" ||
			base == "compile_commands.json" ||
			strings.HasSuffix(lower, ".sln") ||
			strings.HasSuffix(lower, ".vcxproj") ||
			strings.HasSuffix(lower, ".props") ||
			strings.HasSuffix(lower, ".targets") ||
			strings.HasSuffix(lower, ".uproject") ||
			strings.HasSuffix(lower, ".uplugin") ||
			strings.HasSuffix(lower, ".build.cs") ||
			strings.HasSuffix(lower, ".target.cs") ||
			strings.HasSuffix(lower, ".ini") {
			out = append(out, item)
		}
	}
	return analysisUniqueStrings(out)
}

func normalizeAnalysisFreshnessFiles(items []string) []string {
	out := []string{}
	for _, item := range items {
		path := analysisContextProgressFilePath(item)
		if path == "" {
			path = cleanEvidencePath(item)
		}
		path = strings.Trim(strings.TrimSpace(filepath.ToSlash(path)), "/")
		if path == "" || path == "." {
			continue
		}
		out = append(out, path)
	}
	return analysisUniqueStrings(out)
}

func latestAnalysisRealStaleMarkers(artifacts latestAnalysisArtifacts) []string {
	items := []string{}
	items = append(items, artifacts.Pack.AnalysisExecution.InvalidationReasons...)
	items = append(items, artifacts.Pack.AnalysisExecution.SemanticInvalidationReasons...)
	items = append(items, artifacts.Pack.AnalysisExecution.TopChangeClasses...)
	for _, subsystem := range artifacts.Pack.Subsystems {
		items = append(items, subsystem.InvalidationReasons...)
	}
	for _, doc := range artifacts.DocsManifest.Documents {
		items = append(items, doc.StaleMarkers...)
		for _, section := range doc.Sections {
			items = append(items, section.StaleMarkers...)
		}
	}
	return analysisRealStaleMarkers(items)
}

func containsCurrentSourceNeededMarker(markers []string) bool {
	for _, marker := range markers {
		if containsAny(strings.ToLower(strings.TrimSpace(marker)), "current_source_needed", "current source needed", "refresh required", "rerun required") {
			return true
		}
	}
	return false
}

func looksLikeHighRiskAnalysisFreshnessQuery(query string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(query)))
	intent := classifyTurnIntent(lower)
	if intent == TurnIntentEditCode || intent == TurnIntentRunCommand || intent == TurnIntentReviewCode {
		return true
	}
	return looksLikeActionOrToolIntent(lower) ||
		containsAny(lower, "구현", "수정", "고쳐", "패치", "커밋", "푸시", "implement", "fix", "patch", "commit", "push", "security", "보안", "trust boundary", "ioctl")
}

func escalateAnalysisFreshness(report analysisFreshnessReport, status string, action string, reason string) analysisFreshnessReport {
	if analysisFreshnessRank(status) > analysisFreshnessRank(report.Status) {
		report.Status = status
		report.Action = action
	} else if strings.TrimSpace(report.Action) == "" {
		report.Action = action
	}
	if strings.TrimSpace(reason) != "" {
		report.Reasons = analysisUniqueStrings(append(report.Reasons, reason))
	}
	return report
}

func analysisFreshnessRank(status string) int {
	switch strings.TrimSpace(status) {
	case analysisFreshnessFresh:
		return 0
	case analysisFreshnessSuspect:
		return 1
	case analysisFreshnessStale:
		return 2
	case analysisFreshnessUnusable:
		return 3
	default:
		return 1
	}
}
