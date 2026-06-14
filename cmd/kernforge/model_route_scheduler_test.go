package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

type lateProgressProviderClient struct {
	name        string
	started     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

type streamingSlowProviderClient struct {
	name        string
	started     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

type reasoningCaptureProviderClient struct {
	name string
	meta ModelRouteMetadata
	req  ChatRequest
}

type nameOnlyReasoningCaptureProviderClient struct {
	name string
	req  ChatRequest
}

func (c *reasoningCaptureProviderClient) Name() string {
	if c.name != "" {
		return c.name
	}
	return "openai-codex"
}

func (c *reasoningCaptureProviderClient) ModelRouteMetadata() ModelRouteMetadata {
	return c.meta
}

func (c *reasoningCaptureProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	c.req = req
	return ChatResponse{Message: Message{Role: "assistant", Text: "ok"}}, nil
}

func (c *nameOnlyReasoningCaptureProviderClient) Name() string {
	if c.name != "" {
		return c.name
	}
	return "reviewer-provider"
}

func (c *nameOnlyReasoningCaptureProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	c.req = req
	return ChatResponse{Message: Message{Role: "assistant", Text: "ok"}}, nil
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

func (c *lateProgressProviderClient) Name() string {
	if c.name != "" {
		return c.name
	}
	return "ollama"
}

func (c *lateProgressProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	c.started <- struct{}{}
	<-c.release
	if req.OnProgressEvent != nil {
		req.OnProgressEvent(ProgressEvent{
			Kind:   progressKindModelStreamToolReady,
			Status: "late",
		})
	}
	return ChatResponse{Message: Message{Role: "assistant", Text: "ok"}}, nil
}

func (c *lateProgressProviderClient) closeRelease() {
	c.releaseOnce.Do(func() {
		close(c.release)
	})
}

func (c *streamingSlowProviderClient) Name() string {
	if c.name != "" {
		return c.name
	}
	return "ollama"
}

func (c *streamingSlowProviderClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	c.started <- struct{}{}
	if req.OnTextDelta != nil {
		req.OnTextDelta("partial assistant output")
	}
	<-c.release
	return ChatResponse{Message: Message{Role: "assistant", Text: "partial assistant output"}}, nil
}

func (c *streamingSlowProviderClient) closeRelease() {
	c.releaseOnce.Do(func() {
		close(c.release)
	})
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

// M6: a single local backend must serialize (limit=1) across reasoning efforts.
// Two routes that differ only by effort share one limiter, so the second acquire
// blocks until the first releases.
func TestModelRouteSerializesSingleLocalBackendAcrossEfforts(t *testing.T) {
	scheduler := NewModelRouteScheduler()
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-compatible"
	cfg.Model = "local-model"
	cfg.BaseURL = "http://127.0.0.1:1234/v1/"
	policy := modelRoutePolicyFromConfig(cfg)

	lowRoute := modelRouteForRequest(cfg, nil, ChatRequest{Model: cfg.Model, ReasoningEffort: "low"})
	highRoute := modelRouteForRequest(cfg, nil, ChatRequest{Model: cfg.Model, ReasoningEffort: "high"})

	if lowRoute.Key == highRoute.Key {
		t.Fatalf("identity keys should differ across efforts, both = %q", lowRoute.Key)
	}
	if lowRoute.schedulingKey() == "" || lowRoute.schedulingKey() != highRoute.schedulingKey() {
		t.Fatalf("scheduling keys must match: low=%q high=%q", lowRoute.schedulingKey(), highRoute.schedulingKey())
	}
	if got := policy.LimitFor(lowRoute); got != 1 {
		t.Fatalf("local backend limit = %d, want 1", got)
	}

	release, err := scheduler.Acquire(context.Background(), lowRoute, policy.LimitFor(lowRoute))
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = scheduler.Acquire(ctx, highRoute, policy.LimitFor(highRoute))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquire at a different effort should block on the shared backend, err = %v", err)
	}
	release()

	release2, err := scheduler.Acquire(context.Background(), highRoute, policy.LimitFor(highRoute))
	if err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	release2()

	snapshots := scheduler.Snapshot()
	if len(snapshots) != 1 {
		t.Fatalf("expected one shared route snapshot, got %#v", snapshots)
	}
}

// M18: LAN/RFC1918/container-host backends are treated as a single local
// backend (limit 1) rather than getting cloud concurrency.
func TestModelRoutePolicyTreatsPrivateAndContainerHostsAsLocal(t *testing.T) {
	for _, baseURL := range []string{
		"http://10.0.0.5:1234/v1",
		"http://172.16.4.4:1234/v1",
		"http://192.168.1.50:1234/v1",
		"http://host.docker.internal:1234/v1",
		"http://my-box.local:1234/v1",
	} {
		if !isLocalModelRouteBaseURL(baseURL) {
			t.Fatalf("expected %q to be classified local", baseURL)
		}
		cfg := DefaultConfig(t.TempDir())
		cfg.Provider = "openai-compatible"
		cfg.Model = "local-model"
		cfg.BaseURL = baseURL
		route := modelRouteForRequest(cfg, NewOpenAICompatibleClient(cfg.Provider, baseURL, "test-key"), ChatRequest{Model: cfg.Model})
		if got := modelRoutePolicyFromConfig(cfg).LimitFor(route); got != 1 {
			t.Fatalf("private/container host %q limit = %d, want 1", baseURL, got)
		}
	}

	// A public host must remain a cloud route.
	if isLocalModelRouteBaseURL("http://8.8.8.8:1234/v1") {
		t.Fatalf("public host should not be classified local")
	}
}

// M23: a dynamic per-route limit increase is applied while the route is busy and
// immediately admits a previously blocked acquire.
func TestModelRouteSchedulerDynamicLimitResize(t *testing.T) {
	scheduler := NewModelRouteScheduler()
	route := ModelRoute{Key: "resize-test", Label: "resize-test"}

	release1, err := scheduler.Acquire(context.Background(), route, 1)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	// With limit 1 the second acquire blocks.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if _, err := scheduler.Acquire(ctx, route, 1); !errors.Is(err, context.DeadlineExceeded) {
		cancel()
		t.Fatalf("second acquire at limit 1 should block, err = %v", err)
	}
	cancel()

	// Raise the limit to 2 while the first permit is still held; the resize must
	// apply immediately and admit the new acquire.
	release2, err := scheduler.Acquire(context.Background(), route, 2)
	if err != nil {
		t.Fatalf("acquire after resize to 2 failed: %v", err)
	}
	release1()
	release2()

	for _, snap := range scheduler.Snapshot() {
		if snap.Key == "resize-test" && snap.Limit != 2 {
			t.Fatalf("route limit after resize = %d, want 2", snap.Limit)
		}
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

func TestModelRouteProgressStopsAfterCallerContextCancel(t *testing.T) {
	scheduler := NewModelRouteScheduler()
	client := &lateProgressProviderClient{
		name:    "ollama",
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "ollama"
	cfg.Model = "llama-test"
	policy := modelRoutePolicyFromConfig(cfg)

	var mu sync.Mutex
	var events []ProgressEvent
	req := ChatRequest{
		Model: cfg.Model,
		Messages: []Message{{
			Role: "user",
			Text: "cancel",
		}},
		OnProgressEvent: func(event ProgressEvent) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, event)
		},
	}

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
		t.Fatal("provider call did not start")
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("request error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not return after timeout")
	}

	client.closeRelease()
	time.Sleep(30 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	for _, event := range events {
		if event.Kind == progressKindModelRequestDone || event.Kind == progressKindModelStreamToolReady {
			t.Fatalf("progress event leaked after context cancel: %#v in %#v", event, events)
		}
	}
}

func TestModelRouteWaitProgressStopsAfterStreamingOutputStarts(t *testing.T) {
	oldInitialDelay := modelRequestWaitInitialDelay
	oldRepeatDelay := modelRequestWaitRepeatDelay
	modelRequestWaitInitialDelay = 20 * time.Millisecond
	modelRequestWaitRepeatDelay = 20 * time.Millisecond
	t.Cleanup(func() {
		modelRequestWaitInitialDelay = oldInitialDelay
		modelRequestWaitRepeatDelay = oldRepeatDelay
	})

	scheduler := NewModelRouteScheduler()
	client := &streamingSlowProviderClient{
		name:    "ollama",
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "ollama"
	cfg.Model = "llama-test"
	policy := modelRoutePolicyFromConfig(cfg)

	var mu sync.Mutex
	var events []ProgressEvent
	var deltas []string
	req := ChatRequest{
		Model: cfg.Model,
		Messages: []Message{{
			Role: "user",
			Text: "stream then wait",
		}},
		OnTextDelta: func(text string) {
			mu.Lock()
			defer mu.Unlock()
			deltas = append(deltas, text)
		},
		OnProgressEvent: func(event ProgressEvent) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, event)
		},
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := completeModelTurnOnceWithModelRoutes(context.Background(), scheduler, policy, cfg, client, req)
		errCh <- err
	}()

	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("provider call did not start")
	}
	time.Sleep(60 * time.Millisecond)
	client.closeRelease()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("request error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not return after release")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(deltas) == 0 {
		t.Fatalf("expected streamed text delta")
	}
	for _, event := range events {
		if event.Kind == progressKindModelRequestWait {
			t.Fatalf("wait progress should be suppressed after streamed output starts: %#v", events)
		}
	}
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

func TestModelRoutePolicyUsesOpenRouterConservativeDefaultLimit(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openrouter"
	cfg.Model = "deepseek/deepseek-v4-pro"
	policy := modelRoutePolicyFromConfig(cfg)
	route := ModelRoute{Provider: "openrouter", Model: cfg.Model}
	if got := policy.LimitFor(route); got != 2 {
		t.Fatalf("openrouter default route limit = %d, want 2", got)
	}
}

func TestModelRoutePolicyUsesDeepSeekConservativeDefaultLimit(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-v4-pro"
	policy := modelRoutePolicyFromConfig(cfg)
	route := ModelRoute{Provider: "deepseek", Model: cfg.Model}
	if got := policy.LimitFor(route); got != 2 {
		t.Fatalf("deepseek default route limit = %d, want 2", got)
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

// M25: a reviewer client that carries an explicit reasoning effort must keep it
// even when it shares the physical backend with main. Previously the code raised
// the reviewer effort up to the main effort so the route identities would match
// for serialization; with M6 the scheduling key ignores effort, so serialization
// no longer depends on forcing matching effort and the explicit per-role effort
// is honored. (This test previously asserted the now-fixed wrong behavior of
// raising "high" to "xhigh".)
func TestModelRequestHonorsExplicitReviewerEffortWhenSharingBackend(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"
	cfg.ReasoningEffort = "xhigh"
	cfg.MaxRequestRetries = 0
	cfg.RequestTimeoutSecs = 2
	client := &reasoningCaptureProviderClient{
		name: "openai-codex",
		meta: ModelRouteMetadata{
			Provider:        "openai-codex",
			ReasoningEffort: "high",
		},
	}

	_, err := completeModelTurnOnceWithModelRoutes(context.Background(), NewModelRouteScheduler(), modelRoutePolicyFromConfig(cfg), cfg, client, ChatRequest{
		Model: cfg.Model,
		Messages: []Message{{
			Role: "user",
			Text: "review final answer",
		}},
	})
	if err != nil {
		t.Fatalf("completeModelTurnOnceWithModelRoutes: %v", err)
	}
	if client.req.ReasoningEffort != "high" {
		t.Fatalf("request reasoning effort = %q, want high (explicit reviewer effort honored)", client.req.ReasoningEffort)
	}

	// M6: even though efforts differ, the explicit-high reviewer and the
	// xhigh main route must share one scheduling key so a single backend
	// serializes across efforts.
	reviewerRoute := modelRouteForRequest(cfg, client, ChatRequest{Model: cfg.Model})
	mainRoute := modelRouteForRequest(cfg, nil, ChatRequest{Model: cfg.Model, ReasoningEffort: "xhigh"})
	if reviewerRoute.schedulingKey() == "" || reviewerRoute.schedulingKey() != mainRoute.schedulingKey() {
		t.Fatalf("scheduling keys must match across efforts: reviewer=%q main=%q", reviewerRoute.schedulingKey(), mainRoute.schedulingKey())
	}
	if reviewerRoute.Key == mainRoute.Key {
		t.Fatalf("identity keys should differ across efforts, both = %q", reviewerRoute.Key)
	}
}

func TestModelRequestKeepsDistinctReviewerClientEffort(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"
	cfg.ReasoningEffort = "xhigh"
	cfg.MaxRequestRetries = 0
	cfg.RequestTimeoutSecs = 2
	client := &reasoningCaptureProviderClient{
		name: "openai-codex",
		meta: ModelRouteMetadata{
			Provider:        "openai-codex",
			ReasoningEffort: "high",
		},
	}

	_, err := completeModelTurnOnceWithModelRoutes(context.Background(), NewModelRouteScheduler(), modelRoutePolicyFromConfig(cfg), cfg, client, ChatRequest{
		Model: "gpt-5.4",
		Messages: []Message{{
			Role: "user",
			Text: "review final answer",
		}},
	})
	if err != nil {
		t.Fatalf("completeModelTurnOnceWithModelRoutes: %v", err)
	}
	if client.req.ReasoningEffort != "high" {
		t.Fatalf("request reasoning effort = %q, want high", client.req.ReasoningEffort)
	}
}

func TestModelRequestUsesMainServiceTierWhenReviewerClientSharesRoute(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"
	cfg.ServiceTier = "flex"
	cfg.MaxRequestRetries = 0
	cfg.RequestTimeoutSecs = 2
	client := &reasoningCaptureProviderClient{
		name: "openai-codex",
		meta: ModelRouteMetadata{
			Provider:    "openai-codex",
			ServiceTier: "priority",
		},
	}

	_, err := completeModelTurnOnceWithModelRoutes(context.Background(), NewModelRouteScheduler(), modelRoutePolicyFromConfig(cfg), cfg, client, ChatRequest{
		Model: cfg.Model,
		Messages: []Message{{
			Role: "user",
			Text: "review final answer",
		}},
	})
	if err != nil {
		t.Fatalf("completeModelTurnOnceWithModelRoutes: %v", err)
	}
	if client.req.ServiceTier != "flex" {
		t.Fatalf("request service tier = %q, want flex", client.req.ServiceTier)
	}
}

func TestModelRequestKeepsDistinctReviewerClientServiceTier(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"
	cfg.ServiceTier = "flex"
	cfg.MaxRequestRetries = 0
	cfg.RequestTimeoutSecs = 2
	client := &reasoningCaptureProviderClient{
		name: "openai-codex",
		meta: ModelRouteMetadata{
			Provider:    "openai-codex",
			ServiceTier: "priority",
		},
	}

	_, err := completeModelTurnOnceWithModelRoutes(context.Background(), NewModelRouteScheduler(), modelRoutePolicyFromConfig(cfg), cfg, client, ChatRequest{
		Model: "gpt-5.4",
		Messages: []Message{{
			Role: "user",
			Text: "review final answer",
		}},
	})
	if err != nil {
		t.Fatalf("completeModelTurnOnceWithModelRoutes: %v", err)
	}
	if client.req.ServiceTier != "priority" {
		t.Fatalf("request service tier = %q, want priority", client.req.ServiceTier)
	}
}

func TestModelRouteSeparatesServiceTier(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"

	flexRoute := modelRouteForRequest(cfg, NewOpenAICodexClientWithReasoningEffortServiceTierAndWorkspaceIDs("", "", "flex", nil), ChatRequest{Model: cfg.Model})
	priorityRoute := modelRouteForRequest(cfg, NewOpenAICodexClientWithReasoningEffortServiceTierAndWorkspaceIDs("", "", "fast", nil), ChatRequest{Model: cfg.Model})

	if flexRoute.ServiceTier != "flex" {
		t.Fatalf("flex route service tier = %q, want flex", flexRoute.ServiceTier)
	}
	if priorityRoute.ServiceTier != "priority" {
		t.Fatalf("priority route service tier = %q, want priority", priorityRoute.ServiceTier)
	}
	if flexRoute.Key == priorityRoute.Key {
		t.Fatalf("routes with distinct service tiers should not share key: %q", flexRoute.Key)
	}
	if !strings.Contains(flexRoute.Label, "~flex") {
		t.Fatalf("route label should include service tier, got %q", flexRoute.Label)
	}
}

func TestModelRequestKeepsDistinctBaseURLReviewerClientEffort(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"
	cfg.BaseURL = "https://main.example.test/backend-api/codex"
	cfg.ReasoningEffort = "xhigh"
	cfg.MaxRequestRetries = 0
	cfg.RequestTimeoutSecs = 2
	client := &reasoningCaptureProviderClient{
		name: "openai-codex",
		meta: ModelRouteMetadata{
			Provider:        "openai-codex",
			BaseURL:         "https://reviewer.example.test/backend-api/codex",
			ReasoningEffort: "high",
		},
	}

	_, err := completeModelTurnOnceWithModelRoutes(context.Background(), NewModelRouteScheduler(), modelRoutePolicyFromConfig(cfg), cfg, client, ChatRequest{
		Model: cfg.Model,
		Messages: []Message{{
			Role: "user",
			Text: "review final answer",
		}},
	})
	if err != nil {
		t.Fatalf("completeModelTurnOnceWithModelRoutes: %v", err)
	}
	if client.req.ReasoningEffort != "high" {
		t.Fatalf("request reasoning effort = %q, want high", client.req.ReasoningEffort)
	}
}

func TestModelRequestDoesNotInheritMainEffortForMetadataLessDistinctClient(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"
	cfg.ReasoningEffort = "xhigh"
	cfg.MaxRequestRetries = 0
	cfg.RequestTimeoutSecs = 2
	client := &nameOnlyReasoningCaptureProviderClient{name: "anthropic-claude-cli"}

	_, err := completeModelTurnOnceWithModelRoutes(context.Background(), NewModelRouteScheduler(), modelRoutePolicyFromConfig(cfg), cfg, client, ChatRequest{
		Model: cfg.Model,
		Messages: []Message{{
			Role: "user",
			Text: "review final answer",
		}},
	})
	if err != nil {
		t.Fatalf("completeModelTurnOnceWithModelRoutes: %v", err)
	}
	if client.req.ReasoningEffort != "" {
		t.Fatalf("metadata-less distinct client inherited reasoning effort %q", client.req.ReasoningEffort)
	}
	route := modelRouteForRequest(cfg, client, ChatRequest{Model: cfg.Model})
	if route.Provider != "anthropic-claude-cli" {
		t.Fatalf("route provider = %q, want anthropic-claude-cli", route.Provider)
	}
	if route.ReasoningEffort != "" {
		t.Fatalf("distinct metadata-less route effort = %q, want empty", route.ReasoningEffort)
	}
}

func TestModelRouteUsesConfiguredBaseURLForMetadataLessMainRoute(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-compatible"
	cfg.Model = "local-model"
	cfg.BaseURL = "http://127.0.0.1:1234/v1/"
	cfg.ReasoningEffort = "high"
	client := &nameOnlyReasoningCaptureProviderClient{name: "openai-compatible"}

	route := modelRouteForRequest(cfg, client, ChatRequest{Model: cfg.Model})
	if route.BaseURL != "http://127.0.0.1:1234/v1" {
		t.Fatalf("route base URL = %q", route.BaseURL)
	}
	if route.ReasoningEffort != "high" {
		t.Fatalf("main route effort = %q, want high", route.ReasoningEffort)
	}
	if !strings.Contains(route.Label, "http://127.0.0.1:1234/v1") {
		t.Fatalf("route label should include base URL, got %q", route.Label)
	}
	if limit := modelRoutePolicyFromConfig(cfg).LimitFor(route); limit != 1 {
		t.Fatalf("local openai-compatible route limit = %d, want 1", limit)
	}
}

func TestModelRequestUsesMainEffortForMetadataLessMainRouteWithBaseURL(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-compatible"
	cfg.Model = "local-model"
	cfg.BaseURL = "http://127.0.0.1:1234/v1/"
	cfg.ReasoningEffort = "high"
	cfg.MaxRequestRetries = 0
	cfg.RequestTimeoutSecs = 2
	client := &nameOnlyReasoningCaptureProviderClient{name: "openai-compatible"}

	_, err := completeModelTurnOnceWithModelRoutes(context.Background(), NewModelRouteScheduler(), modelRoutePolicyFromConfig(cfg), cfg, client, ChatRequest{
		Model: cfg.Model,
		Messages: []Message{{
			Role: "user",
			Text: "review final answer",
		}},
	})
	if err != nil {
		t.Fatalf("completeModelTurnOnceWithModelRoutes: %v", err)
	}
	if client.req.ReasoningEffort != "high" {
		t.Fatalf("request reasoning effort = %q, want high", client.req.ReasoningEffort)
	}
}

func TestModelRouteDoesNotClaimMainEffortForNilClientDifferentModel(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"
	cfg.ReasoningEffort = "xhigh"
	req := ChatRequest{Model: "gpt-5.4"}

	adjusted := requestWithRouteReasoningEffort(cfg, nil, req)
	if adjusted.ReasoningEffort != "" {
		t.Fatalf("request reasoning effort = %q, want empty", adjusted.ReasoningEffort)
	}
	route := modelRouteForRequest(cfg, nil, req)
	if route.ReasoningEffort != "" {
		t.Fatalf("route reasoning effort = %q, want empty", route.ReasoningEffort)
	}
}
