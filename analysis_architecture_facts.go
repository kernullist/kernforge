package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ArchitectureFactPack struct {
	GeneratedAt                    time.Time                   `json:"generated_at,omitempty"`
	Source                         string                      `json:"source,omitempty"`
	DomainHints                    []string                    `json:"domain_hints,omitempty"`
	TopLevelDirectories            []ArchitectureDirectoryFact `json:"top_level_directories,omitempty"`
	TopLevelNonDirectoryExclusions []string                    `json:"top_level_non_directory_exclusions,omitempty"`
	CriticalAnchors                []ArchitectureAnchorFact    `json:"critical_anchors,omitempty"`
	FlowFacts                      []ArchitectureFlowFact      `json:"flow_facts,omitempty"`
	BoundaryFacts                  []ArchitectureBoundaryFact  `json:"boundary_facts,omitempty"`
	Invariants                     []string                    `json:"invariants,omitempty"`
}

type ArchitectureDirectoryFact struct {
	Path        string   `json:"path"`
	Role        string   `json:"role,omitempty"`
	Kind        string   `json:"kind,omitempty"`
	FileCount   int      `json:"file_count,omitempty"`
	Evidence    []string `json:"evidence,omitempty"`
	Confidence  string   `json:"confidence,omitempty"`
	SourceCount int      `json:"source_count,omitempty"`
}

type ArchitectureAnchorFact struct {
	Role             string   `json:"role,omitempty"`
	Symbol           string   `json:"symbol,omitempty"`
	Kind             string   `json:"kind,omitempty"`
	File             string   `json:"file,omitempty"`
	Line             int      `json:"line,omitempty"`
	Location         string   `json:"location,omitempty"`
	Side             string   `json:"side,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	Why              string   `json:"why,omitempty"`
	VerificationHint string   `json:"verification_hint,omitempty"`
	Score            int      `json:"score,omitempty"`
}

type ArchitectureFlowFact struct {
	Name       string   `json:"name,omitempty"`
	Kind       string   `json:"kind,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Steps      []string `json:"steps,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
}

type ArchitectureBoundaryFact struct {
	Name       string   `json:"name,omitempty"`
	Kind       string   `json:"kind,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
}

func buildArchitectureFactPack(snapshot ProjectSnapshot, index SemanticIndexV2, graph UnrealSemanticGraph, goal string) ArchitectureFactPack {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			Goal: strings.TrimSpace(goal),
			Mode: snapshot.AnalysisMode,
		},
		Snapshot:        snapshot,
		SemanticIndexV2: index,
		UnrealGraph:     graph,
	}
	domainHints := projectStructureDomainHints(run)
	critical := selectProjectStructureCriticalAnchors(run, relevantSemanticIndexV2Hits{}, projectAnalysisQAIntentDeepMap, 24)
	pack := ArchitectureFactPack{
		GeneratedAt:                    snapshot.GeneratedAt,
		Source:                         "deterministic-snapshot-and-structural-index-v2",
		DomainHints:                    domainHints,
		TopLevelDirectories:            buildArchitectureDirectoryFacts(snapshot, index),
		TopLevelNonDirectoryExclusions: buildArchitectureTopLevelExclusions(snapshot),
		CriticalAnchors:                architectureAnchorFactsFromCriticalAnchors(critical),
		FlowFacts:                      buildArchitectureFlowFacts(snapshot, index, domainHints, critical),
		BoundaryFacts:                  buildArchitectureBoundaryFacts(snapshot, index, domainHints, critical),
	}
	pack.Invariants = buildArchitectureInvariants(pack)
	pack = normalizeArchitectureFactPack(pack)
	return pack
}

func buildArchitectureDirectoryFacts(snapshot ProjectSnapshot, index SemanticIndexV2) []ArchitectureDirectoryFact {
	roots := architectureRootDirectories(snapshot)
	if len(roots) == 0 {
		return nil
	}
	symbolsByRoot := map[string][]SymbolRecord{}
	for _, symbol := range index.Symbols {
		root := architecturePathRoot(symbol.File)
		if root != "" {
			symbolsByRoot[root] = append(symbolsByRoot[root], symbol)
		}
	}
	buildContextsByRoot := map[string][]BuildContextRecord{}
	for _, ctx := range snapshot.BuildContexts {
		root := architecturePathRoot(firstNonBlankProjectStructureString(ctx.Directory, ctx.Source, firstSliceValue(ctx.Files)))
		if root != "" {
			buildContextsByRoot[root] = append(buildContextsByRoot[root], ctx)
		}
		for _, file := range ctx.Files {
			root = architecturePathRoot(file)
			if root != "" {
				buildContextsByRoot[root] = append(buildContextsByRoot[root], ctx)
			}
		}
	}
	filesByRoot := map[string][]ScannedFile{}
	for _, file := range snapshot.Files {
		root := architecturePathRoot(file.Path)
		if root != "" {
			filesByRoot[root] = append(filesByRoot[root], file)
		}
	}
	projectsByRoot := map[string][]SolutionProject{}
	for _, project := range snapshot.SolutionProjects {
		root := architecturePathRoot(firstNonBlankAnalysisString(project.Directory, project.Path))
		if root != "" {
			projectsByRoot[root] = append(projectsByRoot[root], project)
		}
	}
	out := []ArchitectureDirectoryFact{}
	for _, root := range roots {
		files := filesByRoot[root]
		record := DeveloperFolderRecord{
			Path:          root,
			KeyFiles:      architectureImportantFiles(files, 8),
			MainSymbols:   limitSymbolRecords(symbolsByRoot[root], 12),
			BuildContexts: uniqueDeveloperBuildContexts(buildContextsByRoot[root]),
			Confidence:    "high",
		}
		role := inferFolderResponsibility(record)
		projectEvidence := []string{}
		projectKinds := []string{}
		for _, project := range projectsByRoot[root] {
			projectEvidence = append(projectEvidence, project.Path)
			projectKinds = append(projectKinds, strings.TrimSpace(project.Kind), strings.TrimSpace(project.OutputType))
			if solutionProjectLooksLikeDriverRuntime(project) {
				role = "kernel driver runtime, privileged dispatch, and protection subsystems"
			}
		}
		evidence := append([]string{}, record.KeyFiles...)
		evidence = append(evidence, projectEvidence...)
		for _, ctx := range record.BuildContexts {
			evidence = append(evidence, ctx.Source)
		}
		sourceCount := 0
		for _, file := range files {
			if analysisSupportsSourceAnchors(file.Extension) {
				sourceCount++
			}
		}
		out = append(out, ArchitectureDirectoryFact{
			Path:        root + "/",
			Role:        role,
			Kind:        firstNonBlankProjectStructureString(append(projectKinds, "directory")...),
			FileCount:   len(files),
			SourceCount: sourceCount,
			Evidence:    limitStrings(analysisUniqueStrings(evidence), 8),
			Confidence:  "high",
		})
	}
	sort.Slice(out, func(i int, j int) bool {
		if out[i].FileCount != out[j].FileCount {
			return out[i].FileCount > out[j].FileCount
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func architectureRootDirectories(snapshot ProjectSnapshot) []string {
	seen := map[string]string{}
	add := func(path string) {
		root := architecturePathRoot(path)
		if root == "" || analysisDocPathLooksLikeFile(root) {
			return
		}
		key := strings.ToLower(root)
		if _, ok := seen[key]; !ok {
			seen[key] = root
		}
	}
	for _, dir := range snapshot.Directories {
		add(dir)
	}
	for _, file := range snapshot.Files {
		add(firstNonBlankAnalysisString(file.Directory, analysisDocDir(file.Path)))
	}
	for _, project := range snapshot.SolutionProjects {
		add(firstNonBlankAnalysisString(project.Directory, analysisDocDir(project.Path)))
	}
	items := make([]string, 0, len(seen))
	for key := range seen {
		items = append(items, key)
	}
	sort.Strings(items)
	out := []string{}
	for _, key := range items {
		out = append(out, seen[key])
	}
	return out
}

func architecturePathRoot(path string) string {
	path = strings.Trim(strings.ReplaceAll(filepath.ToSlash(strings.TrimSpace(path)), "\\", "/"), "/")
	if path == "" || path == "." || analysisDocPathLooksLikeFile(path) {
		if analysisDocPathLooksLikeFile(path) {
			path = analysisDocDir(path)
		}
	}
	if path == "" || path == "." {
		return ""
	}
	if idx := strings.Index(path, "/"); idx >= 0 {
		path = path[:idx]
	}
	if path == "" || path == "." || analysisDocPathLooksLikeFile(path) {
		return ""
	}
	return path
}

func architectureImportantFiles(files []ScannedFile, limit int) []string {
	items := append([]ScannedFile(nil), files...)
	sort.SliceStable(items, func(i int, j int) bool {
		if items[i].ImportanceScore == items[j].ImportanceScore {
			if items[i].LineCount == items[j].LineCount {
				return items[i].Path < items[j].Path
			}
			return items[i].LineCount > items[j].LineCount
		}
		return items[i].ImportanceScore > items[j].ImportanceScore
	})
	out := []string{}
	for _, file := range items {
		if strings.TrimSpace(file.Path) == "" {
			continue
		}
		out = append(out, analysisDocSlashPath(file.Path))
		if len(out) >= limit {
			break
		}
	}
	return analysisUniqueStrings(out)
}

func buildArchitectureTopLevelExclusions(snapshot ProjectSnapshot) []string {
	rootSet := map[string]struct{}{}
	for _, root := range architectureRootDirectories(snapshot) {
		rootSet[strings.ToLower(root)] = struct{}{}
	}
	exclusions := []string{}
	add := func(path string) {
		path = strings.Trim(strings.ReplaceAll(analysisDocSlashPath(path), "\\", "/"), "/")
		if path == "" || path == "." {
			return
		}
		lower := strings.ToLower(path)
		if _, ok := rootSet[lower]; ok {
			return
		}
		if analysisDocPathLooksLikeFile(path) || strings.Contains(path, "/") {
			exclusions = append(exclusions, path)
		}
	}
	for _, file := range snapshot.Files {
		add(file.Path)
	}
	for _, dir := range snapshot.Directories {
		add(dir)
	}
	for _, file := range snapshot.ManifestFiles {
		add(file)
	}
	for _, file := range snapshot.EntrypointFiles {
		add(file)
	}
	return analysisUniqueStrings(projectStructurePrioritizedTopLevelExclusions(exclusions))
}

func architectureAnchorFactsFromCriticalAnchors(items []ProjectStructureCriticalAnchor) []ArchitectureAnchorFact {
	out := []ArchitectureAnchorFact{}
	for _, item := range items {
		side := "source"
		if item.KernelSide {
			side = "kernel"
		} else if item.UserModeSide {
			side = "user"
		}
		out = append(out, ArchitectureAnchorFact{
			Role:             item.Role,
			Symbol:           item.Name,
			Kind:             item.Kind,
			File:             item.File,
			Line:             item.Line,
			Location:         projectStructureCriticalAnchorLocation(item),
			Side:             side,
			Tags:             append([]string(nil), item.Tags...),
			Why:              item.Why,
			VerificationHint: item.VerificationHint,
			Score:            item.Score,
		})
	}
	return out
}

func buildArchitectureFlowFacts(snapshot ProjectSnapshot, index SemanticIndexV2, domainHints []string, critical []ProjectStructureCriticalAnchor) []ArchitectureFlowFact {
	out := []ArchitectureFlowFact{}
	for _, flow := range projectStructureDomainFlows(ProjectStructureAnswerPack{DomainHints: domainHints, CriticalAnchors: critical}) {
		name, steps := splitArchitectureFlowText(flow)
		out = append(out, ArchitectureFlowFact{
			Name:       name,
			Kind:       architectureFlowKind(name),
			Summary:    flow,
			Steps:      steps,
			Evidence:   architectureEvidenceForFlow(flow, critical),
			Confidence: "high",
		})
	}
	if edges := highConfidenceRuntimeEdges(snapshot.RuntimeEdges); len(edges) > 0 {
		steps := []string{}
		evidence := []string{}
		for _, edge := range limitRuntimeEdges(edges, 8) {
			steps = append(steps, fmt.Sprintf("%s -> %s (%s)", edge.Source, edge.Target, firstNonBlankAnalysisString(edge.Kind, "runtime_edge")))
			evidence = append(evidence, edge.Evidence...)
		}
		out = append(out, ArchitectureFlowFact{
			Name:       "high-confidence runtime edges",
			Kind:       "runtime",
			Summary:    "High-confidence runtime transitions discovered before LLM synthesis.",
			Steps:      steps,
			Evidence:   analysisUniqueStrings(evidence),
			Confidence: "high",
		})
	}
	if len(index.CallEdges) > 0 {
		names := semanticIndexV2NameMap(index)
		registrationSteps := []string{}
		registrationEvidence := []string{}
		steps := []string{}
		evidence := []string{}
		for _, edge := range limitCallEdges(index.CallEdges, 12) {
			source := firstNonBlankAnalysisString(names[edge.SourceID], edge.SourceID)
			target := firstNonBlankAnalysisString(names[edge.TargetID], edge.TargetID)
			steps = append(steps, fmt.Sprintf("%s -> %s (%s)", source, target, firstNonBlankAnalysisString(edge.Type, "calls")))
			evidence = append(evidence, edge.Evidence...)
		}
		for _, edge := range index.CallEdges {
			if !architectureEdgeIsRegistration(edge.Type) {
				continue
			}
			source := firstNonBlankAnalysisString(names[edge.SourceID], edge.SourceID)
			target := firstNonBlankAnalysisString(names[edge.TargetID], edge.TargetID)
			registrationSteps = append(registrationSteps, fmt.Sprintf("%s -> %s (%s)", source, target, edge.Type))
			registrationEvidence = append(registrationEvidence, edge.Evidence...)
		}
		if len(registrationSteps) > 0 {
			out = append(out, ArchitectureFlowFact{
				Name:       "registered callback and dispatch edges",
				Kind:       "registration",
				Summary:    "Function-pointer, IRP dispatch, and kernel callback registrations discovered by deterministic source scanning.",
				Steps:      limitStrings(analysisUniqueStrings(registrationSteps), 16),
				Evidence:   analysisUniqueStrings(registrationEvidence),
				Confidence: "high",
			})
		}
		out = append(out, ArchitectureFlowFact{
			Name:       "representative call edges",
			Kind:       "call_graph",
			Summary:    "Representative function-level call edges from structural_index_v2.",
			Steps:      steps,
			Evidence:   analysisUniqueStrings(evidence),
			Confidence: "medium",
		})
	}
	return uniqueArchitectureFlowFacts(out)
}

func architectureEdgeIsRegistration(edgeType string) bool {
	lower := strings.ToLower(strings.TrimSpace(edgeType))
	return strings.HasPrefix(lower, "registers_") ||
		strings.HasPrefix(lower, "assigns_") ||
		strings.Contains(lower, "callback") ||
		strings.Contains(lower, "dispatch")
}

func splitArchitectureFlowText(flow string) (string, []string) {
	flow = strings.TrimSpace(flow)
	if flow == "" {
		return "", nil
	}
	name := flow
	body := flow
	if idx := strings.Index(flow, ":"); idx >= 0 {
		name = strings.TrimSpace(flow[:idx])
		body = strings.TrimSpace(flow[idx+1:])
	}
	parts := strings.FieldsFunc(body, func(r rune) bool {
		return r == ';'
	})
	steps := []string{}
	for _, part := range parts {
		for _, step := range strings.Split(part, "->") {
			step = strings.TrimSpace(step)
			if step != "" {
				steps = append(steps, step)
			}
		}
	}
	return name, analysisUniqueStrings(steps)
}

func architectureFlowKind(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch {
	case containsAny(lower, "ioctl", "device-control"):
		return "ioctl"
	case containsAny(lower, "irp"):
		return "irp"
	case containsAny(lower, "object", "handle"):
		return "handle"
	case containsAny(lower, "process"):
		return "process"
	case containsAny(lower, "teardown", "unload", "cleanup"):
		return "teardown"
	case containsAny(lower, "runtime"):
		return "runtime"
	default:
		return "architecture"
	}
}

func architectureEvidenceForFlow(flow string, anchors []ProjectStructureCriticalAnchor) []string {
	lower := strings.ToLower(flow)
	evidence := []string{}
	for _, anchor := range anchors {
		if strings.TrimSpace(anchor.File) == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(anchor.Name)) ||
			strings.Contains(lower, strings.ToLower(anchor.Role)) ||
			strings.Contains(lower, strings.ToLower(anchor.File)) {
			if anchor.Line > 0 {
				evidence = append(evidence, fmt.Sprintf("%s:%d", anchor.File, anchor.Line))
			} else {
				evidence = append(evidence, anchor.File)
			}
		}
	}
	return analysisUniqueStrings(evidence)
}

func buildArchitectureBoundaryFacts(snapshot ProjectSnapshot, index SemanticIndexV2, domainHints []string, critical []ProjectStructureCriticalAnchor) []ArchitectureBoundaryFact {
	out := []ArchitectureBoundaryFact{}
	if analysisContainsStringCI(domainHints, "windows_driver") {
		kernelEvidence := []string{}
		userEvidence := []string{}
		for _, anchor := range critical {
			if anchor.KernelSide {
				kernelEvidence = append(kernelEvidence, anchor.File)
			}
			if anchor.UserModeSide {
				userEvidence = append(userEvidence, anchor.File)
			}
		}
		out = append(out, ArchitectureBoundaryFact{
			Name:       "user-mode control client vs kernel driver runtime",
			Kind:       "privilege_boundary",
			Summary:    "User-mode control/client wrappers and kernel-side IRP/IOCTL dispatch are separate layers; do not merge their responsibilities.",
			Evidence:   limitStrings(analysisUniqueStrings(append(kernelEvidence, userEvidence...)), 10),
			Confidence: "high",
		})
	}
	if len(index.OverlayEdges) > 0 {
		byDomain := map[string][]OverlayEdge{}
		for _, edge := range index.OverlayEdges {
			byDomain[edge.Domain] = append(byDomain[edge.Domain], edge)
		}
		domains := mapKeysSortedOverlayEdges(byDomain)
		for _, domain := range limitStrings(domains, 8) {
			edges := byDomain[domain]
			evidence := []string{}
			for _, edge := range edges {
				evidence = append(evidence, edge.Evidence...)
			}
			out = append(out, ArchitectureBoundaryFact{
				Name:       domain,
				Kind:       "semantic_overlay",
				Summary:    fmt.Sprintf("%s has %d structural overlay edge(s).", domain, len(edges)),
				Evidence:   limitStrings(analysisUniqueStrings(evidence), 8),
				Confidence: "medium",
			})
		}
	}
	for _, project := range snapshot.SolutionProjects {
		if !solutionProjectLooksLikeDriverRuntime(project) {
			continue
		}
		out = append(out, ArchitectureBoundaryFact{
			Name:       firstNonBlankAnalysisString(project.Name, project.Path),
			Kind:       "driver_build_boundary",
			Summary:    "Build metadata identifies this project as a driver/kernel runtime boundary.",
			Evidence:   analysisUniqueStrings(append([]string{project.Path}, project.EntryFiles...)),
			Confidence: "high",
		})
	}
	return uniqueArchitectureBoundaryFacts(out)
}

func mapKeysSortedOverlayEdges(items map[string][]OverlayEdge) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func buildArchitectureInvariants(pack ArchitectureFactPack) []string {
	out := []string{}
	if len(pack.TopLevelDirectories) > 0 {
		roots := []string{}
		for _, dir := range pack.TopLevelDirectories {
			roots = append(roots, dir.Path)
		}
		out = append(out, "Top-level directory maps must use this closed set only: "+strings.Join(analysisUniqueStrings(roots), ", "))
	}
	if len(pack.TopLevelNonDirectoryExclusions) > 0 {
		out = append(out, "Never list files or nested paths as top-level directories: "+strings.Join(limitStrings(pack.TopLevelNonDirectoryExclusions, 10), ", "))
	}
	if analysisContainsStringCI(pack.DomainHints, "windows_driver") {
		out = append(out,
			"Classify driver projects from build/output evidence; describe file/minifilter code as a subsystem unless build evidence says the whole driver is minifilter-only.",
			"Keep initialization/state setup, runtime callback/filter registration, IOCTL command dispatch, request-origin validation, and teardown as separate paths unless call-edge evidence connects them.",
			"Treat control PID/accessor symbols as identity-state accessors, not Finalize/Unload lifecycle functions.",
		)
	}
	if len(pack.CriticalAnchors) > 0 {
		out = append(out, "Use exact symbol names and exact file:line source anchors from critical_anchors; do not replace known line numbers with ellipsis.")
	}
	return analysisUniqueStrings(out)
}

func normalizeArchitectureFactPack(pack ArchitectureFactPack) ArchitectureFactPack {
	pack.DomainHints = analysisUniqueStrings(pack.DomainHints)
	pack.TopLevelNonDirectoryExclusions = analysisUniqueStrings(pack.TopLevelNonDirectoryExclusions)
	pack.Invariants = analysisUniqueStrings(pack.Invariants)
	for i := range pack.TopLevelDirectories {
		pack.TopLevelDirectories[i].Evidence = analysisUniqueStrings(pack.TopLevelDirectories[i].Evidence)
	}
	for i := range pack.CriticalAnchors {
		pack.CriticalAnchors[i].Tags = analysisUniqueStrings(pack.CriticalAnchors[i].Tags)
	}
	for i := range pack.FlowFacts {
		pack.FlowFacts[i].Steps = analysisUniqueStrings(pack.FlowFacts[i].Steps)
		pack.FlowFacts[i].Evidence = analysisUniqueStrings(pack.FlowFacts[i].Evidence)
	}
	for i := range pack.BoundaryFacts {
		pack.BoundaryFacts[i].Evidence = analysisUniqueStrings(pack.BoundaryFacts[i].Evidence)
	}
	return pack
}

func architectureFactPackHasData(pack ArchitectureFactPack) bool {
	return len(pack.DomainHints) > 0 ||
		len(pack.TopLevelDirectories) > 0 ||
		len(pack.CriticalAnchors) > 0 ||
		len(pack.FlowFacts) > 0 ||
		len(pack.BoundaryFacts) > 0 ||
		len(pack.Invariants) > 0
}

func firstArchitectureFactPack(items ...ArchitectureFactPack) ArchitectureFactPack {
	for _, item := range items {
		if architectureFactPackHasData(item) {
			return item
		}
	}
	return ArchitectureFactPack{}
}

func analysisDocsWriteArchitectureFactPack(b *strings.Builder, run ProjectAnalysisRun) {
	pack := firstArchitectureFactPack(run.Snapshot.ArchitectureFacts, run.KnowledgePack.ArchitectureFacts)
	if !architectureFactPackHasData(pack) {
		return
	}
	fmt.Fprintf(b, "\n## Deterministic Architecture Fact Pack\n\n")
	if len(pack.DomainHints) > 0 {
		fmt.Fprintf(b, "- Domain hints: %s\n", strings.Join(limitStrings(pack.DomainHints, 8), ", "))
	}
	if len(pack.TopLevelDirectories) > 0 {
		fmt.Fprintf(b, "\n### Authoritative Top-Level Directories\n\n")
		fmt.Fprintf(b, "| Directory | Role | Evidence |\n")
		fmt.Fprintf(b, "| --- | --- | --- |\n")
		for _, dir := range limitArchitectureDirectories(pack.TopLevelDirectories, 12) {
			fmt.Fprintf(b, "| `%s` | %s | %s |\n",
				analysisMarkdownCell(dir.Path),
				analysisMarkdownCell(dir.Role),
				analysisMarkdownCell(strings.Join(limitStrings(dir.Evidence, 4), ", ")))
		}
	}
	if len(pack.Invariants) > 0 {
		fmt.Fprintf(b, "\n### Invariants\n\n")
		for _, invariant := range limitStrings(pack.Invariants, 10) {
			fmt.Fprintf(b, "- %s\n", invariant)
		}
	}
	if len(pack.CriticalAnchors) > 0 {
		fmt.Fprintf(b, "\n### Critical Source Anchors\n\n")
		fmt.Fprintf(b, "| Role | Symbol | Location | Side | Verification |\n")
		fmt.Fprintf(b, "| --- | --- | --- | --- | --- |\n")
		for _, anchor := range limitArchitectureAnchors(pack.CriticalAnchors, 18) {
			fmt.Fprintf(b, "| `%s` | `%s` | `%s` | %s | %s |\n",
				analysisMarkdownCell(anchor.Role),
				analysisMarkdownCell(anchor.Symbol),
				analysisMarkdownCell(anchor.Location),
				analysisMarkdownCell(anchor.Side),
				analysisMarkdownCell(anchor.VerificationHint))
		}
	}
	if len(pack.FlowFacts) > 0 {
		fmt.Fprintf(b, "\n### Verified Flow Facts\n\n")
		for _, flow := range limitArchitectureFlows(pack.FlowFacts, 10) {
			fmt.Fprintf(b, "- `%s` (%s): %s\n",
				analysisMarkdownCell(flow.Name),
				analysisMarkdownCell(firstNonBlankAnalysisString(flow.Kind, "flow")),
				compactProjectAnalysisText(firstNonBlankAnalysisString(flow.Summary, strings.Join(flow.Steps, " -> ")), 240))
		}
	}
}

func renderArchitectureFactPackForPrompt(pack ArchitectureFactPack, shard AnalysisShard, maxChars int) string {
	if !architectureFactPackHasData(pack) {
		return ""
	}
	if maxChars <= 0 {
		maxChars = 4000
	}
	scoped := strings.TrimSpace(shard.ID) != "" || len(shard.PrimaryFiles)+len(shard.ReferenceFiles) > 0
	anchors := pack.CriticalAnchors
	flows := pack.FlowFacts
	boundaries := pack.BoundaryFacts
	if scoped {
		anchors = architectureRelevantAnchors(pack.CriticalAnchors, shard)
		flows = architectureRelevantFlows(pack.FlowFacts, shard, anchors)
		boundaries = architectureRelevantBoundaries(pack.BoundaryFacts, shard)
		if len(anchors) == 0 {
			anchors = limitArchitectureAnchors(pack.CriticalAnchors, 8)
		}
		if len(flows) == 0 {
			flows = limitArchitectureFlows(pack.FlowFacts, 5)
		}
		if len(boundaries) == 0 {
			boundaries = limitArchitectureBoundaries(pack.BoundaryFacts, 4)
		}
	}
	var b strings.Builder
	b.WriteString("Deterministic architecture fact pack (code-derived; authoritative over LLM guesses):\n")
	if len(pack.DomainHints) > 0 {
		fmt.Fprintf(&b, "- Domain hints: %s\n", strings.Join(limitStrings(pack.DomainHints, 8), ", "))
	}
	if len(pack.TopLevelDirectories) > 0 {
		b.WriteString("- Closed top-level directory set:\n")
		for _, dir := range limitArchitectureDirectories(pack.TopLevelDirectories, 10) {
			fmt.Fprintf(&b, "  - %s: %s", dir.Path, dir.Role)
			if len(dir.Evidence) > 0 {
				fmt.Fprintf(&b, " evidence=%s", strings.Join(limitStrings(dir.Evidence, 3), ", "))
			}
			b.WriteString("\n")
		}
	}
	if len(pack.TopLevelNonDirectoryExclusions) > 0 {
		fmt.Fprintf(&b, "- Never list as top-level directories: %s\n", strings.Join(limitStrings(pack.TopLevelNonDirectoryExclusions, 10), ", "))
	}
	for _, invariant := range limitStrings(pack.Invariants, 8) {
		fmt.Fprintf(&b, "- Invariant: %s\n", invariant)
	}
	if len(anchors) > 0 {
		b.WriteString("- Critical source anchors:\n")
		for _, anchor := range limitArchitectureAnchors(anchors, 14) {
			fmt.Fprintf(&b, "  - %s: %s (%s, %s)", anchor.Role, anchor.Symbol, anchor.Kind, anchor.Location)
			if strings.TrimSpace(anchor.Side) != "" {
				fmt.Fprintf(&b, " side=%s", anchor.Side)
			}
			if strings.TrimSpace(anchor.Why) != "" {
				fmt.Fprintf(&b, " why=%s", compactProjectAnalysisText(anchor.Why, 120))
			}
			b.WriteString("\n")
		}
	}
	if len(flows) > 0 {
		b.WriteString("- Verified flow facts:\n")
		for _, flow := range limitArchitectureFlows(flows, 8) {
			fmt.Fprintf(&b, "  - %s [%s]: %s\n", flow.Name, firstNonBlankAnalysisString(flow.Kind, "flow"), compactProjectAnalysisText(firstNonBlankAnalysisString(flow.Summary, strings.Join(flow.Steps, " -> ")), 260))
		}
	}
	if len(boundaries) > 0 {
		b.WriteString("- Boundary facts:\n")
		for _, boundary := range limitArchitectureBoundaries(boundaries, 6) {
			fmt.Fprintf(&b, "  - %s [%s]: %s", boundary.Name, firstNonBlankAnalysisString(boundary.Kind, "boundary"), compactProjectAnalysisText(boundary.Summary, 180))
			if len(boundary.Evidence) > 0 {
				fmt.Fprintf(&b, " evidence=%s", strings.Join(limitStrings(boundary.Evidence, 3), ", "))
			}
			b.WriteString("\n")
		}
	}
	return compactProjectAnalysisText(strings.TrimSpace(b.String()), maxChars)
}

func architectureRelevantAnchors(items []ArchitectureAnchorFact, shard AnalysisShard) []ArchitectureAnchorFact {
	scope := architectureShardScopeSet(shard)
	if len(scope) == 0 {
		return append([]ArchitectureAnchorFact(nil), items...)
	}
	out := []ArchitectureAnchorFact{}
	for _, item := range items {
		if architecturePathInScope(item.File, scope) || architecturePathInScope(item.Location, scope) {
			out = append(out, item)
		}
	}
	return out
}

func architectureRelevantFlows(items []ArchitectureFlowFact, shard AnalysisShard, anchors []ArchitectureAnchorFact) []ArchitectureFlowFact {
	scope := architectureShardScopeSet(shard)
	out := []ArchitectureFlowFact{}
	for _, item := range items {
		text := strings.ToLower(strings.Join(append(append([]string{item.Name, item.Kind, item.Summary}, item.Steps...), item.Evidence...), " "))
		matched := false
		for _, anchor := range anchors {
			for _, token := range []string{anchor.Role, anchor.Symbol, anchor.File, anchor.Location} {
				token = strings.ToLower(strings.TrimSpace(token))
				if token != "" && strings.Contains(text, token) {
					out = append(out, item)
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if matched {
			continue
		}
		for path := range scope {
			if path != "" && strings.Contains(text, path) {
				out = append(out, item)
				break
			}
		}
	}
	return uniqueArchitectureFlowFacts(out)
}

func architectureRelevantBoundaries(items []ArchitectureBoundaryFact, shard AnalysisShard) []ArchitectureBoundaryFact {
	scope := architectureShardScopeSet(shard)
	out := []ArchitectureBoundaryFact{}
	for _, item := range items {
		text := strings.ToLower(strings.Join(append([]string{item.Name, item.Kind, item.Summary}, item.Evidence...), " "))
		for path := range scope {
			if path != "" && strings.Contains(text, path) {
				out = append(out, item)
				break
			}
		}
	}
	return uniqueArchitectureBoundaryFacts(out)
}

func architectureShardScopeSet(shard AnalysisShard) map[string]struct{} {
	scope := map[string]struct{}{}
	for _, path := range append(append([]string{}, shard.PrimaryFiles...), shard.ReferenceFiles...) {
		path = strings.ToLower(strings.Trim(strings.ReplaceAll(analysisDocSlashPath(path), "\\", "/"), "/"))
		if path != "" {
			scope[path] = struct{}{}
			if dir := strings.ToLower(analysisDocDir(path)); dir != "" && dir != "." {
				scope[dir] = struct{}{}
			}
		}
	}
	return scope
}

func architecturePathInScope(path string, scope map[string]struct{}) bool {
	path = strings.ToLower(strings.Trim(strings.ReplaceAll(analysisDocSlashPath(path), "\\", "/"), "/"))
	if idx := strings.LastIndex(path, ":"); idx > 1 && allDigits(path[idx+1:]) {
		path = path[:idx]
	}
	if path == "" {
		return false
	}
	if _, ok := scope[path]; ok {
		return true
	}
	for item := range scope {
		if strings.HasPrefix(path, item+"/") || strings.HasPrefix(item, path+"/") {
			return true
		}
	}
	return false
}

func limitArchitectureDirectories(items []ArchitectureDirectoryFact, limit int) []ArchitectureDirectoryFact {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]ArchitectureDirectoryFact(nil), items...)
	}
	return append([]ArchitectureDirectoryFact(nil), items[:limit]...)
}

func limitArchitectureAnchors(items []ArchitectureAnchorFact, limit int) []ArchitectureAnchorFact {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]ArchitectureAnchorFact(nil), items...)
	}
	return append([]ArchitectureAnchorFact(nil), items[:limit]...)
}

func limitArchitectureFlows(items []ArchitectureFlowFact, limit int) []ArchitectureFlowFact {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]ArchitectureFlowFact(nil), items...)
	}
	return append([]ArchitectureFlowFact(nil), items[:limit]...)
}

func limitArchitectureBoundaries(items []ArchitectureBoundaryFact, limit int) []ArchitectureBoundaryFact {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]ArchitectureBoundaryFact(nil), items...)
	}
	return append([]ArchitectureBoundaryFact(nil), items[:limit]...)
}

func uniqueArchitectureFlowFacts(items []ArchitectureFlowFact) []ArchitectureFlowFact {
	seen := map[string]struct{}{}
	out := []ArchitectureFlowFact{}
	for _, item := range items {
		key := strings.ToLower(strings.Join([]string{item.Name, item.Kind, item.Summary}, "|"))
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

func uniqueArchitectureBoundaryFacts(items []ArchitectureBoundaryFact) []ArchitectureBoundaryFact {
	seen := map[string]struct{}{}
	out := []ArchitectureBoundaryFact{}
	for _, item := range items {
		key := strings.ToLower(strings.Join([]string{item.Name, item.Kind, item.Summary}, "|"))
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
