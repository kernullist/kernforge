package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultSourceScanMaxRuns       = 100
	defaultSourceScanMaxCandidates = 4000
	sourceCandidateStatusPending   = "pending"
	sourceCandidateStatusStale     = "stale"
)

type SourceCandidateEvidenceSpan struct {
	Kind      string `json:"kind"`
	File      string `json:"file,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Text      string `json:"text,omitempty"`
}

type SourceCandidateFact struct {
	Kind   string `json:"kind"`
	Line   int    `json:"line,omitempty"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type SourceCandidateAnalysisEntry struct {
	RunID        string    `json:"run_id,omitempty"`
	FuzzRunID    string    `json:"fuzz_run_id,omitempty"`
	Summary      string    `json:"summary,omitempty"`
	Evidence     []string  `json:"evidence,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	Analyzer     string    `json:"analyzer,omitempty"`
	ArtifactPath string    `json:"artifact_path,omitempty"`
}

type SourceCandidateRevalidation struct {
	Verdict         string    `json:"verdict"`
	Reason          string    `json:"reason,omitempty"`
	Evidence        []string  `json:"evidence,omitempty"`
	FuzzRunID       string    `json:"fuzz_run_id,omitempty"`
	CampaignID      string    `json:"campaign_id,omitempty"`
	NativeResultKey string    `json:"native_result_key,omitempty"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
}

type SourceCandidateRecord struct {
	ID                  string                         `json:"id"`
	Workspace           string                         `json:"workspace"`
	RunID               string                         `json:"run_id,omitempty"`
	Status              string                         `json:"status,omitempty"`
	MatcherSlug         string                         `json:"matcher_slug"`
	MatcherDescription  string                         `json:"matcher_description,omitempty"`
	NoiseTier           string                         `json:"noise_tier,omitempty"`
	SeverityHint        string                         `json:"severity_hint,omitempty"`
	ProjectTypes        []string                       `json:"project_types,omitempty"`
	File                string                         `json:"file"`
	LineNumbers         []int                          `json:"line_numbers,omitempty"`
	Snippet             string                         `json:"snippet,omitempty"`
	MatchedPattern      string                         `json:"matched_pattern,omitempty"`
	SymbolID            string                         `json:"symbol_id,omitempty"`
	SymbolName          string                         `json:"symbol_name,omitempty"`
	SymbolKind          string                         `json:"symbol_kind,omitempty"`
	SourceAnchor        string                         `json:"source_anchor,omitempty"`
	Score               int                            `json:"score,omitempty"`
	ConfidenceBreakdown map[string]int                 `json:"confidence_breakdown,omitempty"`
	EvidenceSpans       []SourceCandidateEvidenceSpan  `json:"evidence_spans,omitempty"`
	NegativeEvidence    []string                       `json:"negative_evidence,omitempty"`
	FileContentHash     string                         `json:"file_content_hash,omitempty"`
	CurrentFileHash     string                         `json:"current_file_hash,omitempty"`
	SymbolSignatureHash string                         `json:"symbol_signature_hash,omitempty"`
	CurrentSymbolHash   string                         `json:"current_symbol_hash,omitempty"`
	Stale               bool                           `json:"stale,omitempty"`
	StaleReason         string                         `json:"stale_reason,omitempty"`
	DataflowFacts       []SourceCandidateFact          `json:"dataflow_facts,omitempty"`
	ControlflowFacts    []SourceCandidateFact          `json:"controlflow_facts,omitempty"`
	Reasons             []string                       `json:"reasons,omitempty"`
	Tags                []string                       `json:"tags,omitempty"`
	CreatedAt           time.Time                      `json:"created_at,omitempty"`
	UpdatedAt           time.Time                      `json:"updated_at,omitempty"`
	AnalysisHistory     []SourceCandidateAnalysisEntry `json:"analysis_history,omitempty"`
	RevalidationHistory []SourceCandidateRevalidation  `json:"revalidation_history,omitempty"`
	LinkedFuzzRunIDs    []string                       `json:"linked_fuzz_run_ids,omitempty"`
	LinkedCampaignIDs   []string                       `json:"linked_campaign_ids,omitempty"`
	FeedbackDraftPaths  []string                       `json:"feedback_draft_paths,omitempty"`
}

type SourceScanRun struct {
	ID             string            `json:"id"`
	Workspace      string            `json:"workspace"`
	Goal           string            `json:"goal,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	CandidateCount int               `json:"candidate_count,omitempty"`
	ByMatcher      map[string]int    `json:"by_matcher,omitempty"`
	CandidateIDs   []string          `json:"candidate_ids,omitempty"`
	ArtifactDir    string            `json:"artifact_dir,omitempty"`
	ManifestPath   string            `json:"manifest_path,omitempty"`
	ReportPath     string            `json:"report_path,omitempty"`
	Notes          []string          `json:"notes,omitempty"`
	Options        SourceScanOptions `json:"options,omitempty"`
}

type SourceScanOptions struct {
	Limit     int      `json:"limit,omitempty"`
	OnlySlugs []string `json:"only_slugs,omitempty"`
	SkipSlugs []string `json:"skip_slugs,omitempty"`
	Filter    string   `json:"filter,omitempty"`
	Files     []string `json:"files,omitempty"`
}

type SourceScanStore struct {
	Path          string
	MaxRuns       int
	MaxCandidates int
}

type sourceScanStoreFile struct {
	Runs       []SourceScanRun         `json:"runs,omitempty"`
	Candidates []SourceCandidateRecord `json:"candidates,omitempty"`
}

type sourceMatcher struct {
	Slug              string
	Description       string
	NoiseTier         string
	SeverityHint      string
	ProjectTypes      []string
	Tags              []string
	FileExtensions    []string
	RequiredAnyGroups [][]string
	LineNeedles       []string
	MatchedPattern    string
	BaseScore         int
	Reason            string
}

type sourceMatchContext struct {
	Root            string
	RunID           string
	ProjectTypes    []string
	File            FileRecord
	Symbols         []SymbolRecord
	Content         string
	FullContent     string
	Lines           []string
	WindowStartLine int
	WindowEndLine   int
	WindowSymbol    SymbolRecord
	HasWindowSymbol bool
}

type sourceScanMatch struct {
	Line           int
	Snippet        string
	MatchedPattern string
	Symbol         SymbolRecord
	HasSymbol      bool
	Reasons        []string
}

func NewSourceScanStore() *SourceScanStore {
	return &SourceScanStore{
		Path:          filepath.Join(userConfigDir(), "source_scan.json"),
		MaxRuns:       defaultSourceScanMaxRuns,
		MaxCandidates: defaultSourceScanMaxCandidates,
	}
}

func (s *SourceScanStore) UpsertRunWithCandidates(run SourceScanRun, candidates []SourceCandidateRecord) (SourceScanRun, []SourceCandidateRecord, error) {
	if s == nil {
		return SourceScanRun{}, nil, nil
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	state, err := s.load()
	if err != nil {
		return SourceScanRun{}, nil, err
	}
	run = normalizeSourceScanRun(run)
	normalized := make([]SourceCandidateRecord, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.RunID = firstNonBlankString(candidate.RunID, run.ID)
		candidate.Workspace = firstNonBlankString(candidate.Workspace, run.Workspace)
		normalized = append(normalized, normalizeSourceCandidateRecord(candidate))
	}
	run.CandidateCount = len(normalized)
	run.ByMatcher = sourceScanMatcherCounts(normalized)
	run.CandidateIDs = sourceCandidateIDs(normalized)
	state.Runs = upsertSourceScanRun(state.Runs, run)
	state.Candidates = upsertSourceCandidates(state.Candidates, normalized)
	state = s.trim(state)
	if err := s.save(state); err != nil {
		return SourceScanRun{}, nil, err
	}
	return run, normalized, nil
}

func (s *SourceScanStore) UpsertCandidate(candidate SourceCandidateRecord) (SourceCandidateRecord, error) {
	if s == nil {
		return SourceCandidateRecord{}, nil
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	state, err := s.load()
	if err != nil {
		return SourceCandidateRecord{}, err
	}
	candidate = normalizeSourceCandidateRecord(candidate)
	state.Candidates = upsertSourceCandidates(state.Candidates, []SourceCandidateRecord{candidate})
	state = s.trim(state)
	if err := s.save(state); err != nil {
		return SourceCandidateRecord{}, err
	}
	return candidate, nil
}

func (s *SourceScanStore) ListRuns(workspace string, limit int) ([]SourceScanRun, error) {
	state, err := s.load()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	out := []SourceScanRun{}
	for i := len(state.Runs) - 1; i >= 0; i-- {
		item := normalizeSourceScanRun(state.Runs[i])
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

func (s *SourceScanStore) ListCandidates(workspace string, limit int) ([]SourceCandidateRecord, error) {
	state, err := s.load()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	items := append([]SourceCandidateRecord(nil), state.Candidates...)
	sort.Slice(items, func(i int, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].Score > items[j].Score
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	out := []SourceCandidateRecord{}
	for _, item := range items {
		item = normalizeSourceCandidateRecord(item)
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

func (s *SourceScanStore) GetCandidate(id string) (SourceCandidateRecord, bool, error) {
	return s.GetCandidateForWorkspace(id, "")
}

func (s *SourceScanStore) GetCandidateForWorkspace(id string, workspace string) (SourceCandidateRecord, bool, error) {
	state, err := s.load()
	if err != nil {
		return SourceCandidateRecord{}, false, err
	}
	query := strings.ToLower(strings.TrimSpace(id))
	if query == "" || query == "latest" {
		items, err := s.ListCandidates(workspace, 1)
		if err != nil || len(items) == 0 {
			return SourceCandidateRecord{}, false, err
		}
		return items[0], true, nil
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	for _, item := range state.Candidates {
		item = normalizeSourceCandidateRecord(item)
		if workspace != "" && workspaceAffinityScore(workspace, item.Workspace) == 0 {
			continue
		}
		lowerID := strings.ToLower(item.ID)
		if lowerID == query || strings.HasPrefix(lowerID, query) {
			return item, true, nil
		}
	}
	return SourceCandidateRecord{}, false, nil
}

func (s *SourceScanStore) Stats(workspace string) (int, time.Time, error) {
	items, err := s.ListCandidates(workspace, defaultSourceScanMaxCandidates)
	if err != nil {
		return 0, time.Time{}, err
	}
	var last time.Time
	for _, item := range items {
		if item.UpdatedAt.After(last) {
			last = item.UpdatedAt
		}
	}
	return len(items), last, nil
}

func (s *SourceScanStore) load() (sourceScanStoreFile, error) {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return sourceScanStoreFile{}, nil
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return sourceScanStoreFile{}, nil
		}
		return sourceScanStoreFile{}, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return sourceScanStoreFile{}, nil
	}
	var state sourceScanStoreFile
	if err := json.Unmarshal(data, &state); err != nil {
		return sourceScanStoreFile{}, err
	}
	for i := range state.Runs {
		state.Runs[i] = normalizeSourceScanRun(state.Runs[i])
	}
	for i := range state.Candidates {
		state.Candidates[i] = normalizeSourceCandidateRecord(state.Candidates[i])
	}
	return state, nil
}

func (s *SourceScanStore) save(state sourceScanStoreFile) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(s.Path, append(data, '\n'), 0o644)
}

func (s *SourceScanStore) trim(state sourceScanStoreFile) sourceScanStoreFile {
	maxRuns := s.MaxRuns
	if maxRuns <= 0 {
		maxRuns = defaultSourceScanMaxRuns
	}
	maxCandidates := s.MaxCandidates
	if maxCandidates <= 0 {
		maxCandidates = defaultSourceScanMaxCandidates
	}
	if len(state.Runs) > maxRuns {
		state.Runs = append([]SourceScanRun(nil), state.Runs[len(state.Runs)-maxRuns:]...)
	}
	if len(state.Candidates) > maxCandidates {
		sort.Slice(state.Candidates, func(i int, j int) bool {
			if state.Candidates[i].UpdatedAt.Equal(state.Candidates[j].UpdatedAt) {
				return state.Candidates[i].Score > state.Candidates[j].Score
			}
			return state.Candidates[i].UpdatedAt.After(state.Candidates[j].UpdatedAt)
		})
		state.Candidates = append([]SourceCandidateRecord(nil), state.Candidates[:maxCandidates]...)
	}
	return state
}

func (rt *runtimeState) handleSourceScanCommand(args string) error {
	if rt == nil || rt.sourceScan == nil {
		return fmt.Errorf("source scan store is not configured")
	}
	trimmed := strings.TrimSpace(args)
	if trimmed == "" || strings.EqualFold(trimmed, "status") {
		return rt.showSourceScanStatus()
	}
	fields := splitAnalysisCommandLine(trimmed)
	if len(fields) == 0 {
		return rt.showSourceScanStatus()
	}
	head := strings.ToLower(strings.TrimSpace(fields[0]))
	switch head {
	case "run", "scan", "start":
		return rt.runSourceScan(strings.TrimSpace(trimmed[len(fields[0]):]))
	case "list":
		return rt.listSourceCandidates()
	case "show":
		id := "latest"
		if len(fields) > 1 {
			id = fields[1]
		}
		return rt.showSourceCandidate(id)
	case "revalidate", "review":
		return rt.revalidateSourceCandidateCommand(strings.TrimSpace(trimmed[len(fields[0]):]))
	default:
		if strings.HasPrefix(head, "--") {
			return rt.runSourceScan(trimmed)
		}
		return fmt.Errorf("usage: /source-scan [status|run [--limit N] [--only-slugs a,b] [--skip-slugs a,b] [--filter text] [--files path1,path2]|list|show <id|latest>|revalidate <id|latest>]")
	}
}

func (rt *runtimeState) showSourceScanStatus() error {
	root := workspaceSnapshotRoot(rt.workspace)
	count, last, err := rt.sourceScan.Stats(root)
	if err != nil {
		return err
	}
	runs, err := rt.sourceScan.ListRuns(root, 1)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Source Scan"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("candidates", strconv.Itoa(count)))
	if !last.IsZero() {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("last_updated", last.Format(time.RFC3339)))
	}
	if len(runs) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("latest_run", runs[0].ID))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("latest_candidates", strconv.Itoa(runs[0].CandidateCount)))
	}
	fmt.Fprintln(rt.writer, rt.ui.statusKV("usage", "/source-scan run [--limit N] [--only-slugs slug1,slug2] [--files path1,path2]"))
	if candidates, listErr := rt.sourceScan.ListCandidates(root, 1); listErr == nil && len(candidates) > 0 {
		fmt.Fprintln(rt.writer, renderSourceScanFuzzHandoff(candidates[0]))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("handoff", "/fuzz-func --from-candidate <candidate-id>"))
	}
	return nil
}

func (rt *runtimeState) runSourceScan(args string) error {
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("workspace root is not available")
	}
	options, err := parseSourceScanOptions(args)
	if err != nil {
		return err
	}
	runID := "source-scan-" + time.Now().Format("20060102-150405")
	artifacts, notes, err := prepareFunctionFuzzArtifactsForPlanning(rt.cfg, root, "source scan")
	if err != nil {
		return err
	}
	if !hasSemanticIndexV2Data(artifacts.IndexV2) {
		return fmt.Errorf("source scan could not build a semantic index")
	}
	candidates := buildSourceScanCandidates(root, runID, artifacts.IndexV2, options)
	run := SourceScanRun{
		ID:        runID,
		Workspace: root,
		Goal:      "source candidate scan for fuzz target discovery",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Options:   options,
		Notes:     notes,
	}
	if err := writeSourceScanArtifacts(root, &run, candidates); err != nil {
		return err
	}
	savedRun, savedCandidates, err := rt.sourceScan.UpsertRunWithCandidates(run, candidates)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Source Scan"))
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Captured %d source candidate(s).", len(savedCandidates))))
	fmt.Fprintln(rt.writer, renderSourceScanRun(savedRun, savedCandidates))
	return nil
}

func (rt *runtimeState) listSourceCandidates() error {
	items, err := rt.sourceScan.ListCandidates(workspaceSnapshotRoot(rt.workspace), 12)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Source Candidates"))
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No source candidates found for this workspace."))
		return nil
	}
	for _, item := range items {
		fmt.Fprintf(rt.writer, "- %s  score=%d  tier=%s  matcher=%s  target=%s  file=%s\n",
			rt.ui.dim(item.ID),
			item.Score,
			valueOrUnset(item.NoiseTier),
			valueOrUnset(item.MatcherSlug),
			valueOrUnset(firstNonBlankString(item.SymbolName, item.SourceAnchor)),
			valueOrUnset(item.File))
	}
	fmt.Fprintln(rt.writer, renderSourceScanFuzzHandoff(items[0]))
	return nil
}

func (rt *runtimeState) showSourceCandidate(id string) error {
	item, ok, err := rt.sourceScan.GetCandidateForWorkspace(id, workspaceSnapshotRoot(rt.workspace))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("source candidate not found: %s", id)
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Source Candidate"))
	fmt.Fprintln(rt.writer, renderSourceCandidate(item))
	return nil
}

func (rt *runtimeState) revalidateSourceCandidateCommand(args string) error {
	fields := splitAnalysisCommandLine(strings.TrimSpace(args))
	id := "latest"
	manualVerdict := ""
	manualReason := ""
	if len(fields) > 0 {
		id = fields[0]
	}
	for i := 1; i < len(fields); i++ {
		switch strings.ToLower(fields[i]) {
		case "--verdict":
			if i+1 >= len(fields) {
				return fmt.Errorf("--verdict requires a value")
			}
			manualVerdict = fields[i+1]
			i++
		case "--reason":
			if i+1 >= len(fields) {
				return fmt.Errorf("--reason requires a value")
			}
			manualReason = fields[i+1]
			i++
		}
	}
	item, ok, err := rt.sourceScan.GetCandidateForWorkspace(id, workspaceSnapshotRoot(rt.workspace))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("source candidate not found: %s", id)
	}
	updated, verdict := rt.revalidateSourceCandidate(item, manualVerdict, manualReason)
	if _, err := rt.sourceScan.UpsertCandidate(updated); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Source Candidate Revalidation"))
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("%s -> %s", updated.ID, verdict.Verdict)))
	fmt.Fprintln(rt.writer, verdict.Reason)
	fmt.Fprintln(rt.writer, renderSourceScanFuzzHandoff(updated))
	return nil
}

func parseSourceScanOptions(args string) (SourceScanOptions, error) {
	fields := splitAnalysisCommandLine(strings.TrimSpace(args))
	options := SourceScanOptions{}
	for i := 0; i < len(fields); i++ {
		token := strings.TrimSpace(fields[i])
		switch strings.ToLower(token) {
		case "--limit":
			if i+1 >= len(fields) {
				return options, fmt.Errorf("--limit requires a value")
			}
			value, err := strconv.Atoi(fields[i+1])
			if err != nil || value < 0 {
				return options, fmt.Errorf("invalid --limit value: %s", fields[i+1])
			}
			options.Limit = value
			i++
		case "--only-slugs", "--only":
			if i+1 >= len(fields) {
				return options, fmt.Errorf("%s requires a value", token)
			}
			options.OnlySlugs = splitSourceScanCSV(fields[i+1])
			i++
		case "--skip-slugs", "--skip":
			if i+1 >= len(fields) {
				return options, fmt.Errorf("%s requires a value", token)
			}
			options.SkipSlugs = splitSourceScanCSV(fields[i+1])
			i++
		case "--filter":
			if i+1 >= len(fields) {
				return options, fmt.Errorf("--filter requires a value")
			}
			options.Filter = strings.TrimSpace(fields[i+1])
			i++
		case "--files", "--file":
			if i+1 >= len(fields) {
				return options, fmt.Errorf("%s requires a value", token)
			}
			options.Files = splitSourceScanFiles(fields[i+1])
			i++
		default:
			return options, fmt.Errorf("unsupported /source-scan option: %s", token)
		}
	}
	return options, nil
}

func splitSourceScanCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := []string{}
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return uniqueStrings(out)
}

func splitSourceScanFiles(value string) []string {
	parts := strings.Split(value, ",")
	out := []string{}
	for _, part := range parts {
		part = functionFuzzNormalizeOptionalPath(strings.TrimSpace(part))
		part = strings.TrimPrefix(part, "./")
		if part != "" {
			out = append(out, part)
		}
	}
	return uniqueStrings(out)
}

func buildSourceScanCandidates(root string, runID string, index SemanticIndexV2, options SourceScanOptions) []SourceCandidateRecord {
	matchers := filterSourceMatchers(defaultSourceMatchers(), options)
	if len(matchers) == 0 {
		return nil
	}
	symbolsByFile := sourceScanSymbolsByFile(index.Symbols)
	seen := map[string]SourceCandidateRecord{}
	fileSet := sourceScanOptionFileSet(options.Files)
	files := sourceScanFilesForOptions(index.Files, options.Files)
	scanIndex := index
	scanIndex.Files = files
	projectTypes := sourceScanProjectTypes(scanIndex)
	sort.Slice(files, func(i int, j int) bool {
		return files[i].Path < files[j].Path
	})
	for _, file := range files {
		file.Path = filepath.ToSlash(strings.TrimSpace(file.Path))
		if file.Path == "" || !sourceScanFileLooksSupported(file) {
			continue
		}
		if len(fileSet) > 0 && !sourceScanOptionAllowsFile(fileSet, file.Path) {
			continue
		}
		if options.Filter != "" && !sourceScanFileMatchesFilter(file, symbolsByFile[file.Path], options.Filter) {
			continue
		}
		content, ok := readSourceScanFile(root, file.Path)
		if !ok {
			continue
		}
		lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
		ctx := sourceMatchContext{
			Root:         root,
			RunID:        runID,
			ProjectTypes: projectTypes,
			File:         file,
			Symbols:      symbolsByFile[file.Path],
			Content:      content,
			FullContent:  content,
			Lines:        lines,
		}
		for _, window := range sourceScanMatchContextsForFile(ctx) {
			for _, matcher := range matchers {
				if !matcher.matchesFile(file) || !matcher.matchesProject(projectTypes) {
					continue
				}
				matches := matcher.match(window)
				for _, match := range matches {
					candidate := matcher.toCandidate(window, match)
					if candidate.ID == "" {
						continue
					}
					existing, ok := seen[candidate.ID]
					if ok && existing.Score >= candidate.Score {
						continue
					}
					seen[candidate.ID] = candidate
				}
			}
		}
	}
	out := make([]SourceCandidateRecord, 0, len(seen))
	for _, item := range seen {
		out = append(out, normalizeSourceCandidateRecord(item))
	}
	sort.Slice(out, func(i int, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		left := out[i].MatcherSlug + "|" + out[i].File + "|" + out[i].SymbolName
		right := out[j].MatcherSlug + "|" + out[j].File + "|" + out[j].SymbolName
		return left < right
	})
	if options.Limit > 0 && len(out) > options.Limit {
		out = append([]SourceCandidateRecord(nil), out[:options.Limit]...)
	}
	return out
}

func sourceScanMatchContextsForFile(ctx sourceMatchContext) []sourceMatchContext {
	ctx.FullContent = firstNonBlankString(ctx.FullContent, ctx.Content)
	out := []sourceMatchContext{}
	symbols := append([]SymbolRecord(nil), ctx.Symbols...)
	sort.Slice(symbols, func(i int, j int) bool {
		if symbols[i].StartLine == symbols[j].StartLine {
			return symbols[i].Name < symbols[j].Name
		}
		return symbols[i].StartLine < symbols[j].StartLine
	})
	for index, symbol := range symbols {
		symbol.File = functionFuzzNormalizeOptionalPath(firstNonBlankString(symbol.File, ctx.File.Path))
		if symbol.StartLine <= 0 || symbol.EndLine < symbol.StartLine {
			continue
		}
		start := symbol.StartLine - 8
		if start < 1 {
			start = 1
		}
		if index > 0 && symbols[index-1].EndLine > 0 && start <= symbols[index-1].EndLine {
			start = symbols[index-1].EndLine + 1
		}
		end := symbol.EndLine + 8
		if end > len(ctx.Lines) {
			end = len(ctx.Lines)
		}
		if index+1 < len(symbols) && symbols[index+1].StartLine > 0 && end >= symbols[index+1].StartLine {
			end = symbols[index+1].StartLine - 1
		}
		window := ctx
		window.WindowStartLine = start
		window.WindowEndLine = end
		window.WindowSymbol = symbol
		window.HasWindowSymbol = true
		window.Content = sourceScanLinesInRange(ctx.Lines, start, end)
		out = append(out, window)
	}
	if len(out) == 0 {
		ctx.WindowStartLine = 1
		ctx.WindowEndLine = len(ctx.Lines)
		ctx.Content = firstNonBlankString(ctx.Content, ctx.FullContent)
		out = append(out, ctx)
	}
	return out
}

func sourceScanFilesForOptions(files []FileRecord, optionFiles []string) []FileRecord {
	out := append([]FileRecord(nil), files...)
	seen := map[string]struct{}{}
	for _, file := range out {
		normalized := strings.ToLower(functionFuzzNormalizeOptionalPath(file.Path))
		if normalized != "" {
			seen[normalized] = struct{}{}
		}
	}
	for _, file := range optionFiles {
		normalized := functionFuzzNormalizeOptionalPath(file)
		normalized = strings.TrimPrefix(normalized, "./")
		if normalized == "" {
			continue
		}
		key := strings.ToLower(normalized)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ext := strings.ToLower(filepath.Ext(normalized))
		out = append(out, FileRecord{
			Path:      normalized,
			Extension: ext,
			Language:  analysisLanguageForExtension(ext),
		})
	}
	return out
}

func sourceScanOptionFileSet(files []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, file := range files {
		normalized := functionFuzzNormalizeOptionalPath(file)
		normalized = strings.TrimPrefix(normalized, "./")
		if normalized == "" {
			continue
		}
		out[strings.ToLower(normalized)] = struct{}{}
	}
	return out
}

func sourceScanOptionAllowsFile(files map[string]struct{}, path string) bool {
	normalized := strings.ToLower(functionFuzzNormalizeOptionalPath(path))
	if normalized == "" {
		return false
	}
	if _, ok := files[normalized]; ok {
		return true
	}
	for item := range files {
		if strings.HasSuffix(normalized, "/"+item) || strings.HasSuffix(item, "/"+normalized) {
			return true
		}
	}
	return false
}

func defaultSourceMatchers() []sourceMatcher {
	return []sourceMatcher{
		{
			Slug:              "windows-kernel-method-neither-user-buffer",
			Description:       "METHOD_NEITHER or direct user pointer paths that must prove probe, size, and exception boundaries.",
			NoiseTier:         "precise",
			SeverityHint:      "high",
			ProjectTypes:      []string{"windows_driver", "cpp"},
			Tags:              []string{"windows_kernel", "ioctl", "user_buffer"},
			FileExtensions:    []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".inl"},
			RequiredAnyGroups: [][]string{{"method_neither", "type3inputbuffer", "userbuffer"}, {"probeforread", "probeforwrite", "rtlcopymemory", "memcpy", "copyfrom"}},
			LineNeedles:       []string{"method_neither", "type3inputbuffer", "userbuffer", "probeforread", "rtlcopymemory"},
			MatchedPattern:    "METHOD_NEITHER/user pointer with probe or copy sink",
			BaseScore:         82,
			Reason:            "direct user pointer path needs strict probe, size, and SEH discipline",
		},
		{
			Slug:              "ioctl-dispatch-selector",
			Description:       "IOCTL selector and dispatch surfaces that should seed command-shape fuzzing.",
			NoiseTier:         "noisy",
			SeverityHint:      "medium",
			ProjectTypes:      []string{"windows_driver", "cpp"},
			Tags:              []string{"windows_kernel", "ioctl", "dispatch"},
			FileExtensions:    []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp"},
			RequiredAnyGroups: [][]string{{"irp_mj_device_control", "ioctl", "deviceiocontrol", "ctl_code"}, {"switch", "case", "iocode", "iocontrolcode", "controlcode"}},
			LineNeedles:       []string{"irp_mj_device_control", "iocontrolcode", "ctl_code", "deviceiocontrol", "switch"},
			MatchedPattern:    "IOCTL dispatch selector",
			BaseScore:         58,
			Reason:            "IOCTL selector is an input-facing fuzz target even when no precise bug signal is visible",
		},
		{
			Slug:              "probe-copy-size-drift",
			Description:       "Probe and copy operations in the same scope, useful for size-contract drift checks.",
			NoiseTier:         "precise",
			SeverityHint:      "high",
			ProjectTypes:      []string{"windows_driver", "cpp"},
			Tags:              []string{"windows_kernel", "bounds", "copy"},
			FileExtensions:    []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp"},
			RequiredAnyGroups: [][]string{{"probeforread", "probeforwrite"}, {"rtlcopymemory", "memcpy", "copy_memory", "copyfrom"}, {"size", "length", "inputbufferlength", "outputbufferlength", "bytes"}},
			LineNeedles:       []string{"probeforread", "probeforwrite", "rtlcopymemory", "memcpy", "inputbufferlength"},
			MatchedPattern:    "probe/copy size drift",
			BaseScore:         86,
			Reason:            "probe and copy sinks must share the same trusted size contract",
		},
		{
			Slug:              "size-contract-drift",
			Description:       "Validation, allocation, and copy code that may use different length variables.",
			NoiseTier:         "normal",
			SeverityHint:      "medium",
			ProjectTypes:      []string{"cpp", "windows_driver", "unreal"},
			Tags:              []string{"bounds", "allocation", "copy"},
			FileExtensions:    []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".inl"},
			RequiredAnyGroups: [][]string{{"size", "length", "count", "bytes"}, {"if", "assert", "validate", "check"}, {"memcpy", "rtlcopymemory", "copy", "new ", "malloc", "exallocatepool"}},
			LineNeedles:       []string{"size", "length", "memcpy", "rtlcopymemory", "exallocatepool", "validate"},
			MatchedPattern:    "size validation and sink relation",
			BaseScore:         68,
			Reason:            "size validation and memory sinks should be checked for variable drift",
		},
		{
			Slug:              "irql-paged-memory",
			Description:       "IRQL-sensitive paths that mention pageable code or high-IRQL execution.",
			NoiseTier:         "normal",
			SeverityHint:      "medium",
			ProjectTypes:      []string{"windows_driver", "cpp"},
			Tags:              []string{"windows_kernel", "irql", "lifetime"},
			FileExtensions:    []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp"},
			RequiredAnyGroups: [][]string{{"dispatch_level", "keacquirespinlock", "keraiseirql", "dpc", "paged_code"}, {"paged_code", "pagable", "page_code", "alloc_text"}},
			LineNeedles:       []string{"dispatch_level", "paged_code", "alloc_text", "keacquirespinlock", "dpc"},
			MatchedPattern:    "IRQL and pageable memory tension",
			BaseScore:         62,
			Reason:            "high-IRQL paths must not touch pageable code or unsafe memory lifetimes",
		},
		{
			Slug:              "object-callback-handle-access",
			Description:       "Object callback access-masking logic for handle protection bypass analysis.",
			NoiseTier:         "normal",
			SeverityHint:      "high",
			ProjectTypes:      []string{"windows_driver", "cpp"},
			Tags:              []string{"windows_kernel", "object_callback", "handle_access"},
			FileExtensions:    []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp"},
			RequiredAnyGroups: [][]string{{"obregistercallbacks", "ob_pre_operation_information", "oboperationhandlecreate", "oboperationhandleduplicate"}, {"desiredaccess", "originaldesiredaccess", "psprocesstype", "psthreadtype"}},
			LineNeedles:       []string{"obregistercallbacks", "desiredaccess", "originaldesiredaccess", "ob_pre_operation_information"},
			MatchedPattern:    "object callback handle access mutation",
			BaseScore:         76,
			Reason:            "handle access callbacks are a privileged policy boundary",
		},
		{
			Slug:              "process-notify-lifetime-race",
			Description:       "Process/thread/image notify callbacks that publish shared process state.",
			NoiseTier:         "normal",
			SeverityHint:      "medium",
			ProjectTypes:      []string{"windows_driver", "cpp"},
			Tags:              []string{"windows_kernel", "callback", "race"},
			FileExtensions:    []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp"},
			RequiredAnyGroups: [][]string{{"pssetcreateprocessnotifyroutine", "pssetcreateprocessnotifyroutineex", "pssetcreatethreadnotifyroutine", "pssetloadimagenotifyroutine"}, {"list", "map", "table", "state", "lock", "mutex", "pushlock", "eresource"}},
			LineNeedles:       []string{"pssetcreateprocessnotifyroutine", "pssetloadimagenotifyroutine", "pushlock", "state", "list"},
			MatchedPattern:    "process notify shared-state lifetime",
			BaseScore:         70,
			Reason:            "notify callbacks often race process teardown and shared state publication",
		},
		{
			Slug:              "minifilter-context-cleanup",
			Description:       "Minifilter callback context ownership and cleanup paths.",
			NoiseTier:         "normal",
			SeverityHint:      "medium",
			ProjectTypes:      []string{"windows_driver", "cpp"},
			Tags:              []string{"windows_kernel", "minifilter", "cleanup"},
			FileExtensions:    []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp"},
			RequiredAnyGroups: [][]string{{"fltregisterfilter", "pflt_callback_data", "fltsetstreamcontext", "fltsetfilecontext", "completioncontext"}, {"fltreleasecontext", "cleanup", "postoperation", "preoperation", "deletecontext"}},
			LineNeedles:       []string{"pflt_callback_data", "completioncontext", "fltreleasecontext", "fltsetstreamcontext"},
			MatchedPattern:    "minifilter context ownership",
			BaseScore:         66,
			Reason:            "minifilter context ownership bugs are usually path-sensitive and fuzzable with operation sequencing",
		},
		{
			Slug:              "double-fetch-user-buffer",
			Description:       "User-buffer probe followed by later pointer dereference or copy, a classic double-fetch surface.",
			NoiseTier:         "precise",
			SeverityHint:      "high",
			ProjectTypes:      []string{"windows_driver", "cpp"},
			Tags:              []string{"windows_kernel", "double_fetch", "user_buffer"},
			FileExtensions:    []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".inl"},
			RequiredAnyGroups: [][]string{{"probeforread", "probeforwrite"}, {"userbuffer", "type3inputbuffer", "irp->userbuffer", "usermode"}, {"->", "*", "rtlcopymemory", "memcpy"}},
			LineNeedles:       []string{"probeforread", "userbuffer", "type3inputbuffer", "rtlcopymemory", "memcpy"},
			MatchedPattern:    "probe followed by user-buffer reuse",
			BaseScore:         88,
			Reason:            "user memory must be captured once after probing; later dereference can become a double-fetch race",
		},
		{
			Slug:              "ioctl-output-infoleak",
			Description:       "IOCTL output paths that copy structures or buffers back to the caller and need initialization and length proof.",
			NoiseTier:         "precise",
			SeverityHint:      "high",
			ProjectTypes:      []string{"windows_driver", "cpp"},
			Tags:              []string{"windows_kernel", "ioctl", "infoleak", "output_buffer"},
			FileExtensions:    []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".inl"},
			RequiredAnyGroups: [][]string{{"outputbufferlength", "systembuffer", "userbuffer", "wdfrequestretrieveoutputbuffer"}, {"information", "iostatus.information", "bytesreturned"}, {"rtlcopymemory", "memcpy", "copy"}},
			LineNeedles:       []string{"outputbufferlength", "iostatus.information", "bytesreturned", "wdfrequestretrieveoutputbuffer", "rtlcopymemory"},
			MatchedPattern:    "IOCTL output buffer copy and Information length",
			BaseScore:         84,
			Reason:            "output buffer copies must prove initialized bytes and returned length agree",
		},
		{
			Slug:              "wdf-request-buffer-size-drift",
			Description:       "WDF request buffer retrieval where the retrieved length, requested size, and later copy size can diverge.",
			NoiseTier:         "precise",
			SeverityHint:      "high",
			ProjectTypes:      []string{"windows_driver", "cpp"},
			Tags:              []string{"windows_kernel", "wdf", "buffer", "bounds"},
			FileExtensions:    []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".inl"},
			RequiredAnyGroups: [][]string{{"wdfrequestretrieveinputbuffer", "wdfrequestretrieveoutputbuffer", "wdfrequestretrievememory"}, {"size", "length", "bytes", "buffersize"}, {"rtlcopymemory", "memcpy", "copy", "wdfmemorycopy"}},
			LineNeedles:       []string{"wdfrequestretrieveinputbuffer", "wdfrequestretrieveoutputbuffer", "wdfmemorycopy", "buffersize", "rtlcopymemory"},
			MatchedPattern:    "WDF request buffer size drift",
			BaseScore:         86,
			Reason:            "WDF buffer retrieval length and later sink length should remain the same trusted contract",
		},
		{
			Slug:              "integer-overflow-allocation",
			Description:       "Count/size arithmetic feeding allocation and copy paths.",
			NoiseTier:         "normal",
			SeverityHint:      "high",
			ProjectTypes:      []string{"windows_driver", "cpp", "unreal"},
			Tags:              []string{"integer_overflow", "allocation", "bounds"},
			FileExtensions:    []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".inl"},
			RequiredAnyGroups: [][]string{{"size", "count", "length", "bytes"}, {"*", "<<", "sizeof"}, {"exallocatepool", "malloc", "new ", "allocate", "realloc"}, {"memcpy", "rtlcopymemory", "copy", "read"}},
			LineNeedles:       []string{"sizeof", "exallocatepool", "malloc", "new ", "memcpy", "rtlcopymemory"},
			MatchedPattern:    "allocation size arithmetic feeding memory sink",
			BaseScore:         78,
			Reason:            "attacker-sized allocation arithmetic must be overflow-checked before allocation and copy",
		},
		{
			Slug:              "pool-lifetime-refcount",
			Description:       "Pool allocation, object reference, or shared-state publication with cleanup/refcount obligations.",
			NoiseTier:         "normal",
			SeverityHint:      "medium",
			ProjectTypes:      []string{"windows_driver", "cpp"},
			Tags:              []string{"windows_kernel", "pool", "lifetime", "refcount"},
			FileExtensions:    []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".inl"},
			RequiredAnyGroups: [][]string{{"exallocatepool", "obreferenceobject", "ioreference", "reference"}, {"exfreepool", "obdereferenceobject", "dereference", "cleanup", "goto"}, {"list", "table", "context", "state", "request"}},
			LineNeedles:       []string{"exallocatepool", "obreferenceobject", "exfreepool", "obdereferenceobject", "cleanup", "goto"},
			MatchedPattern:    "pool or object lifetime cleanup contract",
			BaseScore:         74,
			Reason:            "pool and object lifetime contracts need path-sensitive cleanup and refcount fuzzing",
		},
		{
			Slug:              "unreal-rpc-trust-boundary",
			Description:       "Unreal network RPC declarations that should prove authority and payload validation.",
			NoiseTier:         "normal",
			SeverityHint:      "medium",
			ProjectTypes:      []string{"unreal", "cpp"},
			Tags:              []string{"unreal", "rpc", "authority"},
			FileExtensions:    []string{".h", ".hpp", ".cpp", ".cc", ".cxx"},
			RequiredAnyGroups: [][]string{{"ufunction"}, {"server", "netmulticast", "client", "reliable", "unreliable"}},
			LineNeedles:       []string{"ufunction", "server", "netmulticast", "client"},
			MatchedPattern:    "Unreal RPC trust boundary",
			BaseScore:         64,
			Reason:            "network RPC entrypoints must enforce authority and input validation",
		},
		{
			Slug:              "telemetry-parser-untrusted-buffer",
			Description:       "Telemetry, pipe, socket, or ETW parsing paths that consume untrusted buffers.",
			NoiseTier:         "normal",
			SeverityHint:      "medium",
			ProjectTypes:      []string{"cpp", "windows_driver", "unreal"},
			Tags:              []string{"telemetry", "parser", "buffer"},
			FileExtensions:    []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".cs"},
			RequiredAnyGroups: [][]string{{"telemetry", "etw", "eventwrite", "pipe", "socket", "recv", "wsk", "tdi"}, {"parse", "decode", "deserialize", "unmarshal", "memcpy", "copy", "buffer"}},
			LineNeedles:       []string{"eventwrite", "telemetry", "pipe", "socket", "recv", "parse", "decode", "deserialize"},
			MatchedPattern:    "telemetry/parser untrusted buffer",
			BaseScore:         66,
			Reason:            "telemetry and IPC parsers are input-facing and benefit from source-only corpus generation",
		},
	}
}

func filterSourceMatchers(items []sourceMatcher, options SourceScanOptions) []sourceMatcher {
	only := map[string]struct{}{}
	for _, item := range options.OnlySlugs {
		only[strings.ToLower(strings.TrimSpace(item))] = struct{}{}
	}
	skip := map[string]struct{}{}
	for _, item := range options.SkipSlugs {
		skip[strings.ToLower(strings.TrimSpace(item))] = struct{}{}
	}
	out := []sourceMatcher{}
	for _, item := range items {
		slug := strings.ToLower(strings.TrimSpace(item.Slug))
		if len(only) > 0 {
			if _, ok := only[slug]; !ok {
				continue
			}
		}
		if _, ok := skip[slug]; ok {
			continue
		}
		out = append(out, item)
	}
	return out
}

func (m sourceMatcher) matchesFile(file FileRecord) bool {
	ext := strings.ToLower(strings.TrimSpace(file.Extension))
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(file.Path))
	}
	if len(m.FileExtensions) == 0 {
		return true
	}
	for _, candidate := range m.FileExtensions {
		if strings.EqualFold(ext, strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func (m sourceMatcher) matchesProject(projectTypes []string) bool {
	if len(m.ProjectTypes) == 0 {
		return true
	}
	have := map[string]struct{}{}
	for _, item := range projectTypes {
		have[strings.ToLower(strings.TrimSpace(item))] = struct{}{}
	}
	if _, ok := have["cpp"]; ok {
		for _, required := range m.ProjectTypes {
			if strings.EqualFold(strings.TrimSpace(required), "cpp") {
				return true
			}
		}
	}
	for _, required := range m.ProjectTypes {
		if _, ok := have[strings.ToLower(strings.TrimSpace(required))]; ok {
			return true
		}
	}
	return false
}

func (m sourceMatcher) match(ctx sourceMatchContext) []sourceScanMatch {
	lower := strings.ToLower(ctx.Content)
	for _, group := range m.RequiredAnyGroups {
		if !sourceScanContainsAny(lower, group) {
			return nil
		}
	}
	line := sourceScanEvidenceLineInRange(ctx.Lines, m.LineNeedles, ctx.WindowStartLine, ctx.WindowEndLine)
	if line <= 0 {
		line = ctx.WindowStartLine
		if line <= 0 {
			line = sourceScanEvidenceLine(ctx.Lines, m.LineNeedles)
		}
		if line <= 0 {
			line = 1
		}
	}
	symbol, hasSymbol := SymbolRecord{}, false
	if ctx.HasWindowSymbol {
		symbol = ctx.WindowSymbol
		hasSymbol = true
	}
	if !hasSymbol {
		symbol, hasSymbol = sourceScanNearestSymbol(ctx.Symbols, line)
	}
	return []sourceScanMatch{{
		Line:           line,
		Snippet:        sourceScanSnippet(ctx.Lines, line, 2),
		MatchedPattern: m.MatchedPattern,
		Symbol:         symbol,
		HasSymbol:      hasSymbol,
		Reasons:        []string{m.Reason},
	}}
}

func (m sourceMatcher) toCandidate(ctx sourceMatchContext, match sourceScanMatch) SourceCandidateRecord {
	now := time.Now()
	file := filepath.ToSlash(strings.TrimSpace(ctx.File.Path))
	candidate := SourceCandidateRecord{
		Workspace:           ctx.Root,
		RunID:               ctx.RunID,
		Status:              sourceCandidateStatusPending,
		MatcherSlug:         strings.TrimSpace(m.Slug),
		MatcherDescription:  strings.TrimSpace(m.Description),
		NoiseTier:           strings.ToLower(strings.TrimSpace(m.NoiseTier)),
		SeverityHint:        strings.ToLower(strings.TrimSpace(m.SeverityHint)),
		ProjectTypes:        uniqueStrings(m.ProjectTypes),
		File:                file,
		LineNumbers:         []int{match.Line},
		Snippet:             compactPersistentMemoryText(match.Snippet, 900),
		MatchedPattern:      strings.TrimSpace(match.MatchedPattern),
		SourceAnchor:        sourceCandidateAnchor(file, match.Line),
		Score:               sourceScanCandidateScore(m, ctx, match),
		ConfidenceBreakdown: sourceScanCandidateConfidenceBreakdown(m, ctx, match),
		EvidenceSpans:       sourceScanCandidateEvidenceSpans(m, ctx, match),
		NegativeEvidence:    sourceScanCandidateNegativeEvidence(ctx, match),
		FileContentHash:     sourceScanTextHash(firstNonBlankString(ctx.FullContent, ctx.Content)),
		CurrentFileHash:     sourceScanTextHash(firstNonBlankString(ctx.FullContent, ctx.Content)),
		DataflowFacts:       sourceScanCandidateDataflowFacts(ctx, match),
		ControlflowFacts:    sourceScanCandidateControlflowFacts(ctx, match),
		Reasons:             analysisUniqueStrings(match.Reasons),
		Tags:                uniqueStrings(m.Tags),
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if match.HasSymbol {
		candidate.SymbolID = strings.TrimSpace(match.Symbol.ID)
		candidate.SymbolName = firstNonBlankString(functionFuzzDisplayName(match.Symbol), match.Symbol.Name)
		candidate.SymbolKind = strings.TrimSpace(match.Symbol.Kind)
		candidate.SymbolSignatureHash = sourceScanSignatureHash(match.Symbol.Signature)
		candidate.CurrentSymbolHash = candidate.SymbolSignatureHash
		if candidate.SourceAnchor == "" {
			candidate.SourceAnchor = analysisFuzzSourceAnchor(match.Symbol)
		}
	}
	candidate.ID = sourceCandidateID(candidate)
	return normalizeSourceCandidateRecord(candidate)
}

func sourceScanCandidateScore(m sourceMatcher, ctx sourceMatchContext, match sourceScanMatch) int {
	score := m.BaseScore
	switch strings.ToLower(strings.TrimSpace(m.NoiseTier)) {
	case "precise":
		score += 10
	case "normal":
		score += 4
	case "noisy":
		score -= 8
	}
	if match.HasSymbol {
		params := buildFunctionFuzzParameterStrategies(match.Symbol.Signature)
		if functionFuzzSymbolLooksInputFacing(match.Symbol, params) {
			score += 10
		}
		if functionFuzzHasLengthBufferRelation(params) {
			score += 8
		}
		if functionFuzzHarnessReady(match.Symbol, params) {
			score += 6
		}
	}
	if containsAny(strings.ToLower(ctx.File.Path), "test", "mock", "sample", "example") {
		score -= 12
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score
}

func sourceScanCandidateConfidenceBreakdown(m sourceMatcher, ctx sourceMatchContext, match sourceScanMatch) map[string]int {
	out := map[string]int{
		"matcher_base": m.BaseScore,
	}
	switch strings.ToLower(strings.TrimSpace(m.NoiseTier)) {
	case "precise":
		out["noise_tier"] = 10
	case "normal":
		out["noise_tier"] = 4
	case "noisy":
		out["noise_tier"] = -8
	}
	if match.HasSymbol {
		out["symbol_window"] = 10
		params := buildFunctionFuzzParameterStrategies(match.Symbol.Signature)
		if functionFuzzSymbolLooksInputFacing(match.Symbol, params) {
			out["input_facing_symbol"] = 10
		}
		if functionFuzzHasLengthBufferRelation(params) {
			out["length_buffer_relation"] = 8
		}
		if functionFuzzHarnessReady(match.Symbol, params) {
			out["harness_ready_signature"] = 6
		}
	}
	if containsAny(strings.ToLower(ctx.File.Path), "test", "mock", "sample", "example") {
		out["test_or_sample_path"] = -12
	}
	if len(sourceScanCandidateDataflowFacts(ctx, match)) > 0 {
		out["dataflow_facts"] = 6
	}
	if len(sourceScanCandidateControlflowFacts(ctx, match)) > 0 {
		out["controlflow_facts"] = 4
	}
	return out
}

func sourceScanCandidateEvidenceSpans(m sourceMatcher, ctx sourceMatchContext, match sourceScanMatch) []SourceCandidateEvidenceSpan {
	out := []SourceCandidateEvidenceSpan{{
		Kind:      "matcher_focus",
		File:      functionFuzzNormalizeOptionalPath(ctx.File.Path),
		StartLine: match.Line,
		EndLine:   match.Line,
		Text:      compactPersistentMemoryText(strings.TrimSpace(match.Snippet), 500),
	}}
	for _, needle := range m.LineNeedles {
		line := sourceScanEvidenceLineInRange(ctx.Lines, []string{needle}, ctx.WindowStartLine, ctx.WindowEndLine)
		if line <= 0 || line == match.Line {
			continue
		}
		out = append(out, SourceCandidateEvidenceSpan{
			Kind:      "needle",
			File:      functionFuzzNormalizeOptionalPath(ctx.File.Path),
			StartLine: line,
			EndLine:   line,
			Text:      compactPersistentMemoryText(strings.TrimSpace(ctx.Lines[line-1]), 220),
		})
		if len(out) >= 8 {
			break
		}
	}
	return normalizeSourceCandidateEvidenceSpans(out)
}

func sourceScanCandidateNegativeEvidence(ctx sourceMatchContext, match sourceScanMatch) []string {
	lower := strings.ToLower(ctx.Content)
	out := []string{}
	if !containsAny(lower, "try", "__try", "except", "__except") && containsAny(lower, "probeforread", "probeforwrite", "userbuffer", "type3inputbuffer") {
		out = append(out, "no local SEH guard was visible around user-buffer handling")
	}
	if containsAny(lower, "memcpy", "rtlcopymemory", "copy") && !containsAny(lower, "sizeof", "min(", "rtlulong", "safeint", "ntintsafe") {
		out = append(out, "no obvious safe integer or bounded-copy helper was visible in the matched window")
	}
	if match.HasSymbol && strings.TrimSpace(match.Symbol.Signature) == "" {
		out = append(out, "semantic index did not include the matched symbol signature")
	}
	return compactStringSlice(out, 180)
}

func sourceScanCandidateDataflowFacts(ctx sourceMatchContext, match sourceScanMatch) []SourceCandidateFact {
	lower := strings.ToLower(ctx.Content)
	out := []SourceCandidateFact{}
	if containsAny(lower, "probeforread", "probeforwrite") && containsAny(lower, "rtlcopymemory", "memcpy", "copyfrom", "copy_memory") {
		out = append(out, SourceCandidateFact{
			Kind:   "probe_to_copy",
			Line:   match.Line,
			From:   "probe",
			To:     "copy",
			Detail: "matched window contains both user-buffer probe and memory-copy sink",
		})
	}
	if containsAny(lower, "exallocatepool", "malloc", "new ", "allocate") && containsAny(lower, "memcpy", "rtlcopymemory", "copy") {
		out = append(out, SourceCandidateFact{
			Kind:   "allocation_to_copy",
			Line:   match.Line,
			From:   "allocation",
			To:     "copy",
			Detail: "matched window contains allocation and copy operations that should share size semantics",
		})
	}
	if containsAny(lower, "userbuffer", "type3inputbuffer", "mdl", "wdfrequestretrieve") && containsAny(lower, "length", "size", "bytes", "count") {
		out = append(out, SourceCandidateFact{
			Kind:   "attacker_size_contract",
			Line:   match.Line,
			From:   "attacker_input",
			To:     "size_contract",
			Detail: "matched window ties external input storage to length or size state",
		})
	}
	return normalizeSourceCandidateFacts(out)
}

func sourceScanCandidateControlflowFacts(ctx sourceMatchContext, match sourceScanMatch) []SourceCandidateFact {
	lower := strings.ToLower(ctx.Content)
	out := []SourceCandidateFact{}
	if containsAny(lower, "switch", "case") && containsAny(lower, "ioctl", "iocode", "controlcode", "ctl_code") {
		out = append(out, SourceCandidateFact{
			Kind:   "selector_dispatch",
			Line:   match.Line,
			Detail: "matched window contains selector-driven dispatch",
		})
	}
	if containsAny(lower, "if (", "if(") && containsAny(lower, "length", "size", "count", "bytes") {
		out = append(out, SourceCandidateFact{
			Kind:   "size_guard",
			Line:   match.Line,
			Detail: "matched window contains size or length guard control flow",
		})
	}
	if containsAny(lower, "goto", "cleanup", "fail", "return status_", "return false") {
		out = append(out, SourceCandidateFact{
			Kind:   "failure_unwind",
			Line:   match.Line,
			Detail: "matched window has explicit failure or cleanup control flow",
		})
	}
	return normalizeSourceCandidateFacts(out)
}

func sourceScanTextHash(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	sum := sha1.Sum([]byte(text))
	return hex.EncodeToString(sum[:])
}

func sourceScanSymbolsByFile(symbols []SymbolRecord) map[string][]SymbolRecord {
	out := map[string][]SymbolRecord{}
	for _, symbol := range symbols {
		file := filepath.ToSlash(strings.TrimSpace(symbol.File))
		if file == "" {
			continue
		}
		out[file] = append(out[file], symbol)
	}
	for file := range out {
		sort.Slice(out[file], func(i int, j int) bool {
			if out[file][i].StartLine == out[file][j].StartLine {
				return out[file][i].Name < out[file][j].Name
			}
			return out[file][i].StartLine < out[file][j].StartLine
		})
	}
	return out
}

func sourceScanNearestSymbol(symbols []SymbolRecord, line int) (SymbolRecord, bool) {
	var best SymbolRecord
	bestDistance := 1 << 30
	for _, symbol := range symbols {
		if symbol.StartLine > 0 && symbol.EndLine > 0 && line >= symbol.StartLine && line <= symbol.EndLine {
			return symbol, true
		}
		distance := 1 << 30
		if symbol.StartLine > 0 {
			if line >= symbol.StartLine {
				distance = line - symbol.StartLine
			} else {
				distance = symbol.StartLine - line
			}
		}
		if distance < bestDistance {
			bestDistance = distance
			best = symbol
		}
	}
	if strings.TrimSpace(best.ID) == "" {
		return SymbolRecord{}, false
	}
	return best, true
}

func sourceScanProjectTypes(index SemanticIndexV2) []string {
	text := strings.ToLower(strings.Join(append([]string{index.Root, index.Goal}, sourceScanFilePaths(index.Files)...), " "))
	out := []string{}
	if containsAny(text, ".uproject", ".uplugin", ".build.cs", "uclass", "ufunction", "unreal") {
		out = append(out, "unreal")
	}
	if containsAny(text, ".vcxproj", ".sln", "driver", "ioctl", "irp_mj", "ntddk", "wdm", "fltregisterfilter") {
		out = append(out, "windows_driver")
	}
	for _, file := range index.Files {
		if analysisLanguageForExtension(file.Extension) == "cpp" {
			out = append(out, "cpp")
			break
		}
	}
	return uniqueStrings(out)
}

func sourceScanFilePaths(files []FileRecord) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Path)
	}
	return out
}

func sourceScanFileLooksSupported(file FileRecord) bool {
	ext := strings.ToLower(strings.TrimSpace(file.Extension))
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(file.Path))
	}
	switch ext {
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".hh", ".inl", ".cs":
		return true
	default:
		return false
	}
}

func sourceScanFileMatchesFilter(file FileRecord, symbols []SymbolRecord, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}
	corpus := strings.ToLower(file.Path + " " + file.Language + " " + strings.Join(file.Tags, " "))
	for _, symbol := range symbols {
		corpus += " " + symbol.ID + " " + symbol.Name + " " + symbol.CanonicalName + " " + symbol.Kind + " " + strings.Join(symbol.Tags, " ")
	}
	return strings.Contains(corpus, filter)
}

func readSourceScanFile(root string, relPath string) (string, bool) {
	path := filepath.Join(root, filepath.FromSlash(relPath))
	data, err := os.ReadFile(path)
	if err != nil || !isText(data) {
		return "", false
	}
	return string(data), true
}

func sourceScanContainsAny(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, strings.ToLower(strings.TrimSpace(needle))) {
			return true
		}
	}
	return false
}

func sourceScanEvidenceLine(lines []string, needles []string) int {
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, needle := range needles {
			if strings.Contains(lower, strings.ToLower(strings.TrimSpace(needle))) {
				return i + 1
			}
		}
	}
	return 0
}

func sourceScanEvidenceLineInRange(lines []string, needles []string, startLine int, endLine int) int {
	if len(lines) == 0 {
		return 0
	}
	if startLine <= 0 {
		startLine = 1
	}
	if endLine <= 0 || endLine > len(lines) {
		endLine = len(lines)
	}
	for i := startLine; i <= endLine; i++ {
		lower := strings.ToLower(lines[i-1])
		for _, needle := range needles {
			if strings.Contains(lower, strings.ToLower(strings.TrimSpace(needle))) {
				return i
			}
		}
	}
	return 0
}

func sourceScanLinesInRange(lines []string, startLine int, endLine int) string {
	if len(lines) == 0 {
		return ""
	}
	if startLine <= 0 {
		startLine = 1
	}
	if endLine <= 0 || endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > endLine {
		return ""
	}
	out := []string{}
	for i := startLine; i <= endLine; i++ {
		out = append(out, lines[i-1])
	}
	return strings.Join(out, "\n")
}

func sourceScanSnippet(lines []string, line int, radius int) string {
	if len(lines) == 0 {
		return ""
	}
	if line <= 0 {
		line = 1
	}
	start := line - radius
	if start < 1 {
		start = 1
	}
	end := line + radius
	if end > len(lines) {
		end = len(lines)
	}
	out := []string{}
	for i := start; i <= end; i++ {
		out = append(out, fmt.Sprintf("%d: %s", i, strings.TrimRight(lines[i-1], " \t")))
	}
	return strings.Join(out, "\n")
}

func sourceCandidateAnchor(file string, line int) string {
	file = filepath.ToSlash(strings.TrimSpace(file))
	if file == "" {
		return ""
	}
	if line > 0 {
		return fmt.Sprintf("%s:%d", file, line)
	}
	return file
}

func sourceCandidateID(candidate SourceCandidateRecord) string {
	seed := strings.Join([]string{
		normalizePersistentMemoryWorkspace(candidate.Workspace),
		strings.ToLower(strings.TrimSpace(candidate.MatcherSlug)),
		strings.ToLower(filepath.ToSlash(strings.TrimSpace(candidate.File))),
		strings.ToLower(strings.TrimSpace(candidate.SymbolID)),
		strings.ToLower(strings.TrimSpace(candidate.SymbolName)),
		fmt.Sprint(candidate.LineNumbers),
		strings.TrimSpace(candidate.MatchedPattern),
	}, "|")
	sum := sha1.Sum([]byte(seed))
	return "sc-" + hex.EncodeToString(sum[:])[:16]
}

func normalizeSourceCandidateRecord(item SourceCandidateRecord) SourceCandidateRecord {
	now := time.Now()
	item.ID = strings.TrimSpace(item.ID)
	item.Workspace = normalizePersistentMemoryWorkspace(item.Workspace)
	item.RunID = strings.TrimSpace(item.RunID)
	item.Status = strings.ToLower(strings.TrimSpace(item.Status))
	if item.Status == "" {
		item.Status = sourceCandidateStatusPending
	}
	item.MatcherSlug = strings.ToLower(strings.TrimSpace(item.MatcherSlug))
	item.MatcherDescription = compactPersistentMemoryText(item.MatcherDescription, 220)
	item.NoiseTier = strings.ToLower(strings.TrimSpace(item.NoiseTier))
	item.SeverityHint = strings.ToLower(strings.TrimSpace(item.SeverityHint))
	item.ProjectTypes = uniqueStrings(lowerStringSlice(item.ProjectTypes))
	item.File = functionFuzzNormalizeOptionalPath(item.File)
	item.LineNumbers = normalizePositiveInts(item.LineNumbers)
	item.Snippet = compactPersistentMemoryText(item.Snippet, 1200)
	item.MatchedPattern = compactPersistentMemoryText(item.MatchedPattern, 180)
	item.SymbolID = strings.TrimSpace(item.SymbolID)
	item.SymbolName = strings.TrimSpace(item.SymbolName)
	item.SymbolKind = strings.TrimSpace(item.SymbolKind)
	item.SourceAnchor = strings.TrimSpace(filepath.ToSlash(item.SourceAnchor))
	if item.SourceAnchor == "" {
		line := 0
		if len(item.LineNumbers) > 0 {
			line = item.LineNumbers[0]
		}
		item.SourceAnchor = sourceCandidateAnchor(item.File, line)
	}
	if item.Score < 0 {
		item.Score = 0
	}
	if item.Score > 100 {
		item.Score = 100
	}
	item.ConfidenceBreakdown = normalizeSourceCandidateConfidenceBreakdown(item.ConfidenceBreakdown)
	item.EvidenceSpans = normalizeSourceCandidateEvidenceSpans(item.EvidenceSpans)
	item.NegativeEvidence = compactStringSlice(item.NegativeEvidence, 180)
	item.FileContentHash = strings.ToLower(strings.TrimSpace(item.FileContentHash))
	item.CurrentFileHash = strings.ToLower(strings.TrimSpace(item.CurrentFileHash))
	if item.CurrentFileHash == "" {
		item.CurrentFileHash = item.FileContentHash
	}
	item.SymbolSignatureHash = strings.ToLower(strings.TrimSpace(item.SymbolSignatureHash))
	item.CurrentSymbolHash = strings.ToLower(strings.TrimSpace(item.CurrentSymbolHash))
	if item.CurrentSymbolHash == "" {
		item.CurrentSymbolHash = item.SymbolSignatureHash
	}
	item.StaleReason = compactPersistentMemoryText(item.StaleReason, 220)
	item.DataflowFacts = normalizeSourceCandidateFacts(item.DataflowFacts)
	item.ControlflowFacts = normalizeSourceCandidateFacts(item.ControlflowFacts)
	item.Reasons = analysisUniqueStrings(compactStringSlice(item.Reasons, 160))
	item.Tags = uniqueStrings(lowerStringSlice(item.Tags))
	item.LinkedFuzzRunIDs = uniqueStrings(item.LinkedFuzzRunIDs)
	item.LinkedCampaignIDs = uniqueStrings(item.LinkedCampaignIDs)
	for i := range item.AnalysisHistory {
		item.AnalysisHistory[i].RunID = strings.TrimSpace(item.AnalysisHistory[i].RunID)
		item.AnalysisHistory[i].FuzzRunID = strings.TrimSpace(item.AnalysisHistory[i].FuzzRunID)
		item.AnalysisHistory[i].Summary = compactPersistentMemoryText(item.AnalysisHistory[i].Summary, 260)
		item.AnalysisHistory[i].Evidence = compactStringSlice(item.AnalysisHistory[i].Evidence, 220)
		item.AnalysisHistory[i].Analyzer = strings.TrimSpace(item.AnalysisHistory[i].Analyzer)
		item.AnalysisHistory[i].ArtifactPath = functionFuzzNormalizeOptionalPath(item.AnalysisHistory[i].ArtifactPath)
		if item.AnalysisHistory[i].CreatedAt.IsZero() {
			item.AnalysisHistory[i].CreatedAt = now
		}
	}
	for i := range item.RevalidationHistory {
		item.RevalidationHistory[i] = normalizeSourceCandidateRevalidation(item.RevalidationHistory[i])
	}
	item.FeedbackDraftPaths = uniqueStrings(normalizeOptionalPaths(item.FeedbackDraftPaths))
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() || item.UpdatedAt.Before(item.CreatedAt) {
		item.UpdatedAt = item.CreatedAt
	}
	if item.ID == "" {
		item.ID = sourceCandidateID(item)
	}
	return item
}

func normalizeSourceCandidateConfidenceBreakdown(items map[string]int) map[string]int {
	if len(items) == 0 {
		return nil
	}
	out := map[string]int{}
	for key, value := range items {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" || value == 0 {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeSourceCandidateEvidenceSpans(items []SourceCandidateEvidenceSpan) []SourceCandidateEvidenceSpan {
	out := []SourceCandidateEvidenceSpan{}
	seen := map[string]struct{}{}
	for _, item := range items {
		item.Kind = strings.ToLower(strings.TrimSpace(item.Kind))
		item.File = functionFuzzNormalizeOptionalPath(item.File)
		item.Text = compactPersistentMemoryText(strings.TrimSpace(item.Text), 500)
		if item.EndLine > 0 && item.StartLine > item.EndLine {
			item.StartLine, item.EndLine = item.EndLine, item.StartLine
		}
		if item.Kind == "" && item.Text == "" {
			continue
		}
		key := strings.ToLower(strings.Join([]string{item.Kind, item.File, fmt.Sprint(item.StartLine), fmt.Sprint(item.EndLine), item.Text}, "|"))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func normalizeSourceCandidateFacts(items []SourceCandidateFact) []SourceCandidateFact {
	out := []SourceCandidateFact{}
	seen := map[string]struct{}{}
	for _, item := range items {
		item.Kind = strings.ToLower(strings.TrimSpace(item.Kind))
		item.From = compactPersistentMemoryText(strings.TrimSpace(item.From), 120)
		item.To = compactPersistentMemoryText(strings.TrimSpace(item.To), 120)
		item.Detail = compactPersistentMemoryText(strings.TrimSpace(item.Detail), 220)
		if item.Kind == "" && item.Detail == "" {
			continue
		}
		key := strings.ToLower(strings.Join([]string{item.Kind, fmt.Sprint(item.Line), item.From, item.To, item.Detail}, "|"))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func normalizeSourceCandidateRevalidation(item SourceCandidateRevalidation) SourceCandidateRevalidation {
	item.Verdict = strings.ToLower(strings.TrimSpace(item.Verdict))
	item.Reason = compactPersistentMemoryText(item.Reason, 260)
	item.Evidence = compactStringSlice(item.Evidence, 220)
	item.FuzzRunID = strings.TrimSpace(item.FuzzRunID)
	item.CampaignID = strings.TrimSpace(item.CampaignID)
	item.NativeResultKey = strings.TrimSpace(item.NativeResultKey)
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	return item
}

func normalizeSourceScanRun(run SourceScanRun) SourceScanRun {
	now := time.Now()
	run.ID = strings.TrimSpace(run.ID)
	if run.ID == "" {
		run.ID = "source-scan-" + now.Format("20060102-150405")
	}
	run.Workspace = normalizePersistentMemoryWorkspace(run.Workspace)
	run.Goal = compactPersistentMemoryText(run.Goal, 220)
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	if run.UpdatedAt.IsZero() || run.UpdatedAt.Before(run.CreatedAt) {
		run.UpdatedAt = run.CreatedAt
	}
	run.CandidateIDs = uniqueStrings(run.CandidateIDs)
	run.ArtifactDir = functionFuzzNormalizeOptionalPath(run.ArtifactDir)
	run.ManifestPath = functionFuzzNormalizeOptionalPath(run.ManifestPath)
	run.ReportPath = functionFuzzNormalizeOptionalPath(run.ReportPath)
	run.Notes = compactStringSlice(run.Notes, 220)
	run.Options.OnlySlugs = uniqueStrings(lowerStringSlice(run.Options.OnlySlugs))
	run.Options.SkipSlugs = uniqueStrings(lowerStringSlice(run.Options.SkipSlugs))
	run.Options.Filter = strings.TrimSpace(run.Options.Filter)
	run.Options.Files = splitSourceScanFiles(strings.Join(run.Options.Files, ","))
	return run
}

func normalizePositiveInts(items []int) []int {
	seen := map[int]struct{}{}
	out := []int{}
	for _, item := range items {
		if item <= 0 {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Ints(out)
	return out
}

func lowerStringSlice(items []string) []string {
	out := []string{}
	for _, item := range items {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func compactStringSlice(items []string, limit int) []string {
	out := []string{}
	for _, item := range items {
		item = compactPersistentMemoryText(item, limit)
		if item != "" {
			out = append(out, item)
		}
	}
	return uniqueStrings(out)
}

func normalizeOptionalPaths(items []string) []string {
	out := []string{}
	for _, item := range items {
		item = functionFuzzNormalizeOptionalPath(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func upsertSourceScanRun(items []SourceScanRun, run SourceScanRun) []SourceScanRun {
	for i := range items {
		if strings.EqualFold(items[i].ID, run.ID) {
			items[i] = run
			return items
		}
	}
	return append(items, run)
}

func upsertSourceCandidates(existing []SourceCandidateRecord, incoming []SourceCandidateRecord) []SourceCandidateRecord {
	index := map[string]int{}
	out := append([]SourceCandidateRecord(nil), existing...)
	for i, item := range out {
		index[strings.ToLower(strings.TrimSpace(item.ID))] = i
	}
	for _, item := range incoming {
		item = normalizeSourceCandidateRecord(item)
		key := strings.ToLower(item.ID)
		if i, ok := index[key]; ok {
			out[i] = mergeSourceCandidate(out[i], item)
			continue
		}
		index[key] = len(out)
		out = append(out, item)
	}
	return out
}

func mergeSourceCandidate(existing SourceCandidateRecord, incoming SourceCandidateRecord) SourceCandidateRecord {
	existing = normalizeSourceCandidateRecord(existing)
	incoming = normalizeSourceCandidateRecord(incoming)
	if incoming.UpdatedAt.Before(existing.UpdatedAt) {
		incoming.UpdatedAt = existing.UpdatedAt
	}
	incoming.CreatedAt = existing.CreatedAt
	incoming.AnalysisHistory = mergeSourceCandidateAnalysisHistory(existing.AnalysisHistory, incoming.AnalysisHistory)
	incoming.RevalidationHistory = mergeSourceCandidateRevalidationHistory(existing.RevalidationHistory, incoming.RevalidationHistory)
	incoming.LinkedFuzzRunIDs = uniqueStrings(append(existing.LinkedFuzzRunIDs, incoming.LinkedFuzzRunIDs...))
	incoming.LinkedCampaignIDs = uniqueStrings(append(existing.LinkedCampaignIDs, incoming.LinkedCampaignIDs...))
	incoming.FeedbackDraftPaths = uniqueStrings(append(existing.FeedbackDraftPaths, incoming.FeedbackDraftPaths...))
	incoming.ConfidenceBreakdown = mergeSourceCandidateConfidenceBreakdown(existing.ConfidenceBreakdown, incoming.ConfidenceBreakdown)
	if incoming.Status == sourceCandidateStatusPending && existing.Status != "" && existing.Status != sourceCandidateStatusPending && existing.Status != sourceCandidateStatusStale {
		incoming.Status = existing.Status
	}
	return normalizeSourceCandidateRecord(incoming)
}

func mergeSourceCandidateConfidenceBreakdown(existing map[string]int, incoming map[string]int) map[string]int {
	out := normalizeSourceCandidateConfidenceBreakdown(existing)
	if out == nil {
		out = map[string]int{}
	}
	for key, value := range normalizeSourceCandidateConfidenceBreakdown(incoming) {
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeSourceCandidateAnalysisHistory(existing []SourceCandidateAnalysisEntry, incoming []SourceCandidateAnalysisEntry) []SourceCandidateAnalysisEntry {
	out := []SourceCandidateAnalysisEntry{}
	index := map[string]int{}
	for _, item := range append(append([]SourceCandidateAnalysisEntry{}, existing...), incoming...) {
		item.RunID = strings.TrimSpace(item.RunID)
		item.FuzzRunID = strings.TrimSpace(item.FuzzRunID)
		item.Analyzer = strings.TrimSpace(item.Analyzer)
		item.ArtifactPath = functionFuzzNormalizeOptionalPath(item.ArtifactPath)
		key := sourceCandidateAnalysisHistoryKey(item)
		if existingIndex, ok := index[key]; ok {
			out[existingIndex] = item
			continue
		}
		index[key] = len(out)
		out = append(out, item)
	}
	return out
}

func sourceCandidateAnalysisHistoryKey(item SourceCandidateAnalysisEntry) string {
	parts := []string{
		strings.TrimSpace(item.FuzzRunID),
		strings.TrimSpace(item.RunID),
		strings.TrimSpace(item.Analyzer),
		functionFuzzNormalizeOptionalPath(item.ArtifactPath),
	}
	key := strings.ToLower(strings.Join(parts, "|"))
	if strings.Trim(key, "|") != "" {
		return key
	}
	return strings.ToLower(strings.TrimSpace(item.Summary) + "|" + strings.Join(item.Evidence, "|"))
}

func mergeSourceCandidateRevalidationHistory(existing []SourceCandidateRevalidation, incoming []SourceCandidateRevalidation) []SourceCandidateRevalidation {
	out := []SourceCandidateRevalidation{}
	index := map[string]int{}
	for _, item := range append(append([]SourceCandidateRevalidation{}, existing...), incoming...) {
		item = normalizeSourceCandidateRevalidation(item)
		key := sourceCandidateRevalidationHistoryKey(item)
		if existingIndex, ok := index[key]; ok {
			out[existingIndex] = item
			continue
		}
		index[key] = len(out)
		out = append(out, item)
	}
	return out
}

func sourceCandidateRevalidationHistoryKey(item SourceCandidateRevalidation) string {
	parts := []string{
		strings.TrimSpace(item.Verdict),
		strings.TrimSpace(item.FuzzRunID),
		strings.TrimSpace(item.CampaignID),
		strings.TrimSpace(item.NativeResultKey),
		strings.TrimSpace(item.Reason),
	}
	key := strings.ToLower(strings.Join(parts, "|"))
	if strings.Trim(key, "|") != "" {
		return key
	}
	return strings.ToLower(strings.Join(item.Evidence, "|"))
}

func sourceScanMatcherCounts(candidates []SourceCandidateRecord) map[string]int {
	counts := map[string]int{}
	for _, candidate := range candidates {
		slug := strings.TrimSpace(candidate.MatcherSlug)
		if slug != "" {
			counts[slug]++
		}
	}
	return counts
}

func sourceCandidateIDs(candidates []SourceCandidateRecord) []string {
	out := []string{}
	for _, item := range candidates {
		if strings.TrimSpace(item.ID) != "" {
			out = append(out, item.ID)
		}
	}
	return uniqueStrings(out)
}

func writeSourceScanArtifacts(root string, run *SourceScanRun, candidates []SourceCandidateRecord) error {
	if run == nil {
		return nil
	}
	artifactDir := filepath.Join(root, userConfigDirName, "source_scan", run.ID)
	run.ArtifactDir = artifactDir
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return err
	}
	candidatesPath := filepath.Join(artifactDir, "candidates.json")
	data, err := json.MarshalIndent(candidates, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWriteFile(candidatesPath, append(data, '\n'), 0o644); err != nil {
		return err
	}
	run.CandidateCount = len(candidates)
	run.ByMatcher = sourceScanMatcherCounts(candidates)
	run.CandidateIDs = sourceCandidateIDs(candidates)
	manifestPath := filepath.Join(artifactDir, "manifest.json")
	run.ManifestPath = manifestPath
	manifestData, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWriteFile(manifestPath, append(manifestData, '\n'), 0o644); err != nil {
		return err
	}
	reportPath := filepath.Join(artifactDir, "report.md")
	run.ReportPath = reportPath
	return atomicWriteFile(reportPath, []byte(renderSourceScanMarkdown(*run, candidates)), 0o644)
}

func renderSourceScanRun(run SourceScanRun, candidates []SourceCandidateRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Run: %s\n", run.ID)
	fmt.Fprintf(&b, "Candidates: %d\n", len(candidates))
	if strings.TrimSpace(run.ReportPath) != "" {
		fmt.Fprintf(&b, "Report: %s\n", run.ReportPath)
	}
	for _, item := range limitSourceCandidates(candidates, 8) {
		fmt.Fprintf(&b, "- %s score=%d tier=%s matcher=%s target=%s anchor=%s\n",
			item.ID,
			item.Score,
			valueOrUnset(item.NoiseTier),
			valueOrUnset(item.MatcherSlug),
			valueOrUnset(firstNonBlankString(item.SymbolName, item.File)),
			valueOrUnset(item.SourceAnchor))
	}
	if len(candidates) > 0 {
		b.WriteString(renderSourceScanFuzzHandoff(candidates[0]))
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderSourceCandidateConfidenceBreakdown(items map[string]int) string {
	if len(items) == 0 {
		return ""
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%+d", key, items[key]))
	}
	return strings.Join(parts, ", ")
}

func renderSourceCandidateFactSummary(items []SourceCandidateFact) string {
	parts := []string{}
	for _, item := range limitSourceCandidateFacts(items, 4) {
		parts = append(parts, firstNonBlankString(item.Detail, item.Kind))
	}
	return strings.Join(parts, " | ")
}

func limitSourceCandidateFacts(items []SourceCandidateFact, limit int) []SourceCandidateFact {
	if limit <= 0 || len(items) <= limit {
		return append([]SourceCandidateFact(nil), items...)
	}
	return append([]SourceCandidateFact(nil), items[:limit]...)
}

func renderSourceCandidate(item SourceCandidateRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ID: %s\n", item.ID)
	fmt.Fprintf(&b, "Status: %s\n", valueOrUnset(item.Status))
	fmt.Fprintf(&b, "Matcher: %s (%s)\n", valueOrUnset(item.MatcherSlug), valueOrUnset(item.NoiseTier))
	fmt.Fprintf(&b, "Score: %d\n", item.Score)
	fmt.Fprintf(&b, "Target: %s\n", valueOrUnset(firstNonBlankString(item.SymbolName, item.SymbolID)))
	fmt.Fprintf(&b, "File: %s\n", valueOrUnset(item.File))
	fmt.Fprintf(&b, "Anchor: %s\n", valueOrUnset(item.SourceAnchor))
	if item.Stale {
		fmt.Fprintf(&b, "Stale: true - %s\n", valueOrUnset(item.StaleReason))
	}
	if len(item.ConfidenceBreakdown) > 0 {
		fmt.Fprintf(&b, "Confidence: %s\n", renderSourceCandidateConfidenceBreakdown(item.ConfidenceBreakdown))
	}
	if len(item.Reasons) > 0 {
		fmt.Fprintf(&b, "Reasons: %s\n", strings.Join(item.Reasons, " | "))
	}
	if len(item.DataflowFacts) > 0 {
		fmt.Fprintf(&b, "Dataflow: %s\n", renderSourceCandidateFactSummary(item.DataflowFacts))
	}
	if len(item.ControlflowFacts) > 0 {
		fmt.Fprintf(&b, "Controlflow: %s\n", renderSourceCandidateFactSummary(item.ControlflowFacts))
	}
	if len(item.NegativeEvidence) > 0 {
		fmt.Fprintf(&b, "Caveats: %s\n", strings.Join(item.NegativeEvidence, " | "))
	}
	if len(item.LinkedFuzzRunIDs) > 0 {
		fmt.Fprintf(&b, "Fuzz runs: %s\n", strings.Join(item.LinkedFuzzRunIDs, ", "))
	}
	if len(item.RevalidationHistory) > 0 {
		latest := item.RevalidationHistory[len(item.RevalidationHistory)-1]
		fmt.Fprintf(&b, "Latest verdict: %s - %s\n", latest.Verdict, latest.Reason)
	}
	if strings.TrimSpace(item.Snippet) != "" {
		fmt.Fprintf(&b, "\nSnippet:\n%s\n", item.Snippet)
	}
	b.WriteString(renderSourceScanFuzzHandoff(item))
	return strings.TrimRight(b.String(), "\n")
}

func renderSourceScanFuzzHandoff(candidate SourceCandidateRecord) string {
	candidate = normalizeSourceCandidateRecord(candidate)
	if strings.TrimSpace(candidate.ID) == "" {
		return ""
	}
	var b strings.Builder
	target := firstNonBlankString(candidate.SymbolName, candidate.SourceAnchor, candidate.File)
	fmt.Fprintf(&b, "\nNext: send the strongest source candidate into focused function fuzzing.\n")
	fmt.Fprintf(&b, "Command: /fuzz-func --from-candidate %s\n", candidate.ID)
	if strings.TrimSpace(target) != "" || strings.TrimSpace(candidate.MatcherSlug) != "" {
		fmt.Fprintf(&b, "Why: %s via %s, score=%d, tier=%s\n",
			valueOrUnset(target),
			valueOrUnset(candidate.MatcherSlug),
			candidate.Score,
			valueOrUnset(candidate.NoiseTier))
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderSourceScanMarkdown(run SourceScanRun, candidates []SourceCandidateRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Source Scan %s\n\n", run.ID)
	fmt.Fprintf(&b, "- Workspace: `%s`\n", run.Workspace)
	fmt.Fprintf(&b, "- Candidates: %d\n", len(candidates))
	fmt.Fprintf(&b, "- Generated: %s\n\n", run.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "## Top Candidates\n\n")
	for _, item := range limitSourceCandidates(candidates, 30) {
		fmt.Fprintf(&b, "### %s\n\n", item.ID)
		fmt.Fprintf(&b, "- Matcher: `%s`\n", item.MatcherSlug)
		fmt.Fprintf(&b, "- Noise: `%s`\n", item.NoiseTier)
		fmt.Fprintf(&b, "- Score: %d\n", item.Score)
		fmt.Fprintf(&b, "- Target: `%s`\n", firstNonBlankString(item.SymbolName, item.SymbolID, item.File))
		fmt.Fprintf(&b, "- Anchor: `%s`\n", item.SourceAnchor)
		if item.Stale {
			fmt.Fprintf(&b, "- Stale: `%t` - %s\n", item.Stale, item.StaleReason)
		}
		if len(item.ConfidenceBreakdown) > 0 {
			fmt.Fprintf(&b, "- Confidence: %s\n", renderSourceCandidateConfidenceBreakdown(item.ConfidenceBreakdown))
		}
		if len(item.Reasons) > 0 {
			fmt.Fprintf(&b, "- Reasons: %s\n", strings.Join(item.Reasons, "; "))
		}
		fmt.Fprintf(&b, "- Fuzz: `/fuzz-func --from-candidate %s`\n\n", item.ID)
	}
	return b.String()
}

func limitSourceCandidates(items []SourceCandidateRecord, limit int) []SourceCandidateRecord {
	if limit <= 0 || len(items) <= limit {
		return append([]SourceCandidateRecord(nil), items...)
	}
	return append([]SourceCandidateRecord(nil), items[:limit]...)
}

func (rt *runtimeState) revalidateSourceCandidate(item SourceCandidateRecord, manualVerdict string, manualReason string) (SourceCandidateRecord, SourceCandidateRevalidation) {
	item = normalizeSourceCandidateRecord(item)
	item = rt.refreshSourceCandidateFreshness(item)
	verdict := SourceCandidateRevalidation{
		Verdict:   strings.ToLower(strings.TrimSpace(manualVerdict)),
		Reason:    strings.TrimSpace(manualReason),
		CreatedAt: time.Now(),
	}
	if verdict.Verdict == "" {
		verdict = rt.inferSourceCandidateVerdict(item)
	}
	verdict = normalizeSourceCandidateRevalidation(verdict)
	item.Status = verdict.Verdict
	item.RevalidationHistory = append(item.RevalidationHistory, verdict)
	item.UpdatedAt = time.Now()
	return normalizeSourceCandidateRecord(item), verdict
}

func (rt *runtimeState) refreshSourceCandidateFreshness(item SourceCandidateRecord) SourceCandidateRecord {
	item = normalizeSourceCandidateRecord(item)
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = item.Workspace
	}
	if strings.TrimSpace(root) == "" || strings.TrimSpace(item.File) == "" {
		return item
	}
	content, ok := readSourceScanFile(root, item.File)
	if !ok {
		item.Stale = true
		item.StaleReason = "candidate file is no longer readable in this workspace"
		return item
	}
	currentFileHash := sourceScanTextHash(content)
	item.CurrentFileHash = currentFileHash
	if item.FileContentHash != "" && currentFileHash != "" && item.FileContentHash != currentFileHash {
		item.Stale = true
		item.StaleReason = "candidate file content hash changed since the source signal was captured"
	}
	if item.SymbolName != "" && item.SymbolSignatureHash != "" {
		currentSymbolHash := sourceScanCurrentSymbolSignatureHash(content, item.SymbolName)
		if currentSymbolHash != "" {
			item.CurrentSymbolHash = currentSymbolHash
			if currentSymbolHash != item.SymbolSignatureHash {
				item.Stale = true
				item.StaleReason = firstNonBlankString(item.StaleReason, "candidate symbol signature hash changed since capture")
			}
		}
	}
	return normalizeSourceCandidateRecord(item)
}

func sourceScanCurrentSymbolSignatureHash(content string, symbolName string) string {
	symbolName = strings.TrimSpace(symbolName)
	if symbolName == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	lowerName := strings.ToLower(symbolName)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if !strings.Contains(lower, lowerName) || !strings.Contains(trimmed, "(") {
			continue
		}
		if strings.HasPrefix(lower, "if") || strings.HasPrefix(lower, "switch") || strings.HasPrefix(lower, "for") || strings.HasPrefix(lower, "while") {
			continue
		}
		return sourceScanSignatureHash(trimmed)
	}
	return ""
}

func sourceScanSignatureHash(signature string) string {
	normalized := sourceScanNormalizeSignature(signature)
	if normalized == "" {
		return ""
	}
	return sourceScanTextHash(normalized)
}

func sourceScanNormalizeSignature(signature string) string {
	signature = strings.TrimSpace(signature)
	if signature == "" {
		return ""
	}
	if idx := strings.Index(signature, "{"); idx >= 0 {
		signature = strings.TrimSpace(signature[:idx])
	}
	if idx := strings.Index(signature, ";"); idx >= 0 {
		signature = strings.TrimSpace(signature[:idx])
	}
	signature = strings.Join(strings.Fields(signature), " ")
	signature = strings.TrimSuffix(signature, " const")
	return strings.TrimSpace(signature)
}

func (rt *runtimeState) inferSourceCandidateVerdict(item SourceCandidateRecord) SourceCandidateRevalidation {
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = item.Workspace
	}
	if item.File != "" {
		if _, ok := readSourceScanFile(root, item.File); !ok {
			return SourceCandidateRevalidation{
				Verdict:  "fixed",
				Reason:   "candidate file is no longer readable in this workspace",
				Evidence: []string{item.File},
			}
		}
	}
	if rt != nil && rt.fuzzCampaigns != nil {
		campaigns, _ := rt.fuzzCampaigns.ListRecent(root, 20)
		for _, campaign := range campaigns {
			for _, result := range campaign.NativeResults {
				if !sourceCandidateNativeResultMatches(item, result) {
					continue
				}
				key := fuzzCampaignNativeResultKey(result)
				if result.CrashCount > 0 || strings.EqualFold(result.Outcome, "failed") {
					return SourceCandidateRevalidation{
						Verdict:         "native-confirmed",
						Reason:          "linked native fuzz result produced crash or failing evidence",
						Evidence:        []string{result.ReportPath, result.CrashFingerprint, result.SuspectedInvariant},
						FuzzRunID:       result.RunID,
						CampaignID:      campaign.ID,
						NativeResultKey: key,
					}
				}
				if strings.EqualFold(result.Outcome, "passed") {
					return SourceCandidateRevalidation{
						Verdict:         "source-false-positive",
						Reason:          "linked native fuzz result completed without crash evidence",
						Evidence:        []string{result.ReportPath},
						FuzzRunID:       result.RunID,
						CampaignID:      campaign.ID,
						NativeResultKey: key,
					}
				}
			}
		}
	}
	if item.Stale {
		return SourceCandidateRevalidation{
			Verdict:  sourceCandidateStatusStale,
			Reason:   firstNonBlankString(item.StaleReason, "candidate source fingerprint changed; rerun /source-scan before trusting this source signal"),
			Evidence: compactStringSlice([]string{item.File, item.FileContentHash, item.CurrentFileHash, item.SymbolSignatureHash, item.CurrentSymbolHash}, 220),
		}
	}
	if len(item.LinkedFuzzRunIDs) > 0 {
		return SourceCandidateRevalidation{
			Verdict:   "needs-native",
			Reason:    "candidate has source fuzz handoff but no confirmed native campaign result yet",
			Evidence:  append([]string(nil), item.LinkedFuzzRunIDs...),
			FuzzRunID: firstSliceValue(item.LinkedFuzzRunIDs),
		}
	}
	if strings.EqualFold(item.NoiseTier, "precise") || item.Score >= 80 {
		return SourceCandidateRevalidation{
			Verdict:  "source-plausible",
			Reason:   "precise high-score source signal remains present and should be driven into /fuzz-func",
			Evidence: []string{item.SourceAnchor, item.MatcherSlug},
		}
	}
	return SourceCandidateRevalidation{
		Verdict:  "uncertain",
		Reason:   "source signal is still present but needs a focused fuzz run or native verifier",
		Evidence: []string{item.SourceAnchor, item.MatcherSlug},
	}
}

func sourceCandidateNativeResultMatches(item SourceCandidateRecord, result FuzzCampaignNativeResult) bool {
	if strings.TrimSpace(item.ID) != "" && strings.EqualFold(strings.TrimSpace(item.ID), strings.TrimSpace(result.SourceCandidateID)) {
		return true
	}
	if containsString(item.LinkedFuzzRunIDs, result.RunID) {
		return true
	}
	fileCompatible := strings.TrimSpace(item.File) != "" && strings.TrimSpace(result.TargetFile) != "" && strings.EqualFold(functionFuzzNormalizeOptionalPath(item.File), functionFuzzNormalizeOptionalPath(result.TargetFile))
	if strings.TrimSpace(item.SymbolName) != "" && strings.TrimSpace(result.Target) != "" && fileCompatible && strings.EqualFold(strings.TrimSpace(item.SymbolName), strings.TrimSpace(result.Target)) {
		return true
	}
	if strings.TrimSpace(item.SymbolID) != "" && strings.TrimSpace(result.Target) != "" && fileCompatible && strings.EqualFold(strings.TrimSpace(item.SymbolID), strings.TrimSpace(result.Target)) {
		return true
	}
	return false
}

func linkSourceCandidateToFunctionFuzz(candidate SourceCandidateRecord, run FunctionFuzzRun) SourceCandidateRecord {
	candidate = normalizeSourceCandidateRecord(candidate)
	run = normalizeFunctionFuzzRun(run)
	if strings.TrimSpace(run.ID) != "" {
		candidate.LinkedFuzzRunIDs = uniqueStrings(append(candidate.LinkedFuzzRunIDs, run.ID))
		entry := SourceCandidateAnalysisEntry{
			RunID:        run.AnalysisRunID,
			FuzzRunID:    run.ID,
			Summary:      firstNonBlankString(run.Summary, fmt.Sprintf("Generated %d source-only fuzz scenario(s).", len(run.VirtualScenarios))),
			Evidence:     []string{run.ReportPath, run.PlanPath, run.HarnessPath},
			CreatedAt:    time.Now(),
			Analyzer:     "fuzz-func",
			ArtifactPath: run.ReportPath,
		}
		candidate.AnalysisHistory = mergeSourceCandidateAnalysisHistory(candidate.AnalysisHistory, []SourceCandidateAnalysisEntry{entry})
	}
	if len(run.VirtualScenarios) > 0 {
		if !sourceCandidateHasTerminalVerdict(candidate.Status) {
			candidate.Status = "needs-native"
		}
		verdict := SourceCandidateRevalidation{
			Verdict:   "needs-native",
			Reason:    "source fuzz generated deterministic virtual scenarios; native verifier or campaign execution is required before treating it as confirmed",
			Evidence:  []string{run.ReportPath},
			FuzzRunID: run.ID,
			CreatedAt: time.Now(),
		}
		candidate.RevalidationHistory = mergeSourceCandidateRevalidationHistory(candidate.RevalidationHistory, []SourceCandidateRevalidation{verdict})
	} else if !sourceCandidateHasTerminalVerdict(candidate.Status) {
		candidate.Status = "uncertain"
	}
	candidate.UpdatedAt = time.Now()
	return normalizeSourceCandidateRecord(candidate)
}

func sourceCandidateHasTerminalVerdict(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "native-confirmed", "source-false-positive", "false-positive", "fixed":
		return true
	default:
		return false
	}
}

func applySourceCandidateEvidenceToFunctionFuzzRun(run FunctionFuzzRun, candidate SourceCandidateRecord) FunctionFuzzRun {
	run = normalizeFunctionFuzzRun(run)
	candidate = normalizeSourceCandidateRecord(candidate)
	if strings.TrimSpace(candidate.ID) == "" {
		return run
	}
	note := fmt.Sprintf("Source candidate evidence: %s via %s at %s.", candidate.ID, valueOrUnset(candidate.MatcherSlug), valueOrUnset(candidate.SourceAnchor))
	run.Notes = uniqueStrings(append(run.Notes, note))
	for _, fact := range candidate.DataflowFacts {
		observation := FunctionFuzzCodeObservation{
			Kind:         sourceCandidateFactObservationKind(fact, "source_candidate_dataflow"),
			SymbolID:     run.TargetSymbolID,
			Symbol:       firstNonBlankString(run.TargetSymbolName, candidate.SymbolName),
			File:         firstNonBlankString(candidate.File, run.TargetFile),
			Line:         fact.Line,
			Evidence:     firstNonBlankString(fact.Detail, candidate.MatchedPattern),
			FocusInputs:  sourceCandidateFocusInputs(candidate),
			WhyItMatters: "source-scan dataflow evidence was attached before fuzz scenario ranking",
		}
		run.CodeObservations = append(run.CodeObservations, observation)
	}
	for _, fact := range candidate.ControlflowFacts {
		observation := FunctionFuzzCodeObservation{
			Kind:         sourceCandidateFactObservationKind(fact, "source_candidate_controlflow"),
			SymbolID:     run.TargetSymbolID,
			Symbol:       firstNonBlankString(run.TargetSymbolName, candidate.SymbolName),
			File:         firstNonBlankString(candidate.File, run.TargetFile),
			Line:         fact.Line,
			Evidence:     firstNonBlankString(fact.Detail, candidate.MatchedPattern),
			FocusInputs:  sourceCandidateFocusInputs(candidate),
			WhyItMatters: "source-scan control-flow evidence was attached before fuzz scenario ranking",
		}
		run.CodeObservations = append(run.CodeObservations, observation)
	}
	if len(candidate.EvidenceSpans) > 0 {
		scenario := sourceCandidateEvidenceScenario(run, candidate)
		if strings.TrimSpace(scenario.Title) != "" {
			run.VirtualScenarios = append([]FunctionFuzzVirtualScenario{scenario}, run.VirtualScenarios...)
		}
	}
	run.SuggestedCommands = uniqueStrings(append([]string{"/fuzz-campaign run"}, run.SuggestedCommands...))
	run = normalizeFunctionFuzzRun(run)
	return run
}

func sourceCandidateFactObservationKind(fact SourceCandidateFact, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(fact.Kind)) {
	case "probe_to_copy":
		return "copy_sink"
	case "allocation_to_copy":
		return "alloc_site"
	case "attacker_size_contract", "size_guard":
		return "size_guard"
	case "selector_dispatch":
		return "dispatch_guard"
	case "failure_unwind":
		return "cleanup_path"
	default:
		return fallback
	}
}

func sourceCandidateFocusInputs(candidate SourceCandidateRecord) []string {
	out := []string{}
	for _, fact := range append(append([]SourceCandidateFact{}, candidate.DataflowFacts...), candidate.ControlflowFacts...) {
		out = append(out, fact.From, fact.To)
	}
	for _, tag := range candidate.Tags {
		switch tag {
		case "user_buffer", "output_buffer", "ioctl", "bounds", "double_fetch":
			out = append(out, tag)
		}
	}
	return compactStringSlice(out, 80)
}

func sourceCandidateEvidenceScenario(run FunctionFuzzRun, candidate SourceCandidateRecord) FunctionFuzzVirtualScenario {
	span := candidate.EvidenceSpans[0]
	inputs := sourceCandidateFocusInputs(candidate)
	if len(inputs) == 0 {
		inputs = []string{firstNonBlankString(candidate.MatcherSlug, "source-candidate")}
	}
	scenario := FunctionFuzzVirtualScenario{
		Title:         compactPersistentMemoryText("drive source candidate "+firstNonBlankString(candidate.MatcherSlug, candidate.ID), 120),
		Confidence:    firstNonBlankString(candidate.NoiseTier, "normal"),
		FocusSymbolID: firstNonBlankString(run.TargetSymbolID, candidate.SymbolID),
		FocusSymbol:   firstNonBlankString(run.TargetSymbolName, candidate.SymbolName),
		FocusFile:     firstNonBlankString(candidate.File, run.TargetFile),
		ScopeFilePath: uniqueStrings([]string{firstNonBlankString(candidate.File, run.TargetFile)}),
		Inputs:        inputs,
		ConcreteInputs: compactStringSlice([]string{
			"mutate the source candidate focus input until " + firstNonBlankString(candidate.MatchedPattern, candidate.MatcherSlug) + " changes branch behavior",
		}, 180),
		BranchFacts:   compactStringSlice(append([]string{candidate.MatchedPattern}, sourceCandidateFactDetails(candidate.ControlflowFacts)...), 180),
		DriftExamples: compactStringSlice(append(sourceCandidateFactDetails(candidate.DataflowFacts), candidate.NegativeEvidence...), 180),
		ExpectedFlow:  compactPersistentMemoryText("source-scan candidate evidence should reach "+firstNonBlankString(candidate.MatchedPattern, candidate.MatcherSlug), 180),
		LikelyIssues:  compactStringSlice(append([]string{candidate.MatcherDescription}, candidate.Reasons...), 180),
		PathSketch:    compactStringSlice([]string{candidate.SourceAnchor, candidate.MatcherSlug, "fuzz-campaign native revalidation"}, 160),
		PathHint:      "source-scan candidate handoff",
		SourceExcerpt: FunctionFuzzSourceExcerpt{
			Symbol:    firstNonBlankString(candidate.SymbolName, run.TargetSymbolName),
			File:      firstNonBlankString(span.File, candidate.File, run.TargetFile),
			StartLine: span.StartLine,
			FocusLine: firstPositiveInt(span.StartLine, firstSliceInt(candidate.LineNumbers)),
			EndLine:   span.EndLine,
			Snippet:   strings.Split(firstNonBlankString(candidate.Snippet, span.Text), "\n"),
		},
	}
	return scenario
}

func sourceCandidateFactDetails(items []SourceCandidateFact) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, firstNonBlankString(item.Detail, item.Kind))
	}
	return compactStringSlice(out, 180)
}

func firstSliceInt(values []int) int {
	if len(values) == 0 {
		return 0
	}
	return values[0]
}

func buildFunctionFuzzQueryFromSourceCandidate(candidate SourceCandidateRecord) string {
	candidate = normalizeSourceCandidateRecord(candidate)
	target := strings.TrimSpace(candidate.SymbolName)
	if target == "" && candidate.SymbolID != "" {
		target = candidate.SymbolID
	}
	file := functionFuzzNormalizeOptionalPath(candidate.File)
	if target == "" && file == "" {
		return ""
	}
	if target == "" {
		return fmt.Sprintf(`--file "%s"`, file)
	}
	if file == "" {
		return target
	}
	return fmt.Sprintf(`%s --file "%s"`, target, file)
}

func (rt *runtimeState) resolveFunctionFuzzSourceCandidateQuery(query string) (string, SourceCandidateRecord, bool, error) {
	fields := splitAnalysisCommandLine(strings.TrimSpace(query))
	for i := 0; i < len(fields); i++ {
		token := strings.ToLower(strings.TrimSpace(fields[i]))
		if token != "--from-candidate" && token != "from-candidate" {
			continue
		}
		if i+1 >= len(fields) {
			return "", SourceCandidateRecord{}, false, fmt.Errorf("--from-candidate requires a candidate id")
		}
		if rt == nil || rt.sourceScan == nil {
			return "", SourceCandidateRecord{}, false, fmt.Errorf("source scan store is not configured")
		}
		candidate, ok, err := rt.sourceScan.GetCandidateForWorkspace(fields[i+1], workspaceSnapshotRoot(rt.workspace))
		if err != nil {
			return "", SourceCandidateRecord{}, false, err
		}
		if !ok {
			return "", SourceCandidateRecord{}, false, fmt.Errorf("source candidate not found: %s", fields[i+1])
		}
		rest := append([]string{}, fields[:i]...)
		rest = append(rest, fields[i+2:]...)
		base := buildFunctionFuzzQueryFromSourceCandidate(candidate)
		if strings.TrimSpace(base) == "" {
			return "", SourceCandidateRecord{}, false, fmt.Errorf("source candidate %s does not have a fuzzable file or symbol", candidate.ID)
		}
		if len(rest) > 0 {
			base += " " + strings.Join(rest, " ")
		}
		return base, candidate, true, nil
	}
	return query, SourceCandidateRecord{}, false, nil
}

func renderSourceCandidateMatcherDraft(candidate SourceCandidateRecord, run FunctionFuzzRun, result FuzzCampaignNativeResult) map[string]interface{} {
	candidate = normalizeSourceCandidateRecord(candidate)
	return map[string]interface{}{
		"version":             "source_matcher_draft/v1",
		"slug":                firstNonBlankString(candidate.MatcherSlug, sourceDraftSlug("native-confirmed", result.Target)),
		"description":         "Draft matcher generated from native fuzz evidence. Review and add negative examples before promoting to built-ins.",
		"noise_tier":          "precise",
		"file_patterns":       []string{firstNonBlankString(candidate.File, result.TargetFile)},
		"project_types":       candidate.ProjectTypes,
		"matched_pattern":     firstNonBlankString(candidate.MatchedPattern, result.SuspectedInvariant),
		"source_anchor":       firstNonBlankString(candidate.SourceAnchor, result.TargetFile),
		"source_candidate_id": candidate.ID,
		"fuzz_run_id":         run.ID,
		"native_result_key":   fuzzCampaignNativeResultKey(result),
		"evidence":            []string{result.ReportPath, result.CrashFingerprint, result.SuspectedInvariant},
		"examples": []map[string]string{
			{
				"positive": firstNonBlankString(candidate.Snippet, result.SuspectedInvariant),
			},
		},
	}
}

func renderSourceCandidatePatternDraft(candidate SourceCandidateRecord, run FunctionFuzzRun, result FuzzCampaignNativeResult) RootCausePatternPack {
	title := firstNonBlankString(candidate.MatcherDescription, result.SuspectedInvariant, "Native fuzz confirmed source candidate")
	return RootCausePatternPack{
		Version:     "root_cause_patterns/v1",
		Description: "Draft pattern generated from native fuzz feedback. Review before moving into builtin.json or a workspace pattern pack.",
		Patterns: []RootCausePattern{{
			ID:                 sourceDraftSlug("native-fuzz", firstNonBlankString(candidate.MatcherSlug, result.Target, run.TargetSymbolName)),
			Title:              compactPersistentMemoryText(title, 140),
			ProjectTypes:       candidate.ProjectTypes,
			Symptoms:           []string{firstNonBlankString(result.SuspectedInvariant, "native fuzz result indicates candidate invariant failure")},
			RootCauses:         []string{firstNonBlankString(candidate.MatchedPattern, candidate.MatcherSlug, "source candidate matched native fuzz evidence")},
			CodeSignals:        analysisUniqueStrings([]string{candidate.MatcherSlug, candidate.MatchedPattern, candidate.SourceAnchor}),
			OutOfRangeCases:    sourceCandidateOutOfRangeCases(run),
			LikelyFiles:        []string{firstNonBlankString(candidate.File, result.TargetFile)},
			VerificationProbes: []string{firstNonBlankString(result.MinimizeCommand, "rerun the native fuzz harness and inspect crash artifacts")},
			Confidence:         "draft",
			Tags:               analysisUniqueStrings(append([]string{"fuzz", "native_evidence"}, candidate.Tags...)),
			Sources: []RootCausePatternSource{{
				Type:       "fuzz_native_result",
				Evidence:   strings.Join(compactStringSlice([]string{result.ReportPath, result.CrashFingerprint, result.SuspectedInvariant}, 220), " | "),
				Confidence: "medium",
			}},
		}},
	}
}

func sourceCandidateOutOfRangeCases(run FunctionFuzzRun) []string {
	out := []string{}
	for _, scenario := range limitFunctionFuzzVirtualScenarios(run.VirtualScenarios, 4) {
		out = append(out, firstNonBlankString(scenario.Title, scenario.ExpectedFlow, firstSliceValue(scenario.LikelyIssues)))
	}
	return compactStringSlice(out, 160)
}

func sourceDraftSlug(prefix string, value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("\\", "-", "/", "-", ":", "-", " ", "-", "_", "-", ".", "-", "@", "-", "(", "-", ")", "-")
	value = replacer.Replace(value)
	parts := strings.Split(value, "-")
	out := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		out = []string{"candidate"}
	}
	return strings.Trim(strings.ToLower(prefix+"-"+strings.Join(out, "-")), "-")
}

func sourceScanWriteFeedbackDrafts(campaign FuzzCampaign, candidate SourceCandidateRecord, run FunctionFuzzRun, result FuzzCampaignNativeResult) ([]string, error) {
	campaign = normalizeFuzzCampaign(campaign)
	if strings.TrimSpace(campaign.ArtifactDir) == "" {
		return nil, nil
	}
	dir := filepath.Join(campaign.ArtifactDir, "feedback")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	base := sourceDraftSlug("feedback", firstNonBlankString(candidate.ID, run.ID, result.Target))
	matcherPath := filepath.Join(dir, base+"-source-matcher-draft.json")
	matcherData, err := json.MarshalIndent(renderSourceCandidateMatcherDraft(candidate, run, result), "", "  ")
	if err != nil {
		return nil, err
	}
	if err := atomicWriteFile(matcherPath, append(matcherData, '\n'), 0o644); err != nil {
		return nil, err
	}
	patternPath := filepath.Join(dir, base+"-root-cause-pattern-draft.json")
	patternData, err := json.MarshalIndent(renderSourceCandidatePatternDraft(candidate, run, result), "", "  ")
	if err != nil {
		return nil, err
	}
	if err := atomicWriteFile(patternPath, append(patternData, '\n'), 0o644); err != nil {
		return nil, err
	}
	return []string{matcherPath, patternPath}, nil
}

type sourceCandidateFeedback struct {
	Score   int
	Reasons []string
	Matches []string
}

type sourceCandidateFeedbackIndex struct {
	candidates []SourceCandidateRecord
}

func analysisFuzzSourceCandidateFeedback(run ProjectAnalysisRun) sourceCandidateFeedbackIndex {
	root := strings.TrimSpace(run.Snapshot.Root)
	if root == "" {
		return sourceCandidateFeedbackIndex{}
	}
	items, err := NewSourceScanStore().ListCandidates(root, 1000)
	if err != nil {
		return sourceCandidateFeedbackIndex{}
	}
	out := []SourceCandidateRecord{}
	for _, item := range items {
		item = normalizeSourceCandidateRecord(item)
		if item.Status == "source-false-positive" || item.Status == "fixed" {
			continue
		}
		out = append(out, item)
	}
	return sourceCandidateFeedbackIndex{candidates: out}
}

func (idx sourceCandidateFeedbackIndex) match(symbol SymbolRecord, entry AnalysisFuzzTargetCatalogEntry) sourceCandidateFeedback {
	if len(idx.candidates) == 0 {
		return sourceCandidateFeedback{}
	}
	bestScore := 0
	reasons := []string{}
	matches := []string{}
	symbolFile := functionFuzzNormalizeOptionalPath(firstNonBlankString(symbol.File, entry.File))
	symbolName := strings.ToLower(strings.TrimSpace(firstNonBlankString(symbol.ID, symbol.Name, symbol.CanonicalName, entry.Name)))
	for _, candidate := range idx.candidates {
		if !sourceCandidateMatchesSymbol(candidate, symbol, entry, symbolFile, symbolName) {
			continue
		}
		boost := candidate.Score / 4
		if strings.EqualFold(candidate.NoiseTier, "precise") {
			boost += 4
		}
		if boost > 28 {
			boost = 28
		}
		if boost > bestScore {
			bestScore = boost
		}
		reasons = append(reasons, fmt.Sprintf("source candidate %s matched %s", candidate.ID, candidate.MatcherSlug))
		matches = append(matches, candidate.ID)
	}
	return sourceCandidateFeedback{
		Score:   bestScore,
		Reasons: analysisUniqueStrings(reasons),
		Matches: uniqueStrings(matches),
	}
}

func sourceCandidateMatchesSymbol(candidate SourceCandidateRecord, symbol SymbolRecord, entry AnalysisFuzzTargetCatalogEntry, symbolFile string, symbolName string) bool {
	candidate = normalizeSourceCandidateRecord(candidate)
	if candidate.SymbolID != "" && symbol.ID != "" && strings.EqualFold(candidate.SymbolID, symbol.ID) {
		return true
	}
	if candidate.SymbolName != "" {
		name := strings.ToLower(strings.TrimSpace(candidate.SymbolName))
		if name != "" && (strings.Contains(symbolName, name) || strings.Contains(name, strings.ToLower(entry.Name))) {
			return true
		}
	}
	if candidate.File != "" && symbolFile != "" && strings.EqualFold(functionFuzzNormalizeOptionalPath(candidate.File), symbolFile) {
		return true
	}
	return false
}

func sourceScanNativeResultWarrantsDraft(result FuzzCampaignNativeResult) bool {
	normalized := normalizeFuzzCampaignNativeResults([]FuzzCampaignNativeResult{result})
	if len(normalized) == 0 {
		return false
	}
	result = normalized[0]
	// Spurious harness-only crashes must not generate matcher/pattern drafts or
	// native-confirmed feedback; the feasibility gate applies to the whole
	// validation lifecycle, not only FuzzCampaignFinding status.
	if fuzzCampaignNativeResultIsSpurious(result) {
		return false
	}
	if result.CrashCount > 0 {
		return true
	}
	return strings.EqualFold(result.Outcome, "failed") && strings.TrimSpace(result.SuspectedInvariant) != ""
}

func sourceCandidateFromFunctionFuzzRun(run FunctionFuzzRun, result FuzzCampaignNativeResult) SourceCandidateRecord {
	run = normalizeFunctionFuzzRun(run)
	normalized := normalizeFuzzCampaignNativeResults([]FuzzCampaignNativeResult{result})
	if len(normalized) > 0 {
		result = normalized[0]
	}
	line := run.TargetStartLine
	now := time.Now()
	candidate := SourceCandidateRecord{
		ID:               strings.TrimSpace(run.SourceCandidateID),
		Workspace:        run.Workspace,
		Status:           "native-confirmed",
		MatcherSlug:      firstNonBlankString(run.SourceMatcherSlug, "native-fuzz-feedback"),
		NoiseTier:        "precise",
		SeverityHint:     "high",
		File:             firstNonBlankString(run.TargetFile, result.TargetFile),
		LineNumbers:      []int{line},
		Snippet:          firstNonBlankString(functionFuzzTopScenarioSourceSnippet(run), result.SuspectedInvariant),
		MatchedPattern:   firstNonBlankString(result.SuspectedInvariant, "native fuzz result"),
		SymbolID:         run.TargetSymbolID,
		SymbolName:       firstNonBlankString(run.TargetSymbolName, result.Target),
		SymbolKind:       "function",
		SourceAnchor:     sourceCandidateAnchor(firstNonBlankString(run.TargetFile, result.TargetFile), line),
		Score:            95,
		Reasons:          []string{"native fuzz feedback should be converted into a durable matcher and pattern draft"},
		Tags:             []string{"fuzz", "native_evidence"},
		CreatedAt:        now,
		UpdatedAt:        now,
		LinkedFuzzRunIDs: []string{run.ID},
	}
	return normalizeSourceCandidateRecord(candidate)
}

func functionFuzzTopScenarioSourceSnippet(run FunctionFuzzRun) string {
	for _, scenario := range run.VirtualScenarios {
		snippet := strings.TrimSpace(strings.Join(scenario.SourceExcerpt.Snippet, "\n"))
		if snippet != "" {
			return snippet
		}
	}
	return ""
}

func linkSourceCandidateToNativeFeedback(candidate SourceCandidateRecord, campaign FuzzCampaign, result FuzzCampaignNativeResult, paths []string) SourceCandidateRecord {
	candidate = normalizeSourceCandidateRecord(candidate)
	candidate.Status = "native-confirmed"
	candidate.LinkedCampaignIDs = uniqueStrings(append(candidate.LinkedCampaignIDs, campaign.ID))
	candidate.FeedbackDraftPaths = uniqueStrings(append(candidate.FeedbackDraftPaths, paths...))
	candidate.RevalidationHistory = append(candidate.RevalidationHistory, SourceCandidateRevalidation{
		Verdict:         "native-confirmed",
		Reason:          "native fuzz result generated matcher and root-cause pattern feedback drafts",
		Evidence:        append([]string{result.ReportPath, result.CrashFingerprint, result.SuspectedInvariant}, paths...),
		FuzzRunID:       result.RunID,
		CampaignID:      campaign.ID,
		NativeResultKey: fuzzCampaignNativeResultKey(result),
		CreatedAt:       time.Now(),
	})
	candidate.UpdatedAt = time.Now()
	return normalizeSourceCandidateRecord(candidate)
}

func linkSourceCandidateToNativeOutcome(candidate SourceCandidateRecord, campaign FuzzCampaign, result FuzzCampaignNativeResult) SourceCandidateRecord {
	candidate = normalizeSourceCandidateRecord(candidate)
	campaign = normalizeFuzzCampaign(campaign)
	normalized := normalizeFuzzCampaignNativeResults([]FuzzCampaignNativeResult{result})
	if len(normalized) > 0 {
		result = normalized[0]
	}
	verdict := sourceCandidateVerdictFromNativeOutcome(result)
	if verdict == "" {
		return candidate
	}
	candidate.LinkedCampaignIDs = uniqueStrings(append(candidate.LinkedCampaignIDs, campaign.ID))
	if strings.TrimSpace(result.RunID) != "" {
		candidate.LinkedFuzzRunIDs = uniqueStrings(append(candidate.LinkedFuzzRunIDs, result.RunID))
	}
	if !sourceCandidateHasTerminalVerdict(candidate.Status) || verdict == "native-confirmed" {
		candidate.Status = verdict
	}
	delta := 0
	switch verdict {
	case "native-confirmed":
		delta = 18
	case "source-false-positive":
		delta = -22
	}
	if delta != 0 {
		candidate.Score += delta
		if candidate.Score < 0 {
			candidate.Score = 0
		}
		if candidate.Score > 100 {
			candidate.Score = 100
		}
		if candidate.ConfidenceBreakdown == nil {
			candidate.ConfidenceBreakdown = map[string]int{}
		}
		candidate.ConfidenceBreakdown["native_feedback"] = delta
	}
	candidate.RevalidationHistory = mergeSourceCandidateRevalidationHistory(candidate.RevalidationHistory, []SourceCandidateRevalidation{{
		Verdict:         verdict,
		Reason:          sourceCandidateNativeOutcomeReason(verdict, result),
		Evidence:        []string{result.ReportPath, result.CrashFingerprint, result.SuspectedInvariant},
		FuzzRunID:       result.RunID,
		CampaignID:      campaign.ID,
		NativeResultKey: fuzzCampaignNativeResultKey(result),
		CreatedAt:       time.Now(),
	}})
	candidate.UpdatedAt = time.Now()
	return normalizeSourceCandidateRecord(candidate)
}

func sourceCandidateVerdictFromNativeOutcome(result FuzzCampaignNativeResult) string {
	// Harness-misuse crashes are not target confirmation evidence.
	if fuzzCampaignNativeResultIsSpurious(result) {
		return ""
	}
	if result.CrashCount > 0 || strings.EqualFold(result.Outcome, "failed") {
		return "native-confirmed"
	}
	if strings.EqualFold(result.Outcome, "passed") {
		return "source-false-positive"
	}
	return ""
}

func sourceCandidateNativeOutcomeReason(verdict string, result FuzzCampaignNativeResult) string {
	switch verdict {
	case "native-confirmed":
		return firstNonBlankString(result.SuspectedInvariant, "native fuzz result failed or produced crash evidence")
	case "source-false-positive":
		return "native fuzz result completed without crash evidence for this candidate"
	default:
		return "native fuzz result updated candidate confidence"
	}
}

func fuzzCampaignFeedbackRunArtifacts(campaign FuzzCampaign, run FunctionFuzzRun, result FuzzCampaignNativeResult, paths []string) []FuzzCampaignRunArtifact {
	out := []FuzzCampaignRunArtifact{}
	for _, path := range paths {
		kind := "feedback_draft"
		if strings.Contains(strings.ToLower(path), "source-matcher") {
			kind = "source_matcher_draft"
		}
		if strings.Contains(strings.ToLower(path), "root-cause-pattern") {
			kind = "root_cause_pattern_draft"
		}
		out = append(out, FuzzCampaignRunArtifact{
			Kind:         kind,
			RunID:        run.ID,
			Target:       firstNonBlankString(result.Target, run.TargetSymbolName),
			TargetFile:   firstNonBlankString(result.TargetFile, run.TargetFile),
			SourceAnchor: sourceCandidateAnchor(firstNonBlankString(result.TargetFile, run.TargetFile), run.TargetStartLine),
			Path:         path,
			Summary:      "Feedback draft generated from native fuzz evidence.",
			Severity:     "medium",
			Signal:       result.SuspectedInvariant,
			EvidenceID:   result.EvidenceID,
			RecordedAt:   time.Now(),
		})
	}
	return normalizeFuzzCampaignRunArtifacts(out)
}

func (rt *runtimeState) recentSourceCandidateIDs() []string {
	if rt == nil || rt.sourceScan == nil {
		return nil
	}
	items, err := rt.sourceScan.ListCandidates(workspaceSnapshotRoot(rt.workspace), 12)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, item := range items {
		if strings.TrimSpace(item.ID) != "" {
			out = append(out, item.ID)
		}
	}
	return uniqueStrings(out)
}
