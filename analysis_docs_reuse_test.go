package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildAnalysisDocsEvidenceRecordCapturesReusableArtifacts(t *testing.T) {
	generatedAt := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:       "20260424-120000",
			Goal:        "surface docs",
			Mode:        "surface",
			CompletedAt: generatedAt,
		},
	}
	manifest := AnalysisDocsManifest{
		RunID:         run.Summary.RunID,
		Mode:          run.Summary.Mode,
		GeneratedAt:   generatedAt,
		DocumentCount: 3,
		ReuseTargets:  []string{"evidence", "verification_planner"},
		FuzzTargets: []AnalysisFuzzTargetCatalogEntry{
			{Name: "ParsePacket", File: "src/parser.cpp"},
		},
		VerificationMatrix: []AnalysisVerificationMatrixEntry{
			{ChangeArea: "parser", RequiredVerification: "go test ./..."},
		},
	}

	record := buildAnalysisDocsEvidenceRecord(run, manifest, `C:\repo`, "session-1", `C:\repo\.kernforge\analysis\latest`, `C:\repo\.kernforge\analysis\latest\docs`)

	if record.Kind != "analysis_docs" {
		t.Fatalf("expected analysis_docs kind, got %q", record.Kind)
	}
	if record.Attributes["docs_manifest"] == "" || !strings.Contains(record.Attributes["docs_manifest"], "docs_manifest.json") {
		t.Fatalf("expected docs manifest attribute, got %#v", record.Attributes)
	}
	if record.Attributes["fuzz_target_count"] != "1" {
		t.Fatalf("expected fuzz target count, got %q", record.Attributes["fuzz_target_count"])
	}
	if record.Attributes["verification_matrix_count"] != "1" {
		t.Fatalf("expected verification matrix count, got %q", record.Attributes["verification_matrix_count"])
	}
	if !sliceContainsFold(record.Tags, "verification_planner") {
		t.Fatalf("expected reuse target tag, got %#v", record.Tags)
	}
}

func TestBuildAnalysisDocsMemoryRecordPromotesKnowledgeBaseArtifacts(t *testing.T) {
	generatedAt := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:       "20260424-120000",
			Goal:        "document project",
			Mode:        "full",
			CompletedAt: generatedAt,
		},
	}
	manifest := AnalysisDocsManifest{
		RunID:           run.Summary.RunID,
		GeneratedAt:     generatedAt,
		DocumentCount:   2,
		SourceArtifacts: []string{"src/main.cpp"},
		Documents: []AnalysisGeneratedDoc{
			{
				Kind:          "architecture",
				Path:          "ARCHITECTURE.md",
				SourceAnchors: []string{"src/main.cpp"},
				ReuseTargets:  []string{"memory"},
			},
		},
	}

	latestDir := filepath.Join(`C:\repo`, ".kernforge", "analysis", "latest")
	record := buildAnalysisDocsMemoryRecord(run, manifest, `C:\repo`, "session-1", "session", "provider", "model", latestDir, filepath.Join(latestDir, "docs"))

	if record.Importance != PersistentMemoryHigh {
		t.Fatalf("expected high importance, got %q", record.Importance)
	}
	if !sliceContainsFold(record.VerificationArtifacts, filepath.ToSlash(filepath.Join(latestDir, "docs_manifest.json"))) {
		t.Fatalf("expected docs manifest artifact, got %#v", record.VerificationArtifacts)
	}
	if !sliceContainsFold(record.Files, "src/main.cpp") {
		t.Fatalf("expected source anchor in memory files, got %#v", record.Files)
	}
	if !sliceContainsFold(record.Keywords, "project-knowledge-base") {
		t.Fatalf("expected project knowledge keyword, got %#v", record.Keywords)
	}
}
