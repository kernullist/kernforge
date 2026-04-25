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

const defaultFuzzCampaignMaxEntries = 200

type FuzzCampaignSeedTarget struct {
	Name             string   `json:"name"`
	File             string   `json:"file,omitempty"`
	SymbolID         string   `json:"symbol_id,omitempty"`
	SourceAnchor     string   `json:"source_anchor,omitempty"`
	PriorityScore    int      `json:"priority_score,omitempty"`
	PriorityReasons  []string `json:"priority_reasons,omitempty"`
	SuggestedCommand string   `json:"suggested_command,omitempty"`
	Provenance       string   `json:"provenance,omitempty"`
}

type FuzzCampaignSeedArtifact struct {
	RunID      string   `json:"run_id"`
	Scenario   string   `json:"scenario"`
	Path       string   `json:"path"`
	Source     string   `json:"source"`
	Inputs     []string `json:"inputs,omitempty"`
	SourceHint string   `json:"source_hint,omitempty"`
}

type FuzzCampaignNativeResult struct {
	RunID              string    `json:"run_id"`
	Target             string    `json:"target,omitempty"`
	TargetFile         string    `json:"target_file,omitempty"`
	Status             string    `json:"status,omitempty"`
	Outcome            string    `json:"outcome,omitempty"`
	CrashCount         int       `json:"crash_count,omitempty"`
	CrashFingerprint   string    `json:"crash_fingerprint,omitempty"`
	SuspectedInvariant string    `json:"suspected_invariant,omitempty"`
	MinimizeCommand    string    `json:"minimize_command,omitempty"`
	ReportPath         string    `json:"report_path,omitempty"`
	BuildLogPath       string    `json:"build_log_path,omitempty"`
	RunLogPath         string    `json:"run_log_path,omitempty"`
	CrashDir           string    `json:"crash_dir,omitempty"`
	ArtifactIDs        []string  `json:"artifact_ids,omitempty"`
	EvidenceID         string    `json:"evidence_id,omitempty"`
	RecordedAt         time.Time `json:"recorded_at,omitempty"`
}

type FuzzCampaignFinding struct {
	ID                 string    `json:"id"`
	DedupKey           string    `json:"dedup_key,omitempty"`
	Status             string    `json:"status"`
	Severity           string    `json:"severity,omitempty"`
	Target             string    `json:"target,omitempty"`
	TargetFile         string    `json:"target_file,omitempty"`
	SourceAnchor       string    `json:"source_anchor,omitempty"`
	FuzzRunID          string    `json:"fuzz_run_id,omitempty"`
	SeedArtifacts      []string  `json:"seed_artifacts,omitempty"`
	NativeResultKey    string    `json:"native_result_key,omitempty"`
	NativeResultKeys   []string  `json:"native_result_keys,omitempty"`
	EvidenceID         string    `json:"evidence_id,omitempty"`
	EvidenceIDs        []string  `json:"evidence_ids,omitempty"`
	VerificationGate   string    `json:"verification_gate,omitempty"`
	TrackedFeatureGate string    `json:"tracked_feature_gate,omitempty"`
	CrashFingerprint   string    `json:"crash_fingerprint,omitempty"`
	SuspectedInvariant string    `json:"suspected_invariant,omitempty"`
	ReportPath         string    `json:"report_path,omitempty"`
	DuplicateCount     int       `json:"duplicate_count,omitempty"`
	MergedFindingIDs   []string  `json:"merged_finding_ids,omitempty"`
	CreatedAt          time.Time `json:"created_at,omitempty"`
	UpdatedAt          time.Time `json:"updated_at,omitempty"`
}

type FuzzCampaignCoverageGap struct {
	ID            string    `json:"id"`
	Target        string    `json:"target,omitempty"`
	TargetFile    string    `json:"target_file,omitempty"`
	SourceAnchor  string    `json:"source_anchor,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	PriorityBoost int       `json:"priority_boost,omitempty"`
	CampaignID    string    `json:"campaign_id,omitempty"`
	RunID         string    `json:"run_id,omitempty"`
	EvidenceID    string    `json:"evidence_id,omitempty"`
	ReportPath    string    `json:"report_path,omitempty"`
	LastSeenAt    time.Time `json:"last_seen_at,omitempty"`
}

type FuzzCampaignCoverageReport struct {
	ID              string    `json:"id"`
	Format          string    `json:"format,omitempty"`
	Target          string    `json:"target,omitempty"`
	TargetFile      string    `json:"target_file,omitempty"`
	SourceAnchor    string    `json:"source_anchor,omitempty"`
	RunID           string    `json:"run_id,omitempty"`
	Path            string    `json:"path,omitempty"`
	Summary         string    `json:"summary,omitempty"`
	LinesCovered    int       `json:"lines_covered,omitempty"`
	LinesTotal      int       `json:"lines_total,omitempty"`
	CoveragePercent float64   `json:"coverage_percent,omitempty"`
	FeatureCount    int       `json:"feature_count,omitempty"`
	CorpusCount     int       `json:"corpus_count,omitempty"`
	Gap             bool      `json:"gap,omitempty"`
	GapReason       string    `json:"gap_reason,omitempty"`
	PriorityBoost   int       `json:"priority_boost,omitempty"`
	RecordedAt      time.Time `json:"recorded_at,omitempty"`
}

type FuzzCampaignRunArtifact struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	RunID        string    `json:"run_id,omitempty"`
	Target       string    `json:"target,omitempty"`
	TargetFile   string    `json:"target_file,omitempty"`
	SourceAnchor string    `json:"source_anchor,omitempty"`
	Path         string    `json:"path,omitempty"`
	Summary      string    `json:"summary,omitempty"`
	Severity     string    `json:"severity,omitempty"`
	Signal       string    `json:"signal,omitempty"`
	EvidenceID   string    `json:"evidence_id,omitempty"`
	RecordedAt   time.Time `json:"recorded_at,omitempty"`
}

type FuzzCampaignArtifactGraph struct {
	Schema string                  `json:"schema,omitempty"`
	Nodes  []FuzzCampaignGraphNode `json:"nodes,omitempty"`
	Edges  []FuzzCampaignGraphEdge `json:"edges,omitempty"`
}

type FuzzCampaignGraphNode struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Label      string `json:"label,omitempty"`
	Path       string `json:"path,omitempty"`
	EvidenceID string `json:"evidence_id,omitempty"`
}

type FuzzCampaignGraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

type FuzzCampaign struct {
	ID              string                       `json:"id"`
	Workspace       string                       `json:"workspace"`
	Name            string                       `json:"name"`
	Status          string                       `json:"status"`
	CreatedAt       time.Time                    `json:"created_at"`
	UpdatedAt       time.Time                    `json:"updated_at"`
	ArtifactDir     string                       `json:"artifact_dir"`
	ManifestPath    string                       `json:"manifest_path"`
	CorpusDir       string                       `json:"corpus_dir"`
	CrashDir        string                       `json:"crash_dir"`
	CoverageDir     string                       `json:"coverage_dir"`
	ReportsDir      string                       `json:"reports_dir"`
	LogsDir         string                       `json:"logs_dir"`
	SeedTargets     []FuzzCampaignSeedTarget     `json:"seed_targets,omitempty"`
	FunctionRuns    []string                     `json:"function_runs,omitempty"`
	SeedArtifacts   []FuzzCampaignSeedArtifact   `json:"seed_artifacts,omitempty"`
	NativeResults   []FuzzCampaignNativeResult   `json:"native_results,omitempty"`
	Findings        []FuzzCampaignFinding        `json:"findings,omitempty"`
	CoverageReports []FuzzCampaignCoverageReport `json:"coverage_reports,omitempty"`
	CoverageGaps    []FuzzCampaignCoverageGap    `json:"coverage_gaps,omitempty"`
	RunArtifacts    []FuzzCampaignRunArtifact    `json:"run_artifacts,omitempty"`
	ArtifactGraph   FuzzCampaignArtifactGraph    `json:"artifact_graph,omitempty"`
	Summary         string                       `json:"summary,omitempty"`
}

type FuzzCampaignStore struct {
	Path       string
	MaxEntries int
}

func NewFuzzCampaignStore() *FuzzCampaignStore {
	return &FuzzCampaignStore{
		Path:       filepath.Join(userConfigDir(), "fuzz_campaigns.json"),
		MaxEntries: defaultFuzzCampaignMaxEntries,
	}
}

func (s *FuzzCampaignStore) Append(campaign FuzzCampaign) (FuzzCampaign, error) {
	if s == nil {
		return FuzzCampaign{}, nil
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	campaign = normalizeFuzzCampaign(campaign)
	items, err := s.load()
	if err != nil {
		return FuzzCampaign{}, err
	}
	items = append(items, campaign)
	maxEntries := s.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultFuzzCampaignMaxEntries
	}
	if len(items) > maxEntries {
		items = append([]FuzzCampaign(nil), items[len(items)-maxEntries:]...)
	}
	if err := s.save(items); err != nil {
		return FuzzCampaign{}, err
	}
	return campaign, nil
}

func (s *FuzzCampaignStore) Upsert(campaign FuzzCampaign) (FuzzCampaign, error) {
	if s == nil {
		return FuzzCampaign{}, nil
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	campaign = normalizeFuzzCampaign(campaign)
	items, err := s.load()
	if err != nil {
		return FuzzCampaign{}, err
	}
	replaced := false
	for i := range items {
		if strings.EqualFold(strings.TrimSpace(items[i].ID), strings.TrimSpace(campaign.ID)) {
			items[i] = campaign
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, campaign)
	}
	maxEntries := s.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultFuzzCampaignMaxEntries
	}
	if len(items) > maxEntries {
		items = append([]FuzzCampaign(nil), items[len(items)-maxEntries:]...)
	}
	if err := s.save(items); err != nil {
		return FuzzCampaign{}, err
	}
	return campaign, nil
}

func (s *FuzzCampaignStore) ListRecent(workspace string, limit int) ([]FuzzCampaign, error) {
	items, err := s.load()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	var out []FuzzCampaign
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

func (s *FuzzCampaignStore) Get(id string) (FuzzCampaign, bool, error) {
	items, err := s.load()
	if err != nil {
		return FuzzCampaign{}, false, err
	}
	query := strings.TrimSpace(id)
	if strings.EqualFold(query, "latest") || query == "" {
		if len(items) == 0 {
			return FuzzCampaign{}, false, nil
		}
		return items[len(items)-1], true, nil
	}
	for _, item := range items {
		if strings.EqualFold(item.ID, query) || strings.HasPrefix(strings.ToLower(item.ID), strings.ToLower(query)) {
			return item, true, nil
		}
	}
	return FuzzCampaign{}, false, nil
}

func (s *FuzzCampaignStore) load() ([]FuzzCampaign, error) {
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
	var items []FuzzCampaign
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	for i := range items {
		items[i] = normalizeFuzzCampaign(items[i])
	}
	return items, nil
}

func (s *FuzzCampaignStore) save(items []FuzzCampaign) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.Path, append(data, '\n'), 0o644)
}

func normalizeFuzzCampaign(campaign FuzzCampaign) FuzzCampaign {
	now := time.Now()
	if campaign.CreatedAt.IsZero() {
		campaign.CreatedAt = now
	}
	if campaign.UpdatedAt.IsZero() {
		campaign.UpdatedAt = campaign.CreatedAt
	}
	if strings.TrimSpace(campaign.ID) == "" {
		campaign.ID = fmt.Sprintf("campaign-%s-%03d", campaign.CreatedAt.Format("20060102-150405"), campaign.CreatedAt.Nanosecond()/1_000_000)
	}
	campaign.Workspace = normalizePersistentMemoryWorkspace(campaign.Workspace)
	campaign.Name = strings.TrimSpace(campaign.Name)
	if campaign.Name == "" {
		campaign.Name = "Fuzz campaign"
	}
	campaign.Status = strings.ToLower(strings.TrimSpace(campaign.Status))
	if campaign.Status == "" {
		campaign.Status = "planned"
	}
	campaign.ArtifactDir = functionFuzzNormalizeOptionalPath(campaign.ArtifactDir)
	campaign.ManifestPath = functionFuzzNormalizeOptionalPath(campaign.ManifestPath)
	campaign.CorpusDir = functionFuzzNormalizeOptionalPath(campaign.CorpusDir)
	campaign.CrashDir = functionFuzzNormalizeOptionalPath(campaign.CrashDir)
	campaign.CoverageDir = functionFuzzNormalizeOptionalPath(campaign.CoverageDir)
	campaign.ReportsDir = functionFuzzNormalizeOptionalPath(campaign.ReportsDir)
	campaign.LogsDir = functionFuzzNormalizeOptionalPath(campaign.LogsDir)
	campaign.SeedTargets = normalizeFuzzCampaignSeedTargets(campaign.SeedTargets)
	campaign.FunctionRuns = uniqueStrings(campaign.FunctionRuns)
	campaign.SeedArtifacts = normalizeFuzzCampaignSeedArtifacts(campaign.SeedArtifacts)
	campaign.NativeResults = normalizeFuzzCampaignNativeResults(campaign.NativeResults)
	campaign.Findings = normalizeFuzzCampaignFindings(campaign.Findings)
	campaign.CoverageReports = normalizeFuzzCampaignCoverageReports(campaign.CoverageReports)
	campaign.CoverageGaps = normalizeFuzzCampaignCoverageGaps(append(campaign.CoverageGaps, inferFuzzCampaignCoverageGaps(campaign)...))
	campaign.RunArtifacts = normalizeFuzzCampaignRunArtifacts(campaign.RunArtifacts)
	campaign.ArtifactGraph = buildFuzzCampaignArtifactGraph(campaign)
	campaign.Summary = compactPersistentMemoryText(campaign.Summary, 320)
	return campaign
}

func normalizeFuzzCampaignSeedTargets(items []FuzzCampaignSeedTarget) []FuzzCampaignSeedTarget {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]FuzzCampaignSeedTarget, 0, len(items))
	for _, item := range items {
		item.Name = strings.TrimSpace(item.Name)
		item.File = functionFuzzNormalizeOptionalPath(item.File)
		item.SymbolID = strings.TrimSpace(item.SymbolID)
		item.SourceAnchor = strings.TrimSpace(item.SourceAnchor)
		item.PriorityReasons = uniqueStrings(item.PriorityReasons)
		item.SuggestedCommand = strings.TrimSpace(item.SuggestedCommand)
		item.Provenance = strings.TrimSpace(item.Provenance)
		if item.Name == "" && item.File == "" && item.SymbolID == "" {
			continue
		}
		key := strings.ToLower(item.Name + "|" + item.File + "|" + item.SymbolID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func normalizeFuzzCampaignSeedArtifacts(items []FuzzCampaignSeedArtifact) []FuzzCampaignSeedArtifact {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]FuzzCampaignSeedArtifact, 0, len(items))
	for _, item := range items {
		item.RunID = strings.TrimSpace(item.RunID)
		item.Scenario = strings.TrimSpace(item.Scenario)
		item.Path = functionFuzzNormalizeOptionalPath(item.Path)
		item.Source = strings.TrimSpace(item.Source)
		item.Inputs = uniqueStrings(item.Inputs)
		item.SourceHint = strings.TrimSpace(item.SourceHint)
		if item.RunID == "" || item.Path == "" {
			continue
		}
		key := strings.ToLower(item.RunID + "|" + item.Path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func normalizeFuzzCampaignNativeResults(items []FuzzCampaignNativeResult) []FuzzCampaignNativeResult {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]FuzzCampaignNativeResult, 0, len(items))
	for _, item := range items {
		item.RunID = strings.TrimSpace(item.RunID)
		item.Target = strings.TrimSpace(item.Target)
		item.TargetFile = functionFuzzNormalizeOptionalPath(item.TargetFile)
		item.Status = strings.ToLower(strings.TrimSpace(item.Status))
		item.Outcome = strings.ToLower(strings.TrimSpace(item.Outcome))
		item.CrashFingerprint = strings.TrimSpace(item.CrashFingerprint)
		item.SuspectedInvariant = compactPersistentMemoryText(item.SuspectedInvariant, 220)
		item.MinimizeCommand = compactPersistentMemoryText(item.MinimizeCommand, 220)
		item.ReportPath = functionFuzzNormalizeOptionalPath(item.ReportPath)
		item.BuildLogPath = functionFuzzNormalizeOptionalPath(item.BuildLogPath)
		item.RunLogPath = functionFuzzNormalizeOptionalPath(item.RunLogPath)
		item.CrashDir = functionFuzzNormalizeOptionalPath(item.CrashDir)
		item.ArtifactIDs = uniqueStrings(item.ArtifactIDs)
		item.EvidenceID = strings.TrimSpace(item.EvidenceID)
		if item.CrashCount < 0 {
			item.CrashCount = 0
		}
		if item.RunID == "" {
			continue
		}
		if item.RecordedAt.IsZero() {
			item.RecordedAt = time.Now()
		}
		key := fuzzCampaignNativeResultKey(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func normalizeFuzzCampaignRunArtifacts(items []FuzzCampaignRunArtifact) []FuzzCampaignRunArtifact {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]FuzzCampaignRunArtifact, 0, len(items))
	for _, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.Kind = strings.ToLower(strings.TrimSpace(item.Kind))
		item.RunID = strings.TrimSpace(item.RunID)
		item.Target = strings.TrimSpace(item.Target)
		item.TargetFile = functionFuzzNormalizeOptionalPath(item.TargetFile)
		item.SourceAnchor = strings.TrimSpace(filepath.ToSlash(item.SourceAnchor))
		item.Path = functionFuzzNormalizeOptionalPath(item.Path)
		item.Summary = compactPersistentMemoryText(item.Summary, 220)
		item.Severity = strings.ToLower(strings.TrimSpace(item.Severity))
		item.Signal = strings.TrimSpace(item.Signal)
		item.EvidenceID = strings.TrimSpace(item.EvidenceID)
		if item.Severity == "" {
			item.Severity = "medium"
		}
		if item.Kind == "" {
			continue
		}
		if item.ID == "" {
			item.ID = fuzzCampaignFindingID(item.RunID, item.Kind, firstNonBlankString(item.Path, item.Signal, item.Summary))
		}
		if item.ID == "" {
			continue
		}
		if item.RecordedAt.IsZero() {
			item.RecordedAt = time.Now()
		}
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i int, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func normalizeFuzzCampaignFindings(items []FuzzCampaignFinding) []FuzzCampaignFinding {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	mergedByKey := map[string]int{}
	out := make([]FuzzCampaignFinding, 0, len(items))
	for _, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.DedupKey = strings.TrimSpace(item.DedupKey)
		item.Status = strings.ToLower(strings.TrimSpace(item.Status))
		if item.Status == "" {
			item.Status = "open"
		}
		item.Severity = strings.ToLower(strings.TrimSpace(item.Severity))
		item.Target = strings.TrimSpace(item.Target)
		item.TargetFile = functionFuzzNormalizeOptionalPath(item.TargetFile)
		item.SourceAnchor = strings.TrimSpace(filepath.ToSlash(item.SourceAnchor))
		item.FuzzRunID = strings.TrimSpace(item.FuzzRunID)
		item.SeedArtifacts = uniqueStrings(normalizeFuzzCampaignFindingPaths(item.SeedArtifacts))
		item.NativeResultKey = strings.TrimSpace(item.NativeResultKey)
		item.NativeResultKeys = uniqueStrings(item.NativeResultKeys)
		if item.NativeResultKey != "" {
			item.NativeResultKeys = uniqueStrings(append([]string{item.NativeResultKey}, item.NativeResultKeys...))
		}
		item.EvidenceID = strings.TrimSpace(item.EvidenceID)
		item.EvidenceIDs = uniqueStrings(item.EvidenceIDs)
		if item.EvidenceID != "" {
			item.EvidenceIDs = uniqueStrings(append([]string{item.EvidenceID}, item.EvidenceIDs...))
		}
		item.VerificationGate = strings.ToLower(strings.TrimSpace(item.VerificationGate))
		item.TrackedFeatureGate = strings.ToLower(strings.TrimSpace(item.TrackedFeatureGate))
		item.CrashFingerprint = strings.TrimSpace(item.CrashFingerprint)
		item.SuspectedInvariant = compactPersistentMemoryText(item.SuspectedInvariant, 220)
		item.ReportPath = functionFuzzNormalizeOptionalPath(item.ReportPath)
		item.MergedFindingIDs = uniqueStrings(item.MergedFindingIDs)
		if item.DuplicateCount < 0 {
			item.DuplicateCount = 0
		}
		if item.DedupKey == "" {
			item.DedupKey = fuzzCampaignFindingDedupKey(item)
		}
		if item.ID == "" {
			item.ID = fuzzCampaignFindingID(firstNonBlankString(item.DedupKey, item.FuzzRunID), firstNonBlankString(item.CrashFingerprint, item.SourceAnchor, item.Target))
		}
		if item.ID == "" {
			continue
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = time.Now()
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.CreatedAt
		}
		key := strings.ToLower(firstNonBlankString(item.DedupKey, item.ID))
		if existingIndex, ok := mergedByKey[key]; ok {
			out[existingIndex] = mergeFuzzCampaignFinding(out[existingIndex], item)
			continue
		}
		idKey := strings.ToLower(item.ID)
		if _, ok := seen[idKey]; ok {
			continue
		}
		seen[idKey] = struct{}{}
		mergedByKey[key] = len(out)
		out = append(out, item)
	}
	sort.Slice(out, func(i int, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status < out[j].Status
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func normalizeFuzzCampaignFindingPaths(items []string) []string {
	out := []string{}
	for _, item := range items {
		item = functionFuzzNormalizeOptionalPath(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func fuzzCampaignFindingDedupKey(item FuzzCampaignFinding) string {
	source := strings.ToLower(strings.TrimSpace(filepath.ToSlash(firstNonBlankString(item.SourceAnchor, item.TargetFile))))
	fingerprint := strings.ToLower(strings.TrimSpace(item.CrashFingerprint))
	invariant := strings.ToLower(strings.TrimSpace(item.SuspectedInvariant))
	switch {
	case fingerprint != "" && source != "":
		return "crash|" + fingerprint + "|" + source
	case fingerprint != "":
		return "crash|" + fingerprint
	case invariant != "" && source != "":
		return fmt.Sprintf("invariant|%08x|%s", stableHash32(invariant), source)
	default:
		return ""
	}
}

func mergeFuzzCampaignFinding(existing FuzzCampaignFinding, incoming FuzzCampaignFinding) FuzzCampaignFinding {
	out := existing
	out.ID = firstNonBlankString(existing.ID, incoming.ID)
	out.DedupKey = firstNonBlankString(existing.DedupKey, incoming.DedupKey)
	out.Status = fuzzCampaignFindingMergedStatus(existing.Status, incoming.Status)
	out.Severity = fuzzCampaignFindingMergedSeverity(existing.Severity, incoming.Severity)
	out.Target = firstNonBlankString(existing.Target, incoming.Target)
	out.TargetFile = firstNonBlankString(existing.TargetFile, incoming.TargetFile)
	out.SourceAnchor = firstNonBlankString(existing.SourceAnchor, incoming.SourceAnchor)
	out.FuzzRunID = firstNonBlankString(existing.FuzzRunID, incoming.FuzzRunID)
	out.SeedArtifacts = uniqueStrings(append(existing.SeedArtifacts, incoming.SeedArtifacts...))
	out.NativeResultKey = firstNonBlankString(incoming.NativeResultKey, existing.NativeResultKey)
	out.NativeResultKeys = uniqueStrings(append(existing.NativeResultKeys, incoming.NativeResultKeys...))
	if incoming.NativeResultKey != "" {
		out.NativeResultKeys = uniqueStrings(append(out.NativeResultKeys, incoming.NativeResultKey))
	}
	out.EvidenceID = firstNonBlankString(incoming.EvidenceID, existing.EvidenceID)
	out.EvidenceIDs = uniqueStrings(append(existing.EvidenceIDs, incoming.EvidenceIDs...))
	if incoming.EvidenceID != "" {
		out.EvidenceIDs = uniqueStrings(append(out.EvidenceIDs, incoming.EvidenceID))
	}
	out.VerificationGate = fuzzCampaignFindingMergedGate(existing.VerificationGate, incoming.VerificationGate, []string{"required", "pending_native", "monitor"})
	out.TrackedFeatureGate = fuzzCampaignFindingMergedGate(existing.TrackedFeatureGate, incoming.TrackedFeatureGate, []string{"block_close", "monitor"})
	out.CrashFingerprint = firstNonBlankString(existing.CrashFingerprint, incoming.CrashFingerprint)
	out.SuspectedInvariant = firstNonBlankString(existing.SuspectedInvariant, incoming.SuspectedInvariant)
	out.ReportPath = firstNonBlankString(incoming.ReportPath, existing.ReportPath)
	out.MergedFindingIDs = uniqueStrings(append(append(existing.MergedFindingIDs, incoming.MergedFindingIDs...), incoming.ID))
	out.DuplicateCount = existing.DuplicateCount + 1
	if incoming.DuplicateCount > 0 {
		out.DuplicateCount += incoming.DuplicateCount
	}
	if existing.CreatedAt.IsZero() || (!incoming.CreatedAt.IsZero() && incoming.CreatedAt.Before(existing.CreatedAt)) {
		out.CreatedAt = incoming.CreatedAt
	}
	if incoming.UpdatedAt.After(existing.UpdatedAt) {
		out.UpdatedAt = incoming.UpdatedAt
	}
	if out.UpdatedAt.IsZero() {
		out.UpdatedAt = time.Now()
	}
	return out
}

func fuzzCampaignFindingMergedSeverity(left string, right string) string {
	order := map[string]int{"critical": 4, "high": 3, "medium": 2, "low": 1}
	if order[strings.ToLower(right)] > order[strings.ToLower(left)] {
		return strings.ToLower(right)
	}
	return strings.ToLower(firstNonBlankString(left, right))
}

func fuzzCampaignFindingMergedStatus(left string, right string) string {
	return fuzzCampaignFindingMergedGate(left, right, []string{"open", "seeded", "monitoring", "closed"})
}

func fuzzCampaignFindingMergedGate(left string, right string, priority []string) string {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	rank := map[string]int{}
	for i, item := range priority {
		rank[item] = len(priority) - i
	}
	if rank[right] > rank[left] {
		return right
	}
	return left
}

func normalizeFuzzCampaignCoverageGaps(items []FuzzCampaignCoverageGap) []FuzzCampaignCoverageGap {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]FuzzCampaignCoverageGap, 0, len(items))
	for _, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.Target = strings.TrimSpace(item.Target)
		item.TargetFile = functionFuzzNormalizeOptionalPath(item.TargetFile)
		item.SourceAnchor = strings.TrimSpace(filepath.ToSlash(item.SourceAnchor))
		item.Reason = compactPersistentMemoryText(item.Reason, 180)
		item.CampaignID = strings.TrimSpace(item.CampaignID)
		item.RunID = strings.TrimSpace(item.RunID)
		item.EvidenceID = strings.TrimSpace(item.EvidenceID)
		item.ReportPath = functionFuzzNormalizeOptionalPath(item.ReportPath)
		if item.PriorityBoost <= 0 {
			item.PriorityBoost = 10
		}
		if item.PriorityBoost > 30 {
			item.PriorityBoost = 30
		}
		if item.ID == "" {
			item.ID = fuzzCampaignFindingID(item.CampaignID, item.RunID, item.SourceAnchor, item.TargetFile, item.Target)
		}
		if item.ID == "" {
			continue
		}
		if item.LastSeenAt.IsZero() {
			item.LastSeenAt = time.Now()
		}
		key := strings.ToLower(item.ID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i int, j int) bool {
		if out[i].PriorityBoost != out[j].PriorityBoost {
			return out[i].PriorityBoost > out[j].PriorityBoost
		}
		left := out[i].SourceAnchor + "|" + out[i].TargetFile + "|" + out[i].Target
		right := out[j].SourceAnchor + "|" + out[j].TargetFile + "|" + out[j].Target
		return left < right
	})
	return out
}

func normalizeFuzzCampaignCoverageReports(items []FuzzCampaignCoverageReport) []FuzzCampaignCoverageReport {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]FuzzCampaignCoverageReport, 0, len(items))
	for _, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.Format = strings.ToLower(strings.TrimSpace(item.Format))
		item.Target = strings.TrimSpace(item.Target)
		item.TargetFile = functionFuzzNormalizeOptionalPath(item.TargetFile)
		item.SourceAnchor = strings.TrimSpace(filepath.ToSlash(item.SourceAnchor))
		item.RunID = strings.TrimSpace(item.RunID)
		item.Path = functionFuzzNormalizeOptionalPath(item.Path)
		item.Summary = compactPersistentMemoryText(item.Summary, 220)
		item.GapReason = compactPersistentMemoryText(item.GapReason, 180)
		if item.LinesCovered < 0 {
			item.LinesCovered = 0
		}
		if item.LinesTotal < 0 {
			item.LinesTotal = 0
		}
		if item.LinesTotal > 0 && item.CoveragePercent <= 0 {
			item.CoveragePercent = float64(item.LinesCovered) * 100.0 / float64(item.LinesTotal)
		}
		if item.CoveragePercent < 0 {
			item.CoveragePercent = 0
		}
		if item.CoveragePercent > 100 {
			item.CoveragePercent = 100
		}
		if item.PriorityBoost <= 0 && item.Gap {
			item.PriorityBoost = 12
		}
		if item.PriorityBoost > 30 {
			item.PriorityBoost = 30
		}
		if item.ID == "" {
			item.ID = fuzzCampaignFindingID(item.RunID, item.Format, firstNonBlankString(item.Path, item.SourceAnchor, item.TargetFile, item.Target))
		}
		if item.ID == "" {
			continue
		}
		if item.RecordedAt.IsZero() {
			item.RecordedAt = time.Now()
		}
		key := strings.ToLower(item.ID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i int, j int) bool {
		if !out[i].RecordedAt.Equal(out[j].RecordedAt) {
			return out[i].RecordedAt.After(out[j].RecordedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func inferFuzzCampaignCoverageGaps(campaign FuzzCampaign) []FuzzCampaignCoverageGap {
	if len(campaign.SeedTargets) == 0 {
		return nil
	}
	covered := map[string]struct{}{}
	for _, result := range campaign.NativeResults {
		if strings.EqualFold(strings.TrimSpace(result.Outcome), "passed") || strings.EqualFold(strings.TrimSpace(result.Status), "completed") {
			for _, key := range fuzzCampaignCoverageTargetKeys(result.Target, result.TargetFile, "", result.RunID) {
				covered[key] = struct{}{}
			}
		}
	}
	for _, report := range campaign.CoverageReports {
		if !report.Gap {
			for _, key := range fuzzCampaignCoverageTargetKeys(report.Target, report.TargetFile, report.SourceAnchor, report.RunID) {
				covered[key] = struct{}{}
			}
		}
	}
	out := []FuzzCampaignCoverageGap{}
	for _, report := range campaign.CoverageReports {
		if !report.Gap {
			continue
		}
		out = append(out, FuzzCampaignCoverageGap{
			ID:            fuzzCampaignFindingID(campaign.ID, report.ID, "coverage-gap"),
			Target:        report.Target,
			TargetFile:    report.TargetFile,
			SourceAnchor:  report.SourceAnchor,
			Reason:        firstNonBlankString(report.GapReason, report.Summary, "coverage report indicates a target coverage gap"),
			PriorityBoost: report.PriorityBoost,
			CampaignID:    campaign.ID,
			RunID:         report.RunID,
			ReportPath:    report.Path,
			LastSeenAt:    report.RecordedAt,
		})
	}
	for _, target := range campaign.SeedTargets {
		keys := fuzzCampaignCoverageTargetKeys(target.Name, target.File, target.SourceAnchor, target.SymbolID)
		hasCoverage := false
		for _, key := range keys {
			if _, ok := covered[key]; ok {
				hasCoverage = true
				break
			}
		}
		if hasCoverage {
			continue
		}
		out = append(out, FuzzCampaignCoverageGap{
			ID:            fuzzCampaignFindingID(campaign.ID, target.SourceAnchor, target.File, target.Name),
			Target:        target.Name,
			TargetFile:    target.File,
			SourceAnchor:  target.SourceAnchor,
			Reason:        "seed target has no completed native coverage result yet",
			PriorityBoost: 14,
			CampaignID:    campaign.ID,
			LastSeenAt:    time.Now(),
		})
	}
	return out
}

func fuzzCampaignCoverageTargetKeys(target string, file string, sourceAnchor string, symbolID string) []string {
	candidates := []string{target, file, sourceAnchor, symbolID}
	out := []string{}
	for _, candidate := range candidates {
		candidate = strings.ToLower(strings.TrimSpace(filepath.ToSlash(candidate)))
		if candidate == "" {
			continue
		}
		out = append(out, candidate)
		if strings.Contains(candidate, ":") {
			out = append(out, strings.Split(candidate, ":")[0])
		}
	}
	return uniqueStrings(out)
}

func fuzzCampaignNativeResultKey(item FuzzCampaignNativeResult) string {
	return strings.ToLower(strings.Join([]string{
		strings.TrimSpace(item.RunID),
		strings.TrimSpace(item.Status),
		fmt.Sprintf("%d", item.CrashCount),
		strings.TrimSpace(item.CrashFingerprint),
	}, "|"))
}

func fuzzCampaignFindingIDForNativeResult(campaign FuzzCampaign, run FunctionFuzzRun, result FuzzCampaignNativeResult) string {
	probe := FuzzCampaignFinding{
		CrashFingerprint:   result.CrashFingerprint,
		SourceAnchor:       fuzzCampaignSourceAnchorForRun(run),
		TargetFile:         firstNonBlankString(result.TargetFile, run.TargetFile),
		Target:             firstNonBlankString(result.Target, run.TargetSymbolName, run.TargetQuery),
		SuspectedInvariant: result.SuspectedInvariant,
	}
	if dedupKey := fuzzCampaignFindingDedupKey(probe); dedupKey != "" {
		return fuzzCampaignFindingID(campaign.ID, dedupKey)
	}
	return fuzzCampaignFindingID(campaign.ID, run.ID, firstNonBlankString(result.CrashFingerprint, result.Outcome, result.Status))
}

func fuzzCampaignFindingID(parts ...string) string {
	cleaned := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	return fmt.Sprintf("finding-%08x", stableHash32(strings.ToLower(strings.Join(cleaned, "|"))))
}

func buildFuzzCampaignArtifactGraph(campaign FuzzCampaign) FuzzCampaignArtifactGraph {
	nodes := []FuzzCampaignGraphNode{}
	edges := []FuzzCampaignGraphEdge{}
	addNode := func(node FuzzCampaignGraphNode) {
		node.ID = strings.TrimSpace(node.ID)
		node.Kind = strings.TrimSpace(node.Kind)
		if node.ID == "" || node.Kind == "" {
			return
		}
		node.Path = functionFuzzNormalizeOptionalPath(node.Path)
		for _, existing := range nodes {
			if strings.EqualFold(existing.ID, node.ID) {
				return
			}
		}
		nodes = append(nodes, node)
	}
	addEdge := func(source string, target string, kind string) {
		source = strings.TrimSpace(source)
		target = strings.TrimSpace(target)
		kind = strings.TrimSpace(kind)
		if source == "" || target == "" || kind == "" {
			return
		}
		for _, existing := range edges {
			if strings.EqualFold(existing.Source, source) && strings.EqualFold(existing.Target, target) && strings.EqualFold(existing.Kind, kind) {
				return
			}
		}
		edges = append(edges, FuzzCampaignGraphEdge{Source: source, Target: target, Kind: kind})
	}
	addNode(FuzzCampaignGraphNode{ID: "campaign:" + campaign.ID, Kind: "campaign", Label: campaign.Name, Path: campaign.ManifestPath})
	for _, target := range campaign.SeedTargets {
		nodeID := "target:" + fuzzCampaignSafePathPart(firstNonBlankString(target.SymbolID, target.Name, target.File))
		addNode(FuzzCampaignGraphNode{ID: nodeID, Kind: "target", Label: firstNonBlankString(target.Name, target.SymbolID), Path: target.File})
		addEdge("campaign:"+campaign.ID, nodeID, "seeds_target")
	}
	for _, gap := range campaign.CoverageGaps {
		nodeID := "coverage_gap:" + gap.ID
		addNode(FuzzCampaignGraphNode{ID: nodeID, Kind: "coverage_gap", Label: firstNonBlankString(gap.Target, gap.SourceAnchor), Path: firstNonBlankString(gap.TargetFile, gap.SourceAnchor), EvidenceID: gap.EvidenceID})
		addEdge("campaign:"+campaign.ID, nodeID, "has_coverage_gap")
		if strings.TrimSpace(gap.SourceAnchor) != "" {
			targetID := "target:" + fuzzCampaignSafePathPart(firstNonBlankString(gap.SourceAnchor, gap.TargetFile, gap.Target))
			addNode(FuzzCampaignGraphNode{ID: targetID, Kind: "target", Label: firstNonBlankString(gap.Target, gap.SourceAnchor), Path: firstNonBlankString(gap.TargetFile, gap.SourceAnchor)})
			addEdge(nodeID, targetID, "prioritizes_target")
		}
	}
	for _, report := range campaign.CoverageReports {
		nodeID := "coverage_report:" + report.ID
		addNode(FuzzCampaignGraphNode{ID: nodeID, Kind: "coverage_report", Label: firstNonBlankString(report.Format, report.Summary), Path: report.Path})
		addEdge("campaign:"+campaign.ID, nodeID, "has_coverage_report")
		if strings.TrimSpace(report.RunID) != "" {
			runID := "native:" + fuzzCampaignSafePathPart(report.RunID)
			addEdge(runID, nodeID, "produced_coverage")
		}
	}
	for _, artifact := range campaign.RunArtifacts {
		nodeID := "run_artifact:" + artifact.ID
		addNode(FuzzCampaignGraphNode{ID: nodeID, Kind: "run_artifact", Label: firstNonBlankString(artifact.Kind, artifact.Signal, artifact.Summary), Path: artifact.Path, EvidenceID: artifact.EvidenceID})
		addEdge("campaign:"+campaign.ID, nodeID, "has_run_artifact")
		if strings.TrimSpace(artifact.RunID) != "" {
			runID := "run:" + artifact.RunID
			addNode(FuzzCampaignGraphNode{ID: runID, Kind: "fuzz_run", Label: artifact.RunID})
			addEdge(runID, nodeID, "produces_run_artifact")
		}
		if strings.TrimSpace(artifact.EvidenceID) != "" {
			evidenceID := "evidence:" + artifact.EvidenceID
			addNode(FuzzCampaignGraphNode{ID: evidenceID, Kind: "evidence", Label: artifact.EvidenceID, EvidenceID: artifact.EvidenceID})
			addEdge(nodeID, evidenceID, "recorded_as")
		}
	}
	for _, artifact := range campaign.SeedArtifacts {
		nodeID := "seed:" + fuzzCampaignSafePathPart(artifact.RunID+"-"+filepath.Base(artifact.Path))
		addNode(FuzzCampaignGraphNode{ID: nodeID, Kind: "seed_artifact", Label: artifact.Scenario, Path: artifact.Path})
		addEdge("campaign:"+campaign.ID, nodeID, "promotes_seed")
		if strings.TrimSpace(artifact.RunID) != "" {
			runID := "run:" + artifact.RunID
			addNode(FuzzCampaignGraphNode{ID: runID, Kind: "fuzz_run", Label: artifact.RunID})
			addEdge(runID, nodeID, "produces_seed")
		}
	}
	for _, result := range campaign.NativeResults {
		nodeID := "native:" + fuzzCampaignSafePathPart(result.RunID+"-"+firstNonBlankString(result.CrashFingerprint, result.Status, result.Outcome))
		addNode(FuzzCampaignGraphNode{ID: nodeID, Kind: "native_result", Label: firstNonBlankString(result.Target, result.RunID), Path: result.ReportPath, EvidenceID: result.EvidenceID})
		addEdge("campaign:"+campaign.ID, nodeID, "captures_native_result")
		if strings.TrimSpace(result.RunID) != "" {
			runID := "run:" + result.RunID
			addNode(FuzzCampaignGraphNode{ID: runID, Kind: "fuzz_run", Label: result.RunID})
			addEdge(runID, nodeID, "produces_native_result")
		}
	}
	for _, finding := range campaign.Findings {
		nodeID := "finding:" + finding.ID
		addNode(FuzzCampaignGraphNode{ID: nodeID, Kind: "finding", Label: firstNonBlankString(finding.Target, finding.ID), Path: finding.ReportPath, EvidenceID: finding.EvidenceID})
		addEdge("campaign:"+campaign.ID, nodeID, "tracks_finding")
		if strings.TrimSpace(finding.FuzzRunID) != "" {
			runID := "run:" + finding.FuzzRunID
			addNode(FuzzCampaignGraphNode{ID: runID, Kind: "fuzz_run", Label: finding.FuzzRunID})
			addEdge(runID, nodeID, "raises_finding")
		}
		for _, seed := range finding.SeedArtifacts {
			seedID := "seed:" + fuzzCampaignSafePathPart(finding.FuzzRunID+"-"+filepath.Base(seed))
			addEdge(seedID, nodeID, "evidences_finding")
		}
		if strings.TrimSpace(finding.EvidenceID) != "" {
			evidenceID := "evidence:" + finding.EvidenceID
			addNode(FuzzCampaignGraphNode{ID: evidenceID, Kind: "evidence", Label: finding.EvidenceID, EvidenceID: finding.EvidenceID})
			addEdge(nodeID, evidenceID, "recorded_as")
		}
	}
	if len(nodes) == 0 && len(edges) == 0 {
		return FuzzCampaignArtifactGraph{}
	}
	sort.Slice(nodes, func(i int, j int) bool {
		return nodes[i].ID < nodes[j].ID
	})
	sort.Slice(edges, func(i int, j int) bool {
		left := edges[i].Source + "|" + edges[i].Target + "|" + edges[i].Kind
		right := edges[j].Source + "|" + edges[j].Target + "|" + edges[j].Kind
		return left < right
	})
	return FuzzCampaignArtifactGraph{
		Schema: "kernforge.fuzz_campaign.artifact_graph.v1",
		Nodes:  nodes,
		Edges:  edges,
	}
}

func createFuzzCampaignFromWorkspace(root string, name string, manifest AnalysisDocsManifest) (FuzzCampaign, error) {
	root = normalizePersistentMemoryWorkspace(root)
	if strings.TrimSpace(root) == "" {
		return FuzzCampaign{}, fmt.Errorf("workspace root is not available")
	}
	now := time.Now()
	id := fmt.Sprintf("campaign-%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/1_000_000)
	campaignDir := filepath.Join(root, ".kernforge", "fuzz", id)
	campaign := FuzzCampaign{
		ID:           id,
		Workspace:    root,
		Name:         firstNonBlankString(strings.TrimSpace(name), "Fuzz campaign"),
		Status:       "planned",
		CreatedAt:    now,
		UpdatedAt:    now,
		ArtifactDir:  campaignDir,
		ManifestPath: filepath.Join(campaignDir, "manifest.json"),
		CorpusDir:    filepath.Join(campaignDir, "corpus"),
		CrashDir:     filepath.Join(campaignDir, "crashes"),
		CoverageDir:  filepath.Join(campaignDir, "coverage"),
		ReportsDir:   filepath.Join(campaignDir, "reports"),
		LogsDir:      filepath.Join(campaignDir, "logs"),
	}
	campaign.SeedTargets = fuzzCampaignSeedTargetsFromAnalysisDocs(manifest, 12)
	campaign.Summary = buildFuzzCampaignSummary(campaign)
	if err := writeFuzzCampaignManifest(campaign); err != nil {
		return FuzzCampaign{}, err
	}
	return normalizeFuzzCampaign(campaign), nil
}

func writeFuzzCampaignManifest(campaign FuzzCampaign) error {
	dirs := []string{
		campaign.ArtifactDir,
		campaign.CorpusDir,
		campaign.CrashDir,
		campaign.CoverageDir,
		campaign.ReportsDir,
		campaign.LogsDir,
	}
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(normalizeFuzzCampaign(campaign), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(campaign.ManifestPath, append(data, '\n'), 0o644)
}

func fuzzCampaignSeedTargetsFromAnalysisDocs(manifest AnalysisDocsManifest, limit int) []FuzzCampaignSeedTarget {
	if limit <= 0 {
		limit = 12
	}
	var out []FuzzCampaignSeedTarget
	for _, item := range manifest.FuzzTargets {
		target := FuzzCampaignSeedTarget{
			Name:             item.Name,
			File:             item.File,
			SymbolID:         item.SymbolID,
			SourceAnchor:     item.SourceAnchor,
			PriorityScore:    item.PriorityScore,
			PriorityReasons:  item.PriorityReasons,
			SuggestedCommand: item.SuggestedCommand,
			Provenance:       "analysis_docs:" + manifest.RunID,
		}
		out = append(out, target)
		if len(out) >= limit {
			break
		}
	}
	return normalizeFuzzCampaignSeedTargets(out)
}

func buildFuzzCampaignSummary(campaign FuzzCampaign) string {
	return fmt.Sprintf("Fuzz campaign %s tracks %d seed target(s), %d attached fuzz run(s), %d promoted seed artifact(s), %d native result(s), %d finding(s), %d run artifact(s), %d coverage report(s), and %d coverage gap(s) under %s.",
		campaign.ID,
		len(campaign.SeedTargets),
		len(campaign.FunctionRuns),
		len(campaign.SeedArtifacts),
		len(campaign.NativeResults),
		len(campaign.Findings),
		len(campaign.RunArtifacts),
		len(campaign.CoverageReports),
		len(campaign.CoverageGaps),
		filepath.ToSlash(campaign.ArtifactDir))
}

func attachFunctionFuzzRunToCampaign(campaign FuzzCampaign, run FunctionFuzzRun) FuzzCampaign {
	campaign = normalizeFuzzCampaign(campaign)
	run = normalizeFunctionFuzzRun(run)
	if strings.TrimSpace(run.ID) != "" {
		campaign.FunctionRuns = uniqueStrings(append(campaign.FunctionRuns, run.ID))
	}
	target := FuzzCampaignSeedTarget{
		Name:             firstNonBlankString(run.TargetSymbolName, run.TargetQuery),
		File:             run.TargetFile,
		SymbolID:         run.TargetSymbolID,
		SourceAnchor:     fuzzCampaignSourceAnchorForRun(run),
		PriorityScore:    run.RiskScore,
		PriorityReasons:  []string{functionFuzzSourceOnlySynthesisSummary(run)},
		SuggestedCommand: firstNonBlankString(firstFuzzCampaignString(run.SuggestedCommands), "/fuzz-func "+strings.TrimSpace(run.TargetQuery)),
		Provenance:       "fuzz_func:" + run.ID,
	}
	campaign.SeedTargets = normalizeFuzzCampaignSeedTargets(append(campaign.SeedTargets, target))
	campaign.Status = "active"
	campaign.UpdatedAt = time.Now()
	campaign.Summary = buildFuzzCampaignSummary(campaign)
	return normalizeFuzzCampaign(campaign)
}

func promoteFunctionFuzzRunSeeds(campaign FuzzCampaign, runs []FunctionFuzzRun, limitPerRun int) (FuzzCampaign, []FuzzCampaignSeedArtifact, error) {
	campaign = normalizeFuzzCampaign(campaign)
	if strings.TrimSpace(campaign.CorpusDir) == "" {
		return FuzzCampaign{}, nil, fmt.Errorf("campaign corpus directory is not configured")
	}
	if limitPerRun <= 0 {
		limitPerRun = 16
	}
	var promoted []FuzzCampaignSeedArtifact
	for _, run := range runs {
		run = normalizeFunctionFuzzRun(run)
		if strings.TrimSpace(run.ID) == "" || len(run.VirtualScenarios) == 0 {
			continue
		}
		scenarios := functionFuzzSortedVirtualScenarios(run.VirtualScenarios)
		if len(scenarios) > limitPerRun {
			scenarios = scenarios[:limitPerRun]
		}
		runDir := filepath.Join(campaign.CorpusDir, fuzzCampaignSafePathPart(run.ID))
		if err := os.MkdirAll(runDir, 0o755); err != nil {
			return FuzzCampaign{}, nil, err
		}
		for index, scenario := range scenarios {
			artifact, err := writeFuzzCampaignScenarioSeed(runDir, run, scenario, index+1)
			if err != nil {
				return FuzzCampaign{}, nil, err
			}
			promoted = append(promoted, artifact)
			campaign.Findings = upsertFuzzCampaignFinding(campaign.Findings, buildFuzzCampaignSeedFinding(campaign, run, artifact))
		}
		campaign = attachFunctionFuzzRunToCampaign(campaign, run)
	}
	campaign.SeedArtifacts = normalizeFuzzCampaignSeedArtifacts(append(campaign.SeedArtifacts, promoted...))
	if len(promoted) > 0 {
		campaign.Status = "active"
	}
	campaign.UpdatedAt = time.Now()
	campaign.Summary = buildFuzzCampaignSummary(campaign)
	if err := writeFuzzCampaignManifest(campaign); err != nil {
		return FuzzCampaign{}, nil, err
	}
	return normalizeFuzzCampaign(campaign), promoted, nil
}

func (rt *runtimeState) captureFuzzCampaignNativeResults(campaign FuzzCampaign, runs []FunctionFuzzRun) (FuzzCampaign, []FuzzCampaignNativeResult, error) {
	campaign = normalizeFuzzCampaign(campaign)
	if len(runs) == 0 {
		return campaign, nil, nil
	}
	if strings.TrimSpace(campaign.ReportsDir) == "" {
		return campaign, nil, fmt.Errorf("campaign reports directory is not configured")
	}
	if err := os.MkdirAll(campaign.ReportsDir, 0o755); err != nil {
		return campaign, nil, err
	}
	existing := map[string]struct{}{}
	for _, result := range campaign.NativeResults {
		existing[fuzzCampaignNativeResultKey(result)] = struct{}{}
	}
	var captured []FuzzCampaignNativeResult
	for _, run := range runs {
		run = normalizeFunctionFuzzRun(run)
		result, ok := buildFuzzCampaignNativeResult(campaign, run)
		if !ok {
			continue
		}
		if _, seen := existing[fuzzCampaignNativeResultKey(result)]; seen {
			continue
		}
		runArtifacts := collectFuzzCampaignRunArtifacts(campaign, run, result)
		if fuzzCampaignRunArtifactsNeedVerification(runArtifacts) && strings.EqualFold(result.Outcome, "passed") {
			result.Outcome = "failed"
			result.SuspectedInvariant = firstNonBlankString(result.SuspectedInvariant, fuzzCampaignRunArtifactsSummary(runArtifacts))
		}
		for i := range runArtifacts {
			runArtifacts[i].EvidenceID = result.EvidenceID
		}
		result.ArtifactIDs = fuzzCampaignRunArtifactIDs(runArtifacts)
		reportPath, err := writeFuzzCampaignNativeResultReport(campaign, run, result)
		if err != nil {
			return campaign, nil, err
		}
		result.ReportPath = reportPath
		if rt != nil && rt.evidence != nil {
			sessionID := ""
			if rt.session != nil {
				sessionID = rt.session.ID
			}
			record := buildFuzzCampaignNativeEvidenceRecord(campaign, run, result, workspaceSnapshotRoot(rt.workspace), sessionID)
			if err := rt.evidence.Append(record); err != nil {
				return campaign, nil, err
			}
			result.EvidenceID = record.ID
			for i := range runArtifacts {
				runArtifacts[i].EvidenceID = record.ID
			}
		}
		campaign.RunArtifacts = normalizeFuzzCampaignRunArtifacts(append(campaign.RunArtifacts, runArtifacts...))
		campaign.Findings = upsertFuzzCampaignFinding(campaign.Findings, buildFuzzCampaignNativeFinding(campaign, run, result))
		campaign.CoverageReports = normalizeFuzzCampaignCoverageReports(append(campaign.CoverageReports, collectFuzzCampaignCoverageReports(campaign, run, result)...))
		captured = append(captured, result)
		existing[fuzzCampaignNativeResultKey(result)] = struct{}{}
	}
	if len(captured) == 0 {
		return campaign, nil, nil
	}
	campaign.NativeResults = normalizeFuzzCampaignNativeResults(append(campaign.NativeResults, captured...))
	campaign.Status = "active"
	campaign.UpdatedAt = time.Now()
	campaign.Summary = buildFuzzCampaignSummary(campaign)
	if err := writeFuzzCampaignManifest(campaign); err != nil {
		return FuzzCampaign{}, nil, err
	}
	return normalizeFuzzCampaign(campaign), captured, nil
}

func buildFuzzCampaignNativeResult(campaign FuzzCampaign, run FunctionFuzzRun) (FuzzCampaignNativeResult, bool) {
	if strings.TrimSpace(run.ID) == "" {
		return FuzzCampaignNativeResult{}, false
	}
	status := strings.ToLower(strings.TrimSpace(run.Execution.Status))
	crashCount := run.Execution.CrashCount
	if strings.TrimSpace(run.Execution.CrashDir) != "" {
		crashCount = functionFuzzCountCrashArtifacts(run.Execution.CrashDir)
	}
	if !fuzzCampaignNativeStatusIsRecordable(status) && crashCount == 0 {
		return FuzzCampaignNativeResult{}, false
	}
	outcome := "running"
	switch {
	case crashCount > 0:
		outcome = "failed"
	case status == "completed":
		outcome = "passed"
	case status == "failed" || status == "canceled" || status == "preempted" || status == "blocked":
		outcome = "failed"
	}
	result := FuzzCampaignNativeResult{
		RunID:              run.ID,
		Target:             firstNonBlankString(run.TargetSymbolName, run.TargetQuery),
		TargetFile:         run.TargetFile,
		Status:             status,
		Outcome:            outcome,
		CrashCount:         crashCount,
		CrashFingerprint:   fuzzCampaignCrashFingerprint(run),
		SuspectedInvariant: fuzzCampaignSuspectedInvariant(run),
		MinimizeCommand:    fuzzCampaignMinimizeCommand(campaign, run),
		BuildLogPath:       run.Execution.BuildLogPath,
		RunLogPath:         run.Execution.RunLogPath,
		CrashDir:           run.Execution.CrashDir,
		RecordedAt:         time.Now(),
	}
	normalized := normalizeFuzzCampaignNativeResults([]FuzzCampaignNativeResult{result})
	if len(normalized) == 0 {
		return FuzzCampaignNativeResult{}, false
	}
	return normalized[0], true
}

func collectFuzzCampaignCoverageReports(campaign FuzzCampaign, run FunctionFuzzRun, result FuzzCampaignNativeResult) []FuzzCampaignCoverageReport {
	candidates := []string{run.Execution.RunLogPath, run.Execution.BuildLogPath}
	if strings.TrimSpace(campaign.CoverageDir) != "" {
		entries, err := os.ReadDir(campaign.CoverageDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				name := strings.ToLower(entry.Name())
				if !containsAny(name, ".txt", ".log", ".json", ".info", ".lcov", ".summary", ".md") {
					continue
				}
				if strings.TrimSpace(run.ID) != "" && !strings.Contains(name, strings.ToLower(fuzzCampaignSafePathPart(run.ID))) && !strings.Contains(name, "coverage") && !strings.Contains(name, "cov") {
					continue
				}
				candidates = append(candidates, filepath.Join(campaign.CoverageDir, entry.Name()))
			}
		}
	}
	out := []FuzzCampaignCoverageReport{}
	for _, path := range uniqueStrings(normalizeFuzzCampaignFindingPaths(candidates)) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if report, ok := parseFuzzCampaignCoverageReport(path, string(data), run, result); ok {
			out = append(out, report)
		}
	}
	if strings.TrimSpace(run.Execution.LastOutput) != "" {
		if report, ok := parseFuzzCampaignCoverageReport("last_output:"+run.ID, run.Execution.LastOutput, run, result); ok {
			out = append(out, report)
		}
	}
	return normalizeFuzzCampaignCoverageReports(out)
}

func collectFuzzCampaignRunArtifacts(campaign FuzzCampaign, run FunctionFuzzRun, result FuzzCampaignNativeResult) []FuzzCampaignRunArtifact {
	base := FuzzCampaignRunArtifact{
		RunID:        run.ID,
		Target:       firstNonBlankString(result.Target, run.TargetSymbolName, run.TargetQuery),
		TargetFile:   firstNonBlankString(result.TargetFile, run.TargetFile),
		SourceAnchor: fuzzCampaignSourceAnchorForRun(run),
		RecordedAt:   time.Now(),
	}
	out := []FuzzCampaignRunArtifact{}
	textCandidates := []string{run.Execution.RunLogPath, run.Execution.BuildLogPath}
	for _, path := range uniqueStrings(normalizeFuzzCampaignFindingPaths(textCandidates)) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		out = append(out, parseFuzzCampaignRunArtifactsFromText(path, string(data), base)...)
	}
	if strings.TrimSpace(run.Execution.LastOutput) != "" {
		out = append(out, parseFuzzCampaignRunArtifactsFromText("last_output:"+run.ID, run.Execution.LastOutput, base)...)
	}
	crashDirs := uniqueStrings(normalizeFuzzCampaignFindingPaths([]string{run.Execution.CrashDir, campaign.CrashDir}))
	for _, dir := range crashDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			artifact := parseFuzzCampaignRunArtifactFromCrashFile(path, base)
			if artifact.Kind != "" {
				out = append(out, artifact)
			}
		}
	}
	return normalizeFuzzCampaignRunArtifacts(out)
}

func parseFuzzCampaignRunArtifactsFromText(path string, text string, base FuzzCampaignRunArtifact) []FuzzCampaignRunArtifact {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	lower := strings.ToLower(text)
	out := []FuzzCampaignRunArtifact{}
	add := func(kind string, signal string, summary string, severity string) {
		artifact := base
		artifact.Kind = kind
		artifact.Path = path
		artifact.Signal = signal
		artifact.Summary = summary
		artifact.Severity = severity
		artifact.ID = fuzzCampaignFindingID(artifact.RunID, artifact.Kind, artifact.Path, artifact.Signal, artifact.Summary)
		out = append(out, artifact)
	}
	if strings.Contains(lower, "addresssanitizer") || strings.Contains(lower, "undefinedbehaviorsanitizer") || strings.Contains(lower, "threadsanitizer") || strings.Contains(lower, "leaksanitizer") || strings.Contains(lower, "runtime error:") {
		add("sanitizer_report", fuzzCampaignSanitizerSignal(text), fuzzCampaignFirstMatchingLine(text, "sanitizer", "runtime error:"), "high")
	}
	if strings.Contains(lower, "application verifier") || strings.Contains(lower, "verifier stop") {
		add("application_verifier_report", "application_verifier", fuzzCampaignFirstMatchingLine(text, "application verifier", "verifier stop"), "high")
	}
	if strings.Contains(lower, "driver verifier") || strings.Contains(lower, "driver_verifier_detected_violation") || strings.Contains(lower, "!verifier") {
		add("driver_verifier_report", "driver_verifier", fuzzCampaignFirstMatchingLine(text, "driver verifier", "driver_verifier_detected_violation", "!verifier"), "high")
	}
	return out
}

func parseFuzzCampaignRunArtifactFromCrashFile(path string, base FuzzCampaignRunArtifact) FuzzCampaignRunArtifact {
	ext := strings.ToLower(filepath.Ext(path))
	artifact := base
	artifact.Path = path
	artifact.Severity = "high"
	switch ext {
	case ".dmp", ".dump", ".mdmp", ".hdmp":
		artifact.Kind = "windows_crash_dump"
		artifact.Signal = "crash_dump"
		artifact.Summary = "Windows crash dump captured for native fuzz run"
	default:
		name := strings.ToLower(filepath.Base(path))
		if strings.Contains(name, "crash") || strings.Contains(name, "oom") || strings.Contains(name, "timeout") {
			artifact.Kind = "crash_input"
			artifact.Signal = "libfuzzer_crash_artifact"
			artifact.Summary = "Native fuzz crash artifact captured"
		}
	}
	if artifact.Kind == "" {
		return FuzzCampaignRunArtifact{}
	}
	artifact.ID = fuzzCampaignFindingID(artifact.RunID, artifact.Kind, artifact.Path, artifact.Signal)
	return artifact
}

func fuzzCampaignRunArtifactIDs(items []FuzzCampaignRunArtifact) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) != "" {
			ids = append(ids, item.ID)
		}
	}
	return uniqueStrings(ids)
}

func fuzzCampaignRunArtifactKindsForIDs(campaign FuzzCampaign, ids []string) []string {
	kinds := []string{}
	for _, id := range ids {
		for _, artifact := range campaign.RunArtifacts {
			if strings.EqualFold(strings.TrimSpace(artifact.ID), strings.TrimSpace(id)) {
				kinds = append(kinds, artifact.Kind)
			}
		}
	}
	return uniqueStrings(kinds)
}

func fuzzCampaignRunArtifactsNeedVerification(items []FuzzCampaignRunArtifact) bool {
	for _, item := range items {
		switch strings.ToLower(strings.TrimSpace(item.Kind)) {
		case "sanitizer_report", "application_verifier_report", "driver_verifier_report", "windows_crash_dump", "crash_input":
			return true
		}
	}
	return false
}

func fuzzCampaignRunArtifactsSummary(items []FuzzCampaignRunArtifact) string {
	for _, item := range items {
		if strings.TrimSpace(item.Summary) != "" {
			return item.Summary
		}
		if strings.TrimSpace(item.Kind) != "" {
			return item.Kind + " captured for native fuzz run"
		}
	}
	return ""
}

func fuzzCampaignSanitizerSignal(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "addresssanitizer"):
		return "address_sanitizer"
	case strings.Contains(lower, "undefinedbehaviorsanitizer"):
		return "undefined_behavior_sanitizer"
	case strings.Contains(lower, "threadsanitizer"):
		return "thread_sanitizer"
	case strings.Contains(lower, "leaksanitizer"):
		return "leak_sanitizer"
	case strings.Contains(lower, "runtime error:"):
		return "undefined_behavior_runtime_error"
	default:
		return "sanitizer"
	}
}

func fuzzCampaignFirstMatchingLine(text string, needles ...string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if trimmed == "" {
			continue
		}
		for _, needle := range needles {
			if strings.Contains(lower, strings.ToLower(strings.TrimSpace(needle))) {
				return compactPersistentMemoryText(trimmed, 220)
			}
		}
	}
	return compactPersistentMemoryText(text, 220)
}

func parseFuzzCampaignCoverageReport(path string, text string, run FunctionFuzzRun, result FuzzCampaignNativeResult) (FuzzCampaignCoverageReport, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return FuzzCampaignCoverageReport{}, false
	}
	base := FuzzCampaignCoverageReport{
		Target:       firstNonBlankString(result.Target, run.TargetSymbolName, run.TargetQuery),
		TargetFile:   firstNonBlankString(result.TargetFile, run.TargetFile),
		SourceAnchor: fuzzCampaignSourceAnchorForRun(run),
		RunID:        run.ID,
		Path:         path,
		RecordedAt:   time.Now(),
	}
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	if strings.HasSuffix(lowerPath, ".json") {
		if report, ok := parseFuzzCampaignJSONCoverageReport(text, base); ok {
			return report, true
		}
	}
	if strings.Contains(text, "SF:") && strings.Contains(text, "LH:") && strings.Contains(text, "LF:") {
		return parseFuzzCampaignLCOVReport(text, base), true
	}
	if report, ok := parseFuzzCampaignLLVMTextCoverageReport(text, base); ok {
		return report, true
	}
	if report, ok := parseFuzzCampaignLibFuzzerCoverageReport(text, base); ok {
		return report, true
	}
	return FuzzCampaignCoverageReport{}, false
}

func parseFuzzCampaignJSONCoverageReport(text string, base FuzzCampaignCoverageReport) (FuzzCampaignCoverageReport, bool) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return FuzzCampaignCoverageReport{}, false
	}
	report := base
	report.Format = "json"
	report.CoveragePercent = firstJSONFloat(raw, "coverage_percent", "line_coverage", "percent")
	report.LinesCovered = int(firstJSONFloat(raw, "lines_covered", "covered_lines", "covered"))
	report.LinesTotal = int(firstJSONFloat(raw, "lines_total", "total_lines", "total"))
	report.FeatureCount = int(firstJSONFloat(raw, "feature_count", "features", "ft"))
	report.CorpusCount = int(firstJSONFloat(raw, "corpus_count", "corpus", "corpus_entries"))
	if value, ok := raw["summary"].(string); ok {
		report.Summary = value
	}
	return finalizeFuzzCampaignCoverageReport(report), report.CoveragePercent > 0 || report.LinesTotal > 0 || report.FeatureCount > 0 || report.CorpusCount > 0
}

func parseFuzzCampaignLCOVReport(text string, base FuzzCampaignCoverageReport) FuzzCampaignCoverageReport {
	report := base
	report.Format = "lcov"
	total := sumRegexpInts(text, `(?m)^LF:(\d+)`)
	covered := sumRegexpInts(text, `(?m)^LH:(\d+)`)
	report.LinesTotal = total
	report.LinesCovered = covered
	report.Summary = fmt.Sprintf("lcov lines %d/%d", covered, total)
	return finalizeFuzzCampaignCoverageReport(report)
}

func parseFuzzCampaignLLVMTextCoverageReport(text string, base FuzzCampaignCoverageReport) (FuzzCampaignCoverageReport, bool) {
	re := regexp.MustCompile(`(?im)^\s*TOTAL\s+\d+\s+\d+\s+([0-9]+(?:\.[0-9]+)?)%`)
	match := re.FindStringSubmatch(text)
	if len(match) < 2 {
		return FuzzCampaignCoverageReport{}, false
	}
	report := base
	report.Format = "llvm-cov"
	report.CoveragePercent = parseFloatDefault(match[1], 0)
	report.Summary = fmt.Sprintf("llvm-cov total line coverage %.2f%%", report.CoveragePercent)
	return finalizeFuzzCampaignCoverageReport(report), true
}

func parseFuzzCampaignLibFuzzerCoverageReport(text string, base FuzzCampaignCoverageReport) (FuzzCampaignCoverageReport, bool) {
	re := regexp.MustCompile(`cov:\s*([0-9]+).*?ft:\s*([0-9]+).*?corp:\s*([0-9]+)`)
	matches := re.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return FuzzCampaignCoverageReport{}, false
	}
	last := matches[len(matches)-1]
	report := base
	report.Format = "libfuzzer"
	report.LinesCovered = parseIntDefault(last[1], 0)
	report.FeatureCount = parseIntDefault(last[2], 0)
	report.CorpusCount = parseIntDefault(last[3], 0)
	report.Summary = fmt.Sprintf("libFuzzer cov=%d ft=%d corpus=%d", report.LinesCovered, report.FeatureCount, report.CorpusCount)
	return finalizeFuzzCampaignCoverageReport(report), true
}

func finalizeFuzzCampaignCoverageReport(report FuzzCampaignCoverageReport) FuzzCampaignCoverageReport {
	if report.LinesTotal > 0 && report.CoveragePercent <= 0 {
		report.CoveragePercent = float64(report.LinesCovered) * 100.0 / float64(report.LinesTotal)
	}
	switch {
	case report.CoveragePercent > 0 && report.CoveragePercent < 35:
		report.Gap = true
		report.GapReason = fmt.Sprintf("%s coverage is %.2f%%", firstNonBlankString(report.Format, "coverage"), report.CoveragePercent)
		report.PriorityBoost = 20
	case report.Format == "libfuzzer" && report.FeatureCount > 0 && report.FeatureCount < 16:
		report.Gap = true
		report.GapReason = fmt.Sprintf("libFuzzer feature count is low: %d", report.FeatureCount)
		report.PriorityBoost = 16
	case report.Format == "libfuzzer" && report.CorpusCount > 0 && report.CorpusCount < 4:
		report.Gap = true
		report.GapReason = fmt.Sprintf("libFuzzer corpus count is low: %d", report.CorpusCount)
		report.PriorityBoost = 12
	}
	if report.Summary == "" {
		report.Summary = firstNonBlankString(report.GapReason, report.Format+" coverage report")
	}
	report.ID = fuzzCampaignFindingID(report.RunID, report.Format, firstNonBlankString(report.Path, report.SourceAnchor, report.TargetFile, report.Target), report.Summary)
	return report
}

func firstJSONFloat(raw map[string]any, names ...string) float64 {
	for _, name := range names {
		value, ok := raw[name]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return typed
		case int:
			return float64(typed)
		case string:
			return parseFloatDefault(typed, 0)
		}
	}
	return 0
}

func sumRegexpInts(text string, pattern string) int {
	re := regexp.MustCompile(pattern)
	total := 0
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			total += parseIntDefault(match[1], 0)
		}
	}
	return total
}

func parseIntDefault(value string, fallback int) int {
	var out int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &out); err != nil {
		return fallback
	}
	return out
}

func parseFloatDefault(value string, fallback float64) float64 {
	var out float64
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%f", &out); err != nil {
		return fallback
	}
	return out
}

func fuzzCampaignNativeStatusIsRecordable(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "completed", "failed", "canceled", "preempted", "blocked":
		return true
	default:
		return false
	}
}

func buildFuzzCampaignSeedFinding(campaign FuzzCampaign, run FunctionFuzzRun, artifact FuzzCampaignSeedArtifact) FuzzCampaignFinding {
	severity := "low"
	if run.RiskScore >= 80 {
		severity = "high"
	} else if run.RiskScore >= 50 {
		severity = "medium"
	}
	now := time.Now()
	return FuzzCampaignFinding{
		ID:                 fuzzCampaignFindingID(campaign.ID, run.ID, artifact.Path),
		Status:             "seeded",
		Severity:           severity,
		Target:             firstNonBlankString(run.TargetSymbolName, run.TargetQuery),
		TargetFile:         run.TargetFile,
		SourceAnchor:       firstNonBlankString(artifact.SourceHint, fuzzCampaignSourceAnchorForRun(run)),
		FuzzRunID:          run.ID,
		SeedArtifacts:      []string{artifact.Path},
		VerificationGate:   "pending_native",
		TrackedFeatureGate: "monitor",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func buildFuzzCampaignNativeFinding(campaign FuzzCampaign, run FunctionFuzzRun, result FuzzCampaignNativeResult) FuzzCampaignFinding {
	status := "monitoring"
	severity := "low"
	verificationGate := "optional"
	featureGate := "monitor"
	if fuzzCampaignNativeResultNeedsVerification(result) {
		status = "open"
		verificationGate = "required"
		featureGate = "block_close"
		severity = "medium"
	}
	if result.CrashCount > 0 || len(result.ArtifactIDs) > 0 {
		severity = "high"
	}
	now := time.Now()
	nativeKey := fuzzCampaignNativeResultKey(result)
	sourceAnchor := fuzzCampaignSourceAnchorForRun(run)
	findingID := fuzzCampaignFindingIDForNativeResult(campaign, run, result)
	return FuzzCampaignFinding{
		ID:                 findingID,
		Status:             status,
		Severity:           severity,
		Target:             firstNonBlankString(result.Target, run.TargetSymbolName, run.TargetQuery),
		TargetFile:         firstNonBlankString(result.TargetFile, run.TargetFile),
		SourceAnchor:       sourceAnchor,
		FuzzRunID:          run.ID,
		SeedArtifacts:      fuzzCampaignSeedArtifactsForRun(campaign, run.ID),
		NativeResultKey:    nativeKey,
		NativeResultKeys:   []string{nativeKey},
		EvidenceID:         result.EvidenceID,
		EvidenceIDs:        []string{result.EvidenceID},
		VerificationGate:   verificationGate,
		TrackedFeatureGate: featureGate,
		CrashFingerprint:   result.CrashFingerprint,
		SuspectedInvariant: result.SuspectedInvariant,
		ReportPath:         result.ReportPath,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func upsertFuzzCampaignFinding(items []FuzzCampaignFinding, finding FuzzCampaignFinding) []FuzzCampaignFinding {
	normalized := normalizeFuzzCampaignFindings([]FuzzCampaignFinding{finding})
	if len(normalized) == 0 {
		return normalizeFuzzCampaignFindings(items)
	}
	finding = normalized[0]
	for i := range items {
		existing := normalizeFuzzCampaignFindings([]FuzzCampaignFinding{items[i]})
		if len(existing) == 0 {
			continue
		}
		existingFinding := existing[0]
		sameID := strings.EqualFold(existingFinding.ID, finding.ID)
		sameDedup := strings.TrimSpace(existingFinding.DedupKey) != "" && strings.EqualFold(existingFinding.DedupKey, finding.DedupKey)
		if sameID || sameDedup {
			items[i] = mergeFuzzCampaignFinding(existingFinding, finding)
			return normalizeFuzzCampaignFindings(items)
		}
	}
	return normalizeFuzzCampaignFindings(append(items, finding))
}

func fuzzCampaignSeedArtifactsForRun(campaign FuzzCampaign, runID string) []string {
	paths := []string{}
	for _, artifact := range campaign.SeedArtifacts {
		if strings.EqualFold(strings.TrimSpace(artifact.RunID), strings.TrimSpace(runID)) {
			paths = append(paths, artifact.Path)
		}
	}
	return normalizeFuzzCampaignFindingPaths(paths)
}

func fuzzCampaignFindingForNativeResult(campaign FuzzCampaign, result FuzzCampaignNativeResult) FuzzCampaignFinding {
	key := fuzzCampaignNativeResultKey(result)
	for _, finding := range campaign.Findings {
		if strings.EqualFold(finding.NativeResultKey, key) || containsString(finding.NativeResultKeys, key) {
			return finding
		}
	}
	return FuzzCampaignFinding{}
}

func fuzzCampaignCrashFingerprint(run FunctionFuzzRun) string {
	if run.Execution.CrashCount <= 0 && strings.TrimSpace(run.Execution.CrashDir) != "" {
		if functionFuzzCountCrashArtifacts(run.Execution.CrashDir) <= 0 {
			return ""
		}
	}
	parts := []string{
		strings.TrimSpace(run.TargetSymbolID),
		strings.TrimSpace(run.TargetSymbolName),
		filepath.ToSlash(strings.TrimSpace(run.TargetFile)),
	}
	seed := strings.ToLower(strings.Join(uniqueStrings(parts), "|"))
	if strings.TrimSpace(seed) == "" {
		seed = strings.TrimSpace(run.ID)
	}
	return fmt.Sprintf("ff-%08x", stableHash32(seed))
}

func stableHash32(value string) uint32 {
	var hash uint32 = 2166136261
	for _, b := range []byte(value) {
		hash ^= uint32(b)
		hash *= 16777619
	}
	return hash
}

func fuzzCampaignSuspectedInvariant(run FunctionFuzzRun) string {
	for _, scenario := range functionFuzzSortedVirtualScenarios(run.VirtualScenarios) {
		if len(scenario.LikelyIssues) > 0 {
			return compactPersistentMemoryText(scenario.LikelyIssues[0], 220)
		}
		if strings.TrimSpace(scenario.ExpectedFlow) != "" {
			return compactPersistentMemoryText(scenario.ExpectedFlow, 220)
		}
	}
	for _, observation := range run.CodeObservations {
		if strings.TrimSpace(observation.WhyItMatters) != "" {
			return compactPersistentMemoryText(observation.WhyItMatters, 220)
		}
	}
	return "native fuzzing changed the execution state for this target"
}

func fuzzCampaignMinimizeCommand(campaign FuzzCampaign, run FunctionFuzzRun) string {
	if strings.TrimSpace(run.Execution.RunCommand) == "" {
		return ""
	}
	corpusDir := firstNonBlankString(run.Execution.CorpusDir, campaign.CorpusDir)
	if strings.TrimSpace(corpusDir) == "" {
		return ""
	}
	return strings.TrimSpace(run.Execution.RunCommand) + " -merge=1 " + filepath.ToSlash(filepath.Join(corpusDir, "minimized")) + " " + filepath.ToSlash(corpusDir)
}

func writeFuzzCampaignNativeResultReport(campaign FuzzCampaign, run FunctionFuzzRun, result FuzzCampaignNativeResult) (string, error) {
	name := fmt.Sprintf("native-result-%s-%s.md", fuzzCampaignSafePathPart(run.ID), fuzzCampaignSafePathPart(firstNonBlankString(result.Status, result.Outcome, "result")))
	path := filepath.Join(campaign.ReportsDir, name)
	var b strings.Builder
	fmt.Fprintf(&b, "# Native Fuzz Result: %s\n\n", firstNonBlankString(result.Target, run.ID))
	fmt.Fprintf(&b, "- Campaign: `%s`\n", campaign.ID)
	fmt.Fprintf(&b, "- Function fuzz run: `%s`\n", run.ID)
	if findingID := fuzzCampaignFindingIDForNativeResult(campaign, run, result); findingID != "" {
		fmt.Fprintf(&b, "- Finding: `%s`\n", findingID)
	}
	if strings.TrimSpace(result.TargetFile) != "" {
		fmt.Fprintf(&b, "- Target file: `%s`\n", filepath.ToSlash(result.TargetFile))
	}
	fmt.Fprintf(&b, "- Status: `%s`\n", valueOrUnset(result.Status))
	fmt.Fprintf(&b, "- Outcome: `%s`\n", valueOrUnset(result.Outcome))
	fmt.Fprintf(&b, "- Crash artifacts: `%d`\n", result.CrashCount)
	if strings.TrimSpace(result.CrashFingerprint) != "" {
		fmt.Fprintf(&b, "- Crash fingerprint: `%s`\n", result.CrashFingerprint)
	}
	if strings.TrimSpace(result.SuspectedInvariant) != "" {
		fmt.Fprintf(&b, "- Suspected invariant: %s\n", result.SuspectedInvariant)
	}
	if run.Execution.ExitCode != nil {
		fmt.Fprintf(&b, "- Exit code: `%d`\n", *run.Execution.ExitCode)
	}
	if strings.TrimSpace(result.BuildLogPath) != "" {
		fmt.Fprintf(&b, "- Build log: `%s`\n", filepath.ToSlash(result.BuildLogPath))
	}
	if strings.TrimSpace(result.RunLogPath) != "" {
		fmt.Fprintf(&b, "- Run log: `%s`\n", filepath.ToSlash(result.RunLogPath))
	}
	if strings.TrimSpace(result.CrashDir) != "" {
		fmt.Fprintf(&b, "- Crash dir: `%s`\n", filepath.ToSlash(result.CrashDir))
	}
	if len(result.ArtifactIDs) > 0 {
		fmt.Fprintf(&b, "- Run artifacts: `%s`\n", strings.Join(result.ArtifactIDs, ", "))
	}
	if strings.TrimSpace(result.MinimizeCommand) != "" {
		fmt.Fprintf(&b, "\n## Minimization\n\n```text\n%s\n```\n", result.MinimizeCommand)
	}
	if strings.TrimSpace(run.Execution.LastOutput) != "" {
		fmt.Fprintf(&b, "\n## Last Output\n\n```text\n%s\n```\n", run.Execution.LastOutput)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(b.String())+"\n"), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func buildFuzzCampaignNativeEvidenceRecord(campaign FuzzCampaign, run FunctionFuzzRun, result FuzzCampaignNativeResult, workspace string, sessionID string) EvidenceRecord {
	severity := "low"
	if result.CrashCount > 0 || len(result.ArtifactIDs) > 0 {
		severity = "high"
	} else if strings.EqualFold(result.Outcome, "failed") {
		severity = "medium"
	}
	risk := run.RiskScore
	if (result.CrashCount > 0 || len(result.ArtifactIDs) > 0) && risk < 85 {
		risk = 85
	} else if strings.EqualFold(result.Outcome, "failed") && risk < 60 {
		risk = 60
	}
	record := EvidenceRecord{
		SessionID:           sessionID,
		Workspace:           workspace,
		Kind:                "fuzz_native_result",
		Category:            "fuzzing",
		Subject:             firstNonBlankString(result.Target, run.TargetSymbolName, run.ID),
		Outcome:             result.Outcome,
		Severity:            severity,
		Confidence:          "medium",
		SignalClass:         "fuzzing",
		RiskScore:           risk,
		VerificationSummary: fmt.Sprintf("campaign=%s run=%s status=%s crashes=%d artifacts=%d", campaign.ID, run.ID, valueOrUnset(result.Status), result.CrashCount, len(result.ArtifactIDs)),
		Tags:                []string{"fuzz", "native", "campaign", result.Outcome},
		Attributes: map[string]string{
			"campaign_id":         campaign.ID,
			"finding_id":          fuzzCampaignFindingIDForNativeResult(campaign, run, result),
			"fuzz_run_id":         run.ID,
			"target_symbol_id":    run.TargetSymbolID,
			"source_anchor":       fuzzCampaignSourceAnchorForRun(run),
			"target_file":         filepath.ToSlash(firstNonBlankString(result.TargetFile, run.TargetFile)),
			"report_path":         filepath.ToSlash(result.ReportPath),
			"crash_dir":           filepath.ToSlash(result.CrashDir),
			"crash_fingerprint":   result.CrashFingerprint,
			"suspected_invariant": result.SuspectedInvariant,
			"minimize_command":    result.MinimizeCommand,
			"artifact_ids":        strings.Join(result.ArtifactIDs, ","),
		},
	}
	return normalizeEvidenceRecord(record)
}

func writeFuzzCampaignScenarioSeed(dir string, run FunctionFuzzRun, scenario FunctionFuzzVirtualScenario, ordinal int) (FuzzCampaignSeedArtifact, error) {
	name := fmt.Sprintf("scenario-%02d-%s.json", ordinal, fuzzCampaignSafePathPart(firstNonBlankString(scenario.Title, run.TargetSymbolName, run.ID)))
	path := filepath.Join(dir, name)
	inputs := uniqueStrings(append(append([]string{}, scenario.ConcreteInputs...), scenario.Inputs...))
	sourceHint := fuzzCampaignSourceHintForScenario(run, scenario)
	payload := map[string]any{
		"schema":           "kernforge.fuzz_campaign.seed.v1",
		"run_id":           run.ID,
		"target":           firstNonBlankString(run.TargetSymbolName, run.TargetQuery),
		"target_symbol_id": run.TargetSymbolID,
		"target_file":      filepath.ToSlash(strings.TrimSpace(run.TargetFile)),
		"scenario":         strings.TrimSpace(scenario.Title),
		"risk_score":       scenario.RiskScore,
		"confidence":       strings.TrimSpace(scenario.Confidence),
		"inputs":           inputs,
		"expected_flow":    strings.TrimSpace(scenario.ExpectedFlow),
		"likely_issues":    uniqueStrings(scenario.LikelyIssues),
		"path_sketch":      uniqueStrings(scenario.PathSketch),
		"branch_facts":     uniqueStrings(scenario.BranchFacts),
		"source_hint":      sourceHint,
		"source":           "source_only_function_fuzz",
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return FuzzCampaignSeedArtifact{}, err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return FuzzCampaignSeedArtifact{}, err
	}
	return FuzzCampaignSeedArtifact{
		RunID:      run.ID,
		Scenario:   strings.TrimSpace(scenario.Title),
		Path:       path,
		Source:     "source_only_function_fuzz",
		Inputs:     inputs,
		SourceHint: sourceHint,
	}, nil
}

func fuzzCampaignSourceAnchorForRun(run FunctionFuzzRun) string {
	file := filepath.ToSlash(strings.TrimSpace(run.TargetFile))
	if file == "" {
		return ""
	}
	if run.TargetStartLine > 0 {
		return fmt.Sprintf("%s:%d", file, run.TargetStartLine)
	}
	return file
}

func fuzzCampaignSourceHintForScenario(run FunctionFuzzRun, scenario FunctionFuzzVirtualScenario) string {
	excerpt := scenario.SourceExcerpt
	file := filepath.ToSlash(strings.TrimSpace(firstNonBlankString(excerpt.File, scenario.FocusFile, run.TargetFile)))
	if file == "" {
		return ""
	}
	if excerpt.FocusLine > 0 {
		return fmt.Sprintf("%s:%d", file, excerpt.FocusLine)
	}
	if excerpt.StartLine > 0 {
		return fmt.Sprintf("%s:%d", file, excerpt.StartLine)
	}
	return file
}

func fuzzCampaignSafePathPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "item"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "item"
	}
	if len(out) > 64 {
		out = strings.Trim(out[:64], "-")
	}
	return out
}

func firstFuzzCampaignString(items []string) string {
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			return strings.TrimSpace(item)
		}
	}
	return ""
}

func (rt *runtimeState) handleFuzzCampaignCommand(args string) error {
	if rt == nil || rt.fuzzCampaigns == nil {
		return fmt.Errorf("fuzz campaign store is not configured")
	}
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 || strings.EqualFold(fields[0], "status") {
		return rt.showFuzzCampaignStatus()
	}
	switch strings.ToLower(fields[0]) {
	case "run", "start":
		return rt.runFuzzCampaignAutomation()
	case "new", "create":
		return rt.createFuzzCampaign(strings.TrimSpace(strings.Join(fields[1:], " ")))
	case "list":
		return rt.listFuzzCampaigns()
	case "show":
		id := "latest"
		if len(fields) > 1 {
			id = fields[1]
		}
		return rt.showFuzzCampaign(id)
	case "attach":
		if len(fields) < 3 {
			return fmt.Errorf("usage: /fuzz-campaign attach <campaign|latest> <fuzz-run|latest>")
		}
		return rt.attachFuzzCampaignRun(fields[1], fields[2])
	case "promote-seeds":
		campaignID := "latest"
		runID := "all"
		if len(fields) > 1 {
			campaignID = fields[1]
		}
		if len(fields) > 2 {
			runID = fields[2]
		}
		return rt.promoteFuzzCampaignSeeds(campaignID, runID)
	default:
		return fmt.Errorf("usage: /fuzz-campaign [status|run|new <name>|list|show <id|latest>]")
	}
}

func (rt *runtimeState) createFuzzCampaign(name string) error {
	root := workspaceSnapshotRoot(rt.workspace)
	manifest := AnalysisDocsManifest{}
	if loaded, ok := loadLatestAnalysisDocsManifest(root); ok {
		manifest = loaded
	}
	campaign, err := createFuzzCampaignFromWorkspace(root, name, manifest)
	if err != nil {
		return err
	}
	saved, err := rt.fuzzCampaigns.Append(campaign)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Fuzz Campaign"))
	fmt.Fprintln(rt.writer, rt.ui.successLine("Created fuzz campaign: "+saved.ID))
	fmt.Fprintln(rt.writer, renderFuzzCampaign(saved))
	return nil
}

func (rt *runtimeState) showFuzzCampaignStatus() error {
	items, err := rt.fuzzCampaigns.ListRecent(workspaceSnapshotRoot(rt.workspace), 1)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Fuzz Campaigns"))
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.hintLine("No fuzz campaigns found. Kernforge can create one when you run /fuzz-campaign run."))
		fmt.Fprintln(rt.writer, renderFuzzCampaignNextStep(rt.fuzzCampaignAutomationPlan(FuzzCampaign{})))
		return nil
	}
	fmt.Fprintln(rt.writer, renderFuzzCampaign(items[0]))
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, renderFuzzCampaignNextStep(rt.fuzzCampaignAutomationPlan(items[0])))
	return nil
}

func (rt *runtimeState) listFuzzCampaigns() error {
	items, err := rt.fuzzCampaigns.ListRecent(workspaceSnapshotRoot(rt.workspace), 12)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Fuzz Campaigns"))
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.hintLine("No fuzz campaigns found."))
		return nil
	}
	for _, item := range items {
		fmt.Fprintln(rt.writer, fmt.Sprintf("- %s | %s | seeds=%d | %s", item.ID, item.Status, len(item.SeedTargets), item.Name))
	}
	return nil
}

func (rt *runtimeState) showFuzzCampaign(id string) error {
	campaign, ok, err := rt.resolveFuzzCampaign(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("fuzz campaign not found: %s", id)
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Fuzz Campaign"))
	fmt.Fprintln(rt.writer, renderFuzzCampaign(campaign))
	return nil
}

func (rt *runtimeState) attachFuzzCampaignRun(campaignID string, runID string) error {
	if rt == nil || rt.functionFuzz == nil {
		return fmt.Errorf("function fuzz store is not configured")
	}
	campaign, ok, err := rt.resolveFuzzCampaign(campaignID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("fuzz campaign not found: %s", campaignID)
	}
	run, ok, err := rt.resolveFunctionFuzzRun(runID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("function fuzz run not found: %s", runID)
	}
	campaign = attachFunctionFuzzRunToCampaign(campaign, run)
	if err := writeFuzzCampaignManifest(campaign); err != nil {
		return err
	}
	saved, err := rt.fuzzCampaigns.Upsert(campaign)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Fuzz Campaign"))
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Attached fuzz run %s to campaign %s.", run.ID, saved.ID)))
	fmt.Fprintln(rt.writer, renderFuzzCampaign(saved))
	return nil
}

func (rt *runtimeState) promoteFuzzCampaignSeeds(campaignID string, runID string) error {
	if rt == nil || rt.functionFuzz == nil {
		return fmt.Errorf("function fuzz store is not configured")
	}
	campaign, ok, err := rt.resolveFuzzCampaign(campaignID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("fuzz campaign not found: %s", campaignID)
	}
	runs, err := rt.resolveFunctionFuzzRunsForCampaign(campaign, runID)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		return fmt.Errorf("no function fuzz runs available to promote")
	}
	updated, promoted, err := promoteFunctionFuzzRunSeeds(campaign, runs, 16)
	if err != nil {
		return err
	}
	saved, err := rt.fuzzCampaigns.Upsert(updated)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Fuzz Campaign"))
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Promoted %d seed artifact(s) into campaign %s.", len(promoted), saved.ID)))
	for _, artifact := range limitFuzzCampaignSeedArtifacts(promoted, 8) {
		fmt.Fprintln(rt.writer, "- "+filepath.ToSlash(artifact.Path))
	}
	return nil
}

func (rt *runtimeState) runFuzzCampaignAutomation() error {
	root := workspaceSnapshotRoot(rt.workspace)
	campaign, ok, err := rt.resolveFuzzCampaign("latest")
	if err != nil {
		return err
	}
	created := false
	if !ok {
		manifest := AnalysisDocsManifest{}
		if loaded, loadedOK := loadLatestAnalysisDocsManifest(root); loadedOK {
			manifest = loaded
		}
		campaign, err = createFuzzCampaignFromWorkspace(root, "Autonomous fuzz campaign", manifest)
		if err != nil {
			return err
		}
		campaign, err = rt.fuzzCampaigns.Append(campaign)
		if err != nil {
			return err
		}
		created = true
	}
	latestRun, hasLatestRun, err := rt.resolveFunctionFuzzRun("latest")
	if err != nil {
		return err
	}
	attached := false
	if hasLatestRun && strings.TrimSpace(latestRun.ID) != "" && !containsString(campaign.FunctionRuns, latestRun.ID) {
		campaign = attachFunctionFuzzRunToCampaign(campaign, latestRun)
		attached = true
	}
	runs, err := rt.functionFuzzRunsNeedingSeedPromotion(campaign)
	if err != nil {
		return err
	}
	var promoted []FuzzCampaignSeedArtifact
	if len(runs) > 0 {
		campaign, promoted, err = promoteFunctionFuzzRunSeeds(campaign, runs, 16)
		if err != nil {
			return err
		}
	}
	campaignRuns, err := rt.resolveFunctionFuzzRunsForCampaign(campaign, "all")
	if err != nil {
		return err
	}
	refreshedRuns := make([]FunctionFuzzRun, 0, len(campaignRuns))
	for _, run := range campaignRuns {
		if refreshed, changed := rt.refreshFunctionFuzzExecution(run); changed {
			run = refreshed
			if _, err := rt.functionFuzz.Upsert(run); err != nil {
				return err
			}
		}
		refreshedRuns = append(refreshedRuns, run)
	}
	var nativeResults []FuzzCampaignNativeResult
	if len(campaign.SeedArtifacts) > 0 {
		campaign, nativeResults, err = rt.captureFuzzCampaignNativeResults(campaign, refreshedRuns)
		if err != nil {
			return err
		}
	}
	if !created || attached || len(promoted) > 0 || len(nativeResults) > 0 {
		campaign, err = rt.fuzzCampaigns.Upsert(campaign)
		if err != nil {
			return err
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Fuzz Campaign"))
	switch {
	case created || attached || len(promoted) > 0 || len(nativeResults) > 0:
		var actions []string
		if created {
			actions = append(actions, "created campaign")
		}
		if attached {
			actions = append(actions, "attached latest /fuzz-func run")
		}
		if len(promoted) > 0 {
			actions = append(actions, fmt.Sprintf("promoted %d seed artifact(s)", len(promoted)))
		}
		if len(nativeResults) > 0 {
			actions = append(actions, fmt.Sprintf("captured %d native result(s)", len(nativeResults)))
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Kernforge advanced the fuzz campaign: "+strings.Join(actions, ", ")+"."))
	default:
		fmt.Fprintln(rt.writer, rt.ui.hintLine("No automatic campaign action is needed right now."))
	}
	fmt.Fprintln(rt.writer, renderFuzzCampaign(campaign))
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, renderFuzzCampaignNextStep(rt.fuzzCampaignAutomationPlan(campaign)))
	return nil
}

func (rt *runtimeState) functionFuzzRunsNeedingSeedPromotion(campaign FuzzCampaign) ([]FunctionFuzzRun, error) {
	if rt == nil || rt.functionFuzz == nil {
		return nil, nil
	}
	promoted := map[string]struct{}{}
	for _, artifact := range campaign.SeedArtifacts {
		if strings.TrimSpace(artifact.RunID) != "" {
			promoted[strings.TrimSpace(artifact.RunID)] = struct{}{}
		}
	}
	var runs []FunctionFuzzRun
	for _, id := range campaign.FunctionRuns {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := promoted[id]; ok {
			continue
		}
		run, ok, err := rt.functionFuzz.Get(id)
		if err != nil {
			return nil, err
		}
		if ok && len(run.VirtualScenarios) > 0 {
			runs = append(runs, run)
		}
	}
	return runs, nil
}

type fuzzCampaignAutomationPlan struct {
	Title   string
	Details []string
	Command string
	CanRun  bool
}

func (rt *runtimeState) fuzzCampaignAutomationPlan(campaign FuzzCampaign) fuzzCampaignAutomationPlan {
	latestRun, hasLatestRun, _ := rt.resolveFunctionFuzzRun("latest")
	hasCampaign := strings.TrimSpace(campaign.ID) != ""
	switch {
	case !hasCampaign && hasLatestRun && len(latestRun.VirtualScenarios) > 0:
		return fuzzCampaignAutomationPlan{
			Title: "Suggested next step: create a campaign, attach the latest /fuzz-func run, and promote source-only scenarios into corpus seeds.",
			Details: []string{
				"Latest fuzz run: " + latestRun.ID,
				fmt.Sprintf("Source-only scenarios: %d", len(latestRun.VirtualScenarios)),
			},
			Command: "/fuzz-campaign run",
			CanRun:  true,
		}
	case hasCampaign && hasLatestRun && !containsString(campaign.FunctionRuns, latestRun.ID) && len(latestRun.VirtualScenarios) > 0:
		return fuzzCampaignAutomationPlan{
			Title: "Suggested next step: attach the latest /fuzz-func run and promote its source-only scenarios.",
			Details: []string{
				"Campaign: " + campaign.ID,
				"Latest fuzz run: " + latestRun.ID,
			},
			Command: "/fuzz-campaign run",
			CanRun:  true,
		}
	case hasCampaign && len(campaign.FunctionRuns) > 0 && len(campaign.SeedArtifacts) == 0:
		return fuzzCampaignAutomationPlan{
			Title: "Suggested next step: promote attached source-only scenarios into deterministic corpus seed artifacts.",
			Details: []string{
				fmt.Sprintf("Attached fuzz runs: %d", len(campaign.FunctionRuns)),
			},
			Command: "/fuzz-campaign run",
			CanRun:  true,
		}
	case hasCampaign && len(campaign.SeedArtifacts) > 0:
		return fuzzCampaignAutomationPlan{
			Title: "Campaign seed corpus is ready. Next automation target is native fuzz execution and evidence capture.",
			Details: []string{
				fmt.Sprintf("Seed artifacts: %d", len(campaign.SeedArtifacts)),
				fmt.Sprintf("Native results captured: %d", len(campaign.NativeResults)),
			},
			Command: "/fuzz-campaign run",
			CanRun:  true,
		}
	case hasLatestRun:
		return fuzzCampaignAutomationPlan{
			Title:   "Latest /fuzz-func run has no source-only scenarios to promote. Run /fuzz-func on an input-facing target first.",
			Details: []string{"Latest fuzz run: " + latestRun.ID},
			Command: "",
			CanRun:  false,
		}
	default:
		return fuzzCampaignAutomationPlan{
			Title:   "No /fuzz-func run is available yet. Start with /fuzz-func on an input-facing parser, IOCTL, decoder, or buffer-processing function.",
			Command: "",
			CanRun:  false,
		}
	}
}

func renderFuzzCampaignNextStep(plan fuzzCampaignAutomationPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Next: %s\n", plan.Title)
	for _, detail := range plan.Details {
		fmt.Fprintf(&b, "- %s\n", detail)
	}
	if plan.CanRun && strings.TrimSpace(plan.Command) != "" {
		fmt.Fprintf(&b, "Continue: %s\n", plan.Command)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (rt *runtimeState) resolveFuzzCampaign(id string) (FuzzCampaign, bool, error) {
	if rt == nil || rt.fuzzCampaigns == nil {
		return FuzzCampaign{}, false, fmt.Errorf("fuzz campaign store is not configured")
	}
	query := strings.TrimSpace(id)
	if query == "" || strings.EqualFold(query, "latest") {
		items, err := rt.fuzzCampaigns.ListRecent(workspaceSnapshotRoot(rt.workspace), 1)
		if err != nil {
			return FuzzCampaign{}, false, err
		}
		if len(items) == 0 {
			return FuzzCampaign{}, false, nil
		}
		return items[0], true, nil
	}
	return rt.fuzzCampaigns.Get(query)
}

func (rt *runtimeState) resolveFunctionFuzzRun(id string) (FunctionFuzzRun, bool, error) {
	if rt == nil || rt.functionFuzz == nil {
		return FunctionFuzzRun{}, false, fmt.Errorf("function fuzz store is not configured")
	}
	query := strings.TrimSpace(id)
	if query == "" || strings.EqualFold(query, "latest") {
		items, err := rt.functionFuzz.ListRecent(workspaceSnapshotRoot(rt.workspace), 1)
		if err != nil {
			return FunctionFuzzRun{}, false, err
		}
		if len(items) == 0 {
			return FunctionFuzzRun{}, false, nil
		}
		return items[0], true, nil
	}
	return rt.functionFuzz.Get(query)
}

func (rt *runtimeState) resolveFunctionFuzzRunsForCampaign(campaign FuzzCampaign, runID string) ([]FunctionFuzzRun, error) {
	query := strings.TrimSpace(runID)
	if query == "" {
		query = "all"
	}
	if !strings.EqualFold(query, "all") {
		run, ok, err := rt.resolveFunctionFuzzRun(query)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("function fuzz run not found: %s", query)
		}
		return []FunctionFuzzRun{run}, nil
	}
	ids := append([]string{}, campaign.FunctionRuns...)
	sort.Strings(ids)
	var runs []FunctionFuzzRun
	for _, id := range ids {
		run, ok, err := rt.functionFuzz.Get(id)
		if err != nil {
			return nil, err
		}
		if ok {
			runs = append(runs, run)
		}
	}
	return runs, nil
}

func renderFuzzCampaign(campaign FuzzCampaign) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ID: %s\n", campaign.ID)
	fmt.Fprintf(&b, "Name: %s\n", campaign.Name)
	fmt.Fprintf(&b, "Status: %s\n", campaign.Status)
	fmt.Fprintf(&b, "Workspace: %s\n", campaign.Workspace)
	fmt.Fprintf(&b, "Manifest: %s\n", campaign.ManifestPath)
	fmt.Fprintf(&b, "Corpus: %s\n", campaign.CorpusDir)
	fmt.Fprintf(&b, "Crashes: %s\n", campaign.CrashDir)
	fmt.Fprintf(&b, "Coverage: %s\n", campaign.CoverageDir)
	fmt.Fprintf(&b, "Reports: %s\n", campaign.ReportsDir)
	fmt.Fprintf(&b, "Logs: %s\n", campaign.LogsDir)
	if len(campaign.FunctionRuns) > 0 {
		fmt.Fprintf(&b, "Attached fuzz runs: %s\n", strings.Join(campaign.FunctionRuns, ", "))
	}
	if len(campaign.SeedTargets) > 0 {
		fmt.Fprintf(&b, "Seed targets:\n")
		for _, target := range limitFuzzCampaignSeedTargets(campaign.SeedTargets, 8) {
			line := "- " + target.Name
			if strings.TrimSpace(target.File) != "" {
				line += " @ " + target.File
			}
			if strings.TrimSpace(target.SuggestedCommand) != "" {
				line += " | " + target.SuggestedCommand
			}
			fmt.Fprintln(&b, line)
		}
	}
	if len(campaign.SeedArtifacts) > 0 {
		fmt.Fprintf(&b, "Seed artifacts:\n")
		for _, artifact := range limitFuzzCampaignSeedArtifacts(campaign.SeedArtifacts, 8) {
			fmt.Fprintf(&b, "- %s | %s\n", filepath.ToSlash(artifact.Path), artifact.Scenario)
		}
	}
	if len(campaign.NativeResults) > 0 {
		fmt.Fprintf(&b, "Native results:\n")
		for _, result := range limitFuzzCampaignNativeResults(campaign.NativeResults, 6) {
			line := fmt.Sprintf("- %s status=%s outcome=%s crashes=%d", result.RunID, valueOrUnset(result.Status), valueOrUnset(result.Outcome), result.CrashCount)
			if strings.TrimSpace(result.TargetFile) != "" {
				line += " file=" + filepath.ToSlash(result.TargetFile)
			}
			if strings.TrimSpace(result.EvidenceID) != "" {
				line += " evidence=" + result.EvidenceID
			}
			if len(result.ArtifactIDs) > 0 {
				line += fmt.Sprintf(" artifacts=%d", len(result.ArtifactIDs))
			}
			if strings.TrimSpace(result.ReportPath) != "" {
				line += " report=" + filepath.ToSlash(result.ReportPath)
			}
			fmt.Fprintln(&b, line)
		}
	}
	if len(campaign.RunArtifacts) > 0 {
		fmt.Fprintf(&b, "Run artifacts:\n")
		for _, artifact := range limitFuzzCampaignRunArtifacts(campaign.RunArtifacts, 8) {
			line := fmt.Sprintf("- %s kind=%s severity=%s", artifact.ID, valueOrUnset(artifact.Kind), valueOrUnset(artifact.Severity))
			if strings.TrimSpace(artifact.Signal) != "" {
				line += " signal=" + artifact.Signal
			}
			if strings.TrimSpace(artifact.Path) != "" {
				line += " path=" + filepath.ToSlash(artifact.Path)
			}
			if strings.TrimSpace(artifact.Summary) != "" {
				line += " | " + artifact.Summary
			}
			fmt.Fprintln(&b, line)
		}
	}
	if len(campaign.Findings) > 0 {
		fmt.Fprintf(&b, "Findings:\n")
		for _, finding := range limitFuzzCampaignFindings(campaign.Findings, 8) {
			line := fmt.Sprintf("- %s status=%s severity=%s", finding.ID, valueOrUnset(finding.Status), valueOrUnset(finding.Severity))
			if strings.TrimSpace(finding.Target) != "" {
				line += " target=" + finding.Target
			}
			if strings.TrimSpace(finding.SourceAnchor) != "" {
				line += " source=" + filepath.ToSlash(finding.SourceAnchor)
			}
			if strings.TrimSpace(finding.VerificationGate) != "" {
				line += " verify=" + finding.VerificationGate
			}
			if strings.TrimSpace(finding.TrackedFeatureGate) != "" {
				line += " feature=" + finding.TrackedFeatureGate
			}
			if finding.DuplicateCount > 0 {
				line += fmt.Sprintf(" duplicates=%d", finding.DuplicateCount)
			}
			fmt.Fprintln(&b, line)
		}
	}
	if len(campaign.CoverageReports) > 0 {
		fmt.Fprintf(&b, "Coverage reports:\n")
		for _, report := range limitFuzzCampaignCoverageReports(campaign.CoverageReports, 8) {
			line := fmt.Sprintf("- %s format=%s", report.ID, valueOrUnset(report.Format))
			if report.CoveragePercent > 0 {
				line += fmt.Sprintf(" coverage=%.2f%%", report.CoveragePercent)
			}
			if report.FeatureCount > 0 {
				line += fmt.Sprintf(" features=%d", report.FeatureCount)
			}
			if report.Gap {
				line += " gap=true"
			}
			if strings.TrimSpace(report.GapReason) != "" {
				line += " reason=" + report.GapReason
			}
			fmt.Fprintln(&b, line)
		}
	}
	if len(campaign.CoverageGaps) > 0 {
		fmt.Fprintf(&b, "Coverage gaps:\n")
		for _, gap := range limitFuzzCampaignCoverageGaps(campaign.CoverageGaps, 8) {
			line := fmt.Sprintf("- %s boost=%d", gap.ID, gap.PriorityBoost)
			if strings.TrimSpace(gap.Target) != "" {
				line += " target=" + gap.Target
			}
			if strings.TrimSpace(gap.SourceAnchor) != "" {
				line += " source=" + filepath.ToSlash(gap.SourceAnchor)
			}
			if strings.TrimSpace(gap.Reason) != "" {
				line += " reason=" + gap.Reason
			}
			fmt.Fprintln(&b, line)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func limitFuzzCampaignSeedTargets(items []FuzzCampaignSeedTarget, limit int) []FuzzCampaignSeedTarget {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitFuzzCampaignSeedArtifacts(items []FuzzCampaignSeedArtifact, limit int) []FuzzCampaignSeedArtifact {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitFuzzCampaignNativeResults(items []FuzzCampaignNativeResult, limit int) []FuzzCampaignNativeResult {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitFuzzCampaignFindings(items []FuzzCampaignFinding, limit int) []FuzzCampaignFinding {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitFuzzCampaignCoverageGaps(items []FuzzCampaignCoverageGap, limit int) []FuzzCampaignCoverageGap {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitFuzzCampaignCoverageReports(items []FuzzCampaignCoverageReport, limit int) []FuzzCampaignCoverageReport {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitFuzzCampaignRunArtifacts(items []FuzzCampaignRunArtifact, limit int) []FuzzCampaignRunArtifact {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func fuzzCampaignHasSeedArtifactForRun(campaign FuzzCampaign, runID string) bool {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return false
	}
	for _, artifact := range campaign.SeedArtifacts {
		if strings.EqualFold(strings.TrimSpace(artifact.RunID), runID) {
			return true
		}
	}
	return false
}

func (rt *runtimeState) recentFuzzCampaignIDs() []string {
	if rt == nil || rt.fuzzCampaigns == nil {
		return nil
	}
	items, err := rt.fuzzCampaigns.ListRecent(workspaceSnapshotRoot(rt.workspace), 12)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}
