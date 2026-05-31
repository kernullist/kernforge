# analyze-project Phase 5/6 End-to-End 요구사항

기준 시점:
- 코드베이스 기준: 2026-05-31
- 대상 기능: `/analyze-project`, `/docs-refresh`, cached project analysis QA, structural index v2, evidence packet, generated docs, dashboard
- 선행 상태: Phase 1 evidence packet/coverage ledger, Phase 2 structural index, Phase 3 build-aware precise index, Phase 4 security analyzer adapter 기반이 일부 들어온 상태를 전제로 한다.

목표:

Phase 5와 Phase 6를 하나의 완성된 분석 품질 개선 축으로 구현한다. 즉, shard를 파일/디렉토리 묶음이 아니라 graph community와 symbol/evidence packet 단위로 계획하고, LLM worker가 만든 claim은 deterministic verifier가 packet, symbol, line, graph edge, security invariant 기준으로 검증한다. 최종 산출물은 검증된 사실, 추론, 미지원 claim, security/anti-cheat overlay를 명확히 분리해야 한다.

비목표:

1. 외부 도구 강제 설치를 요구하지 않는다.
2. 내부 fallback parser와 기존 산출물 호환성을 깨지 않는다.
3. LLM에게 더 큰 원시 파일 prefix를 넣어 해결하려 하지 않는다.
4. 특정 fixture 이름이나 특정 프로젝트명에 하드코딩하지 않는다.

## 1. Phase 5: Graph-Guided Sharding / Symbol-Level Incremental Reuse

### 1.1 핵심 요구사항

1. `AnalysisShard`를 graph-aware shard로 확장한다.
   - `SeedSymbols`: shard를 시작한 symbol id 목록
   - `RequiredPacketIDs`: worker가 반드시 인용해야 하는 packet id 목록
   - `GraphNeighborhood`: selected node/edge/path summary
   - `MissingEvidenceClasses`: gap shard를 만든 evidence class
   - `GraphFingerprint`: shard reuse 판단에 쓰는 graph-level hash
2. shard planner는 semantic bucket 이후 graph community를 우선 사용한다.
   - 디렉토리 chunking은 fallback이어야 한다.
   - startup, IOCTL, callback registration, handle/memory, RPC, asset/config, build context, generated artifact path는 독립 shard 후보가 된다.
3. packet budget은 파일 수가 아니라 evidence value로 배정한다.
   - required packet, supporting packet, ambiguous packet, gap packet을 구분한다.
   - required packet이 budget 밖으로 밀리면 shard를 쪼개거나 gap으로 기록한다.
4. incremental reuse는 file hash만 보지 않는다.
   - file fingerprint
   - symbol fingerprint
   - edge fingerprint
   - build context fingerprint
   - overlay fingerprint
   - derived graph fingerprint
5. 변경 영향은 symbol neighborhood로 좁힌다.
   - symbol body 변경은 해당 symbol과 caller/callee/reverse reference shard만 invalidation한다.
   - include/build context 변경은 include resolution edge와 관련 source shard를 invalidation한다.
   - config/asset/UE metadata 변경은 asset/config graph shard만 우선 invalidation한다.
   - security rule/overlay 변경은 affected overlay edge class만 invalidation한다.

### 1.2 구현 대상

새 파일 또는 분리 권장:

1. `cmd/kernforge/analysis_graph_sharding.go`
2. `cmd/kernforge/analysis_graph_packets.go`
3. `cmd/kernforge/analysis_incremental_symbols.go`
4. 필요 시 기존 `analysis_sharding_semantic.go`, `analysis_evidence_packets.go`, `analysis_project_contracts.go`, `analysis_project.go` 보강

주요 함수 후보:

1. `buildGraphShardPlan(snapshot ProjectSnapshot, index SemanticIndexV2, mode string, desired int) []AnalysisShard`
2. `selectGraphSeeds(snapshot ProjectSnapshot, index SemanticIndexV2, mode string) []GraphShardSeed`
3. `expandGraphNeighborhood(index SemanticIndexV2, seed GraphShardSeed, policy GraphExpansionPolicy) GraphNeighborhood`
4. `allocateGraphEvidencePackets(snapshot ProjectSnapshot, shard AnalysisShard, budget EvidencePacketBudget) []EvidencePacket`
5. `buildShardGraphFingerprint(snapshot ProjectSnapshot, shard AnalysisShard) string`
6. `compareSymbolIncrementalState(previous, current AnalysisShard) InvalidationChange`

### 1.3 graph expansion 정책

`map`:
- top directory facts, build contexts, containment, critical anchors를 우선한다.

`trace`:
- entrypoint, dispatcher, caller/callee, control-flow branch, generated edge를 우선한다.

`impact`:
- changed symbol, reverse references, build ownership, generated artifacts, config/asset edges를 우선한다.

`surface`:
- untrusted source, boundary edge, dispatcher, validation/sanitizer, privileged sink, telemetry output을 우선한다.

`security`:
- IOCTL, RPC, handle, memory, callback, anti-tamper, authority boundary, taint-like edge를 우선한다.

`performance`:
- startup path, fan-in/fan-out, blocking IO, hot loop 후보, large call chain을 우선한다.

### 1.4 산출물 요구사항

run별 및 `latest/`에 다음을 저장한다.

1. `graph_shards.json`
2. `graph_reuse.json`
3. `evidence_graph.json`
4. 기존 `analysis_preflight.json`에 graph shard contract 확장
5. 기존 generated docs에 `EVIDENCE_GRAPH.md` 또는 `BUILD_AND_ARTIFACTS.md`/`STRUCTURAL_INDEX.md` 내 graph shard 섹션 추가
6. dashboard에 graph shard/reuse/packet coverage summary 추가

### 1.5 완료 기준

1. startup/IOCTL/callback/asset-config path가 각각 별도 evidence packet set으로 분석된다.
2. worker prompt가 file prefix 중심으로 되돌아가지 않는다.
3. `context_truncated` 또는 `file_excerpt` fallback이 required packet을 대체하지 않는다.
4. 단일 symbol body 변경은 전체 shard 재분석으로 번지지 않는다.
5. build context 또는 include path 변경은 관련 include edge와 dependent shard만 invalidation한다.
6. fixture에서 graph fingerprint 변화와 reuse 결정이 deterministic하게 재현된다.

## 2. Phase 6: Deterministic Claim Verifier / Security Anti-Cheat Overlay

### 2.1 핵심 요구사항

1. claim verifier는 항상 실행된다.
   - 모델 reviewer가 skipped여도 deterministic verifier는 mandatory다.
   - verifier 결과는 shard report, run summary, docs, dashboard에 남는다.
2. claim은 evidence packet을 통해서만 high-confidence fact가 될 수 있다.
   - high-confidence claim이 packet id를 갖지 않으면 blocking 또는 downgrade 대상이다.
   - packet id가 존재해도 symbol/line/graph edge가 맞지 않으면 unsupported다.
3. verifier는 다음을 검사한다.
   - packet id 존재 여부
   - packet shard/source scope 일치
   - cited source anchor가 packet line range 안에 있는지
   - cited symbol name이 packet symbol 또는 structural index symbol과 맞는지
   - flow step이 `ReferenceRecord`, `CallEdge`, `BuildOwnershipEdge`, `GeneratedCodeEdge`, overlay edge 중 하나로 연결되는지
   - deterministic architecture fact pack과 충돌하지 않는지
   - security boundary invariant를 위반하지 않는지
4. final synthesis는 verified fact와 unsupported claim을 섞지 않는다.
   - `Verified Facts`
   - `Inferences`
   - `Unsupported Or Downgraded Claims`
   - `Security / Anti-Cheat Overlay`
   - `Verification Follow-Through`
5. security/anti-cheat overlay는 Windows/driver/UE 모두에 동작한다.
   - Windows: driver entry, IRP, IOCTL, callback, handle, memory, service/RPC, telemetry
   - UE: RPC authority, replicated sensitive state, asset/config trust boundary, anti-cheat/integrity module

### 2.2 구현 대상

새 파일 또는 분리 권장:

1. `cmd/kernforge/analysis_claim_verifier.go`
2. `cmd/kernforge/analysis_security_overlay.go`
3. `cmd/kernforge/analysis_claim_docs.go`
4. 필요 시 `analysis_docs.go`, `analysis_dashboard.go`, `analysis_project_contracts.go`, `analysis_index_v2.go` 보강

주요 타입 후보:

1. `ClaimVerificationResult`
2. `ClaimVerificationIssue`
3. `VerifiedClaim`
4. `UnsupportedClaim`
5. `SecurityOverlayNode`
6. `SecurityOverlayEdge`
7. `SecurityOverlaySummary`

주요 함수 후보:

1. `verifyAnalysisClaims(snapshot ProjectSnapshot, run ProjectAnalysisRun) ClaimVerificationReport`
2. `verifyClaimEvidencePackets(claim AnalysisClaim, packets []EvidencePacket) []ClaimVerificationIssue`
3. `verifyClaimSourceAnchors(claim AnalysisClaim, packets []EvidencePacket, index SemanticIndexV2) []ClaimVerificationIssue`
4. `verifyClaimGraphEdges(claim AnalysisClaim, index SemanticIndexV2) []ClaimVerificationIssue`
5. `buildSecurityAntiCheatOverlay(snapshot ProjectSnapshot, index SemanticIndexV2) SecurityOverlaySummary`
6. `applyClaimVerificationToReports(run *ProjectAnalysisRun, report ClaimVerificationReport)`

### 2.3 verifier 판정 정책

`verified`:
- packet id가 존재하고 source anchor 또는 symbol이 packet/structural index와 일치하며, claim 종류에 필요한 graph edge 또는 deterministic fact가 확인된다.

`inference`:
- packet 근거는 있지만 graph edge가 직접 연결되지 않아 합리적 추론으로만 볼 수 있다.

`downgraded`:
- claim이 high confidence였지만 packet/anchor/edge 중 일부가 부족하다.

`unsupported`:
- packet id가 없거나 source scope 밖 anchor를 인용하거나 deterministic fact와 충돌한다.

`blocking`:
- high-confidence unsupported claim, line/symbol 오라벨링, security boundary inversion, verified fact pack 충돌, final doc fact 승격 시도.

### 2.4 security/anti-cheat overlay 요구사항

노드:

1. `untrusted_input`
2. `validation_gate`
3. `dispatcher`
4. `privileged_sink`
5. `callback_registration`
6. `runtime_activation`
7. `tamper_sensitive_state`
8. `authority_boundary`
9. `asset_config_boundary`
10. `telemetry_sink`

엣지:

1. `input_reaches_dispatcher`
2. `dispatcher_routes_to_handler`
3. `handler_requires_validation`
4. `validated_before_sink`
5. `missing_validation_candidate`
6. `registers_callback`
7. `activates_runtime_filter`
8. `crosses_user_kernel_boundary`
9. `crosses_client_server_boundary`
10. `writes_telemetry`

Windows/anti-cheat detector 최소 범위:

1. `DeviceIoControl`, `IRP_MJ_DEVICE_CONTROL`, IOCTL code switch/dispatch
2. `METHOD_BUFFERED`, `METHOD_IN_DIRECT`, `METHOD_OUT_DIRECT`, `METHOD_NEITHER` risk marker
3. `ProbeForRead`, `ProbeForWrite`, size/shape validation, request origin validation
4. `ObRegisterCallbacks`, `PsSet*NotifyRoutine`, `FltRegisterFilter`, WFP/registry callback family
5. process/thread/handle/memory scanner path
6. service/RPC/named pipe/socket command boundary
7. telemetry persistence/output boundary

UE/anti-cheat detector 최소 범위:

1. `UFUNCTION(Server|Client|NetMulticast)` authority edge
2. replicated property and RepNotify sensitive state
3. `PlayerController`, `GameMode`, `GameState`, `Pawn`, `Character`, subsystem role edge
4. `LoadObject`, `LoadClass`, `TSoftObjectPtr`, config map/game mode default
5. anti-cheat/integrity/tamper/security named modules and plugins

### 2.5 산출물 요구사항

run별 및 `latest/`에 다음을 저장한다.

1. `claim_verification.json`
2. `unsupported_claims.json`
3. `security_overlay.json`
4. `SECURITY_OVERLAY.md`
5. `UNSUPPORTED_CLAIMS.md`
6. 기존 final report에 verified/inference/unsupported 분리 섹션
7. dashboard에 claim verification status, blocking issue count, unsupported high-confidence count, overlay node/edge summary 추가

### 2.6 완료 기준

1. line/symbol 오라벨링은 blocking violation으로 남는다.
2. unsupported high-confidence claim은 final doc에서 fact로 합성되지 않는다.
3. final report에는 verified facts, inferences, unsupported claims가 분리된다.
4. Windows driver fixture에서 IOCTL input -> validation -> privileged handler overlay가 생성된다.
5. callback registration fixture에서 registration과 runtime activation이 구분된다.
6. UE fixture에서 RPC authority boundary와 asset/config trust boundary가 생성된다.
7. model reviewer skipped 환경에서도 claim verifier 결과가 남는다.

## 3. 테스트 요구사항

단위 테스트:

1. graph seed selection
2. graph neighborhood expansion
3. evidence packet budget allocator
4. graph fingerprint/reuse decision
5. claim packet existence verifier
6. claim source anchor/symbol verifier
7. graph edge verifier
8. security overlay detector

통합 테스트:

1. non-Unreal Windows driver/security fixture
2. duplicate header/build context fixture
3. callback registration fixture
4. UE RPC/asset/config fixture
5. single-symbol change incremental reuse fixture
6. unsupported high-confidence claim fixture
7. model-review-skipped but deterministic-verified fixture

검증 명령:

1. focused tests for new files
2. `go vet ./cmd/kernforge`
3. `go test ./cmd/kernforge -count=1 -timeout 15m`
4. `git diff --check`

## 4. 문서/UX 요구사항

1. README/README_kor의 Project Analysis 섹션에 Phase 5/6 결과를 반영한다.
2. FEATURE_USAGE_GUIDE/FEATURE_USAGE_GUIDE_kor에 graph shard, claim verifier, security overlay 산출물을 설명한다.
3. ROADMAP_kor에는 완료/남은 범위를 갱신한다.
4. generated docs manifest와 dashboard drilldown에 새 문서를 연결한다.
5. CLI handoff는 다음 명령을 제안한다.
   - `/analyze-dashboard latest`
   - `/docs-refresh`
   - `/verify`
   - `/simulate stealth-surface`
   - `/fuzz-campaign run`

## 5. 호환성 및 운영 요구사항

1. 기존 JSON 산출물 필드는 삭제하지 말고 새 필드는 `omitempty`로 추가한다.
2. `latest/` mirror는 기존 방식처럼 매 run 교체한다.
3. external adapter가 없어도 internal deterministic graph path로 동작해야 한다.
4. Windows/PowerShell에서 tests, gofmt, build 경로가 깨지면 안 된다.
5. `gofmt`는 수정한 Go 파일에만 scope를 좁힌다.
6. prompt/status/log/comment는 ASCII를 유지한다.
7. commit/push는 사용자가 명시적으로 요청하기 전에는 하지 않는다.

## 6. End-to-End Definition Of Done

1. Phase 5 graph-guided sharding이 실제 worker prompt/evidence packet selection에 연결되어 있다.
2. symbol-level incremental reuse가 shard cache decision과 invalidation reason에 반영된다.
3. Phase 6 claim verifier가 모든 run에서 실행되고 artifact로 저장된다.
4. security/anti-cheat overlay가 semantic index, docs, dashboard, final report에 연결된다.
5. unsupported high-confidence claim이 final fact로 승격되지 않는 회귀 테스트가 있다.
6. Windows driver, UE RPC/config, callback, duplicate include, symbol reuse fixture가 모두 통과한다.
7. README/guide/roadmap/generated docs/dashboard가 실제 구현과 일치한다.
8. `go vet ./cmd/kernforge`, `go test ./cmd/kernforge -count=1 -timeout 15m`, `git diff --check`가 통과한다.
