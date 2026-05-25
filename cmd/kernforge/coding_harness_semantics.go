package main

import (
	"strings"
	"unicode"
)

func codingHarnessSourcePrompt(sess *Session) string {
	if sess == nil {
		return ""
	}
	latestExternal := latestExternalUserMessageText(sess.Messages)
	if latestExternal != "" && !acceptanceContextPreservingControlRequest(latestExternal) {
		return latestExternal
	}
	if sess.AcceptanceContract != nil {
		if prompt := strings.TrimSpace(sess.AcceptanceContract.SourcePrompt); prompt != "" {
			return prompt
		}
	}
	if latestExternal != "" {
		return latestExternal
	}
	return ""
}

func latestExternalUserMessageText(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			continue
		}
		if messageIsInternalUserGuidance(msg) {
			continue
		}
		text := strings.TrimSpace(baseUserQueryText(messageExternalSourceText(msg)))
		if text == "" {
			continue
		}
		return text
	}
	return ""
}

func latestExternalUserMessageRawText(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			continue
		}
		if messageIsInternalUserGuidance(msg) {
			continue
		}
		text := strings.TrimSpace(messageExternalSourceText(msg))
		if text == "" {
			continue
		}
		return text
	}
	return ""
}

func latestUserMessageSatisfies(messages []Message, predicate func(string) bool) bool {
	if predicate == nil {
		return false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			continue
		}
		text := strings.TrimSpace(messageExternalSourceText(msg))
		if text == "" {
			continue
		}
		if predicate(text) {
			return true
		}
		if !msg.Internal && !messageIsInternalUserGuidance(msg) {
			return false
		}
	}
	return false
}

func latestInternalUserGuidanceText(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			continue
		}
		if !messageIsInternalUserGuidance(msg) {
			return ""
		}
		text := strings.TrimSpace(messageExternalSourceText(msg))
		if text != "" {
			return text
		}
	}
	return ""
}

func messageIsInternalUserGuidance(msg Message) bool {
	if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
		return false
	}
	if msg.Internal {
		return true
	}
	return looksLikeInternalReviewFeedbackUserMessage(msg.Text)
}

func latestExternalOrUserMessageText(messages []Message) string {
	if prompt := latestExternalUserMessageText(messages); prompt != "" {
		return prompt
	}
	return ""
}

func sessionEffectiveUserRequestText(sess *Session) string {
	if sess == nil {
		return ""
	}
	latestExternal := latestExternalOrUserMessageText(sess.Messages)
	if latestExternal != "" && !actionContextPreservingControlRequest(latestExternal) {
		return latestExternal
	}
	if latestExternal != "" {
		if prompt := preservableSessionAcceptancePrompt(sess); prompt != "" {
			return prompt
		}
		return latestExternal
	}
	return preservableSessionAcceptancePrompt(sess)
}

func messageExternalSourceText(msg Message) string {
	if text := strings.TrimSpace(msg.SourceText); text != "" {
		return text
	}
	return msg.Text
}

func patchTransactionGoalFromSession(sess *Session) string {
	if sess == nil {
		return ""
	}
	latestExternal := latestExternalUserMessageText(sess.Messages)
	preservedExternalContext := ""
	if latestExternal != "" {
		if !acceptanceContextPreservingControlRequest(latestExternal) {
			return latestExternal
		}
		preservedExternalContext = preservableSessionAcceptancePrompt(sess)
		if preservedExternalContext == "" {
			return latestExternal
		}
	}
	if sess.AcceptanceContract != nil {
		if prompt := strings.TrimSpace(sess.AcceptanceContract.SourcePrompt); prompt != "" {
			return prompt
		}
	}
	if sess.TaskState != nil {
		if goal := strings.TrimSpace(sess.TaskState.Goal); goal != "" && !looksLikeInternalReviewFeedbackUserMessage(goal) {
			return strings.TrimSpace(baseUserQueryText(goal))
		}
	}
	if preservedExternalContext != "" {
		return preservedExternalContext
	}
	if latestExternal != "" {
		return latestExternal
	}
	return ""
}

func acceptanceContextPreservingControlRequest(text string) bool {
	text = strings.TrimSpace(baseUserQueryText(text))
	if text == "" {
		return false
	}
	if looksLikeFinalAnswerFollowupPrompt(text) {
		return true
	}
	switch classifyTurnIntent(text) {
	case TurnIntentContinueLastTask, TurnIntentExplainCurrentState:
		return true
	default:
		return false
	}
}

func actionContextPreservingControlRequest(text string) bool {
	text = strings.TrimSpace(baseUserQueryText(text))
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	if requestExplicitlyAsksForWebResearch(lower) ||
		requestLooksLikeLocalVerificationWork(lower) ||
		looksLikeGitOperationRequest(lower) {
		return false
	}
	if looksLikeFinalAnswerFollowupPrompt(text) {
		return true
	}
	return classifyTurnIntent(text) == TurnIntentContinueLastTask
}

func preservableSessionAcceptancePrompt(sess *Session) string {
	if sess == nil {
		return ""
	}
	if sess.AcceptanceContract != nil {
		if prompt := strings.TrimSpace(baseUserQueryText(sess.AcceptanceContract.SourcePrompt)); prompt != "" && !looksLikeInternalReviewFeedbackUserMessage(prompt) {
			return prompt
		}
	}
	if sess.TaskState != nil {
		if goal := strings.TrimSpace(baseUserQueryText(sess.TaskState.Goal)); goal != "" && !looksLikeInternalReviewFeedbackUserMessage(goal) {
			return goal
		}
	}
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		msg := sess.Messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			continue
		}
		if messageIsInternalUserGuidance(msg) {
			continue
		}
		text := strings.TrimSpace(baseUserQueryText(messageExternalSourceText(msg)))
		if text == "" || looksLikeInternalReviewFeedbackUserMessage(text) {
			continue
		}
		if acceptanceContextPreservingControlRequest(text) {
			continue
		}
		return text
	}
	return ""
}

func splitInjectedPromptContext(enriched string) (string, string) {
	trimmed := strings.TrimSpace(enriched)
	if trimmed == "" {
		return "", ""
	}
	external := strings.TrimSpace(baseUserQueryText(trimmed))
	if external == "" {
		return trimmed, ""
	}
	if !strings.HasPrefix(trimmed, external) {
		return external, ""
	}
	internal := strings.TrimSpace(strings.TrimPrefix(trimmed, external))
	return external, internal
}

func codingHarnessMeaningfulTokens(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(text)), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-')
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		token := strings.Trim(field, "_-")
		token = strings.TrimSuffix(token, "가")
		token = strings.TrimSuffix(token, "이")
		token = strings.TrimSuffix(token, "을")
		token = strings.TrimSuffix(token, "를")
		token = strings.TrimSuffix(token, "은")
		token = strings.TrimSuffix(token, "는")
		token = strings.TrimSuffix(token, "에서")
		token = strings.TrimSuffix(token, "으로")
		token = strings.TrimSuffix(token, "로")
		if len([]rune(token)) < 2 || codingHarnessTokenIsGeneric(token) {
			continue
		}
		out = append(out, token)
	}
	return normalizeTaskStateList(out, 32)
}

func codingHarnessTokenIsGeneric(token string) bool {
	switch strings.TrimSpace(strings.ToLower(token)) {
	case "the", "and", "or", "for", "with", "from", "into", "that", "this", "when", "after", "before", "while", "using", "via",
		"create", "generate", "write", "save", "add", "update", "updated", "make", "made", "file", "doc", "docs", "document", "report", "artifact",
		"please", "need", "needs", "request", "requested", "change", "fix", "bug", "problem", "issue", "test", "verify",
		"내", "내가", "나의", "가끔", "자주", "항상", "계속", "문제", "버그", "오류", "에러", "실패", "증상", "발생", "수정", "구현",
		"파일", "문서", "보고서", "리포트", "생성", "작성", "저장", "추가", "검증", "테스트", "해야", "하면", "후", "중", "동안":
		return true
	default:
		return false
	}
}

func codingHarnessMatchedTokens(text string, tokens []string) []string {
	lower := strings.ToLower(text)
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(strings.ToLower(token))
		if token == "" {
			continue
		}
		if strings.Contains(lower, token) {
			out = append(out, token)
		}
	}
	return normalizeTaskStateList(out, 32)
}

func codingHarnessPromptHasBugScenario(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if rootCausePromptHasTrigger(lower) && rootCausePromptHasObservedFailure(lower) {
		return true
	}
	return containsAny(lower,
		"root cause", "root-cause", "bug", "regression", "repro", "scenario", "symptom", "failure", "fails", "broken",
		"원인", "근본 원인", "버그", "회귀", "재현", "시나리오", "증상", "실패", "깨짐", "고쳐",
	)
}

func replyClaimsRootCause(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"root cause", "root-cause", "caused by", "cause is", "because", "leads to", "results in",
		"근본 원인", "원인은", "원인", "때문", "이어져", "발생",
	)
}

func replyClaimsResolution(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"fixed", "resolved", "implemented", "patched", "done", "completed", "root cause",
		"수정", "해결", "구현", "패치", "완료", "끝냈", "원인",
	)
}
