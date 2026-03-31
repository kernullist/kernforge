package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Skill struct {
	Name    string
	Path    string
	Summary string
	Content string
	Enabled bool
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
	content := strings.TrimSpace(string(data))
	name := extractSkillName(path, content)
	return Skill{
		Name:    name,
		Path:    path,
		Summary: summarizeSkillContent(content),
		Content: content,
	}, nil
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
