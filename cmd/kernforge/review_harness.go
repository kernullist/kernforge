package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	reviewSchemaVersion = "review_run.v1"

	reviewDefaultMaxContextChars        = 60000
	reviewFocusedMaxContextChars        = 20000
	reviewPreWriteMaxContextChars       = 20000
	reviewSourceAnalysisMaxContextChars = 180000

	reviewFocusedPromptEvidenceLimit       = 12000
	reviewPreWritePromptEvidenceLimit      = 16000
	reviewFocusedCrossEvidenceLimit        = 12000
	reviewPreWriteCrossEvidenceLimit       = 12000
	reviewFocusedCrossSoftTimeout          = 3 * time.Minute
	reviewPreWriteCrossSoftTimeout         = 3 * time.Minute
	reviewLowerPerformanceCrossSoftTimeout = 5 * time.Minute
	reviewDeepSeekBroadCrossSoftTimeout    = 4 * time.Minute
	reviewFocusedPrimaryRawCrossLimit      = 6000
	reviewFocusedPrimaryFindingCrossLimit  = 6000
	reviewPreWriteDiffEvidenceMaxChars     = 6000
	reviewPreWriteFileContextChars         = 9000
	reviewPreWriteLineContextBefore        = 20
	reviewPreWriteLineContextAfter         = 180

	reviewTargetAuto           = "auto"
	reviewTargetPlan           = "plan"
	reviewTargetChange         = "change"
	reviewTargetSelection      = "selection"
	reviewTargetPR             = "pr"
	reviewTargetFinal          = "final_answer"
	reviewTargetGoal           = "goal_iteration"
	reviewTargetAnalysis       = "analysis_report"
	reviewTargetSourceAnalysis = "source_analysis"
	reviewTargetFinalAlias     = "final"
	reviewTargetGoalAlias      = "goal"
	reviewTargetAnalysisAlias  = "analysis"

	reviewModeCoreBuild           = "core_build"
	reviewModeLiveFix             = "live_fix"
	reviewModeResearch            = "research"
	reviewModeRefactor            = "refactor"
	reviewModeSecurityHardening   = "security_hardening"
	reviewModeUIPolish            = "ui_polish"
	reviewModePerformanceAnalysis = "performance_analysis"
	reviewModeGeneralChange       = "general_change"

	reviewVerdictApproved             = "approved"
	reviewVerdictApprovedWithWarnings = "approved_with_warnings"
	reviewVerdictNeedsRevision        = "needs_revision"
	reviewVerdictBlocked              = "blocked"
	reviewVerdictInsufficientEvidence = "insufficient_evidence"

	reviewMachineStatusOK                   = "ok"
	reviewMachineStatusWarning              = "warning"
	reviewMachineStatusNeedsRevision        = "needs_revision"
	reviewMachineStatusBlocked              = "blocked"
	reviewMachineStatusInsufficientEvidence = "insufficient_evidence"
	reviewMachineStatusFailed               = "review_failed"
	reviewMachineStatusUsageError           = "usage_error"

	reviewModelQualityStrong = "strong"
	reviewModelQualityUsable = "usable"
	reviewModelQualityWeak   = "weak"
	reviewModelQualityFailed = "failed"

	reviewFindingQualityComplete = "complete"
	reviewFindingQualityPartial  = "partial"
	reviewFindingQualityWeak     = "weak"
	reviewFindingQualityInvalid  = "invalid"

	reviewSeverityBlocker = "blocker"
	reviewSeverityHigh    = "high"
	reviewSeverityMedium  = "medium"
	reviewSeverityLow     = "low"
	reviewSeverityInfo    = "info"

	reviewArtifactDirName = "reviews"

	reviewReviewerGatePolicyMainOnlyFallback = "main_only_fallback"
)

type ReviewHarnessConfig struct {
	AutoAfterChange               *bool                        `json:"auto_after_change,omitempty"`
	AutoAfterGoalIteration        *bool                        `json:"auto_after_goal_iteration,omitempty"`
	AutoBeforeGitWrite            *bool                        `json:"auto_before_git_write,omitempty"`
	AutoFollowUp                  string                       `json:"auto_follow_up,omitempty"`
	AutoRepairMaxRounds           int                          `json:"auto_repair_max_rounds,omitempty"`
	RepeatedFindingBlockThreshold int                          `json:"repeated_finding_block_threshold,omitempty"`
	RoleModels                    map[string]ReviewModelConfig `json:"role_models,omitempty"`
}

type ReviewRun struct {
	ID                    string                       `json:"id"`
	SchemaVersion         string                       `json:"schema_version"`
	KernforgeVersion      string                       `json:"kernforge_version,omitempty"`
	PolicyPackVersions    map[string]string            `json:"policy_pack_versions,omitempty"`
	ReviewFingerprint     string                       `json:"review_fingerprint,omitempty"`
	Trigger               string                       `json:"trigger,omitempty"`
	Target                string                       `json:"target,omitempty"`
	Mode                  string                       `json:"mode,omitempty"`
	Flow                  string                       `json:"flow,omitempty"`
	RequestAnalysis       ReviewRequestAnalysis        `json:"request_analysis,omitempty"`
	AutoTriggered         bool                         `json:"auto_triggered,omitempty"`
	Status                string                       `json:"status,omitempty"`
	MachineStatus         string                       `json:"machine_status,omitempty"`
	ExitCode              int                          `json:"exit_code,omitempty"`
	Objective             string                       `json:"objective,omitempty"`
	CreatedAt             time.Time                    `json:"created_at"`
	Workspace             string                       `json:"workspace,omitempty"`
	Branch                string                       `json:"branch,omitempty"`
	Profiles              []string                     `json:"profiles,omitempty"`
	ModelPlan             ReviewModelPlan              `json:"model_plan,omitempty"`
	ChangeSet             ReviewChangeSet              `json:"change_set,omitempty"`
	Evidence              ReviewEvidencePack           `json:"evidence,omitempty"`
	Freshness             ReviewFreshness              `json:"freshness,omitempty"`
	Redaction             ReviewRedactionReport        `json:"redaction,omitempty"`
	EditProposals         []EditProposal               `json:"edit_proposals,omitempty"`
	RepairFindings        []ReviewFinding              `json:"repair_findings,omitempty"`
	StateTransitions      []ReviewStateTransition      `json:"state_transitions,omitempty"`
	ActionEnvelopes       []ReviewActionEnvelope       `json:"action_envelopes,omitempty"`
	ApprovalLedger        ReviewApprovalLedger         `json:"approval_ledger,omitempty"`
	CapabilityManifest    ReviewCapabilityManifest     `json:"capability_manifest,omitempty"`
	SingleModelPolicy     SingleModelReviewPolicy      `json:"single_model_policy,omitempty"`
	ExternalLookupIntents []ReviewExternalLookupIntent `json:"external_lookup_intents,omitempty"`
	ArtifactIntegrity     ReviewArtifactIntegrity      `json:"artifact_integrity,omitempty"`
	LedgerConsistency     ReviewLedgerConsistencyCheck `json:"ledger_consistency,omitempty"`
	ResumeSanity          ReviewResumeSanityCheck      `json:"resume_sanity,omitempty"`
	PolicyPacks           []string                     `json:"policy_packs,omitempty"`
	ReviewerRuns          []ReviewReviewerRun          `json:"reviewer_runs,omitempty"`
	ReviewerGatePolicy    string                       `json:"reviewer_gate_policy,omitempty"`
	MergeResult           ReviewMergeResult            `json:"merge_result,omitempty"`
	Result                ReviewResult                 `json:"result,omitempty"`
	Findings              []ReviewFinding              `json:"findings,omitempty"`
	Gate                  GateDecision                 `json:"gate,omitempty"`
	Waivers               []ReviewWaiver               `json:"waivers,omitempty"`
	RepairPlan            ReviewRepairPlan             `json:"repair_plan,omitempty"`
	RuntimeGateLedger     RuntimeGateLedger            `json:"runtime_gate_ledger,omitempty"`
	NextCommandResults    []ReviewNextCommandRun       `json:"next_command_results,omitempty"`
	ArtifactRefs          []string                     `json:"artifact_refs,omitempty"`
	AuditTrail            []string                     `json:"audit_trail,omitempty"`
}

type ReviewRequestAnalysis struct {
	OriginalRequest   string               `json:"original_request,omitempty"`
	InferredTarget    string               `json:"inferred_target,omitempty"`
	InferredMode      string               `json:"inferred_mode,omitempty"`
	SelectedFlow      string               `json:"selected_flow,omitempty"`
	Confidence        float64              `json:"confidence,omitempty"`
	EvidenceNeeds     []string             `json:"evidence_needs,omitempty"`
	PolicyPacks       []string             `json:"policy_packs,omitempty"`
	CandidateFlows    []string             `json:"candidate_flows,omitempty"`
	DomainSignals     []ReviewDomainSignal `json:"domain_signals,omitempty"`
	RiskSignals       []ReviewRiskSignal   `json:"risk_signals,omitempty"`
	ScopeDiscovery    ReviewScopeDiscovery `json:"scope_discovery,omitempty"`
	Reason            string               `json:"reason,omitempty"`
	AmbiguityWarnings []string             `json:"ambiguity_warnings,omitempty"`
}

type ReviewScopeDiscovery struct {
	CandidateFiles    []string `json:"candidate_files,omitempty"`
	CandidateSymbols  []string `json:"candidate_symbols,omitempty"`
	SearchTerms       []string `json:"search_terms,omitempty"`
	ScopeWidth        string   `json:"scope_width,omitempty"`
	Confidence        float64  `json:"confidence,omitempty"`
	NarrowingCommands []string `json:"narrowing_commands,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
}

type ReviewDomainSignal struct {
	Domain     string `json:"domain,omitempty"`
	Signal     string `json:"signal,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

type ReviewRiskSignal struct {
	Risk       string `json:"risk,omitempty"`
	Signal     string `json:"signal,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

type ReviewChangeSet struct {
	Source             string   `json:"source,omitempty"`
	BaseRef            string   `json:"base_ref,omitempty"`
	HeadRef            string   `json:"head_ref,omitempty"`
	CheckpointID       string   `json:"checkpoint_id,omitempty"`
	PatchTransactionID string   `json:"patch_transaction_id,omitempty"`
	ChangedPaths       []string `json:"changed_paths,omitempty"`
	AddedPaths         []string `json:"added_paths,omitempty"`
	ModifiedPaths      []string `json:"modified_paths,omitempty"`
	DeletedPaths       []string `json:"deleted_paths,omitempty"`
	RenamedPaths       []string `json:"renamed_paths,omitempty"`
	BinaryPaths        []string `json:"binary_paths,omitempty"`
	UntrackedPaths     []string `json:"untracked_paths,omitempty"`
	DiffStat           string   `json:"diff_stat,omitempty"`
	DiffExcerpt        string   `json:"diff_excerpt,omitempty"`
	Fingerprint        string   `json:"fingerprint,omitempty"`
}

type ReviewEvidencePack struct {
	Sources               []string `json:"sources,omitempty"`
	Text                  string   `json:"text,omitempty"`
	Warnings              []string `json:"warnings,omitempty"`
	ChangedPaths          []string `json:"changed_paths,omitempty"`
	VerificationSummary   string   `json:"verification_summary,omitempty"`
	VerificationFailed    bool     `json:"verification_failed,omitempty"`
	VerificationRequired  bool     `json:"verification_required,omitempty"`
	CodingHarnessSummary  string   `json:"coding_harness_summary,omitempty"`
	CompletionAuditStatus string   `json:"completion_audit_status,omitempty"`
}

type ReviewModelPlan struct {
	Strategy           string                  `json:"strategy,omitempty"`
	RequiredRoles      []string                `json:"required_roles,omitempty"`
	OptionalRoles      []string                `json:"optional_roles,omitempty"`
	AssignedModels     map[string]string       `json:"assigned_models,omitempty"`
	CapabilityProfiles []ReviewModelCapability `json:"capability_profiles,omitempty"`
	RouteHealth        []ReviewRouteHealth     `json:"route_health,omitempty"`
	MissingRoles       []string                `json:"missing_roles,omitempty"`
	DegradedRoles      []string                `json:"degraded_roles,omitempty"`
	RouteLimits        []string                `json:"route_limits,omitempty"`
	UserGuidance       []string                `json:"user_guidance,omitempty"`
}

type ReviewReviewerRun struct {
	Role          string    `json:"role,omitempty"`
	Kind          string    `json:"kind,omitempty"`
	Model         string    `json:"model,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	FinishedAt    time.Time `json:"finished_at,omitempty"`
	Status        string    `json:"status,omitempty"`
	ModelQuality  string    `json:"model_quality,omitempty"`
	Error         string    `json:"error,omitempty"`
	RawOutputPath string    `json:"raw_output_path,omitempty"`
	PromptPath    string    `json:"prompt_path,omitempty"`
}

type ReviewMergeResult struct {
	MergedFindings         []string `json:"merged_findings,omitempty"`
	SuppressedDuplicates   []string `json:"suppressed_duplicates,omitempty"`
	Conflicts              []string `json:"conflicts,omitempty"`
	SeverityChanges        []string `json:"severity_changes,omitempty"`
	DeterministicPreserved []string `json:"deterministic_preserved,omitempty"`
	FinalReviewerNotes     []string `json:"final_reviewer_notes,omitempty"`
}

type ReviewResult struct {
	Verdict          string   `json:"verdict,omitempty"`
	Summary          string   `json:"summary,omitempty"`
	ScopeReviewed    []string `json:"scope_reviewed,omitempty"`
	ScopeNotReviewed []string `json:"scope_not_reviewed,omitempty"`
	KeyRisks         []string `json:"key_risks,omitempty"`
	VerifiedEvidence []string `json:"verified_evidence,omitempty"`
	MissingEvidence  []string `json:"missing_evidence,omitempty"`
	FindingCount     int      `json:"finding_count,omitempty"`
	BlockingCount    int      `json:"blocking_count,omitempty"`
	WarningCount     int      `json:"warning_count,omitempty"`
	NoteCount        int      `json:"note_count,omitempty"`
	ModelQuality     string   `json:"model_quality,omitempty"`
	Degraded         bool     `json:"degraded,omitempty"`
	DegradedReason   string   `json:"degraded_reason,omitempty"`
}

type ReviewFinding struct {
	ID                 string   `json:"id,omitempty"`
	Source             string   `json:"source,omitempty"`
	ReviewerRole       string   `json:"reviewer_role,omitempty"`
	Severity           string   `json:"severity,omitempty"`
	Category           string   `json:"category,omitempty"`
	Confidence         string   `json:"confidence,omitempty"`
	Quality            string   `json:"quality,omitempty"`
	Path               string   `json:"path,omitempty"`
	Line               int      `json:"line,omitempty"`
	Symbol             string   `json:"symbol,omitempty"`
	Title              string   `json:"title,omitempty"`
	Evidence           string   `json:"evidence,omitempty"`
	Impact             string   `json:"impact,omitempty"`
	RequiredFix        string   `json:"required_fix,omitempty"`
	TestRecommendation string   `json:"test_recommendation,omitempty"`
	BlocksGate         bool     `json:"blocks_gate,omitempty"`
	ResolutionStatus   string   `json:"resolution_status,omitempty"`
	RelatedPolicy      string   `json:"related_policy,omitempty"`
	EvidenceRefs       []string `json:"evidence_refs,omitempty"`
	FixRefs            []string `json:"fix_refs,omitempty"`
	RawExcerpt         string   `json:"raw_excerpt,omitempty"`
}

type GateDecision struct {
	Verdict              string              `json:"verdict,omitempty"`
	Action               string              `json:"action,omitempty"`
	Reason               string              `json:"reason,omitempty"`
	BlockingFindings     []string            `json:"blocking_findings,omitempty"`
	WarningFindings      []string            `json:"warning_findings,omitempty"`
	RequiredActions      []string            `json:"required_actions,omitempty"`
	WaiverAllowed        bool                `json:"waiver_allowed,omitempty"`
	WaiverReasonRequired bool                `json:"waiver_reason_required,omitempty"`
	NextCommands         []ReviewNextCommand `json:"next_commands,omitempty"`
	QualityNotes         []string            `json:"quality_notes,omitempty"`
}

type ReviewNextCommand struct {
	ID                   string `json:"id,omitempty"`
	Command              string `json:"command,omitempty"`
	Reason               string `json:"reason,omitempty"`
	Safety               string `json:"safety,omitempty"`
	When                 string `json:"when,omitempty"`
	AutoRun              bool   `json:"auto_run"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
	ClientHint           string `json:"client_hint,omitempty"`
	ExpectedResult       string `json:"expected_result,omitempty"`
}

type ReviewNextCommandRun struct {
	Command string `json:"command,omitempty"`
	Status  string `json:"status,omitempty"`
	Summary string `json:"summary,omitempty"`
	Error   string `json:"error,omitempty"`
}

type ReviewFreshness struct {
	ReviewFingerprint string    `json:"review_fingerprint,omitempty"`
	Stale             bool      `json:"stale,omitempty"`
	StaleReason       string    `json:"stale_reason,omitempty"`
	SupersededBy      string    `json:"superseded_by,omitempty"`
	CheckedAt         time.Time `json:"checked_at,omitempty"`
	InvalidatedBy     []string  `json:"invalidated_by,omitempty"`
}

type ReviewRedactionReport struct {
	Status        string   `json:"status,omitempty"`
	Redacted      bool     `json:"redacted,omitempty"`
	Patterns      []string `json:"patterns,omitempty"`
	SensitiveRefs []string `json:"sensitive_refs,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

type EditProposal struct {
	File               string `json:"file,omitempty"`
	Operation          string `json:"operation,omitempty"`
	AnchorBefore       string `json:"anchor_before,omitempty"`
	ReplaceRange       string `json:"replace_range,omitempty"`
	ExactSearch        string `json:"exact_search,omitempty"`
	Replacement        string `json:"replacement,omitempty"`
	Rationale          string `json:"rationale,omitempty"`
	Risk               string `json:"risk,omitempty"`
	ExpectedPreview    string `json:"expected_preview,omitempty"`
	PreviewFingerprint string `json:"preview_fingerprint,omitempty"`
}

type ReviewWaiver struct {
	ID        string    `json:"id,omitempty"`
	FindingID string    `json:"finding_id,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Actor     string    `json:"actor,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Scope     string    `json:"scope,omitempty"`
	Allowed   bool      `json:"allowed,omitempty"`
	Status    string    `json:"status,omitempty"`
}

type ReviewRepairPlan struct {
	Required        bool     `json:"required,omitempty"`
	Prompt          string   `json:"prompt,omitempty"`
	Findings        []string `json:"findings,omitempty"`
	RequiredActions []string `json:"required_actions,omitempty"`
}

type ReviewHarnessOptions struct {
	Trigger             string
	Target              string
	Mode                string
	Flow                string
	Request             string
	Paths               []string
	ProvidedDiff        string
	ProvidedCode        string
	IncludeGitDiff      bool
	IncludeFileContents bool
	NoModel             bool
	AutoTriggered       bool
	AutoFollowUp        string
	EditProposals       []EditProposal
	RepairFindings      []ReviewFinding
	MaxContextChars     int
	ReviewerGatePolicy  string
	RawArgs             string
}

func configReviewHarness(cfg Config) ReviewHarnessConfig {
	out := cfg.Review
	if out.AutoAfterChange == nil {
		out.AutoAfterChange = boolPtr(true)
	}
	if out.AutoAfterGoalIteration == nil {
		out.AutoAfterGoalIteration = boolPtr(true)
	}
	if out.AutoBeforeGitWrite == nil {
		out.AutoBeforeGitWrite = boolPtr(true)
	}
	if strings.TrimSpace(out.AutoFollowUp) == "" {
		out.AutoFollowUp = "safe"
	}
	if out.AutoRepairMaxRounds <= 0 {
		out.AutoRepairMaxRounds = 2
	}
	if out.RepeatedFindingBlockThreshold <= 0 {
		out.RepeatedFindingBlockThreshold = 2
	}
	if out.RoleModels == nil {
		out.RoleModels = map[string]ReviewModelConfig{}
	}
	return out
}

func normalizeReviewRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(role, "-", "_")))
	switch role {
	case "primary", "primary_reviewer", "reviewer":
		return "primary_reviewer"
	case "cross", "cross_reviewer", "second_pass", "second_pass_reviewer":
		return "cross_reviewer"
	case "design", "architect", "architecture", "design_reviewer":
		return "design_reviewer"
	case "security", "security_reviewer":
		return "security_reviewer"
	case "false_positive", "false_positive_reviewer", "falsepositive", "fp":
		return "false_positive_reviewer"
	case "regression", "regression_reviewer":
		return "regression_reviewer"
	case "test", "test_reviewer":
		return "test_reviewer"
	case "final", "gate", "final_gate", "final_gate_reviewer":
		return "final_gate_reviewer"
	default:
		return role
	}
}

func normalizeReviewTarget(target string) string {
	target = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(target, "-", "_")))
	switch target {
	case "", "auto":
		return reviewTargetAuto
	case "change", "changes", "diff", "code":
		return reviewTargetChange
	case "source_analysis", "source", "code_analysis":
		return reviewTargetSourceAnalysis
	case "plan":
		return reviewTargetPlan
	case "selection", "selected":
		return reviewTargetSelection
	case "pr", "pull_request", "pullrequest":
		return reviewTargetPR
	case "final", "final_answer":
		return reviewTargetFinal
	case "goal", "goal_iteration":
		return reviewTargetGoal
	case "analysis", "analysis_report", "report":
		return reviewTargetAnalysis
	default:
		return target
	}
}

func normalizeReviewMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(mode, "-", "_")))
	switch mode {
	case "", "general", "general_change":
		return reviewModeGeneralChange
	case "core", "core_build", "architecture":
		return reviewModeCoreBuild
	case "live", "live_fix", "bugfix", "bug_fix":
		return reviewModeLiveFix
	case "research":
		return reviewModeResearch
	case "refactor":
		return reviewModeRefactor
	case "security", "security_hardening", "hardening":
		return reviewModeSecurityHardening
	case "ui", "ui_polish":
		return reviewModeUIPolish
	case "performance", "performance_analysis", "perf", "hitch", "hitching":
		return reviewModePerformanceAnalysis
	default:
		return mode
	}
}

func reviewStatusForVerdict(verdict string, strictWarnings bool) (string, int) {
	switch strings.TrimSpace(strings.ToLower(verdict)) {
	case reviewVerdictApproved:
		return reviewMachineStatusOK, 0
	case reviewVerdictApprovedWithWarnings:
		if strictWarnings {
			return reviewMachineStatusWarning, 1
		}
		return reviewMachineStatusWarning, 0
	case reviewVerdictNeedsRevision:
		return reviewMachineStatusNeedsRevision, 2
	case reviewVerdictBlocked:
		return reviewMachineStatusBlocked, 3
	case reviewVerdictInsufficientEvidence:
		return reviewMachineStatusInsufficientEvidence, 4
	default:
		return reviewMachineStatusFailed, 5
	}
}

func reviewArtifactRoot(root string) string {
	return filepath.Join(root, userConfigDirName, reviewArtifactDirName)
}

func reviewRunDir(root string, id string) string {
	return filepath.Join(reviewArtifactRoot(root), strings.TrimSpace(id))
}

func computeReviewFingerprint(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(strings.TrimSpace(part)))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func writeReviewRunArtifacts(root string, run *ReviewRun) error {
	if run == nil {
		return fmt.Errorf("nil review run")
	}
	dir := reviewRunDir(root, run.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	reviewJSON := filepath.Join(dir, "review.json")
	reviewMD := filepath.Join(dir, "review.md")
	evidenceMD := filepath.Join(dir, "evidence.md")
	latestJSON := filepath.Join(reviewArtifactRoot(root), "latest.json")
	latestMD := filepath.Join(reviewArtifactRoot(root), "latest.md")
	protocolRefs, err := writeReviewProtocolArtifacts(dir, *run)
	if err != nil {
		return err
	}
	run.ArtifactRefs = append([]string{reviewMD, reviewJSON, evidenceMD, latestMD, latestJSON}, protocolRefs...)
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWriteFile(reviewJSON, data, 0o644); err != nil {
		return err
	}
	if err := atomicWriteFile(reviewMD, []byte(renderReviewRunMarkdown(*run)), 0o644); err != nil {
		return err
	}
	if err := atomicWriteFile(evidenceMD, []byte(renderReviewEvidenceMarkdown(*run)), 0o644); err != nil {
		return err
	}
	if err := atomicWriteFile(latestJSON, data, 0o644); err != nil {
		return err
	}
	if err := atomicWriteFile(latestMD, []byte(renderReviewRunMarkdown(*run)), 0o644); err != nil {
		return err
	}
	return nil
}

func loadLatestReviewRun(root string) (ReviewRun, string, bool, error) {
	path := filepath.Join(reviewArtifactRoot(root), "latest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ReviewRun{}, path, false, nil
		}
		return ReviewRun{}, path, false, err
	}
	var run ReviewRun
	if err := json.Unmarshal(data, &run); err != nil {
		recovered, recoveredPath, recoveredOK, recoveredErr := recoverLatestReviewRun(root)
		if recoveredErr != nil {
			return ReviewRun{}, path, false, err
		}
		return recovered, recoveredPath, recoveredOK, nil
	}
	return run, path, true, nil
}

func (s *Session) recordReviewRun(run ReviewRun) {
	if s == nil {
		return
	}
	s.ReviewRouteHealth = mergeReviewRouteHealthHistory(s.ReviewRouteHealth, reviewRouteHealthFromRun(&run), 8)
	copyRun := run
	s.LastReviewRun = &copyRun
	s.AppendConversationEvent(ConversationEvent{
		Kind:         conversationEventKindReview,
		Severity:     reviewConversationSeverity(run.Gate.Verdict),
		Summary:      fmt.Sprintf("review %s: %s", valueOrDefault(run.Target, "target"), valueOrDefault(run.Gate.Verdict, run.Status)),
		ArtifactRefs: append([]string(nil), run.ArtifactRefs...),
		Entities: map[string]string{
			"review_id":      run.ID,
			"target":         run.Target,
			"mode":           run.Mode,
			"flow":           run.Flow,
			"verdict":        run.Gate.Verdict,
			"machine_status": run.MachineStatus,
			"blockers":       fmt.Sprintf("%d", len(run.Gate.BlockingFindings)),
			"warnings":       fmt.Sprintf("%d", len(run.Gate.WarningFindings)),
		},
	})
}

func reviewConversationSeverity(verdict string) string {
	switch strings.TrimSpace(strings.ToLower(verdict)) {
	case reviewVerdictApproved:
		return conversationSeverityInfo
	case reviewVerdictApprovedWithWarnings, reviewVerdictNeedsRevision, reviewVerdictBlocked, reviewVerdictInsufficientEvidence:
		return conversationSeverityWarn
	default:
		return conversationSeverityError
	}
}

func (r *ReviewRun) finalizeStatus(strictWarnings bool) {
	if r == nil {
		return
	}
	verdict := strings.TrimSpace(r.Gate.Verdict)
	if verdict == "" {
		verdict = strings.TrimSpace(r.Result.Verdict)
	}
	if verdict == "" {
		verdict = reviewVerdictInsufficientEvidence
	}
	r.Status = verdict
	r.Result.Verdict = verdict
	r.MachineStatus, r.ExitCode = reviewStatusForVerdict(verdict, strictWarnings)
	r.Result.FindingCount = len(r.Findings)
	r.Result.BlockingCount = 0
	r.Result.WarningCount = 0
	r.Result.NoteCount = 0
	for _, finding := range r.Findings {
		if reviewFindingBlocksGate(*r, finding) {
			r.Result.BlockingCount++
			continue
		}
		if reviewFindingCountsAsWarning(finding) {
			r.Result.WarningCount++
			continue
		}
		r.Result.NoteCount++
	}
}

func sortReviewFindings(findings []ReviewFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		left := reviewSeverityRank(findings[i].Severity)
		right := reviewSeverityRank(findings[j].Severity)
		if left != right {
			return left < right
		}
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Title < findings[j].Title
	})
}

func reviewSeverityRank(severity string) int {
	switch strings.TrimSpace(strings.ToLower(severity)) {
	case reviewSeverityBlocker:
		return 0
	case reviewSeverityHigh:
		return 1
	case reviewSeverityMedium:
		return 2
	case reviewSeverityLow:
		return 3
	default:
		return 4
	}
}

func runReviewHarness(ctx context.Context, rt *runtimeState, opts ReviewHarnessOptions) (ReviewRun, error) {
	if rt == nil || rt.session == nil {
		return ReviewRun{}, fmt.Errorf("no active runtime")
	}
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = rt.workspace.Root
	}
	if strings.TrimSpace(root) == "" {
		return ReviewRun{}, fmt.Errorf("workspace root is not configured")
	}
	maxContextWasDefaulted := opts.MaxContextChars <= 0
	if maxContextWasDefaulted {
		opts.MaxContextChars = reviewDefaultMaxContextChars
	}
	run := newReviewRunSkeleton(rt, root, opts)
	run.CapabilityManifest = buildReviewCapabilityManifest(rt, root)
	analysis := analyzeReviewRequest(rt, root, opts)
	opts.MaxContextChars = reviewMaxContextCharsForAnalysis(opts.MaxContextChars, analysis, maxContextWasDefaulted)
	opts.MaxContextChars = reviewMaxContextCharsForFastPath(opts.MaxContextChars, opts, analysis, maxContextWasDefaulted)
	run.Target = analysis.InferredTarget
	run.Mode = analysis.InferredMode
	run.Flow = analysis.SelectedFlow
	run.RequestAnalysis = analysis
	run.EditProposals = normalizeEditProposals(opts.EditProposals)
	run.RepairFindings = normalizeReviewFindingCopies(opts.RepairFindings)
	run.ReviewerGatePolicy = normalizeReviewReviewerGatePolicy(opts.ReviewerGatePolicy)
	run.PolicyPacks = analysis.PolicyPacks
	run.PolicyPackVersions = reviewPolicyPackVersions(run.PolicyPacks)
	emitReviewScopeDiscoveryProgress(rt, run)
	changeSet, evidence := collectReviewEvidence(ctx, rt, root, run, opts)
	run.ChangeSet = changeSet
	run.Evidence = evidence
	emitReviewEvidenceProgress(rt, run, opts)
	run.Redaction = redactReviewRunEvidence(&run)
	run.ModelPlan = planReviewModels(rt.cfg, run)
	run.SingleModelPolicy = buildSingleModelReviewPolicy(run, reviewRuntimeHasDistinctCrossReviewer(rt))
	run.Findings = append(run.Findings, deterministicReviewFindings(rt, run)...)
	if !opts.NoModel && len(run.Evidence.Sources) > 0 {
		modelFindings, reviewerRuns := executeReviewModelRuns(ctx, rt, root, &run)
		run.ReviewerRuns = append(run.ReviewerRuns, reviewerRuns...)
		run.Findings = append(run.Findings, modelFindings...)
		run.Findings = append(run.Findings, requiredReviewerFailureFindings(run)...)
	} else if opts.NoModel {
		run.Result.Degraded = true
		run.Result.DegradedReason = "model review disabled by --no-model"
		run.Result.ModelQuality = reviewModelQualityUsable
	}
	run.Findings, run.MergeResult = mergeReviewFindings(run.Findings)
	run.Findings = append(run.Findings, preFixNonConclusiveBugHuntFindings(run)...)
	annotateSingleModelPreWriteRepairStatuses(&run)
	run.Findings = append(run.Findings, singleModelPreWritePolicyFindings(run)...)
	run.Findings, run.MergeResult = mergeReviewFindings(run.Findings)
	run.Gate = evaluateReviewGate(run)
	run.RepairPlan = buildReviewRepairPlan(run)
	run.Result.ScopeReviewed = run.ChangeSet.ChangedPaths
	if len(run.Result.ScopeReviewed) == 0 {
		run.Result.ScopeReviewed = append([]string(nil), run.Evidence.Sources...)
	}
	run.Result.Summary = reviewResultSummaryForConfig(rt.cfg, run)
	run.Result.KeyRisks = reviewKeyRisks(run.Findings)
	run.Result.MissingEvidence = reviewMissingEvidence(run.Findings)
	run.Result.VerifiedEvidence = reviewVerifiedEvidence(run)
	if strings.TrimSpace(run.Result.ModelQuality) == "" {
		run.Result.ModelQuality = reviewModelQualityUsable
	}
	run.ReviewFingerprint = computeReviewFingerprint(run.Target, run.Mode, run.Flow, run.ChangeSet.Fingerprint, strings.Join(run.PolicyPacks, ","), run.Objective)
	run.Freshness = ReviewFreshness{
		ReviewFingerprint: run.ReviewFingerprint,
		CheckedAt:         time.Now(),
	}
	run.finalizeStatus(false)
	run.RuntimeGateLedger = buildRuntimeGateLedgerWithReview(root, rt.session, runtimeGateActionReview, &run, "")
	finalizeReviewRunProtocol(root, rt, &run)
	emitDistinctReviewGateResultProgress(rt, run)
	if err := writeReviewRunArtifacts(root, &run); err != nil {
		return run, err
	}
	rt.session.recordReviewRun(run)
	ledger := buildRuntimeGateLedgerWithReview(root, rt.session, runtimeGateActionReview, &run, "")
	rt.session.RuntimeGateLedger = &ledger
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return run, err
		}
	}
	return run, nil
}

func newReviewRunSkeleton(rt *runtimeState, root string, opts ReviewHarnessOptions) ReviewRun {
	now := time.Now()
	trigger := strings.TrimSpace(opts.Trigger)
	if trigger == "" {
		trigger = "explicit_command"
	}
	objective := strings.TrimSpace(opts.Request)
	if objective == "" && rt.session != nil && rt.session.AcceptanceContract != nil {
		objective = strings.TrimSpace(rt.session.AcceptanceContract.SourcePrompt)
	}
	if objective == "" && rt.session != nil && rt.session.TaskState != nil {
		objective = strings.TrimSpace(rt.session.TaskState.Goal)
	}
	return ReviewRun{
		ID:               fmt.Sprintf("review-%s", now.Format("20060102-150405.000")),
		SchemaVersion:    reviewSchemaVersion,
		KernforgeVersion: currentVersion(),
		Trigger:          trigger,
		AutoTriggered:    opts.AutoTriggered,
		Objective:        objective,
		CreatedAt:        now,
		Workspace:        filepath.ToSlash(root),
		Branch:           delegationGitBranch(root),
		Profiles:         []string{formatProviderModelEffortLabel(rt.cfg.Provider, rt.cfg.Model, rt.cfg.ReasoningEffort)},
		Result: ReviewResult{
			ModelQuality: reviewModelQualityUsable,
		},
	}
}

func reviewPolicyPackVersions(packs []string) map[string]string {
	out := map[string]string{}
	for _, pack := range packs {
		pack = strings.TrimSpace(pack)
		if pack != "" {
			out[pack] = "v1"
		}
	}
	return out
}
