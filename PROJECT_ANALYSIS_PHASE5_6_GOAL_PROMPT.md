# Goal Prompt: Implement Project Analysis Phase 5/6

Use this with `/goal start @PROJECT_ANALYSIS_PHASE5_6_GOAL_PROMPT.md` or `kernforge -goal-file PROJECT_ANALYSIS_PHASE5_6_GOAL_PROMPT.md`.

```text
<task>
Implement Kernforge analyze-project Phase 5 and Phase 6 end to end.

Read PROJECT_ANALYSIS_PHASE5_6_REQUIREMENTS_kor.md first and treat it as the source-of-truth acceptance contract. Also inspect the current implementation before editing, especially:
- cmd/kernforge/analysis_project.go
- cmd/kernforge/analysis_project_contracts.go
- cmd/kernforge/analysis_evidence_packets.go
- cmd/kernforge/analysis_sharding_semantic.go
- cmd/kernforge/analysis_index.go
- cmd/kernforge/analysis_index_v2.go
- cmd/kernforge/analysis_context_v2*.go
- cmd/kernforge/analysis_docs.go
- cmd/kernforge/analysis_dashboard.go
- existing analyze-project tests

Deliver both:
1. Phase 5: graph-guided sharding/retrieval and symbol-level incremental reuse.
2. Phase 6: deterministic claim verifier and security/anti-cheat overlay.
</task>

<default_follow_through_policy>
Do not stop at a proposal. Inspect, implement, test, review your own diff, fix bugs, and update docs. Ask the user only when a requirement is impossible or would require a risky product decision not covered by the requirements document.
</default_follow_through_policy>

<implementation_contract>
- Extend schemas backward-compatibly with omitempty fields.
- Keep existing analyze-project artifacts working.
- Prefer small new files for graph sharding, claim verification, and security overlay instead of growing analysis_project.go unnecessarily.
- Use deterministic internal logic first; external tools must remain optional.
- Do not hardcode fixture or project names.
- Preserve Windows/PowerShell behavior.
- Keep comments and logs ASCII-only.
- Run gofmt only on touched Go files.
- Do not commit or push unless explicitly asked.
</implementation_contract>

<phase5_acceptance>
- AnalysisShard carries graph-aware data such as seed symbols, required packet ids, graph neighborhood, missing evidence classes, and graph fingerprint.
- Shard planning prefers graph communities for startup, IOCTL, callback, handle/memory, RPC, asset/config, build, and generated-artifact paths.
- Evidence packet allocation distinguishes required, supporting, ambiguous, and gap packets.
- Worker prompts consume graph-selected evidence packets, not broad file prefixes.
- Incremental reuse accounts for file, symbol, edge, build-context, overlay, and derived graph fingerprints.
- Tests prove a single symbol change does not invalidate unrelated shards.
</phase5_acceptance>

<phase6_acceptance>
- A deterministic claim verifier runs for every analyze-project run, including model-review-skipped runs.
- High-confidence claims without valid packet evidence are downgraded, unsupported, or blocking.
- The verifier checks packet ids, source scope, line ranges, symbol names, graph edges, deterministic fact-pack conflicts, and security boundary invariants.
- Final reports separate verified facts, inferences, unsupported/downgraded claims, security overlay, and verification follow-through.
- Security/anti-cheat overlay covers Windows driver/IOCTL/callback/handle/memory/RPC/telemetry and UE RPC/replication/asset/config/integrity surfaces.
- Tests prove unsupported high-confidence claims cannot become final facts.
</phase6_acceptance>

<artifact_contract>
Persist run-specific and latest artifacts for:
- graph_shards.json
- graph_reuse.json
- evidence_graph.json
- claim_verification.json
- unsupported_claims.json
- security_overlay.json
- SECURITY_OVERLAY.md
- UNSUPPORTED_CLAIMS.md

Update docs/dashboard/drilldowns and user-facing handoff text so the artifacts are discoverable.
</artifact_contract>

<verification_loop>
Run focused tests for the new graph sharding, incremental reuse, claim verifier, and security overlay behavior.
Then run:
- go vet ./cmd/kernforge
- go test ./cmd/kernforge -count=1 -timeout 15m
- git diff --check

If any check fails, fix the cause and rerun the relevant checks. If a failure is unrelated ambient debt, prove it from output and leave a clear note.
</verification_loop>

<completion_contract>
Before finalizing, perform a review-mode pass over all changed files. Report:
- implemented files and artifacts
- tests run and results
- remaining limitations or follow-up risks
- whether changes are uncommitted

Do not claim Phase 5/6 completion unless every acceptance item in PROJECT_ANALYSIS_PHASE5_6_REQUIREMENTS_kor.md is implemented or explicitly marked as a documented non-goal/blocker.
</completion_contract>
```
