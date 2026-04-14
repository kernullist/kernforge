# Kernforge 빠른 시작

이 문서는 Kernforge를 처음 쓰는 사람이 가장 빨리 핵심 흐름을 체감하도록 돕는 짧은 온보딩 가이드이다.

가장 먼저 기억할 것:
1. 워크스페이스가 크거나 낯설면 먼저 `/analyze-project`
2. live 상태가 중요하면 `/investigate`
3. 공격자 관점이 중요하면 `/simulate`
4. 코드 범위를 좁혀 보고 싶으면 `/open` 후 `/review-selection` 또는 `/edit-selection`
5. 마지막에는 `/verify`, 그리고 결과는 `/evidence-dashboard`와 `/mem-search`로 확인

## 1. 5분 안에 익히는 핵심 루프

추천 순서:

```text
/analyze-project driver startup, integrity, and signing architecture
/analyze-performance startup
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
1. 재사용 가능한 구조 지식과 performance lens를 먼저 만든다.
2. 현재 상태를 캡처한다.
3. `driver-visibility`는 드라이버 로드 원인 분석기가 아니라 가시성 triage snapshot이다.
4. 공격자 관점에서 약한 면을 먼저 본다.
5. 선택한 코드만 집중 리뷰/수정한다.
6. verification으로 닫는다.
7. evidence dashboard로 현재 위험 상태를 확인한다.

## 2. 가장 자주 쓰는 명령

프로젝트 분석:
- `/analyze-project [--mode map|trace|impact|security|performance] <goal>`
- `/analyze-performance [focus]`
- `/set-analysis-models`
- `--mode`를 생략하면 기본 모드는 `map`

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
- `/set-auto-verify [on|off]`
- `/detect-verification-tools`
- `/set-msbuild-path <path>`

증거와 기억:
- `/evidence-dashboard`
- `/evidence-search <query>`
- `/mem-search <query>`

정책:
- `/hooks`
- `/override`

계획과 tracked feature 작업:
- `/do-plan-review <task>`
- `/new-feature <task>`
- `/new-feature status [id]`
- `/new-feature implement [id]`

## 3. 시작할 때 가장 좋은 시나리오

### Driver 변경

```text
/analyze-project driver startup and integrity architecture
/investigate start driver-visibility guard.sys
/simulate tamper-surface guard.sys
/open driver/guard.cpp
/review-selection signing and integrity assumptions
/verify
/evidence-dashboard category:driver
```

### Telemetry 변경

```text
/analyze-project telemetry provider visibility and manifest architecture
/investigate start provider-visibility MyProvider
/simulate stealth-surface MyProvider
/open telemetry/provider.man
/review-selection provider visibility and schema drift
/verify
/evidence-search category:telemetry outcome:failed
```

### 여러 세션에 걸친 feature 작업

```text
/new-feature harden driver registration, preserve telemetry audit artifacts, and document rollback points
/new-feature status
/new-feature implement
/verify
/new-feature close
```

`/new-feature`는 `.kernforge/features/<id>` 아래에 spec, plan, task artifact를 남기며 진행 상태를 추적하고 싶을 때 쓰는 것이 좋다. `/do-plan-review`는 reviewer를 붙여 한 번에 계획을 다듬고 바로 실행하고 싶을 때 더 잘 맞는다.

## 4. 막혔을 때 가장 먼저 볼 것

1. `/status`
2. `/analyze-performance startup` 또는 관련 focus
3. `/evidence-dashboard`
4. `/mem-search category:driver` 또는 `/mem-search category:telemetry`
5. `/hooks`

빠른 해석:
1. `/status`는 현재 세션과 런타임 상태를 빠르게 보는 용도다. approval 상태도 여기서 본다.
2. `/config`는 provider 기본값, hooks, locale, verification toggle 같은 현재 적용 설정을 빠르게 보는 용도다.

Windows build tool이 없어 automatic verification이 실패하면:
1. 먼저 `/detect-verification-tools`를 실행한다.
2. 자동 탐지가 못 찾으면 예를 들어 `/set-msbuild-path "C:\Program Files\Microsoft Visual Studio\2022\Community\MSBuild\Current\Bin\MSBuild.exe"`처럼 직접 지정한다.
3. 당분간 편집 후 verification을 끄고 싶으면 `/set-auto-verify off`를 사용한다.

모델이 stage/commit/push/PR을 하려고 할 때:
1. Kernforge는 git 변경 작업을 파일 수정과 다른 승인 축으로 본다.
2. `Allow git?`는 현재 세션의 git mutation tool 승인이다.
3. 일반 review/edit 프롬프트에서는 사용자가 명시적으로 요청하지 않는 한 git mutation이 실행되지 않는 것이 기본이다.

모델이 큰 파일을 계속 다시 읽으려 할 때:
1. 최근 `read_file` cache hit는 같은 범위를 조용히 다시 읽는 대신 `NOTE:` 접두사로 이미 본 구간임을 드러낸다.
2. `grep` 결과에 `[cached-nearby:inside]` 또는 `[cached-nearby:N]`가 붙을 수 있는데, 이는 최근 읽은 범위와 겹치거나 아주 가깝다는 뜻이다.
3. 이 경우에는 큰 블록 전체를 다시 요청하기보다 빠진 인접 범위나 정확한 수정 지점만 좁게 요청하는 편이 좋다.

## 5. 입력 취소 팁

1. 입력 중 `Esc`는 현재 입력만 취소한다.
2. 모델 응답 대기 중 `Esc`는 진행 중 요청을 취소한다.
3. Windows 콘솔에서는 짧게 누른 `Esc`도 취소로 잡히도록 처리되어 있다.
4. 요청 취소 직후 다음 프롬프트는 잔여 `Esc` 입력 때문에 자동 취소되지 않도록 안정화된다.

## 6. 다음 문서

더 자세한 흐름:
- [상세 사용 가이드](./FEATURE_USAGE_GUIDE_kor.md)

도메인별 운영 문서:
- [Driver 플레이북](./PLAYBOOK_driver_kor.md)
- [Telemetry 플레이북](./PLAYBOOK_telemetry_kor.md)
