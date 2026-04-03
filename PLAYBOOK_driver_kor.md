# Kernforge Driver 플레이북

이 문서는 driver, signing, symbols, package, verifier readiness 작업에 Kernforge를 어떻게 적용하면 좋은지 정리한 운영 플레이북이다.

## 1. 언제 이 플레이북을 쓰면 좋은가

1. `.sys`, `.inf`, `.cat` 산출물이 관여한다.
2. signing/symbol/package/verifier readiness가 중요하다.
3. integrity, registration, load path hardening이 중요하다.
4. 최근 driver 관련 failed evidence가 쌓여 있다.

## 2. 권장 기본 흐름

```text
/investigate start driver-load guard.sys
/investigate snapshot
/simulate tamper-surface guard.sys
/open driver/guard.cpp
/review-selection integrity bypass paths and verifier interactions
/edit-selection harden registration and signing assumptions
/verify
/evidence-dashboard category:driver
/mem-search category:driver signal:signing
```

## 3. 각 단계의 의미

1. `/investigate start driver-load guard.sys`
driver load 상태, verifier 상태, filter stack을 현재 시점 기준으로 잡아 둔다.

2. `/simulate tamper-surface guard.sys`
integrity/signing/bypass surface를 먼저 드러낸다.

3. `/review-selection ...`
simulation finding이 선택 범위와 맞닿으면 adversarial context가 자동 주입된다.

4. `/verify`
driver category 기반 verification과 recent simulation/investigation follow-up step이 같이 들어간다.

5. `/evidence-dashboard category:driver`
최근 signing/symbol/package/verifier 관련 failed evidence를 한눈에 본다.

## 4. 특히 자주 보는 신호

1. `signal:signing`
2. `signal:symbols`
3. `severity:critical`
4. `risk:>=80`

유용한 예:

```text
/evidence-search category:driver signal:signing
/mem-search category:driver signal:symbols
/evidence-search severity:critical risk:>=80
```

## 5. PR 전 체크 추천

1. `/verify`
2. `/evidence-dashboard category:driver`
3. `/override`
4. push/PR 시 hook policy 확인

## 6. 좋은 운영 습관

1. driver 변경 전 live snapshot을 남긴다.
2. 큰 변경 전에는 `tamper-surface`를 먼저 돌린다.
3. signing/symbol 문제는 evidence와 memory 양쪽에서 확인한다.
4. 반복 실패는 override로 넘기기보다 원인 패턴부터 줄인다.
