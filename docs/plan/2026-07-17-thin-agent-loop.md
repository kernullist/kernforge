# 작업 계획서: 업계형 얇은 에이전트 루프

- 작성일: 2026-07-17
- 상태: 완료
- 관련: 로컬 의도 과분류로 README 분석 요청이 웹 강제·수리 카드로 붕괴

## 목표

Claude Code / Codex / Cursor / Grok Build처럼:

1. **의도는 모델이 도구로 수행**
2. **런타임은 실행 권한·명시적 안전 게이트만**
3. 키워드 휴리스틱으로 로컬 `read_file`을 선차단하지 않음

## 변경

1. 웹 리서치 hard-require / local tool defer → **명시적 웹 요청만**
2. soft prioritization에서 bare `현재`/`current`/`now`/`search` 제거
3. `@` / README / 문서·구현 gap 분석을 로컬 workspace inspection으로 인식
4. 분석 턴 stall recovery → "답변" 우선, "수정"은 edit intent만
5. `NOT_EXECUTED` 정책 차단을 repair defect로 취급하지 않음

## 검증

- `@README.md 문서를 읽고 현재 구현에 부족한 부분을 찾아서 알려줘` → web false, local not blocked
- 명시적 `웹에서 검색` / `최신 … 리서치` → 기존 동작 유지
