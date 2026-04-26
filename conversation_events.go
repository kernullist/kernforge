package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	conversationEventKindUserMessage    = "user_message"
	conversationEventKindAssistantReply = "assistant_reply"
	conversationEventKindToolCall       = "tool_call"
	conversationEventKindToolResult     = "tool_result"
	conversationEventKindToolError      = "tool_error"
	conversationEventKindProviderError  = "provider_error"
	conversationEventKindCommandError   = "command_error"
	conversationEventKindVerification   = "verification"
	conversationEventKindHandoff        = "handoff"

	conversationSeverityInfo  = "info"
	conversationSeverityWarn  = "warn"
	conversationSeverityError = "error"

	conversationEventLimit = 200
)

type ConversationEvent struct {
	ID            string            `json:"id"`
	TurnID        string            `json:"turn_id,omitempty"`
	Kind          string            `json:"kind"`
	Severity      string            `json:"severity,omitempty"`
	Summary       string            `json:"summary"`
	Raw           string            `json:"raw,omitempty"`
	Time          time.Time         `json:"time"`
	CorrelationID string            `json:"correlation_id,omitempty"`
	Entities      map[string]string `json:"entities,omitempty"`
	ArtifactRefs  []string          `json:"artifact_refs,omitempty"`
}

type NormalizedRuntimeError struct {
	Kind          string
	Category      string
	Provider      string
	Upstream      string
	Model         string
	Code          string
	StatusCode    int
	Message       string
	Request       string
	Shard         string
	Tool          string
	Retryable     bool
	BYOKHint      bool
	CorrelationID string
	Raw           string
}

func (s *Session) AppendConversationEvent(event ConversationEvent) {
	if s == nil {
		return
	}
	event.Kind = strings.TrimSpace(event.Kind)
	if event.Kind == "" {
		event.Kind = conversationEventKindUserMessage
	}
	event.Severity = normalizeConversationSeverity(event.Severity)
	event.Summary = strings.TrimSpace(event.Summary)
	event.Raw = strings.TrimSpace(event.Raw)
	event.CorrelationID = strings.TrimSpace(event.CorrelationID)
	event.ArtifactRefs = uniqueStrings(event.ArtifactRefs)
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	if strings.TrimSpace(event.ID) == "" {
		event.ID = fmt.Sprintf("evt-%s-%03d", event.Time.Format("20060102-150405"), event.Time.Nanosecond()/1_000_000)
	}
	if event.Entities != nil {
		cleaned := map[string]string{}
		for key, value := range event.Entities {
			k := strings.TrimSpace(key)
			v := strings.TrimSpace(value)
			if k != "" && v != "" {
				cleaned[k] = v
			}
		}
		if len(cleaned) > 0 {
			event.Entities = cleaned
		} else {
			event.Entities = nil
		}
	}
	s.ConversationEvents = append(s.ConversationEvents, event)
	if len(s.ConversationEvents) > conversationEventLimit {
		s.ConversationEvents = append([]ConversationEvent(nil), s.ConversationEvents[len(s.ConversationEvents)-conversationEventLimit:]...)
	}
	s.RefreshConversationState()
	s.UpdatedAt = time.Now()
}

func (s *Session) normalizeConversationRuntime() {
	if s == nil {
		return
	}
	filtered := make([]ConversationEvent, 0, len(s.ConversationEvents))
	for _, event := range s.ConversationEvents {
		event.Kind = strings.TrimSpace(event.Kind)
		if event.Kind == "" {
			continue
		}
		event.Severity = normalizeConversationSeverity(event.Severity)
		event.Summary = strings.TrimSpace(event.Summary)
		event.Raw = strings.TrimSpace(event.Raw)
		event.CorrelationID = strings.TrimSpace(event.CorrelationID)
		event.ArtifactRefs = uniqueStrings(event.ArtifactRefs)
		if event.Time.IsZero() {
			event.Time = s.UpdatedAt
		}
		if strings.TrimSpace(event.ID) == "" {
			event.ID = fmt.Sprintf("evt-%s-%03d", event.Time.Format("20060102-150405"), len(filtered)+1)
		}
		filtered = append(filtered, event)
	}
	if len(filtered) > conversationEventLimit {
		filtered = filtered[len(filtered)-conversationEventLimit:]
	}
	s.ConversationEvents = filtered
	s.RefreshConversationState()
}

func normalizeConversationSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case conversationSeverityError:
		return conversationSeverityError
	case conversationSeverityWarn, "warning":
		return conversationSeverityWarn
	default:
		return conversationSeverityInfo
	}
}

func (a *Agent) noteUserConversationEvent(text string) {
	if a == nil || a.Session == nil {
		return
	}
	summary := compactPromptSection(baseUserQueryText(text), 220)
	if strings.TrimSpace(summary) == "" {
		summary = compactPromptSection(text, 220)
	}
	a.Session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindUserMessage,
		Severity: conversationSeverityInfo,
		Summary:  summary,
		Raw:      compactPromptSection(text, 1200),
		Entities: map[string]string{
			"provider": strings.TrimSpace(a.Session.Provider),
			"model":    strings.TrimSpace(a.Session.Model),
		},
	})
}

func (a *Agent) noteAssistantConversationEvent(text string) {
	if a == nil || a.Session == nil {
		return
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	a.Session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindAssistantReply,
		Severity: conversationSeverityInfo,
		Summary:  compactPromptSection(trimmed, 220),
		Raw:      compactPromptSection(trimmed, 1200),
	})
	if strings.Contains(strings.ToLower(trimmed), "handoff:") {
		a.Session.AppendConversationEvent(ConversationEvent{
			Kind:     conversationEventKindHandoff,
			Severity: conversationSeverityInfo,
			Summary:  compactPromptSection(extractHandoffSummary(trimmed), 260),
			Raw:      compactPromptSection(trimmed, 1200),
		})
	}
}

func (a *Agent) noteToolConversationError(toolName string, err error, displayText string) {
	if a == nil || a.Session == nil || err == nil {
		return
	}
	raw := strings.TrimSpace(strings.Join([]string{displayText, err.Error()}, "\n"))
	normalized := normalizeRuntimeError(err)
	normalized.Kind = conversationEventKindToolError
	normalized.Tool = strings.TrimSpace(toolName)
	if strings.EqualFold(normalized.Tool, "run_shell") || strings.EqualFold(normalized.Tool, "shell") {
		normalized.Kind = conversationEventKindCommandError
	}
	if normalized.Raw == "" {
		normalized.Raw = raw
	}
	a.Session.AppendConversationEvent(runtimeErrorConversationEvent(normalized, a.Session))
}

func (a *Agent) noteToolConversationStart(call ToolCall) {
	if a == nil || a.Session == nil {
		return
	}
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return
	}
	entities := map[string]string{
		"tool": name,
	}
	for key, value := range toolArgumentEntities(call.Arguments) {
		entities[key] = value
	}
	a.Session.AppendConversationEvent(ConversationEvent{
		Kind:          conversationEventKindToolCall,
		Severity:      conversationSeverityInfo,
		Summary:       "tool call started: " + summarizeToolDiagnosticCall(call),
		Raw:           compactPromptSection(call.Arguments, 800),
		CorrelationID: strings.TrimSpace(call.ID),
		Entities:      entities,
	})
}

func (a *Agent) noteToolConversationResult(call ToolCall, result ToolExecutionResult) {
	if a == nil || a.Session == nil {
		return
	}
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return
	}
	entities := map[string]string{
		"tool": name,
	}
	for key, value := range toolArgumentEntities(call.Arguments) {
		entities[key] = value
	}
	for key, value := range toolMetaEntities(result.Meta) {
		entities[key] = value
	}
	summary := "tool result: " + name
	if strings.TrimSpace(result.DisplayText) != "" {
		summary += " | " + compactPromptSection(firstLine(result.DisplayText), 180)
	}
	a.Session.AppendConversationEvent(ConversationEvent{
		Kind:          conversationEventKindToolResult,
		Severity:      conversationSeverityInfo,
		Summary:       summary,
		Raw:           compactPromptSection(result.DisplayText, 1200),
		CorrelationID: strings.TrimSpace(call.ID),
		Entities:      entities,
	})
}

func (a *Agent) noteProviderConversationError(err error, req ChatRequest, final bool) {
	if a == nil || a.Session == nil || err == nil {
		return
	}
	normalized := normalizeRuntimeError(err)
	normalized.Kind = conversationEventKindProviderError
	if normalized.Provider == "" {
		normalized.Provider = strings.TrimSpace(a.Session.Provider)
	}
	if normalized.Model == "" {
		normalized.Model = strings.TrimSpace(req.Model)
	}
	if normalized.Request == "" {
		normalized.Request = summarizeChatRequestForEvent(req)
	}
	if final {
		normalized.CorrelationID = "model-request-final"
	} else {
		normalized.CorrelationID = "model-request-retry"
	}
	event := runtimeErrorConversationEvent(normalized, a.Session)
	if !final && event.Severity == conversationSeverityError {
		event.Severity = conversationSeverityWarn
	}
	a.Session.AppendConversationEvent(event)
}

func runtimeErrorConversationEvent(normalized NormalizedRuntimeError, sess *Session) ConversationEvent {
	entities := map[string]string{}
	addEntity := func(key string, value string) {
		if strings.TrimSpace(value) != "" {
			entities[key] = strings.TrimSpace(value)
		}
	}
	addEntity("category", normalized.Category)
	addEntity("provider", normalized.Provider)
	addEntity("upstream", normalized.Upstream)
	addEntity("model", normalized.Model)
	addEntity("code", normalized.Code)
	addEntity("shard", normalized.Shard)
	addEntity("tool", normalized.Tool)
	if normalized.StatusCode > 0 {
		addEntity("status_code", strconv.Itoa(normalized.StatusCode))
	}
	if normalized.Retryable {
		addEntity("retryable", "true")
	}
	if normalized.BYOKHint {
		addEntity("byok_hint", "true")
	}
	if sess != nil {
		addEntity("session_provider", sess.Provider)
		addEntity("session_model", sess.Model)
	}
	kind := strings.TrimSpace(normalized.Kind)
	if kind == "" {
		kind = conversationEventKindProviderError
	}
	summary := runtimeErrorSummary(normalized)
	raw := strings.TrimSpace(normalized.Raw)
	if raw == "" {
		raw = strings.TrimSpace(normalized.Message)
	}
	return ConversationEvent{
		Kind:          kind,
		Severity:      conversationSeverityError,
		Summary:       summary,
		Raw:           compactPromptSection(raw, 2000),
		CorrelationID: normalized.CorrelationID,
		Entities:      entities,
	}
}

func runtimeErrorSummary(normalized NormalizedRuntimeError) string {
	parts := []string{}
	if normalized.Kind == conversationEventKindCommandError {
		parts = append(parts, "command error")
	} else if normalized.Kind == conversationEventKindToolError {
		parts = append(parts, "tool error")
	} else {
		parts = append(parts, "provider error")
	}
	if normalized.Provider != "" {
		parts = append(parts, "provider="+normalized.Provider)
	}
	if normalized.Upstream != "" {
		parts = append(parts, "upstream="+normalized.Upstream)
	}
	if normalized.Model != "" {
		parts = append(parts, "model="+normalized.Model)
	}
	if normalized.Shard != "" {
		parts = append(parts, "shard="+normalized.Shard)
	}
	if normalized.Code != "" {
		parts = append(parts, "code="+normalized.Code)
	} else if normalized.StatusCode > 0 {
		parts = append(parts, "status="+strconv.Itoa(normalized.StatusCode))
	}
	if normalized.Category != "" {
		parts = append(parts, "category="+normalized.Category)
	}
	if normalized.Message != "" {
		parts = append(parts, compactPromptSection(normalized.Message, 180))
	}
	return strings.Join(parts, " | ")
}

func normalizeRuntimeError(err error) NormalizedRuntimeError {
	out := NormalizedRuntimeError{
		Kind:     conversationEventKindProviderError,
		Category: "unknown",
		Raw:      "",
	}
	if err == nil {
		return out
	}
	out.Message = strings.TrimSpace(err.Error())
	out.Raw = out.Message
	var providerErr *ProviderAPIError
	if errors.As(err, &providerErr) {
		out.Provider = strings.TrimSpace(providerErr.Provider)
		out.StatusCode = providerErr.StatusCode
		out.Code = strings.TrimSpace(providerErr.Code)
		out.Message = strings.TrimSpace(providerErr.Message)
		out.Request = strings.TrimSpace(providerErr.RequestSummary)
		out.Raw = strings.TrimSpace(providerErr.RawBody)
		if out.Raw == "" {
			out.Raw = err.Error()
		}
		out.Retryable = providerErr.Retryable()
	}
	out.mergeFromText(err.Error())
	out.mergeFromText(out.Raw)
	if out.StatusCode == http.StatusTooManyRequests || out.Code == "429" || strings.Contains(strings.ToLower(out.Raw+" "+out.Message), "rate-limit") || strings.Contains(strings.ToLower(out.Raw+" "+out.Message), "rate limited") || strings.Contains(strings.ToLower(out.Raw+" "+out.Message), "too many requests") {
		out.Category = "rate_limit"
		out.Retryable = true
	}
	if strings.Contains(strings.ToLower(out.Raw+" "+out.Message), "timeout") {
		out.Category = "timeout"
		out.Retryable = true
	}
	if out.Code == "" && out.StatusCode > 0 {
		out.Code = strconv.Itoa(out.StatusCode)
	}
	if strings.Contains(strings.ToLower(out.Raw+" "+out.Message), "byok") || strings.Contains(strings.ToLower(out.Raw+" "+out.Message), "add your own key") {
		out.BYOKHint = true
	}
	return out
}

func (n *NormalizedRuntimeError) mergeFromText(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	if n.Provider == "" {
		if match := regexp.MustCompile(`(?i)\b(openrouter|openai|anthropic|ollama|deepinfra|deepseek)\b`).FindStringSubmatch(trimmed); len(match) > 1 {
			n.Provider = strings.TrimSpace(match[1])
		}
	}
	if n.Model == "" {
		if match := regexp.MustCompile(`(?i)\bmodel=([^\s|,]+)`).FindStringSubmatch(trimmed); len(match) > 1 {
			n.Model = strings.Trim(match[1], `"'`)
		}
	}
	if n.Shard == "" {
		if match := regexp.MustCompile(`(?i)\bshard=([^\s|,]+)`).FindStringSubmatch(trimmed); len(match) > 1 {
			n.Shard = strings.Trim(match[1], `"'`)
		}
	}
	if n.Code == "" {
		if match := regexp.MustCompile(`(?i)\bcode[=:]"?([A-Za-z0-9_-]+)"?`).FindStringSubmatch(trimmed); len(match) > 1 {
			n.Code = strings.Trim(match[1], `"`)
		} else if strings.Contains(trimmed, "429") {
			n.Code = "429"
		}
	}
	if n.Upstream == "" {
		if match := regexp.MustCompile(`(?i)"provider_name"\s*:\s*"([^"]+)"`).FindStringSubmatch(trimmed); len(match) > 1 {
			n.Upstream = strings.TrimSpace(match[1])
		}
	}
	if n.Message == "" {
		n.Message = compactPromptSection(trimmed, 240)
	}
}

func summarizeChatRequestForEvent(req ChatRequest) string {
	parts := []string{}
	if strings.TrimSpace(req.Model) != "" {
		parts = append(parts, "model="+strings.TrimSpace(req.Model))
	}
	if len(req.Tools) > 0 {
		parts = append(parts, fmt.Sprintf("tools=%d", len(req.Tools)))
	}
	if strings.TrimSpace(req.WorkingDir) != "" {
		parts = append(parts, "working_dir="+strings.TrimSpace(req.WorkingDir))
	}
	if len(req.Messages) > 0 {
		latest := compactPromptSection(baseUserQueryText(latestUserMessageText(req.Messages)), 160)
		if latest != "" {
			parts = append(parts, "latest_user="+latest)
		}
	}
	return strings.Join(parts, " | ")
}

func recentErrorEvents(sess *Session, limit int) []ConversationEvent {
	if sess == nil {
		return nil
	}
	out := []ConversationEvent{}
	for i := len(sess.ConversationEvents) - 1; i >= 0; i-- {
		event := sess.ConversationEvents[i]
		if event.Severity == conversationSeverityError || event.Kind == conversationEventKindProviderError || event.Kind == conversationEventKindToolError || event.Kind == conversationEventKindCommandError {
			out = append(out, event)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	if sess.ConversationState != nil && strings.TrimSpace(sess.ConversationState.LastError) != "" {
		out = append(out, ConversationEvent{
			Kind:     conversationEventKindCommandError,
			Severity: conversationSeverityError,
			Summary:  strings.TrimSpace(sess.ConversationState.LastError),
			Raw:      strings.TrimSpace(sess.ConversationState.LastError),
			Entities: map[string]string{
				"category": "preserved_state",
			},
		})
		if limit > 0 && len(out) >= limit {
			return out
		}
	}
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		msg := sess.Messages[i]
		if !msg.IsError && !messageTextLooksLikeRuntimeError(msg.Text) {
			continue
		}
		normalized := normalizeRuntimeError(errors.New(msg.Text))
		if strings.TrimSpace(msg.ToolName) != "" {
			normalized.Tool = msg.ToolName
			normalized.Kind = conversationEventKindToolError
		}
		out = append(out, runtimeErrorConversationEvent(normalized, sess))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func toolArgumentEntities(raw string) map[string]string {
	entities := map[string]string{}
	args := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return entities
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return entities
	}
	add := func(key string, value string) {
		if strings.TrimSpace(value) != "" {
			entities[key] = strings.TrimSpace(value)
		}
	}
	add("path", stringValue(args, "path"))
	add("command", stringValue(args, "command"))
	add("pattern", stringValue(args, "pattern"))
	add("owner_node_id", stringValue(args, "owner_node_id"))
	return entities
}

func toolMetaEntities(meta map[string]any) map[string]string {
	entities := map[string]string{}
	if len(meta) == 0 {
		return entities
	}
	add := func(key string, value string) {
		if strings.TrimSpace(value) != "" {
			entities[key] = strings.TrimSpace(value)
		}
	}
	for _, key := range []string{"effect", "path", "command", "branch", "commit_sha", "pr_url"} {
		if value, ok := meta[key]; ok {
			add(key, strings.TrimSpace(fmt.Sprintf("%v", value)))
		}
	}
	if value, ok := meta["changed_count"]; ok {
		add("changed_count", strings.TrimSpace(fmt.Sprintf("%v", value)))
	}
	if value, ok := meta["success"]; ok {
		add("success", strings.TrimSpace(fmt.Sprintf("%v", value)))
	}
	return entities
}

func messageTextLooksLikeRuntimeError(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(lower, "error") ||
		strings.Contains(lower, "failed") ||
		strings.Contains(lower, "429") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "timeout")
}

func extractHandoffSummary(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		if !strings.Contains(strings.ToLower(line), "handoff:") {
			continue
		}
		out := strings.TrimSpace(line)
		if i+1 < len(lines) {
			next := strings.TrimSpace(lines[i+1])
			if next != "" {
				out += " " + next
			}
		}
		return out
	}
	return compactPromptSection(text, 260)
}

func renderRecentConversationEventsPrompt(events []ConversationEvent, maxEvents int) string {
	if maxEvents <= 0 {
		maxEvents = 5
	}
	candidates := append([]ConversationEvent(nil), events...)
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Time.After(candidates[j].Time)
	})
	if len(candidates) > maxEvents {
		candidates = candidates[:maxEvents]
	}
	lines := []string{}
	for _, event := range candidates {
		parts := []string{event.Kind}
		if event.Severity != "" {
			parts = append(parts, "severity="+event.Severity)
		}
		if !event.Time.IsZero() {
			parts = append(parts, "time="+event.Time.Format(time.RFC3339))
		}
		if len(event.Entities) > 0 {
			keys := make([]string, 0, len(event.Entities))
			for key := range event.Entities {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			entityParts := []string{}
			for _, key := range keys {
				entityParts = append(entityParts, key+"="+event.Entities[key])
			}
			parts = append(parts, strings.Join(entityParts, ", "))
		}
		line := "- " + strings.Join(parts, " | ")
		if strings.TrimSpace(event.Summary) != "" {
			line += "\n  summary: " + compactPromptSection(event.Summary, 260)
		}
		if strings.TrimSpace(event.Raw) != "" && event.Severity == conversationSeverityError {
			line += "\n  raw: " + compactPromptSection(event.Raw, 360)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func conversationEventToJSON(event ConversationEvent) string {
	data, err := json.Marshal(event)
	if err != nil {
		return ""
	}
	return string(data)
}
