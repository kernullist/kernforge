# 작업 계획서: final_answer 게이트의 changed-path scope를 턴 판정 기준으로 좁히기

- 작성일: 2026-07-21
- 상태: 완료 (2026-07-22)
- 관련 문서: [[docs/plan/2026-07-19-verify-outofscope-hardstop.md]] §7 후속 이슈 1, [[docs/research/overblock-audit.md]] (F3)

## 1. 목표 / 배경

2026-07-19 계획서의 후속 이슈 1. 게이트 ledger의 changed-path 집합(`runtimeGateChangedPathsForAction`)이 턴 판정 집합(`verificationRepairChangedPaths`)보다 넓었다:

- 턴 판정: 현재 턴 patch transaction + 최근 세션 변경 + verification report의 changed paths. git changed-files fallback 없음.
- 게이트(final_answer): patch paths가 비어 있고 edit 의도 턴이면 `runtimeGateFinalAnswerShouldUseGitChangedFallback`을 통해 **git의 모든 변경 파일**을 scope에 포함.

결과: 세션 작업과 무관한 dirty 파일(사용자의 별도 WIP, 이전 세션 잔여 변경)이 있으면 stale review / unwaived blockers / verification-failed blocker가 무관한 파일에 대해 발동해 상태바 `gate:blocked` advisory가 남았다. 턴 반환은 막지 않지만 git write와 completion readiness를 막고, 7/19에 해소한 "턴은 완료 허용, 게이트는 blocker" 비대칭의 잔재였다.

목표: final_answer 게이트의 scope가 턴 판정과 같은 기준(추적된 patch scope)을 쓰게 해서, ambient dirty 파일로 인한 `gate:blocked`를 없앤다.

## 2. 범위

- 포함:
  - `runtime_gate_ledger.go`: final_answer 액션의 git changed-files fallback 제거.
  - 회귀 테스트(기대 반전 + fixture에 current-turn patch transaction 보강).
- 제외:
  - git_write/mcp_write의 git fallback — 트리 전체를 커밋/쓰기하는 액션이라 전체 scope가 의도. 변경 없음.
  - review/completion_audit 액션의 archived patch scope — 변경 없음.
- 전제:
  - 미추적 편집(도구가 changed_paths를 기록하지 않은 변경)은 patch transaction unknown-scope blocker와 coding harness diff-aware check가 별도로 잡는다. git_write/mcp_write 시점에는 전체 scope 게이트가 그대로 적용된다.

## 3. 접근안 비교

| 안 | 요약 | 장점 | 단점 | 리스크 |
|---|---|---|---|---|
| A | final_answer의 git fallback 제거 (턴 집합과 동일화) | 가장 단순, 비대칭 완전 해소 | 수동/미추적 사용자 편집이 final-answer 게이트를 완전 우회 | 안전망 약화(무경고) |
| B | fallback 유지 + 세션 활동 관련 파일만 필터 | 정밀 | "관련" 정의가 휴리스틱 덩어리 | 오탐/미탐 양쪽 |
| C | fallback 유지하되 git-fallback 유래 scope는 blocker→warning 강등 (provenance 구분) | 추적 scope는 hard-block 유지, ambient scope는 가시성(warning) 확보 | ledger에 경로 provenance 구분 추가 | 중간 |

**선택: A (2026-07-21 사용자 결정 — C 초안에서 변경)**
변경 사유: 사용자가 단순화를 선택. C의 provenance 구분은 ledger 구조/렌더 파급이 크고, A는 "final_answer 게이트 scope = 턴 판정 집합"이라는 하나의 규칙으로 수렴해 7/19 비대칭의 원인을 제거한다. A의 단점(미추적 수동 편집이 final-answer 게이트를 무경고 우회)은 사용자가 수용 — 미추적 변경은 patch transaction unknown-scope blocker와 coding harness diff-aware check가 별도로 잡고, git_write/mcp_write 시점에는 전체 scope 게이트가 그대로 적용되므로 쓰기 전 안전망은 유지된다.

## 4. 구현

1. `runtime_gate_ledger.go`:
   - `runtimeGateChangedPathsForAction`에서 final_answer일 때 `includeGitChanged = false`.
   - `runtimeGateFinalAnswerShouldUseGitChangedFallback` 삭제.
2. 테스트:
   - ambient dirty + edit intent + stale review → final_answer는 empty scope / ready (구 blocker 기대 반전).
   - archived patch + ambient dirty → empty scope / ready.
   - preserved code continuation → git fallback 미사용.
   - 추적 patch scope가 필요한 status/hooks/dismissal fixture에 current-turn patch transaction 추가.
   - git_write는 기존 전체 scope 유지(컨트롤, 기존 테스트).
3. 문서: 2026-07-19 §7 후속 이슈 1 `[x]`, overblock-audit F3 `[x]`, CHANGELOG.

## 5. 위험 및 실패 경로

- 위험: 사용자가 진짜로 수동 편집한 파일이 final-answer 게이트를 우회. → 7/19 정책 방향(중단 없이 완료 + 고지)과 일치. git_write 시점에는 전체 scope 게이트가 다시 적용.
- 실패 시 증상: ledger 테스트 다수 실패(기대가 git fallback을 전제).
- 롤백: ledger + 관련 테스트 파일 단위 revert.

## 6. 검증 방법

- `go test ./cmd/kernforge/ -run 'RuntimeGate|GateClear|OperatorStatusCompact|HooksStatusIncludesRuntimeGate'`
- `go build ./...`, `go vet ./cmd/kernforge/`
- 수동(가능하면): 무관 dirty 파일 + 이전 세션 stale review가 있는 워크스페이스에서 final answer → 상태바 `gate:blocked`가 ambient만으로 뜨지 않는지 확인.

## 7. 오픈 이슈

- [ ] dirty 상시 워크스페이스에서 git_write 게이트 warning/blocker 피로 — final_answer와 무관, 별도 관찰.

## 8. 진행 로그

- 2026-07-21: 2026-07-19 후속 이슈 1 분석. `runtimeGateChangedPathsForAction`/`verificationRepairChangedPaths` 대조로 폭 차이 확정(감사 F3와 동일). A/B/C 비교 후 C안 초안 작성 → 사용자 결정으로 A안 채택.
- 2026-07-22: A안 구현. final_answer git fallback 제거, `runtimeGateFinalAnswerShouldUseGitChangedFallback` 삭제, 회귀 테스트 기대 반전 및 fixture 보강. 문서/CHANGELOG 갱신.
