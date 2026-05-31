# analysis-project 기반 깊은 구조 질문 응답 개선 계획

기준 시점:
- 코드베이스 기준: 2026-04-29
- 대상 기능: `/analyze-project`, `/docs-refresh`, cached project analysis context, structural index v2, generated docs, dashboard

목표:

`/analyze-project`를 한 번 실행해 둔 상태에서 사용자가 프로젝트의 깊은 구조, 실행 흐름, 모듈 경계, 변경 영향, 보안 표면, Unreal/driver build coupling을 질문하면 Kernforge가 Codex나 Claude Code의 일반 workspace 탐색보다 더 자세하고 근거 있는 답변을 할 수 있어야 한다.

핵심 방향:

1. 분석 산출물은 이미 많은 편이다. 다음 병목은 "산출물을 질문 응답 시 충분히 다시 꺼내는 능력"이다.
2. 문서 생성 품질과 런타임 retrieval 품질을 같이 올려야 한다.
3. 깊은 구조 질문은 fast-path 요약 1개로 답하지 말고, docs section, graph neighborhood, source anchor, build context, stale marker를 함께 엮어 답해야 한다.
4. 답변은 요약형이 아니라 "구조 지도 + 근거 파일 + 흐름 + 위험/검증 포인트 + 다음 확인 위치"까지 포함해야 한다.

## 구현 상태 업데이트 (2026-04-28)

현재 Phase 1부터 Phase 5까지의 1차 구현은 완료됐다.

## 근본 개선 업데이트 (2026-04-29)

반복적인 SampleKernelDriver 재분석 테스트에서 드러난 핵심 문제는 "LLM worker/reviewer/synthesis가 각자 산출물을 해석하면서 같은 source anchor와 flow rule을 다르게 재라벨링할 수 있다"는 점이었다. 따라서 프롬프트 보정 중심의 접근을 줄이고, 분석 런타임이 먼저 deterministic architecture fact pack을 만든 뒤 모든 LLM 단계가 같은 사실 집합을 참조하도록 구조를 바꿨다.

완료된 것:

1. Deterministic Architecture Fact Pack 추가
   - `ProjectSnapshot`과 `KnowledgePack`에 `architecture_facts`를 추가했다.
   - fact pack은 `snapshot`, `structural_index_v2`, Unreal graph, build context에서 코드 기반으로 생성된다.
   - 포함 내용은 domain hints, authoritative top-level directory set, top-level table exclusion list, critical source anchors, verified flow facts, boundary facts, invariants다.

2. LLM 단계별 주입
   - worker prompt에 fact pack을 넣어 shard 분석 단계에서 closed root set, exact source anchor, flow separation invariant를 먼저 보게 했다.
   - reviewer prompt에도 같은 fact pack을 넣어 worker report가 deterministic facts와 충돌하면 거절할 수 있게 했다.
   - synthesis prompt에도 전체 fact pack을 넣어 approved shard report가 서로 충돌해도 fact pack을 우선하도록 했다.

3. 산출물과 런타임 QA 재사용
   - `latest/architecture_facts.json`과 run별 `_architecture_facts.json`을 저장한다.
   - generated docs와 knowledge digest에 `Deterministic Architecture Fact Pack` 섹션을 추가했다.
   - deep structure answer pack이 `architecture_facts`를 함께 렌더링하고 source anchor coverage 계산에도 포함한다.

4. 드라이버/보안 프로젝트 일반화
   - SampleKernelDriver 문자열에 의존하지 않고 `DriverEntry`, WDM/driver build context, IRP/IOCTL, `ObRegisterCallbacks`, process notify, minifilter, memory/handle/security overlay 같은 일반 Windows driver 패턴으로 동작한다.
   - top-level directory 표는 snapshot에서 계산한 root directory closed set만 사용하고, `.h/.cpp/.vcxproj/.sln/.inf` 및 nested path는 top-level row에서 제외하도록 invariant를 만든다.

5. 회귀 테스트
   - driver fixture에서 fact pack이 `windows_driver` domain hint, root directory closed set, nested/file exclusion, exact object callback registration anchor, REQUIRED IOCTL command spine을 생성하는지 검증한다.
   - worker/reviewer/synthesis prompt에 fact pack이 모두 주입되는지 검증한다.
   - runtime QA answer pack이 deterministic fact pack을 포함하는지 검증한다.

6. C/C++ registration edge scanner 보강
   - source anchor call edge 추출에 `DriverObject->MajorFunction[IRP_MJ_*] = Handler` 형태의 IRP dispatch table 등록을 추가했다.
   - `DriverUnload = Unload`, `PreOperation = Callback`, filter instance callback assignment 같은 function-pointer handoff를 registration/callback edge로 기록한다.
   - `PsSetCreateProcessNotifyRoutineEx(Callback, ...)`, thread/image/registry notify registration 계열에서 callback argument를 찾아 deterministic call edge로 만든다.
   - architecture fact pack의 `registered callback and dispatch edges` flow fact에 이 edge들을 올려, LLM이 DriverEntry 초기화와 runtime callback/dispatch registration을 임의로 섞지 않도록 했다.
   - driver-like fixture에서 IRP dispatch, unload callback, process notify registration edge와 fact pack flow를 검증한다.

7. 파일 레벨 driver registration table scanner 추가
   - `OB_OPERATION_REGISTRATION` 배열 initializer를 파일 레벨에서 찾아 object callback table symbol로 기록한다.
   - `FLT_OPERATION_REGISTRATION` 배열 initializer를 file filter callback table symbol로 기록한다.
   - table initializer 안의 callback token을 실제 source anchor와 resolve해 `registers_object_callback`, `registers_file_filter_callback` edge로 만든다.
   - 이 edge들은 architecture fact pack의 `registered callback and dispatch edges`에 포함되어, LLM이 minifilter/object callback 등록 지점을 추측하지 않고 deterministic fact를 사용하게 한다.
   - driver-like fixture에서 object callback table, minifilter operation table, fact pack flow 반영을 검증한다.

8. alias/macro/custom registration scanner 확장
   - `typedef OB_OPERATION_REGISTRATION Alias;`, `using Alias = FLT_OPERATION_REGISTRATION;` 같은 alias 기반 table 선언을 원래 registration table로 분류한다.
   - `CUSTOM_CALLBACK_REGISTRATION gCallbacks = { Callback, ... }`처럼 프로젝트별 custom initializer가 callback anchor를 포함하면 generic registration table로 기록한다.
   - `DECLARE_OBJECT_CALLBACK_TABLE(...)`, `DECLARE_MINIFILTER_OPERATION_TABLE(...)` 같은 macro invocation에서 callback 인자를 resolve해 synthetic registration table symbol과 callback edge를 만든다.
   - macro name, args, initializer descriptor를 함께 보고 object callback, file/minifilter callback, process notify callback, generic callback table을 구분한다.
   - driver-like fixture에서 alias object/file table, custom callback registration initializer, object/minifilter macro invocation을 모두 검증한다.

9. 제한적 C preprocessor expansion 추가
   - function-like `#define`을 수집하고 macro invocation에 parameter substitution을 적용한다.
   - `prefix##PreOperation` 같은 token-paste를 `prefix` 인자와 결합해 실제 callback symbol 후보로 만든다.
   - macro body 안에서 다른 registration macro를 호출하는 nested macro도 제한 깊이로 확장한다.
   - multiline macro definition 안의 invocation 자체는 source-level runtime registration으로 오인하지 않도록 preprocessor directive range를 스킵한다.
   - fixture에서 token-paste object callback, nested minifilter macro expansion을 검증한다.

10. include graph 기반 macro/alias 수집 추가
   - `.cpp` 파일 자체뿐 아니라 direct include와 한 단계 transitive include의 function-like registration macro definition을 함께 수집한다.
   - include된 헤더의 `typedef`/`using` registration alias도 `.cpp`의 table declaration 해석에 사용한다.
   - source-local macro/alias가 include definition보다 우선하도록 병합한다.
   - fixture에서 `RegistrationMacros.h`가 `RegistrationBase.h`를 include하고, `.cpp`가 그 macro/alias를 사용하는 경로를 검증한다.

11. deep include/conditional/variadic macro variant 지원
   - registration include closure를 더 깊게 추적하되 최대 depth/file cap을 둬 대형 프로젝트에서 비용 폭증을 막았다.
   - 조건부 컴파일로 같은 macro name이 여러 번 정의되는 경우 한쪽으로 덮어쓰지 않고 variant 후보를 모두 확장한다.
   - variadic macro의 `__VA_ARGS__`를 치환해 `VARARG_OBJECT_TABLE(prefix, pre, post)` 같은 registration DSL도 callback edge로 연결한다.
   - conditional object/file registration variant와 deep include macro fixture를 추가했다.

12. cached answer contract evaluator 추가
   - fast-path가 cached project analysis만으로 답변을 생성한 뒤, deterministic fact pack과 답변 텍스트를 규칙 기반으로 비교한다.
   - top-level directory table에서 closed set 밖의 경로나 명시 exclusion 파일/중첩 경로를 row로 올리면 blocking violation으로 처리한다.
   - accessor anchor를 Finalize/Unload/cleanup 같은 lifecycle anchor로 재라벨링하면 blocking violation으로 처리한다.
   - blocking violation이 있으면 cached fast-path 답변을 폐기하고 일반 tool loop로 fallback한다.
   - evaluator 단위 테스트로 top-level exclusion, 정상 파일 언급, accessor/lifecycle 오라벨링을 검증한다.

13. answer evaluator 확장
   - critical anchor location을 언급하면서 exact symbol name을 빼먹는 답변을 non-blocking warning으로 채점한다.
   - Windows driver 답변에서 IRP_MJ_CREATE/control-open validation과 IRP_MJ_DEVICE_CONTROL command dispatch를 한 흐름으로 뭉개면 blocking violation으로 처리한다.
   - DriverEntry/Initialize 흐름에 runtime callback/filter registration을 근거 없이 넣는 답변도 blocking violation으로 처리한다.
   - fast-path가 이런 blocking violation을 만들면 cached answer를 버리고 일반 tool loop로 fallback한다.

14. 문서 반영
   - README/README_kor에 `architecture_facts.json`, highlighted `Analysis artifacts:` 출력, latest mirror 교체, Project structure answer pack, OpenCode/OpenCode Go/Codex CLI provider 지원을 반영했다.
   - FEATURE_USAGE_GUIDE/FEATURE_USAGE_GUIDE_kor에 deterministic fact pack, C/C++ driver scanner 보강, cached answer evaluator, provider completion 목록을 반영했다.
   - ROADMAP_kor에는 완료 상태로 fact pack, latest 교체, handoff 전 산출물 경로 재출력을 기록했다.

남은 것:

1. fact pack scoring을 더 넓은 실제 프로젝트군에서 튜닝해야 한다. 특히 대형 Unreal, driver, mixed user/kernel/service solution에서 directory role, anchor priority, flow fact limit을 계측할 필요가 있다.
2. macro-wrapped/custom DSL registration table은 많이 개선됐지만 아직 완전한 compiler preprocessor는 아니다. include path가 build system define에 의존하거나 generated header가 아직 생성되지 않은 경우, 또는 macro가 복잡한 `#` stringification/토큰 산술을 쓰는 경우는 놓칠 수 있다.
3. answer evaluator는 주요 blocking contract 위반을 잡지만 아직 제한적이다. 향후 stale caveat 적합성, verification coverage, 다국어 문장형 flow separation까지 더 정밀하게 채점할 수 있다.

## 95점 목표 품질 보강 업데이트 (2026-04-28)

SampleKernelDriver 드라이버 프로젝트 답변 분석 결과를 기준으로 추가 보강을 진행했다. 기존 답변은 cached analysis를 사용하긴 했지만, 드라이버를 DLL처럼 표현할 위험, `SampleKernelAPI.cpp`를 외부 API entrypoint처럼 오해할 위험, user-mode IOCTL wrapper와 kernel-side dispatch/validation을 섞는 위험, 이전 persistent memory의 stale caveat를 최신 분석보다 우선하는 위험이 있었다.

추가 완료된 것:

1. Windows driver domain hint
   - answer pack에 `windows_driver`, `ioctl_control_surface`, `object_handle_filtering`, `process_monitoring`, `memory_inspection` 같은 domain hint를 추가했다.
   - fast-path instruction이 `windows_driver`를 보면 Windows kernel/WDM `.sys` driver로 설명하고, 소스 근거가 없으면 DLL이라고 부르지 않도록 했다.

2. Domain-specific critical anchors
   - `DriverEntry`, `SampleKernelCore::Initialize`, control device creation, kernel IOCTL dispatcher, request origin validation, command validation, IOCTL payload decryption, object callback registration/pre-callback, process monitor, dynamic kernel API resolver, policy engine, user-mode IOCTL client를 역할별 anchor로 선별한다.
   - answer pack 앞부분에 critical anchor와 source line을 먼저 렌더링해, 깊은 구조 질문에서 핵심 심볼이 token budget 뒤쪽으로 밀리지 않게 했다.

3. Driver-specific flow map
   - driver load/control spine, enforcement spine, kernel API dependency를 별도 flow로 렌더링한다.
   - user-mode `SampleKernelManager` wrapper와 kernel-side `SampleKernelCore` dispatch/validation을 분리해서 설명하라는 규칙을 포함했다.

4. Stale marker 정제
   - `no_previous_run`, `none`, `no stale markers` 같은 비-stale sentinel을 실제 stale caveat로 출력하지 않도록 필터링했다.
   - fast-path instruction이 최신 answer pack/docs marker를 persistent memory보다 우선하도록 강화됐다.

5. Intent 분류 보정
   - `드라이버 프로젝트 전체 구조`, `드라이버 초기화 흐름` 같은 질문이 `드라이버` 단어 때문에 보안 표면 질문으로 과분류되지 않도록 수정했다.
   - 전체 구조 질문은 deep map, 초기화/실행 흐름 질문은 flow trace로 분류된다. IOCTL/handle/memory/security surface가 명시된 경우에는 계속 security surface로 간다.

6. 최신 분석 재주입 안정성
   - 429 또는 모델 실패 이후 같은 user turn에 assistant 답변이 없는 경우, deep QA context를 다시 주입하도록 했다.
   - latest run id 판단이 `structural_index_v2`, `unreal_graph`, `docs_manifest`까지 fallback하도록 보강했다.

7. 회귀 테스트
   - SampleKernelDriver-like driver fixture를 추가해 driver 구조 질문에서 kernel critical anchor가 answer pack과 렌더링 결과에 모두 포함되는지 검증한다.
   - `go test -count=1 ./...`, `go build ./...`, `go vet ./...` 모두 통과했다.

8. Review diagnostics 표시 분리
   - `/analyze-project` summary JSON에 `review_provider_failures`와 `review_quality_issues`를 추가했다.
   - worker/reviewer provider 장애는 provider failure로, reviewer의 `needs_revision` 또는 품질 미달 판정은 quality issue로 분리해서 집계한다.
   - 최종 Markdown의 degradation banner, CLI status line, analysis handoff에서 provider 장애와 품질 이슈를 각각 별도 문장으로 표시한다.
   - provider review timeout이 발생한 SampleKernelDriver 같은 케이스가 산출물 품질 거절처럼 보이지 않도록 했다.

9. SampleKernelDriver 구조 QA 2차 피드백 반영
   - 테스트 답변은 전반적으로 개선됐다. `.sys` driver, user-mode manager, kernel-side IOCTL dispatch, critical source anchors를 구분해 설명했다.
   - 남은 문제는 answer pack의 domain flow가 anchor를 너무 직선적인 call chain처럼 제공해 모델이 `ValidateRequestorIsController`을 `DeviceIoControlIrpHandleRoutine` 내부 호출처럼 설명하거나, `Finalize`를 runtime enforcement path처럼 연결할 수 있다는 점이었다.
   - domain flow를 `load/init`, `IRP/control-channel`, `device-control command`, `object handle enforcement`, `process monitor`, `kernel API dependency`, `teardown rule`로 분리했다.
   - `DefaultIrpHandleRoutine`은 `kernel_irp_router`, `DeviceIoControlIrpHandleRoutine`은 `kernel_ioctl_dispatch`로 분리해 IRP router와 IOCTL command handler를 혼동하지 않게 했다.
   - `ValidateRequestorIsController` 설명을 control-channel request origin validation으로 보정하고, command payload validation과 직접 연결하지 말라는 evidence rule을 추가했다.
   - `SampleKernelAPI::Initialize`/`GetExportFunctionAddress`가 dynamic kernel API resolver 대표 anchor로 우선되도록 보정해 `_GetEnclosingSectionHeader` 같은 helper가 대표 흐름으로 과대표현되지 않게 했다.
   - `new_primary_scope`는 stale/invalidation이 아니라 cache scope marker이므로 deep QA stale marker에서 제외했다.

10. SampleKernelDriver 구조 QA 3차 피드백 반영
   - 최신 답변은 구조 레이어, IRP/control-channel, device-control command, object handle enforcement, process monitor, teardown rule을 분리해 설명하는 데 성공했다.
   - 남은 문제는 `Key Source Anchors` 표에서 `SampleKernelCore.cpp:1285` (`GetControlPid`)를 `Finalize`처럼 재라벨링한 점이었다. 이는 source-only anchor와 lifecycle 용어가 섞이면서 발생한 라벨링 오류다.
   - answer pack과 fast-path instruction에 "file:line anchor는 반드시 연결된 exact symbol name으로 라벨링하고, helper/accessor를 lifecycle 함수로 바꾸지 말라"는 규칙을 추가했다.
   - `SampleKernelCore::UnloadRoutine`을 `driver_unload_entry`, `SampleKernelCore::Finalize`를 `teardown_cleanup`으로 별도 critical anchor에 추가했다.
   - `SampleKernelProcessMonitor::StartProcessMonitor`가 `process_monitor` 대표 anchor가 되도록 하고, `SampleKernelAPI::SamplePsSetCreateProcessNotifyRoutineEx`는 `process_notify_api_wrapper`로 분리했다.
   - `SampleKernelFileFilter::Initialize`를 `file_minifilter` 대표 anchor로 우선해 임의 callback/helper line이 파일 필터 대표처럼 출력되지 않도록 했다.
   - critical anchor retrieval 한도를 18개로 늘려 driver 답변에 teardown/process monitor/file filter/API wrapper까지 함께 들어가게 했다.

11. SampleKernelDriver 구조 QA 4차 피드백 반영
   - 새 답변에서도 구조 흐름은 좋아졌지만, `SampleKernelCore::Initialize`/`DeviceIoControlIrpHandleRoutine`/`Finalize`의 line number가 다시 섞였다.
   - 원인은 generic `Source anchors` 목록이 verified critical anchors보다 앞쪽 또는 비슷한 위치에 있어 모델이 unlabeled line을 임의로 심볼에 붙였기 때문이다.
   - answer pack 렌더링을 `Priority docs -> Verified critical source anchors(role/exact symbol/file:line) -> Source anchors summary(unlabeled) -> Domain flow map` 순서로 재정렬했다.
   - domain flow 안의 모든 critical symbol을 `SymbolName (file:line)` 형태로 렌더링해 모델이 라인을 추정하지 않게 했다.
   - detailed verification hint는 뒤쪽으로 보내고, 보안 질문일 때만 security overlays와 verification/fuzz follow-through를 앞쪽에 배치하도록 조정했다.
   - deep map 질문에서는 flow map과 verified anchors가 잘리지 않도록 하고, security surface 질문에서는 security overlays/verification이 token budget 안에 남도록 회귀 테스트를 보강했다.

12. analyze-project evidence packet/coverage ledger 반영
   - worker prompt가 파일 앞부분 위주의 `buildFileContext` 대신 `EvidencePacket` 테이블과 exact source slice를 우선 사용한다.
   - `claims[].evidence_packet_ids`를 추가해 source anchor와 packet id가 함께 남도록 했다.
   - long C++ file fixture에서 파일 뒤쪽 `DispatchIoctl` body가 worker evidence에 포함되는지 회귀 테스트를 추가했다.
   - `coverage_ledger.json`와 `evidence_packets.json`를 run별 artifact와 `latest/` mirror에 저장하고, `COVERAGE_LEDGER.md` / `EVIDENCE_PACKETS.md` generated docs와 dashboard metric에도 노출한다.
   - single-model 환경에서 reviewer를 건너뛴 shard는 `approved`가 아니라 `model_review_skipped`로 집계한다.
   - non-Unreal Windows/security fixture에서 `security_driver`, `security_ioctl`, `security_handles`, `security_memory` semantic shard가 생성되는지 검증한다.

12. SampleKernelDriver 구조 QA 5차 피드백 반영
   - 최신 답변은 line anchor 라벨링은 안정화됐지만, `StartObjectFilter`를 DriverEntry/Core Initialize 흐름에 넣고 `ValidateRequestorIsController`을 `DeviceIoControlIrpHandleRoutine` 내부 command spine처럼 설명하는 문제가 남았다.
   - answer pack과 fast-path instruction에 driver flow guardrail을 추가했다. `StartObjectFilter`는 control operation이 callback registration을 시작/중지하는 경로이고, Core Initialize는 `ObjectFilter::Initialize`로 상태를 준비하는 경로임을 명시했다.
   - `ValidateRequestorIsController`은 `IRP_MJ_CREATE`/control-open validation spine에 두고, `DeviceIoControl` command spine에서는 `DecryptIoctlData`, per-command size/shape check, `IsValidCommand`, `GetControlPid`/`requestorPid` 기반 검사로 분리하도록 했다.
   - domain flow를 `IRP router spine`, `control-open validation spine`, `device-control branch spine`, `device-control command spine`으로 더 세분화했다.
   - `SampleKernelObjectFilter::Initialize`를 `object_filter_initialization`, `SampleKernelProcessMonitor::Initialize`를 `process_monitor_initialization` critical anchor로 추가해 initialization과 runtime callback registration을 구분했다.
   - fixture에 `GetControlPid`, object/process monitor Initialize anchor와 관련 call edge를 추가하고, load/init spine에 `StartObjectFilter`가 들어가지 않는지와 DeviceIoControl command spine에 `ValidateRequestorIsController` 직접 단계가 들어가지 않는지 회귀 테스트를 추가했다.
   - 리뷰 중 `GetControlPid/requestorPid` 규칙이 해당 anchor가 없는 generic Windows-driver fixture에도 들어갈 수 있는 범용성 문제를 발견해, `GetControlPid` critical anchor가 있을 때만 출력되도록 좁혔다.

13. SampleKernelDriver 구조 QA 6차 피드백 반영
   - 최신 답변은 `StartObjectFilter`/`ValidateRequestorIsController`의 위치는 바로잡았지만, `DeviceIoControlIrpHandleRoutine` 내부 command spine을 `DeviceIoControl 핸들러 -> IOCTL 명령별 디스패치` 수준으로 너무 얇게 요약했다.
   - answer contract와 fast-path instruction에 "Domain-specific flow map의 모든 relevant spine을 포함"하고, IOCTL 설명에서는 `device-control branch spine`과 `device-control command spine`을 모두 포함하라는 completeness rule을 추가했다.
   - `device-control command spine`에 `DecryptIoctlData`, per-command size/shape check, `IsValidCommand`, command handler, `GetControlPid/requestorPid` 기반 검사까지 들어가도록 기존 flow를 더 강하게 보존한다.
   - `Common/`을 `SampleKernelDriver/` 하위처럼 렌더링할 수 있는 파일 트리 혼동을 줄이기 위해 answer pack 초반에 `Root folder map (exact sibling paths)`를 추가했다.
   - folder placement rule을 추가해 slash-separated path를 그대로 사용하고, `Common`은 실제 경로가 `SampleKernelDriver/Common`이라고 나올 때만 드라이버 하위로 중첩하라고 명시했다.

14. SampleKernelDriver 구조 QA 7차 피드백 반영
   - 최신 답변은 루트 폴더 sibling 규칙은 잘 반영했지만, `SampleKernelAPI.h/`, `SampleKernelCore.h/`, `SampleKernelProcessMonitor.h/` 같은 header file을 top-level directory처럼 추가했다.
   - folder placement rule을 강화해 `.h`, `.hpp`, `.cpp`, `.vcxproj`, `.sln`, `.inf` 등 확장자를 가진 경로는 file이지 root directory가 아니라고 명시했다.
   - answer pack 초반에 `Required driver answer facts`를 추가해 authoritative top-level directories와 exact IOCTL command spine을 모델이 그대로 복사할 수 있게 했다.
   - `device-control command spine`을 `REQUIRED device-control command spine`으로 렌더링하고, `DecryptIoctlData`, `IsValidCommand`, `GetControlPid/requestorPid` anchor를 실명으로 포함하도록 했다.
   - `projectStructureRootFolders`에서 file-like path를 필터링하는 helper를 추가하고, header/source file path가 root folder map에 남지 않는 회귀 테스트를 추가했다.
   - 리뷰 중 required facts가 security surface 질문의 overlays/verification과 deep map 질문의 flow/anchors를 token budget 밖으로 밀 수 있음을 확인해, security intent에서는 보안/검증 섹션을 우선하고 deep map intent에서는 required facts와 domain flow를 verified anchors보다 먼저 렌더링하도록 순서를 재조정했다.

15. Windows driver 범용화 보정
   - SampleKernelDriver은 회귀 fixture와 실전 품질 확인 대상일 뿐, production answer pack/fast-path 규칙이 특정 프로젝트명이나 특정 심볼명에 묶이면 안 된다.
   - production 코드의 driver QA 규칙에서 `SampleKernelManager`, `SampleKernelCore`, `StartObjectFilter`, `ValidateRequestorIsController`, `GetControlPid` 같은 전용 문구를 제거했다.
   - 문구를 `user-mode control/client wrapper`, `kernel-side IRP/IOCTL dispatch`, `runtime filter start/registration symbol`, `request-origin validation symbol`, `control PID/accessor symbol`처럼 일반화했다.
   - critical anchor classifier도 `SampleKernelDriver*` prefix가 아니라 `objectfilter::initialize`, `processmonitor::initialize`, `PsSetCreateProcessNotifyRoutine`, `filefilter::initialize`, `kernel API resolver`, `controlPid/requestorPid/callerPid` 같은 드라이버 공통 패턴으로 동작하도록 바꿨다.
   - `analysis_qa_context.go`와 `analysis_context.go`에 SampleKernelDriver 전용 문자열이 남지 않는 것을 확인했다. SampleKernelDriver 문자열은 fixture/test data와 실제 검증 기록 문서에만 남긴다.
   - 최신 모델 출력에서 header file을 top-level directory 표에 섞고, runtime filter registration anchor의 line number를 `...`로 흐리는 문제가 남아 있었다.
   - Required driver facts를 강화해 top-level directory table은 authoritative root list만 쓰고 추가 row를 만들지 말라고 명시했다. 파일은 file/source section에서만 언급하도록 했다.
   - runtime filter start/registration anchor를 별도 required fact로 렌더링해, 해당 심볼을 인용할 때 exact file:line을 복사하고 ellipsis로 대체하지 않도록 했다.
   - 최신 출력에서도 모델이 헤더 파일과 nested/build 폴더를 최상위 디렉토리 표에 추가했기 때문에, `CLOSED SET` top-level directory table을 복사 가능한 markdown table 형태로 answer pack에 넣었다.
   - `Never list these paths as top-level directory rows` fact를 추가해 nested folder, source/header/project file 후보를 명시적인 제외 목록으로 렌더링한다.

16. Windows driver 폴더/흐름 산출물 품질 보정
   - 최신 테스트 출력은 IOCTL/control-open/object-filter 분리는 좋아졌지만, Folder Summary 계열 산출물의 노이즈 때문에 top-level directory 표에 파일 또는 nested path가 섞일 여지가 남아 있었다.
   - answer pack 렌더링에서 verified critical source anchors를 domain flow보다 앞에 배치하고 deep QA context budget을 늘려, closed root set, exact anchors, required command spine이 동시에 잘리지 않게 했다.
   - load/init flow를 단순 `A -> B -> C -> D` 체인에서 `DriverEntry -> Core Initialize; Core Initialize coordinates: ...` 형태로 바꿔, 하위 초기화들이 API resolver 내부 단계처럼 오해되는 것을 줄였다.
   - developer folder builder가 `Header.h / Source.cpp`, `Header.h (설명)`, `Source.cpp:120` 같은 annotated evidence를 실제 파일 path로 정규화하도록 했다. 따라서 header/source/project 파일이 folder record로 승격되지 않는다.
   - folder responsibility classifier를 generic하게 보정했다. `BuildCab`, `Batch`, `.bat`, `.ddf`, signing/package/deploy 계열은 build/release/tooling으로, `Common/UserCommon.h`, `KernelCommon.h`, `pehelper` 계열은 shared contracts로, `DriverEntry`, IRP/IOCTL, WDM, Ob/Ps/Flt 콜백 계열은 kernel driver runtime으로 분류한다.
   - 위 보정은 SampleKernelDriver 전용 문자열 없이 적용된다. production Go code에는 `sample` 문자열이 남지 않는 것을 case-insensitive 검색으로 확인했다.
   - 추가 회귀 테스트:
     - annotated source reference가 folder path로 남지 않는지 확인
     - generic `Driver/Common/BuildCab/Batch` fixture에서 driver/shared/build 책임이 올바르게 분류되는지 확인
     - driver answer pack이 closed root set, exact exclusion list, verified critical anchors, required IOCTL command spine을 모두 렌더링하는지 확인

17. Windows driver 폴더 QA 2차 보정
   - 최신 테스트 출력에서 `process/`가 top-level directory로 hallucination 되었고, driver source root가 user-mode harness 책임으로 뒤집혔다.
   - 원인:
     - `process/thread callbacks`, `kernel/user-mode contracts` 같은 자연어 slash phrase가 path candidate로 해석될 수 있었다.
     - folder responsibility inference가 build context의 `wdm_driver`, `.sys`, project/target 정보를 충분히 보지 못했다.
     - 기존 잘못된 responsibility 문구를 다시 inference corpus에 넣으면 `driver control harness` 문구가 스스로 강화될 수 있었다.
   - 보정:
     - slash candidate 필터를 추가해 `process/thread`, `kernel/user-mode`, `client/server`, `request/response` 같은 자연어 쌍을 folder path로 만들지 않는다.
     - snapshot directory 목록도 folder record 원천으로 포함해 실제 root directory는 보존하되, 자연어 pseudo-folder는 제외한다.
     - folder inference corpus에 build context의 id/name/kind/directory/project/target/source/files를 포함한다.
     - responsibility inference는 기존 responsibility 문구를 재사용하지 않고, 파일/심볼/build context 근거에서 다시 계산한다.
   - 회귀 테스트:
     - `process/thread`와 `kernel/user-mode`가 folder path로 남지 않는지 확인했다.
     - `wdm_driver`/`.sys` build context가 있는 generic `Driver/` root가 kernel driver runtime으로 분류되는지 확인했다.

18. 재분석 산출물 20260428-143105 리뷰 반영
   - 새 산출물의 최종 보고서는 큰 흐름과 앵커 품질이 개선됐다. `process/` pseudo-folder는 최종 보고서에는 나오지 않았고, DriverEntry/Core/IOCTL/Object/Process flow도 대체로 안정적이다.
   - 남은 원재료 품질 이슈:
     - `latest/docs/FOLDER_MAP.md`에서 `SampleKernelDriver/` 책임이 user-mode harness로 뒤집혔다.
     - `DEVELOPER_OVERVIEW.md`의 Project Shape가 driver core보다 `Common PE Helper` 같은 보조 shard를 lead로 잡았다.
     - IOCTL contract table에서 user-mode manager wrapper가 `kernel dispatch or handler`로 분류됐다.
   - 보정:
     - folder responsibility inference에서 `DriverEntry`, `wdm_driver`, `.sys`, `IRP_MJ_DEVICE_CONTROL`, `DeviceIoControlIrpHandleRoutine`, `ObRegisterCallbacks`, `PsSetCreateProcessNotifyRoutine`, `FltRegisterFilter` 같은 강한 driver runtime evidence가 있으면 common/harness 문구보다 먼저 kernel driver runtime으로 분류한다.
     - folder specificity score에서 canonical `kernel driver runtime, privileged dispatch...` 책임이 짧은 worker 책임 문구보다 우선되게 했다.
     - Developer Overview의 Project Shape는 windows_driver domain hint가 있으면 ProjectSummary의 보조 shard lead 문구를 그대로 쓰지 않고, kernel driver root/user-mode harness/shared contracts/build context를 요약한다.
     - IOCTL role classifier는 `manager`, `testconsole`, `client`, `user-mode`, `service` 계열 user-mode wrapper를 kernel-side IRP/dispatch handler보다 먼저 분리한다.
     - `DecryptIoctlData`, `IsValidCommand`, payload/buffer/decrypt/validate 계열은 dispatch handler가 아니라 validation/buffer gate로 분류한다.
   - 회귀 테스트:
     - common/helper subsystem이 섞여도 `wdm_driver` build context와 DriverEntry가 있는 driver root가 driver runtime으로 유지되는지 확인했다.
     - windows_driver overview가 stale/common lead summary를 override하는지 확인했다.
     - generic `DriverManager::ControlOperation`은 user-mode request issuer, `DriverCore::DeviceIoControlIrpHandleRoutine`은 kernel dispatch, `DecryptIoctlData`는 validation/buffer gate로 분류되는지 확인했다.

19. 재분석 산출물 20260428-151901 리뷰 반영
   - 새 산출물은 최종 보고서와 generated docs 모두 전반적으로 좋아졌다. Windows kernel/WDM `.sys` driver, user-mode control/test layer, kernel-side IOCTL dispatch, object/process/file subsystem, critical anchors가 대부분 안정적으로 포함됐다.
   - 남은 원재료 품질 이슈:
     - `latest/docs/FOLDER_MAP.md`에서 `SampleKernelTestConsole/`이 user-mode driver control harness가 아니라 build/release/packaging tooling으로 오분류됐다.
     - `DEVELOPER_OVERVIEW.md`의 Project Shape가 root `./`를 user-mode harness/control root로 선택했다. root에는 solution/config manifest가 있으므로 하네스 root가 아니다.
     - 최종 합성 보고서에서 object filter `Initialize`와 runtime callback registration을 다시 섞어 `Initialize -> ObRegisterCallbacks`처럼 표현할 수 있었다.
   - 보정:
     - folder responsibility inference를 재정렬해 강한 driver runtime evidence를 우선하고, 그 다음 user-mode driver control harness를 build/release tooling보다 먼저 분류한다.
     - `CreateService`/`StartService`/`OpenSCManager`/`ControlService`/`DeviceIoControl`/`TestConsole`/`Manager` 계열은 user-mode service lifecycle/control harness로 분류한다.
     - `.sln`, `.props`, `.targets`, `.vmp`, solution marker가 있는 root `.`는 solution root/manifests/top-level configuration으로 분류한다.
     - Project Shape에서 driver/harness/shared root를 찾을 때 root `.`는 제외해 실제 하위 root를 선택하게 했다.
     - synthesis system prompt와 driver worker prompt에 "state initialization"과 "runtime callback/filter registration"을 분리하라는 generic Windows driver 규칙을 추가했다. `Initialize`가 callback registration을 수행한다고 말하려면 제공된 flow에 명시 증거가 있어야 한다.
   - 회귀 테스트:
     - TestConsole-like application project가 `.vcxproj`/Build And Release 문맥을 갖더라도 user-mode bootstrap/control harness로 유지되는지 확인했다.
     - solution root `.`가 harness로 선택되지 않고 configuration root로 분류되는지 확인했다.
     - driver synthesis/worker prompt가 initialization과 runtime registration을 분리하도록 검증했다.

20. 재분석 산출물 20260428-160336 리뷰 반영
   - 새 산출물은 `SampleKernelTestConsole/` 책임과 `DEVELOPER_OVERVIEW.md` Project Shape가 의도대로 개선됐다. user-mode harness/control root가 `SampleKernelTestConsole/`로 잡히고, kernel driver root와 shared contract root도 올바르게 분리됐다.
   - 남은 품질 이슈:
     - `FOLDER_MAP.md`에서 root `.` record가 solution/config root가 아니라 worker report의 bare filename 노이즈에 끌려 `Driver initialization...` 책임으로 남았다.
     - Startup lens의 `Kernel/runtime driver entry files`에 solution startup application entry file이 섞일 수 있었다. 원인은 project name/path에 `kernel` 문자열이 들어간 application project까지 driver로 보는 과도한 heuristic이었다.
     - 최종 synthesis 보고서의 IOCTL 흐름에서 request-origin/open validation이 DeviceIoControl command handler 내부 단계처럼 표현될 수 있었다.
   - 보정:
     - folder responsibility specificity에서 `solution root`와 `top-level configuration`을 강한 canonical responsibility로 승격했다. root manifest가 있는 `.` record는 bare source filename worker noise보다 solution/config root classification이 우선한다.
     - driver entrypoint file selector를 강화했다. solution project는 output/kind가 driver/WDM/kernel-mode로 확인되거나 entry file 자체가 driver entry처럼 보일 때만 kernel/runtime driver entry로 분류한다. `test`, `console`, `client`, `manager`, `app` 계열 user-mode startup file은 driver entry fallback에서 제외한다.
     - synthesis system prompt와 IOCTL worker prompt에 request-origin/open validation과 DeviceIoControl command handling을 분리하는 generic Windows driver rule을 추가했다.
   - 회귀 테스트:
     - root `.`에 `Driver initialization...` worker responsibility가 붙어도 solution root/manifests/top-level configuration으로 유지되는지 확인했다.
     - user-mode test console entry file이 kernel/runtime driver entry files에 들어가지 않는지 확인했다.
     - IOCTL synthesis/worker prompt가 create/open request-origin validation을 DeviceIoControl command spine과 분리하도록 검증했다.

21. 재분석 산출물 20260428-171116 리뷰 반영
   - 새 산출물은 root `.`, kernel/runtime driver entry file, Project Shape의 driver/harness 분리가 개선됐다. `Kernel/runtime driver entry files`에는 `SampleKernelDriver/SampleKernelDriver.cpp`만 남았고, solution startup executable과 kernel DriverEntry가 분리됐다.
   - 남은 품질 이슈:
     - `Common/` 폴더가 `SampleKernelTestConsole` 관련 worker 문구에 오염되어 shared contract/common utility가 아니라 user-mode bootstrap/service lifecycle harness로 오분류됐다.
     - 최종 synthesis 개요에서 파일 미니필터 서브시스템 존재 때문에 전체 프로젝트를 "미니필터 드라이버"로만 표현할 위험이 있었다. 실제로는 build/output evidence 기준 Windows kernel/WDM `.sys` driver이고, minifilter는 파일 필터 서브시스템으로 설명하는 편이 정확하다.
   - 보정:
     - `Common/`, `Shared/`, `Include/`, `Contracts/`, `UserCommon`, `KernelCommon`, `pehelper`, `SecureMetaString`, `ntapi.h` 같은 경로/키 파일 근거가 있으면 shared kernel/user-mode contracts로 먼저 분류한다.
     - user-mode driver control harness 판별은 folder/key file 또는 symbol evidence에 `TestConsole`, `Manager`, SCM/DeviceIoControl 계열이 있을 때만 적용한다. 단순히 관련 subsystem 설명에 TestConsole 단어가 섞인 shared folder는 harness로 승격하지 않는다.
     - synthesis prompt에 "WDM/kernel `.sys` driver"와 "minifilter/file-filter subsystem"을 구분하라는 generic Windows driver classification rule을 추가했다.
   - 회귀 테스트:
     - `Common/UserCommon.h`가 test console service lifecycle 설명과 함께 등장해도 `Common/`이 shared contract root로 유지되는지 확인했다.
     - Project Shape에 `Shared contract root: Common/`이 유지되는지 확인했다.
     - synthesis prompt가 WDM driver를 minifilter-only로 과분류하지 않도록 검증했다.

22. 반복 테스트 루프를 줄이기 위한 근본 보정
   - 문제:
     - 최신 산출물을 볼 때마다 개별 증상을 보정하면 끝이 없다. 공통 원인은 worker가 생성한 자연어 responsibility와 실제 경로/파일/심볼/build context 기반 responsibility가 같은 필드에서 경쟁한다는 점이었다.
     - `SampleKernelCore.cpp`처럼 경로 없는 bare filename이 worker report에서 나오면 실제 snapshot 위치로 resolve되지 못하고 root `.` record를 오염시켰다.
     - 최종 synthesis prompt에는 정확한 top-level directory closed set과 driver activation facts가 충분히 구조화되어 들어가지 않아, 모델이 디렉터리 트리와 startup/runtime 경계를 다시 추론해야 했다.
   - 구조 보정:
     - folder responsibility를 evidence-derived canonical responsibility로 우선 결정한다. deterministic inference가 `source area`보다 구체적인 값을 내면 worker narrative responsibility는 override할 수 없다.
     - worker narrative responsibility는 실제 경로/파일/심볼/build context evidence가 부족해 `source area`로만 분류되는 폴더에서만 fallback 설명으로 사용한다.
     - subsystem key/evidence/high-risk path 후보는 snapshot 파일 basename과 유일하게 매칭될 때 실제 경로로 resolve한다.
     - 유일 매칭이 안 되는 bare source/header/project filename은 root `.` record 근거로 승격하지 않는다.
     - `API_AND_ENTRYPOINTS.md`에도 Startup And Entrypoint Lens, Domain Critical Anchors, IOCTL contract를 넣어 API 문서만 읽어도 startup executable과 kernel DriverEntry, user-mode wrapper와 kernel dispatch를 구분할 수 있게 했다.
     - synthesis prompt에 top-level directory closed set과 driver architecture facts를 명시적으로 넣었다. 최종 보고서 작성 모델은 header/source/project/INF/nested folder를 top-level directory로 재구성하지 말라는 제약을 받는다.
     - deep structure fast-path는 answer pack이 없을 때 preflight 자체를 건너뛰지 않고 `NEEDS_TOOLS`로 정상 fallback하게 했다. 부족한 캐시 상황에서 내부 routing marker가 일반 답변으로 새는 것을 막는다.
   - 기대 효과:
     - `Common/` 같은 shared contract 폴더가 TestConsole 관련 narrative에 오염되어 harness로 바뀌는 문제가 줄어든다.
     - root `.`가 bare filename worker noise 때문에 driver/core folder처럼 보이는 문제가 줄어든다.
     - 최종 보고서 단계에서 "폴더 트리", "startup executable vs kernel DriverEntry", "WDM driver vs minifilter subsystem"을 다시 맞추기 위한 사후 패치가 줄어든다.
   - 회귀 테스트:
     - path 없는 source filename이 root folder record를 만들지 않는지 확인했다.
     - worker narrative가 섞여도 `Common/`은 shared contract root, TestConsole은 user-mode harness, driver root는 kernel driver runtime으로 유지되는지 확인했다.
     - API doc에 startup lens와 IOCTL contract가 포함되는지 확인했다.
     - synthesis prompt가 closed top-level directory set과 driver entry/startup separation facts를 포함하는지 확인했다.
     - answer pack이 없는 deep structure 질문은 fast-path preflight 후 normal tool loop로 fallback되는지 확인했다.

23. 재분석 산출물 20260428-183119 리뷰 반영
   - 좋아진 점:
     - `FOLDER_MAP.md`에서 `SampleKernelDriver/`, `SampleKernelTestConsole/`, `Common/`, root `.`, `BuildCab/`, `Batch/`, `VMProtect/` 책임이 안정화됐다.
     - `DEVELOPER_OVERVIEW.md` Project Shape에 kernel driver root, user-mode harness/control root, shared contract root가 모두 올바르게 들어갔다.
     - `API_AND_ENTRYPOINTS.md`에 Startup And Entrypoint Lens, Domain Critical Anchors, IOCTL contract가 반영됐다.
   - 남은 품질 이슈:
     - 최종 synthesis 보고서에서 실제 `main()` 호출 체인과 manager가 제공하는 available operations가 섞였다. Evidence appendix에는 `main()`이 `CreateDriverService`와 `StopDriverService`만 호출한다고 나오지만, 실행 체인에는 `StartDriverService`, `CreateDeviceHandle`, `AddProtectionTargetProcessPath`, `ControlOperation`까지 실행 단계처럼 포함됐다.
     - ObjectFilter activation 설명에서 `SampleKernelObjectFilter::Initialize -> ObRegisterCallbacks`처럼 상태 초기화와 runtime registration이 다시 섞였다. Domain Critical Anchors에는 `object_filter_initialization`과 `object_callback_registration`이 분리되어 있었으므로 최종 합성 단계의 guardrail이 부족한 문제다.
   - 보정:
     - synthesis system prompt에 "실행 흐름에는 관측된 runtime/internal_flow call만 넣고, 선언된 public method/available operation/lifecycle helper는 available operations/API section으로 분리하라"는 규칙을 추가했다.
     - startup worker prompt에도 visible main/startup call과 declared manager APIs를 분리하라는 규칙을 추가했다.
     - driver worker prompt에는 object/handle filter `Initialize`와 `Start/Register`가 모두 보이면 `Initialize`는 state setup, `Start/Register`는 callback registration으로 분리하라는 규칙을 추가했다.
     - worker reports corpus를 훑어 `main()` + declared public methods/available operations 패턴, `ObjectFilter::Initialize` + `StartObjectFilter`/`ObRegisterCallbacks` 패턴이 보이면 synthesis prompt에 `Synthesis guardrails from worker evidence`를 자동 주입한다.
   - 회귀 테스트:
     - synthesis prompt가 startup declared methods를 executed startup chain에 넣지 말라는 guardrail을 포함하는지 확인했다.
     - object/handle filter Initialize와 Start/Register/ObRegisterCallbacks 분리 guardrail이 synthesis prompt에 들어가는지 확인했다.

실제 SampleKernelDriver 기존 산출물 확인:

1. `C:\git\sample-client\.kernforge\analysis\latest\structural_index_v2.json`에는 `DriverEntry`, `SampleKernelCore::Initialize`, `SampleKernelCore::DeviceIoControlIrpHandleRoutine`, `SampleKernelCore::ValidateRequestorIsController`, `SampleKernelCore::DecryptIoctlData`, `SampleKernelObjectFilter::StartObjectFilter`, `SampleKernelObjectFilter::*ObjectPreCallback`, `SampleKernelAPI::SampleMmCopyVirtualMemory`가 이미 기록되어 있다.
2. 따라서 새 Kernforge 런타임 answer pack은 기존 최신 산출물만으로도 driver critical anchor를 선별할 수 있다.
3. 다만 generated docs의 `Domain Critical Anchors` 섹션과 새 manifest metadata는 새 바이너리로 `/analyze-project` 또는 docs refresh를 다시 실행해야 실제 산출물에 반영된다.

실제 SampleKernelDriver 재분석 결과 확인:

1. 대상 결과: `C:\git\sample-client\.kernforge\analysis\20260428-091058_map_map_the_architecture,_subsystems,_ownership,_mod.md`
2. 최종 보고서 초반에서 `SampleKernelDriver`을 Windows kernel-mode driver, output `tvk.sys`로 설명했고, `SampleKernelTestConsole`을 user-mode development harness로 분리했다.
3. `DriverEntry`, `SampleKernelCore::Initialize`, `DeviceIoControlIrpHandleRoutine`, `ValidateRequestorIsController`, `SampleKernelAPI::Initialize`, object/process/file filter 흐름이 구조 설명에 포함됐다.
4. 새 generated docs에는 `DEVELOPER_OVERVIEW.md`와 `CODE_STRUCTURE_REFERENCE.md`의 `Domain Critical Anchors` 섹션이 생성됐고, `docs_manifest.json`에는 `query_intents`, `entity_refs`, `graph_refs`, `domain_critical_anchors` metadata가 반영됐다.
5. 남은 품질 이슈도 발견했다.
   - provider review timeout 때문에 최종 보고서가 `Analysis With Review Failures`로 시작한다.
   - critical anchor 선별에서 동점일 때 `entity:*`가 concrete function보다 먼저 선택되어 `file=none` anchor가 생길 수 있었다.
   - `kernel_ioctl_dispatch` 대표가 `DeviceIoControlIrpHandleRoutine`보다 `DefaultIrpHandleRoutine`으로 잡힐 수 있었다.
   - IOCTL role 표에서 `ioctl_surface` tag 때문에 dispatch/handler가 `IOCTL code or constant`로 잘못 분류될 수 있었다.
   - `SampleKernelDriver` 폴더가 `DeviceIoControl` 단어 때문에 user-mode harness로 잘못 분류될 수 있었다.
6. 위 이슈는 코드 보정 완료.
   - concrete file/line/function anchor가 `entity:*`보다 우선되도록 critical anchor scoring/tie-break를 보강했다.
   - `DeviceIoControl`/`IRP_MJ_DEVICE_CONTROL` dispatcher를 generic IRP handler보다 우선했다.
   - IOCTL role classifier에서 handler/validation/user-mode issuer/constant 순서를 보정했다.
   - folder/module responsibility classifier에서 `Common`, `SampleKernelTestConsole`, kernel driver folder를 더 명확히 분리했다.
   - provider review timeout과 reviewer quality rejection을 별도 counter/banner/handoff로 분리했다.
   - driver QA answer pack에서 IRP router, IOCTL command dispatch, request-origin validation, object callback registration, process notify monitor, teardown path를 분리했다.
   - SampleKernelDriver-like fixture 회귀 테스트를 추가했고 `go test -count=1 ./...`, `go build ./...`, `go vet ./...`를 통과했다.

완료된 것:

1. Phase 1 런타임 QA retrieval 강화
   - `analysis_qa_context.go`를 추가해 `ProjectAnalysisQAIntent`, `ProjectStructureAnswerPack`, intent별 retrieval policy, answer pack renderer를 구현했다.
   - `renderRelevantProjectAnalysisContext`가 깊은 구조 질문에서 answer pack을 먼저 주입하고, 기존 vector/structural index context도 유지한다.
   - deep QA fast-path는 `Project structure answer pack`이 실제 주입된 경우에만 동작하며, 답변 계약을 만족하지 못하면 `NEEDS_TOOLS`로 빠지도록 instruction을 강화했다.
   - 한국어 `실행 흐름` 질문이 작업 실행/수정 의도로 오분류되던 문제를 수정했다.

2. Phase 2 graph answer view 추가
   - module/folder/runtime/build/security/Unreal/impact 성격의 답변용 graph view를 answer pack에 넣었다.
   - raw edge만 나열하지 않고 `Title`, `Kind`, `SourceAnchors`, `RecommendedDocs`, `VerificationHints`를 가진 compact view로 렌더링한다.
   - flow 질문에서 graph view가 token budget 뒤쪽에 밀려 잘리지 않도록 answer pack 앞부분에 배치했다.

3. Phase 3 docs manifest metadata 확장
   - `AnalysisGeneratedDoc`에 `QueryIntents`, `Priority`를 추가했다.
   - `AnalysisDocSection`에 `QueryIntents`, `Priority`, `EntityRefs`, `GraphRefs`를 추가했다.
   - vector corpus metadata에도 intent, priority, entity refs, graph refs를 기록한다.
   - manifest hit와 vector hit가 중복될 때 stale/source/query metadata가 유실되지 않도록 doc hit 병합 로직을 추가했다.

4. Phase 4 developer docs 보강
   - `DEVELOPER_OVERVIEW.md`: `Architecture Layers`, `Primary Runtime Narratives`, `Most Important Cross-Cutting Paths`
   - `MODULES.md`: `Public API And Boundary`, `Internal Ownership`, `Upstream Downstream Dependencies`, `Change Impact Notes`
   - `STRUCTURE_DIAGRAMS.md`: `Startup To Runtime Flow`, `Build Ownership Flow`, `Security Boundary Flow`, `Unreal Reflection And Replication Flow`
   - `CODE_STRUCTURE_REFERENCE.md`: `Symbol Clusters`, `Caller Callee Hotspots`, `Build Context To Source Mapping`, `Verification Anchor Map`

5. Phase 5 평가/회귀 체계
   - `analysis_qa_context_test.go`를 추가해 intent 분류, developer docs boost, answer pack context injection, manifest/vector metadata, deep QA 문서 섹션, security golden contract, stale/current-source marker 전파를 검증한다.
   - `agent_verify_loop_test.go`에 deep structure fast-path 테스트를 추가해 answer pack, source anchors, graph views, structural index v2 hits, stricter fast-path instruction이 실제 agent request에 들어가는지 검증한다.
   - 전체 회귀 검증은 `go test -count=1 ./...`로 통과했다.

리뷰 중 수정한 버그:

1. `실행 흐름` 같은 한국어 구조 질문이 `실행` 키워드 때문에 edit/run intent로 오분류되어 deep QA fast-path가 꺼지는 문제를 수정했다.
2. docs manifest와 vector corpus에서 같은 문서 hit가 중복될 때 stale marker가 높은 점수의 vector hit에 밀려 사라질 수 있는 문제를 metadata 병합으로 수정했다.
3. answer pack에서 answer contract, graph/security/verification 단서가 긴 docs excerpt 뒤로 밀려 잘리는 문제를 렌더링 순서 조정으로 수정했다.
4. `/analyze-project` 최종 보고서가 provider review timeout과 reviewer quality rejection을 모두 `Analysis With Review Failures`로 뭉뚱그려 보여주던 문제를 수정했다.
5. driver domain flow가 anchor를 단순 나열해 모델이 직접 call chain으로 오해할 수 있던 문제를 수정했다.
6. `new_primary_scope`를 real stale marker처럼 렌더링할 수 있던 문제를 수정했다.
7. source anchor를 다른 lifecycle symbol로 재라벨링할 수 있던 문제를 exact-symbol labeling rule과 teardown/process-monitor/file-filter anchor 보강으로 수정했다.
8. unlabeled source anchor 목록 때문에 file:line이 다른 심볼에 붙던 문제를 verified critical source anchor map과 flow label line embedding으로 보강했다.
9. cached answer evaluator가 Windows식 `\` 경로로 적힌 file:line anchor를 놓칠 수 있던 문제를 수정했다. 이제 `/`와 `\`를 동일하게 정규화해 exact symbol 누락과 accessor/lifecycle 오라벨링을 잡는다.
10. 작은 프로젝트에서 source anchor가 5개 미만이라는 이유만으로 `current_source_needed`가 켜져 cached answer fast-path가 불필요하게 도구 루프로 떨어질 수 있던 문제를 수정했다. deterministic architecture fact pack, critical anchors, docs/folder/graph context가 있으면 작은 프로젝트도 캐시 답변을 허용한다.
11. `codex-cli` provider가 API key를 쓰지 않는데 이전 provider의 `cfg.APIKey` 또는 `ProviderKeys["codex-cli"]`가 role/profile에 섞일 수 있던 설정 오염 문제를 수정했다.
12. `/analyze-project`의 `latest/` mirror를 매 실행마다 비우지 않아 새 분석에 없는 `unreal_graph.json`, `vector_corpus.json` 같은 이전 산출물이 남을 수 있던 문제를 수정했다. 이제 `latest/`는 현재 실행 산출물만 담는다.
13. worker가 `{}` 또는 `{"report":{}}` 같은 빈 JSON을 반환했을 때 normalize 단계에서 shard 이름과 primary files가 채워져 유효 리포트처럼 통과할 수 있던 문제를 수정했다. 내용 없는 JSON은 repair/fallback 경로로 보낸다.

남은 것:

1. 필수 구현 항목은 현재 없다.
2. 실제 SampleKernelDriver 같은 대형 driver 프로젝트에서 최신 바이너리로 `/analyze-project --mode map`을 다시 실행한 뒤, "프로젝트 전체 구조", "초기화 흐름", "IOCTL dispatch/validation", "object filter enforcement" 질문을 반복해 실제 모델 출력 품질을 확인해야 한다.
3. 운영 품질 튜닝은 남아 있다. 실제 Unreal/driver 프로젝트에서 intent별 top-N, token budget, graph view 우선순위를 계측해 조정할 수 있다. 다만 현재는 critical anchor와 domain flow가 먼저 렌더링되므로, 기존보다 안전한 기본값을 갖는다.
4. 모델 출력 자체를 채점하는 end-to-end answer quality eval은 아직 규칙 기반 contract test 수준이다. 추후 실제 답변 텍스트를 대상으로 source anchor 수, stale caveat 포함 여부, verification coverage, domain terminology correctness를 자동 채점할 수 있다.
5. dashboard에서 새 `QueryIntents`, `EntityRefs`, `GraphRefs`, `Domain Critical Anchors`를 더 적극적으로 보여주는 UI 개선은 후속 작업으로 남길 수 있다.

## 1. 현재 구현 요약

현재 Kernforge는 project analysis 측면에서 이미 다음 기반을 갖고 있다.

1. `ProjectAnalysisRun`은 `Snapshot`, `KnowledgePack`, `SemanticIndexV2`, `UnrealGraph`, `VectorCorpus`를 포함한다.
2. `persistRun` 계열은 run JSON, snapshot, structural index, docs, dashboard, latest mirror를 남긴다.
3. 생성 문서는 `ARCHITECTURE.md`, `SECURITY_SURFACE.md`, `API_AND_ENTRYPOINTS.md`, `BUILD_AND_ARTIFACTS.md`, `VERIFICATION_MATRIX.md`, `FUZZ_TARGETS.md`, `OPERATIONS_RUNBOOK.md`, `DEVELOPER_OVERVIEW.md`, `FOLDER_MAP.md`, `MODULES.md`, `STRUCTURE_DIAGRAMS.md`, `CODE_STRUCTURE_REFERENCE.md`, `INDEX.md`까지 확장되어 있다.
4. `analysis_docs_vector.go`는 생성 문서 전체와 `##` 섹션을 vector corpus document로 자동 분해한다.
5. `analysis_docs_reuse.go`는 docs manifest를 evidence와 persistent memory로 승격한다.
6. `analysis_context.go`는 최신 project analysis artifacts를 읽어 일반 사용자 질문에 `Relevant project analysis from past analyze-project runs`를 주입한다.
7. `analysis_context_v2.go`, `analysis_context_v2_graph.go`는 질문을 `map`, `trace`, `impact`, `security`, `performance` 쪽으로 분류하고 structural index v2 graph neighborhood를 확장한다.
8. `agent.go`의 system prompt는 cached architecture summary를 우선 사용하되, 편집이나 고위험 claim 전에는 도구로 검증하라고 지시한다.

즉, "분석 자산을 만드는 쪽"은 상당히 올라와 있다.

## 2. 현재 병목

### 2.1 런타임 컨텍스트 주입량이 얇다

`renderRelevantProjectAnalysisContext`는 기본적으로 다음 정도만 주입한다.

1. subsystem 최대 3개
2. vector document 최대 2개
3. structural index file 최대 3개
4. semantic symbol 최대 4개
5. docs manifest 문서 최대 4개
6. structural index v2 hit 일부

이 방식은 "어느 파일부터 봐야 하지?" 수준에는 충분하지만, 사용자가 "이 프로젝트의 깊은 실행 구조를 설명해줘", "이 모듈이 전체 구조에서 어떤 위치야?", "초기화에서 telemetry upload까지 흐름을 자세히 설명해줘"라고 물으면 재료가 부족하다.

특히 전체 구조 질문은 query token이 넓고 추상적이라 `selectRelevant...` 계열 점수화가 특정 subsystem/doc으로 과하게 수렴하거나, 반대로 중요 graph를 충분히 펼치지 못할 수 있다.

### 2.2 cached fast-path가 충분한 답변과 얕은 답변을 구분하지 못한다

`maybeAnswerFromCachedProjectAnalysis`는 도구 없이 cached context만으로 답할 수 있는지 모델에 물어본다.

장점:

1. 빠르다.
2. 이미 분석해 둔 내용을 바로 재사용한다.
3. 불필요한 대형 파일 재탐색을 줄인다.

한계:

1. fast-path에 들어가는 context 자체가 짧으면 답변도 짧아진다.
2. "깊은 구조 질문"과 "간단한 구조 요약 질문"의 기대 답변 밀도를 구분하지 않는다.
3. answer completeness를 측정하는 구조화 기준이 없다.
4. source anchor, graph edge, docs section을 충분히 인용하지 않아도 통과할 수 있다.

### 2.3 generated docs는 풍부하지만 질의별 section retrieval이 약하다

문서와 섹션은 vector corpus에 들어가지만, 런타임 context는 `selectRelevantVectorDocuments(..., 2)` 중심이다.

필요한 동작:

1. 문서 단위보다 섹션 단위 검색을 우선한다.
2. 깊은 구조 질문에서는 `DEVELOPER_OVERVIEW.md`, `MODULES.md`, `STRUCTURE_DIAGRAMS.md`, `CODE_STRUCTURE_REFERENCE.md`, `ARCHITECTURE.md`를 더 넓게 가져온다.
3. `SourceAnchors`, `Sections`, `StaleMarkers`, `ReuseTargets`를 답변 구성에 반영한다.

현재는 manifest가 "어떤 문서가 있다"는 사실을 알려주는 정도이고, 해당 문서의 핵심 section text를 충분히 재주입하지 않는다.

### 2.4 graph retrieval은 있으나 답변용 구조 view가 부족하다

`SemanticIndexV2`와 graph expansion은 구현되어 있지만, 사용자 답변에 바로 쓰기 좋은 중간 표현이 약하다.

부족한 view:

1. module -> entrypoint -> core symbols -> downstream dependencies
2. startup chain -> runtime handoff -> background worker/job -> external surface
3. folder -> module -> build context -> generated artifact
4. security surface -> trust boundary -> validation symbol -> verification target
5. Unreal project -> target -> module -> reflected type -> RPC/replication/asset/config edge
6. changed file/symbol -> reverse reference -> retest target

현재 구조는 raw hits와 limited path를 주입하는 쪽에 가깝다. 깊은 구조 답변에는 "사용자 질문에 맞춘 structure answer pack"이 필요하다.

### 2.5 baseline map 재사용은 analysis mode에는 좋지만 일반 QA에는 덜 직접적이다

후속 `/analyze-project --mode trace|impact|security|performance`에서는 이전 `map` 실행을 baseline architecture map으로 불러온다.

하지만 일반 대화에서 "이미 분석해둔 기준으로 구조 설명해줘"라고 물었을 때는 baseline map을 별도 QA pack으로 재구성하지 않는다.

결과적으로 후속 analysis command 품질과 일반 chat QA 품질 사이에 차이가 생긴다.

### 2.6 전체 구조 질문용 평가 테스트가 부족하다

현재 테스트는 cached context 주입, fast-path, reinjection, docs 생성, graph metadata 위주다.

부족한 테스트:

1. "전체 구조를 자세히 설명해줘" 질문에서 developer docs가 우선 주입되는지
2. "A에서 B까지 실행 흐름" 질문에서 call path, source anchor, build context가 함께 주입되는지
3. "이 모듈 바꾸면 영향?" 질문에서 reverse refs, docs stale marker, verification matrix가 함께 주입되는지
4. "Codex/Claude보다 자세한 답변"을 위한 최소 answer contract를 만족하는지

## 3. 추천 아키텍처

추천 방향은 다음이다.

`Analysis Artifacts -> Query Intent -> Structure Answer Pack -> Optional Tool Verification -> Grounded Final Answer`

### 3.1 Query Intent

일반 질문을 다음 intent로 분류한다.

1. `deep_map`: 전체 구조, 아키텍처, 폴더/모듈 설명
2. `flow_trace`: 실행 흐름, startup, call chain, request path
3. `module_drilldown`: 특정 모듈/폴더/파일의 위치와 책임
4. `impact`: 변경 영향, 깨질 수 있는 지점, retest 범위
5. `security_surface`: trust boundary, IOCTL/RPC/parser/handle/memory/telemetry surface
6. `unreal_structure`: UE target/module/reflection/RPC/replication/asset/config 구조
7. `build_artifact`: build context, generated artifact, packaging/signing/deployment 흐름
8. `verification`: 어떤 테스트/검증/evidence를 봐야 하는지

현재 `classifyProjectAnalysisQueryMode`를 확장하거나, 일반 QA 전용 `classifyProjectAnalysisQAIntent`를 별도로 둔다.

### 3.2 Structure Answer Pack

fast-path와 일반 tool loop 전에 다음 구조체를 만든다.

```text
ProjectStructureAnswerPack
  Intent
  Confidence
  Summary
  RelevantDocs
  RelevantSections
  Modules
  Folders
  Files
  Symbols
  CallPaths
  BuildContexts
  UnrealEdges
  SecurityOverlays
  VerificationEntries
  FuzzTargets
  StaleMarkers
  SourceAnchors
  SuggestedReads
```

핵심은 "문서 조각", "그래프 조각", "검증 조각"을 한 번에 묶는 것이다.

### 3.3 Retrieval policy

intent별 기본 retrieval 폭을 다르게 둔다.

| Intent | 문서 | 그래프 | 앵커 | 검증/퍼징 |
| --- | --- | --- | --- | --- |
| `deep_map` | developer docs 5종 + architecture | module/folder/build graph top N | top files/symbols | optional |
| `flow_trace` | architecture + API/entrypoints + diagrams | call path depth 2-3 | call edge evidence | verification matrix |
| `module_drilldown` | modules + folder map + code reference | module neighborhood | public/internal files | related tests |
| `impact` | code reference + build + verification | reverse refs/dependents | changed area anchors | required checks |
| `security_surface` | security + API + fuzz + verification | overlay/trust boundary | validation/copy/probe anchors | fuzz targets/checks |
| `unreal_structure` | modules + architecture + security | Unreal graph edges | Build.cs/Target.cs/UCLASS/RPC anchors | replication/security checks |
| `build_artifact` | build docs + modules | build ownership edges | manifest/build files | build verification |

### 3.4 Answer contract

깊은 구조 질문에 대한 답변은 최소한 다음 섹션을 포함하게 한다.

1. 한 줄 결론
2. 주요 구조 계층
3. 실행/의존 흐름
4. 관련 파일/심볼 anchor
5. 변경 시 영향과 검증 포인트
6. 불확실하거나 stale한 부분
7. 다음에 읽을 문서/파일

Korean prompt에서는 이 형식을 한국어로 유지하되, code identifier와 path는 원문을 유지한다.

## 4. 구현 개선 항목

### 4.1 QA intent classifier 추가

파일:

1. `analysis_context.go`
2. `analysis_context_v2.go`
3. 신규 후보: `analysis_qa_context.go`

구현:

1. `classifyProjectAnalysisQAIntent(query string) ProjectAnalysisQAIntent` 추가
2. 한국어 키워드 포함
3. 기존 `classifyProjectAnalysisQueryMode`와 호환되게 mode를 매핑
4. 질문이 "자세히", "깊게", "전체 구조", "구조와 흐름", "왜 이렇게 나뉘었는지"를 포함하면 `deep_map`으로 승격

완료 기준:

1. `이 프로젝트 구조를 자세히 설명해줘` -> `deep_map`
2. `startup에서 telemetry upload까지 흐름` -> `flow_trace`
3. `이 모듈 바꾸면 어디 영향?` -> `impact`
4. `RPC validation surface 설명` -> `security_surface`
5. `Unreal replication 구조` -> `unreal_structure`

### 4.2 Structure Answer Pack builder 추가

파일:

1. 신규 후보: `analysis_qa_pack.go`
2. `analysis_context.go`
3. `analysis_context_v2_graph.go`

구현:

1. latest artifacts에서 answer pack을 만든다.
2. docs manifest, vector corpus, structural index v2, Unreal graph, verification matrix, fuzz targets를 함께 읽는다.
3. intent별 limit을 조절한다.
4. pack confidence를 source coverage로 산정한다.

추천 limit:

1. `deep_map`: docs sections 8-12, modules 8, folders 8, graph edges 20, anchors 20
2. `flow_trace`: paths 3, call edges 20, symbols 12, anchors 20
3. `impact`: references 20, occurrences 20, build edges 12, verification entries 8
4. `security_surface`: overlay edges 20, fuzz targets 8, verification entries 8

완료 기준:

1. 일반 cached context보다 더 큰 구조 재료를 만들 수 있다.
2. pack text는 token budget에 따라 compact 가능하다.
3. source anchor가 없는 claim은 confidence가 낮아진다.

### 4.3 Generated docs section retrieval 강화

파일:

1. `analysis_docs_vector.go`
2. `analysis_context.go`
3. 신규 후보: `analysis_docs_query.go`

구현:

1. `selectRelevantVectorDocuments`를 문서 단위와 section 단위로 분리한다.
2. `AnalysisDocSection.ID`, `Title`, `SourceAnchors`, `StaleMarkers`, `ReuseTargets`를 scoring에 반영한다.
3. `deep_map`에서는 developer docs를 boost한다.
4. `security_surface`에서는 `SECURITY_SURFACE.md`, `API_AND_ENTRYPOINTS.md`, `FUZZ_TARGETS.md`, `VERIFICATION_MATRIX.md`를 boost한다.
5. `impact`에서는 `CODE_STRUCTURE_REFERENCE.md`, `BUILD_AND_ARTIFACTS.md`, `VERIFICATION_MATRIX.md`를 boost한다.

완료 기준:

1. 질문이 넓어도 `DEVELOPER_OVERVIEW.md`, `MODULES.md`, `STRUCTURE_DIAGRAMS.md`, `CODE_STRUCTURE_REFERENCE.md` 섹션이 빠지지 않는다.
2. section title과 source anchor가 답변 pack에 들어간다.

### 4.4 Graph answer view 추가

파일:

1. `analysis_context_v2_graph.go`
2. `analysis_graph.go`
3. 신규 후보: `analysis_qa_graph_views.go`

구현:

1. `BuildModuleFlowView`
2. `RuntimeFlowView`
3. `SecuritySurfaceView`
4. `UnrealSemanticFlowView`
5. `ImpactBlastRadiusView`

각 view는 다음 필드를 가진다.

```text
Title
Nodes
Edges
SourceAnchors
Evidence
Confidence
RecommendedDocs
VerificationHints
```

완료 기준:

1. 답변에 raw edge 나열이 아니라 "무슨 흐름인지"가 들어간다.
2. Mermaid docs와 런타임 QA가 같은 graph projection 로직을 공유한다.

### 4.5 Fast-path gating 강화

파일:

1. `analysis_context.go`
2. `agent_verify_loop_test.go`

구현:

1. deep QA intent에서는 기존 짧은 fast-path context를 바로 쓰지 않는다.
2. 먼저 Structure Answer Pack을 만든 뒤 fast-path에 넣는다.
3. pack confidence가 낮거나 stale marker가 많으면 `NEEDS_TOOLS`를 유도한다.
4. fast-path instruction에 answer contract를 추가한다.

추천 규칙:

1. `deep_map`, `flow_trace`, `impact`, `security_surface`는 pack 없이는 fast-path 금지
2. source anchor 5개 미만이면 "간단 답변은 가능하지만 자세한 답변은 tools 필요"로 판단
3. stale marker가 관련 docs에 있으면 답변에 stale caveat를 넣거나 tools로 현재 파일 확인

완료 기준:

1. 깊은 구조 질문에서 5줄 요약으로 끝나지 않는다.
2. 근거가 충분하면 tool 없이도 자세히 답한다.
3. 근거가 부족하면 자연스럽게 current source verification으로 넘어간다.

### 4.6 일반 QA에서 latest docs 파일 직접 read 경로 추가

현재 context injection은 manifest와 vector text에 기대는 비중이 크다.

개선:

1. answer pack에 `SuggestedDocReads`를 넣는다.
2. tool loop로 넘어갈 때는 raw source보다 `.kernforge/analysis/latest/docs/*.md`를 먼저 읽도록 guidance를 넣는다.
3. 사용자가 "analysis-project 해둔 기준으로"라고 말하면 source file보다 generated docs를 먼저 읽는다.

완료 기준:

1. 일반 workspace 탐색보다 analysis docs 기반 답변이 우선된다.
2. source verification은 필요한 anchor만 좁게 읽는다.

### 4.7 Documentation manifest를 QA 친화적으로 확장

파일:

1. `analysis_docs.go`
2. `analysis_developer_docs.go`

추가 필드 후보:

```text
AnalysisGeneratedDoc
  QueryIntents []string
  Priority int

AnalysisDocSection
  QueryIntents []string
  Priority int
  EntityRefs []string
  GraphRefs []string
```

효과:

1. section retrieval이 단순 텍스트 매칭보다 안정적이다.
2. 질문 intent에 맞는 문서를 deterministic하게 고를 수 있다.
3. dashboard와 QA retrieval이 같은 metadata를 쓴다.

호환성:

1. manifest policy가 additive-fields-only이므로 새 필드는 안전하게 추가 가능하다.
2. reader는 unknown field ignore 정책을 유지한다.

### 4.8 Developer docs 내용 보강

현재 developer docs는 좋은 시작점이다. 깊은 QA용으로 다음 섹션을 추가하면 효과가 크다.

`DEVELOPER_OVERVIEW.md`:

1. `Architecture Layers`
2. `Primary Runtime Narratives`
3. `Most Important Cross-Cutting Paths`

`MODULES.md`:

1. `Public API And Boundary`
2. `Internal Ownership`
3. `Upstream/Downstream Dependencies`
4. `Change Impact Notes`

`STRUCTURE_DIAGRAMS.md`:

1. `Startup To Runtime Flow`
2. `Security Boundary Flow`
3. `Build Ownership Flow`
4. `Unreal Reflection And Replication Flow`

`CODE_STRUCTURE_REFERENCE.md`:

1. `Symbol Clusters`
2. `Caller/Callee Hotspots`
3. `Build Context To Source Mapping`
4. `Verification Anchor Map`

완료 기준:

1. 각 문서 section이 answer pack에 그대로 재사용 가능하다.
2. 문서가 사람용이면서 동시에 QA retrieval용 index 역할을 한다.

## 5. 테스트 계획

### 5.1 Unit tests

추가 테스트 파일 후보:

1. `analysis_qa_context_test.go`
2. `analysis_docs_query_test.go`
3. `analysis_qa_pack_test.go`

테스트:

1. QA intent classification
2. developer docs boost
3. section-level vector retrieval
4. graph answer view 생성
5. stale marker propagation
6. source anchor coverage scoring

### 5.2 Agent loop tests

추가/확장:

1. `agent_verify_loop_test.go`
2. `analysis_context_test.go`

테스트:

1. deep structure query는 Structure Answer Pack을 포함한다.
2. fast-path 답변에는 internal marker가 노출되지 않는다.
3. shallow query는 기존처럼 작은 context로도 답할 수 있다.
4. stale docs가 있으면 fast-path가 caveat를 포함하거나 tool loop로 fallback한다.
5. materially changed query는 answer pack을 재주입한다.

### 5.3 Golden answer contract test

fixture 프로젝트를 만든다.

구성:

1. Go 단일 패키지
2. C++/driver-like IOCTL dispatch
3. Unreal-like `.uproject`, `.Build.cs`, UCLASS/RPC 샘플
4. build artifact 샘플

질문:

1. `이 프로젝트 전체 구조를 자세히 설명해줘`
2. `startup에서 request dispatch까지 흐름을 설명해줘`
3. `IOCTL handler를 바꾸면 어디까지 영향이 가?`
4. `Unreal replication 구조와 보안 경계를 설명해줘`

검증:

1. 답변에 최소 5개 source anchor 포함
2. module/folder/build context 중 2개 이상 포함
3. flow 질문은 call path 또는 runtime edge 포함
4. impact 질문은 verification matrix 또는 stale marker 포함
5. security 질문은 trust boundary, validation, fuzz/verify target 중 2개 이상 포함

## 6. 우선순위

### Phase 1: 런타임 QA retrieval 강화

1. QA intent classifier 추가
2. Structure Answer Pack 추가
3. deep QA에서 pack 기반 fast-path 사용
4. developer docs section retrieval boost

가장 먼저 해야 한다. 이미 산출물은 있으므로, 사용자 체감 품질이 가장 빠르게 오른다.

### Phase 2: graph answer view 추가

1. module/folder/build/runtime/security/unreal view 생성
2. 기존 docs graph projection과 공유
3. 답변용 compact renderer 추가

이 단계가 들어가면 단순 문서 요약이 아니라 "구조를 이해한 답변"이 된다.

### Phase 3: docs manifest metadata 확장

1. `QueryIntents`
2. `EntityRefs`
3. `GraphRefs`
4. `Priority`

이 단계는 retrieval 안정성을 높인다.

### Phase 4: developer docs 보강

1. runtime narratives
2. boundary/API sections
3. impact notes
4. verification anchor map

이 단계는 answer pack의 원재료 품질을 올린다.

### Phase 5: 평가/회귀 체계

1. golden fixture
2. answer contract tests
3. stale/current source fallback tests
4. dashboard/doc QA integration tests

## 7. Codex/Claude Code 대비 차별화 포인트

Codex와 Claude Code는 일반적으로 현재 workspace를 도구로 잘 탐색한다. Kernforge가 더 자세히 답하려면 다음을 전면에 세워야 한다.

1. 이미 실행된 `/analyze-project`의 deterministic 산출물을 우선 사용한다.
2. 문서, structural index, Unreal graph, verification matrix, fuzz targets, evidence를 한 답변에 결합한다.
3. "파일을 읽고 요약"이 아니라 "분석된 구조 지식 베이스에서 intent별 answer pack을 생성"한다.
4. 보안/anti-cheat 관점의 trust boundary, validation, telemetry, driver/IOCTL, memory, Unreal replication surface를 기본 lens로 제공한다.
5. source anchor와 stale marker로 답변 신뢰도를 설명한다.

추천 포지셔닝:

`Codex/Claude Code는 지금 코드를 잘 읽는 범용 agent이고, Kernforge는 한 번 분석해 둔 대형 보안/게임 코드베이스를 구조 지식 베이스로 바꿔서 계속 재사용하는 agent다.`

## 8. 구현 시작점

가장 현실적인 첫 PR 단위:

1. `analysis_qa_context.go` 추가
2. `ProjectAnalysisQAIntent`, `ProjectStructureAnswerPack` 타입 추가
3. latest artifacts에서 developer docs section과 structural index v2 hits를 intent별로 모으는 builder 추가
4. `renderRelevantProjectAnalysisContext`가 deep QA intent일 때 answer pack renderer를 사용
5. `maybeAnswerFromCachedProjectAnalysis` fast-path instruction에 deep answer contract 추가
6. 테스트 4개 추가

권장 첫 테스트:

1. `TestClassifyProjectAnalysisQAIntentDeepMapKorean`
2. `TestBuildProjectStructureAnswerPackBoostsDeveloperDocs`
3. `TestRenderRelevantProjectAnalysisContextUsesAnswerPackForDeepStructureQuery`
4. `TestAgentDeepStructureFastPathReceivesSourceAnchorsAndDocs`

이 묶음만 들어가도 `/analyze-project` 이후의 일반 구조 질문 답변 밀도가 눈에 띄게 올라간다.

## 9. 최종 권고

지금 필요한 것은 새로운 대형 분석 엔진을 바로 추가하는 것이 아니다.

우선순위는 다음이다.

1. 이미 생성된 docs, vector corpus, structural index v2, Unreal graph를 일반 QA에서 더 강하게 재사용한다.
2. 깊은 구조 질문을 별도 intent로 인식한다.
3. 답변 전용 Structure Answer Pack을 만든다.
4. fast-path가 짧은 요약으로 끝나지 않도록 answer contract와 confidence gate를 둔다.
5. developer docs와 graph view를 QA retrieval 친화적으로 보강한다.

한 줄 결론:

`analysis-project를 더 많이 실행하게 만드는 것보다, 한 번 실행한 결과를 질문 시점에 구조적으로 더 잘 꺼내는 것이 Codex/Claude Code 대비 체감 차이를 가장 빠르게 만든다.`
