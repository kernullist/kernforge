# Kernforge Roadmap For Windows Security And Anti-Cheat

이 문서는 현재 Kernforge 구현 상태를 바탕으로 Claude Code 및 Codex와 비교했을 때, 따라가야 하는 범용 기능과 우리만의 강점으로 키워야 하는 기능을 함께 정리한 제품/구현 로드맵이다.

기준 시점:
- 코드베이스 기준: 2026-04-24
- 외부 비교 기준:
  - Claude Code 공식 문서
  - OpenAI Codex 공식 도움말 및 공개 저장소

## 0. 2026-04 방향 재정렬

Kernforge의 다음 발전 방향은 크게 두 축으로 잡는다.

1. 전체 프로젝트 분석 및 문서화
2. 퍼징 전문 도구

이 재정렬은 현재 구현 상태와 잘 맞는다. 코드베이스에는 이미 `multi-agent project analysis`, deterministic documentation writer, analysis dashboard, `structural_index_v2`, Unreal semantic graph, vector corpus, adaptive verification, evidence store, persistent memory, `/fuzz-func` 기반 source-level fuzzing이 들어가 있다. 따라서 다음 단계는 기능을 흩뿌려 늘리는 것이 아니라, 이 두 축을 제품의 중심 경험으로 고정하고 나머지 기능을 보조 계층으로 재배치하는 것이다.

권장 제품 포지션:
- 대형 Windows security / anti-cheat / Unreal 프로젝트를 먼저 이해하고 문서화하는 분석 에이전트
- 소스 기반 triage에서 native fuzzing 실행까지 이어지는 보안 퍼징 워크벤치
- 분석 산출물, fuzz finding, verification, evidence, memory를 한 루프로 묶는 로컬 우선 security engineering runtime

핵심 원칙:
1. `analyze-project`는 일회성 요약이 아니라 프로젝트 지식 베이스를 만드는 기능으로 키운다.
2. `/fuzz-func`는 단일 명령 기능이 아니라 입력 표면 발굴, harness 생성, corpus 관리, crash triage까지 포함하는 전문 도구로 키운다.
3. hooks, specialist, worktree, verification, evidence, desktop shell은 두 축을 더 안전하고 반복 가능하게 만드는 보조 인프라로 둔다.

## 1. 현재 Kernforge의 실제 강점

현재 코드베이스에서 이미 경쟁력이 있는 축은 다음과 같다.

1. 전체 프로젝트 분석 및 문서화
- `/analyze-project [--docs] [--path <dir>] [--mode map|trace|impact|surface|security|performance]`
- `/docs-refresh`
- `/analyze-dashboard`
- conductor/worker/reviewer 기반 multi-agent analysis
- knowledge pack, final document, shard document, performance lens
- `.kernforge/analysis/latest` 아래 deterministic docs, docs manifest, docs index, dashboard
- `ARCHITECTURE.md`, `SECURITY_SURFACE.md`, `API_AND_ENTRYPOINTS.md`, `BUILD_AND_ARTIFACTS.md`, `VERIFICATION_MATRIX.md`, `FUZZ_TARGETS.md`, `OPERATIONS_RUNBOOK.md`
- 문서별 source anchor, section metadata, confidence, stale/invalidation marker, reuse target
- `structural_index_v2`, symbol anchor, call edge, build ownership edge
- Unreal project/module/type/network/asset/config semantic graph
- vector corpus와 ingestion manifest

2. 소스 레벨 Function Fuzzing
- `/fuzz-func <function-name>`
- `/fuzz-func --file <path>` 및 `/fuzz-func @<path>`
- source-only attack input modeling
- guard/probe/copy/dispatch/cleanup observation 추출
- branch predicate, 최소 반례, pass/fail outcome, downstream call chain 요약
- build context가 충분할 때 native fuzzing 실행 계획과 corpus/crash directory 준비

3. 안전한 편집 루프
- diff preview
- selection-aware preview
- 자동 verification
- 자동 checkpoint
- rollback

4. 세션을 넘는 누적 맥락
- persistent memory
- analysis docs reuse memory record
- analysis docs evidence record
- verification history
- evidence dashboard
- memory dashboard
- verification dashboard

5. Windows 친화 UX
- 별도 viewer 창
- selection-first review/edit 흐름
- Windows 입력/취소 처리
- 짧은 `Esc` 탭 취소와 취소 직후 프롬프트 안정화까지 포함한 콘솔 취소 신뢰성

6. planner/reviewer 분리 구조
- `/do-plan-review`
- reviewer 모델 별도 구성

7. 확장 가능성
- local skill
- MCP tool/resource/prompt

핵심 해석:
- Kernforge는 이미 "코드를 잘 쓰는 에이전트"라기보다 "큰 보안 코드베이스를 이해하고, 근거를 남기며, 위험한 변경을 검증하는 에이전트" 쪽에서 강점이 있다.
- 이 강점은 Windows security/anti-cheat 워크플로우와 매우 잘 맞는다.

## 2. Claude Code / Codex 대비 현재 비교

### Claude Code가 강한 축

1. hooks
- 세션 시작, 사용자 프롬프트 제출, tool 실행 전후, subagent 종료 같은 이벤트에 정책을 붙일 수 있다.

2. subagents
- 독립 context, 도구, 권한을 가진 전문 agent를 분리해 쓸 수 있다.

3. agent teams / 병렬 위임
- 긴 작업을 병렬화하기 쉽다.

4. 광범위한 외부 연결
- MCP를 통해 다양한 외부 시스템 연결이 자연스럽다.

### Codex가 강한 축

1. 승인/샌드박스/워크트리 흐름
- 승인 정책과 로컬 작업 모델이 강하다.

2. 병렬 agent와 cloud delegation
- 로컬 페어링과 클라우드 위임을 함께 가져간다.

3. automations
- 반복 작업을 agent 워크플로우로 실행할 수 있다.

4. GitHub 연동형 코드 리뷰
- PR 자동 리뷰 흐름으로 연결된다.

### Kernforge가 이미 더 좋은 축

1. multi-agent project analysis가 단순 요약을 넘어 재사용 가능한 문서와 인덱스를 생성함
2. `structural_index_v2`, Unreal semantic graph, vector corpus까지 한 분석 실행에서 함께 남김
3. `/fuzz-func`가 source-only fuzz triage, harness artifact, native execution readiness를 하나의 흐름으로 묶음
4. checkpoint + rollback가 기본 흐름에 잘 녹아 있음
5. verification history와 adaptive verification이 제품 중심 기능으로 존재함
6. selection-first edit/review UX가 명확함
7. persistent memory에 trust/importance 개념이 이미 있음

### Kernforge가 아직 비어 있는 축

1. analysis dashboard는 rich document portal, cross-doc search, evidence drill-down, trust-boundary/attack-flow MVP, docs-backed vector corpus 재수집까지 갖췄지만 아직 graph UX의 상호작용성은 부족함
2. generated docs는 API map, security surface, verification matrix, fuzz target catalog, trust/data-flow graph section을 생성하고, 변경 diff view를 graph section stale marker와 연결함
3. `/fuzz-func`는 강한 source-level triage와 docs catalog ranking을 갖췄지만 coverage-guided fuzzing, corpus lifecycle, crash minimization, sanitizer/coverage report까지 이어지는 전문 워크벤치는 아직 부족함
4. fuzz finding과 evidence graph, verification history, tracked feature를 하나의 issue lifecycle로 묶는 MVP가 들어갔고, coverage feedback과 dedup도 1차 구현됨. 다음은 coverage report format 확장이 필요함
5. automation/scheduler 부재
6. cloud delegation 부재
7. GitHub/PR review automation 부재

### 대화형 에이전트 관점 비교

이 비교는 단순히 "코드를 수정할 수 있는가"가 아니라, 사용자의 직전 작업과 흐름을 이해하고 스스로 다음 행동을 제안하는 대화형 agent runtime 관점에서 본다.

요약:
- Claude Code는 hook, subagent, external integration을 통해 "사용자/조직이 정책과 전문 agent를 구성하는 능력"이 강하다.
- Codex는 local workspace, approval/sandbox, tool loop, 병렬 delegation, cloud/task handoff가 잘 결합되어 "작업을 실제로 끝까지 몰고 가는 페어 프로그래머" 경험이 강하다.
- Kernforge는 최근 `ConversationEventLog`, `ActiveConversationState`, `RecentErrorResolver`, `SituationSnapshot`, `SuggestionMemory`, `/suggest`를 추가하면서 "프롬프트마다 끊기는 CLI"에서 "현재 상황을 기억하고 다음 행동을 제안하는 보안 엔지니어링 agent" 쪽으로 올라왔다.
- 다만 Claude/Codex가 가진 범용 생태계, cloud delegation, GitHub/PR 자동화, 자연스러운 장기 작업 orchestration은 아직 약하다.

| 항목 | Claude Code | Codex | 현재 Kernforge | 해석 |
|---|---:|---:|---:|---|
| 최근 대화/상황 grounding | 5 | 5 | 4 | `ConversationEventLog`와 `ActiveConversationState`로 직전 오류, tool result, handoff, provider/model을 보존한다. 아직 장기 작업 전환과 multi-session continuity는 더 강화해야 한다. |
| "방금 에러" 같은 지시어 이해 | 5 | 5 | 4 | `RecentErrorResolver`가 provider/tool/command error를 직접 찾아 답한다. 여러 오류 후보가 있으면 가장 가까운 오류를 설명하면서 다른 후보의 kind/source/model/shard/signature도 함께 보여준다. |
| 현재 작업 이어가기 | 5 | 5 | 4 | pending handoff, compact working memory, open artifact 보존이 들어갔다. self-driving loop와 suggestion task node 연결로 현재 작업의 다음 실행 후보를 TaskGraph에 남긴다. |
| 스스로 다음 행동 제안 | 4 | 5 | 4 | `SituationSnapshot`과 `ProactiveSuggestionEngine`이 verification gap, stale docs, fuzz gap, provider 429, dirty worktree, recurring verification, PR review automation을 제안한다. 기본 답변 자동 노출은 과잉 제안을 피하려 provider-blocking 위주로 제한했다. |
| 제안 반복/거절 기억 | 3 | 4 | 4 | `SuggestionMemory`가 shown/accepted/dismissed/executed/cooldown을 session JSON에 보존하고, accepted/dismissed 선호는 persistent memory에도 승격한다. |
| 작업 계획과 실행 루프 | 4 | 5 | 4 | 일반 구현/수정/실행 요청에서 `SelfDrivingWorkLoop`가 task state와 task graph를 자동 시드하고 inspect -> implement -> verify -> summarize 루프를 system prompt와 종료 조건에 주입한다. 복잡한 작업은 기존 planner/reviewer preflight를 우선 사용한다. |
| tool 사용 안정성 | 4 | 5 | 4 | Windows shell guard, diff preview, edit approval, verification, checkpoint가 강하다. 다만 tool failure recovery와 model loop self-correction은 Codex가 더 부드럽다. |
| 병렬/전문 agent 운용 | 5 | 5 | 4 | project analysis conductor/worker/reviewer와 specialist/worktree가 있다. 범용 subagent delegation UX와 병렬 cloud 작업은 Claude/Codex가 앞선다. |
| 검증/증거 기반 판단 | 3 | 4 | 5 | Kernforge의 verification history, evidence store, analysis docs, fuzz findings 결합은 보안 작업에서는 오히려 강점이다. |
| 보안/Windows/anti-cheat 도메인 감도 | 2 | 3 | 5 | Kernforge는 IOCTL, ETW, memory scanning, Unreal, driver/build/signing/fuzz workflow에 맞춘 판단 기준을 제품 중심에 둔다. |
| 외부 시스템 연동 | 5 | 4 | 3 | MCP는 있으나 GitHub/PR, issue tracker, cloud automation은 아직 얇다. 다만 로컬 PR review report automation MVP는 들어갔다. |
| 자동화/스케줄링 | 3 | 5 | 3 | `/automation`으로 recurring verification과 PR review automation slot을 session에 저장하고 수동 실행할 수 있다. 실제 시간 기반 scheduler, GitHub API 연동, cloud recurring job은 다음 단계다. |
| UX polish | 4 | 5 | 3 | Kernforge CLI/Windows viewer는 실용적이지만, Codex급 desktop/app experience와 thread-level 시각화는 아직 부족하다. |

대화형 agent 능력만 놓고 본 현재 위치:
1. Kernforge는 "상황 기억"과 "최근 오류 grounding"에서는 Claude/Codex의 하위 호환 수준까지 올라왔다.
2. "스스로 판단하고 제안"은 1차 구현이 들어갔지만, 아직 rule-based detector 중심이다. 이 선택은 보안 엔지니어링에서는 장점이다. 이유와 근거가 명확하고 테스트 가능하기 때문이다.
3. Codex와의 가장 큰 차이였던 "작업을 맡기면 스스로 전체 루프를 굴리는 자연스러움"은 1차 보강이 들어갔다. 일반 구현/수정 요청은 이제 self-driving work loop로 task graph를 만들고, 도구 사용, 자동 검증, 실패 복구, 최종 요약을 하나의 흐름으로 묶는다.
4. Claude와의 가장 큰 차이는 "사용자 정의 subagent/hook 생태계의 범용성"이다. Kernforge는 보안/Windows 전문성은 강하지만, 조직별 agent 팀과 외부 시스템 연결은 더 얇다.

Kernforge가 차별화해야 할 방향:
1. Codex를 그대로 복제하기보다 "evidence-backed security engineering agent"로 간다.
2. 제안은 LLM 추측이 아니라 `SituationSnapshot`에 근거한 rule/data driven 판단으로 시작하고, LLM은 설명과 우선순위 조율에만 쓴다.
3. verification, fuzz finding, analysis docs stale marker, evidence gap을 "대화 중 자연스러운 next step"으로 계속 승격한다.
4. Windows security 작업에서는 "자동 실행"보다 "위험도와 근거가 붙은 확인 가능한 제안"이 더 중요하다.
5. 장기적으로 `/suggest` 결과를 dashboard, task graph, feature lifecycle, automation scheduler와 연결해 "현재 상황 -> 추천 행동 -> 실행 -> 검증 -> evidence 기록" 루프를 완성한다.

다음 보강 우선순위:
1. 완료: `SuggestionDashboard`를 analysis/verification/evidence dashboard와 통합했다. `/suggest-dashboard-html`은 integrated signals, related dashboard command chips, evidence refs, accept/dismiss command chips를 함께 보여준다.
2. 완료: `AutonomyMode=confirm`에서 `/suggest accept <id>` 시 safe slash command dispatcher가 허용된 명령을 실행하고 suggestion을 `executed`로 전환한다.
3. 완료: dismissed/accepted suggestion을 persistent memory로 승격해 session을 넘는 사용자 선호를 학습한다.
4. 완료: 여러 background job 또는 subagent가 동시에 실패했을 때 recent error resolver가 후보의 kind/source/model/shard/signature를 비교 설명한다.
5. 완료: 일반 구현/수정/실행 요청을 `SelfDrivingWorkLoop`로 승격해 task graph와 자동 verification/final answer review 종료 조건에 연결했다.
6. 완료: `/suggest` 후보와 accepted/dismissed/executed 상태를 `TaskGraph`의 `suggest:<id>` node로 동기화한다.
7. 완료: `/automation`과 `/review-pr` MVP를 추가해 recurring verification slot, PR review report generation, suggestion accept -> automation 등록 흐름을 연결했다.

## 3. 제품 방향 추천

권장 방향은 "Claude Code + Codex의 범용 기능을 뒤쫓는 제품"이 아니라 아래 포지션이다.

추천 포지션:
- project intelligence and documentation engine for large security codebases
- source-to-native fuzzing workbench for Windows security and anti-cheat targets
- safe modification plus live telemetry plus evidence memory runtime

즉, 따라가야 하는 범용 기능은 최소한으로 확보하되, 차별화는 아래 두 축에 둔다.

제품 축 A: 전체 프로젝트 분석 및 문서화
1. architecture map
2. security surface map
3. API/entrypoint catalog
4. build/module ownership map
5. Unreal semantic map
6. verification matrix
7. fuzz target catalog
8. vector-ready knowledge base

제품 축 B: 퍼징 전문 도구
1. source-level fuzz target discovery
2. attack input state modeling
3. branch predicate and counterexample extraction
4. harness generation
5. native coverage-guided execution
6. corpus/crash lifecycle
7. crash triage and minimization
8. evidence-backed finding lifecycle

이를 떠받치는 공통 인프라는 아래와 같다.

1. 정책성 hook
2. 보안/윈도우 전문 subagent
3. evidence graph memory
4. live target telemetry workflow
5. security-aware verification planner
6. incident replay bundle

## 4. 제안 기능 우선순위

### P0. Project Intelligence And Documentation Platform

목표:
- `analyze-project`를 "큰 프로젝트 요약 명령"에서 "프로젝트 지식 베이스와 운영 문서를 생성하는 플랫폼"으로 확장한다.

현재 구현 상태:
1. 완료: analysis run에서 deterministic documentation writer가 실행된다.
2. 완료: `.kernforge/analysis/latest/docs`에 `ARCHITECTURE.md`, `SECURITY_SURFACE.md`, `API_AND_ENTRYPOINTS.md`, `BUILD_AND_ARTIFACTS.md`, `VERIFICATION_MATRIX.md`, `FUZZ_TARGETS.md`, `OPERATIONS_RUNBOOK.md`, `INDEX.md`가 생성된다.
3. 완료: `.kernforge/analysis/latest/docs_manifest.json`, `docs_index.md`, `run.json`, `snapshot.json`, `dashboard.html`이 생성된다.
4. 완료: 문서와 섹션에 source anchor, confidence, stale/invalidation marker, reuse target이 표시된다.
5. 완료: `/analyze-project --docs`, `/analyze-project --path <dir>`, `/docs-refresh`, `/analyze-project --mode surface`, `/analyze-dashboard`가 help와 completion까지 포함해 노출된다.
6. 완료: generated docs의 `FUZZ_TARGETS.md` catalog를 `/fuzz-func` target ranking이 재사용한다.
7. 완료: generated docs의 `VERIFICATION_MATRIX.md`를 `/verify` planner가 재사용한다.
8. 완료: generated docs manifest는 evidence `kind=analysis_docs`와 persistent memory의 project knowledge-base record로 저장된다.
9. 완료: `/analyze-project` 완료 후 `Analysis handoff`가 `/analyze-dashboard`, `/fuzz-campaign run`, 상위 `/fuzz-func ...` drilldown, `/verify` 중 필요한 다음 명령을 안내한다.
10. 완료: 자연어 goal의 디렉토리 힌트는 자동 scope로 유지하고, 명시적인 범위 고정이 필요할 때는 `/analyze-project --path <dir>`가 실행 전 검증된 shard scope를 만든다.
11. 완료: README, feature guide, generated docs의 역할이 분리되고 한국어/영어 README 동기화 기준이 유지된다.
12. 완료: `/analyze-project --mode <mode>` 또는 `/analyze-project --path <dir> --mode <mode>`에서 goal을 생략해도 mode/path 기반 기본 분석 목표를 자동 생성한다.
13. 완료: `trace`, `impact`, `surface`, `security`, `performance` 실행 시 이전 `map` run을 baseline architecture map으로 로드해 worker/reviewer/synthesis prompt가 구조 지도를 재사용한다.
14. 완료: 분석 실행 확인 화면에 선택된 `baseline_map`, goal, artifact, anchor 수를 표시해 사용자가 진행 전에 재사용될 map을 확인할 수 있게 했다.
15. 완료: worker/reviewer provider rate-limit이나 일시 오류가 전체 analysis run을 중단하지 않도록 shard-level low-confidence failure로 degrade하고, synthesis 실패 시 local fallback document를 생성한다.

현재 남은 핵심 과제:
1. 완료: generated docs dashboard를 정적 document portal 수준으로 확장한다.
2. 완료: cross-document search, source anchor deep link, stale section diff, evidence/memory drill-down을 추가했다.
3. 완료: trust-boundary graph와 attack-flow view MVP를 dashboard에 연결했다.
4. 완료: 문서 산출물을 vector corpus에 whole-document/section-level record로 재수집한다.
5. 완료: docs manifest schema versioning과 backward compatibility policy를 둔다.
6. 완료: data-flow edge 정밀화와 generated docs 본문 내 graph section 연결을 보강한다.
7. 완료: 변경 diff view 정밀화와 graph 섹션의 stale marker 연동을 보강했다.
8. 완료: fuzz campaign artifact/evidence graph schema와 finding issue lifecycle 연결을 보강했다.
9. 완료: coverage gap을 `FUZZ_TARGETS.md` refresh와 ranking에 feedback한다.

현재 기반:
1. `ProjectAnalysisRun`은 snapshot, shard report, review decision, final document를 보존한다.
2. `KnowledgePack`은 subsystem, dependency, project edge, Unreal metadata를 구조화한다.
3. `structural_index_v2`는 symbol anchor, call edge, build ownership edge, overlay edge를 제공한다.
4. Unreal graph는 project, plugin, target, module, reflected type, network surface, asset/config 관계를 제공한다.
5. vector corpus와 ingestion manifest는 후속 검색/RAG 계층으로 넘기기 좋은 형태다.

우선 강화할 문서 산출물:
1. `ARCHITECTURE.md`
- subsystem ownership, entrypoint, runtime flow, build/module boundary를 요약한다.

2. `SECURITY_SURFACE.md`
- IOCTL, RPC, IPC, handle, memory, ETW, network, Unreal RPC/replication surface를 중심으로 정리한다.

3. `API_AND_ENTRYPOINTS.md`
- exported function, command handler, service entry, driver dispatch, Unreal module startup/shutdown을 catalog화한다.

4. `BUILD_AND_ARTIFACTS.md`
- solution/project/target/module, compile command coverage, generated code, signing/package readiness를 정리한다.

5. `VERIFICATION_MATRIX.md`
- 변경 유형별 필수 검증, optional 검증, 누락 시 위험, evidence 연결 방식을 표로 남긴다.

6. `FUZZ_TARGETS.md`
- `/fuzz-func`가 바로 이어받을 수 있는 후보 함수, 파일, entrypoint, 입력 파라미터, build context 수준을 정리한다.

7. `OPERATIONS_RUNBOOK.md`
- live investigation, telemetry capture, crash triage, false positive 대응, release gate를 운영 흐름으로 정리한다.

구현 우선순위:
1. 완료: analysis run에서 위 문서들을 deterministic하게 생성하는 documentation writer 추가
2. 완료: 문서별 source anchor, confidence, stale/invalidation marker 표시
3. 완료: `.kernforge/analysis/latest` 아래에 문서 index와 manifest 생성
4. 완료: `/analyze-project --docs`와 `/docs-refresh` 명령 추가
5. 완료: `/analyze-project --mode surface` 정식 노출
6. 완료: `README_kor.md`, feature guide, generated docs 사이의 역할 분리
7. 완료: 문서 산출물을 evidence, memory, verification planner, fuzz target discovery가 재사용하도록 연결
8. 완료: 분석 직후 `Analysis handoff`로 dashboard, fuzz campaign, target drilldown, verification의 다음 단계를 안내
9. 완료: dashboard를 문서 포털로 고도화하고 source anchor/evidence drill-down을 추가
9. 완료: dashboard에 trust-boundary graph와 attack-flow view 생성
10. 완료: generated docs를 retrieval/vector corpus로 재수집
11. 완료: docs manifest schema versioning과 backward compatibility policy
12. 완료: data-flow edge 정밀화와 generated docs graph section
13. 완료: `/analyze-project --path <dir>`와 prompt 기반 scope 안내를 추가해 사용자가 하위 디렉토리 분석 범위를 자연스럽게 고정할 수 있게 함
13. 완료: 변경 diff view와 graph stale marker 연동
14. 완료: fuzz campaign artifact/evidence graph schema와 finding issue lifecycle 연결
15. 완료: coverage gap feedback을 `FUZZ_TARGETS.md` ranking에 연결
16. 완료: finding/crash dedup을 fingerprint, source anchor, suspected invariant 기준으로 병합
17. 완료: coverage feedback 입력 포맷 확장. libFuzzer run log, llvm-cov text, LCOV, JSON coverage summary를 campaign coverage report로 수집하고 coverage gap feedback과 artifact graph에 연결한다.

성공 조건:
1. 달성: 큰 프로젝트를 처음 열었을 때 1회 분석으로 사람이 읽을 수 있는 문서 세트가 생성된다.
2. 달성: 보안 리뷰와 구현 작업이 같은 source anchor와 subsystem map을 공유한다.
3. 달성: fuzz target 후보와 verification matrix가 분석 산출물에서 자동으로 나온다.
4. 달성: 변경 후 재분석 시 오래된 문서 섹션과 재사용 대상이 명확히 구분된다.
5. 달성: dashboard에서 문서, source anchor, fuzz target, verification check, evidence/memory follow-up을 한 화면에서 추적할 수 있다.

### P0. Fuzzing Workbench

목표:
- `/fuzz-func`를 source-only triage 명령에서 전문 퍼징 워크벤치로 확장한다.

현재 기반:
1. 함수명 또는 파일 기반 target resolution
2. on-demand semantic index 복원
3. guard/probe/copy/dispatch/cleanup observation 추출
4. branch predicate, 최소 반례, pass/fail outcome, downstream call chain 렌더링
5. `report.md`, `plan.json`, `harness.cpp` 산출물 생성
6. build context가 있을 때 native execution readiness와 corpus/crash directory 준비
7. `/fuzz-func continue`로 pending native 실행 승인 흐름 지원
8. generated `FUZZ_TARGETS.md` catalog와 `docs_manifest.json`을 target ranking에 반영
9. help/completion에서 파일 기반, 함수 기반, language, continue 흐름을 노출
10. `/fuzz-campaign` MVP가 campaign manifest와 `.kernforge/fuzz/<campaign-id>/` artifact layout을 생성
11. campaign seed target은 최신 generated `FUZZ_TARGETS.md` catalog에서 초기 후보를 가져옴
12. `/fuzz-campaign` planner가 현재 상태를 보고 다음 한 단계를 제안
13. `/fuzz-campaign run`이 campaign 생성, latest `/fuzz-func` attach, source-only `VirtualScenarios`의 `corpus/<run-id>/scenario-XX-*.json` 승격을 자동 수행
14. `/fuzz-func` 결과 출력이 campaign handoff를 자동 표시해 사용자가 다음 명령을 추측하지 않아도 됨
15. 완료: `/investigate`, `/simulate`, `/verify`, `/analyze-performance`가 각각 Investigation/Simulation/Verification/Performance handoff를 출력해 dashboard, simulation, verification, evidence, checkpoint, tracked feature status/close로 자연스럽게 이어짐
16. 완료: `/evidence`, `/mem`, `/checkpoint`, `/new-feature status|implement|close`, `/worktree create|leave|cleanup`, `/specialists assign`도 Evidence/Memory/Checkpoint/Feature/Worktree/Specialist handoff를 출력해 verify, dashboard, confirm/promote, diff, cleanup, feature status로 이어짐
17. 완료: `/fuzz-campaign run`이 attached `/fuzz-func` native execution 상태, crash directory, build/run log를 수집해 campaign native result report와 `kind=fuzz_native_result` evidence로 기록함
18. 완료: native result report에 crash fingerprint, suspected invariant, minimization command, corpus/crash path를 남김
19. 완료: campaign manifest에 finding lifecycle과 artifact graph schema를 추가해 seed, native result, evidence, source anchor, verification gate, tracked feature gate를 연결함
20. 완료: campaign manifest의 coverage gap을 다음 `analyze-project` docs refresh에서 `FUZZ_TARGETS.md` ranking feedback으로 반영함
21. 완료: native crash finding을 crash fingerprint, source anchor, suspected invariant 기준으로 dedup하고 duplicate count와 병합된 native/evidence link를 manifest에 보존함

우선 강화할 전문 기능:
1. target discovery
- project analysis의 `FUZZ_TARGETS.md`, structural index, security overlay에서 fuzz 후보를 자동 추천한다.
- IOCTL dispatch, parser, deserializer, packet handler, Unreal RPC, config loader, telemetry decoder를 우선순위화한다.

2. harness quality
- libFuzzer/AFL++/WinAFL 스타일을 구분한다.
- target signature adapter, fixture init, fake kernel/user boundary shim, deterministic allocator mode를 분리한다.
- generated harness에 unresolved dependency와 required mock을 명시한다.

3. corpus lifecycle
- seed corpus 생성, import, dedup, minimize, promote를 지원한다.
- source-derived counterexample을 seed로 저장한다.
- crash 재현 corpus와 exploratory corpus를 분리한다.

4. coverage and sanitizer integration
- 완료: clang coverage 계열 report와 함께 sanitizer report, Windows crash dump, Application Verifier/Driver Verifier 결과를 campaign run artifact로 연결한다.
- coverage gap을 다음 fuzz target 추천에 반영한다.

5. crash triage
- crash hash, stack fingerprint, minimized input, target symbol, suspected invariant, source excerpt를 하나의 finding으로 묶는다.
- 중복 crash를 evidence graph와 persistent memory에서 합친다.

6. campaign management
- `/fuzz campaign new|status|run|stop|triage|minimize|promote` 계열 명령을 추가한다.
- background job과 verification bundle을 연결한다.
- 장시간 실행은 checkpoint, log tail, crash count, coverage delta를 주기적으로 남긴다.

7. Windows/anti-cheat specialization
- user/kernel boundary fuzz profile
- IOCTL buffer contract fuzz profile
- ETW/event schema fuzz profile
- Unreal RPC/replication fuzz profile
- anti-cheat telemetry parser fuzz profile

구현 우선순위:
1. 완료: 상위 `FuzzCampaign` 모델과 `FuzzCampaignStore` 추가
2. 완료: `.kernforge/fuzz/<campaign-id>/` manifest, corpus, crashes, coverage, reports, logs 표준화
3. 완료: source-only finding을 deterministic JSON seed artifact로 변환
4. 완료: 복잡한 attach/promote 단계를 사용자-facing 명령에서 감추고 `/fuzz-campaign run` 자동 오케스트레이션으로 통합
5. 완료: `/fuzz-func` 완료 후 campaign handoff와 `/fuzz-campaign run` 제안 출력
6. 부분 완료: compile command coverage와 build context 수준은 `FUZZ_TARGETS.md`에 표시한다. 다음은 부족한 build context를 자동 보강 task로 전환한다.
7. 완료: native run 결과를 evidence store에 자동 기록
8. 완료: crash triage report와 minimization command 생성
9. 완료: fuzz native result를 `/verify` targeted planner step과 `/new-feature status` gate handoff에 연결
10. 완료: generated docs catalog에서 campaign seed 후보를 생성
11. 완료: fuzz finding lifecycle과 artifact/evidence graph schema를 campaign manifest에 기록
12. 완료: coverage gap을 다음 `FUZZ_TARGETS.md` refresh와 ranking에 feedback
13. 완료: libFuzzer run log, llvm-cov text, LCOV, JSON coverage summary를 campaign coverage report로 수집하고 coverage gap으로 변환
14. 완료: sanitizer report, Windows crash dump, Application Verifier, Driver Verifier artifact를 campaign run artifact로 수집하고 native result, evidence, finding gate, artifact graph에 연결

성공 조건:
1. 사용자가 파일 하나만 지정해도 우선순위 fuzz target과 harness 후보가 나온다.
2. source-only 반례가 native seed corpus로 이어진다.
3. crash가 발생하면 재현 입력, stack/source anchor, suspected invariant, evidence id가 함께 남는다.
4. campaign 상태를 재개해도 corpus, crash, coverage, report 맥락이 유지된다.

### P0. Specialist Subagents And Worktree Isolation

목표:
- 현재 들어간 built-in specialist catalog, editable/read-only routing, worktree lease, ownership/lease 모델을 분석/문서화와 퍼징 campaign에 맞게 더 단단하게 만든다.

왜 중요한가:
- Codex/Claude Code/Hermes와 비교했을 때 가장 체감 차이를 빠르게 줄일 수 있는 축이다.
- anti-cheat/driver/telemetry 작업은 충돌 없는 병렬 조사, 문서 section ownership, fuzz campaign isolation, 안전한 rollback 경계가 특히 중요하다.

구현 우선순위:
1. documentation specialist와 fuzzing specialist profile 추가
2. node-aware routing을 docs section과 fuzz campaign task까지 확장
3. session-attached isolated git worktree 정책 hardening
4. tracked feature와 fuzz campaign 구현 시 auto isolation
5. specialist/worktree 상태를 `/status`, `/config`, `/specialists`, `/worktree`에 노출

기본 제공 specialist:
1. `planner`
2. `reviewer`
3. `kernel-investigator`
4. `driver-build-fixer`
5. `telemetry-analyst`
6. `unreal-integrity-reviewer`
7. `memory-inspection-reviewer`
8. `attack-surface-reviewer`
9. `project-documentarian`
10. `fuzzing-strategist`
11. `crash-triage-analyst`

specialist 설계 원칙:
1. 각 specialist는 별도 prompt와 선택적 provider/model override를 가진다.
2. 초기 단계에서는 read-only delegation을 우선하고, edit ownership은 main agent가 유지한다.
3. routing은 task node kind, failure text, lifecycle note, goal keyword를 함께 사용한다.
4. specialist assignment는 task graph에 남겨서 재개 시에도 보이게 한다.

worktree isolation 설계 원칙:
1. base workspace root와 active worktree root를 분리한다.
2. memory, hooks, feature metadata, workspace config는 base root 기준으로 유지한다.
3. edit/git/checkpoint는 active root 기준으로 수행한다.
4. cleanup는 dirty worktree를 강제로 지우지 않고 먼저 중단한다.

성공 조건:
1. tracked feature 구현이 기본적으로 isolated worktree에서 시작된다.
2. task graph에 specialist assignment가 축적된다.
3. active root 밖 편집은 막되, 외부 isolated worktree는 정상 허용된다.
4. 실패한 tracked feature 구현을 base root 오염 없이 정리할 수 있다.

### P0. Security Hooks Engine Hardening

목표:
- 기존 hook engine을 "보안 엔지니어링 안전장치"로 더 촘촘하게 만든다.

왜 중요한가:
- Kernforge는 이미 hook runtime을 갖고 있으므로, 이제는 존재 여부보다 보안 정책 깊이를 높이는 단계다.
- anti-cheat/driver/telemetry 작업은 실수 비용이 크므로 preflight 정책 계층의 품질이 중요하다.

주요 이벤트:
1. `SessionStart`
2. `UserPromptSubmit`
3. `PreToolUse`
4. `PostToolUse`
5. `PreEdit`
6. `PostEdit`
7. `PreVerification`
8. `PostVerification`
9. `PreGitPush`
10. `PreCreatePR`
11. `SessionEnd`

권장 동작:
1. allow
2. deny
3. warn
4. ask
5. rewrite-context
6. attach-evidence
7. enqueue-verification

Windows/security 전용 hook 예시:
1. unsigned driver 산출물 감지 시 push/PR 경고
2. `.sys`, `.inf`, `.cat` 변경 시 서명/패키징 검증 강제
3. `bcdedit`, `verifier`, `sc stop`, `fltmc unload` 같은 명령 실행 전 추가 승인
4. kernel 관련 변경인데 PDB/symbol 없음 경고
5. memory scanner 변경인데 synthetic regression 검증 누락 시 경고
6. anti-cheat 모듈 변경인데 telemetry diff 수집을 자동 제안

MVP 범위:
1. JSON 기반 hook rule 파일
2. 이벤트 payload 직렬화
3. allow/warn/deny/ask 4종만 먼저 지원
4. tool/shell/edit/git/verify 이벤트 우선 지원

성공 조건:
1. 보안 민감 작업의 사전 사고율 감소
2. verification coverage 상승
3. "이 작업은 그냥 수정하면 안 된다"를 런타임이 알려줌

### P1. Security-Aware Verification Planner

목표:
- 현재 verification 정책 엔진을 보안/윈도우 특화형으로 확장한다.

현재 기반:
- adaptive verification
- verification policy
- verification history tuning

확장 방향:
1. changed file classification
2. artifact-aware verification
3. risk score 기반 검증 자동 추가
4. failure signature 기반 재검증 우선순위 조정

변경 유형별 예시:
1. driver 변경
- `signtool verify`
- catalog/inf 연계 확인
- symbol 존재 확인
- optional: `verifier` smoke checklist

2. Unreal anti-cheat 변경
- module boundary 체크
- cooked asset/integrity 관련 회귀 테스트
- pattern/schema drift 체크

3. process telemetry 변경
- ETW provider manifest/contract 체크
- log schema compatibility 체크

4. memory scanning 코드 변경
- synthetic evasion corpus
- false positive regression
- performance ceiling 체크

핵심 지표:
- 변경 유형 대비 검증 누락률 감소
- 실패 재현성 증가
- 반복 실패 signature의 자동 회피

### P0. Conversational Runtime Memory And Situation Awareness

현재 구현 상태:
1. 완료: `conversation_events.go`에 append-only `ConversationEvent` log를 추가하고 session JSON에 영속화했다.
2. 완료: `conversation_state.go`에 `ConversationState` working set을 추가하고 provider/model, active feature, latest analysis, latest verification, open artifact, running background job을 매 turn 갱신한다.
3. 완료: `context_assembler.go`가 active state와 recent runtime events를 사용자 메시지와 system prompt에 우선 주입한다.
4. 완료: `turn_intent.go`가 `diagnose_recent_error`, `continue_last_task`, `explain_current_state`, `ask_project_knowledge`, `edit_code`, `run_command`, `plan_or_design`을 분류한다.
5. 완료: `recent_error_resolver.go`가 "방금 에러", "아까 오류", "왜 실패" 질문을 최근 provider/tool/command error event에서 직접 답한다.
6. 완료: provider 429, timeout, tool error, command error를 정규화해 `category`, `provider`, `upstream`, `model`, `code`, `shard`, `retryable`, `byok_hint` entity로 저장한다.
7. 완료: `diagnose_recent_error`, `continue_last_task`, `explain_current_state`에서는 cached project analysis fast-path가 끼어들지 않도록 guard를 추가했다.
8. 완료: `NEEDS_TOOLS`, `Cached analysis fast-path`, fast-path debug marker를 사용자 답변에서 제거한다.
9. 완료: verification pass/fail 결과를 conversation event로 기록한다.
10. 완료: evidence ID가 같은 millisecond 안에서 충돌해 fuzz finding evidence merge가 깨지는 문제를 nanosecond 기반 ID로 수정했다.
11. 완료: tool call start/result success event를 남겨 command output summary와 tool result가 recent state에 들어오게 했다.
12. 완료: `Compact()`가 active conversation state, last error, pending handoff, open artifact를 `[Conversation Working Memory]` block으로 summary에 보존한다.
13. 완료: compact/resume 이후 recent error resolver가 event log와 preserved state 양쪽에서 마지막 오류를 찾는 회귀 테스트를 추가했다.
14. 완료: handoff block에서 current workflow와 pending next step을 추출해 compaction working memory에 남긴다.
15. 완료: fuzz native result evidence dedup key에 campaign/run/report/fingerprint discriminator를 추가해 같은 finding으로 병합되더라도 evidence link는 run별로 유지한다.

남은 구현 항목:
- 없음. P0 범위의 runtime memory, situation awareness, recent error grounding, compact/resume 보존, 회귀 테스트 구현을 완료했다.

목표:
- Kernforge를 "프롬프트마다 독립적으로 답하는 분석 CLI"가 아니라, Codex/Claude처럼 사용자의 직전 작업, 실패 로그, 열린 artifact, 진행 중인 feature, 실행 중인 background job, 방금 선택한 모델/provider까지 이해하는 대화형 agent runtime으로 확장한다.

문제 사례:
- 사용자가 project analysis 도중 `analysis worker request failed ... openai API error (429 Too Many Requests)`를 봤고 바로 "방금 에러는 왜 난거야?"라고 물었다.
- 그러나 assistant는 cached analysis fast-path만 사용해 "어떤 에러인지 알려 달라"고 답했다.
- 실제로는 같은 세션의 직전 assistant/tool/runtime 출력에 provider, model, shard, upstream provider, rate-limit 원인, BYOK 안내까지 모두 있었다.

핵심 진단:
1. 대화 모델에 들어가는 context assembly가 "사용자 최신 프롬프트 + 캐시된 프로젝트 분석" 중심으로 치우쳐 있다.
2. 직전 runtime event, tool error, provider error, background job 상태, handoff block이 retrieval 후보로 승격되지 않는다.
3. fast-path가 current-turn diagnostic 질문을 구분하지 못하고 오래된 project analysis cache를 더 신뢰한다.
4. session transcript는 저장되어도 "최근 상황을 설명하는 working set"으로 구조화되지 않는다.
5. assistant 답변이 `NEEDS_TOOLS`나 cached answer label을 출력하는 등 내부 라우팅 상태가 사용자 경험에 누출된다.

권장 제품 목표:
1. "방금", "아까", "왜 실패", "이 에러", "계속 해", "방금 작업 이어서" 같은 지시어를 세션 상황에 안정적으로 grounding한다.
2. 답변 전 context budget 안에 최근 대화, 최근 tool result, 최근 error/event, active feature, latest analysis/fuzz/verify artifact를 우선 배치한다.
3. project cache는 강력한 보조 지식으로 쓰되, 현재 세션에서 발생한 사건보다 우선하지 않는다.
4. 실패 원인 질문은 추가 정보 요구보다 먼저 최근 error event를 찾아 설명한다.
5. compact/resume 이후에도 현재 작업의 최소 working memory가 유지된다.

권장 아키텍처:

1. `ConversationEventLog`
- 모든 사용자 입력, assistant 답변, tool call, tool result, command output summary, provider error, verification result, handoff block을 append-only event로 저장한다.
- 원문 전체와 별도로 `short_summary`, `event_kind`, `severity`, `entities`, `artifact_refs`, `time`, `turn_id`, `correlation_id`를 기록한다.
- provider 오류는 별도 schema로 정규화한다. 예: `provider=OpenRouter`, `upstream=DeepInfra`, `model=deepseek/deepseek-v4-flash`, `code=429`, `category=rate_limit`, `retryable=true`, `byok_hint=true`.

2. `ActiveConversationState`
- 매 turn마다 빠르게 주입할 작은 working set이다.
- 포함 항목:
  1. 최근 사용자 목표
  2. 현재 명령 또는 workflow
  3. 마지막 실패/경고 3개
  4. 실행 중 background job
  5. active feature/checkpoint/worktree
  6. 최신 analysis/fuzz/verify artifact
  7. 마지막 assistant handoff
  8. 마지막으로 선택된 provider/model/profile
- 이 상태는 session JSON과 별도로 `.kernforge/sessions/<id>_state.json` 또는 기존 session record 내부에 versioned block으로 저장한다.

3. `ContextAssembler`
- 답변 요청마다 context를 아래 우선순위로 조립한다.
  1. 현재 system/developer/project policy
  2. 현재 사용자 메시지
  3. recent-turn window
  4. active conversation state
  5. relevant recent events
  6. active artifact snippets
  7. project memory / analysis docs / vector corpus
  8. long-term persistent memory
- 중요한 원칙은 "최근 사건 > 현재 artifact > 프로젝트 분석 캐시 > 장기 기억"이다.

4. `TurnIntentClassifier`
- 최신 사용자 메시지를 다음 intent로 분류한다.
  1. `diagnose_recent_error`
  2. `continue_last_task`
  3. `explain_current_state`
  4. `ask_project_knowledge`
  5. `edit_code`
  6. `run_command`
  7. `plan_or_design`
- `diagnose_recent_error`와 `continue_last_task`에서는 cached analysis fast-path를 금지하거나 가장 낮은 우선순위로 내린다.

5. `RecentErrorResolver`
- "방금 에러", "이 오류", "왜 실패" 같은 질문이 들어오면 최근 event log에서 error/warning/tool failure/provider failure를 찾는다.
- 매칭 기준:
  1. 시간 proximity
  2. 직전 assistant/tool turn
  3. 현재 workflow correlation id
  4. model/provider/shard/job id entity match
  5. severity
- 후보가 하나면 바로 설명하고, 여러 개면 가장 가능성 높은 원인을 먼저 말한 뒤 다른 후보를 짧게 언급한다.

6. `AnswerGroundingPolicy`
- 답변에는 어떤 context를 근거로 썼는지 내부적으로 추적한다.
- 사용자에게는 필요한 경우만 짧게 노출한다. 예: "직전 analysis worker 로그 기준으로 보면..."
- 내부 라우팅 문자열인 `NEEDS_TOOLS`, `Cached analysis fast-path`, schema/debug marker는 사용자 답변에 직접 출력하지 않는다.

7. `CompactionAndResumeMemory`
- compact 시 단순 대화 요약이 아니라 다음 필드를 보존한다.
  1. unresolved user goal
  2. last command and result
  3. last error and likely root cause
  4. pending recommended next step
  5. open artifact paths
  6. decisions made
  7. user preferences observed in this session
- `/resume` 이후 첫 답변은 이 working state를 우선 로드한다.

방금 오류 사례에서 기대 답변:
1. "직전 로그 기준으로 원인은 Kernforge 분석 worker가 `TavernKernel/TavernKernel/BuildCab_refined_03` shard를 `deepseek/deepseek-v4-flash` 모델로 요청했는데, OpenRouter 뒤의 DeepInfra upstream이 429 rate limit을 반환했기 때문입니다."
2. "코드/프로젝트 분석 내용 문제가 아니라 provider quota/temporary upstream throttling 문제입니다."
3. "재시도하면 풀릴 수 있고, 자주 반복되면 해당 role model을 다른 모델로 바꾸거나 OpenRouter BYOK/provider key를 설정해 rate limit pool을 분리하는 것이 좋습니다."
4. "analysis run은 shard-level degrade가 구현되어 있다면 해당 shard를 low-confidence failure로 기록하고 계속 진행해야 합니다."

구현 단계:
1. `ConversationEvent` schema와 append-only session event store 추가
2. provider/tool/shell/background job error normalization 추가
3. `ActiveConversationState` 생성 및 매 turn 갱신
4. context assembly 우선순위 재설계
5. current-turn intent classifier 추가
6. recent error resolver 추가
7. cached analysis fast-path guard 추가
8. compact/resume summary schema 고도화
9. 내부 라우팅 marker 출력 차단
10. 회귀 테스트 추가

필수 테스트:
1. 직전 provider 429 이후 "방금 에러는 왜 난거야?"가 rate-limit 원인을 설명해야 한다.
2. 직전 compile 실패 이후 "왜 실패했어?"가 compiler error를 참조해야 한다.
3. 직전 `/verify` 실패 이후 "계속 진행해"가 retry/repair flow로 이어져야 한다.
4. cached project analysis가 있어도 recent error 질문에는 analysis cache 답변을 우선하지 않아야 한다.
5. compact/resume 이후 "아까 실패한 거 이어서 봐줘"가 마지막 실패 event를 찾아야 한다.
6. 답변에 `NEEDS_TOOLS`, `Cached analysis fast-path` 같은 내부 marker가 노출되지 않아야 한다.

운영 지표:
1. recent error grounding 성공률
2. "정보를 더 달라" 오답률 감소
3. resume 이후 pending task 복원률
4. cached fast-path 오용률
5. 사용자가 같은 에러 로그를 다시 붙여넣는 빈도 감소

### P0. Proactive Situation Judgment And Suggestions

현재 구현 상태:
1. 완료: `proactive_suggestions.go`에 `SituationSnapshot`, `Suggestion`, `SuggestionMemory`, `ProactiveSources` schema를 추가했다.
2. 완료: snapshot builder가 `ActiveConversationState`, recent conversation events, latest analysis docs stale markers, verification history, evidence store, function fuzz store, fuzz campaign store, git dirty state를 읽어 현재 상황을 구조화한다.
3. 완료: `ProactiveSuggestionEngine` rule set을 추가해 provider 429/timeout, verification gap, stale docs, failed verification, pending handoff, fuzz native/minimization/coverage gap, high-risk dirty worktree checkpoint, evidence capture gap, feature close/cleanup 후보를 생성한다.
4. 완료: `SuggestionPolicy`가 `dedup_key`, dismissed cooldown, accepted/executed suppression으로 같은 제안 반복을 막는다.
5. 완료: `NextActionPlanner`가 blocking/risk/cost 기준으로 후보를 ranking하고 기본 답변에는 provider rate-limit처럼 즉시 조치가 명확한 제안만 1개 붙인다. 나머지 후보는 `/suggest`에서 확인한다.
6. 완료: session JSON에 `suggestion_memory`를 저장하고 shown/accepted/dismissed/executed 상태와 cooldown을 보존한다.
7. 완료: `Compact()` working memory가 pending/dismissed suggestion을 `[Conversation Working Memory]`에 보존한다.
8. 완료: `/suggest`, `/suggest accept <id>`, `/suggest dismiss <id>`, `/suggest mode <observe|suggest|confirm>`, `/suggest-dashboard-html` 명령을 추가했다.
9. 완료: `/suggest-dashboard-html`이 current situation, integrated signals, ranked suggested next actions를 로컬 HTML dashboard로 렌더링한다.
10. 완료: suggestion card가 관련 dashboard 명령(`/verify-dashboard-html`, `/evidence-dashboard-html`, `/analyze-dashboard` 등), evidence refs, `/suggest accept|dismiss <id>` command chip을 같이 보여준다.
11. 완료: provider 429 제안 1회 노출, verification/checkpoint gap 생성, fuzz minimization gap 생성, dismissed suggestion compaction 보존, accepted suggestion event 연결 회귀 테스트를 추가했다.
12. 완료: suggestion dashboard가 analysis/verification/evidence 통합 링크를 렌더링하는 회귀 테스트를 추가했다.
13. 완료: `/suggest accept <id>`가 `confirm` 모드에서는 `/verify`, dashboard, `/docs-refresh`, `/automation add`, `/review-pr` 같은 safe command만 실행하고 성공 시 suggestion 상태를 `executed`로 바꾼다.
14. 완료: accepted/dismissed suggestion을 persistent memory에 `suggestion-preference` category로 승격한다.
15. 완료: `/suggest` 후보를 `TaskGraph`의 `suggest:<stable-id>` node로 동기화하고 상태를 ready/in_progress/completed/canceled로 반영한다.
16. 완료: provider/tool/command error 후보가 여러 개 있을 때 recent error answer가 다른 후보 목록을 함께 보여준다.
17. 완료: `session.Automations`와 `/automation` 명령을 추가해 recurring verification 및 PR review automation slot을 세션에 저장하고 수동 실행한다.
18. 완료: `/review-pr`가 git status/diff stat/changed files/checklist를 `.kernforge/pr_review/latest.md`로 생성하고 conversation event에 artifact ref를 남긴다.

남은 구현 항목:
- 없음. P0 범위의 proactive situation snapshot, rule-based suggestion engine, policy/memory, CLI/HTML dashboard, compact 보존, 회귀 테스트 구현을 완료했다.

### P0. Self-Driving Work Loop Orchestration

현재 구현 상태:
1. 완료: `self_driving_loop.go`를 추가해 일반 구현/수정/실행 요청을 self-driving task로 판정하고, read-only 분석/최근 오류 진단/status 질문은 자동 루프에서 제외한다.
2. 완료: active task가 없거나 완료된 상태에서 새 구현 요청이 들어오면 `TaskState`, shared plan, `TaskGraph`를 자동으로 시작한다.
3. 완료: 기본 plan은 inspect, implement, verify, summarize 네 단계로 구성하고 `TaskGraph` node kind가 inspection/edit/verification/summary로 나뉘게 했다.
4. 완료: reviewer/planner preflight를 사용할 수 있는 세션에서는 기본 plan을 성급히 확정하지 않고 기존 plan-review 루프를 우선 사용한다.
5. 완료: system prompt에 `Self-driving work loop` section을 추가해 "분석에서 멈추지 말고 inspect -> implement -> verify -> summarize로 진행"하도록 명시했다.
6. 완료: final answer 직전 `finalizeSelfDrivingWorkLoopOnReturn`이 자동 검증 실패 시 phase를 `recovery`로 유지하고, 검증 문제가 없으면 plan과 task graph를 `completed`로 마감한다.
7. 완료: compact/resume에 이미 포함되는 `TaskState`/`TaskGraph`에 self-driving event를 기록하므로, 긴 작업 중간에도 현재 goal, phase, next step, pending check가 보존된다.
8. 완료: `self_driving_loop_test.go`에 plan seeding, recent-error exclusion, prompt guidance, verification failure recovery, unblocked completion 회귀 테스트를 추가했다.

남은 구현 항목:
- 없음. P0 범위의 자연어 작업 위임 -> task graph 시드 -> 도구 루프 -> 자동 검증 -> 실패 복구/최종 요약 연결을 완료했다.

목표:
- 사용자가 "이거 구현하자", "남은 항목들을 처리해줘", "테스트까지 돌려서 끝내줘"처럼 작업을 맡기면 Kernforge가 단순 제안이나 부분 답변에서 멈추지 않고 전체 engineering loop를 스스로 유지하게 한다.

동작 원칙:
1. 명시적 구현/수정/실행 요청은 self-driving loop 후보로 본다.
2. "왜 에러가 났어?", "현재 상태가 뭐야?", "분석해줘"처럼 read-only 성격이 강한 요청은 자동 편집 루프를 켜지 않는다.
3. reviewer/planner가 구성되어 있으면 preflight plan-review를 우선 사용하고, 그렇지 않으면 deterministic 기본 plan을 시드한다.
4. 편집 후 자동 verification이 실패하면 task를 끝내지 않고 recovery phase로 남긴다.
5. verification이 통과하거나 명확한 fallback이 정리되면 final answer와 함께 shared plan을 완료 처리한다.

### P0. Proactive Situation Judgment And Suggestions - Design Notes

목표:
- Kernforge가 단순히 사용자의 다음 프롬프트를 기다리는 CLI가 아니라, Codex처럼 현재 작업 상태, 실패/성공 신호, 변경 위험도, 열린 artifact, 검증 공백을 스스로 판단하고 "지금 하면 좋은 다음 행동"을 사용자에게 제안하는 대화형 engineering partner가 되게 한다.
- 제안은 자동 실행이 아니라 "근거 있는 next step 후보"여야 한다. 사용자가 명시적으로 맡긴 작업은 이어서 수행하되, 위험한 변경/비용 큰 실행/외부 시스템 영향은 제안과 확인을 분리한다.

현재 기반:
1. 완료된 `ConversationEventLog`와 `ActiveConversationState`가 직전 오류, tool result, handoff, open artifact, provider/model, active workflow를 보존한다.
2. analysis docs, fuzz campaign, verification history, evidence store, persistent memory, checkpoint/worktree 상태가 이미 독립 저장소로 존재한다.
3. 각 명령의 handoff 출력은 존재하지만, 아직 agent runtime 전체가 이를 통합해 우선순위화하지는 않는다.

핵심 문제:
1. "다음에 뭘 해야 하는지"가 각 command handoff에 흩어져 있고, 대화 turn 전체의 decision layer가 없다.
2. 사용자가 오류를 묻거나 "계속 해"라고 말하면 반응은 가능하지만, 사용자가 묻기 전에 stale docs, missing verification, failed shard, dirty worktree, incomplete fuzz campaign을 먼저 알아채지는 못한다.
3. 제안이 제품 UX로 정리되지 않으면 assistant 답변이 장황해지거나 매번 같은 checklist를 반복할 위험이 있다.
4. 보안/커널/anti-cheat 작업에서는 "할 수 있음"보다 "지금 해야 함"의 판단 근거가 중요하다. 변경 위험도, 검증 공백, 재현 가능성, evidence 누락을 함께 봐야 한다.

권장 아키텍처:

1. `SituationSnapshot`
- 매 assistant turn 직전 생성되는 짧은 구조화 snapshot이다.
- 입력:
  1. `ActiveConversationState`
  2. 최근 `ConversationEvent` window
  3. 최신 analysis docs manifest와 stale section 목록
  4. verification history와 최근 실패 signature
  5. evidence store의 unresolved finding
  6. fuzz campaign 상태와 native result/crash/minimization gap
  7. git dirty state, checkpoint, active worktree, active feature
  8. background job 상태
- 출력 필드:
  1. `current_goal`
  2. `workflow_phase`
  3. `blocking_issue`
  4. `risk_level`
  5. `confidence`
  6. `open_artifacts`
  7. `missing_evidence`
  8. `missing_verification`
  9. `suggestion_candidates`

2. `ProactiveSuggestionEngine`
- snapshot을 받아 0개 이상의 suggestion을 생성한다.
- suggestion schema:
  1. `id`
  2. `title`
  3. `reason`
  4. `evidence_refs`
  5. `command`
  6. `estimated_cost`
  7. `risk`
  8. `requires_confirmation`
  9. `dedup_key`
  10. `expires_at_event_id`
- 제안 유형:
  1. `retry_or_switch_model`: provider 429/timeout 이후 재시도, 모델 변경, BYOK 설정 제안
  2. `run_verification`: 코드 변경 후 테스트/빌드/보안 검증 누락 제안
  3. `refresh_analysis`: 소스 변경으로 docs section stale 시 `/docs-refresh` 또는 `/analyze-project --mode impact` 제안
  4. `inspect_failure`: compile/test/verify 실패 후 root cause drilldown 제안
  5. `continue_workflow`: 직전 handoff의 pending next step 실행 제안
  6. `fuzz_next_step`: fuzz target 발견 후 campaign run, crash minimization, corpus promotion 제안
  7. `checkpoint_or_worktree`: 위험한 편집 전 checkpoint/worktree 생성 제안
  8. `evidence_capture`: 의미 있는 finding/result가 있는데 evidence record가 없을 때 캡처 제안
  9. `cleanup_or_close_feature`: feature 구현/검증 후 close, cleanup, summary 제안

3. `SuggestionPolicy`
- 제안은 "도움이 되는 순간"에만 노출한다.
- 기본 규칙:
  1. 같은 `dedup_key` 제안은 상태가 바뀔 때까지 반복하지 않는다.
  2. 사용자가 명시적으로 좁은 질문을 했으면 최대 1개만 짧게 붙인다.
  3. 오류/실패/검증 공백처럼 즉시 중요한 항목은 답변 본문에 포함한다.
  4. 비용 큰 작업, destructive 가능성, 외부 provider 비용 증가, 장시간 job은 자동 실행하지 않고 확인을 받는다.
  5. 이미 사용자가 거절한 제안은 session scope에서 cooldown한다.
  6. 제안 근거가 낮은 confidence면 "가능성"으로 표시하고 기본 행동으로 삼지 않는다.

4. `NextActionPlanner`
- 여러 suggestion을 우선순위화해 "추천 1개 + 대안 1~2개"로 압축한다.
- 우선순위 기준:
  1. blocking 여부
  2. 데이터 손실/작업 손실 방지
  3. 보안/커널 변경 위험도
  4. 검증 누락 심각도
  5. 사용자의 최근 목표와의 직접성
  6. 비용/시간
  7. confidence
- 출력 예:
  1. 추천: "방금 provider 429 때문에 analysis shard가 실패했으니 같은 shard만 재시도하거나 model을 교체하는 것이 우선입니다."
  2. 대안: "전체 analysis를 다시 돌리기보다 실패 shard를 low-confidence로 남기고 dashboard에서 gap을 확인할 수 있습니다."

5. `SuggestionMemory`
- session JSON에 제안 이력을 저장한다.
- 저장 필드:
  1. shown suggestions
  2. accepted suggestions
  3. dismissed suggestions
  4. executed command/result
  5. cooldown state
- 목적:
  1. 같은 제안 반복 방지
  2. 사용자가 선호하는 workflow 학습
  3. compact/resume 이후 pending suggestion 복원

6. `AutonomyMode`
- 자동성 수준을 명시한다.
- 권장 모드:
  1. `observe`: 제안을 만들지만 출력하지 않고 로그/테스트에만 사용
  2. `suggest`: 기본 모드. 답변 끝에 짧은 next step을 제안
  3. `confirm`: 제안된 명령을 실행하기 전 확인을 요청
  4. `autopilot`: 사용자가 맡긴 bounded task 안에서 안전한 read/test/verify는 자동 실행
- 초기 구현은 `suggest`와 `confirm`까지만 제품화한다.

구현 단계:
1. `SituationSnapshot` schema와 builder 추가
2. analysis docs, verification history, evidence, fuzz campaign, git/checkpoint/worktree 상태를 snapshot source로 연결
3. `Suggestion` schema와 `ProactiveSuggestionEngine` 추가
4. provider error, tool failure, verification gap, stale docs, fuzz campaign gap, dirty worktree용 rule-based detector부터 구현
5. `SuggestionPolicy`와 dedup/cooldown 저장소 추가
6. `NextActionPlanner`로 suggestion ranking과 답변 내 표시 형식 통일
7. `Compact()` working memory에 pending suggestion과 dismissed suggestion 보존
8. `/suggest`, `/suggest accept <id>`, `/suggest dismiss <id>`, `/suggest mode <observe|suggest|confirm>` 명령 추가
9. normal reply path에서 "명시적 질문 답변 + 필요한 next step 1개"로 조립
10. dashboard에 current situation과 suggested next actions 패널 추가

MVP rule set:
1. 직전 provider 429/timeout:
- 원인 설명 후 "같은 shard 재시도", "model fallback", "BYOK/provider key 설정" 중 하나를 상황별 추천

2. 코드 변경 후 verification 없음:
- 변경 파일 유형을 보고 `/verify`, build/test, driver signing/symbol check, synthetic regression 중 필요한 검증 제안

3. analysis docs stale:
- stale section과 변경 파일을 연결해 `/docs-refresh` 또는 scoped `/analyze-project --mode impact --path <dir>` 제안

4. fuzz campaign incomplete:
- source-only scenario만 있고 native run 없음: `/fuzz-campaign run` 제안
- crash artifact는 있는데 minimization 없음: minimization command 제안
- coverage gap 존재: corpus/seed 보강 제안

5. dirty worktree plus risky edit:
- kernel, anti-cheat, telemetry, memory scanner 파일 변경 전 checkpoint/worktree 제안

6. failed verification:
- 같은 failure signature가 반복되면 단순 재실행보다 root cause drilldown과 targeted fix를 우선 제안

필수 테스트:
1. provider 429 event 직후 일반 답변 끝에 model retry/fallback 제안이 1회만 표시되어야 한다.
2. 같은 상태에서 같은 제안이 매 turn 반복되지 않아야 한다.
3. 사용자가 좁은 설명 질문을 했을 때 제안은 본문을 방해하지 않는 짧은 한 줄이어야 한다.
4. 코드 변경 후 verification history가 비어 있으면 변경 유형 기반 검증 제안이 생성되어야 한다.
5. stale docs manifest가 있으면 docs refresh 제안이 생성되어야 한다.
6. fuzz campaign에 crash artifact가 있고 minimization이 없으면 minimization 제안이 생성되어야 한다.
7. dismissed suggestion은 compact/resume 이후에도 즉시 재노출되지 않아야 한다.
8. accepted suggestion은 command 실행 결과와 conversation event로 연결되어야 한다.

운영 지표:
1. 사용자가 제안을 accept한 비율
2. dismissed/repeated suggestion 비율
3. 검증 누락 상태에서 제안이 나온 비율
4. 실패 후 해결까지 turn 수 감소
5. stale docs/fuzz gap/evidence gap의 평균 방치 시간 감소

구현 시 주의점:
1. 제안 엔진은 처음부터 LLM 판단에 의존하지 말고 rule-based detector로 시작한다.
2. LLM은 rule 결과를 자연어로 압축하거나 충돌하는 후보를 설명할 때만 사용한다.
3. 제안은 "명령 실행"과 분리된 data model이어야 한다. 그래야 dashboard, CLI, compact/resume, 테스트가 모두 같은 상태를 볼 수 있다.
4. 보안/커널 작업에서는 낮은 confidence 제안을 자동 실행하지 않는다.
5. 사용자 답변을 방해하지 않도록 기본 출력은 최대 추천 1개로 제한한다.

### P1. Evidence Graph Memory

목표:
- 현재의 persistent memory를 텍스트 요약 저장소에서 "보안 증거 그래프"로 확장한다.

현재 한계:
- request/reply 중심 요약은 남지만, artifact 간 관계 추적은 약하다.

추가 엔티티:
1. issue
2. artifact
3. binary
4. hash
5. build
6. symbol state
7. telemetry finding
8. repro step
9. mitigation
10. environment

추가 관계:
1. issue -> affected binary
2. build -> produced artifact
3. finding -> observed in telemetry
4. mitigation -> validated by verification
5. crash -> associated symbols

효과:
- "지난번에도 비슷한 실패가 있었나?"를 넘어서
- "이 해시/버전/모듈/텔레메트리 패턴 조합이 언제 관찰됐나?"까지 답할 수 있다.

### P1. Live Windows Target Workflow

목표:
- 정적 코드 편집 중심 툴에서, live target을 다루는 investigation runtime으로 확장한다.

우선 지원할 도구군:
1. `signtool`
2. `dumpbin`
3. `symchk`
4. `sc`
5. `fltmc`
6. `verifier`
7. `bcdedit`
8. `wevtutil`
9. `logman`
10. `wpr`
11. `xperf`
12. `cdb` 또는 `windbg` helper

핵심 워크플로우:
1. target 상태 수집
2. 증거 스냅샷 생성
3. ETW/로그/심볼 상태 묶음 수집
4. 결과를 memory와 verification history에 연결

차별화 포인트:
- Codex/Claude가 "코드 작업 보조"라면
- Kernforge는 "코드 + 실행 환경 + 증거 수집"까지 이어지는 anti-cheat/security investigation agent가 된다.

### P2. Incident Replay Bundle

목표:
- 이슈 분석 결과를 나중에 다시 열어볼 수 있는 재현 가능한 bundle로 남긴다.

권장 bundle 내용:
1. request/final answer
2. 관련 selection
3. changed files diff
4. verification report
5. memory citations
6. shell transcript 요약
7. artifact metadata
8. telemetry snapshot index

사용 예:
1. 버그 재오픈 대응
2. 팀 내 handoff
3. anti-cheat false positive 분석 이관
4. 사후 회고

### P2. Review Profiles For Adversarial Thinking

최근 안정화:
1. 완료: `/profile`과 `/profile-review`가 one-shot 모드에서 암묵적으로 첫 profile을 활성화하지 않고 목록만 보여주도록 수정
2. 완료: `/profile <number>`, `/profile rN`, `/profile dN`, `/profile pN`, `/profile pin|unpin|rename|delete <number>` 직접 action 지원
3. 완료: 동일한 안전 동작과 명시 action을 `/profile-review`에도 적용하고 help/completion/docs에 반영
4. 완료: 저장된 main profile이 없을 때 현재 provider/model을 첫 profile로 자동 저장하고, `/profile`에서 plan-review, analysis worker/reviewer, specialist 역할별 model profile routing까지 함께 표시
5. 완료: 모델 선택의 대표 창구를 `/model`로 유지하기 위해 `/profile add|save-current` 및 `/profile-review add|save-current` 노출 제거
6. 완료: legacy `api_key`를 provider별 key store로 자동 보강하고, 사용자 설정 저장 시 빈 key가 기존 API key를 덮어쓰지 않도록 보존 경로 추가
7. 완료: main model 변경 시 provider별 저장 key를 `activateProvider`에서 재사용하고, 다른 model 선택은 기존 profile을 덮지 않고 새 profile로 추가되도록 회귀 테스트 추가
8. 완료: main model 변경이 명시 role model profile을 덮지 않도록 회귀 테스트를 추가하고, 상속 중인 역할은 `not configured; follows ...`로 표시해 실제 변경과 구분
9. 완료: main profile이 plan_reviewer, analysis_worker, analysis_reviewer, specialist subagent model set을 함께 저장하고, `/profile`에서 profile별 role model set을 표시하며 profile 활성화 시 전체 model set을 복원
10. 완료: 사용자 전역 profile과 workspace profile을 로드 시 병합하고, 설정 저장 payload에 profile 배열이 없거나 일부만 있어도 기존 main/review profile을 보존 또는 병합하도록 회귀 테스트 추가

목표:
- 일반 code review가 아니라 anti-cheat/security 전용 review mode를 제공한다.

권장 프로파일:
1. bypass surface review
2. trust boundary review
3. tamper resistance review
4. forensic blind spot review
5. kernel safety review
6. user/kernel boundary review
7. Unreal cheat surface review

효과:
- 일반적 "버그 찾기"보다 위협 모델 기반 리뷰를 빠르게 반복할 수 있다.

### P2. Automations

목표:
- Codex처럼 반복 작업을 자동화하되, 운영 현실 중심으로 설계한다.

현재 구현 상태:
1. 완료: `SessionAutomation`을 session JSON에 저장한다.
2. 완료: `/automation [list|status]`, `/automation add recurring-verification [/verify args]`, `/automation add pr-review [/review-pr]`, `/automation run <id>`, `/automation pause|resume|remove <id>`를 추가했다.
3. 완료: `/review-pr`가 로컬 PR review automation report를 `.kernforge/pr_review/latest.md`에 생성한다.
4. 완료: proactive suggestion이 verification gap과 dirty diff를 보고 recurring verification/PR review automation 등록을 제안한다.
5. 남음: 실제 시간 기반 scheduler, GitHub PR API 연동, recurring background monitor, 실패 알림/digest는 아직 없다.

우선 자동화 대상:
1. 완료/MVP: recurring verification slot
2. 완료/MVP: PR security review report
3. nightly verification digest
4. recurring telemetry anomaly scan
5. driver signing readiness check
6. weekly memory prune and summarize
7. PR security review autopilot with GitHub API

주의:
- 로컬 MVP는 들어갔지만, 운영 자동화로 보려면 scheduler, notification, GitHub/issue tracker 연동이 더 필요하다.

### P1. Desktop UX App Shell

목표:
- 현재의 Windows 중심 CLI를 유지한 채, 실사용 가능한 데스크탑 UX를 가진 앱 셸로 확장한다.

핵심 방향:
1. 기존 Go 코어는 유지한다.
2. UI 셸은 WebView2 기반 데스크탑 프레임워크로 감싼다.
3. 권한이 필요한 Windows 기능은 UI 프로세스와 분리한다.
4. 기존 diff preview, viewer, evidence, verification 흐름을 앱 안의 화면으로 재구성한다.

권장 기술 스택:
1. app shell
- `Wails`

2. frontend
- `React`
- `TypeScript`
- `Vite`

3. state
- `Zustand` 또는 `Redux Toolkit`

4. code and diff surface
- `Monaco Editor`

5. large table and dashboard
- `TanStack Table` 또는 `AG Grid`
- `ECharts`

6. local persistence
- `SQLite`

7. privileged runtime
- `Windows Service` 또는 별도 elevated helper
- `named pipe` 또는 로컬 `gRPC`

추천 이유:
1. 현재 Kernforge는 이미 Go 기반이라 재사용률이 가장 높다.
2. 저장소 안에 이미 WebView2 diff preview와 Windows viewer가 있어 기술 방향이 자연스럽다.
3. Electron보다 가볍고, Tauri보다 현재 Go 코어 통합이 단순하다.

권장 아키텍처:
1. desktop UI
- React 기반 대시보드, 세션, evidence, verification, investigation 화면

2. core worker
- 기존 Kernforge agent, tool registry, analysis, verify, memory, hooks 실행

3. privileged broker
- driver, service, ETW, symbol, memory inspection, protected target 관련 작업 분리

4. optional kernel component
- 정말 필요한 anti-cheat or telemetry 기능만 최소 범위로 유지

이 아키텍처가 중요한 이유:
1. UI 크래시와 고권한 작업을 분리할 수 있다.
2. anti-cheat/security 기능의 권한 경계를 명확히 만들 수 있다.
3. 서비스형 helper를 통해 운영 현실에 맞는 복구, 재시도, 로그 수집이 쉬워진다.

우선 앱에서 먼저 살릴 UX:
1. session explorer
2. project analysis dashboard
3. evidence dashboard
4. verification history
5. selection-aware diff review
6. tracked feature workspace view
7. live target status panel

단계별 구현 제안:
1. Stage A
- 기존 Go 엔진을 UI 친화 API 계층으로 묶는다.
- 장기 실행 작업에 progress/event stream을 붙인다.

2. Stage B
- Wails 셸과 React frontend를 붙인다.
- session, analyze-project, verify, evidence 화면을 먼저 연결한다.

3. Stage C
- 기존 preview/viewer를 앱 내부 diff surface로 통합한다.
- Monaco 기반 읽기/리뷰/selection sync를 붙인다.

4. Stage D
- privileged broker와 desktop UI를 분리한다.
- 관리자 권한이 필요한 Windows 작업은 broker를 통해서만 실행한다.

5. Stage E
- installer, code signing, WebView2 bootstrap, update 전략을 정리한다.

대안 비교:
1. `Wails`
- 가장 추천
- 기존 Go 코어를 거의 그대로 살릴 수 있다.

2. `Tauri + Go sidecar`
- 가능하지만 Rust 셸과 Go sidecar를 함께 운영해야 해서 복잡도가 높다.

3. `Electron`
- UI 생태계는 좋지만 메모리, 패키지 크기, 운영 비용이 크다.
- Kernforge의 Windows low-level tooling 방향과는 우선순위가 맞지 않는다.

## 5. 추천 로드맵

### Phase 1

기간:
- 완료된 MVP, 다음은 portal 고도화

목표:
- project analysis를 실제 문서화 플랫폼으로 만들고, `/fuzz-func`가 그 산출물을 바로 이어받게 한다.

포함:
1. 완료: documentation writer MVP
2. 완료: `ARCHITECTURE.md`, `SECURITY_SURFACE.md`, `API_AND_ENTRYPOINTS.md`, `BUILD_AND_ARTIFACTS.md`, `VERIFICATION_MATRIX.md`, `FUZZ_TARGETS.md`, `OPERATIONS_RUNBOOK.md`, `INDEX.md` 생성
3. 완료: `/analyze-project --docs`, `/analyze-project --path <dir>`, `/docs-refresh`, `/analyze-dashboard` 명령
4. 완료: `/analyze-project --mode surface` 정식 노출
4. 완료: `/analyze-project`의 goal을 선택값으로 바꾸고 생략 시 mode/path 기반 기본 goal을 자동 생성
4. 완료: non-map 모드가 이전 `map` run의 knowledge pack/source anchor를 baseline context로 재사용
5. 완료: fuzz target catalog에서 `/fuzz-func` ranking boost
6. 완료: verification matrix에서 `/verify` planner step 생성
7. 완료: docs manifest를 evidence와 persistent memory에 기록
8. 완료: help와 command completion 갱신

완료 기준:
1. 달성: 1회 분석으로 `.kernforge/analysis/latest`에 사람이 읽을 수 있는 문서 세트가 생성됨
2. 달성: 문서 섹션마다 source anchor, confidence, stale/reused 상태가 표시됨
3. 달성: fuzz target 후보가 build context 수준과 함께 정렬되어 출력됨
4. 달성: `/fuzz-func @<path>` 또는 후보 명령으로 source-only finding과 harness artifact가 이어짐
5. 달성: dashboard HTML로 분석 결과를 탐색할 수 있음
6. 달성: generated docs가 evidence, memory, verification planner, fuzz discovery에 재사용됨

### Phase 2

기간:
- 4~8주

목표:
- 퍼징 전문 워크벤치의 핵심 루프를 만든다.

포함:
1. `FuzzCampaign` 모델과 `.kernforge/fuzz/<campaign-id>/` manifest 표준화
2. corpus/crash/coverage/report directory 구조 표준화
3. source-only counterexample -> seed corpus 변환
4. libFuzzer 우선 native execution path 안정화
5. crash triage report, crash hash, stack/source anchor 연결
6. fuzz finding을 evidence store와 verification history에 자동 기록
7. `fuzz campaign` 계열 명령 MVP

완료 기준:
1. campaign을 만들고 재개해도 corpus, crash, coverage, report 맥락이 유지됨
2. 부분 완료: source-derived seed가 deterministic corpus artifact로 남으며, 다음 단계에서 native run input으로 투입한다.
3. crash 발생 시 minimized input 후보, stack/source anchor, suspected invariant가 report에 남음
4. evidence dashboard에서 fuzz finding을 검색할 수 있음

### Phase 3

기간:
- 8주+

목표:
- 분석/문서화와 퍼징 결과를 운영 workflow와 팀 협업으로 확장한다.

포함:
1. security-aware verification planner 고도화
2. evidence graph memory
3. incident replay bundle
4. live Windows target helper 도구
5. desktop app shell MVP
6. automations
7. GitHub/PR security review automation
8. optional cloud delegation
9. privileged broker integration
10. installer and signing pipeline

## 6. 기능 매트릭스

점수 기준:
- 1 = 거의 없음
- 2 = 제한적
- 3 = 보통
- 4 = 강함
- 5 = 매우 강함

| 기능 축 | Kernforge 현재 | Claude Code | Codex | 권장 방향 |
| --- | --- | --- | --- | --- |
| 전체 프로젝트 분석/문서화 | 5 | 3 | 3 | P0 핵심 제품축, portal/search 고도화 |
| structural index / semantic graph | 4 | 2 | 2 | P0 문서화와 fuzz target discovery에 연결 |
| source-level function fuzzing | 4 | 1 | 1 | P0 전문 워크벤치로 확장 |
| native fuzz campaign 관리 | 2 | 1 | 1 | P0/P1 corpus, crash, coverage lifecycle |
| 편집 안전성 | 5 | 3 | 4 | 유지 및 고도화 |
| checkpoint/rollback | 5 | 2 | 3 | 확실한 차별화 유지 |
| verification orchestration | 4 | 3 | 3 | generated verification matrix 기반으로 확장 |
| verification history/dashboard | 4 | 2 | 2 | 차별화 유지 |
| persistent memory | 4 | 3 | 4 | analysis docs evidence/memory 연결 완료, evidence graph로 상향 |
| conversational situation awareness | 4 | 4 | 5 | recent event/error grounding, situation snapshot, suggestion memory, task graph 연결 완료 |
| selection-first workflow | 5 | 2 | 3 | 강점 유지 |
| hooks/policy runtime | 4 | 5 | 3 | 두 핵심축의 safety gate로 고도화 |
| subagents | 4 | 5 | 4 | 분석/문서화와 fuzz campaign specialist로 고도화 |
| automations | 3 | 2 | 4 | 로컬 `/automation` MVP 완료, scheduler/cloud job은 P2 |
| GitHub review automation | 2 | 2 | 4 | 로컬 `/review-pr` report MVP 완료, GitHub API 연동은 P2 |
| Windows security tooling | 3 | 1 | 2 | 분석, fuzz, evidence를 잇는 차별화 |
| anti-cheat specialization | 3 | 1 | 1 | 분석 문서와 fuzz profile 중심으로 집중 |
| desktop UX shell | 2 | 2 | 4 | Phase 3 이후 Go core 유지형 Wails app으로 확장 |

## 7. 현재 코드 구조 기준 구현 진입점

### Project Intelligence And Documentation

주요 파일:
- `analysis_project.go`
- `analysis_docs.go`
- `analysis_dashboard.go`
- `analysis_docs_reuse.go`
- `analysis_context.go`
- `analysis_context_v2.go`
- `analysis_context_v2_graph.go`
- `analysis_index.go`
- `analysis_index_v2.go`
- `analysis_build_alignment.go`
- `analysis_symbol_anchor.go`
- `analysis_prompt_semantic.go`
- `analysis_sharding_semantic.go`
- `main.go`
- `completion.go`
- `verify.go`
- `commands_fuzz_func.go`

권장 추가 파일:
- `analysis_docs_portal.go`
- `analysis_docs_search.go`
- `analysis_trust_boundary.go`
- `analysis_docs_vector.go`
- `analysis_docs_schema_test.go`

주요 연결 지점:
1. `ProjectAnalysisRun`
- snapshot, shard documents, final document, knowledge pack, semantic index, vector corpus를 문서 생성 입력으로 사용한다.

2. `KnowledgePack`
- subsystem, dependency, project edge, Unreal metadata를 `ARCHITECTURE.md`와 `BUILD_AND_ARTIFACTS.md`로 내린다.

3. `SemanticIndexV2`
- symbol anchor, call edge, build ownership edge, overlay edge를 `API_AND_ENTRYPOINTS.md`, `SECURITY_SURFACE.md`, `FUZZ_TARGETS.md`로 내린다.

4. `VectorCorpus`
- generated docs와 shard documents를 후속 retrieval corpus로 재수집한다.

5. `persistRun`
- `.kernforge/analysis/<run-id>/docs`와 `.kernforge/analysis/latest/docs`를 함께 갱신한다.

6. `writeAnalysisDocs`
- deterministic generated docs, manifest, fuzz target catalog, verification matrix를 생성한다.

7. `writeAnalysisDashboard`
- run별 dashboard HTML과 latest dashboard HTML을 생성한다.

8. `recordLatestAnalysisDocsArtifacts`
- generated docs를 evidence record와 persistent memory record로 기록한다.

9. `loadLatestAnalysisDocsManifest`
- verification planner와 fuzz target discovery가 latest docs manifest를 공유 입력으로 사용한다.

### Fuzzing Workbench

주요 파일:
- `commands_fuzz_func.go`
- `commands_fuzz_func_test.go`
- `analysis_index_v2.go`
- `analysis_context_v2_graph.go`
- `shell_background.go`
- `evidence_store.go`
- `verification_history.go`

권장 추가 파일:
- `fuzz_campaign.go`
- `fuzz_campaign_store.go`
- `fuzz_corpus.go`
- `fuzz_crash.go`
- `fuzz_coverage.go`
- `fuzz_harness.go`
- `fuzz_evidence.go`
- `fuzz_campaign_test.go`

주요 연결 지점:
1. `FunctionFuzzRun`
- 단일 실행 결과를 유지하되 campaign/finding/corpus/crash 계층의 하위 artifact로 편입한다.

2. `FunctionFuzzExecution`
- native run 상태, compile context, log path, corpus/crash dir, background job id를 campaign status로 승격한다.

3. `functionFuzzBuildSourceExcerpt`와 observation/scenario 계층
- source-only finding을 seed corpus, suspected invariant, crash triage template로 재사용한다.

4. `shell_background.go`
- 장시간 fuzz execution, log tail, stop/resume, crash count polling에 사용한다.

5. `evidence_store.go`
- crash/finding/campaign summary를 evidence entity로 기록한다.

### Hook Engine

주요 파일:
- `tools.go`
- `main.go`
- `verify.go`
- `provider.go`

권장 추가 파일:
- `hooks.go`
- `hooks_policy.go`
- `hooks_runtime.go`
- `hooks_test.go`

주요 연결 지점:
1. tool 실행 전후
- `ToolRegistry.Execute`

2. 편집 전후
- `Workspace.BeforeEdit`
- `Workspace.ConfirmEdit`

3. verification 전후
- verification 실행 entry point

4. git push / create pr 전
- git tool 구현부

5. 세션 시작/종료
- `main.go` runtime 초기화/종료 지점

### Subagent Framework

주요 파일:
- `agent.go`
- `main.go`
- `config.go`
- `provider.go`

권장 추가 파일:
- `subagent.go`
- `subagent_registry.go`
- `subagent_profiles.go`
- `subagent_test.go`

필수 요소:
1. subagent 정의 구조체
2. 모델/provider override
3. tool allowlist
4. memory scope
5. file/selection scope
6. delegation result schema

### Security-Aware Verification

주요 파일:
- `verify.go`
- `verify_policy.go`
- `verification_history.go`

권장 추가 파일:
- `verify_classifier.go`
- `verify_security_rules.go`
- `verify_artifacts_windows.go`

필수 요소:
1. changed file classifier
2. artifact classifier
3. verification recommendation scorer
4. failure signature clustering

### Evidence Graph Memory

주요 파일:
- `persistent_memory.go`
- `memory_policy.go`

권장 추가 파일:
- `evidence.go`
- `evidence_store.go`
- `evidence_query.go`
- `evidence_test.go`

권장 전략:
1. 기존 persistent memory는 유지
2. 별도 evidence store를 추가
3. 점진적으로 두 저장소를 연결

### Conversational Runtime Memory

주요 파일:
- `agent.go`
- `session.go`
- `main.go`
- `completion.go`
- `provider.go`
- `shell_background.go`
- `persistent_memory.go`
- `analysis_project.go`
- `command_handoff.go`

권장 추가 파일:
- `conversation_events.go`
- `conversation_state.go`
- `context_assembler.go`
- `turn_intent.go`
- `recent_error_resolver.go`
- `conversation_compaction.go`
- `conversation_events_test.go`
- `context_assembler_test.go`
- `recent_error_resolver_test.go`

필수 요소:
1. append-only conversation event log
2. provider/tool/shell error normalization
3. active conversation state snapshot
4. current-turn intent classifier
5. recent error resolver
6. context assembly priority policy
7. compact/resume working memory schema
8. internal routing marker suppression

주요 연결 지점:
1. 사용자 입력 수신 직후
- `main.go`의 interactive prompt loop에서 `ConversationEvent{kind=user_message}` 기록

2. assistant 응답 생성 전
- `agent.go` 또는 prompt assembly 지점에서 `ContextAssembler`를 호출해 recent window, active state, relevant event, project cache를 우선순위대로 주입

3. tool/provider 오류 발생 시
- `provider.go`, `tools.go`, `shell_background.go`에서 raw error를 정규화해 `ConversationEvent{kind=provider_error|tool_error|command_error}`로 기록

4. handoff 출력 시
- `command_handoff.go`에서 다음 권장 명령과 artifact ref를 active state에 반영

5. session compact/resume 시
- `session.go`가 unresolved goal, last error, pending next step, open artifacts를 versioned summary로 보존

### Live Windows Target Workflow

주요 파일:
- `tools.go`
- `input_windows.go`
- `viewer_windows.go`
- `preview_windows.go`

권장 추가 파일:
- `tools_windows_security.go`
- `windows_symbols.go`
- `windows_etw.go`
- `windows_driver_ops.go`

주의:
- 이 축은 권한/위험도가 높으므로 hook engine 이후에 붙이는 편이 안전하다.

### Desktop UX App Shell

주요 파일:
- `main.go`
- `ui.go`
- `viewer_windows.go`
- `preview_webview_windows.go`
- `preview_html.go`
- `diff_html.go`

권장 추가 디렉토리:
- `app/desktop`
- `app/frontend`
- `app/frontend/src`
- `app/frontend/src/components`
- `app/frontend/src/pages`
- `app/frontend/src/state`
- `app/frontend/src/lib`

권장 추가 파일:
- `app_desktop.go`
- `app_events.go`
- `app_sessions.go`
- `app_analysis.go`
- `app_evidence.go`
- `app_verify.go`
- `app_privileged_client_windows.go`

필수 요소:
1. UI에서 직접 호출 가능한 안정된 core API
2. long-running task progress stream
3. selection and diff state bridge
4. evidence and verification query API
5. privileged broker 연결 abstraction

주의:
1. UI에서 Windows privileged 기능을 직접 호출하지 않는다.
2. 기존 CLI 워크플로우는 유지하고, desktop shell은 병행 제공하는 편이 안전하다.
3. 초기 단계에서는 CLI를 orchestration source of truth로 두는 것이 현실적이다.

## 8. 구현 순서 제안

### 추천 1순위

1. 완료: fuzz campaign model
2. 완료: `.kernforge/fuzz/<campaign-id>/` manifest와 corpus/crash/coverage/report/log layout 표준화
3. 완료: source-only counterexample seed artifact 변환
4. 완료: `/fuzz-campaign run` 중심의 간결한 CLI/help/completion 노출
5. 완료: native run 상태와 background job 결과를 campaign native result로 연결
6. 완료: crash triage report와 minimization command 생성

이유:
- Project Intelligence And Documentation MVP와 FuzzCampaign manifest/layout/one-command seed automation MVP가 완료되었으므로 다음 병목은 seed artifact를 native fuzzing execution과 evidence로 승격하는 단계다.
- generated `FUZZ_TARGETS.md`와 docs manifest는 campaign seed로 들어오고, saved `/fuzz-func` scenario는 corpus artifact로 승격되므로, 다음에는 native run orchestration, crash/coverage feedback loop를 붙인다.

### 추천 2순위

1. 완료: analysis dashboard portal 고도화
2. 완료: generated docs cross-search
3. 완료: source anchor deep link, stale section diff, evidence/memory drill-down
4. 완료: dashboard trust-boundary graph와 attack-flow view
5. 완료: generated docs retrieval/vector corpus 재수집
6. 완료: docs manifest schema versioning과 backward compatibility policy
7. 완료: data-flow edge 정밀화와 generated docs graph section
8. 완료: 변경 diff view와 graph stale marker 연동
9. 완료: fuzz campaign artifact/evidence graph schema와 finding issue lifecycle 연결
10. 완료: coverage feedback 입력 포맷 확장. libFuzzer/llvm-cov/LCOV/JSON coverage summary를 campaign manifest의 coverage report로 수집한다.

이유:
- 현재 dashboard는 HTML overview로 충분하지만, 대형 보안 프로젝트에서는 문서 포털과 근거 추적 UX가 핵심 차별점이 된다.
- docs, evidence, verification, fuzz target을 한 화면에서 왕복할 수 있어야 "프로젝트 지식 베이스"라는 포지션이 완성된다.

### 추천 3순위

1. security-aware verification planner 고도화
2. evidence graph memory
3. hook engine hardening
4. specialist profiles 고도화
5. live Windows target workflow
6. incident replay bundle
7. desktop app shell MVP
8. automations
9. privileged broker hardening and installer/signing

이유:
- 강력하지만 운영/권한/UX 복잡도가 올라간다.
- 분석 문서 MVP는 안정화되었지만, fuzz campaign artifact와 evidence graph schema가 먼저 자리잡아야 UX/automation이 단순한 화면이나 스케줄러가 아니라 실제 운영 자산이 된다.

## 9. 내가 추천하는 최종 제품 메시지

추천 메시지:

"Kernforge is the project intelligence and fuzzing workbench for Windows security and anti-cheat engineering."

더 실무형으로 풀면:

"Kernforge helps security engineers map large codebases, generate durable security documentation, discover fuzz targets, run source-to-native fuzzing workflows, and preserve evidence across Windows and anti-cheat projects."

핵심 차별점 한 줄:
- understand large projects
- document security surfaces
- fuzz from source to native execution
- verify and preserve evidence

## 10. 바로 실행 가능한 다음 작업

가장 추천하는 다음 액션은 아래 항목이다.

1. 완료: source-only finding to seed corpus artifact 변환
- branch predicate와 counterexample를 deterministic JSON seed 후보로 저장
- seed provenance에 source anchor와 target symbol 기록
- generated `FUZZ_TARGETS.md` 후보에서 campaign seed 생성

2. 완료: `/fuzz-campaign`에 intent-driven run automation 추가
- `/fuzz-campaign`은 다음 권장 단계를 보여줌
- `/fuzz-campaign run`은 campaign 생성, latest run attach, seed promotion을 자동 수행
- 내부 expert action은 유지하되 기본 help/completion에서는 숨김

3. 완료: native fuzz execution 결과를 evidence에 기록
- run summary
- corpus/crash directory
- sanitizer/coverage artifact
- crash fingerprint
- suspected invariant

4. 완료: crash triage report와 minimization command 생성
- crash hash
- stack/source anchor
- minimized input 후보
- repro command

5. 완료: fuzz 결과를 verification planner와 tracked feature workflow에 연결
- `.kernforge/fuzz/<campaign-id>/manifest.json`의 native result를 `/verify` targeted step으로 재사용
- fuzz evidence source handoff를 `/fuzz-campaign`으로 연결
- active feature status에서 crash/failure campaign을 close 전 gate로 표시

6. 완료: analysis dashboard portal 고도화
- docs cross-search
- source anchor deep link
- stale section diff
- evidence/memory drill-down

7. 완료: analysis portal graph MVP
- trust-boundary graph
- attack-flow view

8. 완료: analysis docs retrieval 고도화
- generated docs vector corpus 재수집

9. 완료: docs manifest schema policy
- schema versioning
- backward compatibility policy

10. 완료: generated docs graph 고도화
- data-flow edge 정밀화
- docs 본문 graph section

11. 완료: generated docs diff 고도화
- 변경 diff view 정밀화
- graph section stale marker 연동

12. 완료: fuzz campaign finding lifecycle 고도화
- campaign artifact, evidence, verification gate, tracked feature gate를 하나의 finding lifecycle로 묶기
- crash/coverage/native result가 issue 상태와 source anchor를 공유하도록 schema 정리

13. 완료: finding dedup
- 중복 finding/crash를 fingerprint, source anchor, invariant 기준으로 병합

14. 완료: coverage feedback 입력 포맷 고도화
- coverage feedback 입력을 libFuzzer run log, llvm-cov text, LCOV, JSON coverage summary format으로 확장

15. 신규: conversation event log MVP
- user/assistant/tool/provider/shell/background event를 append-only로 저장
- event마다 turn id, correlation id, severity, artifact ref, short summary를 기록
- provider 429, timeout, auth failure, quota exhaustion을 정규화

16. 신규: recent error resolver
- "방금 에러", "왜 실패", "아까 오류" 질문을 `diagnose_recent_error` intent로 분류
- 최근 provider/tool/shell error event를 찾아 원인, 영향, 다음 조치를 설명
- cached analysis fast-path보다 recent error event를 우선

17. 신규: active conversation state
- last command/result/error, active workflow, pending handoff, latest analysis/fuzz/verify artifact를 작은 snapshot으로 유지
- compact/resume 이후에도 이 snapshot을 먼저 복원

18. 신규: context assembler priority policy
- recent turn, active state, recent event, active artifact, project analysis cache, long-term memory 순으로 context budget을 배분
- current-turn diagnostic/continuation intent에서는 오래된 project cache가 답변을 가로채지 않도록 guard

19. 신규: internal marker suppression
- `NEEDS_TOOLS`, `Cached analysis fast-path`, JSON routing marker, debug confidence label이 사용자 답변에 직접 출력되지 않도록 final answer sanitizer 추가
- 필요하면 "직전 로그 기준"처럼 사용자에게 의미 있는 grounding 문구만 남김

20. 완료: suggestion accept 실행 루프
- `confirm` 모드에서 `/suggest accept <id>`가 safe command dispatcher를 통해 허용된 명령만 실행
- 실행 성공 시 suggestion status를 `executed`로 전환하고 TaskGraph node를 completed로 동기화

21. 완료: suggestion preference memory
- accepted/dismissed suggestion을 persistent memory의 `suggestion-preference` category로 기록
- session을 넘어 사용자 선호와 거절 이력을 재사용할 기반 마련

22. 완료: automation MVP
- `/automation`으로 recurring verification 및 PR review automation slot을 저장/실행/일시정지/삭제
- `/review-pr`로 `.kernforge/pr_review/latest.md` report 생성
- proactive suggestion이 automation 등록을 next action으로 제안

이 흐름이 정리되면, 현재 구현된 `fuzz_campaign.go`, `commands_fuzz_func.go`, `evidence_store.go`, `shell_background.go`, `analysis_docs.go`, `verify.go`, `feature_workflow.go`에 더해 새 conversation runtime 계층이 붙으면서 source-to-native fuzzing workbench와 Codex급 대화형 agent 경험이 함께 열린다.

## Sources

- Claude Code overview: https://docs.anthropic.com/en/docs/agents-and-tools/claude-code/overview
- Claude Code subagents: https://code.claude.com/docs/en/sub-agents
- Claude Code hooks: https://docs.anthropic.com/en/docs/claude-code/hooks
- OpenAI Codex help overview: https://help.openai.com/en/articles/11369540
- OpenAI Codex repository: https://github.com/openai/codex
