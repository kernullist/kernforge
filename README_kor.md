# Kernforge

![Kernforge banner](./branding/kernforge-release-banner-1280x640.png)
![Kernforge demo](./branding/kernforge_demo.gif)

`Kernforge`는 Go로 만든 터미널 중심 AI 코딩 CLI입니다. 로컬 우선 개발 흐름에 맞춰 설계되어 있고, 특히 Windows 환경에서 실용적으로 쓰기 좋게 구현되어 있습니다.

## 현재 구현된 기능

- 대화형 REPL과 `-prompt` 기반 one-shot 실행
- `ollama`, `anthropic`, `openai`, `openrouter`, `openai-compatible` provider 지원
- 파일, 패치, 셸, git 중심 도구 호출
- `git_add`, `git_commit`, `git_push`, `git_create_pr` 같은 전용 git 도구
- 로컬 파일 멘션, 이미지 멘션, MCP 리소스 멘션
- 세션 저장, 재개, 이름 변경, clear, compact, Markdown export
- 프로젝트 메모리 파일과 세션 간 persistent memory
- 로컬 `SKILL.md` 스킬 탐색과 요청 단위 활성화
- stdio 기반 MCP server의 tool, resource, prompt 연결
- Windows용 별도 viewer와 diff preview 창
- adaptive verification, 검증 이력 대시보드, checkpoint, rollback
- 별도 reviewer 모델을 사용하는 plan-review 워크플로우

## 핵심 특징

### 입력과 프롬프트

- 대화형 채팅 REPL
- `-prompt` 기반 단발 실행
- `-image`, `-i`, `@path/to/image.png` 이미지 첨부
- `@main.go` 같은 파일 멘션
- `@main.go:120-150` 같은 라인 범위 멘션
- `@mcp:docs:getting-started` 같은 MCP 리소스 멘션
- 줄 끝에 `\`를 붙여 멀티라인 입력
- 파일을 명시하지 않았을 때 자동 코드 scouting

### 편집 워크플로우

- 파일 쓰기 전 diff preview
- selection-aware edit preview
- 편집 후 자동 verification
- 한 요청의 첫 편집 전에 자동 checkpoint 생성
- 수동 checkpoint, checkpoint diff, rollback
- `/open` 중심 selection-first 리뷰/수정 흐름

### 사용성

- 명령, 경로, 멘션, MCP 대상, `/open`에 대한 `Tab` 완성
- 현재 입력 취소를 위한 `Esc`
- 진행 중 요청 취소를 위한 `Esc`
- Windows 콘솔의 `Up`, `Down` 입력 히스토리

### 지속성

- `/resume` 기반 세션 재개
- 세션 이름 변경과 대화 Markdown export
- citation id, trust, importance가 붙는 persistent memory
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
| `max_tool_iterations` | 요청당 tool loop 최대 반복 수 |
| `permission_mode` | `default`, `acceptEdits`, `plan`, `bypassPermissions` |
| `shell` | `run_shell`에 사용할 셸 |
| `session_dir` | 세션 JSON 저장 디렉터리 |
| `auto_compact_chars` | 자동 compact를 시도할 대략적 컨텍스트 길이 |
| `auto_checkpoint_edits` | 첫 편집 전에 안전 checkpoint 생성 |
| `auto_verify_docs_only` | 문서만 바뀐 경우에도 자동 verification 허용 |
| `auto_locale` | 시스템 locale을 프롬프트에 자동 주입 |
| `memory_files` | 추가 메모리 파일 경로 |
| `skill_paths` | 추가 skill 탐색 경로 |
| `enabled_skills` | 항상 프롬프트에 주입할 skill |
| `mcp_servers` | MCP 서버 정의 |
| `profiles` | 최근 또는 고정 provider/model profile |
| `plan_review` | `/do-plan-review`용 reviewer 모델 설정 |
| `review_profiles` | reviewer profile 저장 목록 |

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

## Provider 지원

### Ollama

- 기본 base URL: `http://localhost:11434`
- `OLLAMA_HOST`, `OLLAMA_API_KEY` 사용
- 첫 실행 시 로컬 서버 자동 탐지
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
- 대화형 모델 선택기에서 페이지 이동, 필터링, curated 추천, reasoning-only 필터, 정렬 지원

### OpenAI-compatible

- OpenAI 스타일 chat completions API 사용
- 별도 지정이 없으면 `OPENAI_API_KEY` 사용
- `base_url`을 명시하는 구성이 일반적

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
/init config
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

## 대화형 REPL

### 기본 사용

```text
이 저장소 구조를 설명해줘
```

### 유용한 런타임 명령

```text
/config
/context
/status
/version
/help
/reload
```

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
/model [name]
/profile
/profile-review
/set-plan-review [provider]
/do-plan-review <task>
/permissions [mode]
/locale-auto [on|off]
```

### 취소와 히스토리

- 입력 중 `Esc`: 현재 입력 취소
- 요청 실행 중 `Esc`: 진행 중인 모델 요청 취소
- Windows 콘솔의 `Up`, `Down`: 최근 입력 불러오기

### Tab 완성

`Tab` 완성 지원 대상:

- slash command
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

## 참고

- 별도 viewer 창과 diff preview 창은 주로 Windows 환경에 맞춰 구현되어 있습니다.
- CLI 핵심, 세션, provider, memory, skills, MCP, verification 로직은 가능한 범위에서 이식성을 유지하도록 구성되어 있습니다.
