package main

import (
	"strings"
	"testing"
)

func TestCompileGoalAcceptanceSpecAlwaysHasSubstance(t *testing.T) {
	spec := compileGoalAcceptanceSpec("Fix the buffer overflow in parser.go and add a regression test", nil)
	if !spec.HasSubstanceCriteria() {
		t.Fatal("expected substance criteria")
	}
	if spec.RiskClass == "" {
		t.Fatal("expected risk class")
	}
	joined := strings.Join(spec.PrimaryCriteriaTexts(), "\n")
	if !strings.Contains(joined, "objective") && !strings.Contains(strings.ToLower(joined), "workspace") {
		t.Fatalf("expected substance criterion about objective, got %q", joined)
	}
	if !strings.Contains(joined, "test") && !strings.Contains(joined, "defect") && !strings.Contains(joined, "regression") {
		// compiler should add verify and/or fix criteria for this objective
		if len(spec.Criteria) < 2 {
			t.Fatalf("expected multiple criteria for fix+test objective, got %#v", spec.Criteria)
		}
	}
}

func TestCompileGoalAcceptanceSpecUserCriteriaFirst(t *testing.T) {
	spec := compileGoalAcceptanceSpec("ship feature", []string{"API returns 200", "docs updated"})
	if len(spec.Criteria) < 2 {
		t.Fatalf("expected user criteria present, got %#v", spec.Criteria)
	}
	if spec.Criteria[0].Source != "user" || spec.Criteria[0].Text != "API returns 200" {
		t.Fatalf("user criteria must lead the checklist, got %#v", spec.Criteria[0])
	}
}

func TestCompileGoalAcceptanceSpecRiskAndResearch(t *testing.T) {
	high := compileGoalAcceptanceSpec("Harden kernel driver telemetry against bypass", nil)
	if high.RiskClass != goalRiskHigh && high.RiskClass != goalRiskCritical {
		t.Fatalf("expected high/critical risk, got %q", high.RiskClass)
	}
	research := compileGoalAcceptanceSpec("최신 TPM attestation 동향 조사 후 연구 노트 작성", nil)
	if research.ResearchMode != goalResearchBounded && research.ResearchMode != goalResearchAggressive {
		t.Fatalf("expected research mode for survey objective, got %q", research.ResearchMode)
	}
	// Local code inspection must not be tagged as research mode.
	inspect := compileGoalAcceptanceSpec("코드를 조사해서 버그를 고쳐줘", nil)
	if inspect.ResearchMode != goalResearchNone {
		t.Fatalf("code-inspect objective must not set research mode, got %q", inspect.ResearchMode)
	}
	doc := compileGoalAcceptanceSpec("docs/plan/x.md 설계 문서 보강", nil)
	if doc.RiskClass != goalRiskLow && doc.RiskClass != goalRiskMedium {
		t.Fatalf("doc objective should not be critical, got %q", doc.RiskClass)
	}
	hasArtifact := false
	for _, c := range doc.Criteria {
		if c.Kind == goalCriterionKindArtifact {
			hasArtifact = true
		}
	}
	if !hasArtifact {
		t.Fatalf("doc objective should emit artifact criterion, got %#v", doc.Criteria)
	}
}

func TestEnsureGoalAcceptanceSpecIdempotent(t *testing.T) {
	goal := GoalState{Objective: "implement feature X", UserCriteria: []string{"unit tests pass"}}
	ensureGoalAcceptanceSpec(&goal)
	if goal.AcceptanceSpec == nil || len(goal.AcceptanceSpec.Criteria) == 0 {
		t.Fatal("expected compiled spec")
	}
	firstID := goal.AcceptanceSpec.Criteria[0].ID
	ensureGoalAcceptanceSpec(&goal)
	if goal.AcceptanceSpec.Criteria[0].ID != firstID && goal.UserCriteria[0] != "unit tests pass" {
		t.Fatalf("re-ensure should keep stable user-led checklist, got %#v", goal.AcceptanceSpec.Criteria)
	}
}

func TestGoalNormalizeCompilesMissingSpec(t *testing.T) {
	goal := GoalState{Objective: "fix crash in worker", ID: "goal-1"}
	goal.Normalize()
	if goal.AcceptanceSpec == nil || !goal.AcceptanceSpec.HasSubstanceCriteria() {
		t.Fatalf("Normalize must compile acceptance spec, got %#v", goal.AcceptanceSpec)
	}
}
