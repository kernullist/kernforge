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

const defaultInvestigationMaxEntries = 300

type InvestigationStatus string

const (
	InvestigationActive    InvestigationStatus = "active"
	InvestigationCompleted InvestigationStatus = "completed"
)

type InvestigationFinding struct {
	Kind        string            `json:"kind"`
	Category    string            `json:"category,omitempty"`
	Subject     string            `json:"subject"`
	Outcome     string            `json:"outcome,omitempty"`
	Severity    string            `json:"severity,omitempty"`
	SignalClass string            `json:"signal_class,omitempty"`
	RiskScore   int               `json:"risk_score,omitempty"`
	Message     string            `json:"message,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

type InvestigationCommandResult struct {
	Label      string    `json:"label"`
	Command    string    `json:"command"`
	Success    bool      `json:"success"`
	StartedAt  time.Time `json:"started_at"`
	DurationMs int64     `json:"duration_ms,omitempty"`
	Output     string    `json:"output,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type InvestigationSnapshot struct {
	ID         string                       `json:"id"`
	Kind       string                       `json:"kind"`
	CreatedAt  time.Time                    `json:"created_at"`
	Target     string                       `json:"target,omitempty"`
	Commands   []InvestigationCommandResult `json:"commands,omitempty"`
	Artifacts  []string                     `json:"artifacts,omitempty"`
	Findings   []InvestigationFinding       `json:"findings,omitempty"`
	RawSummary string                       `json:"raw_summary,omitempty"`
}

type InvestigationRecord struct {
	ID                 string                  `json:"id"`
	Workspace          string                  `json:"workspace"`
	Preset             string                  `json:"preset"`
	Target             string                  `json:"target,omitempty"`
	Status             InvestigationStatus     `json:"status"`
	CreatedAt          time.Time               `json:"created_at"`
	UpdatedAt          time.Time               `json:"updated_at"`
	StartedBySessionID string                  `json:"started_by_session_id,omitempty"`
	Notes              []string                `json:"notes,omitempty"`
	Snapshots          []InvestigationSnapshot `json:"snapshots,omitempty"`
	Summary            string                  `json:"summary,omitempty"`
	Tags               []string                `json:"tags,omitempty"`
}

type InvestigationStore struct {
	Path       string
	MaxEntries int
}

type InvestigationDashboardSummary struct {
	Scope         string
	TotalSessions int
	ActiveCount   int
	ByPreset      []NamedCount
	ByStatus      []NamedCount
	ByCategory    []NamedCount
	BySeverity    []NamedCount
	TopSubjects   []NamedCount
	Recent        []InvestigationRecord
	LastUpdated   time.Time
}

func NewInvestigationStore() *InvestigationStore {
	return &InvestigationStore{
		Path:       filepath.Join(userConfigDir(), "investigations.json"),
		MaxEntries: defaultInvestigationMaxEntries,
	}
}

func (s *InvestigationStore) Append(record InvestigationRecord) (InvestigationRecord, error) {
	if s == nil {
		return InvestigationRecord{}, nil
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	record = normalizeInvestigationRecord(record)
	records, err := s.load()
	if err != nil {
		return InvestigationRecord{}, err
	}
	records = append(records, record)
	maxEntries := s.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultInvestigationMaxEntries
	}
	if len(records) > maxEntries {
		records = append([]InvestigationRecord(nil), records[len(records)-maxEntries:]...)
	}
	if err := s.save(records); err != nil {
		return InvestigationRecord{}, err
	}
	return record, nil
}

func (s *InvestigationStore) Update(id string, mutate func(*InvestigationRecord) error) (InvestigationRecord, bool, error) {
	if s == nil {
		return InvestigationRecord{}, false, nil
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	records, err := s.load()
	if err != nil {
		return InvestigationRecord{}, false, err
	}
	query := strings.TrimSpace(id)
	for i := range records {
		if !strings.EqualFold(records[i].ID, query) {
			continue
		}
		if err := mutate(&records[i]); err != nil {
			return InvestigationRecord{}, false, err
		}
		records[i] = normalizeInvestigationRecord(records[i])
		if err := s.save(records); err != nil {
			return InvestigationRecord{}, false, err
		}
		return records[i], true, nil
	}
	return InvestigationRecord{}, false, nil
}

func (s *InvestigationStore) ListRecent(workspace string, limit int) ([]InvestigationRecord, error) {
	records, err := s.load()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	var out []InvestigationRecord
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if workspace != "" && workspaceAffinityScore(workspace, record.Workspace) == 0 {
			continue
		}
		out = append(out, record)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *InvestigationStore) Get(id string) (InvestigationRecord, bool, error) {
	records, err := s.load()
	if err != nil {
		return InvestigationRecord{}, false, err
	}
	query := strings.TrimSpace(id)
	for _, record := range records {
		if strings.EqualFold(record.ID, query) {
			return record, true, nil
		}
	}
	return InvestigationRecord{}, false, nil
}

func (s *InvestigationStore) Active(workspace string) (InvestigationRecord, bool, error) {
	records, err := s.load()
	if err != nil {
		return InvestigationRecord{}, false, err
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if workspace != "" && workspaceAffinityScore(workspace, record.Workspace) == 0 {
			continue
		}
		if record.Status == InvestigationActive {
			return record, true, nil
		}
	}
	return InvestigationRecord{}, false, nil
}

func (s *InvestigationStore) Stats(workspace string) (int, bool, time.Time, error) {
	records, err := s.load()
	if err != nil {
		return 0, false, time.Time{}, err
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	count := 0
	active := false
	var last time.Time
	for _, record := range records {
		if workspace != "" && workspaceAffinityScore(workspace, record.Workspace) == 0 {
			continue
		}
		count++
		if record.Status == InvestigationActive {
			active = true
		}
		if record.UpdatedAt.After(last) {
			last = record.UpdatedAt
		}
	}
	return count, active, last, nil
}

func (s *InvestigationStore) Dashboard(workspace string, limit int) (InvestigationDashboardSummary, error) {
	summary := InvestigationDashboardSummary{
		Scope: "current workspace",
	}
	records, err := s.load()
	if err != nil {
		return summary, err
	}
	if limit <= 0 {
		limit = 8
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	byPreset := map[string]int{}
	byStatus := map[string]int{}
	byCategory := map[string]int{}
	bySeverity := map[string]int{}
	topSubjects := map[string]int{}
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if workspace != "" && workspaceAffinityScore(workspace, record.Workspace) == 0 {
			continue
		}
		summary.TotalSessions++
		if record.Status == InvestigationActive {
			summary.ActiveCount++
		}
		if strings.TrimSpace(record.Preset) != "" {
			byPreset[record.Preset]++
		}
		if strings.TrimSpace(string(record.Status)) != "" {
			byStatus[string(record.Status)]++
		}
		for _, snap := range record.Snapshots {
			for _, finding := range snap.Findings {
				if strings.TrimSpace(finding.Category) != "" {
					byCategory[finding.Category]++
				}
				if strings.TrimSpace(finding.Severity) != "" {
					bySeverity[finding.Severity]++
				}
				if strings.TrimSpace(finding.Subject) != "" {
					topSubjects[finding.Subject]++
				}
			}
		}
		if record.UpdatedAt.After(summary.LastUpdated) {
			summary.LastUpdated = record.UpdatedAt
		}
		if len(summary.Recent) < limit {
			summary.Recent = append(summary.Recent, record)
		}
	}
	summary.ByPreset = sortNamedCounts(byPreset)
	summary.ByStatus = sortNamedCounts(byStatus)
	summary.ByCategory = sortNamedCounts(byCategory)
	summary.BySeverity = sortNamedCounts(bySeverity)
	summary.TopSubjects = sortNamedCounts(topSubjects)
	return summary, nil
}

func normalizeInvestigationRecord(record InvestigationRecord) InvestigationRecord {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.CreatedAt
	}
	if strings.TrimSpace(record.ID) == "" {
		record.ID = fmt.Sprintf("inv-%s-%03d", record.CreatedAt.Format("20060102-150405"), record.CreatedAt.Nanosecond()/1_000_000)
	}
	record.Workspace = normalizePersistentMemoryWorkspace(record.Workspace)
	record.Preset = normalizeInvestigationPreset(record.Preset)
	record.Target = strings.TrimSpace(record.Target)
	record.Summary = compactPersistentMemoryText(record.Summary, 240)
	record.Tags = uniqueStrings(record.Tags)
	var notes []string
	for _, note := range record.Notes {
		if value := strings.TrimSpace(note); value != "" {
			notes = append(notes, compactPersistentMemoryText(value, 220))
		}
	}
	record.Notes = notes
	for i := range record.Snapshots {
		record.Snapshots[i] = normalizeInvestigationSnapshot(record.Snapshots[i])
	}
	if len(record.Snapshots) > 20 {
		record.Snapshots = append([]InvestigationSnapshot(nil), record.Snapshots[len(record.Snapshots)-20:]...)
	}
	if record.Status == "" {
		record.Status = InvestigationActive
	}
	return record
}

func normalizeInvestigationSnapshot(snapshot InvestigationSnapshot) InvestigationSnapshot {
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now()
	}
	if strings.TrimSpace(snapshot.ID) == "" {
		snapshot.ID = fmt.Sprintf("snap-%s-%03d", snapshot.CreatedAt.Format("20060102-150405"), snapshot.CreatedAt.Nanosecond()/1_000_000)
	}
	snapshot.Kind = strings.TrimSpace(snapshot.Kind)
	snapshot.Target = strings.TrimSpace(snapshot.Target)
	snapshot.RawSummary = compactPersistentMemoryText(snapshot.RawSummary, 400)
	snapshot.Artifacts = uniqueStrings(snapshot.Artifacts)
	for i := range snapshot.Findings {
		snapshot.Findings[i] = normalizeInvestigationFinding(snapshot.Findings[i])
	}
	return snapshot
}

func normalizeInvestigationFinding(finding InvestigationFinding) InvestigationFinding {
	finding.Kind = strings.TrimSpace(finding.Kind)
	finding.Category = strings.TrimSpace(finding.Category)
	finding.Subject = strings.TrimSpace(finding.Subject)
	finding.Outcome = strings.TrimSpace(finding.Outcome)
	finding.Severity = strings.ToLower(strings.TrimSpace(finding.Severity))
	finding.SignalClass = strings.ToLower(strings.TrimSpace(finding.SignalClass))
	finding.Message = compactPersistentMemoryText(finding.Message, 220)
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

func renderInvestigationRecord(root string, record InvestigationRecord) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("id: %s", record.ID))
	lines = append(lines, fmt.Sprintf("preset: %s", record.Preset))
	if strings.TrimSpace(record.Target) != "" {
		lines = append(lines, fmt.Sprintf("target: %s", record.Target))
	}
	lines = append(lines, fmt.Sprintf("status: %s", record.Status))
	lines = append(lines, fmt.Sprintf("updated_at: %s", record.UpdatedAt.Format(time.RFC3339)))
	if strings.TrimSpace(record.Summary) != "" {
		lines = append(lines, fmt.Sprintf("summary: %s", record.Summary))
	}
	if len(record.Notes) > 0 {
		lines = append(lines, "notes:")
		for _, note := range record.Notes {
			lines = append(lines, "- "+note)
		}
	}
	if len(record.Snapshots) > 0 {
		lines = append(lines, "snapshots:")
		for _, snap := range record.Snapshots {
			line := fmt.Sprintf("- %s  %s", snap.ID, snap.Kind)
			if strings.TrimSpace(snap.Target) != "" {
				line += "  target=" + snap.Target
			}
			if len(snap.Findings) > 0 {
				line += fmt.Sprintf("  findings=%d", len(snap.Findings))
			}
			lines = append(lines, line)
			for _, finding := range snap.Findings {
				fLine := "  * " + finding.Subject
				if finding.Severity != "" {
					fLine += "  severity=" + finding.Severity
				}
				if finding.SignalClass != "" {
					fLine += "  signal=" + finding.SignalClass
				}
				if finding.RiskScore > 0 {
					fLine += fmt.Sprintf("  risk=%d", finding.RiskScore)
				}
				lines = append(lines, fLine)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func renderInvestigationDashboard(summary InvestigationDashboardSummary) string {
	if summary.TotalSessions == 0 {
		return "No investigation sessions found."
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Scope: %s", summary.Scope))
	lines = append(lines, fmt.Sprintf("Sessions: total=%d active=%d", summary.TotalSessions, summary.ActiveCount))
	if !summary.LastUpdated.IsZero() {
		lines = append(lines, "Last updated: "+summary.LastUpdated.Format(time.RFC3339))
	}
	if len(summary.ByPreset) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Presets:")
		for _, item := range summary.ByPreset {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.ByStatus) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Statuses:")
		for _, item := range summary.ByStatus {
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
	if len(summary.TopSubjects) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Top finding subjects:")
		for _, item := range summary.TopSubjects {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.Recent) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Recent investigations:")
		for _, record := range summary.Recent {
			line := fmt.Sprintf("- %s  preset=%s  status=%s  snapshots=%d", record.ID, record.Preset, record.Status, len(record.Snapshots))
			if strings.TrimSpace(record.Target) != "" {
				line += "  target=" + record.Target
			}
			if strings.TrimSpace(record.Summary) != "" {
				line += "  |  " + compactPersistentMemoryText(record.Summary, 120)
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func renderInvestigationDashboardHTML(summary InvestigationDashboardSummary) string {
	presetBlocks := renderDashboardBars(summary.ByPreset)
	statusBlocks := renderDashboardBars(summary.ByStatus)
	categoryBlocks := renderDashboardBars(summary.ByCategory)
	severityBlocks := renderDashboardBars(summary.BySeverity)
	subjectBlocks := renderDashboardBars(summary.TopSubjects)
	var recent []string
	for _, record := range summary.Recent {
		recent = append(recent, fmt.Sprintf(
			`<details class="report-detail"><summary><span>%s</span><span>%s / %s</span></summary><div class="report-body"><div class="subtle">%s</div><div class="subtle">%s</div><pre>%s</pre></div></details>`,
			htmlEscape(record.ID),
			htmlEscape(record.Preset),
			htmlEscape(string(record.Status)),
			htmlEscape(valueOrDefault(record.Target, "No target")),
			htmlEscape(fmt.Sprintf("snapshots=%d", len(record.Snapshots))),
			htmlEscape(valueOrDefault(record.Summary, "No summary")),
		))
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Investigation Dashboard</title>
  <link href="https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&family=IBM+Plex+Mono:wght@400;500&display=swap" rel="stylesheet">
  <style>
    :root { --bg: #081017; --surface: rgba(13, 20, 33, 0.9); --surface-2: rgba(16, 24, 39, 0.78); --border: rgba(148, 163, 184, 0.16); --text: #e7eef8; --text-dim: #9db0ca; --accent: #f59e0b; --accent-2: #34d399; }
    * { box-sizing: border-box; }
    body { margin: 0; font-family: "Space Grotesk", system-ui, sans-serif; color: var(--text); background: linear-gradient(180deg, #050c13, #081017 55%%, #0b1220); min-height: 100vh; }
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
<body><div class="shell"><section class="hero"><div class="eyebrow">Kernforge Investigation</div><h1>Live snapshots, findings, and active sessions</h1><div class="subtitle">This dashboard summarizes recent investigation sessions, active work, and the finding distribution captured from live Windows state snapshots.</div></section><section class="grid"><article class="card kpi"><div class="label">Scope</div><div class="value">%s</div></article><article class="card kpi"><div class="label">Sessions</div><div class="value">%d</div></article><article class="card kpi"><div class="label">Active</div><div class="value">%d</div></article><article class="card" style="grid-column: span 6;"><h2 class="section-title">Presets</h2><ul class="metric-list">%s</ul></article><article class="card" style="grid-column: span 6;"><h2 class="section-title">Statuses</h2><ul class="metric-list">%s</ul></article><article class="card" style="grid-column: span 6;"><h2 class="section-title">Finding categories</h2><ul class="metric-list">%s</ul></article><article class="card" style="grid-column: span 6;"><h2 class="section-title">Finding severities</h2><ul class="metric-list">%s</ul></article><article class="card" style="grid-column: span 12;"><h2 class="section-title">Top finding subjects</h2><ul class="metric-list">%s</ul></article><article class="card" style="grid-column: span 12;"><h2 class="section-title">Recent investigations</h2><div class="recent-list">%s</div></article></section><div class="footer">Last updated: %s</div></div></body>
</html>`,
		htmlEscape(summary.Scope),
		summary.TotalSessions,
		summary.ActiveCount,
		valueOrDefault(presetBlocks, "<li><span>No presets</span><strong>0</strong></li>"),
		valueOrDefault(statusBlocks, "<li><span>No statuses</span><strong>0</strong></li>"),
		valueOrDefault(categoryBlocks, "<li><span>No categories</span><strong>0</strong></li>"),
		valueOrDefault(severityBlocks, "<li><span>No severities</span><strong>0</strong></li>"),
		valueOrDefault(subjectBlocks, "<li><span>No subjects</span><strong>0</strong></li>"),
		joinOrFallback(recent, `<article class="report-card"><div class="report-summary">No investigation sessions found.</div></article>`),
		htmlEscape(summary.LastUpdated.Format(time.RFC3339)),
	)
}

func createInvestigationDashboardHTML(summary InvestigationDashboardSummary) (string, error) {
	reportsDir := filepath.Join(userConfigDir(), "reports")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		return "", err
	}
	name := "investigation-dashboard-" + time.Now().Format("20060102-150405") + ".html"
	path := filepath.Join(reportsDir, name)
	if err := os.WriteFile(path, []byte(renderInvestigationDashboardHTML(summary)), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func sortFindingsByRisk(findings []InvestigationFinding) []InvestigationFinding {
	out := append([]InvestigationFinding(nil), findings...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RiskScore != out[j].RiskScore {
			return out[i].RiskScore > out[j].RiskScore
		}
		return out[i].Subject < out[j].Subject
	})
	return out
}

func (s *InvestigationStore) load() ([]InvestigationRecord, error) {
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
	var records []InvestigationRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	for i := range records {
		records[i] = normalizeInvestigationRecord(records[i])
	}
	return records, nil
}

func (s *InvestigationStore) save(records []InvestigationRecord) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(s.Path, data, 0o644)
}
