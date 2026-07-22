package main

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// turnPlanAnnounceTimeout bounds the optional pre-turn orientation call.
var turnPlanAnnounceTimeout = 8 * time.Second

const turnPlanAnnounceMaxTokens = 256
const turnPlanAnnounceMaxRunes = 160

// announceTurnPlan asks the model for a one-line "what I'll do" orientation
// before the main turn when the provider can answer a tiny tool-less call
// quickly. CLI/codex subprocess providers skip that preflight (it usually
// times out) and instead capture the first line of the main turn.
func (a *Agent) announceTurnPlan(ctx context.Context, userText string) {
	if a == nil {
		return
	}
	a.clearFirstLineTurnPlanCapture()
	if providerSkipsTurnPlanPreflight(a.Client) {
		a.armFirstLineTurnPlanCapture()
		return
	}
	text := strings.TrimSpace(a.generateTurnPlanAnnouncement(ctx, userText))
	if text == "" {
		a.armFirstLineTurnPlanCapture()
		return
	}
	a.emitTurnPlanLine(text)
}

func (a *Agent) emitTurnPlanLine(text string) {
	text = strings.TrimSpace(text)
	if a == nil || text == "" {
		return
	}
	if a.EmitTurnPlan != nil {
		a.EmitTurnPlan(text)
		return
	}
	if a.EmitProgressEvent != nil {
		a.EmitProgressEvent(ProgressEvent{
			Kind:    progressKindTurnPlan,
			Message: text,
		})
		return
	}
	if a.EmitProgress != nil {
		a.EmitProgress(text)
		return
	}
	if a.EmitAssistant != nil {
		a.EmitAssistant(text)
	}
}

func (a *Agent) generateTurnPlanAnnouncement(ctx context.Context, userText string) string {
	if a == nil || a.Client == nil {
		return ""
	}
	prompt := strings.TrimSpace(baseUserQueryText(userText))
	if prompt == "" {
		prompt = strings.TrimSpace(userText)
	}
	if prompt == "" {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	announceCtx, cancel := context.WithTimeout(ctx, turnPlanAnnounceTimeout)
	defer cancel()

	model := strings.TrimSpace(a.Config.Model)
	if a.Session != nil && strings.TrimSpace(a.Session.Model) != "" {
		model = strings.TrimSpace(a.Session.Model)
	}
	workingDir := ""
	if strings.TrimSpace(a.Workspace.Root) != "" {
		workingDir = a.Workspace.Root
	}

	var (
		streamMu sync.Mutex
		streamed strings.Builder
	)
	// Intentionally omit ReasoningEffort: thinking/adaptive models spend a tiny
	// max_tokens budget on reasoning and return empty Message.Text, which made
	// the announce silently disappear.
	resp, err := a.Client.Complete(announceCtx, ChatRequest{
		Model:          model,
		System:         turnPlanAnnounceSystemPrompt(a.Config),
		Messages:       []Message{{Role: "user", Text: prompt}},
		MaxTokens:      turnPlanAnnounceMaxTokens,
		Temperature:    0.2,
		TemperatureSet: true,
		ServiceTier:    a.Config.ServiceTier,
		WorkingDir:     workingDir,
		SessionID:      firstNonBlankString(a.SessionIDForRequest(), ""),
		OnTextDelta: func(delta string) {
			if delta == "" {
				return
			}
			streamMu.Lock()
			streamed.WriteString(delta)
			if line := sanitizeTurnPlanAnnouncement(streamed.String()); line != "" && strings.Contains(streamed.String(), "\n") {
				streamMu.Unlock()
				cancel()
				return
			}
			streamMu.Unlock()
		},
	})
	streamMu.Lock()
	streamText := streamed.String()
	streamMu.Unlock()

	text := sanitizeTurnPlanAnnouncement(resp.Message.Text)
	if text == "" {
		text = sanitizeTurnPlanAnnouncement(streamText)
	}
	if text != "" {
		return text
	}
	_ = err
	return ""
}

func providerSkipsTurnPlanPreflight(client ProviderClient) bool {
	if client == nil {
		return true
	}
	name := strings.ToLower(strings.TrimSpace(client.Name()))
	if name == "" {
		return true
	}
	// Subprocess CLIs take many seconds per Complete; a preflight call either
	// times out empty or doubles turn latency with no visible benefit.
	// Match "*cli*" only — do not treat API providers like "openai-codex" as CLI.
	return strings.Contains(name, "cli") || strings.Contains(name, "claude-code")
}

func (a *Agent) armFirstLineTurnPlanCapture() {
	if a == nil {
		return
	}
	a.turnPlanMu.Lock()
	a.firstLineTurnPlanCapture = true
	a.turnPlanMu.Unlock()
}

func (a *Agent) clearFirstLineTurnPlanCapture() {
	if a == nil {
		return
	}
	a.turnPlanMu.Lock()
	a.firstLineTurnPlanCapture = false
	a.turnPlanMu.Unlock()
}

func (a *Agent) firstLineTurnPlanCaptureArmed() bool {
	if a == nil {
		return false
	}
	a.turnPlanMu.Lock()
	defer a.turnPlanMu.Unlock()
	return a.firstLineTurnPlanCapture
}

func (a *Agent) firstLineTurnPlanInstruction() string {
	if !a.firstLineTurnPlanCaptureArmed() {
		return ""
	}
	return localizedText(a.Config,
		"Before any tool call, output exactly one short plain-text line saying what you will do this turn, then a newline, then continue the task. Do not wrap that first line in markdown.",
		"도구를 호출하기 전에, 이번 턴에 무엇을 할지 짧은 한 줄 평문으로 먼저 쓰고 줄바꿈한 뒤 작업을 계속하세요. 첫 줄은 마크다운으로 감싸지 마세요.")
}

func turnPlanAnnounceSystemPrompt(cfg Config) string {
	if localePrefersKorean(cfg) {
		return strings.Join([]string{
			"당신은 코딩 에이전트의 짧은 진행 안내자입니다.",
			"사용자 요청만 보고, 이번 턴에 무엇을 할지 한 문장으로 말하세요.",
			"규칙:",
			"- 한 줄만 출력하세요. 끝에 줄바꿈을 넣으세요.",
			"- 도구 호출, 파일 수정, 결과 요약, 추측한 결론을 쓰지 마세요.",
			"- 요청에 없는 동작(평가/수정/작성 등)을 덧붙이지 마세요.",
			"- 예: \"문서를 읽고 설명하겠습니다.\"",
		}, "\n")
	}
	return strings.Join([]string{
		"You write a one-line orientation for a coding agent turn.",
		"Given only the user's request, say what you will do this turn in one sentence.",
		"Rules:",
		"- Output exactly one line, then a newline.",
		"- Do not call tools, claim edits, summarize findings, or invent conclusions.",
		"- Do not add actions the user did not ask for (evaluate/fix/write/etc).",
		"- Example: \"I'll read the document and explain it.\"",
	}, "\n")
}

func sanitizeTurnPlanAnnouncement(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.TrimPrefix(trimmed, "```")
	if idx := strings.IndexByte(trimmed, '\n'); idx >= 0 {
		for _, line := range strings.Split(trimmed, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "```") {
				continue
			}
			trimmed = line
			break
		}
	}
	trimmed = strings.TrimSpace(trimmed)
	trimmed = strings.Trim(trimmed, "`\"'")
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return ""
	}
	if utf8.RuneCountInString(trimmed) > turnPlanAnnounceMaxRunes {
		runes := []rune(trimmed)
		trimmed = strings.TrimSpace(string(runes[:turnPlanAnnounceMaxRunes]))
	}
	return trimmed
}

// splitFirstCompleteLine returns the first line when buf contains a newline.
func splitFirstCompleteLine(buf string) (line string, rest string, ok bool) {
	idx := strings.IndexByte(buf, '\n')
	if idx < 0 {
		return "", buf, false
	}
	return buf[:idx], buf[idx+1:], true
}
