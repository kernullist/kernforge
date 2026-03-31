# Kernforge

`Kernforge`는 Go로 작성된 터미널 기반 AI 코딩 CLI입니다. 이 저장소는 다음과 같은 로컬 우선 개발 흐름에 초점을 맞춥니다.

- 대화형 REPL
- one-shot 프롬프트 모드
- 파일 검색, 패치, 셸, Git 도구
- 세션 저장, 재개, 대화 내보내기
- 프로젝트 메모리 파일과 세션 간 persistent memory
- 로컬 `SKILL.md` 스킬
- stdio 기반 MCP 서버
- Windows용 별도 텍스트 뷰어와 diff preview 창
- 자동 검증, 체크포인트, 롤백
- 별도 리뷰어 모델을 사용하는 plan review 워크플로

Windows 환경에서 특히 쓰기 좋게 다듬어져 있지만, 핵심 구조는 다른 플랫폼도 고려해 유지됩니다.

## 주요 기능

- 지원 provider: `ollama`, `anthropic`, `openai`, `openrouter`
- 추가 alias: `openai-compatible`
- 입력 방식:
  - 대화형 REPL
  - `-prompt` 기반 one-shot 실행
  - `-image`, `-i`, `@image.png` 이미지 첨부
  - `@main.go`, `@main.go:120-150` 같은 파일/라인 범위 멘션
  - `@mcp:docs:getting-started` 같은 MCP 리소스 멘션
- 편집 흐름:
  - 명시적 파일 멘션이 없을 때 자동 코드 scouting
  - 편집 적용 전 diff preview
  - 편집 후 자동 verification
  - workspace checkpoint / rollback
  - `/open`에서 이어지는 selection 중심 review/edit 워크플로
- 사용성:
  - Windows 콘솔에서 `Up` / `Down` 입력 히스토리
  - slash command, 경로, 멘션, MCP 대상에 대한 `Tab` 완성
  - 현재 입력 또는 실행 중 요청을 `Esc`로 취소
- 지속성:
  - 세션 저장 및 재개
  - 최근 provider/model profile 저장
  - importance/trust 메타데이터를 가진 persistent memory
  - verification, selection 정보까지 포함한 Markdown transcript export
- 확장성:
  - `SKILL.md` 기반 로컬 스킬
  - MCP tools / resources / prompts
- 계획 수립:
  - planner 모델과 reviewer 모델을 분리해서 돌리는 plan review 기능

## 빠른 시작

### 빌드

```powershell
go build -o kernforge.exe .
```

### 실행

```powershell
.\kernforge.exe
```

처음 실행할 때 provider/model 설정이 없으면 Kernforge는 다음 순서로 진행할 수 있습니다.

1. 로컬 Ollama 서버 감지 시도
2. 연결 여부 확인
3. 아니면 provider 선택 유도
4. model, API key, base URL 입력
5. 다음 실행을 위해 설정 저장

### One-shot Prompt Mode

```powershell
.\kernforge.exe -prompt "이 프로젝트 구조를 설명해줘"
```

이미지 1장 첨부:

```powershell
.\kernforge.exe -prompt "이 스크린샷의 오류 원인을 설명해줘" -image .\screenshot.png
```

이미지 여러 장 첨부:

```powershell
.\kernforge.exe -prompt "이 두 스크린샷을 비교해줘" -image .\before.png,.\after.png
```

### Provider와 Model을 직접 지정해서 실행

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

## 커맨드 라인 옵션

| 옵션 | 설명 |
| --- | --- |
| `-cwd <dir>` | 시작 workspace root 지정 |
| `-provider <name>` | provider 선택 |
| `-model <name>` | model 선택 |
| `-base-url <url>` | provider base URL override |
| `-prompt "<text>"` | 단일 프롬프트 실행 후 종료 |
| `-image <paths>` / `-i` | one-shot 모드에서 이미지 1개 이상 첨부, 쉼표 구분 |
| `-resume <session-id>` | 저장된 세션 재개 |
| `-permission-mode <mode>` | 권한 모드 지정 |
| `-y` | 모든 권한 자동 승인 (`bypassPermissions`) |

참고:

- `-image`는 `-prompt`와 함께만 동작합니다.
- 대화형 모드에서는 `@path/to/image.png`로도 이미지를 붙일 수 있습니다.

## Workspace Root와 Working Directory

Kernforge에는 두 위치 개념이 있습니다.

- workspace root
- REPL 안의 현재 working directory

workspace root는 시작 시 `-cwd` 또는 프로세스 현재 디렉터리로 정해집니다. 파일 도구는 이 root 밖으로 나가지 않습니다.

REPL 안에서 `!cd`는 현재 작업 디렉터리만 바꾸고 root 경계는 유지합니다.

## 지원 Provider

### Ollama

- 기본 base URL: `http://localhost:11434`
- `OLLAMA_HOST`, `OLLAMA_API_KEY` 사용
- 첫 실행 시 로컬 서버 감지 지원
- 서버에서 모델 목록 직접 조회

### Anthropic

- 기본 base URL: `https://api.anthropic.com`
- `ANTHROPIC_API_KEY` 사용

### OpenAI

- 기본 base URL: `https://api.openai.com`
- `OPENAI_API_KEY` 사용

### OpenRouter

- 기본 base URL: `https://openrouter.ai/api/v1`
- `OPENROUTER_API_KEY` 사용
- 대화형 모델 선택기에서 페이지 이동, 필터, curated 추천, reasoning-only 필터, 정렬 지원

## 설정

### 전역 설정 위치

Windows:

- `~/.kernforge/config.json`

macOS/Linux:

- `~/.kernforge/config.json`

### 워크스페이스 설정 위치

- `.kernforge/config.json`

### 병합 순서

뒤에 오는 항목이 앞 항목을 덮어씁니다.

1. global config
2. workspace config
3. 환경 변수
4. 커맨드 라인 플래그

### 예시

```json
{
  "provider": "ollama",
  "model": "qwen3.5:14b",
  "base_url": "http://localhost:11434",
  "permission_mode": "default",
  "shell": "powershell",
  "max_tool_iterations": 16,
  "auto_compact_chars": 45000,
  "auto_checkpoint_edits": true,
  "auto_verify_docs_only": false,
  "auto_locale": true
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
| `max_tool_iterations` | 요청당 tool-call loop 최대 반복 수 |
| `permission_mode` | `default`, `acceptEdits`, `plan`, `bypassPermissions` |
| `shell` | `run_shell`에 사용할 셸 |
| `session_dir` | 세션 JSON 저장 디렉터리 |
| `auto_compact_chars` | 자동 compact를 시도할 대략적 컨텍스트 길이 |
| `auto_checkpoint_edits` | 요청 내 첫 편집 전에 안전 체크포인트 1회 생성 |
| `auto_verify_docs_only` | `false`면 docs-only 변경은 자동 검증에서 제외, `true`면 문서 변경도 자동 검증 대상 가능 |
| `auto_locale` | 감지된 시스템 locale 언어로 답변하도록 자동 지시 |
| `memory_files` | 추가 memory 파일 경로 |
| `skill_paths` | 추가 skill 탐색 경로 |
| `enabled_skills` | 항상 system prompt에 주입할 skill |
| `mcp_servers` | MCP 서버 정의 |
| `profiles` | 최근/고정 provider profile |
| `plan_review` | `/do-plan-review`용 별도 reviewer 모델 설정 |
| `review_profiles` | plan review용 reviewer profile 저장 목록 |

### 환경 변수

공통 override:

- `KERNFORGE_PROVIDER`
- `KERNFORGE_MODEL`
- `KERNFORGE_BASE_URL`
- `KERNFORGE_API_KEY`
- `KERNFORGE_PERMISSION_MODE`
- `KERNFORGE_SHELL`
- `KERNFORGE_SESSION_DIR`
- `KERNFORGE_AUTO_CHECKPOINT_EDITS`
- `KERNFORGE_AUTO_LOCALE`

provider별:

- `ANTHROPIC_API_KEY`
- `OPENAI_API_KEY`
- `OPENROUTER_API_KEY`
- `OLLAMA_HOST`
- `OLLAMA_API_KEY`

## Memory

### Memory Files

memory 파일은 프로젝트 가이드를 system prompt에 주입하는 용도입니다.

자동 탐색 위치:

- Global:
  - `~/.kernforge/MEMORY.md`
- Workspace 계층:
  - `.kernforge/KERNFORGE.md`
  - `KERNFORGE.md`

starter 파일 생성:

```text
/init
```

### Persistent Memory

Kernforge는 세션 간 persistent memory를 내장하고 있습니다. 완료된 턴을 압축 요약으로 저장하고, 이후 세션에서 관련 기록을 다시 프롬프트에 주입할 수 있습니다.

메타데이터:

- citation id
- 날짜
- 세션 이름 또는 id
- provider/model
- importance: `low`, `medium`, `high`
- trust: `tentative`, `confirmed`

주요 명령:

```text
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

### Memory Retention Policy

워크스페이스 정책 생성:

```text
/init memory-policy
```

정책 파일 위치:

- `.kernforge/memory-policy.json`

## Skills와 MCP

### Skills

starter skill 생성:

```text
/init skill checks
```

skill 탐색 경로에는 전역 skill 디렉터리, workspace `.kernforge/skills`, `skills`가 포함됩니다.

주요 명령:

```text
/skills
/reload
```

현재 요청에만 skill을 활성화하려면 프롬프트에 `$checks`처럼 적으면 됩니다.

### MCP

Kernforge는 stdio 기반 MCP 서버를 붙여서 tools, resources, prompts를 CLI 안으로 노출합니다.

주요 명령:

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

## Interactive REPL

### 기본 사용

```text
이 저장소 구조를 설명해줘
```

### Multiline Input

줄 끝에 `\`를 붙이면 다음 줄로 이어집니다.

```text
인증 관련 흐름을 찾아서 \
핵심 파일을 정리해줘
```

### 취소와 히스토리

- 입력 중 `Esc`: 현재 입력 취소
- 요청 실행 중 `Esc`: 모델 요청 취소
- Windows 콘솔의 `Up` / `Down`: 최근 입력 재호출

### Tab Completion

`Tab` 완성 지원 대상:

- slash command
- `@file` 멘션
- `/open <path>`
- `/resource <server:...>`
- `/prompt <server:...>`
- `@mcp:server:...`

## Viewer, Selection, Review 워크플로

별도 텍스트 뷰어로 파일 열기:

```text
/open main.go
```

뷰어 기능:

- line number 표시
- themed header와 live footer
- 텍스트 선택
- 선택한 줄 범위를 다음 프롬프트에 자동 반영
- selection stack 저장

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

## Shell Commands

`!`로 셸 명령을 실행합니다.

```text
!git status
!go test ./...
```

기본 제공 shortcut:

```text
!cd src
!ls
!dir
!pwd
!cls
!clear
```

## Permission Modes

| 모드 | 의미 |
| --- | --- |
| `default` | read는 자동 허용, write와 shell은 확인 필요 |
| `acceptEdits` | read/write 자동 허용, shell은 확인 필요 |
| `plan` | 읽기 전용 |
| `bypassPermissions` | 전부 자동 승인 |

REPL에서 변경:

```text
/permissions default
/permissions acceptEdits
/permissions plan
/permissions bypassPermissions
```

## Verification, Checkpoint, Rollback

편집이 성공하면 Kernforge는 가능한 경우 자동 verification을 수행합니다.

지원되는 검증 감지:

- Go: targeted `go test` + `go vet ./...`
- Cargo: `cargo check`, `cargo test`
- Node: `npm run typecheck`, `npm run lint`, `npm test`
- CMake: `cmake --build <dir>`, 필요 시 `ctest --test-dir <dir>`
- Visual Studio C++: `msbuild <solution-or-project> /m`

주요 명령:

```text
/verify [path,...|--full]
/verify-dashboard [all]
/verify-dashboard-html [all]
/checkpoint [name]
/checkpoint-auto [on|off]
/checkpoint-diff [target] [-- path[,path2]]
/checkpoints
/rollback [target]
```

워크스페이스 verification policy 생성:

```text
/init verify
```

## Session, Profile, Planning

세션 관련 명령:

```text
/session
/sessions
/resume <session-id>
/rename <name>
/export [file]
/tasks
```

provider profile 관련 명령:

```text
/provider
/provider status
/profile
/model <name>
```

plan review 관련 명령:

```text
/set-plan-review [provider]
/set-plan-review status
/profile-review
/do-plan-review <task>
```

이 흐름을 이용하면 실행 전에 별도 reviewer 모델 설정을 저장하고, reviewer profile을 관리하면서 계획을 반복 검토할 수 있습니다.

## Slash Commands

```text
/help
/status
/config
/context
/reload
/version
/provider
/profile
/model <name>
/permissions <mode>
/verify [path,...|--full]
/verify-dashboard [all]
/verify-dashboard-html [all]
/clear
/reset
/new
/compact [focus]
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
/checkpoint [name]
/checkpoint-auto [on|off]
/checkpoint-diff [target] [-- path[,path2]]
/locale-auto [on|off]
/checkpoints
/rollback [target]
/skills
/mcp
/resources
/resource <server:uri-or-name>
/prompts
/prompt <server:name> {"arg":"value"}
/init
/init config
/init verify
/init memory-policy
/init skill <name>
/open <path>
/selection
/selections
/use-selection <n>
/drop-selection <n>
/note-selection <text>
/tag-selection <tag[,tag2,...]>
/clear-selection
/clear-selections
/diff-selection
/review-selection [...]
/review-selections [...]
/edit-selection <task>
/session
/sessions
/resume <session-id>
/rename <name>
/tasks
/diff
/export [file]
/set-plan-review [provider]
/do-plan-review <task>
/profile-review
/exit
/quit
```

## 문제 해결

### `provider/model are not configured`

- 한 번 interactive로 실행해서 설정
- 또는 `-provider`, `-model` 전달
- 또는 config에 저장

### `-image requires -prompt`

`-image`는 one-shot 모드 전용입니다. 대화형 모드에서는:

```text
@screenshot.png 이 이미지를 설명해줘
```

### config, skill, MCP 변경이 반영되지 않음

```text
/reload
```

### `no Ollama models were returned`

다음을 확인하세요.

- Ollama 서버가 실행 중인지
- base URL이 맞는지
- 해당 모델을 미리 pull 했는지

## 관련 문서

- MCP / skills 빠른 가이드: [MCP-SKILLS.md](./MCP-SKILLS.md)

## 현재 범위 요약

- Go 1.21 단일 바이너리 CLI
- interactive REPL과 one-shot prompt mode
- Anthropic / OpenAI / OpenRouter / Ollama / OpenAI-compatible 지원
- 이미지 입력 지원
- 파일, 이미지, 라인 범위, MCP 멘션
- 자동 코드 scouting
- selection 저장이 가능한 텍스트 뷰어
- selection 중심 review / edit 워크플로
- 세션 저장 / 재개 / export
- dashboard와 retention policy를 포함한 persistent memory
- workspace checkpoint / rollback
- provider profile과 reviewer profile 관리
- 로컬 skill
- stdio MCP tools / resources / prompts
- 자동 verification과 verification dashboard
- workspace verification policy
- 별도 reviewer 모델을 활용하는 plan-review 워크플로
