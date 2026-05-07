package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInitWorkspaceConfigTemplateIsValidJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := t.TempDir()
	scriptPath := filepath.Join(workspace, ".kernforge", "mcp", "web-research-mcp.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("console.log('ok');\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	text := InitWorkspaceConfigTemplate(workspace)
	var decoded struct {
		SkillPaths        []string                  `json:"skill_paths"`
		EnabledSkills     []string                  `json:"enabled_skills"`
		MCPServers        []MCPServerConfig         `json:"mcp_servers"`
		Specialists       SpecialistSubagentsConfig `json:"specialists"`
		WorktreeIsolation WorktreeIsolationConfig   `json:"worktree_isolation"`
	}
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("template must be valid json: %v\n%s", err, text)
	}
	if len(decoded.SkillPaths) != 1 || decoded.SkillPaths[0] != "./.kernforge/skills" {
		t.Fatalf("unexpected skill paths: %#v", decoded.SkillPaths)
	}
	if len(decoded.MCPServers) != 2 {
		t.Fatalf("expected two example mcp servers, got %#v", decoded.MCPServers)
	}
	if !decoded.MCPServers[0].Disabled {
		t.Fatalf("expected generic example server to stay disabled, got %#v", decoded.MCPServers[0])
	}
	if decoded.MCPServers[1].Name != "web-research" {
		t.Fatalf("expected web-research example, got %#v", decoded.MCPServers[1])
	}
	if decoded.MCPServers[1].Disabled {
		t.Fatalf("expected bundled web-research server to be enabled by default, got %#v", decoded.MCPServers[1])
	}
	if len(decoded.MCPServers[1].Args) != 1 || decoded.MCPServers[1].Args[0] != ".kernforge/mcp/web-research-mcp.js" {
		t.Fatalf("expected bundled web-research path, got %#v", decoded.MCPServers[1].Args)
	}
	if decoded.MCPServers[1].Env["TAVILY_API_KEY"] != "" || decoded.MCPServers[1].Env["BRAVE_SEARCH_API_KEY"] != "" || decoded.MCPServers[1].Env["SERPAPI_API_KEY"] != "" {
		t.Fatalf("expected empty web research env placeholders, got %#v", decoded.MCPServers[1].Env)
	}
	if len(decoded.MCPServers[1].Capabilities) != 2 || decoded.MCPServers[1].Capabilities[0] != "web_search" || decoded.MCPServers[1].Capabilities[1] != "web_fetch" {
		t.Fatalf("expected web capability tags in template, got %#v", decoded.MCPServers[1].Capabilities)
	}
	if decoded.Specialists.Enabled == nil || !*decoded.Specialists.Enabled {
		t.Fatalf("expected specialists to be enabled in template, got %#v", decoded.Specialists)
	}
	if decoded.WorktreeIsolation.Enabled == nil || *decoded.WorktreeIsolation.Enabled {
		t.Fatalf("expected worktree isolation to default off in template, got %#v", decoded.WorktreeIsolation)
	}
	if decoded.WorktreeIsolation.BranchPrefix != "kernforge/" {
		t.Fatalf("expected worktree branch prefix, got %#v", decoded.WorktreeIsolation)
	}
}

func TestInitWorkspaceConfigTemplateUsesDeployedWebResearchMCPWhenWorkspaceScriptIsAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	globalScript := filepath.Join(home, ".kernforge", "mcp", "web-research-mcp.js")
	if err := os.MkdirAll(filepath.Dir(globalScript), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(globalScript, []byte("console.log('global');\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	text := InitWorkspaceConfigTemplate(t.TempDir())
	var decoded struct {
		MCPServers []MCPServerConfig `json:"mcp_servers"`
	}
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("template must be valid json: %v\n%s", err, text)
	}
	if len(decoded.MCPServers) != 2 {
		t.Fatalf("expected two example mcp servers, got %#v", decoded.MCPServers)
	}
	if decoded.MCPServers[1].Disabled {
		t.Fatalf("expected deployed web-research server to be enabled, got %#v", decoded.MCPServers[1])
	}
	if len(decoded.MCPServers[1].Args) != 1 || decoded.MCPServers[1].Args[0] != filepath.ToSlash(globalScript) {
		t.Fatalf("expected deployed web-research path, got %#v", decoded.MCPServers[1].Args)
	}
}

func TestInitWorkspaceConfigTemplateKeepsPlaceholderWhenNoWebResearchScriptIsAvailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	text := InitWorkspaceConfigTemplate(t.TempDir())
	var decoded struct {
		MCPServers []MCPServerConfig `json:"mcp_servers"`
	}
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("template must be valid json: %v\n%s", err, text)
	}
	if len(decoded.MCPServers) != 2 {
		t.Fatalf("expected two example mcp servers, got %#v", decoded.MCPServers)
	}
	if !decoded.MCPServers[1].Disabled {
		t.Fatalf("expected placeholder web-research server to stay disabled, got %#v", decoded.MCPServers[1])
	}
	if len(decoded.MCPServers[1].Args) != 1 || decoded.MCPServers[1].Args[0] != "path/to/web-research-mcp.js" {
		t.Fatalf("expected placeholder web-research path, got %#v", decoded.MCPServers[1].Args)
	}
}

func TestEnsureUserConfigDeploysBundledWebResearchMCP(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if err := EnsureUserConfig(DefaultConfig(t.TempDir())); err != nil {
		t.Fatalf("EnsureUserConfig: %v", err)
	}
	deployed := filepath.Join(home, ".kernforge", "mcp", "web-research-mcp.js")
	data, err := os.ReadFile(deployed)
	if err != nil {
		t.Fatalf("ReadFile deployed script: %v", err)
	}
	if !strings.Contains(string(data), "search_web") || !strings.Contains(string(data), "fetch_url") {
		t.Fatalf("expected deployed script contents, got %q", string(data))
	}
	cfgData, err := os.ReadFile(filepath.Join(home, ".kernforge", "config.json"))
	if err != nil {
		t.Fatalf("ReadFile config: %v", err)
	}
	var cfg struct {
		MCPServers []MCPServerConfig `json:"mcp_servers"`
	}
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		t.Fatalf("Unmarshal config: %v", err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("expected auto-added web research server, got %#v", cfg.MCPServers)
	}
	if cfg.MCPServers[0].Name != "web-research" || len(cfg.MCPServers[0].Args) != 1 || cfg.MCPServers[0].Args[0] != filepath.ToSlash(deployed) {
		t.Fatalf("expected auto-added deployed web research server, got %#v", cfg.MCPServers[0])
	}
	if cfg.MCPServers[0].Env["TAVILY_API_KEY"] != "" || cfg.MCPServers[0].Env["BRAVE_SEARCH_API_KEY"] != "" || cfg.MCPServers[0].Env["SERPAPI_API_KEY"] != "" {
		t.Fatalf("expected empty web research env placeholders, got %#v", cfg.MCPServers[0].Env)
	}
}

func TestEnsureUserConfigDoesNotOverwriteExistingProfiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	existing := DefaultConfig(t.TempDir())
	existing.Profiles = []Profile{
		{Name: "old-main", Provider: "openai", Model: "gpt-old"},
	}
	existing.ReviewProfiles = []Profile{
		{Name: "old-review", Provider: "anthropic", Model: "claude-old"},
	}
	if err := SaveUserConfig(existing); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}

	replacement := DefaultConfig(t.TempDir())
	replacement.Profiles = []Profile{
		{Name: "new-main", Provider: "ollama", Model: "llama3"},
	}
	replacement.ReviewProfiles = []Profile{
		{Name: "new-review", Provider: "openai", Model: "gpt-new"},
	}
	if err := EnsureUserConfig(replacement); err != nil {
		t.Fatalf("EnsureUserConfig: %v", err)
	}

	loaded, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(loaded.Profiles) != 1 || loaded.Profiles[0].Name != "old-main" {
		t.Fatalf("expected existing main profile to remain, got %#v", loaded.Profiles)
	}
	if len(loaded.ReviewProfiles) != 1 || loaded.ReviewProfiles[0].Name != "old-review" {
		t.Fatalf("expected existing review profile to remain, got %#v", loaded.ReviewProfiles)
	}
}

func TestLoadConfigBackfillsLegacyAPIKeyIntoProviderKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	configPath := filepath.Join(home, ".kernforge", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("{\n  \"provider\": \"openai\",\n  \"api_key\": \"legacy-key\"\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ProviderKeys["openai"] != "legacy-key" {
		t.Fatalf("expected legacy api_key to backfill provider_keys, got %#v", cfg.ProviderKeys)
	}
}

func TestLoadConfigMergesUserAndWorkspaceProfiles(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	userCfg := DefaultConfig(workspace)
	userCfg.Profiles = []Profile{
		{Name: "user-main-a", Provider: "openai", Model: "gpt-a"},
		{Name: "user-main-b", Provider: "openrouter", Model: "deepseek/deepseek-v4-pro"},
	}
	userCfg.ReviewProfiles = []Profile{
		{Name: "user-review", Provider: "openai", Model: "gpt-review"},
	}
	if err := SaveUserConfig(userCfg); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}
	if err := SaveWorkspaceConfigOverrides(workspace, map[string]any{
		"profiles": []Profile{
			{Name: "workspace-main", Provider: "ollama", Model: "llama3"},
		},
		"review_profiles": []Profile{
			{Name: "workspace-review", Provider: "anthropic", Model: "claude-review"},
		},
	}); err != nil {
		t.Fatalf("SaveWorkspaceConfigOverrides: %v", err)
	}

	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(loaded.Profiles) != 3 {
		t.Fatalf("expected user and workspace main profiles to merge, got %#v", loaded.Profiles)
	}
	for _, want := range []string{"workspace-main", "user-main-a", "user-main-b"} {
		if !profileListContainsName(loaded.Profiles, want) {
			t.Fatalf("expected merged profiles to contain %q, got %#v", want, loaded.Profiles)
		}
	}
	if len(loaded.ReviewProfiles) != 2 {
		t.Fatalf("expected user and workspace review profiles to merge, got %#v", loaded.ReviewProfiles)
	}
	for _, want := range []string{"workspace-review", "user-review"} {
		if !profileListContainsName(loaded.ReviewProfiles, want) {
			t.Fatalf("expected merged review profiles to contain %q, got %#v", want, loaded.ReviewProfiles)
		}
	}
}

func TestLoadConfigRestoresActiveProfileRoleModels(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	active := Profile{
		Name:     "deepseek-main",
		Provider: "openrouter",
		Model:    "deepseek/deepseek-v4-pro",
		BaseURL:  normalizeOpenRouterBaseURL(""),
		RoleModels: &ProfileRoleModels{
			AnalysisWorker: &Profile{
				Name:            "deepseek-worker",
				Provider:        "deepseek",
				Model:           "deepseek-reasoner",
				ReasoningEffort: "medium",
			},
			PlanReviewer: &Profile{
				Name:            "reviewer",
				Provider:        "openai-codex",
				Model:           "gpt-review",
				ReasoningEffort: "high",
			},
			Specialists: []SpecialistSubagentProfile{
				{Name: "planner", Provider: "openai-codex", Model: "gpt-specialist", ReasoningEffort: "xhigh"},
			},
		},
	}
	cfg := DefaultConfig(workspace)
	cfg.Provider = "openrouter"
	cfg.Model = "z-ai/glm-5.1"
	cfg.BaseURL = normalizeOpenRouterBaseURL("")
	cfg.Profiles = []Profile{
		active,
		{Name: "zai-main", Provider: "openrouter", Model: "z-ai/glm-5.1", BaseURL: normalizeOpenRouterBaseURL("")},
	}
	cfg.Specialists.Profiles = []SpecialistSubagentProfile{{
		Name:            "planner",
		Description:     "existing custom planner metadata",
		Provider:        "openai-codex",
		Model:           "stale-specialist",
		ReasoningEffort: "low",
	}}
	cfg.ActiveProfileKey = configProfileKey(active)
	if err := SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}

	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Provider != "openrouter" || loaded.Model != "deepseek/deepseek-v4-pro" {
		t.Fatalf("expected active profile main model, got %s / %s", loaded.Provider, loaded.Model)
	}
	if loaded.ProjectAnalysis.WorkerProfile == nil || loaded.ProjectAnalysis.WorkerProfile.Model != "deepseek-reasoner" || loaded.ProjectAnalysis.WorkerProfile.ReasoningEffort != "medium" {
		t.Fatalf("expected active profile analysis worker, got %#v", loaded.ProjectAnalysis.WorkerProfile)
	}
	if loaded.PlanReview == nil || loaded.PlanReview.Model != "gpt-review" || loaded.PlanReview.ReasoningEffort != "high" {
		t.Fatalf("expected active profile plan reviewer, got %#v", loaded.PlanReview)
	}
	if len(loaded.Specialists.Profiles) != 1 || loaded.Specialists.Profiles[0].Model != "gpt-specialist" || loaded.Specialists.Profiles[0].ReasoningEffort != "xhigh" {
		t.Fatalf("expected active profile specialist model, got %#v", loaded.Specialists.Profiles)
	}
	if loaded.Specialists.Profiles[0].Description != "existing custom planner metadata" {
		t.Fatalf("expected existing specialist metadata to be preserved, got %#v", loaded.Specialists.Profiles[0])
	}
}

func TestLoadConfigRestoresSingleModelActiveProfileAndClearsAnalysisRoles(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	active := Profile{
		Name:       "single-main",
		Provider:   "openrouter",
		Model:      "deepseek/deepseek-v4-pro",
		BaseURL:    normalizeOpenRouterBaseURL(""),
		RoleModels: &ProfileRoleModels{},
	}
	cfg := DefaultConfig(workspace)
	cfg.Provider = "openai"
	cfg.Model = "gpt-stale"
	cfg.PlanReview = &PlanReviewConfig{Provider: "openai", Model: "gpt-stale-review"}
	cfg.ProjectAnalysis.WorkerProfile = &Profile{Name: "stale-worker", Provider: "openai", Model: "gpt-stale-worker"}
	cfg.ProjectAnalysis.ReviewerProfile = &Profile{Name: "stale-reviewer", Provider: "openai", Model: "gpt-stale-reviewer"}
	cfg.Specialists.Profiles = []SpecialistSubagentProfile{{
		Name:            "planner",
		Description:     "custom planner metadata",
		Provider:        "openai-codex",
		Model:           "stale-specialist",
		ReasoningEffort: "high",
	}}
	cfg.Profiles = []Profile{active}
	cfg.ActiveProfileKey = configProfileKey(active)
	if err := SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}

	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Provider != "openrouter" || loaded.Model != "deepseek/deepseek-v4-pro" {
		t.Fatalf("expected active single-model profile, got %s / %s", loaded.Provider, loaded.Model)
	}
	if loaded.PlanReview != nil {
		t.Fatalf("expected single-model profile to clear plan reviewer, got %#v", loaded.PlanReview)
	}
	if loaded.ProjectAnalysis.WorkerProfile != nil || loaded.ProjectAnalysis.ReviewerProfile != nil {
		t.Fatalf("expected single-model profile to clear analysis roles, got worker=%#v reviewer=%#v", loaded.ProjectAnalysis.WorkerProfile, loaded.ProjectAnalysis.ReviewerProfile)
	}
	if len(loaded.Specialists.Profiles) != 1 ||
		loaded.Specialists.Profiles[0].Provider != "" ||
		loaded.Specialists.Profiles[0].Model != "" ||
		loaded.Specialists.Profiles[0].ReasoningEffort != "" {
		t.Fatalf("expected single-model profile to clear specialist model while preserving metadata, got %#v", loaded.Specialists.Profiles)
	}
}

func TestLoadConfigPreservesExplicitAnalysisRolesForUnselectedSingleModelProfile(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfg := DefaultConfig(workspace)
	cfg.Provider = "openrouter"
	cfg.Model = "deepseek/deepseek-v4-pro"
	cfg.BaseURL = normalizeOpenRouterBaseURL("")
	cfg.ProjectAnalysis.WorkerProfile = &Profile{Name: "worker", Provider: "openai", Model: "gpt-worker"}
	cfg.ProjectAnalysis.ReviewerProfile = &Profile{Name: "reviewer", Provider: "openai", Model: "gpt-reviewer"}
	cfg.Profiles = []Profile{{
		Name:     "single-main",
		Provider: "openrouter",
		Model:    "deepseek/deepseek-v4-pro",
		BaseURL:  normalizeOpenRouterBaseURL(""),
	}}
	if err := SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}

	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.ProjectAnalysis.WorkerProfile == nil || loaded.ProjectAnalysis.WorkerProfile.Model != "gpt-worker" {
		t.Fatalf("expected explicit worker role to be preserved, got %#v", loaded.ProjectAnalysis.WorkerProfile)
	}
	if loaded.ProjectAnalysis.ReviewerProfile == nil || loaded.ProjectAnalysis.ReviewerProfile.Model != "gpt-reviewer" {
		t.Fatalf("expected explicit reviewer role to be preserved, got %#v", loaded.ProjectAnalysis.ReviewerProfile)
	}
	if loaded.ActiveProfileKey != "" {
		t.Fatalf("expected unselected single-model profile not to become active, got %q", loaded.ActiveProfileKey)
	}
}

func TestLoadConfigRestoresRoleModelsFromMatchingTopLevelProfile(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfg := DefaultConfig(workspace)
	cfg.Provider = "openrouter"
	cfg.Model = "deepseek/deepseek-v4-pro"
	cfg.BaseURL = normalizeOpenRouterBaseURL("")
	cfg.Profiles = []Profile{
		{
			Name:     "deepseek-main",
			Provider: "openrouter",
			Model:    "deepseek/deepseek-v4-pro",
			BaseURL:  normalizeOpenRouterBaseURL(""),
			RoleModels: &ProfileRoleModels{
				AnalysisWorker: &Profile{
					Provider: "openrouter",
					Model:    "deepseek/deepseek-v4-flash",
					BaseURL:  normalizeOpenRouterBaseURL(""),
				},
			},
		},
	}
	if err := SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}

	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.ActiveProfileKey == "" {
		t.Fatalf("expected matching profile to become active")
	}
	if loaded.ProjectAnalysis.WorkerProfile == nil || loaded.ProjectAnalysis.WorkerProfile.Model != "deepseek/deepseek-v4-flash" {
		t.Fatalf("expected matching profile analysis worker, got %#v", loaded.ProjectAnalysis.WorkerProfile)
	}
}

func profileListContainsName(profiles []Profile, name string) bool {
	for _, profile := range profiles {
		if profile.Name == name {
			return true
		}
	}
	return false
}

func TestSaveUserConfigPreservesExistingProviderKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	existing := DefaultConfig(t.TempDir())
	existing.Provider = "openai"
	existing.APIKey = "legacy-openai-key"
	existing.ProviderKeys = map[string]string{
		"anthropic": "anthropic-key",
	}
	if err := SaveUserConfig(existing); err != nil {
		t.Fatalf("SaveUserConfig existing: %v", err)
	}

	next := DefaultConfig(t.TempDir())
	next.Provider = "openrouter"
	next.Model = "meta-llama/llama-3.1-70b"
	if err := SaveUserConfig(next); err != nil {
		t.Fatalf("SaveUserConfig next: %v", err)
	}

	loaded, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.ProviderKeys["openai"] != "legacy-openai-key" {
		t.Fatalf("expected legacy openai key to remain, got %#v", loaded.ProviderKeys)
	}
	if loaded.ProviderKeys["anthropic"] != "anthropic-key" {
		t.Fatalf("expected existing provider key to remain, got %#v", loaded.ProviderKeys)
	}
}

func TestSaveUserConfigPreservesExistingProfilesWhenNextHasNone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	existing := DefaultConfig(t.TempDir())
	existing.Profiles = []Profile{
		{Name: "main-a", Provider: "openai", Model: "gpt-a"},
		{Name: "main-b", Provider: "openrouter", Model: "deepseek/deepseek-v4-pro"},
	}
	existing.ReviewProfiles = []Profile{
		{Name: "review-a", Provider: "anthropic", Model: "claude-a"},
	}
	if err := SaveUserConfig(existing); err != nil {
		t.Fatalf("SaveUserConfig existing: %v", err)
	}

	next := DefaultConfig(t.TempDir())
	next.Provider = "openrouter"
	next.Model = "qwen/qwen3-coder"
	if err := SaveUserConfig(next); err != nil {
		t.Fatalf("SaveUserConfig next: %v", err)
	}

	loaded, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(loaded.Profiles) != 2 {
		t.Fatalf("expected existing main profiles to remain, got %#v", loaded.Profiles)
	}
	for _, want := range []string{"main-a", "main-b"} {
		if !profileListContainsName(loaded.Profiles, want) {
			t.Fatalf("expected main profiles to contain %q, got %#v", want, loaded.Profiles)
		}
	}
	if len(loaded.ReviewProfiles) != 1 || loaded.ReviewProfiles[0].Name != "review-a" {
		t.Fatalf("expected existing review profiles to remain, got %#v", loaded.ReviewProfiles)
	}
}

func TestSaveUserConfigMergesNewProfilesWithExistingProfiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	existing := DefaultConfig(t.TempDir())
	existing.Profiles = []Profile{
		{Name: "old-main", Provider: "openai", Model: "gpt-old"},
	}
	existing.ReviewProfiles = []Profile{
		{Name: "old-review", Provider: "anthropic", Model: "claude-old"},
	}
	if err := SaveUserConfig(existing); err != nil {
		t.Fatalf("SaveUserConfig existing: %v", err)
	}

	next := DefaultConfig(t.TempDir())
	next.Profiles = []Profile{
		{Name: "new-main", Provider: "openrouter", Model: "deepseek/deepseek-v4-pro"},
	}
	next.ReviewProfiles = []Profile{
		{Name: "new-review", Provider: "openai", Model: "gpt-new"},
	}
	if err := SaveUserConfig(next); err != nil {
		t.Fatalf("SaveUserConfig next: %v", err)
	}

	loaded, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(loaded.Profiles) != 2 {
		t.Fatalf("expected old and new main profiles to merge, got %#v", loaded.Profiles)
	}
	for _, want := range []string{"new-main", "old-main"} {
		if !profileListContainsName(loaded.Profiles, want) {
			t.Fatalf("expected main profiles to contain %q, got %#v", want, loaded.Profiles)
		}
	}
	if len(loaded.ReviewProfiles) != 2 {
		t.Fatalf("expected old and new review profiles to merge, got %#v", loaded.ReviewProfiles)
	}
	for _, want := range []string{"new-review", "old-review"} {
		if !profileListContainsName(loaded.ReviewProfiles, want) {
			t.Fatalf("expected review profiles to contain %q, got %#v", want, loaded.ReviewProfiles)
		}
	}
}

func TestEnsureUserConfigAppendsWebResearchServerWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	configPath := filepath.Join(home, ".kernforge", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("{\n  \"provider\": \"openrouter\"\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := EnsureUserConfig(DefaultConfig(t.TempDir())); err != nil {
		t.Fatalf("EnsureUserConfig: %v", err)
	}
	cfgData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile config: %v", err)
	}
	var cfg struct {
		Provider   string            `json:"provider"`
		MCPServers []MCPServerConfig `json:"mcp_servers"`
	}
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		t.Fatalf("Unmarshal config: %v", err)
	}
	if cfg.Provider != "openrouter" {
		t.Fatalf("expected existing provider to be preserved, got %#v", cfg.Provider)
	}
	if len(cfg.MCPServers) != 1 || cfg.MCPServers[0].Name != "web-research" {
		t.Fatalf("expected auto-added web research server, got %#v", cfg.MCPServers)
	}
	if cfg.MCPServers[0].Env["TAVILY_API_KEY"] != "" || cfg.MCPServers[0].Env["BRAVE_SEARCH_API_KEY"] != "" || cfg.MCPServers[0].Env["SERPAPI_API_KEY"] != "" {
		t.Fatalf("expected empty web research env placeholders, got %#v", cfg.MCPServers[0].Env)
	}
}

func TestEnsureUserConfigDoesNotDuplicateExistingWebResearchServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	configPath := filepath.Join(home, ".kernforge", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	existing := filepath.ToSlash(filepath.Join(home, ".kernforge", "mcp", "custom-search.js"))
	if err := os.WriteFile(configPath, []byte("{\n  \"mcp_servers\": [\n    {\n      \"name\": \"custom-web\",\n      \"command\": \"node\",\n      \"args\": [\""+existing+"\"],\n      \"capabilities\": [\"web_search\", \"web_fetch\"]\n    }\n  ]\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := EnsureUserConfig(DefaultConfig(t.TempDir())); err != nil {
		t.Fatalf("EnsureUserConfig: %v", err)
	}
	cfgData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile config: %v", err)
	}
	var cfg struct {
		MCPServers []MCPServerConfig `json:"mcp_servers"`
	}
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		t.Fatalf("Unmarshal config: %v", err)
	}
	if len(cfg.MCPServers) != 1 || cfg.MCPServers[0].Name != "custom-web" {
		t.Fatalf("expected existing web research server to be preserved without duplication, got %#v", cfg.MCPServers)
	}
	if cfg.MCPServers[0].Env["TAVILY_API_KEY"] != "" || cfg.MCPServers[0].Env["BRAVE_SEARCH_API_KEY"] != "" || cfg.MCPServers[0].Env["SERPAPI_API_KEY"] != "" {
		t.Fatalf("expected existing web research server to receive env placeholders, got %#v", cfg.MCPServers[0].Env)
	}
}

func TestEnsureUserConfigBackfillsExistingWebResearchServerEnvAndArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	configPath := filepath.Join(home, ".kernforge", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("{\n  \"mcp_servers\": [\n    {\n      \"name\": \"web-research\",\n      \"command\": \"node\",\n      \"args\": [\".kernforge/mcp/web-research-mcp.js\"],\n      \"cwd\": \".\",\n      \"capabilities\": [\"web_search\", \"web_fetch\"]\n    }\n  ]\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := EnsureUserConfig(DefaultConfig(t.TempDir())); err != nil {
		t.Fatalf("EnsureUserConfig: %v", err)
	}
	cfgData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile config: %v", err)
	}
	var cfg struct {
		MCPServers []MCPServerConfig `json:"mcp_servers"`
	}
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		t.Fatalf("Unmarshal config: %v", err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("expected one web research server, got %#v", cfg.MCPServers)
	}
	expectedPath := filepath.ToSlash(filepath.Join(home, ".kernforge", "mcp", "web-research-mcp.js"))
	if len(cfg.MCPServers[0].Args) != 1 || cfg.MCPServers[0].Args[0] != expectedPath {
		t.Fatalf("expected backfilled deployed path, got %#v", cfg.MCPServers[0].Args)
	}
	if cfg.MCPServers[0].Env["TAVILY_API_KEY"] != "" || cfg.MCPServers[0].Env["BRAVE_SEARCH_API_KEY"] != "" || cfg.MCPServers[0].Env["SERPAPI_API_KEY"] != "" {
		t.Fatalf("expected backfilled env placeholders, got %#v", cfg.MCPServers[0].Env)
	}
}

func TestEnsureUserConfigBackfillsExistingWebResearchServerCapabilitiesAndEmptyArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	configPath := filepath.Join(home, ".kernforge", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("{\n  \"mcp_servers\": [\n    {\n      \"name\": \"web-research\",\n      \"command\": \"node\",\n      \"args\": [],\n      \"capabilities\": [\"web_search\"]\n    }\n  ]\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := EnsureUserConfig(DefaultConfig(t.TempDir())); err != nil {
		t.Fatalf("EnsureUserConfig: %v", err)
	}
	cfgData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile config: %v", err)
	}
	var cfg struct {
		MCPServers []MCPServerConfig `json:"mcp_servers"`
	}
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		t.Fatalf("Unmarshal config: %v", err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("expected one web research server, got %#v", cfg.MCPServers)
	}
	expectedPath := filepath.ToSlash(filepath.Join(home, ".kernforge", "mcp", "web-research-mcp.js"))
	if len(cfg.MCPServers[0].Args) != 1 || cfg.MCPServers[0].Args[0] != expectedPath {
		t.Fatalf("expected deployed path for empty args, got %#v", cfg.MCPServers[0].Args)
	}
	if !sliceContainsFold(cfg.MCPServers[0].Capabilities, "web_search") || !sliceContainsFold(cfg.MCPServers[0].Capabilities, "web_fetch") {
		t.Fatalf("expected missing web capability to be backfilled, got %#v", cfg.MCPServers[0].Capabilities)
	}
}

func TestLoadConfigWorkspaceWebResearchMCPInheritsNonEmptyGlobalEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	globalConfigPath := filepath.Join(home, ".kernforge", "config.json")
	if err := os.MkdirAll(filepath.Dir(globalConfigPath), 0o755); err != nil {
		t.Fatalf("MkdirAll global: %v", err)
	}
	globalConfig := "{\n" +
		"  \"mcp_servers\": [\n" +
		"    {\n" +
		"      \"name\": \"web-research\",\n" +
		"      \"command\": \"node\",\n" +
		"      \"args\": [\"C:/Users/test/.kernforge/mcp/web-research-mcp.js\"],\n" +
		"      \"env\": {\n" +
		"        \"TAVILY_API_KEY\": \"global-secret\",\n" +
		"        \"BRAVE_SEARCH_API_KEY\": \"\",\n" +
		"        \"SERPAPI_API_KEY\": \"\"\n" +
		"      },\n" +
		"      \"cwd\": \".\",\n" +
		"      \"capabilities\": [\"web_search\", \"web_fetch\"]\n" +
		"    }\n" +
		"  ]\n" +
		"}\n"
	if err := os.WriteFile(globalConfigPath, []byte(globalConfig), 0o644); err != nil {
		t.Fatalf("WriteFile global config: %v", err)
	}

	workspace := t.TempDir()
	workspaceConfigPath := filepath.Join(workspace, ".kernforge", "config.json")
	if err := os.MkdirAll(filepath.Dir(workspaceConfigPath), 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	workspaceConfig := "{\n" +
		"  \"mcp_servers\": [\n" +
		"    {\n" +
		"      \"name\": \"web-research\",\n" +
		"      \"command\": \"node\",\n" +
		"      \"args\": [\".kernforge/mcp/web-research-mcp.js\"],\n" +
		"      \"env\": {\n" +
		"        \"TAVILY_API_KEY\": \"\",\n" +
		"        \"BRAVE_SEARCH_API_KEY\": \"\",\n" +
		"        \"SERPAPI_API_KEY\": \"\"\n" +
		"      },\n" +
		"      \"cwd\": \".\",\n" +
		"      \"capabilities\": [\"web_search\", \"web_fetch\"]\n" +
		"    }\n" +
		"  ]\n" +
		"}\n"
	if err := os.WriteFile(workspaceConfigPath, []byte(workspaceConfig), 0o644); err != nil {
		t.Fatalf("WriteFile workspace config: %v", err)
	}

	cfg, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("expected one effective mcp server, got %#v", cfg.MCPServers)
	}
	server := cfg.MCPServers[0]
	if server.Env["TAVILY_API_KEY"] != "global-secret" {
		t.Fatalf("expected workspace server to inherit global Tavily key, got %#v", server.Env)
	}
	if len(server.Args) != 1 || server.Args[0] != ".kernforge/mcp/web-research-mcp.js" {
		t.Fatalf("expected workspace args to remain in effect, got %#v", server.Args)
	}
}

func TestLoadConfigWorkspaceWebResearchMCPOverridesGlobalEnvWhenNonEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	globalConfigPath := filepath.Join(home, ".kernforge", "config.json")
	if err := os.MkdirAll(filepath.Dir(globalConfigPath), 0o755); err != nil {
		t.Fatalf("MkdirAll global: %v", err)
	}
	globalConfig := "{\n" +
		"  \"mcp_servers\": [\n" +
		"    {\n" +
		"      \"name\": \"web-research\",\n" +
		"      \"command\": \"node\",\n" +
		"      \"args\": [\"C:/Users/test/.kernforge/mcp/web-research-mcp.js\"],\n" +
		"      \"env\": {\n" +
		"        \"TAVILY_API_KEY\": \"global-secret\"\n" +
		"      },\n" +
		"      \"cwd\": \".\",\n" +
		"      \"capabilities\": [\"web_search\", \"web_fetch\"]\n" +
		"    }\n" +
		"  ]\n" +
		"}\n"
	if err := os.WriteFile(globalConfigPath, []byte(globalConfig), 0o644); err != nil {
		t.Fatalf("WriteFile global config: %v", err)
	}

	workspace := t.TempDir()
	workspaceConfigPath := filepath.Join(workspace, ".kernforge", "config.json")
	if err := os.MkdirAll(filepath.Dir(workspaceConfigPath), 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	workspaceConfig := "{\n" +
		"  \"mcp_servers\": [\n" +
		"    {\n" +
		"      \"name\": \"web-research\",\n" +
		"      \"command\": \"node\",\n" +
		"      \"args\": [\".kernforge/mcp/web-research-mcp.js\"],\n" +
		"      \"env\": {\n" +
		"        \"TAVILY_API_KEY\": \"workspace-secret\"\n" +
		"      },\n" +
		"      \"cwd\": \".\",\n" +
		"      \"capabilities\": [\"web_search\", \"web_fetch\"]\n" +
		"    }\n" +
		"  ]\n" +
		"}\n"
	if err := os.WriteFile(workspaceConfigPath, []byte(workspaceConfig), 0o644); err != nil {
		t.Fatalf("WriteFile workspace config: %v", err)
	}

	cfg, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("expected one effective mcp server, got %#v", cfg.MCPServers)
	}
	server := cfg.MCPServers[0]
	if server.Env["TAVILY_API_KEY"] != "workspace-secret" {
		t.Fatalf("expected workspace Tavily key to override global value, got %#v", server.Env)
	}
}

func TestMergeConfigFileRepairsAppendedMCPSnippet(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	body := "{\n" +
		"  \"provider\": \"openrouter\",\n" +
		"  \"project_analysis\": {\n" +
		"    \"worker_profile\": {\n" +
		"      \"provider\": \"openrouter\",\n" +
		"      \"model\": \"openai/gpt-5-mini\"\n" +
		"    }\n" +
		"  },\n" +
		"  {\n" +
		"    \"mcp_servers\": [\n" +
		"      {\n" +
		"        \"name\": \"web-research\",\n" +
		"        \"command\": \"node\",\n" +
		"        \"args\": [\".kernforge/mcp/web-research-mcp.js\"],\n" +
		"        \"capabilities\": [\"web_search\", \"web_fetch\"]\n" +
		"      }\n" +
		"    ]\n" +
		"  }\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := DefaultConfig(root)
	if err := mergeConfigFile(&cfg, path); err != nil {
		t.Fatalf("mergeConfigFile: %v", err)
	}
	if cfg.Provider != "openrouter" {
		t.Fatalf("expected provider to survive repair, got %q", cfg.Provider)
	}
	if len(cfg.MCPServers) != 1 || cfg.MCPServers[0].Name != "web-research" {
		t.Fatalf("expected repaired mcp_servers, got %#v", cfg.MCPServers)
	}
	repairedData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var repaired Config
	if err := json.Unmarshal(repairedData, &repaired); err != nil {
		t.Fatalf("expected repaired file to be valid json, got %v\n%s", err, string(repairedData))
	}
}

func TestNormalizeConfigPathsNormalizesMCPCapabilities(t *testing.T) {
	cfg := &Config{
		MCPServers: []MCPServerConfig{
			{
				Name:         "web",
				Command:      "node",
				Capabilities: []string{" Web_Search ", "WEB_FETCH", "web_fetch", ""},
			},
		},
	}

	normalizeConfigPaths(cfg)

	if len(cfg.MCPServers) != 1 {
		t.Fatalf("expected one server, got %#v", cfg.MCPServers)
	}
	got := cfg.MCPServers[0].Capabilities
	if len(got) != 2 || got[0] != "web_search" || got[1] != "web_fetch" {
		t.Fatalf("expected normalized capabilities, got %#v", got)
	}
}

func TestNormalizeConfigPathsNormalizesMCPEnvEntries(t *testing.T) {
	cfg := &Config{
		MCPServers: []MCPServerConfig{
			{
				Name:    "web",
				Command: "node",
				Env: map[string]string{
					" TAVILY_API_KEY ": "  secret  ",
					"":                 "ignored",
				},
			},
		},
	}

	normalizeConfigPaths(cfg)

	if len(cfg.MCPServers) != 1 {
		t.Fatalf("expected one server, got %#v", cfg.MCPServers)
	}
	got := cfg.MCPServers[0].Env
	if len(got) != 1 || got["TAVILY_API_KEY"] != "secret" {
		t.Fatalf("expected normalized env map, got %#v", got)
	}
}

func TestHelpTextIncludesReloadAndInitExtensions(t *testing.T) {
	help := HelpText()
	for _, needle := range []string{
		"/reload",
		"/init skill <name>",
		"/init config",
		"/init verify",
		"/init memory-policy",
		"/mem",
		"/mem-search <query>",
		"/mem-show <id>",
		"/mem-dashboard [query]",
		"/mem-dashboard-html [query]",
		"/mem-prune [all]",
		"/mem-stats",
		"/selection",
		"/selections",
		"/use-selection <n>",
		"/drop-selection <n>",
		"/clear-selection",
		"/clear-selections",
		"/review-selection [...]",
		"/edit-selection <task>",
		"/verify [path,...|--full]",
		"/verify-dashboard [all]",
		"/verify-dashboard-html [all]",
		"/set-msbuild-path <path>",
		"/clear-msbuild-path",
		"/set-cmake-path <path>",
		"/clear-cmake-path",
		"/set-ctest-path <path>",
		"/clear-ctest-path",
		"/set-ninja-path <path>",
		"/clear-ninja-path",
		"/detect-verification-tools",
		"/set-auto-verify [on|off]",
		"/new-feature <task>",
		"/specialists",
		"/provider status",
		"/worktree [status|list|create|enter|attach|leave|cleanup]",
		"/events [tail|export]",
		"/completion-audit [note]",
		"/recover [note]",
		`Using a on "Allow write?" enables write auto-approval for the current session only.`,
		`Using a on "Open diff preview?" auto-accepts the current and future diff previews for the current session only.`,
		`Using a on "Allow git?" enables git auto-approval for the current session only.`,
		"Shell approval is tracked separately for the current session.",
		"run_shell to modify workspace files",
		"/status to inspect the current session approval state",
		"/config to inspect effective settings",
		"git actions",
	} {
		if !strings.Contains(help, needle) {
			t.Fatalf("expected help text to contain %q", needle)
		}
	}
}

func TestHelpDetailIncludesEventsWorkflow(t *testing.T) {
	detail, ok := HelpDetail("events")
	if !ok {
		t.Fatalf("expected events help detail to resolve")
	}
	for _, needle := range []string{
		"/events tail [n]",
		"/events export [path]",
		".kernforge/events/latest.jsonl",
		"JSONL",
		"local Codex App Server style feed",
	} {
		if !strings.Contains(detail, needle) {
			t.Fatalf("expected events help detail to contain %q", needle)
		}
	}
}

func TestHelpDetailIncludesCompletionAuditWorkflow(t *testing.T) {
	detail, ok := HelpDetail("completion-audit")
	if !ok {
		t.Fatalf("expected completion-audit help detail to resolve")
	}
	for _, needle := range []string{
		"/completion-audit [note]",
		".kernforge/completion_audit/latest.md",
		"required artifacts",
		"latest verification",
		"background jobs",
		"needs_review",
	} {
		if !strings.Contains(detail, needle) {
			t.Fatalf("expected completion-audit help detail to contain %q", needle)
		}
	}
}

func TestHelpDetailIncludesRecoverWorkflow(t *testing.T) {
	detail, ok := HelpDetail("recover")
	if !ok {
		t.Fatalf("expected recover help detail to resolve")
	}
	for _, needle := range []string{
		"/recover [note]",
		".kernforge/recovery/latest.md",
		"/recover execute-safe [note]",
		"structured diagnosis",
		"safe-auto whitelist",
		"latest verification failure",
		"background jobs",
		"without pasting logs",
	} {
		if !strings.Contains(detail, needle) {
			t.Fatalf("expected recover help detail to contain %q", needle)
		}
	}
}

func TestHelpDetailIncludesProviderStatusCommand(t *testing.T) {
	detail, ok := HelpDetail("provider status")
	if !ok {
		t.Fatalf("expected provider status help detail to resolve")
	}
	for _, needle := range []string{
		"/provider status",
		"/provider anthropic",
		"/provider deepseek",
		"OpenRouter performs a live key lookup",
		"DeepSeek performs a live balance lookup",
		"OpenAI and Anthropic show officially documented billing and usage visibility limits",
	} {
		if !strings.Contains(detail, needle) {
			t.Fatalf("expected provider help detail to contain %q", needle)
		}
	}
}

func TestHelpTextIncludesAnalyzeProjectDocsCommands(t *testing.T) {
	help := HelpText()
	for _, needle := range []string{
		"/analyze-project [--path <dir>] [--mode map|trace|impact|surface|security|performance]",
		"infer a mode-specific goal when omitted",
		"/docs-refresh",
		"/analyze-dashboard [latest|path]",
	} {
		if !strings.Contains(help, needle) {
			t.Fatalf("expected help text to include %q", needle)
		}
	}
	if strings.Contains(help, "/analyze-project [--docs]") {
		t.Fatalf("expected analyze-project help to hide deprecated --docs flag")
	}
}

func TestHelpTextIncludesFuzzCampaignCommand(t *testing.T) {
	help := HelpText()
	for _, needle := range []string{
		"/fuzz-campaign [status|run|new|list|show]",
		"deduplicated finding lifecycle",
		"parsed coverage report feedback",
		"sanitizer/verifier artifact capture",
	} {
		if !strings.Contains(help, needle) {
			t.Fatalf("expected help text to include %q", needle)
		}
	}
}

func TestHelpTextIncludesSourceScanCommand(t *testing.T) {
	help := HelpText()
	for _, needle := range []string{
		"/source-scan [status|run|list|show|revalidate]",
		"/create-driver-poc <driver-name>",
		"/fuzz-func --from-candidate <candidate-id>",
		"built-in bug-pattern matchers",
	} {
		if !strings.Contains(help, needle) {
			t.Fatalf("expected help text to include %q", needle)
		}
	}
}

func TestHelpDetailIncludesSourceScanWorkflow(t *testing.T) {
	detail, ok := HelpDetail("source_scan")
	if !ok {
		t.Fatalf("expected source_scan help detail to resolve")
	}
	for _, needle := range []string{
		"/source-scan [status|run|list|show <id|latest>|revalidate <id|latest>]",
		"Windows kernel",
		"Unreal RPC",
		"~/.kernforge/source_scan.json",
		".kernforge/source_scan/<run-id>/",
		"/fuzz-func --from-candidate <candidate-id>",
		"native-confirmed",
	} {
		if !strings.Contains(detail, needle) {
			t.Fatalf("expected source-scan detail to include %q", needle)
		}
	}
}

func TestHelpDetailIncludesCreateDriverPOCWorkflow(t *testing.T) {
	detail, ok := HelpDetail("create-driver-poc")
	if !ok {
		t.Fatalf("expected create-driver-poc help detail to resolve")
	}
	for _, needle := range []string{
		"/create-driver-poc <driver-name>",
		"x64-only Visual Studio solution",
		"C++20 WDM kernel driver",
		"Driver.cpp",
		"<driver-name>.sys",
		"<driver-name>-tester.exe",
		"shared/Ioctl.h",
		"No INF package",
		"MT runtime",
		"SCM kernel-driver service",
		"shared DeviceType",
		"already-running service",
		"growing path buffer",
		"IoctlPing",
		"test-signing",
	} {
		if !strings.Contains(detail, needle) {
			t.Fatalf("expected create-driver-poc detail to include %q", needle)
		}
	}
}

func TestHelpDetailIncludesFuzzCampaignWorkflow(t *testing.T) {
	detail, ok := HelpDetail("fuzz-campaign")
	if !ok {
		t.Fatalf("expected fuzz-campaign help detail to resolve")
	}
	for _, needle := range []string{
		"/fuzz-campaign [status|run|new <name>|list|show <id|latest>]",
		".kernforge/fuzz/<campaign-id>/manifest.json",
		"corpus, crashes, coverage, reports, and logs",
		"FUZZ_TARGETS.md",
		"corpus/<run-id>/",
		"Coverage gaps feed the next generated FUZZ_TARGETS.md refresh",
		"Native crash findings are merged by crash fingerprint",
		"native run results into reports and evidence",
		"libFuzzer logs, llvm-cov text, LCOV, and JSON coverage summaries",
		"parsed coverage reports",
		"sanitizer reports, Windows crash dumps, Application Verifier, and Driver Verifier artifacts",
		"run artifacts",
		"intent-driven automation",
		"Investigation handoff",
		"Simulation handoff",
		"Verification handoff",
	} {
		if !strings.Contains(detail, needle) {
			t.Fatalf("expected fuzz-campaign detail to include %q", needle)
		}
	}
}

func TestHelpDetailIncludesAnalyzeProjectDocsWorkflow(t *testing.T) {
	detail, ok := HelpDetail("analyze-project")
	if !ok {
		t.Fatalf("expected analyze-project help detail to resolve")
	}
	for _, needle := range []string{
		"/analyze-project [--path <dir>] [--mode map|trace|impact|surface|security|performance]",
		"The goal is optional",
		"Non-map modes automatically reuse the most relevant previous map run",
		"--path limits shard execution",
		"schema-versioned docs_manifest.json",
		"missing schema_version as legacy",
		"/docs-refresh",
		"/analyze-dashboard [latest|path]",
		"cross-document search",
		"stale section diff",
		"trust/data-flow graph context",
		"graph sections",
		"vector corpus exports",
		"evidence and persistent memory",
		"Analysis handoff",
	} {
		if !strings.Contains(detail, needle) {
			t.Fatalf("expected analyze-project detail to include %q", needle)
		}
	}
	if strings.Contains(detail, "--docs") {
		t.Fatalf("expected analyze-project detail to hide deprecated --docs flag")
	}
}

func TestHelpDetailIncludesWebResearchMCPTips(t *testing.T) {
	detail, ok := HelpDetail("mcp")
	if !ok {
		t.Fatalf("expected mcp help detail to resolve")
	}
	for _, needle := range []string{
		`"web_search"`,
		`"web_fetch"`,
		`"TAVILY_API_KEY"`,
		"/init config enables the bundled web-research MCP by default when the script is available",
		"auto-adds that MCP to ~/.kernforge/config.json",
	} {
		if !strings.Contains(detail, needle) {
			t.Fatalf("expected mcp help detail to contain %q", needle)
		}
	}
}

func TestConfigSearchPathsUseCurrentLocationsOnly(t *testing.T) {
	paths := configSearchPaths(filepath.Join("workspace", "repo"))
	if len(paths) != 2 {
		t.Fatalf("expected 2 config search paths, got %d: %#v", len(paths), paths)
	}
	for _, path := range paths {
		lower := strings.ToLower(filepath.ToSlash(path))
		if strings.Contains(lower, "/imcli") {
			t.Fatalf("unexpected legacy config path: %s", path)
		}
	}
}

func TestDefaultMemoryPathsExcludeLegacyLocations(t *testing.T) {
	paths := defaultMemoryPaths(filepath.Join("workspace", "repo"))
	for _, path := range paths {
		lower := strings.ToLower(filepath.ToSlash(path))
		if strings.Contains(lower, "/imcli") || strings.HasSuffix(lower, "/imcli.md") {
			t.Fatalf("unexpected legacy memory path: %s", path)
		}
	}
}

func TestDefaultConfigEnablesAutoVerify(t *testing.T) {
	cfg := DefaultConfig(filepath.Join("workspace", "repo"))
	if !configAutoVerify(cfg) {
		t.Fatalf("expected auto_verify to default to true")
	}
}

func TestDefaultConfigUsesAutoProgressDisplay(t *testing.T) {
	cfg := DefaultConfig(filepath.Join("workspace", "repo"))
	if got := configProgressDisplay(cfg); got != "auto" {
		t.Fatalf("expected progress display auto, got %q", got)
	}
}

func TestNormalizeProgressDisplayAliases(t *testing.T) {
	cases := map[string]string{
		"":           "auto",
		"default":    "auto",
		"footer":     "compact",
		"quiet":      "compact",
		"ledger":     "stream",
		"persistent": "stream",
		"bad":        "auto",
	}
	for input, want := range cases {
		if got := normalizeProgressDisplay(input); got != want {
			t.Fatalf("normalizeProgressDisplay(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDefaultConfigRequestTimeoutUsesTwentyMinutes(t *testing.T) {
	cfg := DefaultConfig(filepath.Join("workspace", "repo"))
	if got := configRequestTimeout(cfg); got != 20*time.Minute {
		t.Fatalf("expected default request timeout of 20 minutes, got %s", got)
	}
}

func TestDefaultConfigRequestRetriesUseSaneDefaults(t *testing.T) {
	cfg := DefaultConfig(filepath.Join("workspace", "repo"))
	if got := configMaxRequestRetries(cfg); got != 2 {
		t.Fatalf("expected default max request retries of 2, got %d", got)
	}
	if got := configRequestRetryDelay(cfg); got != 1500*time.Millisecond {
		t.Fatalf("expected default request retry delay of 1500ms, got %s", got)
	}
	if got := configReadHintSpans(cfg); got != defaultReadHintSpans {
		t.Fatalf("expected default read hint spans of %d, got %d", defaultReadHintSpans, got)
	}
	if got := configReadCacheEntries(cfg); got != defaultReadCacheEntries {
		t.Fatalf("expected default read cache entries of %d, got %d", defaultReadCacheEntries, got)
	}
}

func TestConfigRequestTimeoutUsesConfiguredSeconds(t *testing.T) {
	cfg := DefaultConfig(filepath.Join("workspace", "repo"))
	cfg.RequestTimeoutSecs = 7
	if got := configRequestTimeout(cfg); got != 7*time.Second {
		t.Fatalf("expected configured request timeout of 7 seconds, got %s", got)
	}
}

func TestConfigShellTimeoutUsesConfiguredSeconds(t *testing.T) {
	cfg := DefaultConfig(filepath.Join("workspace", "repo"))
	cfg.ShellTimeoutSecs = 9
	if got := configShellTimeout(cfg); got != 9*time.Second {
		t.Fatalf("expected configured shell timeout of 9 seconds, got %s", got)
	}
}

func TestConfigReadSettingsUseConfiguredValues(t *testing.T) {
	cfg := DefaultConfig(filepath.Join("workspace", "repo"))
	cfg.ReadHintSpans = 7
	cfg.ReadCacheEntries = 5
	if got := configReadHintSpans(cfg); got != 7 {
		t.Fatalf("expected configured read hint spans of 7, got %d", got)
	}
	if got := configReadCacheEntries(cfg); got != 5 {
		t.Fatalf("expected configured read cache entries of 5, got %d", got)
	}
}

func TestProviderRetryDelayUsesExponentialBackoffWithinJitterBounds(t *testing.T) {
	base := 100 * time.Millisecond
	first := providerRetryDelay(base, 0)
	third := providerRetryDelay(base, 3)

	if first < 80*time.Millisecond || first > 120*time.Millisecond {
		t.Fatalf("expected first retry delay to stay within jitter bounds, got %s", first)
	}
	if third < 640*time.Millisecond || third > 960*time.Millisecond {
		t.Fatalf("expected third retry delay to stay within jitter bounds, got %s", third)
	}
	if third <= first {
		t.Fatalf("expected exponential backoff to grow retry delay, got first=%s third=%s", first, third)
	}
}

func TestPlatformUserConfigBaseDirUsesHomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home dir: %v", err)
	}
	if got := platformUserConfigBaseDir(); got != home {
		t.Fatalf("expected user config base dir %q, got %q", home, got)
	}
}

func TestPermissionManagerShellPromptDoesNotAdvertiseAlways(t *testing.T) {
	var prompted string
	perms := NewPermissionManager(ModeDefault, func(question string) (bool, error) {
		prompted = question
		return true, nil
	})

	allowed, err := perms.Allow(ActionShell, "go test ./...")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !allowed {
		t.Fatalf("expected shell permission to be allowed")
	}
	if !strings.Contains(prompted, "Allow shell? go test ./...") {
		t.Fatalf("unexpected shell prompt: %q", prompted)
	}
	if strings.Contains(strings.ToLower(prompted), "always") {
		t.Fatalf("shell prompt should not advertise always, got %q", prompted)
	}
}

func TestPermissionManagerShellWritePromptDoesNotAdvertiseAlways(t *testing.T) {
	var prompted string
	perms := NewPermissionManager(ModeDefault, func(question string) (bool, error) {
		prompted = question
		return true, nil
	})

	allowed, err := perms.Allow(ActionShellWrite, "fmt ./... (scoped to main.go)")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !allowed {
		t.Fatalf("expected shell write permission to be allowed")
	}
	if !strings.Contains(prompted, "Allow shell write? fmt ./... (scoped to main.go)") {
		t.Fatalf("unexpected shell write prompt: %q", prompted)
	}
	if strings.Contains(strings.ToLower(prompted), "always") {
		t.Fatalf("shell write prompt should not advertise always, got %q", prompted)
	}
}

func TestPermissionManagerGitPromptAdvertisesAlways(t *testing.T) {
	var prompted string
	perms := NewPermissionManager(ModeDefault, func(question string) (bool, error) {
		prompted = question
		return true, nil
	})

	allowed, err := perms.Allow(ActionGit, "create commit: test subject")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !allowed {
		t.Fatalf("expected git permission to be allowed")
	}
	if !strings.Contains(prompted, "Allow git? create commit: test subject") {
		t.Fatalf("unexpected git prompt: %q", prompted)
	}
	if !strings.Contains(strings.ToLower(prompted), "always") {
		t.Fatalf("git prompt should advertise always, got %q", prompted)
	}
}

func TestMigrateLegacyConfigDefaultsRewritesUserConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	userPath := filepath.Join(home, ".kernforge", "config.json")
	if err := os.MkdirAll(filepath.Dir(userPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	original := `{
  "provider": "openai",
  "max_tool_iterations": 16,
  "max_tokens": 4096
}`
	if err := os.WriteFile(userPath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Sanity: at this point the in-memory cfg still reflects the legacy
	// values because LoadConfig itself does not migrate.
	if cfg.MaxToolIterations != 16 {
		t.Fatalf("pre-migration MaxToolIterations: want 16, got %d", cfg.MaxToolIterations)
	}
	if cfg.MaxTokens != 4096 {
		t.Fatalf("pre-migration MaxTokens: want 4096, got %d", cfg.MaxTokens)
	}

	notices := MigrateLegacyConfigDefaults(t.TempDir(), &cfg)
	if len(notices) != 2 {
		t.Fatalf("expected 2 migration notices, got %d: %#v", len(notices), notices)
	}
	if cfg.MaxToolIterations != 0 {
		t.Fatalf("post-migration MaxToolIterations: want 0 (unlimited), got %d", cfg.MaxToolIterations)
	}
	if cfg.MaxTokens != currentDefaultMaxTokens {
		t.Fatalf("post-migration MaxTokens: want %d, got %d", currentDefaultMaxTokens, cfg.MaxTokens)
	}

	// File on disk must also be rewritten so the next LoadConfig does not
	// re-introduce the legacy values.
	rewritten, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(rewritten, &raw); err != nil {
		t.Fatalf("Unmarshal rewritten file: %v\n%s", err, rewritten)
	}
	if v, ok := raw["max_tool_iterations"].(float64); !ok || int(v) != 0 {
		t.Fatalf("expected max_tool_iterations=0 on disk, got %v", raw["max_tool_iterations"])
	}
	if v, ok := raw["max_tokens"].(float64); !ok || int(v) != currentDefaultMaxTokens {
		t.Fatalf("expected max_tokens=%d on disk, got %v", currentDefaultMaxTokens, raw["max_tokens"])
	}
	// Unrelated fields must be preserved.
	if raw["provider"] != "openai" {
		t.Fatalf("provider field was lost: %v", raw["provider"])
	}

	// Idempotency: running migration again on the already-fixed file
	// produces no notices and does not touch the cfg further.
	if again := MigrateLegacyConfigDefaults(t.TempDir(), &cfg); len(again) != 0 {
		t.Fatalf("second migration should be a no-op, got %#v", again)
	}
}

func TestMigrateLegacyConfigDefaultsRewritesWorkspaceConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := t.TempDir()

	wsPath := filepath.Join(workspace, ".kernforge", "config.json")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(wsPath, []byte(`{"max_tool_iterations": 16}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	notices := MigrateLegacyConfigDefaults(workspace, &cfg)
	if len(notices) != 1 || notices[0].Field != "max_tool_iterations" {
		t.Fatalf("expected 1 max_tool_iterations notice, got %#v", notices)
	}

	rewritten, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(rewritten, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if v, ok := raw["max_tool_iterations"].(float64); !ok || int(v) != 0 {
		t.Fatalf("workspace file should hold migrated value, got %v", raw["max_tool_iterations"])
	}
}

func TestMigrateLegacyConfigDefaultsLeavesExplicitNonLegacyValuesAlone(t *testing.T) {
	cfg := Config{
		MaxToolIterations: 50,    // user-chosen, not legacy
		MaxTokens:         12000, // user-chosen, not legacy
	}
	notices := MigrateLegacyConfigDefaults(t.TempDir(), &cfg)
	if len(notices) != 0 {
		t.Fatalf("expected no notices for non-legacy values, got %#v", notices)
	}
	if cfg.MaxToolIterations != 50 {
		t.Fatalf("MaxToolIterations was clobbered: %d", cfg.MaxToolIterations)
	}
	if cfg.MaxTokens != 12000 {
		t.Fatalf("MaxTokens was clobbered: %d", cfg.MaxTokens)
	}
}

func TestMigrateLegacyConfigDefaultsHandlesMissingConfigFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// In-memory cfg still gets migrated even if no on-disk files exist
	// (e.g. CLI-only invocation). Filesystem patch is best-effort.
	cfg := Config{
		MaxToolIterations: legacyDefaultMaxToolIterations,
		MaxTokens:         legacyDefaultMaxTokens,
	}
	notices := MigrateLegacyConfigDefaults(t.TempDir(), &cfg)
	if len(notices) != 2 {
		t.Fatalf("expected 2 notices, got %d", len(notices))
	}
	if cfg.MaxToolIterations != 0 || cfg.MaxTokens != currentDefaultMaxTokens {
		t.Fatalf("in-memory migration failed: %#v", cfg)
	}
}
