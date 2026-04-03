# Kernforge 빠른 시작

이 문서는 Kernforge를 처음 쓰는 사람이 가장 빨리 핵심 흐름을 체감하도록 돕는 짧은 온보딩 가이드이다.

가장 먼저 기억할 것:
1. live 상태가 중요하면 `/investigate`
2. 공격자 관점이 중요하면 `/simulate`
3. 코드 범위를 좁혀 보고 싶으면 `/open` 후 `/review-selection` 또는 `/edit-selection`
4. 마지막에는 `/verify`
5. 결과는 `/evidence-dashboard`와 `/mem-search`로 확인

## 1. 5분 안에 익히는 핵심 루프

추천 순서:

```text
/investigate start driver-visibility guard.sys
/investigate snapshot
/simulate tamper-surface guard.sys
/open driver/guard.cpp
/review-selection integrity bypass paths
/edit-selection harden the selected integrity checks
/verify
/evidence-dashboard category:driver
```

이 흐름의 의미:
1. 현재 상태를 먼저 캡처한다.
2. `driver-visibility`는 드라이버 로드 원인 분석기가 아니라 가시성 triage snapshot이다.
3. 공격자 관점에서 약한 면을 먼저 본다.
4. 선택한 코드만 집중 리뷰/수정한다.
5. verification으로 닫는다.
6. evidence dashboard로 현재 위험 상태를 확인한다.

## 2. 가장 자주 쓰는 명령

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

선택 영역 작업:
- `/open <path>`
- `/review-selection [extra]`
- `/edit-selection <task>`

검증:
- `/verify`
- `/verify-dashboard`

증거와 기억:
- `/evidence-dashboard`
- `/evidence-search <query>`
- `/mem-search <query>`

정책:
- `/hooks`
- `/override`

## 3. 시작할 때 가장 좋은 시나리오

### Driver 변경

```text
/investigate start driver-visibility guard.sys
/simulate tamper-surface guard.sys
/open driver/guard.cpp
/review-selection signing and integrity assumptions
/verify
/evidence-dashboard category:driver
```

### Telemetry 변경

```text
/investigate start provider-visibility MyProvider
/simulate stealth-surface MyProvider
/open telemetry/provider.man
/review-selection provider visibility and schema drift
/verify
/evidence-search category:telemetry outcome:failed
```

## 4. 막혔을 때 가장 먼저 볼 것

1. `/status`
2. `/evidence-dashboard`
3. `/mem-search category:driver` 또는 `/mem-search category:telemetry`
4. `/hooks`

## 5. 다음 문서

더 자세한 흐름:
- [상세 사용 가이드](./FEATURE_USAGE_GUIDE_kor.md)

도메인별 운영 문서:
- [Driver 플레이북](./PLAYBOOK_driver_kor.md)
- [Telemetry 플레이북](./PLAYBOOK_telemetry_kor.md)
