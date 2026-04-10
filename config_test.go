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
	text := InitWorkspaceConfigTemplate()
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
	if len(decoded.MCPServers) != 1 || !decoded.MCPServers[0].Disabled {
		t.Fatalf("expected one disabled example mcp server, got %#v", decoded.MCPServers)
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

func TestConfigRequestTimeoutUsesConfiguredSeconds(t *testing.T) {
	cfg := DefaultConfig(filepath.Join("workspace", "repo"))
	cfg.RequestTimeoutSecs = 7
	if got := configRequestTimeout(cfg); got != 7*time.Second {
		t.Fatalf("expected configured request timeout of 7 seconds, got %s", got)
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
