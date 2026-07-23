# 작업 계획서: `/goal` 단일 명령 UX

- 작성일: 2026-07-24
- 상태: 완료
- 관련: [[docs/plan/2026-07-23-autonomous-goal-system.md]]

## 요약

공개 표면을 `/goal` 하나로 통일. 설계(plan/slice) 직후 자율 루프. 진행 중 상태 스냅샷 출력.

## 계약

| 입력 | 동작 |
|------|------|
| `/goal <objective>` | create → plan → run loop |
| `/goal @file` | 동일 |
| bare `/goal` | incomplete 재개 / 스냅샷 |
| `run|status|…` | removed 안내 |
| `--no-run` | hard error |
| Esc | interrupt, goal active |

내부: `recordGoalWithoutLoop` (테스트), `runGoalBySelector` / complete·audit (내부 유지).

## 진행 로그

- 2026-07-24: 구현·테스트 통과·CHANGELOG 반영.
