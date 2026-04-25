package main

import (
	"strings"
	"testing"
)

func TestEffectiveProjectAnalysisModeDefaultsToMap(t *testing.T) {
	mode := effectiveProjectAnalysisMode("", "security-sensitive startup path")
	if mode != "map" {
		t.Fatalf("expected default mode map, got %q", mode)
	}
}

func TestParseAnalyzeProjectArgsParsesExplicitMode(t *testing.T) {
	mode, goal, err := parseAnalyzeProjectArgs("--mode security anti cheat trust boundary")
	if err != nil {
		t.Fatalf("parseAnalyzeProjectArgs returned error: %v", err)
	}
	if mode != "security" {
		t.Fatalf("expected security mode, got %q", mode)
	}
	if goal != "anti cheat trust boundary" {
		t.Fatalf("expected goal to preserve remaining text, got %q", goal)
	}
}

func TestParseAnalyzeProjectArgsParsesEqualsMode(t *testing.T) {
	mode, goal, err := parseAnalyzeProjectArgs("--mode=trace trace startup dispatch path")
	if err != nil {
		t.Fatalf("parseAnalyzeProjectArgs returned error: %v", err)
	}
	if mode != "trace" {
		t.Fatalf("expected trace mode, got %q", mode)
	}
	if goal != "trace startup dispatch path" {
		t.Fatalf("expected goal to preserve remaining text, got %q", goal)
	}
}

func TestParseAnalyzeProjectArgsAcceptsDocsAndSurfaceMode(t *testing.T) {
	mode, goal, err := parseAnalyzeProjectArgs("--docs --mode surface ioctl rpc parser surfaces")
	if err != nil {
		t.Fatalf("parseAnalyzeProjectArgs returned error: %v", err)
	}
	if mode != "surface" {
		t.Fatalf("expected surface mode, got %q", mode)
	}
	if goal != "ioctl rpc parser surfaces" {
		t.Fatalf("unexpected goal: %q", goal)
	}
}

func TestParseAnalyzeProjectCommandArgsParsesExplicitPath(t *testing.T) {
	parsed, err := parseAnalyzeProjectCommandArgs("--path src/driver --mode surface ioctl surfaces")
	if err != nil {
		t.Fatalf("parseAnalyzeProjectCommandArgs returned error: %v", err)
	}
	if parsed.Mode != "surface" {
		t.Fatalf("expected surface mode, got %q", parsed.Mode)
	}
	if parsed.Goal != "ioctl surfaces" {
		t.Fatalf("unexpected goal: %q", parsed.Goal)
	}
	if len(parsed.Paths) != 1 || parsed.Paths[0] != "src/driver" {
		t.Fatalf("unexpected paths: %#v", parsed.Paths)
	}
}

func TestResolveExplicitAnalysisScopeMatchesPathPrefix(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root: t.TempDir(),
		Files: []ScannedFile{
			{Path: "src/driver/ioctl.cpp", Directory: "src/driver"},
			{Path: "src/common/shared.cpp", Directory: "src/common"},
		},
		Directories: []string{"src/driver", "src/common"},
		FilesByDirectory: map[string][]ScannedFile{
			"src/driver": {{Path: "src/driver/ioctl.cpp", Directory: "src/driver"}},
			"src/common": {{Path: "src/common/shared.cpp", Directory: "src/common"}},
		},
	}
	scope, unmatched := resolveExplicitAnalysisScope([]string{"src/driver"}, snapshot)
	if len(unmatched) != 0 {
		t.Fatalf("expected no unmatched paths, got %#v", unmatched)
	}
	if len(scope.DirectoryPrefixes) != 1 || scope.DirectoryPrefixes[0] != "src/driver" {
		t.Fatalf("expected src/driver scope, got %#v", scope)
	}
}

func TestParseAnalyzeProjectArgsRejectsInvalidMode(t *testing.T) {
	_, _, err := parseAnalyzeProjectArgs("--mode weird map startup")
	if err == nil {
		t.Fatalf("expected invalid mode error")
	}
}

func TestParseAnalyzeProjectArgsDefaultsGoalFromMode(t *testing.T) {
	mode, goal, err := parseAnalyzeProjectArgs("--mode security")
	if err != nil {
		t.Fatalf("parseAnalyzeProjectArgs returned error: %v", err)
	}
	if mode != "security" {
		t.Fatalf("expected security mode, got %q", mode)
	}
	for _, needle := range []string{"trust boundaries", "privileged paths", "the project"} {
		if !strings.Contains(goal, needle) {
			t.Fatalf("expected default security goal to include %q, got %q", needle, goal)
		}
	}
}

func TestParseAnalyzeProjectCommandArgsDefaultsGoalFromModeAndPath(t *testing.T) {
	parsed, err := parseAnalyzeProjectCommandArgs("--path TavernKernel/TavernKernel --mode trace")
	if err != nil {
		t.Fatalf("parseAnalyzeProjectCommandArgs returned error: %v", err)
	}
	if parsed.Mode != "trace" {
		t.Fatalf("expected trace mode, got %q", parsed.Mode)
	}
	for _, needle := range []string{"runtime flows", "dispatch paths", "TavernKernel/TavernKernel"} {
		if !strings.Contains(parsed.Goal, needle) {
			t.Fatalf("expected default trace goal to include %q, got %q", needle, parsed.Goal)
		}
	}
}

func TestParseAnalyzeProjectArgsDefaultsEmptyCommandToMap(t *testing.T) {
	mode, goal, err := parseAnalyzeProjectArgs("")
	if err != nil {
		t.Fatalf("parseAnalyzeProjectArgs returned error: %v", err)
	}
	if mode != "" {
		t.Fatalf("expected implicit mode to remain empty, got %q", mode)
	}
	for _, needle := range []string{"map the architecture", "the project"} {
		if !strings.Contains(goal, needle) {
			t.Fatalf("expected default map goal to include %q, got %q", needle, goal)
		}
	}
}

func TestProjectAnalysisModeStatusReportsDefaultMap(t *testing.T) {
	status := projectAnalysisModeStatus("", "trace startup dispatch")
	if status != "default(map)" {
		t.Fatalf("expected default(map) status, got %q", status)
	}
}

func TestRenderAnalysisProjectHandoffGuidesNextCommands(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID: "analysis-1",
		},
	}
	manifest := AnalysisDocsManifest{
		DocumentCount: 7,
		FuzzTargets: []AnalysisFuzzTargetCatalogEntry{
			{
				Name:             "ParsePacket",
				SuggestedCommand: "/fuzz-func ParsePacket",
			},
		},
		VerificationMatrix: []AnalysisVerificationMatrixEntry{
			{
				ChangeArea:           "parser",
				RequiredVerification: "go test ./...",
			},
		},
	}
	out := renderAnalysisProjectHandoff(buildAnalysisProjectHandoff(run, manifest, true))
	for _, needle := range []string{
		"Analysis handoff:",
		"Continue: /analyze-dashboard",
		"Fuzz next: /fuzz-campaign run",
		"Target drilldown: /fuzz-func ParsePacket",
		"Verify next: /verify",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("expected handoff to include %q, got:\n%s", needle, out)
		}
	}
}

func TestRenderAnalysisProjectHandoffSuggestsDocsRefreshWhenManifestMissing(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID: "analysis-1",
		},
	}
	out := renderAnalysisProjectHandoff(buildAnalysisProjectHandoff(run, AnalysisDocsManifest{}, false))
	for _, needle := range []string{
		"Analysis handoff:",
		"Continue: /analyze-dashboard",
		"Repair: /docs-refresh",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("expected handoff to include %q, got:\n%s", needle, out)
		}
	}
}
