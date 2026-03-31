package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildVerificationStepsForGoUsesChangedPackagesThenFullSuite(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	steps := buildVerificationSteps(root, []string{
		filepath.Join(root, "cmd", "app", "main.go"),
		filepath.Join(root, "internal", "auth", "service.go"),
	}, VerificationAdaptive)
	if len(steps) < 4 {
		t.Fatalf("expected targeted + full + vet verification steps, got %#v", steps)
	}
	if steps[0].Command != "go test ./cmd/app/..." {
		t.Fatalf("unexpected first verification step: %#v", steps[0])
	}
	if steps[1].Command != "go test ./internal/auth/..." {
		t.Fatalf("unexpected second verification step: %#v", steps[1])
	}
	if steps[2].Command != "go test ./..." {
		t.Fatalf("expected workspace test step, got %#v", steps[2])
	}
	if steps[len(steps)-1].Command != "go vet ./..." {
		t.Fatalf("expected final workspace vet step, got %#v", steps[len(steps)-1])
	}
}

func TestBuildVerificationStepsForNodeIncludesTypecheckLintAndTest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"typecheck":"tsc -p .","lint":"eslint .","test":"vitest"}}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	steps := buildVerificationSteps(root, nil, VerificationAdaptive)
	if len(steps) != 3 {
		t.Fatalf("expected 3 node verification steps, got %#v", steps)
	}
	if steps[0].Command != "npm run typecheck" {
		t.Fatalf("unexpected typecheck step: %#v", steps[0])
	}
	if steps[1].Command != "npm run lint" {
		t.Fatalf("unexpected lint step: %#v", steps[1])
	}
	if steps[2].Command != "npm test -- --runInBand" {
		t.Fatalf("unexpected test step: %#v", steps[2])
	}
}

func TestBuildVerificationStepsForCargoIncludesCheckAndTest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname = \"demo\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatalf("write Cargo.toml: %v", err)
	}
	steps := buildVerificationSteps(root, nil, VerificationAdaptive)
	if len(steps) != 2 {
		t.Fatalf("expected 2 cargo verification steps, got %#v", steps)
	}
	if steps[0].Command != "cargo check" || steps[1].Command != "cargo test" {
		t.Fatalf("unexpected cargo steps: %#v", steps)
	}
}

func TestBuildVerificationStepsForCMakeUsesBuildDirAndCTestInFullMode(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CMakeLists.txt"), []byte("cmake_minimum_required(VERSION 3.20)\nproject(Demo)\n"), 0o644); err != nil {
		t.Fatalf("write CMakeLists.txt: %v", err)
	}
	buildDir := filepath.Join(root, "build")
	if err := os.MkdirAll(filepath.Join(buildDir, "CMakeFiles"), 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "CMakeCache.txt"), []byte("# cache"), 0o644); err != nil {
		t.Fatalf("write CMakeCache.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "CTestTestfile.cmake"), []byte("# tests"), 0o644); err != nil {
		t.Fatalf("write CTestTestfile.cmake: %v", err)
	}
	steps := buildVerificationSteps(root, []string{"src/foo.cpp"}, VerificationFull)
	if len(steps) != 2 {
		t.Fatalf("expected build + test steps for CMake workspace, got %#v", steps)
	}
	if steps[0].Command != `cmake --build "build" --parallel` {
		t.Fatalf("unexpected CMake build step: %#v", steps[0])
	}
	if steps[1].Command != `ctest --test-dir "build" --output-on-failure` {
		t.Fatalf("unexpected CTest step: %#v", steps[1])
	}
}

func TestNormalizeVerificationOverridePathSupportsMentions(t *testing.T) {
	tests := map[string]string{
		"@Common/PEreloc.cpp":       "Common/PEreloc.cpp",
		"@Common/PEreloc.cpp:10-40": "Common/PEreloc.cpp",
		`"@src/foo.cpp:8"`:          "src/foo.cpp",
		"plain/file.cpp":            "plain/file.cpp",
	}
	for input, want := range tests {
		if got := normalizeVerificationOverridePath(input); got != want {
			t.Fatalf("normalizeVerificationOverridePath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCollectSessionChangedPathsParsesEditToolOutputs(t *testing.T) {
	sess := &Session{
		Messages: []Message{
			{Role: "tool", ToolName: "write_file", Text: "wrote 10 bytes to src/main.go"},
			{Role: "tool", ToolName: "replace_in_file", Text: "updated internal/auth/service.go (1 replacement(s))"},
			{Role: "tool", ToolName: "apply_patch", Text: "updated cmd/app/main.go\nadded docs/readme.md"},
		},
	}
	paths := collectSessionChangedPaths(sess)
	joined := strings.Join(paths, ",")
	for _, needle := range []string{"src/main.go", "internal/auth/service.go", "cmd/app/main.go", "docs/readme.md"} {
		if !strings.Contains(joined, needle) {
			t.Fatalf("expected changed paths to include %q, got %#v", needle, paths)
		}
	}
}

func TestVerificationReportSummaryLine(t *testing.T) {
	report := VerificationReport{
		Steps: []VerificationStep{
			{Status: VerificationPassed},
			{Status: VerificationFailed},
			{Status: VerificationSkipped},
		},
	}
	if got := report.SummaryLine(); got != "Verification: passed=1 failed=1 skipped=1" {
		t.Fatalf("unexpected summary line: %q", got)
	}
}

func TestClassifyVerificationFailureRecognizesCommonGoFailures(t *testing.T) {
	kind, hint := classifyVerificationFailure(VerificationStep{
		Label:   "go test ./...",
		Command: "go test ./...",
		Output:  "build failed\nundefined: Foo",
	})
	if kind != "compile_error" {
		t.Fatalf("expected compile_error, got %q", kind)
	}
	if !strings.Contains(strings.ToLower(hint), "compile") {
		t.Fatalf("expected compile-oriented hint, got %q", hint)
	}
}

func TestVerificationReportFailureSummaryIncludesKindsAndHints(t *testing.T) {
	report := VerificationReport{
		Steps: []VerificationStep{{
			Label:       "go vet ./...",
			Status:      VerificationFailed,
			FailureKind: "lint_error",
			Hint:        "Fix the vet warnings first.",
		}},
	}
	summary := report.FailureSummary()
	if !strings.Contains(summary, "lint_error") || !strings.Contains(summary, "Fix the vet warnings first.") {
		t.Fatalf("unexpected failure summary: %q", summary)
	}
}

func TestVerificationReportRepairGuidanceUsesFailureKind(t *testing.T) {
	report := VerificationReport{
		Steps: []VerificationStep{{
			Label:       "go test ./...",
			Status:      VerificationFailed,
			FailureKind: "compile_error",
			Hint:        "Fix compiler errors first.",
		}},
	}
	guidance := report.RepairGuidance()
	if !strings.Contains(strings.ToLower(guidance), "compiler") {
		t.Fatalf("expected compile-oriented guidance, got %q", guidance)
	}
	if !strings.Contains(report.RenderDetailed(), "Suggested repair strategy:") {
		t.Fatalf("expected detailed report to include repair strategy, got %q", report.RenderDetailed())
	}
}

func TestBuildVerificationPlanFullModeSkipsTargetedSteps(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	plan := buildVerificationPlan(root, []string{filepath.Join(root, "cmd", "app", "main.go")}, VerificationFull)
	if len(plan.Steps) != 2 {
		t.Fatalf("expected full mode to include workspace test + vet, got %#v", plan.Steps)
	}
	for _, step := range plan.Steps {
		if step.Stage != "workspace" {
			t.Fatalf("expected only workspace steps in full mode, got %#v", plan.Steps)
		}
	}
}

func TestExecuteVerificationStepsAddsAdaptiveDecision(t *testing.T) {
	ws := Workspace{Root: t.TempDir(), Shell: "powershell"}
	plan := VerificationPlan{
		Mode: VerificationAdaptive,
		Steps: []VerificationStep{
			{Label: "pkg test", Command: "cmd /c exit 1", Stage: "targeted", Status: VerificationPending},
			{Label: "workspace test", Command: "cmd /c exit 0", Stage: "workspace", Status: VerificationPending},
		},
	}
	report := executeVerificationSteps(context.Background(), ws, "manual", plan)
	if !strings.Contains(strings.ToLower(report.Decision), "targeted failure") {
		t.Fatalf("expected adaptive decision summary, got %q", report.Decision)
	}
}

func TestExecuteVerificationStepsCanContinueAfterFailureWhenPolicyAllows(t *testing.T) {
	ws := Workspace{Root: t.TempDir(), Shell: "powershell"}
	plan := VerificationPlan{
		Mode: VerificationAdaptive,
		Steps: []VerificationStep{
			{Label: "first", Command: "cmd /c exit 1", Stage: "workspace", ContinueOnFailure: true, Status: VerificationPending},
			{Label: "second", Command: "cmd /c exit 0", Stage: "workspace", Status: VerificationPending},
		},
	}
	report := executeVerificationSteps(context.Background(), ws, "manual", plan)
	if report.Steps[1].Status != VerificationPassed {
		t.Fatalf("expected second step to run after failure, got %#v", report.Steps)
	}
	if !strings.Contains(strings.ToLower(report.Decision), "continue_on_failure") {
		t.Fatalf("expected decision to mention continue_on_failure, got %q", report.Decision)
	}
}

func TestExecuteVerificationStepsStopOnFailureOverridesContinue(t *testing.T) {
	ws := Workspace{Root: t.TempDir(), Shell: "powershell"}
	plan := VerificationPlan{
		Mode: VerificationAdaptive,
		Steps: []VerificationStep{
			{Label: "first", Command: "cmd /c exit 1", Stage: "workspace", ContinueOnFailure: true, StopOnFailure: true, Status: VerificationPending},
			{Label: "second", Command: "cmd /c exit 0", Stage: "workspace", Status: VerificationPending},
		},
	}
	report := executeVerificationSteps(context.Background(), ws, "manual", plan)
	if report.Steps[1].Status != VerificationSkipped {
		t.Fatalf("expected second step to be skipped when stop_on_failure is set, got %#v", report.Steps)
	}
	if !strings.Contains(strings.ToLower(report.Decision), "stop_on_failure") {
		t.Fatalf("expected decision to mention stop_on_failure, got %q", report.Decision)
	}
}

func TestBuildVerificationPlanWithTuningPrioritizesHistoricallyFlakyWorkspaceCheck(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	tuning := VerificationTuning{
		RunCounts: map[string]int{
			"go test workspace": 2,
			"go vet workspace":  5,
		},
		FailureCounts: map[string]int{
			"go vet workspace": 4,
		},
	}
	plan := buildVerificationPlanWithTuning(root, []string{filepath.Join(root, "internal", "auth", "service.go")}, VerificationAdaptive, tuning)
	if len(plan.Steps) < 3 {
		t.Fatalf("unexpected verification plan: %#v", plan.Steps)
	}
	if plan.Steps[0].Stage != "targeted" {
		t.Fatalf("expected targeted test to remain first, got %#v", plan.Steps)
	}
	if plan.Steps[1].Command != "go vet ./..." {
		t.Fatalf("expected historically flaky workspace check to be prioritized, got %#v", plan.Steps)
	}
	if !strings.Contains(strings.ToLower(plan.PlannerNote), "file-pattern") {
		t.Fatalf("expected planner note describing reordering, got %q", plan.PlannerNote)
	}
}

func TestVerificationPatternScorePrioritizesNodeTypecheckForTSChanges(t *testing.T) {
	steps := []VerificationStep{
		{Label: "npm run lint", Command: "npm run lint", Stage: "workspace"},
		{Label: "npm run typecheck", Command: "npm run typecheck", Stage: "workspace"},
		{Label: "npm test", Command: "npm test -- --runInBand", Stage: "workspace"},
	}
	reordered, note := reorderVerificationSteps(steps, []string{"src/app.tsx"}, VerificationTuning{})
	if reordered[0].Command != "npm run typecheck" {
		t.Fatalf("expected typecheck to be prioritized for TS changes, got %#v", reordered)
	}
	if !strings.Contains(strings.ToLower(note), "file-pattern") {
		t.Fatalf("expected planner note for pattern-driven reordering, got %q", note)
	}
}

func TestVerificationPatternScorePrioritizesNodeTestForSpecChanges(t *testing.T) {
	steps := []VerificationStep{
		{Label: "npm run lint", Command: "npm run lint", Stage: "workspace"},
		{Label: "npm test", Command: "npm test -- --runInBand", Stage: "workspace"},
		{Label: "npm run typecheck", Command: "npm run typecheck", Stage: "workspace"},
	}
	reordered, _ := reorderVerificationSteps(steps, []string{"src/app.spec.ts"}, VerificationTuning{})
	if reordered[0].Command != "npm test -- --runInBand" {
		t.Fatalf("expected test to be prioritized for spec changes, got %#v", reordered)
	}
}

func TestVerificationPatternScorePrioritizesLintForLintConfigChanges(t *testing.T) {
	steps := []VerificationStep{
		{Label: "npm test", Command: "npm test -- --runInBand", Stage: "workspace"},
		{Label: "npm run lint", Command: "npm run lint", Stage: "workspace"},
		{Label: "npm run typecheck", Command: "npm run typecheck", Stage: "workspace"},
	}
	reordered, _ := reorderVerificationSteps(steps, []string{"eslint.config.js"}, VerificationTuning{})
	if reordered[0].Command != "npm run lint" {
		t.Fatalf("expected lint to be prioritized for lint config changes, got %#v", reordered)
	}
}
