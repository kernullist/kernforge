package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var reviewScopeSymbolPattern = regexp.MustCompile(`\b[A-Za-z_~][A-Za-z0-9_~]*(?:::[A-Za-z_~][A-Za-z0-9_~]*)*(?:\(\))?`)
var reviewScopeWindowsRootFragmentPattern = regexp.MustCompile(`^[a-z]:/win(?:dows)?/?$`)
var reviewScopeDriveRootFragmentPattern = regexp.MustCompile(`^[a-z]:/?$`)
var reviewScopeFindingLabelFragmentPattern = regexp.MustCompile(`^(?:blocker|high|medium|low|info)/(?:correctness|stability|security|performance|maintainability|testability|evidence|evidence_gap|test_gap|warning|risk)$`)
var reviewScopeDiffMarkerFragmentPattern = regexp.MustCompile(`^[+\-/]+$`)
var reviewScopeCodeLiteralPathFragmentPattern = regexp.MustCompile("(?i)^(?:u8|[ulr])?['\"`].*[/\\\\]")

const (
	reviewScopeWorkspaceSearchReadableFileLimit = 50000
	reviewScopeWorkspaceSearchResultLimit       = 32
	reviewScopeLargeFileScanLimit               = 4096
	reviewScopeRipgrepTimeout                   = 5 * time.Second
	reviewScopeRipgrepResultLimit               = 128
)

type reviewScopeSignalRule struct {
	domain      string
	risk        string
	severity    string
	confidence  string
	searchTerms []string
	keywords    []string
}

func discoverReviewScope(root string, request string, paths []string) ReviewScopeDiscovery {
	candidateFiles := reviewScopeCandidateFiles(root, request, paths)
	candidateSymbols := reviewScopeFilterPathDerivedSymbols(reviewScopeCandidateSymbols(request), candidateFiles)
	searchTerms := reviewScopeSearchTerms(request, candidateFiles)
	scopeWidth, confidence := reviewScopeWidth(request, candidateFiles, candidateSymbols)
	discovery := ReviewScopeDiscovery{
		CandidateFiles:    candidateFiles,
		CandidateSymbols:  candidateSymbols,
		SearchTerms:       searchTerms,
		ScopeWidth:        scopeWidth,
		Confidence:        confidence,
		NarrowingCommands: reviewScopeNarrowingCommands(candidateFiles, candidateSymbols, searchTerms),
	}
	if reviewScopeDiscoveryNeedsNarrowing(discovery) {
		discovery.Warnings = append(discovery.Warnings, "deterministic scope discovery found a broad review target; rerun with a narrower path, symbol, or search result before treating findings as complete")
	}
	return discovery
}

func reviewScopeFilterPathDerivedSymbols(symbols []string, candidateFiles []string) []string {
	if len(symbols) == 0 || len(candidateFiles) == 0 {
		return symbols
	}
	pathDerived := map[string]bool{}
	for _, file := range candidateFiles {
		base := strings.TrimSpace(filepath.Base(filepath.ToSlash(file)))
		if base == "." || base == "/" || base == "" {
			continue
		}
		pathDerived[strings.ToLower(base)] = true
		withoutExt := strings.TrimSuffix(base, filepath.Ext(base))
		if withoutExt != "" {
			pathDerived[strings.ToLower(withoutExt)] = true
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, symbol := range symbols {
		symbol = strings.TrimSpace(symbol)
		key := strings.ToLower(strings.TrimSuffix(symbol, "()"))
		if key == "" || seen[key] || pathDerived[key] {
			continue
		}
		seen[key] = true
		out = append(out, symbol)
	}
	return out
}

func reviewScopeSignals(discovery ReviewScopeDiscovery, request string) ([]ReviewDomainSignal, []ReviewRiskSignal) {
	text := strings.ToLower(strings.TrimSpace(request + " " + strings.Join(discovery.CandidateFiles, " ") + " " + strings.Join(discovery.CandidateSymbols, " ")))
	rules := []reviewScopeSignalRule{
		{
			domain:      "windows_service_control",
			risk:        "privileged_service_control",
			severity:    reviewSeverityMedium,
			confidence:  "high",
			searchTerms: []string{"CreateService", "StartService", "OpenSCManager", "ControlService", "DeleteService"},
			keywords: []string{
				"createservice", "createservicew", "startservice", "startservicew", "openscmanager", "openscmanagerw",
				"controlservice", "deleteservice", "service install", "service start", "service stop", "scm", "service_control_manager",
				"windows service", "서비스", "서비스 설치", "서비스를 설치", "서비스 실행", "서비스를 실행", "서비스 시작",
				"서비스 중지", "서비스 등록", "서비스 제어",
			},
		},
		{
			domain:      "windows_kernel_driver",
			risk:        "kernel_boundary",
			severity:    reviewSeverityHigh,
			confidence:  "high",
			searchTerms: []string{"DriverEntry", "IRP_MJ_DEVICE_CONTROL", "DeviceIoControl", "IOCTL", "MmCopyMemory", "ProbeForRead", "ProbeForWrite"},
			keywords: []string{
				".sys", "driverentry", "deviceiocontrol", "irp_mj_device_control", "ioctl", "irql", "mmcopymemory",
				"probeforread", "probeforwrite", "exallocatepool", "verifier", "inf", "testsign", "커널", "드라이버",
			},
		},
		{
			domain:      "anti_cheat_detection",
			risk:        "false_positive_or_bypass_surface",
			severity:    reviewSeverityMedium,
			confidence:  "high",
			searchTerms: []string{"telemetry", "ETW", "EventWrite", "TraceLogging", "scan", "detection", "spoof", "evasion", "SetWindowsHookEx", "ImageLoad", "DllInjection"},
			keywords: []string{
				"anti-cheat", "anticheat", "anti_cheat", "telemetry", "etw", "eventwrite", "tracelogging",
				"detection", "detect", "false positive", "false-positive", "false_positive", "bypass", "evasion", "spoof",
				"안티치트", "탐지", "텔레메트리", "오탐", "우회",
				"setwindowshook", "setwindowshookex", "dll injection", "dllinjection", "dll 인젝션",
				"hook injection", "훅 인젝션", "proxydll", "proxy dll", "imageload",
			},
		},
		{
			domain:      "memory_scan",
			risk:        "memory_inspection_safety",
			severity:    reviewSeverityMedium,
			confidence:  "medium",
			searchTerms: []string{"VirtualQuery", "ReadProcessMemory", "MmCopyMemory", "VAD", "PTE", "page table"},
			keywords: []string{
				"memory scan", "memory scanner", "readprocessmemory", "virtualquery", "vad", "pte", "page table",
				"메모리", "스캔",
			},
		},
		{
			domain:      "unreal_integrity",
			risk:        "engine_object_integrity",
			severity:    reviewSeverityMedium,
			confidence:  "medium",
			searchTerms: []string{"UObject", "UFunction", "ProcessEvent", "FName", "UWorld"},
			keywords: []string{
				"unreal", "ue5", "uobject", "ufunction", "processevent", "fname", "uworld",
			},
		},
	}
	var domains []ReviewDomainSignal
	var risks []ReviewRiskSignal
	seenDomains := map[string]bool{}
	seenRisks := map[string]bool{}
	for _, rule := range rules {
		evidence := reviewScopeMatchedKeyword(text, rule.keywords)
		if evidence == "" {
			continue
		}
		if !seenDomains[rule.domain] {
			domains = append(domains, ReviewDomainSignal{
				Domain:     rule.domain,
				Signal:     strings.Join(rule.searchTerms, "|"),
				Evidence:   evidence,
				Confidence: rule.confidence,
			})
			seenDomains[rule.domain] = true
		}
		if rule.risk != "" && !seenRisks[rule.risk] {
			risks = append(risks, ReviewRiskSignal{
				Risk:       rule.risk,
				Signal:     strings.Join(rule.searchTerms, "|"),
				Evidence:   evidence,
				Severity:   rule.severity,
				Confidence: rule.confidence,
			})
			seenRisks[rule.risk] = true
		}
	}
	return domains, risks
}

func reviewScopeCandidateFiles(root string, request string, paths []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(path string) {
		path = reviewScopeNormalizeCandidatePath(root, path)
		if path == "" || reviewScopeCandidatePathLooksSynthetic(path) || shouldSkipMCPReviewFile(path) || seen[strings.ToLower(path)] {
			return
		}
		seen[strings.ToLower(path)] = true
		out = append(out, path)
	}
	for _, path := range mcpReviewCleanPaths(paths) {
		add(path)
	}
	for _, token := range strings.Fields(request) {
		if selection, ok := parseReviewMentionSelection(root, token); ok {
			add(selection.FilePath)
			continue
		}
		if path, ok := parseReviewMentionPath(root, token); ok {
			add(path)
			continue
		}
		cleaned := strings.Trim(token, " \t\r\n\"'`<>.,;()[]{}")
		if reviewScopeTokenLooksLikePath(cleaned) {
			add(cleaned)
		}
	}
	if len(out) == 0 {
		for _, path := range reviewScopeWorkspaceSearchCandidateFiles(root, request) {
			add(path)
			if len(out) >= reviewScopeWorkspaceSearchResultLimit {
				break
			}
		}
	}
	if len(out) == 0 && reviewScopeGitStatusLooksUsable(root) {
		symbols := reviewScopeCandidateSymbols(request)
		searchTerms := reviewScopeSearchTerms(request, nil)
		queryTerms := reviewScopeSearchQueryTerms(request, symbols, searchTerms)
		filterGitFallback := len(symbols) > 0 || len(searchTerms) > 0
		for _, path := range delegationChangedFiles(root) {
			if filterGitFallback && reviewScopeSearchPathScore(path, symbols, searchTerms, queryTerms) <= 0 {
				continue
			}
			add(path)
			if len(out) >= 64 {
				break
			}
		}
	}
	// "Read the design doc and fix the code" collapses scope to the document
	// itself: the only candidate is the .md, and nothing follows the doc's
	// references into source. When the request wants source changes but every
	// candidate is a document artifact, mine the docs for identifiers and pull
	// the referenced source files into scope so the reviewer sees the code.
	if len(out) > 0 && reviewScopeAllDocumentArtifacts(out) &&
		requestHasSourceModificationIntent(strings.ToLower(request), paths) {
		for _, extra := range reviewScopeExpandFromDocumentReferences(root, out) {
			add(extra)
			if len(out) >= 64 {
				break
			}
		}
	}
	sort.Strings(out)
	return limitStrings(out, 64)
}

func reviewScopeAllDocumentArtifacts(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if !reviewPathIsDocumentArtifact(p) {
			return false
		}
	}
	return true
}

// reviewScopeExpandFromDocumentReferences reads the given document artifacts,
// mines identifier-like references (dotted names such as RegGit.Core, source
// file names, path-like tokens), and resolves them to executable source files
// in the workspace. It is bounded in both bytes read and terms used, and only
// returns executable-source candidates so it never re-adds documents.
func reviewScopeExpandFromDocumentReferences(root string, docPaths []string) []string {
	terms := reviewScopeMineDocumentReferenceTerms(root, docPaths)
	if len(terms) == 0 {
		return nil
	}
	query := strings.Join(terms, " ")
	var out []string
	seen := map[string]bool{}
	for _, path := range reviewScopeWorkspaceSearchCandidateFiles(root, query) {
		if !reviewPathIsExecutableSource(path) {
			continue
		}
		key := strings.ToLower(filepath.ToSlash(path))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, path)
		if len(out) >= reviewScopeWorkspaceSearchResultLimit {
			break
		}
	}
	return out
}

var reviewDocReferenceTokenPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:[.\\/][A-Za-z0-9_]+)+`)

// reviewScopeMineDocumentReferenceTerms extracts up to a bounded number of
// identifier/path reference terms from the document artifacts, reading at most
// a bounded prefix of each file.
func reviewScopeMineDocumentReferenceTerms(root string, docPaths []string) []string {
	const maxBytesPerDoc = 64 * 1024
	const maxTerms = 40
	seen := map[string]bool{}
	var terms []string
	for _, doc := range docPaths {
		resolved := doc
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(root, doc)
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			continue
		}
		if len(data) > maxBytesPerDoc {
			data = data[:maxBytesPerDoc]
		}
		for _, match := range reviewDocReferenceTokenPattern.FindAllString(string(data), -1) {
			// Reduce a dotted/path reference to its leaf and its head so both the
			// project name (RegGit) and the type/file (Core, DatabaseService) become
			// search terms.
			for _, part := range regexp.MustCompile(`[.\\/]`).Split(match, -1) {
				part = strings.TrimSpace(part)
				if len(part) < 4 {
					continue
				}
				key := strings.ToLower(part)
				if seen[key] {
					continue
				}
				seen[key] = true
				terms = append(terms, part)
				if len(terms) >= maxTerms {
					return terms
				}
			}
		}
	}
	return terms
}

type reviewScopeWorkspaceCandidate struct {
	path  string
	score int
}

func reviewScopeWorkspaceSearchCandidateFiles(root string, request string) []string {
	root = strings.TrimSpace(root)
	request = strings.TrimSpace(request)
	if root == "" || request == "" {
		return nil
	}
	symbols := reviewScopeCandidateSymbols(request)
	searchTerms := reviewScopeSearchTerms(request, nil)
	queryTerms := reviewScopeSearchQueryTerms(request, symbols, searchTerms)
	if len(queryTerms) == 0 {
		return nil
	}
	var candidates []reviewScopeWorkspaceCandidate
	candidates = append(candidates, reviewScopeRipgrepCandidateFiles(root, symbols, searchTerms, queryTerms)...)
	visited := 0
	largeContentScans := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel := filepath.ToSlash(relOrAbs(root, path))
		if d.IsDir() {
			if path != root && reviewScopeSearchSkipDir(rel, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldSkipMCPReviewFile(rel) || !reviewScopeSearchReadableFile(rel) {
			return nil
		}
		if visited >= reviewScopeWorkspaceSearchReadableFileLimit {
			return nil
		}
		visited++
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > 512*1024 {
			score := reviewScopeSearchPathScore(rel, symbols, searchTerms, queryTerms)
			if score <= 0 && len(symbols) > 0 && largeContentScans < reviewScopeLargeFileScanLimit && info.Size() <= 64*1024*1024 {
				largeContentScans++
				score = reviewScopeLargeFileSymbolScore(path, rel, symbols, searchTerms, queryTerms)
			}
			if score > 0 {
				candidates = append(candidates, reviewScopeWorkspaceCandidate{path: rel, score: score})
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || !isText(data) {
			return nil
		}
		score := reviewScopeSearchScore(rel, string(data), symbols, searchTerms, queryTerms)
		if score <= 0 {
			return nil
		}
		candidates = append(candidates, reviewScopeWorkspaceCandidate{path: rel, score: score})
		return nil
	})
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].path < candidates[j].path
	})
	if len(symbols) > 0 && len(candidates) > 8 && !reviewScopeHasSymbolDefinitionCandidate(candidates, root, symbols) {
		return nil
	}
	candidates = reviewScopePreferSymbolDefinitionCandidates(candidates, root, symbols)
	var out []string
	for _, candidate := range candidates {
		out = append(out, candidate.path)
		if len(out) >= reviewScopeWorkspaceSearchResultLimit {
			break
		}
	}
	return analysisUniqueStrings(out)
}

func reviewScopeHasSymbolDefinitionCandidate(candidates []reviewScopeWorkspaceCandidate, root string, symbols []string) bool {
	for _, candidate := range candidates {
		if reviewScopeCandidateSymbolDefinitionScore(root, candidate.path, symbols) > 0 {
			return true
		}
	}
	return false
}

func reviewScopeRipgrepCandidateFiles(root string, symbols []string, searchTerms []string, queryTerms []string) []reviewScopeWorkspaceCandidate {
	terms := reviewScopeRipgrepTerms(symbols, searchTerms, queryTerms)
	if strings.TrimSpace(root) == "" || len(terms) == 0 {
		return nil
	}
	var candidates []reviewScopeWorkspaceCandidate
	seen := map[string]bool{}
	for termIndex, term := range terms {
		ctx, cancel := context.WithTimeout(context.Background(), reviewScopeRipgrepTimeout)
		args := []string{
			"--files-with-matches",
			"--fixed-strings",
			"--glob", "!.git/**",
			"--glob", "!.kernforge/**",
			"--glob", "!.vs/**",
			"--glob", "!node_modules/**",
			"--glob", "!vendor/**",
			"--glob", "!x64/**",
			"--glob", "!x86/**",
			"--glob", "!Debug/**",
			"--glob", "!Release/**",
			"--glob", "!Binaries/**",
			"--glob", "!Intermediate/**",
			"--glob", "!Saved/**",
			term,
			".",
		}
		text, err := runCommand(ctx, root, "rg", args...)
		cancel()
		if err != nil && strings.TrimSpace(text) == "" {
			continue
		}
		for lineIndex, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
			rel := reviewScopeNormalizeRipgrepPath(root, line)
			key := strings.ToLower(rel)
			if rel == "" || seen[key] || shouldSkipMCPReviewFile(rel) || !reviewScopeSearchReadableFile(rel) {
				continue
			}
			seen[key] = true
			score := 80 - minInt(termIndex*4+lineIndex, 30)
			score += reviewScopeScoreReadableFile(root, rel, symbols, searchTerms, queryTerms)
			if score <= 0 {
				continue
			}
			candidates = append(candidates, reviewScopeWorkspaceCandidate{path: rel, score: score})
			if len(candidates) >= reviewScopeRipgrepResultLimit {
				return candidates
			}
		}
	}
	return candidates
}

func reviewScopeScoreReadableFile(root string, rel string, symbols []string, searchTerms []string, queryTerms []string) int {
	pathScore := reviewScopeSearchPathScore(rel, symbols, searchTerms, queryTerms)
	resolved := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() {
		return pathScore
	}
	if info.Size() > 512*1024 {
		if len(symbols) > 0 && info.Size() <= 64*1024*1024 && reviewScopeFileContainsAnyTerm(resolved, symbols) {
			return maxInt(pathScore, reviewScopeLargeFileSymbolScore(resolved, rel, symbols, searchTerms, queryTerms))
		}
		return pathScore
	}
	data, err := os.ReadFile(resolved)
	if err != nil || !isText(data) {
		return pathScore
	}
	contentScore := reviewScopeSearchScore(rel, string(data), symbols, searchTerms, queryTerms)
	if contentScore <= 0 {
		return pathScore
	}
	return maxInt(pathScore, contentScore)
}

func reviewScopePreferSymbolDefinitionCandidates(candidates []reviewScopeWorkspaceCandidate, root string, symbols []string) []reviewScopeWorkspaceCandidate {
	if len(candidates) == 0 || len(symbols) == 0 {
		return candidates
	}
	var focused []reviewScopeWorkspaceCandidate
	for _, candidate := range candidates {
		score := reviewScopeCandidateSymbolDefinitionScore(root, candidate.path, symbols)
		if score <= 0 {
			continue
		}
		candidate.score += score
		focused = append(focused, candidate)
	}
	if len(focused) == 0 {
		return candidates
	}
	sort.SliceStable(focused, func(i, j int) bool {
		if focused[i].score != focused[j].score {
			return focused[i].score > focused[j].score
		}
		return focused[i].path < focused[j].path
	})
	return focused
}

func reviewScopeCandidateSymbolDefinitionScore(root string, rel string, symbols []string) int {
	pathLower := strings.ToLower(filepath.ToSlash(rel))
	score := 0
	for _, symbol := range symbols {
		symbol = strings.TrimSpace(strings.TrimSuffix(symbol, "()"))
		if len(symbol) < 4 {
			continue
		}
		for _, alias := range reviewScopeSymbolAliases(symbol) {
			lower := strings.ToLower(alias)
			if strings.Contains(pathLower, lower) {
				score += 60
				break
			}
		}
	}
	resolved := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() || info.Size() > 2*1024*1024 {
		return score
	}
	data, err := os.ReadFile(resolved)
	if err != nil || !isText(data) {
		return score
	}
	content := string(data)
	for _, symbol := range symbols {
		symbol = strings.TrimSpace(strings.TrimSuffix(symbol, "()"))
		if len(symbol) < 4 {
			continue
		}
		for _, alias := range reviewScopeSymbolAliases(symbol) {
			if reviewScopeContainsSymbolDefinition(content, alias) {
				score += 80
				break
			}
		}
	}
	return score
}

func reviewScopeSymbolAliases(symbol string) []string {
	symbol = strings.TrimSpace(strings.TrimSuffix(symbol, "()"))
	if symbol == "" || strings.Contains(symbol, "::") {
		return []string{symbol}
	}
	seen := map[string]bool{}
	var out []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[strings.ToLower(value)] {
			return
		}
		seen[strings.ToLower(value)] = true
		out = append(out, value)
	}
	add(symbol)
	if len(symbol) >= 2 && symbol[0] >= 'A' && symbol[0] <= 'Z' {
		for _, prefix := range []string{"A", "U", "F", "I", "E", "S", "T"} {
			if !strings.HasPrefix(symbol, prefix) {
				add(prefix + symbol)
			}
		}
		if strings.Contains("AUFIEST", symbol[:1]) && len(symbol) > 2 && symbol[1] >= 'A' && symbol[1] <= 'Z' {
			add(symbol[1:])
		}
	}
	return out
}

func reviewScopeContainsSymbolDefinition(content string, symbol string) bool {
	escaped := regexp.QuoteMeta(symbol)
	patterns := []string{
		`(?m)\bclass\s+` + escaped + `\b`,
		`(?m)\bstruct\s+` + escaped + `\b`,
		`(?m)\benum\s+(?:class\s+)?` + escaped + `\b`,
		`(?m)\binterface\s+` + escaped + `\b`,
		`(?m)\bfunc\s+` + escaped + `\b`,
		`(?m)\bfunc\s*\([^)]*\*?\s*` + escaped + `\s*\)\s+[A-Za-z_][A-Za-z0-9_]*\s*\(`,
		`(?m)\bfunction\s+` + escaped + `\b`,
		`(?m)\b` + escaped + `::[A-Za-z_~][A-Za-z0-9_~]*\s*\(`,
	}
	for _, pattern := range patterns {
		if regexp.MustCompile(pattern).MatchString(content) {
			return true
		}
	}
	return false
}

func reviewScopeRipgrepTerms(symbols []string, searchTerms []string, queryTerms []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(term string) {
		term = strings.TrimSpace(strings.TrimSuffix(term, "()"))
		if len(term) < 4 {
			return
		}
		lower := strings.ToLower(term)
		if seen[lower] || reviewScopeTokenLooksTooGeneric(lower) {
			return
		}
		seen[lower] = true
		out = append(out, term)
	}
	for _, symbol := range symbols {
		add(symbol)
	}
	if len(out) == 0 {
		for _, term := range searchTerms {
			add(term)
		}
	}
	if len(out) == 0 {
		for _, term := range queryTerms {
			add(term)
		}
	}
	return limitStrings(out, 6)
}

func reviewScopeNormalizeRipgrepPath(root string, line string) string {
	path := strings.TrimSpace(line)
	if path == "" || strings.Contains(path, " failed: ") || strings.EqualFold(path, "(no output)") {
		return ""
	}
	path = strings.TrimPrefix(path, ".\\")
	path = strings.TrimPrefix(path, "./")
	if filepath.IsAbs(path) {
		path = relOrAbs(root, path)
	}
	return filepath.ToSlash(path)
}

func reviewScopeSearchQueryTerms(request string, symbols []string, searchTerms []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" {
			return
		}
		key := strings.ToLower(term)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, term)
	}
	for _, symbol := range symbols {
		add(symbol)
	}
	for _, term := range searchTerms {
		add(term)
	}
	for _, token := range reviewScopeRequestSearchTokens(request) {
		add(token)
	}
	return limitStrings(out, 24)
}

func reviewScopeRequestSearchTokens(request string) []string {
	matches := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{3,}`).FindAllString(request, -1)
	var out []string
	seen := map[string]bool{}
	for _, match := range matches {
		lower := strings.ToLower(match)
		if seen[lower] || reviewScopeTokenLooksTooGeneric(lower) {
			continue
		}
		seen[lower] = true
		out = append(out, match)
	}
	return limitStrings(out, 12)
}

func reviewScopeTokenLooksTooGeneric(token string) bool {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "review", "code", "file", "change", "changes", "process", "service", "server", "client":
		return true
	default:
		return false
	}
}

func reviewScopeSearchSkipDir(rel string, name string) bool {
	lowerRel := strings.ToLower(filepath.ToSlash(rel))
	lowerName := strings.ToLower(strings.TrimSpace(name))
	switch lowerName {
	case ".git", ".kernforge", ".vs", "node_modules", "vendor", "x64", "x86", "debug", "release", "bin", "obj", "build", "out", "packages",
		"binaries", "content", "deriveddatacache", "intermediate", "saved":
		return true
	}
	return strings.Contains(lowerRel, "/.git/") ||
		strings.Contains(lowerRel, "/.kernforge/") ||
		strings.Contains(lowerRel, "/.vs/") ||
		strings.Contains(lowerRel, "/binaries/") ||
		strings.Contains(lowerRel, "/content/") ||
		strings.Contains(lowerRel, "/deriveddatacache/") ||
		strings.Contains(lowerRel, "/intermediate/") ||
		strings.Contains(lowerRel, "/saved/")
}

func reviewScopeSearchReadableFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx",
		".go", ".cs", ".rs", ".java", ".kt", ".py", ".ps1", ".bat", ".cmd",
		".vcxproj", ".props", ".targets", ".sln", ".iss", ".wxs", ".xml", ".json", ".toml", ".yaml", ".yml", ".md", ".txt":
		return true
	default:
		return false
	}
}

func reviewScopeSearchPathScore(path string, symbols []string, searchTerms []string, queryTerms []string) int {
	pathLower := strings.ToLower(filepath.ToSlash(path))
	score := 0
	symbolMatches := 0
	serviceMatches := 0
	for _, symbol := range symbols {
		term := strings.ToLower(strings.TrimSpace(symbol))
		if term == "" {
			continue
		}
		if strings.Contains(pathLower, term) {
			score += 10
			symbolMatches++
		}
	}
	for _, term := range searchTerms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		if strings.Contains(pathLower, term) {
			score += 6
			serviceMatches++
		}
	}
	for _, term := range queryTerms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		if strings.Contains(pathLower, term) {
			score += 3
		}
	}
	if len(symbols) > 0 && symbolMatches == 0 {
		return 0
	}
	if len(searchTerms) > 0 && serviceMatches == 0 && symbolMatches == 0 {
		return 0
	}
	score += reviewScopeSourceFileScoreBoost(path)
	if strings.Contains(pathLower, "test") {
		score--
	}
	return score
}

func reviewScopeLargeFileSymbolScore(path string, rel string, symbols []string, searchTerms []string, queryTerms []string) int {
	if !reviewScopeFileContainsAnyTerm(path, symbols) {
		return 0
	}
	score := 18
	score += reviewScopeSearchPathScore(rel, nil, searchTerms, queryTerms)
	if strings.Contains(strings.ToLower(filepath.ToSlash(rel)), "test") {
		score--
	}
	return score
}

func reviewScopeFileContainsAnyTerm(path string, terms []string) bool {
	var needles [][]byte
	maxNeedleLen := 0
	for _, term := range terms {
		term = strings.TrimSpace(strings.TrimSuffix(term, "()"))
		if len(term) < 4 {
			continue
		}
		needle := []byte(strings.ToLower(term))
		needles = append(needles, needle)
		if len(needle) > maxNeedleLen {
			maxNeedleLen = len(needle)
		}
	}
	if len(needles) == 0 {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	buffer := make([]byte, 64*1024)
	var carry []byte
	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			chunk := append(append([]byte(nil), carry...), buffer[:n]...)
			lower := []byte(strings.ToLower(string(chunk)))
			for _, needle := range needles {
				if bytes.Contains(lower, needle) {
					return true
				}
			}
			carryLen := maxNeedleLen - 1
			if carryLen < 0 {
				carryLen = 0
			}
			if carryLen > len(chunk) {
				carryLen = len(chunk)
			}
			carry = append(carry[:0], chunk[len(chunk)-carryLen:]...)
		}
		if readErr != nil {
			return false
		}
	}
}

func reviewScopeSearchScore(path string, content string, symbols []string, searchTerms []string, queryTerms []string) int {
	haystack := strings.ToLower(filepath.ToSlash(path) + "\n" + content)
	pathLower := strings.ToLower(filepath.ToSlash(path))
	score := 0
	symbolMatches := 0
	serviceMatches := 0
	for _, symbol := range symbols {
		term := strings.ToLower(strings.TrimSpace(symbol))
		if term == "" {
			continue
		}
		if strings.Contains(pathLower, term) {
			score += 8
			symbolMatches++
		}
		if strings.Contains(haystack, term) {
			score += 6
			symbolMatches++
		}
	}
	for _, term := range searchTerms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		if strings.Contains(haystack, term) {
			score += 5
			serviceMatches++
		}
	}
	for _, term := range queryTerms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		if strings.Contains(pathLower, term) {
			score += 3
		} else if strings.Contains(haystack, term) {
			score += 1
		}
	}
	if len(symbols) > 0 && symbolMatches == 0 {
		return 0
	}
	if len(searchTerms) > 0 && serviceMatches == 0 && symbolMatches == 0 {
		return 0
	}
	score += reviewScopeSourceFileScoreBoost(path)
	if strings.Contains(pathLower, "test") {
		score--
	}
	return score
}

func reviewScopeCandidateFilesFromDiff(diff string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "diff --git ") {
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				if path := reviewScopeTrimDiffPath(fields[3]); path != "" {
					out = append(out, path)
				}
			}
			continue
		}
		if strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if path := reviewScopeTrimDiffPath(fields[1]); path != "" {
					out = append(out, path)
				}
			}
		}
	}
	return analysisUniqueStrings(filterReviewablePaths(out))
}

func reviewScopeTrimDiffPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	path = strings.TrimPrefix(path, "before/")
	path = strings.TrimPrefix(path, "after/")
	if path == "/dev/null" {
		return ""
	}
	return filepath.ToSlash(path)
}

func reviewScopeCandidateSymbols(request string) []string {
	matches := reviewScopeSymbolPattern.FindAllString(request, -1)
	seen := map[string]bool{}
	var out []string
	for _, match := range matches {
		symbol := strings.TrimSpace(strings.TrimSuffix(match, "()"))
		if !reviewScopeTokenLooksLikeSymbol(symbol) {
			continue
		}
		key := strings.ToLower(symbol)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, symbol)
		if len(out) >= 16 {
			break
		}
	}
	return out
}

func reviewScopeSearchTerms(request string, candidateFiles []string) []string {
	discovery := ReviewScopeDiscovery{CandidateFiles: candidateFiles}
	domains, _ := reviewScopeSignals(discovery, request)
	seen := map[string]bool{}
	var terms []string
	for _, domain := range domains {
		for _, part := range strings.Split(domain.Signal, "|") {
			part = strings.TrimSpace(part)
			if part == "" || seen[strings.ToLower(part)] {
				continue
			}
			seen[strings.ToLower(part)] = true
			terms = append(terms, part)
		}
	}
	return limitStrings(terms, 16)
}

func reviewScopeWidth(request string, candidateFiles []string, candidateSymbols []string) (string, float64) {
	lower := strings.ToLower(strings.TrimSpace(request))
	switch {
	case len(candidateFiles) == 0 && looksLikeBugSearchAndFixIntent(lower):
		return "broad", 0.35
	case len(candidateFiles) == 0:
		return "unknown", 0.45
	case len(candidateFiles) > 24:
		return "broad", 0.5
	case len(candidateFiles) > 8:
		return "bounded", 0.68
	case len(candidateFiles) <= 3 && len(candidateSymbols) <= 6:
		return "focused", 0.86
	default:
		return "bounded", 0.74
	}
}

func reviewScopeNarrowingCommands(candidateFiles []string, candidateSymbols []string, searchTerms []string) []string {
	var out []string
	if len(candidateSymbols) > 0 && len(candidateFiles) == 0 {
		out = append(out, fmt.Sprintf("rg -n %q .", strings.Join(limitStrings(candidateSymbols, 3), "|")))
	}
	if len(candidateFiles) > 0 {
		for _, path := range limitStrings(candidateFiles, 3) {
			out = append(out, fmt.Sprintf("/review --path %s", path))
		}
	}
	if len(searchTerms) > 0 {
		out = append(out, fmt.Sprintf("rg -n %q .", strings.Join(limitStrings(searchTerms, 4), "|")))
	}
	if len(out) == 0 {
		out = append(out, "/review --path <focused-file>")
	}
	return out
}

func reviewScopeDiscoveryNeedsNarrowing(discovery ReviewScopeDiscovery) bool {
	width := strings.ToLower(strings.TrimSpace(discovery.ScopeWidth))
	return width == "broad" || (width == "unknown" && len(discovery.CandidateFiles) == 0)
}

func reviewScopePreferredNarrowingCommand(discovery ReviewScopeDiscovery) string {
	for _, command := range discovery.NarrowingCommands {
		command = strings.TrimSpace(command)
		if command != "" {
			return command
		}
	}
	return "/review --path <focused-file>"
}

func reviewScopeDiscoveryFindingEvidence(discovery ReviewScopeDiscovery) string {
	parts := []string{}
	if strings.TrimSpace(discovery.ScopeWidth) != "" {
		parts = append(parts, "scope_width="+discovery.ScopeWidth)
	}
	if len(discovery.CandidateFiles) > 0 {
		parts = append(parts, "candidate_files="+strings.Join(limitStrings(discovery.CandidateFiles, 8), ", "))
	}
	if len(discovery.CandidateSymbols) > 0 {
		parts = append(parts, "candidate_symbols="+strings.Join(limitStrings(discovery.CandidateSymbols, 8), ", "))
	}
	if len(discovery.SearchTerms) > 0 {
		parts = append(parts, "search_terms="+strings.Join(limitStrings(discovery.SearchTerms, 8), ", "))
	}
	if len(parts) == 0 {
		return "scope discovery did not find a concrete file or symbol"
	}
	return strings.Join(parts, "; ")
}

func reviewPolicyPacksForScopeDiscovery(discovery ReviewScopeDiscovery, domains []ReviewDomainSignal) []string {
	var packs []string
	for _, signal := range domains {
		switch strings.ToLower(strings.TrimSpace(signal.Domain)) {
		case "windows_kernel_driver":
			packs = append(packs, "windows_kernel_driver")
		case "windows_service_control":
			packs = append(packs, "windows_service_control")
		case "anti_cheat_detection":
			packs = append(packs, "anti_cheat_telemetry")
		case "memory_scan":
			packs = append(packs, "memory_scan")
		case "unreal_integrity":
			packs = append(packs, "unreal_integrity")
		}
	}
	if strings.EqualFold(strings.TrimSpace(discovery.ScopeWidth), "broad") {
		packs = append(packs, "scope_discovery")
	}
	return analysisUniqueStrings(packs)
}

func inferReviewModeFromScopeDiscovery(discovery ReviewScopeDiscovery, domains []ReviewDomainSignal, target string) string {
	_ = target
	for _, signal := range domains {
		switch strings.ToLower(strings.TrimSpace(signal.Domain)) {
		case "windows_kernel_driver", "windows_service_control", "anti_cheat_detection", "memory_scan":
			return reviewModeSecurityHardening
		}
	}
	if reviewScopeDiscoveryNeedsNarrowing(discovery) {
		return reviewModeLiveFix
	}
	return ""
}

func renderReviewScopeDiscoveryForEvidence(analysis ReviewRequestAnalysis) string {
	discovery := analysis.ScopeDiscovery
	var b strings.Builder
	if strings.TrimSpace(discovery.ScopeWidth) != "" {
		fmt.Fprintf(&b, "scope_width=%s confidence=%.2f\n", discovery.ScopeWidth, discovery.Confidence)
	}
	if len(discovery.CandidateFiles) > 0 {
		fmt.Fprintf(&b, "candidate_files=%s\n", strings.Join(limitStrings(discovery.CandidateFiles, 16), ", "))
	}
	if len(discovery.CandidateSymbols) > 0 {
		fmt.Fprintf(&b, "candidate_symbols=%s\n", strings.Join(discovery.CandidateSymbols, ", "))
	}
	if len(discovery.SearchTerms) > 0 {
		fmt.Fprintf(&b, "search_terms=%s\n", strings.Join(discovery.SearchTerms, ", "))
	}
	if len(analysis.DomainSignals) > 0 {
		var domains []string
		for _, signal := range analysis.DomainSignals {
			domains = append(domains, fmt.Sprintf("%s(%s)", signal.Domain, signal.Evidence))
		}
		fmt.Fprintf(&b, "domain_signals=%s\n", strings.Join(domains, ", "))
	}
	if len(analysis.RiskSignals) > 0 {
		var risks []string
		for _, signal := range analysis.RiskSignals {
			risks = append(risks, fmt.Sprintf("%s:%s", signal.Risk, signal.Severity))
		}
		fmt.Fprintf(&b, "risk_signals=%s\n", strings.Join(risks, ", "))
	}
	if len(discovery.NarrowingCommands) > 0 {
		fmt.Fprintf(&b, "narrowing_commands=%s\n", strings.Join(discovery.NarrowingCommands, " | "))
	}
	return strings.TrimSpace(b.String())
}

func reviewScopeNormalizeCandidatePath(root string, path string) string {
	path = strings.TrimSpace(strings.Trim(path, " \t\r\n\"'`<>.,;()[]{}"))
	path = strings.TrimPrefix(path, "@")
	if path == "" || strings.HasPrefix(path, "-") || strings.HasPrefix(path, "+") || strings.ContainsAny(path, " \t\r\n") {
		return ""
	}
	if index := strings.Index(path, ":"); index > 1 && strings.Contains(path[index+1:], "-") {
		path = path[:index]
	}
	if strings.TrimSpace(root) != "" && filepath.IsAbs(path) {
		path = relOrAbs(root, path)
	}
	return filepath.ToSlash(path)
}

func reviewScopeCandidatePathLooksSynthetic(path string) bool {
	rawNormalized := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if strings.Contains(rawNormalized, "%") {
		return true
	}
	if reviewScopeCodeLiteralPathFragmentPattern.MatchString(rawNormalized) {
		return true
	}
	if strings.HasPrefix(rawNormalized, "/") && !strings.Contains(rawNormalized, ".") {
		return true
	}
	normalized := rawNormalized
	normalized = strings.Trim(normalized, "/")
	if normalized == "" || strings.Contains(normalized, "://") {
		return true
	}
	switch normalized {
	case "code/change", "code/review", "review/change", "web", "web/search", "web/research", "web/search/browser", "browser":
		return true
	}
	if strings.HasPrefix(normalized, "code/change/") ||
		strings.HasPrefix(normalized, "web/research/") ||
		strings.HasPrefix(normalized, "web/search/browser/") ||
		strings.HasPrefix(normalized, "mcp__") {
		return true
	}
	if reviewScopeWindowsRootFragmentPattern.MatchString(normalized) {
		return true
	}
	if reviewScopeDriveRootFragmentPattern.MatchString(normalized) {
		return true
	}
	if reviewScopeFindingLabelFragmentPattern.MatchString(normalized) {
		return true
	}
	if reviewScopeDiffMarkerFragmentPattern.MatchString(normalized) {
		return true
	}
	return false
}

func reviewScopeTokenLooksLikePath(token string) bool {
	token = strings.TrimSpace(strings.TrimPrefix(token, "@"))
	if token == "" || strings.HasPrefix(token, "-") || strings.HasPrefix(token, "+") || strings.Contains(token, "://") || strings.ContainsAny(token, " \t\r\n") {
		return false
	}
	if reviewScopeCandidatePathLooksSynthetic(token) {
		return false
	}
	ext := strings.ToLower(filepath.Ext(token))
	switch ext {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".hxx", ".cs", ".rs", ".py", ".js", ".ts", ".tsx", ".jsx", ".java", ".kt", ".swift", ".m", ".mm", ".sln", ".vcxproj", ".props", ".targets", ".inf", ".sys":
		return true
	}
	return strings.Contains(token, "/") || strings.Contains(token, "\\")
}

func reviewScopeGitStatusLooksUsable(root string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	status := runGitText(root, "status", "--short")
	lower := strings.ToLower(strings.TrimSpace(status))
	if lower == "" {
		return true
	}
	if reviewGitOutputIsUnavailable(status) ||
		strings.Contains(lower, "usage:") ||
		strings.Contains(lower, "not a git command") ||
		strings.Contains(lower, "unknown option") {
		return false
	}
	return true
}

func reviewScopeTokenLooksLikeSymbol(token string) bool {
	token = strings.TrimSpace(token)
	if len(token) < 3 || reviewScopeStopWords[strings.ToLower(token)] {
		return false
	}
	if strings.Contains(token, "::") || strings.Contains(token, "_") {
		return true
	}
	for _, r := range token {
		return r >= 'A' && r <= 'Z'
	}
	return false
}

func reviewScopeMatchedKeyword(text string, keywords []string) string {
	for _, keyword := range keywords {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword != "" && reviewScopeKeywordMatches(text, keyword) {
			return keyword
		}
	}
	return ""
}

func reviewScopeKeywordMatches(text string, keyword string) bool {
	if keyword == "" {
		return false
	}
	if !reviewScopeKeywordNeedsBoundary(keyword) {
		return strings.Contains(text, keyword)
	}
	start := 0
	for {
		index := strings.Index(text[start:], keyword)
		if index < 0 {
			return false
		}
		index += start
		beforeOK := index == 0 || !reviewScopeKeywordWordRune(rune(text[index-1]))
		afterIndex := index + len(keyword)
		afterOK := afterIndex >= len(text) || !reviewScopeKeywordWordRune(rune(text[afterIndex]))
		if beforeOK && afterOK {
			return true
		}
		start = index + len(keyword)
	}
}

func reviewScopeKeywordNeedsBoundary(keyword string) bool {
	for _, r := range keyword {
		if !reviewScopeKeywordWordRune(r) {
			return false
		}
	}
	return true
}

func reviewScopeKeywordWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

func reviewScopeSourceFileScoreBoost(path string) int {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".cs", ".rs", ".java", ".kt", ".py", ".go":
		return 8
	case ".json", ".toml", ".yaml", ".yml", ".md", ".txt", ".xml":
		return -8
	default:
		return 0
	}
}

var reviewScopeStopWords = map[string]bool{
	"review": true, "fix": true, "bug": true, "bugs": true, "find": true, "please": true, "code": true,
	"current": true, "change": true, "changes": true, "security": true, "kernel": true, "driver": true,
	"리뷰": true, "검토": true, "수정": true, "버그": true, "문제": true,
}
