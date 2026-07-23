# 연구 노트: 자율 목표 완성 시스템 (2026 동향 + kernforge 현황)

- 작성일: 2026-07-23
- 상태: 결론 도출
- 관련: [[docs/plan/2026-07-23-autonomous-goal-system.md]]

## 질문

kernforge `/goal` 자율 루프를 업계 최고 수준(부분 납품, 증거 기반 완료, 안전 게이트, 계층 계획)으로 올리려면 무엇을 유지·버리고·추가해야 하는가.

## 결론

**강점 = 완료 게이트 체인.** implement → review → verify → completion audit → semantic review, plus no-progress / repeated-failure / absolute ceiling / wall-clock / budgets.  
**약점 = 목표 분해·부분 납품.** 계획이 얇고, goal-to-slice / goal-loop가 러너에 미결합, flat iteration이 모든 크기를 동일 처리.  
2026 SOTA 공통: **Spec → Slice DAG → OPAVR → evidence complete → HITL only for irreversible.**  
다음 단계: 게이트 유지 + Spec Compiler + Slice DAG + slice-scoped runner.

## 현재 루프 (요약)

`/goal` → GoalState → `runGoalLoop` → `runGoalIteration`:
checkpoint → implement → review/repair → verify → audit → semantic → complete|repair|recover|block.

## 갭 G1–G11

G1 얇은 plan · G2 no DAG · G3 slice skill 미결합 · G4 research skill 미결합 · G5 process-meta criteria · G6 churn progress · G7 no partial ship · G8 weak model routing · G9 weak observability · G10 no worktree-first · G11 no external deliverable criteria.

## 2026 동향

Claude Code / Cursor Plan / Devin / Intent: observe-plan-act-evaluate; living specs; worktree isolation; ticket→PR autonomy spectrum.  
Avoid: unbounded web every loop, model-only done, mega-patch, HITL on every safe choice.

## 반증

- goal-loop default-on: too expensive for local bugfix  
- cloud sandbox mandatory: conflicts with local/security workspace  
- remove audit/semantic: destroys trust  

## 레퍼런스

- `cmd/kernforge/goals.go`, `goals_runtime.go`, `goal_tools.go`
- skills `goal-loop`, `goal-to-slice-planner`
- QUICKSTART goal section

## 미해결 (구현 기본값)

- speed=full-auto, strict=gated 권장 분리
- high-risk → require-review 권장
- parallel slices → PR5
- research_mode default none; `--research` opt-in
