package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MCPServerConfig struct {
	Name                      string                `json:"name"`
	Command                   string                `json:"command,omitempty"`
	Args                      []string              `json:"args,omitempty"`
	Env                       map[string]string     `json:"env,omitempty"`
	EnvVars                   []MCPServerEnvVar     `json:"env_vars,omitempty"`
	Cwd                       string                `json:"cwd,omitempty"`
	URL                       string                `json:"url,omitempty"`
	BearerTokenEnvVar         string                `json:"bearer_token_env_var,omitempty"`
	HTTPHeaders               map[string]string     `json:"http_headers,omitempty"`
	EnvHTTPHeaders            map[string]string     `json:"env_http_headers,omitempty"`
	OAuth                     *MCPServerOAuthConfig `json:"oauth,omitempty"`
	OAuthResource             string                `json:"oauth_resource,omitempty"`
	EnvironmentID             string                `json:"environment_id,omitempty"`
	EnvironmentIDSet          bool                  `json:"-"`
	Capabilities              []string              `json:"capabilities,omitempty"`
	SupportsParallelToolCalls bool                  `json:"supports_parallel_tool_calls,omitempty"`
	Disabled                  bool                  `json:"disabled,omitempty"`
	DisabledSet               bool                  `json:"-"`
}

type mcpServerConfigJSON struct {
	Name                      string                `json:"name"`
	Command                   string                `json:"command,omitempty"`
	Args                      []string              `json:"args,omitempty"`
	Env                       map[string]string     `json:"env,omitempty"`
	EnvVars                   []MCPServerEnvVar     `json:"env_vars,omitempty"`
	Cwd                       string                `json:"cwd,omitempty"`
	URL                       string                `json:"url,omitempty"`
	BearerTokenEnvVar         string                `json:"bearer_token_env_var,omitempty"`
	HTTPHeaders               map[string]string     `json:"http_headers,omitempty"`
	EnvHTTPHeaders            map[string]string     `json:"env_http_headers,omitempty"`
	OAuth                     *MCPServerOAuthConfig `json:"oauth,omitempty"`
	OAuthResource             string                `json:"oauth_resource,omitempty"`
	EnvironmentID             *string               `json:"environment_id,omitempty"`
	Capabilities              []string              `json:"capabilities,omitempty"`
	SupportsParallelToolCalls bool                  `json:"supports_parallel_tool_calls,omitempty"`
	Disabled                  *bool                 `json:"disabled,omitempty"`
}

type MCPServerOAuthConfig struct {
	ClientID string `json:"client_id,omitempty"`
}

func (cfg *MCPServerConfig) UnmarshalJSON(data []byte) error {
	var raw mcpServerConfigJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := validateRawMCPServerConfig(raw); err != nil {
		return err
	}

	*cfg = MCPServerConfig{
		Name:                      raw.Name,
		Command:                   raw.Command,
		Args:                      raw.Args,
		Env:                       raw.Env,
		EnvVars:                   raw.EnvVars,
		Cwd:                       raw.Cwd,
		URL:                       raw.URL,
		BearerTokenEnvVar:         raw.BearerTokenEnvVar,
		HTTPHeaders:               raw.HTTPHeaders,
		EnvHTTPHeaders:            raw.EnvHTTPHeaders,
		OAuth:                     raw.OAuth,
		OAuthResource:             raw.OAuthResource,
		EnvironmentID:             defaultMCPServerEnvironmentID,
		Capabilities:              raw.Capabilities,
		SupportsParallelToolCalls: raw.SupportsParallelToolCalls,
	}
	if raw.EnvironmentID != nil {
		cfg.EnvironmentID = normalizeMCPServerEnvironmentID(*raw.EnvironmentID)
		cfg.EnvironmentIDSet = true
	}
	if raw.Disabled != nil {
		cfg.Disabled = *raw.Disabled
		cfg.DisabledSet = true
	}
	return nil
}

func validateRawMCPServerConfig(raw mcpServerConfigJSON) error {
	hasCommand := strings.TrimSpace(raw.Command) != ""
	hasURL := strings.TrimSpace(raw.URL) != ""
	if hasCommand && hasURL {
		return fmt.Errorf("mcp server cannot set both command and url")
	}
	if hasCommand {
		if strings.TrimSpace(raw.BearerTokenEnvVar) != "" {
			return fmt.Errorf("bearer_token_env_var is not supported for stdio")
		}
		if len(raw.HTTPHeaders) > 0 {
			return fmt.Errorf("http_headers is not supported for stdio")
		}
		if len(raw.EnvHTTPHeaders) > 0 {
			return fmt.Errorf("env_http_headers is not supported for stdio")
		}
		if raw.OAuth != nil {
			return fmt.Errorf("oauth is not supported for stdio")
		}
		if strings.TrimSpace(raw.OAuthResource) != "" {
			return fmt.Errorf("oauth_resource is not supported for stdio")
		}
	}
	if hasURL {
		if len(raw.Args) > 0 {
			return fmt.Errorf("args is not supported for streamable_http")
		}
		if len(raw.Env) > 0 {
			return fmt.Errorf("env is not supported for streamable_http")
		}
		if len(raw.EnvVars) > 0 {
			return fmt.Errorf("env_vars is not supported for streamable_http")
		}
		if strings.TrimSpace(raw.Cwd) != "" {
			return fmt.Errorf("cwd is not supported for streamable_http")
		}
	}
	return nil
}

func (cfg MCPServerConfig) MarshalJSON() ([]byte, error) {
	var disabled *bool
	if cfg.DisabledSet || cfg.Disabled {
		value := cfg.Disabled
		disabled = &value
	}
	environmentID := normalizeMCPServerEnvironmentID(cfg.EnvironmentID)

	return json.Marshal(mcpServerConfigJSON{
		Name:                      cfg.Name,
		Command:                   cfg.Command,
		Args:                      cfg.Args,
		Env:                       cfg.Env,
		EnvVars:                   cfg.EnvVars,
		Cwd:                       cfg.Cwd,
		URL:                       cfg.URL,
		BearerTokenEnvVar:         cfg.BearerTokenEnvVar,
		HTTPHeaders:               cfg.HTTPHeaders,
		EnvHTTPHeaders:            cfg.EnvHTTPHeaders,
		OAuth:                     cfg.OAuth,
		OAuthResource:             cfg.OAuthResource,
		EnvironmentID:             &environmentID,
		Capabilities:              cfg.Capabilities,
		SupportsParallelToolCalls: cfg.SupportsParallelToolCalls,
		Disabled:                  disabled,
	})
}

type MCPServerEnvVar struct {
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
}

type MCPToolDescriptor struct {
	Name         string
	Description  string
	InputSchema  map[string]any
	OutputSchema map[string]any
	Annotations  map[string]any
	ReadOnlyHint *bool
}

type MCPResourceDescriptor struct {
	URI         string
	Name        string
	Description string
	MIMEType    string
}

type MCPPromptArgument struct {
	Name        string
	Description string
	Required    bool
}

type MCPPromptDescriptor struct {
	Name        string
	Description string
	Arguments   []MCPPromptArgument
}

type MCPResourceRef struct {
	Server   string
	Resource MCPResourceDescriptor
}

type MCPPromptRef struct {
	Server string
	Prompt MCPPromptDescriptor
}

type MCPServerStatus struct {
	Name          string
	Transport     string
	Command       string
	URL           string
	Cwd           string
	EnvironmentID string
	ToolCount     int
	ResourceCount int
	PromptCount   int
	Error         string
}

type MCPManager struct {
	servers   []*MCPClient
	status    []MCPServerStatus
	workspace Workspace
}

type MCPClient struct {
	config       MCPServerConfig
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       *bufio.Reader
	http         *mcpHTTPTransport
	writeMu      sync.Mutex
	pendingMu    sync.Mutex
	nextID       int64
	pending      map[int64]chan mcpRPCResult
	readLoopOnce sync.Once
	readErr      error
	tools        []MCPToolDescriptor
	resources    []MCPResourceDescriptor
	prompts      []MCPPromptDescriptor
	status       MCPServerStatus
	stderrMu     sync.Mutex
	stderr       []string
}

type mcpHTTPTransport struct {
	endpoint  string
	client    *http.Client
	headers   map[string]string
	sessionID string
	mu        sync.Mutex
}

type mcpRPCResult struct {
	payload map[string]any
	err     error
}

type MCPTool struct {
	client      *MCPClient
	namespaced  string
	remote      MCPToolDescriptor
	description string
	workspace   Workspace
}

type MCPResourceTool struct {
	client    *MCPClient
	workspace Workspace
}

type MCPPromptTool struct {
	client    *MCPClient
	workspace Workspace
}

type mcpTurnMetadataContextKey struct{}
type mcpConversationHistoryContextKey struct{}

const mcpTurnMetadataMetaKey = "x-codex-turn-metadata"
const mcpConversationHistoryMetaKey = "x-codex-conversation-history"
const mcpTurnMetadataUserInputRequestedKey = "user_input_requested_during_turn"
const mcpBridgeCallIDMetaKey = "codex_bridge_mcp_call_id"
const defaultMCPServerEnvironmentID = "local"

var windowsCoreMCPEnvVars = []string{
	"PATH",
	"PATHEXT",
	"SHELL",
	"COMSPEC",
	"SYSTEMROOT",
	"SYSTEMDRIVE",
	"USERNAME",
	"USERDOMAIN",
	"USERPROFILE",
	"HOMEDRIVE",
	"HOMEPATH",
	"PROGRAMFILES",
	"PROGRAMFILES(X86)",
	"PROGRAMW6432",
	"PROGRAMDATA",
	"LOCALAPPDATA",
	"APPDATA",
	"TEMP",
	"TMP",
	"TMPDIR",
	"POWERSHELL",
	"PWSH",
}

var unixCoreMCPEnvVars = []string{
	"PATH",
	"SHELL",
	"TMPDIR",
	"TEMP",
	"TMP",
	"HOME",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"LOGNAME",
	"USER",
}

var managedProxyMCPEnvVars = []string{
	"CODEX_NETWORK_PROXY_ACTIVE",
	"CODEX_NETWORK_ALLOW_LOCAL_BINDING",
	"ELECTRON_GET_USE_PROXY",
	"NODE_USE_ENV_PROXY",
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"http_proxy",
	"https_proxy",
	"WS_PROXY",
	"WSS_PROXY",
	"ws_proxy",
	"wss_proxy",
	"ALL_PROXY",
	"all_proxy",
	"FTP_PROXY",
	"ftp_proxy",
	"NO_PROXY",
	"no_proxy",
	"npm_config_http_proxy",
	"npm_config_https_proxy",
	"npm_config_proxy",
	"NPM_CONFIG_HTTP_PROXY",
	"NPM_CONFIG_HTTPS_PROXY",
	"NPM_CONFIG_PROXY",
	"npm_config_noproxy",
	"NPM_CONFIG_NOPROXY",
	"YARN_HTTP_PROXY",
	"YARN_HTTPS_PROXY",
	"YARN_NO_PROXY",
	"BUNDLE_HTTP_PROXY",
	"BUNDLE_HTTPS_PROXY",
	"BUNDLE_NO_PROXY",
	"PIP_PROXY",
	"DOCKER_HTTP_PROXY",
	"DOCKER_HTTPS_PROXY",
}

var proxyURLMCPEnvVars = []string{
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"http_proxy",
	"https_proxy",
	"WS_PROXY",
	"WSS_PROXY",
	"ws_proxy",
	"wss_proxy",
	"ALL_PROXY",
	"all_proxy",
	"FTP_PROXY",
	"ftp_proxy",
	"npm_config_http_proxy",
	"npm_config_https_proxy",
	"npm_config_proxy",
	"NPM_CONFIG_HTTP_PROXY",
	"NPM_CONFIG_HTTPS_PROXY",
	"NPM_CONFIG_PROXY",
	"YARN_HTTP_PROXY",
	"YARN_HTTPS_PROXY",
	"BUNDLE_HTTP_PROXY",
	"BUNDLE_HTTPS_PROXY",
	"PIP_PROXY",
	"DOCKER_HTTP_PROXY",
	"DOCKER_HTTPS_PROXY",
}

func (v *MCPServerEnvVar) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		v.Name = strings.TrimSpace(name)
		v.Source = ""
		return nil
	}
	var raw struct {
		Name   string `json:"name"`
		Source string `json:"source,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("mcp env_vars entry must be a string or object with name/source: %w", err)
	}
	v.Name = strings.TrimSpace(raw.Name)
	v.Source = strings.ToLower(strings.TrimSpace(raw.Source))
	if err := v.validate(); err != nil {
		return err
	}
	return nil
}

func (v MCPServerEnvVar) validate() error {
	switch strings.TrimSpace(strings.ToLower(v.Source)) {
	case "", "local", "remote":
		return nil
	default:
		return fmt.Errorf("unsupported env_vars source %q; expected \"local\" or \"remote\"", v.Source)
	}
}

func (v MCPServerEnvVar) isRemoteSource() bool {
	return strings.EqualFold(strings.TrimSpace(v.Source), "remote")
}

func contextWithMCPTurnMetadata(ctx context.Context, metadata map[string]any) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	metadata = cloneStringAnyMap(metadata)
	if len(metadata) == 0 {
		return ctx
	}
	return context.WithValue(ctx, mcpTurnMetadataContextKey{}, metadata)
}

func contextWithMCPConversationHistory(ctx context.Context, history map[string]any) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	history = cloneStringAnyMap(history)
	if len(history) == 0 {
		return ctx
	}
	return context.WithValue(ctx, mcpConversationHistoryContextKey{}, history)
}

func mcpToolCallRequestMetaFromContext(ctx context.Context) map[string]any {
	if ctx == nil {
		return nil
	}
	meta := map[string]any{}
	if metadata, ok := ctx.Value(mcpTurnMetadataContextKey{}).(map[string]any); ok && len(metadata) > 0 {
		meta[mcpTurnMetadataMetaKey] = cloneStringAnyMap(metadata)
	}
	if history, ok := ctx.Value(mcpConversationHistoryContextKey{}).(map[string]any); ok && len(history) > 0 {
		meta[mcpConversationHistoryMetaKey] = cloneStringAnyMap(history)
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

func mcpConversationHistorySnapshot(messages []Message) map[string]any {
	if len(messages) == 0 {
		return nil
	}
	items := make([]any, 0, len(messages))
	for _, message := range messages {
		message.ToolMeta = nil
		data, err := json.Marshal(message)
		if err != nil {
			continue
		}
		item := map[string]any{}
		if err := json.Unmarshal(data, &item); err != nil {
			continue
		}
		if len(item) == 0 {
			continue
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil
	}
	return map[string]any{"items": items}
}

func withMCPBridgeCallIDMeta(meta map[string]any, callID string) map[string]any {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return meta
	}
	if meta == nil {
		meta = map[string]any{}
	} else {
		meta = cloneStringAnyMap(meta)
	}
	meta[mcpBridgeCallIDMetaKey] = callID
	return meta
}

func newMCPBridgeCallID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return "mcp-" + hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("mcp-%d", time.Now().UnixNano())
}

func cloneStringAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func LoadMCPManager(ws Workspace, configs []MCPServerConfig) (*MCPManager, []string) {
	manager := &MCPManager{workspace: ws}
	warnings := []string{}
	usedNames := map[string]int{}
	for _, cfg := range configs {
		if cfg.Disabled {
			continue
		}
		name := deriveMCPServerName(cfg)
		if name == "" {
			warnings = append(warnings, "mcp server is missing name/command/url")
			continue
		}
		usedNames[name]++
		if usedNames[name] > 1 {
			name = fmt.Sprintf("%s-%d", name, usedNames[name])
		}
		cfg.Name = name
		client, clientWarnings, err := startMCPClient(ws, cfg)
		if err != nil {
			manager.status = append(manager.status, MCPServerStatus{
				Name:          cfg.Name,
				Transport:     mcpServerTransport(cfg),
				Command:       cfg.Command,
				URL:           cfg.URL,
				Cwd:           resolveMCPServerCwd(ws, cfg),
				EnvironmentID: effectiveMCPServerEnvironmentID(cfg),
				Error:         err.Error(),
			})
			warnings = append(warnings, fmt.Sprintf("mcp server %s: %v", cfg.Name, err))
			continue
		}
		warnings = append(warnings, clientWarnings...)
		manager.servers = append(manager.servers, client)
		manager.status = append(manager.status, client.status)
	}
	return manager, warnings
}

func deriveMCPServerName(cfg MCPServerConfig) string {
	if trimmed := strings.TrimSpace(cfg.Name); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(cfg.URL); trimmed != "" {
		if parsed, err := url.Parse(trimmed); err == nil {
			host := strings.TrimSpace(parsed.Hostname())
			if host != "" {
				return host
			}
		}
		return "streamable-http"
	}
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return ""
	}
	base := filepath.Base(command)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return base
}

func mcpServerTransport(cfg MCPServerConfig) string {
	if strings.TrimSpace(cfg.URL) != "" {
		return "streamable_http"
	}
	return "stdio"
}

func resolveMCPServerCwd(ws Workspace, cfg MCPServerConfig) string {
	if strings.TrimSpace(cfg.URL) != "" {
		return ""
	}
	if strings.TrimSpace(cfg.Cwd) == "" {
		return ws.Root
	}
	if filepath.IsAbs(cfg.Cwd) {
		return filepath.Clean(cfg.Cwd)
	}
	return filepath.Clean(filepath.Join(ws.Root, cfg.Cwd))
}

func effectiveMCPServerEnvironmentID(cfg MCPServerConfig) string {
	return normalizeMCPServerEnvironmentID(cfg.EnvironmentID)
}

func normalizeMCPServerEnvironmentID(environmentID string) string {
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return defaultMCPServerEnvironmentID
	}
	return environmentID
}

func validateMCPServerEnvironment(cfg MCPServerConfig) error {
	environmentID := effectiveMCPServerEnvironmentID(cfg)
	if environmentID == defaultMCPServerEnvironmentID {
		return nil
	}
	if strings.TrimSpace(cfg.URL) != "" {
		return nil
	}
	if strings.TrimSpace(cfg.Command) != "" {
		cwd := strings.TrimSpace(cfg.Cwd)
		if cwd == "" {
			return fmt.Errorf("stdio MCP server %q references environment_id %q but requires an absolute cwd", cfg.Name, environmentID)
		}
		if !filepath.IsAbs(cwd) {
			return fmt.Errorf("stdio MCP server %q references environment_id %q but requires an absolute cwd, got %q", cfg.Name, environmentID, cwd)
		}
	}
	return fmt.Errorf("mcp server %q references unsupported environment_id %q", cfg.Name, environmentID)
}

func normalizeMCPServerEnvVars(vars []MCPServerEnvVar) []MCPServerEnvVar {
	if len(vars) == 0 {
		return nil
	}
	out := make([]MCPServerEnvVar, 0, len(vars))
	seen := map[string]struct{}{}
	for _, envVar := range vars {
		normalized := MCPServerEnvVar{
			Name:   strings.TrimSpace(envVar.Name),
			Source: strings.ToLower(strings.TrimSpace(envVar.Source)),
		}
		if normalized.Name == "" {
			continue
		}
		key := strings.ToUpper(normalized.Name) + "\x00" + normalized.Source
		if runtime.GOOS != "windows" {
			key = normalized.Name + "\x00" + normalized.Source
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func defaultMCPEnvVarNames() []string {
	if runtime.GOOS == "windows" {
		return windowsCoreMCPEnvVars
	}
	return unixCoreMCPEnvVars
}

func buildMCPProcessEnv(cfg MCPServerConfig) ([]string, error) {
	env := map[string]string{}
	addFromParent := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if value, ok := os.LookupEnv(name); ok {
			env[name] = value
		}
	}
	for _, name := range defaultMCPEnvVarNames() {
		addFromParent(name)
	}
	if parentManagedProxyActive() {
		for _, name := range managedProxyMCPEnvVars {
			addFromParent(name)
		}
	}
	for _, envVar := range normalizeMCPServerEnvVars(cfg.EnvVars) {
		if err := envVar.validate(); err != nil {
			return nil, err
		}
		if envVar.isRemoteSource() {
			return nil, fmt.Errorf("env_vars entry %q uses source \"remote\", which requires remote MCP stdio", envVar.Name)
		}
		addFromParent(envVar.Name)
	}
	for key, value := range cfg.Env {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" {
			addFromParent(trimmedKey)
			continue
		}
		env[trimmedKey] = trimmedValue
	}
	applyMCPNodeProxyOptIn(env)
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	sort.Strings(out)
	return out, nil
}

func parentManagedProxyActive() bool {
	value, ok := os.LookupEnv("CODEX_NETWORK_PROXY_ACTIVE")
	normalized := strings.ToLower(strings.TrimSpace(value))
	return ok && normalized != "" && normalized != "0" && normalized != "false"
}

func applyMCPNodeProxyOptIn(env map[string]string) {
	if strings.TrimSpace(env["NODE_USE_ENV_PROXY"]) != "" {
		return
	}
	for _, key := range proxyURLMCPEnvVars {
		if strings.TrimSpace(env[key]) != "" {
			env["NODE_USE_ENV_PROXY"] = "1"
			return
		}
	}
}

func startMCPClient(ws Workspace, cfg MCPServerConfig) (*MCPClient, []string, error) {
	if strings.TrimSpace(cfg.URL) != "" {
		return startMCPHTTPClient(cfg)
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, nil, fmt.Errorf("missing command")
	}
	if err := validateMCPServerEnvironment(cfg); err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Dir = resolveMCPServerCwd(ws, cfg)
	env, err := buildMCPProcessEnv(cfg)
	if err != nil {
		return nil, nil, err
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	client := &MCPClient{
		config: cfg,
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
		status: MCPServerStatus{
			Name:          cfg.Name,
			Transport:     mcpServerTransport(cfg),
			Command:       cfg.Command,
			Cwd:           cmd.Dir,
			EnvironmentID: effectiveMCPServerEnvironmentID(cfg),
		},
	}
	go client.captureStderr(stderr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.initialize(ctx); err != nil {
		client.Close()
		return nil, nil, err
	}
	tools, err := client.listTools(ctx)
	if err != nil {
		client.Close()
		return nil, nil, err
	}
	warnings := []string{}
	tools, toolWarnings := filterValidMCPToolDescriptors(cfg.Name, tools)
	warnings = append(warnings, toolWarnings...)
	client.tools = tools
	client.status.ToolCount = len(tools)
	if resources, err := client.listResources(ctx); err == nil {
		client.resources = resources
		client.status.ResourceCount = len(resources)
	} else if !isOptionalMCPMethodError(err) {
		warnings = append(warnings, fmt.Sprintf("mcp server %s resources: %v", cfg.Name, err))
	}
	if prompts, err := client.listPrompts(ctx); err == nil {
		client.prompts = prompts
		client.status.PromptCount = len(prompts)
	} else if !isOptionalMCPMethodError(err) {
		warnings = append(warnings, fmt.Sprintf("mcp server %s prompts: %v", cfg.Name, err))
	}
	return client, warnings, nil
}

func startMCPHTTPClient(cfg MCPServerConfig) (*MCPClient, []string, error) {
	endpoint := strings.TrimSpace(cfg.URL)
	if endpoint == "" {
		return nil, nil, fmt.Errorf("missing url")
	}
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, nil, fmt.Errorf("invalid streamable_http url %q", endpoint)
	}
	if err := validateMCPServerEnvironment(cfg); err != nil {
		return nil, nil, err
	}
	headers := buildMCPHTTPHeaders(cfg)
	if envName := strings.TrimSpace(cfg.BearerTokenEnvVar); envName != "" {
		if token := strings.TrimSpace(os.Getenv(envName)); token != "" {
			headers["Authorization"] = "Bearer " + token
		}
	}
	client := &MCPClient{
		config: cfg,
		http: &mcpHTTPTransport{
			endpoint: endpoint,
			client: &http.Client{
				Timeout: 30 * time.Second,
			},
			headers: headers,
		},
		status: MCPServerStatus{
			Name:          cfg.Name,
			Transport:     mcpServerTransport(cfg),
			URL:           endpoint,
			EnvironmentID: effectiveMCPServerEnvironmentID(cfg),
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.initialize(ctx); err != nil {
		client.Close()
		return nil, nil, err
	}
	tools, err := client.listTools(ctx)
	if err != nil {
		client.Close()
		return nil, nil, err
	}
	warnings := []string{}
	tools, toolWarnings := filterValidMCPToolDescriptors(cfg.Name, tools)
	warnings = append(warnings, toolWarnings...)
	client.tools = tools
	client.status.ToolCount = len(tools)
	if resources, err := client.listResources(ctx); err == nil {
		client.resources = resources
		client.status.ResourceCount = len(resources)
	} else if !isOptionalMCPMethodError(err) {
		warnings = append(warnings, fmt.Sprintf("mcp server %s resources: %v", cfg.Name, err))
	}
	if prompts, err := client.listPrompts(ctx); err == nil {
		client.prompts = prompts
		client.status.PromptCount = len(prompts)
	} else if !isOptionalMCPMethodError(err) {
		warnings = append(warnings, fmt.Sprintf("mcp server %s prompts: %v", cfg.Name, err))
	}
	return client, warnings, nil
}

func buildMCPHTTPHeaders(cfg MCPServerConfig) map[string]string {
	headers := map[string]string{}
	for name, value := range cfg.HTTPHeaders {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if strings.TrimSpace(value) == "" {
			continue
		}
		headers[name] = value
	}
	for name, envName := range cfg.EnvHTTPHeaders {
		name = strings.TrimSpace(name)
		envName = strings.TrimSpace(envName)
		if name == "" || envName == "" {
			continue
		}
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			headers[name] = value
		}
	}
	return headers
}

func (m *MCPManager) Close() {
	if m == nil {
		return
	}
	for _, server := range m.servers {
		server.Close()
	}
	m.servers = nil
}

func (m *MCPManager) Tools() []Tool {
	if m == nil {
		return nil
	}
	var out []Tool
	for _, server := range m.servers {
		for _, tool := range server.tools {
			out = append(out, MCPTool{
				client:      server,
				namespaced:  namespacedMCPToolName(server.config.Name, tool.Name),
				remote:      tool,
				description: fmt.Sprintf("[MCP:%s] %s", server.config.Name, strings.TrimSpace(tool.Description)),
				workspace:   m.workspace,
			})
		}
		if len(server.resources) > 0 {
			out = append(out, MCPResourceTool{client: server, workspace: m.workspace})
		}
		if len(server.prompts) > 0 {
			out = append(out, MCPPromptTool{client: server, workspace: m.workspace})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Definition().Name < out[j].Definition().Name
	})
	return out
}

func (m *MCPManager) Status() []MCPServerStatus {
	if m == nil {
		return nil
	}
	return append([]MCPServerStatus(nil), m.status...)
}

func (m *MCPManager) ServerConfig(name string) (MCPServerConfig, bool) {
	if m == nil {
		return MCPServerConfig{}, false
	}
	for _, server := range m.servers {
		if strings.EqualFold(strings.TrimSpace(server.config.Name), strings.TrimSpace(name)) {
			return server.config, true
		}
	}
	return MCPServerConfig{}, false
}

func (m *MCPManager) Resources() []MCPResourceRef {
	if m == nil {
		return nil
	}
	var out []MCPResourceRef
	for _, server := range m.servers {
		for _, resource := range server.resources {
			out = append(out, MCPResourceRef{
				Server:   server.config.Name,
				Resource: resource,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server != out[j].Server {
			return out[i].Server < out[j].Server
		}
		left := out[i].Resource.URI
		if left == "" {
			left = out[i].Resource.Name
		}
		right := out[j].Resource.URI
		if right == "" {
			right = out[j].Resource.Name
		}
		return left < right
	})
	return out
}

func (m *MCPManager) Prompts() []MCPPromptRef {
	if m == nil {
		return nil
	}
	var out []MCPPromptRef
	for _, server := range m.servers {
		for _, prompt := range server.prompts {
			out = append(out, MCPPromptRef{
				Server: server.config.Name,
				Prompt: prompt,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server != out[j].Server {
			return out[i].Server < out[j].Server
		}
		return out[i].Prompt.Name < out[j].Prompt.Name
	})
	return out
}

func (m *MCPManager) ResourceCatalogPrompt() string {
	items := m.Resources()
	if len(items) == 0 {
		return ""
	}
	var lines []string
	for _, item := range items {
		label := item.Resource.URI
		if label == "" {
			label = item.Resource.Name
		}
		extra := ""
		if item.Resource.Name != "" && item.Resource.Name != label {
			extra = " (" + item.Resource.Name + ")"
		}
		if desc := strings.TrimSpace(item.Resource.Description); desc != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s%s - %s", item.Server, label, extra, desc))
		} else {
			lines = append(lines, fmt.Sprintf("- %s: %s%s", item.Server, label, extra))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *MCPManager) PromptCatalogPrompt() string {
	items := m.Prompts()
	if len(items) == 0 {
		return ""
	}
	var lines []string
	for _, item := range items {
		args := []string{}
		for _, arg := range item.Prompt.Arguments {
			label := arg.Name
			if arg.Required {
				label += "*"
			}
			args = append(args, label)
		}
		signature := item.Prompt.Name + "(" + strings.Join(args, ", ") + ")"
		if desc := strings.TrimSpace(item.Prompt.Description); desc != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s - %s", item.Server, signature, desc))
		} else {
			lines = append(lines, fmt.Sprintf("- %s: %s", item.Server, signature))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *MCPManager) HasWebResearchCapability() bool {
	return strings.TrimSpace(m.WebResearchCatalogPrompt()) != ""
}

func (m *MCPManager) IsWebResearchToolName(name string) bool {
	if m == nil {
		return false
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	for _, server := range m.servers {
		if serverDeclaresWebResearchCapability(server.config) {
			serverPromptTool := "mcp__prompt__" + sanitizeMCPName(server.config.Name)
			serverResourceTool := "mcp__resource__" + sanitizeMCPName(server.config.Name)
			if trimmed == serverPromptTool || trimmed == serverResourceTool {
				return true
			}
		}
		if trimmed == "mcp__prompt__"+sanitizeMCPName(server.config.Name) {
			for _, prompt := range server.prompts {
				if looksLikeWebResearchMCPText(prompt.Name, prompt.Description) {
					return true
				}
			}
		}
		if trimmed == "mcp__resource__"+sanitizeMCPName(server.config.Name) {
			for _, resource := range server.resources {
				if looksLikeWebResearchMCPText(resource.URI, resource.Name, resource.Description) {
					return true
				}
			}
		}
		for _, tool := range server.tools {
			if trimmed == namespacedMCPToolName(server.config.Name, tool.Name) &&
				(serverDeclaresWebResearchCapability(server.config) || looksLikeWebResearchMCPText(tool.Name, tool.Description)) {
				return true
			}
		}
	}
	return false
}

func (m *MCPManager) IsWebResearchToolCall(call ToolCall) bool {
	if m == nil {
		return false
	}
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return false
	}
	args := toolCallArgumentsMap(call)
	for _, server := range m.servers {
		serverPromptTool := "mcp__prompt__" + sanitizeMCPName(server.config.Name)
		if name == serverPromptTool {
			if serverDeclaresWebResearchCapability(server.config) {
				return true
			}
			promptName := strings.TrimSpace(stringValue(args, "name"))
			for _, prompt := range server.prompts {
				if promptName != "" && !strings.EqualFold(prompt.Name, promptName) {
					continue
				}
				if looksLikeWebResearchMCPText(prompt.Name, prompt.Description) {
					return true
				}
			}
		}
		serverResourceTool := "mcp__resource__" + sanitizeMCPName(server.config.Name)
		if name == serverResourceTool {
			if serverDeclaresWebResearchCapability(server.config) {
				return true
			}
			target := strings.TrimSpace(stringValue(args, "uri"))
			for _, resource := range server.resources {
				if target != "" && !strings.EqualFold(resource.URI, target) && !strings.EqualFold(resource.Name, target) {
					continue
				}
				if looksLikeWebResearchMCPText(resource.URI, resource.Name, resource.Description) {
					return true
				}
			}
		}
		for _, tool := range server.tools {
			if name != namespacedMCPToolName(server.config.Name, tool.Name) {
				continue
			}
			if serverDeclaresWebResearchCapability(server.config) || looksLikeWebResearchMCPText(tool.Name, tool.Description) {
				return true
			}
		}
	}
	return false
}

func (m *MCPManager) WebResearchCatalogPrompt() string {
	if m == nil {
		return ""
	}
	seen := map[string]struct{}{}
	lines := []string{}
	for _, server := range m.servers {
		serverName := server.config.Name
		for _, tool := range server.tools {
			if !serverDeclaresWebResearchCapability(server.config) && !looksLikeWebResearchMCPText(tool.Name, tool.Description) {
				continue
			}
			desc := strings.TrimSpace(tool.Description)
			if desc == "" {
				desc = "MCP web/research tool"
			}
			line := fmt.Sprintf("- tool %s: %s - %s", serverName, namespacedMCPToolName(serverName, tool.Name), desc)
			if _, ok := seen[line]; ok {
				continue
			}
			seen[line] = struct{}{}
			lines = append(lines, line)
		}
		for _, prompt := range server.prompts {
			if !serverDeclaresWebResearchCapability(server.config) && !looksLikeWebResearchMCPText(prompt.Name, prompt.Description) {
				continue
			}
			args := []string{}
			for _, arg := range prompt.Arguments {
				label := arg.Name
				if arg.Required {
					label += "*"
				}
				args = append(args, label)
			}
			signature := prompt.Name + "(" + strings.Join(args, ", ") + ")"
			desc := strings.TrimSpace(prompt.Description)
			if desc == "" {
				desc = "MCP web/research prompt"
			}
			line := fmt.Sprintf("- prompt %s: %s - %s", serverName, signature, desc)
			if _, ok := seen[line]; ok {
				continue
			}
			seen[line] = struct{}{}
			lines = append(lines, line)
		}
		for _, resource := range server.resources {
			if !serverDeclaresWebResearchCapability(server.config) && !looksLikeWebResearchMCPText(resource.URI, resource.Name, resource.Description) {
				continue
			}
			label := resource.URI
			if label == "" {
				label = resource.Name
			}
			extra := ""
			if resource.Name != "" && resource.Name != label {
				extra = " (" + resource.Name + ")"
			}
			desc := strings.TrimSpace(resource.Description)
			if desc == "" {
				desc = "MCP web/research resource"
			}
			line := fmt.Sprintf("- resource %s: %s%s - %s", serverName, label, extra, desc)
			if _, ok := seen[line]; ok {
				continue
			}
			seen[line] = struct{}{}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func serverDeclaresWebResearchCapability(cfg MCPServerConfig) bool {
	for _, capability := range cfg.Capabilities {
		switch strings.ToLower(strings.TrimSpace(capability)) {
		case "web", "web_research", "web_search", "web_fetch", "browser", "browse":
			return true
		}
	}
	return false
}

func namespacedMCPToolName(server, tool string) string {
	return "mcp__" + sanitizeMCPName(server) + "__" + sanitizeMCPName(tool)
}

func filterValidMCPToolDescriptors(serverName string, tools []MCPToolDescriptor) ([]MCPToolDescriptor, []string) {
	if len(tools) == 0 {
		return nil, nil
	}
	valid := make([]MCPToolDescriptor, 0, len(tools))
	warnings := []string{}
	for _, tool := range tools {
		namespaced := namespacedMCPToolName(serverName, tool.Name)
		description := mcpToolDescription(serverName, tool)
		def := snapshotToolDefinition(mcpToolDefinition(namespaced, tool, description))
		if !validToolDefinition(def) {
			warnings = append(warnings, fmt.Sprintf(
				"mcp server %s skipped tool %s: invalid tool definition",
				strings.TrimSpace(serverName),
				strings.TrimSpace(tool.Name),
			))
			continue
		}
		tool.InputSchema = def.InputSchema
		tool.OutputSchema = def.OutputSchema
		valid = append(valid, tool)
	}
	return valid, warnings
}

func mcpToolDescription(serverName string, tool MCPToolDescriptor) string {
	description := fmt.Sprintf("[MCP:%s] %s", serverName, strings.TrimSpace(tool.Description))
	description = strings.TrimSpace(description)
	if description == "" || description == fmt.Sprintf("[MCP:%s]", serverName) {
		description = fmt.Sprintf("[MCP:%s] %s", serverName, strings.TrimSpace(tool.Name))
	}
	return description
}

func mcpToolDefinition(namespaced string, remote MCPToolDescriptor, description string) ToolDefinition {
	schema := remote.InputSchema
	if len(schema) == 0 {
		schema = emptyObjectSchema()
	}
	return ToolDefinition{
		Name:         namespaced,
		Description:  strings.TrimSpace(description),
		InputSchema:  schema,
		OutputSchema: remote.OutputSchema,
	}
}

func looksLikeWebResearchMCPText(values ...string) bool {
	joined := strings.ToLower(strings.TrimSpace(strings.Join(values, " ")))
	if joined == "" {
		return false
	}
	if containsAny(joined,
		"web search", "search web", "internet search", "search internet",
		"browser", "browse", "browse url", "visit url", "open url",
		"fetch url", "read url", "crawl", "crawler", "scrape",
		"serp", "tavily", "exa", "firecrawl", "duckduckgo", "google search", "bing search",
		"online article", "news search", "docs search", "documentation search", "documentation lookup", "reference lookup",
		"http://", "https://",
	) {
		return true
	}
	return containsAny(joined,
		"web", "search", "browser", "browse", "fetch", "crawl", "url", "news", "article", "docs",
	) && !containsAny(joined, "workspace", "local file", "filesystem", "git")
}

func sanitizeMCPName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z':
			b.WriteRune(ch)
		case ch >= '0' && ch <= '9':
			b.WriteRune(ch)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func (c *MCPClient) captureStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		c.stderrMu.Lock()
		c.stderr = append(c.stderr, line)
		if len(c.stderr) > 20 {
			c.stderr = append([]string(nil), c.stderr[len(c.stderr)-20:]...)
		}
		c.stderrMu.Unlock()
	}
}

func (c *MCPClient) stderrSummary() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	if len(c.stderr) == 0 {
		return ""
	}
	return strings.Join(c.stderr, " | ")
}

func (c *MCPClient) initialize(ctx context.Context) error {
	_, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"clientInfo": map[string]any{
			"name":    "kernforge",
			"version": currentVersion(),
		},
	})
	if err != nil {
		return err
	}
	return c.notify("notifications/initialized", map[string]any{})
}

func (c *MCPClient) listTools(ctx context.Context) ([]MCPToolDescriptor, error) {
	var out []MCPToolDescriptor
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err := c.request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		rawTools, _ := result["tools"].([]any)
		for _, raw := range rawTools {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			tool := MCPToolDescriptor{
				Name:        stringValue(item, "name"),
				Description: stringValue(item, "description"),
				InputSchema: emptyObjectSchema(),
			}
			if schema, ok := item["inputSchema"].(map[string]any); ok && len(schema) > 0 {
				tool.InputSchema = schema
			} else if schema, ok := item["input_schema"].(map[string]any); ok && len(schema) > 0 {
				tool.InputSchema = schema
			}
			if schema, ok := item["outputSchema"].(map[string]any); ok && len(schema) > 0 {
				tool.OutputSchema = schema
			} else if schema, ok := item["output_schema"].(map[string]any); ok && len(schema) > 0 {
				tool.OutputSchema = schema
			}
			if annotations, ok := item["annotations"].(map[string]any); ok && len(annotations) > 0 {
				tool.Annotations = cloneStringAnyMap(annotations)
				if hint, ok := mcpReadOnlyHintFromAnnotations(annotations); ok {
					tool.ReadOnlyHint = &hint
				}
			}
			if strings.TrimSpace(tool.Name) != "" {
				out = append(out, tool)
			}
		}
		nextCursor := stringValue(result, "nextCursor")
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	return out, nil
}

func mcpReadOnlyHintFromAnnotations(annotations map[string]any) (bool, bool) {
	for _, key := range []string{"readOnlyHint", "read_only_hint", "readOnly", "read_only"} {
		raw, ok := annotations[key]
		if !ok {
			continue
		}
		if value, ok := raw.(bool); ok {
			return value, true
		}
	}
	return false, false
}

func (d MCPToolDescriptor) readOnlyHint() bool {
	return d.ReadOnlyHint != nil && *d.ReadOnlyHint
}

func (c *MCPClient) listResources(ctx context.Context) ([]MCPResourceDescriptor, error) {
	var out []MCPResourceDescriptor
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err := c.request(ctx, "resources/list", params)
		if err != nil {
			return nil, err
		}
		rawItems, _ := result["resources"].([]any)
		for _, raw := range rawItems {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			resource := MCPResourceDescriptor{
				URI:         stringValue(item, "uri"),
				Name:        stringValue(item, "name"),
				Description: stringValue(item, "description"),
				MIMEType:    firstNonEmptyString(item, "mimeType", "mime_type"),
			}
			if strings.TrimSpace(resource.URI) != "" || strings.TrimSpace(resource.Name) != "" {
				out = append(out, resource)
			}
		}
		nextCursor := firstNonEmptyString(result, "nextCursor", "next_cursor")
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	return out, nil
}

func (c *MCPClient) listPrompts(ctx context.Context) ([]MCPPromptDescriptor, error) {
	var out []MCPPromptDescriptor
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err := c.request(ctx, "prompts/list", params)
		if err != nil {
			return nil, err
		}
		rawItems, _ := result["prompts"].([]any)
		for _, raw := range rawItems {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			prompt := MCPPromptDescriptor{
				Name:        stringValue(item, "name"),
				Description: stringValue(item, "description"),
			}
			if rawArgs, ok := item["arguments"].([]any); ok {
				for _, rawArg := range rawArgs {
					argObj, ok := rawArg.(map[string]any)
					if !ok {
						continue
					}
					prompt.Arguments = append(prompt.Arguments, MCPPromptArgument{
						Name:        stringValue(argObj, "name"),
						Description: stringValue(argObj, "description"),
						Required:    boolValue(argObj, "required", false),
					})
				}
			}
			if strings.TrimSpace(prompt.Name) != "" {
				out = append(out, prompt)
			}
		}
		nextCursor := firstNonEmptyString(result, "nextCursor", "next_cursor")
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	return out, nil
}

func emptyObjectSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}
}

func (c *MCPClient) callTool(ctx context.Context, name string, args any) (string, error) {
	result, err := c.callToolDetailed(ctx, name, args)
	return result.DisplayText, err
}

func (c *MCPClient) callToolDetailed(ctx context.Context, name string, args any) (ToolExecutionResult, error) {
	started := time.Now()
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	mcpCallID := ""
	if requestMeta := mcpToolCallRequestMetaFromContext(ctx); len(requestMeta) > 0 {
		mcpCallID = newMCPBridgeCallID()
		params["_meta"] = withMCPBridgeCallIDMeta(requestMeta, mcpCallID)
	}
	result, err := c.request(ctx, "tools/call", params)
	wallTime := time.Since(started)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	text := formatMCPToolResult(result)
	meta := buildMCPToolResultMeta(c.config.Name, name, result)
	if mcpCallID != "" {
		meta["mcp_call_id"] = mcpCallID
	}
	contentItems := mcpToolContentItems(result)
	modelText, modelContentItems := mcpToolModelOutput(result, wallTime)
	if boolValue(result, "isError", false) {
		if text == "" {
			text = "remote tool reported an error"
		}
		meta["mcp_is_error"] = true
		return ToolExecutionResult{
			DisplayText:       text,
			ContentItems:      contentItems,
			ModelText:         modelText,
			ModelContentItems: modelContentItems,
			Meta:              meta,
		}, fmt.Errorf("mcp tool %s failed", name)
	}
	if text == "" {
		text = "(no output)"
	}
	return ToolExecutionResult{
		DisplayText:       text,
		ContentItems:      contentItems,
		ModelText:         modelText,
		ModelContentItems: modelContentItems,
		Meta:              meta,
	}, nil
}

func buildMCPToolResultMeta(server string, tool string, result map[string]any) map[string]any {
	meta := map[string]any{
		"mcp_server": strings.TrimSpace(server),
		"mcp_tool":   strings.TrimSpace(tool),
	}
	if value, ok := result["content"]; ok {
		meta["mcp_result_content"] = value
	}
	if value, ok := firstPresentMCPResultField(result, "structuredContent", "structured_content"); ok {
		meta["mcp_result_structured_content"] = value
	}
	if value, ok := firstPresentMCPResultField(result, "_meta", "meta"); ok {
		meta["_meta"] = value
		meta["mcp_has_meta"] = true
	}
	if isError, ok := result["isError"]; ok {
		meta["mcp_is_error"] = isError
	}
	return meta
}

func firstPresentMCPResultField(result map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := result[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func (c *MCPClient) readResource(ctx context.Context, query string) (string, string, error) {
	resource, ok := c.findResource(query)
	if !ok {
		return "", "", fmt.Errorf("resource not found: %s", query)
	}
	result, err := c.request(ctx, "resources/read", map[string]any{
		"uri": resource.URI,
	})
	if err != nil {
		return "", "", err
	}
	text := formatMCPResourceResult(result)
	if text == "" {
		text = "(no content)"
	}
	display := c.config.Name + ":" + resource.URI
	if strings.TrimSpace(resource.Name) != "" && resource.Name != resource.URI {
		display += " (" + resource.Name + ")"
	}
	return display, text, nil
}

func (c *MCPClient) getPrompt(ctx context.Context, name string, args map[string]any) (string, string, error) {
	prompt, ok := c.findPrompt(name)
	if !ok {
		return "", "", fmt.Errorf("prompt not found: %s", name)
	}
	result, err := c.request(ctx, "prompts/get", map[string]any{
		"name":      prompt.Name,
		"arguments": args,
	})
	if err != nil {
		return "", "", err
	}
	text := formatMCPPromptResult(result)
	if text == "" {
		text = "(no prompt output)"
	}
	return c.config.Name + ":" + prompt.Name, text, nil
}

func formatMCPToolResult(result map[string]any) string {
	var sections []string
	if structured, ok := firstPresentMCPResultField(result, "structuredContent", "structured_content"); ok && structured != nil {
		if data, err := json.Marshal(structured); err == nil {
			return string(data)
		}
		if data, err := json.MarshalIndent(structured, "", "  "); err == nil {
			return string(data)
		}
	}
	if content, ok := result["content"].([]any); ok {
		for _, raw := range content {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch strings.TrimSpace(stringValue(item, "type")) {
			case "text":
				if text := strings.TrimSpace(stringValue(item, "text")); text != "" {
					sections = append(sections, text)
				}
			default:
				if data, err := json.MarshalIndent(item, "", "  "); err == nil {
					sections = append(sections, string(data))
				}
			}
		}
	}
	if len(sections) == 0 && strings.TrimSpace(stringValue(result, "text")) != "" {
		sections = append(sections, strings.TrimSpace(stringValue(result, "text")))
	}
	return strings.Join(sections, "\n\n")
}

func mcpToolContentItems(result map[string]any) []ToolContentItem {
	if value, ok := firstPresentMCPResultField(result, "structuredContent", "structured_content"); ok && value != nil {
		return nil
	}
	content, ok := result["content"].([]any)
	if !ok {
		return nil
	}
	items := make([]ToolContentItem, 0, len(content))
	sawImage := false
	for _, raw := range content {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(stringValue(item, "type")) {
		case "text":
			items = append(items, ToolContentItem{
				Type: "input_text",
				Text: stringValue(item, "text"),
			})
		case "image":
			sawImage = true
			data := strings.TrimSpace(stringValue(item, "data"))
			mimeType := strings.TrimSpace(firstNonBlankString(stringValue(item, "mimeType"), stringValue(item, "mime_type")))
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			imageURL := data
			if !strings.HasPrefix(strings.ToLower(imageURL), "data:") {
				imageURL = "data:" + mimeType + ";base64," + data
			}
			detail := imageDetailHigh
			if meta, ok := item["_meta"].(map[string]any); ok {
				if rawDetail, ok := meta["codex/imageDetail"].(string); ok {
					if normalized, err := normalizeImageDetail(rawDetail); err == nil && normalized != "" {
						detail = normalized
					}
				}
			}
			items = append(items, ToolContentItem{
				Type:     "input_image",
				ImageURL: imageURL,
				Detail:   detail,
			})
		default:
			if data, err := json.Marshal(item); err == nil {
				items = append(items, ToolContentItem{
					Type: "input_text",
					Text: string(data),
				})
			}
		}
	}
	if !sawImage {
		return nil
	}
	return normalizeToolContentItems(items)
}

func mcpToolModelOutput(result map[string]any, wallTime time.Duration) (string, []ToolContentItem) {
	header := mcpToolWallTimeHeader(wallTime)
	contentItems := mcpToolContentItems(result)
	if len(contentItems) > 0 {
		items := make([]ToolContentItem, 0, len(contentItems)+1)
		items = append(items, ToolContentItem{
			Type: "input_text",
			Text: header,
		})
		items = append(items, contentItems...)
		return header, normalizeToolContentItems(items)
	}
	text := strings.TrimSpace(mcpToolSerializedModelText(result))
	if text == "" {
		return header, nil
	}
	return header + "\n" + text, nil
}

func mcpToolWallTimeHeader(wallTime time.Duration) string {
	return fmt.Sprintf("Wall time: %.4f seconds\nOutput:", wallTime.Seconds())
}

func mcpToolSerializedModelText(result map[string]any) string {
	if structured, ok := firstPresentMCPResultField(result, "structuredContent", "structured_content"); ok && structured != nil {
		if data, err := json.Marshal(structured); err == nil {
			return string(data)
		} else {
			return err.Error()
		}
	}
	if content, ok := result["content"].([]any); ok {
		if data, err := json.Marshal(content); err == nil {
			return string(data)
		} else {
			return err.Error()
		}
	}
	return strings.TrimSpace(stringValue(result, "text"))
}

func formatMCPResourceResult(result map[string]any) string {
	var sections []string
	if contents, ok := result["contents"].([]any); ok {
		for _, raw := range contents {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			label := firstNonEmptyString(item, "uri", "name")
			text := strings.TrimSpace(stringValue(item, "text"))
			if text != "" {
				sections = append(sections, joinNonEmpty("Resource: "+label, text))
				continue
			}
			if blob := strings.TrimSpace(stringValue(item, "blob")); blob != "" {
				mimeType := firstNonEmptyString(item, "mimeType", "mime_type")
				sections = append(sections, joinNonEmpty(
					"Resource: "+label,
					fmt.Sprintf("[binary content omitted; mime=%s, base64_chars=%d]", valueOrDefault(mimeType, "unknown"), len(blob)),
				))
			}
		}
	}
	if len(sections) == 0 {
		if data, err := json.MarshalIndent(result, "", "  "); err == nil {
			return string(data)
		}
	}
	return strings.Join(sections, "\n\n")
}

func formatMCPPromptResult(result map[string]any) string {
	var sections []string
	if desc := strings.TrimSpace(stringValue(result, "description")); desc != "" {
		sections = append(sections, desc)
	}
	if messages, ok := result["messages"].([]any); ok {
		for _, raw := range messages {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			role := strings.TrimSpace(stringValue(item, "role"))
			text := strings.TrimSpace(formatMCPPromptContent(item["content"]))
			if text == "" {
				continue
			}
			if role == "" {
				sections = append(sections, text)
			} else {
				sections = append(sections, strings.ToUpper(role)+":\n"+text)
			}
		}
	}
	if len(sections) == 0 {
		if data, err := json.MarshalIndent(result, "", "  "); err == nil {
			return string(data)
		}
	}
	return strings.Join(sections, "\n\n")
}

func formatMCPPromptContent(raw any) string {
	switch value := raw.(type) {
	case string:
		return value
	case []any:
		var parts []string
		for _, rawItem := range value {
			switch item := rawItem.(type) {
			case string:
				if strings.TrimSpace(item) != "" {
					parts = append(parts, item)
				}
			case map[string]any:
				kind := strings.TrimSpace(stringValue(item, "type"))
				switch kind {
				case "", "text":
					if text := strings.TrimSpace(stringValue(item, "text")); text != "" {
						parts = append(parts, text)
					}
				default:
					if data, err := json.MarshalIndent(item, "", "  "); err == nil {
						parts = append(parts, string(data))
					}
				}
			}
		}
		return strings.Join(parts, "\n\n")
	case map[string]any:
		if text := strings.TrimSpace(stringValue(value, "text")); text != "" {
			return text
		}
		if data, err := json.MarshalIndent(value, "", "  "); err == nil {
			return string(data)
		}
	}
	return ""
}

func (c *MCPClient) findResource(query string) (MCPResourceDescriptor, bool) {
	trimmed := strings.TrimSpace(query)
	for _, resource := range c.resources {
		if strings.EqualFold(resource.URI, trimmed) || strings.EqualFold(resource.Name, trimmed) {
			return resource, true
		}
	}
	return MCPResourceDescriptor{}, false
}

func (c *MCPClient) findPrompt(name string) (MCPPromptDescriptor, bool) {
	trimmed := strings.TrimSpace(name)
	for _, prompt := range c.prompts {
		if strings.EqualFold(prompt.Name, trimmed) {
			return prompt, true
		}
	}
	return MCPPromptDescriptor{}, false
}

func (c *MCPClient) request(ctx context.Context, method string, params any) (map[string]any, error) {
	if c.http != nil {
		return c.httpRequest(ctx, method, params)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	responseCh := make(chan mcpRPCResult, 1)
	id, err := c.registerPendingRequest(responseCh)
	if err != nil {
		return nil, err
	}
	c.ensureReadLoop()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	c.writeMu.Lock()
	err = writeRPCMessage(c.stdin, payload)
	c.writeMu.Unlock()
	if err != nil {
		c.unregisterPendingRequest(id)
		return nil, fmt.Errorf("%s: %w", method, err)
	}

	select {
	case out := <-responseCh:
		if out.err != nil {
			if stderr := c.stderrSummary(); stderr != "" {
				return nil, fmt.Errorf("%w (%s)", out.err, stderr)
			}
			return nil, out.err
		}
		return out.payload, nil
	case <-ctx.Done():
		c.unregisterPendingRequest(id)
		c.Close()
		return nil, ctx.Err()
	}
}

func (c *MCPClient) httpRequest(ctx context.Context, method string, params any) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	id := c.nextHTTPRequestID()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	msg, err := c.postHTTPRPC(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if rawErr, ok := msg["error"]; ok && rawErr != nil {
		return nil, fmt.Errorf("rpc error: %s", formatRPCError(rawErr))
	}
	if result, ok := msg["result"].(map[string]any); ok {
		return result, nil
	}
	return map[string]any{}, nil
}

func (c *MCPClient) nextHTTPRequestID() int64 {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	c.nextID++
	return c.nextID
}

func (c *MCPClient) postHTTPRPC(ctx context.Context, payload map[string]any) (map[string]any, error) {
	if c == nil || c.http == nil {
		return nil, fmt.Errorf("http transport is not configured")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.http.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream, application/json")
	req.Header.Set("Content-Type", "application/json")
	c.http.mu.Lock()
	for name, value := range c.http.headers {
		req.Header.Set(name, value)
	}
	if c.http.sessionID != "" {
		req.Header.Set("mcp-session-id", c.http.sessionID)
	}
	c.http.mu.Unlock()
	resp, err := c.http.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return map[string]any{}, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("streamable HTTP session expired with 404 Not Found")
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("streamable HTTP authentication required")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("streamable HTTP returned %s: %s", resp.Status, strings.TrimSpace(string(preview)))
	}
	if sessionID := strings.TrimSpace(resp.Header.Get("mcp-session-id")); sessionID != "" {
		c.http.mu.Lock()
		c.http.sessionID = sessionID
		c.http.mu.Unlock()
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	mediaType := strings.ToLower(contentType)
	if parsed, _, err := mime.ParseMediaType(contentType); err == nil {
		mediaType = strings.ToLower(parsed)
	}
	switch mediaType {
	case "", "application/json":
		var msg map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
			return nil, err
		}
		return msg, nil
	case "text/event-stream":
		return readMCPHTTPSSEMessage(resp.Body)
	default:
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("unexpected streamable HTTP content type %q: %s", contentType, strings.TrimSpace(string(preview)))
	}
}

func readMCPHTTPSSEMessage(r io.Reader) (map[string]any, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var dataLines []string
	flush := func() (map[string]any, bool, error) {
		if len(dataLines) == 0 {
			return nil, false, nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = nil
		if strings.TrimSpace(data) == "" {
			return nil, false, nil
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			return nil, true, err
		}
		return msg, true, nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			msg, ok, err := flush()
			if ok || err != nil {
				return msg, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	msg, ok, err := flush()
	if ok || err != nil {
		return msg, err
	}
	return nil, io.EOF
}

func (c *MCPClient) registerPendingRequest(ch chan mcpRPCResult) (int64, error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.readErr != nil {
		return 0, c.readErr
	}
	c.nextID++
	id := c.nextID
	if c.pending == nil {
		c.pending = map[int64]chan mcpRPCResult{}
	}
	c.pending[id] = ch
	return id, nil
}

func (c *MCPClient) unregisterPendingRequest(id int64) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.pending != nil {
		delete(c.pending, id)
	}
}

func (c *MCPClient) ensureReadLoop() {
	c.readLoopOnce.Do(func() {
		go c.readLoop()
	})
}

func (c *MCPClient) readLoop() {
	for {
		msg, err := readRPCMessage(c.stdout)
		if err != nil {
			c.failPendingRequests(err)
			return
		}
		if method, _ := msg["method"].(string); method != "" {
			continue
		}
		id, ok := rpcMessageID(msg["id"])
		if !ok {
			continue
		}
		result := mcpRPCResult{}
		if rawErr, ok := msg["error"]; ok && rawErr != nil {
			result.err = fmt.Errorf("rpc error: %s", formatRPCError(rawErr))
		} else if payload, ok := msg["result"].(map[string]any); ok {
			result.payload = payload
		} else {
			result.payload = map[string]any{}
		}
		c.deliverPendingResponse(id, result)
	}
}

func (c *MCPClient) deliverPendingResponse(id int64, result mcpRPCResult) {
	c.pendingMu.Lock()
	ch := c.pending[id]
	if ch != nil {
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
	if ch != nil {
		ch <- result
	}
}

func (c *MCPClient) failPendingRequests(err error) {
	c.pendingMu.Lock()
	c.readErr = err
	pending := c.pending
	c.pending = map[int64]chan mcpRPCResult{}
	c.pendingMu.Unlock()
	for _, ch := range pending {
		ch <- mcpRPCResult{err: err}
	}
}

func rpcMessageID(raw any) (int64, bool) {
	switch value := raw.(type) {
	case float64:
		return int64(value), true
	case int64:
		return value, true
	case int:
		return int64(value), true
	case json.Number:
		n, err := value.Int64()
		return n, err == nil
	default:
		return 0, false
	}
}

func (c *MCPClient) notify(method string, params any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	if c.http != nil {
		_, err := c.postHTTPRPC(context.Background(), payload)
		if err == io.EOF {
			return nil
		}
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeRPCMessage(c.stdin, payload)
}

func (m *MCPManager) ReadResource(ctx context.Context, target string) (string, string, error) {
	serverName, resourceQuery, ok := parseMCPQualifiedTarget(target)
	if !ok {
		return "", "", fmt.Errorf("usage: server:resource-uri-or-name")
	}
	client, ok := m.findServer(serverName)
	if !ok {
		return "", "", fmt.Errorf("unknown MCP server: %s", serverName)
	}
	return client.readResource(ctx, resourceQuery)
}

func (m *MCPManager) GetPrompt(ctx context.Context, target string, args map[string]any) (string, string, error) {
	serverName, promptName, ok := parseMCPQualifiedTarget(target)
	if !ok {
		return "", "", fmt.Errorf("usage: server:prompt-name")
	}
	client, ok := m.findServer(serverName)
	if !ok {
		return "", "", fmt.Errorf("unknown MCP server: %s", serverName)
	}
	return client.getPrompt(ctx, promptName, args)
}

func (m *MCPManager) ResolveMention(ctx context.Context, raw string) (string, string, bool) {
	if m == nil {
		return "", "", false
	}
	token := strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(token), "mcp:") {
		return "", "", false
	}
	display, text, err := m.ReadResource(ctx, strings.TrimPrefix(token, "mcp:"))
	if err != nil {
		return "", "", false
	}
	return "mcp:" + display, text, true
}

func (m *MCPManager) findServer(name string) (*MCPClient, bool) {
	if m == nil {
		return nil, false
	}
	for _, server := range m.servers {
		if strings.EqualFold(server.config.Name, strings.TrimSpace(name)) {
			return server, true
		}
	}
	return nil, false
}

func parseMCPQualifiedTarget(target string) (string, string, bool) {
	trimmed := strings.TrimSpace(target)
	if strings.HasPrefix(strings.ToLower(trimmed), "mcp:") {
		trimmed = strings.TrimSpace(trimmed[len("mcp:"):])
	}
	parts := strings.SplitN(trimmed, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	server := strings.TrimSpace(parts[0])
	name := strings.TrimSpace(parts[1])
	if server == "" || name == "" {
		return "", "", false
	}
	return server, name, true
}

func writeRPCMessage(w io.Writer, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	return nil
}

func readRPCMessage(r *bufio.Reader) (map[string]any, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, err
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	return msg, nil
}

type rpcFrameMode int

const (
	rpcFrameModeHeader rpcFrameMode = iota
	rpcFrameModeJSONLine
)

func writeRPCMessageFramed(w io.Writer, payload map[string]any, mode rpcFrameMode) error {
	if mode == rpcFrameModeJSONLine {
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := w.Write(append(body, '\n')); err != nil {
			return err
		}
		return nil
	}
	return writeRPCMessage(w, payload)
}

func readRPCMessageFramed(r *bufio.Reader) (map[string]any, rpcFrameMode, error) {
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF && strings.TrimSpace(line) == "" {
				return nil, rpcFrameModeHeader, io.EOF
			}
			if err != io.EOF {
				return nil, rpcFrameModeHeader, err
			}
		}

		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if trimmed == "" {
			if err == io.EOF {
				return nil, rpcFrameModeHeader, io.EOF
			}
			continue
		}
		if strings.HasPrefix(trimmed, "{") {
			var msg map[string]any
			if err := json.Unmarshal([]byte(trimmed), &msg); err != nil {
				return nil, rpcFrameModeJSONLine, err
			}
			return msg, rpcFrameModeJSONLine, nil
		}

		msg, err := readRPCHeaderMessageFromFirstLine(r, line)
		if err != nil {
			return nil, rpcFrameModeHeader, err
		}
		return msg, rpcFrameModeHeader, nil
	}
}

func readRPCHeaderMessageFromFirstLine(r *bufio.Reader, firstLine string) (map[string]any, error) {
	length := -1
	line := firstLine
	for {
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, err
			}
			length = n
		}
		next, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = next
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func formatRPCError(raw any) string {
	if obj, ok := raw.(map[string]any); ok {
		if msg := strings.TrimSpace(stringValue(obj, "message")); msg != "" {
			return msg
		}
		if data, err := json.Marshal(obj); err == nil {
			return string(data)
		}
	}
	return fmt.Sprint(raw)
}

func isOptionalMCPMethodError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "method not found") ||
		strings.Contains(lower, "unsupported method") ||
		strings.Contains(lower, "not supported")
}

func firstNonEmptyString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringValue(m, key)); value != "" {
			return value
		}
	}
	return ""
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (c *MCPClient) Close() error {
	if c == nil {
		return nil
	}
	if c.http != nil {
		c.closeHTTP()
		c.http = nil
	}
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	_ = c.stdin.Close()
	_ = c.cmd.Process.Kill()
	_, _ = c.cmd.Process.Wait()
	c.cmd = nil
	return nil
}

func (c *MCPClient) closeHTTP() {
	c.http.mu.Lock()
	sessionID := c.http.sessionID
	endpoint := c.http.endpoint
	headers := map[string]string{}
	for name, value := range c.http.headers {
		headers[name] = value
	}
	c.http.mu.Unlock()
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	req.Header.Set("mcp-session-id", sessionID)
	resp, err := c.http.client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func (t MCPTool) Definition() ToolDefinition {
	description := strings.TrimSpace(t.description)
	if description == "" {
		description = mcpToolDescription(t.client.config.Name, t.remote)
	}
	return mcpToolDefinition(t.namespaced, t.remote, description)
}

func (t MCPTool) ReadOnlyToolCall() bool {
	return t.remote.readOnlyHint()
}

func (t MCPTool) SupportsParallelToolCalls() bool {
	if t.client != nil && t.client.config.SupportsParallelToolCalls {
		return true
	}
	return t.remote.readOnlyHint()
}

func (t MCPTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t MCPTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	originalArgs := cloneStringAnyMap(args)
	verdict, err := t.workspace.Hook(ctx, HookPreToolUse, t.hookPayload(ctx, originalArgs))
	if err != nil {
		return ToolExecutionResult{}, err
	}
	if err := defaultToolUseHookVerdictError(verdict); err != nil {
		return ToolExecutionResult{}, err
	}
	if len(verdict.UpdatedInput) > 0 {
		args = cloneStringAnyMap(verdict.UpdatedInput)
		input = args
	}
	result, err := t.client.callToolDetailed(ctx, t.remote.Name, input)
	if result.Meta == nil {
		result.Meta = map[string]any{}
	}
	result.Meta["mcp_namespaced_tool"] = t.namespaced
	if len(verdict.UpdatedInput) > 0 {
		result.Meta["hook_rewritten"] = true
		result.Meta["original_input"] = originalArgs
	}
	if err == nil {
		result = t.runPostToolUseHook(ctx, args, result)
	}
	return result, err
}

func (t MCPTool) hookPayload(ctx context.Context, args map[string]any) HookPayload {
	payload := defaultToolUseHookPayload(ctx, t.workspace, t.namespaced, args)
	payload["tool_kind"] = "mcp"
	payload["mcp_server"] = strings.TrimSpace(t.client.config.Name)
	payload["mcp_tool"] = strings.TrimSpace(t.remote.Name)
	payload["mcp_namespaced_tool"] = strings.TrimSpace(t.namespaced)
	return payload
}

func (t MCPTool) runPostToolUseHook(ctx context.Context, args map[string]any, result ToolExecutionResult) ToolExecutionResult {
	payload := t.hookPayload(ctx, args)
	payload["tool_response"] = defaultToolUseHookResponse(result)
	verdict, err := t.workspace.Hook(ctx, HookPostToolUse, payload)
	if result.Meta == nil {
		result.Meta = map[string]any{}
	}
	if err != nil {
		if feedback, ok := postToolUseHookFeedbackFromError(err); ok {
			return applyPostToolUseHookFeedback(result, feedback)
		}
		result.Meta["post_tool_use_hook_error"] = err.Error()
		return result
	}
	if len(verdict.ContextAdds) > 0 {
		result.Meta["post_tool_use_context_adds"] = append([]string(nil), verdict.ContextAdds...)
	}
	if feedback, ok := postToolUseHookFeedbackFromVerdict(verdict); ok {
		return applyPostToolUseHookFeedback(result, feedback)
	}
	return result
}

func (t MCPResourceTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "mcp__resource__" + sanitizeMCPName(t.client.config.Name),
		Description: fmt.Sprintf("[MCP:%s] Read a listed MCP resource by uri or name.", t.client.config.Name),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"uri": map[string]any{"type": "string"},
			},
			"required": []string{"uri"},
		},
	}
}

func (t MCPResourceTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	_, text, err := t.client.readResource(ctx, stringValue(args, "uri"))
	return text, err
}

func (t MCPPromptTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "mcp__prompt__" + sanitizeMCPName(t.client.config.Name),
		Description: fmt.Sprintf("[MCP:%s] Resolve a listed MCP prompt by name and arguments.", t.client.config.Name),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
				"arguments": map[string]any{
					"type":                 "object",
					"additionalProperties": true,
				},
			},
			"required": []string{"name"},
		},
	}
}

func (t MCPPromptTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	promptArgs := map[string]any{}
	if raw, ok := args["arguments"].(map[string]any); ok {
		promptArgs = raw
	}
	_, text, err := t.client.getPrompt(ctx, stringValue(args, "name"), promptArgs)
	return text, err
}
