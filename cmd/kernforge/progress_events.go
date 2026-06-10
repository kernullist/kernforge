package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	progressKindModelRequestStart    = "model_request_start"
	progressKindModelRequestWait     = "model_request_wait"
	progressKindModelRequestDone     = "model_request_done"
	progressKindModelRouteWait       = "model_route_wait"
	progressKindModelRouteAcquired   = "model_route_acquired"
	progressKindModelStreamToolCall  = "model_stream_tool_call"
	progressKindModelStreamToolArgs  = "model_stream_tool_arguments"
	progressKindModelStreamToolReady = "model_stream_tool_ready"
	progressKindModelReroute         = "model_reroute"
	progressKindModelVerification    = "model_verification"
	progressKindToolStarted          = "tool_started"
	progressKindToolCompleted        = "tool_completed"
	progressKindToolFailed           = "tool_failed"
	progressKindProviderRetry        = "provider_retry"
	progressKindMemoryContext        = "memory_context"
	progressKindAnalysisContext      = "analysis_context"
	progressKindRuntimeIntervention  = "runtime_intervention"
)

func emitProgressEvent(callback func(ProgressEvent), event ProgressEvent) {
	if callback == nil {
		return
	}
	event.Kind = strings.TrimSpace(event.Kind)
	event.Message = strings.TrimSpace(event.Message)
	event.Provider = normalizeProviderName(event.Provider)
	event.Model = strings.TrimSpace(event.Model)
	event.ToolName = strings.TrimSpace(event.ToolName)
	event.ToolCallID = strings.TrimSpace(event.ToolCallID)
	event.ArgumentsPreview = truncateStatusSnippet(strings.TrimSpace(event.ArgumentsPreview), 160)
	event.RouteLabel = strings.TrimSpace(event.RouteLabel)
	event.Stage = strings.TrimSpace(event.Stage)
	event.Shard = strings.TrimSpace(event.Shard)
	event.Status = strings.TrimSpace(event.Status)
	event.RuntimeState = strings.TrimSpace(event.RuntimeState)
	event.RuntimeIntervention = strings.TrimSpace(event.RuntimeIntervention)
	callback(event)
}

func formatProgressEventMessage(cfg Config, event ProgressEvent) string {
	if strings.TrimSpace(event.Message) != "" {
		return formatProgressEventMessageWithContext(event, humanizeProgressMessage(cfg, strings.TrimSpace(event.Message)))
	}
	target := formatProgressEventTarget(event)
	switch strings.TrimSpace(event.Kind) {
	case progressKindModelRequestStart:
		if target == "" {
			return formatProgressEventMessageWithContext(event, localizedText(cfg, "Sent the request to the model.", "모델에 요청을 보냈습니다."))
		}
		return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Sent the request to %s.", "%s에 요청을 보냈습니다."), target))
	case progressKindModelRequestWait:
		if target == "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Waiting for the model answer (%s elapsed).", "모델 답변을 기다리는 중입니다(%s 경과)."), formatProgressElapsed(event.Elapsed)))
		}
		return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Waiting for %s to answer (%s elapsed).", "%s 답변을 기다리는 중입니다(%s 경과)."), target, formatProgressElapsed(event.Elapsed)))
	case progressKindModelRequestDone:
		return formatProgressEventMessageWithContext(event, formatProgressModelDoneMessage(cfg, event.Status, event.Elapsed))
	case progressKindModelRouteWait:
		if strings.TrimSpace(event.RouteLabel) != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Waiting for this model slot: %s.", "이 모델의 실행 순서를 기다리는 중입니다: %s."), event.RouteLabel))
		}
		return formatProgressEventMessageWithContext(event, localizedText(cfg, "Waiting for an available model slot.", "사용 가능한 모델 실행 순서를 기다리는 중입니다."))
	case progressKindModelRouteAcquired:
		if event.Elapsed > 0 && strings.TrimSpace(event.RouteLabel) != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model slot is ready: %s (waited %s).", "모델 실행 순서가 준비되었습니다: %s(%s 대기)."), event.RouteLabel, formatProgressElapsed(event.Elapsed)))
		}
		if strings.TrimSpace(event.RouteLabel) != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model slot is ready: %s.", "모델 실행 순서가 준비되었습니다: %s."), event.RouteLabel))
		}
		return formatProgressEventMessageWithContext(event, localizedText(cfg, "Model slot is ready.", "모델 실행 순서가 준비되었습니다."))
	case progressKindModelStreamToolCall:
		if event.ToolName != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model is choosing a tool: %s.", "모델이 사용할 도구를 고르는 중입니다: %s."), event.ToolName))
		}
		return formatProgressEventMessageWithContext(event, localizedText(cfg, "Model is choosing a tool.", "모델이 사용할 도구를 고르는 중입니다."))
	case progressKindModelStreamToolArgs:
		if event.ToolName != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model is preparing inputs for %s.", "모델이 %s 입력값을 준비 중입니다."), event.ToolName))
		}
		return formatProgressEventMessageWithContext(event, localizedText(cfg, "Model is preparing tool inputs.", "모델이 도구 입력값을 준비 중입니다."))
	case progressKindModelStreamToolReady:
		if event.ToolName != "" && event.ArgumentsPreview != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Tool request is ready: %s (%s).", "도구 요청이 준비되었습니다: %s(%s)."), event.ToolName, event.ArgumentsPreview))
		}
		if event.ToolName != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Tool request is ready: %s.", "도구 요청이 준비되었습니다: %s."), event.ToolName))
		}
		return formatProgressEventMessageWithContext(event, localizedText(cfg, "Tool request is ready.", "도구 요청이 준비되었습니다."))
	case progressKindModelReroute:
		if event.Model != "" && event.Status != "" {
			if localePrefersKorean(cfg) {
				return formatProgressEventMessageWithContext(event, fmt.Sprintf("서버가 요청 모델 %s 대신 %s를 사용했습니다.", event.Model, event.Status))
			}
			return formatProgressEventMessageWithContext(event, fmt.Sprintf("Server used %s instead of the requested model %s.", event.Status, event.Model))
		}
		return formatProgressEventMessageWithContext(event, localizedText(cfg, "Server reported that a different model was used.", "서버가 다른 모델을 사용했다고 보고했습니다."))
	case progressKindModelVerification:
		if event.Status != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model identity check received: %s.", "모델 확인 정보를 받았습니다: %s."), event.Status))
		}
		return formatProgressEventMessageWithContext(event, localizedText(cfg, "Model identity check received.", "모델 확인 정보를 받았습니다."))
	case progressKindToolStarted:
		if event.ToolName != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Running tool: %s.", "도구 실행 중: %s."), event.ToolName))
		}
	case progressKindToolCompleted:
		if event.ToolName != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Tool finished: %s.", "도구 완료: %s."), event.ToolName))
		}
	case progressKindToolFailed:
		if event.ToolName != "" && event.Status != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Tool failed: %s (%s).", "도구 실패: %s(%s)."), event.ToolName, event.Status))
		}
		if event.ToolName != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Tool failed: %s.", "도구 실패: %s."), event.ToolName))
		}
	case progressKindRuntimeIntervention:
		if event.RuntimeIntervention != "" && event.Status != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Runtime intervention: %s (%s).", "런타임 개입: %s(%s)."), event.RuntimeIntervention, event.Status))
		}
		if event.RuntimeIntervention != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Runtime intervention: %s.", "런타임 개입: %s."), event.RuntimeIntervention))
		}
	}
	return formatProgressEventMessageWithContext(event, humanizeProgressMessage(cfg, strings.TrimSpace(event.Message)))
}

func formatProgressEventTarget(event ProgressEvent) string {
	provider := strings.TrimSpace(providerUserLabel(event.Provider))
	model := strings.TrimSpace(event.Model)
	switch {
	case provider != "" && model != "":
		return provider + " / " + model
	case provider != "":
		return provider
	case model != "":
		return model
	default:
		return ""
	}
}

func formatProgressEventMessageWithContext(event ProgressEvent, message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	if !progressEventAllowsContextPrefix(event) {
		return message
	}
	prefixParts := []string{}
	if stage := strings.TrimSpace(event.Stage); stage != "" {
		prefixParts = append(prefixParts, stage)
	}
	if shard := strings.TrimSpace(event.Shard); shard != "" {
		prefixParts = append(prefixParts, shard)
	}
	if len(prefixParts) == 0 {
		return message
	}
	return strings.Join(prefixParts, " ") + ": " + message
}

func progressEventAllowsContextPrefix(event ProgressEvent) bool {
	switch strings.TrimSpace(event.Kind) {
	case progressKindModelRequestStart,
		progressKindModelRequestWait,
		progressKindModelRequestDone,
		progressKindModelRouteWait,
		progressKindModelRouteAcquired,
		progressKindModelStreamToolCall,
		progressKindModelStreamToolArgs,
		progressKindModelStreamToolReady,
		progressKindModelReroute,
		progressKindModelVerification,
		progressKindProviderRetry,
		progressKindRuntimeIntervention:
		return true
	default:
		return false
	}
}

func formatProgressElapsed(elapsed time.Duration) string {
	if elapsed <= 0 {
		return "0s"
	}
	return elapsed.Round(time.Second).String()
}

func formatProgressModelDoneMessage(cfg Config, status string, elapsed time.Duration) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if elapsed > 0 {
		elapsedText := formatProgressElapsed(elapsed)
		switch status {
		case "failed", "failure", "error":
			return fmt.Sprintf(localizedText(cfg, "Model answer failed after %s.", "모델 답변이 실패했습니다(%s)."), elapsedText)
		case "cancelled", "canceled", "cancel":
			return fmt.Sprintf(localizedText(cfg, "Model answer was canceled after %s.", "모델 답변이 취소되었습니다(%s)."), elapsedText)
		default:
			return fmt.Sprintf(localizedText(cfg, "Model answer completed after %s.", "모델 답변이 완료되었습니다(%s)."), elapsedText)
		}
	}
	switch status {
	case "failed", "failure", "error":
		return localizedText(cfg, "Model answer failed.", "모델 답변이 실패했습니다.")
	case "cancelled", "canceled", "cancel":
		return localizedText(cfg, "Model answer was canceled.", "모델 답변이 취소되었습니다.")
	default:
		return localizedText(cfg, "Model answer completed.", "모델 답변이 완료되었습니다.")
	}
}

func humanizeProgressMessage(cfg Config, text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "\n") {
		return trimmed
	}

	trimmed = stripProgressDiagnosticSuffix(trimmed)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.Contains(lower, "running automatic pre-write review"):
		return localizedText(cfg, "Reviewing the proposed edit before writing files.", "파일에 쓰기 전에 제안된 수정안을 리뷰하는 중입니다.")
	case strings.Contains(lower, "main model prepared an edit proposal"):
		return localizedText(cfg, "Proposed edit is ready; sending it to review before writing.", "수정안이 준비되어, 파일에 쓰기 전에 리뷰로 보냅니다.")
	case strings.Contains(lower, "running automatic post-change review"):
		return localizedText(cfg, "Reviewing the applied change.", "적용된 변경을 리뷰하는 중입니다.")
	case strings.Contains(lower, "automatic post-change review found blockers"):
		return localizedText(cfg, "Review found blockers; asking the model to revise.", "리뷰에서 차단 항목을 발견해 모델에 수정을 요청합니다.")
	case strings.Contains(lower, "automatic post-change review completed"):
		return localizedText(cfg, "Review completed.", "리뷰가 완료되었습니다.")
	case strings.Contains(lower, "automatic pre-write review found blockers"):
		return localizedText(cfg, "Review blocked the proposed edit; asking for a corrected patch.", "리뷰가 수정안을 차단했습니다. 수정된 패치를 다시 요청합니다.")
	case strings.Contains(lower, "review model returned required changes"):
		return localizedText(cfg, "Review requested code changes; sending them back to the model.", "리뷰가 코드 수정을 요구해 모델에 다시 전달합니다.")
	case strings.Contains(lower, "review model returned actionable warnings"):
		return localizedText(cfg, "Review found warnings that need a patch update.", "리뷰에서 패치 수정이 필요한 경고를 발견했습니다.")
	case strings.Contains(lower, "waiting for the model to summarize"):
		return localizedText(cfg, "Preparing the final answer.", "최종 답변을 정리하는 중입니다.")
	case strings.Contains(lower, "tool loop limit reached"):
		return localizedText(cfg, "Tool-use limit reached; asking the model to finish or choose a clearer next step.", "도구 사용 한도에 도달해, 모델에 마무리하거나 다음 단계를 다시 정하게 합니다.")
	case strings.Contains(lower, "current model does not support tool use"):
		return localizedText(cfg, "Current model cannot use tools; retrying without tools.", "현재 모델이 도구를 사용할 수 없어 도구 없이 다시 요청합니다.")
	}
	return trimmed
}

func stripProgressDiagnosticSuffix(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if idx := strings.Index(trimmed, " actor="); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	for {
		end := strings.LastIndex(trimmed, "]")
		start := strings.LastIndex(trimmed, "[")
		if end != len(trimmed)-1 || start < 0 || start >= end {
			break
		}
		suffix := strings.ToLower(trimmed[start+1 : end])
		if !containsAny(suffix, "phase=", "status=", "reason=", "waiting_on=", "next=", "actor=", "next_transition=") {
			break
		}
		trimmed = strings.TrimSpace(trimmed[:start])
	}
	return trimmed
}

func summarizeToolArgumentsPreview(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
		return truncateStatusSnippet(strings.Join(strings.Fields(trimmed), " "), 120)
	}
	parts := make([]string, 0, 4)
	for _, key := range []string{"path", "file", "pattern", "query", "command", "job_id", "bundle_id"} {
		value, ok := args[key]
		if !ok {
			continue
		}
		text := strings.TrimSpace(fmt.Sprintf("%v", value))
		if text == "" {
			continue
		}
		if key == "command" {
			text = summarizeShellCommand(text)
		}
		parts = append(parts, key+"="+truncateStatusSnippet(text, 80))
		if len(parts) >= 3 {
			break
		}
	}
	if len(parts) == 0 {
		return truncateStatusSnippet(strings.Join(strings.Fields(trimmed), " "), 120)
	}
	return strings.Join(parts, " ")
}
