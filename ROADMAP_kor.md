# Kernforge Roadmap For Windows Security And Anti-Cheat

이 문서는 현재 Kernforge 구현 상태를 바탕으로 Claude Code 및 Codex와 비교했을 때, 따라가야 하는 범용 기능과 우리만의 강점으로 키워야 하는 기능을 함께 정리한 제품/구현 로드맵이다.

기준 시점:
- 코드베이스 기준: 2026-04-03
- 외부 비교 기준:
  - Claude Code 공식 문서
  - OpenAI Codex 공식 도움말 및 공개 저장소

## 1. 현재 Kernforge의 실제 강점

현재 코드베이스에서 이미 경쟁력이 있는 축은 다음과 같다.

1. 안전한 편집 루프
- diff preview
- selection-aware preview
- 자동 verification
- 자동 checkpoint
- rollback

2. 세션을 넘는 누적 맥락
- persistent memory
- verification history
- memory dashboard
- verification dashboard

3. Windows 친화 UX
- 별도 viewer 창
- selection-first review/edit 흐름
- Windows 입력/취소 처리
- 짧은 `Esc` 탭 취소와 취소 직후 프롬프트 안정화까지 포함한 콘솔 취소 신뢰성

4. planner/reviewer 분리 구조
- `/do-plan-review`
- reviewer 모델 별도 구성

5. 확장 가능성
- local skill
- MCP tool/resource/prompt

핵심 해석:
- Kernforge는 이미 "코드를 잘 쓰는 에이전트"라기보다 "실수를 줄이며 바꾸는 에이전트" 쪽에서 강점이 있다.
- 이 강점은 Windows security/anti-cheat 워크플로우와 매우 잘 맞는다.

## 2. Claude Code / Codex 대비 현재 비교

### Claude Code가 강한 축

1. hooks
- 세션 시작, 사용자 프롬프트 제출, tool 실행 전후, subagent 종료 같은 이벤트에 정책을 붙일 수 있다.

2. subagents
- 독립 context, 도구, 권한을 가진 전문 agent를 분리해 쓸 수 있다.

3. agent teams / 병렬 위임
- 긴 작업을 병렬화하기 쉽다.

4. 광범위한 외부 연결
- MCP를 통해 다양한 외부 시스템 연결이 자연스럽다.

### Codex가 강한 축

1. 승인/샌드박스/워크트리 흐름
- 승인 정책과 로컬 작업 모델이 강하다.

2. 병렬 agent와 cloud delegation
- 로컬 페어링과 클라우드 위임을 함께 가져간다.

3. automations
- 반복 작업을 agent 워크플로우로 실행할 수 있다.

4. GitHub 연동형 코드 리뷰
- PR 자동 리뷰 흐름으로 연결된다.

### Kernforge가 이미 더 좋은 축

1. checkpoint + rollback가 기본 흐름에 잘 녹아 있음
2. verification history와 adaptive verification이 제품 중심 기능으로 존재함
3. selection-first edit/review UX가 명확함
4. persistent memory에 trust/importance 개념이 이미 있음

### Kernforge가 아직 비어 있는 축

1. hook engine 부재
2. subagent/worker 개념 부재
3. automation/scheduler 부재
4. cloud delegation 부재
5. GitHub/PR review automation 부재

## 3. 제품 방향 추천

권장 방향은 "Claude Code + Codex의 범용 기능을 뒤쫓는 제품"이 아니라 아래 포지션이다.

추천 포지션:
- Windows security engineering agent
- anti-cheat development and investigation copilot
- safe modification plus live telemetry plus evidence memory runtime

즉, 따라가야 하는 범용 기능은 최소한으로 확보하되, 차별화는 아래에 둔다.

1. 정책성 hook
2. 보안/윈도우 전문 subagent
3. evidence graph memory
4. live target telemetry workflow
5. security-aware verification planner
6. incident replay bundle

## 4. 제안 기능 우선순위

### P0. Security Hooks Engine

목표:
- 일반적인 hook 기능을 넘어서 "보안 엔지니어링 안전장치"로 만든다.

왜 중요한가:
- Claude Code의 hooks를 따라가는 동시에, Kernforge만의 제품 정체성을 가장 빠르게 만든다.
- anti-cheat/driver/telemetry 작업은 실수 비용이 크므로, preflight 정책 계층이 가치가 높다.

주요 이벤트:
1. `SessionStart`
2. `UserPromptSubmit`
3. `PreToolUse`
4. `PostToolUse`
5. `PreEdit`
6. `PostEdit`
7. `PreVerification`
8. `PostVerification`
9. `PreGitPush`
10. `PreCreatePR`
11. `SessionEnd`

권장 동작:
1. allow
2. deny
3. warn
4. ask
5. rewrite-context
6. attach-evidence
7. enqueue-verification

Windows/security 전용 hook 예시:
1. unsigned driver 산출물 감지 시 push/PR 경고
2. `.sys`, `.inf`, `.cat` 변경 시 서명/패키징 검증 강제
3. `bcdedit`, `verifier`, `sc stop`, `fltmc unload` 같은 명령 실행 전 추가 승인
4. kernel 관련 변경인데 PDB/symbol 없음 경고
5. memory scanner 변경인데 synthetic regression 검증 누락 시 경고
6. anti-cheat 모듈 변경인데 telemetry diff 수집을 자동 제안

MVP 범위:
1. JSON 기반 hook rule 파일
2. 이벤트 payload 직렬화
3. allow/warn/deny/ask 4종만 먼저 지원
4. tool/shell/edit/git/verify 이벤트 우선 지원

성공 조건:
- 보안 민감 작업의 사전 사고율 감소
- verification coverage 상승
- "이 작업은 그냥 수정하면 안 된다"를 런타임이 알려줌

### P0. Specialist Subagents

목표:
- 범용 병렬 agent가 아니라 도메인 분리형 전문 agent를 제공한다.

기본 제공 subagent 제안:
1. `planner`
2. `reviewer`
3. `kernel-investigator`
4. `driver-build-fixer`
5. `telemetry-analyst`
6. `unreal-integrity-reviewer`
7. `memory-inspection-reviewer`
8. `attack-surface-reviewer`

핵심 설계:
1. 각 subagent는 별도 system prompt를 가진다.
2. 도구 허용 범위를 제한할 수 있다.
3. 모델 선택을 다르게 할 수 있다.
4. memory visibility를 제한할 수 있다.
5. selection/file scope를 전달할 수 있다.

권장 delegation 패턴:
1. main agent가 task decomposition
2. evidence 수집용 subagent 병렬 실행
3. 결과를 main agent가 통합
4. edit는 한 agent 또는 명시된 owner만 수행

차별화 포인트:
- 일반 코드 탐색 agent가 아니라 "증거 클래스별" agent로 나눈다.
- 예: 코드, 로그, dump metadata, ETW, verifier 결과를 각기 다른 subagent가 본다.

### P1. Security-Aware Verification Planner

목표:
- 현재 verification 정책 엔진을 보안/윈도우 특화형으로 확장한다.

현재 기반:
- adaptive verification
- verification policy
- verification history tuning

확장 방향:
1. changed file classification
2. artifact-aware verification
3. risk score 기반 검증 자동 추가
4. failure signature 기반 재검증 우선순위 조정

변경 유형별 예시:
1. driver 변경
- `signtool verify`
- catalog/inf 연계 확인
- symbol 존재 확인
- optional: `verifier` smoke checklist

2. Unreal anti-cheat 변경
- module boundary 체크
- cooked asset/integrity 관련 회귀 테스트
- pattern/schema drift 체크

3. process telemetry 변경
- ETW provider manifest/contract 체크
- log schema compatibility 체크

4. memory scanning 코드 변경
- synthetic evasion corpus
- false positive regression
- performance ceiling 체크

핵심 지표:
- 변경 유형 대비 검증 누락률 감소
- 실패 재현성 증가
- 반복 실패 signature의 자동 회피

### P1. Evidence Graph Memory

목표:
- 현재의 persistent memory를 텍스트 요약 저장소에서 "보안 증거 그래프"로 확장한다.

현재 한계:
- request/reply 중심 요약은 남지만, artifact 간 관계 추적은 약하다.

추가 엔티티:
1. issue
2. artifact
3. binary
4. hash
5. build
6. symbol state
7. telemetry finding
8. repro step
9. mitigation
10. environment

추가 관계:
1. issue -> affected binary
2. build -> produced artifact
3. finding -> observed in telemetry
4. mitigation -> validated by verification
5. crash -> associated symbols

효과:
- "지난번에도 비슷한 실패가 있었나?"를 넘어서
- "이 해시/버전/모듈/텔레메트리 패턴 조합이 언제 관찰됐나?"까지 답할 수 있다.

### P1. Live Windows Target Workflow

목표:
- 정적 코드 편집 중심 툴에서, live target을 다루는 investigation runtime으로 확장한다.

우선 지원할 도구군:
1. `signtool`
2. `dumpbin`
3. `symchk`
4. `sc`
5. `fltmc`
6. `verifier`
7. `bcdedit`
8. `wevtutil`
9. `logman`
10. `wpr`
11. `xperf`
12. `cdb` 또는 `windbg` helper

핵심 워크플로우:
1. target 상태 수집
2. 증거 스냅샷 생성
3. ETW/로그/심볼 상태 묶음 수집
4. 결과를 memory와 verification history에 연결

차별화 포인트:
- Codex/Claude가 "코드 작업 보조"라면
- Kernforge는 "코드 + 실행 환경 + 증거 수집"까지 이어지는 anti-cheat/security investigation agent가 된다.

### P2. Incident Replay Bundle

목표:
- 이슈 분석 결과를 나중에 다시 열어볼 수 있는 재현 가능한 bundle로 남긴다.

권장 bundle 내용:
1. request/final answer
2. 관련 selection
3. changed files diff
4. verification report
5. memory citations
6. shell transcript 요약
7. artifact metadata
8. telemetry snapshot index

사용 예:
1. 버그 재오픈 대응
2. 팀 내 handoff
3. anti-cheat false positive 분석 이관
4. 사후 회고

### P2. Review Profiles For Adversarial Thinking

목표:
- 일반 code review가 아니라 anti-cheat/security 전용 review mode를 제공한다.

권장 프로파일:
1. bypass surface review
2. trust boundary review
3. tamper resistance review
4. forensic blind spot review
5. kernel safety review
6. user/kernel boundary review
7. Unreal cheat surface review

효과:
- 일반적 "버그 찾기"보다 위협 모델 기반 리뷰를 빠르게 반복할 수 있다.

### P2. Automations

목표:
- Codex처럼 반복 작업을 자동화하되, 운영 현실 중심으로 설계한다.

우선 자동화 대상:
1. nightly verification digest
2. recurring telemetry anomaly scan
3. driver signing readiness check
4. weekly memory prune and summarize
5. PR security review autopilot

주의:
- 이 기능은 분명 유용하지만, P0/P1보다 먼저 들어가야 하는 기능은 아니다.

### P1. Desktop UX App Shell

목표:
- 현재의 Windows 중심 CLI를 유지한 채, 실사용 가능한 데스크탑 UX를 가진 앱 셸로 확장한다.

핵심 방향:
1. 기존 Go 코어는 유지한다.
2. UI 셸은 WebView2 기반 데스크탑 프레임워크로 감싼다.
3. 권한이 필요한 Windows 기능은 UI 프로세스와 분리한다.
4. 기존 diff preview, viewer, evidence, verification 흐름을 앱 안의 화면으로 재구성한다.

권장 기술 스택:
1. app shell
- `Wails`

2. frontend
- `React`
- `TypeScript`
- `Vite`

3. state
- `Zustand` 또는 `Redux Toolkit`

4. code and diff surface
- `Monaco Editor`

5. large table and dashboard
- `TanStack Table` 또는 `AG Grid`
- `ECharts`

6. local persistence
- `SQLite`

7. privileged runtime
- `Windows Service` 또는 별도 elevated helper
- `named pipe` 또는 로컬 `gRPC`

추천 이유:
1. 현재 Kernforge는 이미 Go 기반이라 재사용률이 가장 높다.
2. 저장소 안에 이미 WebView2 diff preview와 Windows viewer가 있어 기술 방향이 자연스럽다.
3. Electron보다 가볍고, Tauri보다 현재 Go 코어 통합이 단순하다.

권장 아키텍처:
1. desktop UI
- React 기반 대시보드, 세션, evidence, verification, investigation 화면

2. core worker
- 기존 Kernforge agent, tool registry, analysis, verify, memory, hooks 실행

3. privileged broker
- driver, service, ETW, symbol, memory inspection, protected target 관련 작업 분리

4. optional kernel component
- 정말 필요한 anti-cheat or telemetry 기능만 최소 범위로 유지

이 아키텍처가 중요한 이유:
1. UI 크래시와 고권한 작업을 분리할 수 있다.
2. anti-cheat/security 기능의 권한 경계를 명확히 만들 수 있다.
3. 서비스형 helper를 통해 운영 현실에 맞는 복구, 재시도, 로그 수집이 쉬워진다.

우선 앱에서 먼저 살릴 UX:
1. session explorer
2. project analysis dashboard
3. evidence dashboard
4. verification history
5. selection-aware diff review
6. tracked feature workspace view
7. live target status panel

단계별 구현 제안:
1. Stage A
- 기존 Go 엔진을 UI 친화 API 계층으로 묶는다.
- 장기 실행 작업에 progress/event stream을 붙인다.

2. Stage B
- Wails 셸과 React frontend를 붙인다.
- session, analyze-project, verify, evidence 화면을 먼저 연결한다.

3. Stage C
- 기존 preview/viewer를 앱 내부 diff surface로 통합한다.
- Monaco 기반 읽기/리뷰/selection sync를 붙인다.

4. Stage D
- privileged broker와 desktop UI를 분리한다.
- 관리자 권한이 필요한 Windows 작업은 broker를 통해서만 실행한다.

5. Stage E
- installer, code signing, WebView2 bootstrap, update 전략을 정리한다.

대안 비교:
1. `Wails`
- 가장 추천
- 기존 Go 코어를 거의 그대로 살릴 수 있다.

2. `Tauri + Go sidecar`
- 가능하지만 Rust 셸과 Go sidecar를 함께 운영해야 해서 복잡도가 높다.

3. `Electron`
- UI 생태계는 좋지만 메모리, 패키지 크기, 운영 비용이 크다.
- Kernforge의 Windows low-level tooling 방향과는 우선순위가 맞지 않는다.

## 5. 추천 로드맵

### Phase 1

기간:
- 2~4주

목표:
- 경쟁 제품 대비 비어 있는 핵심 축을 빠르게 메운다.

포함:
1. hook engine MVP
2. subagent framework MVP
3. security review profiles MVP
4. desktop app shell architecture and API boundary 설계

완료 기준:
1. 특정 shell/tool/edit/git 이벤트에 hook rule 적용 가능
2. planner/reviewer 외 2개 이상의 전문 subagent 실행 가능
3. review profile 선택 후 동일 코드에 대해 다른 관점의 리뷰 결과 생성 가능
4. Wails 기반 앱 셸에 필요한 core API surface와 event model이 정리됨

### Phase 2

기간:
- 4~8주

목표:
- Kernforge만의 security workflow를 제품 중심축으로 만든다.

포함:
1. security-aware verification planner
2. evidence graph memory
3. live Windows target helper 도구 일부
4. desktop app shell MVP

권장 desktop app shell MVP 범위:
1. session list and detail
2. analysis result browser
3. evidence and verification dashboard
4. integrated diff review surface

완료 기준:
1. 변경 유형별 검증 자동 편성
2. evidence entity 저장 및 검색
3. symbol/signing/ETW 상태를 수집하고 이슈와 연결 가능
4. 주요 investigation and verification workflow를 앱 UI에서 수행 가능

### Phase 3

기간:
- 8주+

목표:
- 운영 팀 협업과 장기 축적 가치를 강화한다.

포함:
1. incident replay bundle
2. automations
3. GitHub/PR security review automation
4. optional cloud delegation
5. privileged broker integration
6. installer and signing pipeline

## 6. 기능 매트릭스

점수 기준:
- 1 = 거의 없음
- 2 = 제한적
- 3 = 보통
- 4 = 강함
- 5 = 매우 강함

| 기능 축 | Kernforge 현재 | Claude Code | Codex | 권장 방향 |
| --- | --- | --- | --- | --- |
| 편집 안전성 | 5 | 3 | 4 | 유지 및 고도화 |
| checkpoint/rollback | 5 | 2 | 3 | 확실한 차별화 유지 |
| verification orchestration | 4 | 3 | 3 | security-aware로 확장 |
| verification history/dashboard | 4 | 2 | 2 | 차별화 유지 |
| persistent memory | 4 | 3 | 4 | evidence graph로 상향 |
| selection-first workflow | 5 | 2 | 3 | 강점 유지 |
| hooks/policy runtime | 1 | 5 | 3 | P0로 보강 |
| subagents | 1 | 5 | 4 | P0로 보강 |
| automations | 1 | 2 | 4 | P2 |
| GitHub review automation | 1 | 2 | 4 | P2 |
| Windows security tooling | 2 | 1 | 2 | 핵심 차별화로 집중 |
| anti-cheat specialization | 1 | 1 | 1 | 가장 큰 기회 |
| desktop UX shell | 2 | 2 | 4 | Go core 유지형 Wails app으로 확장 |

## 7. 현재 코드 구조 기준 구현 진입점

### Hook Engine

주요 파일:
- `tools.go`
- `main.go`
- `verify.go`
- `provider.go`

권장 추가 파일:
- `hooks.go`
- `hooks_policy.go`
- `hooks_runtime.go`
- `hooks_test.go`

주요 연결 지점:
1. tool 실행 전후
- `ToolRegistry.Execute`

2. 편집 전후
- `Workspace.BeforeEdit`
- `Workspace.ConfirmEdit`

3. verification 전후
- verification 실행 entry point

4. git push / create pr 전
- git tool 구현부

5. 세션 시작/종료
- `main.go` runtime 초기화/종료 지점

### Subagent Framework

주요 파일:
- `agent.go`
- `main.go`
- `config.go`
- `provider.go`

권장 추가 파일:
- `subagent.go`
- `subagent_registry.go`
- `subagent_profiles.go`
- `subagent_test.go`

필수 요소:
1. subagent 정의 구조체
2. 모델/provider override
3. tool allowlist
4. memory scope
5. file/selection scope
6. delegation result schema

### Security-Aware Verification

주요 파일:
- `verify.go`
- `verify_policy.go`
- `verification_history.go`

권장 추가 파일:
- `verify_classifier.go`
- `verify_security_rules.go`
- `verify_artifacts_windows.go`

필수 요소:
1. changed file classifier
2. artifact classifier
3. verification recommendation scorer
4. failure signature clustering

### Evidence Graph Memory

주요 파일:
- `persistent_memory.go`
- `memory_policy.go`

권장 추가 파일:
- `evidence.go`
- `evidence_store.go`
- `evidence_query.go`
- `evidence_test.go`

권장 전략:
1. 기존 persistent memory는 유지
2. 별도 evidence store를 추가
3. 점진적으로 두 저장소를 연결

### Live Windows Target Workflow

주요 파일:
- `tools.go`
- `input_windows.go`
- `viewer_windows.go`
- `preview_windows.go`

권장 추가 파일:
- `tools_windows_security.go`
- `windows_symbols.go`
- `windows_etw.go`
- `windows_driver_ops.go`

주의:
- 이 축은 권한/위험도가 높으므로 hook engine 이후에 붙이는 편이 안전하다.

### Desktop UX App Shell

주요 파일:
- `main.go`
- `ui.go`
- `viewer_windows.go`
- `preview_webview_windows.go`
- `preview_html.go`
- `diff_html.go`

권장 추가 디렉토리:
- `app/desktop`
- `app/frontend`
- `app/frontend/src`
- `app/frontend/src/components`
- `app/frontend/src/pages`
- `app/frontend/src/state`
- `app/frontend/src/lib`

권장 추가 파일:
- `app_desktop.go`
- `app_events.go`
- `app_sessions.go`
- `app_analysis.go`
- `app_evidence.go`
- `app_verify.go`
- `app_privileged_client_windows.go`

필수 요소:
1. UI에서 직접 호출 가능한 안정된 core API
2. long-running task progress stream
3. selection and diff state bridge
4. evidence and verification query API
5. privileged broker 연결 abstraction

주의:
1. UI에서 Windows privileged 기능을 직접 호출하지 않는다.
2. 기존 CLI 워크플로우는 유지하고, desktop shell은 병행 제공하는 편이 안전하다.
3. 초기 단계에서는 CLI를 orchestration source of truth로 두는 것이 현실적이다.

## 8. 구현 순서 제안

### 추천 1순위

1. hook engine
2. subagent framework
3. review profiles

이유:
- Claude/Codex와의 기능 격차를 빠르게 줄인다.
- 이후 보안 특화 기능을 넣을 기반이 된다.

### 추천 2순위

1. security-aware verification
2. evidence graph memory
3. desktop app shell MVP

이유:
- Kernforge의 기존 강점을 더 크게 만든다.
- anti-cheat/security 팀이 반복적으로 얻는 가치가 커진다.
- 이미 있는 viewer/preview 자산을 더 일관된 UX로 묶을 수 있다.

### 추천 3순위

1. live Windows target workflow
2. incident replay bundle
3. automations
4. privileged broker hardening and installer/signing

이유:
- 강력하지만 운영/권한/UX 복잡도가 올라간다.
- 기반 기능이 먼저 안정돼야 한다.

## 9. 내가 추천하는 최종 제품 메시지

추천 메시지:

"Kernforge is the coding and investigation agent for Windows security and anti-cheat engineering."

더 실무형으로 풀면:

"Kernforge helps security engineers safely modify code, validate changes, collect evidence, and replay investigation history across Windows and anti-cheat workflows."

핵심 차별점 한 줄:
- edit safely
- verify deeply
- remember evidence
- investigate live targets

## 10. 바로 실행 가능한 다음 작업

가장 추천하는 다음 액션은 아래 3개다.

1. Hook Engine spec 초안 작성
- 이벤트 모델
- rule schema
- allow/deny/warn/ask semantics
- `tools.go`와 `main.go` integration point 정의

2. Subagent architecture spec 작성
- profile format
- isolation boundary
- tool allowlist
- result merge 규칙

3. Security verification classifier 설계
- file/artifact category
- category별 verification step 매핑
- verification history feedback loop

4. Desktop app shell architecture spec 작성
- Wails app boundary
- frontend page map
- Go core API surface
- privileged broker split point

이 셋이 정리되면, 실제 구현 우선순위와 파일 단위 작업 분할까지 바로 들어갈 수 있다.

## Sources

- Claude Code overview: https://docs.anthropic.com/en/docs/agents-and-tools/claude-code/overview
- Claude Code subagents: https://code.claude.com/docs/en/sub-agents
- Claude Code hooks: https://docs.anthropic.com/en/docs/claude-code/hooks
- OpenAI Codex help overview: https://help.openai.com/en/articles/11369540
- OpenAI Codex repository: https://github.com/openai/codex
