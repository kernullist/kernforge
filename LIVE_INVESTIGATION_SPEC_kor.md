# Live Investigation Mode Spec (Korean)

## 1. 목적

`Live Investigation Mode`는 Kernforge를 단순한 "코드를 수정하는 CLI"에서
"실행 중인 Windows 대상의 상태를 관찰하고, 증거를 수집하고, 이후 verification / evidence / memory / hooks 루프로 다시 연결하는 도구"로 확장하는 기능이다.

이 기능의 핵심 가치는 아래와 같다.

1. 코드만이 아니라 실행 중 상태를 기준으로 문제를 본다.
2. ETW, WPR, process, service, driver, module, symbol 상태를 하나의 investigation session으로 묶는다.
3. 수집 결과를 evidence와 persistent memory에 구조화해 누적한다.
4. 이후 verification, review, push/PR policy에 다시 반영할 수 있다.

이 기능은 특히 다음 상황에 맞는다.

1. driver load/unload 문제
2. anti-cheat initialization failure
3. telemetry provider drift
4. memory-scan false positive / false negative
5. Unreal integrity mismatch
6. crash / watchdog / startup regression triage

## 2. 비목표

MVP에서 하지 않을 것:

1. GUI viewer 신규 구현
2. full WinDbg integration
3. 원격 호스트 agenting
4. 장시간 background daemon
5. 자동 exploit simulation

## 3. 사용자 경험

### 3.1 새 명령

1. `/investigate`
   현재 investigation 상태, 최근 session, preset 요약 출력
2. `/investigate start <preset> [target]`
   새 investigation session 시작
3. `/investigate snapshot [target]`
   현재 session 또는 ad-hoc snapshot 수집
4. `/investigate note <text>`
   현재 investigation에 수동 메모 추가
5. `/investigate stop [summary]`
   session 종료, evidence/memory 기록
6. `/investigate show <id>`
   investigation 상세 표시
7. `/investigate list`
   최근 investigation session 나열

### 3.2 preset 예시

MVP preset:

1. `driver-load`
2. `process-attach`
3. `telemetry-provider`
4. `memory-scan`
5. `unreal-integrity`

예:

```text
/investigate start driver-load mydriver.sys
/investigate snapshot
/investigate note verifier query shows unexpected settings
/investigate stop driver load blocked on target machine
```

## 4. 핵심 개념

### 4.1 Investigation Session

하나의 live triage 작업 단위.

주요 필드:

1. `id`
2. `workspace`
3. `preset`
4. `target`
5. `status`
6. `created_at`
7. `updated_at`
8. `started_by_session_id`
9. `notes`
10. `snapshots`
11. `summary`
12. `tags`

### 4.2 Snapshot

특정 시점의 수집 결과.

주요 필드:

1. `id`
2. `investigation_id`
3. `kind`
4. `created_at`
5. `target`
6. `commands`
7. `artifacts`
8. `findings`
9. `raw_summary`

### 4.3 Finding

Snapshot 안에서 핵심 신호로 추출된 항목.

주요 필드:

1. `kind`
2. `category`
3. `subject`
4. `outcome`
5. `severity`
6. `signal_class`
7. `risk_score`
8. `message`
9. `attributes`

## 5. 저장 구조

새 파일:

1. `investigation_store.go`
2. `investigation_collectors.go`
3. `commands_investigate.go`
4. `investigation_store_test.go`
5. `investigation_collectors_test.go`

저장 파일:

1. `%CODEX_HOME%/kernforge/investigations.json`
   또는 기존 user config dir 하위 `investigations.json`

레코드 수 제한:

1. 기본 300 session
2. session당 snapshot 기본 20개

저장 방식:

1. 기존 evidence/memory와 동일하게 path lock + atomic write 사용

## 6. 수집 모델

MVP는 "실행 가능한 도구가 있으면 실제 command 실행, 없으면 graceful degrade" 전략을 따른다.

### 6.1 driver-load preset

우선 수집:

1. `sc query type= driver`
2. `driverquery /v`
3. `verifier /querysettings`
4. `fltmc`
5. target `.sys/.inf/.cat` 존재 확인

추출 finding 예:

1. target driver artifact missing
2. verifier active
3. verifier inactive
4. target driver not listed
5. filter stack present

### 6.2 process-attach preset

우선 수집:

1. `tasklist /v`
2. `sc query`
3. `wmic process` 또는 대체 PowerShell process query

추출 finding 예:

1. target process missing
2. target process duplicated
3. process protection mismatch

### 6.3 telemetry-provider preset

우선 수집:

1. `logman query providers`
2. `wevtutil el`
3. manifest/provider 관련 파일 존재 확인

추출 finding 예:

1. provider not registered
2. manifest present but provider absent
3. likely xml/provider drift

### 6.4 memory-scan preset

우선 수집:

1. target process 존재 여부
2. scanner config / signature artifact 존재 여부
3. 최근 failed evidence 재참조

추출 finding 예:

1. target process absent
2. scanner artifact missing
3. repeated failure subject still active

### 6.5 unreal-integrity preset

우선 수집:

1. target module / pak / manifest artifact 존재 여부
2. related failed evidence 재참조

추출 finding 예:

1. integrity artifact missing
2. repeated unreal mismatch evidence

## 7. Evidence 연동

Investigation은 자체 저장만 하지 않고 evidence로도 투영한다.

새 evidence kind:

1. `investigation_session`
2. `investigation_snapshot`
3. `investigation_finding`

예:

1. `investigation_session`
   - category: `driver`
   - subject: `driver-load:mydriver.sys`
   - outcome: `active|completed`
2. `investigation_finding`
   - category: `driver`
   - subject: `target driver not listed`
   - severity/risk/signal 반영

중요:

1. investigation finding은 기존 evidence scoring 체계를 재사용
2. severity/risk가 높으면 이후 hook policy가 다시 참조 가능

## 8. Persistent Memory 연동

session 종료 시 persistent memory에 session summary 추가.

요약 예:

1. 어떤 preset을 대상으로 했는지
2. 주요 findings가 무엇인지
3. target 상태가 어땠는지
4. 어떤 artifact가 관찰됐는지

새 verification-like metadata 후보:

1. `investigation_categories`
2. `investigation_targets`
3. `investigation_findings`
4. `investigation_max_risk`

MVP에서는 우선 `summary + keywords` 중심으로 넣고,
2단계에서 structured field를 추가한다.

## 9. Hook / Policy 연동

직접적인 첫 연동은 아래 정도로 제한한다.

1. investigation 중 `critical` finding 발생 시 warning context 추가
2. 최근 investigation finding이 unresolved 상태면 push/PR 전에 `warn` 또는 `ask`

예:

1. 최근 `driver-load` investigation에서 `target driver not listed` + `critical`
2. 그 상태에서 PR 생성 시
3. hook이 "recent unresolved investigation finding exists" 경고 출력

## 10. 상태 표시

### `/status` 확장

추가 후보:

1. `active_investigation`
2. `investigation_sessions`
3. `last_investigation_update`

### `/investigate` 기본 화면

표시:

1. active session 여부
2. preset
3. target
4. recent findings
5. snapshot 개수

## 11. MVP 범위

MVP에서 반드시 들어갈 것:

1. investigation session store
2. command UX
3. `driver-load`, `telemetry-provider`, `process-attach` preset
4. snapshot 수집
5. finding 추출
6. evidence 기록
7. persistent memory summary 기록
8. `/status` 반영

MVP에서 보류:

1. `wpr` 녹화
2. Procmon preset control
3. binary hash / PDB 깊은 분석
4. incident graph UI

## 12. 추천 구현 순서

1. `investigation_store.go`
2. `investigation_collectors.go`
3. `commands_investigate.go`
4. `main.go` command dispatch 연결
5. evidence append 연결
6. `/status` 표시
7. test 추가

## 13. Adversarial Simulation Profiles와의 연결

`Adversarial Simulation Profiles`는 Live Investigation 위에 얹는 2단계 기능으로 본다.

권장 순서:

1. 먼저 실제 대상 상태를 관찰하는 `Live Investigation Mode`
2. 그 다음, 수집된 evidence와 investigation finding을 바탕으로 공격자 관점의 시뮬레이션을 수행하는 `Adversarial Simulation`

즉 순서는:

1. observe
2. collect
3. score
4. simulate

이 순서가 Kernforge의 현재 구조와 가장 잘 맞다.
