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
	callback(event)
}

func formatProgressEventMessage(cfg Config, event ProgressEvent) string {
	if strings.TrimSpace(event.Message) != "" {
		return formatProgressEventMessageWithContext(event, strings.TrimSpace(event.Message))
	}
	target := formatProgressEventTarget(event)
	switch strings.TrimSpace(event.Kind) {
	case progressKindModelRequestStart:
		if target == "" {
			return formatProgressEventMessageWithContext(event, localizedText(cfg, "Model request started.", "모델 요청 시작."))
		}
		return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model request started: %s", "모델 요청 시작: %s"), target))
	case progressKindModelRequestWait:
		if target == "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Waiting for model response (%s)...", "모델 응답 대기 중 (%s) ..."), formatProgressElapsed(event.Elapsed)))
		}
		return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Waiting for model response from %s (%s)...", "%s 모델 응답 대기 중 (%s) ..."), target, formatProgressElapsed(event.Elapsed)))
	case progressKindModelRequestDone:
		status := firstNonBlankString(event.Status, "completed")
		if event.Elapsed > 0 {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model request %s in %s.", "모델 요청 %s (%s)."), status, formatProgressElapsed(event.Elapsed)))
		}
		return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model request %s.", "모델 요청 %s."), status))
	case progressKindModelRouteWait:
		if strings.TrimSpace(event.RouteLabel) != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Waiting for model route: %s", "모델 route 대기 중: %s"), event.RouteLabel))
		}
		return formatProgressEventMessageWithContext(event, localizedText(cfg, "Waiting for model route permit...", "모델 route permit 대기 중 ..."))
	case progressKindModelRouteAcquired:
		if event.Elapsed > 0 && strings.TrimSpace(event.RouteLabel) != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model route ready: %s (waited %s)", "모델 route 준비됨: %s (%s 대기)"), event.RouteLabel, formatProgressElapsed(event.Elapsed)))
		}
		if strings.TrimSpace(event.RouteLabel) != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model route ready: %s", "모델 route 준비됨: %s"), event.RouteLabel))
		}
		return formatProgressEventMessageWithContext(event, localizedText(cfg, "Model route ready.", "모델 route 준비됨."))
	case progressKindModelStreamToolCall:
		if event.ToolName != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model is preparing tool call: %s", "모델이 tool call 준비 중: %s"), event.ToolName))
		}
		return formatProgressEventMessageWithContext(event, localizedText(cfg, "Model is preparing a tool call.", "모델이 tool call 준비 중."))
	case progressKindModelStreamToolArgs:
		if event.ToolName != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model is filling tool arguments for %s...", "모델이 %s 인자를 작성 중 ..."), event.ToolName))
		}
		return formatProgressEventMessageWithContext(event, localizedText(cfg, "Model is filling tool arguments...", "모델이 tool 인자를 작성 중 ..."))
	case progressKindModelStreamToolReady:
		if event.ToolName != "" && event.ArgumentsPreview != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model prepared tool call: %s %s", "모델 tool call 준비 완료: %s %s"), event.ToolName, event.ArgumentsPreview))
		}
		if event.ToolName != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model prepared tool call: %s", "모델 tool call 준비 완료: %s"), event.ToolName))
		}
		return formatProgressEventMessageWithContext(event, localizedText(cfg, "Model prepared a tool call.", "모델 tool call 준비 완료."))
	case progressKindModelReroute:
		if event.Model != "" && event.Status != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Server routed request to %s instead of %s.", "서버가 요청을 %s로 라우팅했습니다(요청 모델: %s)."), event.Status, event.Model))
		}
		return formatProgressEventMessageWithContext(event, localizedText(cfg, "Server reported a different model route.", "서버가 다른 모델 route를 보고했습니다."))
	case progressKindModelVerification:
		if event.Status != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Model verification received: %s", "모델 verification 수신: %s"), event.Status))
		}
		return formatProgressEventMessageWithContext(event, localizedText(cfg, "Model verification received.", "모델 verification 수신."))
	case progressKindToolStarted:
		if event.ToolName != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "Using %s...", "%s 실행 중 ..."), event.ToolName))
		}
	case progressKindToolCompleted:
		if event.ToolName != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "%s completed.", "%s 완료."), event.ToolName))
		}
	case progressKindToolFailed:
		if event.ToolName != "" && event.Status != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "%s failed: %s", "%s 실패: %s"), event.ToolName, event.Status))
		}
		if event.ToolName != "" {
			return formatProgressEventMessageWithContext(event, fmt.Sprintf(localizedText(cfg, "%s failed.", "%s 실패."), event.ToolName))
		}
	}
	return formatProgressEventMessageWithContext(event, strings.TrimSpace(event.Message))
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
		progressKindProviderRetry:
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
