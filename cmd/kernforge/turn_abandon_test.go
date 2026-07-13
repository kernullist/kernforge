package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockingCancelProvider struct {
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (b *blockingCancelProvider) Name() string { return "blocking-cancel" }

func (b *blockingCancelProvider) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = req
	b.mu.Lock()
	b.calls++
	call := b.calls
	b.mu.Unlock()

	if call == 1 {
		if b.started != nil {
			select {
			case <-b.started:
			default:
				close(b.started)
			}
		}
		select {
		case <-b.release:
			return ChatResponse{}, context.Canceled
		case <-ctx.Done():
			return ChatResponse{}, ctx.Err()
		}
	}
	return ChatResponse{
		Message:    Message{Role: "assistant", Text: "updated README"},
		StopReason: "stop",
	}, nil
}

func TestAbandonActiveTurnReleasesLockForNextReply(t *testing.T) {
	root := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	provider := &blockingCancelProvider{started: started, release: release}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false)},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := agent.Reply(context.Background(), "first long turn")
		firstDone <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first turn did not start")
	}

	agent.AbandonActiveTurn()

	reply, err := agent.Reply(context.Background(), "현재 구현을 반영한 README 문서를 최신화해서 작성해")
	if err != nil {
		t.Fatalf("second Reply after abandon: %v", err)
	}
	if strings.Contains(reply, "turn queue") || strings.Contains(reply, "kind=follow_up") {
		t.Fatalf("second Reply must run immediately, not queue: %q", reply)
	}
	if session.HasTurnQueue() {
		t.Fatalf("abandon should clear turn queue, got %#v", session.TurnQueue)
	}

	close(release)
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("abandoned first turn did not finish")
	}
}

func TestAbandonActiveTurnIsIdempotent(t *testing.T) {
	root := t.TempDir()
	agent := &Agent{
		Config:  Config{},
		Session: NewSession(root, "scripted", "model", "", "default"),
		Store:   NewSessionStore(root),
	}
	gen, ok := agent.beginTurn()
	if !ok || gen == 0 {
		t.Fatalf("beginTurn failed: ok=%v gen=%d", ok, gen)
	}
	agent.AbandonActiveTurn()
	agent.AbandonActiveTurn() // must not panic on second unlock attempt
	gen2, ok := agent.beginTurn()
	if !ok {
		t.Fatal("expected beginTurn to succeed after abandon")
	}
	agent.releaseTurn(gen2)
}

type hangForeverProvider struct {
	started chan struct{}
}

func (h *hangForeverProvider) Name() string { return "hang-forever" }

func (h *hangForeverProvider) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = req
	if h.started != nil {
		select {
		case <-h.started:
		default:
			close(h.started)
		}
	}
	// Ignore ctx on purpose: simulates a provider Complete that does not return
	// after cancel/KillAll, which used to leak the model-route scheduler slot.
	select {}
}

func TestAbandonActiveTurnReleasesModelRouteSlot(t *testing.T) {
	scheduler := NewModelRouteScheduler()
	started := make(chan struct{})
	provider := &hangForeverProvider{started: started}
	cfg := Config{
		Provider: "openrouter",
		Model:    "deepseek/deepseek-v4-flash",
		ModelRoutes: ModelRouteSchedulerConfig{
			Enabled:              boolPtr(true),
			DefaultMaxConcurrent: 1,
			ProviderLimits:       map[string]int{"openrouter": 1},
		},
	}
	agent := &Agent{
		Config:      cfg,
		Client:      provider,
		ModelRoutes: scheduler,
		Session:     NewSession(t.TempDir(), "openrouter", cfg.Model, "", "default"),
		Store:       NewSessionStore(t.TempDir()),
		Tools:       NewToolRegistry(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstDone := make(chan error, 1)
	go func() {
		_, err := completeModelTurnOnceWithModelRoutes(ctx, scheduler, modelRoutePolicyFromConfig(cfg), cfg, provider, ChatRequest{
			Model:    cfg.Model,
			Messages: []Message{{Role: "user", Text: "first"}},
		}, agent)
		firstDone <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first Complete did not start")
	}

	// Give Acquire a moment to mark the slot active.
	time.Sleep(50 * time.Millisecond)
	cancel()
	agent.AbandonActiveTurn()

	select {
	case err := <-firstDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first turn err=%v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled turn did not return after abandon")
	}

	route := modelRouteForRequest(cfg, provider, ChatRequest{Model: cfg.Model})
	acquireCtx, acquireCancel := context.WithTimeout(context.Background(), time.Second)
	defer acquireCancel()
	release, err := scheduler.Acquire(acquireCtx, route, 1)
	if err != nil {
		t.Fatalf("Acquire after abandon must not block on leaked slot: %v", err)
	}
	release()
}

func TestCancelReleasesModelRouteSlotWhenCompleteHangs(t *testing.T) {
	prev := modelRouteCancelSlotReleaseTimeout
	modelRouteCancelSlotReleaseTimeout = 50 * time.Millisecond
	defer func() { modelRouteCancelSlotReleaseTimeout = prev }()

	scheduler := NewModelRouteScheduler()
	started := make(chan struct{})
	provider := &hangForeverProvider{started: started}
	cfg := Config{
		Provider: "openrouter",
		Model:    "deepseek/deepseek-v4-flash",
		ModelRoutes: ModelRouteSchedulerConfig{
			Enabled:              boolPtr(true),
			DefaultMaxConcurrent: 1,
			ProviderLimits:       map[string]int{"openrouter": 1},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := completeModelTurnOnceWithModelRoutes(ctx, scheduler, modelRoutePolicyFromConfig(cfg), cfg, provider, ChatRequest{
			Model:    cfg.Model,
			Messages: []Message{{Role: "user", Text: "first"}},
		})
		firstDone <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first Complete did not start")
	}
	cancel()

	select {
	case err := <-firstDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first turn err=%v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled turn did not return after slot-release timeout")
	}

	route := modelRouteForRequest(cfg, provider, ChatRequest{Model: cfg.Model})
	acquireCtx, acquireCancel := context.WithTimeout(context.Background(), time.Second)
	defer acquireCancel()
	release, err := scheduler.Acquire(acquireCtx, route, 1)
	if err != nil {
		t.Fatalf("Acquire after cancel must not block on leaked slot: %v", err)
	}
	release()
}
