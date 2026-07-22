package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type turnPlanAnnounceStubClient struct {
	text    string
	err     error
	delay   time.Duration
	lastReq ChatRequest
	name    string
}

func (c *turnPlanAnnounceStubClient) Name() string {
	if strings.TrimSpace(c.name) != "" {
		return c.name
	}
	return "turn-plan-stub"
}

func (c *turnPlanAnnounceStubClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	c.lastReq = req
	if c.delay > 0 {
		timer := time.NewTimer(c.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ChatResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
	if c.err != nil {
		return ChatResponse{}, c.err
	}
	if req.OnTextDelta != nil && strings.TrimSpace(c.text) != "" {
		req.OnTextDelta(c.text)
	}
	return ChatResponse{Message: Message{Role: "assistant", Text: c.text}}, nil
}

type turnPlanAnnounceStreamingStubClient struct {
	chunks []string
	delay  time.Duration
}

func (c *turnPlanAnnounceStreamingStubClient) Name() string { return "turn-plan-stream-stub" }

func (c *turnPlanAnnounceStreamingStubClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	var full strings.Builder
	for _, chunk := range c.chunks {
		if c.delay > 0 {
			timer := time.NewTimer(c.delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ChatResponse{Message: Message{Role: "assistant", Text: full.String()}}, ctx.Err()
			case <-timer.C:
				timer.Stop()
			}
		}
		if ctx.Err() != nil {
			return ChatResponse{Message: Message{Role: "assistant", Text: full.String()}}, ctx.Err()
		}
		full.WriteString(chunk)
		if req.OnTextDelta != nil {
			req.OnTextDelta(chunk)
		}
	}
	return ChatResponse{Message: Message{Role: "assistant", Text: full.String()}}, nil
}

func TestSanitizeTurnPlanAnnouncement(t *testing.T) {
	got := sanitizeTurnPlanAnnouncement("  \"문서를 읽고 설명하겠습니다.\" \nextra")
	if got != "문서를 읽고 설명하겠습니다." {
		t.Fatalf("sanitize = %q", got)
	}
	got = sanitizeTurnPlanAnnouncement("```\nI'll read the document and explain it.\n```")
	if got != "I'll read the document and explain it." {
		t.Fatalf("fenced sanitize = %q", got)
	}
	if sanitizeTurnPlanAnnouncement("   ") != "" {
		t.Fatalf("expected empty sanitize for blank input")
	}
}

func TestAnnounceTurnPlanEmitsModelLine(t *testing.T) {
	var got string
	client := &turnPlanAnnounceStubClient{
		text: "문서를 읽고 평가하겠습니다.\n",
	}
	agent := &Agent{
		Config: Config{AutoLocale: boolPtr(true), Model: "test-model"},
		Client: client,
		EmitTurnPlan: func(text string) {
			got = text
		},
	}
	agent.announceTurnPlan(context.Background(), "@Callstack_Spoofing_Techniques_Overview.md 문서를 읽고 기술적 깊이가 충분한지 평가해줘")
	if got != "문서를 읽고 평가하겠습니다." {
		t.Fatalf("expected model announce text, got %q", got)
	}
	if strings.TrimSpace(client.lastReq.ReasoningEffort) != "" {
		t.Fatalf("announce must not enable reasoning effort, got %q", client.lastReq.ReasoningEffort)
	}
	if agent.firstLineTurnPlanCaptureArmed() {
		t.Fatalf("successful preflight must not arm first-line capture")
	}
}

func TestAnnounceTurnPlanSkipsCLIPreflightAndArmsCapture(t *testing.T) {
	var emitted bool
	agent := &Agent{
		Config: Config{Model: "test-model"},
		Client: &turnPlanAnnounceStubClient{name: "anthropic-claude-cli", text: "should not call"},
		EmitTurnPlan: func(text string) {
			emitted = true
		},
	}
	agent.announceTurnPlan(context.Background(), "문서를 읽고 설명해줘")
	if emitted {
		t.Fatalf("CLI preflight must be skipped")
	}
	if !agent.firstLineTurnPlanCaptureArmed() {
		t.Fatalf("CLI path must arm first-line capture")
	}
	if instr := agent.firstLineTurnPlanInstruction(); !strings.Contains(instr, "한 줄") && !strings.Contains(strings.ToLower(instr), "one short") {
		t.Fatalf("expected first-line instruction, got %q", instr)
	}
}

func TestAnnounceTurnPlanSkipsOnTimeout(t *testing.T) {
	prev := turnPlanAnnounceTimeout
	turnPlanAnnounceTimeout = 200 * time.Millisecond
	defer func() { turnPlanAnnounceTimeout = prev }()

	var emitted bool
	client := &turnPlanAnnounceStubClient{
		text:  "should not emit",
		delay: 3 * time.Second,
	}
	agent := &Agent{
		Config: Config{Model: "test-model"},
		Client: client,
		EmitTurnPlan: func(text string) {
			emitted = true
		},
	}
	agent.announceTurnPlan(context.Background(), "문서를 읽고 설명해줘")
	if emitted {
		t.Fatalf("timeout must skip announce emit")
	}
	if !agent.firstLineTurnPlanCaptureArmed() {
		t.Fatalf("timeout must arm first-line capture fallback")
	}
}

func TestAnnounceTurnPlanKeepsStreamedLineAfterCancel(t *testing.T) {
	prev := turnPlanAnnounceTimeout
	turnPlanAnnounceTimeout = 500 * time.Millisecond
	defer func() { turnPlanAnnounceTimeout = prev }()

	var got string
	client := &turnPlanAnnounceStreamingStubClient{
		chunks: []string{"문서를 읽고 설명하겠습니다.", "\n", "extra ignored"},
		delay:  50 * time.Millisecond,
	}
	agent := &Agent{
		Config: Config{Model: "test-model", AutoLocale: boolPtr(true)},
		Client: client,
		EmitTurnPlan: func(text string) {
			got = text
		},
	}
	agent.announceTurnPlan(context.Background(), "문서를 읽고 설명해줘")
	if got != "문서를 읽고 설명하겠습니다." {
		t.Fatalf("expected streamed announce line, got %q", got)
	}
}

func TestAnnounceTurnPlanSkipsOnErrorOrEmpty(t *testing.T) {
	for _, tc := range []struct {
		name   string
		client *turnPlanAnnounceStubClient
	}{
		{name: "error", client: &turnPlanAnnounceStubClient{err: errors.New("boom")}},
		{name: "empty", client: &turnPlanAnnounceStubClient{text: "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var emitted bool
			agent := &Agent{
				Config: Config{Model: "test-model"},
				Client: tc.client,
				EmitTurnPlan: func(text string) {
					emitted = true
				},
			}
			agent.announceTurnPlan(context.Background(), "설명해줘")
			if emitted {
				t.Fatalf("expected no announce emit")
			}
			if !agent.firstLineTurnPlanCaptureArmed() {
				t.Fatalf("expected first-line capture fallback")
			}
		})
	}
}

func TestPromoteTurnPlanFromAssistantStream(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{color: false},
		cfg:    Config{ProgressDisplay: "quiet", AutoLocale: boolPtr(true)},
		agent:  &Agent{},
	}
	rt.agent.armFirstLineTurnPlanCapture()
	rt.appendAssistantStream("문서를 읽고 평가하겠습니다.\n")
	rt.appendAssistantStream("본문 평가 결과입니다.")
	rendered := out.String()
	if !strings.Contains(rendered, "[thought") || !strings.Contains(rendered, "문서를 읽고 평가하겠습니다.") {
		t.Fatalf("expected promoted turn plan line, got %q", rendered)
	}
	if !strings.Contains(rendered, ">> assistant") || !strings.Contains(rendered, "본문 평가 결과입니다.") {
		t.Fatalf("expected remaining answer in assistant stream, got %q", rendered)
	}
	if rt.agent.firstLineTurnPlanCaptureArmed() {
		t.Fatalf("capture should be cleared after promotion")
	}
}

func TestPrintAssistantDoesNotReplayPromotedTurnPlan(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{color: false},
		cfg:    Config{ProgressDisplay: "quiet", AutoLocale: boolPtr(true)},
		agent:  &Agent{},
	}
	rt.resetAssistantDedup()
	rt.agent.armFirstLineTurnPlanCapture()
	planAndBody := "문서를 읽고 평가하겠습니다.\n\n짧은 본문입니다."
	rt.appendAssistantStream(planAndBody)
	rt.finishAssistantStream()
	rt.printAssistant(planAndBody)

	rendered := out.String()
	if strings.Count(rendered, "문서를 읽고 평가하겠습니다.") != 1 {
		t.Fatalf("expected orientation line once after stream+printAssistant, got:\n%s", rendered)
	}
	if strings.Count(rendered, ">> assistant") != 1 {
		t.Fatalf("expected a single assistant block, got:\n%s", rendered)
	}
	if strings.Count(rendered, "짧은 본문입니다.") != 1 {
		t.Fatalf("expected body once, got:\n%s", rendered)
	}
}

func TestProviderSkipsTurnPlanPreflightOnlyForCLI(t *testing.T) {
	if !providerSkipsTurnPlanPreflight(&turnPlanAnnounceStubClient{name: "anthropic-claude-cli"}) {
		t.Fatalf("claude cli must skip preflight")
	}
	if !providerSkipsTurnPlanPreflight(&turnPlanAnnounceStubClient{name: "codex-cli"}) {
		t.Fatalf("codex cli must skip preflight")
	}
	if providerSkipsTurnPlanPreflight(&turnPlanAnnounceStubClient{name: "openai-codex"}) {
		t.Fatalf("openai-codex API must not be treated as CLI preflight skip")
	}
	if providerSkipsTurnPlanPreflight(&turnPlanAnnounceStubClient{name: "openai"}) {
		t.Fatalf("openai must use preflight")
	}
}

func TestDocumentReadEvalIsNotDocumentAuthoring(t *testing.T) {
	request := "@Callstack_Spoofing_Techniques_Overview.md 문서를 읽고 기술적 깊이가 충분한지 평가해줘"
	if looksLikeDocumentAuthoringIntent(request) {
		t.Fatalf("read/eval must not look like document authoring")
	}
	if looksLikeDocumentArtifactOutputRequest(request) {
		t.Fatalf("read/eval must not look like document output sink")
	}
	if !looksLikeDocumentReadOrEvalRequest(request) {
		t.Fatalf("expected read/eval detector")
	}
}

func TestTurnPlanProgressPersistsInQuietMode(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer:      &out,
		ui:          UI{},
		interactive: true,
		cfg:         Config{ProgressDisplay: "quiet", AutoLocale: boolPtr(true)},
	}
	rt.footerVisible = true
	rt.footerLineCount = 1
	rt.footerText = "[thinking] [-] 생각 중..."
	rt.printProgressEvent(ProgressEvent{
		Kind:    progressKindTurnPlan,
		Message: "문서를 읽고 설명하겠습니다.",
	})
	rendered := out.String()
	if !strings.Contains(rendered, "문서를 읽고 설명하겠습니다.") {
		t.Fatalf("expected durable turn plan in quiet mode, got %q", rendered)
	}
	if status := rt.currentThinkingStatus(0); strings.Contains(status, "문서를 읽고 설명하겠습니다.") {
		t.Fatalf("turn plan must not live in thinking spinner status, got %q", status)
	}
}

func TestPrintPersistentTurnPlanLineIsNotSpinnerStatus(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer:      &out,
		ui:          UI{color: false},
		interactive: true,
		cfg:         Config{ProgressDisplay: "quiet"},
	}
	rt.startThinkingIndicator()
	defer rt.stopThinkingIndicator()
	time.Sleep(20 * time.Millisecond)
	rt.printPersistentTurnPlanLine("문서를 읽고 설명하겠습니다.")
	rendered := out.String()
	if !strings.Contains(rendered, "[thought") || !strings.Contains(rendered, "문서를 읽고 설명하겠습니다.") {
		t.Fatalf("expected separate thought line, got %q", rendered)
	}
}
