package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const userConfigDirName = ".kernforge"

type PlanReviewConfig struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
}

type Config struct {
	Provider            string                `json:"provider"`
	Model               string                `json:"model"`
	BaseURL             string                `json:"base_url"`
	APIKey              string                `json:"api_key"`
	ProviderKeys        map[string]string     `json:"provider_keys,omitempty"`
	Temperature         float64               `json:"temperature"`
	MaxTokens           int                   `json:"max_tokens"`
	MaxToolIterations   int                   `json:"max_tool_iterations"`
	RequestTimeoutSecs  int                   `json:"request_timeout_seconds,omitempty"`
	MSBuildPath         string                `json:"msbuild_path,omitempty"`
	CMakePath           string                `json:"cmake_path,omitempty"`
	CTestPath           string                `json:"ctest_path,omitempty"`
	NinjaPath           string                `json:"ninja_path,omitempty"`
	Command             string                `json:"command,omitempty"`
	PermissionMode      string                `json:"permission_mode"`
	Shell               string                `json:"shell"`
	SessionDir          string                `json:"session_dir"`
	AutoCompactChars    int                   `json:"auto_compact_chars"`
	AutoCheckpointEdits *bool                 `json:"auto_checkpoint_edits,omitempty"`
	AutoVerify          *bool                 `json:"auto_verify,omitempty"`
	AutoLocale          *bool                 `json:"auto_locale,omitempty"`
	HooksEnabled        *bool                 `json:"hooks_enabled,omitempty"`
	HookPresets         []string              `json:"hook_presets,omitempty"`
	HooksFailClosed     *bool                 `json:"hooks_fail_closed,omitempty"`
	MemoryFiles         []string              `json:"memory_files"`
	SkillPaths          []string              `json:"skill_paths,omitempty"`
	EnabledSkills       []string              `json:"enabled_skills,omitempty"`
	MCPServers          []MCPServerConfig     `json:"mcp_servers,omitempty"`
	Profiles            []Profile             `json:"profiles,omitempty"`
	ProjectAnalysis     ProjectAnalysisConfig `json:"project_analysis,omitempty"`
	PlanReview          *PlanReviewConfig     `json:"plan_review,omitempty"`
	ReviewProfiles      []Profile             `json:"review_profiles,omitempty"`
}

type Profile struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	Pinned   bool   `json:"pinned,omitempty"`
}

func DefaultConfig(cwd string) Config {
	return Config{
		Provider:            "",
		Model:               "",
		Temperature:         0.2,
		MaxTokens:           4096,
		MaxToolIterations:   16,
		RequestTimeoutSecs:  1200,
		PermissionMode:      "default",
		Shell:               defaultShell(),
		SessionDir:          filepath.Join(userConfigDir(), "sessions"),
		AutoCompactChars:    45000,
		AutoCheckpointEdits: boolPtr(false),
		AutoVerify:          boolPtr(true),
		AutoLocale:          boolPtr(true),
		HooksEnabled:        boolPtr(true),
		HooksFailClosed:     boolPtr(false),
	}
}

func LoadConfig(cwd string) (Config, error) {
	cfg := DefaultConfig(cwd)
	for _, path := range configSearchPaths(cwd) {
		if err := mergeConfigFile(&cfg, path); err != nil {
			return cfg, err
		}
	}
	applyEnv(&cfg)
	normalizeConfigPaths(&cfg)
	if err := EnsureUserConfig(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
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

func mergeConfigFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var patch Config
	if err := json.Unmarshal(data, &patch); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	mergeConfig(cfg, patch)
	return nil
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
			if v != "" {
				dst.ProviderKeys[k] = v
			}
		}
	}
	if src.Temperature != 0 {
		dst.Temperature = src.Temperature
	}
	if src.MaxTokens != 0 {
		dst.MaxTokens = src.MaxTokens
	}
	if src.MaxToolIterations != 0 {
		dst.MaxToolIterations = src.MaxToolIterations
	}
	if src.RequestTimeoutSecs != 0 {
		dst.RequestTimeoutSecs = src.RequestTimeoutSecs
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
		dst.MCPServers = append([]MCPServerConfig(nil), src.MCPServers...)
	}
	if len(src.Profiles) > 0 {
		dst.Profiles = append([]Profile(nil), src.Profiles...)
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
	if src.PlanReview != nil {
		copy := *src.PlanReview
		dst.PlanReview = &copy
	}
	if len(src.ReviewProfiles) > 0 {
		dst.ReviewProfiles = append([]Profile(nil), src.ReviewProfiles...)
	}
}

func applyEnv(cfg *Config) {
	envString("KERNFORGE_PROVIDER", &cfg.Provider)
	envString("KERNFORGE_MODEL", &cfg.Model)
	envString("KERNFORGE_BASE_URL", &cfg.BaseURL)
	envString("KERNFORGE_API_KEY", &cfg.APIKey)
	envString("KERNFORGE_PERMISSION_MODE", &cfg.PermissionMode)
	envString("KERNFORGE_SHELL", &cfg.Shell)
	envString("KERNFORGE_SESSION_DIR", &cfg.SessionDir)
	envInt("KERNFORGE_REQUEST_TIMEOUT_SECONDS", &cfg.RequestTimeoutSecs)
	envString("KERNFORGE_MSBUILD_PATH", &cfg.MSBuildPath)
	envString("KERNFORGE_CMAKE_PATH", &cfg.CMakePath)
	envString("KERNFORGE_CTEST_PATH", &cfg.CTestPath)
	envString("KERNFORGE_NINJA_PATH", &cfg.NinjaPath)
	envBool("KERNFORGE_AUTO_CHECKPOINT_EDITS", &cfg.AutoCheckpointEdits)
	envBool("KERNFORGE_AUTO_VERIFY", &cfg.AutoVerify)
	envBool("KERNFORGE_AUTO_LOCALE", &cfg.AutoLocale)
	envBool("KERNFORGE_HOOKS_ENABLED", &cfg.HooksEnabled)
	envBool("KERNFORGE_HOOKS_FAIL_CLOSED", &cfg.HooksFailClosed)

	switch strings.ToLower(cfg.Provider) {
	case "anthropic":
		if cfg.APIKey == "" {
			envString("ANTHROPIC_API_KEY", &cfg.APIKey)
		}
	case "openrouter":
		if cfg.BaseURL == "" {
			cfg.BaseURL = normalizeOpenRouterBaseURL("")
		}
		if cfg.APIKey == "" {
			envString("OPENROUTER_API_KEY", &cfg.APIKey)
		}
	case "ollama":
		if cfg.BaseURL == "" {
			envString("OLLAMA_HOST", &cfg.BaseURL)
		}
		if cfg.APIKey == "" {
			envString("OLLAMA_API_KEY", &cfg.APIKey)
		}
	case "openai", "openai-compatible":
		if cfg.APIKey == "" {
			envString("OPENAI_API_KEY", &cfg.APIKey)
		}
	}
}

func normalizeConfigPaths(cfg *Config) {
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
		cfg.ProjectAnalysis.WorkerProfile.BaseURL = normalizeProfileBaseURL(cfg.ProjectAnalysis.WorkerProfile.Provider, cfg.ProjectAnalysis.WorkerProfile.BaseURL)
	}
	if cfg.ProjectAnalysis.ReviewerProfile != nil {
		cfg.ProjectAnalysis.ReviewerProfile.BaseURL = normalizeProfileBaseURL(cfg.ProjectAnalysis.ReviewerProfile.Provider, cfg.ProjectAnalysis.ReviewerProfile.BaseURL)
	}
	if strings.EqualFold(cfg.Provider, "ollama") && strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = normalizeOllamaBaseURL("")
	}
	if strings.EqualFold(cfg.Provider, "openrouter") && strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = normalizeOpenRouterBaseURL("")
	}
	for i, server := range cfg.MCPServers {
		cfg.MCPServers[i].Name = strings.TrimSpace(server.Name)
		cfg.MCPServers[i].Command = strings.TrimSpace(server.Command)
		if strings.TrimSpace(server.Cwd) != "" {
			cfg.MCPServers[i].Cwd = expandHome(server.Cwd)
		}
	}
	for i, profile := range cfg.Profiles {
		cfg.Profiles[i].BaseURL = normalizeProfileBaseURL(profile.Provider, profile.BaseURL)
	}
}

func SaveUserConfig(cfg Config) error {
	normalizeConfigPaths(&cfg)
	if err := os.MkdirAll(userConfigDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(userConfigPath(), data, 0o644)
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
	switch strings.ToLower(strings.TrimSpace(provider)) {
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
	default:
		return strings.TrimSpace(baseURL)
	}
}

func profileName(provider, model string) string {
	return strings.TrimSpace(provider) + " / " + strings.TrimSpace(model)
}

func EnsureUserConfig(cfg Config) error {
	if err := os.MkdirAll(userConfigDir(), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(userConfigPath()); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return SaveUserConfig(cfg)
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

func configAutoVerify(cfg Config) bool {
	if cfg.AutoVerify == nil {
		return true
	}
	return *cfg.AutoVerify
}

func configMaxToolIterations(cfg Config) int {
	if cfg.MaxToolIterations <= 0 {
		return 16
	}
	return cfg.MaxToolIterations
}

func configRequestTimeout(cfg Config) time.Duration {
	if cfg.RequestTimeoutSecs > 0 {
		return time.Duration(cfg.RequestTimeoutSecs) * time.Second
	}
	return 20 * time.Minute
}

func configAutoLocale(cfg Config) bool {
	if cfg.AutoLocale == nil {
		return true
	}
	return *cfg.AutoLocale
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

func InitWorkspaceConfigTemplate() string {
	sample := struct {
		AutoCheckpointEdits *bool             `json:"auto_checkpoint_edits,omitempty"`
		AutoVerify          *bool             `json:"auto_verify,omitempty"`
		RequestTimeoutSecs  int               `json:"request_timeout_seconds,omitempty"`
		MSBuildPath         string            `json:"msbuild_path,omitempty"`
		CMakePath           string            `json:"cmake_path,omitempty"`
		CTestPath           string            `json:"ctest_path,omitempty"`
		NinjaPath           string            `json:"ninja_path,omitempty"`
		HooksEnabled        *bool             `json:"hooks_enabled,omitempty"`
		HookPresets         []string          `json:"hook_presets,omitempty"`
		SkillPaths          []string          `json:"skill_paths,omitempty"`
		EnabledSkills       []string          `json:"enabled_skills,omitempty"`
		MCPServers          []MCPServerConfig `json:"mcp_servers,omitempty"`
	}{
		AutoCheckpointEdits: boolPtr(false),
		AutoVerify:          boolPtr(true),
		RequestTimeoutSecs:  1200,
		MSBuildPath:         "",
		CMakePath:           "",
		CTestPath:           "",
		NinjaPath:           "",
		HooksEnabled:        boolPtr(true),
		HookPresets:         []string{},
		SkillPaths:          []string{"./.kernforge/skills"},
		EnabledSkills:       []string{},
		MCPServers: []MCPServerConfig{{
			Name:     "example",
			Command:  "node",
			Args:     []string{"path/to/server.js"},
			Cwd:      ".",
			Disabled: true,
		}},
	}
	data, err := json.MarshalIndent(sample, "", "  ")
	if err != nil {
		return "{\n  \"skill_paths\": [\"./.kernforge/skills\"]\n}\n"
	}
	return string(data) + "\n"
}

type Command struct {
	Name string
	Args string
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
	cmd := Command{Name: strings.ToLower(strings.TrimSpace(parts[0]))}
	if len(parts) == 2 {
		cmd.Args = strings.TrimSpace(parts[1])
	}
	return cmd, true
}

func HelpText() string {
	return strings.TrimSpace(`
General:
/config                Show effective runtime config
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
/tasks                 Show the current task list

Provider And Models:
/do-plan-review <task> Generate and iteratively review an implementation plan, then execute
/new-feature <task>    Create tracked feature artifacts and generate spec/plan/tasks
/analyze-project <goal> Analyze the workspace with one conductor and multiple sub-agents, then write a document
/analyze-performance [focus] Analyze likely performance bottlenecks using the latest architecture knowledge pack
/set-analysis-models   Configure worker/reviewer models for /analyze-project
/model [name]          Show or change the active model
/permissions [mode]          Show or change permissions: default, acceptEdits, plan, bypassPermissions
/set-max-tool-iterations <n> Set the maximum tool iteration count per request
/profile               Show recent provider/model profiles and switch to one
/profile-review        Show and manage saved review profiles
/provider              Choose and configure a provider
/set-plan-review [provider] Configure the reviewer model for plan review (interactive)

Verification And Checkpoints:
/checkpoint [note]     Create a workspace checkpoint snapshot, optionally with a note
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
/investigate [subcommand] Manage live investigation sessions and snapshots
/investigate-dashboard  Show an investigation dashboard for this workspace
/investigate-dashboard-html Generate and open an HTML investigation dashboard
/simulate [profile]   Run risk-oriented simulation profiles against recent evidence and investigations
/simulate-dashboard    Show a simulation dashboard for this workspace
/simulate-dashboard-html Generate and open an HTML simulation dashboard
/rollback [target]     Restore the workspace to a selected checkpoint, or a specific target if provided
/verify [path,...|--full] Run adaptive or full verification for this workspace or selected paths
/verify-dashboard [all] Show recent verification history and failure trends
/verify-dashboard-html [all] Generate and open an HTML verification dashboard report

Memory:
/evidence              Show recent evidence records for this workspace
/evidence-search <query> Search evidence records with optional filters
/evidence-show <id>    Show one evidence record in detail
/evidence-dashboard [query] Show an evidence dashboard with optional filters
/evidence-dashboard-html [query] Generate and open an HTML evidence dashboard
/mem                   Show recent persistent memory entries for this workspace
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
/review-selection [...] Run a review-focused prompt on the current selection
/review-selections [...] Run a review-focused prompt across saved selections
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
@mcp:server:target     Mention an MCP resource by server and uri/name
\ at end of line       Continue to the next line for multiline input
`)
}

func HelpDetail(topic string) (string, bool) {
	key := normalizeHelpTopic(topic)
	switch key {
	case "":
		return "", false
	case "general", "hooks", "hook-reload", "override", "override-add", "override-clear":
		return strings.TrimSpace(`
General commands cover high-level runtime inspection and app control.

/config
- Show the effective runtime configuration after global config, workspace config, env vars, and flags are merged.

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
	case "conversation", "sessions", "session":
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

/sessions
- List recent saved sessions.

/tasks
- Show the current shared task list / plan items.
`), true
	case "provider", "providers", "models", "model", "permissions", "profile", "profile-review", "plan-review", "do-plan-review", "new-feature", "set-plan-review", "set-analysis-models", "analyze-project", "analyze-performance":
		return strings.TrimSpace(`
Provider and model commands control which model is active and how planning/review flows work.

/model [name]
- Show the active model or switch to a new one.

/permissions [mode]
- Show or change permissions. Modes: default, acceptEdits, plan, bypassPermissions.

/profile
- Show saved provider/model profiles and switch to one interactively.

/profile-review
- Show and manage saved reviewer profiles for plan review.

/provider
- Choose and configure a provider interactively.

/set-plan-review [provider]
- Configure the reviewer model used by /do-plan-review.

/do-plan-review <task>
- Ask one model to produce a plan, have a reviewer model critique it, iterate, then optionally execute the final plan.

/new-feature <task>
- Create a tracked feature workspace under .kernforge/features/<id>, generate spec.md, plan.md, and tasks.md, then mark it as the active feature.
- Equivalent to /new-feature start <task>.

/new-feature start <task>
- Explicitly create a tracked feature and regenerate its spec/plan/tasks artifacts.

/new-feature list
- List tracked features for the current workspace.

/new-feature status [id]
- Show the active or selected tracked feature, including artifact paths and task preview.

/new-feature plan [id]
- Regenerate spec.md, plan.md, and tasks.md for the active or selected tracked feature.

/new-feature implement [id]
- Execute the saved tracked feature plan and persist an implementation summary artifact.

/new-feature close [id]
- Mark the active or selected tracked feature as done.

/analyze-project <goal>
- Analyze the workspace using a conductor and multiple sub-agents, then write a project document.

/analyze-performance [focus]
- Load the latest architecture knowledge pack and performance lens, then analyze likely bottlenecks and hot paths.
- Add an optional focus such as startup, ETW, scanner, compression, upload, or memory.

/set-analysis-models
- Configure dedicated worker and reviewer models used by /analyze-project.
- Use /set-analysis-models status to show the current analysis model configuration.
- Use /set-analysis-models clear to reset worker and reviewer to the main active model.
`), true
	case "verify", "verification", "checkpoint", "checkpoints", "rollback", "verify-dashboard", "verify-dashboard-html", "checkpoint-auto", "checkpoint-diff", "set-auto-verify", "detect-verification-tools", "set-msbuild-path", "clear-msbuild-path", "set-cmake-path", "clear-cmake-path", "set-ctest-path", "clear-ctest-path", "set-ninja-path", "clear-ninja-path":
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

/checkpoint [note]
- Create a workspace checkpoint snapshot. In interactive mode, Kernforge prompts for an optional note when none is provided.

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

/evidence-show <id>
- Show one evidence record in detail.

/evidence-dashboard [query]
- Show a dashboard-style evidence summary in the terminal.
- Accepts the same filters as /evidence-search.

/evidence-dashboard-html [query]
- Generate an HTML evidence dashboard and try to open it.

/memory
- Show loaded memory files.

/mem
- Show recent persistent memory entries relevant to this workspace.

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

/mem-show <id>
- Show one persistent memory record in detail.

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
	case "selection", "selections", "review", "review-selection", "review-selections", "edit-selection", "open":
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

/review-selection [...]
/review-selections [...]
- Run review-focused prompts on the active selection or multiple saved selections.

/edit-selection <task>
- Run an edit-focused prompt scoped to the active selection.
`), true
	case "init", "workspace", "workspace-setup", "locale-auto":
		return strings.TrimSpace(`
Workspace setup commands generate starter files and adjust workspace-level behavior.

/init
- Create a starter KERNFORGE.md in the current workspace.

/init config
- Create a starter .kernforge/config.json template.

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

type Action string

const (
	ActionRead  Action = "read"
	ActionWrite Action = "write"
	ActionShell Action = "shell"
)

type PromptFunc func(question string) (bool, error)

type PermissionManager struct {
	mode         Mode
	prompt       PromptFunc
	shellAllowed bool
}

func ParseMode(value string) Mode {
	switch strings.TrimSpace(value) {
	case "", string(ModeDefault):
		return ModeDefault
	case string(ModeAcceptEdits):
		return ModeAcceptEdits
	case string(ModePlan):
		return ModePlan
	case string(ModeBypass):
		return ModeBypass
	default:
		return ModeDefault
	}
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

	if m.prompt == nil {
		return false, fmt.Errorf("permission required for %s but no interactive prompt is available", action)
	}
	question := fmt.Sprintf("Allow %s? %s", action, detail)
	if action != ActionShell {
		question += " (add 'always' to allow for entire session)"
	}
	allowed, err := m.prompt(question)
	if allowed && action == ActionShell {
		m.shellAllowed = true
	}
	return allowed, err
}

// IsShellAllowed returns whether shell permissions have been granted for this session.
func (m *PermissionManager) IsShellAllowed() bool {
	return m.shellAllowed
}
