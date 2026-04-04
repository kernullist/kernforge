# Kernforge Telemetry Playbook

This playbook explains how to use Kernforge for ETW, provider, manifest, XML, runtime visibility, and forensic traceability work.

## 1. When To Use This Playbook

1. Provider manifests or registration code are changing.
2. `.man`, `.mc`, or `.xml` files are changing.
3. Event visibility, observer coverage, or schema drift matters.
4. Post-incident artifact retention matters.

## 2. Recommended Baseline Flow

```text
/analyze-project telemetry provider visibility and manifest architecture
/analyze-performance ETW
/investigate start provider-visibility MyProvider
/investigate snapshot MyProvider
/simulate stealth-surface MyProvider
/open telemetry/provider.man
/review-selection provider visibility and schema drift
/open telemetry/register_provider.cpp
/edit-selection align provider registration and fallback visibility
/verify
/evidence-search category:telemetry outcome:failed
/mem-search category:telemetry signal:provider
```

## 3. Extra Flow When Forensics Matter

```text
/simulate forensic-blind-spot MyProvider
/verify
/simulate-dashboard
```

Why this helps:
1. Project analysis builds a reusable map of provider registration, manifest, and visibility paths.
2. `provider-visibility` is a visibility triage snapshot, not a deep ETW or provider root-cause analyzer.
3. `stealth-surface` checks observer coverage.
4. `forensic-blind-spot` checks artifact retention and reconstruction assumptions.

## 4. Signals Worth Watching Closely

1. `signal:provider`
2. `signal:xml`
3. `signal:stealth`
4. `signal:forensics`

Useful examples:

```text
/evidence-search category:telemetry signal:provider
/evidence-search kind:simulation_finding signal:stealth
/mem-search category:telemetry signal:forensics
```

## 5. Recommended Pre-PR Check

1. `/verify`
2. `/evidence-search category:telemetry outcome:failed`
3. `/mem-search category:telemetry tag:provider`
4. `/override`

## 6. Good Team Habits

1. Capture live provider state before and after manifest changes.
2. Narrow provider registration review through selection-based review.
3. Always check not only whether telemetry is visible now, but also whether incident reconstruction will still be possible later.
