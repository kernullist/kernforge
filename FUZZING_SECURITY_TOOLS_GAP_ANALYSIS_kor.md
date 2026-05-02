# Kernforge Codex CLI/App, Fuzzing 및 보안 도구 비교 강화 계획

작성 기준일: 2026-05-02

이 문서는 Kernforge를 Codex CLI/App의 로컬 코딩 에이전트 하네스와, 주요 보안/퍼징 도구와 비교했을 때 더 강화해야 할 부분을 정리한 조사 결과다. Cloud task delegation은 비교 범위에서 제외하고, 로컬 CLI/App, App Server식 에이전트 루프, worktree/skill/automation, fuzzing campaign, 보안 분석 증거 흐름만 다룬다. 결론부터 말하면 Kernforge는 fuzz target 발굴, harness 생성, 증거/검증 라이프사이클, Windows 보안 엔지니어링 특화 문맥에서는 강점이 있지만, fuzzing 엔진 자체의 실행/스케줄링/코퍼스/크래시/커버리지 운영 능력은 libFuzzer, AFL++, WinAFL, syzkaller, OSS-Fuzz 계열과 비교해 아직 보강 여지가 크다.

## 1. 핵심 결론

1. Kernforge는 fuzzing 엔진을 직접 재구현하기보다, 검증된 엔진을 조율하는 security engineering harness가 되는 편이 맞다.
   - libFuzzer, AFL++, WinAFL, syzkaller, boofuzz 같은 엔진은 입력 변이, 커버리지 피드백, 크래시 탐색의 핵심 루프에 이미 강점이 있다.
   - Kernforge는 target discovery, harness generation, campaign planning, crash triage, Windows-specific evidence capture, verification gate를 담당하는 구조가 더 현실적이다.

2. 가장 먼저 강화해야 할 영역은 실제 fuzz run orchestration이다.
   - 현재 Kernforge는 fuzz 대상 분석, harness artifact, campaign manifest, seed/corpus/crash/coverage 디렉터리, native result capture, sanitizer/verifier/crash dump artifact 저장 같은 기반이 있다.
   - 하지만 경쟁 도구처럼 장시간 실행, 엔진별 옵션, 병렬 job, corpus merge/minimize, crash repro/minimize, coverage trend를 한 흐름으로 운영하는 기능은 더 필요하다.

3. Windows anti-cheat/security 관점에서는 WinAFL, Driver Verifier, WER dump, ETW, cdb/WinDbg, IOCTL grammar, VM snapshot/reboot loop를 엮는 기능이 차별점이 될 수 있다.
   - 일반 fuzzing 도구들이 Linux/CI 중심으로 강한 반면, Kernforge는 Windows user/kernel, driver IOCTL, protected process, telemetry, Unreal Engine game security 문맥을 first-class로 만들 수 있다.

4. static analysis와 fuzzing을 연결해야 실전 가치가 커진다.
   - Semgrep/CodeQL/Nuclei/ZAP 같은 도구는 source/sink, pattern, API/protocol surface를 잘 찾는다.
   - Kernforge는 static finding을 fuzz target으로 승격하고, fuzz crash를 Semgrep/CodeQL variant query로 되돌리는 양방향 루프를 만들 수 있다.

5. Codex CLI/App와 비교하면 Kernforge의 방향은 "범용 코딩 에이전트"를 다시 만드는 것이 아니라 Windows security/fuzzing에 특화된 로컬 control plane을 만드는 것이다.
   - Codex CLI는 로컬 터미널에서 repo를 읽고 수정하고 명령을 실행하는 범용 coding agent다.
   - Codex App/App Server 계열은 thread/turn/item lifecycle, approval, diff, streaming progress, multi-surface integration에 강하다.
   - Kernforge는 이 하네스 모델을 참고하되, fuzzing/evidence/failure recovery/Windows driver 검증을 first-class primitive로 올리는 편이 차별화된다.

## 2. Codex CLI/App 및 코딩 에이전트 하네스 비교

범위: Codex Cloud task delegation은 제외한다. 비교 대상은 로컬 Codex CLI, Codex App의 로컬 agent 운영, App Server식 harness protocol, skills/automations/worktree/approval 모델이다.

| 영역 | Codex CLI/App 강점 | 현재 Kernforge 강점 | Kernforge 강화 방향 |
| --- | --- | --- | --- |
| 로컬 repo 작업 루프 | 로컬 디렉터리에서 파일 읽기/수정/명령 실행, approval/sandbox 정책, Windows/macOS/Linux CLI 지원 | security workflow 명령, `/verify`, evidence, memory, fuzzing artifact가 domain-specific로 연결됨 | shell/edit/tool event를 thread/turn/item 수준으로 더 표준화하고, 실패/복구/검증 이벤트를 외부 클라이언트가 구독 가능한 JSONL stream으로 제공 |
| App/IDE 통합 하네스 | App Server가 item/turn/thread primitive, JSON-RPC/JSONL transport, approval request, diff/event streaming 제공 | CLI 내부 command와 session artifact는 풍부하지만 외부 UI protocol은 제한적 | `kernforge app-server` 또는 `kernforge event-stream`으로 dashboard, IDE, scheduler가 같은 session state를 consume하도록 확장 |
| Multi-agent/worktree | App은 여러 agent를 병렬 관리하고 worktree 기반 분리 작업을 UX에 통합 | `/worktree list|enter|attach`, specialist worktree lease, handoff import 기반이 있음 | worker별 ownership, conflict detector, worktree result merge, reviewer validation, fuzz campaign worker sharding을 하나의 scheduler로 묶기 |
| Skills/반복 workflow | skill을 app/CLI/IDE에서 재사용하고 automation에 결합 | feature guide, playbook, command-specific artifacts가 많음 | security skill pack을 repo-local로 formalize: driver fuzz, ETW capture, crash triage, CodeQL/Semgrep bridge, Unreal security review |
| 장기 작업 continuity | thread persistence, resumable UI, long-horizon task examples, automations | `/continuity`, `/recover`, `/completion-audit`, `/jobs`가 로컬 session artifact를 남김 | long-running job supervisor를 더 엄격하게 만들어 stale job, failed verification, incomplete artifacts를 final response 전에 hard gate 처리 |
| 검증 evidence | 터미널 로그, test output, diff 중심 evidence | verification planner, coding harness, fuzz evidence regression, artifact refs | test/fuzz/static/dynamic evidence를 공통 schema로 정규화하고 "claim -> evidence -> verifier -> residual risk" 추적을 강화 |
| 보안 특화 | sandbox/approval/rules 중심의 agent safety | Windows driver, telemetry, memory scan, anti-cheat, fuzzing 문맥 | Codex형 harness 위에 Windows security primitive를 얹는 방향: Driver Verifier, WER/dump, ETW, cdb, IOCTL grammar, VM snapshot |

권장 아키텍처:

1. 로컬 agent harness core
   - `Thread`, `Turn`, `Item`, `ToolCall`, `Approval`, `Diff`, `Artifact`, `Verification`, `Finding`을 명시적 event type으로 둔다.
   - CLI 출력만이 아니라 `.kernforge/events/*.jsonl` 또는 local app-server stream을 canonical log로 사용한다.

2. Security/fuzzing control plane
   - Codex CLI/App이 일반 coding task에 강한 만큼, Kernforge는 `/fuzz-*`, `/verify`, `/recover`, `/completion-audit`, `/evidence-*`를 deeper primitive로 삼는다.
   - fuzzing job은 일반 shell job이 아니라 campaign/run/corpus/crash/coverage/finding lifecycle을 갖는 first-class job이어야 한다.

3. Multi-agent ownership model
   - 각 agent/worktree는 owned path, allowed command, expected artifact, required verification, reviewer gate를 갖는다.
   - result import는 단순 markdown append가 아니라 task graph, artifact refs, changed files, verification summary를 merge해야 한다.

4. Finalization gate
   - Codex식 "테스트 실행 후 요약"을 넘어서, Kernforge는 final answer 전에 `/completion-audit`가 `ready=true`일 때만 완료 선언을 허용하는 정책을 가져야 한다.
   - warning 상태도 completion이 아니라 `needs_review`로 유지하는 현재 방향이 맞다.

## 3. 현재 Kernforge fuzzing 기반

현재 코드/문서 기준으로 Kernforge에는 다음 성격의 fuzzing 기반이 존재한다.

1. Fuzz target discovery
   - 함수 단위 fuzz 대상 후보를 찾고, 위험도/입력 surface/검증 가능성 관점으로 campaign 후보를 만들 수 있다.

2. Harness generation
   - `/fuzz-func`, `/fuzz-campaign` 계열 흐름을 통해 함수 기반 harness와 campaign artifact를 만들 수 있다.

3. Source-only fuzz planning
   - 실제 엔진 실행 전에도 소스 분석만으로 fuzz plan, seed/corpus/crash/coverage 경로, manifest를 생성할 수 있다.

4. Evidence lifecycle
   - crash, coverage, finding, verifier, sanitizer, dump artifact를 investigation 및 verification planner와 연결할 수 있는 구조가 있다.

5. Security-engineering context
   - Windows driver, telemetry, memory scan, evidence severity, live investigation 같은 주변 문서/흐름이 이미 존재한다.

이 기반은 일반 fuzzing 엔진이 약한 "무엇을 왜 fuzz할 것인가", "나온 결과를 어떻게 보안 finding으로 검증할 것인가"에 가깝다. 반대로 "어떻게 오래, 빠르게, 재현 가능하게 fuzz할 것인가"는 보강 대상이다.

## 4. 도구별 비교

| 도구 | 강점 | Kernforge 대비 차이 | Kernforge 강화 방향 |
| --- | --- | --- | --- |
| Codex CLI/App | 로컬 repo edit/run/test loop, App Server식 harness, worktree/skills/automation UX | 범용 coding agent로 강하지만 security/fuzzing lifecycle은 domain-specific하지 않음 | Kernforge event stream/app-server, Windows security skill pack, fuzzing/evidence primitive 강화 |
| libFuzzer | in-process coverage-guided fuzzing, sanitizer 연동, corpus merge/minimize | 엔진 루프와 corpus 최적화가 강함 | `/fuzz-run --engine libfuzzer`, sanitizer preset, corpus merge/minimize wrapper |
| AFL++ | forkserver, persistent mode, instrumentation 옵션, 대규모 corpus 운영 | 장시간 campaign과 변이 전략이 강함 | AFL++ build/run profile, dictionary extraction, parallel workers |
| WinAFL | Windows binary/API fuzzing, DynamoRIO 기반 coverage | Windows user-mode binary fuzzing에 특화 | cdb/WER/ETW capture, target launcher, timeout/hang classification |
| syzkaller | kernel syscall fuzzing, reproducer/minimization, dashboard | kernel fuzzing 운영 모델이 강함 | Windows driver IOCTL fuzzing DSL, VM snapshot/reboot loop, verifier capture |
| OSS-Fuzz / ClusterFuzzLite | CI/continuous fuzzing, regression detection, crash lifecycle | 운영/자동화/회귀 방지 체계가 강함 | local-first continuous fuzzing daemon, PR gate, artifact dashboard |
| OneFuzz | distributed fuzzing orchestration, job/task model | 대규모 job 운영 모델이 강함 | local job graph, resumable campaigns, future cloud backend adapter |
| boofuzz | stateful protocol fuzzing, session graph | protocol/API fuzzing 모델이 강함 | `/fuzz-protocol`, OpenAPI/PCAP/state machine import |
| CI Fuzz / Jazzer | developer-friendly fuzz test creation, JVM/JS/C/C++ fuzz tests, line-level finding and coverage workflow | unit-test-like fuzzing UX와 IDE/CI 흐름이 강함 | `/fuzz-test scaffold`, language-specific fuzz target template, regression corpus replay |
| ZAP | web/API dynamic scan, active/passive scan | 웹 보안 scanning coverage가 강함 | finding import, API fuzz seed generation, auth/session replay |
| Nuclei | template 기반 빠른 exposure scan | template ecosystem과 operational scan이 강함 | template finding import, security checklist/variant scan 연결 |
| CodeQL | semantic code query, variant analysis | static dataflow와 variant search가 강함 | fuzz finding -> CodeQL query draft, static sink -> fuzz target |
| Semgrep | 빠른 pattern/static rule authoring | lightweight rule loop가 강함 | fuzz crash pattern -> Semgrep rule draft, repo-wide variant triage |
| SonarQube | code quality와 taint-style security rules를 개발 workflow에 통합 | developer-facing quality gate 운영이 강함 | Kernforge completion audit와 PR/report gate에 security quality state 연결 |

## 5. 주요 격차

### 5.1 실제 fuzz engine orchestration

필요 기능:

1. 엔진별 실행 프로파일
   - `libfuzzer`
   - `afl++`
   - `winafl`
   - `honggfuzz`
   - 장기적으로 `syzkaller`/driver IOCTL fuzzing profile

2. 공통 CLI
   - `/fuzz-run --engine libfuzzer --target <path> --campaign <name> --time 30m`
   - `/fuzz-run --engine afl++ --target <binary> --corpus <dir> --dict <file>`
   - `/fuzz-run --engine winafl --target <exe> --module <dll> --offset <rva>`

3. 실행 안정성
   - timeout/hang 감지
   - exit code 분류
   - sanitizer output capture
   - stdout/stderr rolling log
   - crash artifact 자동 수집
   - interrupted run resume

4. job model
   - local worker 수 지정
   - 병렬 campaign shard
   - run budget
   - schedule/daemon 연동
   - campaign manifest 업데이트

추천 구현 우선순위:

1. P0: `/fuzz-run --dry-run`으로 엔진별 command plan과 artifact manifest 생성
2. P0: libFuzzer 실제 실행 wrapper
3. P1: AFL++ 실행 wrapper
4. P1: WinAFL 실행 wrapper
5. P2: syzkaller/driver fuzzing adapter

### 5.2 Corpus lifecycle

경쟁 fuzzing 도구에서 중요한 것은 단순히 crash를 찾는 것이 아니라, 좋은 corpus를 계속 관리하는 것이다.

필요 기능:

1. Corpus import
   - 기존 seed corpus를 campaign으로 가져오기
   - 파일 hash, size, source, timestamp 기록
   - sensitive sample flag

2. Corpus dedup
   - 동일 hash 제거
   - size/type 기준 grouping
   - coverage-preserving dedup은 엔진별 도구 사용

3. Corpus minimize
   - libFuzzer `-merge=1`
   - AFL++ `afl-cmin`, `afl-tmin`
   - WinAFL corpus minimization wrapper

4. Corpus replay
   - 특정 corpus entry를 harness에 재주입
   - crash 재현 여부, 실행 시간, coverage delta 기록

권장 CLI:

```text
/fuzz-corpus list --campaign <name>
/fuzz-corpus import --campaign <name> --from <dir> --source manual-seed
/fuzz-corpus dedup --campaign <name>
/fuzz-corpus minimize --campaign <name> --engine libfuzzer
/fuzz-corpus replay --campaign <name> --input <file>
```

### 5.3 Crash triage 및 재현성

fuzzing 운영에서 가장 큰 비용은 crash가 나온 뒤부터다. 단순히 crash file을 저장하는 것만으로는 부족하고, crash가 재현 가능한지, 같은 버그인지, exploitability가 어느 정도인지, 수정 후 회귀 테스트가 가능한지까지 이어져야 한다.

필요 기능:

1. Crash inventory
   - campaign별 crash 목록
   - exception code
   - signal
   - stack fingerprint
   - crashing input hash
   - first seen / last seen
   - repro count

2. Crash fingerprinting
   - sanitizer stack top N frame
   - Windows exception code + module + offset
   - access violation read/write/execute 분류
   - kernel dump bucket
   - source location이 있으면 file:line 포함

3. Crash repro
   - deterministic repro command 생성
   - environment variable snapshot
   - sanitizer/verifier/cdb 옵션 포함
   - timeout/hang 재현 정책 분리

4. Crash minimization
   - libFuzzer `-minimize_crash=1`
   - AFL++ `afl-tmin`
   - Windows binary target의 경우 replay harness 또는 WinAFL minimization profile

5. Finding promotion
   - crash를 investigation finding으로 승격
   - severity 후보 산정
   - variant scan task 생성
   - regression corpus로 자동 편입

권장 CLI:

```text
/fuzz-crash list --campaign <name>
/fuzz-crash repro --campaign <name> --crash <id>
/fuzz-crash minimize --campaign <name> --crash <id>
/fuzz-crash bucket --campaign <name>
/fuzz-crash promote --campaign <name> --crash <id>
```

### 5.4 Coverage feedback loop

현재 Kernforge는 coverage artifact와 gap을 다룰 기반이 있지만, fuzz campaign 운영 관점에서는 시간에 따른 추세와 plateau 판정이 필요하다.

필요 기능:

1. Coverage trend
   - line/function/edge coverage 변화량
   - 시간당 new coverage
   - corpus size 대비 coverage 효율

2. Target ranking
   - high-risk uncovered function
   - parser/deserializer/protocol boundary
   - memory unsafe operation 근처
   - previous finding 주변

3. Plateau handling
   - 일정 시간 coverage 증가가 없으면 dictionary 추천
   - seed 추가 추천
   - target split 추천
   - 다른 engine profile 추천

4. Dashboard
   - campaign별 coverage timeline
   - crash bucket table
   - recent new paths
   - flaky crash
   - regression corpus 상태

권장 CLI:

```text
/fuzz-coverage report --campaign <name>
/fuzz-coverage gaps --campaign <name>
/fuzz-coverage rank-targets --campaign <name>
/fuzz-coverage dashboard --campaign <name>
```

### 5.5 Windows driver 및 anti-cheat fuzzing 특화

Kernforge가 일반 fuzzing 도구와 가장 크게 차별화할 수 있는 영역이다.

필요 기능:

1. IOCTL grammar DSL
   - device path
   - IOCTL code
   - METHOD_BUFFERED / METHOD_IN_DIRECT / METHOD_OUT_DIRECT / METHOD_NEITHER
   - input/output buffer layout
   - handle lifecycle
   - privilege requirement

2. Driver fuzz run profile
   - 관리자 권한 확인
   - test signing / driver loading state 확인
   - Driver Verifier 설정 capture
   - target service start/stop
   - VM snapshot/revert hook

3. Crash capture
   - kernel dump path
   - WER report
   - event log
   - ETW session
   - verifier violation code
   - bugcheck code and parameters

4. Replay
   - crash input replay executable
   - IOCTL sequence repro script
   - cdb/WinDbg command script
   - reboot-required 상태 기록

5. Operational safety
   - isolated VM 강제 권장
   - host protection check
   - reboot loop guard
   - max crash/reboot budget

권장 CLI:

```text
/fuzz-driver plan --device \\\\.\\DeviceName --ioctl 0x222003
/fuzz-driver run --campaign <name> --vm-profile <profile>
/fuzz-driver repro --campaign <name> --crash <id>
/fuzz-driver collect-dump --campaign <name>
```

### 5.6 Protocol/API fuzzing

boofuzz, ZAP, Nuclei 계열과 비교하면 Kernforge는 protocol/API surface를 fuzz campaign으로 바꾸는 기능이 부족하다.

필요 기능:

1. OpenAPI import
   - endpoint/method/schema 기반 seed 생성
   - auth/session profile
   - rate limit budget
   - stateful sequence 후보

2. Protocol state machine
   - handshake
   - auth
   - command sequence
   - reset/reconnect
   - health check

3. Server lifecycle
   - target process start/stop
   - health probe
   - crash/hang/restart 감지
   - log capture

4. Security scanner bridge
   - ZAP finding -> fuzz seed
   - Nuclei template hit -> deeper fuzz target
   - API error response clustering

권장 CLI:

```text
/fuzz-api import-openapi --file openapi.json --campaign <name>
/fuzz-api run --campaign <name> --auth-profile <name>
/fuzz-protocol plan --pcap sample.pcap --campaign <name>
/fuzz-protocol run --campaign <name>
```

### 5.7 Static analysis와 fuzzing의 양방향 연결

CodeQL/Semgrep는 static variant search에 강하고, fuzzing은 runtime proof에 강하다. Kernforge가 둘을 연결하면 finding quality가 올라간다.

필요 기능:

1. Static finding -> fuzz target
   - sink 주변 함수 추출
   - parser/deserializer entrypoint 추정
   - dangerous API argument constraints 추론
   - harness input schema 초안 생성

2. Fuzz crash -> static variant scan
   - crash stack 기반 pattern 추출
   - nearby unsafe operation query 생성
   - similar source/sink search
   - Semgrep rule draft
   - CodeQL query draft

3. Evidence merge
   - static trace
   - fuzz repro
   - crash dump
   - minimized input
   - regression test
   - fix verification

권장 CLI:

```text
/static-to-fuzz --finding <id> --campaign <name>
/fuzz-to-semgrep --crash <id>
/fuzz-to-codeql --crash <id>
/variant-scan --from-crash <id>
```

### 5.8 Agent harness protocol 및 UX

Codex App Server의 핵심 교훈은 agent 작업을 단순 stdout으로 보지 않고, lifecycle이 있는 event stream으로 다루는 것이다. Kernforge도 같은 수준의 machine-readable loop가 필요하다.

필요 기능:

1. Event stream
   - `thread.started`
   - `turn.started`
   - `item.tool.started`
   - `item.tool.completed`
   - `item.diff.created`
   - `item.approval.requested`
   - `verification.completed`
   - `fuzz.crash.discovered`
   - `completion_audit.completed`
   - 현재 Kernforge 반영: `/events tail [n]`과 `/events export [path]`가 session conversation event를 JSONL record로 출력/저장한다.

2. Client protocol
   - JSONL over stdio 우선
   - 이후 named pipe/WebSocket adapter
   - CLI, dashboard, IDE extension, scheduler가 같은 protocol을 사용

3. Approval/rule model
   - command prefix allow/deny
   - network, admin, driver load, reboot, debugger attach 같은 risk class 분리
   - Windows security workflow는 일반 shell command보다 더 엄격한 policy 적용

4. Durable artifacts
   - 모든 event는 artifact ref와 연결
   - final response는 event log와 artifact graph로 검증 가능해야 함

권장 CLI:

```text
/app-server start --stdio
/events tail --session current
/rules list
/rules allow --command-prefix "go test"
/rules require-confirm --risk driver-load
```

## 6. 추천 구현 로드맵

### P0: 바로 구현할 가치가 큰 항목

1. Agent event stream
   - `.kernforge/events/<session>.jsonl`
   - thread/turn/item/tool/diff/verification/fuzz/completion-audit event schema
   - `/events tail`

2. `/fuzz-run`
   - 엔진별 실행 계획 생성
   - `--dry-run` 지원
   - libFuzzer 실제 실행 wrapper
   - campaign manifest에 run result 기록

3. `/fuzz-corpus`
   - `list`
   - `import`
   - `dedup`
   - `manifest`
   - 최소한 hash/size/source/provenance 기록

4. `/fuzz-crash`
   - `list`
   - `repro`
   - `bucket`
   - libFuzzer crash minimization command 생성

5. `/fuzz-coverage report`
   - campaign별 coverage artifact 요약
   - gap list
   - target ranking 초안

### P1: 운영 품질을 올리는 항목

1. Local app-server protocol
   - JSONL over stdio
   - thread/turn/item lifecycle
   - command approval events
   - diff/artifact streaming

2. AFL++ profile
   - build command template
   - `afl-fuzz` execution
   - `afl-cmin`/`afl-tmin` wrapper

3. WinAFL profile
   - target/module/offset config
   - DynamoRIO path validation
   - cdb/WER/ETW capture

4. Crash promotion
   - crash -> finding
   - finding -> task graph
   - regression corpus 자동 등록

5. Dashboard integration
   - session dashboard에 fuzz campaign 상태 추가
   - crash bucket table
   - coverage trend

### P2: Kernforge 차별화 항목

1. Driver IOCTL fuzzing DSL
2. VM snapshot/reboot orchestration
3. Protocol/API stateful fuzzing
4. Static-to-fuzz and fuzz-to-static rule generation
5. Continuous fuzz daemon with PR gate
6. Security skill pack and automation templates

## 7. 제안 artifact schema

### Campaign run result

```json
{
  "campaign": "parser-fuzz",
  "engine": "libfuzzer",
  "target": "build/parser_fuzzer.exe",
  "started_at": "2026-05-02T00:00:00+09:00",
  "ended_at": "2026-05-02T00:30:00+09:00",
  "duration_seconds": 1800,
  "exit_code": 0,
  "status": "completed",
  "corpus_dir": ".kernforge/fuzz/parser-fuzz/corpus",
  "crash_dir": ".kernforge/fuzz/parser-fuzz/crashes",
  "coverage_dir": ".kernforge/fuzz/parser-fuzz/coverage",
  "new_crashes": 0,
  "new_corpus_entries": 42,
  "coverage_summary": {
    "functions": 120,
    "covered_functions": 84
  },
  "logs": [
    ".kernforge/fuzz/parser-fuzz/runs/20260502-000000/stdout.log",
    ".kernforge/fuzz/parser-fuzz/runs/20260502-000000/stderr.log"
  ]
}
```

### Crash record

```json
{
  "id": "crash-20260502-000001",
  "campaign": "parser-fuzz",
  "engine": "libfuzzer",
  "input": ".kernforge/fuzz/parser-fuzz/crashes/crash-abc123",
  "sha256": "abc123",
  "first_seen": "2026-05-02T00:12:31+09:00",
  "repro_status": "reproducible",
  "fingerprint": {
    "type": "asan",
    "summary": "heap-buffer-overflow in ParseHeader",
    "top_frame": "ParseHeader",
    "source": "src/parser.cpp:142"
  },
  "artifacts": [
    ".kernforge/fuzz/parser-fuzz/crashes/crash-abc123.log"
  ]
}
```

### Corpus entry

```json
{
  "sha256": "def456",
  "path": ".kernforge/fuzz/parser-fuzz/corpus/def456",
  "size": 128,
  "source": "manual-seed",
  "imported_at": "2026-05-02T00:02:00+09:00",
  "tags": [
    "header",
    "minimal"
  ]
}
```

### Agent event

```json
{
  "id": "evt-20260502-000001",
  "session_id": "session-abc",
  "thread_id": "thread-main",
  "turn_id": "turn-42",
  "kind": "fuzz.crash.discovered",
  "created_at": "2026-05-02T00:12:31+09:00",
  "severity": "warning",
  "summary": "libFuzzer crash discovered in parser-fuzz",
  "artifact_refs": [
    ".kernforge/fuzz/parser-fuzz/crashes/crash-abc123",
    ".kernforge/fuzz/parser-fuzz/crashes/crash-abc123.log"
  ],
  "entities": {
    "campaign": "parser-fuzz",
    "engine": "libfuzzer",
    "crash_id": "crash-20260502-000001"
  }
}
```

## 8. 운영상 주의점

1. Fuzzing은 결과가 재현 가능해야 의미가 있다.
   - run command, environment, binary hash, corpus hash, sanitizer/verifier option을 반드시 저장해야 한다.

2. Crash artifact는 finding이 아니다.
   - crash는 후보 증거고, repro/minimize/root-cause/impact 분석을 거쳐 finding으로 승격해야 한다.

3. Windows driver fuzzing은 격리 환경이 전제다.
   - Driver Verifier, kernel dump, reboot loop, VM snapshot이 없으면 운영 비용이 급격히 증가한다.

4. Corpus는 보안 민감 데이터가 될 수 있다.
   - seed/import provenance, sensitive flag, export policy가 필요하다.

5. CI fuzzing은 짧고 deterministic해야 한다.
   - PR gate는 smoke fuzz와 regression replay 중심으로 구성하고, 장시간 discovery fuzzing은 daemon 또는 별도 worker로 분리하는 편이 안정적이다.

## 9. 추천 최종 방향

Kernforge의 fuzzing 방향은 다음 구조가 가장 현실적이다.

1. Codex CLI/App 수준의 agent harness primitive를 로컬에 갖춘다.
   - event stream
   - approval/rule model
   - thread/turn/item lifecycle
   - worktree/worker ownership
   - resumable artifacts

2. Fuzz engine은 검증된 외부 엔진을 사용한다.
   - libFuzzer, AFL++, WinAFL, boofuzz, syzkaller 계열을 backend로 둔다.

3. Kernforge는 campaign control plane이 된다.
   - target discovery
   - harness generation
   - engine profile
   - corpus lifecycle
   - crash triage
   - coverage feedback
   - static/fuzz bridge
   - evidence/finding/verification lifecycle

4. Windows security specialization을 first-class로 만든다.
   - IOCTL fuzzing
   - Driver Verifier
   - ETW/WER/dump capture
   - cdb/WinDbg repro
   - VM snapshot/reboot loop
   - anti-cheat telemetry surface 분석

5. 모든 결과는 session, task graph, artifact graph, dashboard에 연결한다.
   - 사람이 보고 판단할 수 있는 증거 흐름이 Kernforge의 차별점이다.

## 10. 참고 자료

- OpenAI Codex CLI documentation: https://developers.openai.com/codex/cli
- OpenAI Codex App introduction: https://openai.com/index/introducing-the-codex-app/
- OpenAI Codex App Server harness article: https://openai.com/index/unlocking-the-codex-harness/
- OpenAI Codex use cases: https://developers.openai.com/codex/use-cases/
- LLVM libFuzzer documentation: https://llvm.org/docs/LibFuzzer.html
- AFL++ documentation: https://aflplus.plus/docs/
- WinAFL repository: https://github.com/googleprojectzero/winafl
- syzkaller documentation: https://github.com/google/syzkaller/tree/master/docs
- OSS-Fuzz documentation: https://google.github.io/oss-fuzz/
- ClusterFuzzLite documentation: https://google.github.io/clusterfuzzlite/
- Microsoft OneFuzz repository: https://github.com/microsoft/onefuzz
- boofuzz documentation: https://boofuzz.readthedocs.io/
- CI Fuzz documentation: https://docs.code-intelligence.com/ci-fuzz
- Jazzer repository: https://github.com/CodeIntelligenceTesting/jazzer
- CodeQL documentation: https://codeql.github.com/docs/
- Semgrep documentation: https://semgrep.dev/docs/
- SonarQube security rules documentation: https://docs.sonarsource.com/sonarcloud/digging-deeper/security-related-rules
- Nuclei documentation: https://docs.projectdiscovery.io/tools/nuclei/overview
- OWASP ZAP documentation: https://www.zaproxy.org/docs/
