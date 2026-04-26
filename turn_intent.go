package main

import "strings"

type TurnIntent string

const (
	TurnIntentGeneral             TurnIntent = "general"
	TurnIntentDiagnoseRecentError TurnIntent = "diagnose_recent_error"
	TurnIntentContinueLastTask    TurnIntent = "continue_last_task"
	TurnIntentExplainCurrentState TurnIntent = "explain_current_state"
	TurnIntentAskProjectKnowledge TurnIntent = "ask_project_knowledge"
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
	if containsAny(base, "계속", "이어", "continue", "resume", "go on", "next step", "다음 단계") {
		return TurnIntentContinueLastTask
	}
	if containsAny(base, "현재 상태", "status", "어디까지", "뭐 하는 중", "what happened", "current state") {
		return TurnIntentExplainCurrentState
	}
	if looksLikeExplicitEditIntent(base) {
		return TurnIntentEditCode
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
