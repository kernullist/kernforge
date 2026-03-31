package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultPersistentMemoryMaxEntries  = 1200
	defaultPersistentMemoryContextMax  = 1800
	defaultPersistentMemorySearchLimit = 6
)

var memoryTokenPattern = regexp.MustCompile(`[A-Za-z0-9_./:-]{3,}`)

type PersistentMemoryImportance string

const (
	PersistentMemoryLow    PersistentMemoryImportance = "low"
	PersistentMemoryMedium PersistentMemoryImportance = "medium"
	PersistentMemoryHigh   PersistentMemoryImportance = "high"
)

type PersistentMemoryTrust string

const (
	PersistentMemoryTentative PersistentMemoryTrust = "tentative"
	PersistentMemoryConfirmed PersistentMemoryTrust = "confirmed"
)

type PersistentMemoryRecord struct {
	ID                  string                     `json:"id"`
	SessionID           string                     `json:"session_id"`
	SessionName         string                     `json:"session_name,omitempty"`
	Provider            string                     `json:"provider,omitempty"`
	Model               string                     `json:"model,omitempty"`
	Workspace           string                     `json:"workspace"`
	CreatedAt           time.Time                  `json:"created_at"`
	Request             string                     `json:"request"`
	Reply               string                     `json:"reply"`
	Summary             string                     `json:"summary"`
	Importance          PersistentMemoryImportance `json:"importance,omitempty"`
	Trust               PersistentMemoryTrust      `json:"trust,omitempty"`
	VerificationSummary string                     `json:"verification_summary,omitempty"`
	ToolNames           []string                   `json:"tool_names,omitempty"`
	Files               []string                   `json:"files,omitempty"`
	Keywords            []string                   `json:"keywords,omitempty"`
}

type PersistentMemoryStats struct {
	Path         string
	Count        int
	WorkspaceSet int
	LastUpdated  time.Time
}

type PersistentMemoryStore struct {
	Path       string
	MaxEntries int
}

type scoredMemory struct {
	record PersistentMemoryRecord
	score  int
}

type PersistentMemoryHit struct {
	Record   PersistentMemoryRecord
	Score    int
	Citation string
}

type PersistentMemoryQuery struct {
	Text       string
	Importance string
	Trust      string
}

type PersistentMemoryDashboardSummary struct {
	Scope        string
	FilterText   string
	TotalRecords int
	ByImportance []NamedCount
	ByTrust      []NamedCount
	TopFiles     []NamedCount
	Recent       []PersistentMemoryRecord
	LastUpdated  time.Time
}

func NewPersistentMemoryStore() *PersistentMemoryStore {
	return &PersistentMemoryStore{
		Path:       filepath.Join(userConfigDir(), "persistent-memory.json"),
		MaxEntries: defaultPersistentMemoryMaxEntries,
	}
}

func (s *PersistentMemoryStore) CaptureTurn(ws Workspace, sess *Session, rawUserText, finalReply string, turnMessages []Message) error {
	if s == nil || sess == nil {
		return nil
	}
	record, ok := buildPersistentMemoryRecord(ws, sess, rawUserText, finalReply, turnMessages)
	if !ok {
		return nil
	}
	return s.Append(record)
}

func buildPersistentMemoryRecord(ws Workspace, sess *Session, rawUserText, finalReply string, turnMessages []Message) (PersistentMemoryRecord, bool) {
	request := compactPersistentMemoryText(rawUserText, 320)
	reply := compactPersistentMemoryText(finalReply, 520)
	tools := uniqueStrings(extractPersistentMemoryTools(turnMessages))
	files := uniqueStrings(extractPersistentMemoryReferences(rawUserText))
	verificationSummary := ""
	if sess.LastVerification != nil {
		verificationSummary = compactPersistentMemoryText(sess.LastVerification.SummaryLine(), 220)
	}
	keywords := uniqueStrings(append(
		append(extractPersistentMemoryTokens(request), extractPersistentMemoryTokens(reply)...),
		append(append(tools, files...), extractPersistentMemoryTokens(verificationSummary)...)...,
	))
	if request == "" && reply == "" && len(tools) == 0 {
		return PersistentMemoryRecord{}, false
	}
	workspace := strings.TrimSpace(ws.BaseRoot)
	if workspace == "" {
		workspace = strings.TrimSpace(sess.WorkingDir)
	}
	workspace = filepath.Clean(workspace)
	now := time.Now()
	id := fmt.Sprintf("mem-%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/1_000_000)
	importance := derivePersistentMemoryImportance(request, reply, tools, files, verificationSummary)
	return PersistentMemoryRecord{
		ID:                  id,
		SessionID:           sess.ID,
		SessionName:         sess.Name,
		Provider:            sess.Provider,
		Model:               sess.Model,
		Workspace:           workspace,
		CreatedAt:           now,
		Request:             request,
		Reply:               reply,
		Summary:             buildPersistentMemorySummary(request, reply, tools, files, verificationSummary),
		Importance:          importance,
		Trust:               derivePersistentMemoryTrust(verificationSummary),
		VerificationSummary: verificationSummary,
		ToolNames:           tools,
		Files:               files,
		Keywords:            keywords,
	}, true
}

func buildPersistentMemorySummary(request, reply string, tools, files []string, verification string) string {
	var parts []string
	if request != "" {
		parts = append(parts, "Request: "+request)
	}
	if len(files) > 0 {
		parts = append(parts, "Refs: "+strings.Join(files, ", "))
	}
	if len(tools) > 0 {
		parts = append(parts, "Tools: "+strings.Join(tools, ", "))
	}
	if reply != "" {
		parts = append(parts, "Outcome: "+reply)
	}
	if verification != "" {
		parts = append(parts, "Verification: "+verification)
	}
	return joinNonEmpty(parts...)
}

func derivePersistentMemoryImportance(request, reply string, tools, files []string, verification string) PersistentMemoryImportance {
	score := 0
	if len(files) > 0 {
		score += 2
	}
	if len(files) > 1 {
		score++
	}
	if len(tools) > 0 {
		score++
	}
	if len(tools) > 1 {
		score++
	}
	if len(request) > 80 {
		score++
	}
	if len(reply) > 100 {
		score++
	}
	lowerVerification := strings.ToLower(strings.TrimSpace(verification))
	switch {
	case strings.Contains(lowerVerification, "passed=") && strings.Contains(lowerVerification, "failed=0"):
		score += 3
	case strings.Contains(lowerVerification, "failed="):
		score++
	}
	switch {
	case score >= 6:
		return PersistentMemoryHigh
	case score >= 2:
		return PersistentMemoryMedium
	default:
		return PersistentMemoryLow
	}
}

func derivePersistentMemoryTrust(verification string) PersistentMemoryTrust {
	lowerVerification := strings.ToLower(strings.TrimSpace(verification))
	if strings.Contains(lowerVerification, "passed=") && strings.Contains(lowerVerification, "failed=0") {
		return PersistentMemoryConfirmed
	}
	return PersistentMemoryTentative
}

func compactPersistentMemoryText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}

func extractPersistentMemoryTools(messages []Message) []string {
	var out []string
	for _, msg := range messages {
		if strings.TrimSpace(msg.ToolName) != "" {
			out = append(out, msg.ToolName)
		}
		for _, tc := range msg.ToolCalls {
			if strings.TrimSpace(tc.Name) != "" {
				out = append(out, tc.Name)
			}
		}
	}
	return out
}

func extractPersistentMemoryReferences(text string) []string {
	matches := mentionPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	var out []string
	for _, match := range matches {
		raw := strings.Trim(match[1], ".,:;()[]{}<>\"'")
		if raw == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(raw), "mcp:") {
			out = append(out, raw)
			continue
		}
		if mention := mentionRangePattern.FindStringSubmatch(raw); len(mention) == 4 {
			raw = mention[1]
		}
		out = append(out, raw)
	}
	return out
}

func extractPersistentMemoryTokens(text string) []string {
	text = strings.ToLower(text)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var out []string
	for _, token := range memoryTokenPattern.FindAllString(text, -1) {
		if len(token) < 3 {
			continue
		}
		out = append(out, token)
	}
	return out
}

func (s *PersistentMemoryStore) Append(record PersistentMemoryRecord) error {
	if s == nil {
		return nil
	}
	record = normalizePersistentMemoryRecord(record)
	records, err := s.load()
	if err != nil {
		return err
	}
	records = append(records, record)
	maxEntries := s.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultPersistentMemoryMaxEntries
	}
	if len(records) > maxEntries {
		records = append([]PersistentMemoryRecord(nil), records[len(records)-maxEntries:]...)
	}
	if err := s.save(records); err != nil {
		return err
	}
	policy, err := LoadPersistentMemoryPolicy(record.Workspace)
	if err == nil && policy.AutoPrune {
		_, _ = s.Prune(record.Workspace, policy, false)
	}
	return nil
}

func normalizePersistentMemoryRecord(record PersistentMemoryRecord) PersistentMemoryRecord {
	if record.Importance == "" {
		record.Importance = derivePersistentMemoryImportance(record.Request, record.Reply, record.ToolNames, record.Files, record.VerificationSummary)
	}
	if record.Trust == "" {
		record.Trust = derivePersistentMemoryTrust(record.VerificationSummary)
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	return record
}

func (s *PersistentMemoryStore) ListRecent(workspace string, limit int) ([]PersistentMemoryRecord, error) {
	records, err := s.load()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	currentWorkspace := normalizePersistentMemoryWorkspace(workspace)
	var filtered []PersistentMemoryRecord
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if currentWorkspace != "" && workspaceAffinityScore(currentWorkspace, record.Workspace) == 0 {
			continue
		}
		filtered = append(filtered, record)
		if len(filtered) >= limit {
			break
		}
	}
	return filtered, nil
}

func (s *PersistentMemoryStore) Get(id string) (PersistentMemoryRecord, bool, error) {
	records, err := s.load()
	if err != nil {
		return PersistentMemoryRecord{}, false, err
	}
	query := strings.TrimSpace(id)
	for _, record := range records {
		if strings.EqualFold(record.ID, query) {
			return record, true, nil
		}
	}
	return PersistentMemoryRecord{}, false, nil
}

func (s *PersistentMemoryStore) Update(id string, mutate func(*PersistentMemoryRecord) error) (PersistentMemoryRecord, bool, error) {
	records, err := s.load()
	if err != nil {
		return PersistentMemoryRecord{}, false, err
	}
	query := strings.TrimSpace(id)
	for i := range records {
		if !strings.EqualFold(records[i].ID, query) {
			continue
		}
		if err := mutate(&records[i]); err != nil {
			return PersistentMemoryRecord{}, false, err
		}
		records[i] = normalizePersistentMemoryRecord(records[i])
		if err := s.save(records); err != nil {
			return PersistentMemoryRecord{}, false, err
		}
		return records[i], true, nil
	}
	return PersistentMemoryRecord{}, false, nil
}

func (s *PersistentMemoryStore) Promote(id string) (PersistentMemoryRecord, bool, error) {
	return s.Update(id, func(record *PersistentMemoryRecord) error {
		switch record.Importance {
		case PersistentMemoryLow:
			record.Importance = PersistentMemoryMedium
		case PersistentMemoryMedium:
			record.Importance = PersistentMemoryHigh
		default:
			record.Importance = PersistentMemoryHigh
		}
		return nil
	})
}

func (s *PersistentMemoryStore) Demote(id string) (PersistentMemoryRecord, bool, error) {
	return s.Update(id, func(record *PersistentMemoryRecord) error {
		switch record.Importance {
		case PersistentMemoryHigh:
			record.Importance = PersistentMemoryMedium
		case PersistentMemoryMedium:
			record.Importance = PersistentMemoryLow
		default:
			record.Importance = PersistentMemoryLow
		}
		return nil
	})
}

func (s *PersistentMemoryStore) SetTrust(id string, trust PersistentMemoryTrust) (PersistentMemoryRecord, bool, error) {
	return s.Update(id, func(record *PersistentMemoryRecord) error {
		record.Trust = trust
		return nil
	})
}

func (s *PersistentMemoryStore) Search(query, workspace, excludeSession string, limit int) ([]PersistentMemoryRecord, error) {
	hits, err := s.SearchHits(query, workspace, excludeSession, limit)
	if err != nil {
		return nil, err
	}
	out := make([]PersistentMemoryRecord, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.Record)
	}
	return out, nil
}

func (s *PersistentMemoryStore) SearchHits(query, workspace, excludeSession string, limit int) ([]PersistentMemoryHit, error) {
	if s == nil {
		return nil, nil
	}
	records, err := s.load()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultPersistentMemorySearchLimit
	}
	currentWorkspace := normalizePersistentMemoryWorkspace(workspace)
	parsedQuery := parsePersistentMemoryQuery(query)
	loweredQuery := strings.ToLower(strings.TrimSpace(parsedQuery.Text))
	queryTokens := uniqueStrings(extractPersistentMemoryTokens(parsedQuery.Text))
	queryRefs := uniqueStrings(extractPersistentMemoryReferences(parsedQuery.Text))
	var scored []scoredMemory
	for _, record := range records {
		if strings.TrimSpace(excludeSession) != "" && strings.EqualFold(record.SessionID, excludeSession) {
			continue
		}
		if !persistentMemoryRecordMatchesFilters(record, parsedQuery) {
			continue
		}
		score := scorePersistentMemoryRecord(record, currentWorkspace, loweredQuery, queryTokens, queryRefs)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredMemory{record: record, score: score})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].record.CreatedAt.After(scored[j].record.CreatedAt)
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]PersistentMemoryHit, 0, len(scored))
	for _, item := range scored {
		out = append(out, PersistentMemoryHit{
			Record:   item.record,
			Score:    item.score,
			Citation: item.record.Citation(),
		})
	}
	return out, nil
}

func parsePersistentMemoryQuery(raw string) PersistentMemoryQuery {
	var textParts []string
	query := PersistentMemoryQuery{}
	for _, token := range strings.Fields(strings.TrimSpace(raw)) {
		lower := strings.ToLower(token)
		switch {
		case strings.HasPrefix(lower, "importance:"):
			query.Importance = strings.TrimSpace(token[len("importance:"):])
		case strings.HasPrefix(lower, "trust:"):
			query.Trust = strings.TrimSpace(token[len("trust:"):])
		default:
			textParts = append(textParts, token)
		}
	}
	query.Text = strings.TrimSpace(strings.Join(textParts, " "))
	return query
}

func persistentMemoryRecordMatchesFilters(record PersistentMemoryRecord, query PersistentMemoryQuery) bool {
	if strings.TrimSpace(query.Importance) != "" && !strings.EqualFold(record.ImportanceLabel(), query.Importance) {
		return false
	}
	if strings.TrimSpace(query.Trust) != "" && !strings.EqualFold(record.TrustLabel(), query.Trust) {
		return false
	}
	return true
}

func scorePersistentMemoryRecord(record PersistentMemoryRecord, currentWorkspace, loweredQuery string, queryTokens, queryRefs []string) int {
	workspaceScore := workspaceAffinityScore(currentWorkspace, record.Workspace)
	if len(queryTokens) == 0 && len(queryRefs) == 0 {
		return workspaceScore + persistentMemoryRecencyBoost(record.CreatedAt)
	}
	score := 0
	lowerSummary := strings.ToLower(record.Summary)
	lowerRequest := strings.ToLower(record.Request)
	lowerReply := strings.ToLower(record.Reply)
	lowerVerification := strings.ToLower(record.VerificationSummary)
	if loweredQuery != "" {
		if strings.Contains(lowerSummary, loweredQuery) {
			score += 28
		}
		if strings.Contains(lowerRequest, loweredQuery) {
			score += 18
		}
		if strings.Contains(lowerReply, loweredQuery) {
			score += 14
		}
	}
	for _, token := range queryTokens {
		if sliceContainsFold(record.Keywords, token) {
			score += 18
		}
		if strings.Contains(lowerSummary, token) {
			score += 12
		}
		if strings.Contains(lowerRequest, token) {
			score += 10
		}
		if strings.Contains(lowerReply, token) {
			score += 6
		}
		if strings.Contains(lowerVerification, token) {
			score += 12
		}
		for _, ref := range record.Files {
			lowerRef := strings.ToLower(ref)
			if strings.Contains(lowerRef, token) {
				score += 15
				break
			}
			if strings.Contains(strings.ToLower(filepath.Base(ref)), token) {
				score += 9
				break
			}
		}
		for _, tool := range record.ToolNames {
			if strings.Contains(strings.ToLower(tool), token) {
				score += 10
			}
		}
	}
	for _, refQuery := range queryRefs {
		lowerQueryRef := strings.ToLower(refQuery)
		for _, ref := range record.Files {
			lowerRef := strings.ToLower(ref)
			switch {
			case lowerRef == lowerQueryRef:
				score += 38
			case filepath.Base(lowerRef) == filepath.Base(lowerQueryRef):
				score += 22
			case strings.Contains(lowerRef, lowerQueryRef) || strings.Contains(lowerQueryRef, lowerRef):
				score += 16
			}
		}
	}
	if score == 0 {
		return 0
	}
	score += persistentMemoryImportanceBoost(record.Importance)
	score += persistentMemoryTrustBoost(record.Trust)
	score += workspaceScore
	score += persistentMemoryRecencyBoost(record.CreatedAt)
	return score
}

func persistentMemoryImportanceBoost(level PersistentMemoryImportance) int {
	switch level {
	case PersistentMemoryHigh:
		return 25
	case PersistentMemoryMedium:
		return 10
	default:
		return 0
	}
}

func persistentMemoryTrustBoost(level PersistentMemoryTrust) int {
	switch level {
	case PersistentMemoryConfirmed:
		return 18
	default:
		return 0
	}
}

func workspaceAffinityScore(current, record string) int {
	if current == "" || record == "" {
		return 0
	}
	current = normalizePersistentMemoryWorkspace(current)
	record = normalizePersistentMemoryWorkspace(record)
	if current == record {
		return 50
	}
	if strings.HasPrefix(current, record+string(filepath.Separator)) || strings.HasPrefix(record, current+string(filepath.Separator)) {
		return 30
	}
	return 0
}

func normalizePersistentMemoryWorkspace(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Clean(strings.TrimSpace(path))
}

func persistentMemoryRecencyBoost(createdAt time.Time) int {
	days := int(time.Since(createdAt).Hours() / 24)
	switch {
	case days <= 1:
		return 5
	case days <= 7:
		return 3
	case days <= 30:
		return 1
	default:
		return 0
	}
}

func sliceContainsFold(items []string, needle string) bool {
	for _, item := range items {
		if strings.EqualFold(item, needle) {
			return true
		}
	}
	return false
}

func (s *PersistentMemoryStore) RelevantContext(workspace, query, excludeSession string) string {
	if s == nil {
		return ""
	}
	hits, err := s.SearchHits(query, workspace, excludeSession, 3)
	if err != nil || len(hits) == 0 {
		return ""
	}
	var lines []string
	total := 0
	for _, hit := range hits {
		record := hit.Record
		line := fmt.Sprintf("- [%s | %s | %s] %s", record.Citation(), record.ImportanceLabel(), record.TrustLabel(), compactPersistentMemoryText(record.Summary, 420))
		if total+len(line) > defaultPersistentMemoryContextMax && len(lines) > 0 {
			break
		}
		total += len(line)
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func (r PersistentMemoryRecord) Citation() string {
	var parts []string
	parts = append(parts, r.ID)
	if !r.CreatedAt.IsZero() {
		parts = append(parts, r.CreatedAt.Format("2006-01-02"))
	}
	if strings.TrimSpace(r.SessionName) != "" {
		parts = append(parts, r.SessionName)
	} else if strings.TrimSpace(r.SessionID) != "" {
		parts = append(parts, r.SessionID)
	}
	if strings.TrimSpace(r.Provider) != "" || strings.TrimSpace(r.Model) != "" {
		parts = append(parts, strings.TrimSpace(r.Provider+" / "+r.Model))
	}
	return strings.Join(parts, " | ")
}

func (r PersistentMemoryRecord) ImportanceLabel() string {
	switch r.Importance {
	case PersistentMemoryHigh:
		return "high"
	case PersistentMemoryLow:
		return "low"
	default:
		return "medium"
	}
}

func (r PersistentMemoryRecord) TrustLabel() string {
	switch r.Trust {
	case PersistentMemoryConfirmed:
		return "confirmed"
	default:
		return "tentative"
	}
}

func (s *PersistentMemoryStore) Stats() (PersistentMemoryStats, error) {
	stats := PersistentMemoryStats{Path: s.Path}
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

func (s *PersistentMemoryStore) Dashboard(workspace string, query string, limit int) (PersistentMemoryDashboardSummary, error) {
	summary := PersistentMemoryDashboardSummary{
		Scope: "current workspace",
	}
	parsed := parsePersistentMemoryQuery(query)
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
	workspace = normalizePersistentMemoryWorkspace(workspace)
	byImportance := map[string]int{}
	byTrust := map[string]int{}
	topFiles := map[string]int{}
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if workspace != "" && workspaceAffinityScore(workspace, record.Workspace) == 0 {
			continue
		}
		if !persistentMemoryRecordMatchesFilters(record, parsed) {
			continue
		}
		summary.TotalRecords++
		byImportance[record.ImportanceLabel()]++
		byTrust[record.TrustLabel()]++
		for _, file := range record.Files {
			topFiles[file]++
		}
		if record.CreatedAt.After(summary.LastUpdated) {
			summary.LastUpdated = record.CreatedAt
		}
		if len(summary.Recent) < limit {
			summary.Recent = append(summary.Recent, record)
		}
	}
	summary.ByImportance = sortNamedCounts(byImportance)
	summary.ByTrust = sortNamedCounts(byTrust)
	summary.TopFiles = sortNamedCounts(topFiles)
	return summary, nil
}

func renderPersistentMemoryDashboard(summary PersistentMemoryDashboardSummary) string {
	if summary.TotalRecords == 0 {
		return "No persistent memory records found."
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
	if len(summary.ByImportance) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Importance distribution:")
		for _, item := range summary.ByImportance {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.ByTrust) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Trust distribution:")
		for _, item := range summary.ByTrust {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.TopFiles) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Most referenced files:")
		for _, item := range summary.TopFiles {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.Name, item.Count))
		}
	}
	if len(summary.Recent) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Recent memories:")
		for _, record := range summary.Recent {
			lines = append(lines, fmt.Sprintf("- [%s | %s | %s] %s", record.Citation(), record.ImportanceLabel(), record.TrustLabel(), compactPersistentMemoryText(record.Summary, 160)))
		}
	}
	return strings.Join(lines, "\n")
}

func renderPersistentMemoryDashboardHTML(summary PersistentMemoryDashboardSummary) string {
	importanceBlocks := renderDashboardBars(summary.ByImportance)
	trustBlocks := renderDashboardBars(summary.ByTrust)
	fileBlocks := renderDashboardBars(summary.TopFiles)
	var recent []string
	for _, record := range summary.Recent {
		var refs []string
		for _, ref := range record.Files {
			refs = append(refs, `<span class="tag">`+htmlEscape(ref)+`</span>`)
		}
		recent = append(recent, fmt.Sprintf(
			`<details class="report-detail"><summary><span>%s</span><span>%s / %s</span></summary><div class="report-body"><div class="subtle">%s</div><div class="subtle">%s</div><div class="subtle">%s</div><div class="tags">%s</div><pre>%s</pre></div></details>`,
			htmlEscape(record.Citation()),
			htmlEscape(record.ImportanceLabel()),
			htmlEscape(record.TrustLabel()),
			htmlEscape(valueOrDefault(record.VerificationSummary, "No verification summary")),
			htmlEscape(valueOrDefault(record.Request, "")),
			htmlEscape(valueOrDefault(record.Reply, "")),
			strings.Join(refs, " "),
			htmlEscape(record.Summary),
		))
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Persistent Memory Dashboard</title>
  <link href="https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&family=IBM+Plex+Mono:wght@400;500&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg: #07131f;
      --surface: rgba(14, 24, 39, 0.88);
      --surface-2: rgba(15, 23, 42, 0.72);
      --border: rgba(148, 163, 184, 0.16);
      --text: #e7eef8;
      --text-dim: #9db0ca;
      --accent: #60a5fa;
      --accent-2: #f59e0b;
      --success: #34d399;
      --danger: #fb7185;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: "Space Grotesk", system-ui, sans-serif;
      color: var(--text);
      background:
        radial-gradient(circle at top right, rgba(96,165,250,0.14), transparent 28%%),
        radial-gradient(circle at left center, rgba(245,158,11,0.10), transparent 24%%),
        linear-gradient(180deg, #06101a, #07131f 55%%, #0b1320);
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
    .tag { display: inline-flex; align-items: center; padding: 2px 8px; border-radius: 999px; background: rgba(96,165,250,0.10); border: 1px solid rgba(96,165,250,0.22); color: var(--accent); font: 500 11px/1 "IBM Plex Mono", monospace; margin: 2px 6px 2px 0; }
    .footer { margin-top: 18px; color: var(--text-dim); font: 400 12px/1.6 "IBM Plex Mono", monospace; }
    @media (max-width: 800px) { .kpi { grid-column: span 12; } .shell { padding: 24px 16px 36px; } }
  </style>
</head>
<body>
  <div class="shell">
    <section class="hero">
      <div class="eyebrow">Kernforge Persistent Memory</div>
      <h1>Cited memory, trust tiers, and recent knowledge</h1>
      <div class="subtitle">This dashboard summarizes the persistent memory stored for the current scope, including importance, trust, referenced files, and detailed drill-down for recent memories.</div>
    </section>
    <section class="grid">
      <article class="card kpi"><div class="label">Scope</div><div class="value">%s</div></article>
      <article class="card kpi"><div class="label">Filters</div><div class="value">%s</div></article>
      <article class="card kpi"><div class="label">Records</div><div class="value">%d</div></article>
      <article class="card" style="grid-column: span 4;"><h2 class="section-title">Importance</h2><ul class="metric-list">%s</ul></article>
      <article class="card" style="grid-column: span 4;"><h2 class="section-title">Trust</h2><ul class="metric-list">%s</ul></article>
      <article class="card" style="grid-column: span 4;"><h2 class="section-title">Most referenced files</h2><ul class="metric-list">%s</ul></article>
      <article class="card" style="grid-column: span 12;"><h2 class="section-title">Recent memory drill-down</h2><div class="subtle">Each record includes its citation, importance, trust label, verification summary, and referenced files.</div><div class="recent-list">%s</div></article>
    </section>
    <div class="footer">Last updated: %s</div>
  </div>
</body>
</html>`,
		htmlEscape(summary.Scope),
		htmlEscape(valueOrDefault(summary.FilterText, "none")),
		summary.TotalRecords,
		valueOrDefault(importanceBlocks, "<li><span>No records</span><strong>0</strong></li>"),
		valueOrDefault(trustBlocks, "<li><span>No records</span><strong>0</strong></li>"),
		valueOrDefault(fileBlocks, "<li><span>No references</span><strong>0</strong></li>"),
		joinOrFallback(recent, `<article class="report-card"><div class="report-summary">No recent memories found.</div></article>`),
		htmlEscape(summary.LastUpdated.Format(time.RFC3339)),
	)
}

func createPersistentMemoryDashboardHTML(summary PersistentMemoryDashboardSummary) (string, error) {
	reportsDir := filepath.Join(userConfigDir(), "reports")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		return "", err
	}
	name := "memory-dashboard-" + time.Now().Format("20060102-150405") + ".html"
	path := filepath.Join(reportsDir, name)
	if err := os.WriteFile(path, []byte(renderPersistentMemoryDashboardHTML(summary)), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (s *PersistentMemoryStore) load() ([]PersistentMemoryRecord, error) {
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
	var records []PersistentMemoryRecord
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *PersistentMemoryStore) save(records []PersistentMemoryRecord) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, data, 0o644)
}
