package main

import (
	"fmt"
	"os"
	"path/filepath"
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
