package main

// Markdown rendering for the deterministic project-structure metrics. Two
// entry points: a full report embedded in STRUCTURE_DIAGRAMS.md and a compact
// section injected into the map-mode final document.

import (
	"fmt"
	"sort"
	"strings"
)

// renderProjectStructureMetricsReport renders the full deterministic structure
// report used inside the generated STRUCTURE_DIAGRAMS.md document.
func renderProjectStructureMetricsReport(metrics ProjectStructureMetrics) string {
	if !projectStructureMetricsHasData(metrics) {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Deterministic Structure Metrics\n\n")
	fmt.Fprintf(&b, "Code-derived from the import graph%s; no model inference. ", structureGitScopeSuffix(metrics))
	fmt.Fprintf(&b, "Packages: %d, package dependencies: %d, file import edges: %d.\n\n",
		metrics.PackageCount, metrics.PackageEdgeCount, metrics.FileEdgeCount)

	structureWriteHealth(&b, metrics)
	structureWriteCycles(&b, metrics, 12)
	structureWriteLayers(&b, metrics, 12)
	structureWriteHubs(&b, metrics, 12)
	structureWriteCouplingTable(&b, metrics, 24)
	structureWritePackageMermaid(&b, metrics, 24)
	structureWriteHotspots(&b, metrics, 20)
	structureWriteChangeCoupling(&b, metrics, 20)
	structureWriteTestTopology(&b, metrics, 24)
	structureWriteOrphans(&b, metrics, 24)
	structureWriteNotes(&b, metrics)
	return strings.TrimSpace(b.String()) + "\n"
}

// renderProjectStructureMetricsCompact renders a tighter section for inclusion
// in the primary map-mode final document.
func renderProjectStructureMetricsCompact(metrics ProjectStructureMetrics) string {
	if !projectStructureMetricsHasData(metrics) {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Project Structure Metrics\n\n")
	fmt.Fprintf(&b, "Deterministic, code-derived from the dependency graph%s.\n\n", structureGitScopeSuffix(metrics))
	fmt.Fprintf(&b, "- Structure health: %d/100 (grade %s)\n", metrics.HealthScore, firstNonBlankAnalysisString(metrics.HealthGrade, "n/a"))
	fmt.Fprintf(&b, "- Packages: %d; package dependencies: %d; circular groups: %d; orphan files: %d\n",
		metrics.PackageCount, metrics.PackageEdgeCount, len(metrics.Cycles), len(metrics.Orphans))
	if metrics.TestTopology.CodePackages > 0 {
		fmt.Fprintf(&b, "- Package test coverage: %d/%d packages have tests (%.0f%%)\n",
			metrics.TestTopology.PackagesWithTests, metrics.TestTopology.CodePackages, metrics.TestTopology.CoverageRatio*100)
	}
	for _, finding := range limitStrings(metrics.HealthFindings, 4) {
		fmt.Fprintf(&b, "- %s\n", finding)
	}
	if len(metrics.Cycles) > 0 {
		fmt.Fprintf(&b, "\n**Circular dependencies (fix first):**\n\n")
		for _, cycle := range limitDependencyCycles(metrics.Cycles, 4) {
			fmt.Fprintf(&b, "- %s\n", strings.Join(limitStrings(cycle.Members, 8), " <-> "))
		}
	}
	if len(metrics.Hubs) > 0 {
		fmt.Fprintf(&b, "\n**Architectural hubs (Ca=depended-on, Ce=depends-on):**\n\n")
		for _, hub := range limitHubRecords(metrics.Hubs, 6) {
			fmt.Fprintf(&b, "- `%s` [%s]: Ca=%d Ce=%d instability=%s\n",
				hub.Label, hub.Role, hub.AfferentCoupling, hub.EfferentCoupling, structureInstabilityForHub(metrics, hub))
		}
	}
	if len(metrics.Hotspots) > 0 {
		fmt.Fprintf(&b, "\n**Top change hotspots (churn x size, last 90d):**\n\n")
		for _, hotspot := range limitHotspots(metrics.Hotspots, 5) {
			suffix := ""
			if hotspot.InCycle {
				suffix = " [in cycle]"
			}
			fmt.Fprintf(&b, "- `%s`: %d commits, %d lines, score %d%s\n",
				hotspot.Path, hotspot.Commits, hotspot.LineCount, hotspot.Score, suffix)
		}
	}
	fmt.Fprintf(&b, "\nSee `STRUCTURE_DIAGRAMS.md` for the full dependency structure matrix, layer map, and package graph.\n")
	return strings.TrimSpace(b.String()) + "\n"
}

func structureGitScopeSuffix(metrics ProjectStructureMetrics) string {
	if metrics.GitAvailable {
		return fmt.Sprintf(" plus %d-day git history", metrics.GitWindowDays)
	}
	return ""
}

func structureWriteHealth(b *strings.Builder, metrics ProjectStructureMetrics) {
	fmt.Fprintf(b, "### Structure Health\n\n")
	fmt.Fprintf(b, "- Score: %d/100 (grade %s)\n", metrics.HealthScore, firstNonBlankAnalysisString(metrics.HealthGrade, "n/a"))
	for _, finding := range metrics.HealthFindings {
		fmt.Fprintf(b, "- %s\n", finding)
	}
	fmt.Fprintf(b, "\n")
}

func structureWriteCycles(b *strings.Builder, metrics ProjectStructureMetrics, limit int) {
	fmt.Fprintf(b, "### Circular Dependencies\n\n")
	if len(metrics.Cycles) == 0 && len(metrics.FileCycles) == 0 {
		fmt.Fprintf(b, "No circular dependencies were detected in the package or file import graph.\n\n")
		return
	}
	if len(metrics.Cycles) > 0 {
		fmt.Fprintf(b, "Package-level tangles (break these first):\n\n")
		for _, cycle := range limitDependencyCycles(metrics.Cycles, limit) {
			fmt.Fprintf(b, "- **%s** (%d packages): %s\n", cycle.ID, cycle.Size, strings.Join(limitStrings(cycle.Members, 12), " <-> "))
			for _, edge := range limitStrings(cycle.BreakEdges, 4) {
				fmt.Fprintf(b, "  - back-edge: %s\n", edge)
			}
		}
		fmt.Fprintf(b, "\n")
	}
	if len(metrics.FileCycles) > 0 {
		fmt.Fprintf(b, "File-level import cycles:\n\n")
		for _, cycle := range limitDependencyCycles(metrics.FileCycles, limit) {
			fmt.Fprintf(b, "- (%d files) %s\n", cycle.Size, strings.Join(limitStrings(cycle.Members, 10), " -> "))
		}
		fmt.Fprintf(b, "\n")
	}
}

func structureWriteLayers(b *strings.Builder, metrics ProjectStructureMetrics, limit int) {
	if len(metrics.Layers) == 0 {
		return
	}
	fmt.Fprintf(b, "### Architecture Layers\n\n")
	fmt.Fprintf(b, "Foundation (layer 0, depends on nothing) to top. Dependencies should point downward.\n\n")
	for _, layer := range metrics.Layers {
		fmt.Fprintf(b, "- Layer %d: %s\n", layer.Level, formatInlineCodeList(limitStrings(layer.Packages, limit), limit))
	}
	fmt.Fprintf(b, "\n")
}

func structureWriteHubs(b *strings.Builder, metrics ProjectStructureMetrics, limit int) {
	if len(metrics.Hubs) == 0 {
		return
	}
	fmt.Fprintf(b, "### Architectural Hubs And Bottlenecks\n\n")
	fmt.Fprintf(b, "| Package | Role | Ca (in) | Ce (out) | Rank |\n")
	fmt.Fprintf(b, "| --- | --- | --- | --- | --- |\n")
	for _, hub := range limitHubRecords(metrics.Hubs, limit) {
		fmt.Fprintf(b, "| `%s` | %s | %d | %d | %.4f |\n",
			analysisMarkdownCell(hub.Label),
			analysisMarkdownCell(hub.Role),
			hub.AfferentCoupling,
			hub.EfferentCoupling,
			hub.Rank)
	}
	fmt.Fprintf(b, "\nCa = afferent coupling (packages that depend on it); Ce = efferent coupling (packages it depends on); Rank = dependency PageRank.\n\n")
}

func structureWriteCouplingTable(b *strings.Builder, metrics ProjectStructureMetrics, limit int) {
	records := structureTopCoupledPackages(metrics.Packages, limit)
	if len(records) == 0 {
		return
	}
	fmt.Fprintf(b, "### Dependency Structure Matrix (coupling)\n\n")
	fmt.Fprintf(b, "| Package | Files | Ca | Ce | Instability | Layer | Tests |\n")
	fmt.Fprintf(b, "| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, record := range records {
		tests := "no"
		if record.HasTests {
			tests = "yes"
		}
		cycleTag := ""
		if record.InCycle {
			cycleTag = " (cycle)"
		}
		fmt.Fprintf(b, "| `%s`%s | %d | %d | %d | %.2f | %d | %s |\n",
			analysisMarkdownCell(record.Label),
			cycleTag,
			record.FileCount,
			record.AfferentCoupling,
			record.EfferentCoupling,
			record.Instability,
			record.Layer,
			tests)
	}
	fmt.Fprintf(b, "\nInstability I = Ce / (Ca + Ce): 0 = maximally stable (only depended upon), 1 = maximally unstable (only depends on others).\n\n")
}

func structureWritePackageMermaid(b *strings.Builder, metrics ProjectStructureMetrics, limit int) {
	edges := metrics.PackageEdges
	if len(edges) == 0 {
		return
	}
	fmt.Fprintf(b, "### Package Dependency Graph\n\n")
	if len(edges) > limit {
		edges = edges[:limit]
		fmt.Fprintf(b, "Showing the %d highest-weight package dependencies.\n\n", limit)
	}
	fmt.Fprintf(b, "```mermaid\ngraph LR\n")
	ids := map[string]string{}
	nodeID := func(label string) string {
		if id, ok := ids[label]; ok {
			return id
		}
		id := fmt.Sprintf("p%02d", len(ids)+1)
		ids[label] = id
		fmt.Fprintf(b, "  %s[\"%s\"]\n", id, mermaidLabel(label))
		return id
	}
	for _, edge := range edges {
		if strings.EqualFold(edge.Source, edge.Target) {
			continue
		}
		source := nodeID(edge.Source)
		target := nodeID(edge.Target)
		fmt.Fprintf(b, "  %s -->|x%d| %s\n", source, edge.Weight, target)
	}
	fmt.Fprintf(b, "```\n\n")
}

func structureWriteHotspots(b *strings.Builder, metrics ProjectStructureMetrics, limit int) {
	if !metrics.GitAvailable {
		return
	}
	if len(metrics.Hotspots) == 0 {
		fmt.Fprintf(b, "### Change Hotspots\n\nNo notable change hotspots in the last %d days.\n\n", metrics.GitWindowDays)
		return
	}
	fmt.Fprintf(b, "### Change Hotspots (churn x size, last %d days)\n\n", metrics.GitWindowDays)
	fmt.Fprintf(b, "| File | Commits | Lines | Score | Last change | In cycle |\n")
	fmt.Fprintf(b, "| --- | --- | --- | --- | --- | --- |\n")
	for _, hotspot := range limitHotspots(metrics.Hotspots, limit) {
		inCycle := "no"
		if hotspot.InCycle {
			inCycle = "yes"
		}
		fmt.Fprintf(b, "| `%s` | %d | %d | %d | %s | %s |\n",
			analysisMarkdownCell(hotspot.Path),
			hotspot.Commits,
			hotspot.LineCount,
			hotspot.Score,
			analysisMarkdownCell(firstNonBlankAnalysisString(hotspot.LastChanged, "unknown")),
			inCycle)
	}
	fmt.Fprintf(b, "\nHotspots combine change frequency with file size; prioritize refactoring the high-score files, especially those inside a cycle.\n\n")
}

func structureWriteChangeCoupling(b *strings.Builder, metrics ProjectStructureMetrics, limit int) {
	if !metrics.GitAvailable || len(metrics.ChangeCoupling) == 0 {
		return
	}
	fmt.Fprintf(b, "### Hidden Change Coupling\n\n")
	fmt.Fprintf(b, "Files that change together but have no static import edge - candidate hidden dependencies.\n\n")
	fmt.Fprintf(b, "| File A | File B | Shared changes | Confidence |\n")
	fmt.Fprintf(b, "| --- | --- | --- | --- |\n")
	for _, edge := range limitChangeCoupling(metrics.ChangeCoupling, limit) {
		fmt.Fprintf(b, "| `%s` | `%s` | %d | %s |\n",
			analysisMarkdownCell(edge.Source),
			analysisMarkdownCell(edge.Target),
			edge.SharedChanges,
			analysisMarkdownCell(edge.Confidence))
	}
	fmt.Fprintf(b, "\n")
}

func structureWriteTestTopology(b *strings.Builder, metrics ProjectStructureMetrics, limit int) {
	topology := metrics.TestTopology
	if topology.CodePackages == 0 && topology.TotalTestFiles == 0 {
		return
	}
	fmt.Fprintf(b, "### Test Topology\n\n")
	fmt.Fprintf(b, "- Test files: %d\n", topology.TotalTestFiles)
	fmt.Fprintf(b, "- Packages with tests: %d/%d (%.0f%%)\n", topology.PackagesWithTests, topology.CodePackages, topology.CoverageRatio*100)
	if len(topology.UntestedPackages) > 0 {
		fmt.Fprintf(b, "- Untested code packages: %s\n", formatInlineCodeList(limitStrings(topology.UntestedPackages, limit), limit))
	}
	fmt.Fprintf(b, "\n")
}

func structureWriteOrphans(b *strings.Builder, metrics ProjectStructureMetrics, limit int) {
	if len(metrics.Orphans) == 0 {
		return
	}
	fmt.Fprintf(b, "### Orphan Files\n\n")
	fmt.Fprintf(b, "Code files with no inbound imports (not entrypoints, manifests, or tests) - candidate dead code or dynamically loaded modules.\n\n")
	for _, orphan := range limitStrings(metrics.Orphans, limit) {
		fmt.Fprintf(b, "- `%s`\n", orphan)
	}
	fmt.Fprintf(b, "\n")
}

func structureWriteNotes(b *strings.Builder, metrics ProjectStructureMetrics) {
	if len(metrics.Notes) == 0 {
		return
	}
	fmt.Fprintf(b, "### Notes\n\n")
	for _, note := range metrics.Notes {
		fmt.Fprintf(b, "- %s\n", note)
	}
	fmt.Fprintf(b, "\n")
}

func structureTopCoupledPackages(packages []PackageStructureRecord, limit int) []PackageStructureRecord {
	items := make([]PackageStructureRecord, 0, len(packages))
	for _, record := range packages {
		if record.AfferentCoupling == 0 && record.EfferentCoupling == 0 && record.FileCount == 0 {
			continue
		}
		items = append(items, record)
	}
	sort.Slice(items, func(i int, j int) bool {
		left := items[i].AfferentCoupling + items[i].EfferentCoupling
		right := items[j].AfferentCoupling + items[j].EfferentCoupling
		if left != right {
			return left > right
		}
		if items[i].FileCount != items[j].FileCount {
			return items[i].FileCount > items[j].FileCount
		}
		return items[i].Label < items[j].Label
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func structureInstabilityForHub(metrics ProjectStructureMetrics, hub StructureHubRecord) string {
	for _, record := range metrics.Packages {
		if record.Path == hub.Path {
			return fmt.Sprintf("%.2f", record.Instability)
		}
	}
	return "n/a"
}

func limitDependencyCycles(items []DependencyCycle, limit int) []DependencyCycle {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitHubRecords(items []StructureHubRecord, limit int) []StructureHubRecord {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitHotspots(items []StructureHotspot, limit int) []StructureHotspot {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitChangeCoupling(items []ChangeCouplingEdge, limit int) []ChangeCouplingEdge {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}
