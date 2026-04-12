package main

import (
	"strings"
	"testing"
	"time"
)

func TestRenderRelevantProjectAnalysisContextIncludesSemanticIndexV2SecurityHits(t *testing.T) {
	artifacts := latestAnalysisArtifacts{
		Pack: KnowledgePack{
			RunID:          "run-ctx-v2",
			Goal:           "map anti-cheat authority boundaries",
			ProjectSummary: "ShooterGame owns anti-cheat sensitive startup and authority checks.",
		},
		IndexV2: SemanticIndexV2{
			RunID:          "run-ctx-v2",
			Goal:           "map anti-cheat authority boundaries",
			Root:           "C:\\repo",
			GeneratedAt:    time.Now(),
			PrimaryStartup: "ShooterGame",
			Files: []FileRecord{
				{Path: "Source/ShooterGame/Public/ShooterGameMode.h", ImportanceScore: 90, Tags: []string{"startup", "authority"}},
			},
			Symbols: []SymbolRecord{
				{ID: "type:AShooterGameMode", Name: "AShooterGameMode", Kind: "uclass", File: "Source/ShooterGame/Public/ShooterGameMode.h", Module: "ShooterGame"},
				{ID: "rpc:ServerStartMatch", Name: "ServerStartMatch", Kind: "rpc"},
			},
			CallEdges: []CallEdge{
				{SourceID: "type:AShooterGameMode", TargetID: "rpc:ServerStartMatch", Type: "rpc_server"},
			},
			OverlayEdges: []OverlayEdge{
				{SourceID: "type:AShooterGameMode", TargetID: "rpc:ServerStartMatch", Type: "rpc_server", Domain: "authority_boundary", Evidence: []string{"Source/ShooterGame/Public/ShooterGameMode.h"}},
			},
			QueryModes: []string{"map", "trace", "impact", "security", "performance"},
		},
	}

	text := renderRelevantProjectAnalysisContext(artifacts, "Explain the anti-cheat authority boundary around ServerStartMatch.")
	if !strings.Contains(text, "Relevant structural index v2 hits") {
		t.Fatalf("expected v2 section, got %q", text)
	}
	if !strings.Contains(text, "query_mode: security") {
		t.Fatalf("expected security mode, got %q", text)
	}
	if !strings.Contains(text, "overlay_v2: authority_boundary") {
		t.Fatalf("expected authority boundary overlay, got %q", text)
	}
	if !strings.Contains(text, "call_v2: AShooterGameMode -> ServerStartMatch [rpc_server]") {
		t.Fatalf("expected call edge rendering, got %q", text)
	}
}

func TestBuildCachedAnalysisFastPathMetadataIncludesStructuralIndexV2Source(t *testing.T) {
	artifacts := latestAnalysisArtifacts{
		IndexV2: SemanticIndexV2{
			RunID:       "run-meta-v2",
			Goal:        "map trust boundaries",
			GeneratedAt: time.Now(),
			Symbols: []SymbolRecord{
				{ID: "type:AShooterGameMode", Name: "AShooterGameMode", Kind: "uclass", File: "Source/ShooterGame/Public/ShooterGameMode.h"},
			},
			OverlayEdges: []OverlayEdge{
				{SourceID: "type:AShooterGameMode", TargetID: "rpc:ServerStartMatch", Type: "rpc_server", Domain: "authority_boundary"},
			},
		},
	}

	meta := buildCachedAnalysisFastPathMetadata(artifacts, "Show anti-cheat trust boundary flow.")
	if !containsStringCI(meta.Sources, "structural_index_v2") {
		t.Fatalf("expected structural_index_v2 source, got %+v", meta.Sources)
	}
	if meta.Confidence != "medium" {
		t.Fatalf("expected medium confidence from v2 hits, got %+v", meta)
	}
}

func TestBuildSessionAnalysisSummaryIncludesMode(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:  "run-mode",
			Goal:   "trace startup dispatch path",
			Mode:   "trace",
			Status: "completed",
		},
		KnowledgePack: KnowledgePack{
			ProjectSummary: "Startup handoff flows through the command dispatcher.",
		},
	}

	text := buildSessionAnalysisSummary(run)
	if !strings.Contains(text, "- Mode: trace") {
		t.Fatalf("expected mode line in session analysis summary, got %q", text)
	}
}

func containsStringCI(items []string, target string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}
