# analysis-project 개발자 문서 생성 계획

기준 시점:
- 코드베이스 기준: 2026-04-27
- 대상 기능: `/analyze-project`, `/docs-refresh`, `.kernforge/analysis/latest/docs`

## 0. 진행 상태

2026-04-27 1차 구현 완료:

1. `analysisGeneratedDocNames()`를 추가해 docs/dashboard의 생성 문서 목록을 공용화했다.
2. `analysis_developer_docs.go`를 추가해 developer folder/module aggregate와 문서 빌더를 구현했다.
3. `/analyze-project`와 `/docs-refresh` 산출물에 다음 문서 3종을 추가했다.
   - `DEVELOPER_OVERVIEW.md`
   - `FOLDER_MAP.md`
   - `MODULES.md`
4. `analysisDocTitle`, `analysisDocPurpose`, `analysisDocSourceAnchors`, `analysisDocSections`, `analysisDocStaleMarkers`를 신규 문서에 맞게 갱신했다.
5. dashboard 문서 목록이 공용 문서 목록을 사용하도록 갱신했다.
6. `analysis_project_test.go`의 문서 목록/manifest count 기대값을 공용 문서 목록 기준으로 바꿨다.
7. `analysis_developer_docs_test.go`를 추가해 folder/module aggregate와 문서 렌더링을 검증했다.
8. `go test ./...` 통과를 확인했다.

2026-04-27 2차 구현 완료:

1. 완료: `STRUCTURE_DIAGRAMS.md` 생성
2. 완료: `CODE_STRUCTURE_REFERENCE.md` 생성
3. 완료: developer graph view model 추가
4. 완료: graph stale marker를 `STRUCTURE_DIAGRAMS.md`에 연결
5. 완료: developer docs reuse target과 memory/evidence tag 보강
6. 유지: vector corpus는 `buildAnalysisDocs`와 `analysisDocSections` 기반으로 신규 문서를 자동 포함
7. `analysis_developer_docs_test.go`에 structure diagram/code reference 렌더링 테스트를 추가했다.
8. `go test ./...` 통과를 확인했다.

2026-04-27 Phase 4 잔여 항목 구현 완료:

1. dashboard generated docs 카드에 developer docs badge를 추가했다.
2. Document Portal에 `Developer Docs`, `Verification`, `Fuzz`, `Evidence` 빠른 필터를 추가했다.
3. developer 문서는 portal kind가 `developer document`로 표시되도록 했다.
4. dashboard 테스트에 developer docs badge/filter 검증을 추가했다.
5. `go test ./...` 통과를 확인했다.

2026-04-27 Phase 5 잔여 항목 구현 완료:

1. empty snapshot에서도 developer docs 5종이 비어 있지 않고 fallback 문구를 출력하는 테스트를 추가했다.
2. Windows 백슬래시 경로가 folder map, code reference, source anchor metadata에서 slash-normalized 되는지 테스트를 추가했다.
3. `analysisDocSlashPath`, `analysisDocSlashPaths`, `analysisDocDir` 헬퍼를 추가하고 developer docs 경로 출력에 적용했다.
4. 공용 `symbolFiles`도 slash-normalized source anchor를 생성하도록 보강했다.
5. `go test ./...` 통과를 확인했다.

2026-04-27 explicit path scan root 수정 완료:

1. 단일 `/analyze-project --path <dir>` 실행 시 preview/run analyzer의 scan root를 해당 폴더로 좁히도록 수정했다.
2. scoped root가 적용된 경우 상위 workspace의 `external`, hidden candidate 같은 예외 후보 디렉터리를 다시 discovery하지 않도록 `prepareAnalysisDirectorySelection`의 기준 root를 조정했다.
3. 다중 `--path` 실행은 기존처럼 workspace root scan 후 scope filter를 유지하도록 했다.
4. 단일 path root narrowing, 다중 path 기존 동작 유지, 상위 `external` 재스캔 방지 테스트를 추가했다.
5. `go test ./...` 통과를 확인했다.

2026-04-27 startup project root-boundary 수정 완료:

1. 하위 폴더 분석 중 `.sln`이 `..\OtherProject\Other.vcxproj` 같은 root 밖 project를 참조해도 startup 후보로 쓰지 않도록 `parseSolutionProjects` 경로 정규화를 보강했다.
2. solution project path는 `.sln` 파일 위치 기준으로 해석한 뒤 analysis root 내부인지 검증한다.
3. root 밖 project는 `SolutionProjects`, `StartupProjects`, `PrimaryStartup`, runtime edge 후보에서 제외된다.
4. 하위 analysis root에서 sibling project가 primary startup으로 선택되지 않는 회귀 테스트를 추가했다.
5. `go test ./...` 통과를 확인했다.

남은 항목:

1. 대형 UE 프로젝트 기준 Mermaid graph top-N/fallback 실사용 검증

## 1. 문제 정의

현재 `analysis-project`는 프로젝트 운영과 보안 분석에 필요한 문서를 생성한다.

현재 생성 문서:

1. `ARCHITECTURE.md`
2. `SECURITY_SURFACE.md`
3. `API_AND_ENTRYPOINTS.md`
4. `BUILD_AND_ARTIFACTS.md`
5. `VERIFICATION_MATRIX.md`
6. `FUZZ_TARGETS.md`
7. `OPERATIONS_RUNBOOK.md`
8. `INDEX.md`

이 문서들은 보안 표면, entrypoint, build artifact, verification/fuzz workflow에는 강하다. 하지만 새 개발자가 코드베이스를 이해하고 수정 범위를 잡기 위해 필요한 다음 문서가 부족하다.

1. 폴더별 역할과 ownership
2. 모듈별 내부 구조
3. 모듈 간 의존 관계 구조도
4. 주요 실행 흐름 구조도
5. 파일/심볼/빌드 컨텍스트를 연결한 상세 개발자 참조
6. 코드 변경 시 어느 문서를 먼저 읽고 어느 테스트를 돌려야 하는지에 대한 개발자 온보딩 가이드

결론:

- 현재 문서 세트는 "운영 지식 베이스"에 가깝다.
- 추가해야 할 문서 세트는 "개발자 온보딩 및 변경 설계용 코드베이스 지도"다.

## 2. 현재 구현 조사 요약

핵심 파일:

1. `analysis_project.go`
   - `ProjectAnalysisRun`에 `Snapshot`, `KnowledgePack`, `SemanticIndexV2`, `UnrealGraph`, `VectorCorpus`가 이미 포함된다.
   - `persistRun`이 run JSON, snapshot, structural index, docs, dashboard, latest mirror를 저장한다.
2. `analysis_docs.go`
   - `buildAnalysisDocs`가 문서 목록을 고정 map으로 만든다.
   - `writeAnalysisDocs`가 문서 파일과 `manifest.json`을 생성한다.
   - `analysisDocTitle`, `analysisDocPurpose`, `analysisDocSections`, `analysisDocSourceAnchors`, `analysisDocReuseTargets`가 문서 metadata를 관리한다.
3. `analysis_dashboard.go`
   - dashboard가 문서 목록을 고정 배열로 렌더링한다.
   - docs portal, source anchor, stale diff, trust boundary, attack/data-flow view를 이미 지원한다.
4. `analysis_docs_reuse.go`
   - docs manifest를 evidence와 persistent memory로 승격한다.
   - 새 문서가 manifest에 들어가면 reuse record에는 자연스럽게 포함될 수 있다.
5. `analysis_project_test.go`, `main_analyze_project_test.go`
   - 생성 문서 개수, 문서 이름, manifest count, dashboard link에 대한 테스트가 있다.
   - 문서 추가 시 테스트 기대값을 갱신해야 한다.

이미 활용 가능한 데이터:

1. `ProjectSnapshot.Files`, `Directories`, `ManifestFiles`, `EntrypointFiles`, `BuildContexts`, `CompileCommands`, `ProjectEdges`
2. `KnowledgePack.Subsystems`, `ArchitectureGroups`, `TopImportantFiles`, `HighRiskFiles`, `Unknowns`
3. `SemanticIndexV2.Files`, `Symbols`, `CallEdges`, `BuildOwnershipEdges`, overlay/path evidence
4. `UnrealGraph.Nodes`, `UnrealGraph.Edges`
5. shard별 `WorkerReport`와 `ShardDocuments`

부족한 데이터:

1. folder-level aggregate 모델
2. module-level aggregate 모델
3. symbol-to-folder/module reverse index
4. developer-facing diagram view model
5. 문서별 stable section id와 stale marker 세분화

## 3. 권장 방향

추천 방향:

`Folder/Module Inventory + Structural Graph Projection + Developer Docs + Dashboard Portal Integration`

핵심 원칙:

1. LLM에게 새 문서를 통째로 맡기지 않는다.
2. 가능한 내용은 snapshot/semantic index에서 deterministic하게 생성한다.
3. LLM worker report는 설명 품질을 보강하는 보조 evidence로만 쓴다.
4. 문서마다 source anchor, confidence, stale marker, reuse target을 manifest에 넣는다.
5. dashboard와 vector corpus가 새 문서를 자동으로 재사용하게 만든다.

## 4. 추가할 개발자 문서 세트

### 4.1 `DEVELOPER_OVERVIEW.md`

목적:

1. 새 개발자가 10분 안에 프로젝트의 큰 구조를 잡는다.
2. 어떤 폴더부터 읽어야 하는지 알려준다.
3. 주요 개발 workflow와 관련 문서를 연결한다.

권장 섹션:

1. Project Shape
2. Primary Execution Flow
3. Main Development Areas
4. Where To Start By Task
5. Change Risk Map
6. Reading Order

데이터 소스:

1. `KnowledgePack.ProjectSummary`
2. `KnowledgePack.ArchitectureGroups`
3. `KnowledgePack.Subsystems`
4. `Snapshot.EntrypointFiles`
5. `SemanticIndexV2.CallEdges`

### 4.2 `FOLDER_MAP.md`

목적:

1. 각 폴더가 무슨 역할인지 설명한다.
2. 폴더별 주요 파일, entrypoint, 테스트, build ownership을 정리한다.
3. 변경 시 downstream 영향과 stale marker를 보여준다.

권장 섹션:

1. Folder Summary Table
2. Top-Level Folder Responsibilities
3. Folder To Module Mapping
4. Folder To Test Mapping
5. Folder Risk And Change Notes

테이블 컬럼:

1. Folder
2. Responsibility
3. Key Files
4. Main Symbols
5. Build Context
6. Tests
7. Risk
8. Source Anchors

데이터 소스:

1. `ProjectSnapshot`의 파일 목록
2. `KnowledgePack.Subsystems[].KeyFiles`
3. `SemanticIndexV2.Symbols`
4. `Snapshot.BuildContexts`
5. 파일명 패턴 기반 테스트 매핑

### 4.3 `MODULES.md`

목적:

1. 코드 모듈의 책임과 내부 구조를 정리한다.
2. Go 단일 패키지 프로젝트, C++/UE module, C# project를 같은 추상화로 다룬다.
3. 모듈 간 dependency와 public/internal boundary를 보여준다.

권장 섹션:

1. Module Inventory
2. Module Responsibility Cards
3. Public Entrypoints
4. Internal Subsystems
5. Module Dependencies
6. Module Verification Notes

모듈 추론 규칙:

1. Go: package + filename cluster + subsystem match
2. C++: `.Build.cs`, `.Target.cs`, include root, source root
3. C#: `.csproj`, namespace root
4. UE: `.uproject`, `.uplugin`, module descriptor, reflection/network/GAS tags
5. fallback: top-level folder and filename prefix cluster

데이터 소스:

1. `Snapshot.BuildContexts`
2. `SemanticIndexV2.BuildOwnershipEdges`
3. `KnowledgePack.Subsystems`
4. `UnrealGraph`

### 4.4 `STRUCTURE_DIAGRAMS.md`

목적:

1. Mermaid 기반 구조도를 문서로 남긴다.
2. dashboard에서도 바로 링크 가능한 diagram section을 만든다.
3. 개발자가 dependency, data flow, ownership boundary를 빠르게 본다.

권장 diagram:

1. Folder/module dependency graph
2. Primary runtime flow
3. Build and artifact flow
4. Security/trust boundary summary
5. Analysis pipeline flow

Mermaid 생성 원칙:

1. 노드는 20개 이하로 제한한다.
2. edge는 confidence와 evidence가 있는 항목을 우선한다.
3. 너무 큰 그래프는 top-N과 "omitted edges" 카운트를 표시한다.
4. 그래프가 비어 있으면 fallback table을 출력한다.

데이터 소스:

1. `Snapshot.ProjectEdges`
2. `analysisGraphEdgeViews`
3. `analysisGraphTrustBoundaryViews`
4. `analysisGraphDataFlowViews`
5. 새로 만들 `DeveloperModuleGraph`

### 4.5 `CODE_STRUCTURE_REFERENCE.md`

목적:

1. 상세 개발자 참조 문서다.
2. 주요 심볼, 파일 cluster, call edge, build context를 한 문서에 정리한다.
3. "어디를 고치면 어디가 영향을 받는가"를 빠르게 추적한다.

권장 섹션:

1. Important Files
2. Important Symbols
3. Representative Call Paths
4. Build Ownership
5. Generated Or Derived Artifacts
6. Source Anchors

데이터 소스:

1. `SemanticIndexV2.Symbols`
2. `SemanticIndexV2.CallEdges`
3. `Snapshot.BuildContexts`
4. `KnowledgePack.TopImportantFiles`
5. `KnowledgePack.HighRiskFiles`

## 5. 신규 내부 모델

새 파일 후보:

1. `analysis_developer_docs.go`
2. `analysis_developer_docs_test.go`

권장 타입:

```go
type DeveloperFolderRecord struct {
	Path             string
	Responsibility   string
	KeyFiles         []string
	TestFiles        []string
	MainSymbols      []SymbolRecord
	BuildContexts    []BuildContextRecord
	Subsystems       []string
	RiskSignals      []string
	SourceAnchors    []string
	Confidence       string
}

type DeveloperModuleRecord struct {
	ID              string
	Name            string
	Kind            string
	Root            string
	Responsibility  string
	PublicFiles     []string
	InternalFiles   []string
	Entrypoints     []string
	Dependencies    []string
	BuildContexts   []string
	SourceAnchors   []string
	Confidence      string
}

type DeveloperStructureGraph struct {
	Nodes []DeveloperStructureNode
	Edges []DeveloperStructureEdge
}
```

구현 함수:

```go
func buildDeveloperFolderRecords(run ProjectAnalysisRun) []DeveloperFolderRecord
func buildDeveloperModuleRecords(run ProjectAnalysisRun) []DeveloperModuleRecord
func buildDeveloperStructureGraph(run ProjectAnalysisRun, modules []DeveloperModuleRecord) DeveloperStructureGraph
func buildAnalysisDeveloperOverviewDoc(run ProjectAnalysisRun) string
func buildAnalysisFolderMapDoc(run ProjectAnalysisRun) string
func buildAnalysisModulesDoc(run ProjectAnalysisRun) string
func buildAnalysisStructureDiagramsDoc(run ProjectAnalysisRun) string
func buildAnalysisCodeStructureReferenceDoc(run ProjectAnalysisRun) string
```

## 6. 구현 계획

### Phase 1: deterministic inventory

상태: 완료

1. 완료: `analysis_developer_docs.go` 추가
2. 완료: folder aggregate 생성
3. 완료: module aggregate 생성
4. 완료: test file mapping 생성
5. 완료: source anchor 정규화
6. 완료: unit test 추가

완료 기준:

1. Go 루트 프로젝트에서 folder/module record가 비어 있지 않다.
2. 테스트 파일이 관련 folder에 매핑된다.
3. `KnowledgePack.Subsystems`의 key file이 folder/module record에 반영된다.

### Phase 2: 문서 빌더 추가

상태: 완료

수정 파일:

1. `analysis_docs.go`
2. `analysis_developer_docs.go`
3. `analysis_project_test.go`

작업:

1. 완료: `buildAnalysisDocs`에 신규 문서 5종 추가
2. 완료: `analysisDocTitle` 갱신
3. 완료: `analysisDocPurpose` 갱신
4. 완료: `analysisDocSourceAnchors` 갱신
5. 완료: `analysisDocSections` 갱신
6. 유지: 신규 문서는 기본 `analysis_context`, `memory` reuse target 사용
7. 완료: manifest document count 테스트 갱신
8. 완료: `STRUCTURE_DIAGRAMS.md` 추가
9. 완료: `CODE_STRUCTURE_REFERENCE.md` 추가

완료 기준:

1. `writeAnalysisDocs`가 신규 문서를 생성한다.
2. manifest에 신규 문서와 section metadata가 들어간다.
3. `/docs-refresh` 재생성 경로에서도 동일하게 생성된다.

### Phase 3: 구조도 생성

상태: 완료

수정 파일:

1. `analysis_graph.go`
2. `analysis_developer_docs.go`
3. `analysis_dashboard.go`

작업:

1. module dependency graph 생성
2. folder dependency graph 생성
3. Mermaid 렌더링 재사용
4. graph source anchor/stale marker 연결
5. dashboard source anchor drilldown에서 신규 section 링크 처리

완료 기준:

1. `STRUCTURE_DIAGRAMS.md`에 최소 2개 이상의 Mermaid graph가 생성된다.
2. 그래프 데이터가 부족하면 table fallback이 생성된다.
3. dashboard portal search에서 구조도 문서가 검색된다.

### Phase 4: dashboard와 reuse 통합

상태: 완료

수정 파일:

1. `analysis_dashboard.go`
2. `analysis_docs_reuse.go`
3. `analysis_docs_vector.go`

작업:

1. 완료: dashboard의 고정 문서 목록을 공용 문서 목록으로 교체
2. 완료: developer docs 전용 portal visual badge/filter 추가
3. 완료: vector corpus는 `buildAnalysisDocs` 기반이라 신규 문서를 자동 포함
4. 완료: memory/evidence tag에 `developer-docs` 추가

완료 기준:

1. dashboard에서 신규 문서 링크가 보인다.
2. source anchor table에서 신규 문서 section으로 이동할 수 있다.
3. vector corpus JSONL에 신규 문서가 들어간다.

### Phase 5: 품질 게이트와 회귀 테스트

상태: 완료

테스트 항목:

1. 완료: 생성 문서 목록 테스트 갱신
2. 완료: manifest document count 갱신
3. 완료: dashboard doc link가 공용 목록을 쓰도록 갱신
4. 완료: folder/module aggregate unit test 추가
5. 완료: structure diagram/code reference 렌더링 테스트 추가
6. 완료: empty snapshot fallback 전용 테스트 추가
7. 완료: Windows path normalization 전용 테스트 추가

실행:

```powershell
go test ./...
```

완료 기준:

1. 전체 테스트 통과
2. 신규 문서가 `.kernforge/analysis/latest/docs`에 생성됨
3. 기존 문서명과 기존 manifest schema compatibility 유지

## 7. 문서 생성 정책

문서 품질 기준:

1. 모든 문서는 source anchor를 가진다.
2. confidence가 낮은 추론은 명확히 표시한다.
3. folder/module 책임은 deterministic evidence를 우선한다.
4. LLM report에서 가져온 설명은 `WorkerReport` evidence가 있을 때만 사용한다.
5. 빈 데이터일 때도 문서 파일은 생성하되 fallback 안내를 제공한다.

출력 크기 제한:

1. folder table은 기본 60개 이하
2. module card는 기본 40개 이하
3. symbol 목록은 기본 80개 이하
4. diagram node는 기본 20개 이하
5. overflow는 omitted count로 표시

stale marker 정책:

1. 폴더 문서는 해당 folder 하위 파일 변경 시 stale
2. 모듈 문서는 build context, key file, dependency edge 변경 시 stale
3. 구조도 문서는 graph edge, build ownership, call edge 변경 시 stale
4. code reference 문서는 semantic index fingerprint 변경 시 stale

## 8. 예상 구현 난이도와 리스크

난이도:

1. Phase 1: 중간
2. Phase 2: 낮음
3. Phase 3: 중간
4. Phase 4: 낮음
5. Phase 5: 낮음

주요 리스크:

1. 고정 문서 목록이 여러 곳에 중복되어 있어 누락 가능성이 있다.
2. folder/module responsibility 추론이 과하게 자신감 있게 보일 수 있다.
3. 대형 UE 프로젝트에서 Mermaid graph가 너무 커질 수 있다.
4. manifest document count 테스트가 깨질 수 있다.
5. vector corpus와 dashboard가 문서 목록을 서로 다르게 볼 수 있다.

완화:

1. 문서 목록을 `analysisGeneratedDocNames()` 같은 단일 함수로 통합하는 리팩터를 선행하거나 Phase 2에 포함한다.
2. responsibility에는 confidence와 source anchor를 항상 붙인다.
3. diagram에는 top-N 제한과 fallback table을 둔다.
4. manifest 기반 테스트를 추가해 dashboard/docs/vector 목록 불일치를 잡는다.

## 9. 권장 작업 순서

1. `analysisGeneratedDocNames()`를 만들어 문서 목록 중복을 제거한다.
2. `analysis_developer_docs.go`에 folder/module aggregate를 구현한다.
3. `DEVELOPER_OVERVIEW.md`, `FOLDER_MAP.md`, `MODULES.md`부터 추가한다.
4. `STRUCTURE_DIAGRAMS.md`와 `CODE_STRUCTURE_REFERENCE.md`를 추가한다.
5. dashboard fixed list를 공용 문서 목록으로 교체한다.
6. manifest, vector corpus, evidence/memory reuse 테스트를 갱신한다.
7. `go test ./...`로 회귀를 확인한다.

## 10. 최종 산출물

구현 완료 후 `/analyze-project`와 `/docs-refresh`는 다음 개발자 문서를 추가 생성해야 한다.

1. `.kernforge/analysis/latest/docs/DEVELOPER_OVERVIEW.md`
2. `.kernforge/analysis/latest/docs/FOLDER_MAP.md`
3. `.kernforge/analysis/latest/docs/MODULES.md`
4. `.kernforge/analysis/latest/docs/STRUCTURE_DIAGRAMS.md`
5. `.kernforge/analysis/latest/docs/CODE_STRUCTURE_REFERENCE.md`

그리고 다음 항목이 함께 갱신되어야 한다.

1. `.kernforge/analysis/latest/docs/INDEX.md`
2. `.kernforge/analysis/latest/docs_manifest.json`
3. `.kernforge/analysis/latest/dashboard.html`
4. vector corpus artifacts
5. evidence/memory reuse records

## 11. 추천 결론

바로 구현한다면 1차 목표는 "개발자가 읽을 수 있는 folder/module 문서"다. 구조도와 심볼 참조는 그 다음이다.

추천 1차 PR 범위:

1. 완료: 문서 목록 공용화
2. 완료: `DEVELOPER_OVERVIEW.md`
3. 완료: `FOLDER_MAP.md`
4. 완료: `MODULES.md`
5. 완료: manifest/dashboard/test 갱신

추천 2차 PR 범위:

1. 완료: `STRUCTURE_DIAGRAMS.md`
2. 완료: `CODE_STRUCTURE_REFERENCE.md`
3. 완료: graph stale marker 강화
4. 유지: vector corpus section record는 신규 `analysisDocSections`를 통해 자동 생성

이 순서가 가장 안전하다. 1차만으로도 개발자 온보딩 문서의 실효성이 생기고, 2차에서 그래프와 심볼 참조의 정확도를 더 끌어올릴 수 있다.

## 12. TavernKernel map 문서 리뷰 후 개선 작업

검토 대상:

1. `C:/git/im-tavern-client/.kernforge/analysis/20260427-221504_map_프로젝트_구조를_분석해서_문서로_작성해.md`
2. 해당 run의 `docs` 디렉터리 산출물

발견한 문제:

1. 완료: Visual Studio startup project를 전체 시스템의 유일한 entrypoint처럼 표현할 수 있었다.
2. 완료: 한국어 요청인데 최종 문서가 영어로 생성될 수 있었다.
3. 완료: 최종 문서에 `snippet-limited` 같은 내부 작업 흔적이 남을 수 있었다.
4. 완료: `FOLDER_MAP.md`에서 폴더 책임이 대표 유틸리티 심볼에 과하게 끌려갈 수 있었다.
5. 완료: `STRUCTURE_DIAGRAMS.md`에서 folder/module self-loop가 생성될 수 있었다.
6. 완료: IOCTL/device-control 계약이 별도 표로 정리되지 않았다.
7. 완료: user-mode harness와 kernel `DriverEntry` 활성화 계층이 분리되지 않았다.
8. 완료: test harness와 실제 test file 분류가 섞일 수 있는 위험을 회귀 테스트로 보강했다.

구현한 개선:

1. 완료: `textContainsHangul`을 추가해 사용자 goal에 한글이 포함되면 worker/synthesis prompt가 한국어 최종 문서를 요구하도록 했다.
2. 완료: worker/synthesis prompt에서 `snippet` 표현 대신 `context-truncated/source-limited` 같은 source-state 표현을 쓰도록 유도했다.
3. 완료: startup 문구를 `primary startup project` 중심에서 `solution startup candidate`로 바꾸고, driver/runtime entry files를 별도 렌즈로 출력하게 했다.
4. 완료: developer overview에 `Startup And Entrypoint Lens`를 추가해 user-mode harness, SCM/service load path, kernel/runtime entry를 분리하도록 했다.
5. 완료: code structure reference와 overview에 `IOCTL And Device-Control Contract` 섹션을 추가했다.
6. 완료: folder responsibility 추론에서 driver/kernel, user-mode bootstrap harness, shared common contracts를 우선 인식하게 했다.
7. 완료: folder/module diagram, runtime graph, build artifact graph에서 self-loop edge를 제거했다.
8. 완료: 관련 회귀 테스트를 추가했다.

수정 파일:

1. 완료: `locale.go`
2. 완료: `analysis_project.go`
3. 완료: `analysis_developer_docs.go`
4. 완료: `analysis_graph.go`
5. 완료: `analysis_project_test.go`
6. 완료: `analysis_developer_docs_test.go`
7. 완료: `ANALYSIS_PROJECT_DEVELOPER_DOCS_PLAN_kor.md`

검증:

```powershell
go test ./...
```

결과:

1. 완료: 전체 테스트 통과
2. 완료: 한국어 goal prompt 유도 테스트 추가
3. 완료: startup/driver entry 분리 테스트 추가
4. 완료: IOCTL contract 렌더링 테스트 추가
5. 완료: folder responsibility 보정 테스트 추가
6. 완료: self-loop 제거 테스트 추가

남은 항목:

1. 남음: 실제 TavernKernel 대상으로 `/analyze-project --mode map`을 다시 실행해 새 한국어 문서가 기대 형태로 생성되는지 end-to-end 확인
2. 남음: `FOLDER_MAP.md`의 `Tests` 열이 harness 프로젝트와 단위 테스트를 더 명확히 구분하도록 별도 `Harness/Tools` 열을 추가할지 검토
3. 남음: IOCTL code/struct 추출을 단순 symbol 기반에서 `CTL_CODE`, shared struct layout 파싱 기반으로 확장
4. 남음: 단일 최종 markdown 본문도 완전 deterministic 문서 세트처럼 source anchor를 line-level로 붙이는 개선 검토

## 13. 전체 커맨드 응답 언어 정책 개선

요구사항:

1. 완료: 특별한 사용자 언어 지시가 있으면 그 지시를 최우선으로 따른다.
2. 완료: 특별한 언어 지시가 없으면 질문/명령 인자의 언어를 보고 응답 언어를 판단한다.
3. 완료: 질문 언어도 명확하지 않을 때만 설정 또는 시스템 locale을 fallback으로 사용한다.
4. 완료: 이 정책은 일반 대화뿐 아니라 Kernforge 커맨드 응답에도 적용한다.

구현한 개선:

1. 완료: `responseLanguageInstructionForUserText`를 추가해 공통 agent system prompt에 응답 언어 정책을 주입했다.
2. 완료: `inferResponseLanguageForUserText`를 추가해 명시적 한국어/영어 요청을 우선 감지하고, 다음으로 한글 포함 여부와 영어 비율을 본다.
3. 완료: `configWithResponseLanguageForUserText`를 추가해 별도 렌더러를 가진 커맨드가 명령 인자 언어를 따를 수 있게 했다.
4. 완료: `/fuzz-func` 실행 결과 렌더링이 명령 인자 언어를 따르도록 command-local config를 적용했다.
5. 완료: `korean`을 내부 `fuzz_func_output_language` 값으로 허용해 시스템 locale이 영어여도 한국어 질문이면 한국어 렌더링을 사용할 수 있게 했다.

검증:

```powershell
go test ./...
```

결과:

1. 완료: 전체 테스트 통과
2. 완료: 공통 system prompt가 한국어 질문을 한국어 응답 정책으로 감지하는 테스트 추가
3. 완료: 명시적 영어 요청이 한국어 질문보다 우선하는 테스트 추가
4. 완료: `/fuzz-func` command-local config가 한국어 query를 따라가는 테스트 추가

남은 항목:

1. 남음: `/analyze-performance`, `/simulate`, `/investigate`처럼 agent prompt를 경유하는 커맨드는 공통 system prompt로 커버되지만, 자체 renderer가 추가될 때마다 `configWithResponseLanguageForUserText` 적용 여부를 점검
2. 남음: 일본어/중국어 등 한영 외 언어 감지는 현재 fallback 수준이므로 다국어 요구가 생기면 확장

## 14. TavernKernel map 대시보드 리뷰 후 개선 작업

검토 대상:

1. `C:/git/im-tavern-client/.kernforge/analysis/20260427-221504_map_프로젝트_구조를_분석해서_문서로_작성해_dashboard.html`

발견한 문제:

1. 완료: 한국어 질문에서 생성된 대시보드인데 HTML `lang`, title, 주요 UI 문구가 영어 중심이었다.
2. 완료: 문서 포털의 초기 table row와 inline JSON 데이터가 같은 내용을 중복 보유해 대시보드가 커질 수 있었다.
3. 완료: portal/doc/source/stale diff 링크 일부가 전달받은 `docsHref` 대신 고정 `docs/` 경로를 사용할 수 있었다.
4. 완료: developer docs가 일반 문서와 구분되지 않아 온보딩용 문서 세트를 빠르게 필터링하기 어려웠다.
5. 완료: startup project, kernel driver entry, IOCTL/device-control, build/signing artifact가 대시보드 첫 화면에서 분리되어 보이지 않았다.
6. 완료: `no_previous_run` 같은 초기 기준선 없음 상태가 `stale`처럼 과하게 표시될 수 있었다.

구현한 개선:

1. 완료: `analysisDashboardLabelsForRun`을 추가해 한국어 goal이면 HTML `lang="ko"`, title, 주요 메타/metric/portal/source anchor 문구를 한국어로 렌더링한다.
2. 완료: 런타임 렌즈를 추가해 `Startup 후보`, driver/runtime entry, IOCTL/device-control, build/signing artifact를 상단 카드로 노출한다.
3. 완료: 문서 포털에 developer docs, verification, fuzz, evidence 필터를 추가하고 developer docs badge/search tag를 부여했다.
4. 완료: portal inline JSON을 1200개로 제한하고 초기 table은 loading fallback row만 렌더링해 HTML 중복과 과도한 초기 크기를 줄였다.
5. 완료: source anchor, stale diff, generated doc 링크가 호출자가 넘긴 `docsHref`를 일관되게 사용하도록 정규화했다.
6. 완료: `no_previous_run`만 있는 문서는 `baseline:none`으로 표시해 실제 stale 상태와 구분했다.
7. 완료: 한국어 대시보드, docsHref 일관성, baseline 상태, runtime lens를 검증하는 회귀 테스트를 추가했다.

수정 파일:

1. 완료: `analysis_dashboard.go`
2. 완료: `analysis_project_test.go`
3. 완료: `ANALYSIS_PROJECT_DEVELOPER_DOCS_PLAN_kor.md`

검증:

```powershell
go test ./...
```

결과:

1. 완료: 전체 테스트 통과
2. 완료: 한국어 dashboard label 회귀 테스트 통과
3. 완료: `docsHref` 고정 경로 회귀 테스트 통과
4. 완료: runtime lens와 `baseline:none` 상태 회귀 테스트 통과

추가 코드리뷰 후 수정:

1. 완료: portal JSON을 수동 문자열 조립으로 생성하던 부분을 `encoding/json` 기반으로 교체했다.
2. 완료: portal item에 `</script>`가 포함되어도 dashboard `<script>` 블록이 조기 종료되지 않도록 회귀 테스트를 추가했다.
3. 완료: 추가 수정 후 전체 테스트를 다시 통과시켰다.

남은 항목:

1. 남음: 실제 TavernKernel 대상으로 `/analyze-project --mode map`을 다시 실행해 신규 dashboard HTML을 브라우저에서 시각 검증
2. 남음: 대시보드의 나머지 심화 섹션(`Evidence And Memory Drilldown`, `Stale Section Diff`, `Trust Boundary Graph` 등)까지 완전 한국어 label set으로 확장할지 검토
3. 남음: portal item이 1200개를 초과하는 초대형 프로젝트에서 별도 JSON sidecar lazy-load를 붙일지 검토
