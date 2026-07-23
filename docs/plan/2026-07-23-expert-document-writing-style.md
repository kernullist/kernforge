# 작업 계획서: 문서 작성 시 전문가 문체 (anti-AI-slop)

- 작성일: 2026-07-23
- 상태: 완료
- 관련 문서: [[docs/research/2026-07-23-expert-doc-writing-anti-slop.md]]

## 1. 목표 / 배경

**목표:** kernforge가 문서를 작성·갱신할 때 AI 말투(slop)가 아니라, 해당 분야 전문가가 쓴 기술 문서처럼 쓰게 한다.

**배경:**
- 사용자 요청: [no-ai-slop](https://github.com/petergyang/no-ai-slop) 스킬을 참고하고, 다른 최신 기법도 조사해 반영.
- 현황: `humanize-doc` 후처리 스킬은 이미 강하지만 **opt-in**. document-authoring 턴에는 문체 계약이 거의 없어 초안 품질이 모델 기본 톤에 좌우됨.
- 채팅용 `system_base.md` 문체 규칙은 최종 답 가독성용이지, plan/ADR/research/README 산출물 계약이 아님.

연구 요약: [[docs/research/2026-07-23-expert-doc-writing-anti-slop.md]]

## 2. 범위

### 포함
1. **생성 시점 예방:** `DocumentAuthoring == true`일 때 시스템 프롬프트에 **문서 전용 전문가 문체 계약** 주입.
2. **후처리 스킬 정렬:** `humanize-doc` + `ai-tells.md`에 no-ai-slop 패턴 이름·eval 자기 검수·생성 가이드와의 일관성 반영.
3. **요청 envelope 보강:** document-authoring 모드에 “전문가 문체로 쓰고, 쓰기 전 self-check” 한 줄 계약.
4. **테스트:** 프롬프트 주입 조건 단위 테스트, 스킬 seed 회귀 테스트 갱신.
5. **문서:** 본 plan, research, CHANGELOG 항목.

### 제외 (이번에 안 하는 것)
- 매 문서 턴에 humanize-doc full 루프(웹 조사 + 블라인드 서브태스크) 강제.
- banned-word 정규식 artifact-quality **blocker** (false positive 위험).
- detector(GPTZero 등) 회피 최적화.
- 채팅 최종 답 톤 전면 개편 (`system_base` 대수술). 기존 채팅 규칙은 유지.
- 사용자 전역 `enabled_skills`에 humanize-doc 기본 ON (원치 않는 턴 비용).

### 전제 / 의존성
- prompt block embed 경로 (`prompt_assets.go` + `prompts/*.md`).
- bundled skill seed (`bundled_assets.go`, `ensureSeedUserFile` — 기존 시드 파일은 사용자 편집 보존이므로 **repo 원본 갱신 + 테스트 기대값**; 이미 배포된 사용자 홈 스킬은 seed가 덮어쓰지 않음 → CHANGELOG/README에 “수동 갱신 또는 파일 삭제 후 재시드” 안내).

## 3. 접근안 비교

| 안 | 요약 | 장점 | 단점 | 리스크 |
|---|---|---|---|---|
| A | humanize-doc만 보강 | 구현 작음 | 생성 시 여전히 slop | 사용자가 스킬을 안 부르면 무의미 |
| B | 문서 모드 컴팩트 문체 계약 + humanize 정렬 | 예방+후처리, 토큰 적음 | 준수율은 모델 의존 | 계약이 너무 길면 무시/토큰 낭비 |
| C | 모든 문서 write 후 자동 humanize 루프 | 품질 상한 높음 | 느림, speed preset 충돌 | 과잉 수정, 비용 |
| D | 정규식 quality blocker | 결정적 | FP, 기술 용어 충돌 | 쓰기 교착 |

**선택: B**

선택 근거:
- no-ai-slop의 핵심은 “패턴을 알고 최소 수정 + eval”이다. 생성 시 그 계약을 짧게 넣는 것이 1차 효과.
- 기존 humanize-doc은 polish 요청용으로 유지·강화하는 편이 역할 분리가 맞다.
- C/D는 운영 비용·FP가 커서 1차에서 제외. soft warning은 후속 후보로 research에 기록.

## 4. 구현 단계

### 4.1 문서 전용 prompt block 추가

파일: `cmd/kernforge/prompts/document_authoring_style.md` (신규)

내용 구성 (목표: 짧고 집행 가능한 체크리스트, ~40–70줄 이내):

1. **역할:** 해당 분야 시니어 엔지니어가 동료에게 넘기는 기술 문서. 마케팅/챗봇 톤 금지.
2. **원칙 (Microsoft/Google + no-ai-slop 교차):**
   - 결론·결정·수치를 앞세운다 (필요 시).
   - 능동태, 직접 동사, 구체 사실. 추상 명사 나열 금지.
   - 확실한 것은 단정, 불확실하면 조건+근거. 입장 없는 양비론 금지.
   - 문장 길이·문단 길이를 의도적으로 흔든다 (균일 리듬 금지).
3. **금지 패턴 (no-ai-slop 압축 목록, 한/영 예시 각 1):**
   - binary contrast, throat-clearing, faux-insight, colon reveal, importance puffery, weasel attribution, synonym cycling, dramatic fragments, fake-profound ending, summary-recap ending, formatting slop, em-dash 남용.
   - 한국어: “살펴보겠습니다/결론적으로”, “다양한·효과적인” 남발, “~할 수 있습니다” 밀도, 이정표 3단(“먼저/다음으로/마지막으로”).
4. **banned words 짧은 목록** (no-ai-slop + humanize 교집합, 인용·코드 제외).
5. **쓰기 전 self-check (eval 축소판, 5항):**
   - 금지 패턴/단어 잔존?
   - 구체 사실 없이 중요성만 주장?
   - 헤딩/불릿이 장식용인가?
   - 끝 문단이 본문 요약 반복인가?
   - 소리 내어 읽었을 때 동료 엔지니어 말투인가?
6. **불변:** 사실 발명 금지, 코드/경로/명령/API 식별자 원문 유지, 요청 언어(한/영) 준수, 프로젝트 plan/ADR 템플릿 섹션 구조는 유지(문체만).

### 4.2 주입 경로

1. `prompt_assets.go`: `PromptBlockDocumentAuthoringStyle` 상수 + asset path.
2. `agent.go` `systemPrompt()`: `requestEnvelope.DocumentAuthoring`이면 base/envelope 직후에 해당 블록 렌더.
3. `request_envelope.md` DocumentAuthoring 분기에 2–4줄 추가:
   - 문서 산출물은 전문가 문체 계약 준수.
   - 파일 쓰기 직전 self-check.
   - 사용자가 “AI 티 제거/humanize”를 명시하면 `$humanize-doc` 루프 사용 가능(강제는 아님).

### 4.3 humanize-doc 정렬

1. `ai-tells.md`: no-ai-slop 패턴 섹션 추가(표 + 수정 방향). 기존 한/영 섹션과 중복 시 상호 참조만.
2. `SKILL.md`:
   - 출처 고지: no-ai-slop(MIT) 패턴·eval 아이디어 반영.
   - Phase 수정 후 **eval self-check** 단계 명시 (별도 agent 없이).
   - 생성 경로 가이드와 동일한 “금지 패턴 이름” 사용으로 일관성.
3. `bundled_assets_test.go`: seed 내용에 새 키 문구(예: `no-ai-slop`, `Binary contrasts` 또는 한국어 표기) 포함 여부 검사 추가.

### 4.4 테스트

1. `agent_prompt_test.go` (또는 인접):
   - document-authoring 요청 시 system prompt에 문체 계약 마커 포함.
   - 순수 코드 수정 요청 시 해당 블록 미포함.
2. 기존 skill seed 테스트 갱신.
3. (선택) request_envelope 렌더 스냅샷에 document 분기 문자열.

### 4.5 문서화

- `docs/CHANGELOG.md` 항목.
- 필요 시 `FEATURE_USAGE_GUIDE(_kor).md`에 “문서 문체 / $humanize-doc” 한 절 (분량 최소화).

## 5. 위험 및 실패 경로

| 위험 | 증상 | 완화 |
|---|---|---|
| 가이드가 길어서 무시됨 | 여전히 slop | 짧게, 금지 목록 우선, 중복 제거 |
| 템플릿 섹션을 문체 규칙이 깨뜨림 | plan 구조 훼손 | “템플릿 섹션 유지” 명시 |
| seed 미갱신으로 사용자 홈 스킬 stale | humanize 예전 버전 | CHANGELOG + 삭제 후 재시드 안내 |
| 채팅 답까지 과도한 단정 톤 | 일반 Q&A가 딱딱해짐 | **DocumentAuthoring 조건부만** 주입 |
| 금지 단어 과잉 | “utilize” 같은 합법 기술 용어도 회피 강박 | “비기술 과시 용법일 때만” 주석 |

롤백: prompt block 제거 + humanize 파일 git revert. envelope 문구만 되돌려도 생성 경로 영향 제거 가능.

## 6. 검증 방법

1. 단위 테스트 통과 (`go test` 관련 패키지/파일).
2. 수동: 한국어 “`docs/plan/…` 설계 계획서 작성” 요청 → 산출물에 “살펴보겠습니다/결론적으로/It's not X” 등 고신뢰 slop이 초안부터 줄었는지 육안 확인.
3. 수동: “이 문서 AI 티 제거” → humanize-doc 루프가 새 패턴 이름을 쓰는지.
4. 성공 기준:
   - DocumentAuthoring system prompt에 문체 계약 존재.
   - non-doc 턴에 미주입.
   - humanize-doc에 no-ai-slop 정렬 섹션 존재.
   - 기존 테스트 회귀 없음.

## 7. 오픈 이슈

- [ ] 꿀보 승인: 안 B 범위(생성 계약 + humanize 정렬)로 진행할지, humanize만/자동 full 루프 원하는지.
- [ ] soft warning quality check를 같은 PR에 넣을지 (기본 제외 권장).
- [ ] 이미 seed된 사용자 스킬 강제 갱신 API가 필요한지 (현재 seed-if-absent).

## 8. 진행 로그

- 2026-07-23: 현황 조사, no-ai-slop/MS style/humanize-doc 교차 분석, research + plan 초안 작성. 구현 대기(승인 필요).
- 2026-07-23: 안 B 승인 후 구현 — `document_authoring_style` prompt block, envelope/codex 안내, humanize-doc 정렬, 단위 테스트, CHANGELOG.
- 2026-07-23: 관련 단위 테스트 통과. seed-if-absent 사용자 스킬 갱신 안내는 CHANGELOG에 기록.
- 2026-07-23: 리뷰 후 수정 — continuation DocumentAuthoring 보존, style inject helper 확장, 내장 스킬 hash upgrade, fallback 마커 정렬, 회귀 테스트 추가.
