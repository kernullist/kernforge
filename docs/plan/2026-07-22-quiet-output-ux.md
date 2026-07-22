# 작업 계획서: Quiet 기본 출력 (Cursor/Codex/Grok 정렬)

- 작성일: 2026-07-22
- 상태: 완료 (Phase A+B 1차)
- 결정: 거슬림 C(중간 잡음→마무리 톤), 기본 모드 Quiet

## 1. 목표

내부 동작 로그 대신 **필요한 내용만** 보이게 한다.

| 표면 | Quiet 기본 |
|------|------------|
| 턴 중간 | spinner/footer만 (shell body·working notes 잔류 없음) |
| 턴 종료 | 최종 답 + 실패/차단; checklist 라벨은 표시만 부드럽게 |
| Expert | `compact` / `auto` / `stream`로 이전 가시성 복구 |

## 2. 구현

1. `progress_display=quiet`를 1급 모드로 분리 (기존 `quiet`→`compact` 별칭 제거).
2. `DefaultConfig` / empty normalize → `quiet`.
3. Quiet: progress persist 없음, `run_shell output:` 드롭, mid-turn thoughts는 transient footer만.
4. Quiet/compact: repair workflow progress를 `N/6 stage` 한 줄로 축소.
5. `softenAssistantDisplayText`: `Validation:` / `Remaining risk:` 등 표시용 완화 (`printAssistant`).
6. completion·help·CHANGELOG 갱신.

## 3. 비범위 (후속)

- 스트리밍 중 chunk 단위 soften
- 모델 프롬프트에서 checklist 형식 자체 제거 (게이트 계약과 충돌 가능)
- WARN 상주 추가 축소 (게이트 선택 UX와 별도)
