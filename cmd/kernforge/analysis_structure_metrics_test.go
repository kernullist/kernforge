package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// structureMetricsTestSnapshot builds a small three-package project whose import
// graph contains a deliberate core<->store cycle, an orphan file, and a single
// tested package, so the deterministic metrics have known expected values.
func structureMetricsTestSnapshot() ProjectSnapshot {
	files := []ScannedFile{
		{Path: "app/main.go", Directory: "app", Extension: ".go", LineCount: 120, IsEntrypoint: true},
		{Path: "app/handler.go", Directory: "app", Extension: ".go", LineCount: 200},
		{Path: "app/orphan.go", Directory: "app", Extension: ".go", LineCount: 40},
		{Path: "core/service.go", Directory: "core", Extension: ".go", LineCount: 400},
		{Path: "core/util.go", Directory: "core", Extension: ".go", LineCount: 90},
		{Path: "core/service_test.go", Directory: "core", Extension: ".go", LineCount: 150},
		{Path: "store/db.go", Directory: "store", Extension: ".go", LineCount: 300},
	}
	snapshot := ProjectSnapshot{
		Root:               "C:\\repo",
		GeneratedAt:        time.Unix(1_700_000_000, 0).UTC(),
		Directories:        []string{"app", "core", "store"},
		EntrypointFiles:    []string{"app/main.go"},
		FilesByPath:        map[string]ScannedFile{},
		FilesByDirectory:   map[string][]ScannedFile{},
		ImportGraph:        map[string][]string{},
		ReverseImportGraph: map[string][]string{},
	}
	for _, file := range files {
		snapshot.Files = append(snapshot.Files, file)
		snapshot.FilesByPath[file.Path] = file
		snapshot.FilesByDirectory[file.Directory] = append(snapshot.FilesByDirectory[file.Directory], file)
		snapshot.TotalFiles++
		snapshot.TotalLines += file.LineCount
	}
	snapshot.ImportGraph = map[string][]string{
		"app/main.go":          {"core/service.go", "app/handler.go"},
		"app/handler.go":       {"core/service.go"},
		"core/service.go":      {"store/db.go"},
		"store/db.go":          {"core/util.go"},
		"core/service_test.go": {"core/service.go"},
	}
	for source, targets := range snapshot.ImportGraph {
		for _, target := range targets {
			snapshot.ReverseImportGraph[target] = append(snapshot.ReverseImportGraph[target], source)
		}
	}
	return snapshot
}

func findPackageRecord(metrics ProjectStructureMetrics, path string) (PackageStructureRecord, bool) {
	for _, record := range metrics.Packages {
		if record.Path == path {
			return record, true
		}
	}
	return PackageStructureRecord{}, false
}

func TestBuildProjectStructureMetricsComputesPackageGraph(t *testing.T) {
	snapshot := structureMetricsTestSnapshot()
	metrics := buildProjectStructureMetrics(context.Background(), snapshot, "")

	if metrics.PackageCount != 3 {
		t.Fatalf("expected 3 packages, got %d", metrics.PackageCount)
	}
	if metrics.GitAvailable {
		t.Fatalf("git metrics should be absent when gitRoot is empty")
	}

	core, ok := findPackageRecord(metrics, "core")
	if !ok {
		t.Fatalf("core package record missing")
	}
	if core.AfferentCoupling != 2 {
		t.Fatalf("core afferent coupling: want 2 (app, store), got %d", core.AfferentCoupling)
	}
	if core.EfferentCoupling != 1 {
		t.Fatalf("core efferent coupling: want 1 (store), got %d", core.EfferentCoupling)
	}
	if !core.InCycle {
		t.Fatalf("core should be flagged as part of a cycle")
	}
	if !core.HasTests {
		t.Fatalf("core should be marked as having tests")
	}

	app, ok := findPackageRecord(metrics, "app")
	if !ok {
		t.Fatalf("app package record missing")
	}
	if app.AfferentCoupling != 0 || app.EfferentCoupling != 1 {
		t.Fatalf("app coupling: want Ca=0 Ce=1, got Ca=%d Ce=%d", app.AfferentCoupling, app.EfferentCoupling)
	}
	if app.Instability != 1 {
		t.Fatalf("app instability: want 1.0, got %v", app.Instability)
	}
	if app.Layer <= core.Layer {
		t.Fatalf("app layer (%d) should be above core/store foundation layer (%d)", app.Layer, core.Layer)
	}
}

func TestBuildProjectStructureMetricsDetectsCycle(t *testing.T) {
	snapshot := structureMetricsTestSnapshot()
	metrics := buildProjectStructureMetrics(context.Background(), snapshot, "")

	if len(metrics.Cycles) != 1 {
		t.Fatalf("expected exactly one package cycle, got %d", len(metrics.Cycles))
	}
	cycle := metrics.Cycles[0]
	if cycle.Size != 2 {
		t.Fatalf("expected cycle of size 2, got %d", cycle.Size)
	}
	joined := strings.Join(cycle.Members, ",")
	if !strings.Contains(joined, "core") || !strings.Contains(joined, "store") {
		t.Fatalf("cycle members should be core and store, got %q", joined)
	}
	if len(cycle.BreakEdges) == 0 {
		t.Fatalf("cycle should list concrete break edges")
	}
	if metrics.HealthScore >= 100 {
		t.Fatalf("health score should be penalized below 100 when a cycle exists, got %d", metrics.HealthScore)
	}
}

func TestBuildProjectStructureMetricsFindsOrphanAndTestTopology(t *testing.T) {
	snapshot := structureMetricsTestSnapshot()
	metrics := buildProjectStructureMetrics(context.Background(), snapshot, "")

	foundOrphan := false
	for _, orphan := range metrics.Orphans {
		if orphan == "app/orphan.go" {
			foundOrphan = true
		}
		if orphan == "app/main.go" {
			t.Fatalf("entrypoint file must not be reported as an orphan")
		}
	}
	if !foundOrphan {
		t.Fatalf("expected app/orphan.go to be reported as an orphan, got %v", metrics.Orphans)
	}

	topology := metrics.TestTopology
	if topology.CodePackages != 3 {
		t.Fatalf("expected 3 code packages, got %d", topology.CodePackages)
	}
	if topology.PackagesWithTests != 1 {
		t.Fatalf("expected 1 package with tests, got %d", topology.PackagesWithTests)
	}
	untested := strings.Join(topology.UntestedPackages, ",")
	if !strings.Contains(untested, "app") || !strings.Contains(untested, "store") {
		t.Fatalf("expected app and store to be untested, got %q", untested)
	}
}

func TestRenderProjectStructureMetricsSections(t *testing.T) {
	snapshot := structureMetricsTestSnapshot()
	metrics := buildProjectStructureMetrics(context.Background(), snapshot, "")

	report := renderProjectStructureMetricsReport(metrics)
	for _, want := range []string{
		"Deterministic Structure Metrics",
		"Circular Dependencies",
		"Architecture Layers",
		"Architectural Hubs And Bottlenecks",
		"Dependency Structure Matrix",
		"Test Topology",
		"Orphan Files",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("structure report missing section %q\n%s", want, report)
		}
	}

	compact := renderProjectStructureMetricsCompact(metrics)
	if !strings.Contains(compact, "## Project Structure Metrics") {
		t.Fatalf("compact section missing heading\n%s", compact)
	}
	if !strings.Contains(compact, "Structure health:") {
		t.Fatalf("compact section missing health line\n%s", compact)
	}
	if !strings.Contains(compact, "Circular dependencies") {
		t.Fatalf("compact section should surface circular dependencies\n%s", compact)
	}
}

func TestProjectStructureMetricsEmptyGraphHasNoData(t *testing.T) {
	snapshot := ProjectSnapshot{
		GeneratedAt:      time.Unix(1_700_000_000, 0).UTC(),
		FilesByPath:      map[string]ScannedFile{},
		FilesByDirectory: map[string][]ScannedFile{},
		ImportGraph:      map[string][]string{},
	}
	metrics := buildProjectStructureMetrics(context.Background(), snapshot, "")
	if projectStructureMetricsHasData(metrics) {
		t.Fatalf("empty snapshot should not report structure data")
	}
	if strings.TrimSpace(renderProjectStructureMetricsCompact(metrics)) != "" {
		t.Fatalf("empty metrics should render an empty compact section")
	}
}

func TestExtractPythonImports(t *testing.T) {
	content := strings.Join([]string{
		"import os",
		"import a.b.c",
		"import numpy as np",
		"from .foo import bar",
		"from ..pkg import x",
		"from mod import y",
		"from . import sibling",
		"# from commented import nope",
	}, "\n")
	got := extractPythonImports(content)
	assertContainsAll(t, "python", got, []string{"os", "a/b/c", "numpy", "./foo", "../pkg", "mod", "./sibling"})
	assertNotContains(t, "python", got, "nope")
}

func TestExtractCSharpImports(t *testing.T) {
	content := strings.Join([]string{
		"using System;",
		"using Foo.Bar;",
		"using static Foo.MathUtil;",
		"using Alias = A.B.C;",
		"using (var stream = Open()) {",
		"// using Commented.Namespace;",
	}, "\n")
	got := extractCSharpImports(content)
	assertContainsAll(t, "csharp", got, []string{"System", "Foo/Bar", "Foo/MathUtil", "A/B/C"})
	assertNotContains(t, "csharp", got, "Commented/Namespace")
}

func TestExtractRustImports(t *testing.T) {
	content := strings.Join([]string{
		"use crate::a::b;",
		"use super::c;",
		"use self::d;",
		"use foo::{bar, baz};",
		"pub use crate::x::y;",
		"mod inner;",
		"mod outer { }",
	}, "\n")
	got := extractRustImports(content)
	assertContainsAll(t, "rust", got, []string{"a/b", "../c", "./d", "foo", "x/y", "./inner"})
	assertNotContains(t, "rust", got, "outer")
}

func TestResolveLanguageScopedImportStaysWithinLanguage(t *testing.T) {
	snapshot := ProjectSnapshot{
		FilesByPath: map[string]ScannedFile{
			"pkg/service.py": {Path: "pkg/service.py", Directory: "pkg", Extension: ".py"},
			"pkg/service.go": {Path: "pkg/service.go", Directory: "pkg", Extension: ".go"},
		},
		FilesByDirectory: map[string][]ScannedFile{
			"pkg": {
				{Path: "pkg/service.py", Directory: "pkg", Extension: ".py"},
				{Path: "pkg/service.go", Directory: "pkg", Extension: ".go"},
			},
		},
	}
	analyzer := &projectAnalyzer{}
	got := analyzer.resolveLanguageScopedImport(snapshot, snapshot.FilesByPath["pkg/service.py"], "pkg/service", pythonImportExtensions)
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "pkg/service.py") {
		t.Fatalf("expected python import to resolve to service.py, got %q", joined)
	}
	if strings.Contains(joined, "pkg/service.go") {
		t.Fatalf("python import must not bind to a Go file, got %q", joined)
	}
}

func assertContainsAll(t *testing.T, label string, got []string, want []string) {
	t.Helper()
	set := map[string]struct{}{}
	for _, item := range got {
		set[item] = struct{}{}
	}
	for _, item := range want {
		if _, ok := set[item]; !ok {
			t.Fatalf("%s imports missing %q; got %v", label, item, got)
		}
	}
}

func assertNotContains(t *testing.T, label string, got []string, unwanted string) {
	t.Helper()
	for _, item := range got {
		if item == unwanted {
			t.Fatalf("%s imports should not contain %q; got %v", label, unwanted, got)
		}
	}
}
