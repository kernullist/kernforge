# Kernforge Evidence Severity And Risk Scoring Spec

이 문서는 Kernforge의 evidence store, persistent memory, hook policy 위에 얹을 `severity / risk scoring` 기능의 상세 설계 문서이다.

기준 시점:
- 코드베이스 기준: 2026-04-03

목표:
1. failed evidence를 모두 같은 무게로 다루지 않도록 한다.
2. verification 결과를 `위험도 있는 신호`로 재해석할 수 있게 만든다.
3. hook policy가 `warn / ask / deny / checkpoint`를 더 정교하게 선택할 수 있게 한다.
4. driver, telemetry, memory-scan, Unreal 보안 워크플로우에서 운영 현실성을 높인다.

비목표:
1. 이번 단계에서 ML 기반 scoring을 도입하지 않는다.
2. 이번 단계에서 외부 서버나 cloud risk engine을 붙이지 않는다.
3. 이번 단계에서 GUI 기반 severity editor를 만들지 않는다.

## 1. 왜 필요한가

현재 Kernforge는 다음을 이미 잘한다.

1. verification 결과를 evidence로 저장
2. evidence를 memory와 hook policy에 다시 연결
3. 최근 실패 evidence가 있으면 push/PR 전에 경고, 확인, 차단
4. 반복 실패가 있으면 checkpoint까지 생성 가능

하지만 아직 부족한 점은 다음이다.

1. `driver signing failed`와 `telemetry XML drift`가 모두 그냥 `failed evidence`로 보인다.
2. `provider warning`과 `symbol missing`이 같은 강도로 정책에 걸릴 수 있다.
3. 팀 입장에서는 "지금 막아야 하는 실패"와 "알고는 있어야 하지만 막을 정도는 아닌 실패"를 구분하기 어렵다.

따라서 evidence에 `severity`, `confidence`, `signal_class`, `risk_score`를 붙여야 한다.

## 2. 설계 원칙

1. 규칙 기반 시작
- 초기 버전은 deterministic rule 기반으로 간다.

2. evidence 중심
- severity는 verification report 전체보다 evidence record 단위에 붙는다.

3. 설명 가능성
- 왜 `critical` 또는 `high`가 되었는지 추론 근거를 남겨야 한다.

4. category-aware
- driver, telemetry, memory-scan, Unreal은 같은 failure kind라도 다르게 해석될 수 있다.

5. hook-friendly
- hook rule에서 severity와 risk score를 바로 조건으로 쓸 수 있어야 한다.

## 3. 새 데이터 모델

`EvidenceRecord`에 아래 필드를 추가한다.

1. `Severity string`
- 예: `low`, `medium`, `high`, `critical`

2. `Confidence string`
- 예: `low`, `medium`, `high`
- 의미:
  - low: 추론 근거가 약함
  - medium: 꽤 유력함
  - high: 직접적인 failure signal

3. `SignalClass string`
- 예:
  - `signing`
  - `symbols`
  - `package`
  - `verifier`
  - `provider`
  - `xml`
  - `schema`
  - `integrity`
  - `evasion`
  - `false_positive`
  - `performance`
  - `runtime`

4. `RiskScore int`
- 범위: `0..100`
- hook policy와 dashboard에서 사용

5. `SeverityReasons []string`
- severity가 결정된 이유
- 예:
  - `driver artifact failed`
  - `signing tag detected`
  - `recent repeated failure pattern`

권장 JSON 예:

```json
{
  "id": "ev-20260403-120001-101",
  "kind": "verification_artifact",
  "category": "driver",
  "subject": "build/guard.sys",
  "outcome": "failed",
  "severity": "critical",
  "confidence": "high",
  "signal_class": "signing",
  "risk_score": 92,
  "severity_reasons": [
    "driver artifact failed",
    "signing tag detected",
    ".sys output involved"
  ]
}
```

## 4. Severity 레벨 정의

### 4.1 `low`

의미:
- 참고는 해야 하지만 흐름을 막을 정도는 아님

예:
1. review-only concern
2. perf observation
3. weak heuristic issue

추천 policy:
1. `warn`

### 4.2 `medium`

의미:
- 운영 리스크가 있을 수 있고, 확인은 필요함

예:
1. telemetry drift 가능성
2. Unreal schema mismatch suspicion
3. memory-scan false positive concern

추천 policy:
1. `warn`
2. `ask`

### 4.3 `high`

의미:
- 실제 배포/검증/동작 실패와 꽤 가까움

예:
1. symbol mismatch
2. provider lookup failure
3. repeated scanner regression

추천 policy:
1. `ask`
2. `create_checkpoint`
3. 특정 흐름에서는 `deny`

### 4.4 `critical`

의미:
- 지금 그대로 push/PR 하면 사고 가능성이 높음

예:
1. driver signing failure
2. missing package artifact
3. repeated recent failed driver output

추천 policy:
1. `create_checkpoint`
2. `deny`

## 5. Risk Score 정의

초기 버전은 아래처럼 합산한다.

`RiskScore = CategoryBase + OutcomeWeight + SignalWeight + RepeatWeight + RecencyWeight`

### 5.1 CategoryBase

1. `driver`: `35`
2. `memory-scan`: `28`
3. `telemetry`: `22`
4. `unreal`: `18`
5. default: `10`

### 5.2 OutcomeWeight

1. `failed`: `30`
2. `passed`: `0`
3. empty/unknown: `5`

### 5.3 SignalWeight

1. `signing`: `25`
2. `symbols`: `20`
3. `package`: `20`
4. `verifier`: `18`
5. `provider`: `16`
6. `xml`: `12`
7. `integrity`: `14`
8. `schema`: `10`
9. `evasion`: `18`
10. `false_positive`: `12`
11. `performance`: `5`
12. `runtime`: `15`

### 5.4 RepeatWeight

최근 같은 workspace + category + subject 또는 signal class에서 반복 실패가 있으면:

1. 2회: `+8`
2. 3회 이상: `+15`

### 5.5 RecencyWeight

1. 최근 24시간: `+10`
2. 최근 72시간: `+5`
3. 그 외: `0`

최종 score는 `100`으로 clamp한다.

## 6. Signal Class 추론 규칙

signal class는 다음 우선순위로 추론한다.

1. explicit tag
2. failure kind
3. artifact suffix
4. category fallback

### 6.1 explicit tag 기반

1. tag에 `signing` 있으면 `signing`
2. tag에 `symbols` 또는 `symbol` 있으면 `symbols`
3. tag에 `provider` 있으면 `provider`
4. tag에 `xml` 있으면 `xml`
5. tag에 `schema` 있으면 `schema`
6. tag에 `integrity` 있으면 `integrity`
7. tag에 `evasion` 있으면 `evasion`
8. tag에 `false_positive` 있으면 `false_positive`
9. tag에 `performance` 또는 `perf` 있으면 `performance`
10. tag에 `runtime` 있으면 `runtime`

### 6.2 artifact suffix 기반

1. `.sys`, `.cat`, `.inf`
- 우선 `signing` 또는 `package` 후보

2. `.man`, `.mc`, `.xml`
- 우선 `provider`, `xml`, `schema` 후보

### 6.3 category fallback

1. driver 기본 fallback: `runtime`
2. telemetry 기본 fallback: `provider`
3. memory-scan 기본 fallback: `evasion`
4. unreal 기본 fallback: `integrity`

## 7. Severity 추론 규칙

초기 버전 규칙은 다음과 같다.

### 7.1 Driver

1. `failed + signing`
- `critical`

2. `failed + symbols`
- `high`

3. `failed + package`
- `high`

4. `failed + verifier`
- `high`

5. `failed + repeated recent artifact`
- `critical`

### 7.2 Telemetry

1. `failed + provider`
- `high`

2. `failed + xml`
- `medium` 또는 `high`

3. `failed + schema`
- `medium`

### 7.3 Memory-Scan

1. `failed + evasion`
- `high`

2. `failed + false_positive`
- `medium`

3. `failed + repeated recent memory-scan`
- `high`

4. `failed + perf only`
- `low` 또는 `medium`

### 7.4 Unreal

1. `failed + integrity`
- `high`

2. `failed + schema`
- `medium`

3. review-only warning
- `low`

## 8. Confidence 추론 규칙

### 8.1 `high`

1. verification step status가 직접 `failed`
2. artifact-specific evidence
3. failure kind가 명시됨

### 8.2 `medium`

1. category evidence
2. review step 기반 추론
3. signal class는 보이지만 artifact/failure 근거가 약함

### 8.3 `low`

1. weak heuristic only
2. review note only

## 9. 구현 포인트

### 9.1 Evidence 생성 시점

현재 `buildEvidenceRecords()`에서 evidence가 만들어진다.

여기에 다음 helper를 추가한다.

1. `deriveEvidenceSignalClass(record, report, storeContext) string`
2. `deriveEvidenceSeverity(record, report, storeContext) (severity string, confidence string, reasons []string)`
3. `deriveEvidenceRiskScore(record, severity, signalClass, storeContext) int`

추천 신규 파일:
- `evidence_scoring.go`
- `evidence_scoring_test.go`

### 9.2 Store context

severity 계산에는 최근 반복 실패 여부가 필요하다.

따라서 `CaptureVerification()` 또는 `Append()` 경로에서 최근 evidence를 읽어 `EvidenceScoringContext`를 만든다.

예:

```go
type EvidenceScoringContext struct
{
    RecentWorkspaceRecords []EvidenceRecord
    RecentCategoryFailures map[string]int
    RecentSubjectFailures map[string]int
    RecentSignalFailures map[string]int
    Now time.Time
}
```

### 9.3 normalize 경로

`normalizeEvidenceRecord()`는 이제 다음도 정리해야 한다.

1. severity default
2. confidence default
3. signal class normalize
4. risk score clamp
5. severity reasons dedupe

## 10. Hook 연동 확장

현재 hook은 category/tag/outcome/age/count를 본다.
다음 단계에서는 severity와 risk score도 직접 볼 수 있어야 한다.

`HookMatch`에 추가할 필드:

1. `EvidenceSeverities []string`
2. `MinEvidenceRiskScore *int`
3. `SignalClasses []string`

runtime payload에 추가할 값:

1. `recent_failed_evidence_severities`
2. `recent_failed_evidence_signal_classes`
3. `recent_failed_evidence_max_risk_score`

이후 policy 예:

1. `driver + severity=critical`
- `create_checkpoint + deny`

2. `telemetry + severity=high + signal_class=provider`
- `ask + append_review_context`

3. `memory-scan + risk_score>=80`
- `checkpoint + deny`

## 11. Search / Dashboard 연동

### 11.1 Evidence search filter

`/evidence-search`에 추가:

1. `severity:<low|medium|high|critical>`
2. `signal:<name>`
3. `risk:>=80`

### 11.2 Persistent memory filter

memory record에도 verification severity metadata를 요약으로 넣을 수 있다.

추가 후보:
1. `verification_severities`
2. `verification_signal_classes`
3. `verification_max_risk_score`

### 11.3 Dashboard

evidence dashboard에 추가:

1. severity distribution
2. signal class distribution
3. top critical subjects
4. highest risk recent evidence

## 12. Backward Compatibility

기존 `evidence.json`에는 severity 필드가 없다.

초기 대응:
1. 필드가 없으면 normalize 시 default 채움
2. 기존 record는 `severity=medium`, `confidence=low`가 아니라 규칙 기반으로 재추론 가능하면 재추론
3. 불가능하면:
  - `verification_failure` -> `medium`
  - `verification_artifact` -> `medium`
  - `verification_category` -> `low`

## 13. 초기 구현 순서

### Phase 1

1. `EvidenceRecord` 필드 확장
2. `evidence_scoring.go`
3. evidence 생성 시 severity/signal/risk 계산
4. search filter와 dashboard 반영

### Phase 2

1. hook payload severity/risk 확장
2. hook rule severity 조건 추가
3. built-in `windows-security` preset 일부를 severity 기반으로 재작성

### Phase 3

1. persistent memory에 severity metadata 요약 반영
2. evidence timeline / incident bundle에서 severity 활용

## 14. MVP 권장 범위

지금 바로 구현할 MVP는 아래까지만 해도 충분히 가치가 크다.

1. `severity`
2. `signal_class`
3. `risk_score`
4. `severity_reasons`
5. evidence search filter
6. evidence dashboard severity/signal 분포
7. hook severity 조건

이렇게만 들어가도:
1. failed evidence의 무게가 달라지고
2. policy 강도가 자연스러워지고
3. dashboard가 훨씬 운영 친화적으로 바뀐다.

## 15. 추천 정책 예시

### Driver release branch

1. `critical`
- `create_checkpoint + deny`

2. `high`
- `ask`

3. `medium`
- `warn`

### Telemetry branch

1. `high + provider`
- `ask + append_review_context`

2. `medium + xml`
- `warn`

### Memory-scan branch

1. `risk_score>=80`
- `create_checkpoint + deny`

2. `risk_score>=60`
- `ask`

## 16. 결론

severity / risk scoring은 지금 Kernforge 구조에서 가장 ROI가 높은 다음 단계다.

이미 있는:
1. verification
2. evidence
3. persistent memory
4. hook policy
5. checkpoint

위에 바로 얹을 수 있고,

결과적으로 다음이 가능해진다.

1. 중요한 실패를 더 강하게 다루기
2. 사소한 실패를 덜 시끄럽게 다루기
3. driver/telemetry/memory-scan/Unreal을 서로 다른 무게로 다루기
4. 실제 운영에 가까운 policy loop 만들기
