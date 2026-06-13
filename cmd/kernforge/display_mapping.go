package main

import (
	"strconv"
	"strings"
)

// display_mapping.go centralizes the translation of internal enum/codename
// values into plain, locale-aware human language for user-facing output.
//
// Rules:
//   - These helpers produce the HUMAN line only. Raw enum values must still be
//     written into machine/JSON/debug fields unchanged.
//   - Every helper takes a korean bool so callers can reuse their existing
//     prefersKorean / reviewRunPrefersKorean / localePrefersKorean signal.
//   - Unknown values fall back to a readable form of the raw token rather than
//     dropping information.

// humanizeEnumFallback turns a raw snake_case/camel enum token into a readable
// phrase when no explicit mapping exists. It never returns an empty string for
// a non-empty input.
func humanizeEnumFallback(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	replaced := strings.ReplaceAll(trimmed, "_", " ")
	replaced = strings.ReplaceAll(replaced, "-", " ")
	return strings.TrimSpace(replaced)
}

// localizedReviewText picks the Korean or English label for a user-facing line
// when the caller has already resolved the locale signal into a bool.
func localizedReviewText(korean bool, english string, koreanText string) string {
	if korean {
		return koreanText
	}
	return english
}

// humanizeBlockerClass maps a blocker class codename to a short localized label.
func humanizeBlockerClass(value string, korean bool) string {
	switch strings.TrimSpace(value) {
	case reviewBlockerClassCodeRepair:
		if korean {
			return "코드 수정 필요"
		}
		return "code fix needed"
	case reviewBlockerClassReviewerRouteProblem:
		if korean {
			return "리뷰어 경로 문제"
		}
		return "reviewer route problem"
	case reviewBlockerClassEvidenceGap:
		if korean {
			return "근거 부족"
		}
		return "missing evidence"
	case reviewBlockerClassVerificationGap:
		if korean {
			return "검증 부족"
		}
		return "missing verification"
	case reviewBlockerClassDocumentArtifactQuality:
		if korean {
			return "문서 품질"
		}
		return "document quality"
	case reviewBlockerClassFinalAnswerContract:
		if korean {
			return "최종 답변 점검"
		}
		return "final-answer check"
	case reviewBlockerClassUserDecisionRequired:
		if korean {
			return "사용자 결정 필요"
		}
		return "user decision needed"
	default:
		return humanizeEnumFallback(value)
	}
}

// humanizeBlockerSentence renders a single operator blocker as a full sentence:
// a localized class label followed by the plain reason it blocks. The raw class
// token stays in machine fields.
func humanizeBlockerSentence(blocker ReviewOperatorBlocker, korean bool) string {
	label := humanizeBlockerClass(blocker.Class, korean)
	why := strings.TrimSpace(firstNonBlankString(blocker.WhyBlocks, blocker.Title))
	if label == "" && why == "" {
		return ""
	}
	if why == "" {
		return label
	}
	if label == "" {
		return why
	}
	return label + ": " + why
}

// humanizeReviewVerdict maps review gate/result verdicts to plain language.
func humanizeReviewVerdict(value string, korean bool) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case reviewVerdictApproved:
		if korean {
			return "승인"
		}
		return "approved"
	case reviewVerdictApprovedWithWarnings:
		if korean {
			return "경고와 함께 승인"
		}
		return "approved with warnings"
	case reviewVerdictNeedsRevision:
		if korean {
			return "수정 필요"
		}
		return "needs revision"
	case reviewVerdictBlocked:
		if korean {
			return "차단됨"
		}
		return "blocked"
	case reviewVerdictInsufficientEvidence:
		if korean {
			return "근거 부족"
		}
		return "insufficient evidence"
	default:
		return humanizeEnumFallback(value)
	}
}

// humanizeReviewTarget maps review targets to plain language.
func humanizeReviewTarget(value string, korean bool) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case reviewTargetAuto:
		if korean {
			return "자동 선택"
		}
		return "auto"
	case reviewTargetPlan:
		if korean {
			return "계획"
		}
		return "plan"
	case reviewTargetChange:
		if korean {
			return "코드 변경"
		}
		return "code change"
	case reviewTargetSelection:
		if korean {
			return "선택 영역"
		}
		return "selection"
	case reviewTargetPR:
		if korean {
			return "PR"
		}
		return "pull request"
	case reviewTargetFinal, reviewTargetFinalAlias:
		if korean {
			return "최종 답변"
		}
		return "final answer"
	case reviewTargetGoal, reviewTargetGoalAlias:
		if korean {
			return "목표 반복"
		}
		return "goal iteration"
	case reviewTargetAnalysis, reviewTargetAnalysisAlias:
		if korean {
			return "분석 보고서"
		}
		return "analysis report"
	case reviewTargetSourceAnalysis:
		if korean {
			return "소스 코드 분석"
		}
		return "source analysis"
	default:
		return humanizeEnumFallback(value)
	}
}

// humanizeReviewMode maps review modes to plain language. The internal "mode"
// is a routing/quality signal; surface it as the kind of change reviewed.
func humanizeReviewMode(value string, korean bool) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case reviewModeCoreBuild:
		if korean {
			return "핵심 기능 구현"
		}
		return "core build"
	case reviewModeLiveFix:
		if korean {
			return "실시간 수정"
		}
		return "live fix"
	case reviewModeResearch:
		if korean {
			return "리서치"
		}
		return "research"
	case reviewModeRefactor:
		if korean {
			return "리팩터링"
		}
		return "refactor"
	case reviewModeSecurityHardening:
		if korean {
			return "보안 강화"
		}
		return "security hardening"
	case reviewModeUIPolish:
		if korean {
			return "UI 다듬기"
		}
		return "UI polish"
	case reviewModePerformanceAnalysis:
		if korean {
			return "성능 분석"
		}
		return "performance analysis"
	case reviewModeGeneralChange:
		if korean {
			return "일반 변경"
		}
		return "general change"
	default:
		return humanizeEnumFallback(value)
	}
}

// humanizeRequestClass maps the request classification to plain language plus a
// short gloss the user can understand.
func humanizeRequestClass(value string, korean bool) string {
	switch normalizeReviewRequestClass(value) {
	case reviewRequestClassGeneral:
		if korean {
			return "일반 작업"
		}
		return "general task"
	case reviewRequestClassReviewOnly:
		if korean {
			return "리뷰만 (코드 수정 없음)"
		}
		return "review only (no edits)"
	case reviewRequestClassDocumentArtifact:
		if korean {
			return "문서 작성"
		}
		return "document artifact"
	case reviewRequestClassReviewThenModify:
		if korean {
			return "먼저 리뷰 후 수정"
		}
		return "review, then modify"
	case reviewRequestClassModifyThenReview:
		if korean {
			return "수정 후 리뷰"
		}
		return "modify, then review"
	case reviewRequestClassVerificationOnly:
		if korean {
			return "검증만"
		}
		return "verification only"
	case reviewRequestClassValidationOnly:
		if korean {
			return "검토/확인만"
		}
		return "validation only"
	default:
		return humanizeEnumFallback(value)
	}
}

// humanizeLifecycleKind maps a review lifecycle phase to plain language. Used by
// situation snapshots and operator status when surfaced to the user.
func humanizeLifecycleKind(value string, korean bool) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case reviewLifecyclePhaseClassifiedRequest:
		if korean {
			return "요청 분류 중"
		}
		return "classifying request"
	case reviewLifecyclePhaseCollectingContext:
		if korean {
			return "맥락 수집 중"
		}
		return "collecting context"
	case reviewLifecyclePhasePreWriteReview:
		if korean {
			return "수정 전 리뷰"
		}
		return "pre-write review"
	case reviewLifecyclePhaseApplyingChange:
		if korean {
			return "변경 적용 중"
		}
		return "applying change"
	case reviewLifecyclePhasePostChangeReview:
		if korean {
			return "변경 후 리뷰"
		}
		return "post-change review"
	case reviewLifecyclePhaseSingleModelSecondPass:
		if korean {
			return "추가 검토 진행 중"
		}
		return "second-pass review"
	case reviewLifecyclePhaseCrossReviewTriage:
		if korean {
			return "교차 리뷰 정리 중"
		}
		return "cross-review triage"
	case reviewLifecyclePhaseArtifactQualityGate:
		if korean {
			return "산출물 품질 확인 중"
		}
		return "artifact quality check"
	case reviewLifecyclePhaseVerification:
		if korean {
			return "검증 중"
		}
		return "verification"
	case reviewLifecyclePhaseFinalAnswerContract:
		if korean {
			return "최종 답변 확인 중"
		}
		return "final-answer check"
	case reviewLifecyclePhaseBlocked:
		if korean {
			return "막힘"
		}
		return "blocked"
	case reviewLifecyclePhaseCompleted:
		if korean {
			return "완료"
		}
		return "completed"
	default:
		return humanizeEnumFallback(value)
	}
}

// humanizeRouteMode maps a model route mode to plain language.
func humanizeRouteMode(value string, korean bool) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "single":
		if korean {
			return "단일 모델"
		}
		return "single model"
	case "primary_fallback", "fallback":
		if korean {
			return "기본 모델 우선, 실패 시 대체"
		}
		return "primary with backup"
	case "race", "parallel":
		if korean {
			return "여러 모델 동시 실행"
		}
		return "parallel models"
	case "round_robin":
		if korean {
			return "모델 번갈아 사용"
		}
		return "round-robin models"
	default:
		return humanizeEnumFallback(value)
	}
}

// humanizeRouteQuality maps a route/model quality signal to plain language.
func humanizeRouteQuality(value string, korean bool) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case reviewModelQualityStrong:
		if korean {
			return "강함"
		}
		return "strong"
	case reviewModelQualityUsable:
		if korean {
			return "사용 가능"
		}
		return "usable"
	case reviewModelQualityWeak:
		if korean {
			return "약함"
		}
		return "weak"
	case reviewModelQualityFailed:
		if korean {
			return "실패"
		}
		return "failed"
	default:
		return humanizeEnumFallback(value)
	}
}

// humanizeGateStatus maps a runtime gate status to plain language for the human
// line. The raw enum stays in machine fields.
func humanizeGateStatus(value string, korean bool) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case runtimeGateStatusReady:
		if korean {
			return "준비됨"
		}
		return "ready"
	case runtimeGateStatusNeedsReview:
		if korean {
			return "리뷰 필요"
		}
		return "needs review"
	case runtimeGateStatusBlocked:
		if korean {
			return "막힘"
		}
		return "blocked"
	case "", "unknown":
		if korean {
			return "알 수 없음"
		}
		return "unknown"
	default:
		return humanizeEnumFallback(value)
	}
}

// humanizeGateAction maps a review gate action to plain language.
func humanizeGateAction(value string, korean bool) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case reviewGateActionRepairRequired:
		if korean {
			return "수정 필요"
		}
		return "repair required"
	case reviewGateActionReviewerUnavailable:
		if korean {
			return "리뷰어 사용 불가"
		}
		return "reviewer unavailable"
	case reviewGateActionUserDecisionRequired:
		if korean {
			return "사용자 결정 필요"
		}
		return "user decision required"
	case reviewGateActionDiffPreviewAllowed:
		if korean {
			return "변경 미리보기 가능"
		}
		return "diff preview allowed"
	case reviewGateActionVerificationRequired:
		if korean {
			return "검증 필요"
		}
		return "verification required"
	case reviewGateActionFinalSummary:
		if korean {
			return "최종 요약"
		}
		return "final summary"
	case "proceed_with_disclosure":
		if korean {
			return "주의 사항과 함께 진행"
		}
		return "proceed with disclosure"
	default:
		return humanizeEnumFallback(value)
	}
}

// humanizeInterventionKind maps a runtime intervention codename to a localized
// phrase. A generic fallback covers any kind without an explicit mapping.
func humanizeInterventionKind(value string, korean bool) string {
	switch RuntimeInterventionKind(strings.TrimSpace(value)) {
	case RuntimeInterventionBlockedTool:
		if korean {
			return "차단된 도구 호출을 정리했습니다"
		}
		return "handled a blocked tool call"
	case RuntimeInterventionRepeatedTool:
		if korean {
			return "같은 도구를 반복 호출해 흐름을 바로잡았습니다"
		}
		return "corrected repeated tool calls"
	case RuntimeInterventionEmptyStop:
		if korean {
			return "빈 응답으로 멈춰 다시 진행하도록 유도했습니다"
		}
		return "nudged past an empty stop"
	case RuntimeInterventionLengthStop:
		if korean {
			return "답변이 길이 제한에 걸려 이어서 진행했습니다"
		}
		return "continued past a length cutoff"
	case RuntimeInterventionCommentaryOnly:
		if korean {
			return "설명만 반복돼 실제 작업을 다시 요청했습니다"
		}
		return "asked the model to act instead of only commenting"
	case RuntimeInterventionManualEditHandoff:
		if korean {
			return "수동 편집 떠넘기기를 막고 직접 수정하도록 했습니다"
		}
		return "blocked a manual-edit handoff"
	case RuntimeInterventionVerificationUnresolved:
		if korean {
			return "검증이 끝나지 않아 마무리를 보류했습니다"
		}
		return "held the finish until verification completes"
	case RuntimeInterventionFinalLooksPremature:
		if korean {
			return "마무리가 이른 것 같아 한 번 더 확인했습니다"
		}
		return "rechecked a premature-looking finish"
	default:
		if trimmed := humanizeEnumFallback(value); trimmed != "" {
			if korean {
				return "런타임 보정을 적용했습니다 (" + trimmed + ")"
			}
			return "applied a runtime correction (" + trimmed + ")"
		}
		if korean {
			return "런타임 보정을 적용했습니다"
		}
		return "applied a runtime correction"
	}
}

// humanizeModelReviewSkipReason maps a model-review skip reason to a plain
// sentence explaining why only deterministic checks ran.
func humanizeModelReviewSkipReason(value string, korean bool) string {
	reason := strings.TrimSpace(value)
	// Some reasons carry a trailing detail after a colon; key off the prefix.
	key := reason
	if idx := strings.Index(key, ":"); idx >= 0 {
		key = strings.TrimSpace(key[:idx])
	}
	switch key {
	case modelReviewSkipByUser:
		if korean {
			return "사용자가 모델 리뷰를 건너뛰어 결정적 검사만 수행했습니다."
		}
		return "Model review was skipped at your request; only deterministic checks ran."
	case modelReviewSkipNoInteractiveConsent:
		if korean {
			return "대화형 동의를 받을 수 없어 모델 리뷰 없이 결정적 검사만 수행했습니다."
		}
		return "No interactive consent was available, so only deterministic checks ran."
	case modelReviewSkipConfigNever:
		if korean {
			return "설정에서 모델 리뷰를 끄도록 되어 있어 결정적 검사만 수행했습니다."
		}
		return "Model review is disabled in settings; only deterministic checks ran."
	case modelReviewSkipReadOnlyBoundary:
		if korean {
			return "읽기 전용 작업이라 모델 리뷰 없이 결정적 검사만 수행했습니다."
		}
		return "This was a read-only step, so only deterministic checks ran."
	case modelReviewSkipTurnBudgetExceeded:
		if korean {
			return "이번 턴의 모델 리뷰 한도를 초과해 결정적 검사만 수행했습니다."
		}
		return "The model-review budget for this turn was used up; only deterministic checks ran."
	default:
		if korean {
			return "모델 리뷰를 건너뛰고 결정적 검사만 수행했습니다."
		}
		return "Model review was skipped; only deterministic checks ran."
	}
}

// finalAnswerCompletenessTitleKorean maps a final-answer completeness/contract
// finding TITLE (the stable English code) to localized Korean. These titles are
// internal vocabulary ("document artifact", "cross-review triage ledger",
// "patch transaction") that must not leak into a Korean reply. English titles
// pass through unchanged; an unmapped title returns "" so callers keep the
// original.
var finalAnswerCompletenessTitleKorean = map[string]string{
	"Changed-file summary is missing":                                "변경된 파일 목록이 빠졌습니다",
	"Final answer contradicts the patch transaction":                 "최종 답변이 실제 변경 내역과 어긋납니다",
	"Review result is missing":                                       "리뷰 결과가 빠졌습니다",
	"Validation result is missing":                                   "검증 결과가 빠졌습니다",
	"Required verification has no outcome":                           "필요한 검증의 결과가 빠졌습니다",
	"Unresolved verification failure":                                "해결되지 않은 검증 실패가 있습니다",
	"Verification was not run disclosure missing":                    "검증을 실행하지 않았다는 안내가 빠졌습니다",
	"Generated document artifact verification disclosure is missing": "생성한 문서의 검증 여부 안내가 빠졌습니다",
	"Verification claim has no recorded evidence":                    "검증했다는 주장에 대한 근거가 없습니다",
	"Edit loop verification failure is unstated":                     "수정 과정의 검증 실패가 언급되지 않았습니다",
	"Remaining-risk statement is missing":                            "남은 위험에 대한 설명이 빠졌습니다",
	"Final answer contradicts remaining edit-loop risk":              "최종 답변이 남은 위험과 어긋납니다",
	"Edit loop remaining risk is omitted":                            "수정 과정의 남은 위험이 빠졌습니다",
	"No-finding review omits residual risk":                          "발견 사항이 없다는 리뷰에 남은 위험 언급이 빠졌습니다",
	"Cross-review residual risk is undisclosed":                      "교차 리뷰에서 남은 위험이 안내되지 않았습니다",
	"Document artifact limitation statement is missing":              "생성한 문서의 한계에 대한 설명이 빠졌습니다",
	"Review-only answer is not findings-first":                       "리뷰 전용 답변이 발견 사항부터 제시하지 않았습니다",
	"Review-only no-edit statement is missing":                       "파일을 수정하지 않았다는 안내가 빠졌습니다",
	"Document artifact path is missing":                              "생성한 문서 경로가 빠졌습니다",
	"Document artifact quality status is missing":                    "생성한 문서의 품질 점검 상태가 빠졌습니다",
	"Document artifact verification disclosure is missing":           "생성한 문서의 검증 여부 안내가 빠졌습니다",
}

// humanizeFinalAnswerCompletenessTitle localizes a completeness/contract finding
// title at render time. The raw English title stays in machine fields; this only
// feeds the human line. Unknown titles fall through unchanged so no information
// is lost.
func humanizeFinalAnswerCompletenessTitle(title string, korean bool) string {
	trimmed := strings.TrimSpace(title)
	if !korean || trimmed == "" {
		return trimmed
	}
	if localized, ok := finalAnswerCompletenessTitleKorean[trimmed]; ok {
		return localized
	}
	return trimmed
}

// humanizeFinalGateState maps a final-gate state enum to a short localized
// headline explaining why the final answer was held. The raw enum stays in the
// machine FinalGateDecision.State field.
func humanizeFinalGateState(state FinalGateState, korean bool) string {
	switch state {
	case FinalGateNeedsVerification:
		if korean {
			return "검증이 끝나지 않아 마무리를 보류했습니다"
		}
		return "Held the final answer until verification is resolved"
	case FinalGateNeedsReview:
		if korean {
			return "리뷰에서 미해결 항목이 남아 마무리를 보류했습니다"
		}
		return "Held the final answer until the review findings are resolved"
	case FinalGateNeedsRecovery:
		if korean {
			return "런타임 보정이 끝나지 않아 마무리를 보류했습니다"
		}
		return "Held the final answer until the runtime issue is resolved"
	case FinalGateNeedsUserConfirmation:
		if korean {
			return "사용자 확인이 필요해 마무리를 보류했습니다"
		}
		return "Held the final answer until you confirm"
	case FinalGateBlocked:
		if korean {
			return "작업이 막혀 마무리를 보류했습니다"
		}
		return "Held the final answer because the turn is blocked"
	default:
		if korean {
			return "마무리를 보류했습니다"
		}
		return "Held the final answer"
	}
}

// reviewFindingNoun returns the localized noun for a review finding ("항목" in
// Korean). Use it instead of embedding the bare English word "finding" in
// localized prose.
func reviewFindingNoun(korean bool) string {
	if korean {
		return "항목"
	}
	return "finding"
}

// reviewFindingNounPlural returns the localized plural finding noun.
func reviewFindingNounPlural(korean bool) string {
	if korean {
		return "항목"
	}
	return "findings"
}

// reviewFindingCountPhrase renders a localized "N findings - blockers X,
// warnings Y[, notes Z]" line, omitting any zero segment. total is the total
// finding count.
func reviewFindingCountPhrase(total int, blockers int, warnings int, notes int, korean bool) string {
	if korean {
		segments := []string{}
		if blockers > 0 {
			segments = append(segments, "차단 "+strconv.Itoa(blockers))
		}
		if warnings > 0 {
			segments = append(segments, "경고 "+strconv.Itoa(warnings))
		}
		if notes > 0 {
			segments = append(segments, "참고 "+strconv.Itoa(notes))
		}
		head := "총 " + strconv.Itoa(total) + "건"
		if len(segments) == 0 {
			return head
		}
		return head + " - " + strings.Join(segments, ", ")
	}
	segments := []string{}
	if blockers > 0 {
		segments = append(segments, "blockers "+strconv.Itoa(blockers))
	}
	if warnings > 0 {
		segments = append(segments, "warnings "+strconv.Itoa(warnings))
	}
	if notes > 0 {
		segments = append(segments, "notes "+strconv.Itoa(notes))
	}
	head := strconv.Itoa(total) + " total"
	if len(segments) == 0 {
		return head
	}
	return head + " - " + strings.Join(segments, ", ")
}
