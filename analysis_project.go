package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type ProjectAnalysisConfig struct {
	Enabled              *bool    `json:"enabled,omitempty"`
	MinAgents            int      `json:"min_agents,omitempty"`
	MaxAgents            int      `json:"max_agents,omitempty"`
	MaxTotalShards       int      `json:"max_total_shards,omitempty"`
	MaxRefinementShards  int      `json:"max_refinement_shards,omitempty"`
	MaxRevisionRounds    int      `json:"max_revision_rounds,omitempty"`
	MaxProviderRetries   int      `json:"max_provider_retries,omitempty"`
	ProviderRetryDelayMs int      `json:"provider_retry_delay_ms,omitempty"`
	MaxFilesPerShard     int      `json:"max_files_per_shard,omitempty"`
	MaxLinesPerShard     int      `json:"max_lines_per_shard,omitempty"`
	ExcludeDirs          []string `json:"exclude_dirs,omitempty"`
	ExcludePaths         []string `json:"exclude_paths,omitempty"`
	OutputDir            string   `json:"output_dir,omitempty"`
	MaxFileBytes         int64    `json:"max_file_bytes,omitempty"`
	WorkerProfile        *Profile `json:"worker_profile,omitempty"`
	ReviewerProfile      *Profile `json:"reviewer_profile,omitempty"`
	Incremental          *bool    `json:"incremental,omitempty"`
}

type ProjectAnalysisSummary struct {
	RunID          string    `json:"run_id"`
	Goal           string    `json:"goal"`
	Status         string    `json:"status"`
	AgentCount     int       `json:"agent_count"`
	OutputPath     string    `json:"output_path"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at"`
	ApprovedShards int       `json:"approved_shards"`
	ReviewFailures int       `json:"review_failures,omitempty"`
	RefinedShards  int       `json:"refined_shards,omitempty"`
	TotalShards    int       `json:"total_shards"`
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
	Root                string                   `json:"root"`
	ModulePath          string                   `json:"module_path,omitempty"`
	GeneratedAt         time.Time                `json:"generated_at"`
	Files               []ScannedFile            `json:"files"`
	Directories         []string                 `json:"directories"`
	ManifestFiles       []string                 `json:"manifest_files"`
	EntrypointFiles     []string                 `json:"entrypoint_files"`
	SolutionProjects    []SolutionProject        `json:"solution_projects,omitempty"`
	StartupProjects     []string                 `json:"startup_projects,omitempty"`
	PrimaryStartup      string                   `json:"primary_startup,omitempty"`
	UnrealProjects      []UnrealProject          `json:"unreal_projects,omitempty"`
	UnrealPlugins       []UnrealPlugin           `json:"unreal_plugins,omitempty"`
	UnrealTargets       []UnrealTarget           `json:"unreal_targets,omitempty"`
	UnrealModules       []UnrealModule           `json:"unreal_modules,omitempty"`
	UnrealTypes         []UnrealReflectedType    `json:"unreal_types,omitempty"`
	UnrealNetwork       []UnrealNetworkSurface   `json:"unreal_network,omitempty"`
	UnrealAssets        []UnrealAssetReference   `json:"unreal_assets,omitempty"`
	UnrealSystems       []UnrealGameplaySystem   `json:"unreal_systems,omitempty"`
	UnrealSettings      []UnrealProjectSetting   `json:"unreal_settings,omitempty"`
	PrimaryUnrealModule string                   `json:"primary_unreal_module,omitempty"`
	AnalysisLenses      []AnalysisLens           `json:"analysis_lenses,omitempty"`
	RuntimeEdges        []RuntimeEdge            `json:"runtime_edges,omitempty"`
	ProjectEdges        []ProjectEdge            `json:"project_edges,omitempty"`
	TotalFiles          int                      `json:"total_files"`
	TotalLines          int                      `json:"total_lines"`
	ImportGraph         map[string][]string      `json:"import_graph"`
	ReverseImportGraph  map[string][]string      `json:"reverse_import_graph"`
	FilesByPath         map[string]ScannedFile   `json:"-"`
	FilesByDirectory    map[string][]ScannedFile `json:"-"`
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
	ShardID          string   `json:"shard_id"`
	Title            string   `json:"title"`
	ScopeSummary     string   `json:"scope_summary"`
	Responsibilities []string `json:"responsibilities"`
	Facts            []string `json:"facts,omitempty"`
	Inferences       []string `json:"inferences,omitempty"`
	KeyFiles         []string `json:"key_files"`
	EntryPoints      []string `json:"entry_points"`
	InternalFlow     []string `json:"internal_flow"`
	Dependencies     []string `json:"dependencies"`
	Collaboration    []string `json:"collaboration"`
	Risks            []string `json:"risks"`
	Unknowns         []string `json:"unknowns"`
	EvidenceFiles    []string `json:"evidence_files"`
	Narrative        string   `json:"narrative"`
	Raw              string   `json:"raw,omitempty"`
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
}

type ReviewDecision struct {
	Status         string   `json:"status"`
	Issues         []string `json:"issues,omitempty"`
	RevisionPrompt string   `json:"revision_prompt,omitempty"`
	Raw            string   `json:"raw,omitempty"`
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
	KnowledgePack    KnowledgePack           `json:"knowledge_pack,omitempty"`
	SemanticIndex    SemanticIndex           `json:"semantic_index,omitempty"`
	UnrealGraph      UnrealSemanticGraph     `json:"unreal_graph,omitempty"`
	VectorCorpus     VectorCorpus            `json:"vector_corpus,omitempty"`
	VectorIngestion  VectorIngestionManifest `json:"vector_ingestion,omitempty"`
	DebugEvents      []string                `json:"debug_events,omitempty"`
	ShardDocuments   map[string]string       `json:"shard_documents,omitempty"`
}

type projectAnalyzer struct {
	cfg            Config
	analysisCfg    ProjectAnalysisConfig
	client         ProviderClient
	workerClient   ProviderClient
	reviewerClient ProviderClient
	workspace      Workspace
	onStatus       func(string)
	onDebug        func(string)
	debugMu        sync.Mutex
	debugEvents    []string
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
		ExcludeDirs:          []string{".git", ".svn", ".hg", ".claude", ".kernforge", "node_modules", "vendor", "third_party", "dist", "build", "out", "bin", "obj", "tmp", "temp"},
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
	if cfg.ProjectAnalysis.MaxProviderRetries > 0 {
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
	if out.MinAgents < 2 {
		out.MinAgents = 2
	}
	if out.MaxAgents < out.MinAgents {
		out.MaxAgents = out.MinAgents
	}
	if out.MaxAgents > 16 {
		out.MaxAgents = 16
	}
	if out.MaxRefinementShards < 0 {
		out.MaxRefinementShards = 0
	}
	if !filepath.IsAbs(out.OutputDir) {
		out.OutputDir = filepath.Clean(filepath.Join(cwd, out.OutputDir))
	}
	return out
}

func newProjectAnalyzer(cfg Config, client ProviderClient, ws Workspace, onStatus func(string), onDebug func(string)) *projectAnalyzer {
	return &projectAnalyzer{
		cfg:         cfg,
		analysisCfg: configProjectAnalysis(cfg, ws.BaseRoot),
		client:      client,
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

func (a *projectAnalyzer) Run(ctx context.Context, goal string) (ProjectAnalysisRun, error) {
	run := ProjectAnalysisRun{}
	a.debugMu.Lock()
	a.debugEvents = nil
	a.debugMu.Unlock()
	run.Summary.RunID = time.Now().Format("20060102-150405")
	run.Summary.Goal = strings.TrimSpace(goal)
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
	snapshot.AnalysisLenses = refineAnalysisLensesForSnapshot(snapshot, chooseAnalysisLenses(goal))
	a.scoreFileImportance(&snapshot, snapshot.AnalysisLenses)
	snapshot.ProjectEdges = buildProjectEdges(snapshot)
	run.Snapshot = snapshot

	agentCount := a.estimateAgentCount(snapshot)
	targetShards := a.estimateShardCount(snapshot, agentCount)
	shards := a.planShards(snapshot, targetShards)
	scope, scopedShards := deriveScopedAnalysisShards(goal, snapshot, shards)
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
	if agentCount > len(shards) {
		agentCount = len(shards)
	}
	if agentCount < 1 {
		agentCount = 1
	}
	run.Summary.AgentCount = agentCount
	run.Summary.TotalShards = len(shards)
	run.Shards = shards

	previousRun, _ := a.loadPreviousRun(goal)
	if previousRun != nil {
		a.status("Loaded previous analysis for incremental reuse.")
		a.debug(fmt.Sprintf("loaded previous analysis run: shards=%d approved=%d", len(previousRun.Shards), previousRun.Summary.ApprovedShards))
	}

	a.status(fmt.Sprintf("Running %d sub-agent(s)...", len(shards)))
	reuseState := a.buildReuseState(previousRun, shards)
	reports, reviews, err := a.executeShards(ctx, snapshot, shards, goal, previousRun, reuseState)
	if err != nil {
		return run, err
	}
	refinementShards, replacedShardIDs := a.planRefinementShards(snapshot, shards, reports, reviews)
	if len(refinementShards) > 0 {
		run.Summary.RefinedShards = len(refinementShards)
		a.status(fmt.Sprintf("Refining %d high-value sub-agent shard(s)...", len(refinementShards)))
		a.debug(fmt.Sprintf("stage-2 refinement planned: shards=%d parents=%d", len(refinementShards), len(replacedShardIDs)))
		refinedReports, refinedReviews, err := a.executeShards(ctx, snapshot, refinementShards, goal, previousRun, analysisReuseState{})
		if err != nil {
			return run, err
		}
		shards, reports, reviews = mergeRefinedShardResults(shards, reports, reviews, refinementShards, refinedReports, refinedReviews, replacedShardIDs)
	}
	run.Shards = shards
	run.Reports = reports
	run.Reviews = reviews
	run.Summary.TotalShards = len(shards)
	for _, review := range run.Reviews {
		if strings.EqualFold(review.Status, "approved") {
			run.Summary.ApprovedShards++
		}
		if strings.EqualFold(review.Status, "review_failed") {
			run.Summary.ReviewFailures++
		}
	}
	approvalRatio := 0.0
	if run.Summary.TotalShards > 0 {
		approvalRatio = float64(run.Summary.ApprovedShards) / float64(run.Summary.TotalShards)
	}
	a.debug(fmt.Sprintf("review summary: approved=%d total=%d ratio=%.2f refined=%d", run.Summary.ApprovedShards, run.Summary.TotalShards, approvalRatio, run.Summary.RefinedShards))

	a.status("Writing final document...")
	document, err := a.synthesizeFinalDocument(ctx, snapshot, shards, reports, goal)
	if err != nil {
		return run, err
	}
	if run.Summary.ApprovedShards == 0 {
		document = "# Draft Analysis\n\nNo shard report was approved by the reviewer. The generated analysis should be treated as low confidence and rerun after fixing worker/reviewer issues.\n\n" + document
		run.Summary.Status = "draft"
		a.debug("analysis downgraded to draft because no shard was approved")
	} else if run.Summary.ReviewFailures > 0 {
		run.Summary.Status = "completed_with_review_failures"
		document = "# Analysis With Review Failures\n\nSome shard reviews timed out or failed at the provider layer. The document was synthesized from available worker reports and approved shards, but the affected shard sections should be treated with reduced confidence.\n\n" + document
		a.debug(fmt.Sprintf("analysis completed with review failures: %d", run.Summary.ReviewFailures))
	}
	run.FinalDocument = document
	run.ShardDocuments = buildShardDocuments(run.Snapshot, run.Shards, run.Reports, goal)
	run.KnowledgePack = buildKnowledgePack(run.Snapshot, run.Shards, run.Reports, goal, run.Summary.RunID)
	run.UnrealGraph = buildUnrealSemanticGraph(run.Snapshot, goal, run.Summary.RunID)
	run.SemanticIndex = buildSemanticIndex(run.Snapshot, goal, run.Summary.RunID, run.UnrealGraph)
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

	outputPath, err = a.persistRun(run)
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
	sort.Strings(snapshot.Directories)
	sort.Strings(snapshot.ManifestFiles)
	sort.Strings(snapshot.EntrypointFiles)
	sort.Strings(snapshot.StartupProjects)
	return snapshot, nil
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
	if count < 2 {
		count = 2
	}
	return count
}

func (a *projectAnalyzer) estimateShardCount(snapshot ProjectSnapshot, concurrentAgents int) int {
	count := concurrentAgents
	count = analysisMaxInt(count, ceilDiv(snapshot.TotalFiles, 120))
	count = analysisMaxInt(count, ceilDiv(snapshot.TotalLines, 15000))
	count = analysisMaxInt(count, ceilDiv(len(snapshot.Directories), 2))
	if count < 2 {
		count = 2
	}
	if a.analysisCfg.MaxTotalShards > 0 && count > a.analysisCfg.MaxTotalShards {
		count = a.analysisCfg.MaxTotalShards
	}
	return count
}

func chooseAnalysisLenses(goal string) []AnalysisLens {
	lower := strings.ToLower(strings.TrimSpace(goal))
	lenses := []AnalysisLens{
		{
			Type:            "architecture",
			PrioritySignals: []string{"entrypoint", "module", "manager", "service", "worker", "dispatcher"},
			OutputFocus:     []string{"module map", "ownership", "execution chain"},
		},
	}
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
		projectPath := filepath.ToSlash(strings.TrimSpace(fields[1]))
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
				shards = mergeShards(shards, maxTotalShards)
			}
			sort.Slice(shards, func(i int, j int) bool {
				return shards[i].ID < shards[j].ID
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

func (a *projectAnalyzer) executeShards(ctx context.Context, snapshot ProjectSnapshot, shards []AnalysisShard, goal string, previousRun *ProjectAnalysisRun, reuseState analysisReuseState) ([]WorkerReport, []ReviewDecision, error) {
	reports := make([]WorkerReport, len(shards))
	reviews := make([]ReviewDecision, len(shards))
	concurrency := analysisMinInt(len(shards), a.analysisCfg.MaxAgents)
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
			return WorkerReport{}, ReviewDecision{}, shard, err
		}
		a.debug(fmt.Sprintf("worker done: shard=%s attempt=%d evidence=%d responsibilities=%d", shard.Name, attempt+1, len(report.EvidenceFiles), len(report.Responsibilities)))
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
		revisionPrompt = strings.TrimSpace(review.RevisionPrompt)
		if revisionPrompt == "" {
			a.debug(fmt.Sprintf("shard %s review requested revision without prompt; stopping retries", shard.Name))
			break
		}
		a.debug(fmt.Sprintf("shard revision requested: %s", shard.Name))
	}
	return lastReport, lastReview, shard, nil
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

func (a *projectAnalyzer) runWorker(ctx context.Context, snapshot ProjectSnapshot, shard AnalysisShard, goal string, revisionPrompt string) (WorkerReport, error) {
	resp, err := a.completeAnalysisRequestWithRetry(ctx, a.workerOrDefaultClient(), "worker", shard.Name, a.workerModel(), ChatRequest{
		Model:       a.workerModel(),
		System:      workerSystemPrompt(),
		Messages:    []Message{{Role: "user", Text: buildWorkerPrompt(snapshot, shard, goal, revisionPrompt)}},
		MaxTokens:   a.cfg.MaxTokens,
		Temperature: a.cfg.Temperature,
		WorkingDir:  a.workspace.Root,
	})
	if err != nil {
		return WorkerReport{}, fmt.Errorf("analysis worker request failed for shard=%s model=%s: %w", shard.Name, a.workerModel(), err)
	}

	raw := strings.TrimSpace(resp.Message.Text)
	report, ok := parseWorkerReportPayload(raw, shard)
	if !ok {
		a.debug(fmt.Sprintf("worker returned non-JSON output for shard=%s; attempting repair", shard.Name))
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
	repairPrompt := strings.TrimSpace(buildWorkerPrompt(snapshot, shard, goal, revisionPrompt) + "\n\nThe previous response was not valid JSON. Reformat the analysis into the required JSON schema only.\n\nPrevious invalid response:\n```\n" + raw + "\n```")
	resp, err := a.completeAnalysisRequestWithRetry(ctx, a.workerOrDefaultClient(), "worker-repair", shard.Name, a.workerModel(), ChatRequest{
		Model:       a.workerModel(),
		System:      workerSystemPrompt(),
		Messages:    []Message{{Role: "user", Text: repairPrompt}},
		MaxTokens:   a.cfg.MaxTokens,
		Temperature: a.cfg.Temperature,
		WorkingDir:  a.workspace.Root,
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
		MaxTokens:   a.cfg.MaxTokens,
		Temperature: a.cfg.Temperature,
		WorkingDir:  a.workspace.Root,
	})
	if err != nil {
		return ReviewDecision{}, fmt.Errorf("analysis reviewer request failed for shard=%s model=%s: %w", shard.Name, a.reviewerModel(), err)
	}

	raw := strings.TrimSpace(resp.Message.Text)
	decision, ok := parseReviewDecisionPayload(raw)
	if !ok {
		return heuristicReviewDecision(report, raw), nil
	}
	return decision, nil
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
		return "", fmt.Errorf("analysis synthesis request failed for model=%s: %w", a.cfg.Model, err)
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
}

func ensureFinalDocumentInsights(text string, snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return trimmed
	}
	items := groupedReportsForSynthesis(shards, reports)
	trimmed = normalizeFinalDocumentHeadings(trimmed)
	trimmed = ensureStartupProjectCoverage(trimmed, snapshot)
	trimmed = ensureExecutionChainCoverage(trimmed, snapshot, reports)
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
	if strings.Contains(strings.ToLower(document), strings.ToLower("primary startup project")) {
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
	b.WriteString("Primary startup project:\n")
	fmt.Fprintf(&b, "- `%s`\n", startup)
	if len(entryFiles) > 0 {
		b.WriteString("Representative startup entry files:\n")
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
	b.WriteString("Startup interpretation:\n")
	fmt.Fprintf(&b, "- The main execution narrative should begin from `%s` and then describe how bootstrap or service modules connect that executable to background monitoring, worker execution, and protection layers.\n", startup)
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
	if hasPathPrefix(shard.PrimaryFiles, "TavernMaster/") || hasPathPrefix(shard.PrimaryFiles, "TavernUpd/") {
		return "Security Control"
	}
	if hasPathPrefix(shard.PrimaryFiles, "TavernWorker/") {
		return "Forensic Analysis"
	}
	if hasPathPrefix(shard.PrimaryFiles, "TavernDart/") {
		return "Protection And Obfuscation"
	}
	if hasPathPrefix(shard.PrimaryFiles, "TavernOtto/") {
		return "Scheduling And Automation"
	}
	if hasPathPrefix(shard.PrimaryFiles, "Tavern/") || hasPathPrefix(shard.PrimaryFiles, "TavernCmn/") {
		return "Core Application"
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
		if strings.Contains(strings.ToLower(report.Title), "runtime") {
			return "Agent Runtime"
		}
		return "Developer Tooling"
	}
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
		resp, err := client.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return ChatResponse{}, err
		}
		if !shouldRetryAnalysisProviderError(err) || attempt == maxRetries {
			return ChatResponse{}, err
		}

		detail := fmt.Sprintf("analysis provider error: stage=%s shard=%s model=%s attempt=%d/%d error=%v", stage, shardName, model, attempt+1, maxRetries+1, err)
		a.debug(strings.TrimSpace(detail))
		delay := baseDelay * time.Duration(attempt+1)
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

func shouldRetryAnalysisProviderError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	retryHints := []string{
		"provider returned error",
		"api error",
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
	base := fmt.Sprintf("%s_%s", run.Summary.RunID, sanitizeFileName(run.Summary.Goal))
	if strings.TrimSpace(base) == "" {
		base = run.Summary.RunID
	}
	mdPath := filepath.Join(a.analysisCfg.OutputDir, base+".md")
	jsonPath := filepath.Join(a.analysisCfg.OutputDir, base+".json")
	knowledgeJSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_knowledge.json")
	knowledgeDigestPath := filepath.Join(a.analysisCfg.OutputDir, base+"_knowledge.md")
	performanceJSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_performance_lens.json")
	performanceDigestPath := filepath.Join(a.analysisCfg.OutputDir, base+"_performance_lens.md")
	snapshotJSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_snapshot.json")
	structuralIndexJSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_structural_index.json")
	unrealGraphJSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_unreal_graph.json")
	vectorCorpusJSONPath := filepath.Join(a.analysisCfg.OutputDir, base+"_vector_corpus.json")
	vectorCorpusJSONLPath := filepath.Join(a.analysisCfg.OutputDir, base+"_vector_corpus.jsonl")
	vectorIngestManifestPath := filepath.Join(a.analysisCfg.OutputDir, base+"_vector_ingest_manifest.json")
	vectorIngestRecordsPath := filepath.Join(a.analysisCfg.OutputDir, base+"_vector_ingest_records.jsonl")
	vectorPGVectorSQLPath := filepath.Join(a.analysisCfg.OutputDir, base+"_vector_pgvector.sql")
	vectorSQLiteSQLPath := filepath.Join(a.analysisCfg.OutputDir, base+"_vector_sqlite.sql")
	vectorQdrantJSONLPath := filepath.Join(a.analysisCfg.OutputDir, base+"_vector_qdrant.jsonl")
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
	if len(run.KnowledgePack.Subsystems) > 0 || strings.TrimSpace(run.KnowledgePack.PrimaryStartup) != "" {
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
		latestDir := filepath.Join(a.analysisCfg.OutputDir, "latest")
		if err := os.MkdirAll(latestDir, 0o755); err != nil {
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

func (a *projectAnalyzer) reviewerModel() string {
	if a.analysisCfg.ReviewerProfile != nil && strings.TrimSpace(a.analysisCfg.ReviewerProfile.Model) != "" {
		return a.analysisCfg.ReviewerProfile.Model
	}
	if a.analysisCfg.WorkerProfile != nil && strings.TrimSpace(a.analysisCfg.WorkerProfile.Model) != "" {
		return a.analysisCfg.WorkerProfile.Model
	}
	return a.cfg.Model
}

func createProviderClientFromProfile(profile Profile, mainCfg Config) (ProviderClient, error) {
	apiKey := profile.APIKey
	if strings.TrimSpace(apiKey) == "" && strings.EqualFold(profile.Provider, mainCfg.Provider) {
		apiKey = mainCfg.APIKey
	}
	cfg := Config{
		Provider: profile.Provider,
		Model:    profile.Model,
		BaseURL:  profile.BaseURL,
		APIKey:   apiKey,
	}
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

func (a *projectAnalyzer) loadPreviousRun(goal string) (*ProjectAnalysisRun, error) {
	if a.analysisCfg.Incremental != nil && !*a.analysisCfg.Incremental {
		return nil, nil
	}
	pattern := filepath.Join(a.analysisCfg.OutputDir, "*_"+sanitizeFileName(goal)+".json")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil, err
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
    "narrative": "string"
  }
}
Rules:
- Analyze the assigned primary files first and use reference files only to explain dependencies.
- Every important claim must be grounded in evidence_files.
- responsibilities should answer what this shard owns.
- facts should contain direct file-grounded observations.
- inferences should contain higher-level interpretations derived from those facts.
- key_files should list the most important files in the shard with file names or short file-role labels.
- internal_flow should describe control flow or data flow inside the shard.
- collaboration should describe how this shard connects to other subsystems.
- The provided file context may be truncated to excerpts. If code is visibly partial, say it is snippet-limited or truncated instead of treating it as an architectural unknown.
- If unsure, put the point in unknowns instead of asserting it as fact, but do not list symbols from the visible file as unknown architectural components when the limitation is just truncated context.
`)
}

func reviewerSystemPrompt() string {
	return strings.TrimSpace(`
You are the conductor reviewing a sub-agent report.
Return strict JSON:
{
  "decision": {
    "status": "approved" | "needs_revision",
    "issues": ["string"],
    "revision_prompt": "string"
  }
}
Approve only if the report is specific, grounded, and suitable for a final architecture document.
Reject reports that are generic, omit control/data flow, or cite evidence files outside the shard scope.
Prefer reports that separate direct facts from higher-level inferences.
When a previous approved report is provided for dependency_changed cases, compare it against the new report and reject stale claims that no longer match the dependency context.
`)
}

func synthesisSystemPrompt() string {
	return strings.TrimSpace(`
You are the conductor writing the final Markdown document.
Use these sections:
1. Project Overview
2. Directory And Module Map
3. Execution Flow And Entry Points
4. Subsystem Breakdown
5. Dependencies And Integration Points
6. Risks And Unknowns
Requirements:
- Use responsibilities to explain ownership boundaries.
- Keep direct facts distinct from higher-level inferences when the reports provide both.
- Use internal_flow to describe actual runtime or data flow.
- Use collaboration to explain subsystem interaction points.
- Consolidate duplicates across shards and call out uncertain areas explicitly.
- Write a detailed document, not a compressed summary.
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
	fmt.Fprintf(&b, "Shard: %s (%s)\n", shard.ID, shard.Name)
	fmt.Fprintf(&b, "Scope rule: primary files are your ownership boundary; reference files are for dependency context only.\n\n")
	if intent := analysisShardIntent(shard.Name); intent != "" {
		fmt.Fprintf(&b, "Shard intent:\n%s\n\n", intent)
	}
	if focus := buildSemanticShardFocus(snapshot, shard); strings.TrimSpace(focus) != "" {
		fmt.Fprintf(&b, "Semantic focus:\n%s\n\n", focus)
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
	b.WriteString("- List concrete responsibilities, not generic statements.\n")
	b.WriteString("- facts should be direct observations grounded in the provided files.\n")
	b.WriteString("- inferences should be clearly labeled interpretations derived from those facts.\n")
	b.WriteString("- key_files should include the files that best explain the subsystem.\n")
	b.WriteString("- entry_points should name files/functions when visible.\n")
	b.WriteString("- internal_flow should explain execution or data flow in steps.\n")
	b.WriteString("- collaboration should mention external subsystems or shared files.\n")
	b.WriteString("- If file excerpts appear truncated, say the analysis is snippet-limited or context-truncated instead of calling visible handlers or symbols architectural unknowns.\n")
	b.WriteString("- Do not cite files outside the provided primary/reference lists.\n\n")
	if strings.HasPrefix(shard.Name, "startup") {
		b.WriteString("- For startup shards, emphasize bootstrap order, target/module ownership, and early runtime handoff.\n")
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

func buildReviewerPrompt(snapshot ProjectSnapshot, shard AnalysisShard, report WorkerReport, goal string, previousReport WorkerReport, hasPreviousReport bool) string {
	data, _ := json.MarshalIndent(report, "", "  ")
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n", goal)
	fmt.Fprintf(&b, "Shard: %s (%s)\n", shard.ID, shard.Name)
	fmt.Fprintf(&b, "Shard cache status: %s\n", shard.CacheStatus)
	if strings.TrimSpace(shard.InvalidationReason) != "" {
		fmt.Fprintf(&b, "Invalidation reason: %s\n", describeAnalysisInvalidationReasonWithContext(shard.InvalidationReason, []string{shard.Name}))
	}
	if intent := analysisShardIntent(shard.Name); intent != "" {
		fmt.Fprintf(&b, "Shard intent:\n%s\n", intent)
	}
	if focus := buildSemanticShardFocus(snapshot, shard); strings.TrimSpace(focus) != "" {
		fmt.Fprintf(&b, "Semantic focus:\n%s\n", focus)
	}
	if reviewRules := buildSemanticReviewerChecklist(shard.Name); strings.TrimSpace(reviewRules) != "" {
		fmt.Fprintf(&b, "Semantic review checklist:\n%s\n", reviewRules)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Assigned files:\n%s\n\n", joinListForPrompt(shard.PrimaryFiles))
	if len(shard.ReferenceFiles) > 0 {
		fmt.Fprintf(&b, "Allowed reference files:\n%s\n\n", joinListForPrompt(shard.ReferenceFiles))
	}
	if hasPreviousReport && (shard.InvalidationReason == "dependency_changed" || shard.InvalidationReason == "semantic_dependency_changed") {
		previousJSON, _ := json.MarshalIndent(previousReport, "", "  ")
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

func buildSynthesisPrompt(snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, goal string) string {
	items := groupedReportsForSynthesis(shards, reports)
	data, _ := json.MarshalIndent(items, "", "  ")
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n", goal)
	fmt.Fprintf(&b, "Workspace: %s\n", snapshot.Root)
	if len(snapshot.AnalysisLenses) > 0 {
		lensNames := []string{}
		for _, lens := range snapshot.AnalysisLenses {
			lensNames = append(lensNames, lens.Type)
		}
		fmt.Fprintf(&b, "Analysis lenses: %s\n", strings.Join(lensNames, ", "))
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
		fmt.Fprintf(&b, "Inferred primary startup project: %s\n\n", snapshot.PrimaryStartup)
		startupEntries := startupProjectEntryFiles(snapshot)
		if len(startupEntries) > 0 {
			fmt.Fprintf(&b, "Primary startup entry files:\n%s\n\n", joinListForPrompt(startupEntries))
		}
	}
	if len(snapshot.EntrypointFiles) > 0 {
		fmt.Fprintf(&b, "Entrypoint files:\n%s\n\n", joinListForPrompt(snapshot.EntrypointFiles))
	}
	b.WriteString("Approved shard reports:\n")
	b.Write(data)
	b.WriteString("\n\nSynthesis requirements:\n")
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
			fmt.Fprintf(&b, "- Explicitly state that the inferred primary startup project is %s and relate bootstrap/service modules to that startup binary.\n", snapshot.PrimaryStartup)
			b.WriteString("- In Project Overview and Execution Flow, describe the primary startup binary first, then explain how helper executables, DLL bootstrap layers, and background worker modules connect to it.\n")
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

func fallbackWorkerReport(shard AnalysisShard, raw string) WorkerReport {
	return WorkerReport{
		ShardID:       shard.ID,
		Title:         shard.Name,
		ScopeSummary:  "Worker returned non-JSON output.",
		EvidenceFiles: append([]string(nil), shard.PrimaryFiles...),
		Narrative:     strings.TrimSpace(raw),
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
			envelope.Report.ShardID = shard.ID
			envelope.Report.Raw = strings.TrimSpace(raw)
			normalizeWorkerReport(&envelope.Report, shard)
			return envelope.Report, true
		}

		report := WorkerReport{}
		if err := json.Unmarshal([]byte(candidate), &report); err == nil {
			report.ShardID = shard.ID
			report.Raw = strings.TrimSpace(raw)
			normalizeWorkerReport(&report, shard)
			return report, true
		}
	}

	return WorkerReport{}, false
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
			return envelope.Decision, true
		}

		decision := ReviewDecision{}
		if err := json.Unmarshal([]byte(candidate), &decision); err == nil {
			decision.Raw = strings.TrimSpace(raw)
			if strings.TrimSpace(decision.Status) == "" {
				decision.Status = "approved"
			}
			return decision, true
		}
	}

	return ReviewDecision{}, false
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
	report.KeyFiles = analysisUniqueStrings(report.KeyFiles)
	report.EntryPoints = analysisUniqueStrings(report.EntryPoints)
	report.InternalFlow = analysisUniqueStrings(report.InternalFlow)
	report.Dependencies = analysisUniqueStrings(report.Dependencies)
	report.Collaboration = analysisUniqueStrings(report.Collaboration)
	report.Risks = analysisUniqueStrings(report.Risks)
	report.Unknowns = analysisUniqueStrings(report.Unknowns)
	report.EvidenceFiles = analysisUniqueStrings(filterEvidence(report.EvidenceFiles, shard))
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
		Raw:            err.Error(),
	}
}

func fallbackFinalDocument(snapshot ProjectSnapshot, shards []AnalysisShard, reports []WorkerReport, goal string) string {
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
		fmt.Fprintf(&b, "- Inferred primary startup project: `%s`\n", snapshot.PrimaryStartup)
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
	allowed := map[string]struct{}{}
	for _, item := range shard.PrimaryFiles {
		allowed[item] = struct{}{}
	}
	for _, item := range shard.ReferenceFiles {
		allowed[item] = struct{}{}
	}
	out := []string{}
	for _, item := range items {
		if _, ok := allowed[strings.TrimSpace(item)]; ok {
			out = append(out, item)
		}
	}
	return out
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
