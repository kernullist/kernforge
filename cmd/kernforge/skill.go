package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Skill struct {
	Name                   string
	Path                   string
	Summary                string
	Content                string
	AllowedTools           []string
	Enabled                bool
	DisableModelInvocation bool
	UserInvocable          bool
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
		skill, skillWarns, err := loadSkillFile(file)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skill %s: %v", file, err))
			continue
		}
		warnings = append(warnings, skillWarns...)
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

func loadSkillFile(path string) (Skill, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, nil, err
	}
	front, body := parseSkillFrontmatter(string(data))
	content := strings.TrimSpace(body)

	name := strings.TrimSpace(front["name"])
	if name == "" {
		name = extractSkillName(path, content)
	}

	summary := strings.TrimSpace(front["description"])
	// Claude Code appends when_to_use to the description as extra trigger
	// guidance; fold it in so the model sees the full "when to use" text rather
	// than dropping it.
	if when := strings.TrimSpace(front["when_to_use"]); when != "" {
		if summary == "" {
			summary = when
		} else {
			summary = summary + " " + when
		}
	}
	if summary == "" {
		summary = summarizeSkillContent(content)
	} else {
		summary = clampSkillSummary(summary)
	}

	var warnings []string
	for key, label := range skillUnsupportedFrontmatterFields {
		if strings.TrimSpace(front[key]) != "" {
			warnings = append(warnings, fmt.Sprintf("skill %s: frontmatter %q (%s) is not supported by kernforge and is ignored", path, key, label))
		}
	}
	sort.Strings(warnings)

	skill := Skill{
		Name:                   name,
		Path:                   path,
		Summary:                summary,
		Content:                content,
		AllowedTools:           parseSkillToolList(front["allowed-tools"]),
		DisableModelInvocation: parseSkillBool(front["disable-model-invocation"], false),
		UserInvocable:          parseSkillBool(front["user-invocable"], true),
	}
	return skill, warnings, nil
}

// skillUnsupportedFrontmatterFields lists SKILL.md frontmatter keys that other
// agents (Claude Code) act on but kernforge does not implement. They are
// parsed-but-ignored; loadSkillFile warns for each so an imported skill's
// unsupported behavior is visible instead of being silently dropped.
var skillUnsupportedFrontmatterFields = map[string]string{
	"model":            "per-skill model override",
	"effort":           "per-skill effort override",
	"context":          "forked/isolated context",
	"agent":            "subagent type for a forked context",
	"paths":            "path-glob auto-trigger",
	"hooks":            "skill lifecycle hooks",
	"disallowed-tools": "tool denylist",
	"shell":            "shell selection for command blocks",
	"arguments":        "named argument substitution",
}

// parseSkillBool reads a YAML-ish boolean scalar, returning def when the value is
// absent or unrecognized.
func parseSkillBool(value string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "on", "1":
		return true
	case "false", "no", "off", "0":
		return false
	default:
		return def
	}
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

// clampSkillSummary bounds the description/when_to_use text used for trigger
// selection. The cap matches Claude Code's ~1536-char description budget so a
// skill's "when to use" guidance is not truncated (the old 280 cap cut off the
// trigger keywords longer anti-cheat skills rely on). Measured in runes so a
// multibyte (e.g. Korean) description is never split mid-character.
func clampSkillSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	runes := []rune(summary)
	if len(runes) > 1536 {
		summary = strings.TrimSpace(string(runes[:1536])) + "..."
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
		// disable-model-invocation skills are user-only (Claude Code hides them
		// from the model's auto-trigger list), so keep them out of the catalog the
		// model selects from. The user can still reach them with $name.
		if skill.DisableModelInvocation {
			continue
		}
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
		// user-invocable:false skills cannot be triggered by a $name mention
		// (Claude Code hides them from the user menu); the model still reaches them
		// via the catalog / load_skill.
		if !skill.UserInvocable {
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
	header := fmt.Sprintf("### %s\nSource: %s", skill.Name, skill.Path)
	if dir := filepath.Dir(skill.Path); strings.TrimSpace(dir) != "" && dir != "." {
		header += fmt.Sprintf("\nBundled files: any supporting files this skill references (scripts, templates, docs) live in its directory %s; read them with read_file. Instruction paths are relative to that directory.", dir)
	}
	if note := skillAllowedToolsNote(skill); note != "" {
		header += "\n" + note
	}
	return header + "\n" + skill.Content
}

// skillAllowedToolsNote surfaces the skill's allowed-tools as a soft preference,
// matching Claude Code's semantics where allowed-tools is pre-approval (the
// skill's expected tools) rather than a hard denylist of everything else. The
// stateless injection model has no turn-level gate, and Claude Code does not hard
// restrict either, so this is phrased as guidance. Returns "" when the skill
// declares no tool scope.
func skillAllowedToolsNote(skill Skill) string {
	if len(skill.AllowedTools) == 0 {
		return ""
	}
	return "Preferred tools (allowed-tools): this skill's steps are expected to use " + strings.Join(skill.AllowedTools, ", ") + ". Treat these as the pre-approved tools for the skill and prefer them; reach for another tool only when the task genuinely needs it. This is guidance, not a hard restriction."
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
