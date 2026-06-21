package main

// review_second_opinion is a model-callable, read-only second-opinion review
// tool. The working model can call it mid-turn with a request (plus optional
// code snippet and focus paths) and immediately receive a list of structured
// ReviewFinding objects (NOT a full ReviewRun artifact) so it can iterate inside
// the same turn.
//
// Independence and honesty (the load-bearing safety property of this tool):
//   - The second opinion runs through the configured reviewer route when one is
//     genuinely distinct from the working client. In that case the result is
//     labeled independent.
//   - When the only available reviewer route IS the working client (or the
//     configured reviewer resolves to the same provider client + model), the
//     tool does NOT silently present same-model output as independent
//     corroboration. It runs with a distinct adversarial guest persona and a
//     bumped reasoning effort, labels the result same_model, and emits one
//     warning per session.
//   - When no model route is available at all, the tool degrades to a
//     deterministic-only second opinion (no model call, no crash) and labels the
//     result deterministic_only.
//
// The reviewer role is guest_reviewer, deliberately distinct from
// primary_reviewer/cross_reviewer/single-model-second-pass so a second opinion
// can never be confused with the gate's own review passes.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// reviewSecondOpinionIndependence labels how independent the returned findings
// are. It is surfaced both in the JSON payload and in the tool result meta so a
// same-model second opinion is never read as independent corroboration.
const (
	reviewSecondOpinionIndependent       = "independent"
	reviewSecondOpinionSameModel         = "same_model"
	reviewSecondOpinionDeterministicOnly = "deterministic_only"
)

const reviewSecondOpinionKind = "second_opinion"

// reviewSecondOpinionMaxRequestChars / reviewSecondOpinionMaxSnippetChars bound
// the model-supplied request and code snippet so a single tool call cannot blow
// past the compact evidence budget regardless of what the model sends.
const (
	reviewSecondOpinionMaxRequestChars = 8000
	reviewSecondOpinionMaxSnippetChars = 24000
	reviewSecondOpinionMaxFocusPaths   = 32
)

// reviewSecondOpinionSoftTimeout bounds the single bounded reviewer call. It is
// smaller than the full review main timeout because a second opinion is meant to
// be a quick mid-turn check, not a full gate review.
const reviewSecondOpinionSoftTimeout = 4 * time.Minute

// ReviewSecondOpinionTool implements the review_second_opinion tool. It captures
// the Workspace (for the lazy agent resolver and progress) but resolves the
// running agent only at execution time, because the tool registry is built
// before the agent exists.
type ReviewSecondOpinionTool struct {
	ws Workspace
}

func NewReviewSecondOpinionTool(ws Workspace) *ReviewSecondOpinionTool {
	return &ReviewSecondOpinionTool{ws: ws}
}

// ReadOnlyToolCall marks the second opinion as non-mutating: it only reads
// supplied evidence and queries a reviewer model. No edit gate is required.
func (t *ReviewSecondOpinionTool) ReadOnlyToolCall() bool {
	return true
}

// SupportsParallelToolCalls allows the model to request a second opinion
// alongside other read-only lookups; each call is independently bounded.
func (t *ReviewSecondOpinionTool) SupportsParallelToolCalls() bool {
	return true
}

func (t *ReviewSecondOpinionTool) hookWorkspace() Workspace {
	return t.ws
}

func (t *ReviewSecondOpinionTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "review_second_opinion",
		Description: "Request a bounded, read-only second-opinion code review mid-turn and get structured findings back immediately so you can iterate.\n" +
			"It runs one bounded reviewer pass over the supplied request and optional code snippet / focus paths and returns a findings list (not a full review artifact).\n" +
			"Independence is enforced and labeled: when a distinct reviewer route is configured the result is independent; when the only route is the working model it runs an adversarial same-model pass labeled same_model (never independent corroboration); when no model is available it returns a deterministic_only second opinion.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"request": map[string]any{
					"type":        "string",
					"description": "Required. What you want a second opinion on (the concern, the change, or the question to review).",
				},
				"code_snippet": map[string]any{
					"type":        "string",
					"description": "Optional code/diff snippet to review. Keep it focused; it is truncated to the compact evidence budget.",
				},
				"focus_paths": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional list of workspace paths the reviewer should focus on.",
				},
				"mode": map[string]any{
					"type":        "string",
					"enum":        []string{"quick", "thorough"},
					"description": "Optional review depth. quick (default) uses a smaller evidence budget; thorough allows a slightly larger budget.",
				},
			},
			"required": []string{"request"},
		},
	}
}

func (t *ReviewSecondOpinionTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

// reviewSecondOpinionResponse is the JSON payload returned to the model. It is a
// findings list plus the honesty/independence labeling, never a full ReviewRun.
type reviewSecondOpinionResponse struct {
	Mode         string          `json:"mode"`
	Role         string          `json:"role"`
	Independence string          `json:"independence"`
	Model        string          `json:"model,omitempty"`
	Status       string          `json:"status"`
	Summary      string          `json:"summary"`
	Findings     []ReviewFinding `json:"findings"`
	Note         string          `json:"note,omitempty"`
	Degraded     bool            `json:"degraded,omitempty"`
}

func (t *ReviewSecondOpinionTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	request := strings.TrimSpace(stringValue(args, "request"))
	if request == "" {
		return ToolExecutionResult{}, fmt.Errorf("review_second_opinion requires a non-empty request")
	}
	if len([]rune(request)) > reviewSecondOpinionMaxRequestChars {
		request = compactPromptSection(request, reviewSecondOpinionMaxRequestChars)
	}
	mode := strings.ToLower(strings.TrimSpace(stringValue(args, "mode")))
	if mode != "thorough" {
		mode = "quick"
	}
	codeSnippet := compactPromptSection(stringValue(args, "code_snippet"), reviewSecondOpinionMaxSnippetChars)
	focusPaths := normalizeTaskStateList(stringSliceValue(args, "focus_paths"), reviewSecondOpinionMaxFocusPaths)

	agent := t.resolveAgent()
	rt := reviewSecondOpinionRuntime(agent)

	// Resolve the reviewer route and decide whether it is genuinely independent
	// of the working client. This is the honesty gate: it is the only place that
	// decides between independent / same_model / deterministic_only.
	client, model, label, independence := reviewSecondOpinionRoute(agent)

	run := buildReviewSecondOpinionRun(rt, request, codeSnippet, focusPaths, mode)
	response := reviewSecondOpinionResponse{
		Mode:         mode,
		Role:         guestReviewerRole,
		Independence: independence,
		Model:        label,
	}

	if independence == reviewSecondOpinionDeterministicOnly || rt == nil || client == nil || strings.TrimSpace(model) == "" {
		// Deterministic-only: no model is available. Return the deterministic
		// findings the harness already derives, with no model call and no crash.
		response.Independence = reviewSecondOpinionDeterministicOnly
		response.Status = "deterministic_only"
		response.Degraded = true
		response.Findings = reviewSecondOpinionDeterministicFindings(rt, run)
		response.Summary = localizedText(reviewSecondOpinionConfig(rt),
			"No reviewer model is available; returning a deterministic-only second opinion (not an independent model review).",
			"리뷰 모델을 사용할 수 없어 결정론적(모델 없는) 2차 의견만 반환합니다. 독립 모델 리뷰가 아닙니다.")
		response.Note = response.Summary
		assignReviewFindingIDs(response.Findings)
		return reviewSecondOpinionResult(response), nil
	}

	if independence == reviewSecondOpinionSameModel {
		// Same-model path: never present as independent. Warn once per session and
		// run an adversarial guest persona at a bumped effort so it is not a
		// verbatim re-ask of the working model.
		reviewSecondOpinionWarnSameModelOnce(agent, label)
		response.Note = localizedText(reviewSecondOpinionConfig(rt),
			"Second opinion used the SAME model as the working client; it is an adversarial same-model pass, not independent corroboration.",
			"2차 의견이 작업 모델과 동일한 모델을 사용했습니다. 적대적 동일 모델 검토이며 독립적 교차 검증이 아닙니다.")
	}

	prompt := buildReviewModelPrompt(rt.cfg, *run, guestReviewerRole)
	findings, reviewerRun, _ := executeSingleReviewModelRun(ctx, rt, reviewSecondOpinionRoot(rt), run, client, model, label, guestReviewerRole, reviewSecondOpinionKind, prompt, nil, reviewModelRunPeerContext{})
	assignReviewFindingIDs(findings)
	response.Findings = findings
	response.Status = strings.TrimSpace(reviewerRun.Status)
	if response.Status == "" {
		response.Status = "completed"
	}
	response.Model = firstNonBlankString(reviewerRun.Model, label)
	if !strings.EqualFold(reviewerRun.Status, "completed") {
		// A failed reviewer run degrades to deterministic findings rather than
		// returning nothing, and is honestly labeled degraded.
		response.Degraded = true
		response.Findings = append(response.Findings, reviewSecondOpinionDeterministicFindings(rt, run)...)
		assignReviewFindingIDs(response.Findings)
		if strings.TrimSpace(reviewerRun.Error) != "" {
			response.Note = strings.TrimSpace(firstNonBlankString(response.Note, "reviewer run did not complete: "+compactPromptSection(reviewerRun.Error, 200)))
		}
	}
	if reviewModelQualityWeakOrFailed(reviewerRun.ModelQuality) {
		response.Degraded = true
	}
	response.Summary = reviewSecondOpinionSummary(reviewSecondOpinionConfig(rt), response)
	return reviewSecondOpinionResult(response), nil
}

func reviewModelQualityWeakOrFailed(quality string) bool {
	switch strings.TrimSpace(quality) {
	case reviewModelQualityWeak, reviewModelQualityFailed:
		return true
	default:
		return false
	}
}

// resolveAgent returns the running agent via the Workspace resolver seam, or nil
// when no agent is wired (deterministic-only path). The resolver is installed by
// the runtime after the agent is constructed; in tests it can be installed
// directly on the Workspace.
func (t *ReviewSecondOpinionTool) resolveAgent() *Agent {
	if t.ws.ResolveAgent == nil {
		return nil
	}
	return t.ws.ResolveAgent()
}

// reviewSecondOpinionConfig returns the config to use for localization and
// budgets, defaulting safely when no runtime is available.
func reviewSecondOpinionConfig(rt *runtimeState) Config {
	if rt == nil {
		return Config{}
	}
	return rt.cfg
}

// reviewSecondOpinionRuntime builds the minimal runtimeState the review model
// path needs (cfg, agent, session). executeSingleReviewModelRun only reads
// rt.agent, rt.cfg, and rt.session, so this minimal shape is sufficient and
// avoids constructing the full interactive runtime. Returns nil when no agent is
// available so callers fall back to the deterministic-only path.
func reviewSecondOpinionRuntime(agent *Agent) *runtimeState {
	if agent == nil {
		return nil
	}
	return &runtimeState{
		cfg:     agent.Config,
		agent:   agent,
		session: agent.Session,
	}
}

func reviewSecondOpinionRoot(rt *runtimeState) string {
	if rt == nil || rt.agent == nil {
		return ""
	}
	root := strings.TrimSpace(rt.agent.Workspace.Root)
	if root == "" {
		root = strings.TrimSpace(rt.agent.Workspace.BaseRoot)
	}
	return root
}

// reviewSecondOpinionRoute resolves the reviewer client/model/label and the
// independence label for the second opinion. It enforces the honesty rule:
//   - A configured reviewer route that is genuinely distinct from the working
//     client is independent.
//   - A configured reviewer route that resolves to the SAME provider client and
//     model as the working client, or no reviewer route at all but a usable
//     working client, is same_model.
//   - No usable client at all is deterministic_only.
func reviewSecondOpinionRoute(agent *Agent) (ProviderClient, string, string, string) {
	if agent == nil {
		return nil, "", "", reviewSecondOpinionDeterministicOnly
	}
	// Prefer a dedicated, distinct reviewer route.
	if agent.ReviewerClient != nil && strings.TrimSpace(agent.ReviewerModel) != "" {
		if !reviewSecondOpinionSameAsWorking(agent, agent.ReviewerClient, agent.ReviewerModel) {
			label := formatProviderModelEffortLabel(agent.Config.Provider, agent.ReviewerModel, agent.Config.ReasoningEffort)
			return agent.ReviewerClient, agent.ReviewerModel, label, reviewSecondOpinionIndependent
		}
	}
	// An aux reviewer route is also acceptable when distinct.
	if agent.AuxReviewerClient != nil && strings.TrimSpace(agent.AuxReviewerModel) != "" {
		if !reviewSecondOpinionSameAsWorking(agent, agent.AuxReviewerClient, agent.AuxReviewerModel) {
			label := formatProviderModelEffortLabel(agent.Config.Provider, agent.AuxReviewerModel, agent.Config.ReasoningEffort)
			return agent.AuxReviewerClient, agent.AuxReviewerModel, label, reviewSecondOpinionIndependent
		}
	}
	// Fall back to the working client, honestly labeled same_model with a
	// distinct adversarial guest persona and a bumped reasoning effort so it is
	// not a verbatim re-ask. Never independent.
	if agent.Client != nil && reviewMainModelRouteConfigured(agent.Config) {
		label := reviewSecondOpinionSameModelLabel(agent.Config)
		return agent.Client, agent.Config.Model, label, reviewSecondOpinionSameModel
	}
	return nil, "", "", reviewSecondOpinionDeterministicOnly
}

// reviewSecondOpinionSameAsWorking reports whether the candidate reviewer route
// is effectively the same as the working client (same provider client instance
// and same model). It reuses the review harness route-equality primitive so the
// same-model detection matches the gate's own independence checks.
func reviewSecondOpinionSameAsWorking(agent *Agent, client ProviderClient, model string) bool {
	if agent == nil || client == nil {
		return false
	}
	if agent.Client == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(model), strings.TrimSpace(agent.Config.Model)) {
		return false
	}
	return sameProviderClient(client, agent.Client)
}

// reviewSecondOpinionSameModelLabel builds a label for the same-model path that
// bumps the reasoning effort one step (when the provider supports it) so the
// guest pass is a distinct persona/effort rather than a verbatim re-ask, and so
// the label honestly reflects the same_model nature.
func reviewSecondOpinionSameModelLabel(cfg Config) string {
	effort := cfg.ReasoningEffort
	if providerSupportsReasoningEffort(cfg.Provider) {
		effort = reasoningEffortAtLeast(firstNonBlankString(effort, "medium"), "high")
	}
	return formatProviderModelEffortLabel(cfg.Provider, cfg.Model, effort) + " (same-model guest)"
}

// reviewSecondOpinionWarnSameModelOnce emits exactly one warning per session
// when a same-model second opinion is produced, so the operator learns the
// second opinion is not independent. Subsequent same-model second opinions in
// the same session do not repeat the warning.
func reviewSecondOpinionWarnSameModelOnce(agent *Agent, label string) {
	if agent == nil || agent.Session == nil {
		return
	}
	if agent.Session.SecondOpinionSameModelWarned {
		return
	}
	agent.Session.SecondOpinionSameModelWarned = true
	if agent.EmitProgress == nil {
		return
	}
	agent.EmitProgress(fmt.Sprintf(
		localizedText(agent.Config,
			"Second opinion uses the same model as the working client (%s); it runs as an adversarial same-model pass and is NOT independent corroboration.",
			"2차 의견이 작업 모델과 동일한 모델(%s)을 사용합니다. 적대적 동일 모델 검토로 실행되며 독립적 교차 검증이 아닙니다."),
		valueOrDefault(strings.TrimSpace(label), "the working model")))
}

// buildReviewSecondOpinionRun assembles the minimal ReviewRun the bounded model
// pass needs. It places the request, optional snippet, and focus paths into the
// evidence text, pre-truncated to the compact mode budget so the prompt stays
// small regardless of the harness default evidence limit. A guest_reviewer model
// plan is set so the role is satisfied and the correctness lens is applied.
func buildReviewSecondOpinionRun(rt *runtimeState, request string, codeSnippet string, focusPaths []string, mode string) *ReviewRun {
	now := time.Now()
	cfg := reviewSecondOpinionConfig(rt)
	var b strings.Builder
	b.WriteString("Second-opinion request:\n")
	b.WriteString(request)
	b.WriteString("\n")
	if len(focusPaths) > 0 {
		b.WriteString("\nFocus paths:\n- ")
		b.WriteString(strings.Join(focusPaths, "\n- "))
		b.WriteString("\n")
	}
	if strings.TrimSpace(codeSnippet) != "" {
		b.WriteString("\nCode snippet under review:\n")
		b.WriteString(codeSnippet)
		b.WriteString("\n")
	}
	evidenceText, redaction := redactSensitiveText(b.String())
	evidenceText = compactReviewPromptSection(evidenceText, reviewSecondOpinionEvidenceLimit(mode))

	run := &ReviewRun{
		ID:            fmt.Sprintf("second-opinion-%s", now.Format("20060102-150405.000")),
		SchemaVersion: reviewSchemaVersion,
		Trigger:       "second_opinion",
		Target:        reviewTargetSourceAnalysis,
		Mode:          reviewModeGeneralChange,
		Flow:          "second_opinion",
		Objective:     request,
		CreatedAt:     now,
		Redaction:     redaction,
		ChangeSet: ReviewChangeSet{
			ChangedPaths: focusPaths,
		},
		Evidence: ReviewEvidencePack{
			Sources: focusPaths,
			Text:    evidenceText,
		},
		Profiles: []string{formatProviderModelEffortLabel(cfg.Provider, cfg.Model, cfg.ReasoningEffort)},
		Result: ReviewResult{
			ModelQuality: reviewModelQualityUsable,
		},
	}
	run.ModelPlan = ReviewModelPlan{
		Strategy:       "single",
		RequiredRoles:  []string{guestReviewerRole},
		RequiredLenses: []string{"correctness"},
		AssignedModels: map[string]string{},
	}
	if reviewRunSecuritySensitive(*run) {
		run.ModelPlan.RequiredLenses = append(run.ModelPlan.RequiredLenses, "security")
	}
	return run
}

// reviewSecondOpinionDeterministicFindings returns the deterministic findings
// the harness already derives for the run, used for the deterministic-only path
// and as a fallback when the model pass fails. It never fabricates findings; an
// empty list is a valid (clean) second opinion.
func reviewSecondOpinionDeterministicFindings(rt *runtimeState, run *ReviewRun) []ReviewFinding {
	if rt == nil || run == nil {
		return nil
	}
	findings := deterministicReviewFindings(rt, *run)
	for i := range findings {
		findings[i].ReviewerRole = guestReviewerRole
	}
	return findings
}

func reviewSecondOpinionSummary(cfg Config, response reviewSecondOpinionResponse) string {
	korean := localePrefersKorean(cfg)
	switch response.Independence {
	case reviewSecondOpinionIndependent:
		if korean {
			return fmt.Sprintf("독립 리뷰 모델 2차 의견: finding %d건.", len(response.Findings))
		}
		return fmt.Sprintf("Independent reviewer second opinion: %d finding(s).", len(response.Findings))
	case reviewSecondOpinionSameModel:
		if korean {
			return fmt.Sprintf("동일 모델 적대적 2차 의견(독립 아님): finding %d건.", len(response.Findings))
		}
		return fmt.Sprintf("Same-model adversarial second opinion (not independent): %d finding(s).", len(response.Findings))
	default:
		if korean {
			return fmt.Sprintf("결정론적 2차 의견: finding %d건.", len(response.Findings))
		}
		return fmt.Sprintf("Deterministic-only second opinion: %d finding(s).", len(response.Findings))
	}
}

func reviewSecondOpinionResult(response reviewSecondOpinionResponse) ToolExecutionResult {
	if response.Findings == nil {
		response.Findings = []ReviewFinding{}
	}
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return ToolExecutionResult{
			DisplayText: err.Error(),
			Meta:        map[string]any{"review_second_opinion_error": err.Error()},
		}
	}
	return ToolExecutionResult{
		DisplayText: string(data),
		ModelText:   string(data),
		Meta: map[string]any{
			"role":          guestReviewerRole,
			"independence":  response.Independence,
			"mode":          response.Mode,
			"finding_count": len(response.Findings),
			"degraded":      response.Degraded,
		},
	}
}
