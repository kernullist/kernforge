package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBaseUserQueryTextStripsConversationRuntimeContext(t *testing.T) {
	text := strings.Join([]string{
		"RuntimeManager.cpp 버그를 수정해",
		"",
		"[Conversation Runtime Context]",
		"Working directory: F:\\repo",
		"Active permission profile: :workspace",
		"[/Conversation Runtime Context]",
	}, "\n")

	if got := baseUserQueryText(text); got != "RuntimeManager.cpp 버그를 수정해" {
		t.Fatalf("expected runtime context to be stripped from base query, got %q", got)
	}
}

func TestBaseUserQueryTextStripsInjectedExecutionContext(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{
			name: "activated skill",
			text: strings.Join([]string{
				"RuntimeManager.cpp 버그를 수정해",
				"",
				"Activated skills for this request:",
				"### samplegame-anticheat",
				"Source: C:\\skills\\samplegame-anticheat\\SKILL.md",
				"Use SampleGame-specific anti-cheat guidance.",
			}, "\n"),
		},
		{
			name: "pending review repair",
			text: strings.Join([]string{
				"계속 수정해",
				"",
				"Pending review repair confirmation:",
				"- The user selected `y` for the pending pre-write review repair prompt.",
				"- Continue from the latest review findings.",
			}, "\n"),
		},
		{
			name: "pending reviewer gate repair",
			text: strings.Join([]string{
				"계속 수정해",
				"",
				"Pending reviewer-gate repair confirmation:",
				"- The user selected `y` after a pre-write reviewer gate failed.",
				"- Do not bypass the reviewer gate.",
			}, "\n"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := baseUserQueryText(tc.text)
			if strings.Contains(got, "Activated skills") ||
				strings.Contains(got, "Pending review") ||
				strings.Contains(got, "Pending reviewer-gate") ||
				strings.Contains(got, "Source:") {
				t.Fatalf("expected injected execution context to be stripped, got %q", got)
			}
			if got == "" {
				t.Fatalf("expected original user request to remain")
			}
		})
	}
}

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

func TestAnalysisContextProgressMessageIncludesReuseEvidence(t *testing.T) {
	cfg := Config{AutoLocale: boolPtr(false)}
	artifacts := latestAnalysisArtifacts{
		Pack: KnowledgePack{
			RunID:          "run-progress",
			ProjectSummary: "SampleWorker owns telemetry startup.",
			Subsystems: []KnowledgeSubsystem{
				{
					Title:         "SampleWorker Runtime",
					Group:         "Forensic Analysis",
					KeyFiles:      []string{"SampleWorker/main.cpp"},
					EvidenceFiles: []string{"SampleWorker/collector.cpp"},
				},
			},
		},
		Corpus: VectorCorpus{
			RunID: "run-progress",
			Documents: []VectorCorpusDocument{
				{
					ID:       "subsystem:sampleworker-runtime",
					Kind:     "subsystem",
					Title:    "SampleWorker Runtime",
					Text:     "Startup initializes telemetry collectors.",
					PathHint: "SampleWorker/main.cpp",
				},
			},
		},
		Index: SemanticIndex{
			RunID: "run-progress",
			Files: []SemanticIndexedFile{
				{Path: "SampleWorker/main.cpp", ImportanceScore: 95, Tags: []string{"startup"}},
			},
		},
	}

	freshness := analysisFreshnessReport{
		Status:      analysisFreshnessFresh,
		Action:      "use",
		GeneratedAt: time.Now().Add(-time.Hour),
		Age:         time.Hour,
		RunID:       "run-progress",
	}
	message := formatProjectAnalysisContextProgressMessage(cfg, artifacts, "SampleWorker collector", freshness)
	if !strings.Contains(message, "Using latest analyze-project artifacts:") {
		t.Fatalf("expected English progress prefix, got %q", message)
	}
	if !strings.Contains(message, "freshness=fresh") ||
		!strings.Contains(message, "action=use") {
		t.Fatalf("expected freshness in progress message, got %q", message)
	}
	if !strings.Contains(message, "run=run-progress") ||
		!strings.Contains(message, "confidence=high") {
		t.Fatalf("expected run id and confidence in progress message, got %q", message)
	}
	if !strings.Contains(message, "knowledge_pack") ||
		!strings.Contains(message, "vector_corpus") ||
		!strings.Contains(message, "structural_index") {
		t.Fatalf("expected source names in progress message, got %q", message)
	}
	if !strings.Contains(message, "files=SampleWorker/main.cpp") {
		t.Fatalf("expected file hint in progress message, got %q", message)
	}
}

func TestAnalysisContextProgressFilePathStripsAnchorDecorations(t *testing.T) {
	if got := analysisContextProgressFilePath("driver/dispatch.cpp:42#DispatchIoctl"); got != "driver/dispatch.cpp" {
		t.Fatalf("expected stripped source anchor path, got %q", got)
	}
	if got := analysisContextProgressFilePath("driver/dispatch.cpp:42-48"); got != "driver/dispatch.cpp" {
		t.Fatalf("expected stripped source range path, got %q", got)
	}
	if got := analysisContextProgressFilePath("driver/dispatch"); got != "" {
		t.Fatalf("expected non-file evidence to be ignored, got %q", got)
	}
}

func TestLatestProjectAnalysisContextSkipsStaleArtifacts(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	writeLatestAnalysisKnowledgePack(t, cfg, root, KnowledgePack{
		RunID:          "run-stale",
		Root:           root,
		GeneratedAt:    time.Now().Add(-10 * 24 * time.Hour),
		ProjectSummary: "SampleWorker owns telemetry collection.",
		Subsystems: []KnowledgeSubsystem{
			{
				Title:         "SampleWorker Runtime",
				Group:         "Forensic Analysis",
				KeyFiles:      []string{"SampleWorker/main.cpp"},
				EvidenceFiles: []string{"SampleWorker/collector.cpp"},
			},
		},
	})
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Config:    cfg,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}

	context, progress := agent.latestProjectAnalysisContextWithProgress("SampleWorker collector")
	if strings.TrimSpace(context) != "" {
		t.Fatalf("expected stale analysis context to be skipped, got %q", context)
	}
	if !strings.Contains(progress, "freshness=stale") ||
		!strings.Contains(progress, "action=refresh_recommended") ||
		!strings.Contains(progress, "age_exceeds_stale_ttl") {
		t.Fatalf("expected stale freshness progress, got %q", progress)
	}
	if strings.TrimSpace(session.LastAnalysisContextRunID) != "" {
		t.Fatalf("stale context should not update last analysis context run id")
	}
}

func TestLatestProjectAnalysisContextMarksSuspectArtifacts(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	writeLatestAnalysisKnowledgePack(t, cfg, root, KnowledgePack{
		RunID:          "run-suspect",
		Root:           root,
		GeneratedAt:    time.Now().Add(-72 * time.Hour),
		ProjectSummary: "SampleWorker owns telemetry collection.",
		Subsystems: []KnowledgeSubsystem{
			{
				Title:         "SampleWorker Runtime",
				Group:         "Forensic Analysis",
				KeyFiles:      []string{"SampleWorker/main.cpp"},
				EvidenceFiles: []string{"SampleWorker/collector.cpp"},
			},
		},
	})
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Config:    cfg,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}

	context, progress := agent.latestProjectAnalysisContextWithProgress("SampleWorker collector")
	if !strings.Contains(context, "Analysis cache freshness: suspect") {
		t.Fatalf("expected suspect freshness caveat in context, got %q", context)
	}
	if !strings.Contains(progress, "freshness=suspect") ||
		!strings.Contains(progress, "action=use_with_verification") ||
		!strings.Contains(progress, "age_exceeds_fresh_ttl") {
		t.Fatalf("expected suspect freshness progress, got %q", progress)
	}
	if session.LastAnalysisContextRunID != "run-suspect" {
		t.Fatalf("expected suspect context to update run id, got %q", session.LastAnalysisContextRunID)
	}
}

func TestLatestProjectAnalysisContextSkipsVerifierBlockedArtifacts(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	pack := KnowledgePack{
		RunID:          "run-blocked",
		Root:           root,
		GeneratedAt:    time.Now(),
		ProjectSummary: "SampleWorker owns telemetry collection.",
		Subsystems: []KnowledgeSubsystem{
			{
				Title:         "SampleWorker Runtime",
				Group:         "Forensic Analysis",
				KeyFiles:      []string{"SampleWorker/main.cpp"},
				EvidenceFiles: []string{"SampleWorker/collector.cpp"},
			},
		},
	}
	latestDir := writeLatestAnalysisKnowledgePack(t, cfg, root, pack)
	writeLatestAnalysisRunMetadata(t, latestDir, ProjectAnalysisSummary{
		RunID:                  "run-blocked",
		Status:                 "completed_with_verifier_blockers",
		CompletedAt:            time.Now(),
		VerifierBlockingIssues: 1,
	}, ClaimVerificationReport{
		BlockingCount: 1,
	})
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Config:    cfg,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}

	context, progress := agent.latestProjectAnalysisContextWithProgress("SampleWorker collector")
	if strings.TrimSpace(context) != "" {
		t.Fatalf("expected verifier-blocked analysis context to be skipped, got %q", context)
	}
	if !strings.Contains(progress, "freshness=unusable") ||
		!strings.Contains(progress, "action=refresh_required") ||
		!strings.Contains(progress, "verifier_blockers") {
		t.Fatalf("expected unusable freshness progress, got %q", progress)
	}
}

func TestAnalysisFreshnessFileOverlapAndCriticalChanges(t *testing.T) {
	overlap := overlapAnalysisFreshnessFiles(
		[]string{"Source/Worker/main.cpp", "README.md"},
		[]string{"Source/Worker/main.cpp:42", "Source/Worker/collector.cpp"},
	)
	if len(overlap) != 1 || overlap[0] != "Source/Worker/main.cpp" {
		t.Fatalf("unexpected overlap: %#v", overlap)
	}
	critical := criticalAnalysisFreshnessChangedFiles([]string{"go.mod", "src/runtime.cpp", "Game/Guard.Build.cs"})
	if len(critical) != 2 || critical[0] != "go.mod" || critical[1] != "Game/Guard.Build.cs" {
		t.Fatalf("unexpected critical changed files: %#v", critical)
	}
}

func writeLatestAnalysisKnowledgePack(t *testing.T, cfg Config, root string, pack KnowledgePack) string {
	t.Helper()
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		t.Fatalf("mkdir latest analysis dir: %v", err)
	}
	data, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal knowledge pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), data, 0o644); err != nil {
		t.Fatalf("write knowledge pack: %v", err)
	}
	return latestDir
}

func writeLatestAnalysisRunMetadata(t *testing.T, latestDir string, summary ProjectAnalysisSummary, report ClaimVerificationReport) {
	t.Helper()
	data, err := json.Marshal(struct {
		Summary           ProjectAnalysisSummary  `json:"summary"`
		ClaimVerification ClaimVerificationReport `json:"claim_verification,omitempty"`
	}{
		Summary:           summary,
		ClaimVerification: report,
	})
	if err != nil {
		t.Fatalf("marshal run metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestDir, "run.json"), data, 0o644); err != nil {
		t.Fatalf("write run metadata: %v", err)
	}
}

func TestLatestAnalysisArtifactsRunIDFallsBackToV2AndDocsManifest(t *testing.T) {
	if got := latestAnalysisArtifactsRunID(latestAnalysisArtifacts{IndexV2: SemanticIndexV2{RunID: "run-v2"}}); got != "run-v2" {
		t.Fatalf("expected v2 run id fallback, got %q", got)
	}
	if got := latestAnalysisArtifactsRunID(latestAnalysisArtifacts{DocsManifest: AnalysisDocsManifest{RunID: "run-docs"}}); got != "run-docs" {
		t.Fatalf("expected docs manifest run id fallback, got %q", got)
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
