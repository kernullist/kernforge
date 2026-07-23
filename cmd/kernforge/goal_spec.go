package main

import (
	"fmt"
	"strings"
)

// GoalAcceptanceSpec is the compiled, durable acceptance contract for a goal.
// Process-meta CompletionCriteria remain secondary; Spec.Criteria (plus
// UserCriteria) are what semantic completion must actually judge.
type GoalAcceptanceSpec struct {
	ObjectiveRestated string          `json:"objective_restated,omitempty"`
	NonGoals          []string        `json:"non_goals,omitempty"`
	Criteria          []GoalCriterion `json:"criteria,omitempty"`
	RequiredArtifacts []string        `json:"required_artifacts,omitempty"`
	RiskClass         string          `json:"risk_class,omitempty"`    // low|medium|high|critical
	ResearchMode      string          `json:"research_mode,omitempty"` // none|bounded|aggressive
	CompiledFrom      string          `json:"compiled_from,omitempty"`
}

// GoalCriterion is one measurable or reviewable acceptance item.
type GoalCriterion struct {
	ID         string `json:"id,omitempty"`
	Text       string `json:"text"`
	Kind       string `json:"kind,omitempty"` // test|artifact|behavior|manual|process
	VerifyHint string `json:"verify_hint,omitempty"`
	Source     string `json:"source,omitempty"` // user|compiler|process
}

const (
	goalRiskLow      = "low"
	goalRiskMedium   = "medium"
	goalRiskHigh     = "high"
	goalRiskCritical = "critical"

	goalResearchNone       = "none"
	goalResearchBounded    = "bounded"
	goalResearchAggressive = "aggressive"

	goalCriterionKindTest     = "test"
	goalCriterionKindArtifact = "artifact"
	goalCriterionKindBehavior = "behavior"
	goalCriterionKindManual   = "manual"
	goalCriterionKindProcess  = "process"
)

// compileGoalAcceptanceSpec builds a primary acceptance contract from the
// objective and any explicit user criteria. It always emits at least one
// substance criterion so completion cannot rest on process gates alone.
func compileGoalAcceptanceSpec(objective string, userCriteria []string) GoalAcceptanceSpec {
	objective = strings.TrimSpace(objective)
	userCriteria = normalizeTaskStateList(userCriteria, 16)
	lower := strings.ToLower(objective)

	spec := GoalAcceptanceSpec{
		ObjectiveRestated: firstNonBlankString(objective, "(empty objective)"),
		RiskClass:         classifyGoalRiskClass(lower),
		ResearchMode:      classifyGoalResearchMode(lower),
		CompiledFrom:      "compileGoalAcceptanceSpec",
	}

	// User-provided criteria are authoritative and listed first.
	for i, item := range userCriteria {
		spec.Criteria = append(spec.Criteria, GoalCriterion{
			ID:     fmt.Sprintf("user-%02d", i+1),
			Text:   item,
			Kind:   goalCriterionKindManual,
			Source: "user",
		})
	}

	// Always require substance: the objective itself must be demonstrably done.
	if !criteriaContainSubstance(spec.Criteria, objective) {
		spec.Criteria = append(spec.Criteria, GoalCriterion{
			ID:         "substance-01",
			Text:       "The workspace demonstrates the stated objective is satisfied with concrete evidence (behavior, artifacts, or tests) — not only that process gates, builds, or pre-existing tests passed.",
			Kind:       goalCriterionKindBehavior,
			VerifyHint: "Inspect changed paths and runtime/test evidence against the objective wording.",
			Source:     "compiler",
		})
	}

	if containsAny(lower, "test", "tests", "verify", "verification", "build", "테스트", "검증", "빌드") {
		spec.Criteria = append(spec.Criteria, GoalCriterion{
			ID:         "verify-01",
			Text:       "Relevant build and/or tests for the changed scope pass, or a concrete blocker explains why they cannot run.",
			Kind:       goalCriterionKindTest,
			VerifyHint: "Prefer targeted tests for touched packages; broaden only when justified.",
			Source:     "compiler",
		})
	}

	if containsAny(lower, ".md", "markdown", "document", "readme", "changelog", "문서", "보고서", "계획서") {
		spec.Criteria = append(spec.Criteria, GoalCriterion{
			ID:         "artifact-01",
			Text:       "Requested document or markdown artifact exists with substantive content covering the objective (not placeholder/TODO-only).",
			Kind:       goalCriterionKindArtifact,
			VerifyHint: "Open the claimed artifact path and check topic coverage.",
			Source:     "compiler",
		})
		if path := firstDocumentPathHint(objective); path != "" {
			spec.RequiredArtifacts = append(spec.RequiredArtifacts, path)
		}
	}

	if containsAny(lower, "fix", "bug", "regression", "crash", "고쳐", "수정", "버그", "크래시") {
		spec.Criteria = append(spec.Criteria, GoalCriterion{
			ID:         "fix-01",
			Text:       "The reported defect is addressed in source with a clear root-cause-oriented change, not only a cosmetic or logging tweak.",
			Kind:       goalCriterionKindBehavior,
			VerifyHint: "Diff the touched source against the failure description.",
			Source:     "compiler",
		})
	}

	// Non-goals reduce silent scope growth.
	spec.NonGoals = []string{
		"Unrelated refactors outside the objective surface",
		"Drive-by dependency upgrades unless required by the objective",
		"Pushing, opening PRs, or external network publish unless the objective explicitly asks",
	}

	spec.Normalize()
	return spec
}

func (s *GoalAcceptanceSpec) Normalize() {
	if s == nil {
		return
	}
	s.ObjectiveRestated = strings.TrimSpace(s.ObjectiveRestated)
	s.NonGoals = normalizeTaskStateList(s.NonGoals, 12)
	s.RequiredArtifacts = uniqueStrings(s.RequiredArtifacts)
	if len(s.RequiredArtifacts) > 16 {
		s.RequiredArtifacts = s.RequiredArtifacts[:16]
	}
	s.RiskClass = canonicalGoalRiskClass(s.RiskClass)
	s.ResearchMode = canonicalGoalResearchMode(s.ResearchMode)
	s.CompiledFrom = strings.TrimSpace(s.CompiledFrom)
	out := make([]GoalCriterion, 0, len(s.Criteria))
	seen := map[string]bool{}
	for i, c := range s.Criteria {
		c.Text = strings.TrimSpace(c.Text)
		if c.Text == "" {
			continue
		}
		key := strings.ToLower(c.Text)
		if seen[key] {
			continue
		}
		seen[key] = true
		if strings.TrimSpace(c.ID) == "" {
			c.ID = fmt.Sprintf("c-%02d", i+1)
		}
		c.Kind = canonicalGoalCriterionKind(c.Kind)
		c.Source = strings.TrimSpace(c.Source)
		c.VerifyHint = strings.TrimSpace(c.VerifyHint)
		out = append(out, c)
		if len(out) >= 24 {
			break
		}
	}
	s.Criteria = out
}

func (s GoalAcceptanceSpec) PrimaryCriteriaTexts() []string {
	s.Normalize()
	out := make([]string, 0, len(s.Criteria))
	for _, c := range s.Criteria {
		if c.Kind == goalCriterionKindProcess {
			continue
		}
		out = append(out, c.Text)
	}
	return out
}

func (s GoalAcceptanceSpec) HasSubstanceCriteria() bool {
	for _, c := range s.PrimaryCriteriaTexts() {
		if strings.TrimSpace(c) != "" {
			return true
		}
	}
	return false
}

func canonicalGoalRiskClass(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case goalRiskLow, goalRiskMedium, goalRiskHigh, goalRiskCritical:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return goalRiskMedium
	}
}

func canonicalGoalResearchMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case goalResearchNone, goalResearchBounded, goalResearchAggressive:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return goalResearchNone
	}
}

func canonicalGoalCriterionKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case goalCriterionKindTest, goalCriterionKindArtifact, goalCriterionKindBehavior, goalCriterionKindManual, goalCriterionKindProcess:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return goalCriterionKindBehavior
	}
}

func classifyGoalRiskClass(lowerObjective string) string {
	if containsAny(lowerObjective,
		"production", "prod ", "kernel", "driver", "hypervisor", "tpm", "secure boot",
		"credential", "secret", "pki", "signing", "anti-cheat", "anticheat", "bypass",
		"프로덕션", "커널", "드라이버", "안티치트", "서명",
	) {
		if containsAny(lowerObjective, "production", "prod ", "프로덕션", "credential", "secret") {
			return goalRiskCritical
		}
		return goalRiskHigh
	}
	if containsAny(lowerObjective, "readme", "docs/", "문서만", "typo", "comment") {
		return goalRiskLow
	}
	return goalRiskMedium
}

func classifyGoalResearchMode(lowerObjective string) string {
	// "조사" alone is too common in Korean for "inspect the code"; require a
	// research-oriented collocates so bug-fix goals are not mis-tagged.
	strongResearch := containsAny(lowerObjective,
		"latest", "current research", "state of the art", "survey", " lit review",
		"literature review", "web research", "최신 동향", "최신 기술", "기술 동향",
		"리서치", "연구 노트", "연구노트", "state-of-the-art",
	)
	weakResearch := containsAny(lowerObjective, "동향", "survey", "research")
	inspectOnly := containsAny(lowerObjective, "조사해서", "조사한 뒤", "조사 후", "코드를 조사", "inspect the code")
	if strongResearch || (weakResearch && !inspectOnly && containsAny(lowerObjective, "최신", "latest", "current", "동향")) {
		if containsAny(lowerObjective, "exhaustive", "comprehensive survey", "전수", "광범위") {
			return goalResearchAggressive
		}
		return goalResearchBounded
	}
	return goalResearchNone
}

func criteriaContainSubstance(criteria []GoalCriterion, objective string) bool {
	if len(criteria) == 0 {
		return false
	}
	obj := strings.ToLower(strings.TrimSpace(objective))
	for _, c := range criteria {
		text := strings.ToLower(strings.TrimSpace(c.Text))
		if text == "" {
			continue
		}
		if c.Source == "user" {
			return true
		}
		if strings.Contains(text, "objective") || strings.Contains(text, "목표") {
			return true
		}
		if obj != "" && len([]rune(obj)) >= 12 {
			// Compare on runes so multi-byte objective text is not split mid-character.
			runes := []rune(obj)
			if len(runes) > 32 {
				runes = runes[:32]
			}
			if strings.Contains(text, string(runes)) {
				return true
			}
		}
	}
	return false
}

func firstDocumentPathHint(objective string) string {
	fields := strings.Fields(objective)
	for _, field := range fields {
		clean := strings.Trim(field, "\"'`")
		lower := strings.ToLower(clean)
		if strings.HasSuffix(lower, ".md") || strings.Contains(lower, ".md/") {
			return strings.ReplaceAll(clean, "\\", "/")
		}
	}
	return ""
}

// ensureGoalAcceptanceSpec populates or refreshes Spec on the goal. Explicit
// UserCriteria always recompile into Spec so CLI flags stay authoritative.
// Spec is also recompiled when the objective text diverges from ObjectiveRestated.
func ensureGoalAcceptanceSpec(goal *GoalState) {
	if goal == nil {
		return
	}
	objective := strings.TrimSpace(goal.Objective)
	if goal.AcceptanceSpec != nil &&
		len(goal.AcceptanceSpec.Criteria) > 0 &&
		len(goal.UserCriteria) == 0 &&
		strings.EqualFold(strings.TrimSpace(goal.AcceptanceSpec.ObjectiveRestated), objective) &&
		objective != "" {
		goal.AcceptanceSpec.Normalize()
		return
	}
	spec := compileGoalAcceptanceSpec(goal.Objective, goal.UserCriteria)
	goal.AcceptanceSpec = &spec
}

// renderGoalAcceptanceCriteriaSection is the primary checklist for implement and
// semantic-review prompts. Prefer Spec; fall back to UserCriteria only.
func renderGoalAcceptanceCriteriaSection(goal GoalState) string {
	ensureGoalAcceptanceSpec(&goal)
	texts := []string{}
	if goal.AcceptanceSpec != nil {
		texts = goal.AcceptanceSpec.PrimaryCriteriaTexts()
	}
	if len(texts) == 0 {
		texts = normalizeTaskStateList(goal.UserCriteria, 16)
	}
	if len(texts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Acceptance criteria (EVERY non-process item must be satisfied with evidence; if any is unmet or unverifiable, do not treat the goal as complete):\n")
	for _, item := range texts {
		fmt.Fprintf(&b, "- [ ] %s\n", item)
	}
	if goal.AcceptanceSpec != nil {
		if risk := strings.TrimSpace(goal.AcceptanceSpec.RiskClass); risk != "" {
			fmt.Fprintf(&b, "Risk class: %s\n", risk)
		}
		if mode := strings.TrimSpace(goal.AcceptanceSpec.ResearchMode); mode != "" && mode != goalResearchNone {
			fmt.Fprintf(&b, "Research mode: %s\n", mode)
		}
		if len(goal.AcceptanceSpec.NonGoals) > 0 {
			b.WriteString("Non-goals:\n")
			for _, item := range goal.AcceptanceSpec.NonGoals {
				fmt.Fprintf(&b, "- %s\n", item)
			}
		}
	}
	b.WriteString("\n")
	return b.String()
}
