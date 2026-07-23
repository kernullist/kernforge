# CHANGELOG

의미 있는 변경 이력. 날짜는 절대 형식(YYYY-MM-DD), 최신 항목이 위에 온다.

## 2026-07-24

### P0 보안 워크벤치 (fuzz / driver / posture)

- 연구: [[docs/research/2026-07-24-security-capability-review.md]], 로드맵: [[ROADMAP_kor.md]] P0 Fuzzing Workbench 보강.
- **Harness build-repair**: 빌드 실패를 `missing-include|unresolved-symbol|wdk-macro|abi-or-link|unknown`으로 분류하고 최대 1–3회 self-repair 후 durable `BuildBlockers`/build log 기록.
- **Crash feasibility**: native crash를 `target_plausible|spurious|unknown`으로 게이트. harness-only stack은 finding `spurious` 격리, verification/`block_close` 비요구, source-scan draft·`native-confirmed` 승격 차단. finding merge 시 Feasibility 보존.
- **IOCTL contract + sequence seeds**: fuzz artifact `ioctl_contract.json` (codes/METHOD_*/buffer length/dispatch anchors), multi-call open/ioctl/close sequence seed, `/fuzz-campaign` promote (`ioctl_sequence`).
- **`/investigate start platform-security`**: Secure Boot, VBS, HVCI/Memory Integrity, test-signing, driver signature enforcement, TPM readiness를 snapshot attributes·`platform_security` finding으로 수집 (불가 시 `unavailable`). multi-collector merge는 구체 값(`enforced` 등)을 약한 `observed`가 덮지 않음.
- **`/create-driver-poc` security handoff**: 완료 시 source-scan/fuzz-func/campaign/verify/platform-security/signing/Driver Verifier 안내 + `.kernforge/security/workflow_seed.json`.
- 테스트: `cmd/kernforge/p0_security_workbench_test.go`. 문서: FEATURE 가이드(한/영), README, driver playbook, ROADMAP 동기화.

### Goal 단일 명령 UX

- 공개 서브커맨드 제거: `run|status|audit|complete|cancel` 및 `--no-run`.
- `/goal <objective>` = Spec/Slice 설계 후 즉시 자율 루프 (끝까지 또는 block).
- bare `/goal` = incomplete 재개, 없으면 최신 스냅샷.
- 루프 중 `printGoalProgressSnapshot` (iteration/slice/verify/semantic/complete/block).
- Esc 인터럽트 유지; 교체는 새 `/goal` + 확인.
- 리뷰 수정: `run the …` 같은 목표 문장 선두 예약어 오인 방지; terminal 상태 스냅샷 중복 출력 제거; QUICKSTART/README/FEATURE 가이드 동기화.

## 2026-07-23

### Goal OS PR5: goal worktree isolation (opt-in)

- `/goal --worktree|--isolated`: 기존 세션 worktree 재사용 또는 `WorktreeManager`로 생성·attach.
- GoalState에 `worktree_id/root/branch` 기록, status 출력. 병렬 multi-worktree 실행은 보류.

### Goal OS PR4: research_mode + cost/events

- 계획: [[docs/plan/2026-07-23-autonomous-goal-system.md]]
- `--research` / `--research-mode none|bounded|aggressive`로 AcceptanceSpec.ResearchMode 강제.
- research_mode 활성 시 implement 프롬프트에 `$goal-loop` 조사 지침 주입 (로컬 버그픽스는 기본 none).
- `GoalEvent` 링버퍼 + cost 요약(tokens/time/iterations/slices/research); status·complete·block 출력.

### Goal OS PR3: Runner v2 slice OPAVR + partial

- 계획: [[docs/plan/2026-07-23-autonomous-goal-system.md]]
- 매 iteration: ready slice 선택 → implement/review/verify/audit/semantic 스코프.
- slice semantic APPROVED 시 해당 slice 완료; 남은 slice 있으면 goal 유지·다음 ready로 계속.
- 전 slice 완료 후에만 goal complete. max-iter/block 시 partial delivery 요약 기록.
- 후속: research_mode + cost/events (PR4).

### Goal OS PR2: Slice DAG 모델 + planner parse

- 계획: [[docs/plan/2026-07-23-autonomous-goal-system.md]]
- `GoalSlice` / `GoalSlicePlan`: outcome/scope/acceptance/risk/depends_on, topo order, ReadySlices.
- `parseGoalSlicePlanFromText`: goal-to-slice-planner 형식 파싱; flat numbered list는 레거시 Plan 유지.
- `/goal --no-run` 플래너 프롬프트가 slice 형식 우선; 실패·`--run` 시 single-slice fallback.
- 아티팩트: `latest.slices.md` + `<id>.slices.md`, goal markdown에 `## Slice DAG`.
- 후속: Runner v2 slice OPAVR (PR3).

### Goal OS PR1: AcceptanceSpec 컴파일러

- 계획: [[docs/plan/2026-07-23-autonomous-goal-system.md]], 연구: [[docs/research/2026-07-23-autonomous-goal-systems.md]]
- `GoalAcceptanceSpec` / `compileGoalAcceptanceSpec`: 목표+`--criteria` → substance criteria, risk_class, research_mode, non-goals.
- Semantic/implement 프롬프트가 **primary acceptance checklist**를 강제; process-meta CompletionCriteria는 secondary.
- Progress fingerprint에 criteria 포함, 검증 없는 파일 churn 점수 하향.
- 후속: Slice DAG (PR2), Runner v2 (PR3).

### humanize-doc 바이너리 1급 내장

- 계획서: [[docs/plan/2026-07-23-builtin-humanize-doc.md]]
- `humanize-doc`을 go:embed에서 항상 카탈로그에 병합. seed 디렉터리가 비어 있어도 `$humanize-doc` / `load_skill` 가능.
- `ai-tells.md`를 스킬 본문에 built-in copy로 인라인 — supporting file 없이도 절차 완결.
- "AI 티 제거" 등 자연어 humanize 의도 시 `$name` 없이 자동 활성화.
- 사용자 커스터마이즈(hash marker 불일치) 디스크 스킬은 보존.
- 스킬 검색: cwd→부모 first-wins + 프로젝트 루트(go.mod/.git)까지. 사용자 홈은 올라가지 않음(Windows TEMP가 홈 아래일 때 `~/.kernforge/skills` 오염 방지).
- 사이드이펙트 수정: (1) 프로젝트 로컬 humanize-doc 무마커 덮어쓰기 금지 — seed 경로만 embed 업그레이드 (2) `$humanize-doc`는 외부 user 요청에 있을 때만 활성화 — envelope 안내 문구만으로는 본문 주입 안 함 (3) always-available builtin만으로는 SelectableCount/매 턴 skill catalog 강제 안 함 (4) humanize 의도 matcher 좁힘 — 코드 식별자/경로 false positive 감소.
- YAML `description: >` folded scalar 파싱 지원.

### 문서 작성 시 전문가 문체 계약 (anti-AI-slop)

- 계획서: [[docs/plan/2026-07-23-expert-document-writing-style.md]], 연구: [[docs/research/2026-07-23-expert-doc-writing-anti-slop.md]]
- document-authoring 턴에만 `Document authoring style contract` 시스템 프롬프트 주입 (`prompts/document_authoring_style.md`). no-ai-slop 패턴 + Microsoft/Google 기술문서 원칙 + 한/영 filler 금지 + 쓰기 전 self-check.
- `request_envelope` document 분기에 전문가 문체·self-check·`$humanize-doc` 안내 추가.
- 내장 `humanize-doc` 스킬: no-ai-slop 패턴 표·eval 자기 검수 단계 정렬 (`references/ai-tells.md` 9절).
- 수정: continuation(`계속`)에서 acceptance contract의 document-authoring 유실 → 문체 계약 미주입 버그 수정. `applyPolicy`를 session context 적용 후에 재실행.
- 수정: 내장 스킬 seed를 hash marker 기반 업그레이드로 전환 — 미커스터마이즈 파일은 새 바이너리로 갱신, 사용자 수정본은 보존. 마커 없는 legacy seed는 1회 마이그레이트.

## 2026-07-22

### 런타임 게이트를 세션 스코프로 전환 (cross-session attach 제거)

- Cursor/Claude Code/Codex와 같이: 새 세션은 `.kernforge/reviews/latest.json`을 게이트에 자동 부착하지 않음. 이 세션의 `LastReviewRun`(또는 명시 provided 리뷰)만 게이트 입력.
- `completionAuditReviewGate`도 동일하게 디스크 latest 폴백 제거.
- `/clear` `/reset` `/new`는 대화뿐 아니라 `LastReviewRun`·세션 gate dismissal/ledger·pending recovery를 비움.
- `/gate clear` 기본 scope를 session으로 변경. `/gate restore`는 해제한 리뷰를 **이 세션에만** 다시 붙임. 리뷰 파일은 히스토리로 유지.
- 리뷰 수정: `/gate restore`가 리뷰를 다시 못 붙여도 성공으로 보이던 문제 — clear 시 세션에 리뷰를 stash하고, 복구 불가 시 dismissal을 유지한 채 실패.
- 리뷰 수정: 문서 턴에서 mismatch 후 성공한 non-`write_file` 편집이 `replace_in_file`/`apply_patch`를 다시 열던 경로 차단.

### 게이트 Everyday WARN CTA 명확화

- 비ready gate footer가 “번호로 고르면 됩니다”라고만 말해, 화면에 선택지가 없는 상태(세션 시작·status)에서 사용자가 다음에 할 일을 알 수 없던 문제를 고침.
- CTA를 “지금은 편집·읽기·분석 가능 / 완료·커밋을 시도하면 선택지(리뷰 갱신·이번만 무시 등)”로 바꿈. 슬래시 커맨드 광고는 계속 없음.

### 문서 보강 요청의 repeated-tool stall 루프 수정

- `보강`/`보완`/`improve the document` 등을 document-authoring 동사에 맞춰 stall helper와 envelope/RF-001 분류를 정렬.
- 문서가 산출물이고 코드·소스 경로가 없을 때 bare `수정해`/`문제점 … 수정해서 문서를 보강`을 code-fix / `review_then_modify`로 보지 않음 (`코드를 수정`·`main.go 수정`은 유지).
- document/analysis 턴에서 identical tool signature가 abort threshold에 도달하면 stall 카드 전에 `write_file`/`apply_patch` guidance를 한 번 push. 이미 push했거나 non-doc이면 기존 stall 카드.

### edit target mismatch 복구 루프 수정

- `replace_in_file` mismatch 시 path만 던지던 에러에 apply_patch급 진단(expected lines / ambiguous candidates / current content window)을 붙임. 모호 매칭은 `not found`로 접지 않고 `ambiguous`로 구분.
- 문서 산출 턴(`문서를 보강` 등)에서 context patch mismatch 후 reanchor(`read_file` 등)로 `replace_in_file`/`apply_patch`를 다시 열지 않음. `write_file` 성공 시에만 복원.
- 매 mismatch마다 reanchor를 다시 강제해, 재확인 없이 context edit를 연타하며 실패 예산만 태우는 경로를 막음.
- mismatch recovery guidance는 tool error 진단이 있으면 그걸 쓰고, 없으면 방금 읽은 exact lines를 복사하도록 맞춤.

### 스트림 최종 답 중복 출력 제거

- `printAssistant`가 스트림 flush **이후**에 dedup하도록 순서를 바꿈. 기존에는 dedup 통과 후 flush하면서 동일 본문이 두 번(`>> assistant` 블록 2개) 찍혔다.
- 정규화 본문이 서로 포함되거나 토큰 겹침 ≥92%면 재출력 억제 (스트림 sanitize drift 대비).

### Speed 기본 프리셋 + 턴 시작 안내

- 계획서: [[docs/plan/2026-07-22-speed-preset-and-turn-announce.md]]
- 기본 `runtime_preset=speed`: auto-verify off, semantic classifier off, 자동 pre/post review off, project-analysis 주입 off, autocompact 예산 상향.
- `/settings preset speed|balanced|strict`로 번들 전환. strict는 이전 안전망 조합.
- 턴 시작 “무엇을 할지” 안내는 하드코딩 없이 모델 한 줄로 출력. API는 thinking/`ReasoningEffort` 없이 짧은 preflight(최대 8s). CLI만 preflight를 건너뛰고 본 턴 첫 줄을 thought 라인으로 승격 (`openai-codex` API는 preflight 유지).
- 스트림에서 승격한 안내 문구는 `printAssistant` 최종 출력에서 제거해 `>> assistant` 중복 재생을 막음.
- `@file.md` 읽기/평가 요청을 document-authoring으로 오분류하던 문제 수정 (`.md` 언급 ≠ 문서 산출). 읽기/평가는 read-only 경로로 유지해 write 하네스 우회.

### Quiet 기본 출력 (Cursor/Codex/Grok 정렬)

- 계획서: [[docs/plan/2026-07-22-quiet-output-ux.md]]
- 기본 `progress_display=quiet`: 턴 중간은 spinner/footer만, shell body·working notes는 transcript에 남기지 않음.
- `compact`/`auto`/`stream`는 유지. repair 진행은 quiet/compact에서 `N/6 stage` 한 줄.
- 최종 답 표시 시 `Validation:`/`Remaining risk:` 등 checklist 라벨을 사람 말로 완화 (저장 텍스트는 유지).

### 게이트/WARN Everyday 선택형 복구 (A+B)

- 계획서: [[docs/plan/2026-07-22-gate-warn-choice-ux.md]]
- Everyday footer에서 `gate:` status pill 제거. 비ready 시 슬래시 커맨드 없는 한 줄 CTA만 표시.
- final_gate stall 시 번호 선택 카드: 리뷰 갱신 / 이번만 무시(조건부) / 자세히 보기 / 편집만 계속. `/status`·`/gate` 상세는 Expert/Hub 유지.

### 커맨드 표면 단순화 (일상/허브/전문가 3층)

- 계획서: [[docs/plan/2026-07-22-command-surface-simplification.md]]
- 기본 `/help`는 L1 Everyday + L2 Hub만 표시; `/help all`로 전체. 구 top-level은 hidden 별칭으로 유지.
- `/selection`, `/mcp`, `/hooks`, `/settings`, `/analyze`, `/probe` 허브로 관련 커맨드 폴딩.
- Tab completion은 L1+L2를 우선하고, L3는 prefix 매칭 시에만 제안.

### final_answer 게이트 changed-path scope를 턴 판정과 동일화

- 계획서: [[docs/plan/2026-07-21-gate-finalanswer-scope-narrowing.md]], 감사: [[docs/research/overblock-audit.md]] F3 (2026-07-19 후속 이슈 1)
- `runtimeGateChangedPathsForAction`: final_answer 액션의 git changed-files fallback 제거. 추적된 patch scope만 사용 — ambient dirty(세션 밖 WIP)로 stale review / unwaived blocker가 `gate:blocked`를 남기던 비대칭 해소.
- `runtimeGateFinalAnswerShouldUseGitChangedFallback` 삭제. git_write/mcp_write는 전체 트리 scope 유지.
- 회귀 테스트 기대 반전 및 status/hooks/dismissal fixture에 current-turn patch transaction 보강.

## 2026-07-21

### v2 final gate / completion audit의 verification 차단 scope 일관화

- 계획서: [[docs/plan/2026-07-21-finalgate-verification-scope.md]], 감사: [[docs/research/overblock-audit.md]]
- v2 structured final gate: `finalGateVerificationResult`가 모든 verification 실패를 `Unresolved`로 계산하던 것을 patch-scoped 실패(ambient/config·환경성 실패 제외)로 한정. `DecideFinalGate` verification 분기에 정직 고지(disclosure) 탈출 추가 — "verification failed/미실행"을 명시한 최종 답변은 차단하지 않음(legacy turn readiness와 동일 조건). request runtime v2 활성화 시 7/19에 제거한 교착 클래스가 부활할 수 있던 latent 결함 해소.
- completion audit: `completionAuditVerification`/`completionAuditVerificationStatus`가 ambient/config 실패를 `blocked` 대신 `warning`으로 강등 — runtime gate ledger와 동일 기준. 사용자 명시 `VerificationRequired` contract 분기는 엄격 유지.
- 회귀 테스트 6개 추가(실패-재현 확인 후 구현).

### note 수준 finding이 gate blocker로 표면화되는 경로 차단

- 계획서: [[docs/plan/2026-07-21-note-finding-blocker-surface.md]] (2026-07-19 계획서 후속 이슈 2 해결)
- `scopeReviewRunToRequestedRepairFindings`의 fallback 제거: 사용자가 "RF-001 수정해줘"처럼 note 수준(info/advisory) finding을 참조하면 gate 양쪽 목록에 없다는 이유로 `BlockingFindings`에 무조건 승격시키던 날조를 차단. finding은 Findings/RepairFindings 지침 채널에 유지하고 gate는 실제 소속만 반영.
- single-model RF-obligation policy 필터 일원화: `buildSingleModelReviewPolicy`의 `RequiresRFObligationStatus` 산정과 `singleModelPreWritePolicyFindings`의 status 검사가 `reviewRepairFindingsRequiringResolutionStatus`(evidence_gap/test_gap 제외 2-leg 필터)를 사용. carried note finding이 resolution status 부재로 deterministic "lacks repair obligation status" blocker를 유발해 `latest review has unwaived blockers: RF-001`로 표면화되던 경로 차단.
- obligation ledger 필터를 `reviewRepairObligationCandidateFindings` 헬퍼로 추출(동작 변경 없음). status 요구 필터에서 `reviewFindingLooksActionableForRepairGate` leg 제외 — RequiredFix만 있는 note finding("Repeat /review...")까지 잡아내 실물 케이스를 걸러내지 못함을 확인.
- 회귀 테스트 3개 추가.

## 2026-07-19

### 자동 검증 out-of-scope 하드 중단 제거 및 scope 판정 일관화

- 계획서: [[docs/plan/2026-07-19-verify-outofscope-hardstop.md]]
- `verificationOutOfScopeFinalOnly` 하드 블록 제거. out-of-scope 판정 시 모든 후속 도구가 `NOT_EXECUTED`로 차단되고 gate는 같은 실패를 in-scope blocker로 보는 교착(`gate:blocked` + 아무것도 못 함)을 해소.
- 대화형 실행에서는 out-of-scope 판정 시 턴당 1회 사용자에게 질의(`PromptResolveOutOfScopeVerification`): 계속 수리(기본) / ambient risk로 고지하고 마무리. 비대화형은 guided continuation으로 폴Fallback. Finish 선택 시에만 최종 답변으로 수렴하며 도구 차단은 없음.
- `verification_repair_scope.go`: `go test|build|vet ./cmd/app/...` 형태의 구체적 Go 패키지 scope가 변경 파일의 디렉터리를 커버하면 in-scope로 판정(`verificationGoPackageScopeCoversChangedDir`). 같은 패키지 다른 파일의 compile_error가 patch 검증을 막는 경우를 in-scope로 교정. 범용 디렉터리 매칭은 MSBuild sibling 실패가 의도적으로 ambient인 기존 동작을 깨므로 Go 전용으로 제한. workspace/`./...` 패턴과 `cmd/app2` vs `cmd/app` 같은 경계 오매칭 제외.
- `coding_harness.go`: "Unresolved verification failure" blocker가 patch-scoped 실패에만 발동하도록 수정 — out-of-scope 실패가 in-scope 문구 blocker로 오표시되던 문제 해소.
- 루프 상한은 기존 retry budget(같은 실패 fingerprint 2회 → change_strategy, 3회 → escalate_reviewer)에 위임.
- 문서(README/FEATURE_USAGE_GUIDE, 영/한)의 `NOT_EXECUTED` 서술을 새 동작으로 갱신.
- 후속 이슈: gate ledger changed-path 집합과 턴 판정 집합의 차이로 ambient Finish 시 `gate:blocked` advisory가 남을 수 있음 → 2026-07-22 해결([[docs/plan/2026-07-21-gate-finalanswer-scope-narrowing.md]]). note 수준 evidence_gap finding(RF-001)이 "unwaived blockers"로 표면화되는 경로 → 2026-07-21 해결.
