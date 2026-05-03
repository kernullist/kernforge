package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type ProjectAnalysisConfig struct {
	Enabled                    *bool    `json:"enabled,omitempty"`
	MinAgents                  int      `json:"min_agents,omitempty"`
	MaxAgents                  int      `json:"max_agents,omitempty"`
	MaxTotalShards             int      `json:"max_total_shards,omitempty"`
	MaxRefinementShards        int      `json:"max_refinement_shards,omitempty"`
	MaxRevisionRounds          int      `json:"max_revision_rounds,omitempty"`
	MaxProviderRetries         int      `json:"max_provider_retries,omitempty"`
	ProviderRetryDelayMs       int      `json:"provider_retry_delay_ms,omitempty"`
	MaxFilesPerShard           int      `json:"max_files_per_shard,omitempty"`
	MaxLinesPerShard           int      `json:"max_lines_per_shard,omitempty"`
	ExcludeDirs                []string `json:"exclude_dirs,omitempty"`
	ExcludePaths               []string `json:"exclude_paths,omitempty"`
	OutputDir                  string   `json:"output_dir,omitempty"`
	MaxFileBytes               int64    `json:"max_file_bytes,omitempty"`
	WorkerProfile              *Profile `json:"worker_profile,omitempty"`
	ReviewerProfile            *Profile `json:"reviewer_profile,omitempty"`
	Incremental                *bool    `json:"incremental,omitempty"`
	minAgentsConfigured        bool
	maxAgentsConfigured        bool
	maxTotalShardsConfigured   bool
	maxFilesPerShardConfigured bool
	maxLinesPerShardConfigured bool
}

type ProjectAnalysisSummary struct {
	RunID                  string    `json:"run_id"`
	Goal                   string    `json:"goal"`
	Mode                   string    `json:"mode,omitempty"`
	Status                 string    `json:"status"`
	AgentCount             int       `json:"agent_count"`
	OutputPath             string    `json:"output_path"`
	StartedAt              time.Time `json:"started_at"`
	CompletedAt            time.Time `json:"completed_at"`
	ApprovedShards         int       `json:"approved_shards"`
	ReviewFailures         int       `json:"review_failures,omitempty"`
	ReviewProviderFailures int       `json:"review_provider_failures,omitempty"`
	ReviewQualityIssues    int       `json:"review_quality_issues,omitempty"`
	RefinedShards          int       `json:"refined_shards,omitempty"`
	EvidenceShards         int       `json:"evidence_shards,omitempty"`
	TotalShards            int       `json:"total_shards"`
}

type ScannedFile struct {
	Path              string   `json:"path"`
	Directory         string   `json:"directory"`
	Extension         string   `json:"extension"`
	LineCount         int      `json:"line_count"`
	IsManifest        bool     `json:"is_manifest"`
	IsEntrypoint      bool     `json:"is_entrypoint"`
	RawImports        []string `json:"raw_imports,omitempty"`
	Imports           []string `json:"imports,omitempty"`
	ImportanceScore   int      `json:"importance_score,omitempty"`
	ImportanceReasons []string `json:"importance_reasons,omitempty"`
}

type ProjectSnapshot struct {
	Root                string                     `json:"root"`
	ModulePath          string                     `json:"module_path,omitempty"`
	AnalysisMode        string                     `json:"analysis_mode,omitempty"`
	BaselineMap         AnalysisBaselineMap        `json:"baseline_map,omitempty"`
	GeneratedAt         time.Time                  `json:"generated_at"`
	Files               []ScannedFile              `json:"files"`
	Directories         []string                   `json:"directories"`
	ManifestFiles       []string                   `json:"manifest_files"`
	EntrypointFiles     []string                   `json:"entrypoint_files"`
	SolutionProjects    []SolutionProject          `json:"solution_projects,omitempty"`
	StartupProjects     []string                   `json:"startup_projects,omitempty"`
	PrimaryStartup      string                     `json:"primary_startup,omitempty"`
	UnrealProjects      []UnrealProject            `json:"unreal_projects,omitempty"`
	UnrealPlugins       []UnrealPlugin             `json:"unreal_plugins,omitempty"`
	UnrealTargets       []UnrealTarget             `json:"unreal_targets,omitempty"`
	UnrealModules       []UnrealModule             `json:"unreal_modules,omitempty"`
	UnrealTypes         []UnrealReflectedType      `json:"unreal_types,omitempty"`
	UnrealNetwork       []UnrealNetworkSurface     `json:"unreal_network,omitempty"`
	UnrealAssets        []UnrealAssetReference     `json:"unreal_assets,omitempty"`
	UnrealSystems       []UnrealGameplaySystem     `json:"unreal_systems,omitempty"`
	UnrealSettings      []UnrealProjectSetting     `json:"unreal_settings,omitempty"`
	CompileCommands     []CompilationCommandRecord `json:"compile_commands,omitempty"`
	BuildContexts       []BuildContextRecord       `json:"build_contexts,omitempty"`
	PrimaryUnrealModule string                     `json:"primary_unreal_module,omitempty"`
	AnalysisLenses      []AnalysisLens             `json:"analysis_lenses,omitempty"`
	ProjectTypes        []string                   `json:"project_types,omitempty"`
	RootCause           RootCauseInvestigation     `json:"root_cause,omitempty"`
	RuntimeEdges        []RuntimeEdge              `json:"runtime_edges,omitempty"`
	ProjectEdges        []ProjectEdge              `json:"project_edges,omitempty"`
	ArchitectureFacts   ArchitectureFactPack       `json:"architecture_facts,omitempty"`
	TotalFiles          int                        `json:"total_files"`
	TotalLines          int                        `json:"total_lines"`
	ImportGraph         map[string][]string        `json:"import_graph"`
	ReverseImportGraph  map[string][]string        `json:"reverse_import_graph"`
	FilesByPath         map[string]ScannedFile     `json:"-"`
	FilesByDirectory    map[string][]ScannedFile   `json:"-"`
}

type AnalysisBaselineMap struct {
	RunID            string    `json:"run_id,omitempty"`
	Goal             string    `json:"goal,omitempty"`
	Mode             string    `json:"mode,omitempty"`
	ArtifactPath     string    `json:"artifact_path,omitempty"`
	DocsManifestPath string    `json:"docs_manifest_path,omitempty"`
	GeneratedAt      time.Time `json:"generated_at,omitempty"`
	ProjectSummary   string    `json:"project_summary,omitempty"`
	PrimaryStartup   string    `json:"primary_startup,omitempty"`
	Subsystems       []string  `json:"subsystems,omitempty"`
	TopFiles         []string  `json:"top_files,omitempty"`
	SourceAnchors    []string  `json:"source_anchors,omitempty"`
}

type SolutionProject struct {
	Name              string   `json:"name"`
	Path              string   `json:"path"`
	Directory         string   `json:"directory,omitempty"`
	Kind              string   `json:"kind,omitempty"`
	OutputType        string   `json:"output_type,omitempty"`
	EntryFiles        []string `json:"entry_files,omitempty"`
	ProjectReferences []string `json:"project_references,omitempty"`
	StartupCandidate  bool     `json:"startup_candidate,omitempty"`
}

type UnrealProject struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Modules []string `json:"modules,omitempty"`
	Plugins []string `json:"plugins,omitempty"`
}

type UnrealPlugin struct {
	Name             string   `json:"name"`
	Path             string   `json:"path"`
	Modules          []string `json:"modules,omitempty"`
	EnabledByDefault bool     `json:"enabled_by_default,omitempty"`
}

type UnrealTarget struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	TargetType string   `json:"target_type,omitempty"`
	Modules    []string `json:"modules,omitempty"`
}

type UnrealModule struct {
	Name                string   `json:"name"`
	Path                string   `json:"path"`
	Kind                string   `json:"kind,omitempty"`
	Plugin              string   `json:"plugin,omitempty"`
	PublicDependencies  []string `json:"public_dependencies,omitempty"`
	PrivateDependencies []string `json:"private_dependencies,omitempty"`
	DynamicallyLoaded   []string `json:"dynamically_loaded,omitempty"`
}

type UnrealReflectedType struct {
	Name                       string   `json:"name"`
	Kind                       string   `json:"kind"`
	BaseClass                  string   `json:"base_class,omitempty"`
	Module                     string   `json:"module,omitempty"`
	File                       string   `json:"file,omitempty"`
	Properties                 []string `json:"properties,omitempty"`
	Functions                  []string `json:"functions,omitempty"`
	Specifiers                 []string `json:"specifiers,omitempty"`
	GameplayRole               string   `json:"gameplay_role,omitempty"`
	BlueprintCallableFunctions []string `json:"blueprint_callable_functions,omitempty"`
	BlueprintEventFunctions    []string `json:"blueprint_event_functions,omitempty"`
	GameInstanceClass          string   `json:"game_instance_class,omitempty"`
	GameModeClass              string   `json:"game_mode_class,omitempty"`
	GameStateClass             string   `json:"game_state_class,omitempty"`
	PlayerControllerClass      string   `json:"player_controller_class,omitempty"`
	PlayerStateClass           string   `json:"player_state_class,omitempty"`
	DefaultPawnClass           string   `json:"default_pawn_class,omitempty"`
	HUDClass                   string   `json:"hud_class,omitempty"`
}

type UnrealNetworkSurface struct {
	TypeName             string   `json:"type_name,omitempty"`
	Module               string   `json:"module,omitempty"`
	File                 string   `json:"file,omitempty"`
	ServerRPCs           []string `json:"server_rpcs,omitempty"`
	ClientRPCs           []string `json:"client_rpcs,omitempty"`
	MulticastRPCs        []string `json:"multicast_rpcs,omitempty"`
	ReplicatedProperties []string `json:"replicated_properties,omitempty"`
	RepNotifyProperties  []string `json:"rep_notify_properties,omitempty"`
	HasReplicationList   bool     `json:"has_replication_list,omitempty"`
}

type UnrealAssetReference struct {
	OwnerName        string   `json:"owner_name,omitempty"`
	Module           string   `json:"module,omitempty"`
	File             string   `json:"file,omitempty"`
	AssetPaths       []string `json:"asset_paths,omitempty"`
	CanonicalTargets []string `json:"canonical_targets,omitempty"`
	ConfigKeys       []string `json:"config_keys,omitempty"`
	LoadMethods      []string `json:"load_methods,omitempty"`
}

type UnrealProjectSetting struct {
	SourceFile            string `json:"source_file,omitempty"`
	GameDefaultMap        string `json:"game_default_map,omitempty"`
	EditorStartupMap      string `json:"editor_startup_map,omitempty"`
	GlobalDefaultGameMode string `json:"global_default_game_mode,omitempty"`
	GameInstanceClass     string `json:"game_instance_class,omitempty"`
	DefaultPawnClass      string `json:"default_pawn_class,omitempty"`
	PlayerControllerClass string `json:"player_controller_class,omitempty"`
	HUDClass              string `json:"hud_class,omitempty"`
}

type UnrealGameplaySystem struct {
	System     string   `json:"system,omitempty"`
	OwnerName  string   `json:"owner_name,omitempty"`
	Module     string   `json:"module,omitempty"`
	File       string   `json:"file,omitempty"`
	Signals    []string `json:"signals,omitempty"`
	Assets     []string `json:"assets,omitempty"`
	Functions  []string `json:"functions,omitempty"`
	OwnedBy    []string `json:"owned_by,omitempty"`
	Targets    []string `json:"targets,omitempty"`
	Contexts   []string `json:"contexts,omitempty"`
	Actions    []string `json:"actions,omitempty"`
	Widgets    []string `json:"widgets,omitempty"`
	Abilities  []string `json:"abilities,omitempty"`
	Effects    []string `json:"effects,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
}

type RuntimeEdge struct {
	Source     string   `json:"source"`
	Target     string   `json:"target"`
	Kind       string   `json:"kind,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
}

type AnalysisLens struct {
	Type              string   `json:"type"`
	PrioritySignals   []string `json:"priority_signals,omitempty"`
	SuppressedSignals []string `json:"suppressed_signals,omitempty"`
	OutputFocus       []string `json:"output_focus,omitempty"`
}

type ProjectEdge struct {
	Source     string            `json:"source"`
	Target     string            `json:"target"`
	Type       string            `json:"type"`
	Confidence string            `json:"confidence,omitempty"`
	Evidence   []string          `json:"evidence,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type AnalysisShard struct {
	ID                           string               `json:"id"`
	Name                         string               `json:"name"`
	ParentShardID                string               `json:"parent_shard_id,omitempty"`
	EvidenceRequestID            string               `json:"evidence_request_id,omitempty"`
	RefinementStage              int                  `json:"refinement_stage,omitempty"`
	PrimaryFiles                 []string             `json:"primary_files"`
	ReferenceFiles               []string             `json:"reference_files,omitempty"`
	EstimatedFiles               int                  `json:"estimated_files"`
	EstimatedLines               int                  `json:"estimated_lines"`
	Fingerprint                  string               `json:"fingerprint,omitempty"`
	PrimaryFingerprint           string               `json:"primary_fingerprint,omitempty"`
	ReferenceFingerprint         string               `json:"reference_fingerprint,omitempty"`
	PrimarySemanticFingerprint   string               `json:"primary_semantic_fingerprint,omitempty"`
	ReferenceSemanticFingerprint string               `json:"reference_semantic_fingerprint,omitempty"`
	CacheStatus                  string               `json:"cache_status,omitempty"`
	InvalidationReason           string               `json:"invalidation_reason,omitempty"`
	InvalidationDiff             []string             `json:"invalidation_diff,omitempty"`
	InvalidationChanges          []InvalidationChange `json:"invalidation_changes,omitempty"`
}

type InvalidationChange struct {
	Kind    string `json:"kind,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Subject string `json:"subject,omitempty"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
	Source  string `json:"source,omitempty"`
}

type WorkerReport struct {
	ShardID             string               `json:"shard_id"`
	Title               string               `json:"title"`
	ScopeSummary        string               `json:"scope_summary"`
	Responsibilities    []string             `json:"responsibilities"`
	Facts               []string             `json:"facts,omitempty"`
	Inferences          []string             `json:"inferences,omitempty"`
	KeyFiles            []string             `json:"key_files"`
	EntryPoints         []string             `json:"entry_points"`
	InternalFlow        []string             `json:"internal_flow"`
	Dependencies        []string             `json:"dependencies"`
	Collaboration       []string             `json:"collaboration"`
	Risks               []string             `json:"risks"`
	Unknowns            []string             `json:"unknowns"`
	EvidenceFiles       []string             `json:"evidence_files"`
	RootCauseCandidates []RootCauseCandidate `json:"root_cause_candidates,omitempty"`
	Narrative           string               `json:"narrative"`
	Raw                 string               `json:"raw,omitempty"`
}

type RootCauseCandidate struct {
	Title                      string                       `json:"title"`
	CandidateChain             []string                     `json:"candidate_chain,omitempty"`
	CausalChain                RootCauseCausalChain         `json:"causal_chain,omitempty"`
	TriggerValues              []string                     `json:"trigger_values,omitempty"`
	ExpectedRange              []string                     `json:"expected_range,omitempty"`
	OutOfRangeCases            []string                     `json:"out_of_range_cases,omitempty"`
	ObservedFailurePath        []string                     `json:"observed_failure_path,omitempty"`
	EvidenceFiles              []string                     `json:"evidence_files,omitempty"`
	DisconfirmingEvidence      []string                     `json:"disconfirming_evidence,omitempty"`
	CannotBeRootCauseIf        []string                     `json:"cannot_be_root_cause_if,omitempty"`
	RequiredRuntimeObservation []string                     `json:"required_runtime_observation,omitempty"`
	VerificationSteps          []string                     `json:"verification_steps,omitempty"`
	Probes                     []RootCauseProbe             `json:"probes,omitempty"`
	Confidence                 string                       `json:"confidence,omitempty"`
	ConfidenceBreakdown        RootCauseConfidenceBreakdown `json:"confidence_breakdown,omitempty"`
	NeedsCrossShardEvidence    []string                     `json:"needs_cross_shard_evidence,omitempty"`
	PatternIDs                 []string                     `json:"pattern_ids,omitempty"`
}

type RootCauseInvestigation struct {
	Symptom           RootCauseSymptomProfile     `json:"symptom,omitempty"`
	Hypotheses        []RootCauseHypothesis       `json:"hypotheses,omitempty"`
	CodeMatches       []RootCauseCodeMatch        `json:"code_matches,omitempty"`
	DeepVerifications []RootCauseDeepVerification `json:"deep_verifications,omitempty"`
	JoinedCandidates  []RootCauseJoinedCandidate  `json:"joined_candidates,omitempty"`
	CandidateClusters []RootCauseCandidateCluster `json:"candidate_clusters,omitempty"`
	EvidenceRequests  []RootCauseEvidenceRequest  `json:"evidence_requests,omitempty"`
	PatternMatches    []RootCausePatternMatch     `json:"pattern_matches,omitempty"`
	AuditTrail        RootCauseAuditTrail         `json:"audit_trail,omitempty"`
	RegressionMemory  RootCauseAuditTrail         `json:"regression_memory,omitempty"`
}

type RootCauseSymptomProfile struct {
	Symptom            string   `json:"symptom,omitempty"`
	ExpectedBehavior   string   `json:"expected_behavior,omitempty"`
	ObservedBehavior   string   `json:"observed_behavior,omitempty"`
	Frequency          string   `json:"frequency,omitempty"`
	TriggerKeywords    []string `json:"trigger_keywords,omitempty"`
	AffectedSurface    []string `json:"affected_surface,omitempty"`
	MustExplain        []string `json:"must_explain,omitempty"`
	ReproductionInputs []string `json:"reproduction_inputs,omitempty"`
}

type RootCauseHypothesis struct {
	ID                 string   `json:"id,omitempty"`
	Title              string   `json:"title,omitempty"`
	CandidateMechanism string   `json:"candidate_mechanism,omitempty"`
	TargetSignals      []string `json:"target_signals,omitempty"`
	TargetFiles        []string `json:"target_files,omitempty"`
	MustProve          []string `json:"must_prove,omitempty"`
	MustDisprove       []string `json:"must_disprove,omitempty"`
	ReproductionInputs []string `json:"reproduction_inputs,omitempty"`
}

type RootCauseJoinedCandidate struct {
	Title                      string                       `json:"title,omitempty"`
	Classification             string                       `json:"classification,omitempty"`
	ClusterID                  string                       `json:"cluster_id,omitempty"`
	ClusterMembers             []string                     `json:"cluster_members,omitempty"`
	CompetesWith               []string                     `json:"competes_with,omitempty"`
	DependsOn                  []string                     `json:"depends_on,omitempty"`
	CanCoexistWith             []string                     `json:"can_coexist_with,omitempty"`
	JoinedChain                []string                     `json:"joined_chain,omitempty"`
	CausalChain                RootCauseCausalChain         `json:"causal_chain,omitempty"`
	SupportingCandidates       []string                     `json:"supporting_candidates,omitempty"`
	EvidenceFiles              []string                     `json:"evidence_files,omitempty"`
	DisconfirmingEvidence      []string                     `json:"disconfirming_evidence,omitempty"`
	CannotBeRootCauseIf        []string                     `json:"cannot_be_root_cause_if,omitempty"`
	RequiredRuntimeObservation []string                     `json:"required_runtime_observation,omitempty"`
	VerificationSteps          []string                     `json:"verification_steps,omitempty"`
	Probes                     []RootCauseProbe             `json:"probes,omitempty"`
	Confidence                 string                       `json:"confidence,omitempty"`
	ConfidenceBreakdown        RootCauseConfidenceBreakdown `json:"confidence_breakdown,omitempty"`
	ConfidenceScore            int                          `json:"confidence_score,omitempty"`
	PatternIDs                 []string                     `json:"pattern_ids,omitempty"`
}

type RootCauseCausalChain struct {
	Trigger            string   `json:"trigger,omitempty"`
	InvalidState       string   `json:"invalid_state,omitempty"`
	StateTransition    string   `json:"state_transition,omitempty"`
	MissingGuard       string   `json:"missing_guard,omitempty"`
	UserVisibleSymptom string   `json:"user_visible_symptom,omitempty"`
	EvidenceFiles      []string `json:"evidence_files,omitempty"`
}

type RootCauseCodeMatch struct {
	Query          string   `json:"query,omitempty"`
	File           string   `json:"file,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	MatchedSignals []string `json:"matched_signals,omitempty"`
	Score          int      `json:"score,omitempty"`
}

type RootCauseDeepVerification struct {
	CandidateTitle             string                       `json:"candidate_title,omitempty"`
	ShardID                    string                       `json:"shard_id,omitempty"`
	ShardName                  string                       `json:"shard_name,omitempty"`
	Status                     string                       `json:"status,omitempty"`
	Summary                    string                       `json:"summary,omitempty"`
	CausalChain                RootCauseCausalChain         `json:"causal_chain,omitempty"`
	EvidenceFiles              []string                     `json:"evidence_files,omitempty"`
	InstrumentationSteps       []string                     `json:"instrumentation_steps,omitempty"`
	DisconfirmingEvidence      []string                     `json:"disconfirming_evidence,omitempty"`
	CannotBeRootCauseIf        []string                     `json:"cannot_be_root_cause_if,omitempty"`
	RequiredRuntimeObservation []string                     `json:"required_runtime_observation,omitempty"`
	Probes                     []RootCauseProbe             `json:"probes,omitempty"`
	Confidence                 string                       `json:"confidence,omitempty"`
	ConfidenceBreakdown        RootCauseConfidenceBreakdown `json:"confidence_breakdown,omitempty"`
	PatternIDs                 []string                     `json:"pattern_ids,omitempty"`
	Raw                        string                       `json:"raw,omitempty"`
}

type RootCauseProbe struct {
	Title          string `json:"title,omitempty"`
	Kind           string `json:"kind,omitempty"`
	Target         string `json:"target,omitempty"`
	Command        string `json:"command,omitempty"`
	ExpectedSignal string `json:"expected_signal,omitempty"`
	DisprovesWhen  string `json:"disproves_when,omitempty"`
}

type RootCauseConfidenceBreakdown struct {
	CausalCompleteness      int      `json:"causal_completeness,omitempty"`
	EvidenceStrength        int      `json:"evidence_strength,omitempty"`
	RuntimeObservability    int      `json:"runtime_observability,omitempty"`
	AlternativeExplanations int      `json:"alternative_explanations,omitempty"`
	DisconfirmationStrength int      `json:"disconfirmation_strength,omitempty"`
	Score                   int      `json:"score,omitempty"`
	Reasons                 []string `json:"reasons,omitempty"`
}

type RootCauseEvidenceRequest struct {
	ID                     string   `json:"id,omitempty"`
	Request                string   `json:"request,omitempty"`
	TargetSignals          []string `json:"target_signals,omitempty"`
	TargetFiles            []string `json:"target_files,omitempty"`
	Reason                 string   `json:"reason,omitempty"`
	RequiredToProve        string   `json:"required_to_prove,omitempty"`
	SourceShardID          string   `json:"source_shard_id,omitempty"`
	Status                 string   `json:"status,omitempty"`
	RoutedShardIDs         []string `json:"routed_shard_ids,omitempty"`
	FulfilledByShards      []string `json:"fulfilled_by_shards,omitempty"`
	SatisfiedEvidenceFiles []string `json:"satisfied_evidence_files,omitempty"`
}

type RootCauseCandidateCluster struct {
	ID              string               `json:"id,omitempty"`
	Title           string               `json:"title,omitempty"`
	CandidateTitles []string             `json:"candidate_titles,omitempty"`
	ShardIDs        []string             `json:"shard_ids,omitempty"`
	EvidenceFiles   []string             `json:"evidence_files,omitempty"`
	CausalChain     RootCauseCausalChain `json:"causal_chain,omitempty"`
	ConfidenceScore int                  `json:"confidence_score,omitempty"`
}

type RootCauseAuditTrail struct {
	GeneratedAt        time.Time                   `json:"generated_at,omitempty"`
	Symptom            string                      `json:"symptom,omitempty"`
	CodeMatches        []RootCauseCodeMatch        `json:"code_matches,omitempty"`
	EvidenceRequests   []RootCauseEvidenceRequest  `json:"evidence_requests,omitempty"`
	PatternMatches     []RootCausePatternMatch     `json:"pattern_matches,omitempty"`
	CandidateDecisions []RootCauseCandidateAudit   `json:"candidate_decisions,omitempty"`
	DeepVerifications  []RootCauseDeepVerification `json:"deep_verifications,omitempty"`
	JoinedCandidates   []RootCauseJoinedCandidate  `json:"joined_candidates,omitempty"`
}

type RootCauseCandidateAudit struct {
	ShardID                   string   `json:"shard_id,omitempty"`
	ShardName                 string   `json:"shard_name,omitempty"`
	ReportTitle               string   `json:"report_title,omitempty"`
	CandidateTitle            string   `json:"candidate_title,omitempty"`
	ReviewStatus              string   `json:"review_status,omitempty"`
	SymptomPossible           string   `json:"symptom_possible,omitempty"`
	CausalChainStages         []string `json:"causal_chain_stages,omitempty"`
	CausalChainMissing        []string `json:"causal_chain_missing,omitempty"`
	Decision                  string   `json:"decision,omitempty"`
	Reason                    string   `json:"reason,omitempty"`
	QualityGateIssues         []string `json:"quality_gate_issues,omitempty"`
	EvidenceFiles             []string `json:"evidence_files,omitempty"`
	DeepVerificationStatus    string   `json:"deep_verification_status,omitempty"`
	PatternIDs                []string `json:"pattern_ids,omitempty"`
	ExplicitPatternIDs        []string `json:"explicit_pattern_ids,omitempty"`
	InferredRelatedPatternIDs []string `json:"inferred_related_pattern_ids,omitempty"`
}

type AnalysisGoalScope struct {
	DirectoryPrefixes []string `json:"directory_prefixes,omitempty"`
}

type KnowledgeSubsystem struct {
	Title                string               `json:"title"`
	Group                string               `json:"group,omitempty"`
	ShardIDs             []string             `json:"shard_ids,omitempty"`
	ShardNames           []string             `json:"shard_names,omitempty"`
	CacheStatuses        []string             `json:"cache_statuses,omitempty"`
	InvalidationReasons  []string             `json:"invalidation_reasons,omitempty"`
	InvalidationEvidence []string             `json:"invalidation_evidence,omitempty"`
	InvalidationDiff     []string             `json:"invalidation_diff,omitempty"`
	InvalidationChanges  []InvalidationChange `json:"invalidation_changes,omitempty"`
	Responsibilities     []string             `json:"responsibilities,omitempty"`
	Facts                []string             `json:"facts,omitempty"`
	Inferences           []string             `json:"inferences,omitempty"`
	KeyFiles             []string             `json:"key_files,omitempty"`
	EvidenceFiles        []string             `json:"evidence_files,omitempty"`
	EntryPoints          []string             `json:"entry_points,omitempty"`
	Dependencies         []string             `json:"dependencies,omitempty"`
	Collaboration        []string             `json:"collaboration,omitempty"`
	Risks                []string             `json:"risks,omitempty"`
	Unknowns             []string             `json:"unknowns,omitempty"`
	RootCauseCandidates  []RootCauseCandidate `json:"root_cause_candidates,omitempty"`
}

type PerformanceHotspot struct {
	Title       string   `json:"title"`
	Category    string   `json:"category,omitempty"`
	Score       int      `json:"score,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	Files       []string `json:"files,omitempty"`
	EntryPoints []string `json:"entry_points,omitempty"`
	Signals     []string `json:"signals,omitempty"`
}

type PerformanceLens struct {
	PrimaryStartup       string               `json:"primary_startup,omitempty"`
	StartupEntryFiles    []string             `json:"startup_entry_files,omitempty"`
	CriticalPaths        []string             `json:"critical_paths,omitempty"`
	Hotspots             []PerformanceHotspot `json:"hotspots,omitempty"`
	LargeFiles           []string             `json:"large_files,omitempty"`
	HeavyEntrypoints     []string             `json:"heavy_entrypoints,omitempty"`
	IOBoundCandidates    []string             `json:"io_bound_candidates,omitempty"`
	CPUBoundCandidates   []string             `json:"cpu_bound_candidates,omitempty"`
	MemoryRiskCandidates []string             `json:"memory_risk_candidates,omitempty"`
}

type AnalysisExecutionSummary struct {
	TotalShards                 int      `json:"total_shards,omitempty"`
	ReusedShards                int      `json:"reused_shards,omitempty"`
	MissedShards                int      `json:"missed_shards,omitempty"`
	NewShards                   int      `json:"new_shards,omitempty"`
	RecomputedShards            int      `json:"recomputed_shards,omitempty"`
	SemanticRecomputedShards    int      `json:"semantic_recomputed_shards,omitempty"`
	InvalidationReasons         []string `json:"invalidation_reasons,omitempty"`
	SemanticInvalidationReasons []string `json:"semantic_invalidation_reasons,omitempty"`
	TopChangeClasses            []string `json:"top_change_classes,omitempty"`
	TopChangeExamples           []string `json:"top_change_examples,omitempty"`
}

type VectorCorpusDocument struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind"`
	Title    string            `json:"title"`
	Text     string            `json:"text"`
	PathHint string            `json:"path_hint,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type VectorCorpus struct {
	RunID       string                 `json:"run_id"`
	Goal        string                 `json:"goal"`
	Root        string                 `json:"root"`
	GeneratedAt time.Time              `json:"generated_at"`
	Documents   []VectorCorpusDocument `json:"documents,omitempty"`
}

type VectorIngestionManifest struct {
	RunID         string                  `json:"run_id"`
	Goal          string                  `json:"goal"`
	Root          string                  `json:"root"`
	GeneratedAt   time.Time               `json:"generated_at"`
	DocumentCount int                     `json:"document_count"`
	DocumentKinds []string                `json:"document_kinds,omitempty"`
	Targets       []VectorIngestionTarget `json:"targets,omitempty"`
}

type VectorIngestionTarget struct {
	Name        string `json:"name"`
	Format      string `json:"format"`
	Filename    string `json:"filename"`
	Description string `json:"description,omitempty"`
}

type KnowledgePack struct {
	RunID                string                   `json:"run_id"`
	Goal                 string                   `json:"goal"`
	AnalysisMode         string                   `json:"analysis_mode,omitempty"`
	Root                 string                   `json:"root"`
	GeneratedAt          time.Time                `json:"generated_at"`
	AnalysisLenses       []AnalysisLens           `json:"analysis_lenses,omitempty"`
	ProjectSummary       string                   `json:"project_summary,omitempty"`
	PrimaryStartup       string                   `json:"primary_startup,omitempty"`
	StartupCandidates    []string                 `json:"startup_candidates,omitempty"`
	StartupEntryFiles    []string                 `json:"startup_entry_files,omitempty"`
	ManifestFiles        []string                 `json:"manifest_files,omitempty"`
	EntrypointFiles      []string                 `json:"entrypoint_files,omitempty"`
	ArchitectureGroups   []string                 `json:"architecture_groups,omitempty"`
	TopImportantFiles    []string                 `json:"top_important_files,omitempty"`
	ProjectEdges         []ProjectEdge            `json:"project_edges,omitempty"`
	UnrealProjects       []UnrealProject          `json:"unreal_projects,omitempty"`
	UnrealPlugins        []UnrealPlugin           `json:"unreal_plugins,omitempty"`
	UnrealTargets        []UnrealTarget           `json:"unreal_targets,omitempty"`
	UnrealModules        []UnrealModule           `json:"unreal_modules,omitempty"`
	UnrealTypes          []UnrealReflectedType    `json:"unreal_types,omitempty"`
	UnrealNetwork        []UnrealNetworkSurface   `json:"unreal_network,omitempty"`
	UnrealAssets         []UnrealAssetReference   `json:"unreal_assets,omitempty"`
	UnrealSystems        []UnrealGameplaySystem   `json:"unreal_systems,omitempty"`
	UnrealSettings       []UnrealProjectSetting   `json:"unreal_settings,omitempty"`
	PrimaryUnrealModule  string                   `json:"primary_unreal_module,omitempty"`
	Subsystems           []KnowledgeSubsystem     `json:"subsystems,omitempty"`
	ExternalDependencies []string                 `json:"external_dependencies,omitempty"`
	HighRiskFiles        []string                 `json:"high_risk_files,omitempty"`
	Unknowns             []string                 `json:"unknowns,omitempty"`
	AnalysisExecution    AnalysisExecutionSummary `json:"analysis_execution,omitempty"`
	PerformanceLens      PerformanceLens          `json:"performance_lens,omitempty"`
	RootCause            RootCauseInvestigation   `json:"root_cause,omitempty"`
	RootCausePatterns    []RootCausePatternMatch  `json:"root_cause_patterns,omitempty"`
	ArchitectureFacts    ArchitectureFactPack     `json:"architecture_facts,omitempty"`
}

type ReviewDecision struct {
	Status                     string                     `json:"status"`
	Issues                     []string                   `json:"issues,omitempty"`
	RevisionPrompt             string                     `json:"revision_prompt,omitempty"`
	SymptomPossible            string                     `json:"symptom_possible,omitempty"`
	SymptomCausality           []string                   `json:"symptom_causality,omitempty"`
	SymptomReproductionBridge  []string                   `json:"symptom_reproduction_bridge,omitempty"`
	RequiredRuntimeObservation []string                   `json:"required_runtime_observation,omitempty"`
	DisqualifyingEvidence      []string                   `json:"disqualifying_evidence,omitempty"`
	CausalChainComplete        bool                       `json:"causal_chain_complete,omitempty"`
	CausalChainStages          []string                   `json:"causal_chain_stages,omitempty"`
	CausalChainMissing         []string                   `json:"causal_chain_missing,omitempty"`
	Disconfirmed               bool                       `json:"disconfirmed,omitempty"`
	DisconfirmingEvidence      []string                   `json:"disconfirming_evidence,omitempty"`
	RejectedCandidates         []string                   `json:"rejected_candidates,omitempty"`
	EvidenceRequests           []RootCauseEvidenceRequest `json:"evidence_requests,omitempty"`
	FailureKind                string                     `json:"failure_kind,omitempty"`
	Raw                        string                     `json:"raw,omitempty"`
}

type ProjectAnalysisRun struct {
	Summary          ProjectAnalysisSummary  `json:"summary"`
	Snapshot         ProjectSnapshot         `json:"snapshot"`
	Shards           []AnalysisShard         `json:"shards"`
	Reports          []WorkerReport          `json:"reports"`
	Reviews          []ReviewDecision        `json:"reviews"`
	FinalDocument    string                  `json:"final_document"`
	ConductorProfile string                  `json:"conductor_profile,omitempty"`
	WorkerProfile    string                  `json:"worker_profile,omitempty"`
	ReviewerProfile  string                  `json:"reviewer_profile,omitempty"`
	RootCause        RootCauseInvestigation  `json:"root_cause,omitempty"`
	KnowledgePack    KnowledgePack           `json:"knowledge_pack,omitempty"`
	SemanticIndex    SemanticIndex           `json:"semantic_index,omitempty"`
	SemanticIndexV2  SemanticIndexV2         `json:"semantic_index_v2,omitempty"`
	UnrealGraph      UnrealSemanticGraph     `json:"unreal_graph,omitempty"`
	VectorCorpus     VectorCorpus            `json:"vector_corpus,omitempty"`
	VectorIngestion  VectorIngestionManifest `json:"vector_ingestion,omitempty"`
	DebugEvents      []string                `json:"debug_events,omitempty"`
	ShardDocuments   map[string]string       `json:"shard_documents,omitempty"`
}

const (
	analysisReviewIssueProvider = "provider"
	analysisReviewIssueQuality  = "quality"

	rootCauseEvidenceRequestRounds         = 2
	rootCauseMaxEvidenceRequestShardsRound = 4
)

func summarizeReviewDecisions(summary *ProjectAnalysisSummary, reviews []ReviewDecision) {
	summary.ApprovedShards = 0
	summary.ReviewFailures = 0
	summary.ReviewProviderFailures = 0
	summary.ReviewQualityIssues = 0
	for _, review := range reviews {
		if strings.EqualFold(review.Status, "approved") {
			summary.ApprovedShards++
			continue
		}
		switch classifyReviewIssueKind(review) {
		case analysisReviewIssueProvider:
			summary.ReviewProviderFailures++
		case analysisReviewIssueQuality:
			summary.ReviewQualityIssues++
		}
	}
	summary.ReviewFailures = summary.ReviewProviderFailures + summary.ReviewQualityIssues
}

func filterRootCauseReportsByReview(snapshot ProjectSnapshot, reports []WorkerReport, reviews []ReviewDecision) []WorkerReport {
	if normalizeProjectAnalysisMode(snapshot.AnalysisMode) != "root-cause" || len(reports) == 0 {
		return reports
	}
	out := append([]WorkerReport(nil), reports...)
	for index := range out {
		if index >= len(reviews) {
			if len(out[index].RootCauseCandidates) > 0 {
				out[index].Unknowns = analysisUniqueStrings(append(out[index].Unknowns, "Root-cause candidate withheld from synthesis because no reviewer decision was available for this shard."))
				out[index].RootCauseCandidates = nil
			}
			continue
		}
		review := reviews[index]
		if rootCauseReviewApprovesSymptomCausality(review) {
			if len(review.RejectedCandidates) > 0 && len(out[index].RootCauseCandidates) > 0 {
				kept := []RootCauseCandidate{}
				rejectedTitles := []string{}
				for _, candidate := range out[index].RootCauseCandidates {
					if rootCauseCandidateRejectedByReview(candidate, review) {
						rejectedTitles = append(rejectedTitles, firstNonBlankAnalysisString(candidate.Title, "unnamed candidate"))
						continue
					}
					kept = append(kept, candidate)
				}
				if len(kept) != len(out[index].RootCauseCandidates) {
					out[index].Unknowns = analysisUniqueStrings(append(out[index].Unknowns, "Root-cause candidate rejected by reviewer: "+strings.Join(limitStrings(rejectedTitles, 3), " | ")))
					out[index].RootCauseCandidates = kept
				}
			}
			continue
		}
		if len(out[index].RootCauseCandidates) > 0 {
			out[index].Unknowns = analysisUniqueStrings(append(out[index].Unknowns, rootCauseReviewRejectionNote(review)))
			out[index].RootCauseCandidates = nil
		}
	}
	return out
}

func applyRootCauseDeterministicQualityGate(snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, reviews []ReviewDecision) []WorkerReport {
	if normalizeProjectAnalysisMode(snapshot.AnalysisMode) != "root-cause" || len(reports) == 0 {
		return reports
	}
	out := append([]WorkerReport(nil), reports...)
	for index := range out {
		shard := AnalysisShard{}
		if index < len(shards) {
			shard = shards[index]
		}
		review := ReviewDecision{}
		if index < len(reviews) {
			review = reviews[index]
		}
		kept := []RootCauseCandidate{}
		for _, candidate := range out[index].RootCauseCandidates {
			gate := evaluateRootCauseCandidateQualityGate(snapshot, shard, candidate, review)
			if gate.Reject {
				out[index].Unknowns = analysisUniqueStrings(append(out[index].Unknowns, rootCauseQualityGateReportNote(candidate, gate)))
				continue
			}
			if len(gate.Issues) > 0 {
				candidate.NeedsCrossShardEvidence = analysisUniqueStrings(append(candidate.NeedsCrossShardEvidence, gate.Issues...))
				candidate.ConfidenceBreakdown.Reasons = analysisUniqueStrings(append(candidate.ConfidenceBreakdown.Reasons, gate.Issues...))
				candidate.ConfidenceBreakdown.Score = analysisMinInt(candidate.ConfidenceBreakdown.Score, gate.MaxScore)
				candidate.ConfidenceBreakdown = normalizeRootCauseConfidenceBreakdown(candidate.ConfidenceBreakdown, gate.MaxScore)
				if gate.DowngradeTo != "" {
					candidate.Confidence = gate.DowngradeTo
				}
				out[index].Unknowns = analysisUniqueStrings(append(out[index].Unknowns, rootCauseQualityGateReportNote(candidate, gate)))
			}
			kept = append(kept, candidate)
		}
		out[index].RootCauseCandidates = normalizeRootCauseCandidates(kept, shard)
	}
	return out
}

type rootCauseQualityGateResult struct {
	Issues      []string
	Reject      bool
	DowngradeTo string
	MaxScore    int
}

func evaluateRootCauseCandidateQualityGate(snapshot ProjectSnapshot, shard AnalysisShard, candidate RootCauseCandidate, review ReviewDecision) rootCauseQualityGateResult {
	result := rootCauseQualityGateResult{MaxScore: 100}
	stageCount := rootCauseCausalChainStageCount(candidate.CausalChain)
	if stageCount < 3 {
		result.Issues = append(result.Issues, fmt.Sprintf("Deterministic gate: only %d causal stage(s) are evidenced; at least 3 are required for a shard-level candidate.", stageCount))
		result.Reject = true
		result.MaxScore = 25
		return result
	}
	if stageCount < 4 {
		result.Issues = append(result.Issues, "Deterministic gate: fewer than 4 causal stages, so this cannot be promoted to root_cause without more evidence.")
		result.DowngradeTo = rootCauseLowerConfidence(candidate.Confidence, "medium")
		result.MaxScore = analysisMinInt(result.MaxScore, 60)
	}
	if len(candidate.EvidenceFiles) == 0 {
		result.Issues = append(result.Issues, "Deterministic gate: candidate has no evidence_files.")
		result.DowngradeTo = rootCauseLowerConfidence(candidate.Confidence, "low")
		result.MaxScore = analysisMinInt(result.MaxScore, 45)
	}
	if !rootCauseCandidateHasValidProbe(candidate) {
		result.Issues = append(result.Issues, "Deterministic gate: candidate probes must include expected_signal and disproves_when.")
		result.DowngradeTo = rootCauseLowerConfidence(candidate.Confidence, "medium")
		result.MaxScore = analysisMinInt(result.MaxScore, 65)
	}
	if !rootCauseCandidateHasConcreteStateSignal(candidate) {
		result.Issues = append(result.Issues, "Deterministic gate: candidate must name at least one concrete variable, field, config key, DB value, enum, boolean, counter, or numeric limit.")
		result.DowngradeTo = rootCauseLowerConfidence(candidate.Confidence, "medium")
		result.MaxScore = analysisMinInt(result.MaxScore, 60)
	}
	if rootCauseCandidateUserSymptomMismatches(snapshot, candidate) {
		result.Issues = append(result.Issues, "Deterministic gate: candidate user_visible_symptom does not overlap the reported symptom.")
		result.Reject = true
		result.MaxScore = 20
		return result
	}
	if strings.EqualFold(strings.TrimSpace(review.Status), "approved") {
		if missing := rootCauseReviewContractMissing(review); len(missing) > 0 {
			result.Issues = append(result.Issues, "Deterministic gate: reviewer approval is missing required root-cause validation fields: "+strings.Join(missing, ", "))
			result.Reject = true
			result.MaxScore = 20
			return result
		}
	}
	if rootCauseReviewApprovesSymptomCausality(review) && strings.EqualFold(review.SymptomPossible, "yes") && stageCount < 4 {
		result.Issues = append(result.Issues, "Deterministic gate: reviewer marked symptom_possible=yes but source evidence has fewer than 4 causal stages.")
		result.DowngradeTo = rootCauseLowerConfidence(candidate.Confidence, "medium")
		result.MaxScore = analysisMinInt(result.MaxScore, 55)
	}
	result.Issues = analysisUniqueStrings(result.Issues)
	if result.MaxScore <= 45 && result.DowngradeTo == "" {
		result.DowngradeTo = "low"
	}
	return result
}

func rootCauseQualityGateReportNote(candidate RootCauseCandidate, gate rootCauseQualityGateResult) string {
	title := firstNonBlankAnalysisString(candidate.Title, "unnamed candidate")
	prefix := "Root-cause candidate downgraded by deterministic gate"
	if gate.Reject {
		prefix = "Root-cause candidate rejected by deterministic gate"
	}
	return fmt.Sprintf("%s: %s: %s", prefix, title, strings.Join(limitStrings(gate.Issues, 4), " | "))
}

func rootCauseLowerConfidence(current string, target string) string {
	rank := map[string]int{"low": 0, "medium": 1, "high": 2}
	current = normalizeRootCauseConfidence(current, 50)
	target = normalizeRootCauseConfidence(target, 50)
	if rank[target] < rank[current] {
		return target
	}
	return current
}

func rootCauseCandidateHasValidProbe(candidate RootCauseCandidate) bool {
	for _, probe := range candidate.Probes {
		if strings.TrimSpace(probe.ExpectedSignal) != "" && strings.TrimSpace(probe.DisprovesWhen) != "" {
			return true
		}
	}
	return false
}

func rootCauseCandidateHasConcreteStateSignal(candidate RootCauseCandidate) bool {
	fields := []string{
		candidate.CausalChain.Trigger,
		candidate.CausalChain.InvalidState,
		candidate.CausalChain.StateTransition,
		candidate.CausalChain.MissingGuard,
	}
	fields = append(fields, candidate.TriggerValues...)
	fields = append(fields, candidate.ExpectedRange...)
	fields = append(fields, candidate.OutOfRangeCases...)
	fields = append(fields, candidate.ObservedFailurePath...)
	for _, field := range fields {
		if rootCauseTextHasConcreteStateSignal(field) {
			return true
		}
	}
	return false
}

func rootCauseTextHasConcreteStateSignal(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(\.|->|::)[A-Za-z_][A-Za-z0-9_]*`).MatchString(trimmed) {
		return true
	}
	if regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*_[A-Za-z0-9_]+`).MatchString(trimmed) {
		return true
	}
	if regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*(Count|Limit|Size|ID|Id|Key|Flag|Status|Handle|Timeout|Value|Config|State|Mode|Enabled|Disabled|Available|Unavailable)\b`).MatchString(trimmed) {
		return true
	}
	for _, token := range []string{"=", "==", "!=", "<", ">", "::", "->"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	keywords := map[string]struct{}{
		"true": {}, "false": {}, "null": {}, "nil": {}, "nullptr": {}, "config": {}, "db": {}, "database": {}, "enum": {}, "flag": {}, "count": {}, "limit": {}, "size": {}, "id": {}, "cache": {}, "timeout": {}, "handle": {}, "status": {}, "value": {}, "key": {}, "counter": {}, "bool": {}, "int": {},
	}
	for _, token := range regexp.MustCompile(`[A-Za-z0-9_]+`).FindAllString(lower, -1) {
		if _, ok := keywords[token]; ok {
			return true
		}
	}
	for _, r := range lower {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func rootCauseCandidateUserSymptomMismatches(snapshot ProjectSnapshot, candidate RootCauseCandidate) bool {
	expected := rootCauseReportedSymptomText(snapshot)
	actual := firstNonBlankRootCauseString(candidate.CausalChain.UserVisibleSymptom, firstString(candidate.ObservedFailurePath))
	if strings.TrimSpace(expected) == "" || strings.TrimSpace(actual) == "" {
		return false
	}
	expectedTerms := rootCauseEvidenceTerms(expected)
	actualTerms := rootCauseEvidenceTerms(actual)
	if len(expectedTerms) < 2 || len(actualTerms) < 1 {
		return false
	}
	expectedLower := strings.ToLower(expected)
	actualLower := strings.ToLower(actual)
	for _, term := range expectedTerms {
		if rootCauseTextContainsTerm(actualLower, term) {
			return false
		}
	}
	for _, term := range actualTerms {
		if rootCauseTextContainsTerm(expectedLower, term) {
			return false
		}
	}
	return true
}

func rootCauseReportedSymptomText(snapshot ProjectSnapshot) string {
	parts := []string{
		snapshot.RootCause.Symptom.Symptom,
		snapshot.RootCause.Symptom.ObservedBehavior,
	}
	parts = append(parts, snapshot.RootCause.Symptom.MustExplain...)
	return strings.Join(analysisUniqueStrings(parts), " ")
}

func rootCauseReviewApprovesSymptomCausality(review ReviewDecision) bool {
	if review.Disconfirmed {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(review.Status), "approved") {
		return false
	}
	if missing := rootCauseReviewContractMissing(review); len(missing) > 0 {
		return false
	}
	if len(review.SymptomCausality) == 0 {
		return false
	}
	stageCount := rootCauseReviewCausalStageCount(review)
	switch strings.ToLower(strings.TrimSpace(review.SymptomPossible)) {
	case "yes", "true", "possible":
		return review.CausalChainComplete || stageCount >= 4
	case "partial":
		return stageCount >= 3
	default:
		return false
	}
}

func rootCauseReviewContractMissing(review ReviewDecision) []string {
	if strings.TrimSpace(review.Raw) == "" {
		return nil
	}
	missing := []string{}
	if len(review.SymptomReproductionBridge) == 0 {
		missing = append(missing, "symptom_reproduction_bridge")
	}
	if len(review.RequiredRuntimeObservation) == 0 {
		missing = append(missing, "required_runtime_observation")
	}
	if len(review.DisqualifyingEvidence) == 0 {
		missing = append(missing, "disqualifying_evidence")
	}
	return missing
}

func rootCauseReviewCausalStageCount(review ReviewDecision) int {
	if review.CausalChainComplete {
		return len(rootCauseCausalChainStageNames())
	}
	return len(rootCauseNormalizeCausalStages(review.CausalChainStages))
}

func rootCauseCausalChainStageNames() []string {
	return []string{"trigger", "invalid_state", "state_transition", "missing_guard", "user_visible_symptom"}
}

func rootCauseNormalizeCausalStages(stages []string) []string {
	out := []string{}
	for _, stage := range stages {
		normalized := rootCauseNormalizeCausalStage(stage)
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return analysisUniqueStrings(out)
}

func rootCauseNormalizeCausalStage(stage string) string {
	key := strings.ToLower(strings.TrimSpace(stage))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, " ", "_")
	switch key {
	case "trigger", "trigger_value", "trigger_values", "input", "input_state":
		return "trigger"
	case "invalid_state", "unexpected_state", "out_of_range", "out_of_range_value", "bad_value":
		return "invalid_state"
	case "state_transition", "branch", "branch_transition", "flow", "path":
		return "state_transition"
	case "missing_guard", "guard", "missing_validation", "finalization_gate", "missing_finalization":
		return "missing_guard"
	case "user_visible_symptom", "symptom", "failure", "observed_failure":
		return "user_visible_symptom"
	default:
		return ""
	}
}

func rootCauseCausalChainStageCount(chain RootCauseCausalChain) int {
	count := 0
	if strings.TrimSpace(chain.Trigger) != "" {
		count++
	}
	if strings.TrimSpace(chain.InvalidState) != "" {
		count++
	}
	if strings.TrimSpace(chain.StateTransition) != "" {
		count++
	}
	if strings.TrimSpace(chain.MissingGuard) != "" {
		count++
	}
	if strings.TrimSpace(chain.UserVisibleSymptom) != "" {
		count++
	}
	return count
}

func rootCauseCausalChainStages(chain RootCauseCausalChain) []string {
	out := []string{}
	if strings.TrimSpace(chain.Trigger) != "" {
		out = append(out, "trigger")
	}
	if strings.TrimSpace(chain.InvalidState) != "" {
		out = append(out, "invalid_state")
	}
	if strings.TrimSpace(chain.StateTransition) != "" {
		out = append(out, "state_transition")
	}
	if strings.TrimSpace(chain.MissingGuard) != "" {
		out = append(out, "missing_guard")
	}
	if strings.TrimSpace(chain.UserVisibleSymptom) != "" {
		out = append(out, "user_visible_symptom")
	}
	return out
}

func rootCauseMissingCausalChainStages(chain RootCauseCausalChain) []string {
	present := map[string]struct{}{}
	for _, stage := range rootCauseCausalChainStages(chain) {
		present[stage] = struct{}{}
	}
	missing := []string{}
	for _, stage := range rootCauseCausalChainStageNames() {
		if _, ok := present[stage]; !ok {
			missing = append(missing, stage)
		}
	}
	return missing
}

func normalizeRootCauseCausalChain(chain RootCauseCausalChain, candidate RootCauseCandidate, shard AnalysisShard) RootCauseCausalChain {
	chain.Trigger = strings.TrimSpace(chain.Trigger)
	chain.InvalidState = strings.TrimSpace(chain.InvalidState)
	chain.StateTransition = strings.TrimSpace(chain.StateTransition)
	chain.MissingGuard = strings.TrimSpace(chain.MissingGuard)
	chain.UserVisibleSymptom = strings.TrimSpace(chain.UserVisibleSymptom)
	if chain.Trigger == "" {
		chain.Trigger = firstNonBlankAnalysisString(firstString(candidate.TriggerValues), firstString(candidate.CandidateChain))
	}
	if chain.InvalidState == "" {
		chain.InvalidState = firstNonBlankAnalysisString(firstString(candidate.OutOfRangeCases), firstString(candidate.ExpectedRange))
	}
	if chain.StateTransition == "" {
		chain.StateTransition = firstNonBlankAnalysisString(firstString(candidate.CandidateChain), firstString(candidate.ObservedFailurePath))
	}
	if chain.MissingGuard == "" {
		chain.MissingGuard = firstNonBlankAnalysisString(firstString(candidate.CannotBeRootCauseIf), firstString(candidate.RequiredRuntimeObservation))
	}
	if chain.UserVisibleSymptom == "" {
		chain.UserVisibleSymptom = firstString(candidate.ObservedFailurePath)
	}
	chain.EvidenceFiles = analysisUniqueStrings(filterEvidence(append(chain.EvidenceFiles, candidate.EvidenceFiles...), shard))
	if len(chain.EvidenceFiles) == 0 {
		chain.EvidenceFiles = append([]string(nil), shard.PrimaryFiles...)
	}
	return chain
}

func rootCauseCandidateRejectedByReview(candidate RootCauseCandidate, review ReviewDecision) bool {
	title := rootCauseComparableText(candidate.Title)
	if title == "" {
		return false
	}
	for _, rejected := range review.RejectedCandidates {
		needle := rootCauseComparableText(rejected)
		if len([]rune(needle)) < 4 {
			continue
		}
		if title == needle {
			return true
		}
		if rootCauseComparableTextIsSpecific(title) && strings.Contains(needle, title) {
			return true
		}
		if rootCauseComparableTextIsSpecific(needle) && strings.Contains(title, needle) {
			return true
		}
	}
	return false
}

func rootCauseComparableText(raw string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(raw))), " ")
}

func rootCauseComparableTextIsSpecific(text string) bool {
	text = strings.TrimSpace(text)
	return len([]rune(text)) >= 12 || len(strings.Fields(text)) >= 2
}

func rootCauseReviewRejectionNote(review ReviewDecision) string {
	parts := []string{"Root-cause candidate withheld from synthesis because reviewer did not validate that it can produce the reported symptom."}
	if strings.TrimSpace(review.SymptomPossible) != "" {
		parts = append(parts, "symptom_possible="+strings.TrimSpace(review.SymptomPossible))
	}
	if len(review.Issues) > 0 {
		parts = append(parts, "issues="+strings.Join(limitStrings(review.Issues, 3), " | "))
	}
	if len(review.RejectedCandidates) > 0 {
		parts = append(parts, "rejected="+strings.Join(limitStrings(review.RejectedCandidates, 3), " | "))
	}
	if len(review.DisconfirmingEvidence) > 0 {
		parts = append(parts, "disconfirming="+strings.Join(limitStrings(review.DisconfirmingEvidence, 3), " | "))
	}
	return strings.Join(parts, "; ")
}

func classifyReviewIssueKind(review ReviewDecision) string {
	kind := strings.ToLower(strings.TrimSpace(review.FailureKind))
	switch kind {
	case analysisReviewIssueProvider, analysisReviewIssueQuality:
		return kind
	}
	status := strings.ToLower(strings.TrimSpace(review.Status))
	switch status {
	case "", "approved":
		return ""
	case "needs_revision":
		return analysisReviewIssueQuality
	case "review_failed":
		if reviewLooksLikeProviderFailure(review) {
			return analysisReviewIssueProvider
		}
		if reviewLooksLikeQualityFailure(review) {
			return analysisReviewIssueQuality
		}
		return analysisReviewIssueProvider
	default:
		return analysisReviewIssueQuality
	}
}

func reviewLooksLikeProviderFailure(review ReviewDecision) bool {
	text := strings.ToLower(strings.Join(append(append([]string{}, review.Issues...), review.Raw), "\n"))
	for _, token := range []string{
		"provider",
		"request failed",
		"api error",
		"rate limit",
		"rate-limited",
		"timeout",
		"timed out",
		"aborted",
		"service unavailable",
		"no available workers",
		"no_available_workers",
		"worker request failed",
		"reviewer request failed",
		"429",
		"503",
		"504",
	} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func reviewLooksLikeWorkerProviderFailure(review ReviewDecision) bool {
	text := strings.ToLower(strings.Join(append(append([]string{}, review.Issues...), review.Raw), "\n"))
	for _, token := range []string{
		"worker request failed",
		"analysis worker request failed",
		"worker provider request failed",
	} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func reviewLooksLikeReviewerProviderFailure(review ReviewDecision) bool {
	text := strings.ToLower(strings.Join(append(append([]string{}, review.Issues...), review.Raw), "\n"))
	for _, token := range []string{
		"reviewer request failed",
		"analysis reviewer request failed",
		"reviewer provider request failed",
	} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func reviewLooksLikeQualityFailure(review ReviewDecision) bool {
	text := strings.ToLower(strings.Join(append(append([]string{}, review.Issues...), review.Raw), "\n"))
	for _, token := range []string{
		"quality",
		"generic",
		"missing",
		"omitted",
		"not grounded",
		"needs revision",
		"invalid report",
	} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func renderAnalysisReviewIssueBanner(summary ProjectAnalysisSummary) string {
	return renderAnalysisReviewIssueBannerForReviews(summary, nil)
}

func renderAnalysisReviewIssueBannerForReviews(summary ProjectAnalysisSummary, reviews []ReviewDecision) string {
	providerFailures := summary.ReviewProviderFailures
	qualityIssues := summary.ReviewQualityIssues
	if providerFailures == 0 && qualityIssues == 0 && summary.ReviewFailures > 0 {
		providerFailures = summary.ReviewFailures
	}
	if providerFailures == 0 && qualityIssues == 0 {
		return ""
	}

	title := "# Analysis With Review Issues"
	if providerFailures > 0 && qualityIssues == 0 {
		title = "# Analysis With Provider Failures"
	} else if providerFailures == 0 && qualityIssues > 0 {
		title = "# Analysis With Reviewer Quality Issues"
	}
	return title + "\n\n" + renderAnalysisReviewIssueDetailsForReviews(providerFailures, qualityIssues, reviews)
}

func renderAnalysisReviewIssueDetails(providerFailures int, qualityIssues int) string {
	return renderAnalysisReviewIssueDetailsForReviews(providerFailures, qualityIssues, nil)
}

func renderAnalysisReviewIssueDetailsForReviews(providerFailures int, qualityIssues int, reviews []ReviewDecision) string {
	lines := []string{}
	if providerFailures > 0 {
		lines = append(lines, fmt.Sprintf("Provider failures: %d shard(s) could not be completed because a worker or reviewer model/provider request failed. The final document was synthesized from available worker reports and approved shards, so affected sections have reduced review confidence.", providerFailures))
		if split := renderAnalysisProviderFailureSplit(reviews); strings.TrimSpace(split) != "" {
			lines = append(lines, split)
		}
		if examples := analysisProviderFailureExamples(reviews, 3); len(examples) > 0 {
			lines = append(lines, "Provider failure examples:\n- "+strings.Join(examples, "\n- "))
		}
	}
	if qualityIssues > 0 {
		lines = append(lines, fmt.Sprintf("Reviewer quality issues: %d shard report(s) remained rejected or marked as needing revision by the reviewer. Treat those areas as requiring follow-up analysis or manual verification.", qualityIssues))
	}
	return strings.Join(lines, "\n\n")
}

func renderAnalysisProviderFailureSplit(reviews []ReviewDecision) string {
	worker, reviewer, unknown := analysisProviderFailureSplit(reviews)
	if worker == 0 && reviewer == 0 && unknown == 0 {
		return ""
	}
	parts := []string{}
	if worker > 0 {
		parts = append(parts, fmt.Sprintf("worker=%d", worker))
	}
	if reviewer > 0 {
		parts = append(parts, fmt.Sprintf("reviewer=%d", reviewer))
	}
	if unknown > 0 {
		parts = append(parts, fmt.Sprintf("unknown=%d", unknown))
	}
	return "Provider failure split: " + strings.Join(parts, ", ") + "."
}

func analysisProviderFailureSplit(reviews []ReviewDecision) (int, int, int) {
	worker := 0
	reviewer := 0
	unknown := 0
	for _, review := range reviews {
		if classifyReviewIssueKind(review) != analysisReviewIssueProvider {
			continue
		}
		switch {
		case reviewLooksLikeWorkerProviderFailure(review):
			worker++
		case reviewLooksLikeReviewerProviderFailure(review):
			reviewer++
		default:
			unknown++
		}
	}
	return worker, reviewer, unknown
}

func analysisProviderFailureExamples(reviews []ReviewDecision, limit int) []string {
	if limit <= 0 {
		return nil
	}
	examples := []string{}
	seen := map[string]bool{}
	for _, review := range reviews {
		if classifyReviewIssueKind(review) != analysisReviewIssueProvider {
			continue
		}
		example := summarizeAnalysisProviderFailure(review.Raw)
		if example == "" {
			example = summarizeAnalysisProviderFailure(strings.Join(review.Issues, " "))
		}
		key := strings.ToLower(example)
		if example == "" || seen[key] {
			continue
		}
		seen[key] = true
		examples = append(examples, example)
		if len(examples) >= limit {
			break
		}
	}
	return examples
}

func summarizeAnalysisProviderFailure(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	if idx := strings.Index(text, "\n"); idx >= 0 {
		text = strings.TrimSpace(text[:idx])
	}
	for _, marker := range []string{" | request=", " request={", "\trequest={"} {
		if idx := strings.Index(text, marker); idx >= 0 {
			text = strings.TrimSpace(text[:idx])
		}
	}
	return truncateStatusSnippet(text, 320)
}

type projectAnalyzer struct {
	cfg                   Config
	analysisCfg           ProjectAnalysisConfig
	explicitScope         AnalysisGoalScope
	client                ProviderClient
	workerClient          ProviderClient
	reviewerClient        ProviderClient
	modelRoutes           *ModelRouteScheduler
	workspace             Workspace
	rootCausePatternPacks []string
	cachedUnrealGraph     UnrealSemanticGraph
	cachedSemanticIndexV2 SemanticIndexV2
	onStatus              func(string)
	onDebug               func(string)
	debugMu               sync.Mutex
	debugEvents           []string
}

type analysisReuseState struct {
	previousByPrimaryKey map[string]int
	changedPrimaryFiles  map[string]struct{}
	changedSemanticFiles map[string]struct{}
}

func defaultProjectAnalysisConfig(cwd string) ProjectAnalysisConfig {
	return ProjectAnalysisConfig{
		Enabled:              boolPtr(true),
		MinAgents:            2,
		MaxAgents:            16,
		MaxTotalShards:       64,
		MaxRefinementShards:  12,
		MaxRevisionRounds:    2,
		MaxProviderRetries:   2,
		ProviderRetryDelayMs: 1500,
		MaxFilesPerShard:     250,
		MaxLinesPerShard:     40000,
		ExcludeDirs:          []string{".git", ".svn", ".hg", ".claude", ".kernforge", ".vs", ".gradle", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".tox", ".nox", ".venv", "venv", "__pycache__", "ipch", "node_modules", "vendor", "third_party", "dist", "build", "out", "bin", "obj", "target", "tmp", "temp", "coverage", "CMakeFiles"},
		OutputDir:            filepath.Join(cwd, ".kernforge", "analysis"),
		MaxFileBytes:         512 * 1024,
		Incremental:          boolPtr(true),
	}
}

func configProjectAnalysis(cfg Config, cwd string) ProjectAnalysisConfig {
	out := defaultProjectAnalysisConfig(cwd)
	if cfg.ProjectAnalysis.Enabled != nil {
		value := *cfg.ProjectAnalysis.Enabled
		out.Enabled = &value
	}
	minAgentsConfigured := cfg.ProjectAnalysis.MinAgents > 0
	maxAgentsConfigured := cfg.ProjectAnalysis.MaxAgents > 0
	maxTotalShardsConfigured := cfg.ProjectAnalysis.MaxTotalShards > 0
	maxFilesPerShardConfigured := cfg.ProjectAnalysis.MaxFilesPerShard > 0
	maxLinesPerShardConfigured := cfg.ProjectAnalysis.MaxLinesPerShard > 0
	if cfg.ProjectAnalysis.MinAgents > 0 {
		out.MinAgents = cfg.ProjectAnalysis.MinAgents
	}
	if cfg.ProjectAnalysis.MaxAgents > 0 {
		out.MaxAgents = cfg.ProjectAnalysis.MaxAgents
	}
	if cfg.ProjectAnalysis.MaxTotalShards > 0 {
		out.MaxTotalShards = cfg.ProjectAnalysis.MaxTotalShards
	}
	if cfg.ProjectAnalysis.MaxRefinementShards > 0 {
		out.MaxRefinementShards = cfg.ProjectAnalysis.MaxRefinementShards
	}
	if cfg.ProjectAnalysis.MaxRevisionRounds > 0 {
		out.MaxRevisionRounds = cfg.ProjectAnalysis.MaxRevisionRounds
	}
	if cfg.ProjectAnalysis.MaxProviderRetries != 0 {
		out.MaxProviderRetries = cfg.ProjectAnalysis.MaxProviderRetries
	}
	if cfg.ProjectAnalysis.ProviderRetryDelayMs > 0 {
		out.ProviderRetryDelayMs = cfg.ProjectAnalysis.ProviderRetryDelayMs
	}
	if cfg.ProjectAnalysis.MaxFilesPerShard > 0 {
		out.MaxFilesPerShard = cfg.ProjectAnalysis.MaxFilesPerShard
	}
	if cfg.ProjectAnalysis.MaxLinesPerShard > 0 {
		out.MaxLinesPerShard = cfg.ProjectAnalysis.MaxLinesPerShard
	}
	if len(cfg.ProjectAnalysis.ExcludeDirs) > 0 {
		out.ExcludeDirs = append([]string(nil), cfg.ProjectAnalysis.ExcludeDirs...)
	}
	if len(cfg.ProjectAnalysis.ExcludePaths) > 0 {
		out.ExcludePaths = append([]string(nil), cfg.ProjectAnalysis.ExcludePaths...)
	}
	if strings.TrimSpace(cfg.ProjectAnalysis.OutputDir) != "" {
		out.OutputDir = cfg.ProjectAnalysis.OutputDir
	}
	if cfg.ProjectAnalysis.MaxFileBytes > 0 {
		out.MaxFileBytes = cfg.ProjectAnalysis.MaxFileBytes
	}
	if cfg.ProjectAnalysis.WorkerProfile != nil {
		copy := *cfg.ProjectAnalysis.WorkerProfile
		out.WorkerProfile = &copy
	}
	if cfg.ProjectAnalysis.ReviewerProfile != nil {
		copy := *cfg.ProjectAnalysis.ReviewerProfile
		out.ReviewerProfile = &copy
	}
	if cfg.ProjectAnalysis.Incremental != nil {
		value := *cfg.ProjectAnalysis.Incremental
		out.Incremental = &value
	}
	out.minAgentsConfigured = minAgentsConfigured
	out.maxAgentsConfigured = maxAgentsConfigured
	out.maxTotalShardsConfigured = maxTotalShardsConfigured
	out.maxFilesPerShardConfigured = maxFilesPerShardConfigured
	out.maxLinesPerShardConfigured = maxLinesPerShardConfigured
	if out.MinAgents < 1 {
		out.MinAgents = 1
	}
	if out.MinAgents > 16 {
		out.MinAgents = 16
	}
	if out.MaxAgents < 1 {
		out.MaxAgents = 1
	}
	if out.MaxAgents > 16 {
		out.MaxAgents = 16
	}
	if out.MaxAgents < out.MinAgents {
		if maxAgentsConfigured && !minAgentsConfigured {
			out.MinAgents = out.MaxAgents
		} else {
			out.MaxAgents = out.MinAgents
		}
	}
	if out.MaxRefinementShards < 0 {
		out.MaxRefinementShards = 0
	}
	if !filepath.IsAbs(out.OutputDir) {
		out.OutputDir = filepath.Clean(filepath.Join(cwd, out.OutputDir))
	}
	return out
}

func (a *projectAnalyzer) applyAdaptiveAnalysisShardSizing(snapshot ProjectSnapshot) []string {
	if a == nil {
		return nil
	}
	policy := a.adaptiveAnalysisShardPolicy(snapshot)
	if policy.MaxLinesPerShard <= 0 && policy.MaxFilesPerShard <= 0 && policy.MaxTotalShards <= 0 {
		return nil
	}
	notes := []string{}
	beforeLines := a.analysisCfg.MaxLinesPerShard
	beforeFiles := a.analysisCfg.MaxFilesPerShard
	beforeTotal := a.analysisCfg.MaxTotalShards
	if !a.analysisCfg.maxLinesPerShardConfigured && policy.MaxLinesPerShard > 0 && policy.MaxLinesPerShard < a.analysisCfg.MaxLinesPerShard {
		a.analysisCfg.MaxLinesPerShard = policy.MaxLinesPerShard
	}
	if !a.analysisCfg.maxFilesPerShardConfigured && policy.MaxFilesPerShard > 0 && policy.MaxFilesPerShard < a.analysisCfg.MaxFilesPerShard {
		a.analysisCfg.MaxFilesPerShard = policy.MaxFilesPerShard
	}
	if !a.analysisCfg.maxTotalShardsConfigured && policy.MaxTotalShards > a.analysisCfg.MaxTotalShards {
		a.analysisCfg.MaxTotalShards = policy.MaxTotalShards
	}
	if beforeLines != a.analysisCfg.MaxLinesPerShard || beforeFiles != a.analysisCfg.MaxFilesPerShard || beforeTotal != a.analysisCfg.MaxTotalShards {
		notes = append(notes, fmt.Sprintf(
			"%s; max_lines_per_shard=%d->%d max_files_per_shard=%d->%d max_total_shards=%d->%d",
			policy.Reason,
			beforeLines,
			a.analysisCfg.MaxLinesPerShard,
			beforeFiles,
			a.analysisCfg.MaxFilesPerShard,
			beforeTotal,
			a.analysisCfg.MaxTotalShards,
		))
	}
	return notes
}

type adaptiveAnalysisShardPolicy struct {
	MaxFilesPerShard int
	MaxLinesPerShard int
	MaxTotalShards   int
	Reason           string
}

func (a *projectAnalyzer) adaptiveAnalysisShardPolicy(snapshot ProjectSnapshot) adaptiveAnalysisShardPolicy {
	provider, model := a.analysisWorkerProviderAndModel()
	provider = normalizeProviderName(provider)
	if !analysisProviderShouldUseAdaptiveShardSizing(provider) {
		return adaptiveAnalysisShardPolicy{}
	}
	maxLines := 8000
	maxFiles := 80
	reasons := []string{fmt.Sprintf("adaptive local-model shard sizing provider=%s model=%s", valueOrUnset(provider), valueOrUnset(model))}

	billions := analysisModelParameterBillions(model)
	if billions >= 70 {
		maxLines = analysisMinInt(maxLines, 3500)
		maxFiles = analysisMinInt(maxFiles, 35)
		reasons = append(reasons, "large-model>=70b")
	} else if billions >= 25 {
		maxLines = analysisMinInt(maxLines, 5000)
		maxFiles = analysisMinInt(maxFiles, 50)
		reasons = append(reasons, "large-model>=25b")
	} else if billions >= 13 {
		maxLines = analysisMinInt(maxLines, 6500)
		maxFiles = analysisMinInt(maxFiles, 70)
		reasons = append(reasons, "model>=13b")
	}

	lowerModel := strings.ToLower(strings.TrimSpace(openCodeAPIModelID(model)))
	if strings.Contains(lowerModel, "qwen") && (strings.Contains(lowerModel, "3") || strings.Contains(lowerModel, "qwq")) {
		maxLines = analysisMinInt(maxLines, 5000)
		maxFiles = analysisMinInt(maxFiles, 50)
		reasons = append(reasons, "qwen-reasoning-family")
	}
	if a.cfg.MaxTokens > 0 && a.cfg.MaxTokens <= 2048 {
		maxLines = analysisMinInt(maxLines, 3000)
		maxFiles = analysisMinInt(maxFiles, 35)
		reasons = append(reasons, "low-max-tokens")
	} else if a.cfg.MaxTokens > 0 && a.cfg.MaxTokens <= 4096 {
		maxLines = analysisMinInt(maxLines, 5000)
		maxFiles = analysisMinInt(maxFiles, 55)
		reasons = append(reasons, "moderate-max-tokens")
	}
	timeout := configRequestTimeout(a.cfg)
	if timeout > 0 && timeout <= 5*time.Minute {
		maxLines = analysisMinInt(maxLines, 3000)
		maxFiles = analysisMinInt(maxFiles, 35)
		reasons = append(reasons, "short-request-timeout")
	} else if timeout > 0 && timeout <= 10*time.Minute {
		maxLines = analysisMinInt(maxLines, 4500)
		maxFiles = analysisMinInt(maxFiles, 45)
		reasons = append(reasons, "bounded-request-timeout")
	}
	if maxLines < 1200 {
		maxLines = 1200
	}
	if maxFiles < 8 {
		maxFiles = 8
	}

	estimatedShards := analysisMaxInt(ceilDiv(snapshot.TotalLines, maxLines), ceilDiv(snapshot.TotalFiles, maxFiles))
	maxTotal := 0
	if estimatedShards > 64 {
		maxTotal = analysisMinInt(128, estimatedShards+8)
	}
	return adaptiveAnalysisShardPolicy{
		MaxFilesPerShard: maxFiles,
		MaxLinesPerShard: maxLines,
		MaxTotalShards:   maxTotal,
		Reason:           strings.Join(analysisUniqueStrings(reasons), ", "),
	}
}

func (a *projectAnalyzer) analysisWorkerProviderAndModel() (string, string) {
	if a == nil {
		return "", ""
	}
	if a.analysisCfg.WorkerProfile != nil {
		provider := strings.TrimSpace(a.analysisCfg.WorkerProfile.Provider)
		model := strings.TrimSpace(a.analysisCfg.WorkerProfile.Model)
		if provider != "" || model != "" {
			return firstNonBlankRootCauseString(provider, a.cfg.Provider), firstNonBlankRootCauseString(model, a.cfg.Model)
		}
	}
	return strings.TrimSpace(a.cfg.Provider), strings.TrimSpace(a.cfg.Model)
}

func analysisProviderShouldUseAdaptiveShardSizing(provider string) bool {
	provider = normalizeProviderName(provider)
	return isLocalOpenAICompatibleProvider(provider) || provider == "ollama"
}

func analysisModelParameterBillions(model string) float64 {
	lower := strings.ToLower(strings.TrimSpace(openCodeAPIModelID(model)))
	if lower == "" {
		return 0
	}
	re := regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*b`)
	matches := re.FindAllStringSubmatch(lower, -1)
	best := 0.0
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			continue
		}
		if value > best {
			best = value
		}
	}
	return best
}

func analysisShouldRetryWithSmallerShards(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var providerErr *ProviderAPIError
	if errors.As(err, &providerErr) {
		if providerErr.StatusCode == 429 {
			return false
		}
		if providerErr.StatusCode >= 500 && providerErr.StatusCode <= 504 {
			return true
		}
	}
	text := strings.ToLower(err.Error())
	shardPressureHints := []string{
		"deadline exceeded",
		"timeout",
		"timed out",
		"client.timeout exceeded",
		"gateway timeout",
		"temporarily unavailable",
		"server error",
		"bad gateway",
		"service unavailable",
		"overloaded",
		"server_overloaded",
		"server_error",
		"timeout_error",
		"unexpected eof",
		"connection reset",
		"model returned an empty response",
	}
	for _, hint := range shardPressureHints {
		if strings.Contains(text, hint) {
			return true
		}
	}
	return false
}

func analysisProviderFailureRecoveryConfig(cfg ProjectAnalysisConfig, snapshot ProjectSnapshot) (ProjectAnalysisConfig, string, bool) {
	out := cfg
	beforeLines := out.MaxLinesPerShard
	beforeFiles := out.MaxFilesPerShard
	beforeTotal := out.MaxTotalShards
	if beforeLines <= 0 {
		beforeLines = 40000
		out.MaxLinesPerShard = beforeLines
	}
	if beforeFiles <= 0 {
		beforeFiles = 250
		out.MaxFilesPerShard = beforeFiles
	}
	nextLines := shrinkAnalysisShardLimit(beforeLines, 1200)
	nextFiles := shrinkAnalysisShardLimit(beforeFiles, 12)
	if nextLines < out.MaxLinesPerShard {
		out.MaxLinesPerShard = nextLines
	}
	if nextFiles < out.MaxFilesPerShard {
		out.MaxFilesPerShard = nextFiles
	}
	estimatedByLines := ceilDiv(snapshot.TotalLines, analysisMaxInt(out.MaxLinesPerShard, 1))
	estimatedByFiles := ceilDiv(snapshot.TotalFiles, analysisMaxInt(out.MaxFilesPerShard, 1))
	desiredTotal := analysisMaxInt(analysisMaxInt(estimatedByLines, estimatedByFiles)+8, out.MaxTotalShards)
	if desiredTotal > 256 {
		desiredTotal = 256
	}
	if desiredTotal > out.MaxTotalShards {
		out.MaxTotalShards = desiredTotal
	}
	changed := out.MaxLinesPerShard != cfg.MaxLinesPerShard || out.MaxFilesPerShard != cfg.MaxFilesPerShard || out.MaxTotalShards != cfg.MaxTotalShards
	if !changed {
		return cfg, "", false
	}
	note := fmt.Sprintf(
		"max_lines_per_shard=%d->%d max_files_per_shard=%d->%d max_total_shards=%d->%d",
		cfg.MaxLinesPerShard,
		out.MaxLinesPerShard,
		cfg.MaxFilesPerShard,
		out.MaxFilesPerShard,
		beforeTotal,
		out.MaxTotalShards,
	)
	return out, note, true
}

func shrinkAnalysisShardLimit(value int, floor int) int {
	if value <= 0 {
		return floor
	}
	if floor <= 0 {
		floor = 1
	}
	next := value / 2
	if value > floor*8 {
		next = value / 4
	}
	if next < floor {
		next = floor
	}
	if next >= value && value > floor {
		next = value - 1
	}
	return next
}

func rootCauseProjectAnalysisConfig(cfg ProjectAnalysisConfig) ProjectAnalysisConfig {
	if !cfg.minAgentsConfigured {
		cfg.MinAgents = 1
	}
	if !cfg.maxAgentsConfigured || cfg.MaxAgents > 8 {
		cfg.MaxAgents = 8
	}
	if cfg.MinAgents < 1 {
		cfg.MinAgents = 1
	}
	if cfg.MaxAgents < 1 {
		cfg.MaxAgents = 1
	}
	if cfg.MinAgents > cfg.MaxAgents {
		cfg.MinAgents = cfg.MaxAgents
	}
	cfg.MaxTotalShards = 8
	cfg.MaxRefinementShards = 8
	cfg.MaxRevisionRounds = analysisMaxInt(cfg.MaxRevisionRounds, 2)
	return cfg
}

func newProjectAnalyzer(cfg Config, client ProviderClient, ws Workspace, onStatus func(string), onDebug func(string)) *projectAnalyzer {
	return &projectAnalyzer{
		cfg:         cfg,
		analysisCfg: configProjectAnalysis(cfg, ws.BaseRoot),
		client:      client,
		modelRoutes: defaultModelRouteScheduler(),
		workspace:   ws,
		onStatus:    onStatus,
		onDebug:     onDebug,
	}
}

type AnalysisDirectoryCandidate struct {
	Path   string
	Reason string
}

func findAnalysisDirectoryCandidates(root string, cfg ProjectAnalysisConfig) ([]AnalysisDirectoryCandidate, error) {
	excludedNames := analysisExcludedDirNameSet(cfg)
	excludedAbs := analysisExcludedAbsolutePathSet(root, cfg)
	seen := map[string]struct{}{}
	candidates := []AnalysisDirectoryCandidate{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		cleanPath := strings.ToLower(filepath.Clean(path))
		if _, ok := excludedAbs[cleanPath]; ok {
			return filepath.SkipDir
		}
		name := strings.TrimSpace(d.Name())
		if _, ok := excludedNames[strings.ToLower(name)]; ok {
			return filepath.SkipDir
		}
		relPath := filepath.ToSlash(relOrAbs(root, path))
		reason := analysisDirectoryCandidateReason(name)
		if reason != "" {
			key := strings.ToLower(relPath)
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				candidates = append(candidates, AnalysisDirectoryCandidate{
					Path:   relPath,
					Reason: reason,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(candidates, func(i int, j int) bool {
		return candidates[i].Path < candidates[j].Path
	})
	return candidates, nil
}

func analysisDirectoryCandidateReason(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(trimmed, ".") {
		return "hidden"
	}
	switch lower {
	case "external", "externals", "dependency", "dependencies", "deps":
		return "external_like"
	}
	return ""
}

func analysisExcludedDirNameSet(cfg ProjectAnalysisConfig) map[string]struct{} {
	excluded := map[string]struct{}{}
	for _, item := range cfg.ExcludeDirs {
		trimmed := strings.ToLower(strings.TrimSpace(item))
		if trimmed == "" {
			continue
		}
		excluded[trimmed] = struct{}{}
	}
	return excluded
}

func analysisExcludedAbsolutePathSet(root string, cfg ProjectAnalysisConfig) map[string]struct{} {
	excludedAbs := map[string]struct{}{}
	if strings.TrimSpace(cfg.OutputDir) != "" {
		excludedAbs[strings.ToLower(filepath.Clean(cfg.OutputDir))] = struct{}{}
	}
	for _, item := range cfg.ExcludePaths {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		target := trimmed
		if !filepath.IsAbs(target) {
			target = filepath.Join(root, target)
		}
		excludedAbs[strings.ToLower(filepath.Clean(target))] = struct{}{}
	}
	return excludedAbs
}

func (a *projectAnalyzer) status(text string) {
	if a.onStatus != nil {
		a.onStatus(text)
	}
}

func (a *projectAnalyzer) debug(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	a.debugMu.Lock()
	a.debugEvents = append(a.debugEvents, trimmed)
	a.debugMu.Unlock()
	if a.onDebug != nil {
		a.onDebug(trimmed)
	}
}

func (a *projectAnalyzer) planRootCauseInvestigation(ctx context.Context, snapshot ProjectSnapshot, goal string) RootCauseInvestigation {
	fallback := fallbackRootCauseInvestigation(goal)
	if a.client == nil {
		return fallback
	}
	resp, err := a.completeAnalysisRequestWithRetry(ctx, a.client, "root-cause-plan", "", a.cfg.Model, ChatRequest{
		Model:       a.cfg.Model,
		System:      rootCausePlannerSystemPrompt(),
		Messages:    []Message{{Role: "user", Text: buildRootCausePlannerPrompt(snapshot, goal)}},
		MaxTokens:   analysisStructuredMaxTokens(a.cfg.Model, a.cfg.MaxTokens),
		Temperature: a.cfg.Temperature,
		WorkingDir:  a.workspace.Root,
		JSONMode:    true,
	})
	if err != nil {
		a.debug(fmt.Sprintf("root-cause planner soft-failed: %v", err))
		return fallback
	}
	plan, ok := parseRootCauseInvestigationPayload(resp.Message.Text)
	if !ok {
		a.debug("root-cause planner returned non-JSON output; using deterministic fallback")
		return fallback
	}
	normalizeRootCauseInvestigation(&plan)
	if strings.TrimSpace(plan.Symptom.Symptom) == "" {
		plan.Symptom = fallback.Symptom
	}
	if len(plan.Hypotheses) == 0 {
		plan.Hypotheses = fallback.Hypotheses
	}
	return plan
}

func rootCausePlannerSystemPrompt() string {
	return strings.TrimSpace(`
You normalize a user-reported bug symptom and plan source-code hypotheses for a root-cause investigation.
Your entire response must be a single JSON object. The first byte must be "{" and the last byte must be "}".
Return strict JSON:
{
  "root_cause": {
    "symptom": {
      "symptom": "string",
      "expected_behavior": "string",
      "observed_behavior": "string",
      "frequency": "string",
      "trigger_keywords": ["string"],
      "affected_surface": ["string"],
      "must_explain": ["string"],
      "reproduction_inputs": ["string"]
    },
    "hypotheses": [
      {
        "id": "H1",
        "title": "string",
        "candidate_mechanism": "string",
        "target_signals": ["string"],
        "target_files": ["string"],
        "must_prove": ["string"],
        "must_disprove": ["string"],
        "reproduction_inputs": ["string"]
      }
    ]
  }
}
Rules:
- Normalize the symptom into expected behavior, observed behavior, frequency, and must_explain.
- Create 3-8 falsifiable hypotheses. Each hypothesis must include must_prove and must_disprove.
- Prefer hypotheses that route workers to concrete source areas: intent parsing, state transitions, persistence, lifecycle, finalization, write paths, guard conditions, permission paths, or generated artifact paths.
- target_files may use exact scanned paths when obvious, otherwise use signal words.
`)
}

func buildRootCausePlannerPrompt(snapshot ProjectSnapshot, goal string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Goal:\n%s\n\n", strings.TrimSpace(goal))
	fmt.Fprintf(&b, "Workspace: %s\n", snapshot.Root)
	fmt.Fprintf(&b, "Files: %d\nLines: %d\n\n", snapshot.TotalFiles, snapshot.TotalLines)
	if len(snapshot.ProjectTypes) > 0 {
		fmt.Fprintf(&b, "Inferred project types: %s\n\n", strings.Join(snapshot.ProjectTypes, ", "))
	}
	if patterns := renderRootCausePatternMatchesForPrompt(snapshot.RootCause.PatternMatches, 8); strings.TrimSpace(patterns) != "" {
		b.WriteString(patterns)
		b.WriteString("\n\n")
	}
	b.WriteString("Top candidate files:\n")
	for _, file := range topImportantFiles(snapshot, 24) {
		fmt.Fprintf(&b, "- %s (score=%d reasons=%s)\n", file.Path, file.ImportanceScore, strings.Join(limitStrings(file.ImportanceReasons, 4), ", "))
	}
	if len(snapshot.ManifestFiles) > 0 {
		b.WriteString("\nManifest files:\n")
		for _, item := range limitStrings(snapshot.ManifestFiles, 12) {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	b.WriteString("\nReturn the JSON plan only.")
	return strings.TrimSpace(b.String())
}

func parseRootCauseInvestigationPayload(raw string) (RootCauseInvestigation, bool) {
	type envelope struct {
		RootCause RootCauseInvestigation `json:"root_cause"`
	}
	for _, candidate := range analysisJSONCandidates(raw) {
		wrapped := envelope{}
		if err := json.Unmarshal([]byte(candidate), &wrapped); err == nil {
			if rootCauseInvestigationHasContent(wrapped.RootCause) {
				normalizeRootCauseInvestigation(&wrapped.RootCause)
				return wrapped.RootCause, true
			}
		}
		plan := RootCauseInvestigation{}
		if err := json.Unmarshal([]byte(candidate), &plan); err == nil {
			if rootCauseInvestigationHasContent(plan) {
				normalizeRootCauseInvestigation(&plan)
				return plan, true
			}
		}
	}
	return RootCauseInvestigation{}, false
}

func rootCauseInvestigationHasContent(plan RootCauseInvestigation) bool {
	return strings.TrimSpace(plan.Symptom.Symptom) != "" ||
		strings.TrimSpace(plan.Symptom.ExpectedBehavior) != "" ||
		strings.TrimSpace(plan.Symptom.ObservedBehavior) != "" ||
		len(plan.Hypotheses) > 0 ||
		len(plan.CodeMatches) > 0 ||
		len(plan.DeepVerifications) > 0 ||
		len(plan.JoinedCandidates) > 0 ||
		len(plan.CandidateClusters) > 0 ||
		len(plan.EvidenceRequests) > 0 ||
		len(plan.PatternMatches) > 0 ||
		rootCauseAuditTrailHasContent(plan.RegressionMemory)
}

func normalizeRootCauseInvestigation(plan *RootCauseInvestigation) {
	plan.Symptom.Symptom = strings.TrimSpace(plan.Symptom.Symptom)
	plan.Symptom.ExpectedBehavior = strings.TrimSpace(plan.Symptom.ExpectedBehavior)
	plan.Symptom.ObservedBehavior = strings.TrimSpace(plan.Symptom.ObservedBehavior)
	plan.Symptom.Frequency = strings.TrimSpace(plan.Symptom.Frequency)
	plan.Symptom.TriggerKeywords = analysisUniqueStrings(plan.Symptom.TriggerKeywords)
	plan.Symptom.AffectedSurface = analysisUniqueStrings(plan.Symptom.AffectedSurface)
	plan.Symptom.MustExplain = analysisUniqueStrings(plan.Symptom.MustExplain)
	plan.Symptom.ReproductionInputs = analysisUniqueStrings(plan.Symptom.ReproductionInputs)
	for i := range plan.Hypotheses {
		h := &plan.Hypotheses[i]
		h.ID = strings.TrimSpace(h.ID)
		if h.ID == "" {
			h.ID = fmt.Sprintf("H%d", i+1)
		}
		h.Title = strings.TrimSpace(h.Title)
		h.CandidateMechanism = strings.TrimSpace(h.CandidateMechanism)
		h.TargetSignals = analysisUniqueStrings(h.TargetSignals)
		h.TargetFiles = analysisUniqueStrings(h.TargetFiles)
		h.MustProve = analysisUniqueStrings(h.MustProve)
		h.MustDisprove = analysisUniqueStrings(h.MustDisprove)
		h.ReproductionInputs = analysisUniqueStrings(h.ReproductionInputs)
	}
	plan.CodeMatches = normalizeRootCauseCodeMatches(plan.CodeMatches)
	plan.DeepVerifications = normalizeRootCauseDeepVerifications(plan.DeepVerifications)
	plan.JoinedCandidates = normalizeRootCauseJoinedCandidates(plan.JoinedCandidates)
	plan.CandidateClusters = normalizeRootCauseCandidateClusters(plan.CandidateClusters)
	plan.EvidenceRequests = normalizeRootCauseEvidenceRequests(plan.EvidenceRequests)
	plan.PatternMatches = normalizeRootCausePatternMatches(plan.PatternMatches)
}

func normalizeRootCauseCodeMatches(matches []RootCauseCodeMatch) []RootCauseCodeMatch {
	out := []RootCauseCodeMatch{}
	for _, match := range matches {
		match.Query = strings.TrimSpace(match.Query)
		match.File = strings.TrimSpace(match.File)
		match.Reason = strings.TrimSpace(match.Reason)
		match.MatchedSignals = analysisUniqueStrings(match.MatchedSignals)
		if match.Score < 0 {
			match.Score = 0
		}
		if match.File == "" && match.Query == "" {
			continue
		}
		out = append(out, match)
	}
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].File < out[j].File
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func normalizeRootCauseDeepVerifications(items []RootCauseDeepVerification) []RootCauseDeepVerification {
	out := []RootCauseDeepVerification{}
	for _, item := range items {
		item.CandidateTitle = strings.TrimSpace(item.CandidateTitle)
		item.ShardID = strings.TrimSpace(item.ShardID)
		item.ShardName = strings.TrimSpace(item.ShardName)
		item.Status = normalizeRootCauseDeepVerificationStatus(item.Status)
		item.Summary = strings.TrimSpace(item.Summary)
		item.CausalChain = normalizeJoinedRootCauseCausalChain(item.CausalChain)
		item.EvidenceFiles = analysisUniqueStrings(item.EvidenceFiles)
		item.InstrumentationSteps = analysisUniqueStrings(item.InstrumentationSteps)
		item.DisconfirmingEvidence = analysisUniqueStrings(item.DisconfirmingEvidence)
		item.CannotBeRootCauseIf = analysisUniqueStrings(item.CannotBeRootCauseIf)
		item.RequiredRuntimeObservation = analysisUniqueStrings(item.RequiredRuntimeObservation)
		item.Probes = normalizeRootCauseProbes(item.Probes)
		item.PatternIDs = analysisUniqueStrings(item.PatternIDs)
		if strings.TrimSpace(item.Confidence) != "" {
			item.Confidence = normalizeRootCauseConfidence(item.Confidence, 0)
		}
		if len(item.Probes) == 0 {
			item.Probes = deriveRootCauseVerificationProbes(item)
		}
		item.ConfidenceBreakdown = normalizeRootCauseConfidenceBreakdown(item.ConfidenceBreakdown, rootCauseDeepVerificationScore(item))
		item.Raw = strings.TrimSpace(item.Raw)
		if item.CandidateTitle == "" && item.Summary == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func normalizeRootCauseProbes(probes []RootCauseProbe) []RootCauseProbe {
	out := []RootCauseProbe{}
	for _, probe := range probes {
		probe.Title = strings.TrimSpace(probe.Title)
		probe.Kind = normalizeRootCauseProbeKind(probe.Kind)
		probe.Target = strings.TrimSpace(probe.Target)
		probe.Command = strings.TrimSpace(probe.Command)
		probe.ExpectedSignal = strings.TrimSpace(probe.ExpectedSignal)
		probe.DisprovesWhen = strings.TrimSpace(probe.DisprovesWhen)
		if probe.Title == "" && probe.Target == "" && probe.ExpectedSignal == "" {
			continue
		}
		if probe.Kind == "" {
			probe.Kind = "trace"
		}
		if probe.Title == "" {
			probe.Title = firstNonBlankRootCauseString(probe.ExpectedSignal, probe.Target, "Root-cause probe")
		}
		out = append(out, probe)
	}
	return dedupeRootCauseProbes(out)
}

func normalizeRootCauseProbeKind(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "log", "logging":
		return "log"
	case "assert", "assertion":
		return "assert"
	case "test", "unit_test", "integration_test":
		return "test"
	case "repro", "reproduction":
		return "repro"
	case "db_config_dump", "db", "config", "dump":
		return "db_config_dump"
	case "trace", "instrument", "instrumentation":
		return "trace"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func dedupeRootCauseProbes(probes []RootCauseProbe) []RootCauseProbe {
	seen := map[string]struct{}{}
	out := []RootCauseProbe{}
	for _, probe := range probes {
		key := strings.ToLower(strings.TrimSpace(probe.Kind + "\x00" + probe.Target + "\x00" + probe.ExpectedSignal + "\x00" + probe.Command))
		if key == "\x00\x00\x00" {
			key = strings.ToLower(strings.TrimSpace(probe.Title))
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, probe)
	}
	return out
}

func normalizeRootCauseConfidenceBreakdown(breakdown RootCauseConfidenceBreakdown, fallbackScore int) RootCauseConfidenceBreakdown {
	breakdown.CausalCompleteness = clampRootCauseScore(breakdown.CausalCompleteness)
	breakdown.EvidenceStrength = clampRootCauseScore(breakdown.EvidenceStrength)
	breakdown.RuntimeObservability = clampRootCauseScore(breakdown.RuntimeObservability)
	breakdown.AlternativeExplanations = clampRootCauseScore(breakdown.AlternativeExplanations)
	breakdown.DisconfirmationStrength = clampRootCauseScore(breakdown.DisconfirmationStrength)
	breakdown.Score = clampRootCauseScore(breakdown.Score)
	breakdown.Reasons = analysisUniqueStrings(breakdown.Reasons)
	if fallbackScore < 0 {
		fallbackScore = 0
	}
	if fallbackScore > 100 {
		fallbackScore = 100
	}
	if breakdown.Score == 0 {
		total := 0
		count := 0
		for _, value := range []int{
			breakdown.CausalCompleteness,
			breakdown.EvidenceStrength,
			breakdown.RuntimeObservability,
			breakdown.AlternativeExplanations,
			breakdown.DisconfirmationStrength,
		} {
			if value > 0 {
				total += value
				count++
			}
		}
		if count > 0 {
			breakdown.Score = total / count
		}
	}
	if breakdown.Score == 0 && fallbackScore > 0 {
		breakdown.Score = fallbackScore
	}
	if breakdown.CausalCompleteness == 0 &&
		breakdown.EvidenceStrength == 0 &&
		breakdown.RuntimeObservability == 0 &&
		breakdown.AlternativeExplanations == 0 &&
		breakdown.DisconfirmationStrength == 0 &&
		breakdown.Score > 0 {
		breakdown.CausalCompleteness = breakdown.Score
		breakdown.EvidenceStrength = analysisMaxInt(25, breakdown.Score-10)
		breakdown.RuntimeObservability = analysisMaxInt(20, breakdown.Score-15)
		breakdown.AlternativeExplanations = analysisMaxInt(20, breakdown.Score-20)
		breakdown.DisconfirmationStrength = analysisMaxInt(20, breakdown.Score-20)
	}
	return breakdown
}

func clampRootCauseScore(score int) int {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func rootCauseDeepVerificationScore(item RootCauseDeepVerification) int {
	score := 20 + rootCauseCausalChainStageCount(item.CausalChain)*12
	switch item.Status {
	case "supported":
		score += 25
	case "weak":
		score += 10
	case "disconfirmed":
		score = 15
	case "unknown":
		score += 0
	}
	score += analysisMinInt(len(item.EvidenceFiles)*4, 12)
	score += analysisMinInt((len(item.InstrumentationSteps)+len(item.Probes))*4, 16)
	if len(item.DisconfirmingEvidence) > 0 || len(item.CannotBeRootCauseIf) > 0 {
		score += 6
	}
	return clampRootCauseScore(score)
}

func buildRootCauseConfidenceBreakdown(candidate RootCauseCandidate, review ReviewDecision) RootCauseConfidenceBreakdown {
	stageCount := rootCauseCausalChainStageCount(candidate.CausalChain)
	score := rootCauseConfidenceScore(candidate, review)
	reasons := []string{}
	if stageCount > 0 {
		reasons = append(reasons, fmt.Sprintf("%d causal stage(s) are evidenced.", stageCount))
	}
	if len(candidate.EvidenceFiles) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d evidence file(s) support the candidate.", len(candidate.EvidenceFiles)))
	}
	if len(candidate.RequiredRuntimeObservation) > 0 || len(candidate.VerificationSteps) > 0 || len(candidate.Probes) > 0 {
		reasons = append(reasons, "Runtime observation or test probes are available.")
	}
	if len(candidate.NeedsCrossShardEvidence) > 0 {
		reasons = append(reasons, "Cross-shard evidence is still needed.")
	}
	if len(candidate.DisconfirmingEvidence) > 0 || len(candidate.CannotBeRootCauseIf) > 0 {
		reasons = append(reasons, "Falsification conditions are documented.")
	}
	return normalizeRootCauseConfidenceBreakdown(RootCauseConfidenceBreakdown{
		CausalCompleteness:      clampRootCauseScore(stageCount * 20),
		EvidenceStrength:        clampRootCauseScore(20 + len(candidate.EvidenceFiles)*12 + len(review.SymptomCausality)*8),
		RuntimeObservability:    clampRootCauseScore(20 + (len(candidate.RequiredRuntimeObservation)+len(candidate.VerificationSteps)+len(candidate.Probes))*12),
		AlternativeExplanations: clampRootCauseScore(55 - len(candidate.NeedsCrossShardEvidence)*10),
		DisconfirmationStrength: clampRootCauseScore(25 + (len(candidate.DisconfirmingEvidence)+len(candidate.CannotBeRootCauseIf))*15),
		Score:                   score,
		Reasons:                 reasons,
	}, score)
}

func deriveRootCauseCandidateProbes(candidate RootCauseCandidate, shard AnalysisShard) []RootCauseProbe {
	target := firstString(candidate.EvidenceFiles)
	if target == "" {
		target = firstString(shard.PrimaryFiles)
	}
	trigger := firstNonBlankRootCauseString(candidate.CausalChain.Trigger, firstString(candidate.TriggerValues), candidate.Title, "reported trigger")
	invalidState := firstNonBlankRootCauseString(candidate.CausalChain.InvalidState, firstString(candidate.OutOfRangeCases), "out-of-range state")
	disproves := firstNonBlankAnalysisString(firstString(candidate.CannotBeRootCauseIf), "The reported symptom reproduces without this state transition.")
	probes := []RootCauseProbe{}
	for _, observation := range limitStrings(candidate.RequiredRuntimeObservation, 2) {
		probes = append(probes, RootCauseProbe{
			Title:          "Trace required runtime observation",
			Kind:           "trace",
			Target:         target,
			ExpectedSignal: observation,
			DisprovesWhen:  disproves,
		})
	}
	for _, step := range limitStrings(candidate.VerificationSteps, 2) {
		probes = append(probes, RootCauseProbe{
			Title:          "Execute focused verification step",
			Kind:           rootCauseProbeKindFromText(step),
			Target:         target,
			Command:        step,
			ExpectedSignal: invalidState,
			DisprovesWhen:  disproves,
		})
	}
	if len(probes) == 0 {
		probes = append(probes, RootCauseProbe{
			Title:          "Reproduce and trace candidate chain",
			Kind:           "repro",
			Target:         target,
			Command:        "Drive the reported trigger while tracing: " + trigger,
			ExpectedSignal: invalidState,
			DisprovesWhen:  disproves,
		})
	}
	if target != "" && invalidState != "" {
		probes = append(probes, RootCauseProbe{
			Title:          "Add guard/assertion probe",
			Kind:           "assert",
			Target:         target,
			ExpectedSignal: invalidState,
			DisprovesWhen:  disproves,
		})
	}
	return normalizeRootCauseProbes(probes)
}

func deriveRootCauseVerificationProbes(item RootCauseDeepVerification) []RootCauseProbe {
	target := firstString(item.EvidenceFiles)
	disproves := firstNonBlankAnalysisString(firstString(item.CannotBeRootCauseIf), "The symptom reproduces while the candidate path is not taken.")
	probes := []RootCauseProbe{}
	for _, step := range limitStrings(item.InstrumentationSteps, 3) {
		probes = append(probes, RootCauseProbe{
			Title:          "Deep verification instrumentation",
			Kind:           rootCauseProbeKindFromText(step),
			Target:         target,
			Command:        step,
			ExpectedSignal: firstNonBlankRootCauseString(firstString(item.RequiredRuntimeObservation), item.CausalChain.InvalidState, item.Summary),
			DisprovesWhen:  disproves,
		})
	}
	for _, observation := range limitStrings(item.RequiredRuntimeObservation, 2) {
		probes = append(probes, RootCauseProbe{
			Title:          "Observe deep-verification signal",
			Kind:           "trace",
			Target:         target,
			ExpectedSignal: observation,
			DisprovesWhen:  disproves,
		})
	}
	return normalizeRootCauseProbes(probes)
}

func rootCauseProbeKindFromText(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "test"):
		return "test"
	case strings.Contains(lower, "assert") || strings.Contains(lower, "invariant"):
		return "assert"
	case strings.Contains(lower, "log") || strings.Contains(lower, "dump"):
		return "log"
	case strings.Contains(lower, "db") || strings.Contains(lower, "config"):
		return "db_config_dump"
	case strings.Contains(lower, "repro"):
		return "repro"
	default:
		return "trace"
	}
}

func enhanceRootCauseReportProbesWithRepoCommands(snapshot ProjectSnapshot, reports []WorkerReport) []WorkerReport {
	if normalizeProjectAnalysisMode(snapshot.AnalysisMode) != "root-cause" || len(reports) == 0 {
		return reports
	}
	out := append([]WorkerReport(nil), reports...)
	for reportIndex := range out {
		for candidateIndex := range out[reportIndex].RootCauseCandidates {
			candidate := &out[reportIndex].RootCauseCandidates[candidateIndex]
			candidate.Probes = enhanceRootCauseProbesWithRepoCommands(snapshot, candidate.Probes, candidate.EvidenceFiles)
		}
	}
	return out
}

func enhanceRootCauseJoinedProbesWithRepoCommands(snapshot ProjectSnapshot, candidates []RootCauseJoinedCandidate) []RootCauseJoinedCandidate {
	out := append([]RootCauseJoinedCandidate(nil), candidates...)
	for index := range out {
		out[index].Probes = enhanceRootCauseProbesWithRepoCommands(snapshot, out[index].Probes, out[index].EvidenceFiles)
	}
	return out
}

func enhanceRootCauseDeepVerificationProbesWithRepoCommands(snapshot ProjectSnapshot, items []RootCauseDeepVerification) []RootCauseDeepVerification {
	out := append([]RootCauseDeepVerification(nil), items...)
	for index := range out {
		out[index].Probes = enhanceRootCauseProbesWithRepoCommands(snapshot, out[index].Probes, out[index].EvidenceFiles)
	}
	return out
}

func enhanceRootCauseProbesWithRepoCommands(snapshot ProjectSnapshot, probes []RootCauseProbe, evidenceFiles []string) []RootCauseProbe {
	out := normalizeRootCauseProbes(probes)
	for index := range out {
		if rootCauseProbeHasConcreteCommand(out[index].Command) {
			continue
		}
		out[index].Command = rootCauseProbeCommandForSnapshot(snapshot, out[index], evidenceFiles)
	}
	return normalizeRootCauseProbes(out)
}

func rootCauseProbeHasConcreteCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	lower := strings.ToLower(command)
	for _, generic := range []string{"drive the reported symptom", "trace the joined causal chain", "reported trigger"} {
		if strings.Contains(lower, generic) {
			return false
		}
	}
	return strings.Contains(command, " ") || strings.Contains(command, ".") || strings.Contains(command, "/") || strings.Contains(command, "\\")
}

func rootCauseProbeCommandForSnapshot(snapshot ProjectSnapshot, probe RootCauseProbe, evidenceFiles []string) string {
	target := firstNonBlankRootCauseString(probe.Target, firstString(evidenceFiles))
	symptom := strings.ToLower(rootCauseReportedSymptomText(snapshot) + " " + probe.Title + " " + probe.ExpectedSignal + " " + probe.Command)
	if strings.Contains(symptom, "sc stop") || strings.Contains(symptom, "service") || strings.Contains(symptom, "서비스") {
		serviceName := rootCauseInferServiceName(snapshot, evidenceFiles)
		return fmt.Sprintf("sc.exe stop %s && sc.exe query %s", serviceName, serviceName)
	}
	if len(snapshot.UnrealProjects) > 0 {
		project := snapshot.UnrealProjects[0].Path
		if strings.TrimSpace(project) == "" {
			project = "*.uproject"
		}
		return fmt.Sprintf("UnrealEditor-Cmd.exe \"%s\" -ExecCmds=\"Automation RunTests Project; Quit\" -unattended -nop4", project)
	}
	if rootCauseSnapshotHasExtension(snapshot, ".go") || strings.TrimSpace(snapshot.ModulePath) != "" {
		return "go test ./... -run TestRootCauseProbe -count=1"
	}
	if rootCauseSnapshotHasAnyExtension(snapshot, []string{".cs"}) {
		return "dotnet test --filter RootCauseProbe"
	}
	if rootCauseSnapshotHasAnyExtension(snapshot, []string{".cpp", ".cc", ".cxx", ".c", ".h", ".hpp"}) {
		if rootCauseSnapshotHasBuildFile(snapshot, "CMakeLists.txt") {
			return "ctest --output-on-failure -R RootCauseProbe"
		}
		return "cmake --build . --config Debug"
	}
	if rootCauseSnapshotHasAnyExtension(snapshot, []string{".ps1"}) {
		return "powershell -ExecutionPolicy Bypass -File .\\root-cause-probe.ps1"
	}
	if target != "" {
		return "Add the probe at " + target + " and run the nearest project test for that module."
	}
	return "Run the smallest repro that triggers the reported symptom and capture the expected_signal."
}

func rootCauseInferServiceName(snapshot ProjectSnapshot, evidenceFiles []string) string {
	for _, path := range evidenceFiles {
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if strings.Contains(strings.ToLower(base), "service") && base != "" {
			return base
		}
	}
	for _, project := range snapshot.SolutionProjects {
		if strings.Contains(strings.ToLower(project.Name), "service") {
			return project.Name
		}
	}
	return "<service-name>"
}

func rootCauseSnapshotHasExtension(snapshot ProjectSnapshot, ext string) bool {
	return rootCauseSnapshotHasAnyExtension(snapshot, []string{ext})
}

func rootCauseSnapshotHasAnyExtension(snapshot ProjectSnapshot, exts []string) bool {
	set := map[string]struct{}{}
	for _, ext := range exts {
		set[strings.ToLower(ext)] = struct{}{}
	}
	for _, file := range rootCauseSnapshotFiles(snapshot) {
		if _, ok := set[strings.ToLower(file.Extension)]; ok {
			return true
		}
		if _, ok := set[strings.ToLower(filepath.Ext(file.Path))]; ok {
			return true
		}
	}
	return false
}

func rootCauseSnapshotHasBuildFile(snapshot ProjectSnapshot, base string) bool {
	for _, file := range rootCauseSnapshotFiles(snapshot) {
		if strings.EqualFold(filepath.Base(file.Path), base) {
			return true
		}
	}
	return false
}

func normalizeRootCauseDeepVerificationStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "supported", "support", "confirmed":
		return "supported"
	case "weak", "partial", "needs_more_evidence":
		return "weak"
	case "disconfirmed", "rejected", "false":
		return "disconfirmed"
	default:
		return "unknown"
	}
}

func fallbackRootCauseInvestigation(goal string) RootCauseInvestigation {
	symptomText := strings.TrimSpace(goal)
	if strings.HasPrefix(strings.ToLower(symptomText), "find the most likely root cause") {
		if idx := strings.Index(symptomText, ":"); idx >= 0 {
			symptomText = strings.TrimSpace(symptomText[idx+1:])
		}
		if idx := strings.Index(symptomText, "\n"); idx >= 0 {
			symptomText = strings.TrimSpace(symptomText[:idx])
		}
	}
	lower := strings.ToLower(symptomText)
	keywords := extractParallelReadOnlyWorkerTerms(symptomText)
	if len(keywords) > 10 {
		keywords = keywords[:10]
	}
	affected := []string{"state transition", "validation", "finalization gate", "persistence/config read"}
	if containsAny(lower, "document", "문서", "report", "markdown", "file", "파일") {
		affected = []string{"intent classification", "tool availability", "document write path", "final answer gate", "artifact path"}
	}
	return RootCauseInvestigation{
		Symptom: RootCauseSymptomProfile{
			Symptom:          symptomText,
			ExpectedBehavior: "The system should follow the user's requested behavior.",
			ObservedBehavior: symptomText,
			Frequency:        "unspecified or intermittent",
			TriggerKeywords:  keywords,
			AffectedSurface:  affected,
			MustExplain: []string{
				"why the observed behavior can happen",
				"which unexpected input or state value triggers the path",
				"what evidence confirms or rejects the candidate",
			},
			ReproductionInputs: []string{symptomText},
		},
		Hypotheses: []RootCauseHypothesis{
			{
				ID:                 "H1",
				Title:              "Input or intent classification routes to the wrong execution path",
				CandidateMechanism: "A user input phrase or decoded request value falls outside the expected category and selects a non-mutating or incomplete path.",
				TargetSignals:      []string{"intent", "classify", "mode", "read-only", "write", "document", "request"},
				MustProve:          []string{"show the branch that selects the wrong path", "show how the selected path can produce the symptom"},
				MustDisprove:       []string{"show the input is classified correctly", "show a later guard forces the expected behavior anyway"},
				ReproductionInputs: []string{symptomText},
			},
			{
				ID:                 "H2",
				Title:              "State transition or finalization gate accepts incomplete work",
				CandidateMechanism: "A loop, lifecycle state, or final answer gate reaches success without verifying the requested output exists.",
				TargetSignals:      []string{"final", "done", "complete", "loop", "gate", "artifact", "exists", "verify"},
				MustProve:          []string{"show completion can occur without the requested output", "show no later existence check rejects it"},
				MustDisprove:       []string{"show output existence is mandatory before completion"},
				ReproductionInputs: []string{symptomText},
			},
			{
				ID:                 "H3",
				Title:              "Write path, permission, hook, or path resolution prevents the artifact",
				CandidateMechanism: "The write attempt fails, is canceled, writes to a different root, or is blocked by guard logic.",
				TargetSignals:      []string{"write_file", "apply_patch", "permission", "hook", "path", "worktree", "artifact"},
				MustProve:          []string{"show a write attempt can fail or target a different path", "show the failure is surfaced as the observed symptom"},
				MustDisprove:       []string{"show no write attempt is made", "show write failures are always reported distinctly"},
				ReproductionInputs: []string{symptomText},
			},
		},
	}
}

func applyRootCausePlanToImportance(snapshot *ProjectSnapshot, plan RootCauseInvestigation) {
	if snapshot == nil || len(plan.Hypotheses) == 0 {
		return
	}
	signals := []string{}
	targets := map[string]struct{}{}
	for _, hypothesis := range plan.Hypotheses {
		signals = append(signals, hypothesis.TargetSignals...)
		for _, file := range hypothesis.TargetFiles {
			key := strings.ToLower(filepath.ToSlash(strings.TrimSpace(file)))
			if key != "" {
				targets[key] = struct{}{}
			}
		}
	}
	signals = analysisUniqueStrings(signals)
	for i := range snapshot.Files {
		file := snapshot.Files[i]
		lowerPath := strings.ToLower(filepath.ToSlash(file.Path))
		boost := 0
		if _, ok := targets[lowerPath]; ok {
			boost += 28
		}
		for _, signal := range signals {
			signal = strings.ToLower(strings.TrimSpace(signal))
			if signal != "" && strings.Contains(lowerPath, signal) {
				boost += 8
			}
		}
		if boost <= 0 {
			continue
		}
		file.ImportanceScore += boost
		file.ImportanceReasons = analysisUniqueStrings(append(file.ImportanceReasons, "root_cause_hypothesis"))
		snapshot.Files[i] = file
		snapshot.FilesByPath[file.Path] = file
		if dirFiles := snapshot.FilesByDirectory[file.Directory]; len(dirFiles) > 0 {
			for j := range dirFiles {
				if dirFiles[j].Path == file.Path {
					dirFiles[j] = file
				}
			}
			snapshot.FilesByDirectory[file.Directory] = dirFiles
		}
	}
}

func deriveRootCauseCodeMatches(snapshot ProjectSnapshot, plan RootCauseInvestigation, goal string, limit int) []RootCauseCodeMatch {
	if limit <= 0 {
		limit = 16
	}
	signals := []string{}
	signals = append(signals, plan.Symptom.TriggerKeywords...)
	signals = append(signals, plan.Symptom.AffectedSurface...)
	signals = append(signals, extractParallelReadOnlyWorkerTerms(goal)...)
	targetFiles := map[string]string{}
	for _, hypothesis := range plan.Hypotheses {
		signals = append(signals, hypothesis.TargetSignals...)
		signals = append(signals, extractParallelReadOnlyWorkerTerms(hypothesis.Title)...)
		signals = append(signals, extractParallelReadOnlyWorkerTerms(hypothesis.CandidateMechanism)...)
		for _, target := range hypothesis.TargetFiles {
			trimmed := strings.ToLower(filepath.ToSlash(strings.TrimSpace(target)))
			if trimmed != "" {
				targetFiles[trimmed] = hypothesis.ID
			}
		}
	}
	signals = rootCauseRelevantSignals(signals)
	matches := []RootCauseCodeMatch{}
	for _, file := range snapshot.Files {
		lowerPath := strings.ToLower(filepath.ToSlash(file.Path))
		score := 0
		reasons := []string{}
		matched := []string{}
		if id, ok := targetFiles[lowerPath]; ok {
			score += 60
			reasons = append(reasons, "hypothesis_target:"+id)
			matched = append(matched, file.Path)
		}
		for target, id := range targetFiles {
			if target != "" && (strings.Contains(lowerPath, target) || strings.Contains(target, lowerPath)) {
				score += 30
				reasons = append(reasons, "target_overlap:"+id)
				matched = append(matched, target)
			}
		}
		for _, signal := range signals {
			lowerSignal := strings.ToLower(strings.TrimSpace(signal))
			if lowerSignal == "" {
				continue
			}
			if strings.Contains(lowerPath, lowerSignal) {
				score += 14
				matched = append(matched, signal)
			}
			for _, reason := range file.ImportanceReasons {
				if strings.Contains(strings.ToLower(reason), lowerSignal) {
					score += 4
					matched = append(matched, signal)
					break
				}
			}
		}
		if score == 0 {
			continue
		}
		if file.IsEntrypoint {
			score += 5
		}
		if file.IsManifest {
			score += 3
		}
		matches = append(matches, RootCauseCodeMatch{
			Query:          strings.Join(limitStrings(signals, 8), ", "),
			File:           file.Path,
			Reason:         strings.Join(analysisUniqueStrings(reasons), ", "),
			MatchedSignals: analysisUniqueStrings(matched),
			Score:          score,
		})
	}
	matches = normalizeRootCauseCodeMatches(matches)
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func rootCauseRelevantSignals(signals []string) []string {
	out := []string{}
	for _, signal := range signals {
		signal = strings.ToLower(strings.TrimSpace(signal))
		signal = strings.Trim(signal, " \t\r\n.,;:!?()[]{}<>\"'`~")
		if len([]rune(signal)) < 3 || rootCausePromptIsGenericToken(signal) {
			continue
		}
		out = append(out, signal)
	}
	return analysisUniqueStrings(out)
}

func applyRootCauseCodeMatchesToImportance(snapshot *ProjectSnapshot, matches []RootCauseCodeMatch) {
	if snapshot == nil || len(matches) == 0 {
		return
	}
	matchByPath := map[string]RootCauseCodeMatch{}
	for _, match := range matches {
		if strings.TrimSpace(match.File) != "" {
			matchByPath[match.File] = match
		}
	}
	for i := range snapshot.Files {
		file := snapshot.Files[i]
		match, ok := matchByPath[file.Path]
		if !ok {
			continue
		}
		file.ImportanceScore += analysisMinInt(24, analysisMaxInt(4, match.Score/4))
		file.ImportanceReasons = analysisUniqueStrings(append(file.ImportanceReasons, "root_cause_code_match"))
		snapshot.Files[i] = file
		snapshot.FilesByPath[file.Path] = file
		if dirFiles := snapshot.FilesByDirectory[file.Directory]; len(dirFiles) > 0 {
			for j := range dirFiles {
				if dirFiles[j].Path == file.Path {
					dirFiles[j] = file
				}
			}
			snapshot.FilesByDirectory[file.Directory] = dirFiles
		}
	}
}

func augmentRootCauseCodeMatchesWithSemanticIndex(plan RootCauseInvestigation, index SemanticIndexV2, goal string, limit int) []RootCauseCodeMatch {
	matches := append([]RootCauseCodeMatch(nil), plan.CodeMatches...)
	if len(index.Symbols) == 0 {
		return normalizeRootCauseCodeMatches(matches)
	}
	signals := []string{}
	signals = append(signals, plan.Symptom.TriggerKeywords...)
	signals = append(signals, plan.Symptom.AffectedSurface...)
	signals = append(signals, extractParallelReadOnlyWorkerTerms(goal)...)
	for _, hypothesis := range plan.Hypotheses {
		signals = append(signals, hypothesis.TargetSignals...)
		signals = append(signals, extractParallelReadOnlyWorkerTerms(hypothesis.Title)...)
		signals = append(signals, extractParallelReadOnlyWorkerTerms(hypothesis.CandidateMechanism)...)
	}
	signals = rootCauseRelevantSignals(signals)
	byFile := map[string]int{}
	for i, match := range matches {
		if strings.TrimSpace(match.File) != "" {
			byFile[match.File] = i
		}
	}
	for _, symbol := range index.Symbols {
		if strings.TrimSpace(symbol.File) == "" {
			continue
		}
		corpus := strings.ToLower(strings.Join([]string{
			symbol.ID,
			symbol.Name,
			symbol.CanonicalName,
			symbol.Kind,
			symbol.Signature,
			strings.Join(symbol.Tags, " "),
		}, " "))
		matched := []string{}
		for _, signal := range signals {
			if signal != "" && strings.Contains(corpus, strings.ToLower(signal)) {
				matched = append(matched, signal)
			}
		}
		if len(matched) == 0 {
			continue
		}
		symbolName := firstNonBlankAnalysisString(symbol.CanonicalName, firstNonBlankAnalysisString(symbol.Name, symbol.ID))
		reason := "symbol_match:" + symbolName
		if index, ok := byFile[symbol.File]; ok {
			matches[index].Score += 18 + len(matched)*4
			matches[index].Reason = strings.Trim(strings.Join(analysisUniqueStrings([]string{matches[index].Reason, reason}), ", "), ", ")
			matches[index].MatchedSignals = analysisUniqueStrings(append(matches[index].MatchedSignals, append([]string{"symbol:" + firstNonBlankAnalysisString(symbol.Name, symbol.ID)}, matched...)...))
			continue
		}
		byFile[symbol.File] = len(matches)
		matches = append(matches, RootCauseCodeMatch{
			Query:          strings.Join(limitStrings(signals, 8), ", "),
			File:           symbol.File,
			Reason:         reason,
			MatchedSignals: analysisUniqueStrings(append([]string{"symbol:" + firstNonBlankAnalysisString(symbol.Name, symbol.ID)}, matched...)),
			Score:          18 + len(matched)*4,
		})
	}
	matches = normalizeRootCauseCodeMatches(matches)
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func (a *projectAnalyzer) Run(ctx context.Context, goal string, mode string) (ProjectAnalysisRun, error) {
	run := ProjectAnalysisRun{}
	a.debugMu.Lock()
	a.debugEvents = nil
	a.debugMu.Unlock()
	a.cachedUnrealGraph = UnrealSemanticGraph{}
	a.cachedSemanticIndexV2 = SemanticIndexV2{}
	run.Summary.RunID = time.Now().Format("20060102-150405")
	run.Summary.Goal = strings.TrimSpace(goal)
	run.Summary.Mode = effectiveProjectAnalysisMode(mode, goal)
	run.Summary.Status = "running"
	run.Summary.StartedAt = time.Now()

	if strings.TrimSpace(goal) == "" {
		return run, fmt.Errorf("analysis goal is empty")
	}
	if a.client == nil {
		return run, fmt.Errorf("no model provider is configured")
	}
	if a.analysisCfg.Enabled != nil && !*a.analysisCfg.Enabled {
		return run, fmt.Errorf("project analysis is disabled")
	}
	if err := a.initializeClients(); err != nil {
		return run, err
	}
	run.ConductorProfile = strings.TrimSpace(a.cfg.Provider) + " / " + strings.TrimSpace(a.cfg.Model)
	run.WorkerProfile = describeAnalysisProfile(a.analysisCfg.WorkerProfile, a.workerOrDefaultClient(), a.workerModel())
	run.ReviewerProfile = describeAnalysisProfile(a.analysisCfg.ReviewerProfile, a.reviewerOrDefaultClient(), a.reviewerModel())
	a.debug(fmt.Sprintf("analysis run started: goal=%q conductor=%s worker=%s reviewer=%s", run.Summary.Goal, run.ConductorProfile, run.WorkerProfile, run.ReviewerProfile))

	a.status("Scanning workspace...")
	snapshot, err := a.scanProject()
	if err != nil {
		return run, err
	}
	for _, note := range a.applyAdaptiveAnalysisShardSizing(snapshot) {
		a.status(note)
		a.debug(note)
	}
	snapshot.AnalysisMode = run.Summary.Mode
	snapshot.AnalysisLenses = refineAnalysisLensesForSnapshot(snapshot, chooseAnalysisLenses(goal, run.Summary.Mode))
	a.scoreFileImportance(&snapshot, snapshot.AnalysisLenses)
	if normalizeProjectAnalysisMode(run.Summary.Mode) == "root-cause" {
		snapshot.ProjectTypes = inferRootCauseProjectTypes(snapshot, goal)
		patternPack, patternDiagnostics := loadRootCausePatternPackWithDiagnostics(snapshot.Root, a.rootCausePatternPacks)
		for _, diagnostic := range patternDiagnostics {
			a.debug(diagnostic)
		}
		snapshot.RootCause.PatternMatches = matchRootCausePatternsFromPack(snapshot, goal, patternPack, 12)
		applyRootCausePatternMatchesToImportance(&snapshot, snapshot.RootCause.PatternMatches)
		a.debug(fmt.Sprintf("root-cause pattern priors prepared: project_types=%s packs=%d matches=%d", strings.Join(snapshot.ProjectTypes, ","), len(rootCausePatternPackInputPaths(snapshot.Root, a.rootCausePatternPacks)), len(snapshot.RootCause.PatternMatches)))
		a.status("Normalizing symptom and planning root-cause hypotheses...")
		rootCausePlan := a.planRootCauseInvestigation(ctx, snapshot, goal)
		rootCausePlan.PatternMatches = snapshot.RootCause.PatternMatches
		snapshot.RootCause = rootCausePlan
		run.RootCause = rootCausePlan
		applyRootCausePlanToImportance(&snapshot, rootCausePlan)
		rootCausePlan.CodeMatches = deriveRootCauseCodeMatches(snapshot, rootCausePlan, goal, 18)
		rootCausePlan.CodeMatches = normalizeRootCauseCodeMatches(append(rootCausePlan.CodeMatches, deriveRootCausePatternCodeMatches(snapshot, rootCausePlan.PatternMatches, 18)...))
		applyRootCauseCodeMatchesToImportance(&snapshot, rootCausePlan.CodeMatches)
		snapshot.RootCause = rootCausePlan
		run.RootCause = rootCausePlan
		a.debug(fmt.Sprintf("root-cause plan prepared: hypotheses=%d symptom=%q", len(rootCausePlan.Hypotheses), rootCausePlan.Symptom.Symptom))
	}
	snapshot.ProjectEdges = buildProjectEdges(snapshot)
	a.cachedUnrealGraph = buildUnrealSemanticGraph(snapshot, goal, run.Summary.RunID)
	a.cachedSemanticIndexV2 = buildSemanticIndexV2(snapshot, goal, run.Summary.RunID, a.cachedUnrealGraph)
	if normalizeProjectAnalysisMode(run.Summary.Mode) == "root-cause" {
		snapshot.RootCause.CodeMatches = augmentRootCauseCodeMatchesWithSemanticIndex(snapshot.RootCause, a.cachedSemanticIndexV2, goal, 24)
		snapshot.RootCause.CodeMatches = normalizeRootCauseCodeMatches(append(snapshot.RootCause.CodeMatches, deriveRootCausePatternCodeMatches(snapshot, snapshot.RootCause.PatternMatches, 24)...))
		applyRootCauseCodeMatchesToImportance(&snapshot, snapshot.RootCause.CodeMatches)
		run.RootCause = snapshot.RootCause
	}
	snapshot.ArchitectureFacts = buildArchitectureFactPack(snapshot, a.cachedSemanticIndexV2, a.cachedUnrealGraph, goal)
	run.Snapshot = snapshot

	agentCount := a.estimateAgentCount(snapshot)
	targetShards := a.estimateShardCount(snapshot, agentCount)
	shards := a.planShards(snapshot, targetShards)
	scope := deriveAnalysisScope(goal, a.explicitScope, snapshot)
	_, scopedShards := deriveScopedAnalysisShardsForScope(scope, shards)
	if len(scopedShards) > 0 {
		shards = scopedShards
	}
	if len(shards) == 0 {
		return run, fmt.Errorf("no analyzable files found")
	}
	if len(scope.DirectoryPrefixes) > 0 {
		a.status(fmt.Sprintf("Applying scoped analysis to %s.", strings.Join(scope.DirectoryPrefixes, ", ")))
		a.debug(fmt.Sprintf("analysis scope matched directories: %s", strings.Join(scope.DirectoryPrefixes, ", ")))
	}
	if baseline, ok := a.loadBaselineMapForMode(run.Summary.Mode, scope); ok {
		snapshot.BaselineMap = baseline
		run.Snapshot = snapshot
		a.status(fmt.Sprintf("Loaded map baseline %s for %s analysis.", baseline.RunID, run.Summary.Mode))
		a.debug(fmt.Sprintf("loaded map baseline: run_id=%s artifact=%s docs_manifest=%s", baseline.RunID, baseline.ArtifactPath, baseline.DocsManifestPath))
	}
	if agentCount > len(shards) {
		agentCount = len(shards)
	}
	if agentCount < 1 {
		agentCount = 1
	}
	agentCount = a.effectiveShardConcurrency(agentCount, len(shards), run.Summary.Mode)
	run.Summary.AgentCount = agentCount
	run.Summary.TotalShards = len(shards)
	run.Shards = shards

	previousRun, _ := a.loadPreviousRun(goal, run.Summary.Mode)
	if previousRun != nil {
		a.status("Loaded previous analysis for incremental reuse.")
		a.debug(fmt.Sprintf("loaded previous analysis run: shards=%d approved=%d", len(previousRun.Shards), previousRun.Summary.ApprovedShards))
		if normalizeProjectAnalysisMode(run.Summary.Mode) == "root-cause" && rootCauseAuditTrailHasContent(previousRun.RootCause.AuditTrail) {
			snapshot.RootCause.RegressionMemory = previousRun.RootCause.AuditTrail
			run.RootCause.RegressionMemory = previousRun.RootCause.AuditTrail
			run.Snapshot = snapshot
			a.debug(fmt.Sprintf("loaded root-cause regression memory: decisions=%d deep=%d joined=%d", len(previousRun.RootCause.AuditTrail.CandidateDecisions), len(previousRun.RootCause.AuditTrail.DeepVerifications), len(previousRun.RootCause.AuditTrail.JoinedCandidates)))
		}
	}

	a.status(fmt.Sprintf("Running %d sub-agent(s)...", len(shards)))
	reuseState := a.buildReuseState(previousRun, shards)
	reports, reviews, err := a.executeShards(ctx, snapshot, shards, goal, previousRun, reuseState, agentCount)
	if err != nil {
		return run, err
	}
	auditShards := append([]AnalysisShard(nil), shards...)
	auditReports := append([]WorkerReport(nil), reports...)
	auditReviews := append([]ReviewDecision(nil), reviews...)
	reports = filterRootCauseReportsByReview(snapshot, reports, reviews)
	reports = applyRootCauseDeterministicQualityGate(snapshot, shards, reports, reviews)
	refinementShards, replacedShardIDs := a.planRefinementShards(snapshot, shards, reports, reviews)
	if len(refinementShards) > 0 {
		run.Summary.RefinedShards = len(refinementShards)
		a.status(fmt.Sprintf("Refining %d high-value sub-agent shard(s)...", len(refinementShards)))
		a.debug(fmt.Sprintf("stage-2 refinement planned: shards=%d parents=%d", len(refinementShards), len(replacedShardIDs)))
		refinedReports, refinedReviews, err := a.executeShards(ctx, snapshot, refinementShards, goal, previousRun, analysisReuseState{}, agentCount)
		if err != nil {
			return run, err
		}
		auditShards, auditReports, auditReviews = mergeRefinedShardResults(auditShards, auditReports, auditReviews, refinementShards, refinedReports, refinedReviews, replacedShardIDs)
		refinedReports = filterRootCauseReportsByReview(snapshot, refinedReports, refinedReviews)
		refinedReports = applyRootCauseDeterministicQualityGate(snapshot, refinementShards, refinedReports, refinedReviews)
		shards, reports, reviews = mergeRefinedShardResults(shards, reports, reviews, refinementShards, refinedReports, refinedReviews, replacedShardIDs)
	}
	if normalizeProjectAnalysisMode(run.Summary.Mode) == "root-cause" {
		memoryChanges := buildRootCauseRegressionMemoryChangeState(previousRun, shards)
		reports = applyRootCauseRegressionMemoryToReportsWithChanges(reports, snapshot.RootCause.RegressionMemory, memoryChanges)
		reports = enhanceRootCauseReportProbesWithRepoCommands(snapshot, reports)
		for round := 1; round <= rootCauseEvidenceRequestRounds; round++ {
			snapshot.RootCause.EvidenceRequests = mergeRootCauseEvidenceRequestState(snapshot.RootCause.EvidenceRequests, rootCauseEvidenceRequestsFromReviews(reviews))
			run.RootCause = snapshot.RootCause
			evidenceShards := a.planRootCauseEvidenceRequestShards(snapshot, shards, reviews, round, rootCauseMaxEvidenceRequestShardsRound)
			if len(evidenceShards) == 0 {
				break
			}
			snapshot.RootCause.EvidenceRequests = markRootCauseEvidenceRequestsRouted(snapshot.RootCause.EvidenceRequests, evidenceShards)
			run.Summary.EvidenceShards += len(evidenceShards)
			a.status(fmt.Sprintf("Routing %d root-cause evidence request shard(s)...", len(evidenceShards)))
			a.debug(fmt.Sprintf("root-cause evidence request round %d planned: shards=%d", round, len(evidenceShards)))
			evidenceReports, evidenceReviews, err := a.executeShards(ctx, snapshot, evidenceShards, goal, previousRun, analysisReuseState{}, agentCount)
			if err != nil {
				return run, err
			}
			auditShards = append(auditShards, evidenceShards...)
			auditReports = append(auditReports, evidenceReports...)
			auditReviews = append(auditReviews, evidenceReviews...)
			evidenceReports = filterRootCauseReportsByReview(snapshot, evidenceReports, evidenceReviews)
			evidenceReports = applyRootCauseDeterministicQualityGate(snapshot, evidenceShards, evidenceReports, evidenceReviews)
			evidenceReports = applyRootCauseRegressionMemoryToReportsWithChanges(evidenceReports, snapshot.RootCause.RegressionMemory, memoryChanges)
			evidenceReports = enhanceRootCauseReportProbesWithRepoCommands(snapshot, evidenceReports)
			snapshot.RootCause.EvidenceRequests = markRootCauseEvidenceRequestsFulfilled(snapshot.RootCause.EvidenceRequests, evidenceShards, evidenceReports)
			shards = append(shards, evidenceShards...)
			reports = append(reports, evidenceReports...)
			reviews = append(reviews, evidenceReviews...)
		}
	}
	run.Shards = shards
	run.Reports = reports
	run.Reviews = reviews
	run.Summary.TotalShards = len(shards)
	summarizeReviewDecisions(&run.Summary, run.Reviews)
	if normalizeProjectAnalysisMode(run.Summary.Mode) == "root-cause" {
		a.status("Deep-verifying reviewer-approved root-cause candidates...")
		deepVerifications := a.deepVerifyRootCauseCandidates(ctx, snapshot, shards, reports, reviews, goal)
		reports = applyRootCauseDeepVerificationsToReports(snapshot, shards, reports, deepVerifications)
		reports = applyRootCauseDeterministicQualityGate(snapshot, shards, reports, reviews)
		reports = enhanceRootCauseReportProbesWithRepoCommands(snapshot, reports)
		run.Reports = reports
		run.RootCause = snapshot.RootCause
		run.RootCause.DeepVerifications = deepVerifications
		snapshot.RootCause = run.RootCause
		run.Snapshot = snapshot
		a.debug(fmt.Sprintf("root-cause deep verification completed: targets=%d", len(deepVerifications)))
		a.status("Joining root-cause evidence across shards...")
		joined := a.joinRootCauseCandidates(ctx, snapshot, shards, reports, reviews, goal)
		joined = applyRootCauseJoinedQualityGate(snapshot, joined)
		joined = enhanceRootCauseJoinedProbesWithRepoCommands(snapshot, joined)
		run.RootCause = snapshot.RootCause
		run.RootCause.JoinedCandidates = joined
		run.RootCause.CandidateClusters = buildRootCauseCandidateClusters(shards, reports, reviews, joined)
		run.RootCause.AuditTrail = buildRootCauseAuditTrail(snapshot, auditShards, auditReports, auditReviews, reports, deepVerifications, joined)
		snapshot.RootCause = run.RootCause
		run.Snapshot = snapshot
		a.debug(fmt.Sprintf("root-cause join completed: joined_candidates=%d", len(joined)))
	}
	approvalRatio := 0.0
	if run.Summary.TotalShards > 0 {
		approvalRatio = float64(run.Summary.ApprovedShards) / float64(run.Summary.TotalShards)
	}
	a.debug(fmt.Sprintf("review summary: approved=%d total=%d ratio=%.2f provider_failures=%d quality_issues=%d refined=%d", run.Summary.ApprovedShards, run.Summary.TotalShards, approvalRatio, run.Summary.ReviewProviderFailures, run.Summary.ReviewQualityIssues, run.Summary.RefinedShards))

	a.status("Writing final document...")
	document, err := a.synthesizeFinalDocument(ctx, snapshot, shards, reports, goal)
	if err != nil {
		return run, err
	}
	if run.Summary.ApprovedShards == 0 {
		draftNotice := "# Draft Analysis\n\nNo shard report was approved by the reviewer. The generated analysis should be treated as low confidence and rerun after fixing worker/reviewer issues."
		if details := renderAnalysisReviewIssueDetailsForReviews(run.Summary.ReviewProviderFailures, run.Summary.ReviewQualityIssues, run.Reviews); strings.TrimSpace(details) != "" {
			draftNotice += "\n\n" + details
		}
		document = draftNotice + "\n\n" + document
		run.Summary.Status = "draft"
		a.debug("analysis downgraded to draft because no shard was approved")
	} else if run.Summary.ReviewFailures > 0 {
		run.Summary.Status = "completed_with_review_failures"
		if banner := renderAnalysisReviewIssueBannerForReviews(run.Summary, run.Reviews); strings.TrimSpace(banner) != "" {
			document = banner + "\n\n" + document
		}
		a.debug(fmt.Sprintf("analysis completed with review issues: provider_failures=%d quality_issues=%d", run.Summary.ReviewProviderFailures, run.Summary.ReviewQualityIssues))
	}
	run.FinalDocument = document
	run.ShardDocuments = buildShardDocuments(run.Snapshot, run.Shards, run.Reports, goal)
	run.KnowledgePack = buildKnowledgePack(run.Snapshot, run.Shards, run.Reports, goal, run.Summary.RunID)
	run.UnrealGraph = a.cachedUnrealGraph
	run.SemanticIndex = buildSemanticIndex(run.Snapshot, goal, run.Summary.RunID, run.UnrealGraph)
	run.SemanticIndexV2 = a.cachedSemanticIndexV2
	run.VectorCorpus = buildVectorCorpus(run)
	run.VectorIngestion = buildVectorIngestionManifest(run.VectorCorpus)
	a.debug("final document synthesis completed")

	if run.Summary.Status == "running" {
		run.Summary.Status = "completed"
	}
	run.Summary.CompletedAt = time.Now()
	outputPath, err := a.persistRun(run)
	if err != nil {
		return run, err
	}
	run.Summary.OutputPath = outputPath
	a.debug(fmt.Sprintf("analysis artifacts written: %s", outputPath))
	a.debugMu.Lock()
	run.DebugEvents = append([]string(nil), a.debugEvents...)
	a.debugMu.Unlock()
	return run, nil
}

func (a *projectAnalyzer) scanProject() (ProjectSnapshot, error) {
	snapshot := ProjectSnapshot{
		Root:               a.workspace.Root,
		ModulePath:         detectGoModulePath(a.workspace.Root),
		GeneratedAt:        time.Now(),
		ImportGraph:        map[string][]string{},
		ReverseImportGraph: map[string][]string{},
		FilesByPath:        map[string]ScannedFile{},
		FilesByDirectory:   map[string][]ScannedFile{},
	}
	excluded := analysisExcludedDirNameSet(a.analysisCfg)
	excludedAbs := analysisExcludedAbsolutePathSet(a.workspace.Root, a.analysisCfg)

	err := filepath.WalkDir(a.workspace.Root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == a.workspace.Root {
			return nil
		}
		if analysisShouldSkipPath(a.workspace.Root, path, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if _, ok := excludedAbs[strings.ToLower(filepath.Clean(path))]; ok {
				return filepath.SkipDir
			}
			if _, ok := excluded[strings.ToLower(d.Name())]; ok {
				return filepath.SkipDir
			}
			snapshot.Directories = append(snapshot.Directories, filepath.ToSlash(relOrAbs(a.workspace.Root, path)))
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > a.analysisCfg.MaxFileBytes {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if !isText(data) {
			return nil
		}
		relPath := filepath.ToSlash(relOrAbs(a.workspace.Root, path))
		dir := filepath.ToSlash(filepath.Dir(relPath))
		if dir == "." {
			dir = ""
		}
		file := ScannedFile{
			Path:         relPath,
			Directory:    dir,
			Extension:    strings.ToLower(filepath.Ext(relPath)),
			LineCount:    countLines(data),
			IsManifest:   isManifestFile(relPath),
			IsEntrypoint: isEntrypointFile(relPath, data),
			RawImports:   discoverImports(strings.ToLower(filepath.Ext(relPath)), string(data)),
		}
		snapshot.Files = append(snapshot.Files, file)
		snapshot.FilesByPath[file.Path] = file
		snapshot.FilesByDirectory[file.Directory] = append(snapshot.FilesByDirectory[file.Directory], file)
		snapshot.TotalFiles++
		snapshot.TotalLines += file.LineCount
		if file.IsManifest {
			snapshot.ManifestFiles = append(snapshot.ManifestFiles, file.Path)
		}
		if file.IsEntrypoint {
			snapshot.EntrypointFiles = append(snapshot.EntrypointFiles, file.Path)
		}
		return nil
	})
	if err != nil {
		return snapshot, err
	}

	sort.Slice(snapshot.Files, func(i int, j int) bool {
		return snapshot.Files[i].Path < snapshot.Files[j].Path
	})
	a.resolveImports(&snapshot)
	a.enrichSolutionMetadata(&snapshot)
	a.enrichUnrealMetadata(&snapshot)
	enrichBuildAlignment(&snapshot)
	sort.Strings(snapshot.Directories)
	sort.Strings(snapshot.ManifestFiles)
	sort.Strings(snapshot.EntrypointFiles)
	sort.Strings(snapshot.StartupProjects)
	return snapshot, nil
}

func analysisShouldSkipPath(root string, path string, d os.DirEntry) bool {
	relPath := filepath.ToSlash(relOrAbs(root, path))
	lowerRel := strings.ToLower(strings.TrimSpace(relPath))
	name := strings.ToLower(strings.TrimSpace(d.Name()))
	if lowerRel == "" || name == "" {
		return false
	}
	if name == ".vs" || name == "ipch" {
		return true
	}
	if strings.HasPrefix(name, "cmake-build-") {
		return true
	}
	if strings.HasSuffix(name, ".tlog") || strings.HasSuffix(lowerRel, ".tlog") {
		return true
	}
	if strings.HasSuffix(lowerRel, ".lastbuildstate") ||
		strings.HasSuffix(lowerRel, ".tsbuildinfo") ||
		strings.HasSuffix(lowerRel, ".pyc") ||
		strings.HasSuffix(lowerRel, ".pyo") ||
		strings.HasSuffix(lowerRel, ".coverage") {
		return true
	}
	segments := strings.Split(lowerRel, "/")
	hasPlatformSegment := false
	hasConfigSegment := false
	for _, segment := range segments {
		switch segment {
		case "x64", "x86", "win32", "arm64":
			hasPlatformSegment = true
		case "debug", "release", "live":
			hasConfigSegment = true
		}
		if hasPlatformSegment && hasConfigSegment {
			return true
		}
	}
	if analysisHasAdjacentSegments(segments, "target", "debug", "release", "deps") {
		return true
	}
	if analysisHasAdjacentSegments(segments, "build", "generated") {
		return true
	}
	return false
}

func analysisHasAdjacentSegments(segments []string, anchors ...string) bool {
	if len(segments) == 0 || len(anchors) < 2 {
		return false
	}
	allowed := map[string]struct{}{}
	for _, item := range anchors {
		allowed[strings.ToLower(strings.TrimSpace(item))] = struct{}{}
	}
	matched := 0
	for _, segment := range segments {
		if _, ok := allowed[segment]; ok {
			matched++
			if matched >= 2 {
				return true
			}
			continue
		}
		matched = 0
	}
	return false
}

func (a *projectAnalyzer) estimateAgentCount(snapshot ProjectSnapshot) int {
	count := analysisMaxInt(ceilDiv(snapshot.TotalFiles, 250), ceilDiv(snapshot.TotalLines, 40000))
	count = analysisMaxInt(count, ceilDiv(len(snapshot.Directories), 12))
	if len(snapshot.ManifestFiles) >= 3 {
		count++
	}
	if count < a.analysisCfg.MinAgents {
		count = a.analysisCfg.MinAgents
	}
	if count > a.analysisCfg.MaxAgents {
		count = a.analysisCfg.MaxAgents
	}
	if count < 1 {
		count = 1
	}
	return count
}

func (a *projectAnalyzer) estimateShardCount(snapshot ProjectSnapshot, concurrentAgents int) int {
	count := concurrentAgents
	count = analysisMaxInt(count, ceilDiv(snapshot.TotalFiles, 120))
	count = analysisMaxInt(count, ceilDiv(snapshot.TotalLines, 15000))
	if a.analysisCfg.MaxFilesPerShard > 0 {
		count = analysisMaxInt(count, ceilDiv(snapshot.TotalFiles, a.analysisCfg.MaxFilesPerShard))
	}
	if a.analysisCfg.MaxLinesPerShard > 0 {
		count = analysisMaxInt(count, ceilDiv(snapshot.TotalLines, a.analysisCfg.MaxLinesPerShard))
	}
	count = analysisMaxInt(count, ceilDiv(len(snapshot.Directories), 2))
	if count < a.analysisCfg.MinAgents {
		count = a.analysisCfg.MinAgents
	}
	if count < 1 {
		count = 1
	}
	if a.analysisCfg.MaxTotalShards > 0 && count > a.analysisCfg.MaxTotalShards {
		count = a.analysisCfg.MaxTotalShards
	}
	return count
}

func (a *projectAnalyzer) effectiveShardConcurrency(concurrency int, shardCount int, mode string) int {
	if concurrency < 1 {
		concurrency = 1
	}
	if a.analysisCfg.MaxAgents > 0 && concurrency > a.analysisCfg.MaxAgents {
		concurrency = a.analysisCfg.MaxAgents
	}
	if shardCount > 0 && concurrency > shardCount {
		concurrency = shardCount
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 1 {
		if routeLimit, shared := a.sharedAnalysisRouteConcurrencyLimit(); shared {
			if routeLimit > 0 && concurrency > routeLimit {
				return routeLimit
			}
		}
	}
	return concurrency
}

func (a *projectAnalyzer) sharedAnalysisRouteConcurrencyLimit() (int, bool) {
	workerRoute := analysisRouteForProfile(a.analysisCfg.WorkerProfile, a.cfg.Provider, a.cfg.Model, a.cfg.BaseURL)
	reviewerProfile := a.analysisCfg.ReviewerProfile
	if reviewerProfile == nil && a.analysisCfg.WorkerProfile != nil {
		reviewerProfile = a.analysisCfg.WorkerProfile
	}
	reviewerRoute := analysisRouteForProfile(reviewerProfile, a.cfg.Provider, a.cfg.Model, a.cfg.BaseURL)
	if strings.TrimSpace(workerRoute.Key) == "" || workerRoute.Key != reviewerRoute.Key {
		return 0, false
	}
	limit := modelRoutePolicyFromConfig(a.cfg).LimitFor(workerRoute)
	if limit < 1 {
		return 0, true
	}
	return limit, true
}

func analysisRouteForProfile(profile *Profile, fallbackProvider string, fallbackModel string, fallbackBaseURL string) ModelRoute {
	fallbackProvider = normalizeProviderName(fallbackProvider)
	provider := fallbackProvider
	model := strings.TrimSpace(fallbackModel)
	baseURL := strings.TrimSpace(fallbackBaseURL)
	if profile != nil {
		profileProvider := normalizeProviderName(profile.Provider)
		if profileProvider != "" {
			provider = profileProvider
		}
		if strings.TrimSpace(profile.Model) != "" {
			model = strings.TrimSpace(profile.Model)
		}
		profileBaseURL := strings.TrimSpace(profile.BaseURL)
		if profileBaseURL != "" {
			baseURL = profileBaseURL
		} else if provider != fallbackProvider {
			baseURL = ""
		}
	}
	baseURL = normalizeProviderBaseURL(provider, baseURL)
	return ModelRoute{
		Key:      modelRouteKeyFromParts(provider, model, baseURL, ""),
		Label:    modelRouteLabel(provider, model, baseURL, ""),
		Provider: provider,
		Model:    strings.TrimSpace(model),
		BaseURL:  normalizeModelRouteBaseURL(provider, baseURL),
	}
}

func analysisRouteKey(profile *Profile, fallbackProvider string, fallbackModel string, fallbackBaseURL string) string {
	route := analysisRouteForProfile(profile, fallbackProvider, fallbackModel, fallbackBaseURL)
	if strings.TrimSpace(route.Provider) == "" && strings.TrimSpace(route.Model) == "" {
		return ""
	}
	return route.Key
}

func chooseAnalysisLenses(goal string, mode string) []AnalysisLens {
	lower := strings.ToLower(strings.TrimSpace(goal))
	lenses := []AnalysisLens{
		{
			Type:            "architecture",
			PrioritySignals: []string{"entrypoint", "module", "manager", "service", "worker", "dispatcher"},
			OutputFocus:     []string{"module map", "ownership", "execution chain"},
		},
	}
	lenses = append(lenses, analysisLensesForMode(mode)...)
	if containsAny(lower, "runtime", "execution", "flow", "startup", "init", "boot", "entry") {
		lenses = append(lenses, AnalysisLens{
			Type:            "runtime_flow",
			PrioritySignals: []string{"loadlibrary", "createprocess", "service", "main", "winmain", "dllmain"},
			OutputFocus:     []string{"startup chain", "runtime graph", "operational chain"},
		})
	}
	if containsAny(lower, "ipc", "pipe", "named pipe", "rpc", "message", "command", "dispatch", "handler") {
		lenses = append(lenses, AnalysisLens{
			Type:              "ipc",
			PrioritySignals:   []string{"pipe", "iocp", "message", "command", "handler", "dispatch", "server", "client"},
			SuppressedSignals: []string{"external dependency catalog"},
			OutputFocus:       []string{"ipc endpoints", "message structures", "dispatch path"},
		})
	}
	if containsAny(lower, "security", "trust", "boundary", "validation", "kernel", "ioctl", "driver", "tamper", "attestation") {
		lenses = append(lenses, AnalysisLens{
			Type:            "security_boundary",
			PrioritySignals: []string{"ioctl", "driver", "privilege", "signature", "policy", "kernel"},
			OutputFocus:     []string{"trust boundaries", "privileged flows", "enforcement points"},
		})
	}
	if containsAny(lower, "unreal", "blueprint", "uobject", "uclass", "uproject", "uplugin", "replication", "gamemode", "gameinstance", "playercontroller") {
		lenses = append(lenses,
			AnalysisLens{
				Type:            "unreal_module",
				PrioritySignals: []string{"uproject", "uplugin", "build.cs", "target.cs", "module", "plugin"},
				OutputFocus:     []string{"module map", "plugin layout", "target composition"},
			},
			AnalysisLens{
				Type:            "unreal_gameplay",
				PrioritySignals: []string{"gameinstance", "gamemode", "playercontroller", "pawn", "character", "subsystem"},
				OutputFocus:     []string{"gameplay framework", "runtime ownership", "subsystem map"},
			},
			AnalysisLens{
				Type:            "unreal_input",
				PrioritySignals: []string{"enhancedinput", "inputaction", "inputmappingcontext", "bindaction"},
				OutputFocus:     []string{"input pipeline", "mapping contexts", "input bindings"},
			},
			AnalysisLens{
				Type:            "unreal_ui",
				PrioritySignals: []string{"uuserwidget", "createwidget", "bindwidget", "widgettree", "wbp_"},
				OutputFocus:     []string{"ui flow", "widget ownership", "screen composition"},
			},
			AnalysisLens{
				Type:            "unreal_ability",
				PrioritySignals: []string{"abilitysystemcomponent", "gameplayability", "attributeset", "gameplayeffect"},
				OutputFocus:     []string{"ability framework", "combat actions", "effect application"},
			},
		)
	}
	return analysisUniqueLenses(lenses)
}

func deriveScopedAnalysisShards(goal string, snapshot ProjectSnapshot, shards []AnalysisShard) (AnalysisGoalScope, []AnalysisShard) {
	scope := deriveAnalysisGoalScope(goal, snapshot)
	return deriveScopedAnalysisShardsForScope(scope, shards)
}

func deriveScopedAnalysisShardsForScope(scope AnalysisGoalScope, shards []AnalysisShard) (AnalysisGoalScope, []AnalysisShard) {
	if len(scope.DirectoryPrefixes) == 0 {
		return AnalysisGoalScope{}, nil
	}
	filtered := []AnalysisShard{}
	for _, shard := range shards {
		if shardMatchesAnalysisGoalScope(shard, scope) {
			filtered = append(filtered, shard)
		}
	}
	if len(filtered) == 0 {
		return AnalysisGoalScope{}, nil
	}
	return scope, filtered
}

func deriveAnalysisScope(goal string, explicitScope AnalysisGoalScope, snapshot ProjectSnapshot) AnalysisGoalScope {
	if len(explicitScope.DirectoryPrefixes) > 0 {
		return AnalysisGoalScope{DirectoryPrefixes: compactAnalysisScopePrefixes(explicitScope.DirectoryPrefixes)}
	}
	return deriveAnalysisGoalScope(goal, snapshot)
}

func deriveAnalysisGoalScope(goal string, snapshot ProjectSnapshot) AnalysisGoalScope {
	lowerGoal := strings.ToLower(filepath.ToSlash(strings.TrimSpace(goal)))
	if lowerGoal == "" {
		return AnalysisGoalScope{}
	}
	directories := analysisScopeDirectories(snapshot)
	fullMatches := []string{}
	baseMatches := []string{}
	hasDirectoryHint := analysisGoalHasDirectoryHint(lowerGoal)
	for _, dir := range directories {
		lowerDir := strings.ToLower(filepath.ToSlash(dir))
		if lowerDir == "" {
			continue
		}
		if strings.Contains(lowerGoal, lowerDir) {
			fullMatches = append(fullMatches, dir)
			continue
		}
		if !hasDirectoryHint {
			continue
		}
		base := strings.ToLower(filepath.Base(lowerDir))
		if base == "" || base == "." {
			continue
		}
		if strings.Contains(lowerGoal, base) {
			baseMatches = append(baseMatches, dir)
		}
	}
	if len(fullMatches) > 0 {
		return AnalysisGoalScope{DirectoryPrefixes: compactAnalysisScopePrefixes(fullMatches)}
	}
	if len(baseMatches) > 0 {
		return AnalysisGoalScope{DirectoryPrefixes: compactAnalysisScopePrefixes(baseMatches)}
	}
	return AnalysisGoalScope{}
}

func analysisScopeDirectories(snapshot ProjectSnapshot) []string {
	seen := map[string]struct{}{}
	items := []string{}
	for _, dir := range snapshot.Directories {
		trimmed := filepath.ToSlash(strings.TrimSpace(dir))
		if trimmed == "" || trimmed == "." {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, trimmed)
	}
	for dir := range snapshot.FilesByDirectory {
		trimmed := filepath.ToSlash(strings.TrimSpace(dir))
		if trimmed == "" || trimmed == "." {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, trimmed)
	}
	sort.Slice(items, func(i int, j int) bool {
		if len(items[i]) == len(items[j]) {
			return items[i] < items[j]
		}
		return len(items[i]) > len(items[j])
	})
	return items
}

func analysisGoalHasDirectoryHint(lowerGoal string) bool {
	return containsAny(lowerGoal,
		" directory",
		" directories",
		" folder",
		" folders",
		" dir ",
		"path ",
		"under ",
		"inside ",
		"within ",
		"only ",
		"just ",
		"디렉토리",
		"폴더",
		"경로",
		"하위",
		"안의",
		"내의",
		"만 분석",
		"만 문서화",
	)
}

func compactAnalysisScopePrefixes(items []string) []string {
	unique := analysisUniqueStrings(items)
	sort.Slice(unique, func(i int, j int) bool {
		if len(unique[i]) == len(unique[j]) {
			return unique[i] < unique[j]
		}
		return len(unique[i]) > len(unique[j])
	})
	kept := []string{}
	for _, item := range unique {
		normalized := strings.ToLower(filepath.ToSlash(strings.TrimSpace(item)))
		if normalized == "" {
			continue
		}
		skip := false
		for _, existing := range kept {
			lowerExisting := strings.ToLower(filepath.ToSlash(existing))
			if strings.HasPrefix(lowerExisting+"/", normalized+"/") {
				skip = true
				break
			}
		}
		if !skip {
			kept = append(kept, item)
		}
	}
	sort.Strings(kept)
	return kept
}

func shardMatchesAnalysisGoalScope(shard AnalysisShard, scope AnalysisGoalScope) bool {
	if len(scope.DirectoryPrefixes) == 0 {
		return true
	}
	for _, prefix := range scope.DirectoryPrefixes {
		if hasPathPrefix(shard.PrimaryFiles, prefix+"/") || hasPathPrefix(shard.PrimaryFiles, prefix) {
			return true
		}
	}
	return false
}

func refineAnalysisLensesForSnapshot(snapshot ProjectSnapshot, lenses []AnalysisLens) []AnalysisLens {
	if len(snapshot.UnrealProjects) == 0 && len(snapshot.UnrealModules) == 0 && len(snapshot.UnrealTargets) == 0 {
		return analysisUniqueLenses(lenses)
	}
	lenses = append(lenses,
		AnalysisLens{
			Type:            "unreal_module",
			PrioritySignals: []string{"uproject", "uplugin", "build.cs", "target.cs", "module", "plugin"},
			OutputFocus:     []string{"module map", "plugin layout", "target composition"},
		},
		AnalysisLens{
			Type:            "unreal_gameplay",
			PrioritySignals: []string{"gameinstance", "gamemode", "playercontroller", "pawn", "character", "subsystem"},
			OutputFocus:     []string{"gameplay framework", "runtime ownership", "subsystem map"},
		},
		AnalysisLens{
			Type:            "unreal_network",
			PrioritySignals: []string{"replicated", "server", "client", "netmulticast", "rpc"},
			OutputFocus:     []string{"replication paths", "authority boundaries", "network surfaces"},
		},
		AnalysisLens{
			Type:            "unreal_input",
			PrioritySignals: []string{"enhancedinput", "inputaction", "inputmappingcontext", "bindaction"},
			OutputFocus:     []string{"input pipeline", "mapping contexts", "input bindings"},
		},
		AnalysisLens{
			Type:            "unreal_ui",
			PrioritySignals: []string{"uuserwidget", "createwidget", "bindwidget", "widgettree", "wbp_"},
			OutputFocus:     []string{"ui flow", "widget ownership", "screen composition"},
		},
		AnalysisLens{
			Type:            "unreal_ability",
			PrioritySignals: []string{"abilitysystemcomponent", "gameplayability", "attributeset", "gameplayeffect"},
			OutputFocus:     []string{"ability framework", "combat actions", "effect application"},
		},
	)
	return analysisUniqueLenses(lenses)
}

func analysisUniqueLenses(items []AnalysisLens) []AnalysisLens {
	out := make([]AnalysisLens, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		key := strings.TrimSpace(item.Type)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		item.PrioritySignals = analysisUniqueStrings(item.PrioritySignals)
		item.SuppressedSignals = analysisUniqueStrings(item.SuppressedSignals)
		item.OutputFocus = analysisUniqueStrings(item.OutputFocus)
		out = append(out, item)
		seen[key] = struct{}{}
	}
	return out
}

func (a *projectAnalyzer) scoreFileImportance(snapshot *ProjectSnapshot, lenses []AnalysisLens) {
	lensTypes := map[string]struct{}{}
	for _, lens := range lenses {
		lensTypes[strings.TrimSpace(lens.Type)] = struct{}{}
	}
	for i := range snapshot.Files {
		file := snapshot.Files[i]
		score := 0
		reasons := []string{}
		lowerPath := strings.ToLower(file.Path)
		base := strings.ToLower(filepath.Base(file.Path))
		if file.IsEntrypoint {
			score += 40
			reasons = append(reasons, "entrypoint")
		}
		if file.IsManifest {
			score += 16
			reasons = append(reasons, "manifest")
		}
		if containsAny(lowerPath, "main.", "winmain", "dllmain", "servicemain") {
			score += 24
			reasons = append(reasons, "startup_symbol")
		}
		if containsAny(lowerPath, "manager", "dispatcher", "router", "scheduler", "worker", "service", "orchestr", "controller", "kernel") {
			score += 18
			reasons = append(reasons, "control_orchestration")
		}
		if containsAny(lowerPath, "pipe", "iocp", "rpc", "message", "command", "handler", "ioctl", "event", "monitor") {
			score += 20
			reasons = append(reasons, "boundary_signal")
		}
		if len(file.Imports) >= 8 {
			score += 8
			reasons = append(reasons, "high_fanout")
		}
		if reverse := len(snapshot.ReverseImportGraph[file.Path]); reverse >= 5 {
			score += 12
			reasons = append(reasons, "high_fanin")
		}
		if file.LineCount >= 800 {
			score += 10
			reasons = append(reasons, "large_file")
		}
		if _, ok := lensTypes["ipc"]; ok && containsAny(lowerPath, "pipe", "message", "command", "handler", "iocp") {
			score += 18
			reasons = append(reasons, "ipc_lens_priority")
		}
		if _, ok := lensTypes["runtime_flow"]; ok && containsAny(lowerPath, "main", "worker", "service", "loader", "startup", "boot") {
			score += 14
			reasons = append(reasons, "runtime_lens_priority")
		}
		if _, ok := lensTypes["security_boundary"]; ok && containsAny(lowerPath, "kernel", "driver", "policy", "integrity", "signature", "protect") {
			score += 14
			reasons = append(reasons, "security_lens_priority")
		}
		if _, ok := lensTypes["root_cause"]; ok && containsAny(lowerPath, "agent", "intent", "context", "tool", "write", "document", "docs", "final", "review", "loop", "command", "completion", "config") {
			score += 22
			reasons = append(reasons, "root_cause_lens_priority")
		}
		if containsAny(lowerPath, ".uproject", ".uplugin", ".build.cs", ".target.cs") {
			score += 26
			reasons = append(reasons, "unreal_project_metadata")
		}
		if containsAny(lowerPath, "/source/", "/plugins/") && containsAny(lowerPath, "build.cs", "target.cs", "gameinstance", "gamemode", "playercontroller", "character", "pawn", "subsystem") {
			score += 18
			reasons = append(reasons, "unreal_framework_signal")
		}
		if _, ok := lensTypes["unreal_module"]; ok && containsAny(lowerPath, ".uproject", ".uplugin", ".build.cs", ".target.cs", "/plugins/", "/source/") {
			score += 20
			reasons = append(reasons, "unreal_module_lens_priority")
		}
		if _, ok := lensTypes["unreal_gameplay"]; ok && containsAny(lowerPath, "gameinstance", "gamemode", "playercontroller", "character", "pawn", "subsystem", "ability", "inventory") {
			score += 18
			reasons = append(reasons, "unreal_gameplay_lens_priority")
		}
		if _, ok := lensTypes["unreal_network"]; ok && containsAny(lowerPath, "replication", "replicated", "rpc", "net", "prediction", "movement") {
			score += 16
			reasons = append(reasons, "unreal_network_lens_priority")
		}
		if _, ok := lensTypes["unreal_input"]; ok && containsAny(lowerPath, "inputaction", "inputmappingcontext", "enhancedinput", "inputconfig", "bindaction") {
			score += 16
			reasons = append(reasons, "unreal_input_lens_priority")
		}
		if _, ok := lensTypes["unreal_ui"]; ok && containsAny(lowerPath, "widget", "hud", "ui", "umg", "bindwidget", "wbp_") {
			score += 16
			reasons = append(reasons, "unreal_ui_lens_priority")
		}
		if _, ok := lensTypes["unreal_ability"]; ok && containsAny(lowerPath, "ability", "gameplayeffect", "attributeset", "abilitysystem", "effect") {
			score += 16
			reasons = append(reasons, "unreal_ability_lens_priority")
		}
		if isExternalLikePath(lowerPath) {
			score -= 18
			reasons = append(reasons, "external_penalty")
		}
		if containsAny(base, "gtest", "inflate.c", "command_line_parser") {
			score -= 12
			reasons = append(reasons, "library_noise_penalty")
		}
		if score < 0 {
			score = 0
		}
		file.ImportanceScore = score
		file.ImportanceReasons = analysisUniqueStrings(reasons)
		snapshot.Files[i] = file
		snapshot.FilesByPath[file.Path] = file
		dirFiles := snapshot.FilesByDirectory[file.Directory]
		for j := range dirFiles {
			if dirFiles[j].Path == file.Path {
				dirFiles[j] = file
				break
			}
		}
		snapshot.FilesByDirectory[file.Directory] = dirFiles
	}
	sort.SliceStable(snapshot.Files, func(i int, j int) bool {
		if snapshot.Files[i].ImportanceScore == snapshot.Files[j].ImportanceScore {
			return snapshot.Files[i].Path < snapshot.Files[j].Path
		}
		return snapshot.Files[i].ImportanceScore > snapshot.Files[j].ImportanceScore
	})
}

func isExternalLikePath(lowerPath string) bool {
	return strings.HasPrefix(lowerPath, "external/") ||
		strings.HasPrefix(lowerPath, "third_party/") ||
		strings.HasPrefix(lowerPath, "vendor/") ||
		strings.HasPrefix(lowerPath, "node_modules/") ||
		containsAny(lowerPath, "/external/", "/third_party/", "/vendor/", "/node_modules/", "/gtest/", "/googletest/")
}

func (a *projectAnalyzer) enrichSolutionMetadata(snapshot *ProjectSnapshot) {
	solutionPaths := []string{}
	for _, path := range snapshot.ManifestFiles {
		if strings.HasSuffix(strings.ToLower(path), ".sln") {
			solutionPaths = append(solutionPaths, path)
		}
	}
	if len(solutionPaths) == 0 {
		return
	}

	projects := []SolutionProject{}
	for _, slnPath := range solutionPaths {
		projects = append(projects, parseSolutionProjects(snapshot.Root, slnPath)...)
	}
	if len(projects) == 0 {
		return
	}

	for i := range projects {
		projectDir := filepath.ToSlash(filepath.Dir(projects[i].Path))
		if projectDir == "." {
			projectDir = ""
		}
		projects[i].Directory = projectDir
		if strings.HasSuffix(strings.ToLower(projects[i].Path), ".vcxproj") {
			projects[i].OutputType = parseVCXProjOutputType(snapshot.Root, projects[i].Path)
			projects[i].ProjectReferences = parseVCXProjProjectReferences(snapshot.Root, projects[i].Path)
		}
		projects[i].EntryFiles = analysisUniqueStrings(solutionProjectEntryFiles(*snapshot, projects[i]))
		if isExecutableSolutionProject(projects[i]) {
			projects[i].StartupCandidate = true
			snapshot.StartupProjects = append(snapshot.StartupProjects, projects[i].Name)
			snapshot.EntrypointFiles = append(snapshot.EntrypointFiles, projects[i].EntryFiles...)
		}
	}

	snapshot.SolutionProjects = projects
	snapshot.PrimaryStartup = inferPrimaryStartupProject(projects, solutionPaths)
	snapshot.RuntimeEdges = inferRuntimeEdges(*snapshot, projects)
	snapshot.EntrypointFiles = analysisUniqueStrings(snapshot.EntrypointFiles)
}

func (a *projectAnalyzer) enrichUnrealMetadata(snapshot *ProjectSnapshot) {
	projects := []UnrealProject{}
	plugins := []UnrealPlugin{}
	targets := []UnrealTarget{}
	modules := []UnrealModule{}
	moduleByName := map[string]UnrealModule{}
	pluginModuleOwners := map[string]string{}

	for _, manifest := range snapshot.ManifestFiles {
		lower := strings.ToLower(manifest)
		switch {
		case strings.HasSuffix(lower, ".uproject"):
			projects = append(projects, parseUnrealProject(snapshot.Root, manifest))
		case strings.HasSuffix(lower, ".uplugin"):
			plugin := parseUnrealPlugin(snapshot.Root, manifest)
			plugins = append(plugins, plugin)
			for _, module := range plugin.Modules {
				pluginModuleOwners[module] = plugin.Name
			}
		}
	}

	for _, file := range snapshot.Files {
		lower := strings.ToLower(file.Path)
		switch {
		case strings.HasSuffix(lower, ".target.cs"):
			targets = append(targets, parseUnrealTarget(snapshot.Root, file.Path))
		case strings.HasSuffix(lower, ".build.cs"):
			module := parseUnrealBuildModule(snapshot.Root, file.Path)
			if module.Name != "" {
				if owner, ok := pluginModuleOwners[module.Name]; ok {
					module.Plugin = owner
				}
				moduleByName[module.Name] = module
			}
		}
	}

	for _, project := range projects {
		for _, moduleName := range project.Modules {
			if module, ok := moduleByName[moduleName]; ok {
				module.Kind = firstNonBlankAnalysisString(module.Kind, "game_module")
				moduleByName[moduleName] = module
			}
		}
	}
	for _, target := range targets {
		for _, moduleName := range target.Modules {
			if module, ok := moduleByName[moduleName]; ok {
				module.Kind = firstNonBlankAnalysisString(module.Kind, strings.ToLower(strings.TrimSpace(target.TargetType))+"_module")
				moduleByName[moduleName] = module
			}
		}
	}
	moduleNames := make([]string, 0, len(moduleByName))
	for name := range moduleByName {
		moduleNames = append(moduleNames, name)
	}
	sort.Strings(moduleNames)
	for _, name := range moduleNames {
		modules = append(modules, moduleByName[name])
	}
	for i := range projects {
		projects[i].Modules = analysisUniqueStrings(projects[i].Modules)
		projects[i].Plugins = analysisUniqueStrings(projects[i].Plugins)
	}
	for i := range plugins {
		plugins[i].Modules = analysisUniqueStrings(plugins[i].Modules)
	}
	for i := range targets {
		targets[i].Modules = analysisUniqueStrings(targets[i].Modules)
	}
	snapshot.UnrealProjects = projects
	snapshot.UnrealPlugins = plugins
	snapshot.UnrealTargets = targets
	snapshot.UnrealModules = modules
	snapshot.PrimaryUnrealModule = inferPrimaryUnrealModule(projects, targets)
	snapshot.UnrealTypes = extractUnrealReflectedTypes(*snapshot)
	snapshot.UnrealNetwork = extractUnrealNetworkSurfaces(*snapshot)
	snapshot.UnrealAssets = extractUnrealAssetReferences(*snapshot)
	snapshot.UnrealSystems = extractUnrealGameplaySystems(*snapshot)
	snapshot.UnrealSettings = extractUnrealProjectSettings(*snapshot)
}

func parseUnrealProject(root string, relPath string) UnrealProject {
	type unrealProjectFile struct {
		Modules []struct {
			Name string `json:"Name"`
		} `json:"Modules"`
		Plugins []struct {
			Name    string `json:"Name"`
			Enabled bool   `json:"Enabled"`
		} `json:"Plugins"`
	}
	project := UnrealProject{
		Name: strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath)),
		Path: relPath,
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return project
	}
	var parsed unrealProjectFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return project
	}
	for _, module := range parsed.Modules {
		if strings.TrimSpace(module.Name) != "" {
			project.Modules = append(project.Modules, module.Name)
		}
	}
	for _, plugin := range parsed.Plugins {
		if plugin.Enabled && strings.TrimSpace(plugin.Name) != "" {
			project.Plugins = append(project.Plugins, plugin.Name)
		}
	}
	project.Modules = analysisUniqueStrings(project.Modules)
	project.Plugins = analysisUniqueStrings(project.Plugins)
	return project
}

func parseUnrealPlugin(root string, relPath string) UnrealPlugin {
	type unrealPluginFile struct {
		EnabledByDefault bool `json:"EnabledByDefault"`
		Modules          []struct {
			Name string `json:"Name"`
		} `json:"Modules"`
	}
	plugin := UnrealPlugin{
		Name: strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath)),
		Path: relPath,
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return plugin
	}
	var parsed unrealPluginFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return plugin
	}
	plugin.EnabledByDefault = parsed.EnabledByDefault
	for _, module := range parsed.Modules {
		if strings.TrimSpace(module.Name) != "" {
			plugin.Modules = append(plugin.Modules, module.Name)
		}
	}
	plugin.Modules = analysisUniqueStrings(plugin.Modules)
	return plugin
}

func parseUnrealTarget(root string, relPath string) UnrealTarget {
	target := UnrealTarget{
		Name: trimCompoundSuffix(filepath.Base(relPath), ".Target.cs"),
		Path: relPath,
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return target
	}
	text := string(data)
	if match := regexp.MustCompile(`(?i)Type\s*=\s*TargetType\.(\w+)`).FindStringSubmatch(text); len(match) == 2 {
		target.TargetType = match[1]
	}
	target.Modules = append(target.Modules, extractQuotedTokenList(text, "ExtraModuleNames.AddRange")...)
	target.Modules = append(target.Modules, extractQuotedTokenList(text, "ExtraModuleNames.Add")...)
	target.Modules = analysisUniqueStrings(target.Modules)
	return target
}

func parseUnrealBuildModule(root string, relPath string) UnrealModule {
	module := UnrealModule{
		Name: trimCompoundSuffix(filepath.Base(relPath), ".Build.cs"),
		Path: relPath,
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return module
	}
	text := string(data)
	module.PublicDependencies = analysisUniqueStrings(extractQuotedTokenList(text, "PublicDependencyModuleNames.AddRange"))
	module.PublicDependencies = analysisUniqueStrings(append(module.PublicDependencies, extractQuotedTokenList(text, "PublicDependencyModuleNames.Add")...))
	module.PrivateDependencies = analysisUniqueStrings(extractQuotedTokenList(text, "PrivateDependencyModuleNames.AddRange"))
	module.PrivateDependencies = analysisUniqueStrings(append(module.PrivateDependencies, extractQuotedTokenList(text, "PrivateDependencyModuleNames.Add")...))
	module.DynamicallyLoaded = analysisUniqueStrings(extractQuotedTokenList(text, "DynamicallyLoadedModuleNames.AddRange"))
	module.DynamicallyLoaded = analysisUniqueStrings(append(module.DynamicallyLoaded, extractQuotedTokenList(text, "DynamicallyLoadedModuleNames.Add")...))
	module.Kind = inferUnrealModuleKind(relPath)
	return module
}

func extractQuotedTokenList(text string, anchor string) []string {
	index := strings.Index(text, anchor)
	if index < 0 {
		return nil
	}
	window := text[index:]
	if end := strings.Index(window, ");"); end >= 0 {
		window = window[:end]
	} else if end := strings.Index(window, "\n"); end >= 0 {
		window = window[:end]
	} else if len(window) > 600 {
		window = window[:600]
	}
	matches := regexp.MustCompile(`"([A-Za-z0-9_]+)"`).FindAllStringSubmatch(window, -1)
	out := []string{}
	for _, match := range matches {
		if len(match) == 2 {
			out = append(out, match[1])
		}
	}
	return analysisUniqueStrings(out)
}

func inferUnrealModuleKind(relPath string) string {
	lower := strings.ToLower(filepath.ToSlash(relPath))
	switch {
	case strings.Contains(lower, "/plugins/"):
		return "plugin_module"
	case strings.Contains(lower, "/source/"):
		return "game_module"
	default:
		return "module"
	}
}

func inferPrimaryUnrealModule(projects []UnrealProject, targets []UnrealTarget) string {
	for _, target := range targets {
		if strings.EqualFold(strings.TrimSpace(target.TargetType), "Game") && len(target.Modules) > 0 {
			return target.Modules[0]
		}
	}
	for _, project := range projects {
		if len(project.Modules) > 0 {
			return project.Modules[0]
		}
	}
	return ""
}

func extractUnrealReflectedTypes(snapshot ProjectSnapshot) []UnrealReflectedType {
	types := []UnrealReflectedType{}
	seen := map[string]struct{}{}
	classRe := regexp.MustCompile(`(?s)UCLASS\s*\(([^)]*)\)\s*class\s+(?:[A-Za-z0-9_]+\s+)?([A-Za-z0-9_]+)\s*:\s*public\s+([A-Za-z0-9_]+)`)
	structRe := regexp.MustCompile(`(?s)USTRUCT\s*\(([^)]*)\)\s*struct\s+(?:[A-Za-z0-9_]+\s+)?([A-Za-z0-9_]+)`)
	functionRe := regexp.MustCompile(`UFUNCTION\s*\(([^)]*)\)\s*[\w:<>\s*&]+\s+([A-Za-z0-9_]+)\s*\(`)
	propertyRe := regexp.MustCompile(`UPROPERTY\s*\(([^)]*)\)\s*[\w:<>\s*&]+\s+([A-Za-z0-9_]+)\s*[;=]`)
	for _, file := range snapshot.Files {
		lower := strings.ToLower(file.Path)
		if filepath.Ext(lower) != ".h" && filepath.Ext(lower) != ".hpp" {
			continue
		}
		if !(strings.HasPrefix(lower, "source/") || strings.HasPrefix(lower, "plugins/") || containsAny(lower, "/source/", "/plugins/")) {
			continue
		}
		abs := filepath.Join(snapshot.Root, filepath.FromSlash(file.Path))
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		text := string(data)
		moduleName := unrealModuleForFile(snapshot, file.Path)
		for _, match := range classRe.FindAllStringSubmatch(text, -1) {
			if len(match) < 4 {
				continue
			}
			item := UnrealReflectedType{
				Name:                       strings.TrimSpace(match[2]),
				Kind:                       "UCLASS",
				BaseClass:                  strings.TrimSpace(match[3]),
				Module:                     moduleName,
				File:                       file.Path,
				Specifiers:                 splitUnrealSpecifiers(match[1]),
				GameplayRole:               inferGameplayRole(strings.TrimSpace(match[2]), strings.TrimSpace(match[3])),
				Functions:                  collectReflectedNames(text, functionRe, 2),
				Properties:                 collectReflectedNames(text, propertyRe, 2),
				BlueprintCallableFunctions: collectBlueprintFunctionNames(text, "BlueprintCallable"),
				BlueprintEventFunctions:    collectBlueprintEventNames(text),
				GameInstanceClass:          extractAssignedUnrealClass(text, "GameInstanceClass"),
				GameModeClass:              extractAssignedUnrealClass(text, "GameModeClass"),
				GameStateClass:             extractAssignedUnrealClass(text, "GameStateClass"),
				PlayerControllerClass:      extractAssignedUnrealClass(text, "PlayerControllerClass"),
				PlayerStateClass:           extractAssignedUnrealClass(text, "PlayerStateClass"),
				DefaultPawnClass:           extractAssignedUnrealClass(text, "DefaultPawnClass"),
				HUDClass:                   extractAssignedUnrealClass(text, "HUDClass"),
			}
			key := strings.ToLower(item.Name + "|" + item.File)
			if _, ok := seen[key]; ok || item.Name == "" {
				continue
			}
			seen[key] = struct{}{}
			types = append(types, item)
		}
		for _, match := range structRe.FindAllStringSubmatch(text, -1) {
			if len(match) < 3 {
				continue
			}
			item := UnrealReflectedType{
				Name:       strings.TrimSpace(match[2]),
				Kind:       "USTRUCT",
				Module:     moduleName,
				File:       file.Path,
				Specifiers: splitUnrealSpecifiers(match[1]),
				Properties: collectReflectedNames(text, propertyRe, 2),
			}
			key := strings.ToLower(item.Name + "|" + item.File)
			if _, ok := seen[key]; ok || item.Name == "" {
				continue
			}
			seen[key] = struct{}{}
			types = append(types, item)
		}
	}
	sort.SliceStable(types, func(i int, j int) bool {
		if types[i].GameplayRole == types[j].GameplayRole {
			if types[i].Module == types[j].Module {
				return types[i].Name < types[j].Name
			}
			return types[i].Module < types[j].Module
		}
		return types[i].GameplayRole < types[j].GameplayRole
	})
	return types
}

func unrealModuleForFile(snapshot ProjectSnapshot, path string) string {
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	for _, module := range snapshot.UnrealModules {
		moduleDir := strings.ToLower(filepath.ToSlash(filepath.Dir(module.Path)))
		if moduleDir != "" && strings.HasPrefix(lowerPath, strings.TrimSuffix(moduleDir, "/")+"/") {
			return module.Name
		}
	}
	return ""
}

func splitUnrealSpecifiers(raw string) []string {
	parts := strings.Split(raw, ",")
	out := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return analysisUniqueStrings(out)
}

func collectReflectedNames(text string, re *regexp.Regexp, nameIndex int) []string {
	out := []string{}
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		if len(match) > nameIndex {
			name := strings.TrimSpace(match[nameIndex])
			if name != "" {
				out = append(out, name)
			}
		}
	}
	return analysisUniqueStrings(out)
}

func collectBlueprintFunctionNames(text string, marker string) []string {
	re := regexp.MustCompile(`UFUNCTION\s*\(([^)]*)\)\s*[\w:<>\s*&]+\s+([A-Za-z0-9_]+)\s*\(`)
	out := []string{}
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		if len(match) < 3 {
			continue
		}
		if strings.Contains(strings.ToLower(match[1]), strings.ToLower(marker)) {
			name := strings.TrimSpace(match[2])
			if name != "" {
				out = append(out, name)
			}
		}
	}
	return analysisUniqueStrings(out)
}

func collectBlueprintEventNames(text string) []string {
	re := regexp.MustCompile(`UFUNCTION\s*\(([^)]*)\)\s*[\w:<>\s*&]+\s+([A-Za-z0-9_]+)\s*\(`)
	out := []string{}
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		if len(match) < 3 {
			continue
		}
		specs := strings.ToLower(match[1])
		if strings.Contains(specs, "blueprintimplementableevent") || strings.Contains(specs, "blueprintnativeevent") {
			name := strings.TrimSpace(match[2])
			if name != "" {
				out = append(out, name)
			}
		}
	}
	return analysisUniqueStrings(out)
}

func extractAssignedUnrealClass(text string, field string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(field + `\s*=\s*([A-Za-z0-9_]+)::StaticClass`),
		regexp.MustCompile(field + `\s*=\s*StaticLoadClass\([^,]+,\s*nullptr,\s*TEXT\("([^"]+)"\)`),
		regexp.MustCompile(field + `\s*=\s*LoadClass<[^>]+>\([^,]+,\s*TEXT\("([^"]+)"\)`),
	}
	for _, pattern := range patterns {
		if match := pattern.FindStringSubmatch(text); len(match) >= 2 {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

func inferGameplayRole(name string, base string) string {
	corpus := strings.ToLower(name + " " + base)
	switch {
	case containsAny(corpus, "gameinstance"):
		return "game_instance"
	case containsAny(corpus, "gamemode"):
		return "game_mode"
	case containsAny(corpus, "gamestate"):
		return "game_state"
	case containsAny(corpus, "playercontroller", "controller"):
		return "player_controller"
	case containsAny(corpus, "character"):
		return "character"
	case containsAny(corpus, "pawn"):
		return "pawn"
	case containsAny(corpus, "hud"):
		return "hud"
	case containsAny(corpus, "localplayersubsystem", "gameinstancesubsystem", "worldsubsystem", "enginesubsystem", "subsystem"):
		return "subsystem"
	default:
		return ""
	}
}

func extractUnrealNetworkSurfaces(snapshot ProjectSnapshot) []UnrealNetworkSurface {
	merged := map[string]UnrealNetworkSurface{}
	typeByFile := map[string]UnrealReflectedType{}
	for _, item := range snapshot.UnrealTypes {
		if strings.TrimSpace(item.File) != "" {
			typeByFile[item.File] = item
		}
	}
	serverRPCRe := regexp.MustCompile(`UFUNCTION\s*\(([^)]*Server[^)]*)\)\s*[\w:<>\s*&]+\s+([A-Za-z0-9_]+)\s*\(`)
	clientRPCRe := regexp.MustCompile(`UFUNCTION\s*\(([^)]*Client[^)]*)\)\s*[\w:<>\s*&]+\s+([A-Za-z0-9_]+)\s*\(`)
	multicastRPCRe := regexp.MustCompile(`UFUNCTION\s*\(([^)]*NetMulticast[^)]*)\)\s*[\w:<>\s*&]+\s+([A-Za-z0-9_]+)\s*\(`)
	repNotifyRe := regexp.MustCompile(`UPROPERTY\s*\(([^)]*ReplicatedUsing\s*=\s*([A-Za-z0-9_]+)[^)]*)\)\s*[\w:<>\s*&]+\s+([A-Za-z0-9_]+)\s*[;=]`)
	replicatedRe := regexp.MustCompile(`UPROPERTY\s*\(([^)]*Replicated[^)]*)\)\s*[\w:<>\s*&]+\s+([A-Za-z0-9_]+)\s*[;=]`)
	for _, file := range snapshot.Files {
		lower := strings.ToLower(file.Path)
		if filepath.Ext(lower) != ".h" && filepath.Ext(lower) != ".hpp" && filepath.Ext(lower) != ".cpp" {
			continue
		}
		if !(strings.HasPrefix(lower, "source/") || strings.HasPrefix(lower, "plugins/") || containsAny(lower, "/source/", "/plugins/")) {
			continue
		}
		abs := filepath.Join(snapshot.Root, filepath.FromSlash(file.Path))
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		text := string(data)
		moduleName := unrealModuleForFile(snapshot, file.Path)
		serverRPCs := collectReflectedNames(text, serverRPCRe, 2)
		clientRPCs := collectReflectedNames(text, clientRPCRe, 2)
		multicastRPCs := collectReflectedNames(text, multicastRPCRe, 2)
		replicated := collectReflectedNames(text, replicatedRe, 2)
		repNotify := []string{}
		for _, match := range repNotifyRe.FindAllStringSubmatch(text, -1) {
			if len(match) >= 4 {
				name := strings.TrimSpace(match[3])
				if name != "" {
					repNotify = append(repNotify, name)
				}
			}
		}
		hasList := containsAny(strings.ToLower(text), "getlifetimereplicatedprops", "doreplifetime")
		if len(serverRPCs) == 0 && len(clientRPCs) == 0 && len(multicastRPCs) == 0 && len(replicated) == 0 && len(repNotify) == 0 && !hasList {
			continue
		}
		typeName := ""
		if reflected, ok := typeByFile[file.Path]; ok {
			typeName = reflected.Name
		}
		if typeName == "" {
			typeName = inferUnrealTypeNameForPath(snapshot.UnrealTypes, file.Path)
		}
		key := strings.ToLower(firstNonBlankAnalysisString(typeName, file.Path))
		surface := merged[key]
		surface.TypeName = firstNonBlankAnalysisString(surface.TypeName, typeName)
		surface.Module = firstNonBlankAnalysisString(surface.Module, moduleName)
		surface.File = firstNonBlankAnalysisString(surface.File, file.Path)
		surface.ServerRPCs = analysisUniqueStrings(append(surface.ServerRPCs, serverRPCs...))
		surface.ClientRPCs = analysisUniqueStrings(append(surface.ClientRPCs, clientRPCs...))
		surface.MulticastRPCs = analysisUniqueStrings(append(surface.MulticastRPCs, multicastRPCs...))
		surface.ReplicatedProperties = analysisUniqueStrings(append(surface.ReplicatedProperties, replicated...))
		surface.RepNotifyProperties = analysisUniqueStrings(append(surface.RepNotifyProperties, repNotify...))
		surface.HasReplicationList = surface.HasReplicationList || hasList
		merged[key] = surface
	}
	surfaces := make([]UnrealNetworkSurface, 0, len(merged))
	for _, item := range merged {
		surfaces = append(surfaces, item)
	}
	sort.SliceStable(surfaces, func(i int, j int) bool {
		if surfaces[i].TypeName == surfaces[j].TypeName {
			return surfaces[i].File < surfaces[j].File
		}
		return surfaces[i].TypeName < surfaces[j].TypeName
	})
	return surfaces
}

func extractUnrealAssetReferences(snapshot ProjectSnapshot) []UnrealAssetReference {
	assetPathRe := regexp.MustCompile(`/(Game|Engine|Script)/[A-Za-z0-9_/\.\-]+`)
	typeByFile := map[string]UnrealReflectedType{}
	for _, item := range snapshot.UnrealTypes {
		if strings.TrimSpace(item.File) != "" {
			typeByFile[item.File] = item
		}
	}
	merged := map[string]UnrealAssetReference{}
	for _, file := range snapshot.Files {
		lower := strings.ToLower(file.Path)
		if !(strings.HasPrefix(lower, "source/") || strings.HasPrefix(lower, "plugins/") || strings.HasPrefix(lower, "config/") || strings.HasPrefix(lower, "content/") || containsAny(lower, "/source/", "/plugins/", "/config/", "/content/")) {
			continue
		}
		abs := filepath.Join(snapshot.Root, filepath.FromSlash(file.Path))
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		text := string(data)
		assetPaths := []string{}
		for _, match := range assetPathRe.FindAllString(text, -1) {
			assetPaths = append(assetPaths, strings.TrimSpace(match))
		}
		canonicalTargets := []string{}
		for _, path := range assetPaths {
			if target := canonicalizeBlueprintAssetClass(path); target != "" {
				canonicalTargets = append(canonicalTargets, target)
			}
		}
		loadMethods := []string{}
		lowerText := strings.ToLower(text)
		if containsAny(lowerText, "tsoftobjectptr", "tsoftclassptr") {
			loadMethods = append(loadMethods, "soft_reference")
		}
		if containsAny(lowerText, "constructorhelpers::fobjectfinder", "constructorhelpers::fclassfinder") {
			loadMethods = append(loadMethods, "constructor_helpers")
		}
		if containsAny(lowerText, "loadobject<", "staticloadobject(") {
			loadMethods = append(loadMethods, "runtime_object_load")
		}
		if containsAny(lowerText, "loadclass<", "staticloadclass(") {
			loadMethods = append(loadMethods, "runtime_class_load")
		}
		configKeys := []string{}
		if strings.HasSuffix(lower, ".ini") {
			for _, line := range splitLines(text) {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
					continue
				}
				parts := strings.SplitN(line, "=", 2)
				if len(parts) != 2 {
					continue
				}
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				if key != "" {
					configKeys = append(configKeys, key+"="+value)
				}
			}
		}
		if len(assetPaths) == 0 && len(loadMethods) == 0 && len(configKeys) == 0 {
			continue
		}
		ownerName := ""
		if reflected, ok := typeByFile[file.Path]; ok {
			ownerName = reflected.Name
		}
		key := strings.ToLower(firstNonBlankAnalysisString(ownerName, file.Path))
		item := merged[key]
		item.OwnerName = firstNonBlankAnalysisString(item.OwnerName, ownerName)
		item.Module = firstNonBlankAnalysisString(item.Module, unrealModuleForFile(snapshot, file.Path))
		item.File = firstNonBlankAnalysisString(item.File, file.Path)
		item.AssetPaths = analysisUniqueStrings(append(item.AssetPaths, assetPaths...))
		item.CanonicalTargets = analysisUniqueStrings(append(item.CanonicalTargets, canonicalTargets...))
		item.ConfigKeys = analysisUniqueStrings(append(item.ConfigKeys, configKeys...))
		item.LoadMethods = analysisUniqueStrings(append(item.LoadMethods, loadMethods...))
		merged[key] = item
	}
	out := make([]UnrealAssetReference, 0, len(merged))
	for _, item := range merged {
		out = append(out, item)
	}
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].OwnerName == out[j].OwnerName {
			return out[i].File < out[j].File
		}
		return out[i].OwnerName < out[j].OwnerName
	})
	return out
}

func canonicalizeBlueprintAssetClass(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.Trim(value, "\"'")
	if strings.HasPrefix(value, "TEXT(") && strings.HasSuffix(value, ")") {
		value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "TEXT("), ")"))
		value = strings.Trim(value, "\"'")
	}
	if strings.HasPrefix(value, "/Script/") {
		stem := value[strings.LastIndex(value, ".")+1:]
		stem = strings.TrimSuffix(stem, "_C")
		return strings.TrimSpace(stem)
	}
	if !strings.HasPrefix(value, "/Game/") && !strings.HasPrefix(value, "/Engine/") {
		return ""
	}
	stem := value
	if index := strings.LastIndex(stem, "."); index >= 0 && index < len(stem)-1 {
		stem = stem[index+1:]
	}
	stem = strings.TrimSuffix(stem, "_C")
	stem = strings.Trim(stem, "\"'")
	return strings.TrimSpace(stem)
}

func extractUnrealProjectSettings(snapshot ProjectSnapshot) []UnrealProjectSetting {
	settings := []UnrealProjectSetting{}
	for _, file := range snapshot.Files {
		lower := strings.ToLower(file.Path)
		if !strings.HasSuffix(lower, ".ini") || !(strings.Contains(lower, "defaultengine.ini") || strings.Contains(lower, "defaultgame.ini")) {
			continue
		}
		abs := filepath.Join(snapshot.Root, filepath.FromSlash(file.Path))
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		content := string(data)
		item := UnrealProjectSetting{
			SourceFile:            file.Path,
			GameDefaultMap:        extractIniValue(content, "GameDefaultMap"),
			EditorStartupMap:      extractIniValue(content, "EditorStartupMap"),
			GlobalDefaultGameMode: extractIniValue(content, "GlobalDefaultGameMode"),
			GameInstanceClass:     extractIniValue(content, "GameInstanceClass"),
			DefaultPawnClass:      extractIniValue(content, "DefaultPawnClass"),
			PlayerControllerClass: extractIniValue(content, "PlayerControllerClass"),
			HUDClass:              extractIniValue(content, "HUDClass"),
		}
		if item.GameDefaultMap == "" && item.EditorStartupMap == "" && item.GlobalDefaultGameMode == "" && item.GameInstanceClass == "" && item.DefaultPawnClass == "" && item.PlayerControllerClass == "" && item.HUDClass == "" {
			continue
		}
		settings = append(settings, item)
	}
	sort.SliceStable(settings, func(i int, j int) bool {
		return settings[i].SourceFile < settings[j].SourceFile
	})
	return settings
}

func extractUnrealGameplaySystems(snapshot ProjectSnapshot) []UnrealGameplaySystem {
	merged := map[string]UnrealGameplaySystem{}
	add := func(system string, owner string, module string, file string, signals []string, assets []string, functions []string, ownedBy []string, targets []string, contexts []string, actions []string, widgets []string, abilities []string, effects []string, attributes []string) {
		system = strings.TrimSpace(system)
		if system == "" {
			return
		}
		key := strings.ToLower(system + "|" + strings.TrimSpace(owner) + "|" + strings.TrimSpace(module))
		item := merged[key]
		item.System = firstNonBlankAnalysisString(item.System, system)
		item.OwnerName = firstNonBlankAnalysisString(item.OwnerName, owner)
		item.Module = firstNonBlankAnalysisString(item.Module, module)
		item.File = firstNonBlankAnalysisString(item.File, file)
		item.Signals = analysisUniqueStrings(append(item.Signals, signals...))
		item.Assets = analysisUniqueStrings(append(item.Assets, assets...))
		item.Functions = analysisUniqueStrings(append(item.Functions, functions...))
		item.OwnedBy = analysisUniqueStrings(append(item.OwnedBy, ownedBy...))
		item.Targets = analysisUniqueStrings(append(item.Targets, targets...))
		item.Contexts = analysisUniqueStrings(append(item.Contexts, contexts...))
		item.Actions = analysisUniqueStrings(append(item.Actions, actions...))
		item.Widgets = analysisUniqueStrings(append(item.Widgets, widgets...))
		item.Abilities = analysisUniqueStrings(append(item.Abilities, abilities...))
		item.Effects = analysisUniqueStrings(append(item.Effects, effects...))
		item.Attributes = analysisUniqueStrings(append(item.Attributes, attributes...))
		merged[key] = item
	}

	typeByFile := map[string]UnrealReflectedType{}
	for _, item := range snapshot.UnrealTypes {
		if strings.TrimSpace(item.File) != "" {
			typeByFile[item.File] = item
		}
	}
	assetsByFile := map[string][]string{}
	for _, item := range snapshot.UnrealAssets {
		if strings.TrimSpace(item.File) != "" {
			assetsByFile[item.File] = analysisUniqueStrings(append(assetsByFile[item.File], item.CanonicalTargets...))
		}
	}

	for _, file := range snapshot.Files {
		lower := strings.ToLower(file.Path)
		if !(strings.HasPrefix(lower, "source/") || strings.HasPrefix(lower, "plugins/") || containsAny(lower, "/source/", "/plugins/")) {
			continue
		}
		abs := filepath.Join(snapshot.Root, filepath.FromSlash(file.Path))
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		text := string(data)
		lowerText := strings.ToLower(text)
		owner := inferUnrealTypeNameForPath(snapshot.UnrealTypes, file.Path)
		if owner == "" {
			if reflected, ok := typeByFile[file.Path]; ok {
				owner = reflected.Name
			}
		}
		module := unrealModuleForFile(snapshot, file.Path)
		assets := assetsByFile[file.Path]
		ownerCandidates := inferUnrealGameplayOwners(snapshot.UnrealTypes, text, file.Path)

		if containsAny(lowerText, "uinputaction", "uinputmappingcontext", "enhancedinputcomponent", "enhancedinputlocalsubsystem", "enhancedinputsubsystem", "bindaction(") {
			signals := []string{}
			if containsAny(lowerText, "uinputaction", "inputaction") {
				signals = append(signals, "input_action")
			}
			if containsAny(lowerText, "uinputmappingcontext", "inputmappingcontext") {
				signals = append(signals, "mapping_context")
			}
			if containsAny(lowerText, "enhancedinputcomponent", "bindaction(") {
				signals = append(signals, "bind_action")
			}
			if containsAny(lowerText, "enhancedinputlocalsubsystem", "enhancedinputsubsystem") {
				signals = append(signals, "input_subsystem")
			}
			functions := []string{}
			bindActionRe := regexp.MustCompile(`BindAction[\s\S]{0,160}?&[A-Za-z0-9_:]+::([A-Za-z0-9_]+)`)
			functions = append(functions, collectReflectedNames(text, bindActionRe, 1)...)
			actions := inferUnrealInputActions(text, assets)
			contexts := inferUnrealInputContexts(text, assets)
			targets := append([]string{}, functions...)
			targets = append(targets, contexts...)
			add("enhanced_input", owner, module, file.Path, signals, assets, functions, ownerCandidates, targets, contexts, actions, nil, nil, nil, nil)
		}

		if containsAny(lowerText, "uuserwidget", "createwidget<", "createwidget(", "bindwidget", "widgettree", "wbp_") {
			signals := []string{}
			if containsAny(lowerText, "uuserwidget") {
				signals = append(signals, "user_widget")
			}
			if containsAny(lowerText, "createwidget<", "createwidget(") {
				signals = append(signals, "create_widget")
			}
			if containsAny(lowerText, "bindwidget") {
				signals = append(signals, "bind_widget")
			}
			if containsAny(lowerText, "widgettree") {
				signals = append(signals, "widget_tree")
			}
			widgets := inferUnrealWidgets(text, assets)
			targets := append([]string{}, widgets...)
			add("umg", owner, module, file.Path, signals, assets, nil, ownerCandidates, targets, nil, nil, widgets, nil, nil, nil)
		}

		if containsAny(lowerText, "uabilitysystemcomponent", "ugameplayability", "uattributeset", "gameplayeffect", "giveability(", "initabilityactorinfo(", "applygameplayeffect") {
			signals := []string{}
			if containsAny(lowerText, "uabilitysystemcomponent") {
				signals = append(signals, "ability_system_component")
			}
			if containsAny(lowerText, "ugameplayability", "giveability(") {
				signals = append(signals, "gameplay_ability")
			}
			if containsAny(lowerText, "uattributeset") {
				signals = append(signals, "attribute_set")
			}
			if containsAny(lowerText, "gameplayeffect", "applygameplayeffect") {
				signals = append(signals, "gameplay_effect")
			}
			functions := []string{}
			for _, token := range []string{"GiveAbility", "InitAbilityActorInfo", "ApplyGameplayEffect"} {
				if strings.Contains(text, token) {
					functions = append(functions, token)
				}
			}
			abilities := inferUnrealGASTypeNames(text, regexp.MustCompile(`([AU][A-Za-z0-9_]+Ability)\b`))
			effects := inferUnrealGASTypeNames(text, regexp.MustCompile(`([AU][A-Za-z0-9_]+Effect)\b`))
			attributes := inferUnrealGASTypeNames(text, regexp.MustCompile(`([AU][A-Za-z0-9_]+AttributeSet)\b`))
			targets := append([]string{}, abilities...)
			targets = append(targets, effects...)
			targets = append(targets, attributes...)
			add("gameplay_ability_system", owner, module, file.Path, signals, assets, functions, ownerCandidates, targets, nil, nil, nil, abilities, effects, attributes)
		}
	}

	out := make([]UnrealGameplaySystem, 0, len(merged))
	for _, item := range merged {
		out = append(out, item)
	}
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].System == out[j].System {
			if out[i].OwnerName == out[j].OwnerName {
				return out[i].File < out[j].File
			}
			return out[i].OwnerName < out[j].OwnerName
		}
		return out[i].System < out[j].System
	})
	return out
}

func inferUnrealGameplayOwners(types []UnrealReflectedType, text string, path string) []string {
	out := []string{}
	for _, item := range types {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		if strings.EqualFold(item.File, path) {
			out = append(out, item.Name)
			continue
		}
		if strings.Contains(text, item.Name+"::") || strings.Contains(text, item.Name+" ") || strings.Contains(text, item.Name+"*") {
			out = append(out, item.Name)
		}
	}
	return analysisUniqueStrings(out)
}

func inferUnrealInputActions(text string, assets []string) []string {
	re := regexp.MustCompile(`\b([A-Za-z0-9_]+Action)\b`)
	out := []string{}
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			out = append(out, strings.TrimSpace(match[1]))
		}
	}
	for _, asset := range assets {
		if strings.HasPrefix(asset, "IA_") {
			out = append(out, asset)
		}
	}
	return analysisUniqueStrings(out)
}

func inferUnrealInputContexts(text string, assets []string) []string {
	re := regexp.MustCompile(`\b([A-Za-z0-9_]+Context|IMC_[A-Za-z0-9_]+)\b`)
	out := []string{}
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			out = append(out, strings.TrimSpace(match[1]))
		}
	}
	for _, asset := range assets {
		if strings.HasPrefix(asset, "IMC_") {
			out = append(out, asset)
		}
	}
	return analysisUniqueStrings(out)
}

func inferUnrealWidgets(text string, assets []string) []string {
	re := regexp.MustCompile(`\b(WBP_[A-Za-z0-9_]+|[AU][A-Za-z0-9_]*Widget)\b`)
	out := []string{}
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			out = append(out, strings.TrimSpace(match[1]))
		}
	}
	for _, asset := range assets {
		if strings.HasPrefix(asset, "WBP_") {
			out = append(out, asset)
		}
	}
	return analysisUniqueStrings(out)
}

func inferUnrealGASTypeNames(text string, re *regexp.Regexp) []string {
	out := []string{}
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			out = append(out, strings.TrimSpace(match[1]))
		}
	}
	return analysisUniqueStrings(out)
}

func extractIniValue(content string, key string) string {
	for _, line := range splitLines(content) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		currentKey := strings.TrimSpace(parts[0])
		if strings.EqualFold(currentKey, key) {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func inferUnrealTypeNameForPath(types []UnrealReflectedType, path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	stem = strings.ToLower(stem)
	for _, item := range types {
		name := strings.ToLower(item.Name)
		trimmed := strings.TrimLeft(name, "auf")
		if strings.EqualFold(name, stem) || strings.EqualFold(trimmed, stem) {
			return item.Name
		}
	}
	return ""
}

func buildUnrealGameplayEdges(types []UnrealReflectedType) []ProjectEdge {
	edges := []ProjectEdge{}
	for _, item := range types {
		edges = append(edges, buildUnrealAssignmentEdges(item)...)
	}
	roleMap := map[string][]UnrealReflectedType{}
	for _, item := range types {
		if strings.TrimSpace(item.GameplayRole) == "" {
			continue
		}
		roleMap[item.GameplayRole] = append(roleMap[item.GameplayRole], item)
	}
	add := func(sourceRole string, targetRole string, kind string) {
		sources := roleMap[sourceRole]
		targets := roleMap[targetRole]
		if len(sources) == 0 || len(targets) == 0 {
			return
		}
		for _, source := range sources {
			for _, target := range targets {
				if strings.EqualFold(source.Name, target.Name) {
					continue
				}
				edges = append(edges, ProjectEdge{
					Source:     source.Name,
					Target:     target.Name,
					Type:       "gameplay_edge",
					Confidence: "medium",
					Evidence:   []string{source.File, target.File},
					Attributes: map[string]string{
						"kind": "gameplay_framework",
						"flow": kind,
					},
				})
			}
		}
	}
	add("game_instance", "game_mode", "game_bootstrap")
	add("game_mode", "game_state", "authority_state")
	add("game_mode", "player_controller", "player_ownership")
	add("player_controller", "pawn", "player_possession")
	add("player_controller", "character", "player_possession")
	add("game_instance", "subsystem", "service_bootstrap")
	add("game_mode", "subsystem", "authority_service")
	return analysisUniqueProjectEdges(edges)
}

func buildUnrealAssignmentEdges(item UnrealReflectedType) []ProjectEdge {
	edges := []ProjectEdge{}
	add := func(target string, flow string) {
		target = strings.TrimSpace(target)
		if target == "" {
			return
		}
		edges = append(edges, ProjectEdge{
			Source:     item.Name,
			Target:     target,
			Type:       "gameplay_edge",
			Confidence: "high",
			Evidence:   []string{item.File},
			Attributes: map[string]string{
				"kind": "framework_assignment",
				"flow": flow,
			},
		})
	}
	add(item.GameInstanceClass, "game_instance_assignment")
	add(item.GameModeClass, "game_mode_assignment")
	add(item.GameStateClass, "game_state_assignment")
	add(item.PlayerControllerClass, "player_controller_assignment")
	add(item.PlayerStateClass, "player_state_assignment")
	add(item.DefaultPawnClass, "default_pawn_assignment")
	add(item.HUDClass, "hud_assignment")
	return edges
}

func buildUnrealGameplayFlowLines(types []UnrealReflectedType, settings []UnrealProjectSetting) []string {
	lines := []string{}
	for _, setting := range settings {
		if setting.GameDefaultMap != "" {
			lines = append(lines, "Startup -> Map="+setting.GameDefaultMap)
		}
		if setting.GameInstanceClass != "" {
			lines = append(lines, "Startup -> GameInstance="+setting.GameInstanceClass)
			if canonical := canonicalizeBlueprintAssetClass(setting.GameInstanceClass); canonical != "" && !strings.EqualFold(canonical, setting.GameInstanceClass) {
				lines = append(lines, "Startup -> GameInstanceClass="+canonical)
			}
		}
		if setting.GlobalDefaultGameMode != "" {
			lines = append(lines, "MapLoad -> GameMode="+setting.GlobalDefaultGameMode)
			if canonical := canonicalizeBlueprintAssetClass(setting.GlobalDefaultGameMode); canonical != "" && !strings.EqualFold(canonical, setting.GlobalDefaultGameMode) {
				lines = append(lines, "MapLoad -> GameModeClass="+canonical)
			}
		}
		if setting.PlayerControllerClass != "" {
			lines = append(lines, "GameMode -> PlayerController="+setting.PlayerControllerClass)
			if canonical := canonicalizeBlueprintAssetClass(setting.PlayerControllerClass); canonical != "" && !strings.EqualFold(canonical, setting.PlayerControllerClass) {
				lines = append(lines, "GameMode -> PlayerControllerClass="+canonical)
			}
		}
		if setting.DefaultPawnClass != "" {
			lines = append(lines, "GameMode -> DefaultPawn="+setting.DefaultPawnClass)
			if canonical := canonicalizeBlueprintAssetClass(setting.DefaultPawnClass); canonical != "" && !strings.EqualFold(canonical, setting.DefaultPawnClass) {
				lines = append(lines, "GameMode -> DefaultPawnClass="+canonical)
			}
		}
		if setting.HUDClass != "" {
			lines = append(lines, "GameMode -> HUD="+setting.HUDClass)
			if canonical := canonicalizeBlueprintAssetClass(setting.HUDClass); canonical != "" && !strings.EqualFold(canonical, setting.HUDClass) {
				lines = append(lines, "GameMode -> HUDClass="+canonical)
			}
		}
	}
	for _, item := range types {
		assignments := []string{}
		if item.GameModeClass != "" {
			assignments = append(assignments, item.Name+" -> GameMode="+item.GameModeClass)
		}
		if item.GameStateClass != "" {
			assignments = append(assignments, item.Name+" -> GameState="+item.GameStateClass)
		}
		if item.PlayerControllerClass != "" {
			assignments = append(assignments, item.Name+" -> PlayerController="+item.PlayerControllerClass)
		}
		if item.PlayerStateClass != "" {
			assignments = append(assignments, item.Name+" -> PlayerState="+item.PlayerStateClass)
		}
		if item.DefaultPawnClass != "" {
			assignments = append(assignments, item.Name+" -> DefaultPawn="+item.DefaultPawnClass)
		}
		if item.HUDClass != "" {
			assignments = append(assignments, item.Name+" -> HUD="+item.HUDClass)
		}
		lines = append(lines, assignments...)
	}
	if len(lines) > 0 {
		return analysisUniqueStrings(lines)
	}
	roleMap := map[string][]string{}
	for _, item := range types {
		if strings.TrimSpace(item.GameplayRole) != "" {
			roleMap[item.GameplayRole] = append(roleMap[item.GameplayRole], item.Name)
		}
	}
	add := func(sourceRole string, targetRole string, label string) {
		for _, source := range analysisUniqueStrings(roleMap[sourceRole]) {
			for _, target := range analysisUniqueStrings(roleMap[targetRole]) {
				if !strings.EqualFold(source, target) {
					lines = append(lines, source+" -> "+target+" ("+label+")")
				}
			}
		}
	}
	add("game_instance", "game_mode", "game_bootstrap")
	add("game_mode", "player_controller", "player_ownership")
	add("player_controller", "pawn", "player_possession")
	add("player_controller", "character", "player_possession")
	add("game_instance", "subsystem", "service_bootstrap")
	return analysisUniqueStrings(lines)
}

func buildUnrealGameplaySystemFlowLines(systems []UnrealGameplaySystem) []string {
	lines := []string{}
	for _, item := range systems {
		source := firstNonBlankAnalysisString(item.OwnerName, item.File)
		for _, owner := range item.OwnedBy {
			lines = append(lines, owner+" -> "+source+" ("+item.System+"_owner)")
		}
		switch item.System {
		case "enhanced_input":
			for _, context := range item.Contexts {
				lines = append(lines, source+" -> InputContext="+context)
			}
			for _, action := range item.Actions {
				lines = append(lines, source+" -> InputAction="+action)
			}
			for _, function := range item.Functions {
				lines = append(lines, source+" -> Handler="+function)
			}
		case "umg":
			for _, widget := range item.Widgets {
				lines = append(lines, source+" -> Widget="+widget)
			}
		case "gameplay_ability_system":
			for _, ability := range item.Abilities {
				lines = append(lines, source+" -> Ability="+ability)
			}
			for _, effect := range item.Effects {
				lines = append(lines, source+" -> Effect="+effect)
			}
			for _, attribute := range item.Attributes {
				lines = append(lines, source+" -> AttributeSet="+attribute)
			}
		}
	}
	return analysisUniqueStrings(lines)
}

func trimCompoundSuffix(name string, suffix string) string {
	if strings.HasSuffix(strings.ToLower(name), strings.ToLower(suffix)) {
		return name[:len(name)-len(suffix)]
	}
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func parseSolutionProjects(root string, slnPath string) []SolutionProject {
	abs := filepath.Join(root, filepath.FromSlash(slnPath))
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	projects := []SolutionProject{}
	for _, line := range splitLines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "Project(") || !strings.Contains(trimmed, "=") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		fields := extractQuotedValues(parts[1])
		if len(fields) < 2 {
			continue
		}
		projectPath, ok := normalizeSolutionProjectPath(root, slnPath, strings.TrimSpace(fields[1]))
		if !ok {
			continue
		}
		ext := strings.ToLower(filepath.Ext(projectPath))
		kind := solutionProjectKind(ext)
		if kind == "" {
			continue
		}
		projects = append(projects, SolutionProject{
			Name: strings.TrimSpace(fields[0]),
			Path: projectPath,
			Kind: kind,
		})
	}
	return projects
}

func normalizeSolutionProjectPath(root string, slnPath string, projectPath string) (string, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return "", false
	}
	nativeProjectPath := filepath.FromSlash(strings.ReplaceAll(projectPath, "\\", "/"))
	projectAbs := nativeProjectPath
	if !filepath.IsAbs(projectAbs) {
		slnAbs := filepath.Join(root, filepath.FromSlash(slnPath))
		projectAbs = filepath.Join(filepath.Dir(slnAbs), nativeProjectPath)
	}
	projectAbs = filepath.Clean(projectAbs)
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	projectAbs, err = filepath.Abs(projectAbs)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(rootAbs, projectAbs)
	if err != nil || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func extractQuotedValues(text string) []string {
	values := []string{}
	start := -1
	for i := 0; i < len(text); i++ {
		if text[i] == '"' {
			if start < 0 {
				start = i + 1
			} else {
				values = append(values, text[start:i])
				start = -1
			}
		}
	}
	return values
}

func solutionProjectKind(ext string) string {
	switch ext {
	case ".vcxproj":
		return "vcxproj"
	case ".csproj":
		return "csproj"
	default:
		return ""
	}
}

type vcxprojConfig struct {
	ConfigurationType string `xml:"ItemDefinitionGroup>Link>SubSystem"`
}

type vcxprojDocument struct {
	ProjectConfigurations []struct {
		ConfigurationType string `xml:"ConfigurationType"`
	} `xml:"ItemDefinitionGroup"`
	PropertyGroups []struct {
		Label             string `xml:"Label,attr"`
		ConfigurationType string `xml:"ConfigurationType"`
	} `xml:"PropertyGroup"`
}

func parseVCXProjOutputType(root string, projectPath string) string {
	abs := filepath.Join(root, filepath.FromSlash(projectPath))
	data, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	doc := vcxprojDocument{}
	if err := xml.Unmarshal(data, &doc); err == nil {
		for _, group := range doc.PropertyGroups {
			value := strings.TrimSpace(strings.ToLower(group.ConfigurationType))
			if value != "" {
				return value
			}
		}
	}
	lower := strings.ToLower(string(data))
	switch {
	case strings.Contains(lower, "<configurationtype>application</configurationtype>"):
		return "application"
	case strings.Contains(lower, "<configurationtype>dynamiclibrary</configurationtype>"):
		return "dynamiclibrary"
	case strings.Contains(lower, "<configurationtype>staticlibrary</configurationtype>"):
		return "staticlibrary"
	}
	return ""
}

func parseVCXProjProjectReferences(root string, projectPath string) []string {
	abs := filepath.Join(root, filepath.FromSlash(projectPath))
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	lower := strings.ToLower(string(data))
	out := []string{}
	marker := "<projectreference include=\""
	index := 0
	for {
		start := strings.Index(lower[index:], marker)
		if start < 0 {
			break
		}
		start += index + len(marker)
		end := strings.Index(lower[start:], "\"")
		if end < 0 {
			break
		}
		ref := filepath.ToSlash(filepath.Clean(string(data[start : start+end])))
		out = append(out, ref)
		index = start + end + 1
	}
	return analysisUniqueStrings(out)
}

func solutionProjectEntryFiles(snapshot ProjectSnapshot, project SolutionProject) []string {
	out := []string{}
	prefix := strings.ToLower(filepath.ToSlash(project.Directory))
	for _, file := range snapshot.Files {
		filePath := strings.ToLower(filepath.ToSlash(file.Path))
		if prefix != "" {
			if !strings.HasPrefix(filePath, prefix+"/") {
				continue
			}
		}
		if file.IsEntrypoint {
			out = append(out, file.Path)
		}
	}
	sort.SliceStable(out, func(i int, j int) bool {
		left := snapshot.FilesByPath[out[i]]
		right := snapshot.FilesByPath[out[j]]
		if left.ImportanceScore == right.ImportanceScore {
			return out[i] < out[j]
		}
		return left.ImportanceScore > right.ImportanceScore
	})
	return out
}

func isExecutableSolutionProject(project SolutionProject) bool {
	switch strings.ToLower(strings.TrimSpace(project.OutputType)) {
	case "application":
		return true
	}
	return len(project.EntryFiles) > 0
}

func inferPrimaryStartupProject(projects []SolutionProject, solutionPaths []string) string {
	if len(projects) == 0 {
		return ""
	}
	candidates := []SolutionProject{}
	for _, project := range projects {
		if project.StartupCandidate {
			candidates = append(candidates, project)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	solutionBase := ""
	if len(solutionPaths) > 0 {
		solutionBase = strings.ToLower(strings.TrimSuffix(filepath.Base(solutionPaths[0]), filepath.Ext(solutionPaths[0])))
	}
	for _, project := range candidates {
		if strings.EqualFold(project.Name, solutionBase) || strings.EqualFold(filepath.Base(project.Directory), solutionBase) {
			return project.Name
		}
	}
	sort.SliceStable(candidates, func(i int, j int) bool {
		leftScore := len(candidates[i].EntryFiles)
		rightScore := len(candidates[j].EntryFiles)
		if leftScore == rightScore {
			return candidates[i].Name < candidates[j].Name
		}
		return leftScore > rightScore
	})
	return candidates[0].Name
}

func inferRuntimeEdges(snapshot ProjectSnapshot, projects []SolutionProject) []RuntimeEdge {
	edges := []RuntimeEdge{}
	seen := map[string]int{}
	projectByDir := map[string]string{}
	projectNames := []string{}
	for _, project := range projects {
		projectByDir[strings.ToLower(filepath.ToSlash(project.Directory))] = project.Name
		projectNames = append(projectNames, project.Name)
	}
	add := func(source string, target string, kind string, evidence string) {
		source = canonicalProjectName(strings.TrimSpace(source), projectNames)
		target = canonicalProjectName(strings.TrimSpace(target), projectNames)
		if source == "" || target == "" || strings.EqualFold(source, target) {
			return
		}
		key := strings.ToLower(source + "->" + target + ":" + kind)
		index, ok := seen[key]
		if !ok {
			edges = append(edges, RuntimeEdge{
				Source:     source,
				Target:     target,
				Kind:       kind,
				Confidence: runtimeEdgeConfidence(kind),
			})
			index = len(edges) - 1
			seen[key] = index
		}
		if strings.TrimSpace(evidence) != "" {
			edges[index].Evidence = analysisUniqueStrings(append(edges[index].Evidence, evidence))
		}
	}

	for _, project := range projects {
		for _, ref := range project.ProjectReferences {
			refName := projectNameFromReference(projects, ref)
			if refName != "" {
				add(project.Name, refName, "project_reference", project.Path)
			}
		}
	}

	for _, file := range snapshot.Files {
		source := projectNameForFile(file.Path, projectByDir)
		if source == "" {
			continue
		}
		abs := filepath.Join(snapshot.Root, filepath.FromSlash(file.Path))
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		content := string(data)
		lower := strings.ToLower(content)
		hasDynamicAPI := containsAny(lower, "loadlibrary", "loadlibrarya", "loadlibraryw", "getprocaddress")
		hasSpawnAPI := containsAny(lower, "createprocess", "createprocessa", "createprocessw", "shellexecute", "shellexecutea", "shellexecutew", "winexec")
		for _, projectName := range projectNames {
			if strings.EqualFold(projectName, source) {
				continue
			}
			projectLower := strings.ToLower(projectName)
			if !containsRuntimeProjectHint(lower, projectLower) {
				continue
			}
			switch {
			case hasDynamicAPI && containsDynamicLoadTarget(lower, projectLower):
				add(source, projectName, "dynamic_load", file.Path)
			case hasSpawnAPI && containsProcessSpawnTarget(lower, projectLower):
				add(source, projectName, "process_spawn", file.Path)
			case containsLooseProjectReference(lower, projectLower):
				add(source, projectName, "string_reference", file.Path)
			}
		}
	}

	sort.SliceStable(edges, func(i int, j int) bool {
		if edges[i].Source == edges[j].Source {
			if edges[i].Target == edges[j].Target {
				if edges[i].Confidence == edges[j].Confidence {
					return edges[i].Kind < edges[j].Kind
				}
				return runtimeEdgeConfidenceRank(edges[i].Confidence) > runtimeEdgeConfidenceRank(edges[j].Confidence)
			}
			return edges[i].Target < edges[j].Target
		}
		return edges[i].Source < edges[j].Source
	})
	return edges
}

func buildProjectEdges(snapshot ProjectSnapshot) []ProjectEdge {
	edges := []ProjectEdge{}
	seen := map[string]int{}
	add := func(source string, target string, edgeType string, confidence string, evidence string, attrs map[string]string) {
		source = strings.TrimSpace(source)
		target = strings.TrimSpace(target)
		if source == "" || target == "" || strings.EqualFold(source, target) {
			return
		}
		key := strings.ToLower(source + "->" + target + ":" + edgeType)
		index, ok := seen[key]
		if !ok {
			edge := ProjectEdge{
				Source:     source,
				Target:     target,
				Type:       edgeType,
				Confidence: confidence,
			}
			if len(attrs) > 0 {
				edge.Attributes = attrs
			}
			edges = append(edges, edge)
			index = len(edges) - 1
			seen[key] = index
		}
		if strings.TrimSpace(evidence) != "" {
			edges[index].Evidence = analysisUniqueStrings(append(edges[index].Evidence, evidence))
		}
		if len(attrs) > 0 {
			if edges[index].Attributes == nil {
				edges[index].Attributes = map[string]string{}
			}
			for k, v := range attrs {
				if strings.TrimSpace(v) != "" {
					edges[index].Attributes[k] = v
				}
			}
		}
	}

	for source, targets := range snapshot.ImportGraph {
		for _, target := range targets {
			targetFile, ok := snapshot.FilesByPath[target]
			if !ok {
				continue
			}
			edgeType := "dependency_edge"
			confidence := "high"
			if isExternalLikePath(strings.ToLower(target)) {
				edgeType = "external_edge"
				confidence = "medium"
			}
			add(source, target, edgeType, confidence, source, map[string]string{
				"source_dir": snapshot.FilesByPath[source].Directory,
				"target_dir": targetFile.Directory,
			})
		}
	}
	for _, edge := range snapshot.RuntimeEdges {
		add(edge.Source, edge.Target, "runtime_edge", edge.Confidence, firstString(edge.Evidence), map[string]string{
			"kind": edge.Kind,
		})
	}
	for _, project := range snapshot.UnrealProjects {
		for _, module := range project.Modules {
			add(project.Name, module, "module_edge", "high", project.Path, map[string]string{
				"kind": "uproject_module",
			})
		}
		for _, plugin := range project.Plugins {
			add(project.Name, plugin, "config_edge", "medium", project.Path, map[string]string{
				"kind": "enabled_plugin",
			})
		}
	}
	for _, plugin := range snapshot.UnrealPlugins {
		for _, module := range plugin.Modules {
			add(plugin.Name, module, "module_edge", "high", plugin.Path, map[string]string{
				"kind": "plugin_module",
			})
		}
	}
	for _, target := range snapshot.UnrealTargets {
		for _, module := range target.Modules {
			add(target.Name, module, "module_edge", "high", target.Path, map[string]string{
				"kind": "target_module",
				"type": target.TargetType,
			})
		}
	}
	for _, module := range snapshot.UnrealModules {
		for _, dep := range module.PublicDependencies {
			add(module.Name, dep, "module_edge", "high", module.Path, map[string]string{
				"kind": "public_dependency",
			})
		}
		for _, dep := range module.PrivateDependencies {
			add(module.Name, dep, "module_edge", "medium", module.Path, map[string]string{
				"kind": "private_dependency",
			})
		}
		for _, dep := range module.DynamicallyLoaded {
			add(module.Name, dep, "module_edge", "medium", module.Path, map[string]string{
				"kind": "dynamic_module",
			})
		}
	}
	for _, item := range snapshot.UnrealTypes {
		if strings.TrimSpace(item.BaseClass) != "" {
			add(item.Name, item.BaseClass, "reflection_edge", "high", item.File, map[string]string{
				"kind":   item.Kind,
				"module": item.Module,
			})
		}
		if strings.TrimSpace(item.Module) != "" {
			add(item.Module, item.Name, "gameplay_edge", "medium", item.File, map[string]string{
				"kind": "module_type",
				"role": item.GameplayRole,
			})
		}
	}
	for _, edge := range buildUnrealGameplayEdges(snapshot.UnrealTypes) {
		add(edge.Source, edge.Target, edge.Type, edge.Confidence, firstString(edge.Evidence), edge.Attributes)
	}
	for _, surface := range snapshot.UnrealNetwork {
		typeName := strings.TrimSpace(surface.TypeName)
		if typeName == "" {
			typeName = filepath.Base(surface.File)
		}
		for _, rpc := range surface.ServerRPCs {
			add(typeName, rpc, "rpc_edge", "high", surface.File, map[string]string{
				"kind":      "server_rpc",
				"direction": "client_to_server",
			})
		}
		for _, rpc := range surface.ClientRPCs {
			add(typeName, rpc, "rpc_edge", "high", surface.File, map[string]string{
				"kind":      "client_rpc",
				"direction": "server_to_client",
			})
		}
		for _, rpc := range surface.MulticastRPCs {
			add(typeName, rpc, "rpc_edge", "high", surface.File, map[string]string{
				"kind":      "multicast_rpc",
				"direction": "server_to_all",
			})
		}
		for _, prop := range surface.ReplicatedProperties {
			add(typeName, prop, "gameplay_edge", "medium", surface.File, map[string]string{
				"kind": "replicated_property",
			})
		}
		for _, prop := range surface.RepNotifyProperties {
			add(typeName, prop, "gameplay_edge", "medium", surface.File, map[string]string{
				"kind": "rep_notify_property",
			})
		}
		if surface.HasReplicationList {
			add(typeName, "GetLifetimeReplicatedProps", "config_edge", "medium", surface.File, map[string]string{
				"kind": "replication_registration",
			})
		}
	}
	for _, asset := range snapshot.UnrealAssets {
		source := firstNonBlankAnalysisString(asset.OwnerName, firstNonBlankAnalysisString(asset.Module, asset.File))
		for _, path := range asset.AssetPaths {
			add(source, path, "asset_edge", "medium", asset.File, map[string]string{
				"kind": "asset_reference",
			})
		}
		for _, target := range asset.CanonicalTargets {
			add(source, target, "gameplay_edge", "medium", asset.File, map[string]string{
				"kind": "blueprint_asset_binding",
			})
		}
		for _, key := range asset.ConfigKeys {
			add(source, key, "config_edge", "medium", asset.File, map[string]string{
				"kind": "config_binding",
			})
		}
	}
	for _, system := range snapshot.UnrealSystems {
		source := firstNonBlankAnalysisString(system.OwnerName, firstNonBlankAnalysisString(system.Module, system.File))
		target := firstNonBlankAnalysisString(system.System, "unreal_system")
		add(source, target, "gameplay_edge", "medium", system.File, map[string]string{
			"kind": "gameplay_system",
		})
		for _, function := range system.Functions {
			add(source, function, "gameplay_edge", "medium", system.File, map[string]string{
				"kind":   "system_function",
				"system": system.System,
			})
		}
		for _, asset := range system.Assets {
			add(source, asset, "asset_edge", "medium", system.File, map[string]string{
				"kind":   "system_asset",
				"system": system.System,
			})
		}
		for _, owner := range system.OwnedBy {
			add(owner, source, "gameplay_edge", "medium", system.File, map[string]string{
				"kind":   "system_owner",
				"system": system.System,
			})
		}
		for _, action := range system.Actions {
			add(source, action, "gameplay_edge", "medium", system.File, map[string]string{
				"kind":   "input_action",
				"system": system.System,
			})
		}
		for _, context := range system.Contexts {
			add(source, context, "gameplay_edge", "medium", system.File, map[string]string{
				"kind":   "input_context",
				"system": system.System,
			})
		}
		for _, widget := range system.Widgets {
			add(source, widget, "gameplay_edge", "medium", system.File, map[string]string{
				"kind":   "widget_target",
				"system": system.System,
			})
		}
		for _, ability := range system.Abilities {
			add(source, ability, "gameplay_edge", "medium", system.File, map[string]string{
				"kind":   "ability_target",
				"system": system.System,
			})
		}
		for _, effect := range system.Effects {
			add(source, effect, "gameplay_edge", "medium", system.File, map[string]string{
				"kind":   "effect_target",
				"system": system.System,
			})
		}
		for _, attribute := range system.Attributes {
			add(source, attribute, "gameplay_edge", "medium", system.File, map[string]string{
				"kind":   "attribute_target",
				"system": system.System,
			})
		}
	}
	for _, setting := range snapshot.UnrealSettings {
		source := firstNonBlankAnalysisString(snapshot.PrimaryUnrealModule, setting.SourceFile)
		if setting.GameDefaultMap != "" {
			add(source, setting.GameDefaultMap, "config_edge", "high", setting.SourceFile, map[string]string{
				"kind": "game_default_map",
			})
		}
		if setting.EditorStartupMap != "" {
			add(source, setting.EditorStartupMap, "config_edge", "medium", setting.SourceFile, map[string]string{
				"kind": "editor_startup_map",
			})
		}
		if setting.GlobalDefaultGameMode != "" {
			add(source, setting.GlobalDefaultGameMode, "gameplay_edge", "high", setting.SourceFile, map[string]string{
				"kind": "global_default_game_mode",
			})
			if target := canonicalizeBlueprintAssetClass(setting.GlobalDefaultGameMode); target != "" {
				add(source, target, "gameplay_edge", "high", setting.SourceFile, map[string]string{
					"kind": "global_default_game_mode_canonical",
				})
			}
		}
		if setting.GameInstanceClass != "" {
			add(source, setting.GameInstanceClass, "gameplay_edge", "high", setting.SourceFile, map[string]string{
				"kind": "game_instance_setting",
			})
			if target := canonicalizeBlueprintAssetClass(setting.GameInstanceClass); target != "" {
				add(source, target, "gameplay_edge", "high", setting.SourceFile, map[string]string{
					"kind": "game_instance_setting_canonical",
				})
			}
		}
		if setting.DefaultPawnClass != "" {
			add(source, setting.DefaultPawnClass, "gameplay_edge", "medium", setting.SourceFile, map[string]string{
				"kind": "default_pawn_setting",
			})
			if target := canonicalizeBlueprintAssetClass(setting.DefaultPawnClass); target != "" {
				add(source, target, "gameplay_edge", "medium", setting.SourceFile, map[string]string{
					"kind": "default_pawn_setting_canonical",
				})
			}
		}
		if setting.PlayerControllerClass != "" {
			add(source, setting.PlayerControllerClass, "gameplay_edge", "medium", setting.SourceFile, map[string]string{
				"kind": "player_controller_setting",
			})
			if target := canonicalizeBlueprintAssetClass(setting.PlayerControllerClass); target != "" {
				add(source, target, "gameplay_edge", "medium", setting.SourceFile, map[string]string{
					"kind": "player_controller_setting_canonical",
				})
			}
		}
		if setting.HUDClass != "" {
			add(source, setting.HUDClass, "gameplay_edge", "medium", setting.SourceFile, map[string]string{
				"kind": "hud_setting",
			})
			if target := canonicalizeBlueprintAssetClass(setting.HUDClass); target != "" {
				add(source, target, "gameplay_edge", "medium", setting.SourceFile, map[string]string{
					"kind": "hud_setting_canonical",
				})
			}
		}
	}
	for _, manifest := range snapshot.ManifestFiles {
		if strings.TrimSpace(snapshot.PrimaryStartup) != "" {
			add(snapshot.PrimaryStartup, manifest, "config_edge", "medium", manifest, nil)
		}
		if strings.TrimSpace(snapshot.PrimaryUnrealModule) != "" && (strings.HasSuffix(strings.ToLower(manifest), ".uproject") || strings.HasSuffix(strings.ToLower(manifest), ".uplugin") || strings.HasSuffix(strings.ToLower(manifest), ".target.cs") || strings.HasSuffix(strings.ToLower(manifest), ".build.cs")) {
			add(snapshot.PrimaryUnrealModule, manifest, "config_edge", "medium", manifest, map[string]string{
				"kind": "unreal_metadata",
			})
		}
	}
	sort.SliceStable(edges, func(i int, j int) bool {
		if edges[i].Type == edges[j].Type {
			if edges[i].Source == edges[j].Source {
				return edges[i].Target < edges[j].Target
			}
			return edges[i].Source < edges[j].Source
		}
		return edges[i].Type < edges[j].Type
	})
	return edges
}

func canonicalProjectName(name string, projectNames []string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	for _, candidate := range projectNames {
		if strings.EqualFold(candidate, name) {
			return candidate
		}
	}
	normalized := normalizeProjectToken(name)
	if normalized == "" {
		return name
	}
	best := name
	bestDistance := 99
	for _, candidate := range projectNames {
		if normalizeProjectToken(candidate) == normalized {
			return candidate
		}
		distance := levenshteinDistance(normalized, normalizeProjectToken(candidate))
		if distance < bestDistance {
			bestDistance = distance
			best = candidate
		}
	}
	if bestDistance <= 2 {
		return best
	}
	return name
}

func normalizeProjectToken(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func levenshteinDistance(left string, right string) int {
	if left == right {
		return 0
	}
	if left == "" {
		return len(right)
	}
	if right == "" {
		return len(left)
	}
	prev := make([]int, len(right)+1)
	curr := make([]int, len(right)+1)
	for j := 0; j <= len(right); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(left); i++ {
		curr[0] = i
		for j := 1; j <= len(right); j++ {
			cost := 0
			if left[i-1] != right[j-1] {
				cost = 1
			}
			curr[j] = min3(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
		}
		copy(prev, curr)
	}
	return prev[len(right)]
}

func min3(a int, b int, c int) int {
	if a <= b && a <= c {
		return a
	}
	if b <= a && b <= c {
		return b
	}
	return c
}

func runtimeEdgeConfidence(kind string) string {
	switch strings.TrimSpace(kind) {
	case "project_reference", "dynamic_load", "process_spawn":
		return "high"
	case "string_reference":
		return "low"
	default:
		return "medium"
	}
}

func runtimeEdgeConfidenceRank(confidence string) int {
	switch strings.TrimSpace(strings.ToLower(confidence)) {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func highConfidenceRuntimeEdges(edges []RuntimeEdge) []RuntimeEdge {
	out := make([]RuntimeEdge, 0, len(edges))
	for _, edge := range edges {
		if runtimeEdgeConfidenceRank(edge.Confidence) >= runtimeEdgeConfidenceRank("high") {
			out = append(out, edge)
		}
	}
	return out
}

func operationalCandidateRuntimeEdges(edges []RuntimeEdge) []RuntimeEdge {
	out := []RuntimeEdge{}
	for _, edge := range edges {
		if edge.Kind == "dynamic_load" || edge.Kind == "process_spawn" || edge.Kind == "project_reference" || edge.Kind == "orchestration_edge" || edge.Kind == "collaboration_edge" {
			out = append(out, edge)
		}
	}
	return out
}

func containsRuntimeProjectHint(lower string, projectLower string) bool {
	if strings.TrimSpace(projectLower) == "" {
		return false
	}
	return containsDynamicLoadTarget(lower, projectLower) || containsProcessSpawnTarget(lower, projectLower) || containsLooseProjectReference(lower, projectLower)
}

func containsDynamicLoadTarget(lower string, projectLower string) bool {
	return containsAny(
		lower,
		projectLower+".dll",
		projectLower+".bin",
		projectLower+".ocx",
		"\\"+projectLower+".dll",
		"/"+projectLower+".dll",
	)
}

func containsProcessSpawnTarget(lower string, projectLower string) bool {
	return containsAny(
		lower,
		projectLower+".exe",
		projectLower+".bin",
		"\\"+projectLower+".exe",
		"/"+projectLower+".exe",
		"\""+projectLower+".exe",
	)
}

func containsLooseProjectReference(lower string, projectLower string) bool {
	return containsAny(
		lower,
		"\""+projectLower+"\"",
		"'"+projectLower+"'",
		"\\"+projectLower+"\\",
		"/"+projectLower+"/",
		projectLower+".vcxproj",
		projectLower+".dll",
		projectLower+".exe",
		projectLower+".bin",
	)
}

func projectNameFromReference(projects []SolutionProject, ref string) string {
	lowerRef := strings.ToLower(filepath.ToSlash(ref))
	for _, project := range projects {
		if strings.EqualFold(filepath.ToSlash(project.Path), lowerRef) {
			return project.Name
		}
		if strings.EqualFold(filepath.ToSlash(filepath.Base(project.Path)), filepath.Base(lowerRef)) {
			return project.Name
		}
	}
	return ""
}

func projectNameForFile(path string, byDir map[string]string) string {
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	best := ""
	bestLen := -1
	for dir, name := range byDir {
		prefix := dir
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		if dir == "" {
			continue
		}
		if strings.HasPrefix(lowerPath, prefix) && len(prefix) > bestLen {
			best = name
			bestLen = len(prefix)
		}
	}
	return best
}

func (a *projectAnalyzer) planShards(snapshot ProjectSnapshot, desiredShards int) []AnalysisShard {
	if rootFiles, ok := snapshot.FilesByDirectory[""]; ok && len(rootFiles) >= 12 {
		rootSubShards := a.planRootSubsystemShards(snapshot, rootFiles)
		if len(rootSubShards) >= 2 {
			otherShards := a.planNonRootDirectoryShards(snapshot)
			shards := make([]AnalysisShard, 0, len(rootSubShards)+len(otherShards))
			for _, shard := range rootSubShards {
				shard.ID = fmt.Sprintf("shard-%02d", len(shards)+1)
				a.finalizeShard(snapshot, &shard, 10)
				shards = append(shards, shard)
			}
			for _, shard := range otherShards {
				shard.ID = fmt.Sprintf("shard-%02d", len(shards)+1)
				a.finalizeShard(snapshot, &shard, 10)
				shards = append(shards, shard)
			}
			return shards
		}
	}

	if semanticShards := a.planSemanticShards(snapshot, desiredShards); len(semanticShards) > 0 {
		shards := make([]AnalysisShard, 0, len(semanticShards))
		for _, shard := range semanticShards {
			shard.ID = fmt.Sprintf("shard-%02d", len(shards)+1)
			a.finalizeShard(snapshot, &shard, 12)
			shards = append(shards, shard)
		}
		if len(shards) > 0 {
			maxTotalShards := a.analysisCfg.MaxTotalShards
			if maxTotalShards <= 0 {
				maxTotalShards = analysisMaxInt(desiredShards, 1)
			}
			if len(shards) > maxTotalShards {
				shards = mergeSemanticShardsByPriority(shards, maxTotalShards, snapshot.AnalysisMode)
			}
			sort.Slice(shards, func(i int, j int) bool {
				leftPriority := semanticShardPriority(shards[i].Name, snapshot.AnalysisMode)
				rightPriority := semanticShardPriority(shards[j].Name, snapshot.AnalysisMode)
				if leftPriority == rightPriority {
					return shards[i].ID < shards[j].ID
				}
				return leftPriority < rightPriority
			})
			return shards
		}
	}

	clusters := a.planDirectoryClusters(snapshot, desiredShards)
	if len(clusters) > 0 {
		shards := make([]AnalysisShard, 0, len(clusters))
		for _, cluster := range clusters {
			fileChunks := a.collectClusterFileChunks(snapshot, cluster)
			if len(fileChunks) == 0 {
				continue
			}
			for chunkIndex, files := range fileChunks {
				shard := AnalysisShard{
					ID:             fmt.Sprintf("shard-%02d", len(shards)+1),
					Name:           shardName(clusterName(cluster), chunkIndex, len(fileChunks)),
					PrimaryFiles:   filesToPaths(files),
					EstimatedFiles: len(files),
					EstimatedLines: sumLines(files),
				}
				a.finalizeShard(snapshot, &shard, 12)
				shards = append(shards, shard)
			}
		}
		if len(shards) > 0 {
			return shards
		}
	}

	type bucket struct {
		Name  string
		Files []ScannedFile
		Lines int
	}

	buckets := []bucket{}
	for dir, files := range snapshot.FilesByDirectory {
		lines := 0
		for _, file := range files {
			lines += file.LineCount
		}
		buckets = append(buckets, bucket{Name: dir, Files: append([]ScannedFile(nil), files...), Lines: lines})
	}
	sort.Slice(buckets, func(i int, j int) bool {
		if buckets[i].Lines == buckets[j].Lines {
			return buckets[i].Name < buckets[j].Name
		}
		return buckets[i].Lines > buckets[j].Lines
	})

	shards := []AnalysisShard{}
	for _, bucket := range buckets {
		if bucket.Name == "" || bucket.Name == "." {
			rootShards := a.planRootSubsystemShards(snapshot, bucket.Files)
			if len(rootShards) > 0 {
				for _, shard := range rootShards {
					shard.ID = fmt.Sprintf("shard-%02d", len(shards)+1)
					a.finalizeShard(snapshot, &shard, 10)
					shards = append(shards, shard)
				}
				continue
			}
		}
		chunks := chunkFiles(bucket.Files, a.analysisCfg.MaxFilesPerShard, a.analysisCfg.MaxLinesPerShard)
		for index, chunk := range chunks {
			shard := AnalysisShard{
				ID:             fmt.Sprintf("shard-%02d", len(shards)+1),
				Name:           shardName(bucket.Name, index, len(chunks)),
				PrimaryFiles:   filesToPaths(chunk),
				EstimatedFiles: len(chunk),
				EstimatedLines: sumLines(chunk),
			}
			a.finalizeShard(snapshot, &shard, 10)
			shards = append(shards, shard)
		}
	}
	maxTotalShards := a.analysisCfg.MaxTotalShards
	if maxTotalShards <= 0 {
		maxTotalShards = analysisMaxInt(desiredShards, 1)
	}
	if len(shards) > maxTotalShards {
		if hasNamedRootSubsystemShards(shards) {
			maxTotalShards = analysisMaxInt(maxTotalShards, len(shards))
		} else {
			shards = mergeShards(shards, maxTotalShards)
		}
	}
	sort.Slice(shards, func(i int, j int) bool {
		return shards[i].ID < shards[j].ID
	})
	return shards
}

func (a *projectAnalyzer) planNonRootDirectoryShards(snapshot ProjectSnapshot) []AnalysisShard {
	type bucket struct {
		Name  string
		Files []ScannedFile
		Lines int
	}

	buckets := []bucket{}
	for dir, files := range snapshot.FilesByDirectory {
		if dir == "" || dir == "." {
			continue
		}
		lines := 0
		for _, file := range files {
			lines += file.LineCount
		}
		buckets = append(buckets, bucket{Name: dir, Files: append([]ScannedFile(nil), files...), Lines: lines})
	}
	sort.Slice(buckets, func(i int, j int) bool {
		if buckets[i].Lines == buckets[j].Lines {
			return buckets[i].Name < buckets[j].Name
		}
		return buckets[i].Lines > buckets[j].Lines
	})

	shards := []AnalysisShard{}
	for _, bucket := range buckets {
		chunks := chunkFiles(bucket.Files, a.analysisCfg.MaxFilesPerShard, a.analysisCfg.MaxLinesPerShard)
		for index, chunk := range chunks {
			shards = append(shards, AnalysisShard{
				Name:           shardName(bucket.Name, index, len(chunks)),
				PrimaryFiles:   filesToPaths(chunk),
				EstimatedFiles: len(chunk),
				EstimatedLines: sumLines(chunk),
			})
		}
	}
	return shards
}

func (a *projectAnalyzer) planDirectoryClusters(snapshot ProjectSnapshot, desiredAgents int) [][]string {
	dirLines := map[string]int{}
	dirCoupling := map[string]map[string]int{}
	dirs := []string{}
	for dir, files := range snapshot.FilesByDirectory {
		if len(files) == 0 {
			continue
		}
		dirs = append(dirs, dir)
		for _, file := range files {
			dirLines[dir] += file.LineCount
			for _, dep := range file.Imports {
				target, ok := snapshot.FilesByPath[dep]
				if !ok {
					continue
				}
				if target.Directory == dir {
					continue
				}
				if dirCoupling[dir] == nil {
					dirCoupling[dir] = map[string]int{}
				}
				if dirCoupling[target.Directory] == nil {
					dirCoupling[target.Directory] = map[string]int{}
				}
				dirCoupling[dir][target.Directory]++
				dirCoupling[target.Directory][dir]++
			}
		}
	}
	sort.Slice(dirs, func(i int, j int) bool {
		if dirLines[dirs[i]] == dirLines[dirs[j]] {
			return dirs[i] < dirs[j]
		}
		return dirLines[dirs[i]] > dirLines[dirs[j]]
	})
	if len(dirs) == 0 {
		return nil
	}

	targetCount := desiredAgents
	if targetCount < 1 {
		targetCount = 1
	}
	targetLines := snapshot.TotalLines / targetCount
	if targetLines < 1 {
		targetLines = a.analysisCfg.MaxLinesPerShard
	}
	assigned := map[string]struct{}{}
	clusters := [][]string{}

	for _, seed := range dirs {
		if _, ok := assigned[seed]; ok {
			continue
		}
		cluster := []string{seed}
		assigned[seed] = struct{}{}
		clusterLines := dirLines[seed]
		for len(cluster) < 8 {
			nextDir := ""
			nextScore := 0
			for _, current := range cluster {
				for candidate, score := range dirCoupling[current] {
					if _, ok := assigned[candidate]; ok {
						continue
					}
					if score > nextScore || (score == nextScore && nextDir != "" && candidate < nextDir) {
						nextDir = candidate
						nextScore = score
					}
				}
			}
			if nextDir == "" {
				break
			}
			if clusterLines >= targetLines && nextScore == 0 && len(clusters)+1 < targetCount {
				break
			}
			cluster = append(cluster, nextDir)
			assigned[nextDir] = struct{}{}
			clusterLines += dirLines[nextDir]
		}
		clusters = append(clusters, cluster)
	}

	if len(clusters) > targetCount {
		clusters = mergeDirectoryClusters(snapshot, clusters, targetCount)
	}
	return clusters
}

func (a *projectAnalyzer) collectClusterFileChunks(snapshot ProjectSnapshot, dirs []string) [][]ScannedFile {
	files := []ScannedFile{}
	for _, dir := range dirs {
		files = append(files, snapshot.FilesByDirectory[dir]...)
	}
	sort.Slice(files, func(i int, j int) bool {
		return files[i].Path < files[j].Path
	})
	if len(files) == 0 {
		return nil
	}
	if len(files) <= a.analysisCfg.MaxFilesPerShard && sumLines(files) <= a.analysisCfg.MaxLinesPerShard {
		return [][]ScannedFile{files}
	}
	return chunkFiles(files, a.analysisCfg.MaxFilesPerShard, a.analysisCfg.MaxLinesPerShard)
}

func (a *projectAnalyzer) planRootSubsystemShards(snapshot ProjectSnapshot, files []ScannedFile) []AnalysisShard {
	if len(files) < 12 {
		return nil
	}
	groups := map[string][]ScannedFile{
		"runtime":                {},
		"verification":           {},
		"hooks_policy":           {},
		"evidence_investigation": {},
		"memory_context":         {},
		"ui_viewer":              {},
		"commands":               {},
		"analysis_engine":        {},
		"platform_io":            {},
		"docs_specs":             {},
		"project_manifest":       {},
		"ops_scripts":            {},
		"error_artifacts":        {},
		"root_tests_misc":        {},
		"misc_root":              {},
	}

	for _, file := range files {
		name := strings.ToLower(file.Path)
		switch {
		case strings.HasPrefix(name, "main.go") || strings.HasPrefix(name, "agent.go") || strings.HasPrefix(name, "session.go") || strings.HasPrefix(name, "provider.go") || strings.HasPrefix(name, "completion.go") || strings.HasPrefix(name, "config.go") || strings.HasPrefix(name, "scout.go"):
			groups["runtime"] = append(groups["runtime"], file)
		case strings.Contains(name, "verify") || strings.Contains(name, "checkpoint"):
			groups["verification"] = append(groups["verification"], file)
		case strings.Contains(name, "hook") || strings.Contains(name, "override"):
			groups["hooks_policy"] = append(groups["hooks_policy"], file)
		case strings.Contains(name, "evidence") || strings.Contains(name, "investigation") || strings.Contains(name, "simulate"):
			groups["evidence_investigation"] = append(groups["evidence_investigation"], file)
		case strings.Contains(name, "memory") || strings.Contains(name, "skill") || strings.Contains(name, "mcp"):
			groups["memory_context"] = append(groups["memory_context"], file)
		case strings.Contains(name, "ui") || strings.Contains(name, "viewer") || strings.Contains(name, "preview") || strings.Contains(name, "selection"):
			groups["ui_viewer"] = append(groups["ui_viewer"], file)
		case strings.HasPrefix(name, "commands_"):
			groups["commands"] = append(groups["commands"], file)
		case strings.HasPrefix(name, "analysis_") || strings.HasPrefix(name, "plan_"):
			groups["analysis_engine"] = append(groups["analysis_engine"], file)
		case strings.Contains(name, "input_") || strings.Contains(name, "cancel") || strings.Contains(name, "openurl") || strings.Contains(name, "atomicfile") || strings.Contains(name, "storage_atomic"):
			groups["platform_io"] = append(groups["platform_io"], file)
		case name == "go.mod" || name == "go.sum" || strings.HasSuffix(name, ".mod") || strings.HasSuffix(name, ".sum"):
			groups["project_manifest"] = append(groups["project_manifest"], file)
		case strings.HasSuffix(name, ".ps1") || strings.HasSuffix(name, ".bat") || strings.HasSuffix(name, ".cmd"):
			groups["ops_scripts"] = append(groups["ops_scripts"], file)
		case strings.HasSuffix(name, "error.txt") || strings.Contains(name, "panic") || strings.Contains(name, "crash"):
			groups["error_artifacts"] = append(groups["error_artifacts"], file)
		case strings.HasSuffix(name, ".md") || name == "license" || strings.HasSuffix(name, ".txt"):
			groups["docs_specs"] = append(groups["docs_specs"], file)
		case strings.HasSuffix(name, "_test.go") || strings.Contains(name, "test"):
			groups["root_tests_misc"] = append(groups["root_tests_misc"], file)
		default:
			groups["misc_root"] = append(groups["misc_root"], file)
		}
	}

	order := []string{"runtime", "commands", "verification", "hooks_policy", "evidence_investigation", "memory_context", "ui_viewer", "analysis_engine", "platform_io", "docs_specs", "project_manifest", "ops_scripts", "error_artifacts", "root_tests_misc", "misc_root"}
	shards := []AnalysisShard{}
	for _, key := range order {
		groupFiles := groups[key]
		if len(groupFiles) == 0 {
			continue
		}
		chunks := chunkFiles(groupFiles, a.analysisCfg.MaxFilesPerShard, a.analysisCfg.MaxLinesPerShard)
		for index, chunk := range chunks {
			shards = append(shards, AnalysisShard{
				Name:           shardName(key, index, len(chunks)),
				PrimaryFiles:   filesToPaths(chunk),
				EstimatedFiles: len(chunk),
				EstimatedLines: sumLines(chunk),
			})
		}
	}
	return shards
}

func (a *projectAnalyzer) executeShards(ctx context.Context, snapshot ProjectSnapshot, shards []AnalysisShard, goal string, previousRun *ProjectAnalysisRun, reuseState analysisReuseState, concurrency int) ([]WorkerReport, []ReviewDecision, error) {
	reports := make([]WorkerReport, len(shards))
	reviews := make([]ReviewDecision, len(shards))
	concurrency = analysisMinInt(len(shards), concurrency)
	if concurrency < 1 {
		concurrency = 1
	}
	totalWaves := ceilDiv(len(shards), concurrency)
	for wave := 0; wave < totalWaves; wave++ {
		start := wave * concurrency
		end := analysisMinInt(len(shards), start+concurrency)
		a.debug(fmt.Sprintf("starting shard wave %d/%d: shards=%d", wave+1, totalWaves, end-start))
		errCh := make(chan error, end-start)
		var wg sync.WaitGroup
		for index := start; index < end; index++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				report, review, shard, err := a.executeShard(ctx, snapshot, shards[i], goal, previousRun, reuseState)
				if err != nil {
					errCh <- err
					return
				}
				shards[i] = shard
				reports[i] = report
				reviews[i] = review
			}(index)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			if err != nil {
				return nil, nil, err
			}
		}
	}
	return reports, reviews, nil
}

func (a *projectAnalyzer) executeShard(ctx context.Context, snapshot ProjectSnapshot, shard AnalysisShard, goal string, previousRun *ProjectAnalysisRun, reuseState analysisReuseState) (WorkerReport, ReviewDecision, AnalysisShard, error) {
	a.debug(fmt.Sprintf("shard %s queued: files=%d refs=%d", shard.Name, len(shard.PrimaryFiles), len(shard.ReferenceFiles)))
	report, review, reason, ok := a.tryReuseShard(previousRun, shard, reuseState)
	if ok {
		shard.CacheStatus = "reused"
		shard.InvalidationReason = reason
		a.debug(fmt.Sprintf("shard %s cache hit: reason=%s", shard.Name, reason))
		return report, review, shard, nil
	}
	shard.CacheStatus = "miss"
	if strings.TrimSpace(reason) != "" {
		shard.InvalidationReason = reason
	} else if _, ok := reuseState.previousByPrimaryKey[primaryFilesKey(shard.PrimaryFiles)]; !ok {
		shard.InvalidationReason = "new"
	} else {
		shard.InvalidationReason = "recomputed"
	}
	if previousShard, ok := a.previousShardForPrimary(previousRun, shard, reuseState); ok {
		shard.InvalidationChanges = buildInvalidationChanges(
			previousRun.Snapshot,
			snapshot,
			[]string{shard.Name},
			previousShard.PrimaryFiles,
			shard.PrimaryFiles,
			4,
		)
		shard.InvalidationDiff = buildInvalidationDiffStrings(shard.InvalidationChanges, 4)
		if len(shard.InvalidationDiff) == 0 {
			shard.InvalidationDiff = buildInvalidationDiffLines(
				previousRun.Snapshot,
				snapshot,
				[]string{shard.Name},
				previousShard.PrimaryFiles,
				shard.PrimaryFiles,
				[]string{shard.InvalidationReason},
				4,
			)
		}
	}
	a.debug(fmt.Sprintf("shard %s cache miss: reason=%s", shard.Name, shard.InvalidationReason))
	revisionPrompt := ""
	lastReport := WorkerReport{}
	lastReview := ReviewDecision{}
	for attempt := 0; attempt <= a.analysisCfg.MaxRevisionRounds; attempt++ {
		a.debug(fmt.Sprintf("worker start: shard=%s attempt=%d model=%s", shard.Name, attempt+1, a.workerModel()))
		report, err := a.runWorker(ctx, snapshot, shard, goal, revisionPrompt)
		if err != nil {
			a.debug(fmt.Sprintf("worker error: shard=%s attempt=%d error=%v", shard.Name, attempt+1, err))
			if ctx.Err() != nil {
				return WorkerReport{}, ReviewDecision{}, shard, err
			}
			failedReport := softFailWorkerReport(shard, err)
			failedReview := softFailWorkerReviewDecision(shard, err)
			a.debug(fmt.Sprintf("worker soft-failed: shard=%s status=%s", shard.Name, failedReview.Status))
			return failedReport, failedReview, shard, nil
		}
		a.debug(fmt.Sprintf("worker done: shard=%s attempt=%d evidence=%d responsibilities=%d", shard.Name, attempt+1, len(report.EvidenceFiles), len(report.Responsibilities)))
		if lintIssues := lintWorkerReportForRootCause(snapshot, shard, report); len(lintIssues) > 0 {
			if attempt < a.analysisCfg.MaxRevisionRounds {
				lastReport = report
				lastReview = ReviewDecision{
					Status:         "needs_revision",
					Issues:         lintIssues,
					RevisionPrompt: buildRootCauseWorkerLintRevisionPrompt(lintIssues),
					FailureKind:    analysisReviewIssueQuality,
				}
				revisionPrompt = lastReview.RevisionPrompt
				a.debug(fmt.Sprintf("worker root-cause lint requested revision: shard=%s issues=%d", shard.Name, len(lintIssues)))
				continue
			}
			report.Unknowns = analysisUniqueStrings(append(report.Unknowns, "Root-cause worker lint issues remained after revisions: "+strings.Join(limitStrings(lintIssues, 4), " | ")))
		}
		a.debug(fmt.Sprintf("reviewer start: shard=%s attempt=%d model=%s", shard.Name, attempt+1, a.reviewerModel()))
		review, err := a.reviewReport(ctx, snapshot, shard, report, goal, previousRun, reuseState)
		if err != nil {
			a.debug(fmt.Sprintf("reviewer error: shard=%s attempt=%d error=%v", shard.Name, attempt+1, err))
			failed := softFailReviewDecision(shard, report, err)
			a.debug(fmt.Sprintf("reviewer soft-failed: shard=%s status=%s", shard.Name, failed.Status))
			return report, failed, shard, nil
		}
		a.debug(fmt.Sprintf("reviewer done: shard=%s attempt=%d status=%s issues=%d", shard.Name, attempt+1, review.Status, len(review.Issues)))
		lastReport = report
		lastReview = review
		if strings.EqualFold(review.Status, "approved") {
			a.debug(fmt.Sprintf("shard approved: %s", shard.Name))
			return report, review, shard, nil
		}
		revisionPrompt = buildWorkerRevisionPromptFromReview(review)
		if revisionPrompt == "" {
			a.debug(fmt.Sprintf("shard %s review requested revision without prompt; stopping retries", shard.Name))
			break
		}
		a.debug(fmt.Sprintf("shard revision requested: %s", shard.Name))
	}
	return lastReport, lastReview, shard, nil
}

func buildWorkerRevisionPromptFromReview(review ReviewDecision) string {
	var b strings.Builder
	addText := func(label string, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		fmt.Fprintf(&b, "%s:\n%s\n\n", label, value)
	}
	addList := func(label string, items []string) {
		items = limitStrings(analysisUniqueStrings(items), 12)
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s:\n", label)
		for _, item := range items {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}

	addText("Reviewer revision prompt", review.RevisionPrompt)
	addList("Reviewer issues", review.Issues)
	addText("Failure kind", review.FailureKind)
	addText("Symptom possible", review.SymptomPossible)
	addList("Symptom causality", review.SymptomCausality)
	addList("Symptom reproduction bridge", review.SymptomReproductionBridge)
	addList("Required runtime observation", review.RequiredRuntimeObservation)
	addList("Disqualifying evidence", review.DisqualifyingEvidence)
	addList("Causal chain stages already supported", review.CausalChainStages)
	addList("Causal chain stages still missing", review.CausalChainMissing)
	if review.Disconfirmed {
		addText("Disconfirmed", "true")
	}
	addList("Disconfirming evidence", review.DisconfirmingEvidence)
	addList("Rejected candidates", review.RejectedCandidates)
	if len(review.EvidenceRequests) > 0 {
		b.WriteString("Evidence requests:\n")
		for _, request := range limitRootCauseEvidenceRequests(review.EvidenceRequests, 8) {
			requestText := strings.TrimSpace(request.Request)
			if requestText == "" {
				requestText = "Inspect the requested cross-shard evidence."
			}
			fmt.Fprintf(&b, "- Request: %s\n", requestText)
			if len(request.TargetSignals) > 0 {
				fmt.Fprintf(&b, "  Target signals: %s\n", strings.Join(limitStrings(request.TargetSignals, 6), " | "))
			}
			if len(request.TargetFiles) > 0 {
				fmt.Fprintf(&b, "  Target files: %s\n", strings.Join(limitStrings(request.TargetFiles, 6), " | "))
			}
			if strings.TrimSpace(request.Reason) != "" {
				fmt.Fprintf(&b, "  Reason: %s\n", strings.TrimSpace(request.Reason))
			}
			if strings.TrimSpace(request.RequiredToProve) != "" {
				fmt.Fprintf(&b, "  Required to prove: %s\n", strings.TrimSpace(request.RequiredToProve))
			}
		}
		b.WriteString("\n")
	}

	if strings.TrimSpace(b.String()) == "" && strings.EqualFold(review.Status, "needs_revision") {
		b.WriteString("Reviewer requested revision but did not provide structured details. Re-check the report against the reviewer system requirements, fill missing concrete evidence, and return a stricter grounded JSON report.\n")
	}
	return strings.TrimSpace(b.String())
}

type refinementCandidate struct {
	Index     int
	Score     int
	Important int
	Chunks    [][]ScannedFile
}

func (a *projectAnalyzer) planRefinementShards(snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, reviews []ReviewDecision) ([]AnalysisShard, map[string]struct{}) {
	if a.analysisCfg.MaxRefinementShards <= 0 || len(shards) == 0 {
		return nil, nil
	}
	topImportant := map[string]struct{}{}
	for _, path := range topImportantFilePaths(snapshot, 24) {
		topImportant[path] = struct{}{}
	}
	lensSet := map[string]struct{}{}
	for _, lens := range snapshot.AnalysisLenses {
		lensSet[strings.TrimSpace(lens.Type)] = struct{}{}
	}
	candidates := []refinementCandidate{}
	for index, shard := range shards {
		chunks := a.refinementChunks(snapshot, shard)
		if len(chunks) < 2 {
			continue
		}
		score, important := scoreRefinementCandidate(shard, reports[index], reviews[index], topImportant, lensSet)
		if score <= 0 {
			continue
		}
		candidates = append(candidates, refinementCandidate{
			Index:     index,
			Score:     score,
			Important: important,
			Chunks:    chunks,
		})
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	sort.SliceStable(candidates, func(i int, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			if candidates[i].Important == candidates[j].Important {
				return shards[candidates[i].Index].EstimatedLines > shards[candidates[j].Index].EstimatedLines
			}
			return candidates[i].Important > candidates[j].Important
		}
		return candidates[i].Score > candidates[j].Score
	})
	refined := []AnalysisShard{}
	replaced := map[string]struct{}{}
	nextID := len(shards) + 1
	for _, candidate := range candidates {
		parent := shards[candidate.Index]
		if len(refined)+len(candidate.Chunks) > a.analysisCfg.MaxRefinementShards {
			if len(refined) > 0 {
				break
			}
			if len(candidate.Chunks) > a.analysisCfg.MaxRefinementShards {
				candidate.Chunks = candidate.Chunks[:a.analysisCfg.MaxRefinementShards]
			}
		}
		replaced[parent.ID] = struct{}{}
		for chunkIndex, chunk := range candidate.Chunks {
			child := AnalysisShard{
				ID:              fmt.Sprintf("shard-%02d", nextID),
				Name:            fmt.Sprintf("%s_refined_%02d", parent.Name, chunkIndex+1),
				ParentShardID:   parent.ID,
				RefinementStage: analysisMaxInt(parent.RefinementStage, 1) + 1,
				PrimaryFiles:    filesToPaths(chunk),
				EstimatedFiles:  len(chunk),
				EstimatedLines:  sumLines(chunk),
			}
			a.finalizeShard(snapshot, &child, 12)
			refined = append(refined, child)
			nextID++
		}
		if len(refined) >= a.analysisCfg.MaxRefinementShards {
			break
		}
	}
	return refined, replaced
}

func (a *projectAnalyzer) planRootCauseEvidenceRequestShards(snapshot ProjectSnapshot, existingShards []AnalysisShard, reviews []ReviewDecision, round int, limit int) []AnalysisShard {
	if normalizeProjectAnalysisMode(snapshot.AnalysisMode) != "root-cause" || limit <= 0 {
		return nil
	}
	existingPrimary := map[string]struct{}{}
	for _, shard := range existingShards {
		for _, path := range shard.PrimaryFiles {
			existingPrimary[path] = struct{}{}
		}
	}
	requests := rootCauseEvidenceRequestsFromReviews(reviews)
	if len(snapshot.RootCause.EvidenceRequests) > 0 {
		requests = snapshot.RootCause.EvidenceRequests
	}
	if len(requests) == 0 {
		return nil
	}
	out := []AnalysisShard{}
	seenRequest := map[string]struct{}{}
	nextID := len(existingShards) + 1
	maxFiles := a.analysisCfg.MaxFilesPerShard
	if maxFiles <= 0 || maxFiles > 12 {
		maxFiles = 12
	}
	for _, request := range requests {
		if rootCauseEvidenceRequestAlreadySatisfied(request) {
			continue
		}
		key := rootCauseEvidenceRequestKey(request)
		if key == "" {
			continue
		}
		if _, ok := seenRequest[key]; ok {
			continue
		}
		seenRequest[key] = struct{}{}
		files := rootCauseFilesForEvidenceRequest(snapshot, request, existingPrimary, maxFiles)
		if len(files) == 0 {
			continue
		}
		chunks := chunkFiles(files, maxFiles, analysisMaxInt(1200, a.analysisCfg.MaxLinesPerShard/2))
		if len(chunks) == 0 {
			chunks = [][]ScannedFile{files}
		}
		for chunkIndex, chunk := range chunks {
			if len(out) >= limit {
				break
			}
			shard := AnalysisShard{
				ID:                 fmt.Sprintf("shard-evidence-%02d-%02d", round, len(out)+1),
				Name:               rootCauseEvidenceShardName(request, round, chunkIndex+1),
				ParentShardID:      "",
				EvidenceRequestID:  request.ID,
				RefinementStage:    round + 1,
				PrimaryFiles:       filesToPaths(chunk),
				EstimatedFiles:     len(chunk),
				EstimatedLines:     sumLines(chunk),
				InvalidationReason: "root_cause_evidence_request",
			}
			if shard.ID == "" {
				shard.ID = fmt.Sprintf("shard-%02d", nextID)
			}
			nextID++
			a.finalizeShard(snapshot, &shard, 12)
			out = append(out, shard)
			for _, path := range shard.PrimaryFiles {
				existingPrimary[path] = struct{}{}
			}
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

func rootCauseEvidenceRequestAlreadySatisfied(request RootCauseEvidenceRequest) bool {
	switch normalizeRootCauseEvidenceRequestStatus(request.Status) {
	case "fulfilled", "blocked", "routed":
		return true
	default:
		return false
	}
}

func rootCauseEvidenceRequestsFromReviews(reviews []ReviewDecision) []RootCauseEvidenceRequest {
	out := []RootCauseEvidenceRequest{}
	for _, review := range reviews {
		out = append(out, normalizeRootCauseEvidenceRequests(review.EvidenceRequests)...)
		if len(review.EvidenceRequests) == 0 && strings.EqualFold(review.SymptomPossible, "partial") {
			signals := append([]string(nil), review.CausalChainMissing...)
			signals = append(signals, review.SymptomCausality...)
			if len(signals) > 0 {
				out = append(out, RootCauseEvidenceRequest{
					Request:         "Find the missing code evidence needed to complete the partial root-cause causal chain.",
					TargetSignals:   signals,
					Reason:          "Reviewer marked symptom causality as partial.",
					RequiredToProve: strings.Join(limitStrings(review.CausalChainMissing, 3), ", "),
				})
			}
		}
	}
	return normalizeRootCauseEvidenceRequests(out)
}

func normalizeRootCauseEvidenceRequests(requests []RootCauseEvidenceRequest) []RootCauseEvidenceRequest {
	out := []RootCauseEvidenceRequest{}
	for _, request := range requests {
		request.ID = strings.TrimSpace(request.ID)
		request.Request = strings.TrimSpace(request.Request)
		request.TargetSignals = analysisUniqueStrings(request.TargetSignals)
		request.TargetFiles = analysisUniqueStrings(request.TargetFiles)
		request.Reason = strings.TrimSpace(request.Reason)
		request.RequiredToProve = strings.TrimSpace(request.RequiredToProve)
		request.SourceShardID = strings.TrimSpace(request.SourceShardID)
		request.Status = normalizeRootCauseEvidenceRequestStatus(request.Status)
		request.RoutedShardIDs = analysisUniqueStrings(request.RoutedShardIDs)
		request.FulfilledByShards = analysisUniqueStrings(request.FulfilledByShards)
		request.SatisfiedEvidenceFiles = analysisUniqueStrings(request.SatisfiedEvidenceFiles)
		if request.Request == "" && len(request.TargetSignals) == 0 && len(request.TargetFiles) == 0 && request.RequiredToProve == "" {
			continue
		}
		if request.ID == "" {
			request.ID = rootCauseEvidenceRequestID(request)
		}
		if request.Status == "" {
			request.Status = "open"
		}
		out = append(out, request)
	}
	return out
}

func normalizeRootCauseEvidenceRequestStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "open", "routed", "fulfilled", "partial", "blocked":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "open"
	}
}

func rootCauseEvidenceRequestID(request RootCauseEvidenceRequest) string {
	key := rootCauseEvidenceRequestKey(RootCauseEvidenceRequest{
		Request:         request.Request,
		TargetSignals:   request.TargetSignals,
		TargetFiles:     request.TargetFiles,
		Reason:          request.Reason,
		RequiredToProve: request.RequiredToProve,
		SourceShardID:   request.SourceShardID,
	})
	if key == "" {
		key = "evidence-request"
	}
	sum := sha256.Sum256([]byte(key))
	return "er-" + hex.EncodeToString(sum[:])[:10]
}

func rootCauseEvidenceRequestKey(request RootCauseEvidenceRequest) string {
	parts := []string{
		rootCauseComparableText(request.Request),
		rootCauseComparableText(strings.Join(request.TargetSignals, " ")),
		rootCauseComparableText(strings.Join(request.TargetFiles, " ")),
		rootCauseComparableText(request.RequiredToProve),
		rootCauseComparableText(request.SourceShardID),
	}
	return strings.Join(analysisUniqueStrings(parts), "\x00")
}

func mergeRootCauseEvidenceRequestState(existing []RootCauseEvidenceRequest, incoming []RootCauseEvidenceRequest) []RootCauseEvidenceRequest {
	out := normalizeRootCauseEvidenceRequests(existing)
	indexByID := map[string]int{}
	for index, request := range out {
		indexByID[request.ID] = index
	}
	for _, request := range normalizeRootCauseEvidenceRequests(incoming) {
		if existingIndex, ok := indexByID[request.ID]; ok {
			out[existingIndex].TargetSignals = analysisUniqueStrings(append(out[existingIndex].TargetSignals, request.TargetSignals...))
			out[existingIndex].TargetFiles = analysisUniqueStrings(append(out[existingIndex].TargetFiles, request.TargetFiles...))
			out[existingIndex].RoutedShardIDs = analysisUniqueStrings(append(out[existingIndex].RoutedShardIDs, request.RoutedShardIDs...))
			out[existingIndex].FulfilledByShards = analysisUniqueStrings(append(out[existingIndex].FulfilledByShards, request.FulfilledByShards...))
			out[existingIndex].SatisfiedEvidenceFiles = analysisUniqueStrings(append(out[existingIndex].SatisfiedEvidenceFiles, request.SatisfiedEvidenceFiles...))
			if out[existingIndex].Status == "open" && request.Status != "" {
				out[existingIndex].Status = request.Status
			}
			continue
		}
		indexByID[request.ID] = len(out)
		out = append(out, request)
	}
	return normalizeRootCauseEvidenceRequests(out)
}

func markRootCauseEvidenceRequestsRouted(requests []RootCauseEvidenceRequest, shards []AnalysisShard) []RootCauseEvidenceRequest {
	out := normalizeRootCauseEvidenceRequests(requests)
	indexByID := map[string]int{}
	for index, request := range out {
		indexByID[request.ID] = index
	}
	for _, shard := range shards {
		if strings.TrimSpace(shard.EvidenceRequestID) == "" {
			continue
		}
		if index, ok := indexByID[shard.EvidenceRequestID]; ok {
			out[index].RoutedShardIDs = analysisUniqueStrings(append(out[index].RoutedShardIDs, shard.ID))
			if out[index].Status == "open" {
				out[index].Status = "routed"
			}
		}
	}
	return normalizeRootCauseEvidenceRequests(out)
}

func markRootCauseEvidenceRequestsFulfilled(requests []RootCauseEvidenceRequest, shards []AnalysisShard, reports []WorkerReport) []RootCauseEvidenceRequest {
	out := normalizeRootCauseEvidenceRequests(requests)
	indexByID := map[string]int{}
	for index, request := range out {
		indexByID[request.ID] = index
	}
	for index, shard := range shards {
		requestID := strings.TrimSpace(shard.EvidenceRequestID)
		if requestID == "" {
			continue
		}
		requestIndex, ok := indexByID[requestID]
		if !ok {
			continue
		}
		out[requestIndex].RoutedShardIDs = analysisUniqueStrings(append(out[requestIndex].RoutedShardIDs, shard.ID))
		evidenceFiles := append([]string(nil), shard.PrimaryFiles...)
		if index < len(reports) {
			evidenceFiles = append(evidenceFiles, reports[index].EvidenceFiles...)
			for _, candidate := range reports[index].RootCauseCandidates {
				evidenceFiles = append(evidenceFiles, candidate.EvidenceFiles...)
			}
		}
		out[requestIndex].SatisfiedEvidenceFiles = analysisUniqueStrings(append(out[requestIndex].SatisfiedEvidenceFiles, evidenceFiles...))
		if index < len(reports) && (len(reports[index].Facts) > 0 || len(reports[index].RootCauseCandidates) > 0 || len(reports[index].EvidenceFiles) > 0) {
			out[requestIndex].FulfilledByShards = analysisUniqueStrings(append(out[requestIndex].FulfilledByShards, shard.ID))
			out[requestIndex].Status = "fulfilled"
		} else if out[requestIndex].Status != "fulfilled" {
			out[requestIndex].Status = "partial"
		}
	}
	return normalizeRootCauseEvidenceRequests(out)
}

func rootCauseEvidenceShardName(request RootCauseEvidenceRequest, round int, index int) string {
	source := firstNonBlankRootCauseString(request.Request, strings.Join(request.TargetSignals, "_"), strings.Join(request.TargetFiles, "_"), "evidence")
	tokens := rootCauseEvidenceTerms(source)
	if len(tokens) == 0 {
		tokens = []string{"evidence"}
	}
	return fmt.Sprintf("root_cause_evidence_%02d_%02d_%s", round, index, sanitizeFileName(strings.Join(limitStrings(tokens, 4), "_")))
}

func rootCauseFilesForEvidenceRequest(snapshot ProjectSnapshot, request RootCauseEvidenceRequest, existingPrimary map[string]struct{}, limit int) []ScannedFile {
	type scoredFile struct {
		File  ScannedFile
		Score int
	}
	scores := map[string]scoredFile{}
	addFile := func(path string, score int) {
		file, ok := rootCauseSnapshotFile(snapshot, path)
		if !ok {
			return
		}
		if _, exists := existingPrimary[file.Path]; exists {
			return
		}
		current := scores[file.Path]
		current.File = file
		current.Score += score
		scores[file.Path] = current
	}
	for _, target := range request.TargetFiles {
		if path := resolveRootCauseEvidenceTargetFile(snapshot, target); path != "" {
			addFile(path, 100)
		}
	}
	terms := rootCauseEvidenceTerms(strings.Join([]string{
		request.Request,
		strings.Join(request.TargetSignals, " "),
		request.Reason,
		request.RequiredToProve,
	}, " "))
	for _, match := range snapshot.RootCause.CodeMatches {
		matchScore := 0
		for _, term := range terms {
			if rootCauseTextContainsTerm(match.File, term) || rootCauseTextContainsTerm(match.Query, term) || rootCauseTextContainsTerm(strings.Join(match.MatchedSignals, " "), term) || rootCauseTextContainsTerm(match.Reason, term) {
				matchScore += 20
			}
		}
		if matchScore > 0 {
			addFile(match.File, matchScore+analysisMaxInt(match.Score/2, 1))
		}
	}
	for _, file := range rootCauseSnapshotFiles(snapshot) {
		if _, exists := existingPrimary[file.Path]; exists {
			continue
		}
		score := 0
		corpus := strings.ToLower(strings.Join([]string{
			file.Path,
			file.Directory,
			file.Extension,
			strings.Join(file.ImportanceReasons, " "),
			strings.Join(file.RawImports, " "),
			strings.Join(file.Imports, " "),
		}, " "))
		for _, term := range terms {
			if rootCauseTextContainsTerm(file.Path, term) {
				score += 18
			}
			if rootCauseTextContainsTerm(filepath.Base(file.Path), term) {
				score += 16
			}
			if strings.Contains(corpus, strings.ToLower(term)) {
				score += 8
			}
		}
		if score > 0 {
			score += analysisMinInt(file.ImportanceScore, 20)
			addFile(file.Path, score)
		}
	}
	items := make([]scoredFile, 0, len(scores))
	for _, item := range scores {
		items = append(items, item)
	}
	sort.SliceStable(items, func(i int, j int) bool {
		if items[i].Score == items[j].Score {
			if items[i].File.ImportanceScore == items[j].File.ImportanceScore {
				return items[i].File.Path < items[j].File.Path
			}
			return items[i].File.ImportanceScore > items[j].File.ImportanceScore
		}
		return items[i].Score > items[j].Score
	})
	out := []ScannedFile{}
	for _, item := range items {
		out = append(out, item.File)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func rootCauseSnapshotFiles(snapshot ProjectSnapshot) []ScannedFile {
	if len(snapshot.Files) > 0 {
		return append([]ScannedFile(nil), snapshot.Files...)
	}
	out := make([]ScannedFile, 0, len(snapshot.FilesByPath))
	for _, file := range snapshot.FilesByPath {
		out = append(out, file)
	}
	sort.SliceStable(out, func(i int, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out
}

func rootCauseSnapshotFile(snapshot ProjectSnapshot, path string) (ScannedFile, bool) {
	path = cleanEvidencePath(path)
	if path == "" {
		return ScannedFile{}, false
	}
	if file, ok := snapshot.FilesByPath[path]; ok {
		return file, true
	}
	for _, file := range snapshot.Files {
		if file.Path == path {
			return file, true
		}
	}
	return ScannedFile{}, false
}

func resolveRootCauseEvidenceTargetFile(snapshot ProjectSnapshot, target string) string {
	target = cleanEvidencePath(target)
	if target == "" {
		return ""
	}
	allowed := map[string]string{}
	for _, file := range rootCauseSnapshotFiles(snapshot) {
		rememberAllowedEvidencePath(allowed, file.Path)
	}
	if canonical := canonicalEvidencePath(target, allowed); canonical != "" {
		return canonical
	}
	targetLower := strings.ToLower(filepath.ToSlash(target))
	for _, file := range rootCauseSnapshotFiles(snapshot) {
		pathLower := strings.ToLower(filepath.ToSlash(file.Path))
		if strings.HasSuffix(pathLower, targetLower) || strings.Contains(pathLower, targetLower) {
			return file.Path
		}
	}
	return ""
}

func rootCauseEvidenceTerms(text string) []string {
	terms := []string{}
	for _, raw := range regexp.MustCompile(`[A-Za-z0-9_가-힣]+`).FindAllString(text, -1) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if len([]rune(raw)) < 3 {
			continue
		}
		terms = append(terms, raw)
	}
	return analysisUniqueStrings(terms)
}

func rootCauseTextContainsTerm(text string, term string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	term = strings.ToLower(strings.TrimSpace(term))
	return text != "" && term != "" && strings.Contains(text, term)
}

func (a *projectAnalyzer) refinementChunks(snapshot ProjectSnapshot, shard AnalysisShard) [][]ScannedFile {
	if len(shard.PrimaryFiles) < 3 {
		return nil
	}
	files := make([]ScannedFile, 0, len(shard.PrimaryFiles))
	for _, path := range shard.PrimaryFiles {
		file, ok := snapshot.FilesByPath[path]
		if !ok {
			continue
		}
		files = append(files, file)
	}
	if len(files) < 3 {
		return nil
	}
	sort.SliceStable(files, func(i int, j int) bool {
		if files[i].ImportanceScore == files[j].ImportanceScore {
			if files[i].Directory == files[j].Directory {
				return files[i].Path < files[j].Path
			}
			return files[i].Directory < files[j].Directory
		}
		return files[i].ImportanceScore > files[j].ImportanceScore
	})
	maxFiles := a.analysisCfg.MaxFilesPerShard / 2
	if maxFiles < 6 {
		maxFiles = 6
	}
	if maxFiles >= len(files) {
		maxFiles = analysisMaxInt(2, len(files)/2)
	}
	maxLines := a.analysisCfg.MaxLinesPerShard / 2
	if maxLines < 1200 {
		maxLines = 1200
	}
	chunks := chunkFiles(files, maxFiles, maxLines)
	if len(chunks) < 2 {
		return nil
	}
	return chunks
}

func mergeRefinedShardResults(baseShards []AnalysisShard, baseReports []WorkerReport, baseReviews []ReviewDecision, refinedShards []AnalysisShard, refinedReports []WorkerReport, refinedReviews []ReviewDecision, replaced map[string]struct{}) ([]AnalysisShard, []WorkerReport, []ReviewDecision) {
	mergedShards := make([]AnalysisShard, 0, len(baseShards)+len(refinedShards))
	mergedReports := make([]WorkerReport, 0, len(baseReports)+len(refinedReports))
	mergedReviews := make([]ReviewDecision, 0, len(baseReviews)+len(refinedReviews))
	for index, shard := range baseShards {
		if _, ok := replaced[shard.ID]; ok {
			continue
		}
		mergedShards = append(mergedShards, shard)
		mergedReports = append(mergedReports, baseReports[index])
		mergedReviews = append(mergedReviews, baseReviews[index])
	}
	mergedShards = append(mergedShards, refinedShards...)
	mergedReports = append(mergedReports, refinedReports...)
	mergedReviews = append(mergedReviews, refinedReviews...)
	return mergedShards, mergedReports, mergedReviews
}

func scoreRefinementCandidate(shard AnalysisShard, report WorkerReport, review ReviewDecision, topImportant map[string]struct{}, lensSet map[string]struct{}) (int, int) {
	score := 0
	score += ceilDiv(shard.EstimatedLines, 12000)
	score += ceilDiv(shard.EstimatedFiles, 12)
	importantHits := 0
	for _, path := range shard.PrimaryFiles {
		if _, ok := topImportant[path]; ok {
			importantHits++
		}
	}
	score += importantHits * 3
	if strings.EqualFold(review.Status, "needs_revision") {
		score += 6
	}
	if strings.EqualFold(review.Status, "review_failed") {
		score += 5
	}
	score += analysisMinInt(len(report.Unknowns), 5)
	if len(report.RootCauseCandidates) > 0 {
		score += analysisMinInt(len(report.RootCauseCandidates)*3, 9)
	}
	for _, candidate := range report.RootCauseCandidates {
		score += analysisMinInt(len(candidate.NeedsCrossShardEvidence), 3)
		if strings.EqualFold(strings.TrimSpace(candidate.Confidence), "low") {
			score += 1
		}
	}
	if len(report.InternalFlow) == 0 {
		score += 2
	}
	if len(report.EvidenceFiles) <= 2 {
		score += 2
	}
	if len(report.EntryPoints) == 0 && len(report.Collaboration) == 0 {
		score += 1
	}
	if _, ok := lensSet["ipc"]; ok && shardHasSignal(shard, report, []string{"pipe", "ipc", "message", "command", "dispatch", "handler"}) {
		score += 4
	}
	if _, ok := lensSet["runtime_flow"]; ok && shardHasSignal(shard, report, []string{"startup", "runtime", "loader", "service", "worker", "manager"}) {
		score += 3
	}
	if _, ok := lensSet["security_boundary"]; ok && shardHasSignal(shard, report, []string{"kernel", "driver", "ioctl", "tamper", "policy", "protect"}) {
		score += 3
	}
	if importantHits == 0 && shard.EstimatedLines < 10000 && len(report.Unknowns) == 0 && strings.EqualFold(review.Status, "approved") {
		score -= 3
	}
	return score, importantHits
}

func shardHasSignal(shard AnalysisShard, report WorkerReport, tokens []string) bool {
	parts := []string{shard.Name}
	parts = append(parts, shard.PrimaryFiles...)
	parts = append(parts, report.Title)
	parts = append(parts, report.ScopeSummary)
	parts = append(parts, report.EntryPoints...)
	parts = append(parts, report.InternalFlow...)
	parts = append(parts, report.Collaboration...)
	joined := strings.ToLower(strings.Join(parts, " "))
	for _, token := range tokens {
		if strings.Contains(joined, strings.ToLower(token)) {
			return true
		}
	}
	return false
}

func lintWorkerReportForRootCause(snapshot ProjectSnapshot, shard AnalysisShard, report WorkerReport) []string {
	if normalizeProjectAnalysisMode(snapshot.AnalysisMode) != "root-cause" {
		return nil
	}
	issues := []string{}
	if len(report.RootCauseCandidates) == 0 {
		if len(report.Unknowns) == 0 && shardLikelyRootCauseRelevant(snapshot, shard) {
			issues = append(issues, "Explain why this shard cannot affect the symptom or provide a concrete root_cause_candidates item.")
		}
		return issues
	}
	for _, candidate := range report.RootCauseCandidates {
		title := firstNonBlankAnalysisString(candidate.Title, "unnamed candidate")
		if rootCauseCausalChainStageCount(candidate.CausalChain) < 3 {
			issues = append(issues, fmt.Sprintf("%s: causal_chain needs at least trigger, invalid_state, and state_transition.", title))
		}
		if len(candidate.EvidenceFiles) == 0 {
			issues = append(issues, fmt.Sprintf("%s: evidence_files must cite exact assigned files.", title))
		}
		if !rootCauseCandidateHasConcreteStateSignal(candidate) {
			issues = append(issues, fmt.Sprintf("%s: name the exact variable/field/config key/DB value/enum/counter/limit that leaves the expected range.", title))
		}
		if len(candidate.OutOfRangeCases) > 0 && !rootCauseListHasConcreteStateSignal(candidate.OutOfRangeCases) {
			issues = append(issues, fmt.Sprintf("%s: out_of_range_cases must include concrete value/state examples, not only prose.", title))
		}
		if len(candidate.CannotBeRootCauseIf) == 0 || len(candidate.RequiredRuntimeObservation) == 0 {
			issues = append(issues, fmt.Sprintf("%s: fill cannot_be_root_cause_if and required_runtime_observation.", title))
		}
		if !rootCauseCandidateHasValidProbe(candidate) {
			issues = append(issues, fmt.Sprintf("%s: probes need expected_signal and disproves_when.", title))
		}
	}
	return analysisUniqueStrings(issues)
}

func shardLikelyRootCauseRelevant(snapshot ProjectSnapshot, shard AnalysisShard) bool {
	corpus := strings.ToLower(strings.Join(append(append([]string{shard.Name}, shard.PrimaryFiles...), shard.ReferenceFiles...), " "))
	for _, token := range rootCauseEvidenceTerms(rootCauseReportedSymptomText(snapshot)) {
		if rootCauseTextContainsTerm(corpus, token) {
			return true
		}
	}
	for _, match := range snapshot.RootCause.CodeMatches {
		for _, path := range shard.PrimaryFiles {
			if cleanEvidencePath(match.File) == cleanEvidencePath(path) {
				return true
			}
		}
	}
	return false
}

func rootCauseListHasConcreteStateSignal(items []string) bool {
	for _, item := range items {
		if rootCauseTextHasConcreteStateSignal(item) {
			return true
		}
	}
	return false
}

func buildRootCauseWorkerLintRevisionPrompt(issues []string) string {
	var b strings.Builder
	b.WriteString("Revise the root-cause report before reviewer evaluation. Fix these deterministic lint issues:\n")
	for _, issue := range limitStrings(issues, 8) {
		fmt.Fprintf(&b, "- %s\n", issue)
	}
	b.WriteString("\nReturn the same JSON schema only. Each candidate must name exact variables/fields/config or DB values, include a five-stage causal_chain when evidence allows, cite assigned evidence_files, and provide probes with expected_signal and disproves_when.")
	return strings.TrimSpace(b.String())
}

func (a *projectAnalyzer) runWorker(ctx context.Context, snapshot ProjectSnapshot, shard AnalysisShard, goal string, revisionPrompt string) (WorkerReport, error) {
	resp, err := a.completeAnalysisRequestWithRetry(ctx, a.workerOrDefaultClient(), "worker", shard.Name, a.workerModel(), ChatRequest{
		Model:       a.workerModel(),
		System:      workerSystemPrompt(),
		Messages:    []Message{{Role: "user", Text: buildWorkerPrompt(snapshot, shard, goal, revisionPrompt)}},
		MaxTokens:   a.workerMaxTokens(),
		Temperature: a.cfg.Temperature,
		WorkingDir:  a.workspace.Root,
		JSONMode:    true,
	})
	if err != nil {
		return WorkerReport{}, fmt.Errorf("analysis worker request failed for shard=%s model=%s: %w", shard.Name, a.workerModel(), err)
	}

	raw := strings.TrimSpace(resp.Message.Text)
	report, ok := parseWorkerReportPayload(raw, shard)
	if !ok {
		stopReason := strings.TrimSpace(resp.StopReason)
		if stopReason != "" {
			a.debug(fmt.Sprintf("worker returned non-JSON output for shard=%s stop_reason=%s raw_chars=%d; attempting repair", shard.Name, stopReason, len(raw)))
		} else {
			a.debug(fmt.Sprintf("worker returned non-JSON output for shard=%s raw_chars=%d; attempting repair", shard.Name, len(raw)))
		}
		repaired, repairErr := a.repairWorkerReport(ctx, snapshot, shard, goal, revisionPrompt, raw)
		if repairErr == nil {
			return repaired, nil
		}
		a.debug(fmt.Sprintf("worker repair failed for shard=%s: %v", shard.Name, repairErr))
		return fallbackWorkerReport(shard, raw), nil
	}
	return report, nil
}

func (a *projectAnalyzer) repairWorkerReport(ctx context.Context, snapshot ProjectSnapshot, shard AnalysisShard, goal string, revisionPrompt string, raw string) (WorkerReport, error) {
	invalidExcerpt := analysisPromptExcerpt(raw, 6000)
	if strings.TrimSpace(invalidExcerpt) == "" {
		invalidExcerpt = "(empty response)"
	}
	repairPrompt := strings.TrimSpace(buildWorkerPrompt(snapshot, shard, goal, revisionPrompt) + "\n\nThe previous response was not valid JSON or was truncated. Return a shorter valid JSON object in the required schema only. Keep 3-7 high-value items per list, keep narrative under 900 characters, and do not include raw source excerpts.\n\nPrevious invalid response excerpt:\n```\n" + invalidExcerpt + "\n```")
	resp, err := a.completeAnalysisRequestWithRetry(ctx, a.workerOrDefaultClient(), "worker-repair", shard.Name, a.workerModel(), ChatRequest{
		Model:       a.workerModel(),
		System:      workerSystemPrompt(),
		Messages:    []Message{{Role: "user", Text: repairPrompt}},
		MaxTokens:   a.workerMaxTokens(),
		Temperature: a.cfg.Temperature,
		WorkingDir:  a.workspace.Root,
		JSONMode:    true,
	})
	if err != nil {
		return WorkerReport{}, fmt.Errorf("analysis worker repair failed for shard=%s model=%s: %w", shard.Name, a.workerModel(), err)
	}
	repairedRaw := strings.TrimSpace(resp.Message.Text)
	report, ok := parseWorkerReportPayload(repairedRaw, shard)
	if !ok {
		return WorkerReport{}, fmt.Errorf("unable to parse repaired worker report")
	}
	return report, nil
}

func (a *projectAnalyzer) reviewReport(ctx context.Context, snapshot ProjectSnapshot, shard AnalysisShard, report WorkerReport, goal string, previousRun *ProjectAnalysisRun, reuseState analysisReuseState) (ReviewDecision, error) {
	previousReport, hasPreviousReport := a.previousReportForShard(previousRun, shard, reuseState)

	resp, err := a.completeAnalysisRequestWithRetry(ctx, a.reviewerOrDefaultClient(), "reviewer", shard.Name, a.reviewerModel(), ChatRequest{
		Model:       a.reviewerModel(),
		System:      reviewerSystemPrompt(),
		Messages:    []Message{{Role: "user", Text: buildReviewerPrompt(snapshot, shard, report, goal, previousReport, hasPreviousReport)}},
		MaxTokens:   a.reviewerMaxTokens(),
		Temperature: a.cfg.Temperature,
		WorkingDir:  a.workspace.Root,
		JSONMode:    true,
	})
	if err != nil {
		return ReviewDecision{}, fmt.Errorf("analysis reviewer request failed for shard=%s model=%s: %w", shard.Name, a.reviewerModel(), err)
	}

	raw := strings.TrimSpace(resp.Message.Text)
	decision, ok := parseReviewDecisionPayload(raw)
	if !ok {
		return heuristicReviewDecision(report, raw), nil
	}
	if normalizeProjectAnalysisMode(snapshot.AnalysisMode) == "root-cause" {
		decision = enforceRootCauseReviewContract(decision)
	}
	return decision, nil
}

func enforceRootCauseReviewContract(decision ReviewDecision) ReviewDecision {
	if !strings.EqualFold(strings.TrimSpace(decision.Status), "approved") {
		return decision
	}
	missing := rootCauseReviewContractMissing(decision)
	if len(missing) == 0 {
		return decision
	}
	decision.Status = "needs_revision"
	decision.FailureKind = analysisReviewIssueQuality
	decision.Issues = analysisUniqueStrings(append(decision.Issues, "Root-cause reviewer approval is missing required fields: "+strings.Join(missing, ", ")))
	if strings.TrimSpace(decision.RevisionPrompt) == "" {
		decision.RevisionPrompt = "Revise the review decision with symptom_reproduction_bridge, required_runtime_observation, and disqualifying_evidence. Approve only if these fields concretely connect the candidate to the reported symptom."
	}
	return decision
}

type rootCauseDeepVerificationTarget struct {
	ShardID     string             `json:"shard_id"`
	ShardName   string             `json:"shard_name"`
	ReportTitle string             `json:"report_title"`
	Candidate   RootCauseCandidate `json:"candidate"`
	Review      ReviewDecision     `json:"review"`
}

func (a *projectAnalyzer) deepVerifyRootCauseCandidates(ctx context.Context, snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, reviews []ReviewDecision, goal string) []RootCauseDeepVerification {
	targets := selectRootCauseDeepVerificationTargets(shards, reports, reviews, 4)
	if len(targets) == 0 {
		return nil
	}
	fallback := enhanceRootCauseDeepVerificationProbesWithRepoCommands(snapshot, fallbackRootCauseDeepVerifications(targets))
	if a.client == nil {
		return fallback
	}
	resp, err := a.completeAnalysisRequestWithRetry(ctx, a.client, "root-cause-deep-verify", "", a.cfg.Model, ChatRequest{
		Model:       a.cfg.Model,
		System:      rootCauseDeepVerificationSystemPrompt(),
		Messages:    []Message{{Role: "user", Text: buildRootCauseDeepVerificationPromptWithIndex(snapshot, shards, targets, goal, a.cachedSemanticIndexV2)}},
		MaxTokens:   analysisStructuredMaxTokens(a.cfg.Model, a.cfg.MaxTokens),
		Temperature: a.cfg.Temperature,
		WorkingDir:  a.workspace.Root,
		JSONMode:    true,
	})
	if err != nil {
		a.debug(fmt.Sprintf("root-cause deep verification soft-failed: %v", err))
		return fallback
	}
	verified, ok := parseRootCauseDeepVerificationPayload(resp.Message.Text)
	if !ok || len(verified) == 0 {
		a.debug("root-cause deep verification returned non-JSON output; using deterministic fallback")
		return fallback
	}
	return enhanceRootCauseDeepVerificationProbesWithRepoCommands(snapshot, verified)
}

func selectRootCauseDeepVerificationTargets(shards []AnalysisShard, reports []WorkerReport, reviews []ReviewDecision, limit int) []rootCauseDeepVerificationTarget {
	targets := []rootCauseDeepVerificationTarget{}
	for index, report := range reports {
		if index >= len(shards) || index >= len(reviews) {
			continue
		}
		review := reviews[index]
		if !rootCauseReviewApprovesSymptomCausality(review) {
			continue
		}
		for _, candidate := range report.RootCauseCandidates {
			if rootCauseCandidateRejectedByReview(candidate, review) {
				continue
			}
			targets = append(targets, rootCauseDeepVerificationTarget{
				ShardID:     shards[index].ID,
				ShardName:   shards[index].Name,
				ReportTitle: report.Title,
				Candidate:   candidate,
				Review:      rootCauseReviewForDeepVerification(review),
			})
		}
	}
	sort.SliceStable(targets, func(i int, j int) bool {
		left := rootCauseConfidenceScore(targets[i].Candidate, targets[i].Review)
		right := rootCauseConfidenceScore(targets[j].Candidate, targets[j].Review)
		if left == right {
			return rootCauseCausalChainStageCount(targets[i].Candidate.CausalChain) > rootCauseCausalChainStageCount(targets[j].Candidate.CausalChain)
		}
		return left > right
	})
	if limit > 0 && len(targets) > limit {
		targets = targets[:limit]
	}
	return targets
}

func rootCauseReviewForDeepVerification(review ReviewDecision) ReviewDecision {
	review.Raw = ""
	review.RevisionPrompt = ""
	return review
}

func rootCauseDeepVerificationSystemPrompt() string {
	return strings.TrimSpace(`
You are a senior deep verifier for root-cause candidates already approved by a reviewer.
Return strict JSON only:
{
  "verifications": [
    {
      "candidate_title": "string",
      "shard_id": "string",
      "shard_name": "string",
      "status": "supported|weak|disconfirmed|unknown",
      "summary": "string",
      "causal_chain": {
        "trigger": "string",
        "invalid_state": "string",
        "state_transition": "string",
        "missing_guard": "string",
        "user_visible_symptom": "string",
        "evidence_files": ["string"]
      },
      "evidence_files": ["string"],
      "instrumentation_steps": ["string"],
      "disconfirming_evidence": ["string"],
      "cannot_be_root_cause_if": ["string"],
      "required_runtime_observation": ["string"],
      "probes": [
        {
          "title": "string",
          "kind": "log|assert|test|repro|db_config_dump|trace",
          "target": "file or function",
          "command": "string",
          "expected_signal": "string",
          "disproves_when": "string"
        }
      ],
      "confidence": "low|medium|high",
      "confidence_breakdown": {
        "causal_completeness": 0,
        "evidence_strength": 0,
        "runtime_observability": 0,
        "alternative_explanations": 0,
        "disconfirmation_strength": 0,
        "score": 0,
        "reasons": ["string"]
      }
    }
  ]
}
Rules:
- Verify whether the candidate can cause the user's exact symptom, not whether it is merely a bug.
- Fill the five causal_chain stages from source evidence. If evidence is missing, status should be weak or unknown.
- Use disconfirmed when visible code proves the symptom cannot follow from the candidate.
- instrumentation_steps must be concrete: where to log/assert, which value to dump, or which branch to trace.
- probes must be executable or directly implementable checks with expected_signal and disproves_when.
- confidence_breakdown must justify the status with 0-100 component scores.
- Do not introduce evidence files outside the provided file context.
`)
}

func buildRootCauseDeepVerificationPrompt(snapshot ProjectSnapshot, shards []AnalysisShard, targets []rootCauseDeepVerificationTarget, goal string) string {
	return buildRootCauseDeepVerificationPromptWithIndex(snapshot, shards, targets, goal, SemanticIndexV2{})
}

func buildRootCauseDeepVerificationPromptWithIndex(snapshot ProjectSnapshot, shards []AnalysisShard, targets []rootCauseDeepVerificationTarget, goal string, index SemanticIndexV2) string {
	data, _ := json.MarshalIndent(targets, "", "  ")
	files := []string{}
	shardsByID := map[string]AnalysisShard{}
	for _, shard := range shards {
		shardsByID[shard.ID] = shard
	}
	for _, target := range targets {
		files = append(files, target.Candidate.EvidenceFiles...)
		if shard, ok := shardsByID[target.ShardID]; ok {
			files = append(files, shard.PrimaryFiles...)
		}
	}
	files = analysisUniqueStrings(files)
	var b strings.Builder
	fmt.Fprintf(&b, "Goal:\n%s\n\n", strings.TrimSpace(goal))
	if plan := renderRootCauseInvestigationForPrompt(snapshot.RootCause, 5000); strings.TrimSpace(plan) != "" {
		b.WriteString("Normalized root-cause plan:\n")
		b.WriteString(plan)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Reviewer-approved candidate targets:\n%s\n\n", string(data))
	if context := buildFocusedRootCauseEvidenceContext(snapshot, shards, targets, index, 12, 4); strings.TrimSpace(context) != "" {
		b.WriteString("Focused source excerpts:\n")
		b.WriteString(context)
		b.WriteString("\n")
	} else if len(files) > 0 {
		b.WriteString("File context:\n")
		for _, section := range buildFileContext(snapshot, limitStrings(files, 12), 8) {
			b.WriteString(section)
			b.WriteString("\n")
		}
	}
	b.WriteString("\nReturn JSON only.")
	return strings.TrimSpace(b.String())
}

func buildFocusedRootCauseEvidenceContext(snapshot ProjectSnapshot, shards []AnalysisShard, targets []rootCauseDeepVerificationTarget, index SemanticIndexV2, maxFiles int, contextLines int) string {
	if maxFiles <= 0 {
		maxFiles = 8
	}
	if contextLines <= 0 {
		contextLines = 3
	}
	shardsByID := map[string]AnalysisShard{}
	for _, shard := range shards {
		shardsByID[shard.ID] = shard
	}
	type targetFile struct {
		Path    string
		Signals []string
	}
	files := []targetFile{}
	fileIndex := map[string]int{}
	for _, target := range targets {
		signals := rootCauseFocusedSignals(target.Candidate)
		paths := append([]string(nil), target.Candidate.EvidenceFiles...)
		paths = append(paths, target.Candidate.CausalChain.EvidenceFiles...)
		if shard, ok := shardsByID[target.ShardID]; ok {
			paths = append(paths, shard.PrimaryFiles...)
		}
		for _, path := range analysisUniqueStrings(paths) {
			if _, ok := rootCauseSnapshotFile(snapshot, path); !ok {
				continue
			}
			if index, ok := fileIndex[path]; ok {
				files[index].Signals = analysisUniqueStrings(append(files[index].Signals, signals...))
				continue
			}
			fileIndex[path] = len(files)
			files = append(files, targetFile{Path: path, Signals: signals})
		}
	}
	var b strings.Builder
	count := 0
	for _, item := range files {
		if count >= maxFiles {
			break
		}
		semanticRanges := rootCauseSemanticFocusedLineRanges(index, item.Path, item.Signals)
		excerpt := focusedSourceExcerptForFile(snapshot, item.Path, item.Signals, semanticRanges, contextLines, 90)
		if strings.TrimSpace(excerpt) == "" {
			continue
		}
		b.WriteString(excerpt)
		b.WriteString("\n")
		count++
	}
	return strings.TrimSpace(b.String())
}

type rootCauseLineRange struct {
	Start  int
	End    int
	Reason string
}

func rootCauseSemanticFocusedLineRanges(index SemanticIndexV2, path string, signals []string) []rootCauseLineRange {
	if len(index.Symbols) == 0 {
		return nil
	}
	path = cleanEvidencePath(path)
	signalText := strings.ToLower(strings.Join(rootCauseEvidenceTerms(strings.Join(signals, " ")), " "))
	out := []rootCauseLineRange{}
	for _, symbol := range index.Symbols {
		if cleanEvidencePath(symbol.File) != path {
			continue
		}
		if symbol.StartLine <= 0 {
			continue
		}
		corpus := strings.ToLower(strings.Join([]string{
			symbol.ID,
			symbol.Name,
			symbol.CanonicalName,
			symbol.Kind,
			symbol.Signature,
			strings.Join(symbol.Tags, " "),
		}, " "))
		if signalText != "" && !rootCauseSemanticCorpusMatchesSignals(corpus, signals) && !rootCauseSemanticCorpusMatchesSignals(signalText, []string{symbol.Name, symbol.CanonicalName}) {
			continue
		}
		start := symbol.StartLine
		end := symbol.EndLine
		if end < start {
			end = start
		}
		out = append(out, rootCauseLineRange{
			Start:  start,
			End:    end,
			Reason: "symbol:" + firstNonBlankRootCauseString(symbol.CanonicalName, symbol.Name, symbol.ID),
		})
	}
	for _, edge := range append(append([]CallEdge(nil), index.CallEdges...), rootCauseCallEdgesFromReferences(index.References)...) {
		if !edgeTouchesFiles(edge.Evidence, map[string]struct{}{path: {}}) {
			continue
		}
		for _, symbol := range rootCauseSymbolsForEdge(index, edge, path) {
			if symbol.StartLine <= 0 {
				continue
			}
			out = append(out, rootCauseLineRange{
				Start:  symbol.StartLine,
				End:    analysisMaxInt(symbol.StartLine, symbol.EndLine),
				Reason: "edge:" + firstNonBlankRootCauseString(symbol.CanonicalName, symbol.Name, symbol.ID),
			})
		}
	}
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].Start == out[j].Start {
			return out[i].End < out[j].End
		}
		return out[i].Start < out[j].Start
	})
	deduped := []rootCauseLineRange{}
	seen := map[string]struct{}{}
	for _, item := range out {
		key := fmt.Sprintf("%d-%d-%s", item.Start, item.End, item.Reason)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, item)
		if len(deduped) >= 8 {
			break
		}
	}
	return deduped
}

func rootCauseSemanticCorpusMatchesSignals(corpus string, signals []string) bool {
	corpus = strings.ToLower(corpus)
	for _, signal := range signals {
		for _, term := range rootCauseEvidenceTerms(signal) {
			if rootCauseTextContainsTerm(corpus, term) {
				return true
			}
		}
	}
	return false
}

func rootCauseCallEdgesFromReferences(refs []ReferenceRecord) []CallEdge {
	out := []CallEdge{}
	for _, ref := range refs {
		if strings.TrimSpace(ref.SourceID) == "" || strings.TrimSpace(ref.TargetID) == "" {
			continue
		}
		out = append(out, CallEdge{
			SourceID: ref.SourceID,
			TargetID: ref.TargetID,
			Type:     ref.Type,
			Evidence: ref.Evidence,
		})
	}
	return out
}

func rootCauseSymbolsForEdge(index SemanticIndexV2, edge CallEdge, path string) []SymbolRecord {
	out := []SymbolRecord{}
	for _, symbol := range index.Symbols {
		if cleanEvidencePath(symbol.File) != path {
			continue
		}
		if symbol.ID == edge.SourceID || symbol.ID == edge.TargetID {
			out = append(out, symbol)
		}
	}
	return out
}

func rootCauseFocusedSignals(candidate RootCauseCandidate) []string {
	signals := []string{
		candidate.Title,
		candidate.CausalChain.Trigger,
		candidate.CausalChain.InvalidState,
		candidate.CausalChain.StateTransition,
		candidate.CausalChain.MissingGuard,
		candidate.CausalChain.UserVisibleSymptom,
	}
	signals = append(signals, candidate.TriggerValues...)
	signals = append(signals, candidate.ExpectedRange...)
	signals = append(signals, candidate.OutOfRangeCases...)
	signals = append(signals, candidate.ObservedFailurePath...)
	signals = append(signals, candidate.RequiredRuntimeObservation...)
	signals = append(signals, candidate.VerificationSteps...)
	terms := []string{}
	for _, signal := range signals {
		terms = append(terms, rootCauseEvidenceTerms(signal)...)
	}
	return limitStrings(analysisUniqueStrings(terms), 16)
}

func focusedSourceExcerptForFile(snapshot ProjectSnapshot, path string, signals []string, semanticRanges []rootCauseLineRange, contextLines int, maxLines int) string {
	file, ok := rootCauseSnapshotFile(snapshot, path)
	if !ok {
		return ""
	}
	abs := filepath.Join(snapshot.Root, filepath.FromSlash(file.Path))
	data, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	lines := splitLines(string(data))
	if len(lines) == 0 {
		return ""
	}
	matched := map[int]struct{}{}
	reasons := []string{}
	for _, item := range semanticRanges {
		start := analysisMaxInt(1, item.Start-contextLines)
		end := analysisMinInt(len(lines), item.End+contextLines)
		for line := start; line <= end; line++ {
			matched[line-1] = struct{}{}
		}
		if strings.TrimSpace(item.Reason) != "" {
			reasons = append(reasons, item.Reason)
		}
	}
	for index, line := range lines {
		for _, signal := range signals {
			if rootCauseTextContainsTerm(line, signal) {
				start := analysisMaxInt(0, index-contextLines)
				end := analysisMinInt(len(lines)-1, index+contextLines)
				for current := start; current <= end; current++ {
					matched[current] = struct{}{}
				}
				break
			}
		}
		if len(matched) >= maxLines {
			break
		}
	}
	if len(matched) == 0 {
		fallbackEnd := analysisMinInt(len(lines), analysisMaxInt(20, maxLines/2))
		for index := 0; index < fallbackEnd; index++ {
			matched[index] = struct{}{}
		}
	}
	indexes := make([]int, 0, len(matched))
	for index := range matched {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	if maxLines > 0 && len(indexes) > maxLines {
		indexes = indexes[:maxLines]
	}
	imports := file.Imports
	if len(imports) == 0 {
		imports = file.RawImports
	}
	var b strings.Builder
	fmt.Fprintf(&b, "FILE %s\n- focused_lines: %d/%d\n- imports: %s\n", file.Path, len(indexes), len(lines), strings.Join(imports, ", "))
	if len(reasons) > 0 {
		fmt.Fprintf(&b, "- semantic_focus: %s\n", strings.Join(limitStrings(analysisUniqueStrings(reasons), 6), ", "))
	}
	b.WriteString("```\n")
	last := -2
	for _, index := range indexes {
		if last >= 0 && index > last+1 {
			b.WriteString("...\n")
		}
		fmt.Fprintf(&b, "%04d: %s\n", index+1, lines[index])
		last = index
	}
	b.WriteString("```\n")
	return strings.TrimSpace(b.String())
}

func parseRootCauseDeepVerificationPayload(raw string) ([]RootCauseDeepVerification, bool) {
	type envelope struct {
		Verifications []RootCauseDeepVerification `json:"verifications"`
	}
	for _, candidate := range analysisJSONCandidates(raw) {
		wrapped := envelope{}
		if err := json.Unmarshal([]byte(candidate), &wrapped); err == nil && len(wrapped.Verifications) > 0 {
			out := normalizeRootCauseDeepVerifications(wrapped.Verifications)
			for i := range out {
				out[i].Raw = strings.TrimSpace(raw)
			}
			return out, true
		}
		items := []RootCauseDeepVerification{}
		if err := json.Unmarshal([]byte(candidate), &items); err == nil && len(items) > 0 {
			out := normalizeRootCauseDeepVerifications(items)
			for i := range out {
				out[i].Raw = strings.TrimSpace(raw)
			}
			return out, true
		}
	}
	return nil, false
}

func fallbackRootCauseDeepVerifications(targets []rootCauseDeepVerificationTarget) []RootCauseDeepVerification {
	out := []RootCauseDeepVerification{}
	for _, target := range targets {
		stageCount := rootCauseCausalChainStageCount(target.Candidate.CausalChain)
		status := "weak"
		if stageCount >= 4 {
			status = "supported"
		}
		out = append(out, RootCauseDeepVerification{
			CandidateTitle:             target.Candidate.Title,
			ShardID:                    target.ShardID,
			ShardName:                  target.ShardName,
			Status:                     status,
			Summary:                    fmt.Sprintf("Deterministic deep verification fallback preserved the reviewer-approved candidate with %d causal stage(s).", stageCount),
			CausalChain:                target.Candidate.CausalChain,
			EvidenceFiles:              append([]string(nil), target.Candidate.EvidenceFiles...),
			InstrumentationSteps:       append([]string(nil), target.Candidate.VerificationSteps...),
			DisconfirmingEvidence:      append([]string(nil), target.Candidate.DisconfirmingEvidence...),
			CannotBeRootCauseIf:        append([]string(nil), target.Candidate.CannotBeRootCauseIf...),
			RequiredRuntimeObservation: append([]string(nil), target.Candidate.RequiredRuntimeObservation...),
			Probes:                     append([]RootCauseProbe(nil), target.Candidate.Probes...),
			Confidence:                 target.Candidate.Confidence,
			ConfidenceBreakdown:        buildRootCauseConfidenceBreakdown(target.Candidate, target.Review),
		})
	}
	return normalizeRootCauseDeepVerifications(out)
}

func applyRootCauseDeepVerificationsToReports(snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, verifications []RootCauseDeepVerification) []WorkerReport {
	if len(verifications) == 0 || len(reports) == 0 {
		return reports
	}
	byKey := map[string]RootCauseDeepVerification{}
	for _, verification := range verifications {
		key := rootCauseCandidateKey(verification.ShardID, verification.CandidateTitle)
		if key != "" {
			byKey[key] = verification
		}
	}
	out := append([]WorkerReport(nil), reports...)
	for index := range out {
		shard := AnalysisShard{}
		if index < len(shards) {
			shard = shards[index]
		}
		kept := []RootCauseCandidate{}
		for _, candidate := range out[index].RootCauseCandidates {
			verification, ok := byKey[rootCauseCandidateKey(shard.ID, candidate.Title)]
			if !ok {
				kept = append(kept, candidate)
				continue
			}
			switch verification.Status {
			case "disconfirmed":
				out[index].Unknowns = analysisUniqueStrings(append(out[index].Unknowns, "Root-cause candidate disconfirmed by deep verification: "+firstNonBlankAnalysisString(candidate.Title, "unnamed candidate")))
				continue
			case "supported", "weak", "unknown":
				candidate = mergeRootCauseCandidateWithDeepVerification(candidate, verification, shard)
			}
			kept = append(kept, candidate)
		}
		out[index].RootCauseCandidates = normalizeRootCauseCandidates(kept, shard)
	}
	return out
}

func mergeRootCauseCandidateWithDeepVerification(candidate RootCauseCandidate, verification RootCauseDeepVerification, shard AnalysisShard) RootCauseCandidate {
	if rootCauseCausalChainStageCount(verification.CausalChain) > rootCauseCausalChainStageCount(candidate.CausalChain) {
		candidate.CausalChain = verification.CausalChain
	}
	candidate.EvidenceFiles = analysisUniqueStrings(append(candidate.EvidenceFiles, verification.EvidenceFiles...))
	candidate.DisconfirmingEvidence = analysisUniqueStrings(append(candidate.DisconfirmingEvidence, verification.DisconfirmingEvidence...))
	candidate.CannotBeRootCauseIf = analysisUniqueStrings(append(candidate.CannotBeRootCauseIf, verification.CannotBeRootCauseIf...))
	candidate.RequiredRuntimeObservation = analysisUniqueStrings(append(candidate.RequiredRuntimeObservation, verification.RequiredRuntimeObservation...))
	candidate.VerificationSteps = analysisUniqueStrings(append(candidate.VerificationSteps, verification.InstrumentationSteps...))
	candidate.Probes = normalizeRootCauseProbes(append(candidate.Probes, verification.Probes...))
	if verification.Status == "weak" || verification.Status == "unknown" {
		candidate.NeedsCrossShardEvidence = analysisUniqueStrings(append(candidate.NeedsCrossShardEvidence, "Deep verification did not prove the full causal chain."))
		if strings.EqualFold(candidate.Confidence, "high") {
			candidate.Confidence = "medium"
		}
	}
	if verification.Status == "supported" && strings.TrimSpace(verification.Confidence) != "" {
		candidate.Confidence = verification.Confidence
	}
	candidate.CausalChain = normalizeRootCauseCausalChain(candidate.CausalChain, candidate, shard)
	if len(candidate.Probes) == 0 {
		candidate.Probes = deriveRootCauseCandidateProbes(candidate, shard)
	}
	candidate.ConfidenceBreakdown = normalizeRootCauseConfidenceBreakdown(verification.ConfidenceBreakdown, rootCauseConfidenceScore(candidate, ReviewDecision{}))
	return candidate
}

func rootCauseCandidateKey(shardID string, title string) string {
	shardID = strings.TrimSpace(shardID)
	title = rootCauseComparableText(title)
	if shardID == "" || title == "" {
		return ""
	}
	return shardID + "\x00" + title
}

func buildRootCauseAuditTrail(snapshot ProjectSnapshot, auditShards []AnalysisShard, auditReports []WorkerReport, auditReviews []ReviewDecision, finalReports []WorkerReport, deepVerifications []RootCauseDeepVerification, joined []RootCauseJoinedCandidate) RootCauseAuditTrail {
	finalCandidateKeys := map[string]struct{}{}
	for index, report := range finalReports {
		if index >= len(auditShards) {
			continue
		}
		for _, candidate := range report.RootCauseCandidates {
			if key := rootCauseCandidateKey(auditShards[index].ID, candidate.Title); key != "" {
				finalCandidateKeys[key] = struct{}{}
			}
		}
	}
	deepStatus := map[string]string{}
	for _, verification := range deepVerifications {
		key := rootCauseCandidateKey(verification.ShardID, verification.CandidateTitle)
		if key != "" {
			deepStatus[key] = verification.Status
		}
	}
	decisions := []RootCauseCandidateAudit{}
	for index, report := range auditReports {
		if index >= len(auditShards) {
			continue
		}
		review := ReviewDecision{}
		if index < len(auditReviews) {
			review = auditReviews[index]
		}
		for _, candidate := range report.RootCauseCandidates {
			key := rootCauseCandidateKey(auditShards[index].ID, candidate.Title)
			explicitPatternIDs := rootCauseExplicitPatternIDsForCandidate(candidate, snapshot.RootCause.PatternMatches)
			inferredPatternIDs := rootCauseInferredRelatedPatternIDsForCandidate(candidate, snapshot.RootCause.PatternMatches)
			decision := "withheld"
			reason := rootCauseReviewRejectionNote(review)
			qualityGate := evaluateRootCauseCandidateQualityGate(snapshot, auditShards[index], candidate, review)
			qualityGateIssues := analysisUniqueStrings(append(rootCauseQualityGateIssuesForCandidate(report, candidate), qualityGate.Issues...))
			if _, ok := finalCandidateKeys[key]; ok {
				decision = "included"
				reason = "Reviewer validated symptom causality and deep verification did not disconfirm the candidate."
			} else if rootCauseCandidateRejectedByReview(candidate, review) {
				decision = "reviewer_rejected"
				reason = "Reviewer listed this candidate in rejected_candidates."
			} else if status := deepStatus[key]; status == "disconfirmed" {
				decision = "deep_disconfirmed"
				reason = "Deep verification disconfirmed the candidate."
			} else if qualityGate.Reject || len(qualityGateIssues) > 0 {
				decision = "quality_gate_rejected"
				reason = strings.Join(limitStrings(qualityGateIssues, 3), " | ")
			} else if !rootCauseReviewApprovesSymptomCausality(review) {
				decision = "review_failed"
			}
			decisions = append(decisions, RootCauseCandidateAudit{
				ShardID:                   auditShards[index].ID,
				ShardName:                 auditShards[index].Name,
				ReportTitle:               report.Title,
				CandidateTitle:            candidate.Title,
				ReviewStatus:              review.Status,
				SymptomPossible:           review.SymptomPossible,
				CausalChainStages:         rootCauseCausalChainStages(candidate.CausalChain),
				CausalChainMissing:        rootCauseMissingCausalChainStages(candidate.CausalChain),
				Decision:                  decision,
				Reason:                    reason,
				QualityGateIssues:         qualityGateIssues,
				EvidenceFiles:             append([]string(nil), candidate.EvidenceFiles...),
				DeepVerificationStatus:    deepStatus[key],
				PatternIDs:                analysisUniqueStrings(append(append([]string{}, explicitPatternIDs...), inferredPatternIDs...)),
				ExplicitPatternIDs:        explicitPatternIDs,
				InferredRelatedPatternIDs: inferredPatternIDs,
			})
		}
	}
	patternMatches := markRootCausePatternMatchUsage(snapshot.RootCause.PatternMatches, auditReports, auditReviews, finalReports)
	return RootCauseAuditTrail{
		GeneratedAt:        time.Now(),
		Symptom:            snapshot.RootCause.Symptom.Symptom,
		CodeMatches:        append([]RootCauseCodeMatch(nil), snapshot.RootCause.CodeMatches...),
		EvidenceRequests:   append([]RootCauseEvidenceRequest(nil), snapshot.RootCause.EvidenceRequests...),
		PatternMatches:     patternMatches,
		CandidateDecisions: decisions,
		DeepVerifications:  append([]RootCauseDeepVerification(nil), deepVerifications...),
		JoinedCandidates:   append([]RootCauseJoinedCandidate(nil), joined...),
	}
}

func rootCauseQualityGateIssuesForCandidate(report WorkerReport, candidate RootCauseCandidate) []string {
	title := rootCauseComparableText(candidate.Title)
	if title == "" {
		return nil
	}
	out := []string{}
	for _, unknown := range report.Unknowns {
		lower := rootCauseComparableText(unknown)
		if !strings.Contains(lower, "deterministic gate") {
			continue
		}
		if strings.Contains(lower, title) || strings.Contains(title, lower) {
			out = append(out, unknown)
		}
	}
	return analysisUniqueStrings(out)
}

func rootCauseAuditTrailHasContent(audit RootCauseAuditTrail) bool {
	return strings.TrimSpace(audit.Symptom) != "" ||
		len(audit.CodeMatches) > 0 ||
		len(audit.EvidenceRequests) > 0 ||
		len(audit.PatternMatches) > 0 ||
		len(audit.CandidateDecisions) > 0 ||
		len(audit.DeepVerifications) > 0 ||
		len(audit.JoinedCandidates) > 0
}

func applyRootCauseRegressionMemoryToReports(reports []WorkerReport, memory RootCauseAuditTrail) []WorkerReport {
	return applyRootCauseRegressionMemoryToReportsWithChanges(reports, memory, nil)
}

func applyRootCauseRegressionMemoryToReportsWithChanges(reports []WorkerReport, memory RootCauseAuditTrail, changedMemory map[string]bool) []WorkerReport {
	if !rootCauseAuditTrailHasContent(memory) || len(reports) == 0 {
		return reports
	}
	negative := []RootCauseCandidateAudit{}
	for _, item := range memory.CandidateDecisions {
		switch strings.ToLower(strings.TrimSpace(item.Decision)) {
		case "reviewer_rejected", "deep_disconfirmed", "review_failed":
			negative = append(negative, item)
		}
	}
	if len(negative) == 0 {
		return reports
	}
	out := append([]WorkerReport(nil), reports...)
	for reportIndex := range out {
		for candidateIndex := range out[reportIndex].RootCauseCandidates {
			candidate := &out[reportIndex].RootCauseCandidates[candidateIndex]
			for _, previous := range negative {
				if !rootCauseCandidateMatchesAudit(*candidate, previous) {
					continue
				}
				note := fmt.Sprintf("Regression memory: previous run marked a similar candidate as %s", previous.Decision)
				if strings.TrimSpace(previous.Reason) != "" {
					note += " (" + previous.Reason + ")"
				}
				changed := changedMemory[rootCauseCandidateAuditChangeKey(previous)]
				if changed {
					note += "; related code changed since that run, so this memory is a weaker prior."
				}
				candidate.DisconfirmingEvidence = analysisUniqueStrings(append(candidate.DisconfirmingEvidence, note))
				candidate.NeedsCrossShardEvidence = analysisUniqueStrings(append(candidate.NeedsCrossShardEvidence, "Re-check the previous rejection/disconfirmation before accepting this candidate."))
				if changed {
					candidate.Confidence = rootCauseLowerConfidence(candidate.Confidence, "medium")
					candidate.ConfidenceBreakdown.Score = analysisMinInt(rootCauseConfidenceScore(*candidate, ReviewDecision{}), 65)
				} else {
					candidate.Confidence = "low"
					candidate.ConfidenceBreakdown.Score = analysisMinInt(rootCauseConfidenceScore(*candidate, ReviewDecision{}), 45)
				}
				candidate.ConfidenceBreakdown.Reasons = analysisUniqueStrings(append(candidate.ConfidenceBreakdown.Reasons, note))
				candidate.ConfidenceBreakdown = normalizeRootCauseConfidenceBreakdown(candidate.ConfidenceBreakdown, candidate.ConfidenceBreakdown.Score)
			}
		}
	}
	return out
}

func buildRootCauseRegressionMemoryChangeState(previousRun *ProjectAnalysisRun, currentShards []AnalysisShard) map[string]bool {
	out := map[string]bool{}
	if previousRun == nil || !rootCauseAuditTrailHasContent(previousRun.RootCause.AuditTrail) {
		return out
	}
	currentByPrimary := map[string]AnalysisShard{}
	for _, shard := range currentShards {
		currentByPrimary[primaryFilesKey(shard.PrimaryFiles)] = shard
	}
	previousByID := map[string]AnalysisShard{}
	for _, shard := range previousRun.Shards {
		previousByID[shard.ID] = shard
	}
	for _, audit := range previousRun.RootCause.AuditTrail.CandidateDecisions {
		previousShard, ok := previousByID[audit.ShardID]
		if !ok {
			out[rootCauseCandidateAuditChangeKey(audit)] = true
			continue
		}
		currentShard, ok := currentByPrimary[primaryFilesKey(previousShard.PrimaryFiles)]
		if !ok {
			out[rootCauseCandidateAuditChangeKey(audit)] = true
			continue
		}
		changed := previousShard.PrimaryFingerprint != currentShard.PrimaryFingerprint ||
			previousShard.PrimarySemanticFingerprint != currentShard.PrimarySemanticFingerprint ||
			previousShard.ReferenceFingerprint != currentShard.ReferenceFingerprint ||
			previousShard.ReferenceSemanticFingerprint != currentShard.ReferenceSemanticFingerprint
		out[rootCauseCandidateAuditChangeKey(audit)] = changed
	}
	return out
}

func rootCauseCandidateAuditChangeKey(audit RootCauseCandidateAudit) string {
	return rootCauseComparableText(strings.Join([]string{audit.ShardID, audit.CandidateTitle}, "\x00"))
}

func rootCauseCandidateMatchesAudit(candidate RootCauseCandidate, audit RootCauseCandidateAudit) bool {
	title := rootCauseComparableText(candidate.Title)
	previous := rootCauseComparableText(audit.CandidateTitle)
	if title != "" && previous != "" {
		if title == previous {
			return true
		}
		if rootCauseComparableTextIsSpecific(title) && strings.Contains(previous, title) {
			return true
		}
		if rootCauseComparableTextIsSpecific(previous) && strings.Contains(title, previous) {
			return true
		}
	}
	candidateFiles := map[string]struct{}{}
	for _, path := range candidate.EvidenceFiles {
		candidateFiles[cleanEvidencePath(path)] = struct{}{}
	}
	for _, path := range audit.EvidenceFiles {
		if _, ok := candidateFiles[cleanEvidencePath(path)]; ok && title != "" && previous != "" {
			return true
		}
	}
	return false
}

func renderRootCauseRegressionMemoryForPrompt(memory RootCauseAuditTrail, limit int) string {
	if !rootCauseAuditTrailHasContent(memory) {
		return ""
	}
	var b strings.Builder
	if strings.TrimSpace(memory.Symptom) != "" {
		fmt.Fprintf(&b, "- Previous symptom: %s\n", memory.Symptom)
	}
	if len(memory.CandidateDecisions) > 0 {
		b.WriteString("Previous candidate decisions:\n")
		for _, item := range limitRootCauseCandidateAudits(memory.CandidateDecisions, 8) {
			fmt.Fprintf(&b, "- %s [%s] shard=%s symptom=%s deep=%s\n", firstNonBlankAnalysisString(item.CandidateTitle, "candidate"), item.Decision, item.ShardID, item.SymptomPossible, item.DeepVerificationStatus)
			if strings.TrimSpace(item.Reason) != "" {
				fmt.Fprintf(&b, "  reason=%s\n", item.Reason)
			}
			if len(item.EvidenceFiles) > 0 {
				fmt.Fprintf(&b, "  evidence=%s\n", strings.Join(limitStrings(item.EvidenceFiles, 4), ", "))
			}
		}
	}
	if len(memory.JoinedCandidates) > 0 {
		b.WriteString("Previous joined findings:\n")
		for _, item := range limitRootCauseJoinedCandidates(memory.JoinedCandidates, 5) {
			fmt.Fprintf(&b, "- %s (%s, confidence=%s, score=%d)\n", item.Title, item.Classification, item.Confidence, item.ConfidenceScore)
		}
	}
	text := strings.TrimSpace(b.String())
	if limit > 0 {
		text = analysisPromptExcerpt(text, limit)
	}
	return strings.TrimSpace(text)
}

func limitRootCauseCandidateAudits(items []RootCauseCandidateAudit, limit int) []RootCauseCandidateAudit {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func (a *projectAnalyzer) joinRootCauseCandidates(ctx context.Context, snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, reviews []ReviewDecision, goal string) []RootCauseJoinedCandidate {
	fallback := fallbackRootCauseJoin(snapshot, shards, reports, reviews)
	if len(fallback) == 0 || a.client == nil {
		return fallback
	}
	resp, err := a.completeAnalysisRequestWithRetry(ctx, a.client, "root-cause-join", "", a.cfg.Model, ChatRequest{
		Model:       a.cfg.Model,
		System:      rootCauseJoinSystemPrompt(),
		Messages:    []Message{{Role: "user", Text: buildRootCauseJoinPrompt(snapshot, shards, reports, reviews, goal)}},
		MaxTokens:   analysisStructuredMaxTokens(a.cfg.Model, a.cfg.MaxTokens),
		Temperature: a.cfg.Temperature,
		WorkingDir:  a.workspace.Root,
		JSONMode:    true,
	})
	if err != nil {
		a.debug(fmt.Sprintf("root-cause join soft-failed: %v", err))
		return fallback
	}
	joined, ok := parseRootCauseJoinPayload(resp.Message.Text)
	if !ok {
		a.debug("root-cause join returned non-JSON output; using deterministic fallback")
		return fallback
	}
	joined = normalizeRootCauseJoinedCandidates(joined)
	if len(joined) == 0 {
		return fallback
	}
	return joined
}

func rootCauseJoinSystemPrompt() string {
	return strings.TrimSpace(`
You join reviewed shard-level root-cause candidates into final root cause findings.
Your entire response must be a single JSON object. The first byte must be "{" and the last byte must be "}".
Return strict JSON:
{
  "joined_candidates": [
    {
      "title": "string",
      "classification": "root_cause|contributing_factor|detection_gap|operational_trigger|unknown",
      "cluster_id": "string",
      "cluster_members": ["string"],
      "competes_with": ["cluster_id or title"],
      "depends_on": ["cluster_id or title"],
      "can_coexist_with": ["cluster_id or title"],
      "joined_chain": ["string"],
      "causal_chain": {
        "trigger": "string",
        "invalid_state": "string",
        "state_transition": "string",
        "missing_guard": "string",
        "user_visible_symptom": "string",
        "evidence_files": ["string"]
      },
      "supporting_candidates": ["string"],
      "evidence_files": ["string"],
      "disconfirming_evidence": ["string"],
      "cannot_be_root_cause_if": ["string"],
      "required_runtime_observation": ["string"],
      "verification_steps": ["string"],
      "probes": [
        {
          "title": "string",
          "kind": "log|assert|test|repro|db_config_dump|trace",
          "target": "file or function",
          "command": "string",
          "expected_signal": "string",
          "disproves_when": "string"
        }
      ],
      "confidence": "low|medium|high",
      "confidence_breakdown": {
        "causal_completeness": 0,
        "evidence_strength": 0,
        "runtime_observability": 0,
        "alternative_explanations": 0,
        "disconfirmation_strength": 0,
        "score": 0,
        "reasons": ["string"]
      },
      "confidence_score": 0,
      "pattern_ids": ["known pattern id if current source evidence supports it"]
    }
  ]
}
Rules:
- Join across shards. Do not merely restate a single worker candidate unless one shard proves the whole chain.
- Known root-cause patterns are priors only. Preserve pattern_ids only when the joined source evidence independently proves the pattern.
- Cluster and deduplicate near-identical candidates. Use the same cluster_id for candidates that share the same causal mechanism or evidence path.
- Fill competes_with, depends_on, and can_coexist_with when candidates are mutually exclusive, causally dependent, or can all be true.
- Separate root_cause, contributing_factor, detection_gap, and operational_trigger.
- A root_cause must explain the exact user symptom end to end.
- A root_cause must have at least four of the five causal_chain stages. If the chain is weaker, classify it as contributing_factor, detection_gap, operational_trigger, or unknown.
- Include disconfirming evidence and cannot_be_root_cause_if so the result remains falsifiable.
- Include concrete probes that a developer can run or implement to confirm and disprove the joined candidate.
- confidence_score is 0-100 and should consider chain completeness, evidence files, reviewer symptom_causality, remaining missing edges, and verification feasibility.
- confidence_breakdown must show those scoring factors explicitly.
`)
}

func buildRootCauseJoinPrompt(snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, reviews []ReviewDecision, goal string) string {
	type reviewedCandidate struct {
		ShardID          string                     `json:"shard_id"`
		ShardName        string                     `json:"shard_name"`
		ReportTitle      string                     `json:"report_title"`
		Candidate        RootCauseCandidate         `json:"candidate"`
		SymptomPossible  string                     `json:"symptom_possible,omitempty"`
		SymptomCausality []string                   `json:"symptom_causality,omitempty"`
		Rejected         []string                   `json:"rejected_candidates,omitempty"`
		EvidenceRequests []RootCauseEvidenceRequest `json:"evidence_requests,omitempty"`
	}
	items := []reviewedCandidate{}
	for index, report := range reports {
		if index >= len(shards) {
			continue
		}
		review := ReviewDecision{}
		if index < len(reviews) {
			review = reviews[index]
		}
		for _, candidate := range report.RootCauseCandidates {
			items = append(items, reviewedCandidate{
				ShardID:          shards[index].ID,
				ShardName:        shards[index].Name,
				ReportTitle:      report.Title,
				Candidate:        candidate,
				SymptomPossible:  review.SymptomPossible,
				SymptomCausality: review.SymptomCausality,
				Rejected:         review.RejectedCandidates,
				EvidenceRequests: review.EvidenceRequests,
			})
		}
	}
	data, _ := json.MarshalIndent(items, "", "  ")
	var b strings.Builder
	fmt.Fprintf(&b, "Goal:\n%s\n\n", goal)
	if rootCausePlan := renderRootCauseInvestigationForPrompt(snapshot.RootCause, 6000); strings.TrimSpace(rootCausePlan) != "" {
		b.WriteString("Normalized symptom and hypotheses:\n")
		b.WriteString(rootCausePlan)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Reviewed candidate inputs:\n%s\n\n", string(data))
	b.WriteString("Join requirements:\n")
	b.WriteString("- Build joined_candidates only from reviewer symptom-approved or partial candidates.\n")
	b.WriteString("- If candidates are weakly related, classify them as contributing_factor or detection_gap rather than root_cause.\n")
	b.WriteString("- Every joined candidate must include a falsification path.\n")
	return strings.TrimSpace(b.String())
}

func parseRootCauseJoinPayload(raw string) ([]RootCauseJoinedCandidate, bool) {
	type envelope struct {
		JoinedCandidates []RootCauseJoinedCandidate `json:"joined_candidates"`
	}
	for _, candidate := range analysisJSONCandidates(raw) {
		wrapped := envelope{}
		if err := json.Unmarshal([]byte(candidate), &wrapped); err == nil && len(wrapped.JoinedCandidates) > 0 {
			return normalizeRootCauseJoinedCandidates(wrapped.JoinedCandidates), true
		}
		items := []RootCauseJoinedCandidate{}
		if err := json.Unmarshal([]byte(candidate), &items); err == nil && len(items) > 0 {
			return normalizeRootCauseJoinedCandidates(items), true
		}
	}
	return nil, false
}

func fallbackRootCauseJoin(snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, reviews []ReviewDecision) []RootCauseJoinedCandidate {
	out := []RootCauseJoinedCandidate{}
	for index, report := range reports {
		if index >= len(shards) {
			continue
		}
		review := ReviewDecision{}
		if index < len(reviews) {
			review = reviews[index]
		}
		if !rootCauseReviewApprovesSymptomCausality(review) {
			continue
		}
		for _, candidate := range report.RootCauseCandidates {
			if rootCauseCandidateRejectedByReview(candidate, review) {
				continue
			}
			joined := RootCauseJoinedCandidate{
				Title:                      candidate.Title,
				Classification:             rootCauseClassificationForCandidate(candidate, review),
				JoinedChain:                analysisUniqueStrings(append(append([]string(nil), candidate.CandidateChain...), candidate.ObservedFailurePath...)),
				CausalChain:                candidate.CausalChain,
				SupportingCandidates:       []string{strings.TrimSpace(report.Title)},
				EvidenceFiles:              append([]string(nil), candidate.EvidenceFiles...),
				DisconfirmingEvidence:      append([]string(nil), candidate.DisconfirmingEvidence...),
				CannotBeRootCauseIf:        append([]string(nil), candidate.CannotBeRootCauseIf...),
				RequiredRuntimeObservation: append([]string(nil), candidate.RequiredRuntimeObservation...),
				VerificationSteps:          append([]string(nil), candidate.VerificationSteps...),
				Probes:                     append([]RootCauseProbe(nil), candidate.Probes...),
				Confidence:                 candidate.Confidence,
				ConfidenceBreakdown:        buildRootCauseConfidenceBreakdown(candidate, review),
				ConfidenceScore:            rootCauseConfidenceScore(candidate, review),
				PatternIDs:                 rootCausePatternIDsForCandidate(candidate, snapshot.RootCause.PatternMatches),
			}
			if len(joined.JoinedChain) == 0 {
				joined.JoinedChain = append(joined.JoinedChain, candidate.Title)
			}
			if len(joined.EvidenceFiles) == 0 && index < len(shards) {
				joined.EvidenceFiles = append([]string(nil), shards[index].PrimaryFiles...)
			}
			if len(joined.RequiredRuntimeObservation) == 0 {
				joined.RequiredRuntimeObservation = append(joined.RequiredRuntimeObservation, "Reproduce the reported symptom while tracing this candidate path.")
			}
			if len(joined.CannotBeRootCauseIf) == 0 {
				joined.CannotBeRootCauseIf = append(joined.CannotBeRootCauseIf, "The reported symptom reproduces while this candidate path is not taken.")
			}
			if len(joined.Probes) == 0 {
				joined.Probes = deriveRootCauseJoinedProbes(joined)
			}
			out = append(out, joined)
		}
	}
	return normalizeRootCauseJoinedCandidates(out)
}

func rootCauseClassificationForCandidate(candidate RootCauseCandidate, review ReviewDecision) string {
	if strings.EqualFold(candidate.Confidence, "high") && strings.EqualFold(review.SymptomPossible, "yes") && rootCauseCausalChainStageCount(candidate.CausalChain) >= 4 {
		return "root_cause"
	}
	if len(candidate.NeedsCrossShardEvidence) > 0 || strings.EqualFold(review.SymptomPossible, "partial") {
		return "contributing_factor"
	}
	return "contributing_factor"
}

func rootCauseConfidenceScore(candidate RootCauseCandidate, review ReviewDecision) int {
	score := 20
	if strings.EqualFold(review.SymptomPossible, "yes") {
		score += 25
	} else if strings.EqualFold(review.SymptomPossible, "partial") {
		score += 12
	}
	score += analysisMinInt(len(candidate.CandidateChain)*6, 18)
	score += rootCauseCausalChainStageCount(candidate.CausalChain) * 5
	score += analysisMinInt(len(candidate.ObservedFailurePath)*6, 18)
	score += analysisMinInt(len(candidate.EvidenceFiles)*4, 12)
	if len(candidate.DisconfirmingEvidence) > 0 || len(candidate.CannotBeRootCauseIf) > 0 {
		score += 7
	}
	if len(candidate.RequiredRuntimeObservation) > 0 || len(candidate.VerificationSteps) > 0 || len(candidate.Probes) > 0 {
		score += 5
	}
	switch strings.ToLower(strings.TrimSpace(candidate.Confidence)) {
	case "high":
		score += 10
	case "low":
		score -= 10
	}
	if len(candidate.NeedsCrossShardEvidence) > 0 {
		score -= analysisMinInt(len(candidate.NeedsCrossShardEvidence)*4, 12)
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func normalizeRootCauseJoinedCandidates(candidates []RootCauseJoinedCandidate) []RootCauseJoinedCandidate {
	out := []RootCauseJoinedCandidate{}
	for _, candidate := range candidates {
		candidate.Title = strings.TrimSpace(candidate.Title)
		candidate.Classification = normalizeRootCauseClassification(candidate.Classification)
		candidate.ClusterID = strings.TrimSpace(candidate.ClusterID)
		candidate.ClusterMembers = analysisUniqueStrings(candidate.ClusterMembers)
		candidate.CompetesWith = analysisUniqueStrings(candidate.CompetesWith)
		candidate.DependsOn = analysisUniqueStrings(candidate.DependsOn)
		candidate.CanCoexistWith = analysisUniqueStrings(candidate.CanCoexistWith)
		candidate.JoinedChain = analysisUniqueStrings(candidate.JoinedChain)
		candidate.CausalChain = normalizeJoinedRootCauseCausalChain(candidate.CausalChain)
		candidate.SupportingCandidates = analysisUniqueStrings(candidate.SupportingCandidates)
		candidate.EvidenceFiles = analysisUniqueStrings(candidate.EvidenceFiles)
		candidate.DisconfirmingEvidence = analysisUniqueStrings(candidate.DisconfirmingEvidence)
		candidate.CannotBeRootCauseIf = analysisUniqueStrings(candidate.CannotBeRootCauseIf)
		candidate.RequiredRuntimeObservation = analysisUniqueStrings(candidate.RequiredRuntimeObservation)
		candidate.VerificationSteps = analysisUniqueStrings(candidate.VerificationSteps)
		candidate.Probes = normalizeRootCauseProbes(candidate.Probes)
		candidate.PatternIDs = analysisUniqueStrings(candidate.PatternIDs)
		if len(candidate.Probes) == 0 {
			candidate.Probes = deriveRootCauseJoinedProbes(candidate)
		}
		candidate.ConfidenceBreakdown = normalizeRootCauseConfidenceBreakdown(candidate.ConfidenceBreakdown, candidate.ConfidenceScore)
		if candidate.ConfidenceScore == 0 && candidate.ConfidenceBreakdown.Score > 0 {
			candidate.ConfidenceScore = candidate.ConfidenceBreakdown.Score
		}
		if candidate.ConfidenceScore == 0 {
			switch strings.ToLower(strings.TrimSpace(candidate.Confidence)) {
			case "high":
				candidate.ConfidenceScore = 75
			case "medium":
				candidate.ConfidenceScore = 50
			case "low":
				candidate.ConfidenceScore = 30
			}
		}
		candidate.Confidence = normalizeRootCauseConfidence(candidate.Confidence, candidate.ConfidenceScore)
		if candidate.ConfidenceScore < 0 {
			candidate.ConfidenceScore = 0
		}
		if candidate.ConfidenceScore > 100 {
			candidate.ConfidenceScore = 100
		}
		if candidate.Title == "" && len(candidate.JoinedChain) == 0 {
			continue
		}
		if candidate.Title == "" {
			candidate.Title = "Root-cause candidate"
		}
		if candidate.ClusterID == "" {
			candidate.ClusterID = rootCauseClusterID(rootCauseJoinedClusterKey(candidate))
		}
		if len(candidate.ClusterMembers) == 0 {
			candidate.ClusterMembers = analysisUniqueStrings(append([]string{candidate.Title}, candidate.SupportingCandidates...))
		}
		candidate.ConfidenceBreakdown = normalizeRootCauseConfidenceBreakdown(candidate.ConfidenceBreakdown, candidate.ConfidenceScore)
		out = append(out, candidate)
	}
	out = dedupeRootCauseJoinedCandidates(out)
	out = inferRootCauseJoinedCandidateRelations(out)
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].Classification == out[j].Classification {
			return out[i].ConfidenceScore > out[j].ConfidenceScore
		}
		return rootCauseClassificationRank(out[i].Classification) < rootCauseClassificationRank(out[j].Classification)
	})
	return out
}

func applyRootCauseJoinedQualityGate(snapshot ProjectSnapshot, candidates []RootCauseJoinedCandidate) []RootCauseJoinedCandidate {
	if len(candidates) == 0 {
		return candidates
	}
	out := append([]RootCauseJoinedCandidate(nil), candidates...)
	for index := range out {
		issues := []string{}
		stageCount := rootCauseCausalChainStageCount(out[index].CausalChain)
		if normalizeRootCauseClassification(out[index].Classification) == "root_cause" && stageCount < 4 {
			out[index].Classification = "contributing_factor"
			issues = append(issues, "Deterministic gate: root_cause classification requires at least 4 causal stages.")
		}
		if len(out[index].EvidenceFiles) == 0 {
			out[index].Confidence = rootCauseLowerConfidence(out[index].Confidence, "low")
			out[index].ConfidenceScore = analysisMinInt(out[index].ConfidenceScore, 45)
			issues = append(issues, "Deterministic gate: joined candidate has no evidence_files.")
		}
		if !rootCauseJoinedCandidateHasValidProbe(out[index]) {
			out[index].Confidence = rootCauseLowerConfidence(out[index].Confidence, "medium")
			out[index].ConfidenceScore = analysisMinInt(out[index].ConfidenceScore, 65)
			issues = append(issues, "Deterministic gate: joined candidate probes must include expected_signal and disproves_when.")
		}
		if rootCauseJoinedCandidateUserSymptomMismatches(snapshot, out[index]) {
			out[index].Classification = "unknown"
			out[index].Confidence = "low"
			out[index].ConfidenceScore = analysisMinInt(out[index].ConfidenceScore, 25)
			issues = append(issues, "Deterministic gate: joined user_visible_symptom does not overlap the reported symptom.")
		}
		if len(issues) > 0 {
			out[index].ConfidenceBreakdown.Reasons = analysisUniqueStrings(append(out[index].ConfidenceBreakdown.Reasons, issues...))
			out[index].ConfidenceBreakdown.Score = analysisMinInt(out[index].ConfidenceBreakdown.Score, out[index].ConfidenceScore)
			out[index].ConfidenceBreakdown = normalizeRootCauseConfidenceBreakdown(out[index].ConfidenceBreakdown, out[index].ConfidenceScore)
			out[index].NeedsRelationNormalization()
		}
	}
	return normalizeRootCauseJoinedCandidates(out)
}

func (candidate *RootCauseJoinedCandidate) NeedsRelationNormalization() {
	candidate.CompetesWith = analysisUniqueStrings(candidate.CompetesWith)
	candidate.DependsOn = analysisUniqueStrings(candidate.DependsOn)
	candidate.CanCoexistWith = analysisUniqueStrings(candidate.CanCoexistWith)
}

func rootCauseJoinedCandidateHasValidProbe(candidate RootCauseJoinedCandidate) bool {
	for _, probe := range candidate.Probes {
		if strings.TrimSpace(probe.ExpectedSignal) != "" && strings.TrimSpace(probe.DisprovesWhen) != "" {
			return true
		}
	}
	return false
}

func rootCauseJoinedCandidateUserSymptomMismatches(snapshot ProjectSnapshot, candidate RootCauseJoinedCandidate) bool {
	expected := rootCauseReportedSymptomText(snapshot)
	actual := firstNonBlankRootCauseString(candidate.CausalChain.UserVisibleSymptom, strings.Join(candidate.JoinedChain, " "))
	if strings.TrimSpace(expected) == "" || strings.TrimSpace(actual) == "" {
		return false
	}
	expectedTerms := rootCauseEvidenceTerms(expected)
	actualTerms := rootCauseEvidenceTerms(actual)
	if len(expectedTerms) < 2 || len(actualTerms) < 1 {
		return false
	}
	for _, term := range expectedTerms {
		if rootCauseTextContainsTerm(actual, term) {
			return false
		}
	}
	for _, term := range actualTerms {
		if rootCauseTextContainsTerm(expected, term) {
			return false
		}
	}
	return true
}

func deriveRootCauseJoinedProbes(candidate RootCauseJoinedCandidate) []RootCauseProbe {
	target := firstString(candidate.EvidenceFiles)
	expected := firstNonBlankRootCauseString(firstString(candidate.RequiredRuntimeObservation), candidate.CausalChain.InvalidState, candidate.CausalChain.StateTransition, candidate.Title)
	disproves := firstNonBlankRootCauseString(firstString(candidate.CannotBeRootCauseIf), "The symptom reproduces while the joined chain is not taken.")
	probes := []RootCauseProbe{}
	for _, step := range limitStrings(candidate.VerificationSteps, 3) {
		probes = append(probes, RootCauseProbe{
			Title:          "Verify joined root-cause chain",
			Kind:           rootCauseProbeKindFromText(step),
			Target:         target,
			Command:        step,
			ExpectedSignal: expected,
			DisprovesWhen:  disproves,
		})
	}
	if len(probes) == 0 {
		probes = append(probes, RootCauseProbe{
			Title:          "Reproduce joined root-cause chain",
			Kind:           "repro",
			Target:         target,
			Command:        "Drive the reported symptom and trace the joined causal chain.",
			ExpectedSignal: expected,
			DisprovesWhen:  disproves,
		})
	}
	return normalizeRootCauseProbes(probes)
}

func dedupeRootCauseJoinedCandidates(candidates []RootCauseJoinedCandidate) []RootCauseJoinedCandidate {
	indexByKey := map[string]int{}
	out := []RootCauseJoinedCandidate{}
	for _, candidate := range candidates {
		key := rootCauseJoinedClusterKey(candidate)
		if strings.TrimSpace(candidate.ClusterID) != "" {
			key = "cluster:" + strings.TrimSpace(candidate.ClusterID)
		}
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(candidate.Title))
		}
		if existingIndex, ok := indexByKey[key]; ok {
			out[existingIndex] = mergeRootCauseJoinedCandidate(out[existingIndex], candidate, key)
			continue
		}
		candidate.ClusterID = firstNonBlankRootCauseString(candidate.ClusterID, rootCauseClusterID(key))
		indexByKey[key] = len(out)
		out = append(out, candidate)
	}
	return out
}

func mergeRootCauseJoinedCandidate(left RootCauseJoinedCandidate, right RootCauseJoinedCandidate, key string) RootCauseJoinedCandidate {
	if rootCauseClassificationRank(right.Classification) < rootCauseClassificationRank(left.Classification) {
		left.Classification = right.Classification
	}
	if right.ConfidenceScore > left.ConfidenceScore {
		left.ConfidenceScore = right.ConfidenceScore
		left.Confidence = right.Confidence
	}
	if rootCauseCausalChainStageCount(right.CausalChain) > rootCauseCausalChainStageCount(left.CausalChain) {
		left.CausalChain = right.CausalChain
	}
	left.ClusterID = firstNonBlankRootCauseString(left.ClusterID, right.ClusterID, rootCauseClusterID(key))
	left.ClusterMembers = analysisUniqueStrings(append(append(left.ClusterMembers, right.ClusterMembers...), right.Title))
	left.CompetesWith = analysisUniqueStrings(append(left.CompetesWith, right.CompetesWith...))
	left.DependsOn = analysisUniqueStrings(append(left.DependsOn, right.DependsOn...))
	left.CanCoexistWith = analysisUniqueStrings(append(left.CanCoexistWith, right.CanCoexistWith...))
	left.JoinedChain = analysisUniqueStrings(append(left.JoinedChain, right.JoinedChain...))
	left.SupportingCandidates = analysisUniqueStrings(append(left.SupportingCandidates, right.SupportingCandidates...))
	left.EvidenceFiles = analysisUniqueStrings(append(left.EvidenceFiles, right.EvidenceFiles...))
	left.DisconfirmingEvidence = analysisUniqueStrings(append(left.DisconfirmingEvidence, right.DisconfirmingEvidence...))
	left.CannotBeRootCauseIf = analysisUniqueStrings(append(left.CannotBeRootCauseIf, right.CannotBeRootCauseIf...))
	left.RequiredRuntimeObservation = analysisUniqueStrings(append(left.RequiredRuntimeObservation, right.RequiredRuntimeObservation...))
	left.VerificationSteps = analysisUniqueStrings(append(left.VerificationSteps, right.VerificationSteps...))
	left.Probes = normalizeRootCauseProbes(append(left.Probes, right.Probes...))
	left.ConfidenceBreakdown = mergeRootCauseConfidenceBreakdown(left.ConfidenceBreakdown, right.ConfidenceBreakdown, left.ConfidenceScore)
	return left
}

func inferRootCauseJoinedCandidateRelations(candidates []RootCauseJoinedCandidate) []RootCauseJoinedCandidate {
	out := append([]RootCauseJoinedCandidate(nil), candidates...)
	for i := range out {
		for j := range out {
			if i == j {
				continue
			}
			leftID := firstNonBlankRootCauseString(out[i].ClusterID, out[i].Title)
			rightID := firstNonBlankRootCauseString(out[j].ClusterID, out[j].Title)
			if leftID == "" || rightID == "" {
				continue
			}
			if rootCauseJoinedCandidatesCompete(out[i], out[j]) {
				out[i].CompetesWith = analysisUniqueStrings(append(out[i].CompetesWith, rightID))
				continue
			}
			if rootCauseJoinedCandidateDependsOn(out[i], out[j]) {
				out[i].DependsOn = analysisUniqueStrings(append(out[i].DependsOn, rightID))
				continue
			}
			if rootCauseJoinedCandidatesCanCoexist(out[i], out[j]) {
				out[i].CanCoexistWith = analysisUniqueStrings(append(out[i].CanCoexistWith, rightID))
			}
		}
	}
	return out
}

func rootCauseJoinedCandidatesCompete(left RootCauseJoinedCandidate, right RootCauseJoinedCandidate) bool {
	if left.ClusterID != "" && left.ClusterID == right.ClusterID {
		return false
	}
	if normalizeRootCauseClassification(left.Classification) == "unknown" || normalizeRootCauseClassification(right.Classification) == "unknown" {
		return false
	}
	if rootCauseEvidenceFilesOverlap(left.EvidenceFiles, right.EvidenceFiles) && rootCauseCausalSymptomOverlap(left.CausalChain, right.CausalChain) {
		return true
	}
	return false
}

func rootCauseJoinedCandidateDependsOn(left RootCauseJoinedCandidate, right RootCauseJoinedCandidate) bool {
	rightTitle := rootCauseComparableText(right.Title)
	rightCluster := rootCauseComparableText(right.ClusterID)
	if rightTitle == "" && rightCluster == "" {
		return false
	}
	corpus := rootCauseComparableText(strings.Join(append(append([]string{}, left.JoinedChain...), left.RequiredRuntimeObservation...), " "))
	return (rightTitle != "" && strings.Contains(corpus, rightTitle)) || (rightCluster != "" && strings.Contains(corpus, rightCluster))
}

func rootCauseJoinedCandidatesCanCoexist(left RootCauseJoinedCandidate, right RootCauseJoinedCandidate) bool {
	if rootCauseJoinedCandidatesCompete(left, right) {
		return false
	}
	if normalizeRootCauseClassification(left.Classification) != normalizeRootCauseClassification(right.Classification) {
		return true
	}
	return !rootCauseEvidenceFilesOverlap(left.EvidenceFiles, right.EvidenceFiles)
}

func rootCauseEvidenceFilesOverlap(left []string, right []string) bool {
	set := map[string]struct{}{}
	for _, item := range left {
		set[cleanEvidencePath(item)] = struct{}{}
	}
	for _, item := range right {
		if _, ok := set[cleanEvidencePath(item)]; ok {
			return true
		}
	}
	return false
}

func rootCauseCausalSymptomOverlap(left RootCauseCausalChain, right RootCauseCausalChain) bool {
	leftText := rootCauseComparableText(strings.Join([]string{left.InvalidState, left.StateTransition, left.MissingGuard, left.UserVisibleSymptom}, " "))
	rightText := rootCauseComparableText(strings.Join([]string{right.InvalidState, right.StateTransition, right.MissingGuard, right.UserVisibleSymptom}, " "))
	if leftText == "" || rightText == "" {
		return false
	}
	for _, term := range rootCauseEvidenceTerms(leftText) {
		if rootCauseTextContainsTerm(rightText, term) {
			return true
		}
	}
	return false
}

func mergeRootCauseConfidenceBreakdown(left RootCauseConfidenceBreakdown, right RootCauseConfidenceBreakdown, fallbackScore int) RootCauseConfidenceBreakdown {
	left.CausalCompleteness = analysisMaxInt(left.CausalCompleteness, right.CausalCompleteness)
	left.EvidenceStrength = analysisMaxInt(left.EvidenceStrength, right.EvidenceStrength)
	left.RuntimeObservability = analysisMaxInt(left.RuntimeObservability, right.RuntimeObservability)
	left.AlternativeExplanations = analysisMaxInt(left.AlternativeExplanations, right.AlternativeExplanations)
	left.DisconfirmationStrength = analysisMaxInt(left.DisconfirmationStrength, right.DisconfirmationStrength)
	left.Score = analysisMaxInt(left.Score, right.Score)
	left.Reasons = analysisUniqueStrings(append(left.Reasons, right.Reasons...))
	return normalizeRootCauseConfidenceBreakdown(left, fallbackScore)
}

func rootCauseJoinedClusterKey(candidate RootCauseJoinedCandidate) string {
	parts := []string{}
	chainKey := strings.Join([]string{
		rootCauseComparableText(candidate.CausalChain.InvalidState),
		rootCauseComparableText(candidate.CausalChain.StateTransition),
		rootCauseComparableText(candidate.CausalChain.MissingGuard),
		rootCauseComparableText(candidate.CausalChain.UserVisibleSymptom),
	}, " ")
	if strings.TrimSpace(chainKey) != "" {
		parts = append(parts, chainKey)
	}
	if len(candidate.EvidenceFiles) > 0 {
		files := append([]string(nil), candidate.EvidenceFiles...)
		sort.Strings(files)
		parts = append(parts, strings.Join(limitStrings(files, 3), "|"))
	}
	title := rootCauseComparableText(candidate.Title)
	if title != "" && len(parts) == 0 {
		parts = append(parts, title)
	}
	return strings.Join(analysisUniqueStrings(parts), "\x00")
}

func rootCauseCandidateClusterKey(candidate RootCauseCandidate) string {
	return rootCauseJoinedClusterKey(RootCauseJoinedCandidate{
		Title:         candidate.Title,
		CausalChain:   candidate.CausalChain,
		EvidenceFiles: candidate.EvidenceFiles,
	})
}

func rootCauseClusterID(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		key = "root-cause-candidate"
	}
	sum := sha256.Sum256([]byte(key))
	return "rc-" + hex.EncodeToString(sum[:])[:10]
}

func normalizeRootCauseCandidateClusters(items []RootCauseCandidateCluster) []RootCauseCandidateCluster {
	out := []RootCauseCandidateCluster{}
	for _, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.Title = strings.TrimSpace(item.Title)
		item.CandidateTitles = analysisUniqueStrings(item.CandidateTitles)
		item.ShardIDs = analysisUniqueStrings(item.ShardIDs)
		item.EvidenceFiles = analysisUniqueStrings(item.EvidenceFiles)
		item.CausalChain = normalizeJoinedRootCauseCausalChain(item.CausalChain)
		item.ConfidenceScore = clampRootCauseScore(item.ConfidenceScore)
		if item.Title == "" && len(item.CandidateTitles) == 0 {
			continue
		}
		if item.Title == "" {
			item.Title = firstString(item.CandidateTitles)
		}
		if item.ID == "" {
			item.ID = rootCauseClusterID(strings.Join(append(item.CandidateTitles, item.EvidenceFiles...), "\x00"))
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].ConfidenceScore == out[j].ConfidenceScore {
			return out[i].ID < out[j].ID
		}
		return out[i].ConfidenceScore > out[j].ConfidenceScore
	})
	return out
}

func buildRootCauseCandidateClusters(shards []AnalysisShard, reports []WorkerReport, reviews []ReviewDecision, joined []RootCauseJoinedCandidate) []RootCauseCandidateCluster {
	clustersByID := map[string]int{}
	out := []RootCauseCandidateCluster{}
	shardIDsByCandidate := rootCauseCandidateShardIndex(shards, reports)
	addCluster := func(id string, title string, candidateTitles []string, shardIDs []string, evidenceFiles []string, chain RootCauseCausalChain, score int) {
		id = firstNonBlankRootCauseString(id, rootCauseClusterID(strings.Join(append(candidateTitles, evidenceFiles...), "\x00")))
		if existing, ok := clustersByID[id]; ok {
			item := &out[existing]
			item.CandidateTitles = analysisUniqueStrings(append(item.CandidateTitles, candidateTitles...))
			item.ShardIDs = analysisUniqueStrings(append(item.ShardIDs, shardIDs...))
			item.EvidenceFiles = analysisUniqueStrings(append(item.EvidenceFiles, evidenceFiles...))
			if rootCauseCausalChainStageCount(chain) > rootCauseCausalChainStageCount(item.CausalChain) {
				item.CausalChain = chain
			}
			if score > item.ConfidenceScore {
				item.ConfidenceScore = score
			}
			return
		}
		clustersByID[id] = len(out)
		out = append(out, RootCauseCandidateCluster{
			ID:              id,
			Title:           firstNonBlankRootCauseString(title, firstString(candidateTitles), "Root-cause cluster"),
			CandidateTitles: analysisUniqueStrings(candidateTitles),
			ShardIDs:        analysisUniqueStrings(shardIDs),
			EvidenceFiles:   analysisUniqueStrings(evidenceFiles),
			CausalChain:     chain,
			ConfidenceScore: clampRootCauseScore(score),
		})
	}
	for _, candidate := range joined {
		candidateTitles := append([]string{candidate.Title}, candidate.ClusterMembers...)
		addCluster(candidate.ClusterID, candidate.Title, candidateTitles, rootCauseShardIDsForCandidateTitles(candidateTitles, shardIDsByCandidate), candidate.EvidenceFiles, candidate.CausalChain, candidate.ConfidenceScore)
	}
	if len(out) == 0 {
		for index, report := range reports {
			if index >= len(shards) {
				continue
			}
			review := ReviewDecision{}
			if index < len(reviews) {
				review = reviews[index]
			}
			if !rootCauseReviewApprovesSymptomCausality(review) {
				continue
			}
			for _, candidate := range report.RootCauseCandidates {
				key := rootCauseCandidateClusterKey(candidate)
				addCluster(rootCauseClusterID(key), candidate.Title, []string{candidate.Title}, []string{shards[index].ID}, candidate.EvidenceFiles, candidate.CausalChain, rootCauseConfidenceScore(candidate, review))
			}
		}
	}
	return normalizeRootCauseCandidateClusters(out)
}

func rootCauseCandidateShardIndex(shards []AnalysisShard, reports []WorkerReport) map[string][]string {
	out := map[string][]string{}
	for index, report := range reports {
		if index >= len(shards) {
			continue
		}
		for _, candidate := range report.RootCauseCandidates {
			key := rootCauseComparableText(candidate.Title)
			if key == "" {
				continue
			}
			out[key] = analysisUniqueStrings(append(out[key], shards[index].ID))
		}
	}
	return out
}

func rootCauseShardIDsForCandidateTitles(titles []string, shardIDsByCandidate map[string][]string) []string {
	out := []string{}
	for _, title := range titles {
		key := rootCauseComparableText(title)
		if key == "" {
			continue
		}
		out = append(out, shardIDsByCandidate[key]...)
		for existing, shardIDs := range shardIDsByCandidate {
			if rootCauseComparableTextIsSpecific(key) && strings.Contains(existing, key) {
				out = append(out, shardIDs...)
				continue
			}
			if rootCauseComparableTextIsSpecific(existing) && strings.Contains(key, existing) {
				out = append(out, shardIDs...)
			}
		}
	}
	return analysisUniqueStrings(out)
}

func normalizeJoinedRootCauseCausalChain(chain RootCauseCausalChain) RootCauseCausalChain {
	chain.Trigger = strings.TrimSpace(chain.Trigger)
	chain.InvalidState = strings.TrimSpace(chain.InvalidState)
	chain.StateTransition = strings.TrimSpace(chain.StateTransition)
	chain.MissingGuard = strings.TrimSpace(chain.MissingGuard)
	chain.UserVisibleSymptom = strings.TrimSpace(chain.UserVisibleSymptom)
	chain.EvidenceFiles = analysisUniqueStrings(chain.EvidenceFiles)
	return chain
}

func normalizeRootCauseClassification(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "root_cause", "root cause", "cause":
		return "root_cause"
	case "contributing_factor", "contributing factor", "factor":
		return "contributing_factor"
	case "detection_gap", "detection gap", "gap":
		return "detection_gap"
	case "operational_trigger", "operational trigger", "trigger":
		return "operational_trigger"
	default:
		return "unknown"
	}
}

func rootCauseClassificationRank(classification string) int {
	switch normalizeRootCauseClassification(classification) {
	case "root_cause":
		return 0
	case "contributing_factor":
		return 1
	case "detection_gap":
		return 2
	case "operational_trigger":
		return 3
	default:
		return 4
	}
}

func normalizeRootCauseConfidence(raw string, score int) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(raw))
	}
	if score >= 75 {
		return "high"
	}
	if score >= 45 {
		return "medium"
	}
	return "low"
}

func (a *projectAnalyzer) synthesizeFinalDocument(ctx context.Context, snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, goal string) (string, error) {
	resp, err := a.completeAnalysisRequestWithRetry(ctx, a.client, "synthesis", "", a.cfg.Model, ChatRequest{
		Model:       a.cfg.Model,
		System:      synthesisSystemPrompt(),
		Messages:    []Message{{Role: "user", Text: buildSynthesisPrompt(snapshot, shards, reports, goal)}},
		MaxTokens:   a.cfg.MaxTokens,
		Temperature: a.cfg.Temperature,
		WorkingDir:  a.workspace.Root,
	})
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("analysis synthesis request failed for model=%s: %w", a.cfg.Model, err)
		}
		a.debug(fmt.Sprintf("synthesis soft-failed: model=%s error=%v", a.cfg.Model, err))
		return "# Analysis With Provider Failures\n\nThe synthesis model request failed after retry, so Kernforge generated this fallback document from available shard reports. Treat sections from failed shards as low confidence and rerun `/analyze-project` when provider rate limits recover.\n\n" + fallbackFinalDocument(snapshot, shards, reports, goal), nil
	}
	text := strings.TrimSpace(resp.Message.Text)
	if text == "" {
		text = fallbackFinalDocument(snapshot, shards, reports, goal)
	} else {
		text = ensureFinalDocumentInsights(text, snapshot, shards, reports)
	}
	return text, nil
}

type synthesisSection struct {
	Title               string               `json:"title"`
	Group               string               `json:"group,omitempty"`
	ShardIDs            []string             `json:"shard_ids"`
	ShardNames          []string             `json:"shard_names,omitempty"`
	CacheStatuses       []string             `json:"cache_statuses,omitempty"`
	InvalidationReasons []string             `json:"invalidation_reasons,omitempty"`
	InvalidationDiff    []string             `json:"invalidation_diff,omitempty"`
	InvalidationChanges []InvalidationChange `json:"invalidation_changes,omitempty"`
	Responsibilities    []string             `json:"responsibilities,omitempty"`
	Facts               []string             `json:"facts,omitempty"`
	Inferences          []string             `json:"inferences,omitempty"`
	KeyFiles            []string             `json:"key_files,omitempty"`
	EvidenceFiles       []string             `json:"evidence_files,omitempty"`
	EntryPoints         []string             `json:"entry_points,omitempty"`
	InternalFlow        []string             `json:"internal_flow,omitempty"`
	Dependencies        []string             `json:"dependencies,omitempty"`
	Collaboration       []string             `json:"collaboration,omitempty"`
	Risks               []string             `json:"risks,omitempty"`
	Unknowns            []string             `json:"unknowns,omitempty"`
	RootCauseCandidates []RootCauseCandidate `json:"root_cause_candidates,omitempty"`
}

func groupedReportsForSynthesis(shards []AnalysisShard, reports []WorkerReport) []synthesisSection {
	items := []synthesisSection{}
	used := map[int]struct{}{}
	tinyNames := map[string]struct{}{
		"project_manifest": {},
		"ops_scripts":      {},
		"error_artifacts":  {},
		".build":           {},
		"release":          {},
	}

	operational := synthesisSection{
		Title: "Operational Metadata And Scripts",
		Group: "Operational Metadata",
	}
	for i := range reports {
		if i >= len(shards) {
			continue
		}
		if _, ok := tinyNames[shards[i].Name]; ok {
			used[i] = struct{}{}
			mergeSynthesisSection(&operational, shards[i], reports[i])
		}
	}
	if len(operational.ShardIDs) > 0 {
		items = append(items, operational)
	}
	for i := range reports {
		if i >= len(shards) {
			continue
		}
		if _, ok := used[i]; ok {
			continue
		}
		section := synthesisSection{
			Title: reports[i].Title,
			Group: synthesisGroupForShard(shards[i], reports[i]),
		}
		mergeSynthesisSection(&section, shards[i], reports[i])
		items = append(items, section)
	}
	return orderSynthesisSections(items)
}

func mergeSynthesisSection(section *synthesisSection, shard AnalysisShard, report WorkerReport) {
	section.ShardIDs = append(section.ShardIDs, shard.ID)
	section.ShardNames = analysisUniqueStrings(append(section.ShardNames, strings.TrimSpace(shard.Name)))
	if strings.TrimSpace(section.Title) == "" {
		section.Title = report.Title
	}
	if status := strings.TrimSpace(shard.CacheStatus); status != "" {
		section.CacheStatuses = analysisUniqueStrings(append(section.CacheStatuses, status))
	}
	if reason := strings.TrimSpace(shard.InvalidationReason); reason != "" {
		section.InvalidationReasons = analysisUniqueStrings(append(section.InvalidationReasons, reason))
	}
	section.InvalidationDiff = analysisUniqueStrings(append(section.InvalidationDiff, shard.InvalidationDiff...))
	section.InvalidationChanges = append(section.InvalidationChanges, shard.InvalidationChanges...)
	section.Responsibilities = analysisUniqueStrings(append(section.Responsibilities, report.Responsibilities...))
	section.Facts = analysisUniqueStrings(append(section.Facts, report.Facts...))
	section.Inferences = analysisUniqueStrings(append(section.Inferences, report.Inferences...))
	section.KeyFiles = analysisUniqueStrings(append(section.KeyFiles, report.KeyFiles...))
	section.EvidenceFiles = analysisUniqueStrings(append(section.EvidenceFiles, report.EvidenceFiles...))
	section.EntryPoints = analysisUniqueStrings(append(section.EntryPoints, report.EntryPoints...))
	section.InternalFlow = analysisUniqueStrings(append(section.InternalFlow, report.InternalFlow...))
	section.Dependencies = analysisUniqueStrings(append(section.Dependencies, report.Dependencies...))
	section.Collaboration = analysisUniqueStrings(append(section.Collaboration, report.Collaboration...))
	section.Risks = analysisUniqueStrings(append(section.Risks, report.Risks...))
	section.Unknowns = analysisUniqueStrings(append(section.Unknowns, report.Unknowns...))
	section.RootCauseCandidates = append(section.RootCauseCandidates, report.RootCauseCandidates...)
}

func ensureFinalDocumentInsights(text string, snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return trimmed
	}
	items := groupedReportsForSynthesis(shards, reports)
	trimmed = normalizeFinalDocumentHeadings(trimmed)
	if normalizeProjectAnalysisMode(snapshot.AnalysisMode) != "root-cause" {
		trimmed = ensureStartupProjectCoverage(trimmed, snapshot)
		trimmed = ensureExecutionChainCoverage(trimmed, snapshot, reports)
		trimmed = ensureSecuritySurfaceCoverage(trimmed, snapshot, items)
	}
	trimmed = ensureAnalysisExecutionCoverage(trimmed, shards)
	trimmed = normalizeUnexpectedLocaleArtifacts(trimmed)
	missingEvidence := sectionsMissingCoverage(trimmed, items, "evidence")
	missingInsights := sectionsMissingCoverage(trimmed, items, "insights")
	needsEvidenceAppendix := len(missingEvidence) > 0
	needsInsightAppendix := len(missingInsights) > 0
	if !needsEvidenceAppendix && !needsInsightAppendix {
		return trimmed
	}

	var b strings.Builder
	b.WriteString(trimmed)

	if needsEvidenceAppendix {
		b.WriteString("\n\n## Evidence Files Appendix\n\n")
		writeEvidenceAppendix(&b, missingEvidence)
	}
	if needsInsightAppendix {
		b.WriteString("\n## Evidence And Inference Appendix\n\n")
		writeInsightAppendix(&b, missingInsights)
	}
	return strings.TrimSpace(b.String())
}

func ensureSecuritySurfaceCoverage(document string, snapshot ProjectSnapshot, items []synthesisSection) string {
	if !strings.EqualFold(strings.TrimSpace(snapshot.AnalysisMode), "security") {
		return document
	}
	lower := strings.ToLower(document)
	if strings.Contains(lower, "security surface decomposition") {
		return document
	}
	snippet := buildSecuritySurfaceDecompositionSection(items)
	if strings.TrimSpace(snippet) == "" {
		return document
	}
	updated := insertBeforeSection(document, "## Subsystem Breakdown", snippet)
	if updated != document {
		return updated
	}
	return strings.TrimSpace(document) + "\n\n" + snippet
}

func buildSecuritySurfaceDecompositionSection(items []synthesisSection) string {
	type securitySurfaceSpec struct {
		ShardName string
		Title     string
	}
	specs := []securitySurfaceSpec{
		{ShardName: "security_driver", Title: "Driver Surface"},
		{ShardName: "security_ioctl", Title: "IOCTL Surface"},
		{ShardName: "security_handles", Title: "Handle Surface"},
		{ShardName: "security_memory", Title: "Memory Surface"},
		{ShardName: "security_rpc", Title: "RPC Surface"},
	}
	surfaces := []synthesisSection{}
	for _, spec := range specs {
		combined := synthesisSection{Title: spec.Title}
		for _, item := range items {
			if !containsStringFold(item.ShardNames, spec.ShardName) {
				continue
			}
			mergeSynthesisSections(&combined, item)
		}
		if len(combined.ShardNames) == 0 {
			continue
		}
		surfaces = append(surfaces, combined)
	}
	if len(surfaces) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Security Surface Decomposition\n\n")
	b.WriteString("This section breaks the security analysis into the primary privileged or abuse-sensitive surfaces that should be reviewed independently.\n\n")
	for _, surface := range surfaces {
		fmt.Fprintf(&b, "### %s\n\n", surface.Title)
		if len(surface.Responsibilities) > 0 {
			b.WriteString("Responsibilities:\n")
			for _, item := range limitStrings(surface.Responsibilities, 3) {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n")
		}
		findings := analysisUniqueStrings(append(append([]string{}, surface.Facts...), surface.Inferences...))
		if len(findings) > 0 {
			b.WriteString("Findings:\n")
			for _, item := range limitStrings(findings, 4) {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n")
		}
		entryPoints := analysisUniqueStrings(append(append([]string{}, surface.EntryPoints...), surface.InternalFlow...))
		if len(entryPoints) > 0 {
			b.WriteString("Entry points and flow:\n")
			for _, item := range limitStrings(entryPoints, 4) {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n")
		}
		keyFiles := analysisUniqueStrings(append(append([]string{}, surface.KeyFiles...), surface.EvidenceFiles...))
		if len(keyFiles) > 0 {
			b.WriteString("Key files:\n")
			for _, item := range limitStrings(keyFiles, 4) {
				fmt.Fprintf(&b, "- `%s`\n", item)
			}
			b.WriteString("\n")
		}
		if len(surface.Risks) > 0 {
			b.WriteString("Risks:\n")
			for _, item := range limitStrings(surface.Risks, 3) {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func mergeSynthesisSections(target *synthesisSection, source synthesisSection) {
	target.ShardIDs = analysisUniqueStrings(append(target.ShardIDs, source.ShardIDs...))
	target.ShardNames = analysisUniqueStrings(append(target.ShardNames, source.ShardNames...))
	target.CacheStatuses = analysisUniqueStrings(append(target.CacheStatuses, source.CacheStatuses...))
	target.InvalidationReasons = analysisUniqueStrings(append(target.InvalidationReasons, source.InvalidationReasons...))
	target.InvalidationDiff = analysisUniqueStrings(append(target.InvalidationDiff, source.InvalidationDiff...))
	target.InvalidationChanges = append(target.InvalidationChanges, source.InvalidationChanges...)
	target.Responsibilities = analysisUniqueStrings(append(target.Responsibilities, source.Responsibilities...))
	target.Facts = analysisUniqueStrings(append(target.Facts, source.Facts...))
	target.Inferences = analysisUniqueStrings(append(target.Inferences, source.Inferences...))
	target.KeyFiles = analysisUniqueStrings(append(target.KeyFiles, source.KeyFiles...))
	target.EvidenceFiles = analysisUniqueStrings(append(target.EvidenceFiles, source.EvidenceFiles...))
	target.EntryPoints = analysisUniqueStrings(append(target.EntryPoints, source.EntryPoints...))
	target.InternalFlow = analysisUniqueStrings(append(target.InternalFlow, source.InternalFlow...))
	target.Dependencies = analysisUniqueStrings(append(target.Dependencies, source.Dependencies...))
	target.Collaboration = analysisUniqueStrings(append(target.Collaboration, source.Collaboration...))
	target.Risks = analysisUniqueStrings(append(target.Risks, source.Risks...))
	target.Unknowns = analysisUniqueStrings(append(target.Unknowns, source.Unknowns...))
}

func ensureAnalysisExecutionCoverage(text string, shards []AnalysisShard) string {
	summary := buildAnalysisExecutionSummary(shards)
	if summary.TotalShards == 0 {
		return text
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "analysis execution") || strings.Contains(lower, "semantic invalidation") || strings.Contains(lower, "cache status") {
		return text
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(text))
	b.WriteString("\n\n## Analysis Execution Appendix\n\n")
	writeAnalysisExecutionSummary(&b, summary)
	return strings.TrimSpace(b.String())
}

func writeEvidenceAppendix(b *strings.Builder, items []synthesisSection) {
	external := []synthesisSection{}
	for _, item := range items {
		if item.Group == "External Dependencies" {
			external = append(external, item)
			continue
		}
		if len(item.EvidenceFiles) == 0 {
			continue
		}
		fmt.Fprintf(b, "### %s\n\n", canonicalSynthesisTitle(item))
		limit := len(item.EvidenceFiles)
		if limit > 5 {
			limit = 5
		}
		b.WriteString("Evidence files:\n")
		for _, evidence := range item.EvidenceFiles[:limit] {
			fmt.Fprintf(b, "- %s\n", evidence)
		}
		b.WriteString("\n")
	}
	if len(external) > 0 {
		b.WriteString("### External Dependencies: Dependency Catalog\n\n")
		for _, item := range external {
			if len(item.EvidenceFiles) == 0 {
				continue
			}
			fmt.Fprintf(b, "- %s: %s\n", strings.TrimSpace(item.Title), strings.Join(limitStrings(item.EvidenceFiles, 3), ", "))
		}
		b.WriteString("\n")
	}
}

func writeInsightAppendix(b *strings.Builder, items []synthesisSection) {
	external := []synthesisSection{}
	for _, item := range items {
		if item.Group == "External Dependencies" {
			external = append(external, item)
			continue
		}
		if len(item.Facts) == 0 && len(item.Inferences) == 0 {
			continue
		}
		fmt.Fprintf(b, "### %s\n\n", canonicalSynthesisTitle(item))
		if len(item.Facts) > 0 {
			b.WriteString("Facts:\n")
			for _, fact := range item.Facts {
				fmt.Fprintf(b, "- %s\n", fact)
			}
			b.WriteString("\n")
		}
		if len(item.Inferences) > 0 {
			b.WriteString("Inferences:\n")
			for _, inference := range item.Inferences {
				fmt.Fprintf(b, "- %s\n", inference)
			}
			b.WriteString("\n")
		}
	}
	if len(external) > 0 {
		b.WriteString("### External Dependencies: Dependency Catalog\n\n")
		for _, item := range external {
			summary := compactDependencyInsight(item)
			if strings.TrimSpace(summary) == "" {
				continue
			}
			fmt.Fprintf(b, "- %s: %s\n", strings.TrimSpace(item.Title), summary)
		}
		b.WriteString("\n")
	}
}

func compactDependencyInsight(item synthesisSection) string {
	parts := []string{}
	if len(item.Facts) > 0 {
		parts = append(parts, "facts="+strings.Join(limitStrings(item.Facts, 2), "; "))
	}
	if len(item.Inferences) > 0 {
		parts = append(parts, "inferences="+strings.Join(limitStrings(item.Inferences, 1), "; "))
	}
	if len(item.Dependencies) > 0 {
		parts = append(parts, "dependencies="+strings.Join(limitStrings(item.Dependencies, 2), "; "))
	}
	return strings.Join(parts, " | ")
}

func limitStrings(items []string, limit int) []string {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]string(nil), items...)
	}
	return append([]string(nil), items[:limit]...)
}

func startupProjectEntryFiles(snapshot ProjectSnapshot) []string {
	if strings.TrimSpace(snapshot.PrimaryStartup) == "" {
		return nil
	}
	for _, project := range snapshot.SolutionProjects {
		if strings.EqualFold(project.Name, snapshot.PrimaryStartup) {
			files := append([]string(nil), project.EntryFiles...)
			sort.SliceStable(files, func(i int, j int) bool {
				left := snapshot.FilesByPath[files[i]]
				right := snapshot.FilesByPath[files[j]]
				if left.ImportanceScore == right.ImportanceScore {
					return files[i] < files[j]
				}
				return left.ImportanceScore > right.ImportanceScore
			})
			return files
		}
	}
	return nil
}

func ensureStartupProjectCoverage(document string, snapshot ProjectSnapshot) string {
	startup := strings.TrimSpace(snapshot.PrimaryStartup)
	if startup == "" {
		return document
	}
	entryFiles := startupProjectEntryFiles(snapshot)
	lower := strings.ToLower(document)
	if strings.Contains(lower, "solution startup candidate") || strings.Contains(lower, "primary startup project") {
		return document
	}

	snippet := buildStartupCoverageSnippet(snapshot, startup, entryFiles)
	updated := injectIntoSection(document, "## 1. Project Overview", snippet)
	if updated != document {
		return updated
	}
	updated = injectIntoSection(document, "## Project Overview", snippet)
	if updated != document {
		return updated
	}
	return snippet + "\n\n" + document
}

func buildStartupCoverageSnippet(snapshot ProjectSnapshot, startup string, entryFiles []string) string {
	var b strings.Builder
	runtimeEdges := runtimeEdgesForStartup(snapshot.RuntimeEdges, startup)
	b.WriteString("Solution startup candidate:\n")
	fmt.Fprintf(&b, "- `%s`\n", startup)
	if len(entryFiles) > 0 {
		b.WriteString("Representative startup-candidate entry files:\n")
		for _, item := range limitStrings(entryFiles, 3) {
			fmt.Fprintf(&b, "- `%s`\n", item)
		}
	}
	if len(snapshot.StartupProjects) > 1 {
		aux := []string{}
		for _, item := range snapshot.StartupProjects {
			if !strings.EqualFold(item, startup) {
				aux = append(aux, item)
			}
		}
		if len(aux) > 0 {
			b.WriteString("Auxiliary executable projects:\n")
			for _, item := range limitStrings(aux, 5) {
				fmt.Fprintf(&b, "- `%s`\n", item)
			}
		}
	}
	if len(runtimeEdges) > 0 {
		b.WriteString("High-confidence runtime chain:\n")
		for _, edge := range limitRuntimeEdges(runtimeEdges, 5) {
			fmt.Fprintf(&b, "- `%s -> %s` (%s)\n", edge.Source, edge.Target, edge.Kind)
		}
	}
	driverEntries := driverEntrypointFiles(ProjectAnalysisRun{Snapshot: snapshot})
	if len(driverEntries) > 0 {
		b.WriteString("Driver/runtime entry files:\n")
		for _, item := range limitStrings(driverEntries, 5) {
			fmt.Fprintf(&b, "- `%s`\n", item)
		}
	}
	b.WriteString("Startup interpretation:\n")
	fmt.Fprintf(&b, "- Treat `%s` as the solution-level startup candidate. If the project contains service, DLL, worker, or driver modules, document their runtime entrypoints as separate activation layers instead of calling the startup executable the sole entrypoint.\n", startup)
	return strings.TrimSpace(b.String())
}

func ensureExecutionChainCoverage(document string, snapshot ProjectSnapshot, reports []WorkerReport) string {
	startupEdges := runtimeEdgesForStartup(snapshot.RuntimeEdges, snapshot.PrimaryStartup)
	operationalEdges := buildOperationalChain(snapshot, reports)
	if len(startupEdges) == 0 && len(operationalEdges) == 0 {
		return document
	}
	lower := strings.ToLower(document)
	if strings.Contains(lower, "primary startup chain") && strings.Contains(lower, "operational security chain") {
		return document
	}
	var b strings.Builder
	if len(startupEdges) > 0 {
		b.WriteString("Primary Startup Chain:\n")
		for _, edge := range limitRuntimeEdges(startupEdges, 5) {
			fmt.Fprintf(&b, "- `%s -> %s` (%s)\n", edge.Source, edge.Target, edge.Kind)
		}
	}
	if len(operationalEdges) > 0 {
		b.WriteString("Operational Security Chain:\n")
		for _, edge := range limitRuntimeEdges(operationalEdges, 6) {
			fmt.Fprintf(&b, "- `%s -> %s` (%s, confidence=%s)\n", edge.Source, edge.Target, edge.Kind, edge.Confidence)
		}
	}
	snippet := strings.TrimSpace(b.String())
	updated := injectIntoSection(document, "## 3. Execution Flow And Entry Points", snippet)
	if updated != document {
		return updated
	}
	updated = injectIntoSection(document, "## Execution Flow And Entry Points", snippet)
	if updated != document {
		return updated
	}
	return document
}

func runtimeEdgesForStartup(edges []RuntimeEdge, startup string) []RuntimeEdge {
	high := highConfidenceRuntimeEdges(edges)
	if strings.TrimSpace(startup) == "" || len(high) == 0 {
		return high
	}
	out := []RuntimeEdge{}
	seen := map[string]struct{}{}
	include := func(edge RuntimeEdge) {
		key := edge.Source + "->" + edge.Target + ":" + edge.Kind
		if _, ok := seen[key]; ok {
			return
		}
		out = append(out, edge)
		seen[key] = struct{}{}
	}
	for _, edge := range high {
		if strings.EqualFold(edge.Source, startup) || strings.EqualFold(edge.Target, startup) {
			include(edge)
		}
	}
	for _, edge := range high {
		for _, seed := range out {
			if strings.EqualFold(edge.Source, seed.Target) || strings.EqualFold(edge.Target, seed.Source) || strings.EqualFold(edge.Source, seed.Source) {
				include(edge)
				break
			}
		}
	}
	if len(out) == 0 {
		return high
	}
	return out
}

func limitRuntimeEdges(edges []RuntimeEdge, limit int) []RuntimeEdge {
	if limit <= 0 || len(edges) == 0 {
		return nil
	}
	if len(edges) <= limit {
		return append([]RuntimeEdge(nil), edges...)
	}
	return append([]RuntimeEdge(nil), edges[:limit]...)
}

func inferOperationalEdges(snapshot ProjectSnapshot, reports []WorkerReport) []RuntimeEdge {
	projectNames := []string{}
	for _, project := range snapshot.SolutionProjects {
		projectNames = append(projectNames, project.Name)
	}
	edges := []RuntimeEdge{}
	seen := map[string]int{}
	add := func(source string, target string, kind string, confidence string, evidence string) {
		source = canonicalProjectName(source, projectNames)
		target = canonicalProjectName(target, projectNames)
		if strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" || strings.EqualFold(source, target) {
			return
		}
		key := strings.ToLower(source + "->" + target + ":" + kind)
		index, ok := seen[key]
		if !ok {
			edges = append(edges, RuntimeEdge{
				Source:     source,
				Target:     target,
				Kind:       kind,
				Confidence: confidence,
			})
			index = len(edges) - 1
			seen[key] = index
		}
		if strings.TrimSpace(evidence) != "" {
			edges[index].Evidence = analysisUniqueStrings(append(edges[index].Evidence, evidence))
		}
	}

	for _, edge := range snapshot.RuntimeEdges {
		if edge.Kind == "string_reference" && evidenceLooksOperational(edge.Evidence) {
			add(edge.Source, edge.Target, "orchestration_edge", "medium", firstString(edge.Evidence))
		}
	}

	for _, report := range reports {
		source := dominantReportProject(snapshot, report)
		if source == "" {
			continue
		}
		for _, target := range projectNames {
			if strings.EqualFold(source, target) {
				continue
			}
			score, evidence := reportProjectRelationScore(report, target)
			switch {
			case score >= 3:
				add(source, target, "collaboration_edge", "high", evidence)
			case score >= 2:
				add(source, target, "collaboration_edge", "medium", evidence)
			}
		}
	}

	sort.SliceStable(edges, func(i int, j int) bool {
		if edges[i].Source == edges[j].Source {
			if edges[i].Target == edges[j].Target {
				if edges[i].Confidence == edges[j].Confidence {
					return edges[i].Kind < edges[j].Kind
				}
				return runtimeEdgeConfidenceRank(edges[i].Confidence) > runtimeEdgeConfidenceRank(edges[j].Confidence)
			}
			return edges[i].Target < edges[j].Target
		}
		return edges[i].Source < edges[j].Source
	})
	return edges
}

func dominantReportProject(snapshot ProjectSnapshot, report WorkerReport) string {
	counts := map[string]int{}
	paths := append([]string{}, report.EvidenceFiles...)
	paths = append(paths, report.KeyFiles...)
	for _, path := range paths {
		name := projectNameForPath(snapshot, path)
		if name != "" {
			counts[name]++
		}
	}
	best := ""
	bestCount := 0
	for name, count := range counts {
		if count > bestCount {
			best = name
			bestCount = count
		}
	}
	return best
}

func projectNameForPath(snapshot ProjectSnapshot, path string) string {
	projectNames := []string{}
	for _, project := range snapshot.SolutionProjects {
		projectNames = append(projectNames, project.Name)
	}
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	best := ""
	bestLen := -1
	for _, project := range snapshot.SolutionProjects {
		dir := strings.ToLower(filepath.ToSlash(project.Directory))
		if dir == "" {
			continue
		}
		prefix := dir
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		if strings.HasPrefix(lowerPath, prefix) && len(prefix) > bestLen {
			best = canonicalProjectName(project.Name, projectNames)
			bestLen = len(prefix)
		}
	}
	return best
}

func reportProjectRelationScore(report WorkerReport, target string) (int, string) {
	targetLower := strings.ToLower(strings.TrimSpace(target))
	if targetLower == "" {
		return 0, ""
	}
	score := 0
	evidence := ""
	checkLines := func(lines []string, weight int, requireOperational bool) {
		for _, line := range lines {
			lower := strings.ToLower(line)
			if !containsProjectMention(lower, targetLower) {
				continue
			}
			if requireOperational && !containsAny(lower, "coord", "manager", "manage", "load", "spawn", "service", "monitor", "worker", "updat", "bootstrap", "handoff", "connect", "communicat", "orchestrat") {
				continue
			}
			score += weight
			if evidence == "" {
				evidence = line
			}
			return
		}
	}
	checkLines(report.Collaboration, 3, false)
	checkLines(report.InternalFlow, 2, true)
	checkLines(report.Responsibilities, 1, true)
	checkLines(report.Inferences, 1, true)
	return score, evidence
}

func containsProjectMention(text string, project string) bool {
	if strings.TrimSpace(project) == "" {
		return false
	}
	normalizedProject := normalizeProjectToken(project)
	if normalizedProject == "" {
		return false
	}
	tokens := splitAlphaNumTokens(strings.ToLower(text))
	for i := 0; i < len(tokens); i++ {
		if normalizeProjectToken(tokens[i]) == normalizedProject {
			return true
		}
		if i+1 < len(tokens) && normalizeProjectToken(tokens[i]+tokens[i+1]) == normalizedProject {
			return true
		}
	}
	return containsAny(
		strings.ToLower(text),
		project+".dll",
		project+".exe",
		project+".bin",
		project+"/",
		project+"\\",
	)
}

func splitAlphaNumTokens(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if strings.TrimSpace(field) != "" {
			out = append(out, field)
		}
	}
	return out
}

func evidenceLooksOperational(evidence []string) bool {
	for _, item := range evidence {
		lower := strings.ToLower(filepath.Base(item))
		if containsAny(lower, "manager", "core", "scheduler", "service", "loader", "monitor", "client") {
			return true
		}
	}
	return false
}

func firstString(items []string) string {
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			return item
		}
	}
	return ""
}

func firstNonBlankAnalysisString(primary string, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

func firstNonBlankRootCauseString(items ...string) string {
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			return item
		}
	}
	return ""
}

func buildOperationalChain(snapshot ProjectSnapshot, reports []WorkerReport) []RuntimeEdge {
	high := highConfidenceRuntimeEdges(snapshot.RuntimeEdges)
	operational := analysisUniqueRuntimeEdges(append([]RuntimeEdge{}, high...))
	operational = analysisUniqueRuntimeEdges(append(operational, inferOperationalEdges(snapshot, reports)...))
	start := strings.TrimSpace(snapshot.PrimaryStartup)
	current := start
	if start != "" {
		for _, edge := range high {
			if strings.EqualFold(edge.Source, start) {
				current = edge.Target
				break
			}
		}
	}
	if current == "" {
		return operational
	}
	chain := []RuntimeEdge{}
	visited := map[string]struct{}{strings.ToLower(current): {}}
	for steps := 0; steps < 6; steps++ {
		next, ok := pickNextOperationalEdge(current, operational)
		if !ok {
			break
		}
		if _, seen := visited[strings.ToLower(next.Target)]; seen {
			break
		}
		chain = append(chain, next)
		visited[strings.ToLower(next.Target)] = struct{}{}
		current = next.Target
	}
	return chain
}

func pickNextOperationalEdge(source string, edges []RuntimeEdge) (RuntimeEdge, bool) {
	candidates := []RuntimeEdge{}
	for _, edge := range edges {
		if strings.EqualFold(edge.Source, source) {
			candidates = append(candidates, edge)
		}
	}
	if len(candidates) == 0 {
		return RuntimeEdge{}, false
	}
	sort.SliceStable(candidates, func(i int, j int) bool {
		left := operationalEdgeScore(candidates[i], edges)
		right := operationalEdgeScore(candidates[j], edges)
		if left == right {
			return candidates[i].Target < candidates[j].Target
		}
		return left > right
	})
	return candidates[0], true
}

func operationalEdgeScore(edge RuntimeEdge, all []RuntimeEdge) int {
	score := runtimeEdgeConfidenceRank(edge.Confidence) * 10
	switch edge.Kind {
	case "collaboration_edge":
		score += 6
	case "orchestration_edge":
		score += 5
	case "dynamic_load":
		score += 4
	case "process_spawn":
		score += 3
	case "project_reference":
		score += 2
	}
	for _, candidate := range all {
		if strings.EqualFold(candidate.Source, edge.Target) {
			score += 2
			break
		}
	}
	return score
}

func analysisUniqueRuntimeEdges(edges []RuntimeEdge) []RuntimeEdge {
	out := make([]RuntimeEdge, 0, len(edges))
	seen := map[string]struct{}{}
	for _, edge := range edges {
		key := strings.ToLower(edge.Source + "->" + edge.Target + ":" + edge.Kind + ":" + edge.Confidence)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, edge)
	}
	return out
}

func injectIntoSection(document string, heading string, snippet string) string {
	pos := strings.Index(document, heading)
	if pos < 0 {
		return document
	}
	insertPos := pos + len(heading)
	if insertPos < len(document) && document[insertPos] == '\r' {
		insertPos++
	}
	if insertPos < len(document) && document[insertPos] == '\n' {
		insertPos++
	}
	if insertPos < len(document) && document[insertPos] == '\n' {
		insertPos++
	}
	return document[:insertPos] + snippet + "\n\n" + document[insertPos:]
}

func insertBeforeSection(document string, heading string, snippet string) string {
	pos := strings.Index(document, heading)
	if pos < 0 {
		return document
	}
	return strings.TrimRight(document[:pos], "\r\n") + "\n\n" + strings.TrimSpace(snippet) + "\n\n" + document[pos:]
}

func sectionsMissingCoverage(document string, items []synthesisSection, mode string) []synthesisSection {
	lowerDoc := strings.ToLower(document)
	missing := []synthesisSection{}
	for _, item := range items {
		title := strings.TrimSpace(item.Title)
		if title == "" {
			continue
		}
		lowerTitle := strings.ToLower(title)
		pos := strings.Index(lowerDoc, lowerTitle)
		if pos < 0 {
			missing = append(missing, item)
			continue
		}
		windowEnd := pos + 1800
		if windowEnd > len(lowerDoc) {
			windowEnd = len(lowerDoc)
		}
		window := lowerDoc[pos:windowEnd]
		switch mode {
		case "evidence":
			if len(item.EvidenceFiles) == 0 {
				continue
			}
			filtered := filterMissingEvidenceFiles(window, item.EvidenceFiles)
			if len(filtered) > 0 || !strings.Contains(window, "evidence files") {
				copy := item
				copy.EvidenceFiles = filtered
				if len(copy.EvidenceFiles) == 0 {
					copy.EvidenceFiles = item.EvidenceFiles
				}
				missing = append(missing, copy)
			}
		case "insights":
			if len(item.Facts) == 0 && len(item.Inferences) == 0 {
				continue
			}
			filteredFacts := filterMissingBullets(window, item.Facts)
			filteredInferences := filterMissingBullets(window, item.Inferences)
			if len(filteredFacts) > 0 || len(filteredInferences) > 0 || !(strings.Contains(window, "facts") && strings.Contains(window, "inferences")) {
				copy := item
				copy.Facts = filteredFacts
				copy.Inferences = filteredInferences
				if len(copy.Facts) == 0 && len(item.Facts) > 0 && !strings.Contains(window, "facts") {
					copy.Facts = item.Facts
				}
				if len(copy.Inferences) == 0 && len(item.Inferences) > 0 && !strings.Contains(window, "inferences") {
					copy.Inferences = item.Inferences
				}
				missing = append(missing, copy)
			}
		}
	}
	return missing
}

func filterMissingEvidenceFiles(window string, items []string) []string {
	out := []string{}
	lowerWindow := strings.ToLower(window)
	for _, item := range items {
		if strings.TrimSpace(item) == "" {
			continue
		}
		if !strings.Contains(lowerWindow, strings.ToLower(item)) {
			out = append(out, item)
		}
	}
	return out
}

func filterMissingBullets(window string, items []string) []string {
	out := []string{}
	lowerWindow := strings.ToLower(window)
	for _, item := range items {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if normalized == "" {
			continue
		}
		if !strings.Contains(lowerWindow, normalized) {
			out = append(out, item)
		}
	}
	return out
}

func containsStringFold(items []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}

func canonicalSynthesisTitle(item synthesisSection) string {
	group := strings.TrimSpace(item.Group)
	title := strings.TrimSpace(item.Title)
	if group == "" {
		return title
	}
	if title == "" || strings.EqualFold(group, title) {
		return group
	}
	return group + ": " + title
}

func normalizeFinalDocumentHeadings(text string) string {
	replacements := []struct {
		old string
		new string
	}{
		{"Agent Runtime Group", "Agent Runtime"},
		{"Safety Control Plane Group", "Safety Control Plane"},
		{"Evidence And Memory Plane Group", "Evidence And Memory Plane"},
		{"Developer Tooling Group", "Developer Tooling"},
		{"Operational Metadata Group", "Operational Metadata"},
		{"Core Application Group", "Core Application"},
		{"Security Control Group", "Security Control"},
		{"Forensic Analysis Group", "Forensic Analysis"},
	}
	out := text
	for _, item := range replacements {
		out = strings.ReplaceAll(out, item.old, item.new)
	}
	return out
}

func normalizeUnexpectedLocaleArtifacts(text string) string {
	replacer := strings.NewReplacer(
		"主要启动链", "Primary Startup Chain",
		"启动项目", "Primary startup project",
		"辅助可执行项目", "Auxiliary executable projects",
		"运行时图", "Runtime Graph",
	)
	return replacer.Replace(text)
}

func synthesisGroupForShard(shard AnalysisShard, report WorkerReport) string {
	if isExternalDependencyShard(shard) {
		return "External Dependencies"
	}
	if hasPathPrefix(shard.PrimaryFiles, "Common/") {
		return "Shared Infrastructure"
	}
	if hasPathPrefix(shard.PrimaryFiles, "Batch/") || hasPathPrefix(shard.PrimaryFiles, "VMProtect/") {
		return "Build And Release"
	}
	switch shard.Name {
	case "runtime", "commands", "ui_viewer", "platform_io":
		return "Agent Runtime"
	case "verification", "hooks_policy":
		return "Safety Control Plane"
	case "evidence_investigation", "memory_context":
		return "Evidence And Memory Plane"
	case "analysis_engine", "root_tests_misc", "misc_root":
		return "Developer Tooling"
	case "docs_specs", "project_manifest", "ops_scripts", "error_artifacts", ".build", "release":
		return "Operational Metadata"
	default:
		corpus := synthesisShardClassificationCorpus(shard, report)
		if containsAny(corpus, "common", "shared", "include", "headers", "contracts") {
			return "Shared Infrastructure"
		}
		if containsAny(corpus, "build", "release", "deploy", "installer", "batch", "package", "signing", "vmprotect") {
			return "Build And Release"
		}
		if containsAny(corpus, "master", "control", "security", "auth", "policy", "service", "admin", "updater", "update") {
			return "Security Control"
		}
		if containsAny(corpus, "worker", "scanner", "scan", "prefetch", "forensic", "telemetry", "collector", "triage") {
			return "Forensic Analysis"
		}
		if containsAny(corpus, "protect", "protection", "obfusc", "obfuscation", "tamper", "packing", "packer") {
			return "Protection And Obfuscation"
		}
		if containsAny(corpus, "schedule", "scheduler", "automation", "timer", "job", "task") {
			return "Scheduling And Automation"
		}
		if containsAny(corpus, "app", "client", "frontend", "main", "core") {
			return "Core Application"
		}
		if strings.Contains(strings.ToLower(report.Title), "runtime") {
			return "Agent Runtime"
		}
		return "Developer Tooling"
	}
}

func synthesisShardClassificationCorpus(shard AnalysisShard, report WorkerReport) string {
	parts := []string{shard.Name, report.Title, report.ScopeSummary, report.Narrative}
	parts = append(parts, report.Responsibilities...)
	parts = append(parts, report.Facts...)
	parts = append(parts, report.KeyFiles...)
	parts = append(parts, report.EntryPoints...)
	parts = append(parts, shard.PrimaryFiles...)
	return strings.ToLower(filepath.ToSlash(strings.Join(parts, " ")))
}

func orderSynthesisSections(items []synthesisSection) []synthesisSection {
	groupOrder := map[string]int{
		"Core Application":           0,
		"Security Control":           1,
		"Forensic Analysis":          2,
		"Protection And Obfuscation": 3,
		"Scheduling And Automation":  4,
		"Shared Infrastructure":      5,
		"Build And Release":          6,
		"Agent Runtime":              7,
		"Safety Control Plane":       8,
		"Evidence And Memory Plane":  9,
		"Developer Tooling":          10,
		"Operational Metadata":       11,
		"External Dependencies":      12,
	}
	sort.SliceStable(items, func(i int, j int) bool {
		left := groupOrder[items[i].Group]
		right := groupOrder[items[j].Group]
		if left == right {
			return items[i].Title < items[j].Title
		}
		return left < right
	})
	return items
}

func isVisualStudioCppProject(snapshot ProjectSnapshot) bool {
	for _, path := range snapshot.ManifestFiles {
		lower := strings.ToLower(filepath.ToSlash(path))
		if strings.HasSuffix(lower, ".sln") || strings.HasSuffix(lower, ".vcxproj") || strings.HasSuffix(lower, ".props") {
			return true
		}
	}
	cppCount := 0
	for _, file := range snapshot.Files {
		switch strings.ToLower(file.Extension) {
		case ".cpp", ".cc", ".cxx", ".c", ".h", ".hpp", ".hh", ".inl":
			cppCount++
		}
		if cppCount >= 30 {
			return true
		}
	}
	return false
}

func isExternalDependencyShard(shard AnalysisShard) bool {
	if len(shard.PrimaryFiles) == 0 {
		return false
	}
	externalCount := 0
	for _, path := range shard.PrimaryFiles {
		lower := strings.ToLower(filepath.ToSlash(path))
		if strings.HasPrefix(lower, "external/") || strings.HasPrefix(lower, "third_party/") || strings.HasPrefix(lower, "vendor/") {
			externalCount++
		}
	}
	return externalCount > 0 && externalCount*2 >= len(shard.PrimaryFiles)
}

func hasPathPrefix(paths []string, prefix string) bool {
	lowerPrefix := strings.ToLower(filepath.ToSlash(prefix))
	for _, path := range paths {
		if strings.HasPrefix(strings.ToLower(filepath.ToSlash(path)), lowerPrefix) {
			return true
		}
	}
	return false
}

func (a *projectAnalyzer) completeAnalysisRequestWithRetry(ctx context.Context, client ProviderClient, stage string, shardName string, model string, req ChatRequest) (ChatResponse, error) {
	maxRetries := a.analysisCfg.MaxProviderRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	baseDelay := time.Duration(a.analysisCfg.ProviderRetryDelayMs) * time.Millisecond
	if baseDelay <= 0 {
		baseDelay = 1500 * time.Millisecond
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, configRequestTimeout(a.cfg))
		resp, err := completeModelTurnOnceWithModelRoutes(attemptCtx, a.modelRoutes, modelRoutePolicyFromConfig(a.cfg), a.cfg, client, req)
		cancel()
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return ChatResponse{}, err
		}
		if !shouldRetryProviderError(err) || attempt == maxRetries {
			return ChatResponse{}, err
		}

		detail := fmt.Sprintf("analysis provider error: stage=%s shard=%s model=%s attempt=%d/%d error=%v", stage, shardName, model, attempt+1, maxRetries+1, err)
		a.debug(strings.TrimSpace(detail))
		delay := providerRetryDelay(baseDelay, attempt)
		a.status(fmt.Sprintf("Provider error during %s (%s). Retrying in %s...", stage, model, delay.Round(time.Second)))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ChatResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
	return ChatResponse{}, lastErr
}

func shouldRetryProviderError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var providerErr *ProviderAPIError
	if errors.As(err, &providerErr) {
		return providerErr.Retryable()
	}
	text := strings.ToLower(err.Error())
	retryHints := []string{
		"provider returned error",
		"deadline exceeded",
		"rate limit",
		"timeout",
		"temporarily unavailable",
		"server error",
		"bad gateway",
		"gateway timeout",
		"service unavailable",
		"429",
		"500",
		"502",
		"503",
		"504",
	}
	for _, hint := range retryHints {
		if strings.Contains(text, hint) {
			return true
		}
	}
	return false
}

func (a *projectAnalyzer) persistRun(run ProjectAnalysisRun) (string, error) {
	if err := os.MkdirAll(a.analysisCfg.OutputDir, 0o755); err != nil {
		return "", err
	}
	base := analysisArtifactBaseName(run.Summary.RunID, run.Summary.Goal, run.Summary.Mode)
	mdPath := filepath.Join(a.analysisCfg.OutputDir, base+".md")
	run.Summary.OutputPath = mdPath
	jsonPath := filepath.Join(a.analysisCfg.OutputDir, base+".json")
	knowledgeJSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_knowledge.json")
	knowledgeDigestPath := filepath.Join(a.analysisCfg.OutputDir, base+"_knowledge.md")
	architectureFactsJSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_architecture_facts.json")
	rootCauseAuditJSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_root_cause_audit.json")
	rootCauseAuditMDPath := filepath.Join(a.analysisCfg.OutputDir, base+"_root_cause_audit.md")
	performanceJSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_performance_lens.json")
	performanceDigestPath := filepath.Join(a.analysisCfg.OutputDir, base+"_performance_lens.md")
	snapshotJSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_snapshot.json")
	structuralIndexJSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_structural_index.json")
	structuralIndexV2JSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_structural_index_v2.json")
	unrealGraphJSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_unreal_graph.json")
	vectorCorpusJSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_vector_corpus.json")
	vectorCorpusJSONLPath := filepath.Join(a.analysisCfg.OutputDir, base+"_vector_corpus.jsonl")
	vectorIngestManifestPath := filepath.Join(a.analysisCfg.OutputDir, base+"_vector_ingest_manifest.json")
	vectorIngestRecordsPath := filepath.Join(a.analysisCfg.OutputDir, base+"_vector_ingest_records.jsonl")
	vectorPGVectorSQLPath := filepath.Join(a.analysisCfg.OutputDir, base+"_vector_pgvector.sql")
	vectorSQLiteSQLPath := filepath.Join(a.analysisCfg.OutputDir, base+"_vector_sqlite.sql")
	vectorQdrantJSONLPath := filepath.Join(a.analysisCfg.OutputDir, base+"_vector_qdrant.jsonl")
	docsDir := filepath.Join(a.analysisCfg.OutputDir, base+"_docs")
	dashboardPath := filepath.Join(a.analysisCfg.OutputDir, base+"_dashboard.html")
	if err := os.WriteFile(mdPath, []byte(run.FinalDocument), 0o644); err != nil {
		return "", err
	}
	shardDir := filepath.Join(a.analysisCfg.OutputDir, base+"_shards")
	if len(run.ShardDocuments) > 0 {
		if err := os.MkdirAll(shardDir, 0o755); err != nil {
			return "", err
		}
		for shardID, doc := range run.ShardDocuments {
			filename := sanitizeFileName(shardID)
			if filename == "" {
				filename = "shard"
			}
			shardPath := filepath.Join(shardDir, filename+".md")
			if err := os.WriteFile(shardPath, []byte(doc), 0o644); err != nil {
				return "", err
			}
		}
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return "", err
	}
	snapshotData, err := json.MarshalIndent(run.Snapshot, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(snapshotJSONPath, snapshotData, 0o644); err != nil {
		return "", err
	}
	if len(run.SemanticIndex.Files) > 0 || len(run.SemanticIndex.Symbols) > 0 || len(run.SemanticIndex.BuildEdges) > 0 {
		indexData, err := json.MarshalIndent(run.SemanticIndex, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(structuralIndexJSONPath, indexData, 0o644); err != nil {
			return "", err
		}
	}
	if hasSemanticIndexV2Data(run.SemanticIndexV2) {
		indexData, err := json.MarshalIndent(run.SemanticIndexV2, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(structuralIndexV2JSONPath, indexData, 0o644); err != nil {
			return "", err
		}
	}
	if len(run.UnrealGraph.Nodes) > 0 || len(run.UnrealGraph.Edges) > 0 {
		graphData, err := json.MarshalIndent(run.UnrealGraph, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(unrealGraphJSONPath, graphData, 0o644); err != nil {
			return "", err
		}
	}
	if len(run.VectorCorpus.Documents) > 0 {
		corpusData, err := json.MarshalIndent(run.VectorCorpus, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(vectorCorpusJSONPath, corpusData, 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(vectorCorpusJSONLPath, []byte(buildVectorCorpusJSONL(run.VectorCorpus)), 0o644); err != nil {
			return "", err
		}
		manifest := run.VectorIngestion
		if manifest.DocumentCount == 0 {
			manifest = buildVectorIngestionManifest(run.VectorCorpus)
		}
		manifestData, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(vectorIngestManifestPath, manifestData, 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(vectorIngestRecordsPath, []byte(buildVectorIngestionRecordsJSONL(run.VectorCorpus)), 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(vectorPGVectorSQLPath, []byte(buildVectorPGVectorSQL(run.VectorCorpus)), 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(vectorSQLiteSQLPath, []byte(buildVectorSQLiteSQL(run.VectorCorpus)), 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(vectorQdrantJSONLPath, []byte(buildVectorQdrantSeedJSONL(run.VectorCorpus)), 0o644); err != nil {
			return "", err
		}
	}
	{
		knowledgeData, err := json.MarshalIndent(run.KnowledgePack, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(knowledgeJSONPath, knowledgeData, 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(knowledgeDigestPath, []byte(buildKnowledgeDigest(run.KnowledgePack)), 0o644); err != nil {
			return "", err
		}
		factsData, err := json.MarshalIndent(run.Snapshot.ArchitectureFacts, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(architectureFactsJSONPath, factsData, 0o644); err != nil {
			return "", err
		}
		if normalizeProjectAnalysisMode(run.Summary.Mode) == "root-cause" {
			auditData, err := json.MarshalIndent(run.RootCause.AuditTrail, "", "  ")
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(rootCauseAuditJSONPath, auditData, 0o644); err != nil {
				return "", err
			}
			if err := os.WriteFile(rootCauseAuditMDPath, []byte(buildRootCauseAuditDigest(run.RootCause.AuditTrail)), 0o644); err != nil {
				return "", err
			}
		}
		docsManifest, err := writeAnalysisDocs(run, docsDir)
		if err != nil {
			return "", err
		}
		if err := writeAnalysisDashboard(run, dashboardPath, filepath.Base(docsDir)); err != nil {
			return "", err
		}
		latestDir := filepath.Join(a.analysisCfg.OutputDir, "latest")
		if err := os.RemoveAll(latestDir); err != nil {
			return "", err
		}
		if err := os.MkdirAll(latestDir, 0o755); err != nil {
			return "", err
		}
		latestDocsDir := filepath.Join(latestDir, "docs")
		latestDocsManifest, err := writeAnalysisDocs(run, latestDocsDir)
		if err != nil {
			return "", err
		}
		if err := writeAnalysisDashboard(run, filepath.Join(latestDir, "dashboard.html"), "docs"); err != nil {
			return "", err
		}
		docsManifestData, err := json.MarshalIndent(docsManifest, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(a.analysisCfg.OutputDir, base+"_docs_manifest.json"), docsManifestData, 0o644); err != nil {
			return "", err
		}
		latestDocsManifestData, err := json.MarshalIndent(latestDocsManifest, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(latestDir, "docs_manifest.json"), latestDocsManifestData, 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(latestDir, "docs_index.md"), []byte(buildAnalysisDocs(run)["INDEX.md"]), 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(latestDir, "run.json"), data, 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(latestDir, "snapshot.json"), snapshotData, 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(latestDir, "knowledge_pack.json"), knowledgeData, 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(latestDir, "architecture_digest.md"), []byte(buildKnowledgeDigest(run.KnowledgePack)), 0o644); err != nil {
			return "", err
		}
		factsData, err = json.MarshalIndent(run.Snapshot.ArchitectureFacts, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(latestDir, "architecture_facts.json"), factsData, 0o644); err != nil {
			return "", err
		}
		if normalizeProjectAnalysisMode(run.Summary.Mode) == "root-cause" {
			auditData, err := json.MarshalIndent(run.RootCause.AuditTrail, "", "  ")
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(latestDir, "root_cause_audit.json"), auditData, 0o644); err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(latestDir, "root_cause_audit.md"), []byte(buildRootCauseAuditDigest(run.RootCause.AuditTrail)), 0o644); err != nil {
				return "", err
			}
		}
		perfData, err := json.MarshalIndent(run.KnowledgePack.PerformanceLens, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(performanceJSONPath, perfData, 0o644); err != nil {
			return "", err
		}
		perfDigest := buildPerformanceLensDigest(run.KnowledgePack.PerformanceLens)
		if err := os.WriteFile(performanceDigestPath, []byte(perfDigest), 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(latestDir, "performance_lens.json"), perfData, 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(latestDir, "performance_digest.md"), []byte(perfDigest), 0o644); err != nil {
			return "", err
		}
		if len(run.SemanticIndex.Files) > 0 || len(run.SemanticIndex.Symbols) > 0 || len(run.SemanticIndex.BuildEdges) > 0 {
			indexData, err := json.MarshalIndent(run.SemanticIndex, "", "  ")
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(latestDir, "structural_index.json"), indexData, 0o644); err != nil {
				return "", err
			}
		}
		if hasSemanticIndexV2Data(run.SemanticIndexV2) {
			indexData, err := json.MarshalIndent(run.SemanticIndexV2, "", "  ")
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(latestDir, "structural_index_v2.json"), indexData, 0o644); err != nil {
				return "", err
			}
		}
		if len(run.UnrealGraph.Nodes) > 0 || len(run.UnrealGraph.Edges) > 0 {
			graphData, err := json.MarshalIndent(run.UnrealGraph, "", "  ")
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(latestDir, "unreal_graph.json"), graphData, 0o644); err != nil {
				return "", err
			}
		}
		if len(run.VectorCorpus.Documents) > 0 {
			corpusData, err := json.MarshalIndent(run.VectorCorpus, "", "  ")
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(latestDir, "vector_corpus.json"), corpusData, 0o644); err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(latestDir, "vector_corpus.jsonl"), []byte(buildVectorCorpusJSONL(run.VectorCorpus)), 0o644); err != nil {
				return "", err
			}
			manifest := run.VectorIngestion
			if manifest.DocumentCount == 0 {
				manifest = buildVectorIngestionManifest(run.VectorCorpus)
			}
			manifestData, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(latestDir, "vector_ingest_manifest.json"), manifestData, 0o644); err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(latestDir, "vector_ingest_records.jsonl"), []byte(buildVectorIngestionRecordsJSONL(run.VectorCorpus)), 0o644); err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(latestDir, "vector_pgvector.sql"), []byte(buildVectorPGVectorSQL(run.VectorCorpus)), 0o644); err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(latestDir, "vector_sqlite.sql"), []byte(buildVectorSQLiteSQL(run.VectorCorpus)), 0o644); err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(latestDir, "vector_qdrant.jsonl"), []byte(buildVectorQdrantSeedJSONL(run.VectorCorpus)), 0o644); err != nil {
				return "", err
			}
		}
	}
	return mdPath, nil
}

func (a *projectAnalyzer) relatedFiles(snapshot ProjectSnapshot, primaryFiles []string, limit int) []string {
	score := map[string]int{}
	primary := map[string]struct{}{}
	for _, file := range primaryFiles {
		primary[file] = struct{}{}
		for _, dep := range snapshot.ImportGraph[file] {
			if _, ok := primary[dep]; ok {
				continue
			}
			score[dep] += 3
		}
		for _, dep := range snapshot.ReverseImportGraph[file] {
			if _, ok := primary[dep]; ok {
				continue
			}
			score[dep] += 2
		}
	}
	type item struct {
		Path  string
		Score int
	}
	items := []item{}
	for path, value := range score {
		items = append(items, item{Path: path, Score: value})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].Score == items[j].Score {
			return items[i].Path < items[j].Path
		}
		return items[i].Score > items[j].Score
	})
	out := []string{}
	for _, item := range items {
		out = append(out, item.Path)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (a *projectAnalyzer) computeShardFingerprint(snapshot ProjectSnapshot, shard AnalysisShard) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "primary:%s\nreference:%s\n", shard.PrimaryFingerprint, shard.ReferenceFingerprint)
	fmt.Fprintf(hash, "primary_semantic:%s\nreference_semantic:%s\n", shard.PrimarySemanticFingerprint, shard.ReferenceSemanticFingerprint)
	fmt.Fprintf(hash, "worker:%s\nreviewer:%s\n", a.workerModel(), a.reviewerModel())
	return hex.EncodeToString(hash.Sum(nil))
}

func (a *projectAnalyzer) computeFileSetFingerprint(snapshot ProjectSnapshot, paths []string) string {
	hash := sha256.New()
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	for _, path := range sorted {
		file := snapshot.FilesByPath[path]
		abs := filepath.Join(snapshot.Root, filepath.FromSlash(path))
		data, err := os.ReadFile(abs)
		if err != nil {
			fmt.Fprintf(hash, "missing:%s\n", path)
			continue
		}
		fmt.Fprintf(hash, "file:%s\nlines:%d\nentry:%t\nmanifest:%t\n", file.Path, file.LineCount, file.IsEntrypoint, file.IsManifest)
		hash.Write(data)
		hash.Write([]byte("\n"))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (a *projectAnalyzer) computeSemanticFingerprint(snapshot ProjectSnapshot, paths []string) string {
	if hasSemanticIndexV2Data(a.cachedSemanticIndexV2) {
		return a.computeSemanticFingerprintV2(snapshot, a.cachedSemanticIndexV2, paths)
	}
	return a.computeSemanticFingerprintLegacy(snapshot, paths)
}

func (a *projectAnalyzer) computeSemanticFingerprintLegacy(snapshot ProjectSnapshot, paths []string) string {
	hash := sha256.New()
	sorted := analysisUniqueStrings(append([]string(nil), paths...))
	sort.Strings(sorted)
	fileSet := map[string]struct{}{}
	for _, path := range sorted {
		fileSet[path] = struct{}{}
		file := snapshot.FilesByPath[path]
		fmt.Fprintf(hash, "file:%s\nimportance:%d\n", path, file.ImportanceScore)
	}
	for _, edge := range snapshot.RuntimeEdges {
		if !edgeTouchesFiles(edge.Evidence, fileSet) {
			continue
		}
		fmt.Fprintf(hash, "runtime:%s|%s|%s|%s|%s\n", edge.Source, edge.Target, edge.Kind, edge.Confidence, strings.Join(analysisUniqueStrings(edge.Evidence), "|"))
	}
	for _, edge := range snapshot.ProjectEdges {
		if !edgeTouchesFiles(edge.Evidence, fileSet) {
			continue
		}
		attrKeys := make([]string, 0, len(edge.Attributes))
		for key := range edge.Attributes {
			attrKeys = append(attrKeys, key)
		}
		sort.Strings(attrKeys)
		attrPairs := []string{}
		for _, key := range attrKeys {
			attrPairs = append(attrPairs, key+"="+edge.Attributes[key])
		}
		fmt.Fprintf(hash, "project:%s|%s|%s|%s|%s|%s\n", edge.Source, edge.Target, edge.Type, edge.Confidence, strings.Join(analysisUniqueStrings(edge.Evidence), "|"), strings.Join(attrPairs, "|"))
	}
	for _, item := range snapshot.UnrealTypes {
		if _, ok := fileSet[item.File]; !ok {
			continue
		}
		fmt.Fprintf(hash, "type:%s|%s|%s|%s|%s\n", item.Name, item.Kind, item.BaseClass, item.Module, item.GameplayRole)
	}
	for _, item := range snapshot.UnrealNetwork {
		if _, ok := fileSet[item.File]; !ok {
			continue
		}
		fmt.Fprintf(hash, "network:%s|server=%s|client=%s|multi=%s|rep=%s|notify=%s\n",
			item.TypeName,
			strings.Join(analysisUniqueStrings(item.ServerRPCs), "|"),
			strings.Join(analysisUniqueStrings(item.ClientRPCs), "|"),
			strings.Join(analysisUniqueStrings(item.MulticastRPCs), "|"),
			strings.Join(analysisUniqueStrings(item.ReplicatedProperties), "|"),
			strings.Join(analysisUniqueStrings(item.RepNotifyProperties), "|"))
	}
	for _, item := range snapshot.UnrealAssets {
		if _, ok := fileSet[item.File]; !ok {
			continue
		}
		fmt.Fprintf(hash, "asset:%s|targets=%s|config=%s|load=%s\n",
			firstNonBlankAnalysisString(item.OwnerName, item.File),
			strings.Join(analysisUniqueStrings(item.CanonicalTargets), "|"),
			strings.Join(analysisUniqueStrings(item.ConfigKeys), "|"),
			strings.Join(analysisUniqueStrings(item.LoadMethods), "|"))
	}
	for _, item := range snapshot.UnrealSystems {
		if _, ok := fileSet[item.File]; !ok {
			continue
		}
		fmt.Fprintf(hash, "system:%s|owner=%s|actions=%s|widgets=%s|abilities=%s|effects=%s\n",
			item.System,
			firstNonBlankAnalysisString(item.OwnerName, item.File),
			strings.Join(analysisUniqueStrings(item.Actions), "|"),
			strings.Join(analysisUniqueStrings(item.Widgets), "|"),
			strings.Join(analysisUniqueStrings(item.Abilities), "|"),
			strings.Join(analysisUniqueStrings(item.Effects), "|"))
	}
	for _, item := range snapshot.UnrealSettings {
		if _, ok := fileSet[item.SourceFile]; !ok {
			continue
		}
		fmt.Fprintf(hash, "settings:%s|map=%s|mode=%s|instance=%s|pawn=%s|controller=%s|hud=%s\n",
			item.SourceFile,
			item.GameDefaultMap,
			item.GlobalDefaultGameMode,
			item.GameInstanceClass,
			item.DefaultPawnClass,
			item.PlayerControllerClass,
			item.HUDClass)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (a *projectAnalyzer) computeSemanticFingerprintV2(snapshot ProjectSnapshot, index SemanticIndexV2, paths []string) string {
	hash := sha256.New()
	sortedPaths := analysisUniqueStrings(append([]string(nil), paths...))
	sort.Strings(sortedPaths)
	fileSet := map[string]struct{}{}
	seedIDs := map[string]struct{}{}
	for _, path := range sortedPaths {
		fileSet[path] = struct{}{}
		file := snapshot.FilesByPath[path]
		fmt.Fprintf(hash, "file:%s|importance:%d|entry:%t|manifest:%t\n", path, file.ImportanceScore, file.IsEntrypoint, file.IsManifest)
		for _, ctxID := range buildContextIDsForFile(snapshot, path) {
			seedIDs[ctxID] = struct{}{}
		}
	}

	for _, item := range index.Symbols {
		if _, ok := fileSet[item.File]; ok {
			seedIDs[item.ID] = struct{}{}
			continue
		}
		if strings.TrimSpace(item.BuildContextID) != "" {
			if _, ok := seedIDs[item.BuildContextID]; ok {
				seedIDs[item.ID] = struct{}{}
			}
		}
	}
	for _, item := range index.Occurrences {
		if _, ok := fileSet[item.File]; ok {
			seedIDs[item.SymbolID] = struct{}{}
		}
	}

	nodeSet := map[string]struct{}{}
	for id := range seedIDs {
		nodeSet[id] = struct{}{}
	}
	expandNodes := func(sourceID string, targetID string, touchesFile bool) {
		if touchesFile {
			if strings.TrimSpace(sourceID) != "" {
				nodeSet[sourceID] = struct{}{}
			}
			if strings.TrimSpace(targetID) != "" {
				nodeSet[targetID] = struct{}{}
			}
			return
		}
		if _, ok := seedIDs[sourceID]; ok {
			if strings.TrimSpace(targetID) != "" {
				nodeSet[targetID] = struct{}{}
			}
		}
		if _, ok := seedIDs[targetID]; ok {
			if strings.TrimSpace(sourceID) != "" {
				nodeSet[sourceID] = struct{}{}
			}
		}
	}
	for _, edge := range index.CallEdges {
		expandNodes(edge.SourceID, edge.TargetID, edgeTouchesFiles(edge.Evidence, fileSet))
	}
	for _, edge := range index.BuildOwnershipEdges {
		expandNodes(edge.SourceID, edge.TargetID, edgeTouchesFiles(edge.Evidence, fileSet))
	}
	for _, edge := range index.InheritanceEdges {
		expandNodes(edge.SourceID, edge.TargetID, edgeTouchesFiles(edge.Evidence, fileSet))
	}
	for _, edge := range index.OverlayEdges {
		expandNodes(edge.SourceID, edge.TargetID, edgeTouchesFiles(edge.Evidence, fileSet))
	}
	for _, edge := range index.References {
		touchesFile := edgeTouchesFiles(edge.Evidence, fileSet)
		if _, ok := fileSet[edge.SourceFile]; ok {
			touchesFile = true
		}
		if _, ok := fileSet[edge.TargetPath]; ok {
			touchesFile = true
		}
		expandNodes(edge.SourceID, edge.TargetID, touchesFile)
	}
	for _, edge := range index.GeneratedCodeEdges {
		if _, ok := fileSet[edge.SourceFile]; ok {
			if strings.TrimSpace(edge.TargetID) != "" {
				nodeSet[edge.TargetID] = struct{}{}
			}
		}
	}

	for _, ctx := range index.BuildContexts {
		if _, ok := nodeSet[ctx.ID]; !ok && !buildContextTouchesFiles(ctx, fileSet) {
			continue
		}
		fmt.Fprintf(hash, "ctx:%s|%s|dir=%s|project=%s|target=%s|module=%s|compiler=%s|files=%s|defines=%s|includes=%s|force=%s\n",
			ctx.ID,
			ctx.Kind,
			ctx.Directory,
			ctx.Project,
			ctx.Target,
			ctx.Module,
			ctx.Compiler,
			strings.Join(analysisUniqueStrings(ctx.Files), "|"),
			strings.Join(analysisUniqueStrings(ctx.Defines), "|"),
			strings.Join(analysisUniqueStrings(ctx.IncludePaths), "|"),
			strings.Join(analysisUniqueStrings(ctx.ForceIncludes), "|"),
		)
	}
	for _, symbol := range index.Symbols {
		if _, ok := nodeSet[symbol.ID]; !ok {
			continue
		}
		attrKeys := make([]string, 0, len(symbol.Attributes))
		for key := range symbol.Attributes {
			attrKeys = append(attrKeys, key)
		}
		sort.Strings(attrKeys)
		attrPairs := []string{}
		for _, key := range attrKeys {
			attrPairs = append(attrPairs, key+"="+symbol.Attributes[key])
		}
		fmt.Fprintf(hash, "symbol:%s|name=%s|kind=%s|file=%s|module=%s|container=%s|ctx=%s|base=%s|sig=%s|lines=%d-%d|tags=%s|attrs=%s\n",
			symbol.ID,
			symbol.Name,
			symbol.Kind,
			symbol.File,
			symbol.Module,
			symbol.ContainerSymbolID,
			symbol.BuildContextID,
			symbol.BaseSymbolID,
			symbol.Signature,
			symbol.StartLine,
			symbol.EndLine,
			strings.Join(analysisUniqueStrings(symbol.Tags), "|"),
			strings.Join(attrPairs, "|"),
		)
	}
	for _, item := range index.Occurrences {
		if _, ok := nodeSet[item.SymbolID]; !ok {
			if _, fileOk := fileSet[item.File]; !fileOk {
				continue
			}
		}
		fmt.Fprintf(hash, "occurrence:%s|%s|%s\n", item.SymbolID, item.Role, item.File)
	}
	for _, edge := range index.CallEdges {
		if !semanticV2EdgeTouchesNodeSet(edge.SourceID, edge.TargetID, fileSet, edge.Evidence, nodeSet) {
			continue
		}
		fmt.Fprintf(hash, "call:%s|%s|%s|%s\n", edge.SourceID, edge.TargetID, edge.Type, strings.Join(analysisUniqueStrings(edge.Evidence), "|"))
	}
	for _, edge := range index.BuildOwnershipEdges {
		if !semanticV2EdgeTouchesNodeSet(edge.SourceID, edge.TargetID, fileSet, edge.Evidence, nodeSet) {
			continue
		}
		fmt.Fprintf(hash, "build:%s|%s|%s|%s\n", edge.SourceID, edge.TargetID, edge.Type, strings.Join(analysisUniqueStrings(edge.Evidence), "|"))
	}
	for _, edge := range index.InheritanceEdges {
		if !semanticV2EdgeTouchesNodeSet(edge.SourceID, edge.TargetID, fileSet, edge.Evidence, nodeSet) {
			continue
		}
		fmt.Fprintf(hash, "inherit:%s|%s|%s\n", edge.SourceID, edge.TargetID, strings.Join(analysisUniqueStrings(edge.Evidence), "|"))
	}
	for _, edge := range index.OverlayEdges {
		if !semanticV2EdgeTouchesNodeSet(edge.SourceID, edge.TargetID, fileSet, edge.Evidence, nodeSet) {
			continue
		}
		fmt.Fprintf(hash, "overlay:%s|%s|%s|%s|%s\n", edge.SourceID, edge.TargetID, edge.Type, edge.Domain, strings.Join(analysisUniqueStrings(edge.Evidence), "|"))
	}
	for _, edge := range index.References {
		touchesFile := edgeTouchesFiles(edge.Evidence, fileSet)
		if _, ok := fileSet[edge.SourceFile]; ok {
			touchesFile = true
		}
		if _, ok := fileSet[edge.TargetPath]; ok {
			touchesFile = true
		}
		_, sourceHit := nodeSet[edge.SourceID]
		_, targetHit := nodeSet[edge.TargetID]
		if !sourceHit && !targetHit && !touchesFile {
			continue
		}
		fmt.Fprintf(hash, "ref:%s|%s|%s|%s|%s|%s\n", edge.SourceID, edge.SourceFile, edge.TargetID, edge.TargetPath, edge.Type, strings.Join(analysisUniqueStrings(edge.Evidence), "|"))
	}
	for _, edge := range index.GeneratedCodeEdges {
		if _, ok := fileSet[edge.SourceFile]; !ok {
			if _, ok := nodeSet[edge.TargetID]; !ok {
				continue
			}
		}
		fmt.Fprintf(hash, "generated:%s|%s|%s|%s\n", edge.SourceFile, edge.TargetID, edge.Type, strings.Join(analysisUniqueStrings(edge.Evidence), "|"))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func buildContextTouchesFiles(ctx BuildContextRecord, fileSet map[string]struct{}) bool {
	for _, path := range ctx.Files {
		if _, ok := fileSet[path]; ok {
			return true
		}
	}
	return false
}

func semanticV2EdgeTouchesNodeSet(sourceID string, targetID string, fileSet map[string]struct{}, evidence []string, nodeSet map[string]struct{}) bool {
	if _, ok := nodeSet[sourceID]; ok {
		return true
	}
	if _, ok := nodeSet[targetID]; ok {
		return true
	}
	return edgeTouchesFiles(evidence, fileSet)
}

func (a *projectAnalyzer) finalizeShard(snapshot ProjectSnapshot, shard *AnalysisShard, referenceLimit int) {
	shard.ReferenceFiles = a.relatedFiles(snapshot, shard.PrimaryFiles, referenceLimit)
	shard.PrimaryFingerprint = a.computeFileSetFingerprint(snapshot, shard.PrimaryFiles)
	shard.ReferenceFingerprint = a.computeFileSetFingerprint(snapshot, shard.ReferenceFiles)
	shard.PrimarySemanticFingerprint = a.computeSemanticFingerprint(snapshot, shard.PrimaryFiles)
	shard.ReferenceSemanticFingerprint = a.computeSemanticFingerprint(snapshot, shard.ReferenceFiles)
	shard.Fingerprint = a.computeShardFingerprint(snapshot, *shard)
}

func (a *projectAnalyzer) initializeClients() error {
	var err error
	if a.analysisCfg.WorkerProfile != nil {
		a.workerClient, err = createProviderClientFromProfile(*a.analysisCfg.WorkerProfile, a.cfg)
		if err != nil {
			return fmt.Errorf("project analysis worker profile: %w", err)
		}
	}
	if a.analysisCfg.ReviewerProfile != nil {
		a.reviewerClient, err = createProviderClientFromProfile(*a.analysisCfg.ReviewerProfile, a.cfg)
		if err != nil {
			return fmt.Errorf("project analysis reviewer profile: %w", err)
		}
	}
	return nil
}

func (a *projectAnalyzer) workerOrDefaultClient() ProviderClient {
	if a.workerClient != nil {
		return a.workerClient
	}
	return a.client
}

func (a *projectAnalyzer) reviewerOrDefaultClient() ProviderClient {
	if a.reviewerClient != nil {
		return a.reviewerClient
	}
	if a.workerClient != nil {
		return a.workerClient
	}
	return a.client
}

func (a *projectAnalyzer) workerModel() string {
	if a.analysisCfg.WorkerProfile != nil && strings.TrimSpace(a.analysisCfg.WorkerProfile.Model) != "" {
		return a.analysisCfg.WorkerProfile.Model
	}
	return a.cfg.Model
}

func (a *projectAnalyzer) workerMaxTokens() int {
	return analysisStructuredMaxTokens(a.workerModel(), a.cfg.MaxTokens)
}

func (a *projectAnalyzer) reviewerModel() string {
	if a.analysisCfg.ReviewerProfile != nil && strings.TrimSpace(a.analysisCfg.ReviewerProfile.Model) != "" {
		return a.analysisCfg.ReviewerProfile.Model
	}
	if a.analysisCfg.WorkerProfile != nil && strings.TrimSpace(a.analysisCfg.WorkerProfile.Model) != "" {
		return a.analysisCfg.WorkerProfile.Model
	}
	return a.cfg.Model
}

func (a *projectAnalyzer) reviewerMaxTokens() int {
	return analysisStructuredMaxTokens(a.reviewerModel(), a.cfg.MaxTokens)
}

func analysisStructuredMaxTokens(model string, base int) int {
	if base <= 0 {
		return base
	}
	if analysisModelNeedsReasoningHeadroom(model) && base < 8192 {
		return 8192
	}
	return base
}

func analysisModelNeedsReasoningHeadroom(model string) bool {
	model = strings.ToLower(strings.TrimSpace(openCodeAPIModelID(model)))
	if model == "" {
		return false
	}
	if strings.HasPrefix(model, "kimi-") || strings.Contains(model, "kimi-k") {
		return true
	}
	return false
}

func createProviderClientFromProfile(profile Profile, mainCfg Config) (ProviderClient, error) {
	provider := normalizeProviderName(profile.Provider)
	if provider == "" {
		return nil, fmt.Errorf("profile provider is empty")
	}
	model := strings.TrimSpace(profile.Model)
	if model == "" {
		return nil, fmt.Errorf("profile model is empty")
	}
	mainProvider := normalizeProviderName(mainCfg.Provider)
	apiKey := strings.TrimSpace(profile.APIKey)
	if apiKey == "" && provider == mainProvider {
		apiKey = strings.TrimSpace(mainCfg.APIKey)
	}
	if strings.TrimSpace(apiKey) == "" && mainCfg.ProviderKeys != nil {
		apiKey = strings.TrimSpace(mainCfg.ProviderKeys[provider])
	}
	baseURL := strings.TrimSpace(profile.BaseURL)
	if baseURL == "" && provider == mainProvider {
		baseURL = strings.TrimSpace(mainCfg.BaseURL)
	}
	cfg := mainCfg
	cfg.Provider = provider
	cfg.Model = model
	cfg.BaseURL = normalizeProfileBaseURL(provider, baseURL)
	cfg.APIKey = apiKey
	return NewProviderClient(cfg)
}

func describeAnalysisProfile(profile *Profile, fallback ProviderClient, fallbackModel string) string {
	if profile != nil {
		return strings.TrimSpace(profile.Provider) + " / " + strings.TrimSpace(profile.Model)
	}
	if fallback != nil {
		return fallback.Name() + " / " + fallbackModel
	}
	return fallbackModel
}

func primaryFilesKey(paths []string) string {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	return strings.Join(sorted, "\n")
}

func (a *projectAnalyzer) buildReuseState(previousRun *ProjectAnalysisRun, shards []AnalysisShard) analysisReuseState {
	state := analysisReuseState{
		previousByPrimaryKey: map[string]int{},
		changedPrimaryFiles:  map[string]struct{}{},
		changedSemanticFiles: map[string]struct{}{},
	}
	if previousRun == nil {
		for _, shard := range shards {
			for _, path := range shard.PrimaryFiles {
				state.changedPrimaryFiles[path] = struct{}{}
				state.changedSemanticFiles[path] = struct{}{}
			}
		}
		return state
	}
	for index, shard := range previousRun.Shards {
		state.previousByPrimaryKey[primaryFilesKey(shard.PrimaryFiles)] = index
	}
	for _, shard := range shards {
		key := primaryFilesKey(shard.PrimaryFiles)
		index, ok := state.previousByPrimaryKey[key]
		if !ok {
			for _, path := range shard.PrimaryFiles {
				state.changedPrimaryFiles[path] = struct{}{}
				state.changedSemanticFiles[path] = struct{}{}
			}
			continue
		}
		previousShard := previousRun.Shards[index]
		if previousShard.PrimaryFingerprint != shard.PrimaryFingerprint {
			for _, path := range shard.PrimaryFiles {
				state.changedPrimaryFiles[path] = struct{}{}
			}
		}
		if previousShard.PrimarySemanticFingerprint != shard.PrimarySemanticFingerprint {
			for _, path := range shard.PrimaryFiles {
				state.changedSemanticFiles[path] = struct{}{}
			}
		}
	}
	return state
}

func (a *projectAnalyzer) loadPreviousRun(goal string, mode string) (*ProjectAnalysisRun, error) {
	if a.analysisCfg.Incremental != nil && !*a.analysisCfg.Incremental {
		return nil, nil
	}
	matches := []string{}
	seen := map[string]struct{}{}
	patterns := []string{
		filepath.Join(a.analysisCfg.OutputDir, "*_"+analysisGoalArtifactSuffix(goal, mode)+".json"),
	}
	legacySuffix := sanitizeFileName(goal)
	if legacySuffix != "" && legacySuffix != analysisGoalArtifactSuffix(goal, mode) {
		patterns = append(patterns, filepath.Join(a.analysisCfg.OutputDir, "*_"+legacySuffix+".json"))
	}
	for _, pattern := range patterns {
		items, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.Slice(matches, func(i int, j int) bool {
		infoI, errI := os.Stat(matches[i])
		infoJ, errJ := os.Stat(matches[j])
		if errI != nil || errJ != nil {
			return matches[i] > matches[j]
		}
		return infoI.ModTime().After(infoJ.ModTime())
	})
	data, err := os.ReadFile(matches[0])
	if err != nil {
		return nil, err
	}
	var run ProjectAnalysisRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (a *projectAnalyzer) loadBaselineMapForMode(mode string, scope AnalysisGoalScope) (AnalysisBaselineMap, bool) {
	if normalizeProjectAnalysisMode(mode) == "" || normalizeProjectAnalysisMode(mode) == "map" {
		return AnalysisBaselineMap{}, false
	}
	candidates := a.findBaselineMapRunCandidates()
	if len(candidates) == 0 {
		return AnalysisBaselineMap{}, false
	}
	sort.SliceStable(candidates, func(i int, j int) bool {
		left := scoreBaselineMapRun(candidates[i], scope)
		right := scoreBaselineMapRun(candidates[j], scope)
		if left != right {
			return left > right
		}
		leftTime := candidates[i].run.Summary.CompletedAt
		if leftTime.IsZero() {
			leftTime = candidates[i].modTime
		}
		rightTime := candidates[j].run.Summary.CompletedAt
		if rightTime.IsZero() {
			rightTime = candidates[j].modTime
		}
		return leftTime.After(rightTime)
	})
	return buildAnalysisBaselineMap(candidates[0]), true
}

type baselineMapRunCandidate struct {
	run              ProjectAnalysisRun
	path             string
	docsManifestPath string
	modTime          time.Time
}

func (a *projectAnalyzer) findBaselineMapRunCandidates() []baselineMapRunCandidate {
	matches := []string{}
	seen := map[string]struct{}{}
	for _, pattern := range []string{
		filepath.Join(a.analysisCfg.OutputDir, "latest", "run.json"),
		filepath.Join(a.analysisCfg.OutputDir, "*_map_*.json"),
		filepath.Join(a.analysisCfg.OutputDir, "*_map.json"),
	} {
		items, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, item := range items {
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			matches = append(matches, item)
		}
	}
	candidates := []baselineMapRunCandidate{}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		run := ProjectAnalysisRun{}
		if err := json.Unmarshal(data, &run); err != nil {
			continue
		}
		if normalizeProjectAnalysisMode(run.Summary.Mode) != "map" {
			continue
		}
		info, _ := os.Stat(path)
		modTime := time.Time{}
		if info != nil {
			modTime = info.ModTime()
		}
		candidates = append(candidates, baselineMapRunCandidate{
			run:              run,
			path:             path,
			docsManifestPath: baselineMapDocsManifestPath(path),
			modTime:          modTime,
		})
	}
	return candidates
}

func baselineMapDocsManifestPath(runPath string) string {
	dir := filepath.Dir(runPath)
	if strings.EqualFold(filepath.Base(runPath), "run.json") && strings.EqualFold(filepath.Base(dir), "latest") {
		return filepath.Join(dir, "docs_manifest.json")
	}
	ext := filepath.Ext(runPath)
	if ext == "" {
		return runPath + "_docs_manifest.json"
	}
	return strings.TrimSuffix(runPath, ext) + "_docs_manifest.json"
}

func scoreBaselineMapRun(candidate baselineMapRunCandidate, scope AnalysisGoalScope) int {
	score := 0
	if strings.EqualFold(filepath.Base(candidate.path), "run.json") && strings.EqualFold(filepath.Base(filepath.Dir(candidate.path)), "latest") {
		score += 8
	}
	if strings.TrimSpace(candidate.run.KnowledgePack.ProjectSummary) != "" {
		score += 8
	}
	if len(candidate.run.KnowledgePack.Subsystems) > 0 {
		score += 6
	}
	if hasSemanticIndexV2Data(candidate.run.SemanticIndexV2) {
		score += 5
	}
	if len(candidate.run.VectorCorpus.Documents) > 0 {
		score += 3
	}
	if len(scope.DirectoryPrefixes) > 0 {
		scopeScore := scoreBaselineScopeOverlap(candidate.run.KnowledgePack, candidate.run.Snapshot, scope)
		if scopeScore == 0 {
			score -= 6
		}
		score += scopeScore
	}
	return score
}

func scoreBaselineScopeOverlap(pack KnowledgePack, snapshot ProjectSnapshot, scope AnalysisGoalScope) int {
	score := 0
	for _, prefix := range scope.DirectoryPrefixes {
		prefix = strings.ToLower(filepath.ToSlash(strings.Trim(prefix, "/")))
		if prefix == "" {
			continue
		}
		for _, file := range append(append([]string(nil), pack.TopImportantFiles...), pack.EntrypointFiles...) {
			lower := strings.ToLower(filepath.ToSlash(strings.Trim(file, "/")))
			if strings.HasPrefix(lower, prefix+"/") || strings.Contains(lower, prefix) {
				score += 4
			}
		}
		for _, subsystem := range pack.Subsystems {
			for _, file := range append(append([]string(nil), subsystem.KeyFiles...), subsystem.EvidenceFiles...) {
				lower := strings.ToLower(filepath.ToSlash(strings.Trim(file, "/")))
				if strings.HasPrefix(lower, prefix+"/") || strings.Contains(lower, prefix) {
					score += 3
				}
			}
		}
		for _, file := range snapshot.Files {
			lower := strings.ToLower(filepath.ToSlash(strings.Trim(file.Path, "/")))
			if strings.HasPrefix(lower, prefix+"/") || strings.Contains(lower, prefix) {
				score++
				break
			}
		}
	}
	if score > 20 {
		return 20
	}
	return score
}

func buildAnalysisBaselineMap(candidate baselineMapRunCandidate) AnalysisBaselineMap {
	run := candidate.run
	generatedAt := run.Summary.CompletedAt
	if generatedAt.IsZero() {
		generatedAt = run.Summary.StartedAt
	}
	if generatedAt.IsZero() {
		generatedAt = run.KnowledgePack.GeneratedAt
	}
	subsystems := []string{}
	sourceAnchors := []string{}
	for _, subsystem := range run.KnowledgePack.Subsystems {
		if strings.TrimSpace(subsystem.Title) != "" {
			subsystems = append(subsystems, subsystem.Title)
		}
		sourceAnchors = append(sourceAnchors, subsystem.KeyFiles...)
		sourceAnchors = append(sourceAnchors, subsystem.EvidenceFiles...)
	}
	topFiles := append([]string(nil), run.KnowledgePack.TopImportantFiles...)
	if len(topFiles) == 0 {
		for _, file := range topImportantFiles(run.Snapshot, 12) {
			topFiles = append(topFiles, file.Path)
		}
	}
	return AnalysisBaselineMap{
		RunID:            run.Summary.RunID,
		Goal:             firstNonBlankAnalysisString(run.Summary.Goal, run.KnowledgePack.Goal),
		Mode:             "map",
		ArtifactPath:     filepath.ToSlash(candidate.path),
		DocsManifestPath: filepath.ToSlash(candidate.docsManifestPath),
		GeneratedAt:      generatedAt,
		ProjectSummary:   firstNonBlankAnalysisString(run.KnowledgePack.ProjectSummary, strings.TrimSpace(firstParagraph(run.FinalDocument))),
		PrimaryStartup:   firstNonBlankAnalysisString(run.KnowledgePack.PrimaryStartup, run.Snapshot.PrimaryStartup),
		Subsystems:       limitStrings(analysisUniqueStrings(subsystems), 16),
		TopFiles:         limitStrings(analysisUniqueStrings(topFiles), 16),
		SourceAnchors:    limitStrings(analysisUniqueStrings(sourceAnchors), 24),
	}
}

func firstParagraph(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	paragraphs := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n")
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" || strings.HasPrefix(paragraph, "#") {
			continue
		}
		return strings.Join(strings.Fields(paragraph), " ")
	}
	return strings.Join(strings.Fields(text), " ")
}

func (a *projectAnalyzer) tryReuseShard(previousRun *ProjectAnalysisRun, shard AnalysisShard, reuseState analysisReuseState) (WorkerReport, ReviewDecision, string, bool) {
	if previousRun == nil {
		return WorkerReport{}, ReviewDecision{}, "no_previous_run", false
	}
	index, ok := reuseState.previousByPrimaryKey[primaryFilesKey(shard.PrimaryFiles)]
	if !ok {
		return WorkerReport{}, ReviewDecision{}, "new_primary_scope", false
	}
	if index >= len(previousRun.Reports) || index >= len(previousRun.Reviews) {
		return WorkerReport{}, ReviewDecision{}, "previous_run_incomplete", false
	}
	previousShard := previousRun.Shards[index]
	if previousShard.PrimaryFingerprint != shard.PrimaryFingerprint {
		return WorkerReport{}, ReviewDecision{}, "primary_changed", false
	}
	if previousShard.PrimarySemanticFingerprint != shard.PrimarySemanticFingerprint {
		return WorkerReport{}, ReviewDecision{}, "semantic_primary_changed", false
	}
	for _, ref := range shard.ReferenceFiles {
		if _, changed := reuseState.changedPrimaryFiles[ref]; changed {
			return WorkerReport{}, ReviewDecision{}, "dependency_changed", false
		}
		if _, changed := reuseState.changedSemanticFiles[ref]; changed {
			return WorkerReport{}, ReviewDecision{}, "semantic_dependency_changed", false
		}
	}
	if previousShard.ReferenceFingerprint != shard.ReferenceFingerprint {
		return WorkerReport{}, ReviewDecision{}, "reference_changed", false
	}
	if previousShard.ReferenceSemanticFingerprint != shard.ReferenceSemanticFingerprint {
		return WorkerReport{}, ReviewDecision{}, "semantic_reference_changed", false
	}
	review := previousRun.Reviews[index]
	if !strings.EqualFold(review.Status, "approved") {
		return WorkerReport{}, ReviewDecision{}, "previous_review_not_approved", false
	}
	report := previousRun.Reports[index]
	report.ShardID = shard.ID
	return report, review, "cache_hit", true
}

func (a *projectAnalyzer) previousReportForShard(previousRun *ProjectAnalysisRun, shard AnalysisShard, reuseState analysisReuseState) (WorkerReport, bool) {
	if previousRun == nil {
		return WorkerReport{}, false
	}
	index, ok := reuseState.previousByPrimaryKey[primaryFilesKey(shard.PrimaryFiles)]
	if !ok {
		return WorkerReport{}, false
	}
	if index < 0 || index >= len(previousRun.Reports) {
		return WorkerReport{}, false
	}
	return previousRun.Reports[index], true
}

func (a *projectAnalyzer) previousShardForPrimary(previousRun *ProjectAnalysisRun, shard AnalysisShard, reuseState analysisReuseState) (AnalysisShard, bool) {
	if previousRun == nil {
		return AnalysisShard{}, false
	}
	index, ok := reuseState.previousByPrimaryKey[primaryFilesKey(shard.PrimaryFiles)]
	if !ok {
		return AnalysisShard{}, false
	}
	if index < 0 || index >= len(previousRun.Shards) {
		return AnalysisShard{}, false
	}
	return previousRun.Shards[index], true
}

func analysisInvalidationContext(shardNames []string) string {
	for _, name := range shardNames {
		trimmed := strings.TrimSpace(name)
		switch {
		case strings.HasPrefix(trimmed, "security_rpc"):
			return "integrity_security"
		case strings.HasPrefix(trimmed, "security_driver"),
			strings.HasPrefix(trimmed, "security_ioctl"),
			strings.HasPrefix(trimmed, "security_handles"),
			strings.HasPrefix(trimmed, "security_memory"):
			return "integrity_security"
		case strings.HasPrefix(trimmed, "unreal_network"):
			return "unreal_network"
		case strings.HasPrefix(trimmed, "unreal_ui"):
			return "unreal_ui"
		case strings.HasPrefix(trimmed, "unreal_ability"):
			return "unreal_ability"
		case strings.HasPrefix(trimmed, "asset_config"):
			return "asset_config"
		case strings.HasPrefix(trimmed, "integrity_security"):
			return "integrity_security"
		case strings.HasPrefix(trimmed, "startup"):
			return "startup"
		case strings.HasPrefix(trimmed, "build_graph"):
			return "build_graph"
		case strings.HasPrefix(trimmed, "unreal_gameplay"):
			return "unreal_gameplay"
		}
	}
	return ""
}

func describeAnalysisInvalidationReasonWithContext(reason string, shardNames []string) string {
	context := analysisInvalidationContext(shardNames)
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "":
		return ""
	case "cache_hit":
		return "Reused previous approved result because no relevant file, dependency, or semantic context changed."
	case "new":
		return "New shard scope detected, so no reusable prior analysis existed for this file set."
	case "new_primary_scope":
		return "Primary shard scope is new compared with the previous run."
	case "recomputed":
		return "Shard was recomputed instead of reused."
	case "primary_changed":
		return "Primary files changed, so the shard had to be recomputed."
	case "semantic_primary_changed":
		switch context {
		case "unreal_network":
			return "Primary files kept the same content scope, but RPC, replication, or authority semantics changed."
		case "unreal_ui":
			return "Primary files kept the same content scope, but widget ownership or gameplay-to-UI coupling semantics changed."
		case "unreal_ability":
			return "Primary files kept the same content scope, but ability, effect, or attribute semantics changed."
		case "asset_config":
			return "Primary files kept the same content scope, but config-driven startup or asset binding semantics changed."
		case "integrity_security":
			return "Primary files kept the same content scope, but trust boundary or anti-tamper semantics changed."
		case "startup":
			return "Primary files kept the same content scope, but bootstrap or startup handoff semantics changed."
		case "build_graph":
			return "Primary files kept the same content scope, but project, target, plugin, or module composition semantics changed."
		case "unreal_gameplay":
			return "Primary files kept the same content scope, but gameplay framework ownership semantics changed."
		}
		return "Primary files kept the same content scope, but their semantic structure changed."
	case "dependency_changed":
		switch context {
		case "unreal_network":
			return "An upstream dependency changed, so RPC, replication, or authority analysis was recomputed."
		case "unreal_ui":
			return "An upstream dependency changed, so widget ownership and gameplay-to-UI analysis was recomputed."
		case "unreal_ability":
			return "An upstream dependency changed, so ability system analysis was recomputed."
		case "asset_config":
			return "An upstream dependency changed, so config-driven startup or asset binding analysis was recomputed."
		case "integrity_security":
			return "An upstream dependency changed, so trust boundary or anti-tamper analysis was recomputed."
		}
		return "An upstream dependency shard changed, so dependent analysis was recomputed."
	case "semantic_dependency_changed":
		switch context {
		case "unreal_network":
			return "An upstream dependency kept the same file scope, but RPC, replication, or authority semantics changed."
		case "unreal_ui":
			return "An upstream dependency kept the same file scope, but widget ownership or gameplay-to-UI semantics changed."
		case "unreal_ability":
			return "An upstream dependency kept the same file scope, but ability, effect, or attribute semantics changed."
		case "asset_config":
			return "An upstream dependency kept the same file scope, but config-driven startup or asset binding semantics changed."
		case "integrity_security":
			return "An upstream dependency kept the same file scope, but trust boundary or anti-tamper semantics changed."
		case "startup":
			return "An upstream dependency kept the same file scope, but bootstrap or startup handoff semantics changed."
		case "build_graph":
			return "An upstream dependency kept the same file scope, but project, target, plugin, or module graph semantics changed."
		case "unreal_gameplay":
			return "An upstream dependency kept the same file scope, but gameplay framework ownership semantics changed."
		}
		return "An upstream dependency kept the same file scope, but its semantic context changed."
	case "reference_changed":
		switch context {
		case "unreal_network":
			return "Reference context changed, so RPC, replication, or authority evidence was refreshed."
		case "asset_config":
			return "Reference context changed, so config and asset binding evidence was refreshed."
		case "integrity_security":
			return "Reference context changed, so trust boundary and anti-tamper evidence was refreshed."
		}
		return "Reference context files changed, so dependency evidence was refreshed."
	case "semantic_reference_changed":
		switch context {
		case "unreal_network":
			return "Reference files kept the same content scope, but RPC, replication, or authority semantics changed."
		case "unreal_ui":
			return "Reference files kept the same content scope, but widget ownership or gameplay-to-UI semantics changed."
		case "unreal_ability":
			return "Reference files kept the same content scope, but ability, effect, or attribute semantics changed."
		case "asset_config":
			return "Reference files kept the same content scope, but config-driven startup or asset binding semantics changed."
		case "integrity_security":
			return "Reference files kept the same content scope, but trust boundary or anti-tamper semantics changed."
		}
		return "Reference files kept the same content scope, but their semantic context changed."
	case "previous_review_not_approved":
		return "Previous shard result was not approved, so it could not be safely reused."
	case "previous_run_incomplete":
		return "Previous run did not contain a complete reusable worker and reviewer result."
	case "no_previous_run":
		return "No previous analysis run was available for reuse."
	default:
		return reason
	}
}

func describeAnalysisInvalidationReason(reason string) string {
	return describeAnalysisInvalidationReasonWithContext(reason, nil)
}

func describeAnalysisInvalidationReasonsWithContext(reasons []string, shardNames []string, limit int) []string {
	out := make([]string, 0, len(reasons))
	for _, reason := range analysisUniqueStrings(reasons) {
		described := describeAnalysisInvalidationReasonWithContext(reason, shardNames)
		if strings.TrimSpace(described) == "" {
			continue
		}
		out = append(out, described)
	}
	return limitStrings(out, limit)
}

func describeAnalysisInvalidationReasons(reasons []string, limit int) []string {
	return describeAnalysisInvalidationReasonsWithContext(reasons, nil, limit)
}

func buildInvalidationEvidenceLines(snapshot ProjectSnapshot, shardNames []string, paths []string, reasons []string, limit int) []string {
	if len(paths) == 0 || len(reasons) == 0 {
		return nil
	}
	fileSet := map[string]struct{}{}
	for _, path := range analysisUniqueStrings(paths) {
		if strings.TrimSpace(path) == "" {
			continue
		}
		fileSet[path] = struct{}{}
	}
	if len(fileSet) == 0 {
		return nil
	}
	context := analysisInvalidationContext(shardNames)
	lines := []string{}
	appendLines := func(items []string) {
		for _, item := range items {
			if strings.TrimSpace(item) == "" {
				continue
			}
			lines = append(lines, item)
		}
	}
	switch context {
	case "unreal_network":
		appendLines(collectPromptNetworkLines(snapshot.UnrealNetwork, fileSet))
		appendLines(trimPromptBullets(promptProjectEdgeLines(snapshot.ProjectEdges, fileSet, 4, "")))
	case "unreal_ui":
		appendLines(collectPromptSystemLines(snapshot.UnrealSystems, fileSet))
		appendLines(collectPromptAssetLines(snapshot.UnrealAssets, fileSet))
	case "unreal_ability":
		appendLines(collectPromptSystemLines(snapshot.UnrealSystems, fileSet))
		appendLines(collectPromptTypeLines(snapshot.UnrealTypes, fileSet))
	case "asset_config":
		appendLines(collectPromptAssetLines(snapshot.UnrealAssets, fileSet))
		appendLines(collectPromptSettingsLines(snapshot.UnrealSettings, fileSet))
	case "integrity_security":
		appendLines(trimPromptBullets(promptProjectEdgeLines(snapshot.ProjectEdges, fileSet, 4, "")))
		appendLines(collectPromptSystemLines(snapshot.UnrealSystems, fileSet))
	case "startup":
		appendLines(trimPromptBullets(promptEdgeLines(snapshot.RuntimeEdges, fileSet, 4, "")))
		appendLines(trimPromptBullets(promptProjectEdgeLines(snapshot.ProjectEdges, fileSet, 4, "")))
	case "build_graph":
		appendLines(collectPromptProjectLines(snapshot.UnrealProjects, fileSet))
		appendLines(collectPromptPluginLines(snapshot.UnrealPlugins, fileSet))
		appendLines(collectPromptTargetLines(snapshot.UnrealTargets, fileSet))
		appendLines(collectPromptModuleLines(snapshot.UnrealModules, fileSet))
	case "unreal_gameplay":
		appendLines(collectPromptTypeLines(snapshot.UnrealTypes, fileSet))
		appendLines(collectPromptSystemLines(snapshot.UnrealSystems, fileSet))
	default:
		appendLines(trimPromptBullets(promptProjectEdgeLines(snapshot.ProjectEdges, fileSet, 4, "")))
		appendLines(trimPromptBullets(promptEdgeLines(snapshot.RuntimeEdges, fileSet, 4, "")))
	}
	if len(lines) == 0 {
		return nil
	}
	return limitStrings(analysisUniqueStrings(lines), limit)
}

func buildInvalidationDiffLines(previousSnapshot ProjectSnapshot, currentSnapshot ProjectSnapshot, shardNames []string, previousPaths []string, currentPaths []string, reasons []string, limit int) []string {
	if changes := buildInvalidationChanges(previousSnapshot, currentSnapshot, shardNames, previousPaths, currentPaths, limit); len(changes) > 0 {
		return buildInvalidationDiffStrings(changes, limit)
	}
	if limit <= 0 {
		return nil
	}
	context := analysisInvalidationContext(shardNames)
	if structured := buildStructuredInvalidationDiffLines(previousSnapshot, currentSnapshot, context, previousPaths, currentPaths, limit); len(structured) > 0 {
		return structured
	}
	previousEvidence := buildInvalidationEvidenceLines(previousSnapshot, shardNames, previousPaths, reasons, limit*2)
	currentEvidence := buildInvalidationEvidenceLines(currentSnapshot, shardNames, currentPaths, reasons, limit*2)
	if len(previousEvidence) == 0 && len(currentEvidence) == 0 {
		return nil
	}
	previousSet := map[string]struct{}{}
	currentSet := map[string]struct{}{}
	for _, item := range previousEvidence {
		previousSet[item] = struct{}{}
	}
	for _, item := range currentEvidence {
		currentSet[item] = struct{}{}
	}
	diff := []string{}
	for _, item := range currentEvidence {
		if _, ok := previousSet[item]; ok {
			continue
		}
		diff = append(diff, "Added: "+item)
	}
	for _, item := range previousEvidence {
		if _, ok := currentSet[item]; ok {
			continue
		}
		diff = append(diff, "Removed: "+item)
	}
	return limitStrings(analysisUniqueStrings(diff), limit)
}

func buildInvalidationChanges(previousSnapshot ProjectSnapshot, currentSnapshot ProjectSnapshot, shardNames []string, previousPaths []string, currentPaths []string, limit int) []InvalidationChange {
	if limit <= 0 {
		return nil
	}
	context := analysisInvalidationContext(shardNames)
	changes := buildStructuredInvalidationChanges(previousSnapshot, currentSnapshot, context, previousPaths, currentPaths, limit)
	return limitInvalidationChanges(dedupeInvalidationChanges(changes), limit)
}

func buildInvalidationDiffStrings(changes []InvalidationChange, limit int) []string {
	lines := []string{}
	for _, change := range limitInvalidationChanges(changes, limit) {
		lines = append(lines, renderInvalidationChange(change))
	}
	return lines
}

func buildStructuredInvalidationDiffLines(previousSnapshot ProjectSnapshot, currentSnapshot ProjectSnapshot, context string, previousPaths []string, currentPaths []string, limit int) []string {
	return buildInvalidationDiffStrings(buildStructuredInvalidationChanges(previousSnapshot, currentSnapshot, context, previousPaths, currentPaths, limit), limit)
}

func buildStructuredInvalidationChanges(previousSnapshot ProjectSnapshot, currentSnapshot ProjectSnapshot, context string, previousPaths []string, currentPaths []string, limit int) []InvalidationChange {
	switch context {
	case "unreal_network":
		return buildUnrealNetworkInvalidationChanges(previousSnapshot.UnrealNetwork, currentSnapshot.UnrealNetwork, previousPaths, currentPaths, limit)
	case "asset_config":
		return buildAssetConfigInvalidationChanges(previousSnapshot, currentSnapshot, previousPaths, currentPaths, limit)
	case "integrity_security":
		return buildSecurityInvalidationChanges(previousSnapshot, currentSnapshot, previousPaths, currentPaths, limit)
	case "unreal_ability", "unreal_ui", "unreal_gameplay":
		return buildGameplaySystemInvalidationChanges(previousSnapshot, currentSnapshot, previousPaths, currentPaths, limit)
	case "startup":
		return buildStartupInvalidationChanges(previousSnapshot, currentSnapshot, previousPaths, currentPaths, limit)
	}
	return nil
}

func buildUnrealNetworkInvalidationChanges(previous []UnrealNetworkSurface, current []UnrealNetworkSurface, previousPaths []string, currentPaths []string, limit int) []InvalidationChange {
	prevMap := mapFilteredNetworkSurfaces(previous, previousPaths)
	currMap := mapFilteredNetworkSurfaces(current, currentPaths)
	changes := []InvalidationChange{}
	for typeName, curr := range currMap {
		prev := prevMap[typeName]
		changes = append(changes, diffNamedChanges("rpc_added", "unreal_network", typeName, prev.ServerRPCs, curr.ServerRPCs)...)
		changes = append(changes, diffNamedChanges("client_rpc_added", "unreal_network", typeName, prev.ClientRPCs, curr.ClientRPCs)...)
		changes = append(changes, diffNamedChanges("multicast_rpc_added", "unreal_network", typeName, prev.MulticastRPCs, curr.MulticastRPCs)...)
		changes = append(changes, diffNamedChanges("replicated_property_added", "unreal_network", typeName, prev.ReplicatedProperties, curr.ReplicatedProperties)...)
		changes = append(changes, diffNamedChanges("repnotify_property_added", "unreal_network", typeName, prev.RepNotifyProperties, curr.RepNotifyProperties)...)
		changes = append(changes, diffNamedChanges("rpc_removed", "unreal_network", typeName, curr.ServerRPCs, prev.ServerRPCs)...)
		changes = append(changes, diffNamedChanges("client_rpc_removed", "unreal_network", typeName, curr.ClientRPCs, prev.ClientRPCs)...)
		changes = append(changes, diffNamedChanges("multicast_rpc_removed", "unreal_network", typeName, curr.MulticastRPCs, prev.MulticastRPCs)...)
		changes = append(changes, diffNamedChanges("replicated_property_removed", "unreal_network", typeName, curr.ReplicatedProperties, prev.ReplicatedProperties)...)
		changes = append(changes, diffNamedChanges("repnotify_property_removed", "unreal_network", typeName, curr.RepNotifyProperties, prev.RepNotifyProperties)...)
	}
	return limitInvalidationChanges(dedupeInvalidationChanges(changes), limit)
}

func buildAssetConfigInvalidationChanges(previousSnapshot ProjectSnapshot, currentSnapshot ProjectSnapshot, previousPaths []string, currentPaths []string, limit int) []InvalidationChange {
	changes := []InvalidationChange{}
	prevAssets := mapFilteredAssetRefs(previousSnapshot.UnrealAssets, previousPaths)
	currAssets := mapFilteredAssetRefs(currentSnapshot.UnrealAssets, currentPaths)
	for owner, curr := range currAssets {
		prev := prevAssets[owner]
		changes = append(changes, diffNamedChanges("config_binding_added", "asset_config", owner, prev.ConfigKeys, curr.ConfigKeys)...)
		changes = append(changes, diffNamedChanges("asset_target_added", "asset_config", owner, prev.CanonicalTargets, curr.CanonicalTargets)...)
		changes = append(changes, diffNamedChanges("load_method_added", "asset_config", owner, prev.LoadMethods, curr.LoadMethods)...)
		changes = append(changes, diffNamedChanges("config_binding_removed", "asset_config", owner, curr.ConfigKeys, prev.ConfigKeys)...)
		changes = append(changes, diffNamedChanges("asset_target_removed", "asset_config", owner, curr.CanonicalTargets, prev.CanonicalTargets)...)
	}
	prevSettings := mapFilteredSettings(previousSnapshot.UnrealSettings, previousPaths)
	currSettings := mapFilteredSettings(currentSnapshot.UnrealSettings, currentPaths)
	for source, curr := range currSettings {
		prev := prevSettings[source]
		changes = append(changes, diffScalarChanges("game_default_map_changed", "asset_config", source, prev.GameDefaultMap, curr.GameDefaultMap)...)
		changes = append(changes, diffScalarChanges("default_game_mode_changed", "asset_config", source, prev.GlobalDefaultGameMode, curr.GlobalDefaultGameMode)...)
	}
	return limitInvalidationChanges(dedupeInvalidationChanges(changes), limit)
}

func buildSecurityInvalidationChanges(previousSnapshot ProjectSnapshot, currentSnapshot ProjectSnapshot, previousPaths []string, currentPaths []string, limit int) []InvalidationChange {
	changes := []InvalidationChange{}
	prevEdges := filteredProjectEdges(previousSnapshot.ProjectEdges, previousPaths)
	currEdges := filteredProjectEdges(currentSnapshot.ProjectEdges, currentPaths)
	changes = append(changes, diffProjectEdgeChanges("trust_boundary_edge_added", "integrity_security", prevEdges, currEdges, "security", "integrity", "anti_tamper", "rpc_server", "configured_by")...)
	changes = append(changes, diffProjectEdgeChanges("trust_boundary_edge_removed", "integrity_security", currEdges, prevEdges, "security", "integrity", "anti_tamper", "rpc_server", "configured_by")...)
	prevSystems := mapFilteredSystems(previousSnapshot.UnrealSystems, previousPaths)
	currSystems := mapFilteredSystems(currentSnapshot.UnrealSystems, currentPaths)
	for key, curr := range currSystems {
		prev := prevSystems[key]
		changes = append(changes, diffNamedChanges("security_signal_added", "integrity_security", key, prev.Signals, curr.Signals)...)
		changes = append(changes, diffNamedChanges("security_action_added", "integrity_security", key, prev.Actions, curr.Actions)...)
		changes = append(changes, diffNamedChanges("security_signal_removed", "integrity_security", key, curr.Signals, prev.Signals)...)
	}
	return limitInvalidationChanges(dedupeInvalidationChanges(changes), limit)
}

func buildGameplaySystemInvalidationChanges(previousSnapshot ProjectSnapshot, currentSnapshot ProjectSnapshot, previousPaths []string, currentPaths []string, limit int) []InvalidationChange {
	prevSystems := mapFilteredSystems(previousSnapshot.UnrealSystems, previousPaths)
	currSystems := mapFilteredSystems(currentSnapshot.UnrealSystems, currentPaths)
	changes := []InvalidationChange{}
	for key, curr := range currSystems {
		prev := prevSystems[key]
		changes = append(changes, diffNamedChanges("gameplay_function_added", "unreal_gameplay", key, prev.Functions, curr.Functions)...)
		changes = append(changes, diffNamedChanges("gameplay_action_added", "unreal_gameplay", key, prev.Actions, curr.Actions)...)
		changes = append(changes, diffNamedChanges("widget_binding_added", "unreal_ui", key, prev.Widgets, curr.Widgets)...)
		changes = append(changes, diffNamedChanges("ability_binding_added", "unreal_ability", key, prev.Abilities, curr.Abilities)...)
		changes = append(changes, diffNamedChanges("effect_binding_added", "unreal_ability", key, prev.Effects, curr.Effects)...)
	}
	return limitInvalidationChanges(dedupeInvalidationChanges(changes), limit)
}

func buildStartupInvalidationChanges(previousSnapshot ProjectSnapshot, currentSnapshot ProjectSnapshot, previousPaths []string, currentPaths []string, limit int) []InvalidationChange {
	changes := []InvalidationChange{}
	prevEdges := filteredRuntimeEdges(previousSnapshot.RuntimeEdges, previousPaths)
	currEdges := filteredRuntimeEdges(currentSnapshot.RuntimeEdges, currentPaths)
	changes = append(changes, diffRuntimeEdgeChanges("startup_edge_added", "startup", prevEdges, currEdges)...)
	changes = append(changes, diffRuntimeEdgeChanges("startup_edge_removed", "startup", currEdges, prevEdges)...)
	prevSettings := mapFilteredSettings(previousSnapshot.UnrealSettings, previousPaths)
	currSettings := mapFilteredSettings(currentSnapshot.UnrealSettings, currentPaths)
	for source, curr := range currSettings {
		prev := prevSettings[source]
		changes = append(changes, diffScalarChanges("startup_map_changed", "startup", source, prev.GameDefaultMap, curr.GameDefaultMap)...)
		changes = append(changes, diffScalarChanges("startup_game_instance_changed", "startup", source, prev.GameInstanceClass, curr.GameInstanceClass)...)
	}
	return limitInvalidationChanges(dedupeInvalidationChanges(changes), limit)
}

func diffNamedChanges(kind string, scope string, owner string, previous []string, current []string) []InvalidationChange {
	prevSet := map[string]struct{}{}
	for _, item := range analysisUniqueStrings(previous) {
		prevSet[item] = struct{}{}
	}
	changes := []InvalidationChange{}
	for _, item := range analysisUniqueStrings(current) {
		if _, ok := prevSet[item]; ok {
			continue
		}
		changes = append(changes, InvalidationChange{
			Kind:    kind,
			Scope:   scope,
			Owner:   firstNonBlankAnalysisString(owner, "unknown"),
			Subject: item,
		})
	}
	return changes
}

func diffScalarChanges(kind string, scope string, source string, previous string, current string) []InvalidationChange {
	if strings.TrimSpace(previous) == strings.TrimSpace(current) {
		return nil
	}
	if strings.TrimSpace(previous) == "" && strings.TrimSpace(current) == "" {
		return nil
	}
	return []InvalidationChange{{
		Kind:   kind,
		Scope:  scope,
		Owner:  firstNonBlankAnalysisString(source, "settings"),
		Before: firstNonBlankAnalysisString(previous, "none"),
		After:  firstNonBlankAnalysisString(current, "none"),
		Source: source,
	}}
}

func mapFilteredNetworkSurfaces(items []UnrealNetworkSurface, paths []string) map[string]UnrealNetworkSurface {
	fileSet := toPathSet(paths)
	out := map[string]UnrealNetworkSurface{}
	for _, item := range items {
		if _, ok := fileSet[item.File]; !ok {
			continue
		}
		out[firstNonBlankAnalysisString(item.TypeName, item.File)] = item
	}
	return out
}

func mapFilteredAssetRefs(items []UnrealAssetReference, paths []string) map[string]UnrealAssetReference {
	fileSet := toPathSet(paths)
	out := map[string]UnrealAssetReference{}
	for _, item := range items {
		if _, ok := fileSet[item.File]; !ok {
			continue
		}
		out[firstNonBlankAnalysisString(item.OwnerName, item.File)] = item
	}
	return out
}

func mapFilteredSystems(items []UnrealGameplaySystem, paths []string) map[string]UnrealGameplaySystem {
	fileSet := toPathSet(paths)
	out := map[string]UnrealGameplaySystem{}
	for _, item := range items {
		if _, ok := fileSet[item.File]; !ok {
			continue
		}
		out[firstNonBlankAnalysisString(item.System, item.File)] = item
	}
	return out
}

func mapFilteredSettings(items []UnrealProjectSetting, paths []string) map[string]UnrealProjectSetting {
	fileSet := toPathSet(paths)
	out := map[string]UnrealProjectSetting{}
	for _, item := range items {
		if _, ok := fileSet[item.SourceFile]; !ok {
			continue
		}
		out[item.SourceFile] = item
	}
	return out
}

func filteredProjectEdges(items []ProjectEdge, paths []string) []ProjectEdge {
	fileSet := toPathSet(paths)
	out := []ProjectEdge{}
	for _, item := range items {
		if !edgeTouchesFiles(item.Evidence, fileSet) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func filteredRuntimeEdges(items []RuntimeEdge, paths []string) []RuntimeEdge {
	fileSet := toPathSet(paths)
	out := []RuntimeEdge{}
	for _, item := range items {
		if !edgeTouchesFiles(item.Evidence, fileSet) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func diffProjectEdgeChanges(kind string, scope string, previous []ProjectEdge, current []ProjectEdge, types ...string) []InvalidationChange {
	prevSet := map[string]struct{}{}
	typeSet := toLowerSet(types)
	for _, edge := range previous {
		if len(typeSet) > 0 {
			if _, ok := typeSet[strings.ToLower(strings.TrimSpace(edge.Type))]; !ok {
				continue
			}
		}
		prevSet[strings.ToLower(edge.Source+"|"+edge.Type+"|"+edge.Target)] = struct{}{}
	}
	changes := []InvalidationChange{}
	for _, edge := range current {
		if len(typeSet) > 0 {
			if _, ok := typeSet[strings.ToLower(strings.TrimSpace(edge.Type))]; !ok {
				continue
			}
		}
		key := strings.ToLower(edge.Source + "|" + edge.Type + "|" + edge.Target)
		if _, ok := prevSet[key]; ok {
			continue
		}
		changes = append(changes, InvalidationChange{
			Kind:    kind,
			Scope:   scope,
			Owner:   edge.Source,
			Subject: edge.Target,
			After:   edge.Type,
		})
	}
	return changes
}

func diffRuntimeEdgeChanges(kind string, scope string, previous []RuntimeEdge, current []RuntimeEdge) []InvalidationChange {
	prevSet := map[string]struct{}{}
	for _, edge := range previous {
		prevSet[strings.ToLower(edge.Source+"|"+edge.Kind+"|"+edge.Target)] = struct{}{}
	}
	changes := []InvalidationChange{}
	for _, edge := range current {
		key := strings.ToLower(edge.Source + "|" + edge.Kind + "|" + edge.Target)
		if _, ok := prevSet[key]; ok {
			continue
		}
		changes = append(changes, InvalidationChange{
			Kind:    kind,
			Scope:   scope,
			Owner:   edge.Source,
			Subject: edge.Target,
			After:   edge.Kind,
		})
	}
	return changes
}

func toPathSet(paths []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, path := range analysisUniqueStrings(paths) {
		if strings.TrimSpace(path) == "" {
			continue
		}
		out[path] = struct{}{}
	}
	return out
}

func toLowerSet(items []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range items {
		trimmed := strings.ToLower(strings.TrimSpace(item))
		if trimmed == "" {
			continue
		}
		out[trimmed] = struct{}{}
	}
	return out
}

func dedupeInvalidationChanges(items []InvalidationChange) []InvalidationChange {
	out := make([]InvalidationChange, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		key := strings.ToLower(strings.Join([]string{
			item.Kind,
			item.Scope,
			item.Owner,
			item.Subject,
			item.Before,
			item.After,
			item.Source,
		}, "|"))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func limitInvalidationChanges(items []InvalidationChange, limit int) []InvalidationChange {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]InvalidationChange(nil), items...)
	}
	return append([]InvalidationChange(nil), items[:limit]...)
}

func renderInvalidationChange(change InvalidationChange) string {
	switch change.Kind {
	case "rpc_added":
		return fmt.Sprintf("RPC added: %s -> %s", change.Owner, change.Subject)
	case "client_rpc_added":
		return fmt.Sprintf("Client RPC added: %s -> %s", change.Owner, change.Subject)
	case "multicast_rpc_added":
		return fmt.Sprintf("Multicast RPC added: %s -> %s", change.Owner, change.Subject)
	case "replicated_property_added":
		return fmt.Sprintf("Replicated property added: %s -> %s", change.Owner, change.Subject)
	case "repnotify_property_added":
		return fmt.Sprintf("RepNotify property added: %s -> %s", change.Owner, change.Subject)
	case "rpc_removed":
		return fmt.Sprintf("RPC removed: %s -> %s", change.Owner, change.Subject)
	case "client_rpc_removed":
		return fmt.Sprintf("Client RPC removed: %s -> %s", change.Owner, change.Subject)
	case "multicast_rpc_removed":
		return fmt.Sprintf("Multicast RPC removed: %s -> %s", change.Owner, change.Subject)
	case "replicated_property_removed":
		return fmt.Sprintf("Replicated property removed: %s -> %s", change.Owner, change.Subject)
	case "repnotify_property_removed":
		return fmt.Sprintf("RepNotify property removed: %s -> %s", change.Owner, change.Subject)
	case "config_binding_added":
		return fmt.Sprintf("Config binding added: %s -> %s", change.Owner, change.Subject)
	case "config_binding_removed":
		return fmt.Sprintf("Config binding removed: %s -> %s", change.Owner, change.Subject)
	case "asset_target_added":
		return fmt.Sprintf("Asset target added: %s -> %s", change.Owner, change.Subject)
	case "asset_target_removed":
		return fmt.Sprintf("Asset target removed: %s -> %s", change.Owner, change.Subject)
	case "load_method_added":
		return fmt.Sprintf("Load method added: %s -> %s", change.Owner, change.Subject)
	case "game_default_map_changed":
		return fmt.Sprintf("Game default map changed: %s (%s -> %s)", change.Owner, change.Before, change.After)
	case "default_game_mode_changed":
		return fmt.Sprintf("Default game mode changed: %s (%s -> %s)", change.Owner, change.Before, change.After)
	case "trust_boundary_edge_added":
		return fmt.Sprintf("Trust boundary edge added: %s -> %s [%s]", change.Owner, change.Subject, change.After)
	case "trust_boundary_edge_removed":
		return fmt.Sprintf("Trust boundary edge removed: %s -> %s [%s]", change.Owner, change.Subject, change.After)
	case "security_signal_added":
		return fmt.Sprintf("Security signal added: %s -> %s", change.Owner, change.Subject)
	case "security_signal_removed":
		return fmt.Sprintf("Security signal removed: %s -> %s", change.Owner, change.Subject)
	case "security_action_added":
		return fmt.Sprintf("Security action added: %s -> %s", change.Owner, change.Subject)
	case "gameplay_function_added":
		return fmt.Sprintf("Gameplay function added: %s -> %s", change.Owner, change.Subject)
	case "gameplay_action_added":
		return fmt.Sprintf("Gameplay action added: %s -> %s", change.Owner, change.Subject)
	case "widget_binding_added":
		return fmt.Sprintf("Widget binding added: %s -> %s", change.Owner, change.Subject)
	case "ability_binding_added":
		return fmt.Sprintf("Ability binding added: %s -> %s", change.Owner, change.Subject)
	case "effect_binding_added":
		return fmt.Sprintf("Effect binding added: %s -> %s", change.Owner, change.Subject)
	case "startup_edge_added":
		return fmt.Sprintf("Startup edge added: %s -> %s (%s)", change.Owner, change.Subject, change.After)
	case "startup_edge_removed":
		return fmt.Sprintf("Startup edge removed: %s -> %s (%s)", change.Owner, change.Subject, change.After)
	case "startup_map_changed":
		return fmt.Sprintf("Startup map changed: %s (%s -> %s)", change.Owner, change.Before, change.After)
	case "startup_game_instance_changed":
		return fmt.Sprintf("Startup game instance changed: %s (%s -> %s)", change.Owner, change.Before, change.After)
	default:
		if change.Before != "" || change.After != "" {
			return fmt.Sprintf("%s: %s (%s -> %s)", change.Kind, change.Owner, change.Before, change.After)
		}
		if change.Subject != "" {
			return fmt.Sprintf("%s: %s -> %s", change.Kind, change.Owner, change.Subject)
		}
		return change.Kind
	}
}

func trimPromptBullets(lines []string) []string {
	out := []string{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasSuffix(trimmed, ":") {
			continue
		}
		trimmed = strings.TrimPrefix(trimmed, "- ")
		out = append(out, trimmed)
	}
	return out
}

func (a *projectAnalyzer) resolveImports(snapshot *ProjectSnapshot) {
	snapshot.ImportGraph = map[string][]string{}
	snapshot.ReverseImportGraph = map[string][]string{}
	updatedFiles := make([]ScannedFile, 0, len(snapshot.Files))
	for _, file := range snapshot.Files {
		resolved := a.resolveFileImports(*snapshot, file)
		file.Imports = resolved
		updatedFiles = append(updatedFiles, file)
		snapshot.FilesByPath[file.Path] = file
		snapshot.ImportGraph[file.Path] = append([]string(nil), resolved...)
		for _, dep := range resolved {
			snapshot.ReverseImportGraph[dep] = append(snapshot.ReverseImportGraph[dep], file.Path)
		}
	}
	snapshot.Files = updatedFiles
	snapshot.FilesByDirectory = map[string][]ScannedFile{}
	for _, file := range snapshot.Files {
		snapshot.FilesByDirectory[file.Directory] = append(snapshot.FilesByDirectory[file.Directory], file)
	}
}

func (a *projectAnalyzer) resolveFileImports(snapshot ProjectSnapshot, file ScannedFile) []string {
	resolved := []string{}
	seen := map[string]struct{}{}
	for _, raw := range file.RawImports {
		for _, candidate := range a.resolveImportCandidate(snapshot, file, raw) {
			if candidate == file.Path {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			resolved = append(resolved, candidate)
		}
	}
	sort.Strings(resolved)
	return resolved
}

func (a *projectAnalyzer) resolveImportCandidate(snapshot ProjectSnapshot, file ScannedFile, raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	switch file.Extension {
	case ".go":
		return a.resolveGoImportCandidate(snapshot, raw)
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp":
		return a.resolveCStyleImportCandidate(snapshot, file, raw)
	case ".js", ".jsx", ".ts", ".tsx":
		return a.resolveJSImportCandidate(snapshot, file, raw)
	default:
		return a.resolveGenericImportCandidate(snapshot, file, raw)
	}
}

func (a *projectAnalyzer) resolveGoImportCandidate(snapshot ProjectSnapshot, raw string) []string {
	out := []string{}
	cleaned := filepath.ToSlash(strings.TrimSpace(raw))
	if snapshot.ModulePath != "" && strings.HasPrefix(cleaned, snapshot.ModulePath+"/") {
		rel := strings.TrimPrefix(cleaned, snapshot.ModulePath+"/")
		out = append(out, resolveDirectoryImport(snapshot, rel)...)
	}
	if snapshot.ModulePath != "" && cleaned == snapshot.ModulePath {
		out = append(out, resolveDirectoryImport(snapshot, "")...)
	}
	out = append(out, resolveDirectoryImport(snapshot, cleaned)...)
	base := strings.ToLower(filepath.Base(cleaned))
	for dir, files := range snapshot.FilesByDirectory {
		if strings.ToLower(filepath.Base(dir)) == base {
			for _, file := range files {
				if file.Extension == ".go" {
					out = append(out, file.Path)
				}
			}
		}
	}
	return analysisUniqueStrings(out)
}

func (a *projectAnalyzer) resolveCStyleImportCandidate(snapshot ProjectSnapshot, file ScannedFile, raw string) []string {
	out := []string{}
	if strings.HasPrefix(raw, ".") || strings.HasPrefix(raw, "/") {
		out = append(out, resolveRelativeImport(snapshot, file, raw)...)
	}
	if strings.Contains(raw, "/") {
		out = append(out, resolveDirectoryImport(snapshot, filepath.ToSlash(filepath.Dir(raw)))...)
	}
	base := strings.ToLower(filepath.Base(raw))
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	for path := range snapshot.FilesByPath {
		pathBase := strings.ToLower(filepath.Base(path))
		if pathBase == base {
			out = append(out, path)
			continue
		}
		if strings.TrimSuffix(pathBase, filepath.Ext(pathBase)) == stem {
			out = append(out, path)
		}
	}
	return analysisUniqueStrings(out)
}

func (a *projectAnalyzer) resolveJSImportCandidate(snapshot ProjectSnapshot, file ScannedFile, raw string) []string {
	out := []string{}
	if strings.HasPrefix(raw, ".") {
		out = append(out, resolveRelativeImport(snapshot, file, raw)...)
	}
	cleaned := filepath.ToSlash(filepath.Clean(raw))
	if strings.HasPrefix(cleaned, "./") || strings.HasPrefix(cleaned, "../") {
		out = append(out, resolveRelativeImport(snapshot, file, cleaned)...)
	}
	out = append(out, resolvePathWithExtensions(snapshot, cleaned, []string{".ts", ".tsx", ".js", ".jsx"})...)
	out = append(out, resolvePathWithExtensions(snapshot, filepath.ToSlash(filepath.Join(cleaned, "index")), []string{".ts", ".tsx", ".js", ".jsx"})...)
	return analysisUniqueStrings(out)
}

func (a *projectAnalyzer) resolveGenericImportCandidate(snapshot ProjectSnapshot, file ScannedFile, raw string) []string {
	out := []string{}
	if strings.HasPrefix(raw, ".") {
		out = append(out, resolveRelativeImport(snapshot, file, raw)...)
	}
	if strings.Contains(raw, "/") {
		cleaned := filepath.ToSlash(filepath.Clean(raw))
		if strings.HasPrefix(cleaned, "./") || strings.HasPrefix(cleaned, "../") {
			out = append(out, resolveRelativeImport(snapshot, file, cleaned)...)
		} else {
			out = append(out, resolveDirectoryImport(snapshot, cleaned)...)
		}
	}
	base := strings.ToLower(filepath.Base(raw))
	for path := range snapshot.FilesByPath {
		if strings.ToLower(filepath.Base(path)) == base {
			out = append(out, path)
		}
	}
	return analysisUniqueStrings(out)
}

func resolveRelativeImport(snapshot ProjectSnapshot, file ScannedFile, raw string) []string {
	baseDir := filepath.Dir(filepath.FromSlash(file.Path))
	candidate := filepath.ToSlash(filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(raw))))
	if _, ok := snapshot.FilesByPath[candidate]; ok {
		return []string{candidate}
	}
	return resolveDirectoryImport(snapshot, candidate)
}

func resolveDirectoryImport(snapshot ProjectSnapshot, dir string) []string {
	dir = strings.TrimPrefix(filepath.ToSlash(dir), "./")
	if files, ok := snapshot.FilesByDirectory[dir]; ok {
		out := make([]string, 0, len(files))
		for _, file := range files {
			out = append(out, file.Path)
		}
		return out
	}
	return nil
}

func resolvePathWithExtensions(snapshot ProjectSnapshot, base string, exts []string) []string {
	base = filepath.ToSlash(strings.TrimSpace(base))
	if base == "" {
		return nil
	}
	if _, ok := snapshot.FilesByPath[base]; ok {
		return []string{base}
	}
	out := []string{}
	for _, ext := range exts {
		candidate := base
		if !strings.HasSuffix(strings.ToLower(candidate), strings.ToLower(ext)) {
			candidate += ext
		}
		if _, ok := snapshot.FilesByPath[candidate]; ok {
			out = append(out, candidate)
		}
	}
	return analysisUniqueStrings(out)
}

func workerSystemPrompt() string {
	return strings.TrimSpace(`
You are a project analysis sub-agent.
Your entire response must be a single JSON object. The first byte must be "{" and the last byte must be "}".
Do not include Markdown fences, headings, prose before JSON, prose after JSON, comments, or trailing commas.
Return strict JSON in this exact shape:
{
  "report": {
    "title": "string",
    "scope_summary": "string",
    "responsibilities": ["string"],
    "facts": ["string"],
    "inferences": ["string"],
    "key_files": ["string"],
    "entry_points": ["string"],
    "internal_flow": ["string"],
    "dependencies": ["string"],
    "collaboration": ["string"],
    "risks": ["string"],
    "unknowns": ["string"],
    "evidence_files": ["string"],
    "root_cause_candidates": [
      {
        "title": "string",
        "candidate_chain": ["string"],
        "causal_chain": {
          "trigger": "string",
          "invalid_state": "string",
          "state_transition": "string",
          "missing_guard": "string",
          "user_visible_symptom": "string",
          "evidence_files": ["string"]
        },
        "trigger_values": ["string"],
        "expected_range": ["string"],
        "out_of_range_cases": ["string"],
        "observed_failure_path": ["string"],
        "evidence_files": ["string"],
        "disconfirming_evidence": ["string"],
        "cannot_be_root_cause_if": ["string"],
        "required_runtime_observation": ["string"],
        "verification_steps": ["string"],
        "probes": [
          {
            "title": "string",
            "kind": "log|assert|test|repro|db_config_dump|trace",
            "target": "file or function",
            "command": "string",
            "expected_signal": "string",
            "disproves_when": "string"
          }
        ],
        "confidence": "low|medium|high",
        "confidence_breakdown": {
          "causal_completeness": 0,
          "evidence_strength": 0,
          "runtime_observability": 0,
          "alternative_explanations": 0,
          "disconfirmation_strength": 0,
          "score": 0,
          "reasons": ["string"]
        },
        "needs_cross_shard_evidence": ["string"],
        "pattern_ids": ["known pattern id if the current source evidence actually matches it"]
      }
    ],
    "narrative": "string"
  }
}
Rules:
- Analyze the assigned primary files first and use reference files only to explain dependencies.
- Treat the deterministic architecture fact pack as authoritative code-derived context. If file excerpts or your reasoning appear to conflict with it, record the conflict in unknowns instead of overriding the fact pack.
- If a field is unknown or not applicable, keep the JSON key and use [] or "" instead of explanatory prose outside JSON.
- Keep the JSON compact enough to finish in one response: 3-7 high-value items per list, short strings, and a narrative under 900 characters.
- Do not include raw source excerpts, project GUIDs, or low-value build metadata unless they change architecture understanding.
- Every important claim must be grounded in evidence_files.
- responsibilities should answer what this shard owns.
- facts should contain direct file-grounded observations.
- inferences should contain higher-level interpretations derived from those facts.
- key_files and evidence_files must use exact relative paths from the Primary files or Reference files lists. Do not use basenames only.
- If a metadata file mentions other filenames that were not provided as primary/reference files, mention them as metadata item names in facts only; do not put them in key_files, evidence_files, or entry_points as inspected files.
- internal_flow should describe control flow or data flow inside the shard.
- collaboration should describe how this shard connects to other subsystems.
- For root-cause mode, root_cause_candidates should describe concrete failure chains. Focus on variables and state that may violate assumed ranges, especially input parameters, decoded payload fields, DB/config values, cached state, counters, IDs, enum values, nullable references, timestamps, and lifecycle flags.
- For root-cause mode, known root-cause patterns are only search priors. Put a pattern id in pattern_ids only when the assigned source evidence matches that pattern and the causal chain is code-supported.
- For root-cause mode, causal_chain must fill the five stages when evidence allows: trigger, invalid_state, state_transition, missing_guard, user_visible_symptom. Leave a stage empty only when this shard cannot prove it.
- For root-cause mode, needs_cross_shard_evidence should name the other subsystem evidence needed to prove or reject the candidate, not generic uncertainty.
- For root-cause mode, every candidate must be falsifiable: fill disconfirming_evidence, cannot_be_root_cause_if, and required_runtime_observation when possible.
- For root-cause mode, probes must be concrete places to log/assert/test/dump state, including the expected signal and the condition that would disprove the candidate.
- For root-cause mode, confidence_breakdown scores are 0-100 and must explain causal completeness, evidence strength, runtime observability, alternative explanations, and disconfirmation strength.
- The provided file context may be truncated to excerpts. If code is visibly partial, record that as source-state evidence such as "context-truncated" instead of using informal wording like "snippet-limited" in final prose.
- If unsure, put the point in unknowns instead of asserting it as fact, but do not list symbols from the visible file as unknown architectural components when the limitation is just truncated context.
`)
}

func reviewerSystemPrompt() string {
	return strings.TrimSpace(`
You are the conductor reviewing a sub-agent report.
Your entire response must be a single JSON object. The first byte must be "{" and the last byte must be "}".
Do not include Markdown fences, headings, prose before JSON, prose after JSON, comments, or trailing commas.
Return strict JSON:
{
  "decision": {
    "status": "approved" | "needs_revision",
    "issues": ["string"],
    "revision_prompt": "string",
    "symptom_possible": "yes|no|partial|unknown",
    "symptom_causality": ["string"],
    "symptom_reproduction_bridge": ["string"],
    "required_runtime_observation": ["string"],
    "disqualifying_evidence": ["string"],
    "causal_chain_complete": false,
    "causal_chain_stages": ["trigger|invalid_state|state_transition|missing_guard|user_visible_symptom"],
    "causal_chain_missing": ["trigger|invalid_state|state_transition|missing_guard|user_visible_symptom"],
    "disconfirmed": false,
    "disconfirming_evidence": ["string"],
    "rejected_candidates": ["string"],
    "evidence_requests": [
      {
        "request": "string",
        "target_signals": ["string"],
        "target_files": ["string"],
        "reason": "string",
        "required_to_prove": "string"
      }
    ]
  }
}
Approve only if the report is specific, grounded, and suitable for the requested mode-specific final document.
Reject reports that are generic, omit control/data flow, or cite evidence files outside the shard scope.
Use the deterministic architecture fact pack as authoritative review context; reject reports that contradict exact source anchors, closed top-level directory facts, or flow separation invariants.
Prefer reports that separate direct facts from higher-level inferences.
For root-cause mode, validate causality against the exact user-reported symptom, not just whether the candidate is a real bug.
For root-cause mode, known root-cause patterns are priors only; approve pattern-backed claims only when current source evidence independently proves the causal chain.
For root-cause mode, evaluate the five causal stages: trigger -> invalid_state -> state_transition -> missing_guard -> user_visible_symptom.
For root-cause mode, set symptom_possible:
- yes: the report shows a complete plausible chain from trigger value/state to the reported symptom.
- partial: the shard shows a necessary part of the chain and names specific cross-shard evidence needed.
- no: the issue may be real but cannot produce the reported symptom.
- unknown: the report lacks enough evidence to judge symptom causality.
For root-cause mode, set causal_chain_complete=true only when all five stages are supported by evidence. Fill causal_chain_stages with supported stages and causal_chain_missing with missing stages.
For root-cause mode, approved decisions must fill symptom_reproduction_bridge, required_runtime_observation, and disqualifying_evidence. Missing any of these means needs_revision.
For root-cause mode, set disconfirmed=true when the file evidence shows the candidate cannot produce the symptom, and explain why in disconfirming_evidence.
For root-cause mode, fill evidence_requests when the candidate is plausible but another file, symbol, config/DB read, lifecycle path, or runtime state must be inspected before the conductor can accept it. Each request must include target_signals or target_files specific enough to route another shard.
For root-cause mode, approve only when symptom_possible is yes with at least four causal stages and specific symptom_causality, or partial with at least three causal stages plus specific needs_cross_shard_evidence. Reject reports that are concrete bugs but do not explain how the user's symptom would happen or are disconfirmed by code evidence.
When a previous approved report is provided for dependency_changed cases, compare it against the new report and reject stale claims that no longer match the dependency context.
`)
}

func synthesisSystemPrompt() string {
	return strings.TrimSpace(`
You are the conductor writing the final Markdown document.
Use these sections by default:
1. Project Overview
2. Directory And Module Map
3. Execution Flow And Entry Points
4. Subsystem Breakdown
5. Dependencies And Integration Points
6. Risks And Unknowns
For root-cause investigations, replace the default structure with:
1. Reported Symptom
2. Root Causes
3. Contributing Factors
4. Detection Gaps
5. Operational Triggers
6. Evidence Trail
7. Value And State Assumption Failures
8. Verification And Falsification Steps
9. Remaining Unknowns
Requirements:
- Treat the deterministic architecture fact pack as authoritative. If approved shard reports conflict with it, prefer the fact pack and mention the conflict as an uncertainty instead of synthesizing the conflicting claim.
- Use responsibilities to explain ownership boundaries.
- Keep direct facts distinct from higher-level inferences when the reports provide both.
- Use internal_flow to describe actual runtime or data flow.
- Use collaboration to explain subsystem interaction points.
- Consolidate duplicates across shards and call out uncertain areas explicitly.
- Write a detailed document, not a compressed summary.
- Match the user's request language. If the goal is written in Korean, write the final Markdown in Korean while preserving code identifiers, paths, API names, and command names in their original spelling.
- Avoid informal "snippet" wording in final prose. Use source-state language such as "indexed source에서 확인됨", "구현이 이번 분석 산출물에 포함되지 않음", or "추론" when appropriate.
- For execution flows, include only calls or transitions explicitly reported as observed runtime/internal_flow edges. Declared public methods, available manager operations, lifecycle helper methods, and likely next steps must be listed as available operations, not as executed startup-chain steps.
- For Windows driver or kernel projects, keep initialization/state setup separate from runtime callback/filter registration. Do not say an Initialize function registers callbacks unless the provided flow explicitly says so. If both Initialize and Start/Register symbols are present, describe Initialize as state setup and Start/Register as the activation or registration path.
- For driver IOCTL/control flows, separate the user-mode control/client wrapper from the kernel IRP router, kernel device-control branch, command payload validation, and runtime enforcement callbacks. Do not place request-origin/open validation inside the DeviceIoControl command handler unless the provided report explicitly proves that call. Prefer this split when evidence is available: IRP_MJ_CREATE/open validates request origin and establishes controller state; IRP_MJ_DEVICE_CONTROL branches to the device-control handler; the command handler performs decrypt/unpack, size/shape checks, command validation, controller-state lookup, and command-specific dispatch.
- For Windows driver classification, call the whole project a WDM/kernel .sys driver when build/output evidence says driver/WDM. If the project has a minifilter/file-filter subsystem, describe that as a subsystem instead of labeling the entire project as only a minifilter driver unless build evidence explicitly says minifilter-only.
- For each subsystem, include:
  - Owned responsibilities
  - Key entry points
  - Internal execution/data flow
  - Important files
  - External dependencies and collaboration points
  - Risks and unknowns
- Prefer depth and clarity over brevity when the project has multiple shards.
`)
}

func buildWorkerPrompt(snapshot ProjectSnapshot, shard AnalysisShard, goal string, revisionPrompt string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n", goal)
	if textContainsHangul(goal) {
		b.WriteString("Response language: Korean. Write narrative fields in Korean; keep code identifiers, API names, paths, and commands unchanged.\n")
	}
	if mode := strings.TrimSpace(snapshot.AnalysisMode); mode != "" {
		fmt.Fprintf(&b, "Analysis mode: %s", mode)
		if label := projectAnalysisModePromptLabel(mode); label != "" {
			fmt.Fprintf(&b, " (%s)", label)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Shard: %s (%s)\n", shard.ID, shard.Name)
	fmt.Fprintf(&b, "Scope rule: primary files are your ownership boundary; reference files are for dependency context only.\n\n")
	if requirements := projectAnalysisModePromptRequirements(snapshot.AnalysisMode); len(requirements) > 0 {
		b.WriteString("Mode requirements:\n")
		for _, item := range requirements {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	if normalizeProjectAnalysisMode(snapshot.AnalysisMode) == "root-cause" {
		if plan := renderRootCauseInvestigationForPrompt(snapshot.RootCause, 5000); strings.TrimSpace(plan) != "" {
			b.WriteString("Normalized symptom and investigation hypotheses:\n")
			b.WriteString(plan)
			b.WriteString("\n\n")
		}
		b.WriteString("Root-cause worker checklist:\n")
		b.WriteString("- Treat this as a bounded bug triage task, not an architecture summary.\n")
		b.WriteString("- First decide whether this shard can directly affect the reported symptom. If not, explain why and list only cross-shard evidence needed.\n")
		b.WriteString("- Search for branch conditions, guard clauses, state transitions, counters, limits, cache flags, persistence reads, retry loops, lifecycle state, tool availability, and finalization gates.\n")
		b.WriteString("- For every candidate, structure causal_chain as five stages: trigger -> invalid_state -> state_transition -> missing_guard -> user_visible_symptom.\n")
		b.WriteString("- For every candidate, fill root_cause_candidates with trigger_values, expected_range, out_of_range_cases, observed_failure_path, confidence, and evidence_files.\n")
		b.WriteString("- Also fill disconfirming_evidence, cannot_be_root_cause_if, required_runtime_observation, and verification_steps so the reviewer can reject false positives.\n")
		b.WriteString("- Fill probes and confidence_breakdown. Probes should name where to log/assert/test/dump state, expected_signal, and disproves_when.\n")
		b.WriteString("- If regression memory lists a previous rejection/disconfirmation, treat it as a prior to re-check, not as proof by itself.\n")
		b.WriteString("- Prefer one concrete candidate over many vague risks. A candidate must link an unexpected value/state to the observed symptom.\n\n")
	}
	if baseline := renderAnalysisBaselineMapPrompt(snapshot.BaselineMap, shard); strings.TrimSpace(baseline) != "" {
		b.WriteString("Baseline architecture map:\n")
		b.WriteString(baseline)
		b.WriteString("\n\n")
	}
	if intent := analysisShardIntent(shard.Name); intent != "" {
		fmt.Fprintf(&b, "Shard intent:\n%s\n\n", intent)
	}
	if focus := buildSemanticShardFocus(snapshot, shard); strings.TrimSpace(focus) != "" {
		fmt.Fprintf(&b, "Semantic focus:\n%s\n\n", focus)
	}
	if factPack := renderArchitectureFactPackForPrompt(snapshot.ArchitectureFacts, shard, 4200); strings.TrimSpace(factPack) != "" {
		b.WriteString(factPack)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Primary files:\n%s\n\n", joinListForPrompt(shard.PrimaryFiles))
	if len(shard.ReferenceFiles) > 0 {
		fmt.Fprintf(&b, "Reference files:\n%s\n\n", joinListForPrompt(shard.ReferenceFiles))
	}
	if strings.TrimSpace(revisionPrompt) != "" {
		fmt.Fprintf(&b, "Revision instructions:\n%s\n\n", revisionPrompt)
	}
	b.WriteString("Output requirements:\n")
	b.WriteString("- Keep scope_summary to 2-4 sentences.\n")
	b.WriteString("- Keep each list to the 3-7 strongest architecture points; prefer concise strings over exhaustive inventories.\n")
	b.WriteString("- Keep narrative under 900 characters and do not repeat every list item inside it.\n")
	b.WriteString("- List concrete responsibilities, not generic statements.\n")
	b.WriteString("- facts should be direct observations grounded in the provided files.\n")
	b.WriteString("- inferences should be clearly labeled interpretations derived from those facts.\n")
	b.WriteString("- key_files and evidence_files must use exact relative paths from the Primary files or Reference files lists. Do not use basenames only.\n")
	b.WriteString("- If a metadata file mentions other filenames that were not provided as primary/reference files, mention them as metadata item names in facts only; do not put them in key_files, evidence_files, or entry_points as inspected files.\n")
	b.WriteString("- entry_points should name files/functions when visible.\n")
	b.WriteString("- internal_flow should explain execution or data flow in steps.\n")
	b.WriteString("- collaboration should mention external subsystems or shared files.\n")
	b.WriteString("- If file excerpts appear truncated, say the analysis is context-truncated/source-limited instead of using informal snippet wording.\n")
	b.WriteString("- Do not cite files outside the provided primary/reference lists.\n\n")
	if strings.HasPrefix(shard.Name, "startup") {
		b.WriteString("- For startup shards, emphasize bootstrap order, target/module ownership, and early runtime handoff.\n")
		b.WriteString("- Keep visible main/startup calls separate from declared manager APIs or available lifecycle operations that are not visibly called.\n")
	}
	if strings.Contains(shard.Name, "security_driver") {
		b.WriteString("- For driver shards, identify kernel-facing entry points, load/state-initialization flow, runtime callback/filter registration flow, and privileged ownership as separate paths when the code exposes separate Initialize and Start/Register symbols.\n")
		b.WriteString("- If object/handle filter Initialize and Start/Register symbols both appear, treat Initialize as state setup and Start/Register as callback registration unless a visible direct call proves otherwise.\n")
	}
	if strings.Contains(shard.Name, "security_ioctl") {
		b.WriteString("- For IOCTL shards, identify the IRP router branch, device-control handler, payload decrypt/unpack, size/shape checks, command validation, controller-state lookup, and command-specific dispatch. Keep create/open request-origin validation separate unless the code explicitly calls it from the DeviceIoControl handler.\n")
	}
	if strings.Contains(shard.Name, "security_handles") {
		b.WriteString("- For handle shards, identify handle acquisition, duplication, and access-check boundaries.\n")
	}
	if strings.Contains(shard.Name, "security_memory") {
		b.WriteString("- For memory shards, identify remote-memory read/write/scan flow and tamper-sensitive buffers.\n")
	}
	if strings.Contains(shard.Name, "security_rpc") {
		b.WriteString("- For security RPC shards, identify RPC/IPC/pipe validation, dispatch ownership, and authority boundaries.\n")
	}
	if strings.Contains(shard.Name, "network") {
		b.WriteString("- For network shards, identify authority boundaries, server/client RPC intent, and replicated state ownership.\n")
	}
	if strings.Contains(shard.Name, "ability") {
		b.WriteString("- For ability shards, identify ASC, abilities, effects, attribute sets, and action-to-effect flow.\n")
	}
	if strings.Contains(shard.Name, "ui") {
		b.WriteString("- For UI shards, identify widget ownership, screen composition, and widget-to-gameplay coupling.\n")
	}
	if strings.Contains(shard.Name, "asset_config") {
		b.WriteString("- For asset/config shards, identify config-driven startup, asset path bindings, and runtime load points.\n")
	}
	if strings.Contains(shard.Name, "integrity_security") {
		b.WriteString("- For integrity/security shards, identify trust boundaries, tamper-sensitive state, and anti-cheat enforcement paths.\n")
	}
	b.WriteString("\n")
	b.WriteString("Note: each file excerpt below may include only the first part of the file, not the full file.\n\n")
	b.WriteString("File context:\n")
	for _, section := range buildFileContext(snapshot, append(append([]string(nil), shard.PrimaryFiles...), shard.ReferenceFiles...), 10) {
		b.WriteString(section)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func renderAnalysisBaselineMapPrompt(baseline AnalysisBaselineMap, shard AnalysisShard) string {
	if strings.TrimSpace(baseline.RunID) == "" && strings.TrimSpace(baseline.ProjectSummary) == "" && len(baseline.Subsystems) == 0 {
		return ""
	}
	var b strings.Builder
	if strings.TrimSpace(baseline.RunID) != "" {
		fmt.Fprintf(&b, "- Baseline run: %s", baseline.RunID)
		if strings.TrimSpace(baseline.ArtifactPath) != "" {
			fmt.Fprintf(&b, " (%s)", baseline.ArtifactPath)
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(baseline.Goal) != "" {
		fmt.Fprintf(&b, "- Baseline goal: %s\n", baseline.Goal)
	}
	if strings.TrimSpace(baseline.ProjectSummary) != "" {
		fmt.Fprintf(&b, "- Project summary: %s\n", baseline.ProjectSummary)
	}
	if strings.TrimSpace(baseline.PrimaryStartup) != "" {
		fmt.Fprintf(&b, "- Primary startup: %s\n", baseline.PrimaryStartup)
	}
	if len(baseline.Subsystems) > 0 {
		fmt.Fprintf(&b, "- Subsystems: %s\n", strings.Join(limitStrings(baseline.Subsystems, 10), "; "))
	}
	relevantAnchors := relevantBaselineAnchors(baseline.SourceAnchors, shard)
	if len(relevantAnchors) > 0 {
		fmt.Fprintf(&b, "- Relevant anchors: %s\n", strings.Join(limitStrings(relevantAnchors, 10), "; "))
	} else if len(baseline.SourceAnchors) > 0 {
		fmt.Fprintf(&b, "- Source anchors: %s\n", strings.Join(limitStrings(baseline.SourceAnchors, 10), "; "))
	}
	if len(baseline.TopFiles) > 0 {
		fmt.Fprintf(&b, "- Top files: %s\n", strings.Join(limitStrings(baseline.TopFiles, 10), "; "))
	}
	b.WriteString("- Use this map as prior structure only; verify trace/security/impact claims against the current assigned files and source anchors.")
	return strings.TrimSpace(b.String())
}

func renderRootCauseInvestigationForPrompt(plan RootCauseInvestigation, limit int) string {
	if !rootCauseInvestigationHasContent(plan) {
		return ""
	}
	var b strings.Builder
	if strings.TrimSpace(plan.Symptom.Symptom) != "" {
		fmt.Fprintf(&b, "- Symptom: %s\n", plan.Symptom.Symptom)
	}
	if strings.TrimSpace(plan.Symptom.ExpectedBehavior) != "" {
		fmt.Fprintf(&b, "- Expected behavior: %s\n", plan.Symptom.ExpectedBehavior)
	}
	if strings.TrimSpace(plan.Symptom.ObservedBehavior) != "" {
		fmt.Fprintf(&b, "- Observed behavior: %s\n", plan.Symptom.ObservedBehavior)
	}
	if strings.TrimSpace(plan.Symptom.Frequency) != "" {
		fmt.Fprintf(&b, "- Frequency: %s\n", plan.Symptom.Frequency)
	}
	if len(plan.Symptom.TriggerKeywords) > 0 {
		fmt.Fprintf(&b, "- Trigger keywords: %s\n", strings.Join(limitStrings(plan.Symptom.TriggerKeywords, 10), ", "))
	}
	if len(plan.Symptom.AffectedSurface) > 0 {
		fmt.Fprintf(&b, "- Affected surface: %s\n", strings.Join(limitStrings(plan.Symptom.AffectedSurface, 10), ", "))
	}
	if len(plan.Symptom.MustExplain) > 0 {
		fmt.Fprintf(&b, "- Must explain: %s\n", strings.Join(limitStrings(plan.Symptom.MustExplain, 8), " | "))
	}
	if len(plan.Hypotheses) > 0 {
		b.WriteString("Hypotheses:\n")
		for _, h := range limitRootCauseHypotheses(plan.Hypotheses, 8) {
			fmt.Fprintf(&b, "- %s %s: %s\n", strings.TrimSpace(h.ID), strings.TrimSpace(h.Title), strings.TrimSpace(h.CandidateMechanism))
			if len(h.TargetSignals) > 0 {
				fmt.Fprintf(&b, "  target_signals=%s\n", strings.Join(limitStrings(h.TargetSignals, 8), ", "))
			}
			if len(h.TargetFiles) > 0 {
				fmt.Fprintf(&b, "  target_files=%s\n", strings.Join(limitStrings(h.TargetFiles, 6), ", "))
			}
			if len(h.MustProve) > 0 {
				fmt.Fprintf(&b, "  must_prove=%s\n", strings.Join(limitStrings(h.MustProve, 4), " | "))
			}
			if len(h.MustDisprove) > 0 {
				fmt.Fprintf(&b, "  must_disprove=%s\n", strings.Join(limitStrings(h.MustDisprove, 4), " | "))
			}
		}
	}
	if len(plan.CodeMatches) > 0 {
		b.WriteString("Code matches:\n")
		for _, match := range limitRootCauseCodeMatches(plan.CodeMatches, 10) {
			fmt.Fprintf(&b, "- %s score=%d query=%s signals=%s\n", match.File, match.Score, match.Query, strings.Join(limitStrings(match.MatchedSignals, 5), ", "))
		}
	}
	if patterns := renderRootCausePatternMatchesForPrompt(plan.PatternMatches, 8); strings.TrimSpace(patterns) != "" {
		b.WriteString(patterns)
		b.WriteString("\n")
	}
	if memory := renderRootCauseRegressionMemoryForPrompt(plan.RegressionMemory, 1800); strings.TrimSpace(memory) != "" {
		b.WriteString("Regression memory from previous run:\n")
		b.WriteString(memory)
		b.WriteString("\n")
	}
	if len(plan.EvidenceRequests) > 0 {
		b.WriteString("Evidence requests:\n")
		for _, request := range limitRootCauseEvidenceRequests(plan.EvidenceRequests, 8) {
			fmt.Fprintf(&b, "- %s [%s] request=%s signals=%s files=%s routed=%s fulfilled=%s\n",
				request.ID,
				request.Status,
				firstNonBlankRootCauseString(request.Request, request.RequiredToProve),
				strings.Join(limitStrings(request.TargetSignals, 4), ", "),
				strings.Join(limitStrings(request.TargetFiles, 4), ", "),
				strings.Join(limitStrings(request.RoutedShardIDs, 4), ", "),
				strings.Join(limitStrings(request.FulfilledByShards, 4), ", "))
		}
	}
	if len(plan.DeepVerifications) > 0 {
		b.WriteString("Deep verifications:\n")
		for _, item := range limitRootCauseDeepVerifications(plan.DeepVerifications, 6) {
			fmt.Fprintf(&b, "- %s [%s, confidence=%s]: %s\n", item.CandidateTitle, item.Status, item.Confidence, item.Summary)
			if len(item.Probes) > 0 {
				fmt.Fprintf(&b, "  probes=%s\n", renderRootCauseProbeList(item.Probes, 2))
			}
		}
	}
	if len(plan.JoinedCandidates) > 0 {
		b.WriteString("Joined candidates:\n")
		for _, c := range limitRootCauseJoinedCandidates(plan.JoinedCandidates, 8) {
			fmt.Fprintf(&b, "- %s (%s, cluster=%s, confidence=%s, score=%d)\n", c.Title, c.Classification, c.ClusterID, c.Confidence, c.ConfidenceScore)
			if rootCauseCausalChainStageCount(c.CausalChain) > 0 {
				fmt.Fprintf(&b, "  causal_chain=%s\n", renderRootCauseCausalChainInline(c.CausalChain))
			}
			for _, step := range limitStrings(c.JoinedChain, 4) {
				fmt.Fprintf(&b, "  chain=%s\n", step)
			}
			if len(c.CannotBeRootCauseIf) > 0 {
				fmt.Fprintf(&b, "  cannot_be_root_cause_if=%s\n", strings.Join(limitStrings(c.CannotBeRootCauseIf, 3), " | "))
			}
			if len(c.Probes) > 0 {
				fmt.Fprintf(&b, "  probes=%s\n", renderRootCauseProbeList(c.Probes, 2))
			}
			if len(c.CompetesWith)+len(c.DependsOn)+len(c.CanCoexistWith) > 0 {
				fmt.Fprintf(&b, "  relations=competes:%s depends:%s coexist:%s\n", strings.Join(limitStrings(c.CompetesWith, 3), ", "), strings.Join(limitStrings(c.DependsOn, 3), ", "), strings.Join(limitStrings(c.CanCoexistWith, 3), ", "))
			}
			if len(c.ConfidenceBreakdown.Reasons) > 0 {
				fmt.Fprintf(&b, "  confidence_reasons=%s\n", strings.Join(limitStrings(c.ConfidenceBreakdown.Reasons, 2), " | "))
			}
		}
	}
	if len(plan.CandidateClusters) > 0 {
		b.WriteString("Candidate clusters:\n")
		for _, cluster := range limitRootCauseCandidateClusters(plan.CandidateClusters, 8) {
			fmt.Fprintf(&b, "- %s %s score=%d candidates=%s\n", cluster.ID, cluster.Title, cluster.ConfidenceScore, strings.Join(limitStrings(cluster.CandidateTitles, 4), " | "))
		}
	}
	text := strings.TrimSpace(b.String())
	if limit > 0 {
		text = analysisPromptExcerpt(text, limit)
	}
	return strings.TrimSpace(text)
}

func limitRootCauseHypotheses(items []RootCauseHypothesis, limit int) []RootCauseHypothesis {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitRootCauseCodeMatches(items []RootCauseCodeMatch, limit int) []RootCauseCodeMatch {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitRootCauseDeepVerifications(items []RootCauseDeepVerification, limit int) []RootCauseDeepVerification {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitRootCauseJoinedCandidates(items []RootCauseJoinedCandidate, limit int) []RootCauseJoinedCandidate {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitRootCauseCandidateClusters(items []RootCauseCandidateCluster, limit int) []RootCauseCandidateCluster {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitRootCauseEvidenceRequests(items []RootCauseEvidenceRequest, limit int) []RootCauseEvidenceRequest {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func renderRootCauseProbeList(probes []RootCauseProbe, limit int) string {
	parts := []string{}
	for _, probe := range limitRootCauseProbes(probes, limit) {
		parts = append(parts, fmt.Sprintf("%s:%s->%s", probe.Kind, firstNonBlankRootCauseString(probe.Target, probe.Title), firstNonBlankRootCauseString(probe.ExpectedSignal, probe.Command)))
	}
	return strings.Join(parts, " | ")
}

func limitRootCauseProbes(items []RootCauseProbe, limit int) []RootCauseProbe {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func renderRootCauseCausalChainInline(chain RootCauseCausalChain) string {
	parts := []string{}
	if strings.TrimSpace(chain.Trigger) != "" {
		parts = append(parts, "trigger="+strings.TrimSpace(chain.Trigger))
	}
	if strings.TrimSpace(chain.InvalidState) != "" {
		parts = append(parts, "invalid_state="+strings.TrimSpace(chain.InvalidState))
	}
	if strings.TrimSpace(chain.StateTransition) != "" {
		parts = append(parts, "state_transition="+strings.TrimSpace(chain.StateTransition))
	}
	if strings.TrimSpace(chain.MissingGuard) != "" {
		parts = append(parts, "missing_guard="+strings.TrimSpace(chain.MissingGuard))
	}
	if strings.TrimSpace(chain.UserVisibleSymptom) != "" {
		parts = append(parts, "user_visible_symptom="+strings.TrimSpace(chain.UserVisibleSymptom))
	}
	return strings.Join(parts, " -> ")
}

func relevantBaselineAnchors(anchors []string, shard AnalysisShard) []string {
	if len(anchors) == 0 || len(shard.PrimaryFiles)+len(shard.ReferenceFiles) == 0 {
		return nil
	}
	scope := append(append([]string(nil), shard.PrimaryFiles...), shard.ReferenceFiles...)
	relevant := []string{}
	for _, anchor := range anchors {
		anchorLower := strings.ToLower(filepath.ToSlash(strings.Trim(anchor, "/")))
		if anchorLower == "" {
			continue
		}
		for _, file := range scope {
			fileLower := strings.ToLower(filepath.ToSlash(strings.Trim(file, "/")))
			if fileLower == "" {
				continue
			}
			if anchorLower == fileLower || strings.Contains(anchorLower, fileLower) || strings.Contains(fileLower, anchorLower) || sameAnalysisDirectory(anchorLower, fileLower) {
				relevant = append(relevant, anchor)
				break
			}
		}
	}
	return analysisUniqueStrings(relevant)
}

func sameAnalysisDirectory(left string, right string) bool {
	leftDir := strings.Trim(filepath.ToSlash(filepath.Dir(left)), ".")
	rightDir := strings.Trim(filepath.ToSlash(filepath.Dir(right)), ".")
	return leftDir != "" && rightDir != "" && (strings.HasPrefix(leftDir, rightDir) || strings.HasPrefix(rightDir, leftDir))
}

func buildReviewerPrompt(snapshot ProjectSnapshot, shard AnalysisShard, report WorkerReport, goal string, previousReport WorkerReport, hasPreviousReport bool) string {
	data, _ := json.MarshalIndent(workerReportForReview(report), "", "  ")
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n", goal)
	if mode := strings.TrimSpace(snapshot.AnalysisMode); mode != "" {
		fmt.Fprintf(&b, "Analysis mode: %s", mode)
		if label := projectAnalysisModePromptLabel(mode); label != "" {
			fmt.Fprintf(&b, " (%s)", label)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Shard: %s (%s)\n", shard.ID, shard.Name)
	fmt.Fprintf(&b, "Shard cache status: %s\n", shard.CacheStatus)
	if requirements := projectAnalysisModePromptRequirements(snapshot.AnalysisMode); len(requirements) > 0 {
		b.WriteString("Mode review requirements:\n")
		for _, item := range requirements {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	if normalizeProjectAnalysisMode(snapshot.AnalysisMode) == "root-cause" {
		b.WriteString("Root-cause review checklist:\n")
		b.WriteString("- Approve only candidates that connect a concrete unexpected value/state to the reported symptom.\n")
		b.WriteString("- If a worker cites pattern_ids, verify the assigned source actually matches the known pattern; pattern similarity alone is not evidence.\n")
		b.WriteString("- Validate the full causality chain: trigger -> invalid_state -> state_transition -> missing_guard -> exact user_visible_symptom.\n")
		b.WriteString("- Fill causal_chain_complete, causal_chain_stages, and causal_chain_missing. Do not approve yes unless at least four stages are evidenced.\n")
		b.WriteString("- A candidate that describes a real defect but cannot produce the reported symptom must be rejected or listed in rejected_candidates.\n")
		b.WriteString("- Fill symptom_possible and symptom_causality. symptom_causality must explain why the symptom can happen, not just why the code is suspicious.\n")
		b.WriteString("- Fill symptom_reproduction_bridge with the concrete trigger-to-symptom path, required_runtime_observation with the runtime value/log/assert needed to confirm it, and disqualifying_evidence with what would falsify it.\n")
		b.WriteString("- Set disconfirmed=true and fill disconfirming_evidence when this shard proves the candidate cannot produce the symptom.\n")
		b.WriteString("- Reject or revise reports that only restate architecture, list generic risks, or omit trigger_values/out_of_range_cases/observed_failure_path.\n")
		b.WriteString("- If this shard has partial evidence that needs another subsystem, approve only when needs_cross_shard_evidence is specific enough for the conductor to route the next shard.\n")
		b.WriteString("- When more evidence is needed, fill evidence_requests with exact target_signals or target_files so the conductor can run additional worker shards.\n")
		b.WriteString("- Verify worker probes and confidence_breakdown; reject candidates whose probes cannot observe or disprove the user-visible symptom chain.\n")
		b.WriteString("- Prefer low confidence over false certainty when the assigned files cannot prove the full chain.\n\n")
	}
	if strings.TrimSpace(shard.InvalidationReason) != "" {
		fmt.Fprintf(&b, "Invalidation reason: %s\n", describeAnalysisInvalidationReasonWithContext(shard.InvalidationReason, []string{shard.Name}))
	}
	if intent := analysisShardIntent(shard.Name); intent != "" {
		fmt.Fprintf(&b, "Shard intent:\n%s\n", intent)
	}
	if focus := buildSemanticShardFocus(snapshot, shard); strings.TrimSpace(focus) != "" {
		fmt.Fprintf(&b, "Semantic focus:\n%s\n", focus)
	}
	if factPack := renderArchitectureFactPackForPrompt(snapshot.ArchitectureFacts, shard, 3600); strings.TrimSpace(factPack) != "" {
		fmt.Fprintf(&b, "Review against this fact pack:\n%s\n", factPack)
	}
	if reviewRules := buildSemanticReviewerChecklist(shard.Name); strings.TrimSpace(reviewRules) != "" {
		fmt.Fprintf(&b, "Semantic review checklist:\n%s\n", reviewRules)
	}
	if baseline := renderAnalysisBaselineMapPrompt(snapshot.BaselineMap, shard); strings.TrimSpace(baseline) != "" {
		fmt.Fprintf(&b, "Baseline architecture map:\n%s\n", baseline)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Assigned files:\n%s\n\n", joinListForPrompt(shard.PrimaryFiles))
	if len(shard.ReferenceFiles) > 0 {
		fmt.Fprintf(&b, "Allowed reference files:\n%s\n\n", joinListForPrompt(shard.ReferenceFiles))
	}
	if hasPreviousReport && (shard.InvalidationReason == "dependency_changed" || shard.InvalidationReason == "semantic_dependency_changed") {
		previousJSON, _ := json.MarshalIndent(workerReportForReview(previousReport), "", "  ")
		b.WriteString("Previous approved report for diff-aware review:\n")
		b.Write(previousJSON)
		b.WriteString("\n\nReview requirement:\n")
		b.WriteString("- Compare the new report against the previous approved report.\n")
		b.WriteString("- Focus on whether dependency changes require correcting ownership, flow, or integration claims.\n\n")
	}
	b.WriteString("Report JSON:\n")
	b.Write(data)
	b.WriteString("\n\nFile context:\n")
	for _, section := range buildFileContext(snapshot, shard.PrimaryFiles, 6) {
		b.WriteString(section)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func workerReportForReview(report WorkerReport) WorkerReport {
	report.Raw = ""
	return report
}

func buildSynthesisPrompt(snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, goal string) string {
	items := groupedReportsForSynthesis(shards, reports)
	data, _ := json.MarshalIndent(items, "", "  ")
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n", goal)
	if mode := strings.TrimSpace(snapshot.AnalysisMode); mode != "" {
		fmt.Fprintf(&b, "Analysis mode: %s", mode)
		if label := projectAnalysisModePromptLabel(mode); label != "" {
			fmt.Fprintf(&b, " (%s)", label)
		}
		b.WriteString("\n")
		if requirements := projectAnalysisModePromptRequirements(mode); len(requirements) > 0 {
			b.WriteString("Mode synthesis requirements:\n")
			for _, item := range requirements {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			if normalizeProjectAnalysisMode(mode) == "root-cause" {
				b.WriteString("- Rank suspected root causes by likelihood and impact; include confidence and the strongest evidence files for each candidate.\n")
				b.WriteString("- Do not present reviewer-rejected or weakly grounded speculation as fact; keep it under Remaining Unknowns or Verification Steps.\n")
				b.WriteString("- Include concrete reproduction or instrumentation steps that would confirm the candidate path.\n")
				b.WriteString("- Split final findings into Root Causes, Contributing Factors, Detection Gaps, Operational Triggers, and Remaining Unknowns.\n")
				b.WriteString("- For each root cause, include cannot_be_root_cause_if and required_runtime_observation so the user can falsify it.\n")
				b.WriteString("- Include concrete probes and confidence breakdowns when joined candidates provide them.\n")
				b.WriteString("- Mention candidate clusters only when they clarify duplicate findings across shards.\n")
			}
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "Workspace: %s\n", snapshot.Root)
	if len(snapshot.AnalysisLenses) > 0 {
		lensNames := []string{}
		for _, lens := range snapshot.AnalysisLenses {
			lensNames = append(lensNames, lens.Type)
		}
		fmt.Fprintf(&b, "Analysis lenses: %s\n", strings.Join(lensNames, ", "))
	}
	if normalizeProjectAnalysisMode(snapshot.AnalysisMode) == "root-cause" {
		if plan := renderRootCauseInvestigationForPrompt(snapshot.RootCause, 9000); strings.TrimSpace(plan) != "" {
			b.WriteString("\nRoot-cause normalized symptom, hypotheses, and joined candidates:\n")
			b.WriteString(plan)
			b.WriteString("\n\n")
		}
	}
	if baseline := renderAnalysisBaselineMapPrompt(snapshot.BaselineMap, AnalysisShard{}); strings.TrimSpace(baseline) != "" {
		fmt.Fprintf(&b, "\nBaseline architecture map:\n%s\n\n", baseline)
	}
	if factPack := renderArchitectureFactPackForPrompt(snapshot.ArchitectureFacts, AnalysisShard{}, 7000); strings.TrimSpace(factPack) != "" {
		b.WriteString("\n")
		b.WriteString(factPack)
		b.WriteString("\n\n")
	}
	if snapshot.ModulePath != "" {
		fmt.Fprintf(&b, "Go module: %s\n", snapshot.ModulePath)
	}
	if len(snapshot.UnrealProjects) > 0 || len(snapshot.UnrealModules) > 0 || len(snapshot.UnrealTargets) > 0 {
		fmt.Fprintf(&b, "Unreal profile: projects=%d plugins=%d targets=%d modules=%d reflected_types=%d network_surfaces=%d asset_bindings=%d gameplay_systems=%d settings=%d\n", len(snapshot.UnrealProjects), len(snapshot.UnrealPlugins), len(snapshot.UnrealTargets), len(snapshot.UnrealModules), len(snapshot.UnrealTypes), len(snapshot.UnrealNetwork), len(snapshot.UnrealAssets), len(snapshot.UnrealSystems), len(snapshot.UnrealSettings))
		if strings.TrimSpace(snapshot.PrimaryUnrealModule) != "" {
			fmt.Fprintf(&b, "Primary Unreal module: %s\n", snapshot.PrimaryUnrealModule)
		}
	}
	fmt.Fprintf(&b, "Files: %d\nLines: %d\n\n", snapshot.TotalFiles, snapshot.TotalLines)
	if len(snapshot.Files) > 0 {
		b.WriteString("Top important files:\n")
		for _, file := range topImportantFiles(snapshot, 12) {
			fmt.Fprintf(&b, "- %s (score=%d reasons=%s)\n", file.Path, file.ImportanceScore, strings.Join(limitStrings(file.ImportanceReasons, 3), ", "))
		}
		b.WriteString("\n")
	}
	if len(snapshot.ProjectEdges) > 0 {
		b.WriteString("Project edges:\n")
		for _, edge := range limitProjectEdges(snapshot.ProjectEdges, 20) {
			fmt.Fprintf(&b, "- %s -> %s [%s, confidence=%s]\n", edge.Source, edge.Target, edge.Type, edge.Confidence)
		}
		b.WriteString("\n")
	}
	if dirs := synthesisTopLevelDirectories(snapshot); len(dirs) > 0 {
		b.WriteString("Top-level directory facts:\n")
		b.WriteString("- Closed set for top-level directory maps: " + strings.Join(dirs, ", ") + "\n")
		b.WriteString("- Do not list headers, source files, project files, INF files, or nested folders as top-level directories.\n\n")
	}
	if facts := synthesisDriverArchitectureFacts(snapshot); len(facts) > 0 {
		b.WriteString("Driver architecture facts:\n")
		for _, fact := range facts {
			fmt.Fprintf(&b, "- %s\n", fact)
		}
		b.WriteString("\n")
	}
	if facts := synthesisReportGuardrailFacts(reports); len(facts) > 0 {
		b.WriteString("Synthesis guardrails from worker evidence:\n")
		for _, fact := range facts {
			fmt.Fprintf(&b, "- %s\n", fact)
		}
		b.WriteString("\n")
	}
	if len(snapshot.ManifestFiles) > 0 {
		fmt.Fprintf(&b, "Manifest files:\n%s\n\n", joinListForPrompt(snapshot.ManifestFiles))
	}
	if len(snapshot.SolutionProjects) > 0 {
		b.WriteString("Solution projects:\n")
		for _, project := range snapshot.SolutionProjects {
			line := fmt.Sprintf("- %s (%s", project.Name, project.Path)
			if strings.TrimSpace(project.OutputType) != "" {
				line += ", output=" + project.OutputType
			}
			if project.StartupCandidate {
				line += ", startup_candidate=true"
			}
			line += ")\n"
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	if len(snapshot.UnrealProjects) > 0 {
		b.WriteString("Unreal projects:\n")
		for _, project := range snapshot.UnrealProjects {
			fmt.Fprintf(&b, "- %s (%s) modules=%s plugins=%s\n", project.Name, project.Path, strings.Join(limitStrings(project.Modules, 6), ", "), strings.Join(limitStrings(project.Plugins, 6), ", "))
		}
		b.WriteString("\n")
	}
	if len(snapshot.UnrealTargets) > 0 {
		b.WriteString("Unreal targets:\n")
		for _, target := range snapshot.UnrealTargets {
			fmt.Fprintf(&b, "- %s (%s, type=%s) modules=%s\n", target.Name, target.Path, target.TargetType, strings.Join(limitStrings(target.Modules, 6), ", "))
		}
		b.WriteString("\n")
	}
	if len(snapshot.UnrealModules) > 0 {
		b.WriteString("Unreal modules:\n")
		for _, module := range limitUnrealModules(snapshot.UnrealModules, 16) {
			fmt.Fprintf(&b, "- %s (%s", module.Name, module.Kind)
			if strings.TrimSpace(module.Plugin) != "" {
				fmt.Fprintf(&b, ", plugin=%s", module.Plugin)
			}
			fmt.Fprintf(&b, ") public=%s private=%s dynamic=%s evidence=%s\n",
				strings.Join(limitStrings(module.PublicDependencies, 4), ", "),
				strings.Join(limitStrings(module.PrivateDependencies, 4), ", "),
				strings.Join(limitStrings(module.DynamicallyLoaded, 4), ", "),
				module.Path)
		}
		b.WriteString("\n")
	}
	if len(snapshot.UnrealTypes) > 0 {
		b.WriteString("Unreal reflected types:\n")
		for _, item := range limitUnrealTypes(snapshot.UnrealTypes, 14) {
			fmt.Fprintf(&b, "- %s (%s", item.Name, item.Kind)
			if strings.TrimSpace(item.GameplayRole) != "" {
				fmt.Fprintf(&b, ", role=%s", item.GameplayRole)
			}
			if strings.TrimSpace(item.BaseClass) != "" {
				fmt.Fprintf(&b, ", base=%s", item.BaseClass)
			}
			if item.DefaultPawnClass != "" {
				fmt.Fprintf(&b, ", default_pawn=%s", item.DefaultPawnClass)
			}
			if item.PlayerControllerClass != "" {
				fmt.Fprintf(&b, ", player_controller=%s", item.PlayerControllerClass)
			}
			if item.HUDClass != "" {
				fmt.Fprintf(&b, ", hud=%s", item.HUDClass)
			}
			fmt.Fprintf(&b, ", file=%s)\n", item.File)
			if len(item.BlueprintCallableFunctions) > 0 {
				fmt.Fprintf(&b, "  blueprint_callable=%s\n", strings.Join(limitStrings(item.BlueprintCallableFunctions, 4), ", "))
			}
			if len(item.BlueprintEventFunctions) > 0 {
				fmt.Fprintf(&b, "  blueprint_events=%s\n", strings.Join(limitStrings(item.BlueprintEventFunctions, 4), ", "))
			}
		}
		b.WriteString("\n")
	}
	if len(snapshot.UnrealNetwork) > 0 {
		b.WriteString("Unreal network surfaces:\n")
		for _, item := range limitUnrealNetwork(snapshot.UnrealNetwork, 12) {
			fmt.Fprintf(&b, "- %s (%s) server=%s client=%s multicast=%s replicated=%s repnotify=%s has_replication_list=%t\n",
				firstNonBlankAnalysisString(item.TypeName, item.File),
				item.File,
				strings.Join(limitStrings(item.ServerRPCs, 4), ", "),
				strings.Join(limitStrings(item.ClientRPCs, 4), ", "),
				strings.Join(limitStrings(item.MulticastRPCs, 4), ", "),
				strings.Join(limitStrings(item.ReplicatedProperties, 4), ", "),
				strings.Join(limitStrings(item.RepNotifyProperties, 4), ", "),
				item.HasReplicationList,
			)
		}
		b.WriteString("\n")
	}
	if len(snapshot.UnrealAssets) > 0 {
		b.WriteString("Unreal asset and config bindings:\n")
		for _, item := range limitUnrealAssets(snapshot.UnrealAssets, 12) {
			fmt.Fprintf(&b, "- %s (%s) assets=%s canonical=%s config=%s load=%s\n",
				firstNonBlankAnalysisString(item.OwnerName, item.File),
				item.File,
				strings.Join(limitStrings(item.AssetPaths, 4), ", "),
				strings.Join(limitStrings(item.CanonicalTargets, 4), ", "),
				strings.Join(limitStrings(item.ConfigKeys, 4), ", "),
				strings.Join(limitStrings(item.LoadMethods, 4), ", "),
			)
		}
		b.WriteString("\n")
	}
	if len(snapshot.UnrealSystems) > 0 {
		b.WriteString("Unreal gameplay systems:\n")
		for _, item := range limitUnrealSystems(snapshot.UnrealSystems, 12) {
			fmt.Fprintf(&b, "- %s owner=%s module=%s file=%s signals=%s assets=%s functions=%s owned_by=%s targets=%s actions=%s contexts=%s widgets=%s abilities=%s effects=%s attributes=%s\n",
				item.System,
				firstNonBlankAnalysisString(item.OwnerName, item.File),
				item.Module,
				item.File,
				strings.Join(limitStrings(item.Signals, 4), ", "),
				strings.Join(limitStrings(item.Assets, 4), ", "),
				strings.Join(limitStrings(item.Functions, 4), ", "),
				strings.Join(limitStrings(item.OwnedBy, 4), ", "),
				strings.Join(limitStrings(item.Targets, 4), ", "),
				strings.Join(limitStrings(item.Actions, 4), ", "),
				strings.Join(limitStrings(item.Contexts, 4), ", "),
				strings.Join(limitStrings(item.Widgets, 4), ", "),
				strings.Join(limitStrings(item.Abilities, 4), ", "),
				strings.Join(limitStrings(item.Effects, 4), ", "),
				strings.Join(limitStrings(item.Attributes, 4), ", "),
			)
		}
		b.WriteString("\n")
	}
	if len(snapshot.UnrealSettings) > 0 {
		b.WriteString("Unreal startup settings:\n")
		for _, item := range snapshot.UnrealSettings {
			fmt.Fprintf(&b, "- %s game_map=%s editor_map=%s game_mode=%s game_instance=%s pawn=%s controller=%s hud=%s\n",
				item.SourceFile,
				item.GameDefaultMap,
				item.EditorStartupMap,
				item.GlobalDefaultGameMode,
				item.GameInstanceClass,
				item.DefaultPawnClass,
				item.PlayerControllerClass,
				item.HUDClass,
			)
		}
		b.WriteString("\n")
	}
	runtimeEdges := highConfidenceRuntimeEdges(snapshot.RuntimeEdges)
	operationalEdges := buildOperationalChain(snapshot, reports)
	if len(runtimeEdges) > 0 {
		b.WriteString("Runtime edges:\n")
		for _, edge := range runtimeEdges {
			fmt.Fprintf(&b, "- %s -> %s (%s, confidence=%s)", edge.Source, edge.Target, edge.Kind, edge.Confidence)
			if len(edge.Evidence) > 0 {
				fmt.Fprintf(&b, " evidence=%s", strings.Join(limitStrings(edge.Evidence, 2), ", "))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(operationalEdges) > 0 {
		b.WriteString("Operational chain candidates:\n")
		for _, edge := range operationalEdges {
			fmt.Fprintf(&b, "- %s -> %s (%s, confidence=%s)", edge.Source, edge.Target, edge.Kind, edge.Confidence)
			if len(edge.Evidence) > 0 {
				fmt.Fprintf(&b, " evidence=%s", strings.Join(limitStrings(edge.Evidence, 1), ", "))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(snapshot.PrimaryStartup) != "" {
		fmt.Fprintf(&b, "Solution startup candidate: %s\n\n", snapshot.PrimaryStartup)
		startupEntries := startupProjectEntryFiles(snapshot)
		if len(startupEntries) > 0 {
			fmt.Fprintf(&b, "Startup candidate entry files:\n%s\n\n", joinListForPrompt(startupEntries))
		}
	}
	if len(snapshot.EntrypointFiles) > 0 {
		fmt.Fprintf(&b, "Entrypoint files:\n%s\n\n", joinListForPrompt(snapshot.EntrypointFiles))
	}
	b.WriteString("Approved shard reports:\n")
	b.Write(data)
	b.WriteString("\n\nSynthesis requirements:\n")
	if textContainsHangul(goal) {
		b.WriteString("- Write the final Markdown document in Korean because the user's goal is Korean. Keep code identifiers, APIs, filenames, paths, commands, and build configuration names unchanged.\n")
	}
	b.WriteString("- Turn internal_flow bullets into a coherent execution-flow section.\n")
	b.WriteString("- Turn collaboration bullets into explicit subsystem integration descriptions.\n")
	b.WriteString("- Preserve uncertainty by collecting unknowns under Risks And Unknowns.\n")
	b.WriteString("- Expand subsystem sections instead of collapsing them into short bullets only.\n")
	b.WriteString("- For each subsystem, include explicit Facts and Inferences subsections when that data is available.\n")
	b.WriteString("- Tiny metadata or script shards may be merged into a higher-level operational subsystem for readability.\n")
	b.WriteString("- Organize subsystem sections under higher-level architecture groups such as Agent Runtime, Safety Control Plane, Evidence And Memory Plane, Developer Tooling, and Operational Metadata when applicable.\n")
	if isVisualStudioCppProject(snapshot) {
		b.WriteString("- This workspace matches a Visual Studio / C++ multi-project solution. Prefer higher-level sections such as Bootstrap, Orchestration, Worker And Scanner Execution, Monitoring, Shared Common Services, Protection And Hardening, Build And Release, and Dependency Catalog.\n")
		b.WriteString("- Keep product-owned modules ahead of dependency catalog content.\n")
		if strings.TrimSpace(snapshot.PrimaryStartup) != "" {
			fmt.Fprintf(&b, "- Explicitly state that %s is the solution startup candidate, not necessarily the only runtime entrypoint.\n", snapshot.PrimaryStartup)
			b.WriteString("- In Project Overview and Execution Flow, separate the user-mode startup executable, SCM/service activation path, and driver/runtime entrypoint when the project includes driver or service modules.\n")
		}
		if len(runtimeEdges) > 0 {
			b.WriteString("- Reconstruct one explicit primary startup chain using only the provided high-confidence runtime edges.\n")
			b.WriteString("- Name the source and target modules directly when describing startup activation.\n")
		}
		if len(operationalEdges) > 0 {
			b.WriteString("- Separately describe one operational security chain using the provided operational chain candidates.\n")
			b.WriteString("- Do not merge the primary startup chain and the operational security chain into a single ambiguous sequence.\n")
		}
	}
	b.WriteString("- Include a short Evidence Files list for each subsystem using the most representative files.\n")
	b.WriteString("- Mention key files for each shard.\n")
	return strings.TrimSpace(b.String())
}

func synthesisTopLevelDirectories(snapshot ProjectSnapshot) []string {
	seen := map[string]string{}
	add := func(path string) {
		path = strings.Trim(strings.ReplaceAll(filepathSlashOrEmpty(path), "\\", "/"), "/")
		if path == "" || path == "." || analysisDocPathLooksLikeFile(path) {
			return
		}
		if idx := strings.Index(path, "/"); idx >= 0 {
			path = path[:idx]
		}
		if path == "" || path == "." || analysisDocPathLooksLikeFile(path) {
			return
		}
		key := strings.ToLower(path)
		if _, ok := seen[key]; !ok {
			seen[key] = path
		}
	}
	for _, dir := range snapshot.Directories {
		add(dir)
	}
	for _, file := range snapshot.Files {
		add(firstNonBlankAnalysisString(file.Directory, analysisDocDir(file.Path)))
	}
	for _, project := range snapshot.SolutionProjects {
		add(firstNonBlankAnalysisString(project.Directory, analysisDocDir(project.Path)))
	}
	items := make([]string, 0, len(seen))
	for key := range seen {
		items = append(items, key)
	}
	sort.Strings(items)
	out := []string{}
	for _, item := range items {
		out = append(out, seen[item]+"/")
	}
	return out
}

func synthesisDriverArchitectureFacts(snapshot ProjectSnapshot) []string {
	driverProjects := []string{}
	for _, project := range snapshot.SolutionProjects {
		if solutionProjectLooksLikeDriverRuntime(project) {
			driverProjects = append(driverProjects, firstNonBlankAnalysisString(project.Name, project.Path))
		}
	}
	driverEntries := driverEntrypointFiles(ProjectAnalysisRun{Snapshot: snapshot})
	if len(driverProjects) == 0 && len(driverEntries) == 0 {
		return nil
	}
	facts := []string{}
	if len(driverProjects) > 0 {
		facts = append(facts, "Driver build/runtime projects: "+strings.Join(analysisUniqueStrings(driverProjects), ", ")+". Treat these as kernel/WDM .sys runtime layers when output/kind evidence says driver.")
	}
	if strings.TrimSpace(snapshot.PrimaryStartup) != "" {
		facts = append(facts, "Solution startup candidate is "+snapshot.PrimaryStartup+"; do not treat it as the only runtime entrypoint when driver/service layers exist.")
	}
	if len(driverEntries) > 0 {
		facts = append(facts, "Kernel/runtime driver entry files: "+strings.Join(driverEntries, ", ")+". Keep these separate from user-mode startup files.")
	}
	facts = append(facts, "If a file/minifilter subsystem exists, describe it as a subsystem unless build evidence says the whole driver is minifilter-only.")
	return facts
}

func synthesisReportGuardrailFacts(reports []WorkerReport) []string {
	corpus := strings.ToLower(workerReportsCorpus(reports))
	facts := []string{}
	if containsAny(corpus, "main()") && containsAny(corpus, "declares public", "public methods", "available", "declared methods") {
		facts = append(facts, "Startup chain guardrail: only calls explicitly described as visible main/startup calls or internal_flow runtime steps are executed startup-chain steps; declared public methods and available lifecycle operations belong in an Available operations/API section.")
	}
	if containsAny(corpus, "objectfilter::initialize", "object filter::initialize", "object filter initialize", "objectfilter initialize") &&
		containsAny(corpus, "startobjectfilter", "start object filter", "obregistercallbacks") {
		facts = append(facts, "Object/handle filter guardrail: when object-filter Initialize and Start/Register evidence both exist, describe Initialize as state setup and Start/Register/ObRegisterCallbacks as callback registration unless a visible direct call proves Initialize registers callbacks.")
	}
	return analysisUniqueStrings(facts)
}

func workerReportsCorpus(reports []WorkerReport) string {
	parts := []string{}
	for _, report := range reports {
		parts = append(parts,
			report.Title,
			report.ScopeSummary,
			report.Narrative,
		)
		parts = append(parts, report.Responsibilities...)
		parts = append(parts, report.EntryPoints...)
		parts = append(parts, report.InternalFlow...)
		parts = append(parts, report.Facts...)
		parts = append(parts, report.Inferences...)
		parts = append(parts, report.Collaboration...)
		parts = append(parts, report.Risks...)
		parts = append(parts, report.Unknowns...)
		parts = append(parts, report.KeyFiles...)
		parts = append(parts, report.EvidenceFiles...)
		for _, candidate := range report.RootCauseCandidates {
			parts = append(parts,
				candidate.Title,
				candidate.Confidence,
				candidate.CausalChain.Trigger,
				candidate.CausalChain.InvalidState,
				candidate.CausalChain.StateTransition,
				candidate.CausalChain.MissingGuard,
				candidate.CausalChain.UserVisibleSymptom,
			)
			parts = append(parts, candidate.CandidateChain...)
			parts = append(parts, candidate.TriggerValues...)
			parts = append(parts, candidate.ExpectedRange...)
			parts = append(parts, candidate.OutOfRangeCases...)
			parts = append(parts, candidate.ObservedFailurePath...)
			parts = append(parts, candidate.EvidenceFiles...)
			parts = append(parts, candidate.DisconfirmingEvidence...)
			parts = append(parts, candidate.CannotBeRootCauseIf...)
			parts = append(parts, candidate.RequiredRuntimeObservation...)
			parts = append(parts, candidate.VerificationSteps...)
			parts = append(parts, candidate.NeedsCrossShardEvidence...)
		}
	}
	return strings.Join(parts, "\n")
}

func buildFileContext(snapshot ProjectSnapshot, paths []string, limit int) []string {
	out := []string{}
	seen := map[string]struct{}{}
	for _, path := range paths {
		if len(out) >= limit {
			break
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		file, ok := snapshot.FilesByPath[path]
		if !ok {
			continue
		}
		abs := filepath.Join(snapshot.Root, filepath.FromSlash(path))
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		lines := splitLines(string(data))
		if len(lines) > 60 {
			lines = lines[:60]
		}
		imports := file.Imports
		if len(imports) == 0 {
			imports = file.RawImports
		}
		out = append(out, fmt.Sprintf("FILE %s\n- lines: %d\n- manifest: %t\n- entrypoint: %t\n- imports: %s\n```\n%s\n```", path, file.LineCount, file.IsManifest, file.IsEntrypoint, strings.Join(imports, ", "), strings.Join(lines, "\n")))
	}
	return out
}

func analysisPromptExcerpt(text string, limit int) string {
	text = strings.TrimSpace(text)
	if text == "" || limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "\n... (truncated)"
}

func fallbackWorkerReport(shard AnalysisShard, raw string) WorkerReport {
	return WorkerReport{
		ShardID:       shard.ID,
		Title:         shard.Name,
		ScopeSummary:  "Worker returned non-JSON output.",
		EvidenceFiles: append([]string(nil), shard.PrimaryFiles...),
		Unknowns:      []string{"Worker output was not valid JSON and was excluded from synthesis; raw output is preserved in the JSON artifact."},
		Raw:           strings.TrimSpace(raw),
	}
}

func parseWorkerReportPayload(raw string, shard AnalysisShard) (WorkerReport, bool) {
	type workerEnvelope struct {
		Report WorkerReport `json:"report"`
	}

	candidates := analysisJSONCandidates(raw)
	for _, candidate := range candidates {
		envelope := workerEnvelope{}
		if err := json.Unmarshal([]byte(candidate), &envelope); err == nil {
			if !workerReportPayloadHasContent(envelope.Report) {
				continue
			}
			envelope.Report.ShardID = shard.ID
			envelope.Report.Raw = strings.TrimSpace(raw)
			normalizeWorkerReport(&envelope.Report, shard)
			if workerReportLooksLikeSchemaPlaceholder(envelope.Report) {
				continue
			}
			return envelope.Report, true
		}

		report := WorkerReport{}
		if err := json.Unmarshal([]byte(candidate), &report); err == nil {
			if !workerReportPayloadHasContent(report) {
				continue
			}
			report.ShardID = shard.ID
			report.Raw = strings.TrimSpace(raw)
			normalizeWorkerReport(&report, shard)
			if workerReportLooksLikeSchemaPlaceholder(report) {
				continue
			}
			return report, true
		}
	}

	return WorkerReport{}, false
}

func workerReportPayloadHasContent(report WorkerReport) bool {
	if strings.TrimSpace(report.ScopeSummary) != "" ||
		strings.TrimSpace(report.Narrative) != "" {
		return true
	}
	return len(report.Responsibilities) > 0 ||
		len(report.Facts) > 0 ||
		len(report.Inferences) > 0 ||
		len(report.KeyFiles) > 0 ||
		len(report.EntryPoints) > 0 ||
		len(report.InternalFlow) > 0 ||
		len(report.Dependencies) > 0 ||
		len(report.Collaboration) > 0 ||
		len(report.Risks) > 0 ||
		len(report.Unknowns) > 0 ||
		len(report.EvidenceFiles) > 0 ||
		len(report.RootCauseCandidates) > 0
}

func workerReportLooksLikeSchemaPlaceholder(report WorkerReport) bool {
	placeholder := 0
	total := 0
	checkString := func(value string) {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			return
		}
		total++
		if value == "string" {
			placeholder++
		}
	}
	checkList := func(values []string) {
		for _, value := range values {
			checkString(value)
		}
	}
	checkString(report.Title)
	checkString(report.ScopeSummary)
	checkString(report.Narrative)
	checkList(report.Responsibilities)
	checkList(report.Facts)
	checkList(report.Inferences)
	checkList(report.KeyFiles)
	checkList(report.EntryPoints)
	checkList(report.InternalFlow)
	checkList(report.Dependencies)
	checkList(report.Collaboration)
	checkList(report.Risks)
	checkList(report.Unknowns)
	checkList(report.EvidenceFiles)
	for _, candidate := range report.RootCauseCandidates {
		checkString(candidate.Title)
		checkString(candidate.CausalChain.Trigger)
		checkString(candidate.CausalChain.InvalidState)
		checkString(candidate.CausalChain.StateTransition)
		checkString(candidate.CausalChain.MissingGuard)
		checkString(candidate.CausalChain.UserVisibleSymptom)
		checkList(candidate.CandidateChain)
		checkList(candidate.TriggerValues)
		checkList(candidate.ExpectedRange)
		checkList(candidate.OutOfRangeCases)
		checkList(candidate.ObservedFailurePath)
		checkList(candidate.EvidenceFiles)
		checkList(candidate.DisconfirmingEvidence)
		checkList(candidate.CannotBeRootCauseIf)
		checkList(candidate.RequiredRuntimeObservation)
		checkList(candidate.VerificationSteps)
		checkString(candidate.Confidence)
		checkList(candidate.NeedsCrossShardEvidence)
	}
	return total >= 6 && placeholder*2 >= total
}

func parseReviewDecisionPayload(raw string) (ReviewDecision, bool) {
	type reviewEnvelope struct {
		Decision ReviewDecision `json:"decision"`
	}

	candidates := analysisJSONCandidates(raw)
	for _, candidate := range candidates {
		envelope := reviewEnvelope{}
		if err := json.Unmarshal([]byte(candidate), &envelope); err == nil {
			envelope.Decision.Raw = strings.TrimSpace(raw)
			if strings.TrimSpace(envelope.Decision.Status) == "" {
				envelope.Decision.Status = "approved"
			}
			normalizeReviewDecision(&envelope.Decision)
			return envelope.Decision, true
		}

		decision := ReviewDecision{}
		if err := json.Unmarshal([]byte(candidate), &decision); err == nil {
			decision.Raw = strings.TrimSpace(raw)
			if strings.TrimSpace(decision.Status) == "" {
				decision.Status = "approved"
			}
			normalizeReviewDecision(&decision)
			return decision, true
		}
	}

	return ReviewDecision{}, false
}

func normalizeReviewDecision(decision *ReviewDecision) {
	status := strings.ToLower(strings.TrimSpace(decision.Status))
	if status == "needs_revision" && strings.TrimSpace(decision.FailureKind) == "" {
		decision.FailureKind = analysisReviewIssueQuality
	}
	decision.SymptomPossible = normalizeSymptomPossible(decision.SymptomPossible)
	decision.SymptomCausality = analysisUniqueStrings(decision.SymptomCausality)
	decision.SymptomReproductionBridge = analysisUniqueStrings(decision.SymptomReproductionBridge)
	decision.RequiredRuntimeObservation = analysisUniqueStrings(decision.RequiredRuntimeObservation)
	decision.DisqualifyingEvidence = analysisUniqueStrings(decision.DisqualifyingEvidence)
	decision.CausalChainStages = rootCauseNormalizeCausalStages(decision.CausalChainStages)
	decision.CausalChainMissing = rootCauseNormalizeCausalStages(decision.CausalChainMissing)
	decision.DisconfirmingEvidence = analysisUniqueStrings(decision.DisconfirmingEvidence)
	decision.RejectedCandidates = analysisUniqueStrings(decision.RejectedCandidates)
	decision.EvidenceRequests = normalizeRootCauseEvidenceRequests(decision.EvidenceRequests)
}

func normalizeSymptomPossible(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "yes", "true", "possible":
		if strings.TrimSpace(raw) == "" {
			return ""
		}
		return "yes"
	case "partial", "partially":
		return "partial"
	case "no", "false", "impossible":
		return "no"
	case "unknown", "unclear":
		return "unknown"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func analysisJSONCandidates(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	candidates := []string{}
	seen := map[string]struct{}{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		candidates = append(candidates, value)
	}

	add(trimmed)
	add(stripMarkdownCodeFence(trimmed))
	if block, ok := firstJSONObject(trimmed); ok {
		add(block)
	}
	stripped := stripMarkdownCodeFence(trimmed)
	if block, ok := firstJSONObject(stripped); ok {
		add(block)
	}

	return candidates
}

func stripMarkdownCodeFence(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}

	newline := strings.Index(trimmed, "\n")
	if newline < 0 {
		return trimmed
	}
	body := trimmed[newline+1:]
	if end := strings.LastIndex(body, "```"); end >= 0 {
		body = body[:end]
	}
	return strings.TrimSpace(body)
}

func firstJSONObject(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	start := strings.Index(trimmed, "{")
	if start < 0 {
		return "", false
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(trimmed); i++ {
		ch := trimmed[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		if ch == '{' {
			depth++
			continue
		}
		if ch == '}' {
			depth--
			if depth == 0 {
				return strings.TrimSpace(trimmed[start : i+1]), true
			}
		}
	}

	return "", false
}

func normalizeWorkerReport(report *WorkerReport, shard AnalysisShard) {
	if strings.TrimSpace(report.Title) == "" {
		report.Title = shard.Name
	}
	if strings.TrimSpace(report.ScopeSummary) == "" {
		report.ScopeSummary = shard.Name
	}
	report.Responsibilities = analysisUniqueStrings(report.Responsibilities)
	report.Facts = analysisUniqueStrings(report.Facts)
	report.Inferences = analysisUniqueStrings(report.Inferences)
	report.KeyFiles = analysisUniqueStrings(filterEvidence(report.KeyFiles, shard))
	report.EntryPoints = analysisUniqueStrings(report.EntryPoints)
	report.InternalFlow = analysisUniqueStrings(report.InternalFlow)
	report.Dependencies = analysisUniqueStrings(report.Dependencies)
	report.Collaboration = analysisUniqueStrings(report.Collaboration)
	report.Risks = analysisUniqueStrings(report.Risks)
	report.Unknowns = analysisUniqueStrings(report.Unknowns)
	report.EvidenceFiles = analysisUniqueStrings(filterEvidence(report.EvidenceFiles, shard))
	report.RootCauseCandidates = normalizeRootCauseCandidates(report.RootCauseCandidates, shard)
	if len(report.EvidenceFiles) == 0 {
		report.EvidenceFiles = append([]string(nil), shard.PrimaryFiles...)
	}
	if len(report.KeyFiles) == 0 {
		report.KeyFiles = append([]string(nil), report.EvidenceFiles...)
		if len(report.KeyFiles) > 8 {
			report.KeyFiles = report.KeyFiles[:8]
		}
	}
}

func normalizeRootCauseCandidates(candidates []RootCauseCandidate, shard AnalysisShard) []RootCauseCandidate {
	out := []RootCauseCandidate{}
	for _, candidate := range candidates {
		candidate.Title = strings.TrimSpace(candidate.Title)
		candidate.CandidateChain = analysisUniqueStrings(candidate.CandidateChain)
		candidate.TriggerValues = analysisUniqueStrings(candidate.TriggerValues)
		candidate.ExpectedRange = analysisUniqueStrings(candidate.ExpectedRange)
		candidate.OutOfRangeCases = analysisUniqueStrings(candidate.OutOfRangeCases)
		candidate.ObservedFailurePath = analysisUniqueStrings(candidate.ObservedFailurePath)
		candidate.EvidenceFiles = analysisUniqueStrings(filterEvidence(candidate.EvidenceFiles, shard))
		candidate.DisconfirmingEvidence = analysisUniqueStrings(candidate.DisconfirmingEvidence)
		candidate.CannotBeRootCauseIf = analysisUniqueStrings(candidate.CannotBeRootCauseIf)
		candidate.RequiredRuntimeObservation = analysisUniqueStrings(candidate.RequiredRuntimeObservation)
		candidate.VerificationSteps = analysisUniqueStrings(candidate.VerificationSteps)
		candidate.Confidence = strings.TrimSpace(candidate.Confidence)
		candidate.NeedsCrossShardEvidence = analysisUniqueStrings(candidate.NeedsCrossShardEvidence)
		candidate.PatternIDs = analysisUniqueStrings(candidate.PatternIDs)
		candidate.CausalChain = normalizeRootCauseCausalChain(candidate.CausalChain, candidate, shard)
		if candidate.Title == "" &&
			len(candidate.CandidateChain) == 0 &&
			len(candidate.TriggerValues) == 0 &&
			len(candidate.OutOfRangeCases) == 0 &&
			len(candidate.ObservedFailurePath) == 0 {
			continue
		}
		if len(candidate.EvidenceFiles) == 0 {
			candidate.EvidenceFiles = append([]string(nil), shard.PrimaryFiles...)
		}
		if candidate.Confidence == "" {
			candidate.Confidence = "medium"
		}
		candidate.Confidence = normalizeRootCauseConfidence(candidate.Confidence, rootCauseConfidenceScore(candidate, ReviewDecision{}))
		candidate.Probes = normalizeRootCauseProbes(candidate.Probes)
		if len(candidate.Probes) == 0 {
			candidate.Probes = deriveRootCauseCandidateProbes(candidate, shard)
		}
		candidate.ConfidenceBreakdown = normalizeRootCauseConfidenceBreakdown(candidate.ConfidenceBreakdown, rootCauseConfidenceScore(candidate, ReviewDecision{}))
		out = append(out, candidate)
	}
	return out
}

func heuristicReviewDecision(report WorkerReport, raw string) ReviewDecision {
	issues := []string{}
	if strings.TrimSpace(report.ScopeSummary) == "" {
		issues = append(issues, "Scope summary is missing.")
	}
	if len(report.Responsibilities) == 0 {
		issues = append(issues, "Responsibilities are missing.")
	}
	if len(report.KeyFiles) == 0 {
		issues = append(issues, "Key files are missing.")
	}
	if len(report.InternalFlow) == 0 {
		issues = append(issues, "Internal flow is missing.")
	}
	if len(report.EvidenceFiles) == 0 {
		issues = append(issues, "Evidence files are missing.")
	}
	decision := ReviewDecision{
		Status: "approved",
		Raw:    raw,
	}
	if len(issues) > 0 {
		decision.Status = "needs_revision"
		decision.Issues = issues
		decision.RevisionPrompt = "Revise the report with explicit responsibilities, internal flow, evidence files, and a concise scope summary grounded in the assigned files."
		decision.FailureKind = analysisReviewIssueQuality
	}
	return decision
}

func softFailReviewDecision(shard AnalysisShard, report WorkerReport, err error) ReviewDecision {
	issues := []string{
		fmt.Sprintf("Reviewer request failed: %v", err),
	}
	if isExternalDependencyShard(shard) {
		issues = append(issues, "This shard belongs to external dependencies, so the overall analysis continued with reduced review confidence for this shard.")
	} else {
		issues = append(issues, "The overall analysis continued using the worker report, but this shard should be treated as needing manual verification.")
	}
	return ReviewDecision{
		Status:         "review_failed",
		Issues:         issues,
		RevisionPrompt: "",
		FailureKind:    analysisReviewIssueProvider,
		Raw:            err.Error(),
	}
}

func softFailWorkerReport(shard AnalysisShard, err error) WorkerReport {
	title := strings.TrimSpace(shard.Name)
	if title == "" {
		title = strings.TrimSpace(shard.ID)
	}
	if title == "" {
		title = "analysis shard"
	}
	summary := "Worker provider request failed after retry, so this shard was preserved as a low-confidence placeholder instead of aborting the full analysis run."
	return WorkerReport{
		ShardID:      shard.ID,
		Title:        title,
		ScopeSummary: summary,
		Responsibilities: []string{
			"Provider request failed before this shard could be analyzed.",
		},
		Facts: []string{
			fmt.Sprintf("Worker request failed for shard %s.", title),
		},
		Inferences: []string{
			"The project-wide analysis can still continue, but this shard needs a later refresh.",
		},
		KeyFiles:      limitStrings(shard.PrimaryFiles, 12),
		EvidenceFiles: limitStrings(shard.PrimaryFiles, 12),
		EntryPoints:   []string{},
		InternalFlow: []string{
			"Not analyzed because the worker provider request failed.",
		},
		Dependencies:  []string{},
		Collaboration: []string{},
		Risks: []string{
			"Low confidence: worker provider failure prevented shard-level analysis.",
		},
		Unknowns: []string{
			"Rerun this analysis when provider rate limits recover.",
		},
		Narrative: summary,
		Raw:       err.Error(),
	}
}

func softFailWorkerReviewDecision(shard AnalysisShard, err error) ReviewDecision {
	issues := []string{
		fmt.Sprintf("Worker request failed: %v", err),
		"The overall analysis continued with a low-confidence placeholder for this shard.",
		"Rerun /analyze-project after provider rate limits recover to refresh this shard.",
	}
	if isExternalDependencyShard(shard) {
		issues = append(issues, "The failed shard is external-dependency scoped, so the main project map may still be partially useful.")
	}
	return ReviewDecision{
		Status:         "review_failed",
		Issues:         issues,
		RevisionPrompt: "",
		FailureKind:    analysisReviewIssueProvider,
		Raw:            err.Error(),
	}
}

func fallbackRootCauseDocument(snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, goal string) string {
	var b strings.Builder
	b.WriteString("# Root Cause Investigation\n\n")
	b.WriteString("## Reported Symptom\n\n")
	if strings.TrimSpace(snapshot.RootCause.Symptom.Symptom) != "" {
		fmt.Fprintf(&b, "- Symptom: %s\n", snapshot.RootCause.Symptom.Symptom)
	} else {
		fmt.Fprintf(&b, "- Goal: %s\n", goal)
	}
	if strings.TrimSpace(snapshot.RootCause.Symptom.ExpectedBehavior) != "" {
		fmt.Fprintf(&b, "- Expected behavior: %s\n", snapshot.RootCause.Symptom.ExpectedBehavior)
	}
	if strings.TrimSpace(snapshot.RootCause.Symptom.ObservedBehavior) != "" {
		fmt.Fprintf(&b, "- Observed behavior: %s\n", snapshot.RootCause.Symptom.ObservedBehavior)
	}
	if strings.TrimSpace(snapshot.RootCause.Symptom.Frequency) != "" {
		fmt.Fprintf(&b, "- Frequency: %s\n", snapshot.RootCause.Symptom.Frequency)
	}
	b.WriteString("\n")
	writeRootCauseJoinedSection(&b, "Root Causes", snapshot.RootCause.JoinedCandidates, "root_cause")
	writeRootCauseJoinedSection(&b, "Contributing Factors", snapshot.RootCause.JoinedCandidates, "contributing_factor")
	writeRootCauseJoinedSection(&b, "Detection Gaps", snapshot.RootCause.JoinedCandidates, "detection_gap")
	writeRootCauseJoinedSection(&b, "Operational Triggers", snapshot.RootCause.JoinedCandidates, "operational_trigger")
	if len(snapshot.RootCause.JoinedCandidates) == 0 {
		b.WriteString("## Root Causes\n\n")
		b.WriteString("- No reviewer-validated root-cause candidate survived symptom-causality review.\n\n")
	}
	b.WriteString("## Evidence Trail\n\n")
	wroteEvidence := false
	for index, report := range reports {
		if index >= len(shards) {
			continue
		}
		if len(report.RootCauseCandidates) == 0 && len(report.Facts) == 0 && len(report.Unknowns) == 0 {
			continue
		}
		wroteEvidence = true
		fmt.Fprintf(&b, "### %s (%s)\n\n", firstNonBlankAnalysisString(report.Title, shards[index].Name), shards[index].ID)
		writeSectionList(&b, "Facts", report.Facts)
		writeSectionList(&b, "Unknowns", report.Unknowns)
		writeRootCauseCandidates(&b, report.RootCauseCandidates)
	}
	if !wroteEvidence {
		b.WriteString("- No shard evidence was available.\n\n")
	}
	if len(snapshot.RootCause.Hypotheses) > 0 {
		b.WriteString("## Remaining Unknowns\n\n")
		for _, hypothesis := range snapshot.RootCause.Hypotheses {
			if len(hypothesis.MustDisprove) == 0 {
				continue
			}
			fmt.Fprintf(&b, "- %s: %s\n", firstNonBlankAnalysisString(hypothesis.ID, "H"), strings.Join(limitStrings(hypothesis.MustDisprove, 3), " | "))
		}
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func buildRootCauseAuditDigest(audit RootCauseAuditTrail) string {
	var b strings.Builder
	b.WriteString("# Root Cause Audit Trail\n\n")
	if strings.TrimSpace(audit.Symptom) != "" {
		fmt.Fprintf(&b, "- Symptom: %s\n", audit.Symptom)
	}
	if !audit.GeneratedAt.IsZero() {
		fmt.Fprintf(&b, "- Generated at: %s\n", audit.GeneratedAt.Format(time.RFC3339))
	}
	if len(audit.CodeMatches) > 0 {
		b.WriteString("\n## Code Matches\n\n")
		for _, match := range limitRootCauseCodeMatches(audit.CodeMatches, 16) {
			fmt.Fprintf(&b, "- %s score=%d signals=%s\n", match.File, match.Score, strings.Join(limitStrings(match.MatchedSignals, 6), ", "))
		}
	}
	if len(audit.EvidenceRequests) > 0 {
		b.WriteString("\n## Evidence Requests\n\n")
		for _, request := range limitRootCauseEvidenceRequests(audit.EvidenceRequests, 16) {
			fmt.Fprintf(&b, "- %s [%s] %s\n", request.ID, request.Status, firstNonBlankRootCauseString(request.Request, request.RequiredToProve, strings.Join(request.TargetSignals, ", ")))
			if len(request.RoutedShardIDs) > 0 {
				fmt.Fprintf(&b, "  - routed: %s\n", strings.Join(limitStrings(request.RoutedShardIDs, 5), ", "))
			}
			if len(request.FulfilledByShards) > 0 {
				fmt.Fprintf(&b, "  - fulfilled_by: %s\n", strings.Join(limitStrings(request.FulfilledByShards, 5), ", "))
			}
			if len(request.SatisfiedEvidenceFiles) > 0 {
				fmt.Fprintf(&b, "  - evidence: %s\n", strings.Join(limitStrings(request.SatisfiedEvidenceFiles, 5), ", "))
			}
		}
	}
	if len(audit.PatternMatches) > 0 {
		b.WriteString("\n## Known Pattern Priors\n\n")
		for _, match := range limitRootCausePatternMatches(audit.PatternMatches, 16) {
			fmt.Fprintf(&b, "- %s score=%d confidence=%s types=%s\n", match.PatternID, match.Score, match.Confidence, strings.Join(match.ProjectTypes, ", "))
			if strings.TrimSpace(match.Title) != "" {
				fmt.Fprintf(&b, "  - title: %s\n", match.Title)
			}
			if len(match.MatchedSignals) > 0 {
				fmt.Fprintf(&b, "  - signals: %s\n", strings.Join(limitStrings(match.MatchedSignals, 5), ", "))
			}
			if len(match.MatchedFiles) > 0 {
				fmt.Fprintf(&b, "  - files: %s\n", strings.Join(limitStrings(match.MatchedFiles, 5), ", "))
			}
		}
	}
	if len(audit.CandidateDecisions) > 0 {
		b.WriteString("\n## Candidate Decisions\n\n")
		for _, item := range audit.CandidateDecisions {
			fmt.Fprintf(&b, "- %s [%s] shard=%s review=%s symptom=%s deep=%s\n", firstNonBlankAnalysisString(item.CandidateTitle, "candidate"), item.Decision, item.ShardID, item.ReviewStatus, item.SymptomPossible, item.DeepVerificationStatus)
			if strings.TrimSpace(item.Reason) != "" {
				fmt.Fprintf(&b, "  - reason: %s\n", item.Reason)
			}
			if len(item.CausalChainStages) > 0 {
				fmt.Fprintf(&b, "  - stages: %s\n", strings.Join(item.CausalChainStages, ", "))
			}
			if len(item.CausalChainMissing) > 0 {
				fmt.Fprintf(&b, "  - missing: %s\n", strings.Join(item.CausalChainMissing, ", "))
			}
			if len(item.PatternIDs) > 0 {
				fmt.Fprintf(&b, "  - patterns: %s\n", strings.Join(limitStrings(item.PatternIDs, 5), ", "))
			}
			if len(item.ExplicitPatternIDs) > 0 {
				fmt.Fprintf(&b, "  - explicit_patterns: %s\n", strings.Join(limitStrings(item.ExplicitPatternIDs, 5), ", "))
			}
			if len(item.InferredRelatedPatternIDs) > 0 {
				fmt.Fprintf(&b, "  - inferred_related_patterns: %s\n", strings.Join(limitStrings(item.InferredRelatedPatternIDs, 5), ", "))
			}
		}
	}
	if len(audit.DeepVerifications) > 0 {
		b.WriteString("\n## Deep Verifications\n\n")
		for _, item := range audit.DeepVerifications {
			fmt.Fprintf(&b, "- %s [%s, confidence=%s]: %s\n", item.CandidateTitle, item.Status, item.Confidence, item.Summary)
			if len(item.InstrumentationSteps) > 0 {
				fmt.Fprintf(&b, "  - instrumentation: %s\n", strings.Join(limitStrings(item.InstrumentationSteps, 4), " | "))
			}
			if len(item.Probes) > 0 {
				fmt.Fprintf(&b, "  - probes: %s\n", renderRootCauseProbeList(item.Probes, 3))
			}
			if len(item.PatternIDs) > 0 {
				fmt.Fprintf(&b, "  - patterns: %s\n", strings.Join(limitStrings(item.PatternIDs, 5), ", "))
			}
		}
	}
	if len(audit.JoinedCandidates) > 0 {
		b.WriteString("\n## Joined Candidates\n\n")
		for _, item := range limitRootCauseJoinedCandidates(audit.JoinedCandidates, 12) {
			fmt.Fprintf(&b, "- %s [%s, %s, %d/100, cluster=%s]\n", item.Title, item.Classification, item.Confidence, item.ConfidenceScore, item.ClusterID)
			if len(item.ConfidenceBreakdown.Reasons) > 0 {
				fmt.Fprintf(&b, "  - confidence: %s\n", strings.Join(limitStrings(item.ConfidenceBreakdown.Reasons, 3), " | "))
			}
			if len(item.Probes) > 0 {
				fmt.Fprintf(&b, "  - probes: %s\n", renderRootCauseProbeList(item.Probes, 3))
			}
		}
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func writeRootCauseJoinedSection(b *strings.Builder, title string, candidates []RootCauseJoinedCandidate, classification string) {
	filtered := []RootCauseJoinedCandidate{}
	for _, candidate := range candidates {
		if normalizeRootCauseClassification(candidate.Classification) == classification {
			filtered = append(filtered, candidate)
		}
	}
	if len(filtered) == 0 {
		return
	}
	fmt.Fprintf(b, "## %s\n\n", title)
	for _, candidate := range filtered {
		fmt.Fprintf(b, "### %s\n\n", firstNonBlankAnalysisString(candidate.Title, "Candidate"))
		fmt.Fprintf(b, "- Confidence: %s (%d/100)\n", candidate.Confidence, candidate.ConfidenceScore)
		if strings.TrimSpace(candidate.ClusterID) != "" {
			fmt.Fprintf(b, "- Cluster: %s\n", candidate.ClusterID)
		}
		if len(candidate.ConfidenceBreakdown.Reasons) > 0 {
			fmt.Fprintf(b, "- Confidence reasons: %s\n", strings.Join(limitStrings(candidate.ConfidenceBreakdown.Reasons, 3), " | "))
		}
		writeIndentedSectionList(b, "Pattern ids", candidate.PatternIDs)
		writeIndentedSectionList(b, "Joined chain", candidate.JoinedChain)
		writeIndentedSectionList(b, "Evidence files", candidate.EvidenceFiles)
		writeIndentedSectionList(b, "Cannot be root cause if", candidate.CannotBeRootCauseIf)
		writeIndentedSectionList(b, "Required runtime observation", candidate.RequiredRuntimeObservation)
		writeIndentedSectionList(b, "Verification steps", candidate.VerificationSteps)
		writeRootCauseProbeSection(b, candidate.Probes)
		writeIndentedSectionList(b, "Competes with", candidate.CompetesWith)
		writeIndentedSectionList(b, "Depends on", candidate.DependsOn)
		writeIndentedSectionList(b, "Can coexist with", candidate.CanCoexistWith)
		writeIndentedSectionList(b, "Disconfirming evidence", candidate.DisconfirmingEvidence)
		b.WriteString("\n")
	}
}

func writeRootCauseProbeSection(b *strings.Builder, probes []RootCauseProbe) {
	if len(probes) == 0 {
		return
	}
	b.WriteString("- Probes:\n")
	for _, probe := range limitRootCauseProbes(probes, 5) {
		fmt.Fprintf(b, "  - %s %s", probe.Kind, firstNonBlankRootCauseString(probe.Title, probe.Target))
		if strings.TrimSpace(probe.Target) != "" {
			fmt.Fprintf(b, " target=%s", probe.Target)
		}
		if strings.TrimSpace(probe.ExpectedSignal) != "" {
			fmt.Fprintf(b, " expected=%s", probe.ExpectedSignal)
		}
		if strings.TrimSpace(probe.DisprovesWhen) != "" {
			fmt.Fprintf(b, " disproves_when=%s", probe.DisprovesWhen)
		}
		b.WriteString("\n")
	}
}

func fallbackFinalDocument(snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, goal string) string {
	if normalizeProjectAnalysisMode(snapshot.AnalysisMode) == "root-cause" {
		return fallbackRootCauseDocument(snapshot, shards, reports, goal)
	}
	type subsystemSection struct {
		Title               string
		Group               string
		Summary             string
		ShardNames          []string
		CacheStatuses       []string
		InvalidationReasons []string
		InvalidationDiff    []string
		Responsibilities    []string
		Facts               []string
		Inferences          []string
		KeyFiles            []string
		EvidenceFiles       []string
		EntryPoints         []string
		InternalFlow        []string
		Dependencies        []string
		Collaboration       []string
		Risks               []string
		Unknowns            []string
		RootCauseCandidates []RootCauseCandidate
	}

	allEntryPoints := []string{}
	allFlow := []string{}
	allDeps := []string{}
	allRisks := []string{}
	allUnknowns := []string{}
	grouped := groupedReportsForSynthesis(shards, reports)
	sections := make([]subsystemSection, 0, len(grouped))
	for _, report := range grouped {
		sections = append(sections, subsystemSection{
			Title:               report.Title,
			Group:               report.Group,
			Summary:             "",
			ShardNames:          report.ShardNames,
			CacheStatuses:       report.CacheStatuses,
			InvalidationReasons: report.InvalidationReasons,
			InvalidationDiff:    report.InvalidationDiff,
			Responsibilities:    report.Responsibilities,
			Facts:               report.Facts,
			Inferences:          report.Inferences,
			KeyFiles:            report.KeyFiles,
			EvidenceFiles:       report.EvidenceFiles,
			EntryPoints:         report.EntryPoints,
			InternalFlow:        report.InternalFlow,
			Dependencies:        report.Dependencies,
			Collaboration:       report.Collaboration,
			Risks:               report.Risks,
			Unknowns:            report.Unknowns,
			RootCauseCandidates: report.RootCauseCandidates,
		})
		allEntryPoints = append(allEntryPoints, report.EntryPoints...)
		allFlow = append(allFlow, report.InternalFlow...)
		allDeps = append(allDeps, report.Dependencies...)
		allRisks = append(allRisks, report.Risks...)
		allUnknowns = append(allUnknowns, report.Unknowns...)
	}
	allEntryPoints = analysisUniqueStrings(allEntryPoints)
	allFlow = analysisUniqueStrings(allFlow)
	allDeps = analysisUniqueStrings(allDeps)
	allRisks = analysisUniqueStrings(allRisks)
	allUnknowns = analysisUniqueStrings(allUnknowns)

	var b strings.Builder
	fmt.Fprintf(&b, "# Project Analysis\n\n")
	fmt.Fprintf(&b, "## Project Overview\n\n")
	fmt.Fprintf(&b, "- Goal: %s\n", goal)
	if strings.TrimSpace(snapshot.AnalysisMode) != "" {
		fmt.Fprintf(&b, "- Mode: `%s`\n", snapshot.AnalysisMode)
	}
	fmt.Fprintf(&b, "- Workspace: `%s`\n", snapshot.Root)
	if len(snapshot.AnalysisLenses) > 0 {
		lensNames := []string{}
		for _, lens := range snapshot.AnalysisLenses {
			lensNames = append(lensNames, lens.Type)
		}
		fmt.Fprintf(&b, "- Analysis lenses: `%s`\n", strings.Join(lensNames, ", "))
	}
	if snapshot.ModulePath != "" {
		fmt.Fprintf(&b, "- Go module: `%s`\n", snapshot.ModulePath)
	}
	if len(snapshot.UnrealProjects) > 0 || len(snapshot.UnrealModules) > 0 || len(snapshot.UnrealTargets) > 0 {
		fmt.Fprintf(&b, "- Unreal profile: `%d project(s), %d plugin(s), %d target(s), %d module(s), %d reflected type(s), %d network surface(s), %d asset/config binding(s), %d gameplay system(s), %d startup setting source(s)`\n", len(snapshot.UnrealProjects), len(snapshot.UnrealPlugins), len(snapshot.UnrealTargets), len(snapshot.UnrealModules), len(snapshot.UnrealTypes), len(snapshot.UnrealNetwork), len(snapshot.UnrealAssets), len(snapshot.UnrealSystems), len(snapshot.UnrealSettings))
		if strings.TrimSpace(snapshot.PrimaryUnrealModule) != "" {
			fmt.Fprintf(&b, "- Primary Unreal module: `%s`\n", snapshot.PrimaryUnrealModule)
		}
	}
	if isVisualStudioCppProject(snapshot) {
		fmt.Fprintf(&b, "- Solution profile: `Visual Studio / C++ multi-project`\n")
	}
	if strings.TrimSpace(snapshot.PrimaryStartup) != "" {
		fmt.Fprintf(&b, "- Solution startup candidate: `%s`\n", snapshot.PrimaryStartup)
		if driverEntries := driverEntrypointFiles(ProjectAnalysisRun{Snapshot: snapshot}); len(driverEntries) > 0 {
			fmt.Fprintf(&b, "- Driver/runtime entry files: `%s`\n", strings.Join(limitStrings(driverEntries, 5), "`, `"))
		}
	}
	highRuntimeEdges := highConfidenceRuntimeEdges(snapshot.RuntimeEdges)
	operationalChain := buildOperationalChain(snapshot, reports)
	if len(highRuntimeEdges) > 0 {
		fmt.Fprintf(&b, "- Runtime graph edges: %d\n", len(highRuntimeEdges))
	}
	if len(snapshot.ProjectEdges) > 0 {
		fmt.Fprintf(&b, "- Typed project edges: %d\n", len(snapshot.ProjectEdges))
	}
	if len(operationalChain) > 0 {
		fmt.Fprintf(&b, "- Operational chain edges: %d\n", len(operationalChain))
	}
	fmt.Fprintf(&b, "- Files analyzed: %d\n", snapshot.TotalFiles)
	fmt.Fprintf(&b, "- Total lines: %d\n\n", snapshot.TotalLines)
	if len(snapshot.Directories) > 0 || len(snapshot.ManifestFiles) > 0 {
		fmt.Fprintf(&b, "## Directory And Module Map\n\n")
		if len(snapshot.ManifestFiles) > 0 {
			for _, item := range snapshot.ManifestFiles {
				fmt.Fprintf(&b, "- Manifest: `%s`\n", item)
			}
		}
		if len(snapshot.SolutionProjects) > 0 {
			for _, project := range snapshot.SolutionProjects {
				entryCount := len(project.EntryFiles)
				startup := ""
				if project.StartupCandidate {
					startup = ", startup candidate"
				}
				outputType := strings.TrimSpace(project.OutputType)
				if outputType == "" {
					outputType = project.Kind
				}
				fmt.Fprintf(&b, "- Solution project: `%s` (%s, %d entry file(s)%s)\n", project.Name, outputType, entryCount, startup)
			}
		}
		if len(snapshot.Directories) > 0 {
			limit := len(snapshot.Directories)
			if limit > 20 {
				limit = 20
			}
			for _, item := range snapshot.Directories[:limit] {
				fmt.Fprintf(&b, "- Directory: `%s`\n", item)
			}
			if len(snapshot.Directories) > limit {
				fmt.Fprintf(&b, "- Additional directories omitted: %d\n", len(snapshot.Directories)-limit)
			}
		}
		b.WriteString("\n")
	}
	execution := buildAnalysisExecutionSummary(shards)
	if execution.TotalShards > 0 {
		fmt.Fprintf(&b, "## Analysis Execution\n\n")
		writeAnalysisExecutionSummary(&b, execution)
		b.WriteString("\n")
	}
	if len(snapshot.UnrealProjects) > 0 || len(snapshot.UnrealModules) > 0 || len(snapshot.UnrealTargets) > 0 {
		fmt.Fprintf(&b, "## Unreal Module And Target Map\n\n")
		for _, project := range snapshot.UnrealProjects {
			fmt.Fprintf(&b, "- Unreal project: `%s` from `%s`\n", project.Name, project.Path)
			if len(project.Modules) > 0 {
				fmt.Fprintf(&b, "  Modules: `%s`\n", strings.Join(limitStrings(project.Modules, 8), "`, `"))
			}
			if len(project.Plugins) > 0 {
				fmt.Fprintf(&b, "  Enabled plugins: `%s`\n", strings.Join(limitStrings(project.Plugins, 8), "`, `"))
			}
		}
		for _, target := range snapshot.UnrealTargets {
			fmt.Fprintf(&b, "- Unreal target: `%s` (`%s`) from `%s`\n", target.Name, target.TargetType, target.Path)
			if len(target.Modules) > 0 {
				fmt.Fprintf(&b, "  Modules: `%s`\n", strings.Join(limitStrings(target.Modules, 8), "`, `"))
			}
		}
		for _, module := range limitUnrealModules(snapshot.UnrealModules, 16) {
			fmt.Fprintf(&b, "- Unreal module: `%s` (`%s`)\n", module.Name, module.Kind)
			if strings.TrimSpace(module.Plugin) != "" {
				fmt.Fprintf(&b, "  Plugin: `%s`\n", module.Plugin)
			}
			if len(module.PublicDependencies) > 0 {
				fmt.Fprintf(&b, "  Public deps: `%s`\n", strings.Join(limitStrings(module.PublicDependencies, 8), "`, `"))
			}
			if len(module.PrivateDependencies) > 0 {
				fmt.Fprintf(&b, "  Private deps: `%s`\n", strings.Join(limitStrings(module.PrivateDependencies, 8), "`, `"))
			}
			if len(module.DynamicallyLoaded) > 0 {
				fmt.Fprintf(&b, "  Dynamic deps: `%s`\n", strings.Join(limitStrings(module.DynamicallyLoaded, 8), "`, `"))
			}
		}
		b.WriteString("\n")
	}
	if len(snapshot.UnrealTypes) > 0 {
		fmt.Fprintf(&b, "## Unreal Reflection And Gameplay Map\n\n")
		for _, item := range limitUnrealTypes(snapshot.UnrealTypes, 20) {
			fmt.Fprintf(&b, "- Reflected type: `%s` (`%s`)\n", item.Name, item.Kind)
			if strings.TrimSpace(item.GameplayRole) != "" {
				fmt.Fprintf(&b, "  Gameplay role: `%s`\n", item.GameplayRole)
			}
			if strings.TrimSpace(item.BaseClass) != "" {
				fmt.Fprintf(&b, "  Base class: `%s`\n", item.BaseClass)
			}
			if len(item.Functions) > 0 {
				fmt.Fprintf(&b, "  Reflected functions: `%s`\n", strings.Join(limitStrings(item.Functions, 6), "`, `"))
			}
			if len(item.Properties) > 0 {
				fmt.Fprintf(&b, "  Reflected properties: `%s`\n", strings.Join(limitStrings(item.Properties, 6), "`, `"))
			}
			if len(item.BlueprintCallableFunctions) > 0 {
				fmt.Fprintf(&b, "  Blueprint callable: `%s`\n", strings.Join(limitStrings(item.BlueprintCallableFunctions, 6), "`, `"))
			}
			if len(item.BlueprintEventFunctions) > 0 {
				fmt.Fprintf(&b, "  Blueprint events: `%s`\n", strings.Join(limitStrings(item.BlueprintEventFunctions, 6), "`, `"))
			}
			if item.GameModeClass != "" || item.GameStateClass != "" || item.PlayerControllerClass != "" || item.PlayerStateClass != "" || item.DefaultPawnClass != "" || item.HUDClass != "" {
				assignments := []string{}
				if item.GameModeClass != "" {
					assignments = append(assignments, "GameMode="+item.GameModeClass)
				}
				if item.GameStateClass != "" {
					assignments = append(assignments, "GameState="+item.GameStateClass)
				}
				if item.PlayerControllerClass != "" {
					assignments = append(assignments, "PlayerController="+item.PlayerControllerClass)
				}
				if item.PlayerStateClass != "" {
					assignments = append(assignments, "PlayerState="+item.PlayerStateClass)
				}
				if item.DefaultPawnClass != "" {
					assignments = append(assignments, "DefaultPawn="+item.DefaultPawnClass)
				}
				if item.HUDClass != "" {
					assignments = append(assignments, "HUD="+item.HUDClass)
				}
				fmt.Fprintf(&b, "  Framework assignments: `%s`\n", strings.Join(assignments, "`, `"))
			}
		}
		b.WriteString("\n")
	}
	if flow := buildUnrealGameplayFlowLines(snapshot.UnrealTypes, snapshot.UnrealSettings); len(flow) > 0 {
		fmt.Fprintf(&b, "## Unreal Gameplay Flow Map\n\n")
		for _, line := range flow {
			fmt.Fprintf(&b, "- %s\n", line)
		}
		b.WriteString("\n")
	}
	if flow := buildUnrealGameplaySystemFlowLines(snapshot.UnrealSystems); len(flow) > 0 {
		fmt.Fprintf(&b, "## Unreal Gameplay System Flow Map\n\n")
		for _, line := range flow {
			fmt.Fprintf(&b, "- %s\n", line)
		}
		b.WriteString("\n")
	}
	if len(snapshot.UnrealNetwork) > 0 {
		fmt.Fprintf(&b, "## Unreal Networking And Replication Map\n\n")
		for _, item := range limitUnrealNetwork(snapshot.UnrealNetwork, 20) {
			fmt.Fprintf(&b, "- Network surface: `%s`\n", firstNonBlankAnalysisString(item.TypeName, item.File))
			fmt.Fprintf(&b, "  File: `%s`\n", item.File)
			if len(item.ServerRPCs) > 0 {
				fmt.Fprintf(&b, "  Server RPCs: `%s`\n", strings.Join(limitStrings(item.ServerRPCs, 6), "`, `"))
			}
			if len(item.ClientRPCs) > 0 {
				fmt.Fprintf(&b, "  Client RPCs: `%s`\n", strings.Join(limitStrings(item.ClientRPCs, 6), "`, `"))
			}
			if len(item.MulticastRPCs) > 0 {
				fmt.Fprintf(&b, "  Multicast RPCs: `%s`\n", strings.Join(limitStrings(item.MulticastRPCs, 6), "`, `"))
			}
			if len(item.ReplicatedProperties) > 0 {
				fmt.Fprintf(&b, "  Replicated properties: `%s`\n", strings.Join(limitStrings(item.ReplicatedProperties, 6), "`, `"))
			}
			if len(item.RepNotifyProperties) > 0 {
				fmt.Fprintf(&b, "  RepNotify properties: `%s`\n", strings.Join(limitStrings(item.RepNotifyProperties, 6), "`, `"))
			}
			if item.HasReplicationList {
				fmt.Fprintf(&b, "  Registers lifetime replication props.\n")
			}
		}
		b.WriteString("\n")
	}
	if len(snapshot.UnrealAssets) > 0 {
		fmt.Fprintf(&b, "## Unreal Content And Config Coupling\n\n")
		for _, item := range limitUnrealAssets(snapshot.UnrealAssets, 20) {
			fmt.Fprintf(&b, "- Asset binding: `%s`\n", firstNonBlankAnalysisString(item.OwnerName, item.File))
			fmt.Fprintf(&b, "  File: `%s`\n", item.File)
			if len(item.AssetPaths) > 0 {
				fmt.Fprintf(&b, "  Asset paths: `%s`\n", strings.Join(limitStrings(item.AssetPaths, 6), "`, `"))
			}
			if len(item.CanonicalTargets) > 0 {
				fmt.Fprintf(&b, "  Canonical targets: `%s`\n", strings.Join(limitStrings(item.CanonicalTargets, 6), "`, `"))
			}
			if len(item.ConfigKeys) > 0 {
				fmt.Fprintf(&b, "  Config keys: `%s`\n", strings.Join(limitStrings(item.ConfigKeys, 6), "`, `"))
			}
			if len(item.LoadMethods) > 0 {
				fmt.Fprintf(&b, "  Load methods: `%s`\n", strings.Join(limitStrings(item.LoadMethods, 6), "`, `"))
			}
		}
		b.WriteString("\n")
	}
	if len(snapshot.UnrealSystems) > 0 {
		fmt.Fprintf(&b, "## Unreal Gameplay Systems\n\n")
		for _, item := range limitUnrealSystems(snapshot.UnrealSystems, 20) {
			fmt.Fprintf(&b, "- System: `%s`\n", item.System)
			fmt.Fprintf(&b, "  Owner: `%s`\n", firstNonBlankAnalysisString(item.OwnerName, item.File))
			fmt.Fprintf(&b, "  File: `%s`\n", item.File)
			if strings.TrimSpace(item.Module) != "" {
				fmt.Fprintf(&b, "  Module: `%s`\n", item.Module)
			}
			if len(item.Signals) > 0 {
				fmt.Fprintf(&b, "  Signals: `%s`\n", strings.Join(limitStrings(item.Signals, 6), "`, `"))
			}
			if len(item.Functions) > 0 {
				fmt.Fprintf(&b, "  Functions: `%s`\n", strings.Join(limitStrings(item.Functions, 6), "`, `"))
			}
			if len(item.OwnedBy) > 0 {
				fmt.Fprintf(&b, "  Owned by: `%s`\n", strings.Join(limitStrings(item.OwnedBy, 6), "`, `"))
			}
			if len(item.Targets) > 0 {
				fmt.Fprintf(&b, "  Targets: `%s`\n", strings.Join(limitStrings(item.Targets, 6), "`, `"))
			}
			if len(item.Actions) > 0 {
				fmt.Fprintf(&b, "  Input actions: `%s`\n", strings.Join(limitStrings(item.Actions, 6), "`, `"))
			}
			if len(item.Contexts) > 0 {
				fmt.Fprintf(&b, "  Mapping contexts: `%s`\n", strings.Join(limitStrings(item.Contexts, 6), "`, `"))
			}
			if len(item.Widgets) > 0 {
				fmt.Fprintf(&b, "  Widgets: `%s`\n", strings.Join(limitStrings(item.Widgets, 6), "`, `"))
			}
			if len(item.Abilities) > 0 {
				fmt.Fprintf(&b, "  Abilities: `%s`\n", strings.Join(limitStrings(item.Abilities, 6), "`, `"))
			}
			if len(item.Effects) > 0 {
				fmt.Fprintf(&b, "  Effects: `%s`\n", strings.Join(limitStrings(item.Effects, 6), "`, `"))
			}
			if len(item.Attributes) > 0 {
				fmt.Fprintf(&b, "  Attribute sets: `%s`\n", strings.Join(limitStrings(item.Attributes, 6), "`, `"))
			}
			if len(item.Assets) > 0 {
				fmt.Fprintf(&b, "  Assets: `%s`\n", strings.Join(limitStrings(item.Assets, 6), "`, `"))
			}
		}
		b.WriteString("\n")
	}
	if len(snapshot.UnrealSettings) > 0 {
		fmt.Fprintf(&b, "## Unreal Startup And Map Settings\n\n")
		for _, item := range snapshot.UnrealSettings {
			fmt.Fprintf(&b, "- Settings source: `%s`\n", item.SourceFile)
			if item.GameDefaultMap != "" {
				fmt.Fprintf(&b, "  Game default map: `%s`\n", item.GameDefaultMap)
			}
			if item.EditorStartupMap != "" {
				fmt.Fprintf(&b, "  Editor startup map: `%s`\n", item.EditorStartupMap)
			}
			if item.GlobalDefaultGameMode != "" {
				fmt.Fprintf(&b, "  Global default game mode: `%s`\n", item.GlobalDefaultGameMode)
			}
			if item.GameInstanceClass != "" {
				fmt.Fprintf(&b, "  Game instance class: `%s`\n", item.GameInstanceClass)
			}
			if item.DefaultPawnClass != "" {
				fmt.Fprintf(&b, "  Default pawn class: `%s`\n", item.DefaultPawnClass)
			}
			if item.PlayerControllerClass != "" {
				fmt.Fprintf(&b, "  Player controller class: `%s`\n", item.PlayerControllerClass)
			}
			if item.HUDClass != "" {
				fmt.Fprintf(&b, "  HUD class: `%s`\n", item.HUDClass)
			}
		}
		b.WriteString("\n")
	}
	if len(shards) > 0 {
		fmt.Fprintf(&b, "## Shard Index\n\n")
		for index, shard := range shards {
			title := shard.Name
			if index < len(reports) && strings.TrimSpace(reports[index].Title) != "" {
				title = reports[index].Title
			}
			fmt.Fprintf(&b, "- %s (%s): %d files, %d reference files\n", shard.ID, title, len(shard.PrimaryFiles), len(shard.ReferenceFiles))
		}
		b.WriteString("\n")
	}
	if isVisualStudioCppProject(snapshot) {
		fmt.Fprintf(&b, "## Solution Execution Lenses\n\n")
		fmt.Fprintf(&b, "- Bootstrap: service entry, DLL loading, updater handoff, and startup indirection\n")
		fmt.Fprintf(&b, "- Orchestration: master coordination, policy decisions, and subsystem scheduling\n")
		fmt.Fprintf(&b, "- Worker And Scanner Execution: deep forensic scans, collectors, and disassembly flows\n")
		fmt.Fprintf(&b, "- Monitoring: runtime telemetry, ETW, process, registry, and file observation\n")
		fmt.Fprintf(&b, "- Shared Common Services: compression, storage, cloud upload, and common helpers\n")
		fmt.Fprintf(&b, "- Protection And Hardening: obfuscation, VMProtect, and anti-tamper layers\n")
		fmt.Fprintf(&b, "- Dependency Catalog: AWS SDK, disassembly libraries, compression, JSON, and test frameworks\n\n")
	}
	if len(highRuntimeEdges) > 0 {
		fmt.Fprintf(&b, "## Runtime Graph\n\n")
		for _, edge := range highRuntimeEdges {
			fmt.Fprintf(&b, "- %s -> %s (%s, confidence=%s)\n", edge.Source, edge.Target, edge.Kind, edge.Confidence)
			for _, evidence := range limitStrings(edge.Evidence, 2) {
				fmt.Fprintf(&b, "  evidence: %s\n", evidence)
			}
		}
		b.WriteString("\n")
	}
	if len(snapshot.ProjectEdges) > 0 {
		fmt.Fprintf(&b, "## Typed Edge Summary\n\n")
		for _, edge := range limitProjectEdges(snapshot.ProjectEdges, 20) {
			fmt.Fprintf(&b, "- %s -> %s [%s, confidence=%s]\n", edge.Source, edge.Target, edge.Type, edge.Confidence)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## Execution Flow And Entry Points\n\n")
	if len(highRuntimeEdges) > 0 {
		fmt.Fprintf(&b, "Primary Startup Chain:\n")
		for _, edge := range limitRuntimeEdges(runtimeEdgesForStartup(snapshot.RuntimeEdges, snapshot.PrimaryStartup), 5) {
			fmt.Fprintf(&b, "- %s -> %s (%s)\n", edge.Source, edge.Target, edge.Kind)
		}
		b.WriteString("\n")
	}
	if len(operationalChain) > 0 {
		fmt.Fprintf(&b, "Operational Security Chain:\n")
		for _, edge := range limitRuntimeEdges(operationalChain, 6) {
			fmt.Fprintf(&b, "- %s -> %s (%s, confidence=%s)\n", edge.Source, edge.Target, edge.Kind, edge.Confidence)
		}
		b.WriteString("\n")
	}
	for _, item := range allEntryPoints {
		fmt.Fprintf(&b, "- Entry point: %s\n", item)
	}
	for _, item := range allFlow {
		fmt.Fprintf(&b, "- Flow: %s\n", item)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "## Subsystem Breakdown\n\n")
	currentGroup := ""
	for _, section := range sections {
		if strings.TrimSpace(section.Group) != "" && section.Group != currentGroup {
			currentGroup = section.Group
			fmt.Fprintf(&b, "### %s\n\n", currentGroup)
		}
		fmt.Fprintf(&b, "#### %s\n\n", section.Title)
		if strings.TrimSpace(section.Summary) != "" {
			fmt.Fprintf(&b, "%s\n\n", section.Summary)
		}
		if len(section.CacheStatuses) > 0 {
			fmt.Fprintf(&b, "Cache status: %s\n\n", strings.Join(limitStrings(section.CacheStatuses, 4), ", "))
		}
		if len(section.InvalidationReasons) > 0 {
			fmt.Fprintf(&b, "Invalidation reasons: %s\n\n", strings.Join(describeAnalysisInvalidationReasonsWithContext(section.InvalidationReasons, section.ShardNames, 6), " | "))
		}
		if evidence := buildInvalidationEvidenceLines(snapshot, section.ShardNames, firstNonEmpty(section.EvidenceFiles, section.KeyFiles), section.InvalidationReasons, 5); len(evidence) > 0 {
			fmt.Fprintf(&b, "Invalidation evidence: %s\n\n", strings.Join(evidence, " | "))
		}
		if len(section.InvalidationDiff) > 0 {
			fmt.Fprintf(&b, "Invalidation diff: %s\n\n", strings.Join(limitStrings(section.InvalidationDiff, 5), " | "))
		}
		if len(section.EntryPoints) > 0 {
			b.WriteString("Entry points:\n")
			for _, item := range section.EntryPoints {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n")
		}
		b.WriteString("Responsibilities:\n")
		for _, item := range section.Responsibilities {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
		if len(section.Facts) > 0 {
			b.WriteString("Facts:\n")
			for _, item := range section.Facts {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n")
		}
		if len(section.Inferences) > 0 {
			b.WriteString("Inferences:\n")
			for _, item := range section.Inferences {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n")
		}
		if len(section.KeyFiles) > 0 {
			b.WriteString("Key files:\n")
			for _, item := range section.KeyFiles {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n")
		}
		if len(section.EvidenceFiles) > 0 {
			b.WriteString("Evidence files:\n")
			limit := len(section.EvidenceFiles)
			if limit > 5 {
				limit = 5
			}
			for _, item := range section.EvidenceFiles[:limit] {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n")
		}
		if len(section.InternalFlow) > 0 {
			b.WriteString("Internal flow:\n")
			for _, item := range section.InternalFlow {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n")
		}
		if len(section.Collaboration) > 0 {
			b.WriteString("Collaboration:\n")
			for _, item := range section.Collaboration {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n")
		}
		if len(section.Dependencies) > 0 {
			b.WriteString("Dependencies:\n")
			for _, item := range section.Dependencies {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n")
		}
		if len(section.Risks) > 0 {
			b.WriteString("Risks:\n")
			for _, item := range section.Risks {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n")
		}
		if len(section.Unknowns) > 0 {
			b.WriteString("Unknowns:\n")
			for _, item := range section.Unknowns {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n")
		}
		if len(section.RootCauseCandidates) > 0 {
			b.WriteString("Root-cause candidates:\n")
			for _, candidate := range section.RootCauseCandidates {
				fmt.Fprintf(&b, "- %s", firstNonBlankAnalysisString(candidate.Title, "Candidate"))
				if strings.TrimSpace(candidate.Confidence) != "" {
					fmt.Fprintf(&b, " (confidence=%s)", candidate.Confidence)
				}
				b.WriteString("\n")
				for _, item := range limitStrings(candidate.CandidateChain, 3) {
					fmt.Fprintf(&b, "  chain: %s\n", item)
				}
				for _, item := range limitStrings(candidate.ObservedFailurePath, 3) {
					fmt.Fprintf(&b, "  failure_path: %s\n", item)
				}
				for _, item := range limitStrings(candidate.EvidenceFiles, 3) {
					fmt.Fprintf(&b, "  evidence: %s\n", item)
				}
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## Dependencies And Integration Points\n\n")
	for _, item := range allDeps {
		fmt.Fprintf(&b, "- Dependency: %s\n", item)
	}
	for _, section := range sections {
		for _, item := range section.Collaboration {
			fmt.Fprintf(&b, "- Integration: %s\n", item)
		}
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "## Risks And Unknowns\n\n")
	for _, item := range allRisks {
		fmt.Fprintf(&b, "- Risk: %s\n", item)
	}
	for _, item := range allUnknowns {
		fmt.Fprintf(&b, "- Unknown: %s\n", item)
	}
	if len(allRisks) == 0 && len(allUnknowns) == 0 {
		b.WriteString("- No explicit risks or unknowns were reported.\n")
	}
	b.WriteString("\n")
	for _, report := range reports {
		if strings.TrimSpace(report.Narrative) != "" {
			fmt.Fprintf(&b, "### Notes: %s\n\n", report.Title)
			b.WriteString(report.Narrative)
			b.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func buildShardDocuments(snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, goal string) map[string]string {
	out := map[string]string{}
	for index, shard := range shards {
		if index >= len(reports) {
			continue
		}
		report := reports[index]
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n", shard.Name)
		fmt.Fprintf(&b, "- Shard ID: %s\n", shard.ID)
		fmt.Fprintf(&b, "- Goal: %s\n", goal)
		if strings.TrimSpace(snapshot.AnalysisMode) != "" {
			fmt.Fprintf(&b, "- Mode: %s\n", snapshot.AnalysisMode)
		}
		fmt.Fprintf(&b, "- Primary files: %d\n", len(shard.PrimaryFiles))
		fmt.Fprintf(&b, "- Reference files: %d\n", len(shard.ReferenceFiles))
		if strings.TrimSpace(shard.CacheStatus) != "" {
			fmt.Fprintf(&b, "- Cache status: %s\n", shard.CacheStatus)
		}
		if strings.TrimSpace(shard.InvalidationReason) != "" {
			fmt.Fprintf(&b, "- Invalidation reason: %s\n", describeAnalysisInvalidationReasonWithContext(shard.InvalidationReason, []string{shard.Name}))
			evidence := buildInvalidationEvidenceLines(snapshot, []string{shard.Name}, firstNonEmpty(shard.PrimaryFiles, report.EvidenceFiles), []string{shard.InvalidationReason}, 4)
			if len(evidence) > 0 {
				fmt.Fprintf(&b, "- Invalidation evidence: %s\n", strings.Join(evidence, " | "))
			}
			if len(shard.InvalidationDiff) > 0 {
				fmt.Fprintf(&b, "- Invalidation diff: %s\n", strings.Join(limitStrings(shard.InvalidationDiff, 4), " | "))
			}
		}
		b.WriteString("\n## Scope Summary\n\n")
		b.WriteString(strings.TrimSpace(report.ScopeSummary))
		b.WriteString("\n\n## Primary Files\n\n")
		for _, item := range shard.PrimaryFiles {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		if len(shard.ReferenceFiles) > 0 {
			b.WriteString("\n## Reference Files\n\n")
			for _, item := range shard.ReferenceFiles {
				fmt.Fprintf(&b, "- %s\n", item)
			}
		}
		writeSectionList(&b, "Responsibilities", report.Responsibilities)
		writeSectionList(&b, "Facts", report.Facts)
		writeSectionList(&b, "Inferences", report.Inferences)
		writeSectionList(&b, "Key Files", report.KeyFiles)
		writeSectionList(&b, "Entry Points", report.EntryPoints)
		writeSectionList(&b, "Internal Flow", report.InternalFlow)
		writeSectionList(&b, "Dependencies", report.Dependencies)
		writeSectionList(&b, "Collaboration", report.Collaboration)
		writeSectionList(&b, "Risks", report.Risks)
		writeSectionList(&b, "Unknowns", report.Unknowns)
		writeSectionList(&b, "Evidence Files", report.EvidenceFiles)
		writeRootCauseCandidates(&b, report.RootCauseCandidates)
		b.WriteString("## Narrative\n\n")
		b.WriteString(strings.TrimSpace(report.Narrative))
		b.WriteString("\n")
		out[shard.ID] = strings.TrimSpace(b.String()) + "\n"
	}
	return out
}

func buildKnowledgePack(snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, goal string, runID string) KnowledgePack {
	grouped := groupedReportsForSynthesis(shards, reports)
	subsystems := make([]KnowledgeSubsystem, 0, len(grouped))
	executionSummary := buildAnalysisExecutionSummary(shards)
	projectSummary := summarizeKnowledgePack(snapshot, grouped, executionSummary)
	highRisk := []string{}
	unknowns := []string{}
	externalDeps := []string{}
	groups := []string{}
	for _, section := range grouped {
		subsystems = append(subsystems, KnowledgeSubsystem{
			Title:                strings.TrimSpace(section.Title),
			Group:                strings.TrimSpace(section.Group),
			ShardIDs:             append([]string(nil), section.ShardIDs...),
			ShardNames:           append([]string(nil), section.ShardNames...),
			CacheStatuses:        append([]string(nil), section.CacheStatuses...),
			InvalidationReasons:  append([]string(nil), section.InvalidationReasons...),
			InvalidationEvidence: buildInvalidationEvidenceLines(snapshot, section.ShardNames, firstNonEmpty(section.EvidenceFiles, section.KeyFiles), section.InvalidationReasons, 6),
			InvalidationDiff:     append([]string(nil), section.InvalidationDiff...),
			InvalidationChanges:  append([]InvalidationChange(nil), section.InvalidationChanges...),
			Responsibilities:     append([]string(nil), section.Responsibilities...),
			Facts:                append([]string(nil), section.Facts...),
			Inferences:           append([]string(nil), section.Inferences...),
			KeyFiles:             append([]string(nil), section.KeyFiles...),
			EvidenceFiles:        append([]string(nil), section.EvidenceFiles...),
			EntryPoints:          append([]string(nil), section.EntryPoints...),
			Dependencies:         append([]string(nil), section.Dependencies...),
			Collaboration:        append([]string(nil), section.Collaboration...),
			Risks:                append([]string(nil), section.Risks...),
			Unknowns:             append([]string(nil), section.Unknowns...),
			RootCauseCandidates:  append([]RootCauseCandidate(nil), section.RootCauseCandidates...),
		})
		if strings.TrimSpace(section.Group) != "" {
			groups = append(groups, section.Group)
		}
		if section.Group == "External Dependencies" {
			externalDeps = append(externalDeps, section.Title)
			externalDeps = append(externalDeps, limitStrings(section.Dependencies, 3)...)
		}
		highRisk = append(highRisk, limitStrings(section.KeyFiles, 3)...)
		highRisk = append(highRisk, limitStrings(section.EvidenceFiles, 2)...)
		unknowns = append(unknowns, limitStrings(section.Unknowns, 3)...)
		unknowns = append(unknowns, limitStrings(section.Risks, 2)...)
	}
	return KnowledgePack{
		RunID:                runID,
		Goal:                 goal,
		AnalysisMode:         snapshot.AnalysisMode,
		Root:                 snapshot.Root,
		GeneratedAt:          snapshot.GeneratedAt,
		AnalysisLenses:       append([]AnalysisLens(nil), snapshot.AnalysisLenses...),
		ProjectSummary:       projectSummary,
		PrimaryStartup:       snapshot.PrimaryStartup,
		StartupCandidates:    append([]string(nil), snapshot.StartupProjects...),
		StartupEntryFiles:    startupProjectEntryFiles(snapshot),
		ManifestFiles:        append([]string(nil), snapshot.ManifestFiles...),
		EntrypointFiles:      append([]string(nil), snapshot.EntrypointFiles...),
		ArchitectureGroups:   analysisUniqueStrings(groups),
		TopImportantFiles:    topImportantFilePaths(snapshot, 15),
		ProjectEdges:         append([]ProjectEdge(nil), limitProjectEdges(snapshot.ProjectEdges, 40)...),
		UnrealProjects:       append([]UnrealProject(nil), snapshot.UnrealProjects...),
		UnrealPlugins:        append([]UnrealPlugin(nil), snapshot.UnrealPlugins...),
		UnrealTargets:        append([]UnrealTarget(nil), snapshot.UnrealTargets...),
		UnrealModules:        append([]UnrealModule(nil), limitUnrealModules(snapshot.UnrealModules, 24)...),
		UnrealTypes:          append([]UnrealReflectedType(nil), limitUnrealTypes(snapshot.UnrealTypes, 32)...),
		UnrealNetwork:        append([]UnrealNetworkSurface(nil), limitUnrealNetwork(snapshot.UnrealNetwork, 24)...),
		UnrealAssets:         append([]UnrealAssetReference(nil), limitUnrealAssets(snapshot.UnrealAssets, 24)...),
		UnrealSystems:        append([]UnrealGameplaySystem(nil), limitUnrealSystems(snapshot.UnrealSystems, 24)...),
		UnrealSettings:       append([]UnrealProjectSetting(nil), snapshot.UnrealSettings...),
		PrimaryUnrealModule:  snapshot.PrimaryUnrealModule,
		Subsystems:           subsystems,
		ExternalDependencies: analysisUniqueStrings(externalDeps),
		HighRiskFiles:        analysisUniqueStrings(highRisk),
		Unknowns:             analysisUniqueStrings(unknowns),
		AnalysisExecution:    executionSummary,
		PerformanceLens:      buildPerformanceLens(snapshot, grouped),
		RootCause:            snapshot.RootCause,
		RootCausePatterns:    append([]RootCausePatternMatch(nil), snapshot.RootCause.PatternMatches...),
		ArchitectureFacts:    snapshot.ArchitectureFacts,
	}
}

func buildPerformanceLens(snapshot ProjectSnapshot, items []synthesisSection) PerformanceLens {
	lens := PerformanceLens{
		PrimaryStartup:    snapshot.PrimaryStartup,
		StartupEntryFiles: startupProjectEntryFiles(snapshot),
	}
	fileLines := map[string]int{}
	for _, file := range snapshot.Files {
		fileLines[file.Path] = file.LineCount
	}

	largeFiles := []string{}
	for _, file := range snapshot.Files {
		if file.LineCount >= 800 {
			largeFiles = append(largeFiles, fmt.Sprintf("%s (%d lines)", file.Path, file.LineCount))
		}
	}
	sort.Strings(largeFiles)
	lens.LargeFiles = limitStrings(largeFiles, 20)

	criticalPaths := []string{}
	hotspots := []PerformanceHotspot{}
	ioBound := []string{}
	cpuBound := []string{}
	memoryRisk := []string{}
	heavyEntrypoints := []string{}

	for _, item := range items {
		signals := detectPerformanceSignals(item)
		if len(signals) == 0 && len(item.EntryPoints) == 0 {
			continue
		}
		category := classifyPerformanceCategory(signals)
		score := scorePerformanceHotspot(item, signals, fileLines)
		reason := performanceReason(item, signals, fileLines, score)
		hotspot := PerformanceHotspot{
			Title:       canonicalSynthesisTitle(item),
			Category:    category,
			Score:       score,
			Reason:      reason,
			Files:       limitStrings(firstNonEmpty(item.KeyFiles, item.EvidenceFiles), 5),
			EntryPoints: limitStrings(item.EntryPoints, 4),
			Signals:     limitStrings(signals, 5),
		}
		hotspots = append(hotspots, hotspot)
		if len(item.InternalFlow) > 0 {
			criticalPaths = append(criticalPaths, fmt.Sprintf("%s: %s", canonicalSynthesisTitle(item), strings.Join(limitStrings(item.InternalFlow, 2), " -> ")))
		}
		if len(item.EntryPoints) > 0 && (len(signals) > 0 || hasLargeFile(item.KeyFiles, fileLines, 700) || hasLargeFile(item.EvidenceFiles, fileLines, 700)) {
			heavyEntrypoints = append(heavyEntrypoints, item.EntryPoints...)
		}
		switch category {
		case "io_bound":
			ioBound = append(ioBound, canonicalSynthesisTitle(item))
		case "cpu_bound":
			cpuBound = append(cpuBound, canonicalSynthesisTitle(item))
		case "memory_risk":
			memoryRisk = append(memoryRisk, canonicalSynthesisTitle(item))
		case "mixed":
			ioBound = append(ioBound, canonicalSynthesisTitle(item))
			cpuBound = append(cpuBound, canonicalSynthesisTitle(item))
		}
	}
	sort.SliceStable(hotspots, func(i int, j int) bool {
		if hotspots[i].Score == hotspots[j].Score {
			return hotspots[i].Title < hotspots[j].Title
		}
		return hotspots[i].Score > hotspots[j].Score
	})

	lens.CriticalPaths = limitStrings(analysisUniqueStrings(criticalPaths), 12)
	lens.Hotspots = hotspots
	lens.HeavyEntrypoints = limitStrings(analysisUniqueStrings(heavyEntrypoints), 12)
	lens.IOBoundCandidates = limitStrings(analysisUniqueStrings(ioBound), 12)
	lens.CPUBoundCandidates = limitStrings(analysisUniqueStrings(cpuBound), 12)
	lens.MemoryRiskCandidates = limitStrings(analysisUniqueStrings(memoryRisk), 12)
	return lens
}

func topImportantFiles(snapshot ProjectSnapshot, limit int) []ScannedFile {
	files := append([]ScannedFile(nil), snapshot.Files...)
	sort.SliceStable(files, func(i int, j int) bool {
		if files[i].ImportanceScore == files[j].ImportanceScore {
			return files[i].Path < files[j].Path
		}
		return files[i].ImportanceScore > files[j].ImportanceScore
	})
	if limit > 0 && len(files) > limit {
		files = files[:limit]
	}
	return files
}

func topImportantFilePaths(snapshot ProjectSnapshot, limit int) []string {
	files := topImportantFiles(snapshot, limit)
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, fmt.Sprintf("%s (score=%d)", file.Path, file.ImportanceScore))
	}
	return out
}

func limitProjectEdges(edges []ProjectEdge, limit int) []ProjectEdge {
	if limit <= 0 || len(edges) == 0 {
		return nil
	}
	if len(edges) <= limit {
		return append([]ProjectEdge(nil), edges...)
	}
	return append([]ProjectEdge(nil), edges[:limit]...)
}

func analysisUniqueProjectEdges(items []ProjectEdge) []ProjectEdge {
	out := make([]ProjectEdge, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item.Source) + "->" + strings.TrimSpace(item.Target) + ":" + strings.TrimSpace(item.Type) + ":" + strings.TrimSpace(item.Attributes["kind"]) + ":" + strings.TrimSpace(item.Attributes["flow"]))
		if _, ok := seen[key]; ok {
			continue
		}
		out = append(out, item)
		seen[key] = struct{}{}
	}
	return out
}

func limitUnrealModules(modules []UnrealModule, limit int) []UnrealModule {
	if limit <= 0 || len(modules) == 0 {
		return nil
	}
	if len(modules) <= limit {
		return append([]UnrealModule(nil), modules...)
	}
	return append([]UnrealModule(nil), modules[:limit]...)
}

func limitUnrealTypes(items []UnrealReflectedType, limit int) []UnrealReflectedType {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]UnrealReflectedType(nil), items...)
	}
	return append([]UnrealReflectedType(nil), items[:limit]...)
}

func limitUnrealNetwork(items []UnrealNetworkSurface, limit int) []UnrealNetworkSurface {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]UnrealNetworkSurface(nil), items...)
	}
	return append([]UnrealNetworkSurface(nil), items[:limit]...)
}

func limitUnrealAssets(items []UnrealAssetReference, limit int) []UnrealAssetReference {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]UnrealAssetReference(nil), items...)
	}
	return append([]UnrealAssetReference(nil), items[:limit]...)
}

func limitUnrealSystems(items []UnrealGameplaySystem, limit int) []UnrealGameplaySystem {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]UnrealGameplaySystem(nil), items...)
	}
	return append([]UnrealGameplaySystem(nil), items[:limit]...)
}

func detectPerformanceSignals(item synthesisSection) []string {
	corpus := strings.ToLower(strings.Join(append(append(append([]string{}, item.Responsibilities...), item.Facts...), append(item.EntryPoints, item.Dependencies...)...), " | "))
	signals := []string{}
	if containsAny(corpus, "etw", "monitor", "watch", "filesystem", "file system", "registry", "upload", "download", "s3", "network", "socket", "stream", "compress", "zip", "decompress", "prefetch", "parse") {
		signals = append(signals, "heavy io path")
	}
	if containsAny(corpus, "scan", "scanner", "decode", "encode", "hash", "sha", "crypto", "encrypt", "decrypt", "disassembly", "zydis", "analysis", "entropy", "spoof", "compression") {
		signals = append(signals, "cpu intensive path")
	}
	if containsAny(corpus, "memory", "buffer", "shared memory", "cache", "allocator", "storage", "thread", "async") {
		signals = append(signals, "memory pressure or concurrency risk")
	}
	return analysisUniqueStrings(signals)
}

func classifyPerformanceCategory(signals []string) string {
	hasIO := false
	hasCPU := false
	hasMemory := false
	for _, signal := range signals {
		switch signal {
		case "heavy io path":
			hasIO = true
		case "cpu intensive path":
			hasCPU = true
		case "memory pressure or concurrency risk":
			hasMemory = true
		}
	}
	switch {
	case hasIO && hasCPU:
		return "mixed"
	case hasIO:
		return "io_bound"
	case hasCPU:
		return "cpu_bound"
	case hasMemory:
		return "memory_risk"
	default:
		return ""
	}
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func containsString(items []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}

func firstNonEmpty(primary []string, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

func hasLargeFile(paths []string, fileLines map[string]int, threshold int) bool {
	for _, path := range paths {
		if fileLines[path] >= threshold {
			return true
		}
	}
	return false
}

func scorePerformanceHotspot(item synthesisSection, signals []string, fileLines map[string]int) int {
	score := 0
	for _, signal := range signals {
		switch signal {
		case "heavy io path":
			score += 3
		case "cpu intensive path":
			score += 4
		case "memory pressure or concurrency risk":
			score += 4
		}
	}
	if len(item.EntryPoints) > 0 {
		score += 2
	}
	if hasLargeFile(item.KeyFiles, fileLines, 800) || hasLargeFile(item.EvidenceFiles, fileLines, 800) {
		score += 3
	}
	corpus := strings.ToLower(strings.Join(append(append(append([]string{}, item.Responsibilities...), item.Facts...), append(item.EntryPoints, item.Dependencies...)...), " | "))
	if containsAny(corpus, "lock", "mutex", "critical", "srwlock", "spin", "contention") {
		score += 4
	}
	if containsAny(corpus, "alloc", "allocator", "buffer", "resize", "copy", "cache") {
		score += 3
	}
	if containsAny(corpus, "log", "logging", "trace", "event") {
		score += 2
	}
	if containsAny(corpus, "retry", "poll", "loop", "watch", "monitor") {
		score += 3
	}
	return score
}

func performanceReason(item synthesisSection, signals []string, fileLines map[string]int, score int) string {
	parts := []string{}
	if len(signals) > 0 {
		parts = append(parts, "signals="+strings.Join(limitStrings(signals, 3), "; "))
	}
	if len(item.EntryPoints) > 0 {
		parts = append(parts, "entrypoints="+strings.Join(limitStrings(item.EntryPoints, 2), "; "))
	}
	if hasLargeFile(item.KeyFiles, fileLines, 800) || hasLargeFile(item.EvidenceFiles, fileLines, 800) {
		parts = append(parts, "large execution file present")
	}
	parts = append(parts, fmt.Sprintf("score=%d", score))
	return strings.Join(parts, " | ")
}

func summarizeKnowledgePack(snapshot ProjectSnapshot, items []synthesisSection, execution AnalysisExecutionSummary) string {
	if len(items) == 0 {
		return summarizeExecutionFocus(snapshot.AnalysisLenses, execution)
	}
	top := items[0]
	summary := []string{}
	if strings.TrimSpace(top.Group) != "" {
		summary = append(summary, "Primary architecture group: "+top.Group)
	}
	if strings.TrimSpace(top.Title) != "" {
		summary = append(summary, "Lead subsystem: "+top.Title)
	}
	if len(top.Responsibilities) > 0 {
		summary = append(summary, "Responsibilities: "+strings.Join(limitStrings(top.Responsibilities, 3), "; "))
	}
	if focus := summarizeExecutionFocus(snapshot.AnalysisLenses, execution); strings.TrimSpace(focus) != "" {
		summary = append(summary, focus)
	}
	return strings.Join(summary, " | ")
}

func summarizeExecutionFocus(lenses []AnalysisLens, execution AnalysisExecutionSummary) string {
	if len(execution.TopChangeClasses) == 0 {
		return ""
	}
	lensSet := map[string]struct{}{}
	for _, lens := range lenses {
		lensSet[strings.TrimSpace(lens.Type)] = struct{}{}
	}
	joined := strings.ToLower(strings.Join(execution.TopChangeClasses, " | "))
	focusParts := []string{}
	if containsAny(joined, "trust_boundary_edge", "security_signal", "rpc_added", "replicated_property_added") {
		if _, ok := lensSet["security_boundary"]; ok {
			focusParts = append(focusParts, "Executive focus: recent changes are concentrated on authority, replication, or security-sensitive boundaries.")
		} else {
			focusParts = append(focusParts, "Executive focus: recent changes are concentrated on authority or replication-sensitive runtime surfaces.")
		}
	}
	if containsAny(joined, "startup_edge", "startup_map_changed", "startup_game_instance_changed") {
		focusParts = append(focusParts, "Startup impact: bootstrap or startup ownership changed in the latest analysis window.")
	}
	if containsAny(joined, "config_binding", "asset_target", "game_default_map_changed", "default_game_mode_changed") {
		focusParts = append(focusParts, "Content/config impact: config-driven startup or asset binding changed materially.")
	}
	if containsAny(joined, "widget_binding_added", "ability_binding_added", "effect_binding_added", "gameplay_function_added") {
		focusParts = append(focusParts, "Gameplay impact: gameplay framework, UI, or ability coupling changed materially.")
	}
	return strings.Join(analysisUniqueStrings(focusParts), " | ")
}

func buildAnalysisExecutionSummary(shards []AnalysisShard) AnalysisExecutionSummary {
	summary := AnalysisExecutionSummary{
		TotalShards:                 len(shards),
		InvalidationReasons:         []string{},
		SemanticInvalidationReasons: []string{},
	}
	reasons := []string{}
	semanticReasons := []string{}
	changeCounts := map[string]int{}
	changeExamples := map[string]string{}
	for _, shard := range shards {
		switch strings.ToLower(strings.TrimSpace(shard.CacheStatus)) {
		case "reused":
			summary.ReusedShards++
		case "miss":
			summary.MissedShards++
		}
		switch strings.ToLower(strings.TrimSpace(shard.InvalidationReason)) {
		case "new", "new_primary_scope":
			summary.NewShards++
		case "recomputed", "primary_changed", "reference_changed", "dependency_changed", "previous_review_not_approved", "previous_run_incomplete":
			summary.RecomputedShards++
		}
		reason := strings.TrimSpace(shard.InvalidationReason)
		if reason != "" {
			reasons = append(reasons, reason)
			if strings.Contains(strings.ToLower(reason), "semantic") {
				semanticReasons = append(semanticReasons, reason)
				summary.SemanticRecomputedShards++
			}
		}
		for _, change := range shard.InvalidationChanges {
			kind := strings.TrimSpace(change.Kind)
			if kind == "" {
				continue
			}
			changeCounts[kind]++
			if _, ok := changeExamples[kind]; !ok {
				changeExamples[kind] = renderInvalidationChange(change)
			}
		}
	}
	summary.InvalidationReasons = analysisUniqueStrings(reasons)
	summary.SemanticInvalidationReasons = analysisUniqueStrings(semanticReasons)
	summary.TopChangeClasses = summarizeTopInvalidationChangeClasses(changeCounts, 6)
	summary.TopChangeExamples = summarizeTopInvalidationChangeExamples(changeCounts, changeExamples, 4)
	return summary
}

func summarizeTopInvalidationChangeClasses(counts map[string]int, limit int) []string {
	type item struct {
		kind  string
		count int
	}
	items := make([]item, 0, len(counts))
	for kind, count := range counts {
		items = append(items, item{kind: kind, count: count})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].count == items[j].count {
			return items[i].kind < items[j].kind
		}
		return items[i].count > items[j].count
	})
	out := []string{}
	for _, item := range items {
		out = append(out, fmt.Sprintf("%s (%d)", item.kind, item.count))
	}
	return limitStrings(out, limit)
}

func summarizeTopInvalidationChangeExamples(counts map[string]int, examples map[string]string, limit int) []string {
	type item struct {
		kind    string
		count   int
		example string
	}
	items := make([]item, 0, len(counts))
	for kind, count := range counts {
		example := strings.TrimSpace(examples[kind])
		if example == "" {
			continue
		}
		items = append(items, item{kind: kind, count: count, example: example})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].count == items[j].count {
			return items[i].kind < items[j].kind
		}
		return items[i].count > items[j].count
	})
	out := []string{}
	for _, item := range items {
		out = append(out, fmt.Sprintf("%s: %s", item.kind, item.example))
	}
	return limitStrings(out, limit)
}

func writeAnalysisExecutionSummary(b *strings.Builder, summary AnalysisExecutionSummary) {
	fmt.Fprintf(b, "- Total shards: %d\n", summary.TotalShards)
	if summary.ReusedShards > 0 {
		fmt.Fprintf(b, "- Reused shards: %d\n", summary.ReusedShards)
	}
	if summary.MissedShards > 0 {
		fmt.Fprintf(b, "- Recomputed shards: %d\n", summary.MissedShards)
	}
	if summary.NewShards > 0 {
		fmt.Fprintf(b, "- New scope shards: %d\n", summary.NewShards)
	}
	if summary.SemanticRecomputedShards > 0 {
		fmt.Fprintf(b, "- Semantic invalidation shards: %d\n", summary.SemanticRecomputedShards)
	}
	if len(summary.InvalidationReasons) > 0 {
		fmt.Fprintf(b, "- Invalidation reasons: %s\n", strings.Join(describeAnalysisInvalidationReasons(summary.InvalidationReasons, 8), " | "))
	}
	if len(summary.SemanticInvalidationReasons) > 0 {
		fmt.Fprintf(b, "- Semantic invalidation reasons: %s\n", strings.Join(describeAnalysisInvalidationReasons(summary.SemanticInvalidationReasons, 8), " | "))
	}
	if len(summary.TopChangeClasses) > 0 {
		fmt.Fprintf(b, "- Top change classes: %s\n", strings.Join(summary.TopChangeClasses, ", "))
	}
	if len(summary.TopChangeExamples) > 0 {
		fmt.Fprintf(b, "- Top change examples: %s\n", strings.Join(summary.TopChangeExamples, " | "))
	}
}

func buildKnowledgeDigest(pack KnowledgePack) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Architecture Knowledge Digest\n\n")
	fmt.Fprintf(&b, "- Run ID: `%s`\n", pack.RunID)
	fmt.Fprintf(&b, "- Goal: %s\n", pack.Goal)
	if strings.TrimSpace(pack.AnalysisMode) != "" {
		fmt.Fprintf(&b, "- Mode: `%s`\n", pack.AnalysisMode)
	}
	fmt.Fprintf(&b, "- Root: `%s`\n", pack.Root)
	if strings.TrimSpace(pack.PrimaryStartup) != "" {
		fmt.Fprintf(&b, "- Primary startup project: `%s`\n", pack.PrimaryStartup)
	}
	if len(pack.StartupEntryFiles) > 0 {
		fmt.Fprintf(&b, "- Startup entry files: %s\n", strings.Join(limitStrings(pack.StartupEntryFiles, 3), ", "))
	}
	if len(pack.ArchitectureGroups) > 0 {
		fmt.Fprintf(&b, "- Architecture groups: %s\n", strings.Join(pack.ArchitectureGroups, ", "))
	}
	if strings.TrimSpace(pack.ProjectSummary) != "" {
		fmt.Fprintf(&b, "\n## Summary\n\n%s\n", pack.ProjectSummary)
	}
	if pack.AnalysisExecution.TotalShards > 0 {
		fmt.Fprintf(&b, "\n## Analysis Execution\n\n")
		writeAnalysisExecutionSummary(&b, pack.AnalysisExecution)
		b.WriteString("\n")
	}
	if factText := renderArchitectureFactPackForPrompt(pack.ArchitectureFacts, AnalysisShard{}, 6000); strings.TrimSpace(factText) != "" {
		fmt.Fprintf(&b, "\n## Deterministic Architecture Facts\n\n%s\n", factText)
	}
	if len(pack.Subsystems) > 0 {
		fmt.Fprintf(&b, "\n## Subsystems\n\n")
		for _, subsystem := range pack.Subsystems {
			fmt.Fprintf(&b, "### %s\n\n", canonicalKnowledgeTitle(subsystem))
			if len(subsystem.CacheStatuses) > 0 {
				fmt.Fprintf(&b, "- Cache status: %s\n", strings.Join(limitStrings(subsystem.CacheStatuses, 4), "; "))
			}
			if len(subsystem.InvalidationReasons) > 0 {
				fmt.Fprintf(&b, "- Invalidation reasons: %s\n", strings.Join(describeAnalysisInvalidationReasonsWithContext(subsystem.InvalidationReasons, subsystem.ShardNames, 6), " | "))
			}
			if len(subsystem.InvalidationEvidence) > 0 {
				fmt.Fprintf(&b, "- Invalidation evidence: %s\n", strings.Join(limitStrings(subsystem.InvalidationEvidence, 5), " | "))
			}
			if len(subsystem.InvalidationDiff) > 0 {
				fmt.Fprintf(&b, "- Invalidation diff: %s\n", strings.Join(limitStrings(subsystem.InvalidationDiff, 5), " | "))
			}
			if len(subsystem.Responsibilities) > 0 {
				fmt.Fprintf(&b, "- Responsibilities: %s\n", strings.Join(limitStrings(subsystem.Responsibilities, 3), "; "))
			}
			if len(subsystem.EntryPoints) > 0 {
				fmt.Fprintf(&b, "- Entry points: %s\n", strings.Join(limitStrings(subsystem.EntryPoints, 3), "; "))
			}
			if len(subsystem.KeyFiles) > 0 {
				fmt.Fprintf(&b, "- Key files: %s\n", strings.Join(limitStrings(subsystem.KeyFiles, 4), "; "))
			}
			if len(subsystem.Dependencies) > 0 {
				fmt.Fprintf(&b, "- Dependencies: %s\n", strings.Join(limitStrings(subsystem.Dependencies, 3), "; "))
			}
			if len(subsystem.Risks) > 0 {
				fmt.Fprintf(&b, "- Risks: %s\n", strings.Join(limitStrings(subsystem.Risks, 2), "; "))
			}
			if len(subsystem.Unknowns) > 0 {
				fmt.Fprintf(&b, "- Unknowns: %s\n", strings.Join(limitStrings(subsystem.Unknowns, 2), "; "))
			}
			b.WriteString("\n")
		}
	}
	if len(pack.HighRiskFiles) > 0 {
		fmt.Fprintf(&b, "## High Risk Files\n\n")
		for _, item := range limitStrings(pack.HighRiskFiles, 12) {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	if len(pack.ExternalDependencies) > 0 {
		fmt.Fprintf(&b, "## External Dependencies\n\n")
		for _, item := range limitStrings(pack.ExternalDependencies, 12) {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func buildVectorCorpus(run ProjectAnalysisRun) VectorCorpus {
	corpus := VectorCorpus{
		RunID:       run.Summary.RunID,
		Goal:        run.Summary.Goal,
		Root:        run.Snapshot.Root,
		GeneratedAt: run.Snapshot.GeneratedAt,
		Documents:   []VectorCorpusDocument{},
	}
	add := func(doc VectorCorpusDocument) {
		doc.ID = strings.TrimSpace(doc.ID)
		doc.Kind = strings.TrimSpace(doc.Kind)
		doc.Title = strings.TrimSpace(doc.Title)
		doc.Text = strings.TrimSpace(doc.Text)
		if doc.ID == "" || doc.Kind == "" || doc.Title == "" || doc.Text == "" {
			return
		}
		if doc.Metadata == nil {
			doc.Metadata = map[string]string{}
		}
		corpus.Documents = append(corpus.Documents, doc)
	}

	add(VectorCorpusDocument{
		ID:    "project_summary",
		Kind:  "project_summary",
		Title: "Project Summary",
		Text:  buildVectorProjectSummary(run),
		Metadata: map[string]string{
			"run_id": run.Summary.RunID,
			"goal":   run.Summary.Goal,
		},
	})
	add(VectorCorpusDocument{
		ID:    "analysis_execution",
		Kind:  "analysis_execution",
		Title: "Analysis Execution Summary",
		Text:  buildVectorExecutionSummary(run.KnowledgePack.AnalysisExecution),
		Metadata: map[string]string{
			"run_id": run.Summary.RunID,
			"goal":   run.Summary.Goal,
		},
	})
	for _, subsystem := range run.KnowledgePack.Subsystems {
		add(VectorCorpusDocument{
			ID:       "subsystem:" + sanitizeFileName(canonicalKnowledgeTitle(subsystem)),
			Kind:     "subsystem",
			Title:    canonicalKnowledgeTitle(subsystem),
			Text:     buildVectorSubsystemDocument(subsystem),
			PathHint: firstNonBlankAnalysisString(firstSliceValue(subsystem.EvidenceFiles), firstSliceValue(subsystem.KeyFiles)),
			Metadata: map[string]string{
				"group": strings.TrimSpace(subsystem.Group),
			},
		})
	}
	for shardID, doc := range run.ShardDocuments {
		shardName := shardID
		for _, shard := range run.Shards {
			if shard.ID == shardID {
				shardName = shard.Name
				break
			}
		}
		add(VectorCorpusDocument{
			ID:    "shard:" + shardID,
			Kind:  "shard",
			Title: shardName,
			Text:  doc,
			Metadata: map[string]string{
				"shard_id": shardID,
			},
		})
	}
	for _, doc := range buildAnalysisDocsVectorDocuments(run) {
		add(doc)
	}
	return corpus
}

func buildVectorProjectSummary(run ProjectAnalysisRun) string {
	parts := []string{}
	if strings.TrimSpace(run.KnowledgePack.ProjectSummary) != "" {
		parts = append(parts, run.KnowledgePack.ProjectSummary)
	}
	if strings.TrimSpace(run.Summary.Goal) != "" {
		parts = append(parts, "Goal: "+run.Summary.Goal)
	}
	if strings.TrimSpace(run.KnowledgePack.PrimaryStartup) != "" {
		parts = append(parts, "Primary startup: "+run.KnowledgePack.PrimaryStartup)
	}
	if len(run.KnowledgePack.ArchitectureGroups) > 0 {
		parts = append(parts, "Architecture groups: "+strings.Join(run.KnowledgePack.ArchitectureGroups, ", "))
	}
	if len(run.KnowledgePack.TopImportantFiles) > 0 {
		parts = append(parts, "Top important files: "+strings.Join(limitStrings(run.KnowledgePack.TopImportantFiles, 6), " | "))
	}
	return strings.Join(parts, "\n")
}

func buildVectorExecutionSummary(summary AnalysisExecutionSummary) string {
	parts := []string{}
	parts = append(parts, fmt.Sprintf("Total shards: %d", summary.TotalShards))
	if summary.SemanticRecomputedShards > 0 {
		parts = append(parts, fmt.Sprintf("Semantic invalidation shards: %d", summary.SemanticRecomputedShards))
	}
	if len(summary.TopChangeClasses) > 0 {
		parts = append(parts, "Top change classes: "+strings.Join(summary.TopChangeClasses, ", "))
	}
	if len(summary.TopChangeExamples) > 0 {
		parts = append(parts, "Top change examples: "+strings.Join(summary.TopChangeExamples, " | "))
	}
	if len(summary.SemanticInvalidationReasons) > 0 {
		parts = append(parts, "Semantic invalidation reasons: "+strings.Join(describeAnalysisInvalidationReasons(summary.SemanticInvalidationReasons, 6), " | "))
	}
	return strings.Join(parts, "\n")
}

func buildVectorSubsystemDocument(subsystem KnowledgeSubsystem) string {
	parts := []string{}
	if len(subsystem.Responsibilities) > 0 {
		parts = append(parts, "Responsibilities: "+strings.Join(limitStrings(subsystem.Responsibilities, 5), " | "))
	}
	if len(subsystem.EntryPoints) > 0 {
		parts = append(parts, "Entry points: "+strings.Join(limitStrings(subsystem.EntryPoints, 4), " | "))
	}
	if len(subsystem.Dependencies) > 0 {
		parts = append(parts, "Dependencies: "+strings.Join(limitStrings(subsystem.Dependencies, 5), " | "))
	}
	if len(subsystem.InvalidationReasons) > 0 {
		parts = append(parts, "Invalidation reasons: "+strings.Join(describeAnalysisInvalidationReasonsWithContext(subsystem.InvalidationReasons, subsystem.ShardNames, 6), " | "))
	}
	if len(subsystem.InvalidationEvidence) > 0 {
		parts = append(parts, "Invalidation evidence: "+strings.Join(limitStrings(subsystem.InvalidationEvidence, 6), " | "))
	}
	if len(subsystem.InvalidationDiff) > 0 {
		parts = append(parts, "Invalidation diff: "+strings.Join(limitStrings(subsystem.InvalidationDiff, 6), " | "))
	}
	if len(subsystem.KeyFiles) > 0 {
		parts = append(parts, "Key files: "+strings.Join(limitStrings(subsystem.KeyFiles, 6), " | "))
	}
	if len(subsystem.EvidenceFiles) > 0 {
		parts = append(parts, "Evidence files: "+strings.Join(limitStrings(subsystem.EvidenceFiles, 6), " | "))
	}
	return strings.Join(parts, "\n")
}

func buildVectorCorpusJSONL(corpus VectorCorpus) string {
	lines := []string{}
	for _, doc := range corpus.Documents {
		data, err := json.Marshal(doc)
		if err != nil {
			continue
		}
		lines = append(lines, string(data))
	}
	return strings.Join(lines, "\n") + "\n"
}

func buildVectorIngestionManifest(corpus VectorCorpus) VectorIngestionManifest {
	kinds := []string{}
	seenKinds := map[string]struct{}{}
	for _, doc := range corpus.Documents {
		kind := strings.TrimSpace(doc.Kind)
		if kind == "" {
			continue
		}
		if _, exists := seenKinds[kind]; exists {
			continue
		}
		seenKinds[kind] = struct{}{}
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return VectorIngestionManifest{
		RunID:         corpus.RunID,
		Goal:          corpus.Goal,
		Root:          corpus.Root,
		GeneratedAt:   corpus.GeneratedAt,
		DocumentCount: len(corpus.Documents),
		DocumentKinds: kinds,
		Targets: []VectorIngestionTarget{
			{
				Name:        "records",
				Format:      "jsonl",
				Filename:    "vector_ingest_records.jsonl",
				Description: "Canonical ingestion records with text, metadata, and content hashes.",
			},
			{
				Name:        "pgvector",
				Format:      "sql",
				Filename:    "vector_pgvector.sql",
				Description: "PostgreSQL and pgvector staging schema plus UPSERT statements for raw analysis documents.",
			},
			{
				Name:        "sqlite",
				Format:      "sql",
				Filename:    "vector_sqlite.sql",
				Description: "SQLite staging schema plus INSERT statements for local embedding or sqlite-vec pipelines.",
			},
			{
				Name:        "qdrant",
				Format:      "jsonl",
				Filename:    "vector_qdrant.jsonl",
				Description: "Qdrant seed payloads ready for an external embedding worker to attach vectors and upsert.",
			},
		},
	}
}

func buildVectorIngestionRecordsJSONL(corpus VectorCorpus) string {
	type ingestionRecord struct {
		ID          string            `json:"id"`
		RunID       string            `json:"run_id"`
		Goal        string            `json:"goal"`
		Kind        string            `json:"kind"`
		Title       string            `json:"title"`
		Text        string            `json:"text"`
		PathHint    string            `json:"path_hint,omitempty"`
		ContentHash string            `json:"content_hash"`
		Metadata    map[string]string `json:"metadata,omitempty"`
	}
	lines := []string{}
	for _, doc := range corpus.Documents {
		record := ingestionRecord{
			ID:          doc.ID,
			RunID:       corpus.RunID,
			Goal:        corpus.Goal,
			Kind:        doc.Kind,
			Title:       doc.Title,
			Text:        doc.Text,
			PathHint:    doc.PathHint,
			ContentHash: hashAnalysisText(doc.Text),
			Metadata:    cloneStringMap(doc.Metadata),
		}
		data, err := json.Marshal(record)
		if err != nil {
			continue
		}
		lines = append(lines, string(data))
	}
	return strings.Join(lines, "\n") + "\n"
}

func buildVectorPGVectorSQL(corpus VectorCorpus) string {
	var b strings.Builder
	b.WriteString("-- Generated by Kernforge project analysis.\n")
	b.WriteString("-- This file stages raw analysis documents for a pgvector embedding pipeline.\n\n")
	b.WriteString("CREATE EXTENSION IF NOT EXISTS vector;\n\n")
	b.WriteString("CREATE TABLE IF NOT EXISTS analysis_vector_documents (\n")
	b.WriteString("    doc_id TEXT PRIMARY KEY,\n")
	b.WriteString("    run_id TEXT NOT NULL,\n")
	b.WriteString("    goal TEXT NOT NULL,\n")
	b.WriteString("    kind TEXT NOT NULL,\n")
	b.WriteString("    title TEXT NOT NULL,\n")
	b.WriteString("    path_hint TEXT,\n")
	b.WriteString("    body TEXT NOT NULL,\n")
	b.WriteString("    content_hash TEXT NOT NULL,\n")
	b.WriteString("    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,\n")
	b.WriteString("    embedding VECTOR,\n")
	b.WriteString("    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()\n")
	b.WriteString(");\n\n")
	for _, doc := range corpus.Documents {
		metadataJSON := "{}"
		if len(doc.Metadata) > 0 {
			if data, err := json.Marshal(doc.Metadata); err == nil {
				metadataJSON = string(data)
			}
		}
		fmt.Fprintf(
			&b,
			"INSERT INTO analysis_vector_documents (doc_id, run_id, goal, kind, title, path_hint, body, content_hash, metadata)\nVALUES ('%s', '%s', '%s', '%s', '%s', %s, '%s', '%s', '%s'::jsonb)\nON CONFLICT (doc_id) DO UPDATE SET\n    run_id = EXCLUDED.run_id,\n    goal = EXCLUDED.goal,\n    kind = EXCLUDED.kind,\n    title = EXCLUDED.title,\n    path_hint = EXCLUDED.path_hint,\n    body = EXCLUDED.body,\n    content_hash = EXCLUDED.content_hash,\n    metadata = EXCLUDED.metadata,\n    updated_at = NOW();\n\n",
			escapeSQLString(doc.ID),
			escapeSQLString(corpus.RunID),
			escapeSQLString(corpus.Goal),
			escapeSQLString(doc.Kind),
			escapeSQLString(doc.Title),
			sqlNullableString(doc.PathHint),
			escapeSQLString(doc.Text),
			escapeSQLString(hashAnalysisText(doc.Text)),
			escapeSQLString(metadataJSON),
		)
	}
	return b.String()
}

func buildVectorSQLiteSQL(corpus VectorCorpus) string {
	var b strings.Builder
	b.WriteString("-- Generated by Kernforge project analysis.\n")
	b.WriteString("-- This file stages raw analysis documents for local embedding or sqlite-vec workflows.\n\n")
	b.WriteString("CREATE TABLE IF NOT EXISTS analysis_vector_documents (\n")
	b.WriteString("    doc_id TEXT PRIMARY KEY,\n")
	b.WriteString("    run_id TEXT NOT NULL,\n")
	b.WriteString("    goal TEXT NOT NULL,\n")
	b.WriteString("    kind TEXT NOT NULL,\n")
	b.WriteString("    title TEXT NOT NULL,\n")
	b.WriteString("    path_hint TEXT,\n")
	b.WriteString("    body TEXT NOT NULL,\n")
	b.WriteString("    content_hash TEXT NOT NULL,\n")
	b.WriteString("    metadata_json TEXT NOT NULL DEFAULT '{}'\n")
	b.WriteString(");\n\n")
	for _, doc := range corpus.Documents {
		metadataJSON := "{}"
		if len(doc.Metadata) > 0 {
			if data, err := json.Marshal(doc.Metadata); err == nil {
				metadataJSON = string(data)
			}
		}
		fmt.Fprintf(
			&b,
			"INSERT INTO analysis_vector_documents (doc_id, run_id, goal, kind, title, path_hint, body, content_hash, metadata_json)\nVALUES ('%s', '%s', '%s', '%s', '%s', %s, '%s', '%s', '%s')\nON CONFLICT(doc_id) DO UPDATE SET\n    run_id=excluded.run_id,\n    goal=excluded.goal,\n    kind=excluded.kind,\n    title=excluded.title,\n    path_hint=excluded.path_hint,\n    body=excluded.body,\n    content_hash=excluded.content_hash,\n    metadata_json=excluded.metadata_json;\n\n",
			escapeSQLString(doc.ID),
			escapeSQLString(corpus.RunID),
			escapeSQLString(corpus.Goal),
			escapeSQLString(doc.Kind),
			escapeSQLString(doc.Title),
			sqlNullableString(doc.PathHint),
			escapeSQLString(doc.Text),
			escapeSQLString(hashAnalysisText(doc.Text)),
			escapeSQLString(metadataJSON),
		)
	}
	return b.String()
}

func buildVectorQdrantSeedJSONL(corpus VectorCorpus) string {
	type qdrantSeed struct {
		ID      string                 `json:"id"`
		RunID   string                 `json:"run_id"`
		Payload map[string]interface{} `json:"payload"`
	}
	lines := []string{}
	for _, doc := range corpus.Documents {
		payload := map[string]interface{}{
			"run_id":       corpus.RunID,
			"goal":         corpus.Goal,
			"kind":         doc.Kind,
			"title":        doc.Title,
			"text":         doc.Text,
			"path_hint":    doc.PathHint,
			"content_hash": hashAnalysisText(doc.Text),
			"metadata":     cloneStringMap(doc.Metadata),
		}
		data, err := json.Marshal(qdrantSeed{
			ID:      doc.ID,
			RunID:   corpus.RunID,
			Payload: payload,
		})
		if err != nil {
			continue
		}
		lines = append(lines, string(data))
	}
	return strings.Join(lines, "\n") + "\n"
}

func hashAnalysisText(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func escapeSQLString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func sqlNullableString(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "NULL"
	}
	return "'" + escapeSQLString(trimmed) + "'"
}

func firstSliceValue(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return strings.TrimSpace(items[0])
}

func buildPerformanceLensDigest(lens PerformanceLens) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Performance Lens Digest\n\n")
	if strings.TrimSpace(lens.PrimaryStartup) != "" {
		fmt.Fprintf(&b, "- Primary startup project: `%s`\n", lens.PrimaryStartup)
	}
	if len(lens.StartupEntryFiles) > 0 {
		fmt.Fprintf(&b, "- Startup entry files: %s\n", strings.Join(limitStrings(lens.StartupEntryFiles, 3), ", "))
	}
	if len(lens.CriticalPaths) > 0 {
		fmt.Fprintf(&b, "\n## Critical Paths\n\n")
		for _, item := range limitStrings(lens.CriticalPaths, 10) {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if len(lens.Hotspots) > 0 {
		fmt.Fprintf(&b, "\n## Hotspots\n\n")
		for _, hotspot := range limitStringsPerformanceHotspots(lens.Hotspots, 12) {
			fmt.Fprintf(&b, "### %s\n\n", hotspot.Title)
			if strings.TrimSpace(hotspot.Category) != "" {
				fmt.Fprintf(&b, "- Category: %s\n", hotspot.Category)
			}
			if hotspot.Score > 0 {
				fmt.Fprintf(&b, "- Score: %d\n", hotspot.Score)
			}
			if strings.TrimSpace(hotspot.Reason) != "" {
				fmt.Fprintf(&b, "- Reason: %s\n", hotspot.Reason)
			}
			if len(hotspot.EntryPoints) > 0 {
				fmt.Fprintf(&b, "- Entry points: %s\n", strings.Join(limitStrings(hotspot.EntryPoints, 3), "; "))
			}
			if len(hotspot.Files) > 0 {
				fmt.Fprintf(&b, "- Files: %s\n", strings.Join(limitStrings(hotspot.Files, 4), "; "))
			}
			b.WriteString("\n")
		}
	}
	if len(lens.LargeFiles) > 0 {
		fmt.Fprintf(&b, "## Large Files\n\n")
		for _, item := range limitStrings(lens.LargeFiles, 12) {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	if len(lens.HeavyEntrypoints) > 0 {
		fmt.Fprintf(&b, "## Heavy Entrypoints\n\n")
		for _, item := range limitStrings(lens.HeavyEntrypoints, 10) {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func limitStringsPerformanceHotspots(items []PerformanceHotspot, limit int) []PerformanceHotspot {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]PerformanceHotspot(nil), items...)
	}
	return append([]PerformanceHotspot(nil), items[:limit]...)
}

func canonicalKnowledgeTitle(item KnowledgeSubsystem) string {
	group := strings.TrimSpace(item.Group)
	title := strings.TrimSpace(item.Title)
	if group == "" {
		return title
	}
	if title == "" || strings.EqualFold(group, title) {
		return group
	}
	return group + ": " + title
}

func writeSectionList(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## %s\n\n", title)
	for _, item := range items {
		fmt.Fprintf(b, "- %s\n", item)
	}
}

func writeRootCauseCandidates(b *strings.Builder, candidates []RootCauseCandidate) {
	if len(candidates) == 0 {
		return
	}
	b.WriteString("\n## Root Cause Candidates\n\n")
	for _, candidate := range candidates {
		title := strings.TrimSpace(candidate.Title)
		if title == "" {
			title = "Candidate"
		}
		fmt.Fprintf(b, "### %s\n\n", title)
		if strings.TrimSpace(candidate.Confidence) != "" {
			fmt.Fprintf(b, "- Confidence: %s\n", candidate.Confidence)
		}
		if len(candidate.ConfidenceBreakdown.Reasons) > 0 {
			fmt.Fprintf(b, "- Confidence reasons: %s\n", strings.Join(limitStrings(candidate.ConfidenceBreakdown.Reasons, 3), " | "))
		}
		writeIndentedSectionList(b, "Pattern ids", candidate.PatternIDs)
		writeIndentedSectionList(b, "Candidate chain", candidate.CandidateChain)
		writeIndentedSectionList(b, "Trigger values", candidate.TriggerValues)
		writeIndentedSectionList(b, "Expected range", candidate.ExpectedRange)
		writeIndentedSectionList(b, "Out-of-range cases", candidate.OutOfRangeCases)
		writeIndentedSectionList(b, "Observed failure path", candidate.ObservedFailurePath)
		writeIndentedSectionList(b, "Evidence files", candidate.EvidenceFiles)
		writeIndentedSectionList(b, "Disconfirming evidence", candidate.DisconfirmingEvidence)
		writeIndentedSectionList(b, "Cannot be root cause if", candidate.CannotBeRootCauseIf)
		writeIndentedSectionList(b, "Required runtime observation", candidate.RequiredRuntimeObservation)
		writeIndentedSectionList(b, "Verification steps", candidate.VerificationSteps)
		writeRootCauseProbeSection(b, candidate.Probes)
		writeIndentedSectionList(b, "Needs cross-shard evidence", candidate.NeedsCrossShardEvidence)
		b.WriteString("\n")
	}
}

func writeIndentedSectionList(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "- %s:\n", title)
	for _, item := range items {
		fmt.Fprintf(b, "  - %s\n", item)
	}
}

func isManifestFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "cargo.toml", "cargo.lock", "cmakelists.txt", "makefile", "dockerfile":
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".sln", ".vcxproj", ".csproj", ".uproject", ".uplugin":
		return true
	}
	if strings.HasSuffix(strings.ToLower(path), ".build.cs") || strings.HasSuffix(strings.ToLower(path), ".target.cs") {
		return true
	}
	return false
}

func isEntrypointFile(path string, data []byte) bool {
	name := strings.ToLower(filepath.Base(path))
	text := string(data)
	if name == "main.go" || name == "main.cpp" || name == "main.c" || name == "main.rs" || name == "main.py" {
		return true
	}
	if strings.Contains(text, "func main(") || strings.Contains(text, "int main(") || strings.Contains(text, "WinMain(") {
		return true
	}
	if strings.Contains(text, "IMPLEMENT_PRIMARY_GAME_MODULE") {
		return true
	}
	return false
}

func discoverImports(ext string, content string) []string {
	lines := splitLines(content)
	out := []string{}
	switch ext {
	case ".go":
		inBlock := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "import (") {
				inBlock = true
				continue
			}
			if inBlock {
				if trimmed == ")" {
					inBlock = false
					continue
				}
				out = append(out, extractQuotedImport(trimmed)...)
				continue
			}
			if strings.HasPrefix(trimmed, "import ") {
				out = append(out, extractQuotedImport(trimmed)...)
			}
		}
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp":
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#include ") {
				out = append(out, extractQuotedImport(trimmed)...)
			}
		}
	case ".js", ".jsx", ".ts", ".tsx":
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "import ") || strings.Contains(trimmed, "require(") {
				out = append(out, extractQuotedImport(trimmed)...)
			}
		}
	}
	return analysisUniqueStrings(out)
}

func chunkFiles(files []ScannedFile, maxFiles int, maxLines int) [][]ScannedFile {
	out := [][]ScannedFile{}
	current := []ScannedFile{}
	currentLines := 0
	for _, file := range files {
		if len(current) > 0 && (len(current) >= maxFiles || currentLines+file.LineCount > maxLines) {
			out = append(out, current)
			current = []ScannedFile{}
			currentLines = 0
		}
		current = append(current, file)
		currentLines += file.LineCount
	}
	if len(current) > 0 {
		out = append(out, current)
	}
	return out
}

func mergeDirectoryClusters(snapshot ProjectSnapshot, clusters [][]string, target int) [][]string {
	merged := append([][]string(nil), clusters...)
	dirLines := map[string]int{}
	for dir, files := range snapshot.FilesByDirectory {
		for _, file := range files {
			dirLines[dir] += file.LineCount
		}
	}
	for len(merged) > target {
		sort.Slice(merged, func(i int, j int) bool {
			left := clusterLines(dirLines, merged[i])
			right := clusterLines(dirLines, merged[j])
			if left == right {
				return clusterName(merged[i]) < clusterName(merged[j])
			}
			return left < right
		})
		combined := append(append([]string(nil), merged[0]...), merged[1]...)
		merged = append([][]string{analysisUniqueStrings(combined)}, merged[2:]...)
	}
	return merged
}

func mergeShards(shards []AnalysisShard, target int) []AnalysisShard {
	merged := append([]AnalysisShard(nil), shards...)
	for len(merged) > target {
		sort.Slice(merged, func(i int, j int) bool {
			if merged[i].EstimatedLines == merged[j].EstimatedLines {
				return merged[i].ID < merged[j].ID
			}
			return merged[i].EstimatedLines < merged[j].EstimatedLines
		})
		left := merged[0]
		right := merged[1]
		combined := AnalysisShard{
			ID:             left.ID,
			Name:           left.Name + "+" + right.Name,
			PrimaryFiles:   analysisUniqueStrings(append(append([]string(nil), left.PrimaryFiles...), right.PrimaryFiles...)),
			ReferenceFiles: analysisUniqueStrings(append(append([]string(nil), left.ReferenceFiles...), right.ReferenceFiles...)),
			EstimatedFiles: left.EstimatedFiles + right.EstimatedFiles,
			EstimatedLines: left.EstimatedLines + right.EstimatedLines,
		}
		merged = append([]AnalysisShard{combined}, merged[2:]...)
	}
	return merged
}

func hasNamedRootSubsystemShards(shards []AnalysisShard) bool {
	for _, shard := range shards {
		if strings.HasPrefix(shard.Name, "runtime") ||
			strings.HasPrefix(shard.Name, "verification") ||
			strings.HasPrefix(shard.Name, "hooks_policy") ||
			strings.HasPrefix(shard.Name, "evidence_investigation") ||
			strings.HasPrefix(shard.Name, "memory_context") ||
			strings.HasPrefix(shard.Name, "ui_viewer") ||
			strings.HasPrefix(shard.Name, "commands") {
			return true
		}
	}
	return false
}

func clusterName(dirs []string) string {
	if len(dirs) == 0 {
		return "root"
	}
	names := append([]string(nil), dirs...)
	sort.Strings(names)
	if len(names) == 1 {
		return shardName(names[0], 0, 1)
	}
	return shardName(names[0], 0, 1) + "_cluster"
}

func clusterLines(dirLines map[string]int, dirs []string) int {
	total := 0
	for _, dir := range dirs {
		total += dirLines[dir]
	}
	return total
}

func shardName(dir string, index int, total int) string {
	name := dir
	if strings.TrimSpace(name) == "" {
		name = "root"
	}
	if total > 1 {
		return fmt.Sprintf("%s-%d", name, index+1)
	}
	return name
}

func filesToPaths(files []ScannedFile) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Path)
	}
	return out
}

func sumLines(files []ScannedFile) int {
	total := 0
	for _, file := range files {
		total += file.LineCount
	}
	return total
}

func analysisUniqueStrings(items []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func filterEvidence(items []string, shard AnalysisShard) []string {
	allowed := map[string]string{}
	for _, item := range shard.PrimaryFiles {
		rememberAllowedEvidencePath(allowed, item)
	}
	for _, item := range shard.ReferenceFiles {
		rememberAllowedEvidencePath(allowed, item)
	}
	out := []string{}
	for _, item := range items {
		if canonical := canonicalEvidencePath(item, allowed); canonical != "" {
			out = append(out, canonical)
		}
	}
	return out
}

func rememberAllowedEvidencePath(allowed map[string]string, path string) {
	clean := cleanEvidencePath(path)
	if clean == "" {
		return
	}
	for _, key := range []string{
		strings.ToLower(clean),
		strings.ToLower(filepath.ToSlash(filepath.Base(clean))),
	} {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if existing, ok := allowed[key]; ok && existing != clean {
			allowed[key] = ""
			continue
		}
		allowed[key] = clean
	}
}

func canonicalEvidencePath(path string, allowed map[string]string) string {
	clean := cleanEvidencePath(path)
	if clean == "" {
		return ""
	}
	for _, key := range []string{
		strings.ToLower(clean),
		strings.ToLower(filepath.ToSlash(filepath.Base(clean))),
	} {
		if canonical, ok := allowed[key]; ok && canonical != "" {
			return canonical
		}
	}
	return ""
}

func cleanEvidencePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if idx := strings.Index(path, " ("); idx > 0 {
		path = strings.TrimSpace(path[:idx])
	}
	path = strings.Trim(path, "`\"'")
	path = filepath.ToSlash(path)
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	return path
}

func joinListForPrompt(items []string) string {
	if len(items) == 0 {
		return "- (none)"
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, "- "+item)
	}
	return strings.Join(lines, "\n")
}

func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func extractQuotedImport(text string) []string {
	out := []string{}
	inQuote := false
	quote := byte(0)
	start := 0
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if !inQuote {
			if ch == '"' || ch == '\'' || ch == '`' {
				inQuote = true
				quote = ch
				start = i + 1
			}
			continue
		}
		if ch == quote {
			token := strings.TrimSpace(text[start:i])
			if token != "" {
				out = append(out, token)
			}
			inQuote = false
			quote = 0
		}
	}
	return out
}

func detectGoModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range splitLines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "module "))
		}
	}
	return ""
}

func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	count := strings.Count(string(data), "\n")
	if !strings.HasSuffix(string(data), "\n") {
		count++
	}
	return count
}

func sanitizeFileName(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	text = replacer.Replace(text)

	var b strings.Builder
	lastUnderscore := false
	for _, r := range text {
		switch {
		case unicode.IsControl(r):
			continue
		case unicode.IsSpace(r):
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		case r == '.':
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		case r == '_':
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		default:
			if !utf8.ValidRune(r) || r == utf8.RuneError {
				continue
			}
			b.WriteRune(unicode.ToLower(r))
			lastUnderscore = false
		}
	}

	cleaned := strings.Trim(b.String(), "._")
	if cleaned == "" {
		return ""
	}
	runes := []rune(cleaned)
	if len(runes) > 48 {
		cleaned = string(runes[:48])
		cleaned = strings.Trim(cleaned, "._")
	}
	return cleaned
}

func ceilDiv(a int, b int) int {
	if a <= 0 || b <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

func analysisMinInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func analysisMaxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
