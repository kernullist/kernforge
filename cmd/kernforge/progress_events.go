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
	progressKindPromptAssembly       = "prompt_assembly"
	// progressKindModelThought is durable operator-facing model working notes:
	// requirement analysis, plan direction, and provider reasoning summaries
	// before or between tools (Claude Code / Grok Build style).
	progressKindModelThought = "model_thought"
	// progressKindTurnPlan is the turn-start "what I'll do" line. Always durable
	// even in quiet mode so the operator sees orientation before long waits.
	progressKindTurnPlan = "turn_plan"
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
	event.PromptBlock = strings.TrimSpace(event.PromptBlock)
	callback(event)
}

func formatProgressEventMessage(cfg Config, event ProgressEvent) string {
	// Working notes / turn plans are already operator-facing prose; do not run
	// them through the generic progress humanizer (meant for status phrases).
	switch strings.TrimSpace(event.Kind) {
	case progressKindModelThought:
		if msg := strings.TrimSpace(event.Message); msg != "" {
			return msg
		}
		return localizedText(cfg, "Model working note", "모델 작업 메모")
	case progressKindTurnPlan:
		if msg := strings.TrimSpace(event.Message); msg != "" {
			return msg
		}
		return localizedText(cfg, "Starting work on your request.", "요청 작업을 시작합니다.")
	}
	if strings.TrimSpace(event.Message) != "" {
		return formatProgressEventMessageWithContext(cfg, event, humanizeProgressMessage(cfg, strings.TrimSpace(event.Message)))
	}
	switch strings.TrimSpace(event.Kind) {
	case progressKindModelRequestStart:
		// Industry-style: do not spam provider/model ids on every turn.
		return formatProgressEventMessageWithContext(cfg, event, localizedText(cfg, "Thinking...", "생각 중..."))
	case progressKindModelRequestWait:
		return formatProgressEventMessageWithContext(cfg, event, fmt.Sprintf(localizedText(cfg,
			"Still thinking (%s elapsed)...",
			"계속 생각 중... (%s 경과)"), formatProgressElapsed(event.Elapsed)))
	case progressKindModelRequestDone:
		return formatProgressEventMessageWithContext(cfg, event, formatProgressModelDoneMessage(cfg, event.Status, event.Elapsed))
	case progressKindModelRouteWait:
		if strings.TrimSpace(event.RouteLabel) != "" {
			return formatProgressEventMessageWithContext(cfg, event, fmt.Sprintf(localizedText(cfg, "Waiting for a free model slot: %s.", "모델 실행 순서를 기다리는 중: %s."), event.RouteLabel))
		}
		return formatProgressEventMessageWithContext(cfg, event, localizedText(cfg, "Waiting for a free model slot...", "모델 실행 순서를 기다리는 중..."))
	case progressKindModelRouteAcquired:
		if event.Elapsed > 0 {
			return formatProgressEventMessageWithContext(cfg, event, fmt.Sprintf(localizedText(cfg, "Model slot ready (waited %s).", "모델 실행 준비 완료 (%s 대기)."), formatProgressElapsed(event.Elapsed)))
		}
		return formatProgressEventMessageWithContext(cfg, event, localizedText(cfg, "Model slot ready.", "모델 실행 준비 완료."))
	case progressKindModelStreamToolCall, progressKindModelStreamToolArgs:
		// Suppress intermediate streaming noise (tool name pick / arg streaming).
		// Users see a natural action line when the tool actually starts.
		return ""
	case progressKindModelStreamToolReady:
		// Also quiet: toolStarted follows immediately with a better natural line.
		return ""
	case progressKindModelReroute:
		if event.Model != "" && event.Status != "" {
			if localePrefersKorean(cfg) {
				return formatProgressEventMessageWithContext(cfg, event, fmt.Sprintf("서버가 요청 모델 %s 대신 %s를 사용했습니다.", event.Model, event.Status))
			}
			return formatProgressEventMessageWithContext(cfg, event, fmt.Sprintf("Server used %s instead of the requested model %s.", event.Status, event.Model))
		}
		return formatProgressEventMessageWithContext(cfg, event, localizedText(cfg, "Server reported that a different model was used.", "서버가 다른 모델을 사용했다고 보고했습니다."))
	case progressKindModelVerification:
		if event.Status != "" {
			return formatProgressEventMessageWithContext(cfg, event, fmt.Sprintf(localizedText(cfg, "Model identity check received: %s.", "모델 확인 정보를 받았습니다: %s."), event.Status))
		}
		return formatProgressEventMessageWithContext(cfg, event, localizedText(cfg, "Model identity check received.", "모델 확인 정보를 받았습니다."))
	case progressKindToolStarted:
		if msg := humanizeToolProgressLine(cfg, event.ToolName, "started", event.ArgumentsPreview, ""); msg != "" {
			return formatProgressEventMessageWithContext(cfg, event, msg)
		}
	case progressKindToolCompleted:
		if msg := humanizeToolProgressLine(cfg, event.ToolName, "completed", event.ArgumentsPreview, event.Status); msg != "" {
			return formatProgressEventMessageWithContext(cfg, event, msg)
		}
	case progressKindToolFailed:
		if msg := humanizeToolProgressLine(cfg, event.ToolName, "failed", event.ArgumentsPreview, event.Status); msg != "" {
			return formatProgressEventMessageWithContext(cfg, event, msg)
		}
	case progressKindRuntimeIntervention:
		if event.RuntimeIntervention != "" {
			phrase := humanizeInterventionKind(event.RuntimeIntervention, localePrefersKorean(cfg))
			return formatProgressEventMessageWithContext(cfg, event, phrase+".")
		}
	case progressKindPromptAssembly:
		// The raw asset id (event.PromptBlock) and error detail (event.Status)
		// stay in the machine/debug fields; the human line is one plain sentence.
		return formatProgressEventMessageWithContext(cfg, event, localizedText(cfg,
			"A prompt section could not be built, so a safe default was used instead.",
			"프롬프트 일부를 만들지 못해 안전한 기본값으로 대체했습니다."))
	}
	return formatProgressEventMessageWithContext(cfg, event, humanizeProgressMessage(cfg, strings.TrimSpace(event.Message)))
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

func formatProgressEventMessageWithContext(cfg Config, event ProgressEvent, message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	if !progressEventAllowsContextPrefix(event) {
		return message
	}
	korean := localePrefersKorean(cfg)
	prefixParts := []string{}
	if stage := humanizeProgressStage(event.Stage, korean); stage != "" {
		prefixParts = append(prefixParts, stage)
	}
	// Suppress opaque numeric shard ids (e.g. "shard-03"); they mean nothing to
	// the user. Keep a meaningful named context only.
	if shard := humanizeProgressShard(event.Shard, korean); shard != "" {
		prefixParts = append(prefixParts, shard)
	}
	if len(prefixParts) == 0 {
		return message
	}
	return strings.Join(prefixParts, " ") + ": " + message
}

// humanizeProgressStage turns an internal stage label into a short, plain
// context word. Unknown stages fall back to a readable form of the raw label.
func humanizeProgressStage(value string, korean bool) string {
	stage := strings.TrimSpace(value)
	if stage == "" {
		return ""
	}
	switch strings.ToLower(stage) {
	case "targeted":
		if korean {
			return "집중 분석"
		}
		return "focused analysis"
	case "workspace":
		if korean {
			return "작업 영역"
		}
		return "workspace"
	case "planner":
		if korean {
			return "계획"
		}
		return "planning"
	case "reviewer":
		if korean {
			return "리뷰"
		}
		return "review"
	case "semantic_classifier", "classifier":
		// Soft label — avoid sounding like an internal subsystem.
		if korean {
			return "요청 이해"
		}
		return "understanding request"
	default:
		return humanizeEnumFallback(stage)
	}
}

// humanizeProgressShard returns a user-meaningful shard context, suppressing
// purely-numeric or "shard-NN" ids that carry no meaning for the user.
func humanizeProgressShard(value string, korean bool) string {
	shard := strings.TrimSpace(value)
	if shard == "" {
		return ""
	}
	lower := strings.ToLower(shard)
	trimmed := strings.TrimPrefix(lower, "shard-")
	trimmed = strings.TrimPrefix(trimmed, "shard")
	trimmed = strings.Trim(trimmed, "-_ ")
	if trimmed == "" || isAllDigits(trimmed) {
		return ""
	}
	return humanizeEnumFallback(shard)
}

func isAllDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
		progressKindModelThought,
		progressKindProviderRetry,
		progressKindRuntimeIntervention,
		progressKindPromptAssembly:
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
			return fmt.Sprintf(localizedText(cfg, "Thinking failed after %s.", "생각 중 오류 (%s)."), elapsedText)
		case "cancelled", "canceled", "cancel":
			return fmt.Sprintf(localizedText(cfg, "Thinking canceled after %s.", "생각 취소됨 (%s)."), elapsedText)
		default:
			// Quiet completion: the next tool/answer line is what users care about.
			if elapsed < 5*time.Second {
				return ""
			}
			return fmt.Sprintf(localizedText(cfg, "Finished thinking (%s).", "생각 정리 완료 (%s)."), elapsedText)
		}
	}
	switch status {
	case "failed", "failure", "error":
		return localizedText(cfg, "Thinking failed.", "생각 중 오류.")
	case "cancelled", "canceled", "cancel":
		return localizedText(cfg, "Thinking canceled.", "생각 취소됨.")
	default:
		return ""
	}
}

// humanizeToolProgressLine turns internal tool ids into Grok-style action lines.
// phase is started|completed|failed. detail is optional result count/error text.
// argsPreview is either a path/command snippet or a "key=value" preview string.
func humanizeToolProgressLine(cfg Config, toolName string, phase string, argsPreview string, detail string) string {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return ""
	}
	phase = strings.ToLower(strings.TrimSpace(phase))
	path, pattern, command, query := parseToolProgressArgs(argsPreview)
	base := strings.ToLower(name)
	if idx := strings.LastIndex(base, "__"); idx >= 0 {
		base = base[idx+2:]
	}

	switch {
	case base == "read_file" || base == "read" || strings.HasSuffix(base, "read_file"):
		target := firstNonBlankString(path, "")
		switch phase {
		case "started":
			if target != "" {
				return fmt.Sprintf(localizedText(cfg, "Reading %s...", "%s 읽는 중..."), target)
			}
			return localizedText(cfg, "Reading a file...", "파일 읽는 중...")
		case "completed":
			if target != "" && strings.TrimSpace(detail) != "" {
				return fmt.Sprintf(localizedText(cfg, "Read %s (%s).", "%s 읽음 (%s)."), target, detail)
			}
			if target != "" {
				return fmt.Sprintf(localizedText(cfg, "Read %s.", "%s 읽음."), target)
			}
			return localizedText(cfg, "Finished reading.", "읽기 완료.")
		case "failed":
			return formatHumanToolFailure(cfg, localizedText(cfg, "Could not read the file", "파일을 읽지 못함"), detail)
		}
	case base == "list_files" || base == "list_dir" || base == "glob" || base == "list":
		target := firstNonBlankString(path, ".")
		switch phase {
		case "started":
			return fmt.Sprintf(localizedText(cfg, "Browsing %s...", "%s 살펴보는 중..."), target)
		case "completed":
			if strings.TrimSpace(detail) != "" {
				return fmt.Sprintf(localizedText(cfg, "Listed %s (%s).", "%s 목록 확인 (%s)."), target, detail)
			}
			return fmt.Sprintf(localizedText(cfg, "Listed %s.", "%s 목록 확인."), target)
		case "failed":
			return formatHumanToolFailure(cfg, localizedText(cfg, "Could not list files", "목록을 가져오지 못함"), detail)
		}
	case base == "grep" || base == "search" || base == "rg":
		pat := firstNonBlankString(pattern, query)
		switch phase {
		case "started":
			if pat != "" {
				return fmt.Sprintf(localizedText(cfg, "Searching for %q...", "%q 검색 중..."), truncateStatusSnippet(pat, 48))
			}
			return localizedText(cfg, "Searching the codebase...", "코드 검색 중...")
		case "completed":
			if pat != "" && strings.TrimSpace(detail) != "" {
				return fmt.Sprintf(localizedText(cfg, "Search for %q found %s.", "%q 검색 결과: %s."), truncateStatusSnippet(pat, 40), detail)
			}
			if pat != "" {
				return fmt.Sprintf(localizedText(cfg, "Finished search for %q.", "%q 검색 완료."), truncateStatusSnippet(pat, 48))
			}
			return localizedText(cfg, "Search finished.", "검색 완료.")
		case "failed":
			return formatHumanToolFailure(cfg, localizedText(cfg, "Search failed", "검색 실패"), detail)
		}
	case base == "run_shell" || base == "bash" || base == "shell":
		cmd := firstNonBlankString(command, "")
		switch phase {
		case "started":
			if cmd != "" {
				return fmt.Sprintf(localizedText(cfg, "Running: %s", "실행 중: %s"), truncateStatusSnippet(cmd, 72))
			}
			return localizedText(cfg, "Running a command...", "명령 실행 중...")
		case "completed":
			if strings.TrimSpace(detail) != "" {
				return fmt.Sprintf(localizedText(cfg, "Command finished: %s", "명령 완료: %s"), truncateStatusSnippet(detail, 80))
			}
			return localizedText(cfg, "Command finished.", "명령 완료.")
		case "failed":
			return formatHumanToolFailure(cfg, localizedText(cfg, "Command failed", "명령 실패"), detail)
		}
	case strings.HasPrefix(base, "run_shell") || strings.Contains(base, "shell_job") || strings.Contains(base, "shell_bundle"):
		switch phase {
		case "started":
			return localizedText(cfg, "Working with background commands...", "백그라운드 명령 처리 중...")
		case "completed":
			return localizedText(cfg, "Background command update received.", "백그라운드 명령 상태 갱신.")
		case "failed":
			return formatHumanToolFailure(cfg, localizedText(cfg, "Background command failed", "백그라운드 명령 실패"), detail)
		}
	case base == "apply_patch" || base == "write_file" || base == "replace_in_file" || base == "search_replace" || base == "edit":
		target := firstNonBlankString(path, "")
		switch phase {
		case "started":
			if target != "" {
				return fmt.Sprintf(localizedText(cfg, "Editing %s...", "%s 수정 중..."), target)
			}
			return localizedText(cfg, "Applying an edit...", "수정 적용 중...")
		case "completed":
			if target != "" {
				return fmt.Sprintf(localizedText(cfg, "Updated %s.", "%s 수정 완료."), target)
			}
			return localizedText(cfg, "Edit applied.", "수정 적용 완료.")
		case "failed":
			return formatHumanToolFailure(cfg, localizedText(cfg, "Edit failed", "수정 실패"), detail)
		}
	case strings.HasPrefix(base, "git_"):
		action := strings.TrimPrefix(base, "git_")
		switch phase {
		case "started":
			return fmt.Sprintf(localizedText(cfg, "Running git %s...", "git %s 실행 중..."), action)
		case "completed":
			return fmt.Sprintf(localizedText(cfg, "git %s finished.", "git %s 완료."), action)
		case "failed":
			return formatHumanToolFailure(cfg, fmt.Sprintf(localizedText(cfg, "git %s failed", "git %s 실패"), action), detail)
		}
	case toolCallNameLooksLikeWebResearch(name):
		switch phase {
		case "started":
			if query != "" {
				return fmt.Sprintf(localizedText(cfg, "Searching the web: %s", "웹 검색: %s"), truncateStatusSnippet(query, 64))
			}
			return localizedText(cfg, "Searching the web...", "웹 검색 중...")
		case "completed":
			return localizedText(cfg, "Web research finished.", "웹 검색 완료.")
		case "failed":
			return formatHumanToolFailure(cfg, localizedText(cfg, "Web research failed", "웹 검색 실패"), detail)
		}
	}

	// Generic fallback: never dump opaque tool ids when we can say "working".
	label := humanizeToolDisplayName(name)
	switch phase {
	case "started":
		return fmt.Sprintf(localizedText(cfg, "Working: %s...", "작업 중: %s..."), label)
	case "completed":
		return fmt.Sprintf(localizedText(cfg, "Finished: %s.", "완료: %s."), label)
	case "failed":
		return formatHumanToolFailure(cfg, fmt.Sprintf(localizedText(cfg, "Failed: %s", "실패: %s"), label), detail)
	default:
		return label
	}
}

func formatHumanToolFailure(cfg Config, head string, detail string) string {
	head = strings.TrimSpace(head)
	detail = truncateStatusSnippet(strings.TrimSpace(detail), 96)
	if head == "" {
		return detail
	}
	if detail == "" {
		return head + "."
	}
	return head + ": " + detail
}

// humanizeToolDisplayName maps a raw tool id to a short plain label without
// leaking mcp__ prefixes.
func humanizeToolDisplayName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "task"
	}
	if strings.HasPrefix(name, "mcp__") {
		parts := strings.Split(name, "__")
		if len(parts) >= 3 {
			return humanizeEnumFallback(parts[len(parts)-1])
		}
	}
	return humanizeEnumFallback(name)
}

// parseToolProgressArgs accepts either a raw path/command snippet or a
// "path=... pattern=... command=..." preview produced by summarizeToolArgumentsPreview.
// Values may contain spaces (e.g. command=rg -n foo bar), so we do not split on
// whitespace — we scan for the next known " key=" boundary instead.
func parseToolProgressArgs(preview string) (path, pattern, command, query string) {
	preview = strings.TrimSpace(preview)
	if preview == "" {
		return "", "", "", ""
	}
	if strings.Contains(preview, "=") {
		lower := strings.ToLower(preview)
		keys := []string{"path", "file", "pattern", "query", "url", "command"}
		for _, key := range keys {
			prefix := key + "="
			idx := strings.Index(lower, prefix)
			if idx < 0 {
				continue
			}
			// Only accept key at start or after whitespace so "file=..." inside a
			// path does not steal the value.
			if idx > 0 && preview[idx-1] != ' ' && preview[idx-1] != '\t' {
				continue
			}
			rest := preview[idx+len(prefix):]
			restLower := strings.ToLower(rest)
			end := len(rest)
			for _, other := range keys {
				marker := " " + other + "="
				if p := strings.Index(restLower, marker); p >= 0 && p < end {
					end = p
				}
			}
			value := strings.TrimSpace(rest[:end])
			if value == "" {
				continue
			}
			switch key {
			case "path", "file":
				if path == "" {
					path = value
				}
			case "pattern":
				if pattern == "" {
					pattern = value
				}
			case "command":
				if command == "" {
					command = value
				}
			case "query", "url":
				if query == "" {
					query = value
				}
			}
		}
		if path != "" || pattern != "" || command != "" || query != "" {
			return path, pattern, command, query
		}
	}
	// Bare path-like preview
	if strings.ContainsAny(preview, `/\`) || strings.Contains(preview, ".") {
		return preview, "", "", ""
	}
	return "", "", preview, ""
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
	case strings.Contains(lower, "producing hidden reasoning") || strings.Contains(lower, "hidden reasoning"):
		return localizedText(cfg, "Model is thinking...", "모델이 추론하는 중입니다...")
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
