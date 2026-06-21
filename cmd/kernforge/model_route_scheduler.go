package main

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	ServiceTier     string
}

type modelRouteMetadataProvider interface {
	ModelRouteMetadata() ModelRouteMetadata
}

type ModelRoute struct {
	// Key is the IDENTITY of a route: provider+model+baseURL+effort+tier. It is
	// used for route-limit lookups, dedup, and anything that must distinguish two
	// requests that differ only by effort/tier.
	Key string
	// ScheduleKey is the SCHEDULING key: the physical backend only
	// (provider+model+baseURL). Reasoning effort and service tier do not change
	// how many requests a backend serves in parallel, so two requests against the
	// same physical backend must serialize on the same limiter even when their
	// effort/tier (and therefore Key) differ. When empty the scheduler falls back
	// to Key for backward compatibility.
	ScheduleKey     string
	Label           string
	Provider        string
	Model           string
	BaseURL         string
	ReasoningEffort string
	ServiceTier     string
}

// schedulingKey returns the key the scheduler should serialize on. It prefers
// the physical-backend ScheduleKey and falls back to the identity Key so that
// callers that only populate Key keep their previous behavior.
func (r ModelRoute) schedulingKey() string {
	if sk := strings.TrimSpace(r.ScheduleKey); sk != "" {
		return sk
	}
	return strings.TrimSpace(r.Key)
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
	key   string
	label string
	// mu guards every field below, including limit. A counter+cond design lets a
	// dynamic per-route limit change resize the gate atomically (M23) without
	// draining in-flight requests: raising the limit wakes waiters, lowering it
	// simply blocks new acquires until active drains below the new limit.
	mu            sync.Mutex
	cond          *sync.Cond
	limit         int
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
	if s == nil || route.schedulingKey() == "" || limit <= 0 {
		return func() {}, nil
	}
	limiter := s.limiter(route, limit)
	start := time.Now()

	// Wake the waiter loop when ctx is cancelled so a queued acquire can bail out
	// even though sync.Cond has no native context support.
	ctxDone := ctx.Done()
	var watcherDone chan struct{}
	if ctxDone != nil {
		watcherDone = make(chan struct{})
		go func() {
			select {
			case <-ctxDone:
				limiter.mu.Lock()
				limiter.cond.Broadcast()
				limiter.mu.Unlock()
			case <-watcherDone:
			}
		}()
	}

	limiter.mu.Lock()
	limiter.queued++
	for limiter.active >= limiter.limit {
		if ctxDone != nil {
			select {
			case <-ctxDone:
				if limiter.queued > 0 {
					limiter.queued--
				}
				limiter.mu.Unlock()
				if watcherDone != nil {
					close(watcherDone)
				}
				return nil, ctx.Err()
			default:
			}
		}
		limiter.cond.Wait()
	}
	wait := time.Since(start)
	limiter.queued--
	limiter.active++
	limiter.totalAcquires++
	limiter.lastWait = wait
	if wait > limiter.maxWait {
		limiter.maxWait = wait
	}
	limiter.lastAcquired = time.Now()
	limiter.mu.Unlock()
	if watcherDone != nil {
		close(watcherDone)
	}

	released := false
	var releaseMu sync.Mutex
	return func() {
		releaseMu.Lock()
		defer releaseMu.Unlock()
		if released {
			return
		}
		released = true
		limiter.mu.Lock()
		if limiter.active > 0 {
			limiter.active--
		}
		// Wake all waiters: a resize may have raised the limit so more than one
		// queued acquire could now proceed.
		limiter.cond.Broadcast()
		limiter.mu.Unlock()
	}, nil
}

func (s *ModelRouteScheduler) limiter(route ModelRoute, limit int) *modelRouteLimiter {
	if limit < 1 {
		limit = 1
	}
	// Serialize on the physical backend (provider+model+baseURL), NOT on the full
	// identity that includes reasoning effort and service tier. A single local
	// backend must get one limiter regardless of effort/tier, so requests at
	// different efforts against the same server still serialize (M6).
	key := route.schedulingKey()
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
		// Dynamic per-route limit change: resize atomically under the limiter mutex
		// (M23). Raising the limit wakes queued waiters immediately; lowering it
		// just gates new acquires until active drains below the new limit. No
		// draining of in-flight requests is required.
		existing.mu.Lock()
		if existing.limit != limit {
			existing.limit = limit
			existing.cond.Broadcast()
		}
		existing.mu.Unlock()
		return existing
	}
	limiter := &modelRouteLimiter{
		key:   key,
		label: label,
		limit: limit,
	}
	limiter.cond = sync.NewCond(&limiter.mu)
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
		"anthropic-claude-cli": 1,
		"codex-cli":            1,
		"deepseek":             2,
		"llama.cpp":            1,
		"lmstudio":             1,
		"ollama":               1,
		"opencode":             1,
		"opencode-go":          1,
		"openrouter":           2,
		"openai-codex":         2,
		"vllm":                 1,
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
	req = requestWithRouteReasoningEffort(cfg, client, req)
	req = requestWithRouteServiceTier(cfg, client, req)
	provider := ""
	model := firstNonBlankString(req.Model, cfg.Model)
	baseURL := ""
	if client == nil {
		provider = strings.TrimSpace(cfg.Provider)
		baseURL = strings.TrimSpace(cfg.BaseURL)
	}
	reasoningEffort := strings.TrimSpace(req.ReasoningEffort)
	serviceTier := strings.TrimSpace(req.ServiceTier)
	metaEffort := ""
	metaServiceTier := ""
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
				metaEffort = strings.TrimSpace(meta.ReasoningEffort)
			}
			if strings.TrimSpace(meta.ServiceTier) != "" && strings.TrimSpace(req.ServiceTier) == "" {
				metaServiceTier = strings.TrimSpace(meta.ServiceTier)
			}
		}
		if strings.TrimSpace(provider) == "" {
			provider = strings.TrimSpace(client.Name())
		}
		if strings.TrimSpace(provider) == "" {
			provider = strings.TrimSpace(cfg.Provider)
		}
	}
	provider = normalizeProviderName(provider)
	model = strings.TrimSpace(model)
	if strings.TrimSpace(baseURL) == "" &&
		provider == normalizeProviderName(cfg.Provider) &&
		strings.EqualFold(model, strings.TrimSpace(cfg.Model)) {
		baseURL = strings.TrimSpace(cfg.BaseURL)
	}
	baseURL = normalizeModelRouteBaseURL(provider, baseURL)
	reasoningEffort = normalizeReasoningEffort(reasoningEffort)
	if reasoningEffort == "" {
		reasoningEffort = normalizeReasoningEffort(metaEffort)
	}
	if reasoningEffort == "" && reviewConfiguredRouteMatchesMain(cfg, provider, model, baseURL) {
		reasoningEffort = normalizeReasoningEffort(cfg.ReasoningEffort)
	}
	serviceTier = normalizeServiceTier(serviceTier)
	if serviceTier == "" {
		serviceTier = normalizeServiceTier(metaServiceTier)
	}
	if serviceTier == "" && reviewConfiguredRouteMatchesMain(cfg, provider, model, baseURL) {
		serviceTier = normalizeServiceTier(cfg.ServiceTier)
	}
	key := modelRouteKeyFromParts(provider, model, baseURL, reasoningEffort, serviceTier)
	scheduleKey := modelRouteScheduleKeyFromParts(provider, model, baseURL)
	return ModelRoute{
		Key:             key,
		ScheduleKey:     scheduleKey,
		Label:           modelRouteLabel(provider, model, baseURL, reasoningEffort, serviceTier),
		Provider:        provider,
		Model:           model,
		BaseURL:         baseURL,
		ReasoningEffort: reasoningEffort,
		ServiceTier:     serviceTier,
	}
}

func requestWithRouteReasoningEffort(cfg Config, client ProviderClient, req ChatRequest) ChatRequest {
	if strings.TrimSpace(req.ReasoningEffort) != "" {
		req.ReasoningEffort = normalizeReasoningEffort(req.ReasoningEffort)
		return req
	}

	provider := ""
	model := firstNonBlankString(req.Model, cfg.Model)
	baseURL := ""
	if client == nil {
		provider = strings.TrimSpace(cfg.Provider)
		baseURL = strings.TrimSpace(cfg.BaseURL)
	}
	metaEffort := ""
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
			metaEffort = normalizeReasoningEffort(meta.ReasoningEffort)
		}
		if strings.TrimSpace(provider) == "" {
			provider = strings.TrimSpace(client.Name())
		}
		if strings.TrimSpace(provider) == "" {
			provider = strings.TrimSpace(cfg.Provider)
		}
	}

	// Only inherit the main effort when the role carries no explicit effort of its
	// own. If the reviewer/worker client has an explicit metaEffort, honor it as-is
	// rather than raising it onto a higher-cost route (M25). With the scheduling
	// key decoupled from effort (M6), route-sharing for serialization no longer
	// requires forcing matching effort, so we never silently raise an explicit
	// per-role effort.
	if metaEffort != "" {
		req.ReasoningEffort = metaEffort
		return req
	}
	if reviewConfiguredRouteMatchesMain(cfg, provider, model, baseURL) {
		mainEffort := normalizeReasoningEffort(cfg.ReasoningEffort)
		if mainEffort != "" {
			req.ReasoningEffort = mainEffort
			return req
		}
	}
	return req
}

func requestWithRouteServiceTier(cfg Config, client ProviderClient, req ChatRequest) ChatRequest {
	if strings.TrimSpace(req.ServiceTier) != "" {
		req.ServiceTier = normalizeServiceTier(req.ServiceTier)
		return req
	}

	provider := ""
	model := firstNonBlankString(req.Model, cfg.Model)
	baseURL := ""
	if client == nil {
		provider = strings.TrimSpace(cfg.Provider)
		baseURL = strings.TrimSpace(cfg.BaseURL)
	}
	metaServiceTier := ""
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
			metaServiceTier = normalizeServiceTier(meta.ServiceTier)
		}
		if strings.TrimSpace(provider) == "" {
			provider = strings.TrimSpace(client.Name())
		}
		if strings.TrimSpace(provider) == "" {
			provider = strings.TrimSpace(cfg.Provider)
		}
	}

	if reviewConfiguredRouteMatchesMain(cfg, provider, model, baseURL) {
		mainServiceTier := normalizeServiceTier(cfg.ServiceTier)
		if mainServiceTier != "" {
			req.ServiceTier = mainServiceTier
			return req
		}
	}
	if metaServiceTier != "" {
		req.ServiceTier = metaServiceTier
	}
	return req
}

func modelRouteKeyFromParts(provider string, model string, baseURL string, reasoningEffort string, serviceTier string) string {
	provider = normalizeProviderName(provider)
	model = strings.TrimSpace(model)
	baseURL = normalizeModelRouteBaseURL(provider, baseURL)
	reasoningEffort = normalizeReasoningEffort(reasoningEffort)
	serviceTier = normalizeServiceTier(serviceTier)
	if provider == "" && model == "" && baseURL == "" && reasoningEffort == "" && serviceTier == "" {
		return ""
	}
	return provider + "\x00" + model + "\x00" + baseURL + "\x00" + reasoningEffort + "\x00" + serviceTier
}

// modelRouteScheduleKeyFromParts builds the SCHEDULING key from the physical
// backend only (provider+model+baseURL). Reasoning effort and service tier are
// deliberately excluded: they do not change how many concurrent requests a
// backend can serve, so they must not fragment a single backend limiter (M6).
func modelRouteScheduleKeyFromParts(provider string, model string, baseURL string) string {
	provider = normalizeProviderName(provider)
	model = strings.TrimSpace(model)
	baseURL = normalizeModelRouteBaseURL(provider, baseURL)
	if provider == "" && model == "" && baseURL == "" {
		return ""
	}
	return provider + "\x00" + model + "\x00" + baseURL
}

func modelRouteLabel(provider string, model string, baseURL string, reasoningEffort string, serviceTier string) string {
	provider = normalizeProviderName(provider)
	model = strings.TrimSpace(model)
	baseURL = normalizeModelRouteBaseURL(provider, baseURL)
	reasoningEffort = normalizeReasoningEffort(reasoningEffort)
	serviceTier = normalizeServiceTier(serviceTier)
	label := providerUserLabel(provider)
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
	if serviceTier != "" {
		label += "~" + serviceTier
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
	if host == "" {
		return false
	}
	// Well-known local and container-host names.
	switch host {
	case "localhost", "0.0.0.0", "::", "host.docker.internal", "gateway.docker.internal":
		return true
	}
	if strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return true
	}
	// IP literals: loopback, link-local, and RFC1918 private ranges are all
	// non-routable single backends, not cloud routes (M18).
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() {
			return true
		}
		// Treat unspecified address (e.g. "::") as local.
		if ip.IsUnspecified() {
			return true
		}
		return false
	}
	return false
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

// maxModelRouteFallbackModels caps how many configured fallback models are
// tried after the primary route fails terminally or is refused. The chain is
// off by default (empty config) and is bounded so a misconfigured list cannot
// turn a single turn into an unbounded sweep of model retries.
const maxModelRouteFallbackModels = 3

// selectModelRouteFallbackModels returns the ordered, deduplicated fallback
// model chain for a turn. It drops blank entries, entries that match the
// primary model (a fallback to the same model cannot recover a model-specific
// terminal error or refusal), and any duplicates, then caps the result at
// maxModelRouteFallbackModels. An empty config yields a nil chain, which keeps
// the current single-route behavior.
func selectModelRouteFallbackModels(cfg Config, primaryModel string) []string {
	if len(cfg.FallbackModels) == 0 {
		return nil
	}
	primaryModel = strings.TrimSpace(primaryModel)
	out := make([]string, 0, maxModelRouteFallbackModels)
	seen := map[string]struct{}{}
	if primaryModel != "" {
		seen[strings.ToLower(primaryModel)] = struct{}{}
	}
	for _, candidate := range cfg.FallbackModels {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		key := strings.ToLower(candidate)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
		if len(out) >= maxModelRouteFallbackModels {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// chatResponseIsRefusal reports whether a provider response is a safety/policy
// refusal (HTTP 200 with stop_reason "refusal"). A refusal is not an error, so
// it must be detected on the response rather than the error value before the
// fallback chain decides whether to try the next model.
func chatResponseIsRefusal(resp ChatResponse) bool {
	return normalizeStopReason(resp.StopReason) == "refusal"
}

// shouldTryFallbackModel decides whether the primary route's outcome warrants
// trying the next model in the fallback chain. The trigger is a terminal
// (non-retryable) provider error or a refusal response. Retryable errors are
// left to the existing per-route retry loop in completeModelTurn, and a clean
// success never falls back.
func shouldTryFallbackModel(err error, resp ChatResponse) bool {
	if err != nil {
		// Context cancellation/deadline is the caller giving up, not a model
		// problem the next model could solve; do not consume the fallback chain.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
		// A retryable transport error is handled by the per-route retry loop; only
		// a terminal/non-retryable error should advance to the next model.
		return !shouldRetryProviderError(err)
	}
	return chatResponseIsRefusal(resp)
}
