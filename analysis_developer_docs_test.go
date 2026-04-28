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
	apiDoc := buildAnalysisAPIEntrypointsDoc(run)
	reference := buildAnalysisCodeStructureReferenceDoc(run)
	if !strings.Contains(overview, "Solution startup candidate") || !strings.Contains(overview, "Kernel/runtime driver entry files") {
		t.Fatalf("expected separated startup and driver entry lens\n%s", overview)
	}
	if strings.Contains(overview, "Kernel/runtime driver entry files: `TavernKernelTestConsole/TavernKernelTestConsole.cpp`") ||
		strings.Contains(overview, "Kernel/runtime driver entry files: `TavernKernelTestConsole/TavernKernelTestConsole.cpp`,") {
		t.Fatalf("expected user-mode startup file not to appear as a kernel/runtime driver entry\n%s", overview)
	}
	if strings.Contains(overview, "sole entrypoint") || strings.Contains(overview, "sole entry point") {
		t.Fatalf("expected docs to avoid sole-entrypoint wording\n%s", overview)
	}
	if !strings.Contains(apiDoc, "Kernel/runtime driver entry files") || !strings.Contains(apiDoc, "IOCTL And Device-Control Contract") {
		t.Fatalf("expected API doc to include driver startup lens and IOCTL contract\n%s", apiDoc)
	}
	if !strings.Contains(reference, "IOCTL And Device-Control Contract") || !strings.Contains(reference, "DeviceIoControlIrpHandleRoutine") {
		t.Fatalf("expected IOCTL contract table\n%s", reference)
	}
}

func TestDeveloperFolderResponsibilityPrefersDriverAndHarnessRoles(t *testing.T) {
	run := ProjectAnalysisRun{
		Snapshot: ProjectSnapshot{
			Files: []ScannedFile{
				{Path: "TavernKernel.sln", Directory: ".", IsManifest: true},
				{Path: "TavernKernel.vmp", Directory: ".", IsManifest: true},
				{Path: "Common/UserCommon.h", Directory: "Common", ImportanceScore: 80},
				{Path: "Common/KernelCommon.h", Directory: "Common", ImportanceScore: 80},
				{Path: "Common/pehelper.h", Directory: "Common", ImportanceScore: 60},
				{Path: "TavernKernel/TavernKernel.cpp", Directory: "TavernKernel", IsEntrypoint: true},
				{Path: "TavernKernel/TavernKernelCore.cpp", Directory: "TavernKernel", ImportanceScore: 90},
				{Path: "TavernKernelTestConsole/TavernKernelManager.cpp", Directory: "TavernKernelTestConsole", IsEntrypoint: true},
				{Path: "TavernKernelTestConsole/TavernKernelTestConsole.vcxproj", Directory: "TavernKernelTestConsole", IsManifest: true},
			},
			BuildContexts: []BuildContextRecord{
				{ID: "buildctx:project:TavernKernel", Name: "TavernKernel", Kind: "wdm_driver", Directory: "TavernKernel", Project: "TavernKernel", Target: "tvk.sys", Files: []string{"TavernKernel/TavernKernel.cpp", "TavernKernel/TavernKernelCore.cpp"}},
				{ID: "buildctx:project:TavernKernelTestConsole", Name: "TavernKernelTestConsole", Kind: "application", Directory: "TavernKernelTestConsole", Project: "TavernKernelTestConsole", Target: "TavernKernelTestConsole.exe", Files: []string{"TavernKernelTestConsole/TavernKernelTestConsole.vcxproj", "TavernKernelTestConsole/TavernKernelManager.cpp"}},
			},
		},
		KnowledgePack: KnowledgePack{
			ProjectSummary: "Primary architecture group: Shared Infrastructure | Lead subsystem: Common PE Helper Header",
			Subsystems: []KnowledgeSubsystem{
				{Title: "Kernel Driver", Responsibilities: []string{"Provide a templated string class (KnString) supporting WCHAR assignments."}, KeyFiles: []string{"TavernKernel/TavernKernel.cpp"}},
				{Title: "Shared Infrastructure Common", Responsibilities: []string{"Provide common helper headers."}, KeyFiles: []string{"TavernKernel/TavernKernelCore.cpp"}},
				{Title: "Build And Release: TavernKernelTestConsole", Responsibilities: []string{"Build and release the console application."}, KeyFiles: []string{"TavernKernelTestConsole/TavernKernelTestConsole.vcxproj", "TavernKernelTestConsole/TavernKernelManager.cpp"}},
				{Title: "Worker Root Noise", Responsibilities: []string{"Driver initialization sets up driver object, device object, registry info, and high-level state."}, KeyFiles: []string{"TavernKernelCore.cpp", "TavernKernelProcessMonitor.cpp"}},
				{Title: "TestConsole Shared Contract Noise", Responsibilities: []string{"The test console depends on Common/UserCommon.h for service lifecycle enums."}, KeyFiles: []string{"Common/UserCommon.h"}},
			},
		},
		SemanticIndexV2: SemanticIndexV2{
			Symbols: []SymbolRecord{
				{Name: "DriverEntry", Kind: "function", File: "TavernKernel/TavernKernel.cpp", Tags: []string{"driver"}},
				{Name: "DeviceIoControlIrpHandleRoutine", CanonicalName: "TavernKernelCore::DeviceIoControlIrpHandleRoutine", Kind: "ioctl_handler", File: "TavernKernel/TavernKernelCore.cpp", Tags: []string{"ioctl_surface"}},
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
	if !strings.Contains(strings.ToLower(byPath["Common"].Responsibility), "shared") {
		t.Fatalf("expected Common to stay shared despite test-console wording, got %+v", byPath["Common"])
	}
	if strings.Contains(strings.ToLower(byPath["TavernKernelTestConsole"].Responsibility), "packaging") {
		t.Fatalf("expected harness responsibility to beat build/release wording, got %+v", byPath["TavernKernelTestConsole"])
	}
	if !strings.Contains(strings.ToLower(byPath["."].Responsibility), "solution root") {
		t.Fatalf("expected solution root responsibility for root manifests, got %+v", byPath["."])
	}
	if strings.Contains(strings.ToLower(byPath["."].Responsibility), "driver initialization") {
		t.Fatalf("expected solution root inference to beat bare-file worker noise, got %+v", byPath["."])
	}
	overview := buildAnalysisDeveloperOverviewDoc(run)
	if strings.Contains(overview, "Lead subsystem: Common PE Helper") {
		t.Fatalf("expected driver-oriented project shape to override stale/common lead summary\n%s", overview)
	}
	if !strings.Contains(overview, "Windows kernel/WDM `.sys` driver solution") || !strings.Contains(overview, "Kernel driver root: `TavernKernel/`") {
		t.Fatalf("expected driver-oriented overview shape\n%s", overview)
	}
	if !strings.Contains(overview, "User-mode harness/control root: `TavernKernelTestConsole/`") {
		t.Fatalf("expected non-root user-mode harness in project shape\n%s", overview)
	}
	if !strings.Contains(overview, "Shared contract root: `Common/`") {
		t.Fatalf("expected shared contract root in project shape\n%s", overview)
	}
	if strings.Contains(overview, "User-mode harness/control root: `./`") {
		t.Fatalf("expected project shape not to choose solution root as harness\n%s", overview)
	}
}

func TestDeveloperIOCTLRolesSeparateUserModeWrappers(t *testing.T) {
	kernelDispatch := developerIOCTLRole(SymbolRecord{
		Name:          "DeviceIoControlIrpHandleRoutine",
		CanonicalName: "DriverCore::DeviceIoControlIrpHandleRoutine",
		Kind:          "ioctl_handler",
		File:          "Driver/DriverCore.cpp",
		Tags:          []string{"ioctl_surface"},
	})
	if kernelDispatch != "kernel dispatch or handler" {
		t.Fatalf("expected kernel dispatch role, got %q", kernelDispatch)
	}

	userWrapper := developerIOCTLRole(SymbolRecord{
		Name:          "ControlOperation",
		CanonicalName: "DriverManager::ControlOperation",
		Kind:          "ioctl_handler",
		File:          "DriverTestConsole/DriverManager.cpp",
		Tags:          []string{"ioctl_surface"},
	})
	if userWrapper != "user-mode request issuer" {
		t.Fatalf("expected user-mode wrapper role, got %q", userWrapper)
	}

	validation := developerIOCTLRole(SymbolRecord{
		Name:          "DecryptIoctlData",
		CanonicalName: "DriverCore::DecryptIoctlData",
		Kind:          "function",
		File:          "Driver/DriverCore.cpp",
		Tags:          []string{"ioctl_surface"},
	})
	if validation != "validation or buffer gate" {
		t.Fatalf("expected validation role, got %q", validation)
	}
}

func TestDeveloperFolderRecordsNormalizeAnnotatedFileReferences(t *testing.T) {
	run := ProjectAnalysisRun{
		Snapshot: ProjectSnapshot{
			Files: []ScannedFile{
				{Path: "Driver/Dispatch.cpp", Directory: "Driver", ImportanceScore: 90},
				{Path: "Common/UserCommon.h", Directory: "Common", ImportanceScore: 80},
				{Path: "BuildCab/driver.inf", Directory: "BuildCab", IsManifest: true},
				{Path: "Batch/build_driver.bat", Directory: "Batch"},
			},
			Directories: []string{"Driver", "Common", "BuildCab", "Batch", "Signed", "Signed/QA"},
			BuildContexts: []BuildContextRecord{
				{ID: "buildctx:driver", Name: "Driver", Kind: "wdm_driver", Directory: "Driver", Target: "driver.sys", Files: []string{"Driver/Dispatch.cpp"}},
			},
		},
		KnowledgePack: KnowledgePack{
			Subsystems: []KnowledgeSubsystem{
				{
					Title:            "Driver Core",
					Responsibilities: []string{"Own driver dispatch and cleanup."},
					KeyFiles: []string{
						"DriverCore.h / DriverCore.cpp",
						"ObjectFilter.h (object filter registration and process/thread callbacks)",
						"kernel/user-mode contracts",
					},
					EvidenceFiles: []string{"Driver/Dispatch.cpp:120"},
				},
			},
			HighRiskFiles: []string{"Driver/Dispatch.cpp:120"},
		},
		SemanticIndexV2: SemanticIndexV2{
			Symbols: []SymbolRecord{
				{Name: "DriverEntry", Kind: "function", File: "Driver/Dispatch.cpp", Tags: []string{"driver"}},
			},
		},
	}

	folders := buildDeveloperFolderRecords(run)
	byPath := map[string]DeveloperFolderRecord{}
	for _, folder := range folders {
		byPath[folder.Path] = folder
		if strings.ContainsAny(folder.Path, "()") || strings.Contains(folder.Path, ".h") || strings.Contains(folder.Path, ".cpp") || strings.EqualFold(folder.Path, "process") || strings.EqualFold(folder.Path, "process/thread") || strings.EqualFold(folder.Path, "kernel") || strings.EqualFold(folder.Path, "kernel/user-mode") {
			t.Fatalf("expected annotated/source file references to stay out of folder paths, got %+v", folders)
		}
	}
	for _, path := range []string{"Driver", "Common", "BuildCab", "Batch", "Signed", "Signed/QA"} {
		if _, ok := byPath[path]; !ok {
			t.Fatalf("expected folder %q, got %+v", path, folders)
		}
	}
	if _, ok := byPath["."]; ok {
		t.Fatalf("did not expect unresolved bare file references to create a root folder record, got %+v", byPath["."])
	}
	if !strings.Contains(strings.ToLower(byPath["Driver"].Responsibility), "driver") {
		t.Fatalf("expected Driver to be classified as driver runtime, got %+v", byPath["Driver"])
	}
	if !strings.Contains(strings.ToLower(byPath["Common"].Responsibility), "shared") {
		t.Fatalf("expected Common to be classified as shared contracts, got %+v", byPath["Common"])
	}
	for _, path := range []string{"BuildCab", "Batch"} {
		if !strings.Contains(strings.ToLower(byPath[path].Responsibility), "build") {
			t.Fatalf("expected %s to be classified as build tooling, got %+v", path, byPath[path])
		}
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
