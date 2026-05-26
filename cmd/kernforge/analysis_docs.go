package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type AnalysisGeneratedDoc struct {
	Name          string               `json:"name"`
	Title         string               `json:"title"`
	Kind          string               `json:"kind"`
	Path          string               `json:"path"`
	GeneratedAt   time.Time            `json:"generated_at"`
	SourceAnchors []string             `json:"source_anchors,omitempty"`
	Confidence    string               `json:"confidence,omitempty"`
	StaleMarkers  []string             `json:"stale_markers,omitempty"`
	ReuseTargets  []string             `json:"reuse_targets,omitempty"`
	QueryIntents  []string             `json:"query_intents,omitempty"`
	Priority      int                  `json:"priority,omitempty"`
	Sections      []AnalysisDocSection `json:"sections,omitempty"`
}

type AnalysisDocSection struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	SourceAnchors []string `json:"source_anchors,omitempty"`
	Confidence    string   `json:"confidence,omitempty"`
	StaleMarkers  []string `json:"stale_markers,omitempty"`
	ReuseTargets  []string `json:"reuse_targets,omitempty"`
	QueryIntents  []string `json:"query_intents,omitempty"`
	Priority      int      `json:"priority,omitempty"`
	EntityRefs    []string `json:"entity_refs,omitempty"`
	GraphRefs     []string `json:"graph_refs,omitempty"`
}

type AnalysisFuzzTargetCatalogEntry struct {
	SymbolID              string   `json:"symbol_id,omitempty"`
	Name                  string   `json:"name"`
	File                  string   `json:"file,omitempty"`
	StartLine             int      `json:"start_line,omitempty"`
	EndLine               int      `json:"end_line,omitempty"`
	Signature             string   `json:"signature,omitempty"`
	InputSurfaceKind      string   `json:"input_surface_kind,omitempty"`
	PriorityScore         int      `json:"priority_score"`
	PriorityReasons       []string `json:"priority_reasons,omitempty"`
	ParameterStrategies   []string `json:"parameter_strategies,omitempty"`
	BuildContext          string   `json:"build_context,omitempty"`
	BuildContextLevel     string   `json:"build_context_level,omitempty"`
	HarnessReadiness      string   `json:"harness_readiness,omitempty"`
	CompileContextWarning string   `json:"compile_context_warning,omitempty"`
	SuggestedCommand      string   `json:"suggested_command,omitempty"`
	SourceAnchor          string   `json:"source_anchor,omitempty"`
	Confidence            string   `json:"confidence,omitempty"`
	CoverageGapScore      int      `json:"coverage_gap_score,omitempty"`
	CoverageFeedback      []string `json:"coverage_feedback,omitempty"`
	SourceCandidateScore  int      `json:"source_candidate_score,omitempty"`
	SourceCandidateIDs    []string `json:"source_candidate_ids,omitempty"`
}

type AnalysisVerificationMatrixEntry struct {
	ChangeArea           string   `json:"change_area"`
	RequiredVerification string   `json:"required_verification"`
	OptionalVerification string   `json:"optional_verification,omitempty"`
	EvidenceHook         string   `json:"evidence_hook,omitempty"`
	SourceAnchors        []string `json:"source_anchors,omitempty"`
	Confidence           string   `json:"confidence,omitempty"`
}

const (
	analysisDocsManifestSchemaVersion     = "analysis_docs_manifest/v1"
	analysisDocsManifestMinReaderVersion  = "analysis_docs_manifest/v1"
	analysisDocsManifestCompatPolicy      = "additive-fields-only"
	analysisDocsManifestLegacySchema      = "legacy"
	analysisDocsManifestCurrentSchemaNote = "Readers must ignore unknown fields and treat missing schema_version as legacy."
)

type AnalysisDocsManifest struct {
	SchemaVersion       string                            `json:"schema_version,omitempty"`
	MinReaderVersion    string                            `json:"min_reader_version,omitempty"`
	CompatibilityPolicy string                            `json:"compatibility_policy,omitempty"`
	SchemaNotes         []string                          `json:"schema_notes,omitempty"`
	RunID               string                            `json:"run_id"`
	Goal                string                            `json:"goal"`
	Mode                string                            `json:"mode,omitempty"`
	GeneratedAt         time.Time                         `json:"generated_at"`
	DocumentCount       int                               `json:"document_count"`
	Documents           []AnalysisGeneratedDoc            `json:"documents"`
	SourceArtifacts     []string                          `json:"source_artifacts,omitempty"`
	ReuseTargets        []string                          `json:"reuse_targets,omitempty"`
	FuzzTargets         []AnalysisFuzzTargetCatalogEntry  `json:"fuzz_targets,omitempty"`
	VerificationMatrix  []AnalysisVerificationMatrixEntry `json:"verification_matrix,omitempty"`
}

func buildAnalysisDocs(run ProjectAnalysisRun) map[string]string {
	docs := map[string]string{
		"FINAL_REPORT.md":             buildAnalysisFinalReportDoc(run),
		"ARCHITECTURE.md":             buildAnalysisArchitectureDoc(run),
		"SECURITY_SURFACE.md":         buildAnalysisSecuritySurfaceDoc(run),
		"API_AND_ENTRYPOINTS.md":      buildAnalysisAPIEntrypointsDoc(run),
		"BUILD_AND_ARTIFACTS.md":      buildAnalysisBuildArtifactsDoc(run),
		"VERIFICATION_MATRIX.md":      buildAnalysisVerificationMatrixDoc(run),
		"FUZZ_TARGETS.md":             buildAnalysisFuzzTargetsDoc(run),
		"OPERATIONS_RUNBOOK.md":       buildAnalysisOperationsRunbookDoc(run),
		"DEVELOPER_OVERVIEW.md":       buildAnalysisDeveloperOverviewDoc(run),
		"FOLDER_MAP.md":               buildAnalysisFolderMapDoc(run),
		"MODULES.md":                  buildAnalysisModulesDoc(run),
		"STRUCTURE_DIAGRAMS.md":       buildAnalysisStructureDiagramsDoc(run),
		"CODE_STRUCTURE_REFERENCE.md": buildAnalysisCodeStructureReferenceDoc(run),
	}
	docs["INDEX.md"] = buildAnalysisDocsIndex(run, docs)
	return docs
}

func analysisGeneratedDocNames() []string {
	return []string{
		"INDEX.md",
		"FINAL_REPORT.md",
		"ARCHITECTURE.md",
		"DEVELOPER_OVERVIEW.md",
		"FOLDER_MAP.md",
		"MODULES.md",
		"STRUCTURE_DIAGRAMS.md",
		"CODE_STRUCTURE_REFERENCE.md",
		"SECURITY_SURFACE.md",
		"API_AND_ENTRYPOINTS.md",
		"BUILD_AND_ARTIFACTS.md",
		"VERIFICATION_MATRIX.md",
		"FUZZ_TARGETS.md",
		"OPERATIONS_RUNBOOK.md",
	}
}

func writeAnalysisDocs(run ProjectAnalysisRun, docsDir string) (AnalysisDocsManifest, error) {
	docs := buildAnalysisDocs(run)
	generatedAt := analysisDocsGeneratedAt(run)
	manifest := AnalysisDocsManifest{
		SchemaVersion:       analysisDocsManifestSchemaVersion,
		MinReaderVersion:    analysisDocsManifestMinReaderVersion,
		CompatibilityPolicy: analysisDocsManifestCompatPolicy,
		SchemaNotes:         []string{analysisDocsManifestCurrentSchemaNote},
		RunID:               run.Summary.RunID,
		Goal:                run.Summary.Goal,
		Mode:                run.Summary.Mode,
		GeneratedAt:         generatedAt,
		SourceArtifacts:     analysisDocsSourceArtifacts(run),
		ReuseTargets:        analysisDocsReuseTargets(),
	}
	manifest.FuzzTargets = analysisFuzzTargetCatalog(run)
	manifest.VerificationMatrix = analysisVerificationMatrixCatalog(run)
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		return manifest, err
	}
	names := make([]string, 0, len(docs))
	for name := range docs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(docsDir, name)
		if err := os.WriteFile(path, []byte(docs[name]), 0o644); err != nil {
			return manifest, err
		}
		manifest.Documents = append(manifest.Documents, AnalysisGeneratedDoc{
			Name:          name,
			Title:         analysisDocTitle(name),
			Kind:          analysisDocKind(name),
			Path:          filepath.ToSlash(name),
			GeneratedAt:   generatedAt,
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
	manifest = normalizeAnalysisDocsManifest(manifest)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return manifest, err
	}
	if err := os.WriteFile(filepath.Join(docsDir, "manifest.json"), data, 0o644); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func normalizeAnalysisDocsManifest(manifest AnalysisDocsManifest) AnalysisDocsManifest {
	if strings.TrimSpace(manifest.SchemaVersion) == "" {
		manifest.SchemaVersion = analysisDocsManifestLegacySchema
	}
	if strings.TrimSpace(manifest.MinReaderVersion) == "" {
		manifest.MinReaderVersion = analysisDocsManifestMinReaderVersion
	}
	if strings.TrimSpace(manifest.CompatibilityPolicy) == "" {
		manifest.CompatibilityPolicy = analysisDocsManifestCompatPolicy
	}
	if len(manifest.SchemaNotes) == 0 {
		manifest.SchemaNotes = []string{analysisDocsManifestCurrentSchemaNote}
	}
	if manifest.DocumentCount == 0 && len(manifest.Documents) > 0 {
		manifest.DocumentCount = len(manifest.Documents)
	}
	if len(manifest.ReuseTargets) == 0 {
		manifest.ReuseTargets = analysisDocsReuseTargets()
	}
	for i := range manifest.Documents {
		doc := &manifest.Documents[i]
		if strings.TrimSpace(doc.Path) == "" {
			doc.Path = filepath.ToSlash(doc.Name)
		}
		if strings.TrimSpace(doc.Title) == "" {
			doc.Title = analysisDocTitle(doc.Name)
		}
		if strings.TrimSpace(doc.Kind) == "" {
			doc.Kind = analysisDocKind(doc.Name)
		}
		if len(doc.ReuseTargets) == 0 {
			doc.ReuseTargets = analysisDocReuseTargets(doc.Name)
		}
		if len(doc.QueryIntents) == 0 {
			doc.QueryIntents = analysisDocQueryIntents(doc.Name)
		}
		if doc.Priority == 0 {
			doc.Priority = analysisDocPriority(doc.Name)
		}
		for j := range doc.Sections {
			section := &doc.Sections[j]
			if len(section.QueryIntents) == 0 {
				section.QueryIntents = analysisDocSectionQueryIntents(doc.Name, section.ID, section.Title)
			}
			if section.Priority == 0 {
				section.Priority = analysisDocSectionPriority(doc.Name, section.ID, section.Title)
			}
		}
	}
	return manifest
}

func decodeAnalysisDocsManifest(data []byte) (AnalysisDocsManifest, error) {
	var manifest AnalysisDocsManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return AnalysisDocsManifest{}, err
	}
	return normalizeAnalysisDocsManifest(manifest), nil
}

func buildAnalysisDocsIndex(run ProjectAnalysisRun, docs map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Project Documentation Index\n\n")
	analysisDocsWriteHeader(&b, run)
	fmt.Fprintf(&b, "\n## Generated Documents\n\n")
	names := make([]string, 0, len(docs))
	for name := range docs {
		if name != "INDEX.md" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "- [%s](./%s): %s\n", analysisDocTitle(name), name, analysisDocPurpose(name))
	}
	fmt.Fprintf(&b, "\n## Source State\n\n")
	fmt.Fprintf(&b, "- Files scanned: %d\n", run.Snapshot.TotalFiles)
	fmt.Fprintf(&b, "- Lines scanned: %d\n", run.Snapshot.TotalLines)
	fmt.Fprintf(&b, "- Shards: %d\n", run.Summary.TotalShards)
	fmt.Fprintf(&b, "- Approved shards: %d\n", run.Summary.ApprovedShards)
	if run.KnowledgePack.AnalysisExecution.ReusedShards > 0 || run.KnowledgePack.AnalysisExecution.MissedShards > 0 {
		fmt.Fprintf(&b, "- Reused shards: %d\n", run.KnowledgePack.AnalysisExecution.ReusedShards)
		fmt.Fprintf(&b, "- Recomputed shards: %d\n", run.KnowledgePack.AnalysisExecution.MissedShards)
	}
	return b.String()
}

func buildAnalysisFinalReportDoc(run ProjectAnalysisRun) string {
	report := strings.TrimSpace(run.FinalDocument)
	if report != "" {
		return report + "\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Final Synthesis Report\n\n")
	analysisDocsWriteHeader(&b, run)
	fmt.Fprintf(&b, "\nNo final synthesis document was captured for this run. Regenerate `/analyze-project` to refresh the assistant-facing final report.\n")
	return b.String()
}

func buildAnalysisArchitectureDoc(run ProjectAnalysisRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Architecture\n\n")
	analysisDocsWriteHeader(&b, run)
	analysisDocsWriteDocMetadata(&b, run, "ARCHITECTURE.md")
	if strings.TrimSpace(run.KnowledgePack.ProjectSummary) != "" {
		fmt.Fprintf(&b, "\n## Executive Summary\n\n%s\n", run.KnowledgePack.ProjectSummary)
	}
	if len(run.KnowledgePack.ArchitectureGroups) > 0 {
		fmt.Fprintf(&b, "\n## Architecture Groups\n\n")
		for _, group := range run.KnowledgePack.ArchitectureGroups {
			fmt.Fprintf(&b, "- %s\n", group)
		}
	}
	if len(run.KnowledgePack.Subsystems) > 0 {
		fmt.Fprintf(&b, "\n## Subsystems\n\n")
		for _, subsystem := range run.KnowledgePack.Subsystems {
			fmt.Fprintf(&b, "### %s\n\n", canonicalKnowledgeTitle(subsystem))
			analysisDocsWriteSubsystemState(&b, subsystem)
			analysisDocsWriteList(&b, "Responsibilities", subsystem.Responsibilities, 6)
			analysisDocsWriteList(&b, "Entry Points", subsystem.EntryPoints, 6)
			analysisDocsWriteList(&b, "Key Files", subsystem.KeyFiles, 8)
			analysisDocsWriteList(&b, "Dependencies", subsystem.Dependencies, 6)
			analysisDocsWriteList(&b, "Facts", subsystem.Facts, 5)
		}
	}
	if len(run.Snapshot.ProjectEdges) > 0 {
		fmt.Fprintf(&b, "\n## Project Edges\n\n")
		analysisDocsWriteSectionMetadata(&b, run, "ARCHITECTURE.md", "architecture.project_edges", analysisGraphSourceAnchors(run))
		fmt.Fprintf(&b, "%s\n", analysisGraphMarkdownTable(analysisGraphEdgeViews(run), 24))
	}
	if graph := analysisGraphTrustBoundaryViews(run); len(graph) > 0 {
		fmt.Fprintf(&b, "\n## Trust Boundary Graph\n\n")
		analysisDocsWriteSectionMetadata(&b, run, "ARCHITECTURE.md", "architecture.trust_boundary_graph", analysisGraphSourceAnchors(run))
		fmt.Fprintf(&b, "%s\n", analysisGraphMermaid(graph, 16))
	}
	if graph := analysisGraphDataFlowViews(run); len(graph) > 0 {
		fmt.Fprintf(&b, "\n## Data Flow Graph\n\n")
		analysisDocsWriteSectionMetadata(&b, run, "ARCHITECTURE.md", "architecture.data_flow_graph", analysisGraphSourceAnchors(run))
		fmt.Fprintf(&b, "%s\n", analysisGraphMermaid(graph, 18))
	}
	return b.String()
}

func buildAnalysisSecuritySurfaceDoc(run ProjectAnalysisRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Security Surface\n\n")
	analysisDocsWriteHeader(&b, run)
	analysisDocsWriteDocMetadata(&b, run, "SECURITY_SURFACE.md")
	surfaces := analysisSecuritySurfaceSymbols(run)
	if len(surfaces) > 0 {
		fmt.Fprintf(&b, "\n## Indexed Security Surfaces\n\n")
		analysisDocsWriteSectionMetadata(&b, run, "SECURITY_SURFACE.md", "security.indexed_surfaces", symbolFiles(surfaces))
		for _, symbol := range surfaces {
			fmt.Fprintf(&b, "- `%s` (%s) in `%s`", firstNonBlankAnalysisString(symbol.CanonicalName, symbol.Name), symbol.Kind, symbol.File)
			if symbol.StartLine > 0 {
				fmt.Fprintf(&b, ":%d", symbol.StartLine)
			}
			if len(symbol.Tags) > 0 {
				fmt.Fprintf(&b, " tags=%s", strings.Join(limitStrings(symbol.Tags, 5), ", "))
			}
			fmt.Fprintf(&b, "\n")
		}
	}
	if len(run.Snapshot.UnrealNetwork) > 0 {
		fmt.Fprintf(&b, "\n## Unreal Network Surface\n\n")
		analysisDocsWriteSectionMetadata(&b, run, "SECURITY_SURFACE.md", "security.unreal_network", unrealNetworkSurfaceFiles(run.Snapshot.UnrealNetwork))
		for _, item := range limitUnrealNetworkSurfaces(run.Snapshot.UnrealNetwork, 20) {
			fmt.Fprintf(&b, "- `%s` in `%s`\n", firstNonBlankAnalysisString(item.TypeName, item.File), item.File)
			analysisDocsWriteInlineList(&b, "Server RPCs", item.ServerRPCs, 6)
			analysisDocsWriteInlineList(&b, "Client RPCs", item.ClientRPCs, 6)
			analysisDocsWriteInlineList(&b, "Multicast RPCs", item.MulticastRPCs, 6)
			analysisDocsWriteInlineList(&b, "Replicated properties", item.ReplicatedProperties, 6)
		}
	}
	if len(run.KnowledgePack.HighRiskFiles) > 0 {
		analysisDocsWriteList(&b, "High Risk Files", run.KnowledgePack.HighRiskFiles, 12)
	}
	if graph := analysisGraphTrustBoundaryViews(run); len(graph) > 0 {
		fmt.Fprintf(&b, "\n## Trust Boundary Graph\n\n")
		analysisDocsWriteSectionMetadata(&b, run, "SECURITY_SURFACE.md", "security.trust_boundary_graph", analysisGraphSourceAnchors(run))
		fmt.Fprintf(&b, "%s\n", analysisGraphMarkdownTable(graph, 18))
	}
	if flows := analysisGraphDataFlowViews(run); len(flows) > 0 {
		fmt.Fprintf(&b, "\n## Attack And Data Flow View\n\n")
		analysisDocsWriteSectionMetadata(&b, run, "SECURITY_SURFACE.md", "security.attack_data_flow", analysisGraphSourceAnchors(run))
		fmt.Fprintf(&b, "%s\n", analysisGraphMarkdownTable(flows, 18))
	}
	analysisDocsWriteRiskSubsystems(&b, run.KnowledgePack.Subsystems)
	return b.String()
}

func buildAnalysisAPIEntrypointsDoc(run ProjectAnalysisRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# API And Entrypoints\n\n")
	analysisDocsWriteHeader(&b, run)
	analysisDocsWriteDocMetadata(&b, run, "API_AND_ENTRYPOINTS.md")
	analysisDocsWriteStartupLens(&b, run)
	analysisDocsWriteList(&b, "Snapshot Entrypoint Files", run.Snapshot.EntrypointFiles, 20)
	if len(run.SemanticIndexV2.Symbols) > 0 {
		fmt.Fprintf(&b, "\n## Indexed Symbols\n\n")
		for _, symbol := range limitSymbolRecords(analysisEntrypointSymbols(run), 40) {
			fmt.Fprintf(&b, "- `%s` (%s) `%s`", firstNonBlankAnalysisString(symbol.CanonicalName, symbol.Name), symbol.Kind, symbol.File)
			if symbol.StartLine > 0 {
				fmt.Fprintf(&b, ":%d", symbol.StartLine)
			}
			if strings.TrimSpace(symbol.Signature) != "" {
				fmt.Fprintf(&b, " - `%s`", symbol.Signature)
			}
			fmt.Fprintf(&b, "\n")
		}
	}
	if len(run.SemanticIndexV2.CallEdges) > 0 {
		fmt.Fprintf(&b, "\n## Representative Call Edges\n\n")
		for _, edge := range limitCallEdges(run.SemanticIndexV2.CallEdges, 30) {
			fmt.Fprintf(&b, "- `%s` -> `%s` (%s)\n", edge.SourceID, edge.TargetID, edge.Type)
		}
	}
	analysisDocsWriteDomainCriticalAnchors(&b, run)
	analysisDocsWriteIOCTLContract(&b, run)
	return b.String()
}

func buildAnalysisBuildArtifactsDoc(run ProjectAnalysisRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Build And Artifacts\n\n")
	analysisDocsWriteHeader(&b, run)
	analysisDocsWriteDocMetadata(&b, run, "BUILD_AND_ARTIFACTS.md")
	analysisDocsWriteList(&b, "Manifest Files", run.Snapshot.ManifestFiles, 20)
	if len(run.Snapshot.SolutionProjects) > 0 {
		fmt.Fprintf(&b, "\n## Solution Projects\n\n")
		for _, project := range run.Snapshot.SolutionProjects {
			fmt.Fprintf(&b, "- `%s`", project.Path)
			if strings.TrimSpace(project.Name) != "" {
				fmt.Fprintf(&b, " (%s)", project.Name)
			}
			fmt.Fprintf(&b, "\n")
		}
	}
	if len(run.Snapshot.BuildContexts) > 0 {
		fmt.Fprintf(&b, "\n## Build Contexts\n\n")
		for _, ctx := range limitBuildContexts(run.Snapshot.BuildContexts, 30) {
			fmt.Fprintf(&b, "- `%s` %s", ctx.ID, ctx.Kind)
			if strings.TrimSpace(ctx.Name) != "" {
				fmt.Fprintf(&b, " name=%s", ctx.Name)
			}
			if strings.TrimSpace(ctx.Module) != "" {
				fmt.Fprintf(&b, " module=%s", ctx.Module)
			}
			if len(ctx.Files) > 0 {
				fmt.Fprintf(&b, " files=%d", len(ctx.Files))
			}
			fmt.Fprintf(&b, "\n")
		}
	}
	if len(run.Snapshot.CompileCommands) > 0 {
		fmt.Fprintf(&b, "\n## Compile Command Coverage\n\n")
		for _, cmd := range limitCompileCommands(run.Snapshot.CompileCommands, 20) {
			fmt.Fprintf(&b, "- `%s` compiler=%s source=%s\n", cmd.File, firstNonBlankAnalysisString(cmd.Compiler, "unknown"), firstNonBlankAnalysisString(cmd.Source, "compile_commands"))
		}
	}
	return b.String()
}

func buildAnalysisVerificationMatrixDoc(run ProjectAnalysisRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Verification Matrix\n\n")
	analysisDocsWriteHeader(&b, run)
	analysisDocsWriteDocMetadata(&b, run, "VERIFICATION_MATRIX.md")
	analysisDocsWriteSectionMetadata(&b, run, "VERIFICATION_MATRIX.md", "verification.matrix", analysisDocSourceAnchors(run, "VERIFICATION_MATRIX.md"))
	fmt.Fprintf(&b, "\n| Change Area | Required Verification | Optional Verification | Evidence Hook |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- |\n")
	rows := analysisVerificationRows(run)
	for _, row := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", row[0], row[1], row[2], row[3])
	}
	if len(run.KnowledgePack.AnalysisExecution.TopChangeClasses) > 0 {
		analysisDocsWriteList(&b, "Recent Change Classes", run.KnowledgePack.AnalysisExecution.TopChangeClasses, 12)
	}
	return b.String()
}

func buildAnalysisFuzzTargetsDoc(run ProjectAnalysisRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Fuzz Targets\n\n")
	analysisDocsWriteHeader(&b, run)
	analysisDocsWriteDocMetadata(&b, run, "FUZZ_TARGETS.md")
	targets := analysisFuzzTargetCatalog(run)
	if len(targets) == 0 {
		fmt.Fprintf(&b, "\nNo high-confidence fuzz target symbols were indexed. Start with `/fuzz-func @<path>` on a parser, IOCTL, telemetry decoder, or buffer-processing file from `SECURITY_SURFACE.md`.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\n## Target Catalog\n\n")
	analysisDocsWriteSectionMetadata(&b, run, "FUZZ_TARGETS.md", "fuzz.target_catalog", symbolFiles(analysisFuzzTargetSymbols(run)))
	fmt.Fprintf(&b, "| Priority | Target | Input Surface | Coverage Feedback / Source Signals | Build Context | Harness | Suggested Command |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, target := range limitAnalysisFuzzTargetCatalog(targets, 20) {
		name := firstNonBlankAnalysisString(target.Name, target.SymbolID)
		location := target.File
		if target.StartLine > 0 {
			location = fmt.Sprintf("%s:%d", target.File, target.StartLine)
		}
		if strings.TrimSpace(location) != "" {
			name = fmt.Sprintf("%s<br>`%s`", name, location)
		}
		build := target.BuildContextLevel
		if strings.TrimSpace(target.BuildContext) != "" {
			build = target.BuildContext + " / " + firstNonBlankAnalysisString(target.BuildContextLevel, "unknown")
		}
		coverage := "none"
		if target.CoverageGapScore > 0 {
			coverage = fmt.Sprintf("+%d %s", target.CoverageGapScore, strings.Join(limitStrings(target.CoverageFeedback, 2), ", "))
		}
		if target.SourceCandidateScore > 0 {
			sourceSignals := fmt.Sprintf("source +%d %s", target.SourceCandidateScore, strings.Join(limitStrings(target.SourceCandidateIDs, 2), ", "))
			if coverage == "none" {
				coverage = sourceSignals
			} else {
				coverage += "; " + sourceSignals
			}
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s | `%s` |\n",
			target.PriorityScore,
			strings.ReplaceAll(name, "|", "/"),
			strings.ReplaceAll(firstNonBlankAnalysisString(target.InputSurfaceKind, "unknown"), "|", "/"),
			strings.ReplaceAll(firstNonBlankAnalysisString(coverage, "none"), "|", "/"),
			strings.ReplaceAll(firstNonBlankAnalysisString(build, "unknown"), "|", "/"),
			strings.ReplaceAll(firstNonBlankAnalysisString(target.HarnessReadiness, "unknown"), "|", "/"),
			strings.ReplaceAll(target.SuggestedCommand, "|", "/"))
	}
	fmt.Fprintf(&b, "\n## Candidate Targets\n\n")
	analysisDocsWriteSectionMetadata(&b, run, "FUZZ_TARGETS.md", "fuzz.candidate_targets", symbolFiles(analysisFuzzTargetSymbols(run)))
	for _, target := range limitAnalysisFuzzTargetCatalog(targets, 40) {
		fmt.Fprintf(&b, "### %s\n\n", firstNonBlankAnalysisString(target.Name, target.SymbolID))
		fmt.Fprintf(&b, "- Priority score: %d\n", target.PriorityScore)
		if len(target.PriorityReasons) > 0 {
			fmt.Fprintf(&b, "- Priority reasons: %s\n", strings.Join(limitStrings(target.PriorityReasons, 6), ", "))
		}
		if len(target.CoverageFeedback) > 0 {
			fmt.Fprintf(&b, "- Coverage feedback: +%d; %s\n", target.CoverageGapScore, strings.Join(limitStrings(target.CoverageFeedback, 4), ", "))
		}
		if len(target.SourceCandidateIDs) > 0 {
			fmt.Fprintf(&b, "- Source candidate feedback: +%d; %s\n", target.SourceCandidateScore, strings.Join(limitStrings(target.SourceCandidateIDs, 4), ", "))
		}
		if strings.TrimSpace(target.File) != "" {
			fmt.Fprintf(&b, "- File: `%s`\n", target.File)
		}
		if strings.TrimSpace(target.SourceAnchor) != "" {
			fmt.Fprintf(&b, "- Source anchor: `%s`\n", target.SourceAnchor)
		}
		if strings.TrimSpace(target.Signature) != "" {
			fmt.Fprintf(&b, "- Signature: `%s`\n", target.Signature)
		}
		fmt.Fprintf(&b, "- Input surface: %s\n", firstNonBlankAnalysisString(target.InputSurfaceKind, "unknown"))
		if len(target.ParameterStrategies) > 0 {
			fmt.Fprintf(&b, "- Parameter strategy: %s\n", strings.Join(limitStrings(target.ParameterStrategies, 8), ", "))
		}
		if strings.TrimSpace(target.BuildContext) != "" || strings.TrimSpace(target.BuildContextLevel) != "" {
			fmt.Fprintf(&b, "- Build context: `%s` (%s)\n", firstNonBlankAnalysisString(target.BuildContext, "unknown"), firstNonBlankAnalysisString(target.BuildContextLevel, "unknown"))
		}
		if strings.TrimSpace(target.CompileContextWarning) != "" {
			fmt.Fprintf(&b, "- Compile context warning: %s\n", target.CompileContextWarning)
		}
		fmt.Fprintf(&b, "- Harness readiness: %s\n", firstNonBlankAnalysisString(target.HarnessReadiness, "unknown"))
		if strings.TrimSpace(target.SuggestedCommand) != "" {
			fmt.Fprintf(&b, "- Suggested command: `%s`\n", target.SuggestedCommand)
		}
	}
	return b.String()
}

func buildAnalysisOperationsRunbookDoc(run ProjectAnalysisRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Operations Runbook\n\n")
	analysisDocsWriteHeader(&b, run)
	analysisDocsWriteDocMetadata(&b, run, "OPERATIONS_RUNBOOK.md")
	fmt.Fprintf(&b, "\n## Recommended Loop\n\n")
	fmt.Fprintf(&b, "1. Review `ARCHITECTURE.md` for subsystem ownership and entrypoints.\n")
	fmt.Fprintf(&b, "2. Review `SECURITY_SURFACE.md` before editing driver, telemetry, memory, or Unreal network paths.\n")
	fmt.Fprintf(&b, "3. Use `FUZZ_TARGETS.md` to start `/fuzz-func` on input-facing functions.\n")
	fmt.Fprintf(&b, "4. Use `VERIFICATION_MATRIX.md` to pick required checks before commit, push, or PR.\n")
	fmt.Fprintf(&b, "5. Record verification and fuzz findings in evidence so hooks can enforce the next gate.\n")
	fmt.Fprintf(&b, "\n## Reuse Hooks\n\n")
	fmt.Fprintf(&b, "- Evidence: each analysis run records `.kernforge/analysis/latest/docs_manifest.json` as `kind=analysis_docs` with source anchors, confidence, and reuse targets.\n")
	fmt.Fprintf(&b, "- Memory: the latest docs index, manifest, dashboard, and generated docs are promoted as a project knowledge-base memory record.\n")
	fmt.Fprintf(&b, "- Verification planner: `VERIFICATION_MATRIX.md` is consumed before `/verify` planning so documented high-risk surfaces become concrete checks.\n")
	fmt.Fprintf(&b, "- Fuzz target discovery: `FUZZ_TARGETS.md` is consumed before `/fuzz-func` ranking so documented parser, IOCTL, telemetry, and protocol surfaces rank first.\n")
	fmt.Fprintf(&b, "\n## Operational State Checklist\n\n")
	fmt.Fprintf(&b, "- Hooks: run `/hooks status` before enforcing generated guidance; stale docs should refresh before strict gates.\n")
	fmt.Fprintf(&b, "- Task ownership: route architecture/documentation work to project analysis, security-surface review to verification planning, and target selection to fuzz discovery.\n")
	fmt.Fprintf(&b, "- Worktree: checkpoint or review dirty changes before large documentation refreshes so stale markers can be interpreted against the intended tree state.\n")
	fmt.Fprintf(&b, "- Evidence handoff: after fuzzing or verification, record pass/fail artifacts so the next analysis run can distinguish confirmed facts from stale assumptions.\n")
	if len(run.KnowledgePack.Unknowns) > 0 {
		analysisDocsWriteList(&b, "Open Unknowns", run.KnowledgePack.Unknowns, 12)
	}
	return b.String()
}

func analysisDocsWriteHeader(b *strings.Builder, run ProjectAnalysisRun) {
	fmt.Fprintf(b, "- Run ID: `%s`\n", run.Summary.RunID)
	fmt.Fprintf(b, "- Goal: %s\n", run.Summary.Goal)
	if strings.TrimSpace(run.Summary.Mode) != "" {
		fmt.Fprintf(b, "- Mode: `%s`\n", run.Summary.Mode)
	}
	if strings.TrimSpace(run.KnowledgePack.Root) != "" {
		fmt.Fprintf(b, "- Workspace: `%s`\n", run.KnowledgePack.Root)
	}
	fmt.Fprintf(b, "- Confidence: %s\n", analysisRunConfidence(run))
	if markers := analysisRunStaleMarkers(run); len(markers) > 0 {
		fmt.Fprintf(b, "- Stale/invalidation markers: %s\n", strings.Join(limitStrings(markers, 6), "; "))
	}
	fmt.Fprintf(b, "- Generated from analysis artifacts: knowledge pack, snapshot, structural index, worker reports\n")
}

func analysisDocsWriteSubsystemState(b *strings.Builder, subsystem KnowledgeSubsystem) {
	if len(subsystem.CacheStatuses) > 0 {
		fmt.Fprintf(b, "- Cache status: %s\n", strings.Join(limitStrings(subsystem.CacheStatuses, 4), "; "))
	}
	if len(subsystem.InvalidationReasons) > 0 {
		fmt.Fprintf(b, "- Stale marker: %s\n", strings.Join(limitStrings(subsystem.InvalidationReasons, 3), "; "))
	}
	if len(subsystem.EvidenceFiles) > 0 {
		fmt.Fprintf(b, "- Source anchors: %s\n", strings.Join(limitStrings(subsystem.EvidenceFiles, 5), ", "))
	}
	fmt.Fprintf(b, "\n")
}

func analysisDocsWriteDocMetadata(b *strings.Builder, run ProjectAnalysisRun, name string) {
	fmt.Fprintf(b, "\n## Document Metadata\n\n")
	fmt.Fprintf(b, "- Confidence: %s\n", analysisDocConfidence(run, name))
	if anchors := analysisDocSourceAnchors(run, name); len(anchors) > 0 {
		fmt.Fprintf(b, "- Source anchors: %s\n", strings.Join(limitStrings(anchors, 8), ", "))
	}
	if markers := analysisDocStaleMarkers(run, name); len(markers) > 0 {
		fmt.Fprintf(b, "- Stale/invalidation markers: %s\n", strings.Join(limitStrings(markers, 6), "; "))
	}
	if targets := analysisDocReuseTargets(name); len(targets) > 0 {
		fmt.Fprintf(b, "- Reuse targets: %s\n", strings.Join(targets, ", "))
	}
}

func analysisDocsWriteSectionMetadata(b *strings.Builder, run ProjectAnalysisRun, docName string, sectionID string, anchors []string) {
	anchors = analysisUniqueStrings(anchors)
	if len(anchors) == 0 {
		anchors = analysisDocSourceAnchors(run, docName)
	}
	fmt.Fprintf(b, "\n_Section metadata: confidence=%s", firstNonBlankAnalysisString(analysisDocConfidence(run, docName), "unknown"))
	if len(anchors) > 0 {
		fmt.Fprintf(b, "; source anchors=%s", strings.Join(limitStrings(anchors, 6), ", "))
	}
	if markers := analysisDocSectionStaleMarkers(run, docName, sectionID, ""); len(markers) > 0 {
		fmt.Fprintf(b, "; stale/invalidation=%s", strings.Join(limitStrings(markers, 4), ", "))
	}
	if strings.TrimSpace(sectionID) != "" {
		fmt.Fprintf(b, "; section_id=%s", strings.TrimSpace(sectionID))
	}
	fmt.Fprintf(b, "._\n")
}

func analysisDocsWriteList(b *strings.Builder, title string, items []string, limit int) {
	items = limitStrings(analysisUniqueStrings(items), limit)
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## %s\n\n", title)
	for _, item := range items {
		fmt.Fprintf(b, "- %s\n", item)
	}
}

func analysisDocsWriteInlineList(b *strings.Builder, title string, items []string, limit int) {
	items = limitStrings(analysisUniqueStrings(items), limit)
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "  - %s: %s\n", title, strings.Join(items, ", "))
}

func analysisDocsWriteRiskSubsystems(b *strings.Builder, subsystems []KnowledgeSubsystem) {
	wrote := false
	for _, subsystem := range subsystems {
		if len(subsystem.Risks) == 0 {
			continue
		}
		if !wrote {
			fmt.Fprintf(b, "\n## Risk Notes By Subsystem\n\n")
			wrote = true
		}
		fmt.Fprintf(b, "### %s\n\n", canonicalKnowledgeTitle(subsystem))
		for _, risk := range limitStrings(subsystem.Risks, 5) {
			fmt.Fprintf(b, "- %s\n", risk)
		}
	}
}

func analysisSecuritySurfaceSymbols(run ProjectAnalysisRun) []SymbolRecord {
	out := []SymbolRecord{}
	for _, symbol := range run.SemanticIndexV2.Symbols {
		corpus := strings.ToLower(strings.Join(append(append([]string{symbol.Name, symbol.CanonicalName, symbol.Kind, symbol.File, symbol.Signature}, symbol.Tags...), mapValues(symbol.Attributes)...), " "))
		if containsAny(corpus, "ioctl", "dispatch", "rpc", "handle", "probe", "copy", "memory", "etw", "telemetry", "socket", "packet", "parser", "parse", "deserialize", "validate") {
			out = append(out, symbol)
		}
	}
	sortSymbolRecords(out)
	return out
}

func analysisEntrypointSymbols(run ProjectAnalysisRun) []SymbolRecord {
	out := []SymbolRecord{}
	for _, symbol := range run.SemanticIndexV2.Symbols {
		corpus := strings.ToLower(strings.Join([]string{symbol.Name, symbol.CanonicalName, symbol.Kind, symbol.Signature}, " "))
		if symbol.Kind == "function" || symbol.Kind == "method" || containsAny(corpus, "main", "entry", "startup", "init", "dispatch", "handler", "command") {
			out = append(out, symbol)
		}
	}
	sortSymbolRecords(out)
	return out
}

func analysisFuzzTargetSymbols(run ProjectAnalysisRun) []SymbolRecord {
	out := []SymbolRecord{}
	for _, symbol := range run.SemanticIndexV2.Symbols {
		corpus := strings.ToLower(strings.Join(append([]string{symbol.Name, symbol.CanonicalName, symbol.Kind, symbol.File, symbol.Signature}, symbol.Tags...), " "))
		if containsAny(corpus, "ioctl", "dispatch", "parse", "parser", "deserialize", "decode", "validate", "probe", "copy", "buffer", "packet", "message", "telemetry", "rpc") {
			out = append(out, symbol)
		}
	}
	sortSymbolRecords(out)
	return out
}

func analysisFuzzTargetCatalog(run ProjectAnalysisRun) []AnalysisFuzzTargetCatalogEntry {
	symbols := analysisFuzzTargetSymbols(run)
	if len(symbols) == 0 {
		return nil
	}
	overlayCounts := map[string]int{}
	for _, edge := range run.SemanticIndexV2.OverlayEdges {
		overlayCounts[strings.TrimSpace(edge.SourceID)]++
	}
	coverageFeedback := analysisFuzzCoverageFeedback(run)
	sourceCandidateFeedback := analysisFuzzSourceCandidateFeedback(run)
	out := make([]AnalysisFuzzTargetCatalogEntry, 0, len(symbols))
	for _, symbol := range symbols {
		params := buildFunctionFuzzParameterStrategies(symbol.Signature)
		entry := AnalysisFuzzTargetCatalogEntry{
			SymbolID:            strings.TrimSpace(symbol.ID),
			Name:                firstNonBlankAnalysisString(functionFuzzDisplayName(symbol), firstNonBlankAnalysisString(symbol.CanonicalName, symbol.Name)),
			File:                filepath.ToSlash(strings.TrimSpace(symbol.File)),
			StartLine:           symbol.StartLine,
			EndLine:             symbol.EndLine,
			Signature:           functionFuzzSanitizeSignature(symbol.Signature),
			InputSurfaceKind:    analysisFuzzInputSurfaceKind(symbol),
			ParameterStrategies: analysisFuzzParameterStrategies(params),
			BuildContext:        strings.TrimSpace(symbol.BuildContextID),
			BuildContextLevel:   analysisFuzzBuildContextLevel(run, symbol),
			HarnessReadiness:    analysisFuzzHarnessReadiness(symbol, params),
			SuggestedCommand:    functionFuzzSuggestedCommandForSymbol(symbol),
			SourceAnchor:        analysisFuzzSourceAnchor(symbol),
			Confidence:          analysisDocConfidence(run, "FUZZ_TARGETS.md"),
		}
		entry.PriorityScore, entry.PriorityReasons = analysisFuzzTargetPriority(symbol, params, overlayCounts[strings.TrimSpace(symbol.ID)])
		if feedback := coverageFeedback.match(symbol, entry); feedback.Score > 0 {
			entry.CoverageGapScore = feedback.Score
			entry.CoverageFeedback = feedback.Reasons
			entry.PriorityScore += feedback.Score
			if entry.PriorityScore > 100 {
				entry.PriorityScore = 100
			}
			entry.PriorityReasons = analysisUniqueStrings(append(entry.PriorityReasons, feedback.Reasons...))
		}
		if feedback := sourceCandidateFeedback.match(symbol, entry); feedback.Score > 0 {
			entry.SourceCandidateScore = feedback.Score
			entry.SourceCandidateIDs = feedback.Matches
			entry.PriorityScore += feedback.Score
			if entry.PriorityScore > 100 {
				entry.PriorityScore = 100
			}
			entry.PriorityReasons = analysisUniqueStrings(append(entry.PriorityReasons, feedback.Reasons...))
		}
		entry.CompileContextWarning = analysisFuzzCompileContextWarning(entry)
		out = append(out, entry)
	}
	sort.Slice(out, func(i int, j int) bool {
		if out[i].PriorityScore != out[j].PriorityScore {
			return out[i].PriorityScore > out[j].PriorityScore
		}
		left := out[i].Name + "|" + out[i].File + "|" + out[i].SymbolID
		right := out[j].Name + "|" + out[j].File + "|" + out[j].SymbolID
		return left < right
	})
	return out
}

func analysisFuzzTargetPriority(symbol SymbolRecord, params []FunctionFuzzParamStrategy, overlayCount int) (int, []string) {
	score := 20
	reasons := []string{"indexed fuzz-facing symbol"}
	if functionFuzzSymbolLooksInputFacing(symbol, params) {
		score += 28
		reasons = append(reasons, "input-facing name or parameter contract")
	}
	if functionFuzzHarnessReady(symbol, params) {
		score += 18
		reasons = append(reasons, "direct harness candidate")
	}
	if functionFuzzHasDirectInputParams(params) {
		score += 12
		reasons = append(reasons, "direct input parameters")
	}
	if functionFuzzHasLengthBufferRelation(params) {
		score += 12
		reasons = append(reasons, "buffer and length relation")
	}
	if overlayCount > 0 {
		score += functionFuzzMin(overlayCount, 3) * 10
		reasons = append(reasons, "security overlay reachable")
	}
	bonus := functionFuzzSuggestedTargetSignalBonus(symbol, params)
	if bonus > 0 {
		score += bonus
		reasons = append(reasons, "parser, dispatch, validation, or memory signal")
	}
	penalty := functionFuzzSuggestedTargetPenalty(symbol, params)
	if penalty > 0 {
		score -= penalty
		reasons = append(reasons, "helper, generated, or low-specificity penalty")
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score, analysisUniqueStrings(reasons)
}

type analysisFuzzCoverageFeedbackSet struct {
	ByKey map[string]analysisFuzzCoverageFeedbackItem
}

type analysisFuzzCoverageFeedbackItem struct {
	Score   int
	Reasons []string
}

func analysisFuzzCoverageFeedback(run ProjectAnalysisRun) analysisFuzzCoverageFeedbackSet {
	root := firstNonBlankAnalysisString(run.Snapshot.Root, run.KnowledgePack.Root)
	campaigns := loadWorkspaceFuzzCampaignManifests(root, 8)
	out := analysisFuzzCoverageFeedbackSet{ByKey: map[string]analysisFuzzCoverageFeedbackItem{}}
	for _, campaign := range campaigns {
		for _, gap := range campaign.CoverageGaps {
			score := gap.PriorityBoost
			if score <= 0 {
				score = 10
			}
			reason := firstNonBlankAnalysisString(gap.Reason, "campaign coverage gap")
			reason = fmt.Sprintf("coverage gap from %s: %s", firstNonBlankAnalysisString(campaign.ID, gap.CampaignID), reason)
			for _, key := range fuzzCampaignCoverageTargetKeys(gap.Target, gap.TargetFile, gap.SourceAnchor, "") {
				out.add(key, score, reason)
			}
		}
	}
	return out
}

func (set analysisFuzzCoverageFeedbackSet) add(key string, score int, reason string) {
	key = strings.ToLower(strings.TrimSpace(filepath.ToSlash(key)))
	if key == "" {
		return
	}
	if set.ByKey == nil {
		set.ByKey = map[string]analysisFuzzCoverageFeedbackItem{}
	}
	item := set.ByKey[key]
	item.Score += score
	if item.Score > 30 {
		item.Score = 30
	}
	item.Reasons = analysisUniqueStrings(append(item.Reasons, reason))
	set.ByKey[key] = item
}

func (set analysisFuzzCoverageFeedbackSet) match(symbol SymbolRecord, entry AnalysisFuzzTargetCatalogEntry) analysisFuzzCoverageFeedbackItem {
	if len(set.ByKey) == 0 {
		return analysisFuzzCoverageFeedbackItem{}
	}
	out := analysisFuzzCoverageFeedbackItem{}
	keys := fuzzCampaignCoverageTargetKeys(
		firstNonBlankAnalysisString(entry.Name, firstNonBlankAnalysisString(symbol.Name, symbol.CanonicalName)),
		firstNonBlankAnalysisString(entry.File, symbol.File),
		firstNonBlankAnalysisString(entry.SourceAnchor, analysisFuzzSourceAnchor(symbol)),
		firstNonBlankAnalysisString(entry.SymbolID, symbol.ID),
	)
	for _, key := range keys {
		item, ok := set.ByKey[key]
		if !ok {
			continue
		}
		out.Score += item.Score
		out.Reasons = analysisUniqueStrings(append(out.Reasons, item.Reasons...))
	}
	if out.Score > 30 {
		out.Score = 30
	}
	return out
}

func analysisFuzzInputSurfaceKind(symbol SymbolRecord) string {
	corpus := functionFuzzSymbolCorpus(symbol)
	switch {
	case containsAny(corpus, "ioctl", "devicecontrol", "irp"):
		return "ioctl"
	case containsAny(corpus, "rpc", "remote", "server", "client"):
		return "rpc"
	case containsAny(corpus, "parse", "parser", "deserialize", "decode", "packet", "message"):
		return "parser"
	case containsAny(corpus, "probe", "copy", "buffer", "memory", "memcpy", "read", "write"):
		return "memory"
	case containsAny(corpus, "telemetry", "etw", "event"):
		return "telemetry"
	case containsAny(corpus, "validate", "verify", "check", "guard"):
		return "validation"
	default:
		return "input"
	}
}

func analysisFuzzParameterStrategies(params []FunctionFuzzParamStrategy) []string {
	out := []string{}
	for _, param := range params {
		name := firstNonBlankAnalysisString(param.Name, fmt.Sprintf("arg%d", param.Index))
		item := fmt.Sprintf("%s:%s", name, firstNonBlankAnalysisString(param.Class, "unknown"))
		if strings.TrimSpace(param.Relation) != "" {
			item += "(" + strings.TrimSpace(param.Relation) + ")"
		}
		out = append(out, item)
	}
	return analysisUniqueStrings(out)
}

func analysisFuzzBuildContextLevel(run ProjectAnalysisRun, symbol SymbolRecord) string {
	if strings.TrimSpace(symbol.BuildContextID) != "" {
		return "symbol_build_context"
	}
	file := filepath.ToSlash(strings.TrimSpace(symbol.File))
	if file == "" {
		return "missing"
	}
	for _, build := range run.SemanticIndexV2.BuildContexts {
		for _, item := range build.Files {
			if strings.EqualFold(filepath.ToSlash(strings.TrimSpace(item)), file) {
				return "indexed_build_context"
			}
		}
	}
	for _, command := range run.Snapshot.CompileCommands {
		if strings.EqualFold(filepath.ToSlash(strings.TrimSpace(command.File)), file) {
			return "compile_commands"
		}
	}
	return "source_only"
}

func analysisFuzzHarnessReadiness(symbol SymbolRecord, params []FunctionFuzzParamStrategy) string {
	if functionFuzzHarnessReady(symbol, params) {
		return "ready"
	}
	if functionFuzzSanitizeSignature(symbol.Signature) == "" {
		return "needs_signature"
	}
	return "needs_binding"
}

func analysisFuzzCompileContextWarning(entry AnalysisFuzzTargetCatalogEntry) string {
	switch strings.TrimSpace(entry.BuildContextLevel) {
	case "", "missing":
		return "no source or build context was available for this target"
	case "source_only":
		return "source-only candidate; native harness may need manual compile context"
	default:
		return ""
	}
}

func analysisFuzzSourceAnchor(symbol SymbolRecord) string {
	file := filepath.ToSlash(strings.TrimSpace(symbol.File))
	if file == "" {
		return ""
	}
	if symbol.StartLine > 0 {
		return fmt.Sprintf("%s:%d", file, symbol.StartLine)
	}
	return file
}

func limitAnalysisFuzzTargetCatalog(items []AnalysisFuzzTargetCatalogEntry, limit int) []AnalysisFuzzTargetCatalogEntry {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]AnalysisFuzzTargetCatalogEntry(nil), items...)
	}
	return append([]AnalysisFuzzTargetCatalogEntry(nil), items[:limit]...)
}

func analysisVerificationMatrixCatalog(run ProjectAnalysisRun) []AnalysisVerificationMatrixEntry {
	rows := analysisVerificationRows(run)
	out := make([]AnalysisVerificationMatrixEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, AnalysisVerificationMatrixEntry{
			ChangeArea:           row[0],
			RequiredVerification: row[1],
			OptionalVerification: row[2],
			EvidenceHook:         row[3],
			SourceAnchors:        analysisDocSourceAnchors(run, "VERIFICATION_MATRIX.md"),
			Confidence:           analysisDocConfidence(run, "VERIFICATION_MATRIX.md"),
		})
	}
	return out
}

func analysisVerificationRows(run ProjectAnalysisRun) [][4]string {
	rows := [][4]string{
		{"General source change", "`go test ./...` or project test suite", "targeted regression tests", "verification history"},
	}
	if len(run.Snapshot.BuildContexts) > 0 || len(run.Snapshot.CompileCommands) > 0 {
		rows = append(rows, [4]string{"C/C++ build path", "compile target or selected translation unit", "sanitizer or static analysis pass", "build artifacts"})
	}
	if hasSecuritySignals(run) {
		rows = append(rows, [4]string{"Security surface", "security review plus targeted verification", "`/fuzz-func` on input-facing functions", "evidence store finding"})
	}
	if len(run.Snapshot.UnrealProjects) > 0 || len(run.Snapshot.UnrealModules) > 0 {
		rows = append(rows, [4]string{"Unreal module or RPC", "UBT build for affected target", "replication/integrity regression", "Unreal semantic graph"})
	}
	if containsAny(strings.ToLower(strings.Join(run.KnowledgePack.HighRiskFiles, " ")), "driver", ".sys", "ioctl") {
		rows = append(rows, [4]string{"Driver or IOCTL", "driver build and symbol/signing readiness", "Driver Verifier smoke checklist", "driver evidence bundle"})
	}
	return rows
}

func hasSecuritySignals(run ProjectAnalysisRun) bool {
	if len(analysisSecuritySurfaceSymbols(run)) > 0 {
		return true
	}
	return len(run.KnowledgePack.HighRiskFiles) > 0 || len(run.Snapshot.UnrealNetwork) > 0
}

func analysisDocsSourceArtifacts(run ProjectAnalysisRun) []string {
	items := []string{"final_document", "snapshot", "knowledge_pack"}
	if hasSemanticIndexV2Data(run.SemanticIndexV2) {
		items = append(items, "structural_index_v2")
	}
	if len(run.UnrealGraph.Nodes) > 0 || len(run.UnrealGraph.Edges) > 0 {
		items = append(items, "unreal_graph")
	}
	if len(run.VectorCorpus.Documents) > 0 {
		items = append(items, "vector_corpus")
	}
	return items
}

func analysisDocsGeneratedAt(run ProjectAnalysisRun) time.Time {
	if !run.Summary.CompletedAt.IsZero() {
		return run.Summary.CompletedAt.UTC()
	}
	if !run.Summary.StartedAt.IsZero() {
		return run.Summary.StartedAt.UTC()
	}
	if !run.KnowledgePack.GeneratedAt.IsZero() {
		return run.KnowledgePack.GeneratedAt.UTC()
	}
	if !run.Snapshot.GeneratedAt.IsZero() {
		return run.Snapshot.GeneratedAt.UTC()
	}
	return time.Time{}
}

func analysisRunConfidence(run ProjectAnalysisRun) string {
	switch {
	case run.Summary.ApprovedShards == 0 && run.Summary.TotalShards > 0:
		return "low"
	case run.Summary.ReviewFailures > 0:
		return "medium"
	case strings.EqualFold(run.Summary.Status, "draft"):
		return "low"
	default:
		return "high"
	}
}

func analysisRunStaleMarkers(run ProjectAnalysisRun) []string {
	items := []string{}
	items = append(items, run.KnowledgePack.AnalysisExecution.InvalidationReasons...)
	items = append(items, run.KnowledgePack.AnalysisExecution.SemanticInvalidationReasons...)
	items = append(items, run.KnowledgePack.AnalysisExecution.TopChangeClasses...)
	for _, subsystem := range run.KnowledgePack.Subsystems {
		items = append(items, subsystem.InvalidationReasons...)
	}
	return analysisUniqueStrings(items)
}

func analysisDocSourceAnchors(run ProjectAnalysisRun, name string) []string {
	switch name {
	case "FINAL_REPORT.md":
		return analysisUniqueStrings(append(run.KnowledgePack.TopImportantFiles, run.Snapshot.EntrypointFiles...))
	case "ARCHITECTURE.md":
		return analysisUniqueStrings(append(run.KnowledgePack.TopImportantFiles, subsystemFiles(run.KnowledgePack.Subsystems)...))
	case "DEVELOPER_OVERVIEW.md":
		return analysisDeveloperDocSourceAnchors(run, "DEVELOPER_OVERVIEW.md")
	case "FOLDER_MAP.md":
		return analysisDeveloperDocSourceAnchors(run, "FOLDER_MAP.md")
	case "MODULES.md":
		return analysisDeveloperDocSourceAnchors(run, "MODULES.md")
	case "STRUCTURE_DIAGRAMS.md":
		return analysisDeveloperDocSourceAnchors(run, "STRUCTURE_DIAGRAMS.md")
	case "CODE_STRUCTURE_REFERENCE.md":
		return analysisDeveloperDocSourceAnchors(run, "CODE_STRUCTURE_REFERENCE.md")
	case "SECURITY_SURFACE.md":
		return analysisUniqueStrings(append(symbolFiles(analysisSecuritySurfaceSymbols(run)), run.KnowledgePack.HighRiskFiles...))
	case "API_AND_ENTRYPOINTS.md":
		return analysisUniqueStrings(append(run.Snapshot.EntrypointFiles, symbolFiles(analysisEntrypointSymbols(run))...))
	case "BUILD_AND_ARTIFACTS.md":
		return analysisUniqueStrings(append(run.Snapshot.ManifestFiles, compileCommandFiles(run.Snapshot.CompileCommands)...))
	case "VERIFICATION_MATRIX.md":
		return analysisUniqueStrings(append(run.KnowledgePack.HighRiskFiles, run.KnowledgePack.AnalysisExecution.TopChangeExamples...))
	case "FUZZ_TARGETS.md":
		return analysisUniqueStrings(symbolFiles(analysisFuzzTargetSymbols(run)))
	case "OPERATIONS_RUNBOOK.md":
		return analysisUniqueStrings(append(run.KnowledgePack.HighRiskFiles, subsystemFiles(run.KnowledgePack.Subsystems)...))
	default:
		return analysisUniqueStrings(append(run.KnowledgePack.TopImportantFiles, run.Snapshot.EntrypointFiles...))
	}
}

func analysisDocConfidence(run ProjectAnalysisRun, name string) string {
	confidence := analysisRunConfidence(run)
	if name == "FUZZ_TARGETS.md" && len(analysisFuzzTargetSymbols(run)) == 0 {
		return "low"
	}
	if name == "BUILD_AND_ARTIFACTS.md" && len(run.Snapshot.BuildContexts) == 0 && len(run.Snapshot.CompileCommands) == 0 {
		return "medium"
	}
	return confidence
}

func analysisDocStaleMarkers(run ProjectAnalysisRun, name string) []string {
	switch name {
	case "FINAL_REPORT.md", "ARCHITECTURE.md", "DEVELOPER_OVERVIEW.md", "FOLDER_MAP.md", "MODULES.md", "OPERATIONS_RUNBOOK.md":
		return analysisRunStaleMarkers(run)
	case "STRUCTURE_DIAGRAMS.md":
		return analysisUniqueStrings(append(analysisRunStaleMarkers(run), analysisGraphStaleMarkers(run)...))
	case "CODE_STRUCTURE_REFERENCE.md":
		return analysisUniqueStrings(append(run.KnowledgePack.AnalysisExecution.InvalidationReasons, run.KnowledgePack.AnalysisExecution.SemanticInvalidationReasons...))
	case "SECURITY_SURFACE.md", "FUZZ_TARGETS.md", "VERIFICATION_MATRIX.md":
		return analysisUniqueStrings(append(run.KnowledgePack.AnalysisExecution.TopChangeClasses, run.KnowledgePack.AnalysisExecution.SemanticInvalidationReasons...))
	default:
		return analysisUniqueStrings(run.KnowledgePack.AnalysisExecution.InvalidationReasons)
	}
}

func analysisDocSectionStaleMarkers(run ProjectAnalysisRun, docName string, sectionID string, sectionTitle string) []string {
	base := analysisDocStaleMarkers(run, docName)
	text := strings.ToLower(strings.Join([]string{docName, sectionID, sectionTitle}, " "))
	if containsAny(text, "project_edges", "trust_boundary", "data_flow", "attack_data_flow", "project edges", "trust boundary graph", "data flow graph", "attack and data flow") {
		return analysisUniqueStrings(append(base, analysisGraphStaleMarkers(run)...))
	}
	return base
}

func analysisGraphStaleMarkers(run ProjectAnalysisRun) []string {
	items := []string{}
	addIfGraphLike := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		text := strings.ToLower(value)
		if containsAny(text, "trust", "boundary", "edge", "data_flow", "data-flow", "flow", "rpc", "ioctl", "kernel", "driver", "replicated", "config_binding", "configured_by", "security_signal", "security_action", "asset", "runtime", "dependency") {
			items = append(items, value)
		}
	}
	for _, marker := range run.KnowledgePack.AnalysisExecution.TopChangeClasses {
		addIfGraphLike(marker)
	}
	for _, marker := range run.KnowledgePack.AnalysisExecution.SemanticInvalidationReasons {
		addIfGraphLike(marker)
	}
	for _, marker := range run.KnowledgePack.AnalysisExecution.InvalidationReasons {
		addIfGraphLike(marker)
	}
	for _, subsystem := range run.KnowledgePack.Subsystems {
		for _, marker := range subsystem.InvalidationReasons {
			addIfGraphLike(marker)
		}
		for _, diff := range subsystem.InvalidationDiff {
			addIfGraphLike(diff)
		}
		for _, change := range subsystem.InvalidationChanges {
			addIfGraphLike(renderInvalidationChange(change))
		}
	}
	for _, shard := range run.Shards {
		addIfGraphLike(shard.InvalidationReason)
		for _, diff := range shard.InvalidationDiff {
			addIfGraphLike(diff)
		}
		for _, change := range shard.InvalidationChanges {
			addIfGraphLike(renderInvalidationChange(change))
		}
	}
	return analysisUniqueStrings(items)
}

func analysisGraphSourceAnchors(run ProjectAnalysisRun) []string {
	items := []string{}
	for _, edge := range analysisGraphProjectEdges(run) {
		items = append(items, edge.Evidence...)
		if source := strings.TrimSpace(edge.Attributes["source"]); source != "" {
			items = append(items, source)
		}
	}
	return analysisUniqueStrings(items)
}

func analysisDocsReuseTargets() []string {
	return []string{"analysis_context", "evidence", "memory", "verification_planner", "fuzz_target_discovery"}
}

func analysisDocReuseTargets(name string) []string {
	switch name {
	case "FINAL_REPORT.md":
		return []string{"analysis_context", "memory", "final_report"}
	case "DEVELOPER_OVERVIEW.md", "FOLDER_MAP.md", "MODULES.md", "STRUCTURE_DIAGRAMS.md", "CODE_STRUCTURE_REFERENCE.md":
		return []string{"analysis_context", "memory", "developer_docs"}
	case "SECURITY_SURFACE.md":
		return []string{"analysis_context", "evidence", "memory", "verification_planner", "fuzz_target_discovery"}
	case "VERIFICATION_MATRIX.md":
		return []string{"verification_planner", "evidence"}
	case "FUZZ_TARGETS.md":
		return []string{"fuzz_target_discovery", "verification_planner"}
	default:
		return []string{"analysis_context", "memory"}
	}
}

func analysisDocQueryIntents(name string) []string {
	switch name {
	case "FINAL_REPORT.md":
		return []string{"deep_map", "flow_trace", "security_surface", "verification"}
	case "DEVELOPER_OVERVIEW.md":
		return []string{"deep_map", "module_drilldown"}
	case "FOLDER_MAP.md":
		return []string{"deep_map", "module_drilldown", "impact"}
	case "MODULES.md":
		return []string{"deep_map", "module_drilldown", "unreal_structure", "build_artifact"}
	case "STRUCTURE_DIAGRAMS.md":
		return []string{"deep_map", "flow_trace", "security_surface", "unreal_structure", "build_artifact"}
	case "CODE_STRUCTURE_REFERENCE.md":
		return []string{"deep_map", "flow_trace", "impact", "module_drilldown", "security_surface", "build_artifact"}
	case "ARCHITECTURE.md":
		return []string{"deep_map", "flow_trace", "security_surface"}
	case "SECURITY_SURFACE.md":
		return []string{"security_surface", "unreal_structure", "verification"}
	case "API_AND_ENTRYPOINTS.md":
		return []string{"flow_trace", "security_surface"}
	case "BUILD_AND_ARTIFACTS.md":
		return []string{"build_artifact", "impact", "unreal_structure"}
	case "VERIFICATION_MATRIX.md":
		return []string{"verification", "impact", "security_surface"}
	case "FUZZ_TARGETS.md":
		return []string{"security_surface", "verification"}
	case "OPERATIONS_RUNBOOK.md":
		return []string{"verification", "deep_map"}
	default:
		return []string{"general"}
	}
}

func analysisDocPriority(name string) int {
	switch name {
	case "FINAL_REPORT.md":
		return 10
	case "DEVELOPER_OVERVIEW.md":
		return 9
	case "MODULES.md", "STRUCTURE_DIAGRAMS.md", "CODE_STRUCTURE_REFERENCE.md":
		return 8
	case "ARCHITECTURE.md", "SECURITY_SURFACE.md", "BUILD_AND_ARTIFACTS.md", "VERIFICATION_MATRIX.md":
		return 7
	case "FOLDER_MAP.md", "API_AND_ENTRYPOINTS.md", "FUZZ_TARGETS.md":
		return 6
	default:
		return 3
	}
}

func analysisDocSectionQueryIntents(docName string, sectionID string, sectionTitle string) []string {
	text := strings.ToLower(strings.Join([]string{docName, sectionID, sectionTitle}, " "))
	intents := []string{}
	if containsAny(text, "runtime", "flow", "call", "entry", "startup", "execution", "api") {
		intents = append(intents, "flow_trace")
	}
	if containsAny(text, "security", "surface", "trust", "boundary", "attack", "fuzz") {
		intents = append(intents, "security_surface")
	}
	if containsAny(text, "impact", "dependency", "dependencies", "risk", "verification", "change") {
		intents = append(intents, "impact", "verification")
	}
	if containsAny(text, "module", "folder", "ownership", "responsibility", "public api", "internal") {
		intents = append(intents, "module_drilldown", "deep_map")
	}
	if containsAny(text, "build", "artifact", "compile", "generated", "target") {
		intents = append(intents, "build_artifact")
	}
	if containsAny(text, "unreal", "replication", "reflection", "rpc", "asset", "config") {
		intents = append(intents, "unreal_structure")
	}
	if len(intents) == 0 {
		intents = append(intents, analysisDocQueryIntents(docName)...)
	}
	return analysisUniqueStrings(intents)
}

func analysisDocSectionPriority(docName string, sectionID string, sectionTitle string) int {
	priority := analysisDocPriority(docName)
	text := strings.ToLower(strings.Join([]string{sectionID, sectionTitle}, " "))
	if containsAny(text, "primary", "runtime", "call", "trust", "security", "module", "source anchor", "verification", "critical anchor", "deterministic", "fact pack") {
		priority += 2
	}
	if containsAny(text, "summary", "overview", "project shape", "inventory") {
		priority++
	}
	return priority
}

func analysisDocSectionEntityRefs(run ProjectAnalysisRun, docName string, sectionID string, sectionTitle string) []string {
	items := []string{}
	text := strings.ToLower(strings.Join([]string{docName, sectionID, sectionTitle}, " "))
	if containsAny(text, "module", "build", "unreal") {
		for _, module := range buildDeveloperModuleRecords(run) {
			items = append(items, module.ID, module.Name, module.Root)
		}
	}
	if containsAny(text, "folder") {
		for _, folder := range buildDeveloperFolderRecords(run) {
			items = append(items, folder.Path)
		}
	}
	if containsAny(text, "symbol", "call", "entry", "surface", "security", "verification") {
		for _, symbol := range limitSymbolRecords(run.SemanticIndexV2.Symbols, 80) {
			items = append(items, symbol.ID, symbol.Name, symbol.CanonicalName, symbol.File)
		}
	}
	if containsAny(text, "build", "artifact", "compile") {
		for _, ctx := range run.Snapshot.BuildContexts {
			items = append(items, ctx.ID, ctx.Name, ctx.Module, ctx.Project, ctx.Target)
		}
	}
	return analysisUniqueStrings(analysisDocSlashPaths(items))
}

func analysisDocSectionGraphRefs(run ProjectAnalysisRun, docName string, sectionID string, sectionTitle string) []string {
	text := strings.ToLower(strings.Join([]string{docName, sectionID, sectionTitle}, " "))
	items := []string{}
	if containsAny(text, "runtime", "flow", "call", "entry") {
		items = append(items, "runtime_edges", "call_edges")
	}
	if containsAny(text, "build", "artifact", "compile", "generated") {
		items = append(items, "build_ownership_edges", "generated_code_edges")
	}
	if containsAny(text, "trust", "security", "surface", "attack") {
		items = append(items, "overlay_edges", "trust_boundary_graph")
	}
	if containsAny(text, "module", "folder", "dependency") {
		items = append(items, "module_dependency_graph", "folder_module_map")
	}
	if containsAny(text, "unreal", "replication", "rpc", "asset", "config") {
		items = append(items, "unreal_graph")
	}
	if len(run.UnrealGraph.Edges) == 0 && analysisContainsStringCI(items, "unreal_graph") {
		items = removeAnalysisStringCI(items, "unreal_graph")
	}
	return analysisUniqueStrings(items)
}

func analysisDocSections(run ProjectAnalysisRun, name string) []AnalysisDocSection {
	sectionTitles := map[string][][2]string{
		"FINAL_REPORT.md": {
			{"final_report.synthesis", "Final Synthesis Report"},
		},
		"ARCHITECTURE.md": {
			{"architecture.executive_summary", "Executive Summary"},
			{"architecture.subsystems", "Subsystems"},
			{"architecture.project_edges", "Project Edges"},
			{"architecture.trust_boundary_graph", "Trust Boundary Graph"},
			{"architecture.data_flow_graph", "Data Flow Graph"},
		},
		"DEVELOPER_OVERVIEW.md": {
			{"developer.project_shape", "Project Shape"},
			{"developer.deterministic_architecture_fact_pack", "Deterministic Architecture Fact Pack"},
			{"developer.architecture_layers", "Architecture Layers"},
			{"developer.primary_execution_flow", "Primary Execution Flow"},
			{"developer.runtime_narratives", "Primary Runtime Narratives"},
			{"developer.cross_cutting_paths", "Most Important Cross-Cutting Paths"},
			{"developer.domain_critical_anchors", "Domain Critical Anchors"},
			{"developer.main_development_areas", "Main Development Areas"},
			{"developer.where_to_start", "Where To Start By Task"},
			{"developer.reading_order", "Reading Order"},
		},
		"FOLDER_MAP.md": {
			{"folders.summary", "Folder Summary"},
			{"folders.responsibilities", "Folder Responsibilities"},
			{"folders.tests", "Folder To Test Mapping"},
			{"folders.risk", "Folder Risk And Change Notes"},
		},
		"MODULES.md": {
			{"modules.inventory", "Module Inventory"},
			{"modules.cards", "Module Responsibility Cards"},
			{"modules.public_api_boundary", "Public API And Boundary"},
			{"modules.internal_ownership", "Internal Ownership"},
			{"modules.dependencies", "Module Dependencies"},
			{"modules.upstream_downstream", "Upstream Downstream Dependencies"},
			{"modules.change_impact", "Change Impact Notes"},
			{"modules.verification", "Module Verification Notes"},
		},
		"STRUCTURE_DIAGRAMS.md": {
			{"diagrams.module_dependency_graph", "Module Dependency Graph"},
			{"diagrams.folder_module_map", "Folder And Module Map"},
			{"diagrams.primary_runtime_flow", "Primary Runtime Flow"},
			{"diagrams.startup_runtime_flow", "Startup To Runtime Flow"},
			{"diagrams.build_artifact_flow", "Build And Artifact Flow"},
			{"diagrams.build_ownership_flow", "Build Ownership Flow"},
			{"diagrams.trust_boundary_summary", "Trust Boundary Summary"},
			{"diagrams.security_boundary_flow", "Security Boundary Flow"},
			{"diagrams.unreal_reflection_replication", "Unreal Reflection And Replication Flow"},
		},
		"CODE_STRUCTURE_REFERENCE.md": {
			{"code.important_files", "Important Files"},
			{"code.important_symbols", "Important Symbols"},
			{"code.domain_critical_anchors", "Domain Critical Anchors"},
			{"code.deterministic_architecture_fact_pack", "Deterministic Architecture Fact Pack"},
			{"code.symbol_clusters", "Symbol Clusters"},
			{"code.call_paths", "Representative Call Paths"},
			{"code.caller_callee_hotspots", "Caller Callee Hotspots"},
			{"code.build_ownership", "Build Ownership"},
			{"code.build_context_source_mapping", "Build Context To Source Mapping"},
			{"code.generated_artifacts", "Generated Or Derived Artifacts"},
			{"code.verification_anchor_map", "Verification Anchor Map"},
			{"code.source_anchors", "Source Anchors"},
		},
		"SECURITY_SURFACE.md": {
			{"security.indexed_surfaces", "Indexed Security Surfaces"},
			{"security.unreal_network", "Unreal Network Surface"},
			{"security.high_risk_files", "High Risk Files"},
			{"security.trust_boundary_graph", "Trust Boundary Graph"},
			{"security.attack_data_flow", "Attack And Data Flow View"},
		},
		"API_AND_ENTRYPOINTS.md": {
			{"api.snapshot_entrypoints", "Snapshot Entrypoint Files"},
			{"api.indexed_symbols", "Indexed Symbols"},
			{"api.call_edges", "Representative Call Edges"},
		},
		"BUILD_AND_ARTIFACTS.md": {
			{"build.contexts", "Build Contexts"},
			{"build.compile_commands", "Compile Command Coverage"},
		},
		"VERIFICATION_MATRIX.md": {
			{"verification.matrix", "Verification Matrix"},
			{"verification.change_classes", "Recent Change Classes"},
		},
		"FUZZ_TARGETS.md": {
			{"fuzz.target_catalog", "Target Catalog"},
			{"fuzz.candidate_targets", "Candidate Targets"},
		},
		"OPERATIONS_RUNBOOK.md": {
			{"ops.default_loop", "Default Operating Loop"},
			{"ops.open_unknowns", "Open Unknowns"},
		},
	}
	items := sectionTitles[name]
	if len(items) == 0 {
		return nil
	}
	out := make([]AnalysisDocSection, 0, len(items))
	for _, item := range items {
		out = append(out, AnalysisDocSection{
			ID:            item[0],
			Title:         item[1],
			SourceAnchors: analysisDocSourceAnchors(run, name),
			Confidence:    analysisDocConfidence(run, name),
			StaleMarkers:  analysisDocSectionStaleMarkers(run, name, item[0], item[1]),
			ReuseTargets:  analysisDocReuseTargets(name),
			QueryIntents:  analysisDocSectionQueryIntents(name, item[0], item[1]),
			Priority:      analysisDocSectionPriority(name, item[0], item[1]),
			EntityRefs:    analysisDocSectionEntityRefs(run, name, item[0], item[1]),
			GraphRefs:     analysisDocSectionGraphRefs(run, name, item[0], item[1]),
		})
	}
	return out
}

func removeAnalysisStringCI(items []string, value string) []string {
	out := []string{}
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(value)) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func analysisContainsStringCI(items []string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), value) {
			return true
		}
	}
	return false
}

func subsystemFiles(subsystems []KnowledgeSubsystem) []string {
	items := []string{}
	for _, subsystem := range subsystems {
		items = append(items, subsystem.KeyFiles...)
		items = append(items, subsystem.EvidenceFiles...)
		items = append(items, subsystem.InvalidationEvidence...)
	}
	return items
}

func unrealNetworkSurfaceFiles(items []UnrealNetworkSurface) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, item.File)
	}
	return analysisUniqueStrings(out)
}

func symbolFiles(symbols []SymbolRecord) []string {
	items := []string{}
	for _, symbol := range symbols {
		if strings.TrimSpace(symbol.File) == "" {
			continue
		}
		file := analysisDocSlashPath(symbol.File)
		if symbol.StartLine > 0 {
			items = append(items, fmt.Sprintf("%s:%d", file, symbol.StartLine))
		} else {
			items = append(items, file)
		}
	}
	return items
}

func compileCommandFiles(commands []CompilationCommandRecord) []string {
	items := []string{}
	for _, command := range commands {
		items = append(items, command.File)
	}
	return items
}

func analysisDocTitle(name string) string {
	switch name {
	case "INDEX.md":
		return "Project Documentation Index"
	case "FINAL_REPORT.md":
		return "Final Synthesis Report"
	case "ARCHITECTURE.md":
		return "Architecture"
	case "DEVELOPER_OVERVIEW.md":
		return "Developer Overview"
	case "FOLDER_MAP.md":
		return "Folder Map"
	case "MODULES.md":
		return "Modules"
	case "STRUCTURE_DIAGRAMS.md":
		return "Structure Diagrams"
	case "CODE_STRUCTURE_REFERENCE.md":
		return "Code Structure Reference"
	case "SECURITY_SURFACE.md":
		return "Security Surface"
	case "API_AND_ENTRYPOINTS.md":
		return "API And Entrypoints"
	case "BUILD_AND_ARTIFACTS.md":
		return "Build And Artifacts"
	case "VERIFICATION_MATRIX.md":
		return "Verification Matrix"
	case "FUZZ_TARGETS.md":
		return "Fuzz Targets"
	case "OPERATIONS_RUNBOOK.md":
		return "Operations Runbook"
	default:
		return strings.TrimSuffix(name, filepath.Ext(name))
	}
}

func analysisDocKind(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
}

func analysisDocPurpose(name string) string {
	switch name {
	case "FINAL_REPORT.md":
		return "the exact assistant-facing final report printed after analyze-project"
	case "ARCHITECTURE.md":
		return "subsystem ownership, runtime flow, and project edges"
	case "DEVELOPER_OVERVIEW.md":
		return "developer onboarding map, reading order, and change starting points"
	case "FOLDER_MAP.md":
		return "folder responsibilities, key files, tests, build context, and risk notes"
	case "MODULES.md":
		return "module inventory, ownership boundaries, entrypoints, and dependencies"
	case "STRUCTURE_DIAGRAMS.md":
		return "Mermaid diagrams for module dependencies, folder ownership, runtime flow, build flow, and trust boundaries"
	case "CODE_STRUCTURE_REFERENCE.md":
		return "important files, symbols, call paths, build ownership, generated artifacts, and source anchors"
	case "SECURITY_SURFACE.md":
		return "privileged, input-facing, and tamper-sensitive surfaces"
	case "API_AND_ENTRYPOINTS.md":
		return "entrypoint files, indexed symbols, and representative call edges"
	case "BUILD_AND_ARTIFACTS.md":
		return "build contexts, manifests, compile command coverage, and artifacts"
	case "VERIFICATION_MATRIX.md":
		return "required and optional verification by change area"
	case "FUZZ_TARGETS.md":
		return "source-derived fuzz target candidates and suggested commands"
	case "OPERATIONS_RUNBOOK.md":
		return "recommended investigation, fuzzing, verification, and evidence loop"
	default:
		return "generated analysis document"
	}
}

func mapValues(items map[string]string) []string {
	out := []string{}
	for key, value := range items {
		out = append(out, key, value)
	}
	sort.Strings(out)
	return out
}

func sortSymbolRecords(items []SymbolRecord) {
	sort.Slice(items, func(i int, j int) bool {
		if items[i].File != items[j].File {
			return items[i].File < items[j].File
		}
		if items[i].StartLine != items[j].StartLine {
			return items[i].StartLine < items[j].StartLine
		}
		return items[i].Name < items[j].Name
	})
}

func limitSymbolRecords(items []SymbolRecord, limit int) []SymbolRecord {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]SymbolRecord(nil), items...)
	}
	return append([]SymbolRecord(nil), items[:limit]...)
}

func limitCallEdges(items []CallEdge, limit int) []CallEdge {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]CallEdge(nil), items...)
	}
	return append([]CallEdge(nil), items[:limit]...)
}

func limitBuildContexts(items []BuildContextRecord, limit int) []BuildContextRecord {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]BuildContextRecord(nil), items...)
	}
	return append([]BuildContextRecord(nil), items[:limit]...)
}

func limitCompileCommands(items []CompilationCommandRecord, limit int) []CompilationCommandRecord {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]CompilationCommandRecord(nil), items...)
	}
	return append([]CompilationCommandRecord(nil), items[:limit]...)
}

func limitUnrealNetworkSurfaces(items []UnrealNetworkSurface, limit int) []UnrealNetworkSurface {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]UnrealNetworkSurface(nil), items...)
	}
	return append([]UnrealNetworkSurface(nil), items[:limit]...)
}
