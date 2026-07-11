package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	implementationDecisionSchemaVersion   = 1
	implementationDecisionSummaryMaxBytes = 24 << 10

	implementationDecisionStatusCompleted = "completed"
	implementationDecisionStatusCorrected = "corrected"
	implementationDecisionStatusDeleted   = "deleted"
)

var ErrImplementationDecisionConflict = errors.New("implementation decision revision conflict")

type ImplementationDecisionOption struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Pros        []string `json:"pros,omitempty"`
	Cons        []string `json:"cons,omitempty"`
	Recommended bool     `json:"recommended,omitempty"`
	Order       int      `json:"order,omitempty"`
	Source      string   `json:"source,omitempty"`
}

type ImplementationDecisionRejection struct {
	OptionID string `json:"option_id"`
	Reason   string `json:"reason,omitempty"`
	Source   string `json:"source,omitempty"`
}

type ImplementationDecisionProvenance struct {
	Options   string `json:"options,omitempty"`
	Selection string `json:"selection,omitempty"`
	Rationale string `json:"rationale,omitempty"`
	Summary   string `json:"summary,omitempty"`
}

type ImplementationDecisionRedaction struct {
	Redacted bool     `json:"redacted,omitempty"`
	Patterns []string `json:"patterns,omitempty"`
}

type ImplementationDecisionRecord struct {
	SchemaVersion       int                               `json:"schema_version"`
	ID                  string                            `json:"id"`
	Revision            int                               `json:"revision"`
	CreatedAt           time.Time                         `json:"created_at"`
	UpdatedAt           time.Time                         `json:"updated_at"`
	SessionID           string                            `json:"session_id,omitempty"`
	FeatureID           string                            `json:"feature_id,omitempty"`
	GoalID              string                            `json:"goal_id,omitempty"`
	EditLoopID          string                            `json:"edit_loop_id,omitempty"`
	ProjectID           string                            `json:"project_id,omitempty"`
	ProjectAlias        string                            `json:"project_alias,omitempty"`
	Workspace           string                            `json:"workspace,omitempty"`
	WorkspaceHash       string                            `json:"workspace_hash,omitempty"`
	Domains             []string                          `json:"domains,omitempty"`
	Languages           []string                          `json:"languages,omitempty"`
	DecisionKind        string                            `json:"decision_kind,omitempty"`
	RiskLevel           string                            `json:"risk_level,omitempty"`
	Problem             string                            `json:"problem"`
	TaskFingerprint     string                            `json:"task_fingerprint,omitempty"`
	EvidenceFingerprint string                            `json:"evidence_fingerprint,omitempty"`
	Options             []ImplementationDecisionOption    `json:"options"`
	RecommendedOptionID string                            `json:"recommended_option_id,omitempty"`
	SelectedOptionID    string                            `json:"selected_option_id,omitempty"`
	CustomSelection     string                            `json:"custom_selection,omitempty"`
	SelectionReasonRaw  string                            `json:"selection_reason_raw,omitempty"`
	RejectedReasons     []ImplementationDecisionRejection `json:"rejected_reasons,omitempty"`
	SummarySentence     string                            `json:"summary_sentence,omitempty"`
	EvidenceRefs        []string                          `json:"evidence_refs,omitempty"`
	Tags                []string                          `json:"tags,omitempty"`
	DetectorConfidence  float64                           `json:"detector_confidence,omitempty"`
	Status              string                            `json:"status"`
	Provenance          ImplementationDecisionProvenance  `json:"provenance,omitempty"`
	Redaction           ImplementationDecisionRedaction   `json:"redaction,omitempty"`
	DeletedAt           *time.Time                        `json:"deleted_at,omitempty"`
}

type ImplementationDecisionFilter struct {
	ProjectID              string
	Workspace              string
	Domain                 string
	DecisionKind           string
	Status                 string
	Query                  string
	IncludeDeleted         bool
	IncludePrivateMetadata bool
	Limit                  int
}

type ImplementationDecisionStore struct {
	Dir string
}

type ImplementationDecisionReadIssue struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

func NewImplementationDecisionStore() *ImplementationDecisionStore {
	return &ImplementationDecisionStore{
		Dir: filepath.Join(userConfigDir(), "decision-rationales", "records"),
	}
}

func (s *ImplementationDecisionStore) Put(record ImplementationDecisionRecord) (ImplementationDecisionRecord, error) {
	if s == nil || strings.TrimSpace(s.Dir) == "" {
		return ImplementationDecisionRecord{}, fmt.Errorf("implementation decision store is not configured")
	}
	if strings.TrimSpace(record.ID) == "" {
		id, err := implementationDecisionIDForRecord(record)
		if err != nil {
			return ImplementationDecisionRecord{}, err
		}
		record.ID = id
	}
	path, err := s.recordPath(record.ID)
	if err != nil {
		return ImplementationDecisionRecord{}, err
	}
	if err := ensureImplementationDecisionPrivateDir(s.Dir); err != nil {
		return ImplementationDecisionRecord{}, err
	}
	unlock := lockFilePath(path)
	defer unlock()
	unlockProcess, err := lockImplementationDecisionFile(path + ".lock")
	if err != nil {
		return ImplementationDecisionRecord{}, err
	}
	defer unlockProcess()

	existing, ok, err := s.readRecord(path)
	if err != nil {
		return ImplementationDecisionRecord{}, err
	}
	if ok {
		candidate := record
		candidate.SchemaVersion = existing.SchemaVersion
		candidate.Revision = existing.Revision
		candidate.CreatedAt = existing.CreatedAt
		candidate.UpdatedAt = existing.UpdatedAt
		candidate.Redaction = existing.Redaction
		candidate = normalizeImplementationDecisionRecord(candidate, existing.UpdatedAt)
		candidate = redactImplementationDecisionRecord(candidate)
		if implementationDecisionEquivalent(existing, candidate) {
			return existing, nil
		}
		return ImplementationDecisionRecord{}, fmt.Errorf("%w: decision %s already exists at revision %d", ErrImplementationDecisionConflict, existing.ID, existing.Revision)
	}

	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	record.Revision = 1
	record.Redaction = ImplementationDecisionRedaction{}
	record = normalizeImplementationDecisionRecord(record, now)
	record = redactImplementationDecisionRecord(record)
	if err := validateImplementationDecisionRecord(record); err != nil {
		return ImplementationDecisionRecord{}, err
	}
	if err := writeImplementationDecisionRecord(path, record); err != nil {
		return ImplementationDecisionRecord{}, err
	}
	return record, nil
}

func (s *ImplementationDecisionStore) Get(id string) (ImplementationDecisionRecord, bool, error) {
	if s == nil || strings.TrimSpace(s.Dir) == "" {
		return ImplementationDecisionRecord{}, false, nil
	}
	path, err := s.recordPath(id)
	if err != nil {
		return ImplementationDecisionRecord{}, false, err
	}
	return s.readRecord(path)
}

func (s *ImplementationDecisionStore) List(filter ImplementationDecisionFilter) ([]ImplementationDecisionRecord, error) {
	records, issues, err := s.ListWithIssues(filter)
	if err != nil {
		return nil, err
	}
	if len(issues) > 0 {
		return nil, fmt.Errorf("implementation decision store has %d unreadable record(s); first: %s", len(issues), issues[0].Error)
	}
	return records, nil
}

func (s *ImplementationDecisionStore) ListWithIssues(filter ImplementationDecisionFilter) ([]ImplementationDecisionRecord, []ImplementationDecisionReadIssue, error) {
	if s == nil || strings.TrimSpace(s.Dir) == "" {
		return nil, nil, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	records := make([]ImplementationDecisionRecord, 0, len(entries))
	var issues []ImplementationDecisionReadIssue
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		record, ok, readErr := s.readRecord(filepath.Join(s.Dir, entry.Name()))
		if readErr != nil {
			issues = append(issues, ImplementationDecisionReadIssue{Path: filepath.Join(s.Dir, entry.Name()), Error: readErr.Error()})
			continue
		}
		if !ok || !implementationDecisionMatchesFilter(record, filter) {
			continue
		}
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool {
		if !records[i].UpdatedAt.Equal(records[j].UpdatedAt) {
			return records[i].UpdatedAt.After(records[j].UpdatedAt)
		}
		return records[i].ID > records[j].ID
	})
	if filter.Limit > 0 && len(records) > filter.Limit {
		records = append([]ImplementationDecisionRecord(nil), records[:filter.Limit]...)
	}
	return records, issues, nil
}

func (s *ImplementationDecisionStore) Search(query string, filter ImplementationDecisionFilter) ([]ImplementationDecisionRecord, error) {
	filter.Query = strings.TrimSpace(query)
	return s.List(filter)
}

func (s *ImplementationDecisionStore) Revise(id string, expectedRevision int, mutate func(*ImplementationDecisionRecord) error) (ImplementationDecisionRecord, error) {
	if s == nil || strings.TrimSpace(s.Dir) == "" {
		return ImplementationDecisionRecord{}, fmt.Errorf("implementation decision store is not configured")
	}
	if expectedRevision <= 0 {
		return ImplementationDecisionRecord{}, fmt.Errorf("expected revision must be positive")
	}
	if mutate == nil {
		return ImplementationDecisionRecord{}, fmt.Errorf("decision mutation callback is required")
	}
	path, err := s.recordPath(id)
	if err != nil {
		return ImplementationDecisionRecord{}, err
	}
	if err := ensureImplementationDecisionPrivateDir(s.Dir); err != nil {
		return ImplementationDecisionRecord{}, err
	}
	unlock := lockFilePath(path)
	defer unlock()
	unlockProcess, err := lockImplementationDecisionFile(path + ".lock")
	if err != nil {
		return ImplementationDecisionRecord{}, err
	}
	defer unlockProcess()
	record, ok, err := s.readRecord(path)
	if err != nil {
		return ImplementationDecisionRecord{}, err
	}
	if !ok {
		return ImplementationDecisionRecord{}, os.ErrNotExist
	}
	if record.Revision != expectedRevision {
		return ImplementationDecisionRecord{}, fmt.Errorf("%w: decision %s is revision %d, expected %d", ErrImplementationDecisionConflict, record.ID, record.Revision, expectedRevision)
	}
	originalRecord, err := cloneImplementationDecisionRecord(record)
	if err != nil {
		return ImplementationDecisionRecord{}, err
	}
	originalID := record.ID
	originalCreatedAt := record.CreatedAt
	originalRedaction := record.Redaction
	if err := mutate(&record); err != nil {
		return ImplementationDecisionRecord{}, err
	}
	record.ID = originalID
	record.CreatedAt = originalCreatedAt
	record.Redaction = originalRedaction
	record.Revision = expectedRevision + 1
	record.UpdatedAt = time.Now().UTC()
	if !strings.EqualFold(strings.TrimSpace(record.Provenance.Summary), "user") {
		record.SummarySentence = ""
	}
	if record.Status != implementationDecisionStatusDeleted {
		record.Status = implementationDecisionStatusCorrected
		record.DeletedAt = nil
	}
	record = normalizeImplementationDecisionRecord(record, record.UpdatedAt)
	record = redactImplementationDecisionRecord(record)
	if err := validateImplementationDecisionRecord(record); err != nil {
		return ImplementationDecisionRecord{}, err
	}
	if err := s.writeHistoryRecord(originalRecord); err != nil {
		return ImplementationDecisionRecord{}, err
	}
	if err := writeImplementationDecisionRecord(path, record); err != nil {
		return ImplementationDecisionRecord{}, err
	}
	return record, nil
}

func (s *ImplementationDecisionStore) SoftDelete(id string, expectedRevision int) (ImplementationDecisionRecord, error) {
	return s.Revise(id, expectedRevision, func(record *ImplementationDecisionRecord) error {
		now := time.Now().UTC()
		record.Status = implementationDecisionStatusDeleted
		record.DeletedAt = &now
		return nil
	})
}

func (s *ImplementationDecisionStore) Export(filter ImplementationDecisionFilter) ([]byte, error) {
	records, err := s.List(filter)
	if err != nil {
		return nil, err
	}
	if !filter.IncludePrivateMetadata {
		for index := range records {
			records[index].SessionID = ""
			records[index].FeatureID = ""
			records[index].GoalID = ""
			records[index].EditLoopID = ""
			records[index].Workspace = ""
			records[index].ProjectAlias = ""
			records[index].EvidenceRefs = nil
		}
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (s *ImplementationDecisionStore) History(id string) ([]ImplementationDecisionRecord, error) {
	if s == nil || strings.TrimSpace(s.Dir) == "" {
		return nil, nil
	}
	if !validImplementationDecisionID(strings.TrimSpace(id)) {
		return nil, fmt.Errorf("invalid implementation decision id %q", id)
	}
	dir := filepath.Join(filepath.Dir(s.Dir), "history", strings.TrimSpace(id))
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	revisions := map[int]ImplementationDecisionRecord{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		record, ok, readErr := s.readRecord(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			return nil, readErr
		}
		if ok {
			if !strings.EqualFold(record.ID, strings.TrimSpace(id)) {
				return nil, fmt.Errorf("implementation decision history id %q does not match %q", record.ID, id)
			}
			if prior, exists := revisions[record.Revision]; exists && !implementationDecisionEquivalent(prior, record) {
				return nil, fmt.Errorf("implementation decision history has conflicting revision %d", record.Revision)
			}
			revisions[record.Revision] = record
		}
	}
	current, ok, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if ok {
		if prior, exists := revisions[current.Revision]; exists && !implementationDecisionEquivalent(prior, current) {
			return nil, fmt.Errorf("implementation decision history conflicts with current revision %d", current.Revision)
		}
		revisions[current.Revision] = current
	}
	records := make([]ImplementationDecisionRecord, 0, len(revisions))
	for _, record := range revisions {
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].Revision < records[j].Revision
	})
	for index, record := range records {
		expected := index + 1
		if record.Revision != expected {
			return nil, fmt.Errorf("implementation decision history is missing revision %d", expected)
		}
	}
	return records, nil
}

func (s *ImplementationDecisionStore) recordPath(id string) (string, error) {
	id = strings.TrimSpace(id)
	if !validImplementationDecisionID(id) {
		return "", fmt.Errorf("invalid implementation decision id %q", id)
	}
	return filepath.Join(s.Dir, id+".json"), nil
}

func (s *ImplementationDecisionStore) readRecord(path string) (ImplementationDecisionRecord, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ImplementationDecisionRecord{}, false, nil
		}
		return ImplementationDecisionRecord{}, false, err
	}
	var record ImplementationDecisionRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return ImplementationDecisionRecord{}, false, fmt.Errorf("parse implementation decision %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ImplementationDecisionRecord{}, false, fmt.Errorf("parse implementation decision %s: trailing data", path)
	}
	if err := validateStoredImplementationDecisionRecord(record); err != nil {
		return ImplementationDecisionRecord{}, false, fmt.Errorf("validate implementation decision %s: %w", path, err)
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.HasPrefix(base, "revision-") {
		var filenameRevision int
		if _, err := fmt.Sscanf(base, "revision-%06d", &filenameRevision); err != nil || base != fmt.Sprintf("revision-%06d", filenameRevision) || filenameRevision != record.Revision {
			return ImplementationDecisionRecord{}, false, fmt.Errorf("implementation decision history filename %q does not match revision %d", base, record.Revision)
		}
	} else if !strings.EqualFold(base, record.ID) {
		return ImplementationDecisionRecord{}, false, fmt.Errorf("implementation decision filename %q does not match id %q", base, record.ID)
	}
	record = normalizeImplementationDecisionRecord(record, record.UpdatedAt)
	if err := validateImplementationDecisionRecord(record); err != nil {
		return ImplementationDecisionRecord{}, false, fmt.Errorf("validate implementation decision %s: %w", path, err)
	}
	return record, true, nil
}

func writeImplementationDecisionRecord(path string, record ImplementationDecisionRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(data, '\n'), 0o600)
}

func (s *ImplementationDecisionStore) writeHistoryRecord(record ImplementationDecisionRecord) error {
	historyRoot := filepath.Join(filepath.Dir(s.Dir), "history")
	historyDir := filepath.Join(historyRoot, record.ID)
	if err := ensureImplementationDecisionPrivateDir(historyRoot); err != nil {
		return err
	}
	if err := ensureImplementationDecisionPrivateDir(historyDir); err != nil {
		return err
	}
	historyPath := filepath.Join(historyDir, fmt.Sprintf("revision-%06d.json", record.Revision))
	if existing, ok, err := s.readRecord(historyPath); err != nil {
		return err
	} else if ok {
		if existing.ID != record.ID || existing.Revision != record.Revision || !implementationDecisionEquivalent(existing, record) {
			return fmt.Errorf("implementation decision history revision %d already exists with different content", record.Revision)
		}
		return nil
	}
	return writeImplementationDecisionRecord(historyPath, record)
}

func cloneImplementationDecisionRecord(record ImplementationDecisionRecord) (ImplementationDecisionRecord, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return ImplementationDecisionRecord{}, err
	}
	var cloned ImplementationDecisionRecord
	if err := json.Unmarshal(data, &cloned); err != nil {
		return ImplementationDecisionRecord{}, err
	}
	return cloned, nil
}

func newImplementationDecisionID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "decision-" + time.Now().UTC().Format("20060102-150405.000000000") + "-" + hex.EncodeToString(buf), nil
}

func implementationDecisionIDForRecord(record ImplementationDecisionRecord) (string, error) {
	if strings.TrimSpace(record.TaskFingerprint) == "" && strings.TrimSpace(record.EvidenceFingerprint) == "" {
		return newImplementationDecisionID()
	}
	optionIDs := make([]string, 0, len(record.Options))
	for _, option := range record.Options {
		optionIDs = append(optionIDs, strings.TrimSpace(option.ID)+"="+strings.TrimSpace(option.Label))
	}
	sort.Strings(optionIDs)
	key := strings.Join([]string{
		strings.TrimSpace(record.ProjectID),
		strings.TrimSpace(record.TaskFingerprint),
		strings.TrimSpace(record.EvidenceFingerprint),
		strings.TrimSpace(record.Problem),
		strings.Join(optionIDs, "|"),
	}, "\x00")
	digest := sha256.Sum256([]byte(key))
	return "decision-" + hex.EncodeToString(digest[:16]), nil
}

func validImplementationDecisionID(id string) bool {
	if id == "" || len(id) > 160 || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return !strings.Contains(id, "..")
}

func normalizeImplementationDecisionRecord(record ImplementationDecisionRecord, now time.Time) ImplementationDecisionRecord {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if record.SchemaVersion == 0 {
		record.SchemaVersion = implementationDecisionSchemaVersion
	}
	if record.Revision <= 0 {
		record.Revision = 1
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.CreatedAt
	}
	record.ID = strings.TrimSpace(record.ID)
	record.SessionID = strings.TrimSpace(record.SessionID)
	record.FeatureID = strings.TrimSpace(record.FeatureID)
	record.GoalID = strings.TrimSpace(record.GoalID)
	record.EditLoopID = strings.TrimSpace(record.EditLoopID)
	record.ProjectID = strings.TrimSpace(record.ProjectID)
	record.ProjectAlias = strings.TrimSpace(record.ProjectAlias)
	record.Workspace = strings.TrimSpace(record.Workspace)
	record.WorkspaceHash = strings.TrimSpace(record.WorkspaceHash)
	record.Domains = uniqueStrings(record.Domains)
	record.Languages = uniqueStrings(record.Languages)
	record.DecisionKind = strings.ToLower(strings.TrimSpace(record.DecisionKind))
	record.RiskLevel = strings.ToLower(strings.TrimSpace(record.RiskLevel))
	record.Problem = strings.TrimSpace(record.Problem)
	record.TaskFingerprint = strings.TrimSpace(record.TaskFingerprint)
	record.EvidenceFingerprint = strings.TrimSpace(record.EvidenceFingerprint)
	record.RecommendedOptionID = strings.TrimSpace(record.RecommendedOptionID)
	record.SelectedOptionID = strings.TrimSpace(record.SelectedOptionID)
	record.CustomSelection = strings.TrimSpace(record.CustomSelection)
	record.SelectionReasonRaw = strings.TrimSpace(record.SelectionReasonRaw)
	record.EvidenceRefs = normalizeTaskStateList(record.EvidenceRefs, 64)
	record.Tags = uniqueStrings(record.Tags)
	record.Status = strings.ToLower(strings.TrimSpace(record.Status))
	if record.Status == "" {
		record.Status = implementationDecisionStatusCompleted
	}
	seenOptions := map[string]bool{}
	for index := range record.Options {
		option := &record.Options[index]
		option.ID = strings.TrimSpace(option.ID)
		option.Label = strings.TrimSpace(option.Label)
		option.Description = strings.TrimSpace(option.Description)
		option.Pros = normalizeTaskStateList(option.Pros, 16)
		option.Cons = normalizeTaskStateList(option.Cons, 16)
		option.Source = strings.TrimSpace(option.Source)
		if option.Order <= 0 {
			option.Order = index + 1
		}
		if !seenOptions[option.ID] {
			seenOptions[option.ID] = true
		}
	}
	sort.SliceStable(record.Options, func(i, j int) bool {
		return record.Options[i].Order < record.Options[j].Order
	})
	for index := range record.RejectedReasons {
		record.RejectedReasons[index].OptionID = strings.TrimSpace(record.RejectedReasons[index].OptionID)
		record.RejectedReasons[index].Reason = strings.TrimSpace(record.RejectedReasons[index].Reason)
		record.RejectedReasons[index].Source = strings.TrimSpace(record.RejectedReasons[index].Source)
	}
	if strings.TrimSpace(record.SummarySentence) == "" {
		record.SummarySentence = renderImplementationDecisionSummary(record)
	} else {
		record.SummarySentence = strings.TrimSpace(record.SummarySentence)
	}
	record.Provenance.Options = strings.TrimSpace(record.Provenance.Options)
	record.Provenance.Selection = strings.TrimSpace(record.Provenance.Selection)
	record.Provenance.Rationale = strings.TrimSpace(record.Provenance.Rationale)
	record.Provenance.Summary = strings.TrimSpace(record.Provenance.Summary)
	return record
}

func validateImplementationDecisionRecord(record ImplementationDecisionRecord) error {
	if record.SchemaVersion != implementationDecisionSchemaVersion {
		return fmt.Errorf("unsupported implementation decision schema version %d", record.SchemaVersion)
	}
	if !validImplementationDecisionID(record.ID) {
		return fmt.Errorf("invalid implementation decision id %q", record.ID)
	}
	if strings.TrimSpace(record.Problem) == "" {
		return fmt.Errorf("implementation decision problem is required")
	}
	if len(record.Problem) > 4096 || len(record.SelectionReasonRaw) > 4096 || len(record.CustomSelection) > 1024 || len(record.SummarySentence) > implementationDecisionSummaryMaxBytes {
		return fmt.Errorf("implementation decision text exceeds the storage limit")
	}
	if len(record.Domains) > 32 || len(record.Languages) > 32 || len(record.Tags) > 64 || len(record.EvidenceRefs) > 64 {
		return fmt.Errorf("implementation decision list exceeds the storage limit")
	}
	for _, value := range append(append(append([]string{}, record.Domains...), record.Languages...), record.Tags...) {
		if len(value) > 512 {
			return fmt.Errorf("implementation decision list item exceeds the storage limit")
		}
	}
	for _, value := range record.EvidenceRefs {
		if len(value) > 2048 {
			return fmt.Errorf("implementation decision evidence reference exceeds the storage limit")
		}
	}
	identifierValues := []string{record.SessionID, record.FeatureID, record.GoalID, record.EditLoopID, record.ProjectID, record.WorkspaceHash, record.TaskFingerprint, record.EvidenceFingerprint, record.DecisionKind, record.RiskLevel}
	for _, value := range append(identifierValues, record.ProjectAlias) {
		if len(value) > 1024 {
			return fmt.Errorf("implementation decision metadata exceeds the storage limit")
		}
	}
	for _, value := range identifierValues {
		if _, report := redactSensitiveText(value); report.Redacted {
			return fmt.Errorf("implementation decision identifier metadata contains sensitive-looking text")
		}
	}
	if len(record.Options) < 2 || len(record.Options) > 4 {
		return fmt.Errorf("implementation decision requires 2 to 4 options")
	}
	optionIDs := map[string]bool{}
	recommended := ""
	for _, option := range record.Options {
		if option.ID == "" || option.Label == "" {
			return fmt.Errorf("implementation decision options require id and label")
		}
		if len(option.ID) > 128 || len(option.Label) > 256 || len(option.Description) > 2048 || len(option.Pros) > 16 || len(option.Cons) > 16 {
			return fmt.Errorf("implementation decision option exceeds the storage limit")
		}
		if !validImplementationDecisionOptionID(option.ID) {
			return fmt.Errorf("implementation decision option has invalid id %q", option.ID)
		}
		if _, report := redactSensitiveText(option.ID); report.Redacted {
			return fmt.Errorf("implementation decision option id contains sensitive-looking text")
		}
		if !validImplementationDecisionSource(option.Source, true) {
			return fmt.Errorf("implementation decision option has invalid source %q", option.Source)
		}
		for _, value := range append(append([]string{}, option.Pros...), option.Cons...) {
			if len(value) > 1024 {
				return fmt.Errorf("implementation decision option detail exceeds the storage limit")
			}
		}
		if optionIDs[option.ID] {
			return fmt.Errorf("duplicate implementation decision option id %q", option.ID)
		}
		optionIDs[option.ID] = true
		if option.Recommended {
			if recommended != "" {
				return fmt.Errorf("implementation decision must not have multiple recommended options")
			}
			recommended = option.ID
		}
	}
	if record.RecommendedOptionID != "" && !optionIDs[record.RecommendedOptionID] {
		return fmt.Errorf("recommended option %q is not present", record.RecommendedOptionID)
	}
	if recommended != "" && record.RecommendedOptionID != "" && recommended != record.RecommendedOptionID {
		return fmt.Errorf("recommended option fields disagree")
	}
	if record.SelectedOptionID == "" && record.CustomSelection == "" {
		return fmt.Errorf("implementation decision requires a selected or custom option")
	}
	if record.SelectedOptionID != "" && record.CustomSelection != "" {
		return fmt.Errorf("implementation decision cannot select a listed and custom option together")
	}
	if record.SelectedOptionID != "" && !optionIDs[record.SelectedOptionID] {
		return fmt.Errorf("selected option %q is not present", record.SelectedOptionID)
	}
	if strings.TrimSpace(record.SelectionReasonRaw) == "" {
		return fmt.Errorf("implementation decision requires a selection rationale")
	}
	seenRejections := map[string]bool{}
	for _, rejection := range record.RejectedReasons {
		if !optionIDs[rejection.OptionID] {
			return fmt.Errorf("rejected option %q is not present", rejection.OptionID)
		}
		if rejection.OptionID == record.SelectedOptionID {
			return fmt.Errorf("selected option %q cannot be rejected", rejection.OptionID)
		}
		if seenRejections[rejection.OptionID] {
			return fmt.Errorf("duplicate rejection for option %q", rejection.OptionID)
		}
		if len(rejection.Reason) > 4096 {
			return fmt.Errorf("implementation decision rejection exceeds the storage limit")
		}
		if strings.TrimSpace(rejection.Reason) == "" {
			return fmt.Errorf("implementation decision requires a rejection rationale for option %q", rejection.OptionID)
		}
		if !validImplementationDecisionSource(rejection.Source, true) {
			return fmt.Errorf("implementation decision rejection has invalid source %q", rejection.Source)
		}
		seenRejections[rejection.OptionID] = true
	}
	for optionID := range optionIDs {
		if optionID == record.SelectedOptionID {
			continue
		}
		if !seenRejections[optionID] {
			return fmt.Errorf("implementation decision requires a rejection entry for option %q", optionID)
		}
	}
	if record.DetectorConfidence < 0 || record.DetectorConfidence > 1 {
		return fmt.Errorf("detector confidence must be between 0 and 1")
	}
	switch record.Status {
	case implementationDecisionStatusCompleted, implementationDecisionStatusCorrected, implementationDecisionStatusDeleted:
	default:
		return fmt.Errorf("unsupported implementation decision status %q", record.Status)
	}
	if record.Status == implementationDecisionStatusDeleted && record.DeletedAt == nil {
		return fmt.Errorf("deleted implementation decision requires deleted_at")
	}
	if record.Status != implementationDecisionStatusDeleted && record.DeletedAt != nil {
		return fmt.Errorf("active implementation decision must not have deleted_at")
	}
	for _, value := range []string{record.Provenance.Options, record.Provenance.Selection, record.Provenance.Rationale, record.Provenance.Summary} {
		if !validImplementationDecisionProvenance(value) {
			return fmt.Errorf("unsupported implementation decision provenance %q", value)
		}
	}
	return nil
}

func validateStoredImplementationDecisionRecord(record ImplementationDecisionRecord) error {
	if record.SchemaVersion != implementationDecisionSchemaVersion {
		return fmt.Errorf("unsupported implementation decision schema version %d", record.SchemaVersion)
	}
	if record.Revision <= 0 || record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() || strings.TrimSpace(record.Status) == "" {
		return fmt.Errorf("stored implementation decision is missing required lifecycle metadata")
	}
	return nil
}

func validImplementationDecisionProvenance(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "user", "model", "runtime", "imported":
		return true
	default:
		return false
	}
}

func validImplementationDecisionSource(value string, allowEmpty bool) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return allowEmpty
	}
	switch value {
	case "user", "model", "runtime", "imported":
		return true
	default:
		return false
	}
}

func redactImplementationDecisionRecord(record ImplementationDecisionRecord) ImplementationDecisionRecord {
	previous := record.Redaction
	report := ReviewRedactionReport{Status: "clean"}
	redact := func(value string) string {
		redacted, current := redactSensitiveText(value)
		report = mergeReviewRedactionReports(report, current)
		return redacted
	}
	record.Problem = redact(record.Problem)
	record.ProjectAlias = redact(record.ProjectAlias)
	record.Workspace = redact(record.Workspace)
	record.CustomSelection = redact(record.CustomSelection)
	record.SelectionReasonRaw = redact(record.SelectionReasonRaw)
	record.SummarySentence = redact(record.SummarySentence)
	for index := range record.Options {
		record.Options[index].Label = redact(record.Options[index].Label)
		record.Options[index].Description = redact(record.Options[index].Description)
		for itemIndex := range record.Options[index].Pros {
			record.Options[index].Pros[itemIndex] = redact(record.Options[index].Pros[itemIndex])
		}
		for itemIndex := range record.Options[index].Cons {
			record.Options[index].Cons[itemIndex] = redact(record.Options[index].Cons[itemIndex])
		}
	}
	for index := range record.Domains {
		record.Domains[index] = redact(record.Domains[index])
	}
	for index := range record.Languages {
		record.Languages[index] = redact(record.Languages[index])
	}
	for index := range record.Tags {
		record.Tags[index] = redact(record.Tags[index])
	}
	for index := range record.RejectedReasons {
		record.RejectedReasons[index].Reason = redact(record.RejectedReasons[index].Reason)
	}
	for index := range record.EvidenceRefs {
		record.EvidenceRefs[index] = redact(record.EvidenceRefs[index])
	}
	record.Redaction.Redacted = previous.Redacted || report.Redacted
	record.Redaction.Patterns = uniqueStrings(append(previous.Patterns, report.Patterns...))
	return record
}

func renderImplementationDecisionSummary(record ImplementationDecisionRecord) string {
	selected := strings.TrimSpace(record.CustomSelection)
	if selected == "" {
		for _, option := range record.Options {
			if option.ID == record.SelectedOptionID {
				selected = option.Label
				break
			}
		}
	}
	if selected == "" {
		return ""
	}
	var rejected []string
	labels := map[string]string{}
	for _, option := range record.Options {
		labels[option.ID] = option.Label
	}
	for _, item := range record.RejectedReasons {
		label := valueOrDefault(labels[item.OptionID], item.OptionID)
		reason := valueOrDefault(item.Reason, "reason not provided")
		rejected = append(rejected, fmt.Sprintf("%s: %s", label, reason))
	}
	reason := valueOrDefault(record.SelectionReasonRaw, "reason not provided")
	summary := fmt.Sprintf("%s 문제를 해결할 때 이 사용자는 %s 결정을 %s 때문에 했다.", record.Problem, selected, reason)
	if len(rejected) > 0 {
		summary += " 다른 방안은 " + strings.Join(rejected, "; ") + " 때문에 선택하지 않았다."
	}
	return summary
}

func implementationDecisionMatchesFilter(record ImplementationDecisionRecord, filter ImplementationDecisionFilter) bool {
	if !filter.IncludeDeleted && record.Status == implementationDecisionStatusDeleted {
		return false
	}
	if filter.ProjectID != "" && !strings.EqualFold(strings.TrimSpace(filter.ProjectID), record.ProjectID) {
		return false
	}
	if filter.Workspace != "" && workspaceAffinityScore(filter.Workspace, record.Workspace) == 0 {
		return false
	}
	if filter.Domain != "" && !sliceContainsFold(record.Domains, strings.TrimSpace(filter.Domain)) {
		return false
	}
	if filter.DecisionKind != "" && !strings.EqualFold(strings.TrimSpace(filter.DecisionKind), record.DecisionKind) {
		return false
	}
	if filter.Status != "" && !strings.EqualFold(strings.TrimSpace(filter.Status), record.Status) {
		return false
	}
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	if query == "" {
		return true
	}
	parts := []string{record.ID, record.ProjectID, record.ProjectAlias, record.Problem, record.DecisionKind, record.SelectionReasonRaw, record.SummarySentence, record.CustomSelection}
	parts = append(parts, record.Domains...)
	parts = append(parts, record.Tags...)
	for _, option := range record.Options {
		parts = append(parts, option.ID, option.Label, option.Description)
		parts = append(parts, option.Pros...)
		parts = append(parts, option.Cons...)
	}
	for _, item := range record.RejectedReasons {
		parts = append(parts, item.OptionID, item.Reason)
	}
	return strings.Contains(strings.ToLower(strings.Join(parts, "\n")), query)
}

func implementationDecisionEquivalent(left ImplementationDecisionRecord, right ImplementationDecisionRecord) bool {
	leftData, leftErr := json.Marshal(left)
	rightData, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftData) == string(rightData)
}
