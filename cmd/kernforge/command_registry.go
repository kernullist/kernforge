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

func commandSpecs() []CommandSpec {
	specs := make([]CommandSpec, 0, len(slashCommands))
	for _, command := range slashCommands {
		specs = append(specs, CommandSpec{
			Canonical:  command,
			Family:     commandFamily(command),
			Visibility: CommandVisibilityPublic,
			HelpTopic:  commandHelpTopic(command),
			Completion: command,
			Handler:    "handleCommand:" + command,
			MCPMapping: commandMCPMapping(command),
		})
	}
	return specs
}

func commandFamily(command string) string {
	if idx := strings.Index(command, "-"); idx > 0 {
		return command[:idx]
	}
	return command
}

func commandHelpTopic(command string) string {
	switch command {
	case "review", "finish", "retry-verify", "continue":
		return "review"
	case "verify", "checkpoint", "fuzz-func", "fuzz-campaign", "source-scan", "create-driver-poc":
		return "verification"
	case "memory", "evidence":
		return "memory"
	case "open", "selection", "selections", "use-selection", "drop-selection", "note-selection", "tag-selection", "clear-selection", "clear-selections", "diff-selection", "edit-selection":
		return "selection"
	case "mcp", "resources", "resource", "prompts", "prompt", "skills":
		return "mcp"
	case "init", "worktree", "locale-auto":
		return "workspace"
	default:
		return command
	}
}

func commandMCPMapping(command string) []string {
	switch command {
	case "review":
		return []string{"kernforge_review"}
	case "verify":
		return []string{"kernforge_verify"}
	case "analyze-project":
		return []string{"kernforge_analyze_project"}
	case "find-root-cause":
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
