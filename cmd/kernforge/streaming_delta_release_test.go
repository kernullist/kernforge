package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

// M13: when a tool-enabled turn produces enough leading text and no tool call
// has appeared yet, readOpenAIStream must release the buffered text mid-stream
// through the delta callback and stream subsequent deltas live, instead of
// holding everything until the end.
func TestReadOpenAIStreamReleasesBufferedTextWhenNoToolCall(t *testing.T) {
	// Two paragraphs separated by a blank line trip shouldReleaseBufferedStreamText
	// (it returns true on a "\n\n" boundary).
	body := io.NopCloser(strings.NewReader(strings.Join([]string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"First paragraph of the answer.\\n\\n\"}}]}",
		"data: {\"choices\":[{\"delta\":{\"content\":\"Second paragraph continues live.\"}}]}",
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}",
		"data: [DONE]",
	}, "\n") + "\n"))

	var deltas []string
	// bufferLeadingText=true simulates a tool-enabled turn.
	resp, err := readOpenAIStream(context.Background(), "openai", body, func(s string) {
		deltas = append(deltas, s)
	}, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deltas) == 0 {
		t.Fatalf("expected mid-stream delta callbacks, got none (text buffered as one block)")
	}
	joined := strings.Join(deltas, "")
	if !strings.Contains(joined, "First paragraph") {
		t.Fatalf("buffered leading text was not flushed through delta callback: %q", joined)
	}
	if !strings.Contains(joined, "Second paragraph continues live.") {
		t.Fatalf("live subsequent delta was not streamed: %q", joined)
	}
	if !strings.Contains(resp.Message.Text, "Second paragraph continues live.") {
		t.Fatalf("final text missing content: %q", resp.Message.Text)
	}
}

// M13: once a tool call has started, partial leading text must NOT leak through
// the delta callback even though it stays in the final message accumulator only
// up to the tool-call boundary.
func TestReadOpenAIStreamDoesNotLeakTextOnceToolCallStarted(t *testing.T) {
	body := io.NopCloser(strings.NewReader(strings.Join([]string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"short pre-tool note\"}}]}",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"grep\",\"arguments\":\"{}\"}}]}}]}",
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}",
		"data: [DONE]",
	}, "\n") + "\n"))

	var deltas []string
	resp, err := readOpenAIStream(context.Background(), "openai", body, func(s string) {
		deltas = append(deltas, s)
	}, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deltas) != 0 {
		t.Fatalf("expected no leaked text deltas once a tool call started, got %v", deltas)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(resp.Message.ToolCalls))
	}
}

// M24: context-window derivation, output-token clamping, and compaction trigger.
func TestModelContextWindowDerivation(t *testing.T) {
	if got := modelContextWindow("openai", "gpt-5", 0); got != 200000 {
		t.Fatalf("gpt-5 window: want 200000, got %d", got)
	}
	if got := modelContextWindow("openrouter", "openai/gpt-4o-mini", 0); got != 128000 {
		t.Fatalf("gpt-4o route window: want 128000, got %d", got)
	}
	if got := modelContextWindow("anthropic", "claude-sonnet-4-5", 0); got != 200000 {
		t.Fatalf("claude window: want 200000, got %d", got)
	}
	// Explicit override wins.
	if got := modelContextWindow("openai", "gpt-5", 32000); got != 32000 {
		t.Fatalf("override window: want 32000, got %d", got)
	}
	// Unknown model + unknown provider => 0 (fallback to globals).
	if got := modelContextWindow("mystery", "totally-unknown-model", 0); got != 0 {
		t.Fatalf("unknown window: want 0, got %d", got)
	}
	// Unknown model but known provider => provider default.
	if got := modelContextWindow("openai", "unknown-x", 0); got != 128000 {
		t.Fatalf("provider default window: want 128000, got %d", got)
	}
}

func TestEffectiveMaxTokensClampsAndFallsBack(t *testing.T) {
	// Unknown window: configured returned unchanged.
	if got := effectiveMaxTokens(0, 8192, 1000); got != 8192 {
		t.Fatalf("unknown window should preserve configured: got %d", got)
	}
	// Plenty of room: configured kept.
	if got := effectiveMaxTokens(200000, 8192, 1000); got != 8192 {
		t.Fatalf("ample window should keep configured: got %d", got)
	}
	// Tight window: clamp below configured.
	// window 10000 - input 8000 - margin 2048 = -48 -> floor minDerivedMaxTokens.
	if got := effectiveMaxTokens(10000, 8192, 8000); got != minDerivedMaxTokens {
		t.Fatalf("tight window clamp: want %d, got %d", minDerivedMaxTokens, got)
	}
	// Moderate room less than configured.
	// window 12000 - input 4000 - margin 2048 = 5952.
	if got := effectiveMaxTokens(12000, 8192, 4000); got != 5952 {
		t.Fatalf("moderate clamp: want 5952, got %d", got)
	}
}

func TestCompactionTriggerChars(t *testing.T) {
	// Unknown window: fallback. bytesPerToken 0 selects the flat ASCII default,
	// preserving the historical behavior this test originally asserted.
	if got := compactionTriggerChars(0, 45000, 0); got != 45000 {
		t.Fatalf("unknown window should return fallback: got %d", got)
	}
	// 128000 tokens * 7/10 = 89600 tokens * 4 chars = 358400.
	if got := compactionTriggerChars(128000, 45000, 0); got != 358400 {
		t.Fatalf("derived trigger: want 358400, got %d", got)
	}
}

// M19: content-filter stop reasons classify distinctly from token-limit and
// generic empty stops.
func TestIsContentFilterStopReason(t *testing.T) {
	for _, r := range []string{"content_filter", "content_filtered", "output_content_filter", "content_filter_after_stream_retry"} {
		if !isContentFilterStopReason(r) {
			t.Fatalf("expected %q to classify as content filter", r)
		}
		if isTokenLimitStopReason(r) {
			t.Fatalf("content filter %q must not classify as token limit", r)
		}
	}
	for _, r := range []string{"stop", "length", "max_tokens", "", "tool_calls"} {
		if isContentFilterStopReason(r) {
			t.Fatalf("did not expect %q to classify as content filter", r)
		}
	}
}
