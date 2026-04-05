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
- `/analyze-project <goal>`
- `/analyze-performance [focus]`
- `/set-analysis-models`

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

Evidence and memory:
- `/evidence-dashboard`
- `/evidence-search <query>`
- `/mem-search <query>`

Policy:
- `/hooks`
- `/override`

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

## 4. What To Check First When Something Feels Wrong

1. `/status`
2. `/analyze-performance startup` or another relevant focus
3. `/evidence-dashboard`
4. `/mem-search category:driver` or `/mem-search category:telemetry`
5. `/hooks`

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
