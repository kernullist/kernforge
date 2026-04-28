package main

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyProjectAnalysisQAIntentDeepMapKorean(t *testing.T) {
	tests := []struct {
		query string
		want  ProjectAnalysisQAIntent
	}{
		{query: "이 프로젝트 전체 구조를 아주 자세히 설명해줘", want: projectAnalysisQAIntentDeepMap},
		{query: "이 드라이버 프로젝트 전체 구조를 아주 자세히 설명해줘", want: projectAnalysisQAIntentDeepMap},
		{query: "드라이버 초기화 흐름을 자세히 설명해줘", want: projectAnalysisQAIntentFlowTrace},
		{query: "startup에서 telemetry upload까지 흐름을 설명해줘", want: projectAnalysisQAIntentFlowTrace},
		{query: "IOCTL handler를 바꾸면 어디까지 영향이 가?", want: projectAnalysisQAIntentSecuritySurface},
		{query: "Unreal replication 구조와 보안 경계를 설명해줘", want: projectAnalysisQAIntentUnrealStructure},
		{query: "Unreal 프로젝트 전체 구조를 자세히 설명해줘", want: projectAnalysisQAIntentUnrealStructure},
		{query: "빌드 산출물과 compile_commands 연결을 설명해줘", want: projectAnalysisQAIntentBuildArtifact},
		{query: "빌드 산출물 전체 구조를 자세히 설명해줘", want: projectAnalysisQAIntentBuildArtifact},
	}
	for _, tt := range tests {
		got := classifyProjectAnalysisQAIntent(tt.query)
		if got != tt.want {
			t.Fatalf("query %q: expected %s, got %s", tt.query, tt.want, got)
		}
	}
}

func TestBuildProjectStructureAnswerPackBoostsDeveloperDocs(t *testing.T) {
	run := sampleProjectStructureQARun()
	run.Snapshot.ArchitectureFacts = buildArchitectureFactPack(run.Snapshot, run.SemanticIndexV2, run.UnrealGraph, run.Summary.Goal)
	run.KnowledgePack.ArchitectureFacts = run.Snapshot.ArchitectureFacts
	docs := buildAnalysisDocsVectorDocuments(run)
	manifest := buildAnalysisDocsManifestForTest(run)
	artifacts := latestAnalysisArtifacts{
		Pack:         run.KnowledgePack,
		Snapshot:     run.Snapshot,
		Corpus:       VectorCorpus{RunID: run.Summary.RunID, Goal: run.Summary.Goal, Documents: docs},
		IndexV2:      run.SemanticIndexV2,
		UnrealGraph:  run.UnrealGraph,
		DocsManifest: manifest,
	}

	pack := buildProjectStructureAnswerPack(artifacts, "이 프로젝트 전체 구조를 자세히 설명해줘")
	if pack.Intent != projectAnalysisQAIntentDeepMap {
		t.Fatalf("expected deep map intent, got %s", pack.Intent)
	}
	if !projectStructurePackHasDoc(pack, "DEVELOPER_OVERVIEW.md") {
		t.Fatalf("expected developer overview doc hit, got %+v", pack.RelevantDocs)
	}
	if !projectStructurePackHasDoc(pack, "MODULES.md") {
		t.Fatalf("expected modules doc hit, got %+v", pack.RelevantDocs)
	}
	if len(pack.GraphViews) == 0 {
		t.Fatalf("expected graph answer views")
	}
	if len(pack.SourceAnchors) < 5 {
		t.Fatalf("expected rich source anchors, got %+v", pack.SourceAnchors)
	}
	if !architectureFactPackHasData(pack.ArchitectureFacts) {
		t.Fatalf("expected deterministic architecture facts in answer pack")
	}
}

func TestRenderRelevantProjectAnalysisContextUsesAnswerPackForDeepStructureQuery(t *testing.T) {
	run := sampleProjectStructureQARun()
	run.Snapshot.ArchitectureFacts = buildArchitectureFactPack(run.Snapshot, run.SemanticIndexV2, run.UnrealGraph, run.Summary.Goal)
	run.KnowledgePack.ArchitectureFacts = run.Snapshot.ArchitectureFacts
	artifacts := latestAnalysisArtifacts{
		Pack:         run.KnowledgePack,
		Snapshot:     run.Snapshot,
		Corpus:       VectorCorpus{RunID: run.Summary.RunID, Goal: run.Summary.Goal, Documents: buildAnalysisDocsVectorDocuments(run)},
		IndexV2:      run.SemanticIndexV2,
		UnrealGraph:  run.UnrealGraph,
		DocsManifest: buildAnalysisDocsManifestForTest(run),
	}

	text := renderRelevantProjectAnalysisContext(artifacts, "이 프로젝트 구조와 실행 흐름을 자세히 설명해줘")
	if !strings.Contains(text, "Project structure answer pack") {
		t.Fatalf("expected answer pack, got %q", text)
	}
	if !strings.Contains(text, "Deterministic architecture fact pack") {
		t.Fatalf("expected deterministic fact pack, got %q", text)
	}
	if !strings.Contains(text, "Answer contract") {
		t.Fatalf("expected answer contract, got %q", text)
	}
	if !strings.Contains(text, "Priority docs and sections") {
		t.Fatalf("expected priority docs to be visible in deep QA context, got %q", text)
	}
	if !strings.Contains(text, "STRUCTURE_DIAGRAMS.md") || !strings.Contains(text, "CODE_STRUCTURE_REFERENCE.md") {
		t.Fatalf("expected flow docs to remain visible in deep QA context, got %q", text)
	}
	if !strings.Contains(text, "Relevant structural index v2 hits") {
		t.Fatalf("expected legacy v2 context to remain, got %q", text)
	}
}

func TestDriverStructureQuestionIsAnalysisOnlyProjectKnowledge(t *testing.T) {
	query := "이 드라이버 프로젝트 전체 구조를 자세히 설명해줘"
	if got := classifyTurnIntent(query); got != TurnIntentAskProjectKnowledge {
		t.Fatalf("expected project knowledge intent, got %s", got)
	}
	if !prefersReadOnlyAnalysisIntent(query) {
		t.Fatalf("expected driver structure explanation to be analysis-only")
	}
	if looksLikeExplicitEditIntent(query) {
		t.Fatalf("did not expect driver structure explanation to be an edit request")
	}
}

func TestDeepProjectAnalysisContextReinjectsAfterFailedUserTurn(t *testing.T) {
	root := t.TempDir()
	query := "이 드라이버 프로젝트 전체 구조를 자세히 설명해줘"
	session := NewSession(root, "scripted", "model", "", "default")
	session.LastAnalysisContextQuery = query
	session.LastAnalysisContextRunID = "run-qa-fixture"
	session.AddMessage(Message{
		Role: "user",
		Text: query + "\n\nRelevant project analysis from past analyze-project runs:\nProject structure answer pack:\n- intent: deep_map",
	})
	agent := &Agent{Session: session}
	artifacts := latestAnalysisArtifacts{
		Pack: KnowledgePack{
			RunID:          "run-qa-fixture",
			ProjectSummary: "cached",
		},
	}
	if !agent.shouldInjectLatestProjectAnalysisContext(artifacts, query) {
		t.Fatalf("expected deep analysis context to be reinjected after a user turn without assistant reply")
	}
	session.AddMessage(Message{Role: "assistant", Text: "answered"})
	if agent.shouldInjectLatestProjectAnalysisContext(artifacts, query) {
		t.Fatalf("did not expect identical deep analysis context to reinject after an assistant reply")
	}
}

func TestProjectAnalysisFastPathNeedsToolsPrefixIsInternalControlToken(t *testing.T) {
	replies := []string{
		"NEEDS_TOOLS",
		"NEEDS_TOOLS\n\n캐시된 분석만으로는 부족합니다.",
		"NEEDS_TOOLS: cache is incomplete",
		"NEEDS_TOOLS. cache is incomplete",
	}
	for _, reply := range replies {
		if !projectAnalysisFastPathReplyNeedsTools(reply) {
			t.Fatalf("expected reply to be treated as internal NEEDS_TOOLS token: %q", reply)
		}
	}
	if projectAnalysisFastPathReplyNeedsTools("The cached analysis is enough to answer.") {
		t.Fatalf("did not expect normal answer to be treated as NEEDS_TOOLS")
	}
}

func TestAnalysisDocsManifestIncludesQAMetadata(t *testing.T) {
	run := sampleProjectStructureQARun()
	manifest := buildAnalysisDocsManifestForTest(run)
	var overview AnalysisGeneratedDoc
	for _, doc := range manifest.Documents {
		if doc.Name == "DEVELOPER_OVERVIEW.md" {
			overview = doc
			break
		}
	}
	if !containsStringCI(overview.QueryIntents, "deep_map") {
		t.Fatalf("expected overview deep_map query intent, got %+v", overview.QueryIntents)
	}
	if overview.Priority == 0 {
		t.Fatalf("expected overview priority")
	}
	var runtimeSection AnalysisDocSection
	for _, section := range overview.Sections {
		if section.ID == "developer.runtime_narratives" {
			runtimeSection = section
			break
		}
	}
	if !containsStringCI(runtimeSection.QueryIntents, "flow_trace") {
		t.Fatalf("expected runtime section flow_trace intent, got %+v", runtimeSection.QueryIntents)
	}
	if !containsStringCI(runtimeSection.GraphRefs, "runtime_edges") {
		t.Fatalf("expected runtime graph ref, got %+v", runtimeSection.GraphRefs)
	}

	corpusDocs := buildAnalysisDocsVectorDocuments(run)
	foundMetadata := false
	for _, doc := range corpusDocs {
		if doc.Metadata["section_id"] == "developer.runtime_narratives" {
			foundMetadata = strings.Contains(doc.Metadata["query_intents"], "flow_trace") &&
				strings.Contains(doc.Metadata["graph_refs"], "runtime_edges")
			break
		}
	}
	if !foundMetadata {
		t.Fatalf("expected vector metadata for runtime narratives section")
	}
}

func TestDeveloperDocsIncludeDeepQAAuthoringSections(t *testing.T) {
	run := sampleProjectStructureQARun()
	overview := buildAnalysisDeveloperOverviewDoc(run)
	modules := buildAnalysisModulesDoc(run)
	diagrams := buildAnalysisStructureDiagramsDoc(run)
	reference := buildAnalysisCodeStructureReferenceDoc(run)
	needles := []string{
		"## Architecture Layers",
		"## Primary Runtime Narratives",
		"## Most Important Cross-Cutting Paths",
		"## Domain Critical Anchors",
		"## Public API And Boundary",
		"## Change Impact Notes",
		"## Startup To Runtime Flow",
		"## Security Boundary Flow",
		"## Unreal Reflection And Replication Flow",
		"## Symbol Clusters",
		"## Verification Anchor Map",
	}
	joined := strings.Join([]string{overview, modules, diagrams, reference}, "\n")
	for _, needle := range needles {
		if !strings.Contains(joined, needle) {
			t.Fatalf("expected developer docs to contain %q", needle)
		}
	}
}

func TestProjectStructureAnswerPackGoldenSecurityContract(t *testing.T) {
	run := sampleProjectStructureQARun()
	artifacts := latestAnalysisArtifacts{
		Pack:         run.KnowledgePack,
		Snapshot:     run.Snapshot,
		Corpus:       VectorCorpus{RunID: run.Summary.RunID, Goal: run.Summary.Goal, Documents: buildAnalysisDocsVectorDocuments(run)},
		IndexV2:      run.SemanticIndexV2,
		UnrealGraph:  run.UnrealGraph,
		DocsManifest: buildAnalysisDocsManifestForTest(run),
	}

	pack := buildProjectStructureAnswerPack(artifacts, "IOCTL handler와 RPC validation surface를 자세히 설명해줘")
	rendered := renderProjectStructureAnswerPack(pack, 4000)
	if pack.Intent != projectAnalysisQAIntentSecuritySurface {
		t.Fatalf("expected security surface intent, got %s", pack.Intent)
	}
	if len(pack.SecurityOverlays) == 0 {
		t.Fatalf("expected security overlays")
	}
	if len(pack.VerificationEntries) == 0 {
		t.Fatalf("expected verification entries")
	}
	if len(pack.FuzzTargets) == 0 {
		t.Fatalf("expected fuzz targets")
	}
	for _, needle := range []string{"Security overlays", "Verification and fuzz follow-through", "Source anchors", "Answer contract"} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected rendered answer pack to contain %q, got %q", needle, rendered)
		}
	}
}

func TestProjectStructureAnswerPackMarksCurrentSourceNeededForStaleImpact(t *testing.T) {
	run := sampleProjectStructureQARun()
	manifest := buildAnalysisDocsManifestForTest(run)
	for i := range manifest.Documents {
		if manifest.Documents[i].Name != "SECURITY_SURFACE.md" {
			continue
		}
		manifest.Documents[i].StaleMarkers = append(manifest.Documents[i].StaleMarkers, "source anchors changed after analysis")
		for j := range manifest.Documents[i].Sections {
			if manifest.Documents[i].Sections[j].ID == "security.indexed_surfaces" {
				manifest.Documents[i].Sections[j].StaleMarkers = append(manifest.Documents[i].Sections[j].StaleMarkers, "verification anchors may be stale")
			}
		}
	}
	artifacts := latestAnalysisArtifacts{
		Pack:         run.KnowledgePack,
		Snapshot:     run.Snapshot,
		Corpus:       VectorCorpus{RunID: run.Summary.RunID, Goal: run.Summary.Goal, Documents: buildAnalysisDocsVectorDocuments(run)},
		IndexV2:      run.SemanticIndexV2,
		UnrealGraph:  run.UnrealGraph,
		DocsManifest: normalizeAnalysisDocsManifest(manifest),
	}

	pack := buildProjectStructureAnswerPack(artifacts, "IOCTL handler를 바꾸면 어떤 영향과 검증이 필요해?")
	rendered := renderProjectStructureAnswerPack(pack, 4000)
	if pack.Intent != projectAnalysisQAIntentSecuritySurface {
		t.Fatalf("expected security surface intent because IOCTL is safety-critical, got %s", pack.Intent)
	}
	if !pack.CurrentSourceNeeded {
		t.Fatalf("expected stale security impact pack to require current source verification")
	}
	if len(pack.StaleMarkers) == 0 {
		t.Fatalf("expected stale markers to propagate")
	}
	for _, needle := range []string{"current_source_needed: true", "Stale or invalidation markers", "Verification and fuzz follow-through"} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected rendered stale pack to contain %q, got %q", needle, rendered)
		}
	}
}

func TestProjectStructureAnswerPackAllowsSmallGroundedArchitecturePack(t *testing.T) {
	pack := ProjectStructureAnswerPack{
		Intent: projectAnalysisQAIntentDeepMap,
		ArchitectureFacts: ArchitectureFactPack{
			DomainHints: []string{"windows_driver"},
			TopLevelDirectories: []ArchitectureDirectoryFact{
				{Path: "Driver/"},
				{Path: "Common/"},
			},
			CriticalAnchors: []ArchitectureAnchorFact{
				{
					Role:     "driver_entry",
					Symbol:   "DriverEntry",
					Location: "Driver/Driver.cpp:10",
				},
			},
		},
		CriticalAnchors: []ProjectStructureCriticalAnchor{
			{
				Role: "driver_entry",
				Name: "DriverEntry",
				File: "Driver/Driver.cpp",
				Line: 10,
			},
		},
		RelevantDocs: []ProjectStructureDocHit{
			{DocName: "DEVELOPER_OVERVIEW.md", Path: "DEVELOPER_OVERVIEW.md"},
		},
		Folders: []DeveloperFolderRecord{
			{Path: "Driver", Responsibility: "Kernel driver runtime."},
			{Path: "Common", Responsibility: "Shared contracts."},
		},
		SourceAnchors: []string{
			"Driver/Driver.cpp:10",
		},
	}
	if projectStructureAnswerPackNeedsCurrentSource(pack) {
		t.Fatalf("small but grounded architecture pack should not require current source")
	}
}

func TestDriverStructureAnswerPackHighlightsKernelCriticalAnchors(t *testing.T) {
	run := sampleDriverProjectStructureQARun()
	manifest := buildAnalysisDocsManifestForTest(run)
	for i := range manifest.Documents {
		manifest.Documents[i].StaleMarkers = []string{"no_previous_run", "new_primary_scope"}
		for j := range manifest.Documents[i].Sections {
			manifest.Documents[i].Sections[j].StaleMarkers = []string{"no_previous_run", "new_primary_scope"}
		}
	}
	artifacts := latestAnalysisArtifacts{
		Pack:         run.KnowledgePack,
		Snapshot:     run.Snapshot,
		Corpus:       VectorCorpus{RunID: run.Summary.RunID, Goal: run.Summary.Goal, Documents: buildAnalysisDocsVectorDocuments(run)},
		IndexV2:      run.SemanticIndexV2,
		DocsManifest: normalizeAnalysisDocsManifest(manifest),
	}

	pack := buildProjectStructureAnswerPack(artifacts, "TavernKernel 프로젝트 전체 구조를 자세히 설명해줘")
	rendered := renderProjectStructureAnswerPack(pack, 12000)
	if !analysisContainsStringCI(pack.DomainHints, "windows_driver") {
		t.Fatalf("expected windows_driver domain hint, got %+v", pack.DomainHints)
	}
	for _, role := range []string{"driver_entry", "core_initialization", "driver_unload_entry", "teardown_cleanup", "kernel_irp_router", "kernel_ioctl_dispatch", "request_origin_validation", "ioctl_command_validation", "ioctl_payload_decryption", "request_validation", "object_filter_initialization", "object_callback_registration", "object_pre_callback", "process_monitor_initialization", "process_monitor", "process_notify_api_wrapper", "file_minifilter", "dynamic_kernel_api_resolver"} {
		if !projectStructurePackHasCriticalRole(pack, role) {
			t.Fatalf("expected critical role %s in %+v", role, pack.CriticalAnchors)
		}
	}
	for _, needle := range []string{
		"windows_driver",
		"Domain-specific flow map",
		"Verified critical source anchors",
		"Windows kernel/WDM .sys driver, not a DLL",
		"separate user-mode control/client wrappers from kernel-side IRP/IOCTL dispatch and validation",
		"constrained architecture evidence",
		"include both the device-control branch spine and the REQUIRED device-control command spine",
		"Required driver answer facts",
		"Authoritative top-level directories",
		"CLOSED SET: for a top-level directory table",
		"Copy this exact top-level directory table if needed",
		"Never list these paths as top-level directory rows",
		"TavernKernel/BuildCab",
		"TavernKernel/TavernKernelObjectFilter.cpp",
		"Do not add *.h/*.cpp/*.vcxproj/*.sln entries as root directories",
		"mention files only in file/source sections",
		"Copy this exact IOCTL command spine with symbol names",
		"Runtime filter start/registration anchor",
		"TavernKernelObjectFilter::StartObjectFilter (TavernKernel/TavernKernelObjectFilter.cpp:106)",
		"do not replace it with ellipsis",
		"Root folder map (exact sibling paths)",
		"Common/",
		"Do not nest one root folder under another",
		"do not place runtime filter start/registration functions in DriverEntry/Core Initialize",
		"Keep request-origin validation outside the DeviceIoControl command spine",
		"use exact symbol names and exact file:line anchors",
		"IRP_MJ_CREATE validates request origin",
		"control-open validation spine",
		"device-control branch spine",
		"REQUIRED device-control command spine",
		"TavernKernelCore::DecryptIoctlData (TavernKernel/TavernKernelCore.cpp:1208)",
		"TavernKernelCore::IsValidCommand (TavernKernel/TavernKernelCore.cpp:1183)",
		"requestor/control-process checks use accessor or request identity state",
		"object filter state initialization, not callback registration",
		"Finalize/Unload stops and unregisters filters",
		"Control PID/accessor symbols are not Finalize/Unload lifecycle functions",
		"teardown spine",
		"TavernKernelCore::Initialize (TavernKernel/TavernKernelCore.cpp:44)",
		"TavernKernelCore::DeviceIoControlIrpHandleRoutine (TavernKernel/TavernKernelCore.cpp:523)",
		"TavernKernelCore::ValidateRequestorIsTavern (TavernKernel/TavernKernelCore.cpp:1144)",
		"TavernKernelCore::GetControlPid (TavernKernel/TavernKernelCore.cpp:1285)",
		"TavernKernelCore::Finalize (TavernKernel/TavernKernelCore.cpp:194)",
		"TavernKernelCore::UnloadRoutine (TavernKernel/TavernKernelCore.cpp:256)",
		"TavernKernelObjectFilter::Initialize (TavernKernel/TavernKernelObjectFilter.cpp:27)",
		"TavernKernelProcessMonitor::StartProcessMonitor (TavernKernel/TavernKernelProcessMonitor.cpp:122)",
		"TavernKernelProcessMonitor::Initialize (TavernKernel/TavernKernelProcessMonitor.cpp:27)",
		"TavernKernelAPI::TavernPsSetCreateProcessNotifyRoutineEx (TavernKernel/TavernKernelAPI.cpp:711)",
		"TavernKernelAPI::Initialize (TavernKernel/TavernKernelAPI.cpp:27)",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected rendered driver pack to contain %q, got %q", needle, rendered)
		}
	}
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "load/init spine:") && strings.Contains(line, "StartObjectFilter") {
			t.Fatalf("did not expect StartObjectFilter in load/init spine, got %q", line)
		}
		if strings.Contains(line, "device-control command spine:") && strings.Contains(line, "-> TavernKernelCore::ValidateRequestorIsTavern") {
			t.Fatalf("did not expect ValidateRequestorIsTavern as a DeviceIoControl command step, got %q", line)
		}
		if strings.Contains(line, "Root folder map") && strings.Contains(line, "TavernKernel/Common") {
			t.Fatalf("did not expect Common to be nested under TavernKernel, got %q", line)
		}
		if strings.Contains(line, "Root folder map") && strings.Contains(line, ".h/") {
			t.Fatalf("did not expect header files to be rendered as root folders, got %q", line)
		}
	}
	if len(pack.StaleMarkers) != 0 {
		t.Fatalf("expected non-stale cache scope markers to be filtered from stale markers, got %+v", pack.StaleMarkers)
	}
	if strings.Contains(rendered, "Stale or invalidation markers") || strings.Contains(rendered, "no_previous_run") || strings.Contains(rendered, "new_primary_scope") {
		t.Fatalf("did not expect cache scope markers to render as stale, got %q", rendered)
	}
	rootFolders := projectStructureRootFolders(append(pack.Folders,
		DeveloperFolderRecord{Path: "TavernKernelAPI.h", Responsibility: "header file"},
		DeveloperFolderRecord{Path: "TavernKernel/TavernKernelCore.cpp", Responsibility: "source file"},
	))
	for _, folder := range rootFolders {
		if strings.Contains(folder.Path, ".h") || strings.Contains(folder.Path, ".cpp") {
			t.Fatalf("did not expect source/header files to survive root folder filtering, got %+v", rootFolders)
		}
	}
	exclusions := projectStructureTopLevelTableExclusions(pack)
	for _, want := range []string{"TavernKernel/BuildCab", "TavernKernel/TavernKernelCore.cpp"} {
		if !containsString(exclusions, want) {
			t.Fatalf("expected top-level table exclusions to include %q, got %+v", want, exclusions)
		}
	}
	assertCriticalAnchorSymbol(t, pack, "kernel_ioctl_dispatch", "TavernKernelCore::DeviceIoControlIrpHandleRoutine")
	assertCriticalAnchorSymbol(t, pack, "kernel_irp_router", "TavernKernelCore::DefaultIrpHandleRoutine")
	assertCriticalAnchorSymbol(t, pack, "dynamic_kernel_api_resolver", "TavernKernelAPI::Initialize")
	assertCriticalAnchorSymbol(t, pack, "request_validation", "TavernKernelCore::GetControlPid")
	assertCriticalAnchorSymbol(t, pack, "object_filter_initialization", "TavernKernelObjectFilter::Initialize")
	assertCriticalAnchorSymbol(t, pack, "object_callback_registration", "TavernKernelObjectFilter::StartObjectFilter")
	assertCriticalAnchorSymbol(t, pack, "process_monitor_initialization", "TavernKernelProcessMonitor::Initialize")
	assertCriticalAnchorSymbol(t, pack, "process_monitor", "TavernKernelProcessMonitor::StartProcessMonitor")
	assertCriticalAnchorSymbol(t, pack, "process_notify_api_wrapper", "TavernKernelAPI::TavernPsSetCreateProcessNotifyRoutineEx")
	assertCriticalAnchorSymbol(t, pack, "file_minifilter", "TavernKernelFileFilter::Initialize")
	assertCriticalAnchorSymbol(t, pack, "teardown_cleanup", "TavernKernelCore::Finalize")
	assertCriticalAnchorSymbol(t, pack, "driver_unload_entry", "TavernKernelCore::UnloadRoutine")
}

func TestDriverDeveloperDocsPreferConcreteResponsibilities(t *testing.T) {
	run := sampleDriverProjectStructureQARun()
	folders := buildDeveloperFolderRecords(run)
	folderResponsibilities := map[string]string{}
	for _, folder := range folders {
		folderResponsibilities[folder.Path] = folder.Responsibility
	}
	if got := folderResponsibilities["TavernKernel"]; !strings.Contains(got, "kernel driver runtime") {
		t.Fatalf("expected TavernKernel folder to be classified as kernel driver runtime, got %q", got)
	}
	if got := folderResponsibilities["Common"]; !strings.Contains(got, "shared kernel/user-mode") {
		t.Fatalf("expected Common folder to be classified as shared contracts, got %q", got)
	}
	if got := developerIOCTLRole(SymbolRecord{Name: "DeviceIoControlIrpHandleRoutine", CanonicalName: "TavernKernelCore::DeviceIoControlIrpHandleRoutine", Kind: "ioctl_handler", Tags: []string{"ioctl_surface"}}); got != "kernel dispatch or handler" {
		t.Fatalf("expected DeviceIoControlIrpHandleRoutine to be kernel dispatch, got %q", got)
	}
	if got := developerIOCTLRole(SymbolRecord{Name: "IOCTL_TAVERN_CONTROL", Kind: "constant"}); got != "IOCTL code or constant" {
		t.Fatalf("expected explicit IOCTL constant role, got %q", got)
	}
}

func buildAnalysisDocsManifestForTest(run ProjectAnalysisRun) AnalysisDocsManifest {
	manifest := AnalysisDocsManifest{
		SchemaVersion:       analysisDocsManifestSchemaVersion,
		MinReaderVersion:    analysisDocsManifestMinReaderVersion,
		CompatibilityPolicy: analysisDocsManifestCompatPolicy,
		SchemaNotes:         []string{analysisDocsManifestCurrentSchemaNote},
		RunID:               run.Summary.RunID,
		Goal:                run.Summary.Goal,
		Mode:                run.Summary.Mode,
		GeneratedAt:         time.Now().UTC(),
		SourceArtifacts:     analysisDocsSourceArtifacts(run),
		ReuseTargets:        analysisDocsReuseTargets(),
		FuzzTargets:         analysisFuzzTargetCatalog(run),
		VerificationMatrix:  analysisVerificationMatrixCatalog(run),
	}
	for _, name := range analysisGeneratedDocNames() {
		manifest.Documents = append(manifest.Documents, AnalysisGeneratedDoc{
			Name:          name,
			Title:         analysisDocTitle(name),
			Kind:          analysisDocKind(name),
			Path:          name,
			GeneratedAt:   manifest.GeneratedAt,
			SourceAnchors: analysisDocSourceAnchors(run, name),
			Confidence:    analysisDocConfidence(run, name),
			StaleMarkers:  analysisDocStaleMarkers(run, name),
			ReuseTargets:  analysisDocReuseTargets(name),
			QueryIntents:  analysisDocQueryIntents(name),
			Priority:      analysisDocPriority(name),
			Sections:      analysisDocSections(run, name),
		})
	}
	manifest.DocumentCount = len(manifest.Documents)
	return normalizeAnalysisDocsManifest(manifest)
}

func sampleProjectStructureQARun() ProjectAnalysisRun {
	generatedAt := time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)
	files := []ScannedFile{
		{Path: "Source/GuardRuntime/Private/Startup.cpp", Directory: "Source/GuardRuntime/Private", Extension: ".cpp", LineCount: 120, IsEntrypoint: true, ImportanceScore: 95},
		{Path: "Source/GuardRuntime/Private/IoctlDispatch.cpp", Directory: "Source/GuardRuntime/Private", Extension: ".cpp", LineCount: 240, ImportanceScore: 100},
		{Path: "Source/GuardRuntime/Private/RpcDispatch.cpp", Directory: "Source/GuardRuntime/Private", Extension: ".cpp", LineCount: 210, ImportanceScore: 90},
		{Path: "Source/GuardRuntime/Public/GuardRuntime.h", Directory: "Source/GuardRuntime/Public", Extension: ".h", LineCount: 80, ImportanceScore: 80},
		{Path: "Source/GuardRuntime/GuardRuntime.Build.cs", Directory: "Source/GuardRuntime", Extension: ".cs", LineCount: 40, IsManifest: true, ImportanceScore: 85},
		{Path: "Source/GuardRuntime/Tests/GuardRuntimeTests.cpp", Directory: "Source/GuardRuntime/Tests", Extension: ".cpp", LineCount: 90, ImportanceScore: 60},
	}
	buildContext := BuildContextRecord{
		ID:        "buildctx:module:GuardRuntime",
		Name:      "GuardRuntime module",
		Kind:      "unreal_module",
		Directory: "Source/GuardRuntime",
		Module:    "GuardRuntime",
		Files: []string{
			"Source/GuardRuntime/Private/Startup.cpp",
			"Source/GuardRuntime/Private/IoctlDispatch.cpp",
			"Source/GuardRuntime/Private/RpcDispatch.cpp",
			"Source/GuardRuntime/Public/GuardRuntime.h",
		},
		Source: "Source/GuardRuntime/GuardRuntime.Build.cs",
	}
	symbols := []SymbolRecord{
		{ID: "func:Startup", Name: "Startup", CanonicalName: "GuardRuntime::Startup", Kind: "function", File: "Source/GuardRuntime/Private/Startup.cpp", Module: "GuardRuntime", BuildContextID: buildContext.ID, StartLine: 12, EndLine: 60, Tags: []string{"startup", "entrypoint"}},
		{ID: "func:DispatchIoctl", Name: "DispatchIoctl", CanonicalName: "GuardRuntime::DispatchIoctl", Kind: "ioctl_handler", File: "Source/GuardRuntime/Private/IoctlDispatch.cpp", Module: "GuardRuntime", BuildContextID: buildContext.ID, StartLine: 20, EndLine: 120, Tags: []string{"ioctl", "security", "input_surface"}},
		{ID: "func:ValidateRequest", Name: "ValidateRequest", CanonicalName: "GuardRuntime::ValidateRequest", Kind: "function", File: "Source/GuardRuntime/Private/IoctlDispatch.cpp", Module: "GuardRuntime", BuildContextID: buildContext.ID, StartLine: 130, EndLine: 180, Tags: []string{"validation", "security"}},
		{ID: "func:DispatchRpc", Name: "DispatchRpc", CanonicalName: "GuardRuntime::DispatchRpc", Kind: "rpc_handler", File: "Source/GuardRuntime/Private/RpcDispatch.cpp", Module: "GuardRuntime", BuildContextID: buildContext.ID, StartLine: 30, EndLine: 140, Tags: []string{"rpc", "security"}},
	}
	indexV2 := SemanticIndexV2{
		RunID:          "run-qa-fixture",
		Goal:           "map GuardRuntime structure",
		Root:           "C:/repo",
		GeneratedAt:    generatedAt,
		PrimaryStartup: "GuardRuntime",
		Files: []FileRecord{
			{Path: "Source/GuardRuntime/Private/Startup.cpp", Directory: "Source/GuardRuntime/Private", Language: "cpp", IsEntrypoint: true, ImportanceScore: 95, ModuleHints: []string{"GuardRuntime"}, BuildContextIDs: []string{buildContext.ID}},
			{Path: "Source/GuardRuntime/Private/IoctlDispatch.cpp", Directory: "Source/GuardRuntime/Private", Language: "cpp", ImportanceScore: 100, Tags: []string{"ioctl", "security"}, ModuleHints: []string{"GuardRuntime"}, BuildContextIDs: []string{buildContext.ID}},
			{Path: "Source/GuardRuntime/Private/RpcDispatch.cpp", Directory: "Source/GuardRuntime/Private", Language: "cpp", ImportanceScore: 90, Tags: []string{"rpc", "security"}, ModuleHints: []string{"GuardRuntime"}, BuildContextIDs: []string{buildContext.ID}},
			{Path: "Source/GuardRuntime/GuardRuntime.Build.cs", Directory: "Source/GuardRuntime", Language: "csharp", IsManifest: true, ImportanceScore: 85, ModuleHints: []string{"GuardRuntime"}, BuildContextIDs: []string{buildContext.ID}},
		},
		BuildContexts: []BuildContextRecord{buildContext},
		Symbols:       symbols,
		CallEdges: []CallEdge{
			{SourceID: "func:Startup", TargetID: "func:DispatchIoctl", Type: "calls", Evidence: []string{"Source/GuardRuntime/Private/Startup.cpp", "Source/GuardRuntime/Private/IoctlDispatch.cpp"}},
			{SourceID: "func:DispatchIoctl", TargetID: "func:ValidateRequest", Type: "calls", Evidence: []string{"Source/GuardRuntime/Private/IoctlDispatch.cpp"}},
			{SourceID: "func:Startup", TargetID: "func:DispatchRpc", Type: "calls", Evidence: []string{"Source/GuardRuntime/Private/Startup.cpp", "Source/GuardRuntime/Private/RpcDispatch.cpp"}},
		},
		References: []ReferenceRecord{
			{SourceID: "func:DispatchIoctl", SourceFile: "Source/GuardRuntime/Private/IoctlDispatch.cpp", TargetID: "func:ValidateRequest", TargetPath: "Source/GuardRuntime/Private/IoctlDispatch.cpp", Type: "validates_with", Evidence: []string{"Source/GuardRuntime/Private/IoctlDispatch.cpp"}},
		},
		BuildOwnershipEdges: []BuildOwnershipEdge{
			{SourceID: buildContext.ID, TargetID: "func:Startup", Type: "compiles_symbol", Evidence: []string{"Source/GuardRuntime/GuardRuntime.Build.cs", "Source/GuardRuntime/Private/Startup.cpp"}},
			{SourceID: buildContext.ID, TargetID: "func:DispatchIoctl", Type: "compiles_symbol", Evidence: []string{"Source/GuardRuntime/GuardRuntime.Build.cs", "Source/GuardRuntime/Private/IoctlDispatch.cpp"}},
		},
		GeneratedCodeEdges: []GeneratedCodeEdge{
			{SourceFile: "Source/GuardRuntime/GuardRuntime.Build.cs", TargetID: "Intermediate/GuardRuntime.generated.h", Type: "generates", Evidence: []string{"Source/GuardRuntime/GuardRuntime.Build.cs"}},
		},
		OverlayEdges: []OverlayEdge{
			{SourceID: "func:DispatchIoctl", TargetID: "entity:ioctl_surface", Type: "issues_ioctl", Domain: "ioctl_surface", Evidence: []string{"Source/GuardRuntime/Private/IoctlDispatch.cpp"}},
			{SourceID: "func:DispatchRpc", TargetID: "entity:rpc_surface", Type: "dispatches_rpc", Domain: "rpc_surface", Evidence: []string{"Source/GuardRuntime/Private/RpcDispatch.cpp"}},
		},
	}
	unrealGraph := UnrealSemanticGraph{
		RunID:       "run-qa-fixture",
		Goal:        "map GuardRuntime structure",
		Root:        "C:/repo",
		GeneratedAt: generatedAt,
		Nodes: []UnrealSemanticNode{
			{ID: "module:GuardRuntime", Kind: "module", Name: "GuardRuntime", Module: "GuardRuntime", File: "Source/GuardRuntime/GuardRuntime.Build.cs"},
			{ID: "type:AGuardController", Kind: "uclass", Name: "AGuardController", Module: "GuardRuntime", File: "Source/GuardRuntime/Public/GuardRuntime.h"},
		},
		Edges: []UnrealSemanticEdge{
			{Source: "module:GuardRuntime", Target: "type:AGuardController", Type: "declares", Attributes: map[string]string{"file": "Source/GuardRuntime/Public/GuardRuntime.h"}},
			{Source: "type:AGuardController", Target: "func:DispatchRpc", Type: "rpc_server", Attributes: map[string]string{"file": "Source/GuardRuntime/Private/RpcDispatch.cpp"}},
		},
	}
	return ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:          "run-qa-fixture",
			Goal:           "map GuardRuntime structure",
			Mode:           "map",
			Status:         "completed",
			StartedAt:      generatedAt,
			CompletedAt:    generatedAt,
			ApprovedShards: 2,
			TotalShards:    2,
		},
		Snapshot: ProjectSnapshot{
			Root:                "C:/repo",
			ModulePath:          "guard/runtime",
			AnalysisMode:        "map",
			GeneratedAt:         generatedAt,
			Files:               files,
			Directories:         []string{"Source/GuardRuntime", "Source/GuardRuntime/Private", "Source/GuardRuntime/Public", "Source/GuardRuntime/Tests"},
			ManifestFiles:       []string{"Source/GuardRuntime/GuardRuntime.Build.cs"},
			EntrypointFiles:     []string{"Source/GuardRuntime/Private/Startup.cpp"},
			PrimaryStartup:      "GuardRuntime",
			BuildContexts:       []BuildContextRecord{buildContext},
			PrimaryUnrealModule: "GuardRuntime",
			RuntimeEdges: []RuntimeEdge{
				{Source: "Source/GuardRuntime/Private/Startup.cpp", Target: "Source/GuardRuntime/Private/IoctlDispatch.cpp", Kind: "startup_dispatch", Confidence: "high", Evidence: []string{"Source/GuardRuntime/Private/Startup.cpp", "Source/GuardRuntime/Private/IoctlDispatch.cpp"}},
				{Source: "Source/GuardRuntime/Private/Startup.cpp", Target: "Source/GuardRuntime/Private/RpcDispatch.cpp", Kind: "startup_rpc", Confidence: "high", Evidence: []string{"Source/GuardRuntime/Private/Startup.cpp", "Source/GuardRuntime/Private/RpcDispatch.cpp"}},
			},
			ProjectEdges: []ProjectEdge{
				{Source: "GuardRuntime", Target: "IOCTL surface", Type: "trust_boundary", Confidence: "high", Evidence: []string{"Source/GuardRuntime/Private/IoctlDispatch.cpp"}},
			},
			TotalFiles: len(files),
			TotalLines: 780,
		},
		KnowledgePack: KnowledgePack{
			RunID:          "run-qa-fixture",
			Goal:           "map GuardRuntime structure",
			AnalysisMode:   "map",
			Root:           "C:/repo",
			GeneratedAt:    generatedAt,
			ProjectSummary: "GuardRuntime starts from Startup.cpp, dispatches IOCTL and RPC requests, and validates input before crossing trust boundaries.",
			PrimaryStartup: "GuardRuntime",
			TopImportantFiles: []string{
				"Source/GuardRuntime/Private/Startup.cpp",
				"Source/GuardRuntime/Private/IoctlDispatch.cpp",
				"Source/GuardRuntime/Private/RpcDispatch.cpp",
				"Source/GuardRuntime/Public/GuardRuntime.h",
				"Source/GuardRuntime/GuardRuntime.Build.cs",
			},
			HighRiskFiles: []string{
				"Source/GuardRuntime/Private/IoctlDispatch.cpp",
				"Source/GuardRuntime/Private/RpcDispatch.cpp",
			},
			ArchitectureGroups: []string{"Runtime dispatch", "Security validation", "Build ownership"},
			Subsystems: []KnowledgeSubsystem{
				{
					Title:            "GuardRuntime Dispatch",
					Group:            "Runtime",
					Responsibilities: []string{"Own startup handoff", "Dispatch IOCTL and RPC requests"},
					KeyFiles:         []string{"Source/GuardRuntime/Private/Startup.cpp", "Source/GuardRuntime/Private/IoctlDispatch.cpp", "Source/GuardRuntime/Private/RpcDispatch.cpp"},
					EvidenceFiles:    []string{"Source/GuardRuntime/Public/GuardRuntime.h"},
					EntryPoints:      []string{"GuardRuntime::Startup"},
					Dependencies:     []string{"GuardRuntime module"},
				},
			},
		},
		SemanticIndexV2: indexV2,
		UnrealGraph:     unrealGraph,
	}
}

func sampleDriverProjectStructureQARun() ProjectAnalysisRun {
	generatedAt := time.Date(2026, 4, 28, 1, 52, 10, 0, time.UTC)
	buildContext := BuildContextRecord{
		ID:        "buildctx:project:TavernKernel",
		Name:      "TavernKernel WDM driver",
		Kind:      "wdm_driver",
		Directory: "TavernKernel",
		Project:   "TavernKernel",
		Target:    "tvk.sys",
		Files: []string{
			"TavernKernel/TavernKernel.cpp",
			"TavernKernel/TavernKernelCore.cpp",
			"TavernKernel/TavernKernelAPI.cpp",
			"TavernKernel/TavernKernelObjectFilter.cpp",
			"TavernKernel/TavernKernelProcessMonitor.cpp",
			"TavernKernel/TavernKernelPolicy.cpp",
		},
		Source: "TavernKernel/TavernKernel.vcxproj",
	}
	files := []ScannedFile{
		{Path: "TavernKernel/TavernKernel.cpp", Directory: "TavernKernel", Extension: ".cpp", LineCount: 80, IsEntrypoint: true, ImportanceScore: 92},
		{Path: "TavernKernel/TavernKernelCore.cpp", Directory: "TavernKernel", Extension: ".cpp", LineCount: 1600, ImportanceScore: 100},
		{Path: "TavernKernel/TavernKernelAPI.cpp", Directory: "TavernKernel", Extension: ".cpp", LineCount: 980, ImportanceScore: 90},
		{Path: "TavernKernel/TavernKernelObjectFilter.cpp", Directory: "TavernKernel", Extension: ".cpp", LineCount: 430, ImportanceScore: 88},
		{Path: "TavernKernel/TavernKernelProcessMonitor.cpp", Directory: "TavernKernel", Extension: ".cpp", LineCount: 520, ImportanceScore: 86},
		{Path: "TavernKernel/TavernKernelPolicy.cpp", Directory: "TavernKernel", Extension: ".cpp", LineCount: 680, ImportanceScore: 84},
		{Path: "TavernKernel/BuildCab/TavernKernel.inf", Directory: "TavernKernel/BuildCab", Extension: ".inf", LineCount: 70, IsManifest: true, ImportanceScore: 72},
		{Path: "TavernKernel/TavernKernel.vcxproj", Directory: "TavernKernel", Extension: ".vcxproj", LineCount: 210, IsManifest: true, ImportanceScore: 80},
		{Path: "TavernKernelTestConsole/TavernKernelManager.cpp", Directory: "TavernKernelTestConsole", Extension: ".cpp", LineCount: 650, ImportanceScore: 78},
		{Path: "Common/UserCommon.h", Directory: "Common", Extension: ".h", LineCount: 160, ImportanceScore: 76},
	}
	symbols := []SymbolRecord{
		{ID: "func:DriverEntry", Name: "DriverEntry", CanonicalName: "DriverEntry", Kind: "function", File: "TavernKernel/TavernKernel.cpp", BuildContextID: buildContext.ID, StartLine: 1, Tags: []string{"entrypoint"}},
		{ID: "func:TavernKernelCore::Initialize", Name: "Initialize", CanonicalName: "TavernKernelCore::Initialize", Kind: "function", File: "TavernKernel/TavernKernelCore.cpp", BuildContextID: buildContext.ID, StartLine: 44, Tags: []string{"security_boundary"}},
		{ID: "func:TavernKernelCore::Finalize", Name: "Finalize", CanonicalName: "TavernKernelCore::Finalize", Kind: "function", File: "TavernKernel/TavernKernelCore.cpp", BuildContextID: buildContext.ID, StartLine: 194, Tags: []string{"member_function"}},
		{ID: "func:TavernKernelCore::UnloadRoutine", Name: "UnloadRoutine", CanonicalName: "TavernKernelCore::UnloadRoutine", Kind: "function", File: "TavernKernel/TavernKernelCore.cpp", BuildContextID: buildContext.ID, StartLine: 256, Tags: []string{"member_function"}},
		{ID: "func:TavernKernelCore::CreateControlDevice", Name: "CreateControlDevice", CanonicalName: "TavernKernelCore::CreateControlDevice", Kind: "function", File: "TavernKernel/TavernKernelCore.cpp", BuildContextID: buildContext.ID, StartLine: 389, Tags: []string{"control_surface"}},
		{ID: "ioctl:TavernKernelCore::DefaultIrpHandleRoutine", Name: "DefaultIrpHandleRoutine", CanonicalName: "TavernKernelCore::DefaultIrpHandleRoutine", Kind: "ioctl_handler", File: "TavernKernel/TavernKernelCore.cpp", BuildContextID: buildContext.ID, StartLine: 468, Tags: []string{"ioctl_surface", "security_surface"}},
		{ID: "ioctl:TavernKernelCore::DeviceIoControlIrpHandleRoutine", Name: "DeviceIoControlIrpHandleRoutine", CanonicalName: "TavernKernelCore::DeviceIoControlIrpHandleRoutine", Kind: "ioctl_handler", File: "TavernKernel/TavernKernelCore.cpp", BuildContextID: buildContext.ID, StartLine: 523, Tags: []string{"ioctl_surface", "security_surface"}},
		{ID: "func:TavernKernelCore::ValidateRequestorIsTavern", Name: "ValidateRequestorIsTavern", CanonicalName: "TavernKernelCore::ValidateRequestorIsTavern", Kind: "function", File: "TavernKernel/TavernKernelCore.cpp", BuildContextID: buildContext.ID, StartLine: 1144, Tags: []string{"control_surface"}},
		{ID: "ioctl:TavernKernelCore::IsValidCommand", Name: "IsValidCommand", CanonicalName: "TavernKernelCore::IsValidCommand", Kind: "ioctl_handler", File: "TavernKernel/TavernKernelCore.cpp", BuildContextID: buildContext.ID, StartLine: 1183, Tags: []string{"ioctl_surface", "security_surface", "tamper_surface", "security_boundary"}},
		{ID: "func:TavernKernelCore::DecryptIoctlData", Name: "DecryptIoctlData", CanonicalName: "TavernKernelCore::DecryptIoctlData", Kind: "function", File: "TavernKernel/TavernKernelCore.cpp", BuildContextID: buildContext.ID, StartLine: 1208, Tags: []string{"ioctl_surface"}},
		{ID: "func:TavernKernelCore::GetControlPid", Name: "GetControlPid", CanonicalName: "TavernKernelCore::GetControlPid", Kind: "function", File: "TavernKernel/TavernKernelCore.cpp", BuildContextID: buildContext.ID, StartLine: 1285, Tags: []string{"control_surface"}},
		{ID: "func:TavernKernelObjectFilter::Initialize", Name: "Initialize", CanonicalName: "TavernKernelObjectFilter::Initialize", Kind: "function", File: "TavernKernel/TavernKernelObjectFilter.cpp", BuildContextID: buildContext.ID, StartLine: 27, Tags: []string{"handle_surface", "security_surface"}},
		{ID: "handle:TavernKernelObjectFilter::StartObjectFilter", Name: "StartObjectFilter", CanonicalName: "TavernKernelObjectFilter::StartObjectFilter", Kind: "handle_path", File: "TavernKernel/TavernKernelObjectFilter.cpp", BuildContextID: buildContext.ID, StartLine: 106, Tags: []string{"handle_surface", "security_surface"}},
		{ID: "entity:TavernKernel/TavernKernelObjectFilter.cpp", Name: "entity:TavernKernel/TavernKernelObjectFilter.cpp", CanonicalName: "entity:TavernKernel/TavernKernelObjectFilter.cpp", Kind: "entity", Tags: []string{"handle_surface", "security_surface"}},
		{ID: "handle:TavernKernelObjectFilter::TavernKernelProcessObjectPreCallback", Name: "TavernKernelProcessObjectPreCallback", CanonicalName: "TavernKernelObjectFilter::TavernKernelProcessObjectPreCallback", Kind: "handle_path", File: "TavernKernel/TavernKernelObjectFilter.cpp", BuildContextID: buildContext.ID, StartLine: 277, Tags: []string{"handle_surface", "security_surface"}},
		{ID: "func:TavernKernelProcessMonitor::Initialize", Name: "Initialize", CanonicalName: "TavernKernelProcessMonitor::Initialize", Kind: "function", File: "TavernKernel/TavernKernelProcessMonitor.cpp", BuildContextID: buildContext.ID, StartLine: 27, Tags: []string{"control_surface"}},
		{ID: "func:TavernKernelProcessMonitor::StartProcessMonitor", Name: "StartProcessMonitor", CanonicalName: "TavernKernelProcessMonitor::StartProcessMonitor", Kind: "function", File: "TavernKernel/TavernKernelProcessMonitor.cpp", BuildContextID: buildContext.ID, StartLine: 122, Tags: []string{"control_surface"}},
		{ID: "func:TavernKernelProcessMonitor::TavernKernelProcessNotifyRoutineEx", Name: "TavernKernelProcessNotifyRoutineEx", CanonicalName: "TavernKernelProcessMonitor::TavernKernelProcessNotifyRoutineEx", Kind: "function", File: "TavernKernel/TavernKernelProcessMonitor.cpp", BuildContextID: buildContext.ID, StartLine: 411, Tags: []string{"control_surface"}},
		{ID: "func:TavernKernelFileFilter::Initialize", Name: "Initialize", CanonicalName: "TavernKernelFileFilter::Initialize", Kind: "function", File: "TavernKernel/TavernKernelFileFilter.cpp", BuildContextID: buildContext.ID, StartLine: 28, Tags: []string{"tamper_surface", "security_boundary"}},
		{ID: "handle:TavernKernelAPI::Initialize", Name: "Initialize", CanonicalName: "TavernKernelAPI::Initialize", Kind: "handle_path", File: "TavernKernel/TavernKernelAPI.cpp", BuildContextID: buildContext.ID, StartLine: 27, Tags: []string{"handle_surface", "security_surface"}},
		{ID: "func:TavernKernelAPI::GetExportFunctionAddress", Name: "GetExportFunctionAddress", CanonicalName: "TavernKernelAPI::GetExportFunctionAddress", Kind: "function", File: "TavernKernel/TavernKernelAPI.cpp", BuildContextID: buildContext.ID, StartLine: 358, Tags: []string{"tamper_surface", "security_boundary"}},
		{ID: "func:TavernKernelAPI::TavernPsSetCreateProcessNotifyRoutineEx", Name: "TavernPsSetCreateProcessNotifyRoutineEx", CanonicalName: "TavernKernelAPI::TavernPsSetCreateProcessNotifyRoutineEx", Kind: "function", File: "TavernKernel/TavernKernelAPI.cpp", BuildContextID: buildContext.ID, StartLine: 711, Tags: []string{"member_function"}},
		{ID: "handle:TavernKernelAPI::TavernObRegisterCallbacks", Name: "TavernObRegisterCallbacks", CanonicalName: "TavernKernelAPI::TavernObRegisterCallbacks", Kind: "handle_path", File: "TavernKernel/TavernKernelAPI.cpp", BuildContextID: buildContext.ID, StartLine: 504, Tags: []string{"handle_surface", "security_surface"}},
		{ID: "entity:TavernKernel/TavernKernelAPI.cpp", Name: "entity:TavernKernel/TavernKernelAPI.cpp", CanonicalName: "entity:TavernKernel/TavernKernelAPI.cpp", Kind: "entity", Tags: []string{"memory_surface", "security_surface"}},
		{ID: "memory:TavernKernelAPI::TavernMmCopyVirtualMemory", Name: "TavernMmCopyVirtualMemory", CanonicalName: "TavernKernelAPI::TavernMmCopyVirtualMemory", Kind: "memory_path", File: "TavernKernel/TavernKernelAPI.cpp", BuildContextID: buildContext.ID, StartLine: 945, Tags: []string{"memory_surface", "security_surface"}},
		{ID: "ioctl:TavernKernelManager::AddProtectionTargetProcessPath", Name: "AddProtectionTargetProcessPath", CanonicalName: "TavernKernelManager::AddProtectionTargetProcessPath", Kind: "ioctl_handler", File: "TavernKernelTestConsole/TavernKernelManager.cpp", BuildContextID: "buildctx:project:TavernKernelTestConsole", StartLine: 504, Tags: []string{"ioctl_surface", "security_surface"}},
		{ID: "ioctl:TavernKernelManager::ControlOperation", Name: "ControlOperation", CanonicalName: "TavernKernelManager::ControlOperation", Kind: "ioctl_handler", File: "TavernKernelTestConsole/TavernKernelManager.cpp", BuildContextID: "buildctx:project:TavernKernelTestConsole", StartLine: 581, Tags: []string{"ioctl_surface", "security_surface"}},
	}
	indexV2 := SemanticIndexV2{
		RunID:          "run-driver-fixture",
		Goal:           "map TavernKernel driver architecture",
		Root:           "C:/repo/TavernKernel",
		GeneratedAt:    generatedAt,
		PrimaryStartup: "TavernKernelTestConsole",
		Files: []FileRecord{
			{Path: "TavernKernel/TavernKernel.cpp", Directory: "TavernKernel", Language: "cpp", IsEntrypoint: true, ImportanceScore: 92, Tags: []string{"driver", "entrypoint"}, ModuleHints: []string{"TavernKernel"}, BuildContextIDs: []string{buildContext.ID}},
			{Path: "TavernKernel/TavernKernelCore.cpp", Directory: "TavernKernel", Language: "cpp", ImportanceScore: 100, Tags: []string{"ioctl", "security", "kernel_driver"}, ModuleHints: []string{"TavernKernel"}, BuildContextIDs: []string{buildContext.ID}},
			{Path: "TavernKernel/TavernKernelAPI.cpp", Directory: "TavernKernel", Language: "cpp", ImportanceScore: 90, Tags: []string{"dynamic_api", "kernel_driver"}, ModuleHints: []string{"TavernKernel"}, BuildContextIDs: []string{buildContext.ID}},
			{Path: "TavernKernel/TavernKernelObjectFilter.cpp", Directory: "TavernKernel", Language: "cpp", ImportanceScore: 88, Tags: []string{"handle_surface", "security"}, ModuleHints: []string{"TavernKernel"}, BuildContextIDs: []string{buildContext.ID}},
			{Path: "TavernKernel/BuildCab/TavernKernel.inf", Directory: "TavernKernel/BuildCab", Language: "config", IsManifest: true, ImportanceScore: 72, Tags: []string{"driver_inf"}, ModuleHints: []string{"TavernKernel"}, BuildContextIDs: []string{buildContext.ID}},
			{Path: "TavernKernel/TavernKernel.vcxproj", Directory: "TavernKernel", Language: "xml", IsManifest: true, ImportanceScore: 80, Tags: []string{"wdm", "driver_project"}, ModuleHints: []string{"TavernKernel"}, BuildContextIDs: []string{buildContext.ID}},
		},
		BuildContexts: []BuildContextRecord{buildContext},
		Symbols:       symbols,
		CallEdges: []CallEdge{
			{SourceID: "func:DriverEntry", TargetID: "func:TavernKernelCore::Initialize", Type: "calls", Evidence: []string{"TavernKernel/TavernKernel.cpp"}},
			{SourceID: "func:TavernKernelCore::Initialize", TargetID: "handle:TavernKernelAPI::Initialize", Type: "calls", Evidence: []string{"TavernKernel/TavernKernelCore.cpp", "TavernKernel/TavernKernelAPI.cpp"}},
			{SourceID: "func:TavernKernelCore::Initialize", TargetID: "func:TavernKernelCore::CreateControlDevice", Type: "calls", Evidence: []string{"TavernKernel/TavernKernelCore.cpp"}},
			{SourceID: "func:TavernKernelCore::Initialize", TargetID: "func:TavernKernelObjectFilter::Initialize", Type: "calls", Evidence: []string{"TavernKernel/TavernKernelCore.cpp", "TavernKernel/TavernKernelObjectFilter.cpp"}},
			{SourceID: "func:TavernKernelCore::Initialize", TargetID: "func:TavernKernelProcessMonitor::Initialize", Type: "calls", Evidence: []string{"TavernKernel/TavernKernelCore.cpp", "TavernKernel/TavernKernelProcessMonitor.cpp"}},
			{SourceID: "func:TavernKernelCore::Initialize", TargetID: "func:TavernKernelProcessMonitor::StartProcessMonitor", Type: "calls", Evidence: []string{"TavernKernel/TavernKernelCore.cpp", "TavernKernel/TavernKernelProcessMonitor.cpp"}},
			{SourceID: "func:TavernKernelCore::UnloadRoutine", TargetID: "func:TavernKernelCore::Finalize", Type: "calls", Evidence: []string{"TavernKernel/TavernKernelCore.cpp"}},
			{SourceID: "ioctl:TavernKernelCore::DefaultIrpHandleRoutine", TargetID: "func:TavernKernelCore::ValidateRequestorIsTavern", Type: "calls", Evidence: []string{"TavernKernel/TavernKernelCore.cpp"}},
			{SourceID: "ioctl:TavernKernelCore::DefaultIrpHandleRoutine", TargetID: "ioctl:TavernKernelCore::DeviceIoControlIrpHandleRoutine", Type: "calls", Evidence: []string{"TavernKernel/TavernKernelCore.cpp"}},
			{SourceID: "ioctl:TavernKernelCore::DeviceIoControlIrpHandleRoutine", TargetID: "ioctl:TavernKernelCore::IsValidCommand", Type: "calls", Evidence: []string{"TavernKernel/TavernKernelCore.cpp"}},
			{SourceID: "ioctl:TavernKernelCore::DeviceIoControlIrpHandleRoutine", TargetID: "func:TavernKernelCore::DecryptIoctlData", Type: "calls", Evidence: []string{"TavernKernel/TavernKernelCore.cpp"}},
			{SourceID: "handle:TavernKernelObjectFilter::StartObjectFilter", TargetID: "handle:TavernKernelAPI::TavernObRegisterCallbacks", Type: "calls", Evidence: []string{"TavernKernel/TavernKernelObjectFilter.cpp", "TavernKernel/TavernKernelAPI.cpp"}},
			{SourceID: "func:TavernKernelProcessMonitor::StartProcessMonitor", TargetID: "func:TavernKernelAPI::TavernPsSetCreateProcessNotifyRoutineEx", Type: "calls", Evidence: []string{"TavernKernel/TavernKernelProcessMonitor.cpp", "TavernKernel/TavernKernelAPI.cpp"}},
		},
		BuildOwnershipEdges: []BuildOwnershipEdge{
			{SourceID: buildContext.ID, TargetID: "func:DriverEntry", Type: "compiles_symbol", Evidence: []string{"TavernKernel/TavernKernel.vcxproj", "TavernKernel/TavernKernel.cpp"}},
			{SourceID: buildContext.ID, TargetID: "ioctl:TavernKernelCore::DeviceIoControlIrpHandleRoutine", Type: "compiles_symbol", Evidence: []string{"TavernKernel/TavernKernel.vcxproj", "TavernKernel/TavernKernelCore.cpp"}},
		},
		OverlayEdges: []OverlayEdge{
			{SourceID: "ioctl:TavernKernelCore::DeviceIoControlIrpHandleRoutine", TargetID: "entity:ioctl_surface", Type: "receives_ioctl", Domain: "ioctl_surface", Evidence: []string{"TavernKernel/TavernKernelCore.cpp"}},
			{SourceID: "handle:TavernKernelObjectFilter::TavernKernelProcessObjectPreCallback", TargetID: "entity:handle_surface", Type: "filters_handle", Domain: "handle_surface", Evidence: []string{"TavernKernel/TavernKernelObjectFilter.cpp"}},
			{SourceID: "memory:TavernKernelAPI::TavernMmCopyVirtualMemory", TargetID: "entity:memory_surface", Type: "copies_memory", Domain: "memory_surface", Evidence: []string{"TavernKernel/TavernKernelAPI.cpp"}},
		},
	}
	return ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:       "run-driver-fixture",
			Goal:        "map TavernKernel driver architecture",
			Mode:        "map",
			Status:      "completed",
			StartedAt:   generatedAt,
			CompletedAt: generatedAt,
		},
		Snapshot: ProjectSnapshot{
			Root:            "C:/repo/TavernKernel",
			ModulePath:      "TavernKernel/TavernKernel",
			AnalysisMode:    "map",
			GeneratedAt:     generatedAt,
			Files:           files,
			Directories:     []string{"TavernKernel", "TavernKernel/BuildCab", "TavernKernelTestConsole", "Common"},
			ManifestFiles:   []string{"TavernKernel/TavernKernel.vcxproj", "TavernKernel/BuildCab/TavernKernel.inf"},
			EntrypointFiles: []string{"TavernKernel/TavernKernel.cpp", "TavernKernelTestConsole/TavernKernelTestConsole.cpp"},
			PrimaryStartup:  "TavernKernelTestConsole",
			BuildContexts:   []BuildContextRecord{buildContext},
			RuntimeEdges: []RuntimeEdge{
				{Source: "TavernKernel/TavernKernel.cpp", Target: "TavernKernel/TavernKernelCore.cpp", Kind: "driver_entry_initializes_core", Confidence: "high", Evidence: []string{"TavernKernel/TavernKernel.cpp", "TavernKernel/TavernKernelCore.cpp"}},
				{Source: "TavernKernel/TavernKernelCore.cpp", Target: "TavernKernel/TavernKernelObjectFilter.cpp", Kind: "core_enables_object_filter", Confidence: "medium", Evidence: []string{"TavernKernel/TavernKernelCore.cpp", "TavernKernel/TavernKernelObjectFilter.cpp"}},
			},
			ProjectEdges: []ProjectEdge{
				{Source: "TavernKernelTestConsole", Target: "TavernKernelCore::DeviceIoControlIrpHandleRoutine", Type: "user_to_kernel_ioctl", Confidence: "high", Evidence: []string{"TavernKernelTestConsole/TavernKernelManager.cpp", "TavernKernel/TavernKernelCore.cpp"}},
			},
			TotalFiles: len(files),
			TotalLines: 5200,
		},
		KnowledgePack: KnowledgePack{
			RunID:          "run-driver-fixture",
			Goal:           "map TavernKernel driver architecture",
			AnalysisMode:   "map",
			Root:           "C:/repo/TavernKernel",
			GeneratedAt:    generatedAt,
			ProjectSummary: "TavernKernel is a Windows kernel/WDM .sys driver with a user-mode test console, IOCTL control device, process monitor, object filter, dynamic kernel API resolver, and policy enforcement layer.",
			PrimaryStartup: "TavernKernelTestConsole",
			TopImportantFiles: []string{
				"TavernKernel/TavernKernel.cpp",
				"TavernKernel/TavernKernelCore.cpp",
				"TavernKernel/TavernKernelAPI.cpp",
				"TavernKernel/TavernKernelObjectFilter.cpp",
				"TavernKernel/TavernKernelProcessMonitor.cpp",
				"TavernKernel/TavernKernel.vcxproj",
			},
			HighRiskFiles: []string{
				"TavernKernel/TavernKernelCore.cpp",
				"TavernKernel/TavernKernelObjectFilter.cpp",
				"TavernKernel/TavernKernelAPI.cpp",
			},
			ArchitectureGroups: []string{"Windows kernel driver", "IOCTL control surface", "Object and process filtering", "Dynamic kernel API resolution"},
			Subsystems: []KnowledgeSubsystem{
				{Title: "Kernel Driver Core", Group: "Driver", Responsibilities: []string{"Own DriverEntry handoff", "Create control device", "Dispatch IOCTLs"}, KeyFiles: []string{"TavernKernel/TavernKernel.cpp", "TavernKernel/TavernKernelCore.cpp"}, EntryPoints: []string{"DriverEntry", "TavernKernelCore::DeviceIoControlIrpHandleRoutine"}},
				{Title: "Object And Process Enforcement", Group: "Security", Responsibilities: []string{"Filter process and thread handles", "Track process lifecycle"}, KeyFiles: []string{"TavernKernel/TavernKernelObjectFilter.cpp", "TavernKernel/TavernKernelProcessMonitor.cpp"}},
			},
		},
		SemanticIndexV2: indexV2,
	}
}

func projectStructurePackHasDoc(pack ProjectStructureAnswerPack, name string) bool {
	for _, doc := range pack.RelevantDocs {
		if strings.EqualFold(doc.DocName, name) {
			return true
		}
	}
	return false
}

func projectStructurePackHasCriticalRole(pack ProjectStructureAnswerPack, role string) bool {
	for _, anchor := range pack.CriticalAnchors {
		if strings.EqualFold(anchor.Role, role) {
			return true
		}
	}
	return false
}

func assertCriticalAnchorSymbol(t *testing.T, pack ProjectStructureAnswerPack, role string, wantName string) {
	t.Helper()
	for _, anchor := range pack.CriticalAnchors {
		if !strings.EqualFold(anchor.Role, role) {
			continue
		}
		if anchor.Name != wantName {
			t.Fatalf("expected critical role %s to prefer %s, got %+v", role, wantName, anchor)
		}
		if strings.TrimSpace(anchor.File) == "" || anchor.Line <= 0 {
			t.Fatalf("expected critical role %s to keep concrete file/line, got %+v", role, anchor)
		}
		return
	}
	t.Fatalf("missing critical role %s in %+v", role, pack.CriticalAnchors)
}
