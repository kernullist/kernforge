# 작업 계획서: 자동 검증 out-of-scope 하드 중단 제거 및 scope 판정 일관화

- 작성일: 2026-07-19
- 상태: 완료 (2026-07-19)
- 관련 문서: [[docs/CHANGELOG.md]]

## 1. 목표 / 배경

증상(사용자 보고 로그, 2026-07-19):
- 모델이 `cmd/kernforge/main_cancel_test.go` 패치 후 자동 검증(`go test ./cmd/kernforge/...`)이 `[compile_error]`로 실패.
- scope 판정이 "현재 patch scope 밖"으로 나오면서 턴이 즉시 **최종-답변-전용**으로 전환 (`verificationOutOfScopeFinalOnly`).
- 이후 모든 도구 호출이 `NOT_EXECUTED`로 차단되고, 도구 노출 자체가 제한되어 모델이 `<tool_call>` 텍스트를 그대로 출력하는 부작용 발생.
- 그런데 상태바는 `gate:blocked (1 blockers)` + `verify:failed` — 게이트는 실패를 in-scope blocker로 보고 있어 **아무것도 못 하는 교착** 상태가 됨.

코드 분석으로 확인한 근본 원인 3가지:

1. **턴 판정 vs gate 판정의 scope 불일치**: 턴 시점 `verificationFailureRepairScope`는 현재 턴 patch transaction + 마지막 user 메시지 이후 편집 경로만 사용 (`verificationRepairChangedPaths`). 반면 runtime gate ledger는 final answer 시점에 git changed files fallback까지 사용 (`runtimeGateChangedPathsForAction` → `includeGitChanged`). 같은 실패를 턴은 out-of-scope, gate는 in-scope로 판정해 교착 발생.
2. **패키지 단위 compile_error 오판정**: `verificationStepIsPatchScoped`는 실패 라인/명령이 변경 path(전체 경로 또는 basename)를 직접 언급해야만 in-scope. `go test ./cmd/kernforge/...`처럼 명령이 변경 파일이 속한 디렉터리를 겨냥하는 경우, 같은 패키지 다른 파일의 컴파일 에러는 patch 검증 자체를 막으므로 사실상 in-scope인데 out-of-scope로 빠짐. (Go/Rust/C++ 모두 패키지/타깃 단위 컴파일)
3. **하드 중단 정책**: out-of-scope 판정 즉시 `verificationOutOfScopeFinalOnly=true`로 모든 후속 도구를 차단. 사용자 요구("최대한 중단 없이 요청 작업을 완료")와 정면충돌. 기존 retry budget(같은 실패 fingerprint 2회 → change_strategy, 3회 → escalate_reviewer)이 이미 루프 상한 역할을 하므로 하드 블록은 중복 안전장치.

목표: 자동 검증 실패가 나도 kernforge가 턴을 강제 종료하지 않고, scope 판정이 일관되며, 무한 수리 루프는 기존 retry budget으로 제어한다.

## 2. 범위

- 포함:
  - `cmd/kernforge/verification_repair_scope.go`: 디렉터리/패키지 단위 scope 매칭 추가.
  - `cmd/kernforge/agent.go`: out-of-scope 시 final-only 하드 블록 제거, guided continuation으로 교체.
  - `cmd/kernforge/runtime_gate_ledger.go`: 턴 판정과 gate 판정의 scope 기준 일관화.
  - 관련 테스트(`agent_verify_loop_test.go` 등) 업데이트 + 신규 회귀 테스트.
- 제외(이번에 안 하는 것):
  - 검증 승인 prompt lifecycle(`Run automatic verification now?`), skipped/declined 흐름은 그대로 유지.
  - `automaticVerificationSkippedFinalOnly`(사용자가 검증을 거부한 경우)는 유지 — 사용자 명시 거부는 존중.
  - retry budget 정책 자체(2회/3회 임계값) 변경 없음.
  - UI/렌더링 변경 없음.
- 전제 조건 / 의존성:
  - `EditLoopState.buildRetryDecision`의 fingerprint 기반 escalation이 유일한 루프 상한 장치가 됨.

## 3. 접근안 비교

| 안 | 요약 | 장점 | 단점 | 리스크 |
|---|---|---|---|---|
| A | out-of-scope 분기 자체를 삭제하고 모든 실패를 in-scope repair loop로 통합 | 가장 단순, 교착 원천 제거 | 진짜 환경성 실패(다른 프로젝트 깨짐)에도 모델이 무관한 파일을 고치려 할 수 있음 | scope 확장 남용, 토큰 낭비 |
| B | 하드 블록만 제거하고, out-of-scope 메시지를 "필요하면 수리 계속, 무관하면 risk 고지 후 마무리" 안내로 교체. 판정 로직(디렉터리 매칭)도 보정 | ambient 신호는 유지하면서 중단 제거. 모델 판단에 위임 + retry budget 상한 | 모델이 안내를 무시하고 무관한 수리를 할 여지는 남음(기존에도 guidance는 항상 무시 가능) | 중간 |
| C | 판정 로직만 고치고(디렉터리 매칭), 하드 블록은 유지 | 변경 최소 | gate/턴 불일치 교착은 남고, "중단 없이 완료" 요구를 충족 못 함 | 교착 재발 |

**선택: B + 사용자 확인 프롬프트 (2026-07-19 승인)**
선택 근거: A는 원래 설계 의도(ambient 워크스페이스 실패로 무관한 프로젝트 수리 금지)를 완전히 버리는 것이라 과함. C는 사용자의 핵심 불만(중단)을 해결 못 함. B는 (1) 판정 오류를 고쳐서 하드 블록이 발동할 이유 자체를 줄이고, (2) 그래도 ambient 판정이 나오면 하드 블록 대신 안내+retry budget으로 제어해 "중단 없이 완료"를 만족한다.
추가 결정(사용자 승인 시): out-of-scope 판정이 나오면 **대화형 실행에서는 사용자에게 진행 여부를 묻는다**(계속 수리 / 환경성 risk로 기록하고 마무리). 기존 `PromptResolveAutoVerifyFailure`(검증 도구 경로 실패 시 retry/disable 질의)와 같은 패턴을 사용한다. 비대화형(`-prompt -y` 등)에서는 물을 수 없으므로 B안 guided continuation(모델이 안내에 따라 판단, retry budget 상한)으로 폴백한다. 사용자가 "마무리"를 선택한 경우에만 기존처럼 최종 답변으로 수렴한다.

## 4. 구현 단계

1. `verification_repair_scope.go`:
   - `verificationStepIsPatchScoped`에 디렉터리 매칭 추가: step.Command/Scope(정규화 후)가 변경 path의 **디렉터리**를 포함하면 in-scope. (예: `go test ./cmd/kernforge/...` vs changed `cmd/kernforge/main_cancel_test.go`)
   - compile_error/빌드 실패에서 "같은 패키지 어디든 컴파일 에러는 patch 검증을 block한다"는 주석 유지.
2. `agent.go`:
   - out-of-scope 분기(4641행 부근): 대화형이고 프롬프트 훅이 있으면 사용자에게 진행 여부 질의 (신규 resolution: 계속 수리 / risk 기록 후 마무리). "계속 수리" 선택 또는 비대화형 폴 시 guided continuation으로 진행 — `verificationOutOfScopeFinalOnly`를 설정하지 않고 repair guidance 메시지만 추가(기존 retry budget이 루프 상한).
   - `automaticVerificationOutOfScopeMessage`를 guided continuation 문구로 교체: "실패가 patch와 무관해 보이면 risk 고지 후 마무리, 요청 완료에 필요하면(예: 패키지가 컴파일되지 않음) 수리 계속. 무관한 파일로 범위를 넓히지 말 것."
   - 3359행/3434행의 final-only 블록과 verification-retry `NOT_EXECUTED` 블록 제거, 관련 guidance 함수(`verificationOutOfScopeFinalOnlyGuidance` 등)와 `ensureOutOfScopeVerificationFinalDisclosure` 경로 정리.
   - `buildTurnToolExposurePlanForEnvelope`의 `verificationOutOfScopeFinalOnly` 도구 제한 제거.
   - `request_runtime_shadow.go` 시그니처 정리.
3. `runtime_gate_ledger.go`: gate 판정 시 `verificationFailureTouchesChangedPaths`에 넘기는 changed-path 집합을 턴 판정과 동일 기준으로 맞추거나, 최소한 1번의 디렉터리 매칭이 양쪽에 모두 적용되도록 공통 함수 사용 확인. 교착(턴은 완료 허용, gate는 blocker)이 재발하지 않음을 확인.
4. 테스트:
   - 신규: 디렉터리 매칭으로 `go test ./cmd/kernforge/...` + changed `cmd/kernforge/x_test.go` + 다른 파일 compile_error → in-scope(ShouldRepair=true).
   - 신규: out-of-scope 실패 후에도 도구 호출이 차단되지 않고 계속 진행되는 회귀 테스트.
   - 기존 out-of-scope 하드 블록 가정 테스트(`agent_verify_loop_test.go` 1804~1945행 부근 등) 새 동작에 맞게 수정.
5. `go test ./cmd/kernforge/...` 전체 실행.

## 5. 위험 및 실패 경로

- 위험 1: 디렉터리 매칭이 너무 넓어 workspace 전체 명령(`go test ./...`)에서도 항상 in-scope 판정. → `./...`같은 전체 패턴은 디렉터리 매칭에서 제외(구체 경로 세그먼트가 있을 때만 매칭).
- 위험 2: 하드 블록 제거 후 모델이 무관한 프로젝트 수리로 새는 경우. → retry budget이 같은 실패 fingerprint 반복을 change_strategy/escalate_reviewer로 전환하고, guidance에 "무관하면 고지 후 마무리"를 명시. 기존에도 guidance는 강제력이 없었으므로 실질 리스크 증가는 제한적.
- 위험 3: 기존 테스트 대량 수정 필요. out-of-scope 가정 테스트가 여러 파일에 퍼져 있음(`agent_verify_loop_test.go`, `ui_test.go`, `proactive_suggestions_test.go` 등). → 동작이 바뀌는 테스트만 최소 수정, 문구만 검사하는 테스트는 유지.
- 실패 시 증상: `gate:blocked` + 도구 차단 교착 재발, 또는 무한 수리 루프(turn 상한 도달).
- 롤백: 세 파일 단위로 git revert 가능. 설정/포맷 변경 없음.

## 6. 검증 방법

- `go test ./cmd/kernforge/...` 전체 통과.
- 신규 회귀 테스트:
  1. 패키지 디렉터리를 겨냥한 `go test` compile_error가 in-scope로 판정되는지 단위 테스트.
  2. out-of-scope 실패 주입 후 모델의 다음 도구 호출이 `NOT_EXECUTED`가 아니라 실제 실행되는지(또는 최소 차단되지 않는지) agent loop 테스트.
- 수동 시나리오(가능하면): 로그와 같은 상황 재현 — test 파일 1개 패치 + 패키지 내 다른 파일 compile error → 턴이 final-only로 닫히지 않고 수리를 계속하는지 확인.

## 7. 오픈 이슈

- [x] out-of-scope 메시지 문구를 "수리 계속 허용"으로 바꿀 때, 사용자가 명시적으로 범위를 제한한 경우(예: "이 파일만 고쳐")와 충돌하지 않는지 — guided continuation 문구에 "사용자 요청 기준으로 판단"을 명시하고, 무관한 범위 확장 금지 조항을 유지하는 것으로 처리. 사용자 범위 제한 지시는 세션 프롬프트 계층에서 별도로 강제되므로 이 메시지가 덮어쓰지 않음.
- [x] `verificationOutOfScopeThisTurn`을 완전 제거할지 — 완전 제거로 결정. verification-retry 완충은 edit-loop retry budget(같은 fingerprint 2회 전략 변경, 3회 리뷰어 에스컬레이션)에 위임.
- [ ] (후속, 별도 작업) gate ledger의 changed-path 집합(git changed files fallback)은 여전히 턴 판정 집합보다 넓다. 하드 스톱 제거로 교착은 해소됐지만, 사용자가 Finish를 선택한 ambient 실패는 상태바 `gate:blocked`를 남길 수 있다(완료/git-write에 대한 advisory이며 턴 반환은 막지 않음). 사용해 보고 거슬리면 gate 쪽도 턴과 같은 scope 집합을 쓰도록 좁히는 안을 검토.
- [ ] (후속, 별도 작업) 최종 답변이 게이트에 막힌 뒤 재시도 과정에서 note 수준 evidence_gap finding(RF-001)이 "latest review has unwaived blockers"로 표면화하는 경로가 있다. 게이트 의도(참고 수준은 비차단)와 표현이 어긋나 보이므로 별도 조사 필요.

## 8. 진행 로그

- 2026-07-19: 사용자 보고 로그 분석. `verification_repair_scope.go`/`agent.go`(final-only 블록)/`runtime_gate_ledger.go`(866~897행) 대조해 근본 원인 3가지 확정. 계획서 작성.
- 2026-07-19: B안 + 사용자 확인 프롬프트로 승인. 구현 완료:
  - `verification_repair_scope.go`: Go 패키지 scope 디렉터리 매칭 추가(`verificationGoPackageScopeCoversChangedDir`). **결정: Go 전용으로 제한** — 범용 디렉터리 매칭은 `TestVerificationRepairScopeDoesNotTreatProjectBuildSiblingFailureAsPatchScoped`(MSBuild sibling 실패는 의도적으로 ambient)를 깨뜨리므로, `go test|build|vet` + 구체 package scope(`./cmd/app/...` 형태, workspace/`./...` 제외)에만 적용. 경계 인식 매칭(`verificationTextMentionsPathSegment`)으로 `cmd/app2` ≠ `cmd/app`.
  - `agent.go`: `PromptResolveOutOfScopeVerification` 훅 + `OutOfScopeVerificationResolution`(continue_repair/finish). out-of-scope 시 턴당 1회만 질의(캐시), Finish만 기존 터미널 UX(고지 노트 + post-change review 스킵)로 수렴 — 도구 차단 없음. `verificationOutOfScopeFinalOnly/ThisTurn` 및 관련 NOT_EXECUTED 블록·guidance·blocked-result 빌더 전량 제거. `buildTurnToolExposurePlan(ForEnvelope)`/`observeRequestRuntimeShadow` 시그니처에서 해당 인자 제거.
  - `main.go`: `promptResolveOutOfScopeVerification` 구현(pinned prompt, `[y=keep repairing (default), n=finish and disclose as ambient risk, Esc=cancel]`, 비대화형 NoAction 폴).
  - `coding_harness.go`: "Unresolved verification failure" blocker가 `patchScopedVerificationFailures`일 때만 발동하도록 수정 — out-of-scope 실패가 guided continuation(`unresolvedVerification=true`)에서 in-scope 문구 blocker로 오표시되던 문제 해소.
  - gate 일관성: 구조 변경 없음을 확인. gate blocker는 턴 반환을 막지 않고(완료/git-write advisory), 교착은 final-only에서만 왔음. 디렉터리 매칭은 `verificationStepIsPatchScoped` 공통 함수라 양쪽 모두에 적용됨.
  - 테스트: 기존 4개를 새 동작에 맞게 재작성(Allows~ 2개, Finish 프롬프트 경로 1개, DoesNotBroaden 유지 1개) + 신규 4개(ContinueRepair 프롬프트, Go 패키지 compile in-scope, workspace go run ambient 유지, path segment 경계). 주의: 게이트 통과용 최종 답변은 "verification failed"/"ambient" 등 인정 문구가 필요(`replyMentionsVerificationBlocker`의 "no known remaining blocker" 가드 때문).
  - 문서: README.md/README_kor.md/FEATURE_USAGE_GUIDE.md/FEATURE_USAGE_GUIDE_kor.md의 NOT_EXECUTED 서술을 새 동작으로 갱신.
- 2026-07-19: 검증. `go build ./...`, `go vet` 클린. `go test ./cmd/kernforge/` 전체: 6개 실패 모두 기존 결함 — `TestAgentSuppressesDuplicateToolPreambleEmitsWithinATurn`, `TestCommandSpecsHaveHelpCoverage`(내 변경 stash 상태에서도 동일 실패 확인), LSP 4개(`gopls` 미설치 환경). 회귀 0.
