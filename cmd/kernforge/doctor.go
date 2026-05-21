package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const kernforgeDoctorSchemaVersion = 1

const (
	kernforgeDoctorStatusOK   = "ok"
	kernforgeDoctorStatusWarn = "warn"
	kernforgeDoctorStatusFail = "fail"
	kernforgeDoctorStatusIdle = "idle"
)

type kernforgeDoctorOptions struct {
	StrictConfig       bool
	Profile            string
	ProviderOverride   string
	ModelOverride      string
	BaseURLOverride    string
	PermissionOverride string
	ForceBypass        bool
	BypassHookTrust    bool
	Summary            bool
	JSON               bool
	All                bool
	NoColor            bool
	Writer             io.Writer
}

type kernforgeDoctorReport struct {
	SchemaVersion int                             `json:"schema_version"`
	GeneratedAt   string                          `json:"generated_at"`
	OverallStatus string                          `json:"overall_status"`
	Notes         []kernforgeDoctorNote           `json:"notes,omitempty"`
	Checks        map[string]kernforgeDoctorCheck `json:"checks"`
	order         []string
}

type kernforgeDoctorNote struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

type kernforgeDoctorCheck struct {
	ID             string            `json:"id"`
	Category       string            `json:"category"`
	Status         string            `json:"status"`
	Summary        string            `json:"summary"`
	Details        map[string]string `json:"details,omitempty"`
	Recommendation string            `json:"recommendation,omitempty"`
}

func runKernforgeDoctorCommand(cwd string, args []string, opts kernforgeDoctorOptions) error {
	fs := flag.NewFlagSet("kernforge doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.JSON, "json", opts.JSON, "print a redacted JSON diagnostic report")
	fs.BoolVar(&opts.Summary, "summary", opts.Summary, "print compact human diagnostics")
	fs.BoolVar(&opts.All, "all", opts.All, "include expanded detail rows")
	fs.BoolVar(&opts.NoColor, "no-color", opts.NoColor, "disable color output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unknown doctor argument: %s", fs.Arg(0))
	}
	writer := opts.Writer
	if writer == nil {
		writer = os.Stdout
	}
	report := collectKernforgeDoctorReport(cwd, opts)
	if opts.JSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(writer, string(data))
		return err
	}
	_, err := fmt.Fprint(writer, renderKernforgeDoctorReport(report, opts))
	return err
}

func collectKernforgeDoctorReport(cwd string, opts kernforgeDoctorOptions) kernforgeDoctorReport {
	report := kernforgeDoctorReport{
		SchemaVersion: kernforgeDoctorSchemaVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		OverallStatus: kernforgeDoctorStatusOK,
		Checks:        map[string]kernforgeDoctorCheck{},
	}
	resolvedCWD := strings.TrimSpace(cwd)
	if resolvedCWD == "" {
		if current, err := os.Getwd(); err == nil {
			resolvedCWD = current
		}
	}
	if abs, err := filepath.Abs(resolvedCWD); err == nil {
		resolvedCWD = abs
	}

	cfg, cfgErr := LoadConfigWithOptions(resolvedCWD, ConfigLoadOptions{
		StrictConfig:         opts.StrictConfig,
		Profile:              opts.Profile,
		SkipEnsureUserConfig: true,
	})
	if cfgErr != nil {
		cfg = DefaultConfig(resolvedCWD)
	}
	applyKernforgeDoctorOverrides(&cfg, opts)
	autoPopulateErr := error(nil)
	if cfgErr == nil {
		_, autoPopulateErr = autoPopulateVerificationToolPaths(resolvedCWD, &cfg, detectWindowsVerificationToolPath)
	}

	report.addCheck(kernforgeDoctorRuntimeCheck(resolvedCWD, cfg))
	report.addCheck(kernforgeDoctorSearchCheck())
	report.addCheck(kernforgeDoctorWorkspaceCheck(resolvedCWD))
	report.addCheck(kernforgeDoctorStateCheck(cfg))
	report.addCheck(kernforgeDoctorConfigCheck(resolvedCWD, cfg, opts, cfgErr, autoPopulateErr))
	report.addCheck(kernforgeDoctorAuthCheck(cfg))
	report.addCheck(kernforgeDoctorMCPCheck(cfg))
	report.addCheck(kernforgeDoctorToolRegistryCheck(resolvedCWD))
	report.addCheck(kernforgeDoctorSandboxCheck(cfg))
	report.addCheck(kernforgeDoctorNetworkCheck(cfg))
	report.addCheck(kernforgeDoctorDaemonCheck())
	report.finalize()
	return report
}

func applyKernforgeDoctorOverrides(cfg *Config, opts kernforgeDoctorOptions) {
	if cfg == nil {
		return
	}
	if strings.TrimSpace(opts.ProviderOverride) != "" {
		cfg.Provider = strings.TrimSpace(opts.ProviderOverride)
	}
	if strings.TrimSpace(opts.ModelOverride) != "" {
		cfg.Model = strings.TrimSpace(opts.ModelOverride)
	}
	if strings.TrimSpace(opts.BaseURLOverride) != "" {
		cfg.BaseURL = strings.TrimSpace(opts.BaseURLOverride)
	}
	if strings.TrimSpace(opts.PermissionOverride) != "" {
		cfg.PermissionMode = strings.TrimSpace(opts.PermissionOverride)
	}
	if opts.ForceBypass {
		cfg.PermissionMode = string(ModeBypass)
	}
	if opts.BypassHookTrust {
		cfg.BypassHookTrust = true
	}
}

func (report *kernforgeDoctorReport) addCheck(check kernforgeDoctorCheck) {
	if report.Checks == nil {
		report.Checks = map[string]kernforgeDoctorCheck{}
	}
	check.ID = strings.TrimSpace(check.ID)
	if check.ID == "" {
		return
	}
	check.Status = normalizeKernforgeDoctorStatus(check.Status)
	report.Checks[check.ID] = check
	report.order = append(report.order, check.ID)
}

func (report *kernforgeDoctorReport) finalize() {
	okCount := 0
	warnCount := 0
	failCount := 0
	idleCount := 0
	for _, check := range report.Checks {
		switch normalizeKernforgeDoctorStatus(check.Status) {
		case kernforgeDoctorStatusFail:
			failCount++
		case kernforgeDoctorStatusWarn:
			warnCount++
		case kernforgeDoctorStatusIdle:
			idleCount++
		default:
			okCount++
		}
	}
	switch {
	case failCount > 0:
		report.OverallStatus = kernforgeDoctorStatusFail
	case warnCount > 0:
		report.OverallStatus = kernforgeDoctorStatusWarn
	default:
		report.OverallStatus = kernforgeDoctorStatusOK
	}
	report.Notes = nil
	if failCount > 0 {
		report.Notes = append(report.Notes, kernforgeDoctorNote{ID: "failures", Status: kernforgeDoctorStatusFail, Summary: fmt.Sprintf("%d failing check(s) need attention", failCount)})
	}
	if warnCount > 0 {
		report.Notes = append(report.Notes, kernforgeDoctorNote{ID: "warnings", Status: kernforgeDoctorStatusWarn, Summary: fmt.Sprintf("%d warning check(s) may affect operation", warnCount)})
	}
	if idleCount > 0 {
		report.Notes = append(report.Notes, kernforgeDoctorNote{ID: "idle", Status: kernforgeDoctorStatusIdle, Summary: fmt.Sprintf("%d optional check(s) were idle", idleCount)})
	}
	_ = okCount
}

func normalizeKernforgeDoctorStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case kernforgeDoctorStatusFail:
		return kernforgeDoctorStatusFail
	case kernforgeDoctorStatusWarn, "warning":
		return kernforgeDoctorStatusWarn
	case kernforgeDoctorStatusIdle, "skip", "skipped":
		return kernforgeDoctorStatusIdle
	default:
		return kernforgeDoctorStatusOK
	}
}

func kernforgeDoctorRuntimeCheck(cwd string, cfg Config) kernforgeDoctorCheck {
	exe, err := os.Executable()
	status := kernforgeDoctorStatusOK
	summary := "runtime metadata available"
	if err != nil {
		status = kernforgeDoctorStatusWarn
		summary = "runtime executable could not be resolved"
		exe = err.Error()
	}
	return kernforgeDoctorCheck{
		ID:       "runtime.provenance",
		Category: "Environment",
		Status:   status,
		Summary:  summary,
		Details: map[string]string{
			"version":    currentVersion(),
			"go":         runtime.Version(),
			"os":         runtime.GOOS,
			"arch":       runtime.GOARCH,
			"executable": exe,
			"cwd":        cwd,
			"shell":      cfg.Shell,
		},
	}
}

func kernforgeDoctorSearchCheck() kernforgeDoctorCheck {
	details := map[string]string{}
	status := kernforgeDoctorStatusOK
	summary := "required helper commands are discoverable"
	for _, name := range []string{"rg", "git", "go"} {
		path, err := exec.LookPath(name)
		if err != nil {
			details[name] = "missing"
			if name == "rg" {
				status = kernforgeDoctorStatusWarn
				summary = "ripgrep is missing; file discovery will be slower"
			}
			continue
		}
		details[name] = path
	}
	return kernforgeDoctorCheck{
		ID:             "environment.search",
		Category:       "Environment",
		Status:         status,
		Summary:        summary,
		Details:        details,
		Recommendation: recommendationIf(status == kernforgeDoctorStatusWarn, "Install rg or put it on PATH for faster repository scans."),
	}
}

func kernforgeDoctorWorkspaceCheck(cwd string) kernforgeDoctorCheck {
	info, err := os.Stat(cwd)
	if err != nil {
		return kernforgeDoctorCheck{
			ID:             "environment.workspace",
			Category:       "Environment",
			Status:         kernforgeDoctorStatusFail,
			Summary:        "workspace path is not readable",
			Details:        map[string]string{"cwd": cwd, "error": err.Error()},
			Recommendation: "Pass -cwd with an existing workspace directory.",
		}
	}
	if !info.IsDir() {
		return kernforgeDoctorCheck{
			ID:             "environment.workspace",
			Category:       "Environment",
			Status:         kernforgeDoctorStatusFail,
			Summary:        "workspace path is not a directory",
			Details:        map[string]string{"cwd": cwd, "mode": info.Mode().String()},
			Recommendation: "Pass -cwd with a directory.",
		}
	}
	return kernforgeDoctorCheck{
		ID:       "environment.workspace",
		Category: "Environment",
		Status:   kernforgeDoctorStatusOK,
		Summary:  "workspace is readable",
		Details:  map[string]string{"cwd": cwd, "mode": info.Mode().String()},
	}
}

func kernforgeDoctorStateCheck(cfg Config) kernforgeDoctorCheck {
	details := map[string]string{"session_dir": cfg.SessionDir}
	stats, err := kernforgeDoctorDirStats(cfg.SessionDir, 2000)
	if err != nil {
		return kernforgeDoctorCheck{
			ID:       "state.sessions",
			Category: "Environment",
			Status:   kernforgeDoctorStatusIdle,
			Summary:  "session directory is not initialized",
			Details:  map[string]string{"session_dir": cfg.SessionDir, "error": err.Error()},
		}
	}
	details["entries"] = fmt.Sprintf("%d", stats.entries)
	details["bytes_sampled"] = fmt.Sprintf("%d", stats.bytes)
	details["truncated"] = fmt.Sprintf("%t", stats.truncated)
	status := kernforgeDoctorStatusOK
	summary := "session state is readable"
	if stats.truncated {
		status = kernforgeDoctorStatusWarn
		summary = "session directory is large"
	}
	return kernforgeDoctorCheck{
		ID:             "state.sessions",
		Category:       "Environment",
		Status:         status,
		Summary:        summary,
		Details:        details,
		Recommendation: recommendationIf(stats.truncated, "Archive or prune old session files if startup or backup scans become slow."),
	}
}

type kernforgeDoctorDirStat struct {
	entries   int
	bytes     int64
	truncated bool
}

func kernforgeDoctorDirStats(root string, maxEntries int) (kernforgeDoctorDirStat, error) {
	var stats kernforgeDoctorDirStat
	if strings.TrimSpace(root) == "" {
		return stats, fmt.Errorf("empty path")
	}
	info, err := os.Stat(root)
	if err != nil {
		return stats, err
	}
	if !info.IsDir() {
		return stats, fmt.Errorf("not a directory")
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path == root {
			return nil
		}
		stats.entries++
		if !entry.IsDir() {
			if info, err := entry.Info(); err == nil {
				stats.bytes += info.Size()
			}
		}
		if maxEntries > 0 && stats.entries >= maxEntries {
			stats.truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	return stats, err
}

func kernforgeDoctorConfigCheck(cwd string, cfg Config, opts kernforgeDoctorOptions, cfgErr error, autoPopulateErr error) kernforgeDoctorCheck {
	details := map[string]string{
		"cwd":             cwd,
		"profile":         strings.TrimSpace(opts.Profile),
		"strict_config":   fmt.Sprintf("%t", opts.StrictConfig),
		"provider":        cfg.Provider,
		"model":           cfg.Model,
		"config_paths":    strings.Join(configSearchPathsWithProfile(cwd, opts.Profile), string(os.PathListSeparator)),
		"mcp_servers":     fmt.Sprintf("%d", len(cfg.MCPServers)),
		"hooks_enabled":   boolPtrDetail(cfg.HooksEnabled),
		"auto_verify":     boolPtrDetail(cfg.AutoVerify),
		"progress":        cfg.ProgressDisplay,
		"permission_mode": cfg.PermissionMode,
	}
	if cfgErr != nil {
		details["error"] = cfgErr.Error()
		return kernforgeDoctorCheck{
			ID:             "configuration.config",
			Category:       "Configuration",
			Status:         kernforgeDoctorStatusFail,
			Summary:        "configuration failed to load",
			Details:        details,
			Recommendation: "Fix the reported config file or rerun without -strict-config to inspect non-strict behavior.",
		}
	}
	if autoPopulateErr != nil {
		details["verification_tool_error"] = autoPopulateErr.Error()
		return kernforgeDoctorCheck{
			ID:             "configuration.config",
			Category:       "Configuration",
			Status:         kernforgeDoctorStatusWarn,
			Summary:        "configuration loaded with verification tool warnings",
			Details:        details,
			Recommendation: "Check configured build tool paths or install the missing verification toolchain.",
		}
	}
	return kernforgeDoctorCheck{
		ID:       "configuration.config",
		Category: "Configuration",
		Status:   kernforgeDoctorStatusOK,
		Summary:  "configuration loaded",
		Details:  details,
	}
}

func kernforgeDoctorAuthCheck(cfg Config) kernforgeDoctorCheck {
	provider := normalizeProviderName(cfg.Provider)
	details := map[string]string{
		"provider":           provider,
		"api_key_configured": fmt.Sprintf("%t", doctorProviderAPIKeyConfigured(cfg, provider)),
		"provider_key_count": fmt.Sprintf("%d", len(cfg.ProviderKeys)),
		"openai_env":         presentDetail(os.Getenv("OPENAI_API_KEY") != ""),
		"anthropic_env":      presentDetail(os.Getenv("ANTHROPIC_API_KEY") != ""),
		"openrouter_env":     presentDetail(os.Getenv("OPENROUTER_API_KEY") != ""),
		"codex_auth_file":    codexOAuthAuthFilePath(),
		"codex_auth_usable":  fmt.Sprintf("%t", codexOAuthAuthFileUsableForWorkspaces("", cfg.ForcedChatGPTWorkspaceID)),
		"forced_workspaces":  fmt.Sprintf("%d", len(cfg.ForcedChatGPTWorkspaceID)),
	}
	if provider == "" {
		return kernforgeDoctorCheck{ID: "configuration.auth", Category: "Configuration", Status: kernforgeDoctorStatusIdle, Summary: "no provider selected", Details: details}
	}
	if provider == "codex-cli" || provider == "anthropic-claude-cli" || isLocalOpenAICompatibleProvider(provider) || provider == "ollama" {
		return kernforgeDoctorCheck{ID: "configuration.auth", Category: "Configuration", Status: kernforgeDoctorStatusOK, Summary: "selected provider does not require Kernforge-managed API credentials", Details: details}
	}
	if provider == "openai-codex" && codexOAuthAuthFileUsableForWorkspaces("", cfg.ForcedChatGPTWorkspaceID) {
		return kernforgeDoctorCheck{ID: "configuration.auth", Category: "Configuration", Status: kernforgeDoctorStatusOK, Summary: "OpenAI Codex OAuth auth is configured", Details: details}
	}
	if doctorProviderAPIKeyConfigured(cfg, provider) {
		return kernforgeDoctorCheck{ID: "configuration.auth", Category: "Configuration", Status: kernforgeDoctorStatusOK, Summary: "API key material is configured", Details: details}
	}
	return kernforgeDoctorCheck{
		ID:             "configuration.auth",
		Category:       "Configuration",
		Status:         kernforgeDoctorStatusWarn,
		Summary:        "selected provider has no detected credentials",
		Details:        details,
		Recommendation: "Configure provider_keys, api_key, the provider-specific environment variable, or /codex-auth login for openai-codex.",
	}
}

func doctorProviderAPIKeyConfigured(cfg Config, provider string) bool {
	if strings.TrimSpace(cfg.APIKey) != "" {
		return true
	}
	if cfg.ProviderKeys != nil && strings.TrimSpace(cfg.ProviderKeys[provider]) != "" {
		return true
	}
	switch provider {
	case "openai", "openai-codex":
		return strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != ""
	case "anthropic":
		return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != ""
	case "openrouter":
		return strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) != ""
	case "deepseek":
		return strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) != ""
	default:
		return false
	}
}

func kernforgeDoctorMCPCheck(cfg Config) kernforgeDoctorCheck {
	details := map[string]string{}
	if len(cfg.MCPServers) == 0 {
		return kernforgeDoctorCheck{ID: "configuration.mcp", Category: "Configuration", Status: kernforgeDoctorStatusIdle, Summary: "no MCP servers configured"}
	}
	disabled := 0
	missingEnv := []string{}
	for _, server := range cfg.MCPServers {
		if server.Disabled {
			disabled++
			continue
		}
		for _, envVar := range server.EnvVars {
			name := strings.TrimSpace(envVar.Name)
			if name == "" {
				continue
			}
			if strings.TrimSpace(server.Env[name]) == "" && strings.TrimSpace(os.Getenv(name)) == "" {
				missingEnv = append(missingEnv, fmt.Sprintf("%s:%s", firstNonBlankString(server.Name, server.Command, "mcp"), name))
			}
		}
	}
	details["configured"] = fmt.Sprintf("%d", len(cfg.MCPServers))
	details["disabled"] = fmt.Sprintf("%d", disabled)
	details["enabled"] = fmt.Sprintf("%d", len(cfg.MCPServers)-disabled)
	if len(missingEnv) > 0 {
		sort.Strings(missingEnv)
		details["missing_env_vars"] = strings.Join(missingEnv, ", ")
		return kernforgeDoctorCheck{
			ID:             "configuration.mcp",
			Category:       "Configuration",
			Status:         kernforgeDoctorStatusWarn,
			Summary:        "MCP configuration has optional missing environment variables",
			Details:        details,
			Recommendation: "Set the missing MCP env vars, provide them in user-level mcp_servers[].env, or disable the affected server.",
		}
	}
	return kernforgeDoctorCheck{ID: "configuration.mcp", Category: "Configuration", Status: kernforgeDoctorStatusOK, Summary: "MCP configuration is locally consistent", Details: details}
}

func kernforgeDoctorToolRegistryCheck(cwd string) kernforgeDoctorCheck {
	ws := Workspace{
		BaseRoot: cwd,
		Root:     cwd,
	}
	registry := buildRegistry(ws, nil)
	issues := formatToolRegistrationIssues(registry.RegistrationIssues())
	details := map[string]string{
		"dispatchable_tools": fmt.Sprintf("%d", len(registry.ToolNames())),
		"model_visible":      fmt.Sprintf("%d", len(registry.Definitions())),
		"issues":             fmt.Sprintf("%d", len(issues)),
	}
	if len(issues) > 0 {
		details["issue_details"] = strings.Join(issues, "; ")
		return kernforgeDoctorCheck{
			ID:             "runtime.tool_registry",
			Category:       "Runtime",
			Status:         kernforgeDoctorStatusWarn,
			Summary:        "tool registry skipped invalid or duplicate tool definitions",
			Details:        details,
			Recommendation: "Fix the reported tool definitions so hidden or dynamic tools do not disappear silently.",
		}
	}
	return kernforgeDoctorCheck{
		ID:       "runtime.tool_registry",
		Category: "Runtime",
		Status:   kernforgeDoctorStatusOK,
		Summary:  "tool registry definitions are valid",
		Details:  details,
	}
}

func kernforgeDoctorSandboxCheck(cfg Config) kernforgeDoctorCheck {
	return kernforgeDoctorCheck{
		ID:       "configuration.permissions",
		Category: "Configuration",
		Status:   kernforgeDoctorStatusOK,
		Summary:  "permission settings loaded",
		Details: map[string]string{
			"permission_mode":   cfg.PermissionMode,
			"bypass_hook_trust": fmt.Sprintf("%t", cfg.BypassHookTrust),
			"shell_timeout":     configShellTimeout(cfg).String(),
			"auto_checkpoint":   boolPtrDetail(cfg.AutoCheckpointEdits),
			"review_auto":       boolPtrDetail(cfg.Review.AutoAfterChange),
		},
	}
}

func kernforgeDoctorNetworkCheck(cfg Config) kernforgeDoctorCheck {
	proxies := doctorPresentEnvNames("HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY", "npm_config_proxy", "npm_config_https_proxy")
	status := kernforgeDoctorStatusOK
	summary := "network-related environment is readable"
	if len(proxies) > 0 {
		status = kernforgeDoctorStatusWarn
		summary = "proxy environment variables are set"
	}
	return kernforgeDoctorCheck{
		ID:       "connectivity.network",
		Category: "Connectivity",
		Status:   status,
		Summary:  summary,
		Details: map[string]string{
			"provider":       normalizeProviderName(cfg.Provider),
			"base_url":       cfg.BaseURL,
			"proxy_env_vars": strings.Join(proxies, ", "),
			"reachability":   "not probed by default",
		},
		Recommendation: recommendationIf(len(proxies) > 0, "If provider calls fail, verify proxy, VPN, firewall, DNS, and custom CA settings."),
	}
}

func kernforgeDoctorDaemonCheck() kernforgeDoctorCheck {
	state, ok := readKernforgeDaemonState()
	if !ok {
		return kernforgeDoctorCheck{ID: "background.daemon", Category: "Background Server", Status: kernforgeDoctorStatusIdle, Summary: "daemon is not running"}
	}
	details := map[string]string{
		"state_path":  kernforgeDaemonStatePath(),
		"pid":         fmt.Sprintf("%d", state.PID),
		"addr":        state.Addr,
		"started_at":  state.StartedAt.Format(time.RFC3339),
		"log_path":    state.LogPath,
		"state_token": presentDetail(strings.TrimSpace(state.Token) != ""),
	}
	health, err := kernforgeDaemonHealth(state, 2*time.Second)
	if err != nil {
		details["error"] = err.Error()
		return kernforgeDoctorCheck{
			ID:             "background.daemon",
			Category:       "Background Server",
			Status:         kernforgeDoctorStatusWarn,
			Summary:        "daemon state exists but is not reachable",
			Details:        details,
			Recommendation: "Run `kernforge daemon stop` to clear stale state, then `kernforge daemon start` if proxy mode is needed.",
		}
	}
	if data, err := json.Marshal(health); err == nil {
		details["health"] = string(data)
	}
	return kernforgeDoctorCheck{ID: "background.daemon", Category: "Background Server", Status: kernforgeDoctorStatusOK, Summary: "daemon is reachable", Details: details}
}

func renderKernforgeDoctorReport(report kernforgeDoctorReport, opts kernforgeDoctorOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Kernforge Doctor v%s - %s-%s\n\n", currentVersion(), runtime.GOOS, runtime.GOARCH)
	if len(report.Notes) > 0 {
		b.WriteString("Notes\n")
		for _, note := range report.Notes {
			fmt.Fprintf(&b, "  %s %-10s %s\n", note.Status, note.ID, note.Summary)
		}
		b.WriteString("\n")
	}
	for _, category := range kernforgeDoctorCategories(report) {
		b.WriteString(category)
		b.WriteString("\n")
		for _, id := range report.order {
			check := report.Checks[id]
			if check.Category != category {
				continue
			}
			fmt.Fprintf(&b, "  %s %-22s %s\n", check.Status, doctorShortID(check.ID), check.Summary)
			if !opts.Summary {
				writeKernforgeDoctorDetails(&b, check, opts)
			}
		}
		b.WriteString("\n")
	}
	okCount, idleCount, warnCount, failCount := kernforgeDoctorStatusCounts(report)
	fmt.Fprintf(&b, "%d ok - %d idle - %d warn - %d fail - overall %s\n", okCount, idleCount, warnCount, failCount, report.OverallStatus)
	if opts.Summary {
		b.WriteString("Run `kernforge doctor` without --summary for detailed diagnostics.\n")
	} else {
		b.WriteString("--summary compact output    --json redacted report\n")
	}
	return b.String()
}

func writeKernforgeDoctorDetails(b *strings.Builder, check kernforgeDoctorCheck, opts kernforgeDoctorOptions) {
	keys := make([]string, 0, len(check.Details))
	for key := range check.Details {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	limit := len(keys)
	if !opts.All && limit > 10 {
		limit = 10
	}
	for _, key := range keys[:limit] {
		fmt.Fprintf(b, "      %-20s %s\n", key, check.Details[key])
	}
	if limit < len(keys) {
		fmt.Fprintf(b, "      %-20s %d more detail row(s); rerun with --all\n", "truncated", len(keys)-limit)
	}
	if strings.TrimSpace(check.Recommendation) != "" {
		fmt.Fprintf(b, "      %-20s %s\n", "next step", check.Recommendation)
	}
}

func kernforgeDoctorCategories(report kernforgeDoctorReport) []string {
	seen := map[string]struct{}{}
	categories := []string{}
	for _, id := range report.order {
		category := report.Checks[id].Category
		if _, ok := seen[category]; ok {
			continue
		}
		seen[category] = struct{}{}
		categories = append(categories, category)
	}
	return categories
}

func kernforgeDoctorStatusCounts(report kernforgeDoctorReport) (okCount int, idleCount int, warnCount int, failCount int) {
	for _, check := range report.Checks {
		switch normalizeKernforgeDoctorStatus(check.Status) {
		case kernforgeDoctorStatusFail:
			failCount++
		case kernforgeDoctorStatusWarn:
			warnCount++
		case kernforgeDoctorStatusIdle:
			idleCount++
		default:
			okCount++
		}
	}
	return okCount, idleCount, warnCount, failCount
}

func doctorShortID(id string) string {
	parts := strings.Split(strings.TrimSpace(id), ".")
	if len(parts) == 0 {
		return id
	}
	return parts[len(parts)-1]
}

func boolPtrDetail(value *bool) string {
	if value == nil {
		return "unset"
	}
	return fmt.Sprintf("%t", *value)
}

func presentDetail(present bool) string {
	if present {
		return "present"
	}
	return "absent"
}

func doctorPresentEnvNames(names ...string) []string {
	present := []string{}
	for _, name := range names {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			present = append(present, name)
		}
	}
	sort.Strings(present)
	return present
}

func recommendationIf(condition bool, value string) string {
	if condition {
		return value
	}
	return ""
}
