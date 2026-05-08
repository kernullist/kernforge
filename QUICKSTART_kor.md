# Kernforge 빠른 시작

이 문서는 Kernforge를 처음 쓰는 사람이 가장 빨리 핵심 흐름을 체감하도록 돕는 짧은 온보딩 가이드이다.

가장 먼저 기억할 것:
1. 워크스페이스가 크거나 낯설면 먼저 `/analyze-project`
2. live 상태가 중요하면 `/investigate`
3. 공격자 관점이 중요하면 `/simulate`
4. 이미 재현되는 증상이 있으면 `/find-root-cause`
5. 입력 파라미터 관점으로 소스만 먼저 흔들어 보고 싶으면 `/fuzz-func`
6. 코드 범위를 좁혀 보고 싶으면 `/open` 후 `/review-selection` 또는 `/edit-selection`
7. 마지막에는 `/verify`, 그리고 결과는 `/evidence-dashboard`와 `/mem-search`로 확인

실행 전에 `kernforge --help`를 입력하면 실행 파일 version과 standalone, one-shot, MCP server, daemon proxy 예시를 볼 수 있습니다. version만 확인할 때는 `kernforge --version`을 쓰고, MCP client에 연결할 때는 `kernforge help mcp`를 먼저 보면 됩니다.
Codex가 Kernforge를 MCP server로 사용할 때 코드 리뷰는 `kernforge_review_code`로 처리합니다. Codex가 넘긴 diff/code 또는 workspace git diff를 Kernforge의 main model로 리뷰합니다.
같은 MCP entry를 여러 repository에서 재사용한다면 현재 repo를 tool의 `workspace` argument로 넘기거나 repo별로 `-cwd`를 지정하세요. 그렇지 않으면 Kernforge는 server launch directory를 fallback workspace로 사용합니다.

## 1. 5분 안에 익히는 핵심 루프

추천 순서:

```text
/analyze-project driver startup, integrity, and signing architecture
/analyze-performance startup
/investigate start driver-visibility guard.sys
/investigate snapshot
/simulate tamper-surface guard.sys
/find-root-cause guard.sys unload 후에도 user process가 device close에서 멈춰. expected: close가 반환되어야 하지만 observed: pending request가 끝나지 않아.
/fuzz-func @driver/guard.cpp
/open driver/guard.cpp
/review-selection integrity bypass paths
/edit-selection harden the selected integrity checks
/verify
/evidence-dashboard category:driver
```

이 흐름의 의미:
1. 재사용 가능한 구조 지식과 performance lens를 먼저 만든다.
2. 현재 상태를 캡처한다.
3. `driver-visibility`는 드라이버 로드 원인 분석기가 아니라 가시성 triage snapshot이다.
4. 공격자 관점에서 약한 면을 먼저 본다.
5. `/find-root-cause`는 증상, trigger, expected invariant, observed failure를 기준으로 worker/reviewer root-cause 조사를 시작한다.
6. `/fuzz-func`는 함수나 파일을 기준으로 공격자 입력 상태, 비교식, 반례, sink 도달 경로를 소스만으로 먼저 본다.
7. 선택한 코드만 집중 리뷰/수정한다.
8. verification으로 닫는다.
9. evidence dashboard로 현재 위험 상태를 확인한다.

## 2. 가장 자주 쓰는 명령

프로젝트 분석:
- `/analyze-project [--mode map|trace|impact|surface|security|performance] <goal>`
- `/analyze-performance [focus]`
- `/set-analysis-models`
- `--mode`를 생략하면 기본 모드는 `map`
- 긴 `/analyze-project` 실행은 shard wave, 완료/실패 shard 수, worker/reviewer 모델 대기 event, 마지막 artifact 저장 단계를 보여준다. 이제 `progress_display` 기본값은 `stream`이므로 모든 update가 기본적으로 남고, 조용한 모드가 필요할 때만 `/progress-display auto` 또는 `/progress-display compact`로 낮춘다.
- project analysis가 이전에 설정한 worker/reviewer route가 아니라 현재 main model을 따르길 원하면 `/set-analysis-models clear`를 사용한다.

조사:
- `/investigate`
- `/investigate start <preset> [target]`
- `/investigate snapshot`
- `/investigate dashboard`

공격자 관점:
- `/simulate tamper-surface [target]`
- `/simulate stealth-surface [target]`
- `/simulate forensic-blind-spot [target]`
- `/simulate dashboard`

근본 원인 분석:
- `/find-root-cause <problem description>`
- `/find-root-cause --pattern-pack <path-or-dir> <problem description>`
- `/root-cause-patterns list [--type <project_type>]`
- `/root-cause-patterns match <problem symptom>`
- `/root-cause-patterns github-search [--type <project_type>] [--limit 20] [query words...]`
- `/root-cause-patterns normalize --in <github_issues.json> --out <pattern_pack.json>`
- `/root-cause-patterns validate [--in <pattern_pack.json>]`

소스 레벨 fuzzing:
- `/fuzz-func <function-name>`
- `/fuzz-func <function-name> --file <path>`
- `/fuzz-func <function-name> --source-scan focused`
- `/fuzz-func --from-candidate <candidate-id>`
- `/fuzz-func @<path>`
- `/fuzz-func status`
- `/fuzz-func show [id|latest]`
- `/fuzz-func language [system|english]`
- `/source-scan run`
- `/source-scan list`
- `/source-scan show [id|latest]`

선택 영역 작업:
- `/open <path>`
- `/review-selection [extra]`
- `/edit-selection <task>`

검증:
- `/verify`
- `/verify-dashboard`
- `/set-auto-verify [on|off]`
- `/detect-verification-tools`
- `/set-msbuild-path <path>`

증거와 기억:
- `/evidence-dashboard`
- `/evidence-search <query>`
- `/mem-search <query>`
- 같은 workspace의 최근 high-value record는 `Workspace continuity`로 자동 주입되며, 재사용 시 `memory` activity line으로 표시됩니다.

정책:
- `/hooks`
- `/override`

계획과 tracked feature 작업:
- `/do-plan-review <task>`
- `/new-feature <task>`
- `/new-feature status [id]`
- `/new-feature implement [id]`

provider 및 런타임 확인:
- `/provider status`
- `/status`
- `/config`

## 3. 시작할 때 가장 좋은 시나리오

### Driver 변경

```text
/analyze-project driver startup and integrity architecture
/investigate start driver-visibility guard.sys
/simulate tamper-surface guard.sys
/fuzz-func @Driver/guard.cpp
/open driver/guard.cpp
/review-selection signing and integrity assumptions
/verify
/evidence-dashboard category:driver
```

### 입력 지향 코드 triage

```text
/fuzz-func ValidateRequest --file src/guard.cpp
/fuzz-func ValidateRequest --source-scan focused
/fuzz-func @Driver/HEVD/Windows/DoubleFetch.c
/source-scan run --limit 50
/fuzz-func --from-candidate <candidate-id>
/fuzz-func show latest
```

이 시나리오의 의미:
1. 함수를 바로 알고 있으면 함수명과 파일 경로로 좁힌다.
2. 함수를 모르면 파일만 지정해도 Kernforge가 대표 루트와 input-facing 경로를 고른다.
3. `/fuzz-func`는 기본적으로 focused source-scan context를 붙인다. 후보를 먼저 보고 고르고 싶으면 `/source-scan run` 후 `/fuzz-func --from-candidate <candidate-id>`로 이어간다. Source candidate에는 function-window evidence, confidence breakdown, 파일/심볼 fingerprint, stale-source 상태가 포함된다.
3. 결과에서 가장 높은 점수의 finding, `가장 유용한 분기 차이 요약`, `먼저 볼 관련 소스`를 먼저 읽는다.

### 증상 기반 root-cause 조사

```text
/find-root-cause 내 Win32 서비스 프로세스가 sc stop으로 종료되지 않아
/find-root-cause 내 게임에서 파티원을 초대하고 추방하다 보면 파티원 제한 숫자를 넘어서서 파티원을 초대할 수 있게 돼
/root-cause-patterns match sc stop을 실행해도 서비스 프로세스가 계속 running 상태야
```

이 시나리오의 의미:
1. 증상 프롬프트에는 component, trigger/repro, expected invariant, observed failure를 넣을수록 좋다.
2. 불명확하면 Kernforge가 부족한 부분과 더 정확한 `/find-root-cause ...` 프롬프트를 다시 제안한다.
3. pattern pack은 후보 prior일 뿐이며, 최종 root cause는 현재 소스 증거와 reviewer causality validation을 거쳐야 한다.

### Telemetry 변경

```text
/analyze-project telemetry provider visibility and manifest architecture
/investigate start provider-visibility MyProvider
/simulate stealth-surface MyProvider
/open telemetry/provider.man
/review-selection provider visibility and schema drift
/verify
/evidence-search category:telemetry outcome:failed
```

### 여러 세션에 걸친 feature 작업

```text
/new-feature harden driver registration, preserve telemetry audit artifacts, and document rollback points
/new-feature status
/new-feature implement
/verify
/new-feature close
```

`/new-feature`는 `.kernforge/features/<id>` 아래에 spec, plan, task artifact를 남기며 진행 상태를 추적하고 싶을 때 쓰는 것이 좋다. `/do-plan-review`는 reviewer를 붙여 한 번에 계획을 다듬고 바로 실행하고 싶을 때 더 잘 맞는다.

## 4. 막혔을 때 가장 먼저 볼 것

1. `/status`
2. `/provider status`
3. `/analyze-performance startup` 또는 관련 focus
4. `/evidence-dashboard`
5. `/mem-search category:driver` 또는 `/mem-search category:telemetry`
6. `/hooks`

빠른 해석:
1. `/status`는 현재 세션과 런타임 상태를 빠르게 보는 용도다. approval 상태도 여기서 본다.
2. `/config`는 provider 기본값, hooks, locale, verification toggle 같은 현재 적용 설정을 빠르게 보는 용도다.
3. `/provider status`는 현재 provider 연결 상태를 빠르게 보는 용도다. 정규화된 endpoint, API key 설정 여부, provider별로 실제 확인 가능한 budget visibility 범위를 보여준다.

Windows build tool이 없어 automatic verification이 실패하면:
1. 먼저 `/detect-verification-tools`를 실행한다.
2. 자동 탐지가 못 찾으면 예를 들어 `/set-msbuild-path "C:\Program Files\Microsoft Visual Studio\2022\Community\MSBuild\Current\Bin\MSBuild.exe"`처럼 직접 지정한다.
3. 당분간 편집 후 verification을 끄고 싶으면 `/set-auto-verify off`를 사용한다.

모델이 stage/commit/push/PR을 하려고 할 때:
1. Kernforge는 git 변경 작업을 파일 수정과 다른 승인 축으로 본다.
2. `Allow git?`는 현재 세션의 git mutation tool 승인이다.
3. 일반 review/edit 프롬프트에서는 사용자가 명시적으로 요청하지 않는 한 git mutation이 실행되지 않는 것이 기본이다.

모델이 큰 파일을 계속 다시 읽으려 할 때:
1. 최근 `read_file` cache hit는 같은 범위를 조용히 다시 읽는 대신 `NOTE:` 접두사로 이미 본 구간임을 드러낸다.
2. `grep` 결과에 `[cached-nearby:inside]` 또는 `[cached-nearby:N]`가 붙을 수 있는데, 이는 최근 읽은 범위와 겹치거나 아주 가깝다는 뜻이다.
3. 이 경우에는 큰 블록 전체를 다시 요청하기보다 빠진 인접 범위나 정확한 수정 지점만 좁게 요청하는 편이 좋다.

## 5. 입력 취소 팁

1. 입력 중 `Esc`는 현재 입력만 취소한다.
2. 모델 응답 대기 중 `Esc`는 진행 중 요청을 취소한다.
3. Windows 콘솔에서는 짧게 누른 `Esc`도 취소로 잡히도록 처리되어 있다.
4. 요청 취소 직후 다음 프롬프트는 잔여 `Esc` 입력 때문에 자동 취소되지 않도록 안정화된다.

## 6. 다음 문서

더 자세한 흐름:
- [상세 사용 가이드](./FEATURE_USAGE_GUIDE_kor.md)

도메인별 운영 문서:
- [Driver 플레이북](./PLAYBOOK_driver_kor.md)
- [Telemetry 플레이북](./PLAYBOOK_telemetry_kor.md)
