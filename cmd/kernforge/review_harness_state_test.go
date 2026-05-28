package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewRunWritesProtocolArtifacts(t *testing.T) {
	root := t.TempDir()
	rt := reviewStateTestRuntime(root, nil)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Target:       reviewTargetChange,
		Request:      "review supplied code",
		ProvidedCode: "package main\nfunc main() {}\n",
		NoModel:      true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(run.ActionEnvelopes) == 0 {
		t.Fatalf("expected action envelopes on run")
	}
	if len(run.StateTransitions) == 0 {
		t.Fatalf("expected state transitions on run")
	}
	if strings.TrimSpace(run.CapabilityManifest.LocalFileRead) == "" {
		t.Fatalf("expected capability manifest on run")
	}
	if strings.TrimSpace(run.ArtifactIntegrity.EvidenceHash) == "" {
		t.Fatalf("expected artifact integrity hash on run")
	}
	if strings.TrimSpace(run.LedgerConsistency.Status) == "" {
		t.Fatalf("expected ledger consistency on run")
	}
	if strings.TrimSpace(run.ResumeSanity.Status) == "" {
		t.Fatalf("expected resume sanity check on run")
	}
	for _, name := range []string{"action_envelope.jsonl", "approval_ledger.json", "capability_manifest.json", "external_lookup_intent.jsonl", "artifact_integrity.json", "ledger_consistency.json", "resume_sanity.json"} {
		path := filepath.Join(reviewRunDir(root, run.ID), name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected protocol artifact %s: %v", name, err)
		}
	}
}

func TestReviewApprovalLedgerChangedPathsIgnoresArchivedPatchFromPreviousTurn(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "", "", "", "default")
	session.Messages = []Message{
		{
			Role: "user",
			Text: "cmd/app/main.go를 수정해",
		},
		{
			Role:  "assistant",
			Phase: messagePhaseFinalAnswer,
			Text:  "수정 완료",
		},
		{
			Role: "user",
			Text: "현재 상태만 알려줘",
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-code-old",
		Goal:   "cmd/app/main.go를 수정해",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-code-old-001",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "cmd/app/main.go",
				Operation: "write_file",
			}},
		}},
	}}

	paths := reviewApprovalLedgerChangedPaths(session, ReviewRun{})
	if len(paths) != 0 {
		t.Fatalf("expected approval ledger to ignore previous-turn patch paths, got %#v", paths)
	}
}

func TestSingleModelPreWriteRecordsRFObligationStatus(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.cpp")
	if err := os.WriteFile(path, []byte("int main(){return 0;}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	provider := &scriptedProviderClient{replies: []ChatResponse{
		approvedReviewResponse("single model review approved the frozen diff"),
		approvedReviewResponse("single model second pass approved the frozen diff"),
	}}
	rt := reviewStateTestRuntime(root, provider)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:      "pre_write",
		Target:       reviewTargetChange,
		Request:      "fix main.cpp",
		Paths:        []string{path},
		ProvidedDiff: "- return 0;\n+ return 1;\n",
		RepairFindings: []ReviewFinding{{
			ID:          "RF-100",
			Severity:    reviewSeverityHigh,
			Category:    "correctness",
			Path:        "main.cpp",
			Title:       "return value is wrong",
			RequiredFix: "return the requested value",
		}},
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if !run.SingleModelPolicy.RequiresRFObligationStatus {
		t.Fatalf("expected RF obligation status requirement, got %#v", run.SingleModelPolicy)
	}
	if run.Gate.Verdict == reviewVerdictNeedsRevision || run.Gate.Verdict == reviewVerdictInsufficientEvidence || run.Gate.Verdict == reviewVerdictBlocked {
		t.Fatalf("single-model pre-write review should not block after recording RF status, got %#v", run.Gate)
	}
	if len(run.RepairFindings) != 1 || run.RepairFindings[0].ResolutionStatus != "evidence_unconfirmed" {
		t.Fatalf("expected evidence_unconfirmed repair status to be recorded without an explicit RF reference, got %#v", run.RepairFindings)
	}
	if reviewFindingsContainTitle(run.Findings, "Single-model pre-write review lacks repair obligation status") {
		t.Fatalf("did not expect RF status blocker after annotation, got %#v", run.Findings)
	}
}

func TestSingleModelPreWriteWithoutUsableReviewerBlocksMissingRFObligationStatus(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.cpp")
	if err := os.WriteFile(path, []byte("int main(){return 0;}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	rt := reviewStateTestRuntime(root, nil)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:      "pre_write",
		Target:       reviewTargetChange,
		Request:      "fix main.cpp",
		Paths:        []string{path},
		ProvidedDiff: "- return 0;\n+ return 1;\n",
		NoModel:      true,
		RepairFindings: []ReviewFinding{{
			ID:          "RF-100",
			Severity:    reviewSeverityHigh,
			Category:    "correctness",
			Path:        "main.cpp",
			Title:       "return value is wrong",
			RequiredFix: "return the requested value",
		}},
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if !run.SingleModelPolicy.RequiresRFObligationStatus {
		t.Fatalf("expected RF obligation status requirement, got %#v", run.SingleModelPolicy)
	}
	if run.Gate.Verdict != reviewVerdictNeedsRevision && run.Gate.Verdict != reviewVerdictInsufficientEvidence {
		t.Fatalf("expected single-model pre-write review to block without usable RF status review, got %#v", run.Gate)
	}
	if !reviewFindingsContainTitle(run.Findings, "Single-model pre-write review lacks repair obligation status") {
		t.Fatalf("expected RF status blocker, got %#v", run.Findings)
	}
}

func TestSingleModelPreWriteDoesNotAddRFStatusBlockerOnRequiredReviewerFailure(t *testing.T) {
	run := ReviewRun{
		Trigger: "pre_write",
		SingleModelPolicy: SingleModelReviewPolicy{
			Enabled:                    true,
			RequiresRFObligationStatus: true,
		},
		ReviewerRuns: []ReviewReviewerRun{{
			Role:         "primary_reviewer",
			Kind:         "cross",
			Status:       "failed",
			ModelQuality: reviewModelQualityFailed,
			Error:        "stream error: stream ID 27; INTERNAL_ERROR; received from peer",
		}},
		RepairFindings: []ReviewFinding{{
			ID:          "RF-100",
			Severity:    reviewSeverityHigh,
			Category:    "correctness",
			Path:        "main.cpp",
			Title:       "return value is wrong",
			RequiredFix: "return the requested value",
		}},
	}
	if got := singleModelPreWritePolicyFindings(run); len(got) != 0 {
		t.Fatalf("required reviewer failure should own the gate reason, got %#v", got)
	}
	if !reviewRunHasRequiredReviewerFailure(run) {
		t.Fatalf("expected required reviewer failure to remain detectable")
	}
}

func TestSingleModelPreWriteReviewUsesFrozenDiff(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.cpp")
	if err := os.WriteFile(path, []byte("int main(){return 0;}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	provider := &scriptedProviderClient{replies: []ChatResponse{approvedReviewResponse("single model review approved without a frozen diff")}}
	rt := reviewStateTestRuntime(root, provider)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger: "pre_write",
		Target:  reviewTargetChange,
		Request: "fix main.cpp",
		Paths:   []string{path},
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if run.Gate.Action == reviewGateActionDiffPreviewAllowed {
		t.Fatalf("single-model pre-write review without frozen diff must not reach diff preview, got %#v", run.Gate)
	}
	if !reviewFindingsContainTitle(run.Findings, "Single-model pre-write review lacks a frozen diff") {
		t.Fatalf("expected frozen diff blocker, got %#v", run.Findings)
	}
}

func TestExternalLookupIntentRecordsBlockedLocalWebResearch(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "model", "", "default")
	agent := &Agent{Session: session}
	agent.recordExternalLookupIntents([]ToolCall{{
		Name:      "mcp__web_search",
		Arguments: `{"query":"latest API docs"}`,
	}}, "blocked_local_code_context", true)
	if len(session.ExternalLookupIntents) != 1 {
		t.Fatalf("expected blocked external lookup intent, got %#v", session.ExternalLookupIntents)
	}
	intent := session.ExternalLookupIntents[0]
	if !intent.Blocked || intent.Status != "blocked" || !strings.Contains(intent.Intent, "latest API docs") {
		t.Fatalf("unexpected external lookup intent: %#v", intent)
	}
	if len(session.ConversationEvents) == 0 || session.ConversationEvents[len(session.ConversationEvents)-1].Kind != conversationEventKindExternalLookup {
		t.Fatalf("expected external lookup conversation event, got %#v", session.ConversationEvents)
	}
}

func TestReviewLedgerConsistencyBlocksStaleFinalAnswer(t *testing.T) {
	root := t.TempDir()
	run := ReviewRun{
		ID:      "review-stale",
		Target:  reviewTargetChange,
		Trigger: "post_change",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"main.cpp"},
		},
		Evidence: ReviewEvidencePack{
			ChangedPaths: []string{"main.cpp"},
		},
		Freshness: ReviewFreshness{
			Stale:       true,
			StaleReason: "changed paths moved after review",
		},
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-001"},
		},
	}
	check := buildReviewLedgerConsistency(root, nil, run)
	if check.Status != reviewLedgerConsistencyBlocked {
		t.Fatalf("expected blocked consistency check, got %#v", check)
	}
	for _, want := range []string{"stale", "unresolved review blockers"} {
		if !strings.Contains(strings.Join(check.Blockers, " "), want) {
			t.Fatalf("expected consistency blocker %q, got %#v", want, check.Blockers)
		}
	}
}

func TestResumeSanityDetectsConflictingLatestUserRequest(t *testing.T) {
	root := t.TempDir()
	rt := reviewStateTestRuntime(root, nil)
	rt.session.AddMessage(Message{Role: "user", Text: "중단하고 답변만 해"})
	run := ReviewRun{
		ID:      "review-resume",
		Trigger: "pre_write",
		ActionEnvelopes: []ReviewActionEnvelope{{
			ActionID:   "RAE-001",
			ActionType: reviewActionPreWriteReview,
			Status:     "completed",
		}},
		StateTransitions: []ReviewStateTransition{{
			To: reviewStateDiffPreview,
		}},
		ArtifactIntegrity: ReviewArtifactIntegrity{
			EvidenceHash: "evidence",
			ProposalHash: "proposal",
		},
	}
	check := buildReviewResumeSanityCheck(root, rt, run)
	if check.Status != reviewResumeSanityConflict || strings.TrimSpace(check.ConflictReason) == "" {
		t.Fatalf("expected resume conflict, got %#v", check)
	}
}

func TestResumeRequestConflictReasonHandlesUppercaseOnlyAnswer(t *testing.T) {
	for _, latest := range []string{
		"Only answer yes or no",
		"ONLY ANSWER yes or no",
		"only answer yes or no",
	} {
		if reason := reviewResumeRequestConflictReason(latest, ReviewRun{}); !strings.Contains(reason, "response mode") {
			t.Fatalf("expected response-mode conflict for %q, got %q", latest, reason)
		}
	}
}

func TestSingleModelReviewRecordsIndependenceLevel(t *testing.T) {
	policy := buildSingleModelReviewPolicy(ReviewRun{Trigger: "pre_write"}, false)
	if !policy.Enabled || policy.IndependenceLevel != "single_model" || !policy.RequiresPreWriteSelfReview {
		t.Fatalf("expected single-model independence policy, got %#v", policy)
	}
}

func TestReviewArtifactAtomicWriteDoesNotCorruptLatest(t *testing.T) {
	root := t.TempDir()
	rt := reviewStateTestRuntime(root, nil)
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Target:       reviewTargetChange,
		Request:      "review supplied code",
		ProvidedCode: "package main\n",
		NoModel:      true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	latestPath := filepath.Join(reviewArtifactRoot(root), "latest.json")
	if err := os.WriteFile(latestPath, []byte("{broken"), 0o644); err != nil {
		t.Fatalf("corrupt latest: %v", err)
	}
	recovered, _, ok, err := loadLatestReviewRun(root)
	if err != nil {
		t.Fatalf("loadLatestReviewRun: %v", err)
	}
	if !ok || recovered.ID != run.ID {
		t.Fatalf("expected latest recovery to keep valid atomic artifact, got ok=%t run=%#v", ok, recovered)
	}
}

func TestSingleModelReviewModeDoesNotRequireCrossReviewer(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.cpp")
	if err := os.WriteFile(path, []byte("int main(){return 0;}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	provider := &scriptedProviderClient{replies: []ChatResponse{
		approvedReviewResponse("single model review approved the frozen diff"),
		approvedReviewResponse("single model second pass approved the frozen diff"),
	}}
	rt := reviewStateTestRuntime(root, provider)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:      "pre_write",
		Target:       reviewTargetChange,
		Request:      "fix main.cpp",
		Paths:        []string{path},
		ProvidedDiff: "- return 0;\n+ return 1;\n",
		EditProposals: []EditProposal{{
			File:            "main.cpp",
			Operation:       "apply_patch",
			ExpectedPreview: "- return 0;\n+ return 1;\n",
		}},
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if !run.SingleModelPolicy.Enabled {
		t.Fatalf("expected single-model review policy, got %#v", run.SingleModelPolicy)
	}
	if run.SingleModelPolicy.IndependenceLevel != "single_model" {
		t.Fatalf("expected single_model independence, got %#v", run.SingleModelPolicy)
	}
	if reviewRunHasRequiredReviewerFailure(run) {
		t.Fatalf("single-model mode must not create required reviewer failure, got %#v", run.Findings)
	}
	if run.Gate.Action != reviewGateActionDiffPreviewAllowed {
		t.Fatalf("expected diff preview gate action, got %#v", run.Gate)
	}
	if !reviewTransitionsInclude(run.StateTransitions, reviewStateNoCrossReview) {
		t.Fatalf("expected no_cross_review transition, got %#v", run.StateTransitions)
	}
}

func TestSameProviderModelReviewerConfigDoesNotCountAsDistinctCrossReviewer(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Provider = "openai-codex-subscription"
	cfg.Model = "gpt-5.5"
	cfg.ReasoningEffort = "xhigh"
	cfg.Review.RoleModels = map[string]ReviewModelConfig{
		"primary_reviewer": {
			Provider:        "openai-codex-subscription",
			Model:           "gpt-5.5",
			ReasoningEffort: "high",
		},
	}
	rt := &runtimeState{
		cfg: cfg,
		agent: &Agent{
			Config: cfg,
			Client: &scriptedProviderClient{},
		},
	}

	if reviewRuntimeHasDistinctCrossReviewer(rt) {
		t.Fatalf("same provider/model reviewer config should be treated as single-model mode")
	}
	if !reviewModelConfigMatchesMain(cfg, cfg.Review.RoleModels["primary_reviewer"]) {
		t.Fatalf("expected same provider/model role config to match main route")
	}
}

func TestSameProviderModelReviewerConfigInheritsMainBaseURL(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"
	cfg.BaseURL = "https://chatgpt.example.test/backend-api/codex/"
	cfg.Review.RoleModels = map[string]ReviewModelConfig{
		"primary_reviewer": {
			Provider: "openai-codex",
			Model:    "gpt-5.5",
		},
	}
	rt := &runtimeState{
		cfg: cfg,
		agent: &Agent{
			Config: cfg,
			Client: &scriptedProviderClient{},
		},
	}

	if !reviewModelConfigMatchesMain(cfg, cfg.Review.RoleModels["primary_reviewer"]) {
		t.Fatalf("expected empty role base URL to inherit the main route base URL")
	}
	if reviewRuntimeHasDistinctCrossReviewer(rt) {
		t.Fatalf("same provider/model reviewer config with inherited base URL should be single-model mode")
	}
}

func TestDifferentBaseURLReviewerConfigCountsAsDistinctCrossReviewer(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"
	cfg.BaseURL = "https://chatgpt.example.test/backend-api/codex/"
	cfg.Review.RoleModels = map[string]ReviewModelConfig{
		"primary_reviewer": {
			Provider: "openai-codex",
			Model:    "gpt-5.5",
			BaseURL:  "https://other.example.test/backend-api/codex/",
		},
	}
	rt := &runtimeState{
		cfg: cfg,
		agent: &Agent{
			Config: cfg,
			Client: &scriptedProviderClient{},
		},
	}

	if reviewModelConfigMatchesMain(cfg, cfg.Review.RoleModels["primary_reviewer"]) {
		t.Fatalf("expected different role base URL to be treated as a distinct route")
	}
	if !reviewRuntimeHasDistinctCrossReviewer(rt) {
		t.Fatalf("different reviewer route should count as an independent cross reviewer")
	}
}

func TestReviewerClientSameProviderModelDoesNotCountAsDistinctCrossReviewer(t *testing.T) {
	cfg := Config{
		Provider:   "scripted",
		Model:      "main-model",
		AutoLocale: boolPtr(false),
	}
	rt := &runtimeState{
		cfg: cfg,
		agent: &Agent{
			Config:         cfg,
			Client:         &scriptedProviderClient{},
			ReviewerClient: &scriptedProviderClient{},
			ReviewerModel:  "main-model",
		},
	}

	if reviewRuntimeHasDistinctCrossReviewer(rt) {
		t.Fatalf("same provider/model reviewer client should not be treated as independent cross reviewer")
	}
	if !reviewClientMatchesMain(rt, rt.agent.ReviewerClient, rt.agent.ReviewerModel) {
		t.Fatalf("expected same provider/model reviewer client to match main")
	}
}

func TestReviewerClientSameRouteInheritsMainBaseURL(t *testing.T) {
	cfg := Config{
		Provider:   "openai-codex",
		Model:      "gpt-5.5",
		BaseURL:    "https://chatgpt.example.test/backend-api/codex/",
		AutoLocale: boolPtr(false),
	}
	mainClient := &namedScriptedProviderClient{scriptedProviderClient: &scriptedProviderClient{}, name: "openai-codex"}
	reviewerClient := &namedScriptedProviderClient{scriptedProviderClient: &scriptedProviderClient{}, name: "openai-codex"}
	rt := &runtimeState{
		cfg: cfg,
		agent: &Agent{
			Config:         cfg,
			Client:         mainClient,
			ReviewerClient: reviewerClient,
			ReviewerModel:  "gpt-5.5",
		},
	}

	if !reviewClientMatchesMain(rt, reviewerClient, "gpt-5.5") {
		t.Fatalf("metadata-less same provider/model reviewer should inherit main base URL")
	}
	if reviewRuntimeHasDistinctCrossReviewer(rt) {
		t.Fatalf("metadata-less same route reviewer should not count as a distinct cross reviewer")
	}
}

func TestAuxReviewerClientExplicitDifferentBaseURLCountsAsDistinct(t *testing.T) {
	cfg := Config{
		Provider:   "openai-codex",
		Model:      "gpt-5.5",
		BaseURL:    "https://chatgpt.example.test/backend-api/codex/",
		AutoLocale: boolPtr(false),
	}
	mainClient := &reasoningCaptureProviderClient{
		name: "openai-codex",
		meta: ModelRouteMetadata{
			Provider: "openai-codex",
			BaseURL:  cfg.BaseURL,
		},
	}
	auxClient := &reasoningCaptureProviderClient{
		name: "openai-codex",
		meta: ModelRouteMetadata{
			Provider: "openai-codex",
			BaseURL:  "https://other.example.test/backend-api/codex/",
		},
	}
	rt := &runtimeState{
		cfg: cfg,
		agent: &Agent{
			Config:            cfg,
			Client:            mainClient,
			AuxReviewerClient: auxClient,
			AuxReviewerModel:  "gpt-5.5",
		},
	}

	if reviewClientMatchesMain(rt, auxClient, "gpt-5.5") {
		t.Fatalf("explicit different base URL should remain distinct")
	}
	if !reviewRuntimeHasDistinctCrossReviewer(rt) {
		t.Fatalf("explicit different base URL should count as a distinct cross reviewer")
	}
}

func TestAuxReviewerClientSameProviderModelDoesNotCountAsDistinctCrossReviewer(t *testing.T) {
	cfg := Config{
		Provider:   "scripted",
		Model:      "main-model",
		AutoLocale: boolPtr(false),
	}
	rt := &runtimeState{
		cfg: cfg,
		agent: &Agent{
			Config:            cfg,
			Client:            &scriptedProviderClient{},
			AuxReviewerClient: &scriptedProviderClient{},
			AuxReviewerModel:  "main-model",
		},
	}

	if reviewRuntimeHasDistinctCrossReviewer(rt) {
		t.Fatalf("same provider/model aux reviewer client should not be treated as independent cross reviewer")
	}
	if !reviewClientMatchesMain(rt, rt.agent.AuxReviewerClient, rt.agent.AuxReviewerModel) {
		t.Fatalf("expected same provider/model aux reviewer client to match main")
	}
}

func TestReviewMCPResponseIncludesProtocolContract(t *testing.T) {
	run := ReviewRun{
		ID:            "review-protocol",
		MachineStatus: reviewMachineStatusOK,
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
			Action:  reviewGateActionFinalSummary,
		},
		ActionEnvelopes: []ReviewActionEnvelope{{
			ActionID:   "RAE-001",
			ActionType: reviewActionCollectEvidence,
			Actor:      "harness",
			Status:     "completed",
		}},
		ApprovalLedger: ReviewApprovalLedger{
			ReviewGateApproved: true,
		},
		CapabilityManifest: ReviewCapabilityManifest{
			LocalFileRead:         "available",
			SingleModelReviewMode: "available",
		},
		ExternalLookupIntents: []ReviewExternalLookupIntent{{
			ID:       "ELI-001",
			ToolName: "web_search",
			Intent:   "query=example",
			Status:   "blocked",
			Blocked:  true,
		}},
		ArtifactIntegrity: ReviewArtifactIntegrity{
			HashAlgorithm: "sha256",
			EvidenceHash:  "abc",
		},
		LedgerConsistency: ReviewLedgerConsistencyCheck{
			Status: reviewLedgerConsistencyOK,
		},
		ResumeSanity: ReviewResumeSanityCheck{
			Status: reviewResumeSanityOK,
		},
		SingleModelPolicy: SingleModelReviewPolicy{
			Enabled:           true,
			IndependenceLevel: "single_model",
		},
		StateTransitions: []ReviewStateTransition{{
			ID:    "RST-001",
			To:    reviewStateCollectEvidence,
			Actor: "harness",
		}},
	}
	rendered := renderReviewMCPResponse(run, 20000)
	for _, want := range []string{
		"action_envelopes",
		"approval_ledger",
		"capability_manifest",
		"single_model_policy",
		"external_lookup_intents",
		"artifact_integrity",
		"ledger_consistency",
		"resume_sanity",
		"state_transitions",
		"diff_preview_shown",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected MCP response to contain %q, got %s", want, rendered)
		}
	}
}

func TestReviewModelPlanRecordsCapabilityProfileAndRouteHealth(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.cpp")
	if err := os.WriteFile(path, []byte("int main(){return 0;}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	provider := &scriptedProviderClient{replies: []ChatResponse{approvedReviewResponse("main review approved")}}
	rt := reviewStateTestRuntime(root, provider)
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Target:              reviewTargetChange,
		Request:             "review main.cpp",
		Paths:               []string{path},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(run.ModelPlan.CapabilityProfiles) == 0 {
		t.Fatalf("expected capability profiles, got %#v", run.ModelPlan)
	}
	if run.ModelPlan.CapabilityProfiles[0].ModelPattern != "main-model" {
		t.Fatalf("expected model pattern in capability profile, got %#v", run.ModelPlan.CapabilityProfiles[0])
	}
	if len(run.ModelPlan.RouteHealth) == 0 {
		t.Fatalf("expected route health from reviewer runs, got %#v", run.ModelPlan)
	}
	if !strings.Contains(renderReviewRunMarkdown(run), "Model Route Capability") {
		t.Fatalf("expected markdown to render route capability section")
	}
}

func TestReviewApprovalLedgerRequiresPassedVerificationStep(t *testing.T) {
	rt := &runtimeState{
		session: &Session{
			LastVerification: &VerificationReport{
				Steps: []VerificationStep{{
					Label:  "msbuild",
					Status: VerificationSkipped,
				}},
			},
		},
	}
	run := ReviewRun{
		Trigger: "post_change",
		Gate: GateDecision{
			Verdict: reviewVerdictApproved,
		},
	}
	ledger := buildReviewApprovalLedger(rt, run)
	if ledger.VerificationPassed {
		t.Fatalf("skipped-only verification must not count as passed: %#v", ledger)
	}

	rt.session.LastVerification = &VerificationReport{
		Steps: []VerificationStep{{
			Label:  "msbuild",
			Status: VerificationPassed,
		}},
	}
	ledger = buildReviewApprovalLedger(rt, run)
	if !ledger.VerificationPassed {
		t.Fatalf("passed verification step should count as passed: %#v", ledger)
	}

	rt.session.LastVerification = &VerificationReport{
		ChangedPaths: []string{"old.cpp"},
		Steps: []VerificationStep{{
			Label:  "msbuild",
			Status: VerificationPassed,
		}},
	}
	run.ChangeSet.ChangedPaths = []string{"new.cpp"}
	ledger = buildReviewApprovalLedger(rt, run)
	if ledger.VerificationPassed {
		t.Fatalf("verification for different changed paths must not count as passed: %#v", ledger)
	}
}

func TestUIPolishReviewRequiresPrimaryForBehavioralFormats(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cases := []struct {
		name         string
		path         string
		wantPrimary  bool
		wantDesignOK bool
	}{
		{name: "config", path: "src/ui/config.json", wantPrimary: true},
		{name: "template", path: "src/views/page.html", wantPrimary: true},
		{name: "css", path: "src/styles/page.css", wantPrimary: true, wantDesignOK: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := planReviewModels(cfg, ReviewRun{
				Mode: reviewModeUIPolish,
				ChangeSet: ReviewChangeSet{
					ChangedPaths: []string{tc.path},
				},
			})
			hasPrimary := reviewStringSliceContainsCI(plan.RequiredRoles, "primary_reviewer")
			if hasPrimary != tc.wantPrimary {
				t.Fatalf("primary coverage for %s = %t, want %t; plan=%#v", tc.path, hasPrimary, tc.wantPrimary, plan)
			}
			if tc.wantDesignOK && !reviewStringSliceContainsCI(plan.RequiredLenses, "design") {
				t.Fatalf("expected design lens for %s, plan=%#v", tc.path, plan)
			}
		})
	}
}

func TestCrossReviewModelStatusReportsRouteHealth(t *testing.T) {
	root := t.TempDir()
	rt := reviewStateTestRuntime(root, nil)
	var output strings.Builder
	rt.writer = &output
	rt.session.ReviewRouteHealth = []ReviewRouteHealth{{
		Role:           "cross_reviewer",
		Model:          "scripted / slow-reviewer",
		RecentRuns:     3,
		TimeoutRate:    0.67,
		LastStatus:     "failed",
		LastQuality:    reviewModelQualityFailed,
		Recommendation: "route is timeout-heavy; reduce strict retries and consider a closer or stronger reviewer",
	}}
	rt.printCrossReviewModelStatus()
	rendered := output.String()
	for _, want := range []string{
		"Route Health",
		"cross_reviewer",
		"timeout_rate=0.67",
		"next_timeout=8m0s",
		"timeout-heavy",
		"next reviewer call auto-extends timeout",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected cross review model status to contain %q, got %q", want, rendered)
		}
	}
}

func TestReviewModelCapabilityProfileControlsTimeout(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"
	cfg.ReasoningEffort = "high"
	cfg.Review.RoleModels = map[string]ReviewModelConfig{
		"cross_reviewer": {
			Provider: "deepseek",
			Model:    "deepseek-reviewer",
		},
	}
	run := ReviewRun{
		Trigger: "pre_write",
	}
	timeout := reviewModelSoftTimeoutForRun(cfg, run, ReviewReviewerRun{
		Kind: "cross",
		Role: "cross_reviewer",
	})
	if timeout != reviewCloudCrossSoftTimeout {
		t.Fatalf("expected cloud capability timeout, got %s", timeout)
	}
	profile := reviewModelCapabilityProfile("cross_reviewer", "deepseek", "deepseek-reviewer", "")
	if profile.CapabilityRank == 0 || profile.RecommendedTimeoutMS != reviewCloudCrossSoftTimeout.Milliseconds() {
		t.Fatalf("expected capability profile to drive timeout/rank, got %#v", profile)
	}
}

func TestReviewRouteHealthSuppressesRepeatedStrictRetry(t *testing.T) {
	root := t.TempDir()
	rt := reviewStateTestRuntime(root, nil)
	rt.session.ReviewRouteHealth = []ReviewRouteHealth{{
		Role:        "cross_reviewer",
		Model:       "scripted / reviewer-model",
		RecentRuns:  3,
		WeakRate:    0.67,
		LastStatus:  "completed",
		LastQuality: reviewModelQualityWeak,
	}}
	if !reviewRouteHealthSuppressesStrictRetry(rt, ReviewReviewerRun{
		Role:  "cross_reviewer",
		Kind:  "cross",
		Model: "scripted / reviewer-model",
	}) {
		t.Fatalf("expected weak repeated route health to suppress strict retry")
	}
}

func TestReviewRouteHealthSkipsRecentlyEmptyInitialReviewerCall(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{replies: []ChatResponse{approvedReviewResponse("should not be called")}}
	rt := reviewStateTestRuntime(root, provider)
	rt.session.ReviewRouteHealth = []ReviewRouteHealth{{
		Role:              "primary_reviewer",
		Model:             "scripted / main-model",
		RecentRuns:        1,
		EmptyResponseRate: 1,
		LastStatus:        "failed",
		LastQuality:       reviewModelQualityFailed,
		Recommendation:    "route returned empty output recently; retry with a different reviewer",
	}}
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:      "pre_write",
		Target:       reviewTargetChange,
		Request:      "review supplied diff",
		ProvidedDiff: "diff --git a/main.cpp b/main.cpp\n+int main(){return 0;}\n",
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("expected unhealthy route to skip model call, got %d requests", len(provider.requests))
	}
	if len(run.ReviewerRuns) == 0 {
		t.Fatalf("expected skipped reviewer run to be recorded")
	}
	if !strings.Contains(run.ReviewerRuns[0].Error, "route health skipped") {
		t.Fatalf("expected route-health skip error, got %#v", run.ReviewerRuns[0])
	}
	if strings.Contains(run.ReviewerRuns[0].Error, "timeout") || strings.Contains(run.ReviewerRuns[0].Error, "empty response") {
		t.Fatalf("skipped route should not masquerade as a fresh provider timeout/empty response, got %q", run.ReviewerRuns[0].Error)
	}
	if run.Result.ModelQuality != reviewModelQualityFailed {
		t.Fatalf("expected skipped reviewer quality to stay failed, got %q", run.Result.ModelQuality)
	}
	if !reviewRunHasRequiredReviewerFailure(run) {
		t.Fatalf("expected skipped required reviewer to block the gate")
	}
}

func TestReviewRouteHealthTimeoutExtendsNextCallWithoutSkipping(t *testing.T) {
	root := t.TempDir()
	rt := reviewStateTestRuntime(root, nil)
	rt.cfg.Review.RoleModels = map[string]ReviewModelConfig{
		"cross_reviewer": {
			Provider: "anthropic-claude-cli",
			Model:    "sonnet",
		},
	}
	rt.session.ReviewRouteHealth = []ReviewRouteHealth{{
		Role:           "cross_reviewer",
		Model:          "anthropic-claude-cli / sonnet",
		RecentRuns:     1,
		TimeoutRate:    1,
		LastStatus:     "failed",
		LastQuality:    reviewModelQualityFailed,
		Recommendation: "route timed out recently; consider a stronger or closer reviewer route",
	}}
	reviewerRun := ReviewReviewerRun{
		Role:  "cross_reviewer",
		Kind:  "cross",
		Model: "anthropic-claude-cli / sonnet",
	}
	if _, ok := reviewRouteHealthSkipsInitialModelCall(rt, reviewerRun); ok {
		t.Fatalf("timeout-only health should retry with an extended timeout instead of skipping the reviewer call")
	}
	timeout := reviewModelSoftTimeoutForRun(rt.cfg, ReviewRun{}, reviewerRun, rt.session.ReviewRouteHealth)
	if timeout != reviewAdaptiveTimeoutCrossSoftTimeout {
		t.Fatalf("expected adaptive timeout after recent timeout, got %s", timeout)
	}
}

func TestReviewRouteHealthTimeoutRateDoesNotDependOnRecommendationText(t *testing.T) {
	item := ReviewRouteHealth{
		Role:        "cross_reviewer",
		Model:       "anthropic-claude-cli / sonnet",
		RecentRuns:  1,
		TimeoutRate: 1,
		LastStatus:  "failed",
		LastQuality: reviewModelQualityFailed,
	}
	if !reviewRouteHealthNeedsAdaptiveTimeout(item) {
		t.Fatalf("timeout rate plus failed status should drive adaptive timeout without recommendation text")
	}
	if timeout := reviewRouteHealthNextSoftTimeout(item); timeout != reviewAdaptiveTimeoutCrossSoftTimeout {
		t.Fatalf("expected adaptive timeout without recommendation text, got %s", timeout)
	}
}

func TestReviewRouteHealthActionUsesRoleSpecificClearCommand(t *testing.T) {
	item := ReviewRouteHealth{
		Role:        "security_reviewer",
		Model:       "anthropic-claude-cli / sonnet",
		RecentRuns:  1,
		TimeoutRate: 1,
		LastStatus:  "failed",
		LastQuality: reviewModelQualityFailed,
	}
	action := reviewRouteHealthActionHint(item)
	if !strings.Contains(action, "/model clear cross-review") {
		t.Fatalf("expected cross-route clear command, got %q", action)
	}
	crossAction := reviewRouteHealthActionHint(ReviewRouteHealth{
		Role:        "cross_reviewer",
		Model:       "anthropic-claude-cli / sonnet",
		RecentRuns:  1,
		TimeoutRate: 1,
		LastStatus:  "failed",
		LastQuality: reviewModelQualityFailed,
	})
	if !strings.Contains(crossAction, "/model clear cross-review") {
		t.Fatalf("expected synthetic cross reviewer to clear cross route, got %q", crossAction)
	}
}

func TestReviewRouteHealthAdaptiveTimeoutIsOneShotAfterSuccess(t *testing.T) {
	previous := ReviewRouteHealth{
		Role:           "cross_reviewer",
		Model:          "anthropic-claude-cli / sonnet",
		RecentRuns:     1,
		TimeoutRate:    1,
		LastStatus:     "failed",
		LastQuality:    reviewModelQualityFailed,
		LastTimeout:    true,
		Recommendation: "route timed out recently; consider a stronger or closer reviewer route",
	}
	latest := ReviewRouteHealth{
		Role:              "cross_reviewer",
		Model:             "anthropic-claude-cli / sonnet",
		RecentRuns:        1,
		UsableFindingRate: 1,
		LastStatus:        "completed",
		LastQuality:       reviewModelQualityUsable,
	}
	combined := combineReviewRouteHealth(previous, latest, 8)
	combined.Recommendation = reviewRouteHealthRecommendation(combined)
	if combined.LastTimeout {
		t.Fatalf("latest successful call should consume the one-shot adaptive timeout marker")
	}
	if reviewRouteHealthNeedsAdaptiveTimeout(combined) {
		t.Fatalf("historical timeout rate should not keep extending every later call: %#v", combined)
	}
	if timeout := reviewRouteHealthNextSoftTimeout(combined); timeout != reviewCLICrossSoftTimeout {
		t.Fatalf("expected CLI base timeout after one-shot marker is consumed, got %s", timeout)
	}
}

func TestReviewRouteHealthDoesNotSkipAfterBadRateCoolsDown(t *testing.T) {
	root := t.TempDir()
	rt := reviewStateTestRuntime(root, nil)
	rt.session.ReviewRouteHealth = []ReviewRouteHealth{{
		Role:              "primary_reviewer",
		Model:             "scripted / main-model",
		RecentRuns:        3,
		EmptyResponseRate: 0.33,
		LastStatus:        "failed",
		LastQuality:       reviewModelQualityFailed,
	}}
	if _, ok := reviewRouteHealthSkipsInitialModelCall(rt, ReviewReviewerRun{
		Role:  "primary_reviewer",
		Kind:  "main",
		Model: "scripted / main-model",
	}); ok {
		t.Fatalf("expected cooled-down route health to allow a fresh reviewer attempt")
	}
}

func TestLoopSignatureRendersRepeatedReadAndToolFailure(t *testing.T) {
	readSig := renderLoopSignature(loopSignatureForRepeatedRead("cmd/kernforge/review_harness.go", 2))
	if !strings.Contains(readSig, "kind=repeated_read_file") ||
		!strings.Contains(readSig, "repeat_count=2") ||
		!strings.Contains(readSig, "required_shift=") {
		t.Fatalf("expected repeated read loop signature, got %q", readSig)
	}
	failureSig := renderLoopSignature(loopSignatureForToolFailure("patch_format_empty_update", 3))
	if !strings.Contains(failureSig, "kind=repeated_tool_error") ||
		!strings.Contains(failureSig, "patch_format_empty_update") {
		t.Fatalf("expected repeated tool failure loop signature, got %q", failureSig)
	}
}

func TestMissingReviewerRouteStartsSingleModelReviewMode(t *testing.T) {
	root := t.TempDir()
	rt := reviewStateTestRuntime(root, &scriptedProviderClient{replies: []ChatResponse{approvedReviewResponse("main review approved")}})
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Target:       reviewTargetChange,
		Request:      "review supplied code",
		ProvidedCode: "package main\n",
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if !run.SingleModelPolicy.Enabled || run.SingleModelPolicy.IndependenceLevel != "single_model" {
		t.Fatalf("expected single-model review mode, got %#v", run.SingleModelPolicy)
	}
	if strings.Contains(strings.Join(run.ModelPlan.MissingRoles, ","), "cross_reviewer") {
		t.Fatalf("single-model mode should not report missing cross reviewer as blocker, got %#v", run.ModelPlan)
	}
}

func TestReviewLatestRecoveryUsesMostRecentValidRun(t *testing.T) {
	root := t.TempDir()
	rt := reviewStateTestRuntime(root, nil)
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Target:       reviewTargetChange,
		Request:      "review supplied code",
		ProvidedCode: "package main\n",
		NoModel:      true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	latestPath := filepath.Join(reviewArtifactRoot(root), "latest.json")
	if err := os.WriteFile(latestPath, []byte("{broken"), 0o644); err != nil {
		t.Fatalf("corrupt latest: %v", err)
	}
	recovered, recoveredPath, ok, err := loadLatestReviewRun(root)
	if err != nil {
		t.Fatalf("loadLatestReviewRun: %v", err)
	}
	if !ok {
		t.Fatalf("expected latest recovery to find valid run")
	}
	if recovered.ID != run.ID {
		data, _ := json.MarshalIndent(recovered, "", "  ")
		t.Fatalf("expected recovered run %s from %s, got %s from %s", run.ID, reviewRunDir(root, run.ID), string(data), recoveredPath)
	}
	if !strings.HasSuffix(filepath.ToSlash(recoveredPath), "/review.json") {
		t.Fatalf("expected recovery path to point to review.json, got %s", recoveredPath)
	}
}

func reviewStateTestRuntime(root string, provider *scriptedProviderClient) *runtimeState {
	cfg := DefaultConfig(root)
	if provider != nil {
		cfg.Provider = "scripted"
		cfg.Model = "main-model"
	}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   NewSession(root, cfg.Provider, cfg.Model, "", "default"),
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	return agent.reviewHarnessRuntime(root)
}

func reviewTransitionsInclude(transitions []ReviewStateTransition, target string) bool {
	for _, transition := range transitions {
		if strings.EqualFold(strings.TrimSpace(transition.To), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}
