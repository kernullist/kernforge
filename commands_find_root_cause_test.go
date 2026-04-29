package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildRootCauseGoalIncludesFuzzLikeInvestigationRequirements(t *testing.T) {
	goal := buildRootCauseGoal("party limit can be bypassed after invite and kick churn")
	for _, needle := range []string{
		"Find the most likely root cause",
		"fuzzing-driven bug investigation",
		"1 to 8 parallel worker shards",
		"inputs and persisted state",
		"five-stage causal chain",
		"deep-verify reviewer-approved candidates",
		"write an audit trail",
	} {
		if !strings.Contains(goal, needle) {
			t.Fatalf("expected root-cause goal to include %q", needle)
		}
	}
}

func TestRootCausePromptClarityAcceptsConcreteSymptoms(t *testing.T) {
	for _, problem := range []string{
		"내 게임에서 파티원을 초대하고 추방하다 보면 파티원 제한 숫자를 넘어서서 파티원을 초대할 수 있게 돼",
		"내 Win32 서비스 프로세스가 sc stop으로 종료되지 않아",
		"가끔 에이전트가 내가 요청한 문서 파일을 생성하질 않아",
	} {
		clarity := analyzeRootCausePromptClarity(problem)
		if !clarity.Clear {
			t.Fatalf("expected clear root-cause prompt for %q, got %#v", problem, clarity.UnclearParts)
		}
	}
}

func TestParseFindRootCauseCommandArgsExtractsPatternPacks(t *testing.T) {
	options := parseFindRootCauseCommandArgs(`--pattern-pack ".kernforge/root cause/pattern packs/local.json" --pattern-pack=C:/packs/root.json In service, when sc stop runs, expected stop but process remains running`)
	if len(options.PatternPackPaths) != 2 {
		t.Fatalf("expected two pattern packs, got %#v", options.PatternPackPaths)
	}
	if !strings.Contains(options.PatternPackPaths[0], "root cause") {
		t.Fatalf("expected quoted pattern pack path to be preserved, got %#v", options.PatternPackPaths)
	}
	if !strings.Contains(options.Problem, "sc stop") {
		t.Fatalf("expected problem text to be preserved, got %q", options.Problem)
	}
}

func TestRootCausePromptClarityRejectsVagueSymptoms(t *testing.T) {
	clarity := analyzeRootCausePromptClarity("가끔 실패해")
	if clarity.Clear {
		t.Fatalf("expected vague root-cause prompt to be rejected")
	}
	for _, needle := range []string{
		"too short",
		"Trigger or reproduction path",
		"Expected behavior",
		"observed 가끔 실패해",
		"/find-root-cause",
	} {
		joined := strings.Join(append(clarity.UnclearParts, clarity.SuggestedCommand), "\n")
		if !strings.Contains(joined, needle) {
			t.Fatalf("expected unclear prompt response to include %q, got %#v", needle, clarity)
		}
	}
}

func TestParseRootCausePromptClarityPayload(t *testing.T) {
	raw := `{"clear":false,"unclear_parts":["Expected behavior is unclear."],"suggested_command":"/find-root-cause In agent, when prompt asks for a document, expected a file, but observed no file.","reason":"missing expected behavior"}`
	clarity, ok := parseRootCausePromptClarityPayload(raw)
	if !ok {
		t.Fatalf("expected clarity payload to parse")
	}
	if clarity.Clear || !clarity.ModelChecked {
		t.Fatalf("unexpected clarity result: %#v", clarity)
	}
	if !strings.Contains(clarity.SuggestedCommand, "/find-root-cause") {
		t.Fatalf("expected suggested command, got %#v", clarity)
	}
}

func TestHelpDetailIncludesFindRootCauseWorkflow(t *testing.T) {
	detail, ok := HelpDetail("find-root-cause")
	if !ok {
		t.Fatalf("expected find-root-cause help detail to resolve")
	}
	for _, needle := range []string{
		"/find-root-cause <problem description>",
		"1-8 worker agents",
		"too ambiguous",
		"input parameters",
		"DB/config values",
		"indexed symbols",
		"evidence_requests",
		"symbol-aware focused source excerpts",
		"Deterministic quality gates",
		"clusters",
		"probes",
		"root_cause_audit",
		"confidence",
	} {
		if !strings.Contains(detail, needle) {
			t.Fatalf("expected find-root-cause detail to include %q", needle)
		}
	}
}

func TestRootCauseCodeMatchesUsePathAndSemanticSymbols(t *testing.T) {
	snapshot := ProjectSnapshot{
		Files: []ScannedFile{
			{Path: "agent_write.go", Directory: ".", ImportanceScore: 1},
			{Path: "ui.go", Directory: ".", ImportanceScore: 1},
		},
		FilesByPath:      map[string]ScannedFile{},
		FilesByDirectory: map[string][]ScannedFile{},
	}
	for _, file := range snapshot.Files {
		snapshot.FilesByPath[file.Path] = file
		snapshot.FilesByDirectory[file.Directory] = append(snapshot.FilesByDirectory[file.Directory], file)
	}
	plan := RootCauseInvestigation{
		Symptom: RootCauseSymptomProfile{TriggerKeywords: []string{"document"}},
		Hypotheses: []RootCauseHypothesis{
			{ID: "H1", TargetSignals: []string{"write"}, TargetFiles: []string{"agent_write.go"}},
		},
	}
	matches := deriveRootCauseCodeMatches(snapshot, plan, "document file not created", 8)
	matches = augmentRootCauseCodeMatchesWithSemanticIndex(RootCauseInvestigation{Symptom: plan.Symptom, Hypotheses: plan.Hypotheses, CodeMatches: matches}, SemanticIndexV2{
		Symbols: []SymbolRecord{{ID: "func:WriteDocument", Name: "WriteDocument", File: "agent_write.go", Kind: "function"}},
	}, "document file not created", 8)
	if len(matches) == 0 || matches[0].File != "agent_write.go" {
		t.Fatalf("expected agent_write.go code match, got %#v", matches)
	}
	if !strings.Contains(strings.Join(matches[0].MatchedSignals, " "), "symbol:WriteDocument") {
		t.Fatalf("expected semantic symbol signal, got %#v", matches[0])
	}
}

func TestRootCauseModeAllowsSingleWorkerWhenConfigured(t *testing.T) {
	analyzer := &projectAnalyzer{
		analysisCfg: ProjectAnalysisConfig{
			MinAgents: 1,
			MaxAgents: 8,
		},
	}
	count := analyzer.estimateAgentCount(ProjectSnapshot{
		TotalFiles: 1,
		TotalLines: 20,
	})
	if count != 1 {
		t.Fatalf("expected tiny root-cause analysis to use one worker, got %d", count)
	}
}

func TestRootCauseDeepVerificationDisconfirmedRemovesCandidate(t *testing.T) {
	shards := []AnalysisShard{{ID: "shard-01", Name: "agent", PrimaryFiles: []string{"agent.go"}}}
	reports := []WorkerReport{
		{
			Title: "Agent path",
			RootCauseCandidates: []RootCauseCandidate{
				{
					Title:         "Document request classified as read-only",
					Confidence:    "high",
					EvidenceFiles: []string{"agent.go"},
				},
			},
		},
	}
	verified := []RootCauseDeepVerification{{ShardID: "shard-01", CandidateTitle: "Document request classified as read-only", Status: "disconfirmed"}}
	filtered := applyRootCauseDeepVerificationsToReports(ProjectSnapshot{AnalysisMode: "root-cause"}, shards, reports, verified)
	if len(filtered[0].RootCauseCandidates) != 0 {
		t.Fatalf("expected disconfirmed candidate to be removed, got %#v", filtered[0].RootCauseCandidates)
	}
	if len(filtered[0].Unknowns) == 0 || !strings.Contains(filtered[0].Unknowns[0], "disconfirmed") {
		t.Fatalf("expected disconfirmed note, got %#v", filtered[0].Unknowns)
	}
}

func TestRootCauseDeepVerificationEmptyConfidencePreservesCandidateConfidence(t *testing.T) {
	shard := AnalysisShard{ID: "shard-01", Name: "agent", PrimaryFiles: []string{"agent.go"}}
	candidate := RootCauseCandidate{
		Title:         "Document request classified as read-only",
		Confidence:    "high",
		EvidenceFiles: []string{"agent.go"},
	}
	verification := RootCauseDeepVerification{
		CandidateTitle: "Document request classified as read-only",
		ShardID:        "shard-01",
		Status:         "supported",
		CausalChain:    RootCauseCausalChain{Trigger: "document prompt", InvalidState: "read-only", StateTransition: "write disabled", MissingGuard: "no file check", UserVisibleSymptom: "file missing"},
	}
	merged := mergeRootCauseCandidateWithDeepVerification(candidate, verification, shard)
	if merged.Confidence != "high" {
		t.Fatalf("expected candidate confidence to be preserved, got %#v", merged)
	}
}

func TestParseWorkerReportPayloadPreservesRootCauseCandidates(t *testing.T) {
	shard := AnalysisShard{
		ID:           "shard-01",
		Name:         "agent_runtime",
		PrimaryFiles: []string{"agent.go", "analysis_context.go"},
	}
	raw := `{
  "report": {
    "title": "Agent document creation path",
    "scope_summary": "Checks whether document creation can be skipped.",
    "responsibilities": ["classify request intent"],
    "facts": ["read-only mode disables edit tools"],
    "inferences": ["document wording can route to read-only"],
    "key_files": ["agent.go"],
    "entry_points": ["Agent.Reply"],
    "internal_flow": ["prefersReadOnlyAnalysisIntent controls disabled tools"],
    "dependencies": [],
    "collaboration": ["analysis_context.go feeds agent.go"],
    "risks": ["file creation can be skipped"],
    "unknowns": [],
    "evidence_files": ["agent.go", "analysis_context.go"],
    "root_cause_candidates": [
      {
        "title": "Document request classified as read-only",
        "candidate_chain": ["request text contains document", "readOnlyAnalysis disables write_file"],
        "trigger_values": ["document", "문서화"],
        "expected_range": ["explicit create/write request should keep edit tools enabled"],
        "out_of_range_cases": ["ambiguous document wording routes to analysis-only"],
        "observed_failure_path": ["agent returns final answer without creating a file"],
        "evidence_files": ["agent.go", "analysis_context.go"],
        "disconfirming_evidence": ["write_file tool result for requested path would disprove no-write path"],
        "cannot_be_root_cause_if": ["readOnlyAnalysis is false for the failing request"],
        "required_runtime_observation": ["record disabled tool set during failing request"],
        "verification_steps": ["run the ambiguous document prompt and inspect tool availability"],
        "confidence": "high",
        "needs_cross_shard_evidence": ["write tool success is not required before final answer"]
      }
    ],
    "narrative": "Root cause candidate is value-oriented."
  }
}`
	report, ok := parseWorkerReportPayload(raw, shard)
	if !ok {
		t.Fatalf("expected worker report to parse")
	}
	if len(report.RootCauseCandidates) != 1 {
		t.Fatalf("expected one root-cause candidate, got %#v", report.RootCauseCandidates)
	}
	candidate := report.RootCauseCandidates[0]
	if candidate.Confidence != "high" {
		t.Fatalf("unexpected confidence: %q", candidate.Confidence)
	}
	if len(candidate.EvidenceFiles) != 2 {
		t.Fatalf("expected candidate evidence files to be preserved, got %#v", candidate.EvidenceFiles)
	}
	if len(candidate.CannotBeRootCauseIf) != 1 {
		t.Fatalf("expected falsification condition to be preserved, got %#v", candidate.CannotBeRootCauseIf)
	}
}

func TestBuildRootCauseAuditTrailRecordsCandidateDecision(t *testing.T) {
	shards := []AnalysisShard{{ID: "shard-01", Name: "agent", PrimaryFiles: []string{"agent.go"}}}
	reports := []WorkerReport{
		{
			Title: "Agent path",
			RootCauseCandidates: []RootCauseCandidate{
				{
					Title:         "Document request classified as read-only",
					Confidence:    "high",
					EvidenceFiles: []string{"agent.go"},
				},
			},
		},
	}
	reviews := []ReviewDecision{{Status: "approved", SymptomPossible: "yes", SymptomCausality: []string{"read-only path can skip file creation"}, CausalChainComplete: true, CausalChainStages: []string{"trigger", "invalid_state", "state_transition", "missing_guard", "user_visible_symptom"}}}
	joined := []RootCauseJoinedCandidate{{Title: "Document request classified as read-only", Classification: "root_cause"}}
	audit := buildRootCauseAuditTrail(ProjectSnapshot{AnalysisMode: "root-cause", RootCause: RootCauseInvestigation{Symptom: RootCauseSymptomProfile{Symptom: "file not created"}}}, shards, reports, reviews, reports, nil, joined)
	if len(audit.CandidateDecisions) != 1 {
		t.Fatalf("expected one audit decision, got %#v", audit)
	}
	if audit.CandidateDecisions[0].Decision != "included" {
		t.Fatalf("expected included audit decision, got %#v", audit.CandidateDecisions[0])
	}
	if !strings.Contains(buildRootCauseAuditDigest(audit), "Candidate Decisions") {
		t.Fatalf("expected audit digest to include candidate decisions")
	}
}

func TestRootCauseWorkerPromptIncludesCandidateChecklist(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:         t.TempDir(),
		AnalysisMode: "root-cause",
		FilesByPath: map[string]ScannedFile{
			"agent.go": {Path: "agent.go", LineCount: 10},
		},
	}
	shard := AnalysisShard{
		ID:           "shard-01",
		Name:         "agent_runtime",
		PrimaryFiles: []string{"agent.go"},
	}
	prompt := buildWorkerPrompt(snapshot, shard, buildRootCauseGoal("document file is not created"), "")
	for _, needle := range []string{
		"Root-cause worker checklist",
		"trigger_values",
		"out_of_range_cases",
		"observed_failure_path",
		"cannot_be_root_cause_if",
		"required_runtime_observation",
		"evidence_files",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("expected root-cause worker prompt to include %q", needle)
		}
	}
}

func TestParseReviewDecisionPayloadPreservesSymptomCausality(t *testing.T) {
	raw := `{
  "decision": {
    "status": "approved",
    "issues": [],
    "revision_prompt": "",
    "symptom_possible": "yes",
    "symptom_causality": ["ambiguous document wording disables write tools, so no file can be created"],
    "symptom_reproduction_bridge": ["document request is classified read-only and write tools are not exposed before final response"],
    "required_runtime_observation": ["tool registry lacks write_file while final response claims the artifact exists"],
    "disqualifying_evidence": ["write_file succeeds for the requested path before final response"],
    "causal_chain_complete": true,
    "causal_chain_stages": ["trigger", "invalid_state", "state_transition", "missing_guard", "user_visible_symptom"],
    "causal_chain_missing": [],
    "disconfirmed": false,
    "disconfirming_evidence": ["successful write_file result for the requested target would disprove this chain"],
    "rejected_candidates": []
  }
}`
	decision, ok := parseReviewDecisionPayload(raw)
	if !ok {
		t.Fatalf("expected review decision to parse")
	}
	if decision.SymptomPossible != "yes" {
		t.Fatalf("unexpected symptom_possible: %q", decision.SymptomPossible)
	}
	if len(decision.SymptomCausality) != 1 {
		t.Fatalf("expected symptom causality to be preserved, got %#v", decision.SymptomCausality)
	}
	if len(decision.SymptomReproductionBridge) != 1 || len(decision.RequiredRuntimeObservation) != 1 || len(decision.DisqualifyingEvidence) != 1 {
		t.Fatalf("expected reviewer validation contract fields to be preserved, got %#v", decision)
	}
	if len(decision.DisconfirmingEvidence) != 1 {
		t.Fatalf("expected disconfirming evidence to be preserved, got %#v", decision.DisconfirmingEvidence)
	}
	if !decision.CausalChainComplete || len(decision.CausalChainStages) != 5 {
		t.Fatalf("expected causal chain validation to be preserved, got %#v", decision)
	}
}

func TestEnforceRootCauseReviewContractRejectsMissingBridgeFields(t *testing.T) {
	decision := ReviewDecision{
		Status:              "approved",
		SymptomPossible:     "yes",
		SymptomCausality:    []string{"missing write tool can leave requested artifact uncreated"},
		CausalChainComplete: true,
		Raw:                 `{"decision":{"status":"approved"}}`,
	}
	decision = enforceRootCauseReviewContract(decision)
	if decision.Status != "needs_revision" {
		t.Fatalf("expected needs_revision for missing root-cause review contract, got %#v", decision)
	}
	if len(decision.Issues) == 0 || !strings.Contains(decision.Issues[0], "symptom_reproduction_bridge") {
		t.Fatalf("expected missing bridge issue, got %#v", decision.Issues)
	}
}

func TestRootCauseReviewWithoutSymptomValidationRemovesCandidates(t *testing.T) {
	reports := []WorkerReport{
		{
			Title: "Agent path",
			RootCauseCandidates: []RootCauseCandidate{
				{
					Title:         "Document request classified as read-only",
					Confidence:    "high",
					EvidenceFiles: []string{"agent.go"},
				},
			},
		},
	}
	reviews := []ReviewDecision{
		{
			Status:          "approved",
			SymptomPossible: "",
		},
	}
	filtered := filterRootCauseReportsByReview(ProjectSnapshot{AnalysisMode: "root-cause"}, reports, reviews)
	if len(filtered[0].RootCauseCandidates) != 0 {
		t.Fatalf("expected unvalidated root-cause candidates to be removed, got %#v", filtered[0].RootCauseCandidates)
	}
	if len(filtered[0].Unknowns) == 0 || !strings.Contains(filtered[0].Unknowns[0], "reviewer did not validate") {
		t.Fatalf("expected reviewer rejection note, got %#v", filtered[0].Unknowns)
	}
}

func TestRootCauseReviewWithoutCausalityRemovesCandidates(t *testing.T) {
	reports := []WorkerReport{
		{
			Title: "Agent path",
			RootCauseCandidates: []RootCauseCandidate{
				{
					Title:         "Document request classified as read-only",
					Confidence:    "high",
					EvidenceFiles: []string{"agent.go"},
				},
			},
		},
	}
	reviews := []ReviewDecision{
		{
			Status:          "approved",
			SymptomPossible: "yes",
		},
	}
	filtered := filterRootCauseReportsByReview(ProjectSnapshot{AnalysisMode: "root-cause"}, reports, reviews)
	if len(filtered[0].RootCauseCandidates) != 0 {
		t.Fatalf("expected root-cause candidates without symptom causality to be removed, got %#v", filtered[0].RootCauseCandidates)
	}
}

func TestRootCauseReviewMissingDecisionRemovesCandidates(t *testing.T) {
	reports := []WorkerReport{
		{
			Title: "Agent path",
			RootCauseCandidates: []RootCauseCandidate{
				{
					Title:         "Document request classified as read-only",
					Confidence:    "high",
					EvidenceFiles: []string{"agent.go"},
				},
			},
		},
	}
	filtered := filterRootCauseReportsByReview(ProjectSnapshot{AnalysisMode: "root-cause"}, reports, nil)
	if len(filtered[0].RootCauseCandidates) != 0 {
		t.Fatalf("expected unreviewed root-cause candidates to be removed, got %#v", filtered[0].RootCauseCandidates)
	}
	if len(filtered[0].Unknowns) == 0 || !strings.Contains(filtered[0].Unknowns[0], "no reviewer decision") {
		t.Fatalf("expected missing-review note, got %#v", filtered[0].Unknowns)
	}
}

func TestRootCauseReviewRejectedCandidateRemovesOnlyThatCandidate(t *testing.T) {
	reports := []WorkerReport{
		{
			Title: "Agent path",
			RootCauseCandidates: []RootCauseCandidate{
				{
					Title:         "Document request classified as read-only",
					Confidence:    "high",
					EvidenceFiles: []string{"agent.go"},
				},
				{
					Title:         "Final response omits artifact link",
					Confidence:    "medium",
					EvidenceFiles: []string{"ui.go"},
				},
			},
		},
	}
	reviews := []ReviewDecision{
		{
			Status:              "approved",
			SymptomPossible:     "yes",
			SymptomCausality:    []string{"read-only routing removes write tools, so requested document file cannot be created"},
			CausalChainComplete: true,
			CausalChainStages:   []string{"trigger", "invalid_state", "state_transition", "missing_guard", "user_visible_symptom"},
			RejectedCandidates:  []string{"Final response omits artifact link"},
		},
	}
	filtered := filterRootCauseReportsByReview(ProjectSnapshot{AnalysisMode: "root-cause"}, reports, reviews)
	if len(filtered[0].RootCauseCandidates) != 1 {
		t.Fatalf("expected one surviving root-cause candidate, got %#v", filtered[0].RootCauseCandidates)
	}
	if filtered[0].RootCauseCandidates[0].Title != "Document request classified as read-only" {
		t.Fatalf("unexpected surviving candidate: %#v", filtered[0].RootCauseCandidates[0])
	}
	if len(filtered[0].Unknowns) == 0 || !strings.Contains(filtered[0].Unknowns[0], "rejected by reviewer") {
		t.Fatalf("expected rejected-candidate note, got %#v", filtered[0].Unknowns)
	}
}

func TestRootCauseReviewRejectedCandidateDoesNotMatchGenericShortTitle(t *testing.T) {
	candidate := RootCauseCandidate{
		Title: "Agent",
	}
	review := ReviewDecision{
		RejectedCandidates: []string{"Agent final response omits artifact link"},
	}
	if rootCauseCandidateRejectedByReview(candidate, review) {
		t.Fatalf("expected short generic title not to be rejected by broad reviewer text")
	}
}

func TestParseRootCauseInvestigationPayload(t *testing.T) {
	raw := `{
  "root_cause": {
    "symptom": {
      "symptom": "document file is not created",
      "expected_behavior": "agent creates the requested document file",
      "observed_behavior": "agent may answer without creating the file",
      "frequency": "intermittent",
      "trigger_keywords": ["document", "문서"],
      "affected_surface": ["intent classification", "tool availability"],
      "must_explain": ["why no file is written"],
      "reproduction_inputs": ["문서화해줘"]
    },
    "hypotheses": [
      {
        "id": "H1",
        "title": "read-only intent disables edit tools",
        "candidate_mechanism": "document wording routes to analysis-only",
        "target_signals": ["intent", "write_file"],
        "target_files": ["analysis_context.go", "agent.go"],
        "must_prove": ["readOnlyAnalysis becomes true"],
        "must_disprove": ["write_file remains available"],
        "reproduction_inputs": ["설계를 문서화해줘"]
      }
    ]
  }
}`
	plan, ok := parseRootCauseInvestigationPayload(raw)
	if !ok {
		t.Fatalf("expected root-cause plan to parse")
	}
	if plan.Symptom.Symptom != "document file is not created" {
		t.Fatalf("unexpected symptom: %#v", plan.Symptom)
	}
	if len(plan.Hypotheses) != 1 || plan.Hypotheses[0].ID != "H1" {
		t.Fatalf("unexpected hypotheses: %#v", plan.Hypotheses)
	}
}

func TestFallbackRootCauseJoinClassifiesValidatedCandidate(t *testing.T) {
	shards := []AnalysisShard{{ID: "shard-01", Name: "agent_runtime", PrimaryFiles: []string{"agent.go"}}}
	reports := []WorkerReport{
		{
			Title: "Agent runtime",
			RootCauseCandidates: []RootCauseCandidate{
				{
					Title: "read-only intent disables write tools",
					CausalChain: RootCauseCausalChain{
						Trigger:            "document wording",
						InvalidState:       "readOnlyAnalysis=true for create request",
						StateTransition:    "write_file tool is removed",
						MissingGuard:       "final answer does not require created file",
						UserVisibleSymptom: "requested document file is not created",
					},
					CandidateChain:             []string{"document wording -> readOnlyAnalysis=true", "readOnlyAnalysis removes write_file"},
					ObservedFailurePath:        []string{"final answer can return without file creation"},
					EvidenceFiles:              []string{"agent.go"},
					CannotBeRootCauseIf:        []string{"write_file is available in failing turn"},
					RequiredRuntimeObservation: []string{"log disabledTools for failing prompt"},
					Confidence:                 "high",
				},
			},
		},
	}
	reviews := []ReviewDecision{{Status: "approved", SymptomPossible: "yes", SymptomCausality: []string{"no write tool means requested document file cannot be created"}, CausalChainComplete: true, CausalChainStages: []string{"trigger", "invalid_state", "state_transition", "missing_guard", "user_visible_symptom"}}}
	joined := fallbackRootCauseJoin(ProjectSnapshot{AnalysisMode: "root-cause"}, shards, reports, reviews)
	if len(joined) != 1 {
		t.Fatalf("expected one joined candidate, got %#v", joined)
	}
	if joined[0].Classification != "root_cause" {
		t.Fatalf("expected root_cause classification, got %#v", joined[0])
	}
	if joined[0].ConfidenceScore <= 0 {
		t.Fatalf("expected confidence score, got %#v", joined[0])
	}
}

func TestRootCauseReviewWithSymptomValidationKeepsCandidates(t *testing.T) {
	reports := []WorkerReport{
		{
			Title: "Agent path",
			RootCauseCandidates: []RootCauseCandidate{
				{
					Title:         "Document request classified as read-only",
					Confidence:    "high",
					EvidenceFiles: []string{"agent.go"},
				},
			},
		},
	}
	reviews := []ReviewDecision{
		{
			Status:              "approved",
			SymptomPossible:     "yes",
			SymptomCausality:    []string{"read-only routing removes write tools, so requested document file cannot be created"},
			CausalChainComplete: true,
			CausalChainStages:   []string{"trigger", "invalid_state", "state_transition", "missing_guard", "user_visible_symptom"},
			RejectedCandidates:  nil,
		},
	}
	filtered := filterRootCauseReportsByReview(ProjectSnapshot{AnalysisMode: "root-cause"}, reports, reviews)
	if len(filtered[0].RootCauseCandidates) != 1 {
		t.Fatalf("expected validated root-cause candidate to remain, got %#v", filtered[0].RootCauseCandidates)
	}
}

func TestParseReviewDecisionPayloadPreservesEvidenceRequests(t *testing.T) {
	raw := `{
  "decision": {
    "status": "approved",
    "symptom_possible": "partial",
    "symptom_causality": ["agent path can skip file creation but tool dispatch must be checked"],
    "causal_chain_stages": ["trigger", "invalid_state", "state_transition"],
    "evidence_requests": [
      {
        "request": "Inspect write tool dispatch",
        "target_signals": ["write_file", "tool dispatch"],
        "target_files": ["tools/write.go"],
        "reason": "Need to prove the missing write operation",
        "required_to_prove": "write_file is unavailable in failing turn"
      }
    ]
  }
}`
	decision, ok := parseReviewDecisionPayload(raw)
	if !ok {
		t.Fatalf("expected review decision to parse")
	}
	if len(decision.EvidenceRequests) != 1 {
		t.Fatalf("expected evidence request, got %#v", decision)
	}
	if decision.EvidenceRequests[0].TargetFiles[0] != "tools/write.go" {
		t.Fatalf("expected target file to be preserved, got %#v", decision.EvidenceRequests[0])
	}
}

func TestRootCauseEvidenceRequestPlansShardFromTargetFile(t *testing.T) {
	snapshot := rootCauseTestSnapshot([]ScannedFile{
		{Path: "agent.go", Directory: ".", LineCount: 20, ImportanceScore: 1},
		{Path: "tools/write.go", Directory: "tools", LineCount: 80, ImportanceScore: 10},
	})
	existing := []AnalysisShard{{ID: "shard-01", Name: "agent", PrimaryFiles: []string{"agent.go"}}}
	reviews := []ReviewDecision{{
		Status:          "approved",
		SymptomPossible: "partial",
		SymptomCausality: []string{
			"read-only routing may skip write_file",
		},
		CausalChainStages: []string{"trigger", "invalid_state", "state_transition"},
		EvidenceRequests: []RootCauseEvidenceRequest{{
			Request:       "Inspect write tool dispatch",
			TargetSignals: []string{"write_file"},
			TargetFiles:   []string{"tools/write.go"},
		}},
	}}
	analyzer := &projectAnalyzer{analysisCfg: ProjectAnalysisConfig{MaxFilesPerShard: 8, MaxLinesPerShard: 1200}}
	shards := analyzer.planRootCauseEvidenceRequestShards(snapshot, existing, reviews, 1, 4)
	if len(shards) != 1 {
		t.Fatalf("expected one evidence shard, got %#v", shards)
	}
	if len(shards[0].PrimaryFiles) != 1 || shards[0].PrimaryFiles[0] != "tools/write.go" {
		t.Fatalf("expected target file shard, got %#v", shards[0])
	}
}

func TestRootCauseEvidenceRequestSkipsClosedOrRoutedRequests(t *testing.T) {
	snapshot := rootCauseTestSnapshot([]ScannedFile{
		{Path: "agent.go", Directory: ".", LineCount: 20, ImportanceScore: 1},
		{Path: "tools/write.go", Directory: "tools", LineCount: 80, ImportanceScore: 10},
		{Path: "tools/create_file.go", Directory: "tools", LineCount: 70, ImportanceScore: 9},
	})
	snapshot.RootCause.EvidenceRequests = normalizeRootCauseEvidenceRequests([]RootCauseEvidenceRequest{
		{
			Request:     "Inspect write tool dispatch",
			TargetFiles: []string{"tools/write.go"},
			Status:      "fulfilled",
		},
		{
			Request:     "Inspect create_file dispatch",
			TargetFiles: []string{"tools/create_file.go"},
			Status:      "routed",
		},
	})
	existing := []AnalysisShard{{ID: "shard-01", Name: "agent", PrimaryFiles: []string{"agent.go"}}}
	analyzer := &projectAnalyzer{analysisCfg: ProjectAnalysisConfig{MaxFilesPerShard: 8, MaxLinesPerShard: 1200}}
	shards := analyzer.planRootCauseEvidenceRequestShards(snapshot, existing, nil, 2, 4)
	if len(shards) != 0 {
		t.Fatalf("expected no shard for fulfilled/routed requests, got %#v", shards)
	}
}

func TestRootCauseEvidenceRequestPlansShardFromSignal(t *testing.T) {
	snapshot := rootCauseTestSnapshot([]ScannedFile{
		{Path: "service/main.cpp", Directory: "service", LineCount: 100, ImportanceScore: 4},
		{Path: "service/control_handler.cpp", Directory: "service", LineCount: 120, ImportanceScore: 9, ImportanceReasons: []string{"service stop control handler"}},
	})
	reviews := []ReviewDecision{{
		Status:             "approved",
		SymptomPossible:    "partial",
		SymptomCausality:   []string{"stop request reaches service lifecycle but handler evidence is missing"},
		CausalChainStages:  []string{"trigger", "invalid_state", "state_transition"},
		EvidenceRequests:   []RootCauseEvidenceRequest{{Request: "Find stop control handler", TargetSignals: []string{"stop control handler"}}},
		CausalChainMissing: []string{"missing_guard", "user_visible_symptom"},
	}}
	analyzer := &projectAnalyzer{analysisCfg: ProjectAnalysisConfig{MaxFilesPerShard: 8, MaxLinesPerShard: 1200}}
	shards := analyzer.planRootCauseEvidenceRequestShards(snapshot, nil, reviews, 1, 4)
	if len(shards) == 0 {
		t.Fatalf("expected evidence shard from signal")
	}
	if shards[0].PrimaryFiles[0] != "service/control_handler.cpp" {
		t.Fatalf("expected control handler first, got %#v", shards[0].PrimaryFiles)
	}
}

func TestBuildRootCauseDeepVerificationPromptUsesFocusedSourceExcerpts(t *testing.T) {
	root := t.TempDir()
	source := strings.Join([]string{
		"package main",
		"func runAgent(readOnlyAnalysis bool) {",
		"    if readOnlyAnalysis {",
		"        disableTool(\"write_file\")",
		"    }",
		"}",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "agent.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("write temp source: %v", err)
	}
	snapshot := rootCauseTestSnapshot([]ScannedFile{{Path: "agent.go", Directory: ".", LineCount: 6, ImportanceScore: 5}})
	snapshot.Root = root
	shards := []AnalysisShard{{ID: "shard-01", Name: "agent", PrimaryFiles: []string{"agent.go"}}}
	targets := []rootCauseDeepVerificationTarget{{
		ShardID:   "shard-01",
		ShardName: "agent",
		Candidate: RootCauseCandidate{
			Title:         "readOnlyAnalysis disables write_file",
			EvidenceFiles: []string{"agent.go"},
			CausalChain: RootCauseCausalChain{
				Trigger:      "document request",
				InvalidState: "readOnlyAnalysis true",
			},
		},
		Review: ReviewDecision{Status: "approved", SymptomPossible: "yes", SymptomCausality: []string{"write_file removed"}},
	}}
	prompt := buildRootCauseDeepVerificationPrompt(snapshot, shards, targets, "document file is not created")
	if !strings.Contains(prompt, "Focused source excerpts") {
		t.Fatalf("expected focused source section, got %s", prompt)
	}
	if !strings.Contains(prompt, "readOnlyAnalysis") || !strings.Contains(prompt, "write_file") {
		t.Fatalf("expected focused source lines, got %s", prompt)
	}
}

func TestRootCauseConfidenceBreakdownAndProbesGenerated(t *testing.T) {
	shard := AnalysisShard{ID: "shard-01", Name: "agent", PrimaryFiles: []string{"agent.go"}}
	candidates := normalizeRootCauseCandidates([]RootCauseCandidate{{
		Title:                      "read-only intent disables write tools",
		EvidenceFiles:              []string{"agent.go"},
		RequiredRuntimeObservation: []string{"disabledTools contains write_file"},
		CausalChain: RootCauseCausalChain{
			Trigger:            "document prompt",
			InvalidState:       "readOnlyAnalysis=true",
			StateTransition:    "write_file unavailable",
			MissingGuard:       "no created-file assertion",
			UserVisibleSymptom: "document file missing",
		},
	}}, shard)
	if len(candidates) != 1 {
		t.Fatalf("expected candidate, got %#v", candidates)
	}
	if len(candidates[0].Probes) == 0 {
		t.Fatalf("expected generated probes, got %#v", candidates[0])
	}
	if candidates[0].ConfidenceBreakdown.Score <= 0 {
		t.Fatalf("expected confidence breakdown score, got %#v", candidates[0].ConfidenceBreakdown)
	}
}

func TestNormalizeRootCauseJoinedCandidatesClustersDuplicates(t *testing.T) {
	chain := RootCauseCausalChain{
		Trigger:            "document prompt",
		InvalidState:       "readOnlyAnalysis=true",
		StateTransition:    "write_file unavailable",
		MissingGuard:       "no file creation guard",
		UserVisibleSymptom: "document file missing",
	}
	joined := normalizeRootCauseJoinedCandidates([]RootCauseJoinedCandidate{
		{
			Title:           "read-only intent disables write tools",
			Classification:  "root_cause",
			CausalChain:     chain,
			EvidenceFiles:   []string{"agent.go"},
			ConfidenceScore: 82,
		},
		{
			Title:           "write tool unavailable in read-only path",
			Classification:  "root_cause",
			CausalChain:     chain,
			EvidenceFiles:   []string{"agent.go"},
			ConfidenceScore: 74,
		},
	})
	if len(joined) != 1 {
		t.Fatalf("expected duplicate joined candidates to cluster, got %#v", joined)
	}
	if joined[0].ClusterID == "" || len(joined[0].ClusterMembers) < 2 {
		t.Fatalf("expected cluster metadata, got %#v", joined[0])
	}
}

func TestRootCauseRegressionMemoryDowngradesPreviouslyRejectedCandidate(t *testing.T) {
	reports := []WorkerReport{{
		Title: "Agent path",
		RootCauseCandidates: []RootCauseCandidate{{
			Title:         "read-only intent disables write tools",
			Confidence:    "high",
			EvidenceFiles: []string{"agent.go"},
		}},
	}}
	memory := RootCauseAuditTrail{CandidateDecisions: []RootCauseCandidateAudit{{
		CandidateTitle: "read-only intent disables write tools",
		Decision:       "deep_disconfirmed",
		Reason:         "write_file remained available",
		EvidenceFiles:  []string{"agent.go"},
	}}}
	out := applyRootCauseRegressionMemoryToReports(reports, memory)
	if out[0].RootCauseCandidates[0].Confidence != "low" {
		t.Fatalf("expected regression memory to downgrade confidence, got %#v", out[0].RootCauseCandidates[0])
	}
	if len(out[0].RootCauseCandidates[0].DisconfirmingEvidence) == 0 {
		t.Fatalf("expected regression memory note, got %#v", out[0].RootCauseCandidates[0])
	}
}

func TestRenderRootCauseInvestigationIncludesRegressionMemory(t *testing.T) {
	plan := RootCauseInvestigation{
		Symptom: RootCauseSymptomProfile{Symptom: "document file is not created"},
		RegressionMemory: RootCauseAuditTrail{CandidateDecisions: []RootCauseCandidateAudit{{
			CandidateTitle: "read-only intent disables write tools",
			Decision:       "reviewer_rejected",
			Reason:         "missing user-visible symptom stage",
		}}},
	}
	text := renderRootCauseInvestigationForPrompt(plan, 2000)
	if !strings.Contains(text, "Regression memory") || !strings.Contains(text, "reviewer_rejected") {
		t.Fatalf("expected regression memory in prompt, got %s", text)
	}
}

func TestRootCauseDeterministicGateRejectsSymptomMismatch(t *testing.T) {
	shard := AnalysisShard{ID: "shard-01", Name: "agent", PrimaryFiles: []string{"agent.go"}}
	snapshot := rootCauseTestSnapshot([]ScannedFile{{Path: "agent.go", Directory: ".", LineCount: 20}})
	snapshot.RootCause.Symptom = RootCauseSymptomProfile{Symptom: "document file is not created"}
	reports := []WorkerReport{{
		Title: "Agent path",
		RootCauseCandidates: []RootCauseCandidate{{
			Title:         "readOnlyAnalysis disables write_file",
			EvidenceFiles: []string{"agent.go"},
			CausalChain: RootCauseCausalChain{
				Trigger:            "document prompt",
				InvalidState:       "readOnlyAnalysis=true",
				StateTransition:    "write_file removed",
				MissingGuard:       "no created-file guard",
				UserVisibleSymptom: "service process remains running",
			},
			Probes: []RootCauseProbe{{ExpectedSignal: "readOnlyAnalysis=true", DisprovesWhen: "write_file is available"}},
		}},
	}}
	reviews := []ReviewDecision{{Status: "approved", SymptomPossible: "yes", SymptomCausality: []string{"write_file removed"}, CausalChainComplete: true}}
	out := applyRootCauseDeterministicQualityGate(snapshot, []AnalysisShard{shard}, reports, reviews)
	if len(out[0].RootCauseCandidates) != 0 {
		t.Fatalf("expected mismatch to be rejected, got %#v", out[0].RootCauseCandidates)
	}
	if len(out[0].Unknowns) == 0 || !strings.Contains(out[0].Unknowns[0], "deterministic gate") {
		t.Fatalf("expected deterministic gate note, got %#v", out[0].Unknowns)
	}
}

func TestBuildRootCauseAuditTrailRecordsQualityGateRejection(t *testing.T) {
	shard := AnalysisShard{ID: "shard-01", Name: "agent", PrimaryFiles: []string{"agent.go"}}
	snapshot := rootCauseTestSnapshot([]ScannedFile{{Path: "agent.go", Directory: ".", LineCount: 20}})
	snapshot.RootCause.Symptom = RootCauseSymptomProfile{Symptom: "document file is not created"}
	reports := []WorkerReport{{
		Title: "Agent path",
		RootCauseCandidates: []RootCauseCandidate{{
			Title:         "readOnlyAnalysis disables write_file",
			EvidenceFiles: []string{"agent.go"},
			CausalChain: RootCauseCausalChain{
				Trigger:            "document prompt",
				InvalidState:       "readOnlyAnalysis=true",
				StateTransition:    "write_file removed",
				MissingGuard:       "no created-file guard",
				UserVisibleSymptom: "service process remains running",
			},
			Probes: []RootCauseProbe{{ExpectedSignal: "readOnlyAnalysis=true", DisprovesWhen: "write_file is available"}},
		}},
	}}
	reviews := []ReviewDecision{{Status: "approved", SymptomPossible: "yes", SymptomCausality: []string{"write_file removed"}, CausalChainComplete: true}}
	audit := buildRootCauseAuditTrail(snapshot, []AnalysisShard{shard}, reports, reviews, nil, nil, nil)
	if len(audit.CandidateDecisions) != 1 {
		t.Fatalf("expected audit decision, got %#v", audit)
	}
	decision := audit.CandidateDecisions[0]
	if decision.Decision != "quality_gate_rejected" {
		t.Fatalf("expected quality gate rejection, got %#v", decision)
	}
	if len(decision.QualityGateIssues) == 0 || !strings.Contains(strings.Join(decision.QualityGateIssues, " "), "user_visible_symptom") {
		t.Fatalf("expected symptom mismatch quality issue, got %#v", decision.QualityGateIssues)
	}
}

func TestRootCauseEvidenceRequestTracksRoutingAndFulfillment(t *testing.T) {
	requests := normalizeRootCauseEvidenceRequests([]RootCauseEvidenceRequest{{
		Request:       "Inspect write tool dispatch",
		TargetSignals: []string{"write_file"},
	}})
	if len(requests) != 1 || requests[0].ID == "" {
		t.Fatalf("expected evidence request id, got %#v", requests)
	}
	shards := []AnalysisShard{{ID: "shard-evidence-01-01", EvidenceRequestID: requests[0].ID, PrimaryFiles: []string{"tools/write.go"}}}
	requests = markRootCauseEvidenceRequestsRouted(requests, shards)
	if requests[0].Status != "routed" || len(requests[0].RoutedShardIDs) != 1 {
		t.Fatalf("expected routed request, got %#v", requests[0])
	}
	reports := []WorkerReport{{Facts: []string{"write_file dispatch inspected"}, EvidenceFiles: []string{"tools/write.go"}}}
	requests = markRootCauseEvidenceRequestsFulfilled(requests, shards, reports)
	if requests[0].Status != "fulfilled" || len(requests[0].FulfilledByShards) != 1 || len(requests[0].SatisfiedEvidenceFiles) == 0 {
		t.Fatalf("expected fulfilled request, got %#v", requests[0])
	}
}

func TestBuildRootCauseDeepVerificationPromptUsesSemanticFocusedLines(t *testing.T) {
	root := t.TempDir()
	source := strings.Join([]string{
		"package main",
		"func unrelated() {}",
		"func runAgent(readOnlyAnalysis bool) {",
		"    if readOnlyAnalysis {",
		"        disableTool(\"write_file\")",
		"    }",
		"}",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "agent.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("write temp source: %v", err)
	}
	snapshot := rootCauseTestSnapshot([]ScannedFile{{Path: "agent.go", Directory: ".", LineCount: 7, ImportanceScore: 5}})
	snapshot.Root = root
	shards := []AnalysisShard{{ID: "shard-01", Name: "agent", PrimaryFiles: []string{"agent.go"}}}
	targets := []rootCauseDeepVerificationTarget{{
		ShardID:   "shard-01",
		ShardName: "agent",
		Candidate: RootCauseCandidate{
			Title:         "runAgent readOnlyAnalysis disables write_file",
			EvidenceFiles: []string{"agent.go"},
			CausalChain:   RootCauseCausalChain{Trigger: "document prompt", InvalidState: "readOnlyAnalysis=true"},
		},
		Review: ReviewDecision{Status: "approved", SymptomPossible: "yes", SymptomCausality: []string{"write_file removed"}},
	}}
	index := SemanticIndexV2{Symbols: []SymbolRecord{{ID: "func:runAgent", Name: "runAgent", Kind: "function", File: "agent.go", StartLine: 3, EndLine: 7, Signature: "func runAgent(readOnlyAnalysis bool)"}}}
	prompt := buildRootCauseDeepVerificationPromptWithIndex(snapshot, shards, targets, "document file is not created", index)
	if !strings.Contains(prompt, "semantic_focus: symbol:runAgent") {
		t.Fatalf("expected semantic focus, got %s", prompt)
	}
}

func TestRootCauseWorkerLintRequiresConcreteStateSignal(t *testing.T) {
	shard := AnalysisShard{ID: "shard-01", Name: "agent", PrimaryFiles: []string{"agent.go"}}
	snapshot := rootCauseTestSnapshot([]ScannedFile{{Path: "agent.go", Directory: ".", LineCount: 20}})
	snapshot.RootCause.Symptom = RootCauseSymptomProfile{Symptom: "document file is not created"}
	report := WorkerReport{RootCauseCandidates: []RootCauseCandidate{{
		Title:               "ambiguous state skips writing",
		EvidenceFiles:       []string{"agent.go"},
		OutOfRangeCases:     []string{"bad state"},
		CausalChain:         RootCauseCausalChain{Trigger: "document prompt", InvalidState: "bad state", StateTransition: "skip write"},
		CannotBeRootCauseIf: []string{},
	}}}
	issues := lintWorkerReportForRootCause(snapshot, shard, report)
	if len(issues) == 0 {
		t.Fatalf("expected lint issues")
	}
	prompt := buildRootCauseWorkerLintRevisionPrompt(issues)
	if !strings.Contains(prompt, "exact variable") && !strings.Contains(prompt, "variable/field") {
		t.Fatalf("expected concrete variable revision prompt, got %s", prompt)
	}
}

func TestRootCauseConcreteStateSignalRejectsSentencePunctuation(t *testing.T) {
	if rootCauseTextHasConcreteStateSignal("bad state.") {
		t.Fatalf("expected plain punctuation sentence to be rejected")
	}
	if rootCauseTextHasConcreteStateSignal("invalid condition.") {
		t.Fatalf("expected substring id in invalid not to count as a concrete state signal")
	}
	if !rootCauseTextHasConcreteStateSignal("Party.MemberCount > MaxMembers") {
		t.Fatalf("expected member access and comparison to count as a concrete state signal")
	}
}

func TestRootCauseRegressionMemoryIsWeakerWhenCodeChanged(t *testing.T) {
	reports := []WorkerReport{{
		Title: "Agent path",
		RootCauseCandidates: []RootCauseCandidate{{
			Title:         "read-only intent disables write tools",
			Confidence:    "high",
			EvidenceFiles: []string{"agent.go"},
		}},
	}}
	audit := RootCauseCandidateAudit{ShardID: "shard-01", CandidateTitle: "read-only intent disables write tools", Decision: "deep_disconfirmed", Reason: "write_file remained available"}
	memory := RootCauseAuditTrail{CandidateDecisions: []RootCauseCandidateAudit{audit}}
	out := applyRootCauseRegressionMemoryToReportsWithChanges(reports, memory, map[string]bool{rootCauseCandidateAuditChangeKey(audit): true})
	if out[0].RootCauseCandidates[0].Confidence != "medium" {
		t.Fatalf("expected changed code to weaken memory to medium, got %#v", out[0].RootCauseCandidates[0])
	}
	if !strings.Contains(strings.Join(out[0].RootCauseCandidates[0].DisconfirmingEvidence, " "), "weaker prior") {
		t.Fatalf("expected weaker prior note, got %#v", out[0].RootCauseCandidates[0].DisconfirmingEvidence)
	}
}

func TestRootCauseRegressionMemoryMissingShardComparisonIsWeakerPrior(t *testing.T) {
	audit := RootCauseCandidateAudit{ShardID: "shard-missing", CandidateTitle: "read-only intent disables write tools", Decision: "deep_disconfirmed"}
	previousRun := &ProjectAnalysisRun{
		RootCause: RootCauseInvestigation{AuditTrail: RootCauseAuditTrail{CandidateDecisions: []RootCauseCandidateAudit{audit}}},
		Shards:    []AnalysisShard{{ID: "shard-other", PrimaryFiles: []string{"agent.go"}, PrimaryFingerprint: "old"}},
	}
	changes := buildRootCauseRegressionMemoryChangeState(previousRun, []AnalysisShard{{ID: "shard-01", PrimaryFiles: []string{"agent.go"}, PrimaryFingerprint: "old"}})
	if !changes[rootCauseCandidateAuditChangeKey(audit)] {
		t.Fatalf("expected missing previous shard comparison to be treated as a weaker prior, got %#v", changes)
	}
}

func TestRootCauseProbeCommandUsesGoTestForGoRepo(t *testing.T) {
	snapshot := rootCauseTestSnapshot([]ScannedFile{{Path: "agent.go", Directory: ".", Extension: ".go", LineCount: 20}})
	snapshot.ModulePath = "kernforge"
	probes := enhanceRootCauseProbesWithRepoCommands(snapshot, []RootCauseProbe{{
		Title:          "Trace write tool",
		Kind:           "test",
		ExpectedSignal: "write_file available",
		DisprovesWhen:  "write_file unavailable",
	}}, []string{"agent.go"})
	if len(probes) != 1 || !strings.Contains(probes[0].Command, "go test ./...") {
		t.Fatalf("expected go test probe command, got %#v", probes)
	}
}

func TestRootCauseJoinedCandidatesInferCompetition(t *testing.T) {
	base := RootCauseCausalChain{Trigger: "document prompt", MissingGuard: "no guard", UserVisibleSymptom: "document file missing"}
	joined := normalizeRootCauseJoinedCandidates([]RootCauseJoinedCandidate{
		{Title: "readOnlyAnalysis true", ClusterID: "rc-a", Classification: "root_cause", CausalChain: RootCauseCausalChain{Trigger: base.Trigger, InvalidState: "readOnlyAnalysis=true", StateTransition: "write_file removed", MissingGuard: base.MissingGuard, UserVisibleSymptom: base.UserVisibleSymptom}, EvidenceFiles: []string{"agent.go"}, ConfidenceScore: 80},
		{Title: "tool registry missing", ClusterID: "rc-b", Classification: "root_cause", CausalChain: RootCauseCausalChain{Trigger: base.Trigger, InvalidState: "toolRegistry missing write_file", StateTransition: "write_file removed", MissingGuard: base.MissingGuard, UserVisibleSymptom: base.UserVisibleSymptom}, EvidenceFiles: []string{"agent.go"}, ConfidenceScore: 70},
	})
	if len(joined) != 2 {
		t.Fatalf("expected two candidates, got %#v", joined)
	}
	if len(joined[0].CompetesWith) == 0 && len(joined[1].CompetesWith) == 0 {
		t.Fatalf("expected inferred competition, got %#v", joined)
	}
}

func TestRootCauseSourceAwareClarityUsesWorkspaceHints(t *testing.T) {
	command := rootCauseClarifiedPromptTemplateWithSourceHints("제한을 넘어서 초대돼", []string{"PartyManager"})
	if !strings.Contains(command, "PartyManager") {
		t.Fatalf("expected source hint in suggested command, got %s", command)
	}
	score := rootCauseSourceHintScore(rootCauseMeaningfulTokens("초대하고 추방하면 제한을 넘어서 초대돼"), "PartyManager")
	if score <= 0 {
		t.Fatalf("expected party source hint score, got %d", score)
	}
}

func rootCauseTestSnapshot(files []ScannedFile) ProjectSnapshot {
	snapshot := ProjectSnapshot{
		Root:             ".",
		AnalysisMode:     "root-cause",
		Files:            append([]ScannedFile(nil), files...),
		FilesByPath:      map[string]ScannedFile{},
		FilesByDirectory: map[string][]ScannedFile{},
	}
	for _, file := range files {
		snapshot.FilesByPath[file.Path] = file
		snapshot.FilesByDirectory[file.Directory] = append(snapshot.FilesByDirectory[file.Directory], file)
	}
	return snapshot
}
