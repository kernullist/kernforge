# goal-to-slice-planner

Use when the user asks to plan, split, sequence, scope, prepare a goal prompt, decide what to do first, or turn a broad development request into implementation slices. Convert vague goals, roadmap items, feature ideas, bug clusters, or quality/documentation work into small, ordered, verifiable slices before any implementation starts.

## Operating Mode

This is a draft-only planning skill by default.

- Do not edit files, run long tests, commit, push, or start implementation unless the user explicitly asks to execute a slice after the plan.
- Do not claim that files were created, updated, staged, tested, or verified while producing the plan.
- Treat file names under "Likely files" as planning candidates only, not artifacts that should exist and not claims that they were modified.
- If the surrounding runtime asks for a final answer audit, state that the output is a plan and that no workspace artifact or verification is claimed unless that is true.

## Workflow

1. Restate the objective in one concrete sentence.
2. Identify non-goals to prevent silent scope growth.
3. Inspect only enough local context to find existing patterns, likely files, tests, documentation surfaces, and risky assumptions.
4. Split the work into 3-7 vertical slices.
5. Make every slice independently useful, reviewable, and verifiable.
6. Recommend the first slice that proves the most important risky assumption, not merely the easiest formatting change.
7. Attach tests, docs, and final validation to the slice where they belong.
8. Keep execution language separate from planning language.

## Slice Rules

Each slice must include:

- Outcome: user-visible or developer-visible result
- Scope: what changes and what explicitly stays out
- Likely files: candidate files or directories to inspect or touch; this is not a change claim
- Acceptance: concrete conditions that prove the slice is done
- Validation: targeted checks first; broader checks only when justified
- Docs: exact docs to update, or no-docs-needed
- Risk: low, medium, or high with one short reason

Keep slices small enough that a reviewer can understand the diff without reading the whole project.

## Planning Heuristics

- Start with discovery only when the target surface is unclear.
- Start with a contract, schema, or test slice when behavior spans multiple modules.
- Start with the core command/API/runtime path when later UX or docs will depend on it.
- Start with docs/status alignment when the request is mostly planning, roadmap, or handoff.
- Defer polish, broad refactors, and optional automation until the core behavior is proven.
- Prefer one risky assumption per slice.
- Avoid giant phase plans that hide dependencies.
- If two slice orders are plausible, recommend the one that minimizes rework and tests the highest-risk assumption first.

## Output Format

Return exactly these sections:

Objective
- <one sentence>

Non-goals
- <items or "None">

Current facts
- <brief code/doc facts actually inspected, or "Not inspected in this turn">

Slices
1. <name>
   Outcome:
   Scope:
   Likely files:
   Acceptance:
   Validation:
   Docs:
   Risk:

Recommended first slice
- <short recommendation and why it reduces risk or rework>

Open questions
- <only blocking questions, or "None">

KernForge execution plan
- [pending] <step>
- [pending] <step>
- [pending] <step>

Draft status
- No files changed, no tests run, and no artifacts created unless explicitly true.

## Wording Guardrails

- Use "candidate", "likely", "to inspect", "to touch", or "planned" for future files.
- Avoid "created", "updated", "implemented", "fixed", "verified", "passed", "changed files", and "artifacts" unless the action already happened.
- Do not say a missing file is a blocker for a draft plan unless the user requested that exact artifact to be created.
- Do not ask questions unless a useful slice plan cannot be produced without the answer.
