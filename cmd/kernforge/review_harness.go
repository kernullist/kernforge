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

	reviewDefaultMaxContextChars        = 180000
	reviewFocusedMaxContextChars        = 180000
	reviewPreWriteMaxContextChars       = 180000
	reviewSourceAnalysisMaxContextChars = 180000

	reviewFocusedPromptEvidenceLimit       = 90000
	reviewPreWritePromptEvidenceLimit      = 120000
	reviewFocusedCrossEvidenceLimit        = 90000
	reviewPreWriteCrossEvidenceLimit       = 120000
	reviewCloudCrossSoftTimeout            = 5 * time.Minute
	reviewFocusedCrossSoftTimeout          = reviewCloudCrossSoftTimeout
	reviewPreWriteCrossSoftTimeout         = reviewCloudCrossSoftTimeout
	reviewCLICrossSoftTimeout              = 8 * time.Minute
	reviewLocalCrossSoftTimeout            = 8 * time.Minute
	reviewLowerPerformanceCrossSoftTimeout = reviewLocalCrossSoftTimeout
	reviewDeepSeekBroadCrossSoftTimeout    = reviewCloudCrossSoftTimeout
	reviewAdaptiveTimeoutCrossSoftTimeout  = 10 * time.Minute
	// reviewMainSoftTimeout bounds how long the harness waits for the MAIN
	// (non-cross) reviewer before degrading gracefully. Without it a hung main
	// reviewer stalls finalization forever. It is generous (longer than the cross
	// timeout) because the main reviewer is the primary verdict source, and it is
	// only a floor: it can be overridden per-config and is auto-extended after a
	// recent timeout via the adaptive timeout. A negative config value disables
	// it entirely.
	reviewMainSoftTimeout                 = 8 * time.Minute
	reviewFocusedPrimaryRawCrossLimit     = 6000
	reviewFocusedPrimaryFindingCrossLimit = 6000
	reviewPreWriteDiffEvidenceMaxChars    = 50000
	reviewPreWriteFileContextChars        = 60000
	reviewPreWriteLineContextBefore       = 20
	reviewPreWriteLineContextAfter        = 180

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

	// reviewFindingVerified* are the verdicts the independent blocker-verification
	// pass writes onto a model finding that would hard-block. "" means the pass
	// did not run for this finding (unrun, or verification was unavailable): the
	// finding keeps its normal gate behavior, which is the conservative fail-closed
	// default. "confirmed" means an independent (or honestly-labeled same-model)
	// pass corroborated the finding, so it still blocks. "refuted" means the pass
	// judged it a false positive, so it no longer blocks and is demoted. "unverified"
	// means the pass could not corroborate it, so it downgrades to a warning.
	reviewFindingVerifiedConfirmed  = "confirmed"
	reviewFindingVerifiedRefuted    = "refuted"
	reviewFindingVerifiedUnverified = "unverified"

	reviewSeverityBlocker = "blocker"
	reviewSeverityHigh    = "high"
	reviewSeverityMedium  = "medium"
	reviewSeverityLow     = "low"
	reviewSeverityInfo    = "info"

	reviewArtifactDirName = "reviews"

	reviewReviewerGatePolicyMainOnlyFallback = "main_only_fallback"

	// guestReviewerRole is the reviewer role used by the model-callable
	// review_second_opinion tool. It is deliberately distinct from
	// primary_reviewer/cross_reviewer/single-model-second-pass so a second
	// opinion requested mid-turn by the working model can never be confused with
	// the gate's own primary/cross review passes.
	guestReviewerRole = "guest_reviewer"

	// reviewSecondOpinionQuickEvidenceLimit and
	// reviewSecondOpinionThoroughEvidenceLimit bound the evidence the bounded
	// second-opinion tool sends to the reviewer model. They are intentionally far
	// smaller than the full review budgets so a mid-turn second opinion stays
	// cheap and fast.
	reviewSecondOpinionQuickEvidenceLimit    = 30000
	reviewSecondOpinionThoroughEvidenceLimit = 40000

	// crossReviewerFallbackThreshold is the number of consecutive cross-review
	// route failures that triggers a fallback to the single-model review path.
	// At 2+ consecutive failures we stop re-running the same failing cross route
	// every review (which otherwise leaves the gate stuck at RF-REVIEWER-001
	// forever) and let the single-model review reach a real verdict. A single
	// isolated cross failure still runs normally so a transient blip does not
	// disable the independent reviewer.
	crossReviewerFallbackThreshold = 2
)

type ReviewHarnessConfig struct {
	AutoAfterChange               *bool  `json:"auto_after_change,omitempty"`
	AutoAfterGoalIteration        *bool  `json:"auto_after_goal_iteration,omitempty"`
	AutoBeforeGitWrite            *bool  `json:"auto_before_git_write,omitempty"`
	ModelReviewConsent            string `json:"model_review_consent,omitempty"`
	AutoFollowUp                  string `json:"auto_follow_up,omitempty"`
	AutoRepairMaxRounds           int    `json:"auto_repair_max_rounds,omitempty"`
	RepeatedFindingBlockThreshold int    `json:"repeated_finding_block_threshold,omitempty"`
	// MainReviewerSoftTimeoutSecs bounds how long the harness waits for the main
	// (non-cross) reviewer before degrading gracefully instead of hanging
	// finalization forever. 0 uses the provider default; a negative value
	// disables the main-reviewer soft timeout entirely.
	MainReviewerSoftTimeoutSecs int `json:"main_reviewer_soft_timeout_seconds,omitempty"`
	// AutoCrossReviewer, when set with both provider and model, designates a
	// reviewer route the harness automatically uses for a genuinely independent
	// second pass when no explicit cross-review route is configured. Without it,
	// the enforced second pass reuses the main model and is honestly labeled as a
	// same-model pass rather than independent cross review.
	AutoCrossReviewer *ReviewModelConfig           `json:"auto_cross_reviewer,omitempty"`
	RoleModels        map[string]ReviewModelConfig `json:"role_models,omitempty"`
	// Blocking selects how review findings gate work: "" or "blocking" (default)
	// lets confirmed blockers stop the write/turn; "advisory" surfaces every
	// review finding as a warning without blocking, so the operator still gets the
	// signal but the review gate never hard-stops them.
	Blocking string `json:"blocking,omitempty"`
	// VerifyBlockers controls the independent blocker-verification pass: "" or
	// "auto" (default) runs one focused second-opinion pass over the model
	// findings that would hard-block and lets only a corroborated finding keep
	// blocking; "off" disables the pass and reverts to the trust-floor blocking
	// behavior. "auto" only spends a model call when there ARE model would-be
	// blockers (often zero), so a clean review costs nothing extra.
	VerifyBlockers string `json:"verify_blockers,omitempty"`
	// Deterministic controls review reproducibility: "" or "on" (default) reuses
	// a recent ACCEPTED (approved / approved_with_warnings) verdict for an
	// identical change fingerprint instead of re-running the model, so reviewing
	// unchanged clean code twice returns the same verdict; deterministic findings
	// (verification, evidence, etc.) still run fresh, and a blocking verdict is
	// never cached. "off" disables verdict reuse and reviews from scratch every
	// time.
	Deterministic string `json:"deterministic,omitempty"`
}

type ReviewRun struct {
	ID                      string                       `json:"id"`
	SchemaVersion           string                       `json:"schema_version"`
	KernforgeVersion        string                       `json:"kernforge_version,omitempty"`
	KernforgeBuild          KernforgeBuildIdentity       `json:"kernforge_build,omitempty"`
	PolicyPackVersions      map[string]string            `json:"policy_pack_versions,omitempty"`
	ReviewFingerprint       string                       `json:"review_fingerprint,omitempty"`
	Trigger                 string                       `json:"trigger,omitempty"`
	AdvisoryReview          bool                         `json:"advisory_review,omitempty"`
	Target                  string                       `json:"target,omitempty"`
	Mode                    string                       `json:"mode,omitempty"`
	Flow                    string                       `json:"flow,omitempty"`
	ModelReviewConsent      string                       `json:"model_review_consent,omitempty"`
	ConsentSource           string                       `json:"consent_source,omitempty"`
	SkipReason              string                       `json:"skip_reason,omitempty"`
	OriginalMainProposal    string                       `json:"original_main_proposal,omitempty"`
	OriginalMainProposalRef string                       `json:"original_main_proposal_ref,omitempty"`
	RequestClass            string                       `json:"request_class,omitempty"`
	RequestAnalysis         ReviewRequestAnalysis        `json:"request_analysis,omitempty"`
	AutoTriggered           bool                         `json:"auto_triggered,omitempty"`
	Status                  string                       `json:"status,omitempty"`
	MachineStatus           string                       `json:"machine_status,omitempty"`
	ExitCode                int                          `json:"exit_code,omitempty"`
	Objective               string                       `json:"objective,omitempty"`
	ImplementationReply     string                       `json:"implementation_reply,omitempty"`
	CreatedAt               time.Time                    `json:"created_at"`
	Workspace               string                       `json:"workspace,omitempty"`
	Branch                  string                       `json:"branch,omitempty"`
	Profiles                []string                     `json:"profiles,omitempty"`
	ModelPlan               ReviewModelPlan              `json:"model_plan,omitempty"`
	ChangeSet               ReviewChangeSet              `json:"change_set,omitempty"`
	Evidence                ReviewEvidencePack           `json:"evidence,omitempty"`
	Freshness               ReviewFreshness              `json:"freshness,omitempty"`
	Redaction               ReviewRedactionReport        `json:"redaction,omitempty"`
	EditProposals           []EditProposal               `json:"edit_proposals,omitempty"`
	RepairFindings          []ReviewFinding              `json:"repair_findings,omitempty"`
	ObligationLedger        ReviewObligationLedger       `json:"obligation_ledger,omitempty"`
	StateTransitions        []ReviewStateTransition      `json:"state_transitions,omitempty"`
	ActionEnvelopes         []ReviewActionEnvelope       `json:"action_envelopes,omitempty"`
	ApprovalLedger          ReviewApprovalLedger         `json:"approval_ledger,omitempty"`
	CapabilityManifest      ReviewCapabilityManifest     `json:"capability_manifest,omitempty"`
	SingleModelPolicy       SingleModelReviewPolicy      `json:"single_model_policy,omitempty"`
	SingleModelSecondPass   *SingleModelSecondPassReview `json:"single_model_second_pass,omitempty"`
	DocumentClaimsCheck     *ReviewDocumentClaimsCheck   `json:"document_claims_check,omitempty"`
	BlockerVerification     *ReviewBlockerVerification   `json:"blocker_verification,omitempty"`
	ExternalLookupIntents   []ReviewExternalLookupIntent `json:"external_lookup_intents,omitempty"`
	ArtifactIntegrity       ReviewArtifactIntegrity      `json:"artifact_integrity,omitempty"`
	LedgerConsistency       ReviewLedgerConsistencyCheck `json:"ledger_consistency,omitempty"`
	ResumeSanity            ReviewResumeSanityCheck      `json:"resume_sanity,omitempty"`
	PolicyPacks             []string                     `json:"policy_packs,omitempty"`
	ReviewerRuns            []ReviewReviewerRun          `json:"reviewer_runs,omitempty"`
	CrossReviewTriage       *CrossReviewTriageLedger     `json:"cross_review_triage,omitempty"`
	ReviewerGatePolicy      string                       `json:"reviewer_gate_policy,omitempty"`
	MergeResult             ReviewMergeResult            `json:"merge_result,omitempty"`
	Result                  ReviewResult                 `json:"result,omitempty"`
	Findings                []ReviewFinding              `json:"findings,omitempty"`
	Gate                    GateDecision                 `json:"gate,omitempty"`
	Waivers                 []ReviewWaiver               `json:"waivers,omitempty"`
	RepairPlan              ReviewRepairPlan             `json:"repair_plan,omitempty"`
	RuntimeGateLedger       RuntimeGateLedger            `json:"runtime_gate_ledger,omitempty"`
	Lifecycle               *ReviewRequestLifecycle      `json:"lifecycle,omitempty"`
	DecisionObservability   *ReviewDecisionObservability `json:"decision_observability,omitempty"`
	RouteHealthEvents       []ReviewRouteHealthEvent     `json:"route_health_events,omitempty"`
	LiveProviderDrill       *LiveProviderDrillReport     `json:"live_provider_drill,omitempty"`
	NextCommandResults      []ReviewNextCommandRun       `json:"next_command_results,omitempty"`
	ArtifactRefs            []string                     `json:"artifact_refs,omitempty"`
	AuditTrail              []string                     `json:"audit_trail,omitempty"`
}

type ReviewRequestAnalysis struct {
	OriginalRequest         string               `json:"original_request,omitempty"`
	InferredTarget          string               `json:"inferred_target,omitempty"`
	InferredMode            string               `json:"inferred_mode,omitempty"`
	SelectedFlow            string               `json:"selected_flow,omitempty"`
	RequestClass            string               `json:"request_class,omitempty"`
	LifecycleKind           string               `json:"lifecycle_kind,omitempty"`
	MixedFlow               bool                 `json:"mixed_flow,omitempty"`
	SecondaryRequestClasses []string             `json:"secondary_request_classes,omitempty"`
	RequestClassReason      string               `json:"request_class_reason,omitempty"`
	RequestClassConfidence  float64              `json:"request_class_confidence,omitempty"`
	RequestClassAmbiguous   bool                 `json:"request_class_ambiguous,omitempty"`
	RequestClassSignals     []string             `json:"request_class_signals,omitempty"`
	Confidence              float64              `json:"confidence,omitempty"`
	EvidenceNeeds           []string             `json:"evidence_needs,omitempty"`
	PolicyPacks             []string             `json:"policy_packs,omitempty"`
	CandidateFlows          []string             `json:"candidate_flows,omitempty"`
	DomainSignals           []ReviewDomainSignal `json:"domain_signals,omitempty"`
	RiskSignals             []ReviewRiskSignal   `json:"risk_signals,omitempty"`
	ScopeDiscovery          ReviewScopeDiscovery `json:"scope_discovery,omitempty"`
	Reason                  string               `json:"reason,omitempty"`
	AmbiguityWarnings       []string             `json:"ambiguity_warnings,omitempty"`
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
	Strategy           string                   `json:"strategy,omitempty"`
	RequiredRoles      []string                 `json:"required_roles,omitempty"`
	OptionalRoles      []string                 `json:"optional_roles,omitempty"`
	RequiredLenses     []string                 `json:"required_lenses,omitempty"`
	OptionalLenses     []string                 `json:"optional_lenses,omitempty"`
	AssignedModels     map[string]string        `json:"assigned_models,omitempty"`
	CapabilityProfiles []ReviewModelCapability  `json:"capability_profiles,omitempty"`
	RouteHealth        []ReviewRouteHealth      `json:"route_health,omitempty"`
	RouteHealthEvents  []ReviewRouteHealthEvent `json:"route_health_events,omitempty"`
	MissingRoles       []string                 `json:"missing_roles,omitempty"`
	DegradedRoles      []string                 `json:"degraded_roles,omitempty"`
	RouteLimits        []string                 `json:"route_limits,omitempty"`
	UserGuidance       []string                 `json:"user_guidance,omitempty"`
}

type ReviewReviewerRun struct {
	Role                       string    `json:"role,omitempty"`
	Kind                       string    `json:"kind,omitempty"`
	Model                      string    `json:"model,omitempty"`
	Provider                   string    `json:"provider,omitempty"`
	ProviderLabel              string    `json:"provider_label,omitempty"`
	ModelID                    string    `json:"model_id,omitempty"`
	StartedAt                  time.Time `json:"started_at,omitempty"`
	FinishedAt                 time.Time `json:"finished_at,omitempty"`
	LatencyMS                  int64     `json:"latency_ms,omitempty"`
	Status                     string    `json:"status,omitempty"`
	ModelQuality               string    `json:"model_quality,omitempty"`
	Error                      string    `json:"error,omitempty"`
	FailureClass               string    `json:"failure_class,omitempty"`
	FailureRecommendation      string    `json:"failure_recommendation,omitempty"`
	RetryCount                 int       `json:"retry_count,omitempty"`
	MalformedOutputCount       int       `json:"malformed_output_count,omitempty"`
	WeakOutputDegraded         bool      `json:"weak_output_degraded,omitempty"`
	DuplicatedOutput           bool      `json:"duplicated_output,omitempty"`
	EvidenceFreeOutput         bool      `json:"evidence_free_output,omitempty"`
	ToolCallMismatch           bool      `json:"tool_call_mismatch,omitempty"`
	ReviewerMainModelCollision bool      `json:"reviewer_main_model_collision,omitempty"`
	RawOutputPath              string    `json:"raw_output_path,omitempty"`
	RawProviderResponsePath    string    `json:"raw_provider_response_path,omitempty"`
	PromptPath                 string    `json:"prompt_path,omitempty"`
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
	Verified           string   `json:"verified,omitempty"`
	ResolutionStatus   string   `json:"resolution_status,omitempty"`
	RelatedPolicy      string   `json:"related_policy,omitempty"`
	EvidenceRefs       []string `json:"evidence_refs,omitempty"`
	FixRefs            []string `json:"fix_refs,omitempty"`
	VerificationRefs   []string `json:"verification_refs,omitempty"`
	RawExcerpt         string   `json:"raw_excerpt,omitempty"`
}

type ReviewObligationLedger struct {
	Items                 []ReviewObligation `json:"items,omitempty"`
	TotalCount            int                `json:"total_count,omitempty"`
	OpenCount             int                `json:"open_count,omitempty"`
	OpenRepairCount       int                `json:"open_repair_count,omitempty"`
	OpenVerificationCount int                `json:"open_verification_count,omitempty"`
	OpenEvidenceCount     int                `json:"open_evidence_count,omitempty"`
	OpenRouteCount        int                `json:"open_route_count,omitempty"`
	Summary               []string           `json:"summary,omitempty"`
}

type ReviewObligation struct {
	ID              string   `json:"id,omitempty"`
	Type            string   `json:"type,omitempty"`
	Status          string   `json:"status,omitempty"`
	SourceFindingID string   `json:"source_finding_id,omitempty"`
	Severity        string   `json:"severity,omitempty"`
	Category        string   `json:"category,omitempty"`
	ReviewerRole    string   `json:"reviewer_role,omitempty"`
	Title           string   `json:"title,omitempty"`
	RequiredAction  string   `json:"required_action,omitempty"`
	Blocking        bool     `json:"blocking"`
	EvidenceRefs    []string `json:"evidence_refs,omitempty"`
	FixRefs         []string `json:"fix_refs,omitempty"`
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
	File               string   `json:"file,omitempty"`
	Files              []string `json:"files,omitempty"`
	Operation          string   `json:"operation,omitempty"`
	AnchorBefore       string   `json:"anchor_before,omitempty"`
	ReplaceRange       string   `json:"replace_range,omitempty"`
	ExactSearch        string   `json:"exact_search,omitempty"`
	Replacement        string   `json:"replacement,omitempty"`
	AfterExcerpt       string   `json:"after_excerpt,omitempty"`
	Rationale          string   `json:"rationale,omitempty"`
	Risk               string   `json:"risk,omitempty"`
	ExpectedPreview    string   `json:"expected_preview,omitempty"`
	ExpectedComplete   *bool    `json:"expected_preview_complete,omitempty"`
	PreviewFingerprint string   `json:"preview_fingerprint,omitempty"`

	trustedPreviewFingerprint string
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
	Trigger              string
	Target               string
	Mode                 string
	Flow                 string
	Request              string
	Paths                []string
	ProvidedDiff         string
	ProvidedCode         string
	ImplementationReply  string
	IncludeGitDiff       bool
	IncludeFileContents  bool
	NoModel              bool
	AutoTriggered        bool
	AutoFollowUp         string
	EditProposals        []EditProposal
	RepairFindings       []ReviewFinding
	MaxContextChars      int
	ReviewerGatePolicy   string
	OriginalMainProposal string
	// BaseRef scopes git diff evidence to changes since this ref using a
	// merge-base three-dot diff (git diff <ref>...). Empty means worktree diff.
	BaseRef string
	// Commit scopes review evidence to a single commit (git show <sha>). It takes
	// precedence over BaseRef and the worktree diff.
	Commit  string
	RawArgs string
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
	out.ModelReviewConsent = normalizeModelReviewConsent(out.ModelReviewConsent)
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
	// Fold a configured auto cross-reviewer into the cross_reviewer role when no
	// explicit cross_reviewer route is set, so the normal route resolvers use it
	// for a genuinely independent second pass. Distinctness from main is still
	// enforced downstream, so an auto route that matches main stays unused. Clone
	// the role map before injecting so the synthetic entry is ephemeral and is
	// never written back into the caller's (and saved) config.
	if out.AutoCrossReviewer != nil &&
		strings.TrimSpace(out.AutoCrossReviewer.Provider) != "" &&
		strings.TrimSpace(out.AutoCrossReviewer.Model) != "" {
		if existing, ok := out.RoleModels["cross_reviewer"]; !ok || strings.TrimSpace(existing.Provider) == "" || strings.TrimSpace(existing.Model) == "" {
			cloned := make(map[string]ReviewModelConfig, len(out.RoleModels)+1)
			for k, v := range out.RoleModels {
				cloned[k] = v
			}
			cloned["cross_reviewer"] = *out.AutoCrossReviewer
			out.RoleModels = cloned
		}
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
	case "guest", "guest_reviewer", "second_opinion", "second_opinion_reviewer":
		return guestReviewerRole
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
	originalProposalRef := strings.TrimSpace(run.OriginalMainProposalRef)
	if originalProposalRef == "" {
		ref, err := writeReviewOriginalMainProposalArtifact(root, run)
		if err != nil {
			return err
		}
		originalProposalRef = ref
	}
	if originalProposalRef != "" {
		run.OriginalMainProposalRef = originalProposalRef
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
	if originalProposalRef != "" {
		run.ArtifactRefs = append(run.ArtifactRefs, originalProposalRef)
	}
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
	run.RouteHealthEvents = reviewRouteHealthEventsFromRun(&run)
	run.ModelPlan.RouteHealthEvents = append([]ReviewRouteHealthEvent(nil), run.RouteHealthEvents...)
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
			"request_class":  run.RequestClass,
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
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ReviewRun{}, err
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
	run.RequestClass = analysis.RequestClass
	run.RequestAnalysis = analysis
	run.ModelReviewConsent = configModelReviewConsent(rt.cfg)
	run.ImplementationReply = strings.TrimSpace(opts.ImplementationReply)
	run.EditProposals = normalizeEditProposals(opts.EditProposals)
	run.OriginalMainProposal = reviewOriginalMainProposalForOptions(opts)
	run.RepairFindings = normalizeReviewFindingCopies(opts.RepairFindings)
	run.ReviewerGatePolicy = normalizeReviewReviewerGatePolicy(opts.ReviewerGatePolicy)
	run.PolicyPacks = analysis.PolicyPacks
	run.PolicyPackVersions = reviewPolicyPackVersions(run.PolicyPacks)
	emitReviewPipelineProgress(rt, run, 1, "scope discovery", "범위 확인", "Find the files, symbols, and review width for this request.", "요청에 맞는 파일, 심볼, 리뷰 범위를 확정합니다.")
	emitReviewScopeDiscoveryProgress(rt, run)
	emitReviewPipelineProgress(rt, run, 2, "evidence pack", "증거 준비", "Collect git state, file excerpts, diffs, repair findings, and verification context.", "git 상태, 파일 발췌, diff, 수리 RF, 검증 맥락을 모읍니다.")
	changeSet, evidence := collectReviewEvidence(ctx, rt, root, run, opts)
	if err := ctx.Err(); err != nil {
		return run, err
	}
	run.ChangeSet = changeSet
	run.Evidence = evidence
	emitReviewEvidenceProgress(rt, run, opts)
	run.Redaction = redactReviewRunEvidence(&run)
	run.ModelPlan = planReviewModels(rt.cfg, run)
	run.SingleModelPolicy = buildSingleModelReviewPolicy(run, reviewRuntimeHasDistinctCrossReviewer(rt))
	run.Findings = append(run.Findings, deterministicReviewFindings(rt, run)...)
	if strings.TrimSpace(run.OriginalMainProposal) != "" {
		ref, err := writeReviewOriginalMainProposalArtifact(root, &run)
		if err != nil {
			return run, err
		}
		run.OriginalMainProposalRef = ref
	}
	emitReviewPipelineProgress(rt, run, 3, "model review", "모델 검토", "Run the main code review and the configured cross-review when available.", "메인 코드 검토와 설정된 교차 리뷰를 실행합니다.")
	reviewCacheKey := reviewVerdictCacheKey(run)
	reviewPrimaryModelLabel := reviewMainModelLabel(rt.cfg)
	modelReviewSkippedByConsent := false
	if opts.AutoTriggered && !opts.NoModel && len(run.Evidence.Sources) > 0 {
		decision := rt.confirmImplicitModelReview(ModelReviewConsentRequest{
			Trigger:                 reviewConsentTriggerForRun(run),
			OriginalMainProposal:    run.OriginalMainProposal,
			OriginalMainProposalRef: run.OriginalMainProposalRef,
		})
		applyModelReviewConsentToRun(&run, decision)
		if !decision.Allowed {
			modelReviewSkippedByConsent = true
			if decision.SkipReason == modelReviewSkipSingleModelRoute {
				emitReviewPipelineProgress(rt, run, 3, "model review skipped", "모델 검토 생략", "Implicit model review is skipped automatically on the single-model route (no independent reviewer configured); deterministic checks and the diff preview remain the gate.", "독립 reviewer가 없는 단일 모델 라우트라 암시적 모델 리뷰를 자동 생략했습니다. deterministic check와 diff preview가 게이트로 유지됩니다.")
			} else {
				emitReviewPipelineProgress(rt, run, 3, "model review skipped", "모델 검토 생략", "Implicit model review was skipped because consent was not granted.", "사용자 동의가 없어 암시적 모델 리뷰를 생략했습니다.")
			}
		}
	} else if opts.AutoTriggered {
		run.ConsentSource = firstNonBlankString(run.ConsentSource, "not_applicable")
	} else {
		run.ConsentSource = firstNonBlankString(run.ConsentSource, "explicit_review")
	}
	reuseCachedVerdict := false
	var cachedVerdictEntry ReviewVerdictCacheEntry
	if reviewDeterministicVerdictReuseEnabled(rt.cfg) && !opts.NoModel && !modelReviewSkippedByConsent && len(run.Evidence.Sources) > 0 {
		if entry, ok := lookupReviewVerdictCache(rt, reviewCacheKey, reviewPrimaryModelLabel); ok {
			reuseCachedVerdict = true
			cachedVerdictEntry = entry
		}
	}
	var capturedModelFindings []ReviewFinding
	if reuseCachedVerdict {
		// Deterministic reuse: an identical change fingerprint with a recent
		// accepted verdict reuses the model findings instead of re-running the
		// model, so the verdict is reproducible. Deterministic findings (evidence,
		// verification, etc.) still ran fresh above, so a new verification failure
		// on the same code is still caught.
		run.Findings = append(run.Findings, cloneReviewFindings(cachedVerdictEntry.ModelFindings)...)
		run.ReviewerRuns = append(run.ReviewerRuns, cachedReviewVerdictReviewerRun(cachedVerdictEntry))
		run.Result.ModelQuality = reviewModelQualityUsable
		emitReviewPipelineProgress(rt, run, 3, "model review reused", "모델 검토 재사용", "Reused a recent accepted review verdict for this identical change instead of re-running the model.", "동일한 변경에 대한 직전 합격 리뷰 결과를 재사용하고 모델을 다시 실행하지 않았습니다.")
	} else if !opts.NoModel && !modelReviewSkippedByConsent && len(run.Evidence.Sources) > 0 {
		modelFindings, reviewerRuns := executeReviewModelRuns(ctx, rt, root, &run)
		if err := ctx.Err(); err != nil {
			return run, err
		}
		capturedModelFindings = modelFindings
		run.ReviewerRuns = append(run.ReviewerRuns, reviewerRuns...)
		run.Findings = append(run.Findings, modelFindings...)
		run.Findings = append(run.Findings, requiredReviewerFailureFindings(run)...)
	} else if opts.NoModel {
		run.Result.Degraded = true
		run.Result.DegradedReason = "model review disabled by --no-model"
		run.Result.ModelQuality = reviewModelQualityUsable
	}
	run.RouteHealthEvents = reviewRouteHealthEventsFromRun(&run)
	run.ModelPlan.RouteHealthEvents = append([]ReviewRouteHealthEvent(nil), run.RouteHealthEvents...)
	emitReviewPipelineProgress(rt, run, 4, "merge/check", "병합/검산", "Normalize findings, separate route/meta noise, and preserve actionable code blockers.", "finding을 정규화하고 route/meta 노이즈와 실행 가능한 코드 blocker를 분리합니다.")
	normalizeNonBlockingReviewMetaFindings(&run)
	normalizeNonBlockingVerificationOnlyFindings(&run)
	run.Findings, run.MergeResult = mergeReviewFindings(run.Findings)
	run.Findings = append(run.Findings, preFixNonConclusiveBugHuntFindings(run)...)
	annotateSingleModelPreWriteRepairStatuses(&run)
	run.Findings = append(run.Findings, singleModelPreWritePolicyFindings(run)...)
	normalizeNonBlockingReviewMetaFindings(&run)
	run.Findings, run.MergeResult = mergeReviewFindings(run.Findings)
	normalizeAdvisoryMainOnlyFallbackModelFindings(&run)
	if !reuseCachedVerdict && !opts.NoModel && !modelReviewSkippedByConsent && len(run.Evidence.Sources) > 0 {
		runReviewBlockerVerificationPass(ctx, rt, root, &run)
		if err := ctx.Err(); err != nil {
			return run, err
		}
	}
	refreshReviewCrossReviewTriage(&run)
	run.ObligationLedger = buildReviewObligationLedger(run)
	emitReviewPipelineProgress(rt, run, 5, "gate decision", "게이트 판정", "Decide approved, approved_with_warnings, needs_revision, or insufficient_evidence.", "approved, approved_with_warnings, needs_revision, insufficient_evidence 중 하나로 판정합니다.")
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
	finalizeReviewRunProtocol(root, rt, &run)
	run.Lifecycle = buildReviewRequestLifecycle(&run, rt.session)
	run.RuntimeGateLedger = buildRuntimeGateLedgerWithReview(root, rt.session, runtimeGateActionReview, &run, "")
	run.DecisionObservability = buildReviewDecisionObservability(&run, &run.RuntimeGateLedger, nil)
	if run.SingleModelSecondPass != nil {
		recordAcceptedSecondPassCache(rt, run, *run.SingleModelSecondPass)
	}
	if !reuseCachedVerdict && reviewDeterministicVerdictReuseEnabled(rt.cfg) {
		recordReviewVerdictCache(rt, reviewCacheKey, run, capturedModelFindings, reviewPrimaryModelLabel)
	}
	emitDistinctReviewGateResultProgress(rt, run)
	emitReviewPipelineProgress(rt, run, 6, "next action", "다음 조치", reviewPipelineNextActionDetail(run, false), reviewPipelineNextActionDetail(run, true))
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
	buildIdentity := currentBuildIdentity()
	return ReviewRun{
		ID:               fmt.Sprintf("review-%s", now.Format("20060102-150405.000")),
		SchemaVersion:    reviewSchemaVersion,
		KernforgeVersion: buildIdentity.Version,
		KernforgeBuild:   buildIdentity,
		Trigger:          trigger,
		AdvisoryReview:   reviewBlockingIsAdvisory(rt.cfg),
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

// reviewBlockingIsAdvisory reports whether the operator configured review to be
// advisory (warn, never hard-block). Any value other than "advisory" keeps the
// default blocking behavior.
func reviewBlockingIsAdvisory(cfg Config) bool {
	return strings.EqualFold(strings.TrimSpace(cfg.Review.Blocking), "advisory")
}

// reviewBlockerVerificationDisabled reports whether the operator turned the
// independent blocker-verification pass off. Any value other than "off"
// (including the empty default) keeps the pass enabled ("auto"): it runs only
// when there are model findings that would hard-block, so it costs nothing on a
// clean review.
func reviewBlockerVerificationDisabled(cfg Config) bool {
	return strings.EqualFold(strings.TrimSpace(cfg.Review.VerifyBlockers), "off")
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
