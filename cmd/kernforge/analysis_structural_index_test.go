package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStructuralIndexExtractsCppSymbolsAndHeaderMacros(t *testing.T) {
	root := t.TempDir()
	writeAnalysisTestFile(t, filepath.Join(root, "driver.hpp"), strings.Join([]string{
		"#pragma once",
		"#define IOCTL_SAMPLE(x) CTL_CODE(FILE_DEVICE_UNKNOWN, x, METHOD_BUFFERED, FILE_ANY_ACCESS)",
		"namespace kern {",
		"class Device {",
		"public:",
		"    void DispatchIoctl();",
		"};",
		"}",
	}, "\n"))
	writeAnalysisTestFile(t, filepath.Join(root, "driver.cpp"), strings.Join([]string{
		"#include \"driver.hpp\"",
		"namespace kern {",
		"void Device::DispatchIoctl()",
		"{",
		"    ValidateRequest();",
		"}",
		"}",
	}, "\n"))
	snapshot := structuralIndexTestSnapshot(t, root, "driver.hpp", "driver.cpp")

	index := buildStructuralIndex(snapshot, "map driver ioctl flow", "run-struct-cpp")
	if index.Metrics.IndexedFiles != 2 {
		t.Fatalf("expected two indexed files, got %+v", index.Metrics)
	}
	assertStructuralSymbol(t, index, "class", "Device")
	assertStructuralSymbol(t, index, "namespace", "kern")
	assertStructuralSymbol(t, index, "function_macro", "IOCTL_SAMPLE")
	assertStructuralSymbol(t, index, "function", "kern::Device::DispatchIoctl")
	if len(index.References) == 0 {
		t.Fatalf("expected include reference in structural index")
	}
}

func TestStructuralIndexExtractsGoFunctionsMethodsAndTypes(t *testing.T) {
	root := t.TempDir()
	writeAnalysisTestFile(t, filepath.Join(root, "service.go"), strings.Join([]string{
		"package sample",
		"type Manager struct {",
		"    value int",
		"}",
		"func NewManager() *Manager {",
		"    return &Manager{}",
		"}",
		"func (m *Manager) Run() {",
		"    m.value++",
		"}",
	}, "\n"))
	snapshot := structuralIndexTestSnapshot(t, root, "service.go")

	index := buildStructuralIndex(snapshot, "map go service", "run-struct-go")
	assertStructuralSymbol(t, index, "struct", "Manager")
	assertStructuralSymbol(t, index, "function", "NewManager")
	assertStructuralSymbol(t, index, "function", "Manager.Run")
}

func TestStructuralIndexRecordsParserFailureFallbackDiagnostics(t *testing.T) {
	root := t.TempDir()
	snapshot := structuralIndexTestSnapshot(t, root, "missing.cpp")

	index := buildStructuralIndex(snapshot, "map missing file", "run-struct-failure")
	if index.Metrics.ParserFailures == 0 {
		t.Fatalf("expected parser failure metric, got %+v", index.Metrics)
	}
	if len(index.Diagnostics) == 0 || index.Diagnostics[0].Reason != "read_failed" {
		t.Fatalf("expected read_failed diagnostic, got %+v", index.Diagnostics)
	}
}

func TestEvidencePacketsPreferStructuralSymbolRanges(t *testing.T) {
	root := t.TempDir()
	writeAnalysisTestFile(t, filepath.Join(root, "driver.cpp"), strings.Join([]string{
		"static void Helper()",
		"{",
		"}",
		"",
		"NTSTATUS DriverEntry()",
		"{",
		"    return STATUS_SUCCESS;",
		"}",
	}, "\n"))
	snapshot := structuralIndexTestSnapshot(t, root, "driver.cpp")
	snapshot.StructuralIndex = buildStructuralIndex(snapshot, "map driver entry", "run-packets")
	shard := AnalysisShard{ID: "security_driver", Name: "security_driver", PrimaryFiles: []string{"driver.cpp"}}

	packets := buildEvidencePacketsForShard(snapshot, shard, 4)
	if len(packets) == 0 {
		t.Fatalf("expected evidence packets")
	}
	packet := packets[0]
	if packet.ExtractionMethod != "structural_symbol" {
		t.Fatalf("expected structural_symbol packet, got %+v", packet)
	}
	if !strings.Contains(packet.SymbolName, "DriverEntry") {
		t.Fatalf("expected DriverEntry packet first, got %+v", packet)
	}
	if packet.StartLine != 5 {
		t.Fatalf("expected symbol start line 5, got %+v", packet)
	}
}

func TestAnalysisPacketCoverageMetrics(t *testing.T) {
	reports := []WorkerReport{
		{
			ShardID: "s1",
			Claims: []AnalysisClaim{
				{ID: "c1", EvidencePacketIDs: []string{"s1-packet-01"}},
				{ID: "c2"},
			},
		},
	}
	packets := []EvidencePacket{
		{ID: "s1-packet-01", SymbolID: "function:DriverEntry", ExtractionMethod: "structural_symbol"},
		{ID: "s1-packet-02", ExtractionMethod: "file_prefix_fallback"},
	}
	metrics := computeAnalysisPacketCoverage(reports, packets)
	if metrics.TotalClaims != 2 || metrics.ClaimsWithPackets != 1 {
		t.Fatalf("unexpected claim coverage: %+v", metrics)
	}
	if metrics.TotalPackets != 2 || metrics.PacketsWithSymbolAnchor != 1 {
		t.Fatalf("unexpected packet coverage: %+v", metrics)
	}
}

func TestPersistRunWritesStructuralIndexArtifacts(t *testing.T) {
	root := t.TempDir()
	writeAnalysisTestFile(t, filepath.Join(root, "main.go"), "package main\nfunc main() {}\n")
	outputDir := filepath.Join(root, ".kernforge", "analysis")
	snapshot := structuralIndexTestSnapshot(t, root, "main.go")
	structural := buildStructuralIndex(snapshot, "map main", "run-artifacts")
	snapshot.StructuralIndex = structural
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:       "run-artifacts",
			Goal:        "map main",
			Mode:        "map",
			Status:      "completed",
			StartedAt:   time.Now().Add(-time.Minute),
			CompletedAt: time.Now(),
		},
		Snapshot:        snapshot,
		StructuralIndex: structural,
		FinalDocument:   "# Analysis\n",
		KnowledgePack: KnowledgePack{
			RunID:       "run-artifacts",
			Goal:        "map main",
			Root:        root,
			GeneratedAt: time.Now(),
		},
	}
	analyzer := &projectAnalyzer{
		analysisCfg: ProjectAnalysisConfig{OutputDir: outputDir},
		workspace:   Workspace{Root: root, BaseRoot: root},
	}
	if _, err := analyzer.persistRun(run, context.Background()); err != nil {
		t.Fatalf("persist run: %v", err)
	}
	base := filepath.Join(outputDir, analysisArtifactBaseName(run.Summary.RunID, run.Summary.Goal, run.Summary.Mode))
	if _, err := os.Stat(base + "_structural_index.json"); err != nil {
		t.Fatalf("expected structural index artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "latest", "structural_index.json")); err != nil {
		t.Fatalf("expected latest structural index artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "latest", "docs", "STRUCTURAL_INDEX.md")); err != nil {
		t.Fatalf("expected structural index doc: %v", err)
	}
}

func structuralIndexTestSnapshot(t *testing.T, root string, paths ...string) ProjectSnapshot {
	t.Helper()
	snapshot := ProjectSnapshot{
		Root:               root,
		GeneratedAt:        time.Now(),
		FilesByPath:        map[string]ScannedFile{},
		FilesByDirectory:   map[string][]ScannedFile{},
		ImportGraph:        map[string][]string{},
		ReverseImportGraph: map[string][]string{},
	}
	for _, path := range paths {
		abs := filepath.Join(root, filepath.FromSlash(path))
		data, _ := os.ReadFile(abs)
		file := ScannedFile{
			Path:       filepath.ToSlash(path),
			Directory:  filepath.ToSlash(filepath.Dir(path)),
			Extension:  strings.ToLower(filepath.Ext(path)),
			LineCount:  countLines(data),
			RawImports: discoverImports(strings.ToLower(filepath.Ext(path)), string(data)),
		}
		if file.Directory == "." {
			file.Directory = ""
		}
		snapshot.Files = append(snapshot.Files, file)
		snapshot.FilesByPath[file.Path] = file
		snapshot.FilesByDirectory[file.Directory] = append(snapshot.FilesByDirectory[file.Directory], file)
		snapshot.TotalFiles++
		snapshot.TotalLines += file.LineCount
	}
	analyzer := projectAnalyzer{}
	analyzer.resolveImports(&snapshot)
	return snapshot
}

func assertStructuralSymbol(t *testing.T, index StructuralIndex, kind string, namePart string) {
	t.Helper()
	for _, symbol := range index.Symbols {
		if strings.EqualFold(symbol.Kind, kind) && strings.Contains(symbol.CanonicalName+" "+symbol.Name, namePart) {
			return
		}
	}
	t.Fatalf("expected structural symbol kind=%s name~=%s in %+v", kind, namePart, index.Symbols)
}
