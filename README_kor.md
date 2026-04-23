# Kernforge

![Kernforge banner](./branding/kernforge-release-banner-1280x640.png)
![Kernforge demo](./branding/kernforge_demo.gif)

`Kernforge`는 Go로 만든 터미널 중심 AI 코딩 CLI입니다. 로컬 우선 개발 흐름에 맞춰 설계되어 있고, 특히 Windows security, anti-cheat, telemetry, driver 워크플로우와 대형 프로젝트 분석에 실용적으로 쓰기 좋게 구현되어 있습니다.

현재 Kernforge의 가장 큰 강점은 `multi-agent project analysis pipeline`입니다. 큰 워크스페이스를 재사용 가능한 knowledge pack으로 정리하고, 그 분석 결과를 편집, 검증, evidence, policy 단계까지 그대로 이어갈 수 있습니다.  
특히 `project analysis -> performance lens -> adaptive verification -> evidence store -> persistent memory -> hook policy -> checkpoint/rollback` 흐름을 중심으로, driver, telemetry, memory-scan, Unreal 보안 작업을 더 안전하고 일관되게 진행할 수 있도록 설계되어 있습니다.

## 대표 강점

Kernforge에서 가장 먼저 봐야 할 기능 하나를 꼽으면 `multi-agent project analysis`입니다.

- `/analyze-project [--mode map|trace|impact|security|performance] <goal>`는 일회성 요약이 아니라 재사용 가능한 architecture map을 만든다.
- 결과물은 knowledge pack, performance lens, structural index, vector-ready analysis set으로 남는다.
- 이 분석 자산은 이후 review, edit, verification, policy 흐름까지 계속 재사용된다.

## 문서

빠른 시작:
- [한국어 빠른 시작](./QUICKSTART_kor.md)
- [English Quickstart](./QUICKSTART.md)

가이드:
- [한국어 기능 활용 가이드](./FEATURE_USAGE_GUIDE_kor.md)
- [English Feature Usage Guide](./FEATURE_USAGE_GUIDE.md)

플레이북:
- [Driver 플레이북](./PLAYBOOK_driver_kor.md)
- [Telemetry 플레이북](./PLAYBOOK_telemetry_kor.md)
- [Memory-Scan 플레이북](./PLAYBOOK_memory_scan_kor.md)

설계 문서:
- [한국어 로드맵](./ROADMAP_kor.md)
- [한국어 Hook Engine 스펙](./HOOK_ENGINE_SPEC_kor.md)
- [한국어 Live Investigation Mode 스펙](./LIVE_INVESTIGATION_SPEC_kor.md)
- [한국어 Adversarial Simulation 스펙](./ADVERSARIAL_SIMULATION_SPEC_kor.md)
- [한국어 차세대 Project Analysis 스펙](./PROJECT_ANALYSIS_NEXT_SPEC_kor.md)

가장 추천되는 실사용 흐름은 [한국어 상세 사용 가이드](./FEATURE_USAGE_GUIDE_kor.md)에 정리되어 있습니다. 특히 `investigate -> simulate -> fuzz-func -> review/edit/plan -> verify -> evidence/memory/hooks` 루프를 그대로 따라가 보면 현재 Kernforge의 핵심 가치를 가장 빨리 체감할 수 있습니다.

## 왜 Kernforge인가

Kernforge는 큰 보안 민감 코드베이스를 먼저 정확히 이해한 다음 변경해야 하는 상황에서 특히 강합니다.

1. driver/signing/symbol/package readiness처럼 실수 비용이 큰 작업
2. telemetry/provider/manifest drift처럼 테스트만으로 놓치기 쉬운 작업
3. memory-scan, Unreal integrity처럼 구조 이해와 운영 가드레일이 동시에 필요한 작업

핵심 차별점은 다음과 같습니다.

1. conductor와 여러 worker/reviewer 패스를 사용해 큰 워크스페이스를 분석할 수 있습니다.
2. 일회성 요약이 아니라 재사용 가능한 knowledge pack과 performance lens를 만듭니다.
3. 그 분석 결과를 review, edit, verification, investigation 흐름으로 그대로 이어갑니다.
4. verification 결과를 evidence와 persistent memory에 구조적으로 남깁니다.
5. 이후 hook policy, push/PR 판단, safety checkpoint까지 연결합니다.

## 현재 구현된 기능

- 재사용 가능한 knowledge pack과 performance lens를 만드는 multi-agent project analysis
- `TaskState`, `TaskGraph`, node-aware recovery, executor guidance를 갖춘 구조화된 interactive orchestration
- built-in specialist subagent catalog와 editable/read-only specialist routing
- node별 editable ownership/lease, specialist worktree lease, session-level worktree isolation
- 대화형 REPL과 `-prompt` 기반 one-shot 실행
- `ollama`, `anthropic`, `openai`, `openrouter`, `openai-compatible` provider 지원
- 파일, 패치, 셸, git 중심 도구 호출
- `git_add`, `git_commit`, `git_push`, `git_create_pr` 같은 전용 git 도구
- 로컬 파일 멘션, 이미지 멘션, MCP 리소스 멘션
- 세션 저장, 재개, 이름 변경, clear, compact, Markdown export
- 프로젝트 메모리 파일과 세션 간 persistent memory
- evidence store, evidence search, evidence dashboard
- 로컬 `SKILL.md` 스킬 탐색과 요청 단위 활성화
- stdio 기반 MCP server의 tool, resource, prompt 연결
- Windows용 별도 텍스트 viewer와 WebView2 기반 diff review/diff viewer
- adaptive verification, 검증 이력 대시보드, checkpoint, rollback
- hook engine, workspace hook rules, evidence-aware push/PR policy
- 별도 reviewer 모델을 사용하는 plan-review 워크플로우
- `.kernforge/features` 아래에 spec/plan/tasks/implementation artifact를 남기는 tracked feature 워크플로우
- disjoint edit lease에 대한 automatic secondary editable worker와 specialist-aware background verification bundle chaining

## 핵심 특징

### Project Analysis

- `/analyze-project [--mode map|trace|impact|security|performance] <goal>`로 conductor와 여러 sub-agent를 사용해 프로젝트 문서를 생성
- `--mode`를 생략하면 기본 모드는 `map`
- `security` 모드에서는 관련 경로가 있을 때 `driver`, `IOCTL`, `handle`, `memory`, `RPC` surface로 결과를 분해해서 본다.
- 변경되지 않은 shard는 가능한 경우 재사용하는 incremental 분석
- goal에 특정 디렉토리 힌트가 있으면 해당 하위 영역으로 분석 범위를 좁힐 수 있다.
- interactive 실행에서는 hidden directory나 external-looking directory를 보여 주고 이번 분석에서 제외할지 확인할 수 있다.
- semantic fingerprint 기반 invalidation으로 file hash만으로 놓치기 쉬운 구조 변화까지 다시 분석
- `.uproject`, `.uplugin`, `.Build.cs`, `.Target.cs`, `compile_commands.json`를 build alignment에 반영해 재사용 가능한 build context를 만든다.
- `structural_index_v2`는 이제 file 중심 요약을 넘어 symbol anchor, build ownership edge, function-level call edge, overlay edge를 함께 담는다.
- `trace`, `impact`, `security` retrieval은 graph neighborhood를 확장하고 `build_context_v2`, `path_v2` 근거를 함께 남긴다.
- Unreal project/module/target/type/network/asset/system/config 신호를 구조화해 대형 UE 프로젝트 대응
- semantic shard planner와 semantic-aware worker/reviewer prompt로 startup, network, UI, GAS, asset/config, integrity 영역을 우선 분석
- knowledge pack 외에도 structural index, `structural_index_v2`, Unreal semantic graph, vector corpus, vector ingestion export를 함께 생성
- source anchor parser는 template out-of-line method, operator, `requires`, `decltype(auto)`, API macro가 낀 scope, friend function 같은 modern C++ 패턴까지 추적한다.
- `security` 모드 최종 문서에는 privileged path를 따로 읽기 쉽도록 `Security Surface Decomposition` 섹션이 추가된다.
- 메인 채팅 모델과 별도로 worker/reviewer 모델을 지정 가능
- `.kernforge/analysis` 아래에 architecture knowledge pack과 performance lens 출력
- `/analyze-performance [focus]`로 최신 분석 산출물을 기준으로 병목과 hot path 분석

### 보안 검증과 정책 루프

- driver, telemetry, Unreal, memory-scan 중심 security-aware verification
- verification history와 verification dashboard
- verification 결과의 evidence store 누적
- evidence 검색과 evidence dashboard
- `/set-auto-verify [on|off]` 기반 automatic verification 런타임 토글
- `/detect-verification-tools`, `/set-*-path` 기반 Windows verification tool 경로 탐지와 override
- recent failed evidence를 이용한 hook 기반 push/PR 경고, 확인, 차단
- 반복 실패 상황에서 자동 safety checkpoint 생성 가능

### 소스 레벨 Function Fuzzing

- `/fuzz-func <function-name>`, `/fuzz-func <function-name> --file <path>`, `/fuzz-func @<path>`로 함수 단위 또는 파일 단위 source-level fuzzing 시작
- `@<path>` 또는 `--file <path>`만 주면 시작 파일과 실제 호출 흐름을 기준으로 대표 루트와 입력 지향 함수 후보를 자동 선택
- `analyze-project`나 `structural_index_v2`가 없어도 워크스페이스 스캔과 on-demand semantic index 복원으로 바로 계획 생성
- 기본 동작은 네이티브 실행보다 AI source-only fuzzing이며, 공격자 입력 상태, 구체 입력 예시, 비교식, 최소 반례, 분기 차이, 후속 호출 체인까지 소스에서 추론
- 고위험 finding은 위험도 점수표, 먼저 볼 소스 위치, 시작 파일에서 거기까지의 파일 확장 경로, 대표 루트에서 이어지는 호출 경로를 함께 보여준다.
- `compile_commands.json`이나 build context가 있으면 후속 네이티브 fuzzing 품질이 올라가고, 없으면 왜 막히는지 먼저 설명한 뒤 진행 여부를 묻는다.
- 결과 산출물은 `.kernforge/fuzz/<run-id>/` 아래의 `report.md`, `harness.cpp`, `plan.json` 등에 저장된다.
- `/fuzz-func status|show|list|continue|language`로 최근 분석 결과, 보류된 실행, 출력 언어를 관리할 수 있다.
- 자동완성은 `/fuzz-func ` 단계에서는 함수명/파일 사용 힌트를 보여주고, `@`를 입력하기 시작하면 실제 workspace 파일 후보로 전환된다.

### 편집 워크플로우

- 파일 쓰기 전 WebView2 diff review
- selection-aware edit preview
- 편집 후 자동 verification
- 큰 파일 편집 루프에서 `read_file`는 변경되지 않은 동일 범위, 포함되는 하위 범위, 부분 겹침 범위를 재사용해서 불필요한 재읽기를 줄인다.
- 최근 `read_file` 문맥과 겹치거나 가까운 `grep` 결과에는 `[cached-nearby:inside]`, `[cached-nearby:N]` 힌트가 붙어 다음 읽기 범위를 더 좁게 잡도록 돕는다.
- 같은 파일에 대한 반복 `read_file` 턴은 캐시 기반 경고를 먼저 주고, 그래도 진전이 없을 때만 강한 반복 호출 중단으로 넘어간다.
- `Allow write?`에서 `a`를 누르면 현재 세션 동안만 write auto-approval이 켜진다.
- `Open diff preview?`에서 `a`를 누르면 현재 수정과 이후 diff preview를 세션 동안 자동 승인
- `git_add`, `git_commit`, `git_push`, `git_create_pr` 같은 git 변경 도구는 별도의 `Allow git?` 세션 승인 경로를 사용한다.
- git 변경 도구는 일반 review/edit 턴이 아니라, 사용자가 명시적으로 git 작업을 요청한 경우에만 쓰는 것이 기본 동작이다.
- 한 요청의 첫 편집 전에 자동 checkpoint 생성
- 수동 checkpoint, checkpoint diff, rollback
- `/open` 중심 selection-first 리뷰/수정 흐름
- 일반 개발에서는 `implementation-owner`가 기본 editable specialist로 붙고, 필요할 때만 `driver-build-fixer`, `telemetry-analyst`, `unreal-integrity-reviewer`, `memory-inspection-reviewer` 같은 도메인 specialist가 task-graph node를 소유해 edit scope를 더 좁힌다.
- `apply_patch`, `write_file`, `replace_in_file`, scoped shell write는 node ownership/lease를 따라가며 배정된 specialist worktree로 라우팅된다.
- `/specialists assign <node-id> <specialist> [glob,glob2]`로 자동 배정 대신 특정 editable specialist와 ownership을 수동 고정할 수 있다.
- `/set-specialist-model <specialist> <provider> [model]`로 이 workspace에서 특정 specialist가 쓸 LLM을 고정하고, `/set-specialist-model clear <specialist|all>`로 override를 지울 수 있다.
- lease가 겹치지 않는 secondary edit node는 automatic editable worker가 추가 patch를 만들 수 있고, lease가 겹치면 충돌 대신 defer된다.
- parallel specialist edit가 verification을 다시 시작하면 같은 owner나 같은 lease의 오래된 background verification bundle은 자동 supersede되고, verification-like bundle이 완료되면 owning node도 함께 닫힌다.

### Tracked Feature Workflow

- `/new-feature <task>`는 tracked feature workspace를 만들고 `spec.md`, `plan.md`, `tasks.md`를 생성한다.
- feature artifact는 `.kernforge/features/<id>` 아래에 저장되어 여러 세션에 걸친 작업을 이어가기 쉽다.
- `/new-feature status|plan|implement|close [id]`로 active feature 상태 확인, 재계획, 실행, 종료를 분리해서 다룰 수 있다.
- `/do-plan-review <task>`는 여전히 one-shot 계획 검토 후 즉시 실행하는 흐름에 더 적합하다.

### 입력과 프롬프트

- 대화형 채팅 REPL
- `-prompt` 기반 단발 실행
- `-image`, `-i`, `@path/to/image.png` 이미지 첨부
- `@main.go` 같은 파일 멘션
- `@main.go:120-150` 같은 라인 범위 멘션
- `@mcp:docs:getting-started` 같은 MCP 리소스 멘션
- 줄 끝에 `\`를 붙여 멀티라인 입력
- 파일을 명시하지 않았을 때 자동 코드 scouting
- 최근 `analyze-project` 결과를 cached architecture context로 재사용해서 큰 코드 영역 재탐색을 줄일 수 있다.
- cached analysis만으로 답이 충분하면 추가 tool 호출 없이 바로 응답할 수 있다.
- `read_file`가 cached NOTE를 반환하면 Kernforge는 해당 줄을 이미 본 문맥으로 간주해 같은 큰 범위를 다시 읽는 흐름을 줄인다.
- `grep`의 `cached-nearby` 힌트는 아직 읽지 않은 인접 줄만 좁게 다시 읽도록 유도해서 큰 파일 재탐색 비용을 낮춘다.
- 분석, 설명, 진단, 문서화 요청은 기본적으로 read-only investigation 모드로 처리된다.
- 명시적으로 수정까지 요청한 프롬프트는 tool-driven edit 흐름을 유지하고, Kernforge는 모델이 패치를 사용자에게 되돌리려 하면 한 번 더 수정 도구 사용을 유도한다.

### 사용성

- 명령, 경로, 멘션, MCP 대상, 고정 인자, `/provider status|anthropic|openai|openrouter|ollama` 같은 provider 하위 명령, analyze-project mode, `/resume`, `/mem-show`, `/evidence-show`, `/investigate show`, `/simulate show`, `/new-feature status|plan|implement|close`, `/specialists status|assign|cleanup`, `/worktree status|create|leave|cleanup` 같은 저장된 id와 하위 명령까지 `Tab` 완성
- command/subcommand 자동완성 메뉴에 각 후보 설명을 같이 보여줘서 이름만 나열되지 않게 했다.
- 현재 입력 취소를 위한 `Esc`
- 진행 중 요청 취소를 위한 `Esc`
- 메인 프롬프트에서 빈 입력 상태로 `Enter`를 눌러도 빈 턴을 만들지 않고 무시한다.
- REPL은 compact branded banner, subtle turn divider, grouped status/config section, assistant/tool activity stream 분리로 더 촘촘한 터미널 UX를 사용한다.
- assistant streaming 출력은 선행 blank chunk를 무시하고, progress/info 출력 전 경계를 정리하며, 반복 follow-on preamble 사이에 줄바꿈을 넣어 더 읽기 쉽게 출력된다.
- `이제 ... 확인하겠습니다`, `먼저 ... 수정하겠습니다` 같은 짧은 tool 전 narration은 가능한 한 footer형 진행 상태로 흡수해서 불필요한 assistant block이 늘어나지 않게 했다.
- 기본 대기 문구는 thinking prefix와 중복되지 않게 정리해서 같은 의미를 두 번 보여주지 않는다.
- 반복 blank streamed chunk는 빈 줄 대신 compact working 상태로 바꿔 보여준다.
- 진행 중 상태, 짧은 `next` 프리앰블, tool progress는 이제 본문 사이에 끼어들지 않고 하단 footer 패널을 공유한다.
- 취소 확인, diff preview 확인, write 승인, verification 복구 같은 확인 프롬프트도 같은 footer 슬롯을 잠시 점유해서 화면 맨 아래에 고정된 것처럼 보이게 했다.
- 완료 요약, output 경로, warning, 설정 변경처럼 나중에 다시 봐야 하는 결과는 본문 transcript에 남기고, 일시적인 진행 상황만 footer로 흘린다.
- 문장 중간에서 잘린 최종 답변은 한 번 continuation 재시도를 걸고 합쳐서 출력한 뒤 프롬프트로 복귀한다.
- Windows 콘솔에서 짧게 누른 `Esc`도 안정적으로 요청 취소
- 요청 취소 직후 다음 프롬프트가 연속 `Esc` 입력으로 자동 취소되지 않도록 안정화
- Windows 콘솔의 `Up`, `Down` 입력 히스토리
- prompt 조립 시 긴 summary를 잘라 넣고, skill/MCP catalog는 실제로 필요한 요청에서만 크게 싣는다.
- auto-scout는 위치 찾기, 정의 찾기, 참조 찾기 성격의 질문에 더 집중하고 턴당 문맥 투입량도 줄였다.

### 지속성

- `/resume` 기반 세션 재개
- 세션 이름 변경과 대화 Markdown export
- citation id, trust, importance가 붙는 persistent memory
- verification category, tag, artifact, failure 기반 memory 검색
- `KERNFORGE.md`, `.kernforge/KERNFORGE.md` 기반 프로젝트 가이드 로딩
- 시스템 locale 기반 자동 언어 지시

### 확장성

- 로컬 `SKILL.md` 스킬
- MCP tool
- MCP resource
- MCP prompt

## 빠른 시작

### 빌드

```powershell
go build -o kernforge.exe .
```

### WebView2 Runtime

Windows diff review와 read-only diff viewer는 WebView2를 사용합니다.

권장 배포 방식:

1. `Evergreen Bootstrapper`
   일반적인 온라인 설치에 가장 무난합니다.
2. `Evergreen Standalone Installer`
   오프라인 또는 제한된 환경에 더 적합합니다.
3. `Fixed Version Runtime`
   렌더링 엔진 버전을 반드시 고정해야 할 때만 권장합니다.

Kernforge 기준 실무 권장안:

1. 설치 프로그램에 `Evergreen Bootstrapper`를 포함하거나 다운로드 경로를 둡니다.
2. Kernforge 실행 전에 WebView2 Runtime 존재 여부를 확인합니다.
3. 없으면 먼저 설치합니다.
4. 그래도 WebView2 초기화가 실패하면 workflow에 따라 브라우저 기반 preview 또는 터미널 diff 출력으로 fallback합니다.

참고:

- [Microsoft WebView2 배포 가이드](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution)
- [WebView2 Runtime 다운로드](https://developer.microsoft.com/en-us/microsoft-edge/webview2/)

### 실행

```powershell
.\kernforge.exe
```

아직 provider/model이 설정되지 않았다면 Kernforge는 다음 순서로 초기 설정을 도와줍니다.

1. 로컬 Ollama 서버를 탐지합니다.
2. 발견되면 바로 연결할지 묻습니다.
3. 아니면 provider 선택 과정을 진행합니다.
4. model, API key, base URL을 입력받습니다.
5. 다음 실행부터 재사용할 수 있도록 저장합니다.

### One-Shot 실행

```powershell
.\kernforge.exe -prompt "이 프로젝트 구조를 설명해줘"
```

이미지 1장 첨부:

```powershell
.\kernforge.exe -prompt "이 스크린샷의 오류 원인을 설명해줘" -image .\screenshot.png
```

이미지 여러 장 첨부:

```powershell
.\kernforge.exe -prompt "이 두 스크린샷 차이를 비교해줘" -image .\before.png,.\after.png
```

### Provider를 지정해서 실행

Anthropic:

```powershell
$env:ANTHROPIC_API_KEY = "your_key"
.\kernforge.exe -provider anthropic -model claude-sonnet-4
```

OpenAI:

```powershell
$env:OPENAI_API_KEY = "your_key"
.\kernforge.exe -provider openai -model gpt-4.1
```

OpenRouter:

```powershell
$env:OPENROUTER_API_KEY = "your_key"
.\kernforge.exe -provider openrouter -model openrouter/auto
```

Ollama:

```powershell
.\kernforge.exe -provider ollama -base-url http://localhost:11434 -model qwen3.5:14b
```

OpenAI-compatible:

```powershell
$env:OPENAI_API_KEY = "your_key"
.\kernforge.exe -provider openai-compatible -base-url http://localhost:8000/v1 -model my-model
```

대화형 REPL 안에서는 `/provider status`로 현재 provider, 정규화된 `base_url`, API key 설정 여부, provider별 budget visibility를 바로 확인할 수 있습니다.

LM Studio:

```powershell
.\kernforge.exe -provider openai-compatible -base-url http://localhost:1234/v1 -model local-model-id
```

### Windows Security Workflow 예시

Driver 변경을 안전하게 밀어붙이는 가장 기본 흐름:

1. driver 관련 파일을 수정합니다.
2. `/verify`를 실행해 signing, symbol, package, verifier readiness 중심 verification plan을 확인합니다.
3. `/evidence-dashboard` 또는 `/evidence-search category:driver`로 최근 failed evidence를 확인합니다.
4. 필요하면 `/mem-search category:driver`로 이전 세션 맥락까지 확인합니다.
5. push 또는 PR 생성 시 hook policy가 최근 evidence를 다시 보고 경고, 확인, 차단, checkpoint 생성을 수행합니다.

더 공격자 관점까지 포함한 권장 흐름:

1. `/investigate start driver-visibility guard.sys`
2. `/investigate snapshot`
3. `/simulate tamper-surface guard.sys`
4. `/open driver/guard.cpp`
5. `/review-selection integrity risk paths`
6. `/edit-selection harden the selected integrity checks`
7. `/verify`
8. `/evidence-dashboard category:driver`

여기서 `driver-visibility` preset은 드라이버 로드 root cause를 깊게 파고드는 분석기가 아니라, 현재 시점의 드라이버 가시성, verifier 상태, 관련 artifact 존재 여부를 빠르게 남기는 lightweight triage snapshot이다.

이 흐름 전체 설명은 [한국어 상세 사용 가이드](./FEATURE_USAGE_GUIDE_kor.md)에 있습니다.

Telemetry 회귀를 볼 때의 기본 흐름:

1. provider, manifest, XML 관련 파일을 수정합니다.
2. `/verify`를 실행합니다.
3. `/evidence-search category:telemetry outcome:failed`로 최근 provider/XML 실패 흔적을 봅니다.
4. `/mem-search category:telemetry tag:provider`로 과거 세션의 판단과 회귀 맥락을 다시 봅니다.
5. 이후 push/PR 전에 hook이 추가 review context나 확인을 요구할 수 있습니다.

### Specialist Subagent와 Worktree Isolation 활용 예시

`specialists`는 기본적으로 켜져 있고, `worktree_isolation`은 기본적으로 꺼져 있습니다. 아래처럼 워크스페이스 설정을 켜 두면 tracked feature 구현, driver/telemetry/Unreal/memory 계열 고위험 수정, 그리고 서로 다른 ownership을 가진 다중 편집 요청에서 특히 효과가 큽니다.

보통의 웹, 백엔드, 툴링, 앱 기능 개발에서는 `implementation-owner`, `planner`, `reviewer`가 먼저 붙고, driver/telemetry/Unreal/memory 같은 specialist는 task 설명이나 파일 경로가 강하게 맞을 때만 들어온다고 생각하면 됩니다.

```json
{
  "auto_verify": true,
  "specialists": {
    "enabled": true
  },
  "worktree_isolation": {
    "enabled": true,
    "root_dir": "C:\\Users\\you\\.kernforge\\worktrees",
    "branch_prefix": "kernforge/",
    "auto_for_tracked_features": true
  }
}
```

언제 쓰면 좋은가:

1. 일반 기능 개발에서도 `api/handlers.go`, `pkg/cache/store.go`, `web/src/settings.tsx`처럼 서로 다른 파일군을 한 요청 안에서 안전하게 나눠 수정하고 싶을 때
2. tracked feature 구현 중 base workspace를 깨끗하게 유지하면서 rollback 가능한 격리 루프가 필요할 때
3. 자동 배정이 너무 넓게 잡혀서 `implementation-owner` 대신 더 좁은 domain specialist를 고정하고 싶을 때
4. 같은 파일을 반복 수정하면서 verification을 여러 번 다시 돌려 stale bundle을 손으로 정리하고 싶지 않을 때

권장 사용 흐름 1: 일반 기능 개발에서 자동 배정 쓰기

1. `.kernforge/config.json`에서 `worktree_isolation.enabled=true`를 켭니다.
2. `/new-feature start settings page and cache invalidation cleanup`
3. `/new-feature implement`
4. 구현 요청을 concrete path 기준으로 씁니다. 예: `web/src/settings.tsx와 pkg/cache/store.go를 각각 안전하게 수정하고, 설정 저장 플로우와 캐시 invalidation verification도 같이 챙겨줘`
5. Kernforge는 task graph node별로 specialist를 고르고, 각 node에 editable ownership과 lease를 붙입니다.
6. lease가 겹치지 않는 secondary edit node가 있으면 automatic editable worker가 별도 specialist worktree에서 추가 patch를 만들 수 있습니다.
7. 같은 owner 또는 같은 lease에 대해 verification을 다시 시작하면 이전 background verification bundle은 자동 supersede되므로, 오래된 결과를 계속 추적하지 않아도 됩니다.
8. `/tasks`, `/specialists status`, `/worktree status`, `/verify-dashboard`로 현재 routing과 verification 상태를 확인합니다.
9. 구현이 끝나고 isolated worktree가 깨끗하면 `/worktree cleanup`으로 정리합니다.

권장 사용 흐름 2: 도메인 specialist를 수동 고정

1. `/tasks`로 node id를 확인합니다.
2. `/specialists assign plan-02 driver-build-fixer driver/**,*.inf,*.cat`
3. `/specialists assign plan-03 telemetry-analyst telemetry/**,*.man,*.xml`
4. 다시 구현 요청을 이어갑니다.
5. 이후 edit tool과 scoped shell write는 해당 node의 ownership과 specialist worktree 안에서만 허용됩니다.
6. ownership 밖 경로를 쓰려 하면 Kernforge가 재배정 힌트를 보여 주므로, 그때 ownership glob을 넓히거나 다른 specialist로 다시 assign하면 됩니다.

권장 사용 흐름 3: worktree isolation만 먼저 쓰기

1. `/worktree create anti-cheat-hardening`
2. 일반 review/edit/verify 흐름을 진행합니다.
3. 격리 worktree를 남겨 둔 채 base root로 돌아가려면 `/worktree leave`
4. 나중에 다시 들어와 확인하거나 merge 준비를 마친 뒤 `/worktree cleanup`

추가 팁:

1. automatic parallel edit를 잘 유도하려면 요청에 `pkg/cache/store.go`, `web/src/settings.tsx`, `Config/DefaultGame.ini`처럼 concrete path를 직접 적는 것이 좋습니다.
2. 서로 겹치는 경로 둘을 동시에 크게 수정하면 Kernforge는 secondary lane을 defer하고 직렬로 처리합니다. 이것은 race를 막기 위한 의도된 안전장치입니다.
3. `specialists.profiles`로 built-in specialist에 provider/model/ownership_paths를 오버레이할 수 있습니다. 예를 들어 `telemetry-analyst`만 더 강한 모델로 올리거나, `driver-build-fixer`의 ownership을 `package/**`까지 넓히는 식입니다.

### 자주 쓰는 명령 치트시트

검증:
- `/verify`
- `/verify-dashboard`

증거:
- `/evidence`
- `/evidence-search category:driver outcome:failed`
- `/evidence-dashboard`

메모리:
- `/mem-search category:telemetry tag:provider`
- `/mem-dashboard`

정책:
- `/hooks`
- `/hook-reload`

격리 구현:
- `/specialists`
- `/specialists status`
- `/specialists assign <node-id> <specialist> [glob,glob2]`
- `/set-specialist-model <specialist> <provider> [model]`
- `/worktree status`
- `/worktree create [name]`
- `/worktree leave`
- `/worktree cleanup`

## 커맨드라인 옵션

| 옵션 | 설명 |
| --- | --- |
| `-cwd <dir>` | 시작 workspace root 지정 |
| `-provider <name>` | provider 선택 |
| `-model <name>` | model 선택 |
| `-base-url <url>` | provider base URL override |
| `-prompt "<text>"` | 단일 프롬프트 실행 후 종료 |
| `-image <paths>` / `-i` | one-shot 모드에서 이미지 첨부, 쉼표 구분 |
| `-resume <session-id>` | 저장된 세션 재개 |
| `-permission-mode <mode>` | 권한 모드 지정 |
| `-y` | 모든 권한 자동 승인 (`bypassPermissions`) |

참고:

- `-image`는 `-prompt`와 함께 사용해야 합니다.
- `-preview-file`, `-preview-result-file`, `-viewer-file`, `-viewer-result-file`는 내부 창 처리용 옵션입니다.

## 워크스페이스와 설정

### Workspace Root와 Current Directory

Kernforge는 두 가지 위치 개념을 따로 관리합니다.

- workspace root
- REPL 내부 current working directory

workspace root는 시작 시 `-cwd` 또는 현재 프로세스 디렉터리로 정해지며, 파일 도구는 이 범위를 벗어나지 않습니다.

REPL 안에서 `!cd`를 사용하면 current directory만 바뀌고 workspace 경계는 유지됩니다.
상대 경로 기반의 읽기/탐색 도구는 먼저 current directory를 기준으로 찾고, 거기서 찾지 못하면 workspace root를 기준으로 한 번 더 찾습니다.

### 설정 파일 위치

- 전역 설정: `~/.kernforge/config.json`
- 워크스페이스 설정: `.kernforge/config.json`

### 병합 순서

뒤에 오는 항목이 앞선 항목을 덮어씁니다.

1. 전역 설정
2. 워크스페이스 설정
3. 환경 변수
4. 커맨드라인 플래그

### 예시 설정

```json
{
  "provider": "ollama",
  "model": "qwen3.5:14b",
  "base_url": "http://localhost:11434",
  "permission_mode": "default",
  "shell": "powershell",
  "request_timeout_seconds": 1200,
  "max_tool_iterations": 16,
  "auto_compact_chars": 45000,
  "auto_checkpoint_edits": true,
  "auto_verify": true,
  "specialists": {
    "enabled": true
  },
  "worktree_isolation": {
    "enabled": true,
    "root_dir": "C:\\Users\\you\\.kernforge\\worktrees",
    "branch_prefix": "kernforge/",
    "auto_for_tracked_features": true
  },
  "msbuild_path": "C:\\Program Files\\Microsoft Visual Studio\\2022\\Community\\MSBuild\\Current\\Bin\\MSBuild.exe",
  "cmake_path": "C:\\Program Files\\CMake\\bin\\cmake.exe",
  "auto_locale": true,
  "hooks_enabled": true,
  "hooks_fail_closed": false
}
```

### 주요 설정 필드

| 필드 | 설명 |
| --- | --- |
| `provider` | `ollama`, `anthropic`, `openai`, `openrouter`, `openai-compatible` |
| `model` | provider에 전달할 모델 이름 |
| `base_url` | provider API base URL |
| `api_key` | API key |
| `temperature` | 모델 temperature |
| `max_tokens` | completion 최대 토큰 수 |
| `max_request_retries` | transient provider error 또는 timeout 시 모델 요청 재시도 횟수 |
| `request_retry_delay_ms` | 모델 요청 재시도 전 기본 backoff 지연(ms) |
| `request_timeout_seconds` | 모델 요청 timeout 초 단위 설정 |
| `max_tool_iterations` | 요청당 tool loop 최대 반복 수 |
| `permission_mode` | `default`, `acceptEdits`, `plan`, `bypassPermissions` |
| `shell` | `run_shell`에 사용할 셸 |
| `shell_timeout_seconds` | `run_shell` 기본 timeout 초 단위 설정 |
| `read_hint_spans` | `read_file`와 `grep`이 공유하는 cached-nearby 힌트 보존 개수 |
| `read_cache_entries` | `read_file` 메모리 캐시 엔트리 개수 |
| `session_dir` | 세션 JSON 저장 디렉터리 |
| `auto_compact_chars` | 자동 compact를 시도할 대략적 컨텍스트 길이 |
| `auto_checkpoint_edits` | 첫 편집 전에 안전 checkpoint 생성 |
| `auto_verify` | 편집 후 automatic verification의 마스터 스위치 |
| `msbuild_path` | PATH가 불완전할 때 사용할 workspace MSBuild override |
| `cmake_path` | PATH가 불완전할 때 사용할 workspace CMake override |
| `ctest_path` | PATH가 불완전할 때 사용할 workspace CTest override |
| `ninja_path` | PATH가 불완전할 때 사용할 workspace Ninja override |
| `auto_locale` | 시스템 locale을 프롬프트에 자동 주입 |
| `memory_files` | 추가 메모리 파일 경로 |
| `skill_paths` | 추가 skill 탐색 경로 |
| `enabled_skills` | 항상 프롬프트에 주입할 skill |
| `mcp_servers` | MCP 서버 정의 |
| `profiles` | 최근 또는 고정 provider/model profile |
| `hooks_enabled` | hook engine 활성화 여부 |
| `hook_presets` | 워크스페이스에 로드할 hook preset 목록 |
| `hooks_fail_closed` | hook 평가 실패 시 허용 대신 차단할지 여부 |
| `project_analysis` | multi-agent project analysis 설정, 출력 경로, worker/reviewer profile |
| `plan_review` | `/do-plan-review`용 reviewer 모델 설정 |
| `review_profiles` | reviewer profile 저장 목록 |
| `specialists` | specialist subagent 사용 여부와 profile overlay 설정 |
| `worktree_isolation` | isolated git worktree root, branch prefix, tracked feature 자동 격리 설정 |

### 인터랙티브 루프 내구성 메모

- 인터랙티브 루프는 이제 새 요청마다 planner/reviewer preflight를 기본으로 시도한다. 별도 reviewer profile이 없으면 현재 main provider/model로 auxiliary client를 만들어 같은 흐름을 유지한다.
- 상당한 final answer를 반환하기 전에는 reviewer가 한 번 더 `APPROVED / NEEDS_REVISION` 판단을 하고, recovery 중에는 짧은 guidance만이 아니라 실행 plan 자체를 새로 짜도록 갱신할 수 있다.
- 인터랙티브 런타임은 이제 transcript 외에 구조화된 `TaskState`와 지속되는 `TaskGraph`를 함께 유지한다. 그래서 goal, plan progress, pending check, background ownership, 고가치 event가 compact 이후에도 더 안정적으로 남는다.
- task-graph node는 retry budget과 최근 failure context를 함께 가진다. 같은 node에서 실패가 반복되면 그 node를 명시적으로 `blocked`로 올려서, executor가 같은 실패를 무한 반복하지 않고 다른 recovery path를 더 강하게 선택하게 만든다.
- `run_shell`은 이제 `allow_workspace_writes=true`와 `write_paths`를 함께 주면 제한된 workspace shell mutation을 허용한다. formatter, codegen, setup처럼 수동 패치보다 실제 명령 실행이 더 안전한 경우를 위한 경로다.
- 오래 걸리는 build, test, verification 명령은 `run_shell_background`와 `check_shell_job`으로 돌려서 같은 비싼 명령을 다시 시작하지 않고 기존 job을 polling할 수 있다. 동일한 running job이 있으면 자동으로 재사용한다.
- 서로 독립적인 긴 검증 명령은 `run_shell_bundle_background`와 `check_shell_bundle`로 여러 background job을 병렬로 시작하고 함께 polling할 수 있다. bundle 메타데이터도 세션에 저장되므로, compact 이후에도 `bundle_id=\"latest\"`로 이어서 polling할 수 있다.
- background 작업은 이제 node-aware하게 연결된다. 오래 걸리는 verification은 `owner_node_id`와 owner lease를 들고 가고, 같은 owner나 같은 lease의 새 verification bundle은 이전 bundle을 supersede하며, verification-like bundle이 끝나면 owning plan node도 자동으로 완료/재개 상태로 동기화된다.
- secondary executor node는 자동 read-only worker뿐 아니라 automatic editable worker follow-up도 가질 수 있다. disjoint lease에서는 specialist가 별도 worktree에서 추가 patch를 만들고, 그 결과와 verification bundle 요약이 task graph에 다시 적재된다.

### 환경 변수

공통 override:

- `KERNFORGE_PROVIDER`
- `KERNFORGE_MODEL`
- `KERNFORGE_BASE_URL`
- `KERNFORGE_API_KEY`
- `KERNFORGE_PERMISSION_MODE`
- `KERNFORGE_SHELL`
- `KERNFORGE_SESSION_DIR`
- `KERNFORGE_MAX_REQUEST_RETRIES`
- `KERNFORGE_REQUEST_RETRY_DELAY_MS`
- `KERNFORGE_REQUEST_TIMEOUT_SECONDS`
- `KERNFORGE_SHELL_TIMEOUT_SECONDS`
- `KERNFORGE_AUTO_CHECKPOINT_EDITS`
- `KERNFORGE_AUTO_VERIFY`
- `KERNFORGE_AUTO_LOCALE`
- `KERNFORGE_MSBUILD_PATH`
- `KERNFORGE_CMAKE_PATH`
- `KERNFORGE_CTEST_PATH`
- `KERNFORGE_NINJA_PATH`

provider별:

- `ANTHROPIC_API_KEY`
- `OPENAI_API_KEY`
- `OPENROUTER_API_KEY`
- `OLLAMA_HOST`
- `OLLAMA_API_KEY`

## Provider 지원

### Ollama

- 기본 base URL: `http://localhost:11434`
- `OLLAMA_HOST`, `OLLAMA_API_KEY` 사용
- 첫 실행 시 로컬 서버 자동 탐지
- 서버에서 모델 목록 직접 조회

### Anthropic

- 기본 base URL: `https://api.anthropic.com`
- `ANTHROPIC_API_KEY` 사용
- `/provider status`는 live balance를 추정하지 않고 Billing page 가시성과 Usage & Cost Admin API 제약을 함께 보여준다.

### OpenAI

- 기본 base URL: `https://api.openai.com`
- `OPENAI_API_KEY` 사용
- assistant가 tool call만 보낼 때는 빈 assistant content를 보내지 않아 호환성을 높인다.
- JSON이 아닌 assistant tool-call arguments는 전송 전에 정규화한다.
- HTTP 오류 메시지에는 provider 디버깅을 빠르게 하기 위한 compact request preview가 포함된다.
- tool call이 진행 중이 아닐 때는 streamed partial text를 timeout에서도 최대한 살리고, timeout 난 model turn은 한 번 자동 재시도한다.
- `/provider status`는 usage/cost visibility와 rate-limit 단서를 보여주고, exact prepaid balance endpoint가 공식 문서에 없다는 점을 같이 알려준다.

### OpenRouter

- 기본 base URL: `https://openrouter.ai/api/v1`
- `OPENROUTER_API_KEY` 사용
- 대화형 모델 선택기에서 페이지 이동, 필터링, curated 추천, reasoning-only 필터, 정렬 지원
- OpenAI-compatible client와 동일하게 request timeout, partial stream 복구, incomplete stream fallback, transient error/timeout 재시도를 사용한다.
- `/provider status`는 live `/key` 조회로 key-level `limit_remaining`, `usage`를 보여주고 management key면 `/credits`도 함께 조회한다.

### OpenAI-compatible

- OpenAI 스타일 chat completions API 사용
- 별도 지정이 없으면 `OPENAI_API_KEY` 사용
- `base_url`을 명시하는 구성이 일반적
- OpenAI provider와 동일한 tool-call 정규화 및 request preview 진단을 적용한다.
- `/provider status`는 정규화된 endpoint와 key 존재 여부까지는 보여주지만 billing visibility는 upstream provider 구현에 따라 달라진다.

## 메모리

### Memory Files

메모리 파일은 시스템 프롬프트에 프로젝트 가이드로 주입됩니다.

자동 탐색 위치:

- 전역: `~/.kernforge/MEMORY.md`
- 워크스페이스 상위 경로: `.kernforge/KERNFORGE.md`
- 워크스페이스 상위 경로: `KERNFORGE.md`

초기 템플릿 생성:

```text
/init
/init config
/init hooks
/init memory-policy
```

### Persistent Memory

Kernforge는 세션 간 압축된 기억을 저장하고, 이후 세션에서 관련 문맥을 다시 주입할 수 있습니다.

메타데이터:

- citation id
- 날짜
- 세션 이름 또는 id
- provider/model
- 중요도: `low`, `medium`, `high`
- 신뢰도: `tentative`, `confirmed`

관련 명령:

```text
/memory
/mem
/mem-search <query>
/mem-show <id>
/mem-promote <id>
/mem-demote <id>
/mem-confirm <id>
/mem-tentative <id>
/mem-dashboard [query]
/mem-dashboard-html [query]
/mem-prune [all]
/mem-stats
```

## Skills와 MCP

### Skills

시작용 스킬 생성:

```text
/init skill checks
```

관련 명령:

```text
/skills
/reload
```

요청 프롬프트 안에서 `$checks`처럼 쓰면 해당 요청에만 스킬을 활성화할 수 있습니다.

### MCP

Kernforge는 stdio 기반 MCP 서버를 연결하고, 해당 서버의 tool, resource, prompt를 CLI에서 사용할 수 있게 노출합니다.

관련 명령:

```text
/mcp
/resources
/resource <server:uri-or-name>
/prompts
/prompt <server:name> {"arg":"value"}
```

멘션 예시:

```text
@mcp:docs:getting-started 이 리소스를 요약해줘
```

실시간 웹 리서치를 위해 Kernforge는 시작 시 번들된 MCP 스크립트를 `~/.kernforge/mcp/web-research-mcp.js`로 배포하고, 아직 동등한 웹 검색 MCP가 없다면 `~/.kernforge/config.json`에 대응되는 `web-research` 항목도 자동으로 추가합니다. `TAVILY_API_KEY`, `BRAVE_SEARCH_API_KEY`, `SERPAPI_API_KEY`는 셸 환경 변수로 넣어도 되고, `config.json`의 `mcp_servers[].env`에 넣어도 됩니다. 시작 이후에 설정이나 환경 변수를 바꿨다면 `/reload`를 실행하면 됩니다. 이 워크스페이스에는 바로 실행 가능한 `.kernforge/mcp/web-research-mcp.js`와 대응되는 `.kernforge/config.json`도 들어 있습니다. 연결되면 Kernforge는 최신/현재 리서치 요청에서 로컬 파일 검사보다 해당 MCP를 먼저 사용하려고 시도합니다. `/init config`도 스크립트를 찾을 수 있으면 기본 활성 상태로 `web-research` MCP를 넣습니다.

최소 워크스페이스 설정 예시는 다음과 같습니다.

```json
{
  "mcp_servers": [
    {
      "name": "web-research",
      "command": "node",
      "args": [".kernforge/mcp/web-research-mcp.js"],
      "env": {
        "TAVILY_API_KEY": "",
        "BRAVE_SEARCH_API_KEY": "",
        "SERPAPI_API_KEY": ""
      },
      "cwd": ".",
      "capabilities": ["web_search", "web_fetch"]
    }
  ]
}
```

## 대화형 REPL

### 기본 사용

```text
이 저장소 구조를 설명해줘
```

### 유용한 런타임 명령

```text
/config
/context
/provider status
/model
/status
/version
/help
/reload
/hooks
/hook-reload
/override
/specialists
/worktree status
```

- `/status`는 현재 세션과 런타임 상태를 보여준다. 예를 들어 approval 상태, 세션 id, 메모리/검증/MCP 카운트가 여기에 들어간다.
- `/config`는 현재 적용된 설정값을 보여준다. 예를 들어 provider 기본값, token limit, hook/locale/verification 설정이 여기에 들어간다.
- `/provider status`는 active provider, 정규화된 `base_url`, API key 설정 여부, provider별 budget visibility를 보여준다. OpenRouter는 live lookup을 수행하고, OpenAI/Anthropic은 공식 문서 기준의 제약과 billing 안내를 노출한다.
- `/model`은 main 모델, plan-review reviewer, analysis worker/reviewer, specialist subagent 모델 오버라이드를 한 번에 보여주고, 바꾸고 싶은 대상을 골라 해당 설정 흐름으로 들어가는 통합 허브다.

### 대화와 세션 명령

```text
/clear
/compact [focus]
/export [file]
/rename <name>
/resume <session-id>
/session
/sessions
/tasks
```

### Provider와 계획 관련 명령

```text
/provider
/provider status
/model
/profile
/profile-review
/set-plan-review [provider]
/set-analysis-models
/set-specialist-model [status|clear <specialist|all>|<specialist> <provider> [model]]
/analyze-project [--mode map|trace|impact|security|performance] <goal>
/analyze-performance [focus]
/do-plan-review <task>
/new-feature <task>
/specialists
/worktree [status|create [name]|leave|cleanup]
/permissions [mode]
/set-max-tool-iterations <n>
/locale-auto [on|off]
```

- `/model`은 파라미터를 받지 않는다. 현재 모델 라우팅을 먼저 보여준 뒤, interactive 모드에서는 변경할 기능 하나를 고르게 한다.
- `/set-plan-review [provider]`는 plan-review의 reviewer 모델만 바꾼다. planner 쪽은 계속 main 모델을 사용한다.
- `/set-analysis-models`는 project analysis worker/reviewer 프로필을 따로 설정할 때 쓴다.
- `/set-specialist-model ...`은 특정 specialist subagent에만 workspace 단위 모델 override를 줄 때 쓴다.

### 취소와 히스토리

- 입력 중 `Esc`: 현재 입력 취소
- 요청 실행 중 `Esc`: 진행 중인 모델 요청 취소
- Windows에서는 짧게 누른 `Esc`도 요청 취소로 인식되도록 처리한다.
- 요청 취소 직후에는 `Esc` release와 콘솔 입력 버퍼를 정리해서 다음 프롬프트가 자동 취소되지 않게 한다.
- Windows 콘솔의 `Up`, `Down`: 최근 입력 불러오기

### Tab 완성

`Tab` 완성 지원 대상:

- slash command
- command/subcommand 설명이 붙은 completion menu
- `/provider status|anthropic|openai|openrouter|ollama`
- `/analyze-project --mode ...`와 내장 mode 값
- `@file` 멘션
- `/open <path>`
- `/resource <server:...>`
- `/prompt <server:...>`
- `@mcp:server:...`

## Viewer, Selection, Review 워크플로우

별도 텍스트 viewer로 파일 열기:

```text
/open main.go
```

viewer 및 selection 기능:

- Windows용 별도 viewer 창
- 라인 번호와 상태 footer
- 텍스트 선택
- 선택한 라인 범위 기반 prompt prefill
- selection stack 저장
- selection 범위만 대상으로 하는 review/edit 프롬프트
- `/diff`, `/diff-selection`은 Windows에서 read-only 내부 diff viewer를 우선 사용
- 내부 diff viewer는 changed-file navigation, unified/split 전환, intraline highlight를 제공

selection 관련 명령:

```text
/selection
/selections
/use-selection <n>
/drop-selection <n>
/clear-selection
/clear-selections
/note-selection <text>
/tag-selection <tag[,tag2,...]>
/diff-selection
/review-selection [...]
/review-selections [...]
/edit-selection <task>
```

## 셸과 Git

`!`로 셸 명령 실행:

```text
!git status
!go test ./...
```

내장 단축 명령:

```text
!cd src
!ls
!dir
!pwd
!cls
!clear
```

git 관련 명령:

```text
/diff
```

Windows에서는 `/diff`, `/diff-selection`이 내부 WebView2 diff viewer를 우선 사용합니다. 해당 surface를 사용할 수 없으면 터미널 출력으로 fallback합니다.

모델이 사용할 수 있는 전용 git 도구:

- `git_status`
- `git_diff`
- `git_add`
- `git_commit`
- `git_push`
- `git_create_pr`

## 권한 모드

| 모드 | 의미 |
| --- | --- |
| `default` | 읽기는 자동 허용, 쓰기와 셸은 확인 필요 |
| `acceptEdits` | 읽기와 쓰기는 자동 허용, 셸은 확인 필요 |
| `plan` | 읽기 전용 모드 |
| `bypassPermissions` | 모든 작업 자동 허용 |

REPL에서 변경:

```text
/permissions default
/permissions acceptEdits
/permissions plan
/permissions bypassPermissions
```

## Verification, Checkpoint, Rollback

편집이 성공적으로 끝난 뒤에는 자동 verification이 실행될 수 있습니다.

현재 구현된 검증 감지:

- Go: 대상 `go test`와 `go vet ./...`
- Cargo: `cargo check`, `cargo test`
- Node: `npm run typecheck`, `npm run lint`, `npm test`
- CMake: `cmake --build <dir>`와 선택적 `ctest --test-dir <dir>`
- Visual Studio C++: `msbuild <solution-or-project> /m`

관련 명령:

```text
/verify [path,...|--full]
/verify-dashboard [all]
/verify-dashboard-html [all]
/checkpoint [note]
/checkpoint-auto [on|off]
/checkpoint-diff [target] [-- path[,path2]]
/checkpoints
/rollback [target]
/init verify
```

## Evidence, Investigation, Simulation

Kernforge는 이제 evidence 축적, live investigation 상태, risk-oriented simulation을 포함하는 보안 중심 운영 루프를 제공합니다.

evidence 관련 명령:

```text
/evidence
/evidence-search <query>
/evidence-show <id>
/evidence-dashboard [query]
/evidence-dashboard-html [query]
```

investigation 관련 명령:

```text
/investigate [subcommand]
/investigate-dashboard
/investigate-dashboard-html
```

simulation 관련 명령:

```text
/simulate [profile]
/simulate-dashboard
/simulate-dashboard-html
```

source-level fuzzing 관련 명령:

```text
/fuzz-func <function-name>
/fuzz-func <function-name> --file <path>
/fuzz-func @<path>
/fuzz-func status
/fuzz-func show [id|latest]
/fuzz-func list
/fuzz-func continue [id|latest]
/fuzz-func language [system|english]
```

hook 및 override 관련 명령:

```text
/hooks
/hook-reload
/override
/override-add ...
/override-clear ...
```

## Project Analysis

새로 추가된 project analysis 흐름은 큰 코드베이스나 위험도가 높은 변경에서, 즉석 요약이 아니라 재사용 가능한 구조 지식을 쌓기 위한 기능입니다.

핵심 명령:

```text
/analyze-project [--mode map|trace|impact|security|performance] <goal>
/analyze-performance [focus]
/set-analysis-models
```

모드 요약:

- `map`: 기본 아키텍처 맵, subsystem ownership과 module boundary 중심
- `trace`: 실행 경로와 caller/callee 흐름 중심
- `impact`: 변경 영향 범위와 blast radius 중심
- `security`: trust boundary, validation, privileged surface, tamper-sensitive path 중심이며 `driver`, `IOCTL`, `handle`, `memory`, `RPC` 단위로도 분해
- `performance`: startup cost, hot path, blocking chain, contention 중심

무엇을 하는가:

- 워크스페이스를 구조화된 snapshot으로 스캔
- 코드베이스를 analysis shard로 분할
- semantic shard planner로 UE/대규모 코드베이스의 startup, network, UI, GAS, asset/config, integrity 영역을 우선 분리
- conductor와 여러 worker/reviewer 패스를 사용
- structural index와 Unreal semantic graph를 생성
- semantic fingerprint와 structured invalidation diff로 재사용 여부와 재분석 원인을 추적
- Markdown과 JSON 분석 산출물 생성
- 후속 분석용 `latest` knowledge pack 유지
- vector corpus와 provider별 ingestion seed를 생성
- incremental 분석이 켜져 있으면 변경 없는 shard 결과 재사용

주요 출력:

- `.kernforge/analysis/<timestamp>_<goal>.md`
- `.kernforge/analysis/<timestamp>_<goal>.json`
- `.kernforge/analysis/<timestamp>_<goal>_snapshot.json`
- `.kernforge/analysis/<timestamp>_<goal>_structural_index.json`
- `.kernforge/analysis/<timestamp>_<goal>_structural_index_v2.json`
- `.kernforge/analysis/<timestamp>_<goal>_unreal_graph.json`
- `.kernforge/analysis/<timestamp>_<goal>_knowledge.md`
- `.kernforge/analysis/<timestamp>_<goal>_knowledge.json`
- `.kernforge/analysis/<timestamp>_<goal>_performance_lens.md`
- `.kernforge/analysis/<timestamp>_<goal>_performance_lens.json`
- `.kernforge/analysis/<timestamp>_<goal>_vector_corpus.json`
- `.kernforge/analysis/<timestamp>_<goal>_vector_corpus.jsonl`
- `.kernforge/analysis/<timestamp>_<goal>_vector_ingest_manifest.json`
- `.kernforge/analysis/<timestamp>_<goal>_vector_ingest_records.jsonl`
- `.kernforge/analysis/<timestamp>_<goal>_vector_pgvector.sql`
- `.kernforge/analysis/<timestamp>_<goal>_vector_sqlite.sql`
- `.kernforge/analysis/<timestamp>_<goal>_vector_qdrant.jsonl`
- `.kernforge/analysis/latest/`

권장 흐름:

1. `/analyze-project anti-cheat startup and integrity architecture`를 실행합니다.
2. 생성된 knowledge pack과 shard 산출물을 확인합니다.
3. `/analyze-performance startup` 또는 `scanner`, `compression`, `upload`, `ETW`, `memory` 같은 focus로 후속 분석을 실행합니다.
4. 결과를 `/review-selection`, `/edit-selection`, `/verify`, evidence 기반 hook policy에 연결합니다.

## Source-Level Function Fuzzing

`/fuzz-func`는 런타임 harness가 없어도 공격자가 입력 파라미터를 정교하게 조작했을 때 어떤 guard, probe, copy, dispatch, cleanup 경로가 열리는지를 소스만으로 추론하는 기능입니다.

핵심 명령:

```text
/fuzz-func <function-name>
/fuzz-func <function-name> --file <path>
/fuzz-func <function-name> @<path>
/fuzz-func --file <path>
/fuzz-func @<path>
/fuzz-func status
/fuzz-func show [id|latest]
/fuzz-func continue [id|latest]
/fuzz-func language [system|english]
```

무엇을 하는가:

- 함수 시그니처, 실제 함수 본문의 guard/probe/copy/dispatch/cleanup 관찰, 호출 closure를 함께 모은다.
- 함수명만 주면 심볼을 자동 찾고, 파일만 주면 include/import와 실제 호출 흐름을 따라 대표 루트를 고른다.
- `analyze-project`가 없어도 snapshot과 semantic index를 on-demand로 복원한다.
- 위험도가 높은 경로에 대해 공격자 입력 상태, 구체 입력 예시, 소스에서 뽑은 비교식, 최소 반례, 분기별 대표 결과, 후속 호출 체인을 합성한다.
- 고위험 시나리오는 위험도 점수표와 함께 먼저 볼 소스 줄, 시작 파일에서 그 파일까지 이어진 경로, 대표 루트에서 이어진 호출 경로를 보여준다.
- build context가 충분하면 후속 네이티브 harness/run까지 연결하고, 부족하면 왜 막히는지 설명한 뒤 확인을 받는다.
- `/fuzz-func ` 자동완성은 함수명과 파일 지정 예시를 먼저 보여주고, `@` 이후에는 실제 파일 후보 목록으로 바뀐다.

이 기능이 특히 좋은 경우:

1. IOCTL handler, parser, validator, buffer-processing 함수처럼 공격자 입력이 직접 들어가는 코드를 빨리 triage하고 싶을 때
2. 단순 리뷰보다 더 구체적으로 "어떤 값으로 어떤 비교식을 뒤집으면 어느 sink가 열리는지" 보고 싶을 때
3. large driver/project에서 파일 하나를 지정하고 그 파일이 실제로 끌어오는 input-facing 경로를 보고 싶을 때

출력 해석 요약:

1. `결론`은 가장 우선 확인할 예측 문제와 가장 유용한 분기 차이 요약을 먼저 보여준다.
2. `위험도 점수표`는 noise가 많은 fallback을 아래로 내리고, 실제 guard/probe/copy 근거가 있는 finding을 위로 올린다.
3. `상위 예측 문제`는 Kernforge가 내부적으로 가정한 입력 상태와 구체 입력 예시를 보여주며, 사용자가 그대로 수동 재현하라는 뜻은 아니다.
4. `소스 기반 공격 표면`은 실제 함수 본문에서 뽑은 probe/copy/dispatch/cleanup 근거를 요약한다.

실무 추천 흐름:

1. 파일 단위로 거칠게 보고 싶으면 `/fuzz-func @Driver/Foo.c`
2. 함수까지 알고 있으면 `/fuzz-func ValidateRequest --file src/guard.cpp`
3. 결과에서 가장 높은 점수의 finding과 `가장 유용한 분기 차이 요약`, `먼저 볼 관련 소스`부터 확인
4. 필요하면 더 안쪽 input-facing 함수로 다시 `/fuzz-func`를 걸어 source-level fuzzing 정밀도를 높인다.

## 참고

- 별도 텍스트 viewer 창과 WebView2 diff surface는 주로 Windows 환경에 맞춰 구현되어 있습니다.
- WebView2 diff surface 초기화가 실패하면 workflow에 따라 브라우저 기반 preview 또는 터미널 출력으로 fallback합니다.
- CLI 핵심, 세션, provider, memory, skills, MCP, verification 로직은 가능한 범위에서 이식성을 유지하도록 구성되어 있습니다.
