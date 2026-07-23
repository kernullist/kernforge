# Kernforge Driver Playbook

This playbook explains how to use Kernforge effectively for driver, signing, symbols, packaging, and verifier-readiness work.

## 1. When To Use This Playbook

1. `.sys`, `.inf`, or `.cat` artifacts are involved.
2. Signing, symbols, packaging, or verifier readiness matters.
3. Integrity, registration, or load-path hardening matters.
4. Recent driver-related failed evidence already exists.

## 2. Recommended Baseline Flow

```text
/analyze-project driver startup, signing, and integrity architecture
/analyze-performance startup
/investigate start driver-visibility guard.sys
/investigate start platform-security
/investigate snapshot
/simulate tamper-surface guard.sys
/source-scan run --files driver
/fuzz-func @driver
/fuzz-campaign run
/open driver/guard.cpp
/review-selection integrity risk paths and verifier interactions
/edit-selection harden registration and signing assumptions
/verify
/evidence-dashboard category:driver
/mem-search category:driver signal:signing
```

When starting from a new driver POC:

```text
/create-driver-poc GuardPoc --type objectfilter
# handoff: /source-scan, /fuzz-func, /fuzz-campaign run, /verify, platform-security, signing, Driver Verifier
# seed: GuardPoc/.kernforge/security/workflow_seed.json
```

## 3. What Each Stage Does

1. `/analyze-project ...`
Builds a reusable architecture map for startup, signing, integrity, and verification-sensitive paths.

2. `/investigate start driver-visibility guard.sys`
Captures a lightweight snapshot of current driver visibility, verifier state, and related artifacts.

3. `/investigate start platform-security`
Records best-effort Secure Boot, VBS, HVCI/Memory Integrity, test-signing, driver signature enforcement, and TPM readiness. Failed probes become `unavailable` fields.

4. `/simulate tamper-surface guard.sys`
Surfaces likely integrity, signing, and tamper-risk pressure points.

5. `/source-scan` / `/fuzz-func` / `/fuzz-campaign run`
Triages input-facing IOCTL/callback surfaces, promotes seeds (including multi-call sequences when present), and records native results. Harness-only crashes stay `spurious`.

6. `/review-selection ...`
If simulation findings match the selected range, simulation risk context is injected automatically.

7. `/verify`
Adds driver-focused verification steps and recent investigation or simulation follow-up review steps.

8. `/evidence-dashboard category:driver`
Shows recent failed signing, symbol, package, or verifier-related evidence.

## 4. Signals Worth Watching Closely

1. `signal:signing`
2. `signal:symbols`
3. `severity:critical`
4. `risk:>=80`

Useful examples:

```text
/evidence-search category:driver signal:signing
/mem-search category:driver signal:symbols
/evidence-search severity:critical risk:>=80
```

## 5. Recommended Pre-PR Check

1. `/verify`
2. `/investigate start platform-security` (refresh posture)
3. `/evidence-dashboard category:driver`
4. `/override`
5. Let hook policy evaluate the final push or PR path

## 6. Good Team Habits

1. Capture a live snapshot and platform-security posture before risky driver changes.
2. Run `tamper-surface` before large hardening work.
3. Check both evidence and memory for signing and symbol issues.
4. Treat repeated failures as a workflow problem first, not just an override problem.
5. After `/create-driver-poc`, follow the printed handoff (`/source-scan` -> `/fuzz-func` -> `/fuzz-campaign run`).
