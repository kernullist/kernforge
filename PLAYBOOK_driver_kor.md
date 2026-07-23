# Kernforge Driver 플레이북

이 문서는 driver, signing, symbols, package, verifier readiness 작업에 Kernforge를 어떻게 적용하면 좋은지 정리한 운영 플레이북이다.

## 1. 언제 이 플레이북을 쓰면 좋은가

1. `.sys`, `.inf`, `.cat` 산출물이 관여한다.
2. signing/symbol/package/verifier readiness가 중요하다.
3. integrity, registration, load path hardening이 중요하다.
4. 최근 driver 관련 failed evidence가 쌓여 있다.

## 2. 권장 기본 흐름

```text
/analyze-project driver startup, signing, and integrity architecture
/analyze-performance startup
/investigate start driver-visibility guard.sys
/investigate start platform-security
/investigate snapshot
/simulate tamper-surface guard.sys
/source-scan run --files driver
/fuzz-func @driver
/fuzz-campaign run
/open driver/guard.cpp
/review-selection integrity risk paths and verifier interactions
/edit-selection harden registration and signing assumptions
/verify
/evidence-dashboard category:driver
/mem-search category:driver signal:signing
```

새 드라이버 POC부터 시작할 때:

```text
/create-driver-poc GuardPoc --type objectfilter
# handoff: /source-scan, /fuzz-func, /fuzz-campaign run, /verify, platform-security, signing, Driver Verifier
# seed: GuardPoc/.kernforge/security/workflow_seed.json
```

## 3. 각 단계의 의미

1. `/analyze-project ...`
startup, signing, integrity, verification 민감 경로를 재사용 가능한 구조 지식으로 정리한다.

2. `/investigate start driver-visibility guard.sys`
현재 시점의 드라이버 가시성, verifier 상태, 관련 artifact 존재 여부를 빠르게 잡아 둔다.

3. `/investigate start platform-security`
Secure Boot, VBS, HVCI/Memory Integrity, test-signing, driver signature enforcement, TPM readiness를 best-effort posture snapshot으로 남긴다. probe 실패 필드는 `unavailable`이다.

4. `/simulate tamper-surface guard.sys`
integrity/signing/tamper risk surface를 먼저 드러낸다.

5. `/source-scan` / `/fuzz-func` / `/fuzz-campaign run`
IOCTL·callback 등 입력 표면을 source triage한 뒤 campaign seed(필요 시 multi-call sequence)와 native result lifecycle으로 이어간다. harness-only crash는 `spurious`로 격리된다.

6. `/review-selection ...`
simulation finding이 선택 범위와 맞닿으면 risk context가 자동 주입된다.

7. `/verify`
driver category 기반 verification과 recent simulation/investigation follow-up step이 같이 들어간다.

8. `/evidence-dashboard category:driver`
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
2. `/investigate start platform-security` (posture 재확인)
3. `/evidence-dashboard category:driver`
4. `/override`
5. push/PR 시 hook policy 확인

## 6. 좋은 운영 습관

1. driver 변경 전 live snapshot과 platform-security posture를 남긴다.
2. 큰 변경 전에는 `tamper-surface`를 먼저 돌린다.
3. signing/symbol 문제는 evidence와 memory 양쪽에서 확인한다.
4. 반복 실패는 override로 넘기기보다 원인 패턴부터 줄인다.
5. POC 생성 직후 handoff 명령 순서(`/source-scan` → `/fuzz-func` → `/fuzz-campaign run`)를 그대로 따른다.
