package main

// Deterministic project-structure metrics engine.
//
// This module derives graph-theoretic and evolutionary structure metrics from
// the already-scanned ProjectSnapshot (file list, import graph) plus optional
// git history. It is intentionally free of any model/LLM call, tree-sitter, or
// cgo dependency so that /analyze-project always produces trustworthy,
// code-derived structure facts even on plain single-language repositories where
// the build-graph and Unreal enrichment paths stay empty.
//
// Everything here is deterministic: every map is sorted before it is turned
// into output, PageRank runs a fixed iteration count, and git access degrades
// gracefully to an empty result when the workspace is not a repository.

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	structureMetricsRootLabel        = "(root)"
	structureMetricsPageRankDamping  = 0.85
	structureMetricsPageRankRounds   = 30
	structureMetricsFileCycleMaxNode = 40000
	structureMetricsGitWindowDays    = 90
	structureMetricsGitMaxCommits    = 800
	structureMetricsGitMaxCommitSize = 40
	structureMetricsChangeCoupleMin  = 3
)

// ProjectStructureMetrics is the full deterministic structure report attached
// to the snapshot and rendered into the analysis document set.
type ProjectStructureMetrics struct {
	GeneratedAt      time.Time                `json:"generated_at,omitempty"`
	FileCount        int                      `json:"file_count,omitempty"`
	PackageCount     int                      `json:"package_count,omitempty"`
	FileEdgeCount    int                      `json:"file_edge_count,omitempty"`
	PackageEdgeCount int                      `json:"package_edge_count,omitempty"`
	Packages         []PackageStructureRecord `json:"packages,omitempty"`
	PackageEdges     []PackageDependencyEdge  `json:"package_edges,omitempty"`
	Cycles           []DependencyCycle        `json:"cycles,omitempty"`
	FileCycles       []DependencyCycle        `json:"file_cycles,omitempty"`
	Layers           []StructureLayer         `json:"layers,omitempty"`
	Hubs             []StructureHubRecord     `json:"hubs,omitempty"`
	Orphans          []string                 `json:"orphans,omitempty"`
	TestTopology     StructureTestTopology    `json:"test_topology,omitempty"`
	Hotspots         []StructureHotspot       `json:"hotspots,omitempty"`
	ChangeCoupling   []ChangeCouplingEdge     `json:"change_coupling,omitempty"`
	GitAvailable     bool                     `json:"git_available,omitempty"`
	GitWindowDays    int                      `json:"git_window_days,omitempty"`
	HealthScore      int                      `json:"health_score,omitempty"`
	HealthGrade      string                   `json:"health_grade,omitempty"`
	HealthFindings   []string                 `json:"health_findings,omitempty"`
	Notes            []string                 `json:"notes,omitempty"`
}

// PackageStructureRecord captures per-directory (package) structural coupling.
type PackageStructureRecord struct {
	Path             string   `json:"path"`
	Label            string   `json:"label"`
	FileCount        int      `json:"file_count,omitempty"`
	LineCount        int      `json:"line_count,omitempty"`
	AfferentCoupling int      `json:"afferent_coupling"`
	EfferentCoupling int      `json:"efferent_coupling"`
	Instability      float64  `json:"instability"`
	InternalEdges    int      `json:"internal_edges,omitempty"`
	Layer            int      `json:"layer"`
	InCycle          bool     `json:"in_cycle,omitempty"`
	Rank             float64  `json:"rank,omitempty"`
	HasTests         bool     `json:"has_tests,omitempty"`
	RiskSignals      []string `json:"risk_signals,omitempty"`
}

// PackageDependencyEdge is a directory-to-directory dependency aggregated from
// the file-level import graph, weighted by the number of underlying imports.
type PackageDependencyEdge struct {
	Source     string   `json:"source"`
	Target     string   `json:"target"`
	Weight     int      `json:"weight"`
	SampleFrom []string `json:"sample_from,omitempty"`
}

// DependencyCycle is a strongly-connected component (size >= 2) in either the
// package graph or the file graph. BreakEdges names concrete import edges whose
// removal would break the tangle.
type DependencyCycle struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Size       int      `json:"size"`
	Members    []string `json:"members"`
	BreakEdges []string `json:"break_edges,omitempty"`
}

// StructureLayer is one level of the acyclic package condensation, foundation
// (no outgoing dependencies) first.
type StructureLayer struct {
	Level    int      `json:"level"`
	Packages []string `json:"packages"`
}

// StructureHubRecord ranks the architecturally central packages.
type StructureHubRecord struct {
	Path             string  `json:"path"`
	Label            string  `json:"label"`
	AfferentCoupling int     `json:"afferent_coupling"`
	EfferentCoupling int     `json:"efferent_coupling"`
	Rank             float64 `json:"rank"`
	Role             string  `json:"role"`
}

// StructureTestTopology maps tests to the packages they cover.
type StructureTestTopology struct {
	TotalTestFiles    int              `json:"total_test_files,omitempty"`
	CodePackages      int              `json:"code_packages,omitempty"`
	PackagesWithTests int              `json:"packages_with_tests,omitempty"`
	CoverageRatio     float64          `json:"coverage_ratio,omitempty"`
	UntestedPackages  []string         `json:"untested_packages,omitempty"`
	Links             []TestSourceLink `json:"links,omitempty"`
}

// TestSourceLink pairs a test file with the source file it most likely covers.
type TestSourceLink struct {
	Test   string `json:"test"`
	Source string `json:"source"`
}

// StructureHotspot is a churn-times-size change hotspot from git history.
type StructureHotspot struct {
	Path        string `json:"path"`
	Commits     int    `json:"commits"`
	LineCount   int    `json:"line_count,omitempty"`
	Score       int    `json:"score"`
	LastChanged string `json:"last_changed,omitempty"`
	InCycle     bool   `json:"in_cycle,omitempty"`
}

// ChangeCouplingEdge is a temporal (co-change) coupling between two files that
// have no static import edge between them - hidden structural coupling.
type ChangeCouplingEdge struct {
	Source        string `json:"source"`
	Target        string `json:"target"`
	SharedChanges int    `json:"shared_changes"`
	Confidence    string `json:"confidence,omitempty"`
}

// buildProjectStructureMetrics is the top-level entry point. gitRoot may be
// empty; when set, git history metrics are added. ctx bounds git execution.
func buildProjectStructureMetrics(ctx context.Context, snapshot ProjectSnapshot, gitRoot string) ProjectStructureMetrics {
	metrics := ProjectStructureMetrics{
		GeneratedAt:   snapshot.GeneratedAt,
		FileCount:     snapshot.TotalFiles,
		GitWindowDays: structureMetricsGitWindowDays,
	}
	if metrics.GeneratedAt.IsZero() {
		metrics.GeneratedAt = time.Now()
	}

	fileGraph := structureInternalFileGraph(snapshot)
	metrics.FileEdgeCount = structureCountEdges(fileGraph)

	packages, packageEdges, adjacency := structureBuildPackageGraph(snapshot, fileGraph)
	metrics.PackageEdges = packageEdges
	metrics.PackageEdgeCount = len(packageEdges)

	cycles := structurePackageCycles(adjacency, packageEdges)
	metrics.Cycles = cycles
	inCycle := structureCycleMembership(cycles)

	layerByPackage := structureAssignLayers(adjacency, cycles)
	rankByPackage := structurePageRank(adjacency)

	testTopology, packagesWithTests := structureTestTopologyFor(snapshot)
	metrics.TestTopology = testTopology

	structurePopulatePackageRecords(&packages, adjacency, layerByPackage, rankByPackage, inCycle, packagesWithTests)
	metrics.Packages = packages
	metrics.PackageCount = len(packages)
	metrics.Layers = structureLayersFromAssignment(packages, layerByPackage)
	metrics.Hubs = structureHubs(packages)
	metrics.Orphans = structureOrphanFiles(snapshot, fileGraph)

	if snapshot.TotalFiles <= structureMetricsFileCycleMaxNode {
		metrics.FileCycles = structureFileCycles(fileGraph)
	} else {
		metrics.Notes = append(metrics.Notes, fmt.Sprintf("file-level cycle detection skipped: %d files exceed the %d-node budget", snapshot.TotalFiles, structureMetricsFileCycleMaxNode))
	}

	if strings.TrimSpace(gitRoot) != "" {
		history, ok := structureCollectGitHistory(ctx, snapshot, gitRoot)
		if ok {
			metrics.GitAvailable = true
			metrics.Hotspots = structureHotspots(snapshot, history, inCyclePathsForFiles(snapshot, cycles))
			metrics.ChangeCoupling = structureChangeCoupling(history, fileGraph)
		} else {
			metrics.Notes = append(metrics.Notes, "git history metrics unavailable (not a git repository or git query failed)")
		}
	} else {
		metrics.Notes = append(metrics.Notes, "git history metrics skipped (workspace is not inside a git repository)")
	}

	metrics.HealthScore, metrics.HealthGrade, metrics.HealthFindings = structureHealth(metrics)
	metrics.Notes = analysisUniqueStrings(metrics.Notes)
	return metrics
}

func projectStructureMetricsHasData(metrics ProjectStructureMetrics) bool {
	return len(metrics.Packages) > 0 ||
		len(metrics.PackageEdges) > 0 ||
		len(metrics.Cycles) > 0 ||
		len(metrics.Hotspots) > 0 ||
		len(metrics.Orphans) > 0
}

// structureInternalFileGraph returns the import graph restricted to edges where
// both endpoints are scanned project files (no external/unresolved targets).
func structureInternalFileGraph(snapshot ProjectSnapshot) map[string][]string {
	graph := map[string][]string{}
	for source, targets := range snapshot.ImportGraph {
		if _, ok := snapshot.FilesByPath[source]; !ok {
			continue
		}
		seen := map[string]struct{}{}
		out := []string{}
		for _, target := range targets {
			if target == source {
				continue
			}
			if _, ok := snapshot.FilesByPath[target]; !ok {
				continue
			}
			if _, ok := seen[target]; ok {
				continue
			}
			seen[target] = struct{}{}
			out = append(out, target)
		}
		if len(out) > 0 {
			sort.Strings(out)
			graph[source] = out
		}
	}
	return graph
}

func structureCountEdges(graph map[string][]string) int {
	total := 0
	for _, targets := range graph {
		total += len(targets)
	}
	return total
}

func structurePackageOf(snapshot ProjectSnapshot, path string) string {
	if file, ok := snapshot.FilesByPath[path]; ok {
		return file.Directory
	}
	return analysisDocDir(path)
}

func structurePackageLabel(pkg string) string {
	pkg = strings.TrimSpace(pkg)
	if pkg == "" || pkg == "." {
		return structureMetricsRootLabel
	}
	return pkg
}

// structureBuildPackageGraph aggregates the file graph into a directory graph.
// It returns the package records (paths only, metrics filled later), the sorted
// weighted package edges, and the package adjacency map.
func structureBuildPackageGraph(snapshot ProjectSnapshot, fileGraph map[string][]string) ([]PackageStructureRecord, []PackageDependencyEdge, map[string][]string) {
	packageFileCount := map[string]int{}
	packageLineCount := map[string]int{}
	for _, file := range snapshot.Files {
		packageFileCount[file.Directory]++
		packageLineCount[file.Directory] += file.LineCount
	}

	type edgeAccum struct {
		weight  int
		samples []string
	}
	edgeMap := map[string]map[string]*edgeAccum{}
	internalEdges := map[string]int{}
	for source, targets := range fileGraph {
		sourcePkg := structurePackageOf(snapshot, source)
		for _, target := range targets {
			targetPkg := structurePackageOf(snapshot, target)
			if sourcePkg == targetPkg {
				internalEdges[sourcePkg]++
				continue
			}
			if edgeMap[sourcePkg] == nil {
				edgeMap[sourcePkg] = map[string]*edgeAccum{}
			}
			accum := edgeMap[sourcePkg][targetPkg]
			if accum == nil {
				accum = &edgeAccum{}
				edgeMap[sourcePkg][targetPkg] = accum
			}
			accum.weight++
			if len(accum.samples) < 3 {
				accum.samples = append(accum.samples, analysisDocSlashPath(source))
			}
		}
	}

	packageSet := map[string]struct{}{}
	for pkg := range packageFileCount {
		packageSet[pkg] = struct{}{}
	}
	for source, targets := range edgeMap {
		packageSet[source] = struct{}{}
		for target := range targets {
			packageSet[target] = struct{}{}
		}
	}

	adjacency := map[string][]string{}
	edges := []PackageDependencyEdge{}
	for source, targets := range edgeMap {
		neighbors := make([]string, 0, len(targets))
		for target, accum := range targets {
			neighbors = append(neighbors, target)
			edges = append(edges, PackageDependencyEdge{
				Source:     structurePackageLabel(source),
				Target:     structurePackageLabel(target),
				Weight:     accum.weight,
				SampleFrom: append([]string(nil), accum.samples...),
			})
		}
		sort.Strings(neighbors)
		adjacency[source] = neighbors
	}
	sort.Slice(edges, func(i int, j int) bool {
		if edges[i].Weight != edges[j].Weight {
			return edges[i].Weight > edges[j].Weight
		}
		if edges[i].Source != edges[j].Source {
			return edges[i].Source < edges[j].Source
		}
		return edges[i].Target < edges[j].Target
	})

	packages := make([]PackageStructureRecord, 0, len(packageSet))
	for pkg := range packageSet {
		packages = append(packages, PackageStructureRecord{
			Path:          pkg,
			Label:         structurePackageLabel(pkg),
			FileCount:     packageFileCount[pkg],
			LineCount:     packageLineCount[pkg],
			InternalEdges: internalEdges[pkg],
		})
	}
	sort.Slice(packages, func(i int, j int) bool {
		return packages[i].Path < packages[j].Path
	})
	return packages, edges, adjacency
}

// structureTarjanSCC returns the strongly connected components of the directed
// graph described by nodes+adjacency. Each returned component is sorted; the
// order of components is deterministic (sorted by first member).
func structureTarjanSCC(nodes []string, adjacency map[string][]string) [][]string {
	index := 0
	indices := map[string]int{}
	lowlink := map[string]int{}
	onStack := map[string]bool{}
	stack := []string{}
	components := [][]string{}

	var strongConnect func(node string)
	strongConnect = func(node string) {
		indices[node] = index
		lowlink[node] = index
		index++
		stack = append(stack, node)
		onStack[node] = true
		for _, next := range adjacency[node] {
			if _, seen := indices[next]; !seen {
				strongConnect(next)
				if lowlink[next] < lowlink[node] {
					lowlink[node] = lowlink[next]
				}
			} else if onStack[next] {
				if indices[next] < lowlink[node] {
					lowlink[node] = indices[next]
				}
			}
		}
		if lowlink[node] == indices[node] {
			component := []string{}
			for {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[top] = false
				component = append(component, top)
				if top == node {
					break
				}
			}
			sort.Strings(component)
			components = append(components, component)
		}
	}

	ordered := append([]string(nil), nodes...)
	sort.Strings(ordered)
	for _, node := range ordered {
		if _, seen := indices[node]; !seen {
			strongConnect(node)
		}
	}
	sort.Slice(components, func(i int, j int) bool {
		if len(components[i]) == 0 || len(components[j]) == 0 {
			return len(components[i]) > len(components[j])
		}
		return components[i][0] < components[j][0]
	})
	return components
}

func structurePackageCycles(adjacency map[string][]string, edges []PackageDependencyEdge) []DependencyCycle {
	nodes := structureNodesFromAdjacency(adjacency)
	components := structureTarjanSCC(nodes, adjacency)
	cycles := []DependencyCycle{}
	for _, component := range components {
		if len(component) < 2 {
			continue
		}
		memberSet := map[string]struct{}{}
		for _, member := range component {
			memberSet[member] = struct{}{}
		}
		breakEdges := []string{}
		for _, edge := range edges {
			source := structureUnlabel(edge.Source)
			target := structureUnlabel(edge.Target)
			if _, ok := memberSet[source]; !ok {
				continue
			}
			if _, ok := memberSet[target]; !ok {
				continue
			}
			breakEdges = append(breakEdges, fmt.Sprintf("%s -> %s (x%d)", edge.Source, edge.Target, edge.Weight))
		}
		labels := make([]string, 0, len(component))
		for _, member := range component {
			labels = append(labels, structurePackageLabel(member))
		}
		cycles = append(cycles, DependencyCycle{
			ID:         "pkgcycle-" + strconv.Itoa(len(cycles)+1),
			Kind:       "package",
			Size:       len(component),
			Members:    labels,
			BreakEdges: limitStrings(breakEdges, 8),
		})
	}
	sort.Slice(cycles, func(i int, j int) bool {
		if cycles[i].Size != cycles[j].Size {
			return cycles[i].Size > cycles[j].Size
		}
		return strings.Join(cycles[i].Members, ",") < strings.Join(cycles[j].Members, ",")
	})
	for i := range cycles {
		cycles[i].ID = "pkgcycle-" + strconv.Itoa(i+1)
	}
	return cycles
}

func structureFileCycles(fileGraph map[string][]string) []DependencyCycle {
	nodes := structureNodesFromAdjacency(fileGraph)
	components := structureTarjanSCC(nodes, fileGraph)
	cycles := []DependencyCycle{}
	for _, component := range components {
		if len(component) < 2 {
			continue
		}
		members := make([]string, 0, len(component))
		for _, member := range component {
			members = append(members, analysisDocSlashPath(member))
		}
		cycles = append(cycles, DependencyCycle{
			Kind:    "file",
			Size:    len(component),
			Members: limitStrings(members, 20),
		})
	}
	sort.Slice(cycles, func(i int, j int) bool {
		if cycles[i].Size != cycles[j].Size {
			return cycles[i].Size > cycles[j].Size
		}
		return strings.Join(cycles[i].Members, ",") < strings.Join(cycles[j].Members, ",")
	})
	if len(cycles) > 20 {
		cycles = cycles[:20]
	}
	for i := range cycles {
		cycles[i].ID = "filecycle-" + strconv.Itoa(i+1)
	}
	return cycles
}

func structureCycleMembership(cycles []DependencyCycle) map[string]struct{} {
	members := map[string]struct{}{}
	for _, cycle := range cycles {
		for _, member := range cycle.Members {
			members[structureUnlabel(member)] = struct{}{}
		}
	}
	return members
}

// structureAssignLayers computes a layer index per package on the acyclic
// condensation of the package graph. Foundation packages (no outgoing
// dependency) are layer 0; a package that depends on layer-k packages is at
// layer k+1. Packages inside the same cycle share the cycle's layer.
func structureAssignLayers(adjacency map[string][]string, cycles []DependencyCycle) map[string]int {
	componentID := map[string]int{}
	componentMembers := map[int][]string{}
	nextID := 0
	assign := func(node string) int {
		if id, ok := componentID[node]; ok {
			return id
		}
		id := nextID
		nextID++
		componentID[node] = id
		componentMembers[id] = []string{node}
		return id
	}
	for _, cycle := range cycles {
		id := nextID
		nextID++
		for _, member := range cycle.Members {
			node := structureUnlabel(member)
			componentID[node] = id
			componentMembers[id] = append(componentMembers[id], node)
		}
	}
	nodes := structureNodesFromAdjacency(adjacency)
	for _, node := range nodes {
		assign(node)
	}

	condensation := map[int]map[int]struct{}{}
	for source, targets := range adjacency {
		sourceComp := componentID[source]
		for _, target := range targets {
			targetComp, ok := componentID[target]
			if !ok {
				targetComp = assign(target)
			}
			if sourceComp == targetComp {
				continue
			}
			if condensation[sourceComp] == nil {
				condensation[sourceComp] = map[int]struct{}{}
			}
			condensation[sourceComp][targetComp] = struct{}{}
		}
	}

	layerByComp := map[int]int{}
	var depth func(comp int, visiting map[int]bool) int
	depth = func(comp int, visiting map[int]bool) int {
		if value, ok := layerByComp[comp]; ok {
			return value
		}
		if visiting[comp] {
			return 0
		}
		visiting[comp] = true
		best := 0
		for next := range condensation[comp] {
			candidate := depth(next, visiting) + 1
			if candidate > best {
				best = candidate
			}
		}
		delete(visiting, comp)
		layerByComp[comp] = best
		return best
	}
	comps := make([]int, 0, len(componentMembers))
	for comp := range componentMembers {
		comps = append(comps, comp)
	}
	sort.Ints(comps)
	for _, comp := range comps {
		depth(comp, map[int]bool{})
	}

	layers := map[string]int{}
	for node, comp := range componentID {
		layers[node] = layerByComp[comp]
	}
	return layers
}

// structurePageRank runs a fixed-round PageRank on the package graph. An edge
// source->target (source depends on target) contributes rank to the target, so
// heavily depended-upon foundation packages score highest.
func structurePageRank(adjacency map[string][]string) map[string]float64 {
	nodes := structureNodesFromAdjacency(adjacency)
	count := len(nodes)
	if count == 0 {
		return map[string]float64{}
	}
	rank := map[string]float64{}
	initial := 1.0 / float64(count)
	for _, node := range nodes {
		rank[node] = initial
	}
	outDegree := map[string]int{}
	for _, node := range nodes {
		outDegree[node] = len(adjacency[node])
	}
	base := (1.0 - structureMetricsPageRankDamping) / float64(count)
	for round := 0; round < structureMetricsPageRankRounds; round++ {
		next := map[string]float64{}
		dangling := 0.0
		for _, node := range nodes {
			if outDegree[node] == 0 {
				dangling += rank[node]
			}
		}
		danglingShare := structureMetricsPageRankDamping * dangling / float64(count)
		for _, node := range nodes {
			next[node] = base + danglingShare
		}
		for _, node := range nodes {
			degree := outDegree[node]
			if degree == 0 {
				continue
			}
			share := structureMetricsPageRankDamping * rank[node] / float64(degree)
			for _, target := range adjacency[node] {
				next[target] += share
			}
		}
		rank = next
	}
	return rank
}

func structurePopulatePackageRecords(packages *[]PackageStructureRecord, adjacency map[string][]string, layers map[string]int, ranks map[string]float64, inCycle map[string]struct{}, packagesWithTests map[string]struct{}) {
	afferent := map[string]map[string]struct{}{}
	efferent := map[string]map[string]struct{}{}
	for source, targets := range adjacency {
		if efferent[source] == nil {
			efferent[source] = map[string]struct{}{}
		}
		for _, target := range targets {
			efferent[source][target] = struct{}{}
			if afferent[target] == nil {
				afferent[target] = map[string]struct{}{}
			}
			afferent[target][source] = struct{}{}
		}
	}
	for i := range *packages {
		record := &(*packages)[i]
		ca := len(afferent[record.Path])
		ce := len(efferent[record.Path])
		record.AfferentCoupling = ca
		record.EfferentCoupling = ce
		if ca+ce > 0 {
			record.Instability = math.Round(float64(ce)/float64(ca+ce)*100) / 100
		}
		record.Layer = layers[record.Path]
		if _, ok := inCycle[record.Path]; ok {
			record.InCycle = true
			record.RiskSignals = append(record.RiskSignals, "participates in a circular dependency")
		}
		if rank, ok := ranks[record.Path]; ok {
			record.Rank = math.Round(rank*10000) / 10000
		}
		if _, ok := packagesWithTests[record.Path]; ok {
			record.HasTests = true
		}
		if ca >= 4 && ce >= 4 {
			record.RiskSignals = append(record.RiskSignals, "high fan-in and fan-out chokepoint")
		}
		if ca >= 3 && ce == 0 && record.Instability == 0 {
			record.RiskSignals = append(record.RiskSignals, "stable foundation package (avoid breaking changes)")
		}
		record.RiskSignals = analysisUniqueStrings(record.RiskSignals)
	}
}

func structureLayersFromAssignment(packages []PackageStructureRecord, layers map[string]int) []StructureLayer {
	byLevel := map[int][]string{}
	for _, record := range packages {
		level := layers[record.Path]
		byLevel[level] = append(byLevel[level], record.Label)
	}
	levels := make([]int, 0, len(byLevel))
	for level := range byLevel {
		levels = append(levels, level)
	}
	sort.Ints(levels)
	out := []StructureLayer{}
	for _, level := range levels {
		members := byLevel[level]
		sort.Strings(members)
		out = append(out, StructureLayer{
			Level:    level,
			Packages: members,
		})
	}
	return out
}

func structureHubs(packages []PackageStructureRecord) []StructureHubRecord {
	hubs := []StructureHubRecord{}
	for _, record := range packages {
		if record.AfferentCoupling == 0 && record.EfferentCoupling == 0 {
			continue
		}
		role := "connector"
		switch {
		case record.AfferentCoupling >= 4 && record.EfferentCoupling >= 4:
			role = "bottleneck"
		case record.AfferentCoupling >= 3 && record.EfferentCoupling <= 1:
			role = "foundation hub"
		case record.EfferentCoupling >= 4 && record.AfferentCoupling <= 1:
			role = "aggregator"
		}
		hubs = append(hubs, StructureHubRecord{
			Path:             record.Path,
			Label:            record.Label,
			AfferentCoupling: record.AfferentCoupling,
			EfferentCoupling: record.EfferentCoupling,
			Rank:             record.Rank,
			Role:             role,
		})
	}
	sort.Slice(hubs, func(i int, j int) bool {
		left := hubs[i].AfferentCoupling + hubs[i].EfferentCoupling
		right := hubs[j].AfferentCoupling + hubs[j].EfferentCoupling
		if left != right {
			return left > right
		}
		if hubs[i].Rank != hubs[j].Rank {
			return hubs[i].Rank > hubs[j].Rank
		}
		return hubs[i].Label < hubs[j].Label
	})
	if len(hubs) > 12 {
		hubs = hubs[:12]
	}
	return hubs
}

func structureOrphanFiles(snapshot ProjectSnapshot, fileGraph map[string][]string) []string {
	if len(fileGraph) == 0 {
		return nil
	}
	referenced := map[string]struct{}{}
	for _, targets := range fileGraph {
		for _, target := range targets {
			referenced[target] = struct{}{}
		}
	}
	orphans := []string{}
	for _, file := range snapshot.Files {
		if !structureImportParsedExtension(file.Extension) {
			continue
		}
		if file.IsEntrypoint || file.IsManifest || analysisIsTestFile(file.Path) {
			continue
		}
		if structureLooksLikeMainFile(file.Path) {
			continue
		}
		if _, ok := referenced[file.Path]; ok {
			continue
		}
		orphans = append(orphans, analysisDocSlashPath(file.Path))
	}
	sort.Strings(orphans)
	if len(orphans) > 40 {
		orphans = orphans[:40]
	}
	return orphans
}

func structureTestTopologyFor(snapshot ProjectSnapshot) (StructureTestTopology, map[string]struct{}) {
	topology := StructureTestTopology{}
	packagesWithTests := map[string]struct{}{}
	codePackages := map[string]struct{}{}
	testFiles := []ScannedFile{}
	sourceByBase := map[string][]string{}
	for _, file := range snapshot.Files {
		if !structureCodeExtension(file.Extension) {
			continue
		}
		if analysisIsTestFile(file.Path) {
			testFiles = append(testFiles, file)
			packagesWithTests[file.Directory] = struct{}{}
			continue
		}
		codePackages[file.Directory] = struct{}{}
		base := strings.ToLower(strings.TrimSuffix(filepath.Base(file.Path), file.Extension))
		sourceByBase[base] = append(sourceByBase[base], file.Path)
	}
	topology.TotalTestFiles = len(testFiles)
	topology.CodePackages = len(codePackages)
	withTests := 0
	for pkg := range codePackages {
		if _, ok := packagesWithTests[pkg]; ok {
			withTests++
		}
	}
	topology.PackagesWithTests = withTests
	if len(codePackages) > 0 {
		topology.CoverageRatio = math.Round(float64(withTests)/float64(len(codePackages))*100) / 100
	}
	untested := []string{}
	for pkg := range codePackages {
		if _, ok := packagesWithTests[pkg]; !ok {
			untested = append(untested, structurePackageLabel(pkg))
		}
	}
	sort.Strings(untested)
	topology.UntestedPackages = limitStrings(untested, 40)

	links := []TestSourceLink{}
	sort.Slice(testFiles, func(i int, j int) bool {
		return testFiles[i].Path < testFiles[j].Path
	})
	for _, test := range testFiles {
		stem := structureTestSourceStem(test.Path, test.Extension)
		if stem == "" {
			continue
		}
		candidates := sourceByBase[stem]
		best := structureBestSourceMatch(test.Directory, candidates)
		if best == "" {
			continue
		}
		links = append(links, TestSourceLink{
			Test:   analysisDocSlashPath(test.Path),
			Source: analysisDocSlashPath(best),
		})
	}
	topology.Links = links
	if len(topology.Links) > 60 {
		topology.Links = topology.Links[:60]
	}
	return topology, packagesWithTests
}

func structureBestSourceMatch(testDir string, candidates []string) string {
	best := ""
	for _, candidate := range candidates {
		if best == "" {
			best = candidate
			continue
		}
		if analysisDocDir(candidate) == testDir && analysisDocDir(best) != testDir {
			best = candidate
		}
	}
	return best
}

func structureHealth(metrics ProjectStructureMetrics) (int, string, []string) {
	score := 100
	findings := []string{}
	if len(metrics.Cycles) > 0 {
		penalty := analysisMinInt(30, 6*len(metrics.Cycles))
		score -= penalty
		findings = append(findings, fmt.Sprintf("%d package-level circular dependency group(s) detected (-%d)", len(metrics.Cycles), penalty))
	}
	if len(metrics.FileCycles) > 0 {
		penalty := analysisMinInt(15, 3*len(metrics.FileCycles))
		score -= penalty
		findings = append(findings, fmt.Sprintf("%d file-level import cycle(s) detected (-%d)", len(metrics.FileCycles), penalty))
	}
	if len(metrics.Orphans) > 0 {
		penalty := analysisMinInt(10, len(metrics.Orphans))
		score -= penalty
		findings = append(findings, fmt.Sprintf("%d orphan file(s) with no inbound imports (-%d)", len(metrics.Orphans), penalty))
	}
	untestedHubs := structureUntestedHubCount(metrics)
	if untestedHubs > 0 {
		penalty := analysisMinInt(20, 3*untestedHubs)
		score -= penalty
		findings = append(findings, fmt.Sprintf("%d high-coupling package(s) without tests (-%d)", untestedHubs, penalty))
	}
	if metrics.GitAvailable {
		hotInCycle := 0
		for _, hotspot := range metrics.Hotspots {
			if hotspot.InCycle {
				hotInCycle++
			}
		}
		if hotInCycle > 0 {
			penalty := analysisMinInt(15, 5*hotInCycle)
			score -= penalty
			findings = append(findings, fmt.Sprintf("%d change hotspot(s) sit inside a dependency cycle (-%d)", hotInCycle, penalty))
		}
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	if len(findings) == 0 {
		findings = append(findings, "no structural anti-patterns detected in the dependency graph")
	}
	return score, structureHealthGrade(score), findings
}

func structureUntestedHubCount(metrics ProjectStructureMetrics) int {
	tested := map[string]struct{}{}
	for _, record := range metrics.Packages {
		if record.HasTests {
			tested[record.Path] = struct{}{}
		}
	}
	count := 0
	for _, hub := range metrics.Hubs {
		if hub.AfferentCoupling < 3 {
			continue
		}
		if _, ok := tested[hub.Path]; ok {
			continue
		}
		count++
	}
	return count
}

func structureHealthGrade(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 80:
		return "B"
	case score >= 70:
		return "C"
	case score >= 55:
		return "D"
	default:
		return "F"
	}
}

func structureNodesFromAdjacency(adjacency map[string][]string) []string {
	set := map[string]struct{}{}
	for source, targets := range adjacency {
		set[source] = struct{}{}
		for _, target := range targets {
			set[target] = struct{}{}
		}
	}
	nodes := make([]string, 0, len(set))
	for node := range set {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	return nodes
}

func structureUnlabel(label string) string {
	if label == structureMetricsRootLabel {
		return ""
	}
	return label
}

func structureCodeExtension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".inl",
		".cs", ".py", ".rs", ".js", ".jsx", ".ts", ".tsx", ".java", ".kt", ".m", ".mm":
		return true
	default:
		return false
	}
}

func structureImportParsedExtension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".inl",
		".cs", ".py", ".rs", ".js", ".jsx", ".ts", ".tsx":
		return true
	default:
		return false
	}
}

func structureLooksLikeMainFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "main.go", "main.cpp", "main.c", "main.rs", "main.py", "__main__.py", "index.js", "index.ts", "program.cs":
		return true
	}
	return strings.HasPrefix(base, "main.") || strings.HasPrefix(base, "cmd")
}

func structureTestSourceStem(path string, ext string) string {
	base := strings.ToLower(filepath.Base(path))
	stem := strings.TrimSuffix(base, strings.ToLower(ext))
	stem = strings.TrimSuffix(stem, "_test")
	stem = strings.TrimSuffix(stem, ".test")
	stem = strings.TrimSuffix(stem, ".spec")
	stem = strings.TrimSuffix(stem, "test")
	stem = strings.TrimSuffix(stem, "tests")
	stem = strings.TrimSuffix(stem, "_spec")
	stem = strings.TrimPrefix(stem, "test_")
	return strings.TrimSpace(stem)
}

func inCyclePathsForFiles(snapshot ProjectSnapshot, cycles []DependencyCycle) map[string]struct{} {
	packageSet := structureCycleMembership(cycles)
	files := map[string]struct{}{}
	for _, file := range snapshot.Files {
		if _, ok := packageSet[file.Directory]; ok {
			files[file.Path] = struct{}{}
		}
	}
	return files
}
