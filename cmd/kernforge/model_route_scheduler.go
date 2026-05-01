package main

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type ModelRouteSchedulerConfig struct {
	Enabled              *bool          `json:"enabled,omitempty"`
	DefaultMaxConcurrent int            `json:"default_max_concurrent,omitempty"`
	ProviderLimits       map[string]int `json:"provider_limits,omitempty"`
	RouteLimits          map[string]int `json:"route_limits,omitempty"`
}

type ModelRouteMetadata struct {
	Provider        string
	Model           string
	BaseURL         string
	ReasoningEffort string
}

type modelRouteMetadataProvider interface {
	ModelRouteMetadata() ModelRouteMetadata
}

type ModelRoute struct {
	Key             string
	Label           string
	Provider        string
	Model           string
	BaseURL         string
	ReasoningEffort string
}

type ModelRoutePolicy struct {
	Enabled              bool
	DefaultMaxConcurrent int
	ProviderLimits       map[string]int
	RouteLimits          map[string]int
	configured           bool
}

type ModelRouteScheduler struct {
	mu     sync.Mutex
	routes map[string]*modelRouteLimiter
}

type modelRouteLimiter struct {
	key           string
	label         string
	limit         int
	sem           chan struct{}
	mu            sync.Mutex
	active        int
	queued        int
	totalAcquires int64
	lastWait      time.Duration
	maxWait       time.Duration
	lastAcquired  time.Time
}

type ModelRouteSnapshot struct {
	Key           string
	Label         string
	Limit         int
	Active        int
	Queued        int
	TotalAcquires int64
	LastWait      time.Duration
	MaxWait       time.Duration
	LastAcquired  time.Time
}

var globalModelRouteScheduler = NewModelRouteScheduler()

func NewModelRouteScheduler() *ModelRouteScheduler {
	return &ModelRouteScheduler{
		routes: map[string]*modelRouteLimiter{},
	}
}

func defaultModelRouteScheduler() *ModelRouteScheduler {
	return globalModelRouteScheduler
}

func (s *ModelRouteScheduler) Acquire(ctx context.Context, route ModelRoute, limit int) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || strings.TrimSpace(route.Key) == "" || limit <= 0 {
		return func() {}, nil
	}
	limiter := s.limiter(route, limit)
	start := time.Now()
	limiter.mu.Lock()
	limiter.queued++
	limiter.mu.Unlock()

	select {
	case limiter.sem <- struct{}{}:
		wait := time.Since(start)
		limiter.mu.Lock()
		limiter.queued--
		limiter.active++
		limiter.totalAcquires++
		limiter.lastWait = wait
		if wait > limiter.maxWait {
			limiter.maxWait = wait
		}
		limiter.lastAcquired = time.Now()
		limiter.mu.Unlock()
		released := false
		var releaseMu sync.Mutex
		return func() {
			releaseMu.Lock()
			defer releaseMu.Unlock()
			if released {
				return
			}
			released = true
			<-limiter.sem
			limiter.mu.Lock()
			if limiter.active > 0 {
				limiter.active--
			}
			limiter.mu.Unlock()
		}, nil
	case <-ctx.Done():
		limiter.mu.Lock()
		if limiter.queued > 0 {
			limiter.queued--
		}
		limiter.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (s *ModelRouteScheduler) limiter(route ModelRoute, limit int) *modelRouteLimiter {
	if limit < 1 {
		limit = 1
	}
	key := strings.TrimSpace(route.Key)
	label := strings.TrimSpace(route.Label)
	if label == "" {
		label = key
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.routes == nil {
		s.routes = map[string]*modelRouteLimiter{}
	}
	if existing, ok := s.routes[key]; ok {
		if existing.limit == limit {
			return existing
		}
		existing.mu.Lock()
		idle := existing.active == 0 && existing.queued == 0 && len(existing.sem) == 0
		existing.mu.Unlock()
		if !idle {
			return existing
		}
	}
	limiter := &modelRouteLimiter{
		key:   key,
		label: label,
		limit: limit,
		sem:   make(chan struct{}, limit),
	}
	s.routes[key] = limiter
	return limiter
}

func (s *ModelRouteScheduler) Snapshot() []ModelRouteSnapshot {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	routes := make([]*modelRouteLimiter, 0, len(s.routes))
	for _, limiter := range s.routes {
		routes = append(routes, limiter)
	}
	s.mu.Unlock()

	out := make([]ModelRouteSnapshot, 0, len(routes))
	for _, limiter := range routes {
		limiter.mu.Lock()
		item := ModelRouteSnapshot{
			Key:           limiter.key,
			Label:         limiter.label,
			Limit:         limiter.limit,
			Active:        limiter.active,
			Queued:        limiter.queued,
			TotalAcquires: limiter.totalAcquires,
			LastWait:      limiter.lastWait,
			MaxWait:       limiter.maxWait,
			LastAcquired:  limiter.lastAcquired,
		}
		limiter.mu.Unlock()
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Label < out[j].Label
	})
	return out
}

func modelRoutePolicyFromConfig(cfg Config) ModelRoutePolicy {
	providerLimits := defaultModelRouteProviderLimits()
	for provider, limit := range cfg.ModelRoutes.ProviderLimits {
		provider = normalizeProviderName(provider)
		if provider == "" || limit <= 0 {
			continue
		}
		providerLimits[provider] = limit
	}
	routeLimits := make(map[string]int, len(cfg.ModelRoutes.RouteLimits))
	for route, limit := range cfg.ModelRoutes.RouteLimits {
		route = strings.TrimSpace(route)
		if route == "" || limit <= 0 {
			continue
		}
		routeLimits[route] = limit
	}
	return ModelRoutePolicy{
		Enabled:              configModelRoutesEnabled(cfg),
		DefaultMaxConcurrent: configModelRoutesDefaultMaxConcurrent(cfg),
		ProviderLimits:       providerLimits,
		RouteLimits:          routeLimits,
		configured:           true,
	}
}

func defaultModelRouteProviderLimits() map[string]int {
	return map[string]int{
		"codex-cli":    1,
		"llama.cpp":    1,
		"lmstudio":     1,
		"ollama":       1,
		"opencode":     1,
		"opencode-go":  1,
		"openai-codex": 2,
		"vllm":         1,
	}
}

func configModelRoutesEnabled(cfg Config) bool {
	if cfg.ModelRoutes.Enabled == nil {
		return true
	}
	return *cfg.ModelRoutes.Enabled
}

func configModelRoutesDefaultMaxConcurrent(cfg Config) int {
	if cfg.ModelRoutes.DefaultMaxConcurrent > 0 {
		return cfg.ModelRoutes.DefaultMaxConcurrent
	}
	return 4
}

func (p ModelRoutePolicy) normalized() ModelRoutePolicy {
	if !p.configured {
		return modelRoutePolicyFromConfig(Config{})
	}
	if p.DefaultMaxConcurrent <= 0 {
		p.DefaultMaxConcurrent = 4
	}
	if p.ProviderLimits == nil {
		p.ProviderLimits = defaultModelRouteProviderLimits()
	}
	if p.RouteLimits == nil {
		p.RouteLimits = map[string]int{}
	}
	return p
}

func (p ModelRoutePolicy) LimitFor(route ModelRoute) int {
	p = p.normalized()
	if !p.Enabled {
		return 0
	}
	if limit := p.RouteLimits[strings.TrimSpace(route.Key)]; limit > 0 {
		return limit
	}
	if limit := p.RouteLimits[strings.TrimSpace(route.Label)]; limit > 0 {
		return limit
	}
	if route.Provider != "" && route.Model != "" {
		if limit := p.RouteLimits[route.Provider+"/"+route.Model]; limit > 0 {
			return limit
		}
	}
	provider := normalizeProviderName(route.Provider)
	if isLocalModelRouteBaseURL(route.BaseURL) {
		switch provider {
		case "openai", "openai-compatible":
			return 1
		}
	}
	if limit := p.ProviderLimits[provider]; limit > 0 {
		return limit
	}
	if p.DefaultMaxConcurrent > 0 {
		return p.DefaultMaxConcurrent
	}
	return 4
}

func modelRouteForRequest(cfg Config, client ProviderClient, req ChatRequest) ModelRoute {
	provider := strings.TrimSpace(cfg.Provider)
	model := firstNonBlankString(req.Model, cfg.Model)
	baseURL := strings.TrimSpace(cfg.BaseURL)
	reasoningEffort := firstNonBlankString(req.ReasoningEffort, cfg.ReasoningEffort)
	if client != nil {
		if metaProvider, ok := client.(modelRouteMetadataProvider); ok {
			meta := metaProvider.ModelRouteMetadata()
			if strings.TrimSpace(meta.Provider) != "" {
				provider = strings.TrimSpace(meta.Provider)
			}
			if strings.TrimSpace(meta.Model) != "" && strings.TrimSpace(req.Model) == "" && strings.TrimSpace(cfg.Model) == "" {
				model = strings.TrimSpace(meta.Model)
			}
			if strings.TrimSpace(meta.BaseURL) != "" {
				baseURL = strings.TrimSpace(meta.BaseURL)
			}
			if strings.TrimSpace(meta.ReasoningEffort) != "" && strings.TrimSpace(req.ReasoningEffort) == "" {
				reasoningEffort = strings.TrimSpace(meta.ReasoningEffort)
			}
		}
		if strings.TrimSpace(provider) == "" {
			provider = strings.TrimSpace(client.Name())
		}
	}
	provider = normalizeProviderName(provider)
	model = strings.TrimSpace(model)
	baseURL = normalizeModelRouteBaseURL(provider, baseURL)
	reasoningEffort = normalizeReasoningEffort(reasoningEffort)
	key := modelRouteKeyFromParts(provider, model, baseURL, reasoningEffort)
	return ModelRoute{
		Key:             key,
		Label:           modelRouteLabel(provider, model, baseURL, reasoningEffort),
		Provider:        provider,
		Model:           model,
		BaseURL:         baseURL,
		ReasoningEffort: reasoningEffort,
	}
}

func modelRouteKeyFromParts(provider string, model string, baseURL string, reasoningEffort string) string {
	provider = normalizeProviderName(provider)
	model = strings.TrimSpace(model)
	baseURL = normalizeModelRouteBaseURL(provider, baseURL)
	reasoningEffort = normalizeReasoningEffort(reasoningEffort)
	if provider == "" && model == "" && baseURL == "" && reasoningEffort == "" {
		return ""
	}
	return provider + "\x00" + model + "\x00" + baseURL + "\x00" + reasoningEffort
}

func modelRouteLabel(provider string, model string, baseURL string, reasoningEffort string) string {
	provider = normalizeProviderName(provider)
	model = strings.TrimSpace(model)
	baseURL = normalizeModelRouteBaseURL(provider, baseURL)
	reasoningEffort = normalizeReasoningEffort(reasoningEffort)
	label := provider
	if model != "" {
		if label != "" {
			label += "/"
		}
		label += model
	}
	if baseURL != "" {
		label += "@" + baseURL
	}
	if reasoningEffort != "" {
		label += "#" + reasoningEffort
	}
	if label == "" {
		return "(unrouted model)"
	}
	return label
}

func normalizeModelRouteBaseURL(provider string, baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}
	if strings.Contains(baseURL, "://") {
		normalized := normalizeProfileBaseURL(provider, baseURL)
		parsed, err := url.Parse(normalized)
		if err == nil && parsed.Scheme != "" {
			parsed.Scheme = strings.ToLower(parsed.Scheme)
			parsed.Host = strings.ToLower(parsed.Host)
			parsed.Path = strings.TrimRight(parsed.Path, "/")
			return parsed.String()
		}
		return strings.TrimRight(normalized, "/")
	}
	return strings.ToLower(strings.Join(strings.Fields(baseURL), " "))
}

func isLocalModelRouteBaseURL(baseURL string) bool {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" || !strings.Contains(baseURL, "://") {
		return false
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	switch host {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	default:
		return strings.HasPrefix(host, "127.")
	}
}

func acquireModelRoute(ctx context.Context, scheduler *ModelRouteScheduler, policy ModelRoutePolicy, cfg Config, client ProviderClient, req ChatRequest) (func(), ModelRoute, error) {
	if scheduler == nil {
		scheduler = defaultModelRouteScheduler()
	}
	policy = policy.normalized()
	route := modelRouteForRequest(cfg, client, req)
	limit := policy.LimitFor(route)
	release, err := scheduler.Acquire(ctx, route, limit)
	if err != nil {
		return nil, route, fmt.Errorf("model route queue wait failed for %s: %w", route.Label, err)
	}
	return release, route, nil
}

func (a *Agent) modelRouteScheduler() *ModelRouteScheduler {
	if a != nil && a.ModelRoutes != nil {
		return a.ModelRoutes
	}
	return defaultModelRouteScheduler()
}

func (a *Agent) modelRoutePolicy() ModelRoutePolicy {
	if a == nil {
		return modelRoutePolicyFromConfig(Config{})
	}
	return modelRoutePolicyFromConfig(a.Config)
}
