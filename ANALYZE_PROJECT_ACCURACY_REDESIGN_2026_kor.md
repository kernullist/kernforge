# analyze-project 분석 정확도 개선 설계

기준 시점:
- 코드베이스 기준: 2026-05-31
- 외부 도구/기술 조사 기준: 2026-05-31
- 대상 기능: `/analyze-project`, `/docs-refresh`, structural index v2, deterministic architecture fact pack, cached project analysis QA

목표:

`/analyze-project`가 단순 프로젝트 요약기가 아니라, 대형 Windows driver/anti-cheat/UE/C++/Go 혼합 워크스페이스에서 정확한 구조 지도, 실행 흐름, 보안 표면, 변경 영향, 검증 포인트를 안정적으로 생성하도록 만든다.

권장 결론:

현재 구조의 큰 방향은 맞다. 하지만 정확도를 올리는 핵심은 더 큰 텍스트를 LLM에 밀어 넣는 것이 아니라, `build-aware precise index + AST structural index + graph-guided evidence packets + deterministic claim verifier`로 바꾸는 것이다. LLM worker는 원시 파일을 앞부분만 읽는 분석자가 아니라, 검증 가능한 symbol slice와 graph evidence를 합성하는 역할로 내려와야 한다.

## 1. 코드 리뷰 findings

### P0. worker가 실제 핵심 코드를 거의 보지 못한다

현재 worker prompt는 `buildFileContext`를 통해 shard primary/reference 파일 중 최대 10개만 넣고, 각 파일도 앞 60줄만 잘라 넣는다.

근거:
- `cmd/kernforge/analysis_project.go:12912`
- `cmd/kernforge/analysis_project.go:13981`
- `cmd/kernforge/analysis_project.go:14002`

영향:

1. 긴 C++/driver 파일에서 실제 `DriverEntry`, IOCTL handler, callback registration, validation 함수가 파일 중후반에 있으면 worker는 metadata와 파일 앞부분만 보고 추론한다.
2. `SemanticIndexV2`와 `ArchitectureFactPack`이 있어도, worker에게 주입되는 code evidence가 함수 body가 아니라 file prefix라서 flow 설명이 얇아지거나 line/symbol 오라벨링이 생긴다.
3. review prompt가 "assigned files에 근거하라"고 요구해도, assigned file의 관련 line range가 prompt에 없으면 reviewer도 검증할 수 없다.

개선:

1. 파일 앞 60줄 대신 `EvidencePacket` 단위로 주입한다.
2. packet은 `symbol definition range`, `caller/callee neighborhood`, `registration table`, `macro expansion source`, `build flag/include context`, `config/asset binding`을 가진다.
3. 각 packet에는 `path`, `start_line`, `end_line`, `symbol_id`, `extraction_method`, `confidence`, `content_hash`를 넣는다.
4. worker claim은 반드시 packet id를 참조해야 하며, packet 없는 claim은 deterministic verifier에서 `unsupported`로 낮춘다.

### P0. Windows/security semantic sharding이 Unreal 신호에 묶여 있다

`analysis_sharding_semantic.go`에는 `security_driver`, `security_ioctl`, `security_handles`, `security_memory`, `security_rpc` bucket이 있지만, semantic shard 진입 조건인 `hasSemanticShardSignals`는 Unreal metadata 존재 여부만 본다.

근거:
- `cmd/kernforge/analysis_sharding_semantic.go:237`

영향:

1. 순수 Windows driver/anti-cheat/security 프로젝트는 semantic shard planner를 타지 못하고 directory/root shard로 떨어진다.
2. IOCTL, handle filter, memory scanner, service/RPC boundary처럼 정확도에 중요한 경계가 shard objective로 분리되지 않는다.
3. worker가 한 shard 안에서 build, driver init, user-mode client, callback, telemetry를 뭉뚱그리기 쉽다.

개선:

1. `hasSemanticShardSignals`를 `collectSemanticShardSignals` 결과 기반으로 바꾼다.
2. Driver/security path가 2개 이상이거나 high-confidence entrypoint가 있으면 semantic planner를 사용한다.
3. `security_driver`, `security_ioctl`, `security_handles`, `security_memory`, `security_rpc` bucket은 Unreal 없이도 활성화한다.

### P1. C/C++ include 해석이 build context 없이 basename/stem 매칭으로 과확장된다

현재 C/C++ import resolver는 quoted include를 상대 경로로 먼저 보고, 이후 전체 snapshot에서 basename 또는 stem이 같은 모든 파일을 후보로 붙인다.

근거:
- `cmd/kernforge/analysis_project.go:12406`
- `cmd/kernforge/analysis_project.go:16595`

영향:

1. `Config.h`, `Types.h`, `Common.h`처럼 흔한 이름은 다른 module/header까지 연결될 수 있다.
2. 실제 compile include order, `/I`, forced include, preprocessor define, WDK/UE generated include가 반영되지 않는다.
3. 잘못된 include edge가 reference shard와 semantic fingerprint에 들어가면 incremental reuse와 graph retrieval도 같이 흔들린다.

개선:

1. `compile_commands.json`, `.vcxproj`, `.props`, `.targets`, `.Build.cs`, `.Target.cs`에서 include path와 define을 build context로 승격한다.
2. C/C++ resolver는 build context가 있으면 include search order를 그대로 따른다.
3. build context가 없으면 basename fallback을 쓰되 confidence를 `low`로 기록하고, 동일 basename 다중 후보는 worker evidence로 넣지 말고 ambiguity marker로 남긴다.

### P1. 핵심 parser가 정규식/휴리스틱 중심이다

C-style function anchor, Unreal reflection metadata, import discovery는 상당히 보강되어 있지만 여전히 regex/fuzzy scanner 중심이다.

근거:
- `cmd/kernforge/analysis_symbol_anchor.go:270`
- `cmd/kernforge/analysis_symbol_anchor.go:370`
- `cmd/kernforge/analysis_project.go:4214`
- `cmd/kernforge/analysis_project.go:4413`

영향:

1. macro-heavy driver/UE 코드, conditional compilation, generated header, template specialization, overload, namespace alias, nested macro DSL에서 누락/오탐이 계속 발생할 수 있다.
2. parser confidence와 build confidence가 산출물에 충분히 드러나지 않으면 최종 문서가 확정적으로 보인다.

개선:

1. Tree-sitter 기반 structural parser를 1차 계층으로 추가한다.
2. C/C++은 build-aware precise index가 있으면 그것을 우선하고, Tree-sitter는 fallback/structural search용으로 둔다.
3. 기존 regex scanner는 domain-specific detector로 유지하되, `extraction_method=heuristic_regex`와 confidence를 명시한다.

### P1. 모델 reviewer가 기본 single-model 모드에서 skip될 수 있다

dedicated reviewer profile이 없고 root-cause mode가 아니면 `shouldSkipModelReviewerForMode`가 true가 되어 reviewer가 "approved"로 대체된다.

근거:
- `cmd/kernforge/analysis_project.go:6497`

영향:

1. 정확도 문제가 있는 architecture/security map에서 모델 cross-check가 사라진다.
2. `ApprovedShards`가 실제 독립 review 승인처럼 보일 수 있다.
3. 품질 문제는 final synthesis 이후에야 사용자 눈에 드러난다.

개선:

1. 모델 reviewer는 optional로 유지하되 deterministic claim verifier는 항상 실행한다.
2. reviewer skip 상태는 `approved`가 아니라 `model_review_skipped` 또는 `deterministic_verified`처럼 분리한다.
3. final/dashboard에서 "model reviewer skipped"와 "deterministic evidence passed/failed"를 별도 badge로 보여 준다.

### P2. 큰 파일은 조용히 제외된다

기본 `MaxFileBytes`는 512 KiB이고, `scanProject`는 이보다 큰 파일을 조용히 skip한다.

근거:
- `cmd/kernforge/analysis_project.go:1458`
- `cmd/kernforge/analysis_project.go:3266`

영향:

1. generated header, amalgamated source, large table/config, protocol schema가 분석에서 빠져도 coverage gap이 명확히 보이지 않는다.
2. 최종 문서가 "분석되지 않은 중요 파일"을 언급하지 못한다.

개선:

1. skip ledger를 `AnalysisCoverageLedger`로 산출물에 저장한다.
2. 파일 크기 때문에 skip된 파일은 path, size, reason, replacement strategy를 기록한다.
3. 큰 파일은 full read 대신 symbol table, section hash, line-range retrieval, generated artifact relation으로 부분 인덱싱한다.

### P2. shard 크기 정책과 prompt evidence 정책이 맞지 않는다

기본 shard limit은 `250 files / 40000 lines`지만 worker prompt는 실제로 10개 파일의 앞 60줄만 본다.

근거:
- `cmd/kernforge/analysis_project.go:1454`
- `cmd/kernforge/analysis_project.go:1455`
- `cmd/kernforge/analysis_project.go:12912`
- `cmd/kernforge/analysis_project.go:13981`

영향:

1. shard planning은 큰 파일 묶음을 허용하지만, prompt evidence는 극소수 prefix만 포함하므로 shard가 커질수록 blind spot이 커진다.
2. "reference files"가 실제 근거가 아니라 이름 목록처럼 동작한다.

개선:

1. shard budget을 파일/라인 수가 아니라 `evidence packet count`, `symbol coverage`, `call-depth`, `max token budget`으로 관리한다.
2. shard는 "파일 묶음"보다 "graph community + required evidence packet set"으로 만든다.

## 2. 최신 도구/기술 조사 요약

### Tree-sitter

Tree-sitter는 parser generator이자 incremental parsing library이며, source file의 concrete syntax tree를 만들고 edit 이후에도 효율적으로 갱신할 수 있다. 공식 문서 기준으로 C, C++, C#, Go, Python, Rust, TypeScript 등 주요 parser가 upstream에 있다.

Kernforge 적용:

1. regex scanner를 대체하는 structural extraction 계층으로 적합하다.
2. function/class/macro/use-site 후보를 빠르게 뽑아 `EvidencePacket` 후보를 만들 수 있다.
3. 정확한 타입/바인딩은 부족하므로 build-aware precise index의 대체재가 아니라 fallback 및 structural search 계층으로 둔다.

출처: https://tree-sitter.github.io/tree-sitter/

### Sourcegraph SCIP / LSIF 계열

SCIP index는 document, occurrence, symbol information을 통해 go-to-definition, find references 같은 code navigation 기능을 지원한다. Sourcegraph 문서는 compiler frontend 또는 language server를 활용해 semantic analysis 이후 occurrence를 기록하는 접근을 권장한다.

Kernforge 적용:

1. `SemanticIndexV2` schema를 SCIP-compatible subset으로 매핑한다.
2. 가능하면 `scip-go`, `scip-clang`, `scip-typescript`, `scip-dotnet` 결과를 import한다.
3. 내부 index도 `definition`, `reference`, `implementation`, `diagnostic`, `hover/doc` 개념을 맞춰 future export 가능하게 둔다.

출처: https://sourcegraph.com/docs/code-navigation/writing-an-indexer

### clangd index

clangd index는 symbol, ref, relation을 저장하고, background index는 compilation database를 발견하면 source file별 compile command를 queue에 올려 전체 codebase coverage를 만든다. 대형 코드베이스에서는 static/remote index도 지원한다.

Kernforge 적용:

1. C/C++/driver/UE 정확도는 `compile_commands.json` 또는 동등한 build context 유무에 크게 좌우된다.
2. `clangd-indexer` 또는 `scip-clang` adapter를 optional high-confidence source로 둔다.
3. `.vcxproj`/MSBuild/WDK 프로젝트는 compile command 생성기가 필요하다.

출처: https://clangd.llvm.org/design/indexing

### CodeQL

CodeQL database는 특정 시점의 codebase에서 추출한 queryable data이며, AST, data flow graph, control flow graph 표현을 포함한다. CodeQL CLI는 codebase database를 만들고 query 결과를 SARIF 등으로 출력할 수 있다.

Kernforge 적용:

1. security/taint/root-cause mode에서 optional external analyzer로 붙인다.
2. CodeQL query 결과는 final finding이 아니라 high-confidence evidence packet으로 가져온다.
3. C/C++ compiled language는 build extraction 부담이 있으므로 "available when buildable"로 둔다.

출처:
- https://codeql.github.com/docs/codeql-overview/about-codeql/
- https://docs.github.com/en/code-security/concepts/code-scanning/codeql/about-the-codeql-cli

### Semgrep taint/dataflow

Semgrep taint mode는 source, propagator, sanitizer, sink를 rule로 정의하고 taint trace를 제공한다. Pro 모드에서는 interprocedural/interfile analysis를 지원한다.

Kernforge 적용:

1. anti-cheat/driver/user-mode telemetry에서 "untrusted input -> privileged sink" 후보를 빠르게 찾는 rule adapter로 쓴다.
2. IOCTL buffer, IPC payload, config-driven command, script/plugin input 같은 surface를 source로 모델링한다.
3. 결과는 `SecuritySurfaceEvidence`로 import해 LLM worker가 trace를 인용하게 한다.

출처: https://semgrep.dev/docs/writing-rules/data-flow/taint-mode/overview

### Joern / Code Property Graph

Joern은 C/C++ code를 syntax, control-flow, data-flow, type information을 함께 담은 code property graph로 만들고 graph traversal query로 mining한다.

Kernforge 적용:

1. 전체 프로젝트 기본 인덱서로 쓰기엔 무겁지만, 고위험 C/C++ 경로에서 slicing/taint/path exploration에 적합하다.
2. memory safety, bounds, lifecycle, trust boundary 분석 mode에서 selective adapter로 둔다.

출처: https://joern.readthedocs.io/en/stable/index.html

### LSP / LSIF

LSP는 editor/IDE와 language server가 go-to-definition, find references, hover 같은 language feature를 표준 프로토콜로 주고받게 한다. LSIF는 local source copy 없이 rich code navigation을 지원하기 위한 graph format이다.

Kernforge 적용:

1. language server가 설치된 환경에서는 low-friction adapter로 `documentSymbol`, `workspaceSymbol`, definition/reference를 얻는다.
2. LSP 응답은 line-accurate evidence packet seed로 쓰고, unsupported server는 graceful fallback한다.

출처: https://microsoft.github.io/language-server-protocol/

### ast-grep

ast-grep은 AST 기반 structural search/rewrite 도구이며 tree-sitter parser를 활용한다. 대규모 코드에서 syntax-aware pattern matching과 lint/rewrite를 지원한다.

Kernforge 적용:

1. domain detector를 regex에서 AST pattern으로 옮길 때 좋은 실행 도구다.
2. "IRP dispatch assignment", "ObRegisterCallbacks", "UFUNCTION(Server)", "DeviceIoControl wrapper" 같은 패턴을 structural rule로 만들 수 있다.

출처: https://ast-grep.github.io/

### OpenGrok / ctags 계열

OpenGrok은 source code search와 cross-reference engine이며 Universal Ctags에 의존한다. 빠른 검색/탐색에는 강하지만 semantic truth source로 쓰기에는 compiler-aware index보다 약하다.

Kernforge 적용:

1. fast fallback search index로는 가치가 있다.
2. definition/reference의 confidence는 `tag_index` 수준으로 낮게 두고, precise index와 충돌하면 precise index를 우선한다.

출처: https://oracle.github.io/opengrok/

### 최근 연구 흐름: persistent code graph + LLM

2026년 연구 흐름도 "LLM이 repo를 grep으로 계속 탐색"하는 방식보다 persistent code graph를 만든 뒤 graph-native query로 evidence를 회수하는 방향이다. Codebase-Memory는 Tree-sitter 기반 knowledge graph와 MCP를 결합해 token/tool call을 줄이는 접근을 제시했고, RepoDoc은 repository knowledge graph를 documentation lifecycle의 semantic foundation으로 두는 방식을 제안했다.

Kernforge 적용:

1. `.kernforge/analysis/latest`를 단순 문서 mirror가 아니라 persistent project graph cache로 강화한다.
2. docs generation도 flat chunk 요약이 아니라 graph query 결과에서 생성한다.

출처:
- https://arxiv.org/abs/2603.27277
- https://arxiv.org/abs/2604.26523

## 3. 목표 아키텍처

추천 아키텍처:

`Coverage Ledger -> Build Context -> Parser/Indexer Adapters -> Unified Evidence Graph -> Graph-Guided Retrieval -> LLM Worker/Reviewer -> Deterministic Claim Verification -> Docs/Dashboard`

### 3.1 Coverage Ledger

새 산출물:

1. `.kernforge/analysis/latest/coverage_ledger.json`
2. run별 `<run>_coverage_ledger.json`

기록 항목:

1. scanned file count, skipped file count, skipped bytes
2. skip reason: size, binary, excluded dir, generated noise, unreadable, parse error
3. parser success/failure by language
4. build context availability: compile database, vcxproj, uproject, go.mod, package manager
5. external tool availability: tree-sitter, clangd/scip, CodeQL, Semgrep, Joern
6. confidence downgrade reasons

운영 효과:

분석 결과가 틀렸을 때 "모델이 멍청했다"가 아니라 "어떤 evidence가 없어서 틀렸는지"를 바로 볼 수 있다.

### 3.2 Build Context Layer

확장 대상:

1. `BuildContextRecord`
2. `CompilationCommandRecord`
3. `ProjectSnapshot.BuildContexts`

추가 필드:

1. `SourceKind`: `compile_commands`, `vcxproj`, `props`, `targets`, `ubt`, `go`, `npm`, `manual`
2. `Confidence`: `high|medium|low`
3. `IncludeResolutionOrder`
4. `EffectiveDefines`
5. `ForcedIncludes`
6. `GeneratedInputs`
7. `GeneratedOutputs`
8. `Toolchain`: MSVC, ClangCL, WDK, UBT, Go, dotnet

Windows/driver 특화:

1. `.vcxproj`의 `ConfigurationType=Driver` / `DriverType` / WDK props를 build fact로 기록한다.
2. `.inf`, service install path, `.sys` output, signing/test-signing readiness를 build artifact graph에 연결한다.
3. IOCTL header, shared user/kernel ABI header는 boundary-critical source로 승격한다.

UE 특화:

1. `.uproject`, `.uplugin`, `.Build.cs`, `.Target.cs`를 build graph로 연결한다.
2. UHT generated header와 original header mapping을 저장한다.
3. generated code가 없으면 "missing generated artifact" coverage marker를 남긴다.

### 3.3 Parser/Indexer Adapter Layer

공통 interface:

```go
type AnalysisIndexAdapter interface {
    Name() string
    Detect(ctx context.Context, snapshot ProjectSnapshot) AnalysisAdapterDetection
    Build(ctx context.Context, snapshot ProjectSnapshot, buildContexts []BuildContextRecord) (AnalysisAdapterResult, error)
}
```

adapter 종류:

1. `internal_tree_sitter`: structural symbols, AST pattern hits, syntax errors
2. `internal_regex_domain`: 기존 driver/UE/security heuristic scanner
3. `scip_import`: external `index.scip` import
4. `scip_generate`: known indexer 실행, 예: `scip-go`, `scip-clang`
5. `clangd_index`: C/C++ symbol/ref/relation import
6. `lsp_live`: installed language server에서 symbol/reference query
7. `codeql`: CodeQL database/query/SARIF import
8. `semgrep`: taint/security rule trace import
9. `joern_cpg`: selective CPG slice/taint import
10. `ctags_opengrok`: fast fallback tags/search import

중요 원칙:

1. external tool failure는 analyze-project failure가 아니다.
2. 실패는 coverage ledger와 dashboard에 남기고 internal parser로 fallback한다.
3. 서로 충돌하는 edge는 confidence ranking으로 해결한다.

confidence ranking:

1. `compiler_or_language_server`: high
2. `codeql_or_cpg`: high for supported flow facts
3. `tree_sitter_structural`: medium
4. `ast_pattern_domain`: medium
5. `regex_domain`: medium/low
6. `ctags_or_text_search`: low
7. `llm_inference`: inference only, never fact by itself

### 3.4 Unified Evidence Graph

현재 `SemanticIndexV2`를 유지하되 다음 edge를 추가한다.

1. `ContainmentEdge`: file -> class -> method -> local function/block
2. `MacroExpansionEdge`: macro definition -> invocation -> expanded registration candidate
3. `IncludeResolutionEdge`: source -> included file with build context/confidence
4. `DataFlowEdge`: source -> propagator -> sanitizer -> sink
5. `ControlFlowEdge`: dispatcher -> branch -> handler
6. `BoundaryEdge`: user -> kernel, untrusted -> privileged, config -> runtime
7. `GeneratedFromEdge`: source declaration -> generated artifact
8. `RuntimeActivationEdge`: initialization -> explicit start/register path

Windows/security overlay:

1. IRP major function registration
2. IOCTL code decode and handler dispatch
3. user-mode DeviceIoControl wrapper to kernel-side dispatcher
4. Ob/Ps/Flt/WFP/registry callback registration
5. handle duplication/open/process/thread/image callback path
6. memory read/write/scan path and validation gates
7. telemetry sink and persistence boundary

UE overlay:

1. UCLASS/USTRUCT/UENUM/UINTERFACE symbol facts
2. UFUNCTION RPC direction and validation
3. replicated property and RepNotify path
4. Gameplay Ability System ownership
5. Enhanced Input action/context binding
6. asset/config soft reference and map/game mode defaults

### 3.5 Graph-Guided Evidence Retrieval

기존 방식:

1. shard primary file list 생성
2. related file list 생성
3. 각 파일 앞 60줄 주입

새 방식:

1. question/mode/goal에서 target intent를 분류한다.
2. intent별 seed symbol/path를 고른다.
3. seed 주변 graph neighborhood를 확장한다.
4. 각 edge를 설명하는 최소 line-range packet을 가져온다.
5. packet budget 안에서 `required packet`, `supporting packet`, `ambiguous packet`, `coverage gap packet`으로 나눈다.
6. worker prompt에는 file prefix가 아니라 packet table과 exact source slices를 넣는다.

retrieval mode별 packet 우선순위:

1. `map`: top directory facts, build contexts, containment, critical anchors
2. `trace`: entrypoint, dispatcher, caller/callee, control-flow branch
3. `impact`: changed symbol neighborhood, reverse references, generated/build dependency
4. `surface`: untrusted inputs, privileged sinks, validation/sanitizer, telemetry output
5. `security`: taint/dataflow, boundary edges, callback/IOCTL/memory/handle overlays
6. `performance`: hot loops, large call fanout, blocking IO, startup path

### 3.6 Worker/Reviewer Contract

worker 입력:

1. `Analysis mode`
2. `Shard objective`
3. `Evidence packet index`
4. `Exact source slices`
5. `Coverage gaps`
6. `Allowed inference policy`

worker 출력 변경:

1. 모든 `claims[]`에 `evidence_packet_ids` 추가
2. `unsupported_needed_evidence[]` 추가
3. `confidence_breakdown`은 evidence strength와 graph coverage를 분리

reviewer 변경:

1. 모델 reviewer는 optional
2. deterministic verifier는 mandatory
3. verifier는 claim의 packet id, line range, symbol id, confidence를 검증
4. unsupported claim은 final synthesis에서 fact로 올라가지 못한다

### 3.7 Docs/Dashboard 변경

추가 산출물:

1. `EVIDENCE_GRAPH.md`
2. `COVERAGE_LEDGER.md`
3. `TOOL_ADAPTER_STATUS.md`
4. `UNSUPPORTED_CLAIMS.md`
5. `ANALYSIS_CONFIDENCE.md`

dashboard 추가 widget:

1. parser coverage by language
2. skipped large files
3. external adapter status
4. high-confidence vs heuristic edge count
5. unsupported claim count
6. stale/invalidation edge count
7. evidence packet truncation warnings

## 4. 구현 단계

### Phase 0. 현재 정확도 계측

목표:

현 상태를 먼저 수치화한다.

작업:

1. `analysis_accuracy_eval.go` 추가
2. golden question set 추가
3. output evaluator에 다음 metric 추가
   - exact anchor accuracy
   - flow ordering accuracy
   - unsupported claim count
   - top-level directory closed set violation
   - security boundary separation
   - source coverage ratio
4. SampleKernelDriver-like, UE fixture, Go multi-package fixture, mixed user/kernel fixture를 만든다.

완료 기준:

1. 현재 baseline 점수와 failure examples가 `.kernforge/test-logs/analysis_accuracy_baseline.json`로 저장된다.
2. 정확도 개선 PR마다 같은 evaluator를 돌릴 수 있다.

### Phase 1. 외부 의존성 없이 즉시 개선

목표:

가장 큰 정확도 병목을 먼저 없앤다.

작업:

1. `buildFileContext`를 `buildEvidencePacketsForShard`로 대체한다.
2. source anchor line range를 사용해 관련 함수 body를 넣는다.
3. `hasSemanticShardSignals`를 driver/security 신호 기반으로 수정한다.
4. 큰 파일/skip file coverage ledger를 추가한다.
5. model reviewer skip 상태를 `approved`와 분리한다.

완료 기준:

1. long C++ file fixture에서 파일 뒤쪽 handler가 worker prompt에 포함된다.
2. non-Unreal driver fixture가 `security_driver`/`security_ioctl` shard를 만든다.
3. skip된 큰 파일이 dashboard와 manifest에 표시된다.

### Phase 2. Tree-sitter structural index

목표:

regex parser의 구조적 누락을 줄인다.

작업:

1. Go binding으로 Tree-sitter parser adapter 추가
2. C/C++/C#/Go/TS/Python 최소 symbol extraction 구현
3. AST query로 domain pattern detector 작성
4. syntax error와 partial parse coverage를 ledger에 기록

완료 기준:

1. regex scanner와 tree-sitter scanner의 symbol diff report가 생성된다.
2. C++ class/method/function symbol range가 line-accurate packet으로 들어간다.

### Phase 3. build-aware precise index adapter

목표:

C/C++/Go/C#/TS의 definition/reference 정확도를 외부 indexer와 연결한다.

작업:

1. `scip_import` adapter 추가
2. `scip-go`/`scip-clang`/`scip-dotnet` detection과 optional execution
3. `compile_commands.json` discovery와 `.vcxproj` to compile command generator 초안
4. clangd static index 또는 LSP live query fallback 검토

완료 기준:

1. SCIP occurrence가 `SemanticIndexV2` symbol/ref로 import된다.
2. include edge confidence가 build-aware resolver 결과에 따라 달라진다.
3. duplicate basename header fixture에서 잘못된 include edge가 high-confidence로 올라가지 않는다.

### Phase 4. Security analyzer adapters

목표:

security/surface/root-cause mode의 flow 정확도를 올린다.

작업:

1. Semgrep rule pack for Kernforge 추가
2. CodeQL database detection/import 추가
3. Joern selective slice adapter 실험
4. IOCTL/user-kernel taint trace schema 추가

완료 기준:

1. IOCTL input -> validation -> privileged handler trace가 evidence graph로 들어간다.
2. analyzer unavailable 상태에서도 internal detector로 degraded result를 만든다.

### Phase 5. Graph-guided sharding/retrieval

목표:

directory/file chunking을 graph community 기반으로 바꾼다.

작업:

1. `AnalysisShard`에 `SeedSymbols`, `RequiredPacketIDs`, `GraphNeighborhood` 추가
2. mode별 graph expansion 정책 추가
3. packet budget allocator 추가
4. coverage gap shard도 file 기준이 아니라 missing evidence class 기준으로 생성

완료 기준:

1. startup/IOCTL/callback/asset-config path가 각자 별도 evidence packet set으로 분석된다.
2. worker가 "context-truncated"를 남발하지 않는다.

### Phase 6. Deterministic claim verifier

목표:

LLM 산출물의 사실 주장과 근거를 기계적으로 검사한다.

작업:

1. claim -> packet id mandatory check
2. cited symbol name과 packet symbol 일치 검사
3. line range와 exact anchor location 검사
4. flow step이 graph edge로 연결되는지 검사
5. boundary invariant violation 검사

완료 기준:

1. line/symbol 오라벨링은 blocking violation이 된다.
2. unsupported high-confidence claim은 final doc에서 fact로 합성되지 않는다.
3. final report에 `verified facts`, `inferences`, `unsupported claims`가 분리된다.

## 5. 우선순위 추천

가장 먼저 할 일:

1. `buildFileContext`를 symbol-aware evidence packet으로 바꾼다.
2. Windows/security semantic sharding 활성화 조건을 고친다.
3. coverage ledger를 추가한다.
4. model reviewer skip과 deterministic verification status를 분리한다.

이 4개는 외부 tool 설치 없이 바로 정확도 체감이 크다.

그 다음:

1. Tree-sitter adapter
2. SCIP import
3. compile_commands/vcxproj build context 강화
4. Semgrep/CodeQL/Joern optional adapter

## 6. Acceptance Criteria

정확도:

1. known driver fixture에서 exact anchor accuracy 95% 이상
2. IOCTL/control-open/device-control/callback registration flow separation violation 0건
3. top-level directory closed set violation 0건
4. unsupported high-confidence claim 0건

coverage:

1. skipped files가 100% ledger에 기록됨
2. parser/tool failure가 dashboard에 표시됨
3. shard별 source packet coverage가 manifest에 기록됨

운영성:

1. external adapter failure는 graceful degradation
2. Windows offline 환경에서도 internal parser path로 동작
3. 대형 repo에서도 incremental cache가 유지됨
4. long-running run은 progress ledger에 adapter/build/retrieval stage를 표시

## 7. 위험과 tradeoff

### 외부 도구 의존성

장점:

1. definition/reference/dataflow 정확도가 크게 오른다.
2. 유명 도구의 검증된 extraction을 재사용한다.

단점:

1. 설치/버전/라이선스/성능/빌드 환경 문제가 생긴다.
2. Windows WDK/UE project는 build context 생성이 어렵다.

판단:

외부 도구는 optional adapter로 두고, internal parser와 coverage ledger를 기본 신뢰 경로로 둔다.

### Tree-sitter 전면 도입

장점:

1. regex보다 구조 안정성이 높다.
2. 빠르고 incremental cache에 적합하다.

단점:

1. 타입/바인딩/전처리 정확도는 compiler frontend보다 낮다.
2. C++ macro/conditional 의미는 여전히 별도 처리가 필요하다.

판단:

Tree-sitter는 "structural extraction and slicing"의 기본 계층으로 쓰고, semantic truth는 SCIP/clangd/CodeQL/CPG에서 보강한다.

### graph-first 설계

장점:

1. LLM hallucination을 줄인다.
2. incremental invalidation과 QA 재사용이 정확해진다.
3. docs, dashboard, root-cause, security mode가 같은 evidence graph를 공유한다.

단점:

1. schema migration 비용이 크다.
2. 잘못된 graph edge가 high-confidence로 올라가면 전체 synthesis가 흔들린다.

판단:

edge마다 `source_adapter`, `confidence`, `evidence_packet_ids`, `invalidation_hash`를 필수화해 운영 리스크를 줄인다.

## 8. 다음 구현 작업 제안

1. `AnalysisCoverageLedger` 타입과 산출물 저장 경로 추가
2. `EvidencePacket` 타입 추가
3. `buildEvidencePacketsForShard` 구현
4. `buildWorkerPrompt`에서 file prefix context 제거
5. `hasSemanticShardSignals` 수정
6. driver/security semantic shard fixture 추가
7. long C++ file evidence packet fixture 추가
8. model reviewer skip status 분리

이 순서가 가장 현실적이다. 외부 indexer 연동은 정확도 기반을 먼저 세운 뒤 붙여야 regression 원인을 분리하기 쉽다.

## 9. Phase 1 구현 상태

2026-05-31 기준으로 Phase 1의 내부 parser 기반 정확도 개선은 다음 항목까지 반영했다.

1. `AnalysisCoverageLedger`를 snapshot/run에 추가하고 run별 `_coverage_ledger.json` 및 `latest/coverage_ledger.json`로 저장한다.
2. `EvidencePacket`를 추가하고 run별 `_evidence_packets.json` 및 `latest/evidence_packets.json`로 저장한다.
3. worker/reviewer prompt가 `buildEvidencePacketsForShard`의 symbol-aware source slice를 우선 사용한다.
4. `AnalysisClaim`에 `evidence_packet_ids`를 추가하고, high-confidence claim이 packet id 없이 남으면 deterministic verifier가 confidence를 낮춘다.
5. non-Unreal Windows/security 프로젝트에서도 security semantic shard 신호를 수집한다.
6. dedicated reviewer가 없는 single-model run은 `approved`가 아니라 `model_review_skipped`로 집계한다.
7. `COVERAGE_LEDGER.md`, `EVIDENCE_PACKETS.md`, dashboard skipped-file/evidence-packet metric을 추가했다.
8. long C++ file 후반 handler, oversized file ledger, security shard 분해, unsupported high-confidence claim downgrade 회귀 테스트를 추가했다.

남은 범위는 external adapter 계층(Tree-sitter/SCIP/clangd/CodeQL/Semgrep/Joern), packet coverage ratio score, graph-edge-level verifier, cross-tool failure dashboard를 Phase 2로 붙이는 일이다.
