# Kernforge

![Kernforge banner](./branding/kernforge-release-banner-1280x640.png)

## Flagship Capability

`Kernforge` is a project intelligence and fuzzing workbench for Windows security, anti-cheat engineering, and evidence-backed verification. It is written in Go, runs as a terminal-first local agent, and is tuned for telemetry, driver-oriented workflows, memory inspection, Unreal security, and large-project analysis.

The first five capabilities to understand are:

- `/analyze-project [--path <dir>] [--mode map|trace|impact|surface|security|performance] [goal]` builds reusable project intelligence: an architecture map, knowledge pack, performance lens, structural index, vector-ready analysis set, operational docs, and an HTML dashboard.
- `/review` is the common evidence-backed review harness for plans, code, selections, PRs, goals, final answers, analysis reports, pre-fix checks, pre-write checks, post-change checks, and MCP review. It tracks structured findings, request class, classification confidence and ambiguity warnings, lifecycle phase, route mode and route quality, typed state transitions, action envelopes, approval ledgers, capability manifests, freshness, next commands, repair guidance, read-only review mode, document-artifact gates, and write-isolated edit proposals. Natural-language review and edit requests now get runtime-enforced Codex-grade handling: simple reviews stay read-only and findings-first, ambiguous mixed requests choose the safest lifecycle with an explanation, document-artifact requests use artifact-quality gates instead of irrelevant code-review loops, single-model runs execute or explicitly record a separate second-pass review phase, optional cross-review findings are persisted in an actionable triage ledger, and final answers are corrected before display when they omit changed files, review result, validation result, artifact quality, limitations, or remaining risk.
- Fuzzing starts with `/fuzz-func` source-level triage and continues through `/fuzz-campaign` for campaign manifests, corpus/crash/coverage artifacts, sanitizer or verifier evidence, and verification gate lifecycle management.
- `/goal`, `-goal`, and `-goal-file` add the long-horizon autonomous execution layer: Kernforge persists an objective from a prompt or markdown file, then loops through implementation, independent review, repair, full verification, completion audit, final semantic review, and recovery until the goal is complete or concretely blocked
- `/find-root-cause [--pattern-pack <path-or-dir>] <problem>` clarity-checks the symptom prompt, then uses 1-8 route-limited worker shards, reviewer causality validation, deep verification, and deterministic quality gates to narrow plausible root causes

Kernforge is centered on `project analysis -> review -> fuzzing/root-cause investigation -> adaptive verification -> evidence store -> persistent memory -> hook policy -> checkpoint/rollback`, which makes it especially useful for driver, telemetry, memory-scan, and Unreal security workflows.

## Claude Code And Codex Comparison

| Axis | Kernforge | Claude Code | Codex |
|---|---|---|---|
| Best fit | Windows security, anti-cheat, telemetry, driver workflows, large-project analysis, evidence-backed verification | General agentic coding, configurable hooks, subagents, external integrations, team policy workflows | General coding agent work, local editing loops, task delegation, automation, PR-oriented workflows |
| Main strength | Turns a large workspace into reusable project intelligence, security docs, fuzz targets, verification history, evidence, and persistent memory | Strong customization surface: hooks, subagents, MCP-style integrations, organization-specific workflows | Feels natural when asked to finish a task end to end: inspect, edit, test, recover, summarize |
| Conversation memory | Stores conversation events, active state, recent errors, recovery briefs, suggestion memory, task graph, session dashboard, continuity packet, and persistent memory | Strong conversational context with configurable project instructions and agent setup | Very strong thread/workspace awareness and task continuity |
| Proactive judgment | Rule/data-driven `SituationSnapshot` suggests verification, stale docs, fuzz gaps, provider failures, checkpoint/worktree, PR review, and automation follow-up | Strong when workflows are encoded through hooks, subagents, and project conventions | Strong at deciding the next practical step during implementation |
| Verification and evidence | First-class: adaptive verification, verification history, evidence store, dashboards, memory promotion, fuzz result gates | Strong tool loop, but evidence modeling depends on user/project setup | Strong test/command loop, but domain evidence modeling is generic |
| Windows/security specialization | Deeply tuned for IOCTL, ETW, drivers, memory scanning, Unreal, telemetry, signing, fuzzing, and anti-cheat surfaces | Broad coding agent, not domain-specific by default | Broad coding agent, not domain-specific by default |
| Automation maturity | Local MVP: `/automation`, interval due checks, automation digest/monitor/watch, process-detached daemon, notify artifact and webhook transport, recurring verification slots, `/jobs`, `/recover`, `/continuity`, `/completion-audit`, `/review pr --github --draft-comments|--post-comments|--resolve-thread|--create-issue` with issue labels/assignees/milestones, `/handoff`, `/session-dashboard-html`, suggestion-to-task graph; cloud jobs still pending | Automation often comes through hooks and external workflow integration | Mature automation and PR/task workflow direction |
| Tradeoff | More specialized and evidence-heavy, with a smaller general ecosystem and less polished desktop/cloud experience | More configurable ecosystem, less built-in Windows security/fuzz workbench depth | More polished general agent experience, less specialized security/fuzzing knowledge out of the box |

## Repository Layout

To keep the GitHub landing page readable, the repository root is reserved for docs, build scripts, branding assets, and release assets. The actual Go application package lives under `cmd/kernforge`, and builds should target that package.

- `cmd/kernforge`: Kernforge CLI, MCP server, daemon, analysis, fuzzing, verification implementation, and Go tests
- `cmd/kernforge/.kernforge/mcp`: embedded web-research MCP script source copy
- `cmd/kernforge/root_cause_patterns`: embedded root-cause pattern packs
- `docs/assets`: screenshots and documentation assets used by README and MCP guides
- `branding`, `buildtools`, `release`: product imagery, Windows resource build tooling, and release artifacts

## Source-Level Fuzzing

Kernforge source-level fuzzing can summarize a function input model, the problematic code location, trigger values, and generated harness/report artifact paths before any native build is required. When called through MCP in the Codex App, the result is designed to lead with `Result`, `Top candidate`, `Problem location`, `Trigger conditions KernForge generated`, and `Artifacts`.

![Codex App source-level fuzz result](./docs/assets/codex-app-source-fuzz-result.png)

This stage is not a confirmed native crash or sanitizer finding. It is a source-level finding meant to accelerate security review and harness design. From there, the natural path is `native_preview -> build_only -> runtime fuzzing` to validate compileability, execution, and crash/sanitizer reproduction.

## Documentation

Quick Start:
- [English Quickstart](./QUICKSTART.md)
- [한국어 빠른 시작](./QUICKSTART_kor.md)

Guides:
- [English Feature Usage Guide](./FEATURE_USAGE_GUIDE.md)
- [한국어 기능 활용 가이드](./FEATURE_USAGE_GUIDE_kor.md)
- [MCP And Skills](./MCP-SKILLS.md)
- [Korean MCP Server Mode Guide](./MCP_SERVER_MODE_kor.md)

Playbooks:
- [Driver Playbook](./PLAYBOOK_driver.md)
- [한국어 Driver 플레이북](./PLAYBOOK_driver_kor.md)
- [Telemetry Playbook](./PLAYBOOK_telemetry.md)
- [한국어 Telemetry 플레이북](./PLAYBOOK_telemetry_kor.md)
- [Memory-Scan Playbook](./PLAYBOOK_memory_scan.md)
- [한국어 Memory-Scan 플레이북](./PLAYBOOK_memory_scan_kor.md)

Specs And Roadmap:
- [Korean Roadmap](./ROADMAP_kor.md)
- [Korean Fuzzing And Security Tools Gap Analysis](./FUZZING_SECURITY_TOOLS_GAP_ANALYSIS_kor.md)
- [Korean Hook Engine Spec](./HOOK_ENGINE_SPEC_kor.md)
- [Korean Common Review Harness Spec](./REVIEW_HARNESS_SPEC_kor.md)
- [Korean Review Harness UX/Ops 85 Design](./REVIEW_HARNESS_UX_OPS_85_DESIGN_kor.md)
- [Korean Live Investigation Mode Spec](./LIVE_INVESTIGATION_SPEC_kor.md)
- [Korean Adversarial Simulation Spec](./ADVERSARIAL_SIMULATION_SPEC_kor.md)
- [Korean Next-Gen Project Analysis Spec](./PROJECT_ANALYSIS_NEXT_SPEC_kor.md)

The most practical end-to-end workflow is described in the [English Detailed Usage Guide](./FEATURE_USAGE_GUIDE.md). The highest-value current loop is `analyze-project -> investigate/simulate -> find-root-cause or fuzz-func -> review/edit/plan -> verify -> evidence/memory/hooks`.

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
- Symptom-driven `/find-root-cause` plus built-in `/root-cause-patterns` knowledge packs
- Pre-final coding harnesses for acceptance, artifact quality, scenario replay, subagent evidence, test impact, and background job state
- Evidence-backed apply/verify/retry edit-loop ledger that carries worker edits, patch evidence, background verification bundles, retry decisions, final review, and remaining risk into the final answer
- Runtime gate ledger that ties the latest review transaction, patch transaction, verification report, completion audit, and final-answer review together, then surfaces request class, classification confidence and ambiguity, lifecycle phase, single-model versus cross-model route mode, route quality and degraded-route reasons, review/repair/document/verification gate status, second-pass state, cross-review triage counts, blockers, waivers, obligations, and next commands in `/status`, `/hooks`, session dashboards, review artifacts, MCP responses, and final-answer prompts
- Structured interactive orchestration with `TaskState`, `TaskGraph`, node-aware recovery, and executor guidance
- Built-in task ownership profile catalog with editable/read-only routing and optional model overrides
- Node-level editable ownership and lease routing plus task-owner worktree leases and session-level worktree isolation
- Interactive REPL, one-shot `-prompt` mode, and one-shot `-command` slash command mode for schedulers
- Codex-style autonomous goals through `/goal`, `-goal`, and `-goal-file`, with prompt or markdown objectives that loop through implementation, self-review, verification, completion audit, final semantic review, and recovery without user prompts
- Providers: `openai-codex-subscription`, `openai-codex-cli`, `openai-api`, `anthropic-claude-cli`, `anthropic-api`, `DeepSeek`, `openrouter`, `opencode`, `opencode-go`, `ollama`, `lmstudio`, `vllm`, `llama.cpp`, plus explicit OpenAI-compatible routes
- A model route scheduler keyed by provider/model/base_url/reasoning_effort to coordinate single local models and shared worker/reviewer routes safely
- File, structured edit proposal, patch fallback, shell, and git-oriented tool use
- Git staging, commit, push, and GitHub pull request creation through dedicated tools
- Local file mentions, image mentions, and MCP resource mentions
- Session persistence, resume, rename, clear, compact, and Markdown export
- Natural failure recovery with `/continuity`, `/recover`, `/recover execute-safe`, `/completion-audit`, `/jobs`, local event export, and session dashboards
- Project memory files plus cross-session persistent memory with trust/importance metadata and automatic workspace continuity injection
- Evidence store, evidence search, and evidence dashboards
- Local `SKILL.md` skills with discovery and per-request activation
- Stdio MCP servers with tools, resources, and prompts
- Windows text viewer plus WebView2-based diff review and diff viewing for selection-first workflows
- Adaptive verification, verification history dashboards, checkpoints, and rollback. Build/test commands that may create artifacts use a Codex-style approval lifecycle: the transcript shows a verification approval request before execution, a declined run is recorded as skipped/declined rather than evidence, and same-turn verification execution or background-poll attempts are returned as `NOT_EXECUTED` before any new progress line is emitted unless the user explicitly approves verification. Declined or prompt-failed automatic verification is persisted as a skipped verification report, leaves verification pending, and is never rendered as completed. Generated-document-only artifact turns are the exception: once deterministic artifact-quality checks approve the report and the final answer avoids unsupported verification claims, Kernforge closes the self-driving task state instead of re-entering shell validation or review loops. A skipped automatic verification also blocks same-turn model-initiated verification retries, and the edit-loop ledger remains `risk_accepted` when changed paths have no successful verification evidence even if the final answer correctly discloses that verification was not run. If post-edit automatic verification fails outside the current patch scope, same-turn build/test/verification retries are also returned as `NOT_EXECUTED` so the model reports the external/ambient blocker instead of probing unrelated project failures. Non-interactive `-prompt -y` runs treat verification prompts like diff preview prompts and auto-run approved verification; non-bypass prompt runs still skip instead of guessing approval. Non-interactive single-shot runs (`-prompt`, `-command`, `-goal`, and `-goal-file`) do not install the ambient keyboard watcher, so long model/review/goal loops do not misclassify console noise as user cancellation. Generated analysis, fuzz, and manifest evidence is carried as informational verification output with no executable command, so untrusted evidence text cannot become shell input. Background job metadata keeps scalar `job_status` separate from list-style `job_entries`, preserving status and log evidence without overloading one key.
- Out-of-scope automatic verification failures now switch the next model turn to final-answer-only by withholding tool definitions. If a local or scripted route still emits a tool call, Kernforge returns `NOT_EXECUTED` and repeats the final-answer-only guidance instead of letting the turn drift into read/probe/build loops.
- Hook engine, workspace hook rules, and evidence-aware push/PR policy
- Common `/review` harness for plan, code, selection, PR, goal, final, analysis, automatic pre-fix, pre-write, post-change, and MCP review flows, including main-first review, optional cross reviewers, request-class-aware orchestration, read-only review mode, mixed edit-tool isolation, workspace-contained mentions, original-request isolation from internal repair guidance, typed action envelopes, separated approval ledgers, route-health-aware retries, local-code web-research blocking, high-budget pre-write evidence that preserves edit proposals, original repair findings, after excerpts, and focused file context before optional compaction, pre-write repair recovery with explicit `y/N` continuation, line-range-aware artifact integrity checks, replay fixtures, patch-transaction-scoped freshness, and final review details before diff preview. General agent and goal prompts include a Codex-grade request-handling contract, and the runtime now enforces the key parts: requests are classified as `review_only`, `document_artifact`, `review_then_modify`, `modify_then_review`, `verification_only`, `validation_only`, or `general` with confidence, ambiguity warnings, and class-specific contracts; no distinct cross-review route triggers a separate single-model second-pass review call or an explicit skipped-state disclosure; a configured reviewer-only post-change path avoids redundant single-model calls while keeping the single-model policy state visible; actionable cross-review findings must be reconciled in `cross_review_triage` as `accepted_fixed`, `accepted_deferred`, `rejected_with_reason`, or `needs_user_decision`; `needs_user_decision` includes what to inspect, what is safe to change, what not to change yet, and the next command; document artifacts are gated by artifact existence/topic/placeholder/claim checks instead of shell-review overblocking; and pre-final coding harness correction blocks incomplete edit/review/document final answers without legacy generic phrase whitelists. UI-polish review routing requires primary reviewer coverage unless collected changed paths prove the scope is style/asset/markup-only; executable source files keep primary correctness coverage even when their names look UI-related.
- Runtime review/final/completion ledgers prefer the current patch transaction over ambient dirty git state, while commit/push gates still account for all changed files. This keeps unrelated dirty worktree files from becoming false review blockers for the current repair.
- Post-change diff review is isolated from final-answer coding-harness state. Coding-harness blockers such as missing worker causal evidence remain final/completion-audit obligations, but they are not injected into post-change code evidence or deterministic diff-review blockers.
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
- Local-model runs adapt shard sizing from provider, model size, max tokens, and request timeout when shard limits are not explicitly configured. If a final provider timeout or 5xx/overload-style error still stops the run, Kernforge tells the user it is retrying and reruns once with smaller `max_lines_per_shard` / `max_files_per_shard` plus a higher shard cap. Rate limits are not retried this way because smaller shards would usually increase request pressure.
- When worker and reviewer share a provider/model route, shard concurrency follows the model route limit to avoid retry storms and low-confidence placeholder cascades. Local providers default to a route limit of 1, while cloud/API routes keep their configured concurrency instead of being forced to serial execution.
- During execution, the transcript shows shard waves, completed/failed shard counts, cache/review state, and model progress lines labeled with the active analysis stage and shard, for example `worker runtime: ...` or `reviewer security_rpc: ...`.
- Each run writes `analysis_preflight.json`, which records the inferred intent, effective mode, scope, required indexes, provider/runtime feedback, shard contracts, and success criteria before worker execution starts.
- Each shard now carries a contract (`type`, `objective`, `required_evidence`, and `success_criteria`), and worker reports include source-anchored `claims` so reviewer and synthesis steps can distinguish direct facts, inferences, risks, and unknowns.
- After worker/reviewer passes, `mode_scorecard.json` records mode-specific coverage, claim/evidence support, review approval, and any deterministic coverage gaps. When useful, Kernforge runs a bounded gap-filling shard pass before final synthesis.
- `surface` mode makes IOCTL, RPC, parser, handle, memory-copy, telemetry decoder, and network entry points first-class analysis targets
- In `security` mode, the analysis now decomposes results into dedicated `driver`, `IOCTL`, `handle`, `memory`, and `RPC` surfaces when those paths are present
- Incremental shard reuse avoids re-analyzing unchanged areas when possible
- Goal text can narrow analysis to matching directories when you explicitly target a sub-area of the workspace; use `--path <dir>` when you want that scope to be explicit and validated before the run
- Interactive runs can flag hidden or external-looking directories and let you exclude them from the analysis pass
- Semantic fingerprint invalidation can force recomputation when structure changes even if file scope looks stable
- Semantic invalidation reasons are refined into contract classes such as network, security, build/startup, asset/config, or runtime-flow changes so incremental reruns explain why a shard was refreshed.
- Build alignment now lifts `.uproject`, `.uplugin`, `.Build.cs`, `.Target.cs`, and `compile_commands.json` into reusable build-context records
- `structural_index_v2` now carries symbol anchors, build ownership edges, function-level call edges, and overlay edges instead of staying file-centric
- `trace`, `impact`, and `security` retrieval now expand graph neighborhoods and emit `build_context_v2` plus `path_v2` evidence
- Unreal project, module, target, type, network, asset, system, and config signals are lifted into structured analysis artifacts
- A semantic shard planner plus semantic-aware worker and reviewer prompts prioritize startup, network, UI, GAS, asset/config, and integrity surfaces
- In addition to a knowledge pack, the pipeline now emits a structural index, `structural_index_v2`, Unreal semantic graph, vector corpus, and vector ingestion exports
- The pipeline also emits `architecture_facts.json`, a deterministic fact pack with top-level directory facts, domain hints, source anchors, registration/dispatch flow facts, boundary facts, and invariants used by cached architecture Q&A
- Generated docs and `dashboard.html` make the latest project knowledge base browsable as a module/function structure map plus a dark static document portal with search across the final report and generated docs, source anchors, graph-linked stale section diff, trust-boundary/attack-flow views, evidence/memory drilldowns, and docs-backed vector corpus reuse
- The dashboard includes an inline Markdown viewer and full-window reader mode so long outputs such as `FINAL_REPORT.md` can be read without leaving the dashboard
- Explicit language requests such as "write the report in English" override the detected conversation language for analysis worker and synthesis prompts, and live progress truncation is UTF-8 safe
- Before the final handoff, Kernforge prints a highlighted `Analysis artifacts:` block with the report, JSON, dashboard, docs, and manifest paths so users do not need to scroll back through a long run
- After analysis, Kernforge prints an `Analysis handoff` that points to `/analyze-dashboard`, `/fuzz-campaign run`, a top `/fuzz-func ...` drilldown, or `/verify` when the generated docs support that next step
- The source-anchor parser now handles modern C++ patterns such as template out-of-line methods, operators, `requires` and `decltype(auto)` headers, API-macro-wrapped scopes, and friend functions
- Security-mode final documents now add a `Security Surface Decomposition` section so privileged and abuse-sensitive paths do not get flattened into a generic summary
- Dedicated worker and reviewer models can be configured separately from the main chat model
- Architecture knowledge packs and performance lenses are written under `.kernforge/analysis`
- `.kernforge/analysis/latest` is replaced on each successful persistence pass so stale files from older runs do not leak into cached retrieval
- `/analyze-dashboard [latest|path]` opens the latest or a selected analysis document portal
- `/docs-refresh` regenerates the latest operational docs, dashboard, and docs-backed vector corpus deterministically from the saved analysis run
- `/analyze-performance [focus]` uses the latest analysis artifacts to reason about hot paths and bottlenecks
- Performance reports now end with a `Performance handoff` toward `/analyze-dashboard`, `/verify`, `/simulate stealth-surface`, or a concrete `/fuzz-func ...` hotspot drilldown

### Root-Cause Investigation

- `/find-root-cause <problem description>` accepts natural-language symptoms such as party-size limits being bypassed, `sc stop` not terminating a service, or a requested document artifact being missing
- If the prompt is too short or does not clearly identify the affected component, trigger/repro path, observed failure, or expected invariant, Kernforge does not start agents; it prints the unclear parts plus a better `/find-root-cause ...` command shape
- Borderline prompts are checked with source hints and an optional model clarity pass so Korean natural-language reports are not rejected only because a keyword heuristic missed them
- Kernforge scans the workspace, combines symptom keywords, source paths, indexed symbols, and built-in pattern priors, then splits likely source areas into 1-8 worker shards based on code size and candidate count; concurrent model calls still follow `model_routes`
- Workers inspect each assigned area like a fuzzing investigation: input parameters, decoded payloads, DB/config values, cached state, counters, ids, enums, nullable references, and lifecycle state may be outside the code's expected range
- Worker candidates must preserve a `trigger -> invalid_state -> state_transition -> missing_guard -> user_visible_symptom` causal chain
- Reviewer passes verify whether a worker-reported issue can actually lead to the user's symptom, and request additional focused shards when proof is missing
- Deterministic quality gates downgrade or reject candidates that lack causal stages, evidence files, concrete state signals, valid probes, or symptom overlap
- Reviewer-approved candidates receive another symbol-aware deep verification pass with focused source excerpts before final synthesis
- The final report summarizes plausible root causes, confidence breakdowns, evidence files/functions, instrumentation, verification probes, and disproof conditions
- Artifacts are written with the normal analysis outputs under `.kernforge/analysis/<run-id>/` and `latest`, including `root_cause_audit.md/json`
- `/root-cause-patterns list|match|github-search|normalize|validate` supports built-in pattern inspection, workspace/symptom matching, and GitHub issue based provisional pack generation. Pattern packs are search priors only; current source evidence and reviewer causality validation are still required

### Security Verification And Policy Loop

- Security-aware verification for driver, telemetry, Unreal, and memory-scan changes
- Verification history and verification dashboards
- `/verify` now ends with a `Verification handoff`: failures point back to repair/retry dashboards, while passing runs suggest checkpointing and either feature status or close depending on tracked feature state; native fuzz findings are pulled into targeted planner steps
- `/recover execute-safe` treats failed `/verify` reports and non-ready `/completion-audit` artifacts as failed recovery actions instead of marking a dispatched slash command as complete
- Safe recovery shell replay is limited to narrow Go/Git verification and status commands, rejects shell chaining/redirection, and blocks high-risk flags such as external test executors, vet tools, and output-writing diff/profile options
- Structured evidence capture from verification
- Evidence search and evidence dashboards
- `/investigate` and `/simulate` now print handoffs into snapshots, risk simulation, `/verify`, and evidence dashboards so the user does not need to memorize the analysis loop
- Evidence and memory views now print handoffs back into `/verify`, source dashboards, `/mem-confirm`, `/mem-promote`, or dashboard review when records need action
- Checkpoints, tracked features, isolated worktrees, and task-owner assignments now give short follow-up hints for diff review, implementation, cleanup, preservation, and fuzz verification gates
- Runtime toggle for automatic verification with `/set-auto-verify [on|off]`
- Windows verification tool path detection and overrides with `/detect-verification-tools` and `/set-*-path`
- Hook-based push and PR warnings, confirmations, and blocks based on recent failed evidence
- Automatic safety checkpoint creation for repeated high-risk failure patterns

### Autonomous Goals

- `/goal <objective>` or `/goal start <objective>` creates a persistent goal and immediately starts an autonomous loop
- `/goal start @GOAL.md` and `kernforge -goal-file GOAL.md` load the objective from a markdown file
- `kernforge -goal "..."` runs the same loop without entering the REPL, with matching `-goal-max-iterations`, `-goal-time-budget`, `-goal-token-budget`, and `-goal-rollback-on-regression` controls
- Each iteration asks the agent to inspect, develop, modify, review its own changes, run final semantic goal review, and fix bugs without asking the user for confirmation
- The runtime now binds each goal to an acceptance contract, task graph, independent review verdict, progress ledger, command history, and per-iteration checkpoint when checkpoint storage is configured
- Goal reviewers receive concrete workspace evidence, including implementation replies, checkpoint diffs when available, git status/diff context, and bounded untracked-file excerpts. If review says `NEEDS_REVISION`, the repair prompt preserves structured reviewer issues and the same implementation context so the worker can act on the actual findings instead of a vague summary.
- Kernforge then runs `/verify --full`, `/completion-audit`, final semantic review, and if needed `/recover execute-safe` before the next iteration
- The loop stops only when the completion audit is ready and final semantic review approves, the goal is canceled, or an unrecoverable blocker such as provider failure, explicit token/time/iteration cap, repeated failure signature, or no-progress loop is recorded
- Goal state and history are written under `.kernforge/goals/latest.md` and `.kernforge/goals/latest.json`, with per-goal copies for later audit
- Goals run until completion by default. Use `--max-iterations N`, `--time-budget 10m`, `--token-budget N`, `--rollback-on-regression`, or `--no-rollback` to tune autonomous stop and recovery policy
- `/goal status`, `/goal audit`, `/goal complete`, `/goal run`, and `/goal cancel` inspect, re-audit, explicitly complete, resume, or stop the active goal

### Source-Level Function Fuzzing

- Start function-level or file-level source fuzzing with `/fuzz-func <function-name>`, `/fuzz-func <function-name> --file <path>`, or `/fuzz-func @<path>`
- When you pass only `@<path>` or `--file <path>`, Kernforge expands from the starting file through include/import plus real call flow and picks a representative root automatically
- Planning no longer depends on `analyze-project` or a prebuilt `structural_index_v2`; Kernforge can scan the workspace and rebuild the semantic index on demand
- The default mode is AI source-only fuzzing rather than native execution, so Kernforge derives attacker input states, concrete sample values, branch predicates, minimal counterexamples, branch deltas, and downstream call chains directly from source
- By default, `/fuzz-func` reuses a matching `/source-scan` candidate or runs a focused source scan over the target and reachable files before saving the plan; candidates now carry function-window evidence spans, file/symbol fingerprints, confidence breakdown, dataflow/control-flow facts, and stale-source state
- Use `/source-scan run` to persist ranked source candidates first, then continue with `/fuzz-func --from-candidate <candidate-id>` when you want an explicit candidate handoff; built-ins include Windows kernel double-fetch, IOCTL output infoleak, WDF buffer size drift, integer allocation overflow, and pool/refcount lifetime matchers
- Use `/create-driver-poc <driver-name> [--type objectfilter|minifilter|registryfilter|wfpcallout]` to generate an x64-only C++20 MSVC/WDK POC driver solution with `Driver.cpp`, a namespace/constexpr shared communication header for service/device/IOCTL names and `DeviceType`, no INF package, and a same-directory `<driver-name>-tester.exe`. Omitting `--type` keeps the original SCM load/open/IOCTL ping/unload loop; typed templates add object manager process/thread handle filtering, a filesystem minifilter open/rename/delete user-mode decision path, registry create/open/set/delete/rename callback filtering, or WFP outbound callout blocking contracts.
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
- Automatic verification after edits when applicable, with build/test confirmation using the same pinned `y/N/a` approval contract as diff preview
- Each edit request keeps an apply/verify/retry ledger so main edits, worker edits, patch transaction ids, background verification bundles, verification reports, retry decisions, final-review verdicts, and remaining risk stay tied together through compaction and final handoff
- Simple edits can go through `apply_edit_proposal`, a structured proposal tool that records file, operation, exact search, replacement/content, rationale, risk, preview fingerprint, and review evidence before writing. `apply_patch` remains the expert fallback for complex hunk-level edits after current file contents have been read.
- Malformed patches are normalized for common wrapper problems such as BOMs, CRLF/CR line endings, surrounding prose, code fences, and quoted paths. If the same invalid patch signature repeats, the agent is steered to reread the target file and generate a new patch instead of retrying the same text.
- After stale-context recovery signals such as edit target mismatch or a blocked pre-write review, broad recovery patches are deferred before they touch the workspace. A blocked pre-write proposal is treated as non-committed state: the next edit tool is blocked until the model re-anchors with `read_file`, `grep`, or `git_diff`, then reissues a narrow standalone patch instead of bundling multiple unrelated hunks or submitting a delta against the rejected proposal.
- Pre-write review stores `edit_proposals` in the review artifact and MCP response, so approved proposal intent, raw preview, changed paths, and freshness checks stay connected.
- Runtime gate freshness blocks or warns on stale reviews, unwaived review blockers, failed verification, or missing review coverage before final answers, git writes, MCP write-side actions, and completion audit handoff.
- `/status` now defaults to a compact operator card: request class, classification confidence and ambiguity, current lifecycle phase, route mode, reviewer route quality, review/repair/document/verification/final-answer gates, second-pass state, cross-review triage counts, blockers by class, remaining obligations, and the next recommended command.
- Use `/status detail` for evidence-heavy output. Detail mode expands the lifecycle timeline across `classified_request`, `collecting_context`, `pre_write_review`, `applying_change`, `post_change_review`, `single_model_second_pass`, `cross_review_triage`, `artifact_quality_gate`, `verification`, `final_answer_contract`, `blocked`, and `completed`, with status, reason, evidence reference, and the next safe action when applicable.
- Operator progress lines are phase-aware. Long waits identify whether Kernforge is waiting on the primary model, cross reviewer, local tool, verification command, or final-answer correction, and repeated status spam is suppressed.
- Blocked states are rendered blocker-first. Primary blockers are grouped as `code_repair_blocker`, `reviewer_route_problem`, `evidence_gap`, `verification_gap`, `document_artifact_quality`, `final_answer_contract`, or `user_decision_required`, and each blocker carries why it blocks, what was checked, what can safely happen next, and the command to run.
- MCP review/status responses keep backward-compatible fields and add operator-card fields: `lifecycle_timeline`, `compact_status`, `blocker_summary`, `route_quality`, `final_answer_contract_status`, and `next_recommended_command`. These surfaces do not dump raw model output.
- Review quality gates retry provider-specific omission patterns, downgrade weak or incomplete high-severity model findings to evidence-gap warnings, and require path or symbol plus evidence, impact, and required fix before a model finding can block the gate. When the reviewer supplies a line anchor through `path: file:line` or `line: n`, Kernforge preserves it as structured finding metadata instead of leaving it buried in prose. `test_gap` remains non-blocking only for pure test/verification work; if a reviewer mislabels a production-code repair as `test_gap` but the `required_fix` is an implementation change, the repair plan still keeps it as an actionable obligation.
- Pre-fix review findings are summarized visibly before patch/write tools run, so the transcript shows which RF items drove the repair.
- Local code repair blocks web/search/browser MCP tools unless the user explicitly asks for external research.
- Before a final answer, the coding harness and `/completion-audit` check the acceptance contract, actual changed paths, requested artifact existence, artifact content quality, scenario replay state, subagent/reviewer evidence, test impact, open tasks, verification, background job state, and required completion facts
- The final-answer reviewer receives the edit-loop ledger plus a typed outcome contract and expects one coherent summary of what changed, what review/self-review found, what verified or was not run, and what risk remains. For edit and local-review lifecycles, missing changed-file, review, validation, or remaining-risk disclosure is corrected internally before the answer is exposed.
- Single-model mode is shown as first-class state: `single_model_second_pass_ran`, `single_model_second_pass_cached`, or `single_model_second_pass_skipped`. Cross-model review is separately labeled as `cross_model_review_ran`, and reviewer-only post-change review is labeled `reviewer_only_post_change_review_used`; a single-model second pass is never presented as independent cross-review.
- If a requested document or report artifact is placeholder/TODO content or does not cover the requested topic, the artifact-quality gate blocks the final answer
- Document-only flows show the artifact path, artifact-quality status, source-review evidence status when relevant, and the explicit reason build/test verification was skipped or required. A corrected document-artifact final answer is reported as a final-answer contract correction, not as code repair.
- If the user provided a bug scenario with trigger/expected/observed behavior, a code-changing fix claim must include replay/verification evidence or explicitly disclose that the replay was not run
- Root-cause answers are blocked when worker evidence does not show a causal bridge to the user-visible symptom or when reviewer issues are hidden
- After verification failures, the failure-repair harness keeps the first meaningful failure line, repeated count, narrow rerun command, and next repair steps in active context
- If a user changes a target file between tool calls, user-change isolation blocks overwrites and requires a fresh read plus merge-aware edit
- `read_file` now reuses unchanged exact ranges, covered subranges, and partial overlaps so large-file edit loops avoid redundant rereads
- `grep` now annotates matches with `[cached-nearby:inside]` or `[cached-nearby:N]` when recent `read_file` context already covers the same area or a nearby span
- Repeated same-file `read_file` turns now prefer cache-aware nudges before falling back to hard repeated-tool aborts
- `a` on `Allow write?` enables write auto-approval for the session only
- `a` on `Open diff preview?` auto-accepts the current edit and future diff previews for the session. A pre-armed one-shot preview approval is consumed before Kernforge asks again, so scripted or recovered preview decisions do not need a second confirmation read.
- Git-mutating tools such as `git_add`, `git_commit`, `git_push`, and `git_create_pr` use a separate session-scoped `Allow git?` approval path
- Git-mutating tools are intended for explicit user requests, not normal review or edit turns
- Automatic checkpoint creation before the first edit in a request
- Manual checkpoints, checkpoint diff, and rollback
- Selection-first edit and review flow through `/open`
- In ordinary product development, `implementation-owner` is the default editable task owner, while narrower domain owners such as `driver-build-fixer`, `telemetry-analyst`, `unreal-integrity-analyst`, and `memory-inspection-analyst` take ownership only when the task or paths match strongly.
- `apply_edit_proposal`, `apply_patch`, `write_file`, `replace_in_file`, and scoped shell writes follow node ownership and lease routing into the assigned task-owner worktree.
- `/specialists assign <node-id> <owner-profile> [glob,glob2]` lets you pin an editable task owner and override ownership globs when auto-routing picked a broader default.
- `/set-specialist-model <owner-profile> <provider> [model]` pins the LLM used by one task owner in this workspace, and `/set-specialist-model clear <owner-profile|all>` removes that override.
- Secondary edit nodes with disjoint leases can run through automatic editable workers, while overlapping leases are deferred instead of racing on the same files.
- When a parallel task-owner edit restarts verification, older background verification bundles for the same owner or same lease are superseded automatically, and verification-like bundle completion closes the owning node.

### Tracked Feature Workflow

- `/new-feature <task>` creates a tracked feature workspace and writes `spec.md`, `plan.md`, and `tasks.md`
- Tracked feature artifacts live under `.kernforge/features/<id>` so large work can survive across sessions
- `/new-feature status|plan|implement|close [id]` lets you inspect, regenerate, execute, and finish the active feature; status also surfaces recent fuzz campaign gates when native results exist
- `/review plan <task>` remains the better fit for one-shot reviewed planning and immediate execution

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
- Deep project-structure questions use a compact answer pack built from generated docs, `architecture_facts.json`, and current-source probes; cached answers are checked against deterministic facts before Kernforge decides whether it can answer without tools
- When cached analysis is sufficient for a question, Kernforge can answer directly from that summary without extra tool calls
- Cached `read_file` NOTE results are now treated as a signal that the relevant lines were already seen, which helps reduce large-file reread loops
- `grep` cache-nearby hints help the model choose smaller follow-up `read_file` ranges around fresh unmatched lines instead of rescanning a wide block
- Prompts that ask to analyze, explain, diagnose, or document default to read-only investigation mode
- Prompts that explicitly ask to fix code stay tool-driven and Kernforge nudges the model away from handing patches back to the user

### Interactive Ergonomics

- `Tab` completion for commands, paths, mentions, MCP targets, fixed command arguments, provider subcommands such as `/provider status|openai-codex-subscription|openai-codex-cli|openai-api|anthropic-claude-cli|anthropic-api|deepseek|openrouter|opencode|opencode-go|ollama|lmstudio|vllm|llama.cpp`, analyze-project modes, compact fuzz campaign actions, `/create-driver-poc <driver-name>`, `/find-root-cause`, `/root-cause-patterns list|match|github-search|normalize|validate`, and saved ids or subcommands such as `/resume`, `/mem-show`, `/evidence-show`, `/investigate show`, `/simulate show`, `/fuzz-campaign run|show`, `/new-feature status|plan|implement|close`, `/jobs status|check|bundle|cancel|cancel-bundle`, `/specialists status|assign|cleanup`, and `/worktree status|list|create|enter|attach|leave|cleanup`
- Completion menus now show inline descriptions for commands and common subcommands instead of listing names only
- `Esc` to cancel current input
- `Esc` to cancel an in-flight request
- Pressing `Enter` on an empty main prompt is ignored so the REPL does not create empty turns
- The REPL uses a compact branded banner, subtle turn dividers, grouped status/config sections, and separate assistant versus tool activity streams for denser terminal UX
- Assistant streaming output now suppresses leading blank chunks, flushes cleanly before progress lines, and inserts line breaks between repeated follow-on preambles
- Short tool-turn narration such as "let me inspect" or similar Korean preambles is buffered and collapsed into footer-style progress instead of spawning extra assistant transcript blocks
- Generic waiting text is collapsed so the thinking indicator does not repeat the same message twice
- Thinking elapsed time is rebased at phase boundaries and stale runaway timer displays are clamped at the 2-hour mark
- Repeated blank streamed chunks are replaced with a compact working status instead of emitting empty lines
- `progress_display` controls in-flight visibility and defaults to `stream` so long-running work leaves an auditable progress transcript. Change it from the REPL with `/progress-display auto|compact|stream`; `/progress_display ...` is accepted as an alias for users copying the config key. `auto` preserves important tool/model/route and project-analysis progress as transcript ledger lines, `compact` keeps them in the footer, and `stream` writes every progress update persistently
- Provider, tool, and command failures are mirrored to `.kernforge/logs/errors.jsonl` as capped JSONL; Kernforge keeps that file at or below 100 MB so retry-only provider failures remain debuggable after the UI has moved on
- OpenAI-compatible and OpenAI Codex streaming providers surface tool-call construction events, so the REPL can show when the model is preparing a tool and when the arguments are ready
- Progress-only model streams keep the same incomplete-stream fallback behavior as normal assistant streams; if a streamed OpenAI-compatible response is empty or incomplete, the retry is forced back through the non-stream path instead of re-entering streaming only because progress events are enabled
- High-frequency shell output and heartbeat updates stay transient in `auto` mode, while durable tool start/result/retry events remain visible in the main transcript
- Confirmation prompts such as cancel, diff preview, write approval, and verification recovery temporarily take over that same footer slot so they stay visually pinned at the bottom
- Persistent results such as completion summaries, output paths, warnings, and configuration changes remain in the main transcript while ephemeral progress stays in the footer or progress ledger depending on `progress_display`
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
go build -o kernforge.exe ./cmd/kernforge
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

Interactive provider pickers and `/model cross-review` use this user-facing order: `openai-codex-subscription`, `openai-codex-cli`, `openai-api`, `anthropic-claude-cli`, `anthropic-api`, `DeepSeek`, `openrouter`, `OpenCode Zen`, `OpenCode Go`, `ollama`, `LM Studio`, `vLLM`, `llama.cpp`.

Anthropic:

```powershell
$env:ANTHROPIC_API_KEY = "your_key"
.\kernforge.exe -provider anthropic -model claude-sonnet-4
```

Anthropic Claude CLI:

```powershell
.\kernforge.exe -provider anthropic-claude-cli -model claude-sonnet-4
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

DeepSeek:

```powershell
$env:DEEPSEEK_API_KEY = "your_key"
.\kernforge.exe -provider deepseek -model deepseek-v4-pro
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

OpenCode Zen:

```powershell
$env:OPENCODE_API_KEY = "your_key"
.\kernforge.exe -provider opencode -model opencode/gpt-5.5
```

OpenCode Go:

```powershell
$env:OPENCODE_GO_API_KEY = "your_key"
.\kernforge.exe -provider opencode-go -model opencode-go/deepseek-v4-pro
```

OpenAI Codex CLI:

```powershell
.\kernforge.exe -provider codex-cli -model gpt-5.5-pro
```

Inside the interactive REPL, use `/provider status` to inspect the active provider, normalized `base_url`, API key presence, and provider-specific budget visibility.

LM Studio:

```powershell
.\kernforge.exe -provider lmstudio -model local-model-id
```

vLLM:

```powershell
.\kernforge.exe -provider vllm -model local-model-id
```

llama.cpp:

```powershell
.\kernforge.exe -provider llama.cpp -model local-model-id
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
5. `/review selection integrity risk paths`
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

### Task Ownership Profiles And Worktree Isolation Example

`task_ownership` is the current config key for task ownership profiles. The `/specialists` command namespace and legacy `specialists` config key still work for compatibility. Task ownership is enabled by default, while `worktree_isolation` is off by default. The combination is especially valuable for tracked feature execution, high-risk driver/telemetry/Unreal/memory changes, and any request that touches multiple ownership domains in one turn.

For most ordinary web, backend, tooling, and application development, `implementation-owner` owns edits by default and `planner` handles read-only planning evidence. Driver, telemetry, Unreal, and memory owners activate only when task text or file paths strongly match those domains, or when you pin them manually with `/specialists assign`.

```json
{
  "auto_verify": true,
  "task_ownership": {
    "enabled": true,
    "profiles": []
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
3. Auto-routing picked the broad default `implementation-owner`, but you want to pin a narrower domain owner for one node.
4. You are iterating on the same file repeatedly and do not want to manually clean up stale background verification bundles between attempts.

Recommended flow 1: let Kernforge auto-assign for ordinary feature work

1. Turn on `worktree_isolation.enabled=true` in `.kernforge/config.json`.
2. Run `/new-feature start settings page and cache invalidation cleanup`.
3. Run `/new-feature implement`.
4. Phrase the implementation request with concrete paths. Example: `Safely update web/src/settings.tsx and pkg/cache/store.go, and keep the settings save flow and cache invalidation verification tight.`
5. Kernforge will assign task owners per task-graph node, then attach editable ownership and lease paths to each node.
6. If the secondary edit nodes have disjoint leases, an automatic editable worker can create an additional patch in its own task-owner worktree.
7. If verification restarts for the same owner or same lease, the older background verification bundle is superseded automatically, so you do not keep following stale output.
8. Use `/tasks`, `/specialists status`, `/worktree status`, and `/verify-dashboard` to inspect routing and verification progress.
9. When the isolated worktree is clean and no longer needed, run `/worktree cleanup`.

Recommended flow 2: pin domain owners manually

1. Run `/tasks` to inspect the node ids.
2. Run `/specialists assign plan-02 driver-build-fixer driver/**,*.inf,*.cat`.
3. Run `/specialists assign plan-03 telemetry-analyst telemetry/**,*.man,*.xml`.
4. Continue the implementation request.
5. After that, edit tools and scoped shell writes are only allowed inside that node's ownership and task-owner worktree.
6. If you try to write outside the owned scope, Kernforge will return a reassignment hint instead of silently widening the boundary.

Recommended flow 3: use worktree isolation first

1. Run `/worktree create anti-cheat-hardening`.
2. Continue the usual review, edit, and verify loop.
3. Use `/worktree leave` if you want to go back to the base root without deleting the isolated tree yet.
4. Use `/worktree cleanup` after the tree is clean and you are done with it.

Practical tips:

1. If you want automatic parallel edit lanes, mention concrete paths such as `pkg/cache/store.go`, `web/src/settings.tsx`, or `Config/DefaultGame.ini` directly in the request.
2. If two edit nodes overlap on the same path or glob, Kernforge intentionally defers the secondary lane and falls back to serial execution.
3. `task_ownership.profiles` can override built-in owner profiles. The legacy `specialists.profiles` key is still accepted. This is useful when you want a stronger model only for `telemetry-analyst`, or when `driver-build-fixer` should also own `package/**`.

Task-owner profile overlay shape:

```json
{
  "task_ownership": {
    "enabled": true,
    "profiles": [
      {
        "name": "telemetry-analyst",
        "provider": "openai-codex",
        "model": "gpt-5.5",
        "reasoning_effort": "high",
        "node_kinds": ["edit", "verification"],
        "keywords": ["etw", "manifest", "provider"],
        "editable": true,
        "ownership_paths": ["telemetry/**", "*.man", "*.xml"]
      }
    ]
  }
}
```

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
- `/specialists assign <node-id> <owner-profile> [glob,glob2]`
- `/set-specialist-model <owner-profile> <provider> [model]`
- `/worktree status`
- `/worktree list`
- `/worktree create [name]`
- `/worktree enter`
- `/worktree attach <path> [branch]`
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
| `-command "<slash-command>"` | Run a single slash command or `!shell` command and exit |
| `-goal "<objective>"` / `-goal-file <path>` | Run an autonomous goal loop from inline text or a markdown file |
| `-image <paths>` / `-i` | Attach one or more images in one-shot mode, comma-separated |
| `-resume <session-id>` | Resume a saved session |
| `-permission-mode <mode>` | Set the permission mode: `default`, `acceptEdits`, `plan`, or `bypassPermissions` |
| `-y` | Auto-approve all permissions (`bypassPermissions`) |
| `-mcp-server` | Run Kernforge as a stdio MCP server |
| `-mcp-daemon-proxy` | Proxy stdio MCP requests through the local Kernforge daemon |
| `--version`, `-version`, `version` | Print the Kernforge executable version and exit |
| `-h`, `--help`, `help` | Show standalone, one-shot, MCP server, and daemon usage |

Notes:

- Run `kernforge --help`, `kernforge help mcp`, or `kernforge help daemon` to see launch examples before config is loaded.
- On Windows release builds, `--version` and the first line of `--help` read the PE `FileVersion` stamped into `kernforge.exe`; non-stamped developer builds fall back to the embedded app version.
- `-image` requires `-prompt`.
- `-command` is intended for automation and external schedulers, for example `-command "/automation monitor --notify"`.
- `-prompt`, `-command`, `-goal`, and `-goal-file` are single-shot modes. They do not install the ambient request-cancel keyboard watcher; only explicit confirmation prompts such as diff preview, write approval, or automatic verification consume stdin.
- Most MCP users should configure only `kernforge -mcp-server -cwd <workspace>`. That starts Kernforge as the MCP stdio server for the selected workspace.
- `-mcp-daemon-proxy` is an advanced shared-daemon mode for setups where multiple MCP clients should reuse one local Kernforge daemon. In that mode, `kernforge -mcp-server -mcp-daemon-proxy -cwd <workspace>` is still the MCP client's stdio command, but the short-lived proxy forwards requests to the already-running daemon.
- `-preview-file`, `-preview-result-file`, `-viewer-file`, and `-viewer-result-file` are internal window helper flags.

## Workspace And Configuration

### Workspace Root vs Current Directory

Kernforge tracks:

- The workspace root
- The current working directory inside the REPL

The workspace root is set from `-cwd` or the process working directory at startup. File tools stay within that root.

Inside the REPL, `!cd` changes the current working directory, but it does not expand the workspace boundary.
If the current directory is already inside the workspace, `!cd ..` can move back up through parent directories until it reaches the workspace root. Attempts to move above the workspace root are rejected. In an active managed worktree, the worktree root is the navigation boundary.
Relative-path read and search tools look in the current directory first, then fall back to the workspace root if the target is not found there.

### Config Locations

- Global config: `~/.kernforge/config.json`
- Workspace config: `.kernforge/config.json`

### Merge Order

Later sources override earlier ones:

1. Global config
2. Workspace config, for trusted projects only
3. Environment variables
4. Command-line flags

Workspace config is repository content. Kernforge therefore ignores `.kernforge/config.json` and `.kernforge/hooks.json` until the current project is explicitly trusted from user config. Use `/trust on`, or add a user-level entry like this to `~/.kernforge/config.json`:

```json
{
  "projects": {
    "F:/kernullist/kernforge": {
      "trust_level": "trusted"
    }
  }
}
```

Project-local config cannot mark itself trusted. Even after a project is trusted, Kernforge does not let workspace config choose where credentials are sent or which host-local executables are run. The following keys are ignored when they appear in `.kernforge/config.json`: `provider`, `base_url`, `api_key`, `provider_keys`, `codex_cli_path`, `codex_cli_args`, `claude_cli_path`, `claude_cli_args`, `permission_mode`, `shell`, `session_dir`, `mcp_servers`, `active_profile_key`, `model_routes`, `projects`, `hooks_enabled`, `hook_presets`, and `hooks_fail_closed`. Put those in `~/.kernforge/config.json`, environment variables, or an explicit runtime command instead. Trusted workspace config remains suitable for project verification paths, `auto_verify`, skill paths, specialist overlays, worktree isolation, and saved profile lists.

### Example Config

```json
{
  "provider": "ollama",
  "model": "qwen3.5:14b",
  "base_url": "http://localhost:11434",
  "permission_mode": "default",
  "shell": "powershell",
  "request_timeout_seconds": 1200,
  "shell_timeout_seconds": 900,
  "progress_display": "stream",
  "max_tokens": 8192,
  "model_routes": {
    "enabled": true,
    "default_max_concurrent": 4,
    "provider_limits": {
      "ollama": 1,
      "lmstudio": 1,
      "vllm": 1,
      "llama.cpp": 1,
      "opencode": 1,
      "opencode-go": 1,
      "deepseek": 2,
      "openrouter": 2,
      "openai-codex": 2,
      "codex-cli": 1
    }
  },
  "max_tool_iterations": 0,
  "auto_compact_chars": 45000,
  "auto_checkpoint_edits": true,
  "auto_verify": true,
  "task_ownership": {
    "enabled": true,
    "profiles": []
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
| `provider` | `openai-codex-subscription`, `openai-codex-cli`, `openai-api`, `anthropic-claude-cli`, `anthropic-api`, `DeepSeek`, `openrouter`, `opencode`, `opencode-go`, `ollama`, `lmstudio`, `vllm`, `llama.cpp`, plus `openai-compatible` |
| `model` | Model name sent to the provider |
| `base_url` | Provider API base URL |
| `api_key` | API key |
| `temperature` | Model temperature |
| `reasoning_effort` | Optional OpenAI Codex and DeepSeek reasoning effort for the active main model: `minimal`, `low`, `medium`, `high`, or `xhigh`; unset is shown as `undefined`. Saved profiles, review profiles, analysis role profiles, and explicit task-owner model overrides can each store their own `reasoning_effort`. DeepSeek maps `minimal`/`low`/`medium`/`high` to `high` and `xhigh` to `max`. |
| `max_tokens` | Max completion tokens. Default is `8192` |
| `max_request_retries` | Retry count for transient provider errors or timed-out model requests |
| `request_retry_delay_ms` | Base backoff delay in milliseconds before retrying model requests |
| `request_timeout_seconds` | Per-request model timeout in seconds |
| `progress_display` | Runtime progress style. Default `stream` writes every progress update into the transcript for long-run debugging; `auto` keeps durable tool/model/project-analysis ledger lines while high-frequency shell output stays in the footer; `compact` keeps progress transient |
| `model_routes` | Per-route model concurrency limits keyed by provider/model/base_url/reasoning_effort. Local providers default to serial execution, while cloud/API routes follow the configured provider or route limit. |
| `max_tool_iterations` | Max tool loop count per request. `0` or any non-positive value means unlimited, and the default is `0` |
| `permission_mode` | `default`, `acceptEdits`, `plan`, `bypassPermissions` |
| `shell` | Shell used by `run_shell` |
| `shell_timeout_seconds` | Default timeout in seconds used by `run_shell`. Default is `900` |
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
| `mcp_servers` | MCP server definitions. Host-local only: ignored from workspace config |
| `profiles` | Saved recent or pinned provider/model profiles |
| `projects` | User-level project trust map. Ignored from workspace config |
| `hooks_enabled` | Enable or disable the hook engine. Host policy only: ignored from workspace config |
| `hook_presets` | Hook preset names. Host policy only: ignored from workspace config |
| `hooks_fail_closed` | Block when hook evaluation fails instead of allowing by default. Host policy only: ignored from workspace config |
| `project_analysis` | Multi-agent project analysis configuration, output path, and worker/reviewer profiles |
| `review` | Common review harness automation settings and the optional cross-review route |
| `task_ownership` | Enable task ownership profiles and overlay built-in owner profiles. Legacy `specialists` is accepted as a compatibility alias |
| `worktree_isolation` | Configure isolated git worktree roots, branch prefixes, and tracked-feature auto-isolation |

Saved main profiles store task-owner model overrides under `profiles[].role_models.task_owners`. Legacy `profiles[].role_models.specialists` is still accepted on load.

Cross-review, analysis worker/reviewer, and task-owner `base_url` values are optional. When a route uses the same provider as the main model and leaves `base_url` empty, it inherits the main normalized endpoint; when it uses a different provider, Kernforge uses that provider's default endpoint unless the route sets its own `base_url`.
On startup and `/reload`, Kernforge migrates config files that still hold the old literal defaults `max_tool_iterations: 16` or `max_tokens: 4096` to `0` (unlimited) and `8192`, then prints a one-time `INFO` notice. Other explicitly chosen values are preserved; if you intentionally want the old numbers, set them again after the notice.
`project_analysis.max_files_per_shard`, `project_analysis.max_lines_per_shard`, and `project_analysis.max_total_shards` can be set explicitly for deterministic sizing. Leaving them unset lets Kernforge apply local-model adaptive sizing and the smaller-shard recovery retry described above. `project_analysis.max_provider_retries` may be set to `-1` to disable per-request provider retries.

### Interactive Loop Durability Notes

- The interactive loop uses the active main model as the first-pass reviewer for `/review`, natural-language review, and pre-fix repair checks. `/model cross-review` configures the optional second-pass cross reviewer route for those flows, while pre-write review keeps the stricter required-reviewer edit gate. Security, design, false-positive, regression, test, and final-gate specialization is applied as review lenses inside the same route. If that gate stops because the cross reviewer failed but the main pre-write review was usable, Kernforge shows the main-review-based fallback phrase; only an explicit user approval plus the normal diff preview confirmation can continue.
- `/status`, `/hooks`, and the session dashboard now expose the latest review decision as an operations summary, not just a pass/fail line. The compact view shows the latest review id, request class, classification confidence and ambiguity, lifecycle phase, trigger, target, mode, route quality, gate verdict/action, review/repair/document/verification gate status, single-model second-pass status and cache hit, cross-review triage counts, incomplete triage blockers, remaining repair/verification/evidence obligations, final-answer correction status, and the next recommended command. Blocker counts are classified as code repair, reviewer route, evidence gap, verification gap, document artifact quality, and final-answer completeness so cross-review noise does not hide a primary code blocker.
- Review artifacts render a dedicated single-model second-pass section with ran/skipped/cached state, reviewed paths, model route, finding count, and prompt/raw-output artifact refs when present. A skipped second pass records a compact reason, and a completed single-model second pass is still labeled runtime evidence, not independent cross-review approval.
- Cross-review triage in Markdown is action oriented: each item carries finding id/title, location, status, reason, required fix, fix refs, changed paths, verification refs, and whether user action is required. `needs_user_decision` entries also surface concrete inspect targets, safe-to-change scope, do-not-change-yet scope, and a next command such as `/continuity continue from review` instead of burying the decision inside raw reviewer output.
- Pre-final answer corrections are persisted as lifecycle facts. Kernforge records whether the final answer was corrected for changed-file disclosure, review/self-review disclosure, validation disclosure, remaining-risk disclosure, review-only findings-first/no-edit formatting, or document-artifact path/quality/limitation disclosure; it does not expose internal correction prompts or accept generic legacy completion phrases as enough evidence.
- Focused review requests such as `@file:line-line review and fix` use a smaller evidence budget and prompt budget, while pre-write review stays diff-first so the reviewer spends its context on the proposed patch and the required repair findings instead of re-reading the whole file. For range-focused pre-write checks, current file context prioritizes the selected range through the enclosing function end, and a separate function-body excerpt preserves cleanup and success paths that reviewers need to judge the patch. Pre-write evidence also adds small required-repair diff excerpts and explicit planned-patch after excerpts keyed from the original RF titles/fixes, so a large diff cannot hide the exact after-preview lines needed to prove a multi-step repair.
- Common review model requests use bounded per-attempt timeouts unless a policy overrides them. Cloud API reviewers default to 5 minutes, while CLI reviewers such as `anthropic-claude-cli` and `openai-codex-cli` and local reviewers such as LM Studio/Ollama default to 8 minutes. If recent route health shows a timeout, only the next reviewer call receives an adaptive extension, and status/failure messages translate that condition into user actions: retry the same reviewer with more time, change the reviewer route, continue in single-model mode when acceptable, or change/fix the main model route when the primary failed. DeepSeek strict-review retries remain capped tightly, and progress logs show the current review phase, retry budget, context mode, and timeout before each model call.
- Natural-language review mode now follows the Codex App-style review boundary. Requests such as `리뷰 모드로 검토해`, `review mode`, or `수정은 하지 말고 리뷰 모드로 검토해` run the common review harness as a read-only code review and stop there; they do not enter the implementation loop or emit patch guidance. Review-mode prompts require concrete findings first, then a short summary. The original user text is used for intent and routing, while the enriched request with mention/runtime context is sent to the reviewer. Review-only mode can use the configured reviewer route even when the main chat client is unavailable. File mentions are normalized and must stay under the workspace root before evidence is collected. If the same request explicitly asks to fix or patch, Kernforge keeps the pre-fix review-and-repair flow instead.
- Review-route recovery now separates universal observability from local-model concessions. Every model route can persist a redacted raw provider response artifact next to the parsed raw review output, making empty/weak reviewer failures diagnosable. Local or degraded OpenAI-compatible routes such as LM Studio, vLLM, llama.cpp, and Ollama use a compact initial review prompt when the focused evidence is larger than their compact budget, and Kernforge can recover a `REVIEW_RESULT` block that a local provider returned through `reasoning_content` without weakening the strict GPT/Claude review schema path. If a local/degraded route writes only provider reasoning content and leaves final content empty, Kernforge performs one compact retry that explicitly requires a final `REVIEW_RESULT` block. If a local/degraded pre-fix review route still fails to produce actionable bug findings, Kernforge does not treat that as approval, but it can still hand the task to the implementation loop for independent source inspection; the normal pre-write gate remains strict.
- If a pre-write repair attempt still does not pass review, Kernforge stops before another broad loop, shows a short verdict -> latest review -> selected edit proposal -> `y/N` decision block, records a pending repair confirmation in the session, and asks `Should I keep repairing from this review result? [y/N]`. Only `y` resumes from the stored findings/proposal; `n` stops; natural-language answers are rejected so the next action is explicit. If the pre-write gate fails because a required review-stage model route returned empty or weak output, interactive runs now print the review/proposal summary and use a pinned runtime `Continue repairing? [y/N]` prompt like the diff-preview prompt only when there are actionable non-reviewer code findings. Choosing `y` continues the same turn from those code findings only; it never counts as write approval or a review bypass, and the next edit proposal must pass the normal pre-write gate again. When the failure contains only route/evidence-gap findings, Kernforge does not ask for `y/N`; it tells the user to inspect the failed route list. If `primary` failed, the active main model route is the problem and the user should use `/model` or fix that provider process such as LM Studio/Qwen. If `cross` failed, the user should use `/model cross-review`, `/model clear cross-review`, or restore that reviewer process. Recent route health is consumed before reviewer calls, so a route that just returned empty output, timed out, or produced weak structured output is skipped instead of being retried for another multi-minute wait. After any blocked pre-write proposal, the next edit must be a complete standalone patch for the current file, not a missing-piece delta on a patch that was never written. The decision block selects from edit proposals inside the current review boundary only: if the latest proposal is include-only while the latest review requires a non-include code patch, Kernforge shows the latest non-include proposal from that same boundary without inspecting source-level API semantics. If the only remaining warning is a harness evidence/after-preview visibility gap, Kernforge labels the RF status as `evidence_unconfirmed` and explains that diff preview is allowed because there is no confirmed unresolved code blocker.
- Local/degraded repair handoff now compresses duplicate RFs before the implementation model sees them. Findings that point at the same operation, variable, or failure path are merged, and placeholder items such as `stability issue -> inspect and address this finding` are downgraded out of the repair gate. When the pre-fix route produces only an evidence-gap warning, Kernforge does not synthesize replacement bug findings or repair contracts from source tokens. The implementation model receives the original user request, the local-route warning, and direct source-inspection instructions; the actual patch is then judged by the normal diff-first pre-write review using the proposed diff and after excerpt. Deterministic pre-write gates are limited to operational checks such as route failures, patch syntax, evidence packaging, and frozen diff/provenance checks; they do not classify source-level patch semantics. Edit-target mismatch recovery also tells the model to re-anchor one narrow standalone hunk instead of recovering with a broad function rewrite, and the stop reply includes the latest review basis plus the selected edit proposal.
- When one model response mixes an edit tool such as `apply_patch` with read-only inspection tools, Kernforge executes only safe inspect/plan tools in that turn. Edit tools, non-read-only tools, and cache-only shell commands are returned as `NOT_EXECUTED` and must be reissued in an isolated later response. This keeps local models from applying a patch while they are still asking for more file context, and it avoids hidden side effects from commands that look like verification but still mutate caches.
- The final-answer reviewer now runs only when there is unresolved verification, a coding-harness blocker, or actual patch transaction changed paths. Read-only replies, plan state, or task-graph presence alone no longer create an extra LLM round-trip. Runtime freshness for final/completion gates is scoped to the current patch transaction instead of unrelated dirty files, while content-hash mismatches still invalidate reviewed paths. Git write gates continue to account for the full dirty tree.
- Verification evidence is current only when it covers the current patch transaction. Session verification reports and persisted verification history that predate the latest patch or do not cover the changed paths are ignored for review blockers and shown as runtime-gate warnings with a `/verify --full` next action. Build/test shell commands that may write artifacts use the same pinned confirmation style as diff preview (`Run automatic verification now? [y/N/a=auto-run]`), so declining verification records a visible gap instead of silently approving or forcing a semantic rewrite loop. These commands follow a Codex App-style execution lifecycle in tool metadata: verification-like commands carry `verification_status` (`pending`, `passed`, `failed`, or `skipped`) plus `command_execution_status`; a background verification start is `pending` with `verification_evidence=false`, a completed zero-exit background check is `passed`, and a user-declined build is `skipped`/`declined`, never clears pending verification or counts as successful evidence. Same-turn verification retries or `latest` background polls after a declined verification are blocked as `NOT_EXECUTED` before emitting another shell/progress status. If automatic verification fails after an edit, Kernforge classifies the failing evidence against the current patch scope before asking the model to repair: only a direct command/scope reference or failure line that names the changed path can drive another repair loop, while ambient workspace or sibling-file failures are recorded as disclosed verification risk and must not expand the edit into unrelated project files. Once a failure is classified as out-of-scope, same-turn build/test/verification retry or probing calls are also blocked as `NOT_EXECUTED`; the model must summarize the external/ambient blocker instead of trying alternate verification commands. Build artifact churn from verification commands is tolerated, but source/config file mutations are still rejected outside the edit review gate. For MSBuild workspaces, adaptive verification first targets the nearest changed `.vcxproj`; when that project declares `ProjectConfiguration` entries, Kernforge passes the selected `/p:Configuration=...` and `/p:Platform=...` so MSBuild does not fall back to an unsupported default such as `Debug|Win32`. Full workspace solution builds remain part of explicit full verification. When a configured or detected verification tool path replaces a leading tool name on PowerShell, Kernforge emits a valid call-operator form such as `& "...\MSBuild.exe" "project.vcxproj" /m` instead of a quoted executable string that PowerShell would parse as a literal. Verification summaries retain both the logical command and the resolved shell command when they differ, and missing-tool classification is tied to the primary executable rather than arbitrary build output text.
- Non-bypass non-interactive `-prompt` runs still render the automatic verification plan and the same pinned confirmation label, and they honor piped `y`/`n`/`a` answers. Missing stdin or EOF is recorded as skipped/declined verification, not as a tool outage or implicit approval.
- External verification callbacks are normalized at the runtime boundary before review or runtime-gate freshness checks. If a callback returns a successful report without `GeneratedAt`, `Trigger`, `Workspace`, or `ChangedPaths`, Kernforge fills those fields from the current automatic verification request so the report is judged by the same current-patch freshness rules as built-in verification.
- After an out-of-scope automatic verification failure, the next model request is sent without tool definitions. This mirrors Codex's explicit terminal state boundary: the route can only produce a final answer, and any stale tool call is answered as `NOT_EXECUTED` without executing or prompting for shell/build approval. Once that final answer arrives, Kernforge discloses the verification risk and does not re-open post-change review or final-answer repair gates for the already-classified external blocker.
- Final-answer sanitization collapses accidental repeated sentence runs outside code fences and repairs adjacent Korean sentence spacing, so degraded/local routes cannot flood the transcript with the same final-risk sentence or jam two final sentences together.
- When a patch transaction exists, review/final/completion runtime gates evaluate that transaction's changed paths instead of every dirty file in the repository. Git write gates still use the full dirty worktree.
- The interactive runtime now keeps both a structured `TaskState` and a persisted `TaskGraph`, so goals, plan progress, pending checks, background ownership, and high-value events survive compaction more reliably than transcript-only state.
- The interactive runtime also persists an edit-loop ledger for apply/verify/retry/final-review state. It records changed paths, worker evidence, patch transaction ids, verification bundle/job/log evidence, retry decisions, reviewer verdict, and remaining risk, then exposes that ledger to the system prompt, final reviewer, `/status`, session export, and pre-final coding harness.
- Task-graph nodes now track retry budgets and recent failure context. Repeated failures on the same node can block that node explicitly, which pushes the executor toward a materially different recovery path instead of repeating the same failing step forever.
- `run_shell` now supports scoped workspace writes when the agent provides `allow_workspace_writes=true` together with `write_paths`. This path is intended for formatters, code generators, or setup commands that are safer to run than re-creating the change by hand.
- Long-running build, test, and verification commands can use `run_shell_background` and `check_shell_job` so the agent can poll an existing job instead of restarting the same expensive command. Matching running jobs are reused automatically.
- Independent long-running verification commands can also use `run_shell_bundle_background` and `check_shell_bundle` to run and poll several background jobs in parallel. Bundle metadata is persisted in the session, so the agent can resume polling with `bundle_id="latest"` even after compaction.
- `/jobs status|check|bundle|cancel|cancel-bundle` exposes those persisted background jobs and bundles to the terminal, so a human or a `-command` runner can poll or cancel long work without waiting for a model turn.
- `/events tail [n]` prints recent session events as JSONL, and `/events export [path]` writes `.kernforge/events/<session-id>.jsonl` plus `.kernforge/events/latest.jsonl` as a local app-server style feed for dashboards, schedulers, harnesses, and external supervisors.
- `/continuity [note]` writes `.kernforge/continuity/latest.md/json` with changed files, open tasks, worktrees, active failure repair, latest verification failure, recent shell/provider/tool errors, background jobs, recovery actions, and next commands. Direct `!shell` failures are recorded as `command_error` events so the packet can recover from local command failures without pasted logs.
- `/recover [note]` writes `.kernforge/recovery/latest.md/json` as a focused failure runbook from the latest provider/tool/command error, verification failure, active failure repair, background jobs, open tasks, and next commands. It now includes a structured diagnosis, stable failure signature, action-plan lifecycle statuses, and execution log. `/recover execute-safe [note]` runs only safe-auto recovery actions such as whitelisted `go test`, `go vet`, `go list`, `git status`, `git diff --check`, `/jobs`, `/continuity`, and `/completion-audit`.
- `/completion-audit [note]` writes `.kernforge/completion_audit/latest.md/json` with completion blockers, warnings, required artifacts, latest verification, open tasks, background jobs, recent errors, and coding harness evidence before a final answer overclaims done state.
- Background work is now node-aware. Long-running verification carries `owner_node_id` and owner lease context, newer verification bundles for the same owner or same lease supersede older ones, and verification-like bundle completion syncs back into the owning plan node automatically.
- Secondary executor nodes can now run not only automatic read-only worker follow-ups but also automatic editable workers. On disjoint leases, a specialist can patch in its own worktree and persist both the edit summary and follow-up verification bundle state back into the task graph.
- Normal work turns print a final elapsed-time line, while local meta commands such as `/exit`, `/status`, `/config`, and `/model` suppress that noise.

### Environment Variables

General overrides:

- `KERNFORGE_PROVIDER`
- `KERNFORGE_MODEL`
- `KERNFORGE_BASE_URL`
- `KERNFORGE_API_KEY`
- `KERNFORGE_REASONING_EFFORT`
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
- `DEEPSEEK_API_KEY`
- `OPENCODE_API_KEY`
- `OPENCODE_ZEN_API_KEY`
- `OPENCODE_GO_API_KEY`
- `OLLAMA_HOST`
- `OLLAMA_API_KEY`
- `KERNFORGE_CODEX_CLI_PATH`
- `KERNFORGE_CODEX_CLI_ARGS`
- `KERNFORGE_CLAUDE_CLI_PATH`
- `KERNFORGE_CLAUDE_CLI_ARGS`
- `KERNFORGE_CODEX_AUTH_FILE`
- `KERNFORGE_CODEX_ACCESS_TOKEN`

## Providers

Provider setup and review-route setup intentionally display stable user-facing labels even when the stored provider id is kept for compatibility. For example, `openai-codex-subscription` maps to the direct ChatGPT/Codex OAuth provider, `openai-codex-cli` maps to the installed `codex` command bridge, `openai-api` maps to the OpenAI API provider, and `anthropic-api` maps to the Anthropic API provider.

### Ollama

- Default base URL: `http://localhost:11434`
- Reads `OLLAMA_HOST` and `OLLAMA_API_KEY`
- Supports first-run local server detection
- Fetches model lists directly from the server

### Anthropic

- Default base URL: `https://api.anthropic.com`
- Reads `ANTHROPIC_API_KEY`
- `/provider status` shows Billing-page visibility plus the documented Usage & Cost Admin API limits instead of guessing a live standard-key balance endpoint

### Anthropic Claude CLI

- User-facing provider label: `anthropic-claude-cli`
- Uses the installed `claude` command as a local provider bridge instead of sending an Anthropic API key through Kernforge
- The provider setup flow asks for the command path, optional extra arguments, and supported model name
- Reads `KERNFORGE_CLAUDE_CLI_PATH` and `KERNFORGE_CLAUDE_CLI_ARGS` for non-interactive setup

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
- Uses the same request-timeout, streamed partial-text, incomplete-stream fallback, and configured retry behavior as the OpenAI-compatible client
- Streaming HTTP errors are surfaced as OpenRouter provider errors instead of being mistaken for empty SSE streams
- `/provider status` performs a live `/key` lookup for key-level `limit_remaining` and `usage`, and it also queries `/credits` when the key is a management key

### DeepSeek

- Default base URL: `https://api.deepseek.com`
- Reads `DEEPSEEK_API_KEY`
- Uses DeepSeek's OpenAI-compatible Chat Completions API at `/chat/completions`; explicit `/v1` base URLs are preserved for compatible proxy setups
- The model picker reads `/models` and prefers `deepseek-v4-pro`, then `deepseek-v4-flash`; legacy `deepseek-chat` and `deepseek-reasoner` are still recognized but are marked by DeepSeek for deprecation on 2026-07-24
- `/effort` applies to DeepSeek thinking-mode requests: `minimal`/`low`/`medium`/`high` map to DeepSeek `high`, while `xhigh` maps to `max`
- DeepSeek thinking-mode tool loops preserve and replay `reasoning_content` on assistant tool-call messages, including streamed tool calls, so follow-up tool-result requests remain compatible with DeepSeek's required multi-turn context shape
- Saved or recovered OpenAI-compatible transcripts are normalized before replay: orphaned `tool` results are dropped, and missing tool-call responses are synthesized as Codex-style `aborted` outputs so DeepSeek does not reject follow-up requests with invalid `tool` message ordering. Runtime guidance paths that intentionally supersede a model tool-call batch now write explicit `NOT_EXECUTED` tool results before the guidance message, matching Codex's typed call/output boundary instead of turning internal tool output into user context.
- `/provider status` performs a live `/user/balance` lookup when an API key is configured and shows DeepSeek's dynamic concurrency/rate-limit guidance
- The default shared model-route limit is `2` because DeepSeek dynamically limits user concurrency and returns HTTP 429 when the current limit is reached

### OpenAI-compatible

- Uses OpenAI-style chat completions
- Reads `OPENAI_API_KEY` unless overridden by config/env
- Requires an explicit `base_url`
- Applies the same assistant tool-call normalization and request-preview diagnostics as the OpenAI provider
- Provider-specific error names are preserved for OpenRouter and generic OpenAI-compatible endpoints
- Streaming HTTP errors are reported immediately with the provider response body and are not retried as empty-stream fallbacks
- `/provider status` can show the normalized endpoint and key presence, but billing visibility depends on the upstream provider

### Local OpenAI-compatible Providers

- `lmstudio`: default base URL `http://localhost:1234/v1`
- `vllm`: default base URL `http://localhost:8000/v1`
- `llama.cpp`: default base URL `http://localhost:8080/v1`
- These providers use OpenAI-style chat completions and discover models from `/v1/models`
- API keys are optional, so local servers work without setting `OPENAI_API_KEY`
- Switching from another provider resets to the selected local provider's default URL unless you pass `-base-url`; switching within the same local provider preserves a custom `base_url`

### OpenCode Zen (`opencode`)

- Default base URL: `https://opencode.ai/zen`
- Reads `OPENCODE_API_KEY` and falls back to `OPENCODE_ZEN_API_KEY`
- Fetches the model list from the configured OpenCode Zen API key, so the picker reflects the subscription/account that will actually be used
- The provider id remains `opencode` for CLI/config compatibility.
- Routes GPT/Codex-style models through responses, Claude-style models through messages, and compatible chat models through chat completions

### OpenCode Go (`opencode-go`)

- Default base URL: `https://opencode.ai/zen/go`
- Reads `OPENCODE_GO_API_KEY`, then falls back to `OPENCODE_API_KEY` and `OPENCODE_ZEN_API_KEY`
- Fetches models using the OpenCode Go key and keeps Go subscription routing separate from OpenCode Zen pay-as-you-go routing
- Routes known message-endpoint models through messages and other supported models through chat completions

### OpenAI Codex CLI

- User-facing provider label: `openai-codex-cli`; stored provider id: `codex-cli`
- Uses the installed `codex` command as a local provider bridge instead of sending an API key through Kernforge
- The provider setup flow asks for the command path and supported model name, including higher-tier Codex CLI models exposed by the installed account
- Reads `KERNFORGE_CODEX_CLI_PATH` and `KERNFORGE_CODEX_CLI_ARGS` for non-interactive setup
- `api_key` and provider-key storage are intentionally ignored for `codex-cli`

### OpenAI Codex Subscription

- User-facing provider label: `openai-codex-subscription`; stored provider id: `openai-codex`
- The `openai-codex` provider calls the Codex Responses backend directly with ChatGPT OAuth tokens
- Default base URL: `https://chatgpt.com/backend-api/codex`
- Authentication uses a Kernforge-owned OAuth file at the Kernforge config path, `codex_auth.json`, and refreshes it when needed. Run `/codex-auth login` to create it, `/codex-auth status` to inspect it, or `/codex-auth logout` to remove it
- Kernforge no longer defaults to the Codex CLI `~/.codex/auth.json` file for the direct provider. Set `KERNFORGE_CODEX_AUTH_FILE` for a different auth file or `KERNFORGE_CODEX_ACCESS_TOKEN` for a temporary access token override
- Use `/effort` to show per-target reasoning effort, `/effort high` to set the active main model, or `/effort analysis-worker low`, `/effort analysis-reviewer medium`, and `/effort specialist <name> high` for analysis and task-owner models. The independent review route effort is set through `/model cross-review <provider> <model> [reasoning_effort]`, while `/model` provides a numbered interactive setup flow. Unset effort is displayed as `undefined`
- Reasoning effort is stored per configured model target. Main profiles, the cross review route, analysis worker/reviewer profiles, and explicit task-owner model overrides can use different values even when they share the same provider family
- When model selection through `/model`, `/provider`, or route-specific model commands selects an effort-capable model while that target's effort is `undefined`, Kernforge defaults that target to `low`. Use `/effort` to change or clear it afterward
- Unlike the `codex-cli` bridge, this provider is wired into Kernforge's main LLM tool loop, so conversation context, tool calls, and tool results stay in the Kernforge session
- Only models exposed to the current ChatGPT/Codex account are usable. Example: `.\kernforge.exe -provider openai-codex -model gpt-5.5`

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

Each completed turn records a compact request/outcome summary, tool names, referenced or changed files from tool metadata, verification metadata, and selected `TaskState` notes such as completed steps or failed attempts. At the start of later turns, Kernforge injects two lightweight prompt sections when available:

- `Workspace continuity`: recent high-value memories from the same workspace, even when the new prompt is vague.
- `Query matches`: older memories that match the current request, file mentions, or structured filters.

When workspace continuity is injected, Kernforge also prints a short `memory` activity line with the memory ids and compact summaries so the user can see which prior records are being reused.

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

When Kernforge is running as the MCP server and Codex is the MCP client, review requests should use `kernforge_review`. That tool runs the same common review harness as `/review`, collects supplied diff/code, file paths, plans, PR context, or the current workspace `git diff`, then returns structured findings, gate status, route/lens status through `model_plan` and `reviewer_runs`, `scope_discovery`, `latest_review_freshness`, `edit_proposals`, `runtime_gate_ledger`, `single_model_second_pass`, `cross_review_triage`, `review_observability`, and action-oriented `next_commands` with reason, timing, safety, expected-result metadata, and artifact paths under `.kernforge/reviews`. Raw model output is kept behind artifact refs instead of being dumped into MCP responses. This keeps "review this with KernForge" on the typed review path instead of accidentally turning it into project analysis, verification, or a worker/reviewer route.

For MCP clients that reuse one Kernforge server entry across multiple repositories, `-cwd` is only the fallback workspace. Kernforge also honors client workspace hints from `initialize.rootUri`, `initialize.workspaceFolders`, `tools/call.params._meta.cwd`, and the per-tool `workspace` or `cwd` argument. In Codex CLI, if the MCP server reports the wrong workspace in `kernforge_status`, pass the current repo as `workspace` on the Kernforge tool call or configure the MCP entry so its `-cwd` matches that repo.

For live web research, Kernforge deploys the bundled MCP script to `~/.kernforge/mcp/web-research-mcp.js` on startup and auto-adds a matching `web-research` MCP entry to `~/.kernforge/config.json` when no equivalent web-search MCP is configured yet. You can provide `TAVILY_API_KEY`, `BRAVE_SEARCH_API_KEY`, or `SERPAPI_API_KEY` through your shell environment or through `mcp_servers[].env` in the user config, then run `/reload` if you changed config or environment after startup. Project-local config and hooks are ignored until `/trust on`, and workspace-local `mcp_servers` stay ignored because they can launch host-local commands from repository content. The bundled script lives with the app source under `cmd/kernforge/.kernforge/mcp/web-research-mcp.js`; runtime workspaces normally use the deployed copy under the user config directory. Once connected, Kernforge will prefer that MCP for latest/current research requests before local file inspection. Continuation turns such as "continue" or "keep going" preserve the original latest/current research intent until a web-research tool result is recorded, while fresh local code, git, or verification requests do not inherit stale web-research priority.

Minimal user config example:

```json
{
  "mcp_servers": [
    {
      "name": "web-research",
      "command": "node",
      "args": ["C:/Users/you/.kernforge/mcp/web-research-mcp.js"],
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
/effort
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

- `/status` shows current session and runtime state such as approvals, active session, memory, verification, MCP counts, and the runtime gate ledger. That gate section tells you whether the latest review is fresh, why the gate passed/blocked/degraded/needs action, which blocker class is active, and which recovery command to run next.
- `/config` shows effective settings such as provider defaults, token limits, hooks, locale, and verification toggles.
- `/provider status` shows the active provider, normalized `base_url`, API key presence, and provider-specific budget visibility. OpenRouter and DeepSeek perform live lookups, while OpenAI and Anthropic expose officially documented limits and billing guidance.
- `/model` is the model-routing hub for the main model, the analysis worker/reviewer, the optional cross review route, and task-owner overrides. The primary review route follows the main model; only the optional cross review route is configured through `/model cross-review`.
- `/effort` shows or sets the `openai-codex` and DeepSeek reasoning effort per configured model target.

### Conversation And Session Commands

```text
/clear
/compact [focus]
/export [file]
/rename <name>
/resume <session-id>
/session
/sessions
/handoff [note]
/handoff import <path>
/session-dashboard-html
/events [tail|export]
/continuity [note]
/recover [note]
/completion-audit [note]
/jobs [status|check|bundle|cancel|cancel-bundle]
/tasks
```

- `/handoff` writes `.kernforge/handoff/latest.md/json` with changed files, open tasks, verification, recent events, artifact refs, and a continuation prompt for another agent or cloud task. `/handoff import <path>` normalizes a returned result packet and marks matching `completed_tasks` in the TaskGraph.
- `/session-dashboard-html` writes `.kernforge/session_dashboard/latest.html` with the current thread events, task graph, automation due/failed state, changed files, background jobs, and artifact refs.
- `/events` tails or exports session conversation events as JSONL for local dashboards, schedulers, harnesses, and app-server style clients.
- `/continuity` writes `.kernforge/continuity/latest.md/json` as a local resume and recovery packet; `/recover` writes `.kernforge/recovery/latest.md/json` as a narrower failure runbook; `/jobs` lets you poll or cancel persisted background shell work from the terminal.
- `/completion-audit` writes `.kernforge/completion_audit/latest.md/json` as a local final-readiness gate for blockers, warnings, verification, tasks, jobs, and artifact evidence.

### Provider And Planning Commands

```text
/provider
/provider status
/model
/model cross-review [provider] [model] [reasoning_effort]
/model clear cross-review
/effort [target] [undefined|minimal|low|medium|high|xhigh]
/profile [list|<number>|rN|dN|pN]
/set-analysis-models
/set-specialist-model [status|clear <owner-profile|all>|<owner-profile> <provider> [model]]
/analyze-project [--path <dir>] [--mode map|trace|impact|surface|security|performance] [goal]
/analyze-dashboard [latest|path]
/docs-refresh
/analyze-performance [focus]
/review plan <task>
/review-soak --mode scripted
/review-soak --mode real-provider --turns <N> --timeout <duration>
/new-feature <task>
/specialists
/worktree [status|list|create [name]|enter|attach <path>|leave|cleanup]
/permissions [mode]
/set-max-tool-iterations <n|0|unlimited|none|off>
/progress-display [auto|compact|stream]
/locale-auto [on|off]
```

- `/model` first shows the current routing, then in interactive mode asks which target you want to change. Scripted cross-review updates use `/model cross-review <provider> <model> [reasoning_effort]`; `/model clear cross-review` returns reviews to single-model mode.
- `/model` is the main entry point for changing the main model, analysis worker/reviewer, optional cross-review route, and optional task-owner model overrides.
- `/effort` is intentionally separate from `/model`. Running `/effort` with no arguments prints each model target's value, `/effort undefined` clears the active main model override, and `/effort analysis-worker high` or `/effort specialist <name> low` changes an analysis or task-owner model. When the active main provider supports reasoning effort, the input prompt also shows `effort=<current>`.
- If a model change selects an effort-capable provider while that target's effort is `undefined`, Kernforge saves `low` as the starting effort instead of leaving that route ambiguous.
- `/config` also reports the model route scheduler. The scheduler queues requests by provider/model/base_url/reasoning_effort, does not hold a permit during retry backoff, and holds the route only while the provider call is actually running.
- Changing only the main model also changes the primary review route. The optional cross route, analysis worker/reviewer routes, and task-owner routes keep their explicit overrides. If project analysis should stop using a dedicated worker/reviewer route and follow the main model again, run `/set-analysis-models clear`.
- `/profile` lists saved profiles without changing anything in one-shot mode. If no main profile exists but a provider/model is already selected, Kernforge saves the current settings as the first profile and then shows the list. Main profiles also store their own analysis worker/reviewer and optional task-owner model override set. Changing those route models through `/model` updates the active main profile, and activating that profile restores the full set. Pass a number or action explicitly to activate, rename, delete, pin, or unpin.
- User and workspace profile lists are merged on load, and saving unrelated settings preserves existing main profiles instead of dropping them when a save payload omits profile arrays.
- `/model cross-review` is the single supported path for the optional `cross` review route. `design`, `security`, `false_positive`, `regression`, `test`, and `final_gate` are review lenses selected by the planner, not model routes.
- `/review-soak --mode scripted` writes `.kernforge/soak/<timestamp>/` artifacts that exercise `document_artifact`, `review_only`, `implementation`, `modify_then_review`, `fix_from_review`, `analysis`, and `mixed_flow` across single-model and cross-model routes. `/review-soak --mode real-provider` only runs when `KERNFORGE_REAL_PROVIDER_SOAK=1` and uses `KERNFORGE_REAL_PROVIDER`, `KERNFORGE_REAL_MODEL`, `KERNFORGE_REAL_BASE_URL`, and `KERNFORGE_REAL_API_KEY`; missing config writes an explicit skipped report instead of failing the suite.
- `/hooks` also prints the compact runtime gate summary, so hook/policy inspection and `/status` use the same freshness and next-command vocabulary.
- `/set-analysis-models` configures dedicated worker and reviewer profiles for project analysis.
- `/set-specialist-model ...` applies a workspace-scoped optional model override to one task owner profile.
- `/set-max-tool-iterations 0`, `/set-max-tool-iterations unlimited`, `/set-max-tool-iterations none`, and `/set-max-tool-iterations off` disable the per-request tool loop cap.
- `/progress-display` shows or saves the runtime progress mode; `/progress_display` is accepted as the same command. The default is `stream` for a fully persistent progress transcript. Use `auto` for durable tool/model ledger lines with transient noisy output, or `compact` for footer-only progress.
- `/analyze-project` generates docs, manifests, and dashboards by default. Older `--docs` input is kept only as hidden parser compatibility and is not shown in help or completion; use `/docs-refresh` when you only need to regenerate docs from the latest run.

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
- `/provider status|openai-codex-subscription|openai-codex-cli|openai-api|anthropic-claude-cli|anthropic-api|deepseek|openrouter|opencode|opencode-go|ollama|lmstudio|vllm|llama.cpp`
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
/review selection [...]
/review selection --all [...]
/edit-selection <task>
```

## Shell And Git Commands

Run shell commands with `!`:

```text
!git status
!go test ./...
```

Go test diagnostics:

```text
go test ./cmd/kernforge -list .
go test -json ./cmd/kernforge -count=1 -timeout 2m
go test ./cmd/kernforge -run "TestRuntimeGate|TestReviewMCPResponse|TestCrossReview|TestSingleModel|TestPreFinalHarness" -count=1
go test ./cmd/kernforge -run "TestAgent|TestApplyEditProposal|TestRunShell|TestVerification|TestProjectAnalyzer" -count=1 -timeout 15m
```

The package has many integration-style tests, so plain `go test ./cmd/kernforge -count=1 -timeout 15m` can appear silent until the package exits. Use `-json` or `-v` to expose the active test name, then split review/runtime-gate, agent/edit-loop, shell/verification, and project-analysis clusters when diagnosing timeouts. The 2026-05-28 investigation reproduced the silence with `-json -timeout 2m`; the last active cluster was project analysis after earlier agent/review tests consumed most of the budget, so the documented strategy is to keep targeted clusters repeatable before spending time on the full package.

Built-in shell shortcuts:

```text
!cd src
!cd ..
!ls
!dir
!pwd
!cls
!clear
```

`!cd` and directory-listing shortcuts resolve paths from the REPL current directory while preserving the workspace boundary. Parent navigation is allowed inside the workspace or active worktree, but crossing above that boundary is blocked.

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
- Visual Studio C++: adaptive source edits target the nearest `.vcxproj` with declared `/p:Configuration=... /p:Platform=...` when available; full verification can still run `msbuild <solution-or-project> /m` (or PowerShell-safe `& "<MSBuild.exe>" "<solution-or-project>" /m` when a tool path override is configured)

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
/fuzz-func <function-name> --source-scan focused
/fuzz-func <function-name> --source-scan full
/fuzz-func <function-name> --no-source-scan
/fuzz-func --from-candidate <candidate-id>
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
/source-scan status
/source-scan run
/source-scan run --limit 50
/source-scan run --only-slugs probe-copy-size-drift,double-fetch-user-buffer
/source-scan run --files driver/nsi.c,api/registry.c
/source-scan list
/source-scan show [id|latest]
/source-scan revalidate [id|latest]
/create-driver-poc <driver-name> [--type objectfilter|minifilter|registryfilter|wfpcallout]
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
- Prints live shard progress, including worker slot count, wave start/completion, shard completion/failure state, and stage/shard-prefixed model wait events
- Builds a structural index and an Unreal semantic graph
- Builds a deterministic `architecture_facts.json` fact pack for cached deep-structure Q&A, with current-source anchors, closed top-level directory facts, driver/control-flow hints, and answer invariants
- Tracks semantic fingerprints plus structured invalidation diffs to explain why shards were recomputed
- Writes Markdown and JSON analysis artifacts
- Generates an operational documentation set with `FINAL_REPORT.md`, `ARCHITECTURE.md`, `SECURITY_SURFACE.md`, `API_AND_ENTRYPOINTS.md`, `BUILD_AND_ARTIFACTS.md`, `VERIFICATION_MATRIX.md`, `FUZZ_TARGETS.md`, and `OPERATIONS_RUNBOOK.md`
- Writes a schema-versioned `docs_manifest.json`; readers treat missing `schema_version` as legacy and ignore unknown fields for additive compatibility
- Writes `dashboard.html` so run summary, module/function structure, the assistant-facing final report, generated docs, source anchors, graph-linked stale section diff, trust-boundary/attack-flow views, evidence/memory follow-ups, subsystem map, security surface, fuzz target candidates, and verification matrix are visible in a browser
- Provides an inline Markdown viewer with a full-window reader mode for long generated documents, while keeping generated-doc links inside the dashboard instead of opening separate tabs
- Honors explicit English/Korean output requests in project-analysis prompts and keeps terminal progress/status text UTF-8 safe when provider or model names are truncated
- Adds generated-doc graph sections for project edges, trust boundaries, data-flow paths, and attack/data-flow follow-up commands, with graph-specific stale markers reflected in section metadata
- Recollects generated docs into `vector_corpus.*` as whole-document and section-level records with source anchors, confidence, stale markers, and reuse metadata
- README describes product scope and flagship commands, the feature guide describes practical operating loops, and generated docs serve as the per-run project knowledge base with source anchors, confidence, and stale markers
- Maintains a `latest` knowledge pack for follow-up analysis
- Replaces `.kernforge/analysis/latest` during persistence so old dashboards, docs, or fact packs do not survive into the next retrieval pass
- Produces a vector corpus and provider-specific ingestion seeds
- Reuses unchanged shard results when incremental analysis is enabled

Typical outputs:

- `.kernforge/analysis/<timestamp>_<goal>.md`
- `.kernforge/analysis/<timestamp>_<goal>.json`
- `.kernforge/analysis/<timestamp>_<goal>_snapshot.json`
- `.kernforge/analysis/<timestamp>_<goal>_structural_index.json`
- `.kernforge/analysis/<timestamp>_<goal>_structural_index_v2.json`
- `.kernforge/analysis/<timestamp>_<goal>_unreal_graph.json`
- `.kernforge/analysis/<timestamp>_<goal>_architecture_facts.json`
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
- `.kernforge/analysis/latest/architecture_facts.json`
- `.kernforge/analysis/latest/docs/`
- `.kernforge/analysis/latest/docs_index.md`
- `.kernforge/analysis/latest/docs_manifest.json`
- `.kernforge/analysis/latest/dashboard.html`

Recommended flow:

1. Run `/analyze-project anti-cheat startup and integrity architecture`.
2. Open the latest dashboard with `/analyze-dashboard`, then review the generated knowledge pack, docs, and shard outputs.
3. Run `/analyze-performance startup` or another focus area such as `scanner`, `compression`, `upload`, `ETW`, or `memory`.
4. Use the resulting knowledge in `/review selection`, `/edit-selection`, `/verify`, and evidence-guided hook policy.

## Source-Level Function Fuzzing

`/fuzz-func` is meant to answer the attacker question directly: if an input parameter is manipulated precisely, which guards, probes, copies, dispatches, and cleanup paths can be pushed into unintended behavior even before you build a runnable harness?

Core commands:

```text
/fuzz-func <function-name>
/fuzz-func <function-name> --file <path>
/fuzz-func <function-name> @<path>
/fuzz-func <function-name> --source-scan focused
/fuzz-func <function-name> --source-scan full
/fuzz-func <function-name> --no-source-scan
/fuzz-func --from-candidate <candidate-id>
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
/source-scan status
/source-scan run
/source-scan run --limit 50
/source-scan run --only-slugs probe-copy-size-drift,double-fetch-user-buffer
/source-scan run --files driver/nsi.c,api/registry.c
/source-scan list
/source-scan show [id|latest]
/source-scan revalidate [id|latest]
/create-driver-poc <driver-name> [--type objectfilter|minifilter|registryfilter|wfpcallout]
```

What it does:

- Combines function signatures, real function-body observations, and reachable call closure.
- Accepts a function name directly, or a file path and then expands through include/import plus actual call flow to pick a representative root automatically.
- Rebuilds snapshot and semantic-index context on demand, so `/analyze-project` is optional rather than required.
- Reuses a matching saved source candidate when one exists, otherwise runs a focused source scan across the target, file scope, and reachable files before saving the `/fuzz-func` plan.
- Stores source-scan linkage in the function fuzz run as `source_candidate_id`, `source_matcher_slug`, `source_scan_mode`, `source_scan_run_id`, and `source_scan_summary`; candidate evidence is injected into source-only scenarios so `/fuzz-campaign run` can promote more targeted seeds.
- Generates x64-only C++20 MSVC/WDK driver POC templates with `/create-driver-poc <driver-name> [--type objectfilter|minifilter|registryfilter|wfpcallout]`, including a `Driver.cpp` WDM `.sys`, namespace/constexpr shared service/device/IOCTL/`DeviceType` header, no INF package, and `<driver-name>-tester.exe` that uses SCM, `CreateFile`, and `DeviceIoControl` for a complete load/ping/unload loop. Omitting `--type` preserves the original ping POC; typed templates cover object manager process/thread access stripping, filesystem minifilter open/rename/delete messaging, registry create/open/set/delete/rename callback blocking, and WFP outbound callout blocking.
- Source candidates preserve function-window evidence spans, confidence breakdown, dataflow/control-flow facts, file and symbol fingerprints, stale-source state, and native feedback calibration.
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
