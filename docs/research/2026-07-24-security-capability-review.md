# 연구 노트: Kernforge 보안 전문 기능 검토 및 개선 제안

- 작성일: 2026-07-24
- 상태: 결론 도출 + P0 구현 완료
- 구현 반영: 2026-07-24 P0 Stage A–E 코드/테스트/문서 완료 (`fdb8f46`, 스켑틱 수정 `4689593`)
- 문서 최신화: 2026-07-24 FEATURE 한/영, README 한/영, driver playbook, CHANGELOG, ROADMAP
- 관련: [[ROADMAP_kor.md]], [[FEATURE_USAGE_GUIDE.md]], [[FEATURE_USAGE_GUIDE_kor.md]], [[PLAYBOOK_driver_kor.md]], [[PLAYBOOK_driver.md]], [[docs/CHANGELOG.md]]

## 질문

Kernforge의 소스 퍼징, 드라이버 템플릿, 보안 분석/검증 기능을 현재 코드·문서 기준으로 평가하고, 2025–2026 보안·퍼징·anti-cheat 기술 동향에 맞춰 개선·추가할 보안 전문 기능을 우선순위와 함께 제안한다.

## 결론 (먼저 쓴다)

Kernforge는 **source-only triage → campaign/corpus → sanitizer·verifier evidence → verification gate** 루프와 Windows driver/Unreal/telemetry 도메인 지식을 제품 중심에 둔다. **P0 다섯 축은 2026-07-24 기준 구현 완료**: (1) IOCTL contract + multi-call sequence seeds, (2) crash feasibility(`spurious` 격리, source-scan lifecycle 포함), (3) platform-security posture collector, (4) driver POC → security handoff, (5) harness build-repair(분류 + 1–3회 + durable blockers). 다음 병목은 P1/P2(추가 template, ETW schema fuzz, lab VM orchestration, syzlang export 등)이다.

## 환경

- 코드베이스: `C:\git\kernforge` (main, 2026-07-24 스냅샷)
- 문서 기준: `README_kor.md`, `ROADMAP_kor.md`, `FEATURE_USAGE_GUIDE.md`, driver/telemetry/memory-scan playbooks
- 외부 동향 조사일: 2026-07-24

## 현재 역량 맵 (코드/문서 기준)

### A. Project intelligence (보안 표면)

| 기능 | 상태 | 핵심 산출/명령 |
|---|---|---|
| multi-mode analysis | 강함 | `/analyze-project --mode map\|trace\|impact\|surface\|security\|performance` |
| security overlay | 강함 | driver/IOCTL/callback/handle/memory/RPC/telemetry + Unreal integrity |
| deterministic claim verifier | 강함 | `UNSUPPORTED_CLAIMS.md`, high-confidence claim 검증 |
| docs portal | 강함 | `SECURITY_SURFACE.md`, `FUZZ_TARGETS.md`, `VERIFICATION_MATRIX.md`, dashboard attack-flow |

### B. Source-level fuzzing workbench

| 기능 | 상태 | 비고 |
|---|---|---|
| `/fuzz-func` source triage | 강함 | guard/probe/copy/dispatch, counterexample, `harness.cpp` |
| `/source-scan` matchers | 강함 | double-fetch, IOCTL infoleak, WDF size drift, pool lifetime, Unreal RPC, telemetry parser 등 |
| `/fuzz-campaign` | 중상 | corpus 승격, dedup finding lifecycle, coverage ingest, sanitizer/AV/DV artifact 수집 |
| native engine | 중 | 기본 libFuzzer; AFL 스크립트 재사용 경로 존재; WinAFL/HLK DF는 handoff 수준 |
| crash minimization | 부분 | minimization command 생성, 본격 auto-minimize 루프는 약함 |

### C. Driver / live ops

| 기능 | 상태 | 비고 |
|---|---|---|
| `/create-driver-poc` | 중 | default WDM IOCTL, objectfilter, minifilter, registryfilter, wfpcallout |
| `/investigate driver-visibility` | 약중 | user-mode 가시성·서비스·verifier 상태 위주, deep load-fail RCA 아님 |
| `/simulate` | 중 | tamper / stealth / forensic-blind-spot 프로파일 |
| security verify categories | 중 | driver/telemetry/unreal/memory-scan 분류 + adaptive plan |

### D. Root-cause / harness / policy

| 기능 | 상태 | 비고 |
|---|---|---|
| `/find-root-cause` + pattern packs | 강함 | causal chain + reviewer + deep verification |
| coding harness | 강함 | artifact/scenario/subagent/test-impact/job gates |
| hooks `windows-security` preset | 중 | 정책 엔진 존재, domain rule 깊이 확장 여지 |

## 강점 요약

1. **Source-first 퍼징**: 컴파일 전에도 공격 입력 모델·분기 반례·하네스 초안을 남긴다. 보안 리뷰 초동에 유리.
2. **Evidence graph 일관성**: analysis docs ↔ fuzz campaign ↔ verification ↔ memory가 한 루프.
3. **Windows/Unreal 도메인 matcher**: double-fetch, METHOD_NEITHER, WDF buffer, ObCallback, minifilter context 등 실무 bug class에 직접 맞음.
4. **완성 게이트**: 범용 agent보다 “검증 없는 완료 주장”을 잘 막음.
5. **Playbook 운영화**: driver/telemetry/memory-scan 작업 순서가 문서화되어 있음.

## 약점 / 갭 요약

1. **Stateful kernel fuzz 부족**: 단일 함수/단일 IOCTL 위주. lifecycle·글로벌 상태 의존 시퀀스(LifeFuzz류) 미흡.
2. **Harness 품질 루프**: 생성 후 compile-error triage → 재생성 → coverage 평가의 agentic 폐루프가 약함 (OSS-Fuzz-gen/HarnessAgent 대비).
3. **Crash 신뢰도**: stack/fingerprint dedup은 있으나, harness 오용 기반 spurious crash 필터(Crash Validation Agent류) 약함.
4. **드라이버 템플릿 깊이**: POC 4종은 시작점으로 충분하나 KMDF queue/IRP cancel, process protect, image notify full stack, signing pipeline 연동은 얕음.
5. **Live kernel 실측**: driver-visibility는 user-mode 관찰 중심. HVCI/VBS/Secure Boot/TPM readiness 체크리스트·Diff 약함.
6. **Anti-cheat 특화 깊이**: Unreal RPC/integrity overlay는 있으나, client trust boundary matrix, replay/integrity corpus, BYOVD surface inventory는 약함.
7. **Binary/closed-source 경로**: 소스 전제. Driver Buddy류 IOCTL 복원·binary-only triage 경로 없음 (의도적일 수 있으나 갭).

## 외부 동향 (2025–2026)과 시사점

### 1) Kernel / IOCTL 퍼징

- **LifeFuzz (2026)**: 글로벌 변수 lifecycle 모델링으로 dependency-respecting IOCTL 시퀀스 생성. → Kernforge campaign에 **state machine / precondition seed** 필요.
- **IOCTL-Hammer**: METHOD buffer descriptor 4종 중심 lightweight harness. → 이미 있는 METHOD_NEITHER matcher를 **파라미터-센트리 네이티브 하네스 템플릿**으로 승격 가능.
- **KernelGPT**: LLM으로 Syzlang-style spec 합성 → coverage 상승. → Kernforge는 source observation을 이미 가짐; **syzlang/IOCTL contract DSL export**가 자연스러운 다음 단계.
- **MS IoSpy/IoAttack**: white-box capture → fuzz. → `/investigate` 후 **captured IOCTL corpus import** 경로.
- **산업 실무(대량 driver audit)**: static surface rank → LLM audit → VM harness → PoC validate. Kernforge는 앞 2단계는 강하고, **VM/lab harness orchestration**이 다음 병목.

### 2) AI-assisted fuzz / exploitgen harness

- **OSS-Fuzz-gen**: multi-agent harness gen + **Crash Validation Agent**로 spurious crash ~65% 필터.
- **HarnessAgent / OSS-Fuzz agent build**: compile-error triage로 빌드 폐루프.
- **Semgrep 2026 harness taxonomy**: LLM-led exploitgen / skill-boosted audit / SAST+LLM hybrid. Kernforge는 hybrid+skill 쪽; exploitgen 쪽은 의도적으로 약할 수 있으나 **validated finding → minimal PoC**는 제품 가치.
- **Mozilla Mythos 교훈**: discovery만으로 부족, **dedup → triage → patch validate → release gate** 파이프라인이 스케일을 만든다. Kernforge lifecycle MVP는 방향이 맞음.

### 3) Anti-cheat / platform security

- **VBS/HVCI/Memory Integrity**가 게임·AC 런타임 전제조건으로 강화 (예: Vanguard on-demand 조건 축: Secure Boot, TPM 2.0, VBS, HVCI, IOMMU).
- 시사점: driver/AC 제품 개발 시 **“기능 correctness”와 별개로 platform readiness 검증**이 1급 요구사항. Kernforge verify/investigate에 **platform security posture collector**가 들어가야 함.
- **BYOVD**: 취약 서명 드라이버 IOCTL surface inventory가 실무 이슈. 자체 드라이버 개발 도구로서 **dangerous API/IOCTL privilege matrix** 문서화가 방어 가치.

### 4) AI-generated code 보안 부채

- 2025–2026 벤치에서 AI 생성 코드 OWASP 실패율 정체(~45%). Kernforge coding harness는 문서/검증 게이트에 강함 → **보안 특화 post-change SAST+pattern gate**를 더 명시적으로 붙일 여지.

## 제안 기능 (우선순위)

### P0 — 즉시 제품 차별화를 키우는 것

#### P0-1. IOCTL Contract Fuzz Profile + Sequence Campaign — **구현됨 (2026-07-24)**

**무엇**: IOCTL code, METHOD_*, in/out length, structure fields, pre/post state를 contract로 추출하고, multi-call 시퀀스 corpus를 campaign에 넣는다.

**착륙**: `ioctl_contract.json` (`kernforge.ioctl_contract.v1`), METHOD_*/buffer length/dispatch anchors, multi-call open/ioctl/close sequence seeds via campaign promote (`ioctl_sequence`). 테스트: `TestFunctionFuzzIOCTLContractArtifactAndSequenceSeeds`.

#### P0-2. Harness Build-Repair Loop (compile feedback) — **구현됨 (2026-07-24)**

**무엇**: 생성된 `harness.cpp` 빌드 실패를 분류(missing include / unresolved symbol / WDK macro / wrong ABI)하고 자동 재생성 또는 타깃 수정 제안을 1–3회 돌린다.

**착륙**: `functionFuzzClassifyCompileFailure` buckets + `functionFuzzDriveBuildRepairLoop` cap=3 + durable `BuildBlockers`/build log. 테스트: `TestFunctionFuzzClassifyCompileFailureBuckets`, `TestFunctionFuzzDriveBuildRepairLoop*`.

#### P0-3. Crash Validation / Feasibility Gate — **구현됨 (2026-07-24)**

**무엇**: crash를 바로 finding으로 승격하지 않고, harness misuse vs target bug 가능성을 판정. stack이 harness 전용 코드에만 있으면 `spurious`로 분류.

**착륙**: `fuzzCampaignValidateCrashFeasibility`; finding status `spurious`는 verification required / feature block_close 없음. 테스트: `TestFuzzCampaignValidateCrashFeasibility*`, `TestBuildFuzzCampaignNativeFindingQuarantinesSpurious`.

#### P0-4. Platform Security Posture Collector — **구현됨 (2026-07-24)**

**무엇**: `/investigate start platform-security`: Secure Boot, VBS, HVCI/Memory Integrity, Test Signing, Driver Signature Enforcement, TPM readiness.

**착륙**: preset + aliases; snapshot `Attributes` 6 fields (unavailable when missing); findings category `platform_security`. 테스트: `TestPlatformSecurityPresetRegisteredAndPostureParsed`.

#### P0-5. Driver POC → Fuzz/Verify 자동 handoff — **구현됨 (2026-07-24)**

**무엇**: `/create-driver-poc` 직후 security workflow handoff와 type-aware seed 파일.

**착륙**: handoff (source-scan / fuzz-func / campaign / verify / platform-security / signing / Driver Verifier) + `.kernforge/security/workflow_seed.json`. 테스트: `TestCreateDriverPOCSecurityHandoff*`.

### P1 — 도메인 깊이 확장

#### P1-1. 추가 Driver Template Pack

| type | 목적 |
|---|---|
| `processprotect` | ObCallback + process/thread protect 패턴, access-mask matrix |
| `imagenotify` | load-image notify + path/hash policy |
| `kmdf-queue` | KMDF sequential/parallel queue, cancel, buffer retrieval |
| `etw-provider` | provider/manifest + user decoder 짝 |
| `nmi-watchdog` / `integrity-check` | periodic integrity sample (AC 쪽) |

각 템플릿에 **의도적 insecure branch 옵션(`--with-vuln-lab`)** 을 두면 source-scan/fuzz 회귀 데모에 활용 가능 (lab only, 기본 off).

#### P1-2. Windows Kernel Source Matcher 확장

추가 slug 후보:
- `irql-raise-lower-mismatch`
- `lookaside-vs-pool-mixed-free`
- `mdl-map-unlock-lifetime`
- `cancel-safe-queue-race`
- `wfp-classify-out-of-band`
- `registry-callback-reentrancy`
- `toctou-path-object-manager`
- `kmdf-request-complete-double`

#### P1-3. ETW / Telemetry Schema Fuzz

manifest/schema drift 감지 + decoder에 malformed event corpus. `PLAYBOOK_telemetry`와 `/verify` telemetry category 연결.

#### P1-4. Unreal / Anti-Cheat Integrity Matrix

- authority: Server/Client/Multicast RPC × validation presence
- replication field trust map
- config/asset integrity check coverage
- “client-trusted decision” finding 전용 severity

#### P1-5. Memory-Scan Synthetic Corpus Generator

`/simulate stealth-surface` 결과를 **synthetic region/pattern corpus**로 승격 → scanner regression + FP/FN 리포트. 현재 playbook의 약한 자동화 지점.

#### P1-6. Security Post-Change Gate (AI-code aware)

changed path에 대해 고정 규칙:
- 새 IOCTL without size check pattern
- `ExAllocatePool` without tag / size overflow
- unbounded `memcpy` after attacker length
- Unreal `Server` RPC without authority check

coding harness warning/blocker로 연결 (deny는 보수적으로).

### P2 — 차별화 연구 / 중장기

#### P2-1. Syzlang / IOCTL DSL Export

source contract → 외부 fuzzer(syzkaller-win 실험, 사내 harness)로 export. Kernforge를 “스펙 컴파일러”로 포지셔닝.

#### P2-2. Lab VM / Snapshot Fuzz Orchestration

QEMU/Hyper-V lab profile: boot → load driver → fuzz → crash dump pull → campaign attach. 안전상 기본 off, 명시 lab config only.

#### P2-3. Binary-assisted Surface Recovery (opt-in)

PDB/public symbols 또는 제한적 static PE 스캔으로 IOCTL table 후보 추출. 소스 없는 third-party driver 감사(방어/BYOVD inventory) 용. 공격 자동화 가이드는 제외.

#### P2-4. Finding → Minimal Patch Proposal Loop

validated crash에 대해 최소 가드 패치 제안 + `/verify` + regression seed 고정. OSS-Fuzz agentic repair 동향 대응. 자동 적용은 항상 human-in-the-loop.

#### P2-5. Evidence-backed Threat Model Live View

dashboard attack-flow를 **변경 diff 민감**하게: 이번 PR이 어느 trust boundary를 건드렸는지. PR review automation과 결합.

#### P2-6. Continuous Security Campaign (local automation)

`/automation`에 `fuzz-campaign smoke`, `platform-security posture`, `source-scan delta` 슬롯. cloud job 전에 로컬 반복 가치 최대화.

## 하지 말아야 할 것 (반증 / 범위 밖)

1. **범용 SAST 제품 복제**: CodeQL/Semgrep 대체는 비목표. Kernforge는 evidence-backed Windows/AC workbench.
2. **공격용 exploit chain 자동화**: validated finding + defensive PoC 수준에 제한.
3. **항상-on 커널 퍼징**: 호스트 안정성 위험. lab/snapshot 전제 없으면 native kernel fuzz 기본 실행 금지 유지.
4. **Cloud-only security backend 성급 도입**: 로컬 evidence/campaign 완성도가 먼저.
5. **Matcher slug 무분별 확장**: noisy tier 증가 시 source-scan 신뢰 붕괴. precise/normal/noisy 등급 유지 + calibration 필수.

## 권장 로드맵 정렬 (90일 스케치)

| 기간 | 초점 | 산출 |
|---|---|---|
| 0–30일 | P0-2 harness build-repair, P0-5 driver handoff, P0-3 crash validation MVP | 빌드 성공률·오탐 분류 artifact |
| 30–60일 | P0-1 IOCTL contract+sequence, P0-4 platform posture | `ioctl_contract.json`, `/investigate platform-security` |
| 60–90일 | P1 matchers/templates, Unreal integrity matrix, telemetry schema fuzz | template pack + overlay docs 강화 |

기존 ROADMAP P0 Fuzzing Workbench의 남은 항목(harness quality, Windows/anti-cheat specialization, campaign stop/triage/minimize UX)과 **정합**. 신규 항목은 ROADMAP에 “P0 보안 차별 축 보강” 섹션으로 흡수 권장.

## 실험 및 관찰

### 관찰 1: 코드 내 보안 표면 구현 존재 확인

- 가설: fuzz/driver/source-scan이 문서 수준이 아니라 구현되어 있다.
- 절차: `cmd/kernforge` 내 `commands_fuzz_func.go`, `fuzz_campaign.go`, `source_scan.go`, `create_driver_poc.go`, `investigation_collectors.go`, `analysis_security_overlay.go` 존재 및 matcher slug 목록 확인.
- 관찰: double-fetch, ioctl-output-infoleak, wdf-request-buffer-size-drift, pool-lifetime-refcount, unreal-rpc-trust-boundary, telemetry-parser-untrusted-buffer 등 구현 확인. driver POC type 4종 + default.
- 해석: P0 문서화 수준이 아니라 실사용 가능한 기반. 개선은 greenfield가 아니라 **심화**.

### 관찰 2: ROADMAP 자기평가와 외부 갭 정합

- 가설: ROADMAP이 이미 fuzz workbench 잔여를 인지한다.
- 절차: `ROADMAP_kor.md` P0 Fuzzing Workbench “우선 강화할 전문 기능” 및 완료 항목 대조.
- 관찰: coverage ingest·sanitizer/DV artifact·finding lifecycle 완료. harness quality 분리, Windows specialization profile, campaign triage/minimize UX는 여전히 우선 강화 목록.
- 해석: 본 제안 P0-1~3은 ROADMAP과 충돌 없이 구체화.

## 반증 / 실패한 시도

- “범용 커널 커버리지 퍼저(syzkaller 완전 이식)를 Kernforge 코어에 넣는 것”은 제품 정체성·유지비 측면에서 기각. **export + orchestration**이 맞음.
- “AI 보안 스캐너 전면 대체”는 기각. claim verifier + domain matcher + evidence gate가 차별 축.

## 레퍼런스

### 내부

- `README_kor.md` — 제품 포지션, source-level fuzz 설명
- `ROADMAP_kor.md` — P0 analysis/fuzz/root-cause/hooks
- `FEATURE_USAGE_GUIDE.md` § Source-Level Function Fuzzing
- `cmd/kernforge/source_scan.go` — `defaultSourceMatchers()`
- `cmd/kernforge/create_driver_poc.go` — POC types
- `cmd/kernforge/fuzz_campaign.go`, `commands_fuzz_func.go`
- Playbooks: driver / telemetry / memory-scan

### 외부 (동향)

- LifeFuzz lifecycle-guided Windows driver fuzzing (ACM, 2026)
- Microsoft IoSpy / IoAttack IOCTL·WMI fuzz
- IOCTL-Hammer (parameter-centric IOCTL harness)
- KernelGPT (LLM kernel syscall/driver specs)
- OSS-Fuzz-gen / OSS-Fuzz LLM target generation + crash validation agents
- HarnessAgent (tool-augmented harness construction, 2025)
- Mozilla Mythos / agentic bug lifecycle (dedup→patch→release)
- Microsoft VBS/HVCI (Memory Integrity) driver guidance
- 2026 anti-cheat platform requirements trend (Secure Boot/TPM/VBS/HVCI/IOMMU)

## 미해결

- [ ] IOCTL contract 추출의 정적 정확도 한계(매크로/테이블 간접 dispatch) 벤치 필요
- [ ] Crash validation 휴리스틱의 false-negative(진짜 버그를 spurious로 분류) 측정 설계
- [ ] Lab VM orchestration의 최소 안전 정책(호스트 차단, snapshot 강제) 문서화
- [ ] Binary-assisted surface recovery 범위를 방어 inventory로 제한하는 제품 문구
- [ ] 본 제안을 `ROADMAP_kor.md`에 공식 섹션으로 흡수할지 제품 결정 필요
