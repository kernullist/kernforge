# Kernforge Memory-Scan 플레이북

이 문서는 pattern scan, signature scan, memory inspection, evasion 대응, false positive/false negative 조정 작업에 Kernforge를 어떻게 적용하면 좋은지 정리한 운영 플레이북이다.

## 1. 언제 이 플레이북을 쓰면 좋은가

1. scanner 로직이나 signature matching 로직이 바뀐다.
2. false positive, false negative, 성능 상한, evasion 대응이 중요하다.
3. stealth 관점에서 observer coverage가 중요하다.
4. 최근 scanner 관련 failed evidence가 이미 쌓여 있다.

## 2. 권장 기본 흐름

```text
/analyze-project scanner stealth, false-positive, and memory hot path architecture
/analyze-performance scanner
/simulate stealth-surface scanner-core
/open scanner/patternscan.cpp
/review-selection false positives, stealth coverage, and performance ceilings
/edit-selection reduce false positives without weakening evasion coverage
/verify
/evidence-dashboard category:memory-scan
/mem-search category:memory-scan risk:>=70
```

## 3. 이 흐름이 좋은 이유

1. scanner 작업은 단순 correctness보다 탐지 coverage와 evasions가 더 중요하다.
2. project analysis가 scanning path, hot path, 위험 의존성을 먼저 구조화해 준다.
3. `stealth-surface`는 탐지 관찰 범위가 비는 지점을 먼저 드러낸다.
4. selection review/edit는 실제 scanning path만 좁혀서 점검할 수 있게 해준다.
5. `/verify`는 memory-scan category 기반 보안 review step과 recent simulation risk context를 같이 넣는다.
6. `/evidence-dashboard`는 최근 high-risk scanner 상태를 빠르게 보여준다.

## 4. forensic 관점도 중요한 경우

메모리 스캔 결과가 incident 분석에도 중요하다면:

```text
/simulate forensic-blind-spot scanner-core
/verify
/simulate-dashboard
```

이 흐름이 좋은 이유:
1. 탐지가 되더라도 사후 분석 artifact가 약하면 운영 가치가 떨어질 수 있다.
2. forensic blind spot simulation은 그 약점을 따로 드러내 준다.

## 5. 특히 자주 보는 신호

1. `signal:stealth`
2. `signal:forensics`
3. `severity:high`
4. `risk:>=70`

유용한 예:

```text
/evidence-search category:memory-scan signal:stealth
/evidence-search kind:simulation_finding signal:forensics
/mem-search category:memory-scan severity:high
```

## 6. PR 전 체크 추천

1. `/verify`
2. `/evidence-dashboard category:memory-scan`
3. `/mem-search category:memory-scan risk:>=70`
4. `/override`

## 7. 좋은 운영 습관

1. scanner 수정 전후로 stealth 관점을 먼저 본다.
2. false positive를 줄일 때 evasion coverage 약화가 없는지 반드시 같이 본다.
3. 성능 문제와 탐지 공백을 같은 문제로 본다.
4. 반복 실패는 override보다 detection gap 자체를 줄이는 방향으로 해결한다.
