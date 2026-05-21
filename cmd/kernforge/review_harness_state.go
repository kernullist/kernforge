package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	reviewStateCollectEvidence      = "collect_evidence"
	reviewStateMainReview           = "main_review"
	reviewStateOptionalCrossReview  = "optional_cross_review"
	reviewStateRequiredCrossReview  = "required_cross_review"
	reviewStateNoCrossReview        = "no_cross_review"
	reviewStateMergeReviews         = "merge_reviews"
	reviewStateGateDecision         = "gate_decision"
	reviewStateActionBoundary       = "action_boundary"
	reviewStateRepairFeedback       = "repair_feedback"
	reviewStateUserFallbackOffer    = "user_fallback_offer"
	reviewStateDiffPreview          = "diff_preview"
	reviewStateVerificationRequired = "verification_required"
	reviewStateFinalSummary         = "final_summary"

	reviewGateActionRepairRequired       = "repair_required"
	reviewGateActionReviewerUnavailable  = "reviewer_unavailable"
	reviewGateActionUserDecisionRequired = "user_decision_required"
	reviewGateActionDiffPreviewAllowed   = "diff_preview_allowed"
	reviewGateActionVerificationRequired = "verification_required"
	reviewGateActionFinalSummary         = "final_summary"

	reviewActionCollectEvidence = "collect_evidence"
	reviewActionMainReview      = "main_review"
	reviewActionCrossReview     = "cross_review"
	reviewActionMergeGate       = "merge_gate"
	reviewActionPreWriteReview  = "pre_write_review"
	reviewActionSummarize       = "summarize"
)

type ReviewStateTransition struct {
	ID            string    `json:"id,omitempty"`
	From          string    `json:"from,omitempty"`
	To            string    `json:"to,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	Actor         string    `json:"actor,omitempty"`
	Blocking      bool      `json:"blocking"`
	VisibleToUser bool      `json:"visible_to_user"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
}

type ReviewActionEnvelope struct {
	SessionID        string   `json:"session_id,omitempty"`
	ActionID         string   `json:"action_id,omitempty"`
	ActionType       string   `json:"action_type,omitempty"`
	Actor            string   `json:"actor,omitempty"`
	InputRefs        []string `json:"input_refs,omitempty"`
	OutputRefs       []string `json:"output_refs,omitempty"`
	ApprovalRequired bool     `json:"approval_required"`
	ApprovalGranted  bool     `json:"approval_granted"`
	ElapsedMS        int64    `json:"elapsed_ms,omitempty"`
	Status           string   `json:"status,omitempty"`
	FailureClass     string   `json:"failure_class,omitempty"`
}

type ReviewApprovalLedger struct {
	ReviewGateApproved  bool     `json:"review_gate_approved"`
	DiffPreviewShown    bool     `json:"diff_preview_shown"`
	UserWriteApproved   bool     `json:"user_write_approved"`
	WriteApplied        bool     `json:"write_applied"`
	VerificationPassed  bool     `json:"verification_passed"`
	UserCommitRequested bool     `json:"user_commit_requested"`
	CommitDone          bool     `json:"commit_done"`
	UserPushRequested   bool     `json:"user_push_requested"`
	PushDone            bool     `json:"push_done"`
	MissingApprovals    []string `json:"missing_approvals,omitempty"`
}

type ReviewCapabilityManifest struct {
	LocalFileRead         string   `json:"local_file_read,omitempty"`
	PatchApply            string   `json:"patch_apply,omitempty"`
	DiffPreview           string   `json:"diff_preview,omitempty"`
	TestRunner            string   `json:"test_runner,omitempty"`
	GitStatus             string   `json:"git_status,omitempty"`
	GitCommit             string   `json:"git_commit,omitempty"`
	GitPush               string   `json:"git_push,omitempty"`
	WebSearch             string   `json:"web_search,omitempty"`
	WebFetch              string   `json:"web_fetch,omitempty"`
	PrimaryModel          string   `json:"primary_model,omitempty"`
	CrossReviewModel      string   `json:"cross_review_model,omitempty"`
	SingleModelReviewMode string   `json:"single_model_review_mode,omitempty"`
	MCPReviewServer       string   `json:"mcp_review_server,omitempty"`
	Notes                 []string `json:"notes,omitempty"`
}

type SingleModelReviewPolicy struct {
	Enabled                        bool     `json:"enabled"`
	IndependenceLevel              string   `json:"independence_level,omitempty"`
	NoCrossReviewReason            string   `json:"no_cross_review_reason,omitempty"`
	RequiresStructuredFindings     bool     `json:"requires_structured_findings"`
	RequiresPreWriteSelfReview     bool     `json:"requires_pre_write_self_review"`
	RequiresRFObligationStatus     bool     `json:"requires_rf_obligation_status"`
	RecordsVerificationObligations bool     `json:"records_verification_obligations"`
	Checklist                      []string `json:"checklist,omitempty"`
	VerificationObligations        []string `json:"verification_obligations,omitempty"`
}

func finalizeReviewRunProtocol(root string, rt *runtimeState, run *ReviewRun) {
	if run == nil {
		return
	}
	run.Gate.Action = reviewGateActionForRun(*run)
	run.ModelPlan.RouteHealth = reviewRouteHealthForRun(rt, run)
	run.ApprovalLedger = buildReviewApprovalLedger(rt, *run)
	run.StateTransitions = buildReviewStateTransitions(*run)
	run.ActionEnvelopes = buildReviewActionEnvelopes(root, rt, *run)
	run.ExternalLookupIntents = mergeReviewExternalLookupIntents(run.ExternalLookupIntents, reviewExternalLookupIntentsForRun(rt, *run))
	run.ArtifactIntegrity = buildReviewArtifactIntegrity(root, *run)
	run.LedgerConsistency = buildReviewLedgerConsistency(root, rt, *run)
	run.ResumeSanity = buildReviewResumeSanityCheck(root, rt, *run)
}

func buildReviewCapabilityManifest(rt *runtimeState, root string) ReviewCapabilityManifest {
	manifest := ReviewCapabilityManifest{
		LocalFileRead:         "unavailable",
		PatchApply:            "available",
		DiffPreview:           "unavailable",
		TestRunner:            "unknown",
		GitStatus:             "unavailable",
		GitCommit:             "unavailable",
		GitPush:               "unavailable",
		WebSearch:             "unavailable",
		WebFetch:              "unavailable",
		PrimaryModel:          "unavailable",
		CrossReviewModel:      "unavailable",
		SingleModelReviewMode: "unavailable",
		MCPReviewServer:       "available",
	}
	if strings.TrimSpace(root) != "" {
		if _, err := os.Stat(root); err == nil {
			manifest.LocalFileRead = "available"
		}
		if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
			manifest.GitStatus = "available"
			manifest.GitCommit = "available"
			manifest.GitPush = "available"
		}
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			manifest.TestRunner = "available"
		}
	}
	if rt != nil {
		if rt.workspace.PreviewEdit != nil {
			manifest.DiffPreview = "available"
		}
		if rt.agent != nil && rt.agent.Client != nil && strings.TrimSpace(rt.cfg.Model) != "" {
			manifest.PrimaryModel = "available"
			manifest.SingleModelReviewMode = "available"
		}
		if reviewRuntimeHasDistinctCrossReviewer(rt) {
			manifest.CrossReviewModel = "available"
		}
		if rt.mcp != nil && rt.mcp.HasWebResearchCapability() {
			manifest.WebSearch = "available"
			manifest.WebFetch = "available"
		}
	}
	if manifest.WebSearch != "available" {
		manifest.Notes = append(manifest.Notes, "web search is unavailable or not loaded; local code reviews must not depend on external lookup")
	}
	return manifest
}

func reviewRuntimeHasDistinctCrossReviewer(rt *runtimeState) bool {
	if rt == nil || rt.agent == nil {
		return false
	}
	if rt.agent.AuxReviewerClient != nil && strings.TrimSpace(rt.agent.AuxReviewerModel) != "" {
		if !reviewClientMatchesMain(rt, rt.agent.AuxReviewerClient, rt.agent.AuxReviewerModel) {
			return true
		}
	}
	if rt.agent.ReviewerClient != nil && strings.TrimSpace(rt.agent.ReviewerModel) != "" {
		if !reviewClientMatchesMain(rt, rt.agent.ReviewerClient, rt.agent.ReviewerModel) {
			return true
		}
	}
	reviewCfg := configReviewHarness(rt.cfg)
	for _, roleCfg := range reviewCfg.RoleModels {
		if strings.TrimSpace(roleCfg.Provider) != "" && strings.TrimSpace(roleCfg.Model) != "" {
			if !reviewModelConfigMatchesMain(rt.cfg, roleCfg) {
				return true
			}
		}
	}
	return false
}

func buildSingleModelReviewPolicy(run ReviewRun, hasCrossReviewer bool) SingleModelReviewPolicy {
	policy := SingleModelReviewPolicy{}
	if hasCrossReviewer {
		return policy
	}
	policy.Enabled = true
	policy.IndependenceLevel = "single_model"
	policy.NoCrossReviewReason = "single_model_mode"
	policy.RequiresStructuredFindings = true
	policy.RequiresPreWriteSelfReview = strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write")
	policy.RequiresRFObligationStatus = policy.RequiresPreWriteSelfReview && len(run.RepairFindings) > 0
	policy.RecordsVerificationObligations = true
	policy.Checklist = []string{
		"correctness",
		"regression_risk",
		"security_bypass_surface",
		"verification_gap",
		"scope_creep",
		"stale_evidence",
	}
	if policy.RequiresPreWriteSelfReview {
		policy.VerificationObligations = append(policy.VerificationObligations, "single-model pre-write review must use the frozen diff and must not create a new patch")
	}
	if policy.RequiresRFObligationStatus {
		policy.VerificationObligations = append(policy.VerificationObligations, "pre-fix repair findings require resolved, partial, unresolved, verification-needed, or evidence-unconfirmed status")
	}
	return policy
}

func reviewGateActionForRun(run ReviewRun) string {
	if reviewRunHasRequiredReviewerFailure(run) {
		if reviewRunHasUsableMainReviewer(run) {
			return reviewGateActionUserDecisionRequired
		}
		return reviewGateActionReviewerUnavailable
	}
	if strings.EqualFold(run.Gate.Verdict, reviewVerdictNeedsRevision) ||
		strings.EqualFold(run.Gate.Verdict, reviewVerdictBlocked) {
		return reviewGateActionRepairRequired
	}
	if strings.EqualFold(run.Gate.Verdict, reviewVerdictInsufficientEvidence) {
		return reviewGateActionUserDecisionRequired
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") &&
		(strings.EqualFold(run.Gate.Verdict, reviewVerdictApproved) ||
			strings.EqualFold(run.Gate.Verdict, reviewVerdictApprovedWithWarnings)) {
		return reviewGateActionDiffPreviewAllowed
	}
	if strings.TrimSpace(run.Evidence.VerificationSummary) == "" && reviewRunHasChangeEvidence(run) && run.Target != reviewTargetPlan {
		return reviewGateActionVerificationRequired
	}
	return reviewGateActionFinalSummary
}

func buildReviewApprovalLedger(rt *runtimeState, run ReviewRun) ReviewApprovalLedger {
	ledger := ReviewApprovalLedger{
		ReviewGateApproved: strings.EqualFold(run.Gate.Verdict, reviewVerdictApproved) ||
			strings.EqualFold(run.Gate.Verdict, reviewVerdictApprovedWithWarnings),
	}
	if rt != nil && rt.session != nil && rt.session.LastVerification != nil {
		ledger.VerificationPassed = reviewApprovalLedgerVerificationPassed(rt.session, run)
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		if ledger.ReviewGateApproved {
			ledger.MissingApprovals = append(ledger.MissingApprovals, "diff_preview_shown", "user_write_approved")
		}
		if !ledger.VerificationPassed {
			ledger.MissingApprovals = append(ledger.MissingApprovals, "verification_passed")
		}
	}
	return ledger
}

func reviewApprovalLedgerVerificationPassed(session *Session, run ReviewRun) bool {
	if session == nil || session.LastVerification == nil {
		return false
	}
	report := *session.LastVerification
	changedPaths := reviewApprovalLedgerChangedPaths(session, run)
	if !verificationReportCoversCurrentPatch(session, report, time.Time{}, changedPaths) {
		return false
	}
	return !report.HasFailures() && report.HasPassedStep()
}

func reviewApprovalLedgerChangedPaths(session *Session, run ReviewRun) []string {
	var paths []string
	paths = append(paths, run.ChangeSet.ChangedPaths...)
	paths = append(paths, run.Evidence.ChangedPaths...)
	if len(paths) == 0 {
		paths = append(paths, currentTurnPatchTransactionChangedPaths(session)...)
	}
	return normalizeTaskStateList(paths, 128)
}

func buildReviewStateTransitions(run ReviewRun) []ReviewStateTransition {
	now := run.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	var out []ReviewStateTransition
	add := func(from string, to string, reason string, actor string, blocking bool, visible bool) {
		out = append(out, ReviewStateTransition{
			ID:            fmt.Sprintf("RST-%03d", len(out)+1),
			From:          from,
			To:            to,
			Reason:        reason,
			Actor:         actor,
			Blocking:      blocking,
			VisibleToUser: visible,
			CreatedAt:     now.Add(time.Duration(len(out)) * time.Millisecond),
		})
	}
	add("", reviewStateCollectEvidence, "request analysis and local evidence collection started", "harness", false, true)
	modelTransition := reviewStateMainReview
	if len(run.ReviewerRuns) == 0 {
		modelTransition = reviewStateMergeReviews
		add(reviewStateCollectEvidence, modelTransition, "model review disabled or unavailable; deterministic findings are used", "harness", false, true)
	} else {
		add(reviewStateCollectEvidence, reviewStateMainReview, "main model first-pass review uses the frozen evidence pack", "main_model", false, true)
		hasCross := false
		for _, reviewerRun := range run.ReviewerRuns {
			if strings.EqualFold(strings.TrimSpace(reviewerRun.Kind), "cross") {
				hasCross = true
				target := reviewStateOptionalCrossReview
				if reviewRunRequiresSuccessfulCrossReviewer(run) {
					target = reviewStateRequiredCrossReview
				}
				add(reviewStateMainReview, target, "cross reviewer receives the same evidence and primary draft", "reviewer_model", reviewerRun.Status == "failed", true)
				modelTransition = target
				break
			}
		}
		if !hasCross {
			add(reviewStateMainReview, reviewStateNoCrossReview, "reason=single_model_mode", "harness", false, true)
			modelTransition = reviewStateNoCrossReview
		}
		add(modelTransition, reviewStateMergeReviews, "findings are normalized, deduplicated, and merged", "harness", false, false)
	}
	add(reviewStateMergeReviews, reviewStateGateDecision, "deterministic gate evaluated merged findings", "harness", len(run.Gate.BlockingFindings) > 0, true)
	boundary := reviewActionBoundaryState(run)
	add(reviewStateGateDecision, boundary, "gate action="+valueOrDefault(run.Gate.Action, reviewGateActionForRun(run)), "harness", reviewActionBoundaryBlocks(boundary), true)
	return out
}

func reviewActionBoundaryState(run ReviewRun) string {
	switch valueOrDefault(run.Gate.Action, reviewGateActionForRun(run)) {
	case reviewGateActionRepairRequired:
		return reviewStateRepairFeedback
	case reviewGateActionReviewerUnavailable, reviewGateActionUserDecisionRequired:
		return reviewStateUserFallbackOffer
	case reviewGateActionDiffPreviewAllowed:
		return reviewStateDiffPreview
	case reviewGateActionVerificationRequired:
		return reviewStateVerificationRequired
	default:
		return reviewStateFinalSummary
	}
}

func reviewActionBoundaryBlocks(state string) bool {
	return state == reviewStateRepairFeedback || state == reviewStateUserFallbackOffer || state == reviewStateVerificationRequired
}

func buildReviewActionEnvelopes(root string, rt *runtimeState, run ReviewRun) []ReviewActionEnvelope {
	sessionID := ""
	if rt != nil && rt.session != nil {
		sessionID = rt.session.ID
	}
	var out []ReviewActionEnvelope
	add := func(actionType string, actor string, inputs []string, outputs []string, approvalRequired bool, approvalGranted bool, status string, failureClass string) {
		out = append(out, ReviewActionEnvelope{
			SessionID:        sessionID,
			ActionID:         fmt.Sprintf("RAE-%03d", len(out)+1),
			ActionType:       actionType,
			Actor:            actor,
			InputRefs:        normalizeTaskStateList(inputs, 32),
			OutputRefs:       normalizeTaskStateList(outputs, 32),
			ApprovalRequired: approvalRequired,
			ApprovalGranted:  approvalGranted,
			Status:           valueOrDefault(status, "completed"),
			FailureClass:     failureClass,
		})
	}
	add(reviewActionCollectEvidence, "harness", []string{run.Objective}, run.Evidence.Sources, false, true, "completed", "")
	if len(run.ReviewerRuns) > 0 {
		for _, reviewerRun := range run.ReviewerRuns {
			action := reviewActionMainReview
			actor := "main_model"
			if strings.EqualFold(strings.TrimSpace(reviewerRun.Kind), "cross") {
				action = reviewActionCrossReview
				actor = "reviewer_model"
			}
			status := "completed"
			failure := ""
			if !strings.EqualFold(strings.TrimSpace(reviewerRun.Status), "completed") {
				status = "failed"
				failure = firstNonBlankString(firstNonEmptyLine(reviewerRun.Error), reviewerRun.ModelQuality, "reviewer_failed")
			}
			add(action, actor, []string{reviewerRun.PromptPath}, []string{reviewerRun.RawOutputPath, reviewerRun.RawProviderResponsePath}, false, true, status, failure)
		}
	} else {
		add(reviewActionMainReview, "harness", run.Evidence.Sources, nil, false, false, "skipped", "model_review_disabled")
	}
	gateStatus := "completed"
	failure := ""
	switch run.Gate.Action {
	case reviewGateActionReviewerUnavailable:
		gateStatus = "blocked"
		failure = "reviewer_unavailable"
	case reviewGateActionUserDecisionRequired:
		gateStatus = "user_decision_required"
		if reviewRunHasRequiredReviewerFailure(run) {
			failure = "reviewer_unavailable"
		} else {
			failure = "insufficient_evidence"
		}
	case reviewGateActionRepairRequired:
		gateStatus = "needs_revision"
		failure = "repair_required"
	case reviewGateActionVerificationRequired:
		gateStatus = "verification_required"
		failure = "verification_required"
	}
	add(reviewActionMergeGate, "harness", run.Evidence.Sources, reviewFindingIDs(run.Findings), false, true, gateStatus, failure)
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		add(reviewActionPreWriteReview, "harness", reviewEditProposalRefs(run.EditProposals), reviewFindingIDs(run.Findings), true, run.ApprovalLedger.ReviewGateApproved, gateStatus, failure)
	}
	add(reviewActionSummarize, "harness", reviewFindingIDs(run.Findings), []string{filepath.ToSlash(filepath.Join(reviewRunDir(root, run.ID), "review.md"))}, false, true, "completed", "")
	return out
}

func reviewFindingIDs(findings []ReviewFinding) []string {
	var out []string
	for _, finding := range findings {
		if strings.TrimSpace(finding.ID) != "" {
			out = append(out, finding.ID)
		}
	}
	return out
}

func reviewEditProposalRefs(proposals []EditProposal) []string {
	var out []string
	for _, proposal := range proposals {
		ref := strings.TrimSpace(firstNonBlankString(proposal.File, proposal.Operation, proposal.ExpectedPreview))
		if ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

func writeReviewProtocolArtifacts(dir string, run ReviewRun) ([]string, error) {
	actionPath := filepath.Join(dir, "action_envelope.jsonl")
	approvalPath := filepath.Join(dir, "approval_ledger.json")
	capabilityPath := filepath.Join(dir, "capability_manifest.json")
	externalLookupPath := filepath.Join(dir, "external_lookup_intent.jsonl")
	integrityPath := filepath.Join(dir, "artifact_integrity.json")
	ledgerPath := filepath.Join(dir, "ledger_consistency.json")
	resumePath := filepath.Join(dir, "resume_sanity.json")
	var lines []string
	for _, envelope := range run.ActionEnvelopes {
		data, err := json.Marshal(envelope)
		if err != nil {
			return nil, err
		}
		lines = append(lines, string(data))
	}
	if err := atomicWriteFile(actionPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return nil, err
	}
	if err := writeJSONFileAtomic(approvalPath, run.ApprovalLedger); err != nil {
		return nil, err
	}
	if err := writeJSONFileAtomic(capabilityPath, run.CapabilityManifest); err != nil {
		return nil, err
	}
	lines = nil
	for _, intent := range run.ExternalLookupIntents {
		data, err := json.Marshal(intent)
		if err != nil {
			return nil, err
		}
		lines = append(lines, string(data))
	}
	if err := atomicWriteFile(externalLookupPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return nil, err
	}
	if err := writeJSONFileAtomic(integrityPath, run.ArtifactIntegrity); err != nil {
		return nil, err
	}
	if err := writeJSONFileAtomic(ledgerPath, run.LedgerConsistency); err != nil {
		return nil, err
	}
	if err := writeJSONFileAtomic(resumePath, run.ResumeSanity); err != nil {
		return nil, err
	}
	return []string{actionPath, approvalPath, capabilityPath, externalLookupPath, integrityPath, ledgerPath, resumePath}, nil
}

func writeJSONFileAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, data, 0o644)
}

func recoverLatestReviewRun(root string) (ReviewRun, string, bool, error) {
	dir := reviewArtifactRoot(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return ReviewRun{}, filepath.Join(dir, "latest.json"), false, nil
		}
		return ReviewRun{}, filepath.Join(dir, "latest.json"), false, err
	}
	type candidate struct {
		run  ReviewRun
		path string
		at   time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name(), "review.json")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var run ReviewRun
		if err := json.Unmarshal(data, &run); err != nil {
			continue
		}
		at := run.CreatedAt
		if at.IsZero() {
			if info, statErr := os.Stat(path); statErr == nil {
				at = info.ModTime()
			}
		}
		candidates = append(candidates, candidate{run: run, path: path, at: at})
	}
	if len(candidates) == 0 {
		return ReviewRun{}, filepath.Join(dir, "latest.json"), false, nil
	}
	sort.SliceStable(candidates, func(i int, j int) bool {
		return candidates[i].at.After(candidates[j].at)
	})
	return candidates[0].run, candidates[0].path, true, nil
}
