package main

import (
	"strings"
)

type CommandVisibility string

const (
	CommandVisibilityPublic CommandVisibility = "public"
	CommandVisibilityHidden CommandVisibility = "hidden"
)

type CommandSpec struct {
	Canonical  string
	Family     string
	Visibility CommandVisibility
	HelpTopic  string
	Completion string
	Handler    string
	MCPMapping []string
}

// Everyday commands advertised at the top of default /help.
var commandLayerEveryday = []string{
	"help",
	"status",
	"clear",
	"exit",
	"model",
	"provider",
	"permissions",
	"review",
	"verify",
	"gate",
	"diff",
	"config",
}

// Hub commands advertised under Everyday in default /help.
var commandLayerHub = []string{
	"session",
	"memory",
	"selection",
	"analyze",
	"probe",
	"mcp",
	"hooks",
	"settings",
	"checkpoint",
	"goal",
	"automation",
	"suggest",
	"worktree",
	"init",
	"specialists",
	"profile",
	"codex-auth",
}

func advertisedSlashCommands() []string {
	out := make([]string, 0, len(commandLayerEveryday)+len(commandLayerHub))
	out = append(out, commandLayerEveryday...)
	out = append(out, commandLayerHub...)
	return out
}

func isAdvertisedSlashCommand(command string) bool {
	command = normalizeSlashCommandName(command)
	for _, name := range advertisedSlashCommands() {
		if name == command {
			return true
		}
	}
	return false
}

func commandSpecs() []CommandSpec {
	specs := make([]CommandSpec, 0, len(slashCommands))
	for _, command := range slashCommands {
		specs = append(specs, CommandSpec{
			Canonical:  command,
			Family:     commandFamily(command),
			Visibility: commandVisibility(command),
			HelpTopic:  commandHelpTopic(command),
			Completion: command,
			Handler:    "handleCommand:" + command,
			MCPMapping: commandMCPMapping(command),
		})
	}
	return specs
}

func commandVisibility(command string) CommandVisibility {
	if isAdvertisedSlashCommand(command) {
		return CommandVisibilityPublic
	}
	return CommandVisibilityHidden
}

func commandFamily(command string) string {
	command = normalizeSlashCommandName(command)
	switch command {
	case "analyze-project", "analyze-dashboard", "analyze-performance", "docs-refresh":
		return "analyze"
	case "fuzz-func", "fuzz-campaign", "source-scan", "create-driver-poc", "find-root-cause", "root-cause-patterns":
		return "probe"
	case "set-auto-verify", "locale-auto", "set-max-tool-iterations", "progress-display":
		return "settings"
	case "hook-reload", "override":
		return "hooks"
	case "resources", "resource", "prompts", "prompt", "skills":
		return "mcp"
	case "open", "selections", "use-selection", "drop-selection", "note-selection", "tag-selection",
		"clear-selection", "clear-selections", "diff-selection", "edit-selection":
		return "selection"
	case "evidence":
		return "memory"
	case "codex-login":
		return "codex-auth"
	case "finish", "retry-verify", "continue":
		return "review"
	}
	if idx := strings.Index(command, "-"); idx > 0 {
		return command[:idx]
	}
	return command
}

func commandHelpTopic(command string) string {
	switch normalizeSlashCommandName(command) {
	case "review", "finish", "retry-verify", "continue", "review-soak":
		return "review"
	case "verify", "checkpoint":
		return "verification"
	case "probe", "fuzz-func", "fuzz-campaign", "source-scan", "create-driver-poc", "find-root-cause", "root-cause-patterns":
		return "probe"
	case "memory", "evidence":
		return "memory"
	case "analyze", "analyze-project", "analyze-dashboard", "analyze-performance", "docs-refresh":
		return "analyze"
	case "settings", "set-auto-verify", "locale-auto", "set-max-tool-iterations", "progress-display":
		return "settings"
	case "hooks", "hook-reload", "override":
		return "hooks"
	case "open", "selection", "selections", "use-selection", "drop-selection", "note-selection", "tag-selection",
		"clear-selection", "clear-selections", "diff-selection", "edit-selection":
		return "selection"
	case "mcp", "resources", "resource", "prompts", "prompt", "skills":
		return "mcp"
	case "init", "worktree":
		return "workspace"
	case "codex-login":
		return "codex-auth"
	default:
		return normalizeSlashCommandName(command)
	}
}

func commandMCPMapping(command string) []string {
	switch normalizeSlashCommandName(command) {
	case "review":
		return []string{"kernforge_review"}
	case "verify":
		return []string{"kernforge_verify"}
	case "analyze", "analyze-project":
		return []string{"kernforge_analyze_project"}
	case "find-root-cause", "probe":
		return []string{"kernforge_find_root_cause"}
	case "source-scan":
		return []string{"kernforge_source_scan"}
	case "fuzz-func":
		return []string{"kernforge_fuzz", "kernforge_fuzz_func", "kernforge_fuzz_func_preview", "kernforge_fuzz_func_build"}
	case "fuzz-campaign":
		return []string{"kernforge_fuzz_campaign_status", "kernforge_fuzz_campaign_run"}
	case "memory":
		return []string{"kernforge_memory_search"}
	case "evidence":
		return []string{"kernforge_evidence_search"}
	case "status":
		return []string{"kernforge_status"}
	default:
		return nil
	}
}

func joinCommandArgs(prefix string, args string) string {
	prefix = strings.TrimSpace(prefix)
	args = strings.TrimSpace(args)
	if prefix == "" {
		return args
	}
	if args == "" {
		return prefix
	}
	return prefix + " " + args
}

// completionSlashCommandMatches returns Tab-completion candidates for a typed
// slash prefix. With an empty prefix, only Everyday+Hub names are offered so
// the default list stays short. Once the user types a prefix, every matching
// command is offered (advertised names first) so aliases like /reload remain
// reachable even when they share a prefix with /review.
func completionSlashCommandMatches(partial string, all []string) []string {
	partial = normalizeSlashCommandName(partial)
	advertised := make(map[string]bool, len(commandLayerEveryday)+len(commandLayerHub))
	for _, name := range advertisedSlashCommands() {
		advertised[name] = true
	}

	var publicMatches []string
	var hiddenMatches []string
	for _, cmd := range all {
		if !strings.HasPrefix(cmd, partial) {
			continue
		}
		if advertised[cmd] {
			publicMatches = append(publicMatches, cmd)
		} else {
			hiddenMatches = append(hiddenMatches, cmd)
		}
	}
	if partial == "" {
		return publicMatches
	}
	out := make([]string, 0, len(publicMatches)+len(hiddenMatches))
	out = append(out, publicMatches...)
	out = append(out, hiddenMatches...)
	return out
}
