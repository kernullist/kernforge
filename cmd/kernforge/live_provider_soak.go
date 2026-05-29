package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	liveProviderSoakEnvEnabled     = "KERNFORGE_REAL_PROVIDER_SOAK"
	liveProviderSoakDefaultTimeout = 2 * time.Minute
	liveProviderSoakMaxRetriesCap  = 2
)

type LiveProviderSoakOptions struct {
	Mode           string
	Turns          int
	Timeout        time.Duration
	SimulateReload bool
	SkipReload     bool
	Now            func() time.Time
	Getenv         func(string) string
}

type LiveProviderSoakRecommendation struct {
	FailureClass   string `json:"failure_class,omitempty"`
	Severity       string `json:"severity,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
	EvidenceRef    string `json:"evidence_ref,omitempty"`
	NextSafeAction string `json:"next_safe_action,omitempty"`
	NextCommand    string `json:"next_command,omitempty"`
}

type LiveProviderSoakValidationResult struct {
	Command     string `json:"command,omitempty"`
	Status      string `json:"status,omitempty"`
	Summary     string `json:"summary,omitempty"`
	EvidenceRef string `json:"evidence_ref,omitempty"`
}

type LiveProviderSoakReloadVerification struct {
	Reloaded          bool     `json:"reloaded"`
	StatusCompact     bool     `json:"status_compact"`
	StatusDetail      bool     `json:"status_detail"`
	MCPStatusJSON     bool     `json:"mcp_status_json"`
	MCPReviewJSON     bool     `json:"mcp_review_json"`
	MarkdownArtifact  bool     `json:"markdown_artifact"`
	RuntimeGateLedger bool     `json:"runtime_gate_ledger"`
	MatchingFields    []string `json:"matching_fields,omitempty"`
	Mismatches        []string `json:"mismatches,omitempty"`
	NextCommand       string   `json:"next_command,omitempty"`
}

type liveProviderSoakRoute struct {
	Config        Config
	Client        ProviderClient
	Provider      string
	ProviderLabel string
	Model         string
	SetupError    error
	Configured    bool
	Independent   bool
	Collides      bool
}

type liveProviderSoakCallResult struct {
	Text                         string
	LatencyMS                    int64
	RetryCount                   int
	MalformedOutputCount         int
	EmptyResponseCount           int
	WeakOutputDegraded           bool
	DuplicatedOutput             bool
	EvidenceFreeOutput           bool
	ToolCallMismatch             bool
	ReviewerMainModelCollision   bool
	MissingFinalAnswerDisclosure bool
	Event                        *ReviewRouteHealthEvent
	Success                      bool
}

func (v *LiveProviderSoakReloadVerification) Normalize() {
	if v == nil {
		return
	}
	v.MatchingFields = normalizeTaskStateList(v.MatchingFields, 16)
	v.Mismatches = normalizeTaskStateList(v.Mismatches, 16)
	v.NextCommand = strings.TrimSpace(v.NextCommand)
}

func normalizeLiveProviderSoakRecommendations(items []LiveProviderSoakRecommendation) []LiveProviderSoakRecommendation {
	out := make([]LiveProviderSoakRecommendation, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item.FailureClass = normalizeProviderFailureClass(item.FailureClass)
		item.Severity = strings.TrimSpace(strings.ToLower(item.Severity))
		item.Recommendation = strings.TrimSpace(item.Recommendation)
		item.EvidenceRef = filepath.ToSlash(strings.TrimSpace(item.EvidenceRef))
		item.NextSafeAction = strings.TrimSpace(item.NextSafeAction)
		item.NextCommand = strings.TrimSpace(item.NextCommand)
		if item.FailureClass == "" && item.Recommendation == "" {
			continue
		}
		key := strings.Join([]string{item.FailureClass, item.EvidenceRef, item.NextCommand}, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
		if len(out) >= 16 {
			break
		}
	}
	return out
}

func normalizeLiveProviderSoakValidationResults(items []LiveProviderSoakValidationResult) []LiveProviderSoakValidationResult {
	out := make([]LiveProviderSoakValidationResult, 0, len(items))
	for _, item := range items {
		item.Command = strings.TrimSpace(item.Command)
		item.Status = strings.TrimSpace(strings.ToLower(item.Status))
		item.Summary = strings.TrimSpace(item.Summary)
		item.EvidenceRef = filepath.ToSlash(strings.TrimSpace(item.EvidenceRef))
		if item.Command == "" && item.Status == "" {
			continue
		}
		out = append(out, item)
		if len(out) >= 16 {
			break
		}
	}
	return out
}

func parseReviewSoakCommandOptions(args string) LiveProviderSoakOptions {
	opts := LiveProviderSoakOptions{
		Mode:           liveProviderDrillModeScripted,
		Timeout:        liveProviderSoakDefaultTimeout,
		SimulateReload: true,
	}
	fields := splitAnalysisCommandLine(args)
	for i := 0; i < len(fields); i++ {
		field := strings.TrimSpace(fields[i])
		lower := strings.ToLower(field)
		switch {
		case lower == "--mode" && i+1 < len(fields):
			i++
			opts.Mode = normalizeLiveProviderSoakMode(fields[i])
		case strings.HasPrefix(lower, "--mode="):
			opts.Mode = normalizeLiveProviderSoakMode(field[len("--mode="):])
		case lower == "--turns" && i+1 < len(fields):
			i++
			if n, err := strconv.Atoi(strings.TrimSpace(fields[i])); err == nil && n > 0 {
				opts.Turns = n
			}
		case strings.HasPrefix(lower, "--turns="):
			if n, err := strconv.Atoi(strings.TrimSpace(field[len("--turns="):])); err == nil && n > 0 {
				opts.Turns = n
			}
		case lower == "--timeout" && i+1 < len(fields):
			i++
			if d, ok := parseLiveProviderSoakDuration(fields[i]); ok {
				opts.Timeout = d
			}
		case strings.HasPrefix(lower, "--timeout="):
			if d, ok := parseLiveProviderSoakDuration(field[len("--timeout="):]); ok {
				opts.Timeout = d
			}
		case lower == "--no-reload":
			opts.SkipReload = true
		case lower == "scripted":
			opts.Mode = liveProviderDrillModeScripted
		case lower == "real-provider" || lower == "real_provider":
			opts.Mode = liveProviderDrillModeReal
		}
	}
	opts.Normalize()
	return opts
}

func (o *LiveProviderSoakOptions) Normalize() {
	if o == nil {
		return
	}
	o.Mode = normalizeLiveProviderSoakMode(o.Mode)
	if o.Timeout <= 0 {
		o.Timeout = liveProviderSoakDefaultTimeout
	}
	o.SimulateReload = !o.SkipReload
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

func normalizeLiveProviderSoakMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(mode, "-", "_")))
	switch mode {
	case "", liveProviderDrillModeScripted:
		return liveProviderDrillModeScripted
	case "real", "real_provider", "realprovider":
		return liveProviderDrillModeReal
	default:
		return mode
	}
}

func parseLiveProviderSoakDuration(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if d, err := time.ParseDuration(value); err == nil && d > 0 {
		return d, true
	}
	if n, err := strconv.Atoi(value); err == nil && n > 0 {
		return time.Duration(n) * time.Second, true
	}
	return 0, false
}

func (rt *runtimeState) handleReviewSoakCommandWithContext(ctx context.Context, args string) error {
	opts := parseReviewSoakCommandOptions(args)
	report, ledger, err := rt.runLiveProviderSoak(ctx, opts)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Review Soak"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("live_provider_soak", liveProviderDrillStatusLine(&report)))
	if report.ArtifactDir != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("artifact_dir", report.ArtifactDir))
	}
	if report.ArtifactPath != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("soak_report", report.ArtifactPath))
	}
	if report.SkippedReason != "" {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("skipped: "+report.SkippedReason))
	} else if report.Status == liveProviderDrillStatusFailed || ledger.Status == runtimeGateStatusBlocked {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("soak completed with blocked final gate; inspect soak_report.json and runtime_gate_ledger.json"))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.successLine("soak completed"))
	}
	return nil
}

func (rt *runtimeState) runLiveProviderSoak(ctx context.Context, opts LiveProviderSoakOptions) (LiveProviderDrillReport, RuntimeGateLedger, error) {
	if rt == nil || rt.session == nil {
		return LiveProviderDrillReport{}, RuntimeGateLedger{}, fmt.Errorf("no active runtime")
	}
	opts.Normalize()
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = rt.workspace.Root
	}
	if strings.TrimSpace(root) == "" {
		root = rt.session.WorkingDir
	}
	if strings.TrimSpace(root) == "" {
		return LiveProviderDrillReport{}, RuntimeGateLedger{}, fmt.Errorf("workspace root is not configured")
	}
	started := opts.Now()
	artifactDir := liveProviderSoakArtifactDir(root, started)
	var report LiveProviderDrillReport
	switch opts.Mode {
	case liveProviderDrillModeReal:
		runCtx := ctx
		cancel := func() {}
		if opts.Timeout > 0 {
			runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		}
		defer cancel()
		report = rt.runRealProviderSoak(runCtx, root, opts, started)
	default:
		report = buildScriptedLiveProviderSoakReport(started, opts)
	}
	report.ArtifactDir = artifactDir
	report.ArtifactPath = filepath.Join(artifactDir, "soak_report.json")
	finalizeLiveProviderDrillGate(&report)
	ledger := rt.recordLiveProviderSoakReport(root, &report)
	if opts.SimulateReload {
		report.ReloadVerification = rt.simulateLiveProviderSoakReload(root, &report)
	}
	report.ProviderRecommendations = liveProviderSoakRecommendations(report.RouteHealthEvents)
	report.ValidationResults = liveProviderSoakValidationResults(report, ledger)
	finalizeLiveProviderDrillGate(&report)
	ledger = rt.recordLiveProviderSoakReport(root, &report)
	statusText := rt.renderLiveProviderSoakStatusText()
	server := &kernforgeMCPServer{rt: rt}
	mcpStatus := server.buildMCPStatusPayload(context.Background(), map[string]any{"detail": true})
	mcpReview := liveProviderSoakMCPReviewPayload(report, ledger)
	if err := writeLiveProviderSoakArtifacts(report, ledger, mcpStatus, mcpReview, statusText); err != nil {
		return report, ledger, err
	}
	return report, ledger, nil
}

func buildScriptedLiveProviderSoakReport(now time.Time, opts LiveProviderSoakOptions) LiveProviderDrillReport {
	report := buildScriptedLiveProviderDrillReport(now)
	report.ID = "live-provider-soak-scripted-" + now.Format("20060102-150405.000")
	report.Summary = "scripted live-provider soak covered required workflow classes across single-model and cross-model routes"
	report.Turns = limitLiveProviderSoakTurns(report.Turns, opts.Turns)
	report.WorkflowClasses = liveProviderDrillWorkflowClassesFromTurns(report.Turns)
	report.RouteModes = liveProviderDrillRouteModesFromTurns(report.Turns)
	finalizeLiveProviderDrillGate(&report)
	return report
}

func (rt *runtimeState) runRealProviderSoak(ctx context.Context, root string, opts LiveProviderSoakOptions, started time.Time) LiveProviderDrillReport {
	mainRoute, skippedReason := liveProviderSoakMainRoute(rt.cfg, opts.Getenv)
	if skippedReason != "" {
		report := buildRealProviderSkippedDrillReport(skippedReason)
		report.ID = "live-provider-soak-real-skipped-" + started.Format("20060102-150405.000")
		report.StartedAt = started
		report.FinishedAt = opts.Now()
		return report
	}
	reviewerRoute := liveProviderSoakReviewerRoute(rt.cfg, mainRoute, opts.Getenv)
	report := LiveProviderDrillReport{
		ID:                     "live-provider-soak-real-" + started.Format("20060102-150405.000"),
		SchemaVersion:          liveProviderDrillSchemaVersion,
		Mode:                   liveProviderDrillModeReal,
		Status:                 liveProviderDrillStatusPassed,
		ProviderConfigured:     true,
		StartedAt:              started,
		WorkflowClasses:        liveProviderDrillWorkflowClasses(),
		FinalGateStatus:        runtimeGateStatusReady,
		NextRecommendedCommand: "/status detail",
		Summary:                "real-provider soak completed provider-backed turns",
	}
	routeModes := []string{reviewRouteModeSingleModel}
	if reviewerRoute.Configured {
		routeModes = append(routeModes, reviewRouteModeCrossModel)
	}
	report.RouteModes = routeModes
	if !reviewerRoute.Configured {
		report.IndependentReviewDisclosure = "single-model soak completed with no independent reviewer evidence available"
	} else if reviewerRoute.Independent {
		report.IndependentReviewDisclosure = "independent reviewer route was available for cross-model soak turns"
	} else {
		report.IndependentReviewDisclosure = "reviewer route was configured but not independently separated from the main model"
	}
	turns := make([]LiveProviderDrillTurn, 0)
	turnLimit := opts.Turns
	if turnLimit <= 0 {
		turnLimit = len(liveProviderDrillWorkflowSpecs()) * len(routeModes)
	}
	for _, workflow := range liveProviderDrillWorkflowSpecs() {
		for _, routeMode := range routeModes {
			if len(turns) >= turnLimit {
				break
			}
			turnID := fmt.Sprintf("real-soak-%02d", len(turns)+1)
			turn := rt.runRealProviderSoakTurn(ctx, root, turnID, workflow, routeMode, mainRoute, reviewerRoute)
			if err := ctx.Err(); err != nil {
				event := liveProviderSoakContextEvent(turnID, mainRoute, err)
				turn.RouteHealthEvents = append(turn.RouteHealthEvents, event)
				finalizeLiveProviderDrillTurnGate(&turn)
				turns = append(turns, turn)
				break
			}
			turns = append(turns, turn)
		}
		if len(turns) >= turnLimit || ctx.Err() != nil {
			break
		}
	}
	report.Turns = turns
	report.WorkflowClasses = liveProviderDrillWorkflowClassesFromTurns(report.Turns)
	report.RouteModes = liveProviderDrillRouteModesFromTurns(report.Turns)
	report.FinishedAt = opts.Now()
	finalizeLiveProviderDrillGate(&report)
	if report.Status == liveProviderDrillStatusPassed {
		report.Summary = "real-provider soak completed provider-backed single-model and available cross-model turns"
	}
	return report
}

func (rt *runtimeState) runRealProviderSoakTurn(ctx context.Context, root string, turnID string, workflow liveProviderDrillWorkflowSpec, routeMode string, mainRoute liveProviderSoakRoute, reviewerRoute liveProviderSoakRoute) LiveProviderDrillTurn {
	turn := LiveProviderDrillTurn{
		TurnID:            turnID,
		RequestClass:      workflow.requestClass,
		LifecycleKind:     workflow.lifecycleKind,
		RouteMode:         routeMode,
		RouteDecision:     "real_provider_soak",
		Provider:          mainRoute.Provider,
		ProviderLabel:     mainRoute.ProviderLabel,
		ModelID:           mainRoute.Model,
		ReviewerSeparated: false,
		FinalGateState:    runtimeGateStatusReady,
		RouteQuality:      "healthy",
		EvidenceRef:       "real_provider_soak/" + turnID,
		NextCommand:       "/status detail",
	}
	mainPrompt := liveProviderSoakMainPrompt(turn)
	mainResult := runLiveProviderSoakProviderCall(ctx, root, mainRoute, turn, "primary_reviewer", "main", "KERNFORGE_SOAK_OK", mainPrompt, true)
	applyLiveProviderSoakCallResult(&turn, mainResult)
	if routeMode == reviewRouteModeSingleModel {
		turn.ReviewerSeparationStatus = "single_model_disclosed"
		turn.IndependentReviewDisclosure = "no independent reviewer evidence was available for this single-model turn"
		finalizeLiveProviderDrillTurnGate(&turn)
		return turn
	}
	turn.ReviewerProvider = reviewerRoute.Provider
	turn.ReviewerProviderLabel = reviewerRoute.ProviderLabel
	turn.ReviewerModelID = reviewerRoute.Model
	if !reviewerRoute.Independent || reviewerRoute.Collides {
		turn.ReviewerMainModelCollision = true
		turn.ReviewerSeparationStatus = "collision"
		event := liveProviderSoakTurnEvent(turn, "cross_reviewer", "cross", providerFailureClassReviewerMainModelCollision, staleContextSeverityBlocker)
		turn.RouteHealthEvents = append(turn.RouteHealthEvents, event)
		finalizeLiveProviderDrillTurnGate(&turn)
		return turn
	}
	turn.ReviewerSeparated = true
	turn.ReviewerSeparationStatus = "independent"
	turn.IndependentReviewDisclosure = "independent reviewer evidence was available for this cross-model turn"
	if reviewerRoute.SetupError != nil || reviewerRoute.Client == nil {
		turn.CrossModelReviewerFailure = true
		event := liveProviderSoakTurnEvent(turn, "cross_reviewer", "cross", reviewRouteHealthFailureClassFromText("failed", reviewModelQualityFailed, errorString(reviewerRoute.SetupError)), staleContextSeverityBlocker)
		turn.RouteHealthEvents = append(turn.RouteHealthEvents, event)
		finalizeLiveProviderDrillTurnGate(&turn)
		return turn
	}
	reviewerPrompt := liveProviderSoakReviewerPrompt(turn, mainResult.Text)
	reviewerResult := runLiveProviderSoakProviderCall(ctx, root, reviewerRoute, turn, "cross_reviewer", "cross", "KERNFORGE_SOAK_REVIEW_OK", reviewerPrompt, false)
	applyLiveProviderSoakCallResult(&turn, reviewerResult)
	if !reviewerResult.Success {
		turn.CrossModelReviewerFailure = true
	}
	finalizeLiveProviderDrillTurnGate(&turn)
	return turn
}

func liveProviderSoakMainRoute(cfg Config, getenv func(string) string) (liveProviderSoakRoute, string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if strings.TrimSpace(getenv(liveProviderSoakEnvEnabled)) != "1" {
		return liveProviderSoakRoute{}, "real-provider soak disabled: set KERNFORGE_REAL_PROVIDER_SOAK=1"
	}
	routeCfg := cfg
	if value := strings.TrimSpace(getenv("KERNFORGE_REAL_PROVIDER")); value != "" {
		routeCfg.Provider = value
	}
	if value := strings.TrimSpace(getenv("KERNFORGE_REAL_MODEL")); value != "" {
		routeCfg.Model = value
	}
	if value := strings.TrimSpace(getenv("KERNFORGE_REAL_BASE_URL")); value != "" {
		routeCfg.BaseURL = value
	}
	if value := strings.TrimSpace(getenv("KERNFORGE_REAL_API_KEY")); value != "" {
		routeCfg.APIKey = value
	}
	routeCfg.Provider = normalizeProviderName(routeCfg.Provider)
	routeCfg.Model = strings.TrimSpace(routeCfg.Model)
	normalizeConfigPaths(&routeCfg)
	if routeCfg.Provider == "" || routeCfg.Model == "" {
		return liveProviderSoakRoute{}, "provider/model config is unavailable"
	}
	client, err := NewProviderClient(routeCfg)
	if err != nil {
		return liveProviderSoakRoute{}, "provider config unavailable: " + err.Error()
	}
	return liveProviderSoakRoute{
		Config:        routeCfg,
		Client:        client,
		Provider:      routeCfg.Provider,
		ProviderLabel: reviewProviderDisplayLabel(routeCfg.Provider),
		Model:         routeCfg.Model,
		Configured:    true,
		Independent:   true,
	}, ""
}

func liveProviderSoakReviewerRoute(cfg Config, mainRoute liveProviderSoakRoute, getenv func(string) string) liveProviderSoakRoute {
	if getenv == nil {
		getenv = os.Getenv
	}
	routeCfg := cfg
	envProvider := strings.TrimSpace(getenv("KERNFORGE_REAL_REVIEWER_PROVIDER"))
	envModel := strings.TrimSpace(getenv("KERNFORGE_REAL_REVIEWER_MODEL"))
	if envProvider != "" || envModel != "" {
		routeCfg.Provider = envProvider
		routeCfg.Model = envModel
		routeCfg.BaseURL = strings.TrimSpace(getenv("KERNFORGE_REAL_REVIEWER_BASE_URL"))
		routeCfg.APIKey = strings.TrimSpace(getenv("KERNFORGE_REAL_REVIEWER_API_KEY"))
	} else {
		reviewCfg := configReviewHarness(cfg)
		for _, role := range []string{"cross_reviewer", "security_reviewer", "regression_reviewer", "test_reviewer", "final_gate_reviewer", "primary_reviewer"} {
			roleCfg, ok := reviewCfg.RoleModels[role]
			if !ok || strings.TrimSpace(roleCfg.Provider) == "" || strings.TrimSpace(roleCfg.Model) == "" {
				continue
			}
			routeCfg.Provider = roleCfg.Provider
			routeCfg.Model = roleCfg.Model
			routeCfg.BaseURL = roleCfg.BaseURL
			routeCfg.APIKey = roleCfg.APIKey
			break
		}
	}
	routeCfg.Provider = normalizeProviderName(routeCfg.Provider)
	routeCfg.Model = strings.TrimSpace(routeCfg.Model)
	if routeCfg.Provider == "" || routeCfg.Model == "" {
		return liveProviderSoakRoute{}
	}
	normalizeConfigPaths(&routeCfg)
	route := liveProviderSoakRoute{
		Config:        routeCfg,
		Provider:      routeCfg.Provider,
		ProviderLabel: reviewProviderDisplayLabel(routeCfg.Provider),
		Model:         routeCfg.Model,
		Configured:    true,
	}
	route.Collides = reviewProviderModelCollides(mainRoute.Provider, mainRoute.Model, route.Provider, route.Model)
	route.Independent = !route.Collides
	if route.Collides {
		return route
	}
	client, err := NewProviderClient(routeCfg)
	if err != nil {
		route.SetupError = err
		return route
	}
	route.Client = client
	return route
}

func runLiveProviderSoakProviderCall(ctx context.Context, root string, route liveProviderSoakRoute, turn LiveProviderDrillTurn, role string, kind string, marker string, prompt string, requireDisclosure bool) liveProviderSoakCallResult {
	result := liveProviderSoakCallResult{}
	maxRetries := route.Config.MaxRequestRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	if maxRetries > liveProviderSoakMaxRetriesCap {
		maxRetries = liveProviderSoakMaxRetriesCap
	}
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			result.RetryCount++
		}
		start := time.Now()
		resp, err := route.Client.Complete(ctx, ChatRequest{
			Model: route.Model,
			Messages: []Message{{
				Role: "user",
				Text: prompt,
			}},
			MaxTokens:  192,
			WorkingDir: root,
		})
		result.LatencyMS += time.Since(start).Milliseconds()
		if err != nil {
			event := liveProviderSoakTurnEvent(turn, role, kind, reviewRouteHealthFailureClassFromText("failed", reviewModelQualityFailed, err.Error()), staleContextSeverityBlocker)
			event.Provider = route.Provider
			event.ProviderLabel = route.ProviderLabel
			event.ModelID = route.Model
			event.LatencyMS = result.LatencyMS
			event.RetryCount = result.RetryCount
			event.Normalize()
			result.Event = &event
			if attempt < maxRetries && event.FailureClass != providerFailureClassAuthConfigMissing {
				continue
			}
			return result
		}
		text := strings.TrimSpace(resp.Message.Text)
		result.Text = text
		ok, evidenceFree, missingDisclosure := validateLiveProviderSoakOutput(text, marker, turn.TurnID, requireDisclosure)
		result.EvidenceFreeOutput = result.EvidenceFreeOutput || evidenceFree
		result.MissingFinalAnswerDisclosure = result.MissingFinalAnswerDisclosure || missingDisclosure
		if text == "" {
			result.EmptyResponseCount++
			event := liveProviderSoakTurnEvent(turn, role, kind, providerFailureClassEmptyResponse, staleContextSeverityBlocker)
			event.Provider = route.Provider
			event.ProviderLabel = route.ProviderLabel
			event.ModelID = route.Model
			event.LatencyMS = result.LatencyMS
			event.RetryCount = result.RetryCount
			event.EmptyResponseCount = result.EmptyResponseCount
			event.Normalize()
			result.Event = &event
			if attempt < maxRetries {
				continue
			}
			return result
		}
		if !ok {
			result.MalformedOutputCount++
			event := liveProviderSoakTurnEvent(turn, role, kind, providerFailureClassMalformedResponse, staleContextSeverityBlocker)
			event.Provider = route.Provider
			event.ProviderLabel = route.ProviderLabel
			event.ModelID = route.Model
			event.LatencyMS = result.LatencyMS
			event.RetryCount = result.RetryCount
			event.MalformedOutputCount = result.MalformedOutputCount
			event.EvidenceFreeOutput = evidenceFree
			event.Normalize()
			result.Event = &event
			if attempt < maxRetries {
				continue
			}
			return result
		}
		result.Success = true
		result.Event = nil
		return result
	}
	event := liveProviderSoakTurnEvent(turn, role, kind, providerFailureClassRetryExhausted, staleContextSeverityBlocker)
	event.Provider = route.Provider
	event.ProviderLabel = route.ProviderLabel
	event.ModelID = route.Model
	event.LatencyMS = result.LatencyMS
	event.RetryCount = result.RetryCount
	event.Normalize()
	result.Event = &event
	return result
}

func validateLiveProviderSoakOutput(text string, marker string, turnID string, requireDisclosure bool) (bool, bool, bool) {
	lower := strings.ToLower(text)
	if !strings.Contains(text, marker) {
		return false, !strings.Contains(lower, "evidence_ref:"), requireDisclosure && !strings.Contains(lower, "final_answer_disclosure:")
	}
	if !strings.Contains(lower, strings.ToLower(turnID)) {
		return false, !strings.Contains(lower, "evidence_ref:"), requireDisclosure && !strings.Contains(lower, "final_answer_disclosure:")
	}
	evidenceFree := !strings.Contains(lower, "evidence_ref:")
	missingDisclosure := requireDisclosure && !strings.Contains(lower, "final_answer_disclosure:")
	if evidenceFree || missingDisclosure {
		return false, evidenceFree, missingDisclosure
	}
	return true, false, false
}

func applyLiveProviderSoakCallResult(turn *LiveProviderDrillTurn, result liveProviderSoakCallResult) {
	if turn == nil {
		return
	}
	turn.LatencyMS += result.LatencyMS
	turn.RetryCount += result.RetryCount
	turn.MalformedOutputCount += result.MalformedOutputCount
	turn.EmptyResponseCount += result.EmptyResponseCount
	turn.WeakOutputDegraded = turn.WeakOutputDegraded || result.WeakOutputDegraded
	turn.DuplicatedOutput = turn.DuplicatedOutput || result.DuplicatedOutput
	turn.EvidenceFreeOutput = turn.EvidenceFreeOutput || result.EvidenceFreeOutput
	turn.ToolCallMismatch = turn.ToolCallMismatch || result.ToolCallMismatch
	turn.ReviewerMainModelCollision = turn.ReviewerMainModelCollision || result.ReviewerMainModelCollision
	turn.MissingFinalAnswerDisclosure = turn.MissingFinalAnswerDisclosure || result.MissingFinalAnswerDisclosure
	if result.Event != nil {
		turn.RouteHealthEvents = append(turn.RouteHealthEvents, *result.Event)
	}
}

func liveProviderSoakMainPrompt(turn LiveProviderDrillTurn) string {
	return strings.Join([]string{
		"You are executing a KernForge live-provider soak turn.",
		"Return exactly these lines and no markdown:",
		"KERNFORGE_SOAK_OK",
		"turn_id: " + turn.TurnID,
		"request_class: " + turn.RequestClass,
		"lifecycle_kind: " + turn.LifecycleKind,
		"evidence_ref: real_provider_soak/" + turn.TurnID,
		"final_answer_disclosure: route_mode=" + turn.RouteMode + " independent_review=" + valueOrDefault(turn.ReviewerSeparationStatus, "pending"),
	}, "\n")
}

func liveProviderSoakReviewerPrompt(turn LiveProviderDrillTurn, mainText string) string {
	return strings.Join([]string{
		"You are the independent reviewer in a KernForge live-provider soak turn.",
		"Prior provider output follows:",
		compactPromptSection(mainText, 1000),
		"Return exactly these lines and no markdown:",
		"KERNFORGE_SOAK_REVIEW_OK",
		"turn_id: " + turn.TurnID,
		"request_class: " + turn.RequestClass,
		"lifecycle_kind: " + turn.LifecycleKind,
		"evidence_ref: real_provider_soak/" + turn.TurnID + "/cross_review",
		"reviewer_separation: independent",
	}, "\n")
}

func finalizeLiveProviderDrillGate(report *LiveProviderDrillReport) {
	if report == nil {
		return
	}
	for i := range report.Turns {
		finalizeLiveProviderDrillTurnGate(&report.Turns[i])
		report.RouteHealthEvents = append(report.RouteHealthEvents, report.Turns[i].RouteHealthEvents...)
	}
	report.RouteHealthEvents = dedupeReviewRouteHealthEvents(report.RouteHealthEvents, 64)
	report.ProviderRecommendations = liveProviderSoakRecommendations(report.RouteHealthEvents)
	report.Normalize()
	if report.Status == liveProviderDrillStatusSkipped {
		report.FinalGateStatus = firstNonBlankString(report.FinalGateStatus, "skipped")
		return
	}
	if liveProviderDrillBlocksFinalization(report) {
		report.FinalGateStatus = runtimeGateStatusBlocked
		report.Status = liveProviderDrillStatusFailed
		if report.NextRecommendedCommand == "" {
			report.NextRecommendedCommand = "/status detail"
		}
		return
	}
	report.FinalGateStatus = runtimeGateStatusReady
	if report.Status == "" || report.Status == liveProviderDrillStatusFailed {
		report.Status = liveProviderDrillStatusPassed
	}
}

func finalizeLiveProviderDrillTurnGate(turn *LiveProviderDrillTurn) {
	if turn == nil {
		return
	}
	turn.Normalize()
	turn.RouteHealthEvents = append(turn.RouteHealthEvents, liveProviderSoakTurnSafetyEvents(*turn)...)
	turn.RouteHealthEvents = dedupeReviewRouteHealthEvents(turn.RouteHealthEvents, 16)
	if liveProviderDrillTurnBlocksFinalization(*turn) {
		turn.FinalGateState = runtimeGateStatusBlocked
		if turn.RouteQuality == "" || turn.RouteQuality == "healthy" || turn.RouteQuality == "single_model" {
			turn.RouteQuality = "degraded"
		}
		if turn.NextCommand == "" {
			turn.NextCommand = "/status detail"
		}
	} else {
		turn.FinalGateState = firstNonBlankString(turn.FinalGateState, runtimeGateStatusReady)
		if turn.RouteQuality == "" {
			if turn.RouteMode == reviewRouteModeSingleModel {
				turn.RouteQuality = "single_model"
			} else {
				turn.RouteQuality = "healthy"
			}
		}
	}
	turn.Normalize()
}

func liveProviderSoakTurnSafetyEvents(turn LiveProviderDrillTurn) []ReviewRouteHealthEvent {
	events := []ReviewRouteHealthEvent{}
	add := func(kind string, class string) {
		event := liveProviderSoakTurnEvent(turn, "cross_reviewer", kind, class, staleContextSeverityBlocker)
		if event.Role == "" && turn.RouteMode == reviewRouteModeSingleModel {
			event.Role = "primary_reviewer"
		}
		events = append(events, event)
	}
	if turn.WeakOutputDegraded {
		add("weak_output", providerFailureClassMalformedResponse)
	}
	if turn.MalformedOutputCount > 0 {
		add("malformed_output", providerFailureClassMalformedResponse)
	}
	if turn.EmptyResponseCount > 0 {
		add("empty_response", providerFailureClassEmptyResponse)
	}
	if turn.DuplicatedOutput {
		add("duplicated_output", providerFailureClassMalformedResponse)
	}
	if turn.EvidenceFreeOutput {
		add("evidence_free_output", providerFailureClassMalformedResponse)
	}
	if turn.ToolCallMismatch {
		add("tool_call_mismatch", providerFailureClassToolCallMismatch)
	}
	if turn.ReviewerMainModelCollision {
		add("reviewer_main_model_collision", providerFailureClassReviewerMainModelCollision)
	}
	if turn.StaleReviewerOutput {
		add("stale_reviewer_output", providerFailureClassMalformedResponse)
	}
	if turn.CrossModelReviewerFailure {
		add("cross_model_reviewer_failure", providerFailureClassRetryExhausted)
	}
	if turn.MissingFinalAnswerDisclosure {
		add("missing_final_answer_disclosure", providerFailureClassMalformedResponse)
	}
	return events
}

func liveProviderSoakTurnEvent(turn LiveProviderDrillTurn, role string, kind string, class string, severity string) ReviewRouteHealthEvent {
	event := ReviewRouteHealthEvent{
		TurnID:                     turn.TurnID,
		Role:                       role,
		Kind:                       kind,
		Provider:                   firstNonBlankString(turn.ReviewerProvider, turn.Provider),
		ProviderLabel:              firstNonBlankString(turn.ReviewerProviderLabel, turn.ProviderLabel),
		ModelID:                    firstNonBlankString(turn.ReviewerModelID, turn.ModelID),
		FailureClass:               class,
		Severity:                   severity,
		Status:                     "failed",
		LatencyMS:                  turn.LatencyMS,
		RetryCount:                 turn.RetryCount,
		MalformedOutputCount:       turn.MalformedOutputCount,
		EmptyResponseCount:         turn.EmptyResponseCount,
		WeakOutputDegraded:         turn.WeakOutputDegraded,
		DuplicatedOutput:           turn.DuplicatedOutput,
		EvidenceFreeOutput:         turn.EvidenceFreeOutput,
		ToolCallMismatch:           turn.ToolCallMismatch,
		ReviewerMainModelCollision: turn.ReviewerMainModelCollision,
		EvidenceRef:                firstNonBlankString(turn.EvidenceRef, "live_provider_soak/"+turn.TurnID),
		NextCommand:                firstNonBlankString(turn.NextCommand, "/status detail"),
		OccurredAt:                 time.Now(),
	}
	event.Normalize()
	return event
}

func liveProviderSoakContextEvent(turnID string, route liveProviderSoakRoute, err error) ReviewRouteHealthEvent {
	event := ReviewRouteHealthEvent{
		TurnID:        turnID,
		Role:          "primary_reviewer",
		Kind:          "context",
		Provider:      route.Provider,
		ProviderLabel: route.ProviderLabel,
		ModelID:       route.Model,
		FailureClass:  reviewRouteHealthFailureClassFromText("failed", reviewModelQualityFailed, errorString(err)),
		Severity:      staleContextSeverityBlocker,
		Status:        "failed",
		EvidenceRef:   "real_provider_soak/" + turnID,
	}
	event.Normalize()
	return event
}

func liveProviderDrillBlocksFinalization(report *LiveProviderDrillReport) bool {
	if report == nil {
		return false
	}
	if report.Status == liveProviderDrillStatusSkipped {
		return false
	}
	if report.FinalGateStatus == runtimeGateStatusBlocked {
		return true
	}
	for _, turn := range report.Turns {
		if liveProviderDrillTurnBlocksFinalization(turn) {
			return true
		}
	}
	for _, event := range report.RouteHealthEvents {
		event.Normalize()
		if event.FailureClass != "" && event.Severity == staleContextSeverityBlocker {
			return true
		}
	}
	return false
}

func liveProviderDrillTurnBlocksFinalization(turn LiveProviderDrillTurn) bool {
	if turn.FinalGateState == runtimeGateStatusBlocked {
		return true
	}
	if turn.WeakOutputDegraded ||
		turn.MalformedOutputCount > 0 ||
		turn.EmptyResponseCount > 0 ||
		turn.DuplicatedOutput ||
		turn.EvidenceFreeOutput ||
		turn.ToolCallMismatch ||
		turn.ReviewerMainModelCollision ||
		turn.StaleReviewerOutput ||
		turn.CrossModelReviewerFailure ||
		turn.MissingFinalAnswerDisclosure {
		return true
	}
	for _, event := range turn.RouteHealthEvents {
		event.Normalize()
		if event.FailureClass != "" && event.Severity == staleContextSeverityBlocker {
			return true
		}
	}
	return false
}

func liveProviderSoakRecommendations(events []ReviewRouteHealthEvent) []LiveProviderSoakRecommendation {
	out := make([]LiveProviderSoakRecommendation, 0, len(events))
	for _, event := range events {
		event.Normalize()
		if event.FailureClass == "" {
			continue
		}
		out = append(out, LiveProviderSoakRecommendation{
			FailureClass:   event.FailureClass,
			Severity:       event.Severity,
			Recommendation: event.Recommendation,
			EvidenceRef:    event.EvidenceRef,
			NextSafeAction: event.NextSafeAction,
			NextCommand:    event.NextCommand,
		})
	}
	return normalizeLiveProviderSoakRecommendations(out)
}

func liveProviderSoakValidationResults(report LiveProviderDrillReport, ledger RuntimeGateLedger) []LiveProviderSoakValidationResult {
	status := "passed"
	if report.Status == liveProviderDrillStatusSkipped {
		status = "skipped"
	} else if ledger.Status == runtimeGateStatusBlocked || report.FinalGateStatus == runtimeGateStatusBlocked {
		status = "blocked"
	}
	return []LiveProviderSoakValidationResult{
		{Command: "/status", Status: status, Summary: "compact status exposes live-provider soak state", EvidenceRef: "status.txt#compact"},
		{Command: "/status detail", Status: status, Summary: "detail status exposes lifecycle, stale context, correction contract, route health, and next command", EvidenceRef: "status.txt#detail"},
		{Command: "kernforge_status", Status: status, Summary: "MCP status JSON exposes equivalent soak fields", EvidenceRef: "mcp_status.json"},
		{Command: "kernforge_review", Status: status, Summary: "MCP review JSON exposes equivalent soak fields", EvidenceRef: "mcp_review.json"},
		{Command: "runtime_gate_ledger", Status: ledger.Status, Summary: "runtime gate ledger persisted with soak state", EvidenceRef: "runtime_gate_ledger.json"},
		{Command: "markdown_artifact", Status: status, Summary: "Markdown soak report rendered from typed report", EvidenceRef: "soak_report.md"},
	}
}

func (rt *runtimeState) recordLiveProviderSoakReport(root string, report *LiveProviderDrillReport) RuntimeGateLedger {
	if rt == nil || rt.session == nil || report == nil {
		return RuntimeGateLedger{}
	}
	finalizeLiveProviderDrillGate(report)
	if rt.session.LastFinalAnswerCorrection == nil {
		correction := liveProviderSoakFinalAnswerCorrection(*report)
		rt.session.LastFinalAnswerCorrection = &correction
	}
	if rt.session.LastFinalAnswerCorrection != nil {
		correction := *rt.session.LastFinalAnswerCorrection
		correction.Normalize()
		report.FinalAnswerCorrection = &correction
	}
	copyReport := *report
	copyReport.Normalize()
	rt.session.LastLiveProviderDrill = &copyReport
	ledger := buildRuntimeGateLedger(root, rt.session, runtimeGateActionFinalAnswer)
	if ledger.StaleContextSummary != nil {
		stale := *ledger.StaleContextSummary
		stale.Normalize()
		report.StaleContextSummary = &stale
	}
	if ledger.FinalAnswerCorrection != nil {
		correction := *ledger.FinalAnswerCorrection
		correction.Normalize()
		report.FinalAnswerCorrection = &correction
	}
	report.NextRecommendedCommand = firstNonBlankString(report.NextRecommendedCommand, runtimeGatePrimaryNextCommand(ledger), "/status detail")
	copyReport = *report
	copyReport.Normalize()
	rt.session.LastLiveProviderDrill = &copyReport
	ledger = buildRuntimeGateLedger(root, rt.session, runtimeGateActionFinalAnswer)
	rt.session.RuntimeGateLedger = &ledger
	if rt.store != nil {
		_ = rt.store.Save(rt.session)
	}
	return ledger
}

func liveProviderSoakFinalAnswerCorrection(report LiveProviderDrillReport) FinalAnswerCorrectionVisibility {
	correction := FinalAnswerCorrectionVisibility{
		Required:    true,
		Corrected:   !liveProviderDrillBlocksFinalization(&report),
		Rejected:    liveProviderDrillBlocksFinalization(&report),
		Source:      "live_provider_soak",
		Reasons:     []string{"review_self_review_disclosure", "validation_disclosure", "remaining_risk_disclosure"},
		RecordedAt:  time.Now(),
		MaxAttempts: finalAnswerCorrectionDefaultMaxAttempts,
	}
	if correction.Rejected {
		correction.AttemptCount = correction.MaxAttempts
		correction.RejectedAt = time.Now()
	} else {
		correction.CorrectedAt = time.Now()
	}
	correction.Normalize()
	return correction
}

func runtimeGatePrimaryNextCommand(ledger RuntimeGateLedger) string {
	ledger.Normalize()
	if len(ledger.NextCommands) == 0 {
		return ""
	}
	return strings.TrimSpace(ledger.NextCommands[0].Command)
}

func (rt *runtimeState) simulateLiveProviderSoakReload(root string, report *LiveProviderDrillReport) *LiveProviderSoakReloadVerification {
	verification := &LiveProviderSoakReloadVerification{
		NextCommand: "/status detail",
	}
	if rt == nil || rt.store == nil || rt.session == nil {
		verification.Mismatches = append(verification.Mismatches, "session store unavailable")
		verification.Normalize()
		return verification
	}
	if err := rt.store.Save(rt.session); err != nil {
		verification.Mismatches = append(verification.Mismatches, "save failed: "+err.Error())
		verification.Normalize()
		return verification
	}
	loaded, err := rt.store.Load(rt.session.ID)
	if err != nil {
		verification.Mismatches = append(verification.Mismatches, "load failed: "+err.Error())
		verification.Normalize()
		return verification
	}
	rt.session = loaded
	verification.Reloaded = true
	ledger := rt.runtimeGateLedgerForStatus(runtimeGateActionFinalAnswer)
	compactStatus := rt.renderLiveProviderSoakStatusCommand(false)
	detailStatus := rt.renderLiveProviderSoakStatusCommand(true)
	server := &kernforgeMCPServer{rt: rt}
	mcpStatus := server.buildMCPStatusPayload(context.Background(), map[string]any{"detail": true})
	mcpReview := liveProviderSoakMCPReviewPayload(*report, ledger)
	markdown := renderLiveProviderDrillMarkdown(report)
	verification.StatusCompact = liveProviderSoakSurfaceHasCoreState(compactStatus)
	verification.StatusDetail = verification.StatusCompact && strings.Contains(detailStatus, "final_answer_correction_contract")
	verification.MCPStatusJSON = liveProviderSoakPayloadHasCoreState(mcpStatus)
	verification.MCPReviewJSON = liveProviderSoakPayloadHasCoreState(mcpReview)
	verification.MarkdownArtifact = strings.Contains(markdown, "Live Provider Drill") && strings.Contains(markdown, "live_provider_drill")
	verification.RuntimeGateLedger = ledger.LiveProviderDrill != nil && strings.EqualFold(ledger.LiveProviderDrill.ID, report.ID)
	if verification.StatusCompact {
		verification.MatchingFields = append(verification.MatchingFields, "status_compact.live_provider_drill")
	} else {
		verification.Mismatches = append(verification.Mismatches, "status compact missing soak fields")
	}
	if verification.StatusDetail {
		verification.MatchingFields = append(verification.MatchingFields, "status_detail.final_answer_correction_contract")
	} else {
		verification.Mismatches = append(verification.Mismatches, "status detail missing correction contract")
	}
	if verification.MCPStatusJSON {
		verification.MatchingFields = append(verification.MatchingFields, "mcp_status.live_provider_drill")
	} else {
		verification.Mismatches = append(verification.Mismatches, "MCP status missing soak fields")
	}
	if verification.MCPReviewJSON {
		verification.MatchingFields = append(verification.MatchingFields, "mcp_review.live_provider_drill")
	} else {
		verification.Mismatches = append(verification.Mismatches, "MCP review missing soak fields")
	}
	if verification.RuntimeGateLedger {
		verification.MatchingFields = append(verification.MatchingFields, "runtime_gate_ledger.live_provider_drill")
	} else {
		verification.Mismatches = append(verification.Mismatches, "runtime gate ledger missing soak report")
	}
	if ledger.LiveProviderDrill != nil {
		verification.NextCommand = firstNonBlankString(ledger.LiveProviderDrill.NextRecommendedCommand, runtimeGatePrimaryNextCommand(ledger), "/status detail")
	}
	verification.Normalize()
	return verification
}

func liveProviderSoakSurfaceHasCoreState(text string) bool {
	return strings.Contains(text, "live_provider_drill") &&
		strings.Contains(text, "final_answer_correction") &&
		strings.Contains(text, "stale_context")
}

func liveProviderSoakPayloadHasCoreState(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	if _, ok := payload["live_provider_drill"]; !ok {
		return false
	}
	if _, ok := payload["final_answer_correction"]; !ok {
		return false
	}
	if _, ok := payload["stale_context_summary"]; !ok {
		return false
	}
	if _, ok := payload["route_health_events"]; !ok {
		return false
	}
	return true
}

func (rt *runtimeState) renderLiveProviderSoakStatusText() string {
	var b strings.Builder
	b.WriteString("# /status\n\n")
	b.WriteString(rt.renderLiveProviderSoakStatusCommand(false))
	b.WriteString("\n\n# /status detail\n\n")
	b.WriteString(rt.renderLiveProviderSoakStatusCommand(true))
	return b.String()
}

func (rt *runtimeState) renderLiveProviderSoakStatusCommand(detail bool) string {
	if rt == nil {
		return ""
	}
	var out bytes.Buffer
	copyRT := *rt
	copyRT.writer = &out
	if detail {
		copyRT.printRuntimeGateStatusDetail(runtimeGateActionFinalAnswer)
	} else {
		copyRT.printRuntimeGateStatus(runtimeGateActionFinalAnswer)
	}
	return out.String()
}

func writeLiveProviderSoakArtifacts(report LiveProviderDrillReport, ledger RuntimeGateLedger, mcpStatus map[string]any, mcpReview map[string]any, statusText string) error {
	report.Normalize()
	if report.ArtifactDir == "" {
		return fmt.Errorf("soak artifact dir is not configured")
	}
	reportJSON, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	ledgerJSON, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	mcpStatusJSON, err := json.MarshalIndent(mcpStatus, "", "  ")
	if err != nil {
		return err
	}
	mcpReviewJSON, err := json.MarshalIndent(mcpReview, "", "  ")
	if err != nil {
		return err
	}
	files := map[string][]byte{
		filepath.Join(report.ArtifactDir, "soak_report.json"):         reportJSON,
		filepath.Join(report.ArtifactDir, "soak_report.md"):           []byte(renderLiveProviderDrillMarkdown(&report)),
		filepath.Join(report.ArtifactDir, "runtime_gate_ledger.json"): ledgerJSON,
		filepath.Join(report.ArtifactDir, "mcp_status.json"):          mcpStatusJSON,
		filepath.Join(report.ArtifactDir, "mcp_review.json"):          mcpReviewJSON,
		filepath.Join(report.ArtifactDir, "status.txt"):               []byte(statusText),
	}
	for path, data := range files {
		if err := atomicWriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func liveProviderSoakArtifactDir(root string, started time.Time) string {
	return filepath.Join(root, userConfigDirName, "soak", started.Format("20060102-150405.000"))
}

func liveProviderSoakMCPReviewPayload(report LiveProviderDrillReport, ledger RuntimeGateLedger) map[string]any {
	report.Normalize()
	ledger.Normalize()
	return map[string]any{
		"summary":                      report.Summary,
		"review_id":                    report.ID,
		"machine_status":               ledger.Status,
		"status_code":                  0,
		"request_class":                "live_provider_soak",
		"lifecycle_kind":               reviewLifecycleKindMixedFlow,
		"route_quality":                liveProviderSoakRouteQualitySummary(report),
		"final_answer_contract_status": report.FinalAnswerCorrection,
		"final_answer_correction":      report.FinalAnswerCorrection,
		"stale_context_summary":        report.StaleContextSummary,
		"route_health_events":          report.RouteHealthEvents,
		"live_provider_drill":          report,
		"next_recommended_command":     report.NextRecommendedCommand,
		"runtime_gate_ledger":          ledger,
		"artifact_refs": []string{
			filepath.ToSlash(filepath.Join(report.ArtifactDir, "soak_report.json")),
			filepath.ToSlash(filepath.Join(report.ArtifactDir, "soak_report.md")),
			filepath.ToSlash(filepath.Join(report.ArtifactDir, "runtime_gate_ledger.json")),
			filepath.ToSlash(filepath.Join(report.ArtifactDir, "mcp_status.json")),
		},
	}
}

func liveProviderSoakRouteQualitySummary(report LiveProviderDrillReport) ReviewRouteQualitySummary {
	report.Normalize()
	summary := ReviewRouteQualitySummary{
		ReviewerRuns:   len(report.Turns),
		HealthEvents:   report.RouteHealthEvents,
		FailureClasses: reviewRouteHealthEventClasses(report.RouteHealthEvents),
	}
	for _, turn := range report.Turns {
		summary.RetryCount += turn.RetryCount
		summary.MalformedOutputs += turn.MalformedOutputCount
		if turn.WeakOutputDegraded {
			summary.WeakOutputs++
		}
		if turn.FinalGateState == runtimeGateStatusBlocked {
			summary.FailedOutputs++
		}
	}
	for _, event := range report.RouteHealthEvents {
		if event.Recommendation != "" {
			summary.Recommendations = append(summary.Recommendations, event.Recommendation)
		}
	}
	summary.FailureClasses = normalizeTaskStateList(summary.FailureClasses, 8)
	summary.Recommendations = normalizeTaskStateList(summary.Recommendations, 8)
	summary.Degraded = len(summary.HealthEvents) > 0 || summary.WeakOutputs > 0 || summary.FailedOutputs > 0
	switch {
	case report.Status == liveProviderDrillStatusSkipped:
		summary.Status = "skipped"
	case summary.FailedOutputs > 0 || report.FinalGateStatus == runtimeGateStatusBlocked:
		summary.Status = "failed"
	case summary.Degraded:
		summary.Status = "degraded"
	case containsString(report.RouteModes, reviewRouteModeSingleModel) && !containsString(report.RouteModes, reviewRouteModeCrossModel):
		summary.Status = "single_model"
	default:
		summary.Status = "healthy"
	}
	return summary
}

func liveProviderDrillWorkflowClassesFromTurns(turns []LiveProviderDrillTurn) []string {
	classes := make([]string, 0, len(turns))
	for _, turn := range turns {
		classes = append(classes, turn.LifecycleKind)
	}
	return normalizeTaskStateList(classes, 16)
}

func liveProviderDrillRouteModesFromTurns(turns []LiveProviderDrillTurn) []string {
	modes := make([]string, 0, len(turns))
	for _, turn := range turns {
		modes = append(modes, turn.RouteMode)
	}
	return normalizeTaskStateList(modes, 8)
}

func limitLiveProviderSoakTurns(turns []LiveProviderDrillTurn, limit int) []LiveProviderDrillTurn {
	if limit <= 0 || limit >= len(turns) {
		return turns
	}
	return append([]LiveProviderDrillTurn(nil), turns[:limit]...)
}

func (s *kernforgeMCPServer) buildMCPStatusPayload(ctx context.Context, args map[string]any) map[string]any {
	root := s.workspaceRoot()
	changed, _ := gitChangedFiles(ctx, root)
	branch, branchErr := gitCurrentBranch(ctx, root)
	if branchErr != nil {
		branch = ""
	}
	evidenceStats, _ := s.rt.evidence.Stats()
	verificationCount := s.rt.verificationHistoryCount()
	latest := s.latestAnalysisSummary()
	functionFuzzRuns := 0
	if s.rt.functionFuzz != nil {
		count, _, err := s.rt.functionFuzz.Stats(root)
		if err == nil {
			functionFuzzRuns = count
		}
	}
	fuzzCampaigns := 0
	if s.rt.fuzzCampaigns != nil {
		items, err := s.rt.fuzzCampaigns.ListRecent(root, 1000)
		if err == nil {
			fuzzCampaigns = len(items)
		}
	}
	ledger := s.rt.runtimeGateLedgerForStatus(runtimeGateActionFinalAnswer)
	var report *CodingHarnessReport
	if s.rt.session != nil && s.rt.session.LastCodingHarnessReport != nil {
		report = s.rt.session.LastCodingHarnessReport
	}
	compact := buildReviewCompactStatus(nil, &ledger, report)
	blockers := buildReviewBlockerSummary(nil, &ledger, report)
	timeline := reviewLifecycleTimelineForRuntimeGate(s.rt.session, ledger.Action, ledger.ChangedPaths, &ledger, report)
	contract := reviewFinalAnswerContractStatusForClass(firstNonBlankString(ledger.RequestClass, compactRequestClass(compact)), s.rt.session, report, "")
	if ledger.ReviewObservability != nil {
		if ledger.ReviewObservability.CompactStatus != nil {
			compact = ledger.ReviewObservability.CompactStatus
		}
		if ledger.ReviewObservability.BlockerSummary != nil {
			blockers = ledger.ReviewObservability.BlockerSummary
		}
		if len(ledger.ReviewObservability.LifecycleTimeline) > 0 {
			timeline = ledger.ReviewObservability.LifecycleTimeline
		}
		if ledger.ReviewObservability.FinalAnswerContract != nil {
			contract = ledger.ReviewObservability.FinalAnswerContract
		}
	}
	nextRecommended := map[string]any(nil)
	if len(ledger.NextCommands) > 0 {
		next := ledger.NextCommands[0]
		nextRecommended = map[string]any{
			"command":               next.Command,
			"reason":                next.Reason,
			"when":                  next.When,
			"safety":                next.Safety,
			"auto_run":              next.AutoRun,
			"requires_confirmation": next.RequiresConfirmation,
			"client_hint":           next.ClientHint,
			"expected_result":       next.ExpectedResult,
		}
	}
	status := map[string]any{
		"version":                      currentVersion(),
		"workspace":                    root,
		"mcp_workspace_source":         valueOrUnset(s.workspaceSource),
		"session_id":                   s.rt.session.ID,
		"provider":                     valueOrUnset(s.rt.cfg.Provider),
		"model":                        valueOrUnset(s.rt.cfg.Model),
		"provider_ready":               s.rt.agent != nil && s.rt.agent.Client != nil,
		"provider_error":               "",
		"git_branch":                   branch,
		"git_changed_files":            changed,
		"evidence_count":               evidenceStats.Count,
		"verification_reports":         verificationCount,
		"function_fuzz_runs":           functionFuzzRuns,
		"fuzz_campaigns":               fuzzCampaigns,
		"latest_analysis":              latest,
		"lifecycle_kind":               reviewCompactLifecycleKind(compact),
		"lifecycle_timeline":           timeline,
		"compact_status":               compact,
		"blocker_summary":              blockers,
		"route_quality":                nil,
		"final_answer_contract_status": contract,
		"final_answer_correction":      ledger.FinalAnswerCorrection,
		"stale_context_summary":        ledger.StaleContextSummary,
		"route_health_events":          ledger.RouteHealthEvents,
		"live_provider_drill":          ledger.LiveProviderDrill,
		"next_recommended_command":     nextRecommended,
	}
	if ledger.ReviewObservability != nil {
		status["route_quality"] = ledger.ReviewObservability.RouteQuality
	}
	if !boolValue(args, "detail", false) {
		delete(status, "lifecycle_timeline")
		if blockers != nil {
			compactBlockers := *blockers
			compactBlockers.SecondaryWarnings = nil
			status["blocker_summary"] = &compactBlockers
		}
	}
	if s.rt.clientErr != nil {
		status["provider_error"] = s.rt.clientErr.Error()
	}
	return status
}
