# Kernforge Quickstart

This short guide is for getting productive with Kernforge as quickly as possible.

The key loop to remember:
1. Use `/investigate` when live state matters.
2. Use `/simulate` when an extra risk lens matters.
3. Use `/open` plus `/review-selection` or `/edit-selection` to stay focused.
4. Use `/verify` to close the loop.
5. Inspect the result with `/evidence-dashboard` and `/mem-search`.

## 1. The Core Loop In Five Minutes

Recommended sequence:

```text
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
1. Capture the current live state first.
2. `driver-visibility` is a lightweight visibility triage snapshot, not a deep driver load diagnostic.
3. Check the target through an extra risk lens.
4. Review and edit only the selected code.
5. Verify with the current context.
6. Inspect the resulting risk picture.

## 2. Most Common Commands

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
/investigate start driver-visibility guard.sys
/simulate tamper-surface guard.sys
/open driver/guard.cpp
/review-selection signing and integrity assumptions
/verify
/evidence-dashboard category:driver
```

### Telemetry change

```text
/investigate start provider-visibility MyProvider
/simulate stealth-surface MyProvider
/open telemetry/provider.man
/review-selection provider visibility and schema drift
/verify
/evidence-search category:telemetry outcome:failed
```

## 4. What To Check First When Something Feels Wrong

1. `/status`
2. `/evidence-dashboard`
3. `/mem-search category:driver` or `/mem-search category:telemetry`
4. `/hooks`

## 5. Next Documents

Full workflow guide:
- [Detailed Usage Guide](./FEATURE_USAGE_GUIDE.md)

Domain playbooks:
- [Driver Playbook](./PLAYBOOK_driver.md)
- [Telemetry Playbook](./PLAYBOOK_telemetry.md)
