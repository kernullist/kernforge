package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func (rt *runtimeState) recordLatestAnalysisDocsArtifacts(run ProjectAnalysisRun, manifest AnalysisDocsManifest, outputDir string) error {
	if rt == nil {
		return nil
	}
	workspace := strings.TrimSpace(rt.workspace.BaseRoot)
	if workspace == "" {
		workspace = strings.TrimSpace(rt.workspace.Root)
	}
	if workspace == "" && rt.session != nil {
		workspace = strings.TrimSpace(rt.session.WorkingDir)
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	latestDir := filepath.Join(outputDir, "latest")
	latestDocsDir := filepath.Join(latestDir, "docs")
	sessionID := ""
	sessionName := ""
	provider := ""
	model := ""
	if rt.session != nil {
		sessionID = rt.session.ID
		sessionName = rt.session.Name
		provider = rt.session.Provider
		model = rt.session.Model
	}
	var firstErr error
	if rt.evidence != nil {
		record := buildAnalysisDocsEvidenceRecord(run, manifest, workspace, sessionID, latestDir, latestDocsDir)
		if err := rt.evidence.Append(record); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if rt.longMem != nil {
		record := buildAnalysisDocsMemoryRecord(run, manifest, workspace, sessionID, sessionName, provider, model, latestDir, latestDocsDir)
		if err := rt.longMem.Append(record); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func buildAnalysisDocsEvidenceRecord(run ProjectAnalysisRun, manifest AnalysisDocsManifest, workspace string, sessionID string, latestDir string, latestDocsDir string) EvidenceRecord {
	generatedAt := analysisDocsRecordTime(run, manifest)
	manifestPath := filepath.Join(latestDir, "docs_manifest.json")
	attrs := map[string]string{
		"run_id":                    run.Summary.RunID,
		"mode":                      run.Summary.Mode,
		"docs_manifest":             filepath.ToSlash(manifestPath),
		"docs_index":                filepath.ToSlash(filepath.Join(latestDir, "docs_index.md")),
		"dashboard":                 filepath.ToSlash(filepath.Join(latestDir, "dashboard.html")),
		"docs_dir":                  filepath.ToSlash(latestDocsDir),
		"document_count":            fmt.Sprintf("%d", manifest.DocumentCount),
		"fuzz_target_count":         fmt.Sprintf("%d", len(manifest.FuzzTargets)),
		"verification_matrix_count": fmt.Sprintf("%d", len(manifest.VerificationMatrix)),
	}
	if strings.TrimSpace(run.Summary.Goal) != "" {
		attrs["goal"] = run.Summary.Goal
	}
	return EvidenceRecord{
		SessionID:           sessionID,
		Workspace:           workspace,
		CreatedAt:           generatedAt,
		Kind:                "analysis_docs",
		Category:            "project-analysis",
		Subject:             filepath.ToSlash(manifestPath),
		Outcome:             "generated",
		Severity:            "low",
		Confidence:          analysisRunConfidence(run),
		SignalClass:         "documentation",
		RiskScore:           5,
		SeverityReasons:     analysisDocsRecordStaleReasons(run, manifest),
		VerificationSummary: analysisDocsRecordSummary(run, manifest),
		Tags:                analysisDocsRecordTags(run, manifest),
		Attributes:          attrs,
	}
}

func buildAnalysisDocsMemoryRecord(run ProjectAnalysisRun, manifest AnalysisDocsManifest, workspace string, sessionID string, sessionName string, provider string, model string, latestDir string, latestDocsDir string) PersistentMemoryRecord {
	generatedAt := analysisDocsRecordTime(run, manifest)
	artifacts := analysisDocsRecordArtifacts(latestDir, latestDocsDir, manifest)
	files := analysisDocsRecordFiles(latestDir, latestDocsDir, manifest)
	summary := analysisDocsRecordSummary(run, manifest)
	return PersistentMemoryRecord{
		ID:                     fmt.Sprintf("analysis-docs-%s", strings.TrimSpace(run.Summary.RunID)),
		SessionID:              sessionID,
		SessionName:            sessionName,
		Provider:               provider,
		Model:                  model,
		Workspace:              workspace,
		CreatedAt:              generatedAt,
		Request:                strings.TrimSpace("/analyze-project " + strings.TrimSpace(run.Summary.Goal)),
		Reply:                  summary,
		Summary:                summary,
		Importance:             PersistentMemoryHigh,
		Trust:                  analysisDocsMemoryTrust(run),
		VerificationSummary:    summary,
		VerificationCategories: []string{"project-analysis", "documentation"},
		VerificationTags:       analysisDocsRecordTags(run, manifest),
		VerificationArtifacts:  artifacts,
		Files:                  files,
		Keywords:               analysisDocsRecordKeywords(run, manifest),
	}
}

func analysisDocsRecordTime(run ProjectAnalysisRun, manifest AnalysisDocsManifest) time.Time {
	if !manifest.GeneratedAt.IsZero() {
		return manifest.GeneratedAt.UTC()
	}
	if t := analysisDocsGeneratedAt(run); !t.IsZero() {
		return t.UTC()
	}
	return time.Now().UTC()
}

func analysisDocsMemoryTrust(run ProjectAnalysisRun) PersistentMemoryTrust {
	switch strings.ToLower(strings.TrimSpace(analysisRunConfidence(run))) {
	case "high", "medium":
		return PersistentMemoryConfirmed
	default:
		return PersistentMemoryTentative
	}
}

func analysisDocsRecordSummary(run ProjectAnalysisRun, manifest AnalysisDocsManifest) string {
	runID := firstNonBlankAnalysisString(run.Summary.RunID, manifest.RunID)
	if strings.TrimSpace(runID) == "" {
		runID = "unknown"
	}
	return fmt.Sprintf("Project knowledge base generated for run %s: docs=%d, fuzz_targets=%d, verification_checks=%d.",
		runID,
		manifest.DocumentCount,
		len(manifest.FuzzTargets),
		len(manifest.VerificationMatrix))
}

func analysisDocsRecordStaleReasons(run ProjectAnalysisRun, manifest AnalysisDocsManifest) []string {
	reasons := analysisRunStaleMarkers(run)
	for _, doc := range manifest.Documents {
		reasons = append(reasons, doc.StaleMarkers...)
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "refresh docs after source, build graph, or generated manifest changes")
	}
	return analysisUniqueStrings(reasons)
}

func analysisDocsRecordTags(run ProjectAnalysisRun, manifest AnalysisDocsManifest) []string {
	tags := []string{"analysis", "analyze-project", "generated-docs", "project-knowledge-base"}
	if strings.TrimSpace(run.Summary.Mode) != "" {
		tags = append(tags, "mode:"+strings.ToLower(strings.TrimSpace(run.Summary.Mode)))
	}
	tags = append(tags, manifest.ReuseTargets...)
	return analysisUniqueStrings(tags)
}

func analysisDocsRecordKeywords(run ProjectAnalysisRun, manifest AnalysisDocsManifest) []string {
	keywords := append([]string{}, analysisDocsRecordTags(run, manifest)...)
	for _, doc := range manifest.Documents {
		keywords = append(keywords, strings.TrimSuffix(doc.Path, filepath.Ext(doc.Path)), doc.Kind)
		keywords = append(keywords, doc.ReuseTargets...)
	}
	for _, target := range manifest.FuzzTargets {
		keywords = append(keywords, target.Name, target.File, target.InputSurfaceKind)
		keywords = append(keywords, target.PriorityReasons...)
	}
	for _, entry := range manifest.VerificationMatrix {
		keywords = append(keywords, entry.ChangeArea, entry.RequiredVerification, entry.OptionalVerification, entry.EvidenceHook)
	}
	return analysisUniqueStrings(keywords)
}

func analysisDocsRecordArtifacts(latestDir string, latestDocsDir string, manifest AnalysisDocsManifest) []string {
	artifacts := []string{
		filepath.ToSlash(filepath.Join(latestDir, "docs_manifest.json")),
		filepath.ToSlash(filepath.Join(latestDir, "docs_index.md")),
		filepath.ToSlash(filepath.Join(latestDir, "dashboard.html")),
	}
	for _, doc := range manifest.Documents {
		if strings.TrimSpace(doc.Path) != "" {
			artifacts = append(artifacts, filepath.ToSlash(filepath.Join(latestDocsDir, filepath.FromSlash(doc.Path))))
		}
	}
	return analysisUniqueStrings(artifacts)
}

func analysisDocsRecordFiles(latestDir string, latestDocsDir string, manifest AnalysisDocsManifest) []string {
	files := analysisDocsRecordArtifacts(latestDir, latestDocsDir, manifest)
	files = append(files, manifest.SourceArtifacts...)
	for _, doc := range manifest.Documents {
		files = append(files, doc.SourceAnchors...)
	}
	return analysisUniqueStrings(files)
}
