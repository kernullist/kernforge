---
name: parallel-agents
description: >
  KernForge 내장 멀티 에이전트 오케스트레이션. 복잡한 작업을 나눠 read-only 조사
  서브태스크(spawn_task)를 병렬로 띄우고, 서로의 결과를 교차 검증하게 한 뒤, 메인
  에이전트가 최종 결과를 종합한다. 독립 검증이 필요한 작업에 쓴다: 코드 리뷰(보안/정확성/성능),
  아키텍처 결정, 버그 조사, 종합 분석. "thorough review", "comprehensive analysis",
  "double-check", "철저히 리뷰", "교차 검증", "여러 관점으로 확인" 같은 요청, 또는 한 번에
  훑으면 놓칠 만큼 복잡한 작업에서 트리거한다. 안티치트 코드 리뷰, 커널 드라이버 분석,
  보안 감사, 리팩터링 계획처럼 품질과 정확성이 중요한 작업에 적극적으로 쓴다.
---

# Parallel Agents Skill (KernForge 내장)

여러 개의 read-only 조사 서브태스크를 KernForge 안에서 오케스트레이션한다: 워커가 작업을 나눠
맡고, 리뷰어가 서로의 발견을 교차 검증하며, 메인 에이전트가 최종 결과를 종합한다.

## KernForge 도구 매핑 — 먼저 읽어라

- 서브태스크는 `spawn_task`로 띄운다. 이 서브태스크는 **read-only 전용**이다: `read_file`/`grep`/`list_files`/`lsp_nav`만 쓸 수 있고 파일 편집·셸·git은 못 한다. 따라서 이 스킬은 **분석/리뷰/조사** 오케스트레이션에 이상적이다.
- 상태·결과 회수는 `get_task`, 열거는 `list_tasks`, 취소는 `cancel_task`.
- `spawn_task`는 objective(조사 목표) 문자열을 받아 task_id를 즉시 반환한다(논블로킹). 워커들을 한 번에 여러 개 띄운 뒤 각 task_id를 `get_task`로 폴링해 결과를 모은다.
- **쓰기가 필요한 작업**(코드 구현·수정)은 서브태스크가 못 한다. 그런 경우 서브태스크에는 조사·설계·리뷰만 맡기고, 실제 편집(`write_file`/`replace_in_file`/`apply_patch`)과 빌드/테스트(`run_shell`)는 메인 에이전트가 종합 결과를 바탕으로 직접 수행한다.
- 서브태스크는 read-only이므로 공유 작업 트리를 오염시키지 않는다. 커밋 전 미커밋 코드를 리뷰할 때도 안전하다.

## 오케스트레이션 흐름

```
Main Agent (you)
  │
  ├─ [Phase 1: 병렬 워커 — spawn_task 여러 개]
  │    ├─ Worker A  →  Output A
  │    ├─ Worker B  →  Output B
  │    └─ Worker C  →  Output C  (선택)
  │
  ├─ [Phase 2: 교차 검증 — 각 리뷰어는 자신을 뺀 다른 워커 결과만 본다]
  │    ├─ Reviewer 1  reviews B + C  →  Feedback on B, C
  │    ├─ Reviewer 2  reviews A + C  →  Feedback on A, C
  │    └─ Reviewer 3  reviews A + B  →  Feedback on A, B  (선택)
  │
  └─ [Phase 3: 종합]
       충돌 해소 → 중복 제거 → 우선순위 → 최종 출력
```

## Step 1 — 작업 분해

무엇을 띄우기 전에 먼저 정한다:

| 질문 | 답 |
|---|---|
| 워커 몇 개? | 보통 2~3개. 각자 의미 있게 다른 각도여야 한다. |
| 각 워커의 범위는? | 서로 겹치지 않되, 합치면 전체를 덮는다. |
| 모든 워커가 공유할 컨텍스트는? | 각 objective에 그대로 넣는다. |
| 출력 형식은? | 종합이 쉽도록 미리 정한다. |

**흔한 분해:**

- **코드 리뷰** → Worker A: 로직/정확성, Worker B: 보안, Worker C: 성능
- **아키텍처** → Worker A: 옵션 X 심층, Worker B: 옵션 Y 심층, Worker C: 트레이드오프
- **버그 조사** → Worker A: 한 서브시스템, Worker B: 다른 서브시스템, Worker C: 통합 계층
- **분석** → Worker A: 정적 분석, Worker B: 런타임/동작 관점, Worker C: 외부 표면

자세한 분해 예시는 `references/decomposition-patterns.md`를 참조한다.

## Step 2 — 워커 서브태스크 띄우기 (spawn_task 병렬)

워커들을 동시에 띄운다(각 `spawn_task` 호출은 논블로킹으로 task_id를 즉시 반환). 각 objective는:
1. 에이전트의 **구체적 역할과 범위**를 명시한다(무엇에 집중하고 무엇을 무시할지).
2. **필요한 컨텍스트**를 objective 안에 넣는다(리뷰 대상 파일 경로, 요구사항, 제약). 서브태스크는 read_file/grep로 워크스페이스를 읽을 수 있으니 파일 경로를 주면 스스로 읽는다.
3. **출력 형식**을 지정한다(제목, 심각도 라벨 등).
4. 에이전트에게 **비판적이고 정직하게**, 동조하지 말라고 지시한다.

```
[워커 objective 템플릿]

너는 [ROLE] 에이전트다. 오직 [SPECIFIC_SCOPE]에만 집중하라.
범위 밖 영역은 다루지 마라.

=== 대상 ===
[리뷰할 파일 경로 / 요구사항 / 제약. 경로를 주면 read_file로 직접 읽어라.]

=== 할 일 ===
[정확히 무엇을 할지]

=== 출력 형식 (필수) ===

## Findings
- [구체적 발견] | Severity: CRITICAL / HIGH / MEDIUM / LOW

## Issues
1. [이슈 설명] — Severity: HIGH
   Location: [file:line 또는 함수]
   Reason: [왜 문제인가]

## Recommendations
- [실행 가능한 수정 또는 개선]

## Summary
[2~3문장]
```

## Step 3 — 리뷰어 서브태스크 띄우기 (워커 완료 후 병렬)

각 리뷰어는 **다른 워커들의 결과**를 받는다(자기 것은 절대 안 봄 — 교차 검증만). 워커 결과 텍스트를
리뷰어 objective 안에 그대로 넣는다.

```
[리뷰어 objective 템플릿]

너는 리뷰어 에이전트다. 원본 코드/작업을 직접 리뷰하는 게 아니다.
아래 에이전트 출력들의 품질과 정확성을 리뷰하라.

=== 리뷰할 출력들 ===
[Agent A Output]
---
[Agent B Output]

=== 할 일 ===
1. 동의하는 발견 (왜 맞는지)
2. 동의하지 않는 발견 (그들 논리의 결함을 설명)
3. GAPS — 두 에이전트가 모두 놓친 중요한 이슈
4. 심각도를 잘못 매긴 것

=== 출력 형식 (필수) ===

## Validated Findings
- [발견] — Confirmed: [동의 이유]

## Challenged Findings
- [발견] — Incorrect because: [당신의 논리]

## Gaps Identified
- [아무도 못 잡은 이슈] — Severity: [레벨]

## Quality Score
Agent A: [N/10] — [한 줄 이유]
Agent B: [N/10] — [한 줄 이유]
```

## Step 4 — 최종 종합

메인 에이전트가 종합한다. 그냥 이어붙이지 마라.

```
종합 체크리스트:
☐ 모든 워커 출력과 리뷰어 피드백을 get_task로 수거
☐ 충돌마다(워커 발견 vs 리뷰어 반박) 어느 쪽이 맞는지 판정
☐ 겹치는 발견은 가장 정확한 서술만 남기고 중복 제거
☐ 심각도로 정렬: CRITICAL → HIGH → MEDIUM → LOW
☐ 강한 이견이 있으면 표시(불확실성을 사용자에게 드러냄)
☐ 사용자가 원래 요청한 형식으로 정리
```

**표준 종합 출력 구조:**
```
# [Task] — 멀티 에이전트 분석

## Critical (배포 전 반드시 수정)
## High (수정 권장)
## Medium (다룰 만함)
## Low / Style / Suggestions

## 에이전트 간 이견
[에이전트들이 크게 충돌했을 때만 — 불확실성을 드러냄]

## 확신도
[High / Medium / Low — 에이전트들이 얼마나 일치했는지 기준]
```

바로 쓸 수 있는 역할별 objective 템플릿은 `references/prompt-library.md`를 참조한다.

## 핵심 원칙

- **워커는 독립적이다** — 공유 대화 없음, 워커 간 조율 없음.
- **충돌은 신호다** — 에이전트들이 이견을 보이면 드러내라. 조용히 하나만 고르지 마라.
- **리뷰어는 자기 리뷰 금지** — 항상 교차 검증만.
- **결론은 네가 낸다** — 종합은 메인 에이전트 몫. 에이전트는 조언하고, 너는 결론짓는다.
- **컨텍스트가 왕이다** — 각 objective에 필요한 컨텍스트를 다 넣어라. 서브태스크는 이 대화의 기억이 없다.
- **쓰기는 메인에서** — 서브태스크는 read-only다. 구현·수정·빌드는 종합 후 메인 에이전트가 직접 한다.

## 언제 쓰지 않나

- 단순하고 빠른 1스텝 작업(오버헤드 > 이득)
- 사용자가 명시적으로 빠른 답을 원할 때
- 모든 에이전트가 뻔하게 같은 결과를 낼 때(의미 있는 분해 불가)
- 실행 중 사용자와의 실시간 왕복이 필요한 작업

## 참조 파일

- `references/decomposition-patterns.md` — Tavern/커널/UE5 작업용 분해 예시
- `references/prompt-library.md` — 흔한 에이전트 역할용 objective 템플릿
