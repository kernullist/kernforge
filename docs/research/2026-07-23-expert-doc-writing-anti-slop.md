# 연구 노트: 문서 작성 시 전문가 문체 / AI slop 제거

- 작성일: 2026-07-23
- 상태: 결론 도출
- 관련: [[docs/plan/2026-07-23-expert-document-writing-style.md]]

## 질문

kernforge가 문서를 쓸 때 AI 말투(slop)가 아니라 해당 분야 전문가가 쓴 것처럼 나오게 하려면, 어떤 최신 기법·체크리스트를 생성 시점과 후처리 시점에 각각 반영해야 하는가.

## 결론 (먼저 쓴다)

**생성 시 예방이 1순위, 후처리 humanize는 2순위.**  
이미 `humanize-doc` 스킬과 `references/ai-tells.md`가 후처리 루프로 잘 갖춰져 있으나, document-authoring 턴의 시스템 프롬프트에는 “산출물을 써라”만 있고 **문체 계약이 없다**. Peter Yang의 [no-ai-slop](https://github.com/petergyang/no-ai-slop)(2026-07, MIT)이 20+ 패턴 + `eval.md` 자기 검수 루프를 공개했고, Microsoft Writing Style Guide / Google tech writing의 능동태·구체성·plain language와 합치면 “문서 전용 컴팩트 가이드”로 주입하는 것이 비용 대비 효과가 가장 크다. 매 문서 작성에 full humanize 루프(웹 조사 + 블라인드 서브태스크)를 강제하는 것은 느리고 과하다.

## 환경

- 대상: kernforge agent document-authoring 경로 (`RequestEnvelope.DocumentAuthoring`)
- 기존 자산: `cmd/kernforge/prompts/system_base.md` (채팅용 문체), `prompts/request_envelope.md` (document mode 경계만), `.kernforge/skills/humanize-doc/` (후처리)
- 외부: no-ai-slop SKILL.md / eval.md (2026-07), Microsoft Writing Style Guide, humanize-doc baseline

## 실험 및 관찰

### 관찰 1: 생성 경로 문체 가이던스 공백

- 가설: document-authoring 시 시스템 프롬프트에 전문가 문체 규칙이 거의 없다.
- 절차: `system_base.md`, `request_envelope.md`, `codexGradeRequestHandlingPrompt`, `humanize-doc` 주입 조건 확인.
- 결과:
  - `system_base.md` “How to write for the user”는 **채팅 최종 답** 가독성 규칙(단문, 일상어, 결론 먼저). 설계서/연구노트/README 같은 **문서 산출물** 전용 규칙이 아님.
  - `request_envelope.md` DocumentAuthoring 분기는 파일 쓰기 허용 + 소스 수정 금지뿐.
  - `codexGradeRequestHandlingPrompt`의 document_artifact 줄은 **artifact quality 게이트**(존재/주제/placeholder)만 언급.
  - `humanize-doc`은 내장 스킬로 seed되나 기본 `enabled_skills`가 아니고, 사용자가 `$humanize-doc` 하거나 모델이 catalog에서 고를 때만 동작.
- 해석: “문서를 써 줘”만 하면 모델 기본 slop이 그대로 파일에 들어간다. 후처리 스킬은 별도 요청 없이는 안 탄다.

### 관찰 2: no-ai-slop 패턴 카탈로그

출처: https://github.com/petergyang/no-ai-slop (`SKILL.md`, `eval.md`, README). 2026-07-22 Peter Yang 공개. MIT.

| 패턴 | 예시 / 냄새 |
|---|---|
| Binary contrasts | "It's not X. It's Y." |
| Throat-clearing openers | "Here's the thing..." |
| Faux-insight setups | "What nobody tells you..." |
| Colon reveals | "The best part: it learns." |
| Superficial analysis | "...highlighting the team's commitment" |
| Importance puffery | "marks a pivotal moment" |
| Weasel attribution | "experts agree," "studies show" |
| Fake-strong verbs | "serves as a centralized hub" |
| Synonym cycling | agent → assistant → tool 로 돌려 쓰기 |
| Negative listing | "Not a X. Not a Y. A Z." |
| Dramatic fragmentation | "That's it. That's the whole thing." |
| Rhetorical setups | "What if I told you...", "Plot twist:" |
| Fake-profound kickers | 마지막 비유/격언 한 방 |
| Summary-recap endings | "In conclusion," "Overall," |
| Formatting slop | 이모지 헤딩, 장식 볼드, 2문장 섹션에 헤더 |
| Em-dash 남용 | 리듬 버팀목으로 — 남발 |

편집 원칙(요약): 최소 유효 수정, 능동태, 구체 수치, 문장마다 존재 이유, 목소리 보존.  
`eval.md`: 편집 후 pass/fail 자기 검수. 탐지 모드와 편집 모드 분리.

### 관찰 3: 기존 humanize-doc와의 겹침

- humanize-doc은 **근본 원인 5가지**(균일 리듬, 입장 부재, 경험 부재, 과잉 신호, 과잉 헤징) + 한/영 어휘·구조·톤 + 탐지-수정-블라인드 판정 루프 + 웹 조사 갱신.
- no-ai-slop은 **패턴 이름 체계**와 **eval self-check**가 더 명시적. 특히 binary contrast / colon reveal / synonym cycling / fake-profound kicker / formatting slop 이 humanize baseline에 일부만 있거나 이름 없이 흩어져 있음.
- 병합 방향: humanize-doc `ai-tells.md`에 no-ai-slop 패턴 이름을 정식 섹션으로 편입하고, 생성용 컴팩트 프롬프트는 겹치는 핵심만 8~15줄로 압축.

### 관찰 4: 기술 문서 스타일 가이드 (상시 원칙)

- **Microsoft Writing Style Guide**: plain language, active voice, “write like you speak”, every word matters, sentence case headings (Learn 계열).
- **Google technical writing (one)**: 능동태, 짧은 문장, 모호 대명사 금지, 목록은 병렬일 때만.
- **에이전트 적용 시 주의**: 마케팅 톤(“warm and relaxed”)을 보안/설계 문서에 그대로 옮기면 안 됨. **crisp + concrete + stance**만 채택. 오버슈팅(반말·은어·가짜 오타)은 humanize-doc 불변조건과 동일하게 금지.

### 관찰 5: 워크플로 기법 (2025–2026)

1. **25/50/25 (Peter Yang)**: 사람 초안 25% / AI 중간 편집 50% / 사람 최종 25%. 에이전트 전용 문서 생성에는 (1) 내용·구조 확정 + 문체 계약 하 초안, (2) 체크리스트 자기 검수, (3) 파일 쓰기 전 한 번 더 손질 — 로 축소 적용.
2. **Self-eval pass (no-ai-slop eval.md)**: 별도 평가 agent 없이 같은 턴에서 pass/fail 후 재수정.
3. **Post-hoc humanize-doc**: 사용자가 “AI 티 제거”를 명시하거나 공개 전 polish를 요청할 때 full 루프.
4. **Detector 최적화 금지**: GPTZero perplexity/burstiness를 목표로 문장을 뒤틀지 말 것. 목표는 전문가 가독성이지 detector 회피가 아님. burstiness(문장 길이 변화)는 결과적으로 따라오는 부산물로만 취급.

## 반증 / 실패한 시도 (의도적으로 버림)

| 접근 | 버린 이유 |
|---|---|
| 매 document-authoring 턴에 humanize-doc full 루프 강제 | web_search + spawn_task 블라인드 판정 비용 큼. speed preset 철학과 충돌. 문서 생성 자체가 느려짐. |
| banned word 정규식 artifact quality blocker | 기술 문서에서 "robust", "leverage" 등이 합법적 맥락으로 등장. false positive → 쓰기 게이트 교착. |
| system_base.md에 모든 패턴 장문 삽입 | 모든 턴 토큰 낭비. 채팅 답과 문서 산출물 요구가 다름. document 전용 조건부 주입이 맞음. |
| “사람 흉내”용 오타/이모지/은어 | humanize-doc 불변조건 위반. 전문 문서 신뢰도 하락. |

## 레퍼런스

- no-ai-slop: https://github.com/petergyang/no-ai-slop (SKILL.md, eval.md)
- Peter Yang essay (2026-07-22): https://creatoreconomy.so/p/use-my-no-ai-slop-skill-to-remove-20-ai-slop-patterns
- Microsoft Writing Style Guide: https://learn.microsoft.com/en-us/style-guide/welcome/
- Microsoft top tips: https://learn.microsoft.com/en-us/style-guide/top-10-tips-style-voice
- kernforge: `cmd/kernforge/.kernforge/skills/humanize-doc/SKILL.md`
- kernforge: `cmd/kernforge/.kernforge/skills/humanize-doc/references/ai-tells.md`
- kernforge: `cmd/kernforge/prompts/system_base.md`, `request_envelope.md`
- kernforge: `cmd/kernforge/prompt_assets.go`, `agent.go` (`systemPrompt`, `codexGradeRequestHandlingPrompt`)

## 미해결

- [ ] 생성 시 주입 텍스트의 적정 길이(토큰 예산 vs 준수율) — 구현 후 샘플 문서로 체감 검증
- [ ] artifact quality에 soft warning(이모지 헤딩, "살펴보겠습니다" 등 고신뢰 패턴)을 넣을지 — 1차 범위 밖, 후속 후보
- [x] 내장 스킬 seed 갱신 — 2026-07-23 hash marker(`*.bundled-sha256`) 업그레이드로 해결. legacy 무마커는 1회 마이그레이트.
- [x] continuation 문체 유실 — acceptance contract에서 DocumentAuthoring 보존 + applyPolicy 재실행.

## 후속 리뷰에서 고친 결함 (2026-07-23)

1. `계속` 같은 continuation 턴이 `DocumentAuthoring=false`로 재분류되어 style contract가 빠짐.
2. session context 적용 후 `applyPolicy` 미호출 → 문서 플래그와 mutation 정책 불일치 가능.
3. 내장 skill seed-if-absent로 humanize-doc 업데이트가 사용자 홈에 안 감.
