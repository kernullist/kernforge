package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type concurrentGuardProviderClient struct {
	name     string
	delay    time.Duration
	mu       sync.Mutex
	active   int
	maxSeen  int
	failures int
}

type contextIgnoringProviderClient struct {
	name        string
	started     chan struct{}
	release     chan struct{}
	mu          sync.Mutex
	active      int
	starts      int
	maxSeen     int
	releaseOnce sync.Once
}

func (c *contextIgnoringProviderClient) Name() string {
	if c.name != "" {
		return c.name
	}
	return "ollama"
}

func (c *contextIgnoringProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	c.mu.Lock()
	c.active++
	c.starts++
	if c.active > c.maxSeen {
		c.maxSeen = c.active
	}
	c.mu.Unlock()

	c.started <- struct{}{}
	<-c.release

	c.mu.Lock()
	c.active--
	c.mu.Unlock()
	return ChatResponse{Message: Message{Role: "assistant", Text: "ok"}}, nil
}

func (c *contextIgnoringProviderClient) closeRelease() {
	c.releaseOnce.Do(func() {
		close(c.release)
	})
}

func (c *contextIgnoringProviderClient) stats() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.starts, c.maxSeen
}

func (c *concurrentGuardProviderClient) Name() string {
	if c.name != "" {
		return c.name
	}
	return "ollama"
}

func (c *concurrentGuardProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	c.mu.Lock()
	c.active++
	if c.active > c.maxSeen {
		c.maxSeen = c.active
	}
	if c.active > 1 {
		c.failures++
	}
	c.mu.Unlock()

	select {
	case <-time.After(c.delay):
	case <-ctx.Done():
		c.mu.Lock()
		c.active--
		c.mu.Unlock()
		return ChatResponse{}, ctx.Err()
	}

	c.mu.Lock()
	c.active--
	c.mu.Unlock()
	return ChatResponse{Message: Message{Role: "assistant", Text: "ok"}}, nil
}

func (c *concurrentGuardProviderClient) stats() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxSeen, c.failures
}

func TestModelRouteSchedulerSerializesSameLocalProviderRoute(t *testing.T) {
	scheduler := NewModelRouteScheduler()
	client := &concurrentGuardProviderClient{name: "ollama", delay: 20 * time.Millisecond}
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "ollama"
	cfg.Model = "llama-test"
	cfg.MaxRequestRetries = 0
	cfg.RequestTimeoutSecs = 2

	agent := &Agent{
		Config:      cfg,
		Client:      client,
		ModelRoutes: scheduler,
	}

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := agent.completeModelTurn(context.Background(), ChatRequest{
				Model: cfg.Model,
				Messages: []Message{{
					Role: "user",
					Text: fmt.Sprintf("request %d", index),
				}},
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("completeModelTurn returned error: %v", err)
	}

	maxSeen, failures := client.stats()
	if failures != 0 || maxSeen > 1 {
		t.Fatalf("expected serialized provider calls, maxSeen=%d failures=%d", maxSeen, failures)
	}
	snapshots := scheduler.Snapshot()
	if len(snapshots) != 1 {
		t.Fatalf("expected one route snapshot, got %#v", snapshots)
	}
	if snapshots[0].TotalAcquires != 4 {
		t.Fatalf("expected four route acquisitions, got %#v", snapshots[0])
	}
}

func TestModelRoutePermitHeldUntilProviderReturnsAfterCallerTimeout(t *testing.T) {
	scheduler := NewModelRouteScheduler()
	client := &contextIgnoringProviderClient{
		name:    "ollama",
		started: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "ollama"
	cfg.Model = "llama-test"
	policy := modelRoutePolicyFromConfig(cfg)
	req := ChatRequest{Model: cfg.Model, Messages: []Message{{Role: "user", Text: "hold route"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := completeModelTurnOnceWithModelRoutes(ctx, scheduler, policy, cfg, client, req)
		errCh <- err
	}()

	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("first provider call did not start")
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("first request error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first request did not return after timeout")
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer waitCancel()
	_, err := completeModelTurnOnceWithModelRoutes(waitCtx, scheduler, policy, cfg, client, req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second request error = %v, want deadline exceeded while first provider call still owns route", err)
	}
	starts, maxSeen := client.stats()
	if starts != 1 || maxSeen != 1 {
		t.Fatalf("second request reached provider before first returned: starts=%d maxSeen=%d", starts, maxSeen)
	}
	client.closeRelease()
}

func TestModelRoutePolicyUsesLocalOpenAICompatibleLimit(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-compatible"
	cfg.Model = "local-model"
	cfg.BaseURL = "http://127.0.0.1:1234/v1/"
	policy := modelRoutePolicyFromConfig(cfg)
	route := modelRouteForRequest(cfg, NewOpenAICompatibleClient(cfg.Provider, cfg.BaseURL, "test-key"), ChatRequest{Model: cfg.Model})
	if got := policy.LimitFor(route); got != 1 {
		t.Fatalf("local openai-compatible route limit = %d, want 1", got)
	}
}

func TestModelRoutePolicyHonorsProviderOverride(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.ModelRoutes.ProviderLimits = map[string]int{"ollama": 3}
	policy := modelRoutePolicyFromConfig(cfg)
	route := ModelRoute{Provider: "ollama", Model: "llama-test"}
	if got := policy.LimitFor(route); got != 3 {
		t.Fatalf("provider override limit = %d, want 3", got)
	}
}

func TestModelRouteForRequestPreservesRequestReasoningEffort(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"
	cfg.ReasoningEffort = "medium"
	client := NewOpenAICodexClientWithReasoningEffort("", "low")
	route := modelRouteForRequest(cfg, client, ChatRequest{
		Model:           cfg.Model,
		ReasoningEffort: "high",
	})
	if route.ReasoningEffort != "high" {
		t.Fatalf("route reasoning effort = %q, want high", route.ReasoningEffort)
	}
}
