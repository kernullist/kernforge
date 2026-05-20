package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const artifactQualityReadLimit = 512 * 1024

type ArtifactQualityReport struct {
	GeneratedAt time.Time              `json:"generated_at,omitempty"`
	Artifacts   []ArtifactQualityCheck `json:"artifacts,omitempty"`
	Notes       []string               `json:"notes,omitempty"`
	Findings    []CodingHarnessFinding `json:"findings,omitempty"`
}

type ArtifactQualityCheck struct {
	Path            string   `json:"path,omitempty"`
	Kind            string   `json:"kind,omitempty"`
	Size            int64    `json:"size,omitempty"`
	ContentChars    int      `json:"content_chars,omitempty"`
	Substantive     bool     `json:"substantive,omitempty"`
	PromptKeywords  []string `json:"prompt_keywords,omitempty"`
	MatchedKeywords []string `json:"matched_keywords,omitempty"`
	Checks          []string `json:"checks,omitempty"`
}

func (a *Agent) buildArtifactQualityReport(reply string) ArtifactQualityReport {
	report := ArtifactQualityReport{GeneratedAt: time.Now()}
	if a == nil || a.Session == nil {
		return report
	}
	prompt := codingHarnessSourcePrompt(a.Session)
	targets := collectArtifactQualityTargets(a.Session, reply)
	if len(targets) == 0 {
		report.Notes = append(report.Notes, "No requested or claimed artifacts were available for content quality checks.")
		report.Normalize()
		return report
	}
	for _, target := range targets {
		check, findings := a.checkArtifactQuality(target, prompt, reply)
		if strings.TrimSpace(check.Path) != "" {
			report.Artifacts = append(report.Artifacts, check)
		}
		report.Findings = append(report.Findings, findings...)
	}
	report.Normalize()
	return report
}

func collectArtifactQualityTargets(sess *Session, reply string) []string {
	targets := make([]string, 0)
	if sess != nil && sess.AcceptanceContract != nil {
		targets = append(targets, sess.AcceptanceContract.RequiredArtifacts...)
	}
	targets = append(targets, extractClaimedArtifactPaths(reply)...)
	for _, path := range sessionPatchTransactionChangedPaths(sess) {
		if pathLooksLikeDocumentArtifact(path) {
			targets = append(targets, path)
		}
	}
	return normalizeTaskStateList(targets, 32)
}

func (a *Agent) checkArtifactQuality(target string, prompt string, reply string) (ArtifactQualityCheck, []CodingHarnessFinding) {
	check := ArtifactQualityCheck{
		Path: normalizeSessionRelativePath(target),
		Kind: artifactQualityKind(target),
	}
	findings := make([]CodingHarnessFinding, 0)
	abs, rel, ok := resolveClaimedArtifactPath(a.Workspace.Root, target)
	if !ok {
		check.Checks = append(check.Checks, "path could not be resolved inside workspace")
		return check, findings
	}
	check.Path = rel
	info, err := os.Stat(abs)
	if err != nil {
		check.Checks = append(check.Checks, "artifact does not exist")
		return check, findings
	}
	check.Size = info.Size()
	if info.IsDir() {
		check.Kind = "directory"
		check.Checks = append(check.Checks, "directory artifact")
		return check, findings
	}
	text, readable, readErr := readArtifactQualityText(abs)
	if readErr != nil {
		check.Checks = append(check.Checks, "read failed: "+compactPromptSection(readErr.Error(), 160))
		return check, findings
	}
	if !readable {
		check.Checks = append(check.Checks, "binary or non-text artifact")
		return check, findings
	}
	check.ContentChars = utf8.RuneCountInString(text)
	check.PromptKeywords = artifactQualityPromptKeywords(prompt, target)
	check.MatchedKeywords = codingHarnessMatchedTokens(text, check.PromptKeywords)
	check.Substantive = artifactQualityRequiresSubstantiveContent(prompt, target, reply)
	check.Checks = append(check.Checks, "text readable")
	if artifactQualityLooksPlaceholder(text) {
		severity := "warning"
		if pathLooksLikeDocumentArtifact(target) || check.Substantive {
			severity = "blocker"
		}
		findings = append(findings, CodingHarnessFinding{
			Severity: severity,
			Title:    "Artifact contains placeholder content",
			Detail:   fmt.Sprintf("%s appears to contain placeholder/TODO content rather than the requested deliverable.", rel),
		})
	}
	findings = append(findings, artifactQualityStructuredDocumentFindings(rel, text)...)
	if check.Substantive && check.ContentChars < 80 {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Artifact content is too thin",
			Detail:   fmt.Sprintf("%s was requested as a substantive artifact, but it only contains %d character(s).", rel, check.ContentChars),
		})
	}
	if check.Substantive && len(check.PromptKeywords) >= 2 && len(check.MatchedKeywords) == 0 {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Artifact does not cover requested topic",
			Detail:   fmt.Sprintf("%s does not mention any key topic from the request: %s.", rel, strings.Join(limitStrings(check.PromptKeywords, 6), ", ")),
		})
	} else if check.Substantive && len(check.PromptKeywords) >= 4 && len(check.MatchedKeywords) < 2 {
		findings = append(findings, CodingHarnessFinding{
			Severity: "warning",
			Title:    "Artifact has weak topic coverage",
			Detail:   fmt.Sprintf("%s only matches %d requested topic keyword(s): %s.", rel, len(check.MatchedKeywords), strings.Join(check.MatchedKeywords, ", ")),
		})
	}
	return check, findings
}

func artifactQualityStructuredDocumentFindings(path string, text string) []CodingHarnessFinding {
	if !pathLooksLikeDocumentArtifact(path) {
		return nil
	}
	profile := analyzeBugReportDocumentCounts(text)
	if !profile.HasBugReportSignals() {
		return nil
	}
	var findings []CodingHarnessFinding
	for _, conflict := range profile.Conflicts {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Artifact has inconsistent bug counts",
			Detail:   fmt.Sprintf("%s has conflicting bug-count metadata: %s.", path, conflict),
		})
	}
	if profile.SeverityTotal > 0 && profile.UniqueBugIDs > 0 && profile.SeverityTotal != profile.UniqueBugIDs {
		findings = append(findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Artifact severity summary does not match bug IDs",
			Detail:   fmt.Sprintf("%s lists %d unique BUG IDs, but the severity rows add up to %d.", path, profile.UniqueBugIDs, profile.SeverityTotal),
		})
	}
	for _, total := range profile.TotalClaims {
		if profile.UniqueBugIDs > 0 && total != profile.UniqueBugIDs {
			findings = append(findings, CodingHarnessFinding{
				Severity: "blocker",
				Title:    "Artifact total does not match bug IDs",
				Detail:   fmt.Sprintf("%s claims a total of %d bugs, but %d unique BUG IDs are present.", path, total, profile.UniqueBugIDs),
			})
		}
		if profile.SeverityTotal > 0 && total != profile.SeverityTotal {
			findings = append(findings, CodingHarnessFinding{
				Severity: "blocker",
				Title:    "Artifact total does not match severity summary",
				Detail:   fmt.Sprintf("%s claims a total of %d bugs, but the severity rows add up to %d.", path, total, profile.SeverityTotal),
			})
		}
	}
	return findings
}

type bugReportDocumentCountProfile struct {
	UniqueBugIDs  int
	SeverityTotal int
	TotalClaims   []int
	Conflicts     []string
}

func (p bugReportDocumentCountProfile) HasBugReportSignals() bool {
	return p.UniqueBugIDs > 0 || p.SeverityTotal > 0 || len(p.TotalClaims) > 0 || len(p.Conflicts) > 0
}

func analyzeBugReportDocumentCounts(text string) bugReportDocumentCountProfile {
	profile := bugReportDocumentCountProfile{}
	profile.UniqueBugIDs = countUniqueBugIDs(text)
	severityCounts, severityConflicts := collectSeveritySummaryCounts(text)
	profile.Conflicts = append(profile.Conflicts, severityConflicts...)
	for _, count := range severityCounts {
		profile.SeverityTotal += count
	}
	totalClaims, totalConflicts := collectBugTotalClaims(text)
	profile.TotalClaims = totalClaims
	profile.Conflicts = append(profile.Conflicts, totalConflicts...)
	return profile
}

func countUniqueBugIDs(text string) int {
	re := regexp.MustCompile(`\bBUG-\d{3,}\b`)
	matches := re.FindAllString(strings.ToUpper(text), -1)
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		seen[match] = struct{}{}
	}
	return len(seen)
}

func collectSeveritySummaryCounts(text string) (map[string]int, []string) {
	counts := make(map[string]int)
	var conflicts []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "|") {
			cells := markdownTableCells(line)
			for i, cell := range cells {
				severity, ok := canonicalBugSeverityLabel(cell)
				if !ok {
					continue
				}
				count, found := firstIntegerInCells(cells[i+1:])
				if !found {
					continue
				}
				conflicts = append(conflicts, recordBugSeverityCount(counts, severity, count)...)
				if listed := bugReferenceCountInLine(line); listed > 0 && listed != count {
					conflicts = append(conflicts, fmt.Sprintf("%s severity row claims %d but lists %d BUG IDs", severity, count, listed))
				}
			}
			continue
		}
		severity, count, ok := severityCountFromTextLine(line)
		if ok {
			conflicts = append(conflicts, recordBugSeverityCount(counts, severity, count)...)
			if listed := bugReferenceCountInLine(line); listed > 0 && listed != count {
				conflicts = append(conflicts, fmt.Sprintf("%s severity line claims %d but lists %d BUG IDs", severity, count, listed))
			}
		}
	}
	return counts, normalizeTaskStateList(conflicts, 8)
}

func recordBugSeverityCount(counts map[string]int, severity string, count int) []string {
	if previous, exists := counts[severity]; exists && previous != count {
		return []string{fmt.Sprintf("%s severity count is both %d and %d", severity, previous, count)}
	}
	counts[severity] = count
	return nil
}

func severityCountFromTextLine(line string) (string, int, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", 0, false
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(critical|high|medium|low)\b(?:\s*[:：|\-–—]\s*|\s+)(\d{1,5})\b`),
		regexp.MustCompile(`(치명|높음|중간|낮음)(?:\s*[:：|\-–—]\s*|\s+)(\d{1,5})\b`),
	}
	for _, pattern := range patterns {
		match := pattern.FindStringSubmatch(trimmed)
		if len(match) < 3 {
			continue
		}
		severity, ok := canonicalBugSeverityLabel(match[1])
		if !ok {
			continue
		}
		count, err := strconv.Atoi(match[2])
		if err != nil || count <= 0 {
			continue
		}
		return severity, count, true
	}
	return "", 0, false
}

func bugReferenceCountInLine(line string) int {
	upper := strings.ToUpper(line)
	fullIDPattern := regexp.MustCompile(`\bBUG-(\d{3,})\b`)
	fullMatches := fullIDPattern.FindAllStringSubmatch(upper, -1)
	if len(fullMatches) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(fullMatches))
	for _, match := range fullMatches {
		if len(match) < 2 {
			continue
		}
		seen["BUG-"+match[1]] = struct{}{}
	}
	abbreviatedIDPattern := regexp.MustCompile(`(?:^|[,;/(\[])\s*(\d{3,})\b`)
	for _, match := range abbreviatedIDPattern.FindAllStringSubmatch(upper, -1) {
		if len(match) < 2 {
			continue
		}
		seen["BUG-"+match[1]] = struct{}{}
	}
	return len(seen)
}

func collectBugTotalClaims(text string) ([]int, []string) {
	claims := make([]int, 0)
	addClaim := func(value int) {
		if value <= 0 {
			return
		}
		claims = append(claims, value)
	}
	for _, line := range strings.Split(text, "\n") {
		cells := markdownTableCells(line)
		if len(cells) > 0 {
			for i, cell := range cells {
				if !cellLooksLikeTotalBugCountLabel(cell) {
					continue
				}
				if count, found := firstIntegerInCells(cells[i+1:]); found {
					addClaim(count)
				}
			}
			continue
		}
		for _, count := range totalBugCountClaimsInText(line) {
			addClaim(count)
		}
	}
	claims = uniquePositiveInts(claims)
	conflicts := make([]string, 0)
	if len(claims) > 1 {
		parts := make([]string, 0, len(claims))
		for _, claim := range claims {
			parts = append(parts, strconv.Itoa(claim))
		}
		conflicts = append(conflicts, "document claims multiple totals: "+strings.Join(parts, ", "))
	}
	return claims, conflicts
}

func markdownTableCells(line string) []string {
	if !strings.Contains(line, "|") {
		return nil
	}
	parts := strings.Split(line, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cell := strings.TrimSpace(strings.Trim(part, "*` "))
		if cell == "" {
			continue
		}
		if strings.Trim(cell, "-: ") == "" {
			continue
		}
		cells = append(cells, cell)
	}
	return cells
}

func canonicalBugSeverityLabel(cell string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(cell))
	switch {
	case strings.Contains(lower, "critical") || strings.Contains(lower, "치명"):
		return "critical", true
	case strings.Contains(lower, "high") || strings.Contains(lower, "높음"):
		return "high", true
	case strings.Contains(lower, "medium") || strings.Contains(lower, "중간"):
		return "medium", true
	case strings.Contains(lower, "low") || strings.Contains(lower, "낮음"):
		return "low", true
	default:
		return "", false
	}
}

func firstIntegerInCells(cells []string) (int, bool) {
	for _, cell := range cells {
		if strings.Contains(strings.ToUpper(cell), "BUG-") {
			continue
		}
		if value, ok := firstInteger(cell); ok {
			return value, true
		}
	}
	return 0, false
}

func firstInteger(text string) (int, bool) {
	re := regexp.MustCompile(`\b\d{1,5}\b`)
	raw := re.FindString(text)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return value, true
}

func cellLooksLikeTotalBugCountLabel(cell string) bool {
	lower := strings.ToLower(strings.TrimSpace(cell))
	return lower == "total" ||
		lower == "합계" ||
		lower == "총계" ||
		strings.Contains(lower, "total bugs") ||
		strings.Contains(lower, "총 버그") ||
		strings.Contains(lower, "전체 버그")
}

func totalBugCountClaimsInText(line string) []int {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:total|overall)\s+(?:of\s+)?(\d{1,5})\s+(?:documented\s+)?bugs?\b`),
		regexp.MustCompile(`(?i)\b(\d{1,5})\s+(?:documented\s+)?bugs?\s+(?:were\s+)?(?:found|identified|documented)\s+(?:in\s+)?(?:total|overall)\b`),
		regexp.MustCompile(`(?i)\b(\d{1,5})\s+documented\s+bugs?\b`),
		regexp.MustCompile(`(?i)\b(\d{1,5})\s+bugs?\s+(?:were\s+)?(?:found|identified)\s+and\s+documented\b`),
		regexp.MustCompile(`(?i)\b(?:found|identified|documented)\s+(\d{1,5})\s+bugs?\b`),
		regexp.MustCompile(`총\s*(\d{1,5})\s*개\s*(?:의\s*)?(?:버그|문제)`),
	}
	var claims []int
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(line, -1) {
			if len(match) < 2 {
				continue
			}
			value, err := strconv.Atoi(match[1])
			if err == nil && value > 0 {
				claims = append(claims, value)
			}
		}
	}
	return uniquePositiveInts(claims)
}

func uniquePositiveInts(values []int) []int {
	seen := make(map[int]struct{}, len(values))
	out := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func artifactQualityKind(path string) string {
	lower := strings.ToLower(strings.TrimSpace(path))
	switch {
	case pathLooksLikeDocumentArtifact(lower):
		return "document"
	case isCodeLikePath(lower):
		return "code"
	default:
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(lower)), ".")
		if ext != "" {
			return ext
		}
		return "artifact"
	}
}

func readArtifactQualityText(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	if len(data) > artifactQualityReadLimit {
		data = data[:artifactQualityReadLimit]
	}
	if len(data) == 0 {
		return "", true, nil
	}
	for _, b := range data {
		if b == 0 {
			return "", false, nil
		}
	}
	if !utf8.Valid(data) {
		return "", false, nil
	}
	return string(data), true, nil
}

func artifactQualityPromptKeywords(prompt string, target string) []string {
	tokens := codingHarnessMeaningfulTokens(prompt)
	pathTokens := map[string]struct{}{}
	for _, token := range codingHarnessMeaningfulTokens(strings.ReplaceAll(target, string(filepath.Separator), " ")) {
		pathTokens[token] = struct{}{}
	}
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, ok := pathTokens[token]; ok {
			continue
		}
		out = append(out, token)
	}
	return normalizeTaskStateList(out, 16)
}

func artifactQualityRequiresSubstantiveContent(prompt string, target string, reply string) bool {
	lowerPrompt := strings.ToLower(strings.TrimSpace(prompt))
	lowerReply := strings.ToLower(strings.TrimSpace(reply))
	if !pathLooksLikeDocumentArtifact(target) {
		return false
	}
	if containsAny(lowerPrompt, "report", "analysis", "design", "guide", "plan", "summary", "spec", "proposal",
		"보고서", "리포트", "분석", "설계", "가이드", "계획", "요약", "명세", "제안") {
		return true
	}
	if containsAny(lowerReply, "report", "analysis", "guide", "document", "보고서", "리포트", "분석", "가이드", "문서") &&
		containsAny(lowerReply, strings.ToLower(filepath.Base(target)), strings.ToLower(normalizeSessionRelativePath(target))) {
		return true
	}
	return len(artifactQualityPromptKeywords(prompt, target)) >= 2
}

func artifactQualityLooksPlaceholder(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"todo", "tbd", "placeholder", "lorem ipsum", "coming soon", "fill this in", "stub only", "not implemented",
		"작성 예정", "내용 추가 예정", "추후 작성", "자리표시자", "임시 내용", "미구현",
	)
}

func (r *ArtifactQualityReport) Normalize() {
	if r == nil {
		return
	}
	for i := range r.Artifacts {
		r.Artifacts[i].Normalize()
	}
	r.Notes = normalizeTaskStateList(r.Notes, 8)
	r.Findings = normalizeCodingHarnessFindings(r.Findings)
}

func (c *ArtifactQualityCheck) Normalize() {
	if c == nil {
		return
	}
	c.Path = normalizeSessionRelativePath(c.Path)
	c.Kind = strings.TrimSpace(strings.ToLower(c.Kind))
	c.PromptKeywords = normalizeTaskStateList(c.PromptKeywords, 16)
	c.MatchedKeywords = normalizeTaskStateList(c.MatchedKeywords, 16)
	c.Checks = normalizeTaskStateList(c.Checks, 8)
}

func (r ArtifactQualityReport) RenderPromptSection() string {
	r.Normalize()
	lines := make([]string, 0, 8)
	if len(r.Artifacts) > 0 {
		parts := make([]string, 0, len(r.Artifacts))
		for _, artifact := range r.Artifacts {
			item := fmt.Sprintf("%s [%s] chars=%d", artifact.Path, artifact.Kind, artifact.ContentChars)
			if artifact.Substantive {
				item += " substantive=true"
			}
			if len(artifact.MatchedKeywords) > 0 {
				item += " matched=" + strings.Join(limitStrings(artifact.MatchedKeywords, 4), ",")
			}
			parts = append(parts, item)
		}
		lines = append(lines, "- Artifacts: "+strings.Join(parts, " | "))
	}
	for _, finding := range r.Findings {
		lines = append(lines, fmt.Sprintf("- [%s] %s: %s", finding.Severity, finding.Title, compactPromptSection(finding.Detail, 220)))
	}
	if len(r.Notes) > 0 {
		lines = append(lines, "- Notes: "+strings.Join(r.Notes, " | "))
	}
	return strings.Join(lines, "\n")
}
