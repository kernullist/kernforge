package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ToolContractSyntheticKind string

const (
	ToolContractSyntheticBlocked     ToolContractSyntheticKind = "blocked"
	ToolContractSyntheticSkipped     ToolContractSyntheticKind = "skipped"
	ToolContractSyntheticAborted     ToolContractSyntheticKind = "aborted"
	ToolContractSyntheticInvalid     ToolContractSyntheticKind = "invalid"
	ToolContractSyntheticIncomplete  ToolContractSyntheticKind = "incomplete"
	ToolContractSyntheticUnsupported ToolContractSyntheticKind = "unsupported"
)

type ToolContractIssue struct {
	Kind     ToolContractSyntheticKind `json:"kind,omitempty"`
	CallID   string                    `json:"call_id,omitempty"`
	ToolName string                    `json:"tool_name,omitempty"`
	Reason   string                    `json:"reason,omitempty"`
}

type ToolContractSyntheticResult struct {
	Call     ToolCall                  `json:"call"`
	Kind     ToolContractSyntheticKind `json:"kind"`
	Reason   string                    `json:"reason,omitempty"`
	Guidance string                    `json:"guidance,omitempty"`
	IsError  bool                      `json:"is_error,omitempty"`
}

type ToolContractNormalizationOptions struct {
	Registry   *ToolRegistry
	StopReason string
}

type ToolContractNormalizationResult struct {
	Calls            []ToolCall
	Issues           []ToolContractIssue
	SyntheticResults []ToolContractSyntheticResult
}

type ToolContractValidationOptions struct {
	Registry *ToolRegistry
	MCP      *MCPManager
	Session  *Session
}

func NormalizeAssistantToolCalls(calls []ToolCall, opts ToolContractNormalizationOptions) ToolContractNormalizationResult {
	result := ToolContractNormalizationResult{
		Calls: make([]ToolCall, 0, len(calls)),
	}
	if len(calls) == 0 {
		return result
	}
	incomplete := toolContractStopReasonIncomplete(opts.StopReason)
	seenIDs := map[string]int{}
	for index, raw := range calls {
		call := raw
		call.ID = strings.TrimSpace(call.ID)
		call.Name = strings.TrimSpace(call.Name)
		call.Namespace = strings.TrimSpace(call.Namespace)
		call.Status = strings.TrimSpace(call.Status)
		if call.ID == "" {
			call.ID = toolContractGeneratedCallID(index, call.Name, seenIDs)
			result.Issues = append(result.Issues, ToolContractIssue{
				Kind:     ToolContractSyntheticInvalid,
				CallID:   call.ID,
				ToolName: call.Name,
				Reason:   "tool call was missing an id; generated a stable local id",
			})
		}
		if seen := seenIDs[call.ID]; seen > 0 {
			original := call.ID
			call.ID = fmt.Sprintf("%s__duplicate_%d", original, seen+1)
			result.Issues = append(result.Issues, ToolContractIssue{
				Kind:     ToolContractSyntheticInvalid,
				CallID:   call.ID,
				ToolName: call.Name,
				Reason:   "tool call id was duplicated; generated a unique local id",
			})
		}
		seenIDs[call.ID]++
		if strings.TrimSpace(call.Arguments) == "" {
			call.Arguments = "{}"
		}
		result.Calls = append(result.Calls, call)
		if incomplete {
			result.SyntheticResults = append(result.SyntheticResults, ToolContractSyntheticResult{
				Call:    call,
				Kind:    ToolContractSyntheticIncomplete,
				Reason:  "INCOMPLETE: model stopped before the tool call could be trusted; this partial tool call was not executed.",
				IsError: true,
			})
			continue
		}
		if call.Name == "" || !toolContractRegistryHasTool(opts.Registry, call.Name) {
			reason := "UNSUPPORTED: requested tool is not available in this runtime."
			if call.Name == "" {
				reason = "UNSUPPORTED: requested tool call had no tool name."
			}
			result.SyntheticResults = append(result.SyntheticResults, ToolContractSyntheticResult{
				Call:    call,
				Kind:    ToolContractSyntheticUnsupported,
				Reason:  reason,
				IsError: true,
			})
			continue
		}
		if _, err := toolContractParseArgumentsObject(call.Arguments); err != nil {
			result.SyntheticResults = append(result.SyntheticResults, ToolContractSyntheticResult{
				Call:     call,
				Kind:     ToolContractSyntheticInvalid,
				Reason:   fmt.Sprintf("INVALID: invalid JSON tool arguments; tool arguments must be a valid JSON object: %v", err),
				Guidance: invalidToolArgumentsGuidance(call.Name),
				IsError:  true,
			})
			continue
		}
	}
	return result
}

func ValidateToolCallsAgainstEnvelope(calls []ToolCall, envelope RequestEnvelope, opts ToolContractValidationOptions) []ToolContractSyntheticResult {
	if len(calls) == 0 {
		return nil
	}
	envelope.Normalize()
	results := make([]ToolContractSyntheticResult, 0)
	for _, call := range calls {
		if !envelope.AllowsGitMutation && toolCallMutatesGitState(call) {
			results = append(results, ToolContractSyntheticResult{
				Call:     call,
				Kind:     ToolContractSyntheticBlocked,
				Reason:   "NOT_EXECUTED: git write actions require an explicit user request; continue without staging, committing, pushing, or opening a PR.",
				Guidance: "Do not stage, commit, push, or open a PR unless the user explicitly asks for a git action first. Continue with inspection, edits, verification, and a summary instead.",
				IsError:  true,
			})
			continue
		}
		if !envelope.AllowsFileMutation && !toolCallMutatesGitState(call) && toolContractCallMutatesWorkspace(call, opts.Registry) {
			results = append(results, ToolContractSyntheticResult{
				Call:     call,
				Kind:     ToolContractSyntheticBlocked,
				Reason:   "NOT_EXECUTED: this is a read-only analysis turn; edit tools are blocked.",
				Guidance: "This request is analysis-only. Do not edit files or call edit tools. Investigate the current code and logs, then answer with the root cause or findings.",
				IsError:  true,
			})
			continue
		}
		if !envelope.AllowsWebResearch && toolContractCallUsesWebResearch(call, opts.MCP) {
			results = append(results, ToolContractSyntheticResult{
				Call:     call,
				Kind:     ToolContractSyntheticBlocked,
				Reason:   "NOT_EXECUTED: external research tools are blocked for this request unless the user explicitly asks for external research.",
				Guidance: "Do not use web/search/browser research tools for this turn unless the latest external user request explicitly asks for current external research. Use local source evidence and available local tools instead.",
				IsError:  true,
			})
			continue
		}
	}
	if block, targetPath, parentPath := shouldBlockUnconfirmedDocumentReadToolCalls(calls, opts.Session); block {
		reason := "NOT_EXECUTED: document read was deferred until the target path is confirmed by listing the parent directory."
		guidance := fmt.Sprintf("This request is document/report authoring work. Do not guess that generated files already exist and call read_file on them immediately. First use list_files on the parent directory %s to confirm whether %s actually exists. If the parent directory is empty or the file is absent, treat the document as not created yet and create or update it with edit tools instead.", parentPath, targetPath)
		for _, call := range calls {
			if strings.TrimSpace(call.Name) != "read_file" || normalizeSessionRelativePath(toolCallPathArgument(call)) != targetPath {
				continue
			}
			results = append(results, ToolContractSyntheticResult{
				Call:     call,
				Kind:     ToolContractSyntheticBlocked,
				Reason:   reason,
				Guidance: guidance,
				IsError:  true,
			})
		}
	}
	return mergeToolContractSyntheticResults(results)
}

func BuildSyntheticToolResult(call ToolCall, kind ToolContractSyntheticKind, reason string) ToolExecutionResult {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = defaultToolContractSyntheticReason(kind)
	}
	payload, _ := toolContractParseArgumentsObject(call.Arguments)
	meta := defaultToolExecutionMeta(call.Name, payload)
	meta["success"] = false
	meta["changed_workspace"] = false
	meta["tool_contract_result"] = string(kind)
	meta["synthetic_result"] = true
	meta["reason"] = reason
	switch kind {
	case ToolContractSyntheticBlocked:
		meta["status"] = "blocked"
	case ToolContractSyntheticSkipped:
		meta["status"] = "skipped"
		meta["deferred"] = true
		meta["requires_reissue"] = true
	case ToolContractSyntheticAborted:
		meta["status"] = "aborted"
	case ToolContractSyntheticInvalid:
		meta["status"] = "invalid"
	case ToolContractSyntheticIncomplete:
		meta["status"] = "incomplete"
	case ToolContractSyntheticUnsupported:
		meta["status"] = "unsupported"
	default:
		meta["status"] = "synthetic"
	}
	if toolCallIsExecCommandLike(call.Name) {
		meta["command_execution_status"] = stringValueOrDefault(meta["status"], "synthetic")
	}
	if toolCallIsPatchApplyLike(call.Name) {
		meta["patch_apply_status"] = stringValueOrDefault(meta["status"], "synthetic")
	}
	if toolCallIsMCPToolLike(call.Name) {
		meta["mcp_is_error"] = true
	}
	return ToolExecutionResult{
		DisplayText: reason,
		Meta:        meta,
	}
}

func ValidateConversationToolPairs(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}
	out := make([]Message, 0, len(messages))
	for index := 0; index < len(messages); index++ {
		msg := messages[index]
		if msg.Role == "tool" {
			continue
		}
		out = append(out, msg)
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		expected := map[string]ToolCall{}
		expectedOrder := make([]string, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			callID := firstNonEmptyTrimmed(call.ID, call.Name)
			if callID == "" {
				continue
			}
			if _, exists := expected[callID]; !exists {
				expectedOrder = append(expectedOrder, callID)
			}
			expected[callID] = call
		}
		if len(expectedOrder) == 0 {
			continue
		}
		matched := map[string]Message{}
		next := index + 1
		for next < len(messages) && messages[next].Role == "tool" {
			toolMsg := messages[next]
			callID := firstNonEmptyTrimmed(toolMsg.ToolCallID, toolMsg.ToolName)
			call, ok := expected[callID]
			if ok {
				if strings.TrimSpace(toolMsg.ToolName) == "" {
					toolMsg.ToolName = call.Name
				}
				if _, exists := matched[callID]; !exists {
					matched[callID] = toolMsg
				}
			}
			next++
		}
		for _, callID := range expectedOrder {
			call := expected[callID]
			if toolMsg, ok := matched[callID]; ok {
				if toolContractToolMessageIncomplete(toolMsg) {
					out = append(out, toolContractSyntheticMessage(call, ToolContractSyntheticAborted, "aborted", true))
				} else {
					out = append(out, toolMsg)
				}
				continue
			}
			text := missingOpenAIToolResultText(messages, next)
			kind := ToolContractSyntheticAborted
			if strings.Contains(strings.ToLower(text), "superseded") {
				kind = ToolContractSyntheticSkipped
			}
			out = append(out, toolContractSyntheticMessage(call, kind, text, strings.HasPrefix(text, "ERROR:")))
		}
		index = next - 1
	}
	return out
}

func toolContractGeneratedCallID(index int, name string, seen map[string]int) string {
	base := strings.Trim(strings.ToLower(strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '_' || r == '-':
			return r
		default:
			return '_'
		}
	}, strings.TrimSpace(name))), "_-")
	if base == "" {
		base = "tool"
	}
	id := fmt.Sprintf("tool_call_%02d_%s", index+1, base)
	for seen[id] > 0 {
		id = fmt.Sprintf("%s_%d", id, seen[id]+1)
	}
	return id
}

func toolContractStopReasonIncomplete(reason string) bool {
	switch normalizeStopReason(reason) {
	case "length", "max_tokens", "incomplete", "stream_incomplete", "context_length_exceeded":
		return true
	default:
		return false
	}
}

func toolContractRegistryHasTool(registry *ToolRegistry, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || registry == nil {
		return false
	}
	_, ok := registry.tools[name]
	return ok
}

func toolContractParseArgumentsObject(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return payload, nil
}

func toolContractCallMutatesWorkspace(call ToolCall, registry *ToolRegistry) bool {
	if isEditTool(call.Name) || shellToolCallMayWriteWorkspace(call) {
		return true
	}
	if toolCallMutatesGitState(call) {
		return true
	}
	name := strings.TrimSpace(call.Name)
	if registry != nil && name != "" && !registry.ToolCallReadOnly(name) {
		switch name {
		case "run_shell", "run_shell_background", "run_shell_bundle_background":
			return shellToolCallMayWriteWorkspace(call)
		}
	}
	return false
}

func toolContractCallUsesWebResearch(call ToolCall, mcp *MCPManager) bool {
	if mcp != nil && mcp.IsWebResearchToolCall(call) {
		return true
	}
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if name == "" {
		return false
	}
	if strings.Contains(name, "web") || strings.Contains(name, "browser") {
		return true
	}
	return strings.HasPrefix(name, "search_") || strings.HasSuffix(name, "_search")
}

func mergeToolContractSyntheticResults(items []ToolContractSyntheticResult) []ToolContractSyntheticResult {
	if len(items) == 0 {
		return nil
	}
	out := make([]ToolContractSyntheticResult, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		key := firstNonEmptyTrimmed(item.Call.ID, item.Call.Name)
		if key == "" {
			key = fmt.Sprintf("%d", len(out))
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func defaultToolContractSyntheticReason(kind ToolContractSyntheticKind) string {
	switch kind {
	case ToolContractSyntheticBlocked:
		return "NOT_EXECUTED: tool call was blocked by the runtime policy."
	case ToolContractSyntheticSkipped:
		return "NOT_EXECUTED: tool call was skipped because another tool call in the same response required a retry."
	case ToolContractSyntheticAborted:
		return "aborted"
	case ToolContractSyntheticInvalid:
		return "INVALID: tool call was malformed and was not executed."
	case ToolContractSyntheticIncomplete:
		return "INCOMPLETE: model stopped before the tool call could be trusted; this partial tool call was not executed."
	case ToolContractSyntheticUnsupported:
		return "UNSUPPORTED: requested tool is not available in this runtime."
	default:
		return "NOT_EXECUTED: synthetic tool result."
	}
}

func toolContractSyntheticKindForReason(reason string) ToolContractSyntheticKind {
	lower := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(lower, "invalid"):
		return ToolContractSyntheticInvalid
	case strings.Contains(lower, "incomplete"):
		return ToolContractSyntheticIncomplete
	case strings.Contains(lower, "unsupported"):
		return ToolContractSyntheticUnsupported
	case strings.Contains(lower, "aborted"):
		return ToolContractSyntheticAborted
	case strings.Contains(lower, "blocked") ||
		strings.Contains(lower, "read-only") ||
		strings.Contains(lower, "explicit user request") ||
		strings.Contains(lower, "not allowed"):
		return ToolContractSyntheticBlocked
	default:
		return ToolContractSyntheticSkipped
	}
}

func toolContractSyntheticMessage(call ToolCall, kind ToolContractSyntheticKind, reason string, isError bool) Message {
	result := BuildSyntheticToolResult(call, kind, reason)
	return Message{
		Role:             "tool",
		ToolCallID:       firstNonEmptyTrimmed(call.ID, call.Name),
		ToolName:         call.Name,
		Text:             toolExecutionModelText(result),
		ToolContentItems: toolExecutionModelContentItems(result),
		ToolMeta:         result.Meta,
		IsError:          isError,
	}
}

func toolContractToolMessageIncomplete(msg Message) bool {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return true
	}
	return strings.HasPrefix(text, "IN_PROGRESS:")
}

func stringValueOrDefault(raw any, fallback string) string {
	text, ok := raw.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return fallback
	}
	return strings.TrimSpace(text)
}
