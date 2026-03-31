package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	scoutFilePattern       = regexp.MustCompile(`(?i)\b[\w./\\-]+\.(go|cpp|cc|cxx|c|h|hpp|hh|cs|java|js|jsx|ts|tsx|py|rb|rs|swift|kt|kts|m|mm|php|lua|sh|ps1|json|ya?ml|toml|ini|conf|xml|html|css|scss|md)\b`)
	scoutIdentifierPattern = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]{2,}\b`)
	scoutIgnoredDirs       = map[string]bool{
		".git":         true,
		".build":       true,
		"node_modules": true,
		".idea":        true,
		".vs":          true,
		"dist":         true,
		"target":       true,
		"vendor":       true,
		"bin":          true,
		"obj":          true,
	}
	scoutStopWords = map[string]bool{
		"code": true, "file": true, "files": true, "function": true, "class": true, "method": true, "line": true,
		"lines": true, "please": true, "show": true, "tell": true, "what": true, "where": true, "when": true,
		"this": true, "that": true, "with": true, "from": true, "into": true, "about": true, "there": true,
		"explain": true, "describe": true, "debug": true, "issue": true, "problem": true, "help": true,
		"find": true, "search": true, "look": true, "make": true, "need": true, "want": true,
	}
)

type scoutTerms struct {
	fileTerms         []string
	symbolTerms       []string
	preferDefinitions bool
	preferReferences  bool
}

type scoutCandidate struct {
	path           string
	score          int
	reasons        []string
	content        string
	lineHits       []int
	definitionHits []int
	referenceHits  []int
	lineCount      int
}

type scoutWindow struct {
	start int
	end   int
}

func (a *Agent) autoScoutContext(input string) string {
	if strings.Contains(input, "@") {
		return ""
	}
	terms := extractScoutTerms(input)
	if len(terms.fileTerms) == 0 && len(terms.symbolTerms) == 0 {
		return ""
	}
	candidates := findScoutCandidates(a.Session.WorkingDir, terms)
	if len(candidates) == 0 {
		return ""
	}
	return renderScoutContext(a.Session.WorkingDir, candidates)
}

func extractScoutTerms(input string) scoutTerms {
	lower := strings.ToLower(input)
	files := uniqueStrings(scoutFilePattern.FindAllString(lower, -1))

	var symbols []string
	for _, token := range scoutIdentifierPattern.FindAllString(input, -1) {
		lowerToken := strings.ToLower(strings.TrimSpace(token))
		if scoutStopWords[lowerToken] {
			continue
		}
		if len(lowerToken) >= 6 || token != lowerToken || strings.ContainsAny(token, "_0123456789") {
			symbols = append(symbols, token)
		}
	}
	return scoutTerms{
		fileTerms:         uniqueStrings(files),
		symbolTerms:       uniqueStrings(symbols),
		preferDefinitions: strings.Contains(lower, "define") || strings.Contains(lower, "definition") || strings.Contains(lower, "declared") || strings.Contains(lower, "implemented"),
		preferReferences:  strings.Contains(lower, "reference") || strings.Contains(lower, "call") || strings.Contains(lower, "caller") || strings.Contains(lower, "used") || strings.Contains(lower, "usage"),
	}
}

func findScoutCandidates(root string, terms scoutTerms) []scoutCandidate {
	var candidates []scoutCandidate
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if scoutIgnoredDirs[strings.ToLower(d.Name())] {
				return filepath.SkipDir
			}
			return nil
		}
		if !isLikelyScoutFile(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 512*1024 {
			return nil
		}
		candidate, ok := scoreScoutCandidate(root, path, terms)
		if ok {
			candidates = append(candidates, candidate)
		}
		return nil
	})

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].path < candidates[j].path
	})

	if len(candidates) > 4 {
		candidates = candidates[:4]
	}
	return candidates
}

func scoreScoutCandidate(root, path string, terms scoutTerms) (scoutCandidate, bool) {
	rel := relOrAbs(root, path)
	lowerRel := strings.ToLower(rel)
	score := 0
	reasons := []string{}

	for _, fileTerm := range terms.fileTerms {
		if strings.Contains(lowerRel, strings.ToLower(fileTerm)) {
			score += 160
			reasons = append(reasons, "path matches "+fileTerm)
		}
	}

	needsContent := score > 0 || len(terms.symbolTerms) > 0
	var content string
	var lineHits []int
	var definitionHits []int
	var referenceHits []int
	if needsContent {
		data, err := os.ReadFile(path)
		if err != nil || !isText(data) {
			return scoutCandidate{}, false
		}
		content = string(data)
		lines := strings.Split(content, "\n")
		for _, term := range terms.symbolTerms {
			defHitsForTerm := 0
			refHitsForTerm := 0
			mentionHitsForTerm := 0
			for idx, line := range lines {
				kind, matched := classifyScoutLine(path, line, term)
				if !matched {
					continue
				}
				switch kind {
				case "definition":
					boost := 85
					if terms.preferDefinitions {
						boost += 35
					}
					score += boost
					definitionHits = append(definitionHits, idx+1)
					lineHits = append(lineHits, idx+1)
					defHitsForTerm++
				case "reference":
					boost := 40
					if terms.preferReferences {
						boost += 30
					}
					score += boost
					referenceHits = append(referenceHits, idx+1)
					lineHits = append(lineHits, idx+1)
					refHitsForTerm++
				default:
					score += 18
					lineHits = append(lineHits, idx+1)
					mentionHitsForTerm++
				}
				if defHitsForTerm >= 2 && refHitsForTerm >= 2 && mentionHitsForTerm >= 1 {
					break
				}
			}
			if defHitsForTerm > 0 {
				reasons = append(reasons, "defines "+term)
			}
			if refHitsForTerm > 0 {
				reasons = append(reasons, "references "+term)
			}
			if mentionHitsForTerm > 0 && defHitsForTerm == 0 && refHitsForTerm == 0 {
				reasons = append(reasons, "mentions "+term)
			}
		}
	}

	if score == 0 {
		return scoutCandidate{}, false
	}
	return scoutCandidate{
		path:           path,
		score:          score,
		reasons:        uniqueStrings(reasons),
		content:        content,
		lineHits:       uniqueInts(lineHits),
		definitionHits: uniqueInts(definitionHits),
		referenceHits:  uniqueInts(referenceHits),
		lineCount:      len(strings.Split(content, "\n")),
	}, true
}

func renderScoutContext(root string, candidates []scoutCandidate) string {
	var sections []string
	totalChars := 0
	for _, candidate := range candidates {
		snippet := buildScoutSnippet(candidate)
		if strings.TrimSpace(snippet) == "" {
			continue
		}
		section := fmt.Sprintf("### %s\nReason: %s\n```\n%s\n```",
			relOrAbs(root, candidate.path),
			strings.Join(candidate.reasons, ", "),
			snippet,
		)
		if totalChars+len(section) > 9000 && len(sections) > 0 {
			break
		}
		totalChars += len(section)
		sections = append(sections, section)
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\nAuto-discovered code context:\n" + strings.Join(sections, "\n\n")
}

func buildScoutSnippet(candidate scoutCandidate) string {
	if candidate.content == "" {
		return ""
	}
	lines := strings.Split(candidate.content, "\n")
	if len(lines) == 0 {
		return ""
	}
	if len(candidate.lineHits) == 0 {
		limit := len(lines)
		if limit > 60 {
			limit = 60
		}
		return formatLineBlock(lines, 1, limit)
	}

	var sections []string
	if len(candidate.definitionHits) > 0 {
		if snippet := renderScoutWindows(lines, candidate.definitionHits, "Definition hits"); snippet != "" {
			sections = append(sections, snippet)
		}
	}
	if len(candidate.referenceHits) > 0 {
		if snippet := renderScoutWindows(lines, candidate.referenceHits, "Reference hits"); snippet != "" {
			sections = append(sections, snippet)
		}
	}
	if len(sections) == 0 {
		if snippet := renderScoutWindows(lines, candidate.lineHits, "Relevant lines"); snippet != "" {
			sections = append(sections, snippet)
		}
	}
	return strings.Join(sections, "\n...\n")
}

func mergeScoutWindows(windows []scoutWindow) []scoutWindow {
	if len(windows) == 0 {
		return windows
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].start < windows[j].start })
	merged := []scoutWindow{windows[0]}
	for _, current := range windows[1:] {
		last := &merged[len(merged)-1]
		if current.start <= last.end+1 {
			if current.end > last.end {
				last.end = current.end
			}
			continue
		}
		merged = append(merged, current)
	}
	return merged
}

func formatLineBlock(lines []string, start, end int) string {
	var out []string
	for i := start - 1; i < end; i++ {
		out = append(out, fmt.Sprintf("%4d | %s", i+1, strings.TrimSuffix(lines[i], "\r")))
	}
	return strings.Join(out, "\n")
}

func renderScoutWindows(lines []string, hits []int, title string) string {
	if len(hits) == 0 {
		return ""
	}
	var windows []scoutWindow
	for _, hit := range hits {
		start := hit - 3
		end := hit + 3
		if start < 1 {
			start = 1
		}
		if end > len(lines) {
			end = len(lines)
		}
		windows = append(windows, scoutWindow{start: start, end: end})
		if len(windows) >= 3 {
			break
		}
	}
	windows = mergeScoutWindows(windows)
	var parts []string
	for _, w := range windows {
		parts = append(parts, formatLineBlock(lines, w.start, w.end))
	}
	if len(parts) == 0 {
		return ""
	}
	return title + ":\n" + strings.Join(parts, "\n...\n")
}

func isLikelyScoutFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".cpp", ".cc", ".cxx", ".c", ".h", ".hpp", ".hh", ".cs", ".java",
		".js", ".jsx", ".ts", ".tsx", ".py", ".rb", ".rs", ".swift", ".kt", ".kts",
		".m", ".mm", ".php", ".lua", ".sh", ".ps1", ".json", ".yaml", ".yml", ".toml",
		".ini", ".conf", ".xml", ".html", ".css", ".scss":
		return true
	default:
		return false
	}
}

func classifyScoutLine(path, line, term string) (string, bool) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if trimmed == "" {
		return "", false
	}
	ext := strings.ToLower(filepath.Ext(path))
	quotedTerm := regexp.QuoteMeta(term)
	containsTerm := regexp.MustCompile(`(?i)\b` + quotedTerm + `\b`).MatchString
	if !containsTerm(trimmed) {
		return "", false
	}

	defPatterns := definitionPatterns(ext, quotedTerm)
	for _, pattern := range defPatterns {
		if regexp.MustCompile(pattern).MatchString(trimmed) {
			return "definition", true
		}
	}

	refPatterns := referencePatterns(ext, quotedTerm)
	for _, pattern := range refPatterns {
		if regexp.MustCompile(pattern).MatchString(trimmed) {
			return "reference", true
		}
	}

	return "mention", true
}

func definitionPatterns(ext, quotedTerm string) []string {
	switch ext {
	case ".go":
		return []string{
			`^\s*func\s+\([^)]+\)\s*` + quotedTerm + `\s*\(`,
			`^\s*func\s+` + quotedTerm + `\s*\(`,
			`^\s*type\s+` + quotedTerm + `\s+(struct|interface)\b`,
			`^\s*(const|var)\s+` + quotedTerm + `\b`,
		}
	case ".cpp", ".cc", ".cxx", ".c", ".h", ".hpp", ".hh", ".m", ".mm":
		return []string{
			`^\s*(class|struct|enum|namespace)\s+` + quotedTerm + `\b`,
			`^\s*[\w:\<\>\*&\s]+` + quotedTerm + `\s*\([^;]*\)\s*(const)?\s*(\{|$)`,
		}
	case ".cs", ".java", ".kt", ".kts", ".swift":
		return []string{
			`^\s*(class|interface|struct|enum|record)\s+` + quotedTerm + `\b`,
			`^\s*(public|private|protected|internal|static|final|abstract|open|override|\s)+[\w\<\>\[\]\?,\s]+\s+` + quotedTerm + `\s*\(`,
			`^\s*(fun|func)\s+` + quotedTerm + `\s*\(`,
		}
	case ".js", ".jsx", ".ts", ".tsx":
		return []string{
			`^\s*(export\s+)?(async\s+)?function\s+` + quotedTerm + `\s*\(`,
			`^\s*(export\s+)?class\s+` + quotedTerm + `\b`,
			`^\s*(export\s+)?(const|let|var)\s+` + quotedTerm + `\s*=\s*(async\s*)?(\(|function\b)`,
			`^\s*(export\s+)?(interface|type)\s+` + quotedTerm + `\b`,
		}
	case ".py":
		return []string{
			`^\s*def\s+` + quotedTerm + `\s*\(`,
			`^\s*class\s+` + quotedTerm + `\b`,
		}
	case ".rs":
		return []string{
			`^\s*(pub\s+)?(async\s+)?fn\s+` + quotedTerm + `\s*\(`,
			`^\s*(pub\s+)?(struct|enum|trait|impl)\s+` + quotedTerm + `\b`,
		}
	default:
		return []string{
			`^\s*(class|struct|interface|enum|type|func|function|def)\s+` + quotedTerm + `\b`,
		}
	}
}

func referencePatterns(ext, quotedTerm string) []string {
	base := []string{
		`\b` + quotedTerm + `\s*\(`,
		`\bnew\s+` + quotedTerm + `\b`,
		`\b` + quotedTerm + `\s*::`,
		`\b` + quotedTerm + `\.`,
	}
	switch ext {
	case ".py":
		return append(base, `\bself\.`+quotedTerm+`\b`)
	default:
		return base
	}
}

func uniqueStrings(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func uniqueInts(items []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, item := range items {
		if item <= 0 || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	sort.Ints(out)
	return out
}
