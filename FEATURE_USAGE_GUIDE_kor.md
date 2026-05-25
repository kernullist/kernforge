# Kernforge 상세 사용 가이드

이 문서는 현재 Kernforge에 구현된 기능을 실제로 어떤 상황에서 어떻게 쓰면 좋은지, 그리고 각 명령이 어떤 흐름 안에서 가장 빛나는지를 설명하는 상세 운영 문서이다.

기준 시점:
- 코드베이스 기준: 2026-05-08

대상 사용자:
- Windows security 엔지니어
- anti-cheat 엔지니어
- kernel/user-mode telemetry 개발자
- driver/signing/symbol/package readiness 담당자
- Unreal Engine 보안/무결성 담당자

이 문서의 목적:
1. 기능 목록을 나열하는 것이 아니라 실제 사용 흐름을 설명한다.
2. 어떤 문제에서 어떤 명령 조합을 쓰면 좋은지 예시 중심으로 정리한다.
3. `analyze-project -> analyze-performance -> investigate/simulate -> find-root-cause 또는 fuzz-func -> review/edit/plan -> verify -> evidence/memory/hooks` 루프를 자연스럽게 익히도록 돕는다.

## 1. Kernforge를 가장 잘 쓰는 관점

Kernforge는 단순히 "질문하고 답받는 코딩 CLI"로 써도 되지만, 현재 가장 강한 사용 방식은 먼저 재사용 가능한 프로젝트 지식을 만들고 그 위에서 나머지 루프를 돌리는 것이다.

1. 워크스페이스가 크거나 낯설면 `/analyze-project`를 먼저 실행한다.
2. 성능이나 startup path가 중요하면 `/analyze-performance`로 최신 knowledge pack을 performance lens로 바꾼다.
3. live 상태가 중요하면 `/investigate`로 현장 상태를 수집한다.
4. risk lens가 중요하면 `/simulate`로 tamper, visibility, forensic blind spot을 본다.
5. 이미 사용자에게 보이는 증상이 있고 원인 후보를 좁히고 싶으면 `/find-root-cause`를 실행한다.
6. 입력 파라미터를 공격자 관점으로 바로 흔들어 보고 싶으면 `/fuzz-func`로 source-level fuzzing을 실행한다. seed handoff가 유용하면 Kernforge가 다음 단계로 `/fuzz-campaign run`을 보여준다.
7. `/review selection`, `/edit-selection`, `/review plan`, `/new-feature`로 실제 작업을 진행한다.
8. `/verify`로 verification plan을 돌린다.
9. `/evidence-*`와 `/mem-*`로 상태와 맥락을 다시 확인한다.
10. analysis, investigation, simulation, performance, root-cause, fuzzing, verification, evidence, memory, checkpoint, feature, worktree, jobs, specialist action 뒤에 출력되는 handoff block과 `/continuity` packet을 따라가면 명령 순서를 외우지 않아도 된다.
11. push/PR 전에는 hooks가 마지막 방어선으로 동작한다.

핵심 해석:
1. `analyze-project`는 일회성 요약이 아니라 재사용 가능한 architecture map을 만든다.
2. `analyze-performance`는 최신 구조 지식에서 hot path와 bottleneck 가능성을 끌어낸다.
3. `investigate`는 실행 중 상태를 관찰한다.
4. `simulate`는 공격자 관점에서 약한 면을 드러낸다.
5. `find-root-cause`는 증상, trigger, expected invariant, observed failure를 worker/reviewer causal analysis로 바꾼다.
6. `fuzz-func`는 실제 소스의 guard/probe/copy/dispatch를 바탕으로 공격자 입력 상태, 반례, 분기 차이를 합성한다.
7. `verify`는 변경과 최근 상태를 바탕으로 검증 계획을 조립한다.
8. `evidence`는 결과를 증거 단위로 구조화한다.
9. `memory`는 세션을 넘어가는 장기 맥락을 저장한다.
10. `hooks`는 그 축적된 맥락을 다시 정책으로 바꾼다.

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
6. 기본 대기 문구는 thinking prefix와 중복되지 않도록 정리한다.
7. thinking elapsed timer는 phase 전환마다 기준 시간이 재설정되고, 비정상 stale 값은 2시간 표시로 clamp된다.
8. 반복 blank streamed chunk는 빈 줄 대신 compact working 상태로 바꿔 보여준다.
9. 최종 streamed 답변이 문장 중간에서 끊겨 보이면 모델에게 한 번 continuation을 요청하고, 이어진 답을 합쳐서 프롬프트로 복귀한다.
10. 메인 프롬프트에서 빈 상태로 `Enter`를 눌러도 빈 턴을 만들지 않고 무시한다.
11. `progress_display`가 진행 표시 방식을 제어하며 기본값은 긴 작업의 진행 이력을 남기기 위한 `stream`이다. `/progress-display auto|compact|stream`으로 REPL에서 바로 바꿀 수 있고, config key를 그대로 친 `/progress_display ...`도 같은 명령으로 처리된다. `auto`는 tool/model/route와 project analysis ledger를 transcript에 남기고 고빈도 shell tail 출력은 transient로 유지하며, `compact`는 footer 중심, `stream`은 모든 update 지속 기록 방식이다.
12. OpenAI-compatible 및 OpenAI Codex streaming provider는 tool-call 구성 event를 emit해서 모델이 tool call을 준비 중인지, 인자가 언제 완성됐는지 사용자가 볼 수 있다.
13. DeepSeek와 OpenAI-compatible follow-up request는 저장된 tool transcript를 replay 전에 정규화한다. 고아 `tool` result는 일반 context로 바꾸고 빠진 tool-call response는 synthetic result로 채워서 복구된 세션이 provider의 message validation에서 거부되지 않게 한다.
14. REPL은 compact branded banner로 시작하고, assistant 본문과 tool/verification activity line을 분리해서 보여준다.
15. `!cd`와 directory listing shortcut은 REPL current directory 기준으로 경로를 해석하되 workspace 경계를 고정한다. `!cd ..`는 workspace 또는 active worktree 내부에서는 상위 이동을 허용하지만 그 경계 밖으로는 나갈 수 없다.

### 런타임 상태 확인과 승인 상태

목적:
1. 현재 세션 상태와 적용된 설정값을 분리해서 본다.
2. write, diff, shell, git 승인 상태를 config 파일을 열지 않고 확인한다.
3. git 변경 작업을 일반 파일 수정과 다른 승인 축으로 관리한다.

대표 명령:
- `/status`
- `/config`
- `/provider status`

현재 동작:
1. `/status`는 현재 세션과 런타임 상태를 보여준다. 예를 들어 세션 id, approval 상태, selection, verification, MCP 카운트, runtime gate ledger가 여기에 들어간다. final answer나 write-side action 전에 `runtime_gate`, `review_freshness`, blocker/warning count, `next_command`를 보면 review/verification/completion audit 수리가 필요한지 판단할 수 있다.
2. `/config`는 현재 적용된 설정값을 보여준다. 예를 들어 provider 기본값, token limit, locale, hook, verification 기본값이 여기에 들어간다.
3. `/provider status`는 active provider, 정규화된 endpoint, API key 존재 여부, provider별 budget visibility를 보여준다.
4. OpenRouter에서는 `/provider status`가 live lookup으로 key-level `limit_remaining`, `usage`를 조회하고 management key면 account credits도 함께 보여준다.
5. DeepSeek에서는 API key가 설정되어 있으면 `/provider status`가 live `/user/balance`를 조회하고 provider의 dynamic concurrency 안내를 함께 보여준다.
6. OpenAI와 Anthropic에서는 `/provider status`가 임의의 live balance endpoint를 추정하지 않고 공식 문서 기준의 billing/usage visibility 제약을 보여준다.
7. `kernforge --version`, `kernforge -version`, `kernforge version`은 config/session 로드 전에 실행 파일 version을 출력한다. Windows release build에서는 PE `FileVersion`을 읽고, stamp가 없는 개발 빌드는 embedded app version으로 fallback한다.
8. `kernforge --help`의 일반 help 상단도 같은 version을 보여준다.
9. `Allow write?`와 `Open diff preview?`는 `a`로 현재 세션 동안 자동 승인할 수 있다.
10. `git_add`, `git_commit`, `git_push`, `git_create_pr` 같은 git 변경 도구는 별도의 `Allow git?` 세션 승인을 사용한다.
11. git 변경 도구는 일반 review/edit 턴이 아니라 사용자가 명시적으로 git 작업을 요청했을 때 사용하는 것이 기본이다.
12. `/hooks`는 `/status`와 같은 compact runtime gate summary를 출력하므로 hook/policy 확인 화면도 review freshness를 별도로 해석하지 않는다.

### 프롬프트 의도 라우팅

목적:
1. 분석/설명 요청을 기본적으로 read-only로 유지한다.
2. 명시적 수정 요청은 prose-only 조언으로 흐르지 않고 tool-driven edit으로 유지한다.
3. 일반 코드 리뷰 중 accidental git mutation이나 patch handoff를 줄인다.

현재 동작:
1. 분석, 설명, 진단, 검토, 문서화 요청은 동시에 수정까지 명시하지 않는 한 기본적으로 read-only investigation 모드로 처리된다.
2. 명시적으로 수정까지 요청한 프롬프트는 edit tool을 유지하고, 모델이 패치를 사용자에게 넘기려 하면 Kernforge가 한 번 더 직접 수정 도구 사용을 유도한다.
3. git stage/commit/push/PR 생성은 사용자가 해당 git 작업을 명시적으로 요청하지 않으면 막힌다.

### Self-Driving Work Loop

목적:
1. 사용자가 자연어로 구현/수정/실행 작업을 맡겼을 때 분석 답변에서 멈추지 않는다.
2. `TaskState`와 `TaskGraph`에 inspect, implement, verify, summarize 루프를 자동으로 만든다.
3. 편집 후 자동 검증 실패가 있으면 task를 완료 처리하지 않고 recovery 상태로 유지한다.

현재 동작:
1. "구현하자", "수정해줘", "남은 항목들을 처리해줘", "테스트까지 돌려서 끝내줘" 같은 요청은 self-driving loop 후보가 된다.
2. 독립 cross review route가 설정되어 있으면 reviewer/planner preflight에 사용할 수 있고, 없으면 active main model과 deterministic gate를 사용한다.
3. "방금 에러는 왜 난거야?", "현재 상태 알려줘", "분석해줘" 같은 read-only 요청은 자동 편집 루프를 켜지 않는다.

### Proactive Suggestion Dashboard

목적:
1. 현재 상황에서 Kernforge가 추천하는 다음 행동을 한 화면에 모은다.
2. analysis stale marker, verification gap, evidence gap, changed path를 같은 dashboard에서 비교한다.
3. 각 suggestion을 관련 dashboard와 연결해 바로 확인할 명령을 보여준다.

대표 명령:
- `/suggest`
- `/suggest accept <id>`
- `/suggest dismiss <id>`
- `/suggest mode <observe|suggest|confirm>`
- `/suggest-dashboard-html`

현재 동작:
1. `/suggest-dashboard-html`은 integrated signals와 suggested next actions를 함께 렌더링한다.
2. suggestion card에는 관련 명령, evidence ref, `/verify-dashboard-html`, `/evidence-dashboard-html`, `/analyze-dashboard` 같은 dashboard link chip이 포함된다.
3. 각 card에는 `/suggest accept <id>`와 `/suggest dismiss <id>` chip이 있어 같은 제안을 반복 노출하지 않도록 상태를 관리할 수 있다.
4. `/suggest` 후보는 `TaskGraph`의 `suggest:<id>` node로 동기화되어 ready/in_progress/completed/canceled 상태를 가진다.
5. `/suggest mode confirm` 상태에서 `/suggest accept <id>`를 실행하면 `/verify`, dashboard, `/docs-refresh`, `/automation add`, `/review pr` 같은 허용된 safe command만 자동 실행된다.
6. accepted/dismissed suggestion은 persistent memory에도 preference record로 남아 session을 넘는 반복 제안 억제와 선호 학습의 기반이 된다.

### Session Dashboard

목적:
1. 현재 thread, TaskGraph, automation 상태, changed files, artifact refs를 하나의 로컬 HTML 화면에서 본다.
2. 긴 세션을 다시 이어갈 때 transcript 전체를 읽지 않아도 핵심 상태를 확인한다.
3. due/failed automation을 open task graph node와 최근 runtime event 옆에서 같이 본다.

대표 명령:
- `/session-dashboard-html`
- `/events tail 20`
- `/events export`

현재 동작:
1. 현재 workspace에 `.kernforge/session_dashboard/latest.html`을 생성한다.
2. session/provider metadata, context size, task status count, open task graph node, automation due/failed/paused count, recent conversation event, changed files, background job/bundle, artifact ref를 포함한다.
3. dashboard 경로를 conversation event log에 기록하고, interactive mode에서는 가능한 경우 자동으로 연다.
4. `/events tail [n]`은 최근 session event를 JSONL record로 출력하고, `/events export [path]`는 `.kernforge/events/<session-id>.jsonl`과 `.kernforge/events/latest.jsonl`에 durable local event stream을 저장한다.

### Continuity Packet과 Local Jobs

목적:
1. 로컬 shell 명령 실패, verification 실패, stale background 작업에서 로그를 다시 붙여넣지 않고 자연스럽게 복구한다.
2. 긴 작업이 compact, 모델 전환, handoff 이후에도 이어질 수 있도록 로컬 resume packet을 남긴다.
3. background job/bundle을 모델 tool call로만 보지 않고 터미널에서 직접 확인하고 취소할 수 있게 한다.

대표 명령:
- `/continuity`
- `/continuity continue Codex parity work`
- `/recover`
- `/recover continue failed verification`
- `/recover execute-safe continue failed verification`
- `/completion-audit`
- `/completion-audit finish Codex parity work`
- `/jobs status`
- `/jobs check latest`
- `/jobs bundle latest`
- `/jobs cancel <job-id> stale verification`
- `/jobs cancel-bundle <bundle-id> superseded`
- `/worktree list`
- `/worktree enter`
- `/worktree attach <path> [branch]`

현재 동작:
1. `/continuity`는 `.kernforge/continuity/latest.md`와 `.kernforge/continuity/latest.json`을 생성한다.
2. packet에는 active/base workspace root, branch, provider/model, changed files, open task graph node, worktree lease, active edit loop, active failure repair, 최신 verification failure, background job/bundle, 최근 runtime error, artifact ref, recovery action, next command, continuation prompt가 포함된다.
3. 직접 실행한 `!shell` 실패는 `command_error` conversation event로 저장되므로, 이후 `/continuity`와 최근 오류 답변이 사용자의 로그 재입력 없이 실패 맥락을 회수할 수 있다.
4. `/jobs`는 저장된 background job/bundle 상태를 동기화해 보여주고, id 또는 `latest` 기준으로 직접 polling/cancel을 지원한다.
5. `/worktree list`는 session worktree, specialist editable worktree lease, `git worktree list --porcelain` 출력을 한 화면에 모아 root 전환이나 재개 전에 확인하게 한다.
6. `/worktree enter`는 `/worktree leave` 이후 기록된 isolated worktree로 다시 들어가고, `/worktree attach <path> [branch]`는 기존 worktree를 unmanaged session worktree로 붙인다.
7. `/recover`는 최신 error, verification failure, failure-repair 상태, background job, open task, next command를 `.kernforge/recovery/latest.md/json`에 더 좁은 failure runbook으로 저장한다. runbook에는 structured diagnosis, 안정적인 failure signature, action plan status, execution log가 포함된다.
8. `/completion-audit`는 finalizing 전에 blocker, warning, required artifact, 최신 verification, open task, background job, 최근 error, coding harness evidence를 `.kernforge/completion_audit/latest.md/json`으로 저장한다.
9. `/recover execute-safe`는 safe-auto recovery action만 실행하고 그 상태를 기록한다. shell replay는 chaining/redirection이 없는 whitelisted verification/status 명령으로 제한된다.
10. slash action은 실행 여부만 보지 않고 기록된 artifact를 다시 확인한다. `/verify` 실패 report와 `ready=false`인 `/completion-audit` 결과는 recovery action 실패로 승격되고, `stop_on_failure`가 있으면 뒤 액션은 skip된다.
11. safe-auto shell whitelist는 `go test`, `go vet`, `go list`, `git status`, `git diff --check` 같은 좁은 명령만 허용하며 외부 도구 실행이나 side artifact 생성을 유발할 수 있는 고위험 Go/Git flag는 거부한다.

### Autonomous Goals

목적:
1. 사용자가 Codex식 목표를 한 번 지정하면 Kernforge가 추가 확인 없이 계속 작업하게 한다.
2. inline prompt와 markdown 파일 목표를 모두 지원한다.
3. 구현, 자체 리뷰, 검증, completion audit, 최종 semantic review, recovery를 목표 완료 또는 구체 blocker 기록까지 반복한다.

대표 명령:
- `/goal "missing recovery test와 docs를 추가해"`
- `/goal start @GOAL.md`
- `/goal start --file GOAL.md --max-iterations 12`
- `/goal start --time-budget 10m --until-complete @GOAL.md`
- `/goal start --token-budget 120000 "context budget을 넘기지 않고 refactor를 끝내"`
- `/goal start --rollback-on-regression "refactor를 끝내고 verification green 유지"`
- `/goal start --no-run @GOAL.md`
- `/goal run latest`
- `/goal status`
- `/goal audit`
- `/goal complete`
- `/goal cancel`
- `kernforge -goal "verification policy change를 끝내"`
- `kernforge -goal "refactor를 끝내" -goal-token-budget 120000 -goal-max-iterations 12`
- `kernforge -goal-file GOAL.md`

현재 동작:
1. `/goal`은 session에 `GoalState`를 만들고 `.kernforge/goals/latest.md`와 `.kernforge/goals/latest.json`을 쓴다.
2. Markdown goal은 `@GOAL.md`, `--file GOAL.md`, `-goal-file` CLI flag로 지정할 수 있으며 one-shot `-goal`도 max-iteration, time-budget, token-budget, until-complete, rollback flag를 지원한다.
3. goal start는 실행 전에 acceptance contract, task graph, completion criteria, status artifact를 준비한다.
4. 각 iteration은 checkpoint 저장소가 설정된 경우 checkpoint를 남기고, 구현 prompt 뒤에 독립 review verdict gate를 실행한다.
5. review prompt는 가능한 경우 implementation reply, iteration 시작 checkpoint diff, git status/diff context, changed-file summary, 제한된 untracked 파일 excerpt 같은 실제 증거를 포함한다.
6. review가 `NEEDS_REVISION`이면 verification 전에 자동 repair pass를 한 번 더 실행한다. repair prompt는 구조화된 reviewer issue와 같은 implementation context를 보존하므로 worker는 짧고 모호한 revision summary가 아니라 실제 지적 사항을 받는다.
7. goal 실행 중에는 write, diff preview, shell, git approval을 session 안에서 bypass해서 사용자 확인으로 멈추지 않는다.
8. agent pass 뒤에는 Kernforge가 `/verify --full`, `/completion-audit`, audit ready 시 최종 semantic reviewer를 실행하고, 필요 시 `/recover execute-safe` 또는 semantic repair pass를 실행한 뒤 다음 iteration으로 넘어간다.
9. 최종 semantic reviewer도 같은 workspace evidence 모델을 받으며, 증거가 부족하거나 실제 작업을 확인할 수 없으면 approval이 아니라 `NEEDS_REVISION`으로 처리한다.
10. progress ledger는 changed files, verification, audit blockers/warnings, review verdict, final semantic verdict, no-progress count, repeated failure signature, token usage estimate, command history를 기록한다.
11. completion audit이 `ready=true`이고 최종 semantic review가 `APPROVED`를 반환해야 완료된다. 그 전에는 취소, 회복 불가능 runtime error, iteration/time/token cap, repeated no-progress/failure 감지에 걸릴 때까지 계속 반복한다.
12. `/goal run`은 pending 또는 blocked goal을 최신 영속 상태에서 재개한다.
13. `/goal audit`은 구현 pass 없이 goal objective 기준 completion audit만 다시 실행하고 goal을 완료 처리하지 않는다.
14. `/goal complete`는 명시적 완료 게이트다. audit을 다시 실행하고 semantic review를 거쳐 둘 다 통과할 때만 complete로 표시한다.

### Local Automations MVP

목적:
1. Codex식 recurring workflow의 최소 기반을 로컬 session 상태와 시간 기반 due 판단으로 제공한다.
2. 반복 verification과 PR review report 생성을 suggestion/TaskGraph 흐름과 연결한다.
3. GitHub API나 cloud job 없이도 로컬에서 due 확인, safe command 실행, report 기록 루프를 검증할 수 있게 한다.

대표 명령:
- `/automation`
- `/automation add recurring-verification /verify`
- `/automation add recurring-verification --every 2h /verify`
- `/automation add pr-review /review pr`
- `/automation due`
- `/automation digest`
- `/automation monitor`
- `/automation monitor --notify`
- `/automation watch --interval 5m --notify`
- `/automation daemon-start --interval 5m --notify`
- `/automation daemon-status`
- `/automation daemon-stop`
- `/automation notify --webhook-url https://example.invalid/kernforge`
- `/automation notify`
- `kernforge -command "/automation monitor --notify"`
- `/automation run-due`
- `/automation run <id>`
- `/automation pause <id>`
- `/automation resume <id>`
- `/automation remove <id>`
- `/review pr`
- `/review pr --github`
- `/review pr --github --draft-comments`
- `/review pr --github --post-comments`
- `/review pr --resolve-thread <thread-id>`
- `/review pr --draft-issue`
- `/review pr --create-issue`
- `/review pr --create-issue --label bug,security --assignee <login> --milestone "May 2026"`

현재 동작:
1. automation slot은 session JSON의 `automations`에 저장된다.
2. `--every`, `--hourly`, `--daily` schedule은 `next_run_at`과 `NextRunHint`를 만들고, `/automation due`에서 실행 시점이 지난 슬롯을 보여준다.
3. `/automation run <id>`와 `/automation run-due`는 safe command dispatcher를 통해 등록된 명령을 실행한다.
4. `/automation digest`, `/automation monitor`, `/status`, REPL 시작 notice는 due/failed/paused automation 수와 실패 결과를 노출해 반복 작업 실패를 놓치지 않게 한다.
5. `/automation notify`와 `/automation monitor --notify`는 `.kernforge/automation/latest_digest.md`를 써서 외부 watcher, CI step, shell script가 terminal output을 긁지 않고 최신 automation 상태를 읽게 한다.
6. `/automation notify|monitor|watch --webhook-url <url>`은 digest JSON을 외부 receiver로 POST한다. webhook URL은 conversation event에 redacted 형태로 저장된다.
7. `/automation watch [--interval 5m] [--cycles N|--once] [--notify] [--webhook-url <url>]`는 foreground standing monitor loop를 실행한다. 각 cycle은 due safe automation을 실행하고 digest를 출력하며, 필요하면 digest artifact를 갱신하거나 webhook을 전송한다.
8. `/automation daemon-start|daemon-status|daemon-stop`은 process-detached local automation watcher를 관리하고 `.kernforge/automation/daemon.json` 및 `daemon.log`에 state/log를 남긴다.
9. `-command "/automation monitor --notify"`는 Windows Task Scheduler, service wrapper, CI가 REPL 없이 slash command를 실행할 수 있게 한다.
10. `/review pr`는 git status, diff stat, changed files, review checklist를 `.kernforge/pr_review/latest.md`에 기록하고 conversation event에 artifact ref를 남긴다.
11. `/review pr --github`는 `gh pr view --json ...`으로 현재 branch PR metadata, review decision, comments, checks 요약을 같은 report에 붙인다. `gh`가 없거나 인증/PR이 없으면 로컬 section은 유지하고 GitHub unavailable reason을 기록한다.
12. `/review pr --draft-comments`는 `.kernforge/pr_review/comments.md`에 file-level review comment draft를 만들며 GitHub에는 게시하지 않는다.
13. `/review pr --post-comments`는 draft 생성 후 `gh pr review --comment --body-file .kernforge/pr_review/comments.md`를 실행한다. 이 write-side 작업은 명시적 명령에서만 허용되고 suggestion accept나 scheduled automation에서는 차단된다.
14. `/review pr --resolve-thread <thread-id>`는 `gh api graphql`로 GitHub `resolveReviewThread` mutation을 실행한다. 이 write-side 작업도 명시적 명령에서만 허용된다.
15. `/review pr --draft-issue`는 `.kernforge/pr_review/issue.md`를 쓰고, `/review pr --create-issue`는 `gh issue create --title ... --body-file ...`로 해당 draft를 게시한다. issue 생성도 명시적 명령에서만 허용된다.
16. issue draft와 create 호출은 반복/쉼표 구분 `--label`, 반복/쉼표 구분 `--assignee`, quoted `--milestone` 값을 받는다. create mode에서는 해당 값들을 `gh issue create` flag로 그대로 넘긴다.
17. verification gap이나 dirty diff가 있으면 `/suggest`가 recurring verification/PR review automation 등록을 다음 행동으로 제안할 수 있다.

### Delegation Handoff

목적:
1. Codex cloud task나 다른 로컬 agent에게 현재 작업을 넘길 때 필요한 최소 상태를 transcript 전체보다 작게 저장한다.
2. changed files, open task graph node, 최근 event/artifact, verification 상태, 이어받기 prompt를 한 곳에 묶는다.
3. 다른 agent나 cloud task의 result packet을 import해 task status와 artifact ref를 현재 session에 merge한다.

대표 명령:
- `/handoff`
- `/handoff continue automation scheduler work`
- `/handoff import .kernforge/handoff/imports/cloud_result.json`

현재 동작:
1. `.kernforge/handoff/latest.md`와 `.kernforge/handoff/latest.json`을 생성한다.
2. 생성된 artifact ref는 conversation event에 남는다.
3. 다른 agent는 `Suggested Prompt`와 `Changed Files`, `Open Tasks`, `Artifact Refs`부터 읽고 이어서 작업할 수 있다.
4. `/handoff import <path>`는 JSON 또는 markdown result를 `.kernforge/handoff/imports/*.json` 및 `*.md`로 정규화하고 conversation event를 남기며, `completed_tasks` ID가 TaskGraph node와 맞으면 completed로 표시한다.

### Coding Harness와 Repair Loop

목적:
1. 모델이 실제 workspace 상태와 다른 최종 답변을 내지 않도록 final answer 직전에 검문한다.
2. Codex식 작업 완료 루프에 필요한 acceptance, artifact, scenario, subagent evidence, test impact, open task, background job, failure repair, completion audit, user-change isolation을 구조화한다.
3. blocker는 최종 답변을 막고 모델에게 수정, 재검증, disclosure 중 하나를 요구한다.

현재 동작:
1. `AcceptanceContract`는 사용자 요청에서 기대 동작, non-goal, 변경 surface, required artifact, verification 필요성을 추출한다.
2. patch transaction은 edit tool, scoped shell write, 변경 path, fingerprint, 실패한 tool call을 기록해 "무엇을 실제로 바꿨는지"를 final harness에 제공한다.
3. artifact quality harness는 요청/주장된 문서 artifact를 읽어 placeholder/TODO, 너무 얇은 본문, 요청 주제 coverage 부족을 blocker 또는 warning으로 분류한다.
4. scenario replay harness는 `when/expected/but observed` 형태의 bug scenario에서, 코드 변경 후 해결을 주장하려면 replay/verification 결과 또는 "실행하지 못했다"는 명시적 disclosure를 요구한다.
5. subagent orchestration harness는 root-cause 답변이 worker evidence와 reviewer validation을 실제 causal bridge로 연결하는지 확인한다. worker가 보고한 문제가 사용자 증상으로 이어질 수 있는지 reviewer가 검증하지 못했거나 review failure를 숨기면 blocker가 된다.
6. test impact harness는 code-like path 변경을 보고 verification planner가 추천하는 좁은 명령을 기록하고, 성공한 verification evidence가 없으면 warning으로 남긴다.
7. artifact를 만들 수 있는 build/test shell 명령은 명시적인 command lifecycle을 가진다. Kernforge는 실행 전에 이를 이미 시작된 shell job이 아니라 검증 승인 요청으로 표시한다. 비대화형 `-prompt -y` 실행은 diff preview와 같은 기준으로 이 prompt를 자동 승인하고, bypass가 아닌 비대화형 실행은 승인을 추측하지 않고 skipped로 남긴다. 사용자가 pinned verification prompt를 거절하면 tool result는 `verification_status=skipped`, `command_execution_status=declined`로 남고, 성공 검증 evidence나 pending verification 해소로 계산되지 않으며, 사용자가 명시적으로 검증 실행을 승인하기 전에는 같은 턴에서 재시도하거나 background job을 poll하지 않는다. declined 또는 prompt 실패 자동 검증도 skipped `VerificationReport`로 저장하고 verification pending check를 유지해, final answer가 검증 완료가 아니라 검증 미실행 사실을 말하도록 한다. 단, generated document만 만든 artifact 턴은 결정적 artifact-quality check가 문서를 승인하고 final answer가 근거 없는 검증 성공을 주장하지 않으면 이 pending 상태에서 제외해 self-driving state를 완료한다. background verification 시작은 `verification_status=pending`, `verification_evidence=false`이고, exit code 0으로 완료된 background check만 성공 evidence가 된다. 이 terminal decision 또는 skipped automatic verification 이후 같은 턴의 검증 재시도와 `latest` background poll은 새 shell/progress status를 내기 전에 synthetic `NOT_EXECUTED` tool result로 접어, 모델이 가짜 복구 시도를 반복하지 않고 미검증 사실을 보고하도록 유도한다. 최종 답변이 검증 미실행을 고지해도 successful verification evidence가 생기는 것은 아니므로, 변경 path에 성공 검증이 없으면 edit-loop ledger는 `risk_accepted`로 남는다. background job bundle은 job 목록을 `job_entries`에 저장하고, scalar `job_status`는 단일 job 상태에만 쓴다. generated analysis, fuzz, manifest finding은 command가 비어 있는 informational verification evidence로만 기록되며 output text로 보고할 뿐 shell로 실행하지 않는다.
8. job supervisor harness는 background job/bundle이 실패, stale, running 상태인데 최종 답변에서 숨기지 않도록 막는다.
9. `/completion-audit`는 final readiness gate를 `.kernforge/completion_audit/latest.md/json`으로 외부화해서, 사람이나 scheduler가 모델 턴 밖에서도 blocker와 warning을 볼 수 있게 한다.
10. failure repair harness는 verification 실패 시 첫 의미 있는 실패 줄, 반복 횟수, 좁은 재실행 명령, 다음 repair step을 active context로 유지한다.
11. user-change isolation은 turn 시작 이후 사용자가 target path를 바꿨는데 agent가 그 파일을 덮어쓰려 하면 edit를 막고 fresh read와 merge-aware edit을 요구한다.
12. final-answer reviewer는 unresolved verification, coding harness blocker, 실제 patch transaction 변경이 있을 때만 추가로 돈다. 계획이 있거나 task graph가 있다는 이유만으로는 추가 reviewer/revision 왕복을 만들지 않는다.

실무 해석:
1. "완료했습니다"라고 말하기 전에 Kernforge는 실제 artifact와 verification evidence를 다시 본다.
2. root-cause 작업에서는 "그럴듯한 원인"보다 `trigger -> invalid_state -> state_transition -> missing_guard -> symptom` 인과 연결이 중요하다.
3. blocker가 뜨면 사용자는 같은 요청을 다시 설명할 필요가 없다. harness feedback이 다음 모델 턴에 들어가서 수정/검증/disclosure 중 필요한 행동으로 이어진다.

### 대형 파일 읽기 재사용과 반복 스캔 완화

목적:
1. 매우 큰 소스 파일에서 `read_file` 반복 호출로 인한 낭비를 줄인다.
2. `grep` 결과만 봐도 최근 읽은 문맥과의 거리 정보를 드러낸다.
3. 이미 본 구간을 다시 크게 스캔하는 루프를 더 빨리 끊는다.

현재 동작:
1. `read_file`는 변경되지 않은 동일 범위, 포함되는 하위 범위, 부분 겹침 범위를 먼저 재사용하고 정말 필요한 줄만 새로 읽는다.
2. cached `read_file` 응답은 `NOTE:` 접두사를 붙여서 모델이 "새 증거"가 아니라 "이미 본 문맥"으로 해석하도록 돕는다.
3. 같은 파일을 반복해서 읽는 흐름은 cached 신호를 보면 더 이른 시점에 경고를 받아 같은 chunk 재읽기를 줄인다.
4. `grep`는 최근 읽은 범위 내부 매치에는 `[cached-nearby:inside]`를 붙인다.
5. `grep`는 최근 읽은 범위 근처 매치에는 `[cached-nearby:N]`를 붙여서 다음 `read_file` 범위를 더 좁게 잡도록 유도한다.
6. 파일 크기나 수정 시간이 바뀌면 이전 read hint는 자동으로 무시된다.

실무 해석:
1. `NOTE: returning cached content...`가 보이면 같은 범위를 다시 읽기보다 빠진 인접 범위만 확인하는 편이 맞다.
2. `grep` 결과에 `[cached-nearby:inside]`가 붙으면 대개 큰 범위 재스캔보다 바로 수정, 설명, 또는 아주 작은 인접 범위 확인이 더 낫다.
3. `grep` 결과에 `[cached-nearby:2]`, `[cached-nearby:5]`처럼 붙으면 그 작은 gap만 읽도록 유도하는 신호로 보면 된다.

### 2.0 Project Analysis

목적:
1. 큰 워크스페이스의 구조를 재사용 가능한 문서로 만든다.
2. 여러 worker와 reviewer 패스로 분석을 분산한다.
3. 후속 작업용 `latest` knowledge pack과 performance lens를 유지한다.
4. incremental 모드에서는 바뀌지 않은 shard를 재사용한다.
5. structural index, Unreal semantic graph, vector corpus까지 후속 자동화에 재사용할 수 있게 남긴다.
6. cached deep-structure 답변이 source-derived invariant와 맞는지 확인할 수 있도록 deterministic architecture fact를 남긴다.
7. 실행 마지막에 눈에 띄는 `Analysis artifacts:` 블록과 `Analysis handoff`를 출력해 사용자가 순서를 외우지 않아도 dashboard, fuzz campaign automation, target drilldown, verification으로 이어갈 수 있게 한다.
8. local provider 또는 명시적으로 route-limited인 환경에서는 같은 모델 route의 worker/reviewer 요청을 전역 scheduler로 제한해 provider 포화와 저신뢰 placeholder 연쇄를 줄인다.
9. shard 제한을 직접 설정하지 않은 local model 환경에서는 Kernforge가 shard 크기를 자동 조절하고, 최종 timeout 또는 5xx/overload 계열 provider error로 run이 멈추면 더 작은 shard로 한 번 자동 재실행한다.
10. worker slot 수, shard wave, 완료/실패 shard 합계, cache/review 상태, analysis stage와 shard 이름이 붙은 model wait event를 실행 중 progress로 보여준다.

대표 명령:
- `/analyze-project [--path <dir>] [--mode map|trace|impact|surface|security|performance] [goal]`
- `/docs-refresh`
- `/analyze-performance [focus]`
- `/set-analysis-models`

goal은 선택값이다. 생략하면 Kernforge가 선택한 mode와 path를 기준으로 실용적인 기본 goal을 만든다.
후속 모드는 가능한 경우 이전 `map` 실행을 baseline 구조 지도로 자동 로드한다. 그래서 `trace`, `impact`, `surface`, `security`, `performance`는 같은 shard cache를 공유하지 않으면서도 architecture map을 출발점으로 삼는다.
confirmation 전에 analysis plan이 선택된 `baseline_map`을 출력하므로 어떤 map run을 재사용할지 사용자가 먼저 확인할 수 있다.
큰 analysis run은 provider failure tolerant하게 동작한다. worker/reviewer rate limit은 저신뢰 shard failure로 기록하고, 최종 synthesis 요청이 실패하면 local fallback document를 생성한다.
LM Studio, vLLM, llama.cpp, Ollama 같은 local-model provider에서는 `max_files_per_shard` / `max_lines_per_shard`가 비어 있으면 confirmation 전에 provider, 모델 크기, max token, request timeout을 보고 값을 조정한다. 일반 request retry를 모두 소진한 뒤에도 timeout, 5xx, overload, empty response, connection reset 같은 provider-pressure error로 run이 끝나면 Kernforge는 `adaptive_retry_shards` 줄을 출력하고 더 작은 shard 제한으로 한 번 다시 실행한다. rate limit은 shard를 줄이면 요청 수가 늘 수 있으므로 이 방식으로 재시도하지 않는다.
worker와 reviewer가 같은 provider/model/base_url/reasoning_effort route를 쓰는 구성에서는 shard 실행이 model route limit 이하로 제한된다. local provider의 기본 route limit은 1이므로 직렬 실행이 기본이지만, cloud/API route는 `model_routes`가 그렇게 지정하지 않는 한 강제로 1로 낮추지 않는다.
reasoning effort는 하나의 전역 override가 아니라 configured model target별로 저장된다. main profile, optional cross review route, analysis worker/reviewer, specialist profile이 각각 다른 `reasoning_effort`를 가질 수 있다. effort 지원 main/analysis/specialist target을 새로 선택했는데 undefined이면 해당 target은 기본 `low`로 저장되지만, cross review route는 기본이 최소 `high`이며 저장된 `low`/`medium` 값도 runtime에서는 `high`로 올려 실행한다.
엄격 생략 재시도는 finding field 기준으로만 동작한다. 구조화된 finding 자체가 생략, 잘림, 또는 생략 표식이 포함된 weak output일 때 재시도하고, summary 같은 prose 영역에 `omitted`류 문구가 있어도 usable structured finding이 완성돼 있으면 그대로 수락한다.
cross review route, analysis worker/reviewer, specialist의 route별 `base_url`은 안전하게 생략할 수 있다. 같은 provider route는 main endpoint를 상속하고, 다른 provider route는 직접 지정한 endpoint 또는 해당 provider 기본 endpoint를 사용하므로 proxy/local route가 조용히 엇갈리지 않는다.
main provider/model만 바꾸면 명시적인 analysis worker/reviewer profile은 유지된다. 이전에 따로 둔 route가 아니라 현재 main model을 다시 상속시키고 싶으면 `/set-analysis-models clear`를 사용한다.
`/analyze-project`는 docs, manifest, dashboard를 기본 생성한다. 예전 `--docs` 입력은 하위 호환용으로만 조용히 허용되고 help와 completion에는 나오지 않는다. 저장된 최신 run에서 문서만 다시 만들 때는 `/docs-refresh`를 쓴다.
생성 문서 세트에는 run 마지막에 출력된 assistant-facing final synthesis를 그대로 보존하는 `FINAL_REPORT.md`와 architecture, security, entrypoint, build artifact, verification, fuzz target, operation 운영 문서가 함께 들어간다.
dashboard는 이 문서들을 inline Markdown viewer로 열며, 최종 리포트나 다른 generated document가 기본 패널에서 읽기 길면 `Reader` 버튼으로 full-window reading mode를 사용할 수 있다.
goal이 영어 또는 한국어 출력을 명시하면 감지된 대화 언어만 따르지 않고 worker와 synthesis prompt에 그 요청을 전달한다. 실행 중 model-wait/progress 문구는 UTF-8 rune boundary에서 잘라 localized status text가 mojibake로 깨지지 않게 한다.

역할 분리:
1. `README_kor.md`는 제품 범위, 대표 명령, 산출물 위치를 빠르게 확인하는 문서다.
2. 이 feature guide는 조사, simulation, root-cause, fuzzing, verification, evidence, memory를 어떤 순서로 운영할지 설명하는 문서다.
3. `analyze-project`가 생성하는 docs는 특정 run의 source anchor, confidence, stale/invalidation marker를 담은 프로젝트별 운영 지식 베이스다.

모드 요약:
1. `map`은 기본 모드이며 architecture ownership과 module boundary를 우선 본다.
2. `trace`는 runtime flow, caller/callee chain, dispatch 순서를 더 강조한다.
3. `impact`는 변경 영향 범위, downstream dependency, 재검증 범위를 더 강조한다.
4. `security`는 trust boundary, validation, privileged surface를 더 강조한다.
5. `performance`는 startup cost, hot path, contention, blocking chain을 더 강조한다.

특히 좋은 상황:
1. 큰 코드베이스에 처음 들어가서 즉석 요약으로는 부족할 때
2. startup, integrity, ETW, scanner, compression, memory, upload path를 같이 봐야 할 때
3. 이후 review와 verification이 안정적인 구조 지식을 공유해야 할 때
4. Unreal 5처럼 module, target, reflection, replication, asset/config coupling이 동시에 얽힌 코드베이스를 다뤄야 할 때

현재 project analysis가 추가로 남기는 핵심 산출물:
1. `snapshot`: 스캔 결과와 runtime/project edge를 담는 구조화된 입력
2. `structural index`: symbol anchor, reference, build context, build ownership edge, call edge, overlay를 함께 담는 정밀 인덱스
3. `unreal graph`: UE project/module/network/asset/system/config를 구조화한 semantic graph
4. `architecture facts`: domain hint, top-level directory fact, critical anchor, dispatch/registration flow, boundary fact, answer invariant를 담는 deterministic fact pack
5. `knowledge pack`: 사람이 읽는 architecture digest와 subsystem 요약
6. `vector corpus`: 임베딩 친화적인 project/subsystem/shard 문서 묶음
7. `vector ingest exports`: pgvector, sqlite, qdrant로 넘기기 쉬운 seed 파일

대규모/UE 프로젝트에서 특히 달라진 점:
1. semantic shard planner가 `startup`, `build_graph`, `unreal_network`, `unreal_ui`, `unreal_ability`, `asset_config`, `integrity_security`, `unreal_gameplay` 영역을 우선 분리한다.
2. worker와 reviewer prompt가 shard 목적에 맞는 semantic focus와 review checklist를 받는다.
3. incremental reuse가 file hash뿐 아니라 semantic fingerprint 변화까지 본다.
4. `.uproject`, `.uplugin`, `.Build.cs`, `.Target.cs`, `compile_commands.json`를 build alignment에 반영해 재사용 가능한 build context를 만든다.
5. Go/C++/C# source anchor를 symbol record, line range, call edge, build ownership edge, security overlay까지 포함하는 구조 자산으로 올린다.
6. `trace`, `impact`, `security` retrieval은 키워드 hit만 보는 대신 graph neighborhood를 확장하고 `build_context_v2`, `path_v2` 근거를 남긴다.
7. C++ anchor parser는 template out-of-line method, operator, `requires`, `decltype(auto)`, API macro가 낀 scope, friend function을 처리한다.
8. 결과 문서에는 subsystem별 invalidation reason, evidence, diff, top change class, graph section stale marker가 같이 남는다.
9. dashboard의 stale diff는 graph 관련 변경을 trust-boundary, data-flow, project-edge 섹션 앵커로 직접 연결한다.
10. 저장 산출물에는 snapshot, structural index, Unreal semantic graph, vector corpus, ingestion seed 파일까지 포함되어 후속 retrieval 파이프라인에 재사용할 수 있다.
11. architecture fact pack은 worker/reviewer/synthesis prompt와 cached answer pack에 함께 들어가며, 추가 tool call 없이 답할 때도 구조 답변이 소스 근거에서 벗어나지 않도록 한다.
12. C/C++와 드라이버 지향 scanner는 파일명 휴리스틱만 보지 않고 dispatch table, unload/finalize path, callback registration, filter registration, alias, macro, include 기반 registration helper까지 찾는다.
13. `.kernforge/analysis/latest`는 run마다 교체되어 반복 테스트 중 이전 산출물이 새 retrieval에 섞이지 않는다.
14. goal에 특정 디렉토리나 하위 영역이 드러나면 해당 경로 위주로 분석 shard를 좁힐 수 있다.
15. interactive 실행에서는 hidden directory나 external-looking directory를 분석 전에 제외할지 확인할 수 있다.

### Root-Cause Investigation

목적:
1. 사용자가 보고한 증상을 source evidence 기반 root-cause 후보로 좁힌다.
2. 큰 코드베이스에서 문제와 관련 있어 보이는 파일/심볼만 골라 1개부터 8개 worker shard로 분석하되, 동시 model call 수는 `model_routes`를 따른다.
3. worker가 제시한 문제가 실제 사용자 증상으로 이어질 수 있는지 reviewer와 deterministic gate가 다시 검증한다.

대표 명령:
- `/find-root-cause <problem description>`
- `/find-root-cause --pattern-pack <path-or-dir> <problem description>`
- `/root-cause-patterns list [--type <project_type>] [--json]`
- `/root-cause-patterns match <problem symptom> [--json]`
- `/root-cause-patterns github-search [--type <project_type>] [--limit 20] [--out .kernforge/root_cause/github_issues.json] [query words...]`
- `/root-cause-patterns normalize --in .kernforge/root_cause/github_issues.json --out .kernforge/root_cause/pattern_pack.json [--type <project_type>]`
- `/root-cause-patterns validate [--in <pattern_pack.json>] [--json]`

좋은 프롬프트 형태:

```text
/find-root-cause In <component/feature>, when <input/command/event sequence/state>, expected <normal behavior or invariant>, but observed <failure>. Frequency/env: <how often and where>. Repro/log/value: <exact prompt, API call, command, DB value, or log line>.
```

예시:

```text
/find-root-cause 내 게임에서 파티원을 초대하고 추방하다 보면 파티원 제한 숫자를 넘어서서 파티원을 초대할 수 있게 돼
/find-root-cause 내 Win32 서비스 프로세스가 sc stop으로 종료되지 않아
```

현재 동작:
1. 프롬프트가 비어 있으면 usage와 예시를 출력한다.
2. affected component, trigger/repro, observed failure, expected behavior/invariant가 불명확하면 부족한 부분을 보여 주고 더 정확한 `/find-root-cause ...` 명령을 다시 입력하게 한다.
3. source hint와 optional model clarity check가 있어 한국어 자연어 증상이 단순 키워드 부족 때문에 거절되는 일을 줄인다.
4. workspace scan, source path/symbol match, built-in pattern prior, explicit `--pattern-pack`을 결합해 후보 code match를 만든다.
5. code size와 후보 수에 따라 worker 수를 1개부터 최대 8개까지 추정하고 shard를 나눈다.
6. worker는 입력 파라미터, DB/config 조회값, cache/state/counter/id/enum/null/lifecycle 값이 코드가 예상한 범위를 벗어날 때 어떤 전이가 생기는지 집중해서 본다.
7. worker 후보는 causal chain, evidence file/function, out-of-range case, required runtime observation, probe, disproof condition을 포함해야 한다.
8. reviewer는 worker 후보가 사용자가 말한 증상과 겹치는지, causal stage가 빠지지 않았는지, 필요한 증거가 있는지 확인한다.
9. reviewer가 더 많은 proof를 요구하면 `evidence_requests`가 focused shard로 이어지고, rejected candidate는 audit trail에 남아 regression prior로 재사용된다.
10. deep verification은 reviewer-approved 후보를 symbol-aware excerpt로 다시 확인하고 confidence breakdown을 보정한다.
11. 최종 문서는 root cause 후보를 cluster/dedup하고, confidence, concrete instrumentation, verification probe, "이 조건이면 이 후보는 root cause가 아니다"라는 disconfirmation 조건을 함께 제공한다.

Pattern pack 운영:
1. 내장 pack은 Windows user service, Windows kernel driver, Unreal client/server, web backend, Go/CLI agent 등 반복 버그 패턴을 search prior로 제공한다.
2. `/root-cause-patterns match`는 현재 workspace type과 증상 텍스트를 기준으로 pattern 후보를 보여준다.
3. `/root-cause-patterns github-search`는 GitHub issues API에서 closed bug/fix/root-cause 신호가 있는 issue corpus를 수집한다.
4. `/root-cause-patterns normalize`는 issue corpus를 provisional pattern pack으로 바꾼다.
5. `/root-cause-patterns validate`는 pattern 품질 문제를 찾는다.
6. pattern pack은 증거가 아니라 prior다. `/find-root-cause`는 항상 현재 소스, worker evidence, reviewer causality validation을 다시 요구한다.

### Source-Level Function Fuzzing

목적:
1. 공격자가 입력 파라미터를 정교하게 조작했을 때 어떤 guard, probe, copy, dispatch, cleanup 경로가 열리는지 소스만으로 본다.
2. 단순 리뷰보다 더 구체적으로 "어떤 비교식을 어떤 값으로 뒤집으면 어느 sink가 열린다"를 보여준다.
3. 함수 하나 또는 파일 하나만 지정해도 실제 호출 흐름을 따라 input-facing path를 빠르게 triage한다.

대표 명령:
- `/fuzz-func <function-name>`
- `/fuzz-func <function-name> --file <path>`
- `/fuzz-func <function-name> @<path>`
- `/fuzz-func <function-name> --source-scan focused`
- `/fuzz-func <function-name> --source-scan full`
- `/fuzz-func <function-name> --no-source-scan`
- `/fuzz-func --from-candidate <candidate-id>`
- `/fuzz-func --file <path>`
- `/fuzz-func @<path>`
- `/fuzz-func status`
- `/fuzz-func show [id|latest]`
- `/fuzz-func list`
- `/fuzz-func continue [id|latest]`
- `/fuzz-func language [system|english]`
- `/fuzz-campaign`
- `/fuzz-campaign run`
- `/source-scan run`
- `/source-scan run --limit 50`
- `/source-scan run --only-slugs probe-copy-size-drift,double-fetch-user-buffer`
- `/source-scan run --files driver/nsi.c,api/registry.c`
- `/source-scan list`
- `/source-scan show [id|latest]`
- `/create-driver-poc <driver-name> [--type objectfilter|minifilter|registryfilter|wfpcallout]`

특히 좋은 상황:
1. IOCTL handler, parser, validator, buffer-processing 함수처럼 공격자 입력이 직접 들어가는 경로를 빨리 triage하고 싶을 때
2. large driver/project에서 의심 파일만 알고 있고, 어떤 함수부터 보는 게 좋은지 아직 모를 때
3. runtime harness 없이도 source-only 기준으로 크기 drift, branch flip, check/use desync, dispatch divergence를 먼저 보고 싶을 때

현재 동작:
1. 함수명을 주면 심볼을 resolve하고, 파일만 주면 include/import와 실제 호출 흐름을 함께 따라가 representative root를 고른다.
2. `analyze-project`나 `structural_index_v2`가 없어도 워크스페이스 스캔과 on-demand semantic index 복원으로 planning이 된다.
3. 실제 소스에서 guard/probe/copy/dispatch/cleanup 관찰을 추출하고, 그 관찰을 기반으로 공격자 입력 상태를 합성한다.
4. 위험도가 높은 finding에는 구체 입력 예시, 소스에서 뽑은 비교식, 최소 반례, 분기 뒤 대표 결과, 후속 호출 체인이 붙는다.
5. 결과는 `결론`, `위험도 점수표`, `상위 예측 문제`, `소스 기반 공격 표면` 순으로 나와서 핵심 finding을 먼저 읽기 쉽다.
6. 기본적으로 `/fuzz-func`는 매칭되는 `/source-scan` 후보를 재사용하고, 없으면 target과 reachable file만 focused source-scan으로 훑은 뒤 plan에 연결한다. 필요하면 `--source-scan off`, `--source-scan focused`, `--source-scan full`, `--no-source-scan`으로 제어한다.
7. `/source-scan run`은 function-window source candidate를 점수순으로 저장하고, 명시 handoff가 필요하면 `/fuzz-func --from-candidate <candidate-id>`를 다음 명령으로 안내한다. 각 candidate는 evidence span, 파일/심볼 fingerprint, confidence breakdown, dataflow/control-flow fact, stale-source 상태, native feedback calibration을 저장한다.
8. built-in source matcher에는 기존 probe/copy, dispatch, IRQL, callback, minifilter, Unreal RPC, telemetry parser 신호에 더해 Windows kernel double-fetch, IOCTL output infoleak, WDF request buffer size drift, integer allocation overflow, pool/refcount lifetime surface가 포함된다.
9. `/create-driver-poc <driver-name> [--type objectfilter|minifilter|registryfilter|wfpcallout]`은 x64 전용 C++20 MSVC/WDK POC driver template을 생성한다. `--type` 생략 시 기존 WDM SCM/IOCTL ping POC를 유지하고, typed template은 object manager 프로세스/쓰레드 access filtering, filesystem minifilter open/rename/delete 유저 모드 판단 메시징, registry create/open/set/delete/rename callback 차단, WFP outbound callout 차단 계약을 생성한다.
10. `compile_commands.json`이나 build context가 충분하면 후속 네이티브 fuzzing으로 이어갈 수 있고, 부족하면 왜 막히는지 먼저 설명한 뒤 확인을 받는다.
11. 결과 산출물은 `.kernforge/fuzz/<run-id>/` 아래에 `report.md`, `harness.cpp`, `plan.json` 등으로 저장된다.
12. `/fuzz-func`는 source-only scenario가 준비되면 campaign handoff를 자동 출력하므로, 사용자는 campaign 내부 단계를 배우지 않고 `/fuzz-campaign run`으로 이어갈 수 있다.
13. `/fuzz-campaign`은 다음 권장 campaign 단계를 보여주고, `/fuzz-campaign run`은 campaign 생성, 최신 run attach, source-only scenario의 `corpus/<run-id>/` 승격, dedup된 finding lifecycle과 coverage gap 갱신, libFuzzer log, llvm-cov text, LCOV, JSON coverage summary 수집, sanitizer report, Windows crash dump, Application Verifier, Driver Verifier artifact 수집, native run 결과의 report/evidence 기록 같은 안전한 자동 단계를 수행한다.
14. campaign manifest에는 target, seed, native result, coverage report, sanitizer/verifier artifact, evidence id, source anchor, verification gate, tracked-feature gate를 연결하는 finding 목록, dedup key, duplicate count, 병합된 native/evidence link, parsed coverage report, run artifact, coverage gap, artifact graph가 포함된다.
15. native crash finding은 crash fingerprint, source anchor, suspected invariant 기준으로 병합되어 반복 실행이 하나의 tracked issue를 강화한다.
16. coverage gap은 다음 생성 `FUZZ_TARGETS.md` refresh에 반영되어 아직 충분히 실행되지 않은 seed target이 ranking feedback을 받는다.
17. `/fuzz-func ` 자동완성은 함수명/파일 사용 힌트를 먼저 보여주고, `@` 이후에는 실제 파일 후보 목록으로 바뀐다.

실무 해석:
1. `가장 유용한 분기 차이 요약`은 사용자가 가장 먼저 볼 한 줄 결론이다.
2. `가상의 구체 입력 예시`는 Kernforge가 내부 분석에 사용한 입력 모델이지, 사용자가 그대로 수동 재현하라는 절차는 아니다.
3. `소스 기반 공격 표면`은 실제 함수 본문에서 뽑은 근거이므로 가장 신뢰도가 높은 섹션이다.
4. score가 높더라도 exploit/helper 코드가 근거인 finding은 noise 가능성이 있으므로, 먼저 target-side source excerpt를 확인하는 편이 좋다.

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
5. 모델 요청 timeout은 `request_timeout_seconds`로 조정할 수 있고, `max_request_retries`와 `request_retry_delay_ms`로 timeout 또는 transient provider error 재시도를 제어한다.
6. interactive shell mode에서는 하위 directory로 깊게 들어간 뒤 `!cd ..`로 workspace 내부 상위 directory를 자유롭게 이동할 수 있다. Kernforge는 workspace 또는 active worktree 경계를 벗어나는 순간만 거부한다.

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
2. selection review도 one-off prompt가 아니라 공통 `ReviewRun` harness와 gate를 통과하게 한다.
3. recent simulation finding이 선택 영역과 맞닿으면 자동으로 review/edit prompt에 주입한다.
4. 더 넓은 리뷰나 수정 전에 workspace/selection diff를 richer Windows diff surface로 확인할 수 있다.

대표 명령:
- `/open <path>`
- `/selection`
- `/selections`
- `/diff`
- `/diff-selection`
- `/review selection [extra]`
- `/review selection --all [extra]`
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

review artifact:
1. `/review selection`은 `.kernforge/reviews/latest.json`과 `.kernforge/reviews/latest.md`를 쓴다.
2. 결과에는 typed finding, freshness/redaction 상태, gate verdict, scope discovery, repair step, runtime gate ledger, 추천 next command가 포함된다.
3. MCP client도 `kernforge_review`를 통해 같은 구조를 받는다. 응답에는 `model_plan`, `reviewer_runs`, `latest_review_freshness`, `edit_proposals`, `runtime_gate_ledger`, `scope_discovery`, `next_commands` action contract가 포함된다.
4. protocol artifact에는 action envelope, approval ledger, capability manifest, external lookup intent, artifact integrity, ledger consistency, resume sanity, state transition, route health도 포함된다. 따라서 CLI 출력, Markdown report, JSON artifact, MCP 응답이 같은 gate 상태를 말한다.

### 2.8 Plan Review Workflow

목적:
1. 구현 계획도 code, selection, PR, goal, final, analysis review와 같은 공통 review harness로 검토한다.
2. active main model은 primary review route가 되고, `/review models cross`만 독립 second-pass route로 둔다. design/security/false-positive/test 관점은 별도 모델 role이 아니라 review lens다.
3. gate와 사용자 흐름이 허용할 때만 실행으로 이어진다.

대표 명령:
- `/review plan <task>`
- `/review models status`
- `/review models`
- `/review models cross <provider> [model]`
- `/review waive <finding-id> --reason <text>`

좋은 상황:
1. 구현이 여러 단계로 얽힌 driver/telemetry 보안 변경
2. 공격자 관점과 운영 현실성을 같이 고려해야 하는 큰 수정
3. 실수 비용이 커서 바로 편집 루프로 들어가기 전에 계획 검토가 필요한 작업

현재 연동:
1. recent simulation finding이 task와 겹치면 review evidence pack에 자동 주입된다.
2. gate는 objective fit, architecture risk, testability, security boundary, maintainability, evidence gap을 structured finding으로 남긴다.
3. multi-model review는 primary plus optional cross route로 제한한다. domain별 추가 모델 누락은 기록하지 않고, planner가 `required_lenses`와 `optional_lenses`를 남긴다.
4. 명시 timeout policy가 없으면 model reviewer 요청은 bounded timeout을 사용한다. 긴 preflight 대기로 전체 턴을 붙잡기보다 빠르게 실패하고 다음 recovery path로 넘어가는 쪽을 우선한다.
5. `@file:line-line 리뷰해줘` 같은 자연어 리뷰 요청은 `/review selection`으로 라우팅하고, focused review-and-fix 요청은 먼저 리뷰를 실행한 뒤 최신 finding을 기준으로 repair 흐름을 이어간다.
6. focused review 요청은 더 작은 evidence/prompt budget을 쓴다. 자동 pre-write review는 diff-first로 동작해서 proposed diff, edit proposal, 필수 repair finding을 넓은 파일 재수집보다 먼저 싣는다. range-focused pre-write evidence는 가능하면 선택 범위부터 감싼 함수 끝까지의 current file context를 보장하고, `function_body_excerpt`를 별도 source로 추가한다.
7. 자동 쓰기 전 리뷰는 문법적으로 유효한 edit preview가 나온 뒤 실제 파일 쓰기 전에 실행하고, 자동 변경 후 리뷰는 changed path가 생긴 뒤 실행한다.
8. service, SCM, driver, 민감 경로 신호는 `security` lens를 추가한다. detection, telemetry, scan, spoofing, evasion-quality surface는 `false_positive` lens를 추가한다.
9. review 진행 출력은 main model과 다른 reviewer가 쓰일 때 route와 provider를 명시하고, 완료 후 gate 결과와 finding 수를 별도로 보여준다. 또한 각 모델 호출 전에 main/cross 단계, context mode, retry budget, soft timeout을 출력하고, 긴 대기 중에는 현재 단계가 메인 1차 리뷰인지 cross 검토인지 설명한다.
10. 단순 exact edit은 `apply_edit_proposal`을 사용할 수 있다. 이 경로는 file, operation, exact search, replacement/content, rationale, risk, preview fingerprint, review evidence를 기록한 뒤 write한다. `apply_patch`는 복잡한 hunk-level fallback으로 남긴다.
11. runtime gate freshness는 review, patch transaction, verification, completion audit, final-answer review를 연결한다. stale review coverage나 waiver 없는 blocker는 `/review`, verification, 표시된 `next_command`가 장부를 회복할 때까지 final answer, 명시적 git write, MCP write-side response, completion audit readiness를 막거나 경고한다.
12. invalid patch recovery는 흔한 wrapper 문제를 정규화하고 반복 patch signature를 기록해, 같은 malformed patch를 재제출하는 대신 target-file context를 다시 읽게 한다.
13. provider behavior는 review token cap, omission retry budget, schema strictness, recovery prompt를 결정한다. weak 또는 불완전한 high-severity model finding은 구체적인 path 또는 symbol, evidence, impact, required fix를 모두 갖추지 못하면 evidence-gap warning으로 낮춘다. finding 위치는 `line: n` 또는 `path: file:line` 형태의 구조화된 line anchor로 보존되므로, 후속 repair handoff가 prose 안의 줄 번호를 다시 긁어야 하는 상황을 줄인다. `test_gap`은 순수 테스트/검증 보강에만 사용한다. reviewer가 production code 변경 required_fix를 `test_gap`으로 잘못 라벨링해도 repair plan은 해당 항목을 실행 가능한 수리 의무로 유지한다. DeepSeek review omission retry는 여러 분짜리 strict-review 반복을 막기 위해 의도적으로 작게 제한한다. 선택적 cross review에서는 메인 1차 리뷰가 이미 usable/actionable finding을 제공했고 reviewer stop reason이 명시적 token-limit/truncation이 아니면, 생략 의심만으로 DeepSeek strict retry를 반복하지 않는다. focused/pre-write cross-reviewer 호출은 기본 3분 soft timeout을 쓰지만, 설정된 리뷰 모델이 active main model보다 낮은 성능으로 판정되면 route timeout으로 보기 전에 자동으로 5분까지 늘린다.
14. 수정 전 리뷰 finding은 edit tool 실행 전에 사용자에게 먼저 노출된다. repair turn에 구조화된 RF 항목이 있으면 Kernforge가 implementation model 시작 전에 해당 ID와 조치 방향을 담은 deterministic `검토 결과:` 요약을 먼저 출력하고 세션에 저장한다. 이후에도 visible summary가 없으면 edit tool 실행 전 가드가 다시 막는다.
15. 로컬 코드 리뷰/수리 턴은 로컬 소스 근거에 머문다. 사용자가 외부 리서치를 명시적으로 요청하지 않는 한 web/search/browser MCP tool은 tool 목록에서 숨겨지고 실행 전에도 차단된다. 모델이 그래도 웹 리서치를 시도하면 어떤 query 또는 URL을 확인하려 했는지 progress에 남기고, 로컬 코드 근거로 돌아오게 한다. 활성 작업 자체가 최신/현재 리서치 요청이면 continuation 턴은 웹 결과가 생길 때까지 그 리서치 의도를 보존하지만, 새 로컬 코드/깃/검증 요청은 우선순위를 다시 로컬 근거로 돌린다.
16. 명시적 수정 흐름에서는 complete high-severity finding과 actionable medium correctness, stability, performance finding이 security finding이 아니어도 repair gate를 막는다. low-severity style, formatting, maintainability finding은 reviewer가 명시적으로 blocker라고 표시하지 않는 한 수정 전 리뷰에서는 warning으로 유지한다.
17. pre-write review는 build/test verification gap을 edit preview 차단 사유가 아니라 edit 이후 검증 의무로 취급한다. "검증이 생략됨"만 말하는 warning은 계속 보이지만 같은 patch를 다시 쓰게 만들지는 않는다. 다만 Allman brace, indentation 같은 patch-local style 문제는 실제 쓰기 전에 고칠 수 있으므로 pre-write에서 차단한다.
18. Claude Code CLI 기본 선택지는 현재 Claude family version을 표시하지만, 실제 CLI 실행에는 `sonnet`, `opus`, `haiku` 같은 안전한 alias를 넘긴다. `/review`, 자연어 리뷰, 수정 전 repair check는 main-first로 동작한다. active main model이 로컬 evidence로 첫 구조화 리뷰를 만들고, optional cross route가 설정돼 있으면 같은 evidence와 primary draft를 받아 second-pass로 다시 본다. domain 전문성은 `security`, `design`, `false_positive`, `regression`, `test`, `final_gate` lens로 prompt에 주입된다. cross reviewer가 실패하거나 빈 응답을 반환하거나 `weak` 품질로 끝나도 run은 degraded 상태로 표시될 뿐, main review finding 보고나 repair loop 시작 자체를 막지는 않는다.
19. cross review route는 기본적으로 최소 `effort=high`를 사용하고, 저장된 `low`/`medium` 값도 reviewer 요청을 만들 때 `high`로 올린다. focused pre-fix bug-hunt review도 이 최소값을 유지한다. pre-write review는 여전히 hard edit gate다. 실제 edit preview가 생긴 뒤에는 필수 main/cross reviewer가 실패하거나 빈 응답을 반환하거나 `weak` 품질이면 `insufficient_evidence`로 write를 막고, Kernforge는 파일을 건드리기 전에 reviewer route 문제를 보고한다. 이때도 implementation model에게 재시도나 웹 근거 수집을 시키지 않는다. 단, 메인 모델의 pre-write 1차 리뷰가 usable이면 중단 응답에 `메인 모델 리뷰 기준으로 진행` 선택지를 함께 보여준다. 사용자가 그 문구로 명시 승인하고 interactive diff preview가 가능한 경우에만 다음 pre-write review에서 cross reviewer 실패를 degraded evidence로 기록하되 hard blocker로는 보지 않고, 그래도 실제 쓰기 전에는 기존 diff preview 확인을 반드시 거친다.
20. pre-write review가 diff preview로 진행할 수 있다고 판단하면, diff preview 질문 전에 최종 검토 결과 본문을 사용자에게 먼저 출력한다. 이 본문은 판정, blocker/warning 수, 수정 확인 대상, 남은 검토 항목, evidence, impact, required fix, test recommendation을 포함하며 긴 필드도 `...`로 잘라 조치 기준을 숨기지 않는다.
21. build/test verification gap은 edit preview를 막는 semantic blocker가 아니라 post-edit obligation으로 처리한다. verification report는 현재 patch transaction 이후에 생성됐고 changed path를 덮을 때만 current로 보며, 오래된 session verification이나 persisted verification history는 review blocker가 아니라 runtime gate warning과 `/verify --full` next action으로 내려간다. artifact를 만들 수 있는 build/test shell 명령은 diff preview와 같은 pinned confirmation 형식인 `자동 검증을 실행할까요? [y/N/a=자동 실행]`으로 묻고, `-prompt -y` 같은 비대화형 bypass 실행에서는 diff preview처럼 자동 승인한다. progress line도 승인 전에는 `검증 승인 확인 중`으로 표시해서 실제 실행/거절/증거 상태를 분리한다. 이 lifecycle은 Codex의 command approval처럼 승인 요청, 사용자 결정, 실제 실행 결과와 evidence 기록을 분리한다. background verification은 시작 시 `pending` evidence gap이며, 완료 결과가 확인될 때만 pass/fail evidence가 된다. 검증이 거절되거나 스킵되면 이후 같은 턴의 shell 검증 재시도와 `latest` background poll은 새 shell/progress status를 내기 전에 `NOT_EXECUTED`로 막는다. 최종 답변이 skipped verification을 고지하는 것은 disclosure 의무를 충족할 뿐 성공 evidence를 만들지 않으므로, 변경 path에 성공 검증 report가 없으면 edit-loop status는 `risk_accepted`로 남긴다. background job bundle metadata는 job-list evidence를 `job_entries`에 저장하고 scalar `job_status`는 단일 job 상태에만 사용한다. 편집 후 자동 검증이 실패하면 Kernforge는 모델에게 수정을 더 시키기 전에 실패 증거가 현재 patch scope에 속하는지 판정한다. 명령/scope 또는 실패 라인이 변경 path를 직접 가리키는 경우만 좁은 repair loop로 이어가고, workspace 또는 sibling-file 실패가 변경 path와 연결되지 않으면 검증 risk로 보고하되 unrelated source/project file 수정으로 확장하지 않는다. out-of-scope 자동 검증 실패 이후 같은 턴에서 build/test/verification 재시도나 probing이 나오면 이것도 `NOT_EXECUTED`로 막아 모델이 대체 빌드 수리로 빠지지 않고 외부/환경성 blocker를 보고하게 한다. 검증 명령이 만드는 build artifact 변경은 허용하지만 source/config file 변경은 edit review gate 밖에서 계속 차단한다. C++/MSBuild adaptive verification은 변경 source를 포함하는 가장 가까운 `.vcxproj`를 먼저 사용하고, 해당 project가 선언한 `Configuration|Platform` 속성을 같이 넘겨 MSBuild가 지원하지 않는 기본 platform으로 빠지지 않게 한다. 솔루션 전체 빌드는 명시적 full verification에 남긴다. configured/detected verification tool path는 shell 경계에서 정규화하며, PowerShell에서는 quoted executable 앞에 `&`를 붙여 detected MSBuild/CMake/CTest/Ninja 경로가 문자열 literal로 파싱되지 않게 한다. verification summary는 논리 명령과 실제 resolved shell command가 다르면 둘 다 보존하고, missing-tool 판정은 빌드 출력의 임의 문구가 아니라 primary executable 기준으로만 한다.
   bypass가 아닌 비대화형 `-prompt` 실행도 검증 계획과 pinned confirmation label을 출력하고 pipe로 들어온 답변을 읽는다. stdin이 없거나 EOF이면 조용한 기본 승인이나 tool failure가 아니라 skipped/declined verification decision으로 기록한다.
   out-of-scope 자동 검증 실패 뒤에는 다음 모델 요청에서 모든 tool definition을 제거해 route를 final-answer-only 모드로 강제한다. scripted/degraded route가 그래도 tool call을 내면 Kernforge는 `NOT_EXECUTED`로 답하고 terminal-state guidance를 다시 전달한다. 이 terminal state에서 나온 최종 답변은 같은 외부 blocker를 이유로 post-change review나 final-answer repair gate에 다시 투입하지 않고, 검증 risk가 드러나도록만 보강한다.
   최종 답변 sanitizer는 code fence 밖의 반복 문장 run을 한 번으로 접고, 한국어 문장이 붙어 출력되는 경우 sentence spacing을 보정한다.
   review, final-answer, completion-audit ledger는 patch transaction이 있으면 그 scope를 우선한다. ambient dirty git file은 git-write gate에서는 계속 보지만, 현재 repair patch의 review를 가짜로 stale하게 만들지는 않는다.
22. 리뷰된 repair가 다시 pre-write gate를 통과하지 못하거나 좁은 확인 예산을 소진하면, Kernforge는 리뷰 미통과를 먼저 알리고 최신 리뷰 결과와 마지막 수정안을 보여준 뒤 `계속 수정할까요? [y/N]`으로 묻는다. 이 confirmation은 자연어 prompt가 아니라 session state다. `y`만 저장된 review/proposal 기준으로 이어가고, `n`은 멈춘다.
   edit target mismatch나 pre-write 차단 뒤의 넓은 recovery `apply_patch`는 파일을 수정하지 않고 연기된다. 차단된 pre-write proposal은 현재 workspace 상태가 아니므로, 다음 edit tool은 `read_file`, `grep`, `git_diff` 중 하나로 현재 파일 또는 diff를 다시 확인한 뒤에만 실행된다. 다음 edit은 현재 파일을 기준으로 한 좁은 standalone patch여야 하며, 넓은 recovery patch가 반복되면 최신 리뷰와 마지막 수정안을 보여주고 멈춘다.
23. 외부 verification callback도 built-in verification과 같은 runtime evidence 계약으로 정규화한다. 성공한 callback report가 `GeneratedAt`, `Trigger`, `Workspace`, `ChangedPaths`를 비워 반환하면 Kernforge가 현재 automatic verification request 기준으로 값을 채운 뒤 review evidence와 runtime-gate freshness를 계산한다.
24. 비대화형 단발 실행(`-prompt`, `-command`, `-goal`, `-goal-file`)은 interactive cancel watcher를 설치하지 않는다. Codex식 명시 event/decision 경계처럼, 사용자가 확인할 수 없는 ambient keyboard state는 요청 취소로 승격하지 않고 장시간 review/repair/goal loop는 실제 모델, 도구, 승인 상태만으로 진행/중단된다.
25. 일반 작업 턴은 완료 시 턴 소요시간을 출력한다. `/exit`, `/status`, `/config`, `/model` 같은 로컬 메타 명령은 소요시간 footer를 생략한다.
26. 16.10 hardening 계층은 review action envelope, approval ledger, capability manifest, external lookup intent, artifact integrity, ledger consistency, resume sanity, route health를 review run에 기록한다. replay fixture는 reviewer route failure, omission/truncation, patch mismatch loop, local web block, pre-fix repair obligation, final visible summary, MCP response contract를 고정한다. 별도 `REVIEW_HARNESS_UX_OPS_85_DESIGN_kor.md` 문서는 Codex App parity를 향한 다음 UX/운영 안정성 목표를 설명한다.

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
4. `/set-auto-verify on|off`, `/progress-display auto|compact|stream`, `/progress_display auto|compact|stream`, `/permissions`, `/checkpoint-auto`, `/provider status|openai-codex-subscription|openai-codex-cli|openai-api|anthropic-claude-cli|anthropic-api|deepseek|openrouter|opencode|opencode-go|ollama|lmstudio|vllm|llama.cpp`, `/profile list|pin|unpin|rename|delete`, `/review models cross|status|clear`, `/verify --full`, `/investigate start <preset>`, `/simulate <profile>`, `/analyze-project --mode <mode>` 같은 고정 인자
5. `/resume`, `/evidence-show`, `/mem-show`, `/mem-promote`, `/mem-demote`, `/mem-confirm`, `/mem-tentative`, `/investigate show`, `/simulate show`, `/new-feature status|plan|implement|close`에 필요한 저장된 id
6. command/subcommand 후보가 이름만이 아니라 설명까지 같이 보이도록 completion list를 렌더링한다.

토큰 예산 관점에서 달라진 점:
1. cached `analyze-project` summary가 더 적절하면 auto-scout 코드 조각보다 먼저 주입될 수 있다.
2. cached project analysis와 architecture fact pack만으로 충분한 질문은 추가 tool iteration 없이 바로 답할 수 있다.
3. 깊은 프로젝트 구조 답변은 deterministic fact, source anchor, 닫힌 directory set, flow invariant와 대조된다. 모순이 있으면 자신 있게 cached 답변을 내지 않고 tool 사용으로 넘어간다.
4. skill/MCP catalog는 실제로 그 정보를 묻는 요청에서만 크게 포함된다.
5. auto-scout는 후보 수와 문맥 길이를 줄였고, 위치 찾기/정의 찾기/참조 찾기 성격의 질문에 더 집중한다.
6. 기본 `max_tokens`는 `8192`이며, 예전 기본값 `4096`이 config에 남아 있으면 시작 또는 `/reload` 때 자동 마이그레이션된다.
7. 기본 `max_tool_iterations`는 `0`(unlimited)이다. 파일 검색/대형 문서화처럼 tool call이 많이 필요한 턴은 더 이상 기본 16회 제한으로 중단되지 않으며, 필요하면 `/set-max-tool-iterations 24`처럼 양수 제한을 다시 걸 수 있다.
8. project analysis worker/reviewer가 main 모델과 같은 OpenRouter 또는 DeepSeek route를 공유하면 기본 model-route limit은 2다. upstream rate-limit 또는 dynamic-concurrency cascade를 줄이기 위한 값이며, key/provider pool이 충분할 때만 `model_routes.provider_limits.openrouter`나 `model_routes.provider_limits.deepseek`를 더 높인다.
9. `/analyze-project`는 tool/model streaming과 같은 progress ledger를 사용한다. `auto`는 오래 남겨야 할 shard/wave와 model-wait update를 기록하고, `compact`는 footer 중심으로 보여주며, `stream`은 긴 실행 디버깅을 위해 모든 update를 기록한다.

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
7. `/review selection integrity risk paths and verifier interactions`
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
- `/review selection ...`
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
6. `/review selection provider visibility and schema drift`
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
3. `/review selection false positives, stealth coverage, and perf ceilings`
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
3. `/review plan harden driver registration, improve telemetry visibility, and preserve post-incident artifacts`
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

### 4.3 `/review selection`과 `/edit-selection`

기본 사용:

```text
/open driver/guard.cpp
/review selection check risk surfaces and cleanup paths
/edit-selection harden the selected registration path
```

좋은 사용 예:
1. 특정 함수나 registration block만 집중적으로 보고 싶을 때
2. recent simulation finding과 실제 코드 범위를 빨리 연결하고 싶을 때

현재 자동 연동:
1. 선택한 파일 경로와 맞닿는 recent simulation finding이 있으면
2. review/edit prompt에 `Additional simulation risk focus`가 자동 주입된다.

### 4.4 `/review plan`

기본 사용:

```text
/review plan harden driver load validation, improve telemetry provider visibility, and preserve audit artifacts
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

persistent memory는 `/mem-search`를 직접 실행하지 않아도 새 turn이 모델로 전달되기 전에 자동으로 주입됩니다. Kernforge는 같은 workspace의 최근 high-value record를 `Workspace continuity` section으로 먼저 넣고, 현재 prompt에 파일 멘션, ASCII 검색어, 구조화 필터가 있으면 `Query matches`도 추가합니다. continuity memory가 주입되면 재사용한 memory id와 짧은 요약을 `memory` activity line으로 사용자에게도 보여줍니다. 그래서 새 세션에서도 최근 수정 파일, verification 결과, 완료 단계, 실패 시도를 다시 문서 전체를 읽기 전에 먼저 참고할 수 있습니다.

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

### 4.10 `/fuzz-func`

기본 사용:

```text
/fuzz-func ValidateRequest
/fuzz-func ValidateRequest --file src/guard.cpp
/fuzz-func ValidateRequest @src/guard.cpp
/fuzz-func ValidateRequest --source-scan focused
/fuzz-func ValidateRequest --source-scan full
/fuzz-func ValidateRequest --no-source-scan
/fuzz-func --from-candidate sc-0123456789abcdef
/fuzz-func @Driver/HEVD/Windows/DoubleFetch.c
/source-scan run --limit 50
/source-scan show latest
/fuzz-func show latest
/fuzz-func language system
/fuzz-campaign
/fuzz-campaign run
/create-driver-poc AcmePoc
```

현재 planner가 보는 것:
1. 함수 시그니처와 파라미터 타입
2. 실제 함수 본문의 size/null/dispatch/cleanup guard
3. 같은 경로의 probe/copy/alloc/publish sink
4. representative root에서 이어지는 caller/callee chain
5. 시작 파일에서 target source까지 이어지는 file expansion path
6. 저장된 source candidate, focused source-scan 결과, function fuzz plan을 연결한 matcher slug
7. build context, `compile_commands.json`, snapshot/semantic index availability

좋은 사용 예:
1. 드라이버나 anti-cheat 코드에서 input-facing 함수의 branch flip과 sink reachability를 빨리 보고 싶을 때
2. 특정 파일이 의심되지만 어떤 함수가 가장 좋은 root인지 모를 때
3. 리뷰 전에 source-only 기준으로 "어떤 값으로 어떤 비교식을 넘기면 어떤 copy/probe path가 열린다"를 확인하고 싶을 때

결과를 읽는 순서:
1. `결론`에서 가장 우선 확인할 예측 문제와 가장 유용한 분기 차이 요약을 본다.
2. `위험도 점수표`에서 high-score finding과 low-score fallback을 구분한다.
3. `상위 예측 문제`에서 구체 입력 예시, 비교식, 최소 반례, 분기 뒤 대표 흐름을 본다.
4. `소스 기반 공격 표면`에서 실제 probe/copy/dispatch 근거 줄을 확인한다.

운영 메모:
1. 함수명만 넣으면 자동 resolve하고, `--file`이나 `@path`를 주면 ambiguity를 크게 줄일 수 있다.
2. 파일만 지정한 `/fuzz-func @path`는 함수명을 몰라도 시작 파일 기준의 representative root를 고른다.
3. `/fuzz-func`는 기본적으로 focused source-scan context를 붙인다. candidate linkage 없이 순수 function fuzz plan만 원하면 `--no-source-scan` 또는 `--source-scan off`를 쓴다.
4. 여러 source matcher 후보를 먼저 훑고 골라야 하면 `/source-scan run`을 먼저 실행한 뒤 `/fuzz-func --from-candidate <candidate-id>`로 이어간다.
5. 네이티브 자동 실행이 차단돼도 source-only fuzzing 결과는 여전히 유효할 수 있다.
6. campaign 하위 단계를 외우지 말고 `/fuzz-campaign`으로 다음 안전한 단계를 확인한 뒤 `/fuzz-campaign run`으로 적용한다. native run artifact가 있으면 dedup된 finding lifecycle, libFuzzer/llvm-cov/LCOV/JSON coverage report 수집, sanitizer/verifier/crash-dump artifact 수집, coverage gap feedback, evidence 기록까지 이어진다.
7. `compile_commands.json`이 있으면 후속 네이티브 fuzzing 품질이 좋아지지만, source-only planning 자체의 선행조건은 아니다.

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
/review selection integrity risk paths
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
/review selection schema and visibility drift
/verify
/evidence-search category:telemetry outcome:failed
```

### 시나리오 C: 큰 변경 전에 plan-review

```text
/simulate tamper-surface guard.sys
/simulate forensic-blind-spot guard.sys
/review plan harden driver registration and preserve telemetry audit artifacts
/verify
/simulate-dashboard
```

### 시나리오 D: source-level fuzzing으로 input-facing path triage

```text
/fuzz-func @Driver/HEVD/Windows/DoubleFetch.c
/fuzz-func TriggerDoubleFetch --file Driver/HEVD/Windows/DoubleFetch.c
/fuzz-func show latest
/fuzz-campaign
/fuzz-campaign run
/verify
```

해석 포인트:
1. 첫 실행은 파일 단위로 representative root와 high-risk path를 빠르게 잡는다.
2. 두 번째 실행은 함수를 직접 고정해서 비교식, 최소 반례, 분기 차이를 더 정밀하게 본다.
3. `show latest`로 report와 source excerpt를 다시 확인한 뒤 verification이나 실제 수정으로 넘어간다.

### 시나리오 E: tracked feature를 만들고 명시적으로 실행

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
3. `/fuzz-func`
4. `/review selection` 또는 `/edit-selection`
5. `/review plan`
6. `/new-feature`
7. `/verify`
8. `/evidence-dashboard`
9. `/mem-search`
10. push/PR에서 hook policy 적용

이 루프가 현재 Kernforge의 가장 큰 차별점이다.
