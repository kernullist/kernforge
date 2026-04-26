package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultEvidenceMaxEntries = 4000

type EvidenceRecord struct {
	ID                  string            `json:"id"`
	SessionID           string            `json:"session_id,omitempty"`
	Workspace           string            `json:"workspace"`
	CreatedAt           time.Time         `json:"created_at"`
	Kind                string            `json:"kind"`
	Category            string            `json:"category,omitempty"`
	Subject             string            `json:"subject"`
	Outcome             string            `json:"outcome,omitempty"`
	Severity            string            `json:"severity,omitempty"`
	Confidence          string            `json:"confidence,omitempty"`
	SignalClass         string            `json:"signal_class,omitempty"`
	RiskScore           int               `json:"risk_score,omitempty"`
	SeverityReasons     []string          `json:"severity_reasons,omitempty"`
	VerificationSummary string            `json:"verification_summary,omitempty"`
	Tags                []string          `json:"tags,omitempty"`
	Attributes          map[string]string `json:"attributes,omitempty"`
}

type EvidenceStats struct {
	Path         string
	Count        int
	WorkspaceSet int
	LastUpdated  time.Time
}

type EvidenceDashboardSummary struct {
	Scope           string
	FilterText      string
	TotalRecords    int
	ByKind          []NamedCount
	ByCategory      []NamedCount
	ByOutcome       []NamedCount
	BySeverity      []NamedCount
	BySignalClass   []NamedCount
	TopTags         []NamedCount
	TopSubjects     []NamedCount
	Recent          []EvidenceRecord
	ActiveOverrides []HookOverrideRecord
	LastUpdated     time.Time
}

type EvidenceSearchQuery struct {
	Text     string
	Kind     string
	Category string
	Tag      string
	Outcome  string
	Severity string
	Signal   string
	MinRisk  int
}

type EvidenceStore struct {
	Path       string
	MaxEntries int
}

func NewEvidenceStore() *EvidenceStore {
	return &EvidenceStore{
		Path:       filepath.Join(userConfigDir(), "evidence.json"),
		MaxEntries: defaultEvidenceMaxEntries,
	}
}

func (s *EvidenceStore) Append(record EvidenceRecord) error {
	if s == nil {
		return nil
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	return s.appendLocked([]EvidenceRecord{record})
}

func (s *EvidenceStore) CaptureVerification(ws Workspace, sess *Session) error {
	if s == nil || sess == nil || sess.LastVerification == nil {
		return nil
	}
	records := buildEvidenceRecords(ws, sess)
	if len(records) == 0 {
		return nil
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	return s.appendLocked(records)
}

func (s *EvidenceStore) appendLocked(items []EvidenceRecord) error {
	if len(items) == 0 {
		return nil
	}
	records, err := s.load()
	if err != nil {
		return err
	}
	now := time.Now()
	for _, item := range items {
		record := normalizeEvidenceRecord(item)
		record = scoreEvidenceRecord(record, buildEvidenceScoringContext(records, record.Workspace, now))
		records = append(records, record)
	}
	maxEntries := s.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultEvidenceMaxEntries
	}
	if len(records) > maxEntries {
		records = append([]EvidenceRecord(nil), records[len(records)-maxEntries:]...)
	}
	return s.save(records)
}

func buildEvidenceRecords(ws Workspace, sess *Session) []EvidenceRecord {
	if sess == nil || sess.LastVerification == nil {
		return nil
	}
	report := sess.LastVerification
	workspace := strings.TrimSpace(ws.BaseRoot)
	if workspace == "" {
		workspace = strings.TrimSpace(ws.Root)
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	var out []EvidenceRecord
	now := time.Now()

	for _, category := range report.SecurityCategories() {
		out = append(out, EvidenceRecord{
			SessionID:           sess.ID,
			Workspace:           workspace,
			CreatedAt:           now,
			Kind:                "verification_category",
			Category:            category,
			Subject:             category,
			Outcome:             evidenceOverallOutcome(report),
			VerificationSummary: report.SummaryLine(),
			Tags:                uniqueStrings(append([]string{category}, report.VerificationTags()...)),
		})
	}

	for _, artifact := range report.VerificationArtifacts() {
		out = append(out, EvidenceRecord{
			SessionID:           sess.ID,
			Workspace:           workspace,
			CreatedAt:           now,
			Kind:                "verification_artifact",
			Category:            guessEvidenceCategoryForArtifact(artifact, report.SecurityCategories()),
			Subject:             artifact,
			Outcome:             evidenceArtifactOutcome(report, artifact),
			VerificationSummary: report.SummaryLine(),
			Tags:                uniqueStrings(append([]string{"artifact"}, evidenceTagsForArtifact(report, artifact)...)),
			Attributes: map[string]string{
				"basename": filepath.Base(artifact),
			},
		})
	}

	for _, failure := range report.FailureKinds() {
		out = append(out, EvidenceRecord{
			SessionID:           sess.ID,
			Workspace:           workspace,
			CreatedAt:           now,
			Kind:                "verification_failure",
			Category:            guessEvidenceCategoryForFailure(report),
			Subject:             failure,
			Outcome:             "failed",
			VerificationSummary: report.SummaryLine(),
			Tags:                uniqueStrings(append([]string{"failure"}, report.VerificationTags()...)),
		})
	}
	ctx := buildEvidenceScoringContext(nil, workspace, now)
	for i := range out {
		out[i] = scoreEvidenceRecord(out[i], ctx)
	}
	return normalizeEvidenceRecords(out)
}

func normalizeEvidenceRecords(records []EvidenceRecord) []EvidenceRecord {
	var out []EvidenceRecord
	seen := map[string]bool{}
	for _, record := range records {
		record = normalizeEvidenceRecord(record)
		key := strings.ToLower(strings.Join([]string{
			record.SessionID,
			record.Kind,
			record.Category,
			record.Subject,
			record.Outcome,
			evidenceRecordDedupDiscriminator(record),
		}, "\x1f"))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, record)
	}
	return out
}

func evidenceRecordDedupDiscriminator(record EvidenceRecord) string {
	if !strings.EqualFold(record.Kind, "fuzz_native_result") {
		return ""
	}
	attrs := record.Attributes
	return strings.Join([]string{
		record.VerificationSummary,
		attrs["campaign_id"],
		attrs["fuzz_run_id"],
		attrs["finding_id"],
		attrs["report_path"],
		attrs["crash_dir"],
		attrs["crash_fingerprint"],
		attrs["artifact_ids"],
	}, "\x1e")
}

func normalizeEvidenceRecord(record EvidenceRecord) EvidenceRecord {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	if strings.TrimSpace(record.ID) == "" {
		record.ID = fmt.Sprintf("ev-%s-%09d", record.CreatedAt.Format("20060102-150405"), record.CreatedAt.Nanosecond())
	}
	record.Workspace = normalizePersistentMemoryWorkspace(record.Workspace)
	record.Kind = strings.TrimSpace(record.Kind)
	record.Category = strings.TrimSpace(record.Category)
	record.Subject = strings.TrimSpace(record.Subject)
	record.Outcome = strings.TrimSpace(record.Outcome)
	record.Severity = strings.ToLower(strings.TrimSpace(record.Severity))
	if record.Severity == "" {
		switch record.Kind {
		case "verification_failure", "verification_artifact":
			record.Severity = "medium"
		default:
			record.Severity = "low"
		}
	}
	record.Confidence = strings.ToLower(strings.TrimSpace(record.Confidence))
	if record.Confidence == "" {
		record.Confidence = "low"
	}
	record.SignalClass = strings.ToLower(strings.TrimSpace(record.SignalClass))
	record.SeverityReasons = uniqueStrings(record.SeverityReasons)
	if record.RiskScore < 0 {
		record.RiskScore = 0
	}
	if record.RiskScore > 100 {
		record.RiskScore = 100
	}
	record.VerificationSummary = compactPersistentMemoryText(record.VerificationSummary, 220)
	record.Tags = uniqueStrings(record.Tags)
	return record
}

func (s *EvidenceStore) Stats() (EvidenceStats, error) {
	stats := EvidenceStats{Path: s.Path}
	records, err := s.load()
	if err != nil {
		return stats, err
	}
	stats.Count = len(records)
	workspaces := map[string]bool{}
	for _, record := range records {
		if strings.TrimSpace(record.Workspace) != "" {
			workspaces[record.Workspace] = true
		}
		if record.CreatedAt.After(stats.LastUpdated) {
			stats.LastUpdated = record.CreatedAt
		}
	}
	stats.WorkspaceSet = len(workspaces)
	return stats, nil
}

func (s *EvidenceStore) ListRecent(workspace string, limit int) ([]EvidenceRecord, error) {
	records, err := s.load()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 12
	}
	currentWorkspace := normalizePersistentMemoryWorkspace(workspace)
	var out []EvidenceRecord
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if currentWorkspace != "" && workspaceAffinityScore(currentWorkspace, record.Workspace) == 0 {
			continue
		}
		out = append(out, record)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *EvidenceStore) Get(id string) (EvidenceRecord, bool, error) {
	records, err := s.load()
	if err != nil {
		return EvidenceRecord{}, false, err
	}
	query := strings.TrimSpace(id)
	for _, record := range records {
		if strings.EqualFold(record.ID, query) {
			return record, true, nil
		}
	}
	return EvidenceRecord{}, false, nil
}

func (s *EvidenceStore) Search(query, workspace string, limit int) ([]EvidenceRecord, error) {
	records, err := s.load()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 12
	}
	parsed := parseEvidenceSearchQuery(query)
	currentWorkspace := normalizePersistentMemoryWorkspace(workspace)
	var filtered []EvidenceRecord
	for _, record := range records {
		if currentWorkspace != "" && workspaceAffinityScore(currentWorkspace, record.Workspace) == 0 {
			continue
		}
		if !evidenceRecordMatchesQuery(record, parsed) {
			continue
		}
		filtered = append(filtered, record)
	}
	sort.Slice(filtered, func(i, j int) bool {
		left := evidenceSearchScore(filtered[i], parsed)
		right := evidenceSearchScore(filtered[j], parsed)
		if left != right {
			return left > right
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

func (s *EvidenceStore) Dashboard(workspace, query string, limit int) (EvidenceDashboardSummary, error) {
	summary := EvidenceDashboardSummary{
		Scope: "current workspace",
	}
	if strings.TrimSpace(query) != "" {
		summary.FilterText = strings.TrimSpace(query)
	}
	records, err := s.load()
	if err != nil {
		return summary, err
	}
	if limit <= 0 {
		limit = 12
	}
	currentWorkspace := normalizePersistentMemoryWorkspace(workspace)
	parsed := parseEvidenceSearchQuery(query)
	byKind := map[string]int{}
	byCategory := map[string]int{}
	byOutcome := map[string]int{}
	bySeverity := map[string]int{}
	bySignalClass := map[string]int{}
	topTags := map[string]int{}
	topSubjects := map[string]int{}
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if currentWorkspace != "" && workspaceAffinityScore(currentWorkspace, record.Workspace) == 0 {
			continue
		}
		if !evidenceRecordMatchesQuery(record, parsed) {
			continue
		}
		summary.TotalRecords++
		if strings.TrimSpace(record.Kind) != "" {
			byKind[record.Kind]++
		}
		if strings.TrimSpace(record.Category) != "" {
			byCategory[record.Category]++
		}
		if strings.TrimSpace(record.Outcome) != "" {
			byOutcome[record.Outcome]++
		}
		if strings.TrimSpace(record.Severity) != "" {
			bySeverity[record.Severity]++
		}
		if strings.TrimSpace(record.SignalClass) != "" {
			bySignalClass[record.SignalClass]++
		}
		if strings.TrimSpace(record.Subject) != "" {
			topSubjects[record.Subject]++
		}
		for _, tag := range record.Tags {
			topTags[tag]++
		}
		if record.CreatedAt.After(summary.LastUpdated) {
			summary.LastUpdated = record.CreatedAt
		}
		if len(summary.Recent) < limit {
			summary.Recent = append(summary.Recent, record)
		}
	}
	summary.ByKind = sortNamedCounts(byKind)
	summary.ByCategory = sortNamedCounts(byCategory)
	summary.ByOutcome = sortNamedCounts(byOutcome)
	summary.BySeverity = sortNamedCounts(bySeverity)
	summary.BySignalClass = sortNamedCounts(bySignalClass)
	summary.TopTags = sortNamedCounts(topTags)
	summary.TopSubjects = sortNamedCounts(topSubjects)
	return summary, nil
}

func (s *EvidenceStore) RecentFailures(workspace string, limit int) ([]EvidenceRecord, error) {
	return s.Search("outcome:failed", workspace, limit)
}

func (s *EvidenceStore) load() ([]EvidenceRecord, error) {
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
	var records []EvidenceRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *EvidenceStore) save(records []EvidenceRecord) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(s.Path, data, 0o644)
}

func evidenceOverallOutcome(report *VerificationReport) string {
	if report == nil {
		return ""
	}
	if report.HasFailures() {
		return "failed"
	}
	return "passed"
}

func evidenceArtifactOutcome(report *VerificationReport, artifact string) string {
	if report == nil {
		return ""
	}
	target := strings.ToLower(strings.TrimSpace(artifact))
	outcome := evidenceOverallOutcome(report)
	for _, step := range report.Steps {
		scope := strings.ToLower(strings.TrimSpace(step.Scope))
		if scope == "" {
			continue
		}
		if scope == target || strings.Contains(scope, target) || strings.Contains(target, scope) {
			switch step.Status {
			case VerificationFailed:
				return "failed"
			case VerificationPassed:
				outcome = "passed"
			}
		}
	}
	return outcome
}

func evidenceTagsForArtifact(report *VerificationReport, artifact string) []string {
	target := strings.ToLower(strings.TrimSpace(artifact))
	var tags []string
	for _, step := range report.Steps {
		scope := strings.ToLower(strings.TrimSpace(step.Scope))
		if scope == "" {
			continue
		}
		if scope == target || strings.Contains(scope, target) || strings.Contains(target, scope) {
			tags = append(tags, step.Tags...)
		}
	}
	return uniqueStrings(tags)
}

func guessEvidenceCategoryForArtifact(artifact string, categories []string) string {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(artifact)))
	switch {
	case strings.HasSuffix(lower, ".sys"), strings.HasSuffix(lower, ".inf"), strings.HasSuffix(lower, ".cat"), strings.Contains(lower, "/driver/"), strings.HasPrefix(lower, "driver/"):
		return "driver"
	case strings.HasSuffix(lower, ".man"), strings.HasSuffix(lower, ".xml"), strings.HasSuffix(lower, ".mc"), strings.Contains(lower, "provider"), strings.Contains(lower, "telemetry"):
		return "telemetry"
	}
	if len(categories) > 0 {
		return strings.TrimSpace(categories[0])
	}
	return ""
}

func guessEvidenceCategoryForFailure(report *VerificationReport) string {
	if report == nil {
		return ""
	}
	categories := report.SecurityCategories()
	if len(categories) > 0 {
		return categories[0]
	}
	return ""
}

func sortEvidenceRecords(records []EvidenceRecord) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID < records[j].ID
		}
		return records[i].CreatedAt.Before(records[j].CreatedAt)
	})
}

func parseEvidenceSearchQuery(raw string) EvidenceSearchQuery {
	var textParts []string
	query := EvidenceSearchQuery{}
	for _, token := range strings.Fields(strings.TrimSpace(raw)) {
		lower := strings.ToLower(token)
		switch {
		case strings.HasPrefix(lower, "kind:"):
			query.Kind = strings.TrimSpace(token[len("kind:"):])
		case strings.HasPrefix(lower, "category:"):
			query.Category = strings.TrimSpace(token[len("category:"):])
		case strings.HasPrefix(lower, "tag:"):
			query.Tag = strings.TrimSpace(token[len("tag:"):])
		case strings.HasPrefix(lower, "outcome:"):
			query.Outcome = strings.TrimSpace(token[len("outcome:"):])
		case strings.HasPrefix(lower, "severity:"):
			query.Severity = strings.TrimSpace(token[len("severity:"):])
		case strings.HasPrefix(lower, "signal:"):
			query.Signal = strings.TrimSpace(token[len("signal:"):])
		case strings.HasPrefix(lower, "risk:>="):
			query.MinRisk = intValueFromAny(parseIntLoose(strings.TrimSpace(token[len("risk:>="):])))
		default:
			textParts = append(textParts, token)
		}
	}
	query.Text = strings.TrimSpace(strings.Join(textParts, " "))
	return query
}

func parseIntLoose(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}

func evidenceRecordMatchesQuery(record EvidenceRecord, query EvidenceSearchQuery) bool {
	if strings.TrimSpace(query.Kind) != "" && !strings.EqualFold(record.Kind, query.Kind) {
		return false
	}
	if strings.TrimSpace(query.Category) != "" && !strings.EqualFold(record.Category, query.Category) {
		return false
	}
	if strings.TrimSpace(query.Tag) != "" && !sliceContainsFold(record.Tags, query.Tag) {
		return false
	}
	if strings.TrimSpace(query.Outcome) != "" && !strings.EqualFold(record.Outcome, query.Outcome) {
		return false
	}
	if strings.TrimSpace(query.Severity) != "" && !strings.EqualFold(record.Severity, query.Severity) {
		return false
	}
	if strings.TrimSpace(query.Signal) != "" && !strings.EqualFold(record.SignalClass, query.Signal) {
		return false
	}
	if query.MinRisk > 0 && record.RiskScore < query.MinRisk {
		return false
	}
	if strings.TrimSpace(query.Text) == "" {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(query.Text))
	if strings.Contains(strings.ToLower(record.Subject), lower) {
		return true
	}
	if strings.Contains(strings.ToLower(record.VerificationSummary), lower) {
		return true
	}
	if strings.Contains(strings.ToLower(record.Category), lower) {
		return true
	}
	if strings.Contains(strings.ToLower(record.Severity), lower) {
		return true
	}
	if strings.Contains(strings.ToLower(record.SignalClass), lower) {
		return true
	}
	for _, tag := range record.Tags {
		if strings.Contains(strings.ToLower(tag), lower) {
			return true
		}
	}
	for key, value := range record.Attributes {
		if strings.Contains(strings.ToLower(key), lower) || strings.Contains(strings.ToLower(value), lower) {
			return true
		}
	}
	return false
}

func evidenceSearchScore(record EvidenceRecord, query EvidenceSearchQuery) int {
	score := persistentMemoryRecencyBoost(record.CreatedAt)
	if strings.TrimSpace(query.Text) == "" {
		return score
	}
	lower := strings.ToLower(strings.TrimSpace(query.Text))
	if strings.Contains(strings.ToLower(record.Subject), lower) {
		score += 24
	}
	if strings.Contains(strings.ToLower(record.VerificationSummary), lower) {
		score += 12
	}
	if strings.Contains(strings.ToLower(record.Category), lower) {
		score += 10
	}
	if strings.Contains(strings.ToLower(record.Severity), lower) {
		score += 10
	}
	if strings.Contains(strings.ToLower(record.SignalClass), lower) {
		score += 10
	}
	if query.MinRisk > 0 && record.RiskScore >= query.MinRisk {
		score += 12
	}
	for _, tag := range record.Tags {
		if strings.Contains(strings.ToLower(tag), lower) {
			score += 10
		}
	}
	return score
}

func renderEvidenceDashboard(summary EvidenceDashboardSummary) string {
	if summary.TotalRecords == 0 {
		return "No evidence records found."
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Scope: %s", summary.Scope))
	if strings.TrimSpace(summary.FilterText) != "" {
		lines = append(lines, "Filters: "+summary.FilterText)
	}
	lines = append(lines, fmt.Sprintf("Records: total=%d", summary.TotalRecords))
	if !summary.LastUpdated.IsZero() {
		lines = append(lines, "Last updated: "+summary.LastUpdated.Format(time.RFC3339))
	}
	if len(summary.ByKind) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Evidence kinds:")
		for _, item := range summary.ByKind {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.ByCategory) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Evidence categories:")
		for _, item := range summary.ByCategory {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.ByOutcome) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Evidence outcomes:")
		for _, item := range summary.ByOutcome {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.BySeverity) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Evidence severities:")
		for _, item := range summary.BySeverity {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.BySignalClass) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Evidence signal classes:")
		for _, item := range summary.BySignalClass {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.TopTags) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Top evidence tags:")
		for _, item := range summary.TopTags {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.TopSubjects) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Top evidence subjects:")
		for _, item := range summary.TopSubjects {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.ActiveOverrides) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Active hook overrides:")
		for _, item := range summary.ActiveOverrides {
			line := fmt.Sprintf("- %s: expires=%s", item.RuleID, item.ExpiresAt.Format(time.RFC3339))
			if strings.TrimSpace(item.Reason) != "" {
				line += " | " + item.Reason
			}
			lines = append(lines, line)
		}
	}
	if len(summary.Recent) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Recent evidence:")
		for _, record := range summary.Recent {
			line := fmt.Sprintf("- [%s] %s", record.Kind, record.Subject)
			if strings.TrimSpace(record.Category) != "" {
				line += " | category=" + record.Category
			}
			if strings.TrimSpace(record.Outcome) != "" {
				line += " | outcome=" + record.Outcome
			}
			if strings.TrimSpace(record.Severity) != "" {
				line += " | severity=" + record.Severity
			}
			if strings.TrimSpace(record.SignalClass) != "" {
				line += " | signal=" + record.SignalClass
			}
			if record.RiskScore > 0 {
				line += fmt.Sprintf(" | risk=%d", record.RiskScore)
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func renderEvidenceDashboardHTML(summary EvidenceDashboardSummary) string {
	kindBlocks := renderDashboardBars(summary.ByKind)
	categoryBlocks := renderDashboardBars(summary.ByCategory)
	outcomeBlocks := renderDashboardBars(summary.ByOutcome)
	severityBlocks := renderDashboardBars(summary.BySeverity)
	signalBlocks := renderDashboardBars(summary.BySignalClass)
	tagBlocks := renderDashboardBars(summary.TopTags)
	subjectBlocks := renderDashboardBars(summary.TopSubjects)
	var overrideRows []string
	for _, item := range summary.ActiveOverrides {
		overrideRows = append(overrideRows, fmt.Sprintf(
			`<li><span>%s</span><strong>%s</strong></li>`,
			htmlEscape(item.RuleID),
			htmlEscape(item.ExpiresAt.Format("2006-01-02 15:04")),
		))
	}
	var recent []string
	for _, record := range summary.Recent {
		var attrs []string
		for key, value := range record.Attributes {
			attrs = append(attrs, key+"="+value)
		}
		sort.Strings(attrs)
		recent = append(recent, fmt.Sprintf(
			`<details class="report-detail"><summary><span>%s</span><span>%s / %s</span></summary><div class="report-body"><div class="subtle">%s</div><div class="subtle">%s</div><div class="subtle">%s</div><div class="subtle">%s</div><div class="subtle">%s</div><pre>%s</pre></div></details>`,
			htmlEscape(record.Subject),
			htmlEscape(valueOrDefault(record.Kind, "unknown")),
			htmlEscape(valueOrDefault(record.Outcome, "unknown")),
			htmlEscape(valueOrDefault(record.Category, "No category")),
			htmlEscape(valueOrDefault(joinOrFallback([]string{record.Severity, record.SignalClass}, " / "), "No severity or signal")),
			htmlEscape(valueOrDefault(strings.Join(record.Tags, ", "), "No tags")),
			htmlEscape(valueOrDefault(strings.Join(attrs, ", "), "No attributes")),
			htmlEscape(valueOrDefault("risk="+fmt.Sprintf("%d", record.RiskScore), "No risk score")),
			htmlEscape(valueOrDefault(record.VerificationSummary, "No verification summary")),
		))
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Evidence Dashboard</title>
  <link href="https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&family=IBM+Plex+Mono:wght@400;500&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg: #081017;
      --surface: rgba(13, 20, 33, 0.9);
      --surface-2: rgba(16, 24, 39, 0.78);
      --border: rgba(148, 163, 184, 0.16);
      --text: #e7eef8;
      --text-dim: #9db0ca;
      --accent: #34d399;
      --accent-2: #60a5fa;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: "Space Grotesk", system-ui, sans-serif;
      color: var(--text);
      background:
        radial-gradient(circle at top right, rgba(52,211,153,0.14), transparent 28%%),
        radial-gradient(circle at left center, rgba(96,165,250,0.10), transparent 24%%),
        linear-gradient(180deg, #050c13, #081017 55%%, #0b1220);
      min-height: 100vh;
    }
    .shell { max-width: 1120px; margin: 0 auto; padding: 40px 24px 56px; }
    .hero { display: grid; gap: 16px; margin-bottom: 28px; }
    .eyebrow { font: 500 12px/1 "IBM Plex Mono", monospace; letter-spacing: 0.14em; text-transform: uppercase; color: var(--accent); }
    h1 { margin: 0; font-size: clamp(34px, 5vw, 56px); line-height: 0.95; }
    .subtitle { max-width: 820px; color: var(--text-dim); font-size: 15px; line-height: 1.7; }
    .grid { display: grid; grid-template-columns: repeat(12, 1fr); gap: 18px; }
    .card { grid-column: span 12; background: var(--surface); border: 1px solid var(--border); border-radius: 22px; padding: 22px; backdrop-filter: blur(14px); box-shadow: 0 20px 50px rgba(0,0,0,0.25); }
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
    .footer { margin-top: 18px; color: var(--text-dim); font: 400 12px/1.6 "IBM Plex Mono", monospace; }
    @media (max-width: 800px) { .kpi { grid-column: span 12; } .shell { padding: 24px 16px 36px; } }
  </style>
</head>
<body>
  <div class="shell">
    <section class="hero">
      <div class="eyebrow">Kernforge Evidence</div>
      <h1>Verification evidence and policy signals</h1>
      <div class="subtitle">This dashboard summarizes structured evidence captured from verification, including kinds, categories, outcomes, tags, subjects, and recent drill-down.</div>
    </section>
    <section class="grid">
      <article class="card kpi"><div class="label">Scope</div><div class="value">%s</div></article>
      <article class="card kpi"><div class="label">Filters</div><div class="value">%s</div></article>
      <article class="card kpi"><div class="label">Records</div><div class="value">%d</div></article>
      <article class="card" style="grid-column: span 4;"><h2 class="section-title">Kinds</h2><ul class="metric-list">%s</ul></article>
      <article class="card" style="grid-column: span 4;"><h2 class="section-title">Categories</h2><ul class="metric-list">%s</ul></article>
      <article class="card" style="grid-column: span 4;"><h2 class="section-title">Outcomes</h2><ul class="metric-list">%s</ul></article>
      <article class="card" style="grid-column: span 6;"><h2 class="section-title">Severities</h2><ul class="metric-list">%s</ul></article>
      <article class="card" style="grid-column: span 6;"><h2 class="section-title">Signal classes</h2><ul class="metric-list">%s</ul></article>
      <article class="card" style="grid-column: span 6;"><h2 class="section-title">Top tags</h2><ul class="metric-list">%s</ul></article>
      <article class="card" style="grid-column: span 6;"><h2 class="section-title">Top subjects</h2><ul class="metric-list">%s</ul></article>
      <article class="card" style="grid-column: span 12;"><h2 class="section-title">Active hook overrides</h2><ul class="metric-list">%s</ul></article>
      <article class="card" style="grid-column: span 12;"><h2 class="section-title">Recent evidence drill-down</h2><div class="subtle">Each record includes its kind, outcome, category, tags, attributes, and verification summary.</div><div class="recent-list">%s</div></article>
    </section>
    <div class="footer">Last updated: %s</div>
  </div>
</body>
</html>`,
		htmlEscape(summary.Scope),
		htmlEscape(valueOrDefault(summary.FilterText, "none")),
		summary.TotalRecords,
		valueOrDefault(kindBlocks, "<li><span>No records</span><strong>0</strong></li>"),
		valueOrDefault(categoryBlocks, "<li><span>No categories</span><strong>0</strong></li>"),
		valueOrDefault(outcomeBlocks, "<li><span>No outcomes</span><strong>0</strong></li>"),
		valueOrDefault(severityBlocks, "<li><span>No severities</span><strong>0</strong></li>"),
		valueOrDefault(signalBlocks, "<li><span>No signals</span><strong>0</strong></li>"),
		valueOrDefault(tagBlocks, "<li><span>No tags</span><strong>0</strong></li>"),
		valueOrDefault(subjectBlocks, "<li><span>No subjects</span><strong>0</strong></li>"),
		joinOrFallback(overrideRows, `<li><span>No active overrides</span><strong>0</strong></li>`),
		joinOrFallback(recent, `<article class="report-card"><div class="report-summary">No recent evidence found.</div></article>`),
		htmlEscape(summary.LastUpdated.Format(time.RFC3339)),
	)
}

func createEvidenceDashboardHTML(summary EvidenceDashboardSummary) (string, error) {
	reportsDir := filepath.Join(userConfigDir(), "reports")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		return "", err
	}
	name := "evidence-dashboard-" + time.Now().Format("20060102-150405") + ".html"
	path := filepath.Join(reportsDir, name)
	if err := os.WriteFile(path, []byte(renderEvidenceDashboardHTML(summary)), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
