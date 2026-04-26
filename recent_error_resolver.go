package main

import (
	"fmt"
	"sort"
	"strings"
)

func (a *Agent) maybeAnswerRecentErrorQuestion(userText string) (string, bool) {
	if a == nil || a.Session == nil {
		return "", false
	}
	if classifyTurnIntent(userText) != TurnIntentDiagnoseRecentError {
		return "", false
	}
	events := recentErrorEvents(a.Session, 3)
	if len(events) == 0 {
		return "", false
	}
	reply := renderRecentErrorAnswer(events[0], events[1:])
	if strings.TrimSpace(reply) == "" {
		return "", false
	}
	a.Session.AddMessage(Message{
		Role: "assistant",
		Text: reply,
	})
	a.noteAssistantConversationEvent(reply)
	a.Session.RefreshConversationState()
	return reply, true
}

func renderRecentErrorAnswer(event ConversationEvent, alternates []ConversationEvent) string {
	entities := event.Entities
	category := strings.TrimSpace(entities["category"])
	provider := firstNonEmptyRuntimeString(entities["provider"], entities["session_provider"])
	if strings.EqualFold(provider, "openai") && strings.TrimSpace(entities["session_provider"]) != "" {
		provider = strings.TrimSpace(entities["session_provider"])
	}
	upstream := strings.TrimSpace(entities["upstream"])
	model := firstNonEmptyRuntimeString(entities["model"], entities["session_model"])
	code := firstNonEmptyRuntimeString(entities["code"], entities["status_code"])
	shard := strings.TrimSpace(entities["shard"])
	tool := strings.TrimSpace(entities["tool"])
	raw := strings.TrimSpace(event.Raw)
	if raw == "" {
		raw = strings.TrimSpace(event.Summary)
	}

	var b strings.Builder
	b.WriteString("직전 로그 기준으로 보면, ")
	switch event.Kind {
	case conversationEventKindProviderError:
		b.WriteString("모델/provider 요청이 실패한 케이스입니다.")
	case conversationEventKindCommandError:
		b.WriteString("실행한 명령 또는 shell tool이 실패한 케이스입니다.")
	case conversationEventKindToolError:
		b.WriteString("tool 호출이 실패한 케이스입니다.")
	default:
		b.WriteString("최근 런타임 오류입니다.")
	}
	b.WriteString("\n\n")

	details := []string{}
	if shard != "" {
		details = append(details, fmt.Sprintf("shard: `%s`", shard))
	}
	if tool != "" {
		details = append(details, fmt.Sprintf("tool: `%s`", tool))
	}
	if provider != "" {
		details = append(details, fmt.Sprintf("provider: `%s`", provider))
	}
	if upstream != "" {
		details = append(details, fmt.Sprintf("upstream: `%s`", upstream))
	}
	if model != "" {
		details = append(details, fmt.Sprintf("model: `%s`", model))
	}
	if code != "" {
		details = append(details, fmt.Sprintf("code/status: `%s`", code))
	}
	if len(details) > 0 {
		b.WriteString("확인된 항목:\n")
		for _, detail := range details {
			b.WriteString("- ")
			b.WriteString(detail)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if category == "rate_limit" || code == "429" {
		b.WriteString("원인은 코드 분석 내용 문제가 아니라 provider 쪽 rate limit입니다. 특히 OpenRouter 뒤 upstream provider가 일시적으로 요청을 제한하면 `429 Too Many Requests`가 납니다.\n\n")
		if upstream != "" {
			b.WriteString("이번 경우 upstream은 `")
			b.WriteString(upstream)
			b.WriteString("`로 보입니다.\n\n")
		}
		b.WriteString("처리 방향은 재시도, analysis worker/reviewer 모델 변경, 또는 OpenRouter BYOK/provider key 설정으로 rate limit pool을 분리하는 것입니다.")
	} else if category == "timeout" {
		b.WriteString("원인은 요청 시간 초과입니다. 모델 응답이나 tool 실행이 지정된 timeout 안에 끝나지 않았습니다. 같은 작업을 더 작은 shard로 나누거나 timeout/retry 설정을 늘리는 쪽이 맞습니다.")
	} else {
		b.WriteString("핵심 원문은 다음과 같습니다:\n\n")
		b.WriteString("```text\n")
		b.WriteString(compactPromptSection(raw, 900))
		b.WriteString("\n```\n\n")
		b.WriteString("이 오류는 최근 세션 event log에서 가져온 것이므로, 새 정보를 다시 붙여넣지 않아도 이 맥락을 기준으로 이어서 볼 수 있습니다.")
	}
	if strings.TrimSpace(entities["retryable"]) == "true" {
		b.WriteString("\n\n이 오류는 재시도 가능성이 높은 transient 오류로 분류됩니다.")
	}
	if strings.TrimSpace(entities["byok_hint"]) == "true" {
		b.WriteString(" 로그에도 BYOK 또는 자체 key 설정 힌트가 포함되어 있습니다.")
	}
	if len(alternates) > 0 {
		b.WriteString("\n\n다른 최근 오류 후보도 있었습니다. 시간상 가장 가까운 오류를 우선 설명했지만, 후보는 다음과 같습니다:\n")
		for _, alt := range limitConversationEvents(alternates, 4) {
			b.WriteString("- ")
			b.WriteString(renderRecentErrorAlternateSummary(alt))
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func renderRecentErrorAlternateSummary(event ConversationEvent) string {
	entities := event.Entities
	parts := []string{}
	if strings.TrimSpace(event.Kind) != "" {
		parts = append(parts, "kind="+event.Kind)
	}
	if value := firstNonEmptyRuntimeString(entities["category"], entities["code"], entities["status_code"]); value != "" {
		parts = append(parts, "signal="+value)
	}
	if value := firstNonEmptyRuntimeString(entities["tool"], entities["provider"], entities["session_provider"]); value != "" {
		parts = append(parts, "source="+value)
	}
	if value := firstNonEmptyRuntimeString(entities["model"], entities["session_model"]); value != "" {
		parts = append(parts, "model="+value)
	}
	if value := strings.TrimSpace(entities["shard"]); value != "" {
		parts = append(parts, "shard="+value)
	}
	summary := compactPromptSection(firstNonEmptyRuntimeString(event.Summary, event.Raw), 160)
	if len(parts) == 0 {
		return summary
	}
	if summary == "" {
		return strings.Join(parts, " | ")
	}
	return strings.Join(parts, " | ") + " | " + summary
}

func limitConversationEvents(events []ConversationEvent, limit int) []ConversationEvent {
	if limit <= 0 || len(events) <= limit {
		return events
	}
	return append([]ConversationEvent(nil), events[:limit]...)
}

func firstNonEmptyRuntimeString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func latestEventsByKind(events []ConversationEvent, kinds ...string) []ConversationEvent {
	kindSet := map[string]bool{}
	for _, kind := range kinds {
		kindSet[strings.TrimSpace(kind)] = true
	}
	out := []ConversationEvent{}
	for _, event := range events {
		if kindSet[event.Kind] {
			out = append(out, event)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Time.After(out[j].Time)
	})
	return out
}
