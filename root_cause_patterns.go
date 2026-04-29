package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

//go:embed root_cause_patterns/*.json
var rootCausePatternFS embed.FS

type RootCausePatternPack struct {
	Version     string             `json:"version,omitempty"`
	Description string             `json:"description,omitempty"`
	Patterns    []RootCausePattern `json:"patterns,omitempty"`
}

type RootCausePattern struct {
	ID                 string                   `json:"id"`
	Title              string                   `json:"title"`
	ProjectTypes       []string                 `json:"project_types,omitempty"`
	Symptoms           []string                 `json:"symptoms,omitempty"`
	RootCauses         []string                 `json:"root_causes,omitempty"`
	CodeSignals        []string                 `json:"code_signals,omitempty"`
	StateVariables     []string                 `json:"state_variables,omitempty"`
	OutOfRangeCases    []string                 `json:"out_of_range_cases,omitempty"`
	LikelyFiles        []string                 `json:"likely_files,omitempty"`
	VerificationProbes []string                 `json:"verification_probes,omitempty"`
	Confidence         string                   `json:"confidence,omitempty"`
	Tags               []string                 `json:"tags,omitempty"`
	Sources            []RootCausePatternSource `json:"sources,omitempty"`
}

type RootCausePatternSource struct {
	Type       string `json:"type,omitempty"`
	URL        string `json:"url,omitempty"`
	Repository string `json:"repository,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

type RootCausePatternMatch struct {
	PatternID          string   `json:"pattern_id,omitempty"`
	Title              string   `json:"title,omitempty"`
	ProjectTypes       []string `json:"project_types,omitempty"`
	Score              int      `json:"score,omitempty"`
	Confidence         string   `json:"confidence,omitempty"`
	MatchedSymptoms    []string `json:"matched_symptoms,omitempty"`
	MatchedSignals     []string `json:"matched_signals,omitempty"`
	MatchedFiles       []string `json:"matched_files,omitempty"`
	RootCauses         []string `json:"root_causes,omitempty"`
	VerificationProbes []string `json:"verification_probes,omitempty"`
	SourceCount        int      `json:"source_count,omitempty"`
	UsedByWorker       bool     `json:"used_by_worker,omitempty"`
	AcceptedByReviewer bool     `json:"accepted_by_reviewer,omitempty"`
}

type RootCauseGitHubIssueCorpus struct {
	GeneratedAt     time.Time                    `json:"generated_at,omitempty"`
	APIURL          string                       `json:"api_url,omitempty"`
	Queries         []string                     `json:"queries,omitempty"`
	ExecutedQueries []string                     `json:"executed_queries,omitempty"`
	QueryResults    []RootCauseGitHubQueryResult `json:"query_results,omitempty"`
	ProjectTypes    []string                     `json:"project_types,omitempty"`
	Items           []RootCauseGitHubIssue       `json:"items,omitempty"`
}

type RootCauseGitHubQueryResult struct {
	Query              string    `json:"query,omitempty"`
	ExecutedQuery      string    `json:"executed_query,omitempty"`
	APIURL             string    `json:"api_url,omitempty"`
	StatusCode         int       `json:"status_code,omitempty"`
	TotalCount         int       `json:"total_count,omitempty"`
	Fetched            int       `json:"fetched,omitempty"`
	FetchedAt          time.Time `json:"fetched_at,omitempty"`
	RateLimitRemaining string    `json:"rate_limit_remaining,omitempty"`
	RateLimitReset     string    `json:"rate_limit_reset,omitempty"`
}

type RootCauseGitHubIssue struct {
	Repository     string    `json:"repository,omitempty"`
	Number         int       `json:"number,omitempty"`
	Title          string    `json:"title,omitempty"`
	Body           string    `json:"body,omitempty"`
	State          string    `json:"state,omitempty"`
	HTMLURL        string    `json:"html_url,omitempty"`
	Labels         []string  `json:"labels,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
	ClosedAt       time.Time `json:"closed_at,omitempty"`
	Score          int       `json:"score,omitempty"`
	Quality        string    `json:"quality,omitempty"`
	QualityReasons []string  `json:"quality_reasons,omitempty"`
}

type rootCauseGitHubSearchConfig struct {
	APIURL       string
	Token        string
	Queries      []string
	ProjectTypes []string
	Limit        int
}

type RootCausePatternPackValidation struct {
	Patterns     int                               `json:"patterns"`
	SourceBacked int                               `json:"source_backed"`
	ProjectTypes map[string]int                    `json:"project_types,omitempty"`
	Promotable   int                               `json:"promotable,omitempty"`
	Provisional  int                               `json:"provisional,omitempty"`
	Errors       []RootCausePatternValidationIssue `json:"errors,omitempty"`
	Warnings     []RootCausePatternValidationIssue `json:"warnings,omitempty"`
}

type RootCausePatternValidationIssue struct {
	Severity  string `json:"severity"`
	PatternID string `json:"pattern_id,omitempty"`
	Field     string `json:"field,omitempty"`
	Message   string `json:"message"`
}

func loadBuiltinRootCausePatternPack() RootCausePatternPack {
	entries, err := rootCausePatternFS.ReadDir("root_cause_patterns")
	if err != nil {
		return RootCausePatternPack{}
	}
	combined := RootCausePatternPack{Version: "1.0"}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		data, err := rootCausePatternFS.ReadFile(filepath.ToSlash(filepath.Join("root_cause_patterns", entry.Name())))
		if err != nil {
			continue
		}
		pack := RootCausePatternPack{}
		if err := json.Unmarshal(data, &pack); err != nil {
			continue
		}
		combined.Patterns = append(combined.Patterns, pack.Patterns...)
		if combined.Description == "" {
			combined.Description = pack.Description
		}
	}
	return normalizeRootCausePatternPack(combined)
}

func loadRootCausePatternPackWithDiagnostics(root string, explicitPaths []string) (RootCausePatternPack, []string) {
	combined := loadBuiltinRootCausePatternPack()
	diagnostics := []string{}
	for _, path := range rootCausePatternPackInputPaths(root, explicitPaths) {
		pack, err := loadRootCausePatternPackFile(path)
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("could not load root-cause pattern pack %s: %v", path, err))
			continue
		}
		combined.Patterns = append(combined.Patterns, pack.Patterns...)
		if combined.Description == "" {
			combined.Description = pack.Description
		}
	}
	return normalizeRootCausePatternPack(combined), diagnostics
}

func loadRootCausePatternPackForValidation(root string, explicitPaths []string) (RootCausePatternPack, []string) {
	combined := loadBuiltinRootCausePatternPack()
	diagnostics := []string{}
	for _, path := range rootCausePatternPackInputPaths(root, explicitPaths) {
		data, err := os.ReadFile(path)
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("could not load root-cause pattern pack %s: %v", path, err))
			continue
		}
		pack := RootCausePatternPack{}
		if err := json.Unmarshal(data, &pack); err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("could not parse root-cause pattern pack %s: %v", path, err))
			continue
		}
		combined.Patterns = append(combined.Patterns, pack.Patterns...)
		if combined.Description == "" {
			combined.Description = pack.Description
		}
	}
	return combined, diagnostics
}

func loadRootCausePatternPack(root string, explicitPaths []string) RootCausePatternPack {
	pack, _ := loadRootCausePatternPackWithDiagnostics(root, explicitPaths)
	return pack
}

func loadRootCausePatternPackFile(path string) (RootCausePatternPack, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RootCausePatternPack{}, err
	}
	pack := RootCausePatternPack{}
	if err := json.Unmarshal(data, &pack); err != nil {
		return RootCausePatternPack{}, err
	}
	return normalizeRootCausePatternPack(pack), nil
}

func rootCausePatternPackInputPaths(root string, explicitPaths []string) []string {
	paths := []string{}
	if strings.TrimSpace(root) != "" {
		paths = append(paths, rootCauseDefaultPatternPackPaths(root)...)
	}
	for _, raw := range explicitPaths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) && strings.TrimSpace(root) != "" {
			path = filepath.Join(root, path)
		}
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			if matches, globErr := filepath.Glob(filepath.Join(path, "*.json")); globErr == nil {
				paths = append(paths, matches...)
			}
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return analysisUniqueStrings(paths)
}

func rootCauseDefaultPatternPackPaths(root string) []string {
	paths := []string{}
	defaultFile := filepath.Join(root, ".kernforge", "root_cause", "pattern_pack.json")
	if info, err := os.Stat(defaultFile); err == nil && !info.IsDir() {
		paths = append(paths, defaultFile)
	}
	defaultDir := filepath.Join(root, ".kernforge", "root_cause", "pattern_packs")
	if matches, err := filepath.Glob(filepath.Join(defaultDir, "*.json")); err == nil {
		paths = append(paths, matches...)
	}
	sort.Strings(paths)
	return paths
}

func normalizeRootCausePatternPack(pack RootCausePatternPack) RootCausePatternPack {
	pack.Version = strings.TrimSpace(pack.Version)
	pack.Description = strings.TrimSpace(pack.Description)
	out := []RootCausePattern{}
	seen := map[string]struct{}{}
	for _, pattern := range pack.Patterns {
		pattern = normalizeRootCausePattern(pattern)
		if pattern.ID == "" {
			continue
		}
		if _, ok := seen[pattern.ID]; ok {
			continue
		}
		seen[pattern.ID] = struct{}{}
		out = append(out, pattern)
	}
	sort.SliceStable(out, func(i int, j int) bool {
		if len(out[i].ProjectTypes) == len(out[j].ProjectTypes) {
			return out[i].ID < out[j].ID
		}
		return strings.Join(out[i].ProjectTypes, ",") < strings.Join(out[j].ProjectTypes, ",")
	})
	pack.Patterns = out
	return pack
}

func normalizeRootCausePattern(pattern RootCausePattern) RootCausePattern {
	pattern.ID = rootCausePatternID(pattern)
	pattern.Title = strings.TrimSpace(pattern.Title)
	pattern.ProjectTypes = normalizeRootCauseProjectTypes(pattern.ProjectTypes)
	pattern.Symptoms = analysisUniqueStrings(pattern.Symptoms)
	pattern.RootCauses = analysisUniqueStrings(pattern.RootCauses)
	pattern.CodeSignals = analysisUniqueStrings(pattern.CodeSignals)
	pattern.StateVariables = analysisUniqueStrings(pattern.StateVariables)
	pattern.OutOfRangeCases = analysisUniqueStrings(pattern.OutOfRangeCases)
	pattern.LikelyFiles = analysisUniqueStrings(pattern.LikelyFiles)
	pattern.VerificationProbes = analysisUniqueStrings(pattern.VerificationProbes)
	pattern.Confidence = normalizeRootCauseConfidence(pattern.Confidence, 55)
	pattern.Tags = analysisUniqueStrings(pattern.Tags)
	sources := []RootCausePatternSource{}
	for _, source := range pattern.Sources {
		source.Type = strings.TrimSpace(source.Type)
		source.URL = strings.TrimSpace(source.URL)
		source.Repository = strings.TrimSpace(source.Repository)
		source.Evidence = strings.TrimSpace(source.Evidence)
		source.Confidence = normalizeRootCauseConfidence(source.Confidence, rootCauseConfidenceScoreForString(pattern.Confidence))
		if source.Type == "" && source.URL == "" && source.Evidence == "" {
			continue
		}
		sources = append(sources, source)
	}
	pattern.Sources = sources
	if pattern.Title == "" && len(pattern.Symptoms) > 0 {
		pattern.Title = pattern.Symptoms[0]
	}
	return pattern
}

func validateRootCausePatternPack(pack RootCausePatternPack) RootCausePatternPackValidation {
	validation := RootCausePatternPackValidation{
		Patterns:     len(pack.Patterns),
		ProjectTypes: map[string]int{},
	}
	seen := map[string]struct{}{}
	for _, rawPattern := range pack.Patterns {
		pattern := normalizeRootCausePattern(rawPattern)
		patternID := pattern.ID
		addIssue := func(severity string, field string, message string) {
			issue := RootCausePatternValidationIssue{
				Severity:  severity,
				PatternID: patternID,
				Field:     field,
				Message:   message,
			}
			if severity == "error" {
				validation.Errors = append(validation.Errors, issue)
			} else {
				validation.Warnings = append(validation.Warnings, issue)
			}
		}
		if patternID == "" {
			addIssue("error", "id", "pattern id is missing and cannot be derived")
		}
		if patternID != "" {
			if _, ok := seen[patternID]; ok {
				addIssue("error", "id", "duplicate pattern id")
			}
			seen[patternID] = struct{}{}
		}
		if strings.TrimSpace(pattern.Title) == "" {
			addIssue("error", "title", "title is missing")
		}
		if len(pattern.ProjectTypes) == 0 {
			addIssue("error", "project_types", "project type is missing")
		}
		for _, projectType := range pattern.ProjectTypes {
			validation.ProjectTypes[projectType]++
		}
		if len(pattern.Sources) > 0 {
			validation.SourceBacked++
		}
		if len(pattern.CodeSignals) == 0 && len(pattern.StateVariables) == 0 {
			addIssue("warning", "code_signals", "pattern has no code signal or state variable")
		}
		if len(pattern.RootCauses) == 0 || rootCausePatternRootCauseLooksPlaceholder(pattern.RootCauses) {
			addIssue("warning", "root_causes", "root cause text is missing or still looks like a placeholder")
		}
		if !rootCausePatternHasSourceURL(pattern) {
			addIssue("warning", "sources", "source URL is missing")
		}
		for _, signal := range append(pattern.CodeSignals, pattern.StateVariables...) {
			if rootCausePatternSignalLooksGeneric(signal) {
				addIssue("warning", "code_signals", fmt.Sprintf("signal %q is too generic", signal))
			}
		}
		for _, likely := range pattern.LikelyFiles {
			if rootCausePatternLikelyFileLooksGeneric(likely) {
				addIssue("warning", "likely_files", fmt.Sprintf("likely file %q is too generic", likely))
			}
		}
		if strings.EqualFold(pattern.Confidence, "high") && !rootCausePatternHasSourceConfidence(pattern, "high") {
			addIssue("warning", "confidence", "high-confidence pattern has no high-confidence source")
		}
		if containsString(pattern.Tags, "github_quality:promotable") {
			validation.Promotable++
		}
		if containsString(pattern.Tags, "github_quality:provisional") {
			validation.Provisional++
		}
	}
	validation.Errors = append([]RootCausePatternValidationIssue(nil), validation.Errors...)
	validation.Warnings = append([]RootCausePatternValidationIssue(nil), validation.Warnings...)
	return validation
}

func rootCausePatternRootCauseLooksPlaceholder(items []string) bool {
	if len(items) == 0 {
		return true
	}
	for _, item := range items {
		lower := strings.ToLower(strings.TrimSpace(item))
		if lower == "" {
			continue
		}
		if strings.Contains(lower, "review linked fix") || strings.Contains(lower, "todo") || strings.Contains(lower, "tbd") || lower == "string" {
			continue
		}
		return false
	}
	return true
}

func rootCausePatternHasSourceURL(pattern RootCausePattern) bool {
	for _, source := range pattern.Sources {
		if strings.TrimSpace(source.URL) != "" {
			return true
		}
	}
	return false
}

func rootCausePatternHasSourceConfidence(pattern RootCausePattern, confidence string) bool {
	for _, source := range pattern.Sources {
		if strings.EqualFold(normalizeRootCauseConfidence(source.Confidence, 50), confidence) {
			return true
		}
	}
	return false
}

func rootCausePatternSignalLooksGeneric(signal string) bool {
	signal = strings.ToLower(strings.TrimSpace(signal))
	if len([]rune(signal)) < 3 {
		return true
	}
	generic := map[string]struct{}{
		"bug": {}, "fix": {}, "error": {}, "handler": {}, "manager": {}, "service": {}, "controller": {}, "request": {}, "response": {}, "state": {}, "status": {}, "value": {}, "data": {}, "file": {}, "main": {}, "util": {}, "utils": {}, "common": {},
	}
	_, ok := generic[signal]
	return ok
}

func rootCausePatternLikelyFileLooksGeneric(path string) bool {
	path = strings.ToLower(strings.TrimSpace(filepath.ToSlash(path)))
	if len([]rune(path)) < 4 {
		return true
	}
	generic := map[string]struct{}{
		"src": {}, "source": {}, "include": {}, "main": {}, "index": {}, "app": {}, "lib": {}, "common": {}, "utils": {}, "service": {}, "handler": {}, "controller": {},
	}
	_, ok := generic[strings.Trim(path, "/")]
	return ok
}

func rootCausePatternID(pattern RootCausePattern) string {
	id := strings.ToLower(strings.TrimSpace(pattern.ID))
	id = regexp.MustCompile(`[^a-z0-9_:\.-]+`).ReplaceAllString(id, "_")
	id = strings.Trim(id, "_")
	if id != "" {
		return id
	}
	source := strings.Join(append([]string{pattern.Title}, pattern.Symptoms...), " ")
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(source))
	return "pattern_" + hex.EncodeToString(sum[:])[:12]
}

func rootCauseConfidenceScoreForString(confidence string) int {
	switch normalizeRootCauseConfidence(confidence, 55) {
	case "high":
		return 80
	case "medium":
		return 55
	default:
		return 30
	}
}

func normalizeRootCauseProjectTypes(types []string) []string {
	out := []string{}
	for _, item := range types {
		item = strings.ToLower(strings.TrimSpace(item))
		item = strings.ReplaceAll(item, " ", "_")
		item = strings.ReplaceAll(item, "-", "_")
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return analysisUniqueStrings(out)
}

func inferRootCauseProjectTypes(snapshot ProjectSnapshot, goal string) []string {
	corpus := rootCauseSnapshotTypeCorpus(snapshot, goal)
	types := []string{}
	if len(snapshot.UnrealProjects) > 0 || len(snapshot.UnrealModules) > 0 || strings.Contains(corpus, ".uproject") || strings.Contains(corpus, ".uplugin") || strings.Contains(corpus, "unreal") {
		if strings.Contains(corpus, "server") || strings.Contains(corpus, "dedicatedserver") || strings.Contains(corpus, "targettype.server") {
			types = append(types, "unreal5_game_server")
		}
		types = append(types, "unreal5_game_client")
	}
	if strings.Contains(corpus, "driverentry") || strings.Contains(corpus, ".inf") || strings.Contains(corpus, "ntddk") || strings.Contains(corpus, "wdm") || strings.Contains(corpus, "kmdf") || strings.Contains(corpus, "wdf_driver_config") {
		types = append(types, "windows_kernel_driver")
	}
	if strings.Contains(corpus, "service_control") || strings.Contains(corpus, "registerservicectrlhandler") || strings.Contains(corpus, "servicemain") || strings.Contains(corpus, "setservicestatus") || strings.Contains(corpus, "sc stop") {
		types = append(types, "windows_user_service")
	}
	if snapshot.ModulePath != "" && (strings.Contains(corpus, "agent") || strings.Contains(corpus, "cli") || strings.Contains(corpus, "command") || strings.Contains(corpus, "tool")) {
		types = append(types, "go_cli_agent")
	}
	if strings.Contains(corpus, "package.json") || strings.Contains(corpus, "next.config") || strings.Contains(corpus, "vite.config") || strings.Contains(corpus, ".tsx") || strings.Contains(corpus, ".jsx") {
		types = append(types, "web_frontend")
	}
	if strings.Contains(corpus, "controller") || strings.Contains(corpus, "handler") || strings.Contains(corpus, "repository") || strings.Contains(corpus, "server") || strings.Contains(corpus, "api/") || strings.Contains(corpus, "routes") {
		types = append(types, "web_backend", "server")
	}
	if len(types) == 0 {
		types = append(types, "generic")
	}
	return normalizeRootCauseProjectTypes(types)
}

func rootCauseSnapshotTypeCorpus(snapshot ProjectSnapshot, goal string) string {
	parts := []string{strings.ToLower(goal), strings.ToLower(snapshot.ModulePath)}
	parts = append(parts, lowerStrings(snapshot.ManifestFiles)...)
	for _, project := range snapshot.SolutionProjects {
		parts = append(parts, strings.ToLower(strings.Join([]string{project.Name, project.Path, project.Kind, project.OutputType}, " ")))
	}
	for _, target := range snapshot.UnrealTargets {
		parts = append(parts, strings.ToLower(strings.Join([]string{target.Name, target.Path, target.TargetType}, " ")))
	}
	for _, file := range snapshot.Files {
		parts = append(parts, strings.ToLower(file.Path))
		parts = append(parts, strings.ToLower(strings.Join(file.RawImports, " ")))
		parts = append(parts, strings.ToLower(strings.Join(file.Imports, " ")))
		parts = append(parts, strings.ToLower(strings.Join(file.ImportanceReasons, " ")))
	}
	parts = append(parts, rootCauseSmallSourceCorpus(snapshot, 320000))
	return strings.ToLower(strings.Join(parts, "\n"))
}

func rootCauseSmallSourceCorpus(snapshot ProjectSnapshot, limitBytes int) string {
	if strings.TrimSpace(snapshot.Root) == "" || limitBytes <= 0 {
		return ""
	}
	var b strings.Builder
	written := 0
	files := topImportantFiles(snapshot, 80)
	if len(files) == 0 {
		files = snapshot.Files
	}
	for _, file := range files {
		if written >= limitBytes {
			break
		}
		if !rootCausePatternSourceExtAllowed(file.Path) {
			continue
		}
		abs := filepath.Join(snapshot.Root, filepath.FromSlash(file.Path))
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || info.Size() > 128000 {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		text := string(data)
		if len(text) > 12000 {
			text = text[:12000]
		}
		b.WriteString("\n")
		b.WriteString(strings.ToLower(file.Path))
		b.WriteString("\n")
		b.WriteString(strings.ToLower(text))
		written += len(text)
	}
	return b.String()
}

func rootCausePatternSourceExtAllowed(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".cpp", ".cc", ".cxx", ".c", ".h", ".hpp", ".cs", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java", ".kt", ".swift", ".ini", ".json", ".uproject", ".uplugin", ".inf", ".vcxproj", ".sln":
		return true
	default:
		return false
	}
}

func matchRootCausePatterns(snapshot ProjectSnapshot, goal string, limit int) []RootCausePatternMatch {
	pack := loadRootCausePatternPack(snapshot.Root, nil)
	return matchRootCausePatternsFromPack(snapshot, goal, pack, limit)
}

func matchRootCausePatternsFromPack(snapshot ProjectSnapshot, goal string, pack RootCausePatternPack, limit int) []RootCausePatternMatch {
	if limit <= 0 {
		limit = 12
	}
	projectTypes := snapshot.ProjectTypes
	if len(projectTypes) == 0 {
		projectTypes = inferRootCauseProjectTypes(snapshot, goal)
	}
	projectTypes = normalizeRootCauseProjectTypes(projectTypes)
	corpus := rootCausePatternMatchCorpus(snapshot, goal)
	goalTerms := rootCauseEvidenceTerms(goal)
	matches := []RootCausePatternMatch{}
	for _, pattern := range pack.Patterns {
		pattern = normalizeRootCausePattern(pattern)
		score := 0
		matchedSymptoms := []string{}
		matchedSignals := []string{}
		matchedFiles := []string{}
		projectTypeScore := 0
		if rootCauseProjectTypeOverlaps(projectTypes, pattern.ProjectTypes) {
			projectTypeScore = 36
			score += projectTypeScore
		}
		patternText := strings.ToLower(strings.Join(append(append(append([]string{pattern.Title}, pattern.Symptoms...), pattern.RootCauses...), pattern.Tags...), " "))
		for _, term := range goalTerms {
			term = strings.ToLower(term)
			if rootCauseTextContainsTerm(patternText, term) {
				score += 5
				matchedSymptoms = append(matchedSymptoms, term)
			}
		}
		for _, symptom := range pattern.Symptoms {
			if rootCausePatternTextOverlaps(goal, symptom) {
				score += 10
				matchedSymptoms = append(matchedSymptoms, symptom)
			}
		}
		for _, rootCause := range pattern.RootCauses {
			if rootCausePatternTextOverlaps(goal, rootCause) {
				score += 8
				matchedSymptoms = append(matchedSymptoms, rootCause)
			}
		}
		for _, signal := range append(pattern.CodeSignals, pattern.StateVariables...) {
			if rootCausePatternSignalMatchesCorpus(corpus, signal) {
				score += 8
				matchedSignals = append(matchedSignals, signal)
			}
		}
		for _, likely := range pattern.LikelyFiles {
			likelyLower := strings.ToLower(strings.TrimSpace(likely))
			if likelyLower == "" {
				continue
			}
			for _, file := range snapshot.Files {
				pathLower := strings.ToLower(filepath.ToSlash(file.Path))
				if strings.Contains(pathLower, likelyLower) {
					score += 7
					matchedFiles = append(matchedFiles, file.Path)
				}
			}
		}
		if score == projectTypeScore {
			continue
		}
		if score < 30 {
			continue
		}
		match := RootCausePatternMatch{
			PatternID:          pattern.ID,
			Title:              pattern.Title,
			ProjectTypes:       append([]string(nil), pattern.ProjectTypes...),
			Score:              score,
			Confidence:         pattern.Confidence,
			MatchedSymptoms:    limitStrings(analysisUniqueStrings(matchedSymptoms), 8),
			MatchedSignals:     limitStrings(analysisUniqueStrings(matchedSignals), 12),
			MatchedFiles:       limitStrings(analysisUniqueStrings(matchedFiles), 16),
			RootCauses:         limitStrings(append([]string(nil), pattern.RootCauses...), 3),
			VerificationProbes: limitStrings(append([]string(nil), pattern.VerificationProbes...), 4),
			SourceCount:        len(pattern.Sources),
		}
		matches = append(matches, match)
	}
	return limitRootCausePatternMatches(normalizeRootCausePatternMatches(matches), limit)
}

func rootCausePatternMatchCorpus(snapshot ProjectSnapshot, goal string) string {
	return rootCauseSnapshotTypeCorpus(snapshot, goal)
}

func rootCauseProjectTypeOverlaps(left []string, right []string) bool {
	if len(right) == 0 {
		return true
	}
	leftSet := map[string]struct{}{}
	for _, item := range normalizeRootCauseProjectTypes(left) {
		leftSet[item] = struct{}{}
	}
	for _, item := range normalizeRootCauseProjectTypes(right) {
		if item == "generic" {
			return true
		}
		if _, ok := leftSet[item]; ok {
			return true
		}
		if item == "server" {
			if _, ok := leftSet["web_backend"]; ok {
				return true
			}
		}
	}
	return false
}

func rootCausePatternTextOverlaps(left string, right string) bool {
	leftTerms := rootCauseEvidenceTerms(left)
	if len(leftTerms) == 0 {
		return false
	}
	rightLower := strings.ToLower(right)
	hits := 0
	for _, term := range leftTerms {
		if rootCauseTextContainsTerm(rightLower, term) {
			hits++
		}
	}
	return hits >= 1 && (hits >= 2 || len(leftTerms) <= 3)
}

func rootCausePatternSignalMatchesCorpus(corpus string, signal string) bool {
	signal = strings.ToLower(strings.TrimSpace(signal))
	if signal == "" {
		return false
	}
	if strings.Contains(corpus, signal) {
		return true
	}
	for _, term := range rootCauseEvidenceTerms(signal) {
		if len([]rune(term)) >= 4 && strings.Contains(corpus, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func normalizeRootCausePatternMatches(matches []RootCausePatternMatch) []RootCausePatternMatch {
	out := []RootCausePatternMatch{}
	seen := map[string]int{}
	for _, match := range matches {
		match.PatternID = strings.TrimSpace(match.PatternID)
		match.Title = strings.TrimSpace(match.Title)
		match.ProjectTypes = normalizeRootCauseProjectTypes(match.ProjectTypes)
		match.Confidence = normalizeRootCauseConfidence(match.Confidence, match.Score)
		match.MatchedSymptoms = analysisUniqueStrings(match.MatchedSymptoms)
		match.MatchedSignals = analysisUniqueStrings(match.MatchedSignals)
		match.MatchedFiles = analysisUniqueStrings(match.MatchedFiles)
		match.RootCauses = analysisUniqueStrings(match.RootCauses)
		match.VerificationProbes = analysisUniqueStrings(match.VerificationProbes)
		if match.Score < 0 {
			match.Score = 0
		}
		if match.PatternID == "" && match.Title == "" {
			continue
		}
		key := firstNonBlankRootCauseString(match.PatternID, rootCauseComparableText(match.Title))
		if existingIndex, ok := seen[key]; ok {
			if match.Score > out[existingIndex].Score {
				out[existingIndex].Score = match.Score
			}
			out[existingIndex].MatchedSymptoms = analysisUniqueStrings(append(out[existingIndex].MatchedSymptoms, match.MatchedSymptoms...))
			out[existingIndex].MatchedSignals = analysisUniqueStrings(append(out[existingIndex].MatchedSignals, match.MatchedSignals...))
			out[existingIndex].MatchedFiles = analysisUniqueStrings(append(out[existingIndex].MatchedFiles, match.MatchedFiles...))
			continue
		}
		seen[key] = len(out)
		out = append(out, match)
	}
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].PatternID < out[j].PatternID
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func limitRootCausePatternMatches(items []RootCausePatternMatch, limit int) []RootCausePatternMatch {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func applyRootCausePatternMatchesToImportance(snapshot *ProjectSnapshot, matches []RootCausePatternMatch) {
	if snapshot == nil || len(matches) == 0 {
		return
	}
	boosts := map[string]int{}
	reasons := map[string][]string{}
	for _, match := range matches {
		boost := analysisMinInt(30, analysisMaxInt(6, match.Score/5))
		for _, path := range match.MatchedFiles {
			clean := cleanEvidencePath(path)
			if clean == "" {
				continue
			}
			boosts[clean] += boost
			reasons[clean] = append(reasons[clean], "root_cause_pattern:"+match.PatternID)
		}
	}
	for index := range snapshot.Files {
		file := snapshot.Files[index]
		boost := boosts[file.Path]
		if boost <= 0 {
			continue
		}
		file.ImportanceScore += boost
		file.ImportanceReasons = analysisUniqueStrings(append(file.ImportanceReasons, reasons[file.Path]...))
		snapshot.Files[index] = file
		snapshot.FilesByPath[file.Path] = file
		if dirFiles := snapshot.FilesByDirectory[file.Directory]; len(dirFiles) > 0 {
			for j := range dirFiles {
				if dirFiles[j].Path == file.Path {
					dirFiles[j] = file
				}
			}
			snapshot.FilesByDirectory[file.Directory] = dirFiles
		}
	}
}

func deriveRootCausePatternCodeMatches(snapshot ProjectSnapshot, matches []RootCausePatternMatch, limit int) []RootCauseCodeMatch {
	out := []RootCauseCodeMatch{}
	for _, match := range matches {
		for _, file := range match.MatchedFiles {
			if strings.TrimSpace(file) == "" {
				continue
			}
			out = append(out, RootCauseCodeMatch{
				Query:          match.PatternID,
				File:           file,
				Reason:         "pattern_match:" + match.PatternID,
				MatchedSignals: analysisUniqueStrings(append([]string{match.Title}, match.MatchedSignals...)),
				Score:          analysisMinInt(90, analysisMaxInt(20, match.Score)),
			})
		}
	}
	out = normalizeRootCauseCodeMatches(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func rootCausePatternIDsForCandidate(candidate RootCauseCandidate, matches []RootCausePatternMatch) []string {
	out := append([]string(nil), rootCauseExplicitPatternIDsForCandidate(candidate, matches)...)
	out = append(out, rootCauseInferredRelatedPatternIDsForCandidate(candidate, matches)...)
	return limitStrings(analysisUniqueStrings(out), 5)
}

func rootCauseExplicitPatternIDsForCandidate(candidate RootCauseCandidate, matches []RootCausePatternMatch) []string {
	allowed := map[string]struct{}{}
	for _, match := range matches {
		if strings.TrimSpace(match.PatternID) != "" {
			allowed[match.PatternID] = struct{}{}
		}
	}
	out := []string{}
	for _, patternID := range candidate.PatternIDs {
		patternID = strings.TrimSpace(patternID)
		if patternID == "" {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[patternID]; !ok {
				continue
			}
		}
		out = append(out, patternID)
	}
	return limitStrings(analysisUniqueStrings(out), 5)
}

func rootCauseInferredRelatedPatternIDsForCandidate(candidate RootCauseCandidate, matches []RootCausePatternMatch) []string {
	explicit := map[string]struct{}{}
	for _, patternID := range rootCauseExplicitPatternIDsForCandidate(candidate, matches) {
		explicit[patternID] = struct{}{}
	}
	out := []string{}
	corpus := strings.ToLower(strings.Join([]string{
		candidate.Title,
		strings.Join(candidate.CandidateChain, " "),
		candidate.CausalChain.Trigger,
		candidate.CausalChain.InvalidState,
		candidate.CausalChain.StateTransition,
		candidate.CausalChain.MissingGuard,
		candidate.CausalChain.UserVisibleSymptom,
		strings.Join(candidate.EvidenceFiles, " "),
		strings.Join(candidate.TriggerValues, " "),
		strings.Join(candidate.OutOfRangeCases, " "),
	}, " "))
	for _, match := range matches {
		if _, ok := explicit[match.PatternID]; ok {
			continue
		}
		if strings.Contains(corpus, strings.ToLower(match.PatternID)) {
			out = append(out, match.PatternID)
			continue
		}
		hits := 0
		for _, signal := range append(match.MatchedSignals, match.MatchedFiles...) {
			if rootCausePatternSignalMatchesCorpus(corpus, signal) {
				hits++
			}
		}
		for _, rootCause := range match.RootCauses {
			if rootCausePatternTextOverlaps(corpus, rootCause) {
				hits++
			}
		}
		if hits > 0 {
			out = append(out, match.PatternID)
		}
	}
	return limitStrings(analysisUniqueStrings(out), 5)
}

func markRootCausePatternMatchUsage(matches []RootCausePatternMatch, reports []WorkerReport, reviews []ReviewDecision, finalReports []WorkerReport) []RootCausePatternMatch {
	out := normalizeRootCausePatternMatches(matches)
	if len(out) == 0 {
		return out
	}
	byID := map[string]int{}
	for index, match := range out {
		byID[match.PatternID] = index
	}
	mark := func(patternIDs []string, accepted bool) {
		for _, patternID := range patternIDs {
			if index, ok := byID[strings.TrimSpace(patternID)]; ok {
				out[index].UsedByWorker = true
				if accepted {
					out[index].AcceptedByReviewer = true
				}
			}
		}
	}
	for reportIndex, report := range reports {
		review := ReviewDecision{}
		if reportIndex < len(reviews) {
			review = reviews[reportIndex]
		}
		accepted := rootCauseReviewApprovesSymptomCausality(review)
		for _, candidate := range report.RootCauseCandidates {
			mark(candidate.PatternIDs, accepted)
		}
	}
	for _, report := range finalReports {
		for _, candidate := range report.RootCauseCandidates {
			mark(candidate.PatternIDs, true)
		}
	}
	return out
}

func renderRootCausePatternMatchesForPrompt(matches []RootCausePatternMatch, limit int) string {
	matches = limitRootCausePatternMatches(normalizeRootCausePatternMatches(matches), limit)
	if len(matches) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Known root-cause pattern priors:\n")
	b.WriteString("- Treat these as search priors only. They are not proof unless current source evidence completes the causal chain.\n")
	for _, match := range matches {
		fmt.Fprintf(&b, "- %s score=%d confidence=%s types=%s\n", match.PatternID, match.Score, match.Confidence, strings.Join(match.ProjectTypes, ", "))
		if strings.TrimSpace(match.Title) != "" {
			fmt.Fprintf(&b, "  title=%s\n", match.Title)
		}
		if len(match.RootCauses) > 0 {
			fmt.Fprintf(&b, "  root_causes=%s\n", strings.Join(limitStrings(match.RootCauses, 3), " | "))
		}
		if len(match.MatchedSignals) > 0 {
			fmt.Fprintf(&b, "  matched_signals=%s\n", strings.Join(limitStrings(match.MatchedSignals, 8), ", "))
		}
		if len(match.MatchedFiles) > 0 {
			fmt.Fprintf(&b, "  matched_files=%s\n", strings.Join(limitStrings(match.MatchedFiles, 8), ", "))
		}
		if len(match.VerificationProbes) > 0 {
			fmt.Fprintf(&b, "  verification_probes=%s\n", strings.Join(limitStrings(match.VerificationProbes, 3), " | "))
		}
	}
	return strings.TrimSpace(b.String())
}

func searchRootCauseGitHubIssues(ctx context.Context, client *http.Client, cfg rootCauseGitHubSearchConfig) (RootCauseGitHubIssueCorpus, error) {
	if client == nil {
		client = http.DefaultClient
	}
	apiURL := strings.TrimSpace(cfg.APIURL)
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	queries := cfg.Queries
	if len(queries) == 0 {
		queries = rootCauseGitHubDefaultQueries(cfg.ProjectTypes)
	}
	if len(queries) == 0 {
		return RootCauseGitHubIssueCorpus{}, fmt.Errorf("github search query is empty")
	}
	corpus := RootCauseGitHubIssueCorpus{
		GeneratedAt:  time.Now(),
		APIURL:       apiURL,
		Queries:      append([]string(nil), queries...),
		ProjectTypes: normalizeRootCauseProjectTypes(cfg.ProjectTypes),
	}
	seen := map[string]struct{}{}
	for _, query := range queries {
		if rootCauseGitHubQueryRequestsPullRequests(query) {
			return corpus, fmt.Errorf("github root-cause issue search only supports issues; remove pull-request qualifier from query %q", query)
		}
		originalQuery := query
		query = rootCauseGitHubIssueQuery(originalQuery)
		endpoint, err := url.Parse(strings.TrimRight(apiURL, "/") + "/search/issues")
		if err != nil {
			return corpus, err
		}
		values := endpoint.Query()
		values.Set("q", query)
		values.Set("sort", "updated")
		values.Set("order", "desc")
		values.Set("per_page", fmt.Sprintf("%d", limit))
		endpoint.RawQuery = values.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return corpus, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "kernforge-root-cause-patterns")
		if strings.TrimSpace(cfg.Token) != "" {
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.Token))
		}
		resp, err := client.Do(req)
		if err != nil {
			return corpus, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return corpus, readErr
		}
		if closeErr != nil {
			return corpus, closeErr
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			corpus.ExecutedQueries = append(corpus.ExecutedQueries, query)
			corpus.QueryResults = append(corpus.QueryResults, RootCauseGitHubQueryResult{
				Query:              originalQuery,
				ExecutedQuery:      query,
				APIURL:             endpoint.String(),
				StatusCode:         resp.StatusCode,
				FetchedAt:          time.Now(),
				RateLimitRemaining: resp.Header.Get("X-RateLimit-Remaining"),
				RateLimitReset:     resp.Header.Get("X-RateLimit-Reset"),
			})
			return corpus, fmt.Errorf("github search failed: status=%d body=%s", resp.StatusCode, analysisPromptExcerpt(string(body), 500))
		}
		payload, err := parseRootCauseGitHubSearchPayload(body)
		if err != nil {
			return corpus, err
		}
		corpus.ExecutedQueries = append(corpus.ExecutedQueries, query)
		corpus.QueryResults = append(corpus.QueryResults, RootCauseGitHubQueryResult{
			Query:              originalQuery,
			ExecutedQuery:      query,
			APIURL:             endpoint.String(),
			StatusCode:         resp.StatusCode,
			TotalCount:         payload.TotalCount,
			Fetched:            len(payload.Items),
			FetchedAt:          time.Now(),
			RateLimitRemaining: resp.Header.Get("X-RateLimit-Remaining"),
			RateLimitReset:     resp.Header.Get("X-RateLimit-Reset"),
		})
		for _, item := range payload.Items {
			key := item.HTMLURL
			if key == "" {
				key = fmt.Sprintf("%s#%d", item.Repository, item.Number)
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			corpus.Items = append(corpus.Items, item)
		}
	}
	sort.SliceStable(corpus.Items, func(i int, j int) bool {
		if corpus.Items[i].Score == corpus.Items[j].Score {
			return corpus.Items[i].UpdatedAt.After(corpus.Items[j].UpdatedAt)
		}
		return corpus.Items[i].Score > corpus.Items[j].Score
	})
	return corpus, nil
}

func rootCauseGitHubIssueQuery(query string) string {
	query = strings.TrimSpace(query)
	lower := strings.ToLower(query)
	if !strings.Contains(lower, "is:issue") && !strings.Contains(lower, "type:issue") {
		query += " is:issue"
	}
	if !strings.Contains(lower, "state:") && !strings.Contains(lower, "is:closed") && !strings.Contains(lower, "is:open") {
		query += " state:closed"
	}
	if !strings.Contains(lower, "label:") {
		query += " bug"
	}
	return strings.TrimSpace(query)
}

func rootCauseGitHubQueryRequestsPullRequests(query string) bool {
	lower := strings.ToLower(query)
	for _, token := range []string{"is:pr", "is:pull-request", "is:pull_request", "type:pr", "type:pull-request", "type:pull_request"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func rootCauseGitHubDefaultQueries(projectTypes []string) []string {
	out := []string{}
	for _, projectType := range normalizeRootCauseProjectTypes(projectTypes) {
		switch projectType {
		case "windows_user_service":
			out = append(out, `"SERVICE_CONTROL_STOP" "SetServiceStatus"`, `"sc stop" service "STOP_PENDING"`)
		case "windows_kernel_driver":
			out = append(out, `"IoMarkIrpPending" "IoSetCancelRoutine" driver`, `"IRQL_NOT_LESS_OR_EQUAL" driver`)
		case "unreal5_game_client", "unreal5_game_server":
			out = append(out, `"Unreal" "Server RPC" "HasAuthority"`, `"Unreal" party invite kick limit`)
		case "web_backend", "server":
			out = append(out, `"idempotency key" duplicate request`, `"FOR UPDATE" quota race`)
		case "web_frontend":
			out = append(out, `"useEffect" "stale" "closure" bug`, `"optimistic update" rollback`)
		case "go_cli_agent":
			out = append(out, `"write_file" "readOnly" agent`, `"context compaction" artifact`)
		}
	}
	return analysisUniqueStrings(out)
}

type rootCauseGitHubSearchPayload struct {
	TotalCount int
	Items      []RootCauseGitHubIssue
}

func parseRootCauseGitHubSearchResponse(data []byte) ([]RootCauseGitHubIssue, error) {
	payload, err := parseRootCauseGitHubSearchPayload(data)
	if err != nil {
		return nil, err
	}
	return payload.Items, nil
}

func parseRootCauseGitHubSearchPayload(data []byte) (rootCauseGitHubSearchPayload, error) {
	type label struct {
		Name string `json:"name"`
	}
	type repository struct {
		FullName string `json:"full_name"`
	}
	type item struct {
		Number      int             `json:"number"`
		Title       string          `json:"title"`
		Body        string          `json:"body"`
		State       string          `json:"state"`
		HTMLURL     string          `json:"html_url"`
		Labels      []label         `json:"labels"`
		Repository  repository      `json:"repository"`
		CreatedAt   *time.Time      `json:"created_at"`
		UpdatedAt   *time.Time      `json:"updated_at"`
		ClosedAt    *time.Time      `json:"closed_at"`
		PullRequest json.RawMessage `json:"pull_request,omitempty"`
	}
	payload := struct {
		TotalCount int    `json:"total_count"`
		Items      []item `json:"items"`
	}{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return rootCauseGitHubSearchPayload{}, err
	}
	out := []RootCauseGitHubIssue{}
	for _, raw := range payload.Items {
		pullRequestMarker := strings.TrimSpace(string(raw.PullRequest))
		if pullRequestMarker != "" && pullRequestMarker != "null" {
			continue
		}
		labels := []string{}
		for _, itemLabel := range raw.Labels {
			labels = append(labels, itemLabel.Name)
		}
		issue := RootCauseGitHubIssue{
			Repository: strings.TrimSpace(raw.Repository.FullName),
			Number:     raw.Number,
			Title:      strings.TrimSpace(raw.Title),
			Body:       strings.TrimSpace(raw.Body),
			State:      strings.TrimSpace(raw.State),
			HTMLURL:    strings.TrimSpace(raw.HTMLURL),
			Labels:     analysisUniqueStrings(labels),
		}
		if raw.CreatedAt != nil {
			issue.CreatedAt = *raw.CreatedAt
		}
		if raw.UpdatedAt != nil {
			issue.UpdatedAt = *raw.UpdatedAt
		}
		if raw.ClosedAt != nil {
			issue.ClosedAt = *raw.ClosedAt
		}
		issue.Score = scoreRootCauseGitHubIssue(issue)
		issue = normalizeRootCauseGitHubIssueQuality(issue)
		out = append(out, issue)
	}
	return rootCauseGitHubSearchPayload{TotalCount: payload.TotalCount, Items: out}, nil
}

func scoreRootCauseGitHubIssue(issue RootCauseGitHubIssue) int {
	score := 0
	if strings.EqualFold(issue.State, "closed") {
		score += 20
	}
	labelText := strings.ToLower(strings.Join(issue.Labels, " "))
	if strings.Contains(labelText, "bug") {
		score += 20
	}
	text := strings.ToLower(issue.Title + "\n" + issue.Body)
	for _, token := range []string{"fix", "fixed", "root cause", "race", "regression", "test", "repro", "crash", "hang", "deadlock", "leak", "overflow", "off by one", "idempot"} {
		if strings.Contains(text, token) {
			score += 6
		}
	}
	if !issue.ClosedAt.IsZero() {
		score += 8
	}
	if issue.Repository != "" {
		score += 3
	}
	return score
}

func normalizeRootCauseGitHubIssueQuality(issue RootCauseGitHubIssue) RootCauseGitHubIssue {
	quality, reasons := assessRootCauseGitHubIssueQuality(issue)
	issue.Quality = quality
	issue.QualityReasons = reasons
	return issue
}

func assessRootCauseGitHubIssueQuality(issue RootCauseGitHubIssue) (string, []string) {
	reasons := []string{}
	closed := strings.EqualFold(strings.TrimSpace(issue.State), "closed") || !issue.ClosedAt.IsZero()
	hasURL := strings.TrimSpace(issue.HTMLURL) != ""
	hasBug := rootCauseGitHubIssueHasBugSignal(issue)
	hasFix := rootCauseGitHubIssueHasRootCauseOrFixSignal(issue)
	hasCodeSignal := len(rootCauseGitHubCodeSignals(issue)) > 0 || len(rootCauseGitHubLikelyFiles(issue)) > 0
	if !closed {
		reasons = append(reasons, "issue is not closed")
	}
	if !hasURL {
		reasons = append(reasons, "source issue URL is missing")
	}
	if !hasBug {
		reasons = append(reasons, "bug label or bug text signal is missing")
	}
	if !hasFix {
		reasons = append(reasons, "root-cause or fix language is missing")
	}
	if !hasCodeSignal {
		reasons = append(reasons, "code signal or likely file path is missing")
	}
	if closed && hasURL && hasBug && hasFix && hasCodeSignal {
		return "promotable", []string{"closed bug issue contains fix/root-cause language and code signals"}
	}
	if closed && hasURL && (hasFix || hasCodeSignal) {
		return "provisional", reasons
	}
	return "rejected", reasons
}

func rootCauseGitHubIssueHasBugSignal(issue RootCauseGitHubIssue) bool {
	labelText := strings.ToLower(strings.Join(issue.Labels, " "))
	if strings.Contains(labelText, "bug") || strings.Contains(labelText, "defect") || strings.Contains(labelText, "regression") {
		return true
	}
	text := strings.ToLower(issue.Title + "\n" + issue.Body)
	return strings.Contains(text, "bug") || strings.Contains(text, "regression") || strings.Contains(text, "crash") || strings.Contains(text, "hang") || strings.Contains(text, "wrong")
}

func rootCauseGitHubIssueHasRootCauseOrFixSignal(issue RootCauseGitHubIssue) bool {
	text := strings.ToLower(issue.Title + "\n" + issue.Body)
	for _, token := range []string{"root cause", "caused by", "because", "fix", "fixed", "race", "missing", "not updated", "stale", "overflow", "off by one", "rollback", "idempot"} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func normalizeGitHubIssuesToRootCausePatternPack(corpus RootCauseGitHubIssueCorpus, projectTypes []string) RootCausePatternPack {
	projectTypes = normalizeRootCauseProjectTypes(append(projectTypes, corpus.ProjectTypes...))
	pack := RootCausePatternPack{
		Version:     "1.0",
		Description: "Provisional root-cause patterns normalized from GitHub issues. Review before promoting to builtin.",
	}
	for _, issue := range corpus.Items {
		issue = normalizeRootCauseGitHubIssueQuality(issue)
		if strings.EqualFold(issue.Quality, "rejected") {
			continue
		}
		issueProjectTypes := projectTypes
		if len(issueProjectTypes) == 0 {
			issueProjectTypes = inferRootCauseProjectTypes(ProjectSnapshot{}, issue.Title+"\n"+issue.Body)
		}
		pattern := RootCausePattern{
			ID:                 rootCauseGitHubPatternID(issue),
			Title:              issue.Title,
			ProjectTypes:       issueProjectTypes,
			Symptoms:           rootCauseGitHubSymptomSentences(issue),
			RootCauses:         rootCauseGitHubRootCauseSentences(issue),
			CodeSignals:        rootCauseGitHubCodeSignals(issue),
			StateVariables:     rootCauseGitHubStateVariables(issue),
			OutOfRangeCases:    rootCauseGitHubOutOfRangeCases(issue),
			LikelyFiles:        rootCauseGitHubLikelyFiles(issue),
			VerificationProbes: rootCauseGitHubVerificationProbes(issue),
			Confidence:         rootCauseConfidenceFromGitHubScore(issue.Score),
			Tags:               analysisUniqueStrings(append(rootCauseGitHubTags(issue), "github_quality:"+issue.Quality)),
			Sources: []RootCausePatternSource{{
				Type:       "github_issue",
				URL:        issue.HTMLURL,
				Repository: issue.Repository,
				Evidence:   "Closed issue normalized by kernforge; quality=" + issue.Quality + "; reviewer should verify linked PR/commit before promoting.",
				Confidence: rootCauseConfidenceFromGitHubScore(issue.Score),
			}},
		}
		pack.Patterns = append(pack.Patterns, pattern)
	}
	return normalizeRootCausePatternPack(pack)
}

func rootCauseGitHubPatternID(issue RootCauseGitHubIssue) string {
	repo := rootCauseASCIIIdentifierSlug(issue.Repository, 80)
	title := rootCauseASCIIIdentifierSlug(issue.Title, 48)
	if repo == "" {
		repo = "github"
	}
	if title == "" {
		sum := sha256.Sum256([]byte(issue.Title))
		title = hex.EncodeToString(sum[:])[:12]
	}
	return strings.Trim(fmt.Sprintf("github_%s_%d_%s", repo, issue.Number, title), "_")
}

func rootCauseASCIIIdentifierSlug(text string, limit int) string {
	text = strings.ToLower(strings.TrimSpace(text))
	var b strings.Builder
	lastSeparator := false
	for _, r := range text {
		isAlphaNumeric := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNumeric {
			b.WriteRune(r)
			lastSeparator = false
		}
		if !isAlphaNumeric {
			if b.Len() > 0 && !lastSeparator {
				b.WriteByte('_')
				lastSeparator = true
			}
		}
		if limit > 0 && b.Len() >= limit {
			break
		}
	}
	return strings.Trim(b.String(), "_")
}

func rootCauseGitHubSymptomSentences(issue RootCauseGitHubIssue) []string {
	return limitStrings(rootCauseInterestingSentences(issue.Title+"\n"+issue.Body, []string{"fail", "bug", "crash", "hang", "stuck", "wrong", "duplicate", "missing", "cannot", "does not", "doesn't", "timeout", "limit", "leak"}), 4)
}

func rootCauseGitHubRootCauseSentences(issue RootCauseGitHubIssue) []string {
	out := rootCauseInterestingSentences(issue.Body, []string{"root cause", "because", "caused by", "fix", "race", "missing", "not updated", "stale", "overflow", "off by one", "cancel", "rollback", "idempot"})
	if len(out) == 0 {
		out = []string{"Review linked fix to confirm the exact root cause."}
	}
	return limitStrings(out, 4)
}

func rootCauseGitHubCodeSignals(issue RootCauseGitHubIssue) []string {
	text := issue.Title + "\n" + issue.Body
	signals := regexp.MustCompile("`([^`]{3,80})`").FindAllStringSubmatch(text, -1)
	out := []string{}
	for _, match := range signals {
		out = append(out, strings.TrimSpace(match[1]))
	}
	for _, token := range []string{"SetServiceStatus", "SERVICE_CONTROL_STOP", "IoMarkIrpPending", "IoSetCancelRoutine", "IRQL_NOT_LESS_OR_EQUAL", "HasAuthority", "Server RPC", "idempotency", "FOR UPDATE", "useEffect", "rollback"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(token)) {
			out = append(out, token)
		}
	}
	return limitStrings(analysisUniqueStrings(out), 12)
}

func rootCauseGitHubStateVariables(issue RootCauseGitHubIssue) []string {
	text := issue.Title + "\n" + issue.Body
	out := []string{}
	for _, token := range regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*(Count|Limit|Size|ID|Id|Key|Flag|Status|Handle|State|Version|Event|Request|Response|Balance|Offset)\b`).FindAllString(text, -1) {
		out = append(out, token)
	}
	return limitStrings(analysisUniqueStrings(out), 10)
}

func rootCauseGitHubOutOfRangeCases(issue RootCauseGitHubIssue) []string {
	return limitStrings(rootCauseInterestingSentences(issue.Body, []string{"null", "zero", "negative", "overflow", "stale", "duplicate", "missing", "invalid", "out of range", "timeout"}), 4)
}

func rootCauseGitHubLikelyFiles(issue RootCauseGitHubIssue) []string {
	text := issue.Title + "\n" + issue.Body
	out := []string{}
	for _, token := range regexp.MustCompile(`\b[A-Za-z0-9_\-/]+\.(go|cpp|cc|cxx|c|h|hpp|cs|ts|tsx|js|jsx|py|rs|java|kt)\b`).FindAllString(text, -1) {
		out = append(out, filepath.ToSlash(token))
	}
	for _, token := range []string{"service", "driver", "ioctl", "party", "inventory", "auth", "cache", "transaction", "pagination", "component", "agent", "completion"} {
		if strings.Contains(strings.ToLower(text), token) {
			out = append(out, token)
		}
	}
	return limitStrings(analysisUniqueStrings(out), 10)
}

func rootCauseGitHubVerificationProbes(issue RootCauseGitHubIssue) []string {
	out := []string{}
	text := strings.ToLower(issue.Title + "\n" + issue.Body)
	switch {
	case strings.Contains(text, "test"):
		out = append(out, "Run the regression test linked by the issue or PR.")
	case strings.Contains(text, "race") || strings.Contains(text, "concurrent"):
		out = append(out, "Reproduce with concurrent requests or event interleaving.")
	case strings.Contains(text, "crash") || strings.Contains(text, "panic"):
		out = append(out, "Run the failing input under debugger or sanitizer and capture the crashing state.")
	default:
		out = append(out, "Inspect linked fix and add a regression probe for the reported symptom.")
	}
	return out
}

func rootCauseGitHubTags(issue RootCauseGitHubIssue) []string {
	text := strings.ToLower(issue.Title + "\n" + issue.Body + "\n" + strings.Join(issue.Labels, " "))
	out := []string{}
	for _, token := range []string{"race", "crash", "hang", "leak", "auth", "cache", "transaction", "pagination", "replication", "shutdown", "driver", "service", "unreal", "frontend", "backend"} {
		if strings.Contains(text, token) {
			out = append(out, token)
		}
	}
	return analysisUniqueStrings(out)
}

func rootCauseInterestingSentences(text string, keywords []string) []string {
	out := []string{}
	for _, sentence := range regexp.MustCompile(`[.!?\n]+`).Split(text, -1) {
		sentence = strings.TrimSpace(sentence)
		if len(sentence) < 8 {
			continue
		}
		lower := strings.ToLower(sentence)
		for _, keyword := range keywords {
			if strings.Contains(lower, strings.ToLower(keyword)) {
				out = append(out, analysisPromptExcerpt(sentence, 220))
				break
			}
		}
	}
	return analysisUniqueStrings(out)
}

func rootCauseConfidenceFromGitHubScore(score int) string {
	if score >= 60 {
		return "high"
	}
	if score >= 35 {
		return "medium"
	}
	return "low"
}

func lowerStrings(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, strings.ToLower(item))
	}
	return out
}
