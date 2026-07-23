# 작업 계획서: Goal OS (자율 목표 완성 시스템)

- 작성일: 2026-07-23
- 상태: 승인됨 (PR1–PR3 커밋, PR4 리뷰·커밋 직전)
- 관련: [[docs/research/2026-07-23-autonomous-goal-systems.md]]

## 1. 목표

증거 기반으로 목표를 끝까지 닫는 Goal OS. Spec → Slice DAG → 자율 실행 → 검증 → (위험 시) HITL → 부분 납품 → 최종 완료.

## 2. 선택: 안 B

기존 게이트 유지 + Spec Compiler + Slice DAG + slice OPAVR. A(튜닝만)/C(전면 재작성) 기각.

## 3. 아키텍처

AcceptanceSpec + GoalSlice DAG + Runner v2.  
Primary complete = criteria evidence. Process-meta criteria secondary.  
Progress: criterion > verify > content hash (no reset on touch-only churn).  
Skills: goal-to-slice default plan; goal-loop only research_mode; humanize on doc slices.

## 4. PR 스택

1. Spec Compiler + criteria-primary semantic gate (**완료**)
2. Slice DAG model + artifacts + slice planner parse (**완료**)
3. Runner v2 slice OPAVR + partial (**완료**)
4. research_mode + cost/events (**리뷰 직전**)
5. worktree + optional parallel (**보류 가능 — 기존 Session worktree 인프라 재사용 여부 검토**)

Flag: `goal_runner_v2` — PR3에서 SlicePlan 상시 사용. research는 `--research` opt-in + Spec 분류.

## 5. 검증

criteria 없이 process-only complete 불가; multi-slice partial; reject×3 block; legacy smoke.

## 6. 진행 로그

- 2026-07-23: 설계 승인. 연구/계획 문서 작성. PR1 착수.
- 2026-07-23: PR1 구현 — goal_spec.go, AcceptanceSpec, semantic/implement 체크리스트, progress fingerprint 보강, 단위 테스트.
- 2026-07-24: PR1 리뷰 수정 후 커밋 `feat(goal): compile AcceptanceSpec...`
  - 기존 실패 테스트: `review.auto_after_goal_iteration` 기본 false → full-loop 픽스처 정렬.
  - research_mode false positive, progress score 클램프, UTF-8, 목표 변경 시 Spec 재컴파일.
- 2026-07-24: PR2 커밋 `feat(goal): add Slice DAG model and planner parse`
  - ArtifactRefs marshal 순서 수정; slices.md; fallback single-slice.
- 2026-07-24: PR3 커밋 `feat(goal): run slice-scoped OPAVR with partial delivery`
  - running slice resume 수정 (NEEDS_REVISION 후 ready 재선택).
- 2026-07-24: PR4 구현 — GoalEvent, cost 요약, `--research`, implement research section, status 출력.

