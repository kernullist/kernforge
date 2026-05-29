# Kernforge Detailed Usage Guide

This document explains how to use the currently implemented Kernforge features in real engineering workflows, with concrete examples and recommended command sequences.

Reference point:
- Codebase snapshot: 2026-05-08

Intended readers:
- Windows security engineers
- Anti-cheat engineers
- Kernel and user-mode telemetry engineers
- Driver, signing, symbol, and package readiness engineers
- Unreal Engine security and integrity engineers

Goals of this guide:
1. Explain real usage patterns instead of just listing features.
2. Show which command combinations fit which kinds of problems.
3. Teach the full loop of `analyze-project -> analyze-performance -> investigate/simulate -> find-root-cause or fuzz-func -> review/edit/plan -> verify -> evidence/memory/hooks`.

## 1. The Best Way To Think About Kernforge

Kernforge can be used like a normal coding CLI, but its strongest current value now comes from building a reusable project knowledge pack before running sensitive engineering changes through the rest of the loop.

The best current loop looks like this:

1. If the workspace is large or unfamiliar, run `/analyze-project` first.
2. Use `/analyze-performance` to turn the latest knowledge pack into a bottleneck lens when performance or startup paths matter.
3. If live state matters, use `/investigate` to capture the current system state.
4. If an extra risk lens matters, use `/simulate` to evaluate tamper, visibility, or forensic blind spots.
5. If you already have a user-visible symptom and need to narrow likely causes, run `/find-root-cause`.
6. If attacker-controlled parameter behavior matters, run `/fuzz-func` for source-level fuzz reasoning; when a seed handoff is useful, Kernforge prints `/fuzz-campaign run` as the next step.
7. Use `/review selection`, `/edit-selection`, `/review plan`, or `/new-feature` to drive the work.
8. Run `/verify` to execute the verification plan.
9. Use `/evidence-*` and `/mem-*` to inspect both recent signals and longer-lived context.
10. Follow the printed handoff blocks and `/continuity` packet after analysis, investigation, simulation, performance, root-cause, fuzzing, verification, evidence, memory, checkpoint, feature, worktree, jobs, and task-owner actions instead of memorizing the command order.
11. Let hooks act as the final policy layer before push or PR.

Practical interpretation:
1. `analyze-project` builds a reusable architecture map instead of a disposable summary.
2. `analyze-performance` extracts likely hot paths and bottlenecks from the latest architecture knowledge.
3. `investigate` captures what is happening live.
4. `simulate` highlights risk-oriented weak spots using lightweight heuristics.
5. `find-root-cause` turns a symptom, trigger, expected invariant, and observed failure into worker/reviewer causal analysis.
6. `fuzz-func` synthesizes attacker input states, counterexamples, and branch deltas from real source-level guard/probe/copy/dispatch behavior.
7. `verify` turns code changes and recent context into a concrete validation plan.
8. `evidence` stores structured recent signals.
9. `memory` keeps conclusions across sessions.
10. `hooks` turn that accumulated context back into guardrails.

## 2. Core Features And When To Use Them

### Input And Cancellation Handling

Purpose:
1. Keep prompt cancel and in-flight request cancel distinct on Windows consoles.
2. Avoid missing brief `Esc` taps during a running request.
3. Prevent leftover console `Esc` input from auto-canceling the next prompt after request cancel.

Current behavior:
1. `Esc` while typing cancels only the current prompt input.
2. `Esc` during model response wait cancels the in-flight request.
3. On Windows, Kernforge combines async key-state checks with console input polling so short `Esc` taps are still recognized.
4. After request cancel, Kernforge waits briefly for `Esc` release and clears pending console input before opening the next prompt.
5. Assistant streaming now suppresses empty leading chunks, flushes cleanly before progress lines, and breaks repeated follow-on preambles onto separate lines for readability.
6. Generic waiting text is collapsed so the thinking indicator does not repeat the same status twice.
7. The thinking elapsed timer is rebased at phase boundaries, and abnormal stale values are clamped at the 2-hour display mark.
8. Repeated blank streamed chunks are converted into a compact working status instead of printing empty lines.
9. If a final streamed answer appears to stop mid-sentence, Kernforge asks the model to continue once and merges the continuation before returning to the prompt.
10. Pressing `Enter` on an empty main prompt is ignored so empty turns do not clutter the session transcript.
11. `progress_display` controls progress visibility and defaults to `compact`, so routine review and coding work stays readable. `/progress-display auto|compact|stream` changes it from the REPL, and `/progress_display ...` works as the same command when you copy the config key: `compact` keeps short progress in the footer, `auto` keeps durable tool/model/route and project-analysis events without repeating verbose review flow text, and `stream` persists every update for detailed debugging.
12. OpenAI-compatible and OpenAI Codex streaming providers emit tool-call construction events so users can see when the model is preparing a tool call and when its arguments are ready.
13. DeepSeek and other OpenAI-compatible follow-up requests normalize saved tool transcripts before replay. Orphaned `tool` results are dropped, and missing tool-call responses are synthesized as `aborted` outputs so provider-side message validation does not reject recovered sessions. Runtime guidance paths that supersede a tool-call batch persist explicit `NOT_EXECUTED` outputs before the guidance message instead of recovering internal tool output as user context.
14. Post-change diff review does not import final-answer coding-harness state. Worker/causal-evidence blockers remain visible for final/completion-audit gates, but they do not become post-change code-review blockers.
15. The REPL opens with a compact branded banner and keeps assistant output separate from tool and verification activity lines.
16. `!cd` and directory-listing shortcuts resolve from the REPL current directory while keeping the workspace boundary fixed. `!cd ..` can move upward inside the workspace or active worktree, but cannot cross above that boundary.

### Runtime Inspection And Approval State

Purpose:
1. Distinguish between current session state and merged effective settings.
2. Make write, diff, shell, and git approvals visible without opening config files.
3. Keep git-mutating actions on a separate approval path from normal file edits.

Useful commands:
- `/status`
- `/config`
- `/provider status`

Current behavior:
1. `/status` shows session and runtime state such as the active session id, current approvals, selection state, verification state, MCP counts, and the runtime gate ledger. Use `runtime_gate`, `review_freshness`, blocker/warning counts, and `next_command` to decide whether review, verification, or completion audit needs repair before final answers or write-side actions.
2. `/config` shows effective settings such as provider defaults, token limits, locale behavior, hook settings, and verification defaults.
3. `/provider status` shows the active provider, normalized endpoint, API key presence, and provider-specific budget visibility.
4. For OpenRouter, `/provider status` performs a live lookup of key-level `limit_remaining` and `usage`, and it also shows account credits when the key is a management key.
5. For DeepSeek, `/provider status` performs a live `/user/balance` lookup when an API key is configured and shows the provider's dynamic concurrency guidance.
6. For OpenAI and Anthropic, `/provider status` intentionally shows officially documented billing and usage visibility limits instead of inventing a live balance endpoint.
7. `kernforge --version`, `kernforge -version`, and `kernforge version` print the executable version before config/session loading. On Windows release builds this is the PE `FileVersion`; unstamped developer builds fall back to the embedded app version.
8. `kernforge --help` includes the same version at the top of the general help text.
9. `Allow write?` and `Open diff preview?` can be auto-approved for the current session with `a`.
10. Git-mutating tools such as `git_add`, `git_commit`, `git_push`, and `git_create_pr` use a separate `Allow git?` session approval.
11. Git-mutating tools are intended for explicit user requests rather than normal review or edit turns.
12. `/hooks` prints the same compact runtime gate summary as `/status`, so hook/policy checks do not invent a second interpretation of review freshness.

### Prompt Intent Routing

Purpose:
1. Keep analysis and explanation requests read-only by default.
2. Keep explicit fix requests tool-driven instead of drifting into prose-only advice.
3. Reduce accidental patch handoff or accidental git mutation during normal code review.

Current behavior:
1. Requests that ask to analyze, explain, diagnose, review, or document default to read-only investigation mode unless they also explicitly ask for a fix.
2. Requests that explicitly ask to fix code keep edit tools available and Kernforge nudges the model back toward direct tool use if it tries to hand the patch back to the user.
3. Git staging, commit, push, and PR creation are blocked unless the user explicitly asked for that git action.

### Self-Driving Work Loop

Purpose:
1. Keep implementation, fix, and execution requests from stopping at analysis.
2. Seed `TaskState` and `TaskGraph` with an inspect, implement, verify, summarize loop.
3. Keep the task in recovery instead of marking it complete when post-edit verification fails.

Current behavior:
1. Requests such as "implement this", "fix this", "handle the remaining items", or "run the tests and finish" become self-driving candidates.
2. If an independent cross review route is configured, Kernforge can use it for reviewer/planner preflight; otherwise it uses the active main model and deterministic gates.
3. Read-only prompts such as "why did that error happen?", "what is the current state?", or "analyze this" do not start an automatic edit loop.

### Proactive Suggestion Dashboard

Purpose:
1. Collect Kernforge's current next-action suggestions in one view.
2. Compare analysis stale markers, verification gaps, evidence gaps, and changed paths in the same dashboard.
3. Link each suggestion to the relevant dashboard or command.

Useful commands:
- `/suggest`
- `/suggest accept <id>`
- `/suggest dismiss <id>`
- `/suggest mode <observe|suggest|confirm>`
- `/suggest-dashboard-html`

Current behavior:
1. `/suggest-dashboard-html` renders integrated signals and suggested next actions together.
2. Suggestion cards include related command chips, evidence refs, and dashboard links such as `/verify-dashboard-html`, `/evidence-dashboard-html`, and `/analyze-dashboard`.
3. Cards include `/suggest accept <id>` and `/suggest dismiss <id>` chips so repeated suggestions can be managed.
4. `/suggest` candidates are synchronized into `TaskGraph` as `suggest:<id>` nodes with ready/in_progress/completed/canceled states.
5. In `/suggest mode confirm`, accepting a suggestion only runs safe commands such as `/verify`, dashboards, `/docs-refresh`, `/automation add`, and `/review pr`.
6. Accepted or dismissed suggestions are also promoted into persistent memory as preference records.

### Session Dashboard

Purpose:
1. Show the current thread, task graph, automation state, changed files, and artifact refs in one local HTML view.
2. Make long-running sessions easier to resume without reading the full transcript.
3. Surface due or failed automation together with open task graph nodes and recent runtime events.

Useful commands:
- `/session-dashboard-html`
- `/events tail 20`
- `/events export`

Current behavior:
1. Writes `.kernforge/session_dashboard/latest.html` for the current workspace.
2. Includes session/provider metadata, context size, task status counts, open task graph nodes, automation due/failed/paused counts, recent conversation events, changed files, background jobs/bundles, and artifact refs.
3. Records the dashboard path in the conversation event log and opens it automatically in interactive mode when possible.
4. `/events tail [n]` prints recent session events as JSONL records, while `/events export [path]` writes a durable local event stream to `.kernforge/events/<session-id>.jsonl` and `.kernforge/events/latest.jsonl`.

### Continuity Packet and Local Jobs

Purpose:
1. Recover naturally from failed local shell commands, failed verification, or stale background work without pasting logs again.
2. Give a long-running task a local resume packet that survives compaction, model switches, and handoff.
3. Let the terminal user inspect persistent background jobs and bundles directly, not only through model tool calls.

Useful commands:
- `/continuity`
- `/continuity continue Codex parity work`
- `/recover`
- `/recover continue failed verification`
- `/recover execute-safe continue failed verification`
- `/completion-audit`
- `/completion-audit finish Codex parity work`
- `/jobs status`
- `/jobs check latest`
- `/jobs bundle latest`
- `/jobs cancel <job-id> stale verification`
- `/jobs cancel-bundle <bundle-id> superseded`
- `/worktree list`
- `/worktree enter`
- `/worktree attach <path> [branch]`

Current behavior:
1. `/continuity` writes `.kernforge/continuity/latest.md` and `.kernforge/continuity/latest.json`.
2. The packet includes active/base workspace roots, branch, provider/model, changed files, open task graph nodes, worktree leases, active edit loop, active failure repair, latest verification failure, background jobs/bundles, recent runtime errors, artifact refs, recovery actions, next commands, and a suggested continuation prompt.
3. Direct `!shell` failures are recorded as `command_error` conversation events, so later `/continuity` and recent-error answers can recover from the failure without requiring the user to paste the output again.
4. `/jobs` syncs and prints persisted background job/bundle status, then supports direct polling and cancellation by id or `latest`.
5. `/worktree list` shows the session worktree, task-owner editable worktree leases, and `git worktree list --porcelain` in one view before resuming or switching roots.
6. `/worktree enter` re-enters the recorded isolated worktree after `/worktree leave`, and `/worktree attach <path> [branch]` attaches an existing worktree as an unmanaged session worktree.
7. `/recover` writes `.kernforge/recovery/latest.md` and `.json` as a narrower failure runbook from the latest error, verification failure, failure-repair state, background jobs, open tasks, and next commands. The runbook includes a structured diagnosis, stable failure signature, action plan status, and execution log.
8. `/completion-audit` writes `.kernforge/completion_audit/latest.md` and `.json` with blockers, warnings, required artifacts, latest verification, open tasks, background jobs, recent errors, and coding harness evidence before finalizing.
9. `/recover execute-safe` runs only safe-auto recovery actions and records their status. Shell replay is limited to whitelisted verification/status commands without chaining or redirection.
10. Slash actions are validated against their recorded artifacts: failed `/verify` reports and non-ready `/completion-audit` results become failed recovery actions, and `stop_on_failure` skips dependent actions.
11. The safe-auto shell whitelist allows narrow commands such as `go test`, `go vet`, `go list`, `git status`, and `git diff --check`, but rejects high-risk Go/Git flags that can launch external tools or write side artifacts.

### Autonomous Goals

Purpose:
1. Let the user define a Codex-style goal once, then let Kernforge keep working without follow-up prompts.
2. Support objectives written inline or stored in markdown files.
3. Repeat implementation, self-review, verification, completion audit, final semantic review, and recovery until the goal is complete or a concrete blocker is recorded.

Useful commands:
- `/goal "add the missing recovery tests and update docs"`
- `/goal start @GOAL.md`
- `/goal start --file GOAL.md --max-iterations 12`
- `/goal start --time-budget 10m --until-complete @GOAL.md`
- `/goal start --token-budget 120000 "finish the refactor without exceeding context budget"`
- `/goal start --rollback-on-regression "finish the refactor and keep verification green"`
- `/goal start --no-run @GOAL.md`
- `/goal run latest`
- `/goal status`
- `/goal audit`
- `/goal complete`
- `/goal cancel`
- `kernforge -goal "finish the verification policy change"`
- `kernforge -goal "finish the refactor" -goal-token-budget 120000 -goal-max-iterations 12`
- `kernforge -goal-file GOAL.md`

Current behavior:
1. `/goal` creates a `GoalState` in the session and writes `.kernforge/goals/latest.md` plus `.kernforge/goals/latest.json`.
2. Markdown goals can be passed as `@GOAL.md`, `--file GOAL.md`, or the `-goal-file` CLI flag; one-shot `-goal` runs support matching max-iteration, time-budget, token-budget, until-complete, and rollback flags.
3. Goal start primes an acceptance contract, task graph, completion criteria, and status artifact before execution.
4. Each iteration records a checkpoint when checkpoint storage is configured, sends an implementation prompt, then runs an independent review verdict gate.
5. The review prompt includes concrete evidence whenever available: the implementation reply, checkpoint diff from the iteration start, git status/diff context, changed-file summaries, and bounded untracked-file excerpts.
6. A `NEEDS_REVISION` review triggers an automatic repair pass before verification. The repair prompt preserves structured reviewer issues plus the same implementation context, so the worker receives actionable findings rather than only a short revision summary.
7. During goal execution, write, diff preview, shell, and git approvals are session-bypassed so the loop does not stop for user confirmation.
8. After the agent pass, Kernforge runs `/verify --full`, writes `/completion-audit`, runs a final semantic reviewer when the audit is ready, and if needed runs `/recover execute-safe` or a semantic repair pass before the next iteration.
9. The final semantic reviewer receives the same workspace evidence model, and unclear or insufficient review evidence is treated as `NEEDS_REVISION` instead of approval.
10. The progress ledger tracks changed files, verification, audit blockers/warnings, review verdicts, final semantic verdicts, no-progress count, repeated failure signatures, token usage estimate, and command history.
11. The loop completes only when the completion audit is `ready=true` and final semantic review returns `APPROVED`; otherwise it keeps iterating until canceled, blocked by an unrecoverable runtime error, stopped by the configured iteration/time/token cap, or stopped by repeated no-progress/failure detection.
12. `/goal run` resumes a pending or blocked goal from the latest persisted state.
13. `/goal audit` re-runs the completion audit for the goal objective without running another implementation pass or marking the goal complete.
14. `/goal complete` is the explicit completion gate: it re-runs audit, runs semantic review, and marks complete only if both approve.

### Local Automations MVP

Purpose:
1. Provide a local-session foundation for Codex-style recurring workflows.
2. Connect recurring verification and PR review report generation to suggestions and the task graph.
3. Validate due checks, safe command execution, and report recording locally before adding cloud jobs.

Useful commands:
- `/automation`
- `/automation add recurring-verification /verify`
- `/automation add recurring-verification --every 2h /verify`
- `/automation add pr-review /review pr`
- `/automation due`
- `/automation digest`
- `/automation monitor`
- `/automation monitor --notify`
- `/automation watch --interval 5m --notify`
- `/automation daemon-start --interval 5m --notify`
- `/automation daemon-status`
- `/automation daemon-stop`
- `/automation notify --webhook-url https://example.invalid/kernforge`
- `/automation notify`
- `kernforge -command "/automation monitor --notify"`
- `/automation run-due`
- `/automation run <id>`
- `/automation pause <id>`
- `/automation resume <id>`
- `/automation remove <id>`
- `/review pr`
- `/review pr --github`
- `/review pr --github --draft-comments`
- `/review pr --github --post-comments`
- `/review pr --resolve-thread <thread-id>`
- `/review pr --draft-issue`
- `/review pr --create-issue`
- `/review pr --create-issue --label bug,security --assignee <login> --milestone "May 2026"`

Current behavior:
1. Automation slots are stored in the session JSON under `automations`.
2. `--every`, `--hourly`, and `--daily` schedules write `next_run_at` plus a next-run hint, and `/automation due` shows active scheduled slots whose time has passed.
3. `/automation run <id>`, `/automation run-due`, and `/automation monitor` execute registered commands through the safe command dispatcher.
4. `/automation digest`, `/automation monitor`, `/status`, and the REPL startup notice surface due, failed, and paused automation state.
5. `/automation notify` and `/automation monitor --notify` write `.kernforge/automation/latest_digest.md` so an external watcher, CI step, or shell script can consume the latest automation state without scraping terminal output.
6. `/automation notify|monitor|watch --webhook-url <url>` POSTs digest JSON to an external receiver. Webhook URLs are redacted in conversation events.
7. `/automation watch [--interval 5m] [--cycles N|--once] [--notify] [--webhook-url <url>]` runs a foreground standing monitor loop. Each cycle runs due safe automations, prints the digest, and optionally refreshes the digest artifact or sends a webhook.
8. `/automation daemon-start|daemon-status|daemon-stop` manages a process-detached local automation watcher with state and logs in `.kernforge/automation/daemon.json` and `daemon.log`.
9. `-command "/automation monitor --notify"` lets Windows Task Scheduler, service wrappers, or CI run a slash command without entering the REPL.
10. `/review pr` writes git status, diff stat, changed files, and a review checklist to `.kernforge/pr_review/latest.md`, then records an artifact ref in the conversation event log.
11. `/review pr --github` adds current PR metadata, review decision, comments, and checks from `gh pr view --json ...` when available.
12. `/review pr --draft-comments` writes `.kernforge/pr_review/comments.md` as a file-level review comment draft without posting to GitHub.
13. `/review pr --post-comments` runs `gh pr review --comment --body-file .kernforge/pr_review/comments.md` after generating the draft. This write-side action is only allowed from the explicit command, not suggestion acceptance or scheduled automation.
14. `/review pr --resolve-thread <thread-id>` runs GitHub's `resolveReviewThread` GraphQL mutation through `gh api graphql`. This write-side action is also explicit-only.
15. `/review pr --draft-issue` writes `.kernforge/pr_review/issue.md`, and `/review pr --create-issue` posts that draft with `gh issue create --title ... --body-file ...`. Issue creation is explicit-only.
16. Issue drafts and create calls accept repeated or comma-separated `--label`, repeated or comma-separated `--assignee`, and quoted `--milestone` values. Create mode passes them through to `gh issue create`.
17. When verification gaps or dirty diffs exist, `/suggest` can recommend recurring verification or PR review automation registration.

### Delegation Handoff

Purpose:
1. Save the minimum state needed to pass the current task to a Codex cloud task, another local agent, or a human reviewer.
2. Package changed files, open task graph nodes, recent events/artifacts, verification state, and a continuation prompt.
3. Import a result packet from another agent or cloud task and merge its task status/artifact refs back into the current session.

Useful commands:
- `/handoff`
- `/handoff continue automation scheduler work`
- `/handoff import .kernforge/handoff/imports/cloud_result.json`

Current behavior:
1. Writes `.kernforge/handoff/latest.md` and `.kernforge/handoff/latest.json`.
2. Records the generated artifact refs in the conversation event log.
3. The next agent starts from `Suggested Prompt`, `Changed Files`, `Open Tasks`, and `Artifact Refs`.
4. `/handoff import <path>` normalizes JSON or markdown results into `.kernforge/handoff/imports/*.json` and `*.md`, records a conversation event, and marks matching `completed_tasks` IDs complete in the TaskGraph.

### Coding Harnesses And Repair Loop

Purpose:
1. Check the final answer against the real workspace state before it is shown.
2. Structure the Codex-style completion loop around acceptance, artifacts, scenarios, subagent evidence, test impact, open tasks, background jobs, failure repair, completion audit, and user-change isolation.
3. Treat blockers as feedback that requires revision, verification, or explicit disclosure.

Current behavior:
1. `AcceptanceContract` extracts expected behavior, non-goals, changed surfaces, required artifacts, and verification requirements from the user request.
2. Patch transactions record edit tools, scoped shell writes, changed paths, fingerprints, and failed tool calls so the final harness knows what actually changed.
3. The artifact-quality harness reads requested or claimed document artifacts and flags placeholder/TODO content, very thin content, or missing topic coverage.
4. The scenario-replay harness detects `when/expected/but observed` bug scenarios and requires replay/verification evidence or an explicit "not run" disclosure before a code-changing fix claim.
5. The subagent-orchestration harness checks whether root-cause answers connect worker evidence and reviewer validation to a real causal bridge. It blocks hidden reviewer failures or weak worker evidence that cannot lead to the user-visible symptom.
6. The test-impact harness maps code-like changed paths to recommended verification commands and records a warning when successful verification evidence is missing.
7. Build/test shell commands that may write artifacts use an explicit command lifecycle. Kernforge shows them as a verification approval request before execution, not as an already-started shell job. Non-interactive `-prompt -y` runs auto-approve this prompt the same way they auto-accept diff preview, while non-bypass prompt runs skip instead of guessing approval. If the user declines the pinned verification prompt, the tool result is `verification_status=skipped` and `command_execution_status=declined`; that result is not successful verification evidence, does not clear pending verification checks, and must not be retried or polled in the same turn unless the user explicitly approves verification. Declined or prompt-failed automatic verification is also saved as a skipped `VerificationReport`, leaves the verification pending check active, and prompts final answers to say verification was not run instead of treating the run as completed. Generated-document-only artifact turns are exempt from that pending state after deterministic artifact-quality checks approve the report and the final answer avoids unsupported verification claims; they complete the self-driving state instead of re-entering shell validation or review. Background verification starts are recorded as `verification_status=pending` with `verification_evidence=false`; only completed zero-exit background checks become successful evidence. After a terminal decline or skipped automatic verification, same-turn verification retries and `latest` background polls are folded into a synthetic `NOT_EXECUTED` tool result before any new shell/progress status is emitted, steering the model toward disclosure instead of another fake recovery attempt. Final answers can disclose that verification was not run, but the edit-loop ledger still remains `risk_accepted` until successful verification evidence exists for the changed paths. Background job bundles store job lists in `job_entries`; scalar `job_status` remains a single-job status field. Generated analysis, fuzz, and manifest findings are informational verification evidence with an empty command and output-only text; they are reported as skipped evidence context, never executed as shell.
8. The job-supervisor harness prevents final answers from hiding failed, stale, or still-running background jobs and bundles.
9. `/completion-audit` externalizes the final readiness gate as `.kernforge/completion_audit/latest.md/json`, so a human or scheduler can see the same blockers and warnings outside the model turn.
10. The failure-repair harness keeps the first meaningful failure line, repeated count, narrow rerun command, and next repair steps in active context after verification fails.
11. User-change isolation blocks overwrites when a target file changed outside the agent after the turn began, forcing a fresh read and merge-aware edit.
12. The final-answer reviewer now runs only for unresolved verification, coding-harness blockers, or actual patch transaction changes. Plan state or task-graph presence alone no longer creates an extra reviewer/revision round-trip.

Practical interpretation:
1. Before saying "done", Kernforge rechecks actual artifacts and verification evidence.
2. For root-cause work, the important bar is not plausibility; it is the causal chain from trigger to invalid state to user-visible symptom.
3. When a blocker appears, the user does not need to restate the request. Harness feedback is injected into the next model turn so the agent can repair, verify, or disclose the remaining gap.

### Read Reuse And Large-File Inspection

Purpose:
1. Reduce repeated `read_file` churn on very large source files.
2. Make `grep` results expose when nearby context was already read.
3. Nudge the model away from scanning the same region again when cache evidence already exists.

Current behavior:
1. `read_file` reuses unchanged exact ranges, covered subranges, and partial overlaps before it falls back to fresh file reads.
2. Cached `read_file` replies include a `NOTE:` prefix so the model can treat them as already-seen context rather than fresh evidence.
3. Repeated same-file `read_file` turns now use that cache signal to warn earlier when the model is looping on the same chunk.
4. `grep` annotates matches with `[cached-nearby:inside]` when the matching line already sits inside a recent read span.
5. `grep` annotates matches with `[cached-nearby:N]` when the match is near a recently read span, which encourages a narrower follow-up `read_file` request.
6. Stale read hints are ignored automatically when file size or modification time changes.

Practical interpretation:
1. If you see `NOTE: returning cached content...`, the tool is telling the model it already has that text and should only read a missing adjacent range if necessary.
2. If `grep` returns `[cached-nearby:inside]`, the next best action is usually edit, explain, or read a tiny adjacent gap rather than rescanning a large block.
3. If `grep` returns `[cached-nearby:2]`, `[cached-nearby:5]`, and similar markers, the model should usually read only that small uncovered neighborhood.

### 2.0 Project Analysis

Purpose:
1. Build a reusable architecture document for a large workspace.
2. Split analysis across multiple worker and reviewer passes.
3. Keep a `latest` knowledge pack and performance lens for follow-up work.
4. Reuse unchanged shard results when incremental mode is enabled.
5. Preserve a structural index, Unreal semantic graph, and vector corpus for downstream automation.
6. Preserve deterministic architecture facts so cached deep-structure answers can be checked against source-derived invariants.
7. End the run with a highlighted `Analysis artifacts:` block and an `Analysis handoff` so the user can continue into the dashboard, fuzz campaign automation, target drilldown, or verification without memorizing the sequence.
8. In local-provider or explicitly route-limited setups, cap shared worker/reviewer model routes through the global scheduler to reduce provider saturation and low-confidence placeholder cascades.
9. Let Kernforge adapt shard size for local models when shard limits are not configured, then automatically retry once with smaller shards when a final timeout or 5xx/overload-style provider error still stops the run.
10. Show live execution progress with worker slot count, shard waves, completed/failed shard totals, cache/review state, and model wait events labeled by analysis stage and shard.

Useful commands:
- `/analyze-project [--path <dir>] [--mode map|trace|impact|surface|security|performance] [goal]`
- `/docs-refresh`
- `/analyze-performance [focus]`
- `/set-analysis-models`

The goal is optional. If omitted, Kernforge infers a practical goal from the selected mode and path.
Follow-up modes automatically load a previous `map` run as baseline structure when available. This lets `trace`, `impact`, `surface`, `security`, and `performance` start from the architecture map without sharing the same shard cache.
Before confirmation, the analysis plan prints the selected `baseline_map` so the user can see which map run will be reused.
Large runs are provider-failure tolerant: worker/reviewer rate limits are recorded as low-confidence shard failures, and synthesis falls back to a local document when the final model request fails.
For local-model providers such as LM Studio, vLLM, llama.cpp, and Ollama, unset `max_files_per_shard` / `max_lines_per_shard` values are adjusted from provider, model size, max tokens, and request timeout before the plan is confirmed. If the run still ends in a timeout, 5xx, overload, empty response, connection reset, or similar provider-pressure error after normal request retries are exhausted, Kernforge prints an `adaptive_retry_shards` line and reruns once with smaller shard limits. Rate limits are not retried this way because smaller shards usually create more requests.
When worker and reviewer use the same provider/model/base_url/reasoning_effort route, shard execution is capped by the model route limit. Local providers default to serial execution with a route limit of 1; cloud/API routes are not forced to serial execution unless `model_routes` says so.
Reasoning effort is stored per configured model target, not as one global override. The main profile, optional cross review route, analysis worker/reviewer, and explicit task-owner model overrides can each carry a different `reasoning_effort`; selecting a new effort-capable main, analysis, or task-owner target defaults that target to `low` when it was still undefined, while the cross review route defaults to at least `high` and raises saved `low`/`medium` values to `high` at runtime.
Strict omission retry is finding-field driven: Kernforge retries when a structured finding is actually omitted, cut off, or weak with omission markers, but it accepts usable structured findings even if prose summary text contains words such as `omitted`.
Route-specific `base_url` values for the cross review route, analysis worker/reviewer, and task-owner model overrides can be omitted safely. Same-provider routes inherit the main endpoint; different-provider routes use their own configured or default endpoint so proxy/local routes do not drift silently.
Changing the main provider/model preserves explicit analysis worker and reviewer profiles. Use `/set-analysis-models clear` when you want project analysis to inherit the current main model again instead of a previously dedicated route.
`/analyze-project` generates docs, manifests, and dashboards by default. Older `--docs` input is accepted only as quiet backward compatibility and is not shown in help or completion; use `/docs-refresh` when you only need to rebuild docs from the latest saved run.
The generated documentation set includes `FINAL_REPORT.md`, which preserves the assistant-facing final synthesis that was printed at the end of the run, plus the operational docs used for architecture, security, entrypoints, build artifacts, verification, fuzz targets, and operations.
The dashboard opens those documents in an inline Markdown viewer. Use the `Reader` button for a full-window reading mode when the final report or another generated document is too long for the default panel.
If the goal explicitly asks for English or Korean output, that request is passed through to the worker and synthesis prompts instead of relying only on the detected conversation language. Live model-wait/progress text is truncated on UTF-8 rune boundaries so localized status text does not become mojibake.

Role split:
1. `README.md` is the quick product-scope, flagship-command, and artifact-location document.
2. This feature guide explains the operating sequence across investigation, simulation, root-cause, fuzzing, verification, evidence, and memory.
3. Generated `analyze-project` docs are the per-run project knowledge base with source anchors, confidence, and stale/invalidation markers.

Mode summary:
1. `map` is the default mode and prioritizes architecture ownership and module boundaries.
2. `trace` emphasizes runtime flow, caller/callee chains, and dispatch order.
3. `impact` emphasizes change impact, downstream dependencies, and retest scope.
4. `security` emphasizes trust boundaries, validation, and privileged surfaces.
5. `performance` emphasizes startup cost, hot paths, contention, and blocking chains.

Best used when:
1. You are entering a large codebase and need more than an ad hoc summary.
2. The work spans startup, integrity, ETW, scanner, compression, memory, or upload paths.
3. You want follow-up review and verification to inherit a stable architecture view.
4. You are dealing with a UE5-scale codebase where modules, targets, reflection, replication, and asset/config coupling all matter at once.

Additional artifacts now produced by project analysis:
1. `snapshot`: structured scan output plus runtime and project edges.
2. `structural index`: symbol anchors, references, build contexts, build ownership edges, call edges, and overlay-oriented analysis state.
3. `unreal graph`: UE project, module, network, asset, system, and config semantics.
4. `architecture facts`: deterministic domain hints, top-level directory facts, critical anchors, dispatch/registration flows, boundary facts, and answer invariants.
5. `knowledge pack`: human-readable architecture digest and subsystem summaries.
6. `vector corpus`: embedding-ready project, subsystem, and shard documents.
7. `vector ingest exports`: staging files for pgvector, SQLite, and Qdrant pipelines.

What materially changed for large and Unreal-heavy workspaces:
1. A semantic shard planner now prioritizes `startup`, `build_graph`, `unreal_network`, `unreal_ui`, `unreal_ability`, `asset_config`, `integrity_security`, and `unreal_gameplay`.
2. Worker and reviewer prompts now carry shard-specific semantic focus and review checklists.
3. Incremental reuse now considers semantic fingerprints instead of relying only on file hashes.
4. Build alignment now promotes `.uproject`, `.uplugin`, `.Build.cs`, `.Target.cs`, and `compile_commands.json` into reusable build-context records.
5. Source anchors now lift Go, C++, and C# functions into symbol records with line ranges, call edges, build ownership edges, and security overlays.
6. `trace`, `impact`, and `security` retrieval now expand graph neighborhoods instead of relying only on keyword hits, and they persist `build_context_v2` plus `path_v2` evidence.
7. The C++ anchor parser now covers template out-of-line methods, operators, `requires`, `decltype(auto)`, API-macro-wrapped scopes, and friend functions.
8. Output documents now expose subsystem invalidation reasons, evidence, diffs, top change classes, and graph-section stale markers.
9. The dashboard stale diff links graph-related changes directly into trust-boundary, data-flow, and project-edge sections.
10. Persisted artifacts now include machine-readable snapshot, structural index, Unreal semantic graph, vector corpus, and ingestion seed files for downstream retrieval pipelines.
11. The architecture fact pack is injected into worker/reviewer/synthesis prompts and into cached answer packs so structure answers stay source-grounded even when no extra tool call is needed.
12. C/C++ and driver-oriented scanners now look for dispatch tables, unload/finalize paths, callback registrations, filter registrations, aliases, macros, and include-driven registration helpers instead of relying only on filename heuristics.
13. `.kernforge/analysis/latest` is replaced per run, avoiding stale artifact bleed-through across repeated analysis tests.
14. Goal text can narrow analysis to matching directories when you clearly target a sub-area.
15. Interactive runs can flag hidden or external-looking directories so you can exclude them before scanning.

### Root-Cause Investigation

Purpose:
1. Turn a user-reported symptom into source-evidence-backed root-cause candidates.
2. Select only source files and symbols that appear relevant, then analyze them with 1-8 worker shards whose concurrent model calls follow `model_routes`.
3. Require reviewer and deterministic gate validation that a worker-reported issue can actually lead to the user's symptom.

Useful commands:
- `/find-root-cause <problem description>`
- `/find-root-cause --pattern-pack <path-or-dir> <problem description>`
- `/root-cause-patterns list [--type <project_type>] [--json]`
- `/root-cause-patterns match <problem symptom> [--json]`
- `/root-cause-patterns github-search [--type <project_type>] [--limit 20] [--out .kernforge/root_cause/github_issues.json] [query words...]`
- `/root-cause-patterns normalize --in .kernforge/root_cause/github_issues.json --out .kernforge/root_cause/pattern_pack.json [--type <project_type>]`
- `/root-cause-patterns validate [--in <pattern_pack.json>] [--json]`

Good prompt shape:

```text
/find-root-cause In <component/feature>, when <input/command/event sequence/state>, expected <normal behavior or invariant>, but observed <failure>. Frequency/env: <how often and where>. Repro/log/value: <exact prompt, API call, command, DB value, or log line>.
```

Examples:

```text
/find-root-cause In the party system, after inviting and kicking members repeatedly, expected the party size limit to block new invites, but observed extra members can still be invited.
/find-root-cause My Win32 service process does not stop through sc stop.
```

Current behavior:
1. With no prompt, the command prints usage and examples.
2. If affected component, trigger/repro, observed failure, or expected behavior/invariant is unclear, Kernforge prints the missing pieces and asks for a sharper `/find-root-cause ...` command before starting agents.
3. Source hints and an optional model clarity check reduce false rejections for natural-language Korean symptom reports.
4. Workspace scan, source path/symbol matches, built-in pattern priors, and explicit `--pattern-pack` inputs are combined into candidate code matches.
5. Worker count is estimated from code size and candidate count, from 1 up to 8 shards.
6. Workers focus on what happens when input parameters, DB/config values, cached state, counters, ids, enums, nullable references, and lifecycle state fall outside the range the code expects.
7. Worker candidates must include causal chain, evidence file/function, out-of-range case, required runtime observation, probes, and disproof conditions.
8. Reviewers check symptom overlap, complete causal stages, and evidence quality.
9. If reviewers need more proof, `evidence_requests` route additional focused shards, and rejected candidates stay in the audit trail as regression priors.
10. Deep verification rechecks reviewer-approved candidates with symbol-aware source excerpts and adjusts confidence breakdowns.
11. Final synthesis deduplicates candidates into clusters and reports confidence, instrumentation, verification probes, and "this is not the root cause if..." disconfirmation conditions.

Pattern pack workflow:
1. The built-in pack provides search priors for recurring bug classes in Windows services, Windows kernel drivers, Unreal clients/servers, web backends, and Go/CLI agents.
2. `/root-cause-patterns match` shows pattern candidates for the current workspace type and symptom text.
3. `/root-cause-patterns github-search` collects closed GitHub issues with bug, fix, or root-cause signals.
4. `/root-cause-patterns normalize` converts that issue corpus into a provisional pattern pack.
5. `/root-cause-patterns validate` reports pack quality issues.
6. Pattern packs are priors, not proof. `/find-root-cause` still requires current source evidence plus reviewer causality validation.

### Source-Level Function Fuzzing

Purpose:
1. Show how attacker-controlled parameters could open guard, probe, copy, dispatch, and cleanup paths directly from source.
2. Go beyond generic review comments by telling you which predicate to flip with which value and which sink opens afterward.
3. Let you start from either one function or one suspicious file and still converge on the most relevant input-facing path.

Useful commands:
- `/fuzz-func <function-name>`
- `/fuzz-func <function-name> --file <path>`
- `/fuzz-func <function-name> @<path>`
- `/fuzz-func <function-name> --source-scan focused`
- `/fuzz-func <function-name> --source-scan full`
- `/fuzz-func <function-name> --no-source-scan`
- `/fuzz-func --from-candidate <candidate-id>`
- `/fuzz-func --file <path>`
- `/fuzz-func @<path>`
- `/fuzz-func status`
- `/fuzz-func show [id|latest]`
- `/fuzz-func list`
- `/fuzz-func continue [id|latest]`
- `/fuzz-func language [system|english]`
- `/fuzz-campaign`
- `/fuzz-campaign run`
- `/source-scan run`
- `/source-scan run --limit 50`
- `/source-scan run --only-slugs probe-copy-size-drift,double-fetch-user-buffer`
- `/source-scan run --files driver/nsi.c,api/registry.c`
- `/source-scan list`
- `/source-scan show [id|latest]`
- `/create-driver-poc <driver-name> [--type objectfilter|minifilter|registryfilter|wfpcallout]`

Best used when:
1. You need fast triage on IOCTL handlers, parsers, validators, or buffer-processing code.
2. You know the suspicious file but not yet the best root function.
3. You want source-only reasoning about size drift, branch flips, check/use desync, or dispatch divergence before building a runtime harness.

Current behavior:
1. If you provide a function name, Kernforge resolves the symbol directly. If you provide only a file path, Kernforge expands through include/import plus actual call flow to pick a representative root automatically.
2. Planning no longer requires `analyze-project` or a prebuilt `structural_index_v2`; Kernforge can rebuild snapshot and semantic-index context on demand.
3. Kernforge extracts real guard, probe, copy, dispatch, and cleanup observations from function bodies and uses them to synthesize attacker input states.
4. Higher-risk findings now include concrete sample values, source-derived branch predicates, minimal counterexamples, branch outcomes, and downstream call chains.
5. Output is organized as `Conclusion`, `Risk score table`, `Top predicted problems`, and `Source-derived attack surface` so the most actionable finding is visible first.
6. By default, `/fuzz-func` reuses a matching `/source-scan` candidate or runs a focused source scan over the target and reachable files before saving the plan. Use `--source-scan off`, `--source-scan focused`, `--source-scan full`, or `--no-source-scan` to control this.
7. `/source-scan run` persists ranked function-window candidates and prints the natural next command, `/fuzz-func --from-candidate <candidate-id>`, for explicit candidate handoff. Each candidate records evidence spans, file/symbol fingerprints, confidence breakdown, dataflow/control-flow facts, stale-source state, and native feedback calibration.
8. Built-in source matchers include Windows kernel double-fetch, IOCTL output infoleak, WDF request buffer size drift, integer allocation overflow, and pool/refcount lifetime surfaces in addition to the existing probe/copy, dispatch, IRQL, callback, minifilter, Unreal RPC, and telemetry parser signals.
9. `/create-driver-poc <driver-name> [--type objectfilter|minifilter|registryfilter|wfpcallout]` generates an x64-only C++20 MSVC/WDK POC driver template. Omitting `--type` keeps the original WDM SCM/IOCTL ping POC; typed templates generate object manager process/thread access filtering, filesystem minifilter open/rename/delete user-mode decision messaging, registry create/open/set/delete/rename callback blocking, or WFP outbound callout blocking contracts.
10. Native execution is an optional follow-up. If build context such as `compile_commands.json` is missing, Kernforge explains the gap before asking whether to continue.
11. Artifacts are written under `.kernforge/fuzz/<run-id>/` with files such as `report.md`, `harness.cpp`, and `plan.json`.
12. `/fuzz-func` automatically prints a campaign handoff when source-only scenarios are ready, so the user can continue with `/fuzz-campaign run` instead of learning campaign internals.
13. `/fuzz-campaign` shows the next recommended campaign step and `/fuzz-campaign run` performs the safe automatic action, such as creating a campaign, attaching the latest useful run, promoting source-only scenarios into `corpus/<run-id>/`, updating deduplicated finding lifecycle and coverage gap entries, ingesting libFuzzer logs, llvm-cov text, LCOV, and JSON coverage summaries, capturing sanitizer reports, Windows crash dumps, Application Verifier, and Driver Verifier artifacts, and recording native run results into reports and evidence.
14. Campaign manifests now include a finding list, dedup keys, duplicate counts, merged native/evidence links, parsed coverage reports, run artifacts, coverage gaps, and artifact graph that link targets, seeds, native results, coverage reports, sanitizer/verifier artifacts, evidence ids, source anchors, verification gates, and tracked-feature gates.
15. Native crash findings merge by crash fingerprint, source anchor, and suspected invariant so repeated runs strengthen one tracked issue.
16. Coverage gaps feed the next generated `FUZZ_TARGETS.md` refresh so unexercised seed targets receive explicit ranking feedback.
17. `/fuzz-func ` completion shows function and file usage hints first, then switches to real file candidates after `@`.

Practical interpretation:
1. `Most useful branch delta` is usually the first line worth reading.
2. `Concrete hypothetical input examples` are internal analysis inputs synthesized by Kernforge, not instructions for manual reproduction.
3. `Source-derived attack surface` is the highest-confidence section because it comes from real function-body evidence.
4. Even high-score findings should still be checked against the cited source excerpt, especially when helper or exploit-side files appear in the closure.

### 2.1 Hook Engine

Purpose:
1. Warn, confirm, or block risky actions.
2. Inject extra review context and verification steps before verification runs.
3. Strengthen push and PR policy using recent evidence.
4. Create automatic checkpoints before risky flows.

Useful commands:
- `/hooks`
- `/hook-reload`
- `/init hooks`
- `/override`
- `/override-add <rule-id> <hours> <reason>`
- `/override-clear <override-id|rule-id|all>`

Current actions:
- `warn`
- `ask`
- `deny`
- `append_context`
- `append_review_context`
- `add_verification_step`
- `create_checkpoint`

Best used when:
1. Your team repeatedly hits signing, symbol, provider, XML, or scanner regressions.
2. Passing normal tests is not enough for approval.
3. You want repeatable PR and push guardrails instead of relying on memory.

Recommended operating model:
1. Start with the `windows-security` preset.
2. Add workspace-specific rules in `.kernforge/hooks.json`.
3. Begin with `warn` and `ask`.
4. Promote only repeat incident classes to `deny`.
5. Use `/override-add` only with an expiration and a reason.

### 2.2 Security-Aware Verification

Purpose:
1. Infer security-relevant categories from the changed files.
2. Build verification steps that match the change type.
3. Pull recent simulation and investigation context into verification planning.

Current categories and signals:
1. `driver`
2. `telemetry`
3. `unreal`
4. `memory-scan`
5. Recent high-risk simulation findings
6. Active investigations and live findings

Useful commands:
- `/verify`
- `/verify --full`
- `/verify src/foo.cpp,driver/guard.cpp`
- `/verify-dashboard`
- `/verify-dashboard-html`
- `/set-auto-verify [on|off]`
- `/detect-verification-tools`
- `/set-msbuild-path <path>`
- `/set-cmake-path <path>`
- `/set-ctest-path <path>`
- `/set-ninja-path <path>`

Best used when:
1. Generic `go test`, `msbuild`, or `ctest` is not enough.
2. You need signing, symbols, package, provider, XML, or verifier-oriented follow-up.
3. You already saw risky investigation or simulation findings and want them reflected in validation.

Operational notes:
1. `auto_verify` is now the master switch for edit-triggered verification.
2. When a Windows verification tool such as `msbuild`, `cmake`, `ctest`, or `ninja` is missing, Kernforge first tries to auto-detect and save a usable path for the workspace, then falls back to prompting if detection still fails.
3. Use quotes for paths that contain spaces, for example `/set-msbuild-path "C:\Program Files\Microsoft Visual Studio\2022\Community\MSBuild\Current\Bin\MSBuild.exe"`.
4. Model request timeout is configurable through `request_timeout_seconds`, while `max_request_retries` and `request_retry_delay_ms` control retries for timed-out or transient provider failures.
5. For long-running local validation, prefer `run_shell_background` plus `check_shell_job` so the agent can reuse one expensive build or test job across multiple turns.
6. When a setup, formatter, or generator command is genuinely safer than a manual patch, the agent can use `run_shell` with scoped workspace writes by declaring `allow_workspace_writes=true` and a narrow `write_paths` list.
7. In interactive shell mode, use `!cd ..` freely to move back up within the workspace after drilling into a subdirectory. Kernforge rejects only the step that would leave the workspace or active worktree boundary.

### 2.3 Evidence Store

Purpose:
1. Store verification, override, investigation, and simulation output as structured evidence.
2. Give you a fast way to inspect recent failed or high-risk signals.
3. Feed recent state back into hooks and verification planning.

Useful commands:
- `/evidence`
- `/evidence-search <query>`
- `/evidence-show <id>`
- `/evidence-dashboard [query]`
- `/evidence-dashboard-html [query]`

Common evidence kinds:
1. `verification_category`
2. `verification_artifact`
3. `verification_failure`
4. `hook_override`
5. `investigation_session`
6. `investigation_snapshot`
7. `investigation_finding`
8. `simulation_run`
9. `simulation_finding`

### 2.4 Persistent Memory

Purpose:
1. Keep important context across sessions.
2. Let you find earlier decisions, failures, and verification context later.
3. Support long-running investigations and repeated regression classes.

Useful commands:
- `/mem`
- `/mem-search <query>`
- `/mem-show <id>`
- `/mem-dashboard [query]`
- `/mem-dashboard-html [query]`

Strength:
1. It stores more than text. It also stores verification categories, tags, artifacts, failures, severities, signals, and risk.

### 2.5 Live Investigation Mode

Purpose:
1. Capture live Windows state as investigation snapshots.
2. Store live findings as evidence and memory.
3. Feed those live findings into simulation and verification later.

Useful commands:
- `/investigate`
- `/investigate start <preset> [target]`
- `/investigate snapshot [target]`
- `/investigate note <text>`
- `/investigate stop [summary]`
- `/investigate list`
- `/investigate show <id>`
- `/investigate dashboard`
- `/investigate dashboard-html`

Current presets:
1. `driver-visibility`
2. `process-visibility`
3. `provider-visibility`

Best used when:
1. Static code review is not enough.
2. You need to capture live verifier, module, driver, service, or provider state before editing.
3. You want a reusable record of the real runtime state that informed later decisions.
4. You want a lightweight visibility triage snapshot before deeper debugging.

Important scope limit:
1. `driver-visibility` is not a deep root-cause analyzer for driver load failures.
2. Its current implementation is intentionally narrow and focuses on user-mode-visible driver, service, filter, verifier, and artifact state.
3. `process-visibility` is a process-listing triage snapshot, not a process attach or protection analyzer.
4. `provider-visibility` is a provider-listing triage snapshot, not a deep ETW or provider root-cause analyzer.

### 2.6 Adversarial Simulation Profiles

Purpose:
1. Evaluate recent evidence and investigation state through a lightweight risk lens.
2. Surface tamper, visibility, and forensic blind spots.
3. Feed that heuristic context back into review, edit, plan-review, and verification flows.

Useful commands:
- `/simulate`
- `/simulate tamper-surface [target]`
- `/simulate stealth-surface [target]`
- `/simulate forensic-blind-spot [target]`
- `/simulate list`
- `/simulate show <id>`
- `/simulate dashboard`
- `/simulate dashboard-html`

Current profiles:
1. `tamper-surface`
2. `stealth-surface`
3. `forensic-blind-spot`

Best used when:
1. You care about integrity or registration risk.
2. You suspect observer or telemetry visibility gaps.
3. You worry that post-incident artifacts may be too weak.

Important scope limit:
1. Simulation is a heuristic risk review, not proof of exploitability.
2. The profile names describe interpretation lenses, not offensive capability.

### 2.7 Selection-First Review And Edit

Purpose:
1. Review or edit only the selected code range instead of the whole file.
2. Route selection review through the common `ReviewRun` harness and gate instead of a one-off prompt.
3. Automatically inject recent simulation findings when they match the selected area.
4. Inspect workspace and selection diffs in a richer Windows diff surface before wider review or editing.

Useful commands:
- `/open <path>`
- `/selection`
- `/selections`
- `/diff`
- `/diff-selection`
- `/review selection [extra]`
- `/review selection --all [extra]`
- `/edit-selection <task>`
- `/note-selection <text>`
- `/tag-selection <tag[,tag2]>`

Diff workflow notes:
1. On Windows, `/diff` and `/diff-selection` prefer the internal WebView2 diff viewer.
2. The read-only diff viewer includes changed-file navigation, unified/split toggles, and intraline highlights.
3. If the internal surface is unavailable, Kernforge falls back to terminal output.
4. In `Open diff preview?`, answering `a` auto-accepts the current edit and skips future diff previews for the rest of the session.

Best used when:
1. You want to focus on a single IOCTL handler, integrity check, or provider registration block.
2. You want to connect a recent simulation finding directly to the relevant code.

Review artifacts:
1. `/review selection` writes `.kernforge/reviews/latest.json` and `.kernforge/reviews/latest.md`.
2. The result includes typed findings, request class, lifecycle phase, route mode, request-class reason, freshness/redaction state, gate status, scope discovery, repair steps, runtime gate ledger, and recommended next commands.
3. MCP clients get the same structure through `kernforge_review`, including additive `request_class` and `lifecycle` fields plus `model_plan`, `reviewer_runs`, `latest_review_freshness`, `edit_proposals`, `runtime_gate_ledger`, `single_model_second_pass`, `cross_review_triage`, `review_observability`, `scope_discovery`, and action-contract fields on `next_commands`.
4. Protocol artifacts also include action envelopes, approval ledger, capability manifest, external lookup intents, artifact integrity, ledger consistency, resume sanity, state transitions, route health, document gate status, single-model second-pass status, and cross-review triage so CLI output, Markdown reports, JSON artifacts, and MCP responses describe the same lifecycle state without dumping raw model output into MCP.

Review UX/Ops observability:
1. Already implemented runtime enforcement remains in the review gate ledger, cross-review triage ledger, enforced single-model second-pass phase, and pre-final answer completeness gate.
2. New request-class-aware orchestration classifies the user request as `review_only`, `document_artifact`, `review_then_modify`, `modify_then_review`, `verification_only`, `validation_only`, or `general`. The selected class, reason, confidence, ambiguity flag, ambiguity warnings, and class-specific final-answer contract are persisted on `ReviewRun`, `RuntimeGateLedger`, status surfaces, artifacts, and MCP review responses.
3. In single-model mode, Kernforge treats the route as an explicit staged lifecycle, not as independent approval. The compact state exposes classify/context/review-or-implementation/second-pass/verification/final phases, whether second-pass ran, was cached, or was skipped, and why any skip is only a disclosure rather than approval.
4. A configured reviewer-only post-change route keeps the single-model policy state visible but avoids spending another primary second-pass call when that reviewer is already the only post-change reviewer. Weak, empty, malformed, stale, or timed-out reviewer output degrades the route; deterministic blockers and required verification gaps remain stronger than weak reviewer approval.
5. In cross-model mode, primary implementation responsibility stays separate from reviewer feedback. Actionable reviewer items must enter `cross_review_triage` as `accepted_fixed`, `accepted_deferred`, `rejected_with_reason`, or `needs_user_decision`; missing fix evidence, deferral reason, technical rejection reason, or user-decision guidance stays visible as an obligation.
6. Document-artifact requests use artifact-quality checks as the primary gate: requested artifact exists, requested topic is covered, content is not placeholder/TODO-only, and the final answer does not claim unsupported shell or code verification. Document-only report creation does not become a code-modification lifecycle, while a report plus source-code edit reopens modification review and validation obligations.
7. The UX/Ops layer exposes lifecycle decisions through `/status`, `/hooks`, session dashboard, Markdown artifacts, JSON artifacts, and MCP responses. The compact status view shows latest review id, request class, classification confidence/ambiguity, lifecycle phase, trigger/target/mode, route mode and route quality, review/repair/document/verification/final-answer gate status, gate verdict/action, second-pass state, triage counts by status, incomplete triage blockers, remaining obligations, final-answer correction state, blocker classes, and the next recommended command.
8. `/status` is compact by default. `/status detail` expands evidence-heavy sections, including the lifecycle timeline: `classified_request`, `collecting_context`, `pre_write_review`, `applying_change`, `post_change_review`, `single_model_second_pass`, `cross_review_triage`, `artifact_quality_gate`, `verification`, `final_answer_contract`, `blocked`, and `completed`. Each phase carries status, short reason, evidence ref when available, and next safe action when blocked or skipped.
9. Progress output uses the same phase language. Long waits say whether Kernforge is waiting on the primary model, cross reviewer, local tool, verification command, or final-answer correction, and completion wording is not printed before the relevant gate has actually passed.
10. Blocker classes separate `code_repair_blocker`, `reviewer_route_problem`, `evidence_gap`, `verification_gap`, `document_artifact_quality`, `final_answer_contract`, and `user_decision_required`. This keeps a primary code blocker above cross-review route warnings or document artifact details.
11. Cross-review triage artifacts render each finding with id/title, reviewer route, severity, location, status, reason, required fix, fix evidence, verification evidence, and safe continuation guidance. `needs_user_decision` entries include concrete inspect targets, safe-to-change scope, do-not-change-yet scope, and a next command instead of asking the user to infer the next step from raw review text.
12. Single-model mode is first-class, not degraded. Status and reports distinguish `single_model_second_pass_ran`, `single_model_second_pass_cached`, `single_model_second_pass_skipped`, `cross_model_review_ran`, and `reviewer_only_post_change_review_used`; a single-model second pass is never described as independent cross-review.
13. Document-artifact flows show artifact path, artifact-quality status, source-review evidence status when relevant, verification skip or required reason, and remaining limitations. A document-artifact final-answer correction is shown as a final-answer contract correction, not a code repair.
14. Final-answer completeness corrections are exposed as lifecycle facts: changed-file disclosure, review/self-review disclosure, validation disclosure, remaining-risk disclosure, review-only findings-first/no-edit formatting, or document-artifact path/quality/verification/limitation disclosure. Generic phrases such as "done", "patched", or "implemented" are never sufficient evidence by themselves.
15. MCP operator-card fields are additive and backward-compatible: `lifecycle_timeline`, `compact_status`, `blocker_summary`, `route_quality`, `final_answer_contract_status`, and `next_recommended_command`. MCP consumers can render a clear operator card without parsing prose or raw model output.
16. Known residual limits: raw model output stays in local artifact refs, MCP responses remain additive/backward-compatible instead of dumping transcripts, generated-document artifact skip behavior remains intentionally separate from code review gates, classification remains conservative for ambiguous natural language, and full-package tests may still need cluster-level diagnosis before a single all-in-one run is useful.

Review/Ops test strategy:
1. Start with `go test ./cmd/kernforge -list .` to confirm the package compiles and inventory current tests.
2. Use `go test -json ./cmd/kernforge -count=1 -timeout 2m` or `-v` when diagnosing a silent timeout; plain `go test` may print nothing until the package exits.
3. Run UX/Ops regressions as a focused cluster: `go test ./cmd/kernforge -run "TestOperator|TestRuntimeGate|TestReviewMCPResponse|TestMCPStatus|TestCrossReview|TestSingleModel|TestPreFinalHarness|TestFinalAnswerContract" -count=1`.
4. Run existing behavior clusters separately when needed: `TestAgent|TestApplyEditProposal|TestRunShell|TestVerification|TestProjectAnalyzer`.
5. The 2026-05-28 timeout investigation saved a JSON trace under `.kernforge/test-logs/` and showed the short 2 minute budget expired in the project-analysis cluster after earlier agent/review tests consumed most of the time; the practical repeatable path is cluster isolation before a full `go test ./cmd/kernforge -count=1 -timeout 15m`.

### 2.8 Plan Review Workflow

Purpose:
1. Review implementation plans through the same common review harness used for code, selection, PR, goal, final, and analysis reviews.
2. Use the active main model as the primary review route and optional `/model cross-review` as the independent second-pass route; design/security/false-positive/test concerns are review lenses, not separate model routes.
3. Execute only when the gate and user flow allow it.

Useful commands:
- `/review plan <task>`
- `/model cross-review status`
- `/model cross-review`
- `/model cross-review <provider> [model]`
- `/model clear cross-review`
- `/review waive <finding-id> --reason <text>`

Best used when:
1. A change spans multiple components.
2. Order of operations, rollback points, or operational caution matter.
3. You want simulation findings to shape the implementation plan before edits begin.

Current integration:
1. Recent simulation findings that match the task are injected into the review evidence pack.
2. The gate records objective fit, architecture risk, testability, security boundary, maintainability, and evidence gaps as structured findings.
3. Multi-model review is limited to primary plus optional cross route. Missing domain-specific roles are not reported; the planner records `required_lenses` and `optional_lenses` instead.
4. Unless an explicit timeout policy overrides it, model reviewer requests use bounded per-attempt timeouts so long preflight waits fail quickly and hand control back to the recovery path.
5. Natural-language review requests such as `@file:line-line review this` route to `/review selection`; focused review-and-fix requests are classified as `review_then_modify`, first run review, then continue the repair flow from the latest findings.
6. Focused review requests use a smaller evidence and prompt budget. Automatic pre-write review is diff-first and uses the proposed diff, edit proposal, and required repair findings before any broader context. For range-focused pre-write evidence, the current file context guarantees the selected range through the enclosing function end when possible, and `function_body_excerpt` is added as a separate source.
7. Automatic pre-write review runs on valid edit previews before an edit is applied, and automatic post-change review runs after changed paths exist.
8. Service, SCM, driver, and sensitive-path signals add the `security` lens. Detection, telemetry, scan, spoofing, and evasion-quality surfaces add the `false_positive` lens.
9. Review progress explicitly names the review route and provider when it differs from the main model, then prints the gate result and finding counts. It also logs the current main/cross phase, context mode, retry budget, and soft timeout before each model call, and long-wait progress explains whether Kernforge is waiting on the main first-pass review or the cross-check reviewer.
10. Simple exact edits can use `apply_edit_proposal`, which records file, operation, exact search, replacement/content, rationale, risk, preview fingerprint, and review evidence before the write. `apply_patch` remains available as a complex hunk-level fallback.
11. Runtime gate freshness links review, patch transaction, verification, completion audit, and final-answer review. Stale review coverage or unwaived blockers can block final answers, explicit git writes, MCP write-side responses, and completion audit readiness until `/review`, verification, or the displayed `next_command` repairs the ledger.
12. Invalid patch recovery normalizes common wrapper problems and records repeated patch signatures so the agent stops resubmitting the same malformed patch and refreshes target-file context instead.
13. Provider behavior drives review token caps, omission retry budgets, schema strictness, and recovery prompts. Weak or incomplete high-severity model findings are downgraded to evidence-gap warnings unless they include a concrete path or symbol, evidence, impact, and required fix. Finding locations can carry a structured `line` anchor, either from `line: n` or `path: file:line`, so downstream repair handoff does not have to scrape prose for the code location. `test_gap` is reserved for pure test or verification work; if a reviewer mislabels a production-code repair as `test_gap` but its `required_fix` changes implementation behavior, the repair plan still keeps it as an actionable obligation. DeepSeek review omission retries are intentionally tight to avoid repeated multi-minute strict-review loops. For optional cross-checks, if the main first-pass review already produced usable actionable findings and the reviewer stop reason is not an explicit token-limit or truncation signal, Kernforge does not run another DeepSeek strict retry just because the cross-check looked abbreviated. Focused and pre-write cross-reviewer calls normally use a 3 minute soft timeout, but if the configured review model is ranked lower than the active main model, Kernforge automatically extends that soft timeout to 5 minutes before treating the route as timed out.
14. Pre-fix review findings are surfaced to the user before any edit tool is allowed to run. When a repair turn has structured RF items, Kernforge emits and stores a deterministic `Review findings:` summary with those IDs and the intended repair direction before the implementation model starts, then still guards edit tools if no visible summary exists.
15. Local code review and repair turns stay on local source evidence. Web/search/browser MCP tools are hidden from the tool list and blocked before execution unless the user explicitly asks for external research. If the model still attempts web research, progress logs the query or URL it tried to check, then redirects the turn back to local code evidence. When the active task itself is a latest/current research request, continuation turns preserve that web-research intent until a web result exists, but fresh local code, git, or verification requests reset the priority back to local evidence.
16. In explicit fix flows, complete high-severity findings and actionable medium correctness, stability, or performance findings block the repair gate even when they are not security findings. Low-severity style, formatting, and maintainability findings stay as pre-fix warnings unless explicitly marked as blockers.
17. Pre-write review treats build/test verification gaps as post-edit obligations, not reasons to block the edit preview. Warnings that say only "verification was skipped" stay visible but do not force another patch rewrite, while patch-local style problems such as Allman brace or indentation violations do block so the edit can be corrected before writing. A verification report is treated as current only if it was produced for the current patch transaction and changed paths; stale session or persisted verification history becomes a runtime-gate warning rather than a review blocker. Artifact-writing build/test shell commands ask with the same pinned confirmation style as diff preview (`Run automatic verification now? [y/N/a=auto-run]`), and non-interactive `-prompt -y` auto-approves that prompt just like diff preview. The lifecycle mirrors Codex command approvals: request approval, record the user decision, then record execution/result evidence only if the command actually ran. Background verification starts are pending evidence gaps until a poll records a completed pass or failure. Once verification is declined or skipped, same-turn shell verification retries and `latest` background polls are rejected with `NOT_EXECUTED` before any new shell/progress status is emitted. A final answer that discloses skipped verification satisfies the disclosure obligation only; it does not create successful evidence, so changed paths without a successful verification report keep the edit-loop status at `risk_accepted`. Background job bundle metadata keeps job-list evidence in `job_entries` and reserves scalar `job_status` for single-job state. After an edit, failed automatic verification is classified against the current patch scope before Kernforge asks the model to repair. Only direct command/scope references or failure lines that name the changed path can continue the repair loop; workspace or sibling-file failures without changed-path evidence are reported as verification risk and must not broaden edits into unrelated source or project files. After an out-of-scope automatic verification failure, same-turn build/test/verification retry or probing calls are also rejected with `NOT_EXECUTED`, forcing the model to disclose the external/ambient blocker instead of searching for alternate project repairs. Build artifact churn from verification commands is tolerated, but source/config file mutations are still rejected outside the edit review gate. C++/MSBuild adaptive verification prefers the nearest `.vcxproj` for changed source files and includes that project's declared `Configuration|Platform` properties when available, so MSBuild does not silently use an unsupported default platform. Full solution builds stay explicit full verification. Configured verification tool paths are normalized at the shell boundary; on PowerShell, quoted executable paths are invoked with `&` so a detected MSBuild/CMake/CTest/Ninja path does not become a string literal. Verification summaries keep both the logical command and the resolved shell command when they differ, and missing-tool classification keys off the primary executable instead of unrelated compiler/build text.
    Non-bypass non-interactive `-prompt` runs still render the verification plan plus the pinned confirmation label and read piped answers. EOF or missing stdin becomes a visible skipped/declined verification decision, not an invisible default approval or a fake tool failure.
    After an out-of-scope automatic verification failure, Kernforge also removes all tool definitions from the next model request so the route is forced into final-answer-only mode. If a scripted or degraded route still emits a tool call, Kernforge answers it as `NOT_EXECUTED` and repeats the terminal-state guidance. A final answer produced in this terminal state is not routed back through post-change review or final-answer repair gates for the same external blocker; Kernforge only ensures that the verification risk is disclosed.
    Final-answer text is sanitized outside code fences to collapse accidental repeated sentence runs and fix adjacent Korean sentence spacing from degraded/local routes.
    Review, final-answer, and completion-audit ledgers are scoped to the patch transaction when one exists. Ambient dirty git files remain visible for git-write gates, but they do not invalidate a review of the current repair patch.
18. Claude Code CLI built-in choices display the current Claude family version, but Kernforge passes CLI-safe aliases such as `sonnet`, `opus`, and `haiku` to the command. `/review`, natural-language review, and pre-fix repair checks are main-first: the active main model produces the first structured review from local evidence, then the optional cross route runs over the same evidence plus the primary draft. When no distinct cross route is configured, Kernforge runs a separate `single_model_second_pass` review phase through the primary route instead of relying only on prompt instructions. That pass receives the original request, touched files, relevant diff, implementation reply, latest verification summary, and first-pass review, then returns normal `ReviewFinding` records consumed by the same gate and repair loop. Accepted second-pass fingerprints are cached so identical reviewed diffs do not create infinite loops. The selected request class decides whether this is read-only review evidence, review-before-repair evidence, post-change self-review evidence, or document-artifact validation disclosure. Cross-review failures, empty responses, or `weak` output degrade the run and stay visible, but do not prevent the main review findings from being reported or the repair loop from starting. Actionable cross-review findings are persisted in `cross_review_triage` and must become exactly one of `accepted_fixed`, `accepted_deferred`, `rejected_with_reason`, or `needs_user_decision`; missing fix evidence, deferral reasons, technical rejection reasons, or actionable user-decision guidance become deterministic review blockers.
19. The cross review route defaults to at least `effort=high`, and saved `low`/`medium` values are raised to `high` when the reviewer request is built. Focused pre-fix bug-hunt reviews keep that minimum even when the selected cross route was configured with a lower effort. Pre-write review remains the hard edit gate: once a concrete edit preview exists, a required main or cross reviewer that fails, returns an empty response, or completes with `weak` quality blocks the write as `insufficient_evidence`; Kernforge stops before touching files and reports the reviewer route problem instead of asking the implementation model to retry or gather web evidence. If the main first-pass pre-write review was usable, the stop message also offers an explicit fallback phrase (`proceed with the main model review`). When the user replies with that phrase and an interactive diff preview is available, Kernforge reruns the pre-write review with the cross-reviewer failure recorded as degraded evidence but no longer as a hard blocker, then still requires the normal diff preview confirmation before writing.
20. When pre-write review approves proceeding to diff preview, Kernforge prints the final review body before asking for diff preview confirmation. The visible body includes the verdict, blocker/warning counts, repair targets checked, remaining review items, evidence, impact, required fix, and test recommendation, and long fields are not hidden behind `...` ellipses.
21. If a reviewed repair still fails the pre-write gate or exhausts its narrow inspection budget, Kernforge reports that the review did not pass, shows the latest review result and selected edit proposal, and asks `Should I keep repairing from this review result? [y/N]`. The confirmation is session state, not a natural-language prompt: only `y` resumes from the stored review/proposal, and `n` stops.
    After edit-target mismatch or pre-write repair blocking, broad recovery `apply_patch` calls are deferred without modifying files. A blocked pre-write proposal is not treated as current workspace state: before the next edit tool can run, the model must re-anchor with `read_file`, `grep`, or `git_diff`. The next edit must be a current-file, narrow standalone patch; repeated broad recovery attempts stop with the latest review and proposal shown to the user.
22. External verification callbacks are normalized into the same runtime evidence contract as built-in verification. When a successful callback report omits `GeneratedAt`, `Trigger`, `Workspace`, or `ChangedPaths`, Kernforge fills them from the automatic verification request before review evidence and runtime-gate freshness are computed.
23. Non-interactive single-shot runs (`-prompt`, `-command`, `-goal`, and `-goal-file`) do not install the interactive cancel watcher. Mirroring Codex-style explicit event/decision boundaries, ambient keyboard state that cannot be confirmed by the user is not promoted to request cancellation, and long review/repair/goal loops stop only on real model, tool, or approval state.
24. Normal work turns print an elapsed-time line at completion. Local meta commands such as `/exit`, `/status`, `/config`, and `/model` suppress the elapsed-time footer.
25. The 16.10 hardening layer records review action envelopes, approval ledgers, capability manifests, external lookup intents, artifact integrity, ledger consistency, resume sanity, and route health in the review run. Replay fixtures cover reviewer route failure, omission/truncation, patch mismatch loop, local web blocking, pre-fix repair obligations, final visible summary, and MCP response contracts; the companion `REVIEW_HARNESS_UX_OPS_85_DESIGN_kor.md` describes the next UX/operations target toward Codex App parity.
26. Before a modification or local review final answer is exposed, the pre-final coding harness now enforces completion facts by request class. Modification replies must name changed files or clearly say no files changed, state review/self-review outcome, state validation outcome or why validation was not run, and state remaining risk or no known blocker. Review-only replies must remain read-only, lead with findings or an explicit no-finding result, and mention residual test/evidence risk when no issue is found. Review-then-modify replies must expose the review finding basis before repair, then summarize the scoped patch, post-change review or second-pass state, validation, and residual risk. Generated-document-only and trivial status/command turns are skipped from code-review completion gates so this gate does not over-block non-code flows; document artifacts still need artifact path and artifact-quality validation disclosure.

### 2.9 Tracked Feature Workflow

Purpose:
1. Create a long-lived feature workspace instead of a disposable plan.
2. Persist spec, plan, task, and implementation artifacts under `.kernforge/features/<id>`.
3. Separate planning from execution so large changes can be resumed safely.

Useful commands:
- `/new-feature <task>`
- `/new-feature list`
- `/new-feature status [id]`
- `/new-feature plan [id]`
- `/new-feature implement [id]`
- `/new-feature close [id]`

Best used when:
1. A feature will span multiple sessions or handoffs.
2. You want explicit artifacts for scope, sequencing, and acceptance tracking.
3. You want implementation to be an explicit follow-up step instead of happening immediately after planning.

Current integration:
1. `/new-feature <task>` behaves like `/new-feature start <task>` and creates `feature.json`, `spec.md`, `plan.md`, and `tasks.md`.
2. The created feature becomes the active feature in the session status.
3. `/new-feature implement [id]` executes the saved tracked plan and writes `implementation.md`.

### 2.10 Interactive Ergonomics

Purpose:
1. Reduce typing friction in long investigative and verification-heavy sessions.
2. Make command discovery faster when subcommands or ids are easy to forget.

What `Tab` completion now covers:
1. Slash commands
2. Workspace paths and `@file` mentions
3. MCP resource and prompt targets
4. Fixed command arguments such as `/set-auto-verify on|off`, `/progress-display auto|compact|stream`, `/progress_display auto|compact|stream`, `/permissions`, `/checkpoint-auto`, `/provider status|openai-codex-subscription|openai-codex-cli|openai-api|anthropic-claude-cli|anthropic-api|deepseek|openrouter|opencode|opencode-go|ollama|lmstudio|vllm|llama.cpp`, `/profile list|pin|unpin|rename|delete`, `/model cross-review|clear cross-review|status`, `/verify --full`, `/investigate start <preset>`, `/simulate <profile>`, and `/analyze-project --mode <mode>`
5. Saved ids for `/resume`, `/evidence-show`, `/mem-show`, `/mem-promote`, `/mem-demote`, `/mem-confirm`, `/mem-tentative`, `/investigate show`, `/simulate show`, and `/new-feature status|plan|implement|close`
6. Inline descriptions for command and subcommand suggestions so the completion list explains what each candidate does

Prompt budget behavior that now matters:
1. Cached `analyze-project` summaries can be injected ahead of auto-scouted code snippets when they are more relevant.
2. If the cached project analysis and architecture fact pack are sufficient to answer a question, Kernforge can reply without spending extra tool iterations.
3. Deep project-structure answers are evaluated against deterministic facts, source anchors, closed directory sets, and flow invariants; contradictions trigger tool use instead of a confident cached answer.
4. Skill and MCP catalogs are now included in full only when the request is actually asking about them.
5. Auto-scout contributes fewer candidates and less text, and it now focuses on locate/definition/reference-style requests.
6. The default `max_tokens` is `8192`; config files that still hold the old default `4096` are migrated at startup or `/reload`.
7. The default `max_tool_iterations` is `0` (unlimited). File search and large documentation turns no longer stop at the old default 16-tool cap unless you explicitly re-pin a positive limit, for example `/set-max-tool-iterations 24`.
8. When project analysis workers or review routes share the same OpenRouter or DeepSeek route as the main model, the default model-route limit is 2 to reduce upstream rate-limit or dynamic-concurrency cascades. Override `model_routes.provider_limits.openrouter` or `model_routes.provider_limits.deepseek` only when your key/provider pool can sustain more concurrency.
9. `/analyze-project` now uses the same progress ledger as tool/model streaming: `auto` records durable shard/wave and model-wait updates, `compact` keeps them in the footer, and `stream` records every update for long-run debugging.

## 3. Recommended Real-World Flows

### 3.1 Driver Hardening Or Signing-Sensitive Work

Situation:
- You changed `driver/guard.cpp` or `driver/guard.inf`.
- Signing, symbols, verifier, or packaging readiness matters.
- Similar failures happened recently.

Recommended flow:
1. `/investigate start driver-visibility guard.sys`
2. `/investigate snapshot`
3. `/investigate note current driver visibility snapshot captured before edit`
4. `/simulate tamper-surface guard.sys`
5. `/open driver/guard.cpp`
6. Select the relevant protection logic in the viewer.
7. `/review selection integrity risk paths and verifier interactions`
8. `/edit-selection harden registration and signing assumptions`
9. `/verify`
10. `/evidence-dashboard category:driver`
11. `/mem-search category:driver signal:signing`
12. `/investigate stop hardened signing path reviewed`

What Kernforge adds here:
1. A live driver-visibility capture before editing.
2. A tamper-oriented risk review before editing.
3. Automatic risk-oriented prompt context during review and edit.
4. Driver-aware verification steps plus recent investigation and simulation follow-up review steps.
5. Evidence-aware push or PR policy later.

### 3.2 Telemetry Provider Drift Or XML And Manifest Regression

Situation:
- You changed a provider manifest and registration logic.
- Runtime visibility is uncertain.
- You also care about observer coverage and post-incident traceability.

Recommended flow:
1. `/investigate start provider-visibility MyProvider`
2. `/investigate snapshot MyProvider`
3. `/simulate stealth-surface MyProvider`
4. `/open telemetry/provider.man`
5. Select the manifest region.
6. `/review selection provider visibility and schema drift`
7. `/open telemetry/register_provider.cpp`
8. `/edit-selection align provider registration and fallback visibility`
9. `/verify`
10. `/evidence-search category:telemetry outcome:failed`
11. `/simulate forensic-blind-spot MyProvider`
12. `/mem-search category:telemetry signal:provider`
13. `/investigate stop provider contract and visibility reviewed`

Why this works well:
1. Investigation captures real provider state.
2. Stealth simulation asks whether you can still observe the path.
3. Forensic blind spot simulation asks whether later reconstruction will still work.
4. Verification turns those concerns into explicit review steps.

### 3.3 Memory Scan Or Pattern Scan Regression Work

Situation:
- You are adjusting scanner logic for false positives, false negatives, or evasion resistance.
- Recent scanner-related failures already exist.

Recommended flow:
1. `/simulate stealth-surface scanner-core`
2. `/open scanner/patternscan.cpp`
3. `/review selection false positives, stealth coverage, and performance ceilings`
4. `/edit-selection reduce false positives without weakening evasion coverage`
5. `/verify`
6. `/evidence-dashboard category:memory-scan`
7. `/mem-search category:memory-scan risk:>=70`

Why this works well:
1. Scanner work is usually about coverage and evasion, not just correctness.
2. Simulation brings an extra risk lens into the prompt.
3. Verification reasserts those review concerns before the loop closes.

### 3.4 Large Multi-Step Change With Plan Review

Situation:
- The change spans driver and telemetry concerns together.
- Ordering, rollback, and review discipline matter.

Recommended flow:
1. `/simulate tamper-surface guard.sys`
2. `/simulate forensic-blind-spot guard.sys`
3. `/review plan harden driver registration, improve telemetry visibility, and preserve post-incident artifacts`
4. Let the reviewer critique the plan.
5. Execute the approved plan.
6. `/verify`
7. `/evidence-dashboard`

Current strength:
1. Simulation findings can shape the planning prompt.
2. They can also shape the final plan execution prompt.

### 3.5 Tracked Feature Lifecycle Across Multiple Sessions

Situation:
- The work is substantial enough that you want durable planning artifacts.
- You expect implementation, verification, and closure to happen over more than one sitting.

Recommended flow:
1. `/simulate tamper-surface guard.sys`
2. `/new-feature harden driver registration, preserve telemetry audit artifacts, and document rollback points`
3. `/new-feature status`
4. Review the generated `spec.md`, `plan.md`, and `tasks.md` under `.kernforge/features/<id>`.
5. `/new-feature implement`
6. `/verify`
7. `/new-feature close`

Why this works well:
1. The feature state survives session boundaries.
2. Planning artifacts are explicit and easy to inspect or regenerate.
3. Execution is intentionally separated from planning, which reduces accidental long-running edits from a rough first draft.

## 4. Command-By-Command Practical Usage

### 4.1 `/investigate`

Basic usage:

```text
/investigate start driver-visibility guard.sys
/investigate snapshot
/investigate note verifier enabled on target system
/investigate stop initial driver state captured
```

Good use cases:
1. Before editing, when you want the current driver visibility or verifier state on record.
2. When you want a quick triage snapshot before deeper driver load debugging.
3. When you want to confirm a telemetry provider is really visible live.
4. When you want a reusable runtime record that later verification and review can reference.

Key interpretation:
1. Investigation does not replace verification.
2. It captures the real-world state that should inform later work.
3. In particular, `driver-visibility` is a lightweight visibility snapshot, not a full driver load analyzer.

### 4.2 `/simulate`

Basic usage:

```text
/simulate tamper-surface guard.sys
/simulate stealth-surface MyProvider
/simulate forensic-blind-spot game.exe
```

Good use cases:
1. Right after a driver change, to look for integrity or registration risk surface.
2. Right after a telemetry change, to inspect observer visibility gaps.
3. When you want to know whether post-incident artifacts will still be usable.

Key interpretation:
1. Simulation is not proof of exploitation.
2. It is a structured way to highlight heuristic risk signals that deserve review.

### 4.3 `/review selection` And `/edit-selection`

Basic usage:

```text
/open driver/guard.cpp
/review selection check risk surfaces and cleanup paths
/edit-selection harden the selected registration path
```

Good use cases:
1. When only one function or block matters.
2. When you want recent simulation findings tied directly to the selected area.

Current automatic behavior:
1. If recent simulation findings match the selected path, Kernforge injects `Additional simulation risk focus` into review and edit prompts.

### 4.4 `/review plan`

Basic usage:

```text
/review plan harden driver load validation, improve telemetry provider visibility, and preserve audit artifacts
```

Good use cases:
1. Large or high-risk changes.
2. Work where rollback points and sequencing matter.
3. Cases where risk-oriented thinking should shape the implementation plan before edits begin.

Current automatic behavior:
1. Matching recent simulation findings are injected into the planning prompt.
2. They are also injected into the execution prompt after approval.

### 4.5 `/new-feature`

Basic usage:

```text
/new-feature harden driver registration, preserve telemetry audit artifacts, and document rollback points
/new-feature status
/new-feature plan
/new-feature implement
/new-feature close
```

Good use cases:
1. New features that need durable scope and execution artifacts.
2. Work that should pause after planning so you can review or resume later.
3. Changes that benefit from an active feature id in session context.

Current automatic behavior:
1. A tracked feature workspace is created under `.kernforge/features/<id>`.
2. `spec.md`, `plan.md`, and `tasks.md` are regenerated when you start or re-plan the feature.
3. `/new-feature implement [id]` executes the saved plan and writes `implementation.md`.
4. `status`, `plan`, `implement`, and `close` accept unique id prefixes as well as full ids.

### 4.6 `/verify`

Basic usage:

```text
/verify
/verify --full
/verify driver/guard.cpp,telemetry/provider.man
```

What the planner currently considers:
1. Changed files
2. Security categories
3. Verification policy
4. Verification history tuning
5. Hook-injected context and extra steps
6. Recent investigation and simulation state

Good use cases:
1. After editing, when you want a real verification plan instead of a generic test command.
2. When recent investigation or simulation findings should influence validation.
3. When you want security-aware review steps in addition to build or test steps.

### 4.7 `/evidence-search` And `/evidence-dashboard`

Useful queries:

```text
/evidence-search category:driver outcome:failed
/evidence-search kind:simulation_finding severity:critical
/evidence-search signal:tamper risk:>=60
/evidence-dashboard category:telemetry
```

Good use cases:
1. When you want to inspect what simulation just produced.
2. When you want only recent signing, provider, or scanner-related failures.
3. When you want to see active overrides and recent high-risk state together.

### 4.8 `/mem-search`

Persistent memory is also injected automatically before the model sees a new turn. Kernforge now includes a small `Workspace continuity` section with recent high-value records from the same workspace, then adds `Query matches` when the current prompt has file mentions, ASCII search terms, or structured filters. When continuity memory is injected, a visible `memory` activity line lists the reused memory ids and compact summaries. This helps a fresh session remember recently touched files, verification outcomes, completed steps, and failed attempts without rereading the same project docs first.

Useful queries:

```text
/mem-search category:driver signal:signing
/mem-search category:telemetry tag:provider
/mem-search severity:critical risk:>=80
/mem-search artifact:guard.sys
```

Good use cases:
1. When you want earlier reasoning from previous sessions.
2. When you want long-lived context for repeated artifacts or failures.

### 4.9 `/hooks` And `/override-*`

Inspect:

```text
/hooks
/override
```

Create an exception:

```text
/override-add deny-driver-pr-with-critical-signing-or-symbol-evidence 4 urgent hotfix after manual verification
```

Clear:

```text
/override-clear all
```

Good use cases:
1. When you want to understand why policy is blocking.
2. When you need a temporary exception with an audit trail.

### 4.10 `/fuzz-func`

Basic usage:

```text
/fuzz-func ValidateRequest
/fuzz-func ValidateRequest --file src/guard.cpp
/fuzz-func ValidateRequest @src/guard.cpp
/fuzz-func ValidateRequest --source-scan focused
/fuzz-func ValidateRequest --source-scan full
/fuzz-func ValidateRequest --no-source-scan
/fuzz-func --from-candidate sc-0123456789abcdef
/fuzz-func @Driver/HEVD/Windows/DoubleFetch.c
/source-scan run --limit 50
/source-scan show latest
/fuzz-func show latest
/fuzz-func language system
/fuzz-campaign
/fuzz-campaign run
/create-driver-poc AcmePoc
```

What the planner currently considers:
1. Function signatures and parameter types
2. Real size, null, dispatch, and cleanup guards in the function body
3. Probe, copy, alloc, publish, and cleanup sinks on the same path
4. The caller/callee chain from the representative root
5. The file-expansion path from the selected starting file into the cited source file
6. Saved source candidates, focused source-scan results, and the matcher slug that linked the function fuzz plan
7. Build context, `compile_commands.json`, and snapshot or semantic-index availability

Good use cases:
1. When you want to see branch flips and sink reachability on input-facing driver or anti-cheat code quickly.
2. When you know the suspicious file but not the best root function yet.
3. When you want concrete "which value breaks which predicate and opens which copy/probe path" guidance before normal review or editing.

Recommended reading order:
1. `Conclusion` for the top predicted problem and the most useful branch delta.
2. `Risk score table` to separate high-signal findings from noisy fallbacks.
3. `Top predicted problems` for concrete sample values, predicates, counterexamples, and pass/fail branch consequences.
4. `Source-derived attack surface` for the real probe, copy, dispatch, and cleanup evidence.

Operational notes:
1. A bare function name triggers automatic symbol resolution, while `--file` or `@path` reduces ambiguity.
2. `/fuzz-func @path` is valid even if you do not know the function name yet.
3. `/fuzz-func` defaults to focused source-scan context; use `--no-source-scan` or `--source-scan off` only when you want a pure function fuzz plan without candidate linkage.
4. `/source-scan run` is the better first step when you want to review several source matcher candidates before choosing one with `/fuzz-func --from-candidate <candidate-id>`.
5. Source-only fuzzing results can still be useful even when native auto-run is blocked.
6. Use `/fuzz-campaign` instead of memorizing campaign substeps; Kernforge will suggest the next safe action and `/fuzz-campaign run` will apply it, including deduplicated finding lifecycle updates, libFuzzer/llvm-cov/LCOV/JSON coverage report ingestion, sanitizer/verifier/crash-dump artifact capture, coverage gap feedback, and native result evidence capture when run artifacts exist.
7. `compile_commands.json` improves native follow-up quality, but it is not a prerequisite for source-only planning.

## 5. When To Use Each Dashboard

### 5.1 `/verify-dashboard`

Best when:
1. You want recent verification trends.
2. You want to see which checks fail most often.

### 5.2 `/evidence-dashboard`

Best when:
1. You want the current workspace risk picture.
2. You want recent failed or high-risk signals plus overrides in one view.

### 5.3 `/mem-dashboard`

Best when:
1. You want long-term context, trust tiers, and verification artifact patterns.
2. You want to skim what the system has learned across sessions.

### 5.4 `/investigate dashboard`

Best when:
1. You want to see how many investigation sessions exist.
2. You want preset, finding category, and finding severity distribution.

### 5.5 `/simulate dashboard`

Best when:
1. You want to see which risk profiles you have been using.
2. You want severity, signal, finding, and recommended-action breakdowns.

## 6. Suggested Baselines By Team

### 6.1 Driver Team

Recommended:
1. Enable `windows-security`.
2. Run `driver-visibility` investigation before risky changes.
3. Run `tamper-surface` simulation before review or edit.
4. Run `/verify`.
5. Inspect `/evidence-dashboard category:driver`.
6. Promote only repeated high-risk failures to `deny`.

### 6.2 Telemetry Team

Recommended:
1. Use `provider-visibility` investigation before manifest and provider changes.
2. Run `stealth-surface` after provider changes.
3. Run `forensic-blind-spot` when incident traceability matters.
4. Run `/verify`.
5. Inspect `/evidence-search category:telemetry outcome:failed`.
6. Use `/mem-search category:telemetry tag:provider` for long-lived context.

### 6.3 Anti-Cheat Or Memory-Scan Team

Recommended:
1. Use `stealth-surface` before scanner changes.
2. Use selection-first review and edit aggressively.
3. Run `/verify`.
4. Let repeated high-risk failures drive checkpoint and deny policy.

## 7. Cases Where You Should Not Over-Enforce Yet

Avoid overly strong policy in these cases:

1. Very early prototyping
2. New projects with almost no evidence history
3. General utility work not tied to the security workflow

Recommended progression:
1. Start with `warn`
2. Move recurring issues to `ask`
3. Reserve `deny` for genuine operational incident classes

## 8. Quick Scenario Recipes

### Scenario A: Driver Integrity Hardening

```text
/investigate start driver-visibility guard.sys
/investigate snapshot
/simulate tamper-surface guard.sys
/open driver/guard.cpp
/review selection integrity risk paths
/edit-selection harden the selected integrity checks
/verify
/evidence-dashboard category:driver
```

### Scenario B: Telemetry Provider Visibility Drift

```text
/investigate start provider-visibility MyProvider
/investigate snapshot MyProvider
/simulate stealth-surface MyProvider
/open telemetry/provider.man
/review selection schema and visibility drift
/verify
/evidence-search category:telemetry outcome:failed
```

### Scenario C: Plan Review Before A Large Change

```text
/simulate tamper-surface guard.sys
/simulate forensic-blind-spot guard.sys
/review plan harden driver registration and preserve telemetry audit artifacts
/verify
/simulate-dashboard
```

### Scenario D: Source-level fuzzing for input-facing path triage

```text
/fuzz-func @Driver/HEVD/Windows/DoubleFetch.c
/fuzz-func TriggerDoubleFetch --file Driver/HEVD/Windows/DoubleFetch.c
/fuzz-func show latest
/fuzz-campaign
/fuzz-campaign run
/verify
```

Interpretation:
1. The first run starts coarse at file scope so Kernforge can pick the representative root and the highest-risk reachable path.
2. The second run pins the target function so predicates, counterexamples, and branch deltas become more precise.
3. `show latest` lets you re-read the report and source excerpts before moving into verification or code changes.

### Scenario E: Tracked Feature With Explicit Execution

```text
/simulate tamper-surface guard.sys
/new-feature harden driver registration and preserve telemetry audit artifacts
/new-feature status
/new-feature implement
/verify
/new-feature close
```

## 9. Summary

The best current one-line description of Kernforge is this:

"Observe first, apply a risk lens, work in focused code regions, verify with recent context, and feed the result back into evidence, memory, and policy."

That means the strongest current loop is:

1. `/investigate`
2. `/simulate`
3. `/fuzz-func`
4. `/review selection` or `/edit-selection`
5. `/review plan`
6. `/new-feature`
7. `/verify`
8. `/evidence-dashboard`
9. `/mem-search`
10. Push or PR under hook policy

That loop is the clearest current Kernforge differentiator.
