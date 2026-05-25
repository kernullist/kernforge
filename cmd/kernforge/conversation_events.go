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
	conversationEventKindUserMessage      = "user_message"
	conversationEventKindAssistantReply   = "assistant_reply"
	conversationEventKindToolCall         = "tool_call"
	conversationEventKindToolResult       = "tool_result"
	conversationEventKindToolError        = "tool_error"
	conversationEventKindProviderError    = "provider_error"
	conversationEventKindCommandError     = "command_error"
	conversationEventKindTurnStarted      = "turn_started"
	conversationEventKindExecCommandBegin = "exec_command_begin"
	conversationEventKindExecCommandEnd   = "exec_command_end"
	conversationEventKindPatchApplyBegin  = "patch_apply_begin"
	conversationEventKindPatchApplyEnd    = "patch_apply_end"
	conversationEventKindMCPToolCallBegin = "mcp_tool_call_begin"
	conversationEventKindMCPToolCallEnd   = "mcp_tool_call_end"
	conversationEventKindTurnDiff         = "turn_diff"
	conversationEventKindVerification     = "verification"
	conversationEventKindHandoff          = "handoff"
	conversationEventKindDashboard        = "dashboard"
	conversationEventKindAutomation       = "automation"
	conversationEventKindContinuity       = "continuity"
	conversationEventKindCompletionAudit  = "completion_audit"
	conversationEventKindRecovery         = "recovery"
	conversationEventKindEventStream      = "event_stream"
	conversationEventKindGoal             = "goal"
	conversationEventKindReview           = "review"
	conversationEventKindExternalLookup   = "external_lookup"
	conversationEventKindMCPServer        = "mcp_server"

	conversationSeverityInfo  = "info"
	conversationSeverityWarn  = "warn"
	conversationSeverityError = "error"

	conversationEventLimit = 200
)

type ConversationEvent struct {
	ID            string            `json:"id"`
	TurnID        string            `json:"turn_id,omitempty"`
	TraceID       string            `json:"trace_id,omitempty"`
	Kind          string            `json:"kind"`
	Severity      string            `json:"severity,omitempty"`
	Summary       string            `json:"summary"`
	Raw           string            `json:"raw,omitempty"`
	Time          time.Time         `json:"time"`
	CorrelationID string            `json:"correlation_id,omitempty"`
	Entities      map[string]string `json:"entities,omitempty"`
	Metadata      map[string]any    `json:"metadata,omitempty"`
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
	event.TurnID = strings.TrimSpace(event.TurnID)
	event.TraceID = strings.TrimSpace(event.TraceID)
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
		event.TurnID = strings.TrimSpace(event.TurnID)
		event.TraceID = strings.TrimSpace(event.TraceID)
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

func (a *Agent) noteUserConversationEvent(text string, images []MessageImage) {
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
		Metadata: userConversationImageMetadata(images),
	})
}

func userConversationImageMetadata(images []MessageImage) map[string]any {
	if len(images) == 0 {
		return nil
	}
	localImages := make([]string, 0, len(images))
	localImageDetails := make([]any, 0, len(images))
	for _, image := range images {
		path := strings.TrimSpace(image.Path)
		if path == "" {
			continue
		}
		localImages = append(localImages, path)
		switch detail := normalizedKnownImageDetail(image.Detail); detail {
		case imageDetailHigh, imageDetailOriginal:
			localImageDetails = append(localImageDetails, detail)
		default:
			localImageDetails = append(localImageDetails, nil)
		}
	}
	if len(localImages) == 0 {
		return nil
	}
	for len(localImageDetails) > 0 && localImageDetails[len(localImageDetails)-1] == nil {
		localImageDetails = localImageDetails[:len(localImageDetails)-1]
	}
	metadata := map[string]any{
		"local_images": localImages,
	}
	if len(localImageDetails) > 0 {
		metadata["local_image_details"] = localImageDetails
	}
	return metadata
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

func (a *Agent) noteTurnStartedConversationEvent(turnStartedAt time.Time, metadata map[string]any) {
	if a == nil || a.Session == nil {
		return
	}
	turnID := strings.TrimSpace(stringValue(metadata, "turn_id"))
	traceID := strings.TrimSpace(stringValue(metadata, "trace_id"))
	if turnID == "" && traceID == "" {
		return
	}
	entities := map[string]string{}
	addEntity := func(key string, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			entities[key] = value
		}
	}
	addEntity("provider", a.Session.Provider)
	addEntity("model", a.Session.Model)
	addEntity("permission_mode", stringValue(metadata, "permission_mode"))
	addEntity("sandbox", stringValue(metadata, "sandbox"))
	addEntity("cwd", stringValue(metadata, "cwd"))
	addEntity("workspace_root", stringValue(metadata, "workspace_root"))
	if len(entities) == 0 {
		entities = nil
	}
	eventTime := turnStartedAt
	if eventTime.IsZero() {
		eventTime = time.Now()
	}
	a.Session.AppendConversationEvent(ConversationEvent{
		TurnID:   turnID,
		TraceID:  traceID,
		Kind:     conversationEventKindTurnStarted,
		Severity: conversationSeverityInfo,
		Summary:  "turn started",
		Time:     eventTime,
		Entities: entities,
		Metadata: cloneStringAnyMap(metadata),
	})
}

func (a *Agent) noteToolConversationError(call ToolCall, err error, displayText string) {
	if a == nil || a.Session == nil || err == nil {
		return
	}
	toolName := strings.TrimSpace(call.Name)
	raw := strings.TrimSpace(strings.Join([]string{displayText, err.Error()}, "\n"))
	normalized := normalizeRuntimeError(err)
	normalized.Kind = conversationEventKindToolError
	normalized.Tool = strings.TrimSpace(toolName)
	normalized.CorrelationID = strings.TrimSpace(call.ID)
	if strings.EqualFold(normalized.Tool, "run_shell") || strings.EqualFold(normalized.Tool, "shell") {
		normalized.Kind = conversationEventKindCommandError
	}
	if normalized.Raw == "" {
		normalized.Raw = raw
	}
	event := runtimeErrorConversationEvent(normalized, a.Session)
	a.Session.AppendConversationEvent(event)
	a.appendRuntimeErrorConversationEvent(event, nil)
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
	a.appendCodexStyleToolLifecycleBegin(call)
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
		Metadata:      toolResultEventMetadata(result.Meta),
	})
	a.appendCodexStyleToolLifecycleEnd(call, result, nil, false)
}

func (a *Agent) noteToolConversationFailureResult(call ToolCall, result ToolExecutionResult, err error, blocked bool) {
	if a == nil || a.Session == nil {
		return
	}
	if err == nil && !blocked {
		return
	}
	a.appendCodexStyleToolLifecycleEnd(call, result, err, blocked)
}

func (a *Agent) noteToolConversationBlockedResult(call ToolCall, result ToolExecutionResult, err error) {
	a.noteToolConversationStart(call)
	a.noteToolConversationFailureResult(call, result, err, true)
}

func (a *Agent) appendCodexStyleToolLifecycleBegin(call ToolCall) {
	if a == nil || a.Session == nil {
		return
	}
	name := strings.TrimSpace(call.Name)
	entities := toolLifecycleEntities(call, nil)
	switch {
	case toolCallIsExecCommandLike(name):
		a.Session.AppendConversationEvent(ConversationEvent{
			Kind:          conversationEventKindExecCommandBegin,
			Severity:      conversationSeverityInfo,
			Summary:       "exec_command begin: " + compactPromptSection(firstNonEmptyRuntimeString(entities["command"], entities["commands"], name), 220),
			Raw:           compactPromptSection(call.Arguments, 1200),
			CorrelationID: strings.TrimSpace(call.ID),
			Entities:      entities,
		})
	case toolCallIsPatchApplyLike(name):
		entities["auto_approved"] = "true"
		if strings.TrimSpace(entities["changed_paths"]) == "" {
			if path := firstNonEmptyRuntimeString(entities["path"], entities["file"]); path != "" {
				entities["changed_paths"] = path
			}
		}
		a.Session.AppendConversationEvent(ConversationEvent{
			Kind:          conversationEventKindPatchApplyBegin,
			Severity:      conversationSeverityInfo,
			Summary:       "patch_apply begin: " + compactPromptSection(firstNonEmptyRuntimeString(entities["changed_paths"], name), 220),
			Raw:           compactPromptSection(call.Arguments, 1200),
			CorrelationID: strings.TrimSpace(call.ID),
			Entities:      entities,
		})
	case toolCallIsMCPToolLike(name):
		entities = mcpToolLifecycleEntities(call, nil)
		a.Session.AppendConversationEvent(ConversationEvent{
			Kind:          conversationEventKindMCPToolCallBegin,
			Severity:      conversationSeverityInfo,
			Summary:       "mcp_tool_call begin: " + compactPromptSection(mcpToolLifecycleLabel(entities, name), 220),
			Raw:           compactPromptSection(call.Arguments, 1200),
			CorrelationID: strings.TrimSpace(call.ID),
			Entities:      entities,
		})
	default:
	}
}

func (a *Agent) appendCodexStyleToolLifecycleEnd(call ToolCall, result ToolExecutionResult, err error, blocked bool) {
	if a == nil || a.Session == nil {
		return
	}
	name := strings.TrimSpace(call.Name)
	entities := toolLifecycleEntities(call, result.Meta)
	status := toolLifecycleStatus(result.Meta, err, blocked)
	entities["status"] = status
	if err != nil {
		entities["error"] = compactPromptSection(err.Error(), 220)
	}
	if blocked {
		entities["blocked"] = "true"
	}
	emittedTurnDiff := false
	switch {
	case toolCallIsExecCommandLike(name):
		a.Session.AppendConversationEvent(ConversationEvent{
			Kind:          conversationEventKindExecCommandEnd,
			Severity:      lifecycleEventSeverity(status),
			Summary:       "exec_command end: " + status + " | " + compactPromptSection(firstNonEmptyRuntimeString(entities["command"], entities["commands"], name), 180),
			Raw:           compactPromptSection(result.DisplayText, 1600),
			CorrelationID: strings.TrimSpace(call.ID),
			Entities:      entities,
		})
	case toolCallIsPatchApplyLike(name):
		a.Session.AppendConversationEvent(ConversationEvent{
			Kind:          conversationEventKindPatchApplyEnd,
			Severity:      lifecycleEventSeverity(status),
			Summary:       "patch_apply end: " + status + " | " + compactPromptSection(firstNonEmptyRuntimeString(entities["changed_paths"], name), 180),
			Raw:           compactPromptSection(result.DisplayText, 1600),
			CorrelationID: strings.TrimSpace(call.ID),
			Entities:      entities,
		})
		if !a.appendCodexStyleTurnDiffEvent(call, result, entities) {
			a.appendCodexStyleTurnDiffInvalidatedEvent(call, result, entities)
		}
		emittedTurnDiff = true
	case toolCallIsMCPToolLike(name):
		entities = mcpToolLifecycleEntities(call, result.Meta)
		entities["status"] = status
		if err != nil {
			entities["error"] = compactPromptSection(err.Error(), 220)
		}
		if blocked {
			entities["blocked"] = "true"
		}
		metadata := toolResultEventMetadata(result.Meta)
		if metadata == nil && err != nil {
			metadata = map[string]any{"error": err.Error()}
		}
		a.Session.AppendConversationEvent(ConversationEvent{
			Kind:          conversationEventKindMCPToolCallEnd,
			Severity:      lifecycleEventSeverity(status),
			Summary:       "mcp_tool_call end: " + status + " | " + compactPromptSection(mcpToolLifecycleLabel(entities, name), 180),
			Raw:           compactPromptSection(result.DisplayText, 1600),
			CorrelationID: strings.TrimSpace(call.ID),
			Entities:      entities,
			Metadata:      metadata,
		})
	default:
	}
	if !emittedTurnDiff && strings.TrimSpace(toolMetaString(result.Meta, "unified_diff")) != "" {
		a.appendCodexStyleTurnDiffEvent(call, result, entities)
	} else if !emittedTurnDiff && toolMetaBool(result.Meta, "turn_diff_invalidated") {
		a.appendCodexStyleTurnDiffInvalidatedEvent(call, result, entities)
	}
}

func (a *Agent) appendCodexStyleTurnDiffEvent(call ToolCall, result ToolExecutionResult, lifecycleEntities map[string]string) bool {
	if a == nil || a.Session == nil {
		return false
	}
	unifiedDiff := strings.TrimSpace(toolMetaString(result.Meta, "unified_diff"))
	if unifiedDiff == "" {
		return false
	}
	entities := map[string]string{
		"tool": strings.TrimSpace(call.Name),
	}
	for key, value := range lifecycleEntities {
		if strings.TrimSpace(value) != "" {
			entities[key] = strings.TrimSpace(value)
		}
	}
	entities["line_count"] = strconv.Itoa(textLineCount(unifiedDiff))
	entities["file_count"] = strconv.Itoa(unifiedDiffFileCount(unifiedDiff))
	a.Session.AppendConversationEvent(ConversationEvent{
		Kind:          conversationEventKindTurnDiff,
		Severity:      conversationSeverityInfo,
		Summary:       "turn_diff: " + compactPromptSection(firstNonEmptyRuntimeString(entities["changed_paths"], strings.TrimSpace(call.Name)), 220),
		Raw:           compactPromptSection(unifiedDiff, 12000),
		CorrelationID: strings.TrimSpace(call.ID),
		Entities:      entities,
	})
	return true
}

func (a *Agent) appendCodexStyleTurnDiffInvalidatedEvent(call ToolCall, result ToolExecutionResult, lifecycleEntities map[string]string) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if !toolMetaBool(result.Meta, "turn_diff_invalidated") {
		return false
	}
	entities := map[string]string{
		"tool":        strings.TrimSpace(call.Name),
		"invalidated": "true",
	}
	for key, value := range lifecycleEntities {
		if strings.TrimSpace(value) != "" {
			entities[key] = strings.TrimSpace(value)
		}
	}
	if reason := strings.TrimSpace(toolMetaString(result.Meta, "unified_diff_unavailable_reason")); reason != "" {
		entities["reason"] = compactPromptSection(reason, 220)
	}
	a.Session.AppendConversationEvent(ConversationEvent{
		Kind:          conversationEventKindTurnDiff,
		Severity:      conversationSeverityInfo,
		Summary:       "turn_diff: invalidated | " + compactPromptSection(firstNonEmptyRuntimeString(entities["changed_paths"], strings.TrimSpace(call.Name)), 220),
		CorrelationID: strings.TrimSpace(call.ID),
		Entities:      entities,
	})
	return true
}

func toolCallIsExecCommandLike(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "run_shell", "run_shell_background", "run_shell_bundle_background":
		return true
	default:
		return false
	}
}

func toolCallIsPatchApplyLike(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "apply_patch", "apply_edit_proposal", "write_file", "replace_in_file":
		return true
	default:
		return false
	}
}

func toolCallIsMCPToolLike(name string) bool {
	_, _, ok := parseMCPInvocationToolName(name)
	return ok
}

func parseMCPInvocationToolName(name string) (string, string, bool) {
	name = strings.TrimSpace(name)
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "mcp__resource__"):
		server := strings.TrimSpace(name[len("mcp__resource__"):])
		return server, "resource", server != ""
	case strings.HasPrefix(lower, "mcp__prompt__"):
		server := strings.TrimSpace(name[len("mcp__prompt__"):])
		return server, "prompt", server != ""
	case strings.HasPrefix(lower, "mcp__"):
		parts := strings.SplitN(name, "__", 3)
		if len(parts) == 3 && strings.TrimSpace(parts[1]) != "" && strings.TrimSpace(parts[2]) != "" {
			return strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2]), true
		}
	}
	return "", "", false
}

func mcpToolLifecycleEntities(call ToolCall, meta map[string]any) map[string]string {
	entities := toolLifecycleEntities(call, meta)
	name := strings.TrimSpace(call.Name)
	server, tool, ok := parseMCPInvocationToolName(name)
	if ok {
		entities["mcp_server"] = server
		entities["mcp_tool"] = tool
		entities["mcp_namespaced_tool"] = name
	}
	return entities
}

func mcpToolLifecycleLabel(entities map[string]string, fallback string) string {
	server := strings.TrimSpace(entities["mcp_server"])
	tool := strings.TrimSpace(entities["mcp_tool"])
	if server != "" && tool != "" {
		return server + "/" + tool
	}
	return strings.TrimSpace(fallback)
}

func toolLifecycleEntities(call ToolCall, meta map[string]any) map[string]string {
	entities := map[string]string{
		"tool": strings.TrimSpace(call.Name),
	}
	for key, value := range toolArgumentEntities(call.Arguments) {
		entities[key] = value
	}
	for key, value := range toolMetaEntities(meta) {
		entities[key] = value
	}
	return entities
}

func toolLifecycleStatus(meta map[string]any, err error, blocked bool) string {
	if status := strings.ToLower(strings.TrimSpace(toolMetaString(meta, "command_execution_status"))); status != "" {
		switch status {
		case "completed", "failed", "declined":
			return status
		default:
			if strings.HasPrefix(status, "blocked") {
				return "declined"
			}
		}
	}
	if status := strings.ToLower(strings.TrimSpace(toolMetaString(meta, "patch_apply_status"))); status != "" {
		switch status {
		case "completed", "failed", "declined":
			return status
		default:
			if strings.HasPrefix(status, "blocked") {
				return "declined"
			}
		}
	}
	if boolValue(meta, "deferred", false) || boolValue(meta, "requires_reissue", false) {
		return "declined"
	}
	if boolValue(meta, "verification_declined", false) || boolValue(meta, "verification_skipped", false) {
		return "declined"
	}
	if blocked || err != nil {
		if errors.Is(err, ErrWriteDenied) || errors.Is(err, ErrEditCanceled) {
			return "declined"
		}
		return "failed"
	}
	return "completed"
}

func lifecycleEventSeverity(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed":
		return conversationSeverityError
	case "declined":
		return conversationSeverityWarn
	default:
		return conversationSeverityInfo
	}
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
	a.appendRuntimeErrorConversationEvent(event, map[string]string{
		"final": strconv.FormatBool(final),
	})
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
		if message := providerRateLimitReachedTypeMessage(providerErr.RateLimitReachedType); message != "" {
			out.Message = message
		}
		out.Request = strings.TrimSpace(providerErr.RequestSummary)
		out.Raw = strings.TrimSpace(providerErr.RawBody)
		if out.Raw == "" {
			out.Raw = err.Error()
		}
		out.Retryable = providerErr.Retryable()
	}
	out.mergeFromText(err.Error())
	out.mergeFromText(out.Raw)
	combinedText := strings.ToLower(strings.TrimSpace(out.Raw + " " + out.Message + " " + out.Code))
	if out.StatusCode == http.StatusTooManyRequests || out.Code == "429" || out.Code == "rate_limit_exceeded" || strings.Contains(combinedText, "rate-limit") || strings.Contains(combinedText, "rate limit") || strings.Contains(combinedText, "rate limited") || strings.Contains(combinedText, "too many requests") {
		out.Category = "rate_limit"
		out.Retryable = true
	}
	if strings.Contains(combinedText, "timeout") {
		out.Category = "timeout"
		out.Retryable = true
	}
	if containsAny(combinedText, "context_length_exceeded", "context window", "exceeds the context") {
		out.Category = "context_window"
		out.Retryable = false
	}
	if containsAny(combinedText, "insufficient_quota", "quota exceeded", "current quota", "billing details", "spend cap", "spend limit") {
		out.Category = "quota"
		out.Retryable = false
	}
	if containsAny(combinedText, "usage_not_included", "usage not included") {
		out.Category = "usage_not_included"
		out.Retryable = false
	}
	if containsAny(combinedText, "cyber_policy", "cyber policy") {
		out.Category = "cyber_policy"
		out.Retryable = false
	}
	if containsAny(combinedText, "invalid_prompt", "invalid prompt") {
		out.Category = "invalid_request"
		out.Retryable = false
	}
	if containsAny(combinedText, "server_is_overloaded", "server_overloaded", "server overloaded", "slow_down", "overloaded", "service unavailable") {
		out.Category = "server_overloaded"
		out.Retryable = true
	}
	if out.Code == "" && out.StatusCode > 0 {
		out.Code = strconv.Itoa(out.StatusCode)
	}
	if strings.Contains(combinedText, "byok") || strings.Contains(combinedText, "add your own key") {
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
		latest := compactPromptSection(latestExternalOrUserMessageText(req.Messages), 160)
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
	add("file", stringValue(args, "file"))
	add("command", stringValue(args, "command"))
	add("workdir", stringValue(args, "workdir"))
	add("pattern", stringValue(args, "pattern"))
	add("owner_node_id", stringValue(args, "owner_node_id"))
	if commands := normalizeTaskStateList(stringSliceValue(args, "commands"), 8); len(commands) > 0 {
		add("commands", strings.Join(commands, " | "))
	}
	if patch := stringValue(args, "patch"); strings.TrimSpace(patch) != "" {
		addPatchArgumentEntities(entities, patch)
	}
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
	for _, key := range []string{
		"effect",
		"path",
		"command",
		"workdir",
		"work_dir",
		"branch",
		"commit_sha",
		"pr_url",
		"owner_node_id",
		"job_id",
		"bundle_id",
		"mutation_class",
		"verification_status",
		"command_execution_status",
		"patch_apply_status",
		"mcp_server",
		"mcp_tool",
		"mcp_call_id",
		"mcp_namespaced_tool",
	} {
		if value, ok := meta[key]; ok {
			add(key, strings.TrimSpace(fmt.Sprintf("%v", value)))
		}
	}
	for _, key := range []string{"paths", "commands", "changed_paths"} {
		if values := toolMetaStringSlice(meta, key); len(values) > 0 {
			add(key, strings.Join(values, ", "))
		}
	}
	for _, key := range []string{"changed_count", "patch_operation_count", "add_count", "update_count", "delete_count", "move_count"} {
		if value, ok := meta[key]; ok {
			add(key, strings.TrimSpace(fmt.Sprintf("%v", value)))
		}
	}
	for _, key := range []string{"success", "clean", "changed_workspace", "requires_verification", "verification_like", "verification_evidence", "verification_approved", "verification_declined", "mcp_has_meta", "mcp_is_error"} {
		if value, ok := meta[key]; ok {
			add(key, strings.TrimSpace(fmt.Sprintf("%v", value)))
		}
	}
	return entities
}

func toolResultEventMetadata(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	mcpResult := map[string]any{}
	if value, ok := meta["mcp_result_content"]; ok {
		mcpResult["content"] = value
	}
	if value, ok := meta["mcp_result_structured_content"]; ok {
		mcpResult["structuredContent"] = value
	}
	if value, ok := meta["mcp_is_error"]; ok {
		mcpResult["isError"] = value
	}
	if value, ok := meta["_meta"]; ok {
		mcpResult["_meta"] = value
	}
	if len(mcpResult) == 0 {
		return nil
	}
	return map[string]any{
		"mcp_result": mcpResult,
	}
}

func unifiedDiffFileCount(diff string) int {
	count := 0
	for _, line := range strings.Split(strings.ReplaceAll(strings.TrimSpace(diff), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "diff --git ") {
			count++
		}
	}
	return count
}

func addPatchArgumentEntities(entities map[string]string, patch string) {
	if entities == nil {
		return
	}
	doc, err := parsePatchDocument(patch)
	if err != nil {
		return
	}
	if len(doc.ops) == 0 {
		return
	}
	paths := make([]string, 0, len(doc.ops)*2)
	added := 0
	updated := 0
	deleted := 0
	moved := 0
	for _, op := range doc.ops {
		if path := strings.TrimSpace(op.path); path != "" {
			paths = append(paths, path)
		}
		switch strings.ToLower(strings.TrimSpace(op.kind)) {
		case "add":
			added++
		case "delete":
			deleted++
		case "update":
			updated++
			if moveTo := strings.TrimSpace(op.moveTo); moveTo != "" {
				moved++
				paths = append(paths, moveTo)
			}
		}
	}
	if normalized := normalizeTaskStateList(paths, 32); len(normalized) > 0 {
		entities["changed_paths"] = strings.Join(normalized, ", ")
	}
	entities["patch_operation_count"] = strconv.Itoa(len(doc.ops))
	if added > 0 {
		entities["add_count"] = strconv.Itoa(added)
	}
	if updated > 0 {
		entities["update_count"] = strconv.Itoa(updated)
	}
	if deleted > 0 {
		entities["delete_count"] = strconv.Itoa(deleted)
	}
	if moved > 0 {
		entities["move_count"] = strconv.Itoa(moved)
	}
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
