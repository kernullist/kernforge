# 작업 계획서: 권한 모드 Grok Build 정렬

- 작성일: 2026-07-17
- 상태: 완료
- 관련 문서: [[PERMISSION_REDESIGN.md]], Grok user-guide `22-permissions-and-safety.md`

## 1. 목표 / 배경

plan/edit/full 사용자 모드를 유지하면서 인가 파이프라인을 Grok Build와 동일하게 맞추고, full 모드에서 권한과 무관하게 막히던 경로를 제거한다.

## 2. 확정 결정

1. 사용자 노출 모드: **plan / edit / full** 유지
2. 인가 순서: hooks → config rules (deny>ask>allow) → remembered → mode policy
3. full에서도 **config deny / ask rule / hooks 유지** (Grok bypassPermissions)
4. full에서 **shell 워크스페이스 쓰기 허용**
5. edit에서 수동 shell 쓰기(Set-Content 등) hard deny; tool-style 쓰기는 ActionShellWrite 프롬프트
6. `:workspace` / `workspace` 입력 → **edit** 로 흡수
7. 세션/config 저장값은 항상 `plan|edit|full`

## 3. 구현 요약

| Slice | 내용 | 상태 |
|-------|------|------|
| 1 | `allowWithoutPromptDetail` Grok 순서; 모드 파싱/표시 정규화 | 완료 |
| 2 | `EnsureWriteWithContext` → `EnsureEditableTarget` | 완료 |
| 3 | shell mutation 모드 인식 (`enforceShellWorkspaceWritePolicy`) | 완료 |
| 4 | `/permissions` canonical 저장; full autoApprove | 완료 |
| 5 | 테스트 + 문서 동기화 | 완료 |

## 4. 검증

- `permission_grok_pipeline_test.go` 및 기존 permission/shell 테스트 green
- 수동: `/permissions full` 후 shell redirect 허용, plan/edit 수동 쓰기 거부

## 5. 진행 로그

- 2026-07-17: 구현 완료. full deny-rule 유지, shell write full 허용, write 경로 통일, 문서 갱신.
