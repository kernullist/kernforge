package main

import (
	"context"
	"net/http"
	"sync"
	"testing"
)

func TestSelectModelRouteFallbackModels(t *testing.T) {
	tests := []struct {
		name      string
		fallbacks []string
		primary   string
		want      []string
	}{
		{
			name:      "empty config yields nil chain",
			fallbacks: nil,
			primary:   "claude-opus-4-8",
			want:      nil,
		},
		{
			name:      "ordered passthrough",
			fallbacks: []string{"claude-sonnet-4-6", "claude-haiku-4-5"},
			primary:   "claude-opus-4-8",
			want:      []string{"claude-sonnet-4-6", "claude-haiku-4-5"},
		},
		{
			name:      "drops blanks and the primary",
			fallbacks: []string{"  ", "claude-opus-4-8", "claude-sonnet-4-6"},
			primary:   "claude-opus-4-8",
			want:      []string{"claude-sonnet-4-6"},
		},
		{
			name:      "primary match is case-insensitive",
			fallbacks: []string{"CLAUDE-OPUS-4-8", "claude-sonnet-4-6"},
			primary:   "claude-opus-4-8",
			want:      []string{"claude-sonnet-4-6"},
		},
		{
			name:      "deduplicates repeated entries",
			fallbacks: []string{"claude-sonnet-4-6", "claude-sonnet-4-6", "claude-haiku-4-5"},
			primary:   "claude-opus-4-8",
			want:      []string{"claude-sonnet-4-6", "claude-haiku-4-5"},
		},
		{
			name:      "caps the chain at three models",
			fallbacks: []string{"m1", "m2", "m3", "m4", "m5"},
			primary:   "primary",
			want:      []string{"m1", "m2", "m3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectModelRouteFallbackModels(Config{FallbackModels: tt.fallbacks}, tt.primary)
			if len(got) != len(tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %#v, want %#v", got, tt.want)
				}
			}
		})
	}
}

func TestShouldTryFallbackModel(t *testing.T) {
	terminalErr := &ProviderAPIError{Provider: "anthropic", StatusCode: http.StatusBadRequest, Message: "invalid_prompt"}
	retryableErr := &ProviderAPIError{Provider: "anthropic", StatusCode: http.StatusTooManyRequests, Message: "rate limit"}

	tests := []struct {
		name string
		err  error
		resp ChatResponse
		want bool
	}{
		{name: "clean success does not fall back", err: nil, resp: ChatResponse{StopReason: "stop"}, want: false},
		{name: "refusal falls back", err: nil, resp: ChatResponse{StopReason: "refusal"}, want: true},
		{name: "terminal error falls back", err: terminalErr, want: true},
		{name: "retryable error does not fall back", err: retryableErr, want: false},
		{name: "context canceled does not fall back", err: context.Canceled, want: false},
		{name: "context deadline does not fall back", err: context.DeadlineExceeded, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldTryFallbackModel(tt.err, tt.resp); got != tt.want {
				t.Fatalf("shouldTryFallbackModel = %v, want %v", got, tt.want)
			}
		})
	}
}

// scriptedModelProviderClient returns a scripted (StopReason, error) outcome per
// requested model, recording the order in which models were called so a test can
// assert the fallback chain advanced exactly as configured.
type scriptedModelProviderClient struct {
	name     string
	outcomes map[string]ChatResponse
	errs     map[string]error
	mu       sync.Mutex
	calls    []string
}

func (c *scriptedModelProviderClient) Name() string {
	if c.name != "" {
		return c.name
	}
	return "anthropic"
}

func (c *scriptedModelProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	c.mu.Lock()
	c.calls = append(c.calls, req.Model)
	c.mu.Unlock()
	if err, ok := c.errs[req.Model]; ok && err != nil {
		return ChatResponse{}, err
	}
	if resp, ok := c.outcomes[req.Model]; ok {
		return resp, nil
	}
	return ChatResponse{Message: Message{Role: "assistant", Text: "ok"}, StopReason: "stop"}, nil
}

func (c *scriptedModelProviderClient) callOrder() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

func newFallbackTestConfig(t *testing.T, primary string, fallbacks []string) Config {
	t.Helper()
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "anthropic"
	cfg.Model = primary
	cfg.FallbackModels = fallbacks
	return cfg
}

func TestCompleteModelTurnFallsBackOnRefusal(t *testing.T) {
	client := &scriptedModelProviderClient{
		name: "anthropic",
		outcomes: map[string]ChatResponse{
			"claude-opus-4-8":   {StopReason: "refusal"},
			"claude-sonnet-4-6": {Message: Message{Role: "assistant", Text: "answer"}, StopReason: "stop"},
		},
	}
	cfg := newFallbackTestConfig(t, "claude-opus-4-8", []string{"claude-sonnet-4-6", "claude-haiku-4-5"})
	scheduler := NewModelRouteScheduler()
	policy := modelRoutePolicyFromConfig(cfg)
	req := ChatRequest{Model: cfg.Model, Messages: []Message{{Role: "user", Text: "do the thing"}}}

	resp, err := completeModelTurnOnceWithModelRoutes(context.Background(), scheduler, policy, cfg, client, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Text != "answer" || normalizeStopReason(resp.StopReason) != "stop" {
		t.Fatalf("expected the fallback model's successful answer, got %#v", resp)
	}
	// The refused primary is tried first, then the first fallback succeeds; the
	// chain must stop before reaching the second fallback.
	order := client.callOrder()
	want := []string{"claude-opus-4-8", "claude-sonnet-4-6"}
	if len(order) != len(want) {
		t.Fatalf("call order = %#v, want %#v", order, want)
	}
	for i := range order {
		if order[i] != want[i] {
			t.Fatalf("call order = %#v, want %#v", order, want)
		}
	}
}

func TestCompleteModelTurnFallsBackOnTerminalError(t *testing.T) {
	client := &scriptedModelProviderClient{
		name: "anthropic",
		errs: map[string]error{
			"claude-opus-4-8": &ProviderAPIError{Provider: "anthropic", StatusCode: http.StatusBadRequest, Message: "invalid_prompt"},
		},
		outcomes: map[string]ChatResponse{
			"claude-haiku-4-5": {Message: Message{Role: "assistant", Text: "rescued"}, StopReason: "stop"},
		},
	}
	cfg := newFallbackTestConfig(t, "claude-opus-4-8", []string{"claude-haiku-4-5"})
	scheduler := NewModelRouteScheduler()
	policy := modelRoutePolicyFromConfig(cfg)
	req := ChatRequest{Model: cfg.Model, Messages: []Message{{Role: "user", Text: "do the thing"}}}

	resp, err := completeModelTurnOnceWithModelRoutes(context.Background(), scheduler, policy, cfg, client, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Text != "rescued" {
		t.Fatalf("expected the fallback model to rescue the terminal error, got %#v", resp)
	}
	order := client.callOrder()
	want := []string{"claude-opus-4-8", "claude-haiku-4-5"}
	if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] {
		t.Fatalf("call order = %#v, want %#v", order, want)
	}
}

func TestCompleteModelTurnNoFallbackOnSuccess(t *testing.T) {
	client := &scriptedModelProviderClient{
		name: "anthropic",
		outcomes: map[string]ChatResponse{
			"claude-opus-4-8": {Message: Message{Role: "assistant", Text: "answer"}, StopReason: "stop"},
		},
	}
	cfg := newFallbackTestConfig(t, "claude-opus-4-8", []string{"claude-sonnet-4-6"})
	scheduler := NewModelRouteScheduler()
	policy := modelRoutePolicyFromConfig(cfg)
	req := ChatRequest{Model: cfg.Model, Messages: []Message{{Role: "user", Text: "do the thing"}}}

	resp, err := completeModelTurnOnceWithModelRoutes(context.Background(), scheduler, policy, cfg, client, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Text != "answer" {
		t.Fatalf("expected the primary answer, got %#v", resp)
	}
	// A clean success must never consume the fallback chain.
	if order := client.callOrder(); len(order) != 1 || order[0] != "claude-opus-4-8" {
		t.Fatalf("expected only the primary to be called, got %#v", order)
	}
}

func TestCompleteModelTurnExhaustsChainReturnsLastOutcome(t *testing.T) {
	// Every model refuses: the chain runs to exhaustion and the caller must see
	// the final fallback model's refusal, not the stale primary one.
	client := &scriptedModelProviderClient{
		name: "anthropic",
		outcomes: map[string]ChatResponse{
			"claude-opus-4-8":   {StopReason: "refusal", ServerModel: "claude-opus-4-8"},
			"claude-sonnet-4-6": {StopReason: "refusal", ServerModel: "claude-sonnet-4-6"},
		},
	}
	cfg := newFallbackTestConfig(t, "claude-opus-4-8", []string{"claude-sonnet-4-6"})
	scheduler := NewModelRouteScheduler()
	policy := modelRoutePolicyFromConfig(cfg)
	req := ChatRequest{Model: cfg.Model, Messages: []Message{{Role: "user", Text: "do the thing"}}}

	resp, err := completeModelTurnOnceWithModelRoutes(context.Background(), scheduler, policy, cfg, client, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !chatResponseIsRefusal(resp) {
		t.Fatalf("expected a refusal to be returned after the chain exhausted, got %#v", resp)
	}
	if resp.ServerModel != "claude-sonnet-4-6" {
		t.Fatalf("expected the last fallback model's outcome, got server model %q", resp.ServerModel)
	}
	order := client.callOrder()
	want := []string{"claude-opus-4-8", "claude-sonnet-4-6"}
	if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] {
		t.Fatalf("call order = %#v, want %#v", order, want)
	}
}
