package main

import "strings"

func (a *Agent) assembleConversationRuntimeContext(userText string) string {
	if a == nil || a.Session == nil {
		return ""
	}
	a.Session.RefreshConversationState()
	sections := []string{}
	if stateText := strings.TrimSpace(renderConversationStatePrompt(a.Session.ConversationState)); stateText != "" {
		sections = append(sections, "Active conversation state:\n"+stateText)
	}
	recentErrors := recentErrorEvents(a.Session, 3)
	if eventsText := strings.TrimSpace(renderRecentConversationEventsPrompt(recentErrors, 3)); eventsText != "" {
		sections = append(sections, "Recent runtime errors:\n"+eventsText)
	}
	recentEvents := recentNonUserConversationEvents(a.Session, 5)
	if eventsText := strings.TrimSpace(renderRecentConversationEventsPrompt(recentEvents, 5)); eventsText != "" {
		sections = append(sections, "Recent session events:\n"+eventsText)
	}
	intent := classifyTurnIntent(userText)
	if intent != TurnIntentGeneral {
		sections = append(sections, "Current turn intent: "+string(intent))
	}
	if len(sections) == 0 {
		return ""
	}
	return "[Conversation Runtime Context]\n" + strings.Join(sections, "\n\n") + "\n[/Conversation Runtime Context]"
}

func recentNonUserConversationEvents(sess *Session, limit int) []ConversationEvent {
	if sess == nil {
		return nil
	}
	out := []ConversationEvent{}
	for i := len(sess.ConversationEvents) - 1; i >= 0; i-- {
		event := sess.ConversationEvents[i]
		if event.Kind == conversationEventKindUserMessage || event.Kind == conversationEventKindAssistantReply {
			continue
		}
		out = append(out, event)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func shouldSuppressProjectAnalysisFastPathForIntent(intent TurnIntent) bool {
	switch intent {
	case TurnIntentDiagnoseRecentError, TurnIntentContinueLastTask, TurnIntentExplainCurrentState:
		return true
	default:
		return false
	}
}

func renderCompactionWorkingMemory(sess *Session) string {
	if sess == nil {
		return ""
	}
	sess.RefreshConversationState()
	lines := []string{}
	if stateText := strings.TrimSpace(renderConversationStatePrompt(sess.ConversationState)); stateText != "" {
		lines = append(lines, "Active conversation state:\n"+stateText)
	}
	if eventsText := strings.TrimSpace(renderRecentConversationEventsPrompt(recentErrorEvents(sess, 3), 3)); eventsText != "" {
		lines = append(lines, "Recent preserved errors:\n"+eventsText)
	}
	if eventsText := strings.TrimSpace(renderRecentConversationEventsPrompt(latestEventsByKind(sess.ConversationEvents, conversationEventKindHandoff), 2)); eventsText != "" {
		lines = append(lines, "Recent handoff:\n"+eventsText)
	}
	if suggestionText := strings.TrimSpace(renderCompactionSuggestionMemory(sess)); suggestionText != "" {
		lines = append(lines, "Pending suggestions:\n"+suggestionText)
	}
	if len(lines) == 0 {
		return ""
	}
	return "[Conversation Working Memory]\n" + strings.Join(lines, "\n\n") + "\n[/Conversation Working Memory]"
}

func renderCompactionSuggestionMemory(sess *Session) string {
	if sess == nil || sess.SuggestionMemory == nil {
		return ""
	}
	lines := []string{}
	for i := len(sess.SuggestionMemory.Records) - 1; i >= 0; i-- {
		record := sess.SuggestionMemory.Records[i]
		if record.Status != SuggestionStatusShown && record.Status != SuggestionStatusDismissed {
			continue
		}
		lines = append(lines, "- ["+record.Status+"] "+record.Suggestion.Title+" | "+record.Suggestion.DedupKey)
		if len(lines) >= 5 {
			break
		}
	}
	return strings.Join(lines, "\n")
}
