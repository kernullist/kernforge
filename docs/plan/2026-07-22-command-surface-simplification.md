# 작업 계획서: Kernforge 커맨드 표면 단순화

- 작성일: 2026-07-22
- 상태: 완료
- 관련: [[docs/CHANGELOG.md]] 2026-07-22

## 1. 목표 / 배경

공개 slash 커맨드 77개가 기본 `/help`에 전부 노출되어 사용자가 외울 대상이 과도하다. 이미 `removedLegacySlashCommands`로 1차 패밀리 폴딩을 했지만, 광고 계층은 줄지 않았다.

목표: 기능을 삭제하지 않고 **일상(L1) / 허브(L2) / 전문가(L3)** 3층으로 재편해 기본 `/help`를 한 화면에 맞춘다. 구 경로는 hidden 별칭으로 유지한다.

## 2. 설계 원칙

1. 기능 삭제가 아니라 광고 계층 축소
2. Verb 허브 — `noun verb` (`/selection use 2`); 기존 `verb-noun`은 조용한 별칭
3. 막힘 복구는 상황형 — `/finish` `/retry-verify` `/continue`는 기본 help에서 제외
4. 호환 — 공개 제거 전 별칭 유지; hard-remove는 후속 릴리스

## 3. 목표 커맨드 맵

### L1 Everyday

`/help` `/status` `/clear` `/exit` `/model` `/provider` `/permissions` `/review` `/verify` `/gate` `/diff` `/config`

### L2 Hubs

| Hub | 흡수 |
|-----|------|
| `/session` | 유지 |
| `/memory` | `/evidence` → `/memory evidence …` |
| `/selection` | `*-selection`, `/open` |
| `/analyze` | analyze-project/dashboard/performance, docs-refresh |
| `/probe` | fuzz-func/campaign, source-scan, find-root-cause, root-cause-patterns, create-driver-poc |
| `/mcp` | resources/resource/prompts/prompt/skills |
| `/hooks` | hook-reload, override |
| `/settings` | set-auto-verify, locale-auto, set-max-tool-iterations, progress-display |
| `/checkpoint` `/goal` `/automation` `/suggest` `/worktree` `/init` `/specialists` `/profile` `/codex-auth` | 유지·정리 |

### L3 Expert

`/review-soak`, `/decision`, `/investigate`, `/simulate`, harness 복구 3종, `/codex-login`(별칭만)

## 4. 구현 단계

0. 본 계획서 + CHANGELOG
1. CommandVisibility + `/help` L1/L2 + `/help all` + hub cheatsheet + completion 필터
2. selection / mcp / hooks / settings 폴딩
3. analyze / probe 허브
4. README / FEATURE_USAGE_GUIDE / QUICKSTART 영·한

## 5. 성공 기준

- 기본 `/help` ≤ ~40줄 본문
- 광고 top-level ≤ ~27
- 구 경로 별칭으로 기능 회귀 0
- command_registry / completion / help 테스트 통과

## 6. 진행 로그

- 2026-07-22: 계획 확정 및 구현 시작.
- 2026-07-22: Phase 0–4 완료. CommandVisibility(L1/L2 public, L3+aliases hidden), `/help`/`/help all`, hub routers(`/selection` `/mcp` `/hooks` `/settings` `/analyze` `/probe`), completion 필터, README/QUICKSTART/FEATURE_USAGE_GUIDE 갱신.
