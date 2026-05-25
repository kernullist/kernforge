package main

import "strings"

type TurnIntent string

const (
	TurnIntentGeneral             TurnIntent = "general"
	TurnIntentDiagnoseRecentError TurnIntent = "diagnose_recent_error"
	TurnIntentContinueLastTask    TurnIntent = "continue_last_task"
	TurnIntentExplainCurrentState TurnIntent = "explain_current_state"
	TurnIntentAskProjectKnowledge TurnIntent = "ask_project_knowledge"
	TurnIntentReviewCode          TurnIntent = "review_code"
	TurnIntentEditCode            TurnIntent = "edit_code"
	TurnIntentRunCommand          TurnIntent = "run_command"
	TurnIntentPlanOrDesign        TurnIntent = "plan_or_design"
)

func classifyTurnIntent(text string) TurnIntent {
	base := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if base == "" {
		base = strings.ToLower(strings.TrimSpace(text))
	}
	if looksLikeRecentErrorQuestion(base) {
		return TurnIntentDiagnoseRecentError
	}
	if looksLikeGitOperationRequest(base) {
		return TurnIntentRunCommand
	}
	if containsAny(base, "계속", "이어", "continue", "resume", "go on", "next step", "다음 단계") {
		return TurnIntentContinueLastTask
	}
	if containsAny(base, "현재 상태", "status", "어디까지", "뭐 하는 중", "몇 %", "몇 퍼센트", "진행률", "작업 완료", "what happened", "current state", "progress") {
		return TurnIntentExplainCurrentState
	}
	if looksLikeCurrentTaskSteeringRequest(base) {
		return TurnIntentContinueLastTask
	}
	if looksLikePlanOrDirectionOnlyRequest(base) {
		return TurnIntentPlanOrDesign
	}
	if looksLikeExecutionFlowQuestion(base) {
		return TurnIntentAskProjectKnowledge
	}
	if looksLikeReviewInspectionOnlyRequest(base) {
		return TurnIntentReviewCode
	}
	if looksLikeExplicitEditIntent(base) {
		return TurnIntentEditCode
	}
	if hasNaturalReviewIntent(base) && !looksLikeReviewArtifactAuthoringRequest(base) {
		return TurnIntentReviewCode
	}
	if containsAny(base, "실행", "run ", "command", "명령", "테스트", "빌드", "build", "test") {
		return TurnIntentRunCommand
	}
	if containsAny(base, "설계", "로드맵", "roadmap", "plan", "architecture", "design") {
		return TurnIntentPlanOrDesign
	}
	if containsAny(base, "분석", "프로젝트", "구조", "architecture", "flow", "entrypoint") {
		return TurnIntentAskProjectKnowledge
	}
	return TurnIntentGeneral
}

func looksLikeCurrentTaskSteeringRequest(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if containsAny(lower, "잊지마", "기억해", "remember this", "keep this in mind") {
		return true
	}
	if containsAny(lower, "좁게만", "작은 기능", "작은 것", "small feature", "tiny feature", "narrowly") &&
		containsAny(lower, "근본", "큰 흐름", "전체", "overall", "big picture", "broader flow", "fundamental") {
		return true
	}
	if containsAny(lower, "큰 흐름", "전체적인 흐름", "전체 흐름", "overall flow", "big flow", "big picture", "broader flow") &&
		containsAny(lower, "위주", "먼저", "집중", "focus", "prioritize", "first") {
		return true
	}
	if containsAny(lower, "문서 산출", "document artifact", "documentation artifact") &&
		containsAny(lower, "만 검토하지 말고", "만 보지 말고", "not only", "not just") {
		return true
	}
	if containsAny(lower, "모든 영역", "전체 영역", "all areas", "whole area") &&
		containsAny(lower, "검토해야", "확인해야", "review", "inspect") &&
		containsAny(lower, "말고", "not just", "not only") {
		return true
	}
	return false
}

func looksLikePlanOrDirectionOnlyRequest(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if containsAny(lower,
		"바로 적용", "적용해", "적용하자", "구현해", "구현하자", "수정해", "수정하자", "고쳐", "패치해", "패치하자",
		"apply it", "implement it", "fix it", "patch it", "make the change",
	) {
		return false
	}
	if !containsAny(lower,
		"수정 방향", "개선 방향", "대응 방향", "보강 방향", "방향을 잡", "방향 잡", "방향을 정",
		"수정 계획", "개선 계획", "보강 계획", "수정 전략", "개선 전략",
		"fix direction", "improvement direction", "repair direction", "fix plan", "repair plan", "improvement plan", "fix strategy", "repair strategy",
	) {
		return false
	}
	return containsAny(lower,
		"분석", "비교", "검토", "잡자", "정하", "세우", "찾자", "확인", "방향", "계획", "전략",
		"analyze", "compare", "review", "plan", "strategy", "direction", "approach",
	)
}

func looksLikeGitOperationRequest(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"커밋", "commit", "푸시", "push",
		"git ", "git-", "git_", "깃 ",
		"스테이지", "stage ",
	)
}

func looksLikeRecentErrorQuestion(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	hasRecentRef := containsAny(lower,
		"방금", "아까", "직전", "최근", "이 에러", "이 오류", "그 에러", "그 오류",
		"last error", "recent error", "that error", "this error", "previous error",
	)
	hasErrorWord := containsAny(lower,
		"에러", "오류", "실패", "왜", "원인", "failed", "failure", "error", "why", "cause",
	)
	return hasRecentRef && hasErrorWord
}
