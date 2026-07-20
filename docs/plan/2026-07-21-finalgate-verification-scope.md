# 작업 계획서: v2 final gate / completion audit의 verification 차단 scope 일관화

- 작성일: 2026-07-21
- 상태: 완료 (2026-07-21)
- 관련 문서: [[docs/research/overblock-audit.md]] (F1/F2), [[docs/plan/2026-07-19-verify-outofscope-hardstop.md]]

## 1. 목표 / 배경

심층 감사(docs/research/overblock-audit.md)에서 확인한 2건의 오버블록:

- **F1**: v2 structured final gate의 verification 차단이 (1) patch scope를 보지 않고(ambient/환경성 실패도 차단), (2) 정직 고지(disclosure) 탈출이 없다. legacy 턴 경로는 2026-07-19 작업으로 "ambient는 warning, patch-scoped는 고지 시 수용" 정책인데, v2 게이트만 그 정책을 따르지 않는다. 현재 v2는 기본 비활성이라 latent지만, 활성화하면 7/19에 제거한 교착 클래스가 부활한다. guidance 문구("clearly preserve the unresolved verification blocker in the final answer")가 disclosure 탈출을 암시하므로 코드가 문서화된 의도를 미구현한 것이기도 하다.
- **F2**: completion audit의 verification 항목이 모든 실패를 `blocked`로 처리해, runtime gate ledger가 warning으로 내리는 ambient 실패를 audit checklist는 차단한다.

목표: 두 영역 모두 7/19에 합의된 scope 정책(patch-scoped만 차단, ambient/config은 warning/비차단, 정직 고지는 수용)을 따르게 한다.

## 2. 범위

- 포함:
  - `final_gate.go`: `finalGateVerificationResult`의 Failed/Unresolved 산정에 patch-scope + config-issue 예외 적용, `DecideFinalGate` verification 분기에 disclosure 탈출 추가.
  - `completion_audit.go`: `completionAuditVerification`/`completionAuditVerificationStatus`에 ambient/config 실패 시 warning 강등. (acceptance contract `VerificationRequired` 분기는 유지 — 사용자 명시 요구는 엄격이 의도.)
  - 회귀 테스트.
- 제외(이번에 안 하는 것):
  - gate ledger changed-path width(F3, 2026-07-19 후속 이슈 1) — 별도 작업.
  - request runtime v2의 활성화/설정 변경 없음.
  - legacy 턴 경로(readiness, coding harness, ledger)는 이미 정책 준수 — 변경 없음.
- 전제 조건 / 의존성:
  - 기존 헬퍼 재사용: `verificationFailureTouchesChangedPaths`, `verificationReportIsOnlyNonCodeBuildIssue`, `replyMentionsVerificationBlocker`, `replyMentionsVerificationNotRun`.

## 3. 접근안 비교

| 안 | 요약 | 장점 | 단점 | 리스크 |
|---|---|---|---|---|
| A | final_gate.go만 수정 (F1) | v2 활성화 시 교착 방지 | F2 잔존 — audit이 ambient로 not-ready 오염 | 낮음 |
| B | completion_audit.go만 수정 (F2) | audit 정합성 | F1 잔존 — v2 활성화 시 교착 부활 | 중간 |
| C | F1+F2 동시 수정 | 7/19 정책을 전 레이어에 완결 | 두 파일 + 테스트 | 기존 final_gate/audit 테스트 영향 |

**선택: C**
선택 근거: 둘 다 같은 정책 불일치라 따로 고치면 감사를 다시 해야 한다. F2는 contract-required 분기를 건드리지 않으므로 엄격성이 필요한 곳은 유지된다.

## 4. 구현 단계

1. `final_gate.go`:
   - `finalGateVerificationResult`: `Failed`는 사실 보고로 유지하되, `Unresolved` 산정 시 patch-scoped 실패(`verificationFailureTouchesChangedPaths(report, changedFiles)`)이면서 config/환경성 전용이 아닌 경우(`!verificationReportIsOnlyNonCodeBuildIssue(report)`)만 포함. ambient 실패는 `Ambient` 필드(신규, observability)로 기록.
   - `DecideFinalGate` verification 분기: `replyMentionsVerificationBlocker(input.Reply) || replyMentionsVerificationNotRun(input.Reply)`이면 차단하지 않음 (turn_runtime.go:212의 해소 조건과 동일). guidance 문구는 이미 이를 암시.
2. `completion_audit.go`:
   - `completionAuditVerification`: 실패 시 patch-scope + config 검사 — ambient/config이면 `blocked` 대신 `warning` + "ambient/config failures remain outside the current patch scope" evidence.
   - `completionAuditVerificationStatus`: 동일 기준으로 blocked/warning 분기. 시그니처에 changed paths 전달 필요 — 호출부(`completionAuditVerification`)에서는 `artifact.ChangedFiles` 사용.
3. 테스트:
   - F1 단위: ambient 실패(다른 경로 대상 실패) → Unresolved=false/Ready; patch-scoped 실패 + 고지 reply → Ready; patch-scoped 실패 + 미고지 → NeedsVerification 유지; config-only 실패 → Unresolved=false.
   - F2 단위: ambient 실패 → audit item warning(비 blocked); patch-scoped 실패 → blocked 유지.
4. `go build`, `go vet`, 전체 테스트. 기존 실패 6개(pre-existing) 베이스라인 유지 확인.
5. research 노트 미해결 항목 [x], CHANGELOG 추가.

## 5. 위험 및 실패 경로

- 위험 1: `changedFiles`가 비어 있으면 `verificationFailureTouchesChangedPaths`가 false를 반환해 실패가 전부 ambient 취급될 수 있음. → `BuildFinalGateInput`이 changedFiles를 못 구하면 `session.LastVerification.ChangedPaths`로 폴Back(final_gate.go:156-158 기존 로직) — 이 경로가 있는지 구현 시 확인하고, 폴Back도 없으면 실패를 patch-scoped로 간주(보수적)하도록 가드.
- 위험 2: v2 게이트에 disclosure 탈출을 주면 "실패를 말로만 고지하고 넘어가는" 우회가 생김. → legacy와 동일한 수준이며, 거짓 고지는 disclosure claims check(coding_harness.go:2051)와 `replyMentionsVerificationBlocker`의 "no known remaining blocker" 가드가 별도로 잡는다.
- 위험 3: contract `VerificationRequired`의 엄격성을 사용자가 기대하는 경우. → 해당 분기는 유지(변경 없음)로 대응.
- 실패 시 증상: final_gate_test/completion_audit_test 기존 테스트 실패.
- 롤백: 두 파일 단위 git revert.

## 6. 검증 방법

- `go build ./...`, `go vet ./cmd/kernforge/` 클린.
- `go test ./cmd/kernforge/ -run 'FinalGate|CompletionAudit'` 집중 통과.
- `go test ./cmd/kernforge/` 전체 — pre-existing 6개 외 회귀 0.
- 신규 테스트가 F1/F2의 실패-재현 → 수정 후 통과임을 확인(수정 전에 새 테스트가 fail하는지 먼저 확인).

## 7. 오픈 이슈

- [ ] contract `VerificationRequired` 분기에도 config-issue 예외를 줄지 — 사용자가 "테스트 통과"를 명시 요구했을 때 환경성 실패를 어디까지 허용할지는 정책 선택이라 이번엔 유지. 사용 사례 보고 판단.

## 8. 진행 로그

- 2026-07-21: 감사 완료(docs/research/overblock-audit.md). F1/F2 확정, 계획서 작성.
- 2026-07-21: C안 승인, 구현 완료.
  - 테스트 먼저 작성해 실패 확인(F1 3건 + F2 1건 fail, 컨트롤 2건 pass) 후 구현.
  - F1: `finalGateVerificationResult` — `Ambient` 필드 추가, Unresolved를 patch-scoped 실패(`verificationFailureTouchesChangedPaths` && `!verificationReportIsOnlyNonCodeBuildIssue`)/Missing/Skipped로 한정. `DecideFinalGate` verification 분기에 `replyMentionsVerificationBlocker/NotRun` disclosure 탈출 추가(turn_runtime.go:212와 동일 조건).
  - F2: `completionAuditVerification` 실패 분기 scope 검사 추가 — ambient/config이면 blocked→warning + evidence 접두사. `completionAuditVerificationStatus`에 changedPaths 인자 추가(호출부 3곳 모두 artifact.ChangedFiles 전달, passed 판정은 불변).
  - 검증: `go build`, `go vet` 클린. `go test -run 'TestFinalGate|TestCompletionAudit'` 통과. 전체 테스트 — 실패 6개 모두 pre-existing(LSP 4 = gopls 미설치, preamble 1, help coverage 1; 7/19 베이스라인과 동일), 회귀 0.
