# Kernforge Quickstart

This short guide is for getting productive with Kernforge as quickly as possible.

The key loop to remember:
1. Use `/analyze-project` first when the workspace is large or unfamiliar.
2. Use `/investigate` when live state matters.
3. Use `/simulate` when an extra risk lens matters.
4. Use `/find-root-cause` when you already have a concrete symptom to explain.
5. Use `/fuzz-func` when you want attacker-style source-only parameter reasoning before building a harness.
6. Use `/open` plus `/review-selection` or `/edit-selection` to stay focused.
7. Use `/verify`, then inspect the result with `/evidence-dashboard` and `/mem-search`.

Before launching, `kernforge --help` shows the executable version plus standalone, one-shot, MCP server, and daemon proxy examples. Use `kernforge --version` for the same version-only check, and `kernforge help mcp` when wiring Kernforge into an MCP client.
When Codex uses Kernforge as an MCP server, ask for code review through `kernforge_review`; it returns structured findings, `latest_review_freshness`, `edit_proposals`, `runtime_gate_ledger`, and action-oriented `next_commands` instead of the older review-code-only surface.
If the same MCP entry is reused across repositories, pass the current repo as the tool `workspace` argument or configure `-cwd` per repo; otherwise Kernforge falls back to the server launch directory.

## 1. The Core Loop In Five Minutes

Recommended sequence:

```text
/analyze-project driver startup, integrity, and signing architecture
/analyze-performance startup
/investigate start driver-visibility guard.sys
/investigate snapshot
/simulate tamper-surface guard.sys
/find-root-cause guard.sys unload leaves the user process stuck in device close. Expected: close returns. Observed: the pending request never completes.
/fuzz-func @driver/guard.cpp
/open driver/guard.cpp
/review-selection integrity bypass paths
/edit-selection harden the selected integrity checks
/verify
/evidence-dashboard category:driver
```

What this does:
1. Build a reusable architecture map and performance lens first.
2. Capture the current live state.
3. `driver-visibility` is a lightweight visibility triage snapshot, not a deep driver load diagnostic.
4. Check the target through an extra risk lens.
5. `/find-root-cause` starts a worker/reviewer investigation around the symptom, trigger, expected invariant, and observed failure.
6. `/fuzz-func` derives attacker-controlled input states, branch predicates, counterexamples, and sink reachability directly from source.
7. Review and edit only the selected code.
8. Verify with the current context.
9. Inspect the resulting risk picture.

## 2. Most Common Commands

Project analysis:
- `/analyze-project [--mode map|trace|impact|surface|security|performance] <goal>`
- `/analyze-performance [focus]`
- `/set-analysis-models`
- If you omit `--mode`, the default mode is `map`
- Long `/analyze-project` runs show shard waves, completed/failed shard counts, worker/reviewer model wait events, and final artifact write steps. `progress_display` now defaults to `compact`, so routine progress stays quiet; switch to `/progress-display stream` when you want every update persisted.
- Use `/set-analysis-models clear` when project analysis should follow the current main model instead of a previously configured worker/reviewer route

Investigation:
- `/investigate`
- `/investigate start <preset> [target]`
- `/investigate snapshot`
- `/investigate dashboard`

Adversarial view:
- `/simulate tamper-surface [target]`
- `/simulate stealth-surface [target]`
- `/simulate forensic-blind-spot [target]`
- `/simulate dashboard`

Root-cause investigation:
- `/find-root-cause <problem description>`
- `/find-root-cause --pattern-pack <path-or-dir> <problem description>`
- `/root-cause-patterns list [--type <project_type>]`
- `/root-cause-patterns match <problem symptom>`
- `/root-cause-patterns github-search [--type <project_type>] [--limit 20] [query words...]`
- `/root-cause-patterns normalize --in <github_issues.json> --out <pattern_pack.json>`
- `/root-cause-patterns validate [--in <pattern_pack.json>]`

Source-level fuzzing:
- `/fuzz-func <function-name>`
- `/fuzz-func <function-name> --file <path>`
- `/fuzz-func <function-name> --source-scan focused`
- `/fuzz-func --from-candidate <candidate-id>`
- `/fuzz-func @<path>`
- `/fuzz-func status`
- `/fuzz-func show [id|latest]`
- `/fuzz-func language [system|english]`
- `/source-scan run`
- `/source-scan list`
- `/source-scan show [id|latest]`

Selection workflow:
- `/open <path>`
- `/review-selection [extra]`
- `/edit-selection <task>`

Verification:
- `/verify`
- `/verify-dashboard`
- `/set-auto-verify [on|off]`
- `/detect-verification-tools`
- `/set-msbuild-path <path>`

Evidence and memory:
- `/evidence-dashboard`
- `/evidence-search <query>`
- `/mem-search <query>`
- Persistent memory is also injected automatically as `Workspace continuity` for recent high-value records from the same workspace; Kernforge shows a `memory` activity line when it reuses those records.

Policy:
- `/hooks`
- `/override`

Planning and tracked feature work:
- `/do-plan-review <task>`
- `/new-feature <task>`
- `/new-feature status [id]`
- `/new-feature implement [id]`

Provider and runtime inspection:
- `/provider status`
- `/status`
- `/config`

## 3. Best First Scenarios

### Driver change

```text
/analyze-project driver startup and integrity architecture
/investigate start driver-visibility guard.sys
/simulate tamper-surface guard.sys
/fuzz-func @Driver/guard.cpp
/open driver/guard.cpp
/review-selection signing and integrity assumptions
/verify
/evidence-dashboard category:driver
```

### Input-facing code triage

```text
/fuzz-func ValidateRequest --file src/guard.cpp
/fuzz-func ValidateRequest --source-scan focused
/fuzz-func @Driver/HEVD/Windows/DoubleFetch.c
/source-scan run --limit 50
/fuzz-func --from-candidate <candidate-id>
/fuzz-func show latest
```

What this means:
1. If you know the function, pin it with a file path.
2. If you only know the suspicious file, let Kernforge pick the representative root and reachable input-facing path.
3. `/fuzz-func` uses focused source-scan context by default; `/source-scan run` is useful when you want to inspect and choose the candidate before fuzz planning. Source candidates include function-window evidence, confidence breakdown, file/symbol fingerprints, and stale-source state.
3. Read the highest-score finding, the branch-delta summary, and the first source location before anything else.

### Symptom-driven root-cause investigation

```text
/find-root-cause My Win32 service process does not stop through sc stop
/find-root-cause In the party system, after inviting and kicking members repeatedly, expected the party size limit to block new invites, but observed extra members can still be invited.
/root-cause-patterns match sc stop leaves the service process running
```

What this means:
1. A good symptom prompt names the component, trigger/repro path, expected invariant, and observed failure.
2. If the prompt is unclear, Kernforge prints the missing pieces and suggests a sharper `/find-root-cause ...` command.
3. Pattern packs are priors. Final root-cause claims still require current source evidence and reviewer causality validation.

### Telemetry change

```text
/analyze-project telemetry provider visibility and manifest architecture
/investigate start provider-visibility MyProvider
/simulate stealth-surface MyProvider
/open telemetry/provider.man
/review-selection provider visibility and schema drift
/verify
/evidence-search category:telemetry outcome:failed
```

### Multi-session feature work

```text
/new-feature harden driver registration, preserve telemetry audit artifacts, and document rollback points
/new-feature status
/new-feature implement
/verify
/new-feature close
```

Use `/new-feature` when you want persisted spec, plan, and task artifacts under `.kernforge/features/<id>`. Use `/do-plan-review` when you want a reviewed plan and immediate execution without a longer-lived tracked feature workspace.

## 4. What To Check First When Something Feels Wrong

1. `/status`
2. `/provider status`
3. `/analyze-performance startup` or another relevant focus
4. `/evidence-dashboard`
5. `/mem-search category:driver` or `/mem-search category:telemetry`
6. `/hooks`

Quick interpretation:
1. `/status` is the fast view for current session and runtime state, including approvals and the runtime gate ledger. Check `runtime_gate`, `review_freshness`, blocker/warning counts, and `next_command` before final answers or git/MCP write-side actions.
2. `/config` is the fast view for effective settings such as provider defaults, hooks, locale, and verification toggles.
3. `/provider status` is the fast view for provider wiring. It shows the normalized endpoint, whether an API key is configured, and what budget visibility is actually available for the current provider.

If automatic verification fails because Windows build tools are missing:
1. Run `/detect-verification-tools` first.
2. If detection does not find the tool, set it explicitly, for example `/set-msbuild-path "C:\Program Files\Microsoft Visual Studio\2022\Community\MSBuild\Current\Bin\MSBuild.exe"`.
3. If you want editing without post-edit verification for a while, use `/set-auto-verify off`.

If a model tries to stage, commit, push, or open a PR:
1. Kernforge treats git mutation as a separate approval path from file edits.
2. `Allow git?` covers git-mutating tools for the current session.
3. Normal review and edit prompts are not supposed to run git mutation unless you explicitly asked for it.

If an edit is simple and exact:
1. Prefer the structured `apply_edit_proposal` path. It records the file, operation, exact search, replacement/content, rationale, risk, preview fingerprint, and review evidence before writing.
2. Keep `apply_patch` for complex hunk-level fallback after the current file contents have been read.
3. If `apply_patch` fails with a malformed wrapper or repeated invalid signature, reread the target file and generate a fresh patch instead of resubmitting the same text.
4. If a review or runtime gate reports stale coverage, rerun `/review` or follow the displayed `next_command` before claiming completion.
5. If `/review` returns `narrow-review`, supply the focused paths, diff, selection, or target symbol before treating model findings as complete.

If the model keeps rereading a large file:
1. Recent `read_file` cache hits now return a `NOTE:` prefix instead of silently rereading the same range.
2. `grep` results can include `[cached-nearby:inside]` or `[cached-nearby:N]` to show that relevant context is already in a recent read span.
3. The best next prompt is usually to ask for the missing adjacent range or the exact local change, not to ask for the whole block again.

## 5. Input Cancellation Tips

1. `Esc` while typing cancels only the current prompt input.
2. `Esc` while a model request is running cancels the in-flight request.
3. On Windows consoles, brief `Esc` taps are handled as valid request cancel input.
4. After request cancel, the next prompt is stabilized so leftover `Esc` input does not auto-cancel it.

## 6. Next Documents

Full workflow guide:
- [Detailed Usage Guide](./FEATURE_USAGE_GUIDE.md)

Domain playbooks:
- [Driver Playbook](./PLAYBOOK_driver.md)
- [Telemetry Playbook](./PLAYBOOK_telemetry.md)
