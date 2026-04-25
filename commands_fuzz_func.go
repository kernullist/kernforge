package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultFunctionFuzzMaxEntries = 200
	functionFuzzMaxClosureNodes   = 160
	functionFuzzMaxListedItems    = 12
	functionFuzzMaxSuggestedItems = 5
)

var functionFuzzIdentPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*$`)
var functionFuzzIncludeDirectivePattern = regexp.MustCompile(`(?i)#include\s+[<"][^>\n"]+[>"]`)
var functionFuzzWhitespacePattern = regexp.MustCompile(`\s+`)
var functionFuzzGroundedObservationPattern = regexp.MustCompile(`^grounded in (\d+) source-derived guard or sink observation\(s\)$`)
var functionFuzzGroundedObservationSummaryPattern = regexp.MustCompile(`^grounded in (\d+) source-derived guard/sink observation\(s\)$`)
var functionFuzzInputStatePattern = regexp.MustCompile(`^(.+?) \((.+?)\) = (.+)$`)
var functionFuzzAccessPathPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\s*(?:->|\.)\s*[A-Za-z_][A-Za-z0-9_]*)*`)
var functionFuzzBinaryComparisonPattern = regexp.MustCompile(`(?i)(sizeof\s*\([^)]*\)|0x[0-9a-f]+|\d+|[A-Z_][A-Z0-9_]*|[A-Za-z_][A-Za-z0-9_]*(?:\s*(?:->|\.)\s*[A-Za-z_][A-Za-z0-9_]*)*)\s*(==|!=|<=|>=|<|>)\s*(sizeof\s*\([^)]*\)|0x[0-9a-f]+|\d+|[A-Z_][A-Z0-9_]*|[A-Za-z_][A-Za-z0-9_]*(?:\s*(?:->|\.)\s*[A-Za-z_][A-Za-z0-9_]*)*)`)
var functionFuzzUnaryNegationPathPattern = regexp.MustCompile(`\(\s*!\s*([A-Za-z_][A-Za-z0-9_]*(?:\s*(?:->|\.)\s*[A-Za-z_][A-Za-z0-9_]*)*)`)
var functionFuzzCallNamePattern = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_:]*)\s*\(`)

var functionFuzzKoreanDisplayTextMap = map[string]string{
	"Attacker-controlled size can diverge from the buffer contract on a real copy or probe path":              "공격자가 조작한 크기가 실제 copy/probe 경로에서 버퍼 계약과 어긋날 수 있음",
	"Unexpected control value can push execution into a weakly checked dispatch path":                         "예상 밖 제어값이 약하게 검증된 dispatch 경로로 실행을 밀어 넣을 수 있음",
	"Pointer or state validity can be checked in one place and broken in another":                             "포인터 또는 상태 유효성이 한 곳에서 검사되고 다른 곳에서 깨질 수 있음",
	"Allocation size and later use size can be forced out of sync":                                            "할당 크기와 이후 사용 크기가 어긋나도록 강제될 수 있음",
	"A later failure can unwind after security-relevant state was already published":                          "보안 관련 상태가 먼저 공개된 뒤 나중 실패로 unwind될 수 있음",
	"Observed source-level guard or sink can likely be stressed by contradictory attacker input":              "관찰된 소스 레벨 가드 또는 sink가 모순된 공격자 입력으로 흔들릴 수 있음",
	"high-confidence source-only hypothesis":                                                                  "높은 신뢰도의 소스 전용 가설",
	"medium-confidence source-only hypothesis":                                                                "중간 신뢰도의 소스 전용 가설",
	"low-confidence source-only hypothesis":                                                                   "낮은 신뢰도의 소스 전용 가설",
	"real memory-transfer sink is present on the same path":                                                   "같은 경로에 실제 메모리 전송 sink가 있음",
	"real user-buffer probe is present on the same path":                                                      "같은 경로에 실제 유저 버퍼 probe가 있음",
	"real size or boundary guard is present on the same path":                                                 "같은 경로에 실제 크기 또는 경계 가드가 있음",
	"real selector-driven dispatch is present on the same path":                                               "같은 경로에 실제 선택자 기반 dispatch가 있음",
	"partial-success and cleanup behavior share the same path":                                                "부분 성공과 cleanup 동작이 같은 경로를 공유함",
	"real check + probe + copy drift pattern is present on the same path":                                     "같은 경로에 실제 check + probe + copy drift 패턴이 있음",
	"allocation sizing and later copy size share the same attacker-controlled path":                           "할당 크기와 이후 copy 크기가 같은 공격자 제어 경로를 공유함",
	"dispatch decision and failure-unwind edge share the same path":                                           "dispatch 결정과 failure-unwind 경로가 같은 경로를 공유함",
	"a real validity guard and a later sink or side effect share the same path":                               "실제 유효성 가드와 이후 sink 또는 부작용이 같은 경로를 공유함",
	"matches a direct attacker-controlled size desynchronization pattern":                                     "공격자가 직접 조작하는 크기 비동기화 패턴과 일치함",
	"matches a real control-value or dispatch-manipulation pattern":                                           "실제 제어값 또는 dispatch 조작 패턴과 일치함",
	"matches an allocation-versus-use size drift pattern":                                                     "할당 크기와 사용 크기 drift 패턴과 일치함",
	"matches a guard-order or stale-state pattern":                                                            "가드 순서 또는 stale-state 패턴과 일치함",
	"matches a partial-initialization and rollback pattern":                                                   "부분 초기화와 rollback 패턴과 일치함",
	"generic fallback wording lowers confidence in specificity":                                               "일반 fallback 문구라 구체성 신뢰도가 낮아짐",
	"generic opaque-input fallback is noisier than concrete path-driven findings":                             "일반적인 opaque-input fallback은 구체적인 경로 기반 결과보다 노이즈가 큼",
	"generic edge-case scenario is low-specificity fallback output":                                           "일반 edge-case 시나리오는 저구체성 fallback 결과임",
	"Kernforge extracted concrete branch predicates from source-level guards":                                 "Kernforge가 소스 레벨 가드에서 구체적인 분기 비교식을 추출했습니다",
	"Kernforge synthesized minimal branch-flip counterexamples from extracted comparisons":                    "Kernforge가 추출한 비교식에서 최소 branch-flip 반례를 합성했습니다",
	"Kernforge mapped concrete pass/fail consequences from the same branch":                                   "Kernforge가 같은 분기에서 실제 pass/fail 이후 결과를 매핑했습니다",
	"Kernforge mapped branch-specific downstream call chains from the same guard":                             "Kernforge가 같은 가드에서 분기별 후속 호출 체인을 매핑했습니다",
	"path hint is grounded in concrete extracted source behavior":                                             "경로 힌트가 실제 추출된 소스 동작에 근거함",
	"path hint relies on generic closure metadata more than concrete source behavior":                         "경로 힌트가 구체적인 소스 동작보다 일반 closure 메타데이터에 더 의존함",
	"source excerpt points at a real guard, probe, dispatch, or copy line":                                    "소스 excerpt가 실제 guard, probe, dispatch, copy 줄을 가리킴",
	"source excerpt still looks noisy or helper-oriented":                                                     "소스 excerpt가 여전히 noisy하거나 helper 성격으로 보임",
	"source excerpt comes from exploit/helper/test-side code, which is noisier":                               "소스 excerpt가 exploit/helper/test 측 코드에서 와서 노이즈가 더 큼",
	"source excerpt comes from driver or kernel-side code":                                                    "소스 excerpt가 driver 또는 kernel 측 코드에서 옴",
	"title suggests a size-contract break, but the matched path is missing the full size-versus-use evidence": "제목은 size-contract 붕괴를 가리키지만, 매칭된 경로에는 전체 size-versus-use 근거가 부족함",
	"title suggests a dispatch manipulation path, but no concrete dispatch guard was matched nearby":          "제목은 dispatch 조작 경로를 가리키지만, 근처에 구체적인 dispatch guard가 매칭되지 않음",
	"title suggests a pointer-validity break, but no concrete null or invalidity guard was matched nearby":    "제목은 포인터 유효성 붕괴를 가리키지만, 근처에 구체적인 null 또는 invalidity guard가 매칭되지 않음",
	"generic fallback scenario is capped below concrete named attack patterns":                                "일반 fallback 시나리오는 구체적인 이름의 공격 패턴보다 낮게 상한이 걸림",
	"opaque fallback scenario is capped because it is intentionally low-specificity":                          "opaque fallback 시나리오는 의도적으로 저구체성이어서 상한이 걸림",
	"Kernforge derived attacker-breakable invariants from concrete source behavior":                           "Kernforge가 구체적인 소스 동작에서 공격자가 깨뜨릴 수 있는 불변식을 도출했습니다",
	"Kernforge synthesized concrete read-to-use drift examples on the same path":                              "Kernforge가 같은 경로에서 읽기와 사용 사이의 구체적인 drift 예시를 합성했습니다",
	"auxiliary-side finding is capped even though it has a strong local pattern":                              "보조 경로 결과는 국소 패턴이 강해도 상한이 걸림",
	"auxiliary-side dispatch finding is capped below primary target-side code":                                "보조 경로 dispatch 결과는 주 타깃 측 코드보다 낮게 상한이 걸림",
	"auxiliary-side finding is capped because closure noise is still likely":                                  "보조 경로 결과는 closure 노이즈 가능성이 커서 상한이 걸림",
	"scenario is capped because no concrete sink or dispatch site was matched nearby":                         "근처에 구체적인 sink 또는 dispatch 지점이 없어 시나리오 상한이 걸림",
	"The function body contains a real memory-transfer or probe site plus a nearby boundary check, so crafted sizes, short headers, or overlapping buffers can make validation and actual access diverge.":                                                           "함수 본문에 실제 메모리 전송 또는 probe 지점과 인접한 경계 검사가 함께 있어서, 조작된 크기, 짧은 헤더, 겹치는 버퍼가 검증과 실제 접근을 어긋나게 만들 수 있습니다.",
	"The function body makes a real dispatch decision from a selector-like value, so unsupported, colliding, or boundary control codes can steer execution into rare paths before all invariants are re-checked.":                                                    "함수 본문이 선택자 성격의 값으로 실제 dispatch 결정을 내리므로, 지원되지 않거나 충돌하는 값, 경계값 제어 코드는 모든 불변식이 다시 확인되기 전에 드문 경로로 실행을 밀어 넣을 수 있습니다.",
	"The function body contains a real validity check and then continues into a meaningful sink or side effect, which is exactly where attacker-controlled stale pointers, null-plus-size combinations, or partially initialized state try to break guard ordering.": "함수 본문에 실제 유효성 검사가 있고 그 뒤에 의미 있는 sink 또는 부작용이 이어지므로, 공격자가 조작한 stale 포인터, null-plus-size 조합, 부분 초기화 상태가 가드 순서를 깨뜨리기 쉬운 지점입니다.",
	"The function body computes or allocates from one size-like input and later copies, probes, or parses according to another condition, which is where attackers try to create alloc-versus-use drift.":                                                            "함수 본문이 한 크기 계열 입력으로 계산 또는 할당을 한 뒤 다른 조건에 따라 copy, probe, parse를 수행하므로, 공격자가 alloc-versus-use drift를 만들려는 지점입니다.",
	"The function body appears to publish or register state and also contains an explicit cleanup edge, so a crafted sequence can try to force partial success before the unwind path runs.":                                                                         "함수 본문이 상태를 공개 또는 등록하는 동시에 명시적인 cleanup 경로도 포함하고 있어, 조작된 입력 시퀀스로 unwind 전에 부분 성공을 강제할 수 있습니다.",
	"Kernforge extracted a concrete guard, sink, or cleanup edge from the function body, so contradictory edge-case inputs should be tested against that exact source path instead of only relying on the signature shape.":                                          "Kernforge가 함수 본문에서 구체적인 guard, sink, cleanup 경로를 추출했으므로, 모순된 edge-case 입력은 시그니처 형태만 보지 말고 그 정확한 소스 경로에 대입해 봐야 합니다.",
	"Out-of-bounds read or write when the checked size is not the consumed size":                                                                           "검사한 크기와 실제 소비한 크기가 다를 때 out-of-bounds read 또는 write",
	"User-controlled buffer contract breaks after a short-header or exact-boundary input":                                                                  "짧은 헤더나 정확한 경계값 입력 뒤에 사용자 제어 버퍼 계약이 깨짐",
	"Probe or validation happens on one layout while the copy or parse uses another":                                                                       "probe 또는 검증은 한 레이아웃 기준으로 하고 copy 또는 parse는 다른 레이아웃 기준으로 수행됨",
	"Unsupported selector falls into an unintended handler or default path":                                                                                "지원되지 않는 선택자가 의도하지 않은 handler 또는 default 경로로 떨어짐",
	"Privilege, capability, or mode checks are skipped on unusual control values":                                                                          "비정상 제어값에서 권한, capability, mode 검사가 건너뛰어짐",
	"Cleanup and rollback logic do not match the rarely used dispatch branch":                                                                              "cleanup과 rollback 로직이 드물게 쓰이는 dispatch 분기와 맞지 않음",
	"Null or stale pointer is consumed after a guard validated the wrong precondition":                                                                     "잘못된 전제조건을 검증한 뒤 null 또는 stale 포인터가 소비됨",
	"Non-zero size or later side effect outlives an earlier pointer validity assumption":                                                                   "non-zero size나 이후 부작용이 앞선 포인터 유효성 가정보다 오래 살아남음",
	"State is published or cleanup starts even though the pointer-backed state was not stable":                                                             "포인터 기반 상태가 안정되지 않았는데도 상태가 공개되거나 cleanup이 시작됨",
	"Allocated storage is smaller than the later copy or parser expectation":                                                                               "할당된 저장공간이 이후 copy 또는 parser 기대치보다 작음",
	"Wrapped or truncated size participates in allocation while a larger value drives use":                                                                 "wrapped 또는 truncated 크기가 할당에 쓰이고 더 큰 값이 실제 사용을 주도함",
	"Boundary validation and allocation sizing disagree about the same attacker-controlled value":                                                          "경계 검증과 할당 크기 계산이 같은 공격자 제어 값에 대해 서로 다르게 판단함",
	"Rollback misses one published object, callback, handle, or device surface":                                                                            "rollback이 공개된 객체, callback, handle, device surface 중 하나를 놓침",
	"Double cleanup or stale-state reuse after a partial success path":                                                                                     "부분 성공 경로 뒤에 double cleanup이나 stale-state 재사용이 발생함",
	"An externally reachable surface remains exposed even though the operation later failed":                                                               "작업이 나중에 실패했는데도 외부에서 닿는 표면이 계속 노출됨",
	"A source-level guard is applied to one interpretation of the input while a later line uses another":                                                   "소스 레벨 guard는 입력을 한 방식으로 해석해 적용했지만 이후 줄은 다른 방식으로 사용함",
	"A sink or cleanup edge assumes an invariant that was only partially checked":                                                                          "sink나 cleanup 경로가 부분적으로만 검증된 불변식을 가정함",
	"The function body contains an explicit pointer or handle validity gate that an attacker can try to bypass, desynchronize, or hit in the wrong order.": "함수 본문에 명시적인 포인터 또는 핸들 유효성 게이트가 있어, 공격자가 이를 우회하거나 비동기화하거나 순서를 깨뜨리려 할 수 있습니다.",
	"The code compares a size-like value in a branch, which is where boundary, truncation, and wraparound inputs usually try to break assumptions.":        "코드가 분기에서 크기 계열 값을 비교하고 있어, 경계값, truncation, wraparound 입력이 가정을 깨뜨리기 쉬운 지점입니다.",
	"The code branches on a control-like selector, so unsupported or colliding values can push execution into rarely tested paths.":                        "코드가 제어값 성격의 선택자에 따라 분기하므로, 지원되지 않거나 충돌하는 값이 실행을 드물게 테스트된 경로로 밀어 넣을 수 있습니다.",
	"The function body performs a memory transfer, which makes buffer ownership, overlap, and length mismatches directly security-relevant.":               "함수 본문이 메모리 전송을 수행하므로, 버퍼 소유권, 겹침, 길이 불일치가 곧바로 보안상 중요한 문제가 됩니다.",
	"The function probes user-controlled memory, which usually means the buffer contract matters more than the raw type name.":                             "함수 본문이 사용자 제어 메모리를 probe하므로, raw type 이름보다 버퍼 계약 자체가 더 중요해집니다.",
	"The function allocates memory from a computed size, so attackers will try to make allocation size and later use size disagree.":                       "함수 본문이 계산된 크기로 메모리를 할당하므로, 공격자는 할당 크기와 이후 사용 크기를 서로 어긋나게 만들려 합니다.",
	"The function contains an explicit failure-unwind edge, which is where partial side effects and rollback gaps usually show up.":                        "함수 본문에 명시적인 failure-unwind 경로가 있어, 부분 부작용과 rollback 누락이 드러나기 쉬운 지점입니다.",
	"The function appears to publish or register state before the whole flow is known to succeed.":                                                         "함수 본문이 전체 흐름의 성공이 확인되기 전에 상태를 공개하거나 등록하는 것으로 보입니다.",
	"The function body contains a source-level condition or sink that can be driven by crafted attacker input.":                                            "함수 본문에 조작된 공격자 입력으로 흔들 수 있는 소스 레벨 조건 또는 sink가 있습니다.",
	"Kernforge extracted a real size or boundary comparison from the function body":                                                                        "Kernforge가 함수 본문에서 실제 크기 또는 경계 비교를 추출했습니다",
	"the same path performs a real memory transfer":                                                                                                        "같은 경로가 실제 메모리 전송을 수행합니다",
	"the same path probes user-controlled memory":                                                                                                          "같은 경로가 사용자 제어 메모리를 probe합니다",
	"the same path makes a selector-driven dispatch decision":                                                                                              "같은 경로가 선택자 기반 dispatch 결정을 내립니다",
	"the same function also contains an explicit failure-unwind edge":                                                                                      "같은 함수에 명시적인 failure-unwind 경로도 있습니다",
	"Kernforge extracted a concrete source-level guard or sink on this path":                                                                               "Kernforge가 이 경로에서 구체적인 소스 레벨 guard 또는 sink를 추출했습니다",
}

type FunctionFuzzParamStrategy struct {
	Index    int      `json:"index"`
	Name     string   `json:"name"`
	RawType  string   `json:"raw_type,omitempty"`
	Class    string   `json:"class"`
	Relation string   `json:"relation,omitempty"`
	Mutators []string `json:"mutators,omitempty"`
	Notes    []string `json:"notes,omitempty"`
}

type FunctionFuzzSinkSignal struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	SymbolID string `json:"symbol_id,omitempty"`
	File     string `json:"file,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type FunctionFuzzCodeObservation struct {
	Kind            string   `json:"kind"`
	SymbolID        string   `json:"symbol_id,omitempty"`
	Symbol          string   `json:"symbol,omitempty"`
	File            string   `json:"file,omitempty"`
	Line            int      `json:"line,omitempty"`
	Evidence        string   `json:"evidence,omitempty"`
	AccessPaths     []string `json:"access_paths,omitempty"`
	ComparisonFacts []string `json:"comparison_facts,omitempty"`
	FocusInputs     []string `json:"focus_inputs,omitempty"`
	WhyItMatters    string   `json:"why_it_matters,omitempty"`
}

type FunctionFuzzInvariant struct {
	Kind   string `json:"kind"`
	Left   string `json:"left,omitempty"`
	Right  string `json:"right,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type FunctionFuzzBranchOutcome struct {
	Predicate       string   `json:"predicate,omitempty"`
	Side            string   `json:"side,omitempty"`
	EffectKind      string   `json:"effect_kind,omitempty"`
	Line            int      `json:"line,omitempty"`
	Evidence        string   `json:"evidence,omitempty"`
	DownstreamCalls []string `json:"downstream_calls,omitempty"`
}

type FunctionFuzzVirtualScenario struct {
	Title          string                      `json:"title"`
	Confidence     string                      `json:"confidence,omitempty"`
	RiskScore      int                         `json:"risk_score,omitempty"`
	ScoreReasons   []string                    `json:"score_reasons,omitempty"`
	FocusSymbolID  string                      `json:"focus_symbol_id,omitempty"`
	FocusSymbol    string                      `json:"focus_symbol,omitempty"`
	FocusFile      string                      `json:"focus_file,omitempty"`
	ScopeFilePath  []string                    `json:"scope_file_path,omitempty"`
	Inputs         []string                    `json:"inputs,omitempty"`
	ConcreteInputs []string                    `json:"concrete_inputs,omitempty"`
	Invariants     []FunctionFuzzInvariant     `json:"invariants,omitempty"`
	BranchFacts    []string                    `json:"branch_facts,omitempty"`
	BranchOutcomes []FunctionFuzzBranchOutcome `json:"branch_outcomes,omitempty"`
	DriftExamples  []string                    `json:"drift_examples,omitempty"`
	ExpectedFlow   string                      `json:"expected_flow,omitempty"`
	LikelyIssues   []string                    `json:"likely_issues,omitempty"`
	PathSketch     []string                    `json:"path_sketch,omitempty"`
	PathHint       string                      `json:"path_hint,omitempty"`
	SourceExcerpt  FunctionFuzzSourceExcerpt   `json:"source_excerpt,omitempty"`
}

type FunctionFuzzSourceExcerpt struct {
	Symbol    string   `json:"symbol,omitempty"`
	File      string   `json:"file,omitempty"`
	StartLine int      `json:"start_line,omitempty"`
	FocusLine int      `json:"focus_line,omitempty"`
	EndLine   int      `json:"end_line,omitempty"`
	Snippet   []string `json:"snippet,omitempty"`
}

type FunctionFuzzExecution struct {
	Eligible             bool     `json:"eligible,omitempty"`
	Status               string   `json:"status,omitempty"`
	Reason               string   `json:"reason,omitempty"`
	CompileContextLevel  string   `json:"compile_context_level,omitempty"`
	CompilerCandidate    string   `json:"compiler_candidate,omitempty"`
	CompilerResolvedPath string   `json:"compiler_resolved_path,omitempty"`
	CompilerStyle        string   `json:"compiler_style,omitempty"`
	CompileCommandSource string   `json:"compile_command_source,omitempty"`
	CompileDirectory     string   `json:"compile_directory,omitempty"`
	TranslationUnit      string   `json:"translation_unit,omitempty"`
	BuildScriptPath      string   `json:"build_script_path,omitempty"`
	BuildLogPath         string   `json:"build_log_path,omitempty"`
	RunLogPath           string   `json:"run_log_path,omitempty"`
	BuildCommand         string   `json:"build_command,omitempty"`
	RunCommand           string   `json:"run_command,omitempty"`
	ExecutablePath       string   `json:"executable_path,omitempty"`
	CorpusDir            string   `json:"corpus_dir,omitempty"`
	CrashDir             string   `json:"crash_dir,omitempty"`
	BackgroundJobID      string   `json:"background_job_id,omitempty"`
	LastOutput           string   `json:"last_output,omitempty"`
	CrashCount           int      `json:"crash_count,omitempty"`
	ExitCode             *int     `json:"exit_code,omitempty"`
	MissingSettings      []string `json:"missing_settings,omitempty"`
	RecoveryNotes        []string `json:"recovery_notes,omitempty"`
	ContinueCommand      string   `json:"continue_command,omitempty"`
}

type FunctionFuzzRun struct {
	ID                  string                        `json:"id"`
	Workspace           string                        `json:"workspace"`
	TargetQuery         string                        `json:"target_query"`
	ScopeMode           string                        `json:"scope_mode,omitempty"`
	ScopeRootFile       string                        `json:"scope_root_file,omitempty"`
	ScopeFiles          []string                      `json:"scope_files,omitempty"`
	TargetSymbolID      string                        `json:"target_symbol_id"`
	TargetSymbolName    string                        `json:"target_symbol_name"`
	TargetSignature     string                        `json:"target_signature,omitempty"`
	TargetFile          string                        `json:"target_file,omitempty"`
	AnalysisRunID       string                        `json:"analysis_run_id,omitempty"`
	AnalysisGoal        string                        `json:"analysis_goal,omitempty"`
	CreatedAt           time.Time                     `json:"created_at"`
	QueryMode           string                        `json:"query_mode,omitempty"`
	RiskScore           int                           `json:"risk_score,omitempty"`
	HarnessReady        bool                          `json:"harness_ready,omitempty"`
	ReachableCallCount  int                           `json:"reachable_call_count,omitempty"`
	ReachableDepth      int                           `json:"reachable_depth,omitempty"`
	ReachableTruncated  bool                          `json:"reachable_truncated,omitempty"`
	ReachableSymbols    []string                      `json:"reachable_symbols,omitempty"`
	ReachableFiles      []string                      `json:"reachable_files,omitempty"`
	OverlayDomains      []string                      `json:"overlay_domains,omitempty"`
	BuildContexts       []string                      `json:"build_contexts,omitempty"`
	ParameterStrategies []FunctionFuzzParamStrategy   `json:"parameter_strategies,omitempty"`
	SinkSignals         []FunctionFuzzSinkSignal      `json:"sink_signals,omitempty"`
	CodeObservations    []FunctionFuzzCodeObservation `json:"code_observations,omitempty"`
	VirtualScenarios    []FunctionFuzzVirtualScenario `json:"virtual_scenarios,omitempty"`
	PrimaryEngine       string                        `json:"primary_engine,omitempty"`
	SecondaryEngines    []string                      `json:"secondary_engines,omitempty"`
	ArtifactDir         string                        `json:"artifact_dir,omitempty"`
	PlanPath            string                        `json:"plan_path,omitempty"`
	ReportPath          string                        `json:"report_path,omitempty"`
	HarnessPath         string                        `json:"harness_path,omitempty"`
	Summary             string                        `json:"summary,omitempty"`
	Interpretation      []string                      `json:"interpretation,omitempty"`
	NextSteps           []string                      `json:"next_steps,omitempty"`
	SuggestedTargets    []string                      `json:"suggested_targets,omitempty"`
	SuggestedCommands   []string                      `json:"suggested_commands,omitempty"`
	Notes               []string                      `json:"notes,omitempty"`
	TargetStartLine     int                           `json:"target_start_line,omitempty"`
	TargetEndLine       int                           `json:"target_end_line,omitempty"`
	Execution           FunctionFuzzExecution         `json:"execution,omitempty"`
}

type FunctionFuzzStore struct {
	Path       string
	MaxEntries int
}

type functionFuzzClosure struct {
	RootSymbol   SymbolRecord
	Symbols      []SymbolRecord
	CallEdges    []CallEdge
	OverlayEdges []OverlayEdge
	Builds       []BuildContextRecord
	Files        []string
	MaxDepth     int
	Truncated    bool
}

type functionFuzzTargetSpec struct {
	Raw      string
	Name     string
	FileHint string
}

type functionFuzzResolvedPlan struct {
	Target     SymbolRecord
	Closure    functionFuzzClosure
	ScopeMode  string
	ScopeRoot  string
	ScopeFiles []string
	ExtraNotes []string
}

func NewFunctionFuzzStore() *FunctionFuzzStore {
	return &FunctionFuzzStore{
		Path:       filepath.Join(userConfigDir(), "function_fuzz.json"),
		MaxEntries: defaultFunctionFuzzMaxEntries,
	}
}

func (s *FunctionFuzzStore) Append(run FunctionFuzzRun) (FunctionFuzzRun, error) {
	if s == nil {
		return FunctionFuzzRun{}, nil
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	run = normalizeFunctionFuzzRun(run)
	items, err := s.load()
	if err != nil {
		return FunctionFuzzRun{}, err
	}
	items = append(items, run)
	maxEntries := s.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultFunctionFuzzMaxEntries
	}
	if len(items) > maxEntries {
		items = append([]FunctionFuzzRun(nil), items[len(items)-maxEntries:]...)
	}
	if err := s.save(items); err != nil {
		return FunctionFuzzRun{}, err
	}
	return run, nil
}

func (s *FunctionFuzzStore) ListRecent(workspace string, limit int) ([]FunctionFuzzRun, error) {
	items, err := s.load()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	var out []FunctionFuzzRun
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if workspace != "" && workspaceAffinityScore(workspace, item.Workspace) == 0 {
			continue
		}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *FunctionFuzzStore) Get(id string) (FunctionFuzzRun, bool, error) {
	items, err := s.load()
	if err != nil {
		return FunctionFuzzRun{}, false, err
	}
	query := strings.TrimSpace(id)
	for _, item := range items {
		if strings.EqualFold(item.ID, query) {
			return item, true, nil
		}
	}
	return FunctionFuzzRun{}, false, nil
}

func (s *FunctionFuzzStore) Upsert(run FunctionFuzzRun) (FunctionFuzzRun, error) {
	if s == nil {
		return FunctionFuzzRun{}, nil
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	run = normalizeFunctionFuzzRun(run)
	items, err := s.load()
	if err != nil {
		return FunctionFuzzRun{}, err
	}
	replaced := false
	for i := range items {
		if strings.EqualFold(strings.TrimSpace(items[i].ID), strings.TrimSpace(run.ID)) {
			items[i] = run
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, run)
	}
	maxEntries := s.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultFunctionFuzzMaxEntries
	}
	if len(items) > maxEntries {
		items = append([]FunctionFuzzRun(nil), items[len(items)-maxEntries:]...)
	}
	if err := s.save(items); err != nil {
		return FunctionFuzzRun{}, err
	}
	return run, nil
}

func (s *FunctionFuzzStore) Stats(workspace string) (int, time.Time, error) {
	items, err := s.load()
	if err != nil {
		return 0, time.Time{}, err
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	count := 0
	var last time.Time
	for _, item := range items {
		if workspace != "" && workspaceAffinityScore(workspace, item.Workspace) == 0 {
			continue
		}
		count++
		if item.CreatedAt.After(last) {
			last = item.CreatedAt
		}
	}
	return count, last, nil
}

func normalizeFunctionFuzzRun(run FunctionFuzzRun) FunctionFuzzRun {
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now()
	}
	if strings.TrimSpace(run.ID) == "" {
		run.ID = fmt.Sprintf("fuzz-%s-%03d", run.CreatedAt.Format("20060102-150405"), run.CreatedAt.Nanosecond()/1_000_000)
	}
	run.Workspace = normalizePersistentMemoryWorkspace(run.Workspace)
	run.TargetQuery = strings.TrimSpace(run.TargetQuery)
	run.ScopeMode = strings.TrimSpace(run.ScopeMode)
	run.ScopeRootFile = filepath.ToSlash(strings.TrimSpace(run.ScopeRootFile))
	run.ScopeFiles = normalizeFunctionFuzzPaths(run.ScopeFiles)
	run.TargetSymbolID = strings.TrimSpace(run.TargetSymbolID)
	run.TargetSymbolName = strings.TrimSpace(run.TargetSymbolName)
	run.TargetSignature = functionFuzzSanitizeSignature(run.TargetSignature)
	run.TargetFile = filepath.ToSlash(strings.TrimSpace(run.TargetFile))
	run.AnalysisRunID = strings.TrimSpace(run.AnalysisRunID)
	run.AnalysisGoal = compactPersistentMemoryText(run.AnalysisGoal, 240)
	run.QueryMode = strings.TrimSpace(run.QueryMode)
	run.ReachableSymbols = uniqueStrings(run.ReachableSymbols)
	run.ReachableFiles = normalizeFunctionFuzzPaths(run.ReachableFiles)
	run.OverlayDomains = uniqueStrings(run.OverlayDomains)
	run.BuildContexts = uniqueStrings(run.BuildContexts)
	run.SecondaryEngines = uniqueStrings(run.SecondaryEngines)
	run.Interpretation = uniqueStrings(run.Interpretation)
	run.NextSteps = uniqueStrings(run.NextSteps)
	run.SuggestedTargets = uniqueStrings(run.SuggestedTargets)
	run.SuggestedCommands = uniqueStrings(run.SuggestedCommands)
	run.Notes = uniqueStrings(run.Notes)
	run.Summary = compactPersistentMemoryText(run.Summary, 260)
	run.ArtifactDir = functionFuzzNormalizeOptionalPath(run.ArtifactDir)
	run.PlanPath = functionFuzzNormalizeOptionalPath(run.PlanPath)
	run.ReportPath = functionFuzzNormalizeOptionalPath(run.ReportPath)
	run.HarnessPath = functionFuzzNormalizeOptionalPath(run.HarnessPath)
	run.Execution = normalizeFunctionFuzzExecution(run.Execution)
	if run.RiskScore < 0 {
		run.RiskScore = 0
	}
	if run.RiskScore > 100 {
		run.RiskScore = 100
	}
	for i := range run.ParameterStrategies {
		run.ParameterStrategies[i].Name = strings.TrimSpace(run.ParameterStrategies[i].Name)
		run.ParameterStrategies[i].RawType = functionFuzzNormalizeDisplayText(run.ParameterStrategies[i].RawType)
		run.ParameterStrategies[i].Class = strings.TrimSpace(run.ParameterStrategies[i].Class)
		run.ParameterStrategies[i].Relation = strings.TrimSpace(run.ParameterStrategies[i].Relation)
		run.ParameterStrategies[i].Mutators = uniqueStrings(run.ParameterStrategies[i].Mutators)
		run.ParameterStrategies[i].Notes = uniqueStrings(run.ParameterStrategies[i].Notes)
	}
	run.SinkSignals = normalizeFunctionFuzzSinkSignals(run.SinkSignals)
	run.CodeObservations = normalizeFunctionFuzzCodeObservations(run.CodeObservations)
	run.VirtualScenarios = normalizeFunctionFuzzVirtualScenarios(run.VirtualScenarios)
	functionFuzzRecomputeScenarioScores(&run)
	return run
}

func normalizeFunctionFuzzExecution(execState FunctionFuzzExecution) FunctionFuzzExecution {
	execState.Status = strings.TrimSpace(execState.Status)
	execState.Reason = compactPersistentMemoryText(execState.Reason, 220)
	execState.CompileContextLevel = strings.TrimSpace(execState.CompileContextLevel)
	execState.CompilerCandidate = strings.TrimSpace(execState.CompilerCandidate)
	execState.CompilerResolvedPath = functionFuzzNormalizeOptionalPath(execState.CompilerResolvedPath)
	execState.CompilerStyle = strings.TrimSpace(execState.CompilerStyle)
	execState.CompileCommandSource = functionFuzzNormalizeOptionalPath(execState.CompileCommandSource)
	execState.CompileDirectory = functionFuzzNormalizeOptionalPath(execState.CompileDirectory)
	execState.TranslationUnit = functionFuzzNormalizeOptionalPath(execState.TranslationUnit)
	execState.BuildScriptPath = functionFuzzNormalizeOptionalPath(execState.BuildScriptPath)
	execState.BuildLogPath = functionFuzzNormalizeOptionalPath(execState.BuildLogPath)
	execState.RunLogPath = functionFuzzNormalizeOptionalPath(execState.RunLogPath)
	execState.ExecutablePath = functionFuzzNormalizeOptionalPath(execState.ExecutablePath)
	execState.CorpusDir = functionFuzzNormalizeOptionalPath(execState.CorpusDir)
	execState.CrashDir = functionFuzzNormalizeOptionalPath(execState.CrashDir)
	execState.BackgroundJobID = strings.TrimSpace(execState.BackgroundJobID)
	execState.LastOutput = compactPersistentMemoryText(execState.LastOutput, 260)
	execState.BuildCommand = compactPersistentMemoryText(execState.BuildCommand, 400)
	execState.RunCommand = compactPersistentMemoryText(execState.RunCommand, 320)
	execState.MissingSettings = uniqueStrings(execState.MissingSettings)
	execState.RecoveryNotes = uniqueStrings(execState.RecoveryNotes)
	execState.ContinueCommand = strings.TrimSpace(execState.ContinueCommand)
	if execState.CrashCount < 0 {
		execState.CrashCount = 0
	}
	return execState
}

func normalizeFunctionFuzzSinkSignals(items []FunctionFuzzSinkSignal) []FunctionFuzzSinkSignal {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]FunctionFuzzSinkSignal, 0, len(items))
	for _, item := range items {
		item.Kind = strings.TrimSpace(item.Kind)
		item.Name = strings.TrimSpace(item.Name)
		item.SymbolID = strings.TrimSpace(item.SymbolID)
		item.File = filepath.ToSlash(strings.TrimSpace(item.File))
		item.Reason = functionFuzzNormalizeDisplayText(item.Reason)
		key := strings.Join([]string{item.Kind, item.Name, item.SymbolID, item.File}, "|")
		if item.Kind == "" || item.Name == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i int, j int) bool {
		left := out[i].Kind + "|" + out[i].Name + "|" + out[i].SymbolID
		right := out[j].Kind + "|" + out[j].Name + "|" + out[j].SymbolID
		return left < right
	})
	return out
}

func normalizeFunctionFuzzCodeObservations(items []FunctionFuzzCodeObservation) []FunctionFuzzCodeObservation {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]FunctionFuzzCodeObservation, 0, len(items))
	for _, item := range items {
		item.Kind = strings.TrimSpace(item.Kind)
		item.SymbolID = strings.TrimSpace(item.SymbolID)
		item.Symbol = strings.TrimSpace(item.Symbol)
		item.File = filepath.ToSlash(strings.TrimSpace(item.File))
		item.Evidence = functionFuzzNormalizeDisplayText(item.Evidence)
		item.AccessPaths = uniqueStrings(item.AccessPaths)
		item.ComparisonFacts = uniqueStrings(item.ComparisonFacts)
		item.WhyItMatters = functionFuzzNormalizeDisplayText(item.WhyItMatters)
		item.FocusInputs = uniqueStrings(item.FocusInputs)
		if item.Kind == "" || item.Symbol == "" || item.File == "" || item.Line <= 0 {
			continue
		}
		key := strings.Join([]string{item.Kind, item.SymbolID, item.File, strconv.Itoa(item.Line), item.Evidence}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i int, j int) bool {
		left := out[i].File + "|" + strconv.Itoa(out[i].Line) + "|" + out[i].Kind
		right := out[j].File + "|" + strconv.Itoa(out[j].Line) + "|" + out[j].Kind
		return left < right
	})
	return out
}

func normalizeFunctionFuzzVirtualScenarios(items []FunctionFuzzVirtualScenario) []FunctionFuzzVirtualScenario {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]FunctionFuzzVirtualScenario, 0, len(items))
	for _, item := range items {
		item.Title = strings.TrimSpace(item.Title)
		item.Confidence = strings.TrimSpace(item.Confidence)
		if item.RiskScore < 0 {
			item.RiskScore = 0
		}
		if item.RiskScore > 100 {
			item.RiskScore = 100
		}
		item.ScoreReasons = uniqueStrings(item.ScoreReasons)
		item.FocusSymbolID = strings.TrimSpace(item.FocusSymbolID)
		item.FocusSymbol = strings.TrimSpace(item.FocusSymbol)
		item.FocusFile = filepath.ToSlash(strings.TrimSpace(item.FocusFile))
		item.ScopeFilePath = normalizeFunctionFuzzPaths(item.ScopeFilePath)
		item.ExpectedFlow = functionFuzzNormalizeDisplayText(item.ExpectedFlow)
		item.Inputs = uniqueStrings(item.Inputs)
		item.ConcreteInputs = uniqueStrings(item.ConcreteInputs)
		item.Invariants = normalizeFunctionFuzzInvariants(item.Invariants)
		item.BranchFacts = uniqueStrings(item.BranchFacts)
		item.BranchOutcomes = normalizeFunctionFuzzBranchOutcomes(item.BranchOutcomes)
		item.DriftExamples = uniqueStrings(item.DriftExamples)
		item.LikelyIssues = uniqueStrings(item.LikelyIssues)
		item.PathSketch = uniqueStrings(item.PathSketch)
		item.PathHint = functionFuzzNormalizeDisplayText(item.PathHint)
		item.SourceExcerpt = normalizeFunctionFuzzSourceExcerpt(item.SourceExcerpt)
		if item.Title == "" {
			continue
		}
		key := strings.ToLower(item.Title)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func normalizeFunctionFuzzInvariants(items []FunctionFuzzInvariant) []FunctionFuzzInvariant {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]FunctionFuzzInvariant, 0, len(items))
	for _, item := range items {
		item.Kind = strings.TrimSpace(item.Kind)
		item.Left = strings.TrimSpace(item.Left)
		item.Right = strings.TrimSpace(item.Right)
		item.Detail = strings.TrimSpace(item.Detail)
		if item.Kind == "" {
			continue
		}
		key := strings.ToLower(item.Kind + "|" + item.Left + "|" + item.Right + "|" + item.Detail)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func normalizeFunctionFuzzBranchOutcomes(items []FunctionFuzzBranchOutcome) []FunctionFuzzBranchOutcome {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]FunctionFuzzBranchOutcome, 0, len(items))
	for _, item := range items {
		item.Predicate = strings.TrimSpace(item.Predicate)
		item.Side = strings.TrimSpace(item.Side)
		item.EffectKind = strings.TrimSpace(item.EffectKind)
		item.Evidence = functionFuzzNormalizeDisplayText(item.Evidence)
		item.DownstreamCalls = uniqueStrings(item.DownstreamCalls)
		if item.Predicate == "" || item.Side == "" || item.Line <= 0 {
			continue
		}
		key := strings.Join([]string{item.Predicate, item.Side, item.EffectKind, strconv.Itoa(item.Line), item.Evidence}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func functionFuzzRecomputeScenarioScores(run *FunctionFuzzRun) {
	if run == nil || len(run.VirtualScenarios) == 0 {
		return
	}
	out := make([]FunctionFuzzVirtualScenario, 0, len(run.VirtualScenarios))
	for _, item := range run.VirtualScenarios {
		item.RiskScore, item.ScoreReasons = functionFuzzScenarioRiskScore(*run, item)
		out = append(out, item)
	}
	run.VirtualScenarios = out
}

func functionFuzzScenarioRiskScore(run FunctionFuzzRun, item FunctionFuzzVirtualScenario) (int, []string) {
	score := 8
	reasons := []string{}

	switch strings.ToLower(strings.TrimSpace(item.Confidence)) {
	case "high":
		score += 26
		reasons = append(reasons, "high-confidence source-only hypothesis")
	case "medium":
		score += 16
		reasons = append(reasons, "medium-confidence source-only hypothesis")
	case "low":
		score += 8
		reasons = append(reasons, "low-confidence source-only hypothesis")
	}

	observationMatches := functionFuzzScenarioObservationMatches(item, run.CodeObservations)
	if len(observationMatches) > 0 {
		score += 10
		reasons = append(reasons, fmt.Sprintf("grounded in %d source-derived guard or sink observation(s)", len(observationMatches)))
	}
	if len(observationMatches) >= 2 {
		score += 6
	}
	if len(observationMatches) >= 3 {
		score += 4
	}
	if functionFuzzScenarioHasObservationKinds(observationMatches, "copy_sink") {
		score += 10
		reasons = append(reasons, "real memory-transfer sink is present on the same path")
	}
	if functionFuzzScenarioHasObservationKinds(observationMatches, "probe_sink") {
		score += 8
		reasons = append(reasons, "real user-buffer probe is present on the same path")
	}
	if functionFuzzScenarioHasObservationKinds(observationMatches, "size_guard") {
		score += 8
		reasons = append(reasons, "real size or boundary guard is present on the same path")
	}
	if functionFuzzScenarioHasObservationKinds(observationMatches, "dispatch_guard") {
		score += 8
		reasons = append(reasons, "real selector-driven dispatch is present on the same path")
	}
	if functionFuzzScenarioHasObservationKinds(observationMatches, "cleanup_path", "state_publish") {
		score += 6
		reasons = append(reasons, "partial-success and cleanup behavior share the same path")
	}
	copySink := functionFuzzScenarioHasObservationKinds(observationMatches, "copy_sink")
	probeSink := functionFuzzScenarioHasObservationKinds(observationMatches, "probe_sink")
	sizeGuard := functionFuzzScenarioHasObservationKinds(observationMatches, "size_guard")
	dispatchGuard := functionFuzzScenarioHasObservationKinds(observationMatches, "dispatch_guard")
	nullGuard := functionFuzzScenarioHasObservationKinds(observationMatches, "null_guard")
	cleanupPath := functionFuzzScenarioHasObservationKinds(observationMatches, "cleanup_path")
	statePublish := functionFuzzScenarioHasObservationKinds(observationMatches, "state_publish")
	allocSite := functionFuzzScenarioHasObservationKinds(observationMatches, "alloc_site")

	if copySink && probeSink && sizeGuard {
		score += 16
		reasons = append(reasons, "real check + probe + copy drift pattern is present on the same path")
	}
	if allocSite && sizeGuard && copySink {
		score += 12
		reasons = append(reasons, "allocation sizing and later copy size share the same attacker-controlled path")
	}
	if dispatchGuard && cleanupPath {
		score += 8
		reasons = append(reasons, "dispatch decision and failure-unwind edge share the same path")
	}
	if nullGuard && (copySink || probeSink || statePublish) {
		score += 6
		reasons = append(reasons, "a real validity guard and a later sink or side effect share the same path")
	}

	lowerTitle := strings.ToLower(strings.TrimSpace(item.Title))
	switch {
	case strings.Contains(lowerTitle, "attacker-controlled size"):
		score += 14
		reasons = append(reasons, "matches a direct attacker-controlled size desynchronization pattern")
	case strings.Contains(lowerTitle, "unexpected control value"):
		score += 12
		reasons = append(reasons, "matches a real control-value or dispatch-manipulation pattern")
	case strings.Contains(lowerTitle, "allocation size"):
		score += 11
		reasons = append(reasons, "matches an allocation-versus-use size drift pattern")
	case strings.Contains(lowerTitle, "pointer or state validity"):
		score += 9
		reasons = append(reasons, "matches a guard-order or stale-state pattern")
	case strings.Contains(lowerTitle, "entry-point partial initialization"):
		score += 7
		reasons = append(reasons, "matches a partial-initialization and rollback pattern")
	case strings.Contains(lowerTitle, "observed source-level guard or sink"):
		score -= 8
		reasons = append(reasons, "generic fallback wording lowers confidence in specificity")
	case strings.Contains(lowerTitle, "opaque input partitioning"):
		score -= 18
		reasons = append(reasons, "generic opaque-input fallback is noisier than concrete path-driven findings")
	case strings.Contains(lowerTitle, "generic edge-case"):
		score -= 24
		reasons = append(reasons, "generic edge-case scenario is low-specificity fallback output")
	}

	if len(item.LikelyIssues) >= 2 {
		score += 3
	}
	if len(item.PathSketch) >= 2 {
		score += 3
	}
	if len(item.Invariants) > 0 {
		score += functionFuzzMin(8, len(item.Invariants)*3)
		reasons = append(reasons, "Kernforge derived attacker-breakable invariants from concrete source behavior")
	}
	if len(item.BranchFacts) > 0 {
		score += functionFuzzMin(6, len(item.BranchFacts)*2)
		reasons = append(reasons, "Kernforge extracted concrete branch predicates from source-level guards")
		reasons = append(reasons, "Kernforge synthesized minimal branch-flip counterexamples from extracted comparisons")
	}
	if len(item.BranchOutcomes) > 0 {
		score += functionFuzzMin(6, len(item.BranchOutcomes)*2)
		reasons = append(reasons, "Kernforge mapped concrete pass/fail consequences from the same branch")
		for _, outcome := range item.BranchOutcomes {
			if len(outcome.DownstreamCalls) > 0 {
				score += 3
				reasons = append(reasons, "Kernforge mapped branch-specific downstream call chains from the same guard")
				break
			}
		}
	}
	if len(item.DriftExamples) > 0 {
		score += functionFuzzMin(6, len(item.DriftExamples)*2)
		reasons = append(reasons, "Kernforge synthesized concrete read-to-use drift examples on the same path")
	}

	lowerHint := strings.ToLower(strings.TrimSpace(item.PathHint))
	if containsAny(lowerHint, "real size or boundary comparison", "real memory transfer", "probes user-controlled memory", "explicit failure-unwind edge", "selector-driven dispatch") {
		score += 8
		reasons = append(reasons, "path hint is grounded in concrete extracted source behavior")
	}
	if containsAny(lowerHint, "security surface overlay appears", "the selected function is input-facing") &&
		!containsAny(lowerHint, "real size or boundary comparison", "real memory transfer", "probes user-controlled memory", "selector-driven dispatch") {
		score -= 6
		reasons = append(reasons, "path hint relies on generic closure metadata more than concrete source behavior")
	}

	if len(item.SourceExcerpt.Snippet) > 0 {
		score += 4
	}
	if functionFuzzScenarioSnippetContainsRealGuardOrSink(item.SourceExcerpt.Snippet) {
		score += 8
		reasons = append(reasons, "source excerpt points at a real guard, probe, dispatch, or copy line")
	}
	if functionFuzzScenarioSnippetLooksNoisy(item.SourceExcerpt.Snippet) {
		score -= 14
		reasons = append(reasons, "source excerpt still looks noisy or helper-oriented")
	}

	lowerFile := strings.ToLower(strings.TrimSpace(item.SourceExcerpt.File))
	auxiliaryFile := functionFuzzScenarioIsAuxiliaryFile(lowerFile)
	switch {
	case auxiliaryFile:
		score -= 18
		reasons = append(reasons, "source excerpt comes from exploit/helper/test-side code, which is noisier")
	case functionFuzzScenarioIsPrimaryCodeFile(lowerFile):
		score += 6
		reasons = append(reasons, "source excerpt comes from driver or kernel-side code")
	}

	if strings.Contains(lowerTitle, "attacker-controlled size") && !(sizeGuard && (copySink || probeSink)) {
		score -= 20
		reasons = append(reasons, "title suggests a size-contract break, but the matched path is missing the full size-versus-use evidence")
	}
	if strings.Contains(lowerTitle, "unexpected control value") && !dispatchGuard {
		score -= 20
		reasons = append(reasons, "title suggests a dispatch manipulation path, but no concrete dispatch guard was matched nearby")
	}
	if strings.Contains(lowerTitle, "pointer or state validity") && !nullGuard {
		score -= 18
		reasons = append(reasons, "title suggests a pointer-validity break, but no concrete null or invalidity guard was matched nearby")
	}

	if strings.Contains(lowerTitle, "observed source-level guard or sink") && score > 82 {
		score = 82
		reasons = append(reasons, "generic fallback scenario is capped below concrete named attack patterns")
	}
	if strings.Contains(lowerTitle, "opaque input partitioning") && score > 25 {
		score = 25
		reasons = append(reasons, "opaque fallback scenario is capped because it is intentionally low-specificity")
	}
	if strings.Contains(lowerTitle, "generic edge-case") && score > 20 {
		score = 20
		reasons = append(reasons, "generic fallback scenario is capped because it is intentionally low-specificity")
	}
	if auxiliaryFile {
		switch {
		case copySink && probeSink && sizeGuard:
			if score > 72 {
				score = 72
				reasons = append(reasons, "auxiliary-side finding is capped even though it has a strong local pattern")
			}
		case dispatchGuard && cleanupPath:
			if score > 58 {
				score = 58
				reasons = append(reasons, "auxiliary-side dispatch finding is capped below primary target-side code")
			}
		default:
			if score > 42 {
				score = 42
				reasons = append(reasons, "auxiliary-side finding is capped because closure noise is still likely")
			}
		}
	}
	if !copySink && !probeSink && !dispatchGuard && !statePublish && score > 68 {
		score = 68
		reasons = append(reasons, "scenario is capped because no concrete sink or dispatch site was matched nearby")
	}

	if score < 1 {
		score = 1
	}
	if score > 100 {
		score = 100
	}
	return score, uniqueStrings(limitStrings(reasons, 6))
}

func functionFuzzScenarioIsAuxiliaryFile(lowerFile string) bool {
	return functionFuzzPathContainsSegment(lowerFile, "exploit") ||
		functionFuzzPathContainsSegment(lowerFile, "example") ||
		functionFuzzPathContainsSegment(lowerFile, "sample") ||
		functionFuzzPathContainsSegment(lowerFile, "samples") ||
		functionFuzzPathContainsSegment(lowerFile, "test") ||
		functionFuzzPathContainsSegment(lowerFile, "tests") ||
		functionFuzzPathContainsSegment(lowerFile, "demo") ||
		functionFuzzPathContainsSegment(lowerFile, "helper")
}

func functionFuzzScenarioIsPrimaryCodeFile(lowerFile string) bool {
	return functionFuzzPathContainsSegment(lowerFile, "driver") ||
		functionFuzzPathContainsSegment(lowerFile, "kernel") ||
		functionFuzzPathContainsSegment(lowerFile, "windows")
}

func functionFuzzPathContainsSegment(lowerFile string, segment string) bool {
	lowerFile = strings.ToLower(filepath.ToSlash(strings.TrimSpace(lowerFile)))
	segment = strings.ToLower(strings.TrimSpace(segment))
	if lowerFile == "" || segment == "" {
		return false
	}
	return strings.HasPrefix(lowerFile, segment+"/") ||
		strings.Contains(lowerFile, "/"+segment+"/") ||
		strings.HasSuffix(lowerFile, "/"+segment) ||
		lowerFile == segment
}

func functionFuzzScenarioObservationMatches(item FunctionFuzzVirtualScenario, observations []FunctionFuzzCodeObservation) []FunctionFuzzCodeObservation {
	if len(observations) == 0 {
		return nil
	}
	excerpt := item.SourceExcerpt
	file := strings.TrimSpace(excerpt.File)
	startLine := excerpt.StartLine
	endLine := excerpt.EndLine
	focusLine := excerpt.FocusLine
	if endLine <= 0 {
		endLine = startLine
	}
	out := []FunctionFuzzCodeObservation{}
	for _, observation := range observations {
		if !functionFuzzScenarioFileMatches(file, observation.File) {
			continue
		}
		line := observation.Line
		if startLine > 0 && endLine >= startLine {
			if line >= startLine-20 && line <= endLine+20 {
				out = append(out, observation)
				continue
			}
		}
		if focusLine > 0 && line >= focusLine-25 && line <= focusLine+25 {
			out = append(out, observation)
		}
	}
	return normalizeFunctionFuzzCodeObservations(out)
}

func functionFuzzScenarioFileMatches(left string, right string) bool {
	left = strings.ToLower(filepath.ToSlash(strings.TrimSpace(left)))
	right = strings.ToLower(filepath.ToSlash(strings.TrimSpace(right)))
	if left == "" || right == "" {
		return false
	}
	return left == right || strings.HasSuffix(left, "/"+right) || strings.HasSuffix(right, "/"+left)
}

func functionFuzzScenarioHasObservationKinds(items []FunctionFuzzCodeObservation, kinds ...string) bool {
	if len(items) == 0 || len(kinds) == 0 {
		return false
	}
	set := map[string]struct{}{}
	for _, item := range items {
		set[strings.TrimSpace(item.Kind)] = struct{}{}
	}
	for _, kind := range kinds {
		if _, ok := set[strings.TrimSpace(kind)]; ok {
			return true
		}
	}
	return false
}

func functionFuzzScenarioSnippetContainsRealGuardOrSink(snippet []string) bool {
	for _, line := range snippet {
		lower := strings.ToLower(strings.TrimSpace(line))
		if functionFuzzLooksLikeInputConsumptionLine(lower) ||
			functionFuzzLooksLikeControlOrDecisionLine(lower) ||
			functionFuzzLooksLikeComparisonLine(lower) ||
			functionFuzzLooksLikeMemoryTransferLine(lower) ||
			functionFuzzLooksLikeProbeLine(lower) {
			return true
		}
	}
	return false
}

func functionFuzzScenarioSnippetLooksNoisy(snippet []string) bool {
	if len(snippet) == 0 {
		return true
	}
	if functionFuzzSnippetLooksLikeBootstrapOnly(snippet) {
		return true
	}
	meaningful := 0
	noisy := 0
	for _, line := range snippet {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "" {
			continue
		}
		if functionFuzzLooksLikeLoggingLine(lower) || functionFuzzLooksLikeBootstrapOrResolverLine(lower) {
			noisy++
		}
		if functionFuzzLooksLikeInputConsumptionLine(lower) || functionFuzzLooksLikeControlOrDecisionLine(lower) || functionFuzzLooksLikeComparisonLine(lower) {
			meaningful++
		}
	}
	return noisy > 0 && meaningful == 0
}

func normalizeFunctionFuzzSourceExcerpt(item FunctionFuzzSourceExcerpt) FunctionFuzzSourceExcerpt {
	item.Symbol = strings.TrimSpace(item.Symbol)
	item.File = filepath.ToSlash(strings.TrimSpace(item.File))
	cleaned := make([]string, 0, len(item.Snippet))
	for _, line := range item.Snippet {
		line = strings.ReplaceAll(line, "\t", "    ")
		line = strings.TrimRight(line, " \t")
		cleaned = append(cleaned, line)
	}
	item.Snippet = cleaned
	if item.StartLine < 0 {
		item.StartLine = 0
	}
	if item.FocusLine < 0 {
		item.FocusLine = 0
	}
	if item.EndLine < 0 {
		item.EndLine = 0
	}
	return item
}

func normalizeFunctionFuzzPaths(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := filepath.ToSlash(strings.TrimSpace(item))
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return uniqueStrings(out)
}

func (s *FunctionFuzzStore) load() ([]FunctionFuzzRun, error) {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var items []FunctionFuzzRun
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	for i := range items {
		items[i] = normalizeFunctionFuzzRun(items[i])
	}
	return items, nil
}

func (s *FunctionFuzzStore) save(items []FunctionFuzzRun) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.Path, data, 0o644)
}

func loadLatestProjectAnalysisArtifactsForRoot(cfg Config, root string) (latestAnalysisArtifacts, bool) {
	root = strings.TrimSpace(root)
	if root == "" {
		return latestAnalysisArtifacts{}, false
	}
	analysisCfg := configProjectAnalysis(cfg, root)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")

	info, err := os.Stat(latestDir)
	if err != nil || !info.IsDir() {
		return latestAnalysisArtifacts{}, false
	}

	artifacts := latestAnalysisArtifacts{}
	loaded := false
	if packData, err := os.ReadFile(filepath.Join(latestDir, "knowledge_pack.json")); err == nil {
		if err := json.Unmarshal(packData, &artifacts.Pack); err == nil {
			loaded = true
		}
	}
	if snapshotData, err := os.ReadFile(filepath.Join(latestDir, "snapshot.json")); err == nil {
		if err := json.Unmarshal(snapshotData, &artifacts.Snapshot); err == nil {
			loaded = true
		}
	}
	if corpusData, err := os.ReadFile(filepath.Join(latestDir, "vector_corpus.json")); err == nil {
		if err := json.Unmarshal(corpusData, &artifacts.Corpus); err == nil {
			loaded = true
		}
	}
	if indexData, err := os.ReadFile(filepath.Join(latestDir, "structural_index.json")); err == nil {
		if err := json.Unmarshal(indexData, &artifacts.Index); err == nil {
			loaded = true
		}
	}
	if indexData, err := os.ReadFile(filepath.Join(latestDir, "structural_index_v2.json")); err == nil {
		if err := json.Unmarshal(indexData, &artifacts.IndexV2); err == nil {
			loaded = true
		}
	}
	if manifestData, err := os.ReadFile(filepath.Join(latestDir, "docs_manifest.json")); err == nil {
		if manifest, err := decodeAnalysisDocsManifest(manifestData); err == nil {
			artifacts.DocsManifest = manifest
			loaded = true
		}
	} else if manifestData, err := os.ReadFile(filepath.Join(latestDir, "docs", "manifest.json")); err == nil {
		if manifest, err := decodeAnalysisDocsManifest(manifestData); err == nil {
			artifacts.DocsManifest = manifest
			loaded = true
		}
	}
	return artifacts, loaded
}

func (rt *runtimeState) handleFuzzFuncCommand(args string) error {
	if rt.functionFuzz == nil {
		return fmt.Errorf("function fuzz store is not configured")
	}
	trimmed := strings.TrimSpace(args)
	if trimmed == "" || strings.EqualFold(trimmed, "status") {
		return rt.showFunctionFuzzStatus()
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return rt.showFunctionFuzzStatus()
	}
	switch strings.ToLower(fields[0]) {
	case "language", "lang":
		return rt.handleFunctionFuzzLanguage(strings.TrimSpace(trimmed[len(fields[0]):]))
	case "show":
		return rt.handleFunctionFuzzShow(strings.TrimSpace(trimmed[len(fields[0]):]))
	case "list":
		return rt.handleFunctionFuzzList()
	case "continue":
		return rt.handleFunctionFuzzContinue(strings.TrimSpace(trimmed[len(fields[0]):]))
	default:
		return rt.handleFunctionFuzzPlan(trimmed)
	}
}

func (rt *runtimeState) showFunctionFuzzStatus() error {
	fmt.Fprintln(rt.writer, rt.ui.section("Function Fuzz"))
	workspace := workspaceSnapshotRoot(rt.workspace)
	count, last, err := rt.functionFuzz.Stats(workspace)
	if err == nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("runs", strconv.Itoa(count)))
		if !last.IsZero() {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("last_updated", last.Format(time.RFC3339)))
		}
	}
	items, err := rt.functionFuzz.ListRecent(workspace, 1)
	if err == nil && len(items) > 0 {
		latest := items[0]
		if refreshed, changed := rt.refreshFunctionFuzzExecution(latest); changed {
			latest = refreshed
			_, _ = rt.functionFuzz.Upsert(latest)
		}
		fmt.Fprintln(rt.writer, rt.ui.statusKV("latest", latest.ID))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("target", latest.TargetSymbolName))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("risk", fmt.Sprintf("%d", latest.RiskScore)))
		if strings.TrimSpace(latest.PrimaryEngine) != "" {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("engine", latest.PrimaryEngine))
		}
		if strings.TrimSpace(latest.Execution.Status) != "" {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("auto_exec", latest.Execution.Status))
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.statusKV("output_language", functionFuzzOutputLanguageSummary(rt.cfg)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("usage", functionFuzzUsage()))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("example", `/fuzz-func ValidateRequest --file "src/guard.cpp"`))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("example_alias", `/fuzz-func ValidateRequest @src/guard.cpp`))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("file_scope", `/fuzz-func --file "src/driver.cpp"`))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("file_scope_alias", `/fuzz-func @src/driver.cpp`))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("language", "/fuzz-func language [system|english]"))
	return nil
}

func functionFuzzUsage() string {
	return "/fuzz-func <function-name> [--file <path>|@<path>] or /fuzz-func [--file <path>|@<path>]"
}

func (rt *runtimeState) handleFunctionFuzzLanguage(args string) error {
	mode := strings.TrimSpace(args)
	if mode == "" {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("fuzz-func output language: "+functionFuzzOutputLanguageSummary(rt.cfg)))
		return nil
	}
	switch strings.ToLower(mode) {
	case "system", "pc", "locale", "auto":
		rt.cfg.FuzzFuncOutputLanguage = "system"
	case "english", "en", "en-us":
		rt.cfg.FuzzFuncOutputLanguage = "english"
	default:
		return fmt.Errorf("usage: /fuzz-func language [system|english]")
	}
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("fuzz-func output language set to "+functionFuzzOutputLanguageSummary(rt.cfg)))
	return nil
}

func functionFuzzOutputLanguageSummary(cfg Config) string {
	switch configFuzzFuncOutputLanguage(cfg) {
	case "english":
		return "english"
	default:
		locale := strings.TrimSpace(getSystemLocale())
		if locale == "" {
			return "system"
		}
		return "system (" + locale + ")"
	}
}

func functionFuzzEnglishConfig() Config {
	return Config{FuzzFuncOutputLanguage: "english"}
}

func (rt *runtimeState) handleFunctionFuzzList() error {
	items, err := rt.functionFuzz.ListRecent(workspaceSnapshotRoot(rt.workspace), 8)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No function fuzz plans found for this workspace."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Function Fuzz Runs"))
	for _, item := range items {
		line := fmt.Sprintf("- %s  target=%s  risk=%d  engine=%s", rt.ui.dim(item.ID), valueOrUnset(item.TargetSymbolName), item.RiskScore, valueOrUnset(item.PrimaryEngine))
		if strings.TrimSpace(item.Execution.Status) != "" {
			line += "  auto_exec=" + item.Execution.Status
		}
		if strings.TrimSpace(item.Summary) != "" {
			line += "  |  " + compactPersistentMemoryText(functionFuzzDisplayText(rt.cfg, item.Summary), 120)
		}
		fmt.Fprintln(rt.writer, line)
	}
	return nil
}

func (rt *runtimeState) handleFunctionFuzzShow(args string) error {
	id := strings.TrimSpace(args)
	if id == "" || strings.EqualFold(id, "latest") {
		items, err := rt.functionFuzz.ListRecent(workspaceSnapshotRoot(rt.workspace), 1)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return fmt.Errorf("no function fuzz runs found for this workspace")
		}
		id = items[0].ID
	}
	run, ok, err := rt.functionFuzz.Get(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("function fuzz run not found: %s", id)
	}
	if refreshed, changed := rt.refreshFunctionFuzzExecution(run); changed {
		run = refreshed
		if _, err := rt.functionFuzz.Upsert(run); err != nil {
			return err
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Function Fuzz Run"))
	fmt.Fprintln(rt.writer, renderFunctionFuzzRunWithConfig(run, rt.cfg))
	rt.printFunctionFuzzCampaignHandoff(run)
	return nil
}

func (rt *runtimeState) handleFunctionFuzzPlan(query string) error {
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("workspace root is not available")
	}
	run, err := buildFunctionFuzzRun(rt.cfg, root, query)
	if err != nil {
		return err
	}
	if err := rt.maybeConfirmFunctionFuzzExecution(&run); err != nil {
		return err
	}
	rt.maybeLaunchFunctionFuzzExecution(&run)
	if err := writeFunctionFuzzPlanJSON(&run); err != nil {
		return err
	}
	saved, err := rt.functionFuzz.Upsert(run)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Function Fuzz"))
	fmt.Fprintln(rt.writer, renderFunctionFuzzRunWithConfig(saved, rt.cfg))
	rt.printFunctionFuzzCampaignHandoff(saved)
	return nil
}

func (rt *runtimeState) handleFunctionFuzzContinue(args string) error {
	if rt.functionFuzz == nil {
		return fmt.Errorf("function fuzz store is not configured")
	}
	id := strings.TrimSpace(args)
	if id == "" || strings.EqualFold(id, "latest") {
		items, err := rt.functionFuzz.ListRecent(workspaceSnapshotRoot(rt.workspace), 1)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return fmt.Errorf("no function fuzz runs found for this workspace")
		}
		id = items[0].ID
	}
	run, ok, err := rt.functionFuzz.Get(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("function fuzz run not found: %s", id)
	}
	if !functionFuzzExecutionNeedsConfirmation(run.Execution) {
		return fmt.Errorf("function fuzz run %s does not need confirmation; current auto_exec=%s", run.ID, valueOrUnset(run.Execution.Status))
	}
	functionFuzzApproveExecution(rt.cfg, &run, false)
	rt.maybeLaunchFunctionFuzzExecution(&run)
	if err := writeFunctionFuzzPlanJSON(&run); err != nil {
		return err
	}
	saved, err := rt.functionFuzz.Upsert(run)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Function Fuzz"))
	fmt.Fprintln(rt.writer, renderFunctionFuzzRunWithConfig(saved, rt.cfg))
	rt.printFunctionFuzzCampaignHandoff(saved)
	return nil
}

func (rt *runtimeState) printFunctionFuzzCampaignHandoff(run FunctionFuzzRun) {
	if rt == nil || rt.writer == nil || rt.fuzzCampaigns == nil {
		return
	}
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, renderFunctionFuzzCampaignHandoff(rt.fuzzCampaignAutomationPlanForRun(run)))
}

func (rt *runtimeState) fuzzCampaignAutomationPlanForRun(run FunctionFuzzRun) fuzzCampaignAutomationPlan {
	if rt == nil {
		return fuzzCampaignAutomationPlan{}
	}
	campaign, ok, err := rt.resolveFuzzCampaign("latest")
	if err != nil || !ok {
		campaign = FuzzCampaign{}
	}
	run = normalizeFunctionFuzzRun(run)
	switch {
	case strings.TrimSpace(run.ID) == "":
		return fuzzCampaignAutomationPlan{}
	case len(run.VirtualScenarios) == 0:
		return fuzzCampaignAutomationPlan{
			Title: "No campaign seed handoff is needed yet because this /fuzz-func run did not produce source-only scenarios.",
			Details: []string{
				"Run: " + run.ID,
			},
			CanRun: false,
		}
	case strings.TrimSpace(campaign.ID) == "":
		return fuzzCampaignAutomationPlan{
			Title: "Suggested next step: create a fuzz campaign and promote this run's source-only scenarios into corpus seeds.",
			Details: []string{
				"Run: " + run.ID,
				fmt.Sprintf("Source-only scenarios: %d", len(run.VirtualScenarios)),
			},
			Command: "/fuzz-campaign run",
			CanRun:  true,
		}
	case !containsString(campaign.FunctionRuns, run.ID):
		return fuzzCampaignAutomationPlan{
			Title: "Suggested next step: attach this /fuzz-func run to the active campaign and promote seed artifacts.",
			Details: []string{
				"Campaign: " + campaign.ID,
				"Run: " + run.ID,
			},
			Command: "/fuzz-campaign run",
			CanRun:  true,
		}
	case !fuzzCampaignHasSeedArtifactForRun(campaign, run.ID):
		return fuzzCampaignAutomationPlan{
			Title: "Suggested next step: promote this attached run's source-only scenarios into corpus seeds.",
			Details: []string{
				"Campaign: " + campaign.ID,
				"Run: " + run.ID,
			},
			Command: "/fuzz-campaign run",
			CanRun:  true,
		}
	default:
		return fuzzCampaignAutomationPlan{
			Title: "This /fuzz-func run is already linked to the active fuzz campaign seed corpus.",
			Details: []string{
				"Campaign: " + campaign.ID,
				"Run: " + run.ID,
			},
			CanRun: false,
		}
	}
}

func renderFunctionFuzzCampaignHandoff(plan fuzzCampaignAutomationPlan) string {
	if strings.TrimSpace(plan.Title) == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Campaign handoff: %s\n", plan.Title)
	for _, detail := range plan.Details {
		fmt.Fprintf(&b, "- %s\n", detail)
	}
	if plan.CanRun && strings.TrimSpace(plan.Command) != "" {
		fmt.Fprintf(&b, "Continue: %s\n", plan.Command)
	}
	return strings.TrimRight(b.String(), "\n")
}

func buildFunctionFuzzRun(cfg Config, root string, query string) (FunctionFuzzRun, error) {
	artifacts, notes, err := prepareFunctionFuzzArtifactsForPlanning(cfg, root, query)
	if err != nil {
		return FunctionFuzzRun{}, err
	}
	return buildFunctionFuzzRunFromArtifacts(cfg, root, query, artifacts, notes)
}

func prepareFunctionFuzzArtifactsForPlanning(cfg Config, root string, query string) (latestAnalysisArtifacts, []string, error) {
	artifacts, ok := loadLatestProjectAnalysisArtifactsForRoot(cfg, root)
	if ok && hasSemanticIndexV2Data(artifacts.IndexV2) {
		notes := []string{}
		if len(artifacts.DocsManifest.Documents) > 0 {
			notes = append(notes, functionFuzzLocalizedText(cfg, "Loaded generated analysis docs manifest; FUZZ_TARGETS.md and SECURITY_SURFACE.md can guide target discovery.", "생성된 분석 문서 manifest를 로드했습니다. FUZZ_TARGETS.md와 SECURITY_SURFACE.md를 타깃 탐색에 재사용할 수 있습니다."))
		}
		return artifacts, notes, nil
	}

	notes := []string{}
	runID := time.Now().Format("20060102-150405")
	goal := fmt.Sprintf("on-demand function fuzz planning for %s", strings.TrimSpace(query))

	if ok && len(artifacts.Snapshot.Files) > 0 {
		unrealGraph := buildUnrealSemanticGraph(artifacts.Snapshot, goal, runID)
		artifacts.IndexV2 = buildSemanticIndexV2(artifacts.Snapshot, goal, runID, unrealGraph)
		if strings.TrimSpace(artifacts.Pack.RunID) == "" {
			artifacts.Pack.RunID = "on-demand-fuzz-" + runID
		}
		if strings.TrimSpace(artifacts.Pack.Goal) == "" {
			artifacts.Pack.Goal = goal
		}
		notes = append(notes, functionFuzzLocalizedText(cfg, "Rebuilt semantic function index on demand from latest snapshot; /analyze-project is optional for /fuzz-func.", "최신 snapshot에서 의미 함수 인덱스를 즉석 재구성했습니다. /analyze-project 없이도 /fuzz-func를 사용할 수 있습니다."))
		return artifacts, notes, nil
	}

	snapshot, err := scanProjectForFunctionFuzz(cfg, root)
	if err != nil {
		return latestAnalysisArtifacts{}, nil, err
	}
	unrealGraph := buildUnrealSemanticGraph(snapshot, goal, runID)
	artifacts = latestAnalysisArtifacts{
		Pack: KnowledgePack{
			RunID:       "on-demand-fuzz-" + runID,
			Goal:        goal,
			Root:        root,
			GeneratedAt: time.Now(),
		},
		Snapshot: snapshot,
		IndexV2:  buildSemanticIndexV2(snapshot, goal, runID, unrealGraph),
	}
	notes = append(notes, functionFuzzLocalizedText(cfg, "Built semantic function index on demand from the current workspace scan; /analyze-project is not required before /fuzz-func.", "현재 워크스페이스 스캔으로 의미 함수 인덱스를 즉석 생성했습니다. /analyze-project는 /fuzz-func의 선행조건이 아닙니다."))
	return artifacts, notes, nil
}

func scanProjectForFunctionFuzz(cfg Config, root string) (ProjectSnapshot, error) {
	analyzer := &projectAnalyzer{
		cfg:         cfg,
		analysisCfg: configProjectAnalysis(cfg, root),
		workspace: Workspace{
			Root:     root,
			BaseRoot: root,
		},
	}
	snapshot, err := analyzer.scanProject()
	if err != nil {
		return ProjectSnapshot{}, err
	}
	snapshot.AnalysisMode = "fuzz_func_on_demand"
	return snapshot, nil
}

func buildFunctionFuzzRunFromArtifacts(cfg Config, root string, query string, artifacts latestAnalysisArtifacts, extraNotes []string) (FunctionFuzzRun, error) {
	index := artifacts.IndexV2
	spec, err := parseFunctionFuzzTargetSpec(query)
	if err != nil {
		return FunctionFuzzRun{}, err
	}
	resolved, err := resolveFunctionFuzzPlan(index, spec, artifacts.DocsManifest)
	if err != nil {
		return FunctionFuzzRun{}, err
	}
	target := resolved.Target
	closure := resolved.Closure
	params := buildFunctionFuzzParameterStrategies(target.Signature)
	overlays := functionFuzzOverlayDomains(closure.OverlayEdges)
	sinks := buildFunctionFuzzSinkSignals(closure)
	observations := buildFunctionFuzzCodeObservations(root, target, params, closure)
	builds := functionFuzzBuildContextNames(closure.Builds)
	queryMode := functionFuzzQueryMode(overlays, sinks)
	primaryEngine, secondaryEngines, notes := functionFuzzEnginePlan(cfg, target, overlays, params, sinks)
	risk := functionFuzzRiskScore(target, overlays, params, sinks, observations, closure)
	harnessReady := functionFuzzHarnessReady(target, params)
	scenarios := buildFunctionFuzzVirtualScenarios(cfg, root, target, params, closure, sinks, overlays, observations)
	scenarios = functionFuzzAttachScenarioConcreteInputs(cfg, params, scenarios)
	scenarios = functionFuzzAttachScenarioConnections(index, resolved.ScopeRoot, closure, scenarios)

	run := FunctionFuzzRun{
		Workspace:           root,
		TargetQuery:         strings.TrimSpace(query),
		ScopeMode:           strings.TrimSpace(resolved.ScopeMode),
		ScopeRootFile:       strings.TrimSpace(resolved.ScopeRoot),
		ScopeFiles:          append([]string(nil), resolved.ScopeFiles...),
		TargetSymbolID:      target.ID,
		TargetSymbolName:    functionFuzzDisplayName(target),
		TargetSignature:     functionFuzzSanitizeSignature(target.Signature),
		TargetFile:          target.File,
		AnalysisRunID:       strings.TrimSpace(artifacts.Pack.RunID),
		AnalysisGoal:        strings.TrimSpace(artifacts.Pack.Goal),
		CreatedAt:           time.Now(),
		QueryMode:           queryMode,
		RiskScore:           risk,
		HarnessReady:        harnessReady,
		ReachableCallCount:  len(closure.CallEdges),
		ReachableDepth:      closure.MaxDepth,
		ReachableTruncated:  closure.Truncated,
		ReachableSymbols:    functionFuzzReachableSymbolNames(closure.Symbols),
		ReachableFiles:      closure.Files,
		OverlayDomains:      overlays,
		BuildContexts:       builds,
		ParameterStrategies: params,
		SinkSignals:         sinks,
		CodeObservations:    observations,
		VirtualScenarios:    scenarios,
		PrimaryEngine:       primaryEngine,
		SecondaryEngines:    secondaryEngines,
		Notes:               append(append([]string{}, notes...), resolved.ExtraNotes...),
		TargetStartLine:     target.StartLine,
		TargetEndLine:       target.EndLine,
	}
	if functionFuzzDocsCatalogBoost(target, artifacts.DocsManifest) > 0 {
		run.Notes = append(run.Notes, functionFuzzLocalizedText(cfg, "Generated FUZZ_TARGETS.md catalog contributed to target ranking for this run.", "생성된 FUZZ_TARGETS.md catalog가 이번 실행의 타깃 순위 결정에 반영되었습니다."))
	}
	run.Interpretation, run.NextSteps, run.SuggestedTargets = buildFunctionFuzzGuidance(cfg, target, closure, run)
	run.SuggestedCommands = functionFuzzSuggestedCommands(target, closure)
	run = normalizeFunctionFuzzRun(run)
	if closure.Truncated {
		run.Notes = append(run.Notes, functionFuzzLocalizedText(cfg, fmt.Sprintf("Call closure was capped at %d reachable symbols to keep planning bounded.", functionFuzzMaxClosureNodes), fmt.Sprintf("계획 크기를 제어하기 위해 호출 closure를 최대 %d개 심볼에서 제한했습니다.", functionFuzzMaxClosureNodes)))
		run.Notes = uniqueStrings(run.Notes)
	}
	if len(extraNotes) > 0 {
		run.Notes = append(run.Notes, extraNotes...)
		run.Notes = uniqueStrings(run.Notes)
	}
	if err := prepareFunctionFuzzArtifacts(&run); err != nil {
		return FunctionFuzzRun{}, err
	}
	planFunctionFuzzExecution(cfg, &run, target, closure, artifacts)
	run.Interpretation, run.NextSteps, run.SuggestedTargets = buildFunctionFuzzGuidance(cfg, target, closure, run)
	run.SuggestedCommands = functionFuzzSuggestedCommands(target, closure)
	run.Summary = buildFunctionFuzzSummaryWithConfig(run, cfg)
	if err := writeFunctionFuzzArtifacts(&run, closure, cfg); err != nil {
		return FunctionFuzzRun{}, err
	}
	return normalizeFunctionFuzzRun(run), nil
}

func parseFunctionFuzzTargetSpec(query string) (functionFuzzTargetSpec, error) {
	spec := functionFuzzTargetSpec{
		Raw: strings.TrimSpace(query),
	}
	if spec.Raw == "" {
		return functionFuzzTargetSpec{}, fmt.Errorf("usage: %s", functionFuzzUsage())
	}
	tokens := splitAnalysisCommandLine(spec.Raw)
	if len(tokens) == 0 {
		return functionFuzzTargetSpec{}, fmt.Errorf("usage: %s", functionFuzzUsage())
	}
	for index := 0; index < len(tokens); index++ {
		token := strings.TrimSpace(tokens[index])
		if token == "" {
			continue
		}
		switch {
		case token == "@":
			if spec.FileHint != "" {
				return functionFuzzTargetSpec{}, fmt.Errorf("usage: %s", functionFuzzUsage())
			}
			if index+1 >= len(tokens) {
				return functionFuzzTargetSpec{}, fmt.Errorf("usage: %s", functionFuzzUsage())
			}
			index++
			spec.FileHint = strings.TrimSpace(tokens[index])
			if spec.FileHint == "" {
				return functionFuzzTargetSpec{}, fmt.Errorf("usage: %s", functionFuzzUsage())
			}
		case strings.HasPrefix(token, "@"):
			if spec.FileHint != "" {
				return functionFuzzTargetSpec{}, fmt.Errorf("usage: %s", functionFuzzUsage())
			}
			spec.FileHint = strings.TrimSpace(strings.TrimPrefix(token, "@"))
			if spec.FileHint == "" {
				return functionFuzzTargetSpec{}, fmt.Errorf("usage: %s", functionFuzzUsage())
			}
		case token == "--file":
			if spec.FileHint != "" {
				return functionFuzzTargetSpec{}, fmt.Errorf("usage: %s", functionFuzzUsage())
			}
			if index+1 >= len(tokens) {
				return functionFuzzTargetSpec{}, fmt.Errorf("usage: %s", functionFuzzUsage())
			}
			index++
			spec.FileHint = strings.TrimSpace(tokens[index])
			if spec.FileHint == "" {
				return functionFuzzTargetSpec{}, fmt.Errorf("usage: %s", functionFuzzUsage())
			}
		case strings.HasPrefix(token, "--file="):
			if spec.FileHint != "" {
				return functionFuzzTargetSpec{}, fmt.Errorf("usage: %s", functionFuzzUsage())
			}
			spec.FileHint = strings.TrimSpace(strings.TrimPrefix(token, "--file="))
			if spec.FileHint == "" {
				return functionFuzzTargetSpec{}, fmt.Errorf("usage: %s", functionFuzzUsage())
			}
		case strings.HasPrefix(token, "--"):
			return functionFuzzTargetSpec{}, fmt.Errorf("unsupported /fuzz-func option: %s", token)
		default:
			if spec.Name != "" {
				return functionFuzzTargetSpec{}, fmt.Errorf("usage: %s", functionFuzzUsage())
			}
			spec.Name = token
		}
	}
	if strings.TrimSpace(spec.Name) == "" && strings.TrimSpace(spec.FileHint) == "" {
		return functionFuzzTargetSpec{}, fmt.Errorf("usage: %s", functionFuzzUsage())
	}
	return spec, nil
}

func resolveFunctionFuzzTarget(index SemanticIndexV2, spec functionFuzzTargetSpec, manifest AnalysisDocsManifest) (SymbolRecord, error) {
	spec.Raw = strings.TrimSpace(spec.Raw)
	spec.Name = strings.TrimSpace(spec.Name)
	spec.FileHint = strings.TrimSpace(spec.FileHint)
	if spec.Name == "" {
		return SymbolRecord{}, fmt.Errorf("usage: %s", functionFuzzUsage())
	}
	type scored struct {
		symbol SymbolRecord
		score  int
	}
	var items []scored
	fileMatched := false
	for _, symbol := range index.Symbols {
		if spec.FileHint != "" {
			fileScore := functionFuzzFileHintScore(index.Root, spec.FileHint, symbol.File)
			if fileScore <= 0 {
				continue
			}
			fileMatched = true
			score := functionFuzzSymbolScore(symbol, spec.Name, index) + fileScore + functionFuzzDocsCatalogBoost(symbol, manifest)
			if score <= 0 {
				continue
			}
			items = append(items, scored{symbol: symbol, score: score})
			continue
		}
		score := functionFuzzSymbolScore(symbol, spec.Name, index) + functionFuzzDocsCatalogBoost(symbol, manifest)
		if score <= 0 {
			continue
		}
		items = append(items, scored{symbol: symbol, score: score})
	}
	if len(items) == 0 {
		if spec.FileHint != "" {
			if !fileMatched {
				return SymbolRecord{}, fmt.Errorf("file hint did not match any indexed source file for /fuzz-func: %s", spec.FileHint)
			}
			return SymbolRecord{}, fmt.Errorf("function-like symbol %q not found under file hint %q", spec.Name, spec.FileHint)
		}
		return SymbolRecord{}, fmt.Errorf("function-like symbol not found in latest structural_index_v2: %s", spec.Name)
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].score == items[j].score {
			left := functionFuzzDisplayName(items[i].symbol) + "|" + items[i].symbol.ID
			right := functionFuzzDisplayName(items[j].symbol) + "|" + items[j].symbol.ID
			return left < right
		}
		return items[i].score > items[j].score
	})
	return items[0].symbol, nil
}

func resolveFunctionFuzzPlan(index SemanticIndexV2, spec functionFuzzTargetSpec, manifest AnalysisDocsManifest) (functionFuzzResolvedPlan, error) {
	spec.Raw = strings.TrimSpace(spec.Raw)
	spec.Name = strings.TrimSpace(spec.Name)
	spec.FileHint = strings.TrimSpace(spec.FileHint)
	if spec.Name != "" {
		target, err := resolveFunctionFuzzTarget(index, spec, manifest)
		if err != nil {
			return functionFuzzResolvedPlan{}, err
		}
		return functionFuzzResolvedPlan{
			Target:    target,
			Closure:   buildFunctionFuzzClosure(index, target),
			ScopeMode: "function",
		}, nil
	}
	scopeRoot, scopeFiles, err := functionFuzzResolveFileScope(index, spec.FileHint)
	if err != nil {
		return functionFuzzResolvedPlan{}, err
	}
	target, err := functionFuzzSelectRepresentativeFileScopeTarget(index, scopeRoot, scopeFiles, manifest)
	if err != nil {
		return functionFuzzResolvedPlan{}, err
	}
	return functionFuzzResolvedPlan{
		Target:     target,
		Closure:    functionFuzzBuildScopedClosure(index, target, scopeFiles),
		ScopeMode:  "file",
		ScopeRoot:  scopeRoot,
		ScopeFiles: scopeFiles,
		ExtraNotes: []string{
			fmt.Sprintf("File-scope mode expanded from %s across %d file(s) and automatically selected %s as the representative function root.", filepath.ToSlash(strings.TrimSpace(scopeRoot)), len(scopeFiles), functionFuzzDisplayName(target)),
		},
	}, nil
}

func functionFuzzResolveFileScope(index SemanticIndexV2, hint string) (string, []string, error) {
	candidates := functionFuzzIndexedFileCandidates(index)
	if len(candidates) == 0 {
		return "", nil, fmt.Errorf("no indexed source files were available for /fuzz-func file scope resolution")
	}
	rootFile, score := functionFuzzBestMatchingIndexedFile(index.Root, hint, candidates)
	if score <= 0 || strings.TrimSpace(rootFile) == "" {
		return "", nil, fmt.Errorf("file hint did not match any indexed source file for /fuzz-func: %s", hint)
	}
	scopeFiles := functionFuzzExpandFileScope(index, rootFile)
	if len(scopeFiles) == 0 {
		scopeFiles = []string{rootFile}
	}
	return rootFile, scopeFiles, nil
}

func functionFuzzIndexedFileCandidates(index SemanticIndexV2) []string {
	items := []string{}
	for _, file := range index.Files {
		if strings.TrimSpace(file.Path) != "" {
			items = append(items, filepath.ToSlash(strings.TrimSpace(file.Path)))
		}
	}
	for _, symbol := range index.Symbols {
		if strings.TrimSpace(symbol.File) != "" {
			items = append(items, filepath.ToSlash(strings.TrimSpace(symbol.File)))
		}
	}
	for _, ref := range index.References {
		if strings.TrimSpace(ref.SourceFile) != "" {
			items = append(items, filepath.ToSlash(strings.TrimSpace(ref.SourceFile)))
		}
		if strings.TrimSpace(ref.TargetPath) != "" {
			items = append(items, filepath.ToSlash(strings.TrimSpace(ref.TargetPath)))
		}
	}
	return normalizeFunctionFuzzPaths(items)
}

func functionFuzzBestMatchingIndexedFile(root string, hint string, candidates []string) (string, int) {
	bestPath := ""
	bestScore := 0
	for _, candidate := range candidates {
		score := functionFuzzFileHintScore(root, hint, candidate)
		if score <= 0 {
			continue
		}
		normalized := filepath.ToSlash(strings.TrimSpace(candidate))
		if score > bestScore || (score == bestScore && (bestPath == "" || normalized < bestPath)) {
			bestPath = normalized
			bestScore = score
		}
	}
	return bestPath, bestScore
}

func functionFuzzExpandFileScope(index SemanticIndexV2, rootFile string) []string {
	rootFile = filepath.ToSlash(strings.TrimSpace(rootFile))
	if rootFile == "" {
		return nil
	}
	adj := functionFuzzFileDependencyAdjacency(index)
	if len(adj) == 0 {
		return []string{rootFile}
	}
	queue := []string{rootFile}
	visited := map[string]struct{}{rootFile: {}}
	out := []string{rootFile}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range uniqueStrings(adj[current]) {
			next = filepath.ToSlash(strings.TrimSpace(next))
			if next == "" {
				continue
			}
			if _, ok := visited[next]; ok {
				continue
			}
			visited[next] = struct{}{}
			queue = append(queue, next)
			out = append(out, next)
		}
	}
	return normalizeFunctionFuzzPaths(out)
}

func functionFuzzFileDependencyAdjacency(index SemanticIndexV2) map[string][]string {
	candidates := functionFuzzIndexedFileCandidates(index)
	if len(candidates) == 0 {
		return nil
	}
	adj := map[string][]string{}
	for _, ref := range index.References {
		if !functionFuzzIsFileDependencyReference(ref) {
			continue
		}
		source, sourceScore := functionFuzzBestMatchingIndexedFile(index.Root, ref.SourceFile, candidates)
		if sourceScore <= 0 || strings.TrimSpace(source) == "" {
			continue
		}
		targetHints := functionFuzzReferenceTargetHints(index.Root, source, ref.TargetPath)
		target := ""
		targetScore := 0
		for _, hint := range targetHints {
			match, score := functionFuzzBestMatchingIndexedFile(index.Root, hint, candidates)
			if score > targetScore {
				target = match
				targetScore = score
			}
		}
		if targetScore <= 0 || strings.TrimSpace(target) == "" {
			continue
		}
		adj[source] = append(adj[source], target)
	}
	for key, values := range adj {
		adj[key] = uniqueStrings(values)
	}
	return adj
}

func functionFuzzShortestFileScopePath(index SemanticIndexV2, rootFile string, targetFile string) []string {
	rootFile = filepath.ToSlash(strings.TrimSpace(rootFile))
	targetFile = filepath.ToSlash(strings.TrimSpace(targetFile))
	if rootFile == "" || targetFile == "" {
		return nil
	}
	candidates := functionFuzzIndexedFileCandidates(index)
	if len(candidates) == 0 {
		if strings.EqualFold(rootFile, targetFile) {
			return []string{rootFile}
		}
		return nil
	}
	resolvedRoot, rootScore := functionFuzzBestMatchingIndexedFile(index.Root, rootFile, candidates)
	if rootScore > 0 && strings.TrimSpace(resolvedRoot) != "" {
		rootFile = resolvedRoot
	}
	resolvedTarget, targetScore := functionFuzzBestMatchingIndexedFile(index.Root, targetFile, candidates)
	if targetScore > 0 && strings.TrimSpace(resolvedTarget) != "" {
		targetFile = resolvedTarget
	}
	if strings.EqualFold(rootFile, targetFile) {
		return []string{rootFile}
	}
	adj := functionFuzzFileDependencyAdjacency(index)
	if len(adj) == 0 {
		return nil
	}
	prev := map[string]string{}
	visited := map[string]struct{}{rootFile: {}}
	queue := []string{rootFile}
	found := false
	for len(queue) > 0 && !found {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adj[current] {
			next = filepath.ToSlash(strings.TrimSpace(next))
			if next == "" {
				continue
			}
			if _, ok := visited[next]; ok {
				continue
			}
			visited[next] = struct{}{}
			prev[next] = current
			if strings.EqualFold(next, targetFile) {
				found = true
				break
			}
			queue = append(queue, next)
		}
	}
	if !found {
		return nil
	}
	path := []string{targetFile}
	for current := targetFile; !strings.EqualFold(current, rootFile); {
		parent, ok := prev[current]
		if !ok {
			break
		}
		path = append(path, parent)
		current = parent
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return normalizeFunctionFuzzPaths(path)
}

func functionFuzzShortestSymbolIDPath(closure functionFuzzClosure, targetID string) []string {
	targetID = strings.TrimSpace(targetID)
	rootID := strings.TrimSpace(closure.RootSymbol.ID)
	if targetID == "" || rootID == "" {
		return nil
	}
	if strings.EqualFold(rootID, targetID) {
		return []string{rootID}
	}
	adj := map[string][]string{}
	for _, edge := range closure.CallEdges {
		sourceID := strings.TrimSpace(edge.SourceID)
		nextID := strings.TrimSpace(edge.TargetID)
		if sourceID == "" || nextID == "" {
			continue
		}
		adj[sourceID] = append(adj[sourceID], nextID)
	}
	prev := map[string]string{}
	visited := map[string]struct{}{rootID: {}}
	queue := []string{rootID}
	found := false
	for len(queue) > 0 && !found {
		current := queue[0]
		queue = queue[1:]
		for _, nextID := range adj[current] {
			if _, ok := visited[nextID]; ok {
				continue
			}
			visited[nextID] = struct{}{}
			prev[nextID] = current
			if strings.EqualFold(nextID, targetID) {
				found = true
				break
			}
			queue = append(queue, nextID)
		}
	}
	if !found {
		return nil
	}
	ids := []string{targetID}
	for current := targetID; !strings.EqualFold(current, rootID); {
		parent, ok := prev[current]
		if !ok {
			break
		}
		ids = append(ids, parent)
		current = parent
	}
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	return ids
}

func functionFuzzIsFileDependencyReference(ref ReferenceRecord) bool {
	lowerType := strings.ToLower(strings.TrimSpace(ref.Type))
	if lowerType == "" {
		return false
	}
	return containsAny(lowerType, "file_import", "import", "include", "header")
}

func functionFuzzReferenceTargetHints(root string, sourceFile string, targetPath string) []string {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return nil
	}
	items := []string{targetPath}
	if filepath.IsAbs(targetPath) {
		return normalizeFunctionFuzzPaths(items)
	}
	items = append(items, functionFuzzResolveWorkspacePath(root, targetPath))
	sourceAbs := functionFuzzResolveWorkspacePath(root, sourceFile)
	if strings.TrimSpace(sourceAbs) != "" {
		items = append(items, filepath.Join(filepath.Dir(sourceAbs), targetPath))
	}
	sourceDir := pathDir(filepath.ToSlash(strings.TrimSpace(sourceFile)))
	if sourceDir != "" {
		items = append(items, filepath.ToSlash(filepath.Join(sourceDir, targetPath)))
	}
	return normalizeFunctionFuzzPaths(items)
}

func functionFuzzSelectRepresentativeFileScopeTarget(index SemanticIndexV2, scopeRootFile string, scopeFiles []string, manifest AnalysisDocsManifest) (SymbolRecord, error) {
	scopeClosure := functionFuzzBuildScopeOnlyClosure(index, scopeRootFile, scopeFiles)
	if len(scopeClosure.Symbols) == 0 {
		return SymbolRecord{}, fmt.Errorf("no function-like symbols were found in file scope rooted at %q", scopeRootFile)
	}
	signalWeights := map[string]int{}
	outgoingCounts := map[string]int{}
	overlayCounts := map[string]int{}
	for _, edge := range scopeClosure.CallEdges {
		outgoingCounts[strings.TrimSpace(edge.SourceID)]++
	}
	for _, edge := range scopeClosure.OverlayEdges {
		overlayCounts[strings.TrimSpace(edge.SourceID)]++
	}
	for _, signal := range buildFunctionFuzzSinkSignals(scopeClosure) {
		weight := 0
		switch signal.Kind {
		case "copy_like", "compare_like", "parse_like":
			weight = 16
		case "overlay":
			weight = 20
		case "alloc_like":
			weight = 8
		}
		if weight == 0 {
			continue
		}
		signalWeights[strings.TrimSpace(signal.SymbolID)] += weight
	}
	best := SymbolRecord{}
	bestScore := -1
	for _, symbol := range scopeClosure.Symbols {
		if !functionFuzzIsCallableSymbol(symbol) {
			continue
		}
		params := buildFunctionFuzzParameterStrategies(symbol.Signature)
		corpus := functionFuzzSymbolCorpus(symbol)
		score := signalWeights[strings.TrimSpace(symbol.ID)]
		if functionFuzzHarnessReady(symbol, params) {
			score += 40
		}
		if functionFuzzSymbolLooksInputFacing(symbol, params) {
			score += 28
		}
		if functionFuzzHasDirectInputParams(params) {
			score += 12
		}
		if functionFuzzHasLengthBufferRelation(params) {
			score += 8
		}
		score += functionFuzzMin(outgoingCounts[strings.TrimSpace(symbol.ID)], 3) * 10
		score += functionFuzzMin(overlayCounts[strings.TrimSpace(symbol.ID)], 2) * 12
		score += functionFuzzSuggestedTargetPathScore(scopeRootFile, symbol.File)
		score += functionFuzzSuggestedTargetSignalBonus(symbol, params)
		score += functionFuzzDocsCatalogBoost(symbol, manifest)
		if strings.EqualFold(filepath.ToSlash(strings.TrimSpace(symbol.File)), filepath.ToSlash(strings.TrimSpace(scopeRootFile))) {
			score += 10
		}
		if outgoingCounts[strings.TrimSpace(symbol.ID)] == 0 && containsAny(corpus, "validate", "check", "verify", "guard") {
			score -= 6
		}
		if functionFuzzLooksLikeEntryRoot(symbol) {
			score -= 24
		}
		score -= functionFuzzSuggestedTargetPenalty(symbol, params)
		if score > bestScore || (score == bestScore && functionFuzzDisplayName(symbol) < functionFuzzDisplayName(best)) {
			best = symbol
			bestScore = score
		}
	}
	if strings.TrimSpace(best.ID) == "" {
		return SymbolRecord{}, fmt.Errorf("no usable function-like symbols were found in file scope rooted at %q", scopeRootFile)
	}
	return best, nil
}

func functionFuzzBuildScopeOnlyClosure(index SemanticIndexV2, scopeRootFile string, scopeFiles []string) functionFuzzClosure {
	scopeSet := functionFuzzScopeFileSet(index.Root, scopeFiles)
	symbols := []SymbolRecord{}
	symbolSeen := map[string]struct{}{}
	for _, symbol := range index.Symbols {
		if !functionFuzzFileInScope(index.Root, symbol.File, scopeSet) {
			continue
		}
		if !functionFuzzIsCallableSymbol(symbol) {
			continue
		}
		if _, ok := symbolSeen[symbol.ID]; ok {
			continue
		}
		symbolSeen[symbol.ID] = struct{}{}
		symbols = append(symbols, symbol)
	}
	callEdges := []CallEdge{}
	callSeen := map[string]struct{}{}
	for _, edge := range index.CallEdges {
		if _, ok := symbolSeen[strings.TrimSpace(edge.SourceID)]; !ok {
			continue
		}
		if _, ok := symbolSeen[strings.TrimSpace(edge.TargetID)]; !ok {
			continue
		}
		key := strings.TrimSpace(edge.SourceID) + "|" + strings.TrimSpace(edge.Type) + "|" + strings.TrimSpace(edge.TargetID)
		if _, ok := callSeen[key]; ok {
			continue
		}
		callSeen[key] = struct{}{}
		callEdges = append(callEdges, edge)
	}
	builds := functionFuzzBuildContextsForScope(index, symbols, scopeSet)
	closure := functionFuzzClosure{
		RootSymbol: SymbolRecord{
			Name: scopeRootFile,
			File: scopeRootFile,
			Kind: "file_scope",
		},
		Symbols:   symbols,
		CallEdges: callEdges,
		Builds:    builds,
		Files:     normalizeFunctionFuzzPaths(scopeFiles),
	}
	closure.OverlayEdges = functionFuzzOverlayEdgesForScope(index, symbols, scopeSet)
	return closure
}

func functionFuzzBuildScopedClosure(index SemanticIndexV2, target SymbolRecord, scopeFiles []string) functionFuzzClosure {
	closure := buildFunctionFuzzClosure(index, target)
	scopeOnly := functionFuzzBuildScopeOnlyClosure(index, target.File, scopeFiles)
	symbolByID := map[string]SymbolRecord{}
	for _, symbol := range closure.Symbols {
		symbolByID[strings.TrimSpace(symbol.ID)] = symbol
	}
	for _, symbol := range scopeOnly.Symbols {
		symbolByID[strings.TrimSpace(symbol.ID)] = symbol
	}
	mergedSymbols := make([]SymbolRecord, 0, len(symbolByID))
	for _, symbol := range symbolByID {
		mergedSymbols = append(mergedSymbols, symbol)
	}
	sort.Slice(mergedSymbols, func(i int, j int) bool {
		left := functionFuzzDisplayName(mergedSymbols[i]) + "|" + strings.TrimSpace(mergedSymbols[i].ID)
		right := functionFuzzDisplayName(mergedSymbols[j]) + "|" + strings.TrimSpace(mergedSymbols[j].ID)
		return left < right
	})
	symbolSet := map[string]struct{}{}
	for _, symbol := range mergedSymbols {
		symbolSet[strings.TrimSpace(symbol.ID)] = struct{}{}
	}
	callEdges := []CallEdge{}
	callSeen := map[string]struct{}{}
	for _, edge := range append(append([]CallEdge{}, closure.CallEdges...), scopeOnly.CallEdges...) {
		if _, ok := symbolSet[strings.TrimSpace(edge.SourceID)]; !ok {
			continue
		}
		if _, ok := symbolSet[strings.TrimSpace(edge.TargetID)]; !ok {
			continue
		}
		key := strings.TrimSpace(edge.SourceID) + "|" + strings.TrimSpace(edge.Type) + "|" + strings.TrimSpace(edge.TargetID)
		if _, ok := callSeen[key]; ok {
			continue
		}
		callSeen[key] = struct{}{}
		callEdges = append(callEdges, edge)
	}
	overlayEdges := append(append([]OverlayEdge{}, closure.OverlayEdges...), scopeOnly.OverlayEdges...)
	builds := append(append([]BuildContextRecord{}, closure.Builds...), scopeOnly.Builds...)
	closure.Symbols = mergedSymbols
	closure.CallEdges = callEdges
	closure.OverlayEdges = normalizeFunctionFuzzOverlayEdges(overlayEdges)
	closure.Builds = functionFuzzUniqueBuildContexts(builds)
	closure.Files = normalizeFunctionFuzzPaths(append(append([]string{}, closure.Files...), scopeFiles...))
	closure.MaxDepth = functionFuzzClosureDepth(target.ID, callEdges)
	return closure
}

func functionFuzzScopeFileSet(root string, files []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, file := range files {
		resolved := strings.ToLower(filepath.Clean(functionFuzzResolveWorkspacePath(root, file)))
		if strings.TrimSpace(resolved) == "" {
			continue
		}
		out[resolved] = struct{}{}
	}
	return out
}

func functionFuzzFileInScope(root string, file string, scopeSet map[string]struct{}) bool {
	if len(scopeSet) == 0 {
		return false
	}
	resolved := strings.ToLower(filepath.Clean(functionFuzzResolveWorkspacePath(root, file)))
	if strings.TrimSpace(resolved) == "" {
		return false
	}
	_, ok := scopeSet[resolved]
	return ok
}

func functionFuzzBuildContextsForScope(index SemanticIndexV2, symbols []SymbolRecord, scopeSet map[string]struct{}) []BuildContextRecord {
	out := []BuildContextRecord{}
	seen := map[string]struct{}{}
	for _, build := range index.BuildContexts {
		match := false
		for _, file := range build.Files {
			if functionFuzzFileInScope(index.Root, file, scopeSet) {
				match = true
				break
			}
		}
		if !match {
			for _, symbol := range symbols {
				if strings.TrimSpace(symbol.BuildContextID) != "" && strings.EqualFold(strings.TrimSpace(symbol.BuildContextID), strings.TrimSpace(build.ID)) {
					match = true
					break
				}
			}
		}
		if !match {
			continue
		}
		key := strings.TrimSpace(build.ID)
		if key == "" {
			key = strings.TrimSpace(build.Name)
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, build)
	}
	return out
}

func functionFuzzOverlayEdgesForScope(index SemanticIndexV2, symbols []SymbolRecord, scopeSet map[string]struct{}) []OverlayEdge {
	symbolSet := map[string]struct{}{}
	symbolFileByID := map[string]string{}
	for _, symbol := range symbols {
		symbolSet[strings.TrimSpace(symbol.ID)] = struct{}{}
		symbolFileByID[strings.TrimSpace(symbol.ID)] = strings.TrimSpace(symbol.File)
	}
	out := []OverlayEdge{}
	seen := map[string]struct{}{}
	for _, edge := range index.OverlayEdges {
		include := false
		if _, ok := symbolSet[strings.TrimSpace(edge.SourceID)]; ok {
			include = true
		}
		if !include {
			if functionFuzzFileInScope(index.Root, symbolFileByID[strings.TrimSpace(edge.SourceID)], scopeSet) {
				include = true
			}
		}
		if !include {
			continue
		}
		key := strings.TrimSpace(edge.Domain) + "|" + strings.TrimSpace(edge.SourceID) + "|" + strings.TrimSpace(edge.Type) + "|" + strings.TrimSpace(edge.TargetID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, edge)
	}
	return out
}

func functionFuzzUniqueBuildContexts(items []BuildContextRecord) []BuildContextRecord {
	if len(items) == 0 {
		return nil
	}
	out := make([]BuildContextRecord, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		key := strings.TrimSpace(item.ID)
		if key == "" {
			key = strings.TrimSpace(item.Name)
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i int, j int) bool {
		left := strings.TrimSpace(out[i].Name)
		if left == "" {
			left = strings.TrimSpace(out[i].ID)
		}
		right := strings.TrimSpace(out[j].Name)
		if right == "" {
			right = strings.TrimSpace(out[j].ID)
		}
		return left < right
	})
	return out
}

func normalizeFunctionFuzzOverlayEdges(items []OverlayEdge) []OverlayEdge {
	if len(items) == 0 {
		return nil
	}
	out := make([]OverlayEdge, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		key := strings.TrimSpace(item.Domain) + "|" + strings.TrimSpace(item.SourceID) + "|" + strings.TrimSpace(item.Type) + "|" + strings.TrimSpace(item.TargetID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i int, j int) bool {
		left := strings.TrimSpace(out[i].Domain) + "|" + strings.TrimSpace(out[i].SourceID)
		right := strings.TrimSpace(out[j].Domain) + "|" + strings.TrimSpace(out[j].SourceID)
		return left < right
	})
	return out
}

func functionFuzzClosureDepth(rootID string, edges []CallEdge) int {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" || len(edges) == 0 {
		return 0
	}
	adj := map[string][]string{}
	for _, edge := range edges {
		sourceID := strings.TrimSpace(edge.SourceID)
		targetID := strings.TrimSpace(edge.TargetID)
		if sourceID == "" || targetID == "" {
			continue
		}
		adj[sourceID] = append(adj[sourceID], targetID)
	}
	visited := map[string]int{rootID: 0}
	queue := []string{rootID}
	maxDepth := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		depth := visited[current]
		for _, next := range uniqueStrings(adj[current]) {
			if _, ok := visited[next]; ok {
				continue
			}
			visited[next] = depth + 1
			if depth+1 > maxDepth {
				maxDepth = depth + 1
			}
			queue = append(queue, next)
		}
	}
	return maxDepth
}

func functionFuzzFileHintScore(root string, hint string, symbolFile string) int {
	hint = strings.TrimSpace(hint)
	symbolFile = strings.TrimSpace(symbolFile)
	if hint == "" || symbolFile == "" {
		return 0
	}
	hintAbs := strings.ToLower(filepath.Clean(functionFuzzResolveWorkspacePath(root, hint)))
	symbolAbs := strings.ToLower(filepath.Clean(functionFuzzResolveWorkspacePath(root, symbolFile)))
	if hintAbs == "" || symbolAbs == "" {
		return 0
	}
	if hintAbs == symbolAbs {
		return 260
	}
	hintRel := strings.ToLower(filepath.ToSlash(relOrAbs(root, hintAbs)))
	symbolRel := strings.ToLower(filepath.ToSlash(relOrAbs(root, symbolAbs)))
	if hintRel == symbolRel {
		return 240
	}
	if strings.HasSuffix(symbolRel, hintRel) || strings.HasSuffix(symbolAbs, hintAbs) {
		return 200
	}
	if filepath.Base(hintAbs) == filepath.Base(symbolAbs) {
		return 80
	}
	return 0
}

func functionFuzzSymbolScore(symbol SymbolRecord, query string, index SemanticIndexV2) int {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0
	}
	lowerQuery := strings.ToLower(query)
	name := strings.TrimSpace(symbol.Name)
	canonical := strings.TrimSpace(symbol.CanonicalName)
	signature := functionFuzzSignatureOrRaw(symbol.Signature)
	display := functionFuzzDisplayName(symbol)
	lowerName := strings.ToLower(name)
	lowerCanonical := strings.ToLower(canonical)
	lowerSignature := strings.ToLower(signature)
	lowerDisplay := strings.ToLower(display)

	score := 0
	if functionFuzzIsCallableSymbol(symbol) {
		score += 25
	}
	if strings.EqualFold(symbol.ID, query) {
		score += 160
	}
	if strings.EqualFold(name, query) || strings.EqualFold(display, query) {
		score += 150
	}
	if strings.EqualFold(functionFuzzQualifiedLeaf(display), query) || strings.EqualFold(functionFuzzQualifiedLeaf(name), query) {
		score += 140
	}
	if canonical != "" && strings.EqualFold(canonical, query) {
		score += 145
	}
	if strings.Contains(lowerName, lowerQuery) {
		score += 70
	}
	if strings.Contains(lowerDisplay, lowerQuery) {
		score += 85
	}
	if strings.Contains(lowerCanonical, lowerQuery) {
		score += 60
	}
	if strings.Contains(lowerSignature, lowerQuery) {
		score += 40
	}
	if strings.HasSuffix(lowerCanonical, lowerQuery) || strings.HasSuffix(lowerDisplay, lowerQuery) {
		score += 25
	}
	if strings.TrimSpace(symbol.File) != "" {
		score += 4
	}
	switch strings.ToLower(strings.TrimSpace(symbol.Kind)) {
	case "function":
		score += 20
	case "method":
		score += 18
	case "rpc":
		score += 16
	case "ioctl_handler", "entrypoint", "handler", "callback":
		score += 12
	}
	outgoing := 0
	for _, edge := range index.CallEdges {
		if edge.SourceID == symbol.ID {
			outgoing++
		}
	}
	if outgoing > 0 {
		score += functionFuzzMin(outgoing, 8)
	}
	return score
}

func functionFuzzDocsCatalogBoost(symbol SymbolRecord, manifest AnalysisDocsManifest) int {
	if len(manifest.FuzzTargets) == 0 {
		return 0
	}
	best := 0
	for _, target := range manifest.FuzzTargets {
		if !functionFuzzDocsCatalogEntryMatchesSymbol(target, symbol) {
			continue
		}
		boost := target.PriorityScore / 4
		switch strings.TrimSpace(target.HarnessReadiness) {
		case "ready":
			boost += 8
		case "needs_binding":
			boost += 3
		}
		switch strings.TrimSpace(target.BuildContextLevel) {
		case "symbol_build_context", "indexed_build_context", "compile_commands":
			boost += 6
		case "source_only":
			boost -= 4
		}
		if boost > best {
			best = boost
		}
	}
	if best < 0 {
		return 0
	}
	if best > 40 {
		return 40
	}
	return best
}

func functionFuzzDocsCatalogEntryMatchesSymbol(entry AnalysisFuzzTargetCatalogEntry, symbol SymbolRecord) bool {
	if strings.TrimSpace(entry.SymbolID) != "" && strings.EqualFold(strings.TrimSpace(entry.SymbolID), strings.TrimSpace(symbol.ID)) {
		return true
	}
	if strings.TrimSpace(entry.Name) != "" {
		display := functionFuzzDisplayName(symbol)
		if strings.EqualFold(strings.TrimSpace(entry.Name), strings.TrimSpace(display)) ||
			strings.EqualFold(strings.TrimSpace(entry.Name), strings.TrimSpace(symbol.Name)) ||
			strings.EqualFold(strings.TrimSpace(entry.Name), strings.TrimSpace(symbol.CanonicalName)) {
			return true
		}
	}
	entryFile := filepath.ToSlash(strings.TrimSpace(entry.File))
	symbolFile := filepath.ToSlash(strings.TrimSpace(symbol.File))
	if entryFile != "" && symbolFile != "" && strings.EqualFold(entryFile, symbolFile) {
		if strings.TrimSpace(entry.Name) == "" {
			return true
		}
		if strings.Contains(strings.ToLower(functionFuzzDisplayName(symbol)), strings.ToLower(strings.TrimSpace(entry.Name))) {
			return true
		}
	}
	return false
}

func functionFuzzIsCallableSymbol(symbol SymbolRecord) bool {
	kind := strings.ToLower(strings.TrimSpace(symbol.Kind))
	if functionFuzzSanitizeSignature(symbol.Signature) != "" {
		return true
	}
	switch kind {
	case "function", "method", "rpc", "ioctl_handler", "entrypoint", "handler", "callback":
		return true
	}
	return strings.Contains(kind, "func") || strings.Contains(kind, "call")
}

func functionFuzzDisplayName(symbol SymbolRecord) string {
	if strings.TrimSpace(symbol.Name) != "" {
		return strings.TrimSpace(symbol.Name)
	}
	if strings.TrimSpace(symbol.CanonicalName) != "" {
		return strings.TrimSpace(symbol.CanonicalName)
	}
	return strings.TrimSpace(symbol.ID)
}

func functionFuzzQualifiedLeaf(name string) string {
	name = strings.TrimSpace(name)
	if idx := strings.LastIndex(name, "::"); idx >= 0 && idx+2 < len(name) {
		return strings.TrimSpace(name[idx+2:])
	}
	return name
}

func functionFuzzStripCommentsPreserveLines(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	var b strings.Builder
	inBlockComment := false
	inLineComment := false
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false
	for index := 0; index < len(text); index++ {
		ch := text[index]
		next := byte(0)
		if index+1 < len(text) {
			next = text[index+1]
		}
		if inBlockComment {
			if ch == '*' && next == '/' {
				inBlockComment = false
				index++
				continue
			}
			if ch == '\n' {
				b.WriteByte('\n')
			}
			continue
		}
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				b.WriteByte('\n')
			}
			continue
		}
		if inSingleQuote {
			b.WriteByte(ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '\'' {
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			b.WriteByte(ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inDoubleQuote = false
			}
			continue
		}
		if ch == '/' && next == '*' {
			inBlockComment = true
			index++
			continue
		}
		if ch == '/' && next == '/' {
			inLineComment = true
			index++
			continue
		}
		b.WriteByte(ch)
		if ch == '\'' {
			inSingleQuote = true
			escaped = false
			continue
		}
		if ch == '"' {
			inDoubleQuote = true
			escaped = false
		}
	}
	return b.String()
}

func functionFuzzNormalizeCodeLine(line string) string {
	line = strings.ReplaceAll(line, "\t", "    ")
	line = functionFuzzIncludeDirectivePattern.ReplaceAllString(line, " ")
	trimmedLeft := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmedLeft, "#") {
		return ""
	}
	line = functionFuzzWhitespacePattern.ReplaceAllString(line, " ")
	return strings.TrimSpace(line)
}

func functionFuzzBuildCodeOnlyLines(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	stripped := functionFuzzStripCommentsPreserveLines(strings.Join(lines, "\n"))
	codeOnly := strings.Split(stripped, "\n")
	switch {
	case len(codeOnly) < len(lines):
		padding := make([]string, len(lines)-len(codeOnly))
		codeOnly = append(codeOnly, padding...)
	case len(codeOnly) > len(lines):
		codeOnly = append([]string(nil), codeOnly[:len(lines)]...)
	}
	for index := range codeOnly {
		codeOnly[index] = functionFuzzNormalizeCodeLine(codeOnly[index])
	}
	return codeOnly
}

func functionFuzzSanitizeSignature(signature string) string {
	signature = strings.ReplaceAll(signature, "\r\n", "\n")
	signature = strings.TrimSpace(signature)
	if signature == "" {
		return ""
	}
	codeOnlyLines := functionFuzzBuildCodeOnlyLines(strings.Split(signature, "\n"))
	parts := make([]string, 0, len(codeOnlyLines))
	for _, line := range codeOnlyLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts = append(parts, line)
	}
	if len(parts) == 0 {
		return ""
	}
	sanitized := strings.TrimSpace(functionFuzzWhitespacePattern.ReplaceAllString(strings.Join(parts, " "), " "))
	open := strings.Index(sanitized, "(")
	if open > 0 {
		prefix := strings.TrimSpace(sanitized[:open])
		if cut := strings.LastIndexAny(prefix, ";{}"); cut >= 0 && cut+1 < len(prefix) {
			sanitized = strings.TrimSpace(prefix[cut+1:] + " " + sanitized[open:])
		}
	}
	return strings.TrimSpace(sanitized)
}

func functionFuzzSignatureOrRaw(signature string) string {
	sanitized := functionFuzzSanitizeSignature(signature)
	if sanitized != "" {
		return sanitized
	}
	return strings.TrimSpace(signature)
}

func functionFuzzSymbolCorpus(symbol SymbolRecord) string {
	return strings.ToLower(strings.Join([]string{
		strings.TrimSpace(symbol.Name),
		functionFuzzSignatureOrRaw(symbol.Signature),
		strings.TrimSpace(symbol.File),
		strings.TrimSpace(symbol.Kind),
	}, " "))
}

func buildFunctionFuzzClosure(index SemanticIndexV2, root SymbolRecord) functionFuzzClosure {
	symbolByID := map[string]SymbolRecord{}
	for _, symbol := range index.Symbols {
		symbolByID[symbol.ID] = symbol
	}
	adj := map[string][]CallEdge{}
	for _, edge := range index.CallEdges {
		adj[edge.SourceID] = append(adj[edge.SourceID], edge)
	}

	visited := map[string]int{root.ID: 0}
	order := []string{root.ID}
	queue := []string{root.ID}
	callSeen := map[string]struct{}{}
	var calls []CallEdge
	maxDepth := 0
	truncated := false

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		depth := visited[current]
		for _, edge := range adj[current] {
			key := edge.SourceID + "|" + edge.Type + "|" + edge.TargetID
			if _, ok := callSeen[key]; !ok {
				callSeen[key] = struct{}{}
				calls = append(calls, edge)
			}
			if _, ok := visited[edge.TargetID]; ok {
				continue
			}
			if len(visited) >= functionFuzzMaxClosureNodes {
				truncated = true
				continue
			}
			nextDepth := depth + 1
			visited[edge.TargetID] = nextDepth
			if nextDepth > maxDepth {
				maxDepth = nextDepth
			}
			order = append(order, edge.TargetID)
			queue = append(queue, edge.TargetID)
		}
	}

	symbols := make([]SymbolRecord, 0, len(order))
	for _, id := range order {
		symbol, ok := symbolByID[id]
		if !ok {
			symbol = SymbolRecord{
				ID:   id,
				Name: functionFuzzQualifiedLeaf(id),
				Kind: "function",
			}
		}
		symbols = append(symbols, symbol)
	}

	closureSet := map[string]struct{}{}
	fileSet := map[string]struct{}{}
	for _, symbol := range symbols {
		closureSet[symbol.ID] = struct{}{}
		if strings.TrimSpace(symbol.File) != "" {
			fileSet[filepath.ToSlash(strings.TrimSpace(symbol.File))] = struct{}{}
		}
	}
	for _, edge := range calls {
		for _, item := range edge.Evidence {
			trimmed := filepath.ToSlash(strings.TrimSpace(item))
			if trimmed != "" {
				fileSet[trimmed] = struct{}{}
			}
		}
	}

	var overlays []OverlayEdge
	for _, edge := range index.OverlayEdges {
		if _, ok := closureSet[edge.SourceID]; ok {
			overlays = append(overlays, edge)
			continue
		}
		if _, ok := closureSet[edge.TargetID]; ok {
			overlays = append(overlays, edge)
			continue
		}
		for _, item := range edge.Evidence {
			if _, ok := fileSet[filepath.ToSlash(strings.TrimSpace(item))]; ok {
				overlays = append(overlays, edge)
				break
			}
		}
	}

	buildSeen := map[string]struct{}{}
	var builds []BuildContextRecord
	for _, symbol := range symbols {
		if strings.TrimSpace(symbol.BuildContextID) == "" {
			continue
		}
		buildSeen[symbol.BuildContextID] = struct{}{}
	}
	for _, build := range index.BuildContexts {
		if _, ok := buildSeen[build.ID]; ok {
			builds = append(builds, build)
			continue
		}
		for _, file := range build.Files {
			if _, ok := fileSet[filepath.ToSlash(strings.TrimSpace(file))]; ok {
				builds = append(builds, build)
				buildSeen[build.ID] = struct{}{}
				break
			}
		}
	}

	files := make([]string, 0, len(fileSet))
	for file := range fileSet {
		files = append(files, file)
	}
	sort.Strings(files)

	sort.Slice(calls, func(i int, j int) bool {
		left := calls[i].SourceID + "|" + calls[i].Type + "|" + calls[i].TargetID
		right := calls[j].SourceID + "|" + calls[j].Type + "|" + calls[j].TargetID
		return left < right
	})
	sort.Slice(overlays, func(i int, j int) bool {
		left := overlays[i].Domain + "|" + overlays[i].SourceID + "|" + overlays[i].TargetID
		right := overlays[j].Domain + "|" + overlays[j].SourceID + "|" + overlays[j].TargetID
		return left < right
	})
	sort.Slice(builds, func(i int, j int) bool {
		return builds[i].ID < builds[j].ID
	})

	return functionFuzzClosure{
		RootSymbol:   root,
		Symbols:      symbols,
		CallEdges:    calls,
		OverlayEdges: overlays,
		Builds:       builds,
		Files:        files,
		MaxDepth:     maxDepth,
		Truncated:    truncated,
	}
}

func functionFuzzOverlayDomains(edges []OverlayEdge) []string {
	out := make([]string, 0, len(edges))
	for _, edge := range edges {
		if strings.TrimSpace(edge.Domain) != "" {
			out = append(out, strings.TrimSpace(edge.Domain))
		}
	}
	return uniqueStrings(out)
}

func functionFuzzBuildContextNames(items []BuildContextRecord) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = strings.TrimSpace(item.ID)
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return uniqueStrings(out)
}

func functionFuzzReachableSymbolNames(items []SymbolRecord) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		name := functionFuzzDisplayName(item)
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	out = uniqueStrings(out)
	sort.Strings(out)
	return out
}

func buildFunctionFuzzSinkSignals(closure functionFuzzClosure) []FunctionFuzzSinkSignal {
	var out []FunctionFuzzSinkSignal
	for _, edge := range closure.OverlayEdges {
		sourceName := edge.SourceID
		targetName := edge.TargetID
		for _, symbol := range closure.Symbols {
			if symbol.ID == edge.SourceID {
				sourceName = functionFuzzDisplayName(symbol)
			}
			if symbol.ID == edge.TargetID {
				targetName = functionFuzzDisplayName(symbol)
			}
		}
		out = append(out, FunctionFuzzSinkSignal{
			Kind:     "overlay",
			Name:     strings.TrimSpace(edge.Domain),
			SymbolID: edge.SourceID,
			Reason:   functionFuzzNormalizeDisplayText(sourceName + " -> " + targetName + " [" + strings.TrimSpace(edge.Type) + "]"),
		})
	}
	for _, symbol := range closure.Symbols {
		corpus := functionFuzzSymbolCorpus(symbol)
		file := filepath.ToSlash(strings.TrimSpace(symbol.File))
		display := functionFuzzDisplayName(symbol)
		switch {
		case containsAny(corpus, "memcpy", "rtlcopymemory", "copymemory", "strcpy", "wcscpy", "memmove", "copy", "append"):
			out = append(out, FunctionFuzzSinkSignal{Kind: "copy_like", Name: display, SymbolID: symbol.ID, File: file, Reason: "copy or move primitive in reachable closure"})
		case containsAny(corpus, "parse", "decode", "deserialize", "unmarshal", "frombuffer", "from_bytes"):
			out = append(out, FunctionFuzzSinkSignal{Kind: "parse_like", Name: display, SymbolID: symbol.ID, File: file, Reason: "parser or decoder logic in reachable closure"})
		case containsAny(corpus, "validate", "check", "verify", "compare", "cmp", "equal", "auth", "guard"):
			out = append(out, FunctionFuzzSinkSignal{Kind: "compare_like", Name: display, SymbolID: symbol.ID, File: file, Reason: "branch-heavy validation or compare logic in reachable closure"})
		case containsAny(corpus, "alloc", "reserve", "resize", "realloc", "malloc", "new", "free"):
			out = append(out, FunctionFuzzSinkSignal{Kind: "alloc_like", Name: display, SymbolID: symbol.ID, File: file, Reason: "allocation-sensitive path in reachable closure"})
		}
	}
	return normalizeFunctionFuzzSinkSignals(out)
}

func buildFunctionFuzzCodeObservations(root string, target SymbolRecord, params []FunctionFuzzParamStrategy, closure functionFuzzClosure) []FunctionFuzzCodeObservation {
	if len(closure.Symbols) == 0 {
		return nil
	}
	type cachedSource struct {
		lines    []string
		codeOnly []string
	}
	cache := map[string]cachedSource{}
	out := []FunctionFuzzCodeObservation{}
	for _, symbol := range closure.Symbols {
		if !functionFuzzIsCallableSymbol(symbol) && !strings.EqualFold(strings.TrimSpace(symbol.ID), strings.TrimSpace(target.ID)) {
			continue
		}
		filePath := functionFuzzResolveWorkspacePath(root, symbol.File)
		if strings.TrimSpace(filePath) == "" {
			continue
		}
		source, ok := cache[filePath]
		if !ok {
			data, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}
			text := strings.ReplaceAll(string(data), "\r\n", "\n")
			lines := strings.Split(text, "\n")
			if len(lines) == 0 {
				continue
			}
			source = cachedSource{
				lines:    lines,
				codeOnly: functionFuzzBuildCodeOnlyLines(lines),
			}
			cache[filePath] = source
		}
		symbolParams := buildFunctionFuzzParameterStrategies(symbol.Signature)
		tokens := functionFuzzObservationTokens(params, symbolParams)
		out = append(out, functionFuzzExtractCodeObservationsForSymbol(symbol, source.lines, source.codeOnly, tokens)...)
	}
	return normalizeFunctionFuzzCodeObservations(out)
}

func functionFuzzObservationTokens(rootParams []FunctionFuzzParamStrategy, symbolParams []FunctionFuzzParamStrategy) []string {
	out := []string{
		"input", "buffer", "payload", "data", "userbuffer", "systembuffer", "type3inputbuffer",
		"length", "size", "count", "bytes", "offset", "index",
		"ioctl", "request", "control", "code", "type", "mode", "flag",
		"copy", "probe", "dispatch", "validate",
	}
	addParamTokens := func(items []FunctionFuzzParamStrategy) {
		for _, item := range items {
			for _, token := range functionFuzzSplitIdentifierTerms(item.Name) {
				if len(token) >= 3 {
					out = append(out, token)
				}
			}
			switch strings.TrimSpace(item.Class) {
			case "buffer":
				out = append(out, "buffer", "payload", "data", "userbuffer", "systembuffer")
			case "pointer", "opaque":
				out = append(out, "input", "payload", "buffer", "data")
			case "length":
				out = append(out, "length", "size", "count", "bytes", "offset", "index")
			case "enum_or_flags", "scalar_int":
				out = append(out, "ioctl", "code", "control", "type", "mode", "flag")
			case "handle":
				out = append(out, "handle", "file", "device")
			case "string":
				out = append(out, "path", "name", "string", "text")
			}
		}
	}
	addParamTokens(rootParams)
	addParamTokens(symbolParams)
	return uniqueStrings(out)
}

func functionFuzzExtractCodeObservationsForSymbol(symbol SymbolRecord, lines []string, codeOnlyLines []string, tokens []string) []FunctionFuzzCodeObservation {
	if len(lines) == 0 || len(codeOnlyLines) == 0 {
		return nil
	}
	start := symbol.StartLine
	if start <= 0 || start > len(lines) {
		start = 1
	}
	end := symbol.EndLine
	if end <= 0 || end < start {
		end = functionFuzzMin(len(lines), start+180)
	}
	if end-start > 220 {
		end = start + 220
	}
	out := []FunctionFuzzCodeObservation{}
	for lineNo := start; lineNo <= end && lineNo <= len(lines); lineNo++ {
		codeLine := strings.TrimSpace(codeOnlyLines[lineNo-1])
		if codeLine == "" {
			continue
		}
		if functionFuzzLooksLikeSignatureOrParameterLine(codeLine) || functionFuzzLooksLikeBootstrapOrResolverLine(codeLine) || functionFuzzLooksLikeLoggingLine(codeLine) {
			continue
		}
		kinds, focusInputs := functionFuzzObservationKinds(codeLine, tokens)
		if len(kinds) == 0 {
			continue
		}
		accessPaths := functionFuzzObservationAccessPaths(codeLine, focusInputs)
		comparisonFacts := functionFuzzObservationComparisonFacts(codeLine, focusInputs, accessPaths)
		for _, kind := range kinds {
			out = append(out, FunctionFuzzCodeObservation{
				Kind:            kind,
				SymbolID:        strings.TrimSpace(symbol.ID),
				Symbol:          functionFuzzDisplayName(symbol),
				File:            filepath.ToSlash(strings.TrimSpace(symbol.File)),
				Line:            lineNo,
				Evidence:        codeLine,
				AccessPaths:     accessPaths,
				ComparisonFacts: comparisonFacts,
				FocusInputs:     focusInputs,
				WhyItMatters:    functionFuzzObservationWhyItMatters(kind, codeLine),
			})
		}
	}
	return out
}

func functionFuzzObservationKinds(codeLine string, tokens []string) ([]string, []string) {
	lower := strings.ToLower(strings.TrimSpace(codeLine))
	if lower == "" {
		return nil, nil
	}
	focusInputs := functionFuzzObservationFocusInputs(lower, tokens)
	tokenRelevant := len(focusInputs) > 0 || functionFuzzLooksLikeInputConsumptionLine(codeLine)
	kinds := []string{}
	if tokenRelevant && functionFuzzLooksLikeNullGuardLine(lower) {
		kinds = append(kinds, "null_guard")
	}
	if tokenRelevant && functionFuzzLooksLikeSizeGuardLine(lower) {
		kinds = append(kinds, "size_guard")
	}
	if tokenRelevant && functionFuzzLooksLikeDispatchGuardLine(lower) {
		kinds = append(kinds, "dispatch_guard")
	}
	if functionFuzzLooksLikeMemoryTransferLine(lower) {
		kinds = append(kinds, "copy_sink")
	}
	if functionFuzzLooksLikeProbeLine(lower) {
		kinds = append(kinds, "probe_sink")
	}
	if tokenRelevant && functionFuzzLooksLikeAllocatorLine(lower) {
		kinds = append(kinds, "alloc_site")
	}
	if tokenRelevant && functionFuzzLooksLikeCleanupTransitionLine(lower) {
		kinds = append(kinds, "cleanup_path")
	}
	if tokenRelevant && functionFuzzLooksLikeStatePublishLine(lower) {
		kinds = append(kinds, "state_publish")
	}
	return uniqueStrings(kinds), focusInputs
}

func functionFuzzObservationFocusInputs(lowerCodeLine string, tokens []string) []string {
	out := []string{}
	for _, token := range tokens {
		token = strings.ToLower(strings.TrimSpace(token))
		if len(token) < 3 {
			continue
		}
		if strings.Contains(lowerCodeLine, token) {
			out = append(out, token)
		}
	}
	return uniqueStrings(out)
}

func functionFuzzObservationAccessPaths(codeLine string, focusInputs []string) []string {
	codeLine = strings.TrimSpace(codeLine)
	if codeLine == "" {
		return nil
	}
	matches := functionFuzzAccessPathPattern.FindAllString(codeLine, -1)
	out := []string{}
	for _, match := range matches {
		cleaned := strings.ReplaceAll(strings.TrimSpace(match), " ", "")
		lower := strings.ToLower(cleaned)
		if lower == "" {
			continue
		}
		if functionFuzzLooksLikeObservationKeyword(lower) {
			continue
		}
		if !functionFuzzLooksLikeRelevantAccessPath(lower, focusInputs) {
			continue
		}
		out = append(out, cleaned)
	}
	return uniqueStrings(out)
}

func functionFuzzLooksLikeObservationKeyword(lower string) bool {
	switch lower {
	case "if", "else", "switch", "case", "default", "return", "goto", "break", "continue", "sizeof", "null", "nullptr", "true", "false":
		return true
	}
	return false
}

func functionFuzzLooksLikeRelevantAccessPath(lower string, focusInputs []string) bool {
	for _, token := range focusInputs {
		token = strings.ToLower(strings.TrimSpace(token))
		if token != "" && strings.Contains(lower, token) {
			return true
		}
	}
	return functionFuzzLooksLikeSizeAccessPath(lower) ||
		functionFuzzLooksLikeBufferAccessPath(lower) ||
		functionFuzzLooksLikeSelectorAccessPath(lower) ||
		functionFuzzLooksLikePointerAccessPath(lower)
}

func functionFuzzLooksLikeSizeAccessPath(lower string) bool {
	return containsAny(lower, "size", "length", "count", "bytes", "offset", "index")
}

func functionFuzzLooksLikeBufferAccessPath(lower string) bool {
	return containsAny(lower, "buffer", "payload", "data", "input", "output", "dst", "src")
}

func functionFuzzLooksLikeSelectorAccessPath(lower string) bool {
	return containsAny(lower, "ioctl", "control", "selector", "opcode", "mode", "flag", "code", "type")
}

func functionFuzzLooksLikePointerAccessPath(lower string) bool {
	return containsAny(lower, "ptr", "pointer", "handle", "context", "object", "file")
}

func functionFuzzFilterObservationKinds(items []FunctionFuzzCodeObservation, kinds ...string) []FunctionFuzzCodeObservation {
	if len(items) == 0 || len(kinds) == 0 {
		return items
	}
	kindSet := map[string]struct{}{}
	for _, kind := range kinds {
		kind = strings.ToLower(strings.TrimSpace(kind))
		if kind != "" {
			kindSet[kind] = struct{}{}
		}
	}
	if len(kindSet) == 0 {
		return items
	}
	out := make([]FunctionFuzzCodeObservation, 0, len(items))
	for _, item := range items {
		if _, ok := kindSet[strings.ToLower(strings.TrimSpace(item.Kind))]; ok {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		return items
	}
	return out
}

func functionFuzzObservationPathsMatching(items []FunctionFuzzCodeObservation, match func(string) bool, kinds ...string) []string {
	if len(items) == 0 || match == nil {
		return nil
	}
	items = functionFuzzFilterObservationKinds(items, kinds...)
	out := []string{}
	for _, item := range items {
		for _, path := range item.AccessPaths {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			if match(strings.ToLower(path)) {
				out = append(out, path)
			}
		}
	}
	return uniqueStrings(out)
}

func firstNonBlankStrings(candidates ...[]string) []string {
	for _, items := range candidates {
		if len(items) == 0 {
			continue
		}
		items = uniqueStrings(items)
		if len(items) > 0 {
			return items
		}
	}
	return nil
}

func functionFuzzFirstDistinctPath(items []string, avoid string) string {
	avoid = strings.TrimSpace(avoid)
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if avoid != "" && strings.EqualFold(item, avoid) {
			continue
		}
		return item
	}
	return ""
}

func functionFuzzDriftToken(kind string, parts ...string) string {
	fields := []string{strings.TrimSpace(kind)}
	for _, part := range parts {
		fields = append(fields, strings.TrimSpace(part))
	}
	return "drift:" + strings.Join(fields, "|")
}

func functionFuzzParseDriftToken(token string) (string, []string, bool) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "drift:") {
		return "", nil, false
	}
	parts := strings.Split(strings.TrimPrefix(token, "drift:"), "|")
	if len(parts) == 0 {
		return "", nil, false
	}
	kind := strings.TrimSpace(parts[0])
	values := make([]string, 0, len(parts)-1)
	for _, part := range parts[1:] {
		values = append(values, strings.TrimSpace(part))
	}
	return kind, values, kind != ""
}

func functionFuzzComparisonToken(left string, op string, right string) string {
	return "cmp:" + strings.TrimSpace(left) + "|" + strings.TrimSpace(op) + "|" + strings.TrimSpace(right)
}

func functionFuzzParseComparisonToken(token string) (string, string, string, bool) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "cmp:") {
		return "", "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(token, "cmp:"), "|")
	if len(parts) != 3 {
		return "", "", "", false
	}
	left := strings.TrimSpace(parts[0])
	op := strings.TrimSpace(parts[1])
	right := strings.TrimSpace(parts[2])
	if left == "" || op == "" || right == "" {
		return "", "", "", false
	}
	return left, op, right, true
}

func functionFuzzNormalizeComparisonOperand(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") && !strings.HasPrefix(strings.ToLower(text), "sizeof(") {
		text = strings.TrimSpace(text[1 : len(text)-1])
	}
	text = strings.ReplaceAll(text, " ", "")
	return text
}

func functionFuzzOperandLooksConstant(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "0x") || regexp.MustCompile(`^\d+$`).MatchString(lower) {
		return true
	}
	if strings.HasPrefix(lower, "sizeof(") {
		return true
	}
	if lower == "null" || lower == "nullptr" || lower == "invalid_handle_value" {
		return true
	}
	return regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`).MatchString(strings.TrimSpace(text))
}

func functionFuzzOperandMatchesAccessPaths(operand string, accessPaths []string) bool {
	operand = strings.ToLower(functionFuzzNormalizeComparisonOperand(operand))
	if operand == "" {
		return false
	}
	for _, path := range accessPaths {
		path = strings.ToLower(functionFuzzNormalizeComparisonOperand(path))
		if path == "" {
			continue
		}
		if operand == path || strings.Contains(operand, path) || strings.Contains(path, operand) {
			return true
		}
	}
	return false
}

func functionFuzzOperandLooksRelevantForComparison(operand string, focusInputs []string, accessPaths []string) bool {
	lower := strings.ToLower(functionFuzzNormalizeComparisonOperand(operand))
	if lower == "" {
		return false
	}
	if functionFuzzOperandMatchesAccessPaths(operand, accessPaths) {
		return true
	}
	return functionFuzzLooksLikeRelevantAccessPath(lower, focusInputs)
}

func functionFuzzUnaryNullComparisonFacts(codeLine string, focusInputs []string, accessPaths []string) []string {
	if strings.Contains(codeLine, "!=") {
		return nil
	}
	matches := functionFuzzUnaryNegationPathPattern.FindAllStringSubmatch(codeLine, -1)
	if len(matches) == 0 {
		return nil
	}
	out := []string{}
	for _, match := range matches {
		path := functionFuzzNormalizeComparisonOperand(match[1])
		if path == "" {
			continue
		}
		if !functionFuzzOperandLooksRelevantForComparison(path, focusInputs, accessPaths) {
			continue
		}
		out = append(out, functionFuzzComparisonToken(path, "==", "NULL"))
	}
	return uniqueStrings(out)
}

func functionFuzzObservationComparisonFacts(codeLine string, focusInputs []string, accessPaths []string) []string {
	codeLine = strings.TrimSpace(codeLine)
	if codeLine == "" {
		return nil
	}
	out := []string{}
	matches := functionFuzzBinaryComparisonPattern.FindAllStringSubmatch(codeLine, -1)
	for _, match := range matches {
		left := functionFuzzNormalizeComparisonOperand(match[1])
		op := strings.TrimSpace(match[2])
		right := functionFuzzNormalizeComparisonOperand(match[3])
		if left == "" || op == "" || right == "" {
			continue
		}
		leftRelevant := functionFuzzOperandLooksRelevantForComparison(left, focusInputs, accessPaths)
		rightRelevant := functionFuzzOperandLooksRelevantForComparison(right, focusInputs, accessPaths)
		if !leftRelevant && !rightRelevant {
			continue
		}
		if !leftRelevant && !functionFuzzOperandLooksConstant(left) {
			continue
		}
		if !rightRelevant && !functionFuzzOperandLooksConstant(right) {
			continue
		}
		out = append(out, functionFuzzComparisonToken(left, op, right))
	}
	out = append(out, functionFuzzUnaryNullComparisonFacts(codeLine, focusInputs, accessPaths)...)
	return uniqueStrings(out)
}

func functionFuzzObservationBranchFacts(items []FunctionFuzzCodeObservation, kinds ...string) []string {
	if len(items) == 0 {
		return nil
	}
	items = functionFuzzFilterObservationKinds(items, kinds...)
	out := []string{}
	for _, item := range items {
		out = append(out, item.ComparisonFacts...)
	}
	return uniqueStrings(out)
}

func functionFuzzPrimaryBranchGuardObservation(items []FunctionFuzzCodeObservation, preferredKinds ...string) (FunctionFuzzCodeObservation, bool) {
	if len(items) == 0 {
		return FunctionFuzzCodeObservation{}, false
	}
	preference := map[string]int{}
	for index, kind := range preferredKinds {
		preference[strings.TrimSpace(kind)] = len(preferredKinds) - index
	}
	best := FunctionFuzzCodeObservation{}
	bestScore := -1
	found := false
	for _, item := range items {
		if len(item.ComparisonFacts) == 0 {
			continue
		}
		score := preference[strings.TrimSpace(item.Kind)] * 10
		if len(item.ComparisonFacts) > 0 {
			score += 4
		}
		if functionFuzzLooksLikeComparisonLine(item.Evidence) {
			score += 2
		}
		if score > bestScore {
			best = item
			bestScore = score
			found = true
		}
	}
	return best, found
}

func functionFuzzObservationAfterLine(items []FunctionFuzzCodeObservation, line int, kinds ...string) *FunctionFuzzCodeObservation {
	if len(items) == 0 {
		return nil
	}
	kindRank := map[string]int{}
	for index, kind := range kinds {
		kind = strings.TrimSpace(kind)
		if kind != "" {
			kindRank[kind] = len(kinds) - index
		}
	}
	var best *FunctionFuzzCodeObservation
	bestScore := -1 << 30
	for _, item := range items {
		if item.Line <= line {
			continue
		}
		if len(kindRank) > 0 {
			if _, ok := kindRank[strings.TrimSpace(item.Kind)]; !ok {
				continue
			}
		}
		score := 0
		score += kindRank[strings.TrimSpace(item.Kind)] * 100
		score -= item.Line - line
		if score > bestScore {
			copyItem := item
			best = &copyItem
			bestScore = score
		}
	}
	return best
}

func functionFuzzBranchEffectKindFromLine(lower string) string {
	switch {
	case containsAny(lower, "status_invalid", "invalid_parameter", "status_", "error", "fail", "denied", "reject"):
		return "reject"
	case functionFuzzLooksLikeCleanupTransitionLine(lower):
		return "cleanup"
	case functionFuzzLooksLikeMemoryTransferLine(lower):
		return "copy"
	case functionFuzzLooksLikeProbeLine(lower):
		return "probe"
	case functionFuzzLooksLikeStatePublishLine(lower):
		return "publish"
	case functionFuzzLooksLikeAllocatorLine(lower):
		return "alloc"
	case functionFuzzLooksLikeCallLine(lower):
		return "call"
	default:
		return "flow"
	}
}

func functionFuzzBranchFailOutcomeFromSource(root string, symbol SymbolRecord, guardLine int, predicate string) *FunctionFuzzBranchOutcome {
	filePath := functionFuzzResolveWorkspacePath(root, symbol.File)
	if strings.TrimSpace(filePath) == "" || guardLine <= 0 {
		return nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	codeOnly := functionFuzzBuildCodeOnlyLines(lines)
	end := functionFuzzMin(len(lines), guardLine+12)
	for lineNo := guardLine + 1; lineNo <= end; lineNo++ {
		if lineNo-1 >= len(codeOnly) {
			break
		}
		codeLine := strings.TrimSpace(codeOnly[lineNo-1])
		if codeLine == "" {
			continue
		}
		lower := strings.ToLower(codeLine)
		if functionFuzzLooksLikeLoggingLine(codeLine) || functionFuzzLooksLikeBootstrapOrResolverLine(codeLine) {
			continue
		}
		if containsAny(lower, "status_", "invalid", "goto ", "return ", "break;", "continue;", "cleanup", "fail", "error") {
			return &FunctionFuzzBranchOutcome{
				Predicate:  predicate,
				Side:       "true",
				EffectKind: functionFuzzBranchEffectKindFromLine(lower),
				Line:       lineNo,
				Evidence:   codeLine,
			}
		}
	}
	return nil
}

func functionFuzzObservationOutcomeFromObservation(side string, predicate string, item *FunctionFuzzCodeObservation) *FunctionFuzzBranchOutcome {
	if item == nil || strings.TrimSpace(predicate) == "" || item.Line <= 0 {
		return nil
	}
	effectKind := strings.TrimSpace(item.Kind)
	switch effectKind {
	case "copy_sink":
		effectKind = "copy"
	case "probe_sink":
		effectKind = "probe"
	case "cleanup_path":
		effectKind = "cleanup"
	case "state_publish":
		effectKind = "publish"
	case "alloc_site":
		effectKind = "alloc"
	case "dispatch_guard":
		effectKind = "dispatch"
	default:
		effectKind = "flow"
	}
	return &FunctionFuzzBranchOutcome{
		Predicate:  predicate,
		Side:       side,
		EffectKind: effectKind,
		Line:       item.Line,
		Evidence:   item.Evidence,
	}
}

func functionFuzzCallNamesFromCodeLine(codeLine string) []string {
	codeLine = strings.TrimSpace(codeLine)
	if codeLine == "" || !functionFuzzLooksLikeCallLine(codeLine) {
		return nil
	}
	matches := functionFuzzCallNamePattern.FindAllStringSubmatch(codeLine, -1)
	out := []string{}
	for _, match := range matches {
		name := strings.TrimSpace(match[1])
		lower := strings.ToLower(name)
		if name == "" || functionFuzzLooksLikeObservationKeyword(lower) {
			continue
		}
		if containsAny(lower, "sizeof", "nt_success", "__alignof", "alignof") {
			continue
		}
		out = append(out, name)
	}
	return uniqueStrings(out)
}

func functionFuzzObservationCallChainAfterLine(items []FunctionFuzzCodeObservation, line int, kinds ...string) []string {
	if len(items) == 0 {
		return nil
	}
	items = functionFuzzFilterObservationKinds(items, kinds...)
	sort.Slice(items, func(i int, j int) bool {
		if items[i].Line == items[j].Line {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Line < items[j].Line
	})
	out := []string{}
	for _, item := range items {
		if item.Line <= line || item.Line > line+40 {
			continue
		}
		out = append(out, functionFuzzCallNamesFromCodeLine(item.Evidence)...)
		if len(uniqueStrings(out)) >= 4 {
			break
		}
	}
	return uniqueStrings(out)
}

func functionFuzzSourceCallChainFromLine(root string, symbol SymbolRecord, startLine int, maxLines int) []string {
	filePath := functionFuzzResolveWorkspacePath(root, symbol.File)
	if strings.TrimSpace(filePath) == "" || startLine <= 0 || maxLines <= 0 {
		return nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	codeOnly := functionFuzzBuildCodeOnlyLines(lines)
	end := functionFuzzMin(len(codeOnly), startLine+maxLines)
	out := []string{}
	for lineNo := startLine; lineNo <= end; lineNo++ {
		if lineNo-1 >= len(codeOnly) {
			break
		}
		codeLine := strings.TrimSpace(codeOnly[lineNo-1])
		if codeLine == "" {
			continue
		}
		if functionFuzzLooksLikeLoggingLine(codeLine) || functionFuzzLooksLikeBootstrapOrResolverLine(codeLine) || functionFuzzLooksLikeSignatureOrParameterLine(codeLine) {
			continue
		}
		out = append(out, functionFuzzCallNamesFromCodeLine(codeLine)...)
		if len(uniqueStrings(out)) >= 4 {
			break
		}
		if containsAny(strings.ToLower(codeLine), "return ", "goto ", "break;", "continue;") {
			break
		}
	}
	return uniqueStrings(out)
}

func functionFuzzObservationBranchOutcomes(root string, symbol SymbolRecord, items []FunctionFuzzCodeObservation, preferredKinds ...string) []FunctionFuzzBranchOutcome {
	guard, ok := functionFuzzPrimaryBranchGuardObservation(items, preferredKinds...)
	if !ok || len(guard.ComparisonFacts) == 0 {
		return nil
	}
	predicate := strings.TrimSpace(guard.ComparisonFacts[0])
	out := []FunctionFuzzBranchOutcome{}
	switch strings.TrimSpace(guard.Kind) {
	case "size_guard", "null_guard":
		if failOutcome := functionFuzzBranchFailOutcomeFromSource(root, symbol, guard.Line, predicate); failOutcome != nil {
			failOutcome.DownstreamCalls = functionFuzzSourceCallChainFromLine(root, symbol, failOutcome.Line, 8)
			out = append(out, *failOutcome)
		}
		passObservation := functionFuzzObservationAfterLine(items, guard.Line, "copy_sink", "probe_sink", "alloc_site", "state_publish")
		if passOutcome := functionFuzzObservationOutcomeFromObservation("false", predicate, passObservation); passOutcome != nil {
			passOutcome.DownstreamCalls = firstNonBlankStrings(
				functionFuzzObservationCallChainAfterLine(items, guard.Line, "copy_sink", "probe_sink", "alloc_site", "state_publish"),
				functionFuzzSourceCallChainFromLine(root, symbol, passOutcome.Line, 12),
			)
			out = append(out, *passOutcome)
		}
	case "dispatch_guard":
		passObservation := functionFuzzObservationAfterLine(items, guard.Line, "copy_sink", "probe_sink", "state_publish", "cleanup_path")
		if passOutcome := functionFuzzObservationOutcomeFromObservation("true", predicate, passObservation); passOutcome != nil {
			passOutcome.DownstreamCalls = firstNonBlankStrings(
				functionFuzzObservationCallChainAfterLine(items, guard.Line, "copy_sink", "probe_sink", "state_publish", "cleanup_path"),
				functionFuzzSourceCallChainFromLine(root, symbol, passOutcome.Line, 12),
			)
			out = append(out, *passOutcome)
		}
	default:
		passObservation := functionFuzzObservationAfterLine(items, guard.Line, "copy_sink", "probe_sink", "state_publish", "cleanup_path", "alloc_site")
		if passOutcome := functionFuzzObservationOutcomeFromObservation("false", predicate, passObservation); passOutcome != nil {
			passOutcome.DownstreamCalls = firstNonBlankStrings(
				functionFuzzObservationCallChainAfterLine(items, guard.Line, "copy_sink", "probe_sink", "state_publish", "cleanup_path", "alloc_site"),
				functionFuzzSourceCallChainFromLine(root, symbol, passOutcome.Line, 12),
			)
			out = append(out, *passOutcome)
		}
	}
	return normalizeFunctionFuzzBranchOutcomes(out)
}

func functionFuzzObservationInvariantInsights(items []FunctionFuzzCodeObservation, focusedKinds ...string) ([]FunctionFuzzInvariant, []string) {
	if len(items) == 0 {
		return nil, nil
	}
	filtered := functionFuzzFilterObservationKinds(items, focusedKinds...)
	kindSet := map[string]bool{}
	for _, item := range filtered {
		kindSet[strings.TrimSpace(item.Kind)] = true
	}

	sizeGuardPaths := functionFuzzObservationPathsMatching(filtered, functionFuzzLooksLikeSizeAccessPath, "size_guard")
	useSizePaths := functionFuzzObservationPathsMatching(filtered, functionFuzzLooksLikeSizeAccessPath, "copy_sink", "probe_sink", "alloc_site")
	allSizePaths := functionFuzzObservationPathsMatching(filtered, functionFuzzLooksLikeSizeAccessPath)
	bufferPaths := functionFuzzObservationPathsMatching(filtered, functionFuzzLooksLikeBufferAccessPath, "copy_sink", "probe_sink")
	pointerGuardPaths := functionFuzzObservationPathsMatching(filtered, functionFuzzLooksLikePointerAccessPath, "null_guard")
	pointerPaths := functionFuzzObservationPathsMatching(filtered, functionFuzzLooksLikePointerAccessPath, "null_guard", "copy_sink", "probe_sink", "state_publish")
	selectorPaths := functionFuzzObservationPathsMatching(filtered, functionFuzzLooksLikeSelectorAccessPath, "dispatch_guard")
	allocSizePaths := functionFuzzObservationPathsMatching(filtered, functionFuzzLooksLikeSizeAccessPath, "alloc_site")

	invariants := []FunctionFuzzInvariant{}
	driftExamples := []string{}
	appendInvariant := func(item FunctionFuzzInvariant) {
		item.Kind = strings.TrimSpace(item.Kind)
		item.Left = strings.TrimSpace(item.Left)
		item.Right = strings.TrimSpace(item.Right)
		item.Detail = strings.TrimSpace(item.Detail)
		if item.Kind == "" {
			return
		}
		invariants = append(invariants, item)
	}
	appendDrift := func(token string) {
		token = strings.TrimSpace(token)
		if token == "" {
			return
		}
		driftExamples = append(driftExamples, token)
	}

	if kindSet["size_guard"] && (kindSet["copy_sink"] || kindSet["probe_sink"]) {
		guardSize := firstNonBlankString(functionFuzzFirstDistinctPath(sizeGuardPaths, ""), functionFuzzFirstDistinctPath(allSizePaths, ""))
		useSize := firstNonBlankString(functionFuzzFirstDistinctPath(useSizePaths, guardSize), functionFuzzFirstDistinctPath(allSizePaths, guardSize), guardSize)
		if guardSize != "" {
			appendInvariant(FunctionFuzzInvariant{
				Kind:  "guard_use_size_equivalence",
				Left:  guardSize,
				Right: useSize,
			})
			appendDrift(functionFuzzDriftToken("guard_use_size", guardSize, useSize))
		}
		bufferPath := firstNonBlankString(functionFuzzFirstDistinctPath(bufferPaths, ""), functionFuzzFirstDistinctPath(pointerPaths, ""))
		sizePath := firstNonBlankString(useSize, guardSize)
		if bufferPath != "" && sizePath != "" {
			appendInvariant(FunctionFuzzInvariant{
				Kind:  "buffer_size_contract",
				Left:  bufferPath,
				Right: sizePath,
			})
			appendDrift(functionFuzzDriftToken("buffer_size_contract", bufferPath, sizePath))
		}
	}

	if kindSet["null_guard"] && (kindSet["copy_sink"] || kindSet["probe_sink"] || kindSet["state_publish"]) {
		pointerPath := firstNonBlankString(functionFuzzFirstDistinctPath(pointerGuardPaths, ""), functionFuzzFirstDistinctPath(pointerPaths, ""), functionFuzzFirstDistinctPath(bufferPaths, ""))
		sizePath := firstNonBlankString(functionFuzzFirstDistinctPath(useSizePaths, ""), functionFuzzFirstDistinctPath(allSizePaths, ""))
		if pointerPath != "" {
			appendInvariant(FunctionFuzzInvariant{
				Kind:  "pointer_state_coupling",
				Left:  pointerPath,
				Right: sizePath,
			})
			appendDrift(functionFuzzDriftToken("pointer_state", pointerPath, sizePath))
		}
	}

	if kindSet["dispatch_guard"] {
		selectorPath := firstNonBlankString(functionFuzzFirstDistinctPath(selectorPaths, ""), functionFuzzFirstDistinctPath(allSizePaths, ""))
		if selectorPath != "" {
			appendInvariant(FunctionFuzzInvariant{
				Kind: "dispatch_selector_revalidation",
				Left: selectorPath,
			})
			appendDrift(functionFuzzDriftToken("selector_dispatch", selectorPath))
		}
	}

	if kindSet["alloc_site"] && (kindSet["copy_sink"] || kindSet["size_guard"] || kindSet["probe_sink"]) {
		allocSize := firstNonBlankString(functionFuzzFirstDistinctPath(allocSizePaths, ""), functionFuzzFirstDistinctPath(sizeGuardPaths, ""))
		useSize := firstNonBlankString(functionFuzzFirstDistinctPath(useSizePaths, allocSize), functionFuzzFirstDistinctPath(allSizePaths, allocSize), allocSize)
		if allocSize != "" {
			appendInvariant(FunctionFuzzInvariant{
				Kind:  "allocation_use_size_equivalence",
				Left:  allocSize,
				Right: useSize,
			})
			appendDrift(functionFuzzDriftToken("allocation_use", allocSize, useSize))
		}
	}

	if kindSet["state_publish"] && kindSet["cleanup_path"] {
		appendInvariant(FunctionFuzzInvariant{
			Kind: "publish_before_cleanup",
		})
		appendDrift(functionFuzzDriftToken("publish_cleanup"))
	}

	return normalizeFunctionFuzzInvariants(invariants), uniqueStrings(driftExamples)
}

func functionFuzzLooksLikeNullGuardLine(lower string) bool {
	if !functionFuzzLooksLikeControlOrDecisionLine(lower) {
		return false
	}
	return containsAny(lower,
		"if (!", "if(!",
		"== null", "== nullptr", "!= null", "!= nullptr",
		"== invalid_handle_value", "!= invalid_handle_value",
		"== 0)", "==0)", "!= 0)", "!=0)")
}

func functionFuzzLooksLikeSizeGuardLine(lower string) bool {
	if !functionFuzzLooksLikeControlOrDecisionLine(lower) && !strings.Contains(lower, "case ") {
		return false
	}
	if !functionFuzzLooksLikeComparisonLine(lower) && !containsAny(lower, "case ", "sizeof(") {
		return false
	}
	return containsAny(lower, "size", "length", "count", "bytes", "offset", "index", "capacity", "sizeof(")
}

func functionFuzzLooksLikeDispatchGuardLine(lower string) bool {
	return (strings.HasPrefix(lower, "switch") || functionFuzzLooksLikeControlOrDecisionLine(lower)) &&
		containsAny(lower, "ioctl", "control", "request", "code", "type", "mode", "flag", "opcode", "api")
}

func functionFuzzLooksLikeMemoryTransferLine(lower string) bool {
	return containsAny(lower,
		"memcpy", "memmove", "rtlcopymemory", "copymemory",
		"strcpy", "strncpy", "wcscpy", "wcsncpy", "copyfile", "rltmovememory")
}

func functionFuzzLooksLikeProbeLine(lower string) bool {
	return containsAny(lower, "probeforread", "probeforwrite", "mmprobeandlockpages")
}

func functionFuzzLooksLikeAllocatorLine(lower string) bool {
	return containsAny(lower,
		"exallocatepool", "exallocatepool2", "exallocatepoolwithtag",
		"malloc(", "calloc(", "realloc(", "heapalloc(", "virtualalloc(",
		"new ", "localalloc(", "globalalloc(")
}

func functionFuzzLooksLikeCleanupTransitionLine(lower string) bool {
	return containsAny(lower,
		"goto cleanup", "goto end", "goto exit", "goto fail", "goto error",
		"__leave", "cleanup:", "fail:", "error:", "closehandle(", "zwclose(", "exfreepool", "free(")
}

func functionFuzzLooksLikeStatePublishLine(lower string) bool {
	return containsAny(lower,
		"iocreatedevice", "iocreatesymboliclink", "register", "obregistercallbacks",
		"pssetcreateprocessnotifyroutine", "createfile", "zwcreatefile", "opendevice", "openprocess")
}

func functionFuzzObservationWhyItMatters(kind string, codeLine string) string {
	switch strings.TrimSpace(kind) {
	case "null_guard":
		return "The function body contains an explicit pointer or handle validity gate that an attacker can try to bypass, desynchronize, or hit in the wrong order."
	case "size_guard":
		return "The code compares a size-like value in a branch, which is where boundary, truncation, and wraparound inputs usually try to break assumptions."
	case "dispatch_guard":
		return "The code branches on a control-like selector, so unsupported or colliding values can push execution into rarely tested paths."
	case "copy_sink":
		return "The function body performs a memory transfer, which makes buffer ownership, overlap, and length mismatches directly security-relevant."
	case "probe_sink":
		return "The function probes user-controlled memory, which usually means the buffer contract matters more than the raw type name."
	case "alloc_site":
		return "The function allocates memory from a computed size, so attackers will try to make allocation size and later use size disagree."
	case "cleanup_path":
		return "The function contains an explicit failure-unwind edge, which is where partial side effects and rollback gaps usually show up."
	case "state_publish":
		return "The function appears to publish or register state before the whole flow is known to succeed."
	default:
		return "The function body contains a source-level condition or sink that can be driven by crafted attacker input."
	}
}

func buildFunctionFuzzParameterStrategies(signature string) []FunctionFuzzParamStrategy {
	signature = functionFuzzSanitizeSignature(signature)
	if signature == "" {
		return []FunctionFuzzParamStrategy{{
			Index:    0,
			Name:     "input",
			RawType:  "opaque",
			Class:    "opaque",
			Mutators: []string{"raw_bytes", "size_partitioning", "compare_guided_bytes"},
			Notes:    []string{"Signature was not available in structural_index_v2; fallback planning assumes raw byte partitioning."},
		}}
	}
	rawParams := functionFuzzSplitSignatureParams(signature)
	if len(rawParams) == 0 {
		return nil
	}
	out := make([]FunctionFuzzParamStrategy, 0, len(rawParams))
	for index, rawParam := range rawParams {
		name, rawType := functionFuzzParseParamNameAndType(rawParam, index)
		class := functionFuzzClassifyParam(rawType, name)
		item := FunctionFuzzParamStrategy{
			Index:    index,
			Name:     name,
			RawType:  rawType,
			Class:    class,
			Mutators: functionFuzzMutatorsForClass(class),
		}
		if class == "object" || class == "container" || class == "handle" || class == "opaque" {
			item.Notes = append(item.Notes, "Target-specific setup or factory inference is still required for this parameter class.")
		}
		out = append(out, item)
	}
	functionFuzzInferRelations(out)
	return out
}

func functionFuzzSplitSignatureParams(signature string) []string {
	open := strings.Index(signature, "(")
	close := strings.LastIndex(signature, ")")
	if open < 0 || close <= open {
		return nil
	}
	body := strings.TrimSpace(signature[open+1 : close])
	if body == "" || strings.EqualFold(body, "void") {
		return nil
	}
	return functionFuzzSplitTopLevel(body, ',')
}

func functionFuzzSplitTopLevel(input string, delimiter rune) []string {
	var out []string
	var b strings.Builder
	parenDepth := 0
	angleDepth := 0
	bracketDepth := 0
	braceDepth := 0
	for _, r := range input {
		switch r {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '<':
			angleDepth++
		case '>':
			if angleDepth > 0 {
				angleDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		}
		if r == delimiter && parenDepth == 0 && angleDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
			item := strings.TrimSpace(b.String())
			if item != "" {
				out = append(out, item)
			}
			b.Reset()
			continue
		}
		b.WriteRune(r)
	}
	item := strings.TrimSpace(b.String())
	if item != "" {
		out = append(out, item)
	}
	return out
}

func functionFuzzParseParamNameAndType(raw string, index int) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Sprintf("arg%d", index), "opaque"
	}
	parts := functionFuzzSplitTopLevel(raw, '=')
	if len(parts) > 0 {
		raw = strings.TrimSpace(parts[0])
	}
	match := functionFuzzIdentPattern.FindStringIndex(raw)
	if match == nil {
		return fmt.Sprintf("arg%d", index), strings.TrimSpace(raw)
	}
	name := strings.TrimSpace(raw[match[0]:match[1]])
	typePart := strings.TrimSpace(strings.TrimSpace(raw[:match[0]]) + strings.TrimSpace(raw[match[1]:]))
	typePart = strings.TrimSpace(strings.TrimSuffix(typePart, "[]"))
	if typePart == "" {
		typePart = strings.TrimSpace(raw)
	}
	if functionFuzzLooksLikeTypeOnly(name) {
		return fmt.Sprintf("arg%d", index), strings.TrimSpace(raw)
	}
	return name, typePart
}

func functionFuzzLooksLikeTypeOnly(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch lower {
	case "const", "volatile", "unsigned", "signed", "struct", "class":
		return true
	}
	return false
}

func functionFuzzClassifyParam(rawType string, name string) string {
	lowerType := strings.ToLower(strings.TrimSpace(rawType))
	lowerName := strings.ToLower(strings.TrimSpace(name))
	switch {
	case lowerType == "" || lowerType == "opaque":
		return "opaque"
	case strings.Contains(lowerType, "bool"):
		return "boolean"
	case containsAny(lowerType, "float", "double"):
		return "scalar_float"
	case functionFuzzIsHandleType(lowerType, lowerName):
		return "handle"
	case functionFuzzIsStringType(lowerType):
		return "string"
	case functionFuzzIsContainerType(lowerType):
		return "container"
	case functionFuzzIsLengthLike(lowerType, lowerName):
		return "length"
	case functionFuzzIsOpaquePointerType(lowerType, lowerName):
		return "opaque"
	case strings.Contains(lowerType, "*") || strings.Contains(lowerType, "[") || strings.Contains(lowerName, "buffer") || strings.Contains(lowerName, "data") || strings.Contains(lowerName, "payload"):
		if functionFuzzIsBufferType(lowerType, lowerName) {
			return "buffer"
		}
		return "pointer"
	case functionFuzzIsEnumLike(lowerType, lowerName):
		return "enum_or_flags"
	case functionFuzzIsBuiltinScalar(lowerType):
		return "scalar_int"
	default:
		return "object"
	}
}

func functionFuzzIsHandleType(lowerType string, lowerName string) bool {
	return containsAny(lowerType, "handle", "hmodule", "hwnd", "hkey", "sc_handle", "psid", "token") || containsAny(lowerName, "handle", "token")
}

func functionFuzzIsStringType(lowerType string) bool {
	return containsAny(lowerType, "string", "wstring", "string_view", "fstring", "fname", "path")
}

func functionFuzzIsContainerType(lowerType string) bool {
	return containsAny(lowerType, "vector<", "span<", "array<", "tarray<", "tmap<", "map<", "set<", "slice")
}

func functionFuzzIsLengthLike(lowerType string, lowerName string) bool {
	if !functionFuzzIsBuiltinScalar(lowerType) {
		return false
	}
	return containsAny(lowerName, "len", "length", "size", "count", "bytes", "cb", "capacity")
}

func functionFuzzIsOpaquePointerType(lowerType string, lowerName string) bool {
	if !containsAny(lowerType, "lpvoid", "pvoid", "void *", "void*", "const void *", "const void*") {
		return false
	}
	if containsAny(lowerName, "buf", "buffer", "data", "payload", "input", "output", "src", "dst", "text", "string", "path", "name") {
		return false
	}
	return true
}

func functionFuzzIsBufferType(lowerType string, lowerName string) bool {
	if containsAny(lowerName, "buf", "buffer", "data", "payload", "input", "output", "src", "dst", "text") {
		return true
	}
	return containsAny(lowerType, "char*", "wchar_t*", "uint8_t*", "byte*", "std::byte*", "void*", "unsigned char*", "int8_t*", "uchar*", "tchar*")
}

func functionFuzzIsEnumLike(lowerType string, lowerName string) bool {
	return strings.Contains(lowerType, "enum") || containsAny(lowerName, "flags", "mode", "kind", "type")
}

func functionFuzzIsBuiltinScalar(lowerType string) bool {
	return containsAny(lowerType,
		"int", "uint", "long", "short", "char", "byte", "word", "dword", "qword", "size_t",
		"ssize_t", "uintptr_t", "intptr_t", "ulong", "ushort", "u32", "u64", "u16", "u8",
		"int32_t", "uint32_t", "int64_t", "uint64_t", "int16_t", "uint16_t", "int8_t", "uint8_t", "ntstatus")
}

func functionFuzzMutatorsForClass(class string) []string {
	switch class {
	case "boolean":
		return []string{"false_true_flip", "persistent_toggle"}
	case "scalar_float":
		return []string{"zero", "nan", "inf", "small_subnormal"}
	case "scalar_int":
		return []string{"zero", "one", "max_small", "sign_flip", "magic_constants"}
	case "length":
		return []string{"zero", "one", "off_by_one", "near_partner_size", "cap_bypass"}
	case "buffer":
		return []string{"empty", "short", "oversized", "repeated_bytes", "cmp_guided_bytes"}
	case "pointer":
		return []string{"null", "short_backing_store", "misaligned_storage"}
	case "string":
		return []string{"empty", "ascii", "utf8_bytes", "path_like", "format_tokens"}
	case "enum_or_flags":
		return []string{"zero", "single_bit", "dense_bits", "invalid_mask"}
	case "handle":
		return []string{"invalid_handle", "pseudo_handle", "stale_handle"}
	case "container":
		return []string{"empty_container", "single_item", "nested_mutation"}
	case "object":
		return []string{"default_builder", "constructor_seed_replay", "factory_inference"}
	default:
		return []string{"raw_bytes", "size_partitioning"}
	}
}

func functionFuzzInferRelations(items []FunctionFuzzParamStrategy) {
	if len(items) == 0 {
		return
	}
	lengthIndexes := []int{}
	bufferIndexes := []int{}
	for index, item := range items {
		switch item.Class {
		case "length":
			lengthIndexes = append(lengthIndexes, index)
		case "buffer", "pointer", "string", "container":
			bufferIndexes = append(bufferIndexes, index)
		}
	}
	for _, bufIndex := range bufferIndexes {
		bestIndex := -1
		bestDistance := 1 << 30
		bufRoot := functionFuzzNameRoot(items[bufIndex].Name)
		for _, lenIndex := range lengthIndexes {
			lenRoot := functionFuzzNameRoot(items[lenIndex].Name)
			distance := functionFuzzAbs(bufIndex - lenIndex)
			if bufRoot != "" && lenRoot != "" && bufRoot == lenRoot {
				distance -= 4
			}
			if distance < bestDistance {
				bestDistance = distance
				bestIndex = lenIndex
			}
		}
		if bestIndex < 0 {
			continue
		}
		if items[bufIndex].Relation == "" {
			items[bufIndex].Relation = "sized_by:" + items[bestIndex].Name
		}
		if items[bestIndex].Relation == "" {
			items[bestIndex].Relation = "sizes:" + items[bufIndex].Name
		}
	}
}

func functionFuzzNameRoot(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	replacements := []string{"length", "len", "size", "count", "bytes", "buffer", "buf", "data", "payload", "ptr", "_", "-"}
	for _, item := range replacements {
		lower = strings.ReplaceAll(lower, item, "")
	}
	return strings.TrimSpace(lower)
}

func functionFuzzQueryMode(overlays []string, sinks []FunctionFuzzSinkSignal) string {
	for _, overlay := range overlays {
		if containsAny(strings.ToLower(overlay), "security", "boundary", "ioctl", "rpc", "handle", "memory") {
			return "security"
		}
	}
	for _, sink := range sinks {
		if sink.Kind == "overlay" || sink.Kind == "copy_like" || sink.Kind == "compare_like" {
			return "security"
		}
	}
	return "trace"
}

func functionFuzzEnginePlan(cfg Config, target SymbolRecord, overlays []string, params []FunctionFuzzParamStrategy, sinks []FunctionFuzzSinkSignal) (string, []string, []string) {
	primary := "libFuzzer + ASan/UBSan"
	secondary := []string{"SanitizerCoverage trace-cmp"}
	notes := []string{}

	complexCount := 0
	for _, item := range params {
		switch item.Class {
		case "object", "container", "handle":
			complexCount++
		}
	}
	if complexCount > 0 {
		primary = "FuzzTest domain model + libFuzzer ABI"
		secondary = append(secondary, "AFL++ CMPLOG")
		notes = append(notes, functionFuzzLocalizedText(cfg, "Detected object or container parameters; structure-aware generation is recommended over raw byte-only mutation.", "객체나 컨테이너형 파라미터가 감지되어 단순 바이트 변이보다 구조 인지형 생성 전략이 더 적합합니다."))
	}
	for _, overlay := range overlays {
		lower := strings.ToLower(overlay)
		if containsAny(lower, "ioctl", "handle", "rpc") {
			secondary = append(secondary, "WinAFL follow-up")
		}
		if containsAny(lower, "ioctl", "driver") {
			notes = append(notes, functionFuzzLocalizedText(cfg, "For live driver boundaries, follow up with HLK DF random IOCTL fuzzing and Driver Verifier after source-level harnessing.", "실제 드라이버 경계는 소스 기반 분석 뒤에 HLK DF random IOCTL fuzzing과 Driver Verifier로 후속 검증하는 편이 좋습니다."))
		}
	}
	for _, sink := range sinks {
		if sink.Kind == "compare_like" {
			secondary = append(secondary, "AFL++ Redqueen style compare solving")
			break
		}
	}
	if strings.Contains(strings.ToLower(target.File), "driver") || strings.Contains(strings.ToLower(target.File), "ioctl") {
		secondary = append(secondary, "wtf snapshot fuzzing")
	}
	return primary, uniqueStrings(secondary), uniqueStrings(notes)
}

func functionFuzzRiskScore(target SymbolRecord, overlays []string, params []FunctionFuzzParamStrategy, sinks []FunctionFuzzSinkSignal, observations []FunctionFuzzCodeObservation, closure functionFuzzClosure) int {
	score := 20
	for _, overlay := range overlays {
		lower := strings.ToLower(overlay)
		switch {
		case containsAny(lower, "security", "boundary"):
			score += 18
		case containsAny(lower, "ioctl", "rpc", "handle", "memory"):
			score += 22
		default:
			score += 8
		}
	}
	for _, item := range params {
		switch item.Class {
		case "buffer", "pointer", "length":
			score += 8
		case "object", "container", "handle":
			score += 10
		case "string":
			score += 5
		}
	}
	score += functionFuzzMin(len(sinks)*4, 20)
	score += functionFuzzMin(len(observations)*2, 20)
	for _, item := range observations {
		switch strings.TrimSpace(item.Kind) {
		case "copy_sink", "probe_sink", "dispatch_guard":
			score += 3
		case "size_guard", "alloc_site", "cleanup_path", "state_publish":
			score += 2
		}
	}
	score += functionFuzzMin(len(closure.CallEdges), 12)
	if containsAny(strings.ToLower(target.File), "ioctl", "rpc", "dispatch", "packet", "serialize", "deserialize", "memory") {
		score += 10
	}
	if score > 100 {
		score = 100
	}
	return score
}

func functionFuzzHarnessReady(target SymbolRecord, params []FunctionFuzzParamStrategy) bool {
	signature := functionFuzzSanitizeSignature(target.Signature)
	if signature == "" {
		return false
	}
	if strings.Contains(signature, "::") || strings.Contains(strings.ToLower(signature), "operator") {
		return false
	}
	for _, item := range params {
		switch item.Class {
		case "object", "container", "handle", "opaque":
			return false
		}
	}
	return true
}

func buildFunctionFuzzGuidance(cfg Config, target SymbolRecord, closure functionFuzzClosure, run FunctionFuzzRun) ([]string, []string, []string) {
	interpretation := []string{
		functionFuzzLocalizedText(cfg, "This is an AI source-only fuzz analysis result, not a runtime-confirmed vulnerability finding.", "이 결과는 AI 기반 소스 전용 fuzz 분석 결과이며, 런타임 재현으로 확정된 취약점 판정은 아닙니다."),
		functionFuzzLocalizedText(cfg, fmt.Sprintf("Kernforge selected %s as the root and mapped %d reachable call edge(s) at depth %d.", valueOrUnset(run.TargetSymbolName), run.ReachableCallCount, run.ReachableDepth), fmt.Sprintf("Kernforge는 %s를 루트로 잡고, 깊이 %d까지 도달 가능한 호출 간선 %d개를 매핑했습니다.", valueOrUnset(run.TargetSymbolName), run.ReachableDepth, run.ReachableCallCount)),
	}
	nextSteps := []string{}
	if strings.EqualFold(strings.TrimSpace(run.ScopeMode), "file") && strings.TrimSpace(run.ScopeRootFile) != "" {
		interpretation = append(interpretation, functionFuzzLocalizedText(cfg, fmt.Sprintf("This run started from file scope %s, expanded include/import relationships across %d file(s), and auto-selected %s as the best representative function root.", filepath.ToSlash(strings.TrimSpace(run.ScopeRootFile)), len(run.ScopeFiles), valueOrUnset(run.TargetSymbolName)), fmt.Sprintf("이번 실행은 파일 범위 %s에서 시작해 include/import 관계로 %d개 파일까지 확장한 뒤, %s를 가장 적합한 대표 함수 루트로 자동 선택했습니다.", filepath.ToSlash(strings.TrimSpace(run.ScopeRootFile)), len(run.ScopeFiles), valueOrUnset(run.TargetSymbolName))))
	}

	if !run.HarnessReady {
		interpretation = append(interpretation, functionFuzzLocalizedText(cfg, "The current target is not ready for native autonomous fuzz execution because it still needs environment-specific object setup, handle provisioning, or custom parameter binding.", "현재 타깃은 환경별 객체 준비, 핸들 준비, 커스텀 파라미터 바인딩이 필요해서 네이티브 자동 fuzz 실행 준비가 아직 되지 않았습니다."))
		interpretation = append(interpretation, functionFuzzLocalizedText(cfg, "Kernforge still completed source-only virtual parameter synthesis below, so you can inspect likely failure modes without building fixtures or a runnable harness.", "그래도 아래의 소스 기반 가상 파라미터 합성은 완료되었으므로, 별도 fixture나 실행 harness 없이도 가능성 높은 실패 모드를 검토할 수 있습니다."))
		nextSteps = append(nextSteps, functionFuzzLocalizedText(cfg, "Prefer a deeper input-facing function whose parameters are raw buffers, lengths, enums, or strings so Kernforge can automatically synthesize richer virtual parameter combinations and path exploration.", "raw buffer, 길이, enum, 문자열처럼 입력을 직접 받는 더 안쪽 함수로 내려가면 Kernforge가 더 정밀한 가상 파라미터 조합과 경로 탐색을 자동 합성할 수 있습니다."))
	}
	if functionFuzzLooksLikeEntryRoot(target) {
		interpretation = append(interpretation, functionFuzzLocalizedText(cfg, "This target looks like a process, module, or driver entry point. Entry points are usually better traversal roots than first autonomous fuzz targets because they pull in broad initialization state.", "이 타깃은 프로세스, 모듈, 드라이버의 진입점에 가깝습니다. 이런 진입점은 자동 fuzz 첫 타깃보다는 내부 입력 처리 함수로 내려가기 위한 traversal root로 더 적합한 경우가 많습니다."))
		nextSteps = append(nextSteps, functionFuzzLocalizedText(cfg, "Use this plan to find a parser, validator, IOCTL dispatch helper, request handler, or copy routine inside the reachable closure and let Kernforge analyze that function as the next root.", "이 계획을 바탕으로 reachable closure 안의 parser, validator, IOCTL dispatch helper, request handler, copy routine 같은 함수를 찾아 다음 루트로 다시 분석하는 편이 좋습니다."))
	}
	switch strings.ToLower(strings.TrimSpace(run.Execution.Status)) {
	case "blocked":
		interpretation = append(interpretation, functionFuzzLocalizedText(cfg, "Native build-and-run did not start. That only affects executable fuzzing, not the source-only virtual analysis results below.", "네이티브 빌드/실행은 시작되지 않았습니다. 다만 이는 실행형 fuzzing에만 영향을 주며, 아래의 소스 전용 가상 분석 결과 자체는 유효합니다."))
	case "pending_confirmation":
		interpretation = append(interpretation, functionFuzzLocalizedText(cfg, "Automatic build-and-run is waiting for confirmation because the recovered build settings are heuristic or incomplete.", "복구된 빌드 설정이 휴리스틱이거나 불완전해서 자동 빌드/실행은 확인 대기 상태입니다."))
	case "planned":
		interpretation = append(interpretation, functionFuzzLocalizedText(cfg, "The current target is suitable for autonomous build-and-run with the recovered compile context.", "현재 타깃은 복구된 컴파일 컨텍스트만으로도 자동 빌드/실행 후보로 적합합니다."))
	case "running":
		interpretation = append(interpretation, functionFuzzLocalizedText(cfg, "Autonomous build-and-run has started; monitor the background job and crash artifacts for concrete findings.", "자동 빌드/실행이 시작되었습니다. 실제 확인 가능한 이슈는 백그라운드 job과 crash artifact를 중심으로 보면 됩니다."))
	case "completed":
		interpretation = append(interpretation, functionFuzzLocalizedText(cfg, "Autonomous build-and-run finished. Review crash artifacts and sanitizer output for concrete findings.", "자동 빌드/실행이 끝났습니다. 구체적인 이슈는 crash artifact와 sanitizer 출력을 확인하면 됩니다."))
	}
	if run.RiskScore >= 85 {
		interpretation = append(interpretation, functionFuzzLocalizedText(cfg, "The risk score is high because the reachable closure crosses security-sensitive surfaces such as memory, handles, IOCTL, RPC, or trust boundaries.", "risk score가 높은 이유는 reachable closure가 memory, handle, IOCTL, RPC, trust boundary 같은 보안 민감 표면을 지나기 때문입니다."))
	}
	if len(run.OverlayDomains) > 0 {
		nextSteps = append(nextSteps, functionFuzzLocalizedText(cfg, "Use the overlay domains below to focus triage on the most exposed boundary first.", "아래 overlay domain을 기준으로 가장 노출도가 높은 경계부터 triage를 시작하면 됩니다."))
	}
	if strings.TrimSpace(run.Execution.ContinueCommand) != "" {
		nextSteps = append(nextSteps, functionFuzzLocalizedText(cfg, "If the recovered build settings look correct, continue with "+run.Execution.ContinueCommand+".", "복구된 빌드 설정이 맞아 보이면 "+run.Execution.ContinueCommand+"로 이어서 진행할 수 있습니다."))
	}
	if len(run.Execution.MissingSettings) > 0 {
		nextSteps = append(nextSteps, functionFuzzLocalizedText(cfg, "If you want more reliable autonomous execution, provide the missing build settings listed below or generate compile_commands.json for this target.", "자동 실행 신뢰도를 높이려면 아래 누락된 빌드 설정을 채우거나 이 타깃의 compile_commands.json을 준비하면 됩니다."))
	}
	if !run.HarnessReady {
		nextSteps = append(nextSteps, functionFuzzLocalizedText(cfg, "You do not need to edit the generated harness for source-only analysis. Native harness customization is only an optional follow-up if you later want executable fuzzing.", "소스 전용 분석만 볼 때는 생성된 harness를 직접 수정할 필요가 없습니다. harness 커스터마이징은 나중에 실행형 fuzzing까지 확장하고 싶을 때의 선택적 후속 단계입니다."))
	}
	return uniqueStrings(interpretation), uniqueStrings(nextSteps), functionFuzzSuggestedTargets(cfg, target, closure)
}

func functionFuzzRefreshGuidance(cfg Config, run *FunctionFuzzRun) {
	if run == nil {
		return
	}
	target := SymbolRecord{
		Name:      run.TargetSymbolName,
		File:      run.TargetFile,
		Signature: run.TargetSignature,
	}
	suggested := append([]string(nil), run.SuggestedTargets...)
	suggestedCommands := append([]string(nil), run.SuggestedCommands...)
	run.Interpretation, run.NextSteps, _ = buildFunctionFuzzGuidance(cfg, target, functionFuzzClosure{}, *run)
	run.SuggestedTargets = uniqueStrings(suggested)
	run.SuggestedCommands = uniqueStrings(suggestedCommands)
}

func functionFuzzLooksLikeEntryRoot(target SymbolRecord) bool {
	lowerName := strings.ToLower(strings.TrimSpace(target.Name))
	lowerDisplay := strings.ToLower(strings.TrimSpace(functionFuzzDisplayName(target)))
	lowerFile := strings.ToLower(strings.TrimSpace(target.File))
	return containsAny(lowerName, "driverentry", "dllmain", "winmain", "main", "moduleentry", "initialize") ||
		containsAny(lowerDisplay, "driverentry", "dllmain", "winmain", "moduleentry") ||
		containsAny(lowerFile, "driver", "entry", "startup", "bootstrap")
}

func functionFuzzSuggestedTargets(cfg Config, target SymbolRecord, closure functionFuzzClosure) []string {
	type candidate struct {
		text  string
		score int
	}
	signalWeights := map[string]int{}
	for _, signal := range buildFunctionFuzzSinkSignals(closure) {
		weight := 0
		switch signal.Kind {
		case "copy_like", "compare_like", "parse_like":
			weight = 16
		case "overlay":
			weight = 20
		case "alloc_like":
			weight = 8
		}
		if weight == 0 {
			continue
		}
		signalWeights[strings.TrimSpace(signal.SymbolID)] += weight
	}

	items := []candidate{}
	for _, symbol := range closure.Symbols {
		if strings.EqualFold(strings.TrimSpace(symbol.ID), strings.TrimSpace(target.ID)) {
			continue
		}
		if !functionFuzzIsCallableSymbol(symbol) {
			continue
		}
		params := buildFunctionFuzzParameterStrategies(symbol.Signature)
		score := signalWeights[strings.TrimSpace(symbol.ID)]
		if functionFuzzHarnessReady(symbol, params) {
			score += 40
		}
		if functionFuzzSymbolLooksInputFacing(symbol, params) {
			score += 28
		}
		if functionFuzzHasDirectInputParams(params) {
			score += 12
		}
		score += functionFuzzSuggestedTargetPathScore(target.File, symbol.File)
		score += functionFuzzSuggestedTargetSignalBonus(symbol, params)
		if functionFuzzLooksLikeEntryRoot(symbol) {
			score -= 24
		}
		score -= functionFuzzSuggestedTargetPenalty(symbol, params)
		if score <= 0 {
			continue
		}
		text := functionFuzzSuggestedTargetLabel(cfg, target, symbol, params)
		if strings.TrimSpace(text) == "" {
			continue
		}
		items = append(items, candidate{text: text, score: score})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].score == items[j].score {
			return items[i].text < items[j].text
		}
		return items[i].score > items[j].score
	})

	out := []string{}
	seen := map[string]struct{}{}
	for _, item := range items {
		if _, ok := seen[item.text]; ok {
			continue
		}
		seen[item.text] = struct{}{}
		out = append(out, item.text)
		if len(out) >= functionFuzzMaxSuggestedItems {
			break
		}
	}
	return out
}

func functionFuzzSuggestedCommands(target SymbolRecord, closure functionFuzzClosure) []string {
	type candidate struct {
		command string
		score   int
	}
	items := []candidate{}
	for _, symbol := range closure.Symbols {
		if strings.EqualFold(strings.TrimSpace(symbol.ID), strings.TrimSpace(target.ID)) {
			continue
		}
		if !functionFuzzIsCallableSymbol(symbol) {
			continue
		}
		params := buildFunctionFuzzParameterStrategies(symbol.Signature)
		score := 0
		if functionFuzzHarnessReady(symbol, params) {
			score += 40
		}
		if functionFuzzSymbolLooksInputFacing(symbol, params) {
			score += 28
		}
		if functionFuzzHasDirectInputParams(params) {
			score += 12
		}
		score += functionFuzzSuggestedTargetPathScore(target.File, symbol.File)
		score += functionFuzzSuggestedTargetSignalBonus(symbol, params)
		if functionFuzzLooksLikeEntryRoot(symbol) {
			score -= 24
		}
		score -= functionFuzzSuggestedTargetPenalty(symbol, params)
		if score <= 0 {
			continue
		}
		command := functionFuzzSuggestedCommandForSymbol(symbol)
		if strings.TrimSpace(command) == "" {
			continue
		}
		items = append(items, candidate{command: command, score: score})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].score == items[j].score {
			return items[i].command < items[j].command
		}
		return items[i].score > items[j].score
	})
	out := []string{}
	for _, item := range items {
		out = append(out, item.command)
		if len(out) >= 3 {
			break
		}
	}
	return uniqueStrings(out)
}

func functionFuzzSuggestedCommandForSymbol(symbol SymbolRecord) string {
	name := strings.TrimSpace(symbol.Name)
	file := filepath.ToSlash(strings.TrimSpace(symbol.File))
	if name == "" {
		name = strings.TrimSpace(functionFuzzDisplayName(symbol))
	}
	if name == "" {
		return ""
	}
	if file != "" {
		return fmt.Sprintf(`/fuzz-func %s --file "%s"`, name, file)
	}
	return fmt.Sprintf("/fuzz-func %s", name)
}

func functionFuzzSuggestedTargetPathScore(targetFile string, candidateFile string) int {
	target := strings.ToLower(filepath.ToSlash(filepath.Clean(strings.TrimSpace(targetFile))))
	candidate := strings.ToLower(filepath.ToSlash(filepath.Clean(strings.TrimSpace(candidateFile))))
	if target == "" || candidate == "" {
		return 0
	}
	if target == candidate {
		return 48
	}
	targetDir := pathDir(target)
	candidateDir := pathDir(candidate)
	if targetDir != "" && targetDir == candidateDir {
		return 34
	}
	targetParts := strings.Split(strings.Trim(targetDir, "/"), "/")
	candidateParts := strings.Split(strings.Trim(candidateDir, "/"), "/")
	shared := 0
	for shared < len(targetParts) && shared < len(candidateParts) {
		if targetParts[shared] != candidateParts[shared] {
			break
		}
		shared++
	}
	return shared * 8
}

func pathDir(value string) string {
	value = filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
	if value == "." || value == "" {
		return ""
	}
	if idx := strings.LastIndex(value, "/"); idx >= 0 {
		return value[:idx]
	}
	return ""
}

func functionFuzzSuggestedTargetSignalBonus(symbol SymbolRecord, params []FunctionFuzzParamStrategy) int {
	score := 0
	corpus := functionFuzzSymbolCorpus(symbol)
	if containsAny(corpus, "validate", "check", "verify", "guard") {
		score += 18
	}
	if containsAny(corpus, "ioctl", "dispatch", "request", "handler", "packet", "payload") {
		score += 20
	}
	if containsAny(corpus, "parse", "decode", "deserialize", "registry", "copy", "memcpy", "read", "write") {
		score += 14
	}
	if functionFuzzHasLengthBufferRelation(params) {
		score += 12
	}
	return score
}

func functionFuzzSuggestedTargetPenalty(symbol SymbolRecord, params []FunctionFuzzParamStrategy) int {
	score := 0
	corpus := functionFuzzSymbolCorpus(symbol)
	display := strings.ToLower(strings.TrimSpace(functionFuzzDisplayName(symbol)))
	switch {
	case containsAny(corpus, "/external/", "/third_party/", "/thirdparty/", "/vendor/", "/deps/", "/generated/"):
		score += 90
	case containsAny(corpus, "/include/aws/", "/tinyxml", "/rapidjson/", "/spdlog/"):
		score += 80
	}
	if containsAny(corpus, ".h", ".hpp", ".inl") {
		score += 10
	}
	if containsAny(corpus, "template<", "__forceinline", "constexpr", "inline") {
		score += 18
	}
	if containsAny(corpus, "obfuscation", "decrypt", "encrypt", "xor", "rotate") {
		score += 28
	}
	if functionFuzzLooksTrivialLeafName(display) {
		score += 22
	}
	if !functionFuzzSymbolLooksInputFacing(symbol, params) && !functionFuzzHarnessReady(symbol, params) {
		score += 18
	}
	return score
}

func functionFuzzLooksTrivialLeafName(name string) bool {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return true
	}
	if len(name) <= 3 {
		return true
	}
	switch name {
	case "get", "set", "free", "copy", "read", "write", "alloc", "new", "delete", "and", "or", "xor":
		return true
	}
	return false
}

func functionFuzzSymbolLooksInputFacing(symbol SymbolRecord, params []FunctionFuzzParamStrategy) bool {
	corpus := functionFuzzSymbolCorpus(symbol)
	if containsAny(corpus, "ioctl", "dispatch", "request", "packet", "parse", "decode", "deserialize", "validate", "verify", "check", "copy", "buffer", "payload", "registry", "read", "write") {
		return true
	}
	return functionFuzzHasDirectInputParams(params)
}

func functionFuzzHasDirectInputParams(params []FunctionFuzzParamStrategy) bool {
	for _, item := range params {
		switch item.Class {
		case "boolean", "scalar_float", "buffer", "pointer", "length", "string", "enum_or_flags", "scalar_int", "opaque":
			return true
		}
	}
	return false
}

func functionFuzzHasLengthBufferRelation(params []FunctionFuzzParamStrategy) bool {
	for _, item := range params {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(item.Relation)), "sized_by:") {
			return true
		}
	}
	return false
}

func functionFuzzSuggestedTargetLabel(cfg Config, target SymbolRecord, symbol SymbolRecord, params []FunctionFuzzParamStrategy) string {
	label := functionFuzzDisplayName(symbol)
	if strings.TrimSpace(label) == "" {
		return ""
	}
	signature := functionFuzzSanitizeSignature(symbol.Signature)
	if strings.TrimSpace(signature) != "" {
		label += " :: " + signature
	}

	hints := []string{}
	if functionFuzzHarnessReady(symbol, params) {
		hints = append(hints, functionFuzzLocalizedText(cfg, "native harness can start without custom object builders", "커스텀 객체 builder 없이 네이티브 harness를 시작할 수 있음"))
	} else if functionFuzzHasDirectInputParams(params) {
		hints = append(hints, functionFuzzLocalizedText(cfg, "only a light adapter should be needed", "가벼운 adapter 정도만 있으면 됨"))
	}
	if functionFuzzSymbolLooksInputFacing(symbol, params) {
		hints = append(hints, functionFuzzLocalizedText(cfg, "its parameters are directly fuzzable", "파라미터가 직접 fuzz하기 좋은 형태임"))
	}
	if functionFuzzHasLengthBufferRelation(params) {
		hints = append(hints, functionFuzzLocalizedText(cfg, "it already exposes a buffer-and-size style relationship", "이미 buffer-size 관계가 드러나 있음"))
	}
	if functionFuzzSuggestedTargetPathScore(target.File, symbol.File) >= 34 {
		hints = append(hints, functionFuzzLocalizedText(cfg, "it lives in the same module as the current root", "현재 루트와 같은 모듈에 있음"))
	}
	if len(hints) > 0 {
		label += "  [" + strings.Join(uniqueStrings(hints), "; ") + "]"
	}
	if strings.TrimSpace(symbol.File) != "" {
		label += "  " + filepath.ToSlash(strings.TrimSpace(symbol.File))
	}
	return strings.TrimSpace(label)
}

func functionFuzzFriendlyExecutionStatusWithConfig(cfg Config, status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "blocked":
		return functionFuzzLocalizedText(cfg, "blocked", "차단됨")
	case "pending_confirmation":
		return functionFuzzLocalizedText(cfg, "awaiting confirmation", "확인 대기")
	case "planned":
		return functionFuzzLocalizedText(cfg, "ready to run", "실행 준비 완료")
	case "running":
		return functionFuzzLocalizedText(cfg, "running", "실행 중")
	case "completed":
		return functionFuzzLocalizedText(cfg, "completed", "완료됨")
	case "failed":
		return functionFuzzLocalizedText(cfg, "failed", "실패")
	case "canceled":
		return functionFuzzLocalizedText(cfg, "canceled", "취소됨")
	case "preempted":
		return functionFuzzLocalizedText(cfg, "preempted", "선점 중단")
	default:
		if strings.TrimSpace(status) == "" {
			return functionFuzzLocalizedText(cfg, "not scheduled", "예약되지 않음")
		}
		return strings.TrimSpace(status)
	}
}

func functionFuzzFriendlyExecutionStatus(status string) string {
	return functionFuzzFriendlyExecutionStatusWithConfig(functionFuzzEnglishConfig(), status)
}

func functionFuzzFriendlyParamClassWithConfig(cfg Config, class string) string {
	switch strings.TrimSpace(class) {
	case "boolean":
		return functionFuzzLocalizedText(cfg, "boolean", "불리언")
	case "scalar_float":
		return functionFuzzLocalizedText(cfg, "floating-point scalar", "부동소수점 스칼라")
	case "scalar_int":
		return functionFuzzLocalizedText(cfg, "integer-like scalar", "정수형 스칼라")
	case "length":
		return functionFuzzLocalizedText(cfg, "size or count", "크기 또는 개수")
	case "buffer":
		return functionFuzzLocalizedText(cfg, "byte buffer", "바이트 버퍼")
	case "pointer":
		return functionFuzzLocalizedText(cfg, "pointer-like input", "포인터형 입력")
	case "string":
		return functionFuzzLocalizedText(cfg, "string or path", "문자열 또는 경로")
	case "enum_or_flags":
		return functionFuzzLocalizedText(cfg, "enum or flags", "enum 또는 flags")
	case "handle":
		return functionFuzzLocalizedText(cfg, "OS or kernel handle", "OS 또는 커널 핸들")
	case "container":
		return functionFuzzLocalizedText(cfg, "container or collection", "컨테이너 또는 컬렉션")
	case "object":
		return functionFuzzLocalizedText(cfg, "structured object", "구조화 객체")
	case "opaque":
		return functionFuzzLocalizedText(cfg, "opaque input", "불투명 입력")
	default:
		return strings.TrimSpace(class)
	}
}

func functionFuzzFriendlyParamClass(class string) string {
	return functionFuzzFriendlyParamClassWithConfig(functionFuzzEnglishConfig(), class)
}

func functionFuzzFriendlySignalKindWithConfig(cfg Config, kind string) string {
	switch strings.TrimSpace(kind) {
	case "copy_like":
		return functionFuzzLocalizedText(cfg, "copy or move path", "copy 또는 move 경로")
	case "parse_like":
		return functionFuzzLocalizedText(cfg, "parser or decoder path", "parser 또는 decoder 경로")
	case "compare_like":
		return functionFuzzLocalizedText(cfg, "validation or compare path", "검증 또는 비교 경로")
	case "alloc_like":
		return functionFuzzLocalizedText(cfg, "allocation-sensitive path", "할당 민감 경로")
	case "overlay":
		return functionFuzzLocalizedText(cfg, "security surface overlay", "보안 표면 overlay")
	default:
		return strings.TrimSpace(kind)
	}
}

func functionFuzzFriendlySignalKind(kind string) string {
	return functionFuzzFriendlySignalKindWithConfig(functionFuzzEnglishConfig(), kind)
}

func functionFuzzFriendlyObservationKindWithConfig(cfg Config, kind string) string {
	switch strings.TrimSpace(kind) {
	case "null_guard":
		return functionFuzzLocalizedText(cfg, "pointer or handle validity guard", "포인터 또는 핸들 유효성 가드")
	case "size_guard":
		return functionFuzzLocalizedText(cfg, "size or boundary guard", "크기 또는 경계 가드")
	case "dispatch_guard":
		return functionFuzzLocalizedText(cfg, "selector or dispatch guard", "선택자 또는 디스패치 가드")
	case "copy_sink":
		return functionFuzzLocalizedText(cfg, "memory transfer sink", "메모리 전송 sink")
	case "probe_sink":
		return functionFuzzLocalizedText(cfg, "user-buffer probe", "유저 버퍼 probe")
	case "alloc_site":
		return functionFuzzLocalizedText(cfg, "allocation from a computed size", "계산된 크기 기반 할당")
	case "cleanup_path":
		return functionFuzzLocalizedText(cfg, "failure unwind or cleanup edge", "실패 unwind 또는 cleanup 경로")
	case "state_publish":
		return functionFuzzLocalizedText(cfg, "state publication or registration side effect", "상태 공개 또는 등록 부작용")
	default:
		return strings.TrimSpace(kind)
	}
}

func functionFuzzSortedCodeObservations(items []FunctionFuzzCodeObservation) []FunctionFuzzCodeObservation {
	if len(items) == 0 {
		return nil
	}
	priority := func(kind string) int {
		switch strings.TrimSpace(kind) {
		case "copy_sink", "probe_sink", "dispatch_guard":
			return 4
		case "size_guard", "alloc_site":
			return 3
		case "cleanup_path", "state_publish":
			return 2
		default:
			return 1
		}
	}
	out := append([]FunctionFuzzCodeObservation(nil), items...)
	sort.SliceStable(out, func(i int, j int) bool {
		left := priority(out[i].Kind)
		right := priority(out[j].Kind)
		if left == right {
			if out[i].File == out[j].File {
				return out[i].Line < out[j].Line
			}
			return out[i].File < out[j].File
		}
		return left > right
	})
	return out
}

func buildFunctionFuzzObservationDrivenScenarios(root string, target SymbolRecord, params []FunctionFuzzParamStrategy, closure functionFuzzClosure, sinks []FunctionFuzzSinkSignal, overlays []string, observations []FunctionFuzzCodeObservation) []FunctionFuzzVirtualScenario {
	if len(observations) == 0 {
		return nil
	}
	symbolByID := map[string]SymbolRecord{}
	for _, symbol := range closure.Symbols {
		symbolByID[strings.TrimSpace(symbol.ID)] = symbol
	}
	grouped := map[string][]FunctionFuzzCodeObservation{}
	for _, item := range observations {
		grouped[strings.TrimSpace(item.SymbolID)] = append(grouped[strings.TrimSpace(item.SymbolID)], item)
	}
	type candidate struct {
		symbolID string
		score    int
	}
	candidates := []candidate{}
	for symbolID, items := range grouped {
		score := len(items) * 4
		for _, item := range items {
			switch item.Kind {
			case "copy_sink", "probe_sink", "dispatch_guard":
				score += 10
			case "size_guard", "alloc_site", "cleanup_path", "state_publish":
				score += 6
			default:
				score += 2
			}
		}
		candidates = append(candidates, candidate{symbolID: symbolID, score: score})
	}
	sort.Slice(candidates, func(i int, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].symbolID < candidates[j].symbolID
		}
		return candidates[i].score > candidates[j].score
	})

	scenarios := []FunctionFuzzVirtualScenario{}
	appendScenario := func(item FunctionFuzzVirtualScenario) {
		if strings.TrimSpace(item.Title) == "" {
			return
		}
		scenarios = append(scenarios, item)
	}
	for _, candidate := range candidates {
		if len(scenarios) >= 6 {
			break
		}
		items := grouped[candidate.symbolID]
		symbol := symbolByID[candidate.symbolID]
		if strings.TrimSpace(symbol.ID) == "" {
			continue
		}
		kindSet := map[string]bool{}
		for _, item := range items {
			kindSet[strings.TrimSpace(item.Kind)] = true
		}
		path := functionFuzzShortestSymbolPath(closure, strings.TrimSpace(symbol.ID))
		if len(path) == 0 {
			path = []string{functionFuzzDisplayName(target), functionFuzzDisplayName(symbol)}
		}
		path = functionFuzzCondensePath(path, 5)
		if kindSet["copy_sink"] && (kindSet["size_guard"] || kindSet["probe_sink"]) {
			focus := functionFuzzBestObservation(items, "copy_sink", "probe_sink", "size_guard")
			invariants, driftExamples := functionFuzzObservationInvariantInsights(items, "copy_sink", "probe_sink", "size_guard")
			branchFacts := functionFuzzObservationBranchFacts(items, "size_guard", "probe_sink", "copy_sink")
			branchOutcomes := functionFuzzObservationBranchOutcomes(root, symbol, items, "size_guard", "null_guard")
			appendScenario(FunctionFuzzVirtualScenario{
				Title:          "Attacker-controlled size can diverge from the buffer contract on a real copy or probe path",
				Confidence:     functionFuzzObservationScenarioConfidence(items, sinks, overlays, "copy_sink", "probe_sink", "size_guard"),
				Inputs:         functionFuzzObservationSizeContractInputs(params),
				Invariants:     invariants,
				BranchFacts:    branchFacts,
				BranchOutcomes: branchOutcomes,
				DriftExamples:  driftExamples,
				ExpectedFlow:   "The function body contains a real memory-transfer or probe site plus a nearby boundary check, so crafted sizes, short headers, or overlapping buffers can make validation and actual access diverge.",
				LikelyIssues: uniqueStrings([]string{
					"Out-of-bounds read or write when the checked size is not the consumed size",
					"User-controlled buffer contract breaks after a short-header or exact-boundary input",
					"Probe or validation happens on one layout while the copy or parse uses another",
				}),
				PathSketch:    path,
				PathHint:      functionFuzzObservationScenarioHint(items),
				SourceExcerpt: functionFuzzSourceExcerptForObservation(root, symbol, focus),
			})
		}
		if len(scenarios) >= 6 {
			break
		}
		if kindSet["dispatch_guard"] {
			focus := functionFuzzBestObservation(items, "dispatch_guard", "size_guard", "cleanup_path")
			invariants, driftExamples := functionFuzzObservationInvariantInsights(items, "dispatch_guard", "cleanup_path", "size_guard")
			branchFacts := functionFuzzObservationBranchFacts(items, "dispatch_guard", "cleanup_path")
			branchOutcomes := functionFuzzObservationBranchOutcomes(root, symbol, items, "dispatch_guard")
			appendScenario(FunctionFuzzVirtualScenario{
				Title:          "Unexpected control value can push execution into a weakly checked dispatch path",
				Confidence:     functionFuzzObservationScenarioConfidence(items, sinks, overlays, "dispatch_guard", "cleanup_path"),
				Inputs:         functionFuzzObservationDispatchInputs(params),
				Invariants:     invariants,
				BranchFacts:    branchFacts,
				BranchOutcomes: branchOutcomes,
				DriftExamples:  driftExamples,
				ExpectedFlow:   "The function body makes a real dispatch decision from a selector-like value, so unsupported, colliding, or boundary control codes can steer execution into rare paths before all invariants are re-checked.",
				LikelyIssues: uniqueStrings([]string{
					"Unsupported selector falls into an unintended handler or default path",
					"Privilege, capability, or mode checks are skipped on unusual control values",
					"Cleanup and rollback logic do not match the rarely used dispatch branch",
				}),
				PathSketch:    path,
				PathHint:      functionFuzzObservationScenarioHint(items),
				SourceExcerpt: functionFuzzSourceExcerptForObservation(root, symbol, focus),
			})
		}
		if len(scenarios) >= 6 {
			break
		}
		if kindSet["null_guard"] && (kindSet["copy_sink"] || kindSet["probe_sink"] || kindSet["state_publish"]) {
			focus := functionFuzzBestObservation(items, "null_guard", "probe_sink", "copy_sink", "state_publish")
			invariants, driftExamples := functionFuzzObservationInvariantInsights(items, "null_guard", "probe_sink", "copy_sink", "state_publish")
			branchFacts := functionFuzzObservationBranchFacts(items, "null_guard", "probe_sink", "copy_sink", "state_publish")
			branchOutcomes := functionFuzzObservationBranchOutcomes(root, symbol, items, "null_guard")
			appendScenario(FunctionFuzzVirtualScenario{
				Title:          "Pointer or state validity can be checked in one place and broken in another",
				Confidence:     functionFuzzObservationScenarioConfidence(items, sinks, overlays, "null_guard", "copy_sink", "probe_sink", "state_publish"),
				Inputs:         functionFuzzObservationNullStateInputs(params),
				Invariants:     invariants,
				BranchFacts:    branchFacts,
				BranchOutcomes: branchOutcomes,
				DriftExamples:  driftExamples,
				ExpectedFlow:   "The function body contains a real validity check and then continues into a meaningful sink or side effect, which is exactly where attacker-controlled stale pointers, null-plus-size combinations, or partially initialized state try to break guard ordering.",
				LikelyIssues: uniqueStrings([]string{
					"Null or stale pointer is consumed after a guard validated the wrong precondition",
					"Non-zero size or later side effect outlives an earlier pointer validity assumption",
					"State is published or cleanup starts even though the pointer-backed state was not stable",
				}),
				PathSketch:    path,
				PathHint:      functionFuzzObservationScenarioHint(items),
				SourceExcerpt: functionFuzzSourceExcerptForObservation(root, symbol, focus),
			})
		}
		if len(scenarios) >= 6 {
			break
		}
		if kindSet["alloc_site"] && (kindSet["copy_sink"] || kindSet["size_guard"]) {
			focus := functionFuzzBestObservation(items, "alloc_site", "copy_sink", "size_guard")
			invariants, driftExamples := functionFuzzObservationInvariantInsights(items, "alloc_site", "copy_sink", "size_guard", "probe_sink")
			branchFacts := functionFuzzObservationBranchFacts(items, "alloc_site", "size_guard", "copy_sink", "probe_sink")
			branchOutcomes := functionFuzzObservationBranchOutcomes(root, symbol, items, "size_guard")
			appendScenario(FunctionFuzzVirtualScenario{
				Title:          "Allocation size and later use size can be forced out of sync",
				Confidence:     functionFuzzObservationScenarioConfidence(items, sinks, overlays, "alloc_site", "copy_sink", "size_guard"),
				Inputs:         functionFuzzObservationAllocationInputs(params),
				Invariants:     invariants,
				BranchFacts:    branchFacts,
				BranchOutcomes: branchOutcomes,
				DriftExamples:  driftExamples,
				ExpectedFlow:   "The function body computes or allocates from one size-like input and later copies, probes, or parses according to another condition, which is where attackers try to create alloc-versus-use drift.",
				LikelyIssues: uniqueStrings([]string{
					"Allocated storage is smaller than the later copy or parser expectation",
					"Wrapped or truncated size participates in allocation while a larger value drives use",
					"Boundary validation and allocation sizing disagree about the same attacker-controlled value",
				}),
				PathSketch:    path,
				PathHint:      functionFuzzObservationScenarioHint(items),
				SourceExcerpt: functionFuzzSourceExcerptForObservation(root, symbol, focus),
			})
		}
		if len(scenarios) >= 6 {
			break
		}
		if kindSet["state_publish"] && kindSet["cleanup_path"] {
			focus := functionFuzzBestObservation(items, "state_publish", "cleanup_path")
			invariants, driftExamples := functionFuzzObservationInvariantInsights(items, "state_publish", "cleanup_path")
			branchFacts := functionFuzzObservationBranchFacts(items, "cleanup_path", "state_publish")
			branchOutcomes := functionFuzzObservationBranchOutcomes(root, symbol, items, "cleanup_path")
			appendScenario(FunctionFuzzVirtualScenario{
				Title:      "A later failure can unwind after security-relevant state was already published",
				Confidence: functionFuzzObservationScenarioConfidence(items, sinks, overlays, "state_publish", "cleanup_path"),
				Inputs: []string{
					"One attacker-controlled check passes far enough to publish, register, open, or expose state, and a later check fails",
				},
				Invariants:     invariants,
				BranchFacts:    branchFacts,
				BranchOutcomes: branchOutcomes,
				DriftExamples:  driftExamples,
				ExpectedFlow:   "The function body appears to publish or register state and also contains an explicit cleanup edge, so a crafted sequence can try to force partial success before the unwind path runs.",
				LikelyIssues: uniqueStrings([]string{
					"Rollback misses one published object, callback, handle, or device surface",
					"Double cleanup or stale-state reuse after a partial success path",
					"An externally reachable surface remains exposed even though the operation later failed",
				}),
				PathSketch:    path,
				PathHint:      functionFuzzObservationScenarioHint(items),
				SourceExcerpt: functionFuzzSourceExcerptForObservation(root, symbol, focus),
			})
		}
		if len(scenarios) >= 6 {
			break
		}
		if len(items) > 0 && len(scenarios) < 3 {
			focus := functionFuzzBestObservation(items, "size_guard", "copy_sink", "probe_sink", "dispatch_guard", "cleanup_path")
			invariants, driftExamples := functionFuzzObservationInvariantInsights(items, "size_guard", "copy_sink", "probe_sink", "dispatch_guard", "cleanup_path", "null_guard", "state_publish")
			branchFacts := functionFuzzObservationBranchFacts(items, "size_guard", "copy_sink", "probe_sink", "dispatch_guard", "cleanup_path", "null_guard", "state_publish")
			branchOutcomes := functionFuzzObservationBranchOutcomes(root, symbol, items, "size_guard", "null_guard", "dispatch_guard")
			appendScenario(FunctionFuzzVirtualScenario{
				Title:          "Observed source-level guard or sink can likely be stressed by contradictory attacker input",
				Confidence:     functionFuzzObservationScenarioConfidence(items, sinks, overlays),
				Inputs:         functionFuzzObservationFallbackInputs(params),
				Invariants:     invariants,
				BranchFacts:    branchFacts,
				BranchOutcomes: branchOutcomes,
				DriftExamples:  driftExamples,
				ExpectedFlow:   "Kernforge extracted a concrete guard, sink, or cleanup edge from the function body, so contradictory edge-case inputs should be tested against that exact source path instead of only relying on the signature shape.",
				LikelyIssues: uniqueStrings([]string{
					"A source-level guard is applied to one interpretation of the input while a later line uses another",
					"A sink or cleanup edge assumes an invariant that was only partially checked",
				}),
				PathSketch:    path,
				PathHint:      functionFuzzObservationScenarioHint(items),
				SourceExcerpt: functionFuzzSourceExcerptForObservation(root, symbol, focus),
			})
		}
	}
	return normalizeFunctionFuzzVirtualScenarios(scenarios)
}

func functionFuzzObservationScenarioConfidence(items []FunctionFuzzCodeObservation, sinks []FunctionFuzzSinkSignal, overlays []string, emphasizedKinds ...string) string {
	score := 0
	kindSet := map[string]bool{}
	for _, item := range items {
		kindSet[strings.TrimSpace(item.Kind)] = true
	}
	for _, kind := range emphasizedKinds {
		if kindSet[strings.TrimSpace(kind)] {
			score += 2
		}
	}
	if len(items) >= 3 {
		score += 1
	}
	for _, sink := range sinks {
		switch strings.TrimSpace(sink.Kind) {
		case "overlay":
			score += 1
		case "copy_like", "compare_like", "parse_like":
			score += 1
		}
	}
	if len(overlays) > 0 {
		score += 1
	}
	switch {
	case score >= 6:
		return "high"
	case score >= 3:
		return "medium"
	default:
		return "low"
	}
}

func functionFuzzObservationScenarioHint(items []FunctionFuzzCodeObservation) string {
	if len(items) == 0 {
		return ""
	}
	parts := []string{}
	kindSet := map[string]bool{}
	for _, item := range items {
		kindSet[strings.TrimSpace(item.Kind)] = true
	}
	if kindSet["size_guard"] {
		parts = append(parts, "Kernforge extracted a real size or boundary comparison from the function body")
	}
	if kindSet["dispatch_guard"] {
		parts = append(parts, "the same path makes a selector-driven dispatch decision")
	}
	if kindSet["copy_sink"] {
		parts = append(parts, "the same path performs a real memory transfer")
	}
	if kindSet["probe_sink"] {
		parts = append(parts, "the same path probes user-controlled memory")
	}
	if kindSet["cleanup_path"] {
		parts = append(parts, "the same function also contains an explicit failure-unwind edge")
	}
	if len(parts) == 0 {
		parts = append(parts, "Kernforge extracted a concrete source-level guard or sink on this path")
	}
	return strings.Join(parts, "; ")
}

func functionFuzzBestObservation(items []FunctionFuzzCodeObservation, preferredKinds ...string) FunctionFuzzCodeObservation {
	if len(items) == 0 {
		return FunctionFuzzCodeObservation{}
	}
	rank := map[string]int{}
	for index, kind := range preferredKinds {
		rank[strings.TrimSpace(kind)] = len(preferredKinds) - index
	}
	best := items[0]
	bestScore := -1
	for _, item := range items {
		score := rank[strings.TrimSpace(item.Kind)] * 20
		score += len(item.FocusInputs)
		if strings.TrimSpace(item.WhyItMatters) != "" {
			score += 2
		}
		if score > bestScore {
			best = item
			bestScore = score
		}
	}
	return best
}

func functionFuzzSourceExcerptForObservation(root string, symbol SymbolRecord, item FunctionFuzzCodeObservation) FunctionFuzzSourceExcerpt {
	filePath := functionFuzzResolveWorkspacePath(root, symbol.File)
	if strings.TrimSpace(filePath) == "" || item.Line <= 0 {
		return functionFuzzBuildSourceExcerpt(root, symbol)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return functionFuzzBuildSourceExcerpt(root, symbol)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return functionFuzzBuildSourceExcerpt(root, symbol)
	}
	codeOnlyLines := functionFuzzBuildCodeOnlyLines(lines)
	focus := functionFuzzMin(len(lines), functionFuzzMax(1, item.Line))
	start := functionFuzzMax(1, focus-2)
	end := functionFuzzMin(len(lines), focus+4)
	snippet := make([]string, 0, end-start+1)
	for index := start; index <= end; index++ {
		rawLine := strings.ReplaceAll(lines[index-1], "\t", "    ")
		cleanLine := strings.TrimSpace(codeOnlyLines[index-1])
		if functionFuzzShouldPreferCleanExcerptLine(rawLine, cleanLine) {
			snippet = append(snippet, cleanLine)
			continue
		}
		snippet = append(snippet, rawLine)
	}
	displayPath := filepath.ToSlash(strings.TrimSpace(symbol.File))
	if strings.TrimSpace(root) != "" {
		displayPath = filepath.ToSlash(relOrAbs(root, filePath))
	}
	return normalizeFunctionFuzzSourceExcerpt(FunctionFuzzSourceExcerpt{
		Symbol:    functionFuzzDisplayName(symbol),
		File:      displayPath,
		StartLine: start,
		FocusLine: focus,
		EndLine:   end,
		Snippet:   snippet,
	})
}

func functionFuzzAttachScenarioConnections(index SemanticIndexV2, scopeRootFile string, closure functionFuzzClosure, items []FunctionFuzzVirtualScenario) []FunctionFuzzVirtualScenario {
	if len(items) == 0 {
		return nil
	}
	out := make([]FunctionFuzzVirtualScenario, 0, len(items))
	for _, item := range items {
		focus := functionFuzzResolveScenarioFocusSymbol(closure, item)
		if strings.TrimSpace(focus.ID) != "" {
			item.FocusSymbolID = strings.TrimSpace(focus.ID)
			item.FocusSymbol = functionFuzzDisplayName(focus)
			item.FocusFile = filepath.ToSlash(strings.TrimSpace(focus.File))
			if len(item.PathSketch) == 0 {
				path := functionFuzzShortestSymbolPath(closure, strings.TrimSpace(focus.ID))
				if len(path) > 0 {
					item.PathSketch = functionFuzzCondensePath(path, 5)
				}
			}
			if strings.TrimSpace(scopeRootFile) != "" && strings.TrimSpace(focus.File) != "" {
				callFilePath := functionFuzzScenarioCallFilePath(closure, focus.ID)
				scopeBaseFile := filepath.ToSlash(strings.TrimSpace(focus.File))
				if len(callFilePath) > 0 {
					scopeBaseFile = callFilePath[0]
				}
				scopePath := functionFuzzShortestFileScopePath(index, scopeRootFile, scopeBaseFile)
				if len(scopePath) == 0 && strings.EqualFold(filepath.ToSlash(strings.TrimSpace(scopeRootFile)), scopeBaseFile) {
					scopePath = []string{filepath.ToSlash(strings.TrimSpace(scopeRootFile))}
				}
				joinedPath := functionFuzzJoinFilePaths(scopePath, callFilePath)
				if len(joinedPath) == 0 {
					joinedPath = functionFuzzShortestFileScopePath(index, scopeRootFile, focus.File)
				}
				if len(joinedPath) == 0 && strings.EqualFold(filepath.ToSlash(strings.TrimSpace(scopeRootFile)), filepath.ToSlash(strings.TrimSpace(focus.File))) {
					joinedPath = []string{filepath.ToSlash(strings.TrimSpace(scopeRootFile))}
				}
				if strings.TrimSpace(scopeRootFile) != "" && (len(joinedPath) == 0 || !functionFuzzSameScenarioFile(joinedPath[0], scopeRootFile)) {
					joinedPath = functionFuzzJoinFilePaths([]string{filepath.ToSlash(strings.TrimSpace(scopeRootFile))}, joinedPath)
				}
				item.ScopeFilePath = functionFuzzCondensePath(joinedPath, 6)
			}
		}
		out = append(out, item)
	}
	return normalizeFunctionFuzzVirtualScenarios(out)
}

func functionFuzzResolveScenarioFocusSymbol(closure functionFuzzClosure, item FunctionFuzzVirtualScenario) SymbolRecord {
	if len(closure.Symbols) == 0 {
		return SymbolRecord{}
	}
	byID := map[string]SymbolRecord{}
	for _, symbol := range closure.Symbols {
		byID[strings.TrimSpace(symbol.ID)] = symbol
	}
	if symbol, ok := byID[strings.TrimSpace(item.FocusSymbolID)]; ok {
		return symbol
	}
	excerptFile := filepath.ToSlash(strings.TrimSpace(item.SourceExcerpt.File))
	excerptSymbol := strings.TrimSpace(item.SourceExcerpt.Symbol)
	if excerptFile != "" && item.SourceExcerpt.FocusLine > 0 {
		best := SymbolRecord{}
		bestSpan := 0
		for _, symbol := range closure.Symbols {
			if !functionFuzzSameScenarioFile(symbol.File, excerptFile) {
				continue
			}
			if symbol.StartLine <= 0 || symbol.EndLine <= 0 {
				continue
			}
			if item.SourceExcerpt.FocusLine < symbol.StartLine || item.SourceExcerpt.FocusLine > symbol.EndLine {
				continue
			}
			span := symbol.EndLine - symbol.StartLine
			if strings.TrimSpace(best.ID) == "" || span < bestSpan {
				best = symbol
				bestSpan = span
			}
		}
		if strings.TrimSpace(best.ID) != "" {
			return best
		}
	}
	if excerptFile != "" && excerptSymbol != "" {
		for _, symbol := range closure.Symbols {
			if !functionFuzzSameScenarioFile(symbol.File, excerptFile) {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(functionFuzzDisplayName(symbol)), excerptSymbol) {
				return symbol
			}
		}
	}
	if len(item.PathSketch) > 0 {
		last := strings.TrimSpace(item.PathSketch[len(item.PathSketch)-1])
		if last != "" && last != "..." {
			matched := []SymbolRecord{}
			for _, symbol := range closure.Symbols {
				if strings.EqualFold(strings.TrimSpace(functionFuzzDisplayName(symbol)), last) {
					matched = append(matched, symbol)
				}
			}
			if len(matched) == 1 {
				return matched[0]
			}
			if excerptFile != "" {
				for _, symbol := range matched {
					if functionFuzzSameScenarioFile(symbol.File, excerptFile) {
						return symbol
					}
				}
			}
		}
	}
	if excerptFile != "" {
		best := SymbolRecord{}
		for _, symbol := range closure.Symbols {
			if !functionFuzzSameScenarioFile(symbol.File, excerptFile) {
				continue
			}
			if strings.TrimSpace(best.ID) == "" || symbol.StartLine < best.StartLine {
				best = symbol
			}
		}
		if strings.TrimSpace(best.ID) != "" {
			return best
		}
	}
	return SymbolRecord{}
}

func functionFuzzSameScenarioFile(left string, right string) bool {
	left = strings.ToLower(filepath.ToSlash(strings.TrimSpace(left)))
	right = strings.ToLower(filepath.ToSlash(strings.TrimSpace(right)))
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	return strings.HasSuffix(left, "/"+right) || strings.HasSuffix(right, "/"+left)
}

func functionFuzzScenarioCallFilePath(closure functionFuzzClosure, focusID string) []string {
	ids := functionFuzzShortestSymbolIDPath(closure, focusID)
	if len(ids) == 0 {
		return nil
	}
	byID := map[string]SymbolRecord{}
	for _, symbol := range closure.Symbols {
		byID[strings.TrimSpace(symbol.ID)] = symbol
	}
	files := []string{}
	for _, id := range ids {
		symbol, ok := byID[strings.TrimSpace(id)]
		if !ok {
			continue
		}
		file := filepath.ToSlash(strings.TrimSpace(symbol.File))
		if file == "" {
			continue
		}
		files = append(files, file)
	}
	return uniqueStrings(files)
}

func functionFuzzJoinFilePaths(paths ...[]string) []string {
	out := []string{}
	seen := map[string]struct{}{}
	for _, group := range paths {
		for _, item := range group {
			trimmed := filepath.ToSlash(strings.TrimSpace(item))
			if trimmed == "" {
				continue
			}
			key := strings.ToLower(trimmed)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, trimmed)
		}
	}
	return out
}

func functionFuzzObservationSizeContractInputs(params []FunctionFuzzParamStrategy) []string {
	bufferLike := functionFuzzFirstParamByClasses(params, "buffer", "pointer", "opaque", "string")
	lengthLike := functionFuzzFirstParamByClasses(params, "length", "scalar_int")
	bufferPairs := functionFuzzRelatedBufferLengthPairs(params)
	if len(bufferPairs) > 0 {
		return []string{
			functionFuzzVirtualInputForParam(bufferPairs[0].Buffer, "short"),
			functionFuzzVirtualInputForParam(bufferPairs[0].Length, "oversized"),
		}
	}
	if bufferLike != nil && lengthLike != nil {
		return []string{
			functionFuzzVirtualInputForParam(*bufferLike, "short"),
			functionFuzzVirtualInputForParam(*lengthLike, "oversized"),
		}
	}
	if bufferLike != nil {
		return []string{
			fmt.Sprintf("%s (%s) = short-header, overlapping, and oversized-declared variants", valueOrUnset(bufferLike.Name), valueOrUnset(bufferLike.RawType)),
		}
	}
	return []string{
		"Attacker-controlled input = short, exact-boundary, overlapping, and oversized-declared variants",
	}
}

func functionFuzzObservationDispatchInputs(params []FunctionFuzzParamStrategy) []string {
	flagLike := functionFuzzFirstParamByClasses(params, "enum_or_flags", "scalar_int", "length")
	if flagLike != nil {
		return []string{functionFuzzVirtualInputForParam(*flagLike, "invalid")}
	}
	return []string{
		"Selector or control value = unsupported, colliding, max-value, and sparse-bit variants",
	}
}

func functionFuzzObservationNullStateInputs(params []FunctionFuzzParamStrategy) []string {
	pointerLike := functionFuzzFirstParamByClasses(params, "object", "handle", "pointer", "buffer", "opaque")
	lengthLike := functionFuzzFirstParamByClasses(params, "length")
	inputs := []string{}
	if pointerLike != nil {
		inputs = append(inputs, functionFuzzVirtualInputForParam(*pointerLike, "invalid"))
	}
	if lengthLike != nil {
		inputs = append(inputs, fmt.Sprintf("%s (%s) = non-zero, exact-boundary, and wrapped values", valueOrUnset(lengthLike.Name), valueOrUnset(lengthLike.RawType)))
	}
	if len(inputs) == 0 {
		inputs = append(inputs, "Pointer-backed state = NULL, stale, freed, or partially initialized while related sizes or mode bits still look valid")
	}
	return inputs
}

func functionFuzzObservationAllocationInputs(params []FunctionFuzzParamStrategy) []string {
	lengthLike := functionFuzzFirstParamByClasses(params, "length", "scalar_int")
	bufferLike := functionFuzzFirstParamByClasses(params, "buffer", "pointer", "opaque")
	inputs := []string{}
	if lengthLike != nil {
		inputs = append(inputs, fmt.Sprintf("%s (%s) = 0, exact-boundary, wrapped, and oversized values", valueOrUnset(lengthLike.Name), valueOrUnset(lengthLike.RawType)))
	}
	if bufferLike != nil {
		inputs = append(inputs, fmt.Sprintf("%s (%s) = backing bytes that force a later copy or parse to consume more than the allocated size", valueOrUnset(bufferLike.Name), valueOrUnset(bufferLike.RawType)))
	}
	if len(inputs) == 0 {
		inputs = append(inputs, "Attacker-controlled size fields = values that make allocation sizing smaller than later use sizing")
	}
	return inputs
}

func functionFuzzObservationFallbackInputs(params []FunctionFuzzParamStrategy) []string {
	if len(params) == 0 {
		return []string{"Ambient state and hidden prerequisites = missing, stale, partially initialized, or already torn down"}
	}
	inputs := []string{}
	for _, item := range params {
		if len(inputs) >= 2 {
			break
		}
		switch strings.TrimSpace(item.Class) {
		case "length", "scalar_int":
			inputs = append(inputs, fmt.Sprintf("%s (%s) = zero, exact-boundary, large, and wrapped values", valueOrUnset(item.Name), valueOrUnset(item.RawType)))
		case "enum_or_flags":
			inputs = append(inputs, functionFuzzVirtualInputForParam(item, "invalid"))
		case "buffer", "pointer", "opaque":
			inputs = append(inputs, fmt.Sprintf("%s (%s) = empty, short-header, overlapping, stale, and oversized-declared variants", valueOrUnset(item.Name), valueOrUnset(item.RawType)))
		default:
			inputs = append(inputs, fmt.Sprintf("%s (%s) = contradictory edge-case state", valueOrUnset(item.Name), valueOrUnset(item.RawType)))
		}
	}
	return inputs
}

func buildFunctionFuzzVirtualScenarios(cfg Config, root string, target SymbolRecord, params []FunctionFuzzParamStrategy, closure functionFuzzClosure, sinks []FunctionFuzzSinkSignal, overlays []string, observations []FunctionFuzzCodeObservation) []FunctionFuzzVirtualScenario {
	_ = cfg
	scenarios := buildFunctionFuzzObservationDrivenScenarios(root, target, params, closure, sinks, overlays, observations)

	objectLike := functionFuzzFirstParamByClasses(params, "object", "handle", "pointer")
	stringLike := functionFuzzFirstParamByClasses(params, "string")
	bufferLike := functionFuzzFirstParamByClasses(params, "buffer", "pointer", "string", "container")
	lengthLike := functionFuzzFirstParamByClasses(params, "length")
	flagLike := functionFuzzFirstParamByClasses(params, "enum_or_flags", "scalar_int")
	handleLike := functionFuzzFirstParamByClasses(params, "handle")
	containerLike := functionFuzzFirstParamByClasses(params, "container")
	pointerLike := functionFuzzFirstParamByClasses(params, "pointer")
	booleanLike := functionFuzzFirstParamByClasses(params, "boolean")
	floatLike := functionFuzzFirstParamByClasses(params, "scalar_float")
	opaqueLike := functionFuzzFirstParamByClasses(params, "opaque")
	outputLike := functionFuzzFirstOutputLikeParam(params)
	signedScalarLike := functionFuzzFirstSignedScalarParam(params)
	bufferPairs := functionFuzzRelatedBufferLengthPairs(params)
	lengthPairs := functionFuzzLengthLengthPairs(params)

	if objectLike != nil {
		inputs := []string{functionFuzzVirtualInputForParam(*objectLike, "invalid")}
		if stringLike != nil {
			inputs = append(inputs, functionFuzzVirtualInputForParam(*stringLike, "empty"))
		}
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"overlay", "alloc_like"}, "init", "register", "device", "driver", "create")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"overlay", "alloc_like"}, "init", "register", "device", "driver", "create")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      "Null, stale, or partially initialized state object",
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "overlay", "alloc_like"),
			Inputs:     inputs,
			ExpectedFlow: compactPersistentMemoryText(
				"Initialization and boundary-handling code may consume object state before it is fully validated, then unwind through cleanup or registration paths.",
				4096,
			),
			LikelyIssues: uniqueStrings([]string{
				"Null dereference or invalid-handle use on setup paths",
				"Partial-initialization leak or double-cleanup during error unwind",
				"Security boundary reached with inconsistent state",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if bufferLike != nil && lengthLike != nil {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"copy_like", "parse_like", "compare_like"}, "copy", "buffer", "validate", "parse", "payload")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"copy_like", "parse_like", "compare_like"}, "copy", "buffer", "validate", "parse", "payload")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      "Short backing store with oversized length",
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "copy_like", "parse_like"),
			Inputs: []string{
				functionFuzzVirtualInputForParam(*bufferLike, "short"),
				functionFuzzVirtualInputForParam(*lengthLike, "oversized"),
			},
			ExpectedFlow: compactPersistentMemoryText(
				"Validation and copy logic may trust the declared size more than the actual backing store and then enter parser or memory-copy paths.",
				4096,
			),
			LikelyIssues: uniqueStrings([]string{
				"Out-of-bounds read or write on copy paths",
				"Parser desynchronization after truncated input",
				"Integer truncation or size-wrap during allocation/copy preparation",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	for _, pair := range bufferPairs {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"copy_like", "parse_like", "compare_like"}, "copy", "buffer", "size", "length", "payload")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"copy_like", "parse_like", "compare_like"}, "copy", "buffer", "size", "length", "payload")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      fmt.Sprintf("Null backing pointer with non-zero %s", valueOrUnset(pair.Length.Name)),
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "copy_like", "parse_like"),
			Inputs: []string{
				fmt.Sprintf("%s (%s) = NULL backing pointer", valueOrUnset(pair.Buffer.Name), valueOrUnset(pair.Buffer.RawType)),
				fmt.Sprintf("%s (%s) = non-zero value that still drives reads, writes, or parsing", valueOrUnset(pair.Length.Name), valueOrUnset(pair.Length.RawType)),
			},
			ExpectedFlow: "Callers may validate only the numeric length or assume the backing storage was already materialized, then enter copy, parse, or compare-heavy logic with a null source.",
			LikelyIssues: uniqueStrings([]string{
				"Null dereference after length-only validation",
				"Guard order bug where pointer presence is checked after data-dependent branching",
				"Error path inconsistency between length validation and storage validation",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      fmt.Sprintf("Zero or boundary length ambiguity on %s", valueOrUnset(pair.Length.Name)),
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "compare_like", "copy_like"),
			Inputs: []string{
				fmt.Sprintf("%s (%s) = valid-looking backing store whose first bytes resemble a header", valueOrUnset(pair.Buffer.Name), valueOrUnset(pair.Buffer.RawType)),
				fmt.Sprintf("%s (%s) = 0, 1, exact boundary, and exact boundary plus one", valueOrUnset(pair.Length.Name), valueOrUnset(pair.Length.RawType)),
			},
			ExpectedFlow: "Boundary-sensitive branches often treat zero, one, and max-allowed lengths differently, which can expose off-by-one checks, short-header handling bugs, or len==0 fast paths.",
			LikelyIssues: uniqueStrings([]string{
				"Off-by-one read or write at an exact boundary",
				"Header-only fast path bypasses a later validation stage",
				"len==0 path leaves stale state or partially initialized output",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if stringLike != nil {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"compare_like", "parse_like"}, "path", "registry", "unicode", "normalize", "validate")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"compare_like", "parse_like"}, "path", "registry", "unicode", "normalize", "validate")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      "Empty, malformed, or length-inconsistent string/path",
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "compare_like", "parse_like"),
			Inputs: []string{
				functionFuzzVirtualInputForParam(*stringLike, "malformed"),
			},
			ExpectedFlow: compactPersistentMemoryText(
				"String normalization, path parsing, registry lookup, or compare-heavy validation may proceed with inconsistent length and content assumptions.",
				4096,
			),
			LikelyIssues: uniqueStrings([]string{
				"Unchecked empty-path or malformed-string handling",
				"Length versus maximum-length mismatch on structured string types",
				"Validation bypass after normalization or canonicalization failure",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      fmt.Sprintf("Missing terminator or embedded-NUL in %s", valueOrUnset(stringLike.Name)),
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "compare_like", "parse_like"),
			Inputs: []string{
				fmt.Sprintf("%s (%s) = text with an embedded NUL before a security-sensitive suffix", valueOrUnset(stringLike.Name), valueOrUnset(stringLike.RawType)),
				fmt.Sprintf("%s (%s) = text with no terminator at the expected boundary", valueOrUnset(stringLike.Name), valueOrUnset(stringLike.RawType)),
			},
			ExpectedFlow: "Normalization, registry lookup, path comparison, or lower-level string helpers may disagree about where the logical string ends.",
			LikelyIssues: uniqueStrings([]string{
				"Validation and use sites disagree about the effective string contents",
				"Path suffix or extension checks are bypassed after embedded-NUL truncation",
				"Read past expected end when a terminator is assumed but absent",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      fmt.Sprintf("Normalization or namespace confusion in %s", valueOrUnset(stringLike.Name)),
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "compare_like", "overlay"),
			Inputs: []string{
				fmt.Sprintf("%s (%s) = path-like input using mixed separators, dot segments, device namespace prefixes, or case variants", valueOrUnset(stringLike.Name), valueOrUnset(stringLike.RawType)),
			},
			ExpectedFlow: "Canonicalization layers may normalize a path or key differently than downstream access-control or dispatch logic.",
			LikelyIssues: uniqueStrings([]string{
				"Validation bypass after alternate normalization order",
				"Namespace confusion between trusted and untrusted prefixes",
				"Security policy checked on one representation and enforced on another",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if flagLike != nil {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"compare_like", "overlay"}, "dispatch", "ioctl", "control", "request", "mode", "flags")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"compare_like", "overlay"}, "dispatch", "ioctl", "control", "request", "mode", "flags")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      "Unexpected flags, control codes, or magic constants",
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "compare_like", "overlay"),
			Inputs: []string{
				functionFuzzVirtualInputForParam(*flagLike, "invalid"),
			},
			ExpectedFlow: compactPersistentMemoryText(
				"Switches, compare-heavy guards, and dispatch helpers may route execution into unsupported or weakly validated states.",
				4096,
			),
			LikelyIssues: uniqueStrings([]string{
				"Branch or mode validation bypass",
				"Unsupported dispatch path with missing cleanup or default handling",
				"Privilege or capability checks skipped on unusual control values",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
		if bufferLike != nil || stringLike != nil {
			payloadName := "payload"
			payloadType := "input"
			if bufferLike != nil {
				payloadName = valueOrUnset(bufferLike.Name)
				payloadType = valueOrUnset(bufferLike.RawType)
			} else if stringLike != nil {
				payloadName = valueOrUnset(stringLike.Name)
				payloadType = valueOrUnset(stringLike.RawType)
			}
			scenarios = append(scenarios, FunctionFuzzVirtualScenario{
				Title:      fmt.Sprintf("Flag or mode contradicts the shape of %s", payloadName),
				Confidence: functionFuzzScenarioConfidence(sinks, overlays, "compare_like", "overlay"),
				Inputs: []string{
					fmt.Sprintf("%s (%s) = valid-looking allow or privileged mode value", valueOrUnset(flagLike.Name), valueOrUnset(flagLike.RawType)),
					fmt.Sprintf("%s (%s) = payload that only satisfies a different mode or layout", payloadName, payloadType),
				},
				ExpectedFlow: "Dispatch or validation logic may accept the control value first and only later discover the payload shape mismatch, after deeper state changes already happened.",
				LikelyIssues: uniqueStrings([]string{
					"Mode validation and payload validation become decoupled",
					"Privileged or uncommon dispatch path is entered with malformed payload state",
					"Rollback gap after state changes tied to a control value",
				}),
				PathSketch:    pathSketch,
				PathHint:      pathHint,
				SourceExcerpt: sourceExcerpt,
			})
		}
	}

	if functionFuzzLooksLikeEntryRoot(target) {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"overlay", "alloc_like", "compare_like"}, "init", "device", "dispatch", "register", "ioctl")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"overlay", "alloc_like", "compare_like"}, "init", "device", "dispatch", "register", "ioctl")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      "Entry-point partial initialization rollback",
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "overlay", "alloc_like"),
			Inputs: []string{
				"Inject a failure after one internal initialization step succeeds and a later validation step fails",
			},
			ExpectedFlow: compactPersistentMemoryText(
				"Broad initialization roots often register state, allocate resources, or expose control surfaces before every dependent step has succeeded.",
				4096,
			),
			LikelyIssues: uniqueStrings([]string{
				"Leaked global or device state after partial success",
				"Rollback path misses one resource or deregistration step",
				"Exposed IOCTL or handle surface becomes reachable before full validation completes",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      "Repeated initialization, reentry, or teardown ordering confusion",
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "overlay", "alloc_like", "compare_like"),
			Inputs: []string{
				"Re-enter the same initialization root after a partial success path already published state",
				"Trigger teardown or deregistration assumptions before the first initialization sequence fully completed",
			},
			ExpectedFlow: "Root-level initialization often assumes a single monotonic state transition, which can break when error recovery and repeated invocation interact.",
			LikelyIssues: uniqueStrings([]string{
				"Double registration or double cleanup on a published resource",
				"State machine confusion between first-init and reinit paths",
				"Public control surface remains reachable while teardown assumptions are false",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if handleLike != nil {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"overlay", "compare_like"}, "handle", "open", "close", "device", "context")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"overlay", "compare_like"}, "handle", "open", "close", "device", "context")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      fmt.Sprintf("Stale or cross-context handle reuse in %s", valueOrUnset(handleLike.Name)),
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "overlay", "compare_like"),
			Inputs: []string{
				fmt.Sprintf("%s (%s) = stale handle value from an earlier successful step", valueOrUnset(handleLike.Name), valueOrUnset(handleLike.RawType)),
				fmt.Sprintf("%s (%s) = handle value that is valid but belongs to the wrong object or context", valueOrUnset(handleLike.Name), valueOrUnset(handleLike.RawType)),
			},
			ExpectedFlow: "Validation may only check that the handle is non-null or syntactically valid before assuming the referenced object is still live and belongs to the current request.",
			LikelyIssues: uniqueStrings([]string{
				"Use-after-close or stale-context dereference through a recycled handle",
				"Privilege or ownership checks bypassed on reused handles",
				"Cleanup path releases an object that was never acquired in the current flow",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if containerLike != nil {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"parse_like", "compare_like", "alloc_like"}, "list", "array", "map", "entry", "count")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"parse_like", "compare_like", "alloc_like"}, "list", "array", "map", "entry", "count")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      fmt.Sprintf("Container cardinality mismatch or duplicate-sensitive ordering in %s", valueOrUnset(containerLike.Name)),
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "parse_like", "compare_like"),
			Inputs: []string{
				fmt.Sprintf("%s (%s) = empty container, duplicate-heavy container, and sparse ordering variants", valueOrUnset(containerLike.Name), valueOrUnset(containerLike.RawType)),
			},
			ExpectedFlow: "Iteration, uniqueness checks, and aggregate sizing logic can diverge when duplicate entries, empty states, or unstable ordering interact with validation assumptions.",
			LikelyIssues: uniqueStrings([]string{
				"Duplicate-sensitive logic bypasses uniqueness or capability assumptions",
				"Container size assumptions drift from actual iteration behavior",
				"Aggregate state or output sizing underestimates repeated entries",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if outputLike != nil && (bufferLike != nil || stringLike != nil || lengthLike != nil) {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"copy_like", "parse_like", "alloc_like"}, "copy", "write", "output", "dst", "result")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"copy_like", "parse_like", "alloc_like"}, "copy", "write", "output", "dst", "result")
		inputName := "input buffer"
		inputType := "input"
		if bufferLike != nil {
			inputName = valueOrUnset(bufferLike.Name)
			inputType = valueOrUnset(bufferLike.RawType)
		} else if stringLike != nil {
			inputName = valueOrUnset(stringLike.Name)
			inputType = valueOrUnset(stringLike.RawType)
		}
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      fmt.Sprintf("Output target %s is smaller than the transformed or copied data", valueOrUnset(outputLike.Name)),
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "copy_like", "alloc_like"),
			Inputs: []string{
				fmt.Sprintf("%s (%s) = large but structurally valid input", inputName, inputType),
				fmt.Sprintf("%s (%s) = tiny destination or result storage", valueOrUnset(outputLike.Name), valueOrUnset(outputLike.RawType)),
			},
			ExpectedFlow: "Copy, decode, or normalize logic may size the output from a transformed interpretation of the input that exceeds the immediate destination capacity.",
			LikelyIssues: uniqueStrings([]string{
				"Output truncation or overwrite after transformation expands data",
				"Length check uses source size but write uses decoded size",
				"Cleanup assumes output initialization even when the write partially failed",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if len(bufferPairs) == 0 && bufferLike != nil && lengthLike != nil {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"copy_like", "compare_like"}, "buffer", "count", "length", "size")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"copy_like", "compare_like"}, "buffer", "count", "length", "size")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      "Buffer and scalar size arguments appear semantically related but validate separately",
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "copy_like", "compare_like"),
			Inputs: []string{
				functionFuzzVirtualInputForParam(*bufferLike, "short"),
				functionFuzzVirtualInputForParam(*lengthLike, "oversized"),
			},
			ExpectedFlow: "Even without an explicit relation edge, nearby pointer and size arguments often participate in the same memory or parser operation.",
			LikelyIssues: uniqueStrings([]string{
				"Implicit buffer-length coupling is missed by one validation branch",
				"Guard uses one size while a downstream copy uses another",
				"Parser assumes the pointer and scalar were prevalidated together",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if len(lengthPairs) > 0 {
		pair := lengthPairs[0]
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"copy_like", "alloc_like", "compare_like"}, "size", "count", "length", "offset")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"copy_like", "alloc_like", "compare_like"}, "size", "count", "length", "offset")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      fmt.Sprintf("Conflicting scalar size fields %s and %s", valueOrUnset(pair.Left.Name), valueOrUnset(pair.Right.Name)),
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "copy_like", "alloc_like", "compare_like"),
			Inputs: []string{
				fmt.Sprintf("%s (%s) = small value that passes an outer guard", valueOrUnset(pair.Left.Name), valueOrUnset(pair.Left.RawType)),
				fmt.Sprintf("%s (%s) = large or wrapped value used deeper in the flow", valueOrUnset(pair.Right.Name), valueOrUnset(pair.Right.RawType)),
			},
			ExpectedFlow: "Multiple scalar size fields often split responsibility between allocation, loop bounds, and copy length calculations.",
			LikelyIssues: uniqueStrings([]string{
				"Allocation and copy size drift apart",
				"Outer validation checks one field while inner logic trusts another",
				"Offset plus length arithmetic overflows or bypasses a bound check",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if signedScalarLike != nil {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"alloc_like", "copy_like", "compare_like"}, "size", "offset", "index", "count")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"alloc_like", "copy_like", "compare_like"}, "size", "offset", "index", "count")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      fmt.Sprintf("Signed underflow or sentinel promotion in %s", valueOrUnset(signedScalarLike.Name)),
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "alloc_like", "copy_like", "compare_like"),
			Inputs: []string{
				fmt.Sprintf("%s (%s) = -1, minimum signed value, or small negative sentinel", valueOrUnset(signedScalarLike.Name), valueOrUnset(signedScalarLike.RawType)),
			},
			ExpectedFlow: "Signed values frequently become indices, lengths, or flag-like state after implicit promotion or cast-to-unsigned steps.",
			LikelyIssues: uniqueStrings([]string{
				"Negative sentinel becomes very large unsigned length or index",
				"Bounds check uses signed comparison but downstream logic uses unsigned arithmetic",
				"Loop or copy preparation underflows before allocation sizing",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if pointerLike != nil {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"parse_like", "copy_like"}, "parse", "header", "struct", "align", "buffer")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"parse_like", "copy_like"}, "parse", "header", "struct", "align", "buffer")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      fmt.Sprintf("Misaligned or structurally shifted pointer input in %s", valueOrUnset(pointerLike.Name)),
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "parse_like", "copy_like"),
			Inputs: []string{
				fmt.Sprintf("%s (%s) = backing storage deliberately shifted by one or two bytes", valueOrUnset(pointerLike.Name), valueOrUnset(pointerLike.RawType)),
			},
			ExpectedFlow: "Structured parsing and cast-heavy code may assume natural alignment or field boundaries that a shifted pointer violates.",
			LikelyIssues: uniqueStrings([]string{
				"Header fields are decoded from the wrong offsets",
				"Architecture-dependent misaligned access fault or silent corruption",
				"Field-by-field validation diverges from bulk copy interpretation",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if booleanLike != nil {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"compare_like", "overlay"}, "enable", "disable", "check", "guard", "policy", "mode")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"compare_like", "overlay"}, "enable", "disable", "check", "guard", "policy", "mode")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      fmt.Sprintf("Boolean gate inversion or stale toggle in %s", valueOrUnset(booleanLike.Name)),
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "compare_like", "overlay"),
			Inputs: []string{
				fmt.Sprintf("%s (%s) = false when later code assumes true", valueOrUnset(booleanLike.Name), valueOrUnset(booleanLike.RawType)),
				fmt.Sprintf("%s (%s) = true while dependent state still reflects the disabled path", valueOrUnset(booleanLike.Name), valueOrUnset(booleanLike.RawType)),
			},
			ExpectedFlow: "Boolean guard values often front-load access control, feature gating, or cleanup behavior before related state has actually converged.",
			LikelyIssues: uniqueStrings([]string{
				"Guard and state drift apart across a later branch",
				"Feature-disabled path still reaches privileged or expensive logic",
				"Teardown or initialization assumes the opposite toggle state",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if floatLike != nil {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"compare_like", "alloc_like"}, "scale", "ratio", "threshold", "time", "score", "weight")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"compare_like", "alloc_like"}, "scale", "ratio", "threshold", "time", "score", "weight")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      fmt.Sprintf("NaN, infinity, or precision collapse in %s", valueOrUnset(floatLike.Name)),
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "compare_like", "alloc_like"),
			Inputs: []string{
				fmt.Sprintf("%s (%s) = NaN, +Inf, -Inf, and subnormal edge cases", valueOrUnset(floatLike.Name), valueOrUnset(floatLike.RawType)),
			},
			ExpectedFlow: "Floating-point inputs can evade ordinary threshold checks, destabilize normalization logic, or collapse to sentinel-like values after casts and range clamps.",
			LikelyIssues: uniqueStrings([]string{
				"Comparison logic behaves differently for NaN or infinity than for ordinary invalid values",
				"Float-to-int conversion collapses into an unexpected size, index, or mode",
				"Normalization or scoring logic silently saturates into a dangerous control path",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if opaqueLike != nil {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"parse_like", "compare_like", "overlay"}, "parse", "decode", "dispatch", "payload", "blob")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"parse_like", "compare_like", "overlay"}, "parse", "decode", "dispatch", "payload", "blob")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      fmt.Sprintf("Opaque input partitioning and parser disagreement in %s", valueOrUnset(opaqueLike.Name)),
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "parse_like", "compare_like"),
			Inputs: []string{
				fmt.Sprintf("%s (%s) = raw bytes partitioned into empty, short-header, oversized, and magic-constant variants", valueOrUnset(opaqueLike.Name), valueOrUnset(opaqueLike.RawType)),
			},
			ExpectedFlow: "When the signature does not reveal the internal layout, the first parser, dispatcher, or validator often imposes an implicit structure that later code interprets differently.",
			LikelyIssues: uniqueStrings([]string{
				"Early structural checks and deeper consumers disagree about the same blob layout",
				"Dispatch reaches an unintended mode because a short prefix resembles a control header",
				"Opaque payload boundaries are trusted before size, ownership, or type assumptions were stabilized",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if len(params) >= 2 && functionFuzzHasCopyLikeSignal(sinks) {
		srcLike := functionFuzzFirstParamMatchingNames(params, "src", "source", "input", "data", "buffer")
		dstLike := functionFuzzFirstParamMatchingNames(params, "dst", "dest", "destination", "out", "output", "result")
		if srcLike != nil && dstLike != nil && !strings.EqualFold(strings.TrimSpace(srcLike.Name), strings.TrimSpace(dstLike.Name)) {
			pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"copy_like"}, "copy", "move", "dst", "src")
			sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"copy_like"}, "copy", "move", "dst", "src")
			scenarios = append(scenarios, FunctionFuzzVirtualScenario{
				Title:      fmt.Sprintf("Aliasing or overlapping storage between %s and %s", valueOrUnset(srcLike.Name), valueOrUnset(dstLike.Name)),
				Confidence: functionFuzzScenarioConfidence(sinks, overlays, "copy_like"),
				Inputs: []string{
					fmt.Sprintf("%s (%s) and %s (%s) = overlapping backing regions or identical pointer values", valueOrUnset(srcLike.Name), valueOrUnset(srcLike.RawType), valueOrUnset(dstLike.Name), valueOrUnset(dstLike.RawType)),
				},
				ExpectedFlow: "Copy-like code often assumes disjoint input and output buffers even when the API shape does not enforce it.",
				LikelyIssues: uniqueStrings([]string{
					"Self-overwrite corrupts later validation or decode steps",
					"Size checks pass but overlap changes the effective copy semantics",
					"Rollback or cleanup uses data already clobbered by the write path",
				}),
				PathSketch:    pathSketch,
				PathHint:      pathHint,
				SourceExcerpt: sourceExcerpt,
			})
		}
	}

	if len(params) == 0 {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"overlay", "compare_like", "alloc_like"}, "init", "state", "check", "dispatch", "register")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"overlay", "compare_like", "alloc_like"}, "init", "state", "check", "dispatch", "register")
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      "Implicit prerequisite state missing before a parameterless call",
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "overlay", "compare_like", "alloc_like"),
			Inputs: []string{
				"Invoke the function with ambient state left uninitialized, stale, already torn down, or only partially prepared by earlier steps",
			},
			ExpectedFlow: "Parameterless entry points still depend on hidden process, module, global, or thread-local state that may be assumed valid without a nearby explicit guard.",
			LikelyIssues: uniqueStrings([]string{
				"Null or stale global state is consumed because the prerequisite was implicit",
				"Repeated call order breaks one-time initialization or teardown assumptions",
				"Security-sensitive side effect occurs before the ambient state is fully validated",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	if len(scenarios) == 0 {
		pathSketch, pathHint := functionFuzzScenarioPathSketch(closure, sinks, []string{"compare_like", "parse_like", "overlay", "alloc_like"}, "check", "parse", "copy", "state", "dispatch")
		sourceExcerpt := functionFuzzScenarioSourceExcerpt(root, closure, sinks, []string{"compare_like", "parse_like", "overlay", "alloc_like"}, "check", "parse", "copy", "state", "dispatch")
		inputSummary := "vary the visible argument shapes across empty, boundary, contradictory, stale-state, and unexpected-value combinations"
		if len(params) > 0 {
			parts := []string{}
			for _, item := range params {
				if len(parts) >= 3 {
					break
				}
				parts = append(parts, fmt.Sprintf("%s (%s)", valueOrUnset(item.Name), functionFuzzFriendlyParamClass(item.Class)))
			}
			if len(parts) > 0 {
				inputSummary = "vary " + strings.Join(parts, ", ") + " across empty, boundary, contradictory, and stale-state combinations"
			}
		}
		scenarios = append(scenarios, FunctionFuzzVirtualScenario{
			Title:      "Generic edge-case state and control-flow divergence",
			Confidence: functionFuzzScenarioConfidence(sinks, overlays, "compare_like", "parse_like", "overlay", "alloc_like"),
			Inputs: []string{
				inputSummary,
			},
			ExpectedFlow: "Even when the signature is not strongly typed for a specific scenario family, unusual edge-case combinations can still push validation, state transitions, and cleanup logic out of sync.",
			LikelyIssues: uniqueStrings([]string{
				"Validation, side effects, and cleanup disagree about the effective input state",
				"One branch treats a value as rejected while a deeper path still consumes it",
				"State transitions partially commit before a later guard fails",
			}),
			PathSketch:    pathSketch,
			PathHint:      pathHint,
			SourceExcerpt: sourceExcerpt,
		})
	}

	return normalizeFunctionFuzzVirtualScenarios(scenarios)
}

func functionFuzzScenarioPathSketch(closure functionFuzzClosure, sinks []FunctionFuzzSinkSignal, preferredKinds []string, keywords ...string) ([]string, string) {
	best := functionFuzzBestScenarioSymbol(closure, sinks, preferredKinds, keywords...)
	if strings.TrimSpace(best.ID) == "" {
		return nil, ""
	}
	path := functionFuzzShortestSymbolPath(closure, strings.TrimSpace(best.ID))
	if len(path) == 0 {
		path = []string{functionFuzzDisplayName(closure.RootSymbol), functionFuzzDisplayName(best)}
	}
	path = functionFuzzCondensePath(path, 5)
	hint := functionFuzzScenarioPathHint(best, sinks, preferredKinds, keywords...)
	return path, hint
}

func functionFuzzScenarioSourceExcerpt(root string, closure functionFuzzClosure, sinks []FunctionFuzzSinkSignal, preferredKinds []string, keywords ...string) FunctionFuzzSourceExcerpt {
	best := functionFuzzBestScenarioSymbol(closure, sinks, preferredKinds, keywords...)
	if strings.TrimSpace(best.ID) == "" {
		return FunctionFuzzSourceExcerpt{}
	}
	return functionFuzzBuildSourceExcerpt(root, best, keywords...)
}

func functionFuzzBuildSourceExcerpt(root string, symbol SymbolRecord, keywords ...string) FunctionFuzzSourceExcerpt {
	filePath := functionFuzzResolveWorkspacePath(root, symbol.File)
	if strings.TrimSpace(filePath) == "" {
		return FunctionFuzzSourceExcerpt{}
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return FunctionFuzzSourceExcerpt{
			Symbol: functionFuzzDisplayName(symbol),
			File:   filepath.ToSlash(firstNonBlankString(strings.TrimSpace(symbol.File), filePath)),
		}
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return FunctionFuzzSourceExcerpt{}
	}
	codeOnlyLines := functionFuzzBuildCodeOnlyLines(lines)
	start := symbol.StartLine
	if start <= 0 || start > len(lines) {
		start = functionFuzzMin(functionFuzzMax(1, start), len(lines))
	}
	end := symbol.EndLine
	if end <= 0 || end < start {
		end = functionFuzzMin(len(lines), start+10)
	}
	if end-start > 40 {
		end = start + 40
	}
	keywords = functionFuzzSourceExcerptKeywords(symbol, keywords...)
	focus, bestScore := functionFuzzBestSourceExcerptLine(lines, codeOnlyLines, start, end, keywords...)
	if focus < start || focus > end {
		focus = start
	}
	if bestScore <= 0 || functionFuzzLineLooksNonCode(lines, codeOnlyLines, focus) || functionFuzzLineNeedsBodyExpansion(lines, codeOnlyLines, focus) {
		expandedStart := functionFuzzMax(1, start-20)
		expandedEnd := functionFuzzMin(len(lines), functionFuzzMax(end+160, start+160))
		expandedFocus, expandedScore := functionFuzzBestSourceExcerptLine(lines, codeOnlyLines, expandedStart, expandedEnd, keywords...)
		if (expandedScore > bestScore || functionFuzzLineNeedsBodyExpansion(lines, codeOnlyLines, focus)) && expandedFocus >= expandedStart && expandedFocus <= expandedEnd {
			focus = expandedFocus
			bestScore = expandedScore
			start = expandedStart
			end = expandedEnd
		}
	}
	if functionFuzzLineNeedsBodyExpansion(lines, codeOnlyLines, focus) {
		bodyStart := functionFuzzMin(len(lines), functionFuzzMax(focus+1, start))
		bodyEnd := functionFuzzMin(len(lines), functionFuzzMax(end+120, bodyStart+40))
		if bodyStart <= bodyEnd {
			bodyFocus, bodyScore := functionFuzzBestSourceExcerptLine(lines, codeOnlyLines, bodyStart, bodyEnd, keywords...)
			if bodyFocus >= bodyStart && bodyFocus <= bodyEnd && (bodyScore > bestScore || !functionFuzzLineNeedsBodyExpansion(lines, codeOnlyLines, bodyFocus)) {
				focus = bodyFocus
				bestScore = bodyScore
				end = functionFuzzMax(end, bodyEnd)
			}
		}
	}
	if bestScore <= 0 || functionFuzzLineLooksNonCode(lines, codeOnlyLines, focus) {
		fileFocus, fileScore := functionFuzzBestSourceExcerptLine(lines, codeOnlyLines, 1, len(lines), keywords...)
		if fileScore > bestScore && fileFocus >= 1 && fileFocus <= len(lines) {
			focus = fileFocus
			bestScore = fileScore
			start = 1
			end = len(lines)
		}
	}
	if functionFuzzLooksLikeBootstrapOrResolverFocus(lines, codeOnlyLines, focus) {
		bodyStart := functionFuzzMin(len(lines), functionFuzzMax(focus+1, start))
		bodyEnd := functionFuzzMin(len(lines), functionFuzzMax(end+120, bodyStart+40))
		betterFocus := functionFuzzPreferredBodyLine(lines, codeOnlyLines, bodyStart, bodyEnd)
		if betterFocus >= bodyStart && betterFocus <= bodyEnd {
			focus = betterFocus
			end = functionFuzzMax(end, bodyEnd)
		}
	}
	if focus < 1 || focus > len(lines) {
		focus = functionFuzzMin(functionFuzzMax(1, start), len(lines))
	}
	excerptStart := functionFuzzMax(start, focus-2)
	excerptEnd := functionFuzzMin(end, focus+4)
	if excerptEnd < excerptStart {
		excerptEnd = excerptStart
	}
	snippet := make([]string, 0, excerptEnd-excerptStart+1)
	for index := excerptStart; index <= excerptEnd && index <= len(lines); index++ {
		rawLine := strings.ReplaceAll(lines[index-1], "\t", "    ")
		cleanLine := ""
		if index-1 < len(codeOnlyLines) {
			cleanLine = strings.TrimSpace(codeOnlyLines[index-1])
		}
		if functionFuzzShouldPreferCleanExcerptLine(rawLine, cleanLine) {
			snippet = append(snippet, cleanLine)
			continue
		}
		snippet = append(snippet, rawLine)
	}
	if functionFuzzSnippetLooksLikeBootstrapOnly(snippet) {
		betterFocus := functionFuzzPreferredBodyLine(lines, codeOnlyLines, functionFuzzMin(len(lines), focus+1), len(lines))
		if betterFocus > 0 {
			focus = betterFocus
			excerptStart = functionFuzzMax(1, focus-2)
			excerptEnd = functionFuzzMin(len(lines), focus+4)
			snippet = make([]string, 0, excerptEnd-excerptStart+1)
			for index := excerptStart; index <= excerptEnd && index <= len(lines); index++ {
				rawLine := strings.ReplaceAll(lines[index-1], "\t", "    ")
				cleanLine := ""
				if index-1 < len(codeOnlyLines) {
					cleanLine = strings.TrimSpace(codeOnlyLines[index-1])
				}
				if functionFuzzShouldPreferCleanExcerptLine(rawLine, cleanLine) {
					snippet = append(snippet, cleanLine)
					continue
				}
				snippet = append(snippet, rawLine)
			}
		}
	}
	displayPath := filepath.ToSlash(strings.TrimSpace(symbol.File))
	if strings.TrimSpace(root) != "" {
		displayPath = filepath.ToSlash(relOrAbs(root, filePath))
	}
	return FunctionFuzzSourceExcerpt{
		Symbol:    functionFuzzDisplayName(symbol),
		File:      displayPath,
		StartLine: excerptStart,
		FocusLine: focus,
		EndLine:   excerptEnd,
		Snippet:   snippet,
	}
}

func functionFuzzSourceExcerptKeywords(symbol SymbolRecord, keywords ...string) []string {
	out := make([]string, 0, len(keywords)+6)
	for _, keyword := range keywords {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword != "" {
			out = append(out, keyword)
		}
	}
	for _, token := range functionFuzzSplitIdentifierTerms(functionFuzzDisplayName(symbol)) {
		if len(token) >= 4 {
			out = append(out, token)
		}
	}
	for _, token := range functionFuzzSplitIdentifierTerms(functionFuzzSignatureCallName(symbol.Signature)) {
		if len(token) >= 4 {
			out = append(out, token)
		}
	}
	return uniqueStrings(out)
}

func functionFuzzSplitIdentifierTerms(input string) []string {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	var terms []string
	var current strings.Builder
	for _, r := range input {
		switch {
		case r >= 'A' && r <= 'Z':
			if current.Len() > 0 {
				terms = append(terms, strings.ToLower(current.String()))
				current.Reset()
			}
			current.WriteRune(r + ('a' - 'A'))
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			current.WriteRune(r)
		default:
			if current.Len() > 0 {
				terms = append(terms, strings.ToLower(current.String()))
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		terms = append(terms, strings.ToLower(current.String()))
	}
	return uniqueStrings(terms)
}

func functionFuzzBestSourceExcerptLine(lines []string, codeOnlyLines []string, start int, end int, keywords ...string) (int, int) {
	if len(lines) == 0 {
		return 0, -1
	}
	normalized := []string{}
	for _, keyword := range keywords {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword != "" {
			normalized = append(normalized, keyword)
		}
	}
	commentMap := functionFuzzBuildBlockCommentMap(lines)
	bestLine := start
	bestScore := -1
	for index := start; index <= end && index <= len(lines); index++ {
		originalTrimmed := strings.TrimSpace(lines[index-1])
		originalLower := strings.ToLower(originalTrimmed)
		codeLine := ""
		if index-1 < len(codeOnlyLines) {
			codeLine = strings.TrimSpace(codeOnlyLines[index-1])
		}
		lower := strings.ToLower(codeLine)
		score := functionFuzzSourceExcerptLineBaseScore(codeLine, lower, originalTrimmed, originalLower, commentMap[index-1])
		for _, keyword := range normalized {
			if strings.Contains(lower, keyword) {
				score += 6
			}
		}
		if functionFuzzLooksLikeControlOrDecisionLine(codeLine) {
			score += 16
		}
		if functionFuzzLooksLikeComparisonLine(codeLine) {
			score += 8
		}
		if functionFuzzLooksLikeCallLine(codeLine) {
			score += 6
		}
		if functionFuzzLooksLikeLoggingLine(codeLine) {
			score -= 6
		}
		if functionFuzzLooksLikeBootstrapOrResolverLine(codeLine) {
			score -= 60
		}
		if functionFuzzLooksLikeSignatureOrParameterLine(codeLine) {
			score -= 28
		}
		if functionFuzzLooksLikeLowSignalStatement(codeLine) {
			score -= 10
		}
		if containsAny(lower, "switch", "case ", "if ", "if(", "else if", "default", "guard", "check", "validate", "dispatch", "return", "fail", "while", "for ", "goto cleanup", "nt_success") {
			score += 6
		}
		if strings.Contains(lower, "(") && strings.Contains(lower, ")") {
			score += 2
		}
		if strings.ContainsAny(lower, "=<>!&|+-") {
			score += 2
		}
		if strings.HasSuffix(codeLine, ";") || strings.HasSuffix(codeLine, "{") {
			score += 1
		}
		if containsAny(lower, "memcpy", "memmove", "rtlcopy", "probe", "copy", "size", "length", "offset", "buffer", "ioctl", "request", "validate", "nt_success", "goto cleanup", "resolve", "create", "open", "register") {
			score += 4
		}
		if score > bestScore {
			bestScore = score
			bestLine = index
		}
	}
	return bestLine, bestScore
}

func functionFuzzBuildBlockCommentMap(lines []string) []bool {
	out := make([]bool, len(lines))
	inBlockComment := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		out[index] = inBlockComment
		search := trimmed
		for len(search) > 0 {
			if !inBlockComment {
				open := strings.Index(search, "/*")
				if open < 0 {
					break
				}
				close := strings.Index(search[open+2:], "*/")
				if close >= 0 {
					search = search[open+2+close+2:]
					continue
				}
				inBlockComment = true
				out[index] = true
				break
			}
			close := strings.Index(search, "*/")
			out[index] = true
			if close < 0 {
				break
			}
			inBlockComment = false
			search = search[close+2:]
		}
	}
	return out
}

func functionFuzzSourceExcerptLineBaseScore(codeTrimmed string, codeLower string, originalTrimmed string, originalLower string, inBlockComment bool) int {
	if strings.TrimSpace(codeTrimmed) == "" {
		return -120
	}
	score := 0
	if inBlockComment && strings.TrimSpace(codeTrimmed) == "" {
		score -= 140
	}
	switch {
	case strings.TrimSpace(codeTrimmed) == "" && (strings.HasPrefix(originalTrimmed, "//") ||
		strings.HasPrefix(originalTrimmed, "/*") ||
		strings.HasPrefix(originalTrimmed, "*") ||
		strings.HasPrefix(originalTrimmed, "*/")):
		score -= 140
	case strings.HasPrefix(codeTrimmed, "#include"),
		strings.HasPrefix(codeTrimmed, "#define"),
		strings.HasPrefix(codeTrimmed, "#pragma"),
		strings.HasPrefix(codeTrimmed, "#if"),
		strings.HasPrefix(codeTrimmed, "#endif"),
		strings.HasPrefix(codeTrimmed, "#ifndef"):
		score -= 80
	}
	if containsAny(originalLower, "author", "contact", "website", "copyright", "licensed", "license") && strings.TrimSpace(codeTrimmed) == "" {
		score -= 120
	}
	if (strings.Count(originalTrimmed, "#") >= 4 || strings.Count(originalTrimmed, "=") >= 6 || strings.Count(originalTrimmed, "-") >= 8) && strings.TrimSpace(codeTrimmed) == "" {
		score -= 100
	}
	if containsAny(originalLower, "hacksys extreme vulnerable driver exploit", "hacksys", "banner", "ascii art") && strings.TrimSpace(codeTrimmed) == "" {
		score -= 180
	}
	if containsAny(codeLower, "goto cleanup", "nt_success", "validate", "dispatch") {
		score += 3
	}
	return score
}

func functionFuzzLooksLikeControlOrDecisionLine(codeLine string) bool {
	lower := strings.ToLower(strings.TrimSpace(codeLine))
	if lower == "" {
		return false
	}
	return strings.HasPrefix(lower, "if ") ||
		strings.HasPrefix(lower, "if(") ||
		strings.HasPrefix(lower, "else if") ||
		strings.HasPrefix(lower, "switch") ||
		strings.HasPrefix(lower, "case ") ||
		strings.HasPrefix(lower, "default:") ||
		strings.HasPrefix(lower, "while ") ||
		strings.HasPrefix(lower, "while(") ||
		strings.HasPrefix(lower, "for ") ||
		strings.HasPrefix(lower, "for(") ||
		strings.HasPrefix(lower, "return ") ||
		strings.HasPrefix(lower, "goto ") ||
		strings.Contains(lower, "nt_success(")
}

func functionFuzzLooksLikeComparisonLine(codeLine string) bool {
	lower := strings.ToLower(strings.TrimSpace(codeLine))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "==") ||
		strings.Contains(lower, "!=") ||
		strings.Contains(lower, "<=") ||
		strings.Contains(lower, ">=") ||
		strings.Contains(lower, "&&") ||
		strings.Contains(lower, "||")
}

func functionFuzzLooksLikeCallLine(codeLine string) bool {
	lower := strings.ToLower(strings.TrimSpace(codeLine))
	if lower == "" {
		return false
	}
	if !strings.Contains(lower, "(") || !strings.Contains(lower, ")") {
		return false
	}
	if functionFuzzLooksLikeControlOrDecisionLine(lower) {
		return true
	}
	return strings.HasSuffix(lower, ");") ||
		strings.HasSuffix(lower, ")") ||
		strings.Contains(lower, " = ") ||
		strings.Contains(lower, "= ")
}

func functionFuzzLooksLikeLoggingLine(codeLine string) bool {
	lower := strings.ToLower(strings.TrimSpace(codeLine))
	if lower == "" {
		return false
	}
	return containsAny(lower, "debug_", "dbgprint", "printf", "fprintf", "trace", "logger", "log(", "spdlog")
}

func functionFuzzLooksLikeBootstrapOrResolverLine(codeLine string) bool {
	lower := strings.ToLower(strings.TrimSpace(codeLine))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"loadlibrary", "loadlibrarya", "loadlibraryw",
		"getprocaddress", "getmodulehandle", "freelibrary",
		"resolvekernel", "resolveapis", "resolveimports",
		"getlasterror", "ntdll.dll", "kernel32.dll", "user32.dll",
		"hntdll", "hkernel", "exit(", "exit_failure")
}

func functionFuzzLooksLikeSignatureOrParameterLine(codeLine string) bool {
	trimmed := strings.TrimSpace(codeLine)
	lower := strings.ToLower(trimmed)
	if trimmed == "" {
		return false
	}
	if functionFuzzLooksLikeParameterAnnotationLine(trimmed) {
		return true
	}
	if strings.HasSuffix(trimmed, "(") && !functionFuzzLooksLikeControlOrDecisionLine(trimmed) {
		return true
	}
	if strings.HasSuffix(trimmed, ",") && !strings.ContainsAny(trimmed, "=;{}") {
		return true
	}
	if strings.HasSuffix(trimmed, ")") && !strings.ContainsAny(trimmed, "=;{}") && !functionFuzzLooksLikeControlOrDecisionLine(trimmed) {
		if containsAny(lower, "pirp", "pio_stack_location", "pdevice_object", "lpvoid", "pvoid", "struct ", "class ", "*") {
			return true
		}
	}
	if containsAny(lower, "__in", "__out", "__inout", "_in_", "_out_", "_inout_", "_in_opt_", "_out_opt_", "_inout_opt_") {
		return true
	}
	return false
}

func functionFuzzLooksLikeParameterAnnotationLine(codeLine string) bool {
	trimmed := strings.TrimSpace(codeLine)
	lower := strings.ToLower(trimmed)
	if trimmed == "" {
		return false
	}
	return strings.HasPrefix(lower, "_in_") ||
		strings.HasPrefix(lower, "_out_") ||
		strings.HasPrefix(lower, "_inout_") ||
		strings.HasPrefix(lower, "_in_opt_") ||
		strings.HasPrefix(lower, "_out_opt_") ||
		strings.HasPrefix(lower, "_inout_opt_") ||
		strings.HasPrefix(lower, "__in") ||
		strings.HasPrefix(lower, "__out") ||
		strings.HasPrefix(lower, "__inout")
}

func functionFuzzLooksLikeLowSignalStatement(codeLine string) bool {
	lower := strings.ToLower(strings.TrimSpace(codeLine))
	if lower == "" || !strings.HasSuffix(lower, ";") {
		return false
	}
	if functionFuzzLooksLikeControlOrDecisionLine(lower) || functionFuzzLooksLikeComparisonLine(lower) {
		return false
	}
	if functionFuzzLooksLikeCallLine(lower) {
		return false
	}
	if containsAny(lower, "return ", "goto ", "break;", "continue;", "case ", "default:", "validate", "dispatch", "copy", "probe", "cleanup") {
		return false
	}
	return true
}

func functionFuzzLooksLikeInputConsumptionLine(codeLine string) bool {
	lower := strings.ToLower(strings.TrimSpace(codeLine))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"probeforread", "probeforwrite", "memcpy", "memmove", "rtlcopymemory",
		"type3inputbuffer", "systembuffer", "userbuffer", "irp->", "irpsp->",
		"deviceiocontrol", "ioctl", "request", "buffer", "payload", "validate", "copy", "input")
}

func functionFuzzLineNeedsBodyExpansion(lines []string, codeOnlyLines []string, line int) bool {
	if line <= 0 || line > len(lines) {
		return false
	}
	codeTrimmed := ""
	if line-1 < len(codeOnlyLines) {
		codeTrimmed = strings.TrimSpace(codeOnlyLines[line-1])
	}
	rawTrimmed := strings.TrimSpace(lines[line-1])
	if functionFuzzLooksLikeSignatureOrParameterLine(codeTrimmed) {
		return true
	}
	if functionFuzzLooksLikeBootstrapOrResolverLine(codeTrimmed) {
		return true
	}
	if strings.HasPrefix(rawTrimmed, "///") {
		return true
	}
	return false
}

func functionFuzzLooksLikeBootstrapOrResolverFocus(lines []string, codeOnlyLines []string, line int) bool {
	if line <= 0 || line > len(lines) {
		return false
	}
	codeTrimmed := ""
	if line-1 < len(codeOnlyLines) {
		codeTrimmed = strings.TrimSpace(codeOnlyLines[line-1])
	}
	if functionFuzzLooksLikeBootstrapOrResolverLine(codeTrimmed) {
		return true
	}
	return false
}

func functionFuzzPreferredBodyLine(lines []string, codeOnlyLines []string, start int, end int) int {
	if start <= 0 || end < start || len(lines) == 0 {
		return 0
	}
	bestLine := 0
	bestScore := -1
	for index := start; index <= end && index <= len(lines); index++ {
		codeLine := ""
		if index-1 < len(codeOnlyLines) {
			codeLine = strings.TrimSpace(codeOnlyLines[index-1])
		}
		if codeLine == "" {
			continue
		}
		if functionFuzzLooksLikeBootstrapOrResolverLine(codeLine) || functionFuzzLooksLikeSignatureOrParameterLine(codeLine) || functionFuzzLooksLikeLoggingLine(codeLine) {
			continue
		}
		score := 0
		if functionFuzzLooksLikeInputConsumptionLine(codeLine) {
			score += 20
		}
		if functionFuzzLooksLikeControlOrDecisionLine(codeLine) {
			score += 12
		}
		if functionFuzzLooksLikeComparisonLine(codeLine) {
			score += 8
		}
		if functionFuzzLooksLikeCallLine(codeLine) {
			score += 6
		}
		if containsAny(strings.ToLower(codeLine), "probe", "buffer", "copy", "validate", "request", "ioctl", "userbuffer", "type3inputbuffer", "goto ", "return ") {
			score += 6
		}
		if score > bestScore {
			bestScore = score
			bestLine = index
		}
	}
	return bestLine
}

func functionFuzzSnippetLooksLikeBootstrapOnly(snippet []string) bool {
	if len(snippet) == 0 {
		return false
	}
	sawBootstrap := false
	sawInputUse := false
	for _, line := range snippet {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if functionFuzzLooksLikeBootstrapOrResolverLine(trimmed) {
			sawBootstrap = true
		}
		if functionFuzzLooksLikeInputConsumptionLine(trimmed) || functionFuzzLooksLikeComparisonLine(trimmed) {
			sawInputUse = true
		}
	}
	return sawBootstrap && !sawInputUse
}

func functionFuzzLineLooksNonCode(lines []string, codeOnlyLines []string, line int) bool {
	if line <= 0 || line > len(lines) {
		return true
	}
	codeTrimmed := ""
	if line-1 < len(codeOnlyLines) {
		codeTrimmed = strings.TrimSpace(codeOnlyLines[line-1])
	}
	originalTrimmed := strings.TrimSpace(lines[line-1])
	originalLower := strings.ToLower(originalTrimmed)
	return functionFuzzSourceExcerptLineBaseScore(codeTrimmed, strings.ToLower(codeTrimmed), originalTrimmed, originalLower, false) < 0
}

func functionFuzzShouldPreferCleanExcerptLine(rawLine string, cleanLine string) bool {
	rawTrimmed := strings.TrimSpace(rawLine)
	cleanLine = strings.TrimSpace(cleanLine)
	if cleanLine == "" {
		return false
	}
	if containsAny(strings.ToLower(rawTrimmed), "hacksys", "author", "contact", "website", "copyright", "license") {
		return true
	}
	if strings.Contains(rawTrimmed, "/*") || strings.Contains(rawTrimmed, "*/") || strings.Contains(rawTrimmed, "//") {
		return true
	}
	if strings.HasPrefix(strings.TrimLeft(rawTrimmed, " \t"), "#") {
		return true
	}
	if len(rawTrimmed) > len(cleanLine)+24 {
		return true
	}
	return false
}

func functionFuzzBestScenarioSymbol(closure functionFuzzClosure, sinks []FunctionFuzzSinkSignal, preferredKinds []string, keywords ...string) SymbolRecord {
	if len(closure.Symbols) == 0 {
		return SymbolRecord{}
	}
	preferred := map[string]struct{}{}
	for _, kind := range preferredKinds {
		preferred[strings.ToLower(strings.TrimSpace(kind))] = struct{}{}
	}
	best := SymbolRecord{}
	bestScore := -1
	for _, symbol := range closure.Symbols {
		if strings.EqualFold(strings.TrimSpace(symbol.ID), strings.TrimSpace(closure.RootSymbol.ID)) {
			continue
		}
		score := 0
		params := buildFunctionFuzzParameterStrategies(symbol.Signature)
		if functionFuzzSymbolLooksInputFacing(symbol, params) {
			score += 18
		}
		if functionFuzzHasDirectInputParams(params) {
			score += 12
		}
		corpus := functionFuzzSymbolCorpus(symbol)
		for _, keyword := range keywords {
			if keyword != "" && strings.Contains(corpus, strings.ToLower(strings.TrimSpace(keyword))) {
				score += 8
			}
		}
		for _, sink := range sinks {
			if !strings.EqualFold(strings.TrimSpace(sink.SymbolID), strings.TrimSpace(symbol.ID)) {
				continue
			}
			if _, ok := preferred[strings.ToLower(strings.TrimSpace(sink.Kind))]; ok {
				score += 26
			} else {
				score += 8
			}
		}
		if functionFuzzLooksLikeEntryRoot(symbol) {
			score -= 12
		}
		if functionFuzzLooksLikeBootstrapOrResolverSymbol(symbol, params) {
			score -= 26
		}
		if score > bestScore {
			best = symbol
			bestScore = score
		}
	}
	if bestScore <= 0 {
		return SymbolRecord{}
	}
	return best
}

func functionFuzzLooksLikeBootstrapOrResolverSymbol(symbol SymbolRecord, params []FunctionFuzzParamStrategy) bool {
	corpus := functionFuzzSymbolCorpus(symbol)
	if !containsAny(corpus,
		"loadlibrary", "loadlibrarya", "loadlibraryw",
		"getprocaddress", "getmodulehandle", "freelibrary",
		"resolvekernel", "resolveapis", "resolveimports",
		"lookup", "findexport", "import", "bootstrap", "startup",
		"haldispatchtable", "dispatchtable") {
		return false
	}
	if containsAny(corpus, "ioctl", "request", "buffer", "payload", "probe", "copy", "validate", "userbuffer", "type3inputbuffer") {
		return false
	}
	return !functionFuzzHasDirectInputParams(params)
}

func functionFuzzShortestSymbolPath(closure functionFuzzClosure, targetID string) []string {
	targetID = strings.TrimSpace(targetID)
	rootID := strings.TrimSpace(closure.RootSymbol.ID)
	if targetID == "" || rootID == "" {
		return nil
	}
	if strings.EqualFold(rootID, targetID) {
		return []string{functionFuzzDisplayName(closure.RootSymbol)}
	}
	adj := map[string][]string{}
	for _, edge := range closure.CallEdges {
		sourceID := strings.TrimSpace(edge.SourceID)
		nextID := strings.TrimSpace(edge.TargetID)
		if sourceID == "" || nextID == "" {
			continue
		}
		adj[sourceID] = append(adj[sourceID], nextID)
	}
	prev := map[string]string{}
	visited := map[string]struct{}{rootID: {}}
	queue := []string{rootID}
	found := false
	for len(queue) > 0 && !found {
		current := queue[0]
		queue = queue[1:]
		for _, nextID := range adj[current] {
			if _, ok := visited[nextID]; ok {
				continue
			}
			visited[nextID] = struct{}{}
			prev[nextID] = current
			if strings.EqualFold(nextID, targetID) {
				found = true
				break
			}
			queue = append(queue, nextID)
		}
	}
	if !found {
		return nil
	}
	byID := map[string]SymbolRecord{}
	for _, symbol := range closure.Symbols {
		byID[strings.TrimSpace(symbol.ID)] = symbol
	}
	ids := []string{targetID}
	for current := targetID; !strings.EqualFold(current, rootID); {
		parent, ok := prev[current]
		if !ok {
			break
		}
		ids = append(ids, parent)
		current = parent
	}
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	names := []string{}
	for _, id := range ids {
		if symbol, ok := byID[strings.TrimSpace(id)]; ok {
			names = append(names, functionFuzzDisplayName(symbol))
		}
	}
	return names
}

func functionFuzzCondensePath(items []string, max int) []string {
	if len(items) <= max || max < 4 {
		return items
	}
	out := []string{}
	out = append(out, items[0], items[1], "...")
	out = append(out, items[len(items)-2], items[len(items)-1])
	return out
}

func functionFuzzScenarioPathHint(symbol SymbolRecord, sinks []FunctionFuzzSinkSignal, preferredKinds []string, keywords ...string) string {
	reasons := []string{}
	preferred := map[string]struct{}{}
	for _, kind := range preferredKinds {
		preferred[strings.ToLower(strings.TrimSpace(kind))] = struct{}{}
	}
	for _, sink := range sinks {
		if !strings.EqualFold(strings.TrimSpace(sink.SymbolID), strings.TrimSpace(symbol.ID)) {
			continue
		}
		if _, ok := preferred[strings.ToLower(strings.TrimSpace(sink.Kind))]; ok {
			reasons = append(reasons, functionFuzzFriendlySignalKind(sink.Kind)+" appears on this reachable chain")
		}
	}
	params := buildFunctionFuzzParameterStrategies(symbol.Signature)
	if functionFuzzSymbolLooksInputFacing(symbol, params) {
		reasons = append(reasons, "the selected function is input-facing")
	}
	corpus := functionFuzzSymbolCorpus(symbol)
	for _, keyword := range keywords {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword != "" && strings.Contains(corpus, keyword) {
			reasons = append(reasons, fmt.Sprintf("its name or signature matches %q-focused logic", keyword))
			break
		}
	}
	return strings.Join(uniqueStrings(reasons), "; ")
}

type functionFuzzParamPair struct {
	Buffer FunctionFuzzParamStrategy
	Length FunctionFuzzParamStrategy
}

type functionFuzzLengthPair struct {
	Left  FunctionFuzzParamStrategy
	Right FunctionFuzzParamStrategy
}

func functionFuzzParamsByClasses(params []FunctionFuzzParamStrategy, classes ...string) []FunctionFuzzParamStrategy {
	if len(params) == 0 || len(classes) == 0 {
		return nil
	}
	classSet := map[string]struct{}{}
	for _, class := range classes {
		classSet[strings.ToLower(strings.TrimSpace(class))] = struct{}{}
	}
	out := []FunctionFuzzParamStrategy{}
	for _, item := range params {
		if _, ok := classSet[strings.ToLower(strings.TrimSpace(item.Class))]; ok {
			out = append(out, item)
		}
	}
	return out
}

func functionFuzzFindParamByName(params []FunctionFuzzParamStrategy, name string) *FunctionFuzzParamStrategy {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	for _, item := range params {
		if strings.EqualFold(strings.TrimSpace(item.Name), name) {
			copyItem := item
			return &copyItem
		}
	}
	return nil
}

func functionFuzzRelatedBufferLengthPairs(params []FunctionFuzzParamStrategy) []functionFuzzParamPair {
	out := []functionFuzzParamPair{}
	seen := map[string]struct{}{}
	for _, item := range params {
		relation := strings.ToLower(strings.TrimSpace(item.Relation))
		if !strings.HasPrefix(relation, "sized_by:") {
			continue
		}
		name := strings.TrimSpace(item.Relation[len("sized_by:"):])
		lengthParam := functionFuzzFindParamByName(params, name)
		if lengthParam == nil {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(item.Name)) + "|" + strings.ToLower(strings.TrimSpace(lengthParam.Name))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, functionFuzzParamPair{
			Buffer: item,
			Length: *lengthParam,
		})
	}
	return out
}

func functionFuzzLengthLengthPairs(params []FunctionFuzzParamStrategy) []functionFuzzLengthPair {
	lengths := functionFuzzParamsByClasses(params, "length")
	if len(lengths) < 2 {
		return nil
	}
	out := []functionFuzzLengthPair{}
	for i := 0; i < len(lengths); i++ {
		for j := i + 1; j < len(lengths); j++ {
			if functionFuzzNameRoot(lengths[i].Name) == functionFuzzNameRoot(lengths[j].Name) {
				continue
			}
			out = append(out, functionFuzzLengthPair{Left: lengths[i], Right: lengths[j]})
			if len(out) >= 3 {
				return out
			}
		}
	}
	return out
}

func functionFuzzFirstOutputLikeParam(params []FunctionFuzzParamStrategy) *FunctionFuzzParamStrategy {
	return functionFuzzFirstParamMatchingNames(params, "out", "output", "dst", "dest", "destination", "result")
}

func functionFuzzFirstParamMatchingNames(params []FunctionFuzzParamStrategy, needles ...string) *FunctionFuzzParamStrategy {
	if len(params) == 0 || len(needles) == 0 {
		return nil
	}
	for _, item := range params {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		if name == "" {
			continue
		}
		for _, needle := range needles {
			needle = strings.ToLower(strings.TrimSpace(needle))
			if needle != "" && strings.Contains(name, needle) {
				copyItem := item
				return &copyItem
			}
		}
	}
	return nil
}

func functionFuzzFirstSignedScalarParam(params []FunctionFuzzParamStrategy) *FunctionFuzzParamStrategy {
	for _, item := range params {
		if !strings.EqualFold(strings.TrimSpace(item.Class), "scalar_int") && !strings.EqualFold(strings.TrimSpace(item.Class), "length") {
			continue
		}
		lowerType := strings.ToLower(strings.TrimSpace(item.RawType))
		if strings.Contains(lowerType, "uint") || strings.Contains(lowerType, "unsigned") {
			continue
		}
		if containsAny(lowerType, "int", "long", "short", "ssize", "intptr", "ptrdiff") {
			copyItem := item
			return &copyItem
		}
	}
	return nil
}

func functionFuzzHasCopyLikeSignal(sinks []FunctionFuzzSinkSignal) bool {
	for _, sink := range sinks {
		if strings.EqualFold(strings.TrimSpace(sink.Kind), "copy_like") {
			return true
		}
	}
	return false
}

func functionFuzzFirstParamByClasses(params []FunctionFuzzParamStrategy, classes ...string) *FunctionFuzzParamStrategy {
	if len(params) == 0 || len(classes) == 0 {
		return nil
	}
	for _, item := range params {
		for _, class := range classes {
			if strings.EqualFold(strings.TrimSpace(item.Class), strings.TrimSpace(class)) {
				copyItem := item
				return &copyItem
			}
		}
	}
	return nil
}

func functionFuzzVirtualInputForParam(param FunctionFuzzParamStrategy, variant string) string {
	name := valueOrUnset(param.Name)
	rawType := valueOrUnset(param.RawType)
	lowerType := strings.ToLower(strings.TrimSpace(param.RawType))
	switch variant {
	case "invalid":
		switch param.Class {
		case "object":
			return fmt.Sprintf("%s (%s) = partially initialized object with required fields missing", name, rawType)
		case "handle":
			return fmt.Sprintf("%s (%s) = stale or invalid handle value", name, rawType)
		case "enum_or_flags", "scalar_int":
			return fmt.Sprintf("%s (%s) = 0xffffffff or unsupported control value", name, rawType)
		default:
			return fmt.Sprintf("%s (%s) = null, stale, or invalid synthetic value", name, rawType)
		}
	case "short":
		return fmt.Sprintf("%s (%s) = 1-byte backing store while paired size/count stays large", name, rawType)
	case "oversized":
		return fmt.Sprintf("%s (%s) = 0x1000 or 0xffffffff style oversized length/count", name, rawType)
	case "empty":
		if strings.Contains(lowerType, "unicode_string") {
			return fmt.Sprintf("%s (%s) = empty UNICODE_STRING with Buffer=NULL and Length=0", name, rawType)
		}
		return fmt.Sprintf("%s (%s) = empty string or path", name, rawType)
	case "malformed":
		if strings.Contains(lowerType, "unicode_string") {
			return fmt.Sprintf("%s (%s) = UNICODE_STRING with Length > MaximumLength or odd byte count", name, rawType)
		}
		return fmt.Sprintf("%s (%s) = malformed path, invalid encoding, or inconsistent terminator", name, rawType)
	default:
		return fmt.Sprintf("%s (%s) = synthetic edge-case input", name, rawType)
	}
}

func functionFuzzAttachScenarioConcreteInputs(cfg Config, params []FunctionFuzzParamStrategy, items []FunctionFuzzVirtualScenario) []FunctionFuzzVirtualScenario {
	if len(items) == 0 {
		return nil
	}
	out := make([]FunctionFuzzVirtualScenario, 0, len(items))
	for _, item := range items {
		if len(item.ConcreteInputs) == 0 {
			item.ConcreteInputs = functionFuzzScenarioConcreteInputs(cfg, params, item)
		}
		out = append(out, item)
	}
	return normalizeFunctionFuzzVirtualScenarios(out)
}

func functionFuzzScenarioConcreteInputs(cfg Config, params []FunctionFuzzParamStrategy, item FunctionFuzzVirtualScenario) []string {
	title := strings.ToLower(strings.TrimSpace(item.Title))
	bufferLike := functionFuzzFirstParamByClasses(params, "buffer", "pointer", "opaque", "string")
	lengthLike := functionFuzzFirstParamByClasses(params, "length", "scalar_int")
	flagLike := functionFuzzFirstParamByClasses(params, "enum_or_flags", "scalar_int", "length")
	pointerLike := functionFuzzFirstParamByClasses(params, "object", "handle", "pointer", "buffer", "opaque")
	stringLike := functionFuzzFirstParamByClasses(params, "string")
	bufferPairs := functionFuzzRelatedBufferLengthPairs(params)

	switch {
	case strings.Contains(title, "attacker-controlled size"), strings.Contains(title, "short backing store"), strings.Contains(title, "buffer and scalar size"), strings.Contains(title, "conflicting scalar size"), strings.Contains(title, "allocation size"):
		if len(bufferPairs) > 0 {
			return functionFuzzConcreteExamplesForBufferPair(cfg, bufferPairs[0].Buffer, bufferPairs[0].Length)
		}
		if bufferLike != nil && lengthLike != nil {
			return functionFuzzConcreteExamplesForBufferPair(cfg, *bufferLike, *lengthLike)
		}
		if bufferLike != nil {
			if fields := functionFuzzScenarioFieldHints(item); len(fields) > 0 {
				return functionFuzzConcreteExamplesForOpaqueStruct(cfg, *bufferLike, fields)
			}
			return []string{
				fmt.Sprintf("%s = 0x10000000 -> %s [41], declared_size = 0x00001000", valueOrUnset(bufferLike.Name), functionFuzzLocalizedText(cfg, "backing bytes", "실제 바이트")),
				fmt.Sprintf("%s = 0x10000000 -> %s [20 00 00 00 FF FF FF FF], %s [41 41 41 41]", valueOrUnset(bufferLike.Name), functionFuzzLocalizedText(cfg, "header bytes", "헤더 바이트"), functionFuzzLocalizedText(cfg, "actual payload bytes", "실제 payload 바이트")),
			}
		}
	case strings.Contains(title, "unexpected control value"):
		if flagLike != nil {
			return []string{
				fmt.Sprintf("%s = 0xffffffff", valueOrUnset(flagLike.Name)),
				fmt.Sprintf("%s = 0x80000000", valueOrUnset(flagLike.Name)),
			}
		}
		return []string{
			"selector = 0xffffffff",
			"selector = 0x80000000",
		}
	case strings.Contains(title, "pointer or state validity"), strings.Contains(title, "null, stale"), strings.Contains(title, "partially initialized"):
		if stringLike != nil && strings.Contains(strings.ToLower(strings.TrimSpace(stringLike.RawType)), "unicode_string") {
			return []string{
				fmt.Sprintf("%s = { Buffer = NULL, Length = 0x0008, MaximumLength = 0x0002 }", valueOrUnset(stringLike.Name)),
				fmt.Sprintf("%s = { Buffer = 0x00000000, Length = 0x0010, MaximumLength = 0x0000 }", valueOrUnset(stringLike.Name)),
			}
		}
		if pointerLike != nil {
			if fields := functionFuzzScenarioFieldHints(item); len(fields) > 0 {
				return functionFuzzConcreteExamplesForOpaqueStruct(cfg, *pointerLike, fields)
			}
			return []string{
				fmt.Sprintf("%s = NULL, related_size = 0x20", valueOrUnset(pointerLike.Name)),
				fmt.Sprintf("%s = 0xDEADBEEF, related_size = 0x20", valueOrUnset(pointerLike.Name)),
			}
		}
	case strings.Contains(title, "opaque input partitioning"):
		if bufferLike != nil {
			return []string{
				fmt.Sprintf("%s = bytes[00 00 00 00]", valueOrUnset(bufferLike.Name)),
				fmt.Sprintf("%s = bytes[41 41 41 41 FF FF FF FF]", valueOrUnset(bufferLike.Name)),
			}
		}
	}
	return nil
}

func functionFuzzConcreteExamplesForBufferPair(cfg Config, buffer FunctionFuzzParamStrategy, length FunctionFuzzParamStrategy) []string {
	bufferName := valueOrUnset(buffer.Name)
	lengthName := valueOrUnset(length.Name)
	return []string{
		fmt.Sprintf("%s = 0x10000000 -> bytes[41], %s = 0x00001000", bufferName, lengthName),
		fmt.Sprintf("%s = 0x10000000 -> %s, %s = 0x00000020", bufferName, functionFuzzLocalizedText(cfg, "same-address overlap with destination", "목적지와 같은 주소로 겹침"), lengthName),
	}
}

func functionFuzzConcreteExamplesForOpaqueStruct(cfg Config, param FunctionFuzzParamStrategy, fields []string) []string {
	name := valueOrUnset(param.Name)
	bufferField := functionFuzzFindFieldHint(fields, "buffer", "ptr", "pointer", "data")
	sizeField := functionFuzzFindFieldHint(fields, "size", "length", "count")
	if bufferField != "" && sizeField != "" {
		return []string{
			fmt.Sprintf("%s = { %s = 0x10000000, %s = 0x00001000 }, %s %s = [41]", name, bufferField, sizeField, bufferField, functionFuzzLocalizedText(cfg, "backing bytes", "실제 바이트")),
			fmt.Sprintf("%s = { %s = NULL, %s = 0x00000020 }", name, bufferField, sizeField),
		}
	}
	if sizeField != "" {
		return []string{
			fmt.Sprintf("%s = { %s = 0x00001000 }, %s = [41]", name, sizeField, functionFuzzLocalizedText(cfg, "raw bytes", "바이트열")),
			fmt.Sprintf("%s = { %s = 0xffffffff }, %s = [41 41 41 41]", name, sizeField, functionFuzzLocalizedText(cfg, "raw bytes", "바이트열")),
		}
	}
	return []string{
		fmt.Sprintf("%s = 0x10000000 -> %s [41], declared_size = 0x00001000", name, functionFuzzLocalizedText(cfg, "raw bytes", "바이트열")),
		fmt.Sprintf("%s = NULL, declared_size = 0x00000020", name),
	}
}

func functionFuzzScenarioFieldHints(item FunctionFuzzVirtualScenario) []string {
	fields := []string{}
	extract := func(text string) {
		reArrow := regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*->\s*([A-Za-z_][A-Za-z0-9_]*)`)
		reDot := regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*([A-Za-z_][A-Za-z0-9_]*)`)
		for _, matches := range reArrow.FindAllStringSubmatch(text, -1) {
			if len(matches) >= 3 {
				fields = append(fields, strings.TrimSpace(matches[2]))
			}
		}
		for _, matches := range reDot.FindAllStringSubmatch(text, -1) {
			if len(matches) >= 3 {
				fields = append(fields, strings.TrimSpace(matches[2]))
			}
		}
	}
	for _, line := range item.SourceExcerpt.Snippet {
		extract(line)
	}
	for _, line := range item.Inputs {
		extract(line)
	}
	return uniqueStrings(fields)
}

func functionFuzzFindFieldHint(fields []string, keywords ...string) string {
	for _, field := range fields {
		lower := strings.ToLower(strings.TrimSpace(field))
		for _, keyword := range keywords {
			if strings.Contains(lower, strings.ToLower(strings.TrimSpace(keyword))) {
				return strings.TrimSpace(field)
			}
		}
	}
	return ""
}

func functionFuzzScenarioConfidence(sinks []FunctionFuzzSinkSignal, overlays []string, kinds ...string) string {
	score := 0
	for _, kind := range kinds {
		kind = strings.TrimSpace(kind)
		for _, sink := range sinks {
			if strings.EqualFold(strings.TrimSpace(sink.Kind), kind) {
				score += 2
			}
		}
		if strings.EqualFold(kind, "overlay") && len(overlays) > 0 {
			score += 2
		}
	}
	switch {
	case score >= 4:
		return "high"
	case score >= 2:
		return "medium"
	default:
		return "low"
	}
}

func functionFuzzRenderTerminalHeader(ui UI, title string, tone string) string {
	label := strings.TrimSpace(title)
	if label == "" {
		label = "Section"
	}
	if !strings.HasSuffix(label, ":") {
		label += ":"
	}
	switch strings.TrimSpace(tone) {
	case "warn":
		return ui.bold(ui.warn(label))
	case "success":
		return ui.bold(ui.success(label))
	case "info":
		return ui.bold(ui.info(label))
	case "mint":
		return ui.bold(ui.mint(label))
	default:
		return ui.bold(ui.accent2(label))
	}
}

func functionFuzzPlanStatus(run FunctionFuzzRun) string {
	if strings.TrimSpace(run.TargetSymbolName) == "" && strings.TrimSpace(run.TargetSymbolID) == "" {
		return "incomplete"
	}
	return "completed"
}

func functionFuzzPlanStatusWithConfig(cfg Config, run FunctionFuzzRun) string {
	return functionFuzzDisplayText(cfg, functionFuzzPlanStatus(run))
}

func functionFuzzSourceOnlyStatus(run FunctionFuzzRun) string {
	if len(run.VirtualScenarios) > 0 {
		return "completed"
	}
	if len(run.CodeObservations) > 0 {
		return "source guards extracted, but no attack scenario was synthesized"
	}
	return "not available"
}

func functionFuzzSourceOnlyStatusWithConfig(cfg Config, run FunctionFuzzRun) string {
	if len(run.VirtualScenarios) > 0 {
		return functionFuzzLocalizedText(cfg, "completed", "완료됨")
	}
	if len(run.CodeObservations) > 0 {
		return functionFuzzLocalizedText(cfg, "source guards extracted, but no attack scenario was synthesized", "소스 가드는 추출됐지만 공격 시나리오는 합성되지 않았습니다")
	}
	return functionFuzzLocalizedText(cfg, "not available", "없음")
}

func functionFuzzSourceOnlySynthesisSummaryWithConfig(cfg Config, run FunctionFuzzRun) string {
	if len(run.VirtualScenarios) == 0 {
		if len(run.CodeObservations) > 0 {
			return functionFuzzLocalizedText(cfg, fmt.Sprintf("Kernforge extracted %d source-derived guard or sink observation(s), but they did not yet combine into a concrete attack scenario", len(run.CodeObservations)), fmt.Sprintf("Kernforge가 소스 기반 가드 또는 sink 관찰 %d개를 추출했지만, 아직 구체적인 공격 시나리오로 이어지지는 않았습니다", len(run.CodeObservations)))
		}
		return functionFuzzLocalizedText(cfg, "no source-only virtual scenarios were synthesized", "소스 전용 가상 시나리오는 생성되지 않았습니다")
	}
	relations := 0
	for _, item := range run.ParameterStrategies {
		if strings.TrimSpace(item.Relation) != "" {
			relations++
		}
	}
	label := "families"
	if len(run.VirtualScenarios) == 1 {
		label = "family"
	}
	summary := functionFuzzLocalizedText(cfg, fmt.Sprintf("Kernforge synthesized %d virtual scenario %s", len(run.VirtualScenarios), label), fmt.Sprintf("Kernforge가 가상 시나리오 %d개를 합성했습니다", len(run.VirtualScenarios)))
	if functionFuzzPrefersKorean(cfg) {
		summary += fmt.Sprintf(" (파라미터 %d개 기준)", len(run.ParameterStrategies))
	} else {
		summary += fmt.Sprintf(" across %d parameter(s)", len(run.ParameterStrategies))
	}
	if relations > 0 {
		if functionFuzzPrefersKorean(cfg) {
			summary += fmt.Sprintf(", 교차 파라미터 관계 %d개", relations)
		} else {
			summary += fmt.Sprintf(" with %d cross-parameter relation(s)", relations)
		}
	}
	if len(run.CodeObservations) > 0 {
		if functionFuzzPrefersKorean(cfg) {
			summary += fmt.Sprintf(", 실제 소스 가드/싱크 관찰 %d개 기반", len(run.CodeObservations))
		} else {
			summary += fmt.Sprintf(", grounded in %d source-derived guard/sink observation(s)", len(run.CodeObservations))
		}
	}
	if len(run.OverlayDomains) > 0 {
		if functionFuzzPrefersKorean(cfg) {
			summary += fmt.Sprintf(", 노출 표면 %d개", len(run.OverlayDomains))
		} else {
			summary += fmt.Sprintf(" and %d exposed surface(s)", len(run.OverlayDomains))
		}
	}
	return summary
}

func functionFuzzSourceOnlySynthesisSummary(run FunctionFuzzRun) string {
	return functionFuzzSourceOnlySynthesisSummaryWithConfig(functionFuzzEnglishConfig(), run)
}

func functionFuzzScenarioConfidenceScore(confidence string) int {
	switch strings.ToLower(strings.TrimSpace(confidence)) {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func functionFuzzSortedVirtualScenarios(items []FunctionFuzzVirtualScenario) []FunctionFuzzVirtualScenario {
	if len(items) == 0 {
		return nil
	}
	out := append([]FunctionFuzzVirtualScenario(nil), items...)
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].RiskScore != out[j].RiskScore {
			return out[i].RiskScore > out[j].RiskScore
		}
		left := functionFuzzScenarioConfidenceScore(out[i].Confidence)
		right := functionFuzzScenarioConfidenceScore(out[j].Confidence)
		if left != right {
			return left > right
		}
		return strings.TrimSpace(out[i].Title) < strings.TrimSpace(out[j].Title)
	})
	return out
}

func functionFuzzTopScenario(run FunctionFuzzRun) *FunctionFuzzVirtualScenario {
	items := functionFuzzSortedVirtualScenarios(run.VirtualScenarios)
	if len(items) == 0 {
		return nil
	}
	item := items[0]
	return &item
}

func functionFuzzTopScenarioHeadline(cfg Config, item FunctionFuzzVirtualScenario) string {
	title := functionFuzzDisplayText(cfg, strings.TrimSpace(item.Title))
	if item.RiskScore > 0 {
		title = fmt.Sprintf("[%d/100] %s", item.RiskScore, title)
	}
	issues := uniqueStrings(item.LikelyIssues)
	if len(issues) == 0 {
		return title
	}
	return title + " -> " + functionFuzzDisplayText(cfg, issues[0])
}

func functionFuzzBranchOutcomeCompactSummary(cfg Config, item FunctionFuzzBranchOutcome) string {
	callChain := uniqueStrings(item.DownstreamCalls)
	switch strings.TrimSpace(item.EffectKind) {
	case "reject":
		return functionFuzzLocalizedText(cfg, "reject/status path is taken", "reject/status 경로로 빠집니다")
	case "cleanup":
		return functionFuzzLocalizedText(cfg, "cleanup or early-exit is taken", "cleanup 또는 early-exit로 빠집니다")
	case "copy", "probe", "dispatch", "publish", "alloc":
		if len(callChain) > 0 {
			if functionFuzzPrefersKorean(cfg) {
				return strings.Join(callChain, " -> ") + " 까지 진행합니다"
			}
			return "execution continues into " + strings.Join(callChain, " -> ")
		}
		switch strings.TrimSpace(item.EffectKind) {
		case "copy":
			return functionFuzzLocalizedText(cfg, "execution reaches a real copy sink", "실제 copy sink까지 진행합니다")
		case "probe":
			return functionFuzzLocalizedText(cfg, "execution reaches a real probe path", "실제 probe 경로로 진행합니다")
		case "dispatch":
			return functionFuzzLocalizedText(cfg, "execution enters a real dispatch path", "실제 dispatch 경로로 진행합니다")
		case "publish":
			return functionFuzzLocalizedText(cfg, "execution reaches publish/register side effects", "상태 공개 또는 등록 경로로 진행합니다")
		case "alloc":
			return functionFuzzLocalizedText(cfg, "execution reaches allocation or size-dependent setup", "할당 또는 크기 기반 준비 경로로 진행합니다")
		}
	default:
		if len(callChain) > 0 {
			if functionFuzzPrefersKorean(cfg) {
				return strings.Join(callChain, " -> ") + " 까지 진행합니다"
			}
			return "execution continues into " + strings.Join(callChain, " -> ")
		}
	}
	return functionFuzzLocalizedText(cfg, "execution continues into the next meaningful path", "다음 의미 있는 경로로 진행합니다")
}

func functionFuzzBranchDeltaSummary(cfg Config, item FunctionFuzzVirtualScenario) string {
	if len(item.BranchOutcomes) == 0 {
		return ""
	}
	var trueOutcome *FunctionFuzzBranchOutcome
	var falseOutcome *FunctionFuzzBranchOutcome
	for _, outcome := range item.BranchOutcomes {
		copyOutcome := outcome
		switch strings.TrimSpace(outcome.Side) {
		case "true":
			if trueOutcome == nil {
				trueOutcome = &copyOutcome
			}
		case "false":
			if falseOutcome == nil {
				falseOutcome = &copyOutcome
			}
		}
	}
	predicateToken := ""
	if len(item.BranchFacts) > 0 {
		predicateToken = item.BranchFacts[0]
	} else {
		predicateToken = firstNonBlankString(
			func() string {
				if trueOutcome != nil {
					return trueOutcome.Predicate
				}
				return ""
			}(),
			func() string {
				if falseOutcome != nil {
					return falseOutcome.Predicate
				}
				return ""
			}(),
		)
	}
	counterexample := strings.TrimSpace(functionFuzzComparisonCounterexampleText(cfg, predicateToken))
	predicate := strings.TrimSpace(functionFuzzComparisonFactText(cfg, predicateToken))
	trueSummary := ""
	falseSummary := ""
	if trueOutcome != nil {
		trueSummary = functionFuzzBranchOutcomeCompactSummary(cfg, *trueOutcome)
	}
	if falseOutcome != nil {
		falseSummary = functionFuzzBranchOutcomeCompactSummary(cfg, *falseOutcome)
	}
	switch {
	case counterexample != "" && trueSummary != "" && falseSummary != "":
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("%s 이면 %s, 그렇지 않으면 %s", counterexample, trueSummary, falseSummary)
		}
		return fmt.Sprintf("If %s, %s; otherwise, %s", counterexample, trueSummary, falseSummary)
	case predicate != "" && trueSummary != "" && falseSummary != "":
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("%s 가 참이면 %s, 거짓이면 %s", predicate, trueSummary, falseSummary)
		}
		return fmt.Sprintf("If %s is true, %s; if false, %s", predicate, trueSummary, falseSummary)
	case counterexample != "" && trueSummary != "":
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("%s 이면 %s", counterexample, trueSummary)
		}
		return fmt.Sprintf("If %s, %s", counterexample, trueSummary)
	case predicate != "" && falseSummary != "":
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("%s 가 거짓이면 %s", predicate, falseSummary)
		}
		return fmt.Sprintf("If %s is false, %s", predicate, falseSummary)
	default:
		return firstNonBlankString(trueSummary, falseSummary)
	}
}

func functionFuzzScenarioScoreLabel(item FunctionFuzzVirtualScenario) string {
	if item.RiskScore <= 0 {
		return ""
	}
	return fmt.Sprintf("%d/100", item.RiskScore)
}

func functionFuzzColorizeRiskText(ui UI, score int, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	switch {
	case score >= 90:
		return ui.bold(ui.error(text))
	case score >= 75:
		return ui.bold(ui.warn(text))
	case score >= 50:
		return ui.bold(ui.accent2(text))
	case score >= 25:
		return ui.info(text)
	case score > 0:
		return ui.dim(text)
	default:
		return text
	}
}

func functionFuzzScenarioDisplayHeader(cfg Config, item FunctionFuzzVirtualScenario) string {
	title := functionFuzzDisplayText(cfg, strings.TrimSpace(item.Title))
	parts := []string{}
	if label := functionFuzzScenarioScoreLabel(item); label != "" {
		parts = append(parts, label)
	}
	if strings.TrimSpace(item.Confidence) != "" {
		parts = append(parts, functionFuzzDisplayText(cfg, strings.TrimSpace(item.Confidence)))
	}
	if len(parts) > 0 {
		title += " [" + strings.Join(parts, " | ") + "]"
	}
	return title
}

func functionFuzzScenarioRankWhy(cfg Config, item FunctionFuzzVirtualScenario) string {
	if len(item.ScoreReasons) == 0 {
		return ""
	}
	reasons := []string{}
	for _, itemReason := range limitStrings(item.ScoreReasons, 3) {
		reasons = append(reasons, functionFuzzDisplayText(cfg, itemReason))
	}
	return strings.Join(reasons, "; ")
}

func functionFuzzPrimaryInspectLocation(run FunctionFuzzRun) string {
	item := functionFuzzTopScenario(run)
	if item == nil {
		return ""
	}
	excerpt := item.SourceExcerpt
	if strings.TrimSpace(excerpt.File) != "" {
		location := strings.TrimSpace(excerpt.File)
		if excerpt.FocusLine > 0 {
			location += ":" + strconv.Itoa(excerpt.FocusLine)
		} else if excerpt.StartLine > 0 {
			location += ":" + strconv.Itoa(excerpt.StartLine)
		}
		return location
	}
	if len(item.PathSketch) > 0 {
		return strings.Join(item.PathSketch, " -> ")
	}
	return ""
}

func functionFuzzSurfaceSummary(cfg Config, run FunctionFuzzRun) string {
	if len(run.OverlayDomains) == 0 {
		return ""
	}
	labels := []string{}
	for _, domain := range limitStrings(run.OverlayDomains, 4) {
		labels = append(labels, functionFuzzFriendlyOverlayNameWithConfig(cfg, domain))
	}
	if len(labels) == 0 {
		return ""
	}
	return functionFuzzLocalizedText(cfg, "The mapped closure crosses "+strings.Join(labels, ", ")+".", "매핑된 closure는 "+strings.Join(labels, ", ")+" 표면을 가로지릅니다.")
}

func functionFuzzConclusionLines(cfg Config, run FunctionFuzzRun) []string {
	lines := []string{}
	target := valueOrUnset(run.TargetSymbolName)
	if strings.EqualFold(strings.TrimSpace(run.ScopeMode), "file") && strings.TrimSpace(run.ScopeRootFile) != "" {
		lines = append(lines, functionFuzzLocalizedText(cfg, fmt.Sprintf("Input scope: Kernforge started from %s, expanded include/import relationships across %d file(s), and then chose %s as the representative function root.", filepath.ToSlash(strings.TrimSpace(run.ScopeRootFile)), len(run.ScopeFiles), target), fmt.Sprintf("입력 범위: Kernforge가 %s에서 시작해 include/import 관계로 %d개 파일까지 확장한 뒤, %s를 대표 함수 루트로 골랐습니다.", filepath.ToSlash(strings.TrimSpace(run.ScopeRootFile)), len(run.ScopeFiles), target)))
	}
	switch {
	case len(run.VirtualScenarios) > 0:
		lines = append(lines, functionFuzzLocalizedText(cfg, fmt.Sprintf("Bottom line: Kernforge completed AI source-only fuzz analysis for %s and predicted %d issue pattern(s) worth reviewing.", target, len(run.VirtualScenarios)), fmt.Sprintf("결론: Kernforge가 %s에 대한 AI 소스 전용 fuzz 분석을 완료했고, 검토할 가치가 있는 이슈 패턴 %d개를 예측했습니다.", target, len(run.VirtualScenarios))))
	case len(run.CodeObservations) > 0:
		lines = append(lines, functionFuzzLocalizedText(cfg, fmt.Sprintf("Bottom line: Kernforge extracted %d source-derived guard or sink observation(s) for %s, but they did not yet combine into a strong attack scenario.", len(run.CodeObservations), target), fmt.Sprintf("결론: Kernforge가 %s에 대해 소스 기반 가드 또는 sink 관찰 %d개를 추출했지만, 아직 강한 공격 시나리오로는 결합되지 않았습니다.", target, len(run.CodeObservations))))
	default:
		lines = append(lines, functionFuzzLocalizedText(cfg, fmt.Sprintf("Bottom line: Kernforge mapped %s, but it could not synthesize useful source-only issue scenarios from the current signature and call closure.", target), fmt.Sprintf("결론: Kernforge가 %s를 매핑했지만, 현재 시그니처와 call closure만으로는 유의미한 소스 전용 이슈 시나리오를 합성하지 못했습니다.", target)))
	}
	if top := functionFuzzTopScenario(run); top != nil {
		lines = append(lines, functionFuzzLocalizedText(cfg, "Most actionable predicted problem: ", "가장 우선 확인할 예측 문제: ")+functionFuzzTopScenarioHeadline(cfg, *top))
		if delta := strings.TrimSpace(functionFuzzBranchDeltaSummary(cfg, *top)); delta != "" {
			lines = append(lines, functionFuzzLocalizedText(cfg, "Most useful branch delta: ", "가장 유용한 분기 차이 요약: ")+delta)
		}
	}
	if location := functionFuzzPrimaryInspectLocation(run); strings.TrimSpace(location) != "" {
		lines = append(lines, functionFuzzLocalizedText(cfg, "Inspect first: ", "먼저 볼 위치: ")+location)
	}
	if surface := functionFuzzSurfaceSummary(cfg, run); strings.TrimSpace(surface) != "" {
		lines = append(lines, surface)
	}
	lines = append(lines, functionFuzzLocalizedText(cfg, "What is not confirmed yet: no runtime crash, sanitizer finding, or concrete repro was produced by this command output alone.", "아직 확정되지 않은 점: 이 명령 출력만으로는 런타임 crash, sanitizer 결과, 구체적 재현이 확인된 것은 아닙니다."))
	return uniqueStrings(lines)
}

func functionFuzzStatusLines(cfg Config, run FunctionFuzzRun) []string {
	lines := []string{}
	if scopeSummary := functionFuzzScopeSummary(cfg, run); strings.TrimSpace(scopeSummary) != "" {
		lines = append(lines, scopeSummary)
	}
	lines = append(lines,
		functionFuzzLocalizedText(cfg, "Planning: ", "계획 상태: ")+functionFuzzPlanStatusWithConfig(cfg, run),
		functionFuzzLocalizedText(cfg, "AI source-only analysis: ", "AI 소스 전용 분석: ")+functionFuzzSourceOnlyStatusWithConfig(cfg, run),
		functionFuzzLocalizedText(cfg, "Source-derived attack surface: ", "소스 기반 공격 표면: ")+functionFuzzObservationSummary(cfg, run),
		functionFuzzLocalizedText(cfg, "Native auto-run: ", "네이티브 자동 실행: ")+functionFuzzFriendlyExecutionStatusWithConfig(cfg, run.Execution.Status),
		functionFuzzLocalizedText(cfg, "Current target role: ", "현재 타깃 역할: ")+functionFuzzTargetRoleSummary(cfg, run),
		functionFuzzLocalizedText(cfg, "Reachability summary: ", "도달성 요약: ")+functionFuzzReachabilitySummary(cfg, run),
		functionFuzzLocalizedText(cfg, "Analysis priority: ", "분석 우선순위: ")+functionFuzzAnalysisPrioritySummary(cfg, run),
	)
	return uniqueStrings(lines)
}

func functionFuzzObservationSummary(cfg Config, run FunctionFuzzRun) string {
	if len(run.CodeObservations) == 0 {
		return functionFuzzLocalizedText(cfg, "no concrete guard or sink observations were extracted from the mapped function bodies", "매핑된 함수 본문에서 구체적인 가드나 sink 관찰을 추출하지 못했습니다")
	}
	sorted := functionFuzzSortedCodeObservations(run.CodeObservations)
	labels := []string{}
	seen := map[string]struct{}{}
	for _, item := range sorted {
		label := functionFuzzFriendlyObservationKindWithConfig(cfg, item.Kind)
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		labels = append(labels, label)
		if len(labels) >= 3 {
			break
		}
	}
	if len(labels) == 0 {
		return functionFuzzLocalizedText(cfg, fmt.Sprintf("%d source-level observation(s)", len(run.CodeObservations)), fmt.Sprintf("소스 레벨 관찰 %d개", len(run.CodeObservations)))
	}
	if functionFuzzPrefersKorean(cfg) {
		return fmt.Sprintf("%d개 관찰 추출됨 (%s)", len(run.CodeObservations), strings.Join(labels, ", "))
	}
	return fmt.Sprintf("%d observation(s) extracted (%s)", len(run.CodeObservations), strings.Join(labels, ", "))
}

func functionFuzzScopeSummary(cfg Config, run FunctionFuzzRun) string {
	if !strings.EqualFold(strings.TrimSpace(run.ScopeMode), "file") || strings.TrimSpace(run.ScopeRootFile) == "" {
		return ""
	}
	fileCount := len(run.ScopeFiles)
	if fileCount <= 0 {
		fileCount = 1
	}
	return functionFuzzLocalizedText(cfg, fmt.Sprintf("Input scope: file-driven analysis starting from %s and expanding across %d file(s) linked by include/import relationships", filepath.ToSlash(strings.TrimSpace(run.ScopeRootFile)), fileCount), fmt.Sprintf("입력 범위: %s에서 시작해 include/import 관계로 연결된 %d개 파일을 기준으로 분석했습니다", filepath.ToSlash(strings.TrimSpace(run.ScopeRootFile)), fileCount))
}

func functionFuzzRiskPriorityLabel(cfg Config, score int) string {
	switch {
	case score >= 85:
		return functionFuzzLocalizedText(cfg, fmt.Sprintf("high (%d/100)", score), fmt.Sprintf("높음 (%d/100)", score))
	case score >= 60:
		return functionFuzzLocalizedText(cfg, fmt.Sprintf("medium-high (%d/100)", score), fmt.Sprintf("중상 (%d/100)", score))
	case score > 0:
		return fmt.Sprintf("%d/100", score)
	default:
		return functionFuzzLocalizedText(cfg, "unscored", "미채점")
	}
}

func functionFuzzAnalysisPrioritySummary(cfg Config, run FunctionFuzzRun) string {
	label := functionFuzzRiskPriorityLabel(cfg, run.RiskScore)
	if run.RiskScore <= 0 {
		return label
	}
	return functionFuzzLocalizedText(cfg, label+"; this is an exposure and review-priority score, not proof of a vulnerability", label+"; 이 점수는 노출도와 검토 우선순위를 뜻할 뿐, 취약점 증거 자체는 아닙니다")
}

func functionFuzzReachabilitySummary(cfg Config, run FunctionFuzzRun) string {
	if run.ReachableCallCount <= 0 && run.ReachableDepth <= 0 {
		return functionFuzzLocalizedText(cfg, "no downstream call relationships were mapped from the selected root", "선택한 루트에서 아래쪽 호출 관계는 매핑되지 않았습니다")
	}
	summary := ""
	if strings.EqualFold(strings.TrimSpace(run.ScopeMode), "file") && strings.TrimSpace(run.ScopeRootFile) != "" {
		summary = functionFuzzLocalizedText(cfg, fmt.Sprintf("within the selected file scope, Kernforge mapped %d caller-to-callee link(s)", run.ReachableCallCount), fmt.Sprintf("선택한 파일 범위 안에서 caller-to-callee 링크 %d개를 매핑했습니다", run.ReachableCallCount))
	} else {
		summary = functionFuzzLocalizedText(cfg, fmt.Sprintf("starting from this root, Kernforge followed %d caller-to-callee link(s)", run.ReachableCallCount), fmt.Sprintf("이 루트에서 시작해 caller-to-callee 링크 %d개를 따라갔습니다", run.ReachableCallCount))
	}
	if run.ReachableDepth > 0 {
		if functionFuzzPrefersKorean(cfg) {
			summary += fmt.Sprintf(", 가장 깊은 경로는 %d 호출 거리입니다", run.ReachableDepth)
		} else {
			summary += fmt.Sprintf("; the deepest discovered path is %d call(s) away", run.ReachableDepth)
		}
	}
	if run.ReachableTruncated {
		summary += functionFuzzLocalizedText(cfg, "; the traversal was truncated to keep the result manageable", ", 결과 크기를 관리하기 위해 traversal을 중간에서 제한했습니다")
	}
	return summary
}

func functionFuzzTargetRoleSummary(cfg Config, run FunctionFuzzRun) string {
	target := SymbolRecord{
		Name:      run.TargetSymbolName,
		File:      run.TargetFile,
		Signature: run.TargetSignature,
	}
	if functionFuzzLooksLikeEntryRoot(target) {
		return functionFuzzLocalizedText(cfg, "broad entry point or initialization root", "넓은 진입점 또는 초기화 루트")
	}
	if run.HarnessReady {
		return functionFuzzLocalizedText(cfg, "direct function target suitable for autonomous source-level fuzzing", "자동 소스 레벨 fuzzing에 적합한 직접 함수 타깃")
	}
	return functionFuzzLocalizedText(cfg, "intermediate function that still needs project-specific setup", "여전히 프로젝트 전용 준비가 필요한 중간 함수")
}

func functionFuzzBestNextMove(cfg Config, run FunctionFuzzRun) string {
	if len(run.SuggestedTargets) > 0 {
		return functionFuzzLocalizedText(cfg, "pivot to the best input-facing function below so Kernforge can automatically explore more precise virtual parameter combinations", "아래의 가장 좋은 입력 지향 함수로 내려가면 Kernforge가 더 정밀한 가상 파라미터 조합을 자동 탐색할 수 있습니다")
	}
	if !run.HarnessReady {
		return functionFuzzLocalizedText(cfg, "choose a deeper function with simpler inputs so source-only analysis becomes more precise without manual fixtures", "입력이 더 단순한 안쪽 함수로 내려가면 별도 fixture 없이도 소스 전용 분석 정밀도가 올라갑니다")
	}
	if strings.EqualFold(strings.TrimSpace(run.Execution.Status), "pending_confirmation") {
		return functionFuzzLocalizedText(cfg, "review recovered build settings and continue autonomous fuzzing when they look correct", "복구된 빌드 설정을 검토한 뒤 맞아 보이면 자동 fuzzing을 계속 진행하면 됩니다")
	}
	if strings.EqualFold(strings.TrimSpace(run.Execution.Status), "planned") {
		return functionFuzzLocalizedText(cfg, "start or monitor native autonomous fuzzing", "네이티브 자동 fuzzing을 시작하거나 진행 상태를 확인하면 됩니다")
	}
	return functionFuzzLocalizedText(cfg, "review the detailed sections below and refine the next harness target", "아래 세부 내용을 보고 다음 harness 타깃을 다듬으면 됩니다")
}

func functionFuzzBestSuggestedTarget(run FunctionFuzzRun) string {
	if len(run.SuggestedTargets) == 0 {
		return ""
	}
	return strings.TrimSpace(run.SuggestedTargets[0])
}

func functionFuzzSplitSuggestedTargetLabel(value string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	start := strings.LastIndex(value, "  [")
	if start < 0 {
		start = strings.LastIndex(value, " [")
	}
	end := strings.LastIndex(value, "]")
	if start < 0 {
		return value, ""
	}
	if end <= start {
		base := strings.TrimSpace(value[:start])
		rationale := strings.TrimSpace(value[start+3:])
		return base, rationale
	}
	base := strings.TrimSpace(value[:start] + value[end+1:])
	rationale := strings.TrimSpace(value[start+3 : end])
	return base, rationale
}

func functionFuzzParameterBlockers(params []FunctionFuzzParamStrategy) []string {
	out := []string{}
	for _, item := range params {
		switch item.Class {
		case "object", "container", "handle":
			out = append(out, fmt.Sprintf("%s (%s)", valueOrUnset(item.Name), valueOrUnset(item.RawType)))
		case "pointer":
			if !strings.Contains(strings.ToLower(strings.TrimSpace(item.RawType)), "char") &&
				!strings.Contains(strings.ToLower(strings.TrimSpace(item.RawType)), "byte") &&
				!strings.Contains(strings.ToLower(strings.TrimSpace(item.RawType)), "uint8_t") {
				out = append(out, fmt.Sprintf("%s (%s)", valueOrUnset(item.Name), valueOrUnset(item.RawType)))
			}
		}
	}
	return uniqueStrings(out)
}

func functionFuzzNativeExecutionDetailLinesWithConfig(cfg Config, run FunctionFuzzRun) []string {
	lines := []string{}
	status := functionFuzzFriendlyExecutionStatusWithConfig(cfg, run.Execution.Status)
	if strings.TrimSpace(run.Execution.Status) == "" {
		lines = append(lines, functionFuzzLocalizedText(cfg, "Native auto-run was not scheduled.", "네이티브 자동 실행은 예약되지 않았습니다."))
		return lines
	}
	lines = append(lines, functionFuzzLocalizedText(cfg, "Native auto-run status: ", "네이티브 자동 실행 상태: ")+status)
	if strings.TrimSpace(run.Execution.Reason) != "" {
		lines = append(lines, functionFuzzLocalizedText(cfg, "Current reason: ", "현재 사유: ")+compactPersistentMemoryText(run.Execution.Reason, 180))
	}
	blockers := functionFuzzParameterBlockers(run.ParameterStrategies)
	if len(blockers) > 0 && !run.HarnessReady {
		lines = append(lines, functionFuzzLocalizedText(cfg, "Inputs that still need builders or fixtures: ", "아직 builder나 fixture가 필요한 입력: ")+strings.Join(limitStrings(blockers, 4), ", "))
	}
	target := SymbolRecord{
		Name:      run.TargetSymbolName,
		File:      run.TargetFile,
		Signature: run.TargetSignature,
	}
	if functionFuzzLooksLikeEntryRoot(target) {
		lines = append(lines, functionFuzzLocalizedText(cfg, "Reason this target is hard to auto-run: it is an entry point, so the call path expects initialization state before the interesting validation logic.", "이 타깃의 자동 실행이 어려운 이유: 진입점 성격이라 흥미로운 검증 로직보다 먼저 초기화 상태를 요구하는 호출 경로가 많기 때문입니다."))
	}
	if !run.HarnessReady {
		lines = append(lines, functionFuzzLocalizedText(cfg, "Source-only virtual analysis is still valid here; only native executable fuzzing is blocked.", "여기서는 소스 전용 가상 분석은 여전히 유효하고, 네이티브 실행형 fuzzing만 막혀 있습니다."))
		lines = append(lines, functionFuzzLocalizedText(cfg, "Fastest improvement path: choose a deeper parser, validator, dispatch helper, or buffer-processing function from the reachable closure so Kernforge can synthesize tighter virtual inputs.", "가장 빠른 개선 경로: reachable closure 안의 더 안쪽 parser, validator, dispatch helper, buffer-processing 함수를 선택하면 Kernforge가 더 정밀한 가상 입력을 합성할 수 있습니다."))
		lines = append(lines, functionFuzzLocalizedText(cfg, "Optional native follow-up only: keep this root and add project-specific builders later if you eventually want executable fuzzing.", "선택적 네이티브 후속 단계: 나중에 실행형 fuzzing이 필요해지면 이 루트를 유지한 채 프로젝트 전용 builder를 추가하면 됩니다."))
	}
	if strings.EqualFold(strings.TrimSpace(run.Execution.Status), "pending_confirmation") && strings.TrimSpace(run.Execution.ContinueCommand) != "" {
		lines = append(lines, functionFuzzLocalizedText(cfg, "Continue command: ", "계속 명령: ")+run.Execution.ContinueCommand)
	}
	return uniqueStrings(lines)
}

func functionFuzzNativeExecutionDetailLines(run FunctionFuzzRun) []string {
	return functionFuzzNativeExecutionDetailLinesWithConfig(functionFuzzEnglishConfig(), run)
}

func functionFuzzParameterDisposition(cfg Config, item FunctionFuzzParamStrategy) string {
	switch item.Class {
	case "object", "container", "handle":
		return functionFuzzLocalizedText(cfg, "needs a project-specific builder or fixture before native auto-run", "네이티브 자동 실행 전 프로젝트 전용 builder나 fixture가 필요함")
	case "opaque":
		return functionFuzzLocalizedText(cfg, "Kernforge will still do source-only byte partitioning here, but native auto-run would need a project-aware adapter", "여기서는 소스 전용 바이트 분할 분석은 가능하지만, 네이티브 자동 실행에는 프로젝트 인지형 adapter가 필요함")
	case "pointer":
		return functionFuzzLocalizedText(cfg, "can be modeled virtually, but native auto-run may need backing storage setup", "가상 모델링은 가능하지만 네이티브 자동 실행에는 backing storage 준비가 필요할 수 있음")
	case "boolean", "scalar_float", "buffer", "length", "string", "enum_or_flags", "scalar_int":
		return functionFuzzLocalizedText(cfg, "good direct mutation target for source-level fuzzing", "소스 레벨 fuzzing에서 직접 변이하기 좋은 타깃")
	default:
		return ""
	}
}

func functionFuzzInvariantText(cfg Config, item FunctionFuzzInvariant) string {
	left := strings.TrimSpace(item.Left)
	right := strings.TrimSpace(item.Right)
	switch strings.TrimSpace(item.Kind) {
	case "guard_use_size_equivalence":
		if right != "" && !strings.EqualFold(left, right) {
			if functionFuzzPrefersKorean(cfg) {
				return fmt.Sprintf("검증 시점의 크기 경로 %s 와 이후 사용 시점의 크기 경로 %s 는 같은 실행 경로에서 동일하게 유지되어야 합니다", left, right)
			}
			return fmt.Sprintf("Validation-time size path %s and later-use size path %s must stay equivalent across the same execution path", left, right)
		}
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("크기 경로 %s 는 검증부터 이후 사용까지 의미와 값이 바뀌지 않아야 합니다", left)
		}
		return fmt.Sprintf("Size-like path %s must not change meaning or value between validation and later use", left)
	case "buffer_size_contract":
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("%s 가 가리키는 실제 backing store 는 선언된 크기 경로 %s 이상을 끝까지 만족해야 합니다", left, right)
		}
		return fmt.Sprintf("Backing store reached via %s must remain at least as large as declared size path %s", left, right)
	case "pointer_state_coupling":
		if right != "" {
			if functionFuzzPrefersKorean(cfg) {
				return fmt.Sprintf("포인터 또는 상태 경로 %s 는 관련 크기 또는 모드 경로 %s 가 유효하다고 간주되는 동안 계속 안정적이어야 합니다", left, right)
			}
			return fmt.Sprintf("Pointer/state path %s must stay valid whenever related size or mode path %s is still considered usable", left, right)
		}
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("포인터 또는 상태 경로 %s 는 이후 side effect가 끝날 때까지 계속 유효해야 합니다", left)
		}
		return fmt.Sprintf("Pointer/state path %s must remain valid through every later side effect on the same path", left)
	case "dispatch_selector_revalidation":
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("선택자 경로 %s 는 dispatch 와 unwind 판단 뒤에도 검증된 handler로만 이어져야 합니다", left)
		}
		return fmt.Sprintf("Selector path %s must continue to map to a validated handler after every dispatch and unwind decision", left)
	case "allocation_use_size_equivalence":
		if right != "" && !strings.EqualFold(left, right) {
			if functionFuzzPrefersKorean(cfg) {
				return fmt.Sprintf("할당 시점 크기 경로 %s 와 이후 사용 시점 크기 경로 %s 는 끝까지 일치해야 합니다", left, right)
			}
			return fmt.Sprintf("Allocation-time size path %s and later-use size path %s must remain consistent", left, right)
		}
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("할당에 쓰인 크기 경로 %s 는 이후 사용에서도 같은 값으로 유지되어야 합니다", left)
		}
		return fmt.Sprintf("Allocation-time size path %s must stay consistent with later use", left)
	case "publish_before_cleanup":
		if functionFuzzPrefersKorean(cfg) {
			return "보안 관련 상태는 최종 검증이 끝나기 전에 외부에서 닿는 형태로 남아 있으면 안 됩니다"
		}
		return "Security-relevant state must not remain externally reachable if a later check unwinds through cleanup"
	default:
		if right != "" {
			return firstNonBlankString(strings.TrimSpace(item.Detail), left+" == "+right)
		}
		return firstNonBlankString(strings.TrimSpace(item.Detail), left)
	}
}

func functionFuzzDriftExampleText(cfg Config, token string) string {
	kind, parts, ok := functionFuzzParseDriftToken(token)
	if !ok {
		return functionFuzzDisplayText(cfg, token)
	}
	left := ""
	right := ""
	if len(parts) > 0 {
		left = strings.TrimSpace(parts[0])
	}
	if len(parts) > 1 {
		right = strings.TrimSpace(parts[1])
	}
	switch kind {
	case "guard_use_size":
		if right != "" && !strings.EqualFold(left, right) {
			if functionFuzzPrefersKorean(cfg) {
				return fmt.Sprintf("검증 시 %s = 0x20 이었지만 이후 사용 시 %s = 0x1000 으로 벌어질 수 있습니다", left, right)
			}
			return fmt.Sprintf("Validation read saw %s = 0x20, later use consumed %s = 0x1000", left, right)
		}
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("%s 가 검증 단계에서는 0x20 이었지만 이후 재조회에서는 0x1000 으로 달라질 수 있습니다", left)
		}
		return fmt.Sprintf("%s was 0x20 during validation but a later read treated it as 0x1000", left)
	case "buffer_size_contract":
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("%s 가 가리키는 실제 backing store 는 1바이트뿐인데 %s = 0x1000 으로 선언될 수 있습니다", left, right)
		}
		return fmt.Sprintf("%s pointed to only 1 byte of backing data while %s = 0x1000", left, right)
	case "pointer_state":
		if right != "" {
			if functionFuzzPrefersKorean(cfg) {
				return fmt.Sprintf("guard 는 %s != NULL 로 통과했지만 이후 사용 시 %s = NULL 이고 %s = 0x20 인 상태가 될 수 있습니다", left, left, right)
			}
			return fmt.Sprintf("Guard accepted %s != NULL, later use saw %s = NULL while %s = 0x20", left, left, right)
		}
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("guard 는 %s 를 유효하다고 봤지만 이후 side effect 시점에는 stale 또는 NULL 이 될 수 있습니다", left)
		}
		return fmt.Sprintf("Guard accepted %s as valid, but a later side effect saw it stale or NULL", left)
	case "selector_dispatch":
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("%s = 0xffffffff 같은 값이 검증 재확인 전에 드문 dispatch 분기로 밀어 넣을 수 있습니다", left)
		}
		return fmt.Sprintf("%s = 0xffffffff can reach a rare branch before validation and cleanup assumptions realign", left)
	case "allocation_use":
		if right != "" && !strings.EqualFold(left, right) {
			if functionFuzzPrefersKorean(cfg) {
				return fmt.Sprintf("할당은 %s = 0x20 으로 이뤄졌지만 이후 copy 또는 parse 는 %s = 0x1000 으로 진행될 수 있습니다", left, right)
			}
			return fmt.Sprintf("Allocation used %s = 0x20, later copy or parse used %s = 0x1000", left, right)
		}
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("%s 는 할당 단계에서는 0x20 이었지만 이후 사용 단계에서는 0x1000 으로 해석될 수 있습니다", left)
		}
		return fmt.Sprintf("%s sized allocation at 0x20, but later use treated it as 0x1000", left)
	case "publish_cleanup":
		if functionFuzzPrefersKorean(cfg) {
			return "등록 또는 공개 단계는 성공했지만 이후 실패가 cleanup 으로 unwind되면서 일부 상태가 노출된 채 남을 수 있습니다"
		}
		return "A publish or register step succeeded, then a later failure unwound through cleanup with partially exposed state"
	default:
		return functionFuzzDisplayText(cfg, token)
	}
}

func functionFuzzInvertComparisonOperator(op string) string {
	switch strings.TrimSpace(op) {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	default:
		return strings.TrimSpace(op)
	}
}

func functionFuzzComparisonFactText(cfg Config, token string) string {
	left, op, right, ok := functionFuzzParseComparisonToken(token)
	if !ok {
		return functionFuzzDisplayText(cfg, token)
	}
	return strings.TrimSpace(left) + " " + strings.TrimSpace(op) + " " + strings.TrimSpace(right)
}

func functionFuzzComparisonCounterexampleText(cfg Config, token string) string {
	left, op, right, ok := functionFuzzParseComparisonToken(token)
	if !ok {
		return functionFuzzDisplayText(cfg, token)
	}
	variable := left
	constant := right
	effectiveOp := op
	if functionFuzzOperandLooksConstant(left) && !functionFuzzOperandLooksConstant(right) {
		variable = right
		constant = left
		effectiveOp = functionFuzzInvertComparisonOperator(op)
	}
	lowerVariable := strings.ToLower(strings.TrimSpace(variable))
	lowerConstant := strings.ToLower(strings.TrimSpace(constant))
	switch effectiveOp {
	case ">":
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("%s = %s + 1", variable, constant)
		}
		return fmt.Sprintf("%s = %s + 1", variable, constant)
	case ">=":
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("%s = %s", variable, constant)
		}
		return fmt.Sprintf("%s = %s", variable, constant)
	case "<":
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("%s = %s", variable, constant)
		}
		return fmt.Sprintf("%s = %s", variable, constant)
	case "<=":
		if functionFuzzPrefersKorean(cfg) {
			return fmt.Sprintf("%s = %s + 1", variable, constant)
		}
		return fmt.Sprintf("%s = %s + 1", variable, constant)
	case "==":
		switch {
		case lowerConstant == "null" || lowerConstant == "nullptr":
			if functionFuzzPrefersKorean(cfg) {
				return fmt.Sprintf("%s = 0xDEADBEEF", variable)
			}
			return fmt.Sprintf("%s = 0xDEADBEEF", variable)
		case lowerConstant == "invalid_handle_value":
			if functionFuzzPrefersKorean(cfg) {
				return fmt.Sprintf("%s = 0x1", variable)
			}
			return fmt.Sprintf("%s = 0x1", variable)
		case functionFuzzLooksLikeSelectorAccessPath(lowerVariable):
			if functionFuzzPrefersKorean(cfg) {
				return fmt.Sprintf("%s = 0xffffffff", variable)
			}
			return fmt.Sprintf("%s = 0xffffffff", variable)
		default:
			if functionFuzzPrefersKorean(cfg) {
				return fmt.Sprintf("%s = %s + 1", variable, constant)
			}
			return fmt.Sprintf("%s = %s + 1", variable, constant)
		}
	case "!=":
		switch {
		case lowerConstant == "null" || lowerConstant == "nullptr":
			return fmt.Sprintf("%s = NULL", variable)
		case lowerConstant == "invalid_handle_value":
			return fmt.Sprintf("%s = INVALID_HANDLE_VALUE", variable)
		default:
			return fmt.Sprintf("%s = %s", variable, constant)
		}
	default:
		return functionFuzzComparisonFactText(cfg, token)
	}
}

func functionFuzzBranchOutcomeSideLabel(cfg Config, side string) string {
	switch strings.TrimSpace(side) {
	case "true":
		return functionFuzzLocalizedText(cfg, "if the predicate evaluates true", "조건이 참이면")
	case "false":
		return functionFuzzLocalizedText(cfg, "if the predicate evaluates false", "조건이 거짓이면")
	default:
		return functionFuzzLocalizedText(cfg, "representative branch outcome", "대표 분기 결과")
	}
}

func functionFuzzBranchOutcomeEffectText(cfg Config, item FunctionFuzzBranchOutcome) string {
	location := ""
	if item.Line > 0 {
		if functionFuzzPrefersKorean(cfg) {
			location = fmt.Sprintf("%d번 줄에서 ", item.Line)
		} else {
			location = fmt.Sprintf("at line %d ", item.Line)
		}
	}
	evidence := strings.TrimSpace(item.Evidence)
	callChain := uniqueStrings(item.DownstreamCalls)
	appendCallChain := func(text string) string {
		if len(callChain) == 0 {
			return text
		}
		if functionFuzzPrefersKorean(cfg) {
			return text + "; 대표 후속 호출: " + strings.Join(callChain, " -> ")
		}
		return text + "; representative downstream calls: " + strings.Join(callChain, " -> ")
	}
	switch strings.TrimSpace(item.EffectKind) {
	case "reject":
		if functionFuzzPrefersKorean(cfg) {
			return appendCallChain(location + "reject 또는 status 설정 경로로 빠집니다: " + evidence)
		}
		return appendCallChain(location + "the path rejects or sets an error status: " + evidence)
	case "cleanup":
		if functionFuzzPrefersKorean(cfg) {
			return appendCallChain(location + "cleanup 또는 early-exit 경로로 빠집니다: " + evidence)
		}
		return appendCallChain(location + "the path unwinds into cleanup or early-exit: " + evidence)
	case "copy":
		if functionFuzzPrefersKorean(cfg) {
			return appendCallChain(location + "실제 copy sink까지 도달합니다: " + evidence)
		}
		return appendCallChain(location + "the path reaches a real copy sink: " + evidence)
	case "probe":
		if functionFuzzPrefersKorean(cfg) {
			return appendCallChain(location + "실제 probe 경로로 진행합니다: " + evidence)
		}
		return appendCallChain(location + "the path reaches a real probe site: " + evidence)
	case "publish":
		if functionFuzzPrefersKorean(cfg) {
			return appendCallChain(location + "상태 공개 또는 등록 경로로 진행합니다: " + evidence)
		}
		return appendCallChain(location + "the path reaches a publish or register side effect: " + evidence)
	case "alloc":
		if functionFuzzPrefersKorean(cfg) {
			return appendCallChain(location + "할당 또는 크기 기반 준비 경로로 진행합니다: " + evidence)
		}
		return appendCallChain(location + "the path reaches allocation or size-dependent setup: " + evidence)
	case "dispatch":
		if functionFuzzPrefersKorean(cfg) {
			return appendCallChain(location + "실제 dispatch 경로로 들어갑니다: " + evidence)
		}
		return appendCallChain(location + "the path enters a real dispatch branch: " + evidence)
	default:
		if functionFuzzPrefersKorean(cfg) {
			return appendCallChain(location + "다음 의미 있는 경로로 이어집니다: " + evidence)
		}
		return appendCallChain(location + "the path continues into the next meaningful operation: " + evidence)
	}
}

func functionFuzzScenarioSummary(cfg Config, item FunctionFuzzVirtualScenario) []string {
	lines := []string{}
	if len(item.Inputs) > 0 {
		inputs := limitStrings(item.Inputs, 3)
		for i := range inputs {
			inputs[i] = functionFuzzDisplayText(cfg, inputs[i])
		}
		lines = append(lines, functionFuzzLocalizedText(cfg, "Kernforge's internal hypothetical input state: ", "Kernforge가 내부적으로 가정한 입력 상태: ")+strings.Join(inputs, "; "))
	}
	if len(item.ConcreteInputs) > 0 {
		examples := limitStrings(item.ConcreteInputs, 2)
		for i := range examples {
			examples[i] = functionFuzzDisplayText(cfg, examples[i])
		}
		lines = append(lines, functionFuzzLocalizedText(cfg, "Hypothetical concrete input examples: ", "가상의 구체 입력 예시: ")+strings.Join(examples, "; "))
	}
	if len(item.Invariants) > 0 {
		invariants := item.Invariants
		if len(invariants) > 2 {
			invariants = invariants[:2]
		}
		texts := make([]string, 0, len(invariants))
		for _, invariant := range invariants {
			text := strings.TrimSpace(functionFuzzInvariantText(cfg, invariant))
			if text != "" {
				texts = append(texts, text)
			}
		}
		if len(texts) > 0 {
			lines = append(lines, functionFuzzLocalizedText(cfg, "Key invariant attackers try to break: ", "깨야 할 핵심 불변식: ")+strings.Join(texts, "; "))
		}
	}
	if len(item.BranchFacts) > 0 {
		branchFacts := limitStrings(item.BranchFacts, 2)
		conditions := make([]string, 0, len(branchFacts))
		counterexamples := make([]string, 0, len(branchFacts))
		for _, fact := range branchFacts {
			if text := strings.TrimSpace(functionFuzzComparisonFactText(cfg, fact)); text != "" {
				conditions = append(conditions, text)
			}
			if text := strings.TrimSpace(functionFuzzComparisonCounterexampleText(cfg, fact)); text != "" {
				counterexamples = append(counterexamples, text)
			}
		}
		if len(conditions) > 0 {
			lines = append(lines, functionFuzzLocalizedText(cfg, "Source-derived branch predicates: ", "소스에서 뽑은 비교식: ")+strings.Join(conditions, "; "))
		}
		if len(counterexamples) > 0 {
			lines = append(lines, functionFuzzLocalizedText(cfg, "Minimal counterexample inputs for those predicates: ", "이 비교식을 깨는 최소 반례 예시: ")+strings.Join(counterexamples, "; "))
		}
	}
	if len(item.BranchOutcomes) > 0 {
		outcomes := item.BranchOutcomes
		if len(outcomes) > 2 {
			outcomes = outcomes[:2]
		}
		texts := make([]string, 0, len(outcomes))
		for _, outcome := range outcomes {
			text := strings.TrimSpace(functionFuzzBranchOutcomeSideLabel(cfg, outcome.Side) + " " + functionFuzzBranchOutcomeEffectText(cfg, outcome))
			if text != "" {
				texts = append(texts, text)
			}
		}
		if len(texts) > 0 {
			lines = append(lines, functionFuzzLocalizedText(cfg, "Representative pass/fail consequences from that branch: ", "그 분기 뒤에 이어지는 대표 흐름: ")+strings.Join(texts, "; "))
		}
	}
	if len(item.DriftExamples) > 0 {
		examples := limitStrings(item.DriftExamples, 2)
		texts := make([]string, 0, len(examples))
		for _, example := range examples {
			text := strings.TrimSpace(functionFuzzDriftExampleText(cfg, example))
			if text != "" {
				texts = append(texts, text)
			}
		}
		if len(texts) > 0 {
			lines = append(lines, functionFuzzLocalizedText(cfg, "Concrete read-to-use drift examples: ", "공격자가 흔드는 전후 값 예시: ")+strings.Join(texts, "; "))
		}
	}
	if strings.TrimSpace(item.ExpectedFlow) != "" {
		lines = append(lines, functionFuzzLocalizedText(cfg, "What the code will likely do: ", "코드가 할 가능성이 높은 일: ")+functionFuzzDisplayText(cfg, strings.TrimSpace(item.ExpectedFlow)))
	}
	if len(item.LikelyIssues) > 0 {
		issues := limitStrings(item.LikelyIssues, 3)
		for i := range issues {
			issues[i] = functionFuzzDisplayText(cfg, issues[i])
		}
		lines = append(lines, functionFuzzLocalizedText(cfg, "What can go wrong: ", "어떤 문제가 생길 수 있는지: ")+strings.Join(issues, "; "))
	}
	if len(item.ScopeFilePath) > 0 {
		lines = append(lines, functionFuzzLocalizedText(cfg, "How Kernforge reached this source file from the selected starting file: ", "선택한 시작 파일에서 이 소스 파일로 이어진 경로: ")+strings.Join(item.ScopeFilePath, " -> "))
	}
	if len(item.PathSketch) > 0 {
		label := functionFuzzLocalizedText(cfg, "Most relevant internal path: ", "가장 관련 있는 내부 경로: ")
		if len(item.ScopeFilePath) > 0 {
			label = functionFuzzLocalizedText(cfg, "Representative call path from the chosen root into this implementation: ", "선택한 대표 루트에서 이 구현까지 이어진 호출 경로: ")
		}
		lines = append(lines, label+strings.Join(item.PathSketch, " -> "))
	}
	if strings.TrimSpace(item.PathHint) != "" {
		lines = append(lines, functionFuzzLocalizedText(cfg, "Why Kernforge focused there: ", "Kernforge가 여기에 주목한 이유: ")+functionFuzzDisplayText(cfg, strings.TrimSpace(item.PathHint)))
	}
	return lines
}

func functionFuzzRenderScenarioExcerptTerminal(cfg Config, b *strings.Builder, item FunctionFuzzVirtualScenario) {
	excerpt := item.SourceExcerpt
	if strings.TrimSpace(excerpt.File) == "" || len(excerpt.Snippet) == 0 {
		return
	}
	location := excerpt.File
	if excerpt.StartLine > 0 {
		location += ":" + strconv.Itoa(excerpt.StartLine)
		if excerpt.EndLine > excerpt.StartLine {
			location += "-" + strconv.Itoa(excerpt.EndLine)
		}
	}
	functionFuzzWriteWrappedText(b, "  ", "  ", functionFuzzLocalizedText(cfg, "Relevant source to inspect first: ", "먼저 볼 관련 소스: ")+location, 112)
	for index, line := range excerpt.Snippet {
		lineNo := excerpt.StartLine + index
		marker := " "
		if lineNo == excerpt.FocusLine && excerpt.FocusLine > 0 {
			marker = ">"
		}
		fmt.Fprintf(b, "    %s %4d | %s\n", marker, lineNo, line)
	}
}

func functionFuzzCodeFenceLanguage(path string) string {
	lower := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	switch lower {
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx", ".inl":
		return "cpp"
	case ".go":
		return "go"
	case ".cs":
		return "csharp"
	case ".py":
		return "python"
	default:
		return "text"
	}
}

func functionFuzzRenderScenarioExcerptMarkdown(cfg Config, b *strings.Builder, item FunctionFuzzVirtualScenario) {
	excerpt := item.SourceExcerpt
	if strings.TrimSpace(excerpt.File) == "" || len(excerpt.Snippet) == 0 {
		return
	}
	location := excerpt.File
	if excerpt.StartLine > 0 {
		location += ":" + strconv.Itoa(excerpt.StartLine)
		if excerpt.EndLine > excerpt.StartLine {
			location += "-" + strconv.Itoa(excerpt.EndLine)
		}
	}
	b.WriteString("- " + functionFuzzLocalizedText(cfg, "Relevant source to inspect first", "먼저 볼 관련 소스") + ": `" + location + "`\n")
	b.WriteString("```" + functionFuzzCodeFenceLanguage(excerpt.File) + "\n")
	for _, line := range excerpt.Snippet {
		b.WriteString(line + "\n")
	}
	b.WriteString("```\n")
}

func functionFuzzNormalizeDisplayText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func functionFuzzDisplayText(cfg Config, text string) string {
	text = strings.TrimSpace(text)
	if text == "" || !functionFuzzPrefersKorean(cfg) {
		return text
	}
	switch text {
	case "completed":
		return "완료"
	case "incomplete":
		return "미완료"
	case "not available":
		return "없음"
	case "Null, stale, or partially initialized state object":
		return "null, stale, 또는 부분 초기화 상태 객체"
	case "Short backing store with oversized length":
		return "짧은 backing store와 과도한 length 조합"
	case "Empty, malformed, or length-inconsistent string/path":
		return "비어 있거나 malformed이거나 length가 불일치하는 문자열/경로"
	case "Unexpected flags, control codes, or magic constants":
		return "예상 밖 flags, control code, magic constant"
	case "Entry-point partial initialization rollback":
		return "진입점 부분 초기화 rollback"
	case "Repeated initialization, reentry, or teardown ordering confusion":
		return "반복 초기화, 재진입, teardown 순서 혼란"
	case "Buffer and scalar size arguments appear semantically related but validate separately":
		return "버퍼와 크기 인자가 의미상 연결돼 보이지만 검증은 분리됨"
	case "Implicit prerequisite state missing before a parameterless call":
		return "파라미터 없는 호출 전에 필요한 선행 상태가 암묵적으로 누락됨"
	case "Generic edge-case state and control-flow divergence":
		return "일반적인 edge-case 상태와 제어 흐름 분기"
	case "high":
		return "높음"
	case "medium":
		return "중간"
	case "low":
		return "낮음"
	default:
		if translated := functionFuzzTranslateDisplayPatternKorean(text); translated != "" {
			return translated
		}
		return text
	}
}

func functionFuzzTranslateDisplayPatternKorean(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if translated, ok := functionFuzzKoreanDisplayTextMap[text]; ok {
		return translated
	}
	if strings.Contains(text, ";") {
		parts := strings.Split(text, ";")
		out := make([]string, 0, len(parts))
		changed := false
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}
			translated := functionFuzzTranslateDisplayPatternKorean(trimmed)
			if translated == "" {
				translated = trimmed
			} else if translated != trimmed {
				changed = true
			}
			out = append(out, translated)
		}
		if changed && len(out) > 0 {
			return strings.Join(out, "; ")
		}
	}
	if match := functionFuzzGroundedObservationPattern.FindStringSubmatch(text); len(match) == 2 {
		return fmt.Sprintf("소스 기반 가드 또는 sink 관찰 %s개에 근거함", match[1])
	}
	if match := functionFuzzGroundedObservationSummaryPattern.FindStringSubmatch(text); len(match) == 2 {
		return fmt.Sprintf("소스 기반 가드/sink 관찰 %s개에 근거함", match[1])
	}
	if match := functionFuzzInputStatePattern.FindStringSubmatch(text); len(match) == 4 {
		name := strings.TrimSpace(match[1])
		rawType := strings.TrimSpace(match[2])
		value := strings.TrimSpace(match[3])
		switch value {
		case "short-header, overlapping, and oversized-declared variants":
			return fmt.Sprintf("%s (%s) = 짧은 헤더, 겹침, 선언 크기 과대 변형", name, rawType)
		case "empty, short-header, overlapping, stale, and oversized-declared variants":
			return fmt.Sprintf("%s (%s) = 비어 있음, 짧은 헤더, 겹침, stale, 선언 크기 과대 변형", name, rawType)
		case "null, stale, or invalid synthetic value":
			return fmt.Sprintf("%s (%s) = null, stale, 또는 유효하지 않은 합성 값", name, rawType)
		case "non-zero, exact-boundary, and wrapped values":
			return fmt.Sprintf("%s (%s) = non-zero, 정확한 경계값, wrap된 값", name, rawType)
		case "backing bytes that force a later copy or parse to consume more than the allocated size":
			return fmt.Sprintf("%s (%s) = 이후 copy 또는 parse가 할당 크기보다 더 많이 소비하도록 만드는 backing bytes", name, rawType)
		case "zero, exact-boundary, large, and wrapped values":
			return fmt.Sprintf("%s (%s) = zero, 정확한 경계값, 큰 값, wrap된 값", name, rawType)
		case "contradictory edge-case state":
			return fmt.Sprintf("%s (%s) = 서로 모순된 edge-case 상태", name, rawType)
		}
	}
	switch text {
	case "Selector or control value = unsupported, colliding, max-value, and sparse-bit variants":
		return "선택자 또는 제어값 = 지원되지 않는 값, 충돌하는 값, 최대값, sparse-bit 변형"
	case "Attacker-controlled input = short, exact-boundary, overlapping, and oversized-declared variants":
		return "공격자 제어 입력 = 짧은 값, 정확한 경계값, 겹치는 값, 선언 크기 과대 변형"
	case "Ambient state and hidden prerequisites = missing, stale, partially initialized, or already torn down":
		return "주변 상태와 숨은 선행조건 = 누락됨, stale, 부분 초기화됨, 또는 이미 teardown됨"
	case "Attacker-controlled size fields = values that make allocation sizing smaller than later use sizing":
		return "공격자 제어 크기 필드 = 할당 크기보다 이후 사용 크기를 더 크게 만드는 값"
	case "One attacker-controlled check passes far enough to publish, register, open, or expose state, and a later check fails":
		return "공격자 제어 검사 하나가 상태 공개, 등록, open, 노출까지 통과한 뒤 나중 검사에서 실패함"
	case "Pointer-backed state = NULL, stale, freed, or partially initialized while related sizes or mode bits still look valid":
		return "포인터 기반 상태 = 관련 크기나 mode bit가 유효해 보여도 NULL, stale, freed, 또는 부분 초기화 상태"
	default:
		return ""
	}
}

func functionFuzzWrapText(text string, width int) []string {
	text = functionFuzzNormalizeDisplayText(text)
	if text == "" {
		return nil
	}
	if width <= 0 {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	lines := []string{words[0]}
	for _, word := range words[1:] {
		current := lines[len(lines)-1]
		candidate := current + " " + word
		if visibleLen(candidate) <= width {
			lines[len(lines)-1] = candidate
			continue
		}
		lines = append(lines, word)
	}
	return lines
}

func functionFuzzWriteWrappedText(b *strings.Builder, firstPrefix string, continuationPrefix string, text string, width int) {
	lines := functionFuzzWrapText(text, width)
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "%s%s\n", firstPrefix, lines[0])
	for _, line := range lines[1:] {
		fmt.Fprintf(b, "%s%s\n", continuationPrefix, line)
	}
}

func functionFuzzSignalDisplayScore(item FunctionFuzzSinkSignal, overlays []string) int {
	score := 0
	switch strings.TrimSpace(item.Kind) {
	case "overlay":
		score += 40
	case "copy_like", "compare_like", "parse_like":
		score += 24
	case "alloc_like":
		score += 12
	}
	name := strings.ToLower(strings.TrimSpace(item.Name))
	if functionFuzzLooksTrivialLeafName(name) {
		score -= 18
	}
	corpus := strings.ToLower(strings.Join([]string{item.Name, item.File, item.Reason}, " "))
	if containsAny(corpus, "/external/", "/third_party/", "/vendor/", "/include/aws/", "/tinyxml") {
		score -= 24
	}
	for _, overlay := range overlays {
		if strings.EqualFold(strings.TrimSpace(item.Name), strings.TrimSpace(overlay)) {
			score += 14
		}
	}
	return score
}

func functionFuzzTopSignals(run FunctionFuzzRun, limit int) []FunctionFuzzSinkSignal {
	if limit <= 0 || len(run.SinkSignals) == 0 {
		return nil
	}
	items := append([]FunctionFuzzSinkSignal(nil), run.SinkSignals...)
	sort.SliceStable(items, func(i int, j int) bool {
		left := functionFuzzSignalDisplayScore(items[i], run.OverlayDomains)
		right := functionFuzzSignalDisplayScore(items[j], run.OverlayDomains)
		if left == right {
			return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
		}
		return left > right
	})
	out := []FunctionFuzzSinkSignal{}
	for _, item := range items {
		if functionFuzzSignalDisplayScore(item, run.OverlayDomains) <= 0 {
			continue
		}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func functionFuzzFriendlyOverlayNameWithConfig(cfg Config, domain string) string {
	switch strings.ToLower(strings.TrimSpace(domain)) {
	case "handle_surface":
		return functionFuzzLocalizedText(cfg, "handle surface", "핸들 표면")
	case "ioctl_surface":
		return functionFuzzLocalizedText(cfg, "IOCTL or control-dispatch surface", "IOCTL 또는 제어 dispatch 표면")
	case "memory_surface":
		return functionFuzzLocalizedText(cfg, "memory-copy or size-sensitive surface", "메모리 복사 또는 크기 민감 표면")
	case "security_boundary":
		return functionFuzzLocalizedText(cfg, "security or trust boundary", "보안 또는 신뢰 경계")
	case "rpc_surface":
		return functionFuzzLocalizedText(cfg, "RPC or IPC surface", "RPC 또는 IPC 표면")
	default:
		domain = strings.ReplaceAll(strings.TrimSpace(domain), "_", " ")
		if domain == "" {
			return functionFuzzLocalizedText(cfg, "exposed surface", "노출 표면")
		}
		return domain
	}
}

func functionFuzzFriendlyOverlayName(domain string) string {
	return functionFuzzFriendlyOverlayNameWithConfig(functionFuzzEnglishConfig(), domain)
}

func functionFuzzOverlayMeaningWithConfig(cfg Config, domain string) string {
	switch strings.ToLower(strings.TrimSpace(domain)) {
	case "handle_surface":
		return functionFuzzLocalizedText(cfg, "reachable code opens, stores, validates, or reuses handle-like objects whose lifetime or ownership can drift", "도달 가능한 코드가 수명이나 소유권이 어긋날 수 있는 handle류 객체를 열고, 저장하고, 검증하거나 재사용합니다")
	case "ioctl_surface":
		return functionFuzzLocalizedText(cfg, "reachable code makes dispatch decisions from control codes or request buffers that are directly attacker-shaped", "도달 가능한 코드가 공격자 영향이 직접 들어가는 control code나 request buffer를 기준으로 dispatch를 결정합니다")
	case "memory_surface":
		return functionFuzzLocalizedText(cfg, "reachable code reaches copy, allocation, or length-sensitive memory operations where size drift matters", "도달 가능한 코드가 copy, allocation, 길이 민감 메모리 연산에 도달해 size drift가 중요해집니다")
	case "security_boundary":
		return functionFuzzLocalizedText(cfg, "reachable code crosses trust, privilege, identity, or authorization checks that can be bypassed by unusual state", "도달 가능한 코드가 비정상 상태로 우회될 수 있는 trust, privilege, identity, authorization 검사를 지납니다")
	case "rpc_surface":
		return functionFuzzLocalizedText(cfg, "reachable code reaches RPC or IPC boundaries where layout and privilege assumptions can diverge", "도달 가능한 코드가 레이아웃과 privilege 가정이 어긋날 수 있는 RPC 또는 IPC 경계에 도달합니다")
	default:
		return functionFuzzLocalizedText(cfg, "reachable code crosses a boundary that deserves focused security triage", "도달 가능한 코드가 우선적으로 보안 triage해야 할 경계를 지납니다")
	}
}

func functionFuzzOverlayMeaning(domain string) string {
	return functionFuzzOverlayMeaningWithConfig(functionFuzzEnglishConfig(), domain)
}

func functionFuzzSignalMeaning(cfg Config, item FunctionFuzzSinkSignal) string {
	switch strings.TrimSpace(item.Kind) {
	case "overlay":
		return functionFuzzOverlayMeaningWithConfig(cfg, item.Name)
	case "copy_like":
		return functionFuzzLocalizedText(cfg, "a copy or move path is reachable here, so buffer length, aliasing, and overlap mistakes matter", "여기서는 copy 또는 move 경로에 도달하므로 buffer 길이, aliasing, overlap 문제가 중요합니다")
	case "parse_like":
		return functionFuzzLocalizedText(cfg, "a parser or decoder path is reachable here, so malformed layouts can desynchronize validation from use", "여기서는 parser 또는 decoder 경로에 도달하므로 malformed layout이 검증과 사용을 어긋나게 만들 수 있습니다")
	case "compare_like":
		return functionFuzzLocalizedText(cfg, "a validation-heavy path is reachable here, so unusual values may bypass, split, or confuse checks", "여기서는 검증 비중이 큰 경로에 도달하므로 이상 값이 검사를 우회하거나 분기시키거나 혼란시킬 수 있습니다")
	case "alloc_like":
		return functionFuzzLocalizedText(cfg, "an allocation-sensitive path is reachable here, so size drift and rollback bugs can surface", "여기서는 할당 민감 경로에 도달하므로 size drift와 rollback 버그가 드러날 수 있습니다")
	default:
		return functionFuzzLocalizedText(cfg, "this reachable code path looked security-relevant during source-only fuzz planning", "이 도달 경로는 소스 전용 fuzz 계획 과정에서 보안 관련성이 높게 보였습니다")
	}
}

func functionFuzzSignalObservedAt(item FunctionFuzzSinkSignal) string {
	name := strings.TrimSpace(item.Name)
	switch strings.TrimSpace(item.Kind) {
	case "overlay":
		return firstNonBlankString(strings.TrimSpace(item.Reason), name)
	default:
		if strings.TrimSpace(item.File) != "" {
			return fmt.Sprintf("%s (%s)", firstNonBlankString(name, "reachable symbol"), filepath.ToSlash(strings.TrimSpace(item.File)))
		}
		return firstNonBlankString(name, "reachable symbol")
	}
}

func functionFuzzSignalDetailLines(cfg Config, item FunctionFuzzSinkSignal) []string {
	lines := []string{}
	switch strings.TrimSpace(item.Kind) {
	case "overlay":
		lines = append(lines, functionFuzzLocalizedText(cfg, "surface: ", "표면: ")+functionFuzzFriendlyOverlayNameWithConfig(cfg, item.Name))
	default:
		lines = append(lines, functionFuzzLocalizedText(cfg, "signal type: ", "신호 유형: ")+functionFuzzFriendlySignalKindWithConfig(cfg, item.Kind))
	}
	lines = append(lines, functionFuzzLocalizedText(cfg, "observed at: ", "관측 위치: ")+functionFuzzSignalObservedAt(item))
	lines = append(lines, functionFuzzLocalizedText(cfg, "why it matters: ", "중요한 이유: ")+functionFuzzSignalMeaning(cfg, item))
	if strings.TrimSpace(item.File) != "" && !strings.EqualFold(strings.TrimSpace(item.Kind), "overlay") {
		lines = append(lines, functionFuzzLocalizedText(cfg, "file: ", "파일: ")+filepath.ToSlash(strings.TrimSpace(item.File)))
	}
	return uniqueStrings(lines)
}

func functionFuzzRenderSignalsTerminal(cfg Config, b *strings.Builder, run FunctionFuzzRun, width int) {
	if len(run.SinkSignals) == 0 {
		return
	}
	ui := NewUI()
	b.WriteString("\n" + functionFuzzRenderTerminalHeader(ui, "Signals", "warn") + "\n")
	functionFuzzWriteWrappedText(
		b,
		"- What these signals mean: ",
		"  ",
		functionFuzzLocalizedText(cfg, "These are the most security-relevant boundaries and code patterns reached from the selected root. They are prioritization clues, not confirmed vulnerabilities.", "여기 표시되는 항목은 선택한 루트에서 도달한 보안 민감 경계와 코드 패턴입니다. 우선순위 판단에 쓰는 단서이지, 확정 취약점은 아닙니다."),
		width,
	)
	if len(run.OverlayDomains) > 0 {
		fmt.Fprintf(b, "- %s\n", functionFuzzLocalizedText(cfg, "Exposed surfaces in this mapped call closure:", "이 call closure에서 노출된 표면:"))
		for _, domain := range limitStrings(run.OverlayDomains, 4) {
			label := functionFuzzFriendlyOverlayNameWithConfig(cfg, domain)
			functionFuzzWriteWrappedText(
				b,
				fmt.Sprintf("  - %s: ", label),
				"    ",
				functionFuzzOverlayMeaningWithConfig(cfg, domain),
				width,
			)
		}
	}
	top := functionFuzzTopSignals(run, 4)
	if len(top) > 0 {
		fmt.Fprintf(b, "- %s\n", functionFuzzLocalizedText(cfg, "Representative evidence from the mapped closure:", "이 mapped closure를 대표하는 근거:"))
		for _, item := range top {
			title := functionFuzzFriendlySignalKindWithConfig(cfg, item.Kind)
			if strings.TrimSpace(item.Kind) == "overlay" {
				title = functionFuzzFriendlyOverlayNameWithConfig(cfg, item.Name)
			}
			fmt.Fprintf(b, "  - %s\n", title)
			for _, detail := range functionFuzzSignalDetailLines(cfg, item) {
				functionFuzzWriteWrappedText(b, "    ", "    ", detail, width)
			}
		}
	}
}

func functionFuzzRenderSignalsMarkdown(cfg Config, run FunctionFuzzRun, limit int) string {
	if len(run.SinkSignals) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Signals\n\n")
	b.WriteString("- " + functionFuzzLocalizedText(cfg, "These are the most security-relevant boundaries and code patterns reached from the selected root. They are prioritization clues, not confirmed vulnerabilities.", "여기 표시되는 항목은 선택한 루트에서 도달한 보안 민감 경계와 코드 패턴입니다. 우선순위 판단에 쓰는 단서이지, 확정 취약점은 아닙니다.") + "\n")
	if len(run.OverlayDomains) > 0 {
		b.WriteString("- " + functionFuzzLocalizedText(cfg, "Exposed surfaces in this mapped call closure:", "이 call closure에서 노출된 표면:") + "\n")
		for _, domain := range limitStrings(run.OverlayDomains, 4) {
			fmt.Fprintf(&b, "  - %s: %s\n", functionFuzzFriendlyOverlayNameWithConfig(cfg, domain), functionFuzzOverlayMeaningWithConfig(cfg, domain))
		}
	}
	top := functionFuzzTopSignals(run, limit)
	if len(top) > 0 {
		b.WriteString("- " + functionFuzzLocalizedText(cfg, "Representative evidence from the mapped closure:", "이 mapped closure를 대표하는 근거:") + "\n")
		for _, item := range top {
			title := functionFuzzFriendlySignalKindWithConfig(cfg, item.Kind)
			if strings.TrimSpace(item.Kind) == "overlay" {
				title = functionFuzzFriendlyOverlayNameWithConfig(cfg, item.Name)
			}
			fmt.Fprintf(&b, "  - %s\n", title)
			for _, detail := range functionFuzzSignalDetailLines(cfg, item) {
				fmt.Fprintf(&b, "    - %s\n", detail)
			}
		}
	}
	b.WriteString("\n")
	return b.String()
}

func buildFunctionFuzzSummaryWithConfig(run FunctionFuzzRun, cfg Config) string {
	summary := functionFuzzLocalizedText(cfg, fmt.Sprintf("Planned directed fuzzing for %s with %d reachable call edge(s), risk=%d, engine=%s.", valueOrUnset(run.TargetSymbolName), run.ReachableCallCount, run.RiskScore, valueOrUnset(run.PrimaryEngine)), fmt.Sprintf("%s 대상으로 directed fuzzing 계획을 만들었습니다. reachable call edge=%d, risk=%d, engine=%s.", valueOrUnset(run.TargetSymbolName), run.ReachableCallCount, run.RiskScore, valueOrUnset(run.PrimaryEngine)))
	if strings.TrimSpace(run.Execution.Status) != "" {
		summary += " auto_exec=" + run.Execution.Status + "."
	}
	if functionFuzzExecutionNeedsConfirmation(run.Execution) {
		summary += functionFuzzLocalizedText(cfg, " confirmation required before background execution.", " 백그라운드 실행 전 확인이 필요합니다.")
	}
	return summary
}

func buildFunctionFuzzSummary(run FunctionFuzzRun) string {
	return buildFunctionFuzzSummaryWithConfig(run, functionFuzzEnglishConfig())
}

func functionFuzzExecutionNeedsConfirmation(execState FunctionFuzzExecution) bool {
	status := strings.ToLower(strings.TrimSpace(execState.Status))
	return status == "pending_confirmation" || status == "needs_confirmation"
}

func functionFuzzExecutionContinueCommand(runID string) string {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		runID = "latest"
	}
	return "/fuzz-func continue " + runID
}

func functionFuzzApproveExecution(cfg Config, run *FunctionFuzzRun, interactiveApproval bool) {
	if run == nil {
		return
	}
	run.Execution.Eligible = true
	run.Execution.Status = "planned"
	if interactiveApproval {
		run.Execution.Reason = functionFuzzLocalizedText(cfg, "Recovered build settings were reviewed and approved for autonomous fuzz execution.", "복구된 빌드 설정을 검토했고 자동 fuzz 실행에 사용해도 되는 것으로 승인했습니다.")
	} else {
		run.Execution.Reason = functionFuzzLocalizedText(cfg, "Recovered build settings were explicitly approved for autonomous fuzz execution.", "복구된 빌드 설정이 자동 fuzz 실행용으로 명시 승인되었습니다.")
	}
	run.Execution.ContinueCommand = ""
	run.Execution = normalizeFunctionFuzzExecution(run.Execution)
	run.Notes = append(run.Notes, functionFuzzLocalizedText(cfg, "Autonomous fuzz execution was approved after reviewing recovered build settings.", "복구된 빌드 설정 검토 후 자동 fuzz 실행이 승인되었습니다."))
	run.Notes = uniqueStrings(run.Notes)
	functionFuzzRefreshGuidance(cfg, run)
	run.Summary = buildFunctionFuzzSummaryWithConfig(*run, cfg)
}

func functionFuzzCompileContextLevel(record CompilationCommandRecord) string {
	source := strings.ToLower(filepath.ToSlash(strings.TrimSpace(record.Source)))
	switch {
	case strings.Contains(source, "compile_commands.json"), strings.HasPrefix(source, "snapshot:compile_commands"):
		return "exact"
	case strings.HasPrefix(source, "heuristic:"):
		return "estimated"
	case strings.HasPrefix(source, "structural_index_v2/"):
		return "partial"
	default:
		if strings.TrimSpace(record.Command) != "" || len(record.Arguments) > 0 {
			return "partial"
		}
	}
	return "estimated"
}

func functionFuzzCompileContextSourceScore(record CompilationCommandRecord) int {
	switch functionFuzzCompileContextLevel(record) {
	case "exact":
		return 120
	case "partial":
		return 45
	case "estimated":
		return 12
	default:
		return 0
	}
}

func functionFuzzMissingCompileSettings(record CompilationCommandRecord) []string {
	level := functionFuzzCompileContextLevel(record)
	missing := []string{}
	if level != "exact" {
		missing = append(missing, "Exact compile_commands.json entry for the target translation unit")
	}
	if strings.TrimSpace(record.Compiler) == "" && len(record.Arguments) == 0 {
		missing = append(missing, "Explicit compiler command line")
	}
	if len(record.IncludePaths) == 0 {
		missing = append(missing, "Verified include search paths")
	}
	if len(record.Defines) == 0 {
		missing = append(missing, "Verified preprocessor defines")
	}
	if len(record.ForceIncludes) == 0 && level != "exact" {
		missing = append(missing, "Any forced include or PCH setup")
	}
	return uniqueStrings(missing)
}

func functionFuzzCompileRecoveryNotes(cfg Config, root string, target SymbolRecord, record CompilationCommandRecord) []string {
	notes := []string{}
	level := functionFuzzCompileContextLevel(record)
	switch level {
	case "exact":
		notes = append(notes, functionFuzzLocalizedText(cfg, "Recovered an exact compile recipe for the target translation unit.", "타깃 translation unit에 대한 정확한 compile recipe를 복구했습니다."))
	case "partial":
		notes = append(notes, functionFuzzLocalizedText(cfg, "Recovered a partial build context and translated it into a fuzz build command.", "부분 build context를 복구해 fuzz build command로 변환했습니다."))
	case "estimated":
		notes = append(notes, functionFuzzLocalizedText(cfg, "Built an estimated compile recipe from source layout and nearby include directories.", "소스 레이아웃과 주변 include 디렉터리로부터 추정 compile recipe를 만들었습니다."))
	}
	if strings.TrimSpace(record.Directory) != "" {
		notes = append(notes, functionFuzzLocalizedText(cfg, "Compile directory candidate: ", "Compile 디렉터리 후보: ")+functionFuzzResolveWorkspacePath(root, record.Directory))
	}
	if strings.TrimSpace(target.File) != "" {
		notes = append(notes, functionFuzzLocalizedText(cfg, "Target translation unit: ", "타깃 translation unit: ")+functionFuzzResolveWorkspacePath(root, target.File))
	}
	if len(record.IncludePaths) > 0 {
		notes = append(notes, functionFuzzLocalizedText(cfg, "Recovered include paths: ", "복구한 include path: ")+strings.Join(limitStrings(resolveFunctionFuzzPaths(root, record.IncludePaths), 4), ", "))
	}
	if len(record.Defines) > 0 {
		notes = append(notes, functionFuzzLocalizedText(cfg, "Recovered defines: ", "복구한 define: ")+strings.Join(limitStrings(record.Defines, 4), ", "))
	}
	return uniqueStrings(notes)
}

func resolveFunctionFuzzPaths(root string, items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		resolved := functionFuzzResolveWorkspacePath(root, item)
		if strings.TrimSpace(resolved) == "" {
			continue
		}
		out = append(out, resolved)
	}
	return uniqueStrings(out)
}

func functionFuzzMissingCompileContextNotes(cfg Config, root string, target SymbolRecord, closure functionFuzzClosure, snapshot ProjectSnapshot) ([]string, []string) {
	missing := []string{
		functionFuzzLocalizedText(cfg, "Exact compile_commands.json entry for the target translation unit", "타깃 translation unit에 대한 정확한 compile_commands.json 항목"),
		functionFuzzLocalizedText(cfg, "Compiler invocation with verified include paths and defines", "검증된 include path와 define이 포함된 compiler invocation"),
	}
	notes := []string{
		functionFuzzLocalizedText(cfg, "Autonomous execution could not map the target to a trusted build recipe.", "자동 실행이 타깃을 신뢰 가능한 build recipe에 매핑하지 못했습니다."),
	}
	if len(snapshot.CompileCommands) == 0 {
		notes = append(notes, functionFuzzLocalizedText(cfg, "No compile_commands.json entries were discovered in the workspace or latest snapshot.", "워크스페이스나 최신 snapshot에서 compile_commands.json 항목을 찾지 못했습니다."))
	}
	if len(closure.Builds) > 0 {
		notes = append(notes, functionFuzzLocalizedText(cfg, "Build contexts exist in the semantic index, but they were not specific enough to recover a runnable compile command.", "semantic index에는 build context가 있지만 실행 가능한 compile command를 복구하기에는 충분히 구체적이지 않았습니다."))
	} else {
		notes = append(notes, functionFuzzLocalizedText(cfg, "No build context records were associated with the target closure.", "타깃 closure에 연결된 build context record가 없었습니다."))
	}
	if strings.TrimSpace(target.File) != "" {
		notes = append(notes, functionFuzzLocalizedText(cfg, "Target translation unit: ", "타깃 translation unit: ")+functionFuzzResolveWorkspacePath(root, target.File))
	}
	return uniqueStrings(missing), uniqueStrings(notes)
}

func prepareFunctionFuzzArtifacts(run *FunctionFuzzRun) error {
	if run == nil {
		return nil
	}
	artifactDir := filepath.Join(run.Workspace, userConfigDirName, "fuzz", run.ID)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return err
	}
	run.ArtifactDir = artifactDir
	run.PlanPath = filepath.Join(artifactDir, "plan.json")
	run.ReportPath = filepath.Join(artifactDir, "report.md")
	run.HarnessPath = filepath.Join(artifactDir, "harness.cpp")
	return nil
}

func writeFunctionFuzzPlanJSON(run *FunctionFuzzRun) error {
	if run == nil {
		return nil
	}
	planData, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(run.PlanPath, planData, 0o644); err != nil {
		return err
	}
	return nil
}

func writeFunctionFuzzArtifacts(run *FunctionFuzzRun, closure functionFuzzClosure, cfg Config) error {
	if run == nil {
		return nil
	}
	if err := prepareFunctionFuzzArtifacts(run); err != nil {
		return err
	}
	if err := writeFunctionFuzzPlanJSON(run); err != nil {
		return err
	}
	if err := os.WriteFile(run.ReportPath, []byte(renderFunctionFuzzReportMarkdownWithConfig(*run, closure, cfg)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(run.HarnessPath, []byte(renderFunctionFuzzHarness(*run)), 0o644); err != nil {
		return err
	}
	return nil
}

func planFunctionFuzzExecution(cfg Config, run *FunctionFuzzRun, target SymbolRecord, closure functionFuzzClosure, artifacts latestAnalysisArtifacts) {
	if run == nil {
		return
	}
	execState := FunctionFuzzExecution{
		Status: "blocked",
	}
	setBlocked := func(reason string) {
		execState.Eligible = false
		execState.Status = "blocked"
		execState.Reason = reason
		run.Execution = normalizeFunctionFuzzExecution(execState)
		if strings.TrimSpace(reason) != "" {
			run.Notes = append(run.Notes, functionFuzzLocalizedText(cfg, "Auto execution blocked: ", "자동 실행 차단 사유: ")+reason)
			run.Notes = uniqueStrings(run.Notes)
		}
	}

	if !functionFuzzSupportsAutoExecutionTarget(target) {
		setBlocked("Autonomous execution currently targets free-function C++ translation units with recoverable compile context.")
		return
	}
	if !run.HarnessReady {
		setBlocked("The detected target still needs object setup, handle provisioning, or custom parameter binding before autonomous execution.")
		return
	}

	record, ok := functionFuzzSelectCompileCommand(run.Workspace, target, closure, artifacts.Snapshot)
	if !ok {
		execState.MissingSettings, execState.RecoveryNotes = functionFuzzMissingCompileContextNotes(cfg, run.Workspace, target, closure, artifacts.Snapshot)
		setBlocked("No matching compile command or build context could be mapped to the target translation unit.")
		return
	}

	execState.CompileContextLevel = functionFuzzCompileContextLevel(record)
	execState.CompileCommandSource = firstNonBlankString(strings.TrimSpace(record.Source), "structural_index_v2")
	execState.TranslationUnit = functionFuzzResolveWorkspacePath(run.Workspace, firstNonBlankString(strings.TrimSpace(record.File), strings.TrimSpace(target.File)))
	execState.CompileDirectory = firstNonBlankString(
		functionFuzzResolveWorkspacePath(run.Workspace, strings.TrimSpace(record.Directory)),
		filepath.Dir(execState.TranslationUnit),
		run.Workspace,
	)
	execState.ExecutablePath = filepath.Join(run.ArtifactDir, "build", "fuzz_target.exe")
	execState.BuildScriptPath = filepath.Join(run.ArtifactDir, "run_fuzz.ps1")
	execState.BuildLogPath = filepath.Join(run.ArtifactDir, "build.log")
	execState.RunLogPath = filepath.Join(run.ArtifactDir, "run.log")
	execState.CorpusDir = filepath.Join(run.ArtifactDir, "corpus")
	execState.CrashDir = filepath.Join(run.ArtifactDir, "crashes")
	execState.MissingSettings = functionFuzzMissingCompileSettings(record)
	execState.RecoveryNotes = functionFuzzCompileRecoveryNotes(cfg, run.Workspace, target, record)

	if err := functionFuzzEnsureExecutionArtifactDirs(execState); err != nil {
		setBlocked(fmt.Sprintf("Failed to prepare fuzz artifact directories: %v", err))
		return
	}

	execState.CompilerCandidate = functionFuzzResolveCompilerCandidate(record, closure.Builds, target)
	execState.CompilerResolvedPath = functionFuzzResolveCompilerPath(execState.CompilerCandidate)
	execState.CompilerStyle = functionFuzzCompilerStyle(firstNonBlankString(execState.CompilerResolvedPath, execState.CompilerCandidate))
	if execState.CompilerResolvedPath == "" {
		setBlocked(fmt.Sprintf("Compiler not found on PATH or disk for recovered candidate %q.", execState.CompilerCandidate))
		return
	}
	if execState.CompilerStyle != "clang-cl" && execState.CompilerStyle != "clang" {
		setBlocked(fmt.Sprintf("Recovered compiler %q is not yet supported for autonomous fuzz execution.", filepath.Base(execState.CompilerResolvedPath)))
		return
	}

	buildArgs, err := functionFuzzBuildExecutionArgs(*run, record, execState)
	if err != nil {
		setBlocked(err.Error())
		return
	}
	runArgs := functionFuzzRunArgs(*run, execState)
	execState.BuildCommand = functionFuzzRenderDisplayCommand(execState.CompilerResolvedPath, buildArgs)
	execState.RunCommand = functionFuzzRenderDisplayCommand(execState.ExecutablePath, runArgs)

	if err := functionFuzzWriteSeedCorpus(execState.CorpusDir); err != nil {
		setBlocked(fmt.Sprintf("Failed to seed corpus directory: %v", err))
		return
	}
	if err := functionFuzzWriteRunnerScript(execState, buildArgs, runArgs); err != nil {
		setBlocked(fmt.Sprintf("Failed to write PowerShell runner: %v", err))
		return
	}

	execState.Eligible = true
	if execState.CompileContextLevel == "exact" {
		execState.Status = "planned"
		execState.Reason = "Recovered compile context is sufficient for automatic background build and smoke fuzzing."
	} else {
		execState.Status = "pending_confirmation"
		execState.Reason = "Recovered build settings are incomplete or heuristic; review them and confirm before automatic fuzzing starts."
		execState.ContinueCommand = functionFuzzExecutionContinueCommand(run.ID)
		run.Notes = append(run.Notes, functionFuzzLocalizedText(cfg, "Recovered build settings are partial or heuristic, so autonomous execution now waits for confirmation before it starts.", "복구된 빌드 설정이 부분적이거나 휴리스틱이라서 자동 실행은 지금 확인 대기 상태입니다."))
		run.Notes = uniqueStrings(run.Notes)
	}
	run.Execution = normalizeFunctionFuzzExecution(execState)
}

func functionFuzzSupportsAutoExecutionTarget(target SymbolRecord) bool {
	language := strings.ToLower(strings.TrimSpace(target.Language))
	file := strings.ToLower(strings.TrimSpace(target.File))
	switch {
	case strings.Contains(language, "cpp"), strings.Contains(language, "c++"), strings.Contains(language, "cxx"):
		return true
	case strings.HasSuffix(file, ".cpp"), strings.HasSuffix(file, ".cc"), strings.HasSuffix(file, ".cxx"):
		return true
	default:
		return false
	}
}

func functionFuzzSelectCompileCommand(root string, target SymbolRecord, closure functionFuzzClosure, snapshot ProjectSnapshot) (CompilationCommandRecord, bool) {
	records := functionFuzzExecutionCompileCommands(root, target, closure, snapshot)
	if len(records) == 0 {
		return CompilationCommandRecord{}, false
	}
	bestIndex := -1
	bestScore := -1
	for i, record := range records {
		score := 0
		score += functionFuzzPathMatchScore(root, record.File, target.File)
		score += functionFuzzCompileContextSourceScore(record)
		if strings.TrimSpace(target.BuildContextID) != "" && strings.EqualFold(strings.TrimSpace(record.BuildContextID), strings.TrimSpace(target.BuildContextID)) {
			score += 80
		}
		if strings.TrimSpace(record.Compiler) != "" || len(record.Arguments) > 0 {
			score += 8
		}
		lowerCompiler := strings.ToLower(strings.TrimSpace(firstNonBlankString(record.Compiler, firstString(record.Arguments))))
		switch {
		case strings.Contains(lowerCompiler, "clang-cl"):
			score += 14
		case strings.Contains(lowerCompiler, "clang"):
			score += 10
		case strings.Contains(lowerCompiler, "cl"):
			score += 4
		}
		if strings.TrimSpace(record.Directory) != "" {
			score += 3
		}
		if len(record.IncludePaths) > 0 || len(record.Defines) > 0 {
			score += 4
		}
		if score > bestScore {
			bestIndex = i
			bestScore = score
		}
	}
	if bestIndex < 0 || bestScore <= 0 {
		return CompilationCommandRecord{}, false
	}
	return records[bestIndex], true
}

func functionFuzzExecutionCompileCommands(root string, target SymbolRecord, closure functionFuzzClosure, snapshot ProjectSnapshot) []CompilationCommandRecord {
	seen := map[string]struct{}{}
	out := make([]CompilationCommandRecord, 0, len(snapshot.CompileCommands)+len(closure.Builds))
	appendRecord := func(record CompilationCommandRecord) {
		record.File = functionFuzzResolveWorkspacePath(root, record.File)
		record.Directory = functionFuzzResolveWorkspacePath(root, record.Directory)
		record.Source = functionFuzzNormalizeOptionalPath(record.Source)
		key := strings.Join([]string{
			strings.ToLower(strings.TrimSpace(record.File)),
			strings.ToLower(strings.TrimSpace(record.Directory)),
			strings.ToLower(strings.TrimSpace(record.BuildContextID)),
			strings.ToLower(strings.TrimSpace(record.Compiler)),
		}, "|")
		if strings.TrimSpace(record.File) == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, record)
	}
	for _, record := range snapshot.CompileCommands {
		record.Source = firstNonBlankString(strings.TrimSpace(record.Source), "snapshot:compile_commands")
		appendRecord(record)
	}
	if len(out) == 0 {
		for _, record := range discoverCompileCommands(root) {
			appendRecord(record)
		}
	}
	for _, build := range closure.Builds {
		record := CompilationCommandRecord{
			File:           strings.TrimSpace(target.File),
			Directory:      strings.TrimSpace(build.Directory),
			Compiler:       strings.TrimSpace(build.Compiler),
			IncludePaths:   append([]string(nil), build.IncludePaths...),
			Defines:        append([]string(nil), build.Defines...),
			ForceIncludes:  append([]string(nil), build.ForceIncludes...),
			BuildContextID: strings.TrimSpace(build.ID),
			Source:         filepath.ToSlash(filepath.Join("structural_index_v2", strings.TrimSpace(build.ID))),
		}
		for _, file := range build.Files {
			if functionFuzzPathMatchScore(root, file, target.File) >= 160 {
				record.File = strings.TrimSpace(file)
				break
			}
		}
		appendRecord(record)
	}
	if len(out) == 0 {
		for _, record := range functionFuzzHeuristicCompileCommands(root, target, closure, snapshot) {
			appendRecord(record)
		}
	}
	return out
}

func functionFuzzHeuristicCompileCommands(root string, target SymbolRecord, closure functionFuzzClosure, snapshot ProjectSnapshot) []CompilationCommandRecord {
	targetFile := strings.TrimSpace(target.File)
	if targetFile == "" {
		return nil
	}
	targetDir := filepath.Dir(functionFuzzResolveWorkspacePath(root, targetFile))
	build := functionFuzzBestBuildContextForTarget(root, target, closure.Builds)
	record := CompilationCommandRecord{
		File:           targetFile,
		Directory:      firstNonBlankString(strings.TrimSpace(build.Directory), targetDir, root),
		Compiler:       firstNonBlankString(strings.TrimSpace(build.Compiler), "clang-cl"),
		IncludePaths:   functionFuzzHeuristicIncludePaths(root, targetFile, build, snapshot),
		Defines:        append([]string(nil), build.Defines...),
		ForceIncludes:  append([]string(nil), build.ForceIncludes...),
		BuildContextID: strings.TrimSpace(build.ID),
		Source:         "heuristic:fuzz_func_source_scan",
	}
	return []CompilationCommandRecord{record}
}

func functionFuzzBestBuildContextForTarget(root string, target SymbolRecord, builds []BuildContextRecord) BuildContextRecord {
	best := BuildContextRecord{}
	bestScore := -1
	for _, build := range builds {
		score := 0
		if strings.TrimSpace(target.BuildContextID) != "" && strings.EqualFold(strings.TrimSpace(build.ID), strings.TrimSpace(target.BuildContextID)) {
			score += 100
		}
		for _, file := range build.Files {
			score += functionFuzzPathMatchScore(root, file, target.File)
		}
		if strings.TrimSpace(build.Compiler) != "" {
			score += 10
		}
		if len(build.IncludePaths) > 0 {
			score += 8
		}
		if len(build.Defines) > 0 {
			score += 4
		}
		if score > bestScore {
			best = build
			bestScore = score
		}
	}
	return best
}

func functionFuzzHeuristicIncludePaths(root string, targetFile string, build BuildContextRecord, snapshot ProjectSnapshot) []string {
	seen := map[string]struct{}{}
	out := []string{}
	appendPath := func(path string) {
		resolved := functionFuzzResolveWorkspacePath(root, path)
		if strings.TrimSpace(resolved) == "" {
			return
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			return
		}
		key := strings.ToLower(filepath.Clean(resolved))
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, resolved)
	}

	for _, includePath := range build.IncludePaths {
		appendPath(includePath)
	}

	targetAbs := functionFuzzResolveWorkspacePath(root, targetFile)
	targetDir := filepath.Dir(targetAbs)
	appendPath(targetDir)

	for _, child := range []string{"include", "includes", "inc", "public", "private", "classes"} {
		appendPath(filepath.Join(root, child))
		appendPath(filepath.Join(targetDir, child))
	}

	current := targetDir
	rootClean := filepath.Clean(root)
	for current != "" {
		for _, child := range []string{"include", "includes", "inc", "public", "private", "classes"} {
			appendPath(filepath.Join(current, child))
		}
		if strings.EqualFold(current, rootClean) {
			break
		}
		next := filepath.Dir(current)
		if next == current {
			break
		}
		current = next
	}

	for _, dir := range snapshot.Directories {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(dir)))
		switch {
		case strings.HasSuffix(lower, "/include"),
			strings.HasSuffix(lower, "/includes"),
			strings.HasSuffix(lower, "/inc"),
			strings.HasSuffix(lower, "/public"),
			strings.HasSuffix(lower, "/private"),
			strings.HasSuffix(lower, "/classes"):
			appendPath(dir)
		}
	}

	if strings.Contains(strings.ToLower(filepath.ToSlash(targetFile)), "/source/") {
		moduleRoot := functionFuzzGuessUnrealModuleRoot(root, targetAbs)
		if moduleRoot != "" {
			appendPath(moduleRoot)
			appendPath(filepath.Join(moduleRoot, "Public"))
			appendPath(filepath.Join(moduleRoot, "Private"))
			appendPath(filepath.Join(moduleRoot, "Classes"))
		}
	}

	return out
}

func functionFuzzGuessUnrealModuleRoot(root string, targetAbs string) string {
	normalized := filepath.ToSlash(strings.TrimSpace(targetAbs))
	lower := strings.ToLower(normalized)
	idx := strings.Index(lower, "/source/")
	if idx < 0 {
		return ""
	}
	rest := normalized[idx+len("/source/"):]
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return ""
	}
	base := normalized[:idx+len("/source/")]
	return filepath.Clean(filepath.FromSlash(base + parts[0]))
}

func functionFuzzPathMatchScore(root string, left string, right string) int {
	leftAbs := strings.ToLower(filepath.Clean(functionFuzzResolveWorkspacePath(root, left)))
	rightAbs := strings.ToLower(filepath.Clean(functionFuzzResolveWorkspacePath(root, right)))
	if leftAbs == "" || rightAbs == "" {
		return 0
	}
	if leftAbs == rightAbs {
		return 200
	}
	leftRel := strings.ToLower(filepath.ToSlash(relOrAbs(root, leftAbs)))
	rightRel := strings.ToLower(filepath.ToSlash(relOrAbs(root, rightAbs)))
	if leftRel == rightRel {
		return 180
	}
	if filepath.Base(leftAbs) == filepath.Base(rightAbs) {
		return 30
	}
	return 0
}

func functionFuzzResolveWorkspacePath(root string, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	if strings.TrimSpace(root) == "" {
		return filepath.Clean(trimmed)
	}
	return filepath.Clean(filepath.Join(root, trimmed))
}

func functionFuzzResolveCompilerCandidate(record CompilationCommandRecord, builds []BuildContextRecord, target SymbolRecord) string {
	if len(record.Arguments) > 0 && strings.TrimSpace(record.Arguments[0]) != "" {
		return strings.TrimSpace(record.Arguments[0])
	}
	if strings.TrimSpace(record.Compiler) != "" {
		return strings.TrimSpace(record.Compiler)
	}
	for _, build := range builds {
		if strings.TrimSpace(target.BuildContextID) != "" && !strings.EqualFold(strings.TrimSpace(build.ID), strings.TrimSpace(target.BuildContextID)) {
			continue
		}
		if strings.TrimSpace(build.Compiler) != "" {
			return strings.TrimSpace(build.Compiler)
		}
	}
	return "clang-cl"
}

func functionFuzzResolveCompilerPath(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}
	if filepath.IsAbs(candidate) {
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Clean(candidate)
		}
	}
	if resolved, err := exec.LookPath(candidate); err == nil {
		return filepath.Clean(resolved)
	}
	return ""
}

func functionFuzzCompilerStyle(candidate string) string {
	lower := strings.ToLower(filepath.Base(strings.TrimSpace(candidate)))
	switch {
	case strings.Contains(lower, "clang-cl"):
		return "clang-cl"
	case strings.Contains(lower, "clang++"), lower == "clang", lower == "clang.exe":
		return "clang"
	case lower == "cl" || lower == "cl.exe":
		return "cl"
	case strings.Contains(lower, "gcc"), strings.Contains(lower, "g++"):
		return "gcc"
	default:
		return ""
	}
}

func functionFuzzEnsureExecutionArtifactDirs(execState FunctionFuzzExecution) error {
	dirs := []string{
		filepath.Dir(execState.ExecutablePath),
		execState.CorpusDir,
		execState.CrashDir,
	}
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func functionFuzzBuildExecutionArgs(run FunctionFuzzRun, record CompilationCommandRecord, execState FunctionFuzzExecution) ([]string, error) {
	switch execState.CompilerStyle {
	case "clang-cl":
		return functionFuzzBuildExecutionArgsClangCL(run, record, execState), nil
	case "clang":
		return functionFuzzBuildExecutionArgsClang(run, record, execState), nil
	default:
		return nil, fmt.Errorf("unsupported compiler style for autonomous execution")
	}
}

func functionFuzzBuildExecutionArgsClangCL(run FunctionFuzzRun, record CompilationCommandRecord, execState FunctionFuzzExecution) []string {
	args := []string{
		"/nologo",
		"/Zi",
		"/Od",
		"/fsanitize=fuzzer,address",
		"/EHsc",
		"/utf-8",
	}
	subset := functionFuzzCompileFlagSubset(record, "clang-cl")
	if !functionFuzzArgsContainPrefix(subset, "/std:") {
		args = append(args, "/std:c++20")
	}
	args = append(args, subset...)
	args = append(args, functionFuzzStructuredBuildFlags(record, "clang-cl", run.Workspace)...)
	args = append(args,
		"/Fe:"+execState.ExecutablePath,
		run.HarnessPath,
		execState.TranslationUnit,
	)
	return functionFuzzUniqueArgs(args)
}

func functionFuzzBuildExecutionArgsClang(run FunctionFuzzRun, record CompilationCommandRecord, execState FunctionFuzzExecution) []string {
	args := []string{
		"-g",
		"-O1",
		"-fno-omit-frame-pointer",
		"-fsanitize=fuzzer,address,undefined",
	}
	subset := functionFuzzCompileFlagSubset(record, "clang")
	if !functionFuzzArgsContainPrefix(subset, "-std=") {
		args = append(args, "-std=c++20")
	}
	args = append(args, subset...)
	args = append(args, functionFuzzStructuredBuildFlags(record, "clang", run.Workspace)...)
	args = append(args,
		"-o", execState.ExecutablePath,
		run.HarnessPath,
		execState.TranslationUnit,
	)
	return functionFuzzUniqueArgs(args)
}

func functionFuzzCompileFlagSubset(record CompilationCommandRecord, style string) []string {
	if len(record.Arguments) <= 1 {
		return nil
	}
	args := record.Arguments[1:]
	out := make([]string, 0, len(args))
	skipNext := false
	for _, raw := range args {
		if skipNext {
			skipNext = false
			continue
		}
		arg := strings.TrimSpace(raw)
		if arg == "" {
			continue
		}
		lower := strings.ToLower(arg)
		switch style {
		case "clang-cl":
			switch {
			case lower == "/c", lower == "/nologo":
				continue
			case lower == "/fo" || lower == "/fe":
				skipNext = true
				continue
			case strings.HasPrefix(lower, "/fo"), strings.HasPrefix(lower, "/fe"), strings.HasPrefix(lower, "/fsanitize"), strings.HasPrefix(lower, "/i"), strings.HasPrefix(lower, "/d"), strings.HasPrefix(lower, "/fi"):
				continue
			case strings.HasPrefix(lower, "/std:"), strings.HasPrefix(lower, "/eh"), strings.HasPrefix(lower, "/gr"), strings.HasPrefix(lower, "/zc:"), strings.HasPrefix(lower, "/utf-"), lower == "/permissive-", strings.HasPrefix(lower, "/bigobj"), strings.HasPrefix(lower, "/favor:"), strings.HasPrefix(lower, "/arch:"), strings.HasPrefix(lower, "/volatile:"), strings.HasPrefix(lower, "/wd"), strings.HasPrefix(lower, "/we"):
				out = append(out, arg)
			}
		case "clang":
			switch {
			case lower == "-o", lower == "-i", lower == "-isystem", lower == "-include", lower == "-d", lower == "-mf", lower == "-mt", lower == "-mq", lower == "-x":
				skipNext = true
				continue
			case lower == "-c":
				continue
			case strings.HasPrefix(lower, "-o"), strings.HasPrefix(lower, "-i"), strings.HasPrefix(lower, "-isystem"), strings.HasPrefix(lower, "-include"), strings.HasPrefix(lower, "-d"), strings.HasPrefix(lower, "-u"), strings.HasPrefix(lower, "-fsanitize="):
				continue
			case strings.HasPrefix(lower, "-std="), strings.HasPrefix(lower, "-stdlib="), strings.HasPrefix(lower, "--target="), lower == "-target", strings.HasPrefix(lower, "-fms-"), strings.HasPrefix(lower, "-fno-"), strings.HasPrefix(lower, "-f"), strings.HasPrefix(lower, "-m"), strings.HasPrefix(lower, "-w"), strings.HasPrefix(lower, "-Winvalid"):
				out = append(out, arg)
			}
		}
	}
	return functionFuzzUniqueArgs(out)
}

func functionFuzzStructuredBuildFlags(record CompilationCommandRecord, style string, root string) []string {
	var out []string
	appendPathFlag := func(prefix string, value string) {
		resolved := functionFuzzResolveWorkspacePath(root, value)
		if strings.TrimSpace(resolved) == "" {
			return
		}
		if style == "clang" && prefix == "-include" {
			out = append(out, prefix, resolved)
			return
		}
		out = append(out, prefix+resolved)
	}
	for _, includePath := range record.IncludePaths {
		if style == "clang-cl" {
			appendPathFlag("/I", includePath)
		} else {
			appendPathFlag("-I", includePath)
		}
	}
	for _, define := range record.Defines {
		define = strings.TrimSpace(define)
		if define == "" {
			continue
		}
		if style == "clang-cl" {
			out = append(out, "/D"+define)
		} else {
			out = append(out, "-D"+define)
		}
	}
	for _, forceInclude := range record.ForceIncludes {
		if style == "clang-cl" {
			appendPathFlag("/FI", forceInclude)
		} else {
			appendPathFlag("-include", forceInclude)
		}
	}
	return functionFuzzUniqueArgs(out)
}

func functionFuzzArgsContainPrefix(args []string, prefix string) bool {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	for _, arg := range args {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(arg)), prefix) {
			return true
		}
	}
	return false
}

func functionFuzzUniqueArgs(args []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(args))
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func functionFuzzRunArgs(run FunctionFuzzRun, execState FunctionFuzzExecution) []string {
	return []string{
		fmt.Sprintf("-max_total_time=%d", 20),
		fmt.Sprintf("-max_len=%d", functionFuzzExecutionMaxLen(run.ParameterStrategies)),
		"-timeout=5",
		"-print_final_stats=1",
		"-rss_limit_mb=4096",
		"-artifact_prefix=" + filepath.Clean(execState.CrashDir) + string(os.PathSeparator),
		execState.CorpusDir,
	}
}

func functionFuzzExecutionMaxLen(items []FunctionFuzzParamStrategy) int {
	maxLen := 256
	for _, item := range items {
		switch item.Class {
		case "buffer", "pointer", "container":
			if maxLen < 4096 {
				maxLen = 4096
			}
		case "string":
			if maxLen < 1024 {
				maxLen = 1024
			}
		}
	}
	return maxLen
}

func functionFuzzWriteSeedCorpus(corpusDir string) error {
	if err := os.MkdirAll(corpusDir, 0o755); err != nil {
		return err
	}
	seeds := map[string][]byte{
		"seed-empty.bin":   {},
		"seed-pattern.bin": {0x00, 0x01, 0x7f, 0x80, 0xff, 0x10, 0x20, 0x40},
		"seed-structured.bin": {
			0x04, 0x00, 0x00, 0x00,
			0x10, 0x20, 0x30, 0x40,
			0xff, 0xee, 0xdd, 0xcc,
			0x00, 0x00, 0x00, 0x01,
		},
	}
	for name, data := range seeds {
		if err := os.WriteFile(filepath.Join(corpusDir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func functionFuzzWriteRunnerScript(execState FunctionFuzzExecution, buildArgs []string, runArgs []string) error {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Stop'\n")
	fmt.Fprintf(&b, "$buildDir = %s\n", functionFuzzPowershellLiteral(filepath.Dir(execState.ExecutablePath)))
	fmt.Fprintf(&b, "$compiler = %s\n", functionFuzzPowershellLiteral(execState.CompilerResolvedPath))
	fmt.Fprintf(&b, "$target = %s\n", functionFuzzPowershellLiteral(execState.ExecutablePath))
	b.WriteString("New-Item -ItemType Directory -Force -Path $buildDir | Out-Null\n")
	fmt.Fprintf(&b, "New-Item -ItemType Directory -Force -Path %s | Out-Null\n", functionFuzzPowershellLiteral(execState.CorpusDir))
	fmt.Fprintf(&b, "New-Item -ItemType Directory -Force -Path %s | Out-Null\n", functionFuzzPowershellLiteral(execState.CrashDir))
	b.WriteString("$buildArgs = @(\n")
	for _, arg := range buildArgs {
		fmt.Fprintf(&b, "    %s\n", functionFuzzPowershellLiteral(arg))
	}
	b.WriteString(")\n")
	fmt.Fprintf(&b, "& $compiler @buildArgs 2>&1 | Tee-Object -FilePath %s\n", functionFuzzPowershellLiteral(execState.BuildLogPath))
	b.WriteString("if ($LASTEXITCODE -ne 0)\n")
	b.WriteString("{\n")
	b.WriteString("    exit $LASTEXITCODE\n")
	b.WriteString("}\n")
	b.WriteString("$runArgs = @(\n")
	for _, arg := range runArgs {
		fmt.Fprintf(&b, "    %s\n", functionFuzzPowershellLiteral(arg))
	}
	b.WriteString(")\n")
	fmt.Fprintf(&b, "& $target @runArgs 2>&1 | Tee-Object -FilePath %s\n", functionFuzzPowershellLiteral(execState.RunLogPath))
	b.WriteString("exit $LASTEXITCODE\n")
	return os.WriteFile(execState.BuildScriptPath, []byte(b.String()), 0o644)
}

func functionFuzzPowershellLiteral(value string) string {
	value = strings.ReplaceAll(value, "'", "''")
	return "'" + value + "'"
}

func functionFuzzRenderDisplayCommand(command string, args []string) string {
	out := make([]string, 0, len(args)+1)
	out = append(out, functionFuzzDisplayCommandPart(command))
	for _, arg := range args {
		out = append(out, functionFuzzDisplayCommandPart(arg))
	}
	return strings.Join(out, " ")
}

func functionFuzzDisplayCommandPart(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return `""`
	}
	if strings.ContainsAny(trimmed, " \t\"") {
		return strconv.Quote(trimmed)
	}
	return trimmed
}

func (rt *runtimeState) maybeConfirmFunctionFuzzExecution(run *FunctionFuzzRun) error {
	if rt == nil || run == nil {
		return nil
	}
	if !functionFuzzExecutionNeedsConfirmation(run.Execution) {
		return nil
	}
	if !rt.interactive {
		run.Execution.ContinueCommand = firstNonBlankString(strings.TrimSpace(run.Execution.ContinueCommand), functionFuzzExecutionContinueCommand(run.ID))
		run.Execution = normalizeFunctionFuzzExecution(run.Execution)
		functionFuzzRefreshGuidance(rt.cfg, run)
		run.Summary = buildFunctionFuzzSummaryWithConfig(*run, rt.cfg)
		return nil
	}

	fmt.Fprintln(rt.writer, rt.ui.warnLine("Autonomous fuzz execution is using recovered build settings instead of an exact compile recipe."))
	for _, item := range limitStrings(run.Execution.MissingSettings, 4) {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("missing", item))
	}
	for _, item := range limitStrings(run.Execution.RecoveryNotes, 4) {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("recovered", compactPersistentMemoryText(item, 140)))
	}
	fmt.Fprintln(rt.writer)

	question := fmt.Sprintf("Start autonomous fuzzing for %s with the recovered build settings?", valueOrUnset(run.TargetSymbolName))
	approved, err := rt.confirm(question)
	if err != nil {
		return err
	}
	if approved {
		functionFuzzApproveExecution(rt.cfg, run, true)
		return nil
	}

	run.Execution.Status = "pending_confirmation"
	run.Execution.Reason = functionFuzzLocalizedText(rt.cfg, "Recovered build settings were reviewed, but autonomous fuzzing was not started yet.", "복구된 빌드 설정은 검토했지만 자동 fuzz 실행은 아직 시작하지 않았습니다.")
	run.Execution.ContinueCommand = firstNonBlankString(strings.TrimSpace(run.Execution.ContinueCommand), functionFuzzExecutionContinueCommand(run.ID))
	run.Execution = normalizeFunctionFuzzExecution(run.Execution)
	run.Notes = append(run.Notes, functionFuzzLocalizedText(rt.cfg, "Autonomous fuzz execution was held after the recovered build settings prompt. Use ", "복구된 빌드 설정 확인 단계에서 자동 fuzz 실행은 보류되었습니다. 계속하려면 ")+run.Execution.ContinueCommand+functionFuzzLocalizedText(rt.cfg, " when you want to proceed.", " 명령을 사용하면 됩니다."))
	run.Notes = uniqueStrings(run.Notes)
	functionFuzzRefreshGuidance(rt.cfg, run)
	run.Summary = buildFunctionFuzzSummaryWithConfig(*run, rt.cfg)
	return nil
}

func (rt *runtimeState) maybeLaunchFunctionFuzzExecution(run *FunctionFuzzRun) {
	if rt == nil || run == nil {
		return
	}
	if !run.Execution.Eligible || !strings.EqualFold(strings.TrimSpace(run.Execution.Status), "planned") {
		return
	}
	if rt.backgroundJobs == nil {
		run.Execution.Reason = functionFuzzLocalizedText(rt.cfg, "Execution plan is ready, but this session has no background job manager.", "실행 계획은 준비됐지만 이 세션에는 백그라운드 job manager가 없습니다.")
		run.Execution = normalizeFunctionFuzzExecution(run.Execution)
		functionFuzzRefreshGuidance(rt.cfg, run)
		run.Summary = buildFunctionFuzzSummaryWithConfig(*run, rt.cfg)
		return
	}
	command := "& " + functionFuzzPowershellLiteral(run.Execution.BuildScriptPath)
	job, err := rt.backgroundJobs.StartShellJob(
		firstNonBlankString(rt.workspace.Shell, rt.cfg.Shell, "powershell"),
		firstNonBlankString(run.Execution.CompileDirectory, run.Workspace),
		command,
		shellCommandAssessment{
			Class:  shellMutationWorkspaceWrite,
			Reason: "function fuzz build and runtime artifacts",
		},
		"",
	)
	if err != nil {
		run.Execution.Eligible = false
		run.Execution.Status = "blocked"
		run.Execution.Reason = fmt.Sprintf("Failed to start background fuzz job: %v", err)
		run.Execution = normalizeFunctionFuzzExecution(run.Execution)
		run.Notes = append(run.Notes, functionFuzzLocalizedText(rt.cfg, "Auto execution blocked: ", "자동 실행 차단 사유: ")+run.Execution.Reason)
		run.Notes = uniqueStrings(run.Notes)
		functionFuzzRefreshGuidance(rt.cfg, run)
		run.Summary = buildFunctionFuzzSummaryWithConfig(*run, rt.cfg)
		return
	}
	run.Execution.BackgroundJobID = job.ID
	run.Execution.Status = "running"
	run.Execution.Reason = functionFuzzLocalizedText(rt.cfg, "Background build and smoke fuzzing started automatically.", "백그라운드 빌드와 스모크 fuzzing이 자동으로 시작되었습니다.")
	run.Execution.LastOutput = compactPersistentMemoryText(job.LastOutput, 220)
	run.Execution = normalizeFunctionFuzzExecution(run.Execution)
	functionFuzzRefreshGuidance(rt.cfg, run)
	run.Summary = buildFunctionFuzzSummaryWithConfig(*run, rt.cfg)
}

func (rt *runtimeState) refreshFunctionFuzzExecution(run FunctionFuzzRun) (FunctionFuzzRun, bool) {
	if rt == nil {
		return run, false
	}
	updated := run
	changed := false
	if strings.TrimSpace(updated.Execution.CrashDir) != "" {
		if crashCount := functionFuzzCountCrashArtifacts(updated.Execution.CrashDir); crashCount != updated.Execution.CrashCount {
			updated.Execution.CrashCount = crashCount
			changed = true
		}
	}
	if rt.backgroundJobs == nil || strings.TrimSpace(updated.Execution.BackgroundJobID) == "" {
		if changed {
			updated.Execution = normalizeFunctionFuzzExecution(updated.Execution)
			updated.Summary = buildFunctionFuzzSummaryWithConfig(updated, rt.cfg)
		}
		return updated, changed
	}
	job, err := rt.backgroundJobs.SyncJob(updated.Execution.BackgroundJobID)
	if err != nil {
		return run, changed
	}
	nextStatus := updated.Execution.Status
	switch strings.ToLower(strings.TrimSpace(job.Status)) {
	case "completed":
		if job.ExitCode != nil && *job.ExitCode != 0 {
			nextStatus = "failed"
		} else {
			nextStatus = "completed"
		}
	case "failed", "canceled", "preempted":
		nextStatus = strings.ToLower(strings.TrimSpace(job.Status))
	case "running":
		nextStatus = "running"
	}
	if nextStatus != updated.Execution.Status {
		updated.Execution.Status = nextStatus
		changed = true
	}
	if updated.Execution.LastOutput != compactPersistentMemoryText(job.LastOutput, 260) {
		updated.Execution.LastOutput = compactPersistentMemoryText(job.LastOutput, 260)
		changed = true
	}
	if job.ExitCode != nil {
		if updated.Execution.ExitCode == nil || *updated.Execution.ExitCode != *job.ExitCode {
			code := *job.ExitCode
			updated.Execution.ExitCode = &code
			changed = true
		}
	}
	reason := updated.Execution.Reason
	switch updated.Execution.Status {
	case "completed":
		reason = functionFuzzLocalizedText(rt.cfg, "Background smoke fuzzing completed.", "백그라운드 스모크 fuzzing이 완료되었습니다.")
	case "failed":
		reason = functionFuzzLocalizedText(rt.cfg, "Background fuzz job failed.", "백그라운드 fuzz job이 실패했습니다.")
	case "running":
		reason = functionFuzzLocalizedText(rt.cfg, "Background build and smoke fuzzing is still running.", "백그라운드 빌드와 스모크 fuzzing이 아직 실행 중입니다.")
	case "canceled", "preempted":
		reason = functionFuzzLocalizedText(rt.cfg, "Background fuzz job did not complete normally.", "백그라운드 fuzz job이 정상 종료되지 않았습니다.")
	}
	if updated.Execution.CrashCount > 0 {
		reason = fmt.Sprintf("%s crash_artifacts=%d.", strings.TrimSpace(reason), updated.Execution.CrashCount)
	}
	if compactPersistentMemoryText(reason, 220) != updated.Execution.Reason {
		updated.Execution.Reason = compactPersistentMemoryText(reason, 220)
		changed = true
	}
	if changed {
		updated.Execution = normalizeFunctionFuzzExecution(updated.Execution)
		functionFuzzRefreshGuidance(rt.cfg, &updated)
		updated.Summary = buildFunctionFuzzSummaryWithConfig(updated, rt.cfg)
		_ = writeFunctionFuzzPlanJSON(&updated)
	}
	return updated, changed
}

func functionFuzzCountCrashArtifacts(crashDir string) int {
	entries, err := os.ReadDir(crashDir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		count++
	}
	return count
}

func renderFunctionFuzzRunWithConfig(run FunctionFuzzRun, cfg Config) string {
	var b strings.Builder
	ui := NewUI()
	b.WriteString(functionFuzzRenderTerminalHeader(ui, functionFuzzLocalizedText(cfg, "Conclusion", "결론"), "mint") + "\n")
	for _, item := range functionFuzzConclusionLines(cfg, run) {
		functionFuzzWriteWrappedText(&b, "- ", "  ", item, 112)
	}

	b.WriteString("\n" + functionFuzzRenderTerminalHeader(ui, functionFuzzLocalizedText(cfg, "Status", "상태"), "info") + "\n")
	for _, item := range functionFuzzStatusLines(cfg, run) {
		functionFuzzWriteWrappedText(&b, "- ", "  ", item, 112)
	}
	functionFuzzWriteWrappedText(&b, "- "+functionFuzzLocalizedText(cfg, "AI source-only fuzzing", "AI 소스 전용 fuzzing")+": ", "  ", functionFuzzSourceOnlySynthesisSummaryWithConfig(cfg, run), 99)
	functionFuzzWriteWrappedText(&b, "- "+functionFuzzLocalizedText(cfg, "Best next move", "다음 권장 조치")+": ", "  ", functionFuzzBestNextMove(cfg, run), 104)
	if best := functionFuzzBestSuggestedTarget(run); strings.TrimSpace(best) != "" {
		targetText, rationale := functionFuzzSplitSuggestedTargetLabel(best)
		functionFuzzWriteWrappedText(&b, "- "+functionFuzzLocalizedText(cfg, "Best immediate next target", "가장 먼저 볼 다음 타깃")+": ", "  ", targetText, 100)
		if strings.TrimSpace(rationale) != "" {
			functionFuzzWriteWrappedText(&b, "- "+functionFuzzLocalizedText(cfg, "Why this target is a good next root", "이 타깃을 다음 루트로 추천하는 이유")+": ", "  ", functionFuzzDisplayText(cfg, rationale), 95)
		}
	}
	if len(run.SuggestedCommands) > 0 {
		functionFuzzWriteWrappedText(&b, "- "+functionFuzzLocalizedText(cfg, "Suggested next command", "추천 다음 명령")+": ", "  ", run.SuggestedCommands[0], 96)
	}

	if details := functionFuzzNativeExecutionDetailLinesWithConfig(cfg, run); len(details) > 0 {
		titleTone := "warn"
		if run.HarnessReady && !strings.EqualFold(strings.TrimSpace(run.Execution.Status), "blocked") {
			titleTone = "success"
		}
		b.WriteString("\n" + functionFuzzRenderTerminalHeader(ui, functionFuzzLocalizedText(cfg, "Native auto-run meaning", "네이티브 자동 실행 의미"), titleTone) + "\n")
		for _, item := range limitStrings(details, 5) {
			functionFuzzWriteWrappedText(&b, "- ", "  ", item, 112)
		}
	}

	if len(run.SuggestedTargets) > 0 {
		b.WriteString("\n" + functionFuzzRenderTerminalHeader(ui, functionFuzzLocalizedText(cfg, "Other good next targets", "다른 좋은 다음 타깃"), "success") + "\n")
		for _, item := range limitStrings(run.SuggestedTargets, 3) {
			targetText, rationale := functionFuzzSplitSuggestedTargetLabel(item)
			functionFuzzWriteWrappedText(&b, "- ", "  ", targetText, 112)
			if strings.TrimSpace(rationale) != "" {
				functionFuzzWriteWrappedText(&b, "  "+functionFuzzLocalizedText(cfg, "why", "이유")+": ", "       ", functionFuzzDisplayText(cfg, rationale), 104)
			}
		}
	}

	if len(run.VirtualScenarios) > 0 {
		scenarios := functionFuzzSortedVirtualScenarios(run.VirtualScenarios)
		b.WriteString("\n" + functionFuzzRenderTerminalHeader(ui, functionFuzzLocalizedText(cfg, "Risk-ranked findings", "위험도 점수표"), "warn") + "\n")
		for _, item := range limitFunctionFuzzVirtualScenarios(scenarios, 5) {
			scoreLabel := functionFuzzScenarioScoreLabel(item)
			if strings.TrimSpace(scoreLabel) == "" {
				scoreLabel = functionFuzzLocalizedText(cfg, "unscored", "미채점")
			}
			line := scoreLabel + " | " + functionFuzzDisplayText(cfg, strings.TrimSpace(item.Title))
			functionFuzzWriteWrappedText(&b, "- ", "  ", functionFuzzColorizeRiskText(ui, item.RiskScore, line), 112)
			if why := functionFuzzScenarioRankWhy(cfg, item); strings.TrimSpace(why) != "" {
				functionFuzzWriteWrappedText(&b, "  "+functionFuzzLocalizedText(cfg, "why this score", "이 점수의 이유")+": ", "       ", why, 104)
			}
		}

		b.WriteString("\n" + functionFuzzRenderTerminalHeader(ui, functionFuzzLocalizedText(cfg, "Top predicted problems", "상위 예측 문제"), "warn") + "\n")
		fmt.Fprintln(&b, "- "+functionFuzzLocalizedText(cfg, "These are AI-generated source-only scenarios. Kernforge did not need to run the target to predict them.", "이 섹션은 AI가 만든 소스 전용 시나리오입니다. Kernforge는 타깃을 실제 실행하지 않아도 이를 예측할 수 있습니다."))
		fmt.Fprintln(&b, "- "+functionFuzzLocalizedText(cfg, "The hypothetical input states below are Kernforge's internal modeling assumptions, not manual reproduction steps for the user.", "아래 입력 상태들은 Kernforge가 내부 분석에 사용한 가정이며, 사용자가 직접 재현하라는 절차가 아닙니다."))
		fmt.Fprintln(&b, "- "+functionFuzzLocalizedText(cfg, "Read each block as: Kernforge's internal hypothetical input state, what the code will likely do, what can go wrong, and the first source file to inspect.", "각 블록은 Kernforge 내부 가정 입력 상태, 코드가 할 가능성이 높은 일, 생길 수 있는 문제, 그리고 먼저 볼 소스 파일 순서로 읽으면 됩니다."))
		for _, item := range limitFunctionFuzzVirtualScenarios(scenarios, 5) {
			title := functionFuzzScenarioDisplayHeader(cfg, item)
			functionFuzzWriteWrappedText(&b, "- ", "  ", functionFuzzColorizeRiskText(ui, item.RiskScore, title), 112)
			if why := functionFuzzScenarioRankWhy(cfg, item); strings.TrimSpace(why) != "" {
				functionFuzzWriteWrappedText(&b, "  "+functionFuzzLocalizedText(cfg, "why this score", "이 점수의 이유")+": ", "       ", why, 104)
			}
			for _, line := range functionFuzzScenarioSummary(cfg, item) {
				functionFuzzWriteWrappedText(&b, "  ", "  ", line, 112)
			}
			functionFuzzRenderScenarioExcerptTerminal(cfg, &b, item)
		}
		if len(scenarios) > 5 {
			titles := []string{}
			for _, item := range scenarios[5:] {
				titles = append(titles, functionFuzzDisplayText(cfg, strings.TrimSpace(item.Title)))
			}
			functionFuzzWriteWrappedText(&b, "- "+functionFuzzLocalizedText(cfg, "Additional predicted problems", "추가 예측 문제")+": ", "  ", strings.Join(limitStrings(titles, 4), "; "), 100)
		}
	}

	if len(run.CodeObservations) > 0 {
		b.WriteString("\n" + functionFuzzRenderTerminalHeader(ui, functionFuzzLocalizedText(cfg, "Source-derived attack surface", "소스 기반 공격 표면"), "accent") + "\n")
		fmt.Fprintln(&b, "- "+functionFuzzLocalizedText(cfg, "These are concrete guards, sinks, and unwind edges extracted from real function bodies in the mapped closure.", "이 섹션은 매핑된 closure 안의 실제 함수 본문에서 추출한 가드, sink, unwind 경로입니다."))
		fmt.Fprintln(&b, "- "+functionFuzzLocalizedText(cfg, "Kernforge uses these observations as the source evidence behind the attacker-style scenarios above.", "Kernforge는 위 공격자 관점 시나리오를 만들 때 이 관찰들을 직접 근거로 사용합니다."))
		for _, item := range limitFunctionFuzzCodeObservations(functionFuzzSortedCodeObservations(run.CodeObservations), 5) {
			location := filepath.ToSlash(strings.TrimSpace(item.File))
			if item.Line > 0 {
				location += ":" + strconv.Itoa(item.Line)
			}
			header := functionFuzzFriendlyObservationKindWithConfig(cfg, item.Kind)
			functionFuzzWriteWrappedText(&b, "- ", "  ", header+" @ "+location, 112)
			if strings.TrimSpace(item.Evidence) != "" {
				functionFuzzWriteWrappedText(&b, "  "+functionFuzzLocalizedText(cfg, "code", "코드")+": ", "       ", item.Evidence, 104)
			}
			if strings.TrimSpace(item.WhyItMatters) != "" {
				functionFuzzWriteWrappedText(&b, "  "+functionFuzzLocalizedText(cfg, "why it matters", "중요한 이유")+": ", "       ", functionFuzzDisplayText(cfg, item.WhyItMatters), 104)
			}
		}
	}

	if len(run.NextSteps) > 0 {
		b.WriteString("\n" + functionFuzzRenderTerminalHeader(ui, functionFuzzLocalizedText(cfg, "What to do next", "다음에 할 일"), "info") + "\n")
		for _, item := range limitStrings(run.NextSteps, 4) {
			functionFuzzWriteWrappedText(&b, "- ", "  ", functionFuzzDisplayText(cfg, item), 112)
		}
	}

	b.WriteString("\n" + functionFuzzRenderTerminalHeader(ui, functionFuzzLocalizedText(cfg, "Technical details", "기술 세부 정보"), "default") + "\n")
	fmt.Fprintf(&b, "- id: %s\n", run.ID)
	if strings.TrimSpace(run.ScopeMode) != "" {
		fmt.Fprintf(&b, "- scope_mode: %s\n", strings.TrimSpace(run.ScopeMode))
	}
	if strings.TrimSpace(run.ScopeRootFile) != "" {
		fmt.Fprintf(&b, "- scope_root_file: %s\n", filepath.ToSlash(strings.TrimSpace(run.ScopeRootFile)))
	}
	if len(run.ScopeFiles) > 0 {
		fmt.Fprintf(&b, "- scope_files: %d\n", len(run.ScopeFiles))
	}
	fmt.Fprintf(&b, "- target: %s\n", valueOrUnset(run.TargetSymbolName))
	fmt.Fprintf(&b, "- symbol: %s\n", valueOrUnset(run.TargetSymbolID))
	if strings.TrimSpace(run.TargetFile) != "" {
		location := filepath.ToSlash(run.TargetFile)
		if run.TargetStartLine > 0 {
			location += ":" + strconv.Itoa(run.TargetStartLine)
		}
		fmt.Fprintf(&b, "- file: %s\n", location)
	}
	fmt.Fprintf(&b, "- query_mode: %s\n", valueOrUnset(run.QueryMode))
	fmt.Fprintf(&b, "- analysis_priority: %s\n", compactPersistentMemoryText(functionFuzzAnalysisPrioritySummary(cfg, run), 220))
	fmt.Fprintf(&b, "- engine: %s\n", valueOrUnset(run.PrimaryEngine))
	if len(run.SecondaryEngines) > 0 {
		fmt.Fprintf(&b, "- engine_followups: %s\n", strings.Join(limitStrings(run.SecondaryEngines, 4), ", "))
	}
	fmt.Fprintf(&b, "- source_observations: %d\n", len(run.CodeObservations))
	fmt.Fprintf(&b, "- reachable_calls: %d\n", run.ReachableCallCount)
	fmt.Fprintf(&b, "- reachable_depth: %d\n", run.ReachableDepth)
	fmt.Fprintf(&b, "- harness_ready: %t\n", run.HarnessReady)
	if strings.TrimSpace(run.Execution.Status) != "" {
		fmt.Fprintf(&b, "- auto_exec: %s\n", functionFuzzFriendlyExecutionStatusWithConfig(cfg, run.Execution.Status))
	}
	if strings.TrimSpace(run.Execution.Reason) != "" {
		fmt.Fprintf(&b, "- auto_exec_reason: %s\n", run.Execution.Reason)
	}
	if strings.TrimSpace(run.Execution.CompileContextLevel) != "" {
		fmt.Fprintf(&b, "- compile_context: %s\n", run.Execution.CompileContextLevel)
	}
	if strings.TrimSpace(run.Execution.CompileCommandSource) != "" {
		fmt.Fprintf(&b, "- compile_source: %s\n", run.Execution.CompileCommandSource)
	}
	if len(run.OverlayDomains) > 0 {
		fmt.Fprintf(&b, "- overlays: %s\n", strings.Join(limitStrings(run.OverlayDomains, 4), ", "))
	}
	if len(run.BuildContexts) > 0 {
		fmt.Fprintf(&b, "- build_contexts: %s\n", strings.Join(limitStrings(run.BuildContexts, 3), ", "))
	}
	if strings.TrimSpace(run.HarnessPath) != "" {
		fmt.Fprintf(&b, "- harness: %s\n", run.HarnessPath)
	}
	if strings.TrimSpace(run.ReportPath) != "" {
		fmt.Fprintf(&b, "- report: %s\n", run.ReportPath)
	}
	if strings.TrimSpace(run.Execution.ExecutablePath) != "" {
		fmt.Fprintf(&b, "- executable: %s\n", run.Execution.ExecutablePath)
	}
	if strings.TrimSpace(run.Execution.BackgroundJobID) != "" {
		fmt.Fprintf(&b, "- background_job: %s\n", run.Execution.BackgroundJobID)
	}
	if strings.TrimSpace(run.Execution.LastOutput) != "" {
		fmt.Fprintf(&b, "- last_output: %s\n", compactPersistentMemoryText(run.Execution.LastOutput, 160))
	}
	if run.Execution.CrashCount > 0 {
		fmt.Fprintf(&b, "- crash_artifacts: %d\n", run.Execution.CrashCount)
	}
	if len(run.Execution.MissingSettings) > 0 {
		b.WriteString("\n" + functionFuzzRenderTerminalHeader(ui, functionFuzzLocalizedText(cfg, "Missing settings", "누락된 설정"), "warn") + "\n")
		for _, item := range limitStrings(run.Execution.MissingSettings, 5) {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if len(run.Execution.RecoveryNotes) > 0 {
		b.WriteString("\n" + functionFuzzRenderTerminalHeader(ui, functionFuzzLocalizedText(cfg, "Recovery", "복구 내용"), "info") + "\n")
		for _, item := range limitStrings(run.Execution.RecoveryNotes, 5) {
			fmt.Fprintf(&b, "- %s\n", compactPersistentMemoryText(item, 140))
		}
	}
	if strings.TrimSpace(run.Execution.ContinueCommand) != "" {
		b.WriteString("\n" + functionFuzzRenderTerminalHeader(ui, functionFuzzLocalizedText(cfg, "Next", "다음"), "success") + "\n")
		fmt.Fprintf(&b, "- %s\n", run.Execution.ContinueCommand)
	}
	if len(run.ParameterStrategies) > 0 {
		b.WriteString("\n" + functionFuzzRenderTerminalHeader(ui, functionFuzzLocalizedText(cfg, "Parameters", "파라미터"), "accent") + "\n")
		for _, item := range run.ParameterStrategies {
			line := fmt.Sprintf("- #%d %s : %s -> %s", item.Index, valueOrUnset(item.Name), valueOrUnset(item.RawType), functionFuzzFriendlyParamClassWithConfig(cfg, item.Class))
			if disposition := functionFuzzParameterDisposition(cfg, item); disposition != "" {
				line += "  |  " + disposition
			}
			if strings.TrimSpace(item.Relation) != "" {
				line += "  relation=" + item.Relation
			}
			if len(item.Mutators) > 0 {
				line += "  mutators=" + strings.Join(limitStrings(item.Mutators, 3), ",")
			}
			fmt.Fprintln(&b, line)
		}
	}
	functionFuzzRenderSignalsTerminal(cfg, &b, run, 112)
	if len(run.Notes) > 0 {
		b.WriteString("\n" + functionFuzzRenderTerminalHeader(ui, functionFuzzLocalizedText(cfg, "Notes", "메모"), "info") + "\n")
		for _, item := range limitStrings(run.Notes, 5) {
			fmt.Fprintf(&b, "- %s\n", compactPersistentMemoryText(functionFuzzDisplayText(cfg, item), 140))
		}
	}
	return strings.TrimSpace(b.String())
}

func renderFunctionFuzzRun(run FunctionFuzzRun) string {
	return renderFunctionFuzzRunWithConfig(run, functionFuzzEnglishConfig())
}

func renderFunctionFuzzReportMarkdownWithConfig(run FunctionFuzzRun, closure functionFuzzClosure, cfg Config) string {
	var b strings.Builder
	b.WriteString("# " + functionFuzzLocalizedText(cfg, "Function Fuzz Plan", "Function Fuzz 계획") + "\n\n")
	b.WriteString("## " + functionFuzzLocalizedText(cfg, "Conclusion", "결론") + "\n\n")
	for _, item := range functionFuzzConclusionLines(cfg, run) {
		b.WriteString("- " + functionFuzzNormalizeDisplayText(item) + "\n")
	}

	b.WriteString("\n## " + functionFuzzLocalizedText(cfg, "Status", "상태") + "\n\n")
	for _, item := range functionFuzzStatusLines(cfg, run) {
		b.WriteString("- " + functionFuzzNormalizeDisplayText(item) + "\n")
	}
	fmt.Fprintf(&b, "- %s: %s\n", functionFuzzLocalizedText(cfg, "AI source-only fuzzing", "AI 소스 전용 fuzzing"), functionFuzzSourceOnlySynthesisSummaryWithConfig(cfg, run))
	fmt.Fprintf(&b, "- %s: %s\n", functionFuzzLocalizedText(cfg, "Best next move", "다음 권장 조치"), functionFuzzBestNextMove(cfg, run))
	if best := functionFuzzBestSuggestedTarget(run); strings.TrimSpace(best) != "" {
		targetText, rationale := functionFuzzSplitSuggestedTargetLabel(best)
		fmt.Fprintf(&b, "- %s: %s\n", functionFuzzLocalizedText(cfg, "Best immediate next target", "가장 먼저 볼 다음 타깃"), targetText)
		if strings.TrimSpace(rationale) != "" {
			fmt.Fprintf(&b, "- %s: %s\n", functionFuzzLocalizedText(cfg, "Why this target is a good next root", "이 타깃을 다음 루트로 추천하는 이유"), functionFuzzDisplayText(cfg, rationale))
		}
	}
	if len(run.SuggestedCommands) > 0 {
		fmt.Fprintf(&b, "- %s: `%s`\n", functionFuzzLocalizedText(cfg, "Suggested next command", "추천 다음 명령"), run.SuggestedCommands[0])
	}

	if details := functionFuzzNativeExecutionDetailLinesWithConfig(cfg, run); len(details) > 0 {
		b.WriteString("\n## " + functionFuzzLocalizedText(cfg, "Native Auto-Run Meaning", "네이티브 자동 실행 의미") + "\n\n")
		for _, item := range details {
			b.WriteString("- " + functionFuzzNormalizeDisplayText(item) + "\n")
		}
	}

	if len(run.SuggestedTargets) > 0 {
		b.WriteString("\n## " + functionFuzzLocalizedText(cfg, "Other Good Next Targets", "다른 좋은 다음 타깃") + "\n\n")
		for _, item := range limitStrings(run.SuggestedTargets, functionFuzzMaxSuggestedItems) {
			targetText, rationale := functionFuzzSplitSuggestedTargetLabel(item)
			b.WriteString("- " + targetText + "\n")
			if strings.TrimSpace(rationale) != "" {
				b.WriteString("  " + functionFuzzLocalizedText(cfg, "why", "이유") + ": " + functionFuzzDisplayText(cfg, rationale) + "\n")
			}
		}
	}

	if len(run.VirtualScenarios) > 0 {
		scenarios := functionFuzzSortedVirtualScenarios(run.VirtualScenarios)
		b.WriteString("\n## " + functionFuzzLocalizedText(cfg, "Risk-Ranked Findings", "위험도 점수표") + "\n\n")
		b.WriteString("| " + functionFuzzLocalizedText(cfg, "Score", "점수") + " | " + functionFuzzLocalizedText(cfg, "Confidence", "신뢰도") + " | " + functionFuzzLocalizedText(cfg, "Finding", "위험요소") + " | " + functionFuzzLocalizedText(cfg, "Why this rank", "이 순위의 이유") + " |\n")
		b.WriteString("| --- | --- | --- | --- |\n")
		for _, item := range limitFunctionFuzzVirtualScenarios(scenarios, 8) {
			score := functionFuzzScenarioScoreLabel(item)
			if strings.TrimSpace(score) == "" {
				score = functionFuzzLocalizedText(cfg, "unscored", "미채점")
			}
			confidence := functionFuzzDisplayText(cfg, strings.TrimSpace(item.Confidence))
			if strings.TrimSpace(confidence) == "" {
				confidence = "-"
			}
			finding := functionFuzzDisplayText(cfg, strings.TrimSpace(item.Title))
			why := functionFuzzScenarioRankWhy(cfg, item)
			if strings.TrimSpace(why) == "" {
				why = "-"
			}
			b.WriteString("| " + score + " | " + confidence + " | " + strings.ReplaceAll(finding, "|", "/") + " | " + strings.ReplaceAll(why, "|", "/") + " |\n")
		}

		b.WriteString("\n## " + functionFuzzLocalizedText(cfg, "Top Predicted Problems", "상위 예측 문제") + "\n\n")
		b.WriteString("- " + functionFuzzLocalizedText(cfg, "These are AI-generated source-only scenarios. Kernforge did not need native execution to predict them.", "이 섹션은 AI가 만든 소스 전용 시나리오입니다. Kernforge는 타깃을 실제 실행하지 않아도 이를 예측할 수 있습니다.") + "\n")
		b.WriteString("- " + functionFuzzLocalizedText(cfg, "The hypothetical input states below are Kernforge's internal modeling assumptions, not manual reproduction steps for the user.", "아래 입력 상태들은 Kernforge가 내부 분석에 사용한 가정이며, 사용자가 직접 재현하라는 절차가 아닙니다.") + "\n")
		b.WriteString("- " + functionFuzzLocalizedText(cfg, "Read each block as: Kernforge's internal hypothetical input state, what the code will likely do, what can go wrong, and the first source file to inspect.", "각 블록은 Kernforge 내부 가정 입력 상태, 코드가 할 가능성이 높은 일, 생길 수 있는 문제, 그리고 먼저 볼 소스 파일 순서로 읽으면 됩니다.") + "\n")
		for _, item := range scenarios {
			title := functionFuzzScenarioDisplayHeader(cfg, item)
			b.WriteString("\n### " + title + "\n\n")
			if why := functionFuzzScenarioRankWhy(cfg, item); strings.TrimSpace(why) != "" {
				b.WriteString("- " + functionFuzzLocalizedText(cfg, "Why this score", "이 점수의 이유") + ": " + why + "\n")
			}
			for _, line := range functionFuzzScenarioSummary(cfg, item) {
				b.WriteString("- " + line + "\n")
			}
			functionFuzzRenderScenarioExcerptMarkdown(cfg, &b, item)
		}
	}

	if len(run.CodeObservations) > 0 {
		b.WriteString("\n## " + functionFuzzLocalizedText(cfg, "Source-Derived Attack Surface", "소스 기반 공격 표면") + "\n\n")
		b.WriteString("- " + functionFuzzLocalizedText(cfg, "These are concrete guards, sinks, and unwind edges extracted from real function bodies in the mapped closure.", "이 섹션은 매핑된 closure 안의 실제 함수 본문에서 추출한 가드, sink, unwind 경로입니다.") + "\n")
		b.WriteString("- " + functionFuzzLocalizedText(cfg, "Kernforge uses these observations as the source evidence behind the attacker-style scenarios above.", "Kernforge는 위 공격자 관점 시나리오를 만들 때 이 관찰들을 직접 근거로 사용합니다.") + "\n")
		for _, item := range limitFunctionFuzzCodeObservations(functionFuzzSortedCodeObservations(run.CodeObservations), 8) {
			location := filepath.ToSlash(strings.TrimSpace(item.File))
			if item.Line > 0 {
				location += ":" + strconv.Itoa(item.Line)
			}
			b.WriteString("- " + functionFuzzFriendlyObservationKindWithConfig(cfg, item.Kind) + " @ `" + location + "`\n")
			if strings.TrimSpace(item.Evidence) != "" {
				b.WriteString("  " + functionFuzzLocalizedText(cfg, "code", "코드") + ": `" + item.Evidence + "`\n")
			}
			if strings.TrimSpace(item.WhyItMatters) != "" {
				b.WriteString("  " + functionFuzzLocalizedText(cfg, "why it matters", "중요한 이유") + ": " + functionFuzzDisplayText(cfg, item.WhyItMatters) + "\n")
			}
		}
	}

	if len(run.NextSteps) > 0 {
		b.WriteString("\n## " + functionFuzzLocalizedText(cfg, "What To Do Next", "다음에 할 일") + "\n\n")
		for _, item := range run.NextSteps {
			b.WriteString("- " + functionFuzzNormalizeDisplayText(functionFuzzDisplayText(cfg, item)) + "\n")
		}
	}

	b.WriteString("\n## " + functionFuzzLocalizedText(cfg, "Technical Details", "기술 세부 정보") + "\n\n")
	fmt.Fprintf(&b, "- Run ID: `%s`\n", run.ID)
	if strings.TrimSpace(run.ScopeMode) != "" {
		fmt.Fprintf(&b, "- Scope mode: `%s`\n", strings.TrimSpace(run.ScopeMode))
	}
	if strings.TrimSpace(run.ScopeRootFile) != "" {
		fmt.Fprintf(&b, "- Scope root file: `%s`\n", filepath.ToSlash(strings.TrimSpace(run.ScopeRootFile)))
	}
	if len(run.ScopeFiles) > 0 {
		fmt.Fprintf(&b, "- Scope files analyzed: `%d`\n", len(run.ScopeFiles))
	}
	fmt.Fprintf(&b, "- Target: `%s`\n", valueOrUnset(run.TargetSymbolName))
	fmt.Fprintf(&b, "- Symbol: `%s`\n", valueOrUnset(run.TargetSymbolID))
	if strings.TrimSpace(run.TargetSignature) != "" {
		fmt.Fprintf(&b, "- Signature: `%s`\n", run.TargetSignature)
	}
	if strings.TrimSpace(run.TargetFile) != "" {
		fmt.Fprintf(&b, "- File: `%s`\n", filepath.ToSlash(run.TargetFile))
	}
	fmt.Fprintf(&b, "- Query mode: `%s`\n", valueOrUnset(run.QueryMode))
	fmt.Fprintf(&b, "- Analysis priority: `%s`\n", functionFuzzAnalysisPrioritySummary(cfg, run))
	fmt.Fprintf(&b, "- Primary engine: `%s`\n", valueOrUnset(run.PrimaryEngine))
	if len(run.SecondaryEngines) > 0 {
		fmt.Fprintf(&b, "- Follow-up engines: `%s`\n", strings.Join(run.SecondaryEngines, "`, `"))
	}
	fmt.Fprintf(&b, "- Source observations: `%d`\n", len(run.CodeObservations))
	fmt.Fprintf(&b, "- Reachable call edges: `%d`\n", run.ReachableCallCount)
	fmt.Fprintf(&b, "- Reachable depth: `%d`\n", run.ReachableDepth)
	fmt.Fprintf(&b, "- Harness ready: `%t`\n", run.HarnessReady)
	if strings.TrimSpace(run.Execution.Status) != "" {
		fmt.Fprintf(&b, "- Auto execution: `%s`\n", functionFuzzFriendlyExecutionStatusWithConfig(cfg, run.Execution.Status))
	}
	if strings.TrimSpace(run.Execution.Reason) != "" {
		fmt.Fprintf(&b, "- Auto execution reason: %s\n", run.Execution.Reason)
	}
	if strings.TrimSpace(run.Execution.CompileContextLevel) != "" {
		fmt.Fprintf(&b, "- Compile context confidence: `%s`\n", run.Execution.CompileContextLevel)
	}
	if strings.TrimSpace(run.Execution.CompileCommandSource) != "" {
		fmt.Fprintf(&b, "- Compile context source: `%s`\n", run.Execution.CompileCommandSource)
	}

	if len(run.ParameterStrategies) > 0 {
		b.WriteString("\n## " + functionFuzzLocalizedText(cfg, "Parameter Strategy", "파라미터 전략") + "\n\n")
		for _, item := range run.ParameterStrategies {
			fmt.Fprintf(&b, "- `%s` : `%s` -> `%s`\n", valueOrUnset(item.Name), valueOrUnset(item.RawType), functionFuzzFriendlyParamClassWithConfig(cfg, item.Class))
			if disposition := functionFuzzParameterDisposition(cfg, item); disposition != "" {
				fmt.Fprintf(&b, "  disposition: %s\n", disposition)
			}
			if strings.TrimSpace(item.Relation) != "" {
				fmt.Fprintf(&b, "  relation: `%s`\n", item.Relation)
			}
			if len(item.Mutators) > 0 {
				fmt.Fprintf(&b, "  mutators: `%s`\n", strings.Join(item.Mutators, "`, `"))
			}
		}
	}

	b.WriteString("\n" + functionFuzzRenderSignalsMarkdown(cfg, run, 8))

	if len(closure.Symbols) > 0 {
		b.WriteString("\n## Reachable closure\n\n")
		for _, item := range limitFunctionFuzzSymbols(closure.Symbols, functionFuzzMaxListedItems) {
			line := fmt.Sprintf("- `%s`", functionFuzzDisplayName(item))
			if strings.TrimSpace(item.Kind) != "" {
				line += " [" + strings.TrimSpace(item.Kind) + "]"
			}
			if strings.TrimSpace(item.File) != "" {
				line += "  " + filepath.ToSlash(item.File)
			}
			b.WriteString(line + "\n")
		}
	}

	if len(run.Notes) > 0 {
		b.WriteString("\n## " + functionFuzzLocalizedText(cfg, "Notes", "메모") + "\n\n")
		for _, item := range run.Notes {
			b.WriteString("- " + compactPersistentMemoryText(functionFuzzDisplayText(cfg, item), 220) + "\n")
		}
	}

	if len(run.Execution.MissingSettings) > 0 {
		b.WriteString("\n## " + functionFuzzLocalizedText(cfg, "Missing settings", "누락된 설정") + "\n\n")
		for _, item := range run.Execution.MissingSettings {
			b.WriteString("- " + item + "\n")
		}
	}

	if len(run.Execution.RecoveryNotes) > 0 {
		b.WriteString("\n## " + functionFuzzLocalizedText(cfg, "Recovered build context", "복구된 빌드 컨텍스트") + "\n\n")
		for _, item := range run.Execution.RecoveryNotes {
			b.WriteString("- " + compactPersistentMemoryText(item, 220) + "\n")
		}
	}

	if strings.TrimSpace(run.Execution.BuildCommand) != "" || strings.TrimSpace(run.Execution.RunCommand) != "" {
		b.WriteString("\n## " + functionFuzzLocalizedText(cfg, "Autonomous execution", "자동 실행") + "\n\n")
		if strings.TrimSpace(run.Execution.CompilerResolvedPath) != "" {
			fmt.Fprintf(&b, "- Compiler: `%s`\n", run.Execution.CompilerResolvedPath)
		}
		if strings.TrimSpace(run.Execution.TranslationUnit) != "" {
			fmt.Fprintf(&b, "- Translation unit: `%s`\n", run.Execution.TranslationUnit)
		}
		if strings.TrimSpace(run.Execution.BuildCommand) != "" {
			fmt.Fprintf(&b, "- Build command: `%s`\n", run.Execution.BuildCommand)
		}
		if strings.TrimSpace(run.Execution.RunCommand) != "" {
			fmt.Fprintf(&b, "- Run command: `%s`\n", run.Execution.RunCommand)
		}
		if strings.TrimSpace(run.Execution.BuildScriptPath) != "" {
			fmt.Fprintf(&b, "- Runner script: `%s`\n", run.Execution.BuildScriptPath)
		}
		if strings.TrimSpace(run.Execution.BackgroundJobID) != "" {
			fmt.Fprintf(&b, "- Background job: `%s`\n", run.Execution.BackgroundJobID)
		}
		if strings.TrimSpace(run.Execution.ContinueCommand) != "" {
			fmt.Fprintf(&b, "- Continue command: `%s`\n", run.Execution.ContinueCommand)
		}
	}

	b.WriteString("\n## " + functionFuzzLocalizedText(cfg, "Artifacts", "산출물") + "\n\n")
	fmt.Fprintf(&b, "- Plan JSON: `%s`\n", run.PlanPath)
	fmt.Fprintf(&b, "- Report: `%s`\n", run.ReportPath)
	fmt.Fprintf(&b, "- Harness: `%s`\n", run.HarnessPath)
	return strings.TrimSpace(b.String()) + "\n"
}

func renderFunctionFuzzReportMarkdown(run FunctionFuzzRun, closure functionFuzzClosure) string {
	return renderFunctionFuzzReportMarkdownWithConfig(run, closure, functionFuzzEnglishConfig())
}

func renderFunctionFuzzHarness(run FunctionFuzzRun) string {
	var b strings.Builder
	b.WriteString("#include <cstddef>\n")
	b.WriteString("#include <cstdint>\n")
	b.WriteString("#include <cstring>\n")
	b.WriteString("#include <string>\n")
	b.WriteString("#include <vector>\n\n")
	b.WriteString("// Generated by Kernforge function fuzz planning.\n")
	b.WriteString("// Replace the target declaration block with real project headers before compiling.\n\n")
	b.WriteString("namespace\n")
	b.WriteString("{\n")
	b.WriteString("struct FuzzInputView\n")
	b.WriteString("{\n")
	b.WriteString("    const uint8_t* data;\n")
	b.WriteString("    size_t size;\n")
	b.WriteString("    size_t offset;\n")
	b.WriteString("};\n\n")
	b.WriteString("bool ReadBytes(FuzzInputView& input, void* dst, size_t size)\n")
	b.WriteString("{\n")
	b.WriteString("    bool ok = false;\n")
	b.WriteString("    do\n")
	b.WriteString("    {\n")
	b.WriteString("        if (dst == nullptr)\n")
	b.WriteString("        {\n")
	b.WriteString("            break;\n")
	b.WriteString("        }\n")
	b.WriteString("        if (input.offset > input.size)\n")
	b.WriteString("        {\n")
	b.WriteString("            break;\n")
	b.WriteString("        }\n")
	b.WriteString("        if (size > input.size - input.offset)\n")
	b.WriteString("        {\n")
	b.WriteString("            break;\n")
	b.WriteString("        }\n")
	b.WriteString("        if (size != 0)\n")
	b.WriteString("        {\n")
	b.WriteString("            memcpy(dst, input.data + input.offset, size);\n")
	b.WriteString("        }\n")
	b.WriteString("        input.offset += size;\n")
	b.WriteString("        ok = true;\n")
	b.WriteString("    }\n")
	b.WriteString("    while (false);\n")
	b.WriteString("    return ok;\n")
	b.WriteString("}\n\n")
	b.WriteString("template <typename T>\n")
	b.WriteString("T ReadScalar(FuzzInputView& input)\n")
	b.WriteString("{\n")
	b.WriteString("    T value{};\n")
	b.WriteString("    (void) ReadBytes(input, &value, sizeof(value));\n")
	b.WriteString("    return value;\n")
	b.WriteString("}\n\n")
	b.WriteString("size_t ReadSize(FuzzInputView& input, size_t cap)\n")
	b.WriteString("{\n")
	b.WriteString("    size_t value = ReadScalar<size_t>(input);\n")
	b.WriteString("    if (cap != 0 && value > cap)\n")
	b.WriteString("    {\n")
	b.WriteString("        value = value % cap;\n")
	b.WriteString("    }\n")
	b.WriteString("    return value;\n")
	b.WriteString("}\n\n")
	b.WriteString("std::vector<uint8_t> ReadByteVector(FuzzInputView& input, size_t cap)\n")
	b.WriteString("{\n")
	b.WriteString("    size_t wanted = ReadSize(input, cap);\n")
	b.WriteString("    size_t remaining = 0;\n")
	b.WriteString("    if (input.offset <= input.size)\n")
	b.WriteString("    {\n")
	b.WriteString("        remaining = input.size - input.offset;\n")
	b.WriteString("    }\n")
	b.WriteString("    if (wanted > remaining)\n")
	b.WriteString("    {\n")
	b.WriteString("        wanted = remaining;\n")
	b.WriteString("    }\n")
	b.WriteString("    std::vector<uint8_t> out(wanted);\n")
	b.WriteString("    if (wanted != 0)\n")
	b.WriteString("    {\n")
	b.WriteString("        (void) ReadBytes(input, out.data(), wanted);\n")
	b.WriteString("    }\n")
	b.WriteString("    return out;\n")
	b.WriteString("}\n\n")
	b.WriteString("std::string ReadString(FuzzInputView& input, size_t cap)\n")
	b.WriteString("{\n")
	b.WriteString("    std::vector<uint8_t> bytes = ReadByteVector(input, cap);\n")
	b.WriteString("    return std::string(bytes.begin(), bytes.end());\n")
	b.WriteString("}\n")
	b.WriteString("}\n\n")

	if run.HarnessReady {
		b.WriteString("// Detected free-function style signature.\n")
		b.WriteString("// Replace this declaration with the correct project include when wiring the harness.\n")
		b.WriteString(strings.TrimSpace(run.TargetSignature))
		b.WriteString(";\n\n")
	} else {
		b.WriteString("// The detected target needs additional binding work before this harness can compile.\n")
		b.WriteString("// Target signature:\n")
		if strings.TrimSpace(run.TargetSignature) != "" {
			b.WriteString("// ")
			b.WriteString(strings.TrimSpace(run.TargetSignature))
			b.WriteString("\n")
		} else {
			b.WriteString("// Signature was not available in the semantic index.\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("extern \"C\" int LLVMFuzzerTestOneInput(const uint8_t* data, size_t size)\n")
	b.WriteString("{\n")
	b.WriteString("    int result = 0;\n")
	b.WriteString("    do\n")
	b.WriteString("    {\n")
	b.WriteString("        if (data == nullptr)\n")
	b.WriteString("        {\n")
	b.WriteString("            break;\n")
	b.WriteString("        }\n")
	b.WriteString("\n")
	b.WriteString("        FuzzInputView input{data, size, 0};\n")
	b.WriteString("\n")
	for _, item := range run.ParameterStrategies {
		for _, line := range functionFuzzHarnessParamLines(item) {
			b.WriteString("        ")
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if run.HarnessReady {
		callName := functionFuzzSignatureCallName(run.TargetSignature)
		returnType := functionFuzzSignatureReturnType(run.TargetSignature)
		args := make([]string, 0, len(run.ParameterStrategies))
		for _, item := range run.ParameterStrategies {
			args = append(args, functionFuzzHarnessCallArg(item))
		}
		if strings.EqualFold(strings.TrimSpace(returnType), "void") {
			fmt.Fprintf(&b, "        %s(%s);\n", callName, strings.Join(args, ", "))
		} else {
			fmt.Fprintf(&b, "        auto target_result = %s(%s);\n", callName, strings.Join(args, ", "))
			b.WriteString("        (void) target_result;\n")
		}
	} else {
		b.WriteString("        // TODO: bind the real target declaration, headers, and object setup.\n")
		b.WriteString("        // The generated parameter extraction below is still useful as a starting point.\n")
	}

	b.WriteString("    }\n")
	b.WriteString("    while (false);\n")
	b.WriteString("    return result;\n")
	b.WriteString("}\n")
	return b.String()
}

func functionFuzzHarnessParamLines(item FunctionFuzzParamStrategy) []string {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = fmt.Sprintf("arg%d", item.Index)
	}
	rawType := strings.TrimSpace(item.RawType)
	if rawType == "" {
		rawType = "uint8_t"
	}
	switch item.Class {
	case "boolean":
		return []string{fmt.Sprintf("bool %s = (ReadScalar<uint8_t>(input) & 1u) != 0;", name)}
	case "scalar_float", "scalar_int":
		return []string{fmt.Sprintf("%s %s = ReadScalar<%s>(input);", rawType, name, rawType)}
	case "enum_or_flags":
		return []string{
			fmt.Sprintf("uint32_t %s_raw = ReadScalar<uint32_t>(input);", name),
			fmt.Sprintf("%s %s = static_cast<%s>(%s_raw);", rawType, name, rawType, name),
		}
	case "length":
		return []string{fmt.Sprintf("%s %s = static_cast<%s>(ReadSize(input, 0x1000));", rawType, name, rawType)}
	case "string":
		return []string{fmt.Sprintf("std::string %s = ReadString(input, 0x400);", name)}
	case "buffer", "pointer":
		storage := name + "_storage"
		return []string{
			fmt.Sprintf("std::vector<uint8_t> %s = ReadByteVector(input, 0x1000);", storage),
			fmt.Sprintf("%s %s = %s.empty() ? nullptr : reinterpret_cast<%s>(%s.data());", rawType, name, storage, rawType, storage),
		}
	case "container":
		storage := name + "_storage"
		return []string{
			fmt.Sprintf("std::vector<uint8_t> %s = ReadByteVector(input, 0x1000);", storage),
			fmt.Sprintf("// TODO: map %s into the container type %s.", storage, rawType),
		}
	case "handle":
		return []string{
			fmt.Sprintf("// TODO: replace this placeholder with a target-specific handle strategy for %s.", name),
			fmt.Sprintf("%s %s{};", rawType, name),
		}
	case "object":
		return []string{
			fmt.Sprintf("// TODO: infer a builder or factory for object parameter %s.", name),
			fmt.Sprintf("%s %s{};", rawType, name),
		}
	default:
		return []string{
			fmt.Sprintf("// TODO: raw byte mapping is still required for %s.", name),
		}
	}
}

func functionFuzzHarnessCallArg(item FunctionFuzzParamStrategy) string {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = fmt.Sprintf("arg%d", item.Index)
	}
	if item.Class == "string" {
		lowerType := strings.ToLower(strings.TrimSpace(item.RawType))
		switch {
		case containsAny(lowerType, "string_view"):
			return fmt.Sprintf("std::string_view(%s)", name)
		case containsAny(lowerType, "char*", "const char*"):
			return fmt.Sprintf("%s.c_str()", name)
		}
	}
	return name
}

func functionFuzzSignatureCallName(signature string) string {
	signature = functionFuzzSanitizeSignature(signature)
	if signature == "" {
		return "TargetFunction"
	}
	open := strings.Index(signature, "(")
	if open < 0 {
		return "TargetFunction"
	}
	prefix := strings.TrimSpace(signature[:open])
	if prefix == "" {
		return "TargetFunction"
	}
	fields := strings.Fields(prefix)
	if len(fields) == 0 {
		return "TargetFunction"
	}
	return fields[len(fields)-1]
}

func functionFuzzSignatureReturnType(signature string) string {
	signature = functionFuzzSanitizeSignature(signature)
	open := strings.Index(signature, "(")
	if open < 0 {
		return ""
	}
	prefix := strings.TrimSpace(signature[:open])
	callName := functionFuzzSignatureCallName(signature)
	if callName == "" {
		return prefix
	}
	idx := strings.LastIndex(prefix, callName)
	if idx < 0 {
		return prefix
	}
	return strings.TrimSpace(prefix[:idx])
}

func limitFunctionFuzzSignals(items []FunctionFuzzSinkSignal, limit int) []FunctionFuzzSinkSignal {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitFunctionFuzzSymbols(items []SymbolRecord, limit int) []SymbolRecord {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitFunctionFuzzVirtualScenarios(items []FunctionFuzzVirtualScenario, limit int) []FunctionFuzzVirtualScenario {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitFunctionFuzzCodeObservations(items []FunctionFuzzCodeObservation, limit int) []FunctionFuzzCodeObservation {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func (rt *runtimeState) recentFunctionFuzzIDs() []string {
	if rt == nil || rt.functionFuzz == nil {
		return nil
	}
	items, err := rt.functionFuzz.ListRecent(workspaceSnapshotRoot(rt.workspace), 12)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func functionFuzzMin(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func functionFuzzMax(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

func functionFuzzAbs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func functionFuzzNormalizeOptionalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}
