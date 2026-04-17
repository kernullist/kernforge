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

func TestRenderRelevantProjectAnalysisContextIncludesIOCTLV2OverlayHits(t *testing.T) {
	artifacts := latestAnalysisArtifacts{
		IndexV2: SemanticIndexV2{
			RunID:       "run-ioctl-v2",
			Goal:        "map ioctl trust boundaries",
			GeneratedAt: time.Now(),
			OverlayEdges: []OverlayEdge{
				{
					SourceID: "entity:Plugins/CheatGuard/Source/CheatGuardRuntime/Private/IoctlDispatch.cpp",
					TargetID: "entity:ioctl_surface",
					Type:     "issues_ioctl",
					Domain:   "ioctl_surface",
					Evidence: []string{"Plugins/CheatGuard/Source/CheatGuardRuntime/Private/IoctlDispatch.cpp"},
				},
			},
		},
	}

	text := renderRelevantProjectAnalysisContext(artifacts, "Show the anti-cheat ioctl validation path.")
	if !strings.Contains(text, "query_mode: security") {
		t.Fatalf("expected security query mode, got %q", text)
	}
	if !strings.Contains(text, "overlay_v2: ioctl_surface") {
		t.Fatalf("expected ioctl overlay hit, got %q", text)
	}
}

func TestRenderRelevantProjectAnalysisContextIncludesHandleV2OverlayHits(t *testing.T) {
	artifacts := latestAnalysisArtifacts{
		IndexV2: SemanticIndexV2{
			RunID:       "run-handle-v2",
			Goal:        "map handle trust boundaries",
			GeneratedAt: time.Now(),
			OverlayEdges: []OverlayEdge{
				{
					SourceID: "entity:Plugins/CheatGuard/Source/CheatGuardRuntime/Private/HandlePolicy.cpp",
					TargetID: "entity:handle_surface",
					Type:     "opens_handle",
					Domain:   "handle_surface",
					Evidence: []string{"Plugins/CheatGuard/Source/CheatGuardRuntime/Private/HandlePolicy.cpp"},
				},
			},
		},
	}

	text := renderRelevantProjectAnalysisContext(artifacts, "Show the anti-cheat handle access policy.")
	if !strings.Contains(text, "query_mode: security") {
		t.Fatalf("expected security query mode, got %q", text)
	}
	if !strings.Contains(text, "overlay_v2: handle_surface") {
		t.Fatalf("expected handle overlay hit, got %q", text)
	}
}

func TestRenderRelevantProjectAnalysisContextIncludesRPCV2OverlayHits(t *testing.T) {
	artifacts := latestAnalysisArtifacts{
		IndexV2: SemanticIndexV2{
			RunID:       "run-rpc-v2",
			Goal:        "map rpc trust boundaries",
			GeneratedAt: time.Now(),
			OverlayEdges: []OverlayEdge{
				{
					SourceID: "entity:Plugins/CheatGuard/Source/CheatGuardRuntime/Private/RpcDispatchPipe.cpp",
					TargetID: "entity:rpc_surface",
					Type:     "dispatches_rpc",
					Domain:   "rpc_surface",
					Evidence: []string{"Plugins/CheatGuard/Source/CheatGuardRuntime/Private/RpcDispatchPipe.cpp"},
				},
			},
		},
	}

	text := renderRelevantProjectAnalysisContext(artifacts, "Show the anti-cheat rpc dispatch validation path.")
	if !strings.Contains(text, "query_mode: security") {
		t.Fatalf("expected security query mode, got %q", text)
	}
	if !strings.Contains(text, "overlay_v2: rpc_surface") {
		t.Fatalf("expected rpc overlay hit, got %q", text)
	}
}

func TestRenderRelevantProjectAnalysisContextIncludesBuildContextAndPathExpansion(t *testing.T) {
	artifacts := latestAnalysisArtifacts{
		IndexV2: SemanticIndexV2{
			RunID:          "run-trace-v2",
			Goal:           "trace ioctl validation path",
			GeneratedAt:    time.Now(),
			PrimaryStartup: "GuardRuntime",
			BuildContexts: []BuildContextRecord{
				{
					ID:        "buildctx:compile:module:GuardRuntime",
					Name:      "GuardRuntime compile context",
					Kind:      "compile_command",
					Directory: "native/cmake-build-debug",
					Module:    "GuardRuntime",
					Files:     []string{"Source/GuardRuntime/Private/IoctlDispatch.cpp"},
					Compiler:  "clang++",
				},
			},
			Files: []FileRecord{
				{
					Path:            "Source/GuardRuntime/Private/IoctlDispatch.cpp",
					ImportanceScore: 85,
					BuildContextIDs: []string{"buildctx:compile:module:GuardRuntime"},
				},
			},
			Symbols: []SymbolRecord{
				{ID: "buildctx:compile:module:GuardRuntime", Name: "GuardRuntime compile context", Kind: "build_context"},
				{ID: "ioctl:GuardDispatch@Source/GuardRuntime/Private/IoctlDispatch.cpp", Name: "GuardDispatch", Kind: "ioctl_handler", File: "Source/GuardRuntime/Private/IoctlDispatch.cpp", BuildContextID: "buildctx:compile:module:GuardRuntime"},
				{ID: "func:ValidateRequest@Source/GuardRuntime/Private/IoctlDispatch.cpp", Name: "ValidateRequest", Kind: "function", File: "Source/GuardRuntime/Private/IoctlDispatch.cpp", BuildContextID: "buildctx:compile:module:GuardRuntime"},
			},
			BuildOwnershipEdges: []BuildOwnershipEdge{
				{SourceID: "buildctx:compile:module:GuardRuntime", TargetID: "ioctl:GuardDispatch@Source/GuardRuntime/Private/IoctlDispatch.cpp", Type: "compiles_symbol", Evidence: []string{"Source/GuardRuntime/Private/IoctlDispatch.cpp"}},
			},
			CallEdges: []CallEdge{
				{SourceID: "ioctl:GuardDispatch@Source/GuardRuntime/Private/IoctlDispatch.cpp", TargetID: "func:ValidateRequest@Source/GuardRuntime/Private/IoctlDispatch.cpp", Type: "calls", Evidence: []string{"Source/GuardRuntime/Private/IoctlDispatch.cpp"}},
			},
			OverlayEdges: []OverlayEdge{
				{SourceID: "ioctl:GuardDispatch@Source/GuardRuntime/Private/IoctlDispatch.cpp", TargetID: "entity:ioctl_surface", Type: "issues_ioctl", Domain: "ioctl_surface", Evidence: []string{"Source/GuardRuntime/Private/IoctlDispatch.cpp"}},
			},
		},
	}

	text := renderRelevantProjectAnalysisContext(artifacts, "Trace the ioctl validation path from build context to handler.")
	if !strings.Contains(text, "build_context_v2: GuardRuntime compile context") {
		t.Fatalf("expected build context rendering, got %q", text)
	}
	if !strings.Contains(text, "path_v2: GuardRuntime compile context -> GuardDispatch") {
		t.Fatalf("expected graph-expanded path rendering, got %q", text)
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
