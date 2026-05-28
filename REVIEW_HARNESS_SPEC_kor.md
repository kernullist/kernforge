# Kernforge Common Review Harness Spec

Reference point: 2026-05-08

## 1. 목적

Kernforge의 리뷰 기능은 현재 여러 곳에 나뉘어 있다.

1. `/do-plan-review`는 계획 문자열을 planner/reviewer 루프로 검토한다.
2. `/goal`은 구현 후 독립 리뷰, `/verify --full`, `/completion-audit`, 최종 semantic review를 반복한다.
3. interactive final-answer reviewer는 마지막 답변이 실제 작업 상태와 맞는지 확인한다.
4. `CodingHarnessReport`는 acceptance, artifact quality, scenario replay, subagent evidence, test impact, background job 상태를 점검한다.
5. MCP `review_code`는 diff/code/file excerpt를 자유 형식으로 리뷰한다.
6. `/review-pr`는 PR 상태와 diff 목록을 보고서로 남기지만, typed finding/gate는 아직 약하다.

이 스펙의 목표는 위 기능을 하나의 근본 구조로 묶는 것이다.

핵심 원칙:

```text
Review Harness = Intent + ChangeSet + Evidence + PolicyPack + Findings + Gate
```

리뷰 대상이 plan이든 실제 code change든, root-cause report든, PR이든 같은 구조를 통과해야 한다. 차이는 reviewer graph와 policy pack이 달라지는 것뿐이어야 한다.

## 2. 현재 구조의 문제

현재 구현은 좋은 조각을 많이 갖고 있지만 공통 계약이 약하다.

1. Plan review는 `APPROVED` / `NEEDS_REVISION` 문자열 판정 중심이다.
2. Goal review는 concrete evidence를 넣지만 review result schema가 별도로 고립되어 있다.
3. Completion audit는 checklist 중심이라 reviewer finding을 line/path/category 단위로 재사용하기 어렵다.
4. Coding harness finding은 `severity/title/detail` 수준이라 보안, 안정성, false positive, test gap 같은 domain category를 표현하기 부족하다.
5. MCP review는 유용하지만 자유 형식 output이라 fix loop, PR comment, gate policy에 바로 연결하기 어렵다.
6. Review evidence 수집 로직이 goal, MCP, completion audit, PR review에 중복된다.

따라서 먼저 typed review ledger를 만들고, 기존 기능들은 그 ledger를 쓰는 consumer/provider로 이동해야 한다.

## 3. 설계 목표

### 3.1 기능 목표

1. plan-review와 code-change review를 같은 harness에서 처리한다.
2. 실제 diff, patch transaction, checkpoint diff, changed files, untracked file excerpt, verification result, background job state를 한 번에 evidence pack으로 만든다.
3. reviewer output을 machine-readable finding으로 정규화한다.
4. blocker/high finding은 repair loop로 되돌릴 수 있어야 한다.
5. `/completion-audit`, `/goal`, `/review pr`, MCP `review`, final-answer reviewer가 같은 review run artifact를 참조할 수 있어야 한다.
6. Windows kernel/anti-cheat/security 작업에서는 domain rubric을 자동으로 추가한다.

### 3.2 비목표

1. 첫 단계에서 모든 리뷰를 멀티 에이전트로 만들 필요는 없다.
2. 모든 finding을 자동으로 line comment로 게시할 필요는 없다.
3. 모델이 낸 자연어 리뷰를 완벽한 정적분석 결과처럼 취급하지 않는다.
4. deterministic gate와 model reviewer의 책임을 섞지 않는다.

## 4. 핵심 개념

### 4.1 Review Target

리뷰 대상의 종류다.

1. `plan`: 구현 계획, 설계안, task graph.
2. `change`: 현재 workspace diff, checkpoint 이후 변경, patch transaction.
3. `final_answer`: 사용자에게 보이기 직전의 답변.
4. `goal_iteration`: `/goal`의 한 iteration 결과.
5. `pr`: current branch 또는 GitHub PR.
6. `analysis_report`: project analysis, root-cause, fuzz/source-scan report.

### 4.2 Review Mode

작업 불확실성의 종류에 따라 reviewer graph와 gate policy를 고른다.

1. `core_build`: 설계 불확실성이 크다. 적은 reviewer, 강한 architecture/security review.
2. `live_fix`: 작업량 불확실성이 크다. 여러 worker 결과와 repro/test evidence를 합친다.
3. `research`: 조사, competing design, PoC, decision review 중심.
4. `refactor`: dependency impact, staged rollout, regression evidence 중심.
5. `security_hardening`: threat model, bypass path, false positive, telemetry quality 중심.
6. `ui_polish`: visual diff, interaction, accessibility, layout regression 중심.
7. `general_change`: 기본 code review.

### 4.3 Evidence Pack

리뷰어가 볼 수 있는 근거 묶음이다. 모델 prompt에 그대로 다 넣는 것이 아니라, artifact로 저장하고 primary/cross route와 lens prompt에는 필요한 부분만 압축한다.

포함 후보:

1. acceptance contract
2. user goal and non-goals
3. review target metadata
4. git status
5. git diff stat
6. git diff excerpt
7. checkpoint diff
8. patch transaction entries
9. changed path list
10. bounded untracked file excerpts
11. latest verification report
12. test impact report
13. coding harness report
14. completion audit summary
15. task graph and open task nodes
16. background jobs and bundles
17. project analysis fact pack
18. security surface hints
19. recent failure repair state

### 4.4 Policy Pack

리뷰 기준 묶음이다. prompt text뿐 아니라 deterministic checks, required evidence, gate thresholds를 함께 가진다.

기본 pack:

1. `base_correctness`
2. `base_security`
3. `base_stability`
4. `base_maintainability`
5. `base_testability`

Kernforge domain pack:

1. `windows_kernel_driver`
2. `anti_cheat_telemetry`
3. `memory_scan`
4. `unreal_integrity`
5. `fuzzing_campaign`
6. `mcp_tooling`
7. `docs_artifact`

## 5. ReviewRun 스키마

초기 구현은 Go struct로 두되, JSON artifact와 markdown report가 같은 정보를 표현해야 한다.

```text
ReviewRun
  id
  schema_version
  kernforge_version
  policy_pack_versions
  review_fingerprint
  trigger
  target
  mode
  flow
  request_analysis
  auto_triggered
  status
  machine_status
  exit_code
  objective
  implementation_reply
  created_at
  workspace
  branch
  profiles
  model_plan
  change_set
  evidence
  freshness
  redaction
  policy_packs
  reviewer_runs
  single_model_second_pass
  merge_result
  result
  findings
  cross_review_triage
  gate
  waivers
  repair_plan
  next_command_results
  artifact_refs
  audit_trail
```

`trigger` 값:

1. `explicit_command`: 사용자가 `/review ...`를 실행했다.
2. `explicit_mcp`: MCP client가 `review` tool을 호출했다.
3. `post_change`: 구현/수정 요청 뒤 workspace change가 생겼다.
4. `goal_iteration`: `/goal` iteration 뒤 자동 review가 필요하다.
5. `pre_final_answer`: final answer 직전 gate에서 review가 필요하다.
6. `pre_git_write`: commit/push/PR 같은 git write 이전에 review gate가 필요하다.

`auto_triggered=true`인 review는 사용자가 별도로 review를 요청하지 않아도 설정에 따라 실행된 review다.

### 5.1 ReviewRequestAnalysis

사용자나 MCP client는 자신에게 필요한 리뷰 타입을 모르는 경우가 많다. 따라서 `/review`와 MCP `review`는 target을 먼저 요구하지 않고, 요청 내용을 분석해 적절한 리뷰 흐름을 선택할 수 있어야 한다.

```text
ReviewRequestAnalysis
  original_request
  inferred_target
  inferred_mode
  selected_flow
  confidence
  evidence_needs
  policy_packs
  candidate_flows
  reason
  ambiguity_warnings
```

`selected_flow`는 target, mode, collector, reviewer route/lens policy, follow-up policy를 묶은 실행 경로다.

기본 flow:

1. `change_review`: dirty diff, provided diff/code, patch transaction을 리뷰한다.
2. `plan_review`: 구현 계획이나 설계안을 리뷰한다.
3. `selection_review`: viewer selection 또는 explicit paths를 리뷰한다.
4. `pr_review`: current PR metadata, checks, diff를 리뷰한다.
5. `goal_review`: active goal iteration 또는 completion state를 리뷰한다.
6. `analysis_review`: analysis/root-cause/fuzz report의 논리와 evidence gap을 리뷰한다.
7. `security_review`: 보안/커널/안티치트 risk가 강한 변경을 깊게 리뷰한다.
8. `evidence_review`: 사용자가 "이게 충분한가"처럼 증거 적정성을 물을 때 verification/evidence 중심으로 리뷰한다.

라우팅 원칙:

1. 사용자가 target을 명시하면 그것을 우선하되, evidence가 맞지 않으면 warning을 남긴다.
2. target이 없으면 request text, dirty state, selection, PR context, active goal, latest analysis artifact를 함께 본다.
3. 보안 민감 신호가 있으면 더 넓은 `security_review`를 우선한다.
4. 여러 후보가 비슷하면 가장 보수적인 flow를 선택하고 `candidate_flows`에 대안을 남긴다.
5. confidence가 낮아도 reviewable evidence가 있으면 질문으로 멈추지 말고 best-effort review를 실행한다.
6. reviewable evidence가 전혀 없으면 모델을 호출하지 않고 필요한 입력과 추천 명령을 안내한다.
7. 모호함은 gate를 실패시키는 것이 아니라 `ambiguity_warnings`와 `next_commands`로 남긴다.

예:

1. `/review "이 변경 커널 안정성 관점에서 봐줘"`
   - target=`change`, mode=`security_hardening`, flow=`security_review`.

2. `/review "이 설계 괜찮아?"`
   - target=`plan`, mode=`core_build`, flow=`plan_review`.

3. MCP `review` with `request="review this patch for regression risk"` and `diff=<...>`
   - target=`change`, mode=`live_fix`, flow=`change_review`.

4. `/review` with active selection
   - target=`selection`, mode=`general_change`, flow=`selection_review`.

5. `/review` with dirty driver files and no request text
   - target=`change`, mode=`security_hardening`, flow=`security_review`.

### 5.2 ChangeSet

```text
ChangeSet
  source
  base_ref
  head_ref
  checkpoint_id
  patch_transaction_id
  changed_paths
  added_paths
  modified_paths
  deleted_paths
  renamed_paths
  binary_paths
  untracked_paths
  diff_stat
  diff_excerpt
  fingerprint
```

`source` 값:

1. `git_worktree`
2. `checkpoint`
3. `patch_transaction`
4. `provided_diff`
5. `provided_code`
6. `github_pr`

### 5.3 ReviewResult

모델 성능이 낮거나 응답이 흔들려도 review result는 항상 같은 구조로 남아야 한다. 자연어 summary는 사람이 읽는 표면일 뿐이고, gate와 automation은 structured fields만 신뢰한다.

```text
ReviewResult
  verdict
  summary
  scope_reviewed
  scope_not_reviewed
  key_risks
  verified_evidence
  missing_evidence
  finding_count
  blocking_count
  warning_count
  model_quality
  degraded
  degraded_reason
```

`model_quality`:

1. `strong`: structured output이 충분하고 evidence/path/fix가 구체적이다.
2. `usable`: 일부 필드가 약하지만 deterministic gate와 합쳐 사용할 수 있다.
3. `weak`: 자유 형식 또는 근거 부족 응답이라 normalizer 보정이 많이 필요했다.
4. `failed`: provider failure, empty response, unparseable response.

`degraded=true`는 모델 reviewer가 약하거나 실패했지만 deterministic reviewer와 evidence 기반으로 review artifact를 생성했다는 뜻이다. 이 경우 gate는 `approved`보다 보수적으로 동작해야 한다.

### 5.4 ReviewFinding

리뷰 하네스의 가장 중요한 단위다.

```text
ReviewFinding
  id
  source
  reviewer_role
  severity
  category
  confidence
  quality
  path
  line
  symbol
  title
  evidence
  impact
  required_fix
  test_recommendation
  blocks_gate
  related_policy
  evidence_refs
  fix_refs
  verification_refs
  raw_excerpt
```

`severity`:

1. `blocker`: merge/complete 불가.
2. `high`: 수정 권고가 강하고, security/stability 위험이 크다.
3. `medium`: 명확한 버그 또는 유지보수 위험.
4. `low`: 개선점.
5. `info`: 기록용.

`category`:

1. `goal_mismatch`
2. `architecture`
3. `correctness`
4. `security`
5. `stability`
6. `performance`
7. `test_gap`
8. `maintainability`
9. `style`
10. `documentation`
11. `false_positive`
12. `bypass_surface`
13. `operational_risk`
14. `evidence_gap`

`quality`:

1. `complete`: path/symbol, evidence, impact, required_fix가 모두 구체적이다.
2. `partial`: 일부 필드는 부족하지만 검토 가치가 있다.
3. `weak`: 근거가 약해 gate blocker로 쓰기 어렵다.
4. `invalid`: diff/evidence와 맞지 않아 reviewer quality issue로만 기록한다.

Finding quality rule:

1. blocker/high finding은 `evidence`, `impact`, `required_fix`가 있어야 한다.
2. source path가 없으면 symbol 또는 affected surface가 있어야 한다.
3. `blocks_gate=true`인 finding은 deterministic finding이거나 quality가 `complete`여야 한다.
4. quality가 `weak`이면 gate blocker로 승격하지 않고 warning 또는 reviewer quality issue로 남긴다.
5. quality가 `invalid`이면 사용자에게 bug finding처럼 보여주지 않는다.

### 5.4.1 CrossReviewTriageLedger

optional cross-review route가 actionable finding을 만들면 primary repair loop가 이를 조용히 무시할 수 없어야 한다. 따라서 cross reviewer의 actionable finding은 별도 triage ledger에 복사되고, 각 항목은 정확히 하나의 상태를 가져야 한다.

```text
CrossReviewTriageLedger
  items
  total_count
  status_counts
  incomplete_count
  blockers
  warnings

CrossReviewTriageEntry
  finding_id
  reviewer_role
  severity
  category
  path
  line
  symbol
  title
  required_fix
  triage_status
  technical_reason
  fix_refs
  changed_paths
  verification_refs
  evidence_refs
```

`triage_status` 값:

1. `accepted_fixed`: finding을 수용했고 이미 수정 근거가 있다.
2. `accepted_deferred`: finding을 수용했지만 현재 턴에서 고치지 않고 명확한 이유로 보류한다.
3. `rejected_with_reason`: finding을 기각하되 코드, diff, path, symbol, evidence 같은 기술 근거가 있어야 한다.
4. `needs_user_decision`: primary loop가 자동으로 결정할 수 없어 사용자 결정이 필요하다.

일관성 규칙:

1. `accepted_fixed`는 `fix_refs` 또는 `changed_paths` 중 하나 이상을 가져야 한다.
2. `accepted_deferred`는 `technical_reason`에 보류 이유를 남겨야 한다.
3. `rejected_with_reason`은 단순 선호가 아니라 evidence 기반 기술 이유가 있어야 한다.
4. 위 조건을 만족하지 못하면 deterministic `RF-CROSS-TRIAGE-001` blocker가 추가되어 review gate를 통과하지 못한다.
5. ledger는 `ReviewRun` JSON과 markdown report 모두에 기록되어 나중에 어떤 cross-review finding이 수용, 보류, 기각, 사용자 결정으로 갔는지 감사할 수 있어야 한다.

### 5.4.2 SingleModelSecondPassReview

distinct cross-review route가 없다는 이유로 첫 primary review 응답을 그대로 신뢰하면 안 된다. single-model mode는 degraded fallback이 아니라 primary route를 한 번 더 호출하는 별도 runtime phase다.

```text
SingleModelSecondPassReview
  enabled
  fingerprint
  status
  cache_hit
  model
  reviewed_at
  reviewed_paths
  finding_count
  prompt_path
  raw_output_path
```

실행 규칙:

1. cross reviewer가 없고 main first-pass review가 usable 이상이면 `single_model_second_pass` phase를 별도 model call로 실행한다.
2. second-pass prompt는 원 요청, touched files, relevant diff, implementation reply, 최신 verification summary, 1차 structured/raw review를 함께 받는다.
3. focus checklist는 touched function, call site, ABI/data contract, default initialization, buffer/length, error path, cancellation/timeout, logging/output compatibility, stale docs, missing focused validation을 포함한다.
4. 결과 finding은 기존 `ReviewFinding`과 같은 parser/gate/repair loop로 들어간다. concrete blocker가 나오면 일반 repair path가 소비한다.
5. 같은 diff와 review evidence를 반복 검토해 loop가 무한 반복되지 않도록 fingerprint를 저장하고, approved 또는 approved_with_warnings second-pass는 `second_pass_review_cache`에 기록해 재사용한다.

### 5.5 GateDecision

```text
GateDecision
  verdict
  reason
  blocking_findings
  warning_findings
  required_actions
  waiver_allowed
  waiver_reason_required
  next_commands
  quality_notes
```

`verdict`:

1. `approved`
2. `approved_with_warnings`
3. `needs_revision`
4. `blocked`
5. `insufficient_evidence`

Gate rule:

1. blocker finding이 하나라도 있으면 `needs_revision` 또는 `blocked`.
2. verification이 명시적으로 required인데 evidence가 없으면 `insufficient_evidence`.
3. 사용자가 명시적으로 "검토하고 버그를 수정" 같은 repair intent를 준 흐름에서는 complete high finding뿐 아니라 actionable medium correctness/stability/performance finding도 `needs_revision`으로 올린다.
4. 수정 전 리뷰의 style/formatting/maintainability finding은 코드 수리를 막는 blocker로 승격하지 않고 warning으로 남긴다.
5. pre-write review에서는 low severity라도 Allman brace, indentation, formatting처럼 지금 쓰려는 patch 자체의 스타일 위반이면 rewrite가 가능한 actionable warning으로 보고 차단한다.
6. pre-write review의 "build/test verification was not run" 류 순수 검증 gap은 edit preview를 막지 않고 post-edit verification obligation으로 남긴다.
7. pre-write review에서 "함수 후반부가 보이지 않는다", "complete function body is not visible", "제공된 preview/evidence가 부족하다"처럼 하네스 증거 패키지 부족을 지적한 `evidence_gap`은 코드 수리 루프로 넘기지 않는다. 먼저 하네스가 current file context, selection 후반부, cleanup/success 경로를 증거에 보강해야 한다.
8. 하네스 증거 부족 경고는 implementation model에게 패치를 다시 쓰게 할 근거가 아니다. 실제 코드 누락, header/API surface 누락, member declaration 누락, requested scope 미구현처럼 patch 자체의 결함을 가리키는 `evidence_gap`만 actionable warning으로 차단한다.
9. `/review`, 자연어 리뷰, pre-fix repair check는 main-first로 동작한다. active main model이 먼저 구조화 리뷰를 만들고, optional cross route가 있으면 같은 evidence와 primary draft를 받아 cross-check로 확인한다. cross route가 없으면 primary route로 별도 `single_model_second_pass` review를 실행한다. domain 전문성은 review lens로 주입한다. pre-fix의 cross reviewer 실패, 빈 응답, `weak` 품질은 degraded/warning으로 남기되 main review finding 보고와 repair loop 시작을 막지 않는다.
10. pre-write review는 hard edit gate다. 실제 edit preview가 있는 상태에서 필수 main/cross/second-pass reviewer가 실패하거나 `weak` 품질이면 `insufficient_evidence`로 write를 막고 edit 루프를 중단한다. 이 상태는 코드 수정 지침이 아니라 reviewer route 장애로 보고해야 하며, implementation model에게 웹 검색이나 반복 패치를 시키지 않는다.
11. build/test 검증 명령이 사용자에게 거절되어 `skipped`/`declined`로 기록된 뒤에는 같은 턴에서 동일 검증 재실행이나 `latest` background poll을 실행하지 않는다. 하네스는 tool progress를 emit하기 전에 `NOT_EXECUTED` tool result와 guidance로 접고, 미검증 gap만 final/status에 남긴다.
12. security pack에서 high severity가 있으면 기본적으로 `needs_revision`.
13. docs-only change는 test gap을 warning으로 낮출 수 있다.
14. user가 명시적으로 waiver를 요청하지 않으면 blocker waiver는 불가.

### 5.6 Review Artifact Versioning

리뷰 artifact는 CLI, MCP, hook, dashboard, completion audit가 함께 읽는 계약이다. 따라서 `review.json`은 명시적인 schema version과 writer version을 가져야 한다.

```text
ReviewSchemaInfo
  schema_version
  kernforge_version
  min_reader_version
  policy_pack_versions
  written_at
```

버전 규칙:

1. `schema_version`은 예를 들어 `review_run.v1`처럼 major version을 드러낸다.
2. additive field는 reader가 무시할 수 있어야 한다.
3. 지원하지 않는 major version은 hook, git write, MCP automation에서 fail-closed로 처리한다.
4. `schema_version`이 없는 artifact는 legacy artifact로 보고 historical report로만 읽는다.
5. `policy_pack_versions`가 바뀌면 기존 review는 stale 후보가 된다.
6. markdown report는 사람이 보는 rendering일 뿐이고, gate는 JSON schema만 신뢰한다.

### 5.7 Staleness And Invalidation

최신 review가 있어도 변경이나 검증 상태가 달라지면 더 이상 gate evidence로 쓰면 안 된다.

```text
ReviewFreshness
  review_fingerprint
  stale
  stale_reason
  superseded_by
  checked_at
  invalidated_by
```

`review_fingerprint`는 target, mode, change set fingerprint, selected policy packs, required verification ids, acceptance contract hash, relevant config hash를 합친 값이다.

stale 처리 규칙:

1. changed paths, diff fingerprint, patch transaction id가 바뀌면 stale.
2. gate가 의존한 verification artifact가 바뀌거나 사라지면 stale.
3. acceptance contract, objective, non-goals가 바뀌면 stale.
4. branch, base ref, PR head sha가 바뀌면 stale.
5. policy pack version이나 model role config가 바뀌면 warning 또는 stale로 표시한다. gate-critical pack 변경은 stale이다.
6. waiver가 만료, 취소, scope mismatch 상태가 되면 stale.
7. unsupported schema version이면 stale가 아니라 unreadable gate로 취급한다.

사용 규칙:

1. `pre_git_write` gate는 non-stale review만 인정한다.
2. final answer는 stale review를 historical evidence로 언급할 수 있지만 completion claim의 근거로 쓰면 안 된다.
3. stale review는 `/review` 또는 더 구체적인 추천 명령을 `next_commands`에 남긴다.
4. `.kernforge/reviews/latest.json`은 가장 최근 run을 가리키지만, freshness check 없이 "승인된 최신 상태"로 해석하지 않는다.

### 5.8 Redaction And Sensitive Evidence

리뷰 하네스는 prompt, raw output, diff excerpt, evidence excerpt를 저장하므로 secret과 민감 로그를 먼저 다뤄야 한다.

```text
ReviewRedactionReport
  status
  redacted
  patterns
  sensitive_refs
  warnings
```

redaction 정책:

1. API key, OAuth token, PAT, private key, password, cookie, signed URL, connection string은 artifact 저장 전에 redaction한다.
2. `.env`, credential file, dump, crash report, user PII 가능성이 큰 파일은 기본적으로 excerpt를 제한하거나 source ref만 남긴다.
3. 확신할 수 없는 민감 excerpt는 저장하지 않고 `sensitive_refs`에 경로와 reason만 남긴다.
4. MCP response는 raw prompt/output 전문을 반환하지 않는다. artifact ref와 structured finding만 반환한다.
5. model reviewer prompt에도 필요한 최소 excerpt만 넣고, redaction report를 함께 전달해 누락 이유를 설명한다.
6. redaction failure가 의심되면 review gate를 approved로 승격하지 않고 `approved_with_warnings` 또는 `insufficient_evidence`로 둔다.

### 5.9 Waiver And Override Records

waiver는 finding을 삭제하는 기능이 아니라, 사용자가 특정 gate risk를 알고도 제한적으로 진행했다는 감사 기록이다.

```text
ReviewWaiver
  id
  finding_id
  reason
  actor
  created_at
  expires_at
  scope
  allowed
  status
```

waiver 규칙:

1. blocker/high finding waiver는 explicit command와 reason이 있어야 한다.
2. waiver scope는 review id, finding id, path, branch, expiration을 포함해야 한다.
3. security boundary, credential leak, destructive external write risk는 기본적으로 waiver 불가이며 policy pack이 명시적으로 허용해야 한다.
4. waiver가 있어도 finding은 report에 남고, gate에는 `approved_with_warnings` 이상으로만 반영한다.
5. expired/revoked/scope-mismatch waiver는 freshness check에서 stale 또는 blocked reason이 된다.
6. MCP는 waiver를 생성하지 않는다. CLI explicit command 또는 local policy file만 waiver source가 될 수 있다.

권장 명령:

```text
/review waive <finding-id> --reason <text>
/review waivers
/review waive revoke <waiver-id>
```

### 5.10 CLI/MCP Status Contract

사람이 읽는 verdict와 자동화가 읽는 상태는 같은 의미를 가져야 한다. CLI exit code와 MCP `machine_status`는 아래 표를 따른다.

```text
ReviewStatusContract
  verdict
  machine_status
  exit_code
  retryable
```

권장 mapping:

1. `approved` -> `ok`, exit code `0`.
2. `approved_with_warnings` -> `warning`, interactive exit code `0`, CI strict mode exit code `1`.
3. `needs_revision` -> `needs_revision`, exit code `2`.
4. `blocked` -> `blocked`, exit code `3`.
5. `insufficient_evidence` -> `insufficient_evidence`, exit code `4`.
6. provider/runtime failure -> `review_failed`, exit code `5`.
7. usage error or no reviewable target -> `usage_error`, exit code `6`.

MCP response는 exit code 대신 `machine_status`, `status_code`, `retryable`, `recommended_command`를 반환한다. CLI와 MCP 모두 gate를 통과하지 못한 run도 artifact를 남겨야 한다.

## 6. 파이프라인

### 6.1 Collect

`ReviewCollector`가 review target을 보고 evidence pack을 만든다.

1. session state 수집
2. acceptance contract 수집
3. changed files 계산
4. diff/checkpoint/patch transaction 중 가장 정확한 change source 선택
5. latest verification, test impact, coding harness, job supervisor 수집
6. analysis fact pack과 security surface hint 연결
7. evidence budget에 맞게 prompt용 excerpt 생성

권장 선택 순서:

1. checkpoint diff가 있으면 우선 사용한다.
2. checkpoint가 없고 active patch transaction이 있으면 patch transaction을 사용한다.
3. 그 외에는 git diff/staged/untracked를 사용한다.
4. MCP가 diff/code를 제공하면 provided input을 우선 사용한다.

### 6.2 Analyze And Classify

`ReviewRequestAnalyzer`가 먼저 요청 내용을 분석하고, 그 결과를 `ReviewModeClassifier`가 mode와 policy pack으로 확정한다. 사용자는 `/review pr`처럼 target을 알아도 되고, 그냥 "이 변경 안정성 관점에서 봐줘"처럼 자연어로 요청해도 된다.

주요 신호:

1. user prompt
2. acceptance contract mode
3. changed path extensions
4. project analysis domain hints
5. verification classifier output
6. security-sensitive keywords
7. current slash command context
8. MCP provided diff/code/path/request fields

예:

1. `.sys`, `DriverEntry`, `IOCTL`, `IRQL`, `MmCopyMemory`가 보이면 `security_hardening` + `windows_kernel_driver`.
2. `telemetry`, `ETW`, `event schema`, `provider`가 보이면 `anti_cheat_telemetry`.
3. 많은 rename/delete가 있으면 `refactor`.
4. PR review 명령이면 `pr` target + mode auto.

분석 출력:

1. inferred target
2. inferred mode
3. selected flow
4. confidence
5. selected policy packs
6. required evidence
7. ambiguity warnings
8. next command 후보

Classifier는 너무 똑똑한 UX를 만들려고 사용자에게 계속 되묻지 않는다. 요청이 모호해도 reviewable evidence가 있으면 보수적인 flow를 골라 실행하고, 결과에 "이 기준으로 리뷰했다"를 명확히 남긴다.

### 6.3 Deterministic Reviewers

모델 호출 전 항상 deterministic reviewer를 먼저 실행한다.

기본 reviewer:

1. acceptance reviewer
2. change accounting reviewer
3. verification evidence reviewer
4. test impact reviewer
5. background job reviewer
6. artifact quality reviewer
7. user-change isolation reviewer
8. policy-required evidence reviewer

이 단계는 이미 있는 `CodingHarnessReport`, `CompletionAuditArtifact`, `TestImpactReport`, `JobSupervisorReport`를 재사용한다.

### 6.4 Model Reviewers

mode별 role을 선택한다.

`core_build`:

1. design reviewer
2. security/edge-case reviewer
3. testability reviewer
4. final gate reviewer

`live_fix`:

1. repro/evidence reviewer
2. regression reviewer
3. merge gate reviewer

`security_hardening`:

1. threat model reviewer
2. bypass path reviewer
3. false positive reviewer
4. stability/OS compatibility reviewer
5. telemetry/test generator

`refactor`:

1. dependency impact reviewer
2. behavioral preservation reviewer
3. regression reviewer

초기 MVP는 단일 reviewer model 호출로 시작해도 된다. 중요한 것은 output을 같은 `ReviewFinding`으로 정규화하는 것이다.

### 6.4.1 Model Plan And Configuration UX

리뷰 전문성은 더 이상 role별 모델 수를 늘려서 표현하지 않는다. 실행 route는 `primary_reviewer`와 `cross_reviewer` 두 축으로 유지하고, security/design/false-positive/regression/test/final 관점은 `required_lenses`와 `optional_lenses`로 같은 프롬프트에 주입한다.

```text
ReviewModelPlan
  strategy
  required_roles      # primary route, plus required cross only when the gate needs it
  optional_roles      # configured cross route when available
  required_lenses
  optional_lenses
  assigned_models
  missing_roles       # route availability only, not domain lens availability
  degraded_roles
  route_limits
  user_guidance
```

`strategy` 값:

1. `single`: active main model이 primary review route로 실행된다.
2. `dual`: primary route 뒤에 독립 cross route가 한 번 더 실행된다.
3. `deterministic_only`: 모델 없이 deterministic gate만 실행한다.
4. `degraded`: 필요한 route가 실패했거나 약한 출력으로 끝났다.

Route:

1. `primary_reviewer`: active main model. `/model` 또는 `/provider`로 바꾼다.
2. `cross_reviewer`: 선택적 독립 second-pass reviewer. `/review models cross`로만 설정한다.

Lens:

1. `correctness`: 일반 correctness/stability/maintainability.
2. `design`: architecture/core build/complexity.
3. `security`: privileged path, trust boundary, bypass risk.
4. `false_positive`: detection, telemetry, anti-cheat 오탐과 운영 해석 가능성.
5. `regression`: behavior compatibility, OS/version drift, refactor risk.
6. `test`: verification, replay, coverage gap.
7. `final_gate`: final readiness와 residual risk.

Flow별 기본 추천:

1. `change_review`: `correctness`; security-sensitive이면 `security` lens 추가.
2. `plan_review`: `design`; security-sensitive이면 `security` lens 추가.
3. `security_review`: `security`, 필요 시 `false_positive`; high-risk이면 `test` 또는 `final_gate` lens 추가.
4. `refactor`: `correctness` + `regression`.
5. `pr_review`: `correctness`; checks가 실패했으면 `test` lens 추가.
6. `goal_review`: `correctness` + `final_gate`.

모델 설정 UX:

```text
/review models
/review models status
/review models cross
/review models clear cross
```

`/review models`는 route 설정 surface다. domain 전문성은 별도 모델 role이 아니라 lens로 자동 선택된다. 직접 지정이 필요한 script/MCP client는 `/review models cross <provider> <model> [reasoning_effort]`를 사용할 수 있다.

예:

```text
Review model guidance:
- Review specialization is applied as lenses, not separate reviewer routes: required=correctness,security optional=false_positive.
- Single-model review mode is active; no independent cross reviewer is configured for this run.
- Configure an independent second pass with: /review models cross
```

기존 설정과의 관계:

1. 기존 `PlanReviewConfig` 기반 plan-review reviewer 경로는 제거한다.
2. 기존 `ProjectAnalysis.ReviewerProfile`은 project analysis 전용 reviewer로만 유지한다.
3. 기존 role-specific `review.role_models` 항목은 호환용 cross-route fallback으로 읽을 수 있지만, 새 설정 UX는 `cross_reviewer`만 쓴다.
4. 기존 specialist profile은 domain 조사/작업 lens로 유지하되, review gate route와 혼합하지 않는다.
5. model route scheduler는 primary/cross route가 같은 provider/model/base_url/reasoning_effort를 공유할 때 saturation을 막는다.

설정 부족 처리:

1. primary route가 없으면 deterministic-only review를 실행하고 `model_plan.strategy=deterministic_only`로 표시한다.
2. cross route가 없으면 single-model review mode로 실행한다. 이것은 실패가 아니라 명시적인 residual-risk 상태다.
3. high-risk security/detection review는 `security`와 `false_positive` lens를 적용하되, 전용 role 모델 누락을 `missing_roles`로 기록하지 않는다.
4. 더 강한 독립 검토가 필요하면 `next_commands`는 role별 모델이 아니라 `/review models cross`를 추천한다.
5. 사용자에게 설정을 강요하지 않는다. "이번 run은 single-model/cross 중 어떤 mode였는지"와 "더 강한 구성을 원하면 이 명령"을 짧게 보여준다.

### 6.4.2 Multi-Model Merge Policy

primary/cross reviewer route를 함께 실행하면 결과를 단순 concatenate하면 안 된다. `ReviewFinding`은 중복 제거, 충돌 기록, severity 보정, deterministic gate 보존을 거쳐 하나의 `merge_result`로 합쳐야 한다.

```text
ReviewMergeResult
  merged_findings
  suppressed_duplicates
  conflicts
  severity_changes
  deterministic_preserved
  final_reviewer_notes
```

merge 규칙:

1. deterministic finding은 모델 reviewer가 반박해도 삭제하지 않는다.
2. 같은 path/symbol/category/evidence fingerprint를 가진 finding은 deduplicate한다.
3. 중복 finding의 severity가 다르면 기본적으로 더 높은 severity를 유지한다.
4. severity downgrade는 `final_gate` lens 또는 cross route가 concrete reason을 남긴 경우에만 가능하다.
5. security, stability, data-loss, external-write risk finding은 downgrade보다 conflict 기록을 우선한다.
6. `false_positive` lens가 detection quality finding을 냈고 `security` lens 관점에서 다른 blocker가 없어도 false-positive finding은 유지한다.
7. 모델 간 의견 충돌은 숨기지 않고 `conflicts`에 남긴다.
8. `quality=weak` finding은 merge 후에도 gate blocker로 승격하지 않는다.
9. final gate는 `merge_result.merged_findings`와 deterministic evidence를 기준으로 계산한다.

역할별 권한:

1. specialist reviewer는 자기 domain finding을 추가할 수 있다.
2. final gate reviewer는 finding을 합치고 우선순위를 조정할 수 있다.
3. final gate reviewer도 deterministic blocker와 complete-quality security blocker를 삭제할 수 없다.
4. normalizer는 모델 응답보다 schema invariant를 우선한다.

### 6.5 Normalize

`FindingNormalizer`는 model output을 schema로 바꾼다.

필수 규칙:

1. path/line이 없으면 symbol 또는 affected surface를 요구한다.
2. concrete required_fix가 없는 high/blocker finding은 `evidence_gap`으로 낮추거나 reviewer quality issue로 표시한다.
3. "maybe", "could be"만 있는 finding은 confidence를 낮춘다.
4. deterministic blocker는 모델이 approved라고 해도 유지한다.
5. 같은 path/category/title은 deduplicate한다.
6. finding마다 `quality`를 계산한다.
7. model output이 JSON/structured block을 지키지 못해도 fallback parser로 verdict, bullet findings, path-like token, required-fix 문장을 추출한다.
8. fallback parser가 아무 근거도 추출하지 못하면 model reviewer를 `failed`가 아니라 `weak` 또는 `failed`로 표시하고 deterministic findings만 유지한다.

Reviewer output contract:

```text
REVIEW_RESULT
verdict: approved|approved_with_warnings|needs_revision|blocked|insufficient_evidence
summary: <one paragraph>
scope_reviewed:
- <path/surface>
scope_not_reviewed:
- <path/surface or none>
findings:
- severity: blocker|high|medium|low|info
  category: correctness|security|stability|performance|test_gap|...
  path: <path or empty>
  line: <line or empty>
  symbol: <symbol or surface>
  evidence: <specific evidence from diff/context>
  impact: <why it matters>
  required_fix: <concrete fix>
  test_recommendation: <specific validation>
missing_evidence:
- <gap>
```

모델이 이 형식을 완전히 지키지 않아도 normalizer가 최대한 구조화한다. 하지만 structured output을 지키지 못한 사실은 `model_quality`와 `quality_notes`에 기록한다.

### 6.5.1 Degraded Model Fallback

리뷰 모델이 약하거나 실패해도 하네스는 빈 승인으로 끝나면 안 된다.

Fallback 순서:

1. deterministic reviewers를 먼저 실행한다.
2. model reviewer 응답이 비었거나 provider failure면 `ReviewResult.degraded=true`로 둔다.
3. free-form text가 있으면 fallback parser로 finding 후보를 추출한다.
4. finding이 약하면 `quality=weak`으로 두고 gate blocker로 승격하지 않는다.
5. deterministic blocker와 required evidence gap은 모델 결과와 무관하게 gate에 반영한다.
6. security-sensitive change에서 model review가 실패하면 gate는 최소 `approved_with_warnings` 이하로만 내려갈 수 있고, evidence gap이 있으면 `insufficient_evidence`로 둔다.
7. review summary는 "model review degraded"를 숨기지 않는다.

Degraded report에도 반드시 남길 것:

1. reviewed scope
2. changed paths
3. deterministic findings
4. missing evidence
5. model failure or weakness reason
6. safe next command

이렇게 해야 local model, overloaded provider, small reviewer model에서도 review artifact의 최소 품질이 유지된다.

### 6.6 Gate

`ReviewGateEvaluator`가 deterministic findings와 model findings를 합쳐 판정한다.

Gate는 모델의 승인보다 강해야 한다.

예:

1. 모델이 `APPROVED`라도 latest verification이 failed면 `blocked`.
2. 모델이 `APPROVED`라도 required artifact가 없으면 `blocked`.
3. 모델이 `APPROVED`라도 security policy required evidence가 없으면 `insufficient_evidence`.
4. 모델이 `NEEDS_REVISION`이라도 concrete finding이 없으면 reviewer quality warning으로 기록하고 deterministic gate를 따른다.
5. model_quality가 `weak` 또는 `failed`이면 `approved`로 승격하지 않고 deterministic evidence 수준에 맞춰 `approved_with_warnings` 또는 `insufficient_evidence`를 선택한다.

### 6.7 Repair

`RepairPlanner`는 blocker/high finding을 다음 agent turn에 넣을 수 있는 repair prompt로 바꾼다.

Repair prompt는 다음을 포함해야 한다.

1. objective
2. exact blocking findings
3. changed paths
4. required fix
5. required verification
6. non-goals
7. warning: do not broaden scope unless required by finding
8. patch construction rule: pre-write gate는 부분 수리를 승인하지 않으므로 필수 RF 전체를 해결해야 하지만, 구현 모델은 RF별로 현재 파일에서 확인한 snippet에 고정된 좁은 hunk를 작성해야 한다. 서로 다른 함수/위치는 같은 proposal 안에서도 hunk를 분리하고, 함수 종료부/중괄호를 새 위치에 중복 삽입하는 대형 rewrite를 금지한다.

자동 repair loop guard:

```text
RepairLoopState
  review_id
  round
  max_rounds
  finding_fingerprints
  resolved_fingerprints
  repeated_fingerprints
  no_progress_count
  last_repair_summary
```

기본 설정:

```text
review.auto_repair_max_rounds = 2
review.repeated_finding_block_threshold = 2
```

loop guard 규칙:

1. 자동 repair는 blocker/high finding에만 연결한다.
2. 같은 finding fingerprint가 `review.repeated_finding_block_threshold` 이상 반복되면 자동 repair를 중단하고 `blocked`로 둔다.
3. 새 review에서 changed paths나 finding set이 전혀 개선되지 않으면 `no_progress_count`를 올린다.
4. max rounds를 넘으면 사용자에게 현재 blocker와 수동 선택지를 보여준다.
5. repair prompt는 finding scope를 벗어난 리팩터링, unrelated cleanup, dependency upgrade를 금지한다.
6. repair 이후 verification evidence가 없으면 gate는 approved로 올라갈 수 없다.
7. 사용자가 명시적으로 계속 진행을 요청하면 새 review run을 만들고 이전 loop state를 audit trail로 연결한다.
8. pre-write block 이후 수리 턴은 리뷰 artifact 재조회나 shell 기반 파일 출력으로 시간을 쓰지 않는다. 필요한 파일/diff 상태는 `read_file`, `grep`, `git_diff`, `git_status` 같은 전용 workspace 도구로만 확인하고 바로 더 좁은 edit proposal을 만든다.

### 6.8 Automatic Review Trigger

Kernforge는 사용자가 별도로 리뷰를 요청하지 않아도, 구현/수정 작업 뒤 자동으로 적절한 review flow를 실행할 수 있어야 한다. 예를 들어 사용자가 "xx 기능을 구현해"라고만 말해도, 변경이 발생하면 설정에 따라 공통 리뷰 하네스가 실행된다.

기본 설정:

```text
review.auto_after_change = true
review.auto_after_goal_iteration = true
review.auto_before_git_write = true
review.auto_follow_up = safe
```

자동 review trigger:

1. explicit edit intent 이후 patch transaction changed paths가 생겼다.
2. `/goal` iteration에서 implementation 또는 repair pass가 workspace를 바꿨다.
3. `/new-feature` implement/verify/close flow에서 변경이 생겼다.
4. MCP client가 code-changing workflow를 호출했고 서버 설정상 auto review가 켜져 있다.
5. git commit/push/PR write 요청 전 latest review gate가 없거나 stale이다.
6. final answer 직전 coding harness가 changed paths, unresolved verification, or blocker를 감지했다.

자동 review는 항상 `ReviewRequestAnalyzer`를 거친다. 사용자가 "구현해"만 말했어도 acceptance contract, changed paths, project domain hints, verification state를 보고 `change_review`, `security_review`, `refactor`, `evidence_review` 같은 적절한 flow를 선택한다.

자동 실행 제한:

1. read-only analysis, explanation, design-only 요청은 workspace change가 없으면 자동 review를 실행하지 않는다.
2. 같은 patch transaction fingerprint에 대해서는 review를 반복하지 않는다.
3. review 이후 새 변경이 생기면 fingerprint가 바뀌므로 다시 실행할 수 있다.
4. tiny docs-only change는 deterministic review만으로 충분할 수 있다.
5. security-sensitive change는 model reviewer가 configured 되어 있으면 model review까지 기본 실행한다.
6. provider failure로 model review가 실패하면 deterministic gate와 warning을 남기고, review 자체를 성공으로 위장하지 않는다.
7. 사용자가 `/review --no-model`, `/review --no-follow-up`, session setting, or explicit no-review flag를 사용하면 해당 범위에서 자동 review를 낮추거나 끈다.

자동 review 결과 처리:

1. `approved` 또는 `approved_with_warnings`이면 final answer는 review summary와 남은 risk를 짧게 포함한다.
2. `needs_revision`이면 repair prompt를 다음 agent turn에 주입한다.
3. `blocked` 또는 `insufficient_evidence`이면 verification, evidence, or user-visible blocker를 남기고 completion claim을 막는다.
4. 자동 review가 생성한 `next_commands`는 7.2의 safety policy에 따라 safe follow-up만 자동 실행 후보가 된다.
5. 자동 review 결과는 `.kernforge/reviews/latest.json/md`, session event, completion audit, edit loop ledger에 연결한다.

## 7. 명령과 통합

### 7.1 새 명령 계층

권장 top-level entrypoint는 `/review`다. 이 하네스는 code change 전용이 아니라 plan, selection, PR, goal iteration, final answer, analysis report를 모두 다룬다. 따라서 change는 명령 이름이 아니라 subtarget이어야 한다.

명령 문법은 최대한 간결하게 유지한다.

```text
/review [target|request] [options]
```

기본 target:

```text
change
plan
selection
pr
final
goal
analysis
```

자주 쓰는 예:

```text
/review
/review plan
/review pr
/review models
/review 이 변경 안정성 관점에서 봐줘
/review 이 설계에서 빠진 위험을 봐줘
/review --no-model
/review --mode security-hardening
```

긴 하위 명령을 많이 만들지 않는다. 예를 들어 `/review-change`, `/review-plan`, `/review-pr-status`, `/review-security-hardening`처럼 기능별 top-level 또는 deep subcommand를 늘리지 않는다. Target은 짧은 명사 하나로 두고, 세부 동작은 option과 review gate의 `next_commands`가 담당한다.

`models`는 review target이 아니라 설정 surface다. `/review models`는 primary/cross route, 적용 lens, deprecated role 설정의 호환 fallback 상태를 짧게 보여준다.

`/review`는 `.kernforge/reviews/latest.md`와 `.kernforge/reviews/latest.json`을 쓴다.

`/review`는 첫 번째 인자가 알려진 target이면 target으로 처리하고, 그렇지 않으면 자연어 review request로 처리한다. 즉 사용자가 review type을 몰라도 요청 내용을 분석해서 flow를 선택한다.

`/review` target 추론 순서:

1. active selection이 있으면 `selection`.
2. dirty git/checkpoint/patch transaction change가 있으면 `change`.
3. active goal iteration 또는 active goal audit context가 있으면 `goal`.
4. current branch에 PR context가 있고 GitHub 조회가 가능한 경우 `pr`.
5. 최신 analysis/root-cause/fuzz report가 명시적으로 reviewable 상태이면 `analysis`.
6. reviewable target이 없으면 아무 모델도 호출하지 않고 대상이 없다고 안내한다.

자연어 요청이 있으면 위 순서를 그대로 쓰지 않고 `ReviewRequestAnalyzer`가 request text와 workspace state를 함께 보고 target, mode, policy pack, selected flow를 고른다. 예를 들어 "오탐 위험 위주로 봐줘"는 일반 change review가 아니라 security/false-positive lens를 추가해야 한다.

쓰기 동작은 계속 명시적이어야 한다.

```text
/review pr --draft-comments
/review pr --post-comments
/review pr --resolve-thread <thread-id>
/review pr --create-issue
```

`--post-comments`, `--resolve-thread`, `--create-issue`는 기존처럼 explicit-only write-side action으로 유지한다.

### 7.2 Next Command 추천과 자동 실행

리뷰 결과는 단순 finding list로 끝나지 않고, 사용자가 바로 이어서 할 수 있는 추천 명령을 구조화해서 제공해야 한다. CLI 사용자와 MCP client가 같은 결과를 소비할 수 있도록 `GateDecision.NextCommands` 또는 별도 `ReviewNextCommand` 목록을 둔다.

```text
ReviewNextCommand
  id
  command
  reason
  safety
  when
  auto_run
  requires_confirmation
  client_hint
```

`safety` 값:

1. `read_only`: dashboard, status, audit, verify plan 조회처럼 workspace를 바꾸지 않는다.
2. `safe_local`: `/verify`, `/completion-audit`, `/recover execute-safe`처럼 정해진 local safe dispatcher를 통과한다.
3. `write_local`: 파일 수정, artifact 생성, comment draft 생성처럼 workspace에 쓰기 흔적을 남긴다.
4. `external_write`: PR comment 게시, issue 생성, review thread resolve, push처럼 외부 상태를 바꾼다.

자동 실행 정책:

1. `/review`는 review gate가 끝난 뒤 `read_only`와 일부 `safe_local` 추천만 자동 실행 후보로 삼는다.
2. 자동 실행은 `auto_run=true`이고 `requires_confirmation=false`인 항목만 가능하다.
3. `write_local`과 `external_write`는 자동 실행하지 않는다. 명시 옵션 또는 별도 사용자 확인이 필요하다.
4. 자동 실행한 추천 명령은 `ReviewRun`에 `next_command_results`로 기록한다.
5. 자동 실행이 실패하면 review gate를 덮어쓰지 않고 follow-up warning으로 남긴다.
6. 사용자가 자동 실행을 원하지 않으면 `/review --no-follow-up`으로 끌 수 있다.
7. 사용자가 적극적인 흐름을 원하면 `/review --follow-up` 또는 session setting으로 safe follow-up 자동 실행을 켤 수 있다.

추천 예:

1. verification evidence가 없고 code change가 있으면 `/verify`.
2. review gate가 `needs_revision`이면 repair prompt 또는 `/continuity continue from review`.
3. gate가 `approved_with_warnings`이면 `/completion-audit`.
4. PR target에서 draft가 필요하면 `/review pr --draft-comments`.
5. security policy evidence가 부족하면 `/simulate <risk-profile>` 또는 `/analyze-project --mode security`를 추천한다.

MCP client 처리:

1. MCP `review` response는 `next_commands`와 `recommended_command`를 반환한다.
2. MCP 서버는 기본적으로 추천 명령을 실행하지 않는다.
3. MCP request가 `auto_follow_up=safe`를 명시하면 `read_only`와 `safe_local`만 실행할 수 있다.
4. MCP client가 직접 이어서 실행할 수 있도록 각 command에는 `client_hint`와 `requires_confirmation`을 포함한다.
5. MCP에서도 `external_write`는 실행하지 않고 CLI explicit command로 안내한다.

### 7.3 Legacy 명령 제거와 마이그레이션

공통 하네스가 들어간 뒤에는 legacy review 명령을 실행 alias로 남기지 않는다. 이유는 명령이 여러 개 남아 있으면 사용자는 어떤 review path가 최신 gate를 쓰는지 알기 어렵고, 내부 구현도 다시 흩어진다.

제거/통합 대상:

```text
/do-plan-review      -> /review plan
/review-pr           -> /review pr
/review-selection    -> /review selection
/review-selections   -> /review selection --all
MCP review           -> ReviewRun target=change, selection, plan, pr, analysis 중 하나
MCP review_code      -> 제거하거나 MCP review로 이름 변경
final-answer reviewer -> ReviewRun target=final
```

마이그레이션 정책:

1. 새 하네스 구현 시점부터 문서와 help는 `/review ...`만 노출한다.
2. legacy slash command entries는 같은 변경에서 제거한다.
3. 사용자가 예전 명령을 입력하면 실행 alias로 넘기지 말고 새 명령을 알려주는 unknown-command guidance만 제공한다.
4. automation slot, suggestion candidate, feature handoff, README/guide 예시는 같은 변경에서 `/review ...`로 갱신한다.
5. session history에 남은 과거 command string은 migration helper가 표시용으로만 해석한다.

### 7.4 기존 기능 연결

1. `/review plan`
   - 기존 planner/reviewer loop의 좋은 부분은 재사용하되, 최종 review log를 `ReviewRun{target=plan}`으로 저장한다.
   - 새 plan review는 plan 문자열 승인만 보지 않고 objective, non-goals, architecture risk, required verification을 typed finding/gate로 남긴다.

2. `/goal`
   - `buildGoalReviewPrompt` 직접 생성 대신 `ReviewHarness`의 evidence pack과 reviewer prompt builder를 사용한다.
   - iteration review와 semantic review는 각각 `target=goal_iteration`, `target=final_answer`로 저장한다.

3. interactive final-answer reviewer
   - 현재 prompt는 유지하되, `ReviewRun{target=final_answer}` artifact를 남긴다.

4. `/completion-audit`
   - latest review gate를 checklist source로 읽는다.
   - blocker finding이 있으면 audit blocker로 승격한다.

5. MCP `review`
   - provided diff/code를 `ChangeSet{source=provided_diff/provided_code}`로 넣고 같은 reviewer/gate를 사용한다.
   - CLI `/review`와 동일한 `ReviewRun` engine을 호출한다.
   - 기존 MCP `review_code`는 공식 surface로 유지하지 않고 `review`로 이름을 맞춘다.

6. `/review pr`
   - current PR metadata와 checks를 evidence로 넣고, finding을 comment draft와 issue draft에 재사용한다.

7. hooks
   - push/PR hook은 latest review gate가 `blocked` 또는 `needs_revision`이면 warning/block policy에 반영한다.

## 8. MCP Review Surface

MCP 서버에서도 같은 review 하네스를 제공할 수 있도록, CLI 명령과 MCP tool은 같은 내부 API를 호출해야 한다.

원칙:

1. MCP tool 이름은 `review`를 기본으로 한다.
2. CLI `/review`와 MCP `review`는 같은 `ReviewRun` schema, collector, policy pack, gate evaluator, artifact writer를 사용한다.
3. MCP는 read-only 기본값이다.
4. MCP 호출자는 diff/code/path/context를 직접 제공할 수 있고, 제공하지 않으면 workspace git/checkpoint state를 수집한다.
5. MCP 응답은 사람이 읽는 summary와 machine-readable finding/gate를 함께 반환한다.
6. MCP write-side action은 제공하지 않는다. PR comment 게시, issue 생성, thread resolve는 CLI `/review pr --post-comments|--create-issue|--resolve-thread` 같은 explicit local command로만 유지한다.

권장 MCP input:

```text
ReviewMCPRequest
  request
  target
  mode
  flow
  auto_review
  paths
  diff
  code
  include_git_diff
  include_file_contents
  policy_packs
  no_model
  auto_follow_up
  max_context_chars
  response_format
```

`target` 값:

1. `auto`
2. `change`
3. `plan`
4. `selection`
5. `pr`
6. `analysis`

`response_format` 값:

1. `summary`
2. `json`
3. `both`

권장 MCP output:

```text
ReviewMCPResponse
  summary
  review_id
  machine_status
  status_code
  retryable
  request_analysis
  artifact_refs
  result
  model_plan
  reviewer_runs
  freshness
  redaction
  gate
  waivers
  findings
  changed_paths
  warnings
  next_commands
  recommended_command
  follow_up_results
```

MCP에서 plan review를 제공할 때는 `target=plan`과 `code` 또는 `request` field를 사용한다. MCP에서 code review를 제공할 때는 `diff`, `code`, `paths`, `include_git_diff` 중 하나를 사용한다. MCP client가 어떤 리뷰가 필요한지 모르면 `target=auto` 또는 target 생략으로 보내고, `request`에 사용자의 자연어 요구를 넣는다. 서버는 `ReviewRequestAnalysis`를 반환해 어떤 flow를 선택했는지 설명한다.

MCP client가 별도 구현 도구나 workflow를 통해 workspace를 변경하는 경우, 서버 설정의 `review.auto_after_change=true`가 적용된다. MCP request의 `auto_review` 값은 `inherit`, `on`, `off` 중 하나로 두고, 생략 시 서버 설정을 따른다.

MCP response의 `model_plan`은 현재 실행이 단일 모델, 복수 모델, deterministic-only, degraded 중 무엇인지 알려주고, `required_lenses`/`optional_lenses`와 `reviewer_runs`는 실제 main/cross route의 kind, model, status, quality를 알려준다. MCP client는 `model_plan`, `reviewer_runs`, `next_commands`를 보고 사용자에게 "더 강한 독립 검토를 원하면 `/review models cross`를 설정하라"처럼 안내할 수 있다. MCP 서버는 클라이언트 대신 모델 설정을 변경하지 않는다.

MCP status 처리:

1. `machine_status`와 `status_code`는 5.10의 CLI/MCP status contract를 따른다.
2. `freshness.stale=true`이면 MCP client는 결과를 latest approval로 표시하지 않는다.
3. `redaction.status`가 warning 또는 failed이면 client는 raw excerpt를 재요청하지 않고 필요한 path/evidence ref만 보여준다.
4. `retryable=true`이면 client는 `recommended_command` 또는 `next_commands` 중 safe 항목을 사용자에게 제안할 수 있다.
5. `waivers`는 적용된 waiver와 만료 상태를 보여주는 읽기 전용 정보다. MCP client가 waiver를 생성하지 않는다.

MCP 보안 경계:

1. MCP `review`는 파일을 수정하지 않는다.
2. MCP `review`는 git stage/commit/push/PR write를 수행하지 않는다.
3. MCP `review`는 provider/model 호출 전 context budget과 path boundary를 검증한다.
4. MCP `review`는 binary, release artifact, `.git`, `.kernforge` 내부 noisy artifact를 기본 제외한다.
5. MCP `review`가 만든 artifact path는 workspace 내부 `.kernforge/reviews` 아래로 제한한다.

기존 MCP `review_code` 처리:

1. 새 구조에서는 공식 tool을 `review`로 등록한다.
2. 기존 `review_code` tool은 제거 대상이다.
3. 필요하면 한 release 동안 tool discovery description에서 `use review instead` guidance만 제공하고, 같은 engine을 호출하는 장기 alias로 남기지 않는다.
4. MCP client examples, README, `MCP-SKILLS.md`, Codex App usage 문서는 `review` 기준으로 갱신한다.

## 9. 저장 경로

```text
.kernforge/reviews/
  latest.json
  latest.md
  <review-id>/
    review.json
    review.md
    evidence.md
    prompt_<role>.md
    raw_<role>.md
```

저장 원칙:

1. JSON은 automation, hook, dashboard가 읽는 source of truth.
2. Markdown은 사람이 보는 보고서.
3. JSON에는 `schema_version`, `review_fingerprint`, `freshness`, `redaction`, `machine_status`를 항상 남긴다.
4. raw model output은 재파싱과 reviewer 품질 분석을 위해 보존하되, redaction 이후에만 저장한다.
5. prompt는 provider/model drift를 추적할 수 있도록 보존하되, sensitive excerpt가 있으면 redacted prompt 또는 omission marker를 저장한다.
6. diff excerpt는 size budget과 redaction rule을 지켜 저장하고, 전체 diff는 필요 시 git/checkpoint에서 재생성한다.
7. `.kernforge/reviews/latest.json`은 최신 run pointer일 뿐이며, hook은 freshness check를 다시 수행한다.
8. unreadable, stale, or redaction-failed artifact도 감사 목적으로 남길 수 있지만 approval 근거로 쓰지 않는다.

## 10. Windows Kernel / Anti-Cheat Policy Pack

`windows_kernel_driver` pack은 아래 항목을 기본 질문으로 강제한다.

1. IRQL 제약을 위반하지 않는가?
2. pageable code/data 접근 위치가 안전한가?
3. pool allocation/free, reference counting, object lifetime이 대칭적인가?
4. user buffer, IOCTL input/output, METHOD_* 처리에서 trust boundary가 지켜지는가?
5. pointer probing/copy, length validation, integer overflow 방어가 충분한가?
6. process/thread/image/registry callback의 unregister and teardown race가 없는가?
7. OS build/version 차이를 guard하거나 degrade하는가?
8. HVCI, CI, PatchGuard, signing 정책과 충돌하지 않는가?
9. telemetry event schema와 backward compatibility가 유지되는가?
10. false positive를 낮추는 provenance, confidence, suppression path가 있는가?
11. attacker/bypass 관점에서 blind spot이 생기지 않는가?
12. hot path 성능 비용이 bounded 되어 있는가?
13. failure/recovery/unload/cancellation path가 안전한가?
14. test or smoke evidence가 실제 risk에 맞는가?

`anti_cheat_telemetry` pack은 추가로 아래를 본다.

1. 탐지 signal이 spoofing, replay, delayed reporting에 취약하지 않은가?
2. local-only signal과 server-verifiable signal이 구분되는가?
3. ban-worthy evidence와 investigation evidence가 혼동되지 않는가?
4. false positive가 발생했을 때 operator가 판독할 context가 충분한가?
5. version mismatch guard와 schema drift guard가 있는가?

## 11. Reviewer 품질 게이트

리뷰어도 검증 대상이다.

Reviewer output quality issue:

1. verdict가 없다.
2. finding이 목표와 무관하다.
3. path/symbol/evidence가 없다.
4. required_fix가 없다.
5. diff에 없는 코드를 근거로 삼는다.
6. deterministic blocker를 무시하고 approved 한다.
7. security-sensitive change를 일반 style review처럼 처리한다.
8. structured output contract를 지키지 않아 normalizer가 fallback parsing을 사용했다.
9. blocker/high finding이 complete quality 기준을 만족하지 못한다.

처리:

1. model reviewer quality issue는 `category=evidence_gap` finding으로 기록한다.
2. provider failure와 reviewer quality failure를 분리한다.
3. quality failure가 반복되면 stronger reviewer profile 또는 smaller evidence pack으로 retry한다.
4. weak model result는 final summary에서 숨기지 않고 degraded review로 표시한다.

## 12. 구현 배치

권장 파일:

```text
cmd/kernforge/review_harness.go
cmd/kernforge/review_harness_router.go
cmd/kernforge/review_harness_collect.go
cmd/kernforge/review_harness_models.go
cmd/kernforge/review_harness_merge.go
cmd/kernforge/review_harness_policy.go
cmd/kernforge/review_harness_prompt.go
cmd/kernforge/review_harness_gate.go
cmd/kernforge/review_harness_freshness.go
cmd/kernforge/review_harness_redaction.go
cmd/kernforge/review_harness_waiver.go
cmd/kernforge/review_harness_status.go
cmd/kernforge/review_harness_render.go
cmd/kernforge/review_harness_command.go
cmd/kernforge/review_harness_test.go
cmd/kernforge/review_harness_router_test.go
cmd/kernforge/review_harness_models_test.go
cmd/kernforge/review_harness_merge_test.go
cmd/kernforge/review_harness_policy_test.go
cmd/kernforge/review_harness_gate_test.go
cmd/kernforge/review_harness_freshness_test.go
cmd/kernforge/review_harness_redaction_test.go
cmd/kernforge/review_harness_status_test.go
```

기존 파일과의 관계:

1. `coding_harness.go`
   - deterministic reviewer provider로 편입한다.

2. `completion_audit.go`
   - latest review gate를 audit checklist에 추가한다.

3. `goals.go`, `goals_runtime.go`
   - goal review prompt/evidence 생성 중복을 review harness로 이동한다.
   - iteration change fingerprint 기준 자동 review를 실행한다.

4. `mcp_review.go`
   - context collector는 review harness collector로 교체한다.

5. `suggestion_execution.go`
   - `/review pr` report/comment/issue draft는 review findings를 재사용한다.

6. `verify_classifier.go`
   - domain policy pack 선택 신호로 재사용한다.

7. `agent.go`, `edit_loop_agent.go`
   - post-change automatic review trigger와 repair prompt injection을 연결한다.

8. `config.go`
   - `review.auto_after_change`, `review.auto_after_goal_iteration`, `review.auto_before_git_write`, `review.auto_follow_up` 기본값을 정의한다.
   - `review.auto_repair_max_rounds`, `review.repeated_finding_block_threshold` 기본값을 정의한다.
   - cross review route 저장 방식과 기존 `ReviewProfiles`/`PlanReviewConfig` 제거 정책을 정의한다.

## 13. 단계별 구현 계획

### Phase 1: Typed ReviewRun MVP

1. `ReviewRun`, `ReviewRequestAnalysis`, `ReviewModelPlan`, `ChangeSet`, `ReviewEvidencePack`, `ReviewFinding`, `GateDecision` 타입 추가.
2. `schema_version`, `ReviewFreshness`, `ReviewRedactionReport`, `ReviewStatusContract` 타입 추가.
3. request text와 workspace state 기반 `ReviewRequestAnalyzer` 추가.
4. automatic review config와 post-change trigger 추가.
5. `ReviewModelPlanner`와 missing-role guidance 추가.
6. `ReviewResult`와 finding quality normalization 추가.
7. git/checkpoint/patch transaction 기반 `ChangeSet` 수집.
8. deterministic findings로 acceptance, verification, changed files, coding harness 상태 연결.
9. redaction을 거친 `.kernforge/reviews/latest.json/md` 저장.
10. `/review --no-model` 추가.

완료 기준:

1. 변경 파일이 있는 상태에서 `/review --no-model`이 review artifact를 만든다.
2. `/review "이 변경 안정성 관점에서 봐줘"`가 target/mode/flow를 자동 선택한다.
3. "xx 기능을 구현해" 같은 explicit edit request 뒤 변경이 있으면 기본 설정으로 자동 review가 실행된다.
4. 같은 patch fingerprint에 대해 자동 review가 반복 실행되지 않는다.
5. security-sensitive change에서 cross route가 없으면 single-model residual-risk guidance와 `/review models cross` 추천 명령이 남는다.
6. weak or failed model reviewer에서도 deterministic findings와 degraded result가 남는다.
7. verification failure가 있으면 gate가 approved 되지 않는다.
8. docs-only change와 code change의 test gap severity가 다르게 나온다.
9. schema version이 없는 artifact는 approval gate로 쓰이지 않는다.
10. stale latest review가 있으면 pre-git-write gate가 재리뷰를 요구한다.
11. secret-looking evidence가 있으면 prompt/raw artifact 저장 전에 redaction report가 남는다.
12. CLI exit code와 MCP machine status가 verdict와 일치한다.

### Phase 2: Model Reviewer Adapter

1. reviewer role prompt builder 추가.
2. `/review models` 설정 UX 추가.
3. model output parser와 finding normalizer 추가.
4. structured `REVIEW_RESULT` output contract 추가.
5. `APPROVED` / `NEEDS_REVISION` legacy verdict를 typed gate로 변환.
6. multi-model merge policy와 `ReviewMergeResult` 추가.
7. reviewer quality gate 추가.

완료 기준:

1. model reviewer가 낸 blocker finding이 repair prompt로 변환된다.
2. 모델이 approved해도 deterministic blocker가 gate를 막는다.
3. raw prompt/output이 redaction 이후 review artifact에 남는다.
4. 복수 reviewer가 같은 finding을 내면 deduplicate되고 severity conflict가 기록된다.
5. final gate reviewer가 deterministic blocker를 삭제할 수 없다.

### Phase 3: 기존 기능 통합

1. `/review plan` 결과를 `ReviewRun{target=plan}`으로 저장.
2. `/goal` independent review를 `ReviewHarness`로 교체.
3. final-answer reviewer 결과를 `ReviewRun{target=final_answer}`로 저장.
4. `/completion-audit`에 latest review gate section 추가.
5. MCP `review`를 공통 collector/gate로 연결하고 기존 `review_code` tool을 제거한다.
6. MCP response에 `machine_status`, `status_code`, `freshness`, `redaction`, `waivers`를 포함한다.
7. legacy review slash commands는 제거하고, unknown-command guidance와 문서 예시를 `/review ...` 기준으로 갱신한다.
8. blocker waiver command와 audit trail을 연결한다.

완료 기준:

1. goal loop, MCP review, manual review가 같은 JSON schema를 쓴다.
2. completion audit이 latest review blocker를 표시한다.
3. `/do-plan-review`, `/review-pr`, `/review-selection` 의존 테스트가 `/review plan|pr|selection` 기준으로 갱신된다.
4. MCP `review`는 파일, git, PR 상태를 쓰지 않고 read-only로 동작한다.
5. waiver가 적용된 blocker는 report에서 사라지지 않고 warning gate와 audit trail에 남는다.

### Phase 4: Domain Policy Packs

1. Windows kernel/driver pack 추가.
2. anti-cheat telemetry pack 추가.
3. memory scan pack 추가.
4. Unreal integrity pack 추가.
5. policy pack별 required evidence와 prompt rubric 추가.

완료 기준:

1. driver/IOCTL 변경에서 kernel pack이 자동 선택된다.
2. telemetry 변경에서 schema/backward compatibility finding이 가능하다.
3. memory scan 변경에서 false positive/bypass surface 질문이 강제된다.

### Phase 5: PR/Hook/Dashboard 확장

1. `/review pr --draft-comments`가 typed findings를 comment draft로 사용한다.
2. hook policy가 latest review gate를 읽는다.
3. session dashboard에 latest review gate를 노출한다.
4. suggestion system이 stale/blocked review를 next action으로 추천한다.
5. pre-git-write hook이 freshness, schema version, waiver validity를 재검사한다.

완료 기준:

1. blocked review가 push/PR warning에 반영된다.
2. dashboard에서 review status, blocker count, changed paths, next command를 볼 수 있다.
3. stale approved review는 push/PR approval 근거로 사용되지 않는다.

## 14. 추천 MVP 범위

가장 먼저 만들 것은 멀티 에이전트가 아니다.

추천 MVP:

1. `/review --no-model`
2. typed `ReviewRun`
3. common change/evidence collector
4. deterministic gate
5. schema/freshness/status contract
6. redacted markdown/json artifact
7. `/completion-audit` integration

그 다음 model reviewer를 붙이면 된다.

이 순서가 좋은 이유:

1. 모델 품질과 무관하게 gate의 뼈대가 안정된다.
2. 기존 coding harness와 verification evidence를 바로 재사용한다.
3. plan-review, goal review, MCP review를 한 번에 갈아엎지 않아도 된다.
4. reviewer model을 바꿔도 artifact schema와 gate freshness 판단은 유지된다.

## 15. 결론

Kernforge의 공통 리뷰 하네스는 "리뷰어 모델 하나 더 호출"이 아니다.

핵심은 아래 네 가지다.

1. 무엇을 리뷰하는지 명확히 고정한다.
2. 어떤 evidence를 보고 판단했는지 남긴다.
3. finding을 machine-readable하게 정규화한다.
4. 최종 gate가 모델 승인보다 강하게 동작한다.

이 구조가 들어가면 plan-review, code change review, autonomous goal review, PR review, MCP review, security hardening review가 같은 기반 위에서 돌아간다. 그 위에 mode별 reviewer graph를 얹으면 Kernforge는 단순한 coding agent가 아니라, 프로젝트 상태에 따라 리뷰 조직과 gate를 바꾸는 개발 오케스트레이터가 된다.

## 16. Codex App 수준 안정성을 위한 후속 개선 원칙

Kernforge의 리뷰 하네스가 Codex App처럼 안정적으로 느껴지려면, 리뷰를 "모델에게 맡기는 기능"이 아니라 런타임이 강제하는 운영 프로토콜로 끌어올려야 한다.

현재 문제의 핵심은 모델 성능만이 아니다. provider마다 출력 형식, tool-call 안정성, 말줄임 습관, 긴 tool loop에서의 집중력, patch syntax 준수율이 다르다. 따라서 Kernforge는 강한 모델을 연결하는 것만으로 충분하지 않고, 모델이 흔들려도 리뷰 품질과 편집 안정성이 유지되는 하네스 계층을 더 두껍게 가져가야 한다.

### 16.1 모델을 덜 믿고 런타임이 더 많이 강제한다

Codex App은 리뷰, diff, patch, conflict, tool result, final answer gate가 내부 프로토콜로 단단하게 연결되어 있다. 모델이 실수해도 런타임이 많은 부분을 보정한다.

Kernforge도 같은 방향으로 가야 한다.

1. 리뷰 target, mode, flow, evidence source는 모델 추론 결과가 아니라 `ReviewRequestAnalysis`와 deterministic collector가 확정한다.
2. reviewer output은 자유 텍스트가 아니라 `ReviewFinding` schema로 정규화한다.
3. model verdict는 gate의 입력일 뿐이며, deterministic blocker를 override할 수 없다.
4. final answer, git write, MCP response는 latest valid review gate와 freshness를 확인한 뒤에만 "완료" 근거로 사용할 수 있다.
5. 모델이 schema를 어기면 바로 사용자에게 실패를 던지기보다, 제한된 retry prompt, fallback parser, degraded artifact를 남긴다.

권장 구현:

```text
natural language request
  -> deterministic intent/scope analysis
  -> evidence collector
  -> reviewer model with fixed schema
  -> schema validator / normalizer
  -> deterministic gate
  -> repair or approve flow
  -> edit proposal
  -> pre-write review
```

### 16.2 Provider별 behavior adapter를 둔다

Kernforge는 OpenAI Codex subscription/CLI/API, Anthropic Claude CLI/API, DeepSeek, OpenRouter, OpenCode, Ollama, LM Studio, vLLM, llama.cpp 같은 provider를 직접 상대한다. 이 편차를 공통 prompt 하나로 숨기기는 어렵다.

따라서 provider별 adapter가 필요하다.

1. `openai-codex-subscription`
   - structured review와 patch proposal에 우선 사용한다.
   - reviewer role model로 설정된 경우 progress line에서 main model과 분명히 구분해 보여준다.

2. `openai-codex-cli`
   - 말줄임이나 압축된 finding을 낼 가능성을 고려해 narrative field 길이와 finding 수를 더 엄격히 제한한다.
   - review-only 요청에는 긴 report보다 짧은 structured result를 우선한다.

3. `anthropic-claude-cli`
   - local CLI bridge 특성을 고려해 짧은 reviewer prompt, 엄격한 JSON recovery, 명확한 no-ellipsis rule을 적용한다.
   - CLI 인증/환경 문제는 review 품질 문제가 아니라 provider setup guidance로 분리한다.

4. `DeepSeek`
   - 긴 tool loop에서 빈 응답이나 지연이 발생할 수 있으므로 scoped request, read budget, recovery guidance를 더 강하게 적용한다.
   - broad bug-fix 요청에서는 execution-plan preflight보다 deterministic scope discovery와 bounded reads를 우선한다.

5. local/open-compatible provider
   - context length, max token, tool-call 품질이 크게 다르므로 prompt budget과 retry 횟수를 보수적으로 잡는다.
   - schema violation이 반복되면 model reviewer quality issue로 degraded 처리한다.

Adapter는 model name 문자열 치환이 아니라 아래 정책을 포함해야 한다.

```text
provider
  display_label
  default_review_effort
  max_review_tokens
  omission_retry_budget
  schema_strictness
  tool_call_recovery_policy
  patch_format_risk
  recommended_roles
  unavailable_capability_notes
```

### 16.3 Patch는 raw text 의존을 줄이고 구조화한다

현재 Kernforge는 모델이 직접 `apply_patch` 문자열을 작성한다. 이 방식은 단순하지만, 모델이 아래 실수를 자주 한다.

1. `*** Update File` 뒤에 hunk를 넣지 않는다.
2. `@@` hunk 앞에 prose를 넣는다.
3. unified diff의 빈 context line을 prefix 없이 쓴다.
4. `Index:`나 `Replace lines ...` 같은 다른 patch dialect를 섞는다.
5. 이미 실패한 patch를 거의 같은 형태로 반복한다.

단기 개선:

1. 복구 가능한 patch format은 parser가 받아들인다.
   - hunk 내부 bare blank line은 blank context line으로 처리한다.
   - path separator, CRLF, surrounding whitespace는 보정한다.
2. 복구 불가능한 patch는 구체적인 실패 원인과 올바른 최소 예시를 모델에게 되돌린다.
3. 같은 patch signature가 반복되면 `apply_patch` 대신 fresh `read_file` 후 새 patch를 요구한다.
4. pre-write review는 patch가 syntactically valid하고 preview가 만들어진 뒤 실행한다.

중기 개선:

```text
model output
  -> structured edit proposal
       file
       operation
       anchor_before
       replace_range or exact_search
       replacement
       rationale
  -> runtime converts to patch
  -> preview
  -> pre-write review
  -> write
```

모델에게 raw patch를 계속 쓰게 하더라도, 내부적으로는 `EditProposal` schema를 병행해 점진적으로 전환하는 것이 좋다.

### 16.4 Broad bug-fix는 deterministic scope discovery를 먼저 수행한다

사용자가 `SampleWorker 서비스 설치/시작 과정에 버그를 찾고 수정해`처럼 넓은 요청을 하면, 모델이 곧바로 전체 파일을 길게 읽고 스스로 방향을 정하기 쉽다. 이 경우 시간이 오래 걸리고, 리뷰 하네스가 언제 개입해야 하는지 흐려진다.

권장 흐름:

```text
broad bug-fix request
  -> intent = bug_search_and_fix
  -> scope discovery
       search terms
       candidate files
       symbol/function candidates
       risk mode inference
  -> pre-fix review on bounded candidate scope
  -> repair guidance injection
  -> implementation tool loop
  -> pre-write review
  -> verification
  -> post-change review
```

scope discovery는 모델에게 맡기기 전에 최소한의 deterministic 탐색을 수행해야 한다.

1. request text에서 domain keywords를 뽑는다.
2. `rg`/grep pattern 후보를 만든다.
3. candidate file을 ranking한다.
4. 서비스/SCM, driver, IOCTL, registry, process creation, telemetry, detection 같은 domain signal로 review mode를 고른다.
5. candidate scope가 너무 넓으면 모델 reviewer를 부르기 전에 evidence gap과 추천 narrowing command를 남긴다.

예:

```text
request: "SampleWorker 서비스 설치/시작 과정에 버그를 찾고 수정해"
signals:
  - service install/start
  - CreateService/StartService/OpenSCManager
  - privileged service control boundary
target: change or candidate_scope
mode: security_hardening
roles:
  - security
  - false-positive only if detection/telemetry signal exists
```

### 16.5 Reviewer output은 검증 가능한 구조로만 승격한다

리뷰어가 `...`, `truncated`, 불완전한 문장, path 없는 high finding, evidence 없는 required fix를 반환하면 그 자체를 코드 finding으로 쓰면 안 된다.

정책:

1. finding field 안의 말줄임은 actionable finding으로 승격하지 않는다.
2. high/blocker finding은 path 또는 symbol, evidence, impact, required_fix를 모두 가져야 한다.
3. evidence가 부족하면 code finding이 아니라 `evidence_gap` finding으로 둔다.
4. retry 후에도 불완전하면 "weak reviewer output"으로 기록하고, repair loop를 막는다.
5. report에는 약한 finding을 숨기지 않되, 사용자가 무엇을 할 수 있는지 분명히 쓴다.

출력 품질 단계:

```text
complete
  code/path/symbol/evidence/impact/fix/test가 충분함
partial
  일부 필드는 약하지만 수리 방향은 명확함
weak
  말줄임, 생략, 일반론, evidence 부족
invalid
  schema 파손, target 무관, hallucination
```

gate는 `weak` finding만으로 코드를 수정하게 만들지 않는다.

### 16.6 "다음 명령"은 명령 나열이 아니라 실행 가능한 action contract여야 한다

현재 `다음 명령`이 단순히 `/verify --full`, `/completion-audit`, `/review models cross`처럼 보이면 사용자는 무엇을 왜 해야 하는지 알기 어렵다.

각 next command는 아래 정보를 가져야 한다.

```text
command
reason
when
safety
auto_run
requires_confirmation
client_hint
expected_result
```

CLI 출력 예:

```text
다음 단계:
1. /continuity continue from review
   - 왜: 차단 finding이 있어 최신 리뷰 finding을 기준으로 repair 흐름을 이어가야 합니다.
   - 언제: 지금 바로.
   - 안전성: safe_local.
   - 자동 실행: 사용자가 "수정해줘"라고 답하면 이 명령과 같은 흐름으로 이어갑니다.

2. /verify --full
   - 왜: 변경된 파일에 대한 최신 빌드/테스트 근거가 없습니다.
   - 언제: 코드 수정 후 완료 선언 전.
   - 안전성: safe_local.
```

MCP response에서는 사람이 읽는 문장뿐 아니라 machine-readable action list도 같이 반환해야 한다.

### 16.7 자동 리뷰는 세 위치에서 서로 다른 목적을 가진다

자동 리뷰는 하나가 아니다. 같은 `/review` 기반이어도 개입 위치마다 목적이 다르다.

1. `pre_fix`
   - 코드 수정 전에 candidate scope와 bug hypothesis를 검토한다.
   - 목적은 "무엇을 고칠지"를 안정화하는 것이다.
   - broad bug-fix와 selection bug-fix에 유용하다.

2. `pre_write`
   - 모델이 제안한 patch preview를 실제 파일에 쓰기 전에 검토한다.
   - 목적은 "이 patch가 리뷰 finding과 맞는지, 새로운 위험을 만들지 않는지"를 막는 것이다.

3. `post_change`
   - 실제 변경 후 diff와 verification evidence를 검토한다.
   - 목적은 "완료 가능한 상태인지"를 판단하는 것이다.

4. `pre_final_answer`
   - 사용자가 볼 최종 답변이 실제 상태와 맞는지 검토한다.
   - 목적은 "하지 않은 검증을 했다고 말하지 않는지, blocker를 숨기지 않는지"를 막는 것이다.

각 단계는 다른 prompt, evidence, gate policy를 가져야 한다.

### 16.8 Domain-sensitive role routing을 좁고 정확하게 한다

보안 민감 신호가 있다고 항상 모든 specialist를 호출하면 느리고 시끄럽다. role routing은 "보수적이되 불필요하게 넓지 않게" 설계해야 한다.

기본 규칙:

1. Windows service, SCM, process creation, registry persistence
   - `security` reviewer.
   - `false-positive` reviewer는 기본 호출하지 않는다.

2. kernel driver, IOCTL, IRQL, pool, signing, verifier
   - `security` reviewer.
   - 필요하면 `regression` 또는 `test` reviewer.

3. anti-cheat detection, telemetry, memory scan, spoof/evasion signal
   - `security` reviewer.
   - `false-positive` reviewer.

4. refactor across shared APIs
   - `primary` + `regression`.

5. UI polish
   - `design` + optional `regression`.

6. final answer / goal completion
   - `final` gate reviewer.

이 구분이 있어야 서비스 설치 버그 수정에서 false-positive reviewer가 끼어들지 않고, 반대로 탐지 로직 변경에서는 오탐 리뷰가 빠지지 않는다.

### 16.9 Codex App과의 차이를 줄이기 위한 마일스톤

단기:

1. provider 표시명 통일.
2. patch parser 복구성 강화.
3. omission marker 검출과 retry 강화.
4. broad bug-fix에서 slow plan preflight 제거.
5. service/SCM/driver/domain signal 기반 mode inference 강화.
6. next command 설명을 이유/시점/안전성/자동 실행 가능성 중심으로 출력.

중기:

1. deterministic scope discovery 단계 추가.
2. `EditProposal` schema 도입.
3. provider behavior adapter 추가.
4. reviewer output quality score를 gate에 더 강하게 반영.
5. MCP review response에 action contract와 latest review freshness 포함.

장기:

1. raw patch tool-call 의존 축소.
2. review run, patch transaction, verification ledger, final-answer review를 하나의 runtime protocol로 묶기.
3. primary/cross reviewer route를 provider capability와 비용/지연 예산에 따라 자동 최적화.
4. dashboard와 hook에서 review freshness, blocker, waiver, next action을 first-class 상태로 표시.

목표는 "Kernforge가 Codex App처럼 보이게 만들기"가 아니다. 목표는 모델이 어떤 provider이든 Kernforge 런타임이 review evidence, finding schema, patch safety, gate freshness를 끝까지 책임지는 것이다.

#### 16.9.1 세부 구현 계획

이 계획은 새 review 시스템을 별도로 만드는 것이 아니라, 현재 common review harness 위에 Codex App에 가까운 런타임 강제력을 단계적으로 얹는 방식으로 진행한다. 기준 anchor는 다음과 같다.

1. `ReviewRun`, `ReviewFinding`, `GateDecision`, `ReviewNextCommand`, `ReviewFreshness`는 `cmd/kernforge/review_harness.go`의 기존 schema를 확장한다.
2. target/mode/evidence 확정은 `cmd/kernforge/review_harness_collect.go`의 `analyzeReviewRequest`, `inferReviewMode`, `collectReviewEvidence` 경로를 기준으로 한다.
3. model reviewer 결과 정규화와 gate 판단은 `cmd/kernforge/review_harness_gate.go`의 `parseModelReviewFindingsForLanguage`, `classifyReviewFindingQuality`, `evaluateReviewGate`를 기준으로 한다.
4. provider별 reviewer 실행 정책은 `cmd/kernforge/review_harness_models.go`의 `planReviewModels`, `executeReviewModelRuns`, `reviewModelDisplayLabel`에서 확장한다.
5. patch 안정성은 `cmd/kernforge/patchtool.go`의 `parsePatchDocument`와 agent tool-error recovery 경로에서 확장한다.
6. MCP 응답 계약은 `cmd/kernforge/mcp_review.go`와 `cmd/kernforge/review_harness_command.go`의 `renderReviewMCPResponse`를 기준으로 한다.

Phase 1: 단기 hardening

1. Provider 표시명과 provider behavior policy를 분리한다.
   - `review_provider_behavior.go`를 추가한다.
   - `ReviewProviderBehavior`는 `provider`, `display_label`, `default_review_effort`, `max_review_tokens`, `retry_review_tokens`, `omission_retry_budget`, `schema_strictness`, `tool_call_recovery_policy`, `patch_format_risk`, `recommended_roles`, `unavailable_capability_notes`를 가진다.
   - `providerUserLabel`, `formatProviderModelEffortLabel`, `reviewModelDisplayLabel`, review progress line, `ReviewModelPlan.AssignedModels`가 같은 display source를 사용하게 한다.
   - `openai-codex-subscription`, `openai-codex-cli`, `anthropic-claude-cli`, `deepseek`, `openrouter`, `opencode`, `ollama`, `lmstudio`, `vllm`, `llama.cpp`의 기본 policy를 먼저 둔다.

2. Patch parser 복구성과 실패 feedback을 강화한다.
   - code fence로 감싼 patch, `*** Begin Patch` 앞뒤 prose, CRLF/CR/path separator, surrounding whitespace를 deterministic하게 정리한다.
   - hunk 내부 bare blank line은 계속 blank context line으로 처리한다.
   - 복구 불가능한 경우 `ErrInvalidPatchFormat`에 reason code와 최소 올바른 예시를 붙인다.
   - 실패한 patch의 normalized signature를 session/tool error context에 남긴다.
   - 같은 patch signature가 반복되면 같은 patch 재시도가 아니라 fresh `read_file` 또는 `list_files` 후 새 patch를 만들라는 recovery prompt를 넣는다.

3. Omission marker와 weak reviewer output gate를 강화한다.
   - provider behavior의 `omission_retry_budget`에 따라 retry 횟수를 정한다.
   - `...`, Unicode ellipsis, `truncated`, `omitted`, `omitted for brevity`, `content omitted`, `details omitted`, `output omitted`은 structured finding 필드 안에서 금지한다.
   - high/blocker finding은 `path` 또는 `symbol`, `evidence`, `impact`, `required_fix`가 있어야 code finding으로 승격한다.
   - 필드가 부족한 high/blocker finding은 `evidence_gap` 또는 `operational_risk` warning으로 강등하고 `BlocksGate=false`로 둔다.
   - weak finding만 있는 경우 repair loop를 시작하지 않고, report에는 "weak reviewer output"과 재리뷰 방법을 남긴다.

4. Broad bug-fix에서 slow plan preflight 대신 deterministic scope discovery를 붙인다.
   - 기존 `shouldSkipInteractivePlanPreflight`의 broad bug-fix skip 정책은 유지한다.
   - `review_scope_discovery.go`를 추가해 request text에서 domain keyword, rg pattern 후보, candidate file, symbol/function 후보, risk mode를 산출한다.
   - discovery 결과는 `ReviewRequestAnalysis`에 `DomainSignals`, `ScopeDiscovery`, `RiskSignals`로 저장한다.
   - candidate scope가 너무 넓으면 reviewer 호출 전에 `evidence_gap` finding과 추천 narrowing command를 남긴다.

5. Service/SCM/driver/domain signal 기반 mode inference를 구조화한다.
   - `CreateService`, `StartService`, `OpenSCManager`, service install/start/stop, registry persistence, process creation은 `security_hardening`으로 분류하되 false-positive reviewer는 기본 호출하지 않는다.
   - `.sys`, IOCTL, IRQL, pool, signing, verifier, INF, SCM driver service는 `security` lens와 필요 시 `test` lens를 적용한다.
   - anti-cheat detection, telemetry, memory scan, spoof/evasion, false-positive signal은 `security` lens와 `false_positive` lens를 같이 적용한다.

6. Next command를 action contract로 완성한다.
   - `ReviewNextCommand`에 `ExpectedResult`를 추가한다.
   - CLI rendering은 `이유`, `시점`, `안전성`, `자동 실행`, `확인 필요`, `예상 결과`를 표시한다.
   - MCP response는 `next_commands`와 `recommended_command` 모두 machine-readable action contract를 그대로 반환한다.

Phase 2: 중기 contract화

1. Deterministic scope discovery를 review run의 first-class evidence로 만든다.
   - `ReviewScopeDiscovery` schema를 추가한다.
   - `candidate_files`, `candidate_symbols`, `search_terms`, `domain_signals`, `scope_width`, `confidence`, `narrowing_commands`를 기록한다.
   - broad bug-fix pre-fix review는 이 discovery 결과를 evidence로 사용한다.

2. `EditProposal` schema를 도입한다.
   - 필드는 `file`, `operation`, `anchor_before`, `replace_range`, `exact_search`, `replacement`, `rationale`, `risk`, `expected_preview`로 시작한다.
   - 단기에는 raw patch와 병행 저장하고, runtime은 proposal을 patch preview로 변환만 한다.
   - pre-write review는 syntactically valid patch와 generated preview가 만들어진 뒤 실행한다.

3. Provider behavior adapter를 reviewer 실행에 연결한다.
   - `reviewRoleMaxTokensForRun`, `reviewRoleRetryMaxTokensForRun`, `reviewRoleReasoningEffortForRun`이 provider behavior를 참조하게 한다.
   - local/open-compatible provider는 token과 retry를 보수적으로 잡고, schema violation 반복 시 degraded 처리한다.
   - Codex subscription/provider는 structured review와 patch proposal에 우선 배치한다.

4. Reviewer output quality score를 gate에 강하게 반영한다.
   - finding별 `Quality`와 run-level `ModelQuality`를 gate blocker/warning 계산에 반영한다.
   - `weak` 또는 `invalid` model output은 deterministic blocker를 override하지 못한다.
   - repeated weak reviewer output은 `review_failed` 또는 `approved_with_warnings`가 아니라 `insufficient_evidence`에 가깝게 처리한다.

5. MCP review response에 latest review freshness를 재계산해 포함한다.
   - 저장된 `run.Freshness`뿐 아니라 현재 branch, changed path, review fingerprint를 다시 비교한 `latest_review_freshness`를 반환한다.
   - MCP client는 final answer나 write-side action 전에 이 값을 확인할 수 있어야 한다.

Phase 3: 장기 runtime protocol화

1. Raw patch tool-call 의존을 줄인다.
   - model은 `EditProposal`을 만들고, runtime이 patch로 변환한다.
   - raw patch tool은 fallback 또는 expert mode로 남긴다.

2. Review run, patch transaction, verification ledger, final-answer review를 하나의 runtime protocol로 묶는다.
   - `ReviewTransaction` 또는 `RuntimeGateLedger`를 추가한다.
   - ledger는 latest valid review, patch preview/write, verification, completion audit, final answer review를 연결한다.
   - final answer, git write, MCP write-side response는 ledger가 stale이 아니고 blocker가 없을 때만 완료 근거로 삼는다.

3. Primary/cross reviewer route를 자동 최적화한다.
   - domain signal, provider capability, 비용, 지연 예산을 기준으로 reviewer graph를 고른다.
   - service/SCM은 security 중심, detection/telemetry는 security + false-positive, refactor는 primary + regression, final answer는 final-gate 중심으로 제한한다.

4. Dashboard와 hook에서 review freshness, blocker, waiver, next action을 first-class 상태로 표시한다.
   - completion audit, evidence dashboard, hook output이 latest review freshness와 next action을 직접 보여준다.
   - stale review가 있으면 "리뷰가 있음"이 아니라 "현재 변경에 대해 무효"로 표시한다.

권장 구현 순서:

1. Provider 표시명/behavior policy와 next command action contract를 먼저 구현한다.
2. Patch parser recovery와 repeated failed patch signature guard를 구현한다.
3. Omission/weak finding quality gate를 강화한다.
4. Deterministic scope discovery와 domain signal inference를 붙인다.
5. MCP freshness 재계산과 action contract 반환을 완성한다.
6. `EditProposal` schema와 pre-write preview review를 도입한다.
7. 장기 ledger와 dashboard/hook integration으로 확장한다.

검증 기준:

1. Targeted unit test
   - `go test ./cmd/kernforge -run "TestReview|TestPatch|TestInteractive|TestMCP"`
2. Full package test
   - `go test ./cmd/kernforge`
3. Manual smoke
   - `/review`
   - `/review selection`
   - broad bug-fix 자연어 요청
   - malformed `apply_patch` retry
   - MCP `kernforge_review`
4. 문서 동기화
   - `README.md`
   - `README_kor.md`
   - `QUICKSTART.md`
   - `QUICKSTART_kor.md`
   - `FEATURE_USAGE_GUIDE.md`
   - `FEATURE_USAGE_GUIDE_kor.md`
   - `MCP-SKILLS.md`
   - `MCP_SERVER_MODE_kor.md`

진행상황:

1. Cycle 1 - provider 표시명/behavior policy와 next command action contract
   - 상태: 구현 및 review pass 완료.
   - 코드 변경:
     - `cmd/kernforge/review_provider_behavior.go`를 추가해 provider별 display label, review token cap, retry token cap, omission retry budget, schema strictness, tool recovery policy, patch format risk를 구조화했다.
     - `providerUserLabel`, review model display, review max token 계산이 provider behavior를 참조하도록 연결했다.
     - `ReviewNextCommand`에 `expected_result`를 추가했고, `auto_run`과 `requires_confirmation`은 false도 JSON에 남도록 action contract 필드로 고정했다.
     - CLI markdown/terminal rendering과 MCP `recommended_command`가 reason, when, safety, auto_run, requires_confirmation, client_hint, expected_result를 함께 반환하도록 확장했다.
   - review pass에서 발견한 버그:
     - `ReviewNextCommand`의 bool field가 `omitempty` 때문에 `false`일 때 MCP `next_commands`에서 빠질 수 있었다.
     - 수정: `auto_run`과 `requires_confirmation` JSON tag에서 `omitempty`를 제거하고 MCP response regression test를 추가했다.
   - 검증:
     - `go test ./cmd/kernforge -run "TestReview|TestPrintReviewRunExplainsNextCommands|TestReviewMarkdownKeepsLongFindingTextAndExplainsNextCommands"`
2. Cycle 2 - patch parser recovery와 repeated failed patch signature guard
   - 상태: 구현 및 review pass 완료.
   - 코드 변경:
     - `parsePatchDocument` 앞에 `normalizePatchDocumentText`를 두어 BOM, CRLF/CR, surrounding whitespace, `*** Begin Patch` 앞 prose, `*** End Patch` 뒤 code fence/prose를 deterministic하게 정리한다.
     - `normalizePatchDocumentPath`를 추가해 `*** Add File`, `*** Update File`, `*** Delete File`, `*** Move to` 경로의 quote/backtick과 backslash separator를 정리한다.
     - patch parser error에 `patch_format_missing_begin`, `patch_format_missing_end`, `patch_format_missing_hunk`, `patch_format_empty_update`, `patch_format_invalid_hunk_line` 같은 reason code를 붙였다.
     - agent apply_patch recovery는 invalid patch format을 최대 2회까지 처리하되, 같은 normalized patch signature가 반복되면 같은 patch 재시도 대신 target file fresh read 후 새 patch를 만들도록 안내한다.
   - review pass 결과:
     - code fence/prose recovery는 parser normalization에서만 처리되고, 실제 patch operation은 기존 preview/write gate를 그대로 통과한다.
     - repeated signature guard는 normalized patch text fingerprint를 사용하므로 prose/code fence만 다른 동일 실패 patch를 같은 실패로 본다.
     - 추가 수정이 필요한 버그는 발견하지 못했다.
   - 검증:
     - `go test ./cmd/kernforge -run "TestApplyPatch|TestInvalidPatchFormatGuidance|TestReview|TestPrintReviewRunExplainsNextCommands|TestReviewMarkdownKeepsLongFindingTextAndExplainsNextCommands"`
3. Cycle 3 - omission/weak finding quality gate 강화
   - 상태: 구현 및 review pass 완료.
   - 코드 변경:
     - provider behavior의 `omission_retry_budget`을 reviewer role provider에 따라 적용해 생략 표식이 남은 model output을 provider별 횟수만큼 strict retry한다.
     - role별 provider behavior가 review max token, retry max token, 기본 reasoning effort에도 반영되도록 `reviewRoleMaxTokensForRoleRun`, `reviewRoleRetryMaxTokensForRoleRun`, `reviewRoleProviderForRun`을 추가했다.
     - model finding이 `weak` 또는 `invalid`이면 high/blocker로 표시되어도 gate blocker로 승격하지 않는다.
     - high/blocker model finding은 `path` 또는 `symbol`, `evidence`, `impact`, `required_fix`가 모두 있어야 blocking finding으로 유지된다.
     - 필수 근거가 부족한 high/blocker model finding은 `evidence_gap` warning으로 강등하고 `BlocksGate=false`, `Quality=partial`, `Confidence=low`로 정규화한다.
   - review pass에서 발견한 버그:
     - omission retry 호출이 실패하면 원 응답을 보존하더라도 run-level degraded reason에 retry failure가 남지 않을 수 있었다.
     - 수정: retry 실패를 `omission retry failed: ...`로 `ReviewReviewerRun.Error`와 `ReviewResult.DegradedReason`에 기록하고 regression test를 추가했다.
   - 검증:
     - `go test ./cmd/kernforge -run "TestReviewModel|TestWeakModel|TestCompleteModel|TestSecurityHighFindingBlocksGate|TestReviewProviderBehavior|TestReviewMCPResponseIncludesActionContractBooleans"`
4. Cycle 4 - deterministic scope discovery와 domain signal inference
   - 상태: 구현 및 review pass 완료.
   - 코드 변경:
     - `cmd/kernforge/review_scope_discovery.go`를 추가해 request/path/git 변경 후보에서 candidate file, candidate symbol, search term, scope width, narrowing command를 산출한다.
     - `ReviewRequestAnalysis`에 `DomainSignals`, `RiskSignals`, `ScopeDiscovery`를 추가해 mode/policy/model prompt가 같은 deterministic 분석 결과를 공유하게 했다.
     - service/SCM signal은 `windows_service_control` domain과 `privileged_service_control` risk로 분류하고 `security_hardening` mode로 올리되, anti-cheat/detection signal이 없으면 false-positive reviewer를 요구하지 않는다.
     - broad/unknown scope는 deterministic `evidence_gap` finding과 `narrow-review` next command를 남겨 모델 finding을 완료 근거로 과신하지 않게 했다.
     - 수집된 실제 review evidence가 있을 때만 scope discovery summary를 evidence section에 붙여, scope metadata만으로 "reviewable evidence 있음"으로 오판하지 않게 했다.
   - review pass에서 발견한 버그:
     - non-git temp directory에서 `git diff` 도움말/usage 출력이 candidate file로 들어와 scope가 broad로 오판되고 test reviewer가 추가 호출될 수 있었다.
     - 수정: scope discovery가 git status를 먼저 검증하고, `--option`, whitespace 포함 token, usage-like token을 candidate path에서 제외하도록 필터링했다.
     - 최종 통합 검증 중 MCP `provided_diff`만 있는 review가 explicit path가 없다는 이유로 broad/live_fix로 오판되어 reviewer가 2회 호출될 수 있었다.
     - 수정: unified diff의 `diff --git`, `---`, `+++` path를 candidate file로 추출하고, provided diff/code가 있으면 path arg가 없어도 bounded evidence로 취급한다.
   - 검증:
     - `go test ./cmd/kernforge -run "TestReviewScope|TestBroadReviewScope|TestSecurityService|TestReviewModel|TestWeakModel|TestCompleteModel|TestReviewProviderBehavior|TestReviewMCPResponseIncludesActionContractBooleans|TestApplyPatch|TestInvalidPatchFormatGuidance|TestPrintReviewRunExplainsNextCommands|TestReviewMarkdownKeepsLongFindingTextAndExplainsNextCommands"`
5. Cycle 5 - MCP freshness 재계산과 action contract 반환 완성
   - 상태: 구현 및 review pass 완료.
   - 코드 변경:
     - `cmd/kernforge/review_freshness.go`를 추가해 MCP response 직전에 현재 branch와 현재 changed path를 기준으로 latest review freshness를 다시 계산한다.
     - MCP `kernforge_review` 응답에 기존 저장값 `freshness`와 별도로 `latest_review_freshness`를 추가했다.
     - `latest_review_freshness.InvalidatedBy`는 `branch`, `changed_paths`, `review_fingerprint` 같은 machine-readable stale 원인을 남긴다.
     - `latest_review_freshness.StaleReason`은 MCP client가 final answer나 write-side action 전에 바로 사용자에게 설명할 수 있는 문장으로 구성한다.
     - Cycle 1에서 추가한 `next_commands`/`recommended_command` action contract는 그대로 유지하고, freshness 정보와 함께 반환된다.
   - review pass에서 확인한 사항:
     - non-git workspace에서는 git usage/help 출력이 freshness changed path로 섞이지 않도록 scope discovery와 같은 git status usability guard를 재사용한다.
     - 새로 review를 실행한 직후에는 reviewed path와 current changed path가 일치하면 stale로 표시하지 않고, review 이후 추가된 변경 파일만 stale 원인으로 잡는다.
   - 검증:
     - `go test ./cmd/kernforge -run "TestReviewScope|TestBroadReviewScope|TestSecurityService|TestReviewModel|TestWeakModel|TestCompleteModel|TestReviewProviderBehavior|TestReviewMCPResponse|TestApplyPatch|TestInvalidPatchFormatGuidance|TestPrintReviewRunExplainsNextCommands|TestReviewMarkdownKeepsLongFindingTextAndExplainsNextCommands"`
6. Cycle 6 - `EditProposal` schema와 pre-write preview review 연결
   - 상태: 구현 및 review pass 완료.
   - 코드 변경:
     - `EditProposal` schema를 추가하고 `ReviewRun.EditProposals`와 `ReviewHarnessOptions.EditProposals`로 review artifact/MCP payload에 보존한다.
     - pre-write `EditPreview`를 `EditProposal`로 변환해 file, operation, rationale, risk, expected_preview, preview_fingerprint를 기록한다.
     - `collectReviewEvidence`가 `EditProposal` summary를 evidence section에 포함하므로 reviewer는 raw diff뿐 아니라 runtime이 생성한 preview contract도 함께 본다.
     - MCP response에 `edit_proposals`를 추가해 외부 client가 raw patch 문자열 대신 previewed edit intent와 fingerprint를 확인할 수 있게 했다.
   - review pass에서 발견한 버그:
     - multi-file preview에서 같은 diff body가 proposal마다 복제되면 review JSON과 evidence가 과도하게 커질 수 있었다.
     - 수정: 첫 proposal만 `expected_preview` 본문을 보존하고, 나머지는 같은 `preview_fingerprint`를 공유한다는 marker만 evidence에 남긴다.
   - 검증:
     - `go test ./cmd/kernforge -run "TestReviewScope|TestBroadReviewScope|TestSecurityService|TestReviewModel|TestWeakModel|TestCompleteModel|TestReviewProviderBehavior|TestReviewMCPResponse|TestAgentRunsPreWriteReviewBeforePreviewAndWrite|TestEditProposalsFromPreview|TestApplyPatch|TestInvalidPatchFormatGuidance|TestPrintReviewRunExplainsNextCommands|TestReviewMarkdownKeepsLongFindingTextAndExplainsNextCommands"`
7. Cycle 7 - `RuntimeGateLedger` / `ReviewTransaction` 통합
   - 상태: 구현 및 review pass 완료. (2026-05-10)
   - 코드 변경:
     - `RuntimeGateLedger`와 `ReviewTransaction`을 추가해 `review_run_id`, `patch_transaction_id`, `verification_report_id`, `completion_audit_id`, `final_answer_review_id`를 하나의 runtime gate artifact로 연결한다.
     - session, review run, completion audit artifact, MCP review response, final-answer reviewer prompt, agent system prompt에 runtime gate ledger를 노출한다.
     - final answer는 ledger blocker가 있으면 기본적으로 revision을 요구하되, 사용자가 알아야 할 blocker를 명시적으로 공개하는 blocked final answer는 허용한다.
     - explicit git write와 MCP PR write-side automation은 stale review 또는 unwaived blocker가 남아 있으면 runtime gate feedback으로 차단한다.
     - completion audit checklist에 `Runtime gate ledger is blocker-free` 항목을 추가해 completion audit과 review/verification/patch transaction 상태를 같은 장부로 묶는다.
     - `collectSessionReviewEvidence`가 patch transaction changed path를 evidence와 changeset에 포함해, 자동 post-change review 직후 ledger가 자기 자신을 stale로 오판하지 않게 했다.
   - review pass에서 발견한 버그:
     - non-git workspace에서 `delegationChangedFiles`의 git error/help 출력이 runtime gate changed path로 섞일 수 있었다.
     - 수정: runtime gate changed path 수집도 `reviewScopeGitStatusLooksUsable` guard를 통과한 git workspace에서만 git changed files를 사용한다.
     - 자동 post-change review에서 patch transaction path가 review changeset에 들어가지 않아 `main.go` 같은 방금 수정한 파일이 즉시 unreviewed path로 판정될 수 있었다.
     - 수정: session patch transaction changed path를 review evidence section과 `ReviewChangeSet.ChangedPaths`에 반영한다.
     - runtime freshness와 session patch transaction freshness가 같은 `unreviewed changed files` stale reason을 중복으로 붙일 수 있었다.
     - 수정: runtime gate review attach 단계에서 이미 기록된 stale reason은 재추가하지 않는다.
   - 검증:
     - `go test ./cmd/kernforge -run "TestRuntimeGate|TestCompletionAuditIncludesRuntimeGateLedger|TestCompletionAuditUsesVerificationHistoryForStandaloneAudit|TestCompletionAuditCommandPassesWhenArtifactsAndVerificationPass"`
     - `go test ./cmd/kernforge -run "TestReviewScope|TestBroadReviewScope|TestReviewMCPResponse|TestAgentRunsPreWriteReviewBeforePreviewAndWrite|TestEditProposalsFromPreview|TestReviewProviderBehavior|TestApplyPatch|TestInvalidPatchFormatGuidance|TestCompletionAudit"`
     - `go test ./cmd/kernforge -run "TestAgentVerificationFailurePromptsAnotherTurnBeforeFinalAnswer|TestAgentCanRepairAfterFailedVerificationAndReturnAfterPass|TestAgentPromptsToDisableAutoVerifyOnFirstMissingToolFailure|TestAgentNudgesForFinalAnswerAfterMultipleSuccessfulEditTurns|TestAgentBlocksFurtherEditToolLoopAfterPostEditNudge|TestAgentRetriesCodeSpanTruncationWhileStreaming|TestAgentRetriesWhenEditRequestHandsPatchBackWithoutUsingTools|TestAgentBlocksGitCommitWithoutExplicitUserRequest|TestAgentFinalAnswerReviewerRequestsRevisionBeforeReturn|TestAgentFinalAnswerReviewerPromptIncludesEditLoopLedger|TestPatchTransactionRecordsWriteFileAndFinalizes|TestAcceptanceContractDrivesMissingRequiredArtifactRepair|TestPreFinalHarnessBlocksVerificationClaimWithoutEvidence|TestFailureRepairPromptIsAddedAfterVerificationFailure|TestPreFinalHarnessStoresTestImpactReport|TestRuntimeGate" -count=1`
     - `go test ./cmd/kernforge -count=1 -timeout 10m`
     - review pass 후 재검증: `go test ./cmd/kernforge -run "TestRuntimeGate|TestReviewMCPResponseIncludesLatestFreshness|TestCompletionAuditIncludesRuntimeGateLedger|TestAgentFinalAnswerReviewerPromptIncludesEditLoopLedger" -count=1`
     - `git diff --check -- cmd/kernforge/runtime_gate_ledger.go cmd/kernforge/runtime_gate_ledger_test.go cmd/kernforge/review_harness_collect.go cmd/kernforge/agent.go cmd/kernforge/completion_audit.go`
8. Cycle 8 - Dashboard/hook/status 통합
   - 상태: 구현 및 review pass 완료. (2026-05-10)
   - 코드 변경:
     - `/status` 출력에 `Runtime Gate` 섹션을 추가해 runtime gate status, review freshness, changed path count, latest review id, patch/verification/completion audit id, blocker/warning/waiver count, stale reason, next command를 표시한다.
     - `/hooks` 출력에도 같은 runtime gate summary, review freshness, next command를 노출해 hook 상태 화면에서 review recovery path를 바로 볼 수 있게 했다.
     - session HTML dashboard snapshot에 `RuntimeGateLedger`를 추가하고, KPI card와 workspace signal card에 runtime gate 상태, review freshness, blocker/warning count, stale reason, next command를 렌더링한다.
     - session dashboard의 changed files 수집은 runtime gate와 같은 git status usability guard를 쓰는 `reviewCurrentChangedPaths`로 맞춰 non-git workspace의 git help/error text 오염을 피한다.
   - review pass에서 발견한 버그:
     - dashboard renderer가 외부/테스트 snapshot처럼 runtime gate ledger가 없는 zero-value 상태를 렌더하면 summary helper가 이를 `ready`로 정규화해 `unknown/ready`처럼 잘못 보일 수 있었다.
     - 수정: `runtimeGateStatusSummary`와 `runtimeGateReviewFreshnessLabel`이 empty ledger를 `unknown`으로 표시하도록 guard를 추가했다.
   - 검증:
     - `go test ./cmd/kernforge -run "TestRuntimeGate|TestRenderSessionDashboard|TestSessionDashboardHTMLCommand" -count=1`
     - review pass 후 재검증: `go test ./cmd/kernforge -run "TestRuntimeGate|TestRenderSessionDashboard|TestSessionDashboardHTMLCommand|TestStatusCommandFocusesOnRuntimeState" -count=1`
     - `git diff --check -- cmd/kernforge/runtime_gate_ledger.go cmd/kernforge/runtime_gate_ledger_test.go cmd/kernforge/main.go cmd/kernforge/commands_hooks.go cmd/kernforge/session_dashboard.go cmd/kernforge/session_dashboard_test.go`
9. Cycle 9 - `EditProposal` 런타임 프로토콜 확장
   - 상태: 구현 및 review pass 완료. (2026-05-10)
   - 코드 변경:
     - `apply_edit_proposal` tool을 추가해 structured edit proposal을 first-class edit path로 실행한다.
     - proposal은 `file`, `operation`, `exact_search`, `replacement`/`content`, `rationale`, `risk`, `owner_node_id`를 받아 exact-search replacement, add, write, delete를 runtime preview로 변환한다.
     - `EditPreview`에 `Proposals []EditProposal`을 추가하고, pre-write review가 preview에서 만든 proposal 또는 tool이 제공한 structured proposal을 그대로 review artifact에 보존하게 했다.
     - proposal 실행은 HookPreEdit -> automatic pre-write review -> user preview approval -> write permission -> actual write/delete -> HookPostEdit 순서를 따른다.
     - `apply_patch` 설명을 expert/debug fallback으로 낮추고, registry에는 `apply_edit_proposal`을 `apply_patch`보다 먼저 등록했다.
     - review evidence rendering이 `exact_search`와 `replacement`를 제한 길이로 포함해 reviewer가 raw diff뿐 아니라 proposal contract도 확인할 수 있게 했다.
   - review pass에서 발견한 버그:
     - 새 `apply_edit_proposal`은 tool meta에 `effect=edit`을 주지만 `isEditTool`/기본 effect inference의 명시 edit tool 목록에는 없었다.
     - 영향: “모든 tool call이 edit tool인가” 같은 agent orchestration 경로에서 기존 edit tool과 다르게 취급될 수 있었다.
     - 수정: `isEditTool`과 `inferToolExecutionEffect`에 `apply_edit_proposal`을 추가하고 regression assertion을 보강했다.
   - 검증:
     - `go test ./cmd/kernforge -run "TestApplyEditProposal|TestEditToolDescriptions|TestApplyPatchRequiresPreviewApprovalBeforeWriting|TestWriteFileReviewBlocksBeforePreviewAndWrite|TestAgentRunsPreWriteReviewBeforePreviewAndWrite|TestEditProposalsFromPreview|TestRuntimeGate" -count=1`
     - review pass 후 재검증: `go test ./cmd/kernforge -run "TestApplyEditProposal|TestEditToolDescriptions|TestToolRegistry|TestAgentRetries|TestInvalidPatchFormatGuidance|TestReviewMCPResponse|TestRuntimeGate" -count=1`
     - `git diff --check -- cmd/kernforge/edit_proposal.go cmd/kernforge/tools.go cmd/kernforge/verify.go cmd/kernforge/tools_edit_guard_test.go cmd/kernforge/review_harness_auto.go cmd/kernforge/main.go cmd/kernforge/patchtool.go`

10. Cycle 10 - 운영 문서 동기화
    - 상태: 구현 및 review pass 완료. (2026-05-10)
    - 문서 변경:
      - `README.md`, `README_kor.md`의 편집 워크플로우와 MCP section에 `apply_edit_proposal`, `edit_proposals`, `runtime_gate_ledger`, `latest_review_freshness`, `scope_discovery`, action-oriented `next_commands`, expected-result metadata를 반영했다.
      - `QUICKSTART.md`, `QUICKSTART_kor.md`에 `kernforge_review` 사용, `/status` runtime gate 해석, malformed/repeated invalid patch recovery, stale review recovery, `narrow-review` handoff를 추가했다.
      - `FEATURE_USAGE_GUIDE.md`, `FEATURE_USAGE_GUIDE_kor.md`에 `/status`/`/hooks` runtime gate summary, MCP review response field, provider behavior, omission retry budget, weak finding downgrade, proposal edit path를 반영했다.
      - `MCP-SKILLS.md`에 `kernforge_review`의 read-only deterministic/no-model 동작과 model-backed review provider 요구사항, runtime gate stop sign, provider behavior 기반 quality gate를 반영했다.
      - `MCP_SERVER_MODE_kor.md`에 tool list, provider/model 요구사항, `kernforge_status` runtime gate 해석, `kernforge_review` JSON 호출 예시, supplied diff/path 전달 방식, `apply_edit_proposal` 우선 경로를 추가했다.
    - review pass에서 발견한 문서 결함:
      - `README_kor.md`의 MCP 설명이 `latest_review_freshness`, `edit_proposals`, `runtime_gate_ledger`, `scope_discovery`, `expected_result`를 빠뜨려 English README와 어긋났다.
      - `MCP_SERVER_MODE_kor.md`와 quickstart/usage guide가 invalid patch recovery, provider behavior, omission retry budget, weak finding downgrade를 충분히 노출하지 않았다.
      - 수정: 위 필드를 한국어/영어 운영 문서 전반에 보강하고 `kernforge_review_code` 같은 옛 tool 이름이 남지 않는지 재검색했다.
    - 검증:
      - `rg -n "kernforge_review_code|review-code-only surface|legacy plan-review|RuntimeGateLedger|runtime gate ledger|latest_review_freshness|edit_proposals|scope_discovery|narrow-review|invalid patch|malformed|patch signature|omission retry|weak.*finding|expected-result|expected_result" README.md README_kor.md QUICKSTART.md QUICKSTART_kor.md FEATURE_USAGE_GUIDE.md FEATURE_USAGE_GUIDE_kor.md MCP-SKILLS.md MCP_SERVER_MODE_kor.md REVIEW_HARNESS_SPEC_kor.md`
      - `git diff --check -- README.md README_kor.md QUICKSTART.md QUICKSTART_kor.md FEATURE_USAGE_GUIDE.md FEATURE_USAGE_GUIDE_kor.md MCP-SKILLS.md MCP_SERVER_MODE_kor.md REVIEW_HARNESS_SPEC_kor.md`
      - 결과: 통과. LF/CRLF 변환 warning만 출력됨.

최종 통합 검증:

1. `git diff --check`
   - 결과: 통과. LF/CRLF 변환 warning만 출력됨.
2. `go test ./cmd/kernforge -count=1 -timeout 10m`
   - 결과: 통과. (Cycle 10 문서 반영 후 재실행, 2026-05-10)
3. `go test ./cmd/kernforge -run "TestApplyEditProposal|TestRuntimeGate|TestRenderSessionDashboard|TestStatusCommandFocusesOnRuntimeState|TestReviewMCPResponse|TestInvalidPatchFormatGuidance|TestReviewProviderBehavior" -count=1`
   - 결과: 통과.
4. `go vet ./cmd/kernforge`
   - 결과: 미통과.
   - 원인: 이번 변경 범위 밖의 `cmd/kernforge/version_windows.go:99`, `cmd/kernforge/version_windows.go:128` 기존 `unsafe.Pointer` 사용에 대한 vet warning.
   - 처리: review harness Cycle 7-10 변경 범위의 회귀는 아니므로 별도 항목으로 남긴다.

추가 review mode 검토:

1. Scope discovery 오염 수정
   - 발견: explicit path/selection review에서도 dirty git worktree의 다른 변경 파일을 candidate scope에 섞어 broad/bounded로 오판할 수 있었다.
   - 영향: focused review가 불필요하게 `narrow-review` warning을 내거나 reviewer role 선택이 흔들릴 수 있다.
   - 수정: 명시 path, mention path, provided diff path가 이미 있으면 git changed files를 자동 후보로 추가하지 않는다.
   - 회귀 테스트: `TestExplicitReviewScopeDoesNotAbsorbUnrelatedGitChanges`.
2. Provided diff changed-path 누락 수정
   - 발견: MCP/provided diff에서 diff path는 scope inference에만 쓰이고 `ReviewChangeSet.ChangedPaths`와 `Evidence.ChangedPaths`에는 기록되지 않았다.
   - 영향: 최신 freshness 재계산이 provided diff로 리뷰한 파일을 reviewed path로 보지 못해 stale 판단을 잘못할 수 있다.
   - 수정: unified diff의 `diff --git`, `---`, `+++` path를 changed path로도 반영한다.
   - 회귀 테스트: `TestReviewScopeDiscoveryUsesProvidedDiffPaths`.
3. 사용자 smoke - 서비스 설치/실행 리뷰가 dirty diff로 오염되는 문제 수정
   - 발견: `SampleWorker 서비스를 설치하고 실행하는 과정을 리뷰해줘` 같은 요청에서 실제 서비스 설치/실행 파일을 검색하지 않고 현재 git dirty diff와 build output path 중심으로 evidence를 구성했다.
   - 영향: 리뷰 모델이 `HandleScanner`, `NMI`, build output 같은 요청과 직접 관련 없는 변경을 근거로 finding을 만들고, 실제 `CreateService`/`StartService`/SCM 흐름은 확인하지 못했다.
   - 수정: 명시 path가 없더라도 request symbol, service-control keyword, domain search term을 기반으로 workspace source file을 먼저 검색하고, request-matched candidate file을 git dirty fallback보다 우선한다. 수집된 candidate file은 자동 file excerpt evidence로 포함하고 git diff도 그 scope로 필터링한다.
   - 추가 수정: file excerpt 수집 함수가 "남은 budget"을 받으면서 전체 evidence 길이와 비교해 파일 본문을 조기 생략할 수 있었다. 남은 budget을 독립적으로 차감하도록 고쳐 request-matched source excerpt가 실제 review evidence에 들어가게 했다.
   - 회귀 테스트: `TestReviewScopeDiscoverySearchesServiceFilesBeforeDirtyGit`.
4. 사용자 smoke - review finding title 잘림 수정
   - 발견: model prompt의 structured schema에 `title` field가 빠져 있었고, 모델이 title을 생략하면 `impact`/`evidence` 앞 100자를 hard truncate한 제목이 CLI에 표시됐다.
   - 영향: 한국어 제목이 `...서명 검증이`처럼 문장 중간에서 끊겨 사용자가 finding을 이해하기 어렵고, "내용이 잘렸다"는 UX가 발생했다.
   - 수정: review model system prompt와 user prompt에 `title` field를 필수로 추가했다. title이 그래도 없으면 긴 본문을 자르지 않고 `security finding in ServiceInstaller.cpp` 같은 안정적인 짧은 fallback 제목을 합성한다. 빈 finding은 fallback 제목으로 되살리지 않도록 basis field를 요구한다.
   - 회귀 테스트: `TestReviewModelPromptFollowsKoreanObjectiveLanguage`, `TestReviewFindingSynthesizesStableTitleWhenModelOmitsTitle`, `TestReviewModelParserKeepsUnstructuredNoBlockingSummary`.
5. 사용자 smoke 수정 후 검증
   - `go test ./cmd/kernforge -run "TestReviewScopeDiscoverySearchesServiceFilesBeforeDirtyGit|TestReviewScopeDiscoveryClassifiesServiceControlWithoutFalsePositive|TestReviewModelPromptFollowsKoreanObjectiveLanguage|TestReviewFindingSynthesizesStableTitleWhenModelOmitsTitle|TestReviewModelParserKeepsMultipleStructuredFindings" -count=1`
     - 결과: 통과.
   - `go test ./cmd/kernforge -run "TestReviewScope|TestBroadReviewScope|TestSecurityService|TestReviewModel|TestWeakModel|TestCompleteModel|TestReviewMCPResponse|TestRuntimeGate|TestApplyEditProposal|TestInvalidPatchFormatGuidance" -count=1`
     - 결과: 통과.
   - `go test ./cmd/kernforge -count=1 -timeout 10m`
     - 결과: 통과.
6. 내부 프로젝트명 하드코딩 제거
   - 발견: MCP guide 예시와 테스트 fixture에 특정 내부 프로젝트명이 남아 있어, request-based scope discovery의 회귀 테스트와 사용자-facing guide 예시가 특정 코드베이스에 묶여 보일 수 있었다.
   - 수정: `cmd/kernforge` Go 코드 전체에서 해당 내부 프로젝트명 문자열을 제거했다. production MCP guide 예시는 `src/driver/IoctlDispatch.cpp`, `src/crypto/CertParser.cpp`, `IOCTL_COMMAND_HEADER`, `CommandId::Max` 같은 일반 경로/심볼로 바꾸고, 회귀 fixture는 `SampleApp`/`SampleWorker`/`SampleKernel` 계열의 중립 샘플로 치환했다.
   - 확인: `rg -n -i "<internal-project-name>" --glob "*.go"` 결과가 0건이다.
   - 검증: `go test ./cmd/kernforge -count=1 -timeout 10m` 통과.
7. 사용자 smoke - 큰 파일의 `@path + symbol` 리뷰가 빈 evidence로 승인되는 문제 수정
   - 발견: `@Product/Master/RuntimeStatus.cpp GetFocusedStatus 관련 코드를 검토해줘`처럼 파일과 심볼을 함께 준 자연어 리뷰에서, 대상 파일이 256KB를 넘으면 file excerpt 수집이 조용히 건너뛰어질 수 있었다. 이 경우 모델은 실제 대상 코드를 보지 못한 채 비정형 approval을 반환하고, CLI는 `발견=1 blocker=0 warning=0`만 보여줘 리뷰가 정상 수행된 것처럼 보였다.
   - 수정: file evidence 수집이 요청 심볼을 기준으로 path-derived token을 제외한 뒤, 큰 소스 파일에서도 해당 심볼 주변 line window를 evidence로 포함한다. 파일이 너무 크거나 심볼을 찾지 못하면 warning으로 남기고, 큰 파일에서는 관련 없는 파일 앞부분으로 fallback하지 않아 silent approval을 막는다.
   - 추가 수정: blocker/warning이 없는 approved review라도 model info finding이 있으면 CLI `참고`/`Notes` 섹션에 표시해, `finding=1`이 있는데 본문이 사라지는 출력을 없앴다.
   - 회귀 테스트: `TestNaturalReviewIncludesSymbolExcerptFromLargeMentionedFile`, `TestNaturalReviewDoesNotApproveLargeFileWhenRequestedSymbolMissing`, `TestApprovedReviewRendersInfoFindingWhenNoWarnings`.
   - 검증: `go test ./cmd/kernforge -run "TestReviewScope|TestReviewModel|TestPrintReviewRun|TestLowSeverity|TestApprovedReview|TestNaturalReview|TestReviewMCPResponse" -count=1 -timeout 10m` 통과.
   - 사용자 smoke 재검증: 큰 소스 파일과 특정 함수명을 함께 지정한 자연어 리뷰에서, 모델이 대상 함수 주변 line evidence를 근거로 buffer capacity 검증 누락 blocker를 생성했고 CLI가 finding 본문과 `/continuity continue from review` next command를 온전히 표시했다. 이전의 빈 approval/잘림 문제는 재현되지 않았다.
8. 사용자 smoke - 자동 pre-write review warning이 progress에서 숨는 문제 수정
   - 발견: review-first repair 흐름에서 수정 전 리뷰는 blocker를 만들고 모델이 실제 수정을 제안했으며, 자동 쓰기 전 리뷰도 실행됐지만 결과가 `approved_with_warnings`인 경우 progress에는 `자동 쓰기 전 리뷰가 완료되었습니다.`만 출력됐다.
   - 영향: pre-write reviewer가 남긴 warning 2개를 사용자가 즉시 볼 수 없어, 실제로는 warning이 남은 edit이 warning-free처럼 보일 수 있다.
   - 수정: pre-write review gate가 `approved_with_warnings`이면 progress message에 warning 개수, 상위 warning finding 제목, report path를 함께 표시한다. gate는 기존 정책대로 blocker가 아니면 쓰기를 막지 않되, warning을 숨기지 않는다.
   - 회귀 테스트: `TestPreWriteReviewWarningProgressIncludesFindingTitles`.
   - 검증: `go test ./cmd/kernforge -run "TestPreWriteReviewWarningProgressIncludesFindingTitles|TestAgentRunsPreWriteReviewBeforePreviewAndWrite" -count=1` 통과.
9. 사용자 smoke - incomplete pre-write patch가 warning 상태로 통과할 수 있는 문제 수정
   - 발견: 정책 다운로드/조회 기능 구현 요청에서 첫 pre-write review가 header/member/getter 누락을 blocker로 막았지만, 두 번째 patch가 implementation file만 수정한 뒤 `approved_with_warnings`로 통과했다. warning 내용은 "멤버 선언과 초기값 변경 증거 없음", "조회 기능 구현 증거 없음"처럼 요청 범위 미충족을 가리켰다.
   - 영향: warning을 표시하더라도 write는 진행되므로, 컴파일되지 않거나 요청한 조회 API가 없는 불완전한 패치가 실제 파일에 적용될 수 있다.
   - 수정: pre-write review에 한해 모델이 낸 medium 이상 actionable warning을 blocking feedback으로 승격한다. `evidence_gap`, `design`, `regression` 등 패치 완성도 warning은 쓰기 전에 모델에게 되돌리고, deterministic scope warning이나 순수 verification warning은 기존처럼 표시만 한다.
   - 회귀 테스트: `TestPreWriteReviewBlocksActionableModelWarnings`, `TestPreWriteReviewDoesNotBlockPureVerificationWarning`.
   - 검증: `go test ./cmd/kernforge -run "TestPreWriteReviewBlocksActionableModelWarnings|TestPreWriteReviewDoesNotBlockPureVerificationWarning|TestPreWriteReviewWarningProgressIncludesFindingTitles|TestAgentRunsPreWriteReviewBeforePreviewAndWrite" -count=1` 통과.
10. 사용자 smoke - pre-write review 차단 뒤 shell write로 우회되는 문제 수정
   - 발견: 정책 다운로드/조회 기능 구현 요청에서 pre-write review가 incomplete edit을 차단한 뒤, 모델이 edit tool 대신 `run_shell`의 PowerShell inline script로 파일을 다시 쓰려고 했다. 기존 shell mutation classifier는 `Set-Content`가 명령 맨 앞에 있는 경우만 잡아서 `$content = ...; [System.IO.File]::WriteAllText(...)` 같은 inline rewrite를 workspace write로 분류하지 못했다.
   - 영향: review gate가 막은 patch가 shell approval만으로 파일에 적용될 수 있고, 이후 모델이 unrelated dirty files까지 성공 근거처럼 요약하는 drift가 발생할 수 있다.
   - 수정: `run_shell` 실행 전에 명령 전체에서 manual file-write primitive를 탐지한다. `Set-Content`, `Out-File`, `-OutFile`, redirection, `tee`, `Export-*`, 파일 이동/삭제/복사 계열, `.NET File.WriteAllText/WriteAllBytes/AppendAllText/Move/Delete/Copy/Replace` 계열을 workspace write로 분류한다. quoted search string은 오탐을 줄이기 위해 제거한 뒤 command segment 시작 위치의 write primitive를 검사하되, nested shell command는 raw command도 보수적으로 검사한다.
   - 추가 hardening: manual file-write primitive는 `allow_workspace_writes=true`와 `write_paths`가 있어도 실행하지 않는다. scoped shell write 예외는 `gofmt -w` 같은 formatter/codegen 계열에만 남겨 두고, 직접 source rewrite는 edit tool을 통과하게 한다.
   - 추가 hardening: workspace write로 분류된 `run_shell`이 실행 전에 차단되어 `changed_workspace=false`인 경우도 edit-loop 실패 evidence로 기록한다. 그래야 실패 뒤 모델이 완료를 주장해도 completion/final-answer gate가 남은 위험을 볼 수 있다.
   - 추가 hardening: pre-write actionable warning 판정에서 "검증/테스트 실행"이라는 단어만으로 implementation evidence gap을 순수 verification gap으로 오판하지 않게 했다. API surface, getter/accessor, 멤버 선언, 구현 증거, 조회 기능, 요청 범위 누락 키워드가 있으면 blocking warning으로 유지한다.
   - 추가 수정: pre-write blocker/actionable warning feedback에 `run_shell`, PowerShell file API, redirection, direct filesystem write로 pre-write review를 우회하지 말라는 implementation rule을 명시했다. system prompt의 shell rule도 `WriteAllText` 같은 .NET file API 금지를 포함하도록 보강했다.
   - 회귀 테스트: `TestRunShellRejectsInlinePowerShellFileWrites`, `TestRunShellRejectsManualFileWriteEvenWithScopedWritePaths`, `TestAssessShellCommandMutationClassifiesVerificationArtifactCommands`, `TestEditLoopRecordsBlockedWorkspaceShellWriteAsFailedApply`, `TestPreWriteReviewBlocksImplementationEvidenceGapEvenWhenVerificationMentioned`.
   - 검증: `go test ./cmd/kernforge -run "TestEditLoop|TestRunShell|TestAssessShellCommandMutation|TestPreWriteReview|TestAgentRunsPreWriteReviewBeforePreviewAndWrite|TestApplyEditProposalRequiresReviewBeforePreviewAndWrite" -count=1` 통과.
   - 전체 검증: `go test ./cmd/kernforge -count=1 -timeout 10m` 통과. `git diff --check`는 LF/CRLF 변환 warning만 출력하고 통과. 내부 프로젝트명 하드코딩 검사 `rg -n -i "<internal-project-name>" --glob "*.go"` 및 최신 review/MCP 문서 검사는 0건.
   - 잔여 정적 점검: `go vet ./cmd/kernforge`는 기존 `cmd/kernforge/version_windows.go:99`, `cmd/kernforge/version_windows.go:128`의 `unsafe.Pointer` warning으로 실패한다. 이번 review harness 변경 범위의 신규 회귀는 아니다.
11. 사용자 smoke - 수정 전 리뷰 warning이 사용자 진행 로그에 보이지 않는 문제 수정
   - 발견: `@Product/Worker/PathConverter.cpp:132-221 검토하고 버그를 수정해` 같은 review-first repair 요청에서 수정 전 리뷰가 `approved_with_warnings`로 끝나도 progress에는 `수정 전 리뷰가 완료되었습니다.`만 표시됐다. warning finding은 implementation model guidance에는 들어가지만, 사용자는 어떤 warning을 기준으로 수정이 시작됐는지 즉시 볼 수 없었다.
   - 영향: pre-write warning은 보이는데 pre-fix warning은 숨겨져, 같은 review pipeline 안에서 사용자 관측성이 일관되지 않았다. 실제로 모델이 warning을 근거로 수정하더라도 사용자는 "무슨 버그를 고친 것인지"를 로그만 보고 확인하기 어렵다.
   - 수정: 수정 전 리뷰 완료 progress도 gate verdict, blocker/warning 개수, 상위 finding 제목 또는 근거, report path를 함께 표시한다. 모델이 title을 생략해 `maintainability finding in file` 같은 generic fallback title만 생긴 경우에는 progress에서 evidence를 우선 표시한다.
   - 회귀 테스트: `TestReviewBeforeFixApprovedWithWarningsStopsBeforeImplementation`.
   - 검증: `go test ./cmd/kernforge -run "TestReviewBeforeFix|TestPreWriteReviewWarningProgressIncludesFindingTitles|TestRunShell|TestAssessShellCommandMutation|TestEditLoopRecordsBlockedWorkspaceShellWriteAsFailedApply" -count=1` 통과.
12. 사용자 smoke - pre-fix warning이 실제 수리 계약에서 누락될 수 있는 문제 수정
   - 발견: `@Product/Worker/PathConverter.cpp:132-221 검토하고 버그를 수정해` 같은 요청에서 수정 전 리뷰가 blocker 1개와 medium warning 1개를 찾았지만, implementation model은 blocker만 고치겠다고 진행할 수 있었다. 기존 pre-write review는 proposed diff와 edit proposal은 보지만, 직전 pre-fix review의 "반드시 처리해야 하는 finding 목록"을 별도 evidence로 받지 않았다.
   - 영향: 사용자는 progress에서 warning을 볼 수 있어도, pre-write reviewer가 "이 diff가 이전 warning까지 해결했는지"를 일관되게 대조하지 못한다. 결과적으로 blocker만 고친 patch가 warning-free처럼 승인될 수 있다.
   - 수정: pre-write review evidence에 직전 pre-fix review의 repair obligations를 추가한다. 대상은 blocking finding 전체와 medium 이상 actionable warning이며, `test_gap`/순수 `evidence_gap`/low warning은 기본 수리 의무에서 제외한다.
   - 추가 수정: pre-write review prompt에 "pre-fix repair findings가 있으면 blocker와 medium 이상 actionable warning이 모두 해결됐는지 확인하라"는 규칙을 추가했다. unresolved item이 있으면 원래 repair id를 명시한 `needs_revision` finding을 내도록 했다.
   - 추가 수정: implementation model에 주입되는 pre-fix feedback의 implementation rules를 "blocking finding과 medium 이상 actionable warning을 모두 고치거나, 경고를 일부러 남기는 이유를 명시하라"로 강화했다.
   - 회귀 테스트: `TestPreWriteRepairObligationsIncludeBlockingAndActionableWarnings`, `TestPreWriteEvidenceIncludesPreFixRepairObligations`.
   - 검증: `go test ./cmd/kernforge -run "TestPreWriteRepairObligations|TestPreWriteEvidenceIncludesPreFixRepairObligations|TestPreWriteReview|TestReviewBeforeFix|TestRunShell|TestAssessShellCommandMutation|TestEditLoopRecordsBlockedWorkspaceShellWriteAsFailedApply" -count=1` 통과.
13. 사용자 smoke - 코드 분석 요청이 `analysis_report`로 빠져 소스 없이 승인되는 문제 수정
   - 발견: `SampleServer 코드를 분석해서 서버 성능이나 히칭 영향을 검토해줘`처럼 명시적으로 코드/모듈을 분석하라는 요청이 `analysis_report` target으로 분류될 수 있었다. 이 경우 evidence가 latest analysis/git status 위주로 구성되어 실제 `SampleServer` 소스 파일이 있는데도 모델이 "코드가 제공되지 않았다"고 판단했다.
   - 영향: 사용자가 코드 기반 검토를 요청했는데 실제 소스 evidence 없이 generic finding이 생성된다. 특히 성능/히칭 분석처럼 함수 호출, lock, I/O, tick path를 봐야 하는 리뷰에서 검토 자체가 실패한다.
   - 수정: review request 분석 순서를 source-first로 바꿨다. deterministic scope discovery를 먼저 실행한 뒤 target을 추론하고, 코드/소스/함수/모듈/서버/성능/히칭 등 source intent와 candidate file/symbol이 있으면 `analysis_report`보다 `change` review를 우선한다.
   - 추가 수정: evidence 수집도 source-first로 바꿨다. focused/bounded source candidate가 있으면 file excerpt를 git status, git diff, latest analysis summary보다 먼저 붙이고, `analysis_report`는 명시적인 보고서 검토 요청이거나 보조 근거가 필요할 때 뒤에서 참고한다. 명시적으로 `analysis_report`를 선택해도 source path가 있으면 git 보조 근거는 같은 focus path로 제한한다.
   - 추가 수정: workspace source search가 512KB 초과 파일을 무조건 건너뛰지 않도록 했다. 큰 소스 파일은 내용 전체 검색은 생략하되, 경로가 요청 심볼/검색어와 맞으면 candidate로 유지해 후속 file evidence collector가 8MB 이하 범위에서 excerpt를 수집할 수 있게 했다.
   - 추가 수정: 파일명이 요청 심볼과 다르더라도 파일 내부에 클래스/함수 심볼이 있으면 후보를 찾도록 `rg --files-with-matches --fixed-strings` 기반 검색을 source discovery에 추가했다. `rg`가 없거나 timeout이 나도 8MB 이하 큰 파일은 제한적으로 직접 스캔한다.
   - 추가 수정: source symbol이 있는 요청에서 workspace search가 실패하면 unrelated dirty git directory로 fallback하지 않는다. 예를 들어 `MissingServer 코드 분석` 요청이 `kernforge/` 같은 도구 산출물 디렉터리를 narrowing path로 제안하지 않게 했다.
   - 추가 수정: file evidence collector가 8MB 초과 파일을 무조건 skip하지 않고, requested symbol이 있으면 streaming scan으로 symbol 주변 line window를 evidence에 포함한다.
   - 추가 수정: UE 프로젝트 규모를 고려해 discovery 한도를 크게 늘렸다. workspace source search는 reviewable file 기준 50000개까지 탐색하고, 결과 후보는 32개까지 유지하며, `rg` timeout은 5초로 늘렸다. `Content`, `Binaries`, `Intermediate`, `Saved`, `DerivedDataCache`는 source discovery에서 건너뛰어 asset 파일이 검색 한도를 소모하지 않게 했다.
   - 추가 수정: 512KB 초과 파일의 fallback symbol scan도 4096개까지 확대하고, 파일 전체를 메모리에 올리지 않는 chunk scan으로 바꿨다.
   - 회귀 테스트: `TestCodeAnalysisRequestPrefersSourceEvidenceBeforeAnalysisReport`, `TestSourceSymbolRequestDoesNotFallbackToUnrelatedGitDirectory`, `TestAnalysisReportRequestWithoutSourceIntentKeepsAnalysisTarget`, `TestAnalysisTargetWithSourceScopeCollectsSourceBeforeReport`, `TestNaturalReviewIncludesSymbolExcerptFromHugeMentionedFile`.
   - 검증: `go test ./cmd/kernforge -run "TestCodeAnalysisRequestPrefersSourceEvidenceBeforeAnalysisReport|TestSourceSymbolRequestDoesNotFallbackToUnrelatedGitDirectory|TestNaturalReviewIncludesSymbolExcerptFromHugeMentionedFile|TestNaturalReviewIncludesSymbolExcerptFromLargeMentionedFile|TestNaturalReviewDoesNotApproveLargeFileWhenRequestedSymbolMissing" -count=1` 통과.
   - 전체 검증: `go test ./cmd/kernforge -count=1 -timeout 10m` 통과.
   - 리뷰 모드 재검토: `go run ./cmd/kernforge -command "/review --path cmd/kernforge/review_harness_collect.go --path cmd/kernforge/review_scope_discovery.go --path cmd/kernforge/review_harness_test.go --no-model"` 실행. 결과는 blocker 0개, warning 1개(`Changed files have no latest verification evidence`)였다. 이후 `/verify --full`도 실행했지만 noninteractive 환경에서 계획된 검증 step이 confirmation unavailable로 skip되어 ledger warning은 남았다. 실제 수동 검증은 targeted test, broad review-harness test, 전체 `go test ./cmd/kernforge`, `git diff --check`로 통과했다.
14. 사용자 smoke - pre-fix bug hunt가 low-effort 승인 후 구현 모델이 뒤늦게 버그를 찾는 문제 수정
   - 발견: `@Product/Worker/PathConverter.cpp:132-221 검토하고 버그를 수정해` 같은 explicit path/range 수리 요청에서 수정 전 리뷰가 `approved`로 끝났지만, 이후 구현 모델이 같은 소스에서 `break`/`continue` 제어 흐름 버그를 발견했다.
   - 영향: pre-fix review progress가 "완료"로만 표시되어 사용자는 리뷰가 충분히 버그를 확인했다고 오해할 수 있다. 실제로는 구현 모델이 뒤늦게 독립 분석을 해야 했고, pre-fix review의 품질/게이트 신뢰도가 낮아졌다.
   - 수정: explicit source scope가 있는 pre-fix bug hunt는 기본 fallback reviewer도 `effort=high`로 실행하고, token budget을 기존 2048 cap에서 provider 한도 기준 최대 6000까지 확장한다. source evidence budget도 bug hunt에서는 30000자로 늘린다.
   - 추가 수정: pre-fix prompt에 "수정 전 버그 탐색 리뷰" 전용 규칙을 추가했다. supplied source를 correctness/stability/performance/boundary 관점에서 line-by-line으로 확인하고, 승인하더라도 코드가 bug-free로 증명됐다고 말하지 않게 했다.
   - 추가 수정: pre-fix bug hunt가 source evidence를 가진 상태에서 actionable finding 없이 `approved`를 반환하면 deterministic medium `evidence_gap` warning을 추가한다. 이 warning은 수리 의무로 승격하지 않지만 progress와 inline guidance에 "독립 소스 검토를 계속하라"는 신호를 남긴다.
   - 회귀 테스트: `TestReviewBeforeFixAddsReviewFeedbackBeforeImplementation`, `TestReviewBeforeFixApprovedBugHuntAddsNonConclusiveWarning`, `TestPreFixSecurityReviewUsesSingleFallbackRole`.
   - 검증: `go test ./cmd/kernforge -run "TestReviewBeforeFixAddsReviewFeedbackBeforeImplementation|TestReviewBeforeFixApprovedBugHuntAddsNonConclusiveWarning|TestReviewBeforeFixApprovedWithWarningsStopsBeforeImplementation|TestPreFixSecurityReviewUsesSingleFallbackRole" -count=1` 통과.
   - 추가 검증: `go test ./cmd/kernforge -run "TestReviewBeforeFix|TestPreFix|TestReviewScope|TestCodeAnalysisRequest|TestSourceSymbolRequest|TestNaturalReview|TestPreWriteReview|TestReviewMCPResponse" -count=1 -timeout 10m` 통과.
   - 전체 검증: 첫 `go test ./cmd/kernforge -count=1 -timeout 10m` 실행에서 변경 범위 밖 `TestHandleNewFeatureImplementUsesTrackedArtifacts`가 `Access is denied`로 1회 실패했다. 해당 테스트 단독 재실행은 통과했고, 전체 테스트 재실행도 통과했다.
   - 리뷰 모드 재검토: `go run ./cmd/kernforge -command "/review --path cmd/kernforge/review_harness.go --path cmd/kernforge/review_harness_pre_fix.go --path cmd/kernforge/review_harness_models.go --path cmd/kernforge/review_harness_natural_test.go --no-model"` 실행. 결과는 blocker 0개, warning 1개(`Changed files have no latest verification evidence`)였다.
15. 사용자 smoke - assistant 출력 중 모델 heartbeat가 겹치고 transcript recovery 문구를 모델이 도구 고장으로 오해하는 문제 수정
   - 발견: review-first repair 흐름에서 모델이 assistant preamble을 스트리밍한 뒤에도 같은 모델 요청의 generic `모델 응답 대기 중` heartbeat가 계속 출력됐다. 사용자는 assistant 출력이 완료된 것인지, 아직 모델 대기 중인지 구분하기 어렵다.
   - 추가 발견: runtime guidance가 tool-call 실행을 의도적으로 supersede한 경우에도 OpenAI-compatible transcript normalizer가 누락된 tool result를 `ERROR: tool result was missing from the saved transcript`로 합성했다. 이후 구현 모델이 이 합성 문구를 실제 tool pipeline 장애로 해석해 "모든 도구가 missing transcript 오류"라고 잘못 보고할 수 있다.
   - 영향: 진행 로그가 assistant 본문과 섞여 UX가 불안정해지고, 복구용 synthetic tool message가 모델의 잘못된 root cause로 전파된다.
   - 수정: 모델 요청 wrapper가 `OnTextDelta` 또는 model stream tool-call progress를 관측하면 해당 요청의 generic wait heartbeat를 억제한다. route/start/done 및 실제 tool-call 준비 progress는 유지하되, 이미 모델 출력이 시작된 뒤에는 "응답 대기"를 반복하지 않는다.
   - 추가 수정: OpenAI-compatible transcript normalizer가 missing tool result 뒤의 user message가 runtime guidance로 보이면, error 대신 `NOTICE: tool call was superseded before execution by runtime guidance...` synthetic tool result를 넣는다. 일반적인 실제 누락(`continue` 같은 사용자 입력 뒤 missing tool result)은 기존 error recovery를 유지한다.
   - 추가 수정: 모델 최종 답변이 `tool result was missing from the saved transcript` 같은 내부 transcript recovery 문구를 실제 "모든 도구 파이프라인 장애"로 해석하고, 세션 재시작/수동 패치 적용을 권하면 final-answer gate가 한 번 차단하고 재시도시킨다. 이 guard는 실제 tool failure가 있으면 최신 tool 이름과 정확한 오류를 인용하게 하고, edit/fix 요청에서는 edit tool을 계속 사용하도록 지시한다.
   - 회귀 테스트: `TestModelRouteWaitProgressStopsAfterStreamingOutputStarts`, `TestOpenAICompatibleClientMarksRuntimeSupersededToolResponses`, `TestAgentRetriesFinalReplyThatBlamesInternalTranscriptRecovery`, 기존 `TestOpenAICompatibleClientSynthesizesMissingToolResponses`.
   - 검증: `go test ./cmd/kernforge -run "TestModelRouteWaitProgressStopsAfterStreamingOutputStarts|TestModelRouteProgressStopsAfterCallerContextCancel|TestOpenAICompatibleClientSynthesizesMissingToolResponses|TestOpenAICompatibleClientMarksRuntimeSupersededToolResponses|TestOpenAICompatibleClientConvertsOrphanToolMessagesToUserContext|TestAgentRetriesFinalReplyThatBlamesInternalTranscriptRecovery" -count=1 -timeout 2m` 통과.
   - 검증: `git diff --check -- cmd/kernforge/parallel_edit_workers.go cmd/kernforge/provider.go cmd/kernforge/model_route_scheduler_test.go cmd/kernforge/provider_test.go REVIEW_HARNESS_SPEC_kor.md` 통과. LF/CRLF 변환 warning만 출력됐다.
   - 하드코딩 점검: `rg -n -i "sample" --glob "*.go" cmd/kernforge` 결과 0건. 전체 회귀는 사용자 지시에 따라 리뷰 요청 시 또는 커밋 직전에만 실행한다.
16. 사용자 smoke - UE 프로젝트 review 초반이 discovery 지연인지 모델 추론 지연인지 구분되지 않는 문제 수정
   - 발견: `FocusedServerRuntime 코드를 분석해서 서버 성능이나 히칭 영향을 검토해줘` 같은 UE 대상 review에서 같은 LM Studio 모델을 reviewer로 쓰면 기존 progress가 `리뷰 모델 요청`을 생략하고 곧바로 generic model route/wait만 출력했다. 사용자는 source discovery가 오래 걸리는지, evidence prompt를 local model이 처리 중인지 구분하기 어렵다.
   - 영향: 27B local model이 큰 source evidence prompt를 처리하는 정상적인 지연도 "초반 작업이 멈춘 것"처럼 보인다. 반대로 discovery가 실제로 실패하거나 느려도 evidence 규모를 확인할 수 없다.
   - 수정: review harness가 scope discovery 직후 `scope`, confidence, 후보 파일/심볼/검색어 수와 후보 preview를 progress로 출력한다.
   - 추가 수정: evidence 수집 직후 sources, changed path 수, evidence text chars, max_context를 progress로 출력한다. evidence 예산은 줄이지 않는다.
   - 추가 수정: reviewer가 main model과 같은 경우에도 `리뷰 모델 요청`, `리뷰 모델 결과`, omission retry progress를 항상 출력한다. 이제 `모델 응답 대기 중`이 보이면 discovery가 아니라 reviewer model inference 구간이라는 점이 로그에서 분명해진다.
   - 회귀 테스트: `TestSameModelReviewProgressShowsScopeEvidenceAndRequest`.
   - 검증: `go test ./cmd/kernforge -run "TestSameModelReviewProgressShowsScopeEvidenceAndRequest|TestDistinctReviewModelProgressIsExplicit|TestReviewModelRetriesOmittedFindingOutput" -count=1 -timeout 2m` 통과.
   - 전체 회귀는 사용자 지시에 따라 리뷰 요청 시 또는 커밋 직전에만 실행한다.
17. 사용자 smoke - UE 프로젝트 source symbol discovery가 unrelated text/config 파일로 오염되는 문제 수정
   - 발견: `FocusedServerRuntime 코드를 분석해서 서버 성능이나 히칭 영향을 검토해줘` 같은 source 분석 요청에서, 요청 심볼이 있음에도 domain/search term 점수가 unrelated text/config 파일을 candidate file로 올릴 수 있었다. 이 경우 실제 source evidence discovery가 실패하고, 모델은 "소스 코드가 제공되지 않았다"거나 `INDEX_IGNORE.txt` 같은 무관한 파일로 narrowing command를 제안할 수 있다.
   - 영향: 코드 기반 검토 요청이 analysis/report 또는 git metadata 중심으로 흐르고, 사용자가 기대한 UE source file evidence가 reviewer prompt에 들어가지 않는다.
   - 수정: 요청에서 구체 symbol/class 후보가 추출되면 candidate ranking은 해당 symbol match를 먼저 요구한다. symbol match가 없는 파일은 domain/search term이 맞아도 candidate로 승격하지 않는다.
   - 추가 수정: source 확장자(`.cpp`, `.h`, `.cs`, `.go`, `.py` 등)는 점수를 올리고, `.txt`, `.json`, `.md`, `.xml` 같은 text/config/doc 파일은 source candidate ranking에서 감점한다.
   - 추가 수정: domain keyword matching에 word-boundary 검사를 추가해 `inf` 같은 짧은 kernel keyword가 일반 단어/경로 내부에서 우연히 매치되어 `security_hardening` mode와 kernel search terms를 끌어오는 일을 줄였다.
   - 추가 수정: symbol 후보는 있는데 source candidate가 없으면 unrelated path를 narrowing command로 내지 않고 `rg -n "<symbol>" .` 형태의 symbol search를 먼저 제안한다.
   - 추가 수정: `.kernforge/`는 local runtime/review 산출물이므로 `.gitignore`에 추가했다. 과거 review evidence artifact에 남은 내부 fixture 이름이 source/doc 검색과 커밋 대상에 섞이지 않게 한다.
   - 회귀 테스트: `TestSourceSymbolRequestRequiresSymbolMatchBeforeDomainTerms`, `TestSourceSymbolRequestWithoutMatchSuggestsSymbolSearchNotUnrelatedPath`.
   - 검증: `go test ./cmd/kernforge -run "TestSourceSymbolRequestRequiresSymbolMatchBeforeDomainTerms|TestSourceSymbolRequestWithoutMatchSuggestsSymbolSearchNotUnrelatedPath|TestCodeAnalysisRequestPrefersSourceEvidenceBeforeAnalysisReport|TestSourceSymbolRequestDoesNotFallbackToUnrelatedGitDirectory|TestSameModelReviewProgressShowsScopeEvidenceAndRequest" -count=1 -timeout 2m` 통과.
   - 하드코딩 점검: source/docs 범위에서 특정 내부 프로젝트/기능 fixture 이름은 0건이다. 전체 회귀는 사용자 지시에 따라 리뷰 요청 시 또는 커밋 직전에만 실행한다.
18. 사용자 smoke - `approved_with_warnings` pre-fix finding이 pre-write 수리 계약에서 빠질 수 있는 문제 수정
   - 발견: `@Product/Worker/PathConverter.cpp:132-221 검토하고 버그를 수정해` 같은 review-first repair 요청에서 수정 전 리뷰가 실제 버그성 warning을 찾았지만 gate verdict는 `approved_with_warnings`였다. implementation guidance에는 warning을 고치라는 문구가 들어가지만, pre-write review evidence에 넘기는 `RepairFindings`는 기존에 `reviewRunNeedsRepair`가 true일 때만 채워졌다.
   - 영향: blocking finding이 없는 `approved_with_warnings` pre-fix review에서는 medium 이상 actionable warning이 사후 검증 계약에서 빠질 수 있다. 구현 모델이 일부 warning을 누락해도 pre-write reviewer가 원래 pre-fix finding 목록과 proposed edit을 직접 대조하지 못한다.
   - 수정: 직전 pre-fix review의 수리 의무 계산을 `reviewRunNeedsRepair`와 분리했다. pre-write evidence에는 blocking finding 전체와 medium 이상 actionable warning을 항상 포함한다.
   - 범위 유지: `reviewRunNeedsRepair`의 의미는 바꾸지 않았다. 따라서 "차단 finding이 없으면 코드 수정 없이 종료"해야 하는 non-review bug hunt 흐름은 기존 동작을 유지한다.
   - 회귀 테스트: `TestPreWriteRepairObligationsIncludeApprovedActionableWarnings`.
   - 검증: `go test ./cmd/kernforge -run "TestPreWriteRepairObligationsIncludeBlockingAndActionableWarnings|TestPreWriteRepairObligationsIncludeApprovedActionableWarnings|TestPreWriteEvidenceIncludesPreFixRepairObligations|TestReviewBeforeFixApprovedWithWarningsStopsBeforeImplementation|TestReviewBeforeFixAddsReviewFeedbackBeforeImplementation" -count=1 -timeout 2m` 통과.
   - 하드코딩 점검: `rg -n -i --hidden --glob '!.git/**' "<internal project fixture terms>" .` 결과 0건이다. 전체 회귀는 사용자 지시에 따라 리뷰 요청 시 또는 커밋 직전에만 실행한다.
19. 사용자 smoke - pre-write diff preview의 synthetic `before/after` path가 scope discovery에 섞이는 문제 수정
   - 발견: pre-write review에서 patch preview diff가 `--- before/<path>`와 `+++ after/<path>` 형태를 포함하면 scope discovery가 실제 파일 외에 `before/<path>`, `after/<path>`를 별도 candidate file로 기록했다. 사용자 로그에서는 파일 후보가 1개여야 할 상황에서 `파일 후보=5`, `paths=3`처럼 부풀어 보였다.
   - 영향: 실제 review gate는 통과할 수 있지만, scope/evidence progress가 과장되고 이후 narrowing command나 freshness 계산이 synthetic path에 오염될 수 있다.
   - 수정: unified diff path 정규화에서 `before/`, `after/` prefix를 `a/`, `b/`와 같은 synthetic prefix로 처리한다. 빈 path는 candidate list에 넣지 않는다.
   - 회귀 테스트: `TestReviewScopeDiscoveryNormalizesBeforeAfterDiffPaths`.
   - 검증: `go test ./cmd/kernforge -run "TestReviewScopeDiscoveryUsesProvidedDiffPaths|TestReviewScopeDiscoveryNormalizesBeforeAfterDiffPaths|TestPreWriteRepairObligationsIncludeApprovedActionableWarnings|TestSourceSymbolRequestRequiresSymbolMatchBeforeDomainTerms" -count=1 -timeout 2m` 통과.
   - 하드코딩 점검: `rg -n -i --hidden --glob '!.git/**' "<internal project fixture terms>" .` 결과 0건이다. 전체 회귀는 사용자 지시에 따라 리뷰 요청 시 또는 커밋 직전에만 실행한다.
20. 사용자 smoke - UE symbol review가 참조 파일 32개를 broad evidence로 보내는 문제 수정
   - 발견: `FocusedServerRuntime 코드를 분석해서 서버 성능이나 히칭 영향을 검토해줘` 같은 UE source-symbol 요청에서 `rg --files-with-matches` 결과를 그대로 후보로 삼았다. 이 경우 symbol을 참조만 하는 GameMode/Component 파일들이 definition/implementation 파일보다 먼저 올라오고, local reviewer가 broad evidence를 2회 처리하면서 weak output을 만들 수 있었다.
   - 영향: source evidence discovery가 동작하더라도 실제 핵심 구현 파일 대신 reference fan-out이 evidence budget을 소비한다. 사용자는 파일 후보가 32개인 broad review와 `Review scope needs narrowing`만 보게 된다.
   - 수정: workspace search 후보를 content-aware score로 재평가한다. 작은 파일은 실제 본문을 읽어 symbol match와 source-file boost를 반영하고, 큰 파일은 streaming symbol scan을 사용한다.
   - 추가 수정: symbol이 있는 요청에서는 `class <Symbol>`, `struct <Symbol>`, `enum class <Symbol>`, `func <Symbol>`, `<Symbol>::Method` 같은 definition/implementation 패턴을 가진 후보를 우선한다. 이런 후보가 있으면 reference-only 파일은 candidate set에서 제거한다.
   - 회귀 테스트: `TestSourceSymbolRequestPrefersDefinitionOverReferences`.
   - 검증: `go test ./cmd/kernforge -run "TestSourceSymbolRequestRequiresSymbolMatchBeforeDomainTerms|TestSourceSymbolRequestPrefersDefinitionOverReferences|TestSourceSymbolRequestWithoutMatchSuggestsSymbolSearchNotUnrelatedPath|TestReviewScopeDiscoveryNormalizesBeforeAfterDiffPaths" -count=1 -timeout 2m` 통과.
   - 하드코딩 점검: `rg -n -i --hidden --glob '!.git/**' "<internal project fixture terms>" .` 결과 0건이다. 전체 회귀는 사용자 지시에 따라 리뷰 요청 시 또는 커밋 직전에만 실행한다.
21. 사용자 smoke - UE prefix가 붙은 class 정의를 symbol definition으로 인식하지 못할 수 있는 문제 수정
   - 발견: UE C++에서는 사용자가 요청한 도메인 이름이 실제 소스에서 `U<Symbol>`, `A<Symbol>`, `F<Symbol>`, `I<Symbol>` 같은 접두 class/struct로 정의될 수 있다. 기존 symbol definition ranking은 정확히 같은 이름의 `class <Symbol>` 또는 `<Symbol>::Method`만 강하게 보았다.
   - 영향: 실제 구현/정의 파일이 있어도 reference-only 파일 fan-out이 broad 후보로 남을 수 있다.
   - 수정: symbol alias를 생성해 UE 접두어(`A`, `U`, `F`, `I`, `E`, `S`, `T`)가 붙은 class/struct/enum/function definition도 같은 symbol의 정의 후보로 본다. 이미 접두어가 붙은 symbol은 접두어를 제거한 alias도 함께 본다.
   - 추가 수정: Go method receiver 형태도 symbol definition pattern에 포함했다.
   - 회귀 테스트: `TestSourceSymbolRequestFindsUnrealPrefixedDefinition`.
   - 검증: `go test ./cmd/kernforge -run "TestSourceSymbolRequestPrefersDefinitionOverReferences|TestSourceSymbolRequestFindsUnrealPrefixedDefinition|TestSourceSymbolRequestRequiresSymbolMatchBeforeDomainTerms|TestSourceSymbolRequestWithoutMatchSuggestsSymbolSearchNotUnrelatedPath" -count=1 -timeout 2m` 통과.
   - 배포 주의: 다른 PC에서 UE smoke를 실행한다면, 해당 PC의 `kernforge.exe`가 이 변경 이후 빌드인지 확인해야 한다. 이전 바이너리에서는 같은 broad candidate 결과가 그대로 재현될 수 있다.
22. 사용자 smoke - symbol reference fan-out만 있고 definition 후보가 없을 때 broad evidence를 보내는 문제 수정
   - 발견: 최신 UE smoke에서도 symbol 후보는 1개였지만 candidate file이 32개로 남았다. 이는 symbol을 참조하는 파일은 많이 찾았지만 definition/implementation 후보가 없거나 탐지되지 않은 상태에서 reference fan-out을 그대로 review evidence로 보냈기 때문이다.
   - 영향: local reviewer가 broad evidence 60KB를 두 번 처리하고도 weak finding만 반환한다. 실제로는 모델 호출 전에 "심볼 검색 결과가 reference fan-out뿐이므로 정의 파일을 좁혀 달라"는 narrowing으로 멈추는 편이 낫다.
   - 수정: symbol이 있는 요청에서 workspace search 후보가 8개를 초과하고 definition/implementation 후보가 하나도 없으면 candidate files를 비운다. 이렇게 하면 scope는 `unknown`으로 남고, next command는 `/review --path <unrelated-reference>` 대신 `rg -n "<symbol>" .` 형태의 symbol search를 제안한다.
   - 회귀 테스트: `TestSourceSymbolRequestRejectsBroadReferenceFanoutWithoutDefinition`.
   - 검증: `go test ./cmd/kernforge -run "TestSourceSymbolRequestPrefersDefinitionOverReferences|TestSourceSymbolRequestFindsUnrealPrefixedDefinition|TestSourceSymbolRequestRejectsBroadReferenceFanoutWithoutDefinition|TestSourceSymbolRequestWithoutMatchSuggestsSymbolSearchNotUnrelatedPath" -count=1 -timeout 2m` 통과.
   - 하드코딩 점검: `rg -n -i --hidden --glob '!.git/**' "<internal project fixture terms>" .` 결과 0건이다. 전체 회귀는 사용자 지시에 따라 리뷰 요청 시 또는 커밋 직전에만 실행한다.
23. 사용자 smoke - 로컬 코드 수리 중 MCP web search로 우회하는 문제 수정
   - 발견: `@Product/Worker/PathConverter.cpp:132-221 검토하고 버그를 수정해` 같은 로컬 코드 수리 흐름에서 edit tool anchor mismatch나 pre-write review block 이후 모델이 MCP web/search tool을 호출하려고 했다. 이어서 scope discovery 후보에도 `web/research`, `web/search/browser`, `code/change`, `C:/Win` 같은 도구명/가짜 경로가 섞일 수 있었다.
   - 영향: 사용자는 소스 기반 리뷰를 요청했는데 외부 검색 실패 로그가 출력되고, pre-write review의 candidate file/evidence scope가 실제 파일 외의 synthetic token으로 오염된다.
   - 수정: latest user request가 로컬 코드 리뷰/수리로 판정되고 사용자가 명시적으로 외부 웹 리서치를 요청하지 않았으면 MCP web/search/browser tool call을 실행 전에 차단한다. `@path`가 있는 요청뿐 아니라 `FocusedRuntime 코드를 분석...`처럼 심볼/모듈명만 주어진 코드 분석 요청도 로컬 코드 작업으로 본다. 모델에는 `read_file`, `grep`, `git diff/status`, review finding evidence로 계속 진행하라는 runtime guidance를 넣는다.
   - 추가 수정: system prompt에서도 로컬 코드 리뷰/수리 작업은 외부 웹 리서치 요청이 명시된 경우를 제외하고 MCP web/search/browser를 쓰지 말라고 명시했다. `source`라는 일반 단어만으로 web research intent가 켜지지 않게 했다.
   - 추가 수정: scope discovery path 후보 필터가 `web/research`, `web/search/browser`, `mcp__*`, `code/change`, Windows root fragment 같은 synthetic tool/path token을 candidate file에서 제외한다. 다만 실제 프론트엔드 저장소의 `web/src/App.tsx` 같은 정상 경로는 유지한다. diff path filter도 같은 synthetic path를 제거한다.
   - 회귀 테스트: `TestSystemPromptDoesNotSuggestWebResearchForLocalCodeRepair`, `TestAgentBlocksWebResearchForLocalCodeRepair`, `TestReviewScopeDiscoveryRejectsSyntheticToolPaths`, `TestReviewScopeDiscoveryKeepsRealWebDirectoryPaths`.
   - 검증: `go test ./cmd/kernforge -run "TestSystemPrompt|TestAgentBlocksWebResearchForLocalCodeRepair|TestAgentBlocksLocalInspectionBeforeWebResearchWhenCapabilityAvailable|TestAgentAllowsLocalInspectionAfterWebResearchToolUsed|TestAgentUsesLoadedWebResearchMCPToolsBeforeLocalInspection|TestReviewScopeDiscoveryRejectsSyntheticToolPaths|TestReviewScopeDiscoveryKeepsRealWebDirectoryPaths" -count=1 -timeout 2m` 통과.
   - 하드코딩 점검: `rg -n -i --hidden --glob '!.git/**' "<internal project fixture terms>" .` 결과 0건이다. 전체 회귀는 사용자 지시에 따라 리뷰 요청 시 또는 커밋 직전에만 실행한다.
24. 사용자 smoke - UE 서버 모듈 리뷰 finding의 test field가 중간에서 잘리는 문제 수정
   - 발견: UE 서버 모듈 성능/히칭 리뷰에서 source discovery와 file evidence 수집은 정상 동작했지만, 로컬 reviewer가 blocker finding의 `test_recommendation` 필드를 문장 중간에서 끊어 반환했다. 기존 parser는 말줄임표나 `(truncated)` 표식만 감지하므로, 짧은 한국어 tail fragment로 끝난 구조화 필드를 `usable` finding으로 받아들였다.
   - 영향: 사용자는 실제 blocker를 볼 수 있지만 테스트 권고와 검증 조건이 잘린 상태로 repair 흐름에 들어갈 수 있다. 특히 로컬 reviewer는 기존 omission retry budget이 0이라 잘림을 감지해도 재시도하지 않았다.
   - 수정: structured review parser가 마지막 구조화 필드가 `test_recommendation`/`test`이고 짧은 한국어 fragment로 끝나는 경우 cut-off placeholder를 추가하고 model quality를 `weak`로 낮춘다. 기존 blocker finding은 보존하되, 엄격 재시도 trigger가 걸리도록 한다.
   - 추가 수정: provider stop reason이 `length`, `max_token`, `incomplete`, `partial`, `truncated` 계열이면 cut-off placeholder를 추가한다.
   - 추가 수정: local/OpenAI-compatible reviewer 계열의 review token cap을 6000, retry token cap을 5000으로 늘리고 omission/cut-off retry budget을 1로 올렸다. 예산을 보수적으로 잡지 말라는 운영 요구를 반영한 값이다.
   - 추가 수정: retry progress 문구를 "생략 표식"뿐 아니라 "생략/잘림 징후"로 바꿔, 이번처럼 명시적 ellipsis 없이 잘린 출력도 설명한다.
   - 회귀 테스트: `TestReviewModelParserFlagsCutOffKoreanTestRecommendation`, `TestReviewModelRetriesCutOffFindingOutput`, 기존 `TestReviewModelRetriesOmittedFindingOutput`.
   - 검증: `go test ./cmd/kernforge -run "TestReviewModelParserFlagsCutOffKoreanTestRecommendation|TestReviewModelRetriesCutOffFindingOutput|TestReviewModelRetriesOmittedFindingOutput|TestReviewProviderBehaviorCapsReviewTokens|TestReviewProviderBehaviorControlsOmissionRetryBudget" -count=1 -timeout 2m` 통과.
   - 하드코딩 점검: 문서와 테스트 fixture에는 실제 smoke 대상의 내부 프로젝트명을 쓰지 않고 `UE 서버 모듈`, `Sample` 계열 fixture만 사용한다.
25. 사용자 smoke - pre-write review warning 이후 로컬 코드 수리가 웹 검색으로 빠지는 문제 수정
   - 발견: 로컬 코드 수리 요청에서 수정 전 리뷰는 focused source evidence로 warning을 찾았지만, pre-write review가 actionable warning으로 patch를 막은 뒤 구현 모델이 "게이트 통과"를 이유로 MCP web search를 호출하려고 했다.
   - 원인: 원래 사용자 요청은 `@path` 기반 로컬 코드 수리였지만, pre-write review가 삽입한 최신 user guidance는 `Automatic pre-write review found actionable warnings...` 형태였다. 기존 local-code 판별은 이 runtime guidance를 로컬 코드 작업으로 강하게 보지 않아 web research guard가 약해졌다.
   - 영향: 소스 기반 수리 흐름에서 외부 검색 실패 로그가 출력되고, 모델이 review gate를 로컬 source evidence가 아니라 웹 검색으로 만족시키려는 잘못된 루프에 들어갈 수 있다.
   - 수정: `automatic pre-write review`, `pre-write review`, `edit proposal`, `proposed edit`, `review findings`, `repair guidance`, `review gate` 및 한국어 대응 문구를 local code work signal로 추가했다. 이제 pre-write review feedback 이후에도 MCP web/search/browser tool call은 실행 전에 차단된다.
   - 추가 수정: blocker/warning pre-write feedback의 implementation rules에 "이 작업은 로컬 코드 리뷰/수리이며, 이 gate를 만족시키기 위해 MCP web/search/browser나 외부 웹 리서치를 쓰지 말라"는 규칙을 직접 포함했다.
   - 회귀 테스트: `TestSystemPromptDoesNotSuggestWebResearchForLocalCodeRepair`, `TestAgentBlocksWebResearchForLocalCodeRepair`, `TestAgentBlocksWebResearchAfterPreWriteReviewFeedback`.
   - 검증: `go test ./cmd/kernforge -run "TestSystemPromptDoesNotSuggestWebResearchForLocalCodeRepair|TestAgentBlocksWebResearchForLocalCodeRepair|TestAgentBlocksWebResearchAfterPreWriteReviewFeedback" -count=1 -timeout 2m` 통과.
   - 하드코딩 점검: `rg -n -i --hidden --glob '!.git/**' "<internal project fixture terms>" .` 결과 0건이다.
26. 사용자 smoke - CLI review header의 finding 총계와 표시 항목 수가 어긋나 보이는 문제 수정
   - 발견: UE 서버 모듈 리뷰에서 헤더는 `finding=7 blocker=2 warning=3`처럼 표시됐지만, 본문에는 blocker 2개와 warning 3개만 표시됐다. 실제로는 gate에 영향이 없는 info/note finding이 총계에는 포함되고 concise CLI 본문에서는 숨겨진 상태였다.
   - 영향: 사용자는 review finding이 출력 도중 잘렸거나 일부 항목이 누락됐다고 판단할 수 있다. 특히 이전에 출력 잘림 문제가 있었기 때문에 같은 로그 형태가 신뢰도 문제로 보인다.
   - 수정: CLI review header에 gate에 영향이 없는 finding 수를 `note=N`으로 표시한다. 본문은 기존처럼 blocker/warning 중심으로 유지하되, 총계가 왜 더 큰지 헤더에서 즉시 해석할 수 있게 했다.
   - 추가 수정: `ReviewResult`에도 `note_count`를 기록해 artifact/MCP 소비자가 blocker/warning/note 합계를 일관되게 볼 수 있게 했다.
   - 회귀 테스트: `TestNeedsRevisionReviewHeaderShowsNoteCount`, 기존 `TestLowSeverityFindingCountsAsGateWarningAndIsRendered`, `TestApprovedReviewRendersInfoFindingWhenNoWarnings`.
   - 검증: `go test ./cmd/kernforge -run "TestNeedsRevisionReviewHeaderShowsNoteCount|TestLowSeverityFindingCountsAsGateWarningAndIsRendered|TestApprovedReviewRendersInfoFindingWhenNoWarnings|TestPrintReviewRunNeedsRevisionUsesWarnAndKoreanLabels|TestPrintReviewRunApprovedWithWarningsShowsWarningFindings" -count=1 -timeout 2m` 통과.
   - 하드코딩 점검: `rg -n -i --hidden --glob '!.git/**' "<internal project fixture terms>" .` 결과 0건이다.
27. 사용자 smoke - pre-fix blocker가 있으면 warning 수리 의무가 repair plan에서 묻히는 문제 수정
   - 발견: 로컬 코드 수리 요청에서 pre-fix review가 blocker 1개와 medium warning 2개를 반환했다. 하지만 `RepairPlan`은 blocker가 존재하면 `Blocking findings`만 작성했고, warning 수리 의무는 implementation rules의 일반 문장으로만 남았다. 구현 모델은 blocker 하나만 수정하려고 했고, pre-write review가 뒤늦게 actionable warning 누락을 막았다.
   - 영향: pre-write gate가 최종 방어선 역할은 하지만, 구현 모델이 첫 patch에서 warning을 놓치기 쉬워 반복 loop와 지연이 늘어난다.
   - 수정: `buildReviewRepairPlan`이 blocker가 있을 때도 medium-or-higher actionable warning을 `Medium-or-higher actionable warnings that must also be handled` 섹션에 명시한다. `test_gap`, `evidence_gap`은 수리 의무에서 제외한다.
   - 추가 수정: pre-write repair obligation과 repair plan warning 판정이 같은 helper를 사용하도록 맞췄다.
   - 추가 수정: `run_shell git status`와 `run_shell git diff`는 전용 `git_status`/`git_diff` 도구를 쓰도록 즉시 guidance를 반환한다. 이렇게 하면 read-only git inspection이 shell approval prompt로 빠지는 일을 줄인다.
   - 회귀 테스트: `TestReviewRepairPlanIncludesActionableWarningsWithBlockers`, `TestRunShellGuidesGitStatusToDedicatedTool`, `TestRunShellGuidesGitDiffToDedicatedTool`.
   - 검증: `go test ./cmd/kernforge -run "TestReviewRepairPlanIncludesActionableWarningsWithBlockers|TestPreWriteRepairObligationsIncludeBlockingAndActionableWarnings|TestPreWriteRepairObligationsIncludeApprovedActionableWarnings|TestPreWriteEvidenceIncludesPreFixRepairObligations" -count=1 -timeout 2m` 통과.
   - 검증: `go test ./cmd/kernforge -run "TestRunShellGuidesGitStatusToDedicatedTool|TestRunShellGuidesGitDiffToDedicatedTool|TestRunShellAllowsReadOnlyCommands|TestRunShellRejectsWorkspaceMutatingCommands" -count=1 -timeout 2m` 통과.
   - 하드코딩 점검: `rg -n -i --hidden --glob '!.git/**' "<internal project fixture terms>" .` 결과 0건이다.
28. 사용자 smoke - read-only 코드 분석 리뷰에서 repair next command가 필수 수정처럼 보이는 문제 수정
   - 발견: UE 서버 모듈 성능/히칭 분석 요청은 "코드를 분석해서 검토"하는 read-only 성격이었지만, blocker finding이 있으면 next command reason이 "수정 작업을 이어가야 합니다"로 표시됐다.
   - 영향: 사용자는 분석 리뷰 결과를 보고 싶었는데 하네스가 즉시 수정 흐름을 강요하는 것처럼 보인다. review finding은 유용하지만, read-only 분석 요청에서는 repair가 optional follow-up이어야 한다.
   - 수정: `prefersReadOnlyAnalysisIntent`가 true인 review run에서는 repair next command의 reason/when/action/expected_result를 optional wording으로 바꾼다. 한국어는 "수정은 사용자가 원할 때만"으로, 영어는 "repair is optional unless the user asks to fix them"으로 표시한다.
   - 범위 유지: `/continuity continue from review` command 자체는 유지한다. 사용자가 바로 고치고 싶을 때 최신 review finding을 repair guidance로 넘기는 경로는 그대로 둔다.
   - 회귀 테스트: `TestReadOnlyAnalysisRepairNextCommandIsOptionalInKoreanAndEnglish`, 기존 `TestPrintReviewRunExplainsNextCommands`, `TestReviewMCPResponseIncludesActionContractBooleans`.
   - 검증: `go test ./cmd/kernforge -run "TestReadOnlyAnalysisRepairNextCommandIsOptionalInKoreanAndEnglish|TestPrintReviewRunExplainsNextCommands|TestReviewMCPResponseIncludesActionContractBooleans" -count=1 -timeout 2m` 통과.
   - 하드코딩 점검: `rg -n -i --hidden --glob '!.git/**' "<internal project fixture terms>" .` 결과 0건이다.
29. 사용자 smoke - synthetic scope token, shell file-read 우회, transcript recovery 오해 동시 수정
   - 발견: 로컬 코드 수리 smoke에서 pre-write scope discovery 후보에 `C://`, `low/correctness`, `medium/stability` 같은 실제 파일이 아닌 토큰이 섞였다. 이어서 구현 모델이 소스 확인을 `run_shell Get-Content`로 우회해 shell approval prompt를 만들고, 내부 transcript recovery 문구를 실제 도구 장애로 단정한 뒤 `write_file` 전체 재작성으로 넘어가려 했다.
   - 영향: source evidence 자체는 준비됐지만, 이후 repair loop가 synthetic path/evidence 후보로 오염되고 read-only 파일 확인도 구조화된 `read_file` evidence가 아니라 shell 출력으로 흘러간다. 마지막 fallback은 "모든 도구가 고장"이라는 잘못된 원인 분석과 큰 write surface를 만들 수 있다.
   - 수정: scope discovery path 필터가 drive-root fragment(`C:`/`C:/`)와 severity/category fragment(`low/correctness`, `medium/stability` 등)를 candidate file에서 제외한다. 실제 `web/src/App.tsx`처럼 정상 경로인 source file은 유지한다.
   - 추가 수정: `run_shell`의 read-only/cache-only command라도 `Get-Content`, `cat`, `type`, `gc` 기반 source inspection이면 실행 전에 `read_file`을 쓰라는 English guidance를 반환한다. 기존 `git status`/`git diff` 전용 도구 유도도 cache-only 분류에서 동작하도록 맞췄고, manual file write command는 여전히 dedicated guidance보다 먼저 차단된다.
   - 추가 수정: assistant text가 `tool result was missing from the saved transcript` 같은 내부 recovery 문구를 "모든 도구 장애"로 해석하면서 tool call을 함께 내면, 해당 tool call을 실행하지 않고 재시도 guidance를 삽입한다. edit/fix 요청에서는 실제 최신 tool error를 인용하거나 edit tool을 계속 사용하게 한다.
   - 회귀 테스트: `TestReviewScopeDiscoveryRejectsSyntheticToolPaths`, `TestRunShellGuidesGetContentToReadFile`, `TestRunShellGuidesGitStatusToDedicatedTool`, `TestRunShellGuidesGitDiffToDedicatedTool`, `TestAgentBlocksToolCallsThatBlameInternalTranscriptRecovery`, 기존 `TestAgentRetriesFinalReplyThatBlamesInternalTranscriptRecovery`.
   - 검증: `go test ./cmd/kernforge -run "TestReviewScopeDiscoveryRejectsSyntheticToolPaths|TestReviewScopeDiscoveryKeepsRealWebDirectoryPaths|TestRunShellGuidesGetContentToReadFile|TestRunShellGuidesGitStatusToDedicatedTool|TestRunShellGuidesGitDiffToDedicatedTool|TestRunShellRejectsInlinePowerShellFileWrites|TestAgentBlocksToolCallsThatBlameInternalTranscriptRecovery|TestAgentRetriesFinalReplyThatBlamesInternalTranscriptRecovery" -count=1 -timeout 2m` 통과.
   - 리뷰 모드 재검토: `go run ./cmd/kernforge -command "/review --path cmd/kernforge/agent.go --path cmd/kernforge/tools.go --path cmd/kernforge/review_scope_discovery.go --path cmd/kernforge/review_harness_test.go --path cmd/kernforge/tools_edit_guard_test.go --path cmd/kernforge/agent_verify_loop_test.go --no-model"` 실행. 결과는 blocker 0개, warning 2개(`Changed files have no latest verification evidence`, `Sensitive evidence was redacted`)였다. 실제 수동 검증은 위 targeted test, `git diff --check`, 하드코딩 점검으로 통과했다.
   - 하드코딩 점검: `rg -n -i --hidden --glob '!.git/**' "<internal project fixture terms>" .` 결과 0건이다.
30. 사용자 smoke - UE 서버 소스 분석 리뷰가 `change/general_change`로 표시되고 성능 finding severity가 과격한 문제 수정
   - 발견: UE 서버 모듈 성능/히칭 분석 요청은 source evidence discovery와 file excerpt 수집이 정상 동작했지만, review header가 `target=change`, `mode=general_change`로 표시됐다. 또한 reviewer prompt에는 성능 분석 finding의 severity를 보수적으로 교정하는 규칙이 없어, 잠재적 lock contention이나 allocation overhead도 high/blocker로 올라가기 쉬웠다.
   - 영향: 사용자는 read-only 분석 리뷰를 요청했는데 변경 diff review처럼 보인다. 실제 repair next command는 optional로 표시되더라도 header와 model prompt가 "수정해야 하는 change review" 쪽으로 기울어 모델 finding severity가 과도해질 수 있다.
   - 수정: source evidence를 우선해야 하는 read-only 분석 요청은 `source_analysis` target으로 분류한다. 성능, 히칭, latency, stall, tick, lock contention 계열 요청은 `performance_analysis` mode로 분류한다. 일반 analysis report 검토는 기존 `analysis_report` target을 유지한다.
   - 추가 수정: review model prompt와 omission retry prompt에 English calibration rule을 추가했다. source analysis review는 proposed code-change review가 아니며, performance/hitch analysis에서는 evidence-backed data race, deadlock, main-thread blocking, unbounded growth, clearly frequent hot-path work만 high/blocker로 두고, frequency/profiling evidence가 없는 plausible lock contention, repeated allocation, broad-copy overhead는 medium으로 두게 한다.
   - 회귀 테스트: `TestCodeAnalysisRequestPrefersSourceEvidenceBeforeAnalysisReport`, `TestSourceSymbolRequestRequiresSymbolMatchBeforeDomainTerms`, `TestReviewModelPromptCalibratesSourcePerformanceAnalysisSeverity`.
   - 검증: `go test ./cmd/kernforge -run "TestCodeAnalysisRequestPrefersSourceEvidenceBeforeAnalysisReport|TestSourceSymbolRequestRequiresSymbolMatchBeforeDomainTerms|TestReviewModelPromptCalibratesSourcePerformanceAnalysisSeverity|TestAnalysisReportRequestWithoutSourceIntentKeepsAnalysisTarget|TestReadOnlyAnalysisRepairNextCommandIsOptionalInKoreanAndEnglish" -count=1 -timeout 2m` 통과.
   - 리뷰 모드 재검토: `go run ./cmd/kernforge -command "/review --path cmd/kernforge/review_harness.go --path cmd/kernforge/review_harness_collect.go --path cmd/kernforge/review_harness_command.go --path cmd/kernforge/review_harness_models.go --path cmd/kernforge/review_harness_test.go --no-model"` 실행. 결과는 blocker 0개, warning 2개(`Changed files have no latest verification evidence`, `Sensitive evidence was redacted`)였다.
   - 하드코딩 점검: `rg -n -i --hidden --glob '!.git/**' "<internal project fixture terms>" .` 결과 0건이다.
31. 사용자 smoke - pre-write review scope 후보에 `+/-` diff marker fragment가 섞이는 문제 수정
   - 발견: 로컬 코드 수리 smoke에서 첫 patch는 `*** End Patch` 누락으로 실패했지만 기존 retry 경로로 다음 patch가 pre-write review까지 진행했다. 다만 pre-write scope discovery 후보가 실제 파일 1개와 함께 `+/-`를 candidate file처럼 표시했다.
   - 영향: 실제 review evidence는 대체로 정상이어도 progress와 artifact의 scope discovery가 synthetic diff marker로 오염된다. 이 상태가 누적되면 candidate path 수, freshness, narrowing command 판단이 실제 파일 범위보다 넓어 보일 수 있다.
   - 수정: scope discovery synthetic path 필터에 diff marker fragment 패턴을 추가했다. `+`, `-`, `/`만으로 구성된 token은 실제 파일 후보에서 제외한다.
   - 회귀 테스트: `TestReviewScopeDiscoveryRejectsSyntheticToolPaths`에 `+/-` 케이스를 추가하고, 기존 before/after diff path normalization과 real web source path 유지 테스트를 함께 확인했다.
   - 검증: `go test ./cmd/kernforge -run "TestReviewScopeDiscoveryRejectsSyntheticToolPaths|TestReviewScopeDiscoveryNormalizesBeforeAfterDiffPaths|TestReviewScopeDiscoveryKeepsRealWebDirectoryPaths" -count=1 -timeout 2m` 통과.
32. 사용자 smoke - UE 서버 모듈 source/performance analysis evidence 예산 확대
   - 발견: UE 서버 모듈 성능/히칭 분석 smoke에서 scope discovery는 bounded 후보 12개를 찾았지만 evidence 준비는 `paths=2 chars=61159 max_context=60000`에서 멈췄다.
   - 영향: 모델이 실제 모듈을 찾는 문제는 해결됐지만, 분석 전용 요청에서 2개 파일만 보고 high finding을 만들면 넓은 서버 모듈의 다른 병목이나 반례를 놓칠 수 있다.
   - 수정: 기본 review context 예산은 60k로 유지하되, 사용자가 별도 `max_context_chars`를 지정하지 않은 source analysis/performance analysis에서는 180k로 자동 상향한다. 사용자가 명시한 값은 그대로 존중한다.
   - 추가 수정: source evidence가 예산을 이미 소진한 뒤 `git diff` excerpt가 음수 remaining budget을 "무제한"처럼 받아 전체 diff를 덧붙이는 문제를 막았다. 예산이 0 이하이면 diff/code excerpt는 생략하고 warning만 남긴다.
   - 회귀 테스트: `TestSameModelReviewProgressShowsScopeEvidenceAndRequest`가 source/performance analysis 진행 로그에 `max_context=180000`이 표시되는지 확인하고, `TestReviewEvidenceOmitsGitDiffWhenContextBudgetIsExhausted`가 예산 소진 뒤 diff marker가 evidence에 새지 않는지 확인한다.
   - 검증: `go test ./cmd/kernforge -run "TestSameModelReviewProgressShowsScopeEvidenceAndRequest|TestReviewEvidenceOmitsGitDiffWhenContextBudgetIsExhausted|TestReviewScopeDiscoveryRejectsSyntheticToolPaths|TestReviewScopeDiscoveryNormalizesBeforeAfterDiffPaths|TestReviewScopeDiscoveryKeepsRealWebDirectoryPaths" -count=1 -timeout 2m` 통과.
33. 사용자 smoke - 한국어 로컬 코드 수리 중 영어 진행문과 웹 검색 재시도 방지
   - 발견: 한국어 로컬 코드 수리 smoke에서 pre-write review 경고 이후 모델이 `I see... Let me...` 형태의 영어 진행문을 내고, `mcp__web_research__search_web` 웹 검색을 시도했다.
   - 영향: 사용자는 한국어로 요청했는데 중간 응답이 영어로 바뀌고, 외부 검색 API key가 없는 환경에서 불필요한 실패가 발생했다. 로컬 코드 수리 흐름도 read_file/apply_patch 중심에서 벗어난다.
   - 수정: 로컬 코드 리뷰/수리 요청에서는 MCP catalog가 비어 있어도 tool name 자체가 web/search/browser 계열이면 실행 전에 차단한다. 한국어 요청이면 차단 피드백도 한국어로 넣는다.
   - 추가 수정: 한국어 로컬 코드 수리에서 assistant가 영어 진행문과 tool call을 함께 내면 해당 tool call을 실행하기 전에 한국어 재시도 지침을 주입한다. 코드 식별자, 경로, API 이름, 명령어는 원문 유지가 가능하다.
   - 회귀 테스트: `TestAgentBlocksWebResearchForLocalCodeRepair`, `TestAgentBlocksNamespacedWebResearchForLocalCodeRepairWithoutMCPCatalog`, `TestAgentBlocksWebResearchAfterPreWriteReviewFeedback`, `TestAgentRetriesEnglishToolNarrationForKoreanLocalCodeRepair`, 기존 `TestSystemPromptDoesNotSuggestWebResearchForLocalCodeRepair`.
   - 검증: `go test ./cmd/kernforge -run "TestAgentBlocksWebResearchForLocalCodeRepair|TestAgentBlocksNamespacedWebResearchForLocalCodeRepairWithoutMCPCatalog|TestAgentBlocksWebResearchAfterPreWriteReviewFeedback|TestAgentRetriesEnglishToolNarrationForKoreanLocalCodeRepair|TestSystemPromptDoesNotSuggestWebResearchForLocalCodeRepair" -count=1 -timeout 2m` 통과.
34. 사용자 smoke - UE 서버 모듈 source/performance analysis의 파일 커버리지와 gate UX 개선
   - 발견: source/performance analysis에서 `max_context=180000`은 적용됐지만 `paths=2`에 머물렀다. 앞쪽 큰 파일이 남은 evidence budget을 대부분 소비해 후보 12개 중 나머지 파일 excerpt가 들어오지 못했다. 또한 분석 전용 high performance finding이 기본 `base_security` pack 영향으로 `blocker=1 needs_revision`처럼 표시됐다.
   - 영향: 분석 품질이 앞쪽 2개 파일에 과의존하고, 사용자가 "검토해줘"라고 한 분석 요청이 "수정해야 함" gate처럼 보인다.
   - 수정: 여러 source candidate를 수집할 때 남은 후보 수 기준으로 파일별 evidence budget을 나눠 더 많은 파일을 샘플링한다. section header/fence 오버헤드로 evidence text가 `max_context`를 넘으면 최종 clamp와 warning을 남긴다.
   - 추가 수정: read-only source/performance analysis에서는 model high finding을 blocker로 승격하지 않고 warning으로 표시한다. 명시적 `blocker` severity나 `BlocksGate=true` deterministic finding은 계속 gate를 막는다. `repair-warnings` next command도 "수정은 사용자가 원할 때만" wording을 사용한다.
   - 회귀 테스트: `TestSourceAnalysisDistributesEvidenceAcrossBoundedFiles`, `TestReadOnlySourceAnalysisHighPerformanceFindingWarnsWithoutBlocking`, 기존 source analysis/context budget tests.
   - 검증: `go test ./cmd/kernforge -run "TestSourceAnalysisDistributesEvidenceAcrossBoundedFiles|TestReadOnlySourceAnalysisHighPerformanceFindingWarnsWithoutBlocking|TestSameModelReviewProgressShowsScopeEvidenceAndRequest|TestReviewEvidenceOmitsGitDiffWhenContextBudgetIsExhausted|TestReadOnlyAnalysisRepairNextCommandIsOptionalInKoreanAndEnglish" -count=1 -timeout 2m` 통과.
35. 사용자 smoke - pre-fix review finding을 사용자에게 출력하지 않고 바로 수정하는 문제 방지
   - 발견: 로컬 코드 수리 smoke에서 수정 전 리뷰가 RF-001/RF-002/RF-003을 찾았지만, 구현 모델의 사용자 가시 응답은 "전체 파일을 확인했으니 수정하겠다" 수준에 머물렀고 실제 검토 결과 요약 없이 곧바로 patch/edit tool로 진입했다.
   - 영향: 리뷰 하네스가 어떤 버그를 근거로 수리하는지 사용자가 대화 로그에서 확인할 수 없다. progress line에는 finding 요약이 있어도 assistant 본문이 검토 결과를 생략하면 "모델이 코드를 검토하지 않은 것처럼" 보인다.
   - 수정: pre-fix review feedback의 implementation rules에 파일 쓰기/패치 도구 호출 전 `검토 결과:` 또는 `Review findings:` 섹션으로 RF 항목과 조치 방향을 먼저 출력하라는 규칙을 추가했다.
   - 추가 수정: 직전 review run이 `pre_fix`이고 수리 의무 finding이 남아 있는 상태에서 assistant가 RF 요약 없이 edit tool을 호출하면, tool 실행 전에 재시도 지침을 주입한다. 한국어 요청에서는 RF 항목 목록과 함께 한국어 가이드를 제공한다.
   - 추가 수정: 모델이 plan/read_file 같은 inspect tool을 먼저 호출하느라 자체 요약을 생략해도 사용자가 검토 결과를 볼 수 있도록, pre-fix review 완료 직후 runtime이 deterministic `검토 결과:` / `Review findings:` assistant 메시지를 직접 emit하고 세션에 저장한다.
   - 회귀 테스트: `TestAgentRetriesEditToolWithoutPreFixReviewSummary`, `TestPreFixVisibleReviewSummaryRequiresStructuredFindingID`.
   - 검증: `go test ./cmd/kernforge -run "TestAgentRetriesEditToolWithoutPreFixReviewSummary|TestPreFixVisibleReviewSummaryRequiresStructuredFindingID|TestReviewScopeDiscoveryRejectsSyntheticToolPaths|TestReviewScopeDiscoveryNormalizesBeforeAfterDiffPaths|TestReviewScopeDiscoveryKeepsRealWebDirectoryPaths|TestAgentBlocksNamespacedWebResearchForLocalCodeRepairWithoutMCPCatalog|TestAgentRetriesEnglishToolNarrationForKoreanLocalCodeRepair" -count=1 -timeout 2m` 통과.
36. 사용자 smoke - high finding이 warning으로 남고 pre-write verification warning이 편집을 반복 차단하는 문제 수정
   - 발견: 한국어 로컬 코드 수리 smoke에서 `std::mismatch` 범위 초과 같은 high/stability finding이 `approved_with_warnings`로만 표시됐다. 이후 pre-write review는 `빌드 검증이 생략되었습니다` 같은 edit 이후 검증성 warning도 actionable warning처럼 취급해 같은 edit proposal을 반복 수정하게 만들었다.
   - 영향: 명시적 "검토하고 버그를 수정해" 흐름에서는 high-severity correctness/stability finding이 repair gate를 막아야 하는데 warning처럼 보였다. 반대로 pre-write 단계에서는 아직 적용되지 않은 patch의 빌드 검증을 요구하며 edit loop가 불필요하게 늘어났다.
   - 수정: 명시적 fix/pre-fix/pre-write/live-fix 흐름에서는 complete high model finding이 evidence/test gap이 아닌 한 gate blocker가 되도록 했다. read-only source/performance analysis에서는 기존처럼 high finding을 warning으로 유지한다.
   - 추가 수정: pre-write warning block 분류가 category에 상관없이 순수 build/test verification gap 문구를 먼저 감지해 non-blocking warning으로 둔다. 구현 증거, accessor, declaration, requested API 누락처럼 실제 patch 내용 보완이 필요한 warning은 계속 차단한다.
   - 회귀 테스트: `TestHighModelFindingBlocksWhenUserAskedToFix`, `TestHighModelFindingDoesNotBlockReadOnlyAnalysis`, `TestPreWriteReviewDoesNotBlockBuildVerificationWarningWithWrongCategory`, 기존 pure verification/implementation evidence gap tests.
   - 검증: `go test ./cmd/kernforge -run "TestPreWriteReviewDoesNotBlockPureVerificationWarning|TestPreWriteReviewDoesNotBlockBuildVerificationWarningWithWrongCategory|TestPreWriteReviewBlocksImplementationEvidenceGapEvenWhenVerificationMentioned|TestHighModelFindingBlocksWhenUserAskedToFix|TestHighModelFindingDoesNotBlockReadOnlyAnalysis" -count=1` 통과.
37. 사용자 smoke - openai-codex Responses provider에서 orphan tool output 400 오류 수정
   - 발견: 메인 모델을 `openai-codex-subscription / gpt-5.5`로, 리뷰 모델을 `anthropic-claude-cli / opus`로 바꾼 smoke에서 `update_plan` tool result가 다음 OpenAI Codex Responses 요청의 첫 `function_call_output`으로 들어갔다. 대응하는 `function_call` item이 같은 input에 없어 API가 `No tool call found for function call output` 400 오류를 반환했다.
   - 영향: 세션 압축, provider 전환, tool-turn 복구 과정에서 assistant tool call 원본이 누락되면 openai-codex Responses 경로가 대화를 이어가지 못한다. 일반 OpenAI chat-completions 경로에는 orphan tool result 보호가 있었지만 Codex Responses payload builder에는 같은 보호가 없었다.
   - 수정: `buildOpenAICodexInput`이 메시지를 직렬화하기 전에 `ensureOpenAIToolCallResponses`를 통과하도록 했다. 매칭 assistant tool call이 없는 saved tool result는 Codex의 `remove_orphan_outputs`처럼 제거하고, assistant tool call 뒤 tool result가 빠진 경우에는 Codex의 missing-output 보정처럼 `aborted` synthetic tool output을 추가한다. runtime guidance가 바로 뒤따르는 경우에는 기존처럼 superseded `NOTICE`를 넣는다.
   - 추가 수정: post-change diff review는 final-answer coding harness 상태를 evidence나 deterministic blocker로 가져오지 않는다. `coding_harness` finding은 final answer/completion audit gate에서 계속 사용하지만, 이미 적용된 코드 diff를 검토하는 post-change review에서는 patch transaction, diff, source excerpt, verification state만 기준으로 삼는다.
   - 회귀 테스트: `TestBuildOpenAICodexRequestBodyConvertsOrphanToolOutputToUserContext`, `TestBuildOpenAICodexRequestBodySynthesizesMissingToolOutput`, 기존 `TestBuildOpenAICodexRequestBodyPreservesToolContext`.
   - 검증: `go test ./cmd/kernforge -run "TestBuildOpenAICodexRequestBodyPreservesToolContext|TestBuildOpenAICodexRequestBodyConvertsOrphanToolOutputToUserContext|TestBuildOpenAICodexRequestBodySynthesizesMissingToolOutput" -count=1` 통과.
38. 사용자 smoke - Claude Code CLI review model 선택지에 버전 모델 표시
   - 발견: `/review models primary`에서 `anthropic-claude-cli`를 선택하면 모델 목록이 `sonnet`, `opus`, `haiku` family alias만 보여 실제 버전 세대를 알 수 없었다.
   - 영향: 사용자가 review model을 고를 때 현재 선택이 Sonnet/Opus의 어떤 버전인지 판단하기 어렵고, main/review 모델 조합을 실험할 때 재현성이 떨어진다.
   - 수정: Claude Code CLI 모델 목록은 `Claude Sonnet 4.7 (CLI alias)`, `Claude Opus 4.7 (CLI alias)`처럼 현재 세대 표시를 유지하되, 실제 선택 ID와 CLI `--model` 값은 `sonnet`, `opus`, `haiku` alias를 사용한다. 기존 설정에 `claude-sonnet-4-7`처럼 versioned ID가 남아 있어도 실행 전 alias로 매핑한다.
   - 회귀 테스트: `TestClaudeCLIModelChoicesShowCurrentVersionsWithSafeAliases`, `TestBuildClaudeCLIArgsMapsVersionedBuiltinsToAliases`.
   - 검증: `go test ./cmd/kernforge -run "TestClaudeCLIModelChoicesIncludeCurrentCustomModel|TestClaudeCLIModelChoicesShowCurrentVersionsWithSafeAliases|TestBuildClaudeCLIArgsUsesModelConfigOverride|TestBuildClaudeCLIArgsMapsVersionedBuiltinsToAliases" -count=1` 통과.
39. 사용자 smoke - medium repair finding과 pre-write style warning gate 조정
   - 발견: `검토하고 버그를 수정해` 흐름에서 `wcslen` 언더플로우, 개별 볼륨 실패로 전체 열거 중단 같은 medium correctness/stability finding이 `approved_with_warnings`로 남아 repair loop가 optional처럼 보였다. 반대로 pre-write 단계의 Allman/들여쓰기 warning은 실제 패치가 쓰이기 전에 고쳐야 하는데 diff preview까지 진행됐다.
   - 영향: 사용자가 명시적으로 수리를 요청한 경우 medium급 실제 버그를 놓치고, pre-write가 낮은 severity style 문제를 edit 이후로 미루는 UX가 생겼다.
   - 수정: 명시적 repair intent에서는 actionable medium correctness/stability/performance finding도 gate blocker로 올린다. 수정 전 리뷰의 low style/formatting/maintainability finding은 warning으로 유지하지만, pre-write review에서 patch-local Allman brace, indentation, formatting 문제가 나오면 severity가 low여도 write 전에 차단한다.
   - 회귀 테스트: `TestMediumModelFindingBlocksWhenUserAskedToFix`, `TestLowStyleFindingDoesNotBlockPreFixGate`, `TestHighStyleFindingDoesNotBlockPreFixGateUnlessExplicitBlocker`, `TestPreWriteReviewBlocksLowStyleWarning`.
   - 검증: `go test ./cmd/kernforge -run "TestMediumModelFindingBlocksWhenUserAskedToFix|TestLowStyleFindingDoesNotBlockPreFixGate|TestHighStyleFindingDoesNotBlockPreFixGateUnlessExplicitBlocker|TestPreWriteReviewBlocksLowStyleWarning|TestPreWriteReviewDoesNotBlockPureVerificationWarning|TestPreWriteReviewDoesNotBlockBuildVerificationWarningWithWrongCategory|TestPreWriteReviewBlocksImplementationEvidenceGapEvenWhenVerificationMentioned|TestHighModelFindingBlocksWhenUserAskedToFix|TestHighModelFindingDoesNotBlockReadOnlyAnalysis" -count=1` 통과.
40. 사용자 smoke - Claude Code CLI reviewer 실패가 승인처럼 보이는 문제 수정
   - 발견: review role을 `anthropic-claude-cli / claude-sonnet-4-7`로 설정하면 Claude CLI가 exit status 1로 실패했지만, pre-fix review는 `RF-PREFIX-001` warning만 남기고 `approved_with_warnings`로 진행했다. pre-write review도 같은 reviewer 실패 뒤 diff preview까지 열렸다.
   - 영향: 별도 reviewer가 실제로 코드를 검토하지 못했는데도 pre-write edit 흐름이 승인된 것처럼 보이며, 잘못된 모델 ID나 broken route가 조용히 묻힌다. 반대로 pre-fix에서 별도 reviewer 실패를 hard stop으로 처리하면 main model이 이미 만든 usable finding까지 버려 repair가 불필요하게 멈춘다.
   - 수정: main-first review 구조로 바꿨다. pre-fix와 일반 `/review`는 active main model의 1차 리뷰를 기준으로 finding을 보고하고, 별도 reviewer 실패는 degraded cross-reviewer 상태로 남긴다. pre-write에서는 reviewer 실패를 deterministic `RF-REVIEWER-001` blocker로 유지해 write 전에 멈춘다.
   - 회귀 테스트: `TestPreFixReviewModelFailureDegradesButKeepsMainFirstRepairGate`, `TestPreWriteReviewModelFailureBlocksEditGate`.
   - 검증: `go test ./cmd/kernforge -run "TestPreFixReviewModelFailureDegradesButKeepsMainFirstRepairGate|TestPreWriteReviewModelFailureBlocksEditGate|TestClaudeCLIModelChoicesShowCurrentVersionsWithSafeAliases|TestBuildClaudeCLIArgsMapsVersionedBuiltinsToAliases" -count=1` 통과.
41. 사용자 smoke - pre-fix review 결과가 plan/read_file 이후에도 보이지 않는 문제 수정
   - 발견: `openai-codex-subscription / gpt-5.5` main model과 `anthropic-claude-cli / sonnet` review model 조합에서 pre-fix review는 RF-002/RF-003 blocker를 찾았지만, implementation loop는 `update_plan`, `read_file`, `apply_patch`로 진행했고 사용자 가시 assistant 본문에는 검토 결과 요약이 안정적으로 나타나지 않았다.
   - 영향: pre-fix progress에는 finding이 보이지만 assistant 본문이 비어 있거나 tool-only 흐름이면 사용자는 모델이 어떤 리뷰 결과를 근거로 수리하는지 확인하기 어렵다. 모델별 tool-call 성향에 UX 계약이 흔들린다.
   - 수정: `maybeRunReviewBeforeFix`가 review 완료 직후 `formatPreFixVisibleReviewSummary`로 RF 항목과 조치 방향을 deterministic assistant 메시지로 emit/store한다. 이후 edit guard는 현재 assistant 응답뿐 아니라 세션에 저장된 visible summary도 인정해, 이미 보인 요약 때문에 불필요한 retry 루프가 생기지 않게 했다.
   - 회귀 테스트: `TestReviewBeforeFixAddsReviewFeedbackBeforeImplementation`, `TestAgentDoesNotRetryEditAfterStoredPreFixVisibleReviewSummary`, 기존 `TestAgentRetriesEditToolWithoutPreFixReviewSummary`, `TestPreFixVisibleReviewSummaryRequiresStructuredFindingID`.
   - 검증: `go test ./cmd/kernforge -run "TestReviewBeforeFixAddsReviewFeedbackBeforeImplementation|TestAgentRetriesEditToolWithoutPreFixReviewSummary|TestPreFixVisibleReviewSummaryRequiresStructuredFindingID|TestAgentDoesNotRetryEditAfterStoredPreFixVisibleReviewSummary" -count=1` 통과.
42. 사용자 smoke - low effort DeepSeek reviewer가 weak/no-actionable 결과로 repair를 통과시키는 문제 수정
   - 발견: review role을 `DeepSeek / deepseek-v4-pro / effort=low`로 바꾼 뒤 focused sample pre-fix review가 2회 모델 호출 후에도 `품질=weak`, `RF-PREFIX-001: Pre-fix review returned no actionable bug findings`만 반환했다. 그럼에도 gate가 `approved_with_warnings`로 끝나 구현 모델이 긴 독자 탐색과 patch 흐름을 이어갔다.
   - 영향: "검토하고 버그를 수정해" 흐름에서 별도 reviewer가 실질적인 검토를 못 하면 승인 비슷하게 보이거나, 반대로 hard stop이 걸려 main model의 1차 finding 기반 repair도 시작하지 못한다. pre-write review가 weak reviewer output을 `경고=0` 완료로 보여 diff preview까지 열릴 수 있는 문제는 여전히 막아야 한다.
   - 수정: focused pre-fix bug-hunt review는 role 설정이 `low` 또는 `medium`이어도 최소 `high` effort로 올린다. 이미 `xhigh`이면 그대로 유지한다. pre-fix의 weak cross reviewer는 degraded 상태로 남기고 main-first finding을 기준으로 repair를 이어가며, pre-write의 weak reviewer만 failed reviewer와 동일한 `RF-REVIEWER-001` blocker로 write를 막는다.
   - 회귀 테스트: `TestFocusedPreFixBugHuntRaisesRoleEffortToHigh`, `TestPreFixWeakReviewModelQualityDegradesWithoutBlockingMainFirstRepair`, `TestPreWriteWeakReviewModelQualityBlocksEditGate`, 기존 reviewer failure tests.
   - 검증: `go test ./cmd/kernforge -run "TestFocusedPreFixBugHuntRaisesRoleEffortToHigh|TestPreFixWeakReviewModelQualityDegradesWithoutBlockingMainFirstRepair|TestPreWriteWeakReviewModelQualityBlocksEditGate|TestPreFixReviewModelFailureDegradesButKeepsMainFirstRepairGate|TestPreWriteReviewModelFailureBlocksEditGate" -count=1` 통과.
43. 사용자 smoke - reviewer role을 처음부터 high 이상으로 실행
   - 발견: focused pre-fix bug-hunt에서는 low/medium reviewer를 high로 올리지만, 일반 review role 설정과 reviewer client 생성 경로는 여전히 provider 기본 `low` 또는 main `low` effort를 상속할 수 있었다.
   - 영향: 사용자가 review model을 새로 선택하거나 기존 low/medium 설정을 재사용하면 첫 reviewer 요청부터 약한 reasoning budget으로 시작해 weak/no-actionable 결과가 반복될 수 있다.
   - 수정: common review role의 기본 reasoning effort를 최소 `high`로 바꾸고, `/review models` 저장, review role 실행, reviewer client 생성, status label 계산에서 저장된 `low`/`medium`도 runtime 최소 `high`로 승격한다. `xhigh`는 그대로 보존한다. main/analysis/specialist target의 기존 `low` 기본값은 유지한다.
   - 회귀 테스트: `TestReviewRoleReasoningEffortDefaultsToAtLeastHigh`, `TestReviewModelsCommandDefaultsRoleEffortToHigh`, 기존 focused pre-fix/weak reviewer tests.
   - 검증: `go test ./cmd/kernforge -run "TestReviewRoleReasoningEffortDefaultsToAtLeastHigh|TestFocusedPreFixBugHuntRaisesRoleEffortToHigh|TestPreFixSecurityReviewUsesSingleFallbackRole|TestReviewModelsCommandDefaultsRoleEffortToHigh|TestReviewModelsCommandShortFormPersistsRole|TestSyncClientFromConfigKeepsOpenAICodexReviewerEffortPerTarget|TestCreateReviewerClientUsesReviewerReasoningEffort" -count=1` 및 `go test ./cmd/kernforge -run "TestPreFixWeakReviewModelQualityBlocksRepairGate|TestPreWriteWeakReviewModelQualityBlocksEditGate|TestPreFixReviewModelFailureBlocksRepairGate|TestPreWriteReviewModelFailureBlocksEditGate" -count=1` 통과.
44. 사용자 smoke - usable finding인데 summary의 omission 문구 때문에 엄격 리뷰가 반복되는 문제 수정
   - 발견: Sonnet reviewer가 완성된 structured finding을 반환했는데도 summary/prose에 `omitted`류 문구가 포함되면 runtime이 "생략/잘림 징후"로 판단해 strict review를 다시 실행했다.
   - 영향: pre-fix 또는 pre-write review가 이미 usable finding을 확보했는데도 reviewer 호출이 2배 이상 늘고, Sonnet/DeepSeek 조합에서는 긴 대기와 반복 edit/review 루프로 보였다.
   - 수정: omission retry 조건을 raw 전체 문자열이 아니라 structured finding 기준으로 좁혔다. finding 자체가 omitted placeholder이거나, partial finding 필드에 생략 표식이 있거나, weak output에 raw omission marker가 있을 때만 재시도한다. usable structured finding이 있으면 prose summary의 omission marker는 재시도 사유가 아니다.
   - 회귀 테스트: `TestReviewModelDoesNotRetryUsableFindingsForRawOmissionMarker`, 기존 omission/cut-off retry tests.
   - 검증: `go test ./cmd/kernforge -run "TestReviewModelDoesNotRetryUsableFindingsForRawOmissionMarker|TestReviewModelRetriesOmittedFindingOutput|TestReviewModelRetriesCutOffFindingOutput|TestReviewModelOmissionRetryFailureMarksRunDegraded|TestReviewProviderBehaviorControlsOmissionRetryBudget" -count=1` 통과.
45. 사용자 smoke - DeepSeek reviewer가 빈 응답을 반환했는데 weak review처럼 보이는 문제 수정
   - 발견: DeepSeek review role이 약 4분 대기 후 empty response를 반환했고, artifact의 `raw_primary_reviewer.md`도 `(empty review response)`뿐이었다. 하지만 progress와 report는 `status=completed quality=weak`로 표시해 reviewer route 장애인지 구조화 finding 부족인지 구분하기 어려웠다.
   - 영향: 실제 코드를 검토하지 않은 빈 응답이 "약한 리뷰 결과"처럼 보이고, 사용자는 모델 선택 문제와 review parser 문제를 혼동할 수 있다.
   - 수정: reviewer response body가 비어 있으면 raw artifact를 남긴 뒤 해당 reviewer run을 `failed`, `quality=failed`, `error=review model returned empty response`로 분류한다. pre-fix에서는 main-first review result를 유지하면서 cross reviewer 실패로 degraded 표시만 남기고, pre-write에서는 `RF-REVIEWER-001` insufficient_evidence blocker로 중단한다.
   - 회귀 테스트: `TestPreFixEmptyReviewModelResponseDegradesWithoutBlockingMainFirstRepair`, `TestPreWriteEmptyReviewModelResponseBlocksAsReviewerFailure`.
   - 검증: `go test ./cmd/kernforge -run "TestPreFixEmptyReviewModelResponseDegradesWithoutBlockingMainFirstRepair|TestPreWriteEmptyReviewModelResponseBlocksAsReviewerFailure|TestPreFixWeakReviewModelQualityDegradesWithoutBlockingMainFirstRepair|TestPreWriteWeakReviewModelQualityBlocksEditGate|TestPreFixReviewModelFailureDegradesButKeepsMainFirstRepairGate|TestPreWriteReviewModelFailureBlocksEditGate" -count=1` 통과.

46. 사용자 smoke - `/review`와 수정 전 리뷰를 main-first + cross-reviewer second 구조로 전환
   - 발견: 사용자가 명시적으로 `/review` 또는 "검토하고 버그를 수정해"를 요청했을 때 별도 reviewer를 먼저 실행하면, DeepSeek처럼 느리거나 weak/empty output을 내는 route가 전체 review를 막거나 구현 모델이 긴 독자 탐색으로 빠질 수 있었다. 반대로 reviewer가 없거나 실패해도 active main model은 이미 로컬 evidence를 보고 1차 판단을 만들 수 있다.
   - 영향: review model 선택 하나가 review-first repair UX 전체의 신뢰성과 속도를 좌우하고, `/review` 결과가 "메인 모델의 검토 + 독립 검증"이 아니라 "외부 reviewer 단독 판단"처럼 동작했다.
   - 수정: `executeReviewModelRuns`를 main-first로 바꿨다. active main model이 `primary_reviewer`로 1차 structured review를 만들고, 별도 role reviewer가 있으면 `cross_reviewer`로 같은 evidence와 primary draft를 받아 독립 재검토한다. pre-fix와 일반 review에서 cross reviewer 실패/weak/empty는 degraded warning으로 남기고, pre-write에서는 hard gate로 유지한다.
   - 회귀 테스트: `TestDistinctReviewModelProgressIsExplicit`, `TestPreFixReviewModelFailureDegradesButKeepsMainFirstRepairGate`, `TestPreFixWeakReviewModelQualityDegradesWithoutBlockingMainFirstRepair`, `TestPreFixEmptyReviewModelResponseDegradesWithoutBlockingMainFirstRepair`, `TestAgentContinuesAfterWeakPreFixCrossReviewerAndStillBlocksWebResearch`, 기존 pre-write reviewer failure tests.
   - 검증: `go test ./cmd/kernforge -run "TestDistinctReviewModelProgressIsExplicit|TestPreFixReviewModelFailureDegradesButKeepsMainFirstRepairGate|TestPreFixWeakReviewModelQualityDegradesWithoutBlockingMainFirstRepair|TestPreFixEmptyReviewModelResponseDegradesWithoutBlockingMainFirstRepair|TestAgentContinuesAfterWeakPreFixCrossReviewerAndStillBlocksWebResearch|TestAgentStopsAfterPreWriteReviewerFailureWithoutWebResearchRetry|TestPreWriteReviewModelFailureBlocksEditGate|TestPreWriteWeakReviewModelQualityBlocksEditGate|TestPreWriteEmptyReviewModelResponseBlocksAsReviewerFailure" -count=1` 통과.

47. 사용자 smoke - focused review가 작은 범위에도 20분 이상 걸리는 문제 수정
   - 발견: `@SampleApp/SampleWorker/PathConverter.cpp:132-221 검토하고 버그를 수정해`처럼 수십 줄을 보는 요청에서도 evidence budget이 60k로 유지되고, pre-write review가 diff보다 넓은 주변 context를 계속 싣고, DeepSeek cross-reviewer가 4~5분 응답 후 strict retry까지 반복했다. 사용자는 메인 모델, 리뷰 모델, 게이트 병합이 어느 단계인지 알 수 없고 긴 대기를 중단할 기준도 없었다.
   - 영향: 작은 로컬 코드 수리도 main review, cross review, strict retry, pre-write review, cross retry가 누적되어 20분 이상 걸릴 수 있다. 빈 응답이나 weak cross reviewer가 반복될 때도 하네스가 같은 비용을 다시 쓰므로 실제 병목이 provider route인지 patch 문제인지 구분하기 어렵다.
   - 수정: focused/range review는 기본 evidence budget을 20k로 줄이고, prompt evidence도 focused/pre-write 별도 상한을 사용한다. pre-write review는 `ProvidedDiff`, edit proposal, pre-fix repair findings를 우선하는 diff-first 예산으로 실행한다. focused/pre-write cross reviewer에는 기본 3분 soft timeout을 걸고, 리뷰 모델이 active main model보다 낮은 성능으로 판정되면 자동으로 5분까지 늘린다. broad DeepSeek cross reviewer도 4분 soft timeout으로 끊는다. DeepSeek omission retry budget은 1회로 낮췄다. progress에는 `Review phase 1/2`, `Review phase 2/2`, context mode, evidence chars, retry budget, soft timeout을 모델 호출 전에 출력한다.
   - 회귀 테스트: `TestFocusedLineRangeReviewUsesFastContextBudget`, `TestPreWriteReviewUsesDiffFirstContextBudget`, `TestFocusedCrossReviewerUsesSoftTimeoutBudget`, `TestFocusedCrossReviewerKeepsDefaultSoftTimeoutWhenNotLowerPerformance`, `TestPreWriteCrossReviewerUsesLongerSoftTimeoutForLowerPerformanceModel`, `TestDistinctReviewModelProgressIsExplicit`, `TestReviewProviderBehaviorControlsOmissionRetryBudget`, 기존 pre-write final summary tests.
   - 검증: `go test ./cmd/kernforge -run "TestDistinctReviewModelProgressIsExplicit|TestFocusedLineRangeReviewUsesFastContextBudget|TestPreWriteReviewUsesDiffFirstContextBudget|TestFocusedCrossReviewerUsesSoftTimeoutBudget|TestFocusedCrossReviewerKeepsDefaultSoftTimeoutWhenNotLowerPerformance|TestPreWriteCrossReviewerUsesLongerSoftTimeoutForLowerPerformanceModel|TestReviewModelLongWaitProgressExplainsCrossHandoff|TestReviewModelRetryProgressIncludesAttemptBudget|TestReviewProviderBehaviorControlsOmissionRetryBudget|TestReviewProviderBehaviorCapsReviewTokens|TestPreWriteFinalReviewProgressMentionsDiffPreview|TestPreWriteRunStoresRepairFindingsForFinalSummary|TestAgentRunsPreWriteReviewBeforePreviewAndWrite|TestSameModelReviewProgressShowsScopeEvidenceAndRequest|TestReviewMCPRunUsesMainFirstAndResponseIncludesReviewerRuns"` 통과.

48. 사용자 smoke - DeepSeek optional cross retry 반복, 웹 검색 재시도, 최종 리뷰 출력 생략 개선
   - 발견: focused sample repair smoke에서 메인 모델 1차 리뷰가 이미 actionable finding을 반환했는데도 DeepSeek cross reviewer가 weak/omission 의심으로 strict retry를 반복했다. 같은 턴에서 edit target mismatch 이후 모델이 Microsoft Learn 웹 검색을 시도했고, 검색 API key 실패나 fetch 대기로 로컬 코드 수리 흐름이 다시 길어졌다. pre-write final review 본문은 diff preview 전에 보이기 시작했지만 일부 긴 evidence/impact/fix/test 필드가 `...`로 잘려 실제 조치 기준을 끝까지 확인하기 어려웠다.
   - 영향: 별도 cross reviewer가 보조 검증 역할인데도 provider 지연과 strict retry가 전체 repair loop의 병목이 됐다. 로컬 코드 수리에서 웹 검색이 열리면 API key 오류와 외부 fetch 대기가 섞여 patch 작성 전에 또 다른 긴 루프가 생긴다. 최종 리뷰 내용이 축약되면 사용자는 diff preview 승인 전에 어떤 RF가 확인됐고 어떤 항목이 남았는지 충분히 판단하기 어렵다.
   - 수정: 선택적 cross review이고 pre-write hard gate가 아닌 경우, DeepSeek cross output에 omission 의심이 있더라도 메인 1차 리뷰가 usable/actionable finding을 이미 제공했고 reviewer stop reason이 명시적 token-limit/truncation이 아니면 strict retry를 생략한다. `length`, `max_tokens`, `partial`, `truncated` 같은 stop reason은 여전히 strict retry 대상이다. 로컬 코드 리뷰/수리에서는 MCP catalog가 비어 있어도 web/search/browser 계열 tool을 숨기고 실행 전에 차단하며, 차단 시 모델이 무엇을 검색하려 했는지 query/url intent를 progress에 남긴다. pre-write final visible summary와 progress 요약은 full finding detail을 사용해 evidence, impact, required fix, test recommendation tail을 `...`로 자르지 않는다.
   - 회귀 테스트: `TestDeepSeekOptionalCrossSkipsOmissionRetryWhenMainReviewIsActionable`, `TestDeepSeekOptionalCrossRetriesExplicitTokenLimitStop`, `TestPreWriteFinalVisibleReviewSummaryDoesNotEllipsizeDetails`, `TestSummarizeToolInvocationWebResearchIncludesIntent`, `TestAgentKeepsWebResearchHiddenAfterEditTargetMismatch`, 기존 omission retry, local web block, pre-write final summary tests.
   - 검증: `go test ./cmd/kernforge -run "Test(SystemPromptDoesNotSuggestWebResearchForLocalCodeRepair|SummarizeToolInvocationWebResearchIncludesIntent|AgentBlocksLocalInspectionBeforeWebResearchWhenCapabilityAvailable|AgentBlocksWebResearchForLocalCodeRepair|AgentBlocksNamespacedWebResearchForLocalCodeRepairWithoutMCPCatalog|AgentBlocksWebResearchAfterPreWriteReviewFeedback|AgentKeepsWebResearchHiddenAfterEditTargetMismatch|AgentContinuesAfterWeakPreFixCrossReviewerAndStillBlocksWebResearch|AgentStopsAfterPreWriteReviewerFailureWithoutWebResearchRetry|FocusedCrossReviewerUsesSoftTimeoutBudget|ReviewModelLongWaitProgressExplainsCrossHandoff|ReviewModelRetriesOmittedFindingOutput|ReviewModelDoesNotRetryUsableFindingsForRawOmissionMarker|DeepSeekOptionalCrossSkipsOmissionRetryWhenMainReviewIsActionable|DeepSeekOptionalCrossRetriesExplicitTokenLimitStop|ReviewModelRetriesCutOffFindingOutput|ReviewModelOmissionRetryFailureMarksRunDegraded|PreWriteFinalReviewProgressMentionsDiffPreview|PreWriteFinalVisibleReviewSummaryDoesNotEllipsizeDetails|ReviewProviderBehaviorControlsOmissionRetryBudget)$" -count=1 -timeout 10m` 및 `go test ./cmd/kernforge -count=1 -timeout 20m` 통과.

49. 사용자 smoke - pre-write reviewer 실패 시 메인 리뷰 결과 기반 사용자 승인 fallback
   - 발견: pre-write main-first review가 usable finding과 수정 확인 내용을 만들었는데도 DeepSeek cross reviewer가 soft timeout으로 실패하면 `RF-REVIEWER-001` 때문에 edit loop가 중단됐다. 최종 응답은 reviewer route를 바꾸거나 재실행하라고만 안내해서, 사용자는 이미 표시된 메인 모델 리뷰 결과를 기준으로 diff preview에서 직접 판단할 수 있는지 알 수 없었다.
   - 영향: broken/slow reviewer route가 write gate를 막는 정책 자체는 맞지만, usable main review가 있는 상태에서도 사용자가 수동으로 위험을 인지하고 진행할 수 있는 명확한 경로가 없었다. 반대로 자동 fallback을 허용하면 reviewer gate의 안전성이 약해지고 비대화형 환경에서 파일이 바로 쓰일 수 있다.
   - 수정: pre-write reviewer gate unavailable 응답에 usable main reviewer가 있으면 `메인 모델 리뷰 기준으로 진행` 선택지를 출력한다. 사용자가 이 문구로 명시 승인하고 interactive diff preview가 가능한 경우에만 `main_only_fallback` reviewer gate policy를 기록해 다음 pre-write run에서 cross reviewer 실패를 degraded evidence로 남기되 hard blocker로는 보지 않는다. diff preview가 없으면 fallback 요청이 있어도 hard stop을 유지한다.
   - 회귀 테스트: `TestPreWriteMainOnlyFallbackPolicyDoesNotTreatCrossFailureAsHardGate`, `TestReviewerGateUnavailableReplyOffersMainModelFallback`, `TestPreWriteMainOnlyFallbackApprovalPhraseRequiresUsableMainReview`, 기존 pre-write reviewer failure/web-research stop tests.
   - 검증: `go test ./cmd/kernforge -run "TestPreWriteReviewModelFailureBlocksEditGate|TestPreWriteMainOnlyFallbackPolicyDoesNotTreatCrossFailureAsHardGate|TestReviewerGateUnavailableReplyOffersMainModelFallback|TestPreWriteMainOnlyFallbackApprovalPhraseRequiresUsableMainReview|TestPreWriteWeakReviewModelQualityBlocksEditGate|TestAgentStopsAfterPreWriteReviewerFailureWithoutWebResearchRetry" -count=1 -timeout 10m` 통과.

50. 사용자 smoke - Codex App식 build verification command lifecycle 반영
   - 발견: artifact를 만들 수 있는 `run_shell` build/test 명령이 사용자 확인에서 거절되면 출력은 "skipped"였지만 tool call 자체는 non-error `success=true`로 저장됐다. 일부 하네스 경로는 `verification_like=true`만 보고 pending verification을 해소하거나 성공 evidence로 오판할 수 있었다.
   - 영향: 사용자가 빌드 검증을 거절했는데도 최종 답변/리뷰 하네스가 검증이 끝난 것처럼 보일 수 있고, 반대로 실제로 필요한 미검증 gap이 사라져 GPT 단일 모델 smoke에서 회귀가 반복될 수 있다.
   - 수정: `F:\kernullist\codex`의 command execution 승인 수명주기처럼 verification-like shell command 결과에 `verification_status`와 `command_execution_status`를 명시적으로 기록한다. 거절된 build/test는 `verification_status=skipped`, `command_execution_status=declined`, `verification_evidence=false`로 남기고, pending verification 해소와 성공 evidence 계산에서 제외한다. background verification 시작은 `verification_status=pending`, `verification_evidence=false`로 남겨 성공 evidence처럼 계산하지 않는다. skipped-only `VerificationReport`도 더 이상 성공 evidence가 아니다.
   - 회귀 테스트: `TestRunShellDeclinedVerificationDetailedStatusIsSkipped`, `TestDeclinedVerificationDoesNotClearPendingCheckOrCountAsEvidence`, `TestSkippedVerificationReportIsNotSuccessfulEvidence`, 기존 verification claim/disclosure tests.
   - 검증: `go test ./cmd/kernforge -run "TestAssessShellCommandMutationClassifiesVerificationArtifactCommands|TestRunShellVerificationCommandRequiresConfirmation|TestRunShellDeclinedVerificationDetailedStatusIsSkipped|TestSkippedVerificationOutputIsNotSuccessfulEvidence|TestManualVerificationClearsPendingCheck|TestDeclinedVerificationDoesNotClearPendingCheckOrCountAsEvidence|TestSkippedVerificationReportIsNotSuccessfulEvidence|TestPreFinalHarnessBlocksVerificationClaimWithoutEvidence|TestDiffAwareHarnessBlocksKoreanBuildPassClaimWithoutEvidence|TestDiffAwareHarnessAllowsKoreanBuildSkippedDisclosure" -count=1 -timeout 4m` 통과.

### 16.10 Review Harness 85점 이상 목표 설계

아래 설계는 Codex App을 100점으로 둔 상대 평가에서 Kernforge review harness의 모든 주요 항목을 85점 이상으로 올리기 위한 목표 상태다. 현재 점수는 주관적 운영 평가이며, 구현 후에는 regression test, synthetic transcript replay, 실제 sample smoke run을 함께 통과해야 85점 달성으로 본다.

| 항목 | 현재 추정 | 85점 이상 기준 |
| --- | ---: | --- |
| 리뷰 구조 설계 | 84 | 모든 review surface가 하나의 typed state machine과 artifact schema를 공유하고, 예외 분기가 schema 밖으로 새지 않는다. |
| pre-fix / pre-write gate 안전성 | 82 | 쓰기 전 gate는 실패를 안전하게 멈추되, usable main review 기반 fallback은 사용자 승인과 diff preview가 있을 때만 작동한다. |
| finding 출력 품질 / 로그 | 80 | 사용자가 모델 간 handoff, verdict, remaining risk, next action을 transcript만 보고 판단할 수 있다. |
| 로컬 코드 evidence 중심성 | 78 | `@path`, diff, pre-write feedback, edit mismatch 이후에도 외부 web/search로 빠지지 않고 local evidence만으로 복구한다. |
| 회귀 테스트 커버리지 | 76 | 주요 failure mode가 table-driven replay로 고정되고, smoke log에서 나온 회귀가 unit/integration test로 환원된다. |
| 모델 라우팅 안정성 | 68 | provider/model/effort별 latency, schema reliability, blocker detection quality를 분리해 route policy가 결정된다. |
| 단일 모델 리뷰 품질 | 72 | 별도 리뷰 모델이 없어도 role-separated structured self-review와 deterministic gate로 Codex App 수준의 review UX를 제공한다. |
| 속도 / 반복 루프 억제 | 64 | 작은 focused repair가 정상 provider에서 8분 안에 diff preview까지 도달하고, bad reviewer route도 bounded failure로 끝난다. |
| 제품급 견고함 | 68 | interrupted run, stale artifact, failed tool, partial patch, reviewer outage가 모두 deterministic recovery path를 가진다. |

#### 16.10.0 Codex App 기준 재검토

이 절의 목표는 Codex App 내부 구현을 복제하는 것이 아니다. 사용자가 관측하는 Codex App의 제품 행동을 100점 기준으로 삼아 KernForge 리뷰 하네스가 따라야 할 불변식을 정의하는 것이다.

Codex App 기준에서 좋은 리뷰 하네스는 다음 성질을 가진다.

1. 작업 경계가 명확하다.
   - 분석, 수정 계획, 패치 작성, 쓰기 전 리뷰, diff preview, 파일 쓰기, 검증, 최종 응답이 섞이지 않는다.
   - 각 단계는 현재 무엇을 하는지 사용자에게 보이는 로그를 남긴다.
   - 한 model response에 read-only tool과 write tool이 섞이면 write tool은 즉시 실행하지 않고 다음 isolated turn으로 미룬다.
2. 승인 경계가 명확하다.
   - 리뷰 승인과 파일 쓰기 승인은 다르다.
   - 파일 쓰기 승인과 commit/push 승인은 다르다.
   - 사용자가 commit/push를 명시하지 않으면 리뷰 하네스는 commit/push를 유도하거나 실행하지 않는다.
3. 메인 모델이 최종 책임을 진다.
   - 별도 리뷰 모델은 교차 검토자다.
   - 최종 정리, 반영 여부 판단, 사용자에게 보여줄 설명은 메인 모델 흐름으로 돌아와야 한다.
   - 리뷰 모델 실패가 있더라도 메인 모델의 1차 리뷰 결과는 숨기지 않는다.
4. 로컬 증거가 기본이다.
   - 코드 리뷰와 수정은 로컬 파일, diff, 테스트 결과, 이전 검증 이력으로 우선 판단한다.
   - 웹 검색은 명시 요청, 최신 외부 사실, API 문서 확인처럼 필요한 경우에만 수행한다.
   - 웹 검색을 수행할 때는 검색 목적과 기대하는 근거를 먼저 로그로 출력한다.
5. 실패가 사용자 선택으로 이어진다.
   - 필수 리뷰어가 timeout, empty, weak로 실패하면 단순 중단만 하지 않는다.
   - 메인 모델 리뷰가 usable이면 그 내용을 사용자에게 보여주고, 정책에 따라 같은 reviewer timeout 연장 재시도, reviewer 변경, single-model mode, main model 복구/변경 중 어떤 행동이 필요한지 출력한다.
   - 자동 모드에서는 위험도가 낮은 문서 변경과 코드 쓰기 변경을 다르게 취급한다.
6. 장시간 작업은 진행 상황이 설명된다.
   - 단순 "모델 응답 대기 중"만 반복하지 않고, 현재 단계, 경과 시간, soft timeout, retry budget, 다음 전이를 같이 출력한다.
   - 2분 이상 대기하는 모델 호출은 왜 기다리는지, 언제 실패로 전환되는지 드러나야 한다.
7. 산출물과 화면 출력이 모두 완전해야 한다.
   - markdown 보고서 경로만 남기지 않는다.
   - diff preview 전 최종 검토 결과는 보고서의 핵심 내용을 생략 없이, 다만 터미널에 맞게 구조화해서 출력한다.
   - 화면 출력에서 `...` 생략이 발생하면 그것 자체를 품질 저하로 간주하고 더 짧은 항목 단위 출력으로 재구성한다.
8. 재개와 복구가 가능해야 한다.
   - 긴 리뷰 중 compaction, 재시작, tool failure가 발생해도 현재 review session id, evidence hash, proposal id, gate result를 기준으로 이어갈 수 있어야 한다.
   - apply_patch 실패 후에는 같은 오래된 patch를 반복하지 않고, 실제 현재 파일 스냅샷 기준으로 patch를 재생성해야 한다.

이 기준으로 보면 기존 16.10 설계는 모델 라우팅과 출력 개선에는 강하지만, 다음 축을 더 보강해야 한다.

| 보강 항목 | Codex App 기준 차이 | KernForge 설계 반영 |
| --- | --- | --- |
| Action envelope | 모델 응답과 tool action의 경계가 명확함 | 모든 review/write/git 작업을 `ReviewActionEnvelope`로 기록한다. |
| Approval ledger | diff preview, write, commit/push가 별도 승인임 | `ApprovalLedger`를 두고 승인 종류를 분리한다. |
| Capability manifest | 사용 가능한 tool/connector가 명시적임 | web, model, patch, test, git capability를 session 시작 시 검증한다. |
| Fallback UX | 실패 원인이 사용자 선택으로 연결됨 | reviewer 실패 시 main review 기반 fallback prompt를 출력한다. |
| Resume metadata | 긴 작업 재개가 이전 상태를 확인함 | review session artifact에 stage, evidence hash, proposal hash를 저장한다. |
| Output completeness | 핵심 결과가 화면에 직접 출력됨 | report path와 별도로 final review body를 chunk 단위로 출력한다. |

##### 16.10.0.1 ReviewActionEnvelope

모든 리뷰 하네스 액션은 다음 envelope를 통과해야 한다.

```text
ReviewActionEnvelope
- session_id
- action_id
- action_type
  - collect_evidence
  - main_review
  - cross_review
  - merge_gate
  - propose_patch
  - pre_write_review
  - diff_preview
  - apply_write
  - verify
  - summarize
- actor
  - main_model
  - reviewer_model
  - harness
  - user
- input_refs
- output_refs
- approval_required
- approval_granted
- elapsed_ms
- status
- failure_class
```

이 envelope는 로그, markdown report, JSON artifact에 동일하게 남긴다. 이렇게 해야 "누가 무엇을 했는지"와 "어떤 결과가 다음 단계로 넘어갔는지"가 항상 추적된다.

##### 16.10.0.2 ApprovalLedger

리뷰 하네스는 승인 상태를 하나의 boolean으로 보지 않는다.

```text
ApprovalLedger
- review_gate_approved
- diff_preview_shown
- user_write_approved
- write_applied
- verification_passed
- user_commit_requested
- commit_done
- user_push_requested
- push_done
```

정책:

1. `review_gate_approved=true`는 `user_write_approved=true`를 의미하지 않는다.
2. `write_applied=true`는 `user_commit_requested=true`를 의미하지 않는다.
3. commit/push는 사용자가 명시한 경우에만 가능하다.
4. review harness 문서와 로그에는 어떤 승인이 없어서 멈췄는지 구체적으로 출력한다.

##### 16.10.0.3 CapabilityManifest

세션 시작 시 다음 capability를 감지하고 review artifact에 남긴다.

```text
CapabilityManifest
- local_file_read
- patch_apply
- diff_preview
- test_runner
- git_status
- git_commit
- git_push
- web_search
- web_fetch
- primary_model
- cross_review_model
- single_model_review_mode
- mcp_review_server
```

정책:

1. web search API key가 없으면 `web_search=unavailable`로 기록하고 모델 prompt에 "웹 검색 tool을 호출하지 말라"는 제약을 넣는다.
2. web fetch가 가능해도 모델이 임의로 외부 문서를 열기 전에 `external_lookup_intent` 로그를 출력한다.
3. patch apply가 실패하면 이전 patch를 재시도하지 않고 현재 파일 digest와 실패 hunk를 기반으로 새 proposal을 만든다.
4. cross review timeout은 provider/profile default를 우선한다. cloud API reviewer는 기본 5분, CLI/local reviewer는 기본 8분으로 시작하고, 최근 timeout health가 있으면 다음 reviewer call에만 adaptive extension을 적용한다. 이 상태는 status와 실패 메시지에서 사용자 선택지로 번역한다.

##### 16.10.0.4 Single-Model Review Mode

Codex App은 별도 reviewer가 없어도 단일 모델이 코드 검토, 수정, 쓰기 전 검토를 수행할 수 있다. KernForge도 별도 리뷰 모델이 없거나 사용자가 main-only 진행을 명시한 경우를 약식 fallback이 아니라 정식 review mode로 취급해야 한다.

원칙:

1. 단일 모델 리뷰는 "리뷰 없음"이 아니다.
   - `no_cross_review`는 cross reviewer가 없다는 뜻이지, gate가 비활성화됐다는 뜻이 아니다.
   - main model은 동일 모델이라도 phase와 prompt를 분리해 review actor와 repair actor를 구분한다.
2. 독립성 수준을 숨기지 않는다.
   - report와 visible summary에 `independence_level=single_model`을 기록한다.
   - 별도 reviewer가 없어서 남는 residual risk를 verification obligation으로 표현한다.
3. evidence를 고정한다.
   - single-model pre-write review는 이미 만들어진 `patch_proposal.diff`, pre-fix findings, local evidence manifest만 입력으로 받는다.
   - review 중에는 patch를 수정하지 않는다. 수정이 필요하면 `needs_revision`으로 돌려 repair phase를 다시 연다.
4. checklist를 강제한다.
   - correctness
   - regression risk
   - security/bypass surface
   - verification gap
   - scope creep
   - stale evidence
5. 자동 승인은 deterministic guard와 함께만 가능하다.
   - structured findings가 없거나 근거가 비어 있으면 `insufficient_evidence`.
   - patch가 pre-fix RF obligation을 해결했는지 확인하지 못하면 `needs_revision`.
   - code write는 여전히 diff preview 또는 explicit write approval 뒤에만 가능하다.

단일 모델 review flow:

```text
collect_evidence
  -> main_review
  -> no_cross_review(reason=single_model_mode)
  -> main_synthesis
  -> gate_decision
  -> action_boundary
```

검토+수정 요청에서는 다음처럼 분리한다.

```text
pre_fix_main_review
  -> repair_plan
  -> patch_proposal
  -> single_model_pre_write_review
  -> gate_decision
  -> diff_preview | repair_feedback | user_decision_required
```

`single_model_pre_write_review` prompt 제약:

1. "당신이 방금 작성한 패치"라고 표현하지 않는다. proposal artifact를 별도 작성자가 만든 diff처럼 검토한다.
2. 새 패치를 만들지 않는다.
3. approve하려면 pre-fix RF별로 "수정됨", "부분 수정", "미해결", "검증 필요" 중 하나를 선택한다.
4. `approved_with_warnings`는 code blocker가 없고 verification/test gap만 남은 경우에만 사용한다.
5. reviewer model 부재는 blocker가 아니라 `independence_note`와 `verification_obligation`으로 남긴다.

Acceptance criteria:

1. 별도 reviewer route가 없어도 `/review`, 검토 요청, 검토+수정 요청이 structured findings를 출력한다.
2. single-model pre-write review는 diff preview 전에 RF별 수정 확인 결과를 보여준다.
3. 단일 모델 모드에서 reviewer unavailable blocker가 발생하지 않는다.
4. 단일 모델 모드임을 사용자에게 숨기지 않고 residual risk와 검증 의무를 표시한다.
5. 단일 모델 모드는 commit/push 권한을 갖지 않는다.

테스트:

1. `TestSingleModelReviewModeDoesNotRequireCrossReviewer`
2. `TestSingleModelPreWriteReviewUsesFrozenDiff`
3. `TestSingleModelReviewRecordsIndependenceLevel`
4. `TestSingleModelReviewDoesNotTreatMissingCrossReviewerAsBlocker`
5. `TestSingleModelPreWriteReviewRequiresRFObligationStatus`

#### 16.10.1 공통 Review State Machine

목표:

1. review harness를 "기능 모음"이 아니라 하나의 상태 기계로 만든다.
2. pre-fix, pre-write, post-change, `/review`, MCP review, final review가 같은 상태 이름과 전이 규칙을 쓴다.
3. gate 실패, reviewer 실패, user fallback, diff preview approval, verification gap이 모두 typed transition으로 남는다.

상태:

```text
collect_evidence
  -> main_review
  -> optional_cross_review | required_cross_review | no_cross_review
  -> merge_reviews
  -> gate_decision
  -> action_boundary
  -> repair_feedback | user_fallback_offer | diff_preview | verification_required | final_summary | resume_recovery
```

설계:

1. `ReviewRun`에 `StateTransitions []ReviewStateTransition`을 추가한다.
2. transition은 `from`, `to`, `reason`, `actor`, `blocking`, `visible_to_user`, `created_at`을 가진다.
3. 현재 progress line은 이 transition에서 파생한다. 즉, 로그 문구가 상태의 원천이 아니라 상태가 로그의 원천이어야 한다.
4. `reviewRunRequiresSuccessfulReviewer`, `reviewRunRequiresSuccessfulCrossReviewer`, `ReviewerGatePolicy`는 transition guard로 이동한다.
5. hard-coded verdict 분기는 `gate_decision` 단계에만 남기고, agent edit loop는 `ReviewRun.Gate.Action`을 읽는다.
6. `action_boundary`는 gate 결과를 실제 행동으로 변환한다.
   - `approved` 또는 `approved_with_warnings`라도 바로 쓰지 않고 diff preview 또는 explicit write approval로 이동한다.
   - `needs_revision`은 메인 모델 repair loop로 이동한다.
   - `insufficient_evidence` 중 main review가 usable이면 사용자 fallback 선택지로 이동한다.
   - `reviewer_unavailable`은 모델 라우팅 변경, timeout 확장, main-only 진행 중 하나를 명시적으로 요구한다.
7. `resume_recovery`는 중간 중단 후 재개 전용 상태다.
   - 현재 파일 digest가 proposal 생성 당시와 다르면 기존 patch를 폐기한다.
   - report만 있고 proposal이 없으면 review-only 결과로 복구한다.
   - proposal은 있지만 pre-write review가 없으면 pre-write review부터 다시 시작한다.
8. `no_cross_review`는 다음 두 경우를 구분한다.
   - `reason=single_model_mode`: 별도 reviewer가 없고 main model structured review가 gate 역할을 한다.
   - `reason=policy_skipped`: optional cross review를 정책상 생략했지만 별도 reviewer route는 존재한다.
   두 경우 모두 사용자 visible summary에 표시한다.

Acceptance criteria:

1. 같은 pre-write reviewer timeout이 발생해도 응답, artifact, progress, tool error가 모두 같은 transition id를 참조한다.
2. 사용자 fallback 승인 후 다음 run에는 `user_fallback_offer -> diff_preview` 전이가 남는다.
3. final answer는 마지막 review transition을 근거로 "왜 멈췄는지" 또는 "왜 진행했는지"를 설명할 수 있다.
4. commit/push는 이 상태 기계의 기본 종착지가 아니다. 별도 git action 요청이 있을 때만 실행한다.
5. single-model mode는 `reviewer_unavailable`이 아니라 `no_cross_review(reason=single_model_mode)`로 기록된다.

테스트:

1. `TestReviewRunRecordsStateTransitionsForPreWriteReviewerFailure`
2. `TestMainOnlyFallbackRequiresPriorUserFallbackOfferTransition`
3. `TestReviewProgressLinesDeriveFromStateTransitions`

#### 16.10.2 Gate Safety 85+ 설계

목표:

1. pre-fix는 repair discovery를 막지 않고, pre-write는 write safety를 막는다.
2. failed reviewer route와 unsafe patch를 같은 "수정 필요"로 섞지 않는다.
3. 사용자가 수동 fallback을 택해도 자동 파일 쓰기는 발생하지 않는다.

설계:

1. Gate action을 verdict와 분리한다.
   - `repair_required`: 모델이 패치를 다시 작성해야 한다.
   - `reviewer_unavailable`: reviewer route 문제라 implementation retry를 금지한다.
   - `user_decision_required`: usable main review가 있으므로 사용자에게 fallback을 물을 수 있다.
   - `diff_preview_allowed`: diff preview로 이동할 수 있다.
   - `verification_required`: write 이후 검증 의무가 남는다.
2. `RF-REVIEWER-001`은 코드 finding이 아니라 `reviewer_unavailable` action으로 분류한다.
3. `main_only_fallback`은 다음 조건을 모두 만족해야 한다.
   - 직전 run이 pre-write.
   - main reviewer가 `usable` 이상.
   - cross reviewer failure가 artifact에 기록됨.
   - 사용자가 명시 승인 문구를 입력함.
   - interactive diff preview callback이 존재함.
4. fallback run은 cross failure를 숨기지 않고 `degraded=true`, `degraded_reason`에 남긴다.
5. 비대화형 MCP/automation에서는 fallback을 허용하지 않고 `next_commands`로만 안내한다.
6. commit/push는 review gate와 분리한다.
   - review gate가 approved여도 commit/push는 실행하지 않는다.
   - 사용자가 "커밋", "푸시"를 명시한 경우에만 Git action gate를 연다.
   - Git action gate는 현재 staged/unstaged 범위, branch, remote, verification 결과를 별도 로그로 보여준다.

Acceptance criteria:

1. reviewer outage는 edit retry나 web research를 유발하지 않는다.
2. fallback 승인 없이는 pre-write reviewer failure가 절대 diff preview로 가지 않는다.
3. fallback 승인 후에도 diff preview prompt 전까지 파일은 변경되지 않는다.
4. commit/push가 사용자 명시 없이 발생하지 않는다.

테스트:

1. `TestReviewerUnavailableGateActionDoesNotPrimeRepairLoop`
2. `TestMainOnlyFallbackUnavailableWithoutInteractiveDiffPreview`
3. `TestMCPPreWriteReviewerFailureReturnsNextCommandNotFallbackWrite`

#### 16.10.3 Finding 출력과 로그 85+ 설계

목표:

1. 사용자는 transcript만 보고 현재 누가 무엇을 검토 중인지 이해해야 한다.
2. 최종 review output은 markdown artifact 경로가 아니라 핵심 finding 내용을 직접 보여줘야 한다.
3. 긴 evidence, impact, required fix, test recommendation은 `...`로 숨기지 않는다.

설계:

1. 모든 review phase progress는 다음 format을 공유한다.

```text
리뷰 단계 1/2: 메인 모델 1차 리뷰. 모델=... context=... evidence_chars=... prompt_limit=... retry_budget=... soft_timeout=...
리뷰 단계 2/2: 리뷰 모델 교차 검토. 모델=... context=... evidence_chars=... prompt_limit=... retry_budget=... soft_timeout=...
```

2. 60초 이상 대기하면 long-wait progress에 현재 actor와 다음 전이를 출력한다.
3. pre-fix와 pre-write final visible summary는 같은 renderer를 사용한다.
4. visible summary는 다음 섹션을 가진다.
   - 판정
   - 차단/경고 수
   - 수정 확인 대상
   - 남은 검토 항목
   - reviewer route 상태
   - 다음 행동
5. finding renderer는 terminal 폭에 맞춰 줄바꿈만 하고 semantic truncation은 하지 않는다.
6. progress line은 compact summary를 유지하되, assistant-visible final review body는 full detail을 출력한다.

Acceptance criteria:

1. diff preview 직전에 사용자가 report 파일을 열지 않아도 승인 판단을 할 수 있다.
2. reviewer timeout이 발생하면 어떤 모델이, 어떤 역할로, 몇 분 동안 기다렸는지 보인다.
3. 같은 finding이 progress, assistant summary, markdown report에서 같은 id/title/severity를 유지한다.

테스트:

1. `TestVisibleReviewSummaryIncludesReviewerRouteStatus`
2. `TestLongWaitProgressNamesCurrentReviewActor`
3. `TestReviewFindingRendererDoesNotSemanticEllipsize`

#### 16.10.4 Local Evidence 중심성 85+ 설계

목표:

1. 로컬 코드 리뷰/수리 요청은 외부 웹 검색 없이 로컬 evidence로 끝난다.
2. 모델이 "API semantics가 필요하다"고 판단해도 먼저 repo-local wrapper, headers, docs, tests를 검색한다.
3. 웹 검색이 정말 필요하면 사용자에게 무엇을 확인하려는지 명시하고 승인된 경우에만 사용한다.

설계:

1. `LocalCodeWorkContext`를 추가한다.
   - original user request에 `@path`, file mention, diff, local symbol이 있으면 true.
   - pre-write repair feedback, patch mismatch recovery, review finding follow-up은 original context를 상속한다.
2. local code context에서는 MCP web/search/browser tool을 prompt와 tool registry 양쪽에서 숨긴다.
3. model이 web tool call을 생성하면 실행 전 차단하고 다음 정보를 progress에 남긴다.
   - tool name
   - query 또는 URL
   - 차단 이유
   - local fallback command 후보
4. Windows API 의미가 필요한 경우에도 `docs/`, `third_party/`, local comments, existing usage search를 먼저 evidence source로 넣는다.
5. 사용자가 명시적으로 "웹 검색해"라고 하면 `ExternalEvidenceRequest` transition을 만들고 검색 목적을 progress에 출력한다.
6. external lookup은 review session artifact에 다음 필드로 남긴다.
   - `external_lookup_intent`
   - `expected_source`
   - `query_or_url`
   - `result_status`
   - `used_in_finding_ids`
7. external lookup 결과가 finding에 실제로 쓰이지 않았으면 report에는 "참고하지 않음"으로 남기고 gate 판단에는 반영하지 않는다.

Acceptance criteria:

1. local repair에서 edit target mismatch 후에도 web search로 빠지지 않는다.
2. blocked web attempt는 사용자가 "모델이 무엇을 검색하려 했는지" 알 수 있게 남는다.
3. 명시적 web 승인 없이는 external URL fetch가 실행되지 않는다.
4. 같은 missing API key 오류가 반복되지 않는다.

테스트:

1. `TestLocalCodeContextPersistsAcrossPatchMismatchRecovery`
2. `TestBlockedWebToolLogsSearchIntent`
3. `TestExplicitWebResearchCreatesExternalEvidenceTransition`

#### 16.10.5 회귀 테스트 커버리지 85+ 설계

목표:

1. 사용자 smoke log에서 발견한 모든 회귀는 최소 하나의 deterministic replay test로 고정한다.
2. model/provider 실제 호출 없이도 timeout, empty, weak, omitted, malformed patch, web attempt를 재현한다.
3. broad full test만 믿지 않고 failure class별 targeted suite를 둔다.

설계:

1. `testdata/review_replay/`에 anonymized transcript fixture를 둔다.
2. fixture schema:

```json
{
  "name": "prewrite_cross_timeout_after_main_usable",
  "user_request": "...",
  "reviewer_runs": [...],
  "tool_calls": [...],
  "expected_gate": "...",
  "expected_progress_contains": [...],
  "expected_reply_contains": [...]
}
```

3. replay runner는 실제 provider 대신 scripted client와 fake clock을 사용한다.
4. category별 suite:
   - reviewer route failure
   - omission/truncation
   - patch mismatch
   - local web block
   - pre-fix repair obligations
   - final visible summary
   - MCP response contract
5. `go test ./cmd/kernforge -run TestReviewReplayFixtures`가 모든 fixture를 순회한다.

Acceptance criteria:

1. 새 사용자 smoke bug를 수정할 때 fixture가 먼저 추가된다.
2. replay fixture는 1초대 실행을 목표로 한다.
3. full `go test ./cmd/kernforge` 실패 전 targeted replay가 더 명확한 원인을 보여준다.

테스트:

1. `TestReviewReplayFixtures`
2. `TestReviewReplayFixtureRejectsMissingExpectedGate`
3. `TestReviewReplayFixtureCanModelSoftTimeoutWithoutSleeping`

#### 16.10.6 모델 라우팅 안정성 85+ 설계

목표:

1. 모델 선택은 이름 문자열이 아니라 capability profile에 의해 결정한다.
2. "강한 모델", "느린 모델", "schema를 잘 지키는 모델", "blocker를 잘 찾는 모델"을 분리해 평가한다.
3. reviewer route 실패가 계속되면 자동으로 같은 실패를 반복하지 않는다.
4. 별도 reviewer가 없는 환경에서도 단일 모델 리뷰 품질이 일정 기준 아래로 떨어지지 않는다.

설계:

1. `ReviewModelCapability`를 도입한다.
   - `provider`
   - `model_pattern`
   - `capability_rank`
   - `schema_reliability`
   - `blocker_detection_prior`
   - `latency_class`
   - `supports_reasoning_effort`
   - `recommended_timeout`
   - `retry_budget`
2. 현재 hard-coded rank는 이 profile table로 이동한다.
3. runtime은 최근 N회 reviewer run을 `ReviewRouteHealth`로 집계한다.
   - timeout rate
   - empty response rate
   - weak rate
   - usable finding rate
   - median latency
4. focused/pre-write route는 다음 순서로 timeout을 정한다.
   - explicit policy override
   - capability recommended timeout
   - provider class default: cloud API 5분, CLI/local 8분
   - route health가 timeout-heavy면 다음 call에 한해서 adaptive extension을 적용하고 fallback 안내를 강화
5. `/review models status`에서 primary/cross route health, lens 상태, 권장 변경을 보여준다.
6. reviewer route가 없으면 `SingleModelReviewPolicy`를 적용한다.
   - `enabled=true`
   - `independence_level=single_model`
   - `requires_structured_findings=true`
   - `requires_pre_write_self_review=true` for code write
   - `requires_rf_obligation_status=true` when pre-fix findings exist
   - `records_verification_obligations=true`
7. reviewer route가 있지만 route health가 repeated timeout/empty/weak 상태이면 자동으로 단일 모델 모드로 몰래 전환하지 않는다.
   - pre-fix에서는 degraded cross reviewer 상태를 표시하고 main review 기준 repair를 허용할 수 있다.
   - pre-write에서는 사용자에게 같은 reviewer timeout 연장 재시도, reviewer route 변경, single-model mode, primary failure의 경우 main model 변경/복구 중 하나를 선택하게 한다.

Acceptance criteria:

1. CLI/local reviewer는 3분 hard failure 대신 capability/provider 기반 timeout을 받는다.
2. Sonnet처럼 compact but usable output을 내는 route는 불필요한 strict retry를 하지 않는다.
3. 같은 route가 연속 실패하면 다음 pre-fix에서는 degraded warning으로 빠르게 진행하고 pre-write에서는 adaptive one-call timeout extension과 명확한 fallback/route-change 안내를 낸다.
4. reviewer route가 없는 설정은 실패가 아니라 single-model mode로 시작한다.
5. single-model mode의 pre-write review는 frozen diff와 RF obligation status 없이는 approved를 만들 수 없다.

테스트:

1. `TestReviewModelCapabilityProfileControlsTimeout`
2. `TestReviewRouteHealthSuppressesRepeatedStrictRetry`
3. `TestReviewModelsStatusReportsRouteHealth`
4. `TestMissingReviewerRouteStartsSingleModelReviewMode`
5. `TestSingleModelPreWriteCannotApproveWithoutRFObligationStatus`

#### 16.10.7 속도와 반복 루프 억제 85+ 설계

목표:

1. focused local repair는 작은 범위일수록 작게 끝난다.
2. 같은 finding, 같은 patch, 같은 reviewer failure를 반복하지 않는다.
3. 사용자는 긴 대기를 끊을지 계속 기다릴지 판단할 수 있다.

설계:

1. Review budget은 `scope_width`와 `target`을 기준으로 산정한다.
   - focused selection: evidence 12k-20k
   - pre-write: diff/proposal/repair obligation 우선 20k
   - broad source analysis: explicit broad request일 때만 확대
2. edit loop에 `LoopSignature`를 둔다.
   - finding ids
   - patch fingerprint
   - reviewer error class
   - tool error class
3. 같은 signature가 2회 반복되면 loop action을 바꾼다.
   - patch mismatch 반복: fresh file read와 smaller patch 강제
   - reviewer timeout 반복: fallback offer 또는 route change 안내
   - verification gap 반복: post-edit verification obligation으로만 유지
4. long-wait progress는 2분 단위로 누적 elapsed와 soft timeout을 보여준다.
5. optional cross reviewer는 main review가 usable/actionable이면 token-limit 신호가 없을 때 retry하지 않는다.
6. patch generation은 selection-first 전략을 사용한다.
   - 사용자가 `file:132-221`처럼 범위를 지정하면 첫 patch proposal은 해당 범위와 직접 dependency만 읽는다.
   - 전체 파일 재독해는 hunk mismatch, symbol dependency, build failure가 있을 때만 확장한다.
7. 모델이 같은 단계에서 같은 파일을 반복해서 읽으면 loop breaker가 개입한다.
   - 동일 path/range를 2회 이상 읽고 새 tool action이 없으면 다음 model prompt에 "이미 확보한 excerpt로 결정하라"는 제약을 넣는다.
   - 추가 evidence가 필요한 경우 모델은 필요한 줄 범위와 이유를 먼저 출력해야 한다.

Acceptance criteria:

1. 100줄 이하 focused repair는 정상 route에서 pre-fix + implementation + pre-write가 8분 안에 diff preview까지 간다.
2. broken reviewer route는 한 번의 bounded failure 뒤 같은 edit loop에서 반복 호출되지 않는다.
3. patch mismatch가 두 번 이상 같은 anchor로 반복되지 않는다.
4. 같은 read/search/apply 실패가 2회 반복되면 사용자에게 현재 loop signature와 다음 선택지를 출력한다.

테스트:

1. `TestFocusedRepairLoopBudgetStaysUnderExpectedModelCalls`
2. `TestRepeatedPatchSignatureForcesFreshContext`
3. `TestRepeatedReviewerTimeoutDoesNotReinvokeSameRouteInSameLoop`

#### 16.10.8 제품급 견고함 85+ 설계

목표:

1. 중단, compaction, provider failure, partial artifact, stale review가 있어도 다음 행동이 결정적이어야 한다.
2. review harness가 사용자에게 "불확실하지만 승인"처럼 보이는 상태를 만들지 않는다.
3. artifact는 사람이 읽을 수 있고, 동시에 MCP/automation이 기계적으로 소비할 수 있어야 한다.

설계:

1. `ReviewRun` 저장은 atomic write로 바꾼다.
   - temp JSON
   - fsync 가능한 환경에서는 fsync
   - rename
   - latest pointer update
2. `latest.json`이 깨졌으면 가장 최근 valid run으로 복구하고 progress에 알린다.
3. session compaction 이후에도 `LastReviewRun`, `TaskState`, `PatchTransaction`이 서로 같은 run id를 참조하는지 검증한다.
4. final answer 전에 `ReviewLedgerConsistencyCheck`를 실행한다.
   - changed paths와 review paths mismatch
   - stale review
   - unresolved blocker
   - missing verification obligation
   - fallback approval without diff preview
5. artifact에는 "human summary"와 "machine contract"를 분리해 넣는다.
6. review session artifact에는 다음 파일을 추가한다.
   - `action_envelope.jsonl`
   - `approval_ledger.json`
   - `capability_manifest.json`
7. resume sanity check:
   - 재개 직후 최신 사용자 요청이 이전 작업과 충돌하는지 확인한다.
   - 충돌하면 이전 proposal을 적용하지 않고 새 요청 기준으로 상태를 재분류한다.
   - 충돌하지 않으면 마지막 stable action부터 이어간다.
8. artifact integrity:
   - evidence hash, proposal hash, current file hash를 분리한다.
   - file hash가 바뀌었는데 proposal hash만 남아 있으면 write gate를 열지 않는다.
   - report path가 유효하지 않으면 화면 출력만으로도 사용자가 판단 가능하도록 final review body를 재출력한다.

Acceptance criteria:

1. review artifact write 중 실패해도 latest pointer가 깨진 run을 가리키지 않는다.
2. compaction 뒤에도 pre-write blocker가 사라지지 않는다.
3. MCP 응답은 UI summary 없이도 gate action과 next command를 정확히 알 수 있다.
4. report path가 없어도 사용자가 최종 검토 결과를 transcript만으로 판단할 수 있다.

테스트:

1. `TestReviewArtifactAtomicWriteDoesNotCorruptLatest`
2. `TestCompactionPreservesReviewGateLedger`
3. `TestReviewLedgerConsistencyBlocksStaleFinalAnswer`

#### 16.10.9 구현 우선순위

1. P0 - State machine과 gate action 분리
   - 이유: 현재 반복 문제의 상당수는 verdict, reviewer failure, repair guidance, user fallback이 같은 문자열/분기로 섞이는 데서 나온다.
   - 완료 기준: pre-write reviewer failure, fallback, diff preview가 transition과 gate action으로 표현된다.

2. P0 - Replay fixture 기반 회귀 테스트
   - 이유: sample smoke log 기반 회귀가 많으므로 사람이 붙여넣은 로그를 deterministic fixture로 환원해야 한다.
   - 완료 기준: 최근 PathConverter smoke 계열 5개 이상이 fixture로 재현된다.

3. P1 - Model capability profile과 route health
   - 이유: 모델 이름 문자열과 임시 timeout 규칙만으로는 Sonnet/DeepSeek/OpenAI Codex 조합을 안정적으로 다루기 어렵다.
   - 완료 기준: timeout, retry, fallback 안내가 profile + 최근 health로 결정된다.

4. P1 - Finding/log renderer 통합
   - 이유: 사용자가 보고 판단하는 출력과 markdown artifact, MCP response가 어긋나면 review harness 신뢰도가 떨어진다.
   - 완료 기준: 같은 finding id가 progress, final visible summary, markdown, JSON에서 일관된다.

5. P2 - Atomic artifact와 ledger consistency
   - 이유: 제품급 견고함은 정상 실행보다 실패/중단 복구에서 갈린다.
   - 완료 기준: corrupted latest, stale review, compaction 후 상태 불일치를 deterministic하게 감지한다.

#### 16.10.10 85점 달성 판정

각 항목은 다음 조건을 만족해야 85점 이상으로 본다.

1. 설계 항목별 acceptance criteria가 모두 테스트 또는 smoke evidence로 확인된다.
2. `go test ./cmd/kernforge -count=1 -timeout 20m`가 통과한다.
3. `TestReviewReplayFixtures`가 최근 사용자 smoke fixture를 모두 통과한다.
4. 실제 sample focused repair smoke에서 다음이 관측된다.
   - main-first review result가 먼저 표시된다.
   - cross reviewer 상태와 timeout이 명확하다.
   - reviewer failure 시 main review fallback 선택지가 보인다.
   - fallback 승인 전에는 파일이 쓰이지 않는다.
   - diff preview 전에 final review body가 full detail로 표시된다.
   - local code repair 중 외부 web/search tool이 실행되지 않는다.
5. 실패한 경우에도 사용자가 다음 선택지를 알 수 있다.
   - reviewer route 변경
   - main review 기준 fallback
   - no-model deterministic review
   - scope 축소
   - post-edit verification 실행

#### 16.10.11 Codex App Parity Acceptance Matrix

아래 항목을 만족하면 KernForge 리뷰 하네스를 Codex App 대비 85점 이상 수준으로 평가한다.

| 영역 | 85점 기준 |
| --- | --- |
| 사용자 가시성 | 사용자가 "메인 모델이 검토 중", "리뷰 모델이 교차 검토 중", "메인 모델이 결과를 반영 중", "diff preview 대기 중"을 로그만 보고 구분할 수 있다. |
| 최종 결과 출력 | diff preview 전 최종 검토 결과에 RF별 문제, 영향, 수정 기준, 확인 방법이 표시된다. `...` 생략이 없다. |
| 실패 처리 | reviewer timeout/weak/empty가 발생해도 main review 결과와 fallback 선택지가 출력된다. |
| 단일 모델 리뷰 | reviewer route가 없어도 `single_model_mode`로 structured review, frozen diff self-review, RF obligation status를 수행한다. |
| 승인 경계 | review approval, write approval, commit request, push request가 독립적으로 기록된다. |
| 속도 | focused code review+repair는 기본 경로에서 5-8분 내 diff preview에 도달한다. 외부 모델 timeout이 있어도 12분을 넘기지 않는다. |
| tool 반복 억제 | 동일 read/apply/search 실패가 2회 반복되면 loop breaker가 단계 전환 또는 사용자 요약으로 전환한다. |
| web discipline | 웹 조회 전 목적 로그가 출력되고, capability가 없으면 모델이 web tool을 호출하지 않는다. |
| patch 안정성 | apply_patch mismatch 후에는 현재 파일 digest 기반으로 proposal을 재생성한다. 같은 patch 재시도는 금지된다. |
| resume | 중단/재개 후 현재 요청과 마지막 stable state가 맞는지 확인한 뒤 이어간다. |
| Git 안전성 | 사용자가 명시하지 않으면 commit/push를 하지 않는다. |

#### 16.10.12 구현 Backlog 보강

P0:

1. `ReviewActionEnvelope`와 `ApprovalLedger`를 review session artifact에 추가한다.
2. reviewer 실패 시 main review 기반 fallback 출력과 사용자 선택지를 구현한다.
3. final review body 출력에서 생략 문자열을 금지하고, 항목 단위 chunk 출력으로 바꾼다.
4. web search/fetch capability check와 `external_lookup_intent` 로그를 추가한다.
5. apply_patch mismatch loop breaker를 구현한다.
6. `SingleModelReviewPolicy`를 추가하고 reviewer route 부재를 실패가 아닌 정식 단일 모델 리뷰 모드로 처리한다.

P1:

1. `CapabilityManifest`를 session 시작 단계에서 생성한다.
2. focused selection 요청의 evidence budget을 selection-first로 축소한다.
3. main보다 낮은 등급 reviewer의 soft timeout을 5분으로 자동 확장한다.
4. repeated read/search/apply action detector를 추가한다.
5. resume sanity check와 file/proposal/evidence hash 검증을 추가한다.
6. single-model pre-write review가 frozen diff와 RF obligation status를 요구하도록 renderer와 gate를 연결한다.

P2:

1. Codex App parity acceptance matrix를 회귀 테스트 fixture로 변환한다.
2. review markdown report와 terminal output snapshot을 golden test로 관리한다.
3. MCP review 서버도 동일 action envelope와 approval ledger를 반환하도록 확장한다.
4. provider health score를 저장해 반복적으로 weak/timeout이 나는 reviewer를 자동 degrade한다.
5. 단일 모델 모드와 별도 reviewer 모드의 결과 품질을 같은 sample fixture로 비교하는 benchmark를 추가한다.

구현 진행 기록:

1. 2026-05-12 - protocol artifact/state machine 1차 구현
   - `ReviewRun`에 `StateTransitions`, `ActionEnvelopes`, `ApprovalLedger`, `CapabilityManifest`, `SingleModelPolicy`를 추가했다.
   - `GateDecision.Action`을 verdict와 분리해 `repair_required`, `reviewer_unavailable`, `user_decision_required`, `diff_preview_allowed`, `verification_required`, `final_summary` 중 하나로 기록한다.
   - review artifact directory에 `action_envelope.jsonl`, `approval_ledger.json`, `capability_manifest.json`을 추가로 저장한다.
   - `latest.json`이 깨진 경우 가장 최근 valid `review.json`으로 fallback recovery를 수행한다.
   - MCP `kernforge_review` 응답에 `state_transitions`, `action_envelopes`, `approval_ledger`, `capability_manifest`, `single_model_policy`를 포함한다.
   - 별도 cross reviewer가 없으면 `reviewer_unavailable`이 아니라 `single_model_mode`로 기록하고, CLI/Markdown summary에 independence level과 residual verification obligation을 표시한다.

2. 2026-05-12 - route capability/health와 replay fixture 보강
   - `ReviewModelCapability`와 `ReviewRouteHealth`를 추가해 provider/model/effort 기반 capability profile, schema reliability, blocker prior, latency class, retry budget, 최근 timeout/weak/empty 상태를 구조화했다.
   - `/review models status`가 최근 review run의 route health와 추천 조치를 표시한다.
   - `testdata/review_replay/`에 pre-write cross reviewer timeout fixture를 추가했고, `TestReviewReplayFixtures`가 provider 호출 없이 gate/action/fallback output을 재현한다.
   - regression coverage: `TestReviewRunWritesProtocolArtifacts`, `TestSingleModelReviewModeDoesNotRequireCrossReviewer`, `TestReviewMCPResponseIncludesProtocolContract`, `TestReviewLatestRecoveryUsesMostRecentValidRun`, `TestReviewModelPlanRecordsCapabilityProfileAndRouteHealth`, `TestReviewModelsStatusReportsRouteHealth`, `TestReviewReplayFixtures`, `TestReviewReplayFixtureRejectsMissingExpectedGate`, `TestReviewReplayFixtureCanModelSoftTimeoutWithoutSleeping`.
   - 전체 회귀: `go test ./cmd/kernforge -count=1 -timeout 20m` 통과.

3. 2026-05-13 - 16.10 잔여 계약 구현
   - `ReviewRun`에 `ExternalLookupIntents`, `ArtifactIntegrity`, `LedgerConsistency`, `ResumeSanity`를 추가했다.
   - review artifact directory에 `external_lookup_intent.jsonl`, `artifact_integrity.json`, `ledger_consistency.json`, `resume_sanity.json`을 추가했다.
   - local code review/repair 중 web/search/browser tool 호출을 차단할 때 `external_lookup_intent`를 session event와 review artifact에 기록한다. 허용된 web tool 호출도 실행 전 intent로 기록한다.
   - `ReviewModelCapability` rank/timeout 기준을 profile table로 이동했고, session-level `ReviewRouteHealth`를 누적 저장해 `/review models status`와 strict retry suppression에 사용한다.
   - single-model pre-write review는 frozen diff 없이는 diff preview gate를 열지 않고, pre-fix repair finding이 있으면 각 RF의 `resolution_status`를 요구한다.
   - `LoopSignature`를 추가해 repeated tool call/read/failure recovery guidance가 현재 loop signature와 required shift를 출력한다.
   - replay fixture 범주를 `reviewer route failure`, `omission/truncation`, `patch mismatch`, `local web block`, `pre-fix repair obligations`, `final visible summary`, `MCP response contract`로 확장했다.
   - pre-write final visible summary golden snapshot을 `cmd/kernforge/testdata/review_golden/prewrite_visible_summary.golden`로 추가했다.
   - targeted regression: `TestReviewRunWritesProtocolArtifacts`, `TestSingleModelReviewModeDoesNotRequireCrossReviewer`, `TestSingleModelPreWriteCannotApproveWithoutRFObligationStatus`, `TestSingleModelPreWriteReviewUsesFrozenDiff`, `TestSingleModelReviewRecordsIndependenceLevel`, `TestExternalLookupIntentRecordsBlockedLocalWebResearch`, `TestReviewLedgerConsistencyBlocksStaleFinalAnswer`, `TestResumeSanityDetectsConflictingLatestUserRequest`, `TestReviewArtifactAtomicWriteDoesNotCorruptLatest`, `TestReviewModelCapabilityProfileControlsTimeout`, `TestReviewRouteHealthSuppressesRepeatedStrictRetry`, `TestLoopSignatureRendersRepeatedReadAndToolFailure`, `TestMissingReviewerRouteStartsSingleModelReviewMode`, `TestReviewMCPResponseIncludesProtocolContract`, `TestPreWriteFinalVisibleReviewSummaryGolden`, `TestReviewReplayFixtures`, `TestReviewReplayFixtureCanModelSoftTimeoutWithoutSleeping` 통과.
   - web/loop/visible-summary focused regression: `TestAgentBlocksWebResearchForLocalCodeRepair`, `TestAgentBlocksNamespacedWebResearchForLocalCodeRepairWithoutMCPCatalog`, `TestAgentBlocksWebResearchAfterPreWriteReviewFeedback`, `TestAgentKeepsWebResearchHiddenAfterEditTargetMismatch`, `TestAgentStopsAfterPreWriteReviewerFailureWithoutWebResearchRetry`, `TestInvalidPatchFormatGuidanceChangesOnRepeatedSignature`, `TestAgentNudgesAfterRepeatedIdenticalToolCalls`, `TestAgentNudgesAfterRepeatedReadFilePathAcrossRanges`, `TestAgentNudgesBeforeAbortingRepeatedToolFailure`, `TestPreWriteFinalVisibleReviewSummaryDoesNotEllipsizeDetails`, `TestPreWriteFinalReviewProgressMentionsDiffPreview`, `TestPreWriteRunStoresRepairFindingsForFinalSummary`, `TestReviewMCPResponseIncludesActionContractBooleans` 통과.
   - review-mode pass:
     - `go run ./cmd/kernforge -command "/review --no-model"`: blocker 없음, broad scope warning만 표시.
     - `go run ./cmd/kernforge -command "/review --no-model --path cmd/kernforge/review_harness_integrity.go --path cmd/kernforge/review_harness_state.go --path cmd/kernforge/review_provider_behavior.go --path cmd/kernforge/review_harness_models.go --path cmd/kernforge/review_harness_gate.go --path cmd/kernforge/review_harness_replay_test.go --path cmd/kernforge/review_harness_state_test.go"`: blocker 없음, symbol excerpt unavailable 정보성 note만 표시.
   - 최종 전체 회귀: `go test ./cmd/kernforge -count=1 -timeout 20m` 통과. (2026-05-13)

49. 사용자 smoke - pre-write evidence gap을 코드 수리 루프로 반복시키는 문제 수정
   - 발견: focused line-range edit의 pre-write review에서 reviewer가 "함수 후반부 변경 결과를 확인할 증거가 부족함" low `evidence_gap`을 냈다. 실제 문제는 patch 자체가 아니라 하네스가 selection-focused preview 앞부분만 제공하고 `m_volumePathMap`, `FindNextVolume`, `FindVolumeClose`, `success` 계산 구간을 evidence에 싣지 않은 것이다.
   - 원칙: 하네스가 제공하지 않은 증거를 근거로 implementation model에게 반복 패치를 시키지 않는다. 함수 후반부, selection 이후 cleanup/success 경로, current file context 부족은 하네스 수집 문제로 보고 먼저 evidence pack을 보강한다. 실제 코드 누락, header/API surface 누락, member declaration 누락처럼 patch 자체의 결함을 가리키는 `evidence_gap`만 actionable warning으로 차단한다.
   - 수정: pre-write review에서 provided diff가 evidence budget을 독점하지 않도록 diff excerpt 상한을 두고, `@file:start-end` 요청이면 current file context는 "선택 범위 + 함수 끝까지"를 우선 보장한다. 같은 파일에서 selection을 감싼 함수 body를 찾으면 `function_body_excerpt`를 별도 source로 추가하고, selection 이후 함수 끝까지는 하네스가 중요 라인을 선별하지 않고 일반 tail excerpt로 보존한다. 긴 selection 때문에 직후 문맥이 중간에서 잘리지 않도록 selected range, immediate post-selection context, function tail excerpt를 나누어 제공한다. 하네스 증거 부족형 `evidence_gap` warning은 pre-write actionable warning block 대상에서 제외하되, compile/header처럼 patch 자체 결함을 가리키는 evidence gap은 계속 차단한다.
   - 회귀 테스트: `TestPreWriteEvidenceIncludesCurrentFileContextAroundRequestedRange`, `TestPreWriteReviewDoesNotBlockHarnessEvidenceGapWarning`, `TestPreWriteReviewBlocksActionableEvidenceGapDespiteEvidenceWording`, 기존 `TestPreWriteReviewBlocksImplementationEvidenceGapEvenWhenVerificationMentioned`, `TestPreWriteReviewUsesDiffFirstContextBudget`.
   - 검증: `go test ./cmd/kernforge -run "TestPreWriteEvidenceIncludesCurrentFileContextAroundRequestedRange|TestPreWriteReviewDoesNotBlockHarnessEvidenceGapWarning|TestPreWriteReviewBlocksActionableEvidenceGapDespiteEvidenceWording|TestPreWriteReviewBlocksImplementationEvidenceGapEvenWhenVerificationMentioned|TestPreWriteReviewUsesDiffFirstContextBudget" -count=1 -timeout 5m` 통과.
   - 최종 전체 회귀: `go test ./cmd/kernforge -count=1 -timeout 20m` 통과. (2026-05-13)

50. 사용자 smoke - pre-write repair 실패 후 사용자 선택형 continuation과 턴 소요시간 표시
   - 발견: pre-write review가 수정안을 막은 뒤 repair pass가 다시 막히거나 확인 도구만 반복하면, 이전에는 최신 검토 결과/수정안을 사용자에게 충분히 남기지 못한 채 중단하거나 넓은 재탐색 루프로 빠질 수 있었다. 또한 "계속 수정" 같은 자연어 확인은 실제 상태 전이가 모호했고, 긴 턴의 소요시간도 사용자가 별도로 계산해야 했다.
   - 원칙: 최종적으로 리뷰 모델을 통과하지 못해도 조용히 끝내지 않는다. 먼저 리뷰 미통과를 알리고, 최신 review result와 마지막 edit proposal을 보여준 뒤 사용자에게 `y/N`만 받는다. `y`는 저장된 review/proposal 기준으로 repair를 이어가고, `n`은 중단한다. 자연어 답변은 confirmation으로 소비하지 않는다. 일반 작업 턴은 소요시간을 출력하되 `/exit`, `/status`, `/config`, `/model` 같은 로컬 메타 명령은 출력하지 않는다.
   - 수정: session에 `pending_review_repair_confirmation` 상태를 추가하고, `ReplyWithImages` 진입 시 pending 상태가 있으면 모델 호출 전에 `y`/`n`만 라우팅한다. pre-write repair block 또는 inspection-budget 소진 시 `formatPreWriteReviewRepairUserDecisionReply`가 최신 검토 결과와 edit proposal을 보여주고 pending 상태를 기록한다. repair block 이후에는 한 번만 좁은 edit-tool 강제 지침을 넣고, 이후에도 편집 없이 확인 도구만 반복하면 사용자 확인으로 돌아온다. 턴 완료 시 `[time]`/`[time] 턴 소요 시간` 라인을 출력하고 local meta slash command는 제외한다.
   - 회귀 테스트: `TestAgentReportsReviewAndProposalAfterRepeatedPreWriteBlock`, `TestAgentAsksUserAfterPreWriteRepairInspectionNudgeIsExhausted`, `TestAgentForcesEditAfterPreWriteRepairInspectionBudget`, `TestAgentContinuesPendingReviewRepairOnlyOnY`, `TestAgentRejectsNaturalLanguagePendingReviewRepairAnswer`, `TestAgentStopsPendingReviewRepairOnN`, `TestSlashCommandTurnElapsedPolicySuppressesLocalMetaCommands`, `TestSlashCommandTurnElapsedPolicyKeepsWorkCommands`.
   - 검증: `go test ./cmd/kernforge -run "TestAgentContinuesPendingReviewRepairOnlyOnY|TestAgentRejectsNaturalLanguagePendingReviewRepairAnswer|TestAgentStopsPendingReviewRepairOnN|TestAgentReportsReviewAndProposalAfterRepeatedPreWriteBlock|TestAgentAsksUserAfterPreWriteRepairInspectionNudgeIsExhausted|TestAgentForcesEditAfterPreWriteRepairInspectionBudget" -count=1 -timeout 5m` 통과. 전체 패키지 테스트는 사용자가 요청할 때만 실행하는 정책에 따라 생략했다.

51. 사용자 smoke - local/degraded review route와 high-quality route의 adaptive 분리
   - 발견: 같은 Qwen/LM Studio 모델이 Claude Code나 Codex agent 경로에서는 코드를 읽고 수정안을 만들지만, Kernforge review-first 경로에서는 긴 evidence pack, 엄격한 `REVIEW_RESULT` schema, no-tool reviewer role, provider 응답 파싱 차이 때문에 empty/weak으로 분류될 수 있었다. 반대로 GPT 5.5 같은 high-quality route까지 schema를 느슨하게 만들면 review gate 품질이 떨어진다.
   - 원칙: raw response 저장, prompt/evidence snapshot, route failure와 code finding 분리는 모든 모델에 공통 적용한다. compact schema, 축소 evidence, reasoning-channel recovery, local prompt concession은 LM Studio, vLLM, llama.cpp, Ollama 같은 local/degraded route에만 적용한다. GPT/Claude strict route는 기존 엄격한 schema와 full focused evidence path를 유지한다.
   - 수정: OpenAI-compatible 응답의 redacted raw provider body를 review artifact에 `provider_raw_<role>.json`으로 저장하고 `ReviewReviewerRun.raw_provider_response_path`에 연결한다. local OpenAI-compatible provider가 빈 `content`와 별도 `reasoning_content`를 반환하면 content를 억지로 사용자 출력으로 승격하지 않고 reasoning channel을 보존한다. review harness는 local/degraded route에서 `reasoning_content` 안에 독립된 `REVIEW_RESULT` marker line이 있을 때 그 구조화 블록만 복구해 파싱한다. focused evidence가 local compact budget보다 크면 local/degraded route는 처음부터 compact initial prompt를 사용해 Qwen류 모델의 schema 부담을 줄인다.
   - 회귀 테스트: `TestLocalOpenAICompatibleClientPreservesReasoningContentWhenContentEmpty`, `TestLargeLocalReviewUsesCompactInitialPrompt`, `TestHighQualityReviewKeepsFullInitialPrompt`, `TestLocalReviewRecoversStructuredResultFromReasoningContent`, `TestLocalReasoningRecoveryRequiresExplicitMarkerLine`, 기존 local compact retry/route-health recovery/DeepSeek reasoning-content tests.
   - 검증: `go test ./cmd/kernforge -run "TestLocalOpenAICompatibleClientPreservesReasoningContentWhenContentEmpty|TestDeepSeekClientPreservesReasoningContentForToolLoop|TestDeepSeekClientPreservesStreamedReasoningContentForToolLoop|TestLargeLocalReviewUsesCompactInitialPrompt|TestHighQualityReviewKeepsFullInitialPrompt|TestLocalReviewRecoversStructuredResultFromReasoningContent|TestLocalReasoningRecoveryRequiresExplicitMarkerLine|TestLocalModelEmptyResponseUsesCompactRetry|TestLocalRouteHealthUsesCompactRecoveryInsteadOfInitialSkip" -count=1 -timeout 5m` 통과. 전체 패키지 테스트는 사용자가 요청할 때만 실행하는 정책에 따라 생략했다.

52. 사용자 smoke - pre-fix local route 실패와 pre-write blocked patch delta 회귀 수정
   - 발견: Qwen/LM Studio pre-fix review route가 empty `content`와 reasoning output만 반환하면 Kernforge가 `insufficient_evidence`로 멈춰 구현 모델의 독립 source inspection 기회를 주지 않았다. 반대로 GPT 5.5 단일 모델 경로에서는 첫 pre-write patch가 차단된 뒤 다음 repair proposal이 `#include <vector>` 같은 missing-piece delta만 제출되어, 실제로 필요한 코드 hunk가 사라지는 회귀가 발생했다.
   - 원칙: local/degraded pre-fix review route 실패는 코드 승인도 아니고 write approval도 아니다. 그러나 요청이 "검토하고 버그를 수정"처럼 source bug hunt 성격이고 신뢰 가능한 code finding이 없을 때는, 구현 루프가 참조 파일을 직접 읽고 독립적으로 실제 버그를 확인할 수 있어야 한다. pre-write는 계속 hard gate이며, 차단된 patch는 파일에 적용되지 않았으므로 다음 proposal은 이전 patch의 delta가 아니라 현재 파일에 바로 적용 가능한 complete standalone patch여야 한다.
   - 수정: `preFixReviewCanContinueWithIndependentInspection`와 local/degraded unreliable route 판정을 추가해 local pre-fix route failure를 non-blocking evidence-gap warning으로 낮추고, 구현 피드백에 "승인도 수정 금지도 아니며 독립 소스 확인 후 명확한 버그만 수정" 규칙을 넣었다. non-local strict route failure는 기존처럼 blocking evidence gap으로 남긴다. pre-write blocker/actionable warning feedback에는 "blocked patch was not applied"와 "다음 proposal은 delta가 아니라 현재 파일에 바로 적용 가능한 standalone patch" 규칙을 넣었다. 사용자 decision block은 current review boundary 안의 edit proposal만 사용하며, 최신 proposal이 include-only인데 최신 리뷰가 non-include code patch를 요구하는 경우에만 같은 boundary 안의 최신 non-include proposal을 보여준다. 이 판정은 diff shape와 review finding class만 사용하고 대상 API나 소스 의미를 해석하지 않는다.
   - 회귀 테스트: `TestPreFixLocalUnreliableReviewContinuesWithIndependentInspection`, `TestPreFixNonLocalUnreliableReviewStillStopsAsEvidenceGap`, `TestLatestEditProposalForUserDecisionShowsLatestEditProposal`, `TestAutomaticReviewFeedbackOmitsArtifactPathsAndSteersPatchSize`.
   - 검증: `go test ./cmd/kernforge -run "TestPreFixLocalUnreliableReviewContinuesWithIndependentInspection|TestPreFixNonLocalUnreliableReviewStillStopsAsEvidenceGap|TestLatestEditProposalForUserDecisionShowsLatestEditProposal|TestAutomaticReviewFeedbackOmitsArtifactPathsAndSteersPatchSize" -count=1 -timeout 5m` 통과. 추가 focused 회귀로 local reasoning/compact prompt, pre-write route failure, pending y/N continuation 계열 테스트를 각각 실행해 통과했다. 전체 패키지 테스트는 사용자가 요청할 때만 실행하는 정책에 따라 생략했다.

53. 사용자 smoke - pre-write after-preview evidence gap과 `partial` 상태 혼동 수정
   - 발견: GPT 5.5 단일 모델 PathConverter smoke에서 pre-write review가 `GetVolumePathNamesForVolumeName` 동적 버퍼 재시도 후반부를 확인하지 못해 RF-003을 `partial`로 표시했지만, gate는 `approved_with_warnings`로 diff preview를 허용했다. 실제 문제는 코드 미해결로 확정된 것이 아니라 큰 diff/file excerpt가 review prompt head-only 축약에 밀려 핵심 after-preview 줄을 보여주지 못한 evidence packaging/UX 문제였다.
   - 원칙: pre-write reviewer가 RF 해결 여부를 판단해야 하는 코드는 generic file excerpt보다 proposed diff의 RF 관련 after-preview 줄이 우선이다. 하네스 evidence gap은 코드 blocker와 분리해 표시해야 하며, diff preview를 허용할 때는 남은 항목이 "코드 미해결"이 아니라 "증거 확인 부족"임을 사용자에게 직접 설명한다.
   - 수정: pre-write evidence 수집 시 pre-fix RF title/evidence/required_fix/test 텍스트에서 일반 식별자 토큰을 뽑아 `Provided diff` 원문에서 주변 diff window를 별도 `repair_diff_excerpt` 섹션으로 추가한다. 이 섹션은 일반 file context보다 앞에 들어가므로 prompt budget이 줄어도 reviewer가 판단해야 하는 after-preview 줄이 남는다. single-model pre-write 상태 추론은 하네스 after-preview/evidence gap을 `partial` 대신 `evidence_unconfirmed`로 표시하고, visible summary/progress는 "코드 미해결 blocker가 아니라 evidence visibility warning"이라고 설명한다.
   - 회귀 테스트: `TestPreWriteEvidenceIncludesRequiredRepairDiffExcerpts`, `TestPreWriteHarnessEvidenceGapMarksRepairEvidenceUnconfirmed`, `TestPreWriteVisibleSummaryExplainsHarnessEvidenceGapPreviewAllowed`, 기존 `TestPreWriteEvidenceIncludesCurrentFileContextAroundRequestedRange`, `TestPreWriteEvidenceIncludesHeaderContextForIncludeSensitiveProposal`, `TestPreWriteReviewDoesNotBlockHarnessEvidenceGapWarning`, `TestPreWriteReviewBlocksActionableEvidenceGapDespiteEvidenceWording`, `TestPreWriteReviewBlocksImplementationEvidenceGapEvenWhenVerificationMentioned`.
   - 검증: `go test ./cmd/kernforge -run "TestPreWriteEvidenceIncludesRequiredRepairDiffExcerpts|TestPreWriteEvidenceIncludesCurrentFileContextAroundRequestedRange|TestPreWriteEvidenceIncludesHeaderContextForIncludeSensitiveProposal|TestPreWriteHarnessEvidenceGapMarksRepairEvidenceUnconfirmed|TestPreWriteVisibleSummaryExplainsHarnessEvidenceGapPreviewAllowed|TestPreWriteReviewDoesNotBlockHarnessEvidenceGapWarning|TestPreWriteReviewBlocksActionableEvidenceGapDespiteEvidenceWording|TestPreWriteReviewBlocksImplementationEvidenceGapEvenWhenVerificationMentioned|TestPreWriteFinalVisibleReviewSummaryGolden" -count=1 -timeout 5m` 통과. 전체 패키지 테스트는 사용자가 요청할 때만 실행하는 정책에 따라 생략한다.

54. 사용자 smoke - Qwen local repair handoff 품질 복구
   - 발견: Qwen + Claude cross reviewer 조합에서는 reasoning channel 복구 덕분에 pre-fix review 자체는 usable로 살아났지만, 병합 결과가 중복 RF, `stability issue` 같은 비실행 finding, 인접 코드 과확장 RF를 모두 차단 항목으로 넘겼다. 그 결과 구현 모델이 좁은 수리 대신 함수 rewrite/인접 코드 재설계 방향으로 가고, `apply_patch` edit target mismatch 뒤에도 현재 파일 기준 single hunk로 회복하지 못했다.
   - 원칙: local/degraded 모델에게는 review 결과를 더 많이 주는 것이 아니라 더 결정적으로 정규화해서 줘야 한다. 같은 호출/변수/실패 경로를 가리키는 RF는 하나의 수리 항목이어야 하며, 근거가 모호한 placeholder finding이나 코드 근거가 약한 인접 코드 재설계는 repair gate의 필수 항목이 되어서는 안 된다.
   - 수정: review finding merge는 API명이나 수리 계약 종류가 아니라 모델 finding 텍스트의 일반 토큰 중복으로 RF를 압축한다. 모델 생성 placeholder finding은 weak로 강등해 repair blocker에서 제외한다. local/degraded primary route의 repair plan에는 "중복 RF는 하나의 코드 변경으로 처리", "리뷰 evidence에 직접 나온 코드 경로만 수정", "모호한 RF로 rewrite 확장 금지" 규칙을 추가했다. edit target mismatch 복구 문구도 현재 파일을 다시 읽은 뒤 하나의 좁은 standalone hunk로 재작성하라고 고정하고, 반복 실패 시 최신 리뷰 기준과 마지막 edit proposal을 보여준다.
   - 회귀 테스트: `TestReviewMergeDeduplicatesGenericRepairContractFindings`, `TestVagueReviewerFindingDoesNotBecomeRepairBlocker`, `TestLocalRepairPlanCompressesDuplicatesAndRejectsSpeculativeBufferRewrite`, `TestAgentPromptsRereadAfterEditTargetMismatch`, `TestAgentStopsAfterSecondEditTargetMismatchEvenWithInterleavedSuccess`.
   - 검증: `go test ./cmd/kernforge -run "TestReviewMergeDeduplicatesGenericRepairContractFindings|TestVagueReviewerFindingDoesNotBecomeRepairBlocker|TestLocalRepairPlanCompressesDuplicatesAndRejectsSpeculativeBufferRewrite|TestReviewRepairPlanIncludesActionableWarningsWithBlockers|TestAgentPromptsRereadAfterEditTargetMismatch|TestAgentStopsAfterSecondEditTargetMismatchEvenWithInterleavedSuccess" -count=1 -timeout 5m` 통과. 기존 GPT/pre-write evidence gap 및 local route recovery 회귀 묶음도 focused `go test`로 재확인했다. 전체 패키지 테스트는 사용자가 요청할 때만 실행하는 정책에 따라 생략한다.

55. 사용자 smoke - Qwen reasoning-only 응답과 GPT after-preview 증거 부족 동시 개선
   - 발견: Qwen/LM Studio route는 compact initial prompt를 써도 최종 content는 비우고 provider `reasoning_content`에만 리뷰를 작성할 수 있었다. 이 경우 이전 로직은 compact initial을 이미 사용했다는 이유로 추가 복구를 하지 않아 `review model returned empty content while reasoning_content was present`로 멈췄다. GPT 5.5 단일 모델 pre-write에서는 proposed diff가 크면 `GetVolumePathNamesForVolumeName` 동적 재시도 후반부가 prompt에서 밀려, 실제 수리는 되었어도 after-preview evidence gap 경고가 반복됐다.
   - 원칙: high-quality GPT/Claude route의 strict schema는 느슨하게 만들지 않는다. 대신 local/degraded route에는 final content로 구조화 블록을 내보내도록 한 번 더 명시하고, pre-write evidence는 모델 종류와 무관하게 planned patch의 after 상태를 직접 보여준다. 구현 모델이 독립 점검으로 넘어갈 때는 첫 발견만 패치하지 말고, 같은 요청 범위 안에서 실제로 확인한 독립 문제를 함께 검토해야 한다.
   - 수정: local/degraded review run에서 content가 비었고 `reasoning_content`가 존재하면, compact initial 사용 여부와 관계없이 한 번의 compact retry를 수행하며 final content에는 `REVIEW_RESULT` 블록만 출력하라고 요구한다. `EditProposal`과 `apply_patch` preview에 selection-aware `after_excerpt`를 추가하고, pre-write evidence 앞부분에 `after_excerpt` 섹션을 별도로 싣는다. 이 excerpt는 선택 범위의 enclosing function body 또는 changed-code window를 head/tail 보존 방식으로 축약해 함수 종료부와 multi-string 순회 후반부가 잘리지 않게 한다. 이 시점의 pre-fix local evidence-gap checklist 접근은 58번 설계에서 제거됐다.
   - 회귀 테스트: `TestLocalReasoningOnlyCompactInitialPromptRetriesForFinalBlock`, `TestPreWriteEvidenceIncludesRequiredRepairAfterExcerpt`, 현재 fallback 문구 검증 테스트.
   - 검증: `go test ./cmd/kernforge -run "TestPreWriteEvidenceIncludesRequiredRepairAfterExcerpt|TestLocalReasoningOnlyCompactInitialPromptRetriesForFinalBlock|TestPreFixLocalIndependentInspectionAddsGenericChecklist|TestPreWriteEvidenceIncludesRequiredRepairDiffExcerpts|TestPreWriteEvidenceIncludesCurrentFileContextAroundRequestedRange|TestPreWriteEvidenceIncludesHeaderContextForIncludeSensitiveProposal|TestPreWriteHarnessEvidenceGapMarksRepairEvidenceUnconfirmed|TestPreWriteVisibleSummaryExplainsHarnessEvidenceGapPreviewAllowed|TestPreWriteReviewModelFailureBlocksEditGate|TestPreWriteMainOnlyFallbackPolicyDoesNotTreatCrossFailureAsHardGate|TestReviewMergeDeduplicatesGenericRepairContractFindings|TestVagueReviewerFindingDoesNotBecomeRepairBlocker|TestLocalRepairPlanCompressesDuplicatesAndRejectsSpeculativeBufferRewrite|TestAgentPromptsRereadAfterEditTargetMismatch|TestAgentStopsAfterSecondEditTargetMismatchEvenWithInterleavedSuccess|TestPreFixLocalUnreliableReviewContinuesWithIndependentInspection|TestLargeLocalReviewUsesCompactInitialPrompt|TestHighQualityReviewKeepsFullInitialPrompt" -count=1 -timeout 5m` 통과. 추가로 기존 agent retry/web-block 회귀 묶음과 `go test ./cmd/kernforge -count=1 -timeout 10m` 전체 패키지 테스트가 통과했다.

56. 사용자 smoke - Qwen 독립 수리 부분 패치 차단 실험 폐기
   - 발견: Qwen/LM Studio pre-fix route가 최종 content 없이 reasoning만 반환하면 Kernforge가 독립 source inspection으로 진행할 수는 있었지만, pre-fix 결과가 evidence gap뿐이라 pre-write의 필수 수리 항목이 비어 있었다. 이를 하네스가 보강하려 했지만, 이 접근은 결국 하네스가 코드 의미를 대신 판단하는 문제를 만들었다.
   - 원칙: local/degraded pre-fix evidence-gap handoff는 코드 승인이 아니지만, 그렇다고 하네스가 evidence에서 대체 수리 항목을 발명해서도 안 된다.
   - 수정: 이 항목의 deterministic repair-finding 생성과 after-state semantic checker는 58번 설계에서 제거됐다.
   - 회귀 테스트: 이 방향의 semantic-contract 테스트는 58번 설계에서 폐기되고, 하네스가 대체 RF를 만들지 않는지 확인하는 테스트로 교체됐다.
   - 검증: 당시 focused/전체 테스트는 통과했지만, 이 접근은 이후 smoke에서 과도한 하네스 의미 판정으로 판명되어 58번에서 제거했다.

57. 사용자 smoke - strict path/local-derived semantic gate 분리 실험 폐기
   - 발견: 56번 접근을 경로별로 나눠 보정해도, 모델 RF의 실제 요구보다 하네스의 의미 판정이 더 강해지는 회귀를 막지 못했다.
   - 원칙: local/degraded 모델과 cloud 모델을 semantic gate policy로 나누지 않는다. 모델별 차이는 prompt/evidence/route recovery에서만 다루고, 코드 의미 판정은 리뷰 모델에게 맡긴다.
   - 수정: 이 항목의 경로별 semantic gate 분기는 58번 설계에서 제거됐다.
   - 회귀 테스트: 이 방향의 호출명별 semantic gate 테스트는 58번 설계에서 폐기되고, body semantic 판단을 reviewer에게 남기는 테스트로 교체됐다.
   - 검증: 당시 focused 테스트는 통과했지만, 이 접근은 cloud/local 경로 모두에서 회귀 지점이 되어 58번에서 제거했다.

58. 사용자 smoke - deterministic semantic repair gate 폐기
   - 발견: API 이름을 직접 hard-code하지 않더라도, `unused gate`, `required-size retry`, `length guard` 같은 repair contract를 하네스가 추론하면 결국 같은 문제가 다른 형태로 반복된다. 특히 local/degraded route의 evidence-gap에서 하네스가 대체 RF를 만들면 메인 모델의 원래 코드 검토를 방해하고, cloud 모델이 낸 실제 RF보다 하네스 추론이 더 강한 요구로 변질될 수 있다.
   - 원칙: 코드 의미 판단은 메인 모델과 리뷰 모델의 책임이다. 하네스는 원래 사용자 요청, 수집한 로컬 evidence, 모델이 만든 실제 finding, proposed diff, after excerpt를 안정적으로 전달하고, 파일 쓰기 전에 reviewer route/patch syntax/evidence packaging 같은 운영 조건만 강제한다. 코드 API, 호출명, 실패 분기, 버퍼 정책, 문자열 인덱싱 같은 의미 판단을 deterministic gate가 재구성하지 않는다.
   - 수정: local evidence-gap에서 deterministic repair obligation을 생성하던 경로를 제거했다. pre-write deterministic gate의 source repair alignment checker도 제거하고, proposal 내용 유형을 deterministic하게 분류해 차단하던 경로도 제거했다. review merge는 hard-coded subject나 수리 계약 종류가 아니라 generic lexical token으로 중복을 압축한다. local/degraded pre-fix fallback 문구도 체크리스트 대신 "원래 요청을 기준으로 직접 소스 검토를 계속하고, 하네스는 대체 버그 목록을 만들지 않는다"는 지침으로 바꿨다.
   - 회귀 테스트: `TestPreWriteRepairObligationsDoNotInventLocalIndependentContracts`, `TestPreWriteDeterministicGateDoesNotPerformSourceRepairJudgement`, `TestPreWriteDeterministicGateLeavesBodyJudgementToReviewer`, `TestPreWriteDeterministicGateDoesNotBlockBodyControlFlowJudgement`, `TestPreWriteDeterministicGateAllowsNonIncludeRepairProposal`, `TestPreFixLocalIndependentInspectionKeepsOriginalRequestWithoutGeneratedChecklist`.

59. Codex App식 리뷰 모드 경계 적용
   - 발견: Codex App의 "리뷰 모드로 검토해"는 code-review stance로 findings-first/read-only 응답을 만들지만, Kernforge Agent 경로에서는 자연어 리뷰가 일반 모델 루프로 흘러가거나, "수정은 하지 말고" 같은 부정 표현이 pre-fix repair intent로 오인될 수 있었다.
   - 원칙: 리뷰 모드는 수정 전 리뷰가 아니라 독립 code review다. 파일 쓰기, patch guidance, repair handoff를 만들지 않고, finding을 먼저 보여준 뒤 요약을 붙인다. 같은 문장에 명시적 수정 요청이 있으면 기존 pre-fix review-and-repair flow로 간다.
   - 수정: 자연어 라우터가 `리뷰 모드`, `검토 모드`, `review mode`, `reviewer stance` 표현을 일반 review command로 인식한다. Agent는 natural review request를 common review harness로 직접 실행하고 `formatCodexAppReviewModeReply`로 응답한 뒤 implementation loop를 종료한다. `수정하지 말고`, `without editing`, `do not patch` 같은 표현은 review-only로 처리해 repair intent를 만들지 않는다. review model prompt와 compact/cross prompt에는 read-only code review 규칙을 추가했다.
   - 회귀 테스트: `TestNaturalLanguageReviewRoutesReviewModePhraseToAuto`, `TestNaturalLanguageReviewModeDoesNotSwallowReviewThenFix`, `TestNaturalLanguageReviewModeHonorsNoEditWording`, `TestAgentReplyReviewModeRunsReviewOnly`.

60. pre-write 차단 이후 전체 RF carry-over 복구
   - 발견: pre-write review가 이전 edit proposal을 차단한 뒤에도 다음 구현 프롬프트와 사용자 decision block이 최신 blocker 중심으로 보일 수 있었다. 이 경우 모델은 이전 patch가 파일에 적용되지 않았다는 사실을 놓치고, 원래 pre-fix RF 전체를 다시 만족하는 standalone patch 대신 마지막 blocker만 보강하는 delta를 만들 수 있었다.
   - 원칙: 하네스는 코드 의미를 직접 판단하지 않는다. 대신 상태 전달을 정확히 해야 한다. pre-write에서 차단된 patch는 디스크에 적용되지 않았으므로, 이후 모든 재시도는 최신 blocker와 함께 원래 pre-fix 필수 RF 전체가 계속 유효하다는 사실을 모델과 사용자에게 명확히 보여줘야 한다.
   - 수정: pre-write blocker/actionable warning feedback, 강제 edit-tool guidance, repeated-block user decision reply에 "계속 유효한 pre-fix 필수 RF" 섹션을 추가했다. 해당 섹션은 이전 edit proposal이 적용되지 않았고 다음 proposal이 최신 blocker delta가 아니라 현재 파일 기준 standalone patch여야 한다고 명시한다. single-model pre-write 상태 추론은 모델 finding이 특정 RF를 명시적으로 참조하지 않으면 기본 `resolved` 대신 `evidence_unconfirmed`로 표시한다.
   - 회귀 테스트: `TestPreWriteFeedbackCarriesAllOriginalRepairObligations`, `TestPreWriteForceEditGuidanceCarriesAllOriginalRepairObligations`, `TestSingleModelPreWriteRepairStatusDefaultsToEvidenceUnconfirmed`.

61. API/코드 의미별 하네스 규칙 제거 재점검
   - 발견: 생산 코드에서는 API명을 직접 판정하는 경로가 제거됐지만, duplicate/evidence token 필터와 회귀 테스트 fixture에 특정 상수/함수명이 남아 있어 하네스가 특정 코드 의미를 알고 있다는 신호를 줄 수 있었다.
   - 원칙: 하네스의 deterministic 경계는 provider 상태, structured output, patch syntax, evidence packaging, RF carry-over 같은 운영 조건에 한정한다. 함수 호출명, 버퍼 정책, 실패 분기, 문자열 인덱싱 같은 코드 의미는 reviewer model과 implementation model이 판단한다.
   - 수정: review harness 생산 코드에서 특정 코드 상수명을 stopword처럼 다루던 항목을 제거하고, 새 회귀 테스트 fixture를 중립 pseudo-code 이름으로 바꿨다. 테스트도 특정 API 해결 여부가 아니라 "하네스가 source/after 상태를 판정하지 않고 reviewer에게 남기는가"를 검증한다.
   - 회귀 테스트: `TestPreWriteDeterministicGateDoesNotPerformSourceRepairJudgement`, `TestPreWriteDeterministicGateLeavesBodyJudgementToReviewer`, `TestPreWriteDeterministicGateDoesNotBlockBodyControlFlowJudgement`, `TestPreWriteEvidenceIncludesRequiredRepairDiffExcerpts`, `TestPreWriteEvidenceIncludesRequiredRepairAfterExcerpt`.

62. Codex App식 리뷰 모드 route/evidence 독립성 보강
   - 발견: review-only mode를 자연어 intent로 감지하더라도, Agent 경로가 main chat client guard 뒤에 있으면 reviewer route만 설정된 환경에서 실행되지 못할 수 있었다. 또한 route 판단에는 원문이 필요하지만, reviewer evidence에는 `@file` mention 확장과 runtime context가 포함된 enriched request가 필요했다.
   - 원칙: 리뷰 모드는 구현 루프가 아니라 독립 code-review path다. 원문은 intent/routing에만 쓰고, reviewer에는 실제로 사용자가 보는 expanded context를 전달한다. main chat model이 없어도 configured reviewer route가 있으면 읽기 전용 리뷰는 실행 가능해야 한다.
   - 수정: `maybeRunCodexAppReviewMode`가 `userText`와 `enriched`를 분리해 받도록 바꾸고, review options의 `Request`/`RawArgs`에는 enriched text를 넣는다. review-only dispatch를 main client nil guard 앞에 배치하고, main role review client가 없으면 primary reviewer route로 fallback한다. 별도 임시 review-mode 파일은 제거하고 자연어 리뷰 구현에 통합했다.
   - 회귀 테스트: `TestAgentReplyReviewModeRunsReviewOnly`, `TestAgentReplyReviewModeCanUseReviewerWithoutMainChatClient`, `TestMaybeHandleNaturalLanguageReviewAllowsNilRuntime`.

63. mixed tool-call write isolation
   - 발견: Qwen/LM Studio가 한 응답 안에서 `apply_patch`와 `read_file` 또는 shell 도구를 함께 호출하면, 하네스가 patch를 즉시 실행하거나 read/write 순서가 꼬여 edit target mismatch, 긴 대기, diff preview 누락 같은 위험을 만들 수 있었다. cache-only shell도 Go cache나 workspace 상태를 바꿀 수 있어 edit와 같은 batch에서 실행하면 안 된다.
   - 원칙: edit tool은 isolated write turn에서만 실행한다. 한 model response에 edit tool과 다른 tool이 섞이면, 그 turn에는 엄격한 read-only inspection/plan tool만 실행하고, edit와 non-read-only tool은 `NOT_EXECUTED`로 미룬다.
   - 수정: mixed edit batch detector를 추가하고, edit tool, non-read-only tool, cache-only shell command를 다음 model turn으로 연기한다. synthetic tool result에는 "edit tool을 단독으로 다시 호출하라"는 복구 지침을 넣는다. 이렇게 해도 read-only file inspection은 먼저 실행되므로 모델은 현재 파일 내용으로 patch를 다시 anchor할 수 있다.
   - 회귀 테스트: `TestAgentDefersEditToolWhenMixedWithReadOnlyTool`, `TestAgentDefersNonReadOnlyToolWhenMixedWithEditTool`, `TestAgentDefersCacheOnlyShellWhenMixedWithEditTool`.

64. review freshness/runtime gate scope 정리
   - 발견: final/completion runtime gate가 전체 dirty tree를 기준으로 최신 review를 판정하면, 현재 patch transaction과 무관한 기존 수정 파일 때문에 stale review blocker가 반복될 수 있었다. 반대로 checked path의 content hash가 바뀌었는데도 pre-write freshness가 너무 넓게 pass하면 실제 stale review를 놓칠 수 있었다.
   - 원칙: final/completion gate freshness는 현재 action의 changed path scope로 판단한다. git write gate는 전체 dirty tree를 봐야 하지만, final answer gate는 unrelated dirty file 때문에 멈추지 않는다. 단, review artifact의 path hash와 현재 파일 hash가 다르면 해당 path review는 stale이다.
   - 수정: runtime gate changed-path 계산을 patch transaction scope로 제한하고, artifact integrity hash mismatch는 stale로 유지했다. final answer gate는 일반적인 "review" 단어가 아니라 blocker/stale/unresolved state를 명시적으로 disclosed 했는지를 확인한다.
   - 회귀 테스트: `TestRuntimeGateFinalAnswerUsesPatchTransactionScopeOverUnrelatedDirtyFiles`, `TestRuntimeGateLedgerBlocksStaleReviewForFinalAnswer`, `TestPreWriteLedgerConsistencyIgnoresUnrelatedDirtyGitPaths`, `TestRuntimeGateFinalAnswerRequiresExplicitBlockerDisclosure`.

65. 자연어 리뷰 mention workspace containment
   - 발견: 자연어 review mention parser가 `@../...` 또는 workspace 밖 absolute path를 정규화 없이 받아들이면, review evidence 수집 단계에서 workspace boundary 밖 파일을 읽을 수 있다.
   - 원칙: review evidence mention은 workspace-contained local evidence다. 하네스가 보안/권한 경계를 넓히는 경로가 되어서는 안 된다.
   - 수정: mention path를 workspace root 기준으로 정규화하고, root 밖으로 나가는 path-like mention은 review route로 전달하지 않는다. path-like mention token이 있었지만 전부 containment에 실패하면 natural review request로도 처리하지 않는다.
   - 회귀 테스트: `TestNaturalLanguageReviewRejectsMentionsOutsideWorkspace`, `TestNaturalLanguageReviewAllowsCleanedMentionInsideWorkspace`.

66. reviewer timeout과 사용자 복구 선택지 정리
   - 발견: Qwen + Sonnet cross review에서 reviewer timeout이 발생하면 사용자에게는 `route failure`만 보이고, 같은 reviewer를 더 긴 timeout으로 재시도해야 하는지, reviewer를 바꿔야 하는지, single-model mode로 가도 되는지 판단하기 어려웠다.
   - 원칙: timeout은 code finding이 아니라 route health 문제다. 하네스는 timeout 자체를 코드 blocker처럼 보이게 하지 말고, 모델 종류와 최근 health를 반영한 기본 시간과 다음 선택지를 표시해야 한다.
   - 수정: cloud API reviewer 기본 timeout은 5분, CLI reviewer와 local reviewer 기본 timeout은 8분으로 정리했다. 최근 timeout route는 다음 reviewer call에만 adaptive extension을 적용하고, status/failure message에는 "같은 reviewer로 timeout을 늘려 재시도", "reviewer 모델 변경", "reviewer 없이 single-model mode 진행", "primary 실패 시 main model 변경/복구" 선택지를 사용자 액션으로 번역한다.
   - 회귀 테스트: review model status/failure rendering 계열 테스트와 route health adaptive timeout focused 테스트로 검증한다.

67. PathConverter GPT-5.5 단일 모델 smoke cycle
   - 발견: `@SampleApp/SampleWorker/PathConverter.cpp:132-221 검토하고 버그를 수정해` smoke는 pre-fix finding, pre-write block/retry, diff preview, post-change review, target file restore가 한 사이클 안에서 모두 연결되어야 한다. 이전 회귀는 특정 API 의미 gate, stale path scope, evidence gap, mixed edit sequencing 중 하나가 깨지면 반복적으로 드러났다.
   - 원칙: smoke 대상 코드는 테스트마다 원본으로 복원한다. 하네스는 source-level semantic judgement를 하지 않고, review evidence/patch/review freshness/write isolation만 보장한다.
   - 결과: 최신 GPT-5.5 단일 모델 cycle에서 첫 pre-write는 reviewer finding으로 차단됐고, 이후 standalone patch proposal은 pre-write `approved_with_warnings`를 거쳐 write approval/diff preview 경로로 진행됐다. post-change review도 `approved_with_warnings`로 끝났으며, warning은 미검증/빌드 증거 부족류였다. MSBuild는 interactive permission 조건 때문에 생략했고, `PathConverter.cpp`는 cycle 종료 후 원본 hash `5192F00CD9E72426093CA73DF7E5382D89558CBD8F372F476D8F3F4A2978974B`로 복원했다.
   - 기록: `F:\kernullist\kernforge\.tmp\cycle-backups\gpt55-single-cycle-5-acceptEdits.log`, `F:\kernullist\kernforge\.tmp\cycle-backups\PathConverter-before-cycle.cpp`.

68. GPT-5.5 single-model pre-write recovery hardening
   - 발견: pre-fix visible summary guidance가 다음 pre-write review의 `objective`로 재사용되면 원래 사용자 프롬프트가 review artifact에서 사라질 수 있었다. 또한 `PathConverter.cpp:132-221` 같은 line-range mention이 artifact integrity에서 실제 파일명처럼 해시되어 불필요한 warning을 만들었고, pre-write block 이후 edit target mismatch stop summary가 실제 blocker 대신 낮은 test gap만 보여줄 수 있었다.
   - 원칙: 하네스는 코드 의미를 판정하지 않는다. 대신 원래 사용자 요청, 현재 파일 경로, reviewer blocker, patch proposal 상태를 정확히 보존하고 전달해야 한다. line-range는 evidence selection metadata이지 파일 경로가 아니며, pre-write block 뒤 복구는 최신 blocker와 원래 pre-fix RF 전체를 함께 carry-over해야 한다.
   - 수정: internal review-summary guidance를 user request 추출에서 제외하고, artifact integrity path resolution에서 trailing `:line`/`:line-line` suffix를 제거한다. pre-write block 직후 다음 model turn에는 `replace_in_file`을 숨기고 standalone `apply_patch` 복구 지침을 주며, repeated edit target mismatch stop reply는 `formatLatestPreWriteReviewForUserDecision`을 사용해 실제 pre-write blocker를 먼저 보여준다.
   - 회귀 테스트: `TestPreWriteReviewUserRequestSkipsPreFixVisibleSummaryGuidance`, `TestReviewArtifactIntegrityStripsLineRangePseudoPath`, `TestEditTargetMismatchLoopLimitShowsPreWriteBlocker`, `TestPreWriteReviewBlockDisablesReplaceForNextRetry`.

69. review evidence/context budget 확장
   - 발견: GPT-5.5 단일 모델 smoke에서 pre-write evidence ordering은 개선됐지만, 기본/focused/pre-write context budget과 내부 excerpt budget이 서로 달라 일부 흐름은 여전히 작은 캡으로 잘릴 수 있었다. 특히 pre-write 전체 budget을 늘려도 diff excerpt, file context, after excerpt, local compact initial prompt limit가 작은 값으로 남아 있으면 reviewer가 실제 proposal 전체와 계속 유효한 RF를 동시에 보지 못한다.
   - 원칙: 코드 의미를 deterministic하게 판정하지 않는다. 대신 reviewer가 판단할 수 있도록 원래 사용자 요청, provided diff, edit proposal, pre-fix RF, required repair after excerpt, 현재 파일 context를 충분히 크게 전달한다. 작은 budget이 필요한 경로는 명시적 `--max-context-chars` 같은 사용자 설정으로만 줄인다.
   - 수정: 기본 review context를 120k, focused context를 60k, pre-write evidence를 source-analysis와 같은 180k로 올렸다. pre-write prompt/cross evidence limit는 120k로 올리고, local/degraded pre-write compact prompt도 같은 120k를 사용한다. pre-write diff excerpt는 50k, file/after context는 60k까지 허용하며, pre-fix explicit path/selection review는 source-analysis급 180k를 사용한다. 일반 `/review` command, 자연어 review, goal iteration review도 새 기본 budget을 사용한다.
   - 회귀 테스트: `TestFocusedPreFixEvidenceKeepsFocusedBudget`, `TestPreFixLineRangeKeepsExplicitLargeEvidenceBudget`, `TestPreWriteEvidencePreservesProposalAndRepairFindingsWhenContextIsTight`, `TestPreWriteEvidenceIncludesPreFixRepairObligations`, `TestPreWriteEvidenceIncludesRequiredRepairDiffExcerpts`, `TestPreWriteEvidenceIncludesRequiredRepairAfterExcerpt`, `TestPreWriteEvidenceKeepsModerateProvidedDiffComplete`, `TestPreWritePromptTreatsProvidedDiffAsAuthoritative`, `TestLargeLocalReviewUsesCompactInitialPrompt`, `TestHighQualityReviewKeepsFullInitialPrompt`.

70. verification freshness와 build approval 분리
   - 발견: 같은 PathConverter smoke를 반복하면 이전 patch에서 남은 failed verification history나 session LastVerification이 다음 patch의 post-change review에 섞여, 이미 다른 수정안으로 넘어간 뒤에도 `Latest verification has failures`가 새 review blocker처럼 되살아났다. 또한 artifact를 만드는 build/test shell command는 사용자 확인 대상인데, 비대화형 skip이나 사용자의 거절이 "검증 실패를 고쳐야 한다"는 semantic repair loop로 번역될 수 있었다.
   - 원칙: verification은 review/write와 별도 state machine이다. 하네스는 코드 의미를 대신 판정하지 않고, verification report가 현재 patch transaction 이후에 생성됐는지와 changed path를 덮는지만 일반 freshness 조건으로 확인한다. 오래된 report는 bug finding이 아니라 검증 gap이다.
   - 수정: review evidence와 runtime gate가 session LastVerification 및 persisted verification_history를 사용할 때 현재 patch transaction timestamp와 changed path coverage를 확인한다. 오래됐거나 범위를 덮지 않는 report는 review blocker로 승격하지 않고 runtime gate warning과 `/verify --full` next action으로 기록한다. artifact-writing build/test shell command는 diff preview와 같은 pinned confirmation인 `자동 검증을 실행할까요? [y/N/a=자동 실행]` 경로를 사용하며, 비대화형 `-prompt -y`에서는 diff preview와 같은 자동 승인 정책을 따른다.
   - 회귀 테스트: `TestPostChangeEvidenceExcludesVerificationHistoryPredatingCurrentPatch`, `TestRuntimeGateTreatsFailedVerificationBeforeCurrentPatchAsWarning`, 기존 verification history/focused review 테스트.

71. Codex식 build verification approval lifecycle
   - 발견: 실제 PathConverter smoke에서 build/test background shell이 사용자 승인 확인 전에도 `백그라운드 shell 시작 중`처럼 표시되어, 거절/스킵된 검증이 실행된 것처럼 보일 수 있었다. 또한 한 번 거절한 검증을 같은 턴에서 다시 실행하거나 `latest` job poll로 이어가면 사용자는 어떤 조치를 취해야 하는지 알기 어렵다.
   - 원칙: Codex의 `ExecApprovalRequestEvent`처럼 실행 요청, 사용자 결정, 실제 실행 결과를 분리한다. 빌드 검증은 코드 의미 판정이 아니라 command lifecycle이다. 승인 전 progress는 "검증 승인 확인 중"이어야 하고, 거절은 `verification_status=skipped`, `command_execution_status=declined`, `verification_evidence=false`인 terminal decision으로 남아야 한다. 실행이 시작된 background 검증은 아직 결과가 아니므로 `pending` evidence gap으로만 기록한다.
   - 수정: `run_shell`, `run_shell_background`, `run_shell_bundle_background`가 build/test artifact 명령으로 분류되면 progress line을 실행 시작이 아니라 verification approval request로 표시한다. 거절 결과의 completion summary도 "started"가 아니라 "verification skipped"로 렌더링한다. 같은 턴 안에서 모델이 재시도하거나 background poll을 시도하면 synthetic `NOT_EXECUTED` 결과로 막고, 사용자가 명시적으로 검증을 승인하기 전에는 다시 시도하지 말라고 지시한다. 또한 거절/스킵이 확정된 뒤에는 남은 model request의 tool list에서 shell 검증 실행과 background poll 도구를 제거해 모델이 같은 회복 실수를 반복하지 않도록 한다.
   - 회귀 테스트: `TestSummarizeToolInvocationVerificationRequestsApprovalNotExecution`, `TestSummarizeToolCompletionVerificationDeclineIsSkippedNotStarted`, `TestAgentBlocksVerificationRetryAndPollAfterDeclineInSameTurn`, `TestDeclinedBackgroundVerificationLeavesNoPollableLatestJob`.

72. Codex repo 기반 approval/write recovery 구조 반영
   - 참고: `F:\kernullist\codex\codex-rs\protocol\src\approvals.rs`의 `ExecApprovalRequestEvent`는 `call_id`, `approval_id`, `turn_id`, `started_at_ms`, `command`, `cwd`, `reason`, `available_decisions`, `parsed_cmd`를 가진 typed request다. `F:\kernullist\codex\codex-rs\core\src\session\mod.rs`의 `request_command_approval`은 pending approval을 먼저 등록한 뒤 request event를 보내고, 사용자 decision을 받은 뒤에만 실행 결과로 넘어간다.
   - 원칙: Kernforge도 build/test verification을 shell 실행 텍스트가 아니라 request -> decision -> execution/result state machine으로 취급한다. 또한 stale-context recovery는 코드 의미 판단이 아니라 patch transaction shape 문제이므로, edit target mismatch나 pre-write block 이후 넓은 recovery patch는 실행하지 않고 현재 파일 기준 좁은 standalone patch 재발행을 요구한다.
   - 수정: broad recovery `apply_patch` shape gate를 추가했다. pre-write block 또는 edit target mismatch 뒤에 multi-file 또는 multi-hunk recovery patch가 나오면 파일을 쓰지 않고 `NOT_EXECUTED` synthetic result로 defer한다. 반복되면 최신 review result와 마지막 edit proposal을 보여주고 멈춘다. 이 가드는 API명, 호출명, 버퍼 정책 같은 소스 의미를 판정하지 않는다.
   - 추가 수정: noninteractive `-prompt`에서 write approval을 받을 수 없는 경우 process failure로 종료하지 않고 `Write canceled.`를 출력한 뒤 정상 종료한다. 파일을 쓰지 않은 사용자/환경 decision을 route failure나 code finding으로 오해하지 않게 하기 위함이다.
   - 추가 수정: 실제 long smoke에서 10분 지점에 `request canceled by user`로 종료되는 경로를 확인했다. 원인은 비대화형 `-prompt` 실행에도 interactive ESC watcher가 설치되고, `confirmRequestCancel`이 비대화형 입력을 묵시 승인으로 처리할 수 있던 실행 모드 경계 문제였다. Codex repo의 command approval/cancellation 흐름처럼 명시 request/decision/event를 기준으로 삼기 위해, noninteractive 실행에서는 ambient keyboard watcher를 설치하지 않고 비대화형 cancel confirmation도 false로 고정했다.
   - 실제 GPT-5.5 smoke: `F:\kernullist\kernforge\.tmp\model-smoke\gpt55-pathconverter-write-cancel-normalized-20260516-085628.log`에서 noninteractive write approval unavailable이 `Write canceled.`로 정리되고 exit 0으로 끝났다. `F:\kernullist\kernforge\.tmp\model-smoke\gpt55-pathconverter-autoapprove-write-20260516-090022.log`에서는 `-y` write path가 실제 patch write, post-change review, build verification approval request, verification skipped/declined 기록까지 통과했다. 두 cycle 모두 대상 `PathConverter.cpp`를 원본 hash `5192F00CD9E72426093CA73DF7E5382D89558CBD8F372F476D8F3F4A2978974B`로 복원했다.
   - 회귀 테스트: `TestAgentDefersBroadApplyPatchAfterEditTargetMismatch`, `TestAgentStopsAfterRepeatedBroadApplyPatchRecovery`, `TestRunSinglePromptTreatsUnavailableWriteApprovalAsCanceled`, `TestRuntimeStateRequestCancelWatcherIsInteractiveOnly`, `TestAgentBlocksVerificationRetryAndPollAfterDeclineInSameTurn`, 기존 verification approval lifecycle 테스트.

73. Runtime gate ledger의 patch transaction scope 분리
   - 발견: 실제 PathConverter smoke의 post-change review ledger가 현재 patch transaction 외의 기존 dirty file까지 `Changed paths`에 섞어 표시했다. 이는 git write gate에서는 맞지만, review/final/completion gate에서는 현재 repair patch의 증거와 무관한 worktree 상태가 가짜 blocker나 stale reason처럼 보일 수 있다.
   - 원칙: Codex의 patch/call/turn 식별 경계처럼, 현재 review가 평가하는 변경 단위와 repository 전체 dirty state를 분리한다. 코드 의미나 특정 API를 하네스가 판정하지 않고, 변경 범위 ledger만 올바르게 분리한다.
   - 수정: patch transaction path가 있으면 review, final-answer, completion-audit ledger는 그 path만 사용한다. git write와 MCP write gate는 기존처럼 전체 dirty git state까지 포함한다.
   - 회귀 테스트: `TestRuntimeGateReviewUsesPatchTransactionScopeOverUnrelatedDirtyFiles`, `TestRuntimeGateFinalAnswerUsesPatchTransactionScopeOverUnrelatedDirtyFiles`.

74. noninteractive verification approval과 background evidence 상태 분리
   - 발견: 실제 GPT-5.5 `-prompt -y` PathConverter smoke에서 diff preview는 비대화형으로 승인되어 patch가 적용됐지만, build verification confirmation은 별도 경로에서 `!interactive`를 곧바로 declined/skipped로 처리했다. 그 결과 사용자가 `-y`로 자동 실행을 의도한 smoke도 최종 답변에 "이전 msbuild 검증은 skipped/declined"와 "pending verification"을 남겼다.
   - 원칙: verification confirmation도 code semantic 판단이 아니라 approval lifecycle이다. diff preview와 같은 사용자 결정 표면을 쓰되, 실행 시작과 성공 evidence는 분리해야 한다. 특히 background job start는 실행 결과가 아니므로 검증 성공으로 계산하면 안 된다.
   - 수정: `promptConfirmAutoVerify`는 `alwaysApproveVerification`, `ModeBypass`, `-y`로 설정된 permission/session/config 상태를 공통 auto-approval으로 인식한다. 비대화형 bypass 실행에서는 verification prompt를 자동 승인하고, bypass가 아닌 비대화형 실행은 기존처럼 skipped로 남긴다. `buildBackgroundJobMeta`와 `buildBackgroundBundleMeta`는 verification-like background start를 `verification_status=pending`, `verification_evidence=false`로 기록하고, exit code 0 완료 상태만 `passed` evidence로 승격한다. `toolResultHasSuccessfulVerificationEvidence`도 `verification_like=true`만으로는 성공 evidence를 만들지 않는다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\src\tools\runtimes\shell.rs`와 `tools\handlers\shell.rs`의 shell runtime은 approval requirement, request, runtime output을 분리한다. 이 구조를 따라 Kernforge도 verification request/decision/result를 metadata 단계에서 분리했다.
   - 회귀 테스트: `TestRuntimeStatePromptConfirmAutoVerifyNonInteractiveBypassApproves`, `TestRuntimeStatePromptConfirmAutoVerifyNonInteractiveDefaultDeclines`, `TestBackgroundVerificationStartIsPendingNotEvidence`, `TestCompletedBackgroundVerificationCanBeEvidence`, 기존 declined verification/background poll tests.

75. automatic verification 실패의 patch-scope repair gate
   - 발견: 실제 GPT-5.5 PathConverter smoke에서 `-prompt -y`로 자동 검증이 승인된 뒤, workspace-level MSBuild가 기존 프로젝트 전반의 C++ 표준/설정 오류를 대량으로 반환했다. 기존 하네스는 이 실패를 곧바로 "계속 고쳐라"는 semantic repair prompt로 바꿔 모델이 `PathConverter.cpp` 범위를 벗어나 여러 `.vcxproj`와 unrelated source를 읽고 고치려는 루프로 확장됐다.
   - 원칙: 검증 실패는 먼저 현재 patch transaction과 연결되는지 판정해야 한다. 하네스는 특정 API나 코드 의미를 판단하지 않는다. 대신 Codex tool orchestrator처럼 request/decision/result와 retry 조건을 분리하고, retry는 실패 증거가 현재 작업 범위에 속할 때만 허용한다.
   - 수정: 자동 검증 실패 후 repair prompt를 내기 전에 failed step의 `scope`, command/label/hint/failure line이 현재 patch changed path를 가리키는지 일반 경로 신호로 분류한다. `targeted` stage라는 사실만으로는 repair loop를 열지 않는다. 명령/scope가 변경 파일을 직접 대상으로 삼거나 실패 라인이 변경 path를 직접 가리키면 좁은 repair loop를 허용한다. workspace 또는 sibling-file failure가 변경 path와 연결되지 않으면 `Automatic verification failed outside the current patch scope` guidance로 전환하고, unrelated build/project/source 수정은 금지하며 final answer에서 verification risk를 명시하게 한다. C++/MSBuild adaptive verification은 변경 source를 포함하는 가장 가까운 `.vcxproj`를 먼저 선택하고, 솔루션 전체 빌드는 full verification에 남긴다.
   - 추가 수정: out-of-scope 자동 검증 실패를 guidance만으로 처리하지 않고 tool execution loop의 terminal state로 남긴다. 다음 모델 요청에는 tool definition을 제공하지 않아 final-answer-only 상태로 전환하고, scripted/degraded route가 그래도 tool call을 내면 실제 shell/progress status를 내기 전에 `NOT_EXECUTED` synthetic tool result로 닫는다. final answer가 돌아오면 post-change review, final-answer review, completion-audit repair gate를 다시 열지 않고 검증 risk disclosure만 보강해 종료한다. 이 차단은 특정 API, 파일명, 빌드 시스템 의미가 아니라 앞 단계의 patch-scope 판정 결과와 generic verification command classification 및 terminal-state lifecycle만 사용한다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\src\tools\orchestrator.rs`는 첫 attempt 실패 뒤에도 sandbox/network denial처럼 retry 조건이 명확한 경우에만 별도 approval과 두 번째 attempt로 넘어간다. Kernforge도 verification failure를 무조건 repair loop로 번역하지 않고, patch-scope 조건을 만족하는 경우에만 다음 model repair turn을 만든다.
   - Codex 추가 참고: `F:\kernullist\codex\codex-rs\core\src\agent\control.rs`는 forked history에 assistant commentary/tool traffic을 남기지 않고 `MessagePhase::FinalAnswer`만 보존한다. Kernforge도 out-of-scope 검증 실패 이후의 답변을 같은 terminal answer boundary로 취급해, 이미 닫힌 검증 실패 상태가 후속 review/repair gate 입력으로 재활성화되지 않게 한다.
   - 회귀 테스트: `TestAgentDoesNotBroadenRepairForOutOfScopeVerificationFailure`, `TestAgentBlocksVerificationRetryAfterOutOfScopeAutomaticFailure`, `TestAgentBlocksAllToolsAfterOutOfScopeAutomaticFailure`, `TestAgentReturnsFinalWithoutPostChangeReviewAfterOutOfScopeAutomaticFailure`, `TestVerificationRepairScopeDoesNotTreatProjectBuildSiblingFailureAsPatchScoped`, `TestAgentVerificationFailurePromptsAnotherTurnBeforeFinalAnswer`, `TestAgentCanRepairAfterFailedVerificationAndReturnAfterPass`, `TestBuildVerificationStepsForCppPrefersChangedProjectInAdaptiveMode`.

76. verification artifact와 source/config mutation 분리
   - 발견: 실제 GPT-5.5 PathConverter smoke에서 모델이 project build 실패 뒤 `msbuild ... /t:ClCompile /p:SelectedFiles=PathConverter.cpp`로 단일 파일 컴파일을 실행했고, 컴파일 자체는 성공했지만 MSBuild가 `x64/Debug/*.tlog` 같은 build artifact를 갱신했다. 기존 shell guard는 verification command 이후 workspace snapshot 변화가 있으면 모두 edit review gate 위반으로 처리해 성공한 검증도 실패처럼 보이게 했다.
   - 원칙: 검증 명령은 산출물을 만들 수 있다. 하네스는 특정 API나 코드 의미를 판단하지 않고, 검증 산출물과 source/config mutation의 실행 경계를 분리해야 한다. build/test artifact churn은 검증 lifecycle의 부작용으로 기록할 수 있지만, source/config 파일 변경은 preview/write gate 밖에서 계속 차단해야 한다.
   - 수정: `run_shell` verification-artifact 명령의 before/after snapshot 비교는 유지하되, 변경 경로를 source/config 파일과 build artifact로 나누어 source/config 변경이 있을 때만 오류로 반환한다. `bin`, `obj`, `build`, `target`, `.tlog`, `.obj`, `.pdb`, `.ilk`, `.lastbuildstate` 등 일반 build output은 허용하고, `.cpp`, `.h`, `.go`, `.json`, `.vcxproj`, `.sln`, `.props`, `.targets` 같은 source/config 변경은 여전히 차단한다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\src\tools\handlers\shell.rs`와 `tools\runtimes\shell.rs`는 shell 실행의 approval, output, exit status를 분리한다. Kernforge도 verification 실행 결과와 build artifact side effect를 source edit gate와 분리한다.
   - 회귀 테스트: `TestRunShellVerificationCommandAllowsBuildArtifacts`, `TestRunShellVerificationCommandReportsWorkspaceMutation`, `TestRunShellVerificationCommandRejectsExternalSymlinkWorkspace`.

77. verification command shell boundary 정규화
   - 발견: 실제 GPT-5.5 PathConverter smoke에서 자동 검증이 `-prompt -y`로 승인된 뒤, 저장된 tool path를 사용한 빌드 명령이 PowerShell에서 `"MSBuild.exe" "project.vcxproj"` 형태로 실행되어 `Unexpected token` 파싱 오류가 발생했다. 모델과 리뷰 하네스는 수리 범위를 잘 멈췄지만, 검증 명령 자체가 shell 경계에서 깨져 실제 build evidence를 만들지 못했다.
   - 원칙: 검증 command 생성은 특정 코드 의미나 API별 repair가 아니라 command lifecycle의 일부다. Codex repo처럼 command를 구조화된 실행 요청으로 보존하고 shell wrapper/canonicalization을 별도 단계로 다루는 것이 이상적이며, Kernforge의 문자열 command 경로에서는 적어도 shell별 실행 문법을 마지막 경계에서 공통 정규화해야 한다.
   - 수정: configured/detected verification tool path가 `msbuild`, `cmake`, `ctest`, `ninja` 같은 leading tool name을 대체할 때, PowerShell 계열 shell에서는 quoted executable 앞에 call operator `&`를 붙인다. `cmd`, `bash`, `sh` shell에서는 기존 quoted executable 형태를 유지한다. 이 변경은 build tool path 치환 규칙에만 적용되며 source-level finding이나 API 의미 판정에는 관여하지 않는다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\src\command_canonicalization.rs`와 `tools\handlers\shell.rs`는 command argv, approval requirement, shell runtime execution을 분리한다. Kernforge는 현재 문자열 command 기반이므로 `resolveVerificationCommandPath`에서 shell-specific executable invocation을 정규화한다.
   - 회귀 테스트: `TestResolveVerificationCommandPathUsesConfiguredMSBuildPath`, `TestResolveVerificationCommandPathUsesConfiguredMSBuildPathForCmd`, `TestResolveVerificationCommandPathUsesConfiguredCTestAndNinjaPaths`.

78. pre-write 차단 proposal의 committed-state re-anchor
   - 발견: 실제 GPT-5.5 PathConverter smoke에서 첫 pre-write review가 안전하게 파일 쓰기를 막은 뒤, 다음 구현 turn이 차단된 proposal의 after-state를 현재 파일처럼 가정하고 즉시 새 patch를 제출해 `edit target mismatch`가 발생했다. 이후 모델이 다시 읽고 회복하긴 했지만, 같은 실수는 오래 걸리는 smoke마다 불필요한 회귀 루프를 만든다.
   - 원칙: pre-write에서 차단된 proposal은 디스크에 쓰이지 않았고 committed workspace state가 아니다. 하네스는 코드 의미나 특정 API를 판단하지 않고, 실제로 적용된 tool result만 다음 edit의 기준으로 인정해야 한다.
   - 수정: pre-write review가 edit을 차단하면 `preWriteReviewRequiresReanchor` terminal repair state를 세운다. 이 상태에서 다음 edit tool이 current file/diff re-anchor 없이 나오면 파일을 수정하지 않고 `NOT_EXECUTED` synthetic result로 닫는다. `read_file`, `grep`, `git_diff` 중 하나가 성공한 뒤에만 다음 standalone edit tool을 실행할 수 있다. 기존 mixed-tool isolation과 결합되어, 같은 assistant 응답에 `read_file`과 `apply_patch`가 함께 오면 read-only tool만 먼저 실행되고 edit은 다음 turn으로 미뤄진다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\src\turn_diff_tracker.rs`는 committed `apply_patch` delta만 current diff로 추적하고, 거절되거나 실행되지 않은 patch text를 workspace state로 취급하지 않는다. Kernforge도 pre-write blocked proposal을 같은 방식으로 non-committed proposal로 취급한다.
   - 회귀 테스트: `TestAgentBlocksImmediateEditAfterPreWriteBlockUntilReanchor`, `TestAgentContinuesAfterDistinctPreWriteCodeFinding`, 기존 pre-write repair/user-decision 및 broad recovery patch tests.

79. external verification callback report 정규화
   - 발견: scripted/test 및 외부 verifier callback이 `VerificationReport.GeneratedAt`, `Trigger`, `Workspace`, `ChangedPaths`를 비워 반환하면, 실제 자동 검증은 실행됐는데도 review/runtime gate가 "현재 patch 이후 검증인지 확인 불가"로 보고 post-change review를 `insufficient_evidence`로 되돌릴 수 있었다.
   - 원칙: 검증 callback은 실행 결과를 제공하고, Kernforge 런타임은 그 결과를 현재 turn/patch transaction의 state machine에 맞게 정규화해야 한다. 하네스는 특정 API나 코드 의미를 판단하지 않고, request -> execution/result boundary의 공통 메타데이터만 보정한다.
   - 수정: `VerifyChanges` callback 결과가 성공적으로 반환되면 `GeneratedAt`이 비어 있을 때 현재 시각을 채우고, `Trigger`가 비어 있으면 `automatic`, `Workspace`가 비어 있으면 현재 workspace root, `ChangedPaths`가 비어 있으면 자동 검증을 트리거한 changed path를 채운다. 따라서 최신 검증 evidence 판단은 callback 구현 세부가 아니라 동일한 verification freshness 규칙에 따라 계산된다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\src\tools\tool_dispatch_trace.rs`와 shell handler 계층은 tool result를 런타임 trace/evidence 형태로 변환해 control flow와 관찰 가능성을 분리한다. Kernforge도 external verifier의 부분 report를 런타임 evidence 계약에 맞게 보정한다.
   - 회귀 테스트: `TestAgentFinalAnswerReviewerPromptIncludesEditLoopLedger`는 `GeneratedAt` 없는 verifier callback을 사용해도 final-answer review prompt가 최신 edit/verification ledger를 포함하고 통과하는지 확인한다.

80. MSBuild project configuration/platform 선택
   - 발견: 실제 GPT-5.5 PathConverter smoke에서 C++ adaptive verification이 nearest `.vcxproj`까지는 좁혔지만, MSBuild command가 project의 `ProjectConfiguration`을 지정하지 않아 환경 기본값인 `Debug|Win32`로 떨어졌다. 해당 project가 `Debug|x64`만 지원하는 경우 `MSB8013`이 발생했고, 모델은 실제 코드 수리와 무관한 빌드 설정 문제를 다시 수리 루프로 오해할 수 있었다.
   - 원칙: 빌드 검증 command 생성은 특정 API나 코드 의미를 해석하는 일이 아니라, workspace build metadata를 읽어 execution request를 정확하게 구성하는 공통 하네스 책임이다. Codex repo의 command canonicalization처럼 실행 요청을 shell에 넘기기 전 구조를 안정화해야 한다.
   - 수정: adaptive C++ verification이 changed source의 nearest `.vcxproj`를 선택하면, 해당 project의 `ProjectConfiguration Include="Configuration|Platform"` 항목을 XML로 읽고 `Debug|x64`, `Release|x64`, `Debug|ARM64`, `Release|ARM64`, `Debug|Win32`, `Release|Win32`, first x64, first declared 순서로 선택한다. 선택된 값은 `msbuild "project.vcxproj" /m /p:Configuration=... /p:Platform=...`에 포함된다. 선언이 없으면 기존 command를 유지한다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\src\command_canonicalization.rs`, `tools\runtimes\shell.rs`, `tools\handlers\shell.rs`는 승인/실행 command를 runtime 경계에서 안정화한다. Kernforge는 현재 문자열 command 기반이므로 MSBuild project metadata를 읽어 command string의 의미 없는 기본값 의존을 제거했다.
   - 회귀 테스트: `TestBuildVerificationStepsForCppUsesProjectConfigurationPlatform`, 기존 `TestBuildVerificationStepsForCppPrefersChangedProjectInAdaptiveMode`.

81. review finding line anchor 정식화
   - 발견: Codex review prompt는 finding의 위치를 짧은 line range로 고정하도록 요구하지만, Kernforge의 `ReviewFinding`에는 `Line` 필드가 있어도 structured review schema와 parser가 `line:`을 명시적으로 다루지 않아 모델이 path 안의 `:line`을 그대로 path로 섞거나 line anchor를 잃을 수 있었다.
   - 원칙: line anchor는 코드 의미/API별 판정이 아니라 review evidence 품질과 UI/수리 handoff의 공통 metadata다. 하네스가 코드를 판단하지 않더라도, 모델이 제공한 위치 정보는 구조화해 보존해야 한다.
   - 수정: reviewer prompt schema에 `line: <1-based line number or 0>`를 추가하고, code finding에는 supplied evidence에 있는 가장 좁은 path/line을 쓰되 모르면 0 또는 empty로 두라고 지시한다. parser는 `path: file.cpp:123`과 별도 `line: 123-124`를 모두 인식해 `ReviewFinding.Path`와 `ReviewFinding.Line`으로 분리한다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\review_prompt.md`의 finding 위치 규칙은 짧은 위치 범위와 diff-overlap을 강제한다. Kernforge는 기존 YAML-like `REVIEW_RESULT` 형식을 유지하되 line anchor를 정식 필드로 편입했다.
   - 회귀 테스트: `TestReviewModelParserKeepsFindingLineAnchors`, 기존 structured finding parser tests.

82. review category 계약, verification command evidence, final-answer 출력 안정화
   - 발견: 실제 GPT-5.5 PathConverter smoke에서 reviewer가 production behavior bug를 `medium/test_gap`으로 잘못 라벨링하자, repair plan은 blocker만 포함했고 메인 모델은 경고로 내려간 코드 수리를 건너뛰었다. 별도 smoke에서는 MSBuild가 실제로 실행됐지만 final answer에는 논리 명령만 보여 command-not-found와 compile failure의 차이를 사용자가 확인하기 어려웠고, out-of-scope verification final answer 문장이 반복되거나 한국어 문장이 붙어 출력되는 경우도 있었다.
   - 원칙: 하네스는 `QueryDosDevice`, `GetVolumePathNamesForVolumeName`, `FindNextVolume` 같은 API 의미를 판단하지 않는다. 대신 structured review field 계약, command execution boundary, final-answer rendering 같은 공통 harness 책임을 강화한다. `test_gap`은 테스트/검증 evidence 부족에만 사용해야 하며, production code 변경 `required_fix`가 들어간 항목은 모델이 category를 잘못 붙여도 수리 의무에서 빠지면 안 된다.
   - 수정: review prompt에 "`test_gap`은 add/run tests 또는 verification evidence 제공에만 사용하고, production behavior defect와 implementation required_fix는 correctness/stability/security/performance/operational_risk로 분류하라"는 규칙을 추가했다. 게이트/repair plan은 `test_gap`이라도 medium 이상 severity, code path/symbol, implementation-style `required_fix`를 가진 항목은 실행 가능한 수리 의무로 유지한다. 순수 "run verification/add test/provide evidence" 항목은 계속 non-blocking test gap으로 남긴다.
   - 추가 수정: `VerificationStep.ResolvedCommand`를 verification report에 보존하고, 논리 command와 resolved shell command가 다르면 `FailureSummary`, `RenderShort`, `RenderDetailed`, terminal render에 함께 표시한다. missing-tool classification은 resolved/original command의 primary executable과 출력의 missing term이 일치할 때만 `command_not_found`로 분류해, MSBuild가 실행된 뒤 나온 compiler/build 문구를 tool-missing으로 오분류하지 않는다.
   - 추가 수정: assistant final text sanitizer를 공통화해 code fence 밖의 동일 sentence 반복 run은 한 번으로 collapse하고, `중단하겠습니다.수정 완료했습니다.`처럼 한국어 문장이 붙어 나오는 경우 sentence spacing을 보정한다. code fence 안의 텍스트와 `PathConverter.cpp` 같은 path-like dot은 보존한다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\review_prompt.md`의 finding 위치/구체성 규칙, `command_canonicalization.rs`와 `tools\handlers\shell.rs`의 command boundary 분리, `tool_dispatch_trace.rs`의 tool result trace 원칙을 반영했다. source API별 판단이 아니라 review schema와 execution/rendering contract를 강화한 변경이다.
   - 회귀 테스트: `TestReviewRepairPlanIncludesMislabeledTestGapImplementationWarning`, `TestReviewRepairPlanIncludesActionableWarningsWithBlockers`, `TestVerificationReportFailureSummaryIncludesResolvedCommand`, `TestClassifyVerificationFailureUsesPrimaryExecutableForMissingCommand`, `TestClassifyVerificationFailureDoesNotTreatBuildOutputAsMissingMSBuild`, `TestAssistantCollapsesRepeatedSentenceRun`, `TestAssistantSentenceSpacingPreservesCodeLikeDots`, `TestAssistantDoesNotCollapseCodeFenceRepeatedText`.

83. verification manifest evidence와 executable command 분리
   - 발견: 리뷰 모드가 `buildFuzzCampaignVerificationSteps`와 `buildAnalysisDocsVerificationSteps`에서 manifest/analysis/fuzz 결과 문자열을 `echo ...` command에 넣는 구조를 blocker로 지적했다. manifest 내용이 신뢰할 수 없는 입력이면 shell metacharacter가 verification command로 승격될 수 있었다.
   - 원칙: 하네스는 특정 API나 대상 코드 의미를 판단하지 않고, evidence text와 executable request의 경계를 지켜야 한다. 사람에게 보여 줄 verification context와 실제 shell command는 같은 문자열 슬롯을 공유하면 안 된다.
   - 수정: `VerificationStep.Informational`을 추가하고, generated analysis/fuzz/manifest evidence는 `Command`가 빈 informational step으로 만든다. 실행기는 informational step을 shell로 보내지 않고 `VerificationSkipped`와 output-only evidence로 기록한다. 표시용 문자열 정규화는 newline/control character 제거와 길이 제한만 수행하며 shell escaping 역할을 하지 않는다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\src\tools\handlers\unified_exec\exec_command.rs`, `codex_delegate.rs`, `client_common.rs`는 표시, 승인, 실행, patch 결과를 분리한다. Kernforge도 evidence report와 shell execution request를 같은 문자열로 합치지 않도록 맞췄다.
   - 회귀 테스트: `TestVerificationManifestEvidenceDoesNotBecomeShellCommands`, `TestExecuteVerificationStepsSkipsInformationalCommands`, 기존 analysis/fuzz verification plan tests.

84. skipped verification, reviewer role 축소, read-cache key, edit-loop evidence 회귀 보정
   - 발견: 리뷰 모드가 skipped automatic verification을 `ok=true` report로 기록한 뒤 agent loop가 `HasFailures()==false`만 보고 완료 progress를 낼 수 있다고 지적했다. 또 `ui_polish` mode가 changed path가 비어 있거나 core harness file을 포함해도 design reviewer만 요구할 수 있었고, 반복 `read_file` key가 case-sensitive로 바뀌어 Windows workspace에서 같은 파일을 다른 대소문자로 반복 읽는 루프를 놓칠 수 있었다. 후속 리뷰에서는 skipped automatic verification 이후 같은 턴의 verification retry가 즉시 차단되지 않을 수 있고, 최종 답변이 "검증하지 않았다"고 고지하면 edit loop가 successful verification evidence 없이 completed로 닫힐 수 있으며, background job metadata의 `job_status`가 scalar status와 job list 양쪽 의미로 쓰인다고 지적했다. 추가 리뷰에서는 `ui.go`, `Button.tsx` 같은 실행 가능한 UI source가 design-only reviewer로 빠질 수 있고, non-Windows에서 ordinary backslash filename이 Windows path처럼 lowercasing될 수 있으며, displayed reviewer profile은 auxiliary reviewer를 보여주는데 interactive planning은 auxiliary reviewer를 쓰지 않는 불일치도 발견했다.
   - 원칙: 하네스는 특정 API나 코드 의미를 판단하지 않고 state boundary를 정확히 표현해야 한다. skipped verification은 passed verification이 아니며, UI-only reviewer 축소는 evidence가 있을 때만 허용해야 하고, read-cache key는 workspace filesystem 특성을 반영해야 한다. UI-polish라는 intent만으로 executable source의 correctness review를 없애면 안 된다. 또한 disclosure와 evidence는 별개다. 모델이 미검증을 올바르게 고지해도 successful verification evidence가 생긴 것은 아니므로, edit-loop outcome은 risk를 보존해야 한다. job status와 job evidence list도 Codex식 tool result boundary처럼 서로 다른 metadata slot에 둔다. 사용자에게 표시하는 reviewer profile과 실제 reviewer client fallback도 같은 정책을 사용해야 한다.
   - 수정: `VerificationReport.WasSkipped`와 `HasPassedStep`을 추가해 skipped-only report는 완료 progress/완료 decision으로 렌더링하지 않는다. declined 또는 prompt-failed verification은 skipped report로 세션/히스토리에 남고 pending verification check를 유지한다. skipped automatic verification report를 받은 agent turn은 `verificationDeclinedThisTurn`을 세워 같은 턴의 build/test/verification retry나 verification background poll을 `NOT_EXECUTED`로 닫는다. `ui_polish` review는 changed path가 비어 있거나 UI-only로 판정되지 않는 path가 있으면 `primary_reviewer`와 `design_reviewer`를 함께 요구한다. UI-only 판정은 style/asset/markup surface에만 적용하고 `.go`, `.ts`, `.tsx`, `.js`, `.cpp`, `.rs`, `.py` 등 executable source 확장자는 primary reviewer를 유지한다. `readFilePathKey`는 Windows 또는 drive/UNC로 식별되는 Windows-style path에서만 case-insensitive key를 사용하고, non-Windows ordinary backslash filename은 case를 보존한다. `finalizeEditLoopOnReturn`은 changed path가 있고 successful verification evidence가 없으면 final answer가 미검증을 고지했더라도 `No successful verification was recorded for the changed paths.` risk를 남기고 `risk_accepted`로 마감한다. background bundle metadata는 job 목록을 `job_entries`에 기록하고, 단일 job의 `job_status`는 scalar status로 유지한다. reader는 legacy `job_status` list도 log path recovery용으로만 허용한다. `ensureInteractiveReviewerClient`는 explicit reviewer가 없고 auxiliary reviewer가 있으면 displayed profile과 동일하게 auxiliary reviewer를 반환한다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\src\tools\handlers\unified_exec\exec_command.rs`와 `session\mod.rs`의 approval/result event 분리, `turn_diff_tracker.rs`의 committed-state 추적 원칙을 반영했다. 사용자 거절, tool result, reviewer role decision을 서로 다른 상태로 보존한다.
   - 회귀 테스트: `TestAgentAsksBeforeAutomaticVerificationAndSkipsOnNo`, `TestAgentBlocksVerificationRetryAfterSkippedAutomaticVerification`, `TestAgentRecordsAutomaticVerificationPromptErrorAsSkipped`, `TestFinalizeEditLoopRecordsMissingVerificationRiskWhenDisclosed`, `TestEditLoopLogPathsFromMetaSupportsScalarAndListContracts`, `TestUIPolishReviewRequiresPrimaryForCorePaths`, `TestUIPolishReviewRequiresPrimaryForEmptyPathSet`, `TestUIPolishReviewAllowsDesignOnlyForUIPaths`, `TestUIPolishReviewRequiresPrimaryForExecutableUIPaths`, `TestReadFilePathKeyNormalizesCaseInsensitivePaths`, `TestEnsureInteractiveReviewerClientUsesAuxReviewerFallback`.

85. review model plan과 실제 실행 role 정렬
   - 발견: 후속 리뷰에서 `ui_polish`의 UI-only path는 계획 단계에서 `design_reviewer`만 요구하도록 축소됐지만, 실제 `executeReviewModelRuns`는 main pass를 항상 `primary_reviewer` prompt와 reviewer run으로 실행했다. 그 결과 plan/report는 design-only처럼 보이는데 실제 model call은 primary role이 되어, role 축소가 사용자에게 체감되지 않고 reviewer plan과 gate ledger가 서로 어긋날 수 있었다.
   - 원칙: 하네스는 코드 의미나 특정 API를 판단하지 않고, 이미 산출한 review model plan을 실행의 source of truth로 삼아야 한다. Codex App의 review thread가 resolved review request를 그대로 child turn에 전달하듯, Kernforge도 role plan을 만든 뒤에는 prompt role, progress role, reviewer run role, assigned model ledger가 같은 role을 가리켜야 한다.
   - 수정: `reviewMainExecutionRole`을 추가해 required role이 하나뿐이면 그 role을 main pass role로 사용하고, primary가 required roles에 포함된 다중-role 계획에서는 기존처럼 primary를 main pass로 둔다. `prepareMainFirstReviewModelPlan`은 더 이상 required roles를 `primary_reviewer`로 덮어쓰지 않고, 실행한 role의 assigned model과 satisfied role만 갱신한다. 따라서 design-only UI polish review는 실제 prompt에 `Role: design_reviewer`가 들어가고, required role도 design으로 보존된다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\src\session\review.rs`와 `review_prompts.rs`는 review request resolve 결과를 별도 child turn의 입력으로 전달하고, 중간 하네스가 review 의미를 다른 role로 바꾸지 않는다. Kernforge도 model plan을 단순 표시용이 아니라 실행 계약으로 취급한다.
   - 회귀 테스트: `TestExecuteReviewModelRunsUsesPlannedSingleRequiredRole`, 기존 `TestUIPolishReviewAllowsDesignOnlyForUIPaths`, `TestUIPolishReviewRequiresPrimaryForExecutableUIPaths`.

86. review-mode/edit intent와 skipped verification risk 경계 보정
   - 발견: GPT-5.5 리뷰 모드가 두 가지 상태 경계 문제를 지적했다. 첫째, `review mode` 문구와 `수정해` 같은 명시적 edit intent가 함께 있는 prompt에서 read-only review mode가 우선하면 사용자가 의도한 pre-fix review-and-repair flow가 눌릴 수 있다. 둘째, shell-tool verification 결과가 `verification_status=skipped`이면 `VerificationStatus`는 채워지지만 successful evidence는 없으므로 finalization에서 missing-verification risk를 유지해야 한다.
   - 원칙: review mode는 사용자가 "수정하지 말고" 또는 순수 review-only를 명시한 경우에만 edit intent를 누른다. verification disclosure와 verification evidence도 분리한다. skipped, running, canceled 같은 상태는 실행 결과 metadata이지 성공 evidence가 아니므로 edit loop completion을 green으로 만들 수 없다.
   - 수정: `resolveAgentRequestMode`를 추가해 explicit edit, repair negation, review-only mode, read-only analysis를 한 곳에서 계산한다. explicit edit intent가 있으면 review-mode wording만으로 read-only로 바꾸지 않고, repair negation이 있으면 edit intent를 제거해 read-only로 유지한다. `recordEditLoopToolResult`와 `finalizeEditLoopOnReturn`은 verification-like tool의 skipped/running/canceled 상태를 unresolved risk로 남기고, changed path에 successful verification evidence가 없으면 `risk_accepted`로 마감한다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\src\session\review.rs`는 review turn을 별도 request로 명시해 일반 edit turn과 섞지 않고, `tools\tool_dispatch_trace.rs`와 shell handler 계층은 tool result status와 실제 성공 evidence를 분리한다. Kernforge도 사용자 intent와 verification result를 같은 문자열 신호로 뭉개지 않게 나눴다.
   - 회귀 테스트: `TestNaturalLanguageReviewModeDoesNotSwallowReviewThenFix`, `TestNaturalLanguageReviewModeHonorsNoEditWording`, `TestFinalizeEditLoopKeepsSkippedShellVerificationRisk`.

87. 비대화형 자동 검증 prompt 가시성
   - 발견: 실제 GPT-5.5 PathConverter smoke에서 diff preview는 비대화형 `-prompt`에서도 pinned confirmation을 출력하고 pipe 답변을 읽었지만, 자동 검증은 `!interactive`이면 곧바로 skipped로 빠져 사용자가 빌드 검증을 승인/거절할 기회를 화면에서 확인할 수 없었다.
   - 원칙: 빌드/테스트 검증은 코드 의미 판정이 아니라 Codex식 command approval lifecycle이다. diff preview와 마찬가지로 request를 먼저 보여주고, 사용자 결정 또는 EOF/취소를 execution result와 분리해 기록해야 한다.
   - 수정: `promptConfirmAutoVerify`가 비대화형에서도 검증 계획과 `자동 검증을 실행할까요? [y/N/a=자동 실행]` label을 출력하고, pipe로 들어온 `y`/`n`/`a` 답변을 처리한다. stdin이 없거나 EOF/취소이면 오류나 암묵 승인으로 처리하지 않고 skipped/declined verification decision으로 기록한다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\src\apply_patch.rs`는 patch 승인을 runtime approval request로 위임하고, `app-server\src\bespoke_event_handling.rs`는 파일 변경/명령 실행 approval response의 decline/cancel을 실행 결과와 분리한다. Kernforge도 verification request, user decision, execution evidence를 같은 경계로 맞췄다.
   - 회귀 테스트: `TestRuntimeStatePromptConfirmAutoVerifyNonInteractiveDefaultDeclines`, `TestRuntimeStatePromptConfirmAutoVerifyNonInteractiveReadsPipedAnswer`, `TestRuntimeStatePromptConfirmAutoVerifyNonInteractiveEOFDeclinesAfterPrompt`.

88. 단발 goal 실행의 interactive mode 경계 보정
   - 발견: 리뷰 모드가 `-goal`과 `-goal-file` 단발 실행이 `promptFlag == "" && commandFlag == ""` 조건 때문에 interactive로 남는 경로를 지적했다. 이 경우 noninteractive 실행에서는 ambient keyboard watcher를 설치하지 않는다는 정책이 goal 실행에서만 우회될 수 있다.
   - 원칙: interactive 여부는 특정 명령 문자열이 아니라 실행 모드의 명시성으로 결정한다. 사용자가 REPL session에 들어간 경우만 interactive이고, `-prompt`, `-command`, `-goal`, `-goal-file` 같은 단발 실행은 모두 explicit event/decision boundary로 처리해야 한다.
   - 수정: `runtimeShouldUseInteractiveLoop` helper를 추가해 prompt, command, goal, goal-file flag가 모두 비어 있을 때만 interactive loop로 분류한다. `shouldInstallRequestCancelWatcher`와 `confirmRequestCancel`는 이 single source of truth를 따른다.
   - Codex 참고: `F:\kernullist\codex\codex-rs\core\src\session\mod.rs`와 app-server approval handlers는 turn/request 단위의 명시 상태를 기준으로 진행하며, ambient input을 승인/취소 decision으로 승격하지 않는다. Kernforge도 one-shot goal을 REPL 입력 상태와 분리했다.
   - 회귀 테스트: `TestRuntimeShouldUseInteractiveLoopOnlyForSessionMode`.

89. diff preview one-shot approval 의미 고정
   - 발견: 후속 리뷰에서 `autoAcceptPreviewOnce`가 현재 `a` 답변의 부작용으로만 소비되어, 사전에 one-shot approval flag가 세트된 경우에도 먼저 confirmation 입력을 요구할 수 있다고 지적했다.
   - 원칙: approval state는 질문/응답 lifecycle과 분리된 runtime state여야 한다. one-shot approval flag가 이미 세트되어 있으면 다시 사용자 입력을 요구하지 않고 정확히 한 번만 소비해야 한다.
   - 수정: `previewEdit`가 session-wide preview approval을 확인한 뒤, confirmation prompt를 띄우기 전에 `autoAcceptPreviewOnce`를 먼저 소비한다. 기존 `a` 답변 경로는 유지되며, 사전 flag와 현재 confirmation side effect 모두 같은 one-shot 의미를 가진다.
   - Codex 참고: Codex app-server approval handlers가 approval response를 decision으로 정규화한 뒤 실행 단계에 전달하는 것처럼, Kernforge도 preview approval flag를 prompt side effect가 아니라 명시 state로 취급한다.
   - 회귀 테스트: `TestRuntimeStatePreviewEditConsumesPreArmedAutoAcceptOnceWithoutPrompt`, 기존 `TestRuntimeStatePreviewEditAutoAcceptsCurrentDiffPreviewAlwaysAnswer`.

90. review specialist 모델을 route + lens 구조로 축소
   - 발견: security/design/false-positive/regression/test/final을 각각 reviewer model role로 관리하면 설정, route health, gate guidance, 실제 실행 role이 계속 벌어져 복잡성이 기능보다 커졌다.
   - 원칙: 실행 route는 active main model을 따르는 `primary_reviewer`와 선택적 독립 `cross_reviewer` 두 축으로 제한한다. 전문성은 model route가 아니라 `required_lenses`와 `optional_lenses`로 prompt에 주입한다.
   - 수정: `planReviewModels`가 항상 primary route를 기본으로 삼고, 보안/설계/오탐/회귀/테스트/final 관점은 lens로 계산한다. `/review models` 설정 surface는 `cross`만 새로 설정하게 하고, 기존 role별 설정은 deprecated compatibility fallback으로만 읽는다. security-sensitive single-model review는 missing role을 만들지 않고, 더 강한 독립 검토가 필요할 때 `/review models cross`를 추천한다.
   - 회귀 테스트: `TestReviewModelsCommandShortFormConfiguresCrossRoute`, `TestExecuteReviewModelRunsUsesPrimaryRouteWithDesignLens`, `TestMainFirstCrossReviewerSatisfiesDedicatedSecurityRole`, `TestSecuritySensitiveFallbackModelsProduceGuidance`, 전체 `go test ./cmd/kernforge -count=1 -timeout 10m`.

91. cross-review instability root fix: obligation ledger 중심 구조
   - 발견: 수십 차례의 cross-review 보강에도 route timeout, weak output, evidence truncation, malformed patch, actionable warning, verification gap이 모두 `approved_with_warnings` 또는 `needs_revision` 주변에서 섞였다. 그 결과 작은 분류 오류가 repair loop, re-anchor loop, reviewer route 복구, diff preview gate로 전파됐다.
   - 근본 원인: evidence packaging, reviewer judgment, orchestration decision이 같은 모델 출력과 gate normalizer에 묶여 있었다. 모델이 코드 판단과 다음 행동 결정을 동시에 암묵적으로 지배하면서, reviewer output 품질 문제가 state machine 문제로 확대됐다.
   - 원칙:
     1. 리뷰 모델은 의사결정자가 아니라 evidence producer다.
     2. RF, verification, evidence, reviewer route 문제는 `obligation_ledger`의 독립 항목으로 기록한다.
     3. `GateDecision.Action`은 모델 문구가 아니라 obligation type/status와 deterministic rule로만 결정한다.
     4. `approved_with_warnings`는 verification/test/evidence/operational warning 중심으로 좁히고, repair intent가 있는 actionable correctness/stability/performance finding은 `repair_required` 경로로 보낸다.
     5. cross reviewer 실패는 code finding이 아니라 `reviewer_route` obligation으로 기록하고, implementation model에게 반복 patch를 시키지 않는다.
   - 구현 단계:
     1. `ReviewObligationLedger`와 `ReviewObligation` schema를 추가한다.
     2. 기존 `RepairFindings`와 `Findings`를 ledger로 정규화해 `repair`, `verification`, `evidence`, `reviewer_route` 타입과 `open`, `resolved`, `evidence_unconfirmed`, `verification_required`, `route_unavailable` 상태를 기록한다.
     3. `reviewGateActionForRun`은 reviewer route obligation, repair obligation, verification obligation 순서로 action을 결정한다.
     4. Markdown/JSON artifact에 obligation summary를 노출해 사용자가 "코드 문제인지, reviewer route 문제인지, evidence 부족인지" 즉시 구분하게 한다.
     5. replay fixture는 malformed patch, weak reviewer, timeout, truncated evidence, partial RF fix를 golden case로 확장한다.
   - acceptance criteria:
     1. pre-fix `approved_with_warnings`의 medium actionable finding이 `repair` obligation으로 기록된다.
     2. `RF-REVIEWER-001`은 `reviewer_route` obligation으로 기록되고 code repair plan에는 들어가지 않는다.
     3. verification gap은 `verification` obligation으로 기록되며 pre-write diff preview를 막지 않고 post-edit verification action으로 남는다.
     4. review markdown과 MCP/JSON response에는 gate verdict와 별도로 obligation ledger가 포함된다.
     5. obligation ledger 기반 action 계산은 기존 single-model/cross-review gate 테스트를 깨지 않는다.
     6. `repair` obligation이라도 `status=verification_required`이면 repair loop가 아니라 verification action으로 라우팅한다. pre-write에서는 diff preview를 막지 않는다.
     7. ledger 기반 `repair_required` action이 verdict만으로 설명되지 않는 경우에도 `next_commands`에 repair continuation을 포함한다.
   - 구현 메모 (2026-05-27):
     1. `GateDecision.Action`은 reviewer_route, repair_required, evidence_required, verification_required obligation을 우선 사용하고, ledger가 비어 있는 legacy run에 한해 verdict fallback을 사용한다.
     2. `buildReviewRepairPlan`은 `Findings`뿐 아니라 `RepairFindings`에서 온 open repair obligation도 repair prompt에 포함한다.
     3. `review_replay` fixture schema에 `expected_obligation_counts`, `expected_obligations`, `repair_findings`를 추가했다.
     4. replay golden은 patch mismatch, weak/truncated reviewer output, cross timeout, truncated evidence, partial RF verification-needed 상태를 obligation ledger 기준으로 검증한다.

92. Codex-grade 자연어 검토/수정 요청 처리 계약
   - 발견: Kernforge가 review harness와 goal loop를 갖고 있어도, 일반 자연어 턴에서는 사용자가 "검토", "버그 찾기", "수정", "수정 후 리뷰", "문서/status", "커밋 준비" 중 무엇을 원하는지 명확히 분류하지 않으면 단순 chat wrapper처럼 첫 답변을 신뢰하거나, 단일 모델 구성에서 독립 리뷰가 없는 상태를 약한 fallback처럼 다룰 수 있었다.
   - 원칙: 단일 모델 구성은 degraded mode가 아니라 explicit staged mode다. cross reviewer가 있을 때는 독립 reviewer feedback을 단순 요약으로 취급하지 않고, primary implementation flow가 수용/보류/기각/사용자 결정 필요로 판단해야 한다.
   - 수정: Agent prompt에 `codexGradeRequestHandlingPrompt`를 추가해 fresh execution, review, edit, command, active goal/task state에서 Codex-grade request handling block을 주입한다. 이 block은 최신 외부 요청을 code review, bug finding, targeted modification, implementation plus verification, review-after-modification, documentation/status update, explicit git cleanup 중 하나로 분류하게 하고, repository inspection, unrelated user change preservation, review-only findings-first 출력, 수정 후 touched function/call-site/ABI/default/buffer/error/cancel/logging/docs self-review, focused validation disclosure를 요구한다.
   - 단일 모델 모드: distinct cross-review route가 없으면 `single_model` route mode를 명시하고 intent classification, context discovery, implementation/review, self-review, validation, final response 순서의 staged loop를 요구한다. 같은 모델의 첫 응답을 그대로 신뢰하지 말고, touched code와 stale docs를 다시 확인하도록 prompt contract를 둔다.
   - cross-review 모드: distinct reviewer route가 감지되면 `cross_review` route mode를 명시하고, cross-review finding을 independent feedback으로 취급해 accepted and fixed, accepted but deferred, rejected with technical reason, needs user decision 중 하나로 triage하도록 요구한다. repair plan에도 cross-review finding이 포함된 경우 같은 triage 의무를 implementation handoff에 넣는다.
   - goal/coding harness 반영: `/goal`, `-goal`, `-goal-file` iteration prompt에는 같은 staged loop를 추가하고, coding harness acceptance contract는 edit request에서 repository context inspection, second-pass regression review, validation/reporting을 expected behavior로 기록한다.
   - 리뷰 모델 prompt 반영: natural review trigger는 read-only code review stance를 명시하고, patch text나 tool request 없이 concrete finding을 먼저 반환하게 한다. issue가 없으면 finding 없이 approve하되 residual test/evidence risk만 summary에 남기게 한다.
   - 회귀 테스트: `TestAgentPromptIncludesCodexGradeRequestHandlingForEditRequests`, `TestAgentPromptUsesSingleModelCodexGradeContractWithoutCrossReviewer`, `TestAgentPromptUsesCrossReviewCodexGradeContractWhenReviewerConfigured`, `TestAgentPromptOmitsCodexGradeContractForTrivialChat`, goal staged-loop prompt tests, review repair handoff cross-review triage tests, natural review-mode read-only prompt tests.

93. Codex App-grade runtime review enforcement
   - 발견: 92번 계약은 prompt 수준에서는 Codex식 행동을 요구했지만, 실제 runtime은 여전히 cross-review finding 누락, single-model 2차 검토 생략, 최종 답변의 변경/검증/리스크 누락을 완전히 막지는 못했다.
   - 원칙: review/repair/final answer 품질은 모델이 기억해야 하는 예절이 아니라 runtime invariant여야 한다. cross-review finding은 audit 가능한 ledger로 남기고, single-model review는 별도 phase/call로 강제하며, 최종 답변은 사용자에게 보이기 전에 완료 사실을 검증해야 한다.
   - 수정:
     1. `CrossReviewTriageLedger`와 `CrossReviewTriageEntry`를 `ReviewRun`에 추가했다. cross reviewer의 actionable finding은 `accepted_fixed`, `accepted_deferred`, `rejected_with_reason`, `needs_user_decision` 중 하나로 기록된다.
     2. `accepted_fixed`에 fix ref 또는 changed path가 없거나, `accepted_deferred`에 이유가 없거나, `rejected_with_reason`에 기술 근거가 없으면 `RF-CROSS-TRIAGE-001` deterministic blocker가 gate를 막는다.
     3. distinct cross reviewer가 없으면 primary route로 `single_model_second_pass` model call을 별도 실행한다. prompt는 원 요청, touched files, diff, implementation reply, verification summary, 1차 review를 포함하고, finding은 기존 `ReviewFinding`/repair gate가 그대로 소비한다.
     4. second-pass fingerprint와 accepted cache를 session에 저장해 같은 diff에 대한 무한 review loop를 피한다.
     5. final answer completeness gate를 coding harness에 연결해 수정 흐름은 changed files/no-change, review/self-review result, validation result 또는 미실행 이유, remaining risk를 요구한다. review-only 흐름은 findings-first, no-edit statement, no-finding residual evidence risk를 요구한다.
     6. generated document artifact, trivial command/status, 순수 document artifact flow는 과차단하지 않도록 skip rule을 유지한다.
   - artifact 반영: `ReviewRun` JSON에는 `implementation_reply`, `single_model_second_pass`, `cross_review_triage`가 추가되고 markdown review report에도 second-pass 상태와 cross-review triage ledger가 렌더링된다. 기존 reader는 omitempty/additive field로 하위 호환된다.
   - 회귀 테스트: `TestSingleModelEditRequestRunsEnforcedSecondPass`, `TestSingleModelSecondPassFindingBlocksReviewGate`, `TestSingleModelSecondPassUsesAcceptedFingerprintCache`, `TestCrossReviewFindingCreatesTriageLedgerEntry`, `TestCrossReviewAcceptedFindingRequiresFixEvidence`, `TestCrossReviewRejectedFindingRequiresTechnicalReason`, `TestPreFinalHarnessCorrectsMissingChangedFileSummary`, `TestPreFinalHarnessCorrectsMissingValidationDisclosure`, `TestReviewOnlyFinalAnswerRemainsReadOnlyAndFindingsFirst`, generated document artifact skip tests, pre-write review gate tests.

남은 항목:

1. 수동 smoke 검증
   - 상태: 부분 실행. (2026-05-10)
   - 목표: 단위 테스트가 커버하지 못하는 실제 UX 흐름을 확인한다.
   - 실행한 smoke:
     - `go run ./cmd/kernforge -command "/status"`
       - 기대: `Runtime Gate` 섹션이 review freshness, blocker/warning count, next command를 표시한다.
       - 실제: `runtime_gate=needs_review warnings=2`, `review_freshness=missing`, `next_command=/review`가 출력됐다.
     - `go run ./cmd/kernforge -command "/hooks"`
       - 기대: hook status에서도 compact runtime gate summary가 출력된다.
       - 실제: `runtime_gate`, `review_freshness`, `next_command`가 `/status`와 같은 의미로 출력됐다.
     - `go run ./cmd/kernforge -command "/review --no-model"`
       - 기대: provider 호출 없이 deterministic review run과 recovery next command가 생성된다.
       - 실제: `approved_with_warnings`, broad scope warning, missing verification warning, `/review --path FEATURE_USAGE_GUIDE.md`, `/verify --full`, `/completion-audit` next command가 출력됐다.
     - `go run ./cmd/kernforge -command "/review --path FEATURE_USAGE_GUIDE.md --no-model"`
       - 기대: path focus로 `narrow-review` warning이 사라지고 focused review로 축소된다.
       - 실제: `ui_polish` focused review로 실행되고 missing verification warning만 남았다.
     - JSON-line MCP smoke:
       - 명령: `go run ./cmd/kernforge -mcp-server`에 `tools/call` `kernforge_review` 요청을 stdin으로 전달.
       - 기대: `latest_review_freshness`, `runtime_gate_ledger`, `scope_discovery`, `next_commands.expected_result`가 machine-readable JSON으로 반환된다.
       - 실제: 응답에 `latest_review_freshness.stale=true`, `runtime_gate_ledger.status=needs_review`, focused `scope_discovery`, `recommended_command.expected_result`가 포함됐다.
   - 아직 수동으로 실행하지 않은 smoke:
     - `/review selection`: 실제 selection buffer가 있는 interactive session 필요.
     - broad bug-fix natural language request: 모델 호출과 실제 edit loop가 필요하므로 현재 noninteractive smoke에서는 제외.
     - malformed `apply_patch` retry: 실제 tool loop에서 고의 실패를 만들어야 하므로 regression test coverage로 대체.
     - pre-write preview review: write approval/diff preview가 필요한 interactive edit smoke라 regression test coverage로 대체.
   - 기록 방식:
     - 각 smoke 결과는 이 문서의 진행상황 섹션에 날짜, 명령, 기대 결과, 실제 결과, 발견 버그, 후속 수정 여부로 남긴다.
