# Review Harness UX/Ops 85점 개선 설계

작성일: 2026-05-13

최근 구현 반영: 2026-05-30

기준: Codex App을 100점으로 둔 상대 평가

대상: KernForge review harness, `/review`, pre-fix/pre-write gate, MCP `kernforge_review`, review artifact/runtime state

## 1. 목표

현재 구현은 typed review harness, request-class lifecycle, state transition, approval ledger, capability manifest, route health, loop signature, compact operator output, replay fixture를 갖춘 상태다. 스펙 구현체와 기본 CLI/MCP operator view는 강해졌지만, Codex App과 비교하면 전용 review status command, doctor/resume, dashboard, circuit breaker 같은 장기 운영 UX는 아직 backlog가 남아 있다.

이 문서의 목표는 다음 두 항목을 각각 85점 수준으로 끌어올리는 것이다.

| 항목 | 현재 추정 | 목표 |
| --- | ---: | ---: |
| UX/제품 경험 | 58 | 85 |
| 운영 안정성 | 74 | 85 |

85점의 의미는 Codex App의 GUI와 내부 구현을 복제한다는 뜻이 아니다. KernForge가 CLI/MCP 중심 도구라는 제약 안에서, 사용자가 다음 네 가지를 안정적으로 느끼는 수준을 말한다.

1. 지금 어떤 단계인지 항상 보인다.
2. 왜 멈췄는지, 다음 선택지가 무엇인지 보인다.
3. 장시간 작업, provider 실패, patch 실패, 재개 상황에서도 상태가 깨지지 않는다.
4. artifact를 열지 않아도 핵심 판단에 필요한 정보가 transcript/MCP 응답에 남는다.

## 1.1 현재 구현 상태 요약

2026-05-30 기준 KernForge review harness는 이 문서의 P0 중 operator-facing compact UX와 상태 parity 축을 상당 부분 구현했다. 아직 `/review status`, `/review timeline`, `/review artifacts`, `/review doctor`, static dashboard 같은 전용 slash command는 별도 backlog지만, 기본 review 출력, `/status`, artifact, MCP, runtime ledger는 같은 lifecycle 상태를 공유한다.

구현된 항목:

1. `progress_display` 기본값은 `compact`이다. 일반 interactive review는 반복적인 전체 1->6 flow 설명을 출력하지 않고 `review 1/6 scope`, `review 2/6 evidence`, `main ... done`, `cross ... done`, `gate ...`, `next ...` 같은 짧은 operator line을 우선한다.
2. `/progress-display stream`은 기존 verbose/detail 역할을 유지한다. stream 모드에서는 phase, lifecycle, waiting target, next transition, 전체 flow text가 transcript에 남는다. 기존 `verbose` alias는 계속 `stream`으로 정규화된다.
3. compact 최종 review CLI 출력은 result-first다. verdict, blocker/warning/note count, gate action, target/mode/request class, severity별 finding, report path, single next recommended command 순서로 렌더링된다.
4. compact terminal output에서는 같은 command를 가진 next command를 하나로 접는다. JSON, Markdown, MCP response, runtime gate ledger, cross-review triage ledger에는 전체 next-command record가 유지된다.
5. 특정 RF id를 지정한 후속 수리 요청, 예를 들어 `RF-004 수정해줘`, 는 repair handoff를 해당 RF로 좁힌다. 선택 RF만 `RepairFindings`와 pre-fix repair obligation으로 carry되며, 나열되지 않은 RF는 고치거나 해결했다고 보고하지 않는다. 선택 RF가 다른 RF와 분리 불가능하면 범위를 넓히기 전에 사용자 결정을 요구한다.
6. `/status`, `/hooks`, review Markdown/JSON, MCP review/status, runtime gate ledger는 request class, lifecycle phase, route mode/quality, gate status, second-pass state, cross-review triage count, blocker class, remaining obligation, next recommended command를 같은 의미로 노출한다.
7. single-model mode, cross-model mode, reviewer-only post-change route, document artifact lifecycle, final-answer correction contract는 artifact와 status/MCP에 additive field로 남는다.

아직 설계 backlog로 남은 항목:

1. `/review status`, `/review timeline`, `/review artifacts` 같은 전용 review status command.
2. `/review resume`, `/review doctor`, diagnostic bundle, corrupt latest pointer 복구.
3. route circuit breaker와 장기 route health trend.
4. review artifact directory의 별도 `index.md/index.json`.
5. read-only static review dashboard.

## 2. Codex App 기준 관찰 포인트

Codex App을 100점 기준으로 삼을 때 KernForge가 참고해야 할 제품 행동은 다음과 같다.

1. 작업 목표가 세션 상단 개념으로 고정된다.
   - 사용자는 현재 goal, 진행 단계, 완료/미완료 상태를 계속 추적할 수 있다.
   - 중간에 context가 압축되거나 재개되어도 최종 응답은 최신 요청을 기준으로 한다.

2. 단계와 도구 호출이 분리되어 보인다.
   - 모델이 생각하는 단계, shell/tool 실행, 파일 수정, 검증 실행이 서로 다른 이벤트로 보인다.
   - 실패한 도구 호출은 실패 원인과 다음 행동으로 연결된다.

3. 승인 경계가 명확하다.
   - 파일 쓰기, 테스트, commit, push, 외부 조회 같은 행동은 각각 다른 경계다.
   - 사용자가 요청하지 않은 commit/push를 하지 않는다는 신뢰가 있다.

4. 장시간 작업에는 heartbeat가 있다.
   - 사용자는 "멈춘 것인지 기다리는 것인지"를 구분할 수 있다.
   - 대기 중인 actor, 남은 retry, soft timeout, 다음 transition이 표시된다.

5. 실패가 다음 선택지로 바뀐다.
   - timeout, weak response, tool failure, patch mismatch가 단순 error로 끝나지 않는다.
   - retry, route change, fallback, scope 축소, verification-only 같은 선택지로 정리된다.

6. artifact와 화면 출력이 같은 사실을 말한다.
   - 화면 summary, markdown report, JSON artifact, MCP response의 gate/verdict/finding id가 어긋나지 않는다.

7. 재개가 안전하다.
   - 재개 시 최신 사용자 요청과 이전 run state가 충돌하는지 확인한다.
   - 오래된 patch나 stale review가 조용히 적용되지 않는다.

## 3. 현재 갭 요약

| 영역 | 현재 강점 | 85점까지 필요한 보강 |
| --- | --- | --- |
| 상태 모델 | `StateTransitions`, `ActionEnvelopes`, `ApprovalLedger`가 있다. | 이를 사용자 화면의 단일 timeline/status로 승격해야 한다. |
| 출력 | final visible summary와 artifact가 있다. | 항상 같은 renderer를 쓰고, next action center를 별도 섹션으로 고정해야 한다. |
| 모델 라우팅 | capability profile과 route health가 있다. | route health를 circuit breaker와 사용자 추천으로 연결해야 한다. |
| 반복 억제 | `LoopSignature`가 있다. | read/apply/model/tool 반복을 run-level budget과 결합해야 한다. |
| 복구 | artifact integrity, ledger consistency, resume sanity가 있다. | `/review resume`, `/review doctor`, diagnostic bundle로 사용자 조작성을 높여야 한다. |
| MCP | machine contract 필드가 있다. | UI summary 없이도 client가 그대로 action UI를 만들 수 있는 contract가 필요하다. |
| 운영 검증 | unit/replay/golden test가 있다. | chaos/recovery fixture, long-wait simulation, stale artifact test를 정례화해야 한다. |

## 4. UX 85점 설계

### 4.1 Review Timeline

모든 review run은 사용자에게 보이는 timeline을 가진다. 기존 `ReviewStateTransition`과 `ReviewActionEnvelope`를 원천 데이터로 사용하고, 별도 `ReviewTimelineEvent` view model을 만든다.

```text
ReviewTimelineEvent
- run_id
- sequence
- phase
- actor
- status
- started_at
- finished_at
- elapsed_ms
- visible_title
- visible_detail
- input_refs
- output_refs
- gate_action
- next_transition
```

원칙:

1. timeline은 artifact와 terminal/MCP 출력에서 같은 순서를 유지한다.
2. progress line은 timeline event에서 파생한다.
3. 사람이 읽는 제목과 machine field를 분리한다.
4. timeline event는 append-only로 남긴다.
5. pre-write에서 reviewer가 "증거가 부족하다"고 말하면 먼저 하네스가 evidence pack을 보강한다. 함수 후반부, selection 이후 cleanup/success 경로, current file context 부족은 코드 수리 루프가 아니라 하네스 수집 문제로 다룬다.
6. implementation model에게 재수정을 요청하는 경고는 patch 자체의 결함으로 한정한다. 하네스가 제공하지 않은 증거를 근거로 반복 패치를 시키지 않는다.
7. pre-fix RF가 여러 개일 때 pre-write gate는 부분 수리를 승인하지 않는다. 대신 repair prompt는 "필수 RF 전체 해결"과 "RF별 좁은 hunk"를 동시에 명시해야 한다. 구현 모델이 여러 함수를 한 hunk나 함수 전체 rewrite로 합치거나 함수 종료부/중괄호를 중복 삽입하도록 유도하면 안 된다.
8. pre-write block 이후 복구 턴은 review artifact 재조회, shell 기반 `Get-Content` line dump, 반복 `git status` 확인으로 시간을 쓰지 않는다. 필요한 근거는 전용 workspace 도구로만 좁게 확인하고, 바로 더 좁은 edit proposal을 만든다.
9. 최종적으로 리뷰를 통과하지 못하면 조용히 종료하지 않는다. 먼저 "리뷰 미통과"를 명시하고, 최신 review result와 마지막 edit proposal을 보여준 뒤 `계속 수정할까요? [y/N]` 상태로 전환한다. 이 상태는 session에 저장되며 `y` 또는 `n`만 소비하고 자연어 답변은 재질문한다.

### 4.2 Phase Banner와 Heartbeat

review run은 단계 진입 시 짧은 phase banner를 출력한다.

예시 출력은 실제 코드 적용 시 ASCII log 정책에 맞춘다.

```text
[review] phase=main_review actor=main_model evidence=18420 chars budget=focused
[review] phase=cross_review actor=reviewer_model model=anthropic-claude-cli soft_timeout=300s retry_budget=1
[review] waiting actor=reviewer_model elapsed=120s soft_timeout=300s next=merge_reviews
```

정책:

1. 30초 이상 tool/model 대기 시 heartbeat를 출력한다.
2. 2분 이상 모델 대기 시 actor, elapsed, soft timeout, retry budget, next transition을 출력한다.
3. heartbeat는 noise가 되지 않도록 같은 문구 반복 대신 timeline state를 요약한다.
4. noninteractive/MCP 모드에서는 heartbeat를 JSON event stream 또는 final event list로 축약한다.

### 4.3 Review Action Center

최종 출력에는 항상 `Action Center`를 둔다. 사용자는 report 파일을 열지 않고도 다음 행동을 고를 수 있어야 한다.

필드:

```text
ReviewNextAction
- id
- label
- command
- reason
- risk
- requires_user_approval
- applies_to
- blocked_by
```

대표 action:

1. `retry_same_scope`
2. `switch_reviewer_route`
3. `continue_main_only`
4. `show_diff_preview`
5. `run_focused_verification`
6. `open_artifact_summary`
7. `repair_patch_mismatch`
8. `stop_due_to_stale_review`

정책:

1. blocker가 있으면 첫 action은 수정/복구 경로여야 한다.
2. approved 상태라도 verification gap이 있으면 verification action을 남긴다.
3. commit/push action은 사용자가 명시 요청한 run에서만 생성한다.
4. MCP 응답은 같은 action list를 그대로 반환한다.

### 4.4 Visible Summary 표준화

현재 final visible summary를 더 엄격한 공통 renderer로 고정한다.

필수 섹션:

1. Verdict
2. Gate Action
3. Findings
4. Fixed/Unfixed Obligations
5. Evidence
6. Route Health
7. Artifact Integrity
8. Next Actions

규칙:

1. semantic truncation 금지. `...`로 판단 정보를 숨기지 않는다.
2. 길면 finding 단위로 chunk 출력한다.
3. finding id, severity, confidence, evidence refs는 markdown report/JSON/MCP와 동일해야 한다.
4. broad review warning과 blocker를 다른 섹션으로 분리한다.
5. no-model deterministic review, single-model review, cross-model review를 visible summary에 명시한다.

### 4.5 Review Status Commands

Codex App의 세션 상태 가시성을 CLI/MCP 명령으로 보완한다.

추가/정리할 명령:

```text
/review status
/review timeline
/review artifacts
/review resume
/review doctor
/review models status
/review recover --run <id>
```

각 명령의 역할:

| 명령 | 역할 |
| --- | --- |
| `/review status` | 최신 run의 verdict, gate action, freshness, next action을 한 화면에 표시한다. |
| `/review timeline` | phase/action/event 순서를 표시한다. |
| `/review artifacts` | report, JSON, diff, ledger, integrity 파일을 사람이 읽기 쉬운 index로 보여준다. |
| `/review resume` | 마지막 stable transition부터 이어갈 수 있는지 sanity check를 실행한다. |
| `/review doctor` | route health, broken latest pointer, stale artifacts, model config, web capability를 점검한다. |
| `/review models status` | provider별 latency/reliability/circuit 상태와 권장 route를 보여준다. |
| `/review recover --run <id>` | 특정 run을 기준으로 latest pointer와 derived artifact를 복구한다. |

### 4.6 Artifact Index

review artifact directory에 사람이 읽는 `index.md`와 machine-friendly `index.json`을 추가한다.

```text
review_artifacts/<run_id>/
- index.md
- index.json
- review.json
- report.md
- action_envelope.jsonl
- timeline.jsonl
- approval_ledger.json
- capability_manifest.json
- artifact_integrity.json
- ledger_consistency.json
- route_health.json
- next_actions.json
```

`index.md`는 다음만 빠르게 보여준다.

1. run id, trigger, target paths
2. verdict/gate action
3. changed paths and reviewed paths
4. blocker/warning count
5. next actions
6. key artifact links

### 4.7 Local HTML Dashboard

85점 목표에는 전체 GUI 앱이 필수는 아니지만, Codex App과의 UX 격차를 줄이려면 local static dashboard가 효과적이다.

명령:

```text
/review dashboard
```

동작:

1. 최신 review artifact를 읽어 `review-dashboard.html`을 생성한다.
2. 브라우저나 file viewer 없이도 CLI summary는 계속 제공한다.
3. dashboard는 artifact를 수정하지 않고 read-only로 렌더링한다.

화면 구성:

1. Run summary
2. Timeline
3. Findings
4. Gate/approval ledger
5. Route health
6. Artifact integrity
7. Next actions

이 기능은 P2로 둔다. 먼저 CLI/MCP contract를 안정화한 뒤 HTML을 얹는 것이 맞다.

## 5. 운영 안정성 85점 설계

### 5.1 Review Operation Health

route health와 ledger consistency를 묶어 run-level health를 계산한다.

```text
ReviewOperationHealth
- run_id
- status
  - healthy
  - degraded
  - blocked
  - stale
  - corrupt
- reasons
- route_health_summary
- artifact_health_summary
- ledger_health_summary
- retry_budget_remaining
- recommended_action
```

정책:

1. `healthy`: gate 결과와 artifact가 일관되고 next action이 명확하다.
2. `degraded`: reviewer timeout, optional route failure, missing noncritical artifact가 있지만 사용자 판단은 가능하다.
3. `blocked`: unresolved blocker, missing approval, stale patch, failed required reviewer가 있다.
4. `stale`: reviewed file digest와 current digest가 다르다.
5. `corrupt`: latest pointer, required JSON, ledger가 파싱 불가능하다.

### 5.2 Provider Circuit Breaker

현재 route health는 retry suppression에 쓰인다. 85점 운영 안정성에서는 route-level circuit breaker가 필요하다.

```text
ReviewRouteCircuit
- route_id
- state
  - closed
  - half_open
  - open
- opened_reason
- failure_window
- cooldown_until
- last_success_at
- last_failure_at
- recommended_fallback
```

정책:

1. 같은 route에서 timeout/empty/schema failure가 N회 반복되면 `open`으로 전환한다.
2. `open` 상태 route는 같은 review loop에서 재호출하지 않는다.
3. cooldown 뒤에는 `half_open`으로 1회 probe만 허용한다.
4. main model route와 reviewer route는 별도로 관리한다.
5. circuit state는 `/review models status`와 `Action Center`에 표시한다.

### 5.3 Retry Budget

retry는 provider별 임시 규칙이 아니라 run-level budget으로 관리한다.

```text
ReviewRetryBudget
- run_id
- scope_class
- model_call_budget
- tool_failure_budget
- patch_rewrite_budget
- reviewer_retry_budget
- web_lookup_budget
- consumed
- exhausted_reason
```

기본값:

| Scope | Model calls | Tool failures | Patch rewrites | Reviewer retries |
| --- | ---: | ---: | ---: | ---: |
| focused review | 3 | 2 | 1 | 1 |
| focused repair | 5 | 3 | 2 | 1 |
| broad review | 6 | 3 | 1 | 1 |
| no-model deterministic | 0 | 2 | 0 | 0 |

정책:

1. budget을 넘으면 자동 반복하지 않고 Action Center로 전환한다.
2. reviewer timeout 반복은 implementation retry budget을 소모하지 않는다.
3. patch mismatch 반복은 fresh context requirement를 강제한다.
4. web lookup budget은 local code review 기본값 0이다.

### 5.4 Stale Review 방지

write gate, final answer, resume 시 모두 stale review를 검사한다.

필수 검사:

1. reviewed paths와 changed paths 일치 여부
2. evidence hash와 current file hash 일치 여부
3. proposal hash와 pre-write review input hash 일치 여부
4. approval ledger의 diff preview/write approval 순서
5. latest user request와 run trigger의 충돌 여부

정책:

1. stale이면 write/diff preview를 열지 않는다.
2. final answer에는 stale reason과 복구 명령을 표시한다.
3. MCP response에는 `stale=true`, `blocked_by=["stale_review"]`를 반환한다.

### 5.5 Diagnostic Bundle

장애 재현과 운영 분석을 위해 `/review doctor --bundle`을 제공한다.

Bundle 구성:

```text
review_diagnostics/<timestamp>/
- summary.md
- run_index.json
- latest_review.json
- timeline.jsonl
- route_health.json
- circuit_breakers.json
- artifact_integrity.json
- ledger_consistency.json
- git_status.txt
- test_commands.txt
```

정책:

1. secrets, API keys, auth token은 수집하지 않는다.
2. 파일 내용 전체 대신 digest/path/line refs를 우선 저장한다.
3. 사용자가 명시하면 focused excerpts만 포함한다.
4. bundle 생성은 read-only action이다.

### 5.6 Concurrency와 Atomicity

운영 안정성 85점 기준에서는 두 개의 review run이 같은 latest pointer나 artifact를 깨지 않아야 한다.

정책:

1. run id별 directory는 immutable에 가깝게 관리한다.
2. derived artifact 재생성은 새 파일에 쓴 뒤 rename한다.
3. latest pointer update는 lock file 또는 atomic replace로 보호한다.
4. stale lock은 pid/time 기준으로 recoverable 상태로 표시한다.
5. 같은 workspace에서 두 review run이 동시에 write gate를 열면 둘 다 user decision required로 낮춘다.

### 5.7 Cancellation

Codex App 수준의 안정감에는 "멈출 수 있음"이 포함된다.

설계:

1. long model call, test run, dashboard generation은 cancellation token을 받는다.
2. 취소된 run은 `cancelled` transition과 operation health `degraded`로 남긴다.
3. 취소 후 재개는 마지막 stable artifact 기준으로만 가능하다.
4. patch apply 중 취소는 partial write 여부를 ledger에 남긴다.

## 6. MCP/UI Contract

MCP client가 별도 UI를 만들 수 있도록 response에 다음 top-level contract를 고정한다.

```text
review_ui_contract
- run_id
- title
- status
- verdict
- gate_action
- phase
- timeline
- findings
- approval_ledger
- operation_health
- next_actions
- artifact_index
- route_health
- stale
- blocked_by
```

정책:

1. `next_actions`는 CLI와 MCP에서 같은 id/command/reason을 가진다.
2. `timeline`은 길면 최근 N개와 artifact link를 같이 반환한다.
3. `findings`는 blocker/warning/info를 severity별로 분리한다.
4. MCP에서 실행 불가능한 action은 command 대신 `suggested_prompt`를 제공한다.

## 7. 구현 우선순위

### P0 - CLI UX와 안정성 핵심

1. `ReviewTimelineEvent` 생성기 추가
2. final visible summary를 공통 renderer로 통합
3. `ReviewNextAction`과 Action Center 추가
4. `/review status`, `/review timeline`, `/review artifacts` 구현
5. run-level `ReviewOperationHealth` 계산
6. stale review/write gate 차단 강화

완료 기준:

1. 사용자가 report를 열지 않아도 latest run 상태와 다음 행동을 알 수 있다.
2. stale review는 final answer와 MCP에서 같은 방식으로 blocked 처리된다.
3. `go test ./cmd/kernforge -run "Review.*(Timeline|Status|NextAction|OperationHealth|Stale)" -count=1` 통과.

### P1 - 운영 안정성 강화

1. provider route circuit breaker 추가
2. run-level retry budget 추가
3. `/review resume`, `/review doctor` 구현
4. diagnostic bundle 생성
5. concurrent latest pointer update 보호
6. cancellation transition 저장

완료 기준:

1. broken reviewer route는 같은 loop에서 반복 호출되지 않는다.
2. retry budget exhaustion은 자동 반복 대신 next action으로 전환된다.
3. corrupt latest, stale lock, missing artifact를 `/review doctor`가 식별한다.

### P2 - Codex App 체감 격차 축소

1. `/review dashboard` static HTML 생성
2. MCP `review_ui_contract` 확장
3. timeline/report golden snapshot 확대
4. 실제 sample smoke run을 dashboard artifact로 보존
5. route health 추세를 최근 N개 run 기준으로 시각화

완료 기준:

1. CLI, MCP, dashboard가 같은 run id와 gate action을 보여준다.
2. 사용자가 dashboard만 보고 blocker, stale state, next action을 판단할 수 있다.

## 8. 테스트 전략

### 8.1 Unit Tests

추가 테스트 후보:

```text
TestReviewTimelineDerivesFromStateTransitions
TestReviewActionCenterIncludesRecoveryCommandForReviewerTimeout
TestReviewStatusShowsLatestGateAndFreshness
TestReviewArtifactsIndexIncludesRequiredFiles
TestReviewOperationHealthClassifiesStaleRun
TestReviewCircuitBreakerOpensAfterRepeatedTimeouts
TestReviewRetryBudgetStopsRepeatedPatchMismatch
TestReviewDoctorDetectsCorruptLatestPointer
TestReviewResumeBlocksConflictingLatestUserRequest
TestReviewMCPUIContractMatchesCLIActionCenter
```

### 8.2 Replay Fixtures

추가 fixture 범주:

1. reviewer timeout 2회 후 circuit open
2. corrupt latest pointer recovery
3. stale file digest before diff preview
4. cancellation after pre-write review
5. patch mismatch after file changed externally
6. MCP client consumes `review_ui_contract`
7. diagnostic bundle redacts sensitive fields

### 8.3 Golden Tests

golden 대상:

1. `/review status` compact output
2. final visible summary with action center
3. stale review blocked output
4. route circuit breaker warning
5. artifact index markdown

### 8.4 Smoke Tests

최소 smoke:

1. `/review --no-model --path <small file>`
2. `/review status`
3. `/review timeline`
4. `/review artifacts`
5. `/review models status`
6. `/review doctor`
7. MCP `kernforge_review` focused request
8. stale review simulation

전체 회귀:

```text
go test ./cmd/kernforge -count=1 -timeout 20m
```

## 9. 85점 판정표

### UX

| 기준 | 배점 | 85점 조건 |
| --- | ---: | --- |
| 현재 단계 가시성 | 15 | phase, actor, elapsed, next transition이 보인다. |
| 최종 판단 가능성 | 20 | report를 열지 않아도 verdict/finding/evidence/next action을 판단할 수 있다. |
| 승인 경계 | 15 | review/write/commit/push 승인이 분리되어 표시된다. |
| 실패 UX | 15 | timeout/weak/stale/tool failure가 다음 선택지로 연결된다. |
| artifact 탐색성 | 10 | `/review artifacts`와 index가 핵심 파일을 안내한다. |
| MCP/UI 일관성 | 10 | CLI와 MCP가 같은 action contract를 반환한다. |
| 장시간 작업 안정감 | 15 | heartbeat와 cancellation/retry 상태가 보인다. |

85점 도달 조건:

1. 총점 85점 이상.
2. 최종 판단 가능성, 승인 경계, 실패 UX 중 어느 항목도 12점 미만이면 안 된다.
3. stale review 또는 reviewer timeout 시에도 Action Center가 출력되어야 한다.

### 운영 안정성

| 기준 | 배점 | 85점 조건 |
| --- | ---: | --- |
| 상태 일관성 | 20 | timeline, ledger, artifact, MCP가 같은 run state를 가리킨다. |
| provider 장애 격리 | 15 | broken route가 circuit breaker로 격리된다. |
| retry/loop 제어 | 15 | 반복 tool/model/patch failure가 budget으로 제한된다. |
| stale/corrupt 복구 | 20 | stale review, corrupt latest, missing artifact가 deterministic하게 차단/복구된다. |
| diagnostic 가능성 | 10 | 장애 재현 bundle이 secrets 없이 생성된다. |
| concurrency/cancellation | 10 | concurrent run과 cancellation이 ledger에 안전하게 남는다. |
| 회귀 테스트 | 10 | unit/replay/golden/smoke가 핵심 failure mode를 고정한다. |

85점 도달 조건:

1. 총점 85점 이상.
2. stale/corrupt 복구와 상태 일관성은 각각 17점 이상이어야 한다.
3. 전체 회귀와 focused replay suite가 통과해야 한다.

## 10. 권장 구현 순서

1. P0-1: `ReviewTimelineEvent`와 renderer를 먼저 만든다.
2. P0-2: `ReviewNextAction`을 만들고 final visible summary, MCP response에 붙인다.
3. P0-3: `/review status`, `/review timeline`, `/review artifacts`를 추가한다.
4. P0-4: `ReviewOperationHealth`와 stale gate를 final answer/write gate/MCP에 연결한다.
5. P1-1: provider circuit breaker와 retry budget을 route health/loop signature와 연결한다.
6. P1-2: `/review resume`, `/review doctor`, diagnostic bundle을 추가한다.
7. P1-3: corrupt latest/stale lock/cancellation/concurrency fixture를 추가한다.
8. P2-1: static HTML dashboard와 MCP `review_ui_contract`를 추가한다.

이 순서가 좋은 이유는 UX와 운영 안정성이 같은 데이터 모델에서 파생되기 때문이다. 먼저 timeline, action center, operation health를 고정하면 CLI/MCP/dashboard는 같은 상태를 렌더링하는 여러 표면이 된다.

## 11. 비목표

1. Codex App의 전체 GUI를 KernForge 안에 복제하지 않는다.
2. review harness가 사용자 명시 없이 commit/push를 자동 실행하지 않는다.
3. local code review 중 웹 조회를 기본 fallback으로 쓰지 않는다.
4. route health가 나쁘다는 이유만으로 finding severity를 낮추지 않는다.
5. dashboard가 artifact의 원천 데이터가 되지 않는다. 원천은 항상 JSON/JSONL artifact다.

## 12. 최종 기대 상태

이 설계가 구현되면 KernForge review harness는 Codex App 대비 다음 수준으로 올라간다.

| 영역 | 기대 점수 | 설명 |
| --- | ---: | --- |
| UX/제품 경험 | 85-88 | CLI/MCP에서도 세션 상태, 실패 원인, 다음 행동이 Codex App처럼 명확해진다. |
| 운영 안정성 | 85-87 | provider 장애, stale artifact, 반복 loop, 재개 실패가 deterministic하게 처리된다. |
| 전체 제품 성숙도 | 82-85 | full GUI 앱은 아니지만 review harness 단일 기능으로는 제품 후보 수준에 가까워진다. |

핵심은 "더 많은 prompt"가 아니라 "같은 사실을 timeline, ledger, action center, health, artifact로 반복 가능하게 표현하는 것"이다. 이 구조가 잡히면 이후 UI나 dashboard는 단순 렌더링 계층이 되고, 운영 안정성은 테스트 가능한 상태 기계로 관리된다.
