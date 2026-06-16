# Permission model redesign: plan / edit / full

Status: planned (mapping done 2026-06-16). Execute in the ordered slices below;
each slice must leave `go build ./cmd/kernforge/` and `go test ./cmd/kernforge/`
green and is independently committable.

## Decisions (confirmed)

Collapse the permission model to three modes; the permission MODE is the single
authority for "may I edit".

- **plan** (NEW DEFAULT): hard read-only. No workspace edits, shell, or git.
- **edit**: in-workspace edits auto; out-of-workspace writes and dangerous ops
  (shell, git, network/web) require an approval prompt.
- **full**: everything allowed, no prompts.

Retire the per-request read-only-analysis HARD block. The request-intent
classifier (`classifyAgentRequestModeHeuristics` / `ReadOnlyAnalysis`) may remain
only as a SOFT hint (whether to proactively propose edits); it must never produce
a hard "I cannot edit" that contradicts an edit/full mode. If an edit is refused,
the only reason is mode=plan, and the message must say so (never "read_only
permission").

## Why this is tractable

The enforcement gate already implements plan and full exactly, and edit ~= the
current `default`/`:workspace` behavior:

`config.go` `PermissionManager.allowWithoutPrompt` (~4527):
- `ModeBypass` -> all allowed                 => **full**
- `ModePlan` -> read only, else hard deny      => **plan** (message already good:
  "permission denied: %s is disabled in plan mode")
- `ModeDefault` -> read auto; write/shell/git prompt; profile `:workspace` => **edit**
- `ModeAcceptEdits` -> read+write auto; shell/git prompt (legacy; maps to edit)

So the work is mostly: user-facing rename + default change + retire the
read-only-analysis hard block + status/CLI/completion + tests. Writes are gated
via `w.Perms.Allow(ActionWrite, path)` (`tools.go:1988`); shell `Allow(ActionShell)`
(`tools.go:2207`); git `Allow(ActionGit)` (`tools.go:2128`).

## Canonical modes and accepted aliases

Canonical (user-facing + persisted going forward): `plan`, `edit`, `full`.

`ParseMode` / `ParseModeStrict` (`config.go:~4390-4424`) must keep accepting every
legacy string and profile id and normalize:
- `plan`, `:read-only`                                  -> **plan**
- `` (empty)                                            -> **plan** (NEW default)
- `default`, `edit`, `acceptEdits`, `:workspace`        -> **edit**
- `full`, `bypassPermissions`, `:danger-full-access`    -> **full**

`config.go:390` `PermissionMode: "default"` -> `"plan"`.

## Slices (ordered)

### Slice 1 - modes + parsing + default  [DONE 2026-06-16]
Implemented as a thin INPUT-ALIAS layer over the existing internal modes (no new
Mode constants, no `allowWithoutPrompt` change), because the existing gate already
matches: plan=ModePlan, edit=ModeAcceptEdits (in-ws write auto via Allow, shell/git
prompt; out-of-ws handled by CheckEditBoundary), full=ModeBypass.
- `config.go` `ParseModeStrict`: `""` -> ModePlan (new default); `"edit"` ->
  ModeAcceptEdits; `"full"` -> ModeBypass; `"plan"` -> ModePlan; legacy
  `default`/`acceptEdits`/`bypassPermissions` and the `:read-only`/`:workspace`/
  `:danger-full-access` profiles still parse unchanged.
- `config.go:390` default `"default"` -> `"plan"`.
- `validPermissionModes()` lists `plan, edit, full` first (legacy still accepted).
- Tests: `permission_mode_canonical_test.go` (parse/aliases/default-plan/3-mode
  enforcement). Full cmd/kernforge suite green; no existing test needed updating
  (tests set modes explicitly or run with nil Perms; the hooks_test "default" comes
  from the `string(ModeDefault)` payload fallback, untouched here).
NOTE: internal Mode values stay ModePlan/ModeAcceptEdits/ModeBypass; the display
mapping mode -> plan/edit/full is Slice 3.

### Slice 2 - retire the read-only-analysis hard block (mode = single authority)
- `agent.go:~2465` and `~9014`: `if readOnlyAnalysis && !toolCallAllowedInReadOnlyAnalysis(call)`
  no longer hard-blocks edit tools. Gate edits on the permission mode (plan) via
  the existing `perms.Allow(...)` path instead; in edit/full a tool call is never
  blocked by request classification.
- Keep `readOnlyAnalysis` only as a soft hint (do not feed a hard "you are
  read-only" instruction to the model in edit/full). Review `prompt_assets.go`
  `ReadOnlyAnalysis` (~55/64) and `final_gate.go:412`, `interactive_orchestration.go:409`.
- Messaging: a refusal-to-edit must reference mode=plan, not "read_only".
- Verify: the model in edit/full proposes/applies edits for a neutral request
  ("이제 수정될거야") instead of self-reporting read-only.

### Slice 3 - status / CLI / completion / help
- Status line `[perm:...]`, `/status detail` permission lines, `cli_help.go`
  (~231-233, ~337), `completion.go` (~163-166, ~728), `config.go` help text
  (~3309, ~3749-3750), `main.go` `confirmLabel` hints (~2908-2922).
- Show `plan|edit|full`; keep accepting legacy strings/profile ids as input.

### Slice 4 - tests + safety regression suite
- Update tests asserting `permission_mode == "default"` (`hooks_test.go` ~336/375/
  420/707) to `"plan"` or to the new default expectation.
- Update `config_test.go` mode<->profile maps and valid-modes.
- The read-only-by-default safety (request_handling_safety_regression_test.go,
  request_semantic_classifier_test.go, request_envelope_test.go,
  request_envelope_boundary_audit_test.go, analysis_qa_context_test.go,
  interactive_orchestration_test.go, tools_edit_guard_test.go): the invariant
  becomes "default MODE is plan (read-only)", not a per-request hard block. Tests
  that assert a request-classified read-only HARD block on a tool call must be
  updated to assert plan-mode denial instead; tests that assert the classifier's
  soft signal can stay.
- Add: plan denies write/shell/git; edit allows in-ws write, prompts out-of-ws/
  shell/git; full allows all without prompts; every legacy alias parses to the
  right canonical mode; empty -> plan.

## Risks / invariants to preserve

- Do not weaken: out-of-workspace writes, shell, and git still require approval in
  edit mode; full is the only no-prompt mode and must be explicitly chosen.
- Persisted sessions/configs with legacy mode strings must still load.
- Never leave the tree between slices with a permission gate that allows more than
  the chosen mode intends (a half-migration is a security hole) - keep each slice
  build+test green.
