# KernForge MCP Server Mode 사용 가이드

이 문서는 Codex 앱서버나 다른 MCP 호스트가 KernForge를 stdio MCP 서버로 호출하도록 설치하고 사용하는 방법을 정리한다.

KernForge MCP server mode의 목적은 KernForge REPL을 직접 열지 않고도 외부 agent가 다음 기능을 도구처럼 호출하게 하는 것이다.

1. 현재 workspace 상태 확인
2. 최신 project analysis 문서와 dashboard artifact 조회
3. evidence store 검색
4. adaptive verification 계획 확인 및 실행
5. multi-agent project analysis 실행
6. 증상 기반 root-cause analysis 실행
7. fuzz target catalog 조회, source-level function fuzz 계획, campaign seed 승격 및 native result capture

## 0. Codex 앱에서 바로 테스트하는 가장 짧은 절차

Codex 앱에서 KernForge MCP를 쓰려면 KernForge가 Codex의 전역 MCP server 목록에 등록되어 있어야 한다. 현재 열린 대화창은 시작 시점의 tool 목록을 쓰는 경우가 많으므로, 등록 후에는 새 스레드를 열거나 Codex 앱을 재시작하는 것이 가장 확실하다.

아래 화면처럼 `codex mcp add`, `codex mcp list`, 새 Codex 스레드 테스트 순서로 확인한다.

![Codex MCP registration screenshot](docs/assets/codex-mcp-registration.png)

PowerShell에서 한 번만 실행:

```powershell
cd C:\git\kernforge
go build -o .\kernforge.exe ./cmd/kernforge
codex mcp add kernforge -- C:\git\kernforge\kernforge.exe -mcp-server
codex mcp list
codex mcp get kernforge
```

상시 구동 daemon을 쓰고 싶으면 다음 방식이 더 좋다. Codex가 여전히 stdio MCP 프로세스를 띄우지만, 그 프로세스는 얇은 proxy로 동작하고 실제 KernForge 상태와 cache는 daemon이 유지한다.

```powershell
cd C:\git\kernforge
go build -o .\kernforge.exe ./cmd/kernforge
.\kernforge.exe daemon start
codex mcp add kernforge -- C:\git\kernforge\kernforge.exe -mcp-server -mcp-daemon-proxy
codex mcp get kernforge
```

daemon 상태 관리는 다음 명령을 쓴다.

```powershell
.\kernforge.exe daemon status
.\kernforge.exe daemon stop
```

정상 등록 예:

```text
Name       Command                         Args         Status
kernforge  C:\git\kernforge\kernforge.exe  -mcp-server  enabled
```

등록 상세 확인:

```powershell
codex mcp get kernforge
```

Codex 앱에서 `MCP client for kernforge timed out after 30 seconds`가 나오면 `C:\Users\<user>\.codex\config.toml`의 서버 항목에 다음 두 줄을 추가한다.

```toml
[mcp_servers.kernforge]
command = 'C:\git\kernforge\kernforge.exe'
args = ["-mcp-server"]
startup_timeout_sec = 120
```

daemon proxy 등록이면 args는 다음처럼 보인다.

```toml
[mcp_servers.kernforge]
command = 'C:\git\kernforge\kernforge.exe'
args = ["-mcp-server", "-mcp-daemon-proxy"]
startup_timeout_sec = 120
```

그 다음 Codex 앱에서 새 스레드를 열고 다음처럼 요청한다.

```text
KernForge MCP로 kernforge_guide를 호출해서 내가 뭘 하려는지 물어봐줘. 아직 native 실행은 하지 마.
```

함수명이나 파일만 던져보고 싶을 때는 더 짧게 요청해도 된다.

```text
KernForge로 IsValidCommand 봐줘.
```

이 경우 Codex는 먼저 기본 진입점인 `kernforge` tool을 호출하는 것이 좋다. `kernforge`는 분석, 빌드, fuzz를 실행하지 않고 KernForge가 다음 선택지를 되묻도록 만든다.

목적이 fuzz라면 더 명확하게 다음처럼 말하는 편이 낫다.

```text
KernForge로 IsValidCommand 퍼징해줘.
```

이 경우 Codex는 shell, `rg`, `git status`, 로컬 파일 읽기보다 `kernforge_fuzz`를 먼저 호출한다. 기본 mode는 `source`라서 source-level fuzz plan만 만들고, 컴파일/native build/native fuzz 실행은 시작하지 않는다. tool 호출 전 안내 문장은 최소화한다. “KernForge source-level fuzzing을 실행하겠다” 정도만 말하고, `KernForge 도구를 확인`, `실제 위치를 찾겠다`, `정의와 호출부를 찾겠다`, `테스트 구조를 보겠다`, `퍼징 대상만 좁게 건드리겠다` 같은 표현은 쓰지 않는다. Codex가 실수로 범용 `kernforge` router를 먼저 호출해도 KernForge는 명시적 fuzz 요청을 같은 source-only 경로로 라우팅한다. source-level 결과를 요약한 뒤에는 다음 선택지로 `native_preview`, `build_only`, 사용자 승인 후 runtime fuzzing 순서를 자연스럽게 추천할 수 있다.

Codex 앱에서 정상적으로 source-level fuzzing이 끝나면 아래처럼 `Result`, `Top candidate`, `Problem location`, `Trigger conditions KernForge generated`, `Artifacts`가 먼저 보인다. 영어로 받고 싶으면 프롬프트에 `Answer in English.`를 붙이면 된다.

![Codex App source-level fuzz result](docs/assets/codex-app-source-fuzz-result.png)

`kernforge_fuzz` 응답에 `source_only=true` 또는 `stop_after_response=true`가 있으면 Codex는 결과를 요약하고 멈춘다. 이때 반드시 `fuzz_result.meaningful_result`를 확인한다. 값이 `true`면 `fuzz_result.meaningful_results`에서 유의미한 결과가 무엇인지 요약하고, `false`면 유의미한 fuzz 결과가 없었다고 명시한 뒤 `no_meaningful_results_reason`을 짧게 설명한다. fuzz 종류와 상관없이 `meaningful_result=true` 또는 `must_report_problem_code_and_trigger_values=true`이면 Codex는 `problem_code_location`의 정확한 파일/라인, `problem_code`의 문제가 되는 코드 snippet, `trigger_values`의 문제가 되는 값/조건을 반드시 보여준다. KernForge가 어느 한쪽을 unavailable로 표시하면 값을 지어내지 말고 unavailable 사유를 그대로 말한다. 응답에 `Highlighted result labels`가 있으면 `Result`, `Top candidate`, `Problem location`, `Trigger conditions KernForge generated`, `Artifacts` 라벨을 Markdown-safe 노란색/주황색 square 라벨로 사용하고, raw HTML span은 쓰지 않는다. 기본 응답에서도 사용자가 나중에 확인할 수 있도록 `artifact_paths` 안의 `artifact_dir`, `report_path`, `plan_path`, `harness_path`를 모두 보여준다. `artifact_dir`만 보여주는 답변은 불완전하다. 결과 요약 뒤에는 선택 가능한 다음 단계로 native/runtime fuzzing 경로를 추천한다. 단, source-only fuzz 답변 앞에서 도구 확인, 로컬 소스 확인, 테스트 구조 확인, 수정 계획, 후속 작업 진행을 예고하지 말고, 사용자가 명시적으로 요청하기 전에는 생성된 report/harness 내용을 다시 읽거나, `kernforge_fuzz_func_preview`, `kernforge_fuzz_func_build`, `kernforge_fuzz_func execute=true`, `kernforge_fuzz_campaign_run`, shell 후속 명령을 호출하지 않는다. 사용자가 source-level fuzz 산출물 내용을 자세히 보고 싶다고 하면 `kernforge_fuzz_artifacts`를 `id=latest` 또는 run id와 함께 호출하고, `artifact=overview|report|plan|harness|all` 중 필요한 항목을 선택한다. Codex가 별도 custom harness를 만들거나 임의 PowerShell/Python semantic runner를 실행해서는 안 된다. 사용자가 코드 수정을 요청했거나 실제 소스 파일을 수정한 경우가 아니면 "소스 코드는 수정하지 않았습니다" 같은 수정 여부 문장을 덧붙이지 않는다.

추천 테스트 프롬프트:

```text
KernForge MCP로 request="IsValidCommand를 fuzz하고 싶어. 먼저 안전하게 볼 수 있는 것만 보여줘"로 kernforge_guide를 호출하고, 부족한 입력이 있으면 질문만 보여줘.
```

```text
KernForge MCP로 현재 workspace 상태를 확인하고, verification history와 persistent memory를 읽은 뒤, 이번 변경에 필요한 kernforge_verify execute=false 계획을 만들어줘.
```

```text
KernForge MCP로 fuzz target catalog를 확인하고, 가장 위험한 target 하나에 대해 kernforge_fuzz_func execute=false source-level fuzz plan만 만들어줘. native 실행은 하지 마.
```

주의할 점:

1. 사용자가 직접 `mcp__kernforge__...` 같은 내부 tool 이름을 입력할 필요는 없다. Codex에게 `kernforge_status`나 `kernforge_fuzz_targets`를 쓰라고 말하면 모델이 MCP tool을 선택한다.
2. 이 문서를 수정 중인 기존 대화창에서는 새로 등록한 MCP tool이 바로 보이지 않을 수 있다. 새 Codex 스레드에서 테스트한다.
3. 등록을 다시 하고 싶으면 `codex mcp remove kernforge` 후 `codex mcp add ...`를 다시 실행한다.
4. `kernforge_analyze_project`, `kernforge_find_root_cause`는 provider/model과 API key가 필요하다. 연결 확인, memory/history/evidence/resource 읽기, fuzz plan, verify plan은 provider 없이도 테스트할 수 있다.
5. Codex에서는 `-cwd`와 MCP config의 `cwd`를 고정하지 않는 것을 권장한다. 그래야 MCP process가 현재 Codex workspace에서 시작되고, KernForge가 프로젝트별 재등록 없이 현재 workspace를 따라간다.

가장 쉬운 사용법은 `kernforge_guide`를 먼저 호출하는 것이다. 이 도구는 실행형 작업을 시작하지 않고, 사용자의 자연어 요청에서 intent를 추정한 뒤 부족한 항목을 `questions`로 되묻고 안전한 다음 tool call을 `recommended_tool_call`로 제안한다.

## 1. 전제 조건

필요한 것:

1. Windows PowerShell
2. Go toolchain
3. KernForge repository
4. MCP 호스트 또는 Codex 앱서버
5. analysis/root-cause 도구를 쓸 경우 provider/model 설정

예시 경로는 이 문서에서 `C:/git/kernforge`를 사용한다. JSON 설정에서는 Windows backslash escaping을 피하려고 `/` 형식 경로를 권장한다.

## 2. 빌드

PowerShell에서:

```powershell
cd C:\git\kernforge
go build -o .\kernforge.exe ./cmd/kernforge
```

빌드 결과:

```text
C:\git\kernforge\kernforge.exe
```

MCP server mode 실행 명령은 다음 형태다.

```powershell
C:\git\kernforge\kernforge.exe -mcp-server
```

주의:

1. 이 명령은 일반 REPL이 아니다.
2. 직접 터미널에서 실행하면 프롬프트 없이 대기하는 것이 정상이다.
3. stdout은 MCP JSON-RPC frame 전용이므로 사람이 읽는 로그를 출력하지 않는다.
4. 실제 사용은 MCP 호스트가 process를 띄우고 `initialize`, `tools/list`, `tools/call`을 보내는 방식이다.

Transport 호환성:

1. KernForge MCP server는 `Content-Length: N\r\n\r\n{json}` 방식의 header-framed JSON-RPC를 지원한다.
2. Codex 앱/일부 MCP client에서 쓰는 newline-delimited JSON-RPC, 즉 `{json}\n` frame도 지원한다.
3. 서버는 client가 보낸 frame 방식을 감지하고 같은 방식으로 응답한다. 따라서 Codex 쪽에서 JSON-line initialize를 보내도 startup timeout 없이 응답해야 한다.

Workspace 선택:

1. Codex처럼 MCP process를 현재 프로젝트 cwd에서 시작하는 host에서는 `-cwd`를 생략한다. 이 경우 KernForge는 process cwd를 workspace로 사용한다.
2. Host가 `initialize.params.rootUri`, `rootPath`, `workspaceFolders`, `roots`, `initializationOptions` 안에 workspace를 전달하면 KernForge는 그 값을 우선 사용한다.
3. `-cwd <path>`는 fallback 또는 특정 workspace에 고정하고 싶을 때만 사용한다.
4. `kernforge_status`의 `workspace`와 `mcp_workspace_source`로 실제 선택된 workspace와 출처를 확인한다.

## 3. MCP 호스트 등록

Codex 앱서버 또는 MCP client 설정에 다음 서버를 등록한다.

Codex 앱에서 직접 테스트할 때는 위 스크린샷처럼 `codex mcp add kernforge -- ...`로 전역 등록하는 방식이 가장 단순하다. 등록이 끝나면 새 Codex 스레드를 열고 `kernforge_status`를 먼저 호출해 달라고 요청한다.

Codex의 실제 전역 설정 파일은 보통 `C:\Users\<user>\.codex\config.toml`이다. Codex에서는 프로젝트별 재등록을 피하기 위해 `cwd`와 `-cwd`를 고정하지 않는다.

```toml
[mcp_servers.kernforge]
command = 'C:\git\kernforge\kernforge.exe'
args = ["-mcp-server"]
startup_timeout_sec = 120
```

```json
{
  "name": "kernforge",
  "command": "C:/git/kernforge/kernforge.exe",
  "args": ["-mcp-server"],
  "capabilities": ["project_analysis", "security_verification", "evidence", "memory", "fuzzing"]
}
```

provider/model을 명령행에서 고정하고 싶으면:

```json
{
  "name": "kernforge",
  "command": "C:/git/kernforge/kernforge.exe",
  "args": [
    "-mcp-server",
    "-provider",
    "openai",
    "-model",
    "replace-with-model"
  ],
  "capabilities": ["project_analysis", "security_verification", "evidence", "memory", "fuzzing"]
}
```

기존 KernForge session의 provider/model을 재사용하려면:

```json
{
  "name": "kernforge",
  "command": "C:/git/kernforge/kernforge.exe",
  "args": [
    "-mcp-server",
    "-resume",
    "SESSION_ID"
  ],
  "capabilities": ["project_analysis", "security_verification", "evidence", "memory", "fuzzing"]
}
```

특정 workspace에 강제로 고정해야 하는 host라면 그때만 다음처럼 fallback을 둔다. 이 방식은 Codex에서 여러 프로젝트를 오갈 때는 권장하지 않는다.

```toml
[mcp_servers.kernforge]
command = 'C:\git\kernforge\kernforge.exe'
args = ["-mcp-server", "-cwd", 'C:\git\some-project']
cwd = 'C:\git\some-project'
startup_timeout_sec = 120
```

운영 주의:

1. 같은 KernForge workspace의 `.kernforge/config.json`에 자기 자신을 MCP server로 다시 등록하지 않는 것이 좋다.
2. KernForge 안에서 KernForge MCP server를 재귀적으로 띄우는 구조는 디버깅 목적이 아니라면 피한다.
3. Codex 앱서버, Claude Desktop, MCP Inspector 같은 외부 MCP 호스트에서 실행 대상으로 등록하는 것이 기본 형태다.

## 4. Provider 및 API key 설정

다음 도구는 모델 provider 없이도 사용할 수 있다.

1. `kernforge`
2. `kernforge_fuzz`
3. `kernforge_guide`
4. `kernforge_look`
5. `kernforge_status`
6. `kernforge_review`의 deterministic/no_model 리뷰
7. `kernforge_latest_analysis`
8. `kernforge_read_analysis_doc`
9. `kernforge_evidence_search`
10. `kernforge_memory_search`
11. `kernforge_verification_history`
12. `kernforge_analysis_context`
13. `kernforge_artifact_index`
14. `kernforge_fuzz_targets`
15. `kernforge_fuzz_func`
16. `kernforge_fuzz_func_preview`
17. `kernforge_fuzz_func_build`
18. `kernforge_fuzz_func_status`
19. `kernforge_fuzz_artifacts`
20. `kernforge_fuzz_campaign_status`
21. `kernforge_fuzz_campaign_run`
22. `kernforge_verify`의 계획 확인

다음 도구는 provider/model이 필요하다.

1. `kernforge_analyze_project`
2. `kernforge_find_root_cause`
3. model-backed `kernforge_review`

`kernforge_review`는 read-only tool이다. `no_model=true` 또는 provider가 없는 degraded 상태에서는 deterministic reviewer와 gate metadata 중심으로 응답할 수 있지만, 실제 모델 기반 finding 품질을 기대하는 운영 리뷰에는 provider/model을 설정하는 것이 좋다.

설정 방법은 세 가지다.

1. KernForge config에 provider/model 저장
2. MCP server args에 `-provider`, `-model`, `-base-url` 전달
3. `-resume <session-id>`로 기존 session provider/model 재사용

OpenAI 예시:

```powershell
$env:OPENAI_API_KEY = "replace-me"
```

OpenRouter 예시:

```powershell
$env:OPENROUTER_API_KEY = "replace-me"
```

Ollama 예시:

```json
{
  "name": "kernforge",
  "command": "C:/git/kernforge/kernforge.exe",
  "args": [
    "-mcp-server",
    "-provider",
    "ollama",
    "-model",
    "qwen2.5-coder:14b"
  ]
}
```

## 5. 연결 확인

MCP 호스트에서 먼저 tool list를 확인한다.

기대되는 tools:

```text
kernforge_status
kernforge_review
kernforge_latest_analysis
kernforge_read_analysis_doc
kernforge_evidence_search
kernforge_memory_search
kernforge_verification_history
kernforge_analysis_context
kernforge_artifact_index
kernforge
kernforge_fuzz
kernforge_guide
kernforge_look
kernforge_fuzz_targets
kernforge_source_scan
kernforge_source_candidate_list
kernforge_source_candidate_show
kernforge_fuzz_workflow
kernforge_fuzz_func
kernforge_fuzz_func_preview
kernforge_fuzz_func_build
kernforge_fuzz_func_status
kernforge_fuzz_artifacts
kernforge_fuzz_campaign_status
kernforge_fuzz_campaign_run
kernforge_verify
kernforge_analyze_project
kernforge_find_root_cause
tool_search
```

`tool_search`는 시작 시 전체 schema를 올리지 않은 deferred MCP tool의 입력 schema를 on-demand로 가져오는 discovery tool이다. `server_name`, `tool_name_filter`로 좁혀서 live tools/list를 짧은 timeout으로 호출하고 결과 schema를 client에 cache한다.

첫 호출은 `kernforge_status`를 권장한다.

Tool call:

```json
{
  "name": "kernforge_status",
  "arguments": {}
}
```

Raw MCP JSON-RPC payload로 보면 다음과 같은 형태다.

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "kernforge_status",
    "arguments": {}
  }
}
```

정상 응답에는 workspace, session id, provider/model 상태, git changed files, evidence count, 최신 analysis 정보가 포함된다. 리뷰/수정 작업 중이면 runtime gate 상태, latest review freshness, blocker/warning count, waiver count, 추천 next command도 같이 확인한다.

MCP host가 discovery를 지원하면 다음도 확인한다.

```text
ping
resources/templates/list
resources/list
prompts/list
```

`resources/templates/list`에는 query/id를 경로에 넣어 읽을 수 있는 template resource가 포함된다. 예를 들어 `kernforge://memory/search/{query}`와 `kernforge://analysis/context/{query}`는 tool call 없이도 context 주입용으로 바로 읽기 좋다.

## 6. 기본 사용 순서

처음 붙였을 때 추천 순서:

```text
kernforge_status
kernforge_review
kernforge_latest_analysis
kernforge_verification_history
kernforge_memory_search
kernforge_evidence_search
kernforge_analysis_context
kernforge_fuzz_targets
kernforge_fuzz_func execute=false
kernforge_fuzz_campaign_run execute=false
kernforge_verify execute=false
kernforge_verify execute=true
kernforge_fuzz_campaign_run execute=true
kernforge_analyze_project 또는 kernforge_find_root_cause
```

실전에서는 verification이나 native fuzz execution을 바로 실행하기 전에 `execute=false`로 계획을 먼저 보는 것이 좋다.

## 7. Tool 예시

### 7.1 상태 확인

```json
{
  "name": "kernforge_status",
  "arguments": {}
}
```

용도:

1. MCP server가 정상 연결됐는지 확인
2. provider/model이 준비됐는지 확인
3. workspace와 git 상태 확인
4. 최신 analysis artifact 존재 여부 확인
5. review freshness와 runtime gate blocker 확인

리뷰나 write-side 작업을 앞둔 client는 `kernforge_status`의 runtime gate 요약을 stop sign으로 취급한다. `runtime_gate=blocked`, stale review, waiver 없는 blocker가 보이면 final answer, git write, PR write-side automation을 진행하기 전에 `next_command`를 먼저 사용자에게 제안하거나 `kernforge_review`를 다시 호출한다.

#### 7.1.1 리뷰 하네스 실행

코드/계획/PR/selection/analysis target 리뷰는 `kernforge_review`를 직접 호출한다. 이 tool은 CLI `/review`와 같은 common review harness를 쓰며 read-only다.

```json
{
  "name": "kernforge_review",
  "arguments": {
    "request": "Review the current change before final answer.",
    "target": "change",
    "include_git_diff": true,
    "response_format": "both"
  }
}
```

MCP 응답에는 structured finding, gate status, `scope_discovery`, `latest_review_freshness`, `edit_proposals`, `runtime_gate_ledger`, `next_commands`, `recommended_command`, `.kernforge/reviews` artifact path가 포함된다. `next_commands`는 reason, when, safety, auto-run 가능 여부, confirmation 필요 여부, expected_result를 포함하는 action contract다. `latest_review_freshness`가 stale이거나 `runtime_gate_ledger`가 blocked이면 client는 완료를 주장하지 말고 표시된 recovery command를 처리한다.

review model 품질 gate는 provider behavior에 따라 token cap, omission retry budget, schema strictness, recovery prompt를 정한다. weak 또는 근거가 부족한 high-severity model finding은 gate blocker로 쓰지 않고 evidence-gap warning으로 낮춘다.

외부 client가 이미 diff나 파일 범위를 알고 있으면 shell `git diff`를 다시 읽지 말고 다음처럼 전달한다.

```json
{
  "name": "kernforge_review",
  "arguments": {
    "request": "Review the supplied patch for correctness regressions.",
    "target": "change",
    "paths": ["cmd/kernforge/review_harness.go"],
    "diff": "diff --git a/cmd/kernforge/review_harness.go b/cmd/kernforge/review_harness.go\n...",
    "include_git_diff": false
  }
}
```

단순 exact edit을 실제 write로 이어가야 하는 host는 review 응답의 `edit_proposals`와 runtime gate freshness를 보존한 뒤, 로컬 agent tool surface에서는 `apply_edit_proposal`을 우선 사용하고 복잡한 hunk-level 변경에만 `apply_patch`를 fallback으로 둔다.

### 7.2 최신 분석 요약

```json
{
  "name": "kernforge_latest_analysis",
  "arguments": {
    "max_chars": 24000
  }
}
```

특정 generated document까지 같이 보고 싶으면:

```json
{
  "name": "kernforge_latest_analysis",
  "arguments": {
    "document": "SECURITY_SURFACE.md",
    "max_chars": 30000
  }
}
```

최신 분석이 없으면 먼저 `kernforge_analyze_project`를 실행한다.

### 7.3 분석 문서 읽기

```json
{
  "name": "kernforge_read_analysis_doc",
  "arguments": {
    "document": "INDEX.md",
    "max_chars": 30000
  }
}
```

자주 읽는 문서:

```text
INDEX.md
ARCHITECTURE.md
SECURITY_SURFACE.md
API_AND_ENTRYPOINTS.md
BUILD_AND_ARTIFACTS.md
VERIFICATION_MATRIX.md
FUZZ_TARGETS.md
OPERATIONS_RUNBOOK.md
DEVELOPER_OVERVIEW.md
FOLDER_MAP.md
MODULES.md
STRUCTURE_DIAGRAMS.md
CODE_STRUCTURE_REFERENCE.md
```

### 7.4 Evidence 검색

최근 evidence:

```json
{
  "name": "kernforge_evidence_search",
  "arguments": {
    "limit": 12
  }
}
```

Driver 실패 evidence:

```json
{
  "name": "kernforge_evidence_search",
  "arguments": {
    "query": "category:driver outcome:failed",
    "limit": 20
  }
}
```

Telemetry 관련 evidence:

```json
{
  "name": "kernforge_evidence_search",
  "arguments": {
    "query": "telemetry provider manifest",
    "limit": 20
  }
}
```

고위험 evidence:

```json
{
  "name": "kernforge_evidence_search",
  "arguments": {
    "query": "severity:high",
    "limit": 20
  }
}
```

### 7.5 Persistent memory 검색

과거 결정, 성공/실패한 verification, evidence-backed 판단을 workspace 범위에서 검색한다.

```json
{
  "name": "kernforge_memory_search",
  "arguments": {
    "query": "driver ioctl fuzz verification",
    "limit": 8
  }
}
```

Dashboard 형태로 보고 싶으면:

```json
{
  "name": "kernforge_memory_search",
  "arguments": {
    "dashboard": true,
    "limit": 12
  }
}
```

### 7.6 Verification history 확인

최근 검증 성공/실패, 반복 실패 check, failure kind를 확인한다.

```json
{
  "name": "kernforge_verification_history",
  "arguments": {
    "limit": 8
  }
}
```

특정 태그 중심:

```json
{
  "name": "kernforge_verification_history",
  "arguments": {
    "tags": ["fuzz", "driver"],
    "limit": 12,
    "include_json": true
  }
}
```

### 7.7 최신 analysis context pack

모델을 새로 호출하지 않고, 저장된 최신 analysis artifact에서 질문 중심 context pack을 만든다.

```json
{
  "name": "kernforge_analysis_context",
  "arguments": {
    "query": "IOCTL handler 변경 영향과 verification/fuzz gate",
    "max_chars": 30000
  }
}
```

이 도구는 최신 `knowledge_pack.json` 또는 `run.json`이 있을 때 가장 유용하다. 저장된 분석이 없으면 먼저 `kernforge_analyze_project`를 실행한다.

### 7.8 Artifact index

Codex 같은 외부 agent가 실제 산출물 경로를 찾아 열 수 있도록 주요 artifact 경로를 JSON으로 반환한다.

```json
{
  "name": "kernforge_artifact_index",
  "arguments": {
    "max_recent": 5
  }
}
```

반환 범위에는 latest analysis directory, dashboard, docs manifest, evidence store, persistent memory, verification history, function fuzz runs, fuzz campaigns가 포함된다.

### 7.8.1 Guided MCP entrypoint

어떤 KernForge tool을 써야 할지 모를 때는 `kernforge_guide`를 먼저 호출한다. 이 도구는 자연어 request를 받아 intent를 추정하고, 부족한 항목은 `questions`로 되묻고, 다음에 호출할 안전한 tool을 `recommended_tool_call`로 제안한다.

사용자가 `KernForge로 IsValidCommand 봐줘`처럼 함수명이나 파일명만 던진 경우에는 기본 진입점인 `kernforge` tool을 먼저 호출한다. 이 도구는 실행형 작업을 절대 시작하지 않고, `kernforge_guide`의 target-only 질의 모드로 연결해 사용자에게 다음 행동을 고르게 한다. MCP host가 look 전용 라우팅을 이미 갖고 있으면 `kernforge_look`도 같은 안전한 first-step 흐름을 제공한다.

```json
{
  "name": "kernforge_guide",
  "arguments": {
    "request": "IsValidCommand를 fuzz하고 싶어. 먼저 안전하게 볼 수 있는 것만 보여줘",
    "execution_mode": "preview"
  }
}
```

예상 응답 형태:

```json
{
  "state": "ready",
  "intent": "fuzz",
  "safe_default": "preview_or_plan_only",
  "recommended_tool_call": {
    "name": "kernforge_fuzz_func_preview",
    "arguments": {
      "query": "IsValidCommand",
      "include_plan": false
    }
  }
}
```

정보가 부족하면 `state=needs_input`과 함께 `questions`가 반환된다. 이 질문에 답한 뒤 `kernforge_guide`를 다시 호출하면 된다.

사용자가 `KernForge로 ...`라고 시작하거나 KernForge 사용을 요청하면 기본 진입점인 `kernforge` tool로 먼저 보낸다. 함수명이나 파일명만 말한 경우도 우선 `kernforge`로 보내는 것이 가장 자연스럽다. 직접 `kernforge_guide`로 보내도 같은 target-only 흐름을 탄다. 이 경우 KernForge는 바로 실행하지 않고 `ask_user`, `choices`, `codex_instruction`을 반환한다. 응답에 `stop_after_response=true`가 있으면 Codex는 여기서 멈추고, `ask_user`와 `choices`만 사용자에게 보여준 뒤 답변을 기다려야 한다. 이 단계에서는 shell로 로컬 파일을 읽거나 `kernforge_status`, `kernforge_analysis_context`, `kernforge_analyze_project`, `kernforge_verify`, fuzz 관련 tool을 추가 호출하지 않는다.

예:

```json
{
  "name": "kernforge",
  "arguments": {
    "request": "KernForge로 IsValidCommand 봐줘",
    "file": "src/driver/IoctlDispatch.cpp"
  }
}
```

응답은 대략 다음 형태다.

```json
{
  "state": "needs_input",
  "intent": "target_only",
  "stop_after_response": true,
  "requires_user_choice": true,
  "ask_user": "KernForge로 IsValidCommand --file src/driver/IoctlDispatch.cpp에 대해 무엇을 할까요?",
  "codex_instruction": "STOP here. Ask the user the ask_user question and present choices only.",
  "choices": [
    {
      "id": "preview",
      "label": "안전하게 가능 여부만 보기",
      "recommended_tool_call": {
        "name": "kernforge_fuzz_func_preview"
      }
    },
    {
      "id": "build_only",
      "label": "컴파일까지만 확인",
      "recommended_tool_call": {
        "name": "kernforge_guide",
        "arguments": {
          "execution_mode": "build_only"
        }
      }
    }
  ]
}
```

### 7.9 Fuzz target catalog 확인

최신 `FUZZ_TARGETS.md`/manifest에서 target 후보를 구조화해서 본다.

```json
{
  "name": "kernforge_fuzz_targets",
  "arguments": {
    "query": "ioctl parser",
    "limit": 12,
    "include_doc": false,
    "max_chars": 30000
  }
}
```

문서 본문까지 같이 보고 싶으면:

```json
{
  "name": "kernforge_fuzz_targets",
  "arguments": {
    "include_doc": true,
    "max_chars": 50000
  }
}
```

최신 analysis에 fuzz target catalog가 없으면 `kernforge_analyze_project`를 `security` 또는 `surface` 모드로 먼저 실행하거나, 이미 알고 있는 함수/파일로 `kernforge_fuzz_func`를 직접 호출한다.

### 7.10 Function fuzz 계획 생성

소스 레벨 function fuzz plan을 만들고 `.kernforge/fuzz/<run-id>/` 아래에 `plan.json`, `report.md`, `harness.cpp`를 저장한다.

```json
{
  "name": "kernforge_fuzz_func",
  "arguments": {
    "query": "ValidateRequest --file src/guard.cpp",
    "source_scan": "focused",
    "execute": false,
    "max_chars": 50000
  }
}
```

`source_scan`은 `focused`, `full`, `off`를 받는다. 기본값인 `focused`는 저장된 source candidate가 target과 맞으면 재사용하고, 없으면 target/file scope/reachable file만 source-scan으로 훑어 function fuzz plan에 연결한다. `full`은 indexed workspace 전체를 스캔하고, `off`는 candidate 재사용과 자동 source-scan을 모두 끈다.

CLI와 같은 명시 handoff가 필요하면 query에 `--from-candidate <candidate-id>`를 넣는다.

```json
{
  "name": "kernforge_fuzz_func",
  "arguments": {
    "query": "--from-candidate sc-0123456789abcdef",
    "execute": false
  }
}
```

응답 summary에는 source candidate context가 있으면 `source_candidate_id`, `source_matcher_slug`, `source_scan_mode`, `source_scan_run_id`, `source_scan_summary`가 포함된다. 이 필드는 source-only finding을 campaign/native feedback으로 이어갈 때 원래 source matcher signal을 되찾는 데 사용한다.

### 7.11 Structured source scan workflow

MCP client가 source candidate를 직접 고르고 이어가야 할 때는 `kernforge_source_scan`, `kernforge_source_candidate_list`, `kernforge_source_candidate_show`, `kernforge_fuzz_workflow`를 사용할 수 있다. 이 응답은 텍스트 로그가 아니라 `candidate_id`, `matcher_slug`, `confidence_breakdown`, `evidence_spans`, `dataflow_facts`, `controlflow_facts`, `stale`, `next_command`, `next_tool_call`, `artifact_paths`를 JSON으로 제공한다.

```json
{
  "name": "kernforge_source_scan",
  "arguments": {
    "action": "run",
    "limit": 50,
    "only_slugs": ["probe-copy-size-drift", "double-fetch-user-buffer"]
  }
}
```

```json
{
  "name": "kernforge_fuzz_workflow",
  "arguments": {
    "candidate_id": "sc-0123456789abcdef",
    "source_scan": "focused",
    "execute": false
  }
}
```

`kernforge_fuzz_workflow`는 candidate가 없으면 source scan을 먼저 실행해 가장 강한 후보를 고르고, 있으면 바로 `/fuzz-func --from-candidate <candidate-id>`와 같은 handoff를 수행한다. Native 실행은 `execute=true`와 필요한 경우 `approve_recovered_build=true`가 있을 때만 시도한다.

파일만 주고 대표 fuzz root를 자동 선택하게 할 수도 있다.

```json
{
  "name": "kernforge_fuzz_func",
  "arguments": {
    "query": "@Driver/dispatch.cpp",
    "execute": false
  }
}
```

`execute=false`는 native background fuzz를 시작하지 않는다. source-only scenario, risk-ranked finding, harness, native 실행 가능성만 만든다.

native 실행 가능 여부, build command, run command, missing settings만 안전하게 보고 싶으면 `kernforge_fuzz_func_preview`를 사용한다. 이 도구는 background fuzz를 절대 시작하지 않고, function fuzz history에도 저장하지 않는다. 정확한 인자 배열이 필요하면 응답의 `build_argv`, `run_argv`를 보고, 사람이 검토할 때는 `build_command`, `run_command`를 보면 된다.

```json
{
  "name": "kernforge_fuzz_func_preview",
  "arguments": {
    "query": "ValidateRequest --file src/guard.cpp",
    "max_chars": 20000
  }
}
```

preview는 build/run command가 실제 artifact 경로를 가리키도록 `.kernforge/fuzz/<run-id>/` 아래에 harness/report 같은 preview artifact는 생성할 수 있다. 다만 `history_saved=false`, `continue_command_available=false`이면 function fuzz run history에 저장된 실행은 아니므로 상태 추적이나 continue 대상으로 취급하지 않는다.

native fuzz 실행 전에 컴파일만 확인하려면 `kernforge_fuzz_func_build`를 사용한다. 이 도구는 fuzz executable을 실행하지 않고 harness build만 시도한다.

```json
{
  "name": "kernforge_fuzz_func_build",
  "arguments": {
    "query": "ValidateRequest --file src/guard.cpp",
    "approve_recovered_build": false,
    "timeout_sec": 120
  }
}
```

빌드 설정이 partial/heuristic이면 `approve_recovered_build=false` 상태에서는 실제 컴파일도 시작하지 않고 `pending_confirmation`으로 멈춘다. preview의 `build_argv`, `missing_settings`를 확인한 뒤 컴파일만 허용하려면:

```json
{
  "name": "kernforge_fuzz_func_build",
  "arguments": {
    "query": "ValidateRequest --file src/guard.cpp",
    "approve_recovered_build": true,
    "timeout_sec": 120
  }
}
```

성공해도 `native_execution_started=false`이며, 다음 단계는 `build_log`와 생성된 executable만 확인하는 것이다.

### 7.11 Function fuzz native 실행

생성된 계획이 exact compile context를 갖고 있고 compiler가 확인되면 native smoke fuzz를 background job으로 시작할 수 있다.

```json
{
  "name": "kernforge_fuzz_func",
  "arguments": {
    "query": "ValidateRequest --file src/guard.cpp",
    "execute": true
  }
}
```

빌드 설정이 partial/heuristic이면 기본적으로 실행하지 않고 confirmation-needed 상태로 둔다. 이 설정을 검토한 뒤 강제로 허용하려면:

```json
{
  "name": "kernforge_fuzz_func",
  "arguments": {
    "query": "ValidateRequest --file src/guard.cpp",
    "execute": true,
    "approve_recovered_build": true
  }
}
```

주의:

1. `execute=true`는 background build/fuzz job을 시작할 수 있다.
2. `approve_recovered_build=true`는 compile context가 완전하지 않은 상태에서도 실행을 허용한다.
3. 실제 실행 여부가 헷갈리면 `kernforge_fuzz_func_preview`로 build command, run command, missing settings를 먼저 검토한다.
4. native fuzz 전에 컴파일만 보고 싶으면 `kernforge_fuzz_func_build`를 사용한다.

### 7.12 Function fuzz 상태 확인

최근 run 목록:

```json
{
  "name": "kernforge_fuzz_func_status",
  "arguments": {
    "limit": 8
  }
}
```

특정 run:

```json
{
  "name": "kernforge_fuzz_func_status",
  "arguments": {
    "id": "latest",
    "max_chars": 50000
  }
}
```

### 7.13 Fuzz campaign 상태와 자동 진행

먼저 campaign이 무엇을 할지 계획만 본다.

```json
{
  "name": "kernforge_fuzz_campaign_run",
  "arguments": {
    "execute": false
  }
}
```

실제로 campaign을 만들고 최신 function fuzz run을 붙이며 source-only scenario를 corpus seed artifact로 승격하려면:

```json
{
  "name": "kernforge_fuzz_campaign_run",
  "arguments": {
    "execute": true,
    "name": "driver ioctl campaign"
  }
}
```

상태 확인:

```json
{
  "name": "kernforge_fuzz_campaign_status",
  "arguments": {
    "id": "latest",
    "max_chars": 50000
  }
}
```

Campaign run은 다음 일을 자동화한다.

1. campaign이 없으면 생성
2. 최신 `kernforge_fuzz_func` run attach
3. source-only scenario를 corpus seed JSON으로 승격
4. background/native fuzz 결과가 있으면 campaign result와 evidence로 capture
5. coverage gap과 finding lifecycle을 갱신

### 7.14 Verification 계획 확인

```json
{
  "name": "kernforge_verify",
  "arguments": {
    "mode": "adaptive",
    "execute": false
  }
}
```

특정 path 중심으로 계획:

```json
{
  "name": "kernforge_verify",
  "arguments": {
    "mode": "adaptive",
    "paths": [
      "Driver/guard.cpp",
      "Telemetry/provider.man"
    ],
    "execute": false
  }
}
```

### 7.15 Verification 실행

```json
{
  "name": "kernforge_verify",
  "arguments": {
    "mode": "adaptive",
    "execute": true
  }
}
```

전체 검증:

```json
{
  "name": "kernforge_verify",
  "arguments": {
    "mode": "full",
    "execute": true
  }
}
```

주의:

1. `execute=true`는 실제 build/test/verification command를 실행할 수 있다.
2. MCP server mode는 interactive approval을 받을 수 없으므로 trusted workspace에서만 실행한다.
3. Windows build tool 경로가 필요한 경우 일반 KernForge REPL에서 `/verify tools detect` 또는 `/verify tools set msbuild`를 먼저 설정해 두는 것이 좋다.

### 7.16 Project analysis 실행

Security surface 분석:

```json
{
  "name": "kernforge_analyze_project",
  "arguments": {
    "mode": "security",
    "goal": "Review kernel/user-mode telemetry and anti-cheat security boundaries",
    "paths": ["."],
    "max_total_shards": 12,
    "max_chars": 30000
  }
}
```

Architecture map:

```json
{
  "name": "kernforge_analyze_project",
  "arguments": {
    "mode": "map",
    "goal": "Build a reusable architecture map for driver, service, telemetry, and verification flows",
    "max_total_shards": 16
  }
}
```

Impact 분석:

```json
{
  "name": "kernforge_analyze_project",
  "arguments": {
    "mode": "impact",
    "goal": "Analyze the impact of recent telemetry and process protection changes",
    "paths": [
      "src/telemetry",
      "src/protection"
    ],
    "max_total_shards": 10
  }
}
```

분석 산출물:

```text
.kernforge/analysis/latest/run.json
.kernforge/analysis/latest/dashboard.html
.kernforge/analysis/latest/docs_manifest.json
.kernforge/analysis/latest/docs/
```

### 7.17 Root-cause analysis 실행

좋은 problem 입력은 component, trigger/repro, observed failure, expected invariant를 포함한다.

```json
{
  "name": "kernforge_find_root_cause",
  "arguments": {
    "problem": "After enabling telemetry collection, the service intermittently misses process creation events. Trigger: launch protected processes rapidly during service startup. Observed: only some launches produce normalized events. Expected invariant: every protected process launch should produce one normalized event.",
    "max_total_shards": 8,
    "max_refinement_shards": 6,
    "max_chars": 30000
  }
}
```

Windows service 예시:

```json
{
  "name": "kernforge_find_root_cause",
  "arguments": {
    "problem": "The Win32 service does not stop after sc stop. Trigger: start service, send sc stop while worker thread is processing device events. Observed: service remains running and device close hangs. Expected invariant: stop should cancel pending work and close should return."
  }
}
```

Unreal 예시:

```json
{
  "name": "kernforge_find_root_cause",
  "arguments": {
    "problem": "Party size limit can be bypassed after inviting and kicking players repeatedly. Trigger: invite, kick, reinvite sequence. Observed: party accepts more members than configured limit. Expected invariant: server-authoritative party member count must never exceed MaxPartySize."
  }
}
```

Pattern pack을 추가로 넣고 싶으면:

```json
{
  "name": "kernforge_find_root_cause",
  "arguments": {
    "problem": "The driver unload path sometimes leaves a handle close pending forever. Trigger: unload while user-mode client is connected. Observed: close blocks. Expected invariant: unload cancels pending IRPs and all handles close.",
    "pattern_pack_paths": [
      "C:/git/kernforge/.kernforge/root-cause-patterns/driver_patterns.json"
    ]
  }
}
```

## 8. Resources

MCP host가 resources를 지원하면 다음 URI를 읽을 수 있다.

```text
kernforge://status
kernforge://analysis/latest
kernforge://analysis/context
kernforge://analysis/context/<url-encoded-query>
kernforge://analysis/latest/manifest
kernforge://analysis/latest/run
kernforge://evidence/recent
kernforge://memory/recent
kernforge://memory/search/<url-encoded-query>
kernforge://verification/history
kernforge://artifacts/index
kernforge://fuzz/targets
kernforge://fuzz/function-runs/recent
kernforge://fuzz/function-runs/<id>
kernforge://fuzz/campaign/latest
kernforge://fuzz/campaign/<id>
kernforge://analysis/latest/docs/INDEX.md
kernforge://analysis/latest/docs/SECURITY_SURFACE.md
kernforge://analysis/latest/docs/FUZZ_TARGETS.md
```

사용 예:

```text
@mcp:kernforge:kernforge://analysis/latest
@mcp:kernforge:kernforge://analysis/latest/docs/SECURITY_SURFACE.md
@mcp:kernforge:kernforge://analysis/context/driver%20ioctl%20verification
@mcp:kernforge:kernforge://memory/search/fuzz%20campaign
@mcp:kernforge:kernforge://verification/history
@mcp:kernforge:kernforge://fuzz/targets
@mcp:kernforge:kernforge://fuzz/campaign/latest
```

Resource는 agent prompt context에 최신 KernForge 분석 문서, persistent memory, verification history, fuzz state를 직접 주입할 때 유용하다.

Resource template discovery를 지원하는 host에서는 다음 template도 노출된다.

```text
kernforge://analysis/latest/docs/{name}
kernforge://analysis/context/{query}
kernforge://memory/search/{query}
kernforge://fuzz/function-runs/{id}
kernforge://fuzz/campaign/{id}
```

## 9. Prompts

서버는 다음 MCP prompt를 제공한다.

```text
kernforge-security-review
kernforge-root-cause
kernforge-fuzz-plan
kernforge-verify-with-memory
```

Security review prompt:

```json
{
  "name": "kernforge-security-review",
  "arguments": {
    "focus": "driver IOCTL and user-mode telemetry boundary"
  }
}
```

Root-cause prompt:

```json
{
  "name": "kernforge-root-cause",
  "arguments": {
    "problem": "The service misses process creation telemetry during startup bursts."
  }
}
```

Fuzz planning prompt:

```json
{
  "name": "kernforge-fuzz-plan",
  "arguments": {
    "focus": "driver IOCTL parser surface"
  }
}
```

Verification with memory prompt:

```json
{
  "name": "kernforge-verify-with-memory",
  "arguments": {
    "focus": "driver IOCTL parser change and fuzz gate"
  }
}
```

## 10. Codex 앱서버에서 추천 프롬프트

MCP server를 붙인 뒤 Codex 쪽에는 이런 식으로 요청하면 좋다.

```text
KernForge MCP를 사용해서 현재 workspace 상태를 확인하고, 최신 security surface 문서를 읽은 뒤, 이번 변경에 필요한 verification plan을 먼저 제안해줘.
```

```text
KernForge의 최신 analysis docs와 evidence를 근거로 driver/user-mode boundary를 리뷰해줘. 위험한 경로는 kernforge_verify execute=false로 계획만 먼저 확인해.
```

```text
KernForge MCP에서 verification history와 persistent memory를 먼저 확인한 뒤, 이번 변경에 맞는 verify plan을 만들어줘. 과거 실패 패턴이 있으면 계획에 반영해.
```

```text
아래 증상에 대해 KernForge root-cause analysis를 실행하고, 결과에서 evidence file/function, causal chain, verification probe만 요약해줘:
서비스 stop 이후 device close가 반환되지 않는다. Trigger는 user client 연결 상태에서 service stop. Expected는 pending work cancel 후 close 반환.
```

```text
KernForge MCP로 최신 FUZZ_TARGETS catalog를 확인하고, 가장 위험한 IOCTL/parser target 하나를 골라 kernforge_fuzz_func execute=false로 source-only fuzz plan을 만들어줘. native 실행은 build/run command를 검토한 뒤에만 제안해.
```

## 11. 문제 해결

### 실행했는데 아무 출력이 없음

정상일 수 있다. `-mcp-server`는 REPL이 아니라 stdio MCP server다. MCP 호스트가 JSON-RPC frame을 보내야 응답한다.

### tools/list에 KernForge tools가 안 보임

확인할 것:

1. `command`가 실제 `kernforge.exe` 경로인지 확인
2. `args`에 `-mcp-server`가 있는지 확인
3. Codex에서는 `cwd`와 `-cwd`를 고정하지 않는 것을 권장한다. 고정되어 있으면 다른 프로젝트에서 workspace mismatch가 난다.
4. 고정 fallback이 필요한 host라면 `cwd`와 `-cwd`가 실제 workspace인지 확인한다.
5. 경로는 JSON에서 `C:/git/kernforge` 형태로 쓰는 것을 권장

### MCP startup timeout이 발생함

Codex 앱에서 다음 경고가 나오면 startup timeout을 늘린다.

```text
MCP client for `kernforge` timed out after 30 seconds.
Add or adjust `startup_timeout_sec` in your config.toml.
```

`C:\Users\<user>\.codex\config.toml`:

```toml
[mcp_servers.kernforge]
command = 'C:\git\kernforge\kernforge.exe'
args = ["-mcp-server"]
startup_timeout_sec = 120
```

그 다음 Codex 앱에서 새 스레드를 열거나 앱을 재시작한다. 직접 확인하려면:

```powershell
codex mcp get kernforge
```

출력에 `args: -mcp-server`, `cwd: -`, `startup_timeout_sec: 120`이 보이면 Codex용 동적 workspace 설정이 반영된 것이다.

120초로 늘려도 계속 timeout이 나면 먼저 최신 바이너리로 다시 빌드한다. 오래된 바이너리는 일부 MCP client가 보내는 JSON-line frame을 기다리지 못하고 `Content-Length` header만 기다리다가 timeout처럼 보일 수 있다.

```powershell
cd C:\git\kernforge
go build -o .\kernforge.exe ./cmd/kernforge
codex mcp get kernforge
```

KernForge binary 자체를 빠르게 확인하려면 JSON-line smoke를 실행한다. 정상이라면 `tool_count=16`, `has_status=True`, `contains_header=False`가 보인다.

```powershell
$p=New-Object Diagnostics.Process
$p.StartInfo.FileName='C:\git\kernforge\kernforge.exe'
$p.StartInfo.Arguments='-mcp-server'
$p.StartInfo.WorkingDirectory='C:\git\kernforge'
$p.StartInfo.UseShellExecute=$false
$p.StartInfo.RedirectStandardInput=$true
$p.StartInfo.RedirectStandardOutput=$true
[void]$p.Start()
$p.StandardInput.WriteLine('{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}')
$p.StandardInput.Flush()
$initRaw=$p.StandardOutput.ReadLine()
$p.StandardInput.WriteLine('{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}')
$p.StandardInput.Flush()
$toolsRaw=$p.StandardOutput.ReadLine()
$p.StandardInput.Close()
$tools=$toolsRaw | ConvertFrom-Json
"tool_count=$($tools.result.tools.Count) has_status=$($toolsRaw.Contains('kernforge_status')) contains_header=$($initRaw.Contains('Content-Length'))"
```

설정과 binary smoke가 모두 정상인데 Codex 앱에서만 실패하면 Codex 앱을 완전히 종료한 뒤 새로 열고, 새 스레드에서 다시 테스트한다. 기존 스레드는 시작 시점의 MCP tool 목록을 유지하는 경우가 있다.

### workspace mismatch가 발생함

`kernforge_status`의 `workspace`가 현재 Codex 프로젝트가 아니라 `C:\git\kernforge` 같은 고정 경로로 나오면, Codex MCP 설정에 예전 `-cwd` 또는 `cwd`가 남아 있는 것이다.

해결:

```powershell
codex mcp remove kernforge
codex mcp add kernforge -- C:\git\kernforge\kernforge.exe -mcp-server
```

그리고 `C:\Users\<user>\.codex\config.toml`의 KernForge 항목이 다음 형태인지 확인한다.

```toml
[mcp_servers.kernforge]
command = 'C:\git\kernforge\kernforge.exe'
args = ["-mcp-server"]
startup_timeout_sec = 120
```

정상이라면 `codex mcp get kernforge`에서 `cwd: -`가 보이고, 새 Codex 스레드의 `kernforge_status`는 현재 프로젝트 경로를 `workspace`로 보고한다. `mcp_workspace_source`가 `fallback`이어도 Codex가 MCP process를 현재 프로젝트 cwd에서 시작했다면 정상이다. `initialize.params.rootUri`나 `initialize.params.workspaceFolders[0].uri`로 나오면 host가 workspace를 initialize 단계에서 명시적으로 전달한 것이다.

### analysis tool이 provider/model 오류를 냄

`kernforge_analyze_project`, `kernforge_find_root_cause`는 model provider가 필요하다.

해결 방법:

1. MCP args에 `-provider`, `-model` 추가
2. 필요한 API key 환경 변수 설정
3. 기존 session을 쓰려면 `-resume <session-id>` 추가
4. 일반 KernForge REPL에서 provider 설정 후 다시 MCP server 실행

### latest analysis가 없다고 나옴

아직 `.kernforge/analysis/latest`가 없다.

해결 방법:

1. `kernforge_analyze_project` 실행
2. 또는 일반 REPL에서 `/analyze-project` 실행
3. 이후 `kernforge_latest_analysis` 또는 resource read 재시도

### fuzz target catalog가 없다고 나옴

최신 analysis manifest에 `fuzz_targets`가 없다.

해결 방법:

1. `kernforge_analyze_project`를 `security` 또는 `surface` 모드로 실행
2. `kernforge_read_analysis_doc`로 `FUZZ_TARGETS.md` 존재 여부 확인
3. 이미 target을 알고 있으면 `kernforge_fuzz_func`를 직접 실행

예:

```json
{
  "name": "kernforge_fuzz_func",
  "arguments": {
    "query": "ValidateRequest --file src/guard.cpp",
    "execute": false
  }
}
```

### native fuzz 실행이 시작되지 않음

`kernforge_fuzz_func`는 기본적으로 `execute=false`면 source-only plan만 만든다. `execute=true`여도 compile context가 partial/heuristic이면 `approve_recovered_build=true` 없이는 background fuzz를 시작하지 않는다.

확인할 것:

1. `kernforge_fuzz_func_preview`, `kernforge_fuzz_func_build`, 또는 `kernforge_fuzz_func_status`로 `auto_exec`, `build_command`, `run_command`, `missing_settings` 항목 확인
2. compiler 경로와 `compile_commands.json` 확인
3. build settings가 안전하면 `approve_recovered_build=true`로 재실행

### verification이 build tool을 못 찾음

일반 KernForge REPL에서 먼저:

```text
/verify tools detect
```

자동 탐지가 실패하면:

```text
/verify tools set msbuild "C:\Program Files\Microsoft Visual Studio\2022\Community\MSBuild\Current\Bin\MSBuild.exe"
```

그 다음 MCP server를 재시작한다.

### execute=true가 부담스러움

먼저 계획만 확인한다.

```json
{
  "name": "kernforge_verify",
  "arguments": {
    "mode": "adaptive",
    "execute": false
  }
}
```

계획이 안전하고 필요한 명령만 포함하는 것을 확인한 뒤 `execute=true`로 바꾼다.

## 12. 운영상 권장값

권장 기본:

```json
{
  "name": "kernforge",
  "command": "C:/git/kernforge/kernforge.exe",
  "args": ["-mcp-server"],
  "capabilities": ["project_analysis", "security_verification", "evidence", "memory", "fuzzing"]
}
```

큰 workspace에서 분석 shard 수를 제한하고 싶으면 tool call마다 `max_total_shards`를 넣는다.

```json
{
  "name": "kernforge_analyze_project",
  "arguments": {
    "mode": "security",
    "goal": "Review driver and telemetry trust boundaries",
    "max_total_shards": 8
  }
}
```

Fuzz는 기본적으로 계획부터 확인한다.

```json
{
  "name": "kernforge_fuzz_func",
  "arguments": {
    "query": "TargetFunction --file src/input.cpp",
    "execute": false
  }
}
```

그 다음 campaign seed 승격도 먼저 계획만 본다.

```json
{
  "name": "kernforge_fuzz_campaign_run",
  "arguments": {
    "execute": false
  }
}
```

MCP server mode는 noninteractive execution path다. 따라서 자동 실행이 가능한 MCP 호스트에는 trusted workspace와 trusted client만 연결하는 것이 맞다.
