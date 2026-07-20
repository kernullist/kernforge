# 연구 노트: 게이트/리뷰/검증/하네스 전 영역 오버블록(잘못 차단) 감사

- 작성일: 2026-07-21
- 상태: 결론 도출
- 관련: [[docs/plan/2026-07-21-note-finding-blocker-surface.md]], [[docs/plan/2026-07-19-verify-outofscope-hardstop.md]]

## 질문

2026-07-19의 out-of-scope 하드 중단 제거와 2026-07-21의 note-finding blocker 표면화 수정 이후에도, 다른 영역에서 "차단되면 안 되는 것이 차단되는" 경로가 남아 있는가?

## 결론 (먼저 쓴다)

전체 차단 생산자 30+ 지점을 감사한 결과, 대부분은 의도된 fail-closed 또는 탈출구가 갖춰진 설계로 확인. **2건의 실결함** 발견:

- **F1 (v2 final gate 검증 분기, latent-high)**: `finalGateVerificationResult`가 `Unresolved = Missing || Failed || Skipped`로 계산할 때 `Failed = report.HasFailures()`를 **scope 검사 없이** 사용하고, `DecideFinalGate`의 검증 차단 분기(final_gate.go:242)에는 **disclosure 탈출이 없다**. ambient/환경성 실패나 patch-scoped 실패를 정직하게 고지한 최종 답변도 `FinalGateNeedsVerification`으로 차단 → nudge 2회 후 stall. request runtime v2 활성화 시 2026-07-19에 제거한 교착 클래스가 부활한다. 현재는 v2 기본 비활성(`RequestRuntimeModeDisabled`)이라 latent.
- **F2 (completion audit 검증 항목, medium)**: `completionAuditVerification`(completion_audit.go:376)와 `completionAuditVerificationStatus`(:394)가 모든 verification 실패를 `blocked`로 처리 — ambient/config 실패에 대한 예외 없음. 같은 액션에 대해 runtime gate ledger는 ambient 실패를 warning으로 내리는데(ledger.go:866-898) audit checklist만 더 엄격한 비일관.

## 감사 범위 및 방법

차단 생산자 전수 조사: `rg "Blockers = append|BlockingFindings = append"` + `rg "HasFailures\(\)"` (blocking 결정에 쓰이는 것만 선별) + 각 producer의 탈출구(degrade/warning 경로) 대조.

### 결함 상세

#### F1: v2 structured final gate 검증 분기

- 경로: agent.go:3080 `buildFinalGateInput` → `DecideFinalGate` → agent.go:3083 `requestRuntimeV2EnabledForEnvelope && !decision.Ready` 시 nudge → 2회 후 `finalizeOperatorStallReply` (턴 종료).
- `finalGateVerificationResult`(final_gate.go:298-320):
  - `result.Failed = report.HasFailures()` — patch scope 검사 없음, config/환경성 예외 없음.
  - `result.Unresolved = result.Missing || result.Failed || result.Skipped`.
- `DecideFinalGate`(final_gate.go:242): `if input.Verification.Unresolved && !input.GeneratedDocumentHarnessOwnsIt` → `FinalGateNeedsVerification`. `input.Reply`는 이 분기에서 미사용 — guidance 문구("clearly preserve the unresolved verification blocker in the final answer")는 disclosure 탈출을 암시하지만 코드에 없음.
- 비교 — legacy 턴 경로의 합의된 정책(2026-07-19/21):
  - runtime gate ledger(ledger.go:866-898): 실패가 `verificationFailureTouchesChangedPaths`이고 `!verificationReportIsOnlyNonCodeBuildIssue`일 때만 blocker, 아니면 ambient warning.
  - coding harness(coding_harness.go:1924-1952): patch-scoped만 blocker, ambient는 warning, 고지 시 면제.
  - turn readiness(turn_runtime.go:212): `replyMentionsVerificationBlocker/NotRun` 또는 구조적 통과 시 intervention 해소. `replyMentionsVerificationBlocker`는 ambient 고지도 인정(coding_harness.go:2737).
- 영향: v2 활성화 시 (1) ambient 실패 → 차단 루프, (2) patch-scoped 실패를 정직 고지 → 그래도 차단 루프. shadow 모드에서는 legacy/v2 divergence로 기록됨.
- 테스트 현황: final_gate_test.go에는 `Unresolved: true`(missing) → NotReady 케이스만 있고, ambient/고지 케이스는 없음.

#### F2: completion audit 검증 항목

- `completionAuditVerification`(completion_audit.go:350-392): `report.HasFailures()` → `completionAuditStatusBlocked` 무조건.
- `completionAuditVerificationStatus`(:394-402): 동일.
- `buildAcceptanceContractReport`의 contract.VerificationRequired 분기(:331-339)도 실패 시 blocked — 이건 "사용자가 명시 요구"라 엄격이 옹호 가능하나, config/환경성 실패까지 코드 실패로 보는 건 동일한 과잉의 소지.
- 비교: 같은 completion_audit 액션의 runtime gate ledger는 ambient → warning.
- 영향: audit artifact의 readiness가 ambient 실패로 "not ready"로 오염. goals_runtime.go:1398이 iteration.Blockers로 복사.

## 반증 / 실패한 시도 (감사했으나 정상으로 판명)

| 영역 | 결론 |
|---|---|
| pre_write evidence_gap warning → blocker 승격 (`preWriteReviewWarningShouldBlock`, auto.go:2085-2090) | **의도된 비대칭**. post-change(G-5 회귀 테스트, harness_test.go:6557)에서는 model evidence_gap 미차단이 원칙이나, pre_write에서는 "코드 갭이 evidence_gap으로 둔갑"(미구현/컴파일 불가/헤더 누락 키워드)만 승격. harness-shaped("function body is not visible" 등)는 제외. 실전 인시던트 기반 키워드(한글 포함)로 다듬어진 설계. |
| deterministic blockers (no evidence / frozen diff / verification required / RF-REVIEWER-001) | 하네스 자체 no-evidence 게이트, 의도된 fail-closed. route 실패는 파일 없으면 needs_review로 degrade(ledger.go:735/757). |
| coding harness contradiction blockers (missing artifact / dishonest claims / bug count / analysis-only 위반) | 부정직 최종 답변 탐지기, 의도된 fail-closed. |
| patch transaction unknown scope blocker (ledger.go:1012) | changed_paths 메타데이터 없는 workspace 변경 — fail-closed 의도. |
| stale context correction-pending block (ledger.go:1132, stale_context_summary.go:187) | 실제 상태 신호(보정 계약 pending 중 새 요청). |
| git write gate (agent.go:1831-1864) | staleness-only면 명시적 사용자 git 요청 시 warning 후 진행 허용. 실제 finding만 hard-block. |
| triage / completion audit 구조 검증 (triage.go:320-347) | cross-review triage completeness 규칙, 구조적 검증. |
| final gate의 git/read-only/draft-only 분기 (final_gate.go:205-241) | 의도된 요청 경계 정책. |
| turn FinalAnswerReadiness / nudge 상한 (agent.go:2700-2774) | nudge 2회 상한 + 강제 disclosure 탈출(2725-2736) + terminal stall reply로 상한 존재. |
| agent.go:4548-4595/4951/5171/5603 등 `HasFailures()` 소비자 | 대부분 수리 루프 트리거/상태 표시/짧은 경로 적격 검사 — 차단 결정이 아님. |
| `verify` 명령/사용자 거부 경로 (automaticVerificationSkippedFinalOnly) | 2026-07-19에 사용자 명시 거부 존중으로 유지 결정됨. |

## 미해결

- [x] F1/F2 수정 (2026-07-21 완료: [[docs/plan/2026-07-21-finalgate-verification-scope.md]]).
- [ ] F3: gate ledger changed-path 집합이 턴 판정보다 넓은 문제 (2026-07-19 계획서 후속 이슈 1, 별도 작업으로 큐잉됨).
- [ ] pre_write evidence_gap 승격 키워드의 오탐 여지 — 모델이 "does not implement X"를 evidence 부족으로 오보하는 경우. 현재 blocker-verification pass(runReviewBlockerVerificationPass)가 refuted blocker를 info로 강등하므로 완화됨. 실사용 관찰 유지.
