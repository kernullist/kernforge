# 작업 계획서: note 수준 finding이 gate blocker로 표면화되는 경로 차단

- 작성일: 2026-07-21
- 상태: 완료 (2026-07-21)
- 관련 문서: [[docs/plan/2026-07-19-verify-outofscope-hardstop.md]] §7 후속 이슈 2, [[docs/CHANGELOG.md]]

## 1. 목표 / 배경

2026-07-19 계획서의 후속 이슈: "최종 답변이 게이트에 막힌 뒤 재시도 과정에서 note 수준 evidence_gap finding(RF-001)이 `latest review has unwaived blockers`로 표면화하는 경로"를 조사해 확정했다.

재현 테스트로 확정한 인과 체인(2개 결함의 연쇄):

1. **트리거**: 최종 답변이 게이트에 막힌 상태에서 사용자가 note 수준 finding ID를 포함한 수리 의도 메시지 입력 (예: "RF-001 수정해줘", "이어서 진행해"). `looksLikeReviewRepairFollowUpIntent`는 "수정/이어/진행/fix/continue" 등에 반응하고, `reviewFindingIDsReferencedByText`가 RF ID를 추출한다.
2. **결함 A — scope fallback이 blocker를 날조** (`scopeReviewRunToRequestedRepairFindings`, review_harness_pre_fix.go:361-368): 참조된 finding이 `Gate.BlockingFindings`/`Gate.WarningFindings` 양쪽에 모두 없으면(note/info finding은 `reviewFindingCountsAsWarning`이 high/medium/low만 인정하므로 항상 해당) 선택된 finding 전부를 `scoped.Gate.BlockingFindings`에 무조건 추가. severity=info, BlocksGate=false인 finding이 blocker가 되는 gate-비일관 상태. 이 scoped run은 피드백에 "차단 finding"으로 렌더링되고, `LastReviewRun.RepairFindings`에 carried obligation으로 기록된다.
3. **결함 B — carried note obligation이 single-model policy blocker 유발** (`singleModelPreWritePolicyFindings`, review_harness_gate.go:279-307): 다음 pre-write 리뷰에서 `RequiresRFObligationStatus = RequiresPreWriteSelfReview && len(RepairFindings) > 0`(review_harness_state.go:214)이 true가 되고, note finding은 resolution status가 없으므로(`annotateSingleModelPreWriteRepairStatuses`는 main reviewer unusable 시 상태를 못 채움 — route 장애 시나리오와 정확히 일치) deterministic evidence_gap **blocker** "Single-model pre-write review lacks repair obligation status" 발동 → `recordReviewRun`으로 영속 → `runtimeGateAttachReview` → "latest review has unwaived blockers: RF-001".

핵심 비일관: obligation ledger(`buildReviewObligationLedger`, review_harness_obligations.go:41-52)는 carried RepairFindings를 `preWritePreFixFindingIsConcreteRepairObligation`(review_harness_auto.go:1187 — evidence_gap/test_gap 명시 제외)으로 걸러내는데, single-model policy 검사(`repairFindingsHaveResolutionStatus`, gate.go:479)는 날것의 RepairFindings를 그대로 본다. 또한 기존 가이드(review_harness_pre_fix.go:1006/1018)도 "evidence gap은 코드 수정 대상으로 삼지 마세요"라고 명시 — 결함 A/B는 이 확립된 관례를 위반한다.

목표: note 수준 finding이 어떤 경로로도 gate blocker / 필수 resolution obligation으로 승격되지 않게 한다.

## 2. 범위

- 포함:
  - `review_harness_pre_fix.go`: `scopeReviewRunToRequestedRepairFindings`의 blocker 날조 fallback 제거/교체.
  - `review_harness_gate.go`: single-model RF-obligation policy가 concrete repair obligation만 보도록 필터 일원화.
  - `review_harness_state.go`: `RequiresRFObligationStatus` 산정 시 동일 필터 적용.
  - 회귀 테스트 2~3개.
- 제외(이번에 안 하는 것):
  - carried obligation의 evidence 텍스트 렌더링(review_harness_collect.go) — 모델 의존 간접 경로이며 정당한 obligation에도 필요한 채널.
  - `formatPreWriteCarriedRepairObligationsFeedback` 문구 변경.
  - runtime gate ledger 자체(ledger는 `Gate.BlockingFindings`를 신뢰하는 게 맞고, 오염은 상류에서 발생).
- 전제 조건 / 의존성:
  - 사용자가 warning/blocker finding을 참조하는 기존 정상 경로(TestReviewRepairFollowUpWithExplicitRFIDScopesRepairGuidance)는 동작 유지.

## 3. 접근안 비교

| 안 | 요약 | 장점 | 단점 | 리스크 |
|---|---|---|---|---|
| A | 결함 A만 수정 (fallback 제거) | 최소 변경 | 결함 B 잔존: carried note obligation이 여전히 policy blocker 유발 가능 | 증상 재발 |
| B | 결함 B만 수정 (policy 필터 일원화) | ledger 표면화 직접 차단 | scoped gate 날조는 남아 모델 피드백이 "차단 finding"으로 오안내 | 수리 루프 오유도 |
| C | A+B 동시 수정 (필터 단일 헬퍼로 일원화) | 인과 체인 양 끝을 모두 차단, 기존 obligation ledger 관례와 일치 | 두 파일+테스트 수정 | 기존 RF scoping 테스트 영향 가능 |

**선택: C**
선택 근거: A만으로는 note finding이 RepairFindings carry를 통해 여전히 deterministic blocker를 만들고, B만으로는 모델이 note finding을 "차단 finding"으로 안낰 받아 수리 루프가 엉뚱한 곳을 고친다. 두 결함은 독립적으로 각각 틀렸고, obligation ledger가 이미 정답 필터(`preWritePreFixFindingIsConcreteRepairObligation` 등)를 갖고 있어 재사용하면 된다. C의 추가 비용은 테스트뿐.

## 4. 구현 단계

1. `review_harness_pre_fix.go` `scopeReviewRunToRequestedRepairFindings`:
   - 361-368 fallback 제거. 참조 finding이 gate 양쪽 목록에 없으면 gate 목록을 비워 둔다(filter 결과 그대로). finding 자체는 `scoped.Findings`/`scoped.RepairFindings`에 남겨 사용자 명시 수리 지침 채널은 유지.
   - `formatReviewRepairFollowUpFeedback`/`buildReviewRepairPlan`이 빈 gate를 자연 처리하는지 확인(RepairPlan은 blocker 없으면 empty 반환, gate.go:2022).
2. `review_harness_gate.go` + `review_harness_state.go`:
   - 단일 헬퍼(예: `reviewConcreteRepairObligationFindings(findings)`) 도입: `preWritePreFixFindingIsConcreteRepairObligation || preWritePreFixWarningIsConcreteRepairObligation || reviewFindingLooksActionableForRepairGate` 중 하나를 만족하는 finding만 (obligation ledger와 동일 기준).
   - `buildSingleModelReviewPolicy`: `RequiresRFObligationStatus`를 필터된 obligation 수로 산정.
   - `singleModelPreWritePolicyFindings`: 필터된 obligation만 status 검사. `annotateSingleModelPreWriteRepairStatuses`도 동일 필터 적용 검토.
3. 테스트:
   - 회귀 1 (결함 A): note finding만 참조("RF-001 수정해줘") 시 scoped gate가 비어 있고 BlockingFindings에 해당 ID가 없음. RepairFindings에는 남는지(지침 채널) 확인.
   - 회귀 2 (결함 B): carried note obligation만 있을 때 `singleModelPreWritePolicyFindings`가 blocker를 내지 않음. 실제 blocker성 obligation이면 여전히 내는지 대조.
   - 기존: `TestReviewRepairFollowUpWithExplicitRFIDScopesRepairGuidance` 등 RF scoping 테스트 전수 통과 확인.
4. `go test ./cmd/kernforge/...` 전체 실행 + `go vet`.
5. 2026-07-19 계획서 §7 후속 이슈 2를 [x]로 갱신, CHANGELOG 추가.

## 5. 위험 및 실패 경로

- 위험 1: fallback 제거 후 "note finding만 참조" 시 피드백이 비어 보이거나 repair prime이 사실상 no-op이 될 수 있음. → `formatReviewBeforeFixFeedback`이 findings 기반으로 렌더링하는지 구현 단계 1에서 확인, 아니면 최소 문구 보정.
- 위험 2: 정당한 carried obligation(evidence_gap이지만 deterministic blocker였던 경우 등)이 필터에서 빠져 policy 검사를 우회. → 필터는 obligation ledger와 동일 기준이라 ledger에 잡히는 obligation은 그대로 검사됨. deterministic no-evidence blocker는 매 실행마다 재산출되므로 carry 우회가 성립하지 않음.
- 위험 3: `RequiresRFObligationStatus` 필터링으로 policy.VerificationObligations 문구가 안 나가는 케이스 증가. → obligation이 실제로 없을 때뿐이라 정당.
- 실패 시 증상: RF scoping 테스트 실패, single-model policy 테스트 실패.
- 롤백: 세 파일 단위 git revert. 설정/포맷 변경 없음.

## 6. 검증 방법

- `go build ./...`, `go vet ./cmd/kernforge/` 클린.
- `go test ./cmd/kernforge/ -run 'ReviewRepairFollowUp|ScopeReviewRun|SingleModelPreWrite'` 집중 통과.
- `go test ./cmd/kernforge/` 전체 통과 (기존 결함 6개 — LSP 4 + duplicate preamble + help coverage — 는 2026-07-19 검증 시와 동일하게 pre-existing으로 판별).
- 재현 스크래치 테스트(조사 중 작성 후 삭제한 것)를 회귀 테스트로 정식화: note finding 참조 → scoped gate 비어 있음 + policy blocker 미발동.

## 7. 오픈 이슈

- [ ] carried note obligation이 pre-write evidence 텍스트("계속 유효한 pre-fix 필수 RF")에 렌더링되어 리뷰어 모델이 echo blocker를 낼 가능성 — 모델 의존이라 이번 수정 후 실사용 관찰로 판단.
- [ ] `formatReviewRepairFollowUpFeedback`의 "직전 리뷰의 차단 finding" 문구가 note-only 참조 시에도 그대로인지 — 필요 시 "요청된 finding"으로 문구 완화 (별도 판단).

## 8. 진행 로그

- 2026-07-21: 조사. 7/19 리뷰 아티팩트 2건 확인(latest: RF-001 info/evidence_gap "Review evidence warning", gate 비어 있음 / 이전 건: RF-001 low/operational_risk). 코드 추적으로 2-결함 연쇄 확정, 스크래치 재현 테스트로 두 hop 모두 확인 후 삭제. 계획서 작성.
- 2026-07-21: C안 승인, 구현 완료.
  - Fix A (pre_fix.go): `scopeReviewRunToRequestedRepairFindings`의 blocker 날조 fallback 제거 — note 참조 시 gate 목록은 실제 소속만 반영, finding은 Findings/RepairFindings 지침 채널에 유지.
  - Fix B (obligations.go/gate.go/state.go): `reviewRepairObligationCandidateFindings`(obligation ledger 기준, 기존 triple 필터 헬퍼화)와 `reviewRepairFindingsRequiringResolutionStatus`(더 엄격한 2-leg 필터) 분리. **설계 보정**: `reviewFindingLooksActionableForRepairGate`는 RequiredFix만 있으면 true라 실물 note finding("Repeat /review...")을 걸러내지 못함을 확인 — status 요구 필터에서 해당 leg 제외. `buildSingleModelReviewPolicy`(RequiresRFObligationStatus)와 `singleModelPreWritePolicyFindings`가 동일 필터 사용.
  - 테스트 3개 추가(natural_test 1, state_test 2): note 참조 시 gate 비일관 미발생 + persisted gate 무결 + status 요구 비대상, policy blocker 미발동(단위/하네스), concrete obligation은 여전히 blocker 유지 대조.
  - 검증: `go build`, `go vet` 클린. 전체 테스트 6개 실패 = 7/19 베이스라인과 동일(pre-existing), 회귀 0.
