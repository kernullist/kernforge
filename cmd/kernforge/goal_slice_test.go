package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGoalSlicePlanFromSkillFormat(t *testing.T) {
	text := `
Objective
- Add session export command end to end

Non-goals
- Unrelated UI polish
- Dependency upgrades

Current facts
- Not inspected in this turn

Slices
1. Contract and routing
   Outcome: /session export is registered and documents its flags
   Scope: command router + help text only
   Likely files: cmd/kernforge/session.go, cmd/kernforge/commands.go
   Acceptance: help lists export; unknown flags error clearly
   Validation: go test ./cmd/kernforge -run TestSessionExport
   Docs: QUICKSTART session section
   Risk: medium

2. Implementation body
   Outcome: export writes a portable archive of the active session
   Scope: export writer; no import yet
   Likely files: cmd/kernforge/session_export.go
   Acceptance: archive contains session json and transcript
   Validation: unit test with temp dir
   Docs: no-docs-needed
   Risk: high
   Depends on: contract-and-routing

3. Verification and docs
   Outcome: regression coverage and docs match behavior
   Scope: tests + docs only
   Likely files: cmd/kernforge/session_export_test.go, docs/QUICKSTART.md
   Acceptance: tests pass; QUICKSTART shows one copy-paste example
   Validation: go test ./cmd/kernforge -run TestSessionExport
   Docs: docs/QUICKSTART.md
   Risk: low
   Depends on: implementation-body

Recommended first slice
- Contract and routing because it locks the user-facing surface before the writer

Open questions
- None

KernForge execution plan
- [pending] Register export command
- [pending] Implement writer
- [pending] Verify and document

Draft status
- No files changed, no tests run, and no artifacts created unless explicitly true.
`
	plan, ok := parseGoalSlicePlanFromText(text)
	if !ok {
		t.Fatal("expected structured slice plan parse")
	}
	if len(plan.Slices) != 3 {
		t.Fatalf("expected 3 slices, got %#v", plan.Slices)
	}
	if !strings.Contains(strings.ToLower(plan.ObjectiveRestated), "export") {
		t.Fatalf("objective restated missing export: %q", plan.ObjectiveRestated)
	}
	if len(plan.NonGoals) != 2 {
		t.Fatalf("expected 2 non-goals, got %#v", plan.NonGoals)
	}
	first := plan.Slices[0]
	if first.Outcome == "" || first.Validation == "" {
		t.Fatalf("first slice missing fields: %#v", first)
	}
	if len(first.LikelyFiles) != 2 {
		t.Fatalf("expected likely files, got %#v", first.LikelyFiles)
	}
	// Sequential default should not override explicit depends_on on later slices.
	if len(plan.Slices[1].DependsOn) == 0 {
		t.Fatalf("expected explicit/derived depends_on on slice 2, got %#v", plan.Slices[1])
	}
	order := plan.TopoOrder()
	if len(order) != 3 {
		t.Fatalf("expected topo order of 3, got %#v", order)
	}
	ready := plan.ReadySlices()
	if len(ready) != 1 {
		t.Fatalf("expected exactly one ready slice, got %#v", ready)
	}
	if ready[0].ID != plan.Slices[0].ID {
		t.Fatalf("ready slice should be first, got %#v want %s", ready[0], plan.Slices[0].ID)
	}
}

func TestParseGoalSlicePlanRejectsFlatNumberedList(t *testing.T) {
	text := `
1. Inspect the codebase
2. Implement the fix
3. Run tests
4. Update docs
`
	if _, ok := parseGoalSlicePlanFromText(text); ok {
		t.Fatal("flat numbered list must not become a SlicePlan")
	}
}

func TestGoalSlicePlanSequentialDefaultsAndCycleBreak(t *testing.T) {
	plan := GoalSlicePlan{
		Slices: []GoalSlice{
			{Name: "A", Outcome: "first"},
			{Name: "B", Outcome: "second"},
			{Name: "C", Outcome: "third"},
		},
	}
	plan.Normalize()
	if len(plan.Slices[0].DependsOn) != 0 {
		t.Fatalf("first slice must have no deps, got %#v", plan.Slices[0].DependsOn)
	}
	if len(plan.Slices[1].DependsOn) != 1 || plan.Slices[1].DependsOn[0] != plan.Slices[0].ID {
		t.Fatalf("slice B should depend on A, got %#v", plan.Slices[1])
	}
	// Introduce a cycle and ensure Normalize linearizes.
	plan.Slices[0].DependsOn = []string{plan.Slices[2].ID}
	plan.Slices[1].DependsOn = []string{plan.Slices[0].ID}
	plan.Slices[2].DependsOn = []string{plan.Slices[1].ID}
	plan.Normalize()
	if order := plan.TopoOrder(); len(order) != 3 {
		t.Fatalf("cycle should be broken into topo order, got %#v questions=%#v", order, plan.OpenQuestions)
	}
}

func TestPlanItemsFromSlices(t *testing.T) {
	plan := GoalSlicePlan{
		Slices: []GoalSlice{
			{Name: "Schema", Outcome: "define contract", Status: goalSliceStatusComplete},
			{Name: "Impl", Outcome: "write code", Status: goalSliceStatusPending},
		},
	}
	items := PlanItemsFromSlices(plan)
	if len(items) != 2 {
		t.Fatalf("expected 2 plan items, got %#v", items)
	}
	if items[0].Status != "completed" {
		t.Fatalf("expected completed projection, got %#v", items[0])
	}
	if !strings.Contains(items[1].Step, "Impl") {
		t.Fatalf("expected name in step, got %#v", items[1])
	}
}

func TestFallbackGoalSlicePlan(t *testing.T) {
	goal := GoalState{Objective: "fix the parser crash", UserCriteria: []string{"regression test added"}}
	ensureGoalAcceptanceSpec(&goal)
	plan := fallbackGoalSlicePlan(goal)
	if len(plan.Slices) != 1 {
		t.Fatalf("expected single fallback slice, got %#v", plan.Slices)
	}
	if !strings.Contains(strings.Join(plan.Slices[0].Acceptance, "\n"), "regression") {
		t.Fatalf("fallback should carry user criteria, got %#v", plan.Slices[0].Acceptance)
	}
}

func TestRenderGoalSlicePlanMarkdown(t *testing.T) {
	goal := GoalState{ID: "goal-1", Status: goalStatusActive, Objective: "ship export"}
	plan := GoalSlicePlan{
		ObjectiveRestated: "ship export",
		Slices: []GoalSlice{
			{Name: "Routing", Outcome: "command exists", Risk: goalSliceRiskLow},
			{Name: "Writer", Outcome: "archive written", Risk: goalSliceRiskHigh},
		},
	}
	md := renderGoalSlicePlanMarkdown(goal, plan)
	for _, want := range []string{"# Goal Slice Plan", "Routing", "Writer", "Ready now", "Execution order"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestReadySlicesAfterCompletion(t *testing.T) {
	plan := GoalSlicePlan{
		Slices: []GoalSlice{
			{ID: "a", Name: "A", Outcome: "a", Status: goalSliceStatusComplete},
			{ID: "b", Name: "B", Outcome: "b", Status: goalSliceStatusPending, DependsOn: []string{"a"}},
			{ID: "c", Name: "C", Outcome: "c", Status: goalSliceStatusPending, DependsOn: []string{"b"}},
		},
	}
	plan.Normalize()
	ready := plan.ReadySlices()
	if len(ready) != 1 || ready[0].ID != "b" {
		t.Fatalf("expected B ready after A complete, got %#v", ready)
	}
}

func TestGenerateAndAttachGoalPlanParsesSlices(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	var output bytes.Buffer
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
		goalReply: func(ctx context.Context, prompt string) (string, error) {
			_ = ctx
			_ = prompt
			return `
Objective
- Ship session export

Non-goals
- Import path

Slices
1. Routing
   Outcome: command registered
   Scope: router only
   Likely files: commands.go
   Acceptance: help lists export
   Validation: go test
   Docs: none
   Risk: low

2. Writer
   Outcome: archive written
   Scope: export writer
   Likely files: session_export.go
   Acceptance: archive has session json
   Validation: unit test
   Docs: none
   Risk: medium
   Depends on: routing

Recommended first slice
- Routing

Open questions
- None
`, nil
		},
	}
	if err := rt.handleGoalCommand("--no-run ship session export"); err != nil {
		t.Fatalf("handleGoalCommand: %v", err)
	}
	goal, ok := session.ActiveGoal()
	if !ok {
		t.Fatal("expected active goal")
	}
	if goal.SlicePlan == nil || len(goal.SlicePlan.Slices) != 2 {
		t.Fatalf("expected 2-slice plan, got %#v", goal.SlicePlan)
	}
	if len(goal.Plan) < 2 {
		t.Fatalf("expected plan projection from slices, got %#v", goal.Plan)
	}
	slicesPath := filepath.Join(root, ".kernforge", "goals", "latest.slices.md")
	data, err := os.ReadFile(slicesPath)
	if err != nil {
		t.Fatalf("read slices artifact: %v", err)
	}
	if !strings.Contains(string(data), "Routing") || !strings.Contains(string(data), "Writer") {
		t.Fatalf("slices artifact missing content:\n%s", data)
	}
	md, err := os.ReadFile(filepath.Join(root, ".kernforge", "goals", "latest.md"))
	if err != nil {
		t.Fatalf("read goal md: %v", err)
	}
	if !strings.Contains(string(md), "## Slice DAG") {
		t.Fatalf("goal markdown missing Slice DAG section:\n%s", md)
	}
}
