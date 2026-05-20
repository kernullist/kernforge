package main

import "strings"

func kernforgeCLIHelpRequest(args []string) (bool, string) {
	if len(args) == 0 {
		return false, ""
	}
	for _, arg := range args {
		if isKernforgeHelpFlag(arg) {
			return true, inferKernforgeHelpTopic(args)
		}
	}
	positionals := kernforgeHelpPositionals(args)
	if len(positionals) == 0 {
		return false, ""
	}
	if isKernforgeHelpToken(positionals[0]) {
		return true, firstKernforgeHelpTopic(positionals[1:])
	}
	if strings.EqualFold(strings.TrimSpace(positionals[0]), "daemon") {
		if len(positionals) > 1 && isKernforgeHelpToken(positionals[1]) {
			return true, "daemon"
		}
	}
	return false, ""
}

func kernforgeCLIVersionRequest(args []string) bool {
	positionals := kernforgeHelpPositionals(args)
	if len(positionals) > 0 && strings.EqualFold(strings.TrimSpace(positionals[0]), "version") {
		return true
	}
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if kernforgeFlagConsumesHelpValue(arg) {
			if !strings.Contains(arg, "=") {
				i++
			}
			continue
		}
		switch strings.TrimSpace(strings.ToLower(arg)) {
		case "--version", "-version":
			return true
		}
	}
	return false
}

func isKernforgeHelpToken(value string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	return trimmed == "help" || isKernforgeHelpFlag(trimmed)
}

func isKernforgeHelpFlag(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "-h", "--help", "-help", "/help", "/?", "-?":
		return true
	default:
		return false
	}
}

func firstKernforgeHelpTopic(args []string) string {
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" || isKernforgeHelpToken(trimmed) {
			continue
		}
		if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "/") {
			continue
		}
		if strings.EqualFold(trimmed, "daemon") {
			return "daemon"
		}
		return strings.ToLower(trimmed)
	}
	return ""
}

func inferKernforgeHelpTopic(args []string) string {
	positionals := kernforgeHelpPositionals(args)
	if topic := firstKernforgeHelpTopic(positionals); topic != "" {
		return topic
	}
	for _, arg := range args {
		trimmed := strings.TrimSpace(strings.ToLower(arg))
		switch trimmed {
		case "daemon":
			return "daemon"
		case "-mcp-server", "--mcp-server", "-mcp-daemon-proxy", "--mcp-daemon-proxy":
			return "mcp"
		case "-prompt", "--prompt", "-command", "--command", "-goal", "--goal", "-goal-file", "--goal-file":
			return "standalone"
		}
	}
	return ""
}

func kernforgeHelpPositionals(args []string) []string {
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if isKernforgeHelpFlag(arg) {
			continue
		}
		if kernforgeFlagConsumesHelpValue(arg) {
			if !strings.Contains(arg, "=") {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		positionals = append(positionals, arg)
	}
	return positionals
}

func kernforgeFlagConsumesHelpValue(arg string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(arg))
	trimmed = strings.TrimLeft(trimmed, "-")
	if idx := strings.Index(trimmed, "="); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	switch trimmed {
	case "cwd",
		"provider",
		"model",
		"base-url",
		"image",
		"i",
		"preview-file",
		"preview-result-file",
		"viewer-file",
		"viewer-result-file",
		"prompt",
		"command",
		"goal",
		"goal-file",
		"goal-max-iterations",
		"goal-time-budget",
		"goal-token-budget",
		"resume",
		"permission-mode":
		return true
	default:
		return false
	}
}

func renderKernforgeCLIHelp(topic string) string {
	switch strings.ToLower(strings.TrimSpace(topic)) {
	case "mcp", "mcp-server", "server":
		return kernforgeMCPHelpText()
	case "daemon", "proxy":
		return kernforgeDaemonHelpText()
	case "prompt", "command", "standalone", "cli", "repl":
		return kernforgeStandaloneHelpText()
	default:
		return kernforgeGeneralHelpText()
	}
}

func kernforgeGeneralHelpText() string {
	return strings.TrimSpace(`
Kernforge
Version: `+currentVersion()+`

Usage:
  kernforge [options]
      Start the standalone interactive REPL in the current workspace.

  kernforge -prompt "<task>" [options]
      Run one model turn, print the answer, and exit.

  kernforge -command "/status" [options]
      Run one slash command, print the command output, and exit.

  kernforge -goal "<objective>" [options]
  kernforge -goal-file goal.md [options]
      Run an autonomous goal loop with verification and completion checks.

  kernforge -mcp-server [options]
      Run Kernforge as a stdio MCP server for Codex, Claude, or another MCP client.

  kernforge daemon <start|run|status|stop> [options]
      Advanced: manage the local daemon used by optional shared MCP proxy mode.

Common options:
  -cwd <dir>                  Workspace root. Default: current directory.
  -provider <name>            Provider override for this process.
  -model <name>               Model override for this process.
  -base-url <url>             Provider endpoint override.
  -permission-mode <mode>     default | acceptEdits | plan | bypassPermissions.
  -resume <session-id>        Resume an existing session.
  -y                          Use bypass permission mode.
  --version                   Show the Kernforge PE file version and exit.
  -h, --help, help            Show this help.

Standalone examples:
  kernforge
  kernforge -cwd C:\repo\driver
  kernforge -prompt "Review the IOCTL path and suggest tests"
  kernforge -command "/analyze-project --mode security"
  kernforge -goal-file .kernforge\goals\stabilize.md

MCP client setup, recommended:
  kernforge -mcp-server -cwd C:\repo\driver

MCP client config:
  {
    "mcpServers": {
      "kernforge": {
        "command": "kernforge",
        "args": ["-mcp-server", "-cwd", "C:\\repo\\driver"]
      }
    }
  }

Meaning:
  - Your MCP client launches Kernforge with -mcp-server.
  - -cwd selects the workspace Kernforge should operate on.
  - You usually do not need daemon mode.

More help:
  kernforge help standalone
  kernforge help mcp
  kernforge help daemon
  Inside the REPL: /help, /help mcp, /help analyze-project
`) + "\n"
}

func kernforgeStandaloneHelpText() string {
	return strings.TrimSpace(`
Kernforge standalone usage

Interactive REPL:
  kernforge
  kernforge -cwd C:\repo\driver
  kernforge -provider openai-codex -model gpt-5.5

One-shot prompt:
  kernforge -prompt "Review recent changes and run focused verification"
  kernforge -prompt "Explain @src/driver.cpp" -image diagram.png

One-shot slash command:
  kernforge -command "/status"
  kernforge -command "/analyze-project --mode security"
  kernforge -command "/verify"

Autonomous goal:
  kernforge -goal "Fix the failing verification and update docs"
  kernforge -goal-file .kernforge\goals\release.md

Useful options:
  -cwd <dir>
  -provider <name>
  -model <name>
  -base-url <url>
  -permission-mode <default|acceptEdits|plan|bypassPermissions>
  -resume <session-id>
  -y
`) + "\n"
}

func kernforgeMCPHelpText() string {
	return strings.TrimSpace(`
Kernforge MCP server usage

Most users only need this MCP client setup:
  kernforge -mcp-server -cwd C:\repo\driver

  {
    "mcpServers": {
      "kernforge": {
        "command": "kernforge",
        "args": ["-mcp-server", "-cwd", "C:\\repo\\driver"]
      }
    }
  }

What this does:
  - The MCP client starts one Kernforge process for this server entry.
  - -mcp-server makes that process speak MCP over stdin/stdout.
  - -cwd C:\repo\driver tells Kernforge which workspace to use.
  - If your MCP client reuses one Kernforge entry across different repos,
    pass the current repo as the tool argument workspace or let the client
    send initialize.rootUri/workspaceFolders. Kernforge treats -cwd as the
    fallback only when no client workspace hint is available.

Optional provider override:
  kernforge -provider deepseek -model deepseek-chat -mcp-server -cwd C:\repo\driver

Advanced shared daemon mode:
  Use this only when multiple MCP clients should reuse one local Kernforge daemon.

  1. Start the daemon:
     kernforge -cwd C:\repo\driver daemon start

  2. Configure each MCP client to launch a small stdio proxy:
  {
    "mcpServers": {
      "kernforge": {
        "command": "kernforge",
        "args": ["-mcp-server", "-mcp-daemon-proxy", "-cwd", "C:\\repo\\driver"]
      }
    }
  }

  In this mode, the proxy still speaks MCP over stdin/stdout to the client,
  but forwards requests to the already-running daemon. The -cwd value is the
  fallback workspace when the client does not send a workspace hint.
`) + "\n"
}

func kernforgeDaemonHelpText() string {
	return strings.TrimSpace(`
Kernforge daemon usage

Commands:
  kernforge daemon start
      Start a background daemon for MCP proxy clients.

  kernforge daemon run
      Run the daemon in the foreground. This is normally launched by daemon start.

  kernforge daemon status
      Show daemon health and state.

  kernforge daemon stop
      Stop the running daemon.

Typical MCP proxy flow:
  kernforge -cwd C:\repo\driver daemon start
  kernforge -mcp-server -mcp-daemon-proxy -cwd C:\repo\driver

Most users do not need this. For normal MCP client setup, use:
  kernforge -mcp-server -cwd C:\repo\driver

The second command is the MCP client entrypoint. It speaks MCP over stdin/stdout,
then forwards requests to the already-running local daemon. The -cwd value is
the fallback workspace for clients that do not include a workspace hint.

The daemon keeps per-workspace MCP runtimes and lets proxy clients route requests by workspace hints when the MCP client provides them.
`) + "\n"
}
