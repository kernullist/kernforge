package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Skill struct {
	Name         string
	Path         string
	Summary      string
	Content      string
	AllowedTools []string
	Enabled      bool
}

type SkillCatalog struct {
	items   []Skill
	byName  map[string]Skill
	enabled []Skill
}

var explicitSkillPattern = regexp.MustCompile(`\$([A-Za-z0-9][A-Za-z0-9._-]*)`)

func LoadSkills(cwd string, extraPaths, enabledNames []string) (SkillCatalog, []string) {
	searchPaths := append(defaultSkillSearchPaths(cwd), extraPaths...)
	files, warnings := collectSkillFiles(searchPaths)

	order := []string{}
	itemsByName := map[string]Skill{}
	for _, file := range files {
		skill, err := loadSkillFile(file)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skill %s: %v", file, err))
			continue
		}
		key := normalizeSkillName(skill.Name)
		if key == "" {
			warnings = append(warnings, fmt.Sprintf("skill %s: missing name", file))
			continue
		}
		if _, exists := itemsByName[key]; !exists {
			order = append(order, key)
		}
		itemsByName[key] = skill
	}

	enabledSet := map[string]bool{}
	for _, name := range enabledNames {
		key := normalizeSkillName(name)
		if key == "" {
			continue
		}
		if _, ok := itemsByName[key]; !ok {
			warnings = append(warnings, fmt.Sprintf("enabled skill not found: %s", name))
			continue
		}
		enabledSet[key] = true
	}

	catalog := SkillCatalog{
		items:  make([]Skill, 0, len(order)),
		byName: make(map[string]Skill, len(itemsByName)),
	}
	for _, key := range order {
		skill := itemsByName[key]
		skill.Enabled = enabledSet[key]
		catalog.items = append(catalog.items, skill)
		catalog.byName[key] = skill
		if skill.Enabled {
			catalog.enabled = append(catalog.enabled, skill)
		}
	}
	return catalog, warnings
}

func defaultSkillSearchPaths(cwd string) []string {
	paths := []string{
		filepath.Join(userConfigDir(), "skills"),
	}
	for _, dir := range ancestorDirs(cwd) {
		paths = append(paths,
			filepath.Join(dir, userConfigDirName, "skills"),
			filepath.Join(dir, "skills"),
		)
	}
	return paths
}

func collectSkillFiles(paths []string) ([]string, []string) {
	seen := map[string]bool{}
	files := []string{}
	warnings := []string{}
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		path = filepath.Clean(expandHome(path))
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("skill path %s: %v", path, err))
			continue
		}
		if !info.IsDir() {
			if seen[path] {
				continue
			}
			seen[path] = true
			files = append(files, path)
			continue
		}
		direct := filepath.Join(path, "SKILL.md")
		if _, err := os.Stat(direct); err == nil && !seen[direct] {
			seen[direct] = true
			files = append(files, direct)
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skill path %s: %v", path, err))
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			candidate := filepath.Join(path, entry.Name(), "SKILL.md")
			if _, err := os.Stat(candidate); err == nil && !seen[candidate] {
				seen[candidate] = true
				files = append(files, candidate)
			}
		}
	}
	return files, warnings
}

func loadSkillFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	front, body := parseSkillFrontmatter(string(data))
	content := strings.TrimSpace(body)

	name := strings.TrimSpace(front["name"])
	if name == "" {
		name = extractSkillName(path, content)
	}

	summary := strings.TrimSpace(front["description"])
	if summary == "" {
		summary = summarizeSkillContent(content)
	} else {
		summary = clampSkillSummary(summary)
	}

	skill := Skill{
		Name:         name,
		Path:         path,
		Summary:      summary,
		Content:      content,
		AllowedTools: parseSkillToolList(front["allowed-tools"]),
	}
	return skill, nil
}

// parseSkillFrontmatter splits an optional leading YAML frontmatter block
// delimited by a "---" line at the very top of the file from the markdown
// body. It supports the flat "key: value" subset used by SKILL.md
// (name, description, allowed-tools). When no frontmatter is present the full
// trimmed input is returned as the body and the map is empty.
func parseSkillFrontmatter(raw string) (map[string]string, string) {
	front := map[string]string{}
	// Normalize line endings so CRLF files parse the same as LF files.
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	trimmedLeft := strings.TrimLeft(normalized, "\n")
	if !strings.HasPrefix(trimmedLeft, "---\n") && trimmedLeft != "---" {
		return front, normalized
	}
	// Drop the opening fence line.
	rest := strings.TrimPrefix(trimmedLeft, "---")
	rest = strings.TrimPrefix(rest, "\n")
	lines := strings.Split(rest, "\n")
	closeIndex := -1
	for index, line := range lines {
		if strings.TrimSpace(line) == "---" {
			closeIndex = index
			break
		}
	}
	if closeIndex < 0 {
		// No closing fence: treat the whole thing as body, not frontmatter.
		return front, normalized
	}
	lastKey := ""
	for _, line := range lines[:closeIndex] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Support simple block-list continuation lines ("- value") that
		// extend the most recent key.
		if strings.HasPrefix(trimmed, "- ") && lastKey != "" {
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			item = trimSkillScalar(item)
			if item == "" {
				continue
			}
			if front[lastKey] == "" {
				front[lastKey] = item
			} else {
				front[lastKey] = front[lastKey] + ", " + item
			}
			continue
		}
		colon := strings.Index(trimmed, ":")
		if colon <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(trimmed[:colon]))
		value := trimSkillScalar(strings.TrimSpace(trimmed[colon+1:]))
		if key == "" {
			continue
		}
		front[key] = value
		lastKey = key
	}
	body := ""
	if closeIndex+1 < len(lines) {
		body = strings.Join(lines[closeIndex+1:], "\n")
	}
	return front, body
}

// trimSkillScalar removes optional surrounding quotes and trailing inline
// comments from a YAML scalar value.
func trimSkillScalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// parseSkillToolList splits an allowed-tools value into individual tool names.
// It accepts comma-separated scalars as well as flow-list syntax ("[a, b]").
func parseSkillToolList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	parts := strings.Split(value, ",")
	tools := make([]string, 0, len(parts))
	for _, part := range parts {
		tool := trimSkillScalar(strings.TrimSpace(part))
		if tool != "" {
			tools = append(tools, tool)
		}
	}
	if len(tools) == 0 {
		return nil
	}
	return tools
}

func clampSkillSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if len(summary) > 280 {
		summary = strings.TrimSpace(summary[:280]) + "..."
	}
	return summary
}

func extractSkillName(path, content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			return strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		}
	}
	base := filepath.Base(filepath.Dir(path))
	if strings.TrimSpace(base) != "" && !strings.EqualFold(base, ".") {
		return base
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

func summarizeSkillContent(content string) string {
	inFence := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence || trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			trimmed = strings.TrimSpace(trimmed[2:])
		}
		if len(trimmed) > 140 {
			trimmed = trimmed[:140] + "..."
		}
		return trimmed
	}
	return ""
}

func normalizeSkillName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func (c SkillCatalog) Count() int {
	return len(c.items)
}

func (c SkillCatalog) EnabledCount() int {
	return len(c.enabled)
}

// SelectableCount reports how many discovered skills are not enabled by
// default and therefore remain available for on-demand selection by relevance.
func (c SkillCatalog) SelectableCount() int {
	count := 0
	for _, skill := range c.items {
		if !skill.Enabled {
			count++
		}
	}
	return count
}

func (c SkillCatalog) Items() []Skill {
	return append([]Skill(nil), c.items...)
}

func (c SkillCatalog) Lookup(name string) (Skill, bool) {
	skill, ok := c.byName[normalizeSkillName(name)]
	return skill, ok
}

func (c SkillCatalog) CatalogPrompt() string {
	if len(c.items) == 0 {
		return ""
	}
	var lines []string
	for _, skill := range c.items {
		summary := skill.Summary
		if summary == "" {
			summary = "No summary available."
		}
		if skill.Enabled {
			lines = append(lines, fmt.Sprintf("- %s (enabled by default): %s", skill.Name, summary))
		} else {
			lines = append(lines, fmt.Sprintf("- %s: %s", skill.Name, summary))
		}
	}
	return strings.Join(lines, "\n")
}

func (c SkillCatalog) DefaultPrompt() string {
	if len(c.enabled) == 0 {
		return ""
	}
	var sections []string
	for _, skill := range c.enabled {
		sections = append(sections, renderSkillPromptSection(skill))
	}
	return strings.Join(sections, "\n\n")
}

func (c SkillCatalog) InjectPromptContext(input string) string {
	matches := explicitSkillPattern.FindAllStringSubmatch(input, -1)
	if len(matches) == 0 {
		return input
	}
	var sections []string
	seen := map[string]bool{}
	for _, match := range matches {
		name := match[1]
		skill, ok := c.Lookup(name)
		if !ok {
			continue
		}
		input = strings.ReplaceAll(input, "$"+name, skill.Name)
		key := normalizeSkillName(skill.Name)
		if seen[key] || skill.Enabled {
			continue
		}
		seen[key] = true
		sections = append(sections, renderSkillPromptSection(skill))
	}
	if len(sections) == 0 {
		return input
	}
	return input + "\n\nActivated skills for this request:\n" + strings.Join(sections, "\n\n")
}

func renderSkillPromptSection(skill Skill) string {
	return fmt.Sprintf("### %s\nSource: %s\n%s", skill.Name, skill.Path, skill.Content)
}

func InitSkillTemplate(name string) string {
	title := strings.TrimSpace(name)
	if title == "" {
		title = "New Skill"
	}
	return fmt.Sprintf(`# %s

## Purpose
- Describe when this skill should be used.

## Workflow
1. Gather the minimum context needed.
2. Perform the task with clear, repeatable steps.
3. Return concise results and any follow-up checks.

## Constraints
- Keep changes focused.
- Prefer existing project conventions.
- Call out risks or assumptions when needed.
`, title)
}
