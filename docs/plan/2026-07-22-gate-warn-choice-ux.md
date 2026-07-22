# 작업 계획서: 게이트/WARN을 커맨드 암기 → 선택형 복구로

- 작성일: 2026-07-22
- 상태: 완료 (A+B — Everyday `gate:` 삭제 + final-gate 선택 카드)
- 관련: `PendingHarnessBlockedRecovery` / `offerHarnessBlockedRecoveryChoice`, `runtimeGateRecoveryGuidanceLines`, command-surface Everyday/Hub 정리

## 1. 문제

게이트·WARN은 사용자를 보호하려는 장치인데, 복구 경로가 **슬래시 커맨드 암기**에 의존했다.

- footer: `gate:blocked` / `WARN` 같은 운영자 라벨
- 안내: `방법 1) /review`, `방법 2) /gate clear`, `자세히: /status`
- 사용자는 “왜 막혔는지”보다 “또 무슨 명령을 쳐야 하지?”를 먼저 느낀다

이미 stall 복구(`PendingHarnessBlockedRecovery` + 번호 선택)는 선택형 UX가 있었다. 게이트만 예외적으로 커맨드 메뉴에 남아 있었다.

## 2. 목표

**막기는 유지하되, 풀기는 대화/선택으로.**

1. 사용자 언어로 한 줄 설명 (게이트 용어 최소화)
2. 막히는 순간에만 강하게 개입 (Everyday footer에서 `gate:` 제거)
3. 번호 선택 → 내부에서 `/review`·`/gate clear` 실행 (커맨드는 L3 탈출구)

비목표: 게이트 정책 자체 삭제, Expert 커맨드 제거.

## 3. 적용된 UX

### 3.1 Everyday footer

- `gate:` status pill **완전 삭제** (`operatorStatusItemsWithoutGate`)
- 비ready 시 커맨드 없는 한 줄 CTA + 선택적 이유 줄
- `/status` overview는 gate pill 유지

### 3.2 막히는 순간 (final_gate stall)

`buildFinalGateBlockedRecovery` 선택 카드:

1. 리뷰 갱신하고 계속
2. 이번만 무시하고 계속 (`runtimeGateShouldOfferClear`일 때만)
3. 자세히 보기
4. 지금은 편집만 계속

기존 `offerHarnessBlockedRecoveryChoice`로 번호 선택.

## 4. 구현 메모

- Phase C/D(findings 전용 카드 강화, WARN 상주 추가 축소)는 후속.
- non-interactive: 기존 `renderRuntimeGateBlockedFeedback` remedy 유지.
