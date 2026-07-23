# 작업 계획서: humanize-doc 1급 내장 스킬

- 작성일: 2026-07-23
- 상태: 완료
- 관련 문서: [[docs/plan/2026-07-23-expert-document-writing-style.md]]

## 1. 목표 / 배경

humanize-doc이 “시드된 사용자 스킬 파일”에만 의존하면, seed 실패·삭제·구버전 상태에서 `$humanize-doc` / `load_skill`이 깨진다.  
바이너리 embed를 권위 있는 소스로 두고, 항상 카탈로그에 존재하며 humanize 의도 요청 시 자동 활성화되게 한다.

## 2. 범위

- 포함: embed에서 humanize-doc 로드, 사용자 커스터마이즈 보존, 자동 활성화, 테스트/CHANGELOG
- 제외: 다른 워크플로 스킬(goal-loop 등) 전부 동일 취급, 매 문서 턴 강제 humanize 루프, enabled_skills 기본 ON

## 3. 접근안

| 안 | 요약 | 선택 |
|---|---|---|
| A | seed만 유지 | 기각 — 내장이라고 부르기 어려움 |
| B | embed 권위 + 디스크 오버라이드 + humanize 의도 시 자동 주입 | **선택** |
| C | 전 턴 enabled | 기각 — 토큰 낭비 |

## 4. 구현 단계

1. `Skill.Builtin` 필드, embed 로더, LoadSkills 병합
2. `looksLikeHumanizeDocRequest` + InjectPromptContext 자동 활성화
3. 테스트 + CHANGELOG

## 5. 위험

- 사용자 수정본 덮어쓰기 → hash marker / content mismatch 시 disk 우선
- 전 턴 full body 주입 → 의도 매칭 시에만

## 6. 검증

- 빈 skills 디렉터리에서도 humanize-doc Lookup 성공
- 커스텀 disk 스킬 보존
- “AI 티 제거” 요청 시 Activated skills 섹션 주입

## 8. 진행 로그

- 2026-07-23: 계획 작성 및 구현 시작
- 2026-07-23: skill_builtin.go, LoadSkills 병합, 자동 활성화, first-wins 검색 순서, 테스트/CHANGELOG 완료
- 2026-07-23: 리뷰 수정 — 프로젝트 스킬 보존, request-only $name/intent, catalog 토큰 비용, 홈 디렉터리 climb 차단, matcher 오탐 축소
