# Adversarial Simulation Profiles Spec (Korean)

## 1. 목적

`Adversarial Simulation Profiles`는 Kernforge가 이미 수집한

1. code context
2. verification 결과
3. evidence
4. live investigation finding

을 바탕으로, 공격자 관점에서 "현재 변경이나 현재 상태가 어떤 우회/은닉/탐지 공백에 약한가"를 구조적으로 점검하는 기능이다.

이 기능은 exploit code 생성이 아니라, 다음을 찾는 데 목적이 있다.

1. tamper surface
2. stealth surface
3. trust boundary weakness
4. forensic blind spot
5. repeated unresolved failure path

즉 핵심은 "방어 코드를 더 넣어라"가 아니라
"공격자가 어디를 먼저 흔들 가능성이 큰가"를 reviewable evidence로 만드는 것이다.

## 2. Live Investigation Mode와의 관계

권장 순서:

1. `Live Investigation Mode`로 현재 상태를 관찰
2. 그 결과를 evidence / memory / investigation finding으로 저장
3. `Adversarial Simulation Profiles`가 그 결과를 입력으로 읽어 공격자 관점 가정을 수행

즉 이 기능은 investigation 위에 얹히는 2단계 기능이다.

## 3. 비목표

MVP에서 하지 않을 것:

1. 실 exploit / bypass code 생성
2. 실제 target process patching
3. kernel debugging automation
4. full symbolic execution
5. malware-like 행동 재현

## 4. 사용자 경험

### 4.1 새 명령

1. `/simulate`
   - 최근 simulation status와 available profile 표시
2. `/simulate <profile> [target]`
   - profile 실행
3. `/simulate show <id>`
   - simulation result 상세 표시
4. `/simulate list`
   - 최근 simulation result 나열

### 4.2 MVP profile

1. `tamper-surface`
2. `stealth-surface`
3. `forensic-blind-spot`

예:

```text
/simulate tamper-surface guard.sys
/simulate stealth-surface game.exe
/simulate forensic-blind-spot telemetry-provider
```

## 5. 데이터 모델

새 파일:

1. `simulation_store.go`
2. `simulation_profiles.go`
3. `commands_simulate.go`
4. `simulation_store_test.go`
5. `simulation_profiles_test.go`

### 5.1 Simulation Result

주요 필드:

1. `id`
2. `workspace`
3. `profile`
4. `target`
5. `created_at`
6. `source_evidence_ids`
7. `source_investigation_ids`
8. `findings`
9. `summary`
10. `tags`

### 5.2 Simulation Finding

주요 필드:

1. `kind`
2. `category`
3. `subject`
4. `severity`
5. `signal_class`
6. `risk_score`
7. `message`
8. `recommended_actions`
9. `attributes`

## 6. 입력 모델

Simulation은 새로만 보는 것이 아니라, 이미 있는 데이터에서 입력을 가져온다.

입력 우선순위:

1. 최근 failed evidence
2. 최근 investigation finding
3. 현재 `LastVerification`
4. 사용자가 준 target

MVP에서는 아래만 사용해도 충분하다.

1. `EvidenceStore.Search("outcome:failed", workspace, N)`
2. 최근 active/completed investigation 3개
3. `session.LastVerification`

## 7. MVP profile 정의

### 7.1 tamper-surface

질문:

1. 현재 변경/상태에서 공격자가 가장 먼저 tamper할 부분은 어디인가?
2. integrity / signing / registration / configuration이 약한 지점이 있는가?

입력 신호:

1. driver signing / symbols / artifact missing
2. provider not registered
3. target process missing
4. repeated failed evidence

출력 finding 예:

1. `unsigned-or-unverified-driver-surface`
2. `artifact-replacement-surface`
3. `provider-registration-bypass-surface`
4. `repeated-failure-path-exposes-tamper-surface`

### 7.2 stealth-surface

질문:

1. 공격자가 조용히 실패시키거나 탐지를 우회하기 쉬운 곳은 어디인가?
2. observer visibility가 약한 구간이 있는가?

입력 신호:

1. target not listed
2. verifier inactive
3. provider absent
4. low evidence diversity

출력 finding 예:

1. `visibility-gap-around-target`
2. `observer-not-active`
3. `telemetry-blind-zone`
4. `single-signal-dependency`

### 7.3 forensic-blind-spot

질문:

1. 사고 후 원인 추적이 어려운 구간은 어디인가?
2. evidence는 있는데 사건 재구성이 어려운가?

입력 신호:

1. failed evidence는 있는데 artifact가 적음
2. investigation notes/findings가 부족함
3. repeated override history 존재
4. verification failure는 있는데 investigation 없음

출력 finding 예:

1. `low-artifact-forensics`
2. `missing-live-snapshot`
3. `override-without-strong-audit-context`
4. `failure-without-repro-observation`

## 8. 출력 규칙

Simulation 결과는 단순 텍스트가 아니라 evidence/hook에 다시 쓰이기 쉬운 형태여야 한다.

필수 출력:

1. profile summary
2. top findings
3. finding별 risk / signal / message
4. recommended actions

추천 action 예:

1. `/investigate start driver-load <target>`
2. `/verify`
3. `/evidence-search category:driver outcome:failed`
4. `/override`

## 9. Evidence 연동

새 evidence kind:

1. `simulation_run`
2. `simulation_finding`

예:

1. `simulation_run`
   - category: `driver`
   - subject: `tamper-surface:guard.sys`
2. `simulation_finding`
   - category: `driver`
   - subject: `unsigned-or-unverified-driver-surface`
   - severity / risk / signal 포함

중요:

1. simulation finding도 기존 severity/risk 체계를 그대로 사용
2. 이후 hook이 `simulation_finding`까지 참조할 수 있게 확장 가능

## 10. Persistent Memory 연동

simulation 종료 시 memory summary 추가.

예:

1. 어떤 profile을 돌렸는지
2. 상위 위험 finding이 무엇인지
3. 추천 action이 무엇인지

MVP는 summary + keywords 중심,
2단계에서 structured simulation metadata를 추가한다.

## 11. Hook 연동 방향

MVP에서 직접 차단까지는 하지 않는다.

대신:

1. 최근 `critical` simulation finding이 있으면 push/PR 전에 `warn`
2. simulation finding이 investigation action을 추천하면 verification planner note에 context 추가

즉 초기에는 strong enforcement보다 advisory가 맞다.

## 12. 상태 표시

### `/simulate`

표시:

1. available profiles
2. recent simulation result
3. latest high-risk finding

### `/status`

추가 후보:

1. `simulation_results`
2. `last_simulation`

## 13. MVP 구현 범위

반드시 포함:

1. simulation result store
2. 3개 profile evaluator
3. `/simulate`, `/simulate list`, `/simulate show`
4. evidence 기록
5. memory summary 기록

보류:

1. interactive question flow
2. profile chaining
3. hook deny/ask 직접 연결
4. HTML dashboard

## 14. 구현 전략

MVP는 LLM reasoning이 아니라 deterministic profile evaluator로 시작한다.

이유:

1. evidence와 investigation finding 구조를 이미 갖고 있다
2. 규칙 기반으로도 충분히 높은 신호를 낼 수 있다
3. testability가 좋다

즉 각 profile은:

1. 입력 evidence/investigation 집합을 읽고
2. 몇 가지 조건을 평가하고
3. simulation finding을 만든다

## 15. 추천 구현 순서

1. `simulation_store.go`
2. `simulation_profiles.go`
3. `commands_simulate.go`
4. `main.go` dispatch 연결
5. evidence/memory append 연결
6. `/status` 반영
7. test 추가

## 16. 이후 확장

MVP 이후 확장 방향:

1. `trust-boundary-surface`
2. `privilege-abuse-surface`
3. `false-positive-pressure`
4. `release-gate-simulation`

그리고 장기적으로는:

1. simulation finding을 hook policy 입력으로 사용
2. investigation + simulation + evidence를 incident graph로 통합
