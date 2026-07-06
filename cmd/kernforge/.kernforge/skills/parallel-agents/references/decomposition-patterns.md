# Decomposition Patterns — Kernel / Anti-cheat / UE5 Context

Reference file for parallel-agents skill. Read when decomposing tasks
specific to Windows kernel development, anti-cheat, or Unreal Engine 5.

---

## Kernel Driver Review (Tvk.sys)

| Worker | Scope |
|--------|-------|
| A — Safety Agent | IRQL violations, page fault risk, non-paged pool usage, lookaside lists, spinlock correctness |
| B — Security Agent | IOCTL attack surface, ProbeForRead/Write coverage, pointer validation, race conditions in callbacks |
| C — Detection Logic Agent | Correctness of detection algorithms, false positive risk, evasion surface |

Reviewer prompt addition:
> "Pay special attention to any finding that could cause a BSOD in production.
> Rate BSOD-risk findings as CRITICAL regardless of other considerations."

---

## Code Review: TavernWorker.exe Detection Module

| Worker | Scope |
|--------|-------|
| A — Logic Agent | Detection algorithm correctness, false positive/negative analysis |
| B — IPC/Comms Agent | Communication with Tvk.sys and TavernMaster.dll, protocol correctness, race conditions |
| C — Evasion Agent | How would a cheater bypass this detection? What assumptions does the code make that an attacker could violate? |

Reviewer prompt addition:
> "The Evasion Agent's output is especially important. If you think they missed
> a bypass vector, describe it in detail under Gaps Identified."

---

## TPM / Hardware Fingerprinting Code Review

| Worker | Scope |
|--------|-------|
| A — Correctness Agent | TBS API usage, error handling for TPM_RC codes, AMD fTPM quirks |
| B — Security Agent | Can the fingerprint be spoofed? EK hash collision risk? Replay attacks? |
| C — Reliability Agent | Behavior on TPM-disabled machines, VMs, TPM 1.2 fallback, CI/CD environments |

---

## Feature Development: New Detection Method

| Worker | Scope |
|--------|-------|
| A — Implementation Agent | Write the detection code. Do not write tests. Focus on correctness. |
| B — Test Agent | Write comprehensive tests and a threat model. Assume the implementation exists but test it adversarially. |
| C — Integration Agent | How does this fit into the existing Tavern architecture? What callbacks/hooks are needed? What could break? |

---

## IDA Pro / Binary Analysis

| Worker | Scope |
|--------|-------|
| A — Static Analysis Agent | Control flow, function signatures, data structures from decompilation |
| B — String/Import Agent | Interesting strings, imports, exports, obfuscation patterns |
| C — Behavioral Agent | What does this binary do at runtime? What APIs suggest about its purpose? |

Reviewer prompt addition:
> "This is reverse engineering — uncertainty is expected. When challenging a finding,
> explain what alternative interpretation fits the evidence better."

---

## UE5 Codebase Analysis (Dark and Darker)

| Worker | Scope |
|--------|-------|
| A — Call Graph Agent | Function call relationships, subsystem dependencies, hot paths |
| B — Anti-cheat Surface Agent | Exposed hooks, detoured functions, memory regions accessible from usermode |
| C — Performance Agent | Expensive operations in game thread, allocation patterns, lock contention |
