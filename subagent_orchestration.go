package main

import (
	"fmt"
	"strings"
	"time"
)

type SubagentOrchestrationReport struct {
	GeneratedAt            time.Time              `json:"generated_at,omitempty"`
	WorkerEvidenceCount    int                    `json:"worker_evidence_count,omitempty"`
	CausalEvidenceCount    int                    `json:"causal_evidence_count,omitempty"`
	ReviewerValidatedCount int                    `json:"reviewer_validated_count,omitempty"`
	ReviewFailures         int                    `json:"review_failures,omitempty"`
	Summaries              []string               `json:"summaries,omitempty"`
	Findings               []CodingHarnessFinding `json:"findings,omitempty"`
}

func (a *Agent) buildSubagentOrchestrationReport(reply string) SubagentOrchestrationReport {
	report := SubagentOrchestrationReport{GeneratedAt: time.Now()}
	if a == nil || a.Session == nil {
		return report
	}
	prompt := codingHarnessSourcePrompt(a.Session)
	strict := subagentOrchestrationStrict(prompt)
	report.addAnalysisRunEvidence(a.Session.LastAnalysis, reply)
	if a.Session.TaskGraph != nil {
		for _, node := range a.Session.TaskGraph.Nodes {
			report.addTaskNodeWorkerEvidence(node, strict, reply)
		}
	}
	if strict && report.WorkerEvidenceCount > 0 && report.CausalEvidenceCount == 0 && replyClaimsRootCause(reply) {
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "No worker evidence validates symptom causality",
			Detail:   "The final answer claims a root cause, but recorded worker evidence does not include a concrete causal bridge from trigger to user-visible symptom.",
		})
	}
	report.Normalize()
	return report
}

func subagentOrchestrationStrict(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	return codingHarnessPromptHasBugScenario(prompt) || containsAny(lower,
		"worker", "subagent", "reviewer", "root cause", "root-cause",
		"워커", "에이전트", "리뷰어", "원인", "근본 원인",
	)
}

func (r *SubagentOrchestrationReport) addAnalysisRunEvidence(summary *ProjectAnalysisSummary, reply string) {
	if r == nil || summary == nil {
		return
	}
	mode := strings.TrimSpace(strings.ToLower(summary.Mode))
	if mode == "" {
		mode = "analysis"
	}
	r.Summaries = append(r.Summaries, fmt.Sprintf("analysis run mode=%s shards=%d approved=%d review_failures=%d", mode, summary.TotalShards, summary.ApprovedShards, summary.ReviewFailures))
	if strings.EqualFold(mode, "root-cause") {
		r.WorkerEvidenceCount += summary.TotalShards
		r.ReviewerValidatedCount += summary.ApprovedShards
		r.ReviewFailures += summary.ReviewFailures
		if summary.ApprovedShards > 0 {
			r.CausalEvidenceCount += summary.ApprovedShards
		}
		if summary.ReviewFailures > 0 && !replyMentionsAnalysisReviewIssues(reply) {
			r.Findings = append(r.Findings, CodingHarnessFinding{
				Severity: "blocker",
				Title:    "Root-cause reviewer issues are not disclosed",
				Detail:   fmt.Sprintf("The last root-cause analysis had %d reviewer issue(s), but the final answer does not mention reduced confidence or review failures.", summary.ReviewFailures),
			})
		}
		if summary.TotalShards > 0 && summary.ApprovedShards == 0 && replyClaimsRootCause(reply) {
			r.Findings = append(r.Findings, CodingHarnessFinding{
				Severity: "blocker",
				Title:    "No reviewer-approved root-cause shard",
				Detail:   "The final answer claims a root cause, but the last root-cause analysis had no reviewer-approved shard.",
			})
		}
	}
}

func (r *SubagentOrchestrationReport) addTaskNodeWorkerEvidence(node TaskNode, strict bool, reply string) {
	if r == nil {
		return
	}
	node.Normalize()
	evidence := []struct {
		Kind string
		Text string
	}{
		{Kind: "micro", Text: node.MicroWorkerBrief},
		{Kind: "read_only", Text: node.ReadOnlyWorkerSummary},
		{Kind: "editable", Text: node.EditableWorkerSummary},
	}
	for _, item := range evidence {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		r.WorkerEvidenceCount++
		score := subagentCausalEvidenceScore(text)
		if score >= 4 {
			r.CausalEvidenceCount++
		}
		if strings.Contains(strings.ToLower(text), "reviewer") && containsAny(strings.ToLower(text), "approved", "validated", "accepted", "승인", "검증", "타당") {
			r.ReviewerValidatedCount++
		}
		r.Summaries = append(r.Summaries, fmt.Sprintf("%s:%s score=%d %s", node.ID, item.Kind, score, compactPromptSection(text, 120)))
		if strict && replyClaimsRootCause(reply) && score < 4 {
			r.Findings = append(r.Findings, CodingHarnessFinding{
				Severity: "blocker",
				Title:    "Worker evidence lacks causal validation",
				Detail:   fmt.Sprintf("%s %s worker evidence is too weak to support a root-cause claim: %s", node.ID, item.Kind, compactPromptSection(text, 180)),
			})
		}
	}
}

func subagentCausalEvidenceScore(text string) int {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return 0
	}
	score := 0
	if containsAny(lower, "trigger", "when", "after", "input", "request", "event", "repro", "트리거", "입력", "요청", "이벤트", "재현") {
		score++
	}
	if containsAny(lower, "invalid state", "state", "db", "config", "persisted", "stale", "null", "empty", "out of range", "상태", "db", "설정", "영속", "널", "비어", "범위") {
		score++
	}
	if containsAny(lower, "transition", "flow", "path", "call", "branch", "race", "sequence", "전이", "흐름", "경로", "호출", "분기", "경쟁", "시퀀스") {
		score++
	}
	if containsAny(lower, "guard", "check", "validation", "missing", "not checked", "allows", "가드", "검사", "검증", "누락", "허용") {
		score++
	}
	if containsAny(lower, "symptom", "failure", "user-visible", "observed", "hang", "crash", "missing file", "증상", "실패", "관찰", "멈", "크래시", "파일 누락") {
		score++
	}
	if containsAny(lower, "evidence", "file", "line", ".go", ".cpp", ".h", ".ts", ".py", "증거", "파일", "라인") {
		score++
	}
	return score
}

func replyMentionsAnalysisReviewIssues(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"review failure", "review failures", "review issue", "reviewer issue", "reduced confidence", "low confidence", "not fully reviewed",
		"리뷰 실패", "리뷰 이슈", "리뷰어 이슈", "신뢰도", "낮은 확신", "완전히 검증되지",
	)
}

func (r *SubagentOrchestrationReport) Normalize() {
	if r == nil {
		return
	}
	r.Summaries = normalizeTaskStateList(r.Summaries, 12)
	r.Findings = normalizeCodingHarnessFindings(r.Findings)
}

func (r SubagentOrchestrationReport) RenderPromptSection() string {
	r.Normalize()
	lines := make([]string, 0, 8)
	if r.WorkerEvidenceCount > 0 || r.ReviewFailures > 0 {
		lines = append(lines, fmt.Sprintf("- Subagent evidence: workers=%d causal=%d reviewer_validated=%d review_failures=%d", r.WorkerEvidenceCount, r.CausalEvidenceCount, r.ReviewerValidatedCount, r.ReviewFailures))
	}
	if len(r.Summaries) > 0 {
		lines = append(lines, "- Summaries: "+strings.Join(r.Summaries, " | "))
	}
	for _, finding := range r.Findings {
		lines = append(lines, fmt.Sprintf("- [%s] %s: %s", finding.Severity, finding.Title, compactPromptSection(finding.Detail, 220)))
	}
	return strings.Join(lines, "\n")
}
