# Kernforge Memory-Scan Playbook

This playbook explains how to use Kernforge for pattern scan, signature scan, memory inspection, evasion resistance, and false-positive or false-negative tuning work.

## 1. When To Use This Playbook

1. Scanner or signature matching logic is changing.
2. False positives, false negatives, performance ceilings, or evasion resistance matter.
3. Observer coverage matters from a stealth perspective.
4. Recent scanner-related failed evidence already exists.

## 2. Recommended Baseline Flow

```text
/simulate stealth-surface scanner-core
/open scanner/patternscan.cpp
/review-selection false positives, stealth coverage, and performance ceilings
/edit-selection reduce false positives without weakening evasion coverage
/verify
/evidence-dashboard category:memory-scan
/mem-search category:memory-scan risk:>=70
```

## 3. Why This Flow Works Well

1. Scanner work is usually about detection coverage and evasions, not just correctness.
2. `stealth-surface` surfaces observer gaps first.
3. Selection review and edit keep the work focused on the real scanning path.
4. `/verify` adds memory-scan review steps plus recent adversarial context.
5. `/evidence-dashboard` gives a fast current view of high-risk scanner state.

## 4. When Forensics Also Matter

If scanner output also matters for incident reconstruction:

```text
/simulate forensic-blind-spot scanner-core
/verify
/simulate-dashboard
```

Why this helps:
1. Detection alone is not enough if post-incident artifacts are too weak.
2. Forensic blind spot simulation highlights that operational weakness separately.

## 5. Signals Worth Watching Closely

1. `signal:stealth`
2. `signal:forensics`
3. `severity:high`
4. `risk:>=70`

Useful examples:

```text
/evidence-search category:memory-scan signal:stealth
/evidence-search kind:simulation_finding signal:forensics
/mem-search category:memory-scan severity:high
```

## 6. Recommended Pre-PR Check

1. `/verify`
2. `/evidence-dashboard category:memory-scan`
3. `/mem-search category:memory-scan risk:>=70`
4. `/override`

## 7. Good Team Habits

1. Evaluate stealth pressure before and after scanner changes.
2. When reducing false positives, explicitly check that evasion coverage does not weaken.
3. Treat performance ceilings and detection gaps as part of the same operational problem.
4. Treat repeated failures as a detection-gap problem first, not an override problem.
