# 차세대 Project Analysis 아키텍처 제안

기준 시점:
- 코드베이스 기준: 2026-04-18

대상:
- UE5급 대규모 게임 프로젝트
- Windows security / anti-cheat / telemetry / integrity 코드베이스
- 대형 C++/C#/Go 혼합 워크스페이스

## 1. 배경

현재 Kernforge의 project analysis는 이미 다음 강점을 갖고 있다.

1. 워크스페이스를 빠르게 스캔해 snapshot을 만든다.
2. goal 기반 lens를 선택한다.
3. 중요 파일을 점수화하고 shard로 분할한다.
4. worker/reviewer 패스로 구조 지식을 생성한다.
5. knowledge pack과 performance lens를 재사용 가능한 산출물로 남긴다.
6. 변경되지 않은 shard를 incremental하게 재사용할 수 있다.

현재 구현의 핵심 지점:

1. `scanProject`
2. `chooseAnalysisLenses`
3. `refineAnalysisLensesForSnapshot`
4. `scoreFileImportance`
5. `planShards`
6. `executeShards`
7. `buildKnowledgePack`
8. `persistRun`

이 구조는 "큰 프로젝트를 한 번에 요약하지 않고 분산 분석한다"는 점에서 이미 방향이 맞다.
하지만 UE5급 대규모 코드베이스에서는 다음 문제가 발생한다.

1. 파일 단위 shard만으로는 symbol 관계가 너무 쉽게 끊긴다.
2. RAG나 텍스트 청크 중심 접근만으로는 call path, ownership, authority boundary를 안정적으로 복원하기 어렵다.
3. Unreal reflection, generated header, UBT/UHT, asset/config coupling 같은 도메인 특성이 일반 코드 검색만으로는 잘 안 잡힌다.
4. 파일 fingerprint 기반 incremental reuse는 거칠다.
5. fallback 문서와 휴리스틱 graph는 설명에는 좋지만 정밀 영향 분석에는 한계가 있다.

결론적으로 다음 단계는 "더 큰 컨텍스트를 LLM에 넣는 것"이 아니라 "정밀 인덱스를 먼저 만들고 LLM은 그 위에서 합성하게 하는 것"이어야 한다.

## 1.1 구현 상태 업데이트

현재 코드 기준으로 이미 들어간 항목은 다음과 같다.

1. `structural_index_v2`는 build context, symbol anchor, build ownership edge, call edge, overlay edge까지 포함하는 방향으로 확장되었다.
2. build alignment는 `.uproject`, `.uplugin`, `.Build.cs`, `.Target.cs`, `compile_commands.json`를 함께 읽어 module/target/file 관계를 build context로 정리한다.
3. source anchor 계층은 Go/C++/C# 함수 정의를 line range와 함께 올리고, function-level call edge와 security overlay를 추가한다.
4. `trace`, `impact`, `security` retrieval은 keyword hit만 고르는 방식에서 graph neighborhood/path expansion 기반으로 강화되어 `build_context_v2`, `path_v2` 근거를 남긴다.
5. incremental reuse는 file hash만이 아니라 symbol neighborhood를 반영한 semantic fingerprint 경로를 사용한다.
6. C++ parser는 template out-of-line method, operator, `requires`, `decltype(auto)`, API macro가 낀 scope, friend function 같은 modern C++ 패턴을 처리한다.

아직 남아 있는 대표 항목은 다음과 같다.

1. 독립적인 `surface` 모드 정식 노출
2. build condition과 generated code를 더 정밀하게 반영하는 parser/ingestion 계층
3. symbol-level invalidation의 세분화와 UE generated artifact 연계 강화

## 2. 기존 방법론 검토

### 2.1 Plain RAG

장점:

1. 구현이 빠르다.
2. 자연어 질의 대응이 쉽다.
3. knowledge pack과 쉽게 결합된다.

한계:

1. 대형 C++/UE 코드베이스에서는 청크 경계 때문에 symbol 관계가 깨진다.
2. 정의/참조/상속/호출 관계를 정확하게 복원하지 못한다.
3. 자주 보이는 용어가 중요해 보이는 착시가 발생한다.

판단:

- 보조 retrieval 계층으로는 유효하다.
- 분석의 주 엔진으로는 부족하다.

### 2.2 GraphRAG

장점:

1. 큰 코드베이스의 전체 구조를 community 단위로 요약하기 좋다.
2. 질의 시 지역 정보와 전역 요약을 함께 줄 수 있다.
3. 재사용 가능한 계층형 요약을 만들기 좋다.

한계:

1. 코드에서 그래프를 뽑는 단계가 부정확하면 전체 품질이 급격히 흔들린다.
2. 자연어 엔티티 추출 기반 GraphRAG는 코드 의미 해석에 약할 수 있다.

판단:

- 코드 전용 graph가 먼저 있고, GraphRAG는 그 위의 질의/요약 계층으로 쓰는 것이 적합하다.
- 원시 코드 텍스트에서 직접 GraphRAG를 만드는 방식은 Kernforge의 핵심 축으로 삼기 어렵다.

### 2.3 CodeGraph / SCIP / LSIF / Precise code navigation

장점:

1. 정의/참조 관계가 안정적이다.
2. 대형 코드베이스에서 symbol 중심 탐색이 가능하다.
3. 영향 분석, caller/callee 추적, cross-file navigation에 강하다.

한계:

1. 언어 및 빌드 환경 정합성이 중요하다.
2. UE는 compile flags, generated headers, UBT/UHT 처리까지 맞춰야 품질이 나온다.

판단:

- UE5급 지원의 핵심 축이다.
- Kernforge의 다음 대형 투자 지점으로 가장 적합하다.

### 2.4 Code Property Graph

장점:

1. AST, CFG, PDG, call graph를 함께 다룰 수 있다.
2. 보안 분석, taint-like reasoning, 공격 표면 추적에 강하다.

한계:

1. 전체 코드베이스에 항상 적용하기엔 무겁다.
2. 구현 복잡도가 높다.

판단:

- 전면 채택보다는 고위험 경로에 선택 적용하는 overlay가 적절하다.

### 2.5 Tree-sitter / 경량 AST

장점:

1. 빠르다.
2. 증분 파싱이 좋다.
3. shard planning과 구조적 chunking에 적합하다.

한계:

1. 타입 해석과 정확한 symbol binding에는 한계가 있다.
2. UE macro와 generated code 의미를 충분히 복원하지 못한다.

판단:

- 1차 구조 인식 계층으로 매우 유용하다.
- 정밀 semantic index의 대체재는 아니다.

## 3. 추천 방향

추천 방향은 다음 한 줄로 요약된다.

`Precise symbol index + Unreal semantic graph + selective retrieval + LLM synthesis`

즉:

1. 기계적으로 안정적인 구조 인덱스를 먼저 만든다.
2. UE/anti-cheat 도메인 의미를 graph overlay로 추가한다.
3. retrieval는 질문 타입에 따라 선택적으로 사용한다.
4. LLM은 원시 코드 탐색 엔진이 아니라 인덱스 위의 해석기와 합성기로 쓴다.

## 4. 목표 아키텍처

차세대 분석 파이프라인은 5계층으로 나눈다.

### 4.1 Layer 1: Snapshot

목적:

1. 빠른 파일 스캔
2. manifest/entrypoint 탐지
3. 기본 import/include/reference 힌트 수집
4. 대략적 중요도 점수화

역할:

- 현재 `ProjectSnapshot` 계층을 유지 및 확장한다.

추가 방향:

1. 파일 단위 외에 symbol block 후보를 기록한다.
2. include graph, build graph, generated file mapping 힌트를 함께 가진다.
3. Unreal 관련 discovery 결과를 더 구조화한다.

### 4.2 Layer 2: Structural Index

목적:

1. symbol 단위 정의/참조/상속/호출 관계 저장
2. 언어별 정밀 navigation 지원
3. symbol fingerprint 기반 incremental invalidation

핵심 원칙:

1. LLM 없이도 기계적으로 재생산 가능해야 한다.
2. knowledge pack과 분리된 별도 산출물이어야 한다.
3. 질의와 shard planning 모두 이 계층을 활용해야 한다.

추천 구성:

1. `FileRecord`
2. `SymbolRecord`
3. `ReferenceRecord`
4. `CallEdge`
5. `InheritanceEdge`
6. `ContainmentEdge`
7. `BuildOwnershipEdge`
8. `GeneratedCodeEdge`

### 4.3 Layer 3: Unreal Semantic Graph

목적:

1. UE 프로젝트의 도메인 구조를 일반 symbol graph 위에 올린다.
2. gameplay framework, replication, UI, GAS, asset/config coupling을 설명 가능한 그래프로 만든다.

핵심 노드:

1. `uproject`
2. `plugin`
3. `target`
4. `module`
5. `uclass`
6. `ufunction`
7. `uproperty`
8. `subsystem`
9. `game_instance`
10. `game_mode`
11. `player_controller`
12. `pawn`
13. `character`
14. `widget`
15. `ability`
16. `effect`
17. `attribute_set`
18. `asset`
19. `config_key`

핵심 엣지:

1. `declares`
2. `depends_on`
3. `loads`
4. `spawns`
5. `owns`
6. `binds_input`
7. `creates_widget`
8. `references_asset`
9. `configured_by`
10. `replicates`
11. `rpc_server`
12. `rpc_client`
13. `rpc_multicast`
14. `authority_transition`
15. `registered_in`

### 4.4 Layer 4: Retrieval And Trace Engine

목적:

1. 질문 타입에 따라 retrieval 전략을 바꾼다.
2. 전체 구조 질의와 정밀 trace 질의를 분리한다.

질의 모드:

1. `map`
2. `trace`
3. `impact`
4. `surface`
5. `performance`
6. `security`

권장 동작:

1. `map`: community summary + top modules + startup chain
2. `trace`: entry symbol에서 graph walk 후 증거 코드 조각만 retrieval
3. `impact`: changed symbol 기준 reverse refs/callers/dependents
4. `surface`: trust boundary, RPC, config, asset load 경로 위주
5. `performance`: hot path 후보와 fan-out/fan-in/high-frequency edge 위주

### 4.5 Layer 5: LLM Synthesis

목적:

1. worker/reviewer가 정밀 인덱스 위에서 문맥을 해석한다.
2. 문서, knowledge pack, performance lens를 생성한다.

중요 원칙:

1. worker 입력을 원시 파일 리스트 중심에서 구조화 컨텍스트 중심으로 바꾼다.
2. "직접 모든 걸 찾아라"보다 "준비된 index evidence를 해석하라"로 전환한다.

## 5. 산출물 구조 재편

현재 `.kernforge/analysis/latest`는 knowledge pack 중심이다.
차세대 구조에서는 다음처럼 분리하는 것이 좋다.

```text
.kernforge/analysis/
  <run-id>_<goal>.md
  <run-id>_<goal>.json
  <run-id>_<goal>_knowledge.md
  <run-id>_<goal>_knowledge.json
  <run-id>_<goal>_performance_lens.md
  <run-id>_<goal>_performance_lens.json
  <run-id>_<goal>_snapshot.json
  <run-id>_<goal>_structural_index.json
  <run-id>_<goal>_unreal_graph.json
  latest/
    snapshot.json
    structural_index.json
    unreal_graph.json
    knowledge_pack.json
    performance_lens.json
```

핵심 원칙:

1. `snapshot`은 빠르게 다시 만들 수 있는 기계 산출물
2. `structural_index`는 정밀 navigation 산출물
3. `unreal_graph`는 도메인 semantic graph
4. `knowledge_pack`은 LLM synthesis 결과

이 분리가 있어야 incremental 재사용과 장애 원인 분리가 쉬워진다.

## 6. UE5 대규모 프로젝트 대응에서 꼭 필요한 요소

### 6.1 Build 정합성

필수 입력:

1. `.uproject`
2. `.uplugin`
3. `.Build.cs`
4. `.Target.cs`
5. 가능하면 `compile_commands.json`
6. 가능하면 UBT/UHT 산출물 위치

이유:

1. Unreal은 소스 파일만 읽어서는 실제 활성 module/target 구성을 확정하기 어렵다.
2. generated header와 macro 확장이 실제 semantic 관계에 큰 영향을 준다.

### 6.2 Reflection 추적

반드시 별도 계층으로 다뤄야 하는 항목:

1. `UCLASS`
2. `USTRUCT`
3. `UENUM`
4. `UPROPERTY`
5. `UFUNCTION`
6. `BlueprintCallable`
7. `BlueprintImplementableEvent`
8. `Server`, `Client`, `NetMulticast`
9. `Replicated`, `ReplicatedUsing`

### 6.3 Asset and Config Coupling

UE에서는 다음도 코드와 동등하게 중요하다.

1. `DefaultEngine.ini`
2. `DefaultGame.ini`
3. map 설정
4. asset path string
5. `TSoftObjectPtr`
6. `ConstructorHelpers`
7. `LoadObject`, `LoadClass`
8. widget blueprint path

즉, "코드 그래프"만으로는 불충분하고 "code + asset + config graph"가 필요하다.

### 6.4 Security Overlay

anti-cheat 관점에서 UE 그래프 위에 다음 overlay를 얹는 것이 좋다.

1. `authority_boundary`
2. `client_trust_boundary`
3. `tamper_sensitive_state`
4. `replicated_sensitive_state`
5. `asset_trust_boundary`
6. `config_attack_surface`
7. `hook_or_patch_sensitive_path`

## 7. 현재 코드 기준 리팩터링 제안

### 7.1 타입 분리

현재 `analysis_project.go`는 기능이 많이 모여 있다.
다음처럼 단계적으로 분리하는 것을 권장한다.

1. `analysis_snapshot.go`
2. `analysis_unreal.go`
3. `analysis_index.go`
4. `analysis_sharding.go`
5. `analysis_synthesis.go`
6. `analysis_persist.go`

효과:

1. 테스트 집중도가 올라간다.
2. snapshot/index/synthesis 경계가 선명해진다.
3. 이후 언어별 인덱서 추가가 쉬워진다.

### 7.2 `ProjectSnapshot` 유지, `SemanticIndex` 신설

현재 `ProjectSnapshot`은 빠른 구조 스캔용으로 유지한다.
대신 아래 타입을 별도 추가한다.

```text
type SemanticIndex struct
type FileRecord struct
type SymbolRecord struct
type SymbolOccurrence struct
type ReferenceRecord struct
type CallEdge struct
type InheritanceEdge struct
type BuildEdge struct
type UnrealSemanticNode struct
type UnrealSemanticEdge struct
```

핵심:

1. snapshot은 가볍게
2. index는 정밀하게
3. knowledge pack은 요약 중심으로

### 7.3 shard 기준 변경

현재는 파일/디렉토리 중심 shard가 강하다.
UE 대응을 위해 다음 shard 클래스를 추가한다.

1. startup shard
2. build graph shard
3. gameplay framework shard
4. replication shard
5. UI/UMG shard
6. GAS shard
7. asset/config coupling shard
8. integrity/anti-cheat shard

권장 규칙:

1. 디렉토리 shard는 fallback
2. semantic shard를 우선
3. high fan-out symbol은 별도 trace shard로 승격

### 7.4 incremental 재사용 정밀화

현행:

1. shard fingerprint 중심

차세대:

1. file fingerprint
2. symbol fingerprint
3. edge fingerprint
4. derived graph fingerprint

재분석 기준:

1. symbol body 변경
2. include/using/dependency 변화
3. Build.cs/Target.cs/UPlugin 변경
4. config/asset binding 변화
5. reflected metadata 변화

### 7.5 worker 입력 재설계

현재 worker prompt는 shard 파일 목록 기반 비중이 크다.
차세대 worker 입력은 아래처럼 구성한다.

1. shard 목적
2. 관련 모듈
3. 관련 symbol 집합
4. 주요 call path
5. 역참조 상위 항목
6. asset/config 연계
7. 보안 overlay 신호
8. 읽어야 할 실제 코드 조각

즉, 파일 묶음을 던지는 대신 "정리된 증거 묶음"을 준다.

## 8. 단계별 구현 로드맵

### Phase 1: Structural groundwork

목표:

1. snapshot / synthesis / persistence 분리
2. `SemanticIndex` 타입 추가
3. 기존 snapshot에서 추출 가능한 관계를 index 형식으로 저장

완료 조건:

1. 기존 `/analyze-project` 산출물과 호환 유지
2. `latest/structural_index.json` 생성

### Phase 2: UE semantic indexing

목표:

1. Unreal 관련 메타데이터를 graph 노드/엣지로 승격
2. gameplay, replication, UI, GAS, asset/config graph 강화

완료 조건:

1. `latest/unreal_graph.json` 생성
2. fallback 문서가 graph 기반 요약을 사용

### Phase 3: Query-mode retrieval

목표:

1. `map`, `trace`, `impact`, `surface`, `performance` 질의 모드 도입
2. retrieval 정책 분리

완료 조건:

1. worker prompt 생성 시 질의 모드 반영
2. trace형 질의에서 정밀 path output 생성

### Phase 4: Symbol-level incremental reuse

목표:

1. symbol 단위 invalidation
2. graph overlay 단위 재계산

완료 조건:

1. 단일 파일 변경이 전체 shard 재분석으로 번지지 않음
2. UE config만 바뀐 경우 관련 graph만 갱신

### Phase 5: Security and anti-cheat overlay

목표:

1. authority boundary
2. tamper-sensitive state
3. RPC validation surface
4. asset/config trust boundary

완료 조건:

1. anti-cheat 관련 질의에서 일반 구조도보다 더 강한 결과 제공
2. `/analyze-performance`, `/verify`, `/simulate`와 연계 가능

## 9. 추천 우선순위

구현 우선순위는 다음과 같다.

1. `SemanticIndex` 계층 도입
2. snapshot/synthesis/persistence 파일 분리
3. UE semantic graph 도입
4. semantic shard planner 추가
5. symbol-level incremental reuse
6. 질의 모드별 retrieval 분리
7. anti-cheat security overlay
8. GraphRAG 스타일 community summary 추가

중요한 판단:

1. GraphRAG는 나중 단계가 맞다.
2. 먼저 코드와 UE 의미를 정확히 잡아야 한다.
3. 그 뒤에 전역 요약과 커뮤니티 탐색을 붙여야 품질이 유지된다.

## 10. 실제 개발 시작점 제안

가장 현실적인 첫 구현 묶음은 다음이다.

1. `SemanticIndex` 타입 추가
2. `persistRun`에 snapshot/index/artifact 분리 저장 추가
3. UE 메타데이터를 `UnrealSemanticNode`, `UnrealSemanticEdge`로 변환하는 함수 추가
4. `planShards`에 semantic bucket 개념 추가
5. `buildWorkerPrompt` 입력을 파일 목록 중심에서 graph evidence 중심으로 바꾸기 위한 중간 구조 추가

이 다섯 개가 들어가면 이후 기능은 대부분 같은 방향으로 확장할 수 있다.

## 11. 최종 권고

UE5급 대규모 프로젝트 지원을 위해 Kernforge가 취해야 할 핵심 전략은 다음이다.

1. Plain RAG 중심 확장은 피한다.
2. precise code navigation 성격의 구조 인덱스를 먼저 만든다.
3. Unreal 전용 semantic graph를 별도 계층으로 둔다.
4. LLM은 구조 인덱스 위에서 요약, 추론, 검토를 수행하게 한다.
5. anti-cheat/security overlay는 별도 그래프 레이어로 설계한다.

한 줄로 정리하면:

`텍스트 요약 엔진`에서 `구조 인덱스 기반 분석 엔진`으로 올라가야 한다.
