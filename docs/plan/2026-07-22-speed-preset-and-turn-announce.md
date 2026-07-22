# 작업 계획서: Speed 기본 프리셋 + 턴 시작 안내

- 작성일: 2026-07-22
- 상태: 완료
- 관련: Quiet 출력, 게이트 선택 UX

## 목표

같은 모델 대비 체감 지연을 줄이고, 턴 시작 시 “무엇을 할지”를 먼저 보여 준다.

## 기본(speed) 변경

1. `auto_verify=false`
2. semantic classifier `disabled`
3. `review.auto_after_change` / `auto_after_goal_iteration=false` (git write 전 리뷰는 유지)
4. `inject_project_analysis=false`, `auto_compact_chars=90000`
5. 턴 시작 `announceTurnPlan`: 하드코딩 없음. API는 thinking 없이 짧은 preflight(최대 8s) → durable thought 줄. CLI/Codex는 preflight 생략 후 본 턴 첫 줄을 thought로 승격.

## 프리셋

`/settings preset speed|balanced|strict`

| preset | verify | classifier | auto review | analysis inject |
|--------|--------|------------|-------------|-----------------|
| speed (기본) | off | off | off | off |
| balanced | off | off | on | off |
| strict | on | on | on | on |

## 복구

이전 안전망: `/settings preset strict`
