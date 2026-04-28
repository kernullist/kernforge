package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type DeveloperFolderRecord struct {
	Path           string
	Responsibility string
	KeyFiles       []string
	TestFiles      []string
	MainSymbols    []SymbolRecord
	BuildContexts  []BuildContextRecord
	Subsystems     []string
	RiskSignals    []string
	SourceAnchors  []string
	Confidence     string
}

type DeveloperModuleRecord struct {
	ID             string
	Name           string
	Kind           string
	Root           string
	Responsibility string
	PublicFiles    []string
	InternalFiles  []string
	Entrypoints    []string
	Dependencies   []string
	BuildContexts  []string
	SourceAnchors  []string
	Confidence     string
}

type DeveloperStructureGraph struct {
	Nodes []DeveloperStructureNode
	Edges []DeveloperStructureEdge
}

type DeveloperStructureNode struct {
	ID     string
	Label  string
	Kind   string
	Source string
}

type DeveloperStructureEdge struct {
	Source     string
	Target     string
	Type       string
	Confidence string
	Evidence   []string
}

func buildAnalysisDeveloperOverviewDoc(run ProjectAnalysisRun) string {
	var b strings.Builder
	folders := buildDeveloperFolderRecords(run)
	modules := buildDeveloperModuleRecords(run)
	fmt.Fprintf(&b, "# Developer Overview\n\n")
	analysisDocsWriteHeader(&b, run)
	analysisDocsWriteDocMetadata(&b, run, "DEVELOPER_OVERVIEW.md")
	analysisDocsWriteStartupLens(&b, run)
	analysisDocsWriteArchitectureFactPack(&b, run)
	fmt.Fprintf(&b, "\n## Project Shape\n\n%s\n", developerProjectShapeSummary(run, folders))
	fmt.Fprintf(&b, "\n## Architecture Layers\n\n")
	if len(modules) > 0 {
		for _, module := range limitDeveloperModuleRecords(modules, 12) {
			fmt.Fprintf(&b, "- `%s` (%s): root `%s`, responsibility: %s\n",
				module.Name,
				firstNonBlankAnalysisString(module.Kind, "module"),
				firstNonBlankAnalysisString(module.Root, "."),
				firstNonBlankAnalysisString(module.Responsibility, "source module"))
		}
	} else if len(run.KnowledgePack.ArchitectureGroups) > 0 {
		for _, group := range limitStrings(run.KnowledgePack.ArchitectureGroups, 12) {
			fmt.Fprintf(&b, "- %s\n", group)
		}
	} else {
		fmt.Fprintf(&b, "No module or architecture layer records were inferred.\n")
	}
	if len(modules) > 0 {
		fmt.Fprintf(&b, "\n## Primary Execution Flow\n\n")
		analysisDocsWriteList(&b, "Entrypoint Files", run.Snapshot.EntrypointFiles, 12)
		if len(run.SemanticIndexV2.CallEdges) > 0 {
			fmt.Fprintf(&b, "\nRepresentative call edges:\n\n")
			for _, edge := range limitCallEdges(run.SemanticIndexV2.CallEdges, 12) {
				fmt.Fprintf(&b, "- `%s` -> `%s` (%s)\n", edge.SourceID, edge.TargetID, firstNonBlankAnalysisString(edge.Type, "calls"))
			}
		}
	}
	fmt.Fprintf(&b, "\n## Primary Runtime Narratives\n\n")
	if views := developerRuntimeGraphViews(run); len(views) > 0 {
		for _, edge := range limitAnalysisGraphEdgeViews(views, 12) {
			fmt.Fprintf(&b, "- `%s` -> `%s` via `%s`; evidence: %s\n",
				edge.Source,
				edge.Target,
				firstNonBlankAnalysisString(edge.Flow, edge.Type),
				firstNonBlankAnalysisString(edge.Evidence, "runtime edge"))
		}
	} else {
		fmt.Fprintf(&b, "No runtime edge narrative was available from the latest snapshot.\n")
	}
	fmt.Fprintf(&b, "\n## Most Important Cross-Cutting Paths\n\n")
	crossCutting := developerCrossCuttingPaths(run)
	if len(crossCutting) > 0 {
		for _, item := range limitStrings(crossCutting, 16) {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	} else {
		fmt.Fprintf(&b, "No cross-cutting path candidates were recorded.\n")
	}
	analysisDocsWriteDomainCriticalAnchors(&b, run)
	analysisDocsWriteIOCTLContract(&b, run)
	if len(folders) > 0 {
		fmt.Fprintf(&b, "\n## Main Development Areas\n\n")
		for _, folder := range limitDeveloperFolderRecords(folders, 12) {
			fmt.Fprintf(&b, "- `%s`: %s", folder.Path, firstNonBlankAnalysisString(folder.Responsibility, "source area"))
			if len(folder.KeyFiles) > 0 {
				fmt.Fprintf(&b, " (key: `%s`)", strings.Join(limitStrings(folder.KeyFiles, 3), "`, `"))
			}
			fmt.Fprintf(&b, "\n")
		}
	}
	fmt.Fprintf(&b, "\n## Where To Start By Task\n\n")
	fmt.Fprintf(&b, "| Task | Start With | Then Check |\n")
	fmt.Fprintf(&b, "| --- | --- | --- |\n")
	fmt.Fprintf(&b, "| Change runtime behavior | `ARCHITECTURE.md` | `MODULES.md`, `API_AND_ENTRYPOINTS.md` |\n")
	fmt.Fprintf(&b, "| Change folder-local code | `FOLDER_MAP.md` | related tests and `VERIFICATION_MATRIX.md` |\n")
	fmt.Fprintf(&b, "| Change build or packaging | `BUILD_AND_ARTIFACTS.md` | module cards in `MODULES.md` |\n")
	fmt.Fprintf(&b, "| Change security-sensitive paths | `SECURITY_SURFACE.md` | `VERIFICATION_MATRIX.md`, `FUZZ_TARGETS.md` |\n")
	fmt.Fprintf(&b, "\n## Reading Order\n\n")
	fmt.Fprintf(&b, "1. `DEVELOPER_OVERVIEW.md`\n")
	fmt.Fprintf(&b, "2. `FOLDER_MAP.md`\n")
	fmt.Fprintf(&b, "3. `MODULES.md`\n")
	fmt.Fprintf(&b, "4. `ARCHITECTURE.md`\n")
	fmt.Fprintf(&b, "5. `VERIFICATION_MATRIX.md`\n")
	return b.String()
}

func buildAnalysisStructureDiagramsDoc(run ProjectAnalysisRun) string {
	var b strings.Builder
	modules := buildDeveloperModuleRecords(run)
	structureGraph := buildDeveloperStructureGraph(run, modules)
	fmt.Fprintf(&b, "# Structure Diagrams\n\n")
	analysisDocsWriteHeader(&b, run)
	analysisDocsWriteDocMetadata(&b, run, "STRUCTURE_DIAGRAMS.md")
	fmt.Fprintf(&b, "\n## Module Dependency Graph\n\n")
	moduleViews := developerModuleGraphViews(structureGraph)
	if len(moduleViews) > 0 {
		analysisDocsWriteSectionMetadata(&b, run, "STRUCTURE_DIAGRAMS.md", "diagrams.module_dependency_graph", developerStructureGraphAnchors(structureGraph))
		fmt.Fprintf(&b, "%s\n", analysisGraphMermaid(moduleViews, 20))
	} else {
		fmt.Fprintf(&b, "No module dependency graph edges were inferred.\n")
	}
	fmt.Fprintf(&b, "\n## Folder And Module Map\n\n")
	folderViews := developerFolderModuleViews(run, modules)
	if len(folderViews) > 0 {
		analysisDocsWriteSectionMetadata(&b, run, "STRUCTURE_DIAGRAMS.md", "diagrams.folder_module_map", analysisDeveloperDocSourceAnchors(run, "FOLDER_MAP.md"))
		fmt.Fprintf(&b, "%s\n", analysisGraphMermaid(folderViews, 20))
	} else {
		fmt.Fprintf(&b, "No folder-to-module graph edges were inferred.\n")
	}
	fmt.Fprintf(&b, "\n## Primary Runtime Flow\n\n")
	runtimeViews := developerRuntimeGraphViews(run)
	if len(runtimeViews) > 0 {
		analysisDocsWriteSectionMetadata(&b, run, "STRUCTURE_DIAGRAMS.md", "diagrams.primary_runtime_flow", runtimeEdgeAnchors(run.Snapshot.RuntimeEdges))
		fmt.Fprintf(&b, "%s\n", analysisGraphMermaid(runtimeViews, 18))
	} else if graph := analysisGraphDataFlowViews(run); len(graph) > 0 {
		analysisDocsWriteSectionMetadata(&b, run, "STRUCTURE_DIAGRAMS.md", "diagrams.primary_runtime_flow", analysisGraphSourceAnchors(run))
		fmt.Fprintf(&b, "%s\n", analysisGraphMermaid(graph, 18))
	} else {
		fmt.Fprintf(&b, "No primary runtime flow graph edges were inferred.\n")
	}
	fmt.Fprintf(&b, "\n## Startup To Runtime Flow\n\n")
	if len(runtimeViews) > 0 {
		analysisDocsWriteSectionMetadata(&b, run, "STRUCTURE_DIAGRAMS.md", "diagrams.startup_runtime_flow", runtimeEdgeAnchors(run.Snapshot.RuntimeEdges))
		fmt.Fprintf(&b, "%s\n", analysisGraphMarkdownTable(runtimeViews, 18))
	} else {
		fmt.Fprintf(&b, "No startup-to-runtime flow edges were inferred.\n")
	}
	fmt.Fprintf(&b, "\n## Build And Artifact Flow\n\n")
	buildViews := developerBuildArtifactViews(run)
	if len(buildViews) > 0 {
		analysisDocsWriteSectionMetadata(&b, run, "STRUCTURE_DIAGRAMS.md", "diagrams.build_artifact_flow", developerBuildArtifactAnchors(run))
		fmt.Fprintf(&b, "%s\n", analysisGraphMermaid(buildViews, 20))
	} else {
		fmt.Fprintf(&b, "No build or generated artifact graph edges were inferred.\n")
	}
	fmt.Fprintf(&b, "\n## Build Ownership Flow\n\n")
	if len(buildViews) > 0 {
		analysisDocsWriteSectionMetadata(&b, run, "STRUCTURE_DIAGRAMS.md", "diagrams.build_ownership_flow", developerBuildArtifactAnchors(run))
		fmt.Fprintf(&b, "%s\n", analysisGraphMarkdownTable(buildViews, 20))
	} else {
		fmt.Fprintf(&b, "No build ownership flow edges were inferred.\n")
	}
	fmt.Fprintf(&b, "\n## Trust Boundary Summary\n\n")
	trustGraph := analysisGraphTrustBoundaryViews(run)
	if len(trustGraph) > 0 {
		analysisDocsWriteSectionMetadata(&b, run, "STRUCTURE_DIAGRAMS.md", "diagrams.trust_boundary_summary", analysisGraphSourceAnchors(run))
		fmt.Fprintf(&b, "%s\n", analysisGraphMermaid(trustGraph, 16))
	} else {
		fmt.Fprintf(&b, "No trust boundary graph edges were inferred.\n")
	}
	fmt.Fprintf(&b, "\n## Security Boundary Flow\n\n")
	if len(trustGraph) > 0 {
		analysisDocsWriteSectionMetadata(&b, run, "STRUCTURE_DIAGRAMS.md", "diagrams.security_boundary_flow", analysisGraphSourceAnchors(run))
		fmt.Fprintf(&b, "%s\n", analysisGraphMarkdownTable(trustGraph, 18))
	} else {
		fmt.Fprintf(&b, "No security boundary flow edges were inferred.\n")
	}
	fmt.Fprintf(&b, "\n## Unreal Reflection And Replication Flow\n\n")
	unrealViews := developerUnrealGraphViews(run)
	if len(unrealViews) > 0 {
		analysisDocsWriteSectionMetadata(&b, run, "STRUCTURE_DIAGRAMS.md", "diagrams.unreal_reflection_replication", analysisDeveloperDocSourceAnchors(run, "STRUCTURE_DIAGRAMS.md"))
		fmt.Fprintf(&b, "%s\n", analysisGraphMarkdownTable(unrealViews, 20))
	} else {
		fmt.Fprintf(&b, "No Unreal reflection or replication graph edges were inferred.\n")
	}
	return b.String()
}

func buildAnalysisCodeStructureReferenceDoc(run ProjectAnalysisRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Structure Reference\n\n")
	analysisDocsWriteHeader(&b, run)
	analysisDocsWriteDocMetadata(&b, run, "CODE_STRUCTURE_REFERENCE.md")
	fmt.Fprintf(&b, "\n## Important Files\n\n")
	important := analysisUniqueStrings(append(append([]string{}, run.KnowledgePack.TopImportantFiles...), run.KnowledgePack.HighRiskFiles...))
	if len(important) == 0 {
		for _, file := range run.Snapshot.Files {
			if file.IsEntrypoint || file.IsManifest || file.ImportanceScore > 0 {
				important = append(important, file.Path)
			}
		}
		important = analysisUniqueStrings(important)
	}
	if len(important) > 0 {
		for _, file := range limitStrings(important, 80) {
			fmt.Fprintf(&b, "- `%s`\n", analysisDocSlashPath(file))
		}
	} else {
		fmt.Fprintf(&b, "No important files were recorded.\n")
	}
	fmt.Fprintf(&b, "\n## Important Symbols\n\n")
	symbols := developerImportantSymbols(run)
	if len(symbols) > 0 {
		fmt.Fprintf(&b, "| Symbol | Kind | File | Build Context | Tags |\n")
		fmt.Fprintf(&b, "| --- | --- | --- | --- | --- |\n")
		for _, symbol := range limitSymbolRecords(symbols, 80) {
			location := analysisDocSlashPath(symbol.File)
			if symbol.StartLine > 0 {
				location = fmt.Sprintf("%s:%d", location, symbol.StartLine)
			}
			fmt.Fprintf(&b, "| `%s` | %s | `%s` | `%s` | %s |\n",
				analysisMarkdownCell(firstNonBlankDeveloperString(symbol.CanonicalName, symbol.Name, symbol.ID)),
				analysisMarkdownCell(symbol.Kind),
				analysisMarkdownCell(location),
				analysisMarkdownCell(symbol.BuildContextID),
				analysisMarkdownCell(strings.Join(limitStrings(symbol.Tags, 6), ", ")))
		}
	} else {
		fmt.Fprintf(&b, "No indexed symbols were recorded.\n")
	}
	analysisDocsWriteDomainCriticalAnchors(&b, run)
	analysisDocsWriteArchitectureFactPack(&b, run)
	fmt.Fprintf(&b, "\n## Symbol Clusters\n\n")
	symbolClusters := developerSymbolClusters(run)
	if len(symbolClusters) > 0 {
		keys := mapKeysSorted(symbolClusters)
		for _, key := range limitStrings(keys, 16) {
			fmt.Fprintf(&b, "- `%s`: %s\n", key, formatSymbolNames(symbolClusters[key], 8))
		}
	} else {
		fmt.Fprintf(&b, "No symbol clusters were inferred.\n")
	}
	fmt.Fprintf(&b, "\n## Representative Call Paths\n\n")
	if len(run.SemanticIndexV2.CallEdges) > 0 {
		symbolNames := developerSymbolNameByID(run.SemanticIndexV2.Symbols)
		fmt.Fprintf(&b, "| Source | Target | Type | Evidence |\n")
		fmt.Fprintf(&b, "| --- | --- | --- | --- |\n")
		for _, edge := range limitCallEdges(run.SemanticIndexV2.CallEdges, 60) {
			fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s |\n",
				analysisMarkdownCell(firstNonBlankDeveloperString(symbolNames[edge.SourceID], edge.SourceID)),
				analysisMarkdownCell(firstNonBlankDeveloperString(symbolNames[edge.TargetID], edge.TargetID)),
				analysisMarkdownCell(firstNonBlankAnalysisString(edge.Type, "calls")),
				analysisMarkdownCell(strings.Join(limitStrings(edge.Evidence, 3), ", ")))
		}
	} else {
		fmt.Fprintf(&b, "No call edges were recorded.\n")
	}
	fmt.Fprintf(&b, "\n## Caller Callee Hotspots\n\n")
	hotspots := developerCallerCalleeHotspots(run)
	if len(hotspots) > 0 {
		for _, item := range limitStrings(hotspots, 20) {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	} else {
		fmt.Fprintf(&b, "No caller/callee hotspots were recorded.\n")
	}
	analysisDocsWriteIOCTLContract(&b, run)
	fmt.Fprintf(&b, "\n## Build Ownership\n\n")
	if len(run.SemanticIndexV2.BuildOwnershipEdges) > 0 {
		fmt.Fprintf(&b, "| Owner | Target | Type | Evidence |\n")
		fmt.Fprintf(&b, "| --- | --- | --- | --- |\n")
		for _, edge := range limitBuildOwnershipEdges(run.SemanticIndexV2.BuildOwnershipEdges, 60) {
			fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s |\n",
				analysisMarkdownCell(edge.SourceID),
				analysisMarkdownCell(edge.TargetID),
				analysisMarkdownCell(edge.Type),
				analysisMarkdownCell(strings.Join(limitStrings(edge.Evidence, 3), ", ")))
		}
	} else {
		fmt.Fprintf(&b, "No build ownership edges were recorded.\n")
	}
	fmt.Fprintf(&b, "\n## Build Context To Source Mapping\n\n")
	if len(run.Snapshot.BuildContexts) > 0 {
		for _, ctx := range limitBuildContexts(run.Snapshot.BuildContexts, 30) {
			fmt.Fprintf(&b, "- `%s` (%s)", firstNonBlankAnalysisString(ctx.Name, ctx.ID), firstNonBlankAnalysisString(ctx.Kind, "build_context"))
			if strings.TrimSpace(ctx.Module) != "" {
				fmt.Fprintf(&b, " module=`%s`", ctx.Module)
			}
			if strings.TrimSpace(ctx.Directory) != "" {
				fmt.Fprintf(&b, " dir=`%s`", analysisDocSlashPath(ctx.Directory))
			}
			if len(ctx.Files) > 0 {
				fmt.Fprintf(&b, " files=%s", formatInlineCodeList(analysisDocSlashPaths(ctx.Files), 6))
			}
			fmt.Fprintf(&b, "\n")
		}
	} else {
		fmt.Fprintf(&b, "No build context to source mapping was recorded.\n")
	}
	fmt.Fprintf(&b, "\n## Generated Or Derived Artifacts\n\n")
	if len(run.SemanticIndexV2.GeneratedCodeEdges) > 0 {
		fmt.Fprintf(&b, "| Source File | Target | Type | Evidence |\n")
		fmt.Fprintf(&b, "| --- | --- | --- | --- |\n")
		for _, edge := range limitGeneratedCodeEdges(run.SemanticIndexV2.GeneratedCodeEdges, 60) {
			fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s |\n",
				analysisMarkdownCell(analysisDocSlashPath(edge.SourceFile)),
				analysisMarkdownCell(analysisDocSlashPath(edge.TargetID)),
				analysisMarkdownCell(edge.Type),
				analysisMarkdownCell(strings.Join(limitStrings(analysisDocSlashPaths(edge.Evidence), 3), ", ")))
		}
	} else {
		fmt.Fprintf(&b, "No generated artifact edges were recorded.\n")
	}
	fmt.Fprintf(&b, "\n## Verification Anchor Map\n\n")
	verificationRows := analysisVerificationMatrixCatalog(run)
	if len(verificationRows) > 0 {
		for _, row := range limitVerificationEntries(verificationRows, 16) {
			fmt.Fprintf(&b, "- `%s`: %s; anchors: %s\n",
				row.ChangeArea,
				row.RequiredVerification,
				strings.Join(limitStrings(analysisDocSlashPaths(row.SourceAnchors), 5), ", "))
		}
	} else {
		fmt.Fprintf(&b, "No verification anchor entries were recorded.\n")
	}
	fmt.Fprintf(&b, "\n## Source Anchors\n\n")
	anchors := analysisDocSourceAnchors(run, "CODE_STRUCTURE_REFERENCE.md")
	if len(anchors) > 0 {
		for _, anchor := range limitStrings(anchors, 80) {
			fmt.Fprintf(&b, "- `%s`\n", analysisDocSlashPath(anchor))
		}
	} else {
		fmt.Fprintf(&b, "No source anchors were recorded.\n")
	}
	return b.String()
}

func buildAnalysisFolderMapDoc(run ProjectAnalysisRun) string {
	var b strings.Builder
	folders := buildDeveloperFolderRecords(run)
	fmt.Fprintf(&b, "# Folder Map\n\n")
	analysisDocsWriteHeader(&b, run)
	analysisDocsWriteDocMetadata(&b, run, "FOLDER_MAP.md")
	if len(folders) == 0 {
		fmt.Fprintf(&b, "\nNo folder records were available. Rerun `/analyze-project` after a successful project scan.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\n## Folder Summary\n\n")
	fmt.Fprintf(&b, "| Folder | Responsibility | Key Files | Tests | Build Context | Risk | Confidence |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, folder := range limitDeveloperFolderRecords(folders, 60) {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s | %s |\n",
			analysisMarkdownCell(folder.Path),
			analysisMarkdownCell(firstNonBlankAnalysisString(folder.Responsibility, "source area")),
			analysisMarkdownCell(formatInlineCodeList(folder.KeyFiles, 4)),
			analysisMarkdownCell(formatInlineCodeList(folder.TestFiles, 3)),
			analysisMarkdownCell(formatBuildContextNames(folder.BuildContexts, 3)),
			analysisMarkdownCell(strings.Join(limitStrings(folder.RiskSignals, 3), ", ")),
			analysisMarkdownCell(firstNonBlankAnalysisString(folder.Confidence, "medium")))
	}
	fmt.Fprintf(&b, "\n## Folder Responsibilities\n\n")
	for _, folder := range limitDeveloperFolderRecords(folders, 24) {
		fmt.Fprintf(&b, "### %s\n\n", folder.Path)
		fmt.Fprintf(&b, "- Responsibility: %s\n", firstNonBlankAnalysisString(folder.Responsibility, "source area"))
		if len(folder.Subsystems) > 0 {
			fmt.Fprintf(&b, "- Related subsystems: %s\n", strings.Join(limitStrings(folder.Subsystems, 5), ", "))
		}
		if len(folder.SourceAnchors) > 0 {
			fmt.Fprintf(&b, "- Source anchors: %s\n", strings.Join(limitStrings(folder.SourceAnchors, 6), ", "))
		}
		if len(folder.MainSymbols) > 0 {
			fmt.Fprintf(&b, "- Main symbols: %s\n", formatSymbolNames(folder.MainSymbols, 6))
		}
	}
	fmt.Fprintf(&b, "\n## Folder To Test Mapping\n\n")
	for _, folder := range limitDeveloperFolderRecords(folders, 24) {
		if len(folder.TestFiles) == 0 {
			continue
		}
		fmt.Fprintf(&b, "- `%s`: %s\n", folder.Path, formatInlineCodeList(folder.TestFiles, 8))
	}
	fmt.Fprintf(&b, "\n## Folder Risk And Change Notes\n\n")
	for _, folder := range limitDeveloperFolderRecords(folders, 24) {
		if len(folder.RiskSignals) == 0 {
			continue
		}
		fmt.Fprintf(&b, "- `%s`: %s\n", folder.Path, strings.Join(limitStrings(folder.RiskSignals, 6), ", "))
	}
	return b.String()
}

func buildAnalysisModulesDoc(run ProjectAnalysisRun) string {
	var b strings.Builder
	modules := buildDeveloperModuleRecords(run)
	fmt.Fprintf(&b, "# Modules\n\n")
	analysisDocsWriteHeader(&b, run)
	analysisDocsWriteDocMetadata(&b, run, "MODULES.md")
	if len(modules) == 0 {
		fmt.Fprintf(&b, "\nNo module records were available. The project may be a flat package or the build graph was not discovered.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\n## Module Inventory\n\n")
	fmt.Fprintf(&b, "| Module | Kind | Root | Entrypoints | Dependencies | Confidence |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | --- | --- |\n")
	for _, module := range limitDeveloperModuleRecords(modules, 40) {
		fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s | %s | %s |\n",
			analysisMarkdownCell(module.Name),
			analysisMarkdownCell(module.Kind),
			analysisMarkdownCell(module.Root),
			analysisMarkdownCell(formatInlineCodeList(module.Entrypoints, 4)),
			analysisMarkdownCell(strings.Join(limitStrings(module.Dependencies, 5), ", ")),
			analysisMarkdownCell(firstNonBlankAnalysisString(module.Confidence, "medium")))
	}
	fmt.Fprintf(&b, "\n## Module Responsibility Cards\n\n")
	for _, module := range limitDeveloperModuleRecords(modules, 24) {
		fmt.Fprintf(&b, "### %s\n\n", module.Name)
		fmt.Fprintf(&b, "- Kind: %s\n", firstNonBlankAnalysisString(module.Kind, "module"))
		fmt.Fprintf(&b, "- Root: `%s`\n", firstNonBlankAnalysisString(module.Root, "."))
		fmt.Fprintf(&b, "- Responsibility: %s\n", firstNonBlankAnalysisString(module.Responsibility, "source module"))
		if len(module.PublicFiles) > 0 {
			fmt.Fprintf(&b, "- Public/key files: %s\n", formatInlineCodeList(module.PublicFiles, 8))
		}
		if len(module.InternalFiles) > 0 {
			fmt.Fprintf(&b, "- Internal files: %s\n", formatInlineCodeList(module.InternalFiles, 8))
		}
		if len(module.Entrypoints) > 0 {
			fmt.Fprintf(&b, "- Entrypoints: %s\n", formatInlineCodeList(module.Entrypoints, 8))
		}
		if len(module.SourceAnchors) > 0 {
			fmt.Fprintf(&b, "- Source anchors: %s\n", strings.Join(limitStrings(module.SourceAnchors, 8), ", "))
		}
	}
	fmt.Fprintf(&b, "\n## Public API And Boundary\n\n")
	for _, module := range limitDeveloperModuleRecords(modules, 24) {
		public := analysisUniqueStrings(append(append([]string{}, module.PublicFiles...), module.Entrypoints...))
		if len(public) == 0 {
			continue
		}
		fmt.Fprintf(&b, "- `%s`: public/key files %s\n", module.Name, formatInlineCodeList(public, 10))
	}
	fmt.Fprintf(&b, "\n## Internal Ownership\n\n")
	for _, module := range limitDeveloperModuleRecords(modules, 24) {
		if len(module.InternalFiles) == 0 && len(module.BuildContexts) == 0 {
			continue
		}
		fmt.Fprintf(&b, "- `%s`: internal=%s build_contexts=%s\n",
			module.Name,
			formatInlineCodeList(module.InternalFiles, 8),
			formatInlineCodeList(module.BuildContexts, 6))
	}
	fmt.Fprintf(&b, "\n## Module Dependencies\n\n")
	for _, module := range limitDeveloperModuleRecords(modules, 40) {
		if len(module.Dependencies) == 0 {
			continue
		}
		fmt.Fprintf(&b, "- `%s`: %s\n", module.Name, strings.Join(limitStrings(module.Dependencies, 10), ", "))
	}
	fmt.Fprintf(&b, "\n## Upstream Downstream Dependencies\n\n")
	dependencyNotes := developerModuleDependencyNotes(modules)
	if len(dependencyNotes) > 0 {
		for _, note := range limitStrings(dependencyNotes, 40) {
			fmt.Fprintf(&b, "- %s\n", note)
		}
	} else {
		fmt.Fprintf(&b, "No upstream/downstream dependency notes were inferred.\n")
	}
	fmt.Fprintf(&b, "\n## Change Impact Notes\n\n")
	for _, module := range limitDeveloperModuleRecords(modules, 24) {
		fmt.Fprintf(&b, "- `%s`: %s\n", module.Name, developerModuleImpactNote(module))
	}
	fmt.Fprintf(&b, "\n## Module Verification Notes\n\n")
	fmt.Fprintf(&b, "- Use `VERIFICATION_MATRIX.md` for required checks after module changes.\n")
	fmt.Fprintf(&b, "- Use `BUILD_AND_ARTIFACTS.md` when build context, compile command, target, or plugin ownership changes.\n")
	fmt.Fprintf(&b, "- Use `SECURITY_SURFACE.md` before editing modules with privileged or input-facing symbols.\n")
	return b.String()
}

func analysisDocsWriteStartupLens(b *strings.Builder, run ProjectAnalysisRun) {
	startup := strings.TrimSpace(run.Snapshot.PrimaryStartup)
	if startup == "" && len(run.Snapshot.EntrypointFiles) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Startup And Entrypoint Lens\n\n")
	if startup != "" {
		fmt.Fprintf(b, "- Solution startup candidate: `%s`\n", startup)
		if project, ok := solutionProjectByName(run.Snapshot.SolutionProjects, startup); ok {
			if strings.TrimSpace(project.OutputType) != "" || strings.TrimSpace(project.Kind) != "" {
				fmt.Fprintf(b, "- Startup project type: %s\n", firstNonBlankAnalysisString(project.OutputType, project.Kind))
			}
			if len(project.EntryFiles) > 0 {
				fmt.Fprintf(b, "- Startup project entry files: %s\n", formatInlineCodeList(project.EntryFiles, 6))
			}
		}
	}
	driverEntries := driverEntrypointFiles(run)
	if len(driverEntries) > 0 {
		fmt.Fprintf(b, "- Kernel/runtime driver entry files: %s\n", formatInlineCodeList(driverEntries, 6))
	}
	if len(run.Snapshot.EntrypointFiles) > 0 {
		fmt.Fprintf(b, "- Indexed entrypoint files: %s\n", formatInlineCodeList(run.Snapshot.EntrypointFiles, 10))
	}
	fmt.Fprintf(b, "\nDo not treat a Visual Studio startup executable as the only runtime entrypoint. For driver solutions, describe the user-mode harness, SCM/service load path, and kernel `DriverEntry` path as separate activation layers.\n")
}

func analysisDocsWriteIOCTLContract(b *strings.Builder, run ProjectAnalysisRun) {
	items := developerIOCTLSymbols(run)
	files := developerIOCTLFiles(run, items)
	if len(items) == 0 && len(files) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## IOCTL And Device-Control Contract\n\n")
	if len(files) > 0 {
		fmt.Fprintf(b, "- Contract/source files: %s\n", formatInlineCodeList(files, 10))
	}
	if len(items) > 0 {
		fmt.Fprintf(b, "\n| Symbol | Kind | File | Contract Role | Evidence |\n")
		fmt.Fprintf(b, "| --- | --- | --- | --- | --- |\n")
		for _, symbol := range limitSymbolRecords(items, 40) {
			location := analysisDocSlashPath(symbol.File)
			if symbol.StartLine > 0 {
				location = fmt.Sprintf("%s:%d", location, symbol.StartLine)
			}
			fmt.Fprintf(b, "| `%s` | %s | `%s` | %s | %s |\n",
				analysisMarkdownCell(firstNonBlankDeveloperString(symbol.CanonicalName, symbol.Name, symbol.ID)),
				analysisMarkdownCell(symbol.Kind),
				analysisMarkdownCell(location),
				analysisMarkdownCell(developerIOCTLRole(symbol)),
				analysisMarkdownCell(strings.Join(limitStrings(symbol.Tags, 5), ", ")))
		}
	}
	fmt.Fprintf(b, "\nReview checklist: enumerate IOCTL codes, request/response structs, caller validation, buffer probing/copy rules, and failure cleanup before changing this surface.\n")
}

func developerProjectShapeSummary(run ProjectAnalysisRun, folders []DeveloperFolderRecord) string {
	domainHints := projectStructureDomainHints(run)
	if analysisContainsStringCI(domainHints, "windows_driver") {
		parts := []string{"This project is a Windows kernel/WDM `.sys` driver solution with separate kernel runtime and user-mode control/test layers."}
		if folder := developerFolderByResponsibility(folders, "kernel driver runtime"); strings.TrimSpace(folder.Path) != "" {
			parts = append(parts, fmt.Sprintf("Kernel driver root: `%s/` (%s).", folder.Path, folder.Responsibility))
		}
		if folder := developerFolderByResponsibility(folders, "user-mode bootstrap"); strings.TrimSpace(folder.Path) != "" {
			parts = append(parts, fmt.Sprintf("User-mode harness/control root: `%s/` (%s).", folder.Path, folder.Responsibility))
		}
		if folder := developerFolderByResponsibility(folders, "shared kernel/user-mode contracts"); strings.TrimSpace(folder.Path) != "" {
			parts = append(parts, fmt.Sprintf("Shared contract root: `%s/` (%s).", folder.Path, folder.Responsibility))
		}
		names := []string{}
		for _, ctx := range run.Snapshot.BuildContexts {
			corpus := strings.ToLower(strings.Join(folderBuildContextTerms([]BuildContextRecord{ctx}), " "))
			if containsAny(corpus, "wdm_driver", ".sys", "kernelmodedriver") {
				names = append(names, firstNonBlankDeveloperString(ctx.Name, ctx.Project, ctx.ID))
			}
		}
		if len(names) > 0 {
			parts = append(parts, "Driver build contexts: "+formatInlineCodeList(analysisUniqueStrings(names), 4)+".")
		}
		return strings.Join(parts, " ")
	}
	if strings.TrimSpace(run.KnowledgePack.ProjectSummary) != "" {
		return strings.TrimSpace(run.KnowledgePack.ProjectSummary)
	}
	return fmt.Sprintf("This project contains %d scanned files across %d folders.", run.Snapshot.TotalFiles, len(folders))
}

func developerFolderByResponsibility(folders []DeveloperFolderRecord, phrase string) DeveloperFolderRecord {
	for _, folder := range folders {
		if strings.TrimSpace(folder.Path) == "." {
			continue
		}
		if strings.Contains(strings.ToLower(folder.Responsibility), strings.ToLower(phrase)) {
			return folder
		}
	}
	return DeveloperFolderRecord{}
}

func analysisDocsWriteDomainCriticalAnchors(b *strings.Builder, run ProjectAnalysisRun) {
	domainHints := projectStructureDomainHints(run)
	anchors := selectProjectStructureCriticalAnchors(run, relevantSemanticIndexV2Hits{}, projectAnalysisQAIntentDeepMap, 16)
	if len(domainHints) == 0 && len(anchors) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Domain Critical Anchors\n\n")
	if len(domainHints) > 0 {
		fmt.Fprintf(b, "- Domain hints: %s\n", strings.Join(limitStrings(domainHints, 8), ", "))
	}
	if analysisContainsStringCI(domainHints, "windows_driver") {
		fmt.Fprintf(b, "- Driver terminology: describe this as a Windows kernel/WDM `.sys` driver, not a DLL, unless a source artifact explicitly says DLL.\n")
		fmt.Fprintf(b, "- IOCTL ownership: separate user-mode manager/wrapper functions from kernel-side dispatch and validation functions.\n")
	}
	if len(anchors) == 0 {
		fmt.Fprintf(b, "- No domain-specific critical anchors were inferred.\n")
		return
	}
	fmt.Fprintf(b, "\n| Role | Symbol | Kind | File | Side | Verification |\n")
	fmt.Fprintf(b, "| --- | --- | --- | --- | --- | --- |\n")
	for _, anchor := range limitProjectStructureCriticalAnchors(anchors, 16) {
		location := analysisDocSlashPath(anchor.File)
		if anchor.Line > 0 {
			location = fmt.Sprintf("%s:%d", location, anchor.Line)
		}
		side := "source"
		if anchor.KernelSide {
			side = "kernel"
		} else if anchor.UserModeSide {
			side = "user"
		}
		fmt.Fprintf(b, "| `%s` | `%s` | %s | `%s` | %s | %s |\n",
			analysisMarkdownCell(anchor.Role),
			analysisMarkdownCell(anchor.Name),
			analysisMarkdownCell(anchor.Kind),
			analysisMarkdownCell(location),
			analysisMarkdownCell(side),
			analysisMarkdownCell(anchor.VerificationHint))
	}
}

func buildDeveloperStructureGraph(run ProjectAnalysisRun, modules []DeveloperModuleRecord) DeveloperStructureGraph {
	graph := DeveloperStructureGraph{}
	seenNodes := map[string]struct{}{}
	addNode := func(id string, label string, kind string, source string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seenNodes[id]; ok {
			return
		}
		seenNodes[id] = struct{}{}
		graph.Nodes = append(graph.Nodes, DeveloperStructureNode{
			ID:     id,
			Label:  firstNonBlankDeveloperString(label, id),
			Kind:   kind,
			Source: analysisDocSlashPath(source),
		})
	}
	moduleByDepName := map[string]DeveloperModuleRecord{}
	for _, module := range modules {
		addNode(module.ID, module.Name, module.Kind, firstSliceValue(module.SourceAnchors))
		moduleByDepName[strings.ToLower(module.Name)] = module
		moduleByDepName[strings.ToLower(module.ID)] = module
	}
	for _, module := range modules {
		for _, dep := range module.Dependencies {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			targetID := dep
			targetLabel := dep
			if target, ok := moduleByDepName[strings.ToLower(dep)]; ok {
				targetID = target.ID
				targetLabel = target.Name
			} else {
				addNode(dep, dep, "external_dependency", "")
			}
			graph.Edges = append(graph.Edges, DeveloperStructureEdge{
				Source:     module.ID,
				Target:     targetID,
				Type:       "depends_on",
				Confidence: module.Confidence,
				Evidence:   module.SourceAnchors,
			})
			addNode(targetID, targetLabel, "dependency", "")
		}
	}
	for _, edge := range run.SemanticIndexV2.BuildOwnershipEdges {
		addNode(edge.SourceID, edge.SourceID, "build_owner", firstSliceValue(edge.Evidence))
		addNode(edge.TargetID, edge.TargetID, "build_target", "")
		graph.Edges = append(graph.Edges, DeveloperStructureEdge{
			Source:     edge.SourceID,
			Target:     edge.TargetID,
			Type:       firstNonBlankAnalysisString(edge.Type, "owns"),
			Confidence: "high",
			Evidence:   edge.Evidence,
		})
	}
	graph.Edges = uniqueDeveloperStructureEdges(graph.Edges)
	return graph
}

func buildDeveloperFolderRecords(run ProjectAnalysisRun) []DeveloperFolderRecord {
	records := map[string]*DeveloperFolderRecord{}
	knownPathByBase := uniqueAnalysisDocPathByBase(run.Snapshot.Files)
	narrativeResponsibilities := map[string][]string{}
	get := func(path string) *DeveloperFolderRecord {
		path = normalizeAnalysisDocFolder(path)
		if existing, ok := records[path]; ok {
			return existing
		}
		record := &DeveloperFolderRecord{Path: path, Confidence: "medium"}
		records[path] = record
		return record
	}
	for _, file := range run.Snapshot.Files {
		dir := firstNonBlankAnalysisString(analysisDocSlashPath(file.Directory), analysisDocDir(file.Path))
		record := get(dir)
		if file.IsEntrypoint || file.IsManifest || file.ImportanceScore > 0 || len(record.KeyFiles) < 8 {
			record.KeyFiles = append(record.KeyFiles, analysisDocSlashPath(file.Path))
		}
		if analysisIsTestFile(file.Path) {
			record.TestFiles = append(record.TestFiles, analysisDocSlashPath(file.Path))
		}
	}
	for _, dir := range run.Snapshot.Directories {
		normalized := normalizeAnalysisDocFolder(dir)
		if normalized != "." && !analysisDocPathLooksLikeFile(normalized) {
			get(normalized)
		}
	}
	for _, subsystem := range run.KnowledgePack.Subsystems {
		title := canonicalKnowledgeTitle(subsystem)
		for _, file := range analysisDocResolvedPathCandidatesFromList(append(append([]string{}, subsystem.KeyFiles...), subsystem.EvidenceFiles...), knownPathByBase) {
			record := get(analysisDocDir(file))
			record.Subsystems = append(record.Subsystems, title)
			record.SourceAnchors = append(record.SourceAnchors, analysisDocSlashPath(file))
			record.KeyFiles = append(record.KeyFiles, analysisDocSlashPath(file))
			if len(subsystem.Responsibilities) > 0 {
				narrativeResponsibilities[record.Path] = append(narrativeResponsibilities[record.Path], subsystem.Responsibilities[0])
			}
			record.RiskSignals = append(record.RiskSignals, subsystem.InvalidationReasons...)
		}
	}
	for _, symbol := range run.SemanticIndexV2.Symbols {
		if strings.TrimSpace(symbol.File) == "" {
			continue
		}
		record := get(analysisDocDir(symbol.File))
		if len(record.MainSymbols) < 12 {
			record.MainSymbols = append(record.MainSymbols, symbol)
		}
		if containsAny(strings.ToLower(strings.Join(append([]string{symbol.Kind, symbol.Name, symbol.CanonicalName}, symbol.Tags...), " ")), "ioctl", "rpc", "network", "security", "driver", "kernel", "auth", "parser") {
			record.RiskSignals = append(record.RiskSignals, "security-sensitive symbol: "+firstNonBlankAnalysisString(symbol.CanonicalName, symbol.Name))
		}
	}
	for _, ctx := range run.Snapshot.BuildContexts {
		for _, file := range ctx.Files {
			record := get(analysisDocDir(file))
			record.BuildContexts = append(record.BuildContexts, ctx)
			record.SourceAnchors = append(record.SourceAnchors, analysisDocSlashPath(file))
		}
		if strings.TrimSpace(ctx.Directory) != "" {
			record := get(ctx.Directory)
			record.BuildContexts = append(record.BuildContexts, ctx)
		}
	}
	for _, file := range analysisDocResolvedPathCandidatesFromList(run.KnowledgePack.HighRiskFiles, knownPathByBase) {
		record := get(analysisDocDir(file))
		record.RiskSignals = append(record.RiskSignals, "high-risk file")
		record.SourceAnchors = append(record.SourceAnchors, analysisDocSlashPath(file))
	}
	out := make([]DeveloperFolderRecord, 0, len(records))
	for _, record := range records {
		if analysisDocPathLooksLikeFile(record.Path) {
			continue
		}
		record.KeyFiles = analysisUniqueStrings(record.KeyFiles)
		record.TestFiles = analysisUniqueStrings(record.TestFiles)
		record.Subsystems = analysisUniqueStrings(record.Subsystems)
		record.RiskSignals = analysisUniqueStrings(record.RiskSignals)
		record.SourceAnchors = analysisUniqueStrings(record.SourceAnchors)
		record.BuildContexts = uniqueDeveloperBuildContexts(record.BuildContexts)
		record.Responsibility = mergeFolderResponsibility(inferFolderResponsibility(*record), narrativeResponsibilities[record.Path])
		if len(record.SourceAnchors) == 0 {
			record.SourceAnchors = append(record.SourceAnchors, record.KeyFiles...)
		}
		out = append(out, *record)
	}
	sort.Slice(out, func(i int, j int) bool {
		if len(out[i].RiskSignals) != len(out[j].RiskSignals) {
			return len(out[i].RiskSignals) > len(out[j].RiskSignals)
		}
		if len(out[i].KeyFiles) != len(out[j].KeyFiles) {
			return len(out[i].KeyFiles) > len(out[j].KeyFiles)
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func buildDeveloperModuleRecords(run ProjectAnalysisRun) []DeveloperModuleRecord {
	records := map[string]*DeveloperModuleRecord{}
	add := func(record DeveloperModuleRecord) {
		if strings.TrimSpace(record.ID) == "" {
			record.ID = "module:" + firstNonBlankAnalysisString(record.Name, record.Root)
		}
		existing, ok := records[record.ID]
		if !ok {
			record.Name = firstNonBlankAnalysisString(record.Name, record.ID)
			record.Kind = firstNonBlankAnalysisString(record.Kind, "module")
			record.Root = analysisDocSlashPath(firstNonBlankAnalysisString(record.Root, "."))
			record.Confidence = firstNonBlankAnalysisString(record.Confidence, "medium")
			copy := record
			records[record.ID] = &copy
			return
		}
		existing.PublicFiles = append(existing.PublicFiles, record.PublicFiles...)
		existing.InternalFiles = append(existing.InternalFiles, record.InternalFiles...)
		existing.Entrypoints = append(existing.Entrypoints, record.Entrypoints...)
		existing.Dependencies = append(existing.Dependencies, record.Dependencies...)
		existing.BuildContexts = append(existing.BuildContexts, record.BuildContexts...)
		existing.SourceAnchors = append(existing.SourceAnchors, record.SourceAnchors...)
		if strings.TrimSpace(existing.Responsibility) == "" {
			existing.Responsibility = record.Responsibility
		}
	}
	for _, ctx := range run.Snapshot.BuildContexts {
		name := firstNonBlankDeveloperString(ctx.Module, ctx.Name, ctx.Project, ctx.Target, ctx.ID)
		add(DeveloperModuleRecord{
			ID:            firstNonBlankAnalysisString(ctx.ID, "buildctx:"+name),
			Name:          name,
			Kind:          firstNonBlankAnalysisString(ctx.Kind, "build_context"),
			Root:          firstNonBlankDeveloperString(ctx.Directory, commonDirectory(ctx.Files), "."),
			PublicFiles:   limitStrings(ctx.Files, 12),
			BuildContexts: []string{ctx.ID},
			SourceAnchors: append([]string{}, ctx.Files...),
			Confidence:    "high",
		})
	}
	for _, module := range run.Snapshot.UnrealModules {
		add(DeveloperModuleRecord{
			ID:           "unreal_module:" + module.Name,
			Name:         module.Name,
			Kind:         firstNonBlankAnalysisString(module.Kind, "unreal_module"),
			Root:         module.Path,
			Dependencies: append(append([]string{}, module.PublicDependencies...), module.PrivateDependencies...),
			SourceAnchors: []string{
				analysisDocSlashPath(module.Path),
			},
			Confidence: "high",
		})
	}
	for _, project := range run.Snapshot.SolutionProjects {
		add(DeveloperModuleRecord{
			ID:            "solution_project:" + firstNonBlankAnalysisString(project.Name, project.Path),
			Name:          firstNonBlankAnalysisString(project.Name, project.Path),
			Kind:          firstNonBlankAnalysisString(project.Kind, "solution_project"),
			Root:          firstNonBlankAnalysisString(analysisDocSlashPath(project.Directory), analysisDocDir(project.Path)),
			PublicFiles:   []string{project.Path},
			Entrypoints:   project.EntryFiles,
			Dependencies:  project.ProjectReferences,
			SourceAnchors: []string{project.Path},
			Confidence:    "high",
		})
	}
	if len(records) == 0 && (strings.TrimSpace(run.Snapshot.ModulePath) != "" || len(run.Snapshot.Files) > 0) {
		files := []string{}
		entrypoints := []string{}
		for _, file := range run.Snapshot.Files {
			files = append(files, file.Path)
			if file.IsEntrypoint {
				entrypoints = append(entrypoints, file.Path)
			}
		}
		add(DeveloperModuleRecord{
			ID:            "package:" + firstNonBlankDeveloperString(run.Snapshot.ModulePath, filepath.Base(run.Snapshot.Root), "root"),
			Name:          firstNonBlankDeveloperString(run.Snapshot.ModulePath, filepath.Base(run.Snapshot.Root), "root"),
			Kind:          "package",
			Root:          ".",
			PublicFiles:   limitStrings(files, 20),
			Entrypoints:   entrypoints,
			SourceAnchors: limitStrings(files, 20),
			Confidence:    "medium",
		})
	}
	for _, subsystem := range run.KnowledgePack.Subsystems {
		title := canonicalKnowledgeTitle(subsystem)
		for _, record := range records {
			if recordMatchesSubsystem(*record, subsystem) {
				record.Responsibility = firstNonBlankDeveloperString(record.Responsibility, firstSliceValue(subsystem.Responsibilities), title)
				record.SourceAnchors = append(record.SourceAnchors, subsystem.KeyFiles...)
			}
		}
	}
	out := make([]DeveloperModuleRecord, 0, len(records))
	for _, record := range records {
		record.PublicFiles = analysisUniqueStrings(record.PublicFiles)
		record.InternalFiles = analysisUniqueStrings(record.InternalFiles)
		record.Entrypoints = analysisUniqueStrings(record.Entrypoints)
		record.Dependencies = analysisUniqueStrings(record.Dependencies)
		record.BuildContexts = analysisUniqueStrings(record.BuildContexts)
		record.SourceAnchors = analysisUniqueStrings(record.SourceAnchors)
		if strings.TrimSpace(record.Responsibility) == "" {
			record.Responsibility = inferModuleResponsibility(*record)
		}
		out = append(out, *record)
	}
	sort.Slice(out, func(i int, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func analysisDeveloperDocSourceAnchors(run ProjectAnalysisRun, name string) []string {
	switch name {
	case "FOLDER_MAP.md":
		items := []string{}
		for _, folder := range buildDeveloperFolderRecords(run) {
			items = append(items, folder.SourceAnchors...)
			items = append(items, folder.KeyFiles...)
		}
		return analysisUniqueStrings(items)
	case "MODULES.md":
		items := []string{}
		for _, module := range buildDeveloperModuleRecords(run) {
			items = append(items, module.SourceAnchors...)
			items = append(items, module.PublicFiles...)
			items = append(items, module.Entrypoints...)
		}
		return analysisUniqueStrings(items)
	case "STRUCTURE_DIAGRAMS.md":
		items := developerStructureGraphAnchors(buildDeveloperStructureGraph(run, buildDeveloperModuleRecords(run)))
		items = append(items, runtimeEdgeAnchors(run.Snapshot.RuntimeEdges)...)
		items = append(items, developerBuildArtifactAnchors(run)...)
		items = append(items, analysisGraphSourceAnchors(run)...)
		return analysisUniqueStrings(items)
	case "CODE_STRUCTURE_REFERENCE.md":
		items := []string{}
		items = append(items, analysisDocSlashPaths(run.KnowledgePack.TopImportantFiles)...)
		items = append(items, analysisDocSlashPaths(run.KnowledgePack.HighRiskFiles)...)
		items = append(items, symbolFiles(developerImportantSymbols(run))...)
		for _, edge := range run.SemanticIndexV2.BuildOwnershipEdges {
			items = append(items, analysisDocSlashPaths(edge.Evidence)...)
		}
		for _, edge := range run.SemanticIndexV2.GeneratedCodeEdges {
			items = append(items, analysisDocSlashPath(edge.SourceFile))
			items = append(items, analysisDocSlashPaths(edge.Evidence)...)
		}
		return analysisUniqueStrings(items)
	default:
		return analysisUniqueStrings(append(append([]string{}, run.Snapshot.EntrypointFiles...), run.KnowledgePack.TopImportantFiles...))
	}
}

func developerModuleGraphViews(graph DeveloperStructureGraph) []analysisGraphEdgeView {
	labelByID := map[string]string{}
	for _, node := range graph.Nodes {
		labelByID[node.ID] = firstNonBlankDeveloperString(node.Label, node.ID)
	}
	views := []analysisGraphEdgeView{}
	for _, edge := range graph.Edges {
		if !containsAny(strings.ToLower(edge.Type), "depend", "own", "align", "module", "project", "target") {
			continue
		}
		views = append(views, analysisGraphEdgeView{
			Source:     firstNonBlankDeveloperString(labelByID[edge.Source], edge.Source),
			Target:     firstNonBlankDeveloperString(labelByID[edge.Target], edge.Target),
			Type:       edge.Type,
			Class:      "build",
			Flow:       firstNonBlankAnalysisString(edge.Type, "depends_on"),
			Confidence: edge.Confidence,
			Evidence:   strings.Join(limitStrings(analysisDocSlashPaths(edge.Evidence), 3), ", "),
			Next:       "/analyze-dashboard",
		})
	}
	return views
}

func developerFolderModuleViews(run ProjectAnalysisRun, modules []DeveloperModuleRecord) []analysisGraphEdgeView {
	views := []analysisGraphEdgeView{}
	seen := map[string]struct{}{}
	addView := func(view analysisGraphEdgeView) {
		if strings.EqualFold(strings.TrimSpace(view.Source), strings.TrimSpace(view.Target)) {
			return
		}
		key := strings.ToLower(strings.Join([]string{view.Source, view.Flow, view.Target}, "|"))
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		views = append(views, view)
	}
	for _, module := range modules {
		files := append(append([]string{}, module.PublicFiles...), module.InternalFiles...)
		files = append(files, module.SourceAnchors...)
		for _, folder := range foldersForFiles(files) {
			addView(analysisGraphEdgeView{
				Source:     normalizeAnalysisDocFolder(folder),
				Target:     module.Name,
				Type:       "contains_module",
				Class:      "build",
				Flow:       "contains",
				Confidence: module.Confidence,
				Evidence:   strings.Join(limitStrings(analysisDocSlashPaths(files), 3), ", "),
				Next:       "/analyze-dashboard",
			})
		}
	}
	if len(views) == 0 {
		for _, folder := range limitDeveloperFolderRecords(buildDeveloperFolderRecords(run), 20) {
			for _, subsystem := range limitStrings(folder.Subsystems, 3) {
				addView(analysisGraphEdgeView{
					Source:     folder.Path,
					Target:     subsystem,
					Type:       "maps_to_subsystem",
					Class:      "relationship",
					Flow:       "maps_to",
					Confidence: folder.Confidence,
					Evidence:   strings.Join(limitStrings(analysisDocSlashPaths(folder.SourceAnchors), 3), ", "),
					Next:       "/analyze-dashboard",
				})
			}
		}
	}
	return views
}

func developerRuntimeGraphViews(run ProjectAnalysisRun) []analysisGraphEdgeView {
	edges := runtimeEdgesForStartup(run.Snapshot.RuntimeEdges, run.Snapshot.PrimaryStartup)
	if len(edges) == 0 {
		edges = highConfidenceRuntimeEdges(run.Snapshot.RuntimeEdges)
	}
	views := []analysisGraphEdgeView{}
	for _, edge := range limitRuntimeEdges(edges, 24) {
		if strings.EqualFold(strings.TrimSpace(edge.Source), strings.TrimSpace(edge.Target)) {
			continue
		}
		views = append(views, analysisGraphEdgeView{
			Source:     analysisDocSlashPath(edge.Source),
			Target:     analysisDocSlashPath(edge.Target),
			Type:       edge.Kind,
			Class:      "runtime",
			Flow:       firstNonBlankAnalysisString(edge.Kind, "runtime"),
			Confidence: edge.Confidence,
			Evidence:   strings.Join(limitStrings(analysisDocSlashPaths(edge.Evidence), 3), ", "),
			Next:       "/analyze-dashboard",
		})
	}
	return views
}

func developerBuildArtifactViews(run ProjectAnalysisRun) []analysisGraphEdgeView {
	views := []analysisGraphEdgeView{}
	for _, edge := range limitBuildOwnershipEdges(run.SemanticIndexV2.BuildOwnershipEdges, 30) {
		if strings.EqualFold(strings.TrimSpace(edge.SourceID), strings.TrimSpace(edge.TargetID)) {
			continue
		}
		views = append(views, analysisGraphEdgeView{
			Source:     edge.SourceID,
			Target:     edge.TargetID,
			Type:       edge.Type,
			Class:      "build",
			Flow:       firstNonBlankAnalysisString(edge.Type, "owns"),
			Confidence: "high",
			Evidence:   strings.Join(limitStrings(analysisDocSlashPaths(edge.Evidence), 3), ", "),
			Next:       "/analyze-dashboard",
		})
	}
	for _, edge := range limitGeneratedCodeEdges(run.SemanticIndexV2.GeneratedCodeEdges, 20) {
		if strings.EqualFold(strings.TrimSpace(edge.SourceFile), strings.TrimSpace(edge.TargetID)) {
			continue
		}
		views = append(views, analysisGraphEdgeView{
			Source:     analysisDocSlashPath(edge.SourceFile),
			Target:     analysisDocSlashPath(edge.TargetID),
			Type:       edge.Type,
			Class:      "build",
			Flow:       firstNonBlankAnalysisString(edge.Type, "generates"),
			Confidence: "high",
			Evidence:   strings.Join(limitStrings(analysisDocSlashPaths(edge.Evidence), 3), ", "),
			Next:       "/analyze-dashboard",
		})
	}
	return views
}

func developerUnrealGraphViews(run ProjectAnalysisRun) []analysisGraphEdgeView {
	views := []analysisGraphEdgeView{}
	for _, edge := range limitUnrealSemanticEdges(run.UnrealGraph.Edges, 30) {
		if strings.EqualFold(strings.TrimSpace(edge.Source), strings.TrimSpace(edge.Target)) {
			continue
		}
		evidence := []string{}
		for key, value := range edge.Attributes {
			if containsAny(strings.ToLower(key), "file", "path", "source") {
				evidence = append(evidence, analysisDocSlashPath(value))
			}
		}
		views = append(views, analysisGraphEdgeView{
			Source:     edge.Source,
			Target:     edge.Target,
			Type:       edge.Type,
			Class:      "unreal",
			Flow:       firstNonBlankAnalysisString(edge.Type, "unreal"),
			Confidence: "medium",
			Evidence:   strings.Join(limitStrings(analysisUniqueStrings(evidence), 3), ", "),
			Next:       "/analyze-dashboard",
		})
	}
	return views
}

func developerCrossCuttingPaths(run ProjectAnalysisRun) []string {
	items := []string{}
	for _, subsystem := range run.KnowledgePack.Subsystems {
		title := canonicalKnowledgeTitle(subsystem)
		if len(subsystem.EntryPoints) > 0 || len(subsystem.Dependencies) > 0 {
			items = append(items, fmt.Sprintf("`%s`: entry=%s dependencies=%s",
				title,
				formatInlineCodeList(subsystem.EntryPoints, 4),
				strings.Join(limitStrings(subsystem.Dependencies, 5), ", ")))
		}
	}
	for _, edge := range limitCallEdges(run.SemanticIndexV2.CallEdges, 16) {
		items = append(items, fmt.Sprintf("call `%s` -> `%s` (%s), evidence=%s",
			edge.SourceID,
			edge.TargetID,
			firstNonBlankAnalysisString(edge.Type, "calls"),
			strings.Join(limitStrings(analysisDocSlashPaths(edge.Evidence), 3), ", ")))
	}
	for _, edge := range limitBuildOwnershipEdges(run.SemanticIndexV2.BuildOwnershipEdges, 12) {
		items = append(items, fmt.Sprintf("build `%s` -> `%s` (%s), evidence=%s",
			edge.SourceID,
			edge.TargetID,
			firstNonBlankAnalysisString(edge.Type, "owns"),
			strings.Join(limitStrings(analysisDocSlashPaths(edge.Evidence), 3), ", ")))
	}
	return analysisUniqueStrings(items)
}

func developerImportantSymbols(run ProjectAnalysisRun) []SymbolRecord {
	out := []SymbolRecord{}
	importantFiles := map[string]struct{}{}
	for _, file := range append(append([]string{}, run.KnowledgePack.TopImportantFiles...), run.KnowledgePack.HighRiskFiles...) {
		importantFiles[analysisDocSlashPath(file)] = struct{}{}
	}
	for _, symbol := range run.SemanticIndexV2.Symbols {
		text := strings.ToLower(strings.Join(append([]string{symbol.Kind, symbol.Name, symbol.CanonicalName, symbol.File}, symbol.Tags...), " "))
		_, importantFile := importantFiles[analysisDocSlashPath(symbol.File)]
		if importantFile || containsAny(text, "main", "entry", "dispatch", "ioctl", "rpc", "verify", "fuzz", "security", "analysis", "build") {
			out = append(out, symbol)
		}
	}
	if len(out) == 0 {
		out = append(out, run.SemanticIndexV2.Symbols...)
	}
	sortSymbolRecords(out)
	return out
}

func developerSymbolClusters(run ProjectAnalysisRun) map[string][]SymbolRecord {
	clusters := map[string][]SymbolRecord{}
	for _, symbol := range run.SemanticIndexV2.Symbols {
		key := firstNonBlankDeveloperString(symbol.Module, symbol.BuildContextID, analysisDocDir(symbol.File), symbol.Kind, "global")
		clusters[key] = append(clusters[key], symbol)
	}
	for key, symbols := range clusters {
		sortSymbolRecords(symbols)
		if len(symbols) > 12 {
			symbols = symbols[:12]
		}
		clusters[key] = symbols
	}
	return clusters
}

func developerCallerCalleeHotspots(run ProjectAnalysisRun) []string {
	counts := map[string]int{}
	evidence := map[string][]string{}
	for _, edge := range run.SemanticIndexV2.CallEdges {
		if strings.TrimSpace(edge.SourceID) != "" {
			counts[edge.SourceID]++
			evidence[edge.SourceID] = append(evidence[edge.SourceID], edge.Evidence...)
		}
		if strings.TrimSpace(edge.TargetID) != "" {
			counts[edge.TargetID]++
			evidence[edge.TargetID] = append(evidence[edge.TargetID], edge.Evidence...)
		}
	}
	type hotspot struct {
		ID    string
		Count int
	}
	items := []hotspot{}
	for id, count := range counts {
		items = append(items, hotspot{ID: id, Count: count})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].ID < items[j].ID
		}
		return items[i].Count > items[j].Count
	})
	out := []string{}
	for _, item := range items {
		out = append(out, fmt.Sprintf("`%s`: call_degree=%d evidence=%s",
			item.ID,
			item.Count,
			strings.Join(limitStrings(analysisDocSlashPaths(evidence[item.ID]), 3), ", ")))
		if len(out) >= 20 {
			break
		}
	}
	return out
}

func mapKeysSorted(items map[string][]SymbolRecord) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func developerModuleDependencyNotes(modules []DeveloperModuleRecord) []string {
	reverse := map[string][]string{}
	for _, module := range modules {
		for _, dep := range module.Dependencies {
			key := strings.ToLower(strings.TrimSpace(dep))
			if key == "" {
				continue
			}
			reverse[key] = append(reverse[key], module.Name)
		}
	}
	out := []string{}
	for _, module := range modules {
		key := strings.ToLower(strings.TrimSpace(module.Name))
		downstream := analysisUniqueStrings(reverse[key])
		out = append(out, fmt.Sprintf("`%s`: upstream=%s downstream=%s",
			module.Name,
			strings.Join(limitStrings(module.Dependencies, 6), ", "),
			strings.Join(limitStrings(downstream, 6), ", ")))
	}
	return out
}

func developerModuleImpactNote(module DeveloperModuleRecord) string {
	parts := []string{}
	if len(module.Entrypoints) > 0 {
		parts = append(parts, "entrypoint changes can alter runtime behavior")
	}
	if len(module.Dependencies) > 0 {
		parts = append(parts, "dependency changes may affect downstream modules")
	}
	corpus := strings.ToLower(strings.Join(append(append([]string{module.Name, module.Kind, module.Root}, module.PublicFiles...), module.SourceAnchors...), " "))
	if containsAny(corpus, "driver", "ioctl", "rpc", "security", "anti", "unreal", "replicated") {
		parts = append(parts, "security or authority boundary review is recommended")
	}
	if len(module.BuildContexts) > 0 {
		parts = append(parts, "rebuild related build contexts")
	}
	if len(parts) == 0 {
		parts = append(parts, "check folder-local tests and module callers before editing")
	}
	return strings.Join(parts, "; ")
}

func developerIOCTLSymbols(run ProjectAnalysisRun) []SymbolRecord {
	out := []SymbolRecord{}
	for _, symbol := range run.SemanticIndexV2.Symbols {
		corpus := strings.ToLower(strings.Join(append([]string{
			symbol.ID,
			symbol.Name,
			symbol.CanonicalName,
			symbol.Kind,
			symbol.File,
			symbol.Signature,
		}, symbol.Tags...), " "))
		if containsAny(corpus, "ioctl", "deviceiocontrol", "devicecontrol", "device_control", "ctl_code", "irp_mj_device_control", "irp", "dispatch") {
			out = append(out, symbol)
		}
	}
	sortSymbolRecords(out)
	return out
}

func developerIOCTLFiles(run ProjectAnalysisRun, symbols []SymbolRecord) []string {
	items := []string{}
	for _, symbol := range symbols {
		items = append(items, analysisDocSlashPath(symbol.File))
	}
	for _, file := range append(append([]string{}, run.Snapshot.EntrypointFiles...), run.KnowledgePack.TopImportantFiles...) {
		lower := strings.ToLower(analysisDocSlashPath(file))
		if containsAny(lower, "ioctl", "devicecontrol", "device_control", "usercommon", "kernelcommon", "dispatch", "irp") {
			items = append(items, analysisDocSlashPath(file))
		}
	}
	for _, subsystem := range run.KnowledgePack.Subsystems {
		corpus := strings.ToLower(strings.Join(append(append(append([]string{
			subsystem.Title,
			subsystem.Group,
		}, subsystem.Responsibilities...), subsystem.EntryPoints...), subsystem.KeyFiles...), " "))
		if !containsAny(corpus, "ioctl", "device-control", "device control", "deviceiocontrol", "irp") {
			continue
		}
		items = append(items, analysisDocSlashPaths(subsystem.KeyFiles)...)
		items = append(items, analysisDocSlashPaths(subsystem.EvidenceFiles)...)
	}
	return analysisUniqueStrings(items)
}

func developerIOCTLRole(symbol SymbolRecord) string {
	nameCorpus := strings.ToLower(strings.Join([]string{symbol.ID, symbol.Name, symbol.CanonicalName, symbol.Kind, symbol.Signature}, " "))
	tagCorpus := strings.ToLower(strings.Join(symbol.Tags, " "))
	corpus := strings.TrimSpace(nameCorpus + " " + tagCorpus + " " + strings.ToLower(analysisDocSlashPath(symbol.File)) + " " + strings.ToLower(symbol.BuildContextID))
	switch {
	case developerSymbolLooksUserModeControlWrapper(corpus):
		return "user-mode request issuer"
	case containsAny(corpus, "deviceiocontrol"):
		if containsAny(corpus, "irp", "routine", "handler", "kernel", "drivercore", "driver_core") {
			return "kernel dispatch or handler"
		}
		return "user-mode request issuer"
	case containsAny(corpus, "probe", "copy", "validate", "validcommand", "isvalid", "requestor", "controlpid", "decrypt", "encrypt", "payload", "unpack", "parse", "marshal", "buffer"):
		return "validation or buffer gate"
	case containsAny(corpus, "irp_mj_device_control", "dispatch", "handler", "routine"):
		return "kernel dispatch or handler"
	case containsAny(nameCorpus, "ctl_code", "ioctl_") && !containsAny(tagCorpus, "ioctl_surface"):
		return "IOCTL code or constant"
	default:
		return "device-control related symbol"
	}
}

func developerSymbolLooksUserModeControlWrapper(corpus string) bool {
	if !containsAny(corpus, "testconsole", "manager", "client", "user-mode", "usermode", "service", "deviceiocontrol") {
		return false
	}
	if containsAny(corpus, "irp", "irp_mj", "drivercore", "driver_core", "kernelcore", "kernel_core", "defaultirphandleroutine", "deviceiocontrolirphandleroutine") {
		return false
	}
	return true
}

func solutionProjectByName(projects []SolutionProject, name string) (SolutionProject, bool) {
	for _, project := range projects {
		if strings.EqualFold(strings.TrimSpace(project.Name), strings.TrimSpace(name)) {
			return project, true
		}
	}
	return SolutionProject{}, false
}

func driverEntrypointFiles(run ProjectAnalysisRun) []string {
	items := []string{}
	for _, project := range run.Snapshot.SolutionProjects {
		if solutionProjectLooksLikeDriverRuntime(project) {
			items = append(items, analysisDocSlashPaths(project.EntryFiles)...)
		}
	}
	for _, symbol := range run.SemanticIndexV2.Symbols {
		corpus := strings.ToLower(strings.Join([]string{symbol.Name, symbol.CanonicalName, symbol.Kind, symbol.File}, " "))
		if containsAny(corpus, "driverentry") {
			items = append(items, analysisDocSlashPath(symbol.File))
		}
	}
	for _, file := range run.Snapshot.EntrypointFiles {
		if pathLooksLikeDriverEntrypointFile(file) {
			items = append(items, analysisDocSlashPath(file))
		}
	}
	return analysisUniqueStrings(items)
}

func solutionProjectLooksLikeDriverRuntime(project SolutionProject) bool {
	kind := strings.ToLower(strings.Join([]string{project.Kind, project.OutputType}, " "))
	if containsAny(kind, "driver", "wdm", "kernelmodedriver") {
		return true
	}
	for _, file := range project.EntryFiles {
		if pathLooksLikeDriverEntrypointFile(file) {
			return true
		}
	}
	return false
}

func pathLooksLikeDriverEntrypointFile(path string) bool {
	lower := strings.ToLower(analysisDocSlashPath(path))
	base := strings.ToLower(filepath.Base(lower))
	if containsAny(lower, "test", "console", "sample", "client", "manager", "app") {
		return false
	}
	return containsAny(base, "driverentry", "driver", "kernel") || containsAny(lower, "/driver/", "/kernel/")
}

func developerSymbolNameByID(symbols []SymbolRecord) map[string]string {
	out := map[string]string{}
	for _, symbol := range symbols {
		out[symbol.ID] = firstNonBlankDeveloperString(symbol.CanonicalName, symbol.Name, symbol.ID)
	}
	return out
}

func developerStructureGraphAnchors(graph DeveloperStructureGraph) []string {
	items := []string{}
	for _, node := range graph.Nodes {
		items = append(items, analysisDocSlashPath(node.Source))
	}
	for _, edge := range graph.Edges {
		items = append(items, analysisDocSlashPaths(edge.Evidence)...)
	}
	return analysisUniqueStrings(items)
}

func runtimeEdgeAnchors(edges []RuntimeEdge) []string {
	items := []string{}
	for _, edge := range edges {
		items = append(items, analysisDocSlashPaths(edge.Evidence)...)
	}
	return analysisUniqueStrings(items)
}

func developerBuildArtifactAnchors(run ProjectAnalysisRun) []string {
	items := []string{}
	for _, edge := range run.SemanticIndexV2.BuildOwnershipEdges {
		items = append(items, analysisDocSlashPaths(edge.Evidence)...)
	}
	for _, edge := range run.SemanticIndexV2.GeneratedCodeEdges {
		items = append(items, analysisDocSlashPath(edge.SourceFile))
		items = append(items, analysisDocSlashPaths(edge.Evidence)...)
	}
	return analysisUniqueStrings(items)
}

func foldersForFiles(files []string) []string {
	out := []string{}
	for _, file := range files {
		file = analysisDocSlashPath(file)
		if file == "" {
			continue
		}
		out = append(out, normalizeAnalysisDocFolder(filepath.Dir(file)))
	}
	return analysisUniqueStrings(out)
}

func uniqueDeveloperStructureEdges(items []DeveloperStructureEdge) []DeveloperStructureEdge {
	seen := map[string]struct{}{}
	out := []DeveloperStructureEdge{}
	for _, item := range items {
		key := strings.Join([]string{item.Source, item.Type, item.Target}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func limitBuildOwnershipEdges(items []BuildOwnershipEdge, limit int) []BuildOwnershipEdge {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]BuildOwnershipEdge(nil), items...)
	}
	return append([]BuildOwnershipEdge(nil), items[:limit]...)
}

func limitGeneratedCodeEdges(items []GeneratedCodeEdge, limit int) []GeneratedCodeEdge {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]GeneratedCodeEdge(nil), items...)
	}
	return append([]GeneratedCodeEdge(nil), items[:limit]...)
}

func inferFolderResponsibility(record DeveloperFolderRecord) string {
	parts := []string{record.Path}
	parts = append(parts, record.KeyFiles...)
	parts = append(parts, record.Subsystems...)
	parts = append(parts, folderSymbolNames(record.MainSymbols)...)
	parts = append(parts, folderBuildContextTerms(record.BuildContexts)...)
	text := strings.ToLower(strings.Join(parts, " "))
	switch {
	case folderLooksLikeSolutionConfigurationRoot(record, text):
		return "solution root, manifests, and top-level configuration"
	case folderLooksLikeKernelDriverRuntime(text):
		return "kernel driver runtime, privileged dispatch, and protection subsystems"
	case folderLooksLikeSharedContracts(record, text):
		return "shared kernel/user-mode contracts and common utilities"
	case folderLooksLikeUserModeDriverControlHarness(record, text):
		return "user-mode bootstrap, service lifecycle, and driver control harness"
	case folderLooksLikeBuildReleaseTooling(text):
		return "build, release, packaging, or tooling support"
	case containsAny(text, "test", "_test"):
		return "tests and verification coverage"
	case containsAny(text, "build", "release", "script", "tool"):
		return "build, release, or tooling support"
	case containsAny(text, "doc", "readme", "spec"):
		return "documentation and specifications"
	case containsAny(text, "ui", "viewer", "dashboard"):
		return "user interface and developer-facing views"
	case containsAny(text, "analysis", "index", "graph"):
		return "project analysis and code intelligence"
	case containsAny(text, "verify", "fuzz", "evidence", "investigation"):
		return "verification, fuzzing, and evidence workflows"
	default:
		return "source area"
	}
}

func folderLooksLikeSolutionConfigurationRoot(record DeveloperFolderRecord, text string) bool {
	if normalizeAnalysisDocFolder(record.Path) != "." {
		return false
	}
	return containsAny(text, ".sln", ".props", ".targets", ".vmp", "solution")
}

func folderLooksLikeKernelDriverRuntime(text string) bool {
	return containsAny(text,
		"driverentry",
		"wdm_driver",
		"kernelmodedriver",
		".sys",
		"driver_project",
		"kernel_driver",
		"kernel mode",
		"kernel-mode",
		"irp_mj_device_control",
		"deviceiocontrolirphandleroutine",
		"defaultirphandleroutine",
		"obregistercallbacks",
		"pssetcreateprocess",
		"fltregisterfilter",
		"fltstartfiltering",
		"minifilter",
	)
}

func folderLooksLikeSharedContracts(record DeveloperFolderRecord, text string) bool {
	pathAndFiles := strings.ToLower(strings.Join(append([]string{record.Path}, record.KeyFiles...), " "))
	if containsAny(pathAndFiles,
		"common",
		"/common/",
		"shared",
		"/shared/",
		"include",
		"/include/",
		"contracts",
		"/contracts/",
		"usercommon",
		"kernelcommon",
		"pehelper",
		"securemetastring",
		"ntapi.h",
	) {
		return true
	}
	return containsAny(text, "shared kernel/user-mode contracts", "common utilities")
}

func folderLooksLikeUserModeDriverControlHarness(record DeveloperFolderRecord, text string) bool {
	pathAndFiles := strings.ToLower(strings.Join(append([]string{record.Path}, record.KeyFiles...), " "))
	symbols := strings.ToLower(strings.Join(folderSymbolNames(record.MainSymbols), " "))
	if !containsAny(pathAndFiles, "testconsole", "manager.cpp", "manager.h", "/client", "/console", "/app") &&
		!containsAny(symbols, "createservice", "startservice", "controlservice", "openscmanager", "deviceiocontrol") {
		return false
	}
	return containsAny(text,
		"testconsole",
		"manager.cpp",
		"manager.h",
		"createservice",
		"startservice",
		"controlservice",
		"openscmanager",
		"service control manager",
		"deviceiocontrol",
		"main()",
		"console application",
		"harness",
	)
}

func folderLooksLikeBuildReleaseTooling(text string) bool {
	if containsAny(text,
		"buildcab",
		"/buildcab",
		"batch/",
		"/batch",
		".bat",
		".cmd",
		".ddf",
		".inf",
		"makefile",
		"cmake",
		"signing",
		"package",
		"installer",
		"deploy",
		"vmprotect",
	) {
		return true
	}
	return containsAny(text, "build", "release") &&
		containsAny(text, "script", "package", "installer", "signing", "artifact", "cab", "deploy")
}

func chooseFolderResponsibility(existing string, candidate string) string {
	existing = strings.TrimSpace(existing)
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return existing
	}
	if existing == "" {
		return candidate
	}
	existingScore := folderResponsibilitySpecificity(existing)
	candidateScore := folderResponsibilitySpecificity(candidate)
	if candidateScore > existingScore {
		return candidate
	}
	return existing
}

func mergeFolderResponsibility(inferred string, narrative []string) string {
	inferred = strings.TrimSpace(inferred)
	if inferred != "" && !strings.EqualFold(inferred, "source area") {
		return inferred
	}
	for _, item := range narrative {
		item = strings.TrimSpace(item)
		if item != "" {
			return item
		}
	}
	if inferred != "" {
		return inferred
	}
	return "source area"
}

func folderResponsibilitySpecificity(text string) int {
	lower := strings.ToLower(text)
	score := 0
	if strings.Contains(lower, "solution root") {
		score += 18
	}
	if strings.Contains(lower, "top-level configuration") {
		score += 8
	}
	if strings.Contains(lower, "kernel driver runtime") {
		score += 12
	}
	if strings.Contains(lower, "privileged dispatch") {
		score += 8
	}
	if strings.Contains(lower, "user-mode bootstrap") {
		score += 7
	}
	if strings.Contains(lower, "shared kernel/user-mode contracts") {
		score += 6
	}
	if strings.Contains(lower, "build, release, packaging") {
		score += 6
	}
	for _, word := range []string{"driver", "kernel", "ioctl", "privileged", "dispatch", "protection", "subsystems", "service", "bootstrap", "build", "release", "package", "tooling", "script", "common", "shared", "contracts", "utilities", "policy", "filter", "monitor"} {
		if strings.Contains(lower, word) {
			score += 2
		}
	}
	if len(strings.Fields(text)) > 8 {
		score++
	}
	if containsAny(lower, "string class", "templated string", "linked list") {
		score--
	}
	return score
}

func folderSymbolNames(symbols []SymbolRecord) []string {
	out := []string{}
	for _, symbol := range symbols {
		out = append(out, symbol.Name, symbol.CanonicalName, symbol.Kind)
		out = append(out, symbol.Tags...)
	}
	return out
}

func folderBuildContextTerms(items []BuildContextRecord) []string {
	out := []string{}
	for _, item := range items {
		out = append(out,
			item.ID,
			item.Name,
			item.Kind,
			item.Directory,
			item.Module,
			item.Project,
			item.Target,
			item.Source)
		out = append(out, item.Files...)
	}
	return out
}

func inferModuleResponsibility(record DeveloperModuleRecord) string {
	text := strings.ToLower(strings.Join(append(append([]string{record.Name, record.Kind, record.Root}, record.PublicFiles...), record.Entrypoints...), " "))
	switch {
	case containsAny(text, "testconsole", "manager", "application", ".exe"):
		return "user-mode test harness and driver service manager"
	case containsAny(text, "wdm_driver", "kernelmodedriver", "driver.vcxproj", "tvk.sys"):
		return "kernel driver build target and runtime module"
	case containsAny(text, "driver", "kernel", "ioctl"):
		return "driver or kernel-facing module"
	case containsAny(text, "unreal", "game", "plugin"):
		return "Unreal/game module"
	case containsAny(text, "analysis", "index", "graph"):
		return "analysis and code intelligence module"
	case containsAny(text, "verify", "fuzz", "evidence"):
		return "verification and security workflow module"
	default:
		return "source module"
	}
}

func normalizeAnalysisDocFolder(path string) string {
	path = analysisDocSlashPath(path)
	path = strings.Trim(path, "/")
	if path == "" || path == "." {
		return "."
	}
	return path
}

func analysisDocSlashPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	path = filepath.ToSlash(path)
	return path
}

func analysisDocPathCandidatesFromList(paths []string) []string {
	out := []string{}
	for _, path := range paths {
		out = append(out, analysisDocPathCandidates(path)...)
	}
	return analysisUniqueStrings(out)
}

func analysisDocResolvedPathCandidatesFromList(paths []string, knownPathByBase map[string]string) []string {
	out := []string{}
	for _, path := range paths {
		for _, candidate := range analysisDocPathCandidates(path) {
			resolved, ok := resolveAnalysisDocPathCandidate(candidate, knownPathByBase)
			if ok {
				out = append(out, resolved)
			}
		}
	}
	return analysisUniqueStrings(out)
}

func resolveAnalysisDocPathCandidate(candidate string, knownPathByBase map[string]string) (string, bool) {
	candidate = analysisDocSlashPath(candidate)
	if strings.TrimSpace(candidate) == "" {
		return "", false
	}
	if strings.Contains(candidate, "/") || !analysisDocPathLooksLikeFile(candidate) {
		return candidate, true
	}
	if resolved := knownPathByBase[strings.ToLower(filepath.Base(candidate))]; strings.TrimSpace(resolved) != "" {
		return resolved, true
	}
	return "", false
}

func uniqueAnalysisDocPathByBase(files []ScannedFile) map[string]string {
	out := map[string]string{}
	duplicate := map[string]bool{}
	for _, file := range files {
		path := analysisDocSlashPath(file.Path)
		if path == "" || !analysisDocPathLooksLikeFile(path) {
			continue
		}
		base := strings.ToLower(filepath.Base(path))
		if existing, ok := out[base]; ok && !strings.EqualFold(existing, path) {
			duplicate[base] = true
			continue
		}
		out[base] = path
	}
	for base := range duplicate {
		delete(out, base)
	}
	return out
}

func analysisDocPathCandidates(path string) []string {
	normalized := analysisDocSlashPath(path)
	if normalized == "" {
		return nil
	}
	candidates := []string{}
	fields := strings.FieldsFunc(normalized, func(r rune) bool {
		switch r {
		case ' ', '\t', '\r', '\n', '`', '"', '\'', '(', ')', '[', ']', '{', '}', ',', ';', '|':
			return true
		default:
			return false
		}
	})
	for _, field := range fields {
		candidate := strings.Trim(field, " .:")
		if candidate == "" || candidate == "/" {
			continue
		}
		candidate = strings.TrimSuffix(candidate, ":")
		if idx := strings.LastIndex(candidate, ":"); idx > 1 && allDigits(candidate[idx+1:]) {
			candidate = candidate[:idx]
		}
		if analysisDocPathLooksLikeFile(candidate) || analysisDocLooksLikePathCandidate(candidate) {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) > 0 {
		return analysisUniqueStrings(candidates)
	}
	if strings.ContainsAny(normalized, " ()[]{};|`\"'") {
		return nil
	}
	return []string{normalized}
}

func analysisDocLooksLikePathCandidate(path string) bool {
	path = strings.Trim(analysisDocSlashPath(path), "/")
	if path == "" || !strings.Contains(path, "/") {
		return false
	}
	if analysisDocLooksLikeNaturalSlashPhrase(path) {
		return false
	}
	if analysisDocPathLooksLikeFile(path) {
		return true
	}
	parts := strings.Split(path, "/")
	pathish := false
	for _, part := range parts {
		lower := strings.ToLower(strings.TrimSpace(part))
		if lower == "" {
			return false
		}
		if strings.ContainsAny(lower, ".\\:") {
			pathish = true
			continue
		}
		if containsAny(lower, "src", "source", "include", "inc", "common", "shared", "driver", "kernel", "core", "app", "apps", "cmd", "pkg", "internal", "public", "private", "test", "tests", "doc", "docs", "build", "batch", "config", "content", "plugin", "plugins", "module", "modules", "engine", "tool", "tools", "lib", "libs", "thirdparty", "third_party", "external") {
			pathish = true
		}
	}
	return pathish
}

func analysisDocLooksLikeNaturalSlashPhrase(path string) bool {
	lower := strings.ToLower(strings.Trim(analysisDocSlashPath(path), "/"))
	if lower == "" || !strings.Contains(lower, "/") {
		return false
	}
	if strings.Contains(lower, "-mode") {
		return true
	}
	switch lower {
	case "process/thread", "thread/process", "client/server", "server/client", "request/response", "input/output", "read/write", "producer/consumer", "source/target":
		return true
	default:
		return false
	}
}

func analysisDocSlashPaths(paths []string) []string {
	out := []string{}
	for _, path := range paths {
		normalized := analysisDocSlashPath(path)
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

func analysisDocDir(path string) string {
	if candidates := analysisDocPathCandidates(path); len(candidates) > 0 {
		path = candidates[0]
	} else {
		path = analysisDocSlashPath(path)
	}
	if path == "" || path == "." {
		return "."
	}
	dir := filepath.Dir(path)
	return normalizeAnalysisDocFolder(dir)
}

func analysisDocPathLooksLikeFile(path string) bool {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(analysisDocSlashPath(path))))
	if base == "" || base == "." || base == ".." {
		return false
	}
	if strings.HasPrefix(base, ".") && strings.Count(base, ".") == 1 {
		return false
	}
	ext := filepath.Ext(base)
	if ext == "" {
		return false
	}
	switch ext {
	case ".h", ".hh", ".hpp", ".hxx", ".c", ".cc", ".cpp", ".cxx", ".cs", ".go", ".rs", ".java", ".kt", ".swift", ".m", ".mm", ".py", ".js", ".jsx", ".ts", ".tsx", ".json", ".xml", ".yaml", ".yml", ".toml", ".ini", ".inf", ".vcxproj", ".sln", ".filters", ".props", ".targets", ".bat", ".cmd", ".ps1", ".sh", ".ddf", ".txt", ".md", ".vmp", ".sys", ".dll", ".exe", ".lib", ".a", ".so", ".dylib":
		return true
	default:
		return false
	}
}

func allDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func analysisIsTestFile(path string) bool {
	name := strings.ToLower(filepath.Base(analysisDocSlashPath(path)))
	return strings.Contains(name, "test") || strings.HasSuffix(name, "_spec.go") || strings.HasSuffix(name, ".spec.ts") || strings.HasSuffix(name, ".test.ts")
}

func uniqueDeveloperBuildContexts(items []BuildContextRecord) []BuildContextRecord {
	seen := map[string]struct{}{}
	out := []BuildContextRecord{}
	for _, item := range items {
		key := firstNonBlankDeveloperString(item.ID, item.Name, item.Module, item.Project, item.Directory)
		if strings.TrimSpace(key) == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func formatBuildContextNames(items []BuildContextRecord, limit int) string {
	names := []string{}
	for _, item := range items {
		names = append(names, firstNonBlankDeveloperString(item.Name, item.Module, item.Project, item.ID))
	}
	return formatInlineCodeList(analysisUniqueStrings(names), limit)
}

func formatInlineCodeList(items []string, limit int) string {
	items = analysisUniqueStrings(items)
	items = limitStrings(items, limit)
	if len(items) == 0 {
		return ""
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, "`"+strings.ReplaceAll(analysisDocSlashPath(item), "`", "'")+"`")
	}
	return strings.Join(out, ", ")
}

func formatSymbolNames(items []SymbolRecord, limit int) string {
	names := []string{}
	for _, item := range limitSymbolRecords(items, limit) {
		names = append(names, firstNonBlankDeveloperString(item.CanonicalName, item.Name, item.ID))
	}
	return formatInlineCodeList(names, limit)
}

func analysisMarkdownCell(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "/")
	return value
}

func firstNonBlankDeveloperString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func commonDirectory(files []string) string {
	if len(files) == 0 {
		return ""
	}
	dir := analysisDocDir(files[0])
	for _, file := range files[1:] {
		next := analysisDocDir(file)
		for dir != "." && dir != "" && !strings.HasPrefix(next+"/", strings.TrimRight(dir, "/")+"/") {
			parent := filepath.Dir(dir)
			if parent == dir {
				return "."
			}
			dir = parent
		}
	}
	return normalizeAnalysisDocFolder(dir)
}

func recordMatchesSubsystem(record DeveloperModuleRecord, subsystem KnowledgeSubsystem) bool {
	haystack := strings.ToLower(strings.Join(append(append([]string{record.Name, record.Root}, record.PublicFiles...), record.SourceAnchors...), " "))
	needles := append([]string{canonicalKnowledgeTitle(subsystem)}, subsystem.KeyFiles...)
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(analysisDocSlashPath(needle)))
		if needle != "" && strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func limitDeveloperFolderRecords(items []DeveloperFolderRecord, limit int) []DeveloperFolderRecord {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]DeveloperFolderRecord(nil), items...)
	}
	return append([]DeveloperFolderRecord(nil), items[:limit]...)
}

func limitDeveloperModuleRecords(items []DeveloperModuleRecord, limit int) []DeveloperModuleRecord {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]DeveloperModuleRecord(nil), items...)
	}
	return append([]DeveloperModuleRecord(nil), items[:limit]...)
}
