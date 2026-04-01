# Kernforge

![Kernforge banner](./branding/kernforge-release-banner-1280x640.png)
![Kernforge demo](./branding/kernforge_demo.gif)

`Kernforge` is a terminal-first AI coding CLI written in Go. It is built around a practical local workflow with strong Windows support, while keeping the core runtime portable.

## What It Currently Supports

- Interactive REPL and one-shot `-prompt` mode
- Providers: `ollama`, `anthropic`, `openai`, `openrouter`, `openai-compatible`
- File, patch, shell, and git-oriented tool use
- Local file mentions, image mentions, and MCP resource mentions
- Session persistence, resume, rename, clear, compact, and Markdown export
- Project memory files plus cross-session persistent memory with trust/importance metadata
- Local `SKILL.md` skills with discovery and per-request activation
- Stdio MCP servers with tools, resources, and prompts
- Windows viewer and diff-preview windows for selection-first workflows
- Adaptive verification, verification history dashboards, checkpoints, and rollback
- Plan-review workflow with a separate reviewer model

## Highlights

### Input And Prompting

- Interactive chat REPL
- One-shot prompt mode with `-prompt`
- Image attachments with `-image`, `-i`, or `@path/to/image.png`
- File mentions like `@main.go`
- Line-range mentions like `@main.go:120-150`
- MCP mentions like `@mcp:docs:getting-started`
- Multiline input by ending a line with `\`
- Automatic code scouting when no explicit file mention is provided

### Editing Workflow

- Diff preview before file writes
- Selection-aware edit previews
- Automatic verification after edits when applicable
- Automatic checkpoint creation before the first edit in a request
- Manual checkpoints, checkpoint diff, and rollback
- Selection-first edit and review flow through `/open`

### Interactive Ergonomics

- `Tab` completion for commands, paths, mentions, MCP targets, and `/open`
- `Esc` to cancel current input
- `Esc` to cancel an in-flight request
- Windows console input history with `Up` and `Down`

### Persistence And Context

- Saved sessions with `/resume`
- Session rename and transcript export
- Persistent memory with citation ids, trust, importance, and search
- Project guidance files loaded from `KERNFORGE.md` and `.kernforge/KERNFORGE.md`
- Auto locale injection based on system locale

### Extensibility

- Local skills from `SKILL.md`
- MCP tools
- MCP resources
- MCP prompts

## Quick Start

### Build

```powershell
go build -o kernforge.exe .
```

### Run

```powershell
.\kernforge.exe
```

If no provider/model is configured yet, Kernforge can:

1. Try to detect a local Ollama server.
2. Offer to use it if found.
3. Otherwise walk through provider setup.
4. Save the chosen provider, model, API key, and base URL for future runs.

### One-Shot Prompt Mode

```powershell
.\kernforge.exe -prompt "Explain the structure of this project"
```

With one image:

```powershell
.\kernforge.exe -prompt "Explain the error in this screenshot" -image .\screenshot.png
```

With multiple images:

```powershell
.\kernforge.exe -prompt "Compare these screenshots" -image .\before.png,.\after.png
```

### Run With A Specific Provider

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

- `-image` requires `-prompt`.
- `-preview-file`, `-preview-result-file`, `-viewer-file`, and `-viewer-result-file` are internal window helper flags.

## Workspace And Configuration

### Workspace Root vs Current Directory

Kernforge tracks:

- The workspace root
- The current working directory inside the REPL

The workspace root is set from `-cwd` or the process working directory at startup. File tools stay within that root.

Inside the REPL, `!cd` changes the current working directory, but it does not expand the workspace boundary.

### Config Locations

- Global config: `~/.kernforge/config.json`
- Workspace config: `.kernforge/config.json`

### Merge Order

Later sources override earlier ones:

1. Global config
2. Workspace config
3. Environment variables
4. Command-line flags

### Example Config

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
| `max_tokens` | Max completion tokens |
| `max_tool_iterations` | Max tool loop count per request |
| `permission_mode` | `default`, `acceptEdits`, `plan`, `bypassPermissions` |
| `shell` | Shell used by `run_shell` |
| `session_dir` | Directory for saved session JSON files |
| `auto_compact_chars` | Approximate context threshold before auto-compacting |
| `auto_checkpoint_edits` | Create a safety checkpoint before the first edit in a request |
| `auto_verify_docs_only` | Allow docs-only changes to still trigger auto verification |
| `auto_locale` | Inject the detected system locale into prompts |
| `memory_files` | Extra memory file paths |
| `skill_paths` | Extra skill search paths |
| `enabled_skills` | Skills always injected into prompts |
| `mcp_servers` | MCP server definitions |
| `profiles` | Saved recent or pinned provider/model profiles |
| `plan_review` | Reviewer model config used by `/do-plan-review` |
| `review_profiles` | Saved reviewer profiles |

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

## Providers

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
- Interactive picker supports paging, filtering, curated recommendations, reasoning-only filtering, and sorting

### OpenAI-compatible

- Uses OpenAI-style chat completions
- Reads `OPENAI_API_KEY` unless overridden by config/env
- Requires an explicit `base_url`

## Memory

### Memory Files

Memory files are injected into the system prompt as project guidance.

Automatic search locations:

- Global: `~/.kernforge/MEMORY.md`
- Workspace ancestry: `.kernforge/KERNFORGE.md`
- Workspace ancestry: `KERNFORGE.md`

Starter commands:

```text
/init
/init memory-policy
```

### Persistent Memory

Kernforge stores cross-session compressed memories and can re-inject relevant context in future sessions.

Memory metadata includes:

- Citation id
- Date
- Session name or id
- Provider and model when available
- Importance: `low`, `medium`, `high`
- Trust: `tentative`, `confirmed`

Useful commands:

```text
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
```

## Skills And MCP

### Skills

Create a starter skill:

```text
/init skill checks
```

Useful commands:

```text
/skills
/reload
```

Use `$checks` in a prompt to activate a skill for the current request.

### MCP

Kernforge supports stdio-based MCP servers and exposes their tools, resources, and prompts in the CLI.

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

### Useful Runtime Commands

```text
/config
/context
/status
/version
/help
/reload
```

### Conversation And Session Commands

```text
/clear
/compact [focus]
/export [file]
/rename <name>
/resume <session-id>
/session
/sessions
/tasks
```

### Provider And Planning Commands

```text
/provider
/model [name]
/profile
/profile-review
/set-plan-review [provider]
/do-plan-review <task>
/permissions [mode]
/locale-auto [on|off]
```

### Canceling And History

- `Esc` while typing: cancel current input
- `Esc` during a request: cancel the in-flight model request
- `Up` / `Down` in the Windows console: recall recent inputs

### Tab Completion

`Tab` completion supports:

- Slash commands
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

Viewer and selection workflow features:

- Separate viewer window on Windows
- Line numbers and footer status
- Text selection
- Prompt prefill from selected lines
- Saved selection stack
- Review-only and edit-only prompts scoped to the selection

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

## Shell And Git Commands

Run shell commands with `!`:

```text
!git status
!go test ./...
```

Built-in shell shortcuts:

```text
!cd src
!ls
!dir
!pwd
!cls
!clear
```

Git command:

```text
/diff
```

## Permission Modes

| Mode | Meaning |
| --- | --- |
| `default` | Reads auto-allowed, writes and shell require confirmation |
| `acceptEdits` | Reads and writes auto-allowed, shell requires confirmation |
| `plan` | Read-only mode |
| `bypassPermissions` | Everything auto-approved |

Change it in the REPL:

```text
/permissions default
/permissions acceptEdits
/permissions plan
/permissions bypassPermissions
```

## Verification, Checkpoints, And Rollback

After successful edits, Kernforge can run automatic verification.

Implemented verification detection includes:

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
/checkpoint [note]
/checkpoint-auto [on|off]
/checkpoint-diff [target] [-- path[,path2]]
/checkpoints
/rollback [target]
/init verify
```

## Notes

- The viewer and diff preview windows are primarily implemented for Windows.
- The CLI core, sessions, providers, memory, skills, MCP support, and verification logic are designed to remain portable where possible.
