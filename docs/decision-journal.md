# Decision Journal

KernForge can capture the reasoning behind material implementation choices and aggregate that evidence across repositories. The feature is designed for preference learning without silently turning one choice into a permanent rule.

## Capture flow

During an interactive code modification request, KernForge may call the internal `present_implementation_decision` tool after inspecting the relevant code and before the first edit. The tool is intentionally narrow:

1. There must be two to four genuinely viable approaches with meaningful tradeoffs.
2. Exactly one option is marked as the model's recommendation, but the user must make an explicit selection. Pressing Enter does not accept the recommendation.
3. `Other` is always available as a free-text alternative.
4. KernForge asks why the selected approach was chosen.
5. KernForge asks why every listed alternative was not chosen.
6. The completed record is saved before implementation continues. Any later tool calls from the same model batch are deferred so the selected approach can be re-evaluated on a fresh model turn.

The tool must not be used for syntax, naming, formatting, or a choice already fixed by the user's requirements. A pending choice survives session save/resume and blocks file, notebook, shell-write, and git mutations until it is completed. If a non-interactive run reaches a decision checkpoint, it stops with a pending result; resume that session interactively to answer it.

## Local dashboard

Run this command in an interactive KernForge session:

```text
/decision
```

The command starts or reuses a process-owned loopback server and opens the dashboard. It has no subcommands; the dashboard provides:

- current-project-first browsing, an explicit all-projects scope, and filters for project, domain, decision kind, status, and text;
- cursor-based incremental loading over a bounded immutable server snapshot, so concurrent revisions or new captures cannot skip or duplicate records mid-pagination;
- a Decision Fork inspector showing selected, recommended, and rejected branches;
- rationale, tags, domains, risk, project alias, and selected-option editing;
- optimistic revision checks and immutable revision history;
- soft delete and restore;
- learned preference rule enable/disable, pin, and private notes;
- profile rebuild from the canonical journal;
- privacy-reduced or explicit private-metadata JSON export.

The dashboard is available only while the interactive KernForge process is running. `kernforge -command /decision` is rejected because that process would exit immediately and close the server. It binds only to an ephemeral `127.0.0.1` port and requires both a scoped HttpOnly session cookie and an origin-scoped request proof after a one-time fragment-token exchange.

## Storage layout

The canonical files live outside individual repositories:

```text
~/.kernforge/decision-rationales/
  records/
    decision-<id>.json
  history/
    decision-<id>/
      revision-000001.json
      revision-000002.json
  profiles/
    profile.json
```

Each decision is an independently atomic JSON record. This avoids a global read-modify-write conflict when several KernForge processes are active. Record and profile updates use both process-local and OS-level file locks, and revisions provide optimistic compare-and-swap semantics.

On Windows, the decision, history, and profile directories receive a current-user-only protected DACL. Free-text fields are secret-redacted before disk writes. The default export removes session, feature, goal, edit-loop, absolute workspace, project alias, and evidence-reference metadata. Private export must be selected explicitly.

Project identity currently uses a SHA-256 hash of the normalized absolute base-workspace path. Temporary KernForge worktrees therefore remain grouped with their source repository, while clones or moved repositories remain distinct projects. The project alias remains editable in the dashboard.

## Preference promotion

`profile.json` is a rebuildable derived artifact, not a second source of truth. Only records with explicit user selection, explicit user rationale, and detector confidence of at least `0.80` are eligible.

Promotion thresholds are conservative:

| Scope | Minimum support | Additional requirement |
| --- | ---: | --- |
| Project | 2 decisions | Same project and decision criterion |
| Domain | 3 decisions | At least 2 projects |
| Global | 5 decisions | At least 3 projects |

When contrary decisions exist, support must exceed contradictions and represent at least two thirds of the evidence. Preference signatures include the option label as well as its ID so unrelated projects cannot accidentally merge semantically different options that reuse a generic identifier.

Dashboard overrides (`enabled`, `pinned`, and `user_note`) survive profile rebuilds. Profile updates also use revision checks so two open dashboards cannot silently overwrite each other. The derived profile stores a source-journal hash; the dashboard marks it stale and offers a rebuild if source decisions change without a successful profile update.

## Current boundary

The journal and derived profile collect and expose preference evidence. They do not automatically force future implementation choices. A future preference-aware agent or tool can consume enabled profile rules, but keeping capture and application separate avoids a self-reinforcing feedback loop while the evidence set is still small.
