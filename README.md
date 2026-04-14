# Kernforge

![Kernforge banner](./branding/kernforge-release-banner-1280x640.png)
![Kernforge demo](./branding/kernforge_demo.gif)

`Kernforge` is a terminal-first AI coding CLI written in Go. It is built around a practical local workflow with strong Windows support, and is especially tuned for Windows security, anti-cheat, telemetry, driver-oriented workflows, and large project analysis.

Its strongest current value is a `multi-agent project analysis pipeline` that turns a large workspace into a reusable knowledge pack, then carries that context into editing, verification, evidence, and policy.  
Kernforge is now centered on `project analysis -> performance lens -> adaptive verification -> evidence store -> persistent memory -> hook policy -> checkpoint/rollback`, which makes it especially useful for driver, telemetry, memory-scan, and Unreal security workflows.

## Flagship Capability

If Kernforge has one feature to understand first, it is `multi-agent project analysis`.

- `/analyze-project [--mode map|trace|impact|security|performance] <goal>` builds a reusable architecture map instead of a disposable summary
- The output becomes a durable knowledge pack, performance lens, structural index, and vector-ready analysis set
- That analysis is then reused in review, editing, verification, and policy workflows

## Documentation

Quick Start:
- [English Quickstart](./QUICKSTART.md)
- [한국어 빠른 시작](./QUICKSTART_kor.md)

Guides:
- [English Feature Usage Guide](./FEATURE_USAGE_GUIDE.md)
- [한국어 기능 활용 가이드](./FEATURE_USAGE_GUIDE_kor.md)

Playbooks:
- [Driver Playbook](./PLAYBOOK_driver.md)
- [Telemetry Playbook](./PLAYBOOK_telemetry.md)
- [Memory-Scan Playbook](./PLAYBOOK_memory_scan.md)

Specs And Roadmap:
- [Korean Roadmap](./ROADMAP_kor.md)
- [Korean Hook Engine Spec](./HOOK_ENGINE_SPEC_kor.md)
- [Korean Live Investigation Mode Spec](./LIVE_INVESTIGATION_SPEC_kor.md)
- [Korean Adversarial Simulation Spec](./ADVERSARIAL_SIMULATION_SPEC_kor.md)
- [Korean Next-Gen Project Analysis Spec](./PROJECT_ANALYSIS_NEXT_SPEC_kor.md)

The most practical end-to-end workflow is described in the [English Detailed Usage Guide](./FEATURE_USAGE_GUIDE.md). The highest-value current loop is `investigate -> simulate -> review/edit/plan -> verify -> evidence/memory/hooks`.

## Why Kernforge

Kernforge is especially strong when you need to understand a large, security-sensitive codebase before you change it.

It is a good fit for:

1. Driver, signing, symbol, and package readiness work
2. Telemetry, provider, and manifest compatibility work
3. Memory-scan and Unreal integrity work that needs both architecture understanding and practical guardrails

Its current differentiators are:

1. It can analyze a large workspace with a conductor plus multiple worker and reviewer passes.
2. It produces a reusable knowledge pack and a derived performance lens instead of a disposable one-shot summary.
3. It carries that analysis forward into review, edit, verification, and investigation workflows.
4. It stores verification output as structured evidence and long-lived memory.
5. It feeds that history back into hook policy, push and PR decisions, and safety checkpoints.

## What It Currently Supports

- Multi-agent project analysis with reusable knowledge packs and a performance lens
- Interactive REPL and one-shot `-prompt` mode
- Providers: `ollama`, `anthropic`, `openai`, `openrouter`, `openai-compatible`
- File, patch, shell, and git-oriented tool use
- Git staging, commit, push, and GitHub pull request creation through dedicated tools
- Local file mentions, image mentions, and MCP resource mentions
- Session persistence, resume, rename, clear, compact, and Markdown export
- Project memory files plus cross-session persistent memory with trust/importance metadata
- Evidence store, evidence search, and evidence dashboards
- Local `SKILL.md` skills with discovery and per-request activation
- Stdio MCP servers with tools, resources, and prompts
- Windows text viewer plus WebView2-based diff review and diff viewing for selection-first workflows
- Adaptive verification, verification history dashboards, checkpoints, and rollback
- Hook engine, workspace hook rules, and evidence-aware push/PR policy
- Plan-review workflow with a separate reviewer model
- Tracked feature workflow with persisted spec, plan, tasks, and implementation artifacts under `.kernforge/features`

## Highlights

### Project Analysis

- `/analyze-project [--mode map|trace|impact|security|performance] <goal>` runs a conductor plus multiple sub-agents and writes a project document
- If you omit `--mode`, the default mode is `map`
- In `security` mode, the analysis now decomposes results into dedicated `driver`, `IOCTL`, `handle`, `memory`, and `RPC` surfaces when those paths are present
- Incremental shard reuse avoids re-analyzing unchanged areas when possible
- Goal text can narrow analysis to matching directories when you explicitly target a sub-area of the workspace
- Interactive runs can flag hidden or external-looking directories and let you exclude them from the analysis pass
- Semantic fingerprint invalidation can force recomputation when structure changes even if file scope looks stable
- Unreal project, module, target, type, network, asset, system, and config signals are lifted into structured analysis artifacts
- A semantic shard planner plus semantic-aware worker and reviewer prompts prioritize startup, network, UI, GAS, asset/config, and integrity surfaces
- In addition to a knowledge pack, the pipeline now emits a structural index, `structural_index_v2`, Unreal semantic graph, vector corpus, and vector ingestion exports
- Security-mode final documents now add a `Security Surface Decomposition` section so privileged and abuse-sensitive paths do not get flattened into a generic summary
- Dedicated worker and reviewer models can be configured separately from the main chat model
- Architecture knowledge packs and performance lenses are written under `.kernforge/analysis`
- `/analyze-performance [focus]` uses the latest analysis artifacts to reason about hot paths and bottlenecks

### Security Verification And Policy Loop

- Security-aware verification for driver, telemetry, Unreal, and memory-scan changes
- Verification history and verification dashboards
- Structured evidence capture from verification
- Evidence search and evidence dashboards
- Runtime toggle for automatic verification with `/set-auto-verify [on|off]`
- Windows verification tool path detection and overrides with `/detect-verification-tools` and `/set-*-path`
- Hook-based push and PR warnings, confirmations, and blocks based on recent failed evidence
- Automatic safety checkpoint creation for repeated high-risk failure patterns

### Editing Workflow

- WebView2 diff review before file writes
- Selection-aware edit previews
- Automatic verification after edits when applicable
- `read_file` now reuses unchanged exact ranges, covered subranges, and partial overlaps so large-file edit loops avoid redundant rereads
- `grep` now annotates matches with `[cached-nearby:inside]` or `[cached-nearby:N]` when recent `read_file` context already covers the same area or a nearby span
- Repeated same-file `read_file` turns now prefer cache-aware nudges before falling back to hard repeated-tool aborts
- `a` on `Allow write?` enables write auto-approval for the session only
- `a` on `Open diff preview?` auto-accepts the current edit and future diff previews for the session
- Git-mutating tools such as `git_add`, `git_commit`, `git_push`, and `git_create_pr` use a separate session-scoped `Allow git?` approval path
- Git-mutating tools are intended for explicit user requests, not normal review or edit turns
- Automatic checkpoint creation before the first edit in a request
- Manual checkpoints, checkpoint diff, and rollback
- Selection-first edit and review flow through `/open`

### Tracked Feature Workflow

- `/new-feature <task>` creates a tracked feature workspace and writes `spec.md`, `plan.md`, and `tasks.md`
- Tracked feature artifacts live under `.kernforge/features/<id>` so large work can survive across sessions
- `/new-feature status|plan|implement|close [id]` lets you inspect, regenerate, execute, and finish the active feature
- `/do-plan-review <task>` remains the better fit for one-shot reviewed planning and immediate execution

### Input And Prompting

- Interactive chat REPL
- One-shot prompt mode with `-prompt`
- Image attachments with `-image`, `-i`, or `@path/to/image.png`
- File mentions like `@main.go`
- Line-range mentions like `@main.go:120-150`
- MCP mentions like `@mcp:docs:getting-started`
- Multiline input by ending a line with `\`
- Automatic code scouting when no explicit file mention is provided
- Recent `analyze-project` results can be injected as cached architecture context before Kernforge rereads large areas
- When cached analysis is sufficient for a question, Kernforge can answer directly from that summary without extra tool calls
- Cached `read_file` NOTE results are now treated as a signal that the relevant lines were already seen, which helps reduce large-file reread loops
- `grep` cache-nearby hints help the model choose smaller follow-up `read_file` ranges around fresh unmatched lines instead of rescanning a wide block
- Prompts that ask to analyze, explain, diagnose, or document default to read-only investigation mode
- Prompts that explicitly ask to fix code stay tool-driven and Kernforge nudges the model away from handing patches back to the user

### Interactive Ergonomics

- `Tab` completion for commands, paths, mentions, MCP targets, fixed command arguments, analyze-project modes, and saved ids such as `/resume`, `/mem-show`, `/evidence-show`, `/investigate show`, `/simulate show`, and `/new-feature status|plan|implement|close`
- `Esc` to cancel current input
- `Esc` to cancel an in-flight request
- Assistant streaming output now suppresses leading blank chunks, flushes cleanly before progress lines, and inserts line breaks between repeated follow-on preambles
- Generic waiting text is collapsed so the thinking indicator does not repeat the same message twice
- Repeated blank streamed chunks are replaced with a compact working status instead of emitting empty lines
- Abruptly cut-off final answers are retried once as a continuation and merged before the CLI returns to the prompt
- On Windows consoles, short `Esc` taps are treated as request cancel reliably
- After a request cancel, the next prompt is stabilized so leftover `Esc` input does not auto-cancel it
- Windows console input history with `Up` and `Down`
- Prompt assembly now trims long summaries and only includes the full skill or MCP catalogs when the request actually asks for them
- Auto-scout now stays focused on find/definition/reference-style questions and contributes less context per turn

### Persistence And Context

- Saved sessions with `/resume`
- Session rename and transcript export
- Persistent memory with citation ids, trust, importance, and search
- Memory search over verification categories, tags, artifacts, and failures
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

### WebView2 Runtime

The Windows diff review and read-only diff viewer use WebView2.

Recommended deployment choices:

1. `Evergreen Bootstrapper`
   Best default for normal online installs.
2. `Evergreen Standalone Installer`
   Better for offline or restricted environments.
3. `Fixed Version Runtime`
   Use only when you must pin the rendering engine version.

Practical recommendation for Kernforge:

1. Bundle or download the `Evergreen Bootstrapper` in your installer.
2. Check for WebView2 Runtime before launching Kernforge.
3. Install it if missing.
4. If WebView2 still cannot be initialized, Kernforge falls back to the browser-based preview or terminal diff output depending on the workflow.

Reference:

- [Microsoft WebView2 distribution guidance](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution)
- [WebView2 Runtime downloads](https://developer.microsoft.com/en-us/microsoft-edge/webview2/)

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

LM Studio:

```powershell
.\kernforge.exe -provider openai-compatible -base-url http://localhost:1234/v1 -model local-model-id
```

### Windows Security Workflow Example

Basic safe flow for driver changes:

1. Edit driver-related files.
2. Run `/verify` to build a verification plan biased toward signing, symbols, packaging, and verifier readiness.
3. Run `/evidence-dashboard` or `/evidence-search category:driver` to inspect recent failed evidence.
4. If needed, run `/mem-search category:driver` to pull in older session context.
5. During push or PR creation, hook policy can re-check recent evidence and respond with warnings, confirmations, blocks, or automatic checkpoints.

Recommended flow with live state and simulation risk context:

1. `/investigate start driver-visibility guard.sys`
2. `/investigate snapshot`
3. `/simulate tamper-surface guard.sys`
4. `/open driver/guard.cpp`
5. `/review-selection integrity risk paths`
6. `/edit-selection harden the selected integrity checks`
7. `/verify`
8. `/evidence-dashboard category:driver`

The `driver-visibility` preset is intentionally narrow. It captures a lightweight triage snapshot of current driver visibility, verifier state, and related artifacts, not a deep driver load root-cause analysis.

The full explanation of this loop is in the [English Detailed Usage Guide](./FEATURE_USAGE_GUIDE.md).

Basic flow for telemetry regressions:

1. Edit provider, manifest, or XML-related files.
2. Run `/verify`.
3. Run `/evidence-search category:telemetry outcome:failed` to inspect recent provider or XML failures.
4. Run `/mem-search category:telemetry tag:provider` to recall earlier reasoning and regression context.
5. Before push or PR, hooks may inject extra review context or require confirmation.

### Frequently Used Command Cheat Sheet

Verification:
- `/verify`
- `/verify-dashboard`

Evidence:
- `/evidence`
- `/evidence-search category:driver outcome:failed`
- `/evidence-dashboard`

Memory:
- `/mem-search category:telemetry tag:provider`
- `/mem-dashboard`

Policy:
- `/hooks`
- `/hook-reload`

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
Relative-path read and search tools look in the current directory first, then fall back to the workspace root if the target is not found there.

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
  "request_timeout_seconds": 1200,
  "max_tool_iterations": 16,
  "auto_compact_chars": 45000,
  "auto_checkpoint_edits": true,
  "auto_verify": true,
  "msbuild_path": "C:\\Program Files\\Microsoft Visual Studio\\2022\\Community\\MSBuild\\Current\\Bin\\MSBuild.exe",
  "cmake_path": "C:\\Program Files\\CMake\\bin\\cmake.exe",
  "auto_locale": true,
  "hooks_enabled": true,
  "hooks_fail_closed": false
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
| `request_timeout_seconds` | Per-request model timeout in seconds; timed-out model turns retry once |
| `max_tool_iterations` | Max tool loop count per request |
| `permission_mode` | `default`, `acceptEdits`, `plan`, `bypassPermissions` |
| `shell` | Shell used by `run_shell` |
| `session_dir` | Directory for saved session JSON files |
| `auto_compact_chars` | Approximate context threshold before auto-compacting |
| `auto_checkpoint_edits` | Create a safety checkpoint before the first edit in a request |
| `auto_verify` | Master switch for automatic verification after edits |
| `msbuild_path` | Workspace override for MSBuild when PATH is incomplete |
| `cmake_path` | Workspace override for CMake when PATH is incomplete |
| `ctest_path` | Workspace override for CTest when PATH is incomplete |
| `ninja_path` | Workspace override for Ninja when PATH is incomplete |
| `auto_locale` | Inject the detected system locale into prompts |
| `memory_files` | Extra memory file paths |
| `skill_paths` | Extra skill search paths |
| `enabled_skills` | Skills always injected into prompts |
| `mcp_servers` | MCP server definitions |
| `profiles` | Saved recent or pinned provider/model profiles |
| `hooks_enabled` | Enable or disable the hook engine |
| `hook_presets` | Hook preset names loaded for the workspace |
| `hooks_fail_closed` | Block when hook evaluation fails instead of allowing by default |
| `project_analysis` | Multi-agent project analysis configuration, output path, and worker/reviewer profiles |
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
- `KERNFORGE_REQUEST_TIMEOUT_SECONDS`
- `KERNFORGE_AUTO_CHECKPOINT_EDITS`
- `KERNFORGE_AUTO_VERIFY`
- `KERNFORGE_AUTO_LOCALE`
- `KERNFORGE_MSBUILD_PATH`
- `KERNFORGE_CMAKE_PATH`
- `KERNFORGE_CTEST_PATH`
- `KERNFORGE_NINJA_PATH`

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
- Assistant turns that contain only tool calls omit empty assistant content for better API compatibility
- Non-JSON assistant tool-call arguments are normalized before request send
- HTTP error messages include a compact request preview to speed up provider debugging
- Streamed partial text is preserved on deadline when no tool call is in progress, and timed-out model turns retry once

### OpenRouter

- Default base URL: `https://openrouter.ai/api/v1`
- Reads `OPENROUTER_API_KEY`
- Interactive picker supports paging, filtering, curated recommendations, reasoning-only filtering, and sorting
- Uses the same request-timeout, streamed partial-text, incomplete-stream fallback, and single-retry behavior as the OpenAI-compatible client

### OpenAI-compatible

- Uses OpenAI-style chat completions
- Reads `OPENAI_API_KEY` unless overridden by config/env
- Requires an explicit `base_url`
- Applies the same assistant tool-call normalization and request-preview diagnostics as the OpenAI provider

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
/init hooks
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
/hooks
/hook-reload
/override
```

- `/status` shows current session and runtime state such as approvals, active session, memory, verification, and MCP counts.
- `/config` shows effective settings such as provider defaults, token limits, hooks, locale, and verification toggles.

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
/set-analysis-models
/analyze-project [--mode map|trace|impact|security|performance] <goal>
/analyze-performance [focus]
/do-plan-review <task>
/new-feature <task>
/permissions [mode]
/set_max_tool_iterations <n>
/locale-auto [on|off]
```

### Canceling And History

- `Esc` while typing: cancel current input
- `Esc` during a request: cancel the in-flight model request
- On Windows, brief `Esc` taps are still recognized as request cancel
- After request cancel, Kernforge waits for `Esc` release and clears pending console input before the next prompt
- `Up` / `Down` in the Windows console: recall recent inputs

### Tab Completion

`Tab` completion supports:

- Slash commands
- `/analyze-project --mode ...` and built-in mode values
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
- `/diff` and `/diff-selection` open a read-only internal diff viewer on Windows
- The internal diff viewer includes changed-file navigation, unified/split mode switching, and intraline highlights

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

On Windows, `/diff` and `/diff-selection` prefer the internal WebView2 diff viewer. If that surface is unavailable, Kernforge falls back to terminal output.

Built-in AI git tools available to the model include:

- `git_status`
- `git_diff`
- `git_add`
- `git_commit`
- `git_push`
- `git_create_pr`

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

## Evidence, Investigation, And Simulation

Kernforge now includes a security-oriented operational loop around evidence capture, live investigation state, and risk-oriented simulation.

Evidence commands:

```text
/evidence
/evidence-search <query>
/evidence-show <id>
/evidence-dashboard [query]
/evidence-dashboard-html [query]
```

Investigation commands:

```text
/investigate [subcommand]
/investigate-dashboard
/investigate-dashboard-html
```

Simulation commands:

```text
/simulate [profile]
/simulate-dashboard
/simulate-dashboard-html
```

Hook and override commands:

```text
/hooks
/hook-reload
/override
/override-add ...
/override-clear ...
```

## Project Analysis

The new project analysis flow is designed for large or risky codebases where you want a durable architecture map instead of an ad hoc one-shot summary.

Core commands:

```text
/analyze-project [--mode map|trace|impact|security|performance] <goal>
/analyze-performance [focus]
/set-analysis-models
```

Mode summary:

- `map`: default architecture map focused on subsystem ownership and module boundaries
- `trace`: execution path and caller/callee flow emphasis
- `impact`: change impact and blast-radius emphasis
- `security`: trust boundaries, validation, privileged surfaces, and tamper-sensitive paths, with dedicated `driver`, `IOCTL`, `handle`, `memory`, and `RPC` decomposition
- `performance`: startup cost, hot paths, blocking chains, and contention emphasis

What it does:

- Scans the workspace into a structured snapshot
- Splits the codebase into analysis shards
- Uses semantic shard planning to prioritize startup, network, UI, GAS, asset/config, and integrity slices in large or Unreal-heavy workspaces
- Uses a conductor plus multiple worker/reviewer passes
- Builds a structural index and an Unreal semantic graph
- Tracks semantic fingerprints plus structured invalidation diffs to explain why shards were recomputed
- Writes Markdown and JSON analysis artifacts
- Maintains a `latest` knowledge pack for follow-up analysis
- Produces a vector corpus and provider-specific ingestion seeds
- Reuses unchanged shard results when incremental analysis is enabled

Typical outputs:

- `.kernforge/analysis/<timestamp>_<goal>.md`
- `.kernforge/analysis/<timestamp>_<goal>.json`
- `.kernforge/analysis/<timestamp>_<goal>_snapshot.json`
- `.kernforge/analysis/<timestamp>_<goal>_structural_index.json`
- `.kernforge/analysis/<timestamp>_<goal>_structural_index_v2.json`
- `.kernforge/analysis/<timestamp>_<goal>_unreal_graph.json`
- `.kernforge/analysis/<timestamp>_<goal>_knowledge.md`
- `.kernforge/analysis/<timestamp>_<goal>_knowledge.json`
- `.kernforge/analysis/<timestamp>_<goal>_performance_lens.md`
- `.kernforge/analysis/<timestamp>_<goal>_performance_lens.json`
- `.kernforge/analysis/<timestamp>_<goal>_vector_corpus.json`
- `.kernforge/analysis/<timestamp>_<goal>_vector_corpus.jsonl`
- `.kernforge/analysis/<timestamp>_<goal>_vector_ingest_manifest.json`
- `.kernforge/analysis/<timestamp>_<goal>_vector_ingest_records.jsonl`
- `.kernforge/analysis/<timestamp>_<goal>_vector_pgvector.sql`
- `.kernforge/analysis/<timestamp>_<goal>_vector_sqlite.sql`
- `.kernforge/analysis/<timestamp>_<goal>_vector_qdrant.jsonl`
- `.kernforge/analysis/latest/`

Recommended flow:

1. Run `/analyze-project anti-cheat startup and integrity architecture`.
2. Review the generated knowledge pack and shard outputs.
3. Run `/analyze-performance startup` or another focus area such as `scanner`, `compression`, `upload`, `ETW`, or `memory`.
4. Use the resulting knowledge in `/review-selection`, `/edit-selection`, `/verify`, and evidence-guided hook policy.

## Notes

- The separate text viewer and the WebView2 diff surfaces are primarily implemented for Windows.
- If the WebView2 diff surface cannot be initialized, Kernforge falls back to the browser-based preview or terminal output depending on the workflow.
- The CLI core, sessions, providers, memory, skills, MCP support, and verification logic are designed to remain portable where possible.
