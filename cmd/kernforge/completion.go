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
	"effort",
	"codex-auth",
	"codex-login",
	"specialists",
	"suggest",
	"suggest-dashboard-html",
	"session-dashboard-html",
	"events",
	"continuity",
	"completion-audit",
	"recover",
	"jobs",
	"automation",
	"review-pr",
	"goal",
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
	"fuzz-func",
	"fuzz-campaign",
	"source-scan",
	"create-driver-poc",
	"find-root-cause",
	"root-cause-patterns",
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
	"worktree",
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
	"handoff",
	"tasks",
	"diff",
	"export",
	"config",
	"set-analysis-models",
	"set-specialist-model",
	"set-plan-review",
	"do-plan-review",
	"new-feature",
	"analyze-project",
	"analyze-dashboard",
	"docs-refresh",
	"analyze-performance",
	"profile-review",
	"set-max-tool-iterations",
	"progress-display",
	"exit",
}

var slashCommandDescriptions = map[string]string{
	"help":                       "Show command lists and detailed usage help.",
	"status":                     "Show current session state, approvals, and extension status.",
	"provider":                   "Configure the model provider and inspect provider status.",
	"profile":                    "Show saved main profiles plus each profile's role model set.",
	"version":                    "Print the current Kernforge version.",
	"model":                      "Show explicit or inherited model routing and interactively reconfigure one target.",
	"effort":                     "Show or set reasoning effort for a configured model target.",
	"codex-auth":                 "Manage Kernforge-owned OpenAI Codex OAuth login state.",
	"codex-login":                "Start Kernforge-owned OpenAI Codex OAuth login.",
	"specialists":                "Show specialist profiles plus editable ownership and worktree routing state.",
	"suggest":                    "Inspect proactive situation judgment, suggested next actions, and suggestion mode.",
	"suggest-dashboard-html":     "Render proactive situation judgment and suggestions in an HTML dashboard.",
	"session-dashboard-html":     "Render the current session thread, task graph, automation, and artifact state in an HTML dashboard.",
	"events":                     "Tail or export session events as JSONL for local dashboards, schedulers, and app-server style clients.",
	"continuity":                 "Write a long-task continuity packet with recovery actions, worktrees, jobs, changed files, and next commands.",
	"completion-audit":           "Write a completion readiness audit with blockers, warnings, verification, tasks, jobs, and artifact evidence.",
	"recover":                    "Write a failure recovery brief with recent errors, verification failure, jobs, actions, and next commands.",
	"jobs":                       "Inspect or cancel persistent background shell jobs and bundles from the terminal.",
	"automation":                 "Manage reusable scheduled verification and PR review automation slots.",
	"review-pr":                  "Generate a PR review automation report, draft comments, and optionally post them through gh when explicit.",
	"goal":                       "Run an autonomous Codex-style goal loop from a prompt or markdown file until audit-ready or blocked.",
	"permissions":                "Inspect or change the session permission mode.",
	"verify":                     "Run verification and suggest the next repair, dashboard, checkpoint, or feature workflow step.",
	"verify-dashboard":           "Summarize recent verification history in the terminal.",
	"verify-dashboard-html":      "Render recent verification history in the HTML dashboard.",
	"clear":                      "Clear the current terminal screen.",
	"compact":                    "Compact the session context to reduce prompt weight.",
	"context":                    "Inspect the current conversation context and memory payloads.",
	"memory":                     "Show or manage short-term memory loaded for this workspace.",
	"mem":                        "Show persistent memory and suggest confirm, promote, verify, or dashboard follow-up.",
	"evidence":                   "Review evidence records and suggest verification, dashboard, or source follow-up.",
	"evidence-search":            "Search evidence records and suggest verification, dashboard, or source follow-up.",
	"evidence-show":              "Open one evidence record and suggest the next verification or dashboard step.",
	"evidence-dashboard":         "Summarize evidence activity in the terminal.",
	"evidence-dashboard-html":    "Render the evidence dashboard in HTML.",
	"investigate":                "Run investigation workflows and suggest the next snapshot, simulation, or evidence step.",
	"investigate-dashboard":      "Summarize investigation history in the terminal.",
	"investigate-dashboard-html": "Render the investigation dashboard in HTML.",
	"simulate":                   "Run anti-tamper simulation profiles and suggest verification or evidence follow-up.",
	"fuzz-func":                  "Auto-plan directed function fuzzing and suggest the campaign handoff when source-only scenarios are ready.",
	"fuzz-campaign":              "Inspect the fuzz campaign planner or let Kernforge advance seeds, deduplicated findings, parsed coverage reports, sanitizer/verifier artifacts, native results, evidence, and verification gates.",
	"source-scan":                "Scan source with built-in kernel, C++, Unreal, and telemetry matchers, then hand candidates to /fuzz-func.",
	"create-driver-poc":          "Generate an x64 C++20 MSVC kernel-driver POC solution with a same-directory SCM/IOCTL tester executable.",
	"find-root-cause":            "Analyze a reported problem with 1-8 route-limited worker shards, reviewer validation, fuzz-like value assumption checks, and root-cause synthesis.",
	"root-cause-patterns":        "Inspect built-in root-cause bug pattern packs, match the current workspace, and collect/normalize GitHub issue priors.",
	"simulate-dashboard":         "Summarize simulation history in the terminal.",
	"simulate-dashboard-html":    "Render the simulation dashboard in HTML.",
	"mem-search":                 "Search persistent memory and suggest confirm, promote, verify, or dashboard follow-up.",
	"mem-show":                   "Open one memory entry and suggest confirm, promote, or verification follow-up.",
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
	"checkpoint":                 "Create a rollback checkpoint and suggest diff or checkpoint-list follow-up.",
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
	"worktree":                   "Create, inspect, detach, or clean isolated git worktrees with tracked-feature follow-up.",
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
	"handoff":                    "Generate a compact delegation handoff artifact for another agent or cloud task.",
	"tasks":                      "Show the active plan and task progress.",
	"diff":                       "Show the current workspace diff.",
	"export":                     "Export the current session transcript or artifacts.",
	"config":                     "Show effective configuration values.",
	"set-analysis-models":        "Configure worker and reviewer providers for analysis.",
	"set-specialist-model":       "Configure the provider and model used by one specialist subagent.",
	"set-plan-review":            "Configure plan review provider behavior.",
	"do-plan-review":             "Run a focused plan review for a task description.",
	"new-feature":                "Create or manage tracked feature workspaces with implement, verify, close, and cleanup handoffs.",
	"analyze-project":            "Run project analysis and suggest the next dashboard, fuzzing, or verification step.",
	"analyze-dashboard":          "Open the latest project analysis document portal with search, graph-linked stale diff, trust/data graphs, attack flows, and drilldowns.",
	"docs-refresh":               "Regenerate latest project analysis docs, graph section stale markers, schema manifest, dashboard, and vector corpus from saved artifacts.",
	"analyze-performance":        "Run a performance-focused analysis pass and suggest the next hotspot follow-up.",
	"profile-review":             "Review saved model profiles and compare their fit.",
	"set-max-tool-iterations":    "Adjust the max tool loop count for the session.",
	"progress-display":           "Show or set how in-flight progress updates are displayed.",
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
	"progress-display": {
		"auto":    "Persist durable tool/model events and keep noisy updates transient.",
		"compact": "Keep progress updates in the transient footer.",
		"stream":  "Write every progress update to the transcript.",
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
		"status":       "Show the current provider, base URL, key state, and billing visibility.",
		"anthropic":    "Switch to Anthropic provider setup.",
		"openai":       "Switch to OpenAI provider setup.",
		"openrouter":   "Switch to OpenRouter provider setup.",
		"deepseek":     "Switch to DeepSeek provider setup.",
		"opencode":     "Switch to OpenCode Zen API provider setup.",
		"opencode-go":  "Switch to OpenCode Go subscription provider setup.",
		"ollama":       "Switch to Ollama provider setup.",
		"codex-cli":    "Switch to Codex CLI provider setup.",
		"openai-codex": "Switch to direct OpenAI Codex OAuth provider setup.",
		"lmstudio":     "Switch to local LM Studio OpenAI-compatible provider setup.",
		"vllm":         "Switch to local vLLM OpenAI-compatible provider setup.",
		"llama.cpp":    "Switch to local llama.cpp OpenAI-compatible provider setup.",
	},
	"effort": {
		"main":              "Set reasoning effort for the active main model.",
		"plan-review":       "Set reasoning effort for the plan-review reviewer model.",
		"analysis-worker":   "Set reasoning effort for the project-analysis worker model.",
		"analysis-reviewer": "Set reasoning effort for the project-analysis reviewer model.",
		"specialist":        "Set reasoning effort for a specialist model.",
		"undefined":         "Do not send a reasoning effort override.",
		"minimal":           "Use minimal reasoning where the selected model supports it.",
		"low":               "Favor speed and lower reasoning token use.",
		"medium":            "Use balanced reasoning effort.",
		"high":              "Favor deeper reasoning.",
		"xhigh":             "Request extra-high reasoning for providers that support it.",
	},
	"codex-auth": {
		"status": "Show Kernforge-owned OpenAI Codex OAuth state.",
		"login":  "Start device OAuth login for OpenAI Codex.",
		"logout": "Remove the Kernforge-owned OpenAI Codex OAuth file.",
		"path":   "Show the OpenAI Codex OAuth auth file path.",
	},
	"profile": {
		"list":   "Show saved provider/model profiles without activating one.",
		"show":   "Show saved provider/model profiles without activating one.",
		"status": "Show saved provider/model profiles without activating one.",
		"pin":    "Pin one saved profile by number.",
		"unpin":  "Unpin one saved profile by number.",
		"rename": "Rename one saved profile by number.",
		"delete": "Delete one saved profile by number.",
	},
	"profile-review": {
		"list":   "Show saved plan-review profiles without activating one.",
		"show":   "Show saved plan-review profiles without activating one.",
		"status": "Show saved plan-review profiles without activating one.",
		"pin":    "Pin one saved review profile by number.",
		"unpin":  "Unpin one saved review profile by number.",
		"rename": "Rename one saved review profile by number.",
		"delete": "Delete one saved review profile by number.",
	},
	"set-plan-review": {
		"status":       "Show the current plan review provider setting.",
		"anthropic":    "Use Anthropic for plan review passes.",
		"openai":       "Use OpenAI for plan review passes.",
		"openrouter":   "Use OpenRouter for plan review passes.",
		"deepseek":     "Use DeepSeek for plan review passes.",
		"opencode":     "Use OpenCode Zen API for plan review passes.",
		"opencode-go":  "Use OpenCode Go for plan review passes.",
		"ollama":       "Use Ollama for plan review passes.",
		"codex-cli":    "Use Codex CLI for plan review passes.",
		"openai-codex": "Use direct OpenAI Codex OAuth for plan review passes.",
		"lmstudio":     "Use local LM Studio for plan review passes.",
		"vllm":         "Use local vLLM for plan review passes.",
		"llama.cpp":    "Use local llama.cpp for plan review passes.",
	},
	"set-analysis-models": {
		"status":   "Show current worker and reviewer analysis providers.",
		"worker":   "Set the provider used for worker analysis passes.",
		"reviewer": "Set the provider used for reviewer analysis passes.",
		"clear":    "Clear provider overrides and use defaults again.",
	},
	"set-specialist-model": {
		"status": "Show the effective provider and model for specialist subagents.",
		"clear":  "Clear one specialist override or remove all specialist model overrides.",
	},
	"analyze-project": {
		"--mode":      "Choose the analysis mode; Kernforge will infer a default goal when you omit one.",
		"--path":      "Limit analysis to one workspace directory or file path; a goal is optional.",
		"map":         "Map structure, modules, and relationships across the project.",
		"trace":       "Trace a concrete flow across files and call sites.",
		"impact":      "Estimate what files and behaviors a change will affect.",
		"surface":     "Focus on concrete IOCTL, RPC, parser, handle, memory, and network surfaces.",
		"security":    "Focus analysis on attack surface and trust boundaries.",
		"performance": "Focus analysis on hotspots and performance costs.",
	},
	"analyze-dashboard": {
		"latest": "Open the latest analyze-project document portal.",
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
	"fuzz-func": {
		"status":   "Show the latest function fuzz planning status.",
		"show":     "Open one saved function fuzz plan by id.",
		"list":     "List saved function fuzz planning runs.",
		"continue": "Approve a pending recovered build configuration and start autonomous fuzzing.",
		"language": "Show or change /fuzz-func output language. Use system to follow the PC language or english to force English.",
	},
	"fuzz-campaign": {
		"status": "Show the latest fuzz campaign plus Kernforge's recommended next step.",
		"run":    "Let Kernforge create, attach, promote seeds, deduplicate findings, ingest coverage reports and sanitizer/verifier artifacts, and capture native result evidence when supported.",
		"new":    "Create a fuzz campaign under .kernforge/fuzz/<campaign-id>/.",
		"list":   "List recent fuzz campaigns for this workspace.",
		"show":   "Show one fuzz campaign by id or latest.",
	},
	"source-scan": {
		"status":     "Show recent source candidate scan state.",
		"run":        "Run function-window source matchers and persist evidence-rich candidate records.",
		"list":       "List recent source candidates for this workspace.",
		"show":       "Show one source candidate by id or latest.",
		"revalidate": "Refresh candidate fingerprints, stale state, and source/native verifier verdict.",
	},
	"find-root-cause": {
		"<problem>": "Describe the runtime symptom or failure; Kernforge selects likely source shards and reports plausible root causes.",
	},
	"root-cause-patterns": {
		"list":          "List built-in root-cause patterns, optionally filtered by project type.",
		"match":         "Match a problem symptom against the current workspace and built-in pattern pack.",
		"github-search": "Collect closed GitHub issues for a project type or query into a JSON corpus.",
		"normalize":     "Convert a GitHub issue corpus into a provisional root-cause pattern pack.",
		"validate":      "Check built-in pack counts and coverage.",
	},
	"init": {
		"config":        "Write or refresh starter config files.",
		"hooks":         "Write or refresh starter hook configuration.",
		"memory-policy": "Write or refresh starter memory policy files.",
		"skill":         "Install a named skill into the workspace setup.",
		"verify":        "Write or refresh verification configuration files.",
	},
	"worktree": {
		"status":  "Show the current worktree isolation status and attached metadata.",
		"list":    "List session, specialist, and git worktree records for continuity.",
		"create":  "Create and attach an isolated git worktree for this session.",
		"enter":   "Re-enter the recorded isolated worktree after leaving it.",
		"attach":  "Attach an existing git worktree path to this session.",
		"leave":   "Detach from the current isolated worktree without deleting it.",
		"cleanup": "Remove the recorded isolated worktree after it is clean.",
	},
	"jobs": {
		"status":        "Show persisted background shell jobs and bundles.",
		"check":         "Poll one background job by id or latest.",
		"bundle":        "Poll one background bundle by id or latest.",
		"cancel":        "Cancel one background job by id or latest.",
		"cancel-bundle": "Cancel one background bundle by id or latest.",
	},
	"specialists": {
		"status":  "Show specialist profiles plus editable ownership and worktree assignments.",
		"assign":  "Bind one task-graph node to an editable specialist and ensure its worktree lease.",
		"cleanup": "Remove one or all specialist worktrees recorded for this session.",
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
	if strings.TrimSpace(completedArg) == "" {
		return buffer, nil, true
	}
	return leading + "/" + commandName + " " + completedArg, nil, true
}

func (rt *runtimeState) completeSlashArgumentText(commandName string, argText string) (string, []string, bool) {
	trimmedArgs := strings.TrimLeft(argText, " \t")
	argFields := strings.Fields(trimmedArgs)
	endsWithSpace := strings.HasSuffix(argText, " ")

	if completedArg, suggestions, ok := rt.completeFuzzFuncAtPathArgument(commandName, argFields, endsWithSpace); ok {
		return completedArg, suggestions, true
	}
	if completedArg, suggestions, ok := rt.completeAnalyzeProjectPathArgument(commandName, argFields, endsWithSpace); ok {
		return completedArg, suggestions, true
	}

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
		if replaceValue == "" && strings.HasPrefix(matches[0], "<") {
			return "", matches, true
		}
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

func (rt *runtimeState) completeFuzzFuncAtPathArgument(commandName string, fields []string, endsWithSpace bool) (string, []string, bool) {
	if commandName != "fuzz-func" || endsWithSpace || len(fields) == 0 {
		return "", nil, false
	}

	replaceIndex := len(fields) - 1
	token := strings.TrimSpace(fields[replaceIndex])
	if !strings.HasPrefix(token, "@") {
		return "", nil, false
	}

	completedPath, suggestions, ok := rt.completeWorkspacePathFiltered(strings.TrimPrefix(token, "@"), false)
	if !ok {
		return "", nil, true
	}

	prefixFields := append([]string(nil), fields[:replaceIndex]...)
	if len(suggestions) > 0 {
		rendered := make([]string, 0, len(suggestions))
		for _, suggestion := range suggestions {
			finalFields := append(prefixFields, "@"+suggestion)
			rendered = append(rendered, strings.Join(finalFields, " "))
		}
		return "", rendered, true
	}

	finalFields := append(prefixFields, "@"+completedPath)
	return strings.Join(finalFields, " "), nil, true
}

func (rt *runtimeState) completeAnalyzeProjectPathArgument(commandName string, fields []string, endsWithSpace bool) (string, []string, bool) {
	if commandName != "analyze-project" || len(fields) == 0 {
		return "", nil, false
	}
	pathIndex := -1
	pathPrefix := ""
	if !endsWithSpace {
		lastIndex := len(fields) - 1
		last := strings.TrimSpace(fields[lastIndex])
		if strings.HasPrefix(last, "--path=") {
			pathIndex = lastIndex
			pathPrefix = "--path="
		}
	}
	if pathIndex < 0 {
		for index, field := range fields {
			if !strings.EqualFold(field, "--path") {
				continue
			}
			if endsWithSpace && index == len(fields)-1 {
				pathIndex = len(fields)
				break
			}
			if !endsWithSpace && index+1 == len(fields)-1 {
				pathIndex = index + 1
				break
			}
		}
	}
	if pathIndex < 0 {
		return "", nil, false
	}
	typed := ""
	if pathIndex < len(fields) {
		typed = strings.TrimPrefix(fields[pathIndex], pathPrefix)
	}
	completedPath, suggestions, ok := rt.completeWorkspacePathFiltered(typed, false)
	if !ok {
		return "", nil, true
	}
	prefixFields := append([]string(nil), fields[:analysisMinInt(pathIndex, len(fields))]...)
	if len(suggestions) > 0 {
		rendered := make([]string, 0, len(suggestions))
		for _, suggestion := range suggestions {
			item := suggestion
			if pathPrefix != "" {
				item = pathPrefix + suggestion
			}
			finalFields := append(prefixFields, item)
			rendered = append(rendered, strings.Join(finalFields, " "))
		}
		return "", rendered, true
	}
	item := completedPath
	if pathPrefix != "" {
		item = pathPrefix + completedPath
	}
	finalFields := append(prefixFields, item)
	return strings.Join(finalFields, " "), nil, true
}

func analyzeProjectSlashArgumentSuggestions(fields []string, firstLevel []string) ([]string, int, bool) {
	if len(fields) == 0 {
		return firstLevel, 0, true
	}
	if len(fields) == 1 {
		return availableAnalyzeProjectFlags(fields, firstLevel), 0, true
	}
	for index := 0; index < len(fields); index++ {
		if strings.EqualFold(fields[index], "--mode") {
			if index+1 >= len(fields) || index+1 == len(fields)-1 {
				return supportedProjectAnalysisModes, index + 1, true
			}
		}
	}
	replaceIndex := len(fields) - 1
	if strings.HasPrefix(strings.TrimSpace(fields[replaceIndex]), "--") {
		return availableAnalyzeProjectFlags(fields[:replaceIndex], firstLevel), replaceIndex, true
	}
	return nil, 0, false
}

func availableAnalyzeProjectFlags(fields []string, firstLevel []string) []string {
	used := map[string]bool{}
	for _, field := range fields {
		used[strings.ToLower(strings.TrimSpace(field))] = true
	}
	var out []string
	for _, flag := range firstLevel {
		if !used[strings.ToLower(strings.TrimSpace(flag))] {
			out = append(out, flag)
		}
	}
	return out
}

func (rt *runtimeState) slashArgumentSuggestions(commandName string, fields []string, endsWithSpace bool) ([]string, int, bool) {
	firstLevel := map[string][]string{
		"permissions":           {"default", "acceptEdits", "plan", "bypassPermissions"},
		"checkpoint-auto":       {"on", "off"},
		"locale-auto":           {"on", "off"},
		"set-auto-verify":       {"on", "off"},
		"progress-display":      {"auto", "compact", "stream"},
		"worktree":              {"status", "list", "create", "enter", "attach", "leave", "cleanup"},
		"jobs":                  {"status", "check", "bundle", "cancel", "cancel-bundle"},
		"specialists":           {"status", "assign", "cleanup"},
		"provider":              {"status", "anthropic", "openai", "openrouter", "deepseek", "opencode", "opencode-go", "ollama", "codex-cli", "openai-codex", "lmstudio", "vllm", "llama.cpp"},
		"effort":                {"undefined", "minimal", "low", "medium", "high", "xhigh"},
		"codex-auth":            {"status", "login", "logout", "path"},
		"profile":               {"list", "show", "status", "pin", "unpin", "rename", "delete"},
		"profile-review":        {"list", "show", "status", "pin", "unpin", "rename", "delete"},
		"analyze-project":       {"--mode", "--path"},
		"analyze-dashboard":     {"latest"},
		"verify":                {"--full"},
		"verify-dashboard":      {"all"},
		"verify-dashboard-html": {"all"},
		"review-pr":             {"--github", "--draft-comments", "--post-comments", "--resolve-thread", "--draft-issue", "--create-issue", "--label", "--assignee", "--milestone"},
		"handoff":               {"import"},
		"mem-prune":             {"all"},
		"set-plan-review":       {"status", "anthropic", "openai", "openrouter", "deepseek", "opencode", "opencode-go", "ollama", "codex-cli", "openai-codex", "lmstudio", "vllm", "llama.cpp"},
		"set-analysis-models":   {"status", "worker", "reviewer", "clear"},
		"set-specialist-model":  {"status", "clear"},
		"new-feature":           {"start", "status", "list", "plan", "implement", "close"},
		"investigate":           {"status", "start", "snapshot", "note", "stop", "show", "list", "dashboard", "dashboard-html"},
		"simulate":              {"status", "show", "list", "dashboard", "dashboard-html", "tamper-surface", "stealth-surface", "forensic-blind-spot"},
		"fuzz-func":             {"<function-name>", "<function-name> --file <path>", "<function-name> @<path>", "<function-name> --source-scan focused", "<function-name> --source-scan full", "<function-name> --no-source-scan", "--from-candidate <id>", "--file <path>", "@<path>", "status", "show", "list", "continue", "language"},
		"fuzz-campaign":         {"status", "run", "new", "list", "show"},
		"source-scan":           {"status", "run", "run --limit 50", "run --only-slugs probe-copy-size-drift,double-fetch-user-buffer", "run --files driver/nsi.c,api/registry.c", "list", "show", "revalidate"},
		"create-driver-poc":     {"<driver-name>"},
		"automation":            {"status", "due", "digest", "monitor", "monitor --notify", "monitor --webhook-url", "watch", "watch --notify", "watch --once", "watch --webhook-url", "daemon-start", "daemon-status", "daemon-stop", "notify", "notify --webhook-url", "run-due"},
		"init":                  {"config", "hooks", "memory-policy", "skill", "verify"},
	}

	if len(fields) == 0 {
		if commandName == "set-specialist-model" {
			options := append([]string{}, firstLevel[commandName]...)
			options = append(options, rt.allSpecialistNames()...)
			return normalizeTaskStateList(options, 32), 0, true
		}
		if options, ok := firstLevel[commandName]; ok {
			return options, 0, true
		}
		return nil, 0, false
	}

	if endsWithSpace {
		fields = append(fields, "")
	}

	switch commandName {
	case "effort":
		if len(fields) <= 1 {
			return firstLevel[commandName], 0, true
		}
		target := strings.ToLower(strings.TrimSpace(fields[0]))
		if target == "specialist" {
			if len(fields) == 2 {
				return rt.allSpecialistNames(), 0, true
			}
			if len(fields) == 3 {
				return []string{"undefined", "minimal", "low", "medium", "high", "xhigh"}, 0, true
			}
			return nil, 0, false
		}
		switch target {
		case "main", "plan-review", "plan_reviewer", "plan-reviewer", "reviewer", "analysis-worker", "analysis_worker", "worker", "analysis-reviewer", "analysis_reviewer":
			if len(fields) == 2 {
				return []string{"undefined", "minimal", "low", "medium", "high", "xhigh"}, 0, true
			}
		default:
			if strings.HasPrefix(target, "specialist:") && len(fields) == 2 {
				return []string{"undefined", "minimal", "low", "medium", "high", "xhigh"}, 0, true
			}
		}
		return nil, 0, false
	case "codex-auth":
		if len(fields) <= 1 {
			return firstLevel[commandName], 0, true
		}
		return nil, 0, false
	case "provider":
		if len(fields) <= 1 {
			return firstLevel[commandName], 0, true
		}
		return nil, 0, false
	case "worktree":
		if len(fields) <= 1 {
			return firstLevel[commandName], 0, true
		}
		return nil, 0, false
	case "jobs":
		if len(fields) <= 1 {
			return firstLevel[commandName], 0, true
		}
		if len(fields) == 2 && (strings.EqualFold(fields[0], "check") || strings.EqualFold(fields[0], "job") || strings.EqualFold(fields[0], "cancel")) {
			return append([]string{"latest"}, rt.recentBackgroundJobIDs()...), 1, true
		}
		if len(fields) == 2 && (strings.EqualFold(fields[0], "bundle") || strings.EqualFold(fields[0], "check-bundle") || strings.EqualFold(fields[0], "cancel-bundle")) {
			return append([]string{"latest"}, rt.recentBackgroundBundleIDs()...), 1, true
		}
		return nil, 0, false
	case "specialists":
		if len(fields) == 1 {
			return firstLevel[commandName], 0, true
		}
		if len(fields) == 2 && strings.EqualFold(fields[0], "assign") {
			return rt.recentTaskGraphNodeIDs(), 1, true
		}
		if len(fields) == 3 && strings.EqualFold(fields[0], "assign") {
			return rt.editableSpecialistNames(), 2, true
		}
		if len(fields) == 2 && strings.EqualFold(fields[0], "cleanup") {
			options := append([]string{"all"}, rt.activeSpecialistNames()...)
			return normalizeTaskStateList(options, 16), 1, true
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
			return []string{"anthropic", "openai", "openrouter", "deepseek", "opencode", "opencode-go", "ollama", "codex-cli", "openai-codex", "lmstudio", "vllm", "llama.cpp"}, 1, true
		}
		return nil, 0, false
	case "set-specialist-model":
		if len(fields) == 1 {
			options := append([]string{}, firstLevel[commandName]...)
			options = append(options, rt.allSpecialistNames()...)
			return normalizeTaskStateList(options, 32), 0, true
		}
		if len(fields) == 2 {
			if strings.EqualFold(fields[0], "status") || strings.EqualFold(fields[0], "clear") {
				options := append([]string{}, rt.allSpecialistNames()...)
				if strings.EqualFold(fields[0], "clear") {
					options = append([]string{"all"}, options...)
				}
				return normalizeTaskStateList(options, 32), 1, true
			}
			if rt.hasSpecialistName(fields[0]) {
				return []string{"anthropic", "openai", "openrouter", "deepseek", "opencode", "opencode-go", "ollama", "codex-cli", "openai-codex", "lmstudio", "vllm", "llama.cpp"}, 1, true
			}
		}
		return nil, 0, false
	case "analyze-project":
		return analyzeProjectSlashArgumentSuggestions(fields, firstLevel[commandName])
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
	case "fuzz-func":
		if len(fields) == 1 {
			return firstLevel[commandName], 0, true
		}
		if len(fields) == 2 && strings.EqualFold(fields[0], "--from-candidate") {
			return rt.recentSourceCandidateIDs(), 1, true
		}
		if len(fields) >= 2 && strings.EqualFold(fields[len(fields)-2], "--source-scan") {
			return []string{"focused", "full", "off"}, len(fields) - 1, true
		}
		if len(fields) == 2 && (strings.EqualFold(fields[0], "language") || strings.EqualFold(fields[0], "lang")) {
			return []string{"system", "english"}, 1, true
		}
		if len(fields) == 2 && strings.EqualFold(fields[0], "show") {
			options := append([]string{"latest"}, rt.recentFunctionFuzzIDs()...)
			return uniqueStrings(options), 1, true
		}
		if len(fields) == 2 && strings.EqualFold(fields[0], "continue") {
			options := append([]string{"latest"}, rt.recentFunctionFuzzIDs()...)
			return uniqueStrings(options), 1, true
		}
		return nil, 0, false
	case "fuzz-campaign":
		if len(fields) == 1 {
			return firstLevel[commandName], 0, true
		}
		if len(fields) == 2 && strings.EqualFold(fields[0], "show") {
			return append([]string{"latest"}, rt.recentFuzzCampaignIDs()...), 1, true
		}
		return nil, 0, false
	case "source-scan":
		if len(fields) == 1 {
			return firstLevel[commandName], 0, true
		}
		if len(fields) == 2 && (strings.EqualFold(fields[0], "show") || strings.EqualFold(fields[0], "revalidate")) {
			return append([]string{"latest"}, rt.recentSourceCandidateIDs()...), 1, true
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

func (rt *runtimeState) recentTaskGraphNodeIDs() []string {
	if rt == nil || rt.session == nil || rt.session.TaskGraph == nil {
		return nil
	}
	ids := make([]string, 0, len(rt.session.TaskGraph.Nodes))
	for _, node := range rt.session.TaskGraph.Nodes {
		if strings.TrimSpace(node.ID) != "" {
			ids = append(ids, node.ID)
		}
	}
	return ids
}

func (rt *runtimeState) recentBackgroundJobIDs() []string {
	if rt == nil || rt.session == nil {
		return nil
	}
	ids := make([]string, 0, len(rt.session.BackgroundJobs))
	for _, job := range rt.session.BackgroundJobs {
		if strings.TrimSpace(job.ID) != "" {
			ids = append(ids, job.ID)
		}
	}
	return normalizeTaskStateList(ids, 16)
}

func (rt *runtimeState) recentBackgroundBundleIDs() []string {
	if rt == nil || rt.session == nil {
		return nil
	}
	ids := make([]string, 0, len(rt.session.BackgroundBundles))
	for _, bundle := range rt.session.BackgroundBundles {
		if strings.TrimSpace(bundle.ID) != "" {
			ids = append(ids, bundle.ID)
		}
	}
	return normalizeTaskStateList(ids, 16)
}

func (rt *runtimeState) editableSpecialistNames() []string {
	cfg := Config{}
	if rt != nil {
		cfg = rt.cfg
	}
	names := make([]string, 0)
	for _, profile := range configuredSpecialistProfiles(cfg) {
		if !specialistProfileEditable(profile) {
			continue
		}
		names = append(names, profile.Name)
	}
	return normalizeTaskStateList(names, 16)
}

func (rt *runtimeState) allSpecialistNames() []string {
	cfg := Config{}
	if rt != nil {
		cfg = rt.cfg
	}
	names := make([]string, 0)
	for _, profile := range configuredSpecialistProfiles(cfg) {
		names = append(names, profile.Name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		return strings.Compare(strings.ToLower(names[i]), strings.ToLower(names[j])) < 0
	})
	return normalizeTaskStateList(names, 32)
}

func (rt *runtimeState) hasSpecialistName(name string) bool {
	target := normalizeSpecialistProfileName(name)
	if target == "" {
		return false
	}
	for _, item := range rt.allSpecialistNames() {
		if normalizeSpecialistProfileName(item) == target {
			return true
		}
	}
	return false
}

func (rt *runtimeState) activeSpecialistNames() []string {
	if rt == nil || rt.session == nil {
		return nil
	}
	names := make([]string, 0, len(rt.session.SpecialistWorktrees))
	for _, lease := range rt.session.SpecialistWorktrees {
		if strings.TrimSpace(lease.Specialist) != "" {
			names = append(names, lease.Specialist)
		}
	}
	return normalizeTaskStateList(names, 16)
}

func commandCompletionDescription(item string) string {
	trimmed := strings.TrimSpace(item)
	if !strings.HasPrefix(trimmed, "/") {
		return ""
	}

	switch trimmed {
	case "/fuzz-func <function-name>":
		return "Target one function by name and let Kernforge resolve the best matching symbol automatically."
	case "/fuzz-func <function-name> --file <path>":
		return "Target one function by name and pin matching to a specific source file when names collide."
	case "/fuzz-func <function-name> @<path>":
		return "Target one function by name and use @<path> as a short file-hint alias."
	case "/fuzz-func --file <path>":
		return "Analyze one file plus the files it includes or imports, then let Kernforge choose the best starting function automatically."
	case "/fuzz-func @<path>":
		return "Analyze one file plus the files it includes or imports, then let Kernforge choose the best starting function automatically."
	case "/fuzz-func --from-candidate <id>":
		return "Start /fuzz-func from a persisted /source-scan candidate and link the resulting plan back to that candidate."
	case "/fuzz-func <function-name> --source-scan focused":
		return "Reuse a matching source candidate or run a target-scoped source scan while planning /fuzz-func."
	case "/fuzz-func <function-name> --source-scan full":
		return "Run workspace-wide source matchers during /fuzz-func planning before linking the best matching candidate."
	case "/fuzz-func <function-name> --no-source-scan":
		return "Plan /fuzz-func without source-scan candidate reuse or automatic source matcher execution."
	case "/fuzz-func language":
		return "Show or change /fuzz-func output language. Use system to follow the PC language or english to force English."
	case "/fuzz-campaign":
		return "Show the fuzz campaign planner and the one command Kernforge recommends next, including deduplicated finding gates plus parsed coverage and sanitizer/verifier artifact feedback."
	case "/source-scan":
		return "Run source matchers for kernel, C++, Unreal, and telemetry surfaces, then hand a candidate to /fuzz-func."
	case "/create-driver-poc":
		return "Generate a buildable x64 C++20 MSVC driver POC with a shared communication header and SCM/IOCTL tester."
	case "/create-driver-poc <driver-name>":
		return "Create <driver-name>.sln, Driver.cpp-based <driver-name>.sys, and <driver-name>-tester.exe projects under a new workspace folder."
	}

	fields := strings.Fields(strings.TrimPrefix(trimmed, "/"))
	if len(fields) == 0 {
		return ""
	}

	commandName := strings.ToLower(strings.TrimSpace(fields[0]))
	if commandName == "analyze-project" {
		for index := 1; index+1 < len(fields); index++ {
			if !strings.EqualFold(fields[index], "--mode") {
				continue
			}
			if description := projectAnalysisModeCompletionDescription(fields[index+1]); description != "" {
				return description
			}
		}
	}
	if commandName == "source-scan" {
		if description := sourceScanCompletionDescription(fields[1:]); description != "" {
			return description
		}
	}
	if len(fields) >= 2 {
		rawSubcommand := strings.TrimSpace(fields[1])
		subcommand := strings.ToLower(rawSubcommand)
		if descriptions, ok := slashSubcommandDescriptions[commandName]; ok {
			if description := strings.TrimSpace(descriptions[rawSubcommand]); description != "" {
				return description
			}
			if description := strings.TrimSpace(descriptions[subcommand]); description != "" {
				return description
			}
		}
		if commandName == "set-specialist-model" && !strings.HasPrefix(subcommand, "-") {
			return "Configure the provider and model used by the " + strings.TrimSpace(fields[1]) + " specialist subagent."
		}
	}
	return strings.TrimSpace(slashCommandDescriptions[commandName])
}

func sourceScanCompletionDescription(args []string) string {
	if len(args) == 0 {
		return ""
	}
	subcommand := strings.ToLower(strings.TrimSpace(args[0]))
	switch subcommand {
	case "status":
		return "Show recent source-scan runs, candidate counts, and the best next /fuzz-func handoff."
	case "run":
		for index := 1; index < len(args); index++ {
			option := strings.ToLower(strings.TrimSpace(args[index]))
			switch option {
			case "--limit":
				return "Cap the scan to the top ranked candidates before writing source-scan artifacts."
			case "--only-slugs":
				return "Run only the listed matcher slugs for a focused scan of specific bug-pattern families."
			case "--skip-slugs":
				return "Run the scan while excluding the listed matcher slugs."
			case "--filter":
				return "Scan only files whose path or symbol context matches the filter text."
			case "--files", "--file":
				return "Restrict the scan to the listed comma-separated source files."
			}
		}
		return "Run all enabled source matchers and persist ranked candidate records."
	case "list":
		return "List recent source-scan candidates with ids, matcher slugs, tiers, and /fuzz-func handoff hints."
	case "show":
		return "Show one source-scan candidate by id or latest, including evidence and the exact /fuzz-func handoff."
	case "revalidate":
		return "Attach source-only or native verifier feedback to one candidate and update its lifecycle state."
	}
	return ""
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
