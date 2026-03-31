# Kernforge

![Kernforge banner](./branding/kernforge-release-banner-1280x640.png)
![Kernforge screenshot](./branding/kernforge.png)

`Kernforge` is a terminal-based AI coding CLI written in Go. It is designed for a practical local-first workflow with:

- an interactive REPL
- one-shot prompt mode
- file/search/patch/shell/git tools
- sessions with resume and transcript export
- project memory files and cross-session persistent memory
- local `SKILL.md` skills
- stdio-based MCP servers
- a separate text viewer and diff preview windows on Windows
- automatic verification, checkpoints, and rollback
- a plan-review workflow with a dedicated reviewer model

It is especially friendly to Windows development environments, while keeping the core architecture portable.

## Highlights

- Providers: `ollama`, `anthropic`, `openai`, `openrouter`
- Additional alias: `openai-compatible`
- Input modes:
  - interactive REPL
  - one-shot mode with `-prompt`
  - image attachments with `-image`, `-i`, or `@image.png`
  - file and line-range mentions like `@main.go` or `@main.go:120-150`
  - MCP resource mentions like `@mcp:docs:getting-started`
- Editing workflow:
  - automatic code scouting when no explicit file mention is provided
  - diff preview before applying edits
  - automatic verification after edits
  - workspace checkpoint / rollback
  - selection-first review and edit flow from `/open`
- Interactive ergonomics:
  - `Up` / `Down` history recall in the Windows console
  - `Tab` completion for slash commands, paths, mentions, and MCP targets
  - `Esc` to cancel the current input or in-flight request
- Persistence:
  - saved sessions
  - recent provider/model profiles
  - persistent memory with importance and trust metadata
  - Markdown transcript export including verification and selections
- Extensibility:
  - local skills via `SKILL.md`
  - MCP tools, resources, and prompts
- Planning:
  - plan-review workflow using a planner model and a separately configured reviewer model

## Quick Start

### Build

```powershell
go build -o kernforge.exe .
```

### Run

```powershell
.\kernforge.exe
```

On the first run, if no provider/model is configured, Kernforge can:

1. try to detect a local Ollama server
2. ask whether you want to connect to it
3. otherwise ask you to choose a provider
4. collect the model, API key, and base URL
5. save the result for future runs

### One-shot Prompt Mode

```powershell
.\kernforge.exe -prompt "Explain the structure of this project"
```

With one image:

```powershell
.\kernforge.exe -prompt "Explain the cause of the error in this screenshot" -image .\screenshot.png
```

With multiple images:

```powershell
.\kernforge.exe -prompt "Compare these screenshots" -image .\before.png,.\after.png
```

### Run With a Specific Provider and Model

Anthropic:

```powershell
$env:ANTHROPIC_API_KEY = "your_key"
.\kernforge.exe -provider anthropic -model claude-sonnet-4
```

OpenAI:

```powershell
$env:OPENAI_API_KEY = "your_key"
.\kernforge.exe -provider openai -model gpt-4.1
```

OpenRouter:

```powershell
$env:OPENROUTER_API_KEY = "your_key"
.\kernforge.exe -provider openrouter -model openrouter/auto
```

Ollama:

```powershell
.\kernforge.exe -provider ollama -base-url http://localhost:11434 -model qwen3.5:14b
```

OpenAI-compatible:

```powershell
$env:OPENAI_API_KEY = "your_key"
.\kernforge.exe -provider openai-compatible -base-url http://localhost:8000/v1 -model my-model
```

## Command-Line Options

| Option | Description |
| --- | --- |
| `-cwd <dir>` | Set the starting workspace root |
| `-provider <name>` | Select the provider |
| `-model <name>` | Select the model |
| `-base-url <url>` | Override the provider base URL |
| `-prompt "<text>"` | Run a single prompt and exit |
| `-image <paths>` / `-i` | Attach one or more images in one-shot mode, comma-separated |
| `-resume <session-id>` | Resume a saved session |
| `-permission-mode <mode>` | Set the permission mode |
| `-y` | Auto-approve all permissions (`bypassPermissions`) |

Notes:

- `-image` only works with `-prompt`.
- In interactive mode, you can also attach images with `@path/to/image.png`.

## Workspace Root vs Working Directory

Kernforge tracks:

- the workspace root
- the current working directory inside the REPL

The workspace root is set from `-cwd` or the process working directory at startup. File tools stay within that root.

Inside the REPL, `!cd` changes the current working directory but not the workspace boundary.

## Supported Providers

### Ollama

- Default base URL: `http://localhost:11434`
- Reads `OLLAMA_HOST` and `OLLAMA_API_KEY`
- Supports first-run local server detection
- Fetches model lists directly from the server

### Anthropic

- Default base URL: `https://api.anthropic.com`
- Reads `ANTHROPIC_API_KEY`

### OpenAI

- Default base URL: `https://api.openai.com`
- Reads `OPENAI_API_KEY`

### OpenRouter

- Default base URL: `https://openrouter.ai/api/v1`
- Reads `OPENROUTER_API_KEY`
- Interactive picker supports paging, filtering, curated models, reasoning-only filtering, and sorting by recommended/price/context

## Configuration

### Global Config Locations

Windows:

- `~/.kernforge/config.json`

macOS/Linux:

- `~/.kernforge/config.json`

### Workspace Config Location

- `.kernforge/config.json`

### Merge Order

Later sources override earlier ones:

1. global config
2. workspace config
3. environment variables
4. command-line flags

### Example

```json
{
  "provider": "ollama",
  "model": "qwen3.5:14b",
  "base_url": "http://localhost:11434",
  "permission_mode": "default",
  "shell": "powershell",
  "max_tool_iterations": 16,
  "auto_compact_chars": 45000,
  "auto_checkpoint_edits": true,
  "auto_verify_docs_only": false,
  "auto_locale": true
}
```

### Important Config Fields

| Field | Description |
| --- | --- |
| `provider` | `ollama`, `anthropic`, `openai`, `openrouter`, `openai-compatible` |
| `model` | Model name sent to the provider |
| `base_url` | Provider API base URL |
| `api_key` | API key |
| `temperature` | Model temperature |
| `max_tokens` | Max tokens for completion |
| `max_tool_iterations` | Max tool-call loop count per request |
| `permission_mode` | `default`, `acceptEdits`, `plan`, `bypassPermissions` |
| `shell` | Shell used by `run_shell` |
| `session_dir` | Directory for saved session JSON files |
| `auto_compact_chars` | Approximate context threshold before auto-compacting |
| `auto_checkpoint_edits` | Create one safety checkpoint before the first edit in a request |
| `auto_verify_docs_only` | If `false`, auto verification ignores docs-only changes; if `true`, docs-only changes can still trigger verification |
| `auto_locale` | Automatically instruct the model to answer in the detected system locale |
| `memory_files` | Extra memory file paths |
| `skill_paths` | Extra skill search paths |
| `enabled_skills` | Skills always injected into the system prompt |
| `mcp_servers` | MCP server definitions |
| `profiles` | Saved recent/pinned provider profiles |
| `plan_review` | Dedicated reviewer model config for `/do-plan-review` |
| `review_profiles` | Saved reviewer profiles for plan review |

### Environment Variables

General overrides:

- `KERNFORGE_PROVIDER`
- `KERNFORGE_MODEL`
- `KERNFORGE_BASE_URL`
- `KERNFORGE_API_KEY`
- `KERNFORGE_PERMISSION_MODE`
- `KERNFORGE_SHELL`
- `KERNFORGE_SESSION_DIR`
- `KERNFORGE_AUTO_CHECKPOINT_EDITS`
- `KERNFORGE_AUTO_LOCALE`

Provider-specific:

- `ANTHROPIC_API_KEY`
- `OPENAI_API_KEY`
- `OPENROUTER_API_KEY`
- `OLLAMA_HOST`
- `OLLAMA_API_KEY`

## Memory

### Memory Files

Memory files are injected into the system prompt as project guidance.

Automatic search locations:

- Global:
  - `~/.kernforge/MEMORY.md`
- Workspace ancestry:
  - `.kernforge/KERNFORGE.md`
  - `KERNFORGE.md`

Create a starter workspace memory file:

```text
/init
```

### Persistent Memory

Kernforge includes persistent memory across sessions. It captures compressed turn summaries and can re-inject relevant historical context in future sessions.

Memory metadata includes:

- citation id
- date
- session name or id
- provider/model when available
- importance: `low`, `medium`, `high`
- trust: `tentative`, `confirmed`

Useful commands:

```text
/mem
/mem-search <query>
/mem-show <id>
/mem-promote <id>
/mem-demote <id>
/mem-confirm <id>
/mem-tentative <id>
/mem-dashboard [query]
/mem-dashboard-html [query]
/mem-prune [all]
/mem-stats
```

### Memory Retention Policy

Generate a workspace policy with:

```text
/init memory-policy
```

Policy file:

- `.kernforge/memory-policy.json`

## Skills And MCP

### Skills

Create a starter skill:

```text
/init skill checks
```

Skill discovery paths include global skill directories, workspace `.kernforge/skills`, and `skills`.

Useful commands:

```text
/skills
/reload
```

Use `$checks` in a prompt to activate a skill for the current request.

### MCP

Kernforge supports stdio-based MCP servers and exposes their tools, resources, and prompts inside the CLI.

Useful commands:

```text
/mcp
/resources
/resource <server:uri-or-name>
/prompts
/prompt <server:name> {"arg":"value"}
```

Mention syntax:

```text
@mcp:docs:getting-started summarize this resource
```

## Interactive REPL

### Basic Usage

```text
Explain the structure of this repository
```

### Multiline Input

End a line with `\` to continue:

```text
Find the auth-related flow and \
summarize the key files
```

### Canceling And History

- `Esc` while typing: cancel current input
- `Esc` during a request: cancel the in-flight model request
- `Up` / `Down` in the Windows console: recall recent inputs

### Tab Completion

`Tab` completion supports:

- slash commands
- `@file` mentions
- `/open <path>`
- `/resource <server:...>`
- `/prompt <server:...>`
- `@mcp:server:...`

## Viewer, Selection, And Review Workflow

Open a file in the separate text viewer:

```text
/open main.go
```

Viewer features:

- line numbers
- themed header and live footer
- text selection
- automatic prompt prefill from the selected line range
- persisted selection stack

Selection commands:

```text
/selection
/selections
/use-selection <n>
/drop-selection <n>
/clear-selection
/clear-selections
/note-selection <text>
/tag-selection <tag[,tag2,...]>
/diff-selection
/review-selection [...]
/review-selections [...]
/edit-selection <task>
```

## Shell Commands

Run shell commands with `!`:

```text
!git status
!go test ./...
```

Built-in shortcuts:

```text
!cd src
!ls
!dir
!pwd
!cls
!clear
```

## Permission Modes

| Mode | Meaning |
| --- | --- |
| `default` | reads auto-allowed, writes and shell require confirmation |
| `acceptEdits` | reads and writes auto-allowed, shell requires confirmation |
| `plan` | read-only mode |
| `bypassPermissions` | everything auto-approved |

Change it in the REPL:

```text
/permissions default
/permissions acceptEdits
/permissions plan
/permissions bypassPermissions
```

## Verification, Checkpoints, And Rollback

After successful edits, Kernforge can run automatic verification.

Supported verification detection includes:

- Go: targeted `go test` plus `go vet ./...`
- Cargo: `cargo check`, `cargo test`
- Node: `npm run typecheck`, `npm run lint`, `npm test`
- CMake: `cmake --build <dir>` and optionally `ctest --test-dir <dir>`
- Visual Studio C++: `msbuild <solution-or-project> /m`

Useful commands:

```text
/verify [path,...|--full]
/verify-dashboard [all]
/verify-dashboard-html [all]
/checkpoint [name]
/checkpoint-auto [on|off]
/checkpoint-diff [target] [-- path[,path2]]
/checkpoints
/rollback [target]
```

Generate a workspace verification policy:

```text
/init verify
```

## Sessions, Profiles, And Planning

Session commands:

```text
/session
/sessions
/resume <session-id>
/rename <name>
/export [file]
/tasks
```

Provider profile commands:

```text
/provider
/provider status
/profile
/model <name>
```

Plan-review workflow:

```text
/set-plan-review [provider]
/set-plan-review status
/profile-review
/do-plan-review <task>
```

This lets you keep a separate reviewer model configuration, save reviewer profiles, and run an iterative plan-review loop before execution.

## Slash Commands

```text
/help
/status
/config
/context
/reload
/version
/provider
/profile
/model <name>
/permissions <mode>
/verify [path,...|--full]
/verify-dashboard [all]
/verify-dashboard-html [all]
/clear
/reset
/new
/compact [focus]
/memory
/mem
/mem-search <query>
/mem-show <id>
/mem-promote <id>
/mem-demote <id>
/mem-confirm <id>
/mem-tentative <id>
/mem-dashboard [query]
/mem-dashboard-html [query]
/mem-prune [all]
/mem-stats
/checkpoint [name]
/checkpoint-auto [on|off]
/checkpoint-diff [target] [-- path[,path2]]
/locale-auto [on|off]
/checkpoints
/rollback [target]
/skills
/mcp
/resources
/resource <server:uri-or-name>
/prompts
/prompt <server:name> {"arg":"value"}
/init
/init config
/init verify
/init memory-policy
/init skill <name>
/open <path>
/selection
/selections
/use-selection <n>
/drop-selection <n>
/note-selection <text>
/tag-selection <tag[,tag2,...]>
/clear-selection
/clear-selections
/diff-selection
/review-selection [...]
/review-selections [...]
/edit-selection <task>
/session
/sessions
/resume <session-id>
/rename <name>
/tasks
/diff
/export [file]
/set-plan-review [provider]
/do-plan-review <task>
/profile-review
/exit
/quit
```

## Troubleshooting

### `provider/model are not configured`

- run the CLI interactively once
- or pass `-provider` and `-model`
- or store them in config

### `-image requires -prompt`

`-image` only works in one-shot mode. In interactive mode, use:

```text
@screenshot.png explain this image
```

### Config, Skills, Or MCP Changes Are Not Reflected

```text
/reload
```

### `no Ollama models were returned`

Check:

- whether the Ollama server is running
- whether the base URL is correct
- whether the model has been pulled locally

## Related Docs

- MCP and skills quick guide: [MCP-SKILLS.md](./MCP-SKILLS.md)

## Current Scope Summary

- Go 1.21 single-binary CLI
- interactive REPL and one-shot prompt mode
- Anthropic / OpenAI / OpenRouter / Ollama / OpenAI-compatible support
- image input support
- file, image, line-range, and MCP mentions
- automatic code scouting
- text viewer with persisted selections
- selection-first review and edit workflow
- session save / resume / export
- cross-session persistent memory with dashboards and retention policy
- workspace checkpoint / rollback
- provider profiles and reviewer profiles
- local skills
- stdio MCP tools / resources / prompts
- automatic verification and verification dashboards
- workspace verification policy
- plan-review workflow with a dedicated reviewer model
