# Kernforge Hook Engine Spec

이 문서는 Kernforge에 도입할 Hook Engine의 상세 설계 초안이다.

목표:
- Claude Code의 hooks와 유사한 범용 확장 지점을 확보한다.
- Kernforge의 기존 강점인 permission, preview, checkpoint, verification 흐름과 자연스럽게 결합한다.
- Windows security/anti-cheat 엔지니어링에 맞는 정책성 제어 계층으로 확장 가능하게 만든다.

비목표:
- 이번 단계에서 cloud workflow를 설계하지 않는다.
- 이번 단계에서 full plugin ABI를 정의하지 않는다.
- 이번 단계에서 GUI 기반 hook editor를 만들지 않는다.

## 1. 설계 원칙

1. 로컬 우선
- hook은 기본적으로 로컬 규칙 파일과 로컬 실행 로직으로 동작한다.

2. 정책 우선
- hook의 핵심 역할은 자동화보다 정책 집행이다.
- allow/deny/warn/ask가 기본이며, 이후 enrich/queue 계열은 확장으로 붙인다.

3. 기존 승인 체계와 충돌하지 않음
- 현재 `PermissionManager.Allow()`를 대체하지 않는다.
- hook은 permission 이전 또는 이후에 개입해 정책적 결정을 보강한다.

4. 설명 가능성
- 사용자는 왜 막혔는지, 왜 추가 확인이 필요한지, 어떤 rule이 발동했는지 볼 수 있어야 한다.

5. 안전한 단계적 도입
- 처음에는 read-only 또는 soft enforcement 중심으로 도입한다.
- destructive 성격이 있는 deny/rewrite는 범위를 제한한다.

## 2. 현재 코드 기준 연결 포인트

현재 코드 기준 핵심 연결 포인트는 다음과 같다.

1. shell 실행
- `tools.go`
- `Workspace.EnsureShell`
- `RunShellTool.Execute`
- `GitPushTool.Execute`
- `GitCreatePRTool.Execute`

2. 파일 편집
- `WriteFileTool.Execute`
- `ReplaceInFileTool.Execute`
- `Workspace.ConfirmEdit`
- `Workspace.BeforeEdit`
- `Workspace.EnsureWrite`

3. verification 실행
- `verify.go`
- `runRecommendedVerification`
- `executeVerificationSteps`

4. 사용자 확인 흐름
- `main.go`
- `runtimeState.confirm`

5. checkpoint/rollback
- `main.go`
- `handleCheckpointCommand`
- `handleRollbackCommand`

핵심 해석:
- Hook Engine은 기존 구조를 갈아엎지 않고, `Workspace`, `runtimeState`, `verify.go`, git tool 경로에 주입하는 방식이 가장 안정적이다.

## 3. 지원할 Hook Event

MVP에서 지원할 이벤트는 아래 순서로 도입한다.

### 3.1 P0 이벤트

1. `UserPromptSubmit`
- 사용자가 프롬프트를 제출했을 때
- 용도:
  - 위험 작업 의도 감지
  - 추가 가이드 문맥 삽입
  - 특정 전문 review profile 자동 제안

2. `PreToolUse`
- 임의 tool 실행 직전
- 용도:
  - 특정 tool 차단
  - 특정 인자 패턴 경고
  - 보안 민감 command 승인 강화

3. `PostToolUse`
- tool 실행 직후
- 용도:
  - 결과 텍스트 검사
  - 후속 verification 예약
  - evidence 추출

4. `PreEdit`
- 실제 파일 write 직전
- 용도:
  - 특정 파일 패턴 보호
  - selection 범위 외 수정 경고
  - checkpoint 강제

5. `PostEdit`
- 파일 write 직후
- 용도:
  - changed file 분류
  - verification hint 추가
  - memory/evidence annotation

6. `PreVerification`
- verification 실행 직전
- 용도:
  - 검증 step 추가/제거
  - 강제 검증 누락 탐지

7. `PostVerification`
- verification 실행 직후
- 용도:
  - failure kind 기반 추가 guidance
  - repeat failure 누적 기록

8. `PreGitPush`
- `git push` 직전
- 용도:
  - 민감 artifact 존재 시 경고 또는 차단

9. `PreCreatePR`
- `gh pr create` 직전
- 용도:
  - 제목/body에 보안 체크리스트 누락 시 경고
  - unsigned artifact 포함 시 차단

### 3.2 P1 이벤트

1. `SessionStart`
2. `SessionEnd`
3. `PreCheckpoint`
4. `PostCheckpoint`
5. `PreRollback`
6. `PostRollback`

### 3.3 P2 이벤트

1. `SubagentStart`
2. `SubagentStop`
3. `AutomationRun`

## 4. Event Payload 모델

모든 hook 이벤트는 공통 envelope와 이벤트별 payload를 가진다.

### 4.1 공통 envelope

```json
{
  "event": "PreToolUse",
  "timestamp": "2026-04-03T12:34:56+09:00",
  "session_id": "sess-...",
  "workspace_root": "C:/git/kernforge",
  "cwd": "C:/git/kernforge",
  "provider": "openai",
  "model": "gpt-5.4",
  "interactive": true,
  "request_id": "req-...",
  "selection": {
    "file": "main.go",
    "start_line": 120,
    "end_line": 180,
    "tags": ["verify", "agent"]
  }
}
```

공통 필드:
1. `event`
2. `timestamp`
3. `session_id`
4. `workspace_root`
5. `cwd`
6. `provider`
7. `model`
8. `interactive`
9. `request_id`
10. `selection`

### 4.2 `UserPromptSubmit` payload

```json
{
  "user_text": "현재 구현된 기능들을 검토해줘",
  "images": [],
  "mentions": ["README_kor.md", "main.go"],
  "auto_scout": true
}
```

### 4.3 `PreToolUse` / `PostToolUse` payload

```json
{
  "tool_name": "run_shell",
  "tool_args": {
    "command": "signtool verify /pa build\\driver.sys",
    "timeout_ms": 30000
  },
  "tool_kind": "shell",
  "risk_tags": ["windows", "signing"],
  "result": {
    "ok": true,
    "output": "Successfully verified",
    "error": ""
  }
}
```

### 4.4 `PreEdit` / `PostEdit` payload

```json
{
  "path": "driver/guard.cpp",
  "absolute_path": "C:/git/kernforge/driver/guard.cpp",
  "operation": "write_file",
  "reason": "write driver/guard.cpp",
  "selection_overlap": {
    "has_selection": true,
    "same_file": true,
    "inside_selection": false
  },
  "diff_stats": {
    "added_lines": 12,
    "removed_lines": 3
  },
  "file_tags": ["cpp", "driver", "kernel"]
}
```

### 4.5 `PreVerification` / `PostVerification` payload

```json
{
  "trigger": "auto_after_edit",
  "mode": "adaptive",
  "changed_files": ["driver/guard.cpp"],
  "steps": [
    {
      "label": "go test ./...",
      "command": "go test ./..."
    }
  ],
  "report": {
    "has_failures": false,
    "summary": "passed=2 failed=0"
  }
}
```

### 4.6 `PreGitPush` / `PreCreatePR` payload

```json
{
  "remote": "origin",
  "branch": "feature/security-hook",
  "changed_files": ["driver/guard.cpp", "README_kor.md"],
  "staged_files": ["driver/guard.cpp"],
  "artifact_tags": ["driver", "kernel"],
  "pr": {
    "title": "Add driver guard validation",
    "draft": true
  }
}
```

## 5. Rule Schema

권장 파일 위치:
- 전역: `~/.kernforge/hooks.json`
- 워크스페이스: `.kernforge/hooks.json`

병합 규칙:
1. 전역 rules 로드
2. 워크스페이스 rules 로드
3. 뒤의 규칙이 앞 규칙보다 우선
4. 단, `priority`가 더 높으면 먼저 평가

### 5.1 Top-level schema

```json
{
  "enabled": true,
  "stop_on_match": false,
  "rules": [
    {
      "id": "warn-driver-edit-without-symbol-check",
      "enabled": true,
      "priority": 200,
      "events": ["PostEdit"],
      "match": {
        "paths": ["driver/**/*.cpp", "driver/**/*.h", "**/*.sys"],
        "tool_names": ["write_file", "replace_in_file"],
        "file_tags": ["driver"]
      },
      "action": {
        "type": "warn",
        "message": "Driver-related changes detected. Consider symbol and signing verification."
      }
    }
  ]
}
```

### 5.2 Rule fields

1. `id`
- 고유 식별자

2. `enabled`
- 개별 rule on/off

3. `priority`
- 큰 숫자가 먼저

4. `events`
- 매칭할 hook event 목록

5. `match`
- 이벤트 payload에 대한 조건

6. `action`
- 실행할 동작

7. `stop`
- 이 rule이 발동하면 이후 rule 평가 중단

### 5.3 Match 조건

MVP에서 지원할 조건:
1. `tool_names`
2. `paths`
3. `commands_regex`
4. `file_tags`
5. `risk_tags`
6. `branches`
7. `changed_files`
8. `contains_text`
9. `interactive`
10. `providers`
11. `models`

예시:

```json
{
  "tool_names": ["run_shell"],
  "commands_regex": [
    "(?i)\\bbcdedit\\b",
    "(?i)\\bverifier\\b",
    "(?i)\\bsc\\s+stop\\b"
  ],
  "interactive": true
}
```

### 5.4 Action 종류

MVP 지원:
1. `allow`
2. `warn`
3. `ask`
4. `deny`

P1 지원:
1. `append_context`
2. `add_verification_step`
3. `require_checkpoint`
4. `tag_evidence`

P2 지원:
1. `rewrite_args`
2. `spawn_subagent`
3. `emit_metric`

### 5.5 Action semantics

#### `allow`
- 명시적으로 통과
- 이후 rule은 계속 볼 수 있음

#### `warn`
- 사용자에게 경고 메시지 출력
- 작업은 계속됨

#### `ask`
- hook 레벨에서 추가 확인을 요구
- 현재 `confirm()`을 재사용 가능

#### `deny`
- 작업 차단
- rule id와 차단 이유를 출력

#### `append_context`
- 현재 request context에 보조 문맥 추가
- 예: "Driver 변경이므로 symbol/signing 검증을 권장"

#### `add_verification_step`
- `PreVerification`에서 verification plan에 step 주입

#### `require_checkpoint`
- `PreEdit`에서 checkpoint 강제 생성

## 6. Hook Runtime 동작 순서

### 6.1 공통 순서

1. event payload 생성
2. event에 해당하는 rules 필터링
3. `priority` 기준 정렬
4. 각 rule의 match 평가
5. 매칭된 rule action 실행
6. `stop=true` 또는 global `stop_on_match`면 종료
7. 최종 verdict 반환

### 6.2 Verdict 모델

```go
type HookVerdict struct
{
    Allow bool
    Warns []HookNotice
    DenyReason string
    AskMessage string
    ContextAdds []string
    VerificationAdds []VerificationStep
    RequireCheckpoint bool
    MatchedRuleIDs []string
}
```

핵심 규칙:
1. 하나라도 `deny`면 차단
2. `ask`는 deny가 없을 때만 사용자 확인
3. `warn`는 누적 가능
4. `append_context`와 `add_verification_step`는 누적 가능

### 6.3 PermissionManager와의 관계

권장 순서:

1. `PreToolUse` hook
2. 기존 `PermissionManager.Allow`
3. 실제 tool 실행
4. `PostToolUse` hook

편집의 경우:

1. `PreEdit` hook
2. diff preview
3. `PermissionManager.Allow(ActionWrite, ...)`
4. auto-checkpoint
5. 실제 write
6. `PostEdit` hook

이유:
- hook은 정책 계층
- permission은 사용자 승인 계층
- 둘을 분리해야 동작 설명이 명확해진다

## 7. 사용자 표시 방식

사용자는 hook 동작을 명확히 알아야 한다.

권장 출력 형식:

1. warn
```text
Hook warning [warn-driver-edit-without-symbol-check]
Driver-related changes detected. Consider symbol and signing verification.
```

2. ask
```text
Hook confirmation [ask-before-bcdedit]
This command modifies boot configuration. Continue?
```

3. deny
```text
Hook denied [deny-unsigned-driver-pr]
PR creation is blocked because unsigned driver artifacts were detected.
```

4. summary
```text
Hook summary: matched=2 warn=1 ask=1
```

추가 요구:
- verbose 모드에서는 payload 요약도 볼 수 있게 한다.
- rule id는 항상 보여준다.

## 8. Windows Security 용 기본 내장 Rule 세트

초기 내장 규칙을 제공하는 것이 좋다.

권장 preset 이름:
- `default`
- `windows-security`
- `anti-cheat`

### 8.1 `windows-security` 예시

1. `bcdedit` 사전 확인
- `run_shell`에서 `bcdedit` 감지 시 `ask`

2. `verifier` 사전 확인
- `verifier /reset` 또는 설정 변경 시 `ask`

3. driver 관련 파일 편집 후 경고
- `.sys`, `.inf`, `.cat`, driver source edit 후 `warn`

4. PR 전 서명 체크 경고
- driver tag 파일이 바뀌었는데 signing 검증 이력이 없으면 `warn` 또는 `deny`

5. selection 바깥 대규모 수정 경고
- selection 기반 작업인데 diff가 selection 밖으로 크게 벗어나면 `warn`

### 8.2 `anti-cheat` 예시

1. telemetry collector 변경 시 ETW/contract verification 권고
2. memory scanning 변경 시 synthetic evasion regression 권고
3. Unreal integrity 코드 변경 시 asset/schema drift 검증 권고
4. process protection 변경 시 trust boundary review profile 권고

## 9. 구현 아키텍처

권장 추가 파일:

1. `hooks.go`
- public entry
- engine lifecycle

2. `hooks_types.go`
- event, payload, verdict, notice 타입

3. `hooks_match.go`
- rule match evaluator

4. `hooks_actions.go`
- action 실행기

5. `hooks_config.go`
- hooks.json load/merge/validate

6. `hooks_builtin.go`
- built-in preset rules

7. `hooks_test.go`
- 단위 테스트

8. `hooks_integration_test.go`
- tool/edit/verify/git 연동 테스트

### 9.1 핵심 타입 예시

```go
type HookEvent string

const
(
    HookUserPromptSubmit HookEvent = "UserPromptSubmit"
    HookPreToolUse HookEvent = "PreToolUse"
    HookPostToolUse HookEvent = "PostToolUse"
    HookPreEdit HookEvent = "PreEdit"
    HookPostEdit HookEvent = "PostEdit"
    HookPreVerification HookEvent = "PreVerification"
    HookPostVerification HookEvent = "PostVerification"
    HookPreGitPush HookEvent = "PreGitPush"
    HookPreCreatePR HookEvent = "PreCreatePR"
)
```

```go
type HookRule struct
{
    ID string `json:"id"`
    Enabled *bool `json:"enabled,omitempty"`
    Priority int `json:"priority,omitempty"`
    Events []HookEvent `json:"events"`
    Match HookMatch `json:"match"`
    Action HookAction `json:"action"`
    Stop bool `json:"stop,omitempty"`
}
```

```go
type HookEngine struct
{
    Rules []HookRule
    StopOnMatch bool
}
```

## 10. 구체적 integration plan

### 10.1 `main.go`

추가 역할:
1. hook engine 초기화
2. workspace에 hook executor 주입
3. `runAgentReplyWithImages()` 직전 `UserPromptSubmit` 실행

권장 변경:
- `runtimeState`에 `hooks *HookEngine` 추가

### 10.2 `Workspace`

현재 `Workspace`는 hook 주입 지점으로 매우 적합하다.

권장 필드 추가:
```go
RunHook func(ctx context.Context, event HookEvent, payload any) (HookVerdict, error)
```

또는 더 구체적으로:
```go
Hooks *HookRuntime
```

### 10.3 `tools.go`

#### `RunShellTool.Execute`

추가 순서:
1. `PreToolUse`
2. hook verdict 처리
3. `EnsureShell`
4. shell 실행
5. `PostToolUse`

#### `WriteFileTool.Execute` / `ReplaceInFileTool.Execute`

추가 순서:
1. `PreEdit`
2. hook verdict 처리
3. preview
4. permission
5. checkpoint
6. write
7. `PostEdit`

#### `GitPushTool.Execute`

추가 순서:
1. `PreGitPush`
2. `PreToolUse`
3. permission/shell
4. 실행
5. `PostToolUse`

#### `GitCreatePRTool.Execute`

추가 순서:
1. optional push 전 `PreGitPush`
2. `PreCreatePR`
3. `PreToolUse`
4. permission/shell
5. 실행
6. `PostToolUse`

### 10.4 `verify.go`

#### `runRecommendedVerification`
- plan 완성 후 `PreVerification`
- report 생성 후 `PostVerification`

#### `executeVerificationSteps`
- step 단위 hook은 P1 이후 고려
- MVP에서는 report 단위만 처리

## 11. Config 및 CLI 노출

권장 config field:

```json
{
  "hooks_enabled": true,
  "hooks_file": ".kernforge/hooks.json",
  "hook_presets": ["windows-security"]
}
```

추가 명령 제안:
1. `/hooks`
- 현재 로드된 hook/rule 목록 출력

2. `/hook-test <event>`
- 샘플 payload로 규칙 매칭 테스트

3. `/hook-reload`
- hook config 재로딩

4. `/init hooks`
- sample hooks.json 생성

## 12. MVP 범위

반드시 MVP에 포함:
1. JSON rule loader
2. `UserPromptSubmit`
3. `PreToolUse`
4. `PostToolUse`
5. `PreEdit`
6. `PostEdit`
7. `PreVerification`
8. `PostVerification`
9. `PreGitPush`
10. `PreCreatePR`
11. action: `warn`, `ask`, `deny`
12. built-in preset: `windows-security`

MVP에서 제외:
1. args rewrite
2. async hook execution
3. remote hook transport
4. GUI editor
5. per-step verification hooks
6. subagent hooks

## 13. 테스트 전략

### 13.1 단위 테스트

1. event filtering
2. priority ordering
3. glob/regex matching
4. interactive flag matching
5. deny precedence
6. warn accumulation
7. ask path

### 13.2 통합 테스트

1. `run_shell`에서 `bcdedit` 감지 시 ask
2. driver file edit 시 warn
3. unsigned artifact 조건에서 PR 차단
4. verification 전 hook이 step 추가
5. verification 후 failure kind 기반 warning

### 13.3 회귀 테스트

1. hooks disabled일 때 기존 동작 보존
2. malformed hooks.json이 전체 runtime을 망가뜨리지 않음
3. non-interactive 모드에서 `ask`는 안전하게 `deny` 또는 configured fallback 처리

## 14. 실패 처리 정책

hook engine 자체 오류가 났을 때의 정책은 중요하다.

권장 기본값:
- config load 실패: 경고 후 hooks 비활성화
- rule parse 실패: 해당 rule만 무시하고 경고
- runtime evaluation 실패:
  - default 모드: 경고 후 continue
  - strict hooks 모드: deny

추가 config:
```json
{
  "hooks_fail_closed": false
}
```

보안 팀용 strict 환경에서는 `true`를 쓸 수 있다.

## 15. 단계별 구현 순서

### Step 1
- 타입/설정/로더 추가
- rule match engine 구현

### Step 2
- `run_shell`, `write_file`, `replace_in_file`, `git_push`, `git_create_pr` 연동

### Step 3
- verification 연동

### Step 4
- built-in `windows-security` preset

### Step 5
- `/hooks`, `/hook-reload`, `/init hooks` 추가

## 16. 추천 구현 결정

제가 추천하는 결정은 아래와 같다.

1. hook은 별도 프로세스가 아니라 in-process rule engine으로 시작
2. rule format은 JSON으로 시작
3. `warn/ask/deny` 중심의 정책 엔진으로 먼저 출시
4. permission과 hook은 분리 유지
5. anti-cheat/Windows용 built-in preset을 초기에 제공

이유:
- 구현 복잡도 대비 사용자 가치가 가장 높다.
- 현재 코드 구조에 가장 무리 없이 붙는다.
- 이후 subagent, automation, security verification과 자연스럽게 연결된다.

## 17. 다음 단계

이 문서 다음으로 바로 이어질 작업은 아래 둘 중 하나다.

1. Hook Engine implementation task breakdown 작성
- 파일별 작업 항목
- 함수 시그니처
- 테스트 목록

2. 실제 MVP 코드 구현 시작
- `hooks_types.go`
- `hooks_config.go`
- `hooks_match.go`
- `hooks.go`

추천:
- 다음 단계는 1번보다 바로 2번으로 들어가도 된다.
- 현재 구조가 명확해서 MVP 구현 착수가 가능하다.
