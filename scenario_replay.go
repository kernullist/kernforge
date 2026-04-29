package main

import (
	"fmt"
	"strings"
	"time"
)

type ScenarioReplayReport struct {
	GeneratedAt time.Time              `json:"generated_at,omitempty"`
	Scenarios   []ScenarioReplayCase   `json:"scenarios,omitempty"`
	Checks      []string               `json:"checks,omitempty"`
	Findings    []CodingHarnessFinding `json:"findings,omitempty"`
}

type ScenarioReplayCase struct {
	Trigger        string   `json:"trigger,omitempty"`
	Expected       string   `json:"expected,omitempty"`
	Observed       string   `json:"observed,omitempty"`
	Invariant      string   `json:"invariant,omitempty"`
	PromptKeywords []string `json:"prompt_keywords,omitempty"`
	ReplyMatches   []string `json:"reply_matches,omitempty"`
}

func (a *Agent) buildScenarioReplayReport(reply string) ScenarioReplayReport {
	report := ScenarioReplayReport{GeneratedAt: time.Now()}
	if a == nil || a.Session == nil {
		return report
	}
	prompt := codingHarnessSourcePrompt(a.Session)
	if !scenarioReplayPromptLooksRelevant(prompt) {
		return report
	}
	scenario := buildScenarioReplayCase(prompt, reply)
	report.Scenarios = append(report.Scenarios, scenario)
	report.Checks = append(report.Checks, "scenario extracted from latest user request")

	changedCode := filterCodeLikePaths(collectTestImpactChangedPaths(a.Session))
	claimsResolution := replyClaimsResolution(reply)
	if replyClaimsFixOrCompletion(reply) && len(scenario.ReplyMatches) < 2 {
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Final answer does not address the reported scenario",
			Detail:   "The answer claims a result but does not clearly connect it back to the trigger, expected behavior, and observed failure from the user request.",
		})
	}
	if claimsResolution && len(changedCode) > 0 && !sessionHasSuccessfulVerificationEvidence(a.Session) && !replyMentionsScenarioReplayNotRun(reply) {
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Scenario replay outcome is missing",
			Detail:   "Code-like files changed for a bug scenario, but the final answer neither records a scenario replay/verification result nor says the replay was not run.",
		})
	}
	if replyClaimsRootCause(reply) && !scenarioReplyMentionsCausalInvariant(reply, scenario) {
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: "warning",
			Title:    "Root-cause answer has weak scenario bridge",
			Detail:   "The answer should explicitly state how the proposed cause violates the scenario invariant: " + scenario.Invariant,
		})
	}
	report.Normalize()
	return report
}

func scenarioReplayPromptLooksRelevant(prompt string) bool {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return false
	}
	lower := strings.ToLower(prompt)
	hasTrigger := rootCausePromptHasTrigger(prompt)
	hasObserved := rootCausePromptHasObservedFailure(prompt)
	hasExpected := rootCausePromptHasExpectedBehavior(prompt, hasObserved, hasTrigger)
	hasObservedBridge := containsAny(lower, "observed", "actual", "but", "instead", "관찰", "실제", "그런데", "하지만")
	hasExplicitScenario := containsAny(lower, "scenario", "repro", "reproduction", "시나리오", "재현")
	if scenarioReplayPromptLooksLikeInstructionalBoilerplate(prompt) && !hasObservedBridge && !hasExplicitScenario {
		return false
	}
	if hasTrigger && hasObserved && (hasExpected || hasObservedBridge || hasExplicitScenario) {
		return true
	}
	if hasTrigger && hasExpected && hasObservedBridge {
		return true
	}
	return hasExplicitScenario && hasObserved
}

func scenarioReplayPromptLooksLikeInstructionalBoilerplate(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"execution requirements:",
		"if you cannot finish cleanly",
		"if you cannot finish",
		"explain the blocker and the remaining work",
		"run relevant verification before finishing when practical",
	)
}

func buildScenarioReplayCase(prompt string, reply string) ScenarioReplayCase {
	keywords := codingHarnessMeaningfulTokens(prompt)
	return ScenarioReplayCase{
		Trigger:        scenarioClause(prompt, []string{"when", "after", "while", "during", "if", "repro", "하면", "하다 보면", "후", "뒤", "동안", "sc stop"}),
		Expected:       scenarioClause(prompt, []string{"expected", "should", "must", "invariant", "limit", "정상", "기대", "해야", "제한", "원래"}),
		Observed:       scenarioClause(prompt, []string{"but", "observed", "actual", "fails", "missing", "cannot", "그런데", "하지만", "관찰", "실제", "못", "않", "누락", "초과"}),
		Invariant:      inferScenarioInvariant(prompt),
		PromptKeywords: keywords,
		ReplyMatches:   codingHarnessMatchedTokens(reply, keywords),
	}
}

func scenarioClause(text string, markers []string) string {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	best := -1
	for _, marker := range markers {
		index := strings.Index(lower, strings.ToLower(marker))
		if index >= 0 && (best < 0 || index < best) {
			best = index
		}
	}
	if best < 0 {
		return compactPromptSection(trimmed, 180)
	}
	return compactPromptSection(strings.TrimSpace(trimmed[best:]), 180)
}

func inferScenarioInvariant(prompt string) string {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	switch {
	case containsAny(lower, "limit", "quota", "capacity", "제한", "한도", "초과", "넘"):
		return "The reported limit or capacity invariant must remain enforced after the trigger sequence."
	case containsAny(lower, "document", "file", "artifact", "문서", "파일", "산출물", "생성하질 않아", "누락"):
		return "A requested artifact must be created or the answer must explicitly report why it could not be created."
	case containsAny(lower, "sc stop", "service", "stop", "terminate", "종료", "서비스"):
		return "The stop command or stop event must transition the process to a stopped state or report a concrete blocker."
	default:
		return "The proposed change or root cause must explain the observed failure under the user's trigger conditions."
	}
}

func replyMentionsScenarioReplayNotRun(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"not reproduced", "not replayed", "replay was not run", "scenario was not run", "not run", "not verified", "verification not run",
		"재현하지", "재현은 하지", "시나리오를 실행하지", "실행하지 않았", "검증하지 않았", "검증은 실행하지 않았", "테스트하지 않았",
	)
}

func replyClaimsFixOrCompletion(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"fixed", "resolved", "implemented", "patched", "done", "completed",
		"수정", "해결", "구현", "패치", "완료", "끝냈",
	)
}

func scenarioReplyMentionsCausalInvariant(reply string, scenario ScenarioReplayCase) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return false
	}
	invariantTokens := codingHarnessMeaningfulTokens(scenario.Invariant)
	if len(codingHarnessMatchedTokens(reply, invariantTokens)) > 0 {
		return true
	}
	return containsAny(lower,
		"trigger", "expected", "observed", "invariant", "causal", "because", "leads to",
		"트리거", "기대", "관찰", "불변식", "인과", "때문", "이어져",
	)
}

func (r *ScenarioReplayReport) Normalize() {
	if r == nil {
		return
	}
	for i := range r.Scenarios {
		r.Scenarios[i].Normalize()
	}
	r.Checks = normalizeTaskStateList(r.Checks, 8)
	r.Findings = normalizeCodingHarnessFindings(r.Findings)
}

func (s *ScenarioReplayCase) Normalize() {
	if s == nil {
		return
	}
	s.Trigger = strings.TrimSpace(s.Trigger)
	s.Expected = strings.TrimSpace(s.Expected)
	s.Observed = strings.TrimSpace(s.Observed)
	s.Invariant = strings.TrimSpace(s.Invariant)
	s.PromptKeywords = normalizeTaskStateList(s.PromptKeywords, 24)
	s.ReplyMatches = normalizeTaskStateList(s.ReplyMatches, 24)
}

func (r ScenarioReplayReport) RenderPromptSection() string {
	r.Normalize()
	lines := make([]string, 0, 8)
	for index, scenario := range r.Scenarios {
		lines = append(lines, fmt.Sprintf("- Scenario %d: trigger=%s | expected=%s | observed=%s", index+1, compactPromptSection(scenario.Trigger, 120), compactPromptSection(scenario.Expected, 120), compactPromptSection(scenario.Observed, 120)))
		if scenario.Invariant != "" {
			lines = append(lines, "- Invariant: "+compactPromptSection(scenario.Invariant, 180))
		}
		if len(scenario.ReplyMatches) > 0 {
			lines = append(lines, "- Reply scenario matches: "+strings.Join(limitStrings(scenario.ReplyMatches, 8), ", "))
		}
	}
	if len(r.Checks) > 0 {
		lines = append(lines, "- Checks: "+strings.Join(r.Checks, " | "))
	}
	for _, finding := range r.Findings {
		lines = append(lines, fmt.Sprintf("- [%s] %s: %s", finding.Severity, finding.Title, compactPromptSection(finding.Detail, 220)))
	}
	return strings.Join(lines, "\n")
}
