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
		SkillPaths    []string          `json:"skill_paths"`
		EnabledSkills []string          `json:"enabled_skills"`
		MCPServers    []MCPServerConfig `json:"mcp_servers"`
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
		"/provider status",
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

func TestHelpDetailIncludesProviderStatusCommand(t *testing.T) {
	detail, ok := HelpDetail("provider status")
	if !ok {
		t.Fatalf("expected provider status help detail to resolve")
	}
	for _, needle := range []string{
		"/provider status",
		"/provider anthropic",
		"OpenRouter performs a live key lookup",
		"OpenAI and Anthropic show officially documented billing and usage visibility limits",
	} {
		if !strings.Contains(detail, needle) {
			t.Fatalf("expected provider help detail to contain %q", needle)
		}
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
