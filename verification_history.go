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

const defaultVerificationHistoryMaxEntries = 240

type VerificationHistoryEntry struct {
	SessionID  string             `json:"session_id"`
	Workspace  string             `json:"workspace"`
	RecordedAt time.Time          `json:"recorded_at"`
	Report     VerificationReport `json:"report"`
}

type VerificationHistoryStore struct {
	Path       string
	MaxEntries int
}

type VerificationDashboardSummary struct {
	Scope         string
	FilterText    string
	TotalReports  int
	PassedReports int
	FailedReports int
	FailureKinds  []NamedCount
	FailingChecks []NamedCount
	Recent        []VerificationHistoryEntry
	LastUpdated   time.Time
}

type NamedCount struct {
	Name  string
	Count int
}

type VerificationTuning struct {
	FailureCounts map[string]int
	RunCounts     map[string]int
}

func NewVerificationHistoryStore() *VerificationHistoryStore {
	return &VerificationHistoryStore{
		Path:       filepath.Join(userConfigDir(), "verification-history.json"),
		MaxEntries: defaultVerificationHistoryMaxEntries,
	}
}

func (s *VerificationHistoryStore) Append(sessionID, workspace string, report VerificationReport) error {
	if s == nil {
		return nil
	}
	items, err := s.load()
	if err != nil {
		return err
	}
	entry := VerificationHistoryEntry{
		SessionID:  strings.TrimSpace(sessionID),
		Workspace:  normalizePersistentMemoryWorkspace(workspace),
		RecordedAt: time.Now(),
		Report:     compactVerificationReport(report),
	}
	items = append(items, entry)
	maxEntries := s.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultVerificationHistoryMaxEntries
	}
	if len(items) > maxEntries {
		items = append([]VerificationHistoryEntry(nil), items[len(items)-maxEntries:]...)
	}
	return s.save(items)
}

func compactVerificationReport(report VerificationReport) VerificationReport {
	out := report
	for i := range out.Steps {
		out.Steps[i].Output = compactPersistentMemoryText(out.Steps[i].Output, 1600)
		out.Steps[i].Hint = compactPersistentMemoryText(out.Steps[i].Hint, 240)
	}
	return out
}

func (s *VerificationHistoryStore) Dashboard(workspace string, all bool, tags []string, limit int) (VerificationDashboardSummary, error) {
	items, err := s.load()
	if err != nil {
		return VerificationDashboardSummary{}, err
	}
	if limit <= 0 {
		limit = 10
	}
	summary := VerificationDashboardSummary{
		Scope: "current workspace",
	}
	if all {
		summary.Scope = "all workspaces"
	}
	if len(tags) > 0 {
		summary.FilterText = "tags=" + strings.Join(tags, ", ")
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	failureKinds := map[string]int{}
	failingChecks := map[string]int{}
	for i := len(items) - 1; i >= 0; i-- {
		entry := items[i]
		if !all && workspaceAffinityScore(workspace, entry.Workspace) == 0 {
			continue
		}
		if !verificationEntryMatchesTags(entry, tags) {
			continue
		}
		summary.TotalReports++
		if entry.Report.HasFailures() {
			summary.FailedReports++
		} else {
			summary.PassedReports++
		}
		for _, step := range entry.Report.Steps {
			if step.Status != VerificationFailed {
				continue
			}
			if strings.TrimSpace(step.FailureKind) != "" {
				failureKinds[step.FailureKind]++
			}
			if strings.TrimSpace(step.Label) != "" {
				failingChecks[step.Label]++
			}
		}
		if entry.RecordedAt.After(summary.LastUpdated) {
			summary.LastUpdated = entry.RecordedAt
		}
		if len(summary.Recent) < limit {
			summary.Recent = append(summary.Recent, entry)
		}
	}
	summary.FailureKinds = sortNamedCounts(failureKinds)
	summary.FailingChecks = sortNamedCounts(failingChecks)
	return summary, nil
}

func sortNamedCounts(items map[string]int) []NamedCount {
	var out []NamedCount
	for name, count := range items {
		out = append(out, NamedCount{Name: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func renderVerificationDashboard(summary VerificationDashboardSummary) string {
	if summary.TotalReports == 0 {
		return "No verification history found."
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Scope: %s", summary.Scope))
	if strings.TrimSpace(summary.FilterText) != "" {
		lines = append(lines, "Filters: "+summary.FilterText)
	}
	lines = append(lines, fmt.Sprintf("Reports: total=%d passed=%d failed=%d", summary.TotalReports, summary.PassedReports, summary.FailedReports))
	if !summary.LastUpdated.IsZero() {
		lines = append(lines, "Last updated: "+summary.LastUpdated.Format(time.RFC3339))
	}
	if len(summary.FailureKinds) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Top failure kinds:")
		for _, item := range summary.FailureKinds {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.FailingChecks) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Most frequently failing checks:")
		for _, item := range summary.FailingChecks {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.Recent) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Recent reports:")
		for _, entry := range summary.Recent {
			line := fmt.Sprintf("- [%s] %s", entry.RecordedAt.Format("2006-01-02 15:04"), entry.Report.SummaryLine())
			if strings.TrimSpace(entry.Report.Decision) != "" {
				line += " | " + compactPersistentMemoryText(entry.Report.Decision, 120)
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func renderVerificationDashboardHTML(summary VerificationDashboardSummary) string {
	failureKinds := renderDashboardBars(summary.FailureKinds)
	failingChecks := renderDashboardBars(summary.FailingChecks)
	var recent []string
	var drilldowns []string
	for _, entry := range summary.Recent {
		recent = append(recent, fmt.Sprintf(
			"<article class=\"report-card\"><div class=\"report-time\">%s</div><div class=\"report-summary\">%s</div><div class=\"report-decision\">%s</div></article>",
			htmlEscape(entry.RecordedAt.Format("2006-01-02 15:04")),
			htmlEscape(entry.Report.SummaryLine()),
			htmlEscape(compactPersistentMemoryText(entry.Report.Decision, 180)),
		))
		drilldowns = append(drilldowns, renderVerificationReportDrilldown(entry))
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Verification Dashboard</title>
  <link href="https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&family=IBM+Plex+Mono:wght@400;500&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg: #0b1220;
      --surface: rgba(17, 24, 39, 0.88);
      --surface-2: rgba(15, 23, 42, 0.72);
      --border: rgba(148, 163, 184, 0.18);
      --text: #e5eefc;
      --text-dim: #9fb2cf;
      --accent: #7dd3fc;
      --accent-2: #f59e0b;
      --success: #34d399;
      --danger: #fb7185;
      --grid: rgba(125, 211, 252, 0.08);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: "Space Grotesk", system-ui, sans-serif;
      color: var(--text);
      background:
        radial-gradient(circle at top right, rgba(125,211,252,0.12), transparent 28%%),
        radial-gradient(circle at left center, rgba(245,158,11,0.10), transparent 22%%),
        linear-gradient(180deg, #07101c, #0b1220 55%%, #09111d);
      min-height: 100vh;
    }
    .shell {
      max-width: 1100px;
      margin: 0 auto;
      padding: 40px 24px 56px;
    }
    .hero {
      display: grid;
      gap: 18px;
      margin-bottom: 28px;
    }
    .eyebrow {
      font: 500 12px/1 "IBM Plex Mono", monospace;
      letter-spacing: 0.14em;
      text-transform: uppercase;
      color: var(--accent);
    }
    h1 {
      margin: 0;
      font-size: clamp(34px, 5vw, 56px);
      line-height: 0.95;
    }
    .subtitle {
      max-width: 780px;
      color: var(--text-dim);
      font-size: 15px;
      line-height: 1.7;
    }
    .grid {
      display: grid;
      grid-template-columns: repeat(12, 1fr);
      gap: 18px;
    }
    .card {
      grid-column: span 12;
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: 22px;
      padding: 22px;
      backdrop-filter: blur(14px);
      box-shadow: 0 20px 50px rgba(0,0,0,0.25);
    }
    .kpi { grid-column: span 3; }
    .kpi .label {
      font: 500 12px/1 "IBM Plex Mono", monospace;
      text-transform: uppercase;
      color: var(--text-dim);
      letter-spacing: 0.08em;
    }
    .kpi .value {
      margin-top: 14px;
      font-size: 40px;
      font-weight: 700;
    }
    .section-title {
      margin: 0 0 14px;
      font-size: 18px;
      font-weight: 700;
    }
    .subtle {
      color: var(--text-dim);
      font: 400 13px/1.6 "IBM Plex Mono", monospace;
    }
    ul.metric-list {
      list-style: none;
      margin: 0;
      padding: 0;
      display: grid;
      gap: 10px;
    }
    .metric-list li {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      background: var(--surface-2);
      border: 1px solid rgba(148,163,184,0.10);
      border-radius: 14px;
      padding: 12px 14px;
    }
    .metric-list span {
      color: var(--text-dim);
      font: 400 13px/1.5 "IBM Plex Mono", monospace;
      overflow-wrap: anywhere;
    }
    .bar-wrap {
      flex: 1;
      min-width: 120px;
      align-self: center;
      height: 8px;
      border-radius: 999px;
      background: rgba(148,163,184,0.12);
      overflow: hidden;
    }
    .bar {
      height: 100%%;
      border-radius: 999px;
      background: linear-gradient(90deg, var(--accent), var(--accent-2));
    }
    .metric-list strong {
      color: var(--text);
      font: 600 14px/1 "IBM Plex Mono", monospace;
    }
    .recent-list {
      display: grid;
      gap: 12px;
    }
    .report-card {
      padding: 14px 16px;
      border-radius: 16px;
      border: 1px solid rgba(148,163,184,0.10);
      background: linear-gradient(180deg, rgba(15,23,42,0.74), rgba(15,23,42,0.58));
    }
    .report-time {
      font: 500 12px/1 "IBM Plex Mono", monospace;
      color: var(--accent);
      margin-bottom: 10px;
    }
    .report-summary {
      font-weight: 600;
      margin-bottom: 8px;
    }
    .report-decision {
      color: var(--text-dim);
      font-size: 14px;
      line-height: 1.6;
    }
    details.report-detail, details.step-detail {
      border: 1px solid rgba(148,163,184,0.10);
      border-radius: 14px;
      background: var(--surface-2);
      overflow: hidden;
    }
    details.report-detail summary, details.step-detail summary {
      cursor: pointer;
      list-style: none;
      display: flex;
      justify-content: space-between;
      gap: 12px;
      padding: 14px 16px;
      font: 500 13px/1.5 "IBM Plex Mono", monospace;
    }
    details.report-detail summary::-webkit-details-marker,
    details.step-detail summary::-webkit-details-marker { display: none; }
    .report-body, .step-body {
      padding: 0 16px 16px;
    }
    .step-meta {
      color: var(--text-dim);
      font-size: 13px;
      line-height: 1.6;
      margin-bottom: 8px;
    }
    .step-body pre {
      margin: 0;
      padding: 12px;
      border-radius: 12px;
      background: rgba(2, 6, 23, 0.6);
      border: 1px solid rgba(148,163,184,0.10);
      overflow: auto;
      white-space: pre-wrap;
      font: 400 12px/1.6 "IBM Plex Mono", monospace;
      color: var(--text);
    }
    .tag {
      display: inline-flex;
      align-items: center;
      padding: 2px 8px;
      border-radius: 999px;
      background: rgba(125,211,252,0.10);
      border: 1px solid rgba(125,211,252,0.22);
      color: var(--accent);
      font: 500 11px/1 "IBM Plex Mono", monospace;
      margin-right: 6px;
    }
    .footer {
      margin-top: 18px;
      color: var(--text-dim);
      font: 400 12px/1.6 "IBM Plex Mono", monospace;
    }
    @media (max-width: 900px) {
      .kpi { grid-column: span 6; }
    }
    @media (max-width: 640px) {
      .shell { padding: 24px 16px 36px; }
      .kpi { grid-column: span 12; }
    }
  </style>
</head>
<body>
  <div class="shell">
    <section class="hero">
      <div class="eyebrow">Kernforge Verification Dashboard</div>
      <h1>Verification signal, failure trends, and recent history</h1>
      <div class="subtitle">This dashboard summarizes recent verification runs, highlights the checks that fail most often, and gives a quick view of how the adaptive planner has been behaving in this scope.</div>
    </section>

    <section class="grid">
      <article class="card kpi">
        <div class="label">Scope</div>
        <div class="value">%s</div>
      </article>
      <article class="card kpi">
        <div class="label">Filter</div>
        <div class="value">%s</div>
      </article>
      <article class="card kpi">
        <div class="label">Reports</div>
        <div class="value">%d</div>
      </article>
      <article class="card kpi">
        <div class="label">Passed</div>
        <div class="value" style="color: var(--success)">%d</div>
      </article>
      <article class="card kpi">
        <div class="label">Failed</div>
        <div class="value" style="color: var(--danger)">%d</div>
      </article>

      <article class="card" style="grid-column: span 6;">
        <h2 class="section-title">Top failure kinds</h2>
        <div class="subtle">Most common failure categories observed in this scope.</div>
        <ul class="metric-list chart-list">%s</ul>
      </article>

      <article class="card" style="grid-column: span 6;">
        <h2 class="section-title">Most frequently failing checks</h2>
        <div class="subtle">Checks that are failing most often and may deserve earlier attention.</div>
        <ul class="metric-list chart-list">%s</ul>
      </article>

      <article class="card" style="grid-column: span 12;">
        <h2 class="section-title">Recent reports</h2>
        <div class="subtle">Latest verification summaries and planner decisions.</div>
        <div class="recent-list">%s</div>
      </article>

      <article class="card" style="grid-column: span 12;">
        <h2 class="section-title">Report drill-down</h2>
        <div class="subtle">Expand any report to inspect its steps, failure kind, hint, output, and tags.</div>
        <div class="recent-list">%s</div>
      </article>
    </section>

    <div class="footer">Last updated: %s</div>
  </div>
</body>
</html>`,
		htmlEscape(summary.Scope),
		htmlEscape(valueOrDefault(summary.FilterText, "none")),
		summary.TotalReports,
		summary.PassedReports,
		summary.FailedReports,
		valueOrDefault(failureKinds, "<li><span>No recorded failures</span><strong>0</strong></li>"),
		valueOrDefault(failingChecks, "<li><span>No failing checks recorded</span><strong>0</strong></li>"),
		joinOrFallback(recent, `<article class="report-card"><div class="report-summary">No recent verification reports found.</div></article>`),
		joinOrFallback(drilldowns, `<article class="report-card"><div class="report-summary">No drill-down reports available.</div></article>`),
		htmlEscape(summary.LastUpdated.Format(time.RFC3339)),
	)
}

func joinOrFallback(items []string, fallback string) string {
	if len(items) == 0 {
		return fallback
	}
	return strings.Join(items, "")
}

func htmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(value)
}

func createVerificationDashboardHTML(summary VerificationDashboardSummary, all bool) (string, error) {
	reportsDir := filepath.Join(userConfigDir(), "reports")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		return "", err
	}
	name := "verification-dashboard"
	if all {
		name += "-all"
	}
	name += "-" + time.Now().Format("20060102-150405") + ".html"
	path := filepath.Join(reportsDir, name)
	if err := os.WriteFile(path, []byte(renderVerificationDashboardHTML(summary)), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func verificationEntryMatchesTags(entry VerificationHistoryEntry, tags []string) bool {
	if len(tags) == 0 {
		return true
	}
	for _, step := range entry.Report.Steps {
		for _, tag := range step.Tags {
			for _, filter := range tags {
				if strings.EqualFold(strings.TrimSpace(tag), strings.TrimSpace(filter)) {
					return true
				}
			}
		}
	}
	return false
}

func renderDashboardBars(items []NamedCount) string {
	if len(items) == 0 {
		return ""
	}
	maxCount := 1
	for _, item := range items {
		if item.Count > maxCount {
			maxCount = item.Count
		}
	}
	var blocks []string
	for _, item := range items {
		width := item.Count * 100 / maxCount
		blocks = append(blocks, fmt.Sprintf(
			`<li><span>%s</span><div class="bar-wrap"><div class="bar" style="width:%d%%"></div></div><strong>%d</strong></li>`,
			htmlEscape(item.Name),
			width,
			item.Count,
		))
	}
	return strings.Join(blocks, "")
}

func renderVerificationReportDrilldown(entry VerificationHistoryEntry) string {
	var stepRows []string
	for _, step := range entry.Report.Steps {
		var tags []string
		for _, tag := range step.Tags {
			tags = append(tags, `<span class="tag">`+htmlEscape(tag)+`</span>`)
		}
		stepRows = append(stepRows, fmt.Sprintf(
			`<details class="step-detail"><summary><span>[%s] %s</span><span>%s</span></summary><div class="step-body"><div class="step-meta">%s %s</div><pre>%s</pre></div></details>`,
			htmlEscape(string(step.Status)),
			htmlEscape(step.Label),
			htmlEscape(valueOrDefault(step.FailureKind, "ok")),
			htmlEscape(valueOrDefault(step.Hint, "")),
			strings.Join(tags, " "),
			htmlEscape(step.Output),
		))
	}
	return fmt.Sprintf(
		`<details class="report-detail"><summary><span>%s</span><span>%s</span></summary><div class="report-body"><div class="subtle">%s</div><div class="subtle">%s</div>%s</div></details>`,
		htmlEscape(entry.RecordedAt.Format("2006-01-02 15:04")),
		htmlEscape(entry.Report.SummaryLine()),
		htmlEscape(valueOrDefault(entry.Report.Decision, "")),
		htmlEscape("Changed: "+strings.Join(entry.Report.ChangedPaths, ", ")),
		strings.Join(stepRows, ""),
	)
}

func (s *VerificationHistoryStore) PlannerTuning(workspace string) (VerificationTuning, error) {
	items, err := s.load()
	if err != nil {
		return VerificationTuning{}, err
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	tuning := VerificationTuning{
		FailureCounts: map[string]int{},
		RunCounts:     map[string]int{},
	}
	for _, entry := range items {
		if workspaceAffinityScore(workspace, entry.Workspace) == 0 {
			continue
		}
		for _, step := range entry.Report.Steps {
			key := verificationHistoryKey(step)
			if key == "" {
				continue
			}
			tuning.RunCounts[key]++
			if step.Status == VerificationFailed {
				tuning.FailureCounts[key]++
			}
		}
	}
	return tuning, nil
}

func (s *VerificationHistoryStore) load() ([]VerificationHistoryEntry, error) {
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
	var items []VerificationHistoryEntry
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *VerificationHistoryStore) save(items []VerificationHistoryEntry) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, data, 0o644)
}
