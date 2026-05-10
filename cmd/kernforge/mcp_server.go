package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const kernforgeMCPProtocolVersion = "2024-11-05"

type kernforgeMCPServer struct {
	rt              *runtimeState
	tools           map[string]kernforgeMCPTool
	prompts         map[string]kernforgeMCPPrompt
	workspaceSource string
}

type kernforgeMCPTool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     func(context.Context, map[string]any) (string, error)
}

type kernforgeMCPPrompt struct {
	Name        string
	Description string
	Arguments   []MCPPromptArgument
	Handler     func(map[string]any) string
}

type kernforgeMCPResource struct {
	URI         string
	Name        string
	Description string
	MIMEType    string
}

type kernforgeMCPResourceTemplate struct {
	URITemplate string
	Name        string
	Description string
	MIMEType    string
}

type mcpServerRunOptions struct {
	ConfigOverrides     mcpServerConfigOverrides
	LoadWorkspaceConfig bool
}

type mcpServerConfigOverrides struct {
	Provider       string
	Model          string
	BaseURL        string
	PermissionMode string
	ForceBypass    bool
}

func runKernforgeMCPServer(cwd string, cfg Config, resumeID string, in io.Reader, out io.Writer, options ...mcpServerRunOptions) error {
	runOptions := mcpServerRunOptions{}
	if len(options) > 0 {
		runOptions = options[0]
	}
	runtime := &kernforgeMCPServerRuntime{
		fallbackCWD:     cwd,
		fallbackConfig:  cfg,
		resumeID:        resumeID,
		options:         runOptions,
		workspaceSource: "fallback",
	}
	defer runtime.close()

	reader := bufio.NewReader(in)
	for {
		msg, frameMode, err := readRPCMessageFramed(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		workspace, source := mcpWorkspaceHintFromMessage(msg)
		server, err := runtime.ensureServer(workspace, source)
		if err != nil {
			id := msg["id"]
			if err := writeRPCMessageFramed(out, mcpServerError(id, -32000, err.Error(), nil), frameMode); err != nil {
				return err
			}
			continue
		}
		response, ok := server.handleMessage(context.Background(), msg)
		if !ok {
			continue
		}
		if err := writeRPCMessageFramed(out, response, frameMode); err != nil {
			return err
		}
	}
}

type kernforgeMCPServerRuntime struct {
	fallbackCWD     string
	fallbackConfig  Config
	resumeID        string
	options         mcpServerRunOptions
	server          *kernforgeMCPServer
	activeWorkspace string
	workspaceSource string
}

func (r *kernforgeMCPServerRuntime) ensureServer(workspace string, source string) (*kernforgeMCPServer, error) {
	root := strings.TrimSpace(workspace)
	if root == "" && r.server != nil {
		return r.server, nil
	}
	if root == "" {
		root = r.fallbackCWD
		source = "fallback"
	}
	resolved, err := resolveMCPWorkspacePath(root)
	if err != nil {
		return nil, err
	}
	if r.server != nil && samePath(r.activeWorkspace, resolved) {
		return r.server, nil
	}
	if r.server != nil && r.server.rt != nil {
		r.server.rt.closeExtensions()
	}
	cfg := r.configForWorkspace(resolved)
	r.options.ConfigOverrides.apply(&cfg)
	if r.options.LoadWorkspaceConfig {
		if _, err := autoPopulateVerificationToolPaths(resolved, &cfg, detectWindowsVerificationToolPath); err != nil {
			return nil, err
		}
	}
	if strings.EqualFold(cfg.Provider, "ollama") && strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = normalizeOllamaBaseURL("")
	}
	if isLocalOpenAICompatibleProvider(cfg.Provider) && strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = normalizeLocalOpenAICompatibleBaseURL(cfg.Provider, "")
	}
	rt, err := newRuntimeStateForMCPServer(resolved, cfg, r.resumeID, io.Discard)
	if err != nil {
		return nil, err
	}
	server := newKernforgeMCPServer(rt)
	server.workspaceSource = firstNonBlankString(source, "fallback")
	r.server = server
	r.activeWorkspace = resolved
	r.workspaceSource = server.workspaceSource
	return server, nil
}

func (r *kernforgeMCPServerRuntime) configForWorkspace(workspace string) Config {
	if r.options.LoadWorkspaceConfig && !samePath(workspace, r.fallbackCWD) {
		if cfg, err := LoadConfig(workspace); err == nil {
			return cfg
		}
	}
	return r.fallbackConfig
}

func (r *kernforgeMCPServerRuntime) close() {
	if r != nil && r.server != nil && r.server.rt != nil {
		r.server.rt.closeExtensions()
	}
}

func (o mcpServerConfigOverrides) apply(cfg *Config) {
	if cfg == nil {
		return
	}
	providerOverride := normalizeProviderName(o.Provider)
	baseURLOverride := strings.TrimSpace(o.BaseURL)
	currentProvider := normalizeProviderName(cfg.Provider)
	if strings.TrimSpace(o.Provider) != "" {
		cfg.Provider = providerOverride
		if baseURLOverride == "" && currentProvider != providerOverride {
			switch providerOverride {
			case "deepseek":
				cfg.BaseURL = normalizeDeepSeekBaseURL("")
			case "openai-codex":
				cfg.BaseURL = normalizeOpenAICodexBaseURL("")
			case "lmstudio", "vllm", "llama.cpp":
				cfg.BaseURL = normalizeLocalOpenAICompatibleBaseURL(providerOverride, "")
			case "codex-cli":
				cfg.BaseURL = ""
			}
		}
	}
	if strings.TrimSpace(o.Model) != "" {
		cfg.Model = strings.TrimSpace(o.Model)
	}
	if strings.TrimSpace(o.Provider) != "" || strings.TrimSpace(o.Model) != "" {
		applyDefaultReasoningEffortForProvider(cfg, cfg.Provider)
	}
	if baseURLOverride != "" {
		cfg.BaseURL = baseURLOverride
	}
	if strings.TrimSpace(o.PermissionMode) != "" {
		cfg.PermissionMode = strings.TrimSpace(o.PermissionMode)
	}
	if o.ForceBypass {
		cfg.PermissionMode = string(ModeBypass)
	}
}

func mcpWorkspaceHintFromMessage(msg map[string]any) (string, string) {
	method := strings.TrimSpace(stringValue(msg, "method"))
	params, ok := msg["params"].(map[string]any)
	if !ok {
		return "", ""
	}
	if strings.EqualFold(method, "initialize") {
		return mcpWorkspaceHintFromAny(params, "initialize.params")
	}
	if strings.EqualFold(method, "tools/call") {
		for _, key := range []string{"_meta", "meta", "clientContext", "context"} {
			if path, src := mcpWorkspaceHintFromAny(params[key], "tools/call.params."+key); path != "" {
				return path, src
			}
		}
		if path, src := mcpWorkspaceHintFromToolArguments(params["arguments"], "tools/call.params.arguments"); path != "" {
			return path, src
		}
		return "", ""
	}
	for _, key := range []string{"_meta", "meta", "clientContext", "context"} {
		if path, src := mcpWorkspaceHintFromAny(params[key], method+".params."+key); path != "" {
			return path, src
		}
	}
	return "", ""
}

func mcpWorkspaceHintFromAny(raw any, source string) (string, string) {
	switch value := raw.(type) {
	case map[string]any:
		for _, key := range []string{"rootUri", "rootURI", "workspaceUri", "workspaceURI", "repoUri", "repositoryUri", "projectUri", "uri"} {
			if path, src, ok := mcpWorkspaceCandidate(value[key], source+"."+key); ok {
				return path, src
			}
		}
		for _, key := range []string{"rootPath", "path", "cwd", "clientCwd", "workingDirectory", "currentWorkingDirectory", "workspace", "workspaceRoot", "projectRoot", "repoRoot", "repositoryRoot"} {
			if path, src, ok := mcpWorkspaceCandidate(value[key], source+"."+key); ok {
				return path, src
			}
		}
		for _, key := range []string{"workspaceFolders", "roots", "folders", "workspaces"} {
			if path, src := mcpWorkspaceHintFromAny(value[key], source+"."+key); path != "" {
				return path, src
			}
		}
		for _, key := range []string{"initializationOptions", "options"} {
			if path, src := mcpWorkspaceHintFromAny(value[key], source+"."+key); path != "" {
				return path, src
			}
		}
	case []any:
		for i, item := range value {
			if path, src := mcpWorkspaceHintFromAny(item, fmt.Sprintf("%s[%d]", source, i)); path != "" {
				return path, src
			}
		}
	case string:
		if path, src, ok := mcpWorkspaceCandidate(value, source); ok {
			return path, src
		}
	}
	return "", ""
}

func mcpWorkspaceHintFromToolArguments(raw any, source string) (string, string) {
	value, ok := raw.(map[string]any)
	if !ok {
		return "", ""
	}
	for _, key := range []string{"workspace", "workspaceRoot", "cwd", "clientCwd", "workingDirectory", "currentWorkingDirectory", "rootPath", "projectRoot", "repoRoot", "repositoryRoot"} {
		if path, src, ok := mcpWorkspaceCandidate(value[key], source+"."+key); ok {
			return path, src
		}
	}
	for _, key := range []string{"rootUri", "rootURI", "workspaceUri", "workspaceURI", "repoUri", "repositoryUri", "projectUri"} {
		if path, src, ok := mcpWorkspaceCandidate(value[key], source+"."+key); ok {
			return path, src
		}
	}
	for _, key := range []string{"_meta", "meta", "clientContext", "context"} {
		if path, src := mcpWorkspaceHintFromAny(value[key], source+"."+key); path != "" {
			return path, src
		}
	}
	return "", ""
}

func mcpWorkspaceCandidate(raw any, source string) (string, string, bool) {
	value, ok := raw.(string)
	if !ok {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	if _, err := resolveMCPWorkspacePath(value); err != nil {
		return "", "", false
	}
	return value, source, true
}

func resolveMCPWorkspacePath(value string) (string, error) {
	value = strings.Trim(strings.TrimSpace(value), `"'`)
	if value == "" {
		return "", fmt.Errorf("empty workspace path")
	}
	if strings.HasPrefix(strings.ToLower(value), "file:") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", err
		}
		if !strings.EqualFold(parsed.Scheme, "file") {
			return "", fmt.Errorf("unsupported workspace URI scheme: %s", parsed.Scheme)
		}
		unescaped, err := url.PathUnescape(parsed.Path)
		if err != nil {
			return "", err
		}
		unescaped = filepath.FromSlash(unescaped)
		if parsed.Host != "" {
			value = `\\` + parsed.Host + unescaped
		} else {
			value = unescaped
			if len(value) >= 3 && (value[0] == '\\' || value[0] == '/') && value[2] == ':' {
				value = value[1:]
			}
		}
	}
	value = expandHome(value)
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace is not a directory: %s", abs)
	}
	return filepath.Clean(abs), nil
}

func samePath(left string, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return left == right
	}
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil {
		left = leftAbs
	}
	if rightErr == nil {
		right = rightAbs
	}
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func newRuntimeStateForMCPServer(cwd string, cfg Config, resumeID string, writer io.Writer) (*runtimeState, error) {
	if writer == nil {
		writer = io.Discard
	}
	store := NewSessionStore(cfg.SessionDir)
	sess, err := loadOrCreateSession(store, resumeID, cwd, cfg)
	if err != nil {
		return nil, err
	}
	mem, err := LoadMemory(sessionBaseWorkingDir(sess), cfg.MemoryFiles)
	if err != nil {
		return nil, err
	}
	rt := &runtimeState{
		cfg:            cfg,
		reader:         bufio.NewReader(strings.NewReader("")),
		writer:         writer,
		ui:             NewUI(),
		store:          store,
		session:        sess,
		memory:         mem,
		longMem:        NewPersistentMemoryStore(),
		evidence:       NewEvidenceStore(),
		investigations: NewInvestigationStore(),
		simulations:    NewSimulationStore(),
		functionFuzz:   NewFunctionFuzzStore(),
		fuzzCampaigns:  NewFuzzCampaignStore(),
		sourceScan:     NewSourceScanStore(),
		hookOverrides:  NewHookOverrideStore(),
		checkpoints:    NewCheckpointManager(),
		autoCP:         &AutoCheckpointController{},
		verifyHistory:  NewVerificationHistoryStore(),
		modelRoutes:    defaultModelRouteScheduler(),
		interactive:    false,
	}
	rt.perms = NewPermissionManager(ModeBypass, nil)
	rt.backgroundJobs = NewBackgroundJobManager(filepath.Join(sessionBaseWorkingDir(sess), userConfigDirName, "jobs"), sess, store)
	rt.workspace = Workspace{
		BaseRoot:              sessionBaseWorkingDir(sess),
		Root:                  sess.WorkingDir,
		Shell:                 cfg.Shell,
		ShellTimeout:          configShellTimeout(cfg),
		ReadHintSpans:         configReadHintSpans(cfg),
		ReadCacheEntries:      configReadCacheEntries(cfg),
		VerificationToolPaths: buildVerificationToolPaths(cfg),
		Perms:                 rt.perms,
		PrepareEdit:           rt.prepareEdit,
		PrepareEditAtRoot:     rt.prepareEditAtRoot,
		ReviewEdit:            rt.reviewProposedEdit,
		ReportProgress:        func(string) {},
		CurrentSelection: func() *ViewerSelection {
			return rt.session.CurrentSelection()
		},
		PreviewEdit: func(EditPreview) (bool, error) {
			return false, fmt.Errorf("edit preview is unavailable in mcp server mode")
		},
		UpdatePlan: func(items []PlanItem) {
			rt.session.SetSharedPlan(items)
			_ = rt.store.Save(rt.session)
		},
		GetPlan: func() []PlanItem {
			return append([]PlanItem(nil), rt.session.Plan...)
		},
		RunHook:        rt.runHook,
		BackgroundJobs: rt.backgroundJobs,
	}
	rt.syncWorkspaceFromSession()
	rt.applyMCPProviderDefaultsFromSession()
	client, clientErr := newProviderClientIfConfigured(rt.cfg)
	rt.clientErr = clientErr
	rt.agent = &Agent{
		Config:        rt.cfg,
		Client:        client,
		ModelRoutes:   rt.modelRoutes,
		Tools:         buildRegistry(rt.workspace, nil),
		Workspace:     rt.workspace,
		Session:       rt.session,
		Store:         rt.store,
		Memory:        rt.memory,
		LongMem:       rt.longMem,
		Evidence:      rt.evidence,
		VerifyHistory: rt.verifyHistory,
		FunctionFuzz:  rt.functionFuzz,
		FuzzCampaigns: rt.fuzzCampaigns,
	}
	rt.reloadHooks()
	return rt, nil
}

func (rt *runtimeState) applyMCPProviderDefaultsFromSession() {
	if rt == nil || rt.session == nil {
		return
	}
	if strings.TrimSpace(rt.cfg.Provider) == "" && strings.TrimSpace(rt.session.Provider) != "" {
		rt.cfg.Provider = rt.session.Provider
	}
	if strings.TrimSpace(rt.cfg.Model) == "" && strings.TrimSpace(rt.session.Model) != "" {
		rt.cfg.Model = rt.session.Model
	}
	if strings.TrimSpace(rt.cfg.BaseURL) == "" && strings.TrimSpace(rt.session.BaseURL) != "" {
		rt.cfg.BaseURL = rt.session.BaseURL
	}
	if strings.TrimSpace(rt.cfg.Provider) != "" && strings.TrimSpace(rt.cfg.Model) != "" {
		rt.session.Provider = rt.cfg.Provider
		rt.session.Model = rt.cfg.Model
		rt.session.BaseURL = rt.cfg.BaseURL
		_ = rt.store.Save(rt.session)
	}
}

func newProviderClientIfConfigured(cfg Config) (ProviderClient, error) {
	if strings.TrimSpace(cfg.Provider) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil, nil
	}
	return NewProviderClient(cfg)
}

func newKernforgeMCPServer(rt *runtimeState) *kernforgeMCPServer {
	server := &kernforgeMCPServer{
		rt:      rt,
		tools:   map[string]kernforgeMCPTool{},
		prompts: map[string]kernforgeMCPPrompt{},
	}
	server.registerTools()
	server.registerPrompts()
	return server
}

func (s *kernforgeMCPServer) registerTools() {
	s.addTool("kernforge", "DO NOT run shell, rg, git status, or Get-Content before this tool for any user request that starts with 'KernForge' or asks to use KernForge. Default KernForge router. Use this immediately. For review requests, it routes to kernforge_review so KernForge's common review harness reviews the current diff, provided code, plan, selection, PR, or analysis target. It never executes native commands; for vague requests it asks the user to choose and the assistant must stop.", mcpObjectSchema(map[string]any{
		"request":               map[string]any{"type": "string", "description": "The user's plain-language KernForge request, e.g. KernForge로 IsValidCommand 봐줘."},
		"target":                map[string]any{"type": "string", "description": "Optional function, symbol, file, subsystem, or target."},
		"file":                  map[string]any{"type": "string", "description": "Optional source file related to the request."},
		"choice":                map[string]any{"type": "string", "enum": []string{"", "preview", "plan", "build_only", "verify"}, "description": "Optional user-selected choice returned by a previous kernforge/kernforge_look response."},
		"diff":                  map[string]any{"type": "string", "description": "Optional unified diff for review requests. Prefer kernforge_review directly when possible."},
		"code":                  map[string]any{"type": "string", "description": "Optional code excerpt for review requests. Prefer kernforge_review directly when possible."},
		"include_git_diff":      map[string]any{"type": "boolean", "description": "For code-review requests, whether to collect workspace git diff."},
		"include_file_contents": map[string]any{"type": "boolean", "description": "For code-review requests, whether to include file excerpts for paths."},
		"max_context_chars":     map[string]any{"type": "integer", "description": "For code-review requests, maximum context characters sent to the main model."},
		"max_chars":             map[string]any{"type": "integer", "description": "Maximum response text characters. Source fuzz results enforce a larger minimum so code, trigger values, and artifact paths stay visible."},
	}), s.toolKernforge)
	s.addTool("kernforge_review", "Use this when the user asks Codex or another MCP client to have KernForge review code, a plan, a selection, a PR, or an analysis report. This calls KernForge's common review harness, returns structured gate/findings/status, and is read-only. Do not run shell or local file reads before this tool when the user explicitly says to review with KernForge.", mcpObjectSchema(map[string]any{
		"request":               map[string]any{"type": "string", "description": "The user's review request or acceptance criteria."},
		"target":                map[string]any{"type": "string", "enum": []string{"", "auto", "change", "plan", "selection", "pr", "analysis"}, "description": "Optional review target. Default auto lets KernForge infer the flow."},
		"mode":                  map[string]any{"type": "string", "description": "Optional review mode such as security-hardening, core-build, live-fix, refactor, research, or general-change."},
		"paths":                 map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional changed files or directories to focus on. Pass this when the MCP client knows which files it edited."},
		"diff":                  map[string]any{"type": "string", "description": "Optional unified diff supplied by the MCP client. If omitted, KernForge collects git diff from the workspace."},
		"code":                  map[string]any{"type": "string", "description": "Optional code excerpt supplied by the MCP client."},
		"auto_review":           map[string]any{"type": "string", "enum": []string{"", "inherit", "on", "off"}, "description": "Whether this MCP review should run. Default inherit follows server config."},
		"include_git_diff":      map[string]any{"type": "boolean", "description": "Whether to collect workspace git diff. Default true only when diff/code is not provided."},
		"include_file_contents": map[string]any{"type": "boolean", "description": "Include file excerpts for paths. Default false unless no diff context is found."},
		"no_model":              map[string]any{"type": "boolean", "description": "Use deterministic reviewers only."},
		"auto_follow_up":        map[string]any{"type": "string", "enum": []string{"", "inherit", "off", "safe"}, "description": "Return safe next commands. MCP does not execute external writes."},
		"response_format":       map[string]any{"type": "string", "enum": []string{"", "summary", "json", "both"}, "description": "Preferred response format. Default both."},
		"max_context_chars":     map[string]any{"type": "integer", "description": "Maximum characters of diff/code context sent to the main model."},
		"max_chars":             map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}), s.toolReview)
	s.addTool("kernforge_fuzz", "PRE-CALL NARRATION: say exactly one short sentence such as 'KernForge source-level fuzzing을 실행하겠습니다.' Do NOT say 'KernForge 도구를 확인', '코드에서 실제 위치를 찾겠다', '정의와 호출부를 찾겠다', '테스트 구조를 보겠다', or '퍼징 대상만 좁게 건드리겠다'. DO NOT run shell, rg, git status, Get-Content, local file reads, status, analysis, or verify tools before this tool for fuzz/fuzzing/퍼징/퍼즈/하네스 requests. Direct source-level fuzzing entrypoint. Default mode is source: no compile, no native build, no native fuzz execution. Source responses always include artifact_paths with artifact_dir, report_path, plan_path, and harness_path. If fuzz_result.meaningful_result is true, show problem_code_location, problem_code, and trigger_values from the response. After a source response, summarize fuzz_result and naturally recommend the optional native path: native_preview, build_only, then runtime fuzzing. Do not call those follow-up tools unless the user explicitly asks.", mcpObjectSchema(map[string]any{
		"request":                 map[string]any{"type": "string", "description": "Plain-language fuzz request, e.g. KernForge로 IsValidCommand 퍼징해줘."},
		"target":                  map[string]any{"type": "string", "description": "Optional function, symbol, file, subsystem, or target."},
		"file":                    map[string]any{"type": "string", "description": "Optional source file related to the fuzz target."},
		"mode":                    map[string]any{"type": "string", "enum": []string{"", "source", "plan", "native_preview"}, "description": "How far to go. Default source produces a source-level fuzz plan only. native_preview reviews build/run command shape without executing."},
		"approve_recovered_build": map[string]any{"type": "boolean", "description": "Explicitly approve partial or heuristic recovered build settings for build_only or execute."},
		"max_chars":               map[string]any{"type": "integer", "description": "Maximum response text characters. Source fuzz results enforce a larger minimum so code, trigger values, and artifact paths stay visible."},
	}), s.toolFuzz)
	s.addTool("kernforge_guide", "Natural-language KernForge entrypoint. Use this first when the user request is underspecified; it asks for missing inputs and suggests the next safe MCP tool call. If the response has stop_after_response=true, the assistant must ask the user and stop.", mcpObjectSchema(map[string]any{
		"request":        map[string]any{"type": "string", "description": "Plain-language request, e.g. fuzz this IOCTL, verify the patch, analyze the project, or find root cause."},
		"target":         map[string]any{"type": "string", "description": "Optional function, file, subsystem, or symbol target."},
		"file":           map[string]any{"type": "string", "description": "Optional source file related to the request."},
		"symptom":        map[string]any{"type": "string", "description": "Optional failure symptom for root-cause analysis."},
		"goal":           map[string]any{"type": "string", "description": "Optional analysis or verification goal."},
		"execution_mode": map[string]any{"type": "string", "enum": []string{"ask", "preview", "plan", "build_only", "execute"}, "description": "How far KernForge should go. Default is ask/preview-safe."},
		"approve_recovered_build": map[string]any{
			"type":        "boolean",
			"description": "Explicitly approve partial or heuristic recovered build settings for build_only or execute recommendations.",
		},
		"max_chars": map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}), s.toolGuide)
	s.addTool("kernforge_look", "Mandatory safe first step for vague 'look at' or Korean '봐줘' requests about a function or file. Use before shell source reads or other KernForge tools. It never runs analysis, verification, build, or fuzz; if it returns stop_after_response=true, ask the user to choose and stop.", mcpObjectSchema(map[string]any{
		"request":   map[string]any{"type": "string", "description": "Plain-language request, e.g. KernForge로 IsValidCommand 봐줘."},
		"target":    map[string]any{"type": "string", "description": "Optional function, symbol, or subsystem target."},
		"file":      map[string]any{"type": "string", "description": "Optional source file related to the request."},
		"max_chars": map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}), s.toolLook)
	s.addTool("kernforge_status", "Show KernForge workspace, provider, latest analysis, verification, evidence, and git status.", emptyObjectSchema(), s.toolStatus)
	s.addTool("kernforge_latest_analysis", "Summarize the latest KernForge project analysis artifacts and optionally include one generated document.", mcpObjectSchema(map[string]any{
		"document":  map[string]any{"type": "string", "description": "Optional latest docs file name, such as INDEX.md or SECURITY_SURFACE.md."},
		"max_chars": map[string]any{"type": "integer", "description": "Maximum text characters to return."},
	}), s.toolLatestAnalysis)
	s.addTool("kernforge_read_analysis_doc", "Read a generated document from .kernforge/analysis/latest/docs.", mcpObjectSchema(map[string]any{
		"document":  map[string]any{"type": "string", "description": "Generated doc file name, such as INDEX.md, SECURITY_SURFACE.md, or FUZZ_TARGETS.md."},
		"max_chars": map[string]any{"type": "integer", "description": "Maximum text characters to return."},
	}, "document"), s.toolReadAnalysisDoc)
	s.addTool("kernforge_evidence_search", "Search KernForge evidence records for the current workspace.", mcpObjectSchema(map[string]any{
		"query": map[string]any{"type": "string", "description": "Evidence query, e.g. category:driver outcome:failed or tag:fuzz."},
		"limit": map[string]any{"type": "integer", "description": "Maximum records to return."},
	}), s.toolEvidenceSearch)
	s.addTool("kernforge_memory_search", "Search KernForge persistent memory for prior decisions, evidence-backed context, files, and verification outcomes.", mcpObjectSchema(map[string]any{
		"query":     map[string]any{"type": "string", "description": "Memory query. Supports filters such as importance:high trust:confirmed category:driver tag:fuzz."},
		"limit":     map[string]any{"type": "integer", "description": "Maximum memory records to return."},
		"dashboard": map[string]any{"type": "boolean", "description": "Return dashboard-style summary instead of individual search hits."},
	}), s.toolMemorySearch)
	s.addTool("kernforge_verification_history", "Summarize recent KernForge verification history and planner failure patterns.", mcpObjectSchema(map[string]any{
		"all":          map[string]any{"type": "boolean", "description": "Include all workspaces instead of only this workspace."},
		"tags":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional verification tags to filter."},
		"limit":        map[string]any{"type": "integer", "description": "Maximum recent verification reports to include."},
		"include_json": map[string]any{"type": "boolean", "description": "Append the dashboard payload as JSON."},
	}), s.toolVerificationHistory)
	s.addTool("kernforge_analysis_context", "Build a query-focused reusable context pack from the latest project analysis artifacts without running a model. Do not call this for vague 'look at/봐줘' requests until kernforge_look or kernforge_guide has returned a user-selected choice.", mcpObjectSchema(map[string]any{
		"query":        map[string]any{"type": "string", "description": "Architecture, security, impact, verification, build, or fuzz question to focus the context pack."},
		"max_chars":    map[string]any{"type": "integer", "description": "Maximum response text characters."},
		"include_json": map[string]any{"type": "boolean", "description": "Append structured answer-pack metadata as JSON."},
	}, "query"), s.toolAnalysisContext)
	s.addTool("kernforge_artifact_index", "List important KernForge artifact paths for analysis docs, fuzz runs, campaigns, verification, evidence, and memory.", mcpObjectSchema(map[string]any{
		"max_recent": map[string]any{"type": "integer", "description": "Maximum recent fuzz runs and campaigns to include."},
	}), s.toolArtifactIndex)
	s.addTool("kernforge_fuzz_targets", "List generated FUZZ_TARGETS.md target candidates from the latest project analysis.", mcpObjectSchema(map[string]any{
		"query":       map[string]any{"type": "string", "description": "Optional target, file, surface, or reason filter."},
		"limit":       map[string]any{"type": "integer", "description": "Maximum target candidates to return."},
		"include_doc": map[string]any{"type": "boolean", "description": "Append latest FUZZ_TARGETS.md text when true."},
		"max_chars":   map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}), s.toolFuzzTargets)
	s.addTool("kernforge_source_scan", "Run, list, show, or revalidate source-scan candidates with structured candidate metadata and fuzz handoff commands.", mcpObjectSchema(map[string]any{
		"action":     map[string]any{"type": "string", "enum": []string{"", "status", "run", "list", "show", "revalidate"}, "description": "Source-scan action. Default run."},
		"id":         map[string]any{"type": "string", "description": "Candidate id or latest for show/revalidate."},
		"limit":      map[string]any{"type": "integer", "description": "Maximum candidate records to return."},
		"only_slugs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Matcher slugs to include."},
		"skip_slugs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Matcher slugs to skip."},
		"files":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional source file scope."},
		"filter":     map[string]any{"type": "string", "description": "Optional target/file/symbol filter."},
		"verdict":    map[string]any{"type": "string", "description": "Optional manual verdict for revalidate."},
		"reason":     map[string]any{"type": "string", "description": "Optional manual verdict reason."},
		"max_chars":  map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}), s.toolSourceScan)
	s.addTool("kernforge_source_candidate_list", "List recent source candidates as structured JSON for MCP clients.", mcpObjectSchema(map[string]any{
		"limit":     map[string]any{"type": "integer", "description": "Maximum candidate records to return."},
		"max_chars": map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}), s.toolSourceCandidateList)
	s.addTool("kernforge_source_candidate_show", "Show one source candidate with evidence, staleness, confidence, and next fuzz command.", mcpObjectSchema(map[string]any{
		"id":        map[string]any{"type": "string", "description": "Candidate id or latest."},
		"max_chars": map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}), s.toolSourceCandidateShow)
	s.addTool("kernforge_fuzz_workflow", "Structured source-scan to fuzz-func workflow. Runs or reuses a source candidate, creates a function fuzz plan, and returns explicit next commands without starting native execution unless execute=true.", mcpObjectSchema(map[string]any{
		"query":                    map[string]any{"type": "string", "description": "Optional target query. Used when candidate_id is empty."},
		"candidate_id":             map[string]any{"type": "string", "description": "Optional source candidate id. If omitted, KernForge runs source-scan and picks the strongest candidate."},
		"source_scan":              map[string]any{"type": "string", "enum": []string{"", "focused", "full", "off"}, "description": "Fuzz-func source-scan mode after candidate selection."},
		"limit":                    map[string]any{"type": "integer", "description": "Source-scan candidate limit when candidate_id is omitted."},
		"execute":                  map[string]any{"type": "boolean", "description": "Start native background fuzz execution if the generated plan is eligible."},
		"approve_recovered_build":  map[string]any{"type": "boolean", "description": "Allow execution with partial or heuristic recovered build settings."},
		"include_function_details": map[string]any{"type": "boolean", "description": "Append function fuzz textual output."},
		"max_chars":                map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}), s.toolFuzzWorkflow)
	s.addTool("kernforge_fuzz_func", "Create a source-level function fuzz plan and optionally start native background fuzz execution when execute is true.", mcpObjectSchema(map[string]any{
		"query":                   map[string]any{"type": "string", "description": "Function fuzz target query, e.g. ValidateRequest --file src/guard.cpp or @src/driver.cpp."},
		"execute":                 map[string]any{"type": "boolean", "description": "Start native background fuzz execution when the generated plan is eligible."},
		"approve_recovered_build": map[string]any{"type": "boolean", "description": "Allow execution with partial or heuristic recovered build settings."},
		"source_scan":             map[string]any{"type": "string", "enum": []string{"", "off", "focused", "full"}, "description": "Source candidate context mode. Default focused reuses a matching candidate or runs a target-scoped source scan; full scans the whole indexed workspace; off disables it."},
		"max_chars":               map[string]any{"type": "integer", "description": "Maximum response text characters. Source-only output enforces a larger minimum so problem code, trigger values, and artifact paths stay visible."},
	}, "query"), s.toolFuzzFunc)
	s.addTool("kernforge_fuzz_func_preview", "Preview native function fuzz eligibility, build command, run command, missing settings, and artifacts without saving history or starting background fuzz execution.", mcpObjectSchema(map[string]any{
		"query":        map[string]any{"type": "string", "description": "Function fuzz target query, e.g. ValidateRequest --file src/guard.cpp or @src/driver.cpp."},
		"include_plan": map[string]any{"type": "boolean", "description": "Append the full source-level fuzz plan after the execution summary."},
		"max_chars":    map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}, "query"), s.toolFuzzFuncPreview)
	s.addTool("kernforge_fuzz_func_build", "Compile the generated function fuzz harness only. This never runs the fuzz executable; partial recovered builds require approve_recovered_build=true.", mcpObjectSchema(map[string]any{
		"query":                   map[string]any{"type": "string", "description": "Function fuzz target query, e.g. ValidateRequest --file src/guard.cpp or @src/driver.cpp."},
		"approve_recovered_build": map[string]any{"type": "boolean", "description": "Allow build-only compilation with partial or heuristic recovered build settings."},
		"timeout_sec":             map[string]any{"type": "integer", "description": "Build-only timeout in seconds. Default 120."},
		"max_chars":               map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}, "query"), s.toolFuzzFuncBuild)
	s.addTool("kernforge_fuzz_func_status", "Show recent function fuzz runs or one specific function fuzz run.", mcpObjectSchema(map[string]any{
		"id":        map[string]any{"type": "string", "description": "Function fuzz run id or latest."},
		"limit":     map[string]any{"type": "integer", "description": "Number of recent runs to list when id is omitted."},
		"max_chars": map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}), s.toolFuzzFuncStatus)
	s.addTool("kernforge_fuzz_artifacts", "Read generated source-level function fuzz artifacts for latest or a specific run. Use only when the user asks to see detailed artifacts, report, plan, harness, or artifact contents. This never builds or runs native fuzzing.", mcpObjectSchema(map[string]any{
		"id":        map[string]any{"type": "string", "description": "Function fuzz run id, id prefix, or latest. Default latest."},
		"artifact":  map[string]any{"type": "string", "enum": []string{"", "overview", "report", "plan", "harness", "all"}, "description": "Artifact to read. Default overview shows paths and detailed source-level summary; report reads report.md; plan reads plan.json; harness reads harness.cpp; all includes report, plan, and harness within max_chars."},
		"max_chars": map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}), s.toolFuzzArtifacts)
	s.addTool("kernforge_fuzz_campaign_status", "Show latest or recent fuzz campaign state, seed artifacts, native results, findings, and coverage gaps.", mcpObjectSchema(map[string]any{
		"id":        map[string]any{"type": "string", "description": "Fuzz campaign id, id prefix, or latest."},
		"limit":     map[string]any{"type": "integer", "description": "Number of recent campaigns to list when id is omitted."},
		"max_chars": map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}), s.toolFuzzCampaignStatus)
	s.addTool("kernforge_fuzz_campaign_run", "Plan or advance the active fuzz campaign by creating a campaign, attaching latest function fuzz run, promoting seeds, and capturing native results. Do not call after kernforge_fuzz source-only output unless the user explicitly asks to create or advance a campaign.", mcpObjectSchema(map[string]any{
		"execute":   map[string]any{"type": "boolean", "description": "Advance campaign state when true; return only the recommended plan when false."},
		"name":      map[string]any{"type": "string", "description": "Optional name when creating the first campaign."},
		"max_chars": map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}), s.toolFuzzCampaignRun)
	s.addTool("kernforge_verify", "Plan or execute KernForge adaptive verification for the current workspace. This may run build or test commands when execute is true.", mcpObjectSchema(map[string]any{
		"mode":    map[string]any{"type": "string", "enum": []string{"adaptive", "full"}, "description": "Verification mode."},
		"paths":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional changed paths to verify."},
		"execute": map[string]any{"type": "boolean", "description": "Run planned commands when true; only return the plan when false."},
	}), s.toolVerify)
	s.addTool("kernforge_analyze_project", "Run KernForge multi-agent project analysis and persist analysis/docs/dashboard artifacts.", mcpObjectSchema(map[string]any{
		"mode":             map[string]any{"type": "string", "enum": supportedProjectAnalysisModes, "description": "Analysis mode."},
		"goal":             map[string]any{"type": "string", "description": "Analysis goal. If empty, KernForge chooses a mode-specific default."},
		"paths":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional path scope."},
		"max_total_shards": map[string]any{"type": "integer", "description": "Optional cap for total analysis shards."},
		"max_chars":        map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}), s.toolAnalyzeProject)
	s.addTool("kernforge_find_root_cause", "Run KernForge symptom-driven root-cause analysis and persist analysis/audit artifacts.", mcpObjectSchema(map[string]any{
		"problem":               map[string]any{"type": "string", "description": "Concrete symptom, trigger, observed failure, and expected invariant."},
		"pattern_pack_paths":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional local root-cause pattern pack paths."},
		"max_total_shards":      map[string]any{"type": "integer", "description": "Optional cap for root-cause shards."},
		"max_refinement_shards": map[string]any{"type": "integer", "description": "Optional cap for refinement shards."},
		"max_chars":             map[string]any{"type": "integer", "description": "Maximum response text characters."},
	}, "problem"), s.toolFindRootCause)
}

func (s *kernforgeMCPServer) addTool(name string, description string, schema map[string]any, handler func(context.Context, map[string]any) (string, error)) {
	s.tools[name] = kernforgeMCPTool{
		Name:        name,
		Description: description,
		InputSchema: schema,
		Handler:     handler,
	}
}

func (s *kernforgeMCPServer) registerPrompts() {
	s.prompts["kernforge-security-review"] = kernforgeMCPPrompt{
		Name:        "kernforge-security-review",
		Description: "Guide Codex to use KernForge evidence, analysis docs, and verification for a security-sensitive review.",
		Arguments: []MCPPromptArgument{
			{Name: "focus", Description: "Review focus such as driver, telemetry, memory-scan, Unreal, or PR diff.", Required: false},
		},
		Handler: func(args map[string]any) string {
			focus := strings.TrimSpace(stringValue(args, "focus"))
			if focus == "" {
				focus = "the current workspace change"
			}
			return "Use KernForge as the security workbench for " + focus + ". First call kernforge_status, then inspect kernforge_latest_analysis and kernforge_evidence_search. For risky code paths, use kernforge_verify with execute=false to inspect the plan before running execute=true. Prioritize correctness, security boundary coverage, stability, and evidence-backed findings."
		},
	}
	s.prompts["kernforge-root-cause"] = kernforgeMCPPrompt{
		Name:        "kernforge-root-cause",
		Description: "Guide Codex to investigate a symptom through KernForge root-cause analysis.",
		Arguments: []MCPPromptArgument{
			{Name: "problem", Description: "Symptom with trigger, observed failure, and expected invariant.", Required: true},
		},
		Handler: func(args map[string]any) string {
			problem := strings.TrimSpace(stringValue(args, "problem"))
			return "Investigate this symptom with KernForge: " + problem + "\n\nCall kernforge_find_root_cause with the full problem statement, then use kernforge_latest_analysis or kernforge_read_analysis_doc to inspect the generated audit/docs. Treat pattern matches as priors and final claims as requiring current source evidence."
		},
	}
	s.prompts["kernforge-fuzz-plan"] = kernforgeMCPPrompt{
		Name:        "kernforge-fuzz-plan",
		Description: "Guide Codex to use KernForge fuzz targets, source-level fuzz planning, and campaign automation safely.",
		Arguments: []MCPPromptArgument{
			{Name: "focus", Description: "Target surface such as IOCTL, parser, telemetry decoder, Unreal RPC, or a source file.", Required: false},
		},
		Handler: func(args map[string]any) string {
			focus := strings.TrimSpace(stringValue(args, "focus"))
			if focus == "" {
				focus = "the highest-risk input-facing target"
			}
			return "Use KernForge to plan fuzzing for " + focus + ". If the user names a concrete target and asks for fuzzing, call kernforge_fuzz with mode=source first, summarize fuzz_result.meaningful_result, and stop. Do not preface the call with local code inspection, test discovery, or fix plans. If any meaningful result is present, show problem_code_location, problem_code, and trigger_values exactly as KernForge returned them. If the user has not named a concrete target or execution depth, call kernforge_guide first and follow its questions. Use kernforge_fuzz_func_preview only when the user explicitly asks for native eligibility, build_command, run_command, or missing_settings without any chance of starting background fuzzing. Use kernforge_fuzz_func_build for compile-only validation before any native fuzz run. Only use kernforge_fuzz_func execute=true when the native build/run plan is acceptable. Use kernforge_fuzz_campaign_run with execute=false before execute=true so campaign seed promotion and native result capture stay explicit."
		},
	}
	s.prompts["kernforge-verify-with-memory"] = kernforgeMCPPrompt{
		Name:        "kernforge-verify-with-memory",
		Description: "Guide Codex to combine verification history, persistent memory, evidence, and adaptive verification.",
		Arguments: []MCPPromptArgument{
			{Name: "focus", Description: "Changed area, failing check, driver, telemetry, Unreal, or fuzz gate to verify.", Required: false},
		},
		Handler: func(args map[string]any) string {
			focus := strings.TrimSpace(stringValue(args, "focus"))
			if focus == "" {
				focus = "the current workspace change"
			}
			return "Use KernForge to verify " + focus + ". First call kernforge_status, kernforge_verification_history, and kernforge_memory_search for relevant prior failures or confirmed decisions. Then call kernforge_evidence_search for current evidence. Use kernforge_verify with execute=false before execute=true, and summarize what past history changed in the verification plan."
		},
	}
}

func (s *kernforgeMCPServer) handleMessage(ctx context.Context, msg map[string]any) (map[string]any, bool) {
	id, hasID := msg["id"]
	method := strings.TrimSpace(stringValue(msg, "method"))
	if method == "" {
		if !hasID {
			return nil, false
		}
		return mcpServerError(id, -32600, "missing method", nil), true
	}
	if !hasID && strings.HasPrefix(method, "notifications/") {
		return nil, false
	}
	params, _ := msg["params"].(map[string]any)
	switch method {
	case "initialize":
		return mcpServerResult(id, map[string]any{
			"protocolVersion": kernforgeMCPProtocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{},
				"resources": map[string]any{
					"listChanged": false,
				},
				"prompts": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "kernforge",
				"version": currentVersion(),
			},
		}), true
	case "ping":
		return mcpServerResult(id, map[string]any{}), true
	case "tools/list":
		return mcpServerResult(id, map[string]any{"tools": s.toolDescriptors()}), true
	case "tools/call":
		return s.handleToolCall(ctx, id, params), true
	case "resources/list":
		return mcpServerResult(id, map[string]any{"resources": s.resourceDescriptors()}), true
	case "resources/templates/list":
		return mcpServerResult(id, map[string]any{"resourceTemplates": s.resourceTemplateDescriptors()}), true
	case "resources/read":
		return s.handleResourceRead(id, params), true
	case "prompts/list":
		return mcpServerResult(id, map[string]any{"prompts": s.promptDescriptors()}), true
	case "prompts/get":
		return s.handlePromptGet(id, params), true
	default:
		return mcpServerError(id, -32601, "unsupported method: "+method, nil), true
	}
}

func (s *kernforgeMCPServer) handleToolCall(ctx context.Context, id any, params map[string]any) map[string]any {
	name := strings.TrimSpace(stringValue(params, "name"))
	tool, ok := s.tools[name]
	if !ok {
		return mcpServerError(id, -32602, "unknown tool: "+name, nil)
	}
	args := mcpArgsObject(params["arguments"])
	text, err := tool.Handler(ctx, args)
	text = strings.TrimSpace(text)
	if text == "" && err == nil {
		text = "(no output)"
	}
	if err != nil {
		if text == "" {
			text = err.Error()
		} else {
			text += "\n\nError: " + err.Error()
		}
		return mcpServerResult(id, map[string]any{
			"isError": true,
			"content": []map[string]any{{
				"type": "text",
				"text": text,
			}},
		})
	}
	return mcpServerResult(id, map[string]any{
		"content": []map[string]any{{
			"type": "text",
			"text": text,
		}},
	})
}

func (s *kernforgeMCPServer) handleResourceRead(id any, params map[string]any) map[string]any {
	uri := strings.TrimSpace(stringValue(params, "uri"))
	text, mimeType, err := s.readResource(uri)
	if err != nil {
		return mcpServerError(id, -32602, err.Error(), nil)
	}
	if mimeType == "" {
		mimeType = "text/plain"
	}
	return mcpServerResult(id, map[string]any{
		"contents": []map[string]any{{
			"uri":      uri,
			"mimeType": mimeType,
			"text":     text,
		}},
	})
}

func (s *kernforgeMCPServer) handlePromptGet(id any, params map[string]any) map[string]any {
	name := strings.TrimSpace(stringValue(params, "name"))
	prompt, ok := s.prompts[name]
	if !ok {
		return mcpServerError(id, -32602, "unknown prompt: "+name, nil)
	}
	args := mcpArgsObject(params["arguments"])
	text := strings.TrimSpace(prompt.Handler(args))
	return mcpServerResult(id, map[string]any{
		"description": prompt.Description,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{{
				"type": "text",
				"text": text,
			}},
		}},
	})
}

func mcpServerResult(id any, result map[string]any) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}
}

func mcpServerError(id any, code int, message string, data any) map[string]any {
	errObj := map[string]any{
		"code":    code,
		"message": strings.TrimSpace(message),
	}
	if data != nil {
		errObj["data"] = data
	}
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   errObj,
	}
}

func (s *kernforgeMCPServer) toolDescriptors() []map[string]any {
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		tool := s.tools[name]
		schema := tool.InputSchema
		if len(schema) == 0 {
			schema = emptyObjectSchema()
		}
		out = append(out, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": schema,
		})
	}
	return out
}

func (s *kernforgeMCPServer) promptDescriptors() []map[string]any {
	names := make([]string, 0, len(s.prompts))
	for name := range s.prompts {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		prompt := s.prompts[name]
		args := make([]map[string]any, 0, len(prompt.Arguments))
		for _, arg := range prompt.Arguments {
			args = append(args, map[string]any{
				"name":        arg.Name,
				"description": arg.Description,
				"required":    arg.Required,
			})
		}
		out = append(out, map[string]any{
			"name":        prompt.Name,
			"description": prompt.Description,
			"arguments":   args,
		})
	}
	return out
}

func (s *kernforgeMCPServer) resourceDescriptors() []map[string]any {
	resources := s.resources()
	out := make([]map[string]any, 0, len(resources))
	for _, resource := range resources {
		out = append(out, map[string]any{
			"uri":         resource.URI,
			"name":        resource.Name,
			"description": resource.Description,
			"mimeType":    resource.MIMEType,
		})
	}
	return out
}

func (s *kernforgeMCPServer) resourceTemplateDescriptors() []map[string]any {
	templates := []kernforgeMCPResourceTemplate{
		{
			URITemplate: "kernforge://analysis/latest/docs/{name}",
			Name:        "analysis-doc-by-name",
			Description: "Read a generated latest analysis document such as INDEX.md, SECURITY_SURFACE.md, or FUZZ_TARGETS.md.",
			MIMEType:    "text/markdown",
		},
		{
			URITemplate: "kernforge://analysis/context/{query}",
			Name:        "analysis-context-by-query",
			Description: "Build a query-focused context pack from latest analysis artifacts.",
			MIMEType:    "text/plain",
		},
		{
			URITemplate: "kernforge://fuzz/function-runs/{id}",
			Name:        "function-fuzz-run-by-id",
			Description: "Read a function fuzz run by id or latest.",
			MIMEType:    "text/plain",
		},
		{
			URITemplate: "kernforge://fuzz/campaign/{id}",
			Name:        "fuzz-campaign-by-id",
			Description: "Read a fuzz campaign by id or latest.",
			MIMEType:    "text/plain",
		},
		{
			URITemplate: "kernforge://memory/search/{query}",
			Name:        "memory-search-by-query",
			Description: "Search persistent memory using query text and filters.",
			MIMEType:    "application/json",
		},
	}
	out := make([]map[string]any, 0, len(templates))
	for _, item := range templates {
		out = append(out, map[string]any{
			"uriTemplate": item.URITemplate,
			"name":        item.Name,
			"description": item.Description,
			"mimeType":    item.MIMEType,
		})
	}
	return out
}

func (s *kernforgeMCPServer) resources() []kernforgeMCPResource {
	resources := []kernforgeMCPResource{
		{URI: "kernforge://status", Name: "status", Description: "Current KernForge workspace and runtime status.", MIMEType: "text/plain"},
		{URI: "kernforge://analysis/latest", Name: "latest-analysis", Description: "Latest project analysis summary.", MIMEType: "text/plain"},
		{URI: "kernforge://analysis/context", Name: "analysis-context", Description: "General latest analysis context pack.", MIMEType: "text/plain"},
		{URI: "kernforge://analysis/latest/manifest", Name: "latest-analysis-manifest", Description: "Latest analysis docs manifest JSON.", MIMEType: "application/json"},
		{URI: "kernforge://analysis/latest/run", Name: "latest-analysis-run", Description: "Latest analysis run JSON.", MIMEType: "application/json"},
		{URI: "kernforge://evidence/recent", Name: "recent-evidence", Description: "Recent evidence records for the current workspace.", MIMEType: "application/json"},
		{URI: "kernforge://memory/recent", Name: "recent-memory", Description: "Recent persistent memory records for the current workspace.", MIMEType: "application/json"},
		{URI: "kernforge://verification/history", Name: "verification-history", Description: "Recent verification history dashboard.", MIMEType: "text/plain"},
		{URI: "kernforge://artifacts/index", Name: "artifact-index", Description: "Important KernForge artifact paths.", MIMEType: "application/json"},
		{URI: "kernforge://fuzz/targets", Name: "fuzz-targets", Description: "Latest generated fuzz target catalog.", MIMEType: "application/json"},
		{URI: "kernforge://fuzz/function-runs/recent", Name: "recent-function-fuzz-runs", Description: "Recent source-level function fuzz runs.", MIMEType: "application/json"},
		{URI: "kernforge://fuzz/campaign/latest", Name: "latest-fuzz-campaign", Description: "Latest fuzz campaign state.", MIMEType: "text/plain"},
	}
	manifest, ok := s.loadLatestManifest()
	if ok {
		for _, doc := range manifest.Documents {
			name := strings.TrimSpace(doc.Name)
			if name == "" {
				continue
			}
			resources = append(resources, kernforgeMCPResource{
				URI:         "kernforge://analysis/latest/docs/" + filepath.ToSlash(name),
				Name:        "analysis-doc-" + name,
				Description: firstNonBlankString(doc.Title, "Generated analysis document"),
				MIMEType:    "text/markdown",
			})
		}
	}
	if s.rt != nil && s.rt.functionFuzz != nil {
		if runs, err := s.rt.functionFuzz.ListRecent(workspaceSnapshotRoot(s.rt.workspace), 5); err == nil {
			for _, run := range runs {
				if strings.TrimSpace(run.ID) == "" {
					continue
				}
				resources = append(resources, kernforgeMCPResource{
					URI:         "kernforge://fuzz/function-runs/" + run.ID,
					Name:        "function-fuzz-run-" + run.ID,
					Description: "Function fuzz run for " + firstNonBlankString(run.TargetSymbolName, run.TargetQuery),
					MIMEType:    "text/plain",
				})
			}
		}
	}
	if s.rt != nil && s.rt.fuzzCampaigns != nil {
		if campaigns, err := s.rt.fuzzCampaigns.ListRecent(workspaceSnapshotRoot(s.rt.workspace), 5); err == nil {
			for _, campaign := range campaigns {
				if strings.TrimSpace(campaign.ID) == "" {
					continue
				}
				resources = append(resources, kernforgeMCPResource{
					URI:         "kernforge://fuzz/campaign/" + campaign.ID,
					Name:        "fuzz-campaign-" + campaign.ID,
					Description: "Fuzz campaign " + firstNonBlankString(campaign.Name, campaign.ID),
					MIMEType:    "text/plain",
				})
			}
		}
	}
	sort.SliceStable(resources, func(i int, j int) bool {
		return resources[i].URI < resources[j].URI
	})
	return resources
}

func (s *kernforgeMCPServer) readResource(uri string) (string, string, error) {
	switch {
	case uri == "kernforge://status":
		text, err := s.toolStatus(context.Background(), map[string]any{})
		return text, "text/plain", err
	case uri == "kernforge://analysis/latest":
		text, err := s.toolLatestAnalysis(context.Background(), map[string]any{})
		return text, "text/plain", err
	case uri == "kernforge://analysis/context":
		text, err := s.toolAnalysisContext(context.Background(), map[string]any{"query": "project architecture, security surface, verification, and fuzz targets"})
		return text, "text/plain", err
	case strings.HasPrefix(uri, "kernforge://analysis/context/"):
		query := mcpDecodeURIComponent(strings.TrimPrefix(uri, "kernforge://analysis/context/"))
		text, err := s.toolAnalysisContext(context.Background(), map[string]any{"query": query})
		return text, "text/plain", err
	case uri == "kernforge://analysis/latest/manifest":
		text, err := s.readLatestAnalysisArtifactJSON("docs_manifest.json")
		return text, "application/json", err
	case uri == "kernforge://analysis/latest/run":
		text, err := s.readLatestAnalysisArtifactJSON("run.json")
		return text, "application/json", err
	case uri == "kernforge://evidence/recent":
		text, err := s.toolEvidenceSearch(context.Background(), map[string]any{"limit": float64(8)})
		return text, "application/json", err
	case uri == "kernforge://memory/recent":
		text, err := s.toolMemorySearch(context.Background(), map[string]any{"limit": float64(8)})
		return text, "application/json", err
	case strings.HasPrefix(uri, "kernforge://memory/search/"):
		query := mcpDecodeURIComponent(strings.TrimPrefix(uri, "kernforge://memory/search/"))
		text, err := s.toolMemorySearch(context.Background(), map[string]any{"query": query, "limit": float64(8)})
		return text, "application/json", err
	case uri == "kernforge://verification/history":
		text, err := s.toolVerificationHistory(context.Background(), map[string]any{"limit": float64(8)})
		return text, "text/plain", err
	case uri == "kernforge://artifacts/index":
		text, err := s.toolArtifactIndex(context.Background(), map[string]any{})
		return text, "application/json", err
	case uri == "kernforge://fuzz/targets":
		text, err := s.toolFuzzTargets(context.Background(), map[string]any{"limit": float64(12)})
		return text, "application/json", err
	case uri == "kernforge://fuzz/function-runs/recent":
		text, err := s.toolFuzzFuncStatus(context.Background(), map[string]any{"limit": float64(8)})
		return text, "application/json", err
	case uri == "kernforge://fuzz/campaign/latest":
		text, err := s.toolFuzzCampaignStatus(context.Background(), map[string]any{"id": "latest"})
		return text, "text/plain", err
	case strings.HasPrefix(uri, "kernforge://fuzz/function-runs/"):
		id := strings.TrimPrefix(uri, "kernforge://fuzz/function-runs/")
		text, err := s.toolFuzzFuncStatus(context.Background(), map[string]any{"id": id})
		return text, "text/plain", err
	case strings.HasPrefix(uri, "kernforge://fuzz/campaign/"):
		id := strings.TrimPrefix(uri, "kernforge://fuzz/campaign/")
		text, err := s.toolFuzzCampaignStatus(context.Background(), map[string]any{"id": id})
		return text, "text/plain", err
	case strings.HasPrefix(uri, "kernforge://analysis/latest/docs/"):
		name := strings.TrimPrefix(uri, "kernforge://analysis/latest/docs/")
		text, err := s.readLatestAnalysisDoc(name, 60000)
		return text, "text/markdown", err
	default:
		return "", "", fmt.Errorf("resource not found: %s", uri)
	}
}

func (s *kernforgeMCPServer) toolGuide(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	request := strings.TrimSpace(stringValue(args, "request"))
	target := strings.TrimSpace(stringValue(args, "target"))
	file := strings.TrimSpace(stringValue(args, "file"))
	symptom := strings.TrimSpace(stringValue(args, "symptom"))
	goal := strings.TrimSpace(stringValue(args, "goal"))
	approveRecovered := boolValue(args, "approve_recovered_build", false)
	executionMode := strings.ToLower(strings.TrimSpace(stringValue(args, "execution_mode")))
	if executionMode == "" {
		executionMode = "ask"
	}
	intent := mcpGuideInferIntent(request, target, file, symptom, goal)
	payload := map[string]any{
		"state":          "ready",
		"intent":         intent,
		"workspace":      workspaceSnapshotRoot(s.rt.workspace),
		"execution_mode": executionMode,
		"safe_default":   "preview_or_plan_only",
		"notes": []string{
			"KernForge guide does not execute native commands.",
			"When required input is missing, answer the questions and call kernforge_guide again or call the recommended tool.",
		},
	}
	questions := []map[string]any{}
	recommended := map[string]any{}
	switch intent {
	case "status":
		recommended = mcpGuideToolCall("kernforge_status", map[string]any{})
	case "target_only":
		query := mcpGuideFuzzQuery(request, target, file)
		if query == "" {
			query = firstNonBlankString(target, file, request)
		}
		payload["state"] = "needs_input"
		payload["target"] = query
		payload["ask_user"] = "KernForge로 " + query + "에 대해 무엇을 할까요?"
		payload["stop_after_response"] = true
		payload["requires_user_choice"] = true
		payload["next_assistant_action"] = "Return ask_user and choices to the user, then wait for the user's answer."
		payload["codex_instruction"] = "STOP here. Ask the user the ask_user question and present choices only. Do not call shell commands, read local files, or call any other KernForge tool until the user chooses one."
		payload["do_not_call_before_user_choice"] = []string{
			"shell",
			"kernforge_status",
			"kernforge_analysis_context",
			"kernforge_analyze_project",
			"kernforge_verify",
			"kernforge_fuzz_targets",
			"kernforge_fuzz_func",
			"kernforge_fuzz_func_preview",
			"kernforge_fuzz_func_build",
		}
		payload["choices"] = mcpGuideTargetChoices(query, target, file, request)
	case "fuzz":
		query := mcpGuideFuzzQuery(request, target, file)
		if query == "" {
			payload["state"] = "needs_input"
			payload["ask_user"] = "어떤 함수나 파일을 fuzz 대상으로 볼까요?"
			payload["codex_instruction"] = "Ask the user for the target function or file, then call kernforge_guide again with target/file."
			questions = append(questions,
				mcpGuideQuestion("target", "Which function or file should KernForge fuzz?", "Examples: IsValidCommand, ParseASN --file TavernKernel/KnCert.cpp, or @TavernKernel/TavernKernelCore.cpp"),
				mcpGuideQuestion("execution_mode", "How far should KernForge go?", "Use preview for build/run command review, build_only for compile-only, or execute only after approval."),
			)
			recommended = mcpGuideToolCall("kernforge_fuzz_targets", map[string]any{"limit": 5, "include_doc": false})
		} else {
			switch executionMode {
			case "build_only":
				if approveRecovered {
					recommended = mcpGuideToolCall("kernforge_fuzz_func_build", map[string]any{
						"query":                   query,
						"approve_recovered_build": true,
					})
					payload["approved_recovered_build"] = true
				} else {
					recommended = mcpGuideToolCall("kernforge_fuzz_func_preview", map[string]any{
						"query":        query,
						"include_plan": false,
					})
					payload["state"] = "needs_approval"
					payload["ask_user"] = "preview의 build_argv와 missing_settings를 확인한 뒤, 복구된 build settings로 컴파일만 진행할까요?"
					payload["codex_instruction"] = "Show the recommended preview first. If the user approves compile-only with recovered settings, call after_approval_tool_call."
					payload["after_approval_tool_call"] = mcpGuideToolCall("kernforge_fuzz_func_build", map[string]any{
						"query":                   query,
						"approve_recovered_build": true,
					})
					questions = append(questions, mcpGuideQuestion("approve_recovered_build", "Preview build_argv and missing_settings first. If they look acceptable, should KernForge compile only with recovered build settings?", "Answer with approve_recovered_build=true. The build-only tool never runs the fuzz executable."))
				}
			case "execute":
				if approveRecovered {
					recommended = mcpGuideToolCall("kernforge_fuzz_func", map[string]any{
						"query":                   query,
						"execute":                 true,
						"approve_recovered_build": true,
					})
					payload["approved_recovered_build"] = true
				} else {
					recommended = mcpGuideToolCall("kernforge_fuzz_func_preview", map[string]any{
						"query":        query,
						"include_plan": false,
					})
					payload["state"] = "needs_approval"
					payload["ask_user"] = "preview의 build_argv, run_argv, missing_settings를 확인한 뒤, native fuzz 실행까지 허용할까요?"
					payload["codex_instruction"] = "Show the recommended preview first. Prefer build_only before execute when compile context is partial. Only call after_approval_tool_call if the user explicitly approves native fuzz."
					payload["after_approval_tool_call"] = mcpGuideToolCall("kernforge_fuzz_func", map[string]any{
						"query":                   query,
						"execute":                 true,
						"approve_recovered_build": true,
					})
					questions = append(questions, mcpGuideQuestion("approve_recovered_build", "Preview build_argv, run_argv, and missing_settings first. If they look acceptable, may KernForge start native fuzz execution?", "Default false. Prefer build_only before execute when compile context is partial."))
				}
			case "plan":
				recommended = mcpGuideToolCall("kernforge_fuzz", map[string]any{
					"request": request,
					"target":  target,
					"file":    file,
					"mode":    "source",
				})
				payload["safe_default"] = "source_only"
				payload["stop_after_recommended_tool"] = true
			default:
				recommended = mcpGuideToolCall("kernforge_fuzz", map[string]any{
					"request": request,
					"target":  target,
					"file":    file,
					"mode":    "source",
				})
				payload["safe_default"] = "source_only"
				payload["stop_after_recommended_tool"] = true
				payload["codex_instruction"] = "Call the recommended kernforge_fuzz source-only tool, summarize fuzz_result.meaningful_result, show problem_code_location, problem_code, and trigger_values when meaningful_result is true, and stop. Do not preface this with local source inspection, test discovery, fix plans, or modification plans. Do not call preview/build/execute, shell commands, read artifacts, create custom harnesses, or run ad hoc fuzz scripts unless the user explicitly asks."
			}
		}
	case "verify":
		recommended = mcpGuideToolCall("kernforge_verify", map[string]any{
			"mode":    "adaptive",
			"execute": executionMode == "execute",
		})
		if executionMode != "execute" {
			payload["state"] = "needs_input"
			questions = append(questions, mcpGuideQuestion("execution_mode", "Should KernForge only plan verification, or may it run build/test commands?", "Default is plan only. Use execution_mode=execute when command execution is allowed."))
		}
	case "review":
		recommended = mcpGuideToolCall("kernforge_review", map[string]any{
			"request": request,
			"paths":   mcpGuideVerificationPaths(file, request),
		})
		payload["safe_default"] = "common_review_harness_read_only"
		payload["codex_instruction"] = "Call kernforge_review when the user asks KernForge to review code, a plan, a PR, a selection, or an analysis target. Do not substitute analyze-project, verify, shell git diff, or local source reads before this review tool."
	case "analyze":
		analysisGoal := firstNonBlankString(goal, request)
		if strings.TrimSpace(analysisGoal) == "" {
			payload["state"] = "needs_input"
			questions = append(questions, mcpGuideQuestion("goal", "What should project analysis focus on?", "Examples: map architecture, security surface, fuzz target catalog, kernel driver boundary review."))
			recommended = mcpGuideToolCall("kernforge_latest_analysis", map[string]any{})
		} else {
			recommended = mcpGuideToolCall("kernforge_analyze_project", map[string]any{
				"goal": analysisGoal,
				"mode": "security",
			})
		}
	case "root_cause":
		rootCauseSymptom := firstNonBlankString(symptom, request)
		if strings.TrimSpace(rootCauseSymptom) == "" || len(strings.Fields(rootCauseSymptom)) < 4 {
			payload["state"] = "needs_input"
			questions = append(questions, mcpGuideQuestion("symptom", "What is the concrete symptom, trigger, observed failure, and expected invariant?", "Example: DeviceIoControl with command X returns success for short input; expected rejection before state change."))
		} else {
			recommended = mcpGuideToolCall("kernforge_find_root_cause", map[string]any{
				"problem": rootCauseSymptom,
			})
		}
	default:
		payload["state"] = "needs_input"
		questions = append(questions,
			mcpGuideQuestion("request", "What do you want KernForge to do?", "Choose one: status, analyze, verify, fuzz, build-only fuzz harness, or root-cause investigation."),
			mcpGuideQuestion("target", "What target, file, subsystem, or symptom should KernForge use?", "Examples: IsValidCommand, TavernKernel/TavernKernelCore.cpp, IOCTL dispatch, or a crash symptom."),
			mcpGuideQuestion("execution_mode", "May KernForge execute native commands?", "Default is preview/plan only. Use build_only for compile-only or execute for native runs."),
		)
		recommended = mcpGuideToolCall("kernforge_status", map[string]any{})
	}
	if len(questions) > 0 {
		payload["questions"] = questions
	}
	if len(recommended) > 0 {
		payload["recommended_tool_call"] = recommended
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return mcpLimitText("KernForge guided MCP\n\n```json\n"+string(data)+"\n```", mcpMaxChars(args, 24000)), nil
}

func (s *kernforgeMCPServer) toolKernforge(ctx context.Context, args map[string]any) (string, error) {
	choice := strings.ToLower(strings.TrimSpace(stringValue(args, "choice")))
	request := strings.TrimSpace(stringValue(args, "request"))
	target := strings.TrimSpace(stringValue(args, "target"))
	file := strings.TrimSpace(stringValue(args, "file"))
	if choice == "" {
		intent := mcpGuideInferIntent(request, target, file, "", "")
		if intent == "review" {
			reviewArgs := map[string]any{
				"request": request,
				"paths":   mcpGuideVerificationPaths(file, request),
			}
			for _, key := range []string{"diff", "code", "target", "mode", "include_git_diff", "include_file_contents", "no_model", "auto_follow_up", "max_context_chars", "max_chars"} {
				if value, ok := args[key]; ok {
					reviewArgs[key] = value
				}
			}
			return s.toolReview(ctx, reviewArgs)
		}
		if intent == "fuzz" {
			fuzzArgs := map[string]any{
				"request": request,
				"target":  target,
				"file":    file,
				"mode":    "source",
			}
			if value, ok := args["max_chars"]; ok {
				fuzzArgs["max_chars"] = value
			}
			if value, ok := args["include_artifacts"]; ok {
				fuzzArgs["include_artifacts"] = value
			}
			text, err := s.toolFuzz(ctx, fuzzArgs)
			if err != nil {
				return "", err
			}
			return strings.Replace(text, "KernForge source-level fuzz plan", "KernForge MCP router source-level fuzz result", 1), nil
		}
		text, err := s.toolLook(ctx, args)
		if err != nil {
			return "", err
		}
		return strings.Replace(text, "KernForge look MCP", "KernForge MCP router", 1), nil
	}
	if target == "" && file == "" {
		extracted := mcpGuideExtractBareTarget(request)
		if mcpGuideLooksLikePath(extracted) {
			file = extracted
		} else {
			target = extracted
		}
	}
	query := mcpGuideFuzzQuery(request, target, file)
	if query == "" {
		text, err := s.toolLook(ctx, args)
		if err != nil {
			return "", err
		}
		return strings.Replace(text, "KernForge look MCP", "KernForge MCP router", 1), nil
	}
	payload := map[string]any{
		"state":       "ready",
		"intent":      "target_choice",
		"target":      query,
		"user_choice": choice,
		"workspace":   workspaceSnapshotRoot(s.rt.workspace),
		"notes": []string{
			"KernForge router does not execute native commands by itself.",
			"Call the recommended tool only because the user selected this choice.",
		},
	}
	switch choice {
	case "preview":
		payload["recommended_tool_call"] = mcpGuideToolCall("kernforge_fuzz_func_preview", map[string]any{
			"query":        query,
			"include_plan": false,
		})
	case "plan":
		payload["recommended_tool_call"] = mcpGuideToolCall("kernforge_fuzz", map[string]any{
			"request": request,
			"target":  target,
			"file":    file,
			"mode":    "source",
		})
	case "build_only":
		payload["recommended_tool_call"] = mcpGuideToolCall("kernforge_guide", map[string]any{
			"request":        "fuzz " + query,
			"target":         target,
			"file":           file,
			"execution_mode": "build_only",
		})
	case "verify":
		payload["recommended_tool_call"] = mcpGuideToolCall("kernforge_verify", map[string]any{
			"mode":    "adaptive",
			"paths":   mcpGuideVerificationPaths(file, request),
			"execute": false,
		})
	default:
		payload["state"] = "needs_input"
		payload["ask_user"] = "선택지는 preview, plan, build_only, verify 중 하나여야 합니다. 무엇으로 진행할까요?"
		payload["stop_after_response"] = true
		payload["requires_user_choice"] = true
		payload["choices"] = mcpGuideTargetChoices(query, target, file, request)
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return mcpLimitText("KernForge MCP router\n\n```json\n"+string(data)+"\n```", mcpMaxChars(args, 24000)), nil
}

func (s *kernforgeMCPServer) toolFuzz(ctx context.Context, args map[string]any) (string, error) {
	request := strings.TrimSpace(stringValue(args, "request"))
	target := strings.TrimSpace(stringValue(args, "target"))
	file := strings.TrimSpace(stringValue(args, "file"))
	if target == "" && file == "" {
		extracted := mcpGuideExtractBareTarget(request)
		if mcpGuideLooksLikePath(extracted) {
			file = extracted
		} else {
			target = extracted
		}
	}
	query := mcpGuideFuzzQuery(request, target, file)
	if query == "" {
		payload := map[string]any{
			"state":                 "needs_input",
			"intent":                "fuzz",
			"ask_user":              "어떤 함수나 파일을 fuzz 대상으로 볼까요?",
			"stop_after_response":   true,
			"requires_user_choice":  true,
			"next_assistant_action": "Ask the user for the fuzz target and stop.",
			"examples": []string{
				"KernForge로 IsValidCommand 퍼징해줘",
				"KernForge로 ParseASN --file TavernKernel/KnCert.cpp 퍼징해줘",
			},
			"workspace": workspaceSnapshotRoot(s.rt.workspace),
		}
		data, _ := json.MarshalIndent(payload, "", "  ")
		return mcpLimitText("KernForge fuzz MCP\n\n```json\n"+string(data)+"\n```", mcpMaxChars(args, 24000)), nil
	}
	retryTarget := firstNonBlankString(target, mcpGuideQueryTargetOnly(query))
	mode := strings.ToLower(strings.TrimSpace(stringValue(args, "mode")))
	if mode == "" {
		mode = "source"
	}
	switch mode {
	case "source", "plan":
		callArgs := map[string]any{
			"query":                query,
			"execute":              false,
			"source_only_response": true,
			"artifact_request":     request,
		}
		if value, ok := args["include_artifacts"]; ok {
			callArgs["include_artifacts"] = value
		}
		if value, ok := args["max_chars"]; ok {
			callArgs["max_chars"] = value
		}
		text, err := s.toolFuzzFunc(ctx, callArgs)
		if err != nil && retryTarget != "" && file != "" && mcpFuzzShouldRetryWithoutFile(err) {
			callArgs["query"] = retryTarget
			retryText, retryErr := s.toolFuzzFunc(ctx, callArgs)
			if retryErr == nil {
				return "KernForge fuzz MCP\n\nFile hint did not match an indexed source file; retried by target symbol only.\n\n" + retryText, nil
			}
		}
		return text, err
	case "native_preview", "preview":
		callArgs := map[string]any{
			"query":        query,
			"include_plan": false,
		}
		if value, ok := args["max_chars"]; ok {
			callArgs["max_chars"] = value
		}
		text, err := s.toolFuzzFuncPreview(ctx, callArgs)
		if err != nil && retryTarget != "" && file != "" && mcpFuzzShouldRetryWithoutFile(err) {
			callArgs["query"] = retryTarget
			retryText, retryErr := s.toolFuzzFuncPreview(ctx, callArgs)
			if retryErr == nil {
				return "KernForge fuzz MCP\n\nFile hint did not match an indexed source file; retried by target symbol only.\n\n" + retryText, nil
			}
		}
		return text, err
	case "build_only", "execute":
		payload := map[string]any{
			"state":                 "needs_explicit_native_tool",
			"intent":                "fuzz",
			"target":                query,
			"requested_mode":        mode,
			"compile_required":      false,
			"native_execution":      false,
			"ask_user":              "이 요청은 source-level fuzzing으로 처리됩니다. 컴파일이나 native 실행이 필요하면 native_preview 또는 전용 build/execute tool을 명시적으로 선택하세요.",
			"next_assistant_action": "Explain that kernforge_fuzz is source-only by default. Do not start compile or native fuzz execution from this alias.",
			"recommended_tool_call": mcpGuideToolCall("kernforge_fuzz", map[string]any{
				"request": request,
				"target":  target,
				"file":    file,
				"mode":    "source",
			}),
			"native_preview_tool_call": mcpGuideToolCall("kernforge_fuzz", map[string]any{
				"request": request,
				"target":  target,
				"file":    file,
				"mode":    "native_preview",
			}),
			"workspace": workspaceSnapshotRoot(s.rt.workspace),
		}
		data, _ := json.MarshalIndent(payload, "", "  ")
		return mcpLimitText("KernForge fuzz MCP\n\n```json\n"+string(data)+"\n```", mcpMaxChars(args, 24000)), nil
	default:
		payload := map[string]any{
			"state":                 "needs_input",
			"intent":                "fuzz",
			"target":                query,
			"ask_user":              "mode는 source, plan, native_preview 중 하나여야 합니다. 무엇으로 진행할까요?",
			"stop_after_response":   true,
			"requires_user_choice":  true,
			"next_assistant_action": "Ask the user to choose a fuzz mode and stop.",
			"modes":                 []string{"source", "plan", "native_preview"},
			"workspace":             workspaceSnapshotRoot(s.rt.workspace),
		}
		data, _ := json.MarshalIndent(payload, "", "  ")
		return mcpLimitText("KernForge fuzz MCP\n\n```json\n"+string(data)+"\n```", mcpMaxChars(args, 24000)), nil
	}
}

func (s *kernforgeMCPServer) toolLook(ctx context.Context, args map[string]any) (string, error) {
	request := strings.TrimSpace(stringValue(args, "request"))
	target := strings.TrimSpace(stringValue(args, "target"))
	file := strings.TrimSpace(stringValue(args, "file"))
	if target == "" && file == "" {
		extracted := mcpGuideExtractBareTarget(request)
		if mcpGuideLooksLikePath(extracted) {
			file = extracted
		} else {
			target = extracted
		}
	}
	if request == "" {
		request = firstNonBlankString(target, file)
	}
	guideArgs := map[string]any{
		"request":        request,
		"target":         target,
		"file":           file,
		"execution_mode": "ask",
	}
	if value, ok := args["max_chars"]; ok {
		guideArgs["max_chars"] = value
	}
	text, err := s.toolGuide(ctx, guideArgs)
	if err != nil {
		return "", err
	}
	return strings.Replace(text, "KernForge guided MCP", "KernForge look MCP", 1), nil
}

func (s *kernforgeMCPServer) toolStatus(ctx context.Context, args map[string]any) (string, error) {
	_ = args
	root := s.workspaceRoot()
	changed, _ := gitChangedFiles(ctx, root)
	branch, branchErr := gitCurrentBranch(ctx, root)
	if branchErr != nil {
		branch = ""
	}
	evidenceStats, _ := s.rt.evidence.Stats()
	verificationCount := s.rt.verificationHistoryCount()
	latest := s.latestAnalysisSummary()
	functionFuzzRuns := 0
	if s.rt.functionFuzz != nil {
		count, _, err := s.rt.functionFuzz.Stats(root)
		if err == nil {
			functionFuzzRuns = count
		}
	}
	fuzzCampaigns := 0
	if s.rt.fuzzCampaigns != nil {
		items, err := s.rt.fuzzCampaigns.ListRecent(root, 1000)
		if err == nil {
			fuzzCampaigns = len(items)
		}
	}
	status := map[string]any{
		"version":              currentVersion(),
		"workspace":            root,
		"mcp_workspace_source": valueOrUnset(s.workspaceSource),
		"session_id":           s.rt.session.ID,
		"provider":             valueOrUnset(s.rt.cfg.Provider),
		"model":                valueOrUnset(s.rt.cfg.Model),
		"provider_ready":       s.rt.agent != nil && s.rt.agent.Client != nil,
		"provider_error":       "",
		"git_branch":           branch,
		"git_changed_files":    changed,
		"evidence_count":       evidenceStats.Count,
		"verification_reports": verificationCount,
		"function_fuzz_runs":   functionFuzzRuns,
		"fuzz_campaigns":       fuzzCampaigns,
		"latest_analysis":      latest,
	}
	if s.rt.clientErr != nil {
		status["provider_error"] = s.rt.clientErr.Error()
	}
	data, _ := json.MarshalIndent(status, "", "  ")
	return "KernForge MCP status\n\n```json\n" + string(data) + "\n```", nil
}

func (s *kernforgeMCPServer) toolLatestAnalysis(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	limit := mcpMaxChars(args, 24000)
	summary := s.latestAnalysisSummary()
	if len(summary) == 0 {
		return "No latest analysis artifacts found. Run kernforge_analyze_project first.", nil
	}
	data, _ := json.MarshalIndent(summary, "", "  ")
	var b strings.Builder
	b.WriteString("Latest KernForge analysis\n\n```json\n")
	b.Write(data)
	b.WriteString("\n```")
	if doc := strings.TrimSpace(stringValue(args, "document")); doc != "" {
		text, err := s.readLatestAnalysisDoc(doc, limit)
		if err != nil {
			return b.String(), err
		}
		b.WriteString("\n\n")
		b.WriteString(text)
	}
	return mcpLimitText(b.String(), limit), nil
}

func (s *kernforgeMCPServer) toolReadAnalysisDoc(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	document := strings.TrimSpace(stringValue(args, "document"))
	if document == "" {
		return "", fmt.Errorf("document is required")
	}
	return s.readLatestAnalysisDoc(document, mcpMaxChars(args, 30000))
}

func (s *kernforgeMCPServer) toolEvidenceSearch(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	query := strings.TrimSpace(stringValue(args, "query"))
	limit := intValue(args, "limit", 12)
	if limit <= 0 {
		limit = 12
	}
	if limit > 50 {
		limit = 50
	}
	var (
		records []EvidenceRecord
		err     error
	)
	if query == "" {
		records, err = s.rt.evidence.ListRecent(s.workspaceRoot(), limit)
	} else {
		records, err = s.rt.evidence.Search(query, s.workspaceRoot(), limit)
	}
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *kernforgeMCPServer) toolMemorySearch(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	if s.rt.longMem == nil {
		return "", fmt.Errorf("persistent memory store is not configured")
	}
	query := strings.TrimSpace(stringValue(args, "query"))
	limit := intValue(args, "limit", 8)
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}
	workspace := workspaceSnapshotRoot(s.rt.workspace)
	if boolValue(args, "dashboard", false) {
		summary, err := s.rt.longMem.Dashboard(workspace, query, limit)
		if err != nil {
			return "", err
		}
		return renderPersistentMemoryDashboard(summary), nil
	}
	if query == "" {
		records, err := s.rt.longMem.ListRecent(workspace, limit)
		if err != nil {
			return "", err
		}
		payload := map[string]any{
			"workspace": workspace,
			"count":     len(records),
			"records":   records,
		}
		data, _ := json.MarshalIndent(payload, "", "  ")
		return string(data), nil
	}
	hits, err := s.rt.longMem.SearchHits(query, workspace, "", limit)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"workspace": workspace,
		"query":     query,
		"count":     len(hits),
		"hits":      hits,
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return string(data), nil
}

func (s *kernforgeMCPServer) toolVerificationHistory(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	if s.rt.verifyHistory == nil {
		return "", fmt.Errorf("verification history store is not configured")
	}
	limit := intValue(args, "limit", 8)
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}
	tags := stringSliceValue(args, "tags")
	all := boolValue(args, "all", false)
	summary, err := s.rt.verifyHistory.Dashboard(workspaceSnapshotRoot(s.rt.workspace), all, tags, limit)
	if err != nil {
		return "", err
	}
	text := renderVerificationDashboard(summary)
	if boolValue(args, "include_json", false) {
		data, _ := json.MarshalIndent(summary, "", "  ")
		text += "\n\n```json\n" + string(data) + "\n```"
	}
	return text, nil
}

func (s *kernforgeMCPServer) toolAnalysisContext(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	query := strings.TrimSpace(stringValue(args, "query"))
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	if s.rt.agent == nil {
		return "", fmt.Errorf("agent is not configured")
	}
	artifacts, ok := s.rt.agent.loadLatestProjectAnalysisArtifacts()
	if !ok {
		return "No latest project analysis artifacts found. Run kernforge_analyze_project first.", nil
	}
	pack := buildProjectStructureAnswerPack(artifacts, query)
	text := renderProjectStructureAnswerPack(pack, mcpMaxChars(args, 30000))
	if strings.TrimSpace(text) == "" {
		text = renderRelevantProjectAnalysisContext(artifacts, query)
	}
	if strings.TrimSpace(text) == "" {
		text = "Latest analysis exists, but no focused context pack matched this query."
	}
	if boolValue(args, "include_json", false) {
		payload := map[string]any{
			"intent":                pack.Intent,
			"confidence":            pack.Confidence,
			"domain_hints":          pack.DomainHints,
			"source_anchors":        pack.SourceAnchors,
			"suggested_reads":       pack.SuggestedReads,
			"stale_markers":         pack.StaleMarkers,
			"current_source_needed": pack.CurrentSourceNeeded,
			"relevant_docs":         pack.RelevantDocs,
			"critical_anchor_count": len(pack.CriticalAnchors),
			"fuzz_target_count":     len(pack.FuzzTargets),
			"verification_count":    len(pack.VerificationEntries),
		}
		data, _ := json.MarshalIndent(payload, "", "  ")
		text += "\n\n```json\n" + string(data) + "\n```"
	}
	return mcpLimitText(text, mcpMaxChars(args, 30000)), nil
}

func (s *kernforgeMCPServer) toolArtifactIndex(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	maxRecent := intValue(args, "max_recent", 5)
	if maxRecent <= 0 {
		maxRecent = 5
	}
	if maxRecent > 25 {
		maxRecent = 25
	}
	root := workspaceSnapshotRoot(s.rt.workspace)
	analysisCfg := configProjectAnalysis(s.rt.cfg, s.rt.workspace.BaseRoot)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	stores := map[string]any{}
	if s.rt.evidence != nil {
		stores["evidence"] = s.rt.evidence.Path
	}
	if s.rt.longMem != nil {
		stores["persistent_memory"] = s.rt.longMem.Path
	}
	if s.rt.verifyHistory != nil {
		stores["verification_history"] = s.rt.verifyHistory.Path
	}
	if s.rt.functionFuzz != nil {
		stores["function_fuzz"] = s.rt.functionFuzz.Path
	}
	if s.rt.fuzzCampaigns != nil {
		stores["fuzz_campaigns"] = s.rt.fuzzCampaigns.Path
	}
	if s.rt.sourceScan != nil {
		stores["source_scan"] = s.rt.sourceScan.Path
	}
	payload := map[string]any{
		"workspace": root,
		"analysis": map[string]any{
			"latest_dir":    latestDir,
			"dashboard":     filepath.Join(latestDir, "dashboard.html"),
			"run_json":      filepath.Join(latestDir, "run.json"),
			"manifest_json": filepath.Join(latestDir, "docs_manifest.json"),
			"docs_dir":      filepath.Join(latestDir, "docs"),
		},
		"stores": stores,
	}
	if s.rt.functionFuzz != nil {
		runs, _ := s.rt.functionFuzz.ListRecent(root, maxRecent)
		items := make([]map[string]any, 0, len(runs))
		for _, run := range runs {
			items = append(items, map[string]any{
				"id":           run.ID,
				"target":       run.TargetSymbolName,
				"plan_path":    run.PlanPath,
				"report_path":  run.ReportPath,
				"harness_path": run.HarnessPath,
				"artifact_dir": run.ArtifactDir,
			})
		}
		payload["function_fuzz_runs"] = items
	}
	if s.rt.fuzzCampaigns != nil {
		campaigns, _ := s.rt.fuzzCampaigns.ListRecent(root, maxRecent)
		items := make([]map[string]any, 0, len(campaigns))
		for _, campaign := range campaigns {
			items = append(items, map[string]any{
				"id":            campaign.ID,
				"name":          campaign.Name,
				"manifest_path": campaign.ManifestPath,
				"artifact_dir":  campaign.ArtifactDir,
				"corpus_dir":    campaign.CorpusDir,
				"crash_dir":     campaign.CrashDir,
				"coverage_dir":  campaign.CoverageDir,
				"reports_dir":   campaign.ReportsDir,
			})
		}
		payload["fuzz_campaigns"] = items
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return string(data), nil
}

func (s *kernforgeMCPServer) toolFuzzTargets(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	manifest, ok := s.loadLatestManifest()
	if !ok || len(manifest.FuzzTargets) == 0 {
		return "No generated fuzz target catalog found. Run kernforge_analyze_project with mode=security or mode=surface, or call kernforge_fuzz_func directly on a known function or file.", nil
	}
	query := strings.ToLower(strings.TrimSpace(stringValue(args, "query")))
	limit := intValue(args, "limit", 12)
	if limit <= 0 {
		limit = 12
	}
	if limit > 50 {
		limit = 50
	}
	targets := make([]AnalysisFuzzTargetCatalogEntry, 0, len(manifest.FuzzTargets))
	for _, target := range manifest.FuzzTargets {
		if query != "" && !mcpFuzzTargetMatchesQuery(target, query) {
			continue
		}
		targets = append(targets, target)
		if len(targets) >= limit {
			break
		}
	}
	payload := map[string]any{
		"run_id":          manifest.RunID,
		"goal":            manifest.Goal,
		"mode":            manifest.Mode,
		"total_targets":   len(manifest.FuzzTargets),
		"matched_targets": len(targets),
		"targets":         targets,
		"docs_resource":   "kernforge://analysis/latest/docs/FUZZ_TARGETS.md",
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	var b strings.Builder
	b.WriteString("KernForge fuzz targets\n\n```json\n")
	b.Write(data)
	b.WriteString("\n```")
	if boolValue(args, "include_doc", false) {
		if doc, err := s.readLatestAnalysisDoc("FUZZ_TARGETS.md", mcpMaxChars(args, 40000)); err == nil {
			b.WriteString("\n\n")
			b.WriteString(doc)
		}
	}
	return mcpLimitText(b.String(), mcpMaxChars(args, 30000)), nil
}

func mcpFuzzTargetMatchesQuery(target AnalysisFuzzTargetCatalogEntry, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		target.Name,
		target.File,
		target.SymbolID,
		target.SourceAnchor,
		target.Signature,
		target.InputSurfaceKind,
		target.BuildContext,
		target.BuildContextLevel,
		target.HarnessReadiness,
		target.SuggestedCommand,
		target.Confidence,
		strings.Join(target.PriorityReasons, " "),
		strings.Join(target.ParameterStrategies, " "),
		strings.Join(target.CoverageFeedback, " "),
	}, " "))
	for _, part := range strings.Fields(query) {
		if !strings.Contains(haystack, part) {
			return false
		}
	}
	return true
}

func (s *kernforgeMCPServer) toolSourceScan(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	if s.rt == nil || s.rt.sourceScan == nil {
		return "", fmt.Errorf("source scan store is not configured")
	}
	action := strings.ToLower(strings.TrimSpace(stringValue(args, "action")))
	if action == "" {
		action = "run"
	}
	switch action {
	case "status":
		return s.renderMCPSourceScanStatus(args)
	case "run":
		return s.runMCPSourceScan(args)
	case "list":
		return s.toolSourceCandidateList(ctx, args)
	case "show":
		return s.toolSourceCandidateShow(ctx, args)
	case "revalidate":
		return s.revalidateMCPSourceCandidate(args)
	default:
		return "", fmt.Errorf("unsupported source scan action: %s", action)
	}
}

func (s *kernforgeMCPServer) renderMCPSourceScanStatus(args map[string]any) (string, error) {
	root := workspaceSnapshotRoot(s.rt.workspace)
	count, last, err := s.rt.sourceScan.Stats(root)
	if err != nil {
		return "", err
	}
	runs, err := s.rt.sourceScan.ListRuns(root, 3)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"workspace_root":  root,
		"candidate_count": count,
		"runs":            runs,
		"next_command":    "/source-scan run",
		"next_tool_call": mcpGuideToolCall("kernforge_source_scan", map[string]any{
			"action": "run",
			"limit":  50,
		}),
	}
	if !last.IsZero() {
		payload["last_updated"] = last.Format(time.RFC3339)
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return mcpLimitText("KernForge source scan status\n\n```json\n"+string(data)+"\n```", mcpMaxChars(args, 24000)), nil
}

func (s *kernforgeMCPServer) runMCPSourceScan(args map[string]any) (string, error) {
	root := workspaceSnapshotRoot(s.rt.workspace)
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("workspace root is not available")
	}
	options := SourceScanOptions{
		Limit:     intValue(args, "limit", 50),
		OnlySlugs: lowerStringSlice(stringSliceValue(args, "only_slugs")),
		SkipSlugs: lowerStringSlice(stringSliceValue(args, "skip_slugs")),
		Filter:    strings.TrimSpace(stringValue(args, "filter")),
		Files:     splitSourceScanFiles(strings.Join(stringSliceValue(args, "files"), ",")),
	}
	runID := "source-scan-" + time.Now().Format("20060102-150405")
	artifacts, notes, err := prepareFunctionFuzzArtifactsForPlanning(s.rt.cfg, root, "source scan")
	if err != nil {
		return "", err
	}
	if !hasSemanticIndexV2Data(artifacts.IndexV2) {
		return "", fmt.Errorf("source scan could not build a semantic index")
	}
	candidates := buildSourceScanCandidates(root, runID, artifacts.IndexV2, options)
	run := SourceScanRun{
		ID:        runID,
		Workspace: root,
		Goal:      "mcp source candidate scan for fuzz target discovery",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Options:   options,
		Notes:     notes,
	}
	if err := writeSourceScanArtifacts(root, &run, candidates); err != nil {
		return "", err
	}
	savedRun, savedCandidates, err := s.rt.sourceScan.UpsertRunWithCandidates(run, candidates)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"workspace_root":  root,
		"run":             savedRun,
		"candidates":      mcpSourceCandidatePayloads(savedCandidates, 12),
		"candidate_count": len(savedCandidates),
		"artifact_paths": map[string]any{
			"artifact_dir":  savedRun.ArtifactDir,
			"manifest_path": savedRun.ManifestPath,
			"report_path":   savedRun.ReportPath,
		},
	}
	if len(savedCandidates) > 0 {
		payload["strongest_candidate"] = mcpSourceCandidatePayload(savedCandidates[0])
		payload["next_command"] = "/fuzz-func --from-candidate " + savedCandidates[0].ID
		payload["next_tool_call"] = mcpGuideToolCall("kernforge_fuzz_func", map[string]any{
			"query": "--from-candidate " + savedCandidates[0].ID,
		})
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return mcpLimitText("KernForge source scan\n\n```json\n"+string(data)+"\n```", mcpMaxChars(args, 40000)), nil
}

func (s *kernforgeMCPServer) toolSourceCandidateList(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	if s.rt == nil || s.rt.sourceScan == nil {
		return "", fmt.Errorf("source scan store is not configured")
	}
	limit := intValue(args, "limit", 12)
	items, err := s.rt.sourceScan.ListCandidates(workspaceSnapshotRoot(s.rt.workspace), limit)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"workspace_root": workspaceSnapshotRoot(s.rt.workspace),
		"candidates":     mcpSourceCandidatePayloads(items, limit),
	}
	if len(items) > 0 {
		payload["strongest_candidate"] = mcpSourceCandidatePayload(items[0])
		payload["next_command"] = "/fuzz-func --from-candidate " + items[0].ID
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return mcpLimitText("KernForge source candidates\n\n```json\n"+string(data)+"\n```", mcpMaxChars(args, 30000)), nil
}

func (s *kernforgeMCPServer) toolSourceCandidateShow(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	if s.rt == nil || s.rt.sourceScan == nil {
		return "", fmt.Errorf("source scan store is not configured")
	}
	id := firstNonBlankString(stringValue(args, "id"), "latest")
	item, ok, err := s.getMCPSourceCandidate(id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("source candidate not found: %s", id)
	}
	payload := mcpSourceCandidatePayload(item)
	data, _ := json.MarshalIndent(payload, "", "  ")
	return mcpLimitText("KernForge source candidate\n\n```json\n"+string(data)+"\n```\n\n"+renderSourceCandidate(item), mcpMaxChars(args, 30000)), nil
}

func (s *kernforgeMCPServer) revalidateMCPSourceCandidate(args map[string]any) (string, error) {
	if s.rt == nil || s.rt.sourceScan == nil {
		return "", fmt.Errorf("source scan store is not configured")
	}
	id := firstNonBlankString(stringValue(args, "id"), "latest")
	item, ok, err := s.getMCPSourceCandidate(id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("source candidate not found: %s", id)
	}
	updated, verdict := s.rt.revalidateSourceCandidate(item, stringValue(args, "verdict"), stringValue(args, "reason"))
	if _, err := s.rt.sourceScan.UpsertCandidate(updated); err != nil {
		return "", err
	}
	payload := map[string]any{
		"candidate":    mcpSourceCandidatePayload(updated),
		"verdict":      verdict,
		"next_command": "/fuzz-func --from-candidate " + updated.ID,
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return mcpLimitText("KernForge source candidate revalidation\n\n```json\n"+string(data)+"\n```", mcpMaxChars(args, 30000)), nil
}

func (s *kernforgeMCPServer) toolFuzzWorkflow(ctx context.Context, args map[string]any) (string, error) {
	if s.rt == nil || s.rt.sourceScan == nil {
		return "", fmt.Errorf("source scan store is not configured")
	}
	candidateID := strings.TrimSpace(stringValue(args, "candidate_id"))
	var candidate SourceCandidateRecord
	var ok bool
	var err error
	scanPayload := map[string]any{}
	if candidateID != "" {
		candidate, ok, err = s.getMCPSourceCandidate(candidateID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("source candidate not found: %s", candidateID)
		}
	} else {
		scanText, err := s.runMCPSourceScan(map[string]any{
			"limit":     intValue(args, "limit", 50),
			"filter":    stringValue(args, "query"),
			"max_chars": float64(120000),
		})
		if err != nil {
			return "", err
		}
		scanPayload["scan_output"] = scanText
		runs, err := s.rt.sourceScan.ListRuns(workspaceSnapshotRoot(s.rt.workspace), 1)
		if err != nil {
			return "", err
		}
		if len(runs) == 0 || len(runs[0].CandidateIDs) == 0 {
			return "", fmt.Errorf("source scan produced no candidates")
		}
		candidate, ok, err = s.rt.sourceScan.GetCandidate(runs[0].CandidateIDs[0])
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("source scan candidate not found after scan: %s", runs[0].CandidateIDs[0])
		}
	}
	query := "--from-candidate " + candidate.ID
	if rawMode := strings.TrimSpace(stringValue(args, "source_scan")); rawMode != "" {
		query += " --source-scan " + rawMode
	}
	fuzzArgs := map[string]any{
		"query":                   query,
		"execute":                 boolValue(args, "execute", false),
		"approve_recovered_build": boolValue(args, "approve_recovered_build", false),
		"max_chars":               float64(mcpMaxChars(args, 40000)),
	}
	fuzzText, err := s.toolFuzzFunc(ctx, fuzzArgs)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"workspace_root": workspaceSnapshotRoot(s.rt.workspace),
		"candidate":      mcpSourceCandidatePayload(candidate),
		"fuzz_query":     query,
		"next_command":   "/fuzz-campaign run",
		"campaign_tool_call": mcpGuideToolCall("kernforge_fuzz_campaign_run", map[string]any{
			"execute": false,
		}),
	}
	for key, value := range scanPayload {
		payload[key] = value
	}
	if boolValue(args, "include_function_details", false) {
		payload["function_fuzz_output"] = fuzzText
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	text := "KernForge fuzz workflow\n\n```json\n" + string(data) + "\n```"
	if boolValue(args, "include_function_details", false) {
		text += "\n\nFunction fuzz output\n\n" + fuzzText
	}
	return mcpLimitText(text, mcpMaxChars(args, 60000)), nil
}

func mcpSourceCandidatePayloads(items []SourceCandidateRecord, limit int) []map[string]any {
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	out := []map[string]any{}
	for _, item := range items[:limit] {
		out = append(out, mcpSourceCandidatePayload(item))
	}
	return out
}

func (s *kernforgeMCPServer) getMCPSourceCandidate(id string) (SourceCandidateRecord, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.EqualFold(id, "latest") {
		items, err := s.rt.sourceScan.ListCandidates(workspaceSnapshotRoot(s.rt.workspace), 1)
		if err != nil || len(items) == 0 {
			return SourceCandidateRecord{}, false, err
		}
		return items[0], true, nil
	}
	return s.rt.sourceScan.GetCandidateForWorkspace(id, workspaceSnapshotRoot(s.rt.workspace))
}

func mcpSourceCandidatePayload(item SourceCandidateRecord) map[string]any {
	item = normalizeSourceCandidateRecord(item)
	payload := map[string]any{
		"candidate_id":          item.ID,
		"status":                item.Status,
		"matcher_slug":          item.MatcherSlug,
		"matcher_description":   item.MatcherDescription,
		"score":                 item.Score,
		"noise_tier":            item.NoiseTier,
		"severity_hint":         item.SeverityHint,
		"target":                firstNonBlankString(item.SymbolName, item.SymbolID, item.File),
		"file":                  item.File,
		"source_anchor":         item.SourceAnchor,
		"confidence_breakdown":  item.ConfidenceBreakdown,
		"evidence_spans":        item.EvidenceSpans,
		"negative_evidence":     item.NegativeEvidence,
		"dataflow_facts":        item.DataflowFacts,
		"controlflow_facts":     item.ControlflowFacts,
		"stale":                 item.Stale,
		"stale_reason":          item.StaleReason,
		"file_content_hash":     item.FileContentHash,
		"current_file_hash":     item.CurrentFileHash,
		"symbol_signature_hash": item.SymbolSignatureHash,
		"current_symbol_hash":   item.CurrentSymbolHash,
		"revalidation_history":  item.RevalidationHistory,
		"linked_fuzz_run_ids":   item.LinkedFuzzRunIDs,
		"linked_campaign_ids":   item.LinkedCampaignIDs,
		"feedback_draft_paths":  item.FeedbackDraftPaths,
		"next_command":          "/fuzz-func --from-candidate " + item.ID,
		"next_tool_call": mcpGuideToolCall("kernforge_fuzz_func", map[string]any{
			"query": "--from-candidate " + item.ID,
		}),
	}
	return payload
}

func (s *kernforgeMCPServer) toolFuzzFunc(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	if s.rt.functionFuzz == nil {
		return "", fmt.Errorf("function fuzz store is not configured")
	}
	query := strings.TrimSpace(stringValue(args, "query"))
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	root := workspaceSnapshotRoot(s.rt.workspace)
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("workspace root is not available")
	}
	query, sourceScanMode, err := parseFunctionFuzzSourceScanMode(query, functionFuzzSourceScanModeFocused)
	if err != nil {
		return "", err
	}
	if rawMode := strings.TrimSpace(stringValue(args, "source_scan")); rawMode != "" {
		parsed, err := parseFunctionFuzzSourceScanModeValue(rawMode)
		if err != nil {
			return "", err
		}
		sourceScanMode = parsed
	}
	resolvedQuery, sourceCandidate, fromCandidate, err := s.rt.resolveFunctionFuzzSourceCandidateQuery(query)
	if err != nil {
		return "", err
	}
	commandCfg := configWithResponseLanguageForUserText(s.rt.cfg, resolvedQuery)
	run, artifacts, err := buildFunctionFuzzRunWithArtifacts(commandCfg, root, resolvedQuery)
	if err != nil {
		return "", err
	}
	run, linkedCandidate, linkSourceCandidate, err := s.rt.attachFunctionFuzzSourceScanContext(root, run, artifacts, sourceCandidate, fromCandidate, sourceScanMode)
	if err != nil {
		return "", err
	}
	execute := boolValue(args, "execute", false)
	approveRecovered := boolValue(args, "approve_recovered_build", false)
	sourceOnlyResponse := boolValue(args, "source_only_response", false)
	artifactRequest := firstNonBlankString(stringValue(args, "artifact_request"), query)
	includeArtifacts := boolValue(args, "include_artifacts", false) && mcpFuzzRequestAsksForArtifacts(artifactRequest)
	if execute && functionFuzzExecutionNeedsConfirmation(run.Execution) {
		if approveRecovered {
			functionFuzzApproveExecution(commandCfg, &run, false)
		} else {
			run.Execution.ContinueCommand = firstNonBlankString(strings.TrimSpace(run.Execution.ContinueCommand), functionFuzzExecutionContinueCommand(run.ID))
			run.Execution = normalizeFunctionFuzzExecution(run.Execution)
			functionFuzzRefreshGuidance(commandCfg, &run)
			run.Summary = buildFunctionFuzzSummaryWithConfig(run, commandCfg)
		}
	}
	if execute && strings.EqualFold(strings.TrimSpace(run.Execution.Status), "planned") {
		s.rt.maybeLaunchFunctionFuzzExecution(&run)
	}
	saved, err := s.rt.saveFunctionFuzzRun(run, linkedCandidate, linkSourceCandidate)
	if err != nil {
		return "", err
	}
	if sourceOnlyResponse && !execute {
		var b strings.Builder
		b.WriteString("KernForge source-level fuzz plan\n\n")
		b.WriteString("Source-only fuzz planning completed. No native build, compile, or fuzz execution was requested.\n\n")
		summary := mcpFunctionFuzzSourceOnlySummary(saved, includeArtifacts)
		b.WriteString("User-facing result\n\n")
		b.WriteString("Use this section first when answering the user. It contains the source code, trigger values, and artifact paths.\n\n")
		b.WriteString(mcpRenderFunctionFuzzSourceOnlyDetails(saved, includeArtifacts))
		b.WriteString("\n\n")
		b.WriteString(mcpRenderFunctionFuzzSourceOnlySummary(summary))
		b.WriteString("\n\nAssistant contract\n\n")
		b.WriteString("- Summarize this source-level fuzz result and artifact paths, then offer the optional native path: native_preview -> build_only -> runtime fuzzing.\n")
		b.WriteString("- If meaningful_result is true, show problem_code_location, problem_code snippet, and trigger_values; if any field is unavailable, say that explicitly.\n")
		b.WriteString("- Use the Markdown-safe orange/yellow square labels from `Highlighted result labels`: `🟧 **Result**`, `🟨 **Top candidate**`, `🟧 **Problem location**`, `🟨 **Trigger conditions KernForge generated**`, and `🟧 **Artifacts**`. Do not use raw HTML spans.\n")
		b.WriteString("- Always include all artifact paths: artifact_dir, report_path, plan_path, and harness_path.\n")
		b.WriteString("- The final answer is incomplete if it lists only artifact_dir without report_path, plan_path, and harness_path.\n")
		b.WriteString("- For future KernForge-prefixed user requests, call KernForge before shell, rg, git status, or local source reads.\n")
		b.WriteString("- Do not preface source-only fuzz answers with tool availability checks, local code inspection, test discovery, fix plans, or modification plans.\n")
		b.WriteString("- Avoid Korean preamble phrases such as `KernForge 도구를 확인`, `실제 위치를 찾겠다`, `정의와 호출부를 찾겠다`, `테스트 구조를 보겠다`, or `퍼징 대상만 좁게 건드리겠다`.\n")
		b.WriteString("- Recommendation is allowed; do not call native preview, build, execute, campaign, shell, or artifact-reading tools unless the user explicitly asks for that next step.\n")
		b.WriteString("- Do not create custom harnesses or run ad hoc fuzz/semantic scripts from Codex after this response.\n")
		b.WriteString("- Do not add a source-code modification/no-modification note unless the user asked for code edits or source files were actually modified.\n")
		b.WriteString("- Artifact paths are included for user inspection; do not read artifact contents unless the user explicitly asks.\n")
		return mcpLimitText(b.String(), mcpSourceFuzzMaxChars(args, 40000)), nil
	}
	var b strings.Builder
	b.WriteString("KernForge function fuzz run\n\n")
	if execute && functionFuzzExecutionNeedsConfirmation(saved.Execution) && !approveRecovered {
		b.WriteString("Native execution was not started because recovered build settings need explicit approval. Re-run with execute=true and approve_recovered_build=true after reviewing the plan.\n\n")
	} else if execute {
		b.WriteString("Native execution request processed. Check auto_exec and background_job_id in the run output.\n\n")
	} else {
		b.WriteString("Source-level fuzz planning completed. Native execution was not started because execute=false.\n\n")
	}
	summary := mcpFunctionFuzzExecutionSummary(saved)
	summary["requested_execute"] = execute
	summary["approve_recovered_build"] = approveRecovered
	b.WriteString(mcpRenderFunctionFuzzExecutionSummary(summary))
	b.WriteString("\n\n")
	b.WriteString(renderFunctionFuzzRunWithConfig(saved, commandCfg))
	handoff := renderFunctionFuzzCampaignHandoff(s.rt.fuzzCampaignAutomationPlanForRun(saved))
	if strings.TrimSpace(handoff) != "" {
		b.WriteString("\n\n")
		b.WriteString(handoff)
	}
	return mcpLimitText(b.String(), mcpMaxChars(args, 40000)), nil
}

func (s *kernforgeMCPServer) toolFuzzFuncPreview(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	if s.rt.functionFuzz == nil {
		return "", fmt.Errorf("function fuzz store is not configured")
	}
	query := strings.TrimSpace(stringValue(args, "query"))
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	root := workspaceSnapshotRoot(s.rt.workspace)
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("workspace root is not available")
	}
	commandCfg := configWithResponseLanguageForUserText(s.rt.cfg, query)
	run, err := buildFunctionFuzzRun(commandCfg, root, query)
	if err != nil {
		return "", err
	}
	summary := mcpFunctionFuzzExecutionSummary(run)
	summary["preview_only"] = true
	summary["requested_execute"] = false
	summary["history_saved"] = false
	summary["artifacts_written"] = true
	summary["continue_command_available"] = false
	delete(summary, "continue_command")
	var b strings.Builder
	b.WriteString("KernForge function fuzz native execution preview\n\n")
	b.WriteString("Native execution was not started. This tool only previews eligibility, build/run commands, and missing settings.\n\n")
	b.WriteString(mcpRenderFunctionFuzzExecutionSummary(summary))
	if boolValue(args, "include_plan", false) {
		b.WriteString("\n\n")
		b.WriteString(renderFunctionFuzzRunWithConfig(run, commandCfg))
	}
	return mcpLimitText(b.String(), mcpMaxChars(args, 24000)), nil
}

func (s *kernforgeMCPServer) toolFuzzFuncBuild(ctx context.Context, args map[string]any) (string, error) {
	if s.rt.functionFuzz == nil {
		return "", fmt.Errorf("function fuzz store is not configured")
	}
	query := strings.TrimSpace(stringValue(args, "query"))
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	root := workspaceSnapshotRoot(s.rt.workspace)
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("workspace root is not available")
	}
	commandCfg := configWithResponseLanguageForUserText(s.rt.cfg, query)
	run, err := buildFunctionFuzzRun(commandCfg, root, query)
	if err != nil {
		return "", err
	}
	approveRecovered := boolValue(args, "approve_recovered_build", false)
	buildAttempted := false
	if functionFuzzExecutionNeedsConfirmation(run.Execution) && approveRecovered {
		functionFuzzApproveExecution(commandCfg, &run, false)
	}
	if functionFuzzExecutionNeedsConfirmation(run.Execution) && !approveRecovered {
		run.Execution.ContinueCommand = firstNonBlankString(strings.TrimSpace(run.Execution.ContinueCommand), functionFuzzExecutionContinueCommand(run.ID))
		run.Execution = normalizeFunctionFuzzExecution(run.Execution)
		functionFuzzRefreshGuidance(commandCfg, &run)
		run.Summary = buildFunctionFuzzSummaryWithConfig(run, commandCfg)
	} else if run.Execution.Eligible && strings.EqualFold(strings.TrimSpace(run.Execution.Status), "planned") {
		buildAttempted = true
		s.runFunctionFuzzBuildOnly(ctx, &run, intValue(args, "timeout_sec", 120))
	} else if strings.TrimSpace(run.Execution.Status) == "" {
		run.Execution.Status = "blocked"
		run.Execution.Reason = "No native build plan was generated for this target."
		run.Execution = normalizeFunctionFuzzExecution(run.Execution)
		functionFuzzRefreshGuidance(commandCfg, &run)
		run.Summary = buildFunctionFuzzSummaryWithConfig(run, commandCfg)
	}
	if err := writeFunctionFuzzPlanJSON(&run); err != nil {
		return "", err
	}
	saved, err := s.rt.functionFuzz.Upsert(run)
	if err != nil {
		return "", err
	}
	summary := mcpFunctionFuzzExecutionSummary(saved)
	summary["build_only"] = true
	summary["build_attempted"] = buildAttempted
	summary["approve_recovered_build"] = approveRecovered
	summary["history_saved"] = true
	summary["native_execution_started"] = false
	var b strings.Builder
	b.WriteString("KernForge function fuzz build-only\n\n")
	if buildAttempted {
		b.WriteString("Build-only compilation was attempted. The fuzz executable was not run.\n\n")
	} else if functionFuzzExecutionNeedsConfirmation(saved.Execution) && !approveRecovered {
		b.WriteString("Build-only compilation was not started because recovered build settings need explicit approval. Re-run with approve_recovered_build=true after reviewing the preview.\n\n")
	} else {
		b.WriteString("Build-only compilation was not started because the target is not eligible for native build planning.\n\n")
	}
	b.WriteString(mcpRenderFunctionFuzzExecutionSummary(summary))
	return mcpLimitText(b.String(), mcpMaxChars(args, 24000)), nil
}

func (s *kernforgeMCPServer) toolFuzzFuncStatus(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	if s.rt.functionFuzz == nil {
		return "", fmt.Errorf("function fuzz store is not configured")
	}
	id := strings.TrimSpace(stringValue(args, "id"))
	if id != "" {
		run, ok, err := s.rt.resolveFunctionFuzzRun(id)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("function fuzz run not found: %s", id)
		}
		if refreshed, changed := s.rt.refreshFunctionFuzzExecution(run); changed {
			run = refreshed
			if _, err := s.rt.functionFuzz.Upsert(run); err != nil {
				return "", err
			}
		}
		var b strings.Builder
		b.WriteString("KernForge function fuzz run status\n\n")
		b.WriteString(mcpRenderFunctionFuzzExecutionSummary(mcpFunctionFuzzExecutionSummary(run)))
		b.WriteString("\n\n")
		b.WriteString(renderFunctionFuzzRunWithConfig(run, s.rt.cfg))
		return mcpLimitText(b.String(), mcpMaxChars(args, 40000)), nil
	}
	limit := intValue(args, "limit", 8)
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}
	items, err := s.rt.functionFuzz.ListRecent(workspaceSnapshotRoot(s.rt.workspace), limit)
	if err != nil {
		return "", err
	}
	summaries := make([]map[string]any, 0, len(items))
	for _, run := range items {
		if refreshed, changed := s.rt.refreshFunctionFuzzExecution(run); changed {
			run = refreshed
			_, _ = s.rt.functionFuzz.Upsert(run)
		}
		summaries = append(summaries, mcpFunctionFuzzRunSummary(run))
	}
	payload := map[string]any{
		"workspace": workspaceSnapshotRoot(s.rt.workspace),
		"count":     len(summaries),
		"runs":      summaries,
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return "KernForge function fuzz runs\n\n```json\n" + string(data) + "\n```", nil
}

func (s *kernforgeMCPServer) toolFuzzArtifacts(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	if s.rt.functionFuzz == nil {
		return "", fmt.Errorf("function fuzz store is not configured")
	}
	id := strings.TrimSpace(stringValue(args, "id"))
	if id == "" {
		id = "latest"
	}
	run, ok, err := s.rt.resolveFunctionFuzzRun(id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("function fuzz run not found: %s", id)
	}
	if refreshed, changed := s.rt.refreshFunctionFuzzExecution(run); changed {
		run = refreshed
		if _, err := s.rt.functionFuzz.Upsert(run); err != nil {
			return "", err
		}
	}
	artifact := strings.ToLower(strings.TrimSpace(stringValue(args, "artifact")))
	if artifact == "" {
		artifact = "overview"
	}
	payload := map[string]any{
		"id":                       run.ID,
		"target":                   run.TargetSymbolName,
		"target_file":              run.TargetFile,
		"source_only_artifacts":    true,
		"native_execution_started": mcpFunctionFuzzNativeExecutionStarted(run.Execution),
		"artifact":                 artifact,
		"artifact_dir":             run.ArtifactDir,
		"plan_path":                run.PlanPath,
		"report_path":              run.ReportPath,
		"harness_path":             run.HarnessPath,
		"resource":                 "kernforge://fuzz/function-runs/" + run.ID,
		"note":                     "Reading artifacts never builds or runs native fuzzing.",
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	var b strings.Builder
	b.WriteString("KernForge function fuzz artifacts\n\n")
	b.WriteString("Artifact index\n\n```json\n")
	b.Write(data)
	b.WriteString("\n```\n")
	switch artifact {
	case "overview":
		b.WriteString("\nDetailed source-level summary\n\n")
		b.WriteString(renderFunctionFuzzRunWithConfig(run, s.rt.cfg))
	case "report":
		mcpAppendFuzzArtifactFile(&b, "report.md", run.ReportPath)
	case "plan":
		mcpAppendFuzzArtifactFile(&b, "plan.json", run.PlanPath)
	case "harness":
		mcpAppendFuzzArtifactFile(&b, "harness.cpp", run.HarnessPath)
	case "all":
		mcpAppendFuzzArtifactFile(&b, "report.md", run.ReportPath)
		mcpAppendFuzzArtifactFile(&b, "plan.json", run.PlanPath)
		mcpAppendFuzzArtifactFile(&b, "harness.cpp", run.HarnessPath)
	default:
		return "", fmt.Errorf("unsupported artifact %q; use overview, report, plan, harness, or all", artifact)
	}
	return mcpLimitText(b.String(), mcpMaxChars(args, 60000)), nil
}

func mcpAppendFuzzArtifactFile(b *strings.Builder, label string, path string) {
	path = strings.TrimSpace(path)
	b.WriteString("\n")
	b.WriteString(label)
	b.WriteString("\n\n")
	if path == "" {
		b.WriteString("(artifact path is empty)\n")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.WriteString("Could not read `")
		b.WriteString(path)
		b.WriteString("`: ")
		b.WriteString(err.Error())
		b.WriteString("\n")
		return
	}
	lang := ""
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		lang = "json"
	case ".md":
		lang = "markdown"
	case ".cpp", ".cc", ".cxx", ".h", ".hpp":
		lang = "cpp"
	}
	b.WriteString("Path: `")
	b.WriteString(path)
	b.WriteString("`\n\n")
	b.WriteString("```")
	b.WriteString(lang)
	b.WriteString("\n")
	b.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		b.WriteString("\n")
	}
	b.WriteString("```\n")
}

func mcpFunctionFuzzRunSummary(run FunctionFuzzRun) map[string]any {
	return map[string]any{
		"id":                         run.ID,
		"target_query":               run.TargetQuery,
		"target":                     run.TargetSymbolName,
		"target_file":                run.TargetFile,
		"source_candidate_id":        run.SourceCandidateID,
		"source_matcher_slug":        run.SourceMatcherSlug,
		"source_scan_mode":           run.SourceScanMode,
		"source_scan_run_id":         run.SourceScanRunID,
		"source_scan_summary":        run.SourceScanSummary,
		"risk_score":                 run.RiskScore,
		"primary_engine":             run.PrimaryEngine,
		"harness_ready":              run.HarnessReady,
		"scenario_count":             len(run.VirtualScenarios),
		"execution_status":           run.Execution.Status,
		"execution_eligible":         run.Execution.Eligible,
		"background_job_id":          run.Execution.BackgroundJobID,
		"crash_count":                run.Execution.CrashCount,
		"plan_path":                  run.PlanPath,
		"report_path":                run.ReportPath,
		"harness_path":               run.HarnessPath,
		"suggested_commands":         run.SuggestedCommands,
		"execution_continue_command": run.Execution.ContinueCommand,
		"summary":                    run.Summary,
	}
}

func mcpFunctionFuzzNativeFollowups(run FunctionFuzzRun) []map[string]any {
	target := strings.TrimSpace(run.TargetSymbolName)
	if target == "" {
		target = mcpGuideQueryTargetOnly(run.TargetQuery)
	}
	file := strings.TrimSpace(run.TargetFile)
	request := "KernForge로 " + firstNonBlankString(target, strings.TrimSpace(run.TargetQuery), "latest fuzz target") + " 퍼징해줘"
	if file != "" {
		request += " --file " + filepath.ToSlash(file)
	}
	return []map[string]any{
		{
			"id":          "native_preview",
			"label":       "native build/run preview",
			"description": "Check recovered build/run commands and missing settings without compiling or running fuzzing.",
			"tool_call": mcpGuideToolCall("kernforge_fuzz", map[string]any{
				"request": request,
				"target":  target,
				"file":    file,
				"mode":    "native_preview",
			}),
		},
		{
			"id":          "build_only",
			"label":       "compile generated harness only",
			"description": "After preview review, ask KernForge to attempt compile-only validation. The fuzz executable is not run.",
			"tool_call": mcpGuideToolCall("kernforge_guide", map[string]any{
				"request":        request,
				"target":         target,
				"file":           file,
				"execution_mode": "build_only",
			}),
		},
		{
			"id":          "runtime_fuzz",
			"label":       "native runtime fuzzing",
			"description": "After preview/build-only look correct and the user approves, ask KernForge to start native runtime fuzzing.",
			"tool_call": mcpGuideToolCall("kernforge_guide", map[string]any{
				"request":        request,
				"target":         target,
				"file":           file,
				"execution_mode": "execute",
			}),
		},
	}
}

func (s *kernforgeMCPServer) runFunctionFuzzBuildOnly(ctx context.Context, run *FunctionFuzzRun, timeoutSec int) {
	if run == nil {
		return
	}
	if timeoutSec <= 0 {
		timeoutSec = 120
	}
	if timeoutSec < 5 {
		timeoutSec = 5
	}
	if timeoutSec > 900 {
		timeoutSec = 900
	}
	argv := append([]string(nil), run.Execution.BuildArgv...)
	if len(argv) == 0 {
		run.Execution.Status = "blocked"
		run.Execution.Reason = "Build-only compile could not start because build_argv is empty."
		run.Execution = normalizeFunctionFuzzExecution(run.Execution)
		functionFuzzRefreshGuidance(s.rt.cfg, run)
		run.Summary = buildFunctionFuzzSummaryWithConfig(*run, s.rt.cfg)
		return
	}
	buildCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(buildCtx, argv[0], argv[1:]...)
	cmd.Dir = firstNonBlankString(run.Execution.CompileDirectory, run.Workspace)
	output, err := cmd.CombinedOutput()
	logText := string(output)
	if err != nil {
		if strings.TrimSpace(logText) != "" {
			logText += "\n"
		}
		logText += "build error: " + err.Error() + "\n"
	}
	if strings.TrimSpace(run.Execution.BuildLogPath) != "" {
		_ = os.WriteFile(run.Execution.BuildLogPath, []byte(logText), 0o644)
	}
	exitCode := 0
	switch {
	case buildCtx.Err() == context.DeadlineExceeded:
		exitCode = -1
		run.Execution.Status = "build_timed_out"
		run.Execution.Reason = fmt.Sprintf("Build-only compile timed out after %d seconds. The fuzz executable was not run.", timeoutSec)
	case err != nil:
		exitCode = mcpExitCodeFromError(err)
		run.Execution.Status = "build_failed"
		run.Execution.Reason = "Build-only compile failed. The fuzz executable was not run."
	default:
		run.Execution.Status = "build_succeeded"
		run.Execution.Reason = "Build-only compile succeeded. The fuzz executable was not run."
	}
	run.Execution.ExitCode = &exitCode
	run.Execution.BackgroundJobID = ""
	run.Execution.LastOutput = compactPersistentMemoryText(logText, 260)
	run.Execution = normalizeFunctionFuzzExecution(run.Execution)
	functionFuzzRefreshGuidance(s.rt.cfg, run)
	run.Summary = buildFunctionFuzzSummaryWithConfig(*run, s.rt.cfg)
}

func mcpExitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}

func mcpFunctionFuzzExecutionSummary(run FunctionFuzzRun) map[string]any {
	execState := run.Execution
	resultSummary := mcpFunctionFuzzResultSummary(run, false)
	payload := map[string]any{
		"id":                        run.ID,
		"target":                    run.TargetSymbolName,
		"target_file":               run.TargetFile,
		"harness_ready":             run.HarnessReady,
		"execution_eligible":        execState.Eligible,
		"native_execution_started":  mcpFunctionFuzzNativeExecutionStarted(execState),
		"requires_approval":         functionFuzzExecutionNeedsConfirmation(execState),
		"next_action":               mcpFunctionFuzzExecutionNextAction(run),
		"fuzz_result":               resultSummary,
		"meaningful_result":         resultSummary["meaningful_result"],
		"meaningful_result_kind":    resultSummary["result_kind"],
		"meaningful_result_summary": resultSummary["summary"],
	}
	mcpPutString(payload, "workspace", run.Workspace)
	mcpPutString(payload, "source_candidate_id", run.SourceCandidateID)
	mcpPutString(payload, "source_matcher_slug", run.SourceMatcherSlug)
	mcpPutString(payload, "source_scan_mode", run.SourceScanMode)
	mcpPutString(payload, "source_scan_run_id", run.SourceScanRunID)
	mcpPutString(payload, "source_scan_summary", run.SourceScanSummary)
	mcpPutString(payload, "auto_exec", execState.Status)
	mcpPutString(payload, "auto_exec_label", functionFuzzFriendlyExecutionStatusWithConfig(functionFuzzEnglishConfig(), execState.Status))
	mcpPutString(payload, "reason", execState.Reason)
	mcpPutString(payload, "compile_context", execState.CompileContextLevel)
	mcpPutString(payload, "compile_source", execState.CompileCommandSource)
	mcpPutString(payload, "compiler_candidate", execState.CompilerCandidate)
	mcpPutString(payload, "compiler", execState.CompilerResolvedPath)
	mcpPutString(payload, "build_command", execState.BuildCommand)
	mcpPutString(payload, "run_command", execState.RunCommand)
	mcpPutString(payload, "continue_command", execState.ContinueCommand)
	mcpPutString(payload, "background_job_id", execState.BackgroundJobID)
	mcpPutString(payload, "build_script", execState.BuildScriptPath)
	mcpPutString(payload, "build_log", execState.BuildLogPath)
	mcpPutString(payload, "run_log", execState.RunLogPath)
	mcpPutString(payload, "executable", execState.ExecutablePath)
	mcpPutString(payload, "corpus_dir", execState.CorpusDir)
	mcpPutString(payload, "crash_dir", execState.CrashDir)
	mcpPutString(payload, "artifact_dir", run.ArtifactDir)
	mcpPutString(payload, "plan_path", run.PlanPath)
	mcpPutString(payload, "report_path", run.ReportPath)
	mcpPutString(payload, "harness_path", run.HarnessPath)
	if len(execState.MissingSettings) > 0 {
		payload["missing_settings"] = execState.MissingSettings
	}
	if len(execState.RecoveryNotes) > 0 {
		payload["recovery_notes"] = execState.RecoveryNotes
	}
	if len(execState.BuildArgv) > 0 {
		payload["build_argv"] = execState.BuildArgv
	}
	if len(execState.RunArgv) > 0 {
		payload["run_argv"] = execState.RunArgv
	}
	if execState.CrashCount > 0 {
		payload["crash_count"] = execState.CrashCount
	}
	if execState.ExitCode != nil {
		payload["exit_code"] = *execState.ExitCode
	}
	if strings.TrimSpace(execState.LastOutput) != "" {
		payload["last_output"] = execState.LastOutput
	}
	return payload
}

func mcpFunctionFuzzSourceOnlySummary(run FunctionFuzzRun, includeArtifacts bool) map[string]any {
	topFinding := mcpFunctionFuzzTopFinding(run)
	resultSummary := mcpFunctionFuzzResultSummary(run, true)
	followups := mcpFunctionFuzzNativeFollowups(run)
	payload := map[string]any{
		"id":                         run.ID,
		"target":                     run.TargetSymbolName,
		"target_file":                run.TargetFile,
		"target_start_line":          run.TargetStartLine,
		"target_end_line":            run.TargetEndLine,
		"risk_score":                 run.RiskScore,
		"harness_ready":              run.HarnessReady,
		"source_only":                true,
		"compile_required":           false,
		"native_build_requested":     false,
		"native_execution_requested": false,
		"native_execution_started":   false,
		"history_saved":              true,
		"artifacts_available":        true,
		"artifacts_included":         true,
		"artifact_paths_included":    true,
		"artifact_content_included":  false,
		"must_report_artifact_paths": true,
		"artifact_paths": map[string]any{
			"artifact_dir": run.ArtifactDir,
			"plan_path":    run.PlanPath,
			"report_path":  run.ReportPath,
			"harness_path": run.HarnessPath,
		},
		"artifact_reader_tool": map[string]any{
			"name": "kernforge_fuzz_artifacts",
			"arguments": map[string]any{
				"id":       run.ID,
				"artifact": "overview",
			},
		},
		"result":                     "source_level_fuzz_completed",
		"bottom_line":                mcpFunctionFuzzSourceOnlyBottomLine(run),
		"fuzz_result":                resultSummary,
		"meaningful_result":          resultSummary["meaningful_result"],
		"meaningful_result_kind":     resultSummary["result_kind"],
		"meaningful_result_summary":  resultSummary["summary"],
		"confirmed_runtime_bug":      false,
		"runtime_crash_observed":     false,
		"sanitizer_finding_observed": false,
		"finding_count":              len(run.VirtualScenarios),
		"top_finding":                topFinding,
		"what_this_means":            "KernForge generated a source-level fuzz model and predicted review candidates. It did not execute a binary, so this is not a confirmed crash or sanitizer finding.",
		"default_next_step":          "report_result_and_offer_native_runtime_fuzzing",
		"recommended_followup": map[string]any{
			"summary": "After reporting the source-level result, offer native/runtime fuzzing as the next optional path. Do not start it unless the user asks.",
			"steps":   followups,
		},
		"must_not_auto_call_next_steps": []string{
			"native_preview",
			"build_only",
			"native fuzz execution",
			"custom harness",
			"ad hoc runner",
		},
		"stop_after_response":   true,
		"next_assistant_action": "Summarize this source-level fuzz result. If meaningful_result is true, show problem_code_location before the snippet, show the problematic code from problem_code, and show the triggering values or conditions from trigger_values. Always include artifact_paths.artifact_dir, artifact_paths.report_path, artifact_paths.plan_path, and artifact_paths.harness_path. The final answer is incomplete if it lists only artifact_dir. Then naturally offer optional native/runtime fuzzing as the next path: native_preview, build_only, then runtime fuzzing. Do not preface the answer with tool availability checks, local source inspection, test discovery, fix plans, or modification plans. Specifically avoid Korean phrasing like 'KernForge 도구를 확인', '실제 위치를 찾겠다', '정의와 호출부를 찾겠다', '테스트 구조를 보겠다', or '퍼징 대상만 좁게 건드리겠다'. Do not call shell, read artifacts, start campaign promotion, or call native preview/build/execute tools unless the user explicitly asks. Do not create custom harnesses or run ad hoc fuzz/semantic scripts. Do not add a source-code modification status unless the user asked for code edits or source files were actually modified.",
		"do_not_call_after_response": []string{
			"shell",
			"kernforge_fuzz_func_preview",
			"kernforge_fuzz_func_build",
			"kernforge_fuzz_func execute=true",
			"kernforge_fuzz_campaign_run",
			"kernforge_analyze_project",
			"kernforge_analysis_context",
			"custom_harness",
			"ad_hoc_runner",
		},
		"parameter_model":    run.ParameterStrategies,
		"guard_sink_signals": run.SinkSignals,
	}
	mcpPutString(payload, "workspace", run.Workspace)
	mcpPutString(payload, "scope_mode", run.ScopeMode)
	mcpPutString(payload, "source_candidate_id", run.SourceCandidateID)
	mcpPutString(payload, "source_matcher_slug", run.SourceMatcherSlug)
	mcpPutString(payload, "source_scan_mode", run.SourceScanMode)
	mcpPutString(payload, "source_scan_run_id", run.SourceScanRunID)
	mcpPutString(payload, "source_scan_summary", run.SourceScanSummary)
	mcpPutString(payload, "primary_engine", run.PrimaryEngine)
	mcpPutString(payload, "artifact_dir", run.ArtifactDir)
	mcpPutString(payload, "plan_path", run.PlanPath)
	mcpPutString(payload, "report_path", run.ReportPath)
	mcpPutString(payload, "harness_path", run.HarnessPath)
	if len(run.SecondaryEngines) > 0 {
		payload["secondary_engines"] = run.SecondaryEngines
	}
	if len(run.ScopeFiles) > 0 {
		payload["scope_files"] = run.ScopeFiles
	}
	return payload
}

func mcpFunctionFuzzResultSummary(run FunctionFuzzRun, sourceOnly bool) map[string]any {
	nativeStarted := mcpFunctionFuzzNativeExecutionStarted(run.Execution)
	results := []map[string]any{}
	topFinding := mcpFunctionFuzzTopFinding(run)
	problemCode := mcpFunctionFuzzProblemCodeForRun(run)
	problemCodeLocation := mcpFunctionFuzzProblemCodeLocation(problemCode)
	sourceTriggerValues := mcpFunctionFuzzTriggerValuesForRun(run)
	runtimeTriggerValues := mcpFunctionFuzzRuntimeTriggerValues(run)
	confirmedRuntimeCount := 0
	if run.Execution.CrashCount > 0 {
		confirmedRuntimeCount += run.Execution.CrashCount
		item := map[string]any{
			"kind":      "runtime_crash_artifacts",
			"confirmed": true,
			"count":     run.Execution.CrashCount,
			"summary":   fmt.Sprintf("Native fuzzing produced %d crash artifact(s).", run.Execution.CrashCount),
			"crash_dir": strings.TrimSpace(run.Execution.CrashDir),
			"status":    strings.TrimSpace(run.Execution.Status),
		}
		if len(runtimeTriggerValues) > 0 {
			item["trigger_values"] = runtimeTriggerValues
			item["trigger_values_summary"] = strings.Join(limitStrings(runtimeTriggerValues, 4), "; ")
		} else {
			item["trigger_values"] = []string{}
			item["trigger_values_note"] = "No concrete crashing input artifact was available in the recorded runtime state."
		}
		if len(problemCode) > 0 {
			item["problem_code"] = problemCode
			mcpPutString(item, "problem_code_location", problemCodeLocation)
			mcpPutString(item, "problem_code_summary", mcpFunctionFuzzProblemCodeSummary(problemCode))
		} else {
			item["problem_code_unavailable_reason"] = "No source excerpt or target source location was available for this fuzz run."
		}
		results = append(results, item)
	}
	if mcpFunctionFuzzOutputLooksLikeSanitizerFinding(run.Execution.LastOutput) {
		confirmedRuntimeCount++
		item := map[string]any{
			"kind":      "sanitizer_or_crash_output",
			"confirmed": true,
			"summary":   "Captured native fuzz output contains sanitizer or crash-like signal.",
			"status":    strings.TrimSpace(run.Execution.Status),
			"excerpt":   compactPersistentMemoryText(run.Execution.LastOutput, 220),
		}
		if len(runtimeTriggerValues) > 0 {
			item["trigger_values"] = runtimeTriggerValues
			item["trigger_values_summary"] = strings.Join(limitStrings(runtimeTriggerValues, 4), "; ")
		} else {
			item["trigger_values"] = []string{}
			item["trigger_values_note"] = "No concrete sanitizer input artifact was available in the recorded runtime state."
		}
		if len(problemCode) > 0 {
			item["problem_code"] = problemCode
			mcpPutString(item, "problem_code_location", problemCodeLocation)
			mcpPutString(item, "problem_code_summary", mcpFunctionFuzzProblemCodeSummary(problemCode))
		} else {
			item["problem_code_unavailable_reason"] = "No source excerpt or target source location was available for this fuzz run."
		}
		results = append(results, item)
	}
	predictedCount := len(run.VirtualScenarios)
	if predictedCount > 0 {
		item := map[string]any{
			"kind":      "source_predicted_issue",
			"confirmed": false,
			"count":     predictedCount,
			"summary":   mcpFunctionFuzzSourceOnlyBottomLine(run),
		}
		if title := strings.TrimSpace(stringValue(topFinding, "title")); title != "" {
			item["title"] = title
		}
		if score, ok := topFinding["risk_score"].(int); ok && score > 0 {
			item["risk_score"] = score
		}
		if issues, ok := topFinding["likely_issues"].([]string); ok && len(issues) > 0 {
			item["likely_issues"] = limitStrings(issues, 4)
		}
		if len(sourceTriggerValues) > 0 {
			item["trigger_values"] = sourceTriggerValues
			item["trigger_values_summary"] = strings.Join(limitStrings(sourceTriggerValues, 4), "; ")
		} else {
			item["trigger_values"] = []string{}
			item["trigger_values_note"] = "No concrete source-level trigger value was synthesized; inspect the input model and generated artifacts."
		}
		if len(problemCode) > 0 {
			item["problem_code"] = problemCode
			mcpPutString(item, "problem_code_location", problemCodeLocation)
			mcpPutString(item, "problem_code_summary", mcpFunctionFuzzProblemCodeSummary(problemCode))
		} else {
			item["problem_code_unavailable_reason"] = "No source excerpt or target source location was available for this fuzz run."
		}
		results = append(results, item)
	}

	resultKind := "none"
	summary := ""
	switch {
	case confirmedRuntimeCount > 0:
		resultKind = "confirmed_runtime_finding"
		summary = fmt.Sprintf("Meaningful native fuzz result found: %d confirmed runtime finding signal(s).", confirmedRuntimeCount)
	case predictedCount > 0:
		resultKind = "source_predicted_issue"
		top := mcpFunctionFuzzTopFinding(run)
		title := firstNonBlankString(strings.TrimSpace(stringValue(top, "title")), "review candidate")
		summary = fmt.Sprintf("Meaningful source-level result found: %d predicted review candidate(s). Top candidate: %s. No confirmed runtime crash or sanitizer finding was observed.", predictedCount, title)
	case nativeStarted && strings.EqualFold(strings.TrimSpace(run.Execution.Status), "completed"):
		resultKind = "no_runtime_finding_observed"
		summary = "No meaningful runtime finding was observed: native fuzzing completed without saved crash artifacts or sanitizer-like output in the captured result."
	case sourceOnly || !nativeStarted:
		resultKind = "no_source_issue_predicted"
		summary = "No meaningful source-level issue scenario was generated. Native execution was not run, so no runtime crash or sanitizer result is available."
	default:
		resultKind = "no_meaningful_result_yet"
		summary = "No meaningful fuzz result is available yet. Check status again after native execution finishes."
	}

	payload := map[string]any{
		"meaningful_result":               len(results) > 0,
		"result_kind":                     resultKind,
		"summary":                         summary,
		"confirmed_runtime_finding":       confirmedRuntimeCount > 0,
		"confirmed_runtime_finding_count": confirmedRuntimeCount,
		"predicted_source_finding_count":  predictedCount,
		"native_execution_started":        nativeStarted,
	}
	if len(results) > 0 {
		payload["must_report_problem_code_and_trigger_values"] = true
		if len(problemCode) > 0 {
			payload["problem_code"] = problemCode
			mcpPutString(payload, "problem_code_location", problemCodeLocation)
			mcpPutString(payload, "problem_code_summary", mcpFunctionFuzzProblemCodeSummary(problemCode))
		} else {
			payload["problem_code_unavailable_reason"] = "No source excerpt or target source location was available for this fuzz run."
		}
		if confirmedRuntimeCount > 0 && len(runtimeTriggerValues) > 0 {
			payload["trigger_values"] = runtimeTriggerValues
			payload["trigger_values_summary"] = strings.Join(limitStrings(runtimeTriggerValues, 4), "; ")
		} else if len(sourceTriggerValues) > 0 {
			payload["trigger_values"] = sourceTriggerValues
			payload["trigger_values_summary"] = strings.Join(limitStrings(sourceTriggerValues, 4), "; ")
		} else {
			payload["trigger_values"] = []string{}
			payload["trigger_values_note"] = "No concrete trigger value was synthesized or recorded."
		}
	}
	if len(results) > 0 {
		payload["meaningful_results"] = results
	} else {
		payload["meaningful_results"] = []map[string]any{}
		payload["no_meaningful_results_reason"] = summary
	}
	return payload
}

func mcpFunctionFuzzOutputLooksLikeSanitizerFinding(output string) bool {
	lower := strings.ToLower(strings.TrimSpace(output))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"addresssanitizer",
		"undefinedbehaviorsanitizer",
		"runtime error:",
		"deadlysignal",
		"heap-buffer-overflow",
		"stack-buffer-overflow",
		"global-buffer-overflow",
		"use-after-free",
		"use-after-return",
		"null pointer dereference",
		"access violation",
		"segmentation fault",
		"crash artifact",
	)
}

func mcpFunctionFuzzSourceOnlyBottomLine(run FunctionFuzzRun) string {
	if len(run.VirtualScenarios) == 0 {
		return "Source-level fuzzing completed. No predicted issue scenario was generated for this target."
	}
	top := mcpFunctionFuzzTopFinding(run)
	title := strings.TrimSpace(stringValue(top, "title"))
	if title == "" {
		title = "review candidate"
	}
	return fmt.Sprintf("Source-level fuzzing completed and predicted %d review candidate(s). Top candidate: %s. This is not a confirmed runtime crash.", len(run.VirtualScenarios), title)
}

func mcpFunctionFuzzTopFinding(run FunctionFuzzRun) map[string]any {
	best := functionFuzzTopScenario(run)
	if best == nil {
		return map[string]any{}
	}
	enrichedBest := mcpFunctionFuzzScenarioWithRunEvidence(run, *best)
	out := map[string]any{
		"title":      enrichedBest.Title,
		"risk_score": enrichedBest.RiskScore,
	}
	mcpPutString(out, "confidence", enrichedBest.Confidence)
	mcpPutString(out, "focus_symbol", enrichedBest.FocusSymbol)
	mcpPutString(out, "focus_file", enrichedBest.FocusFile)
	if len(enrichedBest.Inputs) > 0 {
		out["inputs"] = limitStrings(enrichedBest.Inputs, 6)
	}
	if len(enrichedBest.ConcreteInputs) > 0 {
		out["concrete_inputs"] = limitStrings(enrichedBest.ConcreteInputs, 6)
	}
	if len(enrichedBest.LikelyIssues) > 0 {
		out["likely_issues"] = limitStrings(enrichedBest.LikelyIssues, 6)
	}
	if len(enrichedBest.BranchFacts) > 0 {
		out["branch_facts"] = limitStrings(enrichedBest.BranchFacts, 6)
	}
	if strings.TrimSpace(enrichedBest.ExpectedFlow) != "" {
		out["expected_flow"] = enrichedBest.ExpectedFlow
	}
	if values := mcpFunctionFuzzTriggerValuesForScenario(enrichedBest); len(values) > 0 {
		out["trigger_values"] = values
		out["trigger_values_summary"] = strings.Join(limitStrings(values, 4), "; ")
	}
	if code := mcpFunctionFuzzProblemCodeForScenario(run, enrichedBest); len(code) > 0 {
		out["problem_code"] = code
		mcpPutString(out, "problem_code_location", mcpFunctionFuzzProblemCodeLocation(code))
		mcpPutString(out, "problem_code_summary", mcpFunctionFuzzProblemCodeSummary(code))
	}
	return out
}

func mcpFunctionFuzzTriggerValuesForRun(run FunctionFuzzRun) []string {
	best := functionFuzzTopScenario(run)
	if best == nil {
		return nil
	}
	return mcpFunctionFuzzTriggerValuesForScenario(mcpFunctionFuzzScenarioWithRunEvidence(run, *best))
}

func mcpFunctionFuzzScenarioWithRunEvidence(run FunctionFuzzRun, item FunctionFuzzVirtualScenario) FunctionFuzzVirtualScenario {
	if len(item.SourceExcerpt.Snippet) > 0 {
		return item
	}
	code := mcpFunctionFuzzProblemCodeForScenario(run, item)
	snippet := mcpFunctionFuzzProblemCodeSnippet(code)
	if len(snippet) == 0 {
		return item
	}
	item.SourceExcerpt.Snippet = snippet
	item.SourceExcerpt.File = firstNonBlankString(item.SourceExcerpt.File, stringValue(code, "file"))
	item.SourceExcerpt.StartLine = firstPositiveInt(item.SourceExcerpt.StartLine, intValue(code, "start_line", 0))
	item.SourceExcerpt.FocusLine = firstPositiveInt(item.SourceExcerpt.FocusLine, intValue(code, "focus_line", 0))
	item.SourceExcerpt.EndLine = firstPositiveInt(item.SourceExcerpt.EndLine, intValue(code, "end_line", 0))
	return item
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func mcpFunctionFuzzTriggerValuesForScenario(item FunctionFuzzVirtualScenario) []string {
	values := []string{}
	appendValues := func(items []string, transform func(string) string) {
		for _, raw := range items {
			value := strings.TrimSpace(raw)
			if transform != nil {
				value = strings.TrimSpace(transform(value))
			}
			if value != "" {
				values = append(values, value)
			}
		}
	}
	appendValues(item.ConcreteInputs, nil)
	appendValues(item.BranchFacts, func(value string) string {
		return functionFuzzComparisonCounterexampleText(functionFuzzEnglishConfig(), value)
	})
	appendValues(item.DriftExamples, func(value string) string {
		return functionFuzzDriftExampleText(functionFuzzEnglishConfig(), value)
	})
	appendValues(mcpFunctionFuzzFieldTriggerExamples(item), nil)
	appendValues(item.Inputs, nil)
	return limitStrings(uniqueStrings(values), 8)
}

func mcpFunctionFuzzFieldTriggerExamples(item FunctionFuzzVirtualScenario) []string {
	text := strings.ToLower(strings.Join(append(append([]string{}, item.SourceExcerpt.Snippet...), item.Inputs...), "\n"))
	out := []string{}
	if strings.Contains(text, "commanddata") {
		hasTotalSize := strings.Contains(text, "totalsize")
		hasCommand := strings.Contains(text, "command")
		if hasTotalSize && hasCommand {
			out = append(out,
				"commandData.totalSize = sizeof(TAVERN_IOCTL_COMMANDDATA_BASE), commandData.command = first valid command requiring a larger command-specific payload",
				"commandData.totalSize = sizeof(TAVERN_IOCTL_COMMANDDATA_BASE) + 1, commandData.command = valid command with payload shorter than its concrete struct",
				"commandData.totalSize = oversized value, commandData.command = valid/reserved command below TavernKernelCommand::Max",
				"commandData.command = reserved command below TavernKernelCommand::Max",
			)
		} else if hasTotalSize {
			out = append(out,
				"commandData.totalSize = sizeof(TAVERN_IOCTL_COMMANDDATA_BASE) - 1",
				"commandData.totalSize = sizeof(TAVERN_IOCTL_COMMANDDATA_BASE)",
				"commandData.totalSize = oversized value",
			)
		} else if hasCommand {
			out = append(out,
				"commandData.command = unsupported-but-in-range selector",
				"commandData.command = sparse or reserved selector below Max",
			)
		}
		if len(out) > 0 {
			return uniqueStrings(out)
		}
	}
	if strings.Contains(text, "size") || strings.Contains(text, "length") {
		if strings.Contains(text, "<") || strings.Contains(text, ">") {
			out = append(out,
				"size/length field = 0",
				"size/length field = boundary - 1",
				"size/length field = boundary + 1",
			)
		}
	}
	return uniqueStrings(out)
}

func mcpFunctionFuzzRuntimeTriggerValues(run FunctionFuzzRun) []string {
	values := []string{}
	if strings.TrimSpace(run.Execution.CrashDir) != "" {
		values = append(values, "crash_dir="+filepath.ToSlash(strings.TrimSpace(run.Execution.CrashDir)))
		for _, path := range mcpFunctionFuzzListCrashArtifacts(run.Execution.CrashDir, 5) {
			values = append(values, "crash_artifact="+filepath.ToSlash(path))
		}
	}
	if strings.TrimSpace(run.Execution.RunLogPath) != "" {
		values = append(values, "run_log="+filepath.ToSlash(strings.TrimSpace(run.Execution.RunLogPath)))
	}
	if strings.TrimSpace(run.Execution.LastOutput) != "" {
		values = append(values, "last_output="+compactPersistentMemoryText(run.Execution.LastOutput, 220))
	}
	return limitStrings(uniqueStrings(values), 8)
}

func mcpFunctionFuzzListCrashArtifacts(dir string, limit int) []string {
	dir = strings.TrimSpace(dir)
	if dir == "" || limit <= 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	paths := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(paths)
	if len(paths) > limit {
		paths = paths[:limit]
	}
	return paths
}

func mcpFunctionFuzzProblemCodeForRun(run FunctionFuzzRun) map[string]any {
	best := functionFuzzTopScenario(run)
	if best != nil {
		if code := mcpFunctionFuzzProblemCodeForScenario(run, *best); len(code) > 0 {
			return code
		}
	}
	return mcpFunctionFuzzProblemCodeFallback(run)
}

func mcpFunctionFuzzProblemCodeForScenario(run FunctionFuzzRun, item FunctionFuzzVirtualScenario) map[string]any {
	excerpt := item.SourceExcerpt
	if strings.TrimSpace(excerpt.File) == "" && len(excerpt.Snippet) == 0 {
		return mcpFunctionFuzzProblemCodeFallback(run)
	}
	code := map[string]any{}
	mcpPutString(code, "symbol", firstNonBlankString(excerpt.Symbol, item.FocusSymbol, run.TargetSymbolName))
	mcpPutString(code, "file", filepath.ToSlash(firstNonBlankString(excerpt.File, item.FocusFile, run.TargetFile)))
	if excerpt.StartLine > 0 {
		code["start_line"] = excerpt.StartLine
	}
	if excerpt.FocusLine > 0 {
		code["focus_line"] = excerpt.FocusLine
	}
	if excerpt.EndLine > 0 {
		code["end_line"] = excerpt.EndLine
	}
	if len(excerpt.Snippet) > 0 {
		snippet := excerpt.Snippet
		if expanded, startLine, endLine := mcpFunctionFuzzExpandedTargetSnippetForProblemCode(run); len(expanded) > len(snippet) {
			snippet = expanded
			code["start_line"] = startLine
			code["end_line"] = endLine
			if run.TargetStartLine > 0 {
				code["focus_line"] = run.TargetStartLine
			}
		}
		code["snippet"] = limitStrings(snippet, mcpFunctionFuzzProblemCodeSnippetLineLimit)
	} else if fallback := mcpFunctionFuzzTargetCodeSnippet(run); len(fallback) > 0 {
		if snippet := mcpFunctionFuzzProblemCodeSnippet(fallback); len(snippet) > 0 {
			code["snippet"] = snippet
			if file := strings.TrimSpace(stringValue(fallback, "file")); file != "" {
				code["file"] = file
			}
			if start := intValue(fallback, "start_line", 0); start > 0 {
				code["start_line"] = start
			}
			if focus := intValue(fallback, "focus_line", 0); focus > 0 {
				code["focus_line"] = focus
			}
			if end := intValue(fallback, "end_line", 0); end > 0 {
				code["end_line"] = end
			}
		}
	}
	mcpPutString(code, "why_this_code", firstNonBlankString(item.PathHint, item.ExpectedFlow))
	return code
}

func mcpFunctionFuzzExpandedTargetSnippetForProblemCode(run FunctionFuzzRun) ([]string, int, int) {
	if run.TargetStartLine <= 0 || run.TargetEndLine < run.TargetStartLine {
		return nil, 0, 0
	}
	if run.TargetEndLine-run.TargetStartLine+1 > mcpFunctionFuzzProblemCodeSnippetLineLimit {
		return nil, 0, 0
	}
	snippet, startLine, endLine := mcpFunctionFuzzReadTargetSnippet(run)
	if len(snippet) == 0 {
		return nil, 0, 0
	}
	if endLine-startLine+1 > mcpFunctionFuzzProblemCodeSnippetLineLimit {
		return nil, 0, 0
	}
	symbol := strings.TrimSpace(run.TargetSymbolName)
	if symbol != "" && !strings.Contains(strings.Join(snippet, "\n"), symbol) {
		return nil, 0, 0
	}
	return snippet, startLine, endLine
}

func mcpFunctionFuzzProblemCodeFallback(run FunctionFuzzRun) map[string]any {
	for _, item := range run.CodeObservations {
		file := filepath.ToSlash(strings.TrimSpace(item.File))
		if file == "" && strings.TrimSpace(item.Evidence) == "" {
			continue
		}
		code := map[string]any{}
		mcpPutString(code, "symbol", firstNonBlankString(item.Symbol, run.TargetSymbolName))
		mcpPutString(code, "file", firstNonBlankString(file, filepath.ToSlash(run.TargetFile)))
		if item.Line > 0 {
			code["focus_line"] = item.Line
			code["start_line"] = item.Line
			code["end_line"] = item.Line
		}
		if strings.TrimSpace(item.Evidence) != "" {
			code["snippet"] = []string{strings.TrimSpace(item.Evidence)}
		}
		mcpPutString(code, "why_this_code", item.WhyItMatters)
		return code
	}
	return mcpFunctionFuzzTargetCodeSnippet(run)
}

func mcpFunctionFuzzTargetCodeSnippet(run FunctionFuzzRun) map[string]any {
	file := firstNonBlankString(mcpFunctionFuzzResolveReadableTargetFile(run), strings.TrimSpace(run.TargetFile))
	if file == "" {
		return nil
	}
	code := map[string]any{}
	mcpPutString(code, "symbol", run.TargetSymbolName)
	mcpPutString(code, "file", filepath.ToSlash(file))
	if run.TargetStartLine > 0 {
		code["start_line"] = run.TargetStartLine
		code["focus_line"] = run.TargetStartLine
	}
	if run.TargetEndLine > 0 {
		code["end_line"] = run.TargetEndLine
	}
	snippet, startLine, endLine := mcpFunctionFuzzReadTargetSnippet(run)
	if len(snippet) > 0 {
		code["snippet"] = snippet
		code["start_line"] = startLine
		code["end_line"] = endLine
		if run.TargetStartLine > 0 {
			code["focus_line"] = run.TargetStartLine
		}
	}
	return code
}

func mcpFunctionFuzzReadTargetSnippet(run FunctionFuzzRun) ([]string, int, int) {
	file := firstNonBlankString(mcpFunctionFuzzResolveReadableTargetFile(run), strings.TrimSpace(run.TargetFile))
	if file == "" {
		return nil, 0, 0
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(run.Workspace, filepath.FromSlash(file))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return nil, 0, 0
	}
	start := run.TargetStartLine
	if start <= 0 {
		start = 1
	}
	end := run.TargetEndLine
	if end <= 0 || end < start {
		end = start + 8
	}
	if end-start >= mcpFunctionFuzzProblemCodeSnippetLineLimit {
		end = start + mcpFunctionFuzzProblemCodeSnippetLineLimit - 1
	}
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return nil, 0, 0
	}
	out := make([]string, 0, end-start+1)
	for _, line := range lines[start-1 : end] {
		out = append(out, strings.TrimRight(line, "\r"))
	}
	return out, start, end
}

func mcpFunctionFuzzResolveReadableTargetFile(run FunctionFuzzRun) string {
	file := strings.TrimSpace(run.TargetFile)
	root := strings.TrimSpace(run.Workspace)
	if file == "" || root == "" {
		return file
	}
	candidates := []string{file}
	if !filepath.IsAbs(file) {
		candidates = append(candidates, filepath.Join(root, filepath.FromSlash(file)))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			if rel, relErr := filepath.Rel(root, candidate); relErr == nil {
				return filepath.ToSlash(rel)
			}
			return candidate
		}
	}
	base := strings.ToLower(filepath.Base(filepath.FromSlash(file)))
	if base == "" || base == "." {
		return file
	}
	target := strings.TrimSpace(run.TargetSymbolName)
	found := ""
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return nil
		}
		if strings.ToLower(d.Name()) != base {
			return nil
		}
		if target != "" {
			data, readErr := os.ReadFile(path)
			if readErr == nil && !strings.Contains(string(data), target) {
				return nil
			}
		}
		if rel, relErr := filepath.Rel(root, path); relErr == nil {
			found = filepath.ToSlash(rel)
		} else {
			found = path
		}
		return nil
	})
	if found != "" {
		return found
	}
	return file
}

func mcpRenderFunctionFuzzSourceOnlySummary(summary map[string]any) string {
	data, _ := json.MarshalIndent(summary, "", "  ")
	return "Source fuzz summary\n\n```json\n" + string(data) + "\n```"
}

func mcpRenderFunctionFuzzSourceOnlyDetails(run FunctionFuzzRun, includeArtifacts bool) string {
	var b strings.Builder
	b.WriteString("Conclusion\n\n")
	resultSummary := mcpFunctionFuzzResultSummary(run, true)
	if boolValue(resultSummary, "meaningful_result", false) {
		b.WriteString("- Meaningful result: yes. ")
	} else {
		b.WriteString("- Meaningful result: no. ")
	}
	b.WriteString(strings.TrimSpace(stringValue(resultSummary, "summary")))
	b.WriteString("\n")
	if len(run.VirtualScenarios) == 0 {
		b.WriteString("- Result: source-level fuzzing completed; no predicted issue scenario was generated.\n")
	} else {
		top := mcpFunctionFuzzTopFinding(run)
		b.WriteString("- Result: source-level fuzzing completed; predicted review candidates: ")
		b.WriteString(fmt.Sprintf("%d", len(run.VirtualScenarios)))
		b.WriteString(".\n")
		if title := strings.TrimSpace(stringValue(top, "title")); title != "" {
			b.WriteString("- Top candidate: ")
			b.WriteString(title)
			if score, ok := top["risk_score"].(int); ok && score > 0 {
				b.WriteString(fmt.Sprintf(" (risk=%d)", score))
			}
			b.WriteString(".\n")
		}
		if issues, ok := top["likely_issues"].([]string); ok && len(issues) > 0 {
			b.WriteString("- Likely issue pattern: ")
			b.WriteString(strings.Join(limitStrings(issues, 3), "; "))
			b.WriteString(".\n")
		}
		if values := mcpFunctionFuzzTriggerValuesForRun(run); len(values) > 0 {
			b.WriteString("- Trigger values to report: ")
			b.WriteString(strings.Join(limitStrings(values, 4), "; "))
			b.WriteString(".\n")
		}
		if code := mcpFunctionFuzzProblemCodeForRun(run); len(code) > 0 {
			if location := mcpFunctionFuzzProblemCodeLocation(code); location != "" {
				b.WriteString("- Problem code to show: `")
				b.WriteString(location)
				b.WriteString("`.\n")
			}
		}
	}
	b.WriteString("- Confirmed crash/sanitizer finding: no. Native execution was not run.\n")
	b.WriteString("- Recommended next path: offer native_preview, then build_only, then native runtime fuzzing. Do not start any follow-up tool until the user asks.\n\n")
	b.WriteString(mcpRenderFunctionFuzzHighlightedSummary(run))
	if strings.TrimSpace(run.ArtifactDir) != "" || strings.TrimSpace(run.ReportPath) != "" || strings.TrimSpace(run.PlanPath) != "" || strings.TrimSpace(run.HarnessPath) != "" {
		b.WriteString("Artifact paths to report\n\n")
		if strings.TrimSpace(run.ArtifactDir) != "" {
			b.WriteString("- artifact_dir: `")
			b.WriteString(run.ArtifactDir)
			b.WriteString("`\n")
		}
		if strings.TrimSpace(run.ReportPath) != "" {
			b.WriteString("- report_path: `")
			b.WriteString(run.ReportPath)
			b.WriteString("`\n")
		}
		if strings.TrimSpace(run.PlanPath) != "" {
			b.WriteString("- plan_path: `")
			b.WriteString(run.PlanPath)
			b.WriteString("`\n")
		}
		if strings.TrimSpace(run.HarnessPath) != "" {
			b.WriteString("- harness_path: `")
			b.WriteString(run.HarnessPath)
			b.WriteString("`\n")
		}
		b.WriteString("\n")
	}

	if len(run.VirtualScenarios) > 0 {
		b.WriteString("Problem evidence\n\n")
		values := mcpFunctionFuzzTriggerValuesForRun(run)
		if len(values) > 0 {
			b.WriteString("- Trigger values / conditions: ")
			b.WriteString(strings.Join(limitStrings(values, 6), "; "))
			b.WriteString("\n")
		}
		code := mcpFunctionFuzzProblemCodeForRun(run)
		if len(code) > 0 {
			if location := mcpFunctionFuzzProblemCodeLocation(code); location != "" {
				b.WriteString("- Code location: `")
				b.WriteString(location)
				b.WriteString("`\n")
			}
			if why := strings.TrimSpace(stringValue(code, "why_this_code")); why != "" {
				b.WriteString("- Why this code: ")
				b.WriteString(why)
				b.WriteString("\n")
			}
			if snippet := mcpFunctionFuzzProblemCodeSnippet(code); len(snippet) > 0 {
				b.WriteString("\n```")
				b.WriteString(functionFuzzCodeFenceLanguage(stringValue(code, "file")))
				b.WriteString("\n")
				startLine := intValue(code, "start_line", 0)
				for index, line := range snippet {
					if startLine > 0 {
						b.WriteString(fmt.Sprintf("%4d | %s\n", startLine+index, line))
					} else {
						b.WriteString(line)
						b.WriteString("\n")
					}
				}
				b.WriteString("```\n")
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("Source-level plan\n\n")
	b.WriteString("- Summary: ")
	b.WriteString(mcpFunctionFuzzSourceOnlyPlainSummary(run))
	b.WriteString("\n")
	if strings.TrimSpace(run.TargetSignature) != "" {
		b.WriteString("- Signature: `")
		b.WriteString(run.TargetSignature)
		b.WriteString("`\n")
	}
	if len(run.ParameterStrategies) > 0 {
		b.WriteString("\nInput model\n\n")
		for _, item := range limitSlice(run.ParameterStrategies, 8) {
			b.WriteString("- ")
			b.WriteString(firstNonBlankString(item.Name, fmt.Sprintf("arg%d", item.Index)))
			if strings.TrimSpace(item.RawType) != "" {
				b.WriteString(" : `")
				b.WriteString(item.RawType)
				b.WriteString("`")
			}
			if len(item.Mutators) > 0 {
				b.WriteString(" | mutators: ")
				b.WriteString(strings.Join(limitStrings(item.Mutators, 6), ", "))
			}
			b.WriteString("\n")
		}
	}
	if len(run.SinkSignals) > 0 {
		b.WriteString("\nGuard / sink signals\n\n")
		for _, item := range limitSlice(run.SinkSignals, 8) {
			b.WriteString("- ")
			b.WriteString(firstNonBlankString(item.Name, item.Kind))
			if strings.TrimSpace(item.Kind) != "" {
				b.WriteString(" (`")
				b.WriteString(item.Kind)
				b.WriteString("`)")
			}
			if strings.TrimSpace(item.Reason) != "" {
				b.WriteString(": ")
				b.WriteString(item.Reason)
			}
			b.WriteString("\n")
		}
	}
	if len(run.VirtualScenarios) > 0 {
		b.WriteString("\nSource-only scenarios\n\n")
		for _, item := range limitSlice(run.VirtualScenarios, 5) {
			b.WriteString("- ")
			b.WriteString(item.Title)
			if item.RiskScore > 0 {
				b.WriteString(fmt.Sprintf(" (risk=%d)", item.RiskScore))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\nArtifact paths\n\n")
	if strings.TrimSpace(run.ArtifactDir) != "" {
		b.WriteString("- dir: `")
		b.WriteString(run.ArtifactDir)
		b.WriteString("`\n")
	}
	if strings.TrimSpace(run.ReportPath) != "" {
		b.WriteString("- report: `")
		b.WriteString(run.ReportPath)
		b.WriteString("`\n")
	}
	if strings.TrimSpace(run.PlanPath) != "" {
		b.WriteString("- plan: `")
		b.WriteString(run.PlanPath)
		b.WriteString("`\n")
	}
	if strings.TrimSpace(run.HarnessPath) != "" {
		b.WriteString("- harness source: `")
		b.WriteString(run.HarnessPath)
		b.WriteString("`\n")
	}
	b.WriteString("\nOptional native/runtime follow-up\n\n")
	b.WriteString("- native_preview: review recovered build/run commands and missing settings without compiling or running.\n")
	b.WriteString("- build_only: after preview review, compile the generated harness only; do not run fuzzing.\n")
	b.WriteString("- runtime fuzzing: after preview/build-only look correct and the user approves, start native fuzz execution.\n")
	return b.String()
}

func mcpRenderFunctionFuzzHighlightedSummary(run FunctionFuzzRun) string {
	var b strings.Builder
	resultSummary := mcpFunctionFuzzResultSummary(run, true)
	top := mcpFunctionFuzzTopFinding(run)
	location := ""
	if code := mcpFunctionFuzzProblemCodeForRun(run); len(code) > 0 {
		location = mcpFunctionFuzzProblemCodeLocation(code)
	}
	b.WriteString("Highlighted result labels\n\n")
	b.WriteString("- ")
	b.WriteString(mcpHighlightedResultLabel("Result"))
	b.WriteString(": ")
	b.WriteString(strings.TrimSpace(stringValue(resultSummary, "summary")))
	b.WriteString("\n")
	if title := strings.TrimSpace(stringValue(top, "title")); title != "" {
		b.WriteString("- ")
		b.WriteString(mcpHighlightedResultLabel("Top candidate"))
		b.WriteString(": ")
		b.WriteString(title)
		b.WriteString("\n")
	}
	if location != "" {
		b.WriteString("- ")
		b.WriteString(mcpHighlightedResultLabel("Problem location"))
		b.WriteString(": `")
		b.WriteString(location)
		b.WriteString("`\n")
	}
	if values := mcpFunctionFuzzTriggerValuesForRun(run); len(values) > 0 {
		b.WriteString("- ")
		b.WriteString(mcpHighlightedResultLabel("Trigger conditions KernForge generated"))
		b.WriteString(": ")
		b.WriteString(strings.Join(limitStrings(values, 4), "; "))
		b.WriteString("\n")
	}
	if strings.TrimSpace(run.ArtifactDir) != "" || strings.TrimSpace(run.ReportPath) != "" || strings.TrimSpace(run.PlanPath) != "" || strings.TrimSpace(run.HarnessPath) != "" {
		b.WriteString("- ")
		b.WriteString(mcpHighlightedResultLabel("Artifacts"))
		b.WriteString(": ")
		parts := []string{}
		if strings.TrimSpace(run.ArtifactDir) != "" {
			parts = append(parts, "artifact_dir")
		}
		if strings.TrimSpace(run.ReportPath) != "" {
			parts = append(parts, "report_path")
		}
		if strings.TrimSpace(run.PlanPath) != "" {
			parts = append(parts, "plan_path")
		}
		if strings.TrimSpace(run.HarnessPath) != "" {
			parts = append(parts, "harness_path")
		}
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

func mcpHighlightedResultLabel(label string) string {
	switch label {
	case "Top candidate", "Trigger conditions KernForge generated":
		return "🟨 **" + label + "**"
	default:
		return "🟧 **" + label + "**"
	}
}

func mcpFunctionFuzzProblemCodeLocation(code map[string]any) string {
	file := filepath.ToSlash(strings.TrimSpace(stringValue(code, "file")))
	if file == "" {
		return ""
	}
	start := intValue(code, "start_line", 0)
	end := intValue(code, "end_line", 0)
	focus := intValue(code, "focus_line", 0)
	switch {
	case focus > 0:
		return fmt.Sprintf("%s:%d", file, focus)
	case start > 0 && end > start:
		return fmt.Sprintf("%s:%d-%d", file, start, end)
	case start > 0:
		return fmt.Sprintf("%s:%d", file, start)
	default:
		return file
	}
}

func mcpFunctionFuzzProblemCodeSummary(code map[string]any) string {
	location := mcpFunctionFuzzProblemCodeLocation(code)
	symbol := strings.TrimSpace(stringValue(code, "symbol"))
	if location == "" {
		return symbol
	}
	if symbol == "" {
		return location
	}
	return fmt.Sprintf("%s at %s", symbol, location)
}

const mcpFunctionFuzzProblemCodeSnippetLineLimit = 40

func mcpFunctionFuzzProblemCodeSnippet(code map[string]any) []string {
	raw, ok := code["snippet"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return limitStrings(typed, mcpFunctionFuzzProblemCodeSnippetLineLimit)
	case []any:
		out := []string{}
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return limitStrings(out, mcpFunctionFuzzProblemCodeSnippetLineLimit)
	default:
		text := strings.TrimSpace(fmt.Sprint(raw))
		if text == "" {
			return nil
		}
		return []string{text}
	}
}

func mcpFunctionFuzzSourceOnlyPlainSummary(run FunctionFuzzRun) string {
	parts := []string{
		"Planned source-level fuzzing for " + firstNonBlankString(run.TargetSymbolName, run.TargetQuery),
		fmt.Sprintf("risk=%d", run.RiskScore),
	}
	if strings.TrimSpace(run.PrimaryEngine) != "" {
		parts = append(parts, "engine model="+run.PrimaryEngine)
	}
	if len(run.ParameterStrategies) > 0 {
		parts = append(parts, fmt.Sprintf("input models=%d", len(run.ParameterStrategies)))
	}
	if len(run.SinkSignals) > 0 {
		parts = append(parts, fmt.Sprintf("guard/sink signals=%d", len(run.SinkSignals)))
	}
	parts = append(parts, "native build/compile/execution not requested")
	return strings.Join(parts, "; ") + "."
}

func mcpRenderFunctionFuzzExecutionSummary(summary map[string]any) string {
	data, _ := json.MarshalIndent(summary, "", "  ")
	return "Execution summary\n\n```json\n" + string(data) + "\n```"
}

func mcpFunctionFuzzNativeExecutionStarted(execState FunctionFuzzExecution) bool {
	if strings.TrimSpace(execState.BackgroundJobID) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(execState.Status)) {
	case "running", "completed", "failed", "canceled", "preempted":
		return true
	default:
		return false
	}
}

func mcpFunctionFuzzExecutionNextAction(run FunctionFuzzRun) string {
	status := strings.ToLower(strings.TrimSpace(run.Execution.Status))
	switch {
	case functionFuzzExecutionNeedsConfirmation(run.Execution):
		return "Review build_command, run_command, and missing_settings. Re-run with execute=true and approve_recovered_build=true only if the recovered build settings look correct."
	case status == "planned":
		return "The native fuzz plan is ready to start. Use execute=true after reviewing build_command and run_command."
	case status == "running":
		return "Monitor this run with kernforge_fuzz_func_status and inspect build_log, run_log, and crash_dir."
	case status == "completed":
		return "Review run_log and crash_dir for sanitizer findings or saved repro artifacts."
	case status == "build_succeeded":
		return "Build-only compile succeeded. Review build_log, then run native fuzz only with explicit approval."
	case status == "build_failed":
		return "Build-only compile failed. Fix missing includes, defines, PCH, or harness bindings shown in build_log before running fuzz."
	case status == "build_timed_out":
		return "Build-only compile timed out. Review build_log and build settings before retrying with a higher timeout."
	case status == "blocked":
		return "Fix the blocking reason or missing_settings, then create a new function fuzz run."
	default:
		return "Review the source-level plan first; native execution has not been requested or scheduled."
	}
}

func mcpPutString(payload map[string]any, name string, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		payload[name] = value
	}
}

func mcpGuideInferIntent(request string, target string, file string, symptom string, goal string) string {
	lower := strings.ToLower(strings.Join([]string{request, target, file, symptom, goal}, " "))
	switch {
	case strings.TrimSpace(lower) == "":
		return "unknown"
	case containsAny(lower, "fuzz", "fuzzing", "libfuzzer", "harness", "crash", "corpus", "sanitize", "sanitizer", "퍼즈", "퍼징", "하네스", "크래시", "충돌"):
		return "fuzz"
	case containsAny(lower, "root cause", "root-cause", "why", "symptom", "regression", "failure", "bug", "원인", "근본", "왜", "실패", "버그", "증상"):
		return "root_cause"
	case mcpGuideLooksLikeReviewIntent(lower):
		return "review"
	case containsAny(lower, "verify", "verification", "test", "check", "build", "검증", "테스트", "확인", "빌드"):
		return "verify"
	case containsAny(lower, "analyze", "analysis", "map", "architecture", "surface", "document", "catalog", "분석", "구조", "문서", "카탈로그"):
		return "analyze"
	case containsAny(lower, "status", "state", "workspace", "health", "상태", "워크스페이스"):
		return "status"
	case strings.TrimSpace(target) != "" || strings.TrimSpace(file) != "" || mcpGuideLooksLikeBareTarget(request) || mcpGuideExtractBareTarget(request) != "":
		return "target_only"
	default:
		return "unknown"
	}
}

func mcpGuideLooksLikeReviewIntent(lower string) bool {
	if containsAny(lower, "code review", "diff review", "review this code", "review the code", "review changes", "review diff", "pr review", "patch review", "리뷰", "코드 리뷰", "리뷰해") {
		return true
	}
	if !containsAny(lower, "검토", "검토해", "검토하") {
		return false
	}
	return containsAny(lower, "code", "diff", "patch", "pr", "change", "changes", "modified", "written", "코드", "변경", "수정", "작성", "패치", "차이", "방금")
}

func mcpGuideFuzzQuery(request string, target string, file string) string {
	target = strings.TrimSpace(target)
	file = strings.TrimSpace(file)
	if target == "" {
		target = mcpGuideExtractFuzzTarget(request)
	}
	if target == "" {
		extracted := mcpGuideExtractBareTarget(request)
		if mcpGuideLooksLikePath(extracted) {
			file = firstNonBlankString(file, extracted)
		} else {
			target = extracted
		}
	}
	if target == "" && mcpGuideLooksLikeBareTarget(request) {
		if mcpGuideLooksLikePath(request) {
			file = firstNonBlankString(file, strings.TrimSpace(request))
		} else {
			target = strings.TrimSpace(request)
		}
	}
	if target == "" && file != "" {
		return "@" + file
	}
	if target == "" {
		return ""
	}
	if file != "" && !strings.Contains(target, "--file") && !strings.Contains(target, "@") {
		return target + " --file " + file
	}
	return target
}

func mcpGuideLooksLikeBareTarget(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if len(strings.Fields(value)) > 1 && !mcpGuideLooksLikePath(value) {
		return false
	}
	lower := strings.ToLower(value)
	if containsAny(lower, " ", "\t", "\n") {
		return false
	}
	if mcpGuideLooksLikePath(value) {
		return true
	}
	return mcpGuideLooksLikeIdentifierTarget(value) || strings.Contains(value, "::")
}

func mcpGuideLooksLikePath(value string) bool {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(filepath.ToSlash(value))
	return strings.Contains(lower, "/") || strings.HasSuffix(lower, ".cpp") || strings.HasSuffix(lower, ".cc") || strings.HasSuffix(lower, ".cxx") || strings.HasSuffix(lower, ".c") || strings.HasSuffix(lower, ".h") || strings.HasSuffix(lower, ".hpp")
}

func mcpGuideExtractBareTarget(request string) string {
	tokens := splitAnalysisCommandLine(strings.TrimSpace(request))
	for _, token := range tokens {
		candidate := mcpGuideCleanTargetToken(token)
		if candidate == "" || mcpGuideGenericTargetWord(candidate) {
			continue
		}
		if mcpGuideLooksLikePath(candidate) {
			return candidate
		}
		if strings.Contains(candidate, "::") {
			return candidate
		}
		if mcpGuideLooksLikeIdentifierTarget(candidate) {
			return candidate
		}
	}
	return ""
}

func mcpGuideLooksLikeIdentifierTarget(value string) bool {
	return functionFuzzIdentPattern.FindString(value) == value
}

func mcpGuideCleanTargetToken(token string) string {
	token = strings.TrimSpace(token)
	token = strings.Trim(token, "`\"'.,:;()[]{}<>")
	token = strings.TrimPrefix(token, "@")
	token = mcpGuideTrimKoreanParticle(token)
	return strings.TrimSpace(token)
}

func mcpGuideTrimKoreanParticle(value string) string {
	value = strings.TrimSpace(value)
	suffixes := []string{"에서", "으로", "부터", "까지", "에게", "한테", "하고", "처럼", "으로", "로", "을", "를", "은", "는", "이", "가", "에", "도", "만", "와", "과", "랑"}
	for {
		changed := false
		for _, suffix := range suffixes {
			if strings.HasSuffix(value, suffix) && len([]rune(value)) > len([]rune(suffix)) {
				value = strings.TrimSuffix(value, suffix)
				changed = true
				break
			}
		}
		if !changed {
			return value
		}
	}
}

func mcpGuideExtractFuzzTarget(request string) string {
	tokens := splitAnalysisCommandLine(strings.TrimSpace(request))
	for i, token := range tokens {
		lower := strings.ToLower(strings.Trim(token, ".,:;"))
		if lower == "fuzz" || lower == "fuzzing" || lower == "harness" || lower == "퍼즈" || lower == "퍼징" || lower == "하네스" {
			for j := i + 1; j < len(tokens); j++ {
				next := strings.Trim(tokens[j], ".,:;")
				if next != "" && !strings.HasPrefix(next, "-") && !mcpGuideGenericTargetWord(next) {
					return next
				}
			}
		}
	}
	return ""
}

func mcpGuideGenericTargetWord(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "this", "that", "it", "the", "a", "an", "target", "function", "file", "driver", "entry", "point", "routine", "code", "path",
		"kernforge", "codex", "mcp", "look", "review", "inspect", "analyze", "analysis", "check", "please",
		"fuzz", "fuzzing", "harness", "verify", "verification", "build", "test", "status", "root", "cause",
		"이", "그", "이거", "저거", "대상", "타깃", "함수", "파일", "드라이버", "엔트리", "포인트", "루틴", "코드", "경로",
		"봐줘", "봐", "보자", "검토", "확인", "분석", "빌드", "테스트", "상태", "원인", "퍼즈", "퍼징", "하네스", "좀", "부탁":
		return true
	default:
		return false
	}
}

func mcpGuideQuestion(id string, question string, detail string) map[string]any {
	return map[string]any{
		"id":       id,
		"question": question,
		"detail":   detail,
	}
}

func mcpGuideToolCall(name string, args map[string]any) map[string]any {
	return map[string]any{
		"name":      name,
		"arguments": args,
	}
}

func mcpGuideChoice(id string, label string, description string, toolCall map[string]any) map[string]any {
	return map[string]any{
		"id":                    id,
		"label":                 label,
		"description":           description,
		"recommended_tool_call": toolCall,
	}
}

func mcpGuideTargetChoices(query string, target string, file string, request string) []map[string]any {
	return []map[string]any{
		mcpGuideChoice("preview", "안전하게 가능 여부만 보기", "Native 실행 없이 fuzz 가능성, build_argv, missing_settings만 확인합니다.", mcpGuideToolCall("kernforge_fuzz_func_preview", map[string]any{
			"query":        query,
			"include_plan": false,
		})),
		mcpGuideChoice("plan", "소스 레벨 fuzz 결과 보기", "Native 실행 없이 KernForge source-only fuzz 결과를 만들고 Codex는 요약 후 멈춥니다.", mcpGuideToolCall("kernforge_fuzz", map[string]any{
			"request": request,
			"target":  target,
			"file":    file,
			"mode":    "source",
		})),
		mcpGuideChoice("build_only", "컴파일까지만 확인", "먼저 preview를 본 뒤 승인되면 harness 컴파일만 시도합니다. fuzz executable은 실행하지 않습니다.", mcpGuideToolCall("kernforge_guide", map[string]any{
			"request":        "fuzz " + query,
			"target":         target,
			"file":           file,
			"execution_mode": "build_only",
		})),
		mcpGuideChoice("verify", "관련 검증 계획 보기", "해당 파일 또는 함수 주변 변경에 대한 verification plan을 만듭니다. 기본은 execute=false입니다.", mcpGuideToolCall("kernforge_verify", map[string]any{
			"mode":    "adaptive",
			"paths":   mcpGuideVerificationPaths(file, request),
			"execute": false,
		})),
	}
}

func mcpFuzzShouldRetryWithoutFile(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "file hint did not match") || strings.Contains(text, "matched no indexed source file")
}

func mcpFuzzRequestAsksForArtifacts(request string) bool {
	lower := strings.ToLower(request)
	return containsAny(lower,
		"artifact", "artifacts", "path", "paths", "report", "harness", "plan.json", "report.md", "harness.cpp",
		"산출물", "아티팩트", "경로", "파일", "리포트", "보고서", "하네스",
	)
}

func mcpGuideQueryTargetOnly(query string) string {
	tokens := splitAnalysisCommandLine(strings.TrimSpace(query))
	if len(tokens) == 0 {
		return ""
	}
	first := strings.TrimSpace(tokens[0])
	if strings.HasPrefix(first, "@") || first == "" {
		return ""
	}
	return first
}

func limitSlice[T any](items []T, limit int) []T {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func mcpGuideVerificationPaths(file string, request string) []string {
	if strings.TrimSpace(file) != "" {
		return []string{strings.TrimSpace(file)}
	}
	if mcpGuideLooksLikePath(request) {
		return []string{strings.TrimSpace(request)}
	}
	return nil
}

func (s *kernforgeMCPServer) toolFuzzCampaignStatus(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	if s.rt.fuzzCampaigns == nil {
		return "", fmt.Errorf("fuzz campaign store is not configured")
	}
	id := strings.TrimSpace(stringValue(args, "id"))
	if id != "" {
		campaign, ok, err := s.rt.resolveFuzzCampaign(id)
		if err != nil {
			return "", err
		}
		if !ok {
			return "No fuzz campaign found for " + id + ". Use kernforge_fuzz_campaign_run with execute=true to create one.", nil
		}
		var b strings.Builder
		b.WriteString("KernForge fuzz campaign status\n\n")
		b.WriteString(renderFuzzCampaign(campaign))
		b.WriteString("\n\n")
		b.WriteString(renderFuzzCampaignNextStep(s.rt.fuzzCampaignAutomationPlan(campaign)))
		return mcpLimitText(b.String(), mcpMaxChars(args, 40000)), nil
	}
	limit := intValue(args, "limit", 8)
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}
	items, err := s.rt.fuzzCampaigns.ListRecent(workspaceSnapshotRoot(s.rt.workspace), limit)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "No fuzz campaigns found.\n\n" + renderFuzzCampaignNextStep(s.rt.fuzzCampaignAutomationPlan(FuzzCampaign{})), nil
	}
	summaries := make([]map[string]any, 0, len(items))
	for _, campaign := range items {
		summaries = append(summaries, mcpFuzzCampaignSummary(campaign))
	}
	data, _ := json.MarshalIndent(map[string]any{
		"workspace": workspaceSnapshotRoot(s.rt.workspace),
		"count":     len(summaries),
		"campaigns": summaries,
	}, "", "  ")
	var b strings.Builder
	b.WriteString("KernForge fuzz campaigns\n\n```json\n")
	b.Write(data)
	b.WriteString("\n```\n\nLatest campaign:\n\n")
	b.WriteString(renderFuzzCampaign(items[0]))
	b.WriteString("\n\n")
	b.WriteString(renderFuzzCampaignNextStep(s.rt.fuzzCampaignAutomationPlan(items[0])))
	return mcpLimitText(b.String(), mcpMaxChars(args, 40000)), nil
}

func mcpFuzzCampaignSummary(campaign FuzzCampaign) map[string]any {
	return map[string]any{
		"id":               campaign.ID,
		"name":             campaign.Name,
		"status":           campaign.Status,
		"manifest_path":    campaign.ManifestPath,
		"artifact_dir":     campaign.ArtifactDir,
		"seed_targets":     len(campaign.SeedTargets),
		"function_runs":    len(campaign.FunctionRuns),
		"seed_artifacts":   len(campaign.SeedArtifacts),
		"native_results":   len(campaign.NativeResults),
		"findings":         len(campaign.Findings),
		"coverage_reports": len(campaign.CoverageReports),
		"coverage_gaps":    len(campaign.CoverageGaps),
		"summary":          campaign.Summary,
	}
}

func (s *kernforgeMCPServer) toolFuzzCampaignRun(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	if s.rt.fuzzCampaigns == nil {
		return "", fmt.Errorf("fuzz campaign store is not configured")
	}
	execute := boolValue(args, "execute", false)
	if !execute {
		campaign, ok, err := s.rt.resolveFuzzCampaign("latest")
		if err != nil {
			return "", err
		}
		if !ok {
			campaign = FuzzCampaign{}
		}
		return "KernForge fuzz campaign plan\n\n" + renderFuzzCampaignNextStep(s.rt.fuzzCampaignAutomationPlan(campaign)), nil
	}
	name := strings.TrimSpace(stringValue(args, "name"))
	if name != "" {
		if _, ok, err := s.rt.resolveFuzzCampaign("latest"); err != nil {
			return "", err
		} else if !ok {
			manifest := AnalysisDocsManifest{}
			if loaded, loadedOK := s.loadLatestManifest(); loadedOK {
				manifest = loaded
			}
			campaign, err := createFuzzCampaignFromWorkspace(workspaceSnapshotRoot(s.rt.workspace), name, manifest)
			if err != nil {
				return "", err
			}
			if _, err := s.rt.fuzzCampaigns.Append(campaign); err != nil {
				return "", err
			}
		}
	}
	output, err := captureMCPRuntimeOutput(s.rt, func() error {
		return s.rt.runFuzzCampaignAutomation()
	})
	if err != nil {
		return output, err
	}
	return mcpLimitText(output, mcpMaxChars(args, 40000)), nil
}

func (s *kernforgeMCPServer) toolVerify(ctx context.Context, args map[string]any) (string, error) {
	mode := VerificationAdaptive
	if strings.EqualFold(strings.TrimSpace(stringValue(args, "mode")), "full") {
		mode = VerificationFull
	}
	changed := stringSliceValue(args, "paths")
	if len(changed) == 0 {
		changed = collectVerificationChangedPaths(s.rt.workspace.Root, s.rt.session)
	}
	tuning := VerificationTuning{}
	if s.rt.verifyHistory != nil {
		if loaded, err := s.rt.verifyHistory.PlannerTuning(s.rt.workspace.Root); err == nil {
			tuning = loaded
		}
	}
	plan := buildVerificationPlanWithTuning(s.rt.workspace.Root, changed, mode, tuning)
	if len(plan.Steps) == 0 {
		return "No recommended verification steps were found for this workspace.", nil
	}
	if !boolValue(args, "execute", true) {
		data, _ := json.MarshalIndent(plan, "", "  ")
		return "KernForge verification plan\n\n```json\n" + string(data) + "\n```", nil
	}
	ws := s.rt.workspace
	ws.Perms = NewPermissionManager(ModeBypass, nil)
	report := executeVerificationSteps(ctx, ws, "mcp", plan)
	s.rt.session.LastVerification = &report
	_ = s.rt.store.Save(s.rt.session)
	if s.rt.verifyHistory != nil {
		_ = s.rt.verifyHistory.Append(s.rt.session.ID, workspaceSnapshotRoot(s.rt.workspace), report)
	}
	if s.rt.evidence != nil {
		_ = s.rt.evidence.CaptureVerification(s.rt.workspace, s.rt.session)
	}
	return report.RenderDetailed(), nil
}

func (s *kernforgeMCPServer) toolAnalyzeProject(ctx context.Context, args map[string]any) (string, error) {
	if err := s.ensureProviderReady(); err != nil {
		return "", err
	}
	mode := normalizeProjectAnalysisMode(stringValue(args, "mode"))
	if mode == "" && strings.TrimSpace(stringValue(args, "mode")) != "" {
		return "", fmt.Errorf("invalid analysis mode: %s", stringValue(args, "mode"))
	}
	paths := stringSliceValue(args, "paths")
	goal := strings.TrimSpace(stringValue(args, "goal"))
	if goal == "" {
		goal = defaultProjectAnalysisGoal(mode, paths)
	}
	analysisWorkspace, analysisCfg, explicitScope, err := s.prepareMCPAnalysis(mode, goal, paths, args)
	if err != nil {
		return "", err
	}
	var logs []string
	analyzer := newProjectAnalyzer(s.rt.cfg, s.rt.agent.Client, analysisWorkspace, func(status string) {
		logs = append(logs, "status: "+status)
	}, func(debug string) {
		logs = append(logs, "debug: "+debug)
	})
	analyzer.analysisCfg = analysisCfg
	analyzer.explicitScope = explicitScope
	run, err := analyzer.Run(ctx, goal, mode)
	if err != nil {
		return strings.Join(limitStrings(logs, 20), "\n"), err
	}
	s.rt.session.LastAnalysis = &run.Summary
	s.rt.session.Summary = mergeSessionSummaryWithAnalysis(s.rt.session.Summary, run)
	s.rt.session.LastAnalysisContextQuery = ""
	s.rt.session.LastAnalysisContextRunID = run.Summary.RunID
	_ = s.rt.store.Save(s.rt.session)
	if docsManifest, ok := s.loadLatestManifest(); ok {
		_ = s.rt.recordLatestAnalysisDocsArtifacts(run, docsManifest, analysisCfg.OutputDir)
	}
	return s.renderAnalysisRunResult("Project analysis completed", run, analysisCfg, logs, mcpMaxChars(args, 30000)), nil
}

func (s *kernforgeMCPServer) toolFindRootCause(ctx context.Context, args map[string]any) (string, error) {
	if err := s.ensureProviderReady(); err != nil {
		return "", err
	}
	problem := strings.TrimSpace(stringValue(args, "problem"))
	if problem == "" {
		return "", fmt.Errorf("problem is required")
	}
	sourceHints := rootCauseWorkspaceSourceHints(s.rt.workspace.Root, 1600)
	clarity := analyzeRootCausePromptClarityWithSourceHints(problem, sourceHints)
	if !clarity.Clear {
		return renderRootCauseClarityForMCP(problem, clarity), nil
	}
	analysisCfg := rootCauseProjectAnalysisConfig(configProjectAnalysis(s.rt.cfg, s.rt.workspace.BaseRoot))
	if maxTotal := intValue(args, "max_total_shards", 0); maxTotal > 0 {
		analysisCfg.MaxTotalShards = maxTotal
	}
	if maxRefine := intValue(args, "max_refinement_shards", 0); maxRefine > 0 {
		analysisCfg.MaxRefinementShards = maxRefine
	}
	analysisCfg, err := s.rt.prepareAnalysisDirectorySelection(s.rt.workspace.Root, analysisCfg)
	if err != nil {
		return "", err
	}
	goal := buildRootCauseGoal(problem)
	var logs []string
	analyzer := newProjectAnalyzer(s.rt.cfg, s.rt.agent.Client, s.rt.workspace, func(status string) {
		logs = append(logs, "status: "+status)
	}, func(debug string) {
		logs = append(logs, "debug: "+debug)
	})
	analyzer.analysisCfg = analysisCfg
	analyzer.rootCausePatternPacks = append([]string(nil), stringSliceValue(args, "pattern_pack_paths")...)
	run, err := analyzer.Run(ctx, goal, "root-cause")
	if err != nil {
		return strings.Join(limitStrings(logs, 20), "\n"), err
	}
	s.rt.session.LastAnalysis = &run.Summary
	s.rt.session.Summary = mergeSessionSummaryWithAnalysis(s.rt.session.Summary, run)
	s.rt.session.LastAnalysisContextQuery = problem
	s.rt.session.LastAnalysisContextRunID = run.Summary.RunID
	_ = s.rt.store.Save(s.rt.session)
	return s.renderAnalysisRunResult("Root-cause analysis completed", run, analysisCfg, logs, mcpMaxChars(args, 30000)), nil
}

func (s *kernforgeMCPServer) prepareMCPAnalysis(mode string, goal string, paths []string, args map[string]any) (Workspace, ProjectAnalysisConfig, AnalysisGoalScope, error) {
	analysisCfg := configProjectAnalysis(s.rt.cfg, s.rt.workspace.BaseRoot)
	if maxTotal := intValue(args, "max_total_shards", 0); maxTotal > 0 {
		analysisCfg.MaxTotalShards = maxTotal
	}
	analysisWorkspace, analysisPaths, err := prepareExplicitAnalysisWorkspace(s.rt.workspace, paths)
	if err != nil {
		return analysisWorkspace, analysisCfg, AnalysisGoalScope{}, err
	}
	analysisCfg, err = s.rt.prepareAnalysisDirectorySelection(analysisWorkspace.Root, analysisCfg)
	if err != nil {
		return analysisWorkspace, analysisCfg, AnalysisGoalScope{}, err
	}
	analyzer := newProjectAnalyzer(s.rt.cfg, s.rt.agent.Client, analysisWorkspace, nil, nil)
	analyzer.analysisCfg = analysisCfg
	previewSnapshot, err := analyzer.scanProject()
	if err != nil {
		return analysisWorkspace, analysisCfg, AnalysisGoalScope{}, err
	}
	explicitScope, unmatchedPaths := resolveExplicitAnalysisScope(analysisPaths, previewSnapshot)
	if len(unmatchedPaths) > 0 {
		return analysisWorkspace, analysisCfg, AnalysisGoalScope{}, fmt.Errorf("analysis path not found in scanned workspace: %s", strings.Join(unmatchedPaths, ", "))
	}
	scope := deriveAnalysisScope(goal, explicitScope, previewSnapshot)
	if len(scope.DirectoryPrefixes) > 0 {
		_, scopedShards := deriveScopedAnalysisShardsForScope(scope, analyzer.planShards(previewSnapshot, analyzer.estimateShardCount(previewSnapshot, analyzer.estimateAgentCount(previewSnapshot))))
		if len(scopedShards) == 0 {
			return analysisWorkspace, analysisCfg, AnalysisGoalScope{}, fmt.Errorf("analysis scope matched no analyzable shards: %s", strings.Join(scope.DirectoryPrefixes, ", "))
		}
	}
	_ = mode
	return analysisWorkspace, analysisCfg, explicitScope, nil
}

func (s *kernforgeMCPServer) ensureProviderReady() error {
	s.rt.applyMCPProviderDefaultsFromSession()
	if strings.TrimSpace(s.rt.cfg.Provider) == "" || strings.TrimSpace(s.rt.cfg.Model) == "" {
		return fmt.Errorf("provider/model are not configured; set KernForge provider and model before using analysis tools")
	}
	if s.rt.agent != nil && s.rt.agent.Client != nil {
		return nil
	}
	client, err := NewProviderClient(s.rt.cfg)
	if err != nil {
		s.rt.clientErr = err
		return err
	}
	s.rt.clientErr = nil
	if s.rt.agent == nil {
		s.rt.agent = &Agent{}
	}
	s.rt.agent.Client = client
	s.rt.agent.Config = s.rt.cfg
	s.rt.agent.ModelRoutes = s.rt.modelRoutes
	return nil
}

func (s *kernforgeMCPServer) renderAnalysisRunResult(title string, run ProjectAnalysisRun, cfg ProjectAnalysisConfig, logs []string, maxChars int) string {
	payload := map[string]any{
		"run_id":              run.Summary.RunID,
		"goal":                run.Summary.Goal,
		"mode":                run.Summary.Mode,
		"status":              run.Summary.Status,
		"approved_shards":     run.Summary.ApprovedShards,
		"total_shards":        run.Summary.TotalShards,
		"review_failures":     run.Summary.ReviewFailures,
		"output_path":         run.Summary.OutputPath,
		"dashboard":           filepath.Join(cfg.OutputDir, "latest", "dashboard.html"),
		"docs_manifest":       filepath.Join(cfg.OutputDir, "latest", "docs_manifest.json"),
		"docs_dir":            filepath.Join(cfg.OutputDir, "latest", "docs"),
		"root_cause_audit_md": filepath.Join(cfg.OutputDir, "latest", "root_cause_audit.md"),
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n```json\n")
	b.Write(data)
	b.WriteString("\n```")
	if len(logs) > 0 {
		b.WriteString("\n\nRecent analysis events:\n")
		for _, line := range limitStrings(logs, 20) {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if strings.TrimSpace(run.FinalDocument) != "" {
		b.WriteString("\n\n")
		b.WriteString(run.FinalDocument)
	}
	return mcpLimitText(b.String(), maxChars)
}

func (s *kernforgeMCPServer) latestAnalysisSummary() map[string]any {
	cfg := configProjectAnalysis(s.rt.cfg, s.rt.workspace.BaseRoot)
	latestDir := filepath.Join(cfg.OutputDir, "latest")
	out := map[string]any{
		"latest_dir": latestDir,
		"dashboard":  filepath.Join(latestDir, "dashboard.html"),
	}
	if manifest, ok := s.loadLatestManifest(); ok {
		docs := make([]map[string]any, 0, len(manifest.Documents))
		for _, doc := range manifest.Documents {
			docs = append(docs, map[string]any{
				"name":       doc.Name,
				"title":      doc.Title,
				"kind":       doc.Kind,
				"path":       doc.Path,
				"confidence": doc.Confidence,
			})
		}
		out["manifest"] = map[string]any{
			"run_id":                    manifest.RunID,
			"goal":                      manifest.Goal,
			"mode":                      manifest.Mode,
			"generated_at":              manifest.GeneratedAt,
			"document_count":            manifest.DocumentCount,
			"documents":                 docs,
			"fuzz_target_count":         len(manifest.FuzzTargets),
			"verification_matrix_count": len(manifest.VerificationMatrix),
		}
	}
	runPath := filepath.Join(latestDir, "run.json")
	if run, ok := loadProjectAnalysisRunFile(runPath); ok {
		out["run"] = map[string]any{
			"run_id":          run.Summary.RunID,
			"goal":            run.Summary.Goal,
			"mode":            run.Summary.Mode,
			"status":          run.Summary.Status,
			"output_path":     run.Summary.OutputPath,
			"completed_at":    run.Summary.CompletedAt,
			"approved_shards": run.Summary.ApprovedShards,
			"total_shards":    run.Summary.TotalShards,
			"review_failures": run.Summary.ReviewFailures,
			"project_summary": run.KnowledgePack.ProjectSummary,
			"high_risk_files": limitStrings(run.KnowledgePack.HighRiskFiles, 12),
			"important_files": limitStrings(run.KnowledgePack.TopImportantFiles, 12),
			"project_types":   run.Snapshot.ProjectTypes,
			"total_files":     run.Snapshot.TotalFiles,
			"total_lines":     run.Snapshot.TotalLines,
		}
	}
	if len(out) == 2 {
		if _, err := os.Stat(latestDir); err != nil {
			return nil
		}
	}
	return out
}

func (s *kernforgeMCPServer) loadLatestManifest() (AnalysisDocsManifest, bool) {
	cfg := configProjectAnalysis(s.rt.cfg, s.rt.workspace.BaseRoot)
	paths := []string{
		filepath.Join(cfg.OutputDir, "latest", "docs_manifest.json"),
		filepath.Join(cfg.OutputDir, "latest", "docs", "manifest.json"),
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		manifest, err := decodeAnalysisDocsManifest(data)
		if err != nil {
			continue
		}
		if len(manifest.Documents) > 0 || len(manifest.VerificationMatrix) > 0 || len(manifest.FuzzTargets) > 0 {
			return manifest, true
		}
	}
	return AnalysisDocsManifest{}, false
}

func (s *kernforgeMCPServer) readLatestAnalysisDoc(document string, maxChars int) (string, error) {
	clean, err := safeAnalysisDocName(document)
	if err != nil {
		return "", err
	}
	cfg := configProjectAnalysis(s.rt.cfg, s.rt.workspace.BaseRoot)
	path := filepath.Join(cfg.OutputDir, "latest", "docs", clean)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := "# " + filepath.ToSlash(clean) + "\n\n" + string(data)
	return mcpLimitText(text, maxChars), nil
}

func (s *kernforgeMCPServer) readLatestAnalysisArtifactJSON(name string) (string, error) {
	clean, err := safeAnalysisDocName(name)
	if err != nil {
		return "", err
	}
	cfg := configProjectAnalysis(s.rt.cfg, s.rt.workspace.BaseRoot)
	path := filepath.Join(cfg.OutputDir, "latest", clean)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !json.Valid(data) {
		return "", fmt.Errorf("latest analysis artifact is not valid JSON: %s", clean)
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func safeAnalysisDocName(document string) (string, error) {
	trimmed := strings.TrimSpace(document)
	if trimmed == "" {
		return "", fmt.Errorf("document is required")
	}
	clean := filepath.Clean(filepath.FromSlash(trimmed))
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid analysis document path: %s", document)
	}
	return clean, nil
}

func loadProjectAnalysisRunFile(path string) (ProjectAnalysisRun, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ProjectAnalysisRun{}, false
	}
	run := ProjectAnalysisRun{}
	if err := json.Unmarshal(data, &run); err != nil {
		return ProjectAnalysisRun{}, false
	}
	return run, true
}

func renderRootCauseClarityForMCP(problem string, clarity rootCausePromptClarity) string {
	payload := map[string]any{
		"problem":           problem,
		"clear":             clarity.Clear,
		"unclear_parts":     clarity.UnclearParts,
		"suggested_command": clarity.SuggestedCommand,
		"model_checked":     clarity.ModelChecked,
		"model_reason":      clarity.ModelReason,
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return "Root-cause prompt needs more detail.\n\n```json\n" + string(data) + "\n```"
}

func (s *kernforgeMCPServer) workspaceRoot() string {
	return firstNonBlankString(s.rt.workspace.BaseRoot, s.rt.workspace.Root)
}

func mcpArgsObject(raw any) map[string]any {
	if raw == nil {
		return map[string]any{}
	}
	if obj, ok := raw.(map[string]any); ok {
		return obj
	}
	return map[string]any{}
}

func mcpObjectSchema(properties map[string]any, required ...string) map[string]any {
	properties = mcpSchemaWithWorkspaceHint(properties)
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func mcpSchemaWithWorkspaceHint(properties map[string]any) map[string]any {
	out := make(map[string]any, len(properties)+1)
	for key, value := range properties {
		out[key] = value
	}
	if _, exists := out["workspace"]; !exists {
		out["workspace"] = map[string]any{
			"type":        "string",
			"description": "Optional absolute workspace root hint. Pass the MCP client's current repo/workspace when it differs from the server launch cwd or when no initialize rootUri/workspaceFolders hint is provided.",
		}
	}
	return out
}

func mcpMaxChars(args map[string]any, fallback int) int {
	value := intValue(args, "max_chars", fallback)
	if value <= 0 {
		value = fallback
	}
	if value < 1000 {
		return 1000
	}
	if value > 120000 {
		return 120000
	}
	return value
}

func mcpSourceFuzzMaxChars(args map[string]any, fallback int) int {
	value := mcpMaxChars(args, fallback)
	if value < 30000 {
		return 30000
	}
	return value
}

func mcpLimitText(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	if maxChars <= 0 || len(text) <= maxChars {
		return text
	}
	if maxChars < 64 {
		maxChars = 64
	}
	return strings.TrimSpace(text[:maxChars]) + "\n\n... (truncated)"
}

func mcpDecodeURIComponent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return value
	}
	return decoded
}

func captureMCPRuntimeOutput(rt *runtimeState, fn func() error) (string, error) {
	if rt == nil {
		return "", fmt.Errorf("runtime is not configured")
	}
	var buf bytes.Buffer
	previous := rt.writer
	rt.writer = &buf
	defer func() {
		rt.writer = previous
	}()
	err := fn()
	return strings.TrimSpace(buf.String()), err
}
