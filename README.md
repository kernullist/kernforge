# Kernforge

![Kernforge banner](./branding/kernforge-release-banner-1280x640.png)

| Axis | Kernforge | Codex | Claude Code |
|---|---|---|---|
| Best fit | Windows security, anti-cheat, telemetry, driver workflows, large-project analysis, evidence-backed verification | General coding agent work, local editing loops, task delegation, automation, PR-oriented workflows | General agentic coding, configurable hooks, subagents, external integrations, team policy workflows |
| Main strength | Turns a large workspace into reusable project intelligence, security docs, fuzz targets, verification history, evidence, and persistent memory | Feels natural when asked to finish a task end to end: inspect, edit, test, recover, summarize | Strong customization surface: hooks, subagents, MCP-style integrations, organization-specific workflows |
| Conversation memory | Stores conversation events, active state, recent errors, suggestion memory, task graph, and persistent memory | Very strong thread/workspace awareness and task continuity | Strong conversational context with configurable project instructions and agent setup |
| Proactive judgment | Rule/data-driven `SituationSnapshot` suggests verification, stale docs, fuzz gaps, provider failures, checkpoint/worktree, PR review, and automation follow-up | Strong at deciding the next practical step during implementation | Strong when workflows are encoded through hooks, subagents, and project conventions |
| Verification and evidence | First-class: adaptive verification, verification history, evidence store, dashboards, memory promotion, fuzz result gates | Strong test/command loop, but domain evidence modeling is generic | Strong tool loop, but evidence modeling depends on user/project setup |
| Windows/security specialization | Deeply tuned for IOCTL, ETW, drivers, memory scanning, Unreal, telemetry, signing, fuzzing, and anti-cheat surfaces | Broad coding agent, not domain-specific by default | Broad coding agent, not domain-specific by default |
| Automation maturity | Local MVP: `/automation`, recurring verification slots, `/review-pr` report, suggestion-to-task graph; scheduler/GitHub API still pending | Mature automation and PR/task workflow direction | Automation often comes through hooks and external workflow integration |
| Tradeoff | More specialized and evidence-heavy, with a smaller general ecosystem and less polished desktop/cloud experience | More polished general agent experience, less specialized security/fuzzing knowledge out of the box | More configurable ecosystem, less built-in Windows security/fuzz workbench depth |

`Kernforge` is a project intelligence and fuzzing workbench for Windows security, anti-cheat engineering, and evidence-backed verification. It is written in Go, runs as a terminal-first local agent, and is tuned for telemetry, driver-oriented workflows, memory inspection, Unreal security, and large project analysis.

Its strongest current value is a `multi-agent project analysis pipeline` that turns a large workspace into reusable project intelligence, then carries that context into editing, verification, evidence, fuzzing, and policy.  
Kernforge is now centered on `project analysis -> performance lens -> adaptive verification -> evidence store -> persistent memory -> hook policy -> checkpoint/rollback`, which makes it especially useful for driver, telemetry, memory-scan, and Unreal security workflows.

The current product direction has two main pillars. The first is whole-project analysis and documentation. The second is a specialized fuzzing toolchain that runs from source-based triage into native fuzzing execution. The Korean and English README files should contain the same content, with each document maintained as a translation of the same feature scope and roadmap direction.

## Flagship Capability

If Kernforge has one feature to understand first, it is `multi-agent project analysis`.

- `/analyze-project [--path <dir>] [--mode map|trace|impact|surface|security|performance] [goal]` builds a reusable architecture map instead of a disposable summary, and infers a mode-specific goal when you omit one
- The output becomes a durable knowledge pack, performance lens, structural index, vector-ready analysis set, operational docs, and an HTML dashboard
- That analysis is then reused in review, editing, verification, and policy workflows
- The next roadmap focus is expanding the new `/fuzz-campaign` planner from one-command campaign automation into native crash, coverage, evidence, and verification-gate lifecycle management

## Documentation

Quick Start:
- [English Quickstart](./QUICKSTART.md)
- [한국어 빠른 시작](./QUICKSTART_kor.md)

Guides:
- [English Feature Usage Guide](./FEATURE_USAGE_GUIDE.md)
- [한국어 기능 활용 가이드](./FEATURE_USAGE_GUIDE_kor.md)

Playbooks:
- [Driver Playbook](./PLAYBOOK_driver.md)
- [한국어 Driver 플레이북](./PLAYBOOK_driver_kor.md)
- [Telemetry Playbook](./PLAYBOOK_telemetry.md)
- [한국어 Telemetry 플레이북](./PLAYBOOK_telemetry_kor.md)
- [Memory-Scan Playbook](./PLAYBOOK_memory_scan.md)
- [한국어 Memory-Scan 플레이북](./PLAYBOOK_memory_scan_kor.md)

Specs And Roadmap:
- [Korean Roadmap](./ROADMAP_kor.md)
- [Korean Hook Engine Spec](./HOOK_ENGINE_SPEC_kor.md)
- [Korean Live Investigation Mode Spec](./LIVE_INVESTIGATION_SPEC_kor.md)
- [Korean Adversarial Simulation Spec](./ADVERSARIAL_SIMULATION_SPEC_kor.md)
- [Korean Next-Gen Project Analysis Spec](./PROJECT_ANALYSIS_NEXT_SPEC_kor.md)

The most practical end-to-end workflow is described in the [English Detailed Usage Guide](./FEATURE_USAGE_GUIDE.md). The highest-value current loop is `investigate -> simulate -> fuzz-func -> review/edit/plan -> verify -> evidence/memory/hooks`.

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

- Multi-agent project analysis with reusable knowledge packs, a performance lens, operational docs, and an HTML dashboard
- Structured interactive orchestration with `TaskState`, `TaskGraph`, node-aware recovery, and executor guidance
- Built-in specialist subagent catalog with editable and read-only routing profiles
- Node-level editable ownership and lease routing plus specialist worktree leases and session-level worktree isolation
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
- Automatic secondary editable workers for disjoint edit leases plus specialist-aware background verification bundle chaining

## Highlights

### Project Analysis

- `/analyze-project [--path <dir>] [--mode map|trace|impact|surface|security|performance] [goal]` runs a conductor plus multiple sub-agents and writes a project document
- If you omit `--mode`, the default mode is `map`
- If you omit the goal, Kernforge infers one from `--mode` and `--path`
- Non-map modes such as `trace`, `impact`, `surface`, `security`, and `performance` automatically load the most relevant previous `map` run as a baseline architecture map when one exists
- The analysis confirmation screen shows the selected `baseline_map` before asking whether to proceed
- Provider rate-limit or transient worker/reviewer failures degrade the affected shard instead of aborting the whole analysis run; the final document marks those sections as low confidence
- `surface` mode makes IOCTL, RPC, parser, handle, memory-copy, telemetry decoder, and network entry points first-class analysis targets
- In `security` mode, the analysis now decomposes results into dedicated `driver`, `IOCTL`, `handle`, `memory`, and `RPC` surfaces when those paths are present
- Incremental shard reuse avoids re-analyzing unchanged areas when possible
- Goal text can narrow analysis to matching directories when you explicitly target a sub-area of the workspace; use `--path <dir>` when you want that scope to be explicit and validated before the run
- Interactive runs can flag hidden or external-looking directories and let you exclude them from the analysis pass
- Semantic fingerprint invalidation can force recomputation when structure changes even if file scope looks stable
- Build alignment now lifts `.uproject`, `.uplugin`, `.Build.cs`, `.Target.cs`, and `compile_commands.json` into reusable build-context records
- `structural_index_v2` now carries symbol anchors, build ownership edges, function-level call edges, and overlay edges instead of staying file-centric
- `trace`, `impact`, and `security` retrieval now expand graph neighborhoods and emit `build_context_v2` plus `path_v2` evidence
- Unreal project, module, target, type, network, asset, system, and config signals are lifted into structured analysis artifacts
- A semantic shard planner plus semantic-aware worker and reviewer prompts prioritize startup, network, UI, GAS, asset/config, and integrity surfaces
- In addition to a knowledge pack, the pipeline now emits a structural index, `structural_index_v2`, Unreal semantic graph, vector corpus, and vector ingestion exports
- Generated docs and `dashboard.html` make the latest project knowledge base browsable as a static document portal with search, source anchors, graph-linked stale section diff, trust-boundary/attack-flow views, evidence/memory drilldowns, and docs-backed vector corpus reuse
- After analysis, Kernforge prints an `Analysis handoff` that points to `/analyze-dashboard`, `/fuzz-campaign run`, a top `/fuzz-func ...` drilldown, or `/verify` when the generated docs support that next step
- The source-anchor parser now handles modern C++ patterns such as template out-of-line methods, operators, `requires` and `decltype(auto)` headers, API-macro-wrapped scopes, and friend functions
- Security-mode final documents now add a `Security Surface Decomposition` section so privileged and abuse-sensitive paths do not get flattened into a generic summary
- Dedicated worker and reviewer models can be configured separately from the main chat model
- Architecture knowledge packs and performance lenses are written under `.kernforge/analysis`
- `/analyze-dashboard [latest|path]` opens the latest or a selected analysis document portal
- `/docs-refresh` regenerates the latest operational docs, dashboard, and docs-backed vector corpus deterministically from the saved analysis run
- `/analyze-performance [focus]` uses the latest analysis artifacts to reason about hot paths and bottlenecks
- Performance reports now end with a `Performance handoff` toward `/analyze-dashboard`, `/verify`, `/simulate stealth-surface`, or a concrete `/fuzz-func ...` hotspot drilldown

### Security Verification And Policy Loop

- Security-aware verification for driver, telemetry, Unreal, and memory-scan changes
- Verification history and verification dashboards
- `/verify` now ends with a `Verification handoff`: failures point back to repair/retry dashboards, while passing runs suggest checkpointing and either feature status or close depending on tracked feature state; native fuzz findings are pulled into targeted planner steps
- Structured evidence capture from verification
- Evidence search and evidence dashboards
- `/investigate` and `/simulate` now print handoffs into snapshots, risk simulation, `/verify`, and evidence dashboards so the user does not need to memorize the analysis loop
- Evidence and memory views now print handoffs back into `/verify`, source dashboards, `/mem-confirm`, `/mem-promote`, or dashboard review when records need action
- Checkpoints, tracked features, isolated worktrees, and specialist assignments now give short follow-up hints for diff review, implementation, cleanup, preservation, and fuzz verification gates
- Runtime toggle for automatic verification with `/set-auto-verify [on|off]`
- Windows verification tool path detection and overrides with `/detect-verification-tools` and `/set-*-path`
- Hook-based push and PR warnings, confirmations, and blocks based on recent failed evidence
- Automatic safety checkpoint creation for repeated high-risk failure patterns

### Source-Level Function Fuzzing

- Start function-level or file-level source fuzzing with `/fuzz-func <function-name>`, `/fuzz-func <function-name> --file <path>`, or `/fuzz-func @<path>`
- When you pass only `@<path>` or `--file <path>`, Kernforge expands from the starting file through include/import plus real call flow and picks a representative root automatically
- Planning no longer depends on `analyze-project` or a prebuilt `structural_index_v2`; Kernforge can scan the workspace and rebuild the semantic index on demand
- The default mode is AI source-only fuzzing rather than native execution, so Kernforge derives attacker input states, concrete sample values, branch predicates, minimal counterexamples, branch deltas, and downstream call chains directly from source
- High-risk findings now show a scored risk table, the first source location to inspect, the file-expansion path from the selected starting file, and the representative call path from the chosen root
- If `compile_commands.json` or other build context exists, Kernforge can prepare a stronger native follow-up; if it does not, Kernforge explains the missing setup before asking whether to continue
- Artifacts are stored under `.kernforge/fuzz/<run-id>/` with files such as `report.md`, `harness.cpp`, and `plan.json`
- Use `/fuzz-func status|show|list|continue|language` to inspect saved runs, resume blocked execution, and switch output language
- After `/fuzz-func` produces source-only scenarios, Kernforge prints a campaign handoff so the user sees `/fuzz-campaign run` as the single next command instead of memorizing campaign steps
- Use `/fuzz-campaign new <name>` to create a campaign manifest under `.kernforge/fuzz/<campaign-id>/` with `corpus`, `crashes`, `coverage`, `reports`, and `logs` directories
- Campaigns seed their initial target list from the latest generated `FUZZ_TARGETS.md` catalog when analysis docs are available
- Use `/fuzz-campaign` to see Kernforge's recommended next step, then `/fuzz-campaign run` to let it create, attach, promote source-only seed artifacts, update deduplicated finding lifecycle and coverage gap entries, ingest libFuzzer logs, llvm-cov text, LCOV, and JSON coverage summaries from run output or the campaign coverage directory, capture sanitizer reports, Windows crash dumps, Application Verifier, Driver Verifier artifacts, and native run reports/evidence, feed the next `FUZZ_TARGETS.md` ranking refresh, and feed `/verify` plus tracked feature gates automatically
- Completion shows function-name and file-usage hints after `/fuzz-func `, then switches to real workspace file candidates as soon as you start typing `@`

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
- In ordinary product development, `implementation-owner` is the default editable specialist, while narrower domain specialists such as `driver-build-fixer`, `telemetry-analyst`, `unreal-integrity-reviewer`, and `memory-inspection-reviewer` take ownership only when the task or paths match strongly.
- `apply_patch`, `write_file`, `replace_in_file`, and scoped shell writes follow node ownership and lease routing into the assigned specialist worktree.
- `/specialists assign <node-id> <specialist> [glob,glob2]` lets you pin an editable specialist and override ownership globs when auto-routing picked a broader default.
- `/set-specialist-model <specialist> <provider> [model]` pins the LLM used by one specialist in this workspace, and `/set-specialist-model clear <specialist|all>` removes that override.
- Secondary edit nodes with disjoint leases can run through automatic editable workers, while overlapping leases are deferred instead of racing on the same files.
- When a parallel specialist edit restarts verification, older background verification bundles for the same owner or same lease are superseded automatically, and verification-like bundle completion closes the owning node.

### Tracked Feature Workflow

- `/new-feature <task>` creates a tracked feature workspace and writes `spec.md`, `plan.md`, and `tasks.md`
- Tracked feature artifacts live under `.kernforge/features/<id>` so large work can survive across sessions
- `/new-feature status|plan|implement|close [id]` lets you inspect, regenerate, execute, and finish the active feature; status also surfaces recent fuzz campaign gates when native results exist
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

- `Tab` completion for commands, paths, mentions, MCP targets, fixed command arguments, provider subcommands such as `/provider status|anthropic|openai|openrouter|ollama`, analyze-project modes, compact fuzz campaign actions, and saved ids or subcommands such as `/resume`, `/mem-show`, `/evidence-show`, `/investigate show`, `/simulate show`, `/fuzz-campaign run|show`, `/new-feature status|plan|implement|close`, `/specialists status|assign|cleanup`, and `/worktree status|create|leave|cleanup`
- Completion menus now show inline descriptions for commands and common subcommands instead of listing names only
- `Esc` to cancel current input
- `Esc` to cancel an in-flight request
- Pressing `Enter` on an empty main prompt is ignored so the REPL does not create empty turns
- The REPL uses a compact branded banner, subtle turn dividers, grouped status/config sections, and separate assistant versus tool activity streams for denser terminal UX
- Assistant streaming output now suppresses leading blank chunks, flushes cleanly before progress lines, and inserts line breaks between repeated follow-on preambles
- Short tool-turn narration such as "let me inspect" or similar Korean preambles is buffered and collapsed into footer-style progress instead of spawning extra assistant transcript blocks
- Generic waiting text is collapsed so the thinking indicator does not repeat the same message twice
- Repeated blank streamed chunks are replaced with a compact working status instead of emitting empty lines
- Transient in-flight status, short `next` preambles, and tool progress now share a bottom footer panel instead of interleaving with the main transcript
- Confirmation prompts such as cancel, diff preview, write approval, and verification recovery temporarily take over that same footer slot so they stay visually pinned at the bottom
- Persistent results such as completion summaries, output paths, warnings, and configuration changes remain in the main transcript while ephemeral progress stays in the footer
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

Inside the interactive REPL, use `/provider status` to inspect the active provider, normalized `base_url`, API key presence, and provider-specific budget visibility.

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

### Specialist Subagents And Worktree Isolation Example

`specialists` are enabled by default, while `worktree_isolation` is off by default. The combination is especially valuable for tracked feature execution, high-risk driver/telemetry/Unreal/memory changes, and any request that touches multiple ownership domains in one turn.

For most ordinary web, backend, tooling, and application development, think of `implementation-owner`, `planner`, and `reviewer` as the default trio. Driver, telemetry, Unreal, and memory specialists are narrower profiles that activate when task text or file paths strongly match those domains.

```json
{
  "auto_verify": true,
  "specialists": {
    "enabled": true
  },
  "worktree_isolation": {
    "enabled": true,
    "root_dir": "C:\\Users\\you\\.kernforge\\worktrees",
    "branch_prefix": "kernforge/",
    "auto_for_tracked_features": true
  }
}
```

When to use it:

1. Even in normal feature work, you want to touch files like `api/handlers.go`, `pkg/cache/store.go`, and `web/src/settings.tsx` in one request without one edit lane spilling into the others.
2. You are implementing a tracked feature and want a rollback-friendly isolated git worktree instead of mutating the base workspace directly.
3. Auto-routing picked the broad default `implementation-owner`, but you want to pin a narrower domain specialist for one node.
4. You are iterating on the same file repeatedly and do not want to manually clean up stale background verification bundles between attempts.

Recommended flow 1: let Kernforge auto-assign for ordinary feature work

1. Turn on `worktree_isolation.enabled=true` in `.kernforge/config.json`.
2. Run `/new-feature start settings page and cache invalidation cleanup`.
3. Run `/new-feature implement`.
4. Phrase the implementation request with concrete paths. Example: `Safely update web/src/settings.tsx and pkg/cache/store.go, and keep the settings save flow and cache invalidation verification tight.`
5. Kernforge will assign specialists per task-graph node, then attach editable ownership and lease paths to each node.
6. If the secondary edit nodes have disjoint leases, an automatic editable worker can create an additional patch in its own specialist worktree.
7. If verification restarts for the same owner or same lease, the older background verification bundle is superseded automatically, so you do not keep following stale output.
8. Use `/tasks`, `/specialists status`, `/worktree status`, and `/verify-dashboard` to inspect routing and verification progress.
9. When the isolated worktree is clean and no longer needed, run `/worktree cleanup`.

Recommended flow 2: pin domain specialists manually

1. Run `/tasks` to inspect the node ids.
2. Run `/specialists assign plan-02 driver-build-fixer driver/**,*.inf,*.cat`.
3. Run `/specialists assign plan-03 telemetry-analyst telemetry/**,*.man,*.xml`.
4. Continue the implementation request.
5. After that, edit tools and scoped shell writes are only allowed inside that node's ownership and specialist worktree.
6. If you try to write outside the owned scope, Kernforge will return a reassignment hint instead of silently widening the boundary.

Recommended flow 3: use worktree isolation first

1. Run `/worktree create anti-cheat-hardening`.
2. Continue the usual review, edit, and verify loop.
3. Use `/worktree leave` if you want to go back to the base root without deleting the isolated tree yet.
4. Use `/worktree cleanup` after the tree is clean and you are done with it.

Practical tips:

1. If you want automatic parallel edit lanes, mention concrete paths such as `pkg/cache/store.go`, `web/src/settings.tsx`, or `Config/DefaultGame.ini` directly in the request.
2. If two edit nodes overlap on the same path or glob, Kernforge intentionally defers the secondary lane and falls back to serial execution.
3. `specialists.profiles` can override built-in profiles. This is useful when you want a stronger model only for `telemetry-analyst`, or when `driver-build-fixer` should also own `package/**`.

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

Isolated implementation:
- `/specialists`
- `/specialists status`
- `/specialists assign <node-id> <specialist> [glob,glob2]`
- `/set-specialist-model <specialist> <provider> [model]`
- `/worktree status`
- `/worktree create [name]`
- `/worktree leave`
- `/worktree cleanup`

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
  "specialists": {
    "enabled": true
  },
  "worktree_isolation": {
    "enabled": true,
    "root_dir": "C:\\Users\\you\\.kernforge\\worktrees",
    "branch_prefix": "kernforge/",
    "auto_for_tracked_features": true
  },
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
| `max_request_retries` | Retry count for transient provider errors or timed-out model requests |
| `request_retry_delay_ms` | Base backoff delay in milliseconds before retrying model requests |
| `request_timeout_seconds` | Per-request model timeout in seconds |
| `max_tool_iterations` | Max tool loop count per request |
| `permission_mode` | `default`, `acceptEdits`, `plan`, `bypassPermissions` |
| `shell` | Shell used by `run_shell` |
| `shell_timeout_seconds` | Default timeout in seconds used by `run_shell` |
| `read_hint_spans` | Shared `read_file` and `grep` cached-nearby hint history size |
| `read_cache_entries` | `read_file` in-memory cached range entry count |
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
| `specialists` | Enable specialist subagents and overlay built-in specialist profiles |
| `worktree_isolation` | Configure isolated git worktree roots, branch prefixes, and tracked-feature auto-isolation |

### Interactive Loop Durability Notes

- The interactive loop now attempts planner/reviewer preflight by default for each new request. If no dedicated review profile is configured, Kernforge falls back to an auxiliary client created from the active main provider/model.
- Before returning a substantial final answer, the interactive loop now asks the reviewer to approve or request revision. Recovery can also trigger a refreshed execution plan instead of repeating the same failing path.
- The interactive runtime now keeps both a structured `TaskState` and a persisted `TaskGraph`, so goals, plan progress, pending checks, background ownership, and high-value events survive compaction more reliably than transcript-only state.
- Task-graph nodes now track retry budgets and recent failure context. Repeated failures on the same node can block that node explicitly, which pushes the executor toward a materially different recovery path instead of repeating the same failing step forever.
- `run_shell` now supports scoped workspace writes when the agent provides `allow_workspace_writes=true` together with `write_paths`. This path is intended for formatters, code generators, or setup commands that are safer to run than re-creating the change by hand.
- Long-running build, test, and verification commands can use `run_shell_background` and `check_shell_job` so the agent can poll an existing job instead of restarting the same expensive command. Matching running jobs are reused automatically.
- Independent long-running verification commands can also use `run_shell_bundle_background` and `check_shell_bundle` to run and poll several background jobs in parallel. Bundle metadata is persisted in the session, so the agent can resume polling with `bundle_id="latest"` even after compaction.
- Background work is now node-aware. Long-running verification carries `owner_node_id` and owner lease context, newer verification bundles for the same owner or same lease supersede older ones, and verification-like bundle completion syncs back into the owning plan node automatically.
- Secondary executor nodes can now run not only automatic read-only worker follow-ups but also automatic editable workers. On disjoint leases, a specialist can patch in its own worktree and persist both the edit summary and follow-up verification bundle state back into the task graph.

### Environment Variables

General overrides:

- `KERNFORGE_PROVIDER`
- `KERNFORGE_MODEL`
- `KERNFORGE_BASE_URL`
- `KERNFORGE_API_KEY`
- `KERNFORGE_PERMISSION_MODE`
- `KERNFORGE_SHELL`
- `KERNFORGE_SESSION_DIR`
- `KERNFORGE_MAX_REQUEST_RETRIES`
- `KERNFORGE_REQUEST_RETRY_DELAY_MS`
- `KERNFORGE_REQUEST_TIMEOUT_SECONDS`
- `KERNFORGE_SHELL_TIMEOUT_SECONDS`
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
- `/provider status` shows Billing-page visibility plus the documented Usage & Cost Admin API limits instead of guessing a live standard-key balance endpoint

### OpenAI

- Default base URL: `https://api.openai.com`
- Reads `OPENAI_API_KEY`
- Assistant turns that contain only tool calls omit empty assistant content for better API compatibility
- Non-JSON assistant tool-call arguments are normalized before request send
- HTTP error messages include a compact request preview to speed up provider debugging
- Streamed partial text is preserved on deadline when no tool call is in progress, and transient provider errors or timed-out model turns retry according to the interactive request retry settings
- `/provider status` shows usage/cost visibility and rate-limit guidance, and notes that an exact prepaid-balance API endpoint is not currently documented

### OpenRouter

- Default base URL: `https://openrouter.ai/api/v1`
- Reads `OPENROUTER_API_KEY`
- Interactive picker supports paging, filtering, curated recommendations, reasoning-only filtering, and sorting
- Uses the same request-timeout, streamed partial-text, incomplete-stream fallback, and single-retry behavior as the OpenAI-compatible client
- `/provider status` performs a live `/key` lookup for key-level `limit_remaining` and `usage`, and it also queries `/credits` when the key is a management key

### OpenAI-compatible

- Uses OpenAI-style chat completions
- Reads `OPENAI_API_KEY` unless overridden by config/env
- Requires an explicit `base_url`
- Applies the same assistant tool-call normalization and request-preview diagnostics as the OpenAI provider
- `/provider status` can show the normalized endpoint and key presence, but billing visibility depends on the upstream provider

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

For live web research, Kernforge now deploys the bundled MCP script to `~/.kernforge/mcp/web-research-mcp.js` on startup and auto-adds a matching `web-research` MCP entry to `~/.kernforge/config.json` when no equivalent web-search MCP is configured yet. You can provide `TAVILY_API_KEY`, `BRAVE_SEARCH_API_KEY`, or `SERPAPI_API_KEY` either through your shell environment or through `mcp_servers[].env` in config, then run `/reload` if you changed config or environment after startup. This workspace also includes a ready-to-run script at `.kernforge/mcp/web-research-mcp.js` plus a matching `.kernforge/config.json` entry. Once connected, Kernforge will prefer that MCP for latest/current research requests before local file inspection. `/init config` also enables the bundled web-research MCP by default when the script is available.

Minimal workspace config example:

```json
{
  "mcp_servers": [
    {
      "name": "web-research",
      "command": "node",
      "args": [".kernforge/mcp/web-research-mcp.js"],
      "env": {
        "TAVILY_API_KEY": "",
        "BRAVE_SEARCH_API_KEY": "",
        "SERPAPI_API_KEY": ""
      },
      "cwd": ".",
      "capabilities": ["web_search", "web_fetch"]
    }
  ]
}
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
/provider status
/model
/status
/version
/help
/reload
/hooks
/hook-reload
/override
/specialists
/worktree status
```

- `/status` shows current session and runtime state such as approvals, active session, memory, verification, and MCP counts.
- `/config` shows effective settings such as provider defaults, token limits, hooks, locale, and verification toggles.
- `/provider status` shows the active provider, normalized `base_url`, API key presence, and provider-specific budget visibility. OpenRouter performs a live lookup, while OpenAI and Anthropic expose officially documented limits and billing guidance.
- `/model` is the unified model-routing hub. It shows the main model, the plan-review reviewer, the analysis worker and reviewer, and any specialist-subagent overrides, then lets you pick one target to reconfigure.

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
/provider status
/model
/profile [list|<number>|rN|dN|pN]
/profile-review [list|<number>|rN|dN|pN]
/set-plan-review [provider]
/set-analysis-models
/set-specialist-model [status|clear <specialist|all>|<specialist> <provider> [model]]
/analyze-project [--path <dir>] [--mode map|trace|impact|surface|security|performance] [goal]
/analyze-dashboard [latest|path]
/docs-refresh
/analyze-performance [focus]
/do-plan-review <task>
/new-feature <task>
/specialists
/worktree [status|create [name]|leave|cleanup]
/permissions [mode]
/set-max-tool-iterations <n>
/locale-auto [on|off]
```

- `/model` does not take parameters. It first shows the current routing, then in interactive mode asks which target you want to change.
- `/model` is the main entry point for changing the main model, plan-review reviewer, analysis worker/reviewer, and specialist subagent models.
- Changing only the main model preserves explicit role model profiles. Any target shown as `not configured; follows main model` is intentionally inherited and will display the new main model until you configure that role.
- `/profile` and `/profile-review` list saved profiles without changing anything in one-shot mode. If no main profile exists but a provider/model is already selected, Kernforge saves the current settings as the first profile and then shows the list. Main profiles also store their own role model set for plan-review, analysis worker/reviewer, and specialist subagents. Changing those role models through `/model` updates the active main profile, and activating that profile restores the full set. Pass a number or action explicitly to activate, rename, delete, pin, or unpin.
- User and workspace profile lists are merged on load, and saving unrelated settings preserves existing main and review profiles instead of dropping them when a save payload omits profile arrays.
- `/set-plan-review [provider]` changes only the reviewer model used by plan review. The planner side still uses the main model.
- `/set-analysis-models` configures dedicated worker and reviewer profiles for project analysis.
- `/set-specialist-model ...` applies a workspace-scoped model override to one specialist subagent.

### Canceling And History

- `Esc` while typing: cancel current input
- `Esc` during a request: cancel the in-flight model request
- On Windows, brief `Esc` taps are still recognized as request cancel
- After request cancel, Kernforge waits for `Esc` release and clears pending console input before the next prompt
- `Up` / `Down` in the Windows console: recall recent inputs

### Tab Completion

`Tab` completion supports:

- Slash commands
- Command and subcommand descriptions in completion menus
- `/provider status|anthropic|openai|openrouter|ollama`
- `/analyze-project --path ...`, `/analyze-project --mode ...`, and built-in mode values
- `/fuzz-campaign status|run|new|list|show`
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

Source-level fuzzing commands:

```text
/fuzz-func <function-name>
/fuzz-func <function-name> --file <path>
/fuzz-func @<path>
/fuzz-func status
/fuzz-func show [id|latest]
/fuzz-func list
/fuzz-func continue [id|latest]
/fuzz-func language [system|english]
/fuzz-campaign status
/fuzz-campaign run
/fuzz-campaign new <name>
/fuzz-campaign list
/fuzz-campaign show [id|latest]
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
/analyze-project [--path <dir>] [--mode map|trace|impact|surface|security|performance] [goal]
/analyze-dashboard [latest|path]
/docs-refresh
/analyze-performance [focus]
/set-analysis-models
```

The goal is optional. When it is omitted, Kernforge derives a practical default from the selected mode and path.
When a previous `map` run exists, follow-up modes reuse it as baseline structure while still verifying mode-specific claims against the current files.

Mode summary:

- `map`: default architecture map focused on subsystem ownership and module boundaries
- `trace`: one runtime/request flow through callers, callees, dispatch points, ownership transitions, and source anchors
- `impact`: change blast radius, upstream/downstream dependencies, affected files, retest targets, and stale documentation risks
- `surface`: exposed entry surfaces such as IOCTL, RPC, parsers, handles, memory-copy paths, telemetry decoders, network inputs, and fuzz targets
- `security`: trust boundaries, validation, privileged paths, tamper-sensitive state, enforcement points, and driver/IOCTL/handle/RPC risks
- `performance`: startup cost, hot paths, blocking chains, allocation/copy pressure, contention, and profiling order

What it does:

- Scans the workspace into a structured snapshot
- Splits the codebase into analysis shards
- Uses semantic shard planning to prioritize startup, network, UI, GAS, asset/config, and integrity slices in large or Unreal-heavy workspaces
- Uses a conductor plus multiple worker/reviewer passes
- Builds a structural index and an Unreal semantic graph
- Tracks semantic fingerprints plus structured invalidation diffs to explain why shards were recomputed
- Writes Markdown and JSON analysis artifacts
- Generates an operational documentation set with `ARCHITECTURE.md`, `SECURITY_SURFACE.md`, `API_AND_ENTRYPOINTS.md`, `BUILD_AND_ARTIFACTS.md`, `VERIFICATION_MATRIX.md`, `FUZZ_TARGETS.md`, and `OPERATIONS_RUNBOOK.md`
- Writes a schema-versioned `docs_manifest.json`; readers treat missing `schema_version` as legacy and ignore unknown fields for additive compatibility
- Writes `dashboard.html` so run summary, generated docs, source anchors, graph-linked stale section diff, trust-boundary/attack-flow views, evidence/memory follow-ups, subsystem map, security surface, fuzz target candidates, and verification matrix are visible in a browser
- Adds generated-doc graph sections for project edges, trust boundaries, data-flow paths, and attack/data-flow follow-up commands, with graph-specific stale markers reflected in section metadata
- Recollects generated docs into `vector_corpus.*` as whole-document and section-level records with source anchors, confidence, stale markers, and reuse metadata
- README describes product scope and flagship commands, the feature guide describes practical operating loops, and generated docs serve as the per-run project knowledge base with source anchors, confidence, and stale markers
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
- `.kernforge/analysis/<timestamp>_<goal>_docs/`
- `.kernforge/analysis/<timestamp>_<goal>_docs_manifest.json`
- `.kernforge/analysis/<timestamp>_<goal>_dashboard.html`
- `.kernforge/analysis/latest/`
- `.kernforge/analysis/latest/run.json`
- `.kernforge/analysis/latest/docs/`
- `.kernforge/analysis/latest/docs_index.md`
- `.kernforge/analysis/latest/docs_manifest.json`
- `.kernforge/analysis/latest/dashboard.html`

Recommended flow:

1. Run `/analyze-project anti-cheat startup and integrity architecture`.
2. Open the latest dashboard with `/analyze-dashboard`, then review the generated knowledge pack, docs, and shard outputs.
3. Run `/analyze-performance startup` or another focus area such as `scanner`, `compression`, `upload`, `ETW`, or `memory`.
4. Use the resulting knowledge in `/review-selection`, `/edit-selection`, `/verify`, and evidence-guided hook policy.

## Source-Level Function Fuzzing

`/fuzz-func` is meant to answer the attacker question directly: if an input parameter is manipulated precisely, which guards, probes, copies, dispatches, and cleanup paths can be pushed into unintended behavior even before you build a runnable harness?

Core commands:

```text
/fuzz-func <function-name>
/fuzz-func <function-name> --file <path>
/fuzz-func <function-name> @<path>
/fuzz-func --file <path>
/fuzz-func @<path>
/fuzz-func status
/fuzz-func show [id|latest]
/fuzz-func continue [id|latest]
/fuzz-func language [system|english]
/fuzz-campaign status
/fuzz-campaign run
/fuzz-campaign new <name>
/fuzz-campaign list
/fuzz-campaign show [id|latest]
```

What it does:

- Combines function signatures, real function-body observations, and reachable call closure.
- Accepts a function name directly, or a file path and then expands through include/import plus actual call flow to pick a representative root automatically.
- Rebuilds snapshot and semantic-index context on demand, so `/analyze-project` is optional rather than required.
- Synthesizes attacker input states, concrete sample values, source-derived branch predicates, minimal counterexamples, pass/fail branch outcomes, and downstream call chains for higher-risk paths.
- Shows the first source lines to inspect, the path from the selected starting file into the target file, and the representative call path from the chosen root into that implementation.
- Uses native execution only as an optional follow-up. If build context is incomplete, Kernforge explains the gap first instead of silently failing.
- After a useful `/fuzz-func` result, Kernforge prints the campaign handoff and points to `/fuzz-campaign run` as the next automatic step.
- `/fuzz-campaign` shows the next recommended campaign action; `/fuzz-campaign run` performs the safe automatic step, including campaign creation, latest `/fuzz-func` attachment, deterministic JSON corpus seed promotion, deduplicated finding lifecycle updates, libFuzzer/llvm-cov/LCOV/JSON coverage report ingestion, sanitizer/verifier/crash-dump artifact capture, coverage gap feedback, artifact graph updates, native result report generation, crash fingerprinting, minimization command capture, evidence recording, `/verify` planner reuse, and tracked feature gate guidance when available.
- Native crash findings are merged by crash fingerprint, source anchor, and suspected invariant. The manifest preserves duplicate counts plus merged native result and evidence ids so repeated runs strengthen one issue instead of creating noisy copies.
- Campaign coverage gaps are written into the manifest and reused by the next `analyze-project` docs refresh so unexercised targets receive explicit `FUZZ_TARGETS.md` ranking feedback.
- `/fuzz-func ` completion starts with usage hints, then flips to real file candidates after `@` so file-scoped runs are easy to launch.

This is especially useful when:

1. You need fast triage on IOCTL handlers, parsers, validators, or buffer-processing code.
2. A normal source review is too vague and you want concrete "flip this predicate with this value, then this sink opens" guidance.
3. You only know the suspicious file, not the best root function yet.

How to read the output:

1. `Conclusion` gives the top predicted problem and the most useful branch delta first.
2. `Risk score table` pushes noisy generic fallbacks down and keeps source-grounded guard/probe/copy findings at the top.
3. `Top predicted problems` shows Kernforge's internal hypothetical input state and concrete sample values. These are analysis inputs, not instructions for manual reproduction.
4. `Source-derived attack surface` lists the real probes, copies, dispatches, and cleanup edges that grounded the scenarios.

Recommended workflow:

1. Start coarse with `/fuzz-func @Driver/Foo.c`
2. If you know the function, narrow with `/fuzz-func ValidateRequest --file src/guard.cpp`
3. Read the highest-score finding first, especially the branch delta summary and first source location
4. Re-run `/fuzz-func` on a deeper input-facing helper if you want tighter source-only fuzz reasoning

## Notes

- The separate text viewer and the WebView2 diff surfaces are primarily implemented for Windows.
- If the WebView2 diff surface cannot be initialized, Kernforge falls back to the browser-based preview or terminal output depending on the workflow.
- The CLI core, sessions, providers, memory, skills, MCP support, and verification logic are designed to remain portable where possible.
