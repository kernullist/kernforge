# CHANGELOG

의미 있는 변경 이력. 날짜는 절대 형식(YYYY-MM-DD), 최신 항목이 위에 온다.

## 2026-07-19

### 자동 검증 out-of-scope 하드 중단 제거 및 scope 판정 일관화

- 계획서: [[docs/plan/2026-07-19-verify-outofscope-hardstop.md]]
- `verificationOutOfScopeFinalOnly` 하드 블록 제거. out-of-scope 판정 시 모든 후속 도구가 `NOT_EXECUTED`로 차단되고 gate는 같은 실패를 in-scope blocker로 보는 교착(`gate:blocked` + 아무것도 못 함)을 해소.
- 대화형 실행에서는 out-of-scope 판정 시 턴당 1회 사용자에게 질의(`PromptResolveOutOfScopeVerification`): 계속 수리(기본) / ambient risk로 고지하고 마무리. 비대화형은 guided continuation으로 폴Fallback. Finish 선택 시에만 최종 답변으로 수렴하며 도구 차단은 없음.
- `verification_repair_scope.go`: `go test|build|vet ./cmd/app/...` 형태의 구체적 Go 패키지 scope가 변경 파일의 디렉터리를 커버하면 in-scope로 판정(`verificationGoPackageScopeCoversChangedDir`). 같은 패키지 다른 파일의 compile_error가 patch 검증을 막는 경우를 in-scope로 교정. 범용 디렉터리 매칭은 MSBuild sibling 실패가 의도적으로 ambient인 기존 동작을 깨므로 Go 전용으로 제한. workspace/`./...` 패턴과 `cmd/app2` vs `cmd/app` 같은 경계 오매칭 제외.
- `coding_harness.go`: "Unresolved verification failure" blocker가 patch-scoped 실패에만 발동하도록 수정 — out-of-scope 실패가 in-scope 문구 blocker로 오표시되던 문제 해소.
- 루프 상한은 기존 retry budget(같은 실패 fingerprint 2회 → change_strategy, 3회 → escalate_reviewer)에 위임.
- 문서(README/FEATURE_USAGE_GUIDE, 영/한)의 `NOT_EXECUTED` 서술을 새 동작으로 갱신.
- 후속 이슈(별도 작업): gate ledger changed-path 집합과 턴 판정 집합의 차이로 ambient Finish 시 `gate:blocked` advisory가 남을 수 있음, note 수준 evidence_gap finding(RF-001)이 "unwaived blockers"로 표면화되는 경로.
