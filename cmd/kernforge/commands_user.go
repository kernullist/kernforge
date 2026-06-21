package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// UserCommand is a user-defined slash command discovered from a markdown file
// under a commands directory (".kernforge/commands/*.md" in the workspace or
// user config dir). The file body becomes a prompt template that is sent to
// the agent when the command is invoked; "$ARGUMENTS" in the body is replaced
// with the command arguments.
type UserCommand struct {
	Name        string
	Path        string
	Description string
	ArgHint     string
	Body        string
}

// UserCommandSet is the merged, deduplicated set of discovered user commands.
type UserCommandSet struct {
	items  []UserCommand
	byName map[string]UserCommand
}

// userCommandArgumentToken is replaced with the invocation arguments inside a
// user command body when present.
const userCommandArgumentToken = "$ARGUMENTS"

// LoadUserCommands discovers user-defined slash commands from the workspace and
// user config command directories. Built-in commands always win: any file whose
// name collides with a built-in command (or with another already-loaded file)
// is skipped and reported as a warning. Earlier search paths win over later
// ones so workspace commands override user-global commands.
func LoadUserCommands(cwd string, extraPaths []string) (UserCommandSet, []string) {
	searchPaths := append(defaultCommandSearchPaths(cwd), extraPaths...)
	files, warnings := collectUserCommandFiles(searchPaths)

	builtin := builtinCommandNameSet()

	order := []string{}
	byName := map[string]UserCommand{}
	for _, file := range files {
		cmd, err := loadUserCommandFile(file)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("command %s: %v", file, err))
			continue
		}
		key := normalizeSlashCommandName(cmd.Name)
		if key == "" {
			warnings = append(warnings, fmt.Sprintf("command %s: missing name", file))
			continue
		}
		if builtin[key] {
			warnings = append(warnings, fmt.Sprintf("command %s: name %q is a built-in command and was skipped", file, key))
			continue
		}
		if existing, exists := byName[key]; exists {
			warnings = append(warnings, fmt.Sprintf("command %s: name %q already defined by %s and was skipped", file, key, existing.Path))
			continue
		}
		order = append(order, key)
		byName[key] = cmd
	}

	set := UserCommandSet{
		items:  make([]UserCommand, 0, len(order)),
		byName: make(map[string]UserCommand, len(order)),
	}
	for _, key := range order {
		set.items = append(set.items, byName[key])
		set.byName[key] = byName[key]
	}
	return set, warnings
}

func defaultCommandSearchPaths(cwd string) []string {
	// Workspace command directories are searched first so a project-local
	// command wins over a same-named user-global command (the loader keeps the
	// first occurrence of a name). The user config dir is searched last as the
	// fallback location.
	var paths []string
	for _, dir := range ancestorDirs(cwd) {
		paths = append(paths,
			filepath.Join(dir, userConfigDirName, "commands"),
			filepath.Join(dir, "commands"),
		)
	}
	paths = append(paths, filepath.Join(userConfigDir(), "commands"))
	return paths
}

func collectUserCommandFiles(paths []string) ([]string, []string) {
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
			warnings = append(warnings, fmt.Sprintf("command path %s: %v", path, err))
			continue
		}
		if !info.IsDir() {
			if isCommandMarkdownFile(path) && !seen[path] {
				seen[path] = true
				files = append(files, path)
			}
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("command path %s: %v", path, err))
			continue
		}
		// Stable order so collisions resolve deterministically within a dir.
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			names = append(names, entry.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			candidate := filepath.Join(path, name)
			if !isCommandMarkdownFile(candidate) || seen[candidate] {
				continue
			}
			seen[candidate] = true
			files = append(files, candidate)
		}
	}
	return files, warnings
}

func isCommandMarkdownFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
}

func loadUserCommandFile(path string) (UserCommand, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return UserCommand{}, err
	}
	// Reuse the SKILL.md frontmatter parser: the flat "key: value" subset is
	// identical to what command files use.
	front, body := parseSkillFrontmatter(string(data))
	name := strings.TrimSpace(front["name"])
	if name == "" {
		name = commandNameFromPath(path)
	}
	name = normalizeSlashCommandName(name)

	description := strings.TrimSpace(front["description"])
	if description == "" {
		description = summarizeSkillContent(strings.TrimSpace(body))
	}
	description = clampSkillSummary(description)

	cmd := UserCommand{
		Name:        name,
		Path:        path,
		Description: description,
		ArgHint:     strings.TrimSpace(front["argument-hint"]),
		Body:        strings.TrimSpace(body),
	}
	return cmd, nil
}

func commandNameFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// builtinCommandNameSet returns the set of canonical built-in command names so
// user commands can never shadow them.
func builtinCommandNameSet() map[string]bool {
	set := make(map[string]bool, len(slashCommands)+2)
	for _, name := range slashCommands {
		set[normalizeSlashCommandName(name)] = true
	}
	// "quit" is an alias of "exit" handled in the dispatcher.
	set["quit"] = true
	return set
}

// userCommandDescriptionRegistry mirrors discovered user command descriptions
// so the stateless completion rendering layer (UI methods that do not hold a
// runtimeState reference) can show help text for user commands. It is written
// only from reloadExtensions, which runs single-threaded during init/reload.
var (
	userCommandDescriptionMu sync.RWMutex
	userCommandDescriptions  = map[string]string{}
)

func registerUserCommandDescriptions(set UserCommandSet) {
	descriptions := make(map[string]string, len(set.items))
	for _, item := range set.items {
		if strings.TrimSpace(item.Description) != "" {
			descriptions[item.Name] = item.Description
		}
	}
	userCommandDescriptionMu.Lock()
	userCommandDescriptions = descriptions
	userCommandDescriptionMu.Unlock()
}

func userCommandDescription(name string) string {
	userCommandDescriptionMu.RLock()
	defer userCommandDescriptionMu.RUnlock()
	return userCommandDescriptions[normalizeSlashCommandName(name)]
}

func (s UserCommandSet) Count() int {
	return len(s.items)
}

func (s UserCommandSet) Items() []UserCommand {
	return append([]UserCommand(nil), s.items...)
}

func (s UserCommandSet) Lookup(name string) (UserCommand, bool) {
	cmd, ok := s.byName[normalizeSlashCommandName(name)]
	return cmd, ok
}

// Names returns the discovered command names without the leading slash, sorted.
func (s UserCommandSet) Names() []string {
	names := make([]string, 0, len(s.items))
	for _, item := range s.items {
		names = append(names, item.Name)
	}
	sort.Strings(names)
	return names
}

// RenderPrompt expands the command body into a prompt for the agent, replacing
// the "$ARGUMENTS" token with the supplied arguments. When no token is present,
// the arguments are appended after the body so the user's extra text is not lost.
func (c UserCommand) RenderPrompt(args string) string {
	args = strings.TrimSpace(args)
	body := c.Body
	if strings.Contains(body, userCommandArgumentToken) {
		return strings.ReplaceAll(body, userCommandArgumentToken, args)
	}
	if args == "" {
		return body
	}
	if body == "" {
		return args
	}
	return body + "\n\n" + args
}
