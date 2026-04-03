package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const defaultSimulationMaxEntries = 300

type SimulationFinding struct {
	Kind               string            `json:"kind"`
	Category           string            `json:"category,omitempty"`
	Subject            string            `json:"subject"`
	Severity           string            `json:"severity,omitempty"`
	SignalClass        string            `json:"signal_class,omitempty"`
	RiskScore          int               `json:"risk_score,omitempty"`
	Message            string            `json:"message,omitempty"`
	RecommendedActions []string          `json:"recommended_actions,omitempty"`
	Attributes         map[string]string `json:"attributes,omitempty"`
}

type SimulationResult struct {
	ID                     string              `json:"id"`
	Workspace              string              `json:"workspace"`
	Profile                string              `json:"profile"`
	Target                 string              `json:"target,omitempty"`
	CreatedAt              time.Time           `json:"created_at"`
	SourceEvidenceIDs      []string            `json:"source_evidence_ids,omitempty"`
	SourceInvestigationIDs []string            `json:"source_investigation_ids,omitempty"`
	Findings               []SimulationFinding `json:"findings,omitempty"`
	Summary                string              `json:"summary,omitempty"`
	Tags                   []string            `json:"tags,omitempty"`
}

type SimulationStore struct {
	Path       string
	MaxEntries int
}

type SimulationDashboardSummary struct {
	Scope         string
	TotalRuns     int
	ByProfile     []NamedCount
	ByCategory    []NamedCount
	BySeverity    []NamedCount
	BySignalClass []NamedCount
	TopSubjects   []NamedCount
	TopActions    []NamedCount
	MaxRisk       int
	Recent        []SimulationResult
	LastUpdated   time.Time
}

func NewSimulationStore() *SimulationStore {
	return &SimulationStore{
		Path:       filepath.Join(userConfigDir(), "simulations.json"),
		MaxEntries: defaultSimulationMaxEntries,
	}
}

func (s *SimulationStore) Append(result SimulationResult) (SimulationResult, error) {
	if s == nil {
		return SimulationResult{}, nil
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	result = normalizeSimulationResult(result)
	items, err := s.load()
	if err != nil {
		return SimulationResult{}, err
	}
	items = append(items, result)
	maxEntries := s.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultSimulationMaxEntries
	}
	if len(items) > maxEntries {
		items = append([]SimulationResult(nil), items[len(items)-maxEntries:]...)
	}
	if err := s.save(items); err != nil {
		return SimulationResult{}, err
	}
	return result, nil
}

func (s *SimulationStore) ListRecent(workspace string, limit int) ([]SimulationResult, error) {
	items, err := s.load()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	var out []SimulationResult
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if workspace != "" && workspaceAffinityScore(workspace, item.Workspace) == 0 {
			continue
		}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *SimulationStore) Get(id string) (SimulationResult, bool, error) {
	items, err := s.load()
	if err != nil {
		return SimulationResult{}, false, err
	}
	query := strings.TrimSpace(id)
	for _, item := range items {
		if strings.EqualFold(item.ID, query) {
			return item, true, nil
		}
	}
	return SimulationResult{}, false, nil
}

func (s *SimulationStore) Stats(workspace string) (int, time.Time, error) {
	items, err := s.load()
	if err != nil {
		return 0, time.Time{}, err
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	count := 0
	var last time.Time
	for _, item := range items {
		if workspace != "" && workspaceAffinityScore(workspace, item.Workspace) == 0 {
			continue
		}
		count++
		if item.CreatedAt.After(last) {
			last = item.CreatedAt
		}
	}
	return count, last, nil
}

func (s *SimulationStore) Dashboard(workspace string, limit int) (SimulationDashboardSummary, error) {
	summary := SimulationDashboardSummary{
		Scope: "current workspace",
	}
	items, err := s.load()
	if err != nil {
		return summary, err
	}
	if limit <= 0 {
		limit = 8
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	byProfile := map[string]int{}
	byCategory := map[string]int{}
	bySeverity := map[string]int{}
	bySignalClass := map[string]int{}
	topSubjects := map[string]int{}
	topActions := map[string]int{}
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if workspace != "" && workspaceAffinityScore(workspace, item.Workspace) == 0 {
			continue
		}
		summary.TotalRuns++
		if strings.TrimSpace(item.Profile) != "" {
			byProfile[item.Profile]++
		}
		for _, finding := range item.Findings {
			if strings.TrimSpace(finding.Category) != "" {
				byCategory[finding.Category]++
			}
			if strings.TrimSpace(finding.Severity) != "" {
				bySeverity[finding.Severity]++
			}
			if strings.TrimSpace(finding.SignalClass) != "" {
				bySignalClass[finding.SignalClass]++
			}
			if strings.TrimSpace(finding.Subject) != "" {
				topSubjects[finding.Subject]++
			}
			for _, action := range finding.RecommendedActions {
				if strings.TrimSpace(action) != "" {
					topActions[action]++
				}
			}
			if finding.RiskScore > summary.MaxRisk {
				summary.MaxRisk = finding.RiskScore
			}
		}
		if item.CreatedAt.After(summary.LastUpdated) {
			summary.LastUpdated = item.CreatedAt
		}
		if len(summary.Recent) < limit {
			summary.Recent = append(summary.Recent, item)
		}
	}
	summary.ByProfile = sortNamedCounts(byProfile)
	summary.ByCategory = sortNamedCounts(byCategory)
	summary.BySeverity = sortNamedCounts(bySeverity)
	summary.BySignalClass = sortNamedCounts(bySignalClass)
	summary.TopSubjects = sortNamedCounts(topSubjects)
	summary.TopActions = sortNamedCounts(topActions)
	return summary, nil
}

func normalizeSimulationResult(result SimulationResult) SimulationResult {
	if result.CreatedAt.IsZero() {
		result.CreatedAt = time.Now()
	}
	if strings.TrimSpace(result.ID) == "" {
		result.ID = fmt.Sprintf("sim-%s-%03d", result.CreatedAt.Format("20060102-150405"), result.CreatedAt.Nanosecond()/1_000_000)
	}
	result.Workspace = normalizePersistentMemoryWorkspace(result.Workspace)
	result.Profile = strings.TrimSpace(result.Profile)
	result.Target = strings.TrimSpace(result.Target)
	result.SourceEvidenceIDs = uniqueStrings(result.SourceEvidenceIDs)
	result.SourceInvestigationIDs = uniqueStrings(result.SourceInvestigationIDs)
	result.Summary = compactPersistentMemoryText(result.Summary, 260)
	result.Tags = uniqueStrings(result.Tags)
	for i := range result.Findings {
		result.Findings[i] = normalizeSimulationFinding(result.Findings[i])
	}
	result.Findings = sortSimulationFindings(result.Findings)
	return result
}

func normalizeSimulationFinding(finding SimulationFinding) SimulationFinding {
	finding.Kind = strings.TrimSpace(finding.Kind)
	finding.Category = strings.TrimSpace(finding.Category)
	finding.Subject = strings.TrimSpace(finding.Subject)
	finding.Severity = strings.ToLower(strings.TrimSpace(finding.Severity))
	finding.SignalClass = strings.ToLower(strings.TrimSpace(finding.SignalClass))
	finding.Message = compactPersistentMemoryText(finding.Message, 220)
	finding.RecommendedActions = uniqueStrings(finding.RecommendedActions)
	if finding.RiskScore < 0 {
		finding.RiskScore = 0
	}
	if finding.RiskScore > 100 {
		finding.RiskScore = 100
	}
	if len(finding.Attributes) > 0 {
		copied := make(map[string]string, len(finding.Attributes))
		for k, v := range finding.Attributes {
			key := strings.TrimSpace(k)
			val := strings.TrimSpace(v)
			if key == "" || val == "" {
				continue
			}
			copied[key] = val
		}
		finding.Attributes = copied
	}
	return finding
}

func sortSimulationFindings(findings []SimulationFinding) []SimulationFinding {
	out := append([]SimulationFinding(nil), findings...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RiskScore != out[j].RiskScore {
			return out[i].RiskScore > out[j].RiskScore
		}
		return out[i].Subject < out[j].Subject
	})
	return out
}

func renderSimulationResult(result SimulationResult) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("id: %s", result.ID))
	lines = append(lines, fmt.Sprintf("profile: %s", result.Profile))
	if strings.TrimSpace(result.Target) != "" {
		lines = append(lines, fmt.Sprintf("target: %s", result.Target))
	}
	lines = append(lines, fmt.Sprintf("created_at: %s", result.CreatedAt.Format(time.RFC3339)))
	if strings.TrimSpace(result.Summary) != "" {
		lines = append(lines, fmt.Sprintf("summary: %s", result.Summary))
	}
	if len(result.Findings) > 0 {
		lines = append(lines, "findings:")
		for _, finding := range result.Findings {
			line := fmt.Sprintf("- %s", finding.Subject)
			if finding.Severity != "" {
				line += "  severity=" + finding.Severity
			}
			if finding.SignalClass != "" {
				line += "  signal=" + finding.SignalClass
			}
			if finding.RiskScore > 0 {
				line += fmt.Sprintf("  risk=%d", finding.RiskScore)
			}
			lines = append(lines, line)
			if strings.TrimSpace(finding.Message) != "" {
				lines = append(lines, "  "+finding.Message)
			}
			for _, action := range finding.RecommendedActions {
				lines = append(lines, "  action: "+action)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func renderSimulationDashboard(summary SimulationDashboardSummary) string {
	if summary.TotalRuns == 0 {
		return "No simulation results found."
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Scope: %s", summary.Scope))
	lines = append(lines, fmt.Sprintf("Runs: total=%d max_risk=%d", summary.TotalRuns, summary.MaxRisk))
	if !summary.LastUpdated.IsZero() {
		lines = append(lines, "Last updated: "+summary.LastUpdated.Format(time.RFC3339))
	}
	if len(summary.ByProfile) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Profiles:")
		for _, item := range summary.ByProfile {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.ByCategory) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Finding categories:")
		for _, item := range summary.ByCategory {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.BySeverity) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Finding severities:")
		for _, item := range summary.BySeverity {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.BySignalClass) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Signal classes:")
		for _, item := range summary.BySignalClass {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.TopSubjects) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Top findings:")
		for _, item := range summary.TopSubjects {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.TopActions) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Recommended actions:")
		for _, item := range summary.TopActions {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.Recent) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Recent simulations:")
		for _, item := range summary.Recent {
			line := fmt.Sprintf("- %s  profile=%s  findings=%d", item.ID, item.Profile, len(item.Findings))
			if strings.TrimSpace(item.Target) != "" {
				line += "  target=" + item.Target
			}
			if strings.TrimSpace(item.Summary) != "" {
				line += "  |  " + compactPersistentMemoryText(item.Summary, 120)
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func renderSimulationDashboardHTML(summary SimulationDashboardSummary) string {
	profileBlocks := renderDashboardBars(summary.ByProfile)
	categoryBlocks := renderDashboardBars(summary.ByCategory)
	severityBlocks := renderDashboardBars(summary.BySeverity)
	signalBlocks := renderDashboardBars(summary.BySignalClass)
	subjectBlocks := renderDashboardBars(summary.TopSubjects)
	actionBlocks := renderDashboardBars(summary.TopActions)
	var recent []string
	for _, item := range summary.Recent {
		recent = append(recent, fmt.Sprintf(
			`<details class="report-detail"><summary><span>%s</span><span>%s / findings=%d</span></summary><div class="report-body"><div class="subtle">%s</div><pre>%s</pre></div></details>`,
			htmlEscape(item.ID),
			htmlEscape(item.Profile),
			len(item.Findings),
			htmlEscape(valueOrDefault(item.Target, "No target")),
			htmlEscape(valueOrDefault(item.Summary, "No summary")),
		))
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Simulation Dashboard</title>
  <link href="https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&family=IBM+Plex+Mono:wght@400;500&display=swap" rel="stylesheet">
  <style>
    :root { --bg: #07131f; --surface: rgba(14, 24, 39, 0.88); --surface-2: rgba(15, 23, 42, 0.72); --border: rgba(148, 163, 184, 0.16); --text: #e7eef8; --text-dim: #9db0ca; --accent: #fb7185; --accent-2: #60a5fa; }
    * { box-sizing: border-box; }
    body { margin: 0; font-family: "Space Grotesk", system-ui, sans-serif; color: var(--text); background: linear-gradient(180deg, #06101a, #07131f 55%%, #0b1320); min-height: 100vh; }
    .shell { max-width: 1120px; margin: 0 auto; padding: 40px 24px 56px; }
    .hero { display: grid; gap: 16px; margin-bottom: 28px; }
    .eyebrow { font: 500 12px/1 "IBM Plex Mono", monospace; letter-spacing: 0.14em; text-transform: uppercase; color: var(--accent); }
    h1 { margin: 0; font-size: clamp(34px, 5vw, 56px); line-height: 0.95; }
    .subtitle { max-width: 820px; color: var(--text-dim); font-size: 15px; line-height: 1.7; }
    .grid { display: grid; grid-template-columns: repeat(12, 1fr); gap: 18px; }
    .card { grid-column: span 12; background: var(--surface); border: 1px solid var(--border); border-radius: 22px; padding: 22px; backdrop-filter: blur(14px); }
    .kpi { grid-column: span 4; }
    .label { font: 500 12px/1 "IBM Plex Mono", monospace; text-transform: uppercase; color: var(--text-dim); letter-spacing: 0.08em; }
    .value { margin-top: 14px; font-size: 40px; font-weight: 700; }
    .section-title { margin: 0 0 14px; font-size: 18px; font-weight: 700; }
    .subtle { color: var(--text-dim); font: 400 13px/1.6 "IBM Plex Mono", monospace; }
    ul.metric-list { list-style: none; margin: 0; padding: 0; display: grid; gap: 10px; }
    .metric-list li { display: flex; justify-content: space-between; gap: 12px; background: var(--surface-2); border: 1px solid rgba(148,163,184,0.10); border-radius: 14px; padding: 12px 14px; }
    .metric-list span { color: var(--text-dim); font: 400 13px/1.5 "IBM Plex Mono", monospace; overflow-wrap: anywhere; }
    .metric-list strong { color: var(--text); font: 600 14px/1 "IBM Plex Mono", monospace; }
    .bar-wrap { flex: 1; min-width: 120px; align-self: center; height: 8px; border-radius: 999px; background: rgba(148,163,184,0.12); overflow: hidden; }
    .bar { height: 100%%; border-radius: 999px; background: linear-gradient(90deg, var(--accent), var(--accent-2)); }
    details.report-detail { border: 1px solid rgba(148,163,184,0.10); border-radius: 14px; background: var(--surface-2); overflow: hidden; }
    details.report-detail summary { cursor: pointer; list-style: none; display: flex; justify-content: space-between; gap: 12px; padding: 14px 16px; font: 500 13px/1.5 "IBM Plex Mono", monospace; }
    details.report-detail summary::-webkit-details-marker { display: none; }
    .report-body { padding: 0 16px 16px; }
    .report-body pre { margin: 0; padding: 12px; border-radius: 12px; background: rgba(2, 6, 23, 0.6); border: 1px solid rgba(148,163,184,0.10); overflow: auto; white-space: pre-wrap; font: 400 12px/1.6 "IBM Plex Mono", monospace; color: var(--text); }
  </style>
</head>
<body><div class="shell"><section class="hero"><div class="eyebrow">Kernforge Adversarial Simulation</div><h1>Attacker lenses, gaps, and recommended actions</h1><div class="subtitle">This dashboard summarizes simulation runs, attack-surface findings, severity and signal breakdowns, and the actions suggested by recent adversarial analysis.</div></section><section class="grid"><article class="card kpi"><div class="label">Scope</div><div class="value">%s</div></article><article class="card kpi"><div class="label">Runs</div><div class="value">%d</div></article><article class="card kpi"><div class="label">Max Risk</div><div class="value">%d</div></article><article class="card" style="grid-column: span 6;"><h2 class="section-title">Profiles</h2><ul class="metric-list">%s</ul></article><article class="card" style="grid-column: span 6;"><h2 class="section-title">Categories</h2><ul class="metric-list">%s</ul></article><article class="card" style="grid-column: span 4;"><h2 class="section-title">Severities</h2><ul class="metric-list">%s</ul></article><article class="card" style="grid-column: span 4;"><h2 class="section-title">Signal classes</h2><ul class="metric-list">%s</ul></article><article class="card" style="grid-column: span 4;"><h2 class="section-title">Top findings</h2><ul class="metric-list">%s</ul></article><article class="card" style="grid-column: span 12;"><h2 class="section-title">Recommended actions</h2><ul class="metric-list">%s</ul></article><article class="card" style="grid-column: span 12;"><h2 class="section-title">Recent simulations</h2><div class="recent-list">%s</div></article></section><div class="footer">Last updated: %s</div></div></body>
</html>`,
		htmlEscape(summary.Scope),
		summary.TotalRuns,
		summary.MaxRisk,
		valueOrDefault(profileBlocks, "<li><span>No profiles</span><strong>0</strong></li>"),
		valueOrDefault(categoryBlocks, "<li><span>No categories</span><strong>0</strong></li>"),
		valueOrDefault(severityBlocks, "<li><span>No severities</span><strong>0</strong></li>"),
		valueOrDefault(signalBlocks, "<li><span>No signals</span><strong>0</strong></li>"),
		valueOrDefault(subjectBlocks, "<li><span>No findings</span><strong>0</strong></li>"),
		valueOrDefault(actionBlocks, "<li><span>No actions</span><strong>0</strong></li>"),
		joinOrFallback(recent, `<article class="report-card"><div class="report-summary">No simulation results found.</div></article>`),
		htmlEscape(summary.LastUpdated.Format(time.RFC3339)),
	)
}

func createSimulationDashboardHTML(summary SimulationDashboardSummary) (string, error) {
	reportsDir := filepath.Join(userConfigDir(), "reports")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		return "", err
	}
	name := "simulation-dashboard-" + time.Now().Format("20060102-150405") + ".html"
	path := filepath.Join(reportsDir, name)
	if err := os.WriteFile(path, []byte(renderSimulationDashboardHTML(summary)), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (s *SimulationStore) load() ([]SimulationResult, error) {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var items []SimulationResult
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	for i := range items {
		items[i] = normalizeSimulationResult(items[i])
	}
	return items, nil
}

func (s *SimulationStore) save(items []SimulationResult) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(s.Path, data, 0o644)
}
