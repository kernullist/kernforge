package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const userConfigDirName = ".kernforge"

const (
	defaultReadHintSpans    = 48
	defaultReadCacheEntries = 24
	maxProviderRetryDelay   = 30 * time.Second
)

type configSourceKind int

const (
	configSourceUser configSourceKind = iota
	configSourceWorkspace
)

// Workspace-local config is repository content. Keep project workflow settings
// there, but do not let it choose credential destinations or host-local
// executables. User config and environment variables still own those fields.
var workspaceLocalConfigDenylist = []string{
	"provider",
	"base_url",
	"api_key",
	"provider_keys",
	"codex_cli_path",
	"codex_cli_args",
	"claude_cli_path",
	"claude_cli_args",
	"permission_mode",
	"shell",
	"session_dir",
	"mcp_servers",
	"active_profile_key",
	"model_routes",
	"projects",
	"hooks_enabled",
	"hook_presets",
	"hooks_fail_closed",
}

type ReviewModelConfig struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	BaseURL         string `json:"base_url,omitempty"`
	APIKey          string `json:"api_key,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type SpecialistSubagentProfile struct {
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	Prompt          string   `json:"prompt,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	Model           string   `json:"model,omitempty"`
	BaseURL         string   `json:"base_url,omitempty"`
	APIKey          string   `json:"api_key,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	NodeKinds       []string `json:"node_kinds,omitempty"`
	Keywords        []string `json:"keywords,omitempty"`
	ReadOnly        *bool    `json:"read_only,omitempty"`
	Editable        *bool    `json:"editable,omitempty"`
	OwnershipPaths  []string `json:"ownership_paths,omitempty"`
}

type SpecialistSubagentsConfig struct {
	Enabled  *bool                       `json:"enabled,omitempty"`
	Profiles []SpecialistSubagentProfile `json:"profiles,omitempty"`
}

type WorktreeIsolationConfig struct {
	Enabled                *bool  `json:"enabled,omitempty"`
	RootDir                string `json:"root_dir,omitempty"`
	BranchPrefix           string `json:"branch_prefix,omitempty"`
	AutoForTrackedFeatures *bool  `json:"auto_for_tracked_features,omitempty"`
}

type ProjectTrustConfig struct {
	TrustLevel string `json:"trust_level,omitempty"`
}

type ForcedChatGPTWorkspaceIDs []string

func (ids *ForcedChatGPTWorkspaceIDs) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*ids = nil
		return nil
	}
	var single string
	if err := json.Unmarshal(trimmed, &single); err == nil {
		if strings.Contains(single, ",") {
			return fmt.Errorf("forced_chatgpt_workspace_id must be a single workspace ID string or a JSON array of strings; comma-separated strings are not supported")
		}
		*ids = normalizeForcedChatGPTWorkspaceIDs([]string{single})
		return nil
	}
	var multiple []string
	if err := json.Unmarshal(trimmed, &multiple); err != nil {
		return fmt.Errorf("forced_chatgpt_workspace_id must be a string or an array of strings")
	}
	*ids = normalizeForcedChatGPTWorkspaceIDs(multiple)
	return nil
}

func (ids ForcedChatGPTWorkspaceIDs) MarshalJSON() ([]byte, error) {
	normalized := normalizeForcedChatGPTWorkspaceIDs(ids)
	if len(normalized) == 1 {
		return json.Marshal(normalized[0])
	}
	return json.Marshal(normalized)
}

func normalizeForcedChatGPTWorkspaceIDs(ids []string) ForcedChatGPTWorkspaceIDs {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return ForcedChatGPTWorkspaceIDs(out)
}

func forcedChatGPTWorkspaceIDsDisplay(ids []string) string {
	normalized := normalizeForcedChatGPTWorkspaceIDs(ids)
	if len(normalized) == 0 {
		return ""
	}
	return strings.Join(normalized, ", ")
}

func mergeOpaqueConfigMaps(base map[string]any, overlay map[string]any) map[string]any {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	merged := map[string]any{}
	for key, value := range base {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		merged[key] = cloneOpaqueConfigValue(value)
	}
	for key, value := range overlay {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if existing, ok := merged[key].(map[string]any); ok {
			if next, ok := value.(map[string]any); ok {
				merged[key] = mergeOpaqueConfigMaps(existing, next)
				continue
			}
		}
		merged[key] = cloneOpaqueConfigValue(value)
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func cloneOpaqueConfigValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return mergeOpaqueConfigMaps(nil, typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, cloneOpaqueConfigValue(item))
		}
		return out
	default:
		return typed
	}
}

type Config struct {
	Provider                 string                        `json:"provider"`
	Model                    string                        `json:"model"`
	BaseURL                  string                        `json:"base_url"`
	APIKey                   string                        `json:"api_key"`
	ProviderKeys             map[string]string             `json:"provider_keys,omitempty"`
	CodexCLIPath             string                        `json:"codex_cli_path,omitempty"`
	CodexCLIArgs             []string                      `json:"codex_cli_args,omitempty"`
	ClaudeCLIPath            string                        `json:"claude_cli_path,omitempty"`
	ClaudeCLIArgs            []string                      `json:"claude_cli_args,omitempty"`
	ForcedChatGPTWorkspaceID ForcedChatGPTWorkspaceIDs     `json:"forced_chatgpt_workspace_id,omitempty"`
	Temperature              float64                       `json:"temperature"`
	ReasoningEffort          string                        `json:"reasoning_effort,omitempty"`
	MaxTokens                int                           `json:"max_tokens"`
	MaxToolIterations        int                           `json:"max_tool_iterations"`
	MaxRequestRetries        int                           `json:"max_request_retries,omitempty"`
	RequestRetryDelayMs      int                           `json:"request_retry_delay_ms,omitempty"`
	RequestTimeoutSecs       int                           `json:"request_timeout_seconds,omitempty"`
	ProgressDisplay          string                        `json:"progress_display,omitempty"`
	ModelRoutes              ModelRouteSchedulerConfig     `json:"model_routes,omitempty"`
	ShellTimeoutSecs         int                           `json:"shell_timeout_seconds,omitempty"`
	ReadHintSpans            int                           `json:"read_hint_spans,omitempty"`
	ReadCacheEntries         int                           `json:"read_cache_entries,omitempty"`
	MSBuildPath              string                        `json:"msbuild_path,omitempty"`
	CMakePath                string                        `json:"cmake_path,omitempty"`
	CTestPath                string                        `json:"ctest_path,omitempty"`
	NinjaPath                string                        `json:"ninja_path,omitempty"`
	Command                  string                        `json:"command,omitempty"`
	PermissionMode           string                        `json:"permission_mode"`
	Shell                    string                        `json:"shell"`
	SessionDir               string                        `json:"session_dir"`
	AutoCompactChars         int                           `json:"auto_compact_chars"`
	AutoCheckpointEdits      *bool                         `json:"auto_checkpoint_edits,omitempty"`
	AutoVerify               *bool                         `json:"auto_verify,omitempty"`
	AutoLocale               *bool                         `json:"auto_locale,omitempty"`
	FuzzFuncOutputLanguage   string                        `json:"fuzz_func_output_language,omitempty"`
	HooksEnabled             *bool                         `json:"hooks_enabled,omitempty"`
	HookPresets              []string                      `json:"hook_presets,omitempty"`
	HooksFailClosed          *bool                         `json:"hooks_fail_closed,omitempty"`
	BypassHookTrust          bool                          `json:"-"`
	MemoryFiles              []string                      `json:"memory_files"`
	SkillPaths               []string                      `json:"skill_paths,omitempty"`
	EnabledSkills            []string                      `json:"enabled_skills,omitempty"`
	MCPServers               []MCPServerConfig             `json:"mcp_servers,omitempty"`
	Profiles                 []Profile                     `json:"profiles,omitempty"`
	ActiveProfileKey         string                        `json:"active_profile_key,omitempty"`
	Projects                 map[string]ProjectTrustConfig `json:"projects,omitempty"`
	ProjectAnalysis          ProjectAnalysisConfig         `json:"project_analysis,omitempty"`
	Review                   ReviewHarnessConfig           `json:"review,omitempty"`
	Specialists              SpecialistSubagentsConfig     `json:"specialists,omitempty"`
	WorktreeIsolation        WorktreeIsolationConfig       `json:"worktree_isolation,omitempty"`
	Desktop                  map[string]any                `json:"desktop,omitempty"`
}

type ConfigLoadOptions struct {
	StrictConfig bool
}

type Profile struct {
	Name            string             `json:"name"`
	Provider        string             `json:"provider"`
	Model           string             `json:"model"`
	BaseURL         string             `json:"base_url,omitempty"`
	APIKey          string             `json:"api_key,omitempty"`
	ReasoningEffort string             `json:"reasoning_effort,omitempty"`
	Pinned          bool               `json:"pinned,omitempty"`
	RoleModels      *ProfileRoleModels `json:"role_models,omitempty"`
}

type ProfileRoleModels struct {
	AnalysisWorker   *Profile                    `json:"analysis_worker,omitempty"`
	AnalysisReviewer *Profile                    `json:"analysis_reviewer,omitempty"`
	Specialists      []SpecialistSubagentProfile `json:"specialists,omitempty"`
}

func DefaultConfig(cwd string) Config {
	return Config{
		Provider:               "",
		Model:                  "",
		Temperature:            0.2,
		MaxTokens:              8192,
		MaxToolIterations:      0,
		MaxRequestRetries:      2,
		RequestRetryDelayMs:    1500,
		RequestTimeoutSecs:     1200,
		ProgressDisplay:        "stream",
		ShellTimeoutSecs:       currentDefaultShellTimeoutSecs,
		ReadHintSpans:          defaultReadHintSpans,
		ReadCacheEntries:       defaultReadCacheEntries,
		PermissionMode:         "default",
		Shell:                  defaultShell(),
		SessionDir:             filepath.Join(userConfigDir(), "sessions"),
		AutoCompactChars:       45000,
		AutoCheckpointEdits:    boolPtr(false),
		AutoVerify:             boolPtr(true),
		AutoLocale:             boolPtr(true),
		FuzzFuncOutputLanguage: "english",
		HooksEnabled:           boolPtr(true),
		HooksFailClosed:        boolPtr(false),
		Review: ReviewHarnessConfig{
			AutoAfterChange:               boolPtr(true),
			AutoAfterGoalIteration:        boolPtr(true),
			AutoBeforeGitWrite:            boolPtr(true),
			AutoFollowUp:                  "safe",
			AutoRepairMaxRounds:           2,
			RepeatedFindingBlockThreshold: 2,
		},
		Specialists: SpecialistSubagentsConfig{
			Enabled: boolPtr(true),
		},
		WorktreeIsolation: WorktreeIsolationConfig{
			Enabled:                boolPtr(false),
			RootDir:                filepath.Join(userConfigDir(), "worktrees"),
			BranchPrefix:           "kernforge/",
			AutoForTrackedFeatures: boolPtr(true),
		},
	}
}

func LoadConfig(cwd string) (Config, error) {
	return LoadConfigWithOptions(cwd, ConfigLoadOptions{
		StrictConfig: strictConfigEnvEnabled(),
	})
}

func LoadConfigWithOptions(cwd string, options ConfigLoadOptions) (Config, error) {
	cfg := DefaultConfig(cwd)
	for index, path := range configSearchPaths(cwd) {
		source := configSourceUser
		if index > 0 {
			source = configSourceWorkspace
		}
		if source == configSourceWorkspace && !configProjectTrusted(cfg, cwd) {
			continue
		}
		if err := mergeConfigFileForSourceWithOptions(&cfg, path, source, options); err != nil {
			return cfg, err
		}
	}
	applyEnv(&cfg)
	normalizeConfigPaths(&cfg)
	applyActiveProfileRoleModels(&cfg)
	applyReasoningEffortEnvOverride(&cfg)
	if err := normalizeConfigPermissionMode(&cfg); err != nil {
		return cfg, err
	}
	if err := EnsureUserConfig(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func strictConfigEnvEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("KERNFORGE_STRICT_CONFIG"))
	if raw == "" {
		return false
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func applyReasoningEffortEnvOverride(cfg *Config) {
	if cfg == nil {
		return
	}
	if raw := strings.TrimSpace(os.Getenv("KERNFORGE_REASONING_EFFORT")); raw != "" {
		cfg.ReasoningEffort = normalizeReasoningEffort(raw)
	}
}

// LegacyDefaultMigration captures one config field whose stored value matched
// a previous KernForge hard-coded default and was rewritten to the current
// default. Surfaced to the caller so it can print a one-time INFO notice.
type LegacyDefaultMigration struct {
	Field    string // human-readable field name (e.g. "max_tool_iterations")
	OldValue string // value previously stored on disk
	NewValue string // value written back after migration
	Reason   string // why the migration happened
}

// MigrateLegacyConfigDefaults rewrites config values that exactly match the
// previous hard-coded defaults of KernForge (max_tool_iterations: 16,
// max_tokens: 4096, shell_timeout_seconds: 300) to the new defaults. Both the in-memory cfg and any
// on-disk config file in the search path are patched so the migration is
// sticky — the user only sees the notice once.
//
// We migrate only when the value matches the old default exactly: a user who
// happened to pick those numbers intentionally is statistically rare, and
// even if it happens they will see the INFO message and can re-pin them.
func MigrateLegacyConfigDefaults(cwd string, cfg *Config) []LegacyDefaultMigration {
	if cfg == nil {
		return nil
	}
	var notices []LegacyDefaultMigration

	if cfg.MaxToolIterations == legacyDefaultMaxToolIterations {
		notices = append(notices, LegacyDefaultMigration{
			Field:    "max_tool_iterations",
			OldValue: fmt.Sprintf("%d", cfg.MaxToolIterations),
			NewValue: "unlimited",
			Reason:   "matched the previous KernForge default; tool-loop is now uncapped to support multi-file analysis and reasoning models.",
		})
		cfg.MaxToolIterations = 0
	}
	if cfg.MaxTokens == legacyDefaultMaxTokens {
		notices = append(notices, LegacyDefaultMigration{
			Field:    "max_tokens",
			OldValue: fmt.Sprintf("%d", cfg.MaxTokens),
			NewValue: fmt.Sprintf("%d", currentDefaultMaxTokens),
			Reason:   "matched the previous KernForge default; raised so longer outputs (e.g. documentation, large patches) fit in one response.",
		})
		cfg.MaxTokens = currentDefaultMaxTokens
	}
	if cfg.ShellTimeoutSecs == legacyDefaultShellTimeoutSecs {
		notices = append(notices, LegacyDefaultMigration{
			Field:    "shell_timeout_seconds",
			OldValue: fmt.Sprintf("%d", cfg.ShellTimeoutSecs),
			NewValue: fmt.Sprintf("%d", currentDefaultShellTimeoutSecs),
			Reason:   "matched the previous KernForge default; raised so full workspace verification and slow local providers have room to finish.",
		})
		cfg.ShellTimeoutSecs = currentDefaultShellTimeoutSecs
	}

	if len(notices) == 0 {
		return nil
	}
	// Persist the migration to every config file in the search path that
	// still carries the legacy literal — otherwise the next merge would
	// overwrite our in-memory fix and we'd repeat the notice forever.
	for _, path := range configSearchPaths(cwd) {
		_ = patchLegacyDefaultsInFile(path)
	}
	return notices
}

const (
	// Previous hard-coded defaults that we now treat as migration sentinels.
	legacyDefaultMaxToolIterations = 16
	legacyDefaultMaxTokens         = 4096
	legacyDefaultShellTimeoutSecs  = 300

	// New defaults the migration writes back. Keep in sync with DefaultConfig.
	currentDefaultMaxTokens        = 8192
	currentDefaultShellTimeoutSecs = 900
)

// patchLegacyDefaultsInFile reads a config file, replaces legacy default
// values in-place, and writes it back. Missing files and parse errors are
// ignored — migration is best-effort and never blocks startup.
func patchLegacyDefaultsInFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err // includes os.IsNotExist; caller ignores
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	// Use a generic map so we only touch known legacy fields and preserve
	// everything else exactly (key order, comments are already gone after
	// any prior write but unknown fields stay intact).
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	changed := false
	if v, ok := raw["max_tool_iterations"]; ok {
		if asFloat, isNum := v.(float64); isNum && int(asFloat) == legacyDefaultMaxToolIterations {
			raw["max_tool_iterations"] = 0
			changed = true
		}
	}
	if v, ok := raw["max_tokens"]; ok {
		if asFloat, isNum := v.(float64); isNum && int(asFloat) == legacyDefaultMaxTokens {
			raw["max_tokens"] = currentDefaultMaxTokens
			changed = true
		}
	}
	if v, ok := raw["shell_timeout_seconds"]; ok {
		if asFloat, isNum := v.(float64); isNum && int(asFloat) == legacyDefaultShellTimeoutSecs {
			raw["shell_timeout_seconds"] = currentDefaultShellTimeoutSecs
			changed = true
		}
	}
	if !changed {
		return nil
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func configSearchPaths(cwd string) []string {
	return []string{
		userConfigPath(),
		workspaceConfigPath(cwd),
	}
}

func userConfigDir() string {
	return filepath.Join(platformUserConfigBaseDir(), userConfigDirName)
}

func userConfigPath() string {
	return filepath.Join(userConfigDir(), "config.json")
}

func workspaceConfigPath(cwd string) string {
	return filepath.Join(cwd, userConfigDirName, "config.json")
}

func configProjectTrusted(cfg Config, cwd string) bool {
	return strings.EqualFold(projectTrustLevelForPath(cfg, cwd), "trusted")
}

func projectTrustLevelForPath(cfg Config, cwd string) string {
	if len(cfg.Projects) == 0 {
		return ""
	}
	keys := projectTrustCandidateKeys(cwd)
	if len(keys) == 0 {
		return ""
	}
	normalizedProjects := map[string]ProjectTrustConfig{}
	for rawKey, project := range cfg.Projects {
		key := normalizeProjectTrustLookupKey(rawKey)
		if key == "" {
			continue
		}
		normalizedProjects[key] = project
	}
	for _, key := range keys {
		if project, ok := normalizedProjects[key]; ok {
			return normalizeProjectTrustLevel(project.TrustLevel)
		}
	}
	return ""
}

func projectTrustCandidateKeys(cwd string) []string {
	var candidates []string
	if root := findGitProjectRoot(cwd); root != "" {
		candidates = append(candidates, root)
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		candidates = append(candidates, abs)
	} else if strings.TrimSpace(cwd) != "" {
		candidates = append(candidates, cwd)
	}
	seen := map[string]bool{}
	var keys []string
	for _, candidate := range candidates {
		for _, key := range normalizedProjectTrustKeys(candidate) {
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
}

func findGitProjectRoot(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	info, statErr := os.Stat(abs)
	if statErr == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, ".git")); err == nil {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	return ""
}

func normalizedProjectTrustKeys(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	keys := []string{normalizeProjectTrustLookupKey(path)}
	if abs, err := filepath.Abs(path); err == nil {
		keys = append(keys, normalizeProjectTrustLookupKey(abs))
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			keys = append(keys, normalizeProjectTrustLookupKey(resolved))
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, key := range keys {
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

func normalizeProjectTrustLookupKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	key = filepath.Clean(expandHome(key))
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

func normalizeProjectTrustLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trusted":
		return "trusted"
	case "untrusted":
		return "untrusted"
	default:
		return ""
	}
}

func mergeProjectTrustConfigs(dst, src map[string]ProjectTrustConfig) map[string]ProjectTrustConfig {
	if len(dst) == 0 && len(src) == 0 {
		return nil
	}
	merged := map[string]ProjectTrustConfig{}
	for key, project := range dst {
		level := normalizeProjectTrustLevel(project.TrustLevel)
		if level == "" {
			continue
		}
		normalizedKey := normalizeProjectTrustLookupKey(key)
		if normalizedKey == "" {
			continue
		}
		merged[normalizedKey] = ProjectTrustConfig{TrustLevel: level}
	}
	for key, project := range src {
		level := normalizeProjectTrustLevel(project.TrustLevel)
		if level == "" {
			continue
		}
		normalizedKey := normalizeProjectTrustLookupKey(key)
		if normalizedKey == "" {
			continue
		}
		merged[normalizedKey] = ProjectTrustConfig{TrustLevel: level}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func loadUserConfigOnly(cwd string) (Config, error) {
	cfg := DefaultConfig(cwd)
	if err := mergeConfigFileForSource(&cfg, userConfigPath(), configSourceUser); err != nil {
		return cfg, err
	}
	normalizeConfigPaths(&cfg)
	applyActiveProfileRoleModels(&cfg)
	if err := normalizeConfigPermissionMode(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func SaveProjectTrustLevel(cwd string, level string) (string, error) {
	level = normalizeProjectTrustLevel(level)
	if level == "" {
		return "", fmt.Errorf("project trust level must be trusted or untrusted")
	}
	keys := projectTrustCandidateKeys(cwd)
	if len(keys) == 0 {
		return "", fmt.Errorf("could not resolve project trust key for %s", cwd)
	}
	cfg, err := loadUserConfigOnly(cwd)
	if err != nil {
		return "", err
	}
	if cfg.Projects == nil {
		cfg.Projects = map[string]ProjectTrustConfig{}
	}
	for _, key := range keys {
		delete(cfg.Projects, key)
	}
	cfg.Projects[keys[0]] = ProjectTrustConfig{TrustLevel: level}
	if err := SaveUserConfig(cfg); err != nil {
		return "", err
	}
	return keys[0], nil
}

func mergeConfigFile(cfg *Config, path string) error {
	return mergeConfigFileForSource(cfg, path, configSourceUser)
}

func mergeConfigFileForSource(cfg *Config, path string, source configSourceKind) error {
	return mergeConfigFileForSourceWithOptions(cfg, path, source, ConfigLoadOptions{})
}

func mergeConfigFileForSourceWithOptions(cfg *Config, path string, source configSourceKind, options ConfigLoadOptions) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	dataForDecode := sanitizeConfigDataForSource(data, source)
	var patch Config
	if err := decodeConfigPatchJSON(dataForDecode, &patch, options.StrictConfig); err != nil {
		if repaired, ok := repairAppendedMCPSnippetConfigJSON(data); ok {
			repairedForDecode := sanitizeConfigDataForSource(repaired, source)
			if repairErr := decodeConfigPatchJSON(repairedForDecode, &patch, options.StrictConfig); repairErr == nil {
				_ = os.WriteFile(path, repaired, 0o644)
				sanitizeConfigPatchForSource(&patch, source)
				mergeConfig(cfg, patch)
				return nil
			}
		}
		return fmt.Errorf("parse %s: %w%s", path, err, configParseRepairHint(data))
	}
	sanitizeConfigPatchForSource(&patch, source)
	mergeConfig(cfg, patch)
	return nil
}

func decodeConfigPatchJSON(data []byte, patch *Config, strict bool) error {
	if err := json.Unmarshal(data, patch); err != nil {
		return err
	}
	if !strict {
		return nil
	}
	return validateStrictConfigJSON(data, reflect.TypeOf(Config{}))
}

func validateStrictConfigJSON(data []byte, target reflect.Type) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if path, ok := firstUnknownJSONConfigPath(raw, target, nil); ok {
		return fmt.Errorf("unknown configuration field `%s`", strings.Join(path, "."))
	}
	return nil
}

func firstUnknownJSONConfigPath(value any, target reflect.Type, path []string) ([]string, bool) {
	target = unwrapStrictConfigType(target)
	switch target.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		fields := strictConfigJSONFields(target)
		for key, child := range object {
			field, ok := fields[key]
			if !ok {
				return append(append([]string(nil), path...), key), true
			}
			if childPath, ok := firstUnknownJSONConfigPath(child, field.Type, append(path, key)); ok {
				return childPath, true
			}
		}
	case reflect.Map:
		if target.Elem().Kind() == reflect.Interface {
			return nil, false
		}
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		for key, child := range object {
			if childPath, ok := firstUnknownJSONConfigPath(child, target.Elem(), append(path, key)); ok {
				return childPath, true
			}
		}
	case reflect.Slice, reflect.Array:
		items, ok := value.([]any)
		if !ok {
			return nil, false
		}
		for index, child := range items {
			if childPath, ok := firstUnknownJSONConfigPath(child, target.Elem(), append(path, strconv.Itoa(index))); ok {
				return childPath, true
			}
		}
	}
	return nil, false
}

func unwrapStrictConfigType(target reflect.Type) reflect.Type {
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	return target
}

func strictConfigJSONFields(target reflect.Type) map[string]reflect.StructField {
	fields := map[string]reflect.StructField{}
	for i := 0; i < target.NumField(); i++ {
		field := target.Field(i)
		if !field.IsExported() {
			continue
		}
		name := field.Name
		if tag := field.Tag.Get("json"); tag != "" {
			name = strings.Split(tag, ",")[0]
		}
		if name == "-" || name == "" {
			continue
		}
		fields[name] = field
	}
	return fields
}

func sanitizeConfigDataForSource(data []byte, source configSourceKind) []byte {
	if source != configSourceWorkspace {
		return data
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return data
	}
	changed := false
	for _, key := range workspaceLocalConfigDenylist {
		if _, ok := raw[key]; ok {
			delete(raw, key)
			changed = true
		}
	}
	if !changed {
		return data
	}
	sanitized, err := json.Marshal(raw)
	if err != nil {
		return data
	}
	return sanitized
}

func sanitizeConfigPatchForSource(patch *Config, source configSourceKind) {
	if patch == nil || source != configSourceWorkspace {
		return
	}
	patch.Provider = ""
	patch.BaseURL = ""
	patch.APIKey = ""
	patch.ProviderKeys = nil
	patch.CodexCLIPath = ""
	patch.CodexCLIArgs = nil
	patch.ClaudeCLIPath = ""
	patch.ClaudeCLIArgs = nil
	patch.PermissionMode = ""
	patch.Shell = ""
	patch.SessionDir = ""
	patch.MCPServers = nil
	patch.ActiveProfileKey = ""
	patch.ModelRoutes = ModelRouteSchedulerConfig{}
	patch.Projects = nil
	patch.HooksEnabled = nil
	patch.HookPresets = nil
	patch.HooksFailClosed = nil
}

func repairAppendedMCPSnippetConfigJSON(data []byte) ([]byte, bool) {
	text := string(data)
	if !strings.Contains(text, `"mcp_servers"`) {
		return nil, false
	}
	openPattern := regexp.MustCompile(`,\s*\{\s*"mcp_servers"\s*:`)
	if !openPattern.MatchString(text) {
		return nil, false
	}
	repaired := openPattern.ReplaceAllString(text, ","+"\n"+`  "mcp_servers":`)
	closePattern := regexp.MustCompile(`\r?\n\s*\}\r?\n\}\s*$`)
	repaired = closePattern.ReplaceAllString(repaired, "\n}")
	if repaired == text {
		return nil, false
	}
	return []byte(repaired), true
}

func configParseRepairHint(data []byte) string {
	text := string(data)
	if strings.Contains(text, `"mcp_servers"`) && regexp.MustCompile(`,\s*\{\s*"mcp_servers"\s*:`).MatchString(text) {
		return " (config appears to contain an extra top-level object; move mcp_servers inside the root JSON object)"
	}
	return ""
}

func mergeReviewHarnessConfig(dst *ReviewHarnessConfig, src ReviewHarnessConfig) {
	if dst == nil {
		return
	}
	if src.AutoAfterChange != nil {
		value := *src.AutoAfterChange
		dst.AutoAfterChange = &value
	}
	if src.AutoAfterGoalIteration != nil {
		value := *src.AutoAfterGoalIteration
		dst.AutoAfterGoalIteration = &value
	}
	if src.AutoBeforeGitWrite != nil {
		value := *src.AutoBeforeGitWrite
		dst.AutoBeforeGitWrite = &value
	}
	if strings.TrimSpace(src.AutoFollowUp) != "" {
		dst.AutoFollowUp = strings.TrimSpace(src.AutoFollowUp)
	}
	if src.AutoRepairMaxRounds > 0 {
		dst.AutoRepairMaxRounds = src.AutoRepairMaxRounds
	}
	if src.RepeatedFindingBlockThreshold > 0 {
		dst.RepeatedFindingBlockThreshold = src.RepeatedFindingBlockThreshold
	}
	if len(src.RoleModels) > 0 {
		if dst.RoleModels == nil {
			dst.RoleModels = map[string]ReviewModelConfig{}
		}
		for role, model := range src.RoleModels {
			role = normalizeReviewRole(role)
			if role == "" {
				continue
			}
			dst.RoleModels[role] = model
		}
	}
}

func normalizeReviewHarnessConfig(cfg *ReviewHarnessConfig) {
	if cfg == nil {
		return
	}
	if cfg.AutoAfterChange == nil {
		cfg.AutoAfterChange = boolPtr(true)
	}
	if cfg.AutoAfterGoalIteration == nil {
		cfg.AutoAfterGoalIteration = boolPtr(true)
	}
	if cfg.AutoBeforeGitWrite == nil {
		cfg.AutoBeforeGitWrite = boolPtr(true)
	}
	if strings.TrimSpace(cfg.AutoFollowUp) == "" {
		cfg.AutoFollowUp = "safe"
	}
	if cfg.AutoRepairMaxRounds <= 0 {
		cfg.AutoRepairMaxRounds = 2
	}
	if cfg.RepeatedFindingBlockThreshold <= 0 {
		cfg.RepeatedFindingBlockThreshold = 2
	}
	if len(cfg.RoleModels) > 0 {
		normalized := map[string]ReviewModelConfig{}
		for role, model := range cfg.RoleModels {
			role = normalizeReviewRole(role)
			model.Provider = normalizeProviderName(model.Provider)
			model.Model = strings.TrimSpace(model.Model)
			model.BaseURL = normalizeOptionalProfileBaseURL(model.Provider, model.BaseURL)
			model.APIKey = strings.TrimSpace(model.APIKey)
			model.ReasoningEffort = normalizeReasoningEffort(model.ReasoningEffort)
			if role != "" && model.Provider != "" && model.Model != "" {
				normalized[role] = model
			}
		}
		cfg.RoleModels = normalized
	}
}

func mergeConfig(dst *Config, src Config) {
	if src.Provider != "" {
		dst.Provider = src.Provider
	}
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.BaseURL != "" {
		dst.BaseURL = src.BaseURL
	}
	if src.APIKey != "" {
		dst.APIKey = src.APIKey
	}
	if len(src.ProviderKeys) > 0 {
		if dst.ProviderKeys == nil {
			dst.ProviderKeys = make(map[string]string)
		}
		for k, v := range src.ProviderKeys {
			provider := normalizeProviderName(k)
			if provider != "" && v != "" {
				dst.ProviderKeys[provider] = v
			}
		}
	}
	if strings.TrimSpace(src.CodexCLIPath) != "" {
		dst.CodexCLIPath = strings.TrimSpace(src.CodexCLIPath)
	}
	if len(src.CodexCLIArgs) > 0 {
		dst.CodexCLIArgs = append([]string(nil), src.CodexCLIArgs...)
	}
	if strings.TrimSpace(src.ClaudeCLIPath) != "" {
		dst.ClaudeCLIPath = strings.TrimSpace(src.ClaudeCLIPath)
	}
	if len(src.ClaudeCLIArgs) > 0 {
		dst.ClaudeCLIArgs = append([]string(nil), src.ClaudeCLIArgs...)
	}
	if len(src.ForcedChatGPTWorkspaceID) > 0 {
		dst.ForcedChatGPTWorkspaceID = normalizeForcedChatGPTWorkspaceIDs(src.ForcedChatGPTWorkspaceID)
	}
	if src.Temperature != 0 {
		dst.Temperature = src.Temperature
	}
	if strings.TrimSpace(src.ReasoningEffort) != "" {
		dst.ReasoningEffort = normalizeReasoningEffort(src.ReasoningEffort)
	}
	if src.MaxTokens != 0 {
		dst.MaxTokens = src.MaxTokens
	}
	if src.MaxToolIterations != 0 {
		dst.MaxToolIterations = src.MaxToolIterations
	}
	if src.MaxRequestRetries != 0 {
		dst.MaxRequestRetries = src.MaxRequestRetries
	}
	if src.RequestRetryDelayMs != 0 {
		dst.RequestRetryDelayMs = src.RequestRetryDelayMs
	}
	if src.RequestTimeoutSecs != 0 {
		dst.RequestTimeoutSecs = src.RequestTimeoutSecs
	}
	if strings.TrimSpace(src.ProgressDisplay) != "" {
		dst.ProgressDisplay = normalizeProgressDisplay(src.ProgressDisplay)
	}
	if src.ModelRoutes.Enabled != nil {
		value := *src.ModelRoutes.Enabled
		dst.ModelRoutes.Enabled = &value
	}
	if src.ModelRoutes.DefaultMaxConcurrent != 0 {
		dst.ModelRoutes.DefaultMaxConcurrent = src.ModelRoutes.DefaultMaxConcurrent
	}
	if len(src.ModelRoutes.ProviderLimits) > 0 {
		if dst.ModelRoutes.ProviderLimits == nil {
			dst.ModelRoutes.ProviderLimits = map[string]int{}
		}
		for provider, limit := range src.ModelRoutes.ProviderLimits {
			provider = normalizeProviderName(provider)
			if provider != "" && limit > 0 {
				dst.ModelRoutes.ProviderLimits[provider] = limit
			}
		}
	}
	if len(src.ModelRoutes.RouteLimits) > 0 {
		if dst.ModelRoutes.RouteLimits == nil {
			dst.ModelRoutes.RouteLimits = map[string]int{}
		}
		for route, limit := range src.ModelRoutes.RouteLimits {
			route = strings.TrimSpace(route)
			if route != "" && limit > 0 {
				dst.ModelRoutes.RouteLimits[route] = limit
			}
		}
	}
	if src.ShellTimeoutSecs != 0 {
		dst.ShellTimeoutSecs = src.ShellTimeoutSecs
	}
	if src.ReadHintSpans != 0 {
		dst.ReadHintSpans = src.ReadHintSpans
	}
	if src.ReadCacheEntries != 0 {
		dst.ReadCacheEntries = src.ReadCacheEntries
	}
	if src.MSBuildPath != "" {
		dst.MSBuildPath = src.MSBuildPath
	}
	if src.CMakePath != "" {
		dst.CMakePath = src.CMakePath
	}
	if src.CTestPath != "" {
		dst.CTestPath = src.CTestPath
	}
	if src.NinjaPath != "" {
		dst.NinjaPath = src.NinjaPath
	}
	if src.PermissionMode != "" {
		dst.PermissionMode = src.PermissionMode
	}
	if src.Shell != "" {
		dst.Shell = src.Shell
	}
	if src.SessionDir != "" {
		dst.SessionDir = src.SessionDir
	}
	if src.AutoCompactChars != 0 {
		dst.AutoCompactChars = src.AutoCompactChars
	}
	if src.AutoCheckpointEdits != nil {
		value := *src.AutoCheckpointEdits
		dst.AutoCheckpointEdits = &value
	}
	if src.AutoVerify != nil {
		value := *src.AutoVerify
		dst.AutoVerify = &value
	}
	if src.AutoLocale != nil {
		value := *src.AutoLocale
		dst.AutoLocale = &value
	}
	if strings.TrimSpace(src.FuzzFuncOutputLanguage) != "" {
		dst.FuzzFuncOutputLanguage = strings.TrimSpace(src.FuzzFuncOutputLanguage)
	}
	if src.HooksEnabled != nil {
		value := *src.HooksEnabled
		dst.HooksEnabled = &value
	}
	if len(src.HookPresets) > 0 {
		dst.HookPresets = append([]string(nil), src.HookPresets...)
	}
	if src.HooksFailClosed != nil {
		value := *src.HooksFailClosed
		dst.HooksFailClosed = &value
	}
	if len(src.MemoryFiles) > 0 {
		dst.MemoryFiles = append([]string(nil), src.MemoryFiles...)
	}
	if len(src.SkillPaths) > 0 {
		dst.SkillPaths = append([]string(nil), src.SkillPaths...)
	}
	if len(src.EnabledSkills) > 0 {
		dst.EnabledSkills = append([]string(nil), src.EnabledSkills...)
	}
	if len(src.MCPServers) > 0 {
		dst.MCPServers = mergeMCPServerOverrides(dst.MCPServers, src.MCPServers)
	}
	if len(src.Profiles) > 0 {
		dst.Profiles = mergeConfigProfiles(dst.Profiles, src.Profiles)
	}
	if strings.TrimSpace(src.ActiveProfileKey) != "" {
		dst.ActiveProfileKey = strings.TrimSpace(src.ActiveProfileKey)
	}
	if len(src.Projects) > 0 {
		dst.Projects = mergeProjectTrustConfigs(dst.Projects, src.Projects)
	}
	if src.ProjectAnalysis.Enabled != nil {
		value := *src.ProjectAnalysis.Enabled
		dst.ProjectAnalysis.Enabled = &value
	}
	if src.ProjectAnalysis.MinAgents != 0 {
		dst.ProjectAnalysis.MinAgents = src.ProjectAnalysis.MinAgents
	}
	if src.ProjectAnalysis.MaxAgents != 0 {
		dst.ProjectAnalysis.MaxAgents = src.ProjectAnalysis.MaxAgents
	}
	if src.ProjectAnalysis.MaxTotalShards != 0 {
		dst.ProjectAnalysis.MaxTotalShards = src.ProjectAnalysis.MaxTotalShards
	}
	if src.ProjectAnalysis.MaxRevisionRounds != 0 {
		dst.ProjectAnalysis.MaxRevisionRounds = src.ProjectAnalysis.MaxRevisionRounds
	}
	if src.ProjectAnalysis.MaxProviderRetries != 0 {
		dst.ProjectAnalysis.MaxProviderRetries = src.ProjectAnalysis.MaxProviderRetries
	}
	if src.ProjectAnalysis.ProviderRetryDelayMs != 0 {
		dst.ProjectAnalysis.ProviderRetryDelayMs = src.ProjectAnalysis.ProviderRetryDelayMs
	}
	if src.ProjectAnalysis.MaxFilesPerShard != 0 {
		dst.ProjectAnalysis.MaxFilesPerShard = src.ProjectAnalysis.MaxFilesPerShard
	}
	if src.ProjectAnalysis.MaxLinesPerShard != 0 {
		dst.ProjectAnalysis.MaxLinesPerShard = src.ProjectAnalysis.MaxLinesPerShard
	}
	if len(src.ProjectAnalysis.ExcludeDirs) > 0 {
		dst.ProjectAnalysis.ExcludeDirs = append([]string(nil), src.ProjectAnalysis.ExcludeDirs...)
	}
	if len(src.ProjectAnalysis.ExcludePaths) > 0 {
		dst.ProjectAnalysis.ExcludePaths = append([]string(nil), src.ProjectAnalysis.ExcludePaths...)
	}
	if src.ProjectAnalysis.OutputDir != "" {
		dst.ProjectAnalysis.OutputDir = src.ProjectAnalysis.OutputDir
	}
	if src.ProjectAnalysis.MaxFileBytes != 0 {
		dst.ProjectAnalysis.MaxFileBytes = src.ProjectAnalysis.MaxFileBytes
	}
	if src.ProjectAnalysis.WorkerProfile != nil {
		copy := *src.ProjectAnalysis.WorkerProfile
		dst.ProjectAnalysis.WorkerProfile = &copy
	}
	if src.ProjectAnalysis.ReviewerProfile != nil {
		copy := *src.ProjectAnalysis.ReviewerProfile
		dst.ProjectAnalysis.ReviewerProfile = &copy
	}
	if src.ProjectAnalysis.Incremental != nil {
		value := *src.ProjectAnalysis.Incremental
		dst.ProjectAnalysis.Incremental = &value
	}
	mergeReviewHarnessConfig(&dst.Review, src.Review)
	if src.Specialists.Enabled != nil {
		value := *src.Specialists.Enabled
		dst.Specialists.Enabled = &value
	}
	if len(src.Specialists.Profiles) > 0 {
		dst.Specialists.Profiles = append([]SpecialistSubagentProfile(nil), src.Specialists.Profiles...)
	}
	if src.WorktreeIsolation.Enabled != nil {
		value := *src.WorktreeIsolation.Enabled
		dst.WorktreeIsolation.Enabled = &value
	}
	if strings.TrimSpace(src.WorktreeIsolation.RootDir) != "" {
		dst.WorktreeIsolation.RootDir = src.WorktreeIsolation.RootDir
	}
	if strings.TrimSpace(src.WorktreeIsolation.BranchPrefix) != "" {
		dst.WorktreeIsolation.BranchPrefix = src.WorktreeIsolation.BranchPrefix
	}
	if src.WorktreeIsolation.AutoForTrackedFeatures != nil {
		value := *src.WorktreeIsolation.AutoForTrackedFeatures
		dst.WorktreeIsolation.AutoForTrackedFeatures = &value
	}
	if len(src.Desktop) > 0 {
		dst.Desktop = mergeOpaqueConfigMaps(dst.Desktop, src.Desktop)
	}
}

func applyEnv(cfg *Config) {
	envString("KERNFORGE_PROVIDER", &cfg.Provider)
	envString("KERNFORGE_MODEL", &cfg.Model)
	envString("KERNFORGE_BASE_URL", &cfg.BaseURL)
	envString("KERNFORGE_API_KEY", &cfg.APIKey)
	envString("KERNFORGE_CODEX_CLI_PATH", &cfg.CodexCLIPath)
	if rawArgs := strings.TrimSpace(os.Getenv("KERNFORGE_CODEX_CLI_ARGS")); rawArgs != "" {
		cfg.CodexCLIArgs = splitAnalysisCommandLine(rawArgs)
	}
	envString("KERNFORGE_CLAUDE_CLI_PATH", &cfg.ClaudeCLIPath)
	if rawArgs := strings.TrimSpace(os.Getenv("KERNFORGE_CLAUDE_CLI_ARGS")); rawArgs != "" {
		cfg.ClaudeCLIArgs = splitAnalysisCommandLine(rawArgs)
	}
	if rawWorkspaceIDs := strings.TrimSpace(os.Getenv("KERNFORGE_FORCED_CHATGPT_WORKSPACE_ID")); rawWorkspaceIDs != "" {
		cfg.ForcedChatGPTWorkspaceID = normalizeForcedChatGPTWorkspaceIDs(strings.Split(rawWorkspaceIDs, ","))
	}
	envString("KERNFORGE_PERMISSION_MODE", &cfg.PermissionMode)
	envString("KERNFORGE_SHELL", &cfg.Shell)
	envString("KERNFORGE_SESSION_DIR", &cfg.SessionDir)
	envString("KERNFORGE_REASONING_EFFORT", &cfg.ReasoningEffort)
	envString("KERNFORGE_PROGRESS_DISPLAY", &cfg.ProgressDisplay)
	envInt("KERNFORGE_MAX_REQUEST_RETRIES", &cfg.MaxRequestRetries)
	envInt("KERNFORGE_REQUEST_RETRY_DELAY_MS", &cfg.RequestRetryDelayMs)
	envInt("KERNFORGE_REQUEST_TIMEOUT_SECONDS", &cfg.RequestTimeoutSecs)
	envInt("KERNFORGE_SHELL_TIMEOUT_SECONDS", &cfg.ShellTimeoutSecs)
	envInt("KERNFORGE_READ_HINT_SPANS", &cfg.ReadHintSpans)
	envInt("KERNFORGE_READ_CACHE_ENTRIES", &cfg.ReadCacheEntries)
	envString("KERNFORGE_MSBUILD_PATH", &cfg.MSBuildPath)
	envString("KERNFORGE_CMAKE_PATH", &cfg.CMakePath)
	envString("KERNFORGE_CTEST_PATH", &cfg.CTestPath)
	envString("KERNFORGE_NINJA_PATH", &cfg.NinjaPath)
	envBool("KERNFORGE_AUTO_CHECKPOINT_EDITS", &cfg.AutoCheckpointEdits)
	envBool("KERNFORGE_AUTO_VERIFY", &cfg.AutoVerify)
	envBool("KERNFORGE_AUTO_LOCALE", &cfg.AutoLocale)
	envString("KERNFORGE_FUZZ_FUNC_OUTPUT_LANGUAGE", &cfg.FuzzFuncOutputLanguage)
	envBool("KERNFORGE_HOOKS_ENABLED", &cfg.HooksEnabled)
	envBool("KERNFORGE_HOOKS_FAIL_CLOSED", &cfg.HooksFailClosed)

	switch normalizeProviderName(cfg.Provider) {
	case "anthropic":
		if cfg.APIKey == "" {
			envString("ANTHROPIC_API_KEY", &cfg.APIKey)
		}
	case "deepseek":
		if cfg.BaseURL == "" {
			cfg.BaseURL = normalizeDeepSeekBaseURL("")
		}
		if cfg.APIKey == "" {
			envString("DEEPSEEK_API_KEY", &cfg.APIKey)
		}
	case "openrouter":
		if cfg.BaseURL == "" {
			cfg.BaseURL = normalizeOpenRouterBaseURL("")
		}
		if cfg.APIKey == "" {
			envString("OPENROUTER_API_KEY", &cfg.APIKey)
		}
	case "opencode":
		if cfg.BaseURL == "" {
			cfg.BaseURL = normalizeOpenCodeBaseURL("")
		}
		if cfg.APIKey == "" {
			envString("OPENCODE_API_KEY", &cfg.APIKey)
		}
		if cfg.APIKey == "" {
			envString("OPENCODE_ZEN_API_KEY", &cfg.APIKey)
		}
	case "opencode-go":
		if cfg.BaseURL == "" {
			cfg.BaseURL = normalizeOpenCodeGoBaseURL("")
		}
		if cfg.APIKey == "" {
			envString("OPENCODE_GO_API_KEY", &cfg.APIKey)
		}
		if cfg.APIKey == "" {
			envString("OPENCODE_API_KEY", &cfg.APIKey)
		}
		if cfg.APIKey == "" {
			envString("OPENCODE_ZEN_API_KEY", &cfg.APIKey)
		}
	case "ollama":
		if cfg.BaseURL == "" {
			envString("OLLAMA_HOST", &cfg.BaseURL)
		}
		if cfg.APIKey == "" {
			envString("OLLAMA_API_KEY", &cfg.APIKey)
		}
	case "openai-codex":
		if cfg.BaseURL == "" {
			cfg.BaseURL = normalizeOpenAICodexBaseURL("")
		}
	case "lmstudio", "vllm", "llama.cpp":
		if cfg.BaseURL == "" {
			cfg.BaseURL = normalizeLocalOpenAICompatibleBaseURL(cfg.Provider, "")
		}
	case "openai", "openai-compatible":
		if cfg.APIKey == "" {
			envString("OPENAI_API_KEY", &cfg.APIKey)
		}
	}
}

func normalizeConfigPaths(cfg *Config) {
	cfg.ReasoningEffort = normalizeReasoningEffort(cfg.ReasoningEffort)
	cfg.ProgressDisplay = normalizeProgressDisplay(cfg.ProgressDisplay)
	cfg.ForcedChatGPTWorkspaceID = normalizeForcedChatGPTWorkspaceIDs(cfg.ForcedChatGPTWorkspaceID)
	cfg.Desktop = mergeOpaqueConfigMaps(nil, cfg.Desktop)
	if cfg.SessionDir != "" {
		cfg.SessionDir = expandHome(cfg.SessionDir)
	}
	if cfg.SessionDir == "" {
		cfg.SessionDir = filepath.Join(userConfigDir(), "sessions")
	}
	for i, item := range cfg.MemoryFiles {
		cfg.MemoryFiles[i] = expandHome(item)
	}
	for i, item := range cfg.SkillPaths {
		cfg.SkillPaths[i] = expandHome(item)
	}
	cfg.Projects = mergeProjectTrustConfigs(nil, cfg.Projects)
	if strings.TrimSpace(cfg.CodexCLIPath) != "" {
		cfg.CodexCLIPath = expandHome(cfg.CodexCLIPath)
	}
	if strings.TrimSpace(cfg.ClaudeCLIPath) != "" {
		cfg.ClaudeCLIPath = expandHome(cfg.ClaudeCLIPath)
	}
	if strings.TrimSpace(cfg.ProjectAnalysis.OutputDir) != "" {
		cfg.ProjectAnalysis.OutputDir = expandHome(cfg.ProjectAnalysis.OutputDir)
	}
	if strings.TrimSpace(cfg.MSBuildPath) != "" {
		cfg.MSBuildPath = expandHome(cfg.MSBuildPath)
	}
	if strings.TrimSpace(cfg.CMakePath) != "" {
		cfg.CMakePath = expandHome(cfg.CMakePath)
	}
	if strings.TrimSpace(cfg.CTestPath) != "" {
		cfg.CTestPath = expandHome(cfg.CTestPath)
	}
	if strings.TrimSpace(cfg.NinjaPath) != "" {
		cfg.NinjaPath = expandHome(cfg.NinjaPath)
	}
	if cfg.ProjectAnalysis.WorkerProfile != nil {
		cfg.ProjectAnalysis.WorkerProfile.Provider = normalizeProviderName(cfg.ProjectAnalysis.WorkerProfile.Provider)
		cfg.ProjectAnalysis.WorkerProfile.Model = strings.TrimSpace(cfg.ProjectAnalysis.WorkerProfile.Model)
		cfg.ProjectAnalysis.WorkerProfile.BaseURL = normalizeOptionalProfileBaseURL(cfg.ProjectAnalysis.WorkerProfile.Provider, cfg.ProjectAnalysis.WorkerProfile.BaseURL)
		cfg.ProjectAnalysis.WorkerProfile.APIKey = strings.TrimSpace(cfg.ProjectAnalysis.WorkerProfile.APIKey)
		cfg.ProjectAnalysis.WorkerProfile.ReasoningEffort = normalizeReasoningEffort(cfg.ProjectAnalysis.WorkerProfile.ReasoningEffort)
	}
	if cfg.ProjectAnalysis.ReviewerProfile != nil {
		cfg.ProjectAnalysis.ReviewerProfile.Provider = normalizeProviderName(cfg.ProjectAnalysis.ReviewerProfile.Provider)
		cfg.ProjectAnalysis.ReviewerProfile.Model = strings.TrimSpace(cfg.ProjectAnalysis.ReviewerProfile.Model)
		cfg.ProjectAnalysis.ReviewerProfile.BaseURL = normalizeOptionalProfileBaseURL(cfg.ProjectAnalysis.ReviewerProfile.Provider, cfg.ProjectAnalysis.ReviewerProfile.BaseURL)
		cfg.ProjectAnalysis.ReviewerProfile.APIKey = strings.TrimSpace(cfg.ProjectAnalysis.ReviewerProfile.APIKey)
		cfg.ProjectAnalysis.ReviewerProfile.ReasoningEffort = normalizeReasoningEffort(cfg.ProjectAnalysis.ReviewerProfile.ReasoningEffort)
	}
	normalizeReviewHarnessConfig(&cfg.Review)
	if strings.EqualFold(cfg.Provider, "ollama") && strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = normalizeOllamaBaseURL("")
	}
	if strings.EqualFold(cfg.Provider, "openrouter") && strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = normalizeOpenRouterBaseURL("")
	}
	if strings.EqualFold(cfg.Provider, "deepseek") && strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = normalizeDeepSeekBaseURL("")
	}
	if strings.EqualFold(normalizeProviderName(cfg.Provider), "openai-codex") && strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = normalizeOpenAICodexBaseURL("")
	}
	if isLocalOpenAICompatibleProvider(cfg.Provider) && strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = normalizeLocalOpenAICompatibleBaseURL(cfg.Provider, "")
	}
	for i, server := range cfg.MCPServers {
		cfg.MCPServers[i].Name = strings.TrimSpace(server.Name)
		cfg.MCPServers[i].Command = strings.TrimSpace(server.Command)
		if strings.TrimSpace(server.Cwd) != "" {
			cfg.MCPServers[i].Cwd = expandHome(server.Cwd)
		}
		if len(server.Capabilities) > 0 {
			cleaned := make([]string, 0, len(server.Capabilities))
			seen := map[string]struct{}{}
			for _, capability := range server.Capabilities {
				normalized := strings.ToLower(strings.TrimSpace(capability))
				if normalized == "" {
					continue
				}
				if _, ok := seen[normalized]; ok {
					continue
				}
				seen[normalized] = struct{}{}
				cleaned = append(cleaned, normalized)
			}
			cfg.MCPServers[i].Capabilities = cleaned
		}
		if len(server.Env) > 0 {
			cleaned := make(map[string]string, len(server.Env))
			for key, value := range server.Env {
				trimmedKey := strings.TrimSpace(key)
				if trimmedKey == "" {
					continue
				}
				cleaned[trimmedKey] = strings.TrimSpace(value)
			}
			cfg.MCPServers[i].Env = cleaned
		}
	}
	cfg.Provider = normalizeProviderName(cfg.Provider)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if len(cfg.ProviderKeys) > 0 {
		cleaned := make(map[string]string, len(cfg.ProviderKeys))
		for provider, key := range cfg.ProviderKeys {
			provider = normalizeProviderName(provider)
			key = strings.TrimSpace(key)
			if provider == "" || key == "" {
				continue
			}
			cleaned[provider] = key
		}
		cfg.ProviderKeys = cleaned
	}
	if cfg.Provider != "" && cfg.APIKey != "" {
		if cfg.ProviderKeys == nil {
			cfg.ProviderKeys = make(map[string]string)
		}
		if strings.TrimSpace(cfg.ProviderKeys[cfg.Provider]) == "" {
			cfg.ProviderKeys[cfg.Provider] = cfg.APIKey
		}
	}
	if len(cfg.ModelRoutes.ProviderLimits) > 0 {
		cleaned := make(map[string]int, len(cfg.ModelRoutes.ProviderLimits))
		for provider, limit := range cfg.ModelRoutes.ProviderLimits {
			provider = normalizeProviderName(provider)
			if provider == "" || limit <= 0 {
				continue
			}
			cleaned[provider] = limit
		}
		cfg.ModelRoutes.ProviderLimits = cleaned
	}
	if len(cfg.ModelRoutes.RouteLimits) > 0 {
		cleaned := make(map[string]int, len(cfg.ModelRoutes.RouteLimits))
		for route, limit := range cfg.ModelRoutes.RouteLimits {
			route = strings.TrimSpace(route)
			if route == "" || limit <= 0 {
				continue
			}
			cleaned[route] = limit
		}
		cfg.ModelRoutes.RouteLimits = cleaned
	}
	for i, profile := range cfg.Profiles {
		cfg.Profiles[i].BaseURL = normalizeProfileBaseURL(profile.Provider, profile.BaseURL)
	}
	for i, profile := range cfg.Specialists.Profiles {
		cfg.Specialists.Profiles[i].Name = strings.TrimSpace(profile.Name)
		cfg.Specialists.Profiles[i].Description = strings.TrimSpace(profile.Description)
		cfg.Specialists.Profiles[i].Prompt = strings.TrimSpace(profile.Prompt)
		cfg.Specialists.Profiles[i].Provider = normalizeProviderName(profile.Provider)
		cfg.Specialists.Profiles[i].Model = strings.TrimSpace(profile.Model)
		cfg.Specialists.Profiles[i].BaseURL = normalizeOptionalProfileBaseURL(profile.Provider, profile.BaseURL)
		cfg.Specialists.Profiles[i].APIKey = strings.TrimSpace(profile.APIKey)
		cfg.Specialists.Profiles[i].NodeKinds = normalizeTaskStateList(profile.NodeKinds, 16)
		cfg.Specialists.Profiles[i].Keywords = normalizeTaskStateList(profile.Keywords, 32)
		cfg.Specialists.Profiles[i].OwnershipPaths = normalizeTaskStateList(profile.OwnershipPaths, 32)
	}
	if strings.TrimSpace(cfg.WorktreeIsolation.RootDir) != "" {
		cfg.WorktreeIsolation.RootDir = expandHome(cfg.WorktreeIsolation.RootDir)
	}
	if strings.TrimSpace(cfg.WorktreeIsolation.RootDir) == "" {
		cfg.WorktreeIsolation.RootDir = filepath.Join(userConfigDir(), "worktrees")
	}
	cfg.WorktreeIsolation.BranchPrefix = strings.TrimSpace(cfg.WorktreeIsolation.BranchPrefix)
	if cfg.WorktreeIsolation.BranchPrefix == "" {
		cfg.WorktreeIsolation.BranchPrefix = "kernforge/"
	}
}

type saveUserConfigOptions struct {
	PreserveReviewRoleModels bool
}

func SaveUserConfig(cfg Config) error {
	return saveUserConfigWithOptions(cfg, saveUserConfigOptions{PreserveReviewRoleModels: true})
}

func SaveUserConfigReplacingReviewRoleModels(cfg Config) error {
	return saveUserConfigWithOptions(cfg, saveUserConfigOptions{PreserveReviewRoleModels: false})
}

func saveUserConfigWithOptions(cfg Config, opts saveUserConfigOptions) error {
	normalizeConfigPaths(&cfg)
	if err := normalizeConfigPermissionMode(&cfg); err != nil {
		return err
	}
	preserveExistingUserConfig(&cfg, opts)
	if err := os.MkdirAll(userConfigDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(userConfigPath(), data, 0o644)
}

func preserveExistingUserConfig(cfg *Config, opts saveUserConfigOptions) {
	if cfg == nil {
		return
	}
	data, err := os.ReadFile(userConfigPath())
	if err != nil {
		return
	}
	var existing Config
	if err := json.Unmarshal(data, &existing); err != nil {
		return
	}
	normalizeConfigPaths(&existing)
	if strings.TrimSpace(cfg.APIKey) == "" &&
		strings.TrimSpace(existing.APIKey) != "" &&
		strings.EqualFold(cfg.Provider, existing.Provider) {
		cfg.APIKey = strings.TrimSpace(existing.APIKey)
	}
	if cfg.ProviderKeys == nil {
		cfg.ProviderKeys = map[string]string{}
	}
	for provider, key := range existing.ProviderKeys {
		provider = normalizeProviderName(provider)
		key = strings.TrimSpace(key)
		if provider == "" || key == "" {
			continue
		}
		if strings.TrimSpace(cfg.ProviderKeys[provider]) == "" {
			cfg.ProviderKeys[provider] = key
		}
	}
	if strings.TrimSpace(existing.Provider) != "" && strings.TrimSpace(existing.APIKey) != "" {
		provider := normalizeProviderName(existing.Provider)
		if strings.TrimSpace(cfg.ProviderKeys[provider]) == "" {
			cfg.ProviderKeys[provider] = strings.TrimSpace(existing.APIKey)
		}
	}
	if len(cfg.ProviderKeys) == 0 {
		cfg.ProviderKeys = nil
	}
	if strings.TrimSpace(cfg.CodexCLIPath) == "" && strings.TrimSpace(existing.CodexCLIPath) != "" {
		cfg.CodexCLIPath = strings.TrimSpace(existing.CodexCLIPath)
	}
	if len(cfg.CodexCLIArgs) == 0 && len(existing.CodexCLIArgs) > 0 {
		cfg.CodexCLIArgs = append([]string(nil), existing.CodexCLIArgs...)
	}
	if strings.TrimSpace(cfg.ClaudeCLIPath) == "" && strings.TrimSpace(existing.ClaudeCLIPath) != "" {
		cfg.ClaudeCLIPath = strings.TrimSpace(existing.ClaudeCLIPath)
	}
	if len(cfg.ClaudeCLIArgs) == 0 && len(existing.ClaudeCLIArgs) > 0 {
		cfg.ClaudeCLIArgs = append([]string(nil), existing.ClaudeCLIArgs...)
	}
	if len(cfg.Profiles) == 0 && len(existing.Profiles) > 0 {
		cfg.Profiles = append([]Profile(nil), existing.Profiles...)
	} else if len(cfg.Profiles) > 0 && len(existing.Profiles) > 0 {
		cfg.Profiles = mergeConfigProfiles(existing.Profiles, cfg.Profiles)
	}
	if strings.TrimSpace(cfg.ActiveProfileKey) == "" && strings.TrimSpace(existing.ActiveProfileKey) != "" {
		cfg.ActiveProfileKey = strings.TrimSpace(existing.ActiveProfileKey)
	}
	cfg.Projects = mergeProjectTrustConfigs(existing.Projects, cfg.Projects)
	if opts.PreserveReviewRoleModels {
		preserveExistingReviewRoleModels(cfg, existing)
	}
	cfg.Desktop = mergeOpaqueConfigMaps(existing.Desktop, cfg.Desktop)
}

func preserveExistingReviewRoleModels(cfg *Config, existing Config) {
	if cfg == nil || len(existing.Review.RoleModels) == 0 {
		return
	}
	if cfg.Review.RoleModels == nil {
		cfg.Review.RoleModels = map[string]ReviewModelConfig{}
	}
	for role, existingModel := range existing.Review.RoleModels {
		role = normalizeReviewRole(role)
		if role == "" || strings.TrimSpace(existingModel.Provider) == "" || strings.TrimSpace(existingModel.Model) == "" {
			continue
		}
		current, ok := cfg.Review.RoleModels[role]
		if !ok {
			cfg.Review.RoleModels[role] = existingModel
			continue
		}
		if strings.TrimSpace(current.APIKey) == "" &&
			strings.TrimSpace(existingModel.APIKey) != "" &&
			sameProfileRoute(current.Provider, current.Model, current.BaseURL, existingModel.Provider, existingModel.Model, existingModel.BaseURL) {
			current.APIKey = strings.TrimSpace(existingModel.APIKey)
			cfg.Review.RoleModels[role] = current
		}
	}
}

func SaveWorkspaceConfigOverrides(cwd string, overrides map[string]any) error {
	path := workspaceConfigPath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	payload := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &payload); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	for key, value := range overrides {
		if value == nil {
			delete(payload, key)
			continue
		}
		payload[key] = value
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func normalizeProfileBaseURL(provider, baseURL string) string {
	switch normalizeProviderName(provider) {
	case "ollama":
		if strings.TrimSpace(baseURL) == "" {
			return normalizeOllamaBaseURL("")
		}
		return normalizeOllamaBaseURL(baseURL)
	case "openrouter":
		if strings.TrimSpace(baseURL) == "" {
			return normalizeOpenRouterBaseURL("")
		}
		return normalizeOpenRouterBaseURL(baseURL)
	case "deepseek":
		if strings.TrimSpace(baseURL) == "" {
			return normalizeDeepSeekBaseURL("")
		}
		return normalizeDeepSeekBaseURL(baseURL)
	case "opencode":
		if strings.TrimSpace(baseURL) == "" {
			return normalizeOpenCodeBaseURL("")
		}
		return normalizeOpenCodeBaseURL(baseURL)
	case "opencode-go":
		if strings.TrimSpace(baseURL) == "" {
			return normalizeOpenCodeGoBaseURL("")
		}
		return normalizeOpenCodeGoBaseURL(baseURL)
	case "openai-codex":
		if strings.TrimSpace(baseURL) == "" {
			return normalizeOpenAICodexBaseURL("")
		}
		return normalizeOpenAICodexBaseURL(baseURL)
	case "lmstudio", "vllm", "llama.cpp":
		if strings.TrimSpace(baseURL) == "" {
			return normalizeLocalOpenAICompatibleBaseURL(provider, "")
		}
		return normalizeLocalOpenAICompatibleBaseURL(provider, baseURL)
	default:
		return strings.TrimSpace(baseURL)
	}
}

func normalizeOptionalProfileBaseURL(provider, baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}
	return normalizeProfileBaseURL(provider, baseURL)
}

func normalizeProgressDisplay(value string) string {
	if normalized, ok := parseProgressDisplayInput(value); ok {
		return normalized
	}
	return "auto"
}

func parseProgressDisplayInput(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto", "default":
		return "auto", true
	case "compact", "footer", "quiet":
		return "compact", true
	case "stream", "ledger", "verbose", "persistent":
		return "stream", true
	default:
		return "", false
	}
}

func configProgressDisplay(cfg Config) string {
	return normalizeProgressDisplay(cfg.ProgressDisplay)
}

func normalizeReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "default", "undefined", "none", "off", "unset", "clear":
		return ""
	case "min", "minimal":
		return "minimal"
	case "lo", "low", "light":
		return "low"
	case "med", "medium", "normal":
		return "medium"
	case "hi", "high":
		return "high"
	case "xhigh", "x-high", "extra-high", "extra_high", "heavy":
		return "xhigh"
	default:
		return strings.ToLower(strings.TrimSpace(effort))
	}
}

func validReasoningEffort(effort string) bool {
	switch normalizeReasoningEffort(effort) {
	case "", "minimal", "low", "medium", "high", "xhigh":
		return true
	default:
		return false
	}
}

func reasoningEffortDisplay(effort string) string {
	effort = normalizeReasoningEffort(effort)
	if effort == "" {
		return "undefined"
	}
	return effort
}

func mergeConfigProfiles(base []Profile, overlay []Profile) []Profile {
	merged := make([]Profile, 0, len(base)+len(overlay))
	seen := map[string]struct{}{}
	for _, profile := range overlay {
		profile = normalizeConfigProfile(profile)
		key := configProfileKey(profile)
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, profile)
	}
	for _, profile := range base {
		profile = normalizeConfigProfile(profile)
		key := configProfileKey(profile)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, profile)
	}
	return merged
}

func normalizeConfigProfile(profile Profile) Profile {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Provider = normalizeProviderName(profile.Provider)
	profile.Model = strings.TrimSpace(profile.Model)
	profile.BaseURL = normalizeProfileBaseURL(profile.Provider, profile.BaseURL)
	profile.APIKey = strings.TrimSpace(profile.APIKey)
	profile.ReasoningEffort = normalizeReasoningEffort(profile.ReasoningEffort)
	if profile.Name == "" && profile.Provider != "" && profile.Model != "" {
		profile.Name = profileName(profile.Provider, profile.Model)
	}
	return profile
}

func configProfileKey(profile Profile) string {
	provider := normalizeProviderName(profile.Provider)
	model := strings.ToLower(strings.TrimSpace(profile.Model))
	if provider == "" || model == "" {
		return ""
	}
	baseURL := strings.ToLower(strings.TrimSpace(normalizeProfileBaseURL(provider, profile.BaseURL)))
	return provider + "\x00" + model + "\x00" + baseURL
}

func activeConfigProfileKey(cfg Config) string {
	return configProfileKey(Profile{
		Provider: cfg.Provider,
		Model:    cfg.Model,
		BaseURL:  cfg.BaseURL,
	})
}

func applyActiveProfileRoleModels(cfg *Config) {
	if cfg == nil || len(cfg.Profiles) == 0 {
		return
	}
	profile, ok, selectedByActiveKey := findActiveConfigProfile(*cfg)
	if !ok {
		return
	}
	if !selectedByActiveKey && profile.RoleModels == nil {
		return
	}
	cfg.Provider = strings.TrimSpace(profile.Provider)
	cfg.Model = strings.TrimSpace(profile.Model)
	cfg.BaseURL = normalizeProfileBaseURL(profile.Provider, profile.BaseURL)
	cfg.ReasoningEffort, _ = reasoningEffortOrDefaultForProvider(profile.Provider, profile.ReasoningEffort)
	if strings.TrimSpace(profile.APIKey) != "" {
		cfg.APIKey = strings.TrimSpace(profile.APIKey)
	}
	if strings.TrimSpace(cfg.ActiveProfileKey) == "" {
		cfg.ActiveProfileKey = configProfileKey(profile)
	}
	if profile.RoleModels == nil {
		return
	}
	applyConfigProfileRoleModels(cfg, profile)
}

func findActiveConfigProfile(cfg Config) (Profile, bool, bool) {
	activeKey := strings.TrimSpace(cfg.ActiveProfileKey)
	if activeKey != "" {
		for _, profile := range cfg.Profiles {
			profile = normalizeConfigProfile(profile)
			if configProfileKey(profile) == activeKey {
				return profile, true, true
			}
		}
	}
	currentKey := activeConfigProfileKey(cfg)
	if currentKey == "" {
		return Profile{}, false, false
	}
	for _, profile := range cfg.Profiles {
		profile = normalizeConfigProfile(profile)
		if configProfileKey(profile) == currentKey {
			return profile, true, false
		}
	}
	return Profile{}, false, false
}

func applyConfigProfileRoleModels(cfg *Config, profile Profile) {
	if cfg == nil {
		return
	}
	roles := profile.RoleModels
	if roles == nil {
		roles = &ProfileRoleModels{}
	}
	cfg.ProjectAnalysis.WorkerProfile = cloneConfigProfile(roles.AnalysisWorker)
	cfg.ProjectAnalysis.ReviewerProfile = cloneConfigProfile(roles.AnalysisReviewer)
	applyConfigProfileSpecialistRoleModels(cfg, roles.Specialists)
}

func cloneConfigProfile(profile *Profile) *Profile {
	if profile == nil || strings.TrimSpace(profile.Provider) == "" || strings.TrimSpace(profile.Model) == "" {
		return nil
	}
	cloned := *profile
	cloned.Name = strings.TrimSpace(cloned.Name)
	cloned.Provider = normalizeProviderName(cloned.Provider)
	cloned.Model = strings.TrimSpace(cloned.Model)
	cloned.BaseURL = normalizeOptionalProfileBaseURL(cloned.Provider, cloned.BaseURL)
	cloned.APIKey = strings.TrimSpace(cloned.APIKey)
	cloned.ReasoningEffort, _ = reasoningEffortOrDefaultForProvider(cloned.Provider, cloned.ReasoningEffort)
	if cloned.Name == "" && cloned.Provider != "" && cloned.Model != "" {
		cloned.Name = profileName(cloned.Provider, cloned.Model)
	}
	return &cloned
}

func applyConfigProfileSpecialistRoleModels(cfg *Config, roleSpecialists []SpecialistSubagentProfile) {
	modelsByName := map[string]SpecialistSubagentProfile{}
	for _, profile := range roleSpecialists {
		name := strings.ToLower(strings.TrimSpace(profile.Name))
		if name == "" || strings.TrimSpace(profile.Provider) == "" || strings.TrimSpace(profile.Model) == "" {
			continue
		}
		profile.Name = strings.TrimSpace(profile.Name)
		profile.Provider = normalizeProviderName(profile.Provider)
		profile.Model = strings.TrimSpace(profile.Model)
		profile.BaseURL = normalizeOptionalProfileBaseURL(profile.Provider, profile.BaseURL)
		profile.APIKey = strings.TrimSpace(profile.APIKey)
		profile.ReasoningEffort, _ = reasoningEffortOrDefaultForProvider(profile.Provider, profile.ReasoningEffort)
		modelsByName[name] = profile
	}
	next := make([]SpecialistSubagentProfile, 0, len(cfg.Specialists.Profiles)+len(modelsByName))
	seen := map[string]bool{}
	for _, existing := range cfg.Specialists.Profiles {
		name := strings.ToLower(strings.TrimSpace(existing.Name))
		if name == "" {
			continue
		}
		if model, ok := modelsByName[name]; ok {
			existing.Provider = model.Provider
			existing.Model = model.Model
			existing.BaseURL = model.BaseURL
			existing.APIKey = model.APIKey
			existing.ReasoningEffort = model.ReasoningEffort
			seen[name] = true
			next = append(next, normalizeSpecialistProfile(existing))
			continue
		}
		existing.Provider = ""
		existing.Model = ""
		existing.BaseURL = ""
		existing.APIKey = ""
		existing.ReasoningEffort = ""
		if specialistProfileHasNonModelOverrides(existing) {
			next = append(next, normalizeSpecialistProfile(existing))
		}
		seen[name] = true
	}
	for name, model := range modelsByName {
		if seen[name] {
			continue
		}
		next = append(next, normalizeSpecialistProfile(model))
	}
	cfg.Specialists.Profiles = normalizeSpecialistProfiles(next)
}

func profileName(provider, model string) string {
	return providerUserLabel(provider) + " / " + strings.TrimSpace(model)
}

func EnsureUserConfig(cfg Config) error {
	if err := os.MkdirAll(userConfigDir(), 0o755); err != nil {
		return err
	}
	if err := ensureBundledUserAssets(); err != nil {
		return err
	}
	if _, err := os.Stat(userConfigPath()); err == nil {
		return ensureSupportedUserConfigDefaults()
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := SaveUserConfig(cfg); err != nil {
		return err
	}
	return ensureSupportedUserConfigDefaults()
}

func envString(key string, dst *string) {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		*dst = value
	}
}

func envBool(key string, dst **bool) {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return
	}
	parsed, ok := parseBoolString(value)
	if !ok {
		return
	}
	*dst = boolPtr(parsed)
}

func envInt(key string, dst *int) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return
	}
	*dst = parsed
}

func boolPtr(value bool) *bool {
	v := value
	return &v
}

func configAutoCheckpointEdits(cfg Config) bool {
	if cfg.AutoCheckpointEdits == nil {
		return false
	}
	return *cfg.AutoCheckpointEdits
}

func configHooksEnabled(cfg Config) bool {
	if cfg.HooksEnabled == nil {
		return true
	}
	return *cfg.HooksEnabled
}

func configHooksFailClosed(cfg Config) bool {
	if cfg.HooksFailClosed == nil {
		return false
	}
	return *cfg.HooksFailClosed
}

func configBypassHookTrust(cfg Config) bool {
	return cfg.BypassHookTrust
}

func configAutoVerify(cfg Config) bool {
	if cfg.AutoVerify == nil {
		return true
	}
	return *cfg.AutoVerify
}

// configMaxToolIterations returns the configured tool-loop budget.
// A return value of 0 (or any non-positive cfg.MaxToolIterations) means
// "no cap" — the loop runs until the model produces a final answer or
// the context is cancelled.
func configMaxToolIterations(cfg Config) int {
	if cfg.MaxToolIterations <= 0 {
		return 0
	}
	return cfg.MaxToolIterations
}

func formatMaxToolIterations(value int) string {
	if value <= 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%d", value)
}

func configMaxRequestRetries(cfg Config) int {
	if cfg.MaxRequestRetries < 0 {
		return 0
	}
	if cfg.MaxRequestRetries == 0 {
		return 2
	}
	return cfg.MaxRequestRetries
}

func configRequestRetryDelay(cfg Config) time.Duration {
	if cfg.RequestRetryDelayMs > 0 {
		return time.Duration(cfg.RequestRetryDelayMs) * time.Millisecond
	}
	return 1500 * time.Millisecond
}

func providerRetryDelay(baseDelay time.Duration, attempt int) time.Duration {
	if baseDelay <= 0 {
		return 0
	}
	if attempt < 0 {
		attempt = 0
	}
	delay := baseDelay
	for i := 0; i < attempt; i++ {
		if delay >= maxProviderRetryDelay/2 {
			delay = maxProviderRetryDelay
			break
		}
		delay *= 2
	}
	if delay > maxProviderRetryDelay {
		delay = maxProviderRetryDelay
	}
	jitterWindow := delay / 5
	if jitterWindow <= 0 {
		return delay
	}
	span := int64(jitterWindow*2 + 1)
	if span <= 0 {
		return delay
	}
	offset := time.Duration(time.Now().UnixNano()%span) - jitterWindow
	if delay+offset <= 0 {
		return time.Millisecond
	}
	return delay + offset
}

func configRequestTimeout(cfg Config) time.Duration {
	if cfg.RequestTimeoutSecs > 0 {
		return time.Duration(cfg.RequestTimeoutSecs) * time.Second
	}
	return 20 * time.Minute
}

func configShellTimeout(cfg Config) time.Duration {
	if cfg.ShellTimeoutSecs > 0 {
		return time.Duration(cfg.ShellTimeoutSecs) * time.Second
	}
	return time.Duration(currentDefaultShellTimeoutSecs) * time.Second
}

func configReadHintSpans(cfg Config) int {
	if cfg.ReadHintSpans > 0 {
		return cfg.ReadHintSpans
	}
	return defaultReadHintSpans
}

func configReadCacheEntries(cfg Config) int {
	if cfg.ReadCacheEntries > 0 {
		return cfg.ReadCacheEntries
	}
	return defaultReadCacheEntries
}

func configAutoLocale(cfg Config) bool {
	if cfg.AutoLocale == nil {
		return true
	}
	return *cfg.AutoLocale
}

func configFuzzFuncOutputLanguage(cfg Config) string {
	mode := strings.ToLower(strings.TrimSpace(cfg.FuzzFuncOutputLanguage))
	switch mode {
	case "", "english", "en", "en-us":
		return "english"
	case "korean", "ko", "ko-kr":
		return "korean"
	case "system", "pc", "locale", "auto":
		return "system"
	default:
		return "english"
	}
}

func configSpecialistsEnabled(cfg Config) bool {
	if cfg.Specialists.Enabled == nil {
		return true
	}
	return *cfg.Specialists.Enabled
}

func configWorktreeIsolationEnabled(cfg Config) bool {
	if cfg.WorktreeIsolation.Enabled == nil {
		return false
	}
	return *cfg.WorktreeIsolation.Enabled
}

func configWorktreeIsolationRootDir(cfg Config) string {
	root := strings.TrimSpace(cfg.WorktreeIsolation.RootDir)
	if root == "" {
		return filepath.Join(userConfigDir(), "worktrees")
	}
	return root
}

func configWorktreeIsolationBranchPrefix(cfg Config) string {
	prefix := strings.TrimSpace(cfg.WorktreeIsolation.BranchPrefix)
	if prefix == "" {
		return "kernforge/"
	}
	return prefix
}

func configWorktreeIsolationAutoForTrackedFeatures(cfg Config) bool {
	if cfg.WorktreeIsolation.AutoForTrackedFeatures == nil {
		return true
	}
	return *cfg.WorktreeIsolation.AutoForTrackedFeatures
}

func parseBoolString(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "on", "yes", "y":
		return true, true
	case "0", "false", "off", "no", "n":
		return false, true
	default:
		return false, false
	}
}

func defaultShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "sh"
}

func platformUserConfigBaseDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func expandHome(path string) string {
	if path == "" || !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}

type MemoryFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type MemoryBundle struct {
	Files []MemoryFile `json:"files"`
}

func LoadMemory(cwd string, extra []string) (MemoryBundle, error) {
	paths := defaultMemoryPaths(cwd)
	for _, item := range extra {
		paths = append(paths, expandHome(item))
	}

	seen := map[string]bool{}
	var out MemoryBundle
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return out, err
		}
		out.Files = append(out.Files, MemoryFile{
			Path:    path,
			Content: string(data),
		})
	}
	return out, nil
}

func (b MemoryBundle) Combined() string {
	if len(b.Files) == 0 {
		return ""
	}
	var sections []string
	for _, file := range b.Files {
		sections = append(sections, fmt.Sprintf("### %s\n%s", file.Path, strings.TrimSpace(file.Content)))
	}
	return strings.Join(sections, "\n\n")
}

func defaultMemoryPaths(cwd string) []string {
	paths := []string{}
	paths = append(paths,
		filepath.Join(userConfigDir(), "MEMORY.md"),
	)
	for _, dir := range ancestorDirs(cwd) {
		paths = append(paths,
			filepath.Join(dir, userConfigDirName, "KERNFORGE.md"),
			filepath.Join(dir, "KERNFORGE.md"),
		)
	}
	return paths
}

func ancestorDirs(cwd string) []string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil
	}
	var out []string
	for {
		out = append([]string{abs}, out...)
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	return out
}

func InitMemoryTemplate(projectName string) string {
	return fmt.Sprintf(`# %s

## Project overview
- Describe the product, users, and high-level architecture.

## Commands
- Build:
- Test:
- Lint:
- Run:

## Conventions
- Coding style:
- Review expectations:
- Safety notes:

## Current priorities
- 
`, projectName)
}

func InitWorkspaceConfigTemplate(workspaceRoot string) string {
	sample := struct {
		AutoCheckpointEdits *bool                     `json:"auto_checkpoint_edits,omitempty"`
		AutoVerify          *bool                     `json:"auto_verify,omitempty"`
		MaxRequestRetries   int                       `json:"max_request_retries,omitempty"`
		RequestRetryDelayMs int                       `json:"request_retry_delay_ms,omitempty"`
		RequestTimeoutSecs  int                       `json:"request_timeout_seconds,omitempty"`
		ProgressDisplay     string                    `json:"progress_display,omitempty"`
		ShellTimeoutSecs    int                       `json:"shell_timeout_seconds,omitempty"`
		ReadHintSpans       int                       `json:"read_hint_spans,omitempty"`
		ReadCacheEntries    int                       `json:"read_cache_entries,omitempty"`
		MSBuildPath         string                    `json:"msbuild_path,omitempty"`
		CMakePath           string                    `json:"cmake_path,omitempty"`
		CTestPath           string                    `json:"ctest_path,omitempty"`
		NinjaPath           string                    `json:"ninja_path,omitempty"`
		SkillPaths          []string                  `json:"skill_paths,omitempty"`
		EnabledSkills       []string                  `json:"enabled_skills,omitempty"`
		Specialists         SpecialistSubagentsConfig `json:"specialists,omitempty"`
		WorktreeIsolation   WorktreeIsolationConfig   `json:"worktree_isolation,omitempty"`
	}{
		AutoCheckpointEdits: boolPtr(false),
		AutoVerify:          boolPtr(true),
		MaxRequestRetries:   2,
		RequestRetryDelayMs: 1500,
		RequestTimeoutSecs:  1200,
		ProgressDisplay:     "stream",
		ShellTimeoutSecs:    currentDefaultShellTimeoutSecs,
		ReadHintSpans:       defaultReadHintSpans,
		ReadCacheEntries:    defaultReadCacheEntries,
		MSBuildPath:         "",
		CMakePath:           "",
		CTestPath:           "",
		NinjaPath:           "",
		SkillPaths:          []string{"./.kernforge/skills"},
		EnabledSkills:       []string{},
		Specialists: SpecialistSubagentsConfig{
			Enabled:  boolPtr(true),
			Profiles: []SpecialistSubagentProfile{},
		},
		WorktreeIsolation: WorktreeIsolationConfig{
			Enabled:                boolPtr(false),
			RootDir:                filepath.Join("~", userConfigDirName, "worktrees"),
			BranchPrefix:           "kernforge/",
			AutoForTrackedFeatures: boolPtr(true),
		},
	}
	data, err := json.MarshalIndent(sample, "", "  ")
	if err != nil {
		return "{\n  \"skill_paths\": [\"./.kernforge/skills\"]\n}\n"
	}
	return string(data) + "\n"
}

func workspaceHasBundledWebResearchMCP(workspaceRoot string) bool {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return false
	}
	path := filepath.Join(root, userConfigDirName, "mcp", "web-research-mcp.js")
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func defaultWebResearchMCPServer(workspaceRoot string) MCPServerConfig {
	server := MCPServerConfig{
		Name:         "web-research",
		Command:      "node",
		Args:         []string{"path/to/web-research-mcp.js"},
		Env:          defaultWebResearchMCPEnvTemplate(),
		Cwd:          ".",
		Capabilities: []string{"web_search", "web_fetch"},
		Disabled:     true,
	}
	if workspaceHasBundledWebResearchMCP(workspaceRoot) {
		server.Args = []string{filepath.ToSlash(filepath.Join(userConfigDirName, "mcp", "web-research-mcp.js"))}
		server.Disabled = false
		return server
	}
	if deployedWebResearchMCPScriptAvailable() {
		server.Args = []string{filepath.ToSlash(deployedWebResearchMCPScriptPath())}
		server.Disabled = false
		return server
	}
	return server
}

func defaultUserWebResearchMCPServer() MCPServerConfig {
	return MCPServerConfig{
		Name:         "web-research",
		Command:      "node",
		Args:         []string{filepath.ToSlash(deployedWebResearchMCPScriptPath())},
		Env:          defaultWebResearchMCPEnvTemplate(),
		Cwd:          ".",
		Capabilities: []string{"web_search", "web_fetch"},
	}
}

func defaultWebResearchMCPEnvTemplate() map[string]string {
	return map[string]string{
		"TAVILY_API_KEY":       "",
		"BRAVE_SEARCH_API_KEY": "",
		"SERPAPI_API_KEY":      "",
	}
}

func ensureSupportedUserConfigDefaults() error {
	if !deployedWebResearchMCPScriptAvailable() {
		return nil
	}
	cfg, err := loadRawConfigFile(userConfigPath())
	if err != nil {
		return err
	}
	if hasConfiguredWebResearchMCP(cfg.MCPServers) {
		updatedServers, changed := backfillUserWebResearchMCPServers(cfg.MCPServers)
		if !changed {
			return nil
		}
		cfg.MCPServers = updatedServers
		return SaveUserConfig(cfg)
	}
	cfg.MCPServers = append(cfg.MCPServers, defaultUserWebResearchMCPServer())
	return SaveUserConfig(cfg)
}

func loadRawConfigFile(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func hasConfiguredWebResearchMCP(servers []MCPServerConfig) bool {
	for _, server := range servers {
		if isWebResearchMCPServer(server) {
			return true
		}
	}
	return false
}

func backfillUserWebResearchMCPServers(servers []MCPServerConfig) ([]MCPServerConfig, bool) {
	updated := append([]MCPServerConfig(nil), servers...)
	changed := false
	defaultEnv := defaultWebResearchMCPEnvTemplate()
	defaultArgs := []string{filepath.ToSlash(deployedWebResearchMCPScriptPath())}
	defaultCaps := []string{"web_search", "web_fetch"}
	for i, server := range updated {
		if !isWebResearchMCPServer(server) {
			continue
		}
		if shouldUseDeployedUserWebResearchPath(server.Args) {
			updated[i].Args = append([]string(nil), defaultArgs...)
			changed = true
		}
		if len(server.Capabilities) == 0 {
			updated[i].Capabilities = append([]string(nil), defaultCaps...)
			changed = true
		} else {
			for _, capability := range defaultCaps {
				if sliceContainsFold(updated[i].Capabilities, capability) {
					continue
				}
				updated[i].Capabilities = append(updated[i].Capabilities, capability)
				changed = true
			}
		}
		if updated[i].Env == nil {
			updated[i].Env = map[string]string{}
		}
		for key, value := range defaultEnv {
			if _, ok := updated[i].Env[key]; ok {
				continue
			}
			updated[i].Env[key] = value
			changed = true
		}
	}
	return updated, changed
}

func shouldUseDeployedUserWebResearchPath(args []string) bool {
	if len(args) == 0 {
		return true
	}
	if len(args) != 1 {
		return false
	}
	arg := filepath.ToSlash(strings.TrimSpace(args[0]))
	if arg == "" {
		return true
	}
	if arg == filepath.ToSlash(deployedWebResearchMCPScriptPath()) {
		return false
	}
	switch arg {
	case ".kernforge/mcp/web-research-mcp.js", "./.kernforge/mcp/web-research-mcp.js", "path/to/web-research-mcp.js":
		return true
	}
	return false
}

func mergeMCPServerOverrides(base []MCPServerConfig, overlay []MCPServerConfig) []MCPServerConfig {
	merged := make([]MCPServerConfig, 0, len(overlay))
	for _, server := range overlay {
		inherited := server
		if parent, ok := findMatchingMCPServer(base, server); ok {
			inherited = mergeMCPServerConfig(parent, server)
		} else {
			inherited = cloneMCPServerConfig(server)
		}
		merged = append(merged, inherited)
	}
	return merged
}

func findMatchingMCPServer(servers []MCPServerConfig, target MCPServerConfig) (MCPServerConfig, bool) {
	for _, server := range servers {
		if sameMCPServerIdentity(server, target) {
			return server, true
		}
	}
	return MCPServerConfig{}, false
}

func sameMCPServerIdentity(a MCPServerConfig, b MCPServerConfig) bool {
	if strings.TrimSpace(a.Name) != "" && strings.TrimSpace(b.Name) != "" && strings.EqualFold(strings.TrimSpace(a.Name), strings.TrimSpace(b.Name)) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(a.Command), strings.TrimSpace(b.Command)) && sameMCPServerArgs(a.Args, b.Args) {
		return true
	}
	if isWebResearchMCPServer(a) && isWebResearchMCPServer(b) {
		return true
	}
	return false
}

func sameMCPServerArgs(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(filepath.ToSlash(strings.TrimSpace(a[i])), filepath.ToSlash(strings.TrimSpace(b[i]))) {
			return false
		}
	}
	return len(a) > 0
}

func mergeMCPServerConfig(base MCPServerConfig, overlay MCPServerConfig) MCPServerConfig {
	merged := cloneMCPServerConfig(base)
	if strings.TrimSpace(overlay.Name) != "" {
		merged.Name = overlay.Name
	}
	if strings.TrimSpace(overlay.Command) != "" {
		merged.Command = overlay.Command
	}
	if len(overlay.Args) > 0 {
		merged.Args = append([]string(nil), overlay.Args...)
	}
	if strings.TrimSpace(overlay.Cwd) != "" {
		merged.Cwd = overlay.Cwd
	}
	if len(overlay.Capabilities) > 0 {
		merged.Capabilities = append([]string(nil), overlay.Capabilities...)
	}
	merged.Disabled = base.Disabled || overlay.Disabled
	merged.Env = mergeMCPServerEnv(base.Env, overlay.Env)
	return merged
}

func cloneMCPServerConfig(server MCPServerConfig) MCPServerConfig {
	cloned := server
	if server.Args != nil {
		cloned.Args = append([]string(nil), server.Args...)
	}
	if server.Capabilities != nil {
		cloned.Capabilities = append([]string(nil), server.Capabilities...)
	}
	if server.Env != nil {
		cloned.Env = mergeMCPServerEnv(nil, server.Env)
	}
	return cloned
}

func mergeMCPServerEnv(base map[string]string, overlay map[string]string) map[string]string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	merged := map[string]string{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range overlay {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" {
			if _, ok := merged[trimmedKey]; !ok {
				merged[trimmedKey] = ""
			}
			continue
		}
		merged[trimmedKey] = trimmedValue
	}
	return merged
}

func isWebResearchMCPServer(server MCPServerConfig) bool {
	if serverDeclaresWebResearchCapability(server) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(server.Name), "web-research") {
		return true
	}
	for _, arg := range server.Args {
		if strings.EqualFold(filepath.Base(strings.TrimSpace(arg)), "web-research-mcp.js") {
			return true
		}
	}
	return false
}

type Command struct {
	Name string
	Args string
}

func normalizeSlashCommandName(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "_", "-")
}

func ParseCommand(input string) (Command, bool) {
	line := strings.TrimSpace(input)
	if !strings.HasPrefix(line, "/") {
		return Command{}, false
	}
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return Command{Name: "help"}, true
	}
	parts := strings.SplitN(line, " ", 2)
	cmd := Command{Name: normalizeSlashCommandName(parts[0])}
	if len(parts) == 2 {
		cmd.Args = strings.TrimSpace(parts[1])
	}
	return cmd, true
}

func HelpText() string {
	return strings.TrimSpace(`
General:
/config                Show effective runtime config
/trust [status|on|off] Show or set project-local config and hook trust
/context               Show context usage summary
/exit                  Exit the CLI
/help                  Show available commands
/reload                Reload config, memory, skills, hooks, and MCP extensions
/hook-reload           Reload hook configuration only
/hooks                 Show loaded hook rules and warnings
/override              Show active hook overrides for this workspace
/override-add ...      Create a temporary hook override with a reason
/override-clear ...    Remove one override, all overrides, or all for a rule
/status                Show current provider, model, session, and memory info
/version               Show the current application version

Conversation And Sessions:
/clear                 Clear conversation history for the current session
/compact [focus]       Summarize older messages and shrink context
/export [file]         Export the conversation as markdown
/rename <name>         Rename the current session
/resume <session-id>   Resume a saved session
/session               Show the current session id and storage path
/sessions              List recent sessions
/handoff [note]|import <path> Generate or import a compact delegation handoff/result artifact
/suggest [status]      Show proactive situation judgment and suggested next actions
/suggest-dashboard-html Render proactive suggestions as an HTML dashboard
/session-dashboard-html Generate and open a session thread, task graph, automation, and artifact dashboard
/events [tail|export] Tail or export session conversation events as JSONL for local clients
/continuity [note]     Generate a long-task resume/recovery packet with worktrees, jobs, failures, and next commands
/completion-audit [note] Generate a completion readiness audit with blockers, warnings, verification, tasks, jobs, and artifact evidence
/recover [note]        Generate a focused recovery brief from recent errors, verification failure, jobs, and next commands
/jobs [status|check|bundle|cancel|cancel-bundle] Inspect or cancel persistent background shell work
/automation [status|due|digest|monitor|watch|daemon-start|notify|run-due] Show or manage local verification and PR review automations
/review [change|plan|selection|pr|final|goal|analysis] Run the common review harness and write .kernforge/reviews/latest.*
/review pr [--draft-comments|--post-comments|--resolve-thread <id>|--create-issue] [--label <name>] [--assignee <login>] [--milestone <name>] Review a PR target, optionally with explicit GitHub writes
/goal [start|run|status|audit|complete|cancel] Run a Codex-style autonomous goal loop from a prompt or markdown file
/tasks                 Show the current task list

Provider And Models:
/review plan <task>   Review an implementation plan through the common review harness
/new-feature <task>    Create tracked feature artifacts and guide implement, verify, close, or cleanup follow-up
/analyze-project [--path <dir>] [--mode map|trace|impact|surface|security|performance] [goal] Analyze the workspace or a scoped path, infer a mode-specific goal when omitted, generate a project knowledge base, docs, manifest, dashboard, and next-step handoff
/docs-refresh          Regenerate latest analysis docs, docs manifest, dashboard, and docs-backed vector corpus from saved artifacts
/analyze-dashboard [latest|path] Open the latest or selected project analysis document portal
/analyze-performance [focus] Analyze likely performance bottlenecks and suggest hotspot follow-up commands
/specialists           Show specialist profiles plus editable ownership and worktree routing state
/set-analysis-models   Configure worker/reviewer models for /analyze-project
/set-specialist-model  Configure the provider/model used by a specialist subagent
/model                 Show all model routing and interactively reconfigure one target
/effort [target] [value] Show or set per-model reasoning effort: undefined, minimal, low, medium, high, xhigh
/codex-auth [status|login|logout] Manage Kernforge-owned OpenAI Codex OAuth auth
/permissions [mode]          Show or change permissions: default, acceptEdits, plan, bypassPermissions, or Codex built-in profile ids
/set-max-tool-iterations <n|0|unlimited|none|off> Set the maximum tool iteration count per request; 0 disables the cap
/progress-display [auto|compact|stream] Show or set in-flight progress visibility
/profile [list|<number>|rN|dN|pN] Show saved provider/model profiles, role model routing, or manage one explicitly
/provider              Choose and configure a provider
/provider status       Show provider connectivity, key state, and budget visibility
- Write approval prompts accept y/N/a. Using a on "Allow write?" enables write auto-approval for the current session only.
- Diff preview prompts accept y/N/a. Using a on "Open diff preview?" auto-accepts the current and future diff previews for the current session only.
- Git approval prompts accept y/N/a. Using a on "Allow git?" enables git auto-approval for the current session only.
- Shell approval is tracked separately for the current session. Once a shell command is approved, later run_shell calls can proceed without another shell prompt in the same session.
- Kernforge does not allow run_shell to modify workspace files. File edits must go through apply_patch, write_file, or replace_in_file so diff preview and write approval rules can still apply.
- Use /status to inspect the current session approval state for writes, diff previews, shell access, and git actions.
- Use /config to inspect effective settings such as provider, token limits, progress display, hooks, and verification defaults.

Verification And Checkpoints:
/checkpoint [note]     Create a workspace checkpoint snapshot and suggest diff/list follow-up
/checkpoint-auto [on|off] Show or change automatic checkpoint creation before edits
/detect-verification-tools Detect and save workspace verification tool paths
/set-msbuild-path <path> Set the MSBuild executable path for workspace verification
/clear-msbuild-path     Clear the workspace MSBuild path override
/set-cmake-path <path> Set the CMake executable path for workspace verification
/clear-cmake-path       Clear the workspace CMake path override
/set-ctest-path <path> Set the CTest executable path for workspace verification
/clear-ctest-path       Clear the workspace CTest path override
/set-ninja-path <path> Set the Ninja executable path for workspace verification
/clear-ninja-path       Clear the workspace Ninja path override
/set-auto-verify [on|off] Show or change automatic verification after edits
- Quote paths that contain spaces. Example: /set-msbuild-path "C:\Program Files\...\MSBuild.exe"
/checkpoint-diff [target] [-- path[,path2]] Preview differences between current files and a checkpoint
/checkpoints           List checkpoints for the current workspace
/investigate [subcommand] Manage live investigation sessions and guide the next snapshot, simulation, or evidence step
/investigate-dashboard  Show an investigation dashboard for this workspace
/investigate-dashboard-html Generate and open an HTML investigation dashboard
/simulate [profile]   Run risk-oriented simulation profiles and guide verification or evidence follow-up
/fuzz-func <name> [--file <path>|@<path>] [--source-scan off|focused|full] Auto-plan directed function fuzzing for one function, reuse or run source-scan context, recover build settings when possible, and ask before heuristic execution
/fuzz-func --file <path> or @<path> Analyze one file plus its include/import closure, then auto-pick the best representative function root
/fuzz-func --from-candidate <candidate-id> Start focused function fuzzing from a saved /source-scan candidate
/fuzz-func language [system|english] Choose whether /fuzz-func output follows the PC language or stays in English
/fuzz-campaign [status|run|new|list|show] Inspect or advance the campaign planner through seed promotion, deduplicated finding lifecycle updates, parsed coverage report feedback, sanitizer/verifier artifact capture, native result reports, and evidence capture
/source-scan [status|run|list|show|revalidate] Scan source with built-in bug-pattern matchers, persist candidates, and guide /fuzz-func --from-candidate
/create-driver-poc <driver-name> [--type objectfilter|minifilter|registryfilter|wfpcallout] Generate an x64 C++20 MSVC kernel-driver POC solution plus <driver-name>-tester.exe; omitting --type keeps the original SCM/IOCTL ping POC
/create-driver-poc <driver-name> --type objectfilter Generate an object manager process/thread handle filter POC
/create-driver-poc <driver-name> --type minifilter Generate a filesystem minifilter POC with user-mode decision messaging
/create-driver-poc <driver-name> --type registryfilter Generate a registry callback filter POC
/create-driver-poc <driver-name> --type wfpcallout Generate a WFP outbound callout POC
/find-root-cause <problem> Analyze a reported symptom with 1-8 route-limited worker shards, reviewer validation, fuzz-like input/state assumption checks, and root-cause synthesis
/simulate-dashboard    Show a simulation dashboard for this workspace
/simulate-dashboard-html Generate and open an HTML simulation dashboard
/rollback [target]     Restore the workspace to a selected checkpoint, or a specific target if provided
/verify [path,...|--full] Run adaptive or full verification and suggest repair, dashboard, checkpoint, or feature workflow follow-up
/verify-dashboard [all] Show recent verification history and failure trends
/verify-dashboard-html [all] Generate and open an HTML verification dashboard report

Memory:
/evidence              Show recent evidence records and suggest verification/dashboard/source follow-up
/evidence-search <query> Search evidence records with optional filters and follow-up guidance
/evidence-show <id>    Show one evidence record and suggest the next useful action
/evidence-dashboard [query] Show an evidence dashboard with optional filters
/evidence-dashboard-html [query] Generate and open an HTML evidence dashboard
/mem                   Show recent persistent memory entries and suggest confirm/promote/verify follow-up
/mem-confirm <id>      Mark a memory as confirmed
/mem-dashboard [query] Show a persistent memory dashboard with optional filters
/mem-dashboard-html [query] Generate and open an HTML persistent memory dashboard
/mem-demote <id>       Lower a memory's importance tier
/mem-promote <id>      Raise a memory's importance tier
/mem-prune [all]       Prune persistent memory using the current retention policy
/mem-search <query>    Search persistent memory across past sessions
/mem-show <id>         Show a detailed persistent memory record by citation id
/mem-stats             Show persistent memory storage stats
/mem-tentative <id>    Mark a memory as tentative
/memory                Show loaded memory files

Selection And Review:
/clear-selection       Clear the current selected code range
/clear-selections      Clear all saved selections
/diff-selection        Show git diff limited to the current selected range
/drop-selection <n>    Remove one saved selection by number
/edit-selection <task> Run an edit-focused prompt on the current selection
/note-selection <text> Set or replace the note on the active selection
/open <path>           Open a workspace file in a separate viewer window
/review selection [...] Run the common review harness on the active selection
/selection             Show the current selected code range
/selections            List saved selections and show the active one
/tag-selection <tags>  Set comma-separated tags on the active selection
/use-selection <n>     Switch the active selection by number

Workspace Setup:
/init                  Create a starter KERNFORGE.md in the current workspace
/init config           Create a workspace .kernforge/config.json template
/init hooks            Create a workspace .kernforge/hooks.json template
/init memory-policy    Create a workspace .kernforge/memory-policy.json template
/init skill <name>     Create a starter SKILL.md in .kernforge/skills/<name>
/init verify           Create a workspace .kernforge/verify.json template
/locale-auto [on|off]  Show or change automatic locale insertion in prompts
/worktree [status|list|create|enter|attach|leave|cleanup] Manage isolated git worktrees and suggest tracked-feature follow-up

MCP And Skills:
/mcp                   Show configured MCP servers and tool status
/prompt <target> [...] Resolve an MCP prompt by server:name and optional JSON args
/prompts               Show discovered MCP prompts
/resource <target>     Read an MCP resource by server:uri-or-name
/resources             Show discovered MCP resources
/skills                Show discovered local skills

Git:
/diff                  Show git diff

Use /help <command-or-topic> for detailed help.

Special input:
!<command>             Run a shell command in the current workspace
@path/to/file          Mention a text file to inject its contents
@path/to/image.png     Mention an image file to attach it as vision input
@path.png?detail=original
                       Preserve original image resolution; supported detail values are high and original
@mcp:server:target     Mention an MCP resource by server and uri/name
\ at end of line       Continue to the next line for multiline input
`)
}

func HelpDetail(topic string) (string, bool) {
	key := normalizeHelpTopic(topic)
	switch key {
	case "":
		return "", false
	case "suggest", "suggestions", "proactive":
		return strings.TrimSpace(`
/suggest [status|list]
- Build a SituationSnapshot from conversation state, recent runtime events, docs, verification, evidence, fuzz campaign, git, checkpoint, worktree, and feature state.
- Show ranked next-action suggestions without executing them.

/suggest accept <id>
- Mark a suggestion as accepted and keep the associated command visible for manual execution.

/suggest dismiss <id>
- Dismiss a suggestion and put its dedup key on session cooldown so it does not repeat immediately.

/suggest mode <observe|suggest|confirm>
- observe: build suggestions for logs/tests only.
- suggest: append one concise next step when it is useful.
- confirm: keep suggestions explicit and confirmation-oriented for risky or costly work.

/suggest-dashboard-html
- Render the current situation and ranked suggestions as a local HTML dashboard.

/session-dashboard-html
- Generate .kernforge/session_dashboard/latest.html with session metadata, task graph status, automation due/failed state, recent thread events, changed files, background jobs, and artifact refs.
- In interactive mode, Kernforge tries to open the generated dashboard automatically.

/events [tail [n]]
- Print recent session conversation events as JSONL records with session id, workspace, provider, model, and event payload.

/events export [path]
- Write the current session event stream to .kernforge/events/<session-id>.jsonl and refresh .kernforge/events/latest.jsonl unless an explicit path is provided.
- Use this as the local app-server style event feed for dashboards, schedulers, test harnesses, and external supervisors.

/automation [list|status]
- Show saved automation slots for recurring verification and PR review, including interval schedule and due state.

/automation add recurring-verification [--every 30m|--hourly|--daily|--manual] [/verify args]
- Register a reusable verification automation. Interval schedules can be checked with /automation due and executed with /automation run-due.

/automation add pr-review [--every 1d|--manual] [/review pr]
- Register a reusable PR review automation report generator.

/automation run <id>
- Run a configured automation through the same safe command dispatcher used by accepted suggestions.

/automation due
- Show active scheduled automations whose next_run_at has passed.

/automation digest
- Summarize total, scheduled, due, failed, and paused automations, and print the due or failed items.

/automation monitor [--notify] [--webhook-url <url>]
- Print the automation digest, run due active scheduled automations, then print the post-run digest. With --notify, also write .kernforge/automation/latest_digest.md for external watchers. With --webhook-url, POST the digest JSON to an external receiver.

/automation watch [--interval 5m] [--cycles N|--once] [--notify] [--webhook-url <url>]
- Run a foreground automation monitor loop. Each cycle checks due active scheduled automations, runs them through the safe dispatcher, prints the digest, and optionally refreshes .kernforge/automation/latest_digest.md or POSTs digest JSON to a webhook. Without --cycles or --once, the loop continues until the process is interrupted.

/automation daemon-start [--interval 5m] [--notify] [--webhook-url <url>]
- Start a process-detached automation daemon that runs /automation watch through the non-interactive -command runner. State and logs are written to .kernforge/automation/daemon.json and daemon.log.

/automation daemon-status
- Show whether the detached automation daemon recorded for this workspace is running or stale.

/automation daemon-stop
- Stop the detached automation daemon recorded for this workspace and remove its state file.

/automation notify [--webhook-url <url>] [--no-file]
- Print the automation digest and write .kernforge/automation/latest_digest.md without running due automations. With --webhook-url, also POST digest JSON; --no-file sends only the webhook.

/automation run-due
- Run all due active scheduled automations through the safe command dispatcher.

/review pr [--draft-comments|--post-comments|--resolve-thread <id>|--draft-issue|--create-issue] [--label <name>] [--assignee <login>] [--milestone <name>]
- Generate a common .kernforge/reviews/latest.* PR review gate. Explicit draft/write flags also generate the existing PR comment or issue draft artifacts.
- With --github, collect current PR metadata through gh pr view --json when the gh CLI is installed and authenticated. If gh is unavailable or the branch has no PR, the report keeps the local review sections and records the GitHub reason.
- With --draft-comments, generate .kernforge/pr_review/comments.md as a safe file-level review comment draft. It does not post to GitHub.
- With --post-comments, also run gh pr review --comment --body-file .kernforge/pr_review/comments.md. This is only allowed from the explicit /review pr command, not suggestion acceptance or scheduled automation.
- With --resolve-thread <id>, run the GitHub GraphQL resolveReviewThread mutation through gh api graphql. This is only allowed from the explicit /review pr command, not suggestion acceptance or scheduled automation.
- With --draft-issue, generate .kernforge/pr_review/issue.md. With --create-issue, also run gh issue create --title ... --body-file .kernforge/pr_review/issue.md. Issue creation is explicit-only.
- Issue drafts and create calls accept repeated --label, --assignee, and --milestone values. Comma-separated labels or assignees are split and passed as repeated gh flags.
`), true
	case "goal", "goals":
		return strings.TrimSpace(`
/goal <objective>
/goal start <objective>
/goal start @GOAL.md
/goal start --file GOAL.md
- Create a Codex-style goal from inline text or a markdown file without submitting another model turn.
- Kernforge records the acceptance contract, task graph, completion criteria, and progress ledger so the goal can guide later turns.

/goal start --run <objective>
- Explicitly start Kernforge's autonomous goal loop after creating the goal.
- Kernforge asks the agent to inspect, implement, review, repair concrete review findings, verify, run final semantic review, and fix bugs without user intervention.
- Each loop iteration runs the agent, /verify --full, /completion-audit, final semantic review, and when needed /recover execute-safe.

/goal start --max-iterations N <objective>
/goal start --until-complete <objective>
/goal start --time-budget 10m <objective>
/goal start --token-budget N <objective>
/goal start --rollback-on-regression <objective>
- These options are persisted with the goal. Add --run, or use /goal run later, when you explicitly want the autonomous loop to execute.
- /goal start --until-complete is the explicit convenience form that creates the goal and runs until completion. The loop also stops on repeated no-progress or repeated failure signatures.

/goal start --no-run <objective>
- Persist the goal and write .kernforge/goals/latest.md/json without starting the autonomous loop.

/goal run [id|latest]
- Resume a pending or blocked goal and continue until completion audit and final semantic review are ready or an unrecoverable blocker is recorded.

/goal status [id|latest]
- Show the active goal, status, iteration count, latest audit state, and artifact paths.

/goal audit [id|latest]
- Re-run /completion-audit for the goal objective and attach the result to the goal state without marking it complete.

/goal complete [id|latest]
- Re-run completion audit, run the final semantic goal reviewer, and mark the goal complete only when both gates approve.

/goal cancel [id|latest]
- Mark a goal canceled without deleting its artifact history.
`), true
	case "events", "event-stream":
		return strings.TrimSpace(`
/events
/events tail [n]
- Print recent session conversation events as JSONL records.
- Each record includes session_id, workspace, provider, model, and the normalized event payload.

/events export [path]
- Write a durable JSONL event stream for the current session.
- Without a path, Kernforge writes .kernforge/events/<session-id>.jsonl and refreshes .kernforge/events/latest.jsonl.
- Use the exported stream as a local Codex App Server style feed for dashboards, schedulers, test harnesses, or external supervisors.
`), true
	case "continuity", "resume-brief", "recovery":
		return strings.TrimSpace(`
/continuity [note]
- Generate .kernforge/continuity/latest.md and latest.json as a local long-task resume and failure-recovery packet.
- The packet includes active/base workspace roots, branch, provider/model, changed files, open task graph nodes, worktree leases, active edit loop, active failure repair, latest verification failure, background jobs/bundles, recent runtime errors, artifact refs, recovery actions, next commands, and a suggested continuation prompt.
- Use it before resuming after context compaction, switching models, delegating work, or recovering from a failed verification/shell command.
- Direct !shell command failures are recorded as command_error conversation events, so /continuity can surface them without requiring you to paste the error again.
`), true
	case "completion-audit", "completion", "final-audit":
		return strings.TrimSpace(`
/completion-audit [note]
- Generate .kernforge/completion_audit/latest.md and latest.json as an explicit completion readiness gate.
- The audit checks the objective, acceptance contract, required artifacts, latest verification, open task graph nodes, active edit/failure-repair state, background jobs/bundles, recent runtime errors, and the latest coding harness report.
- Status is blocked when hard evidence is missing or failing, needs_review when warnings or uncertainty remain, and ready only when no blockers or warnings remain.
- Use this before finalizing long coding work so the final answer does not overclaim completed tasks, verification, artifacts, or background job state.
`), true
	case "recover", "failure-recovery":
		return strings.TrimSpace(`
/recover [note]
- Generate .kernforge/recovery/latest.md and latest.json as a focused failure recovery brief.
- The brief pulls from the most recent provider/tool/command error, latest verification failure, active failure repair attempt, failed/stale/running background jobs or bundles, changed files, open tasks, worktrees, and artifact refs.
- It prints a primary failure, structured diagnosis, action plan with lifecycle status, recovery actions, next commands, and a continuation prompt so a local user or -command runner can resume without pasting logs back into the model.
- Use /recover immediately after a failed shell command, failed /verify, stale background job, or provider/tool error.

/recover execute-safe [note]
- Generate the same recovery brief, then execute only safe_auto actions such as whitelisted verification reruns, /jobs inspection, /continuity, and /completion-audit.
- Shell commands are not replayed unless they pass the recovery safe-auto whitelist, for example go test, go vet, go list, git status, or git diff --check without shell chaining or redirection.
- Execution status and output are recorded in the recovery action plan and execution log.
`), true
	case "jobs", "background-jobs", "background":
		return strings.TrimSpace(`
/jobs
/jobs status
- Show persisted background shell jobs and bundles, including running, completed, failed, stale, canceled, preempted, and superseded counts.

/jobs check <job-id|latest>
- Poll one background shell job and show its latest status, exit code, log path, and output tail.

/jobs bundle <bundle-id|latest>
- Poll one background shell bundle and summarize the member jobs.

/jobs cancel <job-id|latest> [reason]
- Cancel one background shell job when it is stale, superseded, or no longer needed.

/jobs cancel-bundle <bundle-id|latest> [reason]
- Cancel every job in a background shell bundle and mark the bundle canceled.
`), true
	case "handoff", "delegation":
		return strings.TrimSpace(`
/handoff [note]
- Generate .kernforge/handoff/latest.md and latest.json as a compact delegation packet for another local agent, Codex cloud task, or human reviewer.
- The packet includes workspace, branch, provider/model, changed files, open task graph nodes, last verification summary, recent events, artifact refs, and a suggested continuation prompt.
- The command does not push, post, or run external services.

/handoff import <artifact.json|result.md>
- Import a result packet from another local agent, human reviewer, or cloud task.
- The import is normalized into .kernforge/handoff/imports/*.json and *.md, added to the conversation event log, and any completed_tasks IDs are marked completed in the TaskGraph when they match existing nodes.
`), true
	case "find-root-cause", "root-cause":
		return strings.TrimSpace(`
/find-root-cause <problem description>
- Investigate a concrete failure symptom, such as party member limits being bypassed after invite/kick churn or a Win32 service that does not stop through sc stop.
- If the symptom is too ambiguous, Kernforge prints the unclear parts and asks you to rerun /find-root-cause with a more precise prompt before starting agents.
- Borderline symptom prompts may be checked by a model classifier before agents start, so concrete Korean natural-language reports are not rejected only because a keyword heuristic missed them.
- Kernforge scans the workspace, selects likely source shards, and plans 1-8 worker agents depending on source size and count. Concurrent model calls follow the configured model route policy, so local single-model routes default to serial execution while cloud/API routes are not forced down to one request.
- Kernforge matches symptom keywords and hypothesis signals against source paths and indexed symbols before sharding.
- Workers inspect each assigned code area like a fuzzing investigation: input parameters, decoded payloads, DB/config values, cached state, counters, IDs, enum values, nullable references, and lifecycle state may be outside the code's expected range.
- Workers must structure each candidate as trigger -> invalid_state -> state_transition -> missing_guard -> user_visible_symptom.
- Reviewer passes validate each worker report against that causal chain. When more proof is needed, reviewers emit evidence_requests that route additional focused shards.
- Deterministic quality gates downgrade or reject model-approved candidates that lack causal stages, evidence files, concrete state signals, valid probes, or symptom overlap.
- Reviewer-approved candidates receive an additional deep verification pass with symbol-aware focused source excerpts before final synthesis.
- Kernforge deduplicates near-identical candidates into clusters, tracks candidate relationships, keeps code-change-aware previous rejection/disconfirmation memory as a regression prior, and asks for probes with expected signals and disproving conditions.
- The final answer summarizes plausible root causes, evidence files/functions, confidence breakdowns, concrete instrumentation, verification probes, and writes root_cause_audit.md/json artifacts.

Examples:
/find-root-cause 내 게임에서 파티원을 초대하고 추방하다 보면 파티원 제한 숫자를 넘어서서 파티원을 초대할 수 있게 돼
/find-root-cause 내 Win32 서비스 프로세스가 sc stop으로 종료되지 않아
`), true
	case "general", "hooks", "hook-reload", "trust", "override", "override-add", "override-clear":
		return strings.TrimSpace(`
General commands cover high-level runtime inspection and app control.

/config
- Show the effective runtime configuration after user config, trusted project config, env vars, and flags are merged.

/trust [status|on|off]
- Show or set whether the current project may load .kernforge/config.json and .kernforge/hooks.json.
- Trust is stored in user config (~/.kernforge/config.json) under projects; project-local config cannot mark itself trusted.

/context
- Show approximate context size, message count, summary size, and plan item count.

/exit
- Exit the CLI.

/help [topic]
- Show the grouped command list or detailed help for one command or topic.

/reload
- Reload config, memory files, skills, and MCP extensions without restarting the app.

/hook-reload
- Reload hook configuration only.

/hooks
- Show the loaded hook engine state, presets, warnings, and rule list.

/override
- Show active hook overrides for the current workspace.

/override-add <rule-id> <hours> <reason>
- Create a temporary hook override for one rule in the current workspace.
- Example:
  /override-add deny-driver-pr-with-critical-signing-or-symbol-evidence 4 urgent hotfix with manual verification completed

/override-clear <override-id|rule-id|all>
- Remove one hook override by override id, remove all overrides for a rule, or clear all overrides in the current workspace.

/status
- Show current provider, model, workspace, session id, memory counts, verification summary, and extension counts.

/version
- Show the current application version.
`), true
	case "conversation", "sessions", "session", "session-dashboard-html":
		return strings.TrimSpace(`
Conversation and session commands manage chat history and saved sessions.

/clear
- Clear the current conversation history for this session.

/compact [focus]
- Summarize older messages and shrink context. Add an optional focus to bias the summary.

/export [file]
- Export the current conversation as Markdown. Without a file path, print the export to the terminal.

/rename <name>
- Rename the current session.

/resume <session-id>
- Resume a previously saved session by id.

/session
- Show the current session id and save location.

/session-dashboard-html
- Generate and open .kernforge/session_dashboard/latest.html for the current session thread, task graph, automations, and artifact refs.

/continuity [note]
- Generate .kernforge/continuity/latest.md and latest.json for long-task resume, recovery actions, background jobs, worktrees, and next commands.

/completion-audit [note]
- Generate .kernforge/completion_audit/latest.md and latest.json with completion blockers, warnings, verification, tasks, background jobs, and artifact evidence.

/recover [note]
- Generate .kernforge/recovery/latest.md and latest.json from recent errors, verification failure, active failure repair, background jobs, open tasks, and next commands.

/sessions
- List recent saved sessions.

/tasks
- Show the current shared task list / plan items.
`), true
	case "provider", "provider status", "providers", "models", "model", "effort", "codex-auth", "codex-login", "permissions", "progress-display", "profile", "plan-review", "new-feature", "set-analysis-models", "set-specialist-model", "analyze-project", "docs-refresh", "analyze-dashboard", "analyze-performance", "specialists":
		return strings.TrimSpace(`
Provider and model commands control which model is active and how planning/review flows work.

/model
- Show all current model routing at once, including the main model, project-analysis models, and specialist subagent models.
- Common /review role models are separate. Use /review models for primary, design, security, false-positive, regression, test, and final-gate reviewers.
- In interactive mode, select which target you want to reconfigure and continue through the matching setup flow.
- Changing the main model does not overwrite explicit role model profiles. Targets marked "not configured" follow the main model until configured.
- Main profiles store their own analysis and specialist role model set. When you change analysis or specialist models through /model, the active main profile remembers those role models; activating that profile restores the full set.

/effort [target] [undefined|minimal|low|medium|high|xhigh]
- Show per-target reasoning effort when no value is provided. Empty config is displayed as undefined.
- /effort high sets the active main model effort. Use /effort analysis-worker low, /effort analysis-reviewer medium, or /effort specialist <name> high for role-specific models.
- Main profiles, analysis role profiles, and specialist profiles each store their own reasoning_effort.
- When the active main provider supports reasoning effort, the main input prompt includes effort=<current>.
- When model selection through /model, /provider, or role-specific model commands selects an effort-capable model while that target's effort is undefined, Kernforge defaults that target to low. Use /effort to change or clear it.

/permissions [mode]
- Show or change permissions. Modes: default, acceptEdits, plan, bypassPermissions.
- Codex built-in active profile ids are accepted as aliases: :workspace, :read-only, :danger-full-access.

/progress-display [auto|compact|stream]
- Show or change in-flight progress visibility.
- auto keeps durable tool/model/route ledger lines in the transcript while noisy shell tail output stays transient.
- compact keeps progress updates in the footer, and stream writes every progress update persistently.

/profile
- Show saved main provider/model profiles and each profile's stored role model set.
- If no main profile exists but a main provider/model is already selected, Kernforge saves that selection as the first profile automatically.
- In interactive mode, enter a number to activate, rN to rename, dN to delete, or pN to pin/unpin.
- In one-shot or scripted mode, /profile only lists profiles; use /profile <number>, /profile rename <number> <name>, /profile delete <number>, /profile pin <number>, or /profile unpin <number> for explicit changes.
- Use /model as the main entry point for changing main, analysis, and specialist models.
- Use /review models for common review harness role models.

/provider
- Choose and configure a provider interactively.
- You can also jump directly with /provider anthropic, /provider openai, /provider openrouter, /provider deepseek, /provider opencode, /provider opencode-go, /provider ollama, /provider codex-cli, /provider openai-codex, /provider lmstudio, /provider vllm, or /provider llama.cpp.
- LM Studio, vLLM, and llama.cpp use provider-specific local defaults unless you pass or save a base URL; custom base URLs are preserved when reconfiguring the same provider.

/provider status
- Show the active provider, normalized base URL, API key presence, and provider-specific budget visibility.
- OpenRouter performs a live key lookup and, for management keys, also shows account credits.
- DeepSeek performs a live balance lookup when an API key is configured.
- OpenAI and Anthropic show officially documented billing and usage visibility limits instead of guessing a live balance endpoint.

/codex-auth [status|login|logout|path]
- Manage Kernforge-owned OpenAI Codex OAuth tokens for the openai-codex provider.
- The default auth file is under Kernforge config, not the Codex CLI auth file. Set KERNFORGE_CODEX_AUTH_FILE to override it.
- /codex-login is a shortcut for /codex-auth login.

/review plan <task>
- Review an implementation plan through the common ReviewRun schema and gate.

/review models
- Show role-specific common review harness models and, in interactive mode, choose a reviewer role/provider/model by number.
- /review models <primary|security|false-positive|design|regression|test|final> [provider] [model] [reasoning_effort] configures one role directly.
- /review models clear <role> clears one role override.
- This is separate from /model's project-analysis and specialist subagent routes.

/new-feature <task>
- Create a tracked feature workspace under .kernforge/features/<id>, generate spec.md, plan.md, and tasks.md, then mark it as the active feature.
- Equivalent to /new-feature start <task>.

/new-feature start <task>
- Explicitly create a tracked feature and regenerate its spec/plan/tasks artifacts.

/new-feature list
- List tracked features for the current workspace.

/new-feature status [id]
- Show the active or selected tracked feature, including artifact paths and task preview.
- Planned features point to /new-feature implement; implemented features point to /verify and /new-feature close.

/new-feature plan [id]
- Regenerate spec.md, plan.md, and tasks.md for the active or selected tracked feature.

/new-feature implement [id]
- Execute the saved tracked feature plan and persist an implementation summary artifact.
- After implementation, Kernforge suggests /verify before closing the feature.

/new-feature close [id]
- Mark the active or selected tracked feature as done.
- Closing suggests /worktree cleanup when an isolated worktree is attached, plus /checkpoint feature-done.

/analyze-project [--path <dir>] [--mode map|trace|impact|surface|security|performance] [goal]
- Analyze the workspace using a conductor and multiple sub-agents, then write project analysis artifacts.
- The goal is optional: when omitted, Kernforge infers a mode-specific goal from --mode and --path.
- Non-map modes automatically reuse the most relevant previous map run as baseline architecture context when available.
- Always writes .kernforge/analysis/latest/run.json, the assistant-facing final report, deterministic docs, schema-versioned docs_manifest.json, docs_index.md, and dashboard.html.
- Generated docs include FINAL_REPORT.md, architecture, security surface, trust/data-flow graph sections with section-level stale markers, fuzz targets, verification matrix, and operations runbook.
- Generated docs are recorded as evidence and persistent memory so verification planning and fuzz target discovery can reuse them.
- After analysis, Kernforge prints an Analysis handoff with the next dashboard, fuzz campaign, target drilldown, or verification command when the generated docs support it.
- --path limits shard execution to a matching workspace directory or file prefix. Natural-language prompts such as "src/driver only" still auto-detect scope; if the prompt looks scoped but no directory matches, Kernforge shows that before confirmation.
- Modes:
  - map: default architecture map for subsystems, ownership, module boundaries, entry points, docs, dashboard, and reusable knowledge base.
  - trace: one runtime/request flow through callers, callees, dispatch points, ownership transitions, and source anchors.
  - impact: change blast radius, upstream/downstream dependencies, affected files, retest targets, and stale documentation risks.
  - surface: exposed entry surfaces such as IOCTL, RPC, parsers, handles, memory-copy paths, telemetry decoders, network inputs, and fuzz targets.
  - security: trust boundaries, validation, privileged paths, tamper-sensitive state, enforcement points, and driver/IOCTL/handle/RPC risks.
  - performance: startup cost, hot paths, blocking chains, allocation/copy pressure, contention, and profiling order.
- When omitted, mode is inferred from the goal text.

/docs-refresh
- Rebuild .kernforge/analysis/latest/docs, FINAL_REPORT.md, graph sections, graph stale markers, schema-versioned docs_manifest.json, docs_index.md, dashboard.html, and vector corpus exports from the latest saved run.json.
- Manifest readers treat missing schema_version as legacy and ignore unknown fields for additive compatibility.
- Use this after code or doc-writer changes when you do not need a full model-backed project analysis rerun.
- Refresh also re-records the generated docs as reusable evidence and memory artifacts, and rewrites run.json with the refreshed docs-backed corpus.

/analyze-dashboard [latest|path]
- Open .kernforge/analysis/latest/dashboard.html by default.
- Pass latest, a dashboard HTML file, or a directory containing dashboard.html.
- The dashboard now opens with a human-readable module/function structure map, then acts as a static document portal with cross-document search, source-anchor drilldown, graph-linked stale section diff, trust/data-flow graph context, attack-flow view, and evidence/memory follow-up commands.

/analyze-performance [focus]
- Load the latest architecture knowledge pack and performance lens, then analyze likely bottlenecks and hot paths.
- Add an optional focus such as startup, ETW, scanner, compression, upload, or memory.
- After the report, Kernforge prints a Performance handoff toward /analyze-dashboard, /verify, /simulate stealth-surface, or a hotspot /fuzz-func drilldown when an entry point is available.

/specialists
- Show the effective specialist catalog plus editable ownership and specialist worktree state for the current session.

/specialists status
- Show the same catalog explicitly, including active editable specialist assignments and worktree leases.

/specialists assign <node-id> <specialist> [glob,glob2]
- Bind one task-graph node to an editable specialist, optionally override its ownership globs, and ensure that specialist has an isolated worktree lease.
- After assignment, Kernforge points back to /specialists status and /new-feature status when a tracked feature is active.

/specialists cleanup <specialist|all>
- Remove one specialist worktree or all recorded specialist worktrees after verifying they are clean.

/set-analysis-models
- Configure dedicated worker and reviewer models used by /analyze-project.
- Use /set-analysis-models status to show the current analysis model configuration.
- Use /set-analysis-models clear to reset worker and reviewer to the main active model.

/set-specialist-model
- Configure the provider/model used by a specific specialist subagent.
- Use /set-specialist-model status to show effective specialist model routing.
- Use /set-specialist-model <specialist> <provider> [model] to set an override, or /set-specialist-model clear <specialist|all> to remove overrides.
`), true
	case "verify", "verification", "checkpoint", "checkpoints", "rollback", "verify-dashboard", "verify-dashboard-html", "checkpoint-auto", "checkpoint-diff", "set-auto-verify", "detect-verification-tools", "set-msbuild-path", "clear-msbuild-path", "set-cmake-path", "clear-cmake-path", "set-ctest-path", "clear-ctest-path", "set-ninja-path", "clear-ninja-path", "fuzz-func", "fuzz-campaign", "source-scan", "source_scan", "sourcescan", "create-driver-poc", "create_driver_poc", "createdriverpoc":
		return strings.TrimSpace(`
Verification and checkpoint commands help you validate changes and recover safely.

/verify [path,...|--full]
- Run adaptive or full verification for the current workspace.
- Paths are optional overrides used to target verification planning.
- Supports plain paths and mention-style paths such as @src/foo.cpp or @src/foo.cpp:10-40.
- Examples:
  /verify
  /verify --full
  /verify src/foo.cpp,src/bar.cpp
  /verify @Common/PEreloc.cpp --full

/verify-dashboard [all]
- Show recent verification history and failure trends in the terminal.

/verify-dashboard-html [all]
- Generate an HTML verification dashboard and try to open it.

/fuzz-func <name> [--file <path>|@<path>] [--source-scan off|focused|full]
- Resolve one function-like symbol from the latest structural_index_v2 or an on-demand workspace scan and plan a directed fuzzing run automatically.
- Kernforge expands the reachable call closure, infers parameter mutation strategy, scores the risk surface, and writes a harness scaffold plus report artifacts.
- By default, Kernforge reuses a matching /source-scan candidate or runs a focused scan over the target and reachable files before saving the plan.
- Minimal input is preferred: pass only the function name unless you need a more explicit symbol match.
- Use --file <path> when the same function name exists in multiple translation units or you want to pin the target file directly.
- You can also write @<path> as a shorter file-hint alias.
- Use --source-scan off to disable this context, --source-scan focused for the default target-scoped scan, or --source-scan full to scan the whole indexed workspace.
- If exact build settings are missing, Kernforge recovers what it can, shows the missing pieces, and asks before heuristic autonomous execution starts.

/fuzz-func --file <path> or /fuzz-func @<path>
- Analyze the selected file together with files it includes or imports, then let Kernforge choose the best input-facing function inside that file scope automatically.
- Use this when you know the file you care about but do not want to guess the best starting function by hand.

/fuzz-func --from-candidate <candidate-id>
- Load a saved /source-scan candidate and derive the focused function, source file, matcher context, and scenario hints from that record.
- The saved /fuzz-func plan keeps SourceCandidateID and SourceMatcherSlug so campaign and native feedback can update the original candidate later.

/fuzz-func list
- Show recent function fuzz planning runs for the current workspace.

/fuzz-func show [id]
- Show one saved function fuzz plan in detail. Without an id, the latest run is shown.

/fuzz-func continue [id]
- Approve a pending recovered build configuration and start autonomous fuzzing for that saved run.

/fuzz-func language [system|english]
- Show or change the /fuzz-func output language.
- Use system to follow the PC language.
- Use english to force English output regardless of the PC language.
- After a /fuzz-func run produces source-only scenarios, Kernforge prints a campaign handoff with the single next command to run.

/source-scan [status|run|list|show <id|latest>|revalidate <id|latest>]
- Run built-in source matchers for Windows kernel, C/C++ parser boundaries, Unreal RPC/trust surfaces, and telemetry parsers before native fuzzing exists.
- A run writes candidate records to ~/.kernforge/source_scan.json and artifacts under .kernforge/source_scan/<run-id>/.
- Candidates are scored and tiered from source evidence only. Treat them as source-only until /fuzz-func, /fuzz-campaign, or native verifier feedback confirms the behavior.
- After run/list/show output, Kernforge guides the next focused command as /fuzz-func --from-candidate <candidate-id>.
- revalidate attaches source-only or native verifier verdicts and can promote a candidate to needs-native or native-confirmed.

/create-driver-poc <driver-name> [--type objectfilter|minifilter|registryfilter|wfpcallout]
- Generate a new <driver-name>/ folder containing an x64-only Visual Studio solution.
- The solution has a C++20 WDM kernel driver project that builds <driver-name>.sys from Driver.cpp and a C++20 console tester project that builds <driver-name>-tester.exe.
- Both projects share shared/Ioctl.h for service names, NT device names, Win32 device paths, and the same IOCTL definition used by the driver dispatch and tester DeviceIoControl call.
- The driver uses the shared DeviceType constant when creating its device object, keeping IOCTL and device metadata under the same contract.
- Omitting --type keeps the original WDM ping POC. Specialized types generate object manager process/thread handle filtering, filesystem minifilter user-mode decision messaging, registry callback filtering, or WFP outbound callout blocking contracts.
- --type objectfilter: generate ObRegisterCallbacks process/thread handle create/duplicate filtering and strip dangerous desired access such as PROCESS_VM_WRITE and THREAD_SUSPEND_RESUME.
- --type minifilter: generate a filesystem minifilter POC with FltSendMessage, a Filter Manager communication port, IOCP-based user-mode decisions, and minifilter service instance registry setup.
- --type registryfilter: generate a CmRegisterCallbackEx registry filter POC with an IOCTL-registered registry path.
- --type wfpcallout: generate a WFP outbound callout POC with an IOCTL-registered IPv4 target.
- No INF package is generated; the tester creates or updates the demand-start SCM kernel-driver service directly.
- The tester Release build links the MT runtime.
- The generated tester resolves the .sys next to itself with a growing path buffer, creates or updates a demand-start SCM kernel-driver service, restarts an already-running service before testing to avoid stale images, opens \\.\<driver-name>, sends the shared IoctlPing value, closes the handle, then stops and deletes the service.
- Driver names must be 1-64 ASCII letters, digits, or underscores, starting with a letter. Loading unsigned x64 drivers still requires Administrator plus test-signing or an equivalent lab policy.

/fuzz-campaign [status|run|new <name>|list|show <id|latest>]
- Create and inspect campaign-level fuzzing manifests without forcing the user to remember internal ordering.
- A new campaign writes .kernforge/fuzz/<campaign-id>/manifest.json plus corpus, crashes, coverage, reports, and logs directories.
- When latest analyze-project docs include FUZZ_TARGETS.md entries, the campaign manifest seeds its initial target list from that catalog.
- /fuzz-campaign shows Kernforge's recommended next step.
- /fuzz-campaign run lets Kernforge create the campaign if needed, attach the latest useful /fuzz-func run, promote source-only scenarios into deterministic JSON seed artifacts under corpus/<run-id>/, update deduplicated finding lifecycle and coverage gap entries, ingest libFuzzer logs, llvm-cov text, LCOV, and JSON coverage summaries from run output or the campaign coverage directory, capture sanitizer reports, Windows crash dumps, Application Verifier, and Driver Verifier artifacts, and capture native run results into reports and evidence when artifacts exist.
- Campaign manifests include findings, dedup keys, duplicate counts, merged native/evidence links, parsed coverage reports, coverage gaps, run artifacts, and an artifact graph that links campaign, target, seed, native result, coverage report, sanitizer/verifier artifact, evidence, and source-anchor state.
- Native crash findings are merged by crash fingerprint, source anchor, and suspected invariant so repeated runs preserve the evidence trail without creating noisy duplicate issues.
- Coverage gaps feed the next generated FUZZ_TARGETS.md refresh and target ranking so unexercised seed targets move back toward the top automatically.
- Captured native failures feed /verify planner steps and appear in tracked feature status as a verification gate through the same finding lifecycle.
- Internal expert actions still exist for tests and recovery, but the default UX is intent-driven automation.
- Use this before moving source-only /fuzz-func findings into crash, coverage, and evidence lifecycle work.

/investigate [start|snapshot|note|stop|show|list|dashboard|dashboard-html]
- Live investigation commands now print an Investigation handoff after start, snapshot, and stop.
- The handoff points to /investigate snapshot, /simulate <profile>, /verify, or /evidence-dashboard depending on the captured state.

/simulate <profile> [target]
- Simulation runs and show output now print a Simulation handoff.
- The handoff points to /verify when findings exist, plus /simulate-dashboard and /evidence-dashboard for review.

/verify [path,...|--full]
- Verification output now prints a Verification handoff.
- Failed runs point back to /verify plus /verify-dashboard and /evidence-dashboard.
- Passing runs point to /checkpoint verified-state, and to /new-feature status or /new-feature close depending on tracked feature state.

/checkpoint [note]
- Create a workspace checkpoint snapshot. In interactive mode, Kernforge prompts for an optional note when none is provided.
- After creation, Kernforge suggests /checkpoint-diff latest and /checkpoints.

/checkpoint-auto [on|off]
- Show or change automatic checkpoint creation before edits.

/detect-verification-tools
- Detect MSBuild, CMake, CTest, and Ninja executable paths on Windows and save any newly found workspace overrides.

/set-msbuild-path <path>
/set-cmake-path <path>
/set-ctest-path <path>
/set-ninja-path <path>
- Set a workspace-specific executable path used by automatic verification when PATH is incomplete.
- Without a path, show the current value and any detected suggestion.
- Paths containing spaces are supported.
- Quote Windows paths with spaces to avoid ambiguity.
- Examples:
  /set-msbuild-path "C:\Program Files\Microsoft Visual Studio\2022\Community\MSBuild\Current\Bin\MSBuild.exe"
  /set-cmake-path "C:\Program Files\CMake\bin\cmake.exe"

/clear-msbuild-path
/clear-cmake-path
/clear-ctest-path
/clear-ninja-path
- Remove the corresponding workspace-specific executable override.

/set-auto-verify [on|off]
- Show or change automatic verification after edits.
- This is the master switch for edit-triggered verification.

/checkpoint-diff [target] [-- path[,path2]]
- Compare the current workspace to a checkpoint, optionally limited to specific paths.

/checkpoints
- List checkpoints for the current workspace.

/rollback [target]
- Restore the workspace to a selected checkpoint by default, or a specific target if provided.
`), true
	case "investigate", "investigation":
		return strings.TrimSpace(`
Investigation commands capture live Windows state and store the result as investigation sessions, evidence, and memory.

/investigate
- Show the active investigation and recent investigation status for this workspace.

/investigate start <preset> [target]
- Start a new investigation session.
- MVP presets: driver-visibility, process-visibility, provider-visibility

/investigate snapshot [target]
- Capture a live snapshot for the active investigation.

/investigate note <text>
- Add a note to the active investigation.

/investigate stop [summary]
- Complete the active investigation and write a summary to evidence/memory.

/investigate list
- Show recent investigation sessions for this workspace.

/investigate show <id>
- Show one investigation session in detail.

/investigate dashboard
- Show a dashboard-style summary of recent investigation sessions.

/investigate dashboard-html
- Generate an HTML investigation dashboard and try to open it.
`), true
	case "simulate", "simulation":
		return strings.TrimSpace(`
Simulation commands evaluate recent evidence and investigation state through lightweight risk-focused heuristics.

/simulate
- Show available simulation profiles and the most recent simulation status.

/simulate <profile> [target]
- Run one simulation profile.
- MVP profiles: tamper-surface, stealth-surface, forensic-blind-spot

/simulate list
- Show recent simulation runs for the current workspace.

/simulate show <id>
- Show one simulation result in detail.

/simulate dashboard
- Show a dashboard-style summary of recent simulation runs.

/simulate dashboard-html
- Generate an HTML simulation dashboard and try to open it.
`), true
	case "memory", "mem", "mem-search", "mem-show", "mem-promote", "mem-demote", "mem-confirm", "mem-tentative", "mem-dashboard", "mem-dashboard-html", "mem-prune", "mem-stats", "evidence", "evidence-search", "evidence-show", "evidence-dashboard", "evidence-dashboard-html":
		return strings.TrimSpace(`
Memory commands inspect and manage loaded memory files, persistent memory records from past sessions, and structured evidence extracted from verification.

/evidence
- Show recent evidence records for the current workspace.
- The strongest actionable record prints an Evidence handoff toward /verify, /evidence-dashboard, or the source dashboard.

/evidence-search <query>
- Search evidence records.
- Filters:
  kind:<verification_category|verification_artifact|verification_failure|hook_override|investigation_session|investigation_snapshot|investigation_finding|simulation_run|simulation_finding>
  category:<driver|telemetry|unreal|memory-scan>
  tag:<name>
  outcome:<passed|failed>
  severity:<low|medium|high|critical>
  signal:<name>
  risk:>=<score>
- Search results also print the same Evidence handoff when a record needs follow-up.

/evidence-show <id>
- Show one evidence record in detail.
- Failed, high-risk, simulation, investigation, or analysis evidence points to the relevant next command.

/evidence-dashboard [query]
- Show a dashboard-style evidence summary in the terminal.
- Accepts the same filters as /evidence-search.

/evidence-dashboard-html [query]
- Generate an HTML evidence dashboard and try to open it.

/memory
- Show loaded memory files.

/mem
- Show recent persistent memory entries relevant to this workspace.
- Tentative, high-risk, or verification-linked records print a Memory handoff.

/mem-search <query>
- Search persistent memory across past sessions.
- Filters:
  importance:<low|medium|high>
  trust:<tentative|confirmed>
  category:<driver|telemetry|unreal|memory-scan>
  tag:<name>
  artifact:<path-or-file>
  failure:<kind>
  severity:<low|medium|high|critical>
  signal:<name>
  risk:>=<score>
- Search results can suggest /mem-confirm, /mem-promote, /verify, or /mem-dashboard.

/mem-show <id>
- Show one persistent memory record in detail.
- The detail view prints the same confirm/promote/verify guidance when applicable.

/mem-promote <id>
/mem-demote <id>
- Raise or lower a memory record's importance tier.

/mem-confirm <id>
/mem-tentative <id>
- Mark a memory record as confirmed or tentative.

/mem-dashboard [query]
- Show a dashboard-style memory summary in the terminal.
- Accepts the same filters as /mem-search.

/mem-dashboard-html [query]
- Generate an HTML dashboard and try to open it.

/mem-prune [all]
- Apply retention policy pruning to persistent memory.

/mem-stats
- Show persistent memory storage stats.
`), true
	case "selection", "selections", "review", "edit-selection", "open":
		return strings.TrimSpace(`
Selection and review commands let you work on a focused code region instead of the whole workspace.

/open <path>
- Open a workspace file in the viewer window.

/selection
- Show the active selection.

/selections
- List saved selections and show which one is active.

/use-selection <n>
- Switch the active selection by index.

/drop-selection <n>
- Remove one saved selection.

/clear-selection
/clear-selections
- Clear the active selection or all saved selections.

/note-selection <text>
- Set or replace the note attached to the active selection.

/tag-selection <tags>
- Set comma-separated tags on the active selection.

/diff-selection
- Show git diff limited to the selected range. On Windows this prefers the internal read-only diff viewer.

/review selection [...]
- Run the common review harness on the active selection.

/edit-selection <task>
- Run an edit-focused prompt scoped to the active selection.
`), true
	case "init", "workspace", "workspace-setup", "locale-auto", "worktree":
		return strings.TrimSpace(`
Workspace setup commands generate starter files and adjust workspace-level behavior.

/init
- Create a starter KERNFORGE.md in the current workspace.

/init config
- Create a starter .kernforge/config.json template.
- Project-local config is ignored until /trust on stores a user-level trust entry for this project.

/init hooks
- Create a starter .kernforge/hooks.json template.

/init memory-policy
- Create a starter .kernforge/memory-policy.json template.

/init skill <name>
- Create a starter SKILL.md in .kernforge/skills/<name>.

/init verify
- Create a starter .kernforge/verify.json template.

/locale-auto [on|off]
- Show or change automatic locale insertion in prompts.

/worktree
- Show the current worktree isolation status for this session.

/worktree status
- Show the base workspace root, active root, configured worktree root directory, and any attached isolated worktree metadata.

/worktree list
- Show the session worktree, editable specialist worktree leases, and git worktree list --porcelain output in one terminal view.
- Use this before switching roots or resuming delegated work so edits do not cross into the wrong worktree.

/worktree create [name]
- Create and attach an isolated git worktree rooted under the configured worktree isolation root.
- The optional name influences the branch and directory slug.
- When a tracked feature is active, creation points back to /new-feature status so Kernforge can choose the right next step.

/worktree enter
- Re-enter the recorded isolated worktree after /worktree leave without creating a new branch.

/worktree attach <path> [branch]
- Attach an existing git worktree path to this session. Kernforge records it as unmanaged, switches the active root to that path, and preserves the base workspace root for later /worktree leave.

/worktree leave
- Detach from the current isolated worktree and return the session to the base workspace root without deleting the worktree.
- After leaving, Kernforge suggests /worktree status and the active feature status when present.

/worktree cleanup
- Remove the managed isolated worktree after verifying it has no uncommitted changes, then return the session to the base workspace root.
- Cleanup keeps the user oriented with /worktree status and active tracked-feature status.
`), true
	case "mcp", "resource", "resources", "prompt", "prompts", "skills":
		return strings.TrimSpace(`
MCP and skills commands expose local skills plus external MCP tools, resources, and prompts.

/skills
- Show discovered local skills.

/mcp
- Show configured MCP servers and their tool/resource/prompt counts.

/resources
- List discovered MCP resources.

/resource <target>
- Read one MCP resource by server and uri/name.

/prompts
- List discovered MCP prompts.

/prompt <target> [json]
- Resolve an MCP prompt by server:name, with optional JSON arguments.

Web research setup:
- Mark web-capable MCP servers with capabilities like "web_search" and "web_fetch" in ~/.kernforge/config.json.
- You can also put keys like "TAVILY_API_KEY", "BRAVE_SEARCH_API_KEY", and "SERPAPI_API_KEY" in user-level mcp_servers[].env.
- Project-local config and hooks are ignored until the project is trusted, and workspace-local mcp_servers stay ignored because MCP servers launch host-local commands.
- On startup, Kernforge deploys the bundled web-research MCP into ~/.kernforge/mcp/web-research-mcp.js and auto-adds that MCP to ~/.kernforge/config.json when no equivalent web-research server is configured yet.
- Once a web-search/browser MCP is configured, Kernforge can prioritize it for latest/current research requests before local file inspection.
`), true
	case "git", "diff":
		return strings.TrimSpace(`
Git commands expose lightweight repository inspection helpers.

/diff
- Show the current git diff for the workspace. On Windows this prefers the internal read-only diff viewer.
`), true
	case "help":
		return strings.TrimSpace(`
/help [topic]

- Without arguments, show the grouped command list.
- With a command name, command path, or topic, show detailed help.

Examples:
- /help verify
- /help /verify
- /help selection
- /help memory
- /help mcp
`), true
	}
	return "", false
}

func normalizeHelpTopic(topic string) string {
	value := strings.TrimSpace(strings.ToLower(topic))
	value = strings.TrimPrefix(value, "/")
	return value
}

type Mode string

const (
	ModeDefault     Mode = "default"
	ModeAcceptEdits Mode = "acceptEdits"
	ModePlan        Mode = "plan"
	ModeBypass      Mode = "bypassPermissions"
)

const (
	builtInPermissionProfileReadOnly         = ":read-only"
	builtInPermissionProfileWorkspace        = ":workspace"
	builtInPermissionProfileDangerFullAccess = ":danger-full-access"
)

type Action string

const (
	ActionRead       Action = "read"
	ActionWrite      Action = "write"
	ActionShell      Action = "shell"
	ActionShellWrite Action = "shell_write"
	ActionGit        Action = "git"
)

type PromptFunc func(question string) (bool, error)

type UserInputRequestTracker struct {
	mu       sync.Mutex
	callback func()
}

func NewUserInputRequestTracker() *UserInputRequestTracker {
	return &UserInputRequestTracker{}
}

func (t *UserInputRequestTracker) SetCallback(callback func()) func() {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	previous := t.callback
	t.callback = callback
	t.mu.Unlock()
	return previous
}

func (t *UserInputRequestTracker) MarkRequested() {
	if t == nil {
		return
	}
	t.mu.Lock()
	callback := t.callback
	t.mu.Unlock()
	if callback != nil {
		callback()
	}
}

type PermissionManager struct {
	mode              Mode
	prompt            PromptFunc
	userInputRequests *UserInputRequestTracker
	shellAllowed      bool
	gitAllowed        bool
}

func ParseMode(value string) Mode {
	mode, ok := ParseModeStrict(value)
	if !ok {
		return ModeDefault
	}
	return mode
}

func ParseModeStrict(value string) (Mode, bool) {
	switch strings.TrimSpace(value) {
	case "", string(ModeDefault):
		return ModeDefault, true
	case string(ModeAcceptEdits):
		return ModeAcceptEdits, true
	case string(ModePlan):
		return ModePlan, true
	case string(ModeBypass):
		return ModeBypass, true
	default:
		return modeForBuiltInActivePermissionProfileID(value)
	}
}

func modeForBuiltInActivePermissionProfileID(value string) (Mode, bool) {
	switch strings.TrimSpace(value) {
	case builtInPermissionProfileReadOnly:
		return ModePlan, true
	case builtInPermissionProfileWorkspace:
		return ModeDefault, true
	case builtInPermissionProfileDangerFullAccess:
		return ModeBypass, true
	default:
		return ModeDefault, false
	}
}

func normalizeConfigPermissionMode(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	mode, ok := ParseModeStrict(cfg.PermissionMode)
	if !ok {
		return invalidPermissionModeError("permission_mode", cfg.PermissionMode)
	}
	cfg.PermissionMode = string(mode)
	return nil
}

func invalidPermissionModeError(field string, value string) error {
	return fmt.Errorf("invalid %s %q (valid: %s)", field, strings.TrimSpace(value), validPermissionModes())
}

func validPermissionModes() string {
	return strings.Join([]string{
		string(ModeDefault),
		string(ModeAcceptEdits),
		string(ModePlan),
		string(ModeBypass),
		builtInPermissionProfileReadOnly,
		builtInPermissionProfileWorkspace,
		builtInPermissionProfileDangerFullAccess,
	}, ", ")
}

func activePermissionProfileIDForMode(mode Mode) string {
	switch mode {
	case ModePlan:
		return builtInPermissionProfileReadOnly
	case ModeBypass:
		return builtInPermissionProfileDangerFullAccess
	case ModeDefault, ModeAcceptEdits:
		return builtInPermissionProfileWorkspace
	default:
		return ""
	}
}

func activePermissionProfileIDForModeString(value string) string {
	mode, ok := ParseModeStrict(value)
	if !ok {
		return ""
	}
	return activePermissionProfileIDForMode(mode)
}

func activePermissionProfileSnapshotForModeString(value string) map[string]any {
	id := activePermissionProfileIDForModeString(value)
	if id == "" {
		return nil
	}
	return map[string]any{"id": id}
}

func activePermissionProfileSnapshotForMode(mode Mode) map[string]any {
	id := activePermissionProfileIDForMode(mode)
	if id == "" {
		return nil
	}
	return map[string]any{"id": id}
}

func NewPermissionManager(mode Mode, prompt PromptFunc) *PermissionManager {
	return &PermissionManager{mode: mode, prompt: prompt}
}

func (m *PermissionManager) Mode() Mode {
	return m.mode
}

func (m *PermissionManager) SetMode(mode Mode) {
	m.mode = mode
}

func (m *PermissionManager) SetUserInputRequestTracker(tracker *UserInputRequestTracker) {
	if m == nil {
		return
	}
	m.userInputRequests = tracker
}

func (m *PermissionManager) Allow(action Action, detail string) (bool, error) {
	switch m.mode {
	case ModeBypass:
		return true, nil
	case ModePlan:
		if action == ActionRead {
			return true, nil
		}
		return false, fmt.Errorf("permission denied: %s is disabled in plan mode", action)
	case ModeAcceptEdits:
		if action == ActionRead || action == ActionWrite {
			return true, nil
		}
	case ModeDefault:
		if action == ActionRead {
			return true, nil
		}
	}
	if action == ActionShell && m.shellAllowed {
		return true, nil
	}
	if action == ActionGit && m.gitAllowed {
		return true, nil
	}

	if m.prompt == nil {
		return false, fmt.Errorf("permission required for %s but no interactive prompt is available", action)
	}
	question := permissionQuestion(action, detail)
	if permissionActionSupportsAlwaysApproval(action) {
		question += " (add 'always' to allow for entire session)"
	}
	if m.userInputRequests != nil {
		m.userInputRequests.MarkRequested()
	}
	allowed, err := m.prompt(question)
	if allowed && action == ActionShell {
		m.shellAllowed = true
	}
	return allowed, err
}

func permissionQuestion(action Action, detail string) string {
	switch action {
	case ActionShellWrite:
		return fmt.Sprintf("Allow shell write? %s", detail)
	default:
		return fmt.Sprintf("Allow %s? %s", action, detail)
	}
}

func permissionActionSupportsAlwaysApproval(action Action) bool {
	switch action {
	case ActionWrite, ActionGit:
		return true
	default:
		return false
	}
}

// IsShellAllowed returns whether shell permissions have been granted for this session.
func (m *PermissionManager) IsShellAllowed() bool {
	return m.shellAllowed
}

func (m *PermissionManager) IsGitAllowed() bool {
	return m.gitAllowed
}

func (m *PermissionManager) RememberGitApproval() {
	m.gitAllowed = true
}
