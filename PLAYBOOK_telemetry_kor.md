# Kernforge Telemetry 플레이북

이 문서는 ETW, provider, manifest, XML, runtime visibility, forensic traceability 작업에 Kernforge를 어떻게 적용하면 좋은지 정리한 운영 플레이북이다.

## 1. 언제 이 플레이북을 쓰면 좋은가

1. provider manifest나 registration 코드가 바뀐다.
2. `.man`, `.mc`, `.xml` 파일이 바뀐다.
3. 이벤트 visibility, observer coverage, schema drift가 중요하다.
4. incident 이후 artifact retention이 중요하다.

## 2. 권장 기본 흐름

```text
/investigate start telemetry-provider MyProvider
/investigate snapshot MyProvider
/simulate stealth-surface MyProvider
/open telemetry/provider.man
/review-selection provider visibility and schema drift
/open telemetry/register_provider.cpp
/edit-selection align provider registration and fallback visibility
/verify
/evidence-search category:telemetry outcome:failed
/mem-search category:telemetry signal:provider
```

## 3. forensic 관점이 중요할 때 추가 흐름

```text
/simulate forensic-blind-spot MyProvider
/verify
/simulate-dashboard
```

이 흐름이 좋은 이유:
1. telemetry는 "보이는가"와 "나중에 추적 가능한가"를 같이 봐야 한다.
2. stealth-surface는 observer coverage를,
3. forensic-blind-spot은 artifact retention을 다시 보게 만든다.

## 4. 특히 자주 보는 신호

1. `signal:provider`
2. `signal:xml`
3. `signal:stealth`
4. `signal:forensics`

유용한 예:

```text
/evidence-search category:telemetry signal:provider
/evidence-search kind:simulation_finding signal:stealth
/mem-search category:telemetry signal:forensics
```

## 5. PR 전 체크 추천

1. `/verify`
2. `/evidence-search category:telemetry outcome:failed`
3. `/mem-search category:telemetry tag:provider`
4. `/override`

## 6. 좋은 운영 습관

1. manifest 변경 전후 live provider 상태를 snapshot으로 남긴다.
2. provider visibility와 fallback path는 selection review로 좁혀서 본다.
3. runtime에서는 보이지만 사후 분석이 안 되는 경우도 반드시 simulation으로 확인한다.
