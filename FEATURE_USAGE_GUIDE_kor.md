# Kernforge 상세 사용 가이드

이 문서는 현재 Kernforge에 구현된 기능을 실제로 어떤 상황에서 어떻게 쓰면 좋은지, 그리고 각 명령이 어떤 흐름 안에서 가장 빛나는지를 설명하는 상세 운영 문서이다.

기준 시점:
- 코드베이스 기준: 2026-04-03

대상 사용자:
- Windows security 엔지니어
- anti-cheat 엔지니어
- kernel/user-mode telemetry 개발자
- driver/signing/symbol/package readiness 담당자
- Unreal Engine 보안/무결성 담당자

이 문서의 목적:
1. 기능 목록을 나열하는 것이 아니라 실제 사용 흐름을 설명한다.
2. 어떤 문제에서 어떤 명령 조합을 쓰면 좋은지 예시 중심으로 정리한다.
3. `analyze-project -> analyze-performance -> investigate -> simulate -> review/edit/plan -> verify -> evidence/memory/hooks` 루프를 자연스럽게 익히도록 돕는다.

## 1. Kernforge를 가장 잘 쓰는 관점

Kernforge는 단순히 "질문하고 답받는 코딩 CLI"로 써도 되지만, 현재 가장 강한 사용 방식은 먼저 재사용 가능한 프로젝트 지식을 만들고 그 위에서 나머지 루프를 돌리는 것이다.

1. 워크스페이스가 크거나 낯설면 `/analyze-project`를 먼저 실행한다.
2. 성능이나 startup path가 중요하면 `/analyze-performance`로 최신 knowledge pack을 performance lens로 바꾼다.
3. live 상태가 중요하면 `/investigate`로 현장 상태를 수집한다.
4. risk lens가 중요하면 `/simulate`로 tamper, visibility, forensic blind spot을 본다.
5. `/review-selection`, `/edit-selection`, `/do-plan-review`, `/new-feature`로 실제 작업을 진행한다.
6. `/verify`로 verification plan을 돌린다.
7. `/evidence-*`와 `/mem-*`로 상태와 맥락을 다시 확인한다.
8. push/PR 전에는 hooks가 마지막 방어선으로 동작한다.

핵심 해석:
1. `analyze-project`는 일회성 요약이 아니라 재사용 가능한 architecture map을 만든다.
2. `analyze-performance`는 최신 구조 지식에서 hot path와 bottleneck 가능성을 끌어낸다.
3. `investigate`는 실행 중 상태를 관찰한다.
4. `simulate`는 공격자 관점에서 약한 면을 드러낸다.
5. `verify`는 변경과 최근 상태를 바탕으로 검증 계획을 조립한다.
6. `evidence`는 결과를 증거 단위로 구조화한다.
7. `memory`는 세션을 넘어가는 장기 맥락을 저장한다.
8. `hooks`는 그 축적된 맥락을 다시 정책으로 바꾼다.

## 2. 현재 구현된 핵심 기능과 언제 쓰면 좋은가

### 입력과 취소 처리

목적:
1. Windows 콘솔에서도 입력 취소와 요청 취소를 분리해서 안정적으로 처리한다.
2. 짧게 누른 `Esc`를 진행 중 요청 취소로 놓치지 않게 한다.
3. 요청 취소 직후 남은 콘솔 입력 때문에 다음 프롬프트가 바로 취소되지 않게 한다.

실제 동작:
1. 입력 중 `Esc`는 현재 프롬프트 입력만 취소한다.
2. 모델 응답 대기 중 `Esc`는 진행 중 요청을 취소한다.
3. Windows에서는 async key state와 console input record를 함께 사용해 짧은 `Esc` 탭도 놓치지 않게 처리한다.
4. 요청 취소 뒤에는 `Esc` release를 잠깐 기다리고 pending console input을 정리한 뒤 다음 입력을 받는다.
5. assistant streaming은 선행 빈 chunk를 무시하고, progress 출력 전 경계를 정리하며, 반복 follow-on preamble을 별도 줄로 나눠 가독성을 높인다.

### 2.0 Project Analysis

목적:
1. 큰 워크스페이스의 구조를 재사용 가능한 문서로 만든다.
2. 여러 worker와 reviewer 패스로 분석을 분산한다.
3. 후속 작업용 `latest` knowledge pack과 performance lens를 유지한다.
4. incremental 모드에서는 바뀌지 않은 shard를 재사용한다.
5. structural index, Unreal semantic graph, vector corpus까지 후속 자동화에 재사용할 수 있게 남긴다.

대표 명령:
- `/analyze-project <goal>`
- `/analyze-performance [focus]`
- `/set-analysis-models`

특히 좋은 상황:
1. 큰 코드베이스에 처음 들어가서 즉석 요약으로는 부족할 때
2. startup, integrity, ETW, scanner, compression, memory, upload path를 같이 봐야 할 때
3. 이후 review와 verification이 안정적인 구조 지식을 공유해야 할 때
4. Unreal 5처럼 module, target, reflection, replication, asset/config coupling이 동시에 얽힌 코드베이스를 다뤄야 할 때

현재 project analysis가 추가로 남기는 핵심 산출물:
1. `snapshot`: 스캔 결과와 runtime/project edge를 담는 구조화된 입력
2. `structural index`: symbol, reference, build edge 중심의 정밀 인덱스
3. `unreal graph`: UE project/module/network/asset/system/config를 구조화한 semantic graph
4. `knowledge pack`: 사람이 읽는 architecture digest와 subsystem 요약
5. `vector corpus`: 임베딩 친화적인 project/subsystem/shard 문서 묶음
6. `vector ingest exports`: pgvector, sqlite, qdrant로 넘기기 쉬운 seed 파일

대규모/UE 프로젝트에서 특히 달라진 점:
1. semantic shard planner가 `startup`, `build_graph`, `unreal_network`, `unreal_ui`, `unreal_ability`, `asset_config`, `integrity_security`, `unreal_gameplay` 영역을 우선 분리한다.
2. worker와 reviewer prompt가 shard 목적에 맞는 semantic focus와 review checklist를 받는다.
3. incremental reuse가 file hash뿐 아니라 semantic fingerprint 변화까지 본다.
4. 결과 문서에는 subsystem별 invalidation reason, evidence, diff, top change class가 같이 남는다.
5. 저장 산출물에는 snapshot, structural index, Unreal semantic graph, vector corpus, ingestion seed 파일까지 포함되어 후속 retrieval 파이프라인에 재사용할 수 있다.
6. goal에 특정 디렉토리나 하위 영역이 드러나면 해당 경로 위주로 분석 shard를 좁힐 수 있다.
7. interactive 실행에서는 hidden directory나 external-looking directory를 분석 전에 제외할지 확인할 수 있다.

### 2.1 Hook Engine

목적:
1. 위험 작업 전에 경고하거나 차단한다.
2. verification 전에 추가 review context와 verification step을 주입한다.
3. 최근 evidence 상태를 보고 push/PR 정책을 더 강하게 만든다.
4. 필요하면 자동 checkpoint를 만든다.

대표 명령:
- `/hooks`
- `/hook-reload`
- `/init hooks`
- `/override`
- `/override-add <rule-id> <hours> <reason>`
- `/override-clear <override-id|rule-id|all>`

대표 액션:
- `warn`
- `ask`
- `deny`
- `append_context`
- `append_review_context`
- `add_verification_step`
- `create_checkpoint`

특히 좋은 상황:
1. signing/symbol/provider/XML 같은 반복 실수가 많은 팀
2. "PR 전에 최소한 이것만은 확인되어야 한다"는 정책이 있는 팀
3. 운영 영향이 큰 변경을 사람이 매번 기억에만 의존하면 안 되는 팀

추천 운영 방식:
1. 처음에는 `windows-security` preset만 켠다.
2. `warn`과 `ask` 위주로 적응한다.
3. 반복 사고가 나는 규칙만 `deny`로 올린다.
4. 너무 강한 규칙은 `/override-add`로 예외 흐름을 열되, reason과 expiry를 반드시 남긴다.

### 2.2 Security-Aware Verification

목적:
1. 변경 파일을 보고 보안 카테고리를 추론한다.
2. 카테고리에 맞는 verification step을 자동으로 추가한다.
3. 최근 simulation/investigation 결과까지 참고해 verification plan을 보강한다.

현재 인식하는 축:
1. `driver`
2. `telemetry`
3. `unreal`
4. `memory-scan`
5. 최근 high-risk simulation signal
6. active investigation / live finding

대표 명령:
- `/verify`
- `/verify --full`
- `/verify src/foo.cpp,driver/guard.cpp`
- `/verify-dashboard`
- `/verify-dashboard-html`
- `/set-auto-verify [on|off]`
- `/detect-verification-tools`
- `/set-msbuild-path <path>`
- `/set-cmake-path <path>`
- `/set-ctest-path <path>`
- `/set-ninja-path <path>`

좋은 상황:
1. 일반 `go test`, `msbuild`, `ctest`만으로는 부족한 작업
2. signing, symbols, package, provider, XML, verifier 상태까지 같이 봐야 하는 작업
3. 최근 investigation/simulation에서 이미 위험 신호가 나온 상태

운영 메모:
1. `auto_verify`는 편집 후 automatic verification 전체를 켜고 끄는 마스터 스위치다.
2. Windows에서 `msbuild`, `cmake`, `ctest`, `ninja`가 없으면 Kernforge가 automatic verification 비활성화 또는 실행 파일 경로 저장을 제안할 수 있다.
3. 공백이 있는 경로는 따옴표로 감싸는 편이 안전하다.
4. 예: `/set-msbuild-path "C:\Program Files\Microsoft Visual Studio\2022\Community\MSBuild\Current\Bin\MSBuild.exe"`
5. 모델 요청 timeout은 `request_timeout_seconds`로 조정할 수 있고, timeout 난 model turn은 한 번 자동 재시도한다.

### 2.3 Evidence Store

목적:
1. verification, override, investigation, simulation 결과를 evidence 단위로 저장한다.
2. 최근 failed/high-risk 상태를 빠르게 검색하고 대시보드로 볼 수 있게 한다.
3. hooks와 verification planner가 다시 참고할 수 있는 구조화된 근거를 만든다.

대표 명령:
- `/evidence`
- `/evidence-search <query>`
- `/evidence-show <id>`
- `/evidence-dashboard [query]`
- `/evidence-dashboard-html [query]`

현재 자주 보게 되는 evidence 종류:
1. `verification_category`
2. `verification_artifact`
3. `verification_failure`
4. `hook_override`
5. `investigation_session`
6. `investigation_snapshot`
7. `investigation_finding`
8. `simulation_run`
9. `simulation_finding`

### 2.4 Persistent Memory

목적:
1. 세션이 끝나도 중요한 판단과 결과를 다음 세션까지 유지한다.
2. 과거 verification/evidence 맥락을 다시 찾아볼 수 있게 한다.
3. 장기적 회귀나 반복 실패 패턴을 시간축으로 추적하는 기반이 된다.

대표 명령:
- `/mem`
- `/mem-search <query>`
- `/mem-show <id>`
- `/mem-dashboard [query]`
- `/mem-dashboard-html [query]`

특징:
1. 단순 메모가 아니라 verification category/tag/artifact/failure/severity/signal/risk를 같이 저장한다.
2. evidence보다 더 긴 시간축의 판단과 작업 맥락을 제공한다.

### 2.5 Live Investigation Mode

목적:
1. 실행 중인 Windows 상태를 스냅샷으로 수집한다.
2. live 상태에서 보인 finding을 evidence와 memory에 남긴다.
3. 이후 simulation, verification, review 흐름의 입력으로 쓴다.

대표 명령:
- `/investigate`
- `/investigate start <preset> [target]`
- `/investigate snapshot [target]`
- `/investigate note <text>`
- `/investigate stop [summary]`
- `/investigate list`
- `/investigate show <id>`
- `/investigate dashboard`
- `/investigate dashboard-html`

현재 preset:
1. `driver-visibility`
2. `process-visibility`
3. `provider-visibility`

좋은 상황:
1. 코드 수정 전에 현재 로딩 상태, verifier 상태, provider 상태를 먼저 보고 싶은 경우
2. "재현은 되는데 왜 그런지 live 상태가 필요하다"는 경우
3. 단순 정적 코드 리뷰보다 현장 관찰이 중요한 경우
4. 깊은 원인 분석 전에 가시성 triage snapshot을 남기고 싶은 경우

중요한 범위 제한:
1. `driver-visibility`는 드라이버 로드 실패 root cause를 깊게 분석하는 preset이 아니다.
2. 현재 구현은 사용자 모드에서 보이는 driver/service/filter/verifier 상태와 workspace artifact 존재 여부를 빠르게 남기는 데 초점이 있다.
3. `process-visibility`는 attach나 protection 분석기가 아니라 process listing 기반 triage snapshot이다.
4. `provider-visibility`는 ETW/provider registration root cause 분석기가 아니라 provider listing 기반 triage snapshot이다.

### 2.6 Adversarial Simulation Profiles

목적:
1. recent failed evidence와 investigation 결과를 바탕으로 공격자 관점 리스크를 평가한다.
2. tamper, visibility, forensic blind spot 관점에서 약한 면을 드러낸다.
3. review, edit, plan-review, verify에 heuristic risk context를 다시 주입한다.

대표 명령:
- `/simulate`
- `/simulate tamper-surface [target]`
- `/simulate stealth-surface [target]`
- `/simulate forensic-blind-spot [target]`
- `/simulate list`
- `/simulate show <id>`
- `/simulate dashboard`
- `/simulate dashboard-html`

현재 profile:
1. `tamper-surface`
2. `stealth-surface`
3. `forensic-blind-spot`

좋은 상황:
1. integrity, signing, registration risk가 걱정되는 driver/anti-cheat 작업
2. observer coverage, telemetry visibility가 약할 수 있는 telemetry 작업
3. forensic artifact가 부족해 사후 분석이 힘들 수 있는 작업

중요한 범위 제한:
1. simulation은 실제 공격 재현이나 exploitability 증명이 아니라 heuristic risk review다.
2. `tamper-surface`, `stealth-surface`, `forensic-blind-spot`은 offensive capability가 아니라 해석 프레임 이름이다.

### 2.7 Selection-First Review / Edit

목적:
1. 전체 파일이 아니라 선택한 코드 범위만 집중 리뷰하거나 수정한다.
2. recent simulation finding이 선택 영역과 맞닿으면 자동으로 review/edit prompt에 주입한다.
3. 더 넓은 리뷰나 수정 전에 workspace/selection diff를 richer Windows diff surface로 확인할 수 있다.

대표 명령:
- `/open <path>`
- `/selection`
- `/selections`
- `/diff`
- `/diff-selection`
- `/review-selection [extra]`
- `/review-selections [extra]`
- `/edit-selection <task>`
- `/note-selection <text>`
- `/tag-selection <tag[,tag2]>`

diff workflow 메모:
1. Windows에서는 `/diff`, `/diff-selection`이 내부 WebView2 diff viewer를 우선 사용한다.
2. read-only diff viewer에는 changed-file navigation, unified/split 전환, intraline highlight가 포함된다.
3. 내부 surface를 사용할 수 없으면 터미널 출력으로 fallback한다.
4. `Open diff preview?`에서 `a`를 누르면 현재 수정은 바로 승인되고, 이후 diff preview도 세션 동안 건너뛴다.

좋은 상황:
1. 특정 IOCTL handler, provider registration block, integrity check 함수만 집중 분석하고 싶을 때
2. 방금 simulation에서 지적된 surface와 실제 코드 영역을 빠르게 연결하고 싶을 때

### 2.8 Plan Review Workflow

목적:
1. planner 모델이 계획을 만들고
2. reviewer 모델이 계획을 검토하고
3. 승인된 계획을 다시 실행하게 한다.

대표 명령:
- `/set-plan-review`
- `/do-plan-review <task>`

좋은 상황:
1. 구현이 여러 단계로 얽힌 driver/telemetry 보안 변경
2. 공격자 관점과 운영 현실성을 같이 고려해야 하는 큰 수정
3. 실수 비용이 커서 바로 편집 루프로 들어가기 전에 계획 검토가 필요한 작업

현재 연동:
1. recent simulation finding이 task와 겹치면 planning prompt에 자동 주입된다.
2. 최종 plan 실행 prompt에도 같은 관점이 자동 주입된다.

### 2.9 Tracked Feature Workflow

목적:
1. 일회성 plan 대신 여러 세션에 걸쳐 유지되는 feature workspace를 만든다.
2. `.kernforge/features/<id>` 아래에 spec, plan, task, implementation artifact를 남긴다.
3. planning과 execution을 분리해서 큰 변경을 더 안전하게 이어간다.

대표 명령:
- `/new-feature <task>`
- `/new-feature list`
- `/new-feature status [id]`
- `/new-feature plan [id]`
- `/new-feature implement [id]`
- `/new-feature close [id]`

좋은 상황:
1. feature 작업이 한 세션 안에 끝나지 않을 때
2. scope, sequencing, acceptance 기준을 artifact로 남기고 싶을 때
3. 계획을 만든 직후 바로 구현하지 않고 한 번 더 점검하고 싶을 때

현재 연동:
1. `/new-feature <task>`는 `/new-feature start <task>`와 같게 동작하며 `feature.json`, `spec.md`, `plan.md`, `tasks.md`를 만든다.
2. 생성된 feature는 세션의 active feature로 기록된다.
3. `/new-feature implement [id]`는 저장된 plan을 실행하고 `implementation.md`를 남긴다.

### 2.10 Interactive Ergonomics

목적:
1. investigation, verification, review 흐름에서 반복 입력 부담을 줄인다.
2. subcommand나 id를 기억하지 못해도 빠르게 이어서 작업하게 한다.

현재 `Tab` 완성이 커버하는 범위:
1. slash command 이름
2. workspace path와 `@file` 멘션
3. MCP resource/prompt target
4. `/set-auto-verify on|off`, `/permissions`, `/checkpoint-auto`, `/verify --full`, `/investigate start <preset>`, `/simulate <profile>` 같은 고정 인자
5. `/resume`, `/evidence-show`, `/mem-show`, `/mem-promote`, `/mem-demote`, `/mem-confirm`, `/mem-tentative`, `/investigate show`, `/simulate show`, `/new-feature status|plan|implement|close`에 필요한 저장된 id

토큰 예산 관점에서 달라진 점:
1. cached `analyze-project` summary가 더 적절하면 auto-scout 코드 조각보다 먼저 주입될 수 있다.
2. cached project analysis만으로 충분한 질문은 추가 tool iteration 없이 바로 답할 수 있다.
3. skill/MCP catalog는 실제로 그 정보를 묻는 요청에서만 크게 포함된다.
4. auto-scout는 후보 수와 문맥 길이를 줄였고, 위치 찾기/정의 찾기/참조 찾기 성격의 질문에 더 집중한다.

## 3. 가장 추천하는 실전 흐름

### 3.1 Driver hardening 또는 signing 관련 변경

상황:
- `driver/guard.cpp`, `driver/guard.inf`를 수정했다.
- signing/symbol/package readiness가 중요하다.
- 최근에도 비슷한 실패를 겪은 적이 있다.

추천 흐름:
1. `/investigate start driver-visibility guard.sys`
2. `/investigate snapshot`
3. `/investigate note current driver visibility snapshot captured before edit`
4. `/simulate tamper-surface guard.sys`
5. `/open driver/guard.cpp`
6. viewer에서 보호 로직 부분을 선택한다.
7. `/review-selection integrity risk paths and verifier interactions`
8. `/edit-selection harden registration and signing assumptions`
9. `/verify`
10. `/evidence-dashboard category:driver`
11. `/mem-search category:driver signal:signing`
12. 필요하면 `/investigate stop hardened signing path reviewed`

이 흐름에서 Kernforge가 해주는 일:
1. live driver visibility 상태를 investigation evidence로 남긴다.
2. tamper-surface simulation으로 tamper risk 신호를 먼저 드러낸다.
3. 선택한 코드 범위 리뷰/수정 시 simulation 관점을 prompt에 자동 주입한다.
4. `/verify`가 driver security verification과 recent simulation/investigation follow-up step을 같이 넣는다.
5. evidence/hook이 push/PR 전 마지막 방어를 맡는다.

어떤 명령이 특히 중요하나:
- `/simulate tamper-surface guard.sys`
- `/review-selection ...`
- `/verify`
- `/evidence-dashboard category:driver`

### 3.2 Telemetry provider drift 또는 XML/manifest 회귀

상황:
- provider manifest와 registration 코드가 같이 바뀌었다.
- 이벤트가 실제 런타임에서 보일지 불안하다.
- stealth 관점에서 observer coverage도 같이 보고 싶다.

추천 흐름:
1. `/investigate start provider-visibility MyProvider`
2. `/investigate snapshot MyProvider`
3. `/simulate stealth-surface MyProvider`
4. `/open telemetry/provider.man`
5. manifest range를 선택한다.
6. `/review-selection provider visibility and schema drift`
7. `/open telemetry/register_provider.cpp`
8. `/edit-selection align provider registration and fallback visibility`
9. `/verify`
10. `/evidence-search category:telemetry outcome:failed`
11. `/simulate forensic-blind-spot MyProvider`
12. `/mem-search category:telemetry signal:provider`
13. `/investigate stop provider contract and visibility reviewed`

Kernforge가 도와주는 부분:
1. live provider 상태를 먼저 관찰한다.
2. stealth-surface가 "보이기는 하는가" 관점을 앞에 끌어온다.
3. forensic-blind-spot이 "나중에 추적 가능한가"까지 확인하게 만든다.
4. `/verify`는 XML/provider/telemetry review step과 recent simulation follow-up step을 동시에 넣는다.

### 3.3 Memory scan / pattern scan 회귀 점검

상황:
- false positive 또는 evasion 대응 수정이 들어갔다.
- 최근 scanner 변경에서 반복 실패가 있었다.

추천 흐름:
1. `/simulate stealth-surface scanner-core`
2. `/open scanner/patternscan.cpp`
3. `/review-selection false positives, stealth coverage, and perf ceilings`
4. `/edit-selection reduce false positives without weakening evasion coverage`
5. `/verify`
6. `/evidence-dashboard category:memory-scan`
7. `/mem-search category:memory-scan risk:>=70`

왜 이 흐름이 좋은가:
1. scanner 작업은 단순 correctness보다 coverage와 evasions가 중요하다.
2. simulation이 우선 공격자 관점을 주고,
3. verification이 이후 보안 review step으로 다시 고정해 준다.

### 3.4 큰 변경 전에 plan-review를 거는 경우

상황:
- driver + telemetry 쪽을 함께 건드리는 큰 변경
- 구현 순서와 rollback 포인트가 중요하다.

추천 흐름:
1. `/simulate tamper-surface guard.sys`
2. `/simulate forensic-blind-spot guard.sys`
3. `/do-plan-review harden driver registration, improve telemetry visibility, and preserve post-incident artifacts`
4. reviewer가 계획을 비판하도록 둔다.
5. 승인된 뒤 plan 실행
6. `/verify`
7. `/evidence-dashboard`

현재 장점:
1. simulation finding이 planning prompt에 직접 주입된다.
2. 최종 plan 실행 prompt에도 그 관점이 다시 들어간다.

### 3.5 여러 세션에 걸친 tracked feature lifecycle

상황:
- 구현과 verification, 정리가 여러 번에 나뉘는 큰 feature 작업
- spec, plan, task artifact를 남기며 추적하고 싶은 경우

추천 흐름:
1. `/simulate tamper-surface guard.sys`
2. `/new-feature harden driver registration, preserve telemetry audit artifacts, and document rollback points`
3. `/new-feature status`
4. `.kernforge/features/<id>` 아래의 `spec.md`, `plan.md`, `tasks.md`를 검토한다.
5. `/new-feature implement`
6. `/verify`
7. `/new-feature close`

왜 이 흐름이 좋은가:
1. feature 상태가 세션 밖에서도 유지된다.
2. planning artifact를 다시 읽고 재생성하기 쉽다.
3. planning과 execution이 분리되어 초안 품질이 낮을 때 바로 긴 구현으로 들어갈 위험을 줄인다.

## 4. 명령별 상세 사용법과 좋은 예시

### 4.1 `/investigate`

기본 사용:

```text
/investigate start driver-visibility guard.sys
/investigate snapshot
/investigate note verifier enabled on target system
/investigate stop initial driver state captured
```

좋은 사용 예:
1. 코드 수정 전에 현재 driver visibility와 verifier 상태를 고정하고 싶을 때
2. driver load root cause를 파기 전에 현재 가시성 triage를 남기고 싶을 때
3. telemetry provider가 정말 보이는지 live 상태를 남기고 싶을 때
4. 나중에 "그때 live 상태가 어땠지?"를 evidence로 다시 찾고 싶을 때

추천 해석:
1. investigation은 verification 대체가 아니다.
2. verification 전에 현실 상태를 고정하는 역할이다.
3. 특히 `driver-visibility`는 깊은 로드 분석기가 아니라 lightweight visibility snapshot이다.

### 4.2 `/simulate`

기본 사용:

```text
/simulate tamper-surface guard.sys
/simulate stealth-surface MyProvider
/simulate forensic-blind-spot game.exe
```

좋은 사용 예:
1. driver 변경 직후 integrity/signing risk 면을 보고 싶을 때
2. telemetry 변경 후 observer visibility gap을 보고 싶을 때
3. forensic artifact가 부족한 변경인지 보고 싶을 때

추천 해석:
1. simulation은 "지금 당장 exploit 가능"을 증명하는 도구가 아니다.
2. evidence와 investigation 결과를 바탕으로 risk signal을 구조화하는 도구다.

### 4.3 `/review-selection`과 `/edit-selection`

기본 사용:

```text
/open driver/guard.cpp
/review-selection check risk surfaces and cleanup paths
/edit-selection harden the selected registration path
```

좋은 사용 예:
1. 특정 함수나 registration block만 집중적으로 보고 싶을 때
2. recent simulation finding과 실제 코드 범위를 빨리 연결하고 싶을 때

현재 자동 연동:
1. 선택한 파일 경로와 맞닿는 recent simulation finding이 있으면
2. review/edit prompt에 `Additional simulation risk focus`가 자동 주입된다.

### 4.4 `/do-plan-review`

기본 사용:

```text
/do-plan-review harden driver load validation, improve telemetry provider visibility, and preserve audit artifacts
```

좋은 사용 예:
1. 구현이 여러 단계로 얽힌 대형 변경
2. 먼저 계획을 비판적으로 다듬고 싶은 경우
3. simulation 결과를 바로 구현 계획에 녹이고 싶은 경우

현재 자동 연동:
1. task 텍스트와 겹치는 recent simulation finding이 있으면 planning prompt에 자동 주입된다.
2. 최종 실행 prompt에도 같은 관점이 다시 들어간다.

### 4.5 `/new-feature`

기본 사용:

```text
/new-feature harden driver registration, preserve telemetry audit artifacts, and document rollback points
/new-feature status
/new-feature plan
/new-feature implement
/new-feature close
```

좋은 사용 예:
1. spec, plan, task, implementation artifact를 남기며 진행하고 싶은 새 기능 작업
2. planning 직후 바로 구현하지 않고 pause/resume이 필요한 경우
3. 세션 상태에 active feature id를 유지하는 편이 유리한 변경

현재 자동 연동:
1. `.kernforge/features/<id>` 아래에 tracked feature workspace가 생성된다.
2. start 또는 re-plan 시 `spec.md`, `plan.md`, `tasks.md`가 다시 생성된다.
3. `/new-feature implement [id]`는 저장된 plan을 실행하고 `implementation.md`를 남긴다.
4. `status`, `plan`, `implement`, `close`는 전체 id뿐 아니라 고유 prefix도 받을 수 있다.

### 4.6 `/verify`

기본 사용:

```text
/verify
/verify --full
/verify driver/guard.cpp,telemetry/provider.man
```

현재 planner가 보는 것:
1. 변경 파일
2. security category
3. verify policy
4. verification history tuning
5. hook이 추가한 context/step
6. recent investigation/simulation 결과

좋은 사용 예:
1. edit 이후 실제 verification plan을 확인하고 싶을 때
2. 최근 simulation finding이 verification에도 반영되는지 보고 싶을 때
3. 단순 빌드/테스트보다 깊은 보안 review step이 필요한 경우

### 4.7 `/evidence-search`와 `/evidence-dashboard`

자주 쓰는 쿼리 예:

```text
/evidence-search category:driver outcome:failed
/evidence-search kind:simulation_finding severity:critical
/evidence-search signal:tamper risk:>=60
/evidence-dashboard category:telemetry
```

좋은 사용 예:
1. 방금 simulation이 뭘 남겼는지 보고 싶을 때
2. 최근 failed signing/provider finding만 보고 싶을 때
3. override가 활성화되어 있는지 같이 보고 싶을 때

### 4.8 `/mem-search`

자주 쓰는 쿼리 예:

```text
/mem-search category:driver signal:signing
/mem-search category:telemetry tag:provider
/mem-search severity:critical risk:>=80
/mem-search artifact:guard.sys
```

좋은 사용 예:
1. 예전 세션에서 왜 이 방향으로 판단했는지 다시 찾고 싶을 때
2. 특정 artifact 또는 failure가 반복됐는지 장기 관점으로 보고 싶을 때

### 4.9 `/hooks`와 `/override-*`

확인:

```text
/hooks
/override
```

예외 추가:

```text
/override-add deny-driver-pr-with-critical-signing-or-symbol-evidence 4 urgent hotfix after manual verification
```

해제:

```text
/override-clear all
```

좋은 사용 예:
1. 정책이 왜 막는지 먼저 확인하고 싶을 때
2. 예외를 주더라도 감사 추적을 남기고 싶을 때

## 5. 대시보드는 언제 어떤 것을 보면 좋은가

### 5.1 `/verify-dashboard`

추천 시점:
1. 최근 verification 실패 경향을 보고 싶을 때
2. 어떤 check가 자주 깨지는지 보고 싶을 때

### 5.2 `/evidence-dashboard`

추천 시점:
1. 지금 workspace의 failed/high-risk 상태를 빠르게 보고 싶을 때
2. override, severity, signal 분포까지 함께 보고 싶을 때

### 5.3 `/mem-dashboard`

추천 시점:
1. 장기 맥락, trust/importance, verification artifact 분포를 보고 싶을 때
2. 이전 세션 누적 지식을 훑고 싶을 때

### 5.4 `/investigate dashboard`

추천 시점:
1. 최근 investigation session이 얼마나 쌓였는지 보고 싶을 때
2. 어떤 preset과 finding category가 많이 나왔는지 보고 싶을 때

### 5.5 `/simulate dashboard`

추천 시점:
1. 최근 simulation run이 어떤 profile에 몰렸는지 보고 싶을 때
2. tamper/stealth/forensics signal과 recommended action 분포를 보고 싶을 때

## 6. 처음 쓰는 팀을 위한 추천 운영안

### 6.1 Driver 팀

추천:
1. `windows-security` preset 활성화
2. 코드 수정 전에 `driver-visibility` investigation
3. 수정 전에 `tamper-surface` simulation
4. `/verify`
5. `/evidence-dashboard category:driver`
6. 반복 실패만 deny로 강화

### 6.2 Telemetry 팀

추천:
1. provider manifest 작업 전 `provider-visibility` investigation
2. 수정 후 `stealth-surface`, 필요하면 `forensic-blind-spot`
3. `/verify`
4. `/evidence-search category:telemetry outcome:failed`
5. `/mem-search category:telemetry tag:provider`

### 6.3 Anti-Cheat / Memory Scan 팀

추천:
1. scanner 관련 변경 전 `stealth-surface`
2. 선택 영역 리뷰/수정 적극 사용
3. `/verify`
4. recent high-risk failure는 checkpoint와 deny 정책으로 묶기

## 7. 너무 과하게 쓰지 않는 것이 좋은 경우

다음 상황에서는 규칙과 절차를 너무 강하게 두지 않는 것이 좋다.

1. 아주 초기 프로토타이핑
2. 아직 evidence가 거의 쌓이지 않은 새 프로젝트
3. security workflow와 무관한 범용 유틸리티 수정

추천:
1. 초반에는 `warn`
2. 익숙해지면 `ask`
3. 실제 운영 사고와 직결된 항목만 `deny`

## 8. 빠른 시작용 추천 시나리오

### 시나리오 A: driver integrity hardening

```text
/investigate start driver-visibility guard.sys
/investigate snapshot
/simulate tamper-surface guard.sys
/open driver/guard.cpp
/review-selection integrity risk paths
/edit-selection harden the selected integrity checks
/verify
/evidence-dashboard category:driver
```

### 시나리오 B: telemetry provider visibility drift

```text
/investigate start provider-visibility MyProvider
/investigate snapshot MyProvider
/simulate stealth-surface MyProvider
/open telemetry/provider.man
/review-selection schema and visibility drift
/verify
/evidence-search category:telemetry outcome:failed
```

### 시나리오 C: 큰 변경 전에 plan-review

```text
/simulate tamper-surface guard.sys
/simulate forensic-blind-spot guard.sys
/do-plan-review harden driver registration and preserve telemetry audit artifacts
/verify
/simulate-dashboard
```

### 시나리오 D: tracked feature를 만들고 명시적으로 실행

```text
/simulate tamper-surface guard.sys
/new-feature harden driver registration and preserve telemetry audit artifacts
/new-feature status
/new-feature implement
/verify
/new-feature close
```

## 9. 문서 요약

현재 Kernforge를 가장 잘 쓰는 방법은 다음 한 문장으로 요약할 수 있다.

"먼저 관찰하고, risk lens로 약한 면을 점검하고, 선택 영역 단위로 리뷰/수정하고, verification으로 닫고, evidence와 memory를 다시 정책으로 사용한다."

즉 가장 추천되는 루프는 아래와 같다.

1. `/investigate`
2. `/simulate`
3. `/review-selection` 또는 `/edit-selection`
4. `/do-plan-review`
5. `/new-feature`
6. `/verify`
7. `/evidence-dashboard`
8. `/mem-search`
9. push/PR에서 hook policy 적용

이 루프가 현재 Kernforge의 가장 큰 차별점이다.
