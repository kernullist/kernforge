package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	modelReviewConsentAsk    = "ask"
	modelReviewConsentAlways = "always"
	modelReviewConsentNever  = "never"

	modelReviewSkipByUser               = "skipped_by_user"
	modelReviewSkipNoInteractiveConsent = "skipped_no_interactive_consent"
	modelReviewSkipConfigNever          = "skipped_by_config_never"
	modelReviewSkipReadOnlyBoundary     = "skipped_read_only_boundary"
	modelReviewSkipTurnBudgetExceeded   = "skipped_turn_model_review_budget_exceeded"

	maxImplicitModelReviewsPerTurn = 2
)

type ModelReviewConsentRequest struct {
	Trigger                 string
	OriginalMainProposal    string
	OriginalMainProposalRef string
}

type ModelReviewConsentDecision struct {
	Allowed       bool
	Policy        string
	ConsentSource string
	SkipReason    string
}

func normalizeModelReviewConsent(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", modelReviewConsentAsk:
		return modelReviewConsentAsk
	case "on", "yes", "true", modelReviewConsentAlways:
		return modelReviewConsentAlways
	case "off", "no", "false", modelReviewConsentNever:
		return modelReviewConsentNever
	default:
		return modelReviewConsentAsk
	}
}

func configModelReviewConsent(cfg Config) string {
	return normalizeModelReviewConsent(configReviewHarness(cfg).ModelReviewConsent)
}

func (a *Agent) confirmImplicitModelReview(trigger string, originalMainProposal string) ModelReviewConsentDecision {
	req := ModelReviewConsentRequest{
		Trigger:              strings.TrimSpace(trigger),
		OriginalMainProposal: strings.TrimSpace(originalMainProposal),
	}
	if decision, blocked := implicitModelReviewPreConsentDecision(configForAgent(a), sessionForAgent(a), req); blocked {
		return decision
	}
	if a != nil && a.PromptConfirmModelReview != nil {
		beforeUsed := implicitModelReviewBudgetUsed(sessionForAgent(a))
		decision := normalizeModelReviewConsentDecision(a.PromptConfirmModelReview(req), a.Config)
		if decision.Allowed && !implicitModelReviewTriggerBypassesBoundaryBudget(req) && implicitModelReviewBudgetUsed(sessionForAgent(a)) == beforeUsed {
			implicitModelReviewRecordAllowed(sessionForAgent(a), req)
		}
		return decision
	}
	policy := configModelReviewConsent(Config{})
	if a != nil {
		policy = configModelReviewConsent(a.Config)
	}
	switch policy {
	case modelReviewConsentAlways:
		return implicitModelReviewFinalizeDecision(sessionForAgent(a), req, ModelReviewConsentDecision{Allowed: true, Policy: policy, ConsentSource: "config_always"})
	case modelReviewConsentNever:
		return ModelReviewConsentDecision{Allowed: false, Policy: policy, ConsentSource: "config_never", SkipReason: modelReviewSkipConfigNever}
	default:
		return ModelReviewConsentDecision{Allowed: false, Policy: policy, ConsentSource: "no_runtime_prompt", SkipReason: modelReviewSkipNoInteractiveConsent}
	}
}

func configForAgent(a *Agent) Config {
	if a == nil {
		return Config{}
	}
	return a.Config
}

func sessionForAgent(a *Agent) *Session {
	if a == nil {
		return nil
	}
	return a.Session
}

func (rt *runtimeState) confirmImplicitModelReview(req ModelReviewConsentRequest) ModelReviewConsentDecision {
	policy := modelReviewConsentAsk
	if rt != nil {
		policy = configModelReviewConsent(rt.cfg)
	}
	var session *Session
	if rt != nil {
		session = rt.session
	}
	if decision, blocked := implicitModelReviewPreConsentDecision(configForRuntimeState(rt), session, req); blocked {
		return decision
	}
	if rt != nil && !rt.modelReviewConsentPromptEnabled && rt.agent != nil && rt.agent.PromptConfirmModelReview != nil {
		beforeUsed := implicitModelReviewBudgetUsed(session)
		decision := normalizeModelReviewConsentDecision(rt.agent.PromptConfirmModelReview(req), rt.cfg)
		if decision.Allowed && !implicitModelReviewTriggerBypassesBoundaryBudget(req) && implicitModelReviewBudgetUsed(session) == beforeUsed {
			implicitModelReviewRecordAllowed(session, req)
		}
		return decision
	}
	switch policy {
	case modelReviewConsentAlways:
		return implicitModelReviewFinalizeDecision(session, req, ModelReviewConsentDecision{Allowed: true, Policy: policy, ConsentSource: "config_always"})
	case modelReviewConsentNever:
		return ModelReviewConsentDecision{Allowed: false, Policy: policy, ConsentSource: "config_never", SkipReason: modelReviewSkipConfigNever}
	}
	if rt == nil {
		return ModelReviewConsentDecision{Allowed: false, Policy: policy, ConsentSource: "runtime_missing", SkipReason: modelReviewSkipNoInteractiveConsent}
	}
	if !rt.modelReviewConsentPromptEnabled {
		return ModelReviewConsentDecision{Allowed: false, Policy: policy, ConsentSource: "runtime_prompt_not_enabled", SkipReason: modelReviewSkipNoInteractiveConsent}
	}
	if rt.alwaysApproveModelReview {
		return implicitModelReviewFinalizeDecision(session, req, ModelReviewConsentDecision{Allowed: true, Policy: policy, ConsentSource: "session_auto_review"})
	}
	if !rt.interactive {
		return ModelReviewConsentDecision{Allowed: false, Policy: policy, ConsentSource: "non_interactive", SkipReason: modelReviewSkipNoInteractiveConsent}
	}
	trigger := strings.TrimSpace(req.Trigger)
	if trigger == "" {
		trigger = "implicit"
	}
	rt.printModelReviewConsentContext(req, trigger)
	beforeAlways := rt.alwaysApproveModelReview
	allowed, err := rt.confirm(modelReviewQuestion(rt.cfg))
	if err != nil {
		return ModelReviewConsentDecision{Allowed: false, Policy: policy, ConsentSource: "prompt_error", SkipReason: "model_review_consent_prompt_failed: " + err.Error()}
	}
	if allowed {
		source := "user"
		if !beforeAlways && rt.alwaysApproveModelReview {
			source = "session_auto_review"
		}
		return implicitModelReviewFinalizeDecision(session, req, ModelReviewConsentDecision{Allowed: true, Policy: policy, ConsentSource: source})
	}
	return ModelReviewConsentDecision{Allowed: false, Policy: policy, ConsentSource: "user", SkipReason: modelReviewSkipByUser}
}

func configForRuntimeState(rt *runtimeState) Config {
	if rt == nil {
		return Config{}
	}
	return rt.cfg
}

func implicitModelReviewPreConsentDecision(cfg Config, session *Session, req ModelReviewConsentRequest) (ModelReviewConsentDecision, bool) {
	policy := configModelReviewConsent(cfg)
	req.Trigger = strings.TrimSpace(req.Trigger)
	if implicitModelReviewTriggerBypassesBoundaryBudget(req) {
		return ModelReviewConsentDecision{}, false
	}
	if implicitModelReviewReadOnlyBoundary(session, req) {
		implicitModelReviewRecordSkip(session, req, modelReviewSkipReadOnlyBoundary)
		return ModelReviewConsentDecision{Allowed: false, Policy: policy, ConsentSource: "request_boundary", SkipReason: modelReviewSkipReadOnlyBoundary}, true
	}
	if implicitModelReviewBudgetExceeded(session, req) {
		implicitModelReviewRecordSkip(session, req, modelReviewSkipTurnBudgetExceeded)
		return ModelReviewConsentDecision{Allowed: false, Policy: policy, ConsentSource: "turn_budget", SkipReason: modelReviewSkipTurnBudgetExceeded}, true
	}
	return ModelReviewConsentDecision{}, false
}

func implicitModelReviewTriggerBypassesBoundaryBudget(req ModelReviewConsentRequest) bool {
	trigger := strings.ToLower(strings.TrimSpace(req.Trigger))
	return strings.Contains(trigger, "analysis") && strings.Contains(trigger, "reviewer")
}

func implicitModelReviewFinalizeDecision(session *Session, req ModelReviewConsentRequest, decision ModelReviewConsentDecision) ModelReviewConsentDecision {
	decision = normalizeModelReviewConsentDecision(decision, Config{})
	if decision.Allowed && !implicitModelReviewTriggerBypassesBoundaryBudget(req) {
		implicitModelReviewRecordAllowed(session, req)
	}
	return decision
}

func implicitModelReviewReadOnlyBoundary(session *Session, req ModelReviewConsentRequest) bool {
	envelope, ok := implicitModelReviewRequestEnvelope(session)
	if !ok {
		return false
	}
	envelope.Normalize()
	if !envelope.ReadOnlyAnalysis {
		return false
	}
	if envelope.ExplicitEditRequest || envelope.ExplicitGitRequest || envelope.DocumentAuthoring {
		return false
	}
	if envelope.AllowsFileMutation || envelope.AllowsGitMutation {
		return false
	}
	return strings.TrimSpace(req.Trigger) != ""
}

func implicitModelReviewRequestEnvelope(session *Session) (RequestEnvelope, bool) {
	if session == nil {
		return RequestEnvelope{}, false
	}
	request := strings.TrimSpace(sessionEffectiveUserRequestText(session))
	if session.LastRequestEnvelope != nil {
		envelope := *session.LastRequestEnvelope
		envelope.Normalize()
		if request == "" || strings.EqualFold(strings.TrimSpace(baseUserQueryText(envelope.ExternalUserText)), strings.TrimSpace(baseUserQueryText(request))) {
			return envelope, true
		}
	}
	if request == "" {
		return RequestEnvelope{}, false
	}
	envelope := buildRequestEnvelope(request)
	envelope.Normalize()
	return envelope, true
}

func implicitModelReviewBudgetExceeded(session *Session, req ModelReviewConsentRequest) bool {
	budget := implicitModelReviewBudgetForTurn(session)
	if budget == nil {
		return false
	}
	return budget.Used >= maxImplicitModelReviewsPerTurn
}

func implicitModelReviewBudgetUsed(session *Session) int {
	budget := implicitModelReviewBudgetForTurn(session)
	if budget == nil {
		return 0
	}
	return budget.Used
}

func implicitModelReviewRecordAllowed(session *Session, req ModelReviewConsentRequest) {
	budget := implicitModelReviewBudgetForTurn(session)
	if budget == nil {
		return
	}
	budget.Used++
	budget.LastTrigger = strings.TrimSpace(req.Trigger)
	budget.LastSkipReason = ""
}

func implicitModelReviewRecordSkip(session *Session, req ModelReviewConsentRequest, reason string) {
	budget := implicitModelReviewBudgetForTurn(session)
	if budget == nil {
		return
	}
	switch reason {
	case modelReviewSkipReadOnlyBoundary:
		budget.SkippedReadOnly++
	case modelReviewSkipTurnBudgetExceeded:
		budget.SkippedBudget++
	}
	budget.LastTrigger = strings.TrimSpace(req.Trigger)
	budget.LastSkipReason = strings.TrimSpace(reason)
}

func implicitModelReviewBudgetForTurn(session *Session) *ImplicitModelReviewTurnBudget {
	if session == nil {
		return nil
	}
	key := implicitModelReviewTurnKey(session)
	if key == "" {
		return nil
	}
	if session.ImplicitModelReviewBudget == nil || session.ImplicitModelReviewBudget.TurnKey != key {
		session.ImplicitModelReviewBudget = &ImplicitModelReviewTurnBudget{TurnKey: key}
	}
	return session.ImplicitModelReviewBudget
}

func implicitModelReviewTurnKey(session *Session) string {
	if session == nil {
		return ""
	}
	for i := len(session.Messages) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(session.Messages[i].Role), "user") {
			continue
		}
		text := strings.TrimSpace(baseUserQueryText(session.Messages[i].Text))
		if text == "" {
			text = strings.TrimSpace(session.Messages[i].Text)
		}
		if text == "" {
			continue
		}
		return fmt.Sprintf("%d:%s", i, compactPromptSection(text, 180))
	}
	return ""
}

func implicitModelReviewBudgetStatusLine(session *Session) string {
	if session == nil || session.ImplicitModelReviewBudget == nil || session.ImplicitModelReviewBudget.TurnKey == "" {
		return fmt.Sprintf("used=0/%d skipped_read_only=0 skipped_budget=0", maxImplicitModelReviewsPerTurn)
	}
	budget := session.ImplicitModelReviewBudget
	last := valueOrUnset(strings.TrimSpace(budget.LastTrigger))
	if strings.TrimSpace(budget.LastSkipReason) != "" {
		last += " skip=" + strings.TrimSpace(budget.LastSkipReason)
	}
	return fmt.Sprintf(
		"used=%d/%d skipped_read_only=%d skipped_budget=%d last=%s",
		budget.Used,
		maxImplicitModelReviewsPerTurn,
		budget.SkippedReadOnly,
		budget.SkippedBudget,
		last,
	)
}

func (rt *runtimeState) printModelReviewConsentContext(req ModelReviewConsentRequest, trigger string) {
	if rt == nil {
		return
	}
	lines := []string{rt.ui.statusKV("model_review_trigger", strings.TrimSpace(trigger))}
	if context := formatModelReviewConsentOriginalProposal(rt.cfg, req); context != "" {
		lines = append(lines, strings.Split(context, "\n")...)
	}
	rt.printPersistentWhileThinking(lines...)
}

func formatModelReviewConsentOriginalProposal(cfg Config, req ModelReviewConsentRequest) string {
	ref := strings.TrimSpace(req.OriginalMainProposalRef)
	proposal := strings.TrimSpace(req.OriginalMainProposal)
	if ref == "" && proposal == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(localizedText(cfg, "Main model proposal awaiting review:", "리뷰 전 메인 모델 제안:"))
	if ref != "" {
		fmt.Fprintf(&b, "\n- %s: %s", localizedText(cfg, "artifact", "artifact"), filepath.ToSlash(ref))
	}
	if proposal != "" {
		preview := compactPromptSection(proposal, 2400)
		if strings.TrimSpace(preview) != "" {
			fmt.Fprintf(&b, "\n- %s:", localizedText(cfg, "preview", "preview"))
			for _, line := range strings.Split(preview, "\n") {
				if strings.TrimSpace(line) == "" {
					b.WriteString("\n  ")
					continue
				}
				b.WriteString("\n  ")
				b.WriteString(line)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func normalizeModelReviewConsentDecision(decision ModelReviewConsentDecision, cfg Config) ModelReviewConsentDecision {
	decision.Policy = normalizeModelReviewConsent(firstNonBlankString(decision.Policy, configModelReviewConsent(cfg)))
	decision.ConsentSource = strings.TrimSpace(decision.ConsentSource)
	decision.SkipReason = strings.TrimSpace(decision.SkipReason)
	if decision.Allowed {
		decision.SkipReason = ""
		if decision.ConsentSource == "" {
			decision.ConsentSource = "allowed"
		}
	} else if decision.SkipReason == "" {
		decision.SkipReason = modelReviewSkipByUser
	}
	return decision
}

func applyModelReviewConsentToRun(run *ReviewRun, decision ModelReviewConsentDecision) {
	if run == nil {
		return
	}
	decision = normalizeModelReviewConsentDecision(decision, Config{})
	run.ModelReviewConsent = decision.Policy
	run.ConsentSource = decision.ConsentSource
	run.SkipReason = decision.SkipReason
	if decision.Allowed {
		return
	}
	run.Result.Degraded = true
	run.Result.DegradedReason = "model review " + decision.SkipReason
	if run.Result.ModelQuality == "" {
		run.Result.ModelQuality = reviewModelQualityUsable
	}
	if run.ModelPlan.UserGuidance == nil {
		run.ModelPlan.UserGuidance = []string{}
	}
	run.ModelPlan.UserGuidance = append(run.ModelPlan.UserGuidance, "Implicit model review skipped: "+decision.SkipReason)
}

func reviewConsentTriggerForRun(run ReviewRun) string {
	trigger := strings.TrimSpace(run.Trigger)
	if trigger != "" {
		return strings.ReplaceAll(trigger, "_", "-")
	}
	switch strings.TrimSpace(run.Target) {
	case reviewTargetFinal:
		return "final-answer"
	case reviewTargetGoal:
		return "goal iteration"
	case reviewTargetAnalysis:
		return "analysis reviewer"
	case reviewTargetPlan:
		return "plan/feature reviewer"
	case reviewTargetChange:
		return "post-change"
	default:
		return "implicit"
	}
}

func reviewOriginalMainProposalForOptions(opts ReviewHarnessOptions) string {
	parts := []string{}
	if strings.TrimSpace(opts.OriginalMainProposal) != "" {
		parts = append(parts, strings.TrimSpace(opts.OriginalMainProposal))
	}
	if strings.TrimSpace(opts.ProvidedDiff) != "" {
		parts = append(parts, "Proposed diff:\n"+strings.TrimSpace(opts.ProvidedDiff))
	}
	if strings.TrimSpace(opts.ImplementationReply) != "" {
		parts = append(parts, "Implementation reply:\n"+strings.TrimSpace(opts.ImplementationReply))
	}
	if len(opts.EditProposals) > 0 {
		parts = append(parts, "Edit proposals:\n"+renderOriginalMainEditProposals(opts.EditProposals))
	}
	if strings.TrimSpace(opts.ProvidedCode) != "" {
		parts = append(parts, "Provided code:\n"+strings.TrimSpace(opts.ProvidedCode))
	}
	return compactPromptSection(strings.Join(parts, "\n\n"), 12000)
}

func renderOriginalMainEditProposals(items []EditProposal) string {
	items = normalizeEditProposals(items)
	if len(items) == 0 {
		return ""
	}
	lines := []string{}
	for _, item := range items {
		line := strings.TrimSpace(item.File)
		if line == "" && len(item.Files) > 0 {
			line = strings.Join(item.Files, ", ")
		}
		if line == "" {
			line = strings.TrimSpace(item.Operation)
		}
		if line == "" {
			line = "edit proposal"
		}
		if strings.TrimSpace(item.Rationale) != "" {
			line += ": " + strings.TrimSpace(item.Rationale)
		} else if strings.TrimSpace(item.Risk) != "" {
			line += ": " + strings.TrimSpace(item.Risk)
		}
		lines = append(lines, "- "+line)
	}
	return strings.Join(lines, "\n")
}

func writeReviewOriginalMainProposalArtifact(root string, run *ReviewRun) (string, error) {
	if run == nil || strings.TrimSpace(run.OriginalMainProposal) == "" {
		return "", nil
	}
	dir := reviewRunDir(root, run.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "original_main_proposal.md")
	body := "# Original Main Proposal\n\n" + strings.TrimSpace(run.OriginalMainProposal) + "\n"
	if err := atomicWriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return filepath.ToSlash(path), nil
}

func formatOriginalMainProposalReviewRef(cfg Config, run ReviewRun) string {
	ref := strings.TrimSpace(run.OriginalMainProposalRef)
	proposal := strings.TrimSpace(run.OriginalMainProposal)
	if ref == "" && proposal == "" {
		return ""
	}
	korean := reviewRunPrefersKorean(cfg, run)
	var b strings.Builder
	if korean {
		b.WriteString("원본 메인 모델 제안")
	} else {
		b.WriteString("Original main-model proposal")
	}
	if ref != "" {
		fmt.Fprintf(&b, ": %s", filepath.ToSlash(ref))
	}
	if first := firstNonEmptyLine(proposal); first != "" {
		if ref != "" {
			b.WriteString("\n")
		} else {
			b.WriteString(": ")
		}
		if korean {
			b.WriteString("요약: ")
		} else {
			b.WriteString("Summary: ")
		}
		b.WriteString(compactPromptSection(first, 300))
	}
	return strings.TrimSpace(b.String())
}

func modelReviewQuestion(_ Config) string {
	return modelReviewQuestionEnglish
}

const (
	modelReviewQuestionEnglish = "Run model review now?"
)

func isModelReviewQuestion(question string) bool {
	normalized := strings.TrimSpace(question)
	return strings.EqualFold(normalized, modelReviewQuestionEnglish)
}
