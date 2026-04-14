# Kernforge Quickstart

This short guide is for getting productive with Kernforge as quickly as possible.

The key loop to remember:
1. Use `/analyze-project` first when the workspace is large or unfamiliar.
2. Use `/investigate` when live state matters.
3. Use `/simulate` when an extra risk lens matters.
4. Use `/open` plus `/review-selection` or `/edit-selection` to stay focused.
5. Use `/verify`, then inspect the result with `/evidence-dashboard` and `/mem-search`.

## 1. The Core Loop In Five Minutes

Recommended sequence:

```text
/analyze-project driver startup, integrity, and signing architecture
/analyze-performance startup
/investigate start driver-visibility guard.sys
/investigate snapshot
/simulate tamper-surface guard.sys
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
5. Review and edit only the selected code.
6. Verify with the current context.
7. Inspect the resulting risk picture.

## 2. Most Common Commands

Project analysis:
- `/analyze-project [--mode map|trace|impact|security|performance] <goal>`
- `/analyze-performance [focus]`
- `/set-analysis-models`
- If you omit `--mode`, the default mode is `map`

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

Policy:
- `/hooks`
- `/override`

Planning and tracked feature work:
- `/do-plan-review <task>`
- `/new-feature <task>`
- `/new-feature status [id]`
- `/new-feature implement [id]`

## 3. Best First Scenarios

### Driver change

```text
/analyze-project driver startup and integrity architecture
/investigate start driver-visibility guard.sys
/simulate tamper-surface guard.sys
/open driver/guard.cpp
/review-selection signing and integrity assumptions
/verify
/evidence-dashboard category:driver
```

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
2. `/analyze-performance startup` or another relevant focus
3. `/evidence-dashboard`
4. `/mem-search category:driver` or `/mem-search category:telemetry`
5. `/hooks`

Quick interpretation:
1. `/status` is the fast view for current session and runtime state, including approvals.
2. `/config` is the fast view for effective settings such as provider defaults, hooks, locale, and verification toggles.

If automatic verification fails because Windows build tools are missing:
1. Run `/detect-verification-tools` first.
2. If detection does not find the tool, set it explicitly, for example `/set-msbuild-path "C:\Program Files\Microsoft Visual Studio\2022\Community\MSBuild\Current\Bin\MSBuild.exe"`.
3. If you want editing without post-edit verification for a while, use `/set-auto-verify off`.

If a model tries to stage, commit, push, or open a PR:
1. Kernforge treats git mutation as a separate approval path from file edits.
2. `Allow git?` covers git-mutating tools for the current session.
3. Normal review and edit prompts are not supposed to run git mutation unless you explicitly asked for it.

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
