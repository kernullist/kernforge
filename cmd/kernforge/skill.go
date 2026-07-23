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
	// Builtin is true when the skill body comes from the kernforge binary
	// (go:embed). Built-ins are always catalogued; on-disk user customizations
	// still win when skillPathLooksUserCustomized reports true.
	Builtin bool
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
		// First wins: earlier search paths are more specific (cwd/project before
		// parent directories before the user-global skills dir). Overwriting with a
		// later hit would let a home-directory skill shadow a workspace skill
		// when the working directory lives under the user profile (common on Windows).
		if _, exists := itemsByName[key]; exists {
			continue
		}
		order = append(order, key)
		itemsByName[key] = skill
	}

	// Binary-resident skills (humanize-doc, ...) always join the catalog.
	// User-customized disk skills keep winning over the embed body.
	var builtinWarns []string
	order, itemsByName, builtinWarns = mergeEmbeddedBuiltinSkills(order, itemsByName)
	warnings = append(warnings, builtinWarns...)

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
	// Prefer project-local skills (cwd, then parents up to the nearest project
	// root) over the user-global skills directory. Do not climb past a project
	// root all the way to the user home: on Windows the temp dir often lives
	// under %USERPROFILE%, and unbounded ancestor walks would pick up
	// ~/.kernforge/skills as a "project" path and shadow built-ins / tests.
	// Built-ins are merged later from the binary and still override
	// uncustomized seeded copies under userConfigDir()/skills.
	paths := []string{}
	for _, dir := range skillSearchProjectDirs(cwd) {
		paths = append(paths,
			filepath.Join(dir, userConfigDirName, "skills"),
			filepath.Join(dir, "skills"),
		)
	}
	paths = append(paths, filepath.Join(userConfigDir(), "skills"))
	return paths
}

// skillSearchProjectDirs returns cwd→parent directories up to and including the
// nearest project root (go.mod, .git, ...). Nearest-first order. Never climbs
// into the user home directory: user-global skills are loaded only via
// userConfigDir()/skills, and temp dirs under %USERPROFILE% must not surface
// ~/.kernforge/skills as a project path.
func skillSearchProjectDirs(cwd string) []string {
	abs, err := filepath.Abs(strings.TrimSpace(cwd))
	if err != nil || strings.TrimSpace(abs) == "" {
		return nil
	}
	var nearestFirst []string
	current := abs
	for {
		if skillSearchIsUserHomeDir(current) {
			// Stop before including the home directory itself.
			break
		}
		nearestFirst = append(nearestFirst, current)
		if skillSearchStopsAtDir(current) {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return nearestFirst
}

func skillSearchStopsAtDir(dir string) bool {
	// Markers that define a project boundary for skill discovery.
	for _, marker := range []string{
		".git",
		"go.mod",
		"go.work",
		"Cargo.toml",
		"package.json",
		"pyproject.toml",
		"CMakeLists.txt",
	} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// processStartUserHome is captured once at package init before tests rewrite
// USERPROFILE/HOME. Skill discovery must never climb through the real user
// profile when the working directory is under %TEMP% (common on Windows).
var processStartUserHome string

func init() {
	processStartUserHome, _ = os.UserHomeDir()
}

func skillSearchIsUserHomeDir(dir string) bool {
	dir = filepath.Clean(dir)
	candidates := []string{
		processStartUserHome,
		platformUserConfigBaseDir(),
	}
	if strings.TrimSpace(userConfigDirOverride) != "" {
		candidates = append(candidates, userConfigDirOverride)
	}
	// Also treat the parent of userConfigDir as home (override-safe).
	if cfg := userConfigDir(); cfg != "" {
		candidates = append(candidates, filepath.Dir(cfg))
	}
	for _, home := range candidates {
		home = strings.TrimSpace(home)
		if home == "" {
			continue
		}
		if sameFilePath(dir, home) {
			return true
		}
	}
	return false
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
	return loadSkillFromBytes(path, data)
}

func loadSkillFromBytes(path string, data []byte) (Skill, []string, error) {
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
	blockScalar := false
	for _, line := range lines[:closeIndex] {
		// Folded/literal block scalar body lines are indented continuations of
		// the previous key (description: > / |). Collect them before the
		// blank/comment skip so multi-line skill summaries stay intact.
		if blockScalar && lastKey != "" {
			if strings.TrimSpace(line) == "" {
				// Blank line inside a block scalar: keep a space so words do not
				// glue across paragraphs, then continue the block.
				if front[lastKey] != "" && !strings.HasSuffix(front[lastKey], " ") {
					front[lastKey] = front[lastKey] + " "
				}
				continue
			}
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				blockScalar = false
			} else {
				piece := strings.TrimSpace(line)
				if front[lastKey] == "" || front[lastKey] == ">" || front[lastKey] == "|" {
					front[lastKey] = piece
				} else {
					front[lastKey] = front[lastKey] + " " + piece
				}
				continue
			}
		}
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
		blockScalar = value == ">" || value == "|"
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
// Always-available built-ins (e.g. humanize-doc) are excluded so their presence
// alone does not force the skill catalog into every system prompt; they still
// appear in CatalogPrompt when the catalog is shown for other reasons, and they
// still auto-activate / load via $name and load_skill.
func (c SkillCatalog) SelectableCount() int {
	count := 0
	for _, skill := range c.items {
		// A disable-model-invocation skill is hidden from the model catalog
		// (CatalogPrompt skips it), so it is not selectable on demand by the model;
		// excluding it keeps SelectableCount in sync with the catalog and avoids
		// triggering an empty catalog build.
		if skill.Enabled || skill.DisableModelInvocation {
			continue
		}
		if skill.Builtin && isAlwaysAvailableBuiltinName(skill.Name) {
			continue
		}
		count++
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
		switch {
		case skill.Enabled && skill.Builtin:
			lines = append(lines, fmt.Sprintf("- %s (built-in, enabled by default): %s", skill.Name, summary))
		case skill.Enabled:
			lines = append(lines, fmt.Sprintf("- %s (enabled by default): %s", skill.Name, summary))
		case skill.Builtin:
			lines = append(lines, fmt.Sprintf("- %s (built-in): %s", skill.Name, summary))
		default:
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

// InjectPromptContext rewrites $name tokens in input and auto-activates
// built-ins based on the same text. Prefer InjectPromptContextForRequest when
// input has been enriched with envelope/memory so intent matching stays on the
// raw user request.
func (c SkillCatalog) InjectPromptContext(input string) string {
	return c.InjectPromptContextForRequest(input, input)
}

// InjectPromptContextForRequest rewrites $name tokens in message and decides
// activation from request only. request should be the external user text, not
// the fully enriched tool prompt (envelope/memory must not inject huge skills).
func (c SkillCatalog) InjectPromptContextForRequest(request, message string) string {
	var sections []string
	seen := map[string]bool{}
	input := message
	intentSource := request
	if strings.TrimSpace(intentSource) == "" {
		intentSource = input
	}

	// Activate only $name tokens that appear in the external request. Tokens that
	// only exist in enriched guidance (e.g. "use $humanize-doc after …") must not
	// inject the full skill body.
	if explicitSkillPattern.MatchString(intentSource) {
		for _, token := range explicitSkillPattern.FindAllString(intentSource, -1) {
			name := strings.TrimPrefix(token, "$")
			skill, ok := c.Lookup(name)
			if !ok {
				continue
			}
			key := normalizeSkillName(skill.Name)
			if seen[key] || skill.Enabled || !skill.UserInvocable {
				continue
			}
			seen[key] = true
			sections = append(sections, renderSkillPromptSection(skill))
		}
	}

	// Cosmetic rewrite of $name in the message for known skills (including
	// guidance-only mentions). Does not activate by itself.
	if explicitSkillPattern.MatchString(input) {
		input = explicitSkillPattern.ReplaceAllStringFunc(input, func(token string) string {
			name := strings.TrimPrefix(token, "$")
			skill, ok := c.Lookup(name)
			if !ok {
				return token
			}
			return skill.Name
		})
	}

	// Built-in skills can auto-activate from natural language (e.g. "AI 티 제거").
	for _, skill := range c.items {
		if !shouldAutoActivateBuiltinSkill(skill, intentSource) {
			continue
		}
		key := normalizeSkillName(skill.Name)
		if seen[key] {
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
	source := skill.Path
	if skill.Builtin && (source == "" || strings.HasPrefix(source, "builtin:")) {
		source = "kernforge built-in (" + skill.Name + ")"
	}
	header := fmt.Sprintf("### %s\nSource: %s", skill.Name, source)
	if skill.Builtin {
		header += "\nOrigin: shipped inside the kernforge binary; always available without a user skills install."
	}
	if dir := filepath.Dir(skill.Path); strings.TrimSpace(dir) != "" && dir != "." && !strings.HasPrefix(skill.Path, "builtin:") {
		header += fmt.Sprintf("\nBundled files: any supporting files this skill references (scripts, templates, docs) live in its directory %s; read them with read_file. Instruction paths are relative to that directory. Built-in reference text may also be inlined in this skill body.", dir)
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
