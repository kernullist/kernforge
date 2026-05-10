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

리뷰어가 볼 수 있는 근거 묶음이다. 모델 prompt에 그대로 다 넣는 것이 아니라, artifact로 저장하고 role별 prompt에는 필요한 부분만 압축한다.

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
  merge_result
  result
  findings
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

`selected_flow`는 target, mode, collector, reviewer roles, follow-up policy를 묶은 실행 경로다.

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
3. security pack에서 high severity가 있으면 기본적으로 `needs_revision`.
4. docs-only change는 test gap을 warning으로 낮출 수 있다.
5. user가 명시적으로 waiver를 요청하지 않으면 blocker waiver는 불가.

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

리뷰 타입에 따라 두 개 이상의 모델이 유리한 경우가 있다. 예를 들어 `core_build`는 architecture reviewer와 security reviewer가 분리되는 편이 좋고, `security_hardening`은 threat/bypass reviewer와 false-positive reviewer가 서로 다른 관점으로 보는 것이 낫다. 반대로 docs-only 또는 tiny change는 deterministic reviewer와 단일 reviewer면 충분하다.

`ReviewModelPlanner`는 selected flow를 보고 필요한 reviewer role과 모델 구성을 결정한다.

```text
ReviewModelPlan
  strategy
  required_roles
  optional_roles
  assigned_models
  missing_roles
  degraded_roles
  route_limits
  user_guidance
```

`strategy` 값:

1. `single`: main reviewer 하나로 충분하다.
2. `dual`: primary reviewer + specialist reviewer를 쓴다.
3. `multi`: 세 개 이상의 role reviewer가 유리하다.
4. `deterministic_only`: 모델 없이 deterministic gate만 실행한다.
5. `degraded`: 필요한 role이 없어서 더 약한 구성으로 실행한다.

권장 role:

1. `primary_reviewer`: 일반 correctness/stability review.
2. `design_reviewer`: architecture/core build review.
3. `security_reviewer`: security boundary, privileged path, abuse risk review.
4. `false_positive_reviewer`: detection, telemetry, anti-cheat 오탐 review.
5. `regression_reviewer`: live fix, refactor, compatibility review.
6. `test_reviewer`: verification, replay, coverage gap review.
7. `final_gate_reviewer`: conflicting findings를 합쳐 최종 gate를 검토.

Flow별 기본 추천:

1. `change_review`: `primary_reviewer`; security-sensitive이면 `security_reviewer` 추가.
2. `plan_review`: `design_reviewer`; security-sensitive이면 `security_reviewer` 추가.
3. `security_review`: `security_reviewer` + `false_positive_reviewer`; high-risk이면 `test_reviewer` 또는 `final_gate_reviewer` 추가.
4. `refactor`: `primary_reviewer` + `regression_reviewer`.
5. `pr_review`: `primary_reviewer`; checks가 실패했으면 `test_reviewer` 추가.
6. `goal_review`: `primary_reviewer` + `final_gate_reviewer`.

모델 설정 UX:

```text
/review models
/review models status
/review models primary
/review models security
/review models false-positive
/review models clear <role>
```

명령은 짧게 유지하되, `/review models`는 `/model`처럼 role, provider, model을 번호로 고르는 interactive flow를 제공한다. 직접 지정이 필요한 script/MCP client는 `/review models <role> <provider> <model> [reasoning_effort]`를 사용할 수 있다. 설정이 부족한 경우 review 결과에 guidance를 함께 남긴다.

예:

```text
Review model guidance:
- This security review would benefit from a dedicated false-positive reviewer.
- Configure it with: /review models false-positive
- Current run used primary reviewer only and is marked degraded=false, missing_roles=false_positive_reviewer.
```

기존 설정과의 관계:

1. 기존 `PlanReviewConfig` 기반 plan-review reviewer 경로는 제거한다.
2. 기존 `ProjectAnalysis.ReviewerProfile`은 analysis/report review의 reviewer 기본값으로 재사용할 수 있다.
3. 기존 `ReviewProfiles` 기반 별도 reviewer profile 경로는 제거하고, review role model 저장은 `review.role_models`로 단일화한다.
4. 기존 specialist profile은 domain 조사/작업 lens로 유지하되, 공통 review role model과 혼합하지 않는다.
5. model route scheduler는 reviewer role들이 같은 provider/model/base_url/reasoning_effort를 공유할 때 saturation을 막는다.

설정 부족 처리:

1. 필수 role이 없으면 `primary_reviewer` 또는 main model로 fallback한다. 별도 plan-review reviewer fallback은 두지 않는다.
2. fallback model도 없으면 deterministic-only review를 실행하고 `model_plan.strategy=deterministic_only`로 표시한다.
3. high-risk security review에서 `security_reviewer`가 없으면 `missing_roles`에 기록하고 `next_commands`에 `/review models security`를 추천한다.
4. anti-cheat detection/telemetry change에서 `false_positive_reviewer`가 없으면 warning과 추천 명령을 남긴다.
5. 모델 설정 부족은 review 실행을 막지 않지만, `ReviewResult.degraded` 또는 gate `quality_notes`에 반영한다.
6. 사용자에게 설정을 강요하지 않는다. "이번 run은 이렇게 fallback 했다"와 "더 좋은 구성을 원하면 이 명령"을 짧게 보여준다.

### 6.4.2 Multi-Model Merge Policy

여러 reviewer role을 실행하면 결과를 단순 concatenate하면 안 된다. `ReviewFinding`은 중복 제거, 충돌 기록, severity 보정, deterministic gate 보존을 거쳐 하나의 `merge_result`로 합쳐야 한다.

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
4. severity downgrade는 `final_gate_reviewer`가 concrete reason을 남긴 경우에만 가능하다.
5. security, stability, data-loss, external-write risk finding은 downgrade보다 conflict 기록을 우선한다.
6. `false_positive_reviewer`가 detection quality finding을 냈고 `security_reviewer`가 approved했더라도 false-positive finding은 유지한다.
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

`models`는 review target이 아니라 설정 surface다. `/review models`는 현재 role별 reviewer 설정과 부족한 추천 구성을 짧게 보여준다.

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

MCP response의 `model_plan`은 현재 실행이 단일 모델, 복수 모델, deterministic-only, degraded 중 무엇인지 알려준다. MCP client는 `model_plan.missing_roles`와 `next_commands`를 보고 사용자에게 "더 강한 리뷰 구성을 원하면 `/review models security`를 설정하라"처럼 안내할 수 있다. MCP 서버는 클라이언트 대신 모델 설정을 변경하지 않는다.

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
   - review role model 저장 방식과 기존 `ReviewProfiles`/`PlanReviewConfig` 제거 정책을 정의한다.

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
5. security-sensitive change에서 dedicated reviewer가 없으면 missing-role guidance와 추천 명령이 남는다.
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

사용자가 `TavernWorker 서비스 설치/시작 과정에 버그를 찾고 수정해`처럼 넓은 요청을 하면, 모델이 곧바로 전체 파일을 길게 읽고 스스로 방향을 정하기 쉽다. 이 경우 시간이 오래 걸리고, 리뷰 하네스가 언제 개입해야 하는지 흐려진다.

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
request: "TavernWorker 서비스 설치/시작 과정에 버그를 찾고 수정해"
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

현재 `다음 명령`이 단순히 `/verify --full`, `/completion-audit`, `/review models security`처럼 보이면 사용자는 무엇을 왜 해야 하는지 알기 어렵다.

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
3. role-specific reviewer graph를 provider capability와 비용/지연 예산에 따라 자동 최적화.
4. dashboard와 hook에서 review freshness, blocker, waiver, next action을 first-class 상태로 표시.

목표는 "Kernforge가 Codex App처럼 보이게 만들기"가 아니다. 목표는 모델이 어떤 provider이든 Kernforge 런타임이 review evidence, finding schema, patch safety, gate freshness를 끝까지 책임지는 것이다.
