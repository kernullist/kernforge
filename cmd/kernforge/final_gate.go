package main

import (
	"strings"
	"time"
)

type FinalGateState string

const (
	FinalGateReady                 FinalGateState = "Ready"
	FinalGateNeedsVerification     FinalGateState = "NeedsVerification"
	FinalGateNeedsReview           FinalGateState = "NeedsReview"
	FinalGateNeedsRecovery         FinalGateState = "NeedsRecovery"
	FinalGateBlocked               FinalGateState = "Blocked"
	FinalGateNeedsUserConfirmation FinalGateState = "NeedsUserConfirmation"
)

type FinalGateRuntimeSummary struct {
	State                   TurnRuntimeStateName  `json:"state,omitempty"`
	LastTransitionReason    string                `json:"last_transition_reason,omitempty"`
	UnresolvedInterventions []RuntimeIntervention `json:"unresolved_interventions,omitempty"`
}

type FinalGateVerificationResult struct {
	Present      bool     `json:"present"`
	Summary      string   `json:"summary,omitempty"`
	Passed       bool     `json:"passed"`
	Failed       bool     `json:"failed"`
	Skipped      bool     `json:"skipped"`
	Missing      bool     `json:"missing"`
	Unresolved   bool     `json:"unresolved"`
	ChangedPaths []string `json:"changed_paths,omitempty"`
}

type FinalGateReviewResult struct {
	Present             bool   `json:"present"`
	RunID               string `json:"run_id,omitempty"`
	Trigger             string `json:"trigger,omitempty"`
	Target              string `json:"target,omitempty"`
	RequestClass        string `json:"request_class,omitempty"`
	Verdict             string `json:"verdict,omitempty"`
	MachineStatus       string `json:"machine_status,omitempty"`
	GateAction          string `json:"gate_action,omitempty"`
	NeedsRevision       bool   `json:"needs_revision"`
	BlocksFinal         bool   `json:"blocks_final"`
	SingleModelMode     bool   `json:"single_model_mode"`
	NoCrossReviewReason string `json:"no_cross_review_reason,omitempty"`
}

type FinalGateGitMutationState struct {
	Requested         bool   `json:"requested"`
	AllowedByEnvelope bool   `json:"allowed_by_envelope"`
	MutationAttempted bool   `json:"mutation_attempted"`
	IdentityChecked   bool   `json:"identity_checked"`
	IdentityName      string `json:"identity_name,omitempty"`
	IdentityEmail     string `json:"identity_email,omitempty"`
	IdentityAllowed   bool   `json:"identity_allowed"`
	IdentityReason    string `json:"identity_reason,omitempty"`
}

type FinalGateInput struct {
	RequestEnvelope                RequestEnvelope             `json:"request_envelope"`
	Runtime                        FinalGateRuntimeSummary     `json:"runtime"`
	ChangedFiles                   []string                    `json:"changed_files,omitempty"`
	Verification                   FinalGateVerificationResult `json:"verification"`
	Review                         FinalGateReviewResult       `json:"review"`
	GitMutation                    FinalGateGitMutationState   `json:"git_mutation"`
	Reply                          string                      `json:"reply,omitempty"`
	AttemptedEditTool              bool                        `json:"attempted_edit_tool"`
	ExplicitEditRequest            bool                        `json:"explicit_edit_request"`
	GeneratedDocumentHarnessOwnsIt bool                        `json:"generated_document_harness_owns_it"`
	CreatedAt                      time.Time                   `json:"created_at,omitempty"`
}

type FinalGateDecision struct {
	State                FinalGateState        `json:"state"`
	Ready                bool                  `json:"ready"`
	Reasons              []string              `json:"reasons,omitempty"`
	Guidance             string                `json:"guidance,omitempty"`
	RuntimeState         TurnRuntimeStateName  `json:"runtime_state,omitempty"`
	ChangedFiles         []string              `json:"changed_files,omitempty"`
	VerificationSummary  string                `json:"verification_summary,omitempty"`
	ReviewRunID          string                `json:"review_run_id,omitempty"`
	ReviewGateAction     string                `json:"review_gate_action,omitempty"`
	NoCrossReviewReason  string                `json:"no_cross_review_reason,omitempty"`
	GitMutationRequested bool                  `json:"git_mutation_requested"`
	GitIdentityAllowed   bool                  `json:"git_identity_allowed,omitempty"`
	BlockedBy            []RuntimeIntervention `json:"blocked_by,omitempty"`
	CreatedAt            time.Time             `json:"created_at"`
}

func BuildFinalGateInput(root string, session *Session, envelope RequestEnvelope, turnRuntime *TurnRuntimeState, reply string, ctx TurnRuntimeFinalContext) FinalGateInput {
	envelope.Normalize()
	changedFiles := runtimeGateChangedPathsForAction(root, session, runtimeGateActionFinalAnswer)
	if len(changedFiles) == 0 && session != nil && session.LastVerification != nil {
		changedFiles = session.LastVerification.ChangedPaths
	}
	input := FinalGateInput{
		RequestEnvelope:                envelope,
		Runtime:                        finalGateRuntimeSummary(turnRuntime),
		ChangedFiles:                   normalizeTaskStateList(changedFiles, 128),
		Verification:                   finalGateVerificationResult(session, envelope, changedFiles),
		Review:                         finalGateReviewResult(session),
		GitMutation:                    finalGateGitMutationState(session, envelope),
		Reply:                          strings.TrimSpace(reply),
		AttemptedEditTool:              ctx.AttemptedEditTool,
		ExplicitEditRequest:            ctx.ExplicitEditRequest || envelope.ExplicitEditRequest,
		GeneratedDocumentHarnessOwnsIt: ctx.GeneratedDocumentHarnessOwnsIt,
		CreatedAt:                      time.Now(),
	}
	return input
}

func DecideFinalGate(input FinalGateInput) FinalGateDecision {
	input.RequestEnvelope.Normalize()
	input.ChangedFiles = normalizeTaskStateList(input.ChangedFiles, 128)
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now()
	}
	decision := FinalGateDecision{
		State:                FinalGateReady,
		Ready:                true,
		RuntimeState:         input.Runtime.State,
		ChangedFiles:         append([]string(nil), input.ChangedFiles...),
		VerificationSummary:  strings.TrimSpace(input.Verification.Summary),
		ReviewRunID:          strings.TrimSpace(input.Review.RunID),
		ReviewGateAction:     strings.TrimSpace(input.Review.GateAction),
		NoCrossReviewReason:  strings.TrimSpace(input.Review.NoCrossReviewReason),
		GitMutationRequested: input.GitMutation.Requested,
		GitIdentityAllowed:   input.GitMutation.IdentityAllowed,
		CreatedAt:            input.CreatedAt,
	}
	set := func(state FinalGateState, reason string, guidance string) FinalGateDecision {
		decision.State = state
		decision.Ready = false
		if strings.TrimSpace(reason) != "" {
			decision.Reasons = append(decision.Reasons, strings.TrimSpace(reason))
		}
		if strings.TrimSpace(decision.Guidance) == "" {
			decision.Guidance = strings.TrimSpace(guidance)
		}
		return decision
	}
	if input.Runtime.State == TurnRuntimeBlocked {
		return set(FinalGateBlocked, "turn runtime is blocked", "Report the blocker instead of presenting the work as complete.")
	}
	blockingInterventions := finalGateBlockingInterventions(input.Runtime.UnresolvedInterventions)
	if len(blockingInterventions) > 0 {
		decision.BlockedBy = blockingInterventions
		if finalGateOnlyVerificationInterventions(blockingInterventions) {
			return set(FinalGateNeedsVerification, "runtime has unresolved verification intervention", "Disclose the unresolved verification state or run the required verification before the final answer.")
		}
		return set(FinalGateNeedsRecovery, "runtime has unresolved recovery intervention", "Resolve the runtime intervention before producing the final answer.")
	}
	if input.GitMutation.MutationAttempted && !input.RequestEnvelope.AllowsGitMutation {
		return set(FinalGateNeedsUserConfirmation, "git mutation was attempted without an explicit user request", "Do not stage, commit, push, or open a PR until the user explicitly asks for that git action.")
	}
	if input.GitMutation.Requested && input.GitMutation.IdentityChecked && !input.GitMutation.IdentityAllowed {
		return set(FinalGateNeedsUserConfirmation, "git commit identity is not approved", "Configure git identity as kernullist <gloryo@naver.com> before creating a commit.")
	}
	// Unexpected file mutation guard. Any turn whose envelope does not allow file
	// mutation must NOT leak a workspace edit, regardless of PrimaryClass. This
	// covers question/research/git turns that the read-only boundary helper does
	// not classify. Harness-owned generated documents are exempt because the
	// document gate (not this guard) governs their writes.
	if !input.RequestEnvelope.AllowsFileMutation && !input.GeneratedDocumentHarnessOwnsIt {
		if input.AttemptedEditTool || len(input.ChangedFiles) > 0 {
			return set(FinalGateNeedsRecovery, "request does not allow file mutation but a workspace edit was attempted or produced", "Keep non-edit turns read-only unless the user explicitly asks for a file mutation.")
		}
	}
	if finalGateReadOnlyBoundary(input.RequestEnvelope) {
		if input.AttemptedEditTool || len(input.ChangedFiles) > 0 {
			return set(FinalGateNeedsRecovery, "read-only request attempted or produced workspace mutation", "Keep review-only, plan-only, and draft-only goal prompt turns read-only unless the user explicitly asks for mutation.")
		}
	}
	if input.RequestEnvelope.GoalPromptDraftOnly {
		if input.RequestEnvelope.ExplicitEditRequest || input.RequestEnvelope.AllowsGitMutation {
			return set(FinalGateNeedsRecovery, "draft-only goal prompt request was promoted to execution", "Return goal prompt text only; do not activate, run, commit, or push a goal.")
		}
	}
	if input.Verification.Unresolved && !input.GeneratedDocumentHarnessOwnsIt {
		return set(FinalGateNeedsVerification, "verification is missing, skipped, or failing for changed files", "Run the relevant verification or clearly preserve the unresolved verification blocker in the final answer.")
	}
	if input.Review.BlocksFinal {
		switch strings.TrimSpace(input.Review.GateAction) {
		case reviewGateActionUserDecisionRequired:
			return set(FinalGateNeedsUserConfirmation, "review gate requires user decision", "Ask for the required user decision before treating the work as complete.")
		case reviewGateActionVerificationRequired:
			return set(FinalGateNeedsVerification, "review gate requires verification", "Run or record the required verification before the final answer.")
		default:
			return set(FinalGateNeedsReview, "review gate has unresolved findings or reviewer route blockers", "Resolve the review findings or record the reviewer-route blocker before the final answer.")
		}
	}
	if input.ExplicitEditRequest && !input.AttemptedEditTool && len(input.ChangedFiles) == 0 && replySuggestsManualEditHandoff(input.Reply) {
		return set(FinalGateNeedsRecovery, "explicit edit request was downgraded to manual handoff", "Use the available edit path or report the concrete tool failure that prevents direct edits.")
	}
	return decision
}

func (a *Agent) buildFinalGateInput(envelope RequestEnvelope, turnRuntime *TurnRuntimeState, reply string, ctx TurnRuntimeFinalContext) FinalGateInput {
	root := ""
	if a != nil {
		root = a.Workspace.Root
	}
	var session *Session
	if a != nil {
		session = a.Session
	}
	return BuildFinalGateInput(root, session, envelope, turnRuntime, reply, ctx)
}

func (a *Agent) recordFinalGateDecision(input FinalGateInput) FinalGateDecision {
	decision := DecideFinalGate(input)
	if a != nil && a.Session != nil {
		a.Session.LastFinalGateDecision = &decision
	}
	return decision
}

func finalGateRuntimeSummary(turnRuntime *TurnRuntimeState) FinalGateRuntimeSummary {
	if turnRuntime == nil {
		return FinalGateRuntimeSummary{State: TurnRuntimeNeedFinalGate}
	}
	out := FinalGateRuntimeSummary{
		State:                turnRuntime.State,
		LastTransitionReason: strings.TrimSpace(turnRuntime.LastTransitionReason),
	}
	for _, item := range turnRuntime.Interventions {
		if item.Resolved {
			continue
		}
		out.UnresolvedInterventions = append(out.UnresolvedInterventions, item)
	}
	return out
}

func finalGateVerificationResult(session *Session, envelope RequestEnvelope, changedFiles []string) FinalGateVerificationResult {
	changedFiles = normalizeTaskStateList(changedFiles, 128)
	result := FinalGateVerificationResult{
		ChangedPaths: append([]string(nil), changedFiles...),
	}
	if session == nil || session.LastVerification == nil {
		result.Missing = envelope.RequiresVerification && len(changedFiles) > 0
		result.Unresolved = result.Missing
		return result
	}
	report := *session.LastVerification
	result.Present = true
	result.Summary = report.SummaryLine()
	result.Passed = !report.HasFailures() && report.HasPassedStep() && !report.WasSkipped()
	result.Failed = report.HasFailures()
	result.Skipped = report.WasSkipped()
	if len(result.ChangedPaths) == 0 {
		result.ChangedPaths = normalizeTaskStateList(report.ChangedPaths, 128)
	}
	result.Missing = envelope.RequiresVerification && len(result.ChangedPaths) > 0 && !result.Passed && !result.Failed && !result.Skipped
	result.Unresolved = result.Missing || result.Failed || result.Skipped
	return result
}

func finalGateReviewResult(session *Session) FinalGateReviewResult {
	if session == nil || session.LastReviewRun == nil {
		return FinalGateReviewResult{}
	}
	run := *session.LastReviewRun
	run.Gate.Action = valueOrDefault(run.Gate.Action, reviewGateActionForRun(run))
	verdict := firstNonBlankString(run.Gate.Verdict, run.Result.Verdict)
	result := FinalGateReviewResult{
		Present:             true,
		RunID:               strings.TrimSpace(run.ID),
		Trigger:             strings.TrimSpace(run.Trigger),
		Target:              strings.TrimSpace(run.Target),
		RequestClass:        normalizeReviewRequestClass(run.RequestClass),
		Verdict:             strings.TrimSpace(verdict),
		MachineStatus:       strings.TrimSpace(run.MachineStatus),
		GateAction:          strings.TrimSpace(run.Gate.Action),
		NeedsRevision:       reviewRunNeedsRepair(run),
		SingleModelMode:     run.SingleModelPolicy.Enabled,
		NoCrossReviewReason: strings.TrimSpace(run.SingleModelPolicy.NoCrossReviewReason),
	}
	if result.NoCrossReviewReason == "" {
		result.NoCrossReviewReason = finalGateNoCrossReviewReason(run)
	}
	result.BlocksFinal = result.NeedsRevision || finalGateReviewActionBlocksFinal(result.GateAction)
	return result
}

func finalGateGitMutationState(session *Session, envelope RequestEnvelope) FinalGateGitMutationState {
	state := FinalGateGitMutationState{
		Requested:         envelope.ExplicitGitRequest || envelope.AllowsGitMutation,
		AllowedByEnvelope: envelope.AllowsGitMutation,
		IdentityAllowed:   false,
	}
	// MutationAttempted is driven by recorded runtime evidence: a git-mutating
	// tool execution (git_add/commit/push/create_pr) or a run_shell command that
	// mutates git state. This is the only layer where the actual tool history is
	// available, so the git-safety branches in DecideFinalGate are reachable.
	state.MutationAttempted = finalGateGitMutationAttempted(session)
	// Git commit identity is enforced at the git_commit/git_push tool layer
	// (checkAllowedGitCommitIdentity runs there before any mutation). The final
	// gate must stay a pure, deterministic decision; it does not shell out to read
	// git config, so IdentityChecked stays false here and the identity branch in
	// DecideFinalGate defers to that tool-layer enforcement.
	return state
}

// finalGateGitMutationAttempted reports whether the recorded conversation shows a
// git-mutating tool execution in the current session. Tool result messages carry
// the executed tool name and, for run_shell, the executed command in ToolMeta.
func finalGateGitMutationAttempted(session *Session) bool {
	if session == nil {
		return false
	}
	for i := range session.Messages {
		msg := session.Messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "tool") {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(msg.ToolName)) {
		case "git_add", "git_commit", "git_push", "git_create_pr":
			return true
		}
		if finalGateToolMetaIsGitMutation(msg.ToolMeta) {
			return true
		}
	}
	return false
}

func finalGateToolMetaIsGitMutation(meta map[string]any) bool {
	if len(meta) == 0 {
		return false
	}
	if effect, ok := meta["effect"].(string); ok {
		if strings.EqualFold(strings.TrimSpace(effect), "git_mutation") {
			return true
		}
	}
	if command, ok := meta["command"].(string); ok {
		if shellCommandMutatesGitState(strings.ToLower(strings.TrimSpace(command))) {
			return true
		}
	}
	return false
}

func finalGateReadOnlyBoundary(envelope RequestEnvelope) bool {
	if envelope.AllowsFileMutation {
		return false
	}
	return envelope.ReadOnlyAnalysis ||
		envelope.ReviewOnlyModeRequest ||
		envelope.GoalPromptDraftOnly ||
		envelope.PrimaryClass == RequestClassReview ||
		envelope.PrimaryClass == RequestClassPlan
}

func finalGateBlockingInterventions(items []RuntimeIntervention) []RuntimeIntervention {
	out := make([]RuntimeIntervention, 0)
	for _, item := range items {
		if item.Resolved {
			continue
		}
		out = append(out, item)
	}
	return out
}

func finalGateOnlyVerificationInterventions(items []RuntimeIntervention) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if normalizeRuntimeInterventionKind(item.Kind) != RuntimeInterventionVerificationUnresolved {
			return false
		}
	}
	return true
}

func finalGateReviewActionBlocksFinal(action string) bool {
	switch strings.TrimSpace(action) {
	case reviewGateActionRepairRequired,
		reviewGateActionReviewerUnavailable,
		reviewGateActionUserDecisionRequired,
		reviewGateActionVerificationRequired:
		return true
	default:
		return false
	}
}

func finalGateNoCrossReviewReason(run ReviewRun) string {
	for _, transition := range run.StateTransitions {
		if strings.TrimSpace(transition.To) != reviewStateNoCrossReview {
			continue
		}
		reason := strings.TrimSpace(transition.Reason)
		if strings.HasPrefix(reason, "reason=") {
			return strings.TrimSpace(strings.TrimPrefix(reason, "reason="))
		}
		return reason
	}
	return ""
}
