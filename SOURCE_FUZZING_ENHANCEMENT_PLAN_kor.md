# Kernforge 소스 코드 퍼징 강화 플랜

작성 기준일: 2026-05-18

이 문서는 Kernforge의 소스 코드 기반 퍼징(`/fuzz-func`, `/fuzz-campaign`) 기능을 현재 코드 상태에서 어떻게 강화할지 정리한 플랜이다. 상위 GAP 분석 문서(`FUZZING_SECURITY_TOOLS_GAP_ANALYSIS_kor.md`)와 `ROADMAP_kor.md`의 "Fuzzing Workbench" 항목을 받아, 실제 소스 위치 기반의 실행 계획으로 좁힌다.

연관 문서:
- [FUZZING_SECURITY_TOOLS_GAP_ANALYSIS_kor.md](FUZZING_SECURITY_TOOLS_GAP_ANALYSIS_kor.md): 외부 도구 대비 격차 분석
- [ROADMAP_kor.md](ROADMAP_kor.md): P0 Fuzzing Workbench 마일스톤
- [PROJECT_ANALYSIS_NEXT_SPEC_kor.md](PROJECT_ANALYSIS_NEXT_SPEC_kor.md): 분석 산출물에서 fuzz 후보로 이어지는 흐름

## 1. 현황 진단

### 강점

- 소스 단위 fuzz 후보 발굴이 두텁다. [`commands_fuzz_func.go`](cmd/kernforge/commands_fuzz_func.go)는 symbol, overlay domain, parameter strategy, sink signal, code observation(`copy_sink`, `probe_sink`, `dispatch_guard`, `size_guard`, `alloc_site`, `cleanup_path`, `state_publish`), branch outcome, virtual scenario를 plan 자료구조로 누적한다.
- 산출물 형식이 정형화돼 있다. `plan.json`, `report.md`, `harness.cpp`와 `.kernforge/fuzz/<campaign-id>/{corpus,crashes,coverage,reports,logs}` 표준이 존재한다.
- Campaign artifact graph와 finding dedup이 1급. [`fuzz_campaign.go:597`](cmd/kernforge/fuzz_campaign.go:597) `fuzzCampaignFindingDedupKey`, [`fuzz_campaign.go:914`](cmd/kernforge/fuzz_campaign.go:914) `buildFuzzCampaignArtifactGraph`가 native result, evidence, source anchor, verification gate, tracked feature gate를 한 그래프로 묶는다.
- Coverage gap inference와 docs catalog feedback이 이미 들어가 있다. `FUZZ_TARGETS.md` 랭킹이 다음 라운드 분석에 재사용된다.

### 한계

- **엔진 선택지가 사실상 하나다.** [`commands_fuzz_func.go:9818`](cmd/kernforge/commands_fuzz_func.go:9818) `functionFuzzBuildExecutionArgs`는 `clang`/`clang-cl`만 지원하고 결과적으로 `libFuzzer + ASan/UBSan` 단일 프로필이다. AFL++/WinAFL/honggfuzz는 plan의 `secondary_engines`/`notes`에만 등장하고 실제 wrapper가 없다.
- **Fuzz 실행이 "한 번 짧게" 모델이다.** [`commands_fuzz_func.go:9989`](cmd/kernforge/commands_fuzz_func.go:9989) `functionFuzzRunArgs`가 `-max_total_time=20 -timeout=5 -rss_limit_mb=4096`로 하드코딩된 스모크 fuzz 한 번을 돌리고, [`commands_fuzz_func.go:10153`](cmd/kernforge/commands_fuzz_func.go:10153)에서 PowerShell 스크립트를 background shell job 하나로 실행한 뒤 crash dir 파일 개수만 폴링한다([`commands_fuzz_func.go:10262`](cmd/kernforge/commands_fuzz_func.go:10262)).
- **Corpus 라이프사이클이 없다.** seed가 [`commands_fuzz_func.go:10018`](cmd/kernforge/commands_fuzz_func.go:10018) `functionFuzzWriteSeedCorpus`에서 3개 하드코딩(`seed-empty.bin`, `seed-pattern.bin`, `seed-structured.bin`)으로 끝난다. `-merge=1`, dedup, replay, import provenance가 없다.
- **Crash triage가 native 단계에서 비어 있다.** repro(`-runs=1 -error_exitcode=N`), minimize(`-minimize_crash=1`), bucket이 명령으로 노출돼 있지 않다. campaign manifest의 finding dedup에는 들어가지만 그 전 단계가 비어 있다.
- **Sanitizer/libFuzzer 로그 파서가 없다.** `BuildLogPath`/`RunLogPath`를 tee로 받지만 `ERROR: AddressSanitizer:`, `SUMMARY: libFuzzer:`, UBSan `runtime error:` 같은 신호를 정규화해 fingerprint로 묶는 경로가 없다.
- **Source 사실 -> fuzz 자산 변환이 아깝다.** AST/symbol 인덱스에 enum, magic constant, length-prefix, format token이 들어있지만 libFuzzer `-dict=`, structure-aware grammar, boundary corpus로 떨어뜨리는 파이프라인이 없다.

## 2. 차별화 축

일반 fuzz 엔진이 약하고 Kernforge가 잘하는 영역에 자원을 집중한다.

1. **소스 사실 -> fuzz 자산 변환**
   - enum/magic/IOCTL code/`STATUS_*`/format token을 dictionary로 결정적으로 떨어뜨린다.
   - struct layout(packed/aligned, length-prefix)을 grammar 또는 LPM/Protobuf 스키마로 떨어뜨린다.
   - guard predicate(`if (len > N) return`, `if (header != MAGIC) return`)에서 boundary value 코퍼스를 생성한다.

2. **소스 invariant -> runtime oracle**
   - `code_observations`의 size guard/cleanup path/state publish 조건을 harness에 추가 assertion으로 emit해서 crash 없는 invariant 위반도 잡는다.

3. **Windows/anti-cheat first-class**
   - IOCTL dispatch tree, ETW provider schema, Unreal RPC unpack 같은 도메인 grammar는 일반 엔진이 흉내내기 어렵다.

## 3. 우선순위별 강화 방안

### P0 — 한 PR 단위로 즉시 가치가 나오는 항목

1. **Source-derived dictionary 자동 생성** (ROI 최상)
   - `code_observations`, `branch_outcomes`, `sink_signals`, `parameter_strategies`를 순회해 `<artifact_dir>/dict.txt`를 libFuzzer 포맷으로 emit한다.
   - 추출 대상:
     - 정수 상수 (특히 size/IOCTL/magic/`STATUS_*`/`NTSTATUS`)
     - 4/8바이트 little-endian 인코딩
     - 문자열 리터럴
     - enum value
   - [`commands_fuzz_func.go:9989`](cmd/kernforge/commands_fuzz_func.go:9989) `functionFuzzRunArgs`에 dict 비어있지 않을 때만 `-dict=<dict.txt>`를 추가한다.

2. **Deterministic boundary corpus**
   - [`commands_fuzz_func.go:10018`](cmd/kernforge/commands_fuzz_func.go:10018) `functionFuzzWriteSeedCorpus`를 교체한다.
   - `virtual_scenarios`와 parameter class별로 `zero / one / max / max-1 / signed-flip / off-by-one / very-large / null-terminator-injected` 패턴 seed를 결정적으로 생성한다.
   - 파일명은 `seed-<scenario-id>-<role>.bin`으로 통일하고 corpus manifest에 provenance(소스 위치, 파생 규칙)를 기록한다.
   - ROADMAP 항목 16(`corpus/<run-id>/scenario-XX-*.json` 승격)과 합쳐서 raw binary seed도 함께 떨군다.

3. **Multi-stage run profile (smoke / extended / repro / minimize)**
   - `FunctionFuzzExecution`에 `Profile string` 필드를 추가한다([`commands_fuzz_func.go:200`](cmd/kernforge/commands_fuzz_func.go:200)).
   - `functionFuzzRunArgs`를 profile별로 분기한다.
     - `smoke`: 기존 20초 유지 (CI 친화)
     - `extended`: `-max_total_time=600 -fork=N -ignore_crashes=1` + 사용자 지정 workers
     - `repro`: 단일 입력 파일을 positional arg로, `-runs=1 -error_exitcode=88`
     - `minimize`: `-minimize_crash=1 -runs=100000`
   - CLI:
     - `/fuzz-func continue <id> --profile extended`
     - `/fuzz-func repro <crash-path>`
     - `/fuzz-func minimize <crash-path>`

4. **Sanitizer / libFuzzer 로그 파서 -> finding 정규화**
   - 새 파일 `cmd/kernforge/commands_fuzz_func_log_parser.go`에 파서 추가.
   - 패턴:
     - `==N==ERROR: AddressSanitizer: <kind>`
     - `SUMMARY: AddressSanitizer: <kind> <addr> in <func>`
     - `SUMMARY: libFuzzer: <kind>`
     - UBSan `<file>:<line>:<col>: runtime error: <msg>`
   - 결과(sanitizer kind, top-3 frame, source:line, raw summary)를 [`fuzz_campaign.go:510`](cmd/kernforge/fuzz_campaign.go:510) finding 정규화 경로로 흘려서 `fuzzCampaignFindingDedupKey` fingerprint가 채워지게 한다.

### P1 — 다음 마일스톤 단위

5. **`/fuzz-crash` 명령군 신설** (`list | repro | minimize | bucket | promote`)
   - `bucket` 키: (sanitizer kind, top-3 frame hash, source:line).
   - `promote`는 crash를 `investigation finding`으로 승격하고 persistent memory feedback과 evidence graph에 연결한다.

6. **`/fuzz-corpus` 명령군** (`list | import | dedup | minimize | replay`)
   - 1차로 libFuzzer `-merge=1` wrapper 하나만 만들어도 dedup/minimize가 동시에 해결된다.
   - import 시 sha256/size/source/sensitive 플래그를 corpus manifest에 기록한다.

7. **AFL++ wrapper** (옵셔널 빌드)
   - [`commands_fuzz_func.go:9818`](cmd/kernforge/commands_fuzz_func.go:9818) `functionFuzzBuildExecutionArgs`에 `style == "afl-clang-fast"`를 추가한다.
   - `LLVMFuzzerTestOneInput` 시그니처는 유지하고 persistent mode 매크로만 emit한다.
   - run 명령은 `afl-fuzz -i corpus -o out -- ./harness @@` 형태로 분리한다.
   - Windows는 WSL 또는 Linux remote build를 1차 지원한다.

8. **Coverage trend & plateau detector**
   - [`fuzz_campaign.go`](cmd/kernforge/fuzz_campaign.go)의 `collectFuzzCampaignCoverageReports`가 cumulative edge count time series를 manifest에 적도록 확장한다.
   - plateau 판정 (예: 5분간 +0.5% 미만)이면 `proactive_suggestions`에 dictionary 보강 / seed 추가 / target split 추천을 등록한다.

### P2 — Kernforge 진짜 해자

9. **IOCTL grammar DSL**
   - YAML 또는 JSON 한 포맷으로 device path, IOCTL code, METHOD_BUFFERED/IN_DIRECT/OUT_DIRECT/NEITHER, 입출력 버퍼 필드 스키마, handle lifecycle, privilege requirement를 표현한다.
   - source-scan이 dispatch table을 보고 grammar 초안을 emit하고, 사용자가 검토한 뒤 `/fuzz-driver run`이 실행한다.
   - VM snapshot/revert 훅과 bugcheck/WER 캡처는 별도 P2지만 grammar 자체가 우선이다.

10. **Static <-> Fuzz 양방향 루프**
    - Static finding(root cause pattern hit, security overlay)에서 함수 시그니처를 추출해 `/fuzz-func --from-finding <id>`로 자동 생성한다.
    - Crash fingerprint(파일:라인, sink kind)에서 Semgrep/CodeQL 초안 query를 emit한다.
    - 이미 `root_cause_patterns` 인프라가 있으므로 연결만 하면 된다.

11. **Long-running fuzz daemon + PR gate**
    - `kernforge fuzz-daemon` 백그라운드: campaign queue, workers, checkpoint, coverage tail event를 `.kernforge/events/<session>.jsonl`로 emit한다.
    - PR gate는 regression corpus replay + 짧은 smoke만 통과 조건으로 둔다.

## 4. 실제로 손댈 코드 지점

| 변경 | 위치 |
| --- | --- |
| Dictionary emit | [`commands_fuzz_func.go:9989`](cmd/kernforge/commands_fuzz_func.go:9989) 근처에 `functionFuzzWriteDictionary`, [`commands_fuzz_func.go:10018`](cmd/kernforge/commands_fuzz_func.go:10018) 호출부 뒤 |
| Boundary corpus | [`commands_fuzz_func.go:10018`](cmd/kernforge/commands_fuzz_func.go:10018) `functionFuzzWriteSeedCorpus` 교체 |
| Run profile 분기 | [`commands_fuzz_func.go:9989`](cmd/kernforge/commands_fuzz_func.go:9989) `functionFuzzRunArgs` |
| Sanitizer parser | 새 파일 `cmd/kernforge/commands_fuzz_func_log_parser.go` + [`fuzz_campaign.go:510`](cmd/kernforge/fuzz_campaign.go:510) finding 정규화 호출 |
| 엔진 style 추가 | [`commands_fuzz_func.go:9818`](cmd/kernforge/commands_fuzz_func.go:9818) `functionFuzzBuildExecutionArgs` 분기 |
| `/fuzz-crash`, `/fuzz-corpus` 등록 | [`main.go:6334`](cmd/kernforge/main.go:6334) 인근 case 추가 + [`completion.go:1251`](cmd/kernforge/completion.go:1251) 라벨 |
| MCP 노출 | [`mcp_server.go:633`](cmd/kernforge/mcp_server.go:633) `kernforge_fuzz_func` 옆에 새 tool |
| Profile 필드 | [`commands_fuzz_func.go:200`](cmd/kernforge/commands_fuzz_func.go:200) `FunctionFuzzExecution` 구조체 + [`commands_fuzz_func.go:505`](cmd/kernforge/commands_fuzz_func.go:505) `normalizeFunctionFuzzExecution` |

## 5. 권장 첫 한 입

지금 코드에서 가장 즉시 효과가 큰 한 묶음:

1. P0-1 source-derived dictionary
2. P0-2 deterministic boundary corpus
3. P0-3 `--profile smoke|extended|repro|minimize` + `/fuzz-func minimize|repro` 서브커맨드

이 셋만 들어가도 같은 harness가 같은 시간에 잡는 crash가 체감 가능하게 늘고, minimize/repro 흐름이 명령 한 줄로 닫힌다. AFL++/WinAFL/IOCTL grammar는 그 다음 단계로 가는 것이 자연스럽다.

## 6. 성공 조건

1. `/fuzz-func`가 dict + boundary corpus를 결정적으로 떨어뜨리고, dict 변화가 corpus manifest provenance에 남는다.
2. `/fuzz-func continue <id> --profile extended`가 background job으로 10분 이상 안정적으로 돌고, plateau 시 proactive suggestion이 뜬다.
3. crash가 발생하면 sanitizer fingerprint(top-3 frame, source:line, kind)가 campaign finding으로 자동 dedup되고, `/fuzz-func minimize <crash>`가 minimized input을 corpus와 evidence에 남긴다.
4. AFL++ 빌드 옵션이 없는 환경에서는 동작이 동일하고 기능만 조용히 비활성화된다.

## 7. 운영 주의점

- 모든 run은 binary hash, corpus hash, sanitizer/verifier 옵션, dictionary hash를 manifest에 남겨야 재현 가능하다.
- corpus는 보안 민감 데이터일 수 있다. import provenance와 sensitive flag가 없으면 export 정책을 자동 차단한다.
- Windows driver fuzzing은 격리 환경이 전제다. P2 IOCTL grammar 작업은 isolated VM 강제와 reboot/crash budget 가드를 함께 설계한다.
- CI 게이트는 항상 smoke + regression replay에 한정한다. 장시간 discovery fuzzing은 daemon 또는 별도 worker로 분리한다.
