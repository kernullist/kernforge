package main

import (
	"strings"
	"testing"
)

func TestBuildDeveloperFolderRecordsMapsFilesTestsSymbolsAndRisk(t *testing.T) {
	run := ProjectAnalysisRun{
		Snapshot: ProjectSnapshot{
			Files: []ScannedFile{
				{Path: "analysis_project.go", Directory: ".", ImportanceScore: 90},
				{Path: "analysis_project_test.go", Directory: "."},
				{Path: "cmd/main.go", Directory: "cmd", IsEntrypoint: true},
			},
			BuildContexts: []BuildContextRecord{
				{ID: "buildctx:root", Name: "root", Kind: "go", Directory: ".", Files: []string{"analysis_project.go"}},
			},
		},
		KnowledgePack: KnowledgePack{
			Subsystems: []KnowledgeSubsystem{
				{
					Title:                "Project Analysis",
					Responsibilities:     []string{"Analyze projects"},
					KeyFiles:             []string{"analysis_project.go"},
					InvalidationReasons:  []string{"analysis code changed"},
					InvalidationEvidence: []string{"analysis_project.go"},
					InvalidationChanges:  []InvalidationChange{},
					InvalidationDiff:     []string{},
				},
			},
			HighRiskFiles: []string{"analysis_project.go"},
		},
		SemanticIndexV2: SemanticIndexV2{
			Symbols: []SymbolRecord{
				{Name: "DispatchIoctl", Kind: "function", File: "analysis_project.go", Tags: []string{"ioctl"}},
			},
		},
	}

	records := buildDeveloperFolderRecords(run)
	if len(records) == 0 {
		t.Fatalf("expected folder records")
	}
	root := DeveloperFolderRecord{}
	for _, record := range records {
		if record.Path == "." {
			root = record
			break
		}
	}
	if root.Path == "" {
		t.Fatalf("expected root folder record, got %+v", records)
	}
	if !sliceContainsFold(root.TestFiles, "analysis_project_test.go") {
		t.Fatalf("expected test file mapping, got %+v", root)
	}
	if len(root.MainSymbols) == 0 || root.MainSymbols[0].Name != "DispatchIoctl" {
		t.Fatalf("expected symbol mapping, got %+v", root.MainSymbols)
	}
	if len(root.BuildContexts) == 0 {
		t.Fatalf("expected build context mapping, got %+v", root)
	}
	if len(root.RiskSignals) == 0 {
		t.Fatalf("expected risk signals, got %+v", root)
	}
}

func TestDeveloperDocsRenderFolderAndModuleContent(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{RunID: "run-dev-docs", Goal: "map developer docs", Mode: "map", Status: "completed"},
		Snapshot: ProjectSnapshot{
			Root:       "C:/repo",
			ModulePath: "kernforge",
			Files: []ScannedFile{
				{Path: "analysis_project.go", Directory: ".", ImportanceScore: 90},
				{Path: "analysis_project_test.go", Directory: "."},
			},
			EntrypointFiles: []string{"main.go"},
		},
		KnowledgePack: KnowledgePack{
			ProjectSummary: "Kernforge analyzes projects.",
			TopImportantFiles: []string{
				"analysis_project.go",
			},
		},
	}

	overview := buildAnalysisDeveloperOverviewDoc(run)
	folderMap := buildAnalysisFolderMapDoc(run)
	modules := buildAnalysisModulesDoc(run)
	for name, body := range map[string]string{
		"overview": overview,
		"folders":  folderMap,
		"modules":  modules,
	} {
		if strings.TrimSpace(body) == "" {
			t.Fatalf("expected %s doc body", name)
		}
	}
	if !strings.Contains(overview, "Reading Order") {
		t.Fatalf("expected reading order\n%s", overview)
	}
	if !strings.Contains(folderMap, "analysis_project.go") {
		t.Fatalf("expected folder source anchor\n%s", folderMap)
	}
	if !strings.Contains(modules, "kernforge") {
		t.Fatalf("expected package module\n%s", modules)
	}
}

func TestStructureDiagramsAndCodeReferenceRenderGraphAndSymbols(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{RunID: "run-structure-docs", Goal: "map structure docs", Mode: "map", Status: "completed"},
		Snapshot: ProjectSnapshot{
			Root: "C:/repo",
			RuntimeEdges: []RuntimeEdge{
				{Source: "main.go", Target: "analysis_project.go", Kind: "calls", Confidence: "high", Evidence: []string{"main.go"}},
			},
			BuildContexts: []BuildContextRecord{
				{ID: "buildctx:core", Name: "core", Kind: "go", Directory: ".", Files: []string{"analysis_project.go"}},
			},
		},
		KnowledgePack: KnowledgePack{
			TopImportantFiles: []string{"analysis_project.go"},
		},
		SemanticIndexV2: SemanticIndexV2{
			Symbols: []SymbolRecord{
				{ID: "func:AnalyzeProject", Name: "AnalyzeProject", Kind: "function", File: "analysis_project.go", StartLine: 10, BuildContextID: "buildctx:core", Tags: []string{"analysis"}},
			},
			CallEdges: []CallEdge{
				{SourceID: "func:AnalyzeProject", TargetID: "func:BuildDocs", Type: "calls", Evidence: []string{"analysis_project.go:10"}},
			},
			BuildOwnershipEdges: []BuildOwnershipEdge{
				{SourceID: "buildctx:core", TargetID: "analysis_project.go", Type: "owns", Evidence: []string{"analysis_project.go"}},
			},
			GeneratedCodeEdges: []GeneratedCodeEdge{
				{SourceFile: "schema.idl", TargetID: "generated/schema.go", Type: "generates", Evidence: []string{"schema.idl"}},
			},
		},
	}

	diagrams := buildAnalysisStructureDiagramsDoc(run)
	reference := buildAnalysisCodeStructureReferenceDoc(run)
	if !strings.Contains(diagrams, "```mermaid") {
		t.Fatalf("expected Mermaid diagram\n%s", diagrams)
	}
	if !strings.Contains(diagrams, "Build And Artifact Flow") {
		t.Fatalf("expected build artifact section\n%s", diagrams)
	}
	if !strings.Contains(reference, "AnalyzeProject") {
		t.Fatalf("expected important symbol\n%s", reference)
	}
	if !strings.Contains(reference, "Generated Or Derived Artifacts") || !strings.Contains(reference, "schema.idl") {
		t.Fatalf("expected generated artifact reference\n%s", reference)
	}
}

func TestDeveloperDocsSeparateStartupHarnessDriverEntryAndIOCTLContract(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{RunID: "run-driver-docs", Goal: "TavernKernel 구조를 분석해", Mode: "map", Status: "completed"},
		Snapshot: ProjectSnapshot{
			Root:           "C:/repo/TavernKernel",
			PrimaryStartup: "TavernKernelTestConsole",
			SolutionProjects: []SolutionProject{
				{Name: "TavernKernel", Path: "TavernKernel/TavernKernel.vcxproj", Directory: "TavernKernel", OutputType: "driver", EntryFiles: []string{"TavernKernel/TavernKernel.cpp"}},
				{Name: "TavernKernelTestConsole", Path: "TavernKernelTestConsole/TavernKernelTestConsole.vcxproj", Directory: "TavernKernelTestConsole", OutputType: "application", EntryFiles: []string{"TavernKernelTestConsole/TavernKernelTestConsole.cpp"}, StartupCandidate: true},
			},
			EntrypointFiles: []string{"TavernKernel/TavernKernel.cpp", "TavernKernelTestConsole/TavernKernelTestConsole.cpp"},
		},
		SemanticIndexV2: SemanticIndexV2{
			Symbols: []SymbolRecord{
				{ID: "func:DriverEntry", Name: "DriverEntry", Kind: "function", File: "TavernKernel/TavernKernel.cpp", StartLine: 10, Tags: []string{"driver"}},
				{ID: "ioctl:DeviceIoControlIrpHandleRoutine", Name: "DeviceIoControlIrpHandleRoutine", Kind: "function", File: "TavernKernel/TavernKernelCore.cpp", StartLine: 120, Tags: []string{"ioctl", "dispatch"}},
				{ID: "ioctl:TavernKernelManager::ControlOperation", Name: "TavernKernelManager::ControlOperation", Kind: "method", File: "TavernKernelTestConsole/TavernKernelManager.cpp", StartLine: 88, Tags: []string{"DeviceIoControl"}},
			},
		},
	}

	overview := buildAnalysisDeveloperOverviewDoc(run)
	reference := buildAnalysisCodeStructureReferenceDoc(run)
	if !strings.Contains(overview, "Solution startup candidate") || !strings.Contains(overview, "Kernel/runtime driver entry files") {
		t.Fatalf("expected separated startup and driver entry lens\n%s", overview)
	}
	if strings.Contains(overview, "sole entrypoint") || strings.Contains(overview, "sole entry point") {
		t.Fatalf("expected docs to avoid sole-entrypoint wording\n%s", overview)
	}
	if !strings.Contains(reference, "IOCTL And Device-Control Contract") || !strings.Contains(reference, "DeviceIoControlIrpHandleRoutine") {
		t.Fatalf("expected IOCTL contract table\n%s", reference)
	}
}

func TestDeveloperFolderResponsibilityPrefersDriverAndHarnessRoles(t *testing.T) {
	run := ProjectAnalysisRun{
		Snapshot: ProjectSnapshot{
			Files: []ScannedFile{
				{Path: "TavernKernel/TavernKernel.cpp", Directory: "TavernKernel", IsEntrypoint: true},
				{Path: "TavernKernelTestConsole/TavernKernelManager.cpp", Directory: "TavernKernelTestConsole", IsEntrypoint: true},
			},
		},
		KnowledgePack: KnowledgePack{
			Subsystems: []KnowledgeSubsystem{
				{Title: "Kernel Driver", Responsibilities: []string{"Provide a templated string class (KnString) supporting WCHAR assignments."}, KeyFiles: []string{"TavernKernel/TavernKernel.cpp"}},
			},
		},
		SemanticIndexV2: SemanticIndexV2{
			Symbols: []SymbolRecord{
				{Name: "DriverEntry", Kind: "function", File: "TavernKernel/TavernKernel.cpp", Tags: []string{"driver"}},
				{Name: "CreateDriverService", Kind: "method", File: "TavernKernelTestConsole/TavernKernelManager.cpp", Tags: []string{"service"}},
			},
		},
	}

	folders := buildDeveloperFolderRecords(run)
	byPath := map[string]DeveloperFolderRecord{}
	for _, folder := range folders {
		byPath[folder.Path] = folder
	}
	if !strings.Contains(strings.ToLower(byPath["TavernKernel"].Responsibility), "driver") {
		t.Fatalf("expected driver responsibility, got %+v", byPath["TavernKernel"])
	}
	if !strings.Contains(strings.ToLower(byPath["TavernKernelTestConsole"].Responsibility), "bootstrap") {
		t.Fatalf("expected harness responsibility, got %+v", byPath["TavernKernelTestConsole"])
	}
}

func TestDeveloperDiagramsDropSelfLoopEdges(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{RunID: "run-self-loop", Goal: "map", Mode: "map", Status: "completed"},
		Snapshot: ProjectSnapshot{
			SolutionProjects: []SolutionProject{
				{Name: "Core", Path: "Core/Core.vcxproj", Directory: "Core"},
			},
		},
	}

	diagrams := buildAnalysisStructureDiagramsDoc(run)
	if strings.Contains(diagrams, "Core\"]\n  n01 -->|contains| n01") {
		t.Fatalf("expected self-loop edge to be removed\n%s", diagrams)
	}
}

func TestDeveloperDocsHandleEmptySnapshotFallbacks(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{RunID: "run-empty-docs", Goal: "map empty docs", Mode: "map", Status: "completed"},
	}

	docs := map[string]string{
		"overview":  buildAnalysisDeveloperOverviewDoc(run),
		"folders":   buildAnalysisFolderMapDoc(run),
		"modules":   buildAnalysisModulesDoc(run),
		"diagrams":  buildAnalysisStructureDiagramsDoc(run),
		"reference": buildAnalysisCodeStructureReferenceDoc(run),
	}
	for name, body := range docs {
		if strings.TrimSpace(body) == "" {
			t.Fatalf("expected non-empty %s fallback doc", name)
		}
	}
	for _, want := range []string{
		"No folder records were available",
		"No module records were available",
		"No module dependency graph edges were inferred",
		"No important files were recorded",
	} {
		found := false
		for _, body := range docs {
			if strings.Contains(body, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected fallback text %q in docs: %+v", want, docs)
		}
	}
}

func TestDeveloperDocsNormalizeWindowsPaths(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{RunID: "run-windows-paths", Goal: "map windows paths", Mode: "map", Status: "completed"},
		Snapshot: ProjectSnapshot{
			Root:       `C:\repo`,
			ModulePath: "kernforge",
			Files: []ScannedFile{
				{Path: `driver\dispatch.cpp`, Directory: `driver`, ImportanceScore: 80},
				{Path: `driver\dispatch_test.cpp`, Directory: `driver`},
			},
			BuildContexts: []BuildContextRecord{
				{ID: "buildctx:driver", Name: "driver", Kind: "compile", Directory: `driver`, Files: []string{`driver\dispatch.cpp`}},
			},
		},
		KnowledgePack: KnowledgePack{
			TopImportantFiles: []string{`driver\dispatch.cpp`},
			HighRiskFiles:     []string{`driver\dispatch.cpp`},
		},
		SemanticIndexV2: SemanticIndexV2{
			Symbols: []SymbolRecord{
				{ID: "func:DispatchIoctl", Name: "DispatchIoctl", Kind: "function", File: `driver\dispatch.cpp`, StartLine: 42, Tags: []string{"ioctl"}},
			},
			GeneratedCodeEdges: []GeneratedCodeEdge{
				{SourceFile: `schema\guard.idl`, TargetID: `generated\guard.go`, Type: "generates", Evidence: []string{`schema\guard.idl`}},
			},
		},
	}

	folders := buildDeveloperFolderRecords(run)
	if len(folders) == 0 {
		t.Fatalf("expected folder records")
	}
	if folders[0].Path != "driver" {
		t.Fatalf("expected normalized folder path, got %+v", folders[0])
	}
	folderMap := buildAnalysisFolderMapDoc(run)
	reference := buildAnalysisCodeStructureReferenceDoc(run)
	for _, body := range []string{folderMap, reference} {
		if strings.Contains(body, `driver\dispatch.cpp`) || strings.Contains(body, `schema\guard.idl`) {
			t.Fatalf("expected slash-normalized paths\n%s", body)
		}
	}
	for _, want := range []string{"driver/dispatch.cpp", "schema/guard.idl"} {
		if !strings.Contains(reference, want) && !strings.Contains(folderMap, want) {
			t.Fatalf("expected normalized path %q\nfolder:\n%s\nreference:\n%s", want, folderMap, reference)
		}
	}
}
