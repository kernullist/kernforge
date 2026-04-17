package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var slashCommands = []string{
	"help",
	"status",
	"provider",
	"profile",
	"version",
	"model",
	"permissions",
	"verify",
	"verify-dashboard",
	"verify-dashboard-html",
	"clear",
	"compact",
	"context",
	"memory",
	"mem",
	"evidence",
	"evidence-search",
	"evidence-show",
	"evidence-dashboard",
	"evidence-dashboard-html",
	"investigate",
	"investigate-dashboard",
	"investigate-dashboard-html",
	"simulate",
	"simulate-dashboard",
	"simulate-dashboard-html",
	"mem-search",
	"mem-show",
	"mem-promote",
	"mem-demote",
	"mem-confirm",
	"mem-tentative",
	"mem-dashboard",
	"mem-dashboard-html",
	"mem-prune",
	"mem-stats",
	"override",
	"override-add",
	"override-clear",
	"checkpoint",
	"checkpoint-auto",
	"detect-verification-tools",
	"set-msbuild-path",
	"clear-msbuild-path",
	"set-cmake-path",
	"clear-cmake-path",
	"set-ctest-path",
	"clear-ctest-path",
	"set-ninja-path",
	"clear-ninja-path",
	"set-auto-verify",
	"checkpoint-diff",
	"locale-auto",
	"checkpoints",
	"rollback",
	"skills",
	"mcp",
	"resources",
	"resource",
	"prompts",
	"prompt",
	"reload",
	"hook-reload",
	"hooks",
	"init",
	"open",
	"selection",
	"selections",
	"use-selection",
	"drop-selection",
	"note-selection",
	"tag-selection",
	"clear-selection",
	"clear-selections",
	"diff-selection",
	"review-selection",
	"review-selections",
	"edit-selection",
	"resume",
	"rename",
	"session",
	"sessions",
	"tasks",
	"diff",
	"export",
	"config",
	"set-analysis-models",
	"set-plan-review",
	"do-plan-review",
	"new-feature",
	"analyze-project",
	"analyze-performance",
	"profile-review",
	"set-max-tool-iterations",
	"exit",
}

var slashCommandDescriptions = map[string]string{
	"help":                       "Show command lists and detailed usage help.",
	"status":                     "Show current session state, approvals, and extension status.",
	"provider":                   "Configure the model provider and inspect provider status.",
	"profile":                    "Manage saved provider and model profiles.",
	"version":                    "Print the current Kernforge version.",
	"model":                      "Show or change the active model.",
	"permissions":                "Inspect or change the session permission mode.",
	"verify":                     "Run manual verification for the current workspace state.",
	"verify-dashboard":           "Summarize recent verification history in the terminal.",
	"verify-dashboard-html":      "Render recent verification history in the HTML dashboard.",
	"clear":                      "Clear the current terminal screen.",
	"compact":                    "Compact the session context to reduce prompt weight.",
	"context":                    "Inspect the current conversation context and memory payloads.",
	"memory":                     "Show or manage short-term memory loaded for this workspace.",
	"mem":                        "Alias for memory commands.",
	"evidence":                   "Capture or review evidence records tied to the workspace.",
	"evidence-search":            "Search saved evidence records by query.",
	"evidence-show":              "Open a specific evidence record by id.",
	"evidence-dashboard":         "Summarize evidence activity in the terminal.",
	"evidence-dashboard-html":    "Render the evidence dashboard in HTML.",
	"investigate":                "Run or manage investigation workflows and snapshots.",
	"investigate-dashboard":      "Summarize investigation history in the terminal.",
	"investigate-dashboard-html": "Render the investigation dashboard in HTML.",
	"simulate":                   "Run or inspect anti-tamper simulation profiles.",
	"simulate-dashboard":         "Summarize simulation history in the terminal.",
	"simulate-dashboard-html":    "Render the simulation dashboard in HTML.",
	"mem-search":                 "Search persistent memory entries.",
	"mem-show":                   "Open a persistent memory entry by id.",
	"mem-promote":                "Promote a memory entry for stronger reuse weight.",
	"mem-demote":                 "Demote a memory entry so it is reused less often.",
	"mem-confirm":                "Mark a tentative memory entry as confirmed.",
	"mem-tentative":              "Mark a memory entry as tentative for later review.",
	"mem-dashboard":              "Summarize persistent memory usage in the terminal.",
	"mem-dashboard-html":         "Render the persistent memory dashboard in HTML.",
	"mem-prune":                  "Prune stale or low-value persistent memory entries.",
	"mem-stats":                  "Show persistent memory counts and health metrics.",
	"override":                   "Inspect or manage temporary hook override rules.",
	"override-add":               "Add a temporary hook override rule.",
	"override-clear":             "Clear active hook override rules.",
	"checkpoint":                 "Create a rollback checkpoint for the workspace.",
	"checkpoint-auto":            "Enable or disable automatic checkpoints before edits.",
	"detect-verification-tools":  "Probe common Windows build and test tool locations.",
	"set-msbuild-path":           "Override the MSBuild executable path for verification.",
	"clear-msbuild-path":         "Clear the workspace MSBuild override path.",
	"set-cmake-path":             "Override the CMake executable path for verification.",
	"clear-cmake-path":           "Clear the workspace CMake override path.",
	"set-ctest-path":             "Override the CTest executable path for verification.",
	"clear-ctest-path":           "Clear the workspace CTest override path.",
	"set-ninja-path":             "Override the Ninja executable path for verification.",
	"clear-ninja-path":           "Clear the workspace Ninja override path.",
	"set-auto-verify":            "Enable or disable automatic verification after edits.",
	"checkpoint-diff":            "Compare current workspace files against a checkpoint.",
	"locale-auto":                "Enable or disable automatic locale switching.",
	"checkpoints":                "List saved checkpoints for the workspace.",
	"rollback":                   "Restore the workspace or selected paths from a checkpoint.",
	"skills":                     "Inspect and manage loaded Codex skills.",
	"mcp":                        "Inspect MCP server status and tool availability.",
	"resources":                  "List MCP resources across configured servers.",
	"resource":                   "Open a specific MCP resource by name or URI.",
	"prompts":                    "List MCP prompts across configured servers.",
	"prompt":                     "Run a specific MCP prompt with JSON arguments.",
	"reload":                     "Reload config, skills, hooks, and MCP state.",
	"hook-reload":                "Reload hook configuration without restarting.",
	"hooks":                      "Inspect loaded hooks and hook runtime status.",
	"init":                       "Bootstrap config, hooks, memory policy, or verify assets.",
	"open":                       "Open a file in the internal text viewer.",
	"selection":                  "Show the current viewer selection.",
	"selections":                 "List saved viewer selections.",
	"use-selection":              "Promote a saved selection to the active one.",
	"drop-selection":             "Remove one saved selection from the stack.",
	"note-selection":             "Attach a note to the active selection.",
	"tag-selection":              "Attach tags to the active selection.",
	"clear-selection":            "Clear the active selection only.",
	"clear-selections":           "Clear the full saved selection stack.",
	"diff-selection":             "Diff the active selection against current changes.",
	"review-selection":           "Review only the active selection.",
	"review-selections":          "Review the saved selection set together.",
	"edit-selection":             "Apply an edit task scoped to the active selection.",
	"resume":                     "Resume a previous session by id.",
	"rename":                     "Rename the current session.",
	"session":                    "Show the current session metadata.",
	"sessions":                   "List recent sessions.",
	"tasks":                      "Show the active plan and task progress.",
	"diff":                       "Show the current workspace diff.",
	"export":                     "Export the current session transcript or artifacts.",
	"config":                     "Show effective configuration values.",
	"set-analysis-models":        "Configure worker and reviewer providers for analysis.",
	"set-plan-review":            "Configure plan review provider behavior.",
	"do-plan-review":             "Run a focused plan review for a task description.",
	"new-feature":                "Create or manage tracked feature workspaces.",
	"analyze-project":            "Run project analysis with a selected analysis mode.",
	"analyze-performance":        "Run a performance-focused project analysis pass.",
	"profile-review":             "Review saved model profiles and compare their fit.",
	"set-max-tool-iterations":    "Adjust the max tool loop count for the session.",
	"exit":                       "Exit the interactive Kernforge session.",
}

var slashSubcommandDescriptions = map[string]map[string]string{
	"permissions": {
		"default":           "Ask before shell, write, and git actions.",
		"acceptEdits":       "Auto-approve workspace edits while still asking for shell and git.",
		"plan":              "Favor planning and read-only analysis before edits.",
		"bypassPermissions": "Bypass runtime permission prompts for this session.",
	},
	"checkpoint-auto": {
		"on":  "Create a safety checkpoint before edits.",
		"off": "Skip automatic checkpoint creation before edits.",
	},
	"locale-auto": {
		"on":  "Let Kernforge switch response locale automatically.",
		"off": "Keep the current response locale fixed.",
	},
	"set-auto-verify": {
		"on":  "Run verification automatically after edits.",
		"off": "Disable automatic post-edit verification.",
	},
	"verify": {
		"--full": "Verify the full workspace instead of changed paths only.",
	},
	"verify-dashboard": {
		"all": "Show dashboard entries across all workspaces.",
	},
	"verify-dashboard-html": {
		"all": "Render dashboard entries across all workspaces in HTML.",
	},
	"mem-prune": {
		"all": "Prune memory entries without limiting to the current workspace.",
	},
	"provider": {
		"status":     "Show the current provider, base URL, key state, and billing visibility.",
		"anthropic":  "Switch to Anthropic provider setup.",
		"openai":     "Switch to OpenAI provider setup.",
		"openrouter": "Switch to OpenRouter provider setup.",
		"ollama":     "Switch to Ollama provider setup.",
	},
	"set-plan-review": {
		"status":     "Show the current plan review provider setting.",
		"anthropic":  "Use Anthropic for plan review passes.",
		"openai":     "Use OpenAI for plan review passes.",
		"openrouter": "Use OpenRouter for plan review passes.",
		"ollama":     "Use Ollama for plan review passes.",
	},
	"set-analysis-models": {
		"status":   "Show current worker and reviewer analysis providers.",
		"worker":   "Set the provider used for worker analysis passes.",
		"reviewer": "Set the provider used for reviewer analysis passes.",
		"clear":    "Clear provider overrides and use defaults again.",
	},
	"analyze-project": {
		"--mode":      "Choose the analysis mode before describing the goal.",
		"map":         "Map structure, modules, and relationships across the project.",
		"trace":       "Trace a concrete flow across files and call sites.",
		"impact":      "Estimate what files and behaviors a change will affect.",
		"security":    "Focus analysis on attack surface and trust boundaries.",
		"performance": "Focus analysis on hotspots and performance costs.",
	},
	"new-feature": {
		"start":     "Create a tracked feature workspace and seed planning files.",
		"status":    "Show the current state of a tracked feature.",
		"list":      "List tracked feature workspaces.",
		"plan":      "Regenerate or inspect the tracked feature plan.",
		"implement": "Execute the next implementation slice for a tracked feature.",
		"close":     "Finish and archive a tracked feature workspace.",
	},
	"investigate": {
		"status":         "Show the current investigation status.",
		"start":          "Start a new investigation from a preset.",
		"snapshot":       "Capture a new investigation snapshot.",
		"note":           "Add an operator note to the active investigation.",
		"stop":           "Stop the active investigation.",
		"show":           "Open a saved investigation by id.",
		"list":           "List investigation sessions.",
		"dashboard":      "Summarize investigations in the terminal.",
		"dashboard-html": "Render the investigation dashboard in HTML.",
	},
	"simulate": {
		"status":              "Show the current simulation status.",
		"show":                "Open a saved simulation result by id.",
		"list":                "List saved simulation results.",
		"dashboard":           "Summarize simulations in the terminal.",
		"dashboard-html":      "Render the simulation dashboard in HTML.",
		"tamper-surface":      "Model obvious tamper vectors and exposed surfaces.",
		"stealth-surface":     "Model stealthier attacker paths and blind spots.",
		"forensic-blind-spot": "Model telemetry gaps that weaken post-incident review.",
	},
	"init": {
		"config":        "Write or refresh starter config files.",
		"hooks":         "Write or refresh starter hook configuration.",
		"memory-policy": "Write or refresh starter memory policy files.",
		"skill":         "Install a named skill into the workspace setup.",
		"verify":        "Write or refresh verification configuration files.",
	},
}

func (rt *runtimeState) completeLine(buffer string) (string, []string, bool) {
	if completed, suggestions, ok := rt.completeSlashCommand(buffer); ok {
		return completed, suggestions, true
	}
	if completed, suggestions, ok := rt.completeMCPCommandTarget(buffer); ok {
		return completed, suggestions, true
	}
	if completed, suggestions, ok := rt.completeOpenPath(buffer); ok {
		return completed, suggestions, true
	}
	if completed, suggestions, ok := rt.completeShellPath(buffer); ok {
		return completed, suggestions, true
	}
	if completed, suggestions, ok := rt.completeMCPMention(buffer); ok {
		return completed, suggestions, true
	}
	if completed, suggestions, ok := rt.completeMentionPath(buffer); ok {
		return completed, suggestions, true
	}
	return buffer, nil, false
}

func (rt *runtimeState) completeSlashCommand(buffer string) (string, []string, bool) {
	trimmedLeft := strings.TrimLeft(buffer, " \t")
	if !strings.HasPrefix(trimmedLeft, "/") {
		return buffer, nil, false
	}
	commandText := strings.TrimPrefix(trimmedLeft, "/")
	if strings.Contains(commandText, " ") {
		if completed, suggestions, ok := rt.completeSlashSubcommand(buffer, trimmedLeft, commandText); ok {
			return completed, suggestions, true
		}
		return buffer, nil, false
	}
	leading := buffer[:len(buffer)-len(trimmedLeft)]
	partial := strings.ToLower(commandText)
	var matches []string
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd, partial) {
			matches = append(matches, cmd)
		}
	}
	if len(matches) == 0 {
		return buffer, nil, true
	}
	if len(matches) == 1 {
		return leading + "/" + matches[0] + " ", nil, true
	}
	prefix := longestCommonPrefix(matches)
	if len(prefix) > len(partial) {
		return leading + "/" + prefix, nil, true
	}
	suggestions := make([]string, 0, len(matches))
	for _, match := range matches {
		suggestions = append(suggestions, "/"+match)
	}
	return buffer, suggestions, true
}

func (rt *runtimeState) completeSlashSubcommand(buffer string, trimmedLeft string, commandText string) (string, []string, bool) {
	parts := strings.SplitN(commandText, " ", 2)
	if len(parts) != 2 {
		return buffer, nil, false
	}
	commandName := strings.ToLower(strings.TrimSpace(parts[0]))
	argText := parts[1]

	completedArg, suggestions, ok := rt.completeSlashArgumentText(commandName, argText)
	if !ok {
		return buffer, nil, false
	}
	leading := buffer[:len(buffer)-len(trimmedLeft)]
	if len(suggestions) > 0 {
		prefixed := make([]string, 0, len(suggestions))
		for _, suggestion := range suggestions {
			prefixed = append(prefixed, leading+"/"+commandName+" "+suggestion)
		}
		return buffer, prefixed, true
	}
	return leading + "/" + commandName + " " + completedArg, nil, true
}

func (rt *runtimeState) completeSlashArgumentText(commandName string, argText string) (string, []string, bool) {
	trimmedArgs := strings.TrimLeft(argText, " \t")
	argFields := strings.Fields(trimmedArgs)
	endsWithSpace := strings.HasSuffix(argText, " ")

	suggestions, replaceIndex, ok := rt.slashArgumentSuggestions(commandName, argFields, endsWithSpace)
	if !ok || len(suggestions) == 0 {
		return "", nil, ok
	}

	if replaceIndex > len(argFields) {
		replaceIndex = len(argFields)
	}
	replaceValue := ""
	if replaceIndex < len(argFields) {
		replaceValue = strings.ToLower(strings.TrimSpace(argFields[replaceIndex]))
	}
	matches := make([]string, 0, len(suggestions))
	for _, suggestion := range suggestions {
		if strings.HasPrefix(strings.ToLower(suggestion), replaceValue) {
			matches = append(matches, suggestion)
		}
	}
	if len(matches) == 0 {
		return "", nil, true
	}

	prefixFields := append([]string(nil), argFields[:replaceIndex]...)
	if len(matches) == 1 {
		finalFields := append(prefixFields, matches[0])
		return strings.Join(finalFields, " ") + " ", nil, true
	}

	prefix := longestCommonPrefix(matches)
	if len(prefix) > len(replaceValue) {
		finalFields := append(prefixFields, prefix)
		return strings.Join(finalFields, " "), nil, true
	}

	rendered := make([]string, 0, len(matches))
	for _, match := range matches {
		finalFields := append(prefixFields, match)
		rendered = append(rendered, strings.Join(finalFields, " "))
	}
	return "", rendered, true
}

func (rt *runtimeState) slashArgumentSuggestions(commandName string, fields []string, endsWithSpace bool) ([]string, int, bool) {
	firstLevel := map[string][]string{
		"permissions":           {"default", "acceptEdits", "plan", "bypassPermissions"},
		"checkpoint-auto":       {"on", "off"},
		"locale-auto":           {"on", "off"},
		"set-auto-verify":       {"on", "off"},
		"provider":              {"status", "anthropic", "openai", "openrouter", "ollama"},
		"analyze-project":       {"--mode"},
		"verify":                {"--full"},
		"verify-dashboard":      {"all"},
		"verify-dashboard-html": {"all"},
		"mem-prune":             {"all"},
		"set-plan-review":       {"status", "anthropic", "openai", "openrouter", "ollama"},
		"set-analysis-models":   {"status", "worker", "reviewer", "clear"},
		"new-feature":           {"start", "status", "list", "plan", "implement", "close"},
		"investigate":           {"status", "start", "snapshot", "note", "stop", "show", "list", "dashboard", "dashboard-html"},
		"simulate":              {"status", "show", "list", "dashboard", "dashboard-html", "tamper-surface", "stealth-surface", "forensic-blind-spot"},
		"init":                  {"config", "hooks", "memory-policy", "skill", "verify"},
	}

	if len(fields) == 0 {
		if options, ok := firstLevel[commandName]; ok {
			return options, 0, true
		}
		return nil, 0, false
	}

	if endsWithSpace {
		fields = append(fields, "")
	}

	switch commandName {
	case "provider":
		if len(fields) <= 1 {
			return firstLevel[commandName], 0, true
		}
		return nil, 0, false
	case "resume":
		if len(fields) <= 1 {
			return rt.recentSessionIDs(), 0, true
		}
		return nil, 0, false
	case "evidence-show":
		if len(fields) <= 1 {
			return rt.recentEvidenceIDs(), 0, true
		}
		return nil, 0, false
	case "mem-show", "mem-promote", "mem-demote", "mem-confirm", "mem-tentative":
		if len(fields) <= 1 {
			return rt.recentPersistentMemoryIDs(), 0, true
		}
		return nil, 0, false
	case "set-plan-review":
		if len(fields) <= 1 {
			return firstLevel[commandName], 0, true
		}
	case "set-analysis-models":
		if len(fields) == 1 {
			return firstLevel[commandName], 0, true
		}
		if len(fields) == 2 && (strings.EqualFold(fields[0], "worker") || strings.EqualFold(fields[0], "reviewer")) {
			return []string{"anthropic", "openai", "openrouter", "ollama"}, 1, true
		}
		return nil, 0, false
	case "analyze-project":
		if len(fields) == 1 {
			return firstLevel[commandName], 0, true
		}
		if len(fields) == 2 && strings.EqualFold(fields[0], "--mode") {
			return supportedProjectAnalysisModes, 1, true
		}
		return nil, 0, false
	case "new-feature":
		if len(fields) == 1 {
			return firstLevel[commandName], 0, true
		}
		if len(fields) == 2 {
			switch strings.ToLower(strings.TrimSpace(fields[0])) {
			case "status", "plan", "implement", "close":
				return rt.recentFeatureIDs(), 1, true
			}
		}
		return nil, 0, false
	case "investigate":
		if len(fields) == 1 {
			return firstLevel[commandName], 0, true
		}
		if len(fields) == 2 && strings.EqualFold(fields[0], "start") {
			return []string{"driver-visibility", "process-visibility", "provider-visibility"}, 1, true
		}
		if len(fields) == 2 && strings.EqualFold(fields[0], "show") {
			return rt.recentInvestigationIDs(), 1, true
		}
		return nil, 0, false
	case "simulate":
		if len(fields) == 1 {
			return firstLevel[commandName], 0, true
		}
		if len(fields) == 2 && strings.EqualFold(fields[0], "show") {
			return rt.recentSimulationIDs(), 1, true
		}
		return nil, 0, false
	case "init":
		if len(fields) == 1 {
			return firstLevel[commandName], 0, true
		}
		return nil, 0, false
	default:
		if options, ok := firstLevel[commandName]; ok && len(fields) <= 1 {
			return options, 0, true
		}
	}

	return nil, 0, false
}

func (rt *runtimeState) recentSessionIDs() []string {
	if rt == nil || rt.store == nil {
		return nil
	}
	items, err := rt.store.List()
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func (rt *runtimeState) recentEvidenceIDs() []string {
	if rt == nil || rt.evidence == nil {
		return nil
	}
	items, err := rt.evidence.ListRecent(rt.workspace.BaseRoot, 12)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func (rt *runtimeState) recentPersistentMemoryIDs() []string {
	if rt == nil || rt.longMem == nil {
		return nil
	}
	items, err := rt.longMem.ListRecent(rt.workspace.BaseRoot, 12)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func (rt *runtimeState) recentInvestigationIDs() []string {
	if rt == nil || rt.investigations == nil {
		return nil
	}
	items, err := rt.investigations.ListRecent(rt.workspace.BaseRoot, 12)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func (rt *runtimeState) recentSimulationIDs() []string {
	if rt == nil || rt.simulations == nil {
		return nil
	}
	items, err := rt.simulations.ListRecent(rt.workspace.BaseRoot, 12)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func (rt *runtimeState) recentFeatureIDs() []string {
	if rt == nil {
		return nil
	}
	store := NewFeatureStore(rt.workspace.BaseRoot)
	items, err := store.List()
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func commandCompletionDescription(item string) string {
	trimmed := strings.TrimSpace(item)
	if !strings.HasPrefix(trimmed, "/") {
		return ""
	}

	fields := strings.Fields(strings.TrimPrefix(trimmed, "/"))
	if len(fields) == 0 {
		return ""
	}

	commandName := strings.ToLower(strings.TrimSpace(fields[0]))
	if len(fields) >= 2 {
		subcommand := strings.ToLower(strings.TrimSpace(fields[1]))
		if descriptions, ok := slashSubcommandDescriptions[commandName]; ok {
			if description := strings.TrimSpace(descriptions[subcommand]); description != "" {
				return description
			}
		}
	}
	return strings.TrimSpace(slashCommandDescriptions[commandName])
}

func (rt *runtimeState) completeMentionPath(buffer string) (string, []string, bool) {
	atIndex := lastMentionStart(buffer)
	if atIndex < 0 {
		return buffer, nil, false
	}
	token := buffer[atIndex+1:]
	searchToken := normalizeTypedPath(token)
	dirPart, partial := splitTypedPath(searchToken)

	baseDir := "."
	if dirPart != "" {
		baseDir = dirPart
	}
	resolvedBase, err := rt.workspace.Resolve(baseDir)
	if err != nil {
		return buffer, nil, true
	}
	entries, err := os.ReadDir(resolvedBase)
	if err != nil {
		return buffer, nil, true
	}

	lowerPartial := strings.ToLower(partial)
	type candidate struct {
		display string
		dir     bool
	}
	var matches []candidate
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(strings.ToLower(name), lowerPartial) {
			continue
		}
		display := name
		if dirPart != "" {
			display = filepath.ToSlash(filepath.Join(dirPart, name))
		}
		if entry.IsDir() {
			display += "/"
		}
		matches = append(matches, candidate{
			display: display,
			dir:     entry.IsDir(),
		})
	}
	if len(matches) == 0 {
		return buffer, nil, true
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].display < matches[j].display })
	if len(matches) == 1 {
		replacement := "@" + matches[0].display
		if !matches[0].dir {
			replacement += " "
		}
		return buffer[:atIndex] + replacement, nil, true
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match.display)
	}
	common := longestCommonPrefixInsensitive(names)
	if len(common) > len(searchToken) {
		return buffer[:atIndex] + "@" + common, nil, true
	}
	suggestions := make([]string, 0, len(matches))
	for _, match := range matches {
		suggestions = append(suggestions, "@"+match.display)
	}
	return buffer, suggestions, true
}

func (rt *runtimeState) completeMCPMention(buffer string) (string, []string, bool) {
	atIndex := lastMentionStart(buffer)
	if atIndex < 0 {
		return buffer, nil, false
	}
	token := buffer[atIndex+1:]
	if !strings.HasPrefix(strings.ToLower(token), "mcp:") {
		return buffer, nil, false
	}
	replacement, suggestions, ok := rt.completeMCPQualifiedTarget(token)
	if !ok {
		return buffer, nil, true
	}
	if len(suggestions) > 0 {
		out := make([]string, 0, len(suggestions))
		for _, suggestion := range suggestions {
			out = append(out, "@"+suggestion)
		}
		return buffer, out, true
	}
	if replacement != token {
		if !strings.HasSuffix(replacement, ":") {
			replacement += " "
		}
		return buffer[:atIndex] + "@" + replacement, nil, true
	}
	return buffer, nil, true
}

func (rt *runtimeState) completeShellPath(buffer string) (string, []string, bool) {
	trimmedLeft := strings.TrimLeft(buffer, " \t")
	lower := strings.ToLower(trimmedLeft)
	command := ""
	dirsOnly := false
	switch {
	case strings.HasPrefix(lower, "!cd "):
		command = trimmedLeft[:4]
		dirsOnly = true
	case strings.HasPrefix(lower, "!ls "):
		command = trimmedLeft[:4]
	default:
		return buffer, nil, false
	}
	leading := buffer[:len(buffer)-len(trimmedLeft)]
	pathPart := trimmedLeft[len(command):]
	completed, suggestions, ok := rt.completeWorkspacePathFiltered(pathPart, dirsOnly)
	if !ok {
		return buffer, nil, true
	}
	if len(suggestions) > 0 {
		prefixed := make([]string, 0, len(suggestions))
		for _, s := range suggestions {
			prefixed = append(prefixed, command+s)
		}
		return buffer, prefixed, true
	}
	return leading + command + completed, nil, true
}

func (rt *runtimeState) completeWorkspacePathFiltered(typed string, dirsOnly bool) (string, []string, bool) {
	searchToken := normalizeTypedPath(typed)
	dirPart, partial := splitTypedPath(searchToken)

	baseDir := "."
	if dirPart != "" {
		baseDir = dirPart
	}
	resolvedBase, err := rt.workspace.Resolve(baseDir)
	if err != nil {
		return typed, nil, false
	}
	entries, err := os.ReadDir(resolvedBase)
	if err != nil {
		return typed, nil, false
	}

	lowerPartial := strings.ToLower(partial)
	type candidate struct {
		display string
		dir     bool
	}
	var matches []candidate
	for _, entry := range entries {
		if dirsOnly && !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(strings.ToLower(name), lowerPartial) {
			continue
		}
		display := name
		if dirPart != "" {
			display = filepath.ToSlash(filepath.Join(dirPart, name))
		}
		if entry.IsDir() {
			display += "/"
		}
		matches = append(matches, candidate{display: display, dir: entry.IsDir()})
	}
	if len(matches) == 0 {
		return typed, nil, false
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].display < matches[j].display })
	if len(matches) == 1 {
		one := matches[0]
		if one.dir {
			return one.display, nil, true
		}
		return one.display + " ", nil, true
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m.display)
	}
	common := longestCommonPrefixInsensitive(names)
	if len(common) > len(searchToken) {
		return common, nil, true
	}
	return typed, names, true
}

func (rt *runtimeState) completeOpenPath(buffer string) (string, []string, bool) {
	trimmedLeft := strings.TrimLeft(buffer, " \t")
	if !strings.HasPrefix(strings.ToLower(trimmedLeft), "/open ") {
		return buffer, nil, false
	}

	leading := buffer[:len(buffer)-len(trimmedLeft)]
	pathPart := trimmedLeft[len("/open "):]
	completed, suggestions, ok := rt.completeWorkspacePathValue(pathPart, true)
	if !ok {
		return buffer, nil, true
	}
	if len(suggestions) > 0 {
		prefixed := make([]string, 0, len(suggestions))
		for _, suggestion := range suggestions {
			prefixed = append(prefixed, "/open "+suggestion)
		}
		return buffer, prefixed, true
	}
	return leading + "/open " + completed, nil, true
}

func (rt *runtimeState) completeMCPCommandTarget(buffer string) (string, []string, bool) {
	trimmedLeft := strings.TrimLeft(buffer, " \t")
	lower := strings.ToLower(trimmedLeft)
	command := ""
	kind := ""
	switch {
	case strings.HasPrefix(lower, "/resource "):
		command = "/resource "
		kind = "resource"
	case strings.HasPrefix(lower, "/prompt "):
		command = "/prompt "
		kind = "prompt"
	default:
		return buffer, nil, false
	}
	leading := buffer[:len(buffer)-len(trimmedLeft)]
	targetPart := trimmedLeft[len(command):]
	if strings.ContainsAny(targetPart, " \t") {
		return buffer, nil, false
	}
	var (
		completed   string
		suggestions []string
		ok          bool
	)
	switch kind {
	case "resource":
		completed, suggestions, ok = rt.completeMCPQualifiedResource("mcp:" + targetPart)
	case "prompt":
		completed, suggestions, ok = rt.completeMCPQualifiedPrompt("mcp:" + targetPart)
	}
	if !ok {
		return buffer, nil, true
	}
	if len(suggestions) > 0 {
		out := make([]string, 0, len(suggestions))
		for _, suggestion := range suggestions {
			out = append(out, command+strings.TrimPrefix(suggestion, "mcp:"))
		}
		return buffer, out, true
	}
	return leading + command + strings.TrimPrefix(completed, "mcp:"), nil, true
}

func (rt *runtimeState) completeMCPQualifiedTarget(token string) (string, []string, bool) {
	return rt.completeMCPQualifiedResource(token)
}

func (rt *runtimeState) completeMCPQualifiedResource(token string) (string, []string, bool) {
	if rt.mcp == nil {
		return token, nil, false
	}
	trimmed := strings.TrimSpace(token)
	if !strings.HasPrefix(strings.ToLower(trimmed), "mcp:") {
		return token, nil, false
	}
	rest := trimmed[len("mcp:"):]
	if rest == "" {
		var suggestions []string
		for _, status := range rt.mcpStatus() {
			suggestions = append(suggestions, "mcp:"+status.Name+":")
		}
		return token, suggestions, true
	}
	if !strings.Contains(rest, ":") {
		partial := strings.ToLower(rest)
		var matches []string
		for _, status := range rt.mcpStatus() {
			if strings.HasPrefix(strings.ToLower(status.Name), partial) {
				matches = append(matches, "mcp:"+status.Name+":")
			}
		}
		if len(matches) == 1 {
			return matches[0], nil, true
		}
		return token, matches, true
	}
	parts := strings.SplitN(rest, ":", 2)
	server := parts[0]
	partial := strings.ToLower(parts[1])
	var matches []string
	for _, item := range rt.mcpResources() {
		if !strings.EqualFold(item.Server, server) {
			continue
		}
		target := item.Resource.URI
		if target == "" {
			target = item.Resource.Name
		}
		if target == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(target), partial) || strings.HasPrefix(strings.ToLower(item.Resource.Name), partial) {
			matches = append(matches, "mcp:"+item.Server+":"+target)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil, true
	}
	return token, matches, true
}

func (rt *runtimeState) completeMCPQualifiedPrompt(token string) (string, []string, bool) {
	if rt.mcp == nil {
		return token, nil, false
	}
	trimmed := strings.TrimSpace(token)
	if !strings.HasPrefix(strings.ToLower(trimmed), "mcp:") {
		return token, nil, false
	}
	rest := trimmed[len("mcp:"):]
	if rest == "" {
		var suggestions []string
		for _, status := range rt.mcpStatus() {
			suggestions = append(suggestions, "mcp:"+status.Name+":")
		}
		return token, suggestions, true
	}
	if !strings.Contains(rest, ":") {
		partial := strings.ToLower(rest)
		var matches []string
		for _, status := range rt.mcpStatus() {
			if strings.HasPrefix(strings.ToLower(status.Name), partial) {
				matches = append(matches, "mcp:"+status.Name+":")
			}
		}
		if len(matches) == 1 {
			return matches[0], nil, true
		}
		return token, matches, true
	}
	parts := strings.SplitN(rest, ":", 2)
	server := parts[0]
	partial := strings.ToLower(parts[1])
	var matches []string
	for _, item := range rt.mcpPrompts() {
		if !strings.EqualFold(item.Server, server) {
			continue
		}
		if strings.HasPrefix(strings.ToLower(item.Prompt.Name), partial) {
			matches = append(matches, "mcp:"+item.Server+":"+item.Prompt.Name)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil, true
	}
	return token, matches, true
}

func (rt *runtimeState) completeWorkspacePathValue(typed string, preferFiles bool) (string, []string, bool) {
	searchToken := normalizeTypedPath(typed)
	dirPart, partial := splitTypedPath(searchToken)

	baseDir := "."
	if dirPart != "" {
		baseDir = dirPart
	}
	resolvedBase, err := rt.workspace.Resolve(baseDir)
	if err != nil {
		return typed, nil, false
	}
	entries, err := os.ReadDir(resolvedBase)
	if err != nil {
		return typed, nil, false
	}

	lowerPartial := strings.ToLower(partial)
	type candidate struct {
		display string
		dir     bool
	}
	var matches []candidate
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(strings.ToLower(name), lowerPartial) {
			continue
		}
		display := name
		if dirPart != "" {
			display = filepath.ToSlash(filepath.Join(dirPart, name))
		}
		if entry.IsDir() {
			display += "/"
		}
		matches = append(matches, candidate{display: display, dir: entry.IsDir()})
	}
	if len(matches) == 0 {
		return typed, nil, false
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].display < matches[j].display })
	if len(matches) == 1 {
		one := matches[0]
		if one.dir {
			return one.display, nil, true
		}
		if preferFiles {
			return one.display + " ", nil, true
		}
		return one.display, nil, true
	}

	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match.display)
	}
	common := longestCommonPrefixInsensitive(names)
	if len(common) > len(searchToken) {
		return common, nil, true
	}
	return typed, names, true
}

func lastMentionStart(buffer string) int {
	for i := len(buffer) - 1; i >= 0; i-- {
		if buffer[i] != '@' {
			continue
		}
		if i > 0 {
			prev := buffer[i-1]
			if prev != ' ' && prev != '\t' && prev != '\n' {
				continue
			}
		}
		if strings.ContainsAny(buffer[i+1:], " \t\n") {
			continue
		}
		return i
	}
	return -1
}

func splitTypedPath(path string) (dirPart string, partial string) {
	if path == "" {
		return "", ""
	}
	if strings.HasSuffix(path, "/") {
		return strings.TrimSuffix(path, "/"), ""
	}
	last := strings.LastIndex(path, "/")
	if last < 0 {
		return "", path
	}
	return path[:last], path[last+1:]
}

func normalizeTypedPath(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}

func longestCommonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for _, value := range values[1:] {
		for !strings.HasPrefix(value, prefix) && prefix != "" {
			prefix = prefix[:len(prefix)-1]
		}
		if prefix == "" {
			return ""
		}
	}
	return prefix
}

func longestCommonPrefixInsensitive(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := []rune(values[0])
	for _, value := range values[1:] {
		runes := []rune(value)
		limit := len(prefix)
		if len(runes) < limit {
			limit = len(runes)
		}
		idx := 0
		for idx < limit && strings.EqualFold(string(prefix[idx]), string(runes[idx])) {
			idx++
		}
		prefix = prefix[:idx]
		if len(prefix) == 0 {
			return ""
		}
	}
	return string(prefix)
}
