package main

import (
	"os"
	"path/filepath"
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

func TestParseAnalyzeProjectArgsAcceptsDeprecatedDocsFlag(t *testing.T) {
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

func TestPrepareExplicitAnalysisWorkspaceNarrowsSingleDirectoryPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "src", "driver")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "external"), 0o755); err != nil {
		t.Fatalf("mkdir external: %v", err)
	}
	ws := Workspace{BaseRoot: root, Root: root}
	updated, paths, err := prepareExplicitAnalysisWorkspace(ws, []string{"src/driver"})
	if err != nil {
		t.Fatalf("prepareExplicitAnalysisWorkspace returned error: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("expected path scope to be consumed after root narrowing, got %#v", paths)
	}
	if !strings.EqualFold(filepath.Clean(updated.Root), filepath.Clean(target)) {
		t.Fatalf("expected analysis root %q, got %q", target, updated.Root)
	}
}

func TestExplicitAnalysisWorkspaceKeepsMultiplePathsScoped(t *testing.T) {
	root := t.TempDir()
	ws := Workspace{BaseRoot: root, Root: root}
	updated, paths, err := prepareExplicitAnalysisWorkspace(ws, []string{"src/driver", "src/common"})
	if err != nil {
		t.Fatalf("prepareExplicitAnalysisWorkspace returned error: %v", err)
	}
	if !strings.EqualFold(filepath.Clean(updated.Root), filepath.Clean(root)) {
		t.Fatalf("expected workspace root to stay unchanged, got %q", updated.Root)
	}
	if len(paths) != 2 {
		t.Fatalf("expected paths to remain scoped for multi-path analysis, got %#v", paths)
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
			RunID:                  "analysis-1",
			ReviewProviderFailures: 1,
			ReviewQualityIssues:    2,
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
		"Provider failures: 1",
		"Review quality issues: 2",
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

func TestRenderAnalysisProjectArtifactPaths(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			OutputPath: filepath.Join("C:\\repo", ".kernforge", "analysis", "analysis-1_map_goal.md"),
		},
	}
	out := renderAnalysisProjectArtifactPaths(run, filepath.Join("C:\\repo", ".kernforge", "analysis"))
	for _, needle := range []string{
		"Analysis artifacts:",
		"Report:",
		"Run JSON:",
		"Latest:",
		"Dashboard:",
		"Docs:",
		"Manifest:",
		filepath.Join("C:\\repo", ".kernforge", "analysis", "latest", "dashboard.html"),
		filepath.Join("C:\\repo", ".kernforge", "analysis", "latest", "docs", "INDEX.md"),
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("expected artifact paths to include %q, got:\n%s", needle, out)
		}
	}
}

func TestRenderAnalysisProjectArtifactPathsIncludesRootCauseAudit(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			Mode:       "root-cause",
			OutputPath: filepath.Join("C:\\repo", ".kernforge", "analysis", "analysis-1_root-cause_goal.md"),
		},
	}
	out := renderAnalysisProjectArtifactPaths(run, filepath.Join("C:\\repo", ".kernforge", "analysis"))
	for _, needle := range []string{
		"Root-cause audit:",
		"Root-cause audit JSON:",
		filepath.Join("C:\\repo", ".kernforge", "analysis", "latest", "root_cause_audit.md"),
		filepath.Join("C:\\repo", ".kernforge", "analysis", "latest", "root_cause_audit.json"),
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("expected root-cause artifact paths to include %q, got:\n%s", needle, out)
		}
	}
}

func TestRenderAnalysisProjectArtifactPathsStyledHighlightsHeader(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			OutputPath: filepath.Join("C:\\repo", ".kernforge", "analysis", "analysis-1_map_goal.md"),
		},
	}
	out := renderAnalysisProjectArtifactPathsStyled(run, filepath.Join("C:\\repo", ".kernforge", "analysis"), UI{color: true})
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected styled artifact paths to include ANSI color, got:\n%s", out)
	}
	if !strings.Contains(out, "Analysis artifacts:") {
		t.Fatalf("expected styled artifact paths to retain readable header, got:\n%s", out)
	}
	if strings.HasPrefix(out, "Analysis artifacts:") {
		t.Fatalf("expected artifact header prefix to be colorized, got:\n%s", out)
	}
}
