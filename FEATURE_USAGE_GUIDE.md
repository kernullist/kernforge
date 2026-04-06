# Kernforge Detailed Usage Guide

This document explains how to use the currently implemented Kernforge features in real engineering workflows, with concrete examples and recommended command sequences.

Reference point:
- Codebase snapshot: 2026-04-03

Intended readers:
- Windows security engineers
- Anti-cheat engineers
- Kernel and user-mode telemetry engineers
- Driver, signing, symbol, and package readiness engineers
- Unreal Engine security and integrity engineers

Goals of this guide:
1. Explain real usage patterns instead of just listing features.
2. Show which command combinations fit which kinds of problems.
3. Teach the full loop of `analyze-project -> analyze-performance -> investigate -> simulate -> review/edit/plan -> verify -> evidence/memory/hooks`.

## 1. The Best Way To Think About Kernforge

Kernforge can be used like a normal coding CLI, but its strongest current value now comes from building a reusable project knowledge pack before running sensitive engineering changes through the rest of the loop.

The best current loop looks like this:

1. If the workspace is large or unfamiliar, run `/analyze-project` first.
2. Use `/analyze-performance` to turn the latest knowledge pack into a bottleneck lens when performance or startup paths matter.
3. If live state matters, use `/investigate` to capture the current system state.
4. If an extra risk lens matters, use `/simulate` to evaluate tamper, visibility, or forensic blind spots.
5. Use `/review-selection`, `/edit-selection`, or `/do-plan-review` to drive the work.
6. Run `/verify` to execute the verification plan.
7. Use `/evidence-*` and `/mem-*` to inspect both recent signals and longer-lived context.
8. Let hooks act as the final policy layer before push or PR.

Practical interpretation:
1. `analyze-project` builds a reusable architecture map instead of a disposable summary.
2. `analyze-performance` extracts likely hot paths and bottlenecks from the latest architecture knowledge.
3. `investigate` captures what is happening live.
4. `simulate` highlights risk-oriented weak spots using lightweight heuristics.
5. `verify` turns code changes and recent context into a concrete validation plan.
6. `evidence` stores structured recent signals.
7. `memory` keeps conclusions across sessions.
8. `hooks` turn that accumulated context back into guardrails.

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

### 2.0 Project Analysis

Purpose:
1. Build a reusable architecture document for a large workspace.
2. Split analysis across multiple worker and reviewer passes.
3. Keep a `latest` knowledge pack and performance lens for follow-up work.
4. Reuse unchanged shard results when incremental mode is enabled.
5. Preserve a structural index, Unreal semantic graph, and vector corpus for downstream automation.

Useful commands:
- `/analyze-project <goal>`
- `/analyze-performance [focus]`
- `/set-analysis-models`

Best used when:
1. You are entering a large codebase and need more than an ad hoc summary.
2. The work spans startup, integrity, ETW, scanner, compression, memory, or upload paths.
3. You want follow-up review and verification to inherit a stable architecture view.
4. You are dealing with a UE5-scale codebase where modules, targets, reflection, replication, and asset/config coupling all matter at once.

Additional artifacts now produced by project analysis:
1. `snapshot`: structured scan output plus runtime and project edges.
2. `structural index`: symbol, reference, and build-edge oriented analysis state.
3. `unreal graph`: UE project, module, network, asset, system, and config semantics.
4. `knowledge pack`: human-readable architecture digest and subsystem summaries.
5. `vector corpus`: embedding-ready project, subsystem, and shard documents.
6. `vector ingest exports`: staging files for pgvector, SQLite, and Qdrant pipelines.

What materially changed for large and Unreal-heavy workspaces:
1. A semantic shard planner now prioritizes `startup`, `build_graph`, `unreal_network`, `unreal_ui`, `unreal_ability`, `asset_config`, `integrity_security`, and `unreal_gameplay`.
2. Worker and reviewer prompts now carry shard-specific semantic focus and review checklists.
3. Incremental reuse now considers semantic fingerprints instead of relying only on file hashes.
4. Output documents now expose subsystem invalidation reasons, evidence, diffs, and top change classes.
5. Persisted artifacts now include machine-readable snapshot, structural index, Unreal semantic graph, vector corpus, and ingestion seed files for downstream retrieval pipelines.

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

Best used when:
1. Generic `go test`, `msbuild`, or `ctest` is not enough.
2. You need signing, symbols, package, provider, XML, or verifier-oriented follow-up.
3. You already saw risky investigation or simulation findings and want them reflected in validation.

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
2. Automatically inject recent simulation findings when they match the selected area.
3. Inspect workspace and selection diffs in a richer Windows diff surface before wider review or editing.

Useful commands:
- `/open <path>`
- `/selection`
- `/selections`
- `/diff`
- `/diff-selection`
- `/review-selection [extra]`
- `/review-selections [extra]`
- `/edit-selection <task>`
- `/note-selection <text>`
- `/tag-selection <tag[,tag2]>`

Diff workflow notes:
1. On Windows, `/diff` and `/diff-selection` prefer the internal WebView2 diff viewer.
2. The read-only diff viewer includes changed-file navigation, unified/split toggles, and intraline highlights.
3. If the internal surface is unavailable, Kernforge falls back to terminal output.

Best used when:
1. You want to focus on a single IOCTL handler, integrity check, or provider registration block.
2. You want to connect a recent simulation finding directly to the relevant code.

### 2.8 Plan Review Workflow

Purpose:
1. Have one model produce a plan.
2. Have another model review that plan.
3. Execute the approved plan through the normal agent flow.

Useful commands:
- `/set-plan-review`
- `/do-plan-review <task>`

Best used when:
1. A change spans multiple components.
2. Order of operations, rollback points, or operational caution matter.
3. You want simulation findings to shape the implementation plan before edits begin.

Current integration:
1. Recent simulation findings that match the task are injected into the planning prompt.
2. The same perspective is injected again into the final execution prompt.

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
7. `/review-selection integrity risk paths and verifier interactions`
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
6. `/review-selection provider visibility and schema drift`
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
3. `/review-selection false positives, stealth coverage, and performance ceilings`
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
3. `/do-plan-review harden driver registration, improve telemetry visibility, and preserve post-incident artifacts`
4. Let the reviewer critique the plan.
5. Execute the approved plan.
6. `/verify`
7. `/evidence-dashboard`

Current strength:
1. Simulation findings can shape the planning prompt.
2. They can also shape the final plan execution prompt.

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

### 4.3 `/review-selection` And `/edit-selection`

Basic usage:

```text
/open driver/guard.cpp
/review-selection check risk surfaces and cleanup paths
/edit-selection harden the selected registration path
```

Good use cases:
1. When only one function or block matters.
2. When you want recent simulation findings tied directly to the selected area.

Current automatic behavior:
1. If recent simulation findings match the selected path, Kernforge injects `Additional simulation risk focus` into review and edit prompts.

### 4.4 `/do-plan-review`

Basic usage:

```text
/do-plan-review harden driver load validation, improve telemetry provider visibility, and preserve audit artifacts
```

Good use cases:
1. Large or high-risk changes.
2. Work where rollback points and sequencing matter.
3. Cases where risk-oriented thinking should shape the implementation plan before edits begin.

Current automatic behavior:
1. Matching recent simulation findings are injected into the planning prompt.
2. They are also injected into the execution prompt after approval.

### 4.5 `/verify`

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

### 4.6 `/evidence-search` And `/evidence-dashboard`

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

### 4.7 `/mem-search`

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

### 4.8 `/hooks` And `/override-*`

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
/review-selection integrity risk paths
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
/review-selection schema and visibility drift
/verify
/evidence-search category:telemetry outcome:failed
```

### Scenario C: Plan Review Before A Large Change

```text
/simulate tamper-surface guard.sys
/simulate forensic-blind-spot guard.sys
/do-plan-review harden driver registration and preserve telemetry audit artifacts
/verify
/simulate-dashboard
```

## 9. Summary

The best current one-line description of Kernforge is this:

"Observe first, apply a risk lens, work in focused code regions, verify with recent context, and feed the result back into evidence, memory, and policy."

That means the strongest current loop is:

1. `/investigate`
2. `/simulate`
3. `/review-selection` or `/edit-selection`
4. `/do-plan-review`
5. `/verify`
6. `/evidence-dashboard`
7. `/mem-search`
8. Push or PR under hook policy

That loop is the clearest current Kernforge differentiator.
